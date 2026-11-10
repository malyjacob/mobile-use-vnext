# Agent 接入指南

本页说明 Agent 宿主如何连接设备服务，覆盖两类宿主：

- **主机宿主**：运行在 PC（Windows/Linux/macOS）上的 Agent、脚本或 MCP 客户端；
- **手机内宿主**：Android 应用、Termux、chroot 内进程等运行在手机上的 Agent 或 MCP 客户端。

工具字段、错误码与恢复语义见[接口契约](interface.md)；MCP 工具投影见 [MCP 入口](mcp.md)；
控制 App 的配对协议见[控制端协议](control-protocol.md)；手机侧进程、文件与故障处置见
[手机侧运维](phone-operations.md)。

## 快速接入

默认部署会自动完成以下四件事：

```text
device-service        手机 127.0.0.1:18880
device-mcp           手机 127.0.0.1:18882（HTTP MCP 适配器）
控制 App              与设备服务配对
adb forward          主机 18881 → 服务；主机 18883 → 手机 MCP
```

因此：

1. `./scripts/deploy.ps1` 部署后，主机执行
   `adb forward tcp:18883 tcp:18882`（部署已自动建立）；
2. 取手机 MCP token（只读取，不打印）：

   ```powershell
   $mcpToken = (Get-Content .state/deploy.json -Raw | ConvertFrom-Json).mcp_token
   ```

3. 主机 MCP 客户端填：

   ```text
   URL:    http://127.0.0.1:18883/mcp
   Header: Authorization: Bearer <mcpToken>
   ```

4. 手机内 Android 宿主直接填 `http://127.0.0.1:18882/mcp` 与同一个 MCP token；不需要
   `adb forward`，也不需要 Agent token。

只需要直连 HTTP 的 Agent 则使用 `http://127.0.0.1:18881/api/<tool>`、`agent_token` 与
`X-Mobile-Use-Contract: 5`。

## 1. 默认部署后的网络与进程

```text
手机 Android
  ├─ 控制 App com.mobileuse.vnext ──/v1/control/*──┐
  ├─ 手机内 MCP 宿主 ──HTTP /mcp──────────────────┤
  │                                                ▼
  ├─ device-mcp 127.0.0.1:18882 ──/api/*──> device-service 127.0.0.1:18880
  │                                                ▲
  │                                                │
  └─ 主机 adb forward ─────────────────────────────┘
       18881 → 18880（直连 HTTP）
       18883 → 18882（手机 MCP）
```

| 端点 | 位置 | 用途 |
| --- | --- | --- |
| `127.0.0.1:18880` | 手机 | 设备服务；Agent 工具与健康检查 |
| `127.0.0.1:18882/mcp` | 手机 | 手机内 MCP 适配器；Android/手机内宿主使用 |
| `127.0.0.1:18881` | 主机转发 | 主机直连 HTTP |
| `127.0.0.1:18883/mcp` | 主机转发 | 主机 MCP 客户端使用手机适配器 |
| `127.0.0.1:18882/mcp` | 主机（可选） | 主机自建 MCP 适配器时的默认监听 |

- 手机侧只监听回环；不要做端口映射、反向代理或 `adb reverse` 暴露到局域网/公网。
- 主机的 `18882` 与手机的 `18882` 是两个命名空间；手机适配器默认转发到主机 `18883`，
  以免与主机自建适配器冲突。

## 2. 三个 token 到底分别是什么

默认部署有三个独立凭据，**用途、存放位置和客户端配置都不同**：

