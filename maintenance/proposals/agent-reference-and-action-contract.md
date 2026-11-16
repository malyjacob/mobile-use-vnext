# 提案：统一 Agent 请求引用并收紧受控动作契约

状态：提案，未实现。
基线提交：`4fe270de7cd979cba9659a6662677b111028d4c6`（分支 `master`，`2026-11-17`）。
适用范围：`act`、`inspect`、`app` 的 HTTP 与 MCP 输入，`act` 的 tap/long_press 目标规划与 text 执行结果，MCP Instructions 中的动作调用决策，以及客户端、状态/重复 P、冻结结果和结果投影路径。
影响范围：`internal/protocol`、`internal/engine`、`internal/mcp`（含 server Instructions 与 act 描述）、`internal/client`、`scripts/mcp-probe`、相关测试、正式文档与最终共同发布清单；不修改 Android 业务逻辑、uiautomator JAR 或既有设备 RPC，共同版本生成的 Java/XML 和配套 APK 仍随整组发布更新。
契约（`/api/*` 字段与 `contract_version`）：改变。请求输入字段、固定 `app` Schema、`act.operation` 成员和 `Result.text_input` 均变化；目标契约仍为一次共同 `15 → 16` 升级，但本次提案落盘不改版本，正式升级只发生在第一、第二两批全部实现并验证后的唯一发布中。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/protocol/requests.go`、
`internal/protocol/surface.go`、`internal/protocol/encode.go`、
`internal/protocol/responses.go`、`internal/protocol/decode_response.go`、
`internal/engine/act.go`、`internal/engine/tap_scope.go`、
`internal/engine/operations.go`、`internal/engine/retirement.go`、
`internal/engine/ui_execution.go`、`internal/mcp/schema.go` 与 `internal/mcp/result_*.go`）。

前置提案为 [agent-output-and-wait-acquisition.md](agent-output-and-wait-acquisition.md)。第一份与本提案
互为链接；本提案对应第二批，但“先完成第一份”是用户选择的验收门禁，不是第二份各能力在代码逻辑上的硬依赖。

---

## 0. 摘要

本提案只改变 Agent 可见请求和受控动作事实，不扩大工具目录。

选定路线为：把 `act`、`inspect`、`app` 的同类输入统一为 `reference`，严格拒绝旧名和双传；
保留单一 `app` 工具、七个 `kind` 和 12 个固定工具，以扁平 Schema 表达字段、由服务端
`DecodeApp` 作为互斥权威；`open` 使用 `target.app`，`list` 继续使用顶层 `app` 作过滤。

`tap`/`long_press` 的 ref 目标新增默认开启的一跳 `bubble`（只借用 DOM 事件常用术语的命名）：
目标自身已有请求动作时仍走 direct；
只有原 child 完全没有 touch-interactive 动作时，才允许向最近祖先做一次安全目标提升，并始终同时
绑定原 child 与第一个交互祖先。`text` 默认包含自动聚焦、写入、精确读回和一次 ENTER；显式
`press_enter=false` 只做输入链路，不发送 ENTER。默认复合 text 始终用固定
`text_input:{write,enter}` 保存两个可观察原语，而焦点 tap 仍由整体 outcome 保守表达。

两个默认值都是减少 Agent 规划与往返的便利性取舍，不替代既有授权、目标身份、STOP、事实分层或
派发证据。默认值只在 `operation.type + target` 的适用分支计算 effective 值，不对 raw bool
做全局 true 替换。多行输入、草稿、逐字段填写等不应逐次提交的场景应显式
`press_enter=false`；这是调用选择，不需要因每次 text 动作重复向用户确认。默认 ENTER 只证明
按键原语；可依据后续观察中的具体页面事实判断业务结果，但不得追改原动作 outcome。

本提案不拆 `app`、不增加工具、不新增 JAR/RPC、不承诺 ENTER 完成业务提交，也不把进程重启后的
普通动作历史扩展为新的恢复账本能力。

## 1. 问题

分诊：[#17](https://github.com/malyjacob/mobile-use-vnext/issues/17)、[#18](https://github.com/malyjacob/mobile-use-vnext/issues/18)、[#19](https://github.com/malyjacob/mobile-use-vnext/issues/19)、[#23](https://github.com/malyjacob/mobile-use-vnext/issues/23) 均为取舍，改变当前合法契约及明确授权范围。当前严格解码的参数错误为 `422 invalid_request`（`internal/protocol/requests.go:676-678`、`internal/protocol/errors.go:276-278`），不照抄报告中 `400` 的表述；本提案不另改状态码。

### 1.1 现象与代价

同一“观察/操作引用”当前分成 `observation_id` 与 `reference` 两套请求名。宿主需要按工具记忆，
跨工具组合容易发送未知字段；#17 已提出统一命名。证据见
`internal/protocol/requests.go:318-349,656-674`、`internal/protocol/surface.go:163-280`、
`internal/mcp/schema.go:210-216,340-346,473-479`。

`app` 当前根 Schema 使用 `allOf + if/then/not`，嵌套 `target` 使用 `oneOf`；七个 kind 的可接受字段
只有在服务端解码时才完整落实。复杂结构增加公开描述和宿主 Schema 处理成本，但现有固定目录、
严格解码与 kind 级取消语义本身工作正常。证据见 `internal/mcp/schema.go:423-584`、
`internal/protocol/surface.go:163-307`、`internal/engine/app.go:13-68`。

`tap`/`long_press` 的 `target.ref` 当前要求目标自身带动作；无动作 child 虽可能提供
`nearest_actionable_ref` 引导，但 Agent 必须重新理解并改写目标。现有静态与新鲜树规划已经
具备 child 身份匹配、子树安全落点和祖先关系能力，可在不猜测业务语义的前提下提供默认的一跳
安全目标提升，并给调用方显式关闭入口。证据见 `internal/engine/tap_scope.go:722-765,782-844,906-954` 与
`internal/engine/act.go:446-664`。

`text` 当前在焦点 tap 后执行 SetText，再通过捕获、重新绑定和精确 readback 证明写入；
即使 readback 已等于请求文本，调用方仍需另发 `key=ENTER`。常见搜索、登录和单行表单由此多一次
规划与网络往返，且现有流程没有表达“写已确认、ENTER 尚未派发”“ENTER 可能已派发但结果未知”
等阶段事实。证据见
`internal/engine/act.go:820-944`；固定按键白名单已含 `ENTER`：
`internal/protocol/requests.go:130-134`。

### 1.2 证据链

| 事实 | 基线位置 |
| --- | --- |
| `act` 请求解码只承认 `observation_id`，应用通用 O/P 语法检查后才进入 engine | `internal/protocol/requests.go:318-349,631-641` |
| `inspect` 请求解码只承认 `observation_id`；engine 只从 `observations` 中查对象 | `internal/protocol/surface.go:407-450`、`internal/engine/inspect.go:48-97` |
| `app.list` 已使用 `reference`；`open/claim/handoff/close/kill` 使用 `observation_id` | `internal/protocol/surface.go:163-307` |
| 七个 app kind 的成员集合与工作/P 分派分别由 decode 和 engine 分支决定 | `internal/protocol/surface.go:285-309`、`internal/engine/app.go:27-68` |
| `find` 使用 detached P，`list` 绑定原会话；二者都无 action outcome | `internal/engine/app.go:27-51`、`internal/engine/app_query.go:12-21` |
| MCP 整工具不是 read-only，但查询 kind 的取消不执行 `end` | `internal/mcp/server.go:161-188` |
| 固定目录为 12 工具，探针禁止旧 `apps`/`open_app` | `docs/mcp.md:14-19`、`scripts/mcp-probe/checks.go:36-45` |
| 既有用户决策是单 `app`、七 kind、kind 严格分支；查询 P/取消语义也被专项评审确认 | `maintenance/proposals/implemented/unified-app-lifecycle.md:88-105,273-295,646-654` |
| static tap 失败发生在普通 work 与 O claim 之前 | `internal/engine/act.go:26-41,59-72`、`internal/engine/tap_scope.go:906-954` |
| 新鲜 tap 当前只匹配目标自身，并要求目标仍有对应 public action | `internal/engine/act.go:516-567` |
| text 先发送焦点 tap，之后才可能发送 SetText | `internal/engine/act.go:838-885` |
| 精确 readback 只有在候选唯一且值等于请求文本时才确认成功 | `internal/engine/act.go:893-944` |
| UI 派发前先持久化 `may_dispatch`，回执关闭同一窗口但不证明业务成功 | `internal/engine/ui_execution.go:84-178` |
| `phase` 会被 `setOperationPhase`/`finishOperation` 改写 | `internal/engine/operations.go:159-199` |
| 冻结覆盖 phase/outcome/error，且可能优先采用 `KnownOutcome` | `internal/engine/retirement.go:175-205` |
| 迟到结果由冻结视图替换，不能依赖原始临时 Result | `internal/engine/retirement.go:528-544` |
| `status(P)` 与重复 P 通过 operationState 的固定投影复制已知事实 | `internal/engine/engine.go:1154-1216` |
| `error.details` 可能由后来的 finish/freeze 覆盖，不能作为阶段事实存储 | `internal/engine/operations.go:170-199`、`internal/engine/retirement.go:197-205` |
| Result 编码、响应解码、MCP canonical/text 与 manifest 各有独立门禁 | `internal/protocol/responses.go:655-770`、`internal/protocol/decode_response.go:456-519`、`internal/mcp/result_text.go:26-74`、`internal/mcp/result_manifest_test.go:91-96,156-174` |

### 1.3 为什么直觉会出错

- 统一字段名不等于扩大引用语义。`act` 以及 `app` 的五个变更 kind 仍必须解析为 ready O；
  `inspect` 仍必须解析为保留 O；只有 `list` 等原本接受 O/P 的路径保持原范围。
- 接受 `observation_id` 与 `reference` 两种名字可以减轻迁移，但会留下永久双契约、两套反例和
  “未知字段本应被拒绝”的歧义，因此本提案不提供别名。
- `app` 扁平化只是把 Schema 表达改成宿主更稳的字段集合；互斥仍由服务端严格解码执行。去掉
  `oneOf` 不能退化成“字段只要存在就接受”。
- 这不是推翻当初“单 app + 七 kind”的用户决策：工具、kind、查询 P/取消/无 outcome 均保留，
  只收敛宿主 Schema 形状，并把 `open` 的包名按既有 target 风格移入 `target.app`。
- 最近祖先回退不是把 child 无条件替身为任意可点击容器。原 child 和第一个交互祖先必须同时
  唯一、保持父子关系并共同证明安全落点；原 child 丢失时不能仅凭祖先身份继续。
- 默认 `bubble` 不是 DOM 式事件传播。它只在 direct 不可用时完成 C→最近交互祖先 A 的一次目标提升，
  只派发一次 A 安全点，不把同一事件继续传给多个祖先。
- 默认 `press_enter` 不是新增一个可盲发的 submit 动作。ENTER 只能在同一次调用、同一个 work/P、
  精确写回读成功、最终派发门禁通过后发送一次；显式 false 明示调用方只要求写入。
- `phase` 和 `error.details` 都不能承载 text 阶段事实：它们会被 finish、freeze 或迟到结果覆盖。
  阶段事实必须进入 `operationState` 的专用有界字段并由统一 operation view 投影。

## 2. 目标与非目标

目标：

1. `act`、`inspect`、`app` 的统一输入字段为 `reference`，旧 `observation_id` 被严格拒绝。
2. 保持 12 工具、单一 `app`、七个 kind；MCP 使用扁平、封闭 Schema，服务端严格成员矩阵不变。
3. `app.open` 使用 `target.app`，`app.list` 保留顶层 `app` 过滤；查询 P、取消和 IsError 语义不变。
4. `tap`/`long_press` 的 ref 目标默认使用 `operation.bubble=true` 的一跳安全目标提升；显式 false
   关闭，坐标目标省略该字段且不触发祖先逻辑。
5. `text` 默认 `operation.press_enter=true`，形成自动聚焦、写入、精确读回和至多一次受控 ENTER
   的复合流程，并用固定 `text_input` 暴露 write/enter 阶段；显式 false 只做输入链路。
6. 首次结果、运行中 `status(P)`、重复 P、冻结结果、文本与 structured MCP codec 对同一有界快照保持一致。
7. 所有 B 变更完成后只做一次契约、目录、manifest 与正式发布升级。

非目标：

- 不把 `act` 或 `inspect` 的语义扩展为 P；不改变 O/P 的租期、归属、单向消费或取消规则。
- 不拆七工具、不新增 `app_query`、不增加第 13 个工具，也不恢复 `apps`/`open_app`。
- 不采用 `oneOf`、`allOf`、`if/then/not` 作为 `app` 输入 Schema 的互斥手段。
- 不把祖先回退扩展到坐标目标、swipe、scroll、key、text，或越过第一个交互祖先。
- 不新增 `text.submit`、`key.submit`、任意 key 参数或单独 ENTER 工具。
- 不修改 Android App 业务逻辑、JAR、uiautomator RPC、键码集合或 shell 能力；版本常量仍由唯一版本源生成。
- 不保证 ENTER 导致表单提交、导航或业务成功，也不重放任何动作。
- 不增加跨进程 text 阶段恢复记录、普通动作历史账本或日志正文。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保留双字段、当前 `app` 组合 Schema、无 `bubble`、无 press_enter | 现有行为稳定 | 宿主继续记两套字段；复杂 Schema 与额外 tap/ENTER 往返保留；冻结时文本阶段仍不可表达 |
| R2 本提案 | #17 硬切 `reference`；12 工具扁平 app；ref tap/long_press 默认 `bubble`；text 默认复合 `press_enter`；新增有界 `text_input` | 一次协调契约、客户端、探针和文档迁移；普通调用更方便，响应增加复合阶段事实 | 任一路径把省略误判为 false、漏存阶段快照或把阶段当业务成功时，严格测试必须阻止合入 |
| R3 别名与 14 工具拆分 | 接受两种引用名；拆成 `app_find`、`app_list`、`app` | 首轮迁移较缓，单工具 Schema 可更小 | 永久兼容面和固定目录膨胀；若同时去查询 P，还将推翻既有统一 app、查询取消语义 |
| R4 `app` 使用 `oneOf` | 以七个分支表达 kind 互斥 | Schema 静态约束更强 | 当前没有复杂宿主兼容证据；扁平根与严格 decoder 已足以作为权威，额外换形无收益 |
| R5 未选的默认关闭路线 | 两个能力都省略为关闭、显式 true 才开启 | 契约保守 | Agent 仍需先识别无动作 child、再重写 ref；普通 text 仍要额外 ENTER 往返，默认路径没有解决主要成本 |
| R6 用 phase/details 表示 text 阶段 | 不新增响应字段，把 write/enter 写入 phase 或错误详情 | 响应形状看似更小 | finish/freeze 覆盖旧值，迟到结果替换原 Result；status、重复 P 与 MCP codec 无法稳定复原 |

选择 R2。默认 true 是用方便性换取更大的默认复合行为：ref 目标的常见 tap/long_press 和无障碍
child 不再要求 Agent 先找祖先；普通 text 不再要求先独立 tap 再补 ENTER。代价是有动作目标仍要
经过 direct 判定、无动作 ref 可能在 direct 静态失败后多一次有界祖先规划，普通 text 默认多一次
ENTER 派发与一个小型固定响应对象。child+A 双绑定、Role/Label、STOP、阶段快照和派发证据均按
原提案强度保留，默认变宽不会降低安全门槛。显式 false 为多行、草稿和逐字段输入保留输入链路；
调用方按任务意图选择，不把每次字段输入变成用户确认流程。

它把用户已确认的四项契约放进一次迁移，保持工具和能力边界最小。无法在现有 phase/error 上安全
编码复合 text 事实，且不能把“焦点 tap 已发送”伪装成两键的已知状态。

## 4. 判据

### 4.1 统一 `reference`

公开请求字段统一如下：

| 工具或 kind | 字段与语义 |
| --- | --- |
| `act` | `reference`：必须是当前 ready O；不因改名接受 P |
| `inspect` | `reference`：必须解析为保留中的历史 O；P 仍不可用 |
| `app.find` | 无引用字段 |
| `app.list` | `reference`：当前 ready O，或原实现接受的、可解析到本 Agent 活动原会话的 P |
| `app.open/claim/handoff/close/kill` | `reference`：必须是当前 ready O |
| `observe/wait_for/keepalive/end/shell` | `reference`：保持现有 O/P 范围 |
| `status/diagnose` | 可选 `reference`：保持现有 O/P 范围 |

只统一请求名，不重命名以下既有输出身份：

- `inspection.observation_id` 继续表示被查看的观察。
- 节点选择器继续为 `target.ref`。
- recovery 枚举继续为 `reference=O/P/none`。

`observation_id` 在 `act`、`inspect` 和 `app` 请求中必须作为未知字段被 strict decoder 拒绝；
同一请求同时出现旧名与新名、大小写变体、重复键、显式 null、错误类型或尾随第二个 JSON 值均拒绝。

### 4.2 固定 12 工具和扁平 `app`

MCP 目录严格保持：

```text
start status observe wait_for act inspect keepalive end app notify shell diagnose
```

`app` 根 Schema 使用一个扁平对象：

```text
type=object
properties={kind,query,reference,scope,app,target,restore_foreground,
            allow_borrowed,allow_stop_playback}
