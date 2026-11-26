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
`X-Mobile-Use-Contract: 33`。

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
- 手机适配器监听手机 `18882`。部署通过 `adb forward tcp:18883 tcp:18882` 将主机
  `18883` 的连接转发到手机 `18882`，避免占用主机自建适配器的端口。

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
3. 应看到固定 13 个工具：`start`、`status`、`observe`、`snap`、`wait_for`、`act`、`inspect`、
   `keepalive`、`end`、`app`、`notify`、`shell`、`diagnose`。`status`、`inspect`、
   `diagnose` 和 `snap` 发布 `annotations.readOnlyHint=true`。

MCP 的 `act` schema 含 `scroll` 分支（`container`/`direction`/`amount`，`amount` 为闭合四档
`step`/`half`/`page`/`full`，省略为 `page`）与 `swipe` 的可选 `duration_ms`（`scroll` 不接受
该时长，注入时长由服务端按档位固定）；`text` 分支另含 `auto_submit`（默认 `true`）与
`submit_ref`（默认 `"enter_key"`，或满足 `^n[1-9][0-9]*$` 且不超过 64 个字符的公开节点编号）。
`scroll` 的容器轴向与请求方向不兼容时自动回退兼容外层滚动祖先，无解返回
`422 unsupported_node_action`（`reason=axis_mismatch`）。观察质量与滚动
效果的字段和投影见[接口契约](interface.md)与 [MCP 入口](mcp.md)，本页不重复滚动判据。

只替换手机上的二进制或重启 service 进程，不会让已连接的宿主自动刷新工具；必须重启宿主进程/
连接。MCP 适配器本身不保存会话，重启后重新发现即可。

MCP 服务在 `server/discover` 与兼容 `initialize` 响应中发布同一份 server instructions，
包括完整树图例及会话、O/P、引用和恢复等跨工具规则。宿主应将该说明提供给模型。说明同时
给出 `result v4` 外壳、`obs` 单行、`app_items`/`app_tasks` 块与 `recovery ref=`
的语法。各工具描述提供本工具的关键分流和恢复提示，`start` 也简述 O/P 与 full/delta。
用 `base` 指向的 full 和本次 delta 重建当前树。基线池保存 1 条当前基线与至多 4 条备用 full，
均要求采集龄小于 180 秒；当前基线可覆盖跨度 1–12，其他备用 full 覆盖跨度 1–10。客户端必须
保留所有可能被引用的完整树，当前基线可在跨度 1–12 内被选为 base，不能按修订数提前淘汰仍
可能被引用的 full，并逐条按 `base` 应用。

### 4.5 结果模式

MCP 适配器对每次工具调用提供 `both`、`text`、`structured` 三种结果模式。模式只选择表示
形式；三种模式承载同一份事实与同一张图片，不用于脱敏或截断：

| 模式 | `content` | `structuredContent` | 适用 |
| --- | --- | --- | --- |
| `both` | 紧凑 `result v4` 文本与图片 | 完整 `Result` JSON | 显式选择；同时需要两种形式 |
| `text`（默认） | 紧凑 `result v4` 文本与图片 | 不发送 | 默认模式；只读取文本通道的宿主 |
| `structured` | 图片；`isError=true` 时附最小 `diagnostic v1` 文本 | 完整 `Result` JSON | 会把结构化结果交给模型或程序的宿主 |

`text`/`both` 的文本是面向调用方的紧凑投影。文本省略整个 `observation.quality` 和
`effect`，以及 `observation.origin`、`observation.revision`、`observation.actionable_until`、
`session.agent`、`inspection.origin`、默认 JPEG `media_type`、日期与纳秒部分。
`/api/*` 与 `both`/`structured` 的
`structuredContent` 完整无损保留 `Result` JSON，含全部审计字段、三态与空集合语义。需要
精确机器字段时使用 `structured` 或 `both`，逐项规则见 [MCP 入口](mcp.md)。