| 凭据 | 谁生成 | 存放在哪里 | 谁读取 | 用途 |
| --- | --- | --- | --- | --- |
| `agent_token` | `deploy.ps1` | 部署机 `.state/deploy.json`；chroot `/opt/mobile-use-vnext/config.json` 的 `tokens.agent`；适配器配置的 `service.agent_token` | 设备服务、MCP 适配器、直连 HTTP 的 Agent | `/api/*` 普通工具认证 |
| `control_token` | `deploy.ps1` | 部署机 `.state/deploy.json`；服务 `config.json` 的 `tokens.control`；App 私有 `files/connection.json` | 设备服务与控制 App | `/v1/control/*` 控制同步与 STOP |
| `mcp_token` | `deploy.ps1` | 部署机 `.state/deploy.json` 的 `mcp_token`；chroot `/opt/mobile-use-vnext/mcp-config.json` 的 `http.token` | 手机 MCP 适配器与 MCP 客户端 | 手机 MCP HTTP 入口的 Bearer 认证 |

关键区别：

- **`http.token` 不是服务端 token，也不是 `agent_token`。** 它是 MCP 适配器自己的 HTTP
  Bearer 口令，只写在适配器配置的 `http.token`，以及 MCP 客户端的
  `Authorization: Bearer ...` 请求头里。设备服务的 `config.json` 里没有 `http.token`。
- 手机适配器的 `http.token` 就是部署机 `.state/deploy.json` 的 `mcp_token`。部署时由
  `deploy.ps1` 生成，并通过私有配置写入 chroot。
- 主机自建 MCP 适配器时，使用 `.state/mcp-config.json` 里的 `http.token`；它与手机
  `mcp_token` 可以不同，二者是两个独立监听器/入口。
- 三个 token 必须互不相同。适配器配置会拒绝 `http.token` 与 `service.agent_token` 相同。
- 客户端永远不需要同时持有两个 MCP token：用手机适配器就只填 `mcp_token`，用主机适配器
  就只填主机配置里的 `http.token`。

