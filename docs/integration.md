# Agent 接入指南

本页说明 Agent 宿主如何连接设备服务，覆盖两类宿主：

- **主机宿主**：运行在 PC（Windows/Linux/macOS）上的 Agent、脚本或 MCP 客户端；
- **手机内宿主**：运行在手机 chroot 内（或其他可访问手机回环地址）的 Agent 或 MCP 客户端。

工具字段、错误码与恢复语义见[接口契约](interface.md)；MCP 工具投影见 [MCP 入口](mcp.md)；
控制 App 的配对协议见[控制端协议](control-protocol.md)；手机侧进程、文件与故障处置见
[手机侧运维](phone-operations.md)。

## 快速接入

1. 主机执行 `adb devices` 与 `adb forward tcp:18881 tcp:18880`，确认 `/health` 返回
   `contract_version=2`；
2. 用 agent token 调 `POST /api/status`，确认 `can_start=true`；
3. 主机宿主：构建 `build/device-mcp.exe`，用私有 `.state/mcp-config.json` 配 stdio，或运行
   HTTP 适配器后连接 `http://127.0.0.1:18882/mcp`；直连则使用
   `http://127.0.0.1:18881/api/<tool>` 与 `X-Mobile-Use-Contract: 2`；
4. 手机内宿主：直接使用 `http://127.0.0.1:18880`；Android App 可只持有 MCP token，连接
   手机内 HTTP 适配器 `http://127.0.0.1:18882/mcp`。

## 1. 接入面与网络位置

```text
手机 Android
  ├─ 控制 App com.mobileuse.vnext ──/v1/control/*──┐
  ├─ 手机内 Agent（可选）────────/api/*────────────┤
  │                                                ▼
  └─ chroot：device-service 127.0.0.1:18880
        ▲
        │ adb forward tcp:18881 tcp:18880
        │
主机 Agent / device-mcp（stdio 或 Streamable HTTP）──/api/*
```

- 设备服务只监听手机回环地址 `127.0.0.1:18880`，不监听局域网或公网。
- 主机宿主通过 `adb forward` 使用 `127.0.0.1:18881`；手机内进程直接使用 `127.0.0.1:18880`。
- 控制身份、Agent 身份与 MCP HTTP 身份是三份不同凭据，不能互换。
- 一个部署只有一个 Agent 身份（默认 `local-agent`）。多个宿主同时使用同一凭据时仍共享
  该身份与同一设备工作位置；服务不按宿主拆分会话权威。

## 2. 身份与凭据

| 凭据 | 位置 | 用途 |
| --- | --- | --- |
| `agent_token` | 服务私有 `config.json` 的 `tokens.agent`；部署机 `.state/deploy.json` | `/api/*` 普通工具 |
| `control_token` | 服务 `tokens.control`；App 私有 `files/connection.json` | `/v1/control/*` 控制同步与 STOP |
| MCP HTTP `token` | 私有 `mcp-config.json` 的 `http.token` | MCP Streamable HTTP 入口认证 |

- 三者必须不同；MCP 配置会拒绝 `http.token` 与 `agent_token` 相同。
- 凭据不进入工具参数、MCP 正文、日志、错误或仓库；`config.json` 为 `0600`，App
  `connection.json` 为 App UID 私有。
- 手机内 Agent 需要读取私有配置或由运维显式提供受限凭据；默认部署不向 Android 第三方
  应用下发 Agent 令牌。

## 3. 选择接入方式

| 方式 | 适用 | 传输 | 身份 |
| --- | --- | --- | --- |
| 直连 HTTP `/api/<tool>` | 自写 Agent/脚本 | HTTP JSON，单请求 | agent token + 契约头 |
| MCP stdio | 本地 MCP 客户端/桌面宿主 | 宿主拉起子进程，stdin/stdout | 配置内 agent token |
| MCP Streamable HTTP | 已支持 HTTP MCP 的宿主 | `POST/GET`，`/mcp` | Bearer MCP token + agent token |

三种方式最终都调用同一套服务端工具；MCP 适配器不保存第二份会话、不直接访问 ADB 或账本。

## 4. 主机宿主接入