required=[kind]
additionalProperties=false
target.type=object
target.properties={task_id,app}
target.additionalProperties=false
```

`app` 输入 Schema 内不使用 `allOf`、`if`、`then`、`else`、`not` 或 `oneOf`。
根字段可被 Schema 看见，不表示每个 kind 都可接受；`DecodeApp` 的封闭成员矩阵是互斥权威。

| kind | 必填（除 kind） | 可选与默认 | 禁止或额外语义 |
| --- | --- | --- | --- |
| `find` | 无 | `query`，≤256 Unicode 码点 | 禁 `reference/scope/app/target/restore_foreground/allow_borrowed/allow_stop_playback` |
| `list` | `reference` | `scope=session` 或 `primary`；顶层 `app` 作为明确包名过滤 | 禁 `query/target/restore_foreground/allow_borrowed/allow_stop_playback` |
| `open` | `reference`；`target={app:包名}` | 无 | 禁顶层 `app`、`task_id` target、`scope/query` 和三组动作选项 |
| `claim` | `reference`；`target` | target 为 `{task_id}` 或 `{app}`，恰好一种 | 禁顶层 `app/scope/query/restore_foreground/allow_borrowed/allow_stop_playback` |
| `handoff` | `reference`；`target` | target 同 claim；`restore_foreground=false` | 禁顶层 `app/scope/query/allow_borrowed/allow_stop_playback` |
| `close` | `reference`；`target` | target 同 claim；`allow_borrowed=false` | 禁顶层 `app/scope/query/restore_foreground/allow_stop_playback` |
| `kill` | `reference`；`target={task_id}` | `allow_stop_playback=false` | 禁 `app` target、顶层 `app/scope/query/restore_foreground/allow_borrowed` |

`open.target.app` 与 `list.app` 都使用同一明确包名校验；前者是操作目标，后者仍是查询过滤，
不得混用。target 同时出现 `task_id` 与 `app`、都不出现、task ID 非正整数或 app 非明确包名，
均按 `422 invalid_request` 拒绝。

`find/list` 继续各分配本次调用的 P；`find` detached，`list` 绑定解析到的原会话。二者不消费 O、
不续租、无 outcome、取消不发 `end`。该规则由 kind 判定，不因 MCP 工具仍是 `app` 或 Schema
改为扁平而改变。

### 4.3 `operation.bubble`

请求规则：

- `operation.bubble` 仅适用于 `tap`、`long_press` 且 `target={"ref":"n..."}`。
- 名称只借鉴 [WHATWG DOM Events 的 `bubbles` 术语](https://dom.spec.whatwg.org/#introduction-to-dom-events)；
  DOM 的事件路径可经过多个祖先并通知多个 listener，本提案不采用该传播语义，只做一次目标提升并
  只向 A 的安全点派发一次 tap/long_press。
- 成员省略时该适用分支的有效值为 true；显式 true 与省略等价，显式 false 关闭祖先提升。
- 坐标 target 可以完全省略 `bubble`，此时不产生默认值，也不进入任何祖先逻辑；坐标 target 显式
  携带 true 或 false 均返回 `422 invalid_request`。
- 其他 operation type 不适用该默认；显式 true 或 false 均返回 `422 invalid_request`。
- 显式 null、非 boolean、重复键、大小写变体或旧 `allow_ancestor_fallback` 均由 strict decoder
  拒绝；旧长字段不留别名、不映射到 `bubble`。
- 有效值为 false 时完全沿用现有直接目标算法与错误。

有效值为 true 且原目标已有请求动作时，仍使用既有直接目标算法，不提升祖先。只有原目标完全没有
touch-interactive 动作时才允许做一次安全目标提升，并同时证明：

1. 原 child 在观察树中存在，不是 context，且不具任何独立 touch-interactive 能力（tap/long_press/text）；它仍是授权选择器，保持 `tapGuidanceFor` 当前对非交互 child 的限制。
2. 从 child 向根方向遇到的第一个 touch-interactive 祖先 A 唯一支持该 tap/long_press，
   启用、有界、可见且有有效 bounds；不得越过 A 再找更远的可点祖先。
3. child 到 A 的父子关系完整；在 A 的作用域内能生成唯一、可证明且不落到另一独立交互
   child 或更远祖先的固定安全点。
4. 只有 child 存在且有效、但回退证明不成立（首交互祖先不支持动作、无可证明安全点等）才返回 `422 unsupported_node_action`。child 不存在、已禁用、层级损坏分别保留当前 `409 node_not_found`、`409 node_disabled`、`503 hierarchy_unavailable`；均不得认领 O 或派发。

claim 后必须重新匹配原 child 与原 A 两者：

- child 或 A 任一不为唯一候选，返回 `409 target_changed`。
- 原 child 的公开 Role 和完整 Label 必须与授权观察一致；即使资源标识、位置或祖先不变，可读内容被替换也返回 `target_changed`，不得仅凭 A 或复用后的资源 ID 认作原内容。
- fresh A 必须仍是 fresh child 的祖先，且仍是 child 向上遇到的第一个交互祖先。
- child 不得在 fresh 树中新增任何独立 touch-interactive 能力（tap/long_press/text）；A 必须仍有对应动作与有效边界。
- 安全点必须在 A 作用域内唯一，不能解析到同容器另一个交互 child 或更远祖先。
- 原 child 丢失时，即使 A 能独立唯一匹配，也必须拒绝；禁止把授权静默解绑为 A。

`bubble` 只表示上述一次 C→A 安全目标提升。它不模拟 DOM 事件冒泡或捕获，不把同一动作依次发送给
多个祖先，也不因默认开启而放宽“第一个交互祖先”和 child+A 双绑定。上述身份只用于本次请求的
规划与重验，不进入公共树、账本、日志正文或恢复语义。

### 4.4 `press_enter` 与固定 `text_input`

请求规则：

- `operation.press_enter` 仅 `text` 接受。成员省略时有效值为 true；显式 true 与省略等价。
- 已接纳且未指定或有效值为 true 的 text 执行“自动聚焦 → SetText → 精确 readback → 至多一次 ENTER”
  复合流程，并始终携带下述 `text_input`。
- 显式 false 只执行“自动聚焦 → SetText → 精确 readback”，不发送 ENTER，也不新增
  `text_input`；首次结果、status/重复 P 和冻结结果都保持该分支既有响应形状。多行输入、草稿和
  逐字段填写应选择 false，Agent 按用户意图决定，不为每个字段重复索要确认。
- 显式 false 必须由 client 编码为 `false`，不得因 Go bool 零值而省略。显式 null、非 boolean、
  重复键、大小写变体或用于其他 operation type 均返回 `422 invalid_request`；其他 type 不存在
  可生效的 `press_enter` 默认值。
- 有效值为 true 且动作已接纳时，Result 必须在本调用后续首次结果、status/重复 P 与冻结结果中
  保持固定对象：

```json
{"text_input":{"write":"not_executed","enter":"not_executed"}}
```

`text_input` 的 `write`、`enter` 均必须出现，枚举仅为
`not_executed|uncertain|executed`；不接受 null、未知值、单键对象、额外成员或省略其中一个键。
“尚不知道”必须写 `uncertain`，不得靠省略表达。

令 N/U/E 分别表示上述三个枚举，合法组合仅为 `(N,N)`、`(U,N)`、`(E,N)`、`(E,U)`、`(E,E)`。ENTER 非 N 必须有 write=E；其他组合由响应严格解码和 MCP 投影一致拒绝，不能靠调用方猜测阶段顺序。

`write` 只陈述 SetText 及精确 readback：

- 未授予 SetText 派发许可且无该步骤未结派发，或已有确定未派发回执时为 `not_executed`。
- 授予该步骤派发许可时，在同一短锁转换中推进为 `uncertain`；完整证据成立前不改成 `executed`。
- SetText 已取得现有要求的终止回执、唯一候选、Issued/Accepted，随后取得候选唯一且值精确相等的独立 readback，并通过提交门禁，才为 `executed`；不能仅凭页面原本已有同文值作确认。

`enter` 只陈述一次 `KEYCODE_ENTER`：

- 未授予 ENTER 派发许可且无该步骤未结派发，或已有确定未派发回执时为 `not_executed`。
- 授予该步骤派发许可时，在同一短锁转换中推进为 `uncertain`；派发回执/最终确认不完整时保持。
- 回执已确认输入链终止且最终门禁通过后为 `executed`，仅表示 ENTER 已受控派发，不表示业务提交。

焦点 tap 不属于 `text_input` 两键；它是额外 UI 原语，必须进入内部阶段快照和整体 outcome 计算。
该累计事实保存在 `operationState` 的专用内部阶段对象，focus 固定为 `not_attempted|not_executed|uncertain|executed`，连同 write/enter 与 enterAttempted 一并有界保存。后续 `work.UI` 替换 token 不得抹去早先 focus 事实；focus 不进入公共 text_input 或跨进程普通历史账本。
即使焦点 tap 已派发后 SetText/readback 失败且两键均为 `not_executed`，整体 outcome 仍为
`uncertain`。

### 4.5 raw presence、effective 默认与入口一致性

Boolean 默认值不是对 raw bool 做一次全局替换，而是先保留 `{present,value}`（Go 可用 pointer 或
独立的 `Has` 标志），再按 operation 分支求 effective 值：

| 分支 | 成员省略 | 显式成员 |
| --- | --- | --- |
| `tap/long_press + target.ref` | `bubble` effective=true | true/false 分别得到 effective true/false |
| `tap/long_press + target.coords` | 允许省略；`bubble` 对该分支不生效，继续现有点算法 | 即使 false 也返回 `422 invalid_request` |
| `text` | `press_enter` effective=true | true/false 分别得到 effective true/false |
| 其他 operation type | 两个成员均可省略；对应默认值不生效 | 携带任一成员均返回 `422 invalid_request` |

HTTP 解码、MCP 参数转换和直接调用 engine 的内部请求入口必须复用同一个 Effective 计算，不各自另写
“缺省 false”或“字段出现即 true”的判断。规范化的输入至少同时保留 raw presence 与 effective
值，engine 的后续授权只读取 effective 值；它仍独占会话、O/P、work 与派发权威。MCP Schema 的
字段说明也必须表达分支条件，不能让一个通用 `default:true` 看似适用于坐标 target 或无关动作。

client/Go 编码不得把零值作为“未设置”：调用方未提供时省略成员，显式 false 必须输出 `false`。
`text_input` 的保留条件只读取 effective `press_enter=true`，因此字段省略的普通 text 必须携带它；
判据不能退化为“raw 成员实际出现”。

### 4.6 整体事实与版本边界

`phase` 只表示流程阶段，不表示 write/enter 事实。`error.details` 不得作为阶段载体。

对省略 `press_enter` 或有效值为 true 的已接纳 text 动作：

- `executed` 当且仅当 `write=executed` 且 `enter=executed`。
- `not_executed` 仅当包括焦点 tap、SetText 和 ENTER 在内的全部输入原语均未获派发许可，或已有确定未派发回执；readback 是观察证据，不是输入原语。
- 其他情况一律 `uncertain`，包括焦点 tap 已派发但两键未派发、`write=executed` 而
  `enter=not_executed`、或任一原语跨过 may_dispatch 后证据不足。

`press_enter=false` 不进入上述复合事实视图，也不初始化 `text_input`；它继续使用既有 text 整体
outcome、focus/write 证据和派发门禁，绝不会因省略 ENTER 而产生新的“未提交”告警。两种分支都只
证明服务实际派发的输入原语，后观察也不能把 ENTER 已派发改判为业务成功。

`operationState.KnownOutcome` 只有 write 与 enter 两步骤整体确认 executed 后才可设为
`executed`；不能仅因文字 readback 成功就提前设置。

本提案导致的普通请求/响应变化共同进入一次 `contract_version 15 → 16`。本次落盘不改版本源；第一、第二两批功能完成并通过相关验证后，在最终版本冻结步骤仅改一次 `release/versions.json` 并生成常量，再同步文档、完成独立文档审计与实现提交。最终从干净已提交源码构建、生成和核验 manifest，不能在清单核验后再改版本。

## 5. 算法

### 5.1 共享请求解码

```text
sharedResolveEffective(rawOperation) is used by:
    HTTP request decode
    MCP arguments conversion
    direct engine request entry

