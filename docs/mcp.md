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
- 工具目录固定为 13 个，与 [接口契约](interface.md) 同名同义：`start`、`status`、`observe`、
  `snap`、`wait_for`、`act`、`inspect`、`keepalive`、`end`、`app`、`notify`、`shell`、
  `diagnose`。不注册 `/health`、认证初始化、旧工具别名或旧协议回退。
- 目录指纹是工具数、注册顺序、名称、描述与入参 schema 的 canonical SHA-256，当前为
  `91fed94c832e6b80eee98df4c6c4d0053819452ed6e630e25f8700cfb483f1c9`；发布清单与探针按
  它比对实际 `tools/list`，任一条描述或 schema 变化都会改变该值。工具目录指纹不覆盖
  server instructions；instructions 由探针把 `server/discover` 返回的说明与构建内的固定
  说明源逐字节比较，并附加兼容 `initialize` 检查。
- stdio 模式的 stdout 只承载 MCP 协议帧；日志与诊断一律写 stderr。
- 私有配置把适配进程绑定到一个可信 Agent 的服务凭据；该凭据不是工具参数，不进入日志、
  错误、结果或 MCP 正文。

## 2. 工具 Schema 与严格接入

每个工具以显式 JSON Schema 发布：`type=object`、`properties`、`required` 与
`additionalProperties=false`。可选参数缺省等于其声明默认值；`act.operation` 只允许
`tap`、`long_press`、`text`、`swipe`、`scroll`、`key` 六个分支及其声明成员。`act`、
`inspect` 与 `app` 的请求引用字段统一为 `reference`；旧 `observation_id` 是未知成员，
不提供别名。`act` 的 `reference`、`app` 的五个变更 kind 必须指向当前 ready O；
`inspect` 必须指向保留中的历史 O；`app.list` 仍接受能解析到活动原会话的 P。
`operation.bubble` 与 `operation.press_enter` 是 presence-aware 布尔，省略与显式 true
在该适用分支等价；schema 的字段说明明确它们只对 ref tap/long_press 或 text 生效，不发布
可能被误读为全类型生效的通用 `default`。

`act` 的滚动与手势输入与 [接口契约](interface.md) §6 一致：`scroll` 用 `container`（ref 或
坐标）与 `direction`（`up`/`down`/`left`/`right`）表达，可选 `amount`（`page` 默认或
`half`）；`swipe` 仍以 `from`/`to` 表达原始手势，并接受可选 `duration_ms`（50–2000，默认
300）。分支成员互斥：`scroll` 不接受 `target`/`from`/`to`/`key`/`text`/`duration_ms`，其他
分支也不接受 `container`/`direction`/`amount`。`operation` schema 因此暴露 `scroll`、
`container`、`direction`、`amount`、`duration_ms`、`bubble` 与 `press_enter`；服务端严格
解码仍是权威。

`tap`/`long_press` 的 `ref` 目标有效 `bubble=true`（省略或显式 true）时，原目标自身已有
请求动作仍走直接目标规划；原目标完全没有 `tap`、`long_press`、`text` 动作时，只向最近的
touch-interactive 祖先提升一次。该祖先不存在、不可用或不支持请求动作时返回
`422 unsupported_node_action`，支持动作但没有可证安全点时返回 `422 no_safe_point`，都不
继续向更远祖先查找。静态规划同时绑定原 child 与祖先；认领后重验两者、公开 Role 与完整
Label、最近祖先关系及安全点，child 丢失或内容被替换返回 `409 target_changed`，零派发。
有效 `bubble=false` 保持直接目标错误；坐标目标必须省略 `bubble`。该提升只在一次调用内
派发一个动作，不模拟 DOM 多级事件传播。

`tap`/`long_press` 的 ref 静态拒绝按 code 区分：`invalid_target` 表示目标结构或几何不可用，
`unsupported_node_action` 表示请求动作或提升前提不满足（含祖先不可用），`no_safe_point` 表示有动作但
固定候选都无法证明。`no_safe_point` 不是向祖先继续查找目标的指令；`error.details` 的
`reason`、有界阻塞/歧义/归属 ref 列表与可选 `suggested_point` 只描述同一 O 上可用的调用线索。
三类静态拒绝都不消费 O、结果为 `not_executed`；建议坐标不是执行授权，坐标重试须使用新 P
并独立通过准入与重验。

