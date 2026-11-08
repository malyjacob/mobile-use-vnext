# Go 客户端

`internal/client` 是设备服务的强类型 HTTP 客户端。MCP 适配器与验证工具都通过它访问
`/api/<tool>`；它不导入 `internal/engine`、`internal/android` 或账本，不保存第二份会话、
观察或操作权威。

## 1. 构造与发现

```go
c, err := client.New(client.Config{
    BaseURL:    "http://127.0.0.1:18881",
    AgentToken: agentToken,
    // HTTPClient / Timeout 可选；默认每条调用 90s 上限，不跟随重定向
})
instance, err := c.Discover(ctx)
```

`Discover` 只做两次只读请求：`GET /health` 取得服务实例、契约版本与发行版本，再
`POST /api/status {}` 取得当前准入事实（`can_start`、`blocking_code`、本 Agent 的
`session`、`start_operation_id` 与 `cleanup`）。它不生成 P、不续租、不创建会话。

契约版本不等时返回 `contract_mismatch`；客户端不刷新版本、不重发业务动作。端口可应答、
`/health` 正常都不等于 `can_start=true`。

## 2. 操作编号 P

会改变设备/会话或发布新观察的调用在发送前生成 P：

| 工具 | P |
| --- | --- |
| `start`、`observe`、`act`、`keepalive`、`end`、`apps`、`open_app`、`notify`、`shell` | 生成并放在 `X-Mobile-Use-Operation` |
| `status`、`inspect`、`diagnose` | 不生成、不发送该头 |

- 默认 P 形状为 `P` + 24 位小写十六进制；调用方可用 `client.WithOperationID` 指定一个
  已持有的 P（恢复、验证或接入层预分配），非法编号在发送前被拒。
- 同一 P 不自动重发。服务对重复 P 只返回原操作视图，不再次派发。
- 普通业务 HTTP 传输失败（拨号、写入、读取、超时、取消）返回结构化 `*client.Error`，
  `Transport=true`，`OperationID` 保留本次 P。调用方用 `Status(ctx, p)` 查询原事实，
  不重发同一 P。
- 取消 `ctx` 只停止本次客户端读取/交付；它不调用 `end`，也不证明手机动作已撤回或原
  执行链已结束。MCP 入口的取消语义见 [MCP 入口](mcp.md)。

## 3. 方法与结果

每个方法返回 `*client.Response` 与 `error`：

```go
type Response struct {
    HTTPStatus  int
    OperationID string
    Result      *protocol.Result
    Image       *client.Image
}
```

`Result` 与服务 HTTP 结果同形：`outcome`、`phase`、`session`、`observation`、`inspection`、
`apps`、`app`、`shell`、`notification`、`diagnosis`、`cleanup`、`error`、`next` 等字段只在
本次确有该事实时出现。客户端不改写主事实：`observation.status=failed` 的 503 仍是带
`Result` 的正常响应，不降成无归属异常。

| 方法 | 请求参数 | P |
| --- | --- | --- |
| `Start` | 无 | 是 |
| `Status` | `reference`（空串表示无引用） | 否 |
| `Observe` | `*protocol.ObserveRequest` | 是 |
| `Act` | `*protocol.ActRequest` | 是 |
| `End` | `reference` | 是 |
| `Inspect` | `*protocol.InspectRequest` | 否 |
| `Keepalive` | `reference` | 是 |
| `Apps` | `*protocol.AppsRequest` | 是 |
| `OpenApp` | `*protocol.OpenAppRequest` | 是 |
| `Notify` | `*protocol.NotifyRequest` | 是 |
| `Shell` | `*protocol.ShellRequest` | 是 |
| `Diagnose` | `*protocol.DiagnoseRequest` | 否 |

请求体由 `internal/protocol` 的 `Encode` 生成，只包含该工具声明的成员；未知字段、类型
混用与显式 `null` 不会被客户端补成默认值。客户端每条逻辑调用最多发出一次 HTTP 请求
（`Discover` 固定两次只读请求），不排队、不重试、不跟随重定向。

## 4. 错误

```go
type Error struct {
    OperationID string
    Code        string
    Message     string
    HTTPStatus  int
    Transport   bool
    Result      *protocol.Result
}
```

| Code | 含义 |
| --- | --- |
| 服务稳定错误码（如 `session_ended`、`device_busy`、`invalid_request`） | 服务已返回结构化 `Result`；`Result` 保留完整阶段事实 |
| `transport_unavailable` | 未取得服务响应（拨号、写入、读取、超时、取消）；P 保留 |
| `invalid_response` | 响应无法解析、形状不符或图片载体无效 |
| `response_too_large` | 响应超过 8 MiB 上限 |

`Error()` 只给出稳定 code/message/HTTP 状态，不包含 URL、令牌、请求体或原始异常文本。
调用方按 `Code` 与 `Result` 决定恢复入口，不把错误字符串当作设备事实。

## 5. 图片

`observe(image=true)`、`act(image=true)` 成功时，服务在 `observation.image` 给出本次图片
元数据（`captured_at`、`width`、`height`、`media_type`），并在 HTTP 载体
`observation.image.base64` 携带编码后的图片字节。客户端解码一次：

- `Response.Image.Data` 是解码后的图片字节；`MediaType` 为 `image/jpeg`；
- `Response.Image` 的宽高、采集时间、`read_only` 与 `screen` 来自同一元数据；
- `Response.Result.Observation.Image.Base64` 在解码后被清空，结构化结果与可读正文都不会
  再携带 base64。

图片失败不影响有效核心树：`observation.status=partial`、`missing=["image"]`、
`image.error.code` 为 `image_capture_failed`、`image_encode_failed`、`image_too_large` 或
`image_stale`。图片成功而核心树失败时没有新 O，`image.read_only=true`，不能作为输入依据。
客户端不缓存图片，也不提供历史图片补取。