parse request with strict unknown/duplicate/null/type checks
if act/inspect/app contains observation_id:
    reject 422 invalid_request
if act/inspect/app contains the new reference:
    decode once and retain channel-specific semantic range
decode operation.bubble as optionalBool{value,present}
decode operation.press_enter as optionalBool{value,present}

if bubble present:
    require type in {tap,long_press} and target form is ref
    explicit false -> effective bubble=false
    explicit true  -> effective bubble=true
else if type in {tap,long_press} and target form is ref:
    effective bubble=true
else:
    bubble has no effective value

if type=text:
    press_enter absent -> effective press_enter=true
    press_enter present -> effective press_enter=decoded value
else if press_enter present:
    reject 422 invalid_request

return raw presence + branch-qualified effective values
```

服务端校验与 canonical client encoder必须接受和拒绝同一组样本。仅改字段名，不把原先不可用的
P 变成可用。HTTP、MCP 与直接 engine 入口必须进入同一个 sharedResolveEffective；入口层可以
复用原始对象或构造相同 raw presence，但不能各自决定默认。engine 在 claim/派发前只消费
branch-qualified effective 值，同时保留 raw presence 供严格契约和编码往返。
`optionalBool.present` 必须来自实际来源，不能让 Go bool 零值同时代表“省略”和“显式 false”；
canonical encoder也必须输出显式 false，而不是按零值省略。有效默认只在 `type+target` 进入适用
分支后计算，绝不能先把所有省略 raw bool 改成 true。

### 5.2 bubble 一跳目标提升时间轴

```text
t0 immutable O content
   resolve child C
   if C directly supports action:
       use existing direct plan
   else if effective bubble=false:
       preserve existing 422
   else:
       find nearest-first interactive ancestor A
       require A supports action and has a unique safe point
       freeze plan={C,A,point,strategy}
       failure -> 422, no claim