读取 token 时不要打印到终端历史或日志：

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
$mcpToken = $deploy.mcp_token       # 手机 MCP
$agentToken = $deploy.agent_token   # 直连 HTTP / 主机适配器内部使用
```

如果需要生成一个自己的 MCP token（例如主机适配器）：

```powershell
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$bytes = New-Object byte[] 32
$rng.GetBytes($bytes)
$rng.Dispose()
$hostMcpToken = ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
```

不要把生成的 token 回显到屏幕或提交到仓库。

## 3. 选择哪条接入路径

| 场景 | 使用路径 | 客户端需要什么 |
| --- | --- | --- |
| PC 上的 MCP 客户端（Claude Desktop、Operit、其他 HTTP MCP 客户端） | **默认：手机适配器** `18883/mcp` | 手机 `mcp_token` |
| PC 上的 MCP 客户端只支持 stdio | 主机适配器（stdio） | `.state/mcp-config.json`（内含 agent token） |
| PC 上的普通 Agent/脚本 | 直连 HTTP `18881/api/*` | `agent_token` + 契约头 |
| PC 上想自己跑 HTTP MCP 适配器 | 主机适配器（HTTP，`18882/mcp`） | 主机配置的 `http.token` |
| Android App / Termux / 手机内 MCP | **默认：手机适配器** `18882/mcp` | 手机 `mcp_token` |
| chroot 内 Agent | 直连 `18880/api/*` 或手机适配器 | `agent_token` 或 `mcp_token` |

默认部署已经把手机适配器装好并启动；不需要再手工构建或复制。只有选择主机适配器（stdio
或主机 HTTP）时才需要 `build/device-mcp.exe` 和 `.state/mcp-config.json`。

## 4. 路径 A：主机 MCP 客户端 → 手机适配器（推荐）

### 4.1 确认部署与转发

```powershell
adb devices
adb forward tcp:18883 tcp:18882
curl.exe -s -o NUL -w "%{http_code}" http://127.0.0.1:18883/mcp
```

最后一条应输出 `401`：说明手机适配器在监听，且没有 bearer 时拒绝请求。`405` 只有在
带 token 使用 `GET` 时才出现；MCP 客户端应使用 POST，不要用 GET 判断可用性。

### 4.2 取 MCP token

```powershell
$mcpToken = (Get-Content .state/deploy.json -Raw | ConvertFrom-Json).mcp_token
if (-not $mcpToken) { throw 'mcp_token is missing; redeploy with the MCP adapter enabled' }
```

不要把 `$mcpToken` 打印出来，只把它填进宿主配置或宿主的凭据存储。

### 4.3 配置宿主

不同 MCP 客户端的字段名不同，但值只有两个：

```text
URL:    http://127.0.0.1:18883/mcp
Header: Authorization: Bearer <mcp_token>
```

例如通用 JSON 形状：

```json
{
  "mcpServers": {
    "mobile-use-vnext": {
      "url": "http://127.0.0.1:18883/mcp",
      "headers": {
        "Authorization": "Bearer <mcp_token>"
      }
    }
  }
}
```

如果宿主把 HTTP MCP 配置成 `transport: "http"` + `serverUrl`/`endpoint`，填同一个 URL 和
同一个 Authorization 头即可；具体字段以宿主文档为准。

### 4.4 重启宿主并确认工具目录

1. 完全退出并重启 MCP 客户端/宿主进程；
2. 执行 `server/discover` 与 `tools/list`；
3. 应看到固定 12 个工具：`start`、`status`、`observe`、`act`、`inspect`、`keepalive`、
   `end`、`apps`、`open_app`、`notify`、`shell`、`diagnose`。

只替换手机上的二进制或重启 service 进程，不会让已连接的宿主自动刷新工具；必须重启宿主进程/
连接。MCP 适配器本身不保存会话，重启后重新发现即可。

### 4.5 结果模式

MCP 适配器对每次工具调用提供 `both`、`text`、`structured` 三种结果模式。模式只选择表示
形式；三种模式承载同一份事实与同一张图片，不用于脱敏或截断：

| 模式 | `content` | `structuredContent` | 适用 |
| --- | --- | --- | --- |
| `both`（默认） | 完整 `result v1` 文本与图片 | 完整 `Result` JSON | 兼容默认；同时需要两种形式 |
| `text` | 完整 `result v1` 文本与图片 | 不发送 | 只读取文本通道的宿主 |
| `structured` | 图片；`isError=true` 时附最小 `diagnostic v1` 文本 | 完整 `Result` JSON | 会把结构化结果交给模型或程序的宿主 |

- `both` 同时发送两份完整编码，传输与上下文占用更大；宿主明确只消费一种形式时使用对应
  单向模式。
- `text` 完整文本以 `result v1 is_error=<true|false>` 开头、`end result` 结束，不产生
  `omitted` 块；缺少终止行表示文本被宿主截断，应改用 `structured` 或通过 `inspect` 的
  node/query 收窄。
- `structured` 成功且无图时 `content=[]`；不接受空 content 的宿主使用 `both` 或 `text`。
  错误附带的 `diagnostic v1` 以 `diagnostic v1 is_error=true` 开头、`end diagnostic`
  结束，只用于解释，不代表完整结果。
- 模式不改变工具目录、入参 schema、认证、归属或 `/api/*` 契约；不发布 `outputSchema`。

选择方式：

- **进程默认（stdio 与 HTTP 进程均适用）**：显式 `-result-mode` >
  `MOBILE_USE_MCP_RESULT_MODE` > `both`。
  值只接受 `both`、`text`、`structured`；显式空值、前后空白、大小写变体或非法值启动失败，
  退出码为 `2`，不回退到环境变量。
- **HTTP 每请求**：`X-Mobile-Use-Result-Mode: both|text|structured` 覆盖进程默认。该头必须
  恰好一个精确值；空值、多值、逗号、前后空白与大小写变体在工具执行前返回 `400`。无此头
  时使用默认。
- 该头只在适配器内使用，不发送给设备服务，也不影响认证、会话归属或 STOP。

```powershell
./build/device-mcp.exe -transport stdio -config .state/mcp-config.json -result-mode text
$env:MOBILE_USE_MCP_RESULT_MODE = 'structured'
./build/device-mcp.exe -transport stdio -config .state/mcp-config.json
Remove-Item Env:MOBILE_USE_MCP_RESULT_MODE
```

HTTP 宿主在原有 `Authorization` 头之外按需添加：

```json
{
  "mcpServers": {
    "mobile-use-vnext": {
      "url": "http://127.0.0.1:18883/mcp",
      "headers": {
        "Authorization": "Bearer <mcp_token>",
        "X-Mobile-Use-Result-Mode": "text"
      }
    }
  }
}
```

结果模式不是 `mcp-config.json` 的成员；适配器配置是严格 JSON，加入 `result_mode` 等未知
成员会被拒绝。默认部署启动的手机适配器使用 `both`，且部署参数中没有单独的结果模式选项；
客户端用每请求头覆盖。若要在该进程上改变启动默认值，需按其启动方式传入
`-result-mode` 或在进程环境中设置 `MOBILE_USE_MCP_RESULT_MODE`，然后重启适配器。逐块
语法、取消保留键与图片规则见 [MCP 入口](mcp.md)。

### 4.6 Origin 限制

只有浏览器/Electron 等会发送 `Origin` 的客户端才需要配置允许来源。默认手机适配器的
`allowed_origins` 为空数组，带 `Origin` 的请求会被拒绝（403）。

需要放行时，部署时显式传入精确来源：

```powershell
./scripts/deploy.ps1 -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'
```

不要填 `*`；比较是精确匹配。改完来源后需要重新部署/重启适配器，并重启宿主。

## 5. 路径 B：主机直连 HTTP

所有工具使用 `POST /api/<tool>`，必须携带：

```text
Authorization: Bearer <agent_token>
X-Mobile-Use-Contract: 5
Content-Type: application/json; charset=utf-8
```

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
$headers = @{
    Authorization           = "Bearer $($deploy.agent_token)"
    'X-Mobile-Use-Contract' = '5'
}
Invoke-RestMethod http://127.0.0.1:18881/api/status -Method Post `
  -Headers $headers -ContentType 'application/json' -Body '{}'
```

- `can_start=true` 才可开始普通工作；`/health` 正常、端口监听或进程存在都不是准入证明。
- 工作工具建议始终预分配 `X-Mobile-Use-Operation: P...`；响应丢失后可用 `status(P)` 查询
  原事实。没有 P 的服务端生成操作只能按 `status()` 恢复自身唯一活动启动 P。
- 不自动重试、不跟随重定向、不排队；同一 P 重复到达只返回原操作视图。
- `409 device_busy` 表示已有普通工作，应串行化调用，不要并发重试。

## 6. 路径 C：主机自建 MCP 适配器

只有需要 stdio，或希望在主机本地监听 MCP 时才使用。手机适配器已经满足 HTTP MCP 场景时，
不必再起第二个适配器。

### 6.1 构建

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go build -o build/device-mcp.exe ./cmd/device-mcp
```

### 6.2 创建主机适配器配置

`.state/mcp-config.json` 是**主机适配器**的私有配置，不是手机上的 `mcp-config.json`：

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$bytes = New-Object byte[] 32
$rng.GetBytes($bytes)
$rng.Dispose()
$hostMcpToken = ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
$config = [ordered]@{
    service   = [ordered]@{
        base_url    = 'http://127.0.0.1:18881'
        agent_token = $deploy.agent_token
    }
    transport = 'stdio'
    http      = [ordered]@{
        listen          = '127.0.0.1:18882'
        token           = $hostMcpToken
        allowed_origins = @()
    }
}
$json = $config | ConvertTo-Json -Depth 5
[System.IO.File]::WriteAllText(
    (Join-Path (Get-Location) '.state/mcp-config.json'),
    $json,
    (New-Object System.Text.UTF8Encoding($false)))
```

字段含义：

| 字段 | 主机适配器取值 | 说明 |
| --- | --- | --- |
| `service.base_url` | `http://127.0.0.1:18881` | 主机经 adb forward 访问服务 |
| `service.agent_token` | `.state/deploy.json` 的 `agent_token` | 适配器内部调用 `/api/*` |
| `transport` | `stdio` 或 `http` | 可被 `-transport` 命令行覆盖 |
| `http.listen` | `127.0.0.1:18882` | 仅 HTTP 模式使用；只允许回环 |
| `http.token` | 上面生成的 `hostMcpToken` | MCP HTTP Bearer；不得等于 agent token |
| `http.allowed_origins` | `[]` 或精确 Origin | 仅浏览器类客户端需要 |

### 6.3 stdio 宿主配置

```json
{
  "mcpServers": {
    "mobile-use-vnext": {
      "command": "E:/Code/Python/mobile-use-vnext/build/device-mcp.exe",
      "args": [
        "-transport", "stdio",
        "-config", "E:/Code/Python/mobile-use-vnext/.state/mcp-config.json"
      ]
    }
  }
}
```

宿主只引用二进制和配置路径，不把 token 写进宿主文件。重启宿主后应看到 12 个工具。
只读文本的 stdio 宿主在 `args` 中追加 `"-result-mode", "text"`；结构化宿主可用
`structured`；不写时默认 `both`。结果模式不是 JSON 配置成员，选择规则见第 4.5 节。

### 6.4 主机 HTTP 适配器

```powershell
./build/device-mcp.exe -transport http -config .state/mcp-config.json
```

```text
URL:    http://127.0.0.1:18882/mcp
Header: Authorization: Bearer <hostMcpToken>
```

需要非默认模式时，在请求头中加 `X-Mobile-Use-Result-Mode: text|structured`；该头按请求
生效，不写入配置（见第 4.5 节）。

主机 HTTP 适配器使用主机 `18882`；手机适配器已转发到主机 `18883`，两者不冲突。不要把
手机 `18882` 再转发到主机 `18882`，否则主机适配器无法绑定该端口。

### 6.5 与手机适配器的区别

| 项 | 手机适配器（默认） | 主机适配器（可选） |
| --- | --- | --- |
| 进程位置 | chroot `/opt/mobile-use-vnext/device-mcp` | 主机 `build/device-mcp.exe` |
| 服务地址 | `127.0.0.1:18880` | `127.0.0.1:18881`（adb forward） |
| HTTP 入口 | 手机 `18882/mcp`；主机转发 `18883/mcp` | 主机 `18882/mcp` |
| Token | `.state/deploy.json` 的 `mcp_token` | `.state/mcp-config.json` 的 `http.token` |
| stdio | Android 宿主需自行提供拉起封装 | 宿主直接拉起 `device-mcp.exe` |
| 结果模式 | 默认 `both`；HTTP 每请求头覆盖 | 默认 `both`；启动参数/环境变量与每请求头 |
| 适用 | 主机/手机 HTTP MCP 客户端 | 只支持 stdio 的宿主，或主机本地 HTTP |

## 7. 路径 D：手机内宿主

### 7.1 Android 应用 / Termux（只持有 MCP token）

默认手机适配器已经启动，Android 应用直接配置：

```text
URL:    http://127.0.0.1:18882/mcp
Header: Authorization: Bearer <mcp_token>
可选:   X-Mobile-Use-Result-Mode: text|structured（默认 both）
```

- 不需要 `agent_token`，也不需要 `control_token`；适配器在 chroot 内读取服务凭据。
- 不需要 `adb forward`；`18882` 是手机回环。
- Android 宿主必须允许本机明文 HTTP；`usesCleartextTraffic`/network security config
  由宿主应用决定，服务端不提供 TLS 或公网入口。
- 如果宿主需要 `Origin`，部署时用 `-McpAllowedOrigins` 放行精确来源并重启适配器。

### 7.2 chroot 内 Agent

两种选择：

```text
直连 HTTP:  http://127.0.0.1:18880/api/<tool>   + agent_token + X-Mobile-Use-Contract: 5
MCP HTTP:   http://127.0.0.1:18882/mcp          + mcp_token
```

直连 HTTP 需要能读取 chroot `/opt/mobile-use-vnext/config.json`（root，`0600`），或由运维
提供该 agent token 的私有副本（仅该 UID 可读）。不要把 root 配置复制到 `/sdcard`、共享
目录或其他应用可读路径。

### 7.3 手机内 stdio

手机内 stdio 需要宿主能拉起一个持有私有配置的进程。默认部署不提供这种封装；只支持
stdio 的 Android 宿主应改用手机适配器的 HTTP 入口，或由运维自行实现拉起封装。

### 7.4 生命周期与电源

- 默认手机适配器由部署启动，不由 `service.sh` 托管；手机系统启动后若部署未执行，按第 9 节
  启动该适配器。
- 控制 App 的前台服务与唤醒锁只保证控制同步和停止通道，不替 Agent 续租会话或延长 O。
- Doze、锁屏与熄屏由 App 上报；服务按 `device_not_interactive`/`device_locked` 拒绝普通
  工作，不要用第二个唤醒锁或后台白名单绕过。

## 8. 手机适配器的生命周期

部署会自动安装并启动：

```text
/opt/mobile-use-vnext/device-mcp          二进制，0755
/opt/mobile-use-vnext/mcp-config.json     手机适配器配置，0600
/var/lib/mobile-use-vnext/device-mcp.pid  进程号
/var/lib/mobile-use-vnext/device-mcp.log  适配器日志（stdout/stderr）
```

`./scripts/deploy.ps1 -SkipPhoneMcp` 只用于显式跳过手机适配器；这时部署不完整，手机内
HTTP MCP 不可用，主机只能走直连 HTTP 或自建适配器。

发布升级后，如果部署脚本没有刚启动手机适配器，手工启动：

```powershell
adb shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "nohup setsid /opt/mobile-use-vnext/device-mcp -transport http -config /opt/mobile-use-vnext/mcp-config.json >> /var/lib/mobile-use-vnext/device-mcp.log 2>&1 < /dev/null &"'
```

停止：

```powershell
adb shell su -c 'pkill -f "^/opt/mobile-use-vnext/device-mcp " || true'
```

查看：

```powershell
adb shell 'su -c "pgrep -f \"^/opt/mobile-use-vnext/device-mcp \""'
adb shell su -c 'tail -n 50 /data/local/chroots/debian13/var/lib/mobile-use-vnext/device-mcp.log'
```

- 同一时间只启动一个监听手机 `18882` 的进程；端口冲突会出现在适配器日志中。
- 适配器无状态；重启后客户端重新 `tools/list`，不会丢失设备服务的会话事实。
- 部署升级会停止旧适配器、覆盖二进制与配置并启动新适配器；宿主仍需重启以刷新工具目录。

## 9. 启动顺序、升级与回退

部署脚本的正常顺序（已自动执行）：

1. 启动设备服务并等待 `/health`；
2. 安装并启动手机 MCP 适配器，确认 `/mcp` 无 token 返回 `401`；
3. 启动控制 App；
4. 建立主机转发 `18881→18880`、`18883→18882`；
5. 等待当前能力与最多 5 秒的旧逻辑交接完成并取得 `can_start=true`，然后报告完成。

手工启动后的顺序：

1. 确认 chroot 与 Android 侧 adb 可用；
2. `service.sh start`；
3. 启动控制 App 前台服务；
4. 建立 `adb forward tcp:18881 tcp:18880` 与 `tcp:18883 tcp:18882`；
5. 启动手机 MCP 适配器（第 8 节）；
6. 检查 `/health`、`can_start` 与 `18883/mcp` 的 `401`；
7. 重启/连接实际 MCP 客户端，重新 `tools/list`。

升级后：

- 服务、App、适配器与客户端共同使用新的 `contract_version`；
- 重新建立可能失效的转发；
- 重启实际 MCP 宿主进程，不能只替换磁盘文件；
- 旧会话转退休记录，不恢复旧 O/P 或主屏授权；旧 cleanup 的 `pending`/`unconfirmed` 可继续
  作为原会话事实存在，但不成为新会话的永久准入理由。

回退是另一轮受控部署，不自动回放旧操作；见[发布与回退](release.md)。

## 10. 配置字段速查

适配器配置（手机和主机同 schema）：

```json
{
  "service": {
    "base_url": "http://127.0.0.1:18880",
    "agent_token": "<agent_token>"
  },
  "transport": "http",
  "http": {
    "listen": "127.0.0.1:18882",
    "token": "<mcp_token>",
    "allowed_origins": []
  }
}
```

| 字段 | 手机适配器 | 主机适配器 | 规则 |
| --- | --- | --- | --- |
| `service.base_url` | `http://127.0.0.1:18880` | `http://127.0.0.1:18881` | 必须 loopback |
| `service.agent_token` | 部署生成 | 部署生成，写入主机配置 | 必填，不打印 |
| `transport` | `http` | `stdio` 或 `http` | 被 `-transport` 覆盖 |
| `http.listen` | `127.0.0.1:18882` | `127.0.0.1:18882` | HTTP 模式必填，loopback |
| `http.token` | `mcp_token` | 主机自生成 | 必填，不得等于 agent token |
| `http.allowed_origins` | `[]` 或部署参数 | `[]` 或精确 Origin | 仅浏览器客户端需要 |

配置是严格 JSON：未知成员、重复键、类型错误会被拒绝。手机适配器配置由部署生成，
通常不需要手改；主机适配器配置由运维创建。

结果模式不属于适配器 JSON 配置：`result_mode` 等成员会被严格 JSON 拒绝。启动默认值用
`-result-mode` 或 `MOBILE_USE_MCP_RESULT_MODE`；HTTP 每请求用
`X-Mobile-Use-Result-Mode` 覆盖。见第 4.5 节。

## 11. 常见困惑

**Q：`http.token` 要写进设备服务的 `config.json` 吗？**
不需要。设备服务 `config.json` 只有 `tokens.agent` 与 `tokens.control`。`http.token` 属于
MCP 适配器配置，是 MCP HTTP 入口的 Bearer 口令；手机适配器的值就是 `mcp_token`。

**Q：手机 `/opt/mobile-use-vnext/mcp-config.json` 和主机 `.state/mcp-config.json` 是同一份吗？**
不是。前者由部署生成，服务地址是手机 `18880`，token 是 `mcp_token`；后者是主机自建适配器
的可选配置，服务地址是主机 `18881`，token 是主机自己的 `http.token`。两者不要混用。

**Q：客户端应该连 `18882` 还是 `18883`？**
主机 MCP 客户端连 `http://127.0.0.1:18883/mcp`（`adb forward` 到手机 `18882`）；手机内
Android 宿主连 `http://127.0.0.1:18882/mcp`。主机自建 HTTP 适配器才使用主机 `18882`。

**Q：为什么 `GET /mcp` 带 token 返回 405？**
这是正常的：MCP Streamable HTTP 用 POST。无 token 的 `401` 用来确认适配器在监听；不要用
GET 判断工具是否可用。

**Q：轮换 token 后客户端一直 401？**
手机路径要重新读取 `.state/deploy.json` 的 `mcp_token` 并重启客户端；主机适配器要重新读取
`.state/mcp-config.json` 的 `http.token` 并重启适配器/客户端。旧 token 不会继续有效。

**Q：为什么 `text` 模式没有 `structuredContent`，或 `structured` 模式没有文本？**
这是模式定义，不是缺少字段。`text` 只发送完整文本与图片；`structured` 发送完整结构化结果
与图片，成功且无图时 `content=[]`，错误时只附最小诊断文本。两种模式承载同一份事实；需要
同时取得两份完整编码时使用 `both`。

## 12. 错误与恢复映射

| 现象 | 含义 | 处理 |
| --- | --- | --- |
| `/mcp` 无 token 返回 `401` | 正常：适配器在监听且要求认证 | 带 `Authorization: Bearer <mcp_token>` |
| `/mcp` 带 token 用 GET 返回 `405` | 正常：MCP 客户端应使用 POST | 用 MCP 客户端连接，不用 GET 判断 |
| 带 token 仍 `401` | token 填错，或用了主机/手机另一侧的 token | 手机用 `mcp_token`；主机适配器用其 `http.token` |
| `403 forbidden` | `Origin` 不在 `allowed_origins` | 精确加入来源并重启适配器 |
| `400 invalid result mode` | 模式头不是单个精确的 `both`/`text`/`structured` | 去掉多值、逗号、前后空白与大小写变体；不设置则使用默认 |
| 适配器启动即退出 | 端口被占用、配置错误或二进制不匹配 | 看 `device-mcp.log`；确认手机 `18882` 只有一个进程 |
| `409 contract_mismatch` | 普通契约版本不一致 | 整组更新服务/App/客户端，不要只升一侧 |
| `409 device_busy` | 已有普通工作 | 读 `blocking_operation_id`，串行化后重试 |
| `409 observation_expired`/`target_changed` | O 过期或目标变化 | 重新 `observe`，不要重放旧动作 |
| `503 delivery_unavailable` | 慢交付额度不足 | 等待既有结果交付后再试，不排队 |
| `504 action_result_uncertain` | 输入已派发但结果未知 | `status(P)`/重新观察，不重放 |
| `409 session_not_found`/`operation_not_found` | 引用不属于当前身份或已淘汰 | 重新建立会话，不猜测旧引用 |
| `409 operation_unknown` | 原 P 已不可定位 | `status` 无法证明过去未执行；`end(P)` 会封闭其未来接纳，结果保持 `uncertain`，不得改结束当前其它会话 |
| 传输超时且持有 P | 服务事实可能已提交 | `status(P)` 查询；不重发动作 |
| 传输超时且未持有 P | 只读查询未完成，或服务已生成 P 但客户端未知 | 只读可重查；工作不重发，先用 `status()` 恢复启动 P/会话 |

独立 STOP 不受普通契约影响；`accepted=true` 只表示停止边界已绑定，物理清理仍看与该原会话
同帧的 `status.cleanup` 与 App 历史。`can_start=true` 不要求旧 cleanup 已全部 `confirmed`。

## 13. 最小示例

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
$headers = @{
    Authorization           = "Bearer $($deploy.agent_token)"
    'X-Mobile-Use-Contract' = '5'
}
$start = Invoke-RestMethod http://127.0.0.1:18881/api/start -Method Post `
  -Headers $headers -ContentType 'application/json' -Body '{}'
$oid = $start.observation.id
Invoke-RestMethod http://127.0.0.1:18881/api/observe -Method Post -Headers $headers `
  -ContentType 'application/json' -Body (@{ reference = $oid } | ConvertTo-Json)
Invoke-RestMethod http://127.0.0.1:18881/api/end -Method Post -Headers $headers `
  -ContentType 'application/json' -Body (@{ reference = $start.operation_id } | ConvertTo-Json)
```

## 14. 安全边界

- 设备服务、手机适配器与主机适配器都只允许回环地址；不做端口映射、反向代理或
  `adb reverse` 暴露。
- `agent_token`、`control_token`、`mcp_token`/`http.token` 不进入仓库、日志、聊天或宿主
  云端配置；主机适配器配置含 agent token，尤其要私有保存。
- Android 应用默认只拿 MCP token，不接触 agent/control 凭据；不要为了省事把 root 配置
  复制到共享目录。
- 控制 App 的 STOP 与本地停止始终优先；接入方不能用配置或文案扩大授权。
- MCP stdout 只承载协议，诊断写 stderr；不要把日志混入协议流。
