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

当前共同契约为 `"21"`。契约版本不等时返回 `contract_mismatch`；客户端不刷新版本、不重发
业务动作。端口可应答、`/health` 正常都不等于 `can_start=true`。

公开树与 detailed inspection 使用相同的完整节点布尔默认/unknown 规则，不携带 defaults。
delta 的 set 保留显式 false/空值/null；应用器将五块作为固定基线的原子最终图声明，支持
存续节点迁移与新增组装父。调用方始终按 `delta.base` 应用到该 full 基线副本，不能叠加在上一份
delta 结果上；阅读 ref 连续性不代替服务端输入授权。格式见[接口契约](interface.md)。

## 2. 操作编号 P

会改变设备/会话、发布新观察或占用普通工作位的图片读取在发送前生成 P：

| 工具 | P |
| --- | --- |
| `start`、`observe`、`snap`、`wait_for`、`act`、`keepalive`、`end`、`app`、`notify`、`shell` | 生成并放在 `X-Mobile-Use-Operation` |
| `status`、`inspect`、`diagnose` | 不生成、不发送该头 |

- `NewOperationID()` 生成进程级 `P<4位>_<seq>`：首次调用选取 4 位无前导零数字前缀
  （1000–9999）并在进程生命周期内固定，之后每次以原子递增序号生成（如 `P7391_12`）。
  随机源读取失败时返回错误，不静默降级。调用方可用 `client.WithOperationID` 指定一个
  已持有的 P（恢复、验证或接入层预分配），非法编号在发送前被拒。
- 同一 P 不自动重发。服务对重复 P 只返回原操作视图，不再次派发。
- 普通业务 HTTP 传输失败（拨号、写入、读取、超时、取消）返回结构化 `*client.Error`，
  `Transport=true`，`OperationID` 保留本次 P。调用方用 `Status(ctx, p)` 查询原事实，
  不重发同一 P。
- `Status(ctx, p)` 返回 `409 operation_unknown` 时，只说明服务当前无法定位该 P，不能推出
  过去未执行。`End(ctx, p)` 对未知 P 仍会封闭该 P 的未来接纳，返回 `request_closed=true`、
  `outcome=uncertain`、`phase=reference_unknown`；调用方不得把该错误当成资源已释放或改去结束
  当前其它会话。
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
`app`、`shell`、`wait`、`notification`、`diagnosis`、`cleanup`、`error`、`recovery` 等字段
只在本次确有该事实时出现；`recovery` 缺失表示本次结果没有待恢复事实。客户端不改写主事实：
`observation.status=failed` 的 503 仍是带 `Result` 的正常响应，不降成无归属异常。

`effect` 与 `observation.quality` 也来自同一份 `Result`：客户端用 `internal/protocol` 的
响应解码器直接读取，不另建投影层、不补默认值。`effect` 对象出现时 `state` 必填且枚举受限；
`observation.quality` 对象出现时 `state`/`samples`/`stop_reason` 三个成员都必填，显式
`null`、未知枚举或负 `samples` 使整次响应按 `invalid_response` 拒绝。`samples=0` 是合法
值；对象缺失只表示本次结果没有该事实，不能当作 `not_observed` 或 `unknown` 的替代来源。