t1 acquire work -> claim ready O
   failure -> existing busy/reference behavior

t2 fresh capture
   match C uniquely; failure -> target_changed
   match A uniquely; failure -> target_changed
   prove A ancestor of fresh C
   prove A is nearest interactive ancestor and C did not gain touch interactivity
   prove C public Role and full Label unchanged
   recompute/prove one safe point in A scope
   any mutable STOP/owner/session/display/deadline failure -> existing control error

t3 beginUI, dispatch exactly one tap or long_press at frozen point
   no retry, no rebinding
```

静态失败发生在 `claimObservationLocked` 前，因此 O 保持 ready。claim 后失败沿用现行
`target_changed`/新观察交付边界，但不能因 A 仍唯一而绕过 C 丢失。该时间轴至多尝试 C 和 A 两个
身份，不遍历第二个祖先；direct 成功时默认 `bubble` 不产生额外祖先动作。

### 5.3 text 默认复合与显式输入-only 时间轴

```text
accept text action, compute effective_press_enter from presence:
  if effective_press_enter=true:
    within the same e.mu critical section:
      grant work and successfully claim O
      initialize operationState private stage={focus=not_attempted,
                                              write=not_executed, enter=not_executed}
      publish public TextInput snapshot {write:not_executed,enter:not_executed}
  else:
    follow existing text claim/outcome path and existing focus/write evidence
    never initialize or publish the new TextInput object
  unlock; only then may device work begin

focus tap:
  durable may_dispatch outside e.mu
  effective_press_enter=true -> retain private focus=uncertain atomically with final dispatch authorization
  dispatch once
  record focus may/terminated/not_dispatched/unknown
  continue to binding only after a confirmed terminated receipt