### 4.1 前置检查

```powershell
adb devices
adb forward tcp:18881 tcp:18880
curl.exe -s http://127.0.0.1:18881/health
```

`/health` 返回 `contract_version=2` 与 `release_version`。再用 agent token 查询准入：

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
Invoke-RestMethod -Uri http://127.0.0.1:18881/api/status -Method Post `
  -Headers @{ Authorization = "Bearer $($deploy.agent_token)"; "X-Mobile-Use-Contract" = "2" } `
  -ContentType 'application/json' -Body '{}'
```

`can_start=true` 才可开始普通工作；`/health` 正常、端口监听或进程存在都不是准入证明。

### 4.2 直连 HTTP

所有工具使用 `POST /api/<tool>`，JSON 对象，必须携带：

```text
Authorization: Bearer <agent_token>
X-Mobile-Use-Contract: 2
Content-Type: application/json; charset=utf-8
```

工作工具在发送前分配操作号，可用 `X-Mobile-Use-Operation: P...` 预分配；未预分配时服务
生成。直连 HTTP 建议**始终预分配 P**：响应丢失后仍可用 `status(P)` 查询原事实；没有 P
的服务端生成操作只能按 `status()` 恢复自身唯一活动启动 P，无法直接引用本次普通工作。
示例：

```powershell
$base = 'http://127.0.0.1:18881'
$headers = @{
    Authorization = "Bearer $($deploy.agent_token)"
    'X-Mobile-Use-Contract' = '2'
    'X-Mobile-Use-Operation' = 'Pdemo-start-1'
}
Invoke-RestMethod -Uri "$base/api/start" -Method Post -Headers $headers `
  -ContentType 'application/json' -Body '{}'
```

- 不自动重试、不跟随重定向、不排队；同一 P 重复到达只返回原操作视图。
- 传输超时且已持有 P 时，用 `POST /api/status {"reference":"P..."}` 查询原事实，不重发动作。
- 设备同时只有一项普通工作；冲突立即 `409 device_busy`，应串行化调用而不是并发重试。
- 普通契约不匹配返回 `409 contract_mismatch`；`/v1/control/stop` 不受该版本检查影响。

### 4.3 MCP stdio

构造私有配置 `.state/mcp-config.json`（`0600`，不入库）。可运行
`./scripts/verify-m5.ps1` 生成/刷新该配置，同时构建适配器并验证两种传输：

```json
{
  "service": {
    "base_url": "http://127.0.0.1:18881",
    "agent_token": "<agent_token>"
  },
  "transport": "stdio",
  "http": {
    "listen": "127.0.0.1:18882",
    "token": "<独立 MCP token>",
    "allowed_origins": ["http://127.0.0.1:3000"]
  }
}
```

宿主只引用二进制与配置路径，不把 token 写进宿主配置文件：

```json
{
  "mcpServers": {
    "mobile-use-vnext": {
      "command": "E:\\Code\\Python\\mobile-use-vnext\\build\\device-mcp.exe",
      "args": [
        "-transport", "stdio",
        "-config", "E:\\Code\\Python\\mobile-use-vnext\\.state\\mcp-config.json"
      ]
    }
  }
}
```

不同宿主的顶层字段名可能不同（`mcpServers`/`command`/`args` 等），语义相同。启动后宿主应
发现固定 12 个工具；升级后必须重启实际宿主进程重新 `tools/list`，不能只替换磁盘文件。

手工运行：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go build -o build/device-mcp.exe ./cmd/device-mcp
./build/device-mcp.exe -transport stdio -config .state/mcp-config.json
```

stdout 只承载 MCP 协议帧，诊断写 stderr。

### 4.4 MCP Streamable HTTP

```powershell
./build/device-mcp.exe -transport http -config .state/mcp-config.json
```

宿主配置：

```text
URL:    http://127.0.0.1:18882/mcp
Header: Authorization: Bearer <http.token>
```

- 缺失或错误 bearer 返回 `401`。
- 请求携带 `Origin` 时必须与 `allowed_origins` 精确匹配，否则返回 `403`。
- 会话按请求隔离（stateless）；每次请求都重新认证，不能继承其他客户端的 MCP 会话。
- 适配器只监听 `127.0.0.1:18882`；不要把它改成局域网地址或反向代理到公网。