`text` 的 `press_enter` 省略或有效为 true 时，一次调用完成必要聚焦（已聚焦 ref 不重复点击）、
将目标框全文设为 text、以同一次写入调用中同一节点的刷新值精确读回，并至多发送一次 ENTER；
普通输入不需要先 tap 或另发 ENTER。显式 false 只做必要聚焦、写入并读回，不发送 ENTER，也不返回
`text_input`。复合 text 的 `text_input` 在首次结果、运行中 `status(P)`、重复 P 与冻结结果中
保持同一份 `{write,enter}` 快照；ENTER 只证明按键原语，不证明业务提交，`enter=uncertain`
时先观察而不是原样重发。text 失败的 `error.details` 固定给出 `stage`、`reason` 与
`dispatch.focus/write/enter`（`not_attempted|not_dispatched|dispatched|unknown`），
可选 `candidate_count` 保留 `0`；MCP 的 structured/text/both 三种模式都保留同一组嵌套字段，
调用方按枚举决策而不解析 `message`。写入或 ENTER 仍 uncertain 且原会话活动时，语义恢复直接
指向 `observe`/`P`，实时会话/耐久/设备/忙阻塞才覆盖为 `end`/`status`/`user` 并给
`blocking_code`。字段与合法组合见
[接口契约](interface.md) §6。

公共树语法与调用规则由 server instructions 发布：`server/discover` 与兼容 `initialize`
返回同一份说明，完整树块词表恰好出现一次，另含会话推进、O/P 身份、统一 `reference`、
租期、工具选择、默认 `bubble`/`press_enter`、`text_input` 事实与恢复分类；13 个工具描述不再
内联树引导，只保留各自用途、关键参数/默认、结果或动作边界与最少恢复路径。`start` 描述保留
一段短兼容提示：O 是可消费观察，P 是该操作的幂等/查询编号；full 是完整树，delta 只相对其
base 且新 delta 覆盖旧 delta。树块词表本身不变：树节点行的动作列仍只有 `tap`、`long_press`、
`text`、`swipe`（`Node.actions` 不新增枚举）；`actions=swipe` 只是节点的源能力，不是 raw
`swipe` 授权，滚动用 `operation.type=scroll` 与 `container` 表达。引导声明块头 `view=`、
块头后的空行、`entries` 计数与 `chain` 行。

调用参数在进入客户端前由 `internal/protocol` 的严格解码器接入：未知成员、重复键、大小写
变体、尾随第二个 JSON 值、类型混用与未声明 `null` 都被拒绝，且不产生设备调用。参数错误
返回 `IsError=true` 与 `invalid_request`，按所选结果模式投影。

`keepalive` 的 schema 声明可选的整数 `duration_minutes`（1～120，省略为 2），适配器按参数
是否存在选择 SDK 的默认调用或含时长调用，完整透传到服务；不会在本地补默认值、丢弃显式
时长或改写成单调续租。server instructions 与参数说明明确：显式续期覆盖原截止时间且可
缩短，省略也重设为两分钟；普通活动在续期点取 `max(现有截止时间, now + 2 分钟)`，不继承
本次时长；start 使用五分钟租期，会话没有累计时长硬上限。调用取消仍按第 6 节撤销授权，
长租约不覆盖取消边界。

`snap` 的 schema 只有一个必填 `reference`（`O…`/`P…`，≤64），没有其他成员；它与
`observe` 共用引用解析口径（额外拒绝本次请求 P 的自引用），但只读取一张图，不读取树、
不发布观察。它是唯一带预分配 P 的
读图工具：`tools/list` 只为它发布 `annotations.readOnlyHint=true`，其他工具不发布该注解。
成功时结果是顶层 `image` 元数据加一个 `ImageContent`；`result v2` 以
`image=<紧凑 JSON>` 输出同一元数据（不含 base64），`structuredContent` 在顶层 `image` 携带
同组成员，三种结果模式都只发送一个 `ImageContent`。`isError=false` 要求完整图片实际交付；
重复 P 的无图摘要、失败、准入或传输错误均为 `isError=true`。取消见第 6 节。