SetText:
  bind fresh target and require candidate count=1
  durable may_dispatch outside e.mu
  if effective_press_enter=true:
    publish write=uncertain atomically with final dispatch authorization
  dispatch once
  definite not-dispatched:
    effective_press_enter=true  -> publish {write=not_executed,enter=not_executed}
    effective_press_enter=false -> keep public TextInput absent
    finish uncertain if focus dispatched
  poll bounded captures/readbacks
  accepted SetText receipt + exact unique readback + commit gate:
    effective_press_enter=true  -> publish write=executed, continue to ENTER
    effective_press_enter=false -> keep existing text outcome; finish without TextInput
  any incomplete evidence:
    effective_press_enter=true  -> publish write=uncertain
    effective_press_enter=false -> preserve existing text uncertain outcome without TextInput

if effective_press_enter=false:
  never authorize or dispatch KEYCODE_ENTER
  never publish Result.text_input

ENTER:
  require effective_press_enter=true and write=executed
  lock and prove same P/work/owner, active session, same display generation,
     deadline valid, not revoked, enterAttempted=false
  set enterAttempted=true
  durable may_dispatch outside e.mu
  recheck final authorization and publish enter=uncertain in the same short lock
  dispatch KEYCODE_ENTER once
  unusable/unknown receipt -> enter=uncertain, finish uncertain
  definite not-dispatched -> enter=not_executed, finish uncertain because write executed
  terminated + final owner/revocation gate:
    atomically publish enter=executed and set KnownOutcome=executed under one lock
```

若最终门禁在 ENTER 的 may_dispatch 前拒绝，保存 `write=executed,enter=not_executed`，整体为
`uncertain`。若焦点 tap 之后设备捕获失败而尚未 SetText，保存两键 `not_executed`，整体仍为
`uncertain`。所有“派发一次”均由同一 P/work 的幂等边界保证，迟到回调不能补发第二次 ENTER。

显式 `press_enter=false` 的自动聚焦、SetText 与精确 readback 门禁不降低；成功时只表示输入链路
已确认，不等待、不推断也不执行提交。该分支不创建公开阶段对象，避免把调用方主动选择的 input-only
流程误报成遗漏提交。

阶段快照只记录当前进程已经获得的证据，不是重试授权。`write=executed,enter=not_executed`
不得自动补发 ENTER，也不回滚文字或重打 SetText；调用方可先取新 O，再按新状态决定是否单独
发送 key。`enter=uncertain` 时，新动作前必须先观察业务/页面状态，防止重复 ENTER。
ENTER 只绑定同一 display generation；readback 与 ENTER 之间 App 仍可异步改焦点或页面，
因此两个阶段事实不构成原子业务提交承诺。在复合 text 操作尚未冻结时，若并发提交失败且
已有 primitive 跨过 `may_dispatch`，先在该操作可写的窗口内提交 `uncertain`；冻结先赢后，
不得再用迟到提交改写快照或整体 outcome。

### 5.4 快照、状态与冻结

```text
commitStage(snapshot):
  lock e.mu
  require operation still running, same P/work/owner and active original session
  require matching epoch/display ID/generation, deadline valid, not revoked
  copy-on-write operationState.TextInput
  if both steps confirmed: set KnownOutcome=executed within this same critical section
  unlock

operationView:
  copy TextInput snapshot only if kind=act, type=text, and effective_press_enter=true accepted
  never infer it from phase/error/details

freeze:
  lock e.mu
  freeze terminal state once
  retain last committed TextInput snapshot unchanged
  derive overall:
    executed only if KnownOutcome was set after both primitives confirmed
    not_executed only if cumulative UI evidence proves all primitives not dispatched
    otherwise uncertain
  unlock

late handler:
  commitStage fails after freeze
  applyFrozenResult replaces temporary Result with operationView
```

运行中 `status(P)` 和重复 P 只复制最后已提交的不可变快照；阶段可随新证据推进，但每次观测到的
对象完整且一致。freeze 先到后，任何并发完成或失败都只能保留冻结时的事实，不能回补。

成功 claim 与默认复合 text 的初始内部/公开快照必须同锁提交：省略/true 分支在 claim 后到首个
输入前即使 STOP 先赢，冻结视图也必须带完整 `(N,N)`。显式 false 分支和 claim 前拒绝保持既有
响应形状，不带 `text_input`。freeze 根据 operationState 的累计 focus/write/enter 事实推导整体
结果，不依赖最后一个 work.UI token。

派发许可与对应 `uncertain` 快照必须在同一锁内转换，禁止 Android 命令已可派发而 status/freeze 仍看到 `not_executed`。账本持久化和设备 I/O 均在锁外；持久化失败沿用确定未派发路径，阶段提交失败后不得继续派发。`enter=executed` 与 `KnownOutcome=executed` 同时提交，防止冻结插入二者之间导致两键已确认而整体仍未知。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/protocol/requests.go` | `act` 改 `reference`；`rawOperation` 增加 presence-aware 布尔；集中实现 HTTP/MCP/直接 engine 共用的 Effective 解析与分支校验 |
| `internal/protocol/surface.go` | `inspect/app` 改 `reference`；删除 app 请求 `observation_id`；平坦化 target 输入；按七 kind 保持封闭矩阵 |
| `internal/protocol/encode.go` | canonical client 输出新字段；保留两个 boolean 的显式 false presence；禁止 bool 零值驱动省略 |
| `internal/protocol/responses.go` | 新增固定 `TextInputView` 与 `Result.text_input`，固定 `write`、`enter` 顺序和枚举 |
| `internal/protocol/decode_response.go` | 严格解码并拒绝不完整/未知 `text_input`，保真服务响应 |
| `internal/client/client.go` | 继续为 `act/app` 预分配 P；使用 Has/pointer 保留省略与显式 false，经共享 Effective 解析，不新增重试或工具方法 |
| `internal/mcp/schema.go` | 12 工具不变；`app` 改扁平 Schema；`act` 请求改 `reference`，声明按分支解释的 `bubble` 与 `press_enter`；Server Instructions 写入调用决策；不引入组合器 |
| `internal/mcp/server.go` | 保持 kind 级 app 取消/IsError；准备函数传递新请求字段 |
| `internal/mcp/result_text.go` | 在固定位置投影 `text_input.write/enter`，与 canonical structured 保持同源 |
| `internal/engine/tap_scope.go` | 新增 ref 目标默认 bubble 的一跳 nearest ancestor 静态规划；固定 child+ancestor 双绑定 |
| `internal/engine/act.go` | 只消费共享解析后的 branch-qualified effective 值；双绑定重验与安全点；text 焦点/SetText/readback/ENTER 的有界流程；只在整体确认后设置 KnownOutcome |
| `internal/engine/ui_execution.go` | 复用现有持久 `may_dispatch`；累计每个 UI 原语证据，供 text 整体 outcome 使用 |
| `internal/engine/operations.go` | 新增锁内不可变 stage 提交 API；finish 不得用 phase/error 推导 text 阶段 |
| `internal/engine/act.go` 的接纳临界区 | 默认复合 text 成功 claim、初始私有 focus 状态和公共 text_input 快照同锁初始化；显式 false 不初始化公共对象 |
| `internal/engine/retirement.go` | 冻结时保留 stage 快照并采用保守的总 outcome；迟到提交失败 |
| `internal/engine/engine.go` | `operationViewLocked` 统一投影 `TextInput`，供 status/重复 P/冻结结果使用 |
| `internal/engine/status.go` | 保持只读且不采集设备；运行中 P 视图携带当前快照 |
| `scripts/mcp-probe` | 更新 act/app 请求样例、12 工具 Schema 断言、text_input typed/text 双 codec 与 manifest fixture |
| `docs/interface.md`、`docs/mcp.md`、`docs/architecture.md`、`docs/integration.md` | 实现后仅同步已落地契约；本次不改 |

### 6.2 配置与取值

| 参数 | 取值 | 校验 |
| --- | --- | --- |
| `operation.bubble` | 仅 tap/long_press + target.ref；省略有效 true，显式 false 关闭 | 坐标 target 必须省略，显式 true/false 均 422；其他 type 带成员均 422 |
| `operation.press_enter` | 仅 text；省略有效 true，显式 false 只输入 | 其他 type 带成员即 422 |
| `text_input.write/enter` | 三个固定枚举 | 对象两键同时存在；不 nullable、不允许未知值 |
| app `scope` | `session` 或 `primary` | 仅 list 接受；默认 session |