### 4.5 多宿主

多个宿主可以共享同一 agent 凭据，但服务只有一个设备工作位置：

- 普通工作冲突立即 `409 device_busy`，不建队列；
- 同一 P 不会被派发第二次；
- 一个宿主的 `STOP`/`end` 会撤销设备上的原会话，即使其他宿主仍持有旧 O/P；
- 需要多个并发 Agent 时，应由运维层串行化，不要依赖服务排队。

## 5. 手机内宿主接入

### 5.1 直连 HTTP

手机内进程（chroot 内或同网络命名空间）直接使用：

```text
http://127.0.0.1:18880
```

不需要 `adb forward`。其余请求头、契约版本、P/恢复语义与主机接入相同。

### 5.2 凭据边界

服务私有配置位于 chroot `/opt/mobile-use-vnext/config.json`（`0600`，root）。手机内 Agent
只有在能够读取该配置（例如以 root 运行于 chroot）时，才能直接取得 `agent_token`。

默认部署不为 Android 第三方 App 提供 Agent 凭据下发通道；不要把 root 私有配置复制到
`/sdcard`、共享目录或其他应用可读路径。非 root 的手机内 Agent 需要由运维显式提供该
agent token 的私有副本（仅其 UID 可读），并接受同一 Agent 身份与单工作位置约束。

### 5.3 手机内 MCP 适配器

整组发布包含 `device-mcp` 产物，但默认部署只安装设备服务、固定制品与 `service.sh`。要在
手机内运行 MCP 适配器：

1. 使用 linux/arm64 构建：

   ```bash
   GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
     go build -trimpath -o build/device-mcp ./cmd/device-mcp
   ```

2. 由运维把 `device-mcp` 放入 chroot（例如 `/opt/mobile-use-vnext/device-mcp`，`0755`），
   并使用手机内私有配置：

   ```json
   {
     "service": {"base_url": "http://127.0.0.1:18880", "agent_token": "<agent_token>"},
     "transport": "stdio",
     "http": {"listen": "127.0.0.1:18882", "token": "<独立 token>", "allowed_origins": []}
   }
   ```

3. `stdio` 由手机内宿主拉起；`http` 监听手机回环 `127.0.0.1:18882/mcp`。不要监听
   `0.0.0.0`，也不要在 chroot 外再启动第二个同端口进程。

手机内适配器与主机适配器共用同一工具目录与投影；版本升级后同样需要重启实际进程。

### 5.4 Android 侧 MCP 宿主

手机内 Android 应用（Termux、Operit 或其他支持 Streamable HTTP 的 MCP 客户端）不必持有
Agent token，可以只连接手机回环上的 MCP 适配器：

1. 在 chroot 内启动 HTTP 适配器，配置
   `service.base_url=http://127.0.0.1:18880`、`http.listen=127.0.0.1:18882`、
   `http.token=<独立 MCP token>`；
2. Android 宿主配置 `http://127.0.0.1:18882/mcp`，请求携带
   `Authorization: Bearer <http.token>`；
3. 适配器以 chroot/root 权限读取 Agent 凭据；Android 应用只持有受限的 MCP token，不接触
   `agent_token` 或控制凭据。

注意：

- Android 宿主必须允许本机明文 HTTP；`usesCleartextTraffic`/network security config 由宿主
  决定，服务端不提供 TLS、反向代理或公网入口。
- 适配器不由设备服务托管，需要 `nohup ... &` 或运维进程管理器保持运行。它不保存会话；
  重启后宿主重新 `tools/list`，会话权威仍在设备服务。
- 同一时间只启动一个监听 `127.0.0.1:18882` 的进程；升级后重启该进程，不能只替换磁盘文件。
- 只支持 stdio 的 Android 宿主需要自行提供能拉起 chroot 进程且持有私有配置的封装；默认
  部署不提供该封装，由运维显式实现并承担进程生命周期。

### 5.5 生命周期与电源