`wait_for` 的 schema 以 `reference` 与 `timeout` 为必填，`query`、`absent_query`、`stable`、
`full`、`image` 可选：`timeout` 是 1–40 的整数，`stable=true` 时至少 2；两个条件字符串各
至多 256 个码点。该工具的说明与结果契约：到时同样按成功返回并发布一个观察
（条件或稳态命中复用候选帧；到时或采样上限退出时，最新成功轮询帧仍在 `CaptureMaxAge=15s`
内且之后没有失败尝试则复用该帧，否则做一次终帧采集），调用方须检查 `wait.trigger` 与
`wait.matched`，连续 3 帧稳定由 `observation.quality.state=stable` 表达；
`wait.elapsed_ms` 计自准入完成至终帧发布，验证采集整体受 35 秒固定预留约束，实际耗时可
超过 `timeout` 至多约 35 秒；两个条件只匹配当前公开可见树且 `stable=true` 对整树计算摘要，
页面有倒计时或实时进度变化时应保持 `stable=false`。参数在适配器侧按同一严格解码器接入，
解码器可判定的非法组合在发出 HTTP 请求前被拒；两个条件折叠后相同由引擎准入在请求发出后
拒绝，不接触设备。

`app` 的根 schema 是一个扁平、封闭对象：根声明
`properties={kind,query,reference,scope,app,target,restore_foreground,allow_borrowed,
allow_stop_playback}`、`required=["kind"]` 与 `additionalProperties=false`；`target` 也是
扁平对象 `{task_id,app}`、`additionalProperties=false`。根内不使用 `allOf`、`if`/`then`/
`else`、`not` 或 `oneOf`。根字段可见不代表每个 kind 都接受；服务端 `DecodeApp` 的封闭
kind 成员矩阵是互斥权威：

| kind | 必填（除 kind） | 可选与默认 |
| --- | --- | --- |
| `find` | 无 | `query`（≤256 码点） |
| `list` | `reference`：ready O 或活动原会话 P | `scope=session`（默认）或 `primary`、`app`（包名过滤） |
| `open` | `reference`（当前 ready O）、`target={app}` | 无 |
| `claim` | `reference`（当前 ready O）、`target` | 无 |
| `handoff` | `reference`（当前 ready O）、`target` | `restore_foreground=false` |
| `close` | `reference`（当前 ready O）、`target` | `allow_borrowed=false` |
| `kill` | `reference`（当前 ready O）、`target={task_id}` | `allow_stop_playback=false` |

`target` 为 `{"task_id": 正整数}` 或 `{"app": 包名}`，恰好一种；`open` 只接受 `target.app`，
`kill` 只接受 `target.task_id`，`claim`/`handoff`/`close` 接受两者之一。顶层 `app` 只保留给
`list` 过滤。`find`/`list` 是查询，成功与失败均无 `outcome`；结果各按 32 KiB 完整条目前缀
返回，`total`/`truncated` 说明预算前数量与截断；其余五个 kind 消费当前 ready O，成功返回
原会话屏的一次 full 无图观察。查询与变更的 P 绑定、IsError 与取消差异见第 3、6 节。

## 3. 结果投影

结果模式只选择表示形式，三种模式承载同一份事实与相同图片：

| 模式 | `content` | `structuredContent` |
| --- | --- | --- |
| `both` | 完整 `result v2` 文本与图片 | 完整 `Result` JSON |
| `text`（默认） | 完整 `result v2` 文本与图片 | 不发送 |
| `structured` | 图片；`isError=true` 时附最小 `diagnostic v1` 文本 | 完整 `Result` JSON |