- `both` 同时发送文本与结构化两种表示，传输与上下文占用更大；宿主明确只消费一种形式时使用
  对应单向模式。
- `text` 文本以 `result v4 <ok|err> [op=<P>] [state=…] [outcome=…] [phase=…]` 开头、
  `end result` 结束；常规成功省略 `outcome=executed`，错误结果、`not_executed`、
  `uncertain`、`partial` 与带 `state=` 的重放摘要显式输出 `outcome=…`。缺少终止行表示文本
  被宿主截断，应改用 `structured` 或通过 `inspect` 的 node/query 收窄。
- `structured` 成功且无图时 `content=[]`；不接受空 content 的宿主使用 `both` 或 `text`。
  错误附带的 `diagnostic v1` 以 `diagnostic v1 is_error=true` 开头、`end diagnostic`
  结束，只用于解释，不代表完整结果。
- 模式不改变工具目录、入参 schema、认证、归属或 `/api/*` 契约；不发布 `outputSchema`。

`text` 中每次观察写成单行，`obs ` 之后首个不含 `=` 的字段是完整 `O`：

```text
obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [visual=…] [change=…] [hint=…] [screen=<W>x<H>[@<display_id>]] [rot=…] [missing=…]
```

结构化结果中该编号仍是 `observation.id`。`visual` 仅在观察带该字段时出现，取值为
`loading`/`image_heavy(<pct>%)`/`media_surface`/`sparse_tree`（判据见
[接口契约](interface.md#observation-与-quality)）；`full` 观察输出本次分类，`delta` 中
`loading` 不省略、其余三类仅在同会话、同显示代次的分类变化时输出，`structured` 的
`observation.visual` 使用同一值。`change` 映射 `observation.tree_change`，只在 `act` 常规
成功时出现（`unchanged`/`detail_only`/`matched_base`；常规内容变化时省略，`unchanged` 不
要求本次差分为空）；`hint` 映射 `observation.hint`
（`snap`/`wait_for`）。调用方按 `hint` 分流：`hint=snap` 用同一 O 调 `snap` 核对画面（只读，
不消费也不发布 O）；`hint=wait_for` 用 `wait_for` 等条件或稳定；`change=matched_base` 表示
前后树内容或公开语境有变、只是差分零差异命中历史基线，不是动作没生效。可见 O/P 形如
`O4821_72` 与 `P7391_12`：
`O`/`P` 后接 4 位十进制实例标签（`1000`–`9999`，无前导零）、`_` 与单调序号，与 `/api/*`
使用同一编号。`start` 首帧的会话屏 `display_id` 从 `obs` 行的
`screen=<W>x<H>@<display_id>` 读取；`snap` 的 `image` 与 `app(list)` 的 `display=` 也给出
同一编号。O 的输入期限默认约 60 秒并受会话租期截断，文本省略该字段；精确截止时间见
`structured`，过期以服务端反馈为准。

`recovery` 行写作 `recovery entry=<entry> ref=<精确 O/P|O|P|none> [input=…] [cause=…]`：
能确定性证明时 `ref=` 直接给出可抄用的真实编号，抽象词 `O`/`P` 只在无法证明安全时出现；
`structured` 中的 `recovery.reference` 仍是 `"O"`/`"P"`/`"none"`。`outcome=partial` 的首次
结果在附带有效新 ready O 时省略 `recovery`；重复 P、`status(P)` 或交付失败时，在会话仍活动
且无实时阻塞时给出 `observe(P, retired)`，撤权或实时阻塞按 `recovery` 的覆盖规则处理。

普通观察树由 `tree v5` 标识，完整块头只写 `nodes`，差分块头写 `base` 与 `changes`。普通
节点几何在 `root` 与 `scroll` 上为四元 `l,t,r,b`，其他角色为二元 `x,y` 中心点；`~` 行的
`bounds` 按同一角色规则使用 `[x,y]` 或 `[l,t,r,b]`，`hidden`/`null` 分别表示按策略省略与
未知。compact 投影还会执行固定净体（露边细条、`input` 重复标签、无标注装饰图、祖先复合标签
覆盖的被动文本叶、隐藏匿名容器扁平化、无字可点击容器的文本提升与 `resource` 短尾名），并
对有动作、非 `input`、非 `context` 节点中超过 `250` 个 Unicode 码点的 `label` 段前置截取
前 `250` 个码点、在 `resource=` 之后写 `truncated=true`；未超过的段不截断、不置位，无动作
正文与 `input.value` 保留全文，截断不追加
省略号。`base`
指向基线池中的已确认 full，要求同一会话 origin、同一目标显示 ID/代次及采集龄 `< 180s`，
池中保存 1 条当前基线与至多 4 条备用 full。当前基线可用跨度 1–12，其他备用 full 为 1–10；
备用 full 成功选回后，从下一次发布起成为当前基线。服务拿每份 full 自己的节点编号重新匹配
同一采集，再按实际 `tree v5` 普通树块计量 UTF-8 字节。只有
`10×deltaBytes < 8×candidateFullBytes` 的候选合格，取最短
delta，平手优先当前基线、再取较新 revision。指向同一个完整 base 的多条差分分别应用到该整树，
不能叠加在上一份 delta 上。`base=_72` 是文本读树简写，不能作为后续工具调用的引用。
`observe(P)` 与 `full=true` 强制 full；`inspect(base)` 在基线仍在池内时可回看。需要操作当前
观察时使用 `obs` 行的完整 O，需要看历史树时使用完整历史 O。

`inspect` 继续使用 `inspect v3` 的两格缩进、方括号矩形和显式动作词，始终交付四元
`[l,t,r,b]` 与完整未截断的 label，不输出节点级 `truncated`。`tree v5` 的默认动作、
`@` 同几何引用（矩形或中心点）与短 base 不适用于补看块。
未知或无法无损表达的字段继续在文本中使用绝对 JSON Pointer 的原值兜底；解析时按块头
`tree v5`、`inspect v3` 分派，不要混用两种语法。

选择方式：

- **进程默认（stdio 与 HTTP 进程均适用）**：显式 `-result-mode` >
  `MOBILE_USE_MCP_RESULT_MODE` > `text`。
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
成员会被拒绝。默认部署启动的手机适配器使用 `text`，且部署参数中没有单独的结果模式选项；
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
X-Mobile-Use-Contract: 33
Content-Type: application/json; charset=utf-8
```

```powershell
$deploy = Get-Content .state/deploy.json -Raw | ConvertFrom-Json
$headers = @{
    Authorization           = "Bearer $($deploy.agent_token)"
    'X-Mobile-Use-Contract' = '33'
}
Invoke-RestMethod http://127.0.0.1:18881/api/status -Method Post `
  -Headers $headers -ContentType 'application/json' -Body '{}'
```

- `can_start=true` 才可开始普通工作；`/health` 正常、端口监听或进程存在都不是准入证明。
- 工作工具建议始终预分配 `X-Mobile-Use-Operation: P...`；响应丢失后可用 `status(P)` 查询
  原事实。没有 P 的服务端生成操作只能按 `status()` 恢复自身唯一活动启动 P。
- 不自动重试、不跟随重定向、不排队；同一 P 重复到达只返回原操作视图。
- `409 device_busy` 表示已有普通工作，应串行化调用，不要并发重试。

### 5.1 `app` 接入示例

`app` 必须是严格 JSON：根对象只允许 `kind` 与另外八个已声明顶层成员（共九个成员），未知字段、重复键、
大小写变体、显式 `null` 与尾随第二个 JSON 值都会被 `422 invalid_request` 拒绝，请求体上限
8 KiB。七个 kind 都接受全部已声明顶层成员，但只有该分支的生效成员进入服务端请求；冗余成员
只做类型检查后忽略。

```json
{"kind":"find","query":"设置"}
{"kind":"list","reference":"O4821_72"}
{"kind":"list","reference":"P7391_12","scope":"primary","app":"com.example.target"}
{"kind":"open","reference":"O4821_72","target":{"app":"com.example.target"}}
{"kind":"claim","reference":"O4821_72","target":{"task_id":42}}
{"kind":"handoff","reference":"O4821_72","target":{"task_id":42},"restore_foreground":true}
{"kind":"close","reference":"O4821_72","target":{"app":"com.example.target"}}
{"kind":"kill","reference":"O4821_72","target":{"task_id":42}}
{"kind":"kill","reference":"O4821_72","target":{"app":"com.example.target"}}
```

生效 `target` 的 `task_id` 与 `app` 互斥（`find`/`list` 的 `target` 不生效，只做嵌套键与类型检查）；`open` 只接受 `target.app`，`kill` 接受 `target.task_id`
或 `target.app`，`claim`/`handoff`/`close` 接受两者之一。`find`/`list` 是查询，成功或失败
都不返回 `outcome`；其余五个 kind 成功返回 `outcome=executed` 与原会话屏的一次 full 无图
观察。`find` 使用 detached P；`list` 的 P 绑定 `reference` 解析到的原会话。每次新调用都
使用自己的新 P；重复 P 只返回原摘要。`kill` 按从任务清单解析并核验的 Android 用户对整个
包执行一次 `force-stop`；包入口需要全设备同包 live 任务均属本会话，或凭 `close` 留下的
关闭资格进入。`act`、`inspect` 与 `app` 的引用字段都是 `reference`；旧 `observation_id`
按未知成员拒绝。

`act` 的常见省略形式与显式关闭：

```json
{"reference":"O4821_72","operation":{"type":"tap","target":{"ref":"n1"}}}
{"reference":"O4821_72","operation":{"type":"text","target":{"ref":"n1"},"text":"查询"}}
{"reference":"O4821_72","operation":{"type":"tap","target":{"x":100,"y":200}}}
{"reference":"O4821_72","operation":{"type":"tap","target":{"ref":"n1"},"bubble":false}}
{"reference":"O4821_72","operation":{"type":"text","target":{"ref":"n1"},"text":"草稿","auto_submit":false}}
{"reference":"O4821_72","operation":{"type":"text","target":{"ref":"n1"},"text":"周杰伦","submit_ref":"n7"}}
```

ref 点按/长按默认 `bubble=true`，原目标无 `tap`/`long_press`/`text` 动作时先在目标视觉区内尝试最近交互祖先提升，遇竞争控件止漂并自动吸附唯一局部共位按钮；`scroll` 支持同视口滚动栈直传、非滚动节点自动祖先提升、容器轴向不兼容时回退兼容外层滚动祖先，以及坐标最内层/回退/消歧；遇静态目标解析或规划 `422`（`invalid_target`/`unsupported_node_action`/`ambiguous_point`/`no_safe_point`/`no_safe_trajectory`）时，若 `error.details.candidates` 非空，取首个 `executable=true` 的 `ref` 用同一 O 与新 P 重试（无候选或无可执行项时按错误原因另行处理）。坐标目标不传 `bubble`。`text` 的 ref 目标只要求 `role=input`、公开动作含 `text`、未禁用、`protected=false` 与正面积几何，不要求 `tap`。`text` 默认 `auto_submit=true`、`submit_ref="enter_key"`，一次调用已包含必要聚焦（已聚焦 ref 走零点击快路径，未聚焦时在目标子树内规划唯一安全落点并至多点击一次）、全文写入、写后精确确认（刷新值相等即完成；否则至多 3 次只读读回，退避 `40ms`/`80ms`/`150ms`、墙钟上限 `1000ms`，几何变化时至多一次严格写后重绑定）和至多一次 ENTER，不需要先 tap 或另发 ENTER；草稿、多行、逐字段填写或清空但不提交时显式 `auto_submit=false`（此时 `submit_ref` 只做语法校验后忽略）。`submit_ref="n<ref>"` 表示写后点击该提交按钮：认领前按完整 tap 规划校验，写前灰置也接受，写后重验启用状态与安全落点，失败不补发 ENTER。复合 text 结果携带 `text_input.write/submit`；`submit=uncertain` 先观察页面，不原样重发。`write=executed` 且提交确定未派发时 `outcome=partial`、`error.code=text_submit_not_executed`
（期间 STOP 撤权为 `control_revoked`），首次结果通常附带新的 ready O；用 `obs` 行取该 O
继续，需要时再读回框值，不重放原文字。text 失败时读取 `error.details.stage/reason/dispatch` 区分未派发、目标不唯一、读回不等与提交问题，不要解析 `message`；写入或提交仍 uncertain 且会话活动时按 `recovery entry=observe ref=<P>`（`structured` 中为 `recovery.entry`）用新 P 获取现场，而不是循环 `status(P)`。

P 的两种用法：`observe({"reference":"P7391_4"})` 的 `reference` 引用本 Agent 已有的
`P7391_4` 原操作，不是本次 `observe` 的请求体 P；本次调用仍使用自己的新 P。
`status(P7391_4)` 只返回操作摘要，不重新采集，也不补回业务集合。

### 5.2 `wait_for` 条件等待示例

外层“先等几秒再 `observe`”的轮询把每一帧都变成新 O 与一次完整往返。已知界面会在数秒内就绪
时，改用一次服务端条件等待：

```json
{"reference":"O4821_72","absent_query":"加载中","timeout":15}
{"reference":"O4821_72","query":"完成","stable":true,"timeout":20}
{"reference":"O4821_72","timeout":5,"image":true}
```

```powershell
$wait = Invoke-RestMethod http://127.0.0.1:18881/api/wait_for -Method Post `
  -Headers $headers -ContentType 'application/json' `
  -Body '{"reference":"O4821_72","query":"完成","timeout":20}'
$wait.wait.trigger      # query / absent_query / stable / timeout
$wait.wait.matched      # ["query"]；没有可判定项时该成员缺失
$wait.wait.elapsed_ms   # 自准入完成到终帧发布的耗时
$newOid = $wait.observation.id
```

- `timeout` 必填、1–40 秒，`stable=true` 时至少 2。到时不是错误：只要验证采集成功，结果仍带
  新观察与 `wait.trigger=timeout`，`wait.matched` 至少包含 `timeout`；终帧若同时命中
  `query`/`absent_query`，对应项也按固定顺序列出，调用方按 `observation` 继续即可。
- 条件或稳定判据满足后服务立即返回，不跑满 `timeout`；等待期不发布中间帧，只有最终验证采集
  发布一个新 O。`query`/`absent_query` 只匹配当前公开可见树，视口外内容不参与。
- `stable=true` 要求连续 3 帧内容摘要一致（至少跨越 1.2 秒）。摘要忽略节点坐标、ref
  与无动作叶子的文本，因此倒计时或实时进度可以继续变化。页面内容条件仍可用
  `query` 或 `absent_query`，它们匹配完整公开树。
- 宿主侧工具超时必须覆盖验证采集：该阶段整体受 35 秒固定预留截止约束，带图与无图调用都按
  `timeout <= host_timeout - 35s` 预留。宿主默认 60 秒时，`timeout` 取 25 秒以内；否则宿主
  会取消本次调用。
- 取消语义：STOP 与 `end` 会释放工作位并把 `409 control_revoked` 交付给调用方；MCP 取消与
  客户端断连同样立即关闭等待并释放工作位，但由适配器（有界 `end(P)`，`call_cancelled`）或
  传输层收尾。取消不证明设备侧已撤回，也不保证本次等待产出观察。
- 等待期独占唯一普通工作位：同会话的 `observe`、`snap`、`act`、`app`、`shell` 立即返回
  `409 device_busy`。已知很慢的流程用一次较长 `timeout`，不要并发试探。

### 5.3 `snap` 立即看当前画面

只需要确认弹窗遮挡、画布内容或页面布局时，不必为了取图先读树：

```powershell
$snap = Invoke-RestMethod http://127.0.0.1:18881/api/snap -Method Post `
  -Headers $headers -ContentType 'application/json' -Body '{"reference":"O4821_72"}'
$snap.operation_id       # 本次 P
$snap.image.width        # 输出 JPEG 像素尺寸
$snap.image.screen       # 准入时会话绑定显示的几何快照
[IO.File]::WriteAllBytes("snap.jpg", [Convert]::FromBase64String($snap.image.base64))
```

- `snap` 只读当前屏的一张图，不读树、不发布观察、不续租、不认领 O；原 O 仍按自己的
  `actionable_until` 与 `act` 实时重验使用。MCP `text` 通道把这次结果写作单行
  `image <W>x<H> read_only screen=<W>x<H>[@<display_id>] [at=<HH:MM:SS>]`，`display_id` 即当前会话屏。
- 成功结果只有 `operation_id` 与顶层 `image`；`image.base64` 是 JPEG 字节，元数据与树/图片
  附加材料同形，并固定 `read_only=true` 与 `screen`。
- 重复使用同一 P、或对 `snap` 的 P 调 `status(P)`，只返回无图操作摘要，不重读设备。
- 需要同时取新树和图时仍用 `observe(image=true)`；需要在条件满足后取图时用
  `wait_for(image=true)`。
- 失败是顶层 `error`：`display_unavailable`、`stale_screen`、`image_capture_failed`、
  `image_stale`、`control_revoked` 等；请求取消只结束本次读取，不撤销会话。

## 6. 路径 C：主机自建 MCP 适配器

只有需要 stdio，或希望在主机本地监听 MCP 时才使用。手机适配器已经满足 HTTP MCP 场景时，
不必再起第二个适配器。

### 6.1 构建

```powershell
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

脚本在 Windows/amd64 下构建 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，并写
`build/host-tools-manifest.json`；构建路径默认拒绝脏工作树，对脏树使用 `-AllowDirtySource`
只用于开发构建且永远不能通过 `-VerifyOnly`；`-VerifyOnly` 要求干净树/HEAD 与完整的手机配套
清单。发布前按清单核验，不把 linux/arm64 产物改后缀当 exe。

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

宿主只引用二进制和配置路径，不把 token 写进宿主文件。重启宿主后应看到 13 个工具。
stdio 宿主不写 `-result-mode` 时默认 `text`；需要结构化结果时追加
`"-result-mode", "structured"`。结果模式不是 JSON 配置成员，选择规则见第 4.5 节。

### 6.4 主机 HTTP 适配器

```powershell
./build/device-mcp.exe -transport http -config .state/mcp-config.json
```

```text
URL:    http://127.0.0.1:18882/mcp
Header: Authorization: Bearer <hostMcpToken>
```

需要显式指定模式时，在请求头中加 `X-Mobile-Use-Result-Mode: both|text|structured`；该头
按请求生效，不写入配置（见第 4.5 节）。

主机 HTTP 适配器使用主机 `18882`，访问手机适配器使用主机 `18883`。主机 `18883`
转发到手机 `18882`，主机 `18882` 可供本机适配器绑定。

### 6.5 与手机适配器的区别

| 项 | 手机适配器（默认） | 主机适配器（可选） |
| --- | --- | --- |
| 进程位置 | chroot `/opt/mobile-use-vnext/device-mcp` | 主机 `build/device-mcp.exe` |
| 服务地址 | `127.0.0.1:18880` | `127.0.0.1:18881`（adb forward） |
| HTTP 入口 | 手机 `18882/mcp`；主机转发 `18883/mcp` | 主机 `18882/mcp` |
| Token | `.state/deploy.json` 的 `mcp_token` | `.state/mcp-config.json` 的 `http.token` |
| stdio | Android 宿主需自行提供拉起封装 | 宿主直接拉起 `device-mcp.exe` |
| 结果模式 | 默认 `text`；HTTP 每请求头覆盖 | 默认 `text`；启动参数/环境变量与每请求头 |
| 适用 | 主机/手机 HTTP MCP 客户端 | 只支持 stdio 的宿主，或主机本地 HTTP |

## 7. 路径 D：手机内宿主

### 7.1 Android 应用 / Termux（只持有 MCP token）

默认手机适配器已经启动，Android 应用直接配置：

```text
URL:    http://127.0.0.1:18882/mcp
Header: Authorization: Bearer <mcp_token>
可选:   X-Mobile-Use-Result-Mode: both|text|structured（默认 text）
```

- 不需要 `agent_token`，也不需要 `control_token`；适配器在 chroot 内读取服务凭据。
- 不需要 `adb forward`；`18882` 是手机回环。
- Android 宿主必须允许本机明文 HTTP；`usesCleartextTraffic`/network security config
  由宿主应用决定，服务端不提供 TLS 或公网入口。
- 如果宿主需要 `Origin`，部署时用 `-McpAllowedOrigins` 放行精确来源并重启适配器。

### 7.2 chroot 内 Agent

两种选择：

```text
直连 HTTP:  http://127.0.0.1:18880/api/<tool>   + agent_token + X-Mobile-Use-Contract: 33
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

- 服务、App、适配器与客户端共同使用同一个 `contract_version`（当前 `33`）；
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
这是模式定义，不是缺少字段。`text` 只发送紧凑文本与图片；`structured` 发送完整结构化结果
与图片，成功且无图时 `content=[]`，错误时只附最小诊断文本。两者承载同一份事实，文本省略
的固定元信息（如 `observation.origin`、`observation.actionable_until`）仍保留在
`structuredContent`；需要同时取得两种形式时使用 `both`。

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
| `409 task_target_ambiguous` | `claim`/`handoff`/`close` 按包名匹配到多个任务 | 读 `details.task_ids`，用精确 `task_id` 加新 P 重试；不取第一个 |
| `409 close_refused_borrowed` | 关闭目标含借入窗口且 `allow_borrowed=false` | 先 `handoff` 该窗口，或显式传 `allow_borrowed=true` 并接受销毁 |
| `409 kill_refused_*` / `409 root_unavailable` | 无本会话 owned 任务或有效关闭资格、同包任务并非全部 owned、他屏仍有同包任务、FGS/播放门禁，或 root 不可用 | 按受控原因处理，不绕过身份与播放检查；`close` 确认关闭 owned 任务后可在 5 分钟内按包请求 `kill`，同包多窗口不再单独拒绝 |
| `409 task_lifecycle_busy` | 该 task 仍有未确认的旧搬移/删除责任 | 用 `status` 与 `app.list` 核验现状；只限制该任务，稍后新 P 重试 |
| `503 app_list_unavailable` | 应用清单读取失败 | 条件恢复后用新 P 重新 `app.find`，不伪造空列表 |
| `504 app_launch_unconfirmed` | `app.open` 已派发启动但归属未确认 | 按 `recovery entry=observe`、`reference=P` 用 `observe`/`app.list` 核验（仅传输超时且未知操作结果时先用 `status(P)` 确认是否已完成）；不重放启动 |
| `409 observation_expired`/`target_changed` | O 过期或目标变化 | `target_changed` 的重验失败结果通常已带新观察：用它的 O 加新 P 重试；没有 `observation` 时再 `observe`。`reason=lineage_changed` 且结果返回 `error.details.changed_ref` 时，它指向第一个确实缺失、能唯一确定的具体公开祖先（仅平移的祖先不会被指名）；`bubble_ancestor` 的最近交互祖先变化时指向原先的交互祖先，即使它仍存在；不要据此重放旧动作 |
| `409 text_submit_not_executed` | 复合 text 写入已确认、提交确定未派发（`outcome=partial`） | 结果通常已附带新的 ready O，直接用它继续；没有新 O 时按 `recovery entry=observe` 获取现场；不要重放原文字，也不要自动补发 ENTER |
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
    'X-Mobile-Use-Contract' = '33'
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