- 长期运行的手机内 Agent 应自行管理其线程/网络生命周期，不依赖控制 App 的每秒同步。
- 控制 App 的前台服务与部分唤醒锁只保证控制同步和停止通道；不会替 Agent 续租会话、
  延长 O 期限或绕过锁屏门禁。
- Doze、锁屏与熄屏由 App 上报，服务按 `device_not_interactive`/`device_locked` 拒绝普通工作；
  不要用第二个唤醒锁或后台白名单绕过。
- 手机内 MCP 适配器不保存会话；宿主重启后重新 `tools/list` 并从服务事实恢复。

## 6. 启动顺序与升级

推荐顺序：

1. 确认 chroot 与 Android 侧 ADB 可用；
2. 启动设备服务（`/opt/mobile-use-vnext/service.sh start`）；
3. 启动控制 App 前台服务；
4. 主机建立 `adb forward tcp:18881 tcp:18880`；
5. 检查 `/health` 与 `can_start`；
6. 启动 MCP 适配器或 Agent；
7. 宿主完成 `server/discover` 与 `tools/list`。

升级后：

- 服务、App、MCP 适配器与客户端共同使用新的 `contract_version`；
- 重新建立端口转发（如已失效）；
- 重启实际 MCP 宿主进程，重新发现工具目录；
- 不恢复旧会话、不重放旧操作；主屏共享需要用户重新确认。

## 7. 错误与恢复映射

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| `401 unauthorized` | Agent/MCP/控制凭据错误 | 核对对应身份令牌；三者不能互换 |
| `403 forbidden` | MCP HTTP Origin 不在允许列表 | 精确加入 `allowed_origins`，不要放宽为任意来源 |
| `409 contract_mismatch` | 普通契约版本不一致 | 整组更新服务/App/客户端，不要只升一侧 |
| `409 device_busy` | 已有普通工作 | 读 `blocking_operation_id`，串行化后重试 |
| `409 observation_expired`/`target_changed` | O 过期或目标变化 | 重新 `observe`，不要重放旧动作 |
| `503 delivery_unavailable` | 慢交付额度不足 | 等待既有结果交付后再试，不排队 |
| `504 action_result_uncertain` | 输入已派发但结果未知 | `status(P)`/重新观察判断现状，不重放 |
| `409 session_not_found`/`operation_not_found` | 引用不属于当前身份或已淘汰 | 重新建立会话，不猜测旧引用 |
| 传输超时且持有 P | 服务事实可能已提交 | `status(P)` 查询；不重发动作 |
| 传输超时且未持有 P | 只读查询未完成，或服务已生成 P 但客户端未知 | 只读查询可重新查询；工作调用不重发，先用 `status()` 恢复自身唯一活动启动 P/会话，或重新观察 |

独立 STOP 不受普通契约影响；`accepted=true` 只表示停止边界已绑定，物理清理仍看
`status.cleanup` 与 App 呈现。

## 8. 最小示例

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
$headers = @{
    Authorization = "Bearer $($deploy.agent_token)"
    'X-Mobile-Use-Contract' = '2'
}
$start = Invoke-RestMethod http://127.0.0.1:18881/api/start -Method Post `
  -Headers $headers -ContentType 'application/json' -Body '{}'
$oid = $start.observation.id
Invoke-RestMethod http://127.0.0.1:18881/api/observe -Method Post -Headers $headers `
  -ContentType 'application/json' -Body (@{ reference = $oid } | ConvertTo-Json)
Invoke-RestMethod http://127.0.0.1:18881/api/end -Method Post -Headers $headers `
  -ContentType 'application/json' -Body (@{ reference = $start.operation_id } | ConvertTo-Json)
```

## 9. 安全边界

- 设备服务与 MCP HTTP 只允许回环地址；不要做端口映射、反向代理或 `adb reverse` 暴露。
- `agent_token`、`control_token`、MCP token 不进入仓库、日志、聊天或宿主云端配置。
- 手机内接入不改变身份模型：同一部署仍是同一 Agent 身份与同一设备工作位置。
- 控制 App 的 STOP 与本地停止始终优先；接入方不能用配置或文案扩大授权。
- MCP stdout 只承载协议，诊断写 stderr；不要把日志混入协议流。