完整结果是 `(canonical A, CallToolResult.IsError, ImageContent)`。`A` 是去除图片 base64
载体后的确定性 `Result` JSON；取消时还包含本节规定的 `error.details.mcp.*`。文本和结构化
编码必须无损还原同一结果：缺失、`null`、`false`、`0`、`""`、`[]` 与数组顺序均保留。
图片只在 `ImageContent` 中发送。`structured` 成功且无图时 `content=[]`；不接受空 content
的宿主应使用 `both` 或 `text`。模式不用于脱敏或截断，不发布 `outputSchema`。

`effect` 与 `observation.quality` 按同一份 `Result` 投影，三种模式承载相同字段与值：
`result v2` 在 `phase` 之后投影 `effect.state`，在观察字段中投影
`observation.quality.state`、`observation.quality.samples` 与
`observation.quality.stop_reason`；`structured` 与 `both` 的 `structuredContent` 保留同一份
对象，`text` 不发送结构化内容。适配器不重算或补投影这些事实；`effect` 只由 `scroll` 的
操作结果与保留的 P 视图携带，带树的观察都携带 `observation.quality`（未采样或重验交付为
`unknown`/`0`/`not_sampled`），只有没有树的失败观察不携带它。两者的成员与取值见
[接口契约](interface.md) §11。

复合 `text` 的 `text_input` 也按同一份 `Result` 投影：`result v2` 在 `effect` 之后输出
`text_input.write` 与 `text_input.enter` 两个 token（固定顺序，枚举
`not_executed|uncertain|executed`），`structured` 与 `both` 的 `structuredContent` 保留同一
对象；显式 `press_enter=false` 和非法组合不会产生该对象。首次结果、运行中 `status(P)`、
重复 P 与冻结结果使用同一快照。

`wait_for` 的 `wait` 对象也按同一份 `Result` 投影：`result v2` 输出 `wait.trigger`（token）、
`wait.matched`（紧凑 JSON 数组）与 `wait.elapsed_ms`（数字字面量），成员顺序固定为
`trigger`、`matched`、`elapsed_ms`；`matched` 为空时服务端已省略该成员，适配器不补
`null`、不补空数组。`structured` 与 `both` 的 `structuredContent` 保留同一份对象。验证采集
失败的结果按第 3 节的诊断投影透传 `wait.trigger` 与 `wait.elapsed_ms`，不输出
`wait.matched`。

`snap` 的顶层 `image` 也按同一份 `Result` 投影：`result v2` 在 `saved` 之后、`session` 之前
输出 `image=<紧凑 JSON>`，`structuredContent` 保留同一元数据对象；两者都不包含 base64。
图片字节只出现在唯一的 `ImageContent`。失败、重复 P 与 `status(P)` 摘要没有顶层 `image`，
也不会被适配器补成空对象。

完整文本第一行为 `result v2 is_error=<true|false>`，最后为 `end result`。已知字段按
`Result` 的公共顺序输出，字段行和块可以交错；例如 shell 块之后仍可有错误与恢复字段。
字段行为 `key=value`，受控 ID、枚举、时间戳、整数、布尔可用无空白、`=`、引号、反斜杠的
ASCII token；任意文本与设备来源字符串使用 JSON 字符串，空串和非法 token 也使用 JSON
字符串；复合值使用紧凑 JSON。未知成员或不可表示的复合值使用完整兜底：

```text
json v1 path="/observation/tree" value={"format":"full","scope":"","root":null}
```

`path` 为 RFC 6901 JSON Pointer，`~`、`/` 分别转义为 `~0`、`~1`。每个事实路径只编码
一次。块前恰好一个空行，块后接字段或结果终止行时没有额外空行；相邻块之间恰好一个空行。
块头的 `nodes`、`changes`、`events`、`output_bytes` 仅为派生计数；delta 的
`scope`、subtree/query 的 `tree_scope` 也是派生显示，不新增 `A` 的成员。

`structured` 的错误文本使用 `diagnostic v1 is_error=true` / `end diagnostic`，只包含
操作号、操作状态、outcome、phase、目标引用、准入/消息状态、观察 status/id、会话、收尾、
条件等待的 `wait.trigger` 与 `wait.elapsed_ms`、错误与恢复等存在的解释字段；它不代表
完整 `A`。所有字段均从同一份 `A` 生成。`app` 只在 diagnostic 中补充 `app.kind`，不展开
`items`、`tasks`、PID 或其他业务事实；`wait.matched` 不进入 diagnostic：失败结果没有可判定
那一帧的已发布观察。