两个 boolean 都必须先用 presence-aware 原始结构解码，保留“成员是否出现”。不能使用单个 Go
bool 的零值兼表省略与 false：省略的来源是 `present=false`，显式 false 的来源是
`present=true,value=false`。完成 operation type 与 target 形式校验后再计算有效值：
`bubble` 只在 ref tap/long_press 分支省略为 true，`press_enter` 只在 text 分支省略为 true；
适用分支外没有默认值。canonical encoder必须保真 `present=true,value=false`，不能以
`omitempty` 或零值判断把它丢掉。presence 只用于严格契约和编码，不进入日志正文。

### 6.3 日志与可观测性

只允许记录 `operation_id`、动作枚举、目标策略枚举、三个固定 stage 枚举、是否已派发、
是否完成、耗时和既有受控错误码。禁止记录 text 正文、树/差异、未经白名单批准的包名、节点 ref、坐标、
祖先链、命令或原始响应。

默认复用既有事件；若确需增加 `act.text_input` 或目标策略字段，必须同步 `internal/eventlog/log.go` 的字段白名单及测试，值只能是已批准的 ID/封闭枚举/布尔/数值，不能把新增正文当诊断信息。focus 只允许受控枚举，不能记录原始回执、文本或树。现有 `ui.window_closed` 保留，用于物理输入窗口事实。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一执行权威 | 仍只在 engine；MCP/client 不保存第二份执行权威 |
| O 单向消费 | 字段改名不改变；祖先静态失败不 claim，claim 后失败不归还 |
| STOP 不等待 | freeze 先提交终态与 stage 快照，迟到 handler 不得回补 |
| 条件化提交 | stage 和整体事实均在锁内重验 work/P/owner/session/display/deadline 后推进 |
| 严格接入 | 旧字段、别名、组合器绕行、null、错误类型和不完整 text_input 均拒绝 |
| 事实分层 | write、enter、focus 副作用、整体 outcome、后观察分别表达 |
| 日志白名单 | 只记枚举/ID/布尔/耗时，不新增正文 |

### 6.5 契约与文档影响

本提案修改 HTTP/MCP 请求和 Result，必须进入共同 contract 16。MCP 工具数量仍为 12，但描述或
Schema 变化会改变目录指纹；最终 manifest 必须按新契约与新指纹一次生成。

实现提交应先完成代码与测试，再同步正式文档并执行独立只读文档审计。提案归档、验证记录和
共同发布只在该实现与审计完成后进行。

### 6.6 第二批最终 Instructions 必须覆盖

最终 server `Instructions` 与第一份提案的 #16 共用同一份源文本。通用 tree、O/P、租期与恢复
正文以 [agent-output-and-wait-acquisition.md](agent-output-and-wait-acquisition.md) §4.2 的同源
规范为准；本小节只规定第二批动作决策，不在本文件另写一套恢复表。

Instructions 与全部 12 个工具的 description 统一面向 caller-agent：帮助选择工具、填写必要参数、
理解结果并采取下一步。公共规则只在 Instructions 解释一次；每个工具用短段说明用途、关键默认/
限制和常见恢复，不展开匹配算法、证明过程、持久化或内部调用链。以下是内容验收清单及示例，
不是要求把本节逐字复制到工具目录；与 Schema 已清楚表达的内容不再逐项复述。

常用最简请求省略两个布尔值：

```json
{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"}}}
{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"..."}}
{"reference":"O1","operation":{"type":"tap","target":{"x":100,"y":200}}}
```

显式关闭例：

```json
{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"bubble":false}}
{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"...","press_enter":false}}
```

最终 Instructions 必须明确：

1. ref 点按/长按默认 `bubble=true`：目标自身没有该动作时，可使用最近交互祖先一次；该祖先不支持
   本动作时不再越过它。坐标目标不传 `bubble`。保持一次动作，不沿祖先逐层点击。
2. 需要关闭祖先提升时传 `operation.bubble=false`；需要让 text 只输入时传
   `operation.press_enter=false`。多行输入、草稿和逐字段填写通常使用后一种形式。
3. text 的省略形式与 `press_enter=true` 相同，已包含自动聚焦、写入、精确读回和默认 ENTER。
   调用方不再先发送独立 tap，也不再另发 key=ENTER；一次 text 调用完成这条复合流程。
4. 已接纳的省略/true text Result 始终包含 `text_input:{write,enter}`；显式 false 只自动聚焦、写入并
   精确读回，不发送 ENTER，也不新增 `text_input`。这些是调用方选择，不要求每次向用户重复确认。
5. ENTER 已派发或 `text_input.enter=executed` 只证明按键原语；业务目标是否达成由后观察中的具体
   页面事实确认。仅“取得后观察”不等于业务成功，后观察失败也不改写已确认的输入执行事实。
6. 恢复动作务实依据结果中的 `recovery` 参数选择最小下一步；不要因 `uncertain` 或显式
   input-only 默认发出泛化告警，也不要要求用户重复确认已经明确的任务意图。

沿用第一份提案的整体体积验收：同时统计 Instructions 和完整 tools/list，不能只把长描述移入
Instructions 就宣称瘦身。第二批只补新能力必需的调用语义，所有新增句子都须能说明会改变 caller
哪项选择；具体算法及完整错误分支仍由工程文档承载。

## 7. 预期效果与能力边界

### 7.1 能治什么

- Agent 对一个引用字段形成单一心智模型，旧字段在发送前或严格接入时立即失败。
- 复杂宿主拿到无条件组合器的稳定根 Schema；服务端仍准确拒绝跨 kind 成员。
- 无动作 child 在 ref tap/long_press 默认路径中可做一次到最近安全交互祖先目标提升，并保持
  child/ancestor 双重身份；有动作目标仍走 direct。
- text 默认一次调用完成自动聚焦、写入、精确读回与 ENTER，调用方能看到各自阶段而非从 phase
  猜结果；多行、草稿和逐字段输入可用 false 保持 input-only。
- status、重复 P、冻结和 MCP 两 codec 不再因响应分支而丢失 write/enter 事实。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| ENTER 是否真正提交表单、触发搜索或导致导航 | 本提案只证明 ENTER 输入链已派发，不推断业务效果 |
| 原 child 消失后继续点击同名/同位置祖先 | child 是绑定身份；不能用 A 单独解绑 |
| 任意祖先、多级事件传播、更远祖先或同容器 sibling 的选择 | `bubble` 只允许 C→第一个交互祖先 A 的一次安全目标提升，安全点不得落到其他独立目标 |
| SetText 已派发但 readback 无法确认时的真实写入结果 | 保持 `uncertain`，不回滚、不重放 |
| STOP 后迟到回执补全 stage | 冻结后不回补；仅保留冻结时已知事实 |
| 服务重启后恢复普通 text 阶段历史 | 不新增恢复账本普通历史，不承诺跨进程 status 恢复 |
| readback 后到 ENTER 前页面或焦点被 App 异步改变 | ENTER 只绑定同一 display generation；不证明页面、焦点或业务结果保持原子一致 |
| `write=executed,enter=not_executed` 时自动继续提交 | 不自动补发；可由调用方取新 O 后决定新的单独动作，旧阶段不能被重放 |
| `enter=uncertain` 时直接重试 ENTER | 阶段事实不是重试授权；先观察业务/页面状态，防重复按键 |
| 显式 `press_enter=false` 时自动提交 | 该分支只完成输入并精确读回；不回滚文字、不自动补 ENTER，也不产生新阶段对象 |
| 旧 `observation_id` 客户端无改迁移 | 本提案硬切字段，旧调用以严格错误失败 |

### 7.3 成本

每次 act 请求增加最多两个布尔字段的编码/校验成本，可忽略。ref tap/long_press 省略 `bubble`
时默认 true，只有 direct 不可用且 child 无交互动作才增加一次有界祖先规划与第二次 matcher；
坐标 target不运行该逻辑，其他 type 也没有可生效默认。树算法仍受现有预算，静态失败不占普通 work。