`Result.Observation.Tree` 携带 compact full 或 delta，节点集合随服务的[裁剪策略](interface.md#node)
确定。留存节点使用原编号，匿名包装省略后孩子可能换父。调用方留存已确认的 full，按每条
`delta.base` 独立应用差分。使用 `observation.ReadApply` 时，历史 full 与 `add`/`replace` 定义中的
折叠链会展开，重建结果以普通节点定义交付。只读 `Client.Inspect` 返回 detailed 视图。
诊断中的节点编号可能缺席于 compact。同一调用方的原 O 仍在服务端留存时，按该 O 和编号补看。

失败结果可以携带 `observation`：`act` 的重验在派发前失败且已发布新帧时，`not_executed` 结果带
一个新 O 与 `recovery`（`entry=retry`、`reference=O`、`input_state=effective`）。客户端必须在
错误响应上同样解码 `observation`（本仓库客户端已如此），不得丢弃；`retry` 指向的 O 以结果里的
`observation.id` 为准，没有该成员时用该操作原请求引用的 O（重复 `P` 时即请求体里的 O）。
重复 `P`、`status(P)` 与冻结结果的视图不带 O 编号。

| 方法 | 请求参数 | P |
| --- | --- | --- |
| `Start` | 无 | 是 |
| `Status` | `reference`（空串表示无引用） | 否 |
| `Observe` | `*protocol.ObserveRequest` | 是 |
| `Snap` | `*protocol.SnapRequest` | 是 |
| `WaitFor` | `*protocol.WaitForRequest` | 是 |
| `Act` | `*protocol.ActRequest` | 是 |
| `End` | `reference` | 是 |
| `Inspect` | `*protocol.InspectRequest` | 否 |
| `Keepalive` | `reference`（省略时长，显式重设为两分钟） | 是 |
| `KeepaliveWithDuration` | `reference` + `durationMinutes`（1～120，显式覆盖） | 是 |
| `App` | `*protocol.AppRequest`（`kind` 选择七个互斥分支） | 是 |
| `Notify` | `*protocol.NotifyRequest` | 是 |
| `Shell` | `*protocol.ShellRequest` | 是 |
| `Diagnose` | `*protocol.DiagnoseRequest` | 否 |

请求体由 `internal/protocol` 的 `Encode` 生成，只包含该工具声明的成员；未知字段、类型
混用与显式 `null` 不会被客户端补成默认值。客户端每条逻辑调用最多发出一次 HTTP 请求
（`Discover` 固定两次只读请求），不排队、不重试、不跟随重定向。

`Client.WaitFor(ctx, *protocol.WaitForRequest, ...Option)` 只发一次 `POST /api/wait_for`，
不轮询、不重试。请求编码只写 `reference`、`timeout`、调用方给出的 `query`/`absent_query`
与 `stable`/`full`/`image`：两个条件只在对应的 `Has` 标记为真时出现，省略不会被补成空串；
三个布尔成员总是按值编码。编码前只拒绝空 `reference` 与 1–40 之外的 `timeout`；
`stable=true` 时少于 2 秒、条件为空或全空白、超长以及两个条件字面相同等组合由服务端与
MCP 适配器的严格解码拒绝；两个条件折叠后相同则是引擎准入判定的矛盾，请求已经发出但尚未
接触设备。
响应由 `internal/protocol` 的响应解码器直接读成 `Result`：`wait` 出现时 `trigger` 与
`elapsed_ms` 必填；`matched` 可以整个缺失（表示最终发布帧上没有可判定的单帧条件），显式
`null` 或空列表都不是合法编码，条目只接受 `query`/`absent_query`/`timeout`。未知
`trigger` 枚举值、非法的 `matched` 形态或负 `elapsed_ms` 按 `invalid_response` 拒绝；
响应对象对未知成员向前兼容，多出成员不构成拒绝理由。客户端不重算 `trigger`、不补
`matched`，也不把 `stable` 写进该列表。一次等待的实际耗时可达 `timeout` 加 35 秒的验证
采集预留，仍由 `defaultTimeout=90s` 覆盖；该全局默认值不因本方法改变。

`Act` 仍接收 `*protocol.ActRequest`，编码器只写所选分支的成员：`scroll` 的
`container`/`direction`/`amount` 与 `swipe` 的 `from`/`to`/`duration_ms` 互斥，缺成员、
混用或 `duration_ms` 为负数、1–49/2001 以上都会在发送前被拒绝；`duration_ms=0` 表示省略并
使用服务端默认 300，不表示一个显式的零时长。请求引用字段是 `reference`；`operation.bubble`
与 `operation.press_enter` 使用 presence-aware 结构，省略时不写成员，显式 `false` 必须写成
`false`，不能按 Go bool 零值省略。编码器与 HTTP 解码共用同一分支校验：坐标目标或其他动作
携带 `bubble`、非 text 携带 `press_enter` 都在发送前拒绝。客户端不新增滚动、质量或效果参数，
也不在本地选择容器或起止点；`status(P)` 与重复 P 视图中的 `effect` 只按服务返回值读取，不重算。
`Result.text_input` 出现时 `write` 与 `enter` 都必须存在且属于固定枚举，合法组合受响应
解码器约束；客户端不补 `not_executed`、不根据 `phase` 推导阶段，也不在本地改写快照。
`text` 错误的 `details` 保留嵌套 `dispatch` 对象、`stage`/`reason` 与 `candidate_count`；
响应解码器不把 `0` 当成缺失、不改写枚举，客户端也不据此重建 `text_input` 或恢复入口。

`Client.App(ctx, *protocol.AppRequest, ...Option)` 按 `Kind` 编码七个互斥分支：编码器只写所选
分支的成员；分支不完整、缺少选择器，或 `open`/`kill` 使用不允许的选择器时，`Encode` 在发送
前拒绝。`list` 的 `reference` 可为 ready O 或活动原会话 P；`open`/`claim`/`handoff`/`close`/
`kill` 的 `reference` 必须是当前 ready O。`open` 只编码 `target.app`，不接受顶层 `app`；
`list` 的顶层 `app` 只是过滤。`App` 的每次新调用都生成自己的 P（或用 `WithOperationID`
预分配）；`find` 是 detached P，不绑定调用时恰有的全局活动会话；`list` 的 P 绑定
`reference` 解析到的原会话。`find`/`list` 成功与失败都没有 `outcome`；响应丢失时用
`Status(ctx, p)` 只能读取摘要，集合需要以新 P 重新查询。重复 P 只返回原操作视图，不再次
读取或派发。`app` 的五个变更 kind 在响应中带 `outcome` 与后观察，消费失败或重试规则与其他
工作工具一致。

`Keepalive(ctx, reference)` 表示显式重设为两分钟；`KeepaliveWithDuration` 发送显式
`duration_minutes`。两者共用同一发送路径与 P 校验；直接传入 0、负数、超过 120 或把时长
加进通用选项都不会发送请求，也不会把非法值归一化为默认值。显式调用覆盖原截止时间，因此
可以缩短 start 的五分钟租期或先前设置的长租期；普通活动之后仍可按 `max(expires_at, now+2分钟)`
再次延长。重复 P 只读取原操作视图，不再次更新；改变时长必须使用新的 P。

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
| 服务稳定错误码（如 `session_ended`、`operation_unknown`、`device_busy`、`invalid_request`） | 服务已返回结构化 `Result`；`Result` 保留完整阶段事实与可能的 `request_closed` |
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

`Client.Snap(ctx, *protocol.SnapRequest, ...CallOption)` 只发一次 `POST /api/snap`，请求体
只有 `reference`。成功响应只有一个顶层 `image`：客户端复用同一有界解码路径，把
`image.base64` 解码到 `Response.Image` 并清空 `Result.Image.Base64`。除共用响应边界外，
客户端还要求顶层图片满足：有效的 `captured_at`、正整数的输出 `width`/`height`、
`media_type=image/jpeg`、`read_only=true`、合法的 `screen` 与非空 base64。以下成功形状
按 `invalid_response` 拒绝：

- 顶层 `image` 与 `observation.image` 同时出现；
- 顶层 `image` 与 `error`、`outcome` 或 `observation` 同时出现；
- 顶层 `image` 的任一必填元数据缺失、为 `null` 或越界（含显式 `null` 的顶层 `image`）。

重复 P 与 `status(P)` 的无图摘要不是错误响应，可以正常解码；它的 `operation_state`、
`phase` 与 `error` 按服务结果读取，`Response.Image` 与 `Result.Image` 都为空。客户端不会
为无图摘要补图片，也不会把 `snap` 的成功形状交给观察图片路径。