`app` 结果字段在完整文本中的分类固定如下；复合字段使用紧凑完整 JSON，保留嵌套成员、
数组顺序、显式 `null` 与 `[]`：

| `app` 路径 | 文本编码 |
| --- | --- |
| `kind`、`ownership`、`foreground_restore`、`captured_at` | token |
| `package` | JSON 字符串 |
| `task_id`、`source_task_id`、`display_id`、`source_display_id`、`total` | 数字字面量 |
| `reused`、`state_preserved`、`complete`、`truncated`、`restarted` | true/false/null 字面量 |
| `items`、`tasks`、`foreground`、`target_pids`、`killed_pids`、`remaining_pids` | 紧凑 JSON |

`state_preserved:null`、`restarted:null`、`recents_cleaned:null` 与空数组都必须保留；未知或
不可表示的字段仍走整值 `json v1 path=... value=...` 兜底，不能只发计数或丢成员。

适配器严格解码成功并预分配 P、但服务结果未知时，只能从已接纳的请求补 `app.kind` 作为调用
元数据；不得补 `task_id`、`display_id`、PID、`ownership` 或其他设备事实，也不得声称服务已
登记或已 finished。适配器自身的参数接入拒绝不分配 P，也不补 `app`。

文本字节是稳定契约：`result v2` 的成员顺序、分类与兜底规则不变；取消统一标为
`isError=true`。工具名称、描述、入参 schema 与目录指纹随发布清单核验，描述变更会改变
目录指纹，部署必须按清单重新核验。

树、图片与 shell 输出的事实来源都是服务的同一份结果。base64 只出现在 HTTP 载体
`observation.image.base64` 与 MCP `ImageContent.data`；可读正文与 `structuredContent`
都不包含图片字节，也不把 base64 混进树正文。服务未返回图片时，结果不虚构图片元数据。

`isError` 只表达“本次工具调用是否达成请求”，不改写已确认事实：

| 事实 | isError |
| --- | --- |
| 主动作 `executed`，仅后树/图片/清理独立失败（`act`/`app` 的后树；含 shell 自然非零） | `false` |
| 主动作 `not_executed` / `uncertain` | `true` |
| 纯 `observe` 有合法核心 O，仅缺图 | `false` |
| 纯 `observe` 没有合法核心 O（含只有只读图） | `true` |
| `wait_for` 发布出合法核心 O（含到期未命中） | `false`（按 `observe` 同口径） |
| `wait_for` 没有发布新 O（含验证采集失败、只有只读图、准入或传输错误） | `true` |
| `snap` 完整交付顶层图片与一个 `ImageContent` | `false`；失败、重复 P 无图摘要与取消为 `true` |
| `status` 正常给出 `request_closed` 或其他查询事实 | `false` |
| `status`/`end` 返回 `operation_unknown` | `true`；`end` 可同时封闭该 P 的未来接纳 |
| `diagnose` 证据不足或覆盖范围有限 | `false` |
| `inspect` 内容不可用、准入/协议/传输错误 | `true` |
| `start` 已建立会话但首个观察失败 | `false`（主操作 `executed`） |
| `act` 已派发但完成未知 | `true`（`uncertain`） |
| `app` 的 `find`/`list`：首次完整查询或已明确标记的无 error 操作摘要 | `false`；读取、接入或传输错误为 `true`，查询取消调用本身为 `true` |
| `app` 的五个变更 kind：主 outcome `executed` | `false`；`not_executed`/`uncertain` 为 `true` |
| `app.close` 仅 `complete=false`（recents 未确认）、`app.handoff` 仅前台恢复未确认、主动作后树失败 | 不改写主 outcome 与 isError |
| MCP 调用取消（含已确认 `executed` 的结果） | `true`，保留原执行事实 |

`status(P)` 的 `query_cancelled` 摘要在没有 `error` 时 `IsError=false`，只表示摘要已提供，
不代表原查询成功或包含集合；查询调用本身的取消仍按上一行记为 `true`。