默认复合 text 增加一次 ENTER 派发、可能增加少量回执/门禁耗时；不会增加 JAR/RPC。它省去常见
“先 tap 再 text 再 key=ENTER”的 Agent 规划与网络往返。`text_input` 在省略/true 的 text 上固定
增加一个小型对象，文本和 structured 各多两条事实；显式 false 保持既有响应形状。false 的代价是
多行、草稿、逐字段输入不会自动提交，需要后续由调用方按业务意图另行操作。
该流程继续使用现有 `WorkAct` 总预算（`internal/engine/operations.go:236-247`），不新增
专用超时、隐藏重试或用更长时间掩盖读回/门禁成本；预算耗尽仍走既有冻结与事实分层。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| act 新 `reference` / 旧 `observation_id` / 双传 | 新字段按现行 O 语义处理；旧名与双传均为 422，P 不因改名变可用 |
| inspect 新/旧字段 | 新字段读取保留 O；旧字段 422；P 仍不可解析为观察 |
| app 七 kind 新旧字段矩阵 | 全部接受新字段、拒绝旧字段；任一跨 kind 成员均 422 |
| app 扁平 Schema 结构 | 根和 target 均封闭；不存在 `allOf/if/then/else/not/oneOf`；仍只有 12 工具 |
| act 请求 Schema | 声明 `bubble`、`press_enter`，字段说明明确分支条件且没有可误读为全类型生效的默认 |
| `open.target.app` / 旧 `open.app` | 新形态精确编码与解码；旧形态为未知字段 |
| `list.app` 过滤 | 顶层 app 仍被接受且不影响其他 kind 的 app 规则 |
| find/list P/取消/status | 查询仍分配 P；取消不 `end`、无 outcome；status 只回摘要 |
| 坐标 target 省略 `bubble` | 接受并完全沿用坐标 tap/long_press 路径；不运行祖先规划 |
| 坐标 target 显式 `bubble=true/false` | 均 422；O 不 claim |
| 非 tap/long_press 带 `bubble=true/false` | 均 422；直接失败无设备调用 |
| ref tap/long_press 省略 / true / false `bubble` | 省略与 true 均形成 C+A plan（direct 不适用时）；false 保留现有 422 |
| `bubble` 旧名 `allow_ancestor_fallback` | 作为未知字段拒绝，不提供别名或映射 |
| HTTP / MCP / 直接 engine 入口 Effective 矩阵 | 三者对省略/true/false、坐标无默认和其他 type 无默认得到完全相同结论 |
| bubble 静态成功/无候选/最近祖不支持 | 成功形成 C+A plan；后两者 422 且 O 保持 ready |
| child 与 A 均唯一且关系保持 | 只派发一次 A 安全点 tap/long_press |
| 原目标已有动作且 bubble 省略或 true | 继续直接目标算法，不提升到祖先；证明不是 DOM 多级传播 |
| child 丢失但 A 唯一 | `target_changed`，零派发；禁止解绑为 A |
| child 的资源标识/祖先不变但公开 Role/Label 改变 | `target_changed`，零派发；防止内容替换后点击 |
| 更近 fresh 交互祖先出现 / child 新增 tap/long_press/text / 安全点命中 sibling | 均为 `target_changed`，零派发 |
| text 省略 `press_enter` 与显式 true | 选择同一复合流程；省略也因 effective=true 在所有 Result 通道携带完整 `text_input` |
| text 显式 `press_enter=false` | 自动聚焦、SetText、精确 readback 保持；ENTER 零派发且 Result 无新 `text_input` |
| `press_enter` 用于非 text / null / 非 bool | 全部 422；适用分支外没有默认值 |
| optionalBool presence 解码 | 省略得到 `present=false`；显式 false 得到 `present=true,value=false`，二者不可由 Go bool 零值混淆 |
| canonical client encoder 往返 | 显式 false 在 JSON 中保真出现；解码后再编码不因 `omitempty` 消失 |
| `text_input` 保留判据反向对照 | 只看 raw 字段是否出现会让省略 text 丢失对象；只应使用 effective=true |
| write readback 不精确 | `write=uncertain`；绝不 dispatch ENTER |
| claim 前不存在/禁用 child，或层级损坏 | 保留 node_not_found/node_disabled/hierarchy_unavailable 的当前状态码；均不认领 O |
| 成功 claim 后、首个输入前 STOP | 同锁初始快照已存在，冻结包含 `(N,N)`；零输入时整体 not_executed |
| 焦点 tap 已派发后 capture/bind 失败 | 两键 `not_executed` 或按后续证据推进，但整体 `uncertain` |
| write executed、ENTER 前 STOP/期限/owner 失败 | `text_input={executed,not_executed}`，整体 `uncertain`，ENTER 零派发 |
| write executed、ENTER 回执不完整 | `{executed,uncertain}`，整体 `uncertain`，不重试 |
| write+ENTER 均确认 | `{executed,executed}`，整体 `executed`；KnownOutcome 仅在此时设置 |
| 后观察失败 | 两键和整体 executed 保留；仅后观察事实失败 |
| readback 后同 display 内焦点/页面异步变化 | ENTER 仍可能派发；测试不据此声称原子业务提交，也不增加补偿/重放 |
| ENTER 门禁前 display generation 变化 | ENTER 零派发；保留 `{executed,not_executed}` 且整体 `uncertain` |
| write executed、enter not_executed / enter uncertain 后重放旧 P 或直接再来一次 | 旧 P 不二发；不自动继续/重试；任何新动作使用新观察与新 P，并先取得状态证据 |
| 并发 duplicate P 与 ENTER | 同一 P 至多一次 ENTER；重复调用只回冻结/摘要 |
| 运行中 status(P) / duplicate P | 复制当前完整 stage 快照，不采集设备、不省略键 |
| freeze 与 stage 提交竞争 | 冻结方先赢后迟到提交不改字段；保守整体 outcome |
| 派发许可、uncertain 快照、双确认与 KnownOutcome 的竞争 | 不存在已可派发却仍显示未派发，或两键 executed 而冻结整体 uncertain 的间隙 |
| 复合 text 操作并发失败贴近 STOP/freeze | 冻结前跨派发许可的未确认 primitive 提交 `uncertain`；冻结后不回补、不改写 |
| 进程在 press_enter 中途退出 | 不恢复普通 stage、不自动补发；重启后只能按当前业务状态建立新观察/请求 |
| text_input 单键/null/未知枚举/额外成员 | protocol decoder 与 MCP projector 均拒绝 |
| text_input 非法组合，如 `(N,E)`、`(U,U)` | protocol decoder 与 MCP projector 均拒绝 |
| focus executed → SetText 确定未派发 → STOP | 私有累计 focus 不因 work.UI 替换丢失；公开 `(N,N)`、整体 uncertain |
| Result manifest | 覆盖 write/enter 的 all-executed、write-only、both-not-executed+focus-uncertain 等事实 |
| canonical/text parity | Text v2 可还原规范对象；structured 与 both 包含同一 text_input |
| Caller 文案与整体体积 | 按第一份提案检查用途/参数/结果/恢复的可操作性及发现元数据总量；无内部算法搬入 Instructions，也不在 12 个描述中重复公共说明 |

反向对照至少覆盖：

- 去掉双绑定后，A 唯一而 child 丢失的用例必须从拒绝变成误派发，证明守卫必要。
- 把省略误当 Go bool false 后，默认 bubble 与默认 press_enter 用例必须变红；反之把显式 false
  丢掉后，encoder presence 往返必须变红。
- 把一个全局 raw-bool=true 默认套到所有操作后，坐标 tap 省略路径或其他 action 的“默认不适用”
  用例必须变红；HTTP、MCP 与直接 engine 三入口的共享 Effective 矩阵也必须同时变红。
- 去掉精确 readback 门禁后，值不一致用例必须错误地发送 ENTER。
- 去掉累积 focus 证据后，“两键 not_executed 但焦点 tap 已派发”必须错误地降为 not_executed。
- 用 phase/error 代替 operationState 快照后，freeze/迟到结果用例必须丢失 text 阶段。
- 恢复旧字段别名或把 app 组合器重新当唯一权威后，strict/matrix 测试必须变红。

child+A 双绑定、公开 Role/Label、STOP/冻结、阶段快照和各 primitive 派发证据的原测试矩阵全部保留；
默认 true 只改变走哪条既有规划/复合分支，不删减任何失败守卫或用更宽默认覆盖旧反例。

### 8.2 实机/系统级

使用 `com.mobileuse.probe` 或同等受控页面完成：

