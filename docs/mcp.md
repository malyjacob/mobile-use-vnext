# MCP 入口

`cmd/device-mcp` 是设备服务的 MCP 适配进程。两种传输共用同一套工具注册、JSON Schema、
严格参数接入与结果投影；它只通过 [Go 客户端](client.md) 访问服务的 `/api/<tool>`，不直接
访问引擎、ADB 或账本，也不保存第二份会话或操作权威。默认部署还会把同一适配器安装到手机
chroot，并在 `127.0.0.1:18882` 启动 HTTP 入口；主机侧 stdio/HTTP 适配器仍可按本页运行。

## 1. 协议与进程边界

- 使用 MCP `2026-07-28` 的新协议路径：客户端以 `server/discover` 取得服务能力并协商版本；
  SDK 服务只发布 `2026-07-28`。收到旧版 `initialize` 时，SDK 按生命周期规范回答
  `2025-11-25` 并期望客户端断开；该回答不是本适配器的新协议协商结果，也不对应第二套工具、
  别名或旧协议回退。
- 工具目录固定为 12 个，与 [接口契约](interface.md) 同名同义：`start`、`status`、`observe`、
  `act`、`inspect`、`keepalive`、`end`、`apps`、`open_app`、`notify`、`shell`、`diagnose`。
  不注册 `/health`、认证初始化、旧工具别名或旧协议回退。
- stdio 模式的 stdout 只承载 MCP 协议帧；日志与诊断一律写 stderr。
- 私有配置把适配进程绑定到一个可信 Agent 的服务凭据；该凭据不是工具参数，不进入日志、
  错误、结果或 MCP 正文。

## 2. 工具 Schema 与严格接入

每个工具以显式 JSON Schema 发布：`type=object`、`properties`、`required` 与
`additionalProperties=false`。可选参数缺省等于其声明默认值；`act.operation` 只允许
`tap`、`long_press`、`text`、`swipe`、`key` 五个分支及其声明成员。

调用参数在进入客户端前由 `internal/protocol` 的严格解码器接入：未知成员、重复键、大小写
变体、尾随第二个 JSON 值、类型混用与未声明 `null` 都被拒绝，且不产生设备调用。参数错误
返回 `IsError=true` 的结构化结果与 `invalid_request`。

## 3. 结果投影

一次工具调用的结果包含：

- 一份可读正文：`key=value` 行，保留本次已知的阶段事实（操作号、状态、outcome、会话、
  观察头、树格式、图片元数据、收尾五项、错误与 `recovery.entry`、`recovery.reference`、
  `recovery.input_state`、`recovery.cause` 等）；
- 一份 `structuredContent`：与服务同形的 `Result` JSON；图片字段只保留元数据；
- 请求图片且本次取得时，额外一个 `ImageContent`：`data` 为图片字节的 base64 载体，
  `mimeType` 为 `image/jpeg`。

树、图片与 shell 输出的事实来源都是服务的同一份结果。base64 只出现在 HTTP 载体
`observation.image.base64` 与 MCP `ImageContent.data`；可读正文与 `structuredContent`
都不包含图片字节，也不把 base64 混进树正文。服务未返回图片时，结果不虚构图片元数据。

`isError` 只表达“本次工具调用是否达成请求”，不改写已确认事实：

| 事实 | isError |
| --- | --- |
| 主动作 `executed`，仅后树/图片/清理独立失败（`act`/`open_app` 的后树；含 shell 自然非零） | `false` |
| 主动作 `not_executed` / `uncertain` | `true` |
| 纯 `observe` 有合法核心 O，仅缺图 | `false` |
| 纯 `observe` 没有合法核心 O（含只有只读图） | `true` |
| `status` 给出 `not_seen`、`request_closed` 或其他查询事实 | `false` |
| `diagnose` 证据不足或覆盖范围有限 | `false` |
| `inspect` 内容不可用、准入/协议/传输错误 | `true` |
| `start` 已建立会话但首个观察失败 | `false`（主操作 `executed`） |
| `act` 已派发但完成未知 | `true`（`uncertain`） |

## 4. stdio

```text
device-mcp -transport stdio -config <private-config>
```

- 每条工具调用在独立请求上下文中执行；宿主发送 `notifications/cancelled` 时，本次调用
  上下文被取消，由取消包装按第 6 节处理。
- 进程退出、stdin 关闭与连接断开都不证明设备动作已撤回或原执行链已结束。

## 5. Streamable HTTP

```text
device-mcp -transport http -config <private-config>
```

- 端点以 `POST` 承载请求与 SSE 响应；stateless 模式下 `GET`/`DELETE` 返回 `405`，不建立
  跨请求的 MCP 会话。进程只监听私有配置中的 loopback 地址，路径不作为授权依据。
- 每个请求必须携带 `Authorization: Bearer <mcp_token>`；缺失或错误返回 `401`，不进入
  工具调用。该令牌是宿主侧 MCP 凭据，与服务 `agent_token` 分离。
- 携带 `Origin` 的请求必须精确匹配私有配置的 `allowed_origins`（`scheme://host[:port]`）；
  不匹配返回 `403`。不含 `Origin` 的非浏览器客户端按已认证请求处理。
- 每请求使用独立临时 MCP 会话，不继承其他调用方的 MCP 会话、工具参数或结果；不跨 Agent
  串用归属。
- `2026-07-28` 的一次 POST 就是完整请求生命周期。客户端关闭响应的 SSE 流时，服务请求
  上下文被取消，按第 6 节处理；SSE 断开本身不证明设备动作已撤回。

## 6. 取消、P 与漏收边界

有本次新 P 的工具调用（`start`、`observe`、`act`、`keepalive`、`end`、`apps`、
`open_app`、`notify`、`shell`）在取消时：

1. 停止等待本次调用结果；
2. 用独立的有界上下文调用 `end(P)`（上限 5 秒），不等待设备工作完成；
3. 保留取消前已取得的服务结果；没有结果时不伪造 `not_executed`；
4. 返回结构化取消结果，说明 P 与已发起的有界 `end(P)`，但不声称设备动作已撤回或原执行
   链已结束。

`status`、`inspect`、`diagnose` 只取消本次读取，不登记 P、不调用 `end`。调用参数中的
`reference` 是原观察/操作定位，不是本次取消的 P；不得把它当成本次调用的操作编号。

漏收分两段：

- 服务到适配进程的本次 HTTP 结果丢失时，MCP 持有发送前的 P；可返回带 P 的结构化错误，
  宿主随后按 P 查询原事实。
- MCP 到宿主的整份工具结果丢失且宿主没有 P 时，不保证定位该次操作；只能用已有的 O/P 或
  `status()` 恢复自身原会话，不能用最近一次操作代替，也不把 MCP 请求 ID 当作公开恢复
  编号。

## 7. 配置与运行

主机适配器私有配置只存在于部署机，权限 0600，不入库：

```json
{
  "service": {"base_url": "http://127.0.0.1:18881", "agent_token": "<agent-token>"},
  "transport": "stdio",
  "http": {
    "listen": "127.0.0.1:18882",
    "token": "<mcp-token>",
    "allowed_origins": ["http://127.0.0.1:3000"]
  }
}
```

- `service.base_url` 必须是 loopback；`service.agent_token` 只用于访问服务；
  `http.token` 与 `agent_token` 必须不同。
- 未提供 `http.listen` 时不能以 `http` 传输启动；`allowed_origins` 只接受
  `scheme://host[:port]`。
- 构建与启动方式见 [运行手册](runbook.md) 的 MCP 章节。