### 3.1 树块

树块只投影本次 `Result` 中已有的树或补看内容：不新增采集、不展开 delta、不缓存基线，
也不读取请求参数、时间或随机数；同一个 `Result` 重复渲染逐字节相同。

在 `result v2` 文本里（`text`/`both` 模式），错误结果同样按成员投影：`act` 重验失败交付的
新观察会带树块，`is_error=true` 与树块可以出现在同一个结果里。交付与否不改变 `is_error` 的
判定：`act` 看 `outcome` 是否为 `executed`，`observe` 与 `wait_for` 看是否发布出核心 O，
其余工具按 `error` 与各自的查询事实。
`structured` 模式的错误文本是 `diagnostic v1`，不含树块。

**观察块。** 块头之后是一个空行、节点行，最后是终止行：

```text
tree v2 format=full id=O3 scope=reported_visible_with_context view=compact nodes=9 entries=9

n1 root [0,0,1080,2400]
  n2 group
    n3 button [24,60,144,180] "返回" tap
    n4 text [300,80,780,160] "设置"
    n6 switch [900,60,1032,180] "启用" tap checked=true
  n5 scroll [0,220,1080,2400] axis=y
    n12 group -
      n13 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
    n14 group -
end tree
```

节点行固定列序：2 空格/层缩进、`n<ref>` 或 `chain [n2 n3]`、`role`、几何列、每个
`label` 一个 JSON 字符串、`value=` / `hint=`、动作词、标志。动作词为 `tap`、
`long_press`、`text`、`swipe`，来自 `Node.actions` 的源能力词表；受控滚动不增加动作词，
以 `act.operation.type=scroll` 表达。标志为 `enabled=false|null`、
`focused`/`selected`/`protected` 的 `true|null`、`checked=true|null`、
`axis=x|y|both`、`resource="…"` 与 `context`。设备来源字符串按 JSON 字符串字面量转义，
`<`、`>`、`&` 保持原样；缺省值按协议省略规则不输出。

几何列与节点定义一一对应：已知矩形写 `[l,t,r,b]`，源未提供几何写 `-`；观察视图里按匿名
规则隐藏的已知矩形直接没有该列，`-` 不表示隐藏。连续匿名单子节点折成一条
`chain [n2 n3]`，成员共享其余列，孩子属于末成员。`nodes` 是展开后的逻辑节点数，
`entries` 是普通节点或 `chain` 定义行数；详细补看视图里两者相同。`checked` 默认
`false`（省略），只有 `true` 与 `null` 显式出现。完整节点的缺席布尔恢复为已知默认，
显式 null 保持未知；set 的缺席只表示未修改。树与补看块均不输出 defaults 行。

delta 块头为
`tree v2 format=delta id=<O> base=<O2> view=compact scope=<scope> changes=<n>`，正文按
`set → add → remove → children → replace` 顺序输出。`changes` 是五个块的顶层声明条数
之和：一个 `set` 条目即使改了多个字段也只计 1，chain 内的 ref 数也不计入。

```text
tree v2 format=delta id=O4 base=O3 view=compact scope=reported_visible_with_context changes=6

~ n7 label=["WLAN 已关闭"] actions=["tap"] bounds=[40,300,240,380]
~ n8 checked=false
+ n14 group
  n15 text [48,490,700,550] "移动网络" tap
- n16
children n5 = [n6 n9 n14]
= n12 group
  n91 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
end tree
```

`~` 行只列实际变化的字段，字段值保留显式 `false`、`""`、`[]` 与 `null`；字段顺序为
`label`、`actions`、`bounds`、`enabled`、`focused`、`selected`、`checked`、`value`、
`hint`、`protected`、`axis`、`resource`。`bounds=hidden` 表示该几何按策略不交付，
`bounds=null` 表示置为未知。`SetNode` 没有 `role`，`~` 行也不输出 `role`。
`add` / `remove` 保持结果中的顺序；`set`、`children`、`replace` 按 ref 数值升序输出。
同一个 `base` 下的新 delta 覆盖旧 delta，不叠加；块头 `base=` 是唯一锚点。