1. 构造无动作 text child 与唯一可点击祖先，分别验证 `bubble` 省略、显式 true 和显式 false：
   前两者计数证明只触发祖先一次，sibling 与更远祖先零触发；false 保留 direct 错误。坐标 tap
   省略 `bubble` 时验证祖先逻辑零运行。
2. 使用可显示 focus、SetText 次数与 ENTER 次数的表单夹具；验证省略/true 的 text 在精确
   readback 后恰好一次 ENTER，显式 false 的 ENTER 为零且无 `text_input`。两种情况下业务未提交
   都只报告已确认的输入事实。
3. 在 SetText 后、ENTER 持久边界前后分别触发 STOP/end，检查 `text_input`、整体 outcome、
   实际输入次数和迟到回补均为预期。
4. 通过真实 MCP 宿主发现 12 工具，检查扁平 app Schema、新 reference、`bubble` 与
   `press_enter` 的默认/显式 false 表达，以及 Instructions 明确“默认 text 已含聚焦、读回和 ENTER，
   不需要先独立 tap 或再发 ENTER”；不假定宿主 UI 会展示所有字段。
5. 手机服务、主机 MCP、Go probe 与客户端使用同一候选发布产物，检查 contract、目录指纹与
   manifest；中间构建不得安装为正式发布或单独推进 contract。

实机不可用时，验证记录必须逐项写“未执行”及原因，不能把单测替代为设备结论。

### 8.3 记录

写入 `maintenance/verification/agent-reference-and-action-contract.md`，记录实现提交、构建环境、
实际命令、退出码、设备/宿主身份、受控夹具结果、反向对照、未执行项和真实结论。区分
“已实现 / 已单测 / 已实测”，不以旧提案或历史设备记录冒充本轮验证。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| #17 | 仅改 O/P 引用输入名为 `reference`；不扩大范围、不留别名；保留响应 observation_id、target.ref 与 recovery 枚举 |
| #18 | 保留 12 工具和单 `app`；扁平 Schema、服务端严格 kind 矩阵；`open.target.app`，`list.app` 过滤保留 |
| #18 被拒路线 | 不用组合器作为唯一互斥，不拆七工具或 14/13 工具 |
| #19 | `operation.bubble` 仅 tap/long_press + ref，省略有效 true；显式 false 关闭；坐标必须省略；child+nearest ancestor 双绑定 |
| #23 | `operation.press_enter` 仅 text，省略有效 true；显式 false 只聚焦/写入/精确读回；true 在同 work/P 至多一次 ENTER；不保证业务提交 |
| text 事实 | 省略/true 固定 `text_input:{write,enter}`；显式 false 不新增该对象；焦点 tap 不括入两键但始终计入整体 outcome |
| boolean presence | 原始 optionalBool 区分省略与显式 false；encoder保真 false；有效默认只在 type+target 适用分支计算 |
| 冻结 | stage 保存在 operationState；冻结保留快照并保守判 outcome；迟到结果不回补 |
| Instructions/工具描述 | caller-agent-oriented；公共规则单处说明，单工具短文按用途/参数/结果/恢复编写；明确默认 bubble/text、false 用例和阶段事实，整体体积按第一份提案验收 |
| 依赖 | #21→#20 是取证依赖；A→B 是用户验收门禁；#17 先行是共享迁移推荐顺序，不是能力硬依赖 |
| 发布 | 两批完成后一次正式发布，一次 contract 15→16，一次目录/manifest 更新 |

### 9.2 未决

无。实现期间若发现新判据、时序或契约取舍，必须回到提案修订并重新确认；不能由实现者临场
决定。第一份提案中的 #20 数值仍按第一份的独立证据门禁处理，不构成本提案未决项。

## 10. 实施顺序

依赖类型必须分开理解：

| 关系 | 类型 | 含义 |
| --- | --- | --- |
| #21 → #20 | 目标模式取证硬依赖 | #20 的跨度与比例测量必须使用 #21 的 text 目标模式；不是 #21 功能逻辑依赖 #20 |
| 第一份 → 第二份 | 用户选择的分批验收门禁 | 用户要求 A 完成验证并处理 #20 保留/调整结论后再实施 B；不是 B 的字段/算法在逻辑上无法独立编码 |
| #17 → #18/#19/#23 | 推荐实施顺序 / 共享请求迁移 | 先稳定 reference 与 operation 请求面可减少重复改动；不是三项能力离不开改名 |
| #16 → 第二份描述落地 | 推荐顺序 | 先确定 Instructions/描述风格，避免第二份重复修改同一描述 |
| #22 与其余工作 | 可独立并行 | 写集是 wait/capture；仍需在阶段 A 出口前完成验收 |
| #19 与 #23 | 共享写集，默认顺序合并 | 都改 `act.go`、operation/state/retirement/protocol tests；可并行仅在先隔离接口、提交边界和 ownership 分工后 |
| 全部 → 最终发布 | 发布硬门禁 | 所有已批准实现、验证、文档审计、manifest 与目录核验完成后才能统一发布 |

推荐顺序：

1. **A1–A3：** #21、#16 按第一份判据落地；#22 可与其并行开发，但按自身测试闭环。
2. **A4：** 在 #21 的 text 模式执行 #20 取证。达到门槛也只形成待确认候选；不达标或证据不足
   保留 `12/70` 与 `180s`，不阻塞第一份其余工作或第二份启动。
3. **阶段 A 出口：** 完成 #16/#21/#22 验证和 #20 保留/调整结论，形成用户选择的分批验收门禁。
4. **B1：#17。** 先完成 reference 的 protocol、client、MCP probe 迁移与反例，稳定共享词法。
5. **B2：#18。** 在 reference 基础上落地 12 工具扁平 app、七 kind 矩阵与 `open.target.app`。
6. **B3：#19。** 完成默认 `bubble`、显式 false、坐标无默认、静态双绑定、fresh 双匹配、安全点与零重放测试。
7. **B4：#23。** 完成 press_enter、text_input、持久 may_dispatch、status/duplicate/freeze/MCP parity；
   若与 B3 并行，必须先分开 commit 并隔离新增接口，最终顺序合并审计。
8. **共同集成：** 对 B1–B4 做跨层严格接入、真实宿主、实机与反向对照验证；不单独发布任一
   中间 B commit。
9. **版本冻结与提交：** 在 `release/versions.json` 一次选定下一发行号、共同契约 `16` 与 Android versionCode；运行生成脚本，同步文档和相关检查，通过独立只读文档审计后提交实现。提案此时仍为“提案，未实现”；随后按流程另行归档两份提案，写真实实现 hash 与各自验证记录。
10. **唯一发布核验：** 从干净已提交源码构建配套手机/主机产物，生成新目录指纹和 manifest 并核验；普通同步的共同契约、APK、客户端与适配器必须匹配。实际部署与宿主重启按发布流程执行，不发布中间态，不在清单核验后改版本。

#22 不与 B 共享核心实现，可在 A 期间独立推进；#20 在任何结果下都不阻塞 B 的已确认需求。
若实施资源只允许串行，优先完成 B1，因为它统一后续 B2–B4 的请求样本和 Schema 基线，但不得
把该排序描述为逻辑硬依赖。

## 11. 实现结果（归档时补写）

实现提交：待归档。

实现与验证记录：待归档时链接 `../../verification/agent-reference-and-action-contract.md`。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 待归档 | 本提案尚未实现，归档时填写真实代码提交与函数/文件落点 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 待归档 | 归档时逐项记录偏离、未执行项与理由；当前不预填实现结论 |

---

## 附：本提案不改变的事项

- 不改变固定 12 个 MCP 工具名称、顺序或数量，继续禁止 `apps`、`open_app` 和任何查询子工具。
- 不改变 app 查询的 P 分配、detached/list 绑定、取消不发 `end`、无 outcome 和摘要语义。
- 不改变 O 单向消费、STOP 不等待、5 秒逻辑交接、设备单普通工作位和恢复屏障。
- 不改变点/手势/滚动既有安全算法；ref tap/long_press 的默认 `bubble` 只增加一次经 child+A
  双绑定的安全目标提升分支，坐标目标不进入该分支。
- 不改变 Android 业务逻辑、uiautomator JAR、RPC、键码集合或配对/安装流程；共同版本生成常量与配套 APK 仍随唯一正式发布更新。
- 不新增恢复账本普通动作历史、日志正文、text 原文、树/坐标或截图。
- 本次不修改正式文档、代码、生成版本或 `release/versions.json`，不提交、不推送。