**补看块。** 块头为
`inspect v2 scope=<tree|subtree|query> observation=<O> revision=<n> captured_at=<RFC3339> tree_scope=<scope> view=detailed [root=<ref>] [matches=<n> total=<n> truncated=<bool>] nodes=<n> entries=<m>`，
随后是一个空行与正文。
`tree` 输出整树，`subtree` 额外带 `root=`，`query` 按命中顺序输出
`match i/N path=[…]` 与完整命中子树，命中之间以空行分隔。`matches` 是本次返回条数，
`total` 是预算前命中数，`truncated=true` 表示引擎只返回了完整条目前缀。`nodes` 是正文
逻辑节点数之和；查询的嵌套命中按各自子树重复计数。`path` 是 root..parent 的 ref 序列，
不含命中节点本身。补看固定使用 detailed 视图：逐节点展开、不折 chain、不隐藏已知几何。

**预算与完整性。** MCP 不设文本大小上限，不产生 `omitted`。可读块能无损表示时完整输出；
非法 ref、缺少必要元数据、未知成员或其他不可表示情况以完整 `json v1` 兜底。树 ref 必须
满足 `n` 加非零十进制数字。服务的观察树与 detailed 全树上限为 128 KiB，subtree/query 为
32 KiB；引擎返回的 `truncated` 仍保留。块分别以 `end tree` / `end inspect` 结束。
缺失 `end result` 或 `end diagnostic` 表示对应文本被宿主截断；应改用宿主支持的
`structured`，或通过 `inspect` 的 node/query 收窄。`both` 同时发送两种完整编码，会增加
传输与上下文占用。

### 3.2 shell 与 timeline 块

```text
shell v1 termination=exited exit_code=0 truncated=false output_bytes=6
output "hello\n"
end shell

timeline v1 events=1
event {"at":"2026-11-10T03:00:00Z","phase":"session","code":"active"}
end timeline
```

shell 的 `output` 按换行分块，每块包含原换行并编码为 JSON 字符串，拼接后保留原输出；空串
有一行 `output ""`，`null` 对应 `output_bytes=null` 且无 output 行。`output_error` 使用
完整 JSON。timeline 的 event 为完整 canonical 对象，保持数组顺序与未来成员；timeline 为空
时按 JSON 兜底。shell 的额外成员按 JSON Pointer 输出。这些内容只进入响应，不写日志。

## 4. stdio

```text
device-mcp -transport stdio -config <private-config>
device-mcp -transport stdio -config <private-config> -result-mode text
```

- 每条工具调用在独立请求上下文中执行；宿主发送 `notifications/cancelled` 时，本次调用
  上下文被取消，由取消包装按第 6 节处理。
- 进程退出、stdin 关闭与连接断开都不证明设备动作已撤回或原执行链已结束。
- 默认模式按显式 `-result-mode` > `MOBILE_USE_MCP_RESULT_MODE` > `text` 选择；
  值仅接受 `both`、`text`、`structured`。显式空值、前后空白、大小写变体或非法值启动失败，
  退出码为 `2`，不会回退到环境变量。

## 5. Streamable HTTP

```text
device-mcp -transport http -config <private-config>
```

- 端点以 `POST` 承载请求与 SSE 响应；stateless 模式下 `GET`/`DELETE` 返回 `405`，不建立
  跨请求的 MCP 会话。进程只监听私有配置中的 loopback 地址，路径不作为授权依据。
- 每个请求必须携带 `Authorization: Bearer <mcp_token>`；缺失或错误返回 `401`，不进入
  工具调用。该令牌是宿主侧 MCP 凭据，与服务 `agent_token` 分离。
- 携带 `Origin` 的请求与私有配置的 `allowed_origins`（`scheme://host[:port]`）在忽略末尾
  一个或多个 `/` 后精确比较；其余差异返回 `403`。不含 `Origin` 的非浏览器客户端按
  已认证请求处理。
- 每请求使用独立临时 MCP 会话，不继承其他调用方的 MCP 会话、工具参数或结果；不跨 Agent
  串用归属。
- 每请求可用 `X-Mobile-Use-Result-Mode: both|text|structured` 覆盖进程默认模式；无此头时
  使用默认值。该头必须恰好一个值，空值、多值、逗号、前后空白与大小写变体返回 `400`，在
  工具执行前拒绝。它只在适配器内使用，不发送给设备服务，也不影响认证与归属。
- `2026-07-28` 的一次 POST 就是完整请求生命周期。客户端关闭响应的 SSE 流时，服务请求
  上下文被取消，按第 6 节处理；SSE 断开本身不证明设备动作已撤回。

## 6. 取消、P 与漏收边界

有本次新 P 的工具调用（`start`、`observe`、`snap`、`wait_for`、`act`、`keepalive`、`end`、
`app`、`notify`、`shell`）在取消时：

1. 停止等待本次调用结果；
2. 对需要 `end` 的分支用独立的有界上下文调用 `end(P)`（上限 5 秒），不等待设备工作完成；
   若服务已不认识原 P，没有原结果时保留其 `operation_unknown`、`request_closed=true`、
   `uncertain` 事实；已知原执行事实优先；
3. 保留取消前已取得的服务结果；没有结果时不伪造 `not_executed`；
4. 返回结构化取消结果，说明 P 与已发起的有界 `end(P)`，但不声称设备动作已撤回、资源已释放
   或原执行链已结束。未知 `end` 只封闭该 P 的未来接纳，绝不能用来结束当前其它会话。

`app` 整工具不是只读，七个 kind 都预分配 P，但取消策略按 kind 区分：

- `find`/`list` 查询取消不调用 `end`。结果保留已预分配 `operation_id` 与 `app.kind`，没有
  `outcome`、集合与 `recovery`。没有已知服务错误时生成 `error.code=call_cancelled`；已有
  服务错误则保留原 code/message/details。两种情况都在 `error.details` 注入
  `mcp.cancelled=true` 与受控文案 `mcp.cancellation="查询已取消；未发起 end"`。已知业务集合
  不因取消删除；集合未取得时不伪造 `items`/`tasks`。
- 五个变更 kind（`open`/`claim`/`handoff`/`close`/`kill`）按通用步骤 2 发起有界 `end(P)`，
  保留真实的 end 文案、session/cleanup 与恢复入口；不能把查询的“未发起 end”文案复制给
  变更，也不能省略实际发起的 end。

`wait_for` 不是只读工具：它按通用步骤 2 发起有界 `end(P)`，取消同时关闭等待上下文并释放
普通工作位。取消保留取消前已取得的 `wait` 事实，也不声称新观察已发布。

`snap` 预分配 P，但取消不调用 `end`：只撤销本次读取。即使适配器已经拿到成功提交结果，
取消分支也清空顶层 `image` 元数据与 `ImageContent`，返回 `call_cancelled`，不交付图片。
原会话仍活动；读取的其余撤销与收尾由服务端 STOP/end 语义处理。

`status`、`inspect`、`diagnose` 只取消本次读取，不登记 P、不调用 `end`。调用参数中的
`reference` 是原观察/操作定位，不是本次取消的 P；不得把它当成本次调用的操作编号。

取消结果一律 `isError=true`。适配器保留已有服务错误，缺少错误时合成 `call_cancelled`；`snap`
是唯一例外：取消分支无条件返回 `call_cancelled`，并丢弃已取得的图片。适配器还在
`error.details` 注入保留键 `mcp.cancelled=true`、`mcp.cancellation=<受控说明>`，两种
完整编码均包含它们。此命名空间只由 MCP 适配器使用，`/api/*` 永不产生。文本无额外顶层
`cancelled` 行；取消不改写原 `outcome`、观察与收尾证据。

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
- 宿主侧工具超时必须覆盖一次调用的完整预算：验证采集整体受 35 秒固定预留截止约束，因此带图
  与无图调用都按 `timeout <= host_timeout - 35s` 预留。宿主在到点时取消调用，适配器按第 6
  节发起有界 `end(P)`，被取消的等待不保证产出新观察。
- 构建与启动方式见 [运行手册](runbook.md) 的 MCP 章节。
