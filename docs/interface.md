# 接口契约

本页定义 `/api/<tool>` 与控制端 HTTP 端点的请求字段、响应形状、状态码与错误码。字段以
`internal/protocol`、`internal/httpapi` 与 `internal/engine` 的定义为准；控制 App 侧的行为
另见 [控制端协议](control-protocol.md)。本页只列服务会接受或产生的字段，未列出的字段一律
不发送、不接受。

本页 JSON 示例只表示字段形状与层级；示例中的尺寸、坐标、包名、编号与时间均为示意，不代表
具体设备状态。

## 1. 接入约定

| 项 | 规则 |
| --- | --- |
| 方法 | 所有业务端点只接受 `POST`；其他方法返回 `405`，错误码 `invalid_request` |
| 认证 | `/api/*` 用 `agent_token`；`/v1/control/*` 用 `control_token`；`Authorization: Bearer <token>` |
| 身份隔离 | agent token 不能访问 `/v1/control/*`，control token 不能访问 `/api/*`；错误为 `401 unauthorized` |
| 普通契约 | `/api/*` 必须带 `X-Mobile-Use-Contract: 20`，精确相等；缺失或不匹配返回 `409 contract_mismatch` |
| 预分配 P | `/api/*` 可带 `X-Mobile-Use-Operation: P…`；缺省由服务分配。`status` 不使用该头 |
| 请求体 | 必须是一个 JSON 对象；大小上限：`start`/`status`/`end`/`wait_for` 16 KiB，`observe` 32 KiB，`snap` 8 KiB，`act` 64 KiB，`inspect`/`keepalive`/`diagnose`/`app` 8 KiB，`notify` 16 KiB，`shell` 32 KiB，`sync` 32 KiB，`stop` 8 KiB |
| 响应 | `Content-Type: application/json; charset=utf-8`，`Cache-Control: no-store` |
| 健康检查 | `GET /health` 无认证，见 §10 |

严格 JSON（`/api/*` 与 `/v1/control/sync|stop` 一致，来自 `encoding/json/v2` 与显式检查）：

- 未知成员、重复成员名、大小写变体、尾随第二个 JSON 值、类型混用 → `422 invalid_request`；
- 必填字段必须实际出现；未声明 nullable 的字段出现显式 `null` → `422 invalid_request`
  （`status` 的 `reference`、`observe` 的 `full`/`image`/`wait_ms`、`snap` 的 `reference`、
  `wait_for` 的 `query`/`absent_query`/`stable`/`full`/`image`、`act` 的 `image`、
  `text`/`key` 的值等）；
- 请求体为空、只有空白或不是对象 → `422 invalid_request`；
- 编号规则：`O…` 为观察，`P…` 为操作；引用长度 1–64，首字符必须是 `O` 或 `P`。
  客户端预分配的 P 至少 2 个字符。编号是不透明句柄，
  服务生成的编号含服务实例前缀，调用方不得解析其中数值，也不得跨服务实例复用；客户端预分配的
  P 按原值登记，不由服务改写。

会话内做收敛采样的自动观察点——`start` 的首个观察、`act` 的后观察与 `app` 五个变更 kind 的
后观察——在目标树收敛后或该阶段采样预算耗尽时发布：连续两帧的节点内容身份、公开树与前台/
显示语境一致才停止采样，预算耗尽照常发布最后一帧。静默窗口与总预算按阶段固定
（`docs/architecture.md` §4），不是请求字段，也不改变任何端点的字段与错误码；页面持续动画时
这些观察最多多花该阶段的预算（`act(image=true)` 另加一次刷新读取）。这些观察发布
`observation.quality`，记录收敛状态与有效样本数（见 §11）。

`act` 的重验在派发前失败时另有一个交付观察点：重验已经读取的那一帧直接发布为新的 `ready`
观察，随失败结果返回（见 §6）。该点不做收敛采样，也不重新采集，其质量固定为
`unknown`/`0`/`not_sampled`。`observe` 由调用方决定节奏（`wait_ms` 与 `full`），不做收敛
采样；`snap` 只读一张图，不取树，也不参与这些采样点；`wait_for` 不在这几个采样点之列，它的观察质量来自自身的条件等待（见 §14）；
`shell` 不发布观察，命令是否改变屏幕由下一次 `act` 的实时重验或一次 `observe` 回答。

## 2. 结果分层与错误

> 除外部不可抗力（`end`、STOP、App 实例变化、设备不可达、服务退出/重启、租约到期）外，任何内部原因都不得撤销会话、不得让会话无法继续使用、不得让服务锁死到只能重启手机。

> 该保证约束工具级期限与内部未决事实。启动阶段失败、设备/App 不可达、恢复账本不可写与当前
> 能力门禁属于外部资源条件，仍按各自错误码拒绝新工作或结束启动阶段的原会话（包括激活后的
> 首个观察失败）。

工具响应是一个 `Result` 对象，字段只在本次调用确有该事实时出现，固定顺序为：

```text
operation_id, operation_state, outcome, phase, effect, text_input, target_reference,
request_closed, blocking_operation_id, can_start, blocking_code, start_operation_id,
observation_state, input_state, message_id, saved, image, session, observation, inspection,
app, shell, wait, notification, diagnosis, cleanup, error, recovery
```

事实互不替代：`outcome`（动作执行）、`effect`（本次受控滚动的位移观察）、
`text_input`（复合 text 的 write/enter 原语）、
`wait`（条件等待的退出分支、单帧匹配项与耗时）、
`image`（`snap` 的顶层单张图片元数据）、
`observation.status` 与 `observation.quality`（观察及其收敛质量）、`session.state`（会话）、
`cleanup.*`（物理收尾）。`error` 只在失败时出现：

```json
{"operation_id":"P2","outcome":"not_executed",
 "error":{"code":"session_ended","message":"原会话已结束"},
 "recovery":{"entry":"end","reference":"P"}}
```

- `code` 稳定，`message` 是受控中文短句，调用方只依赖 `code`；
- `/api/*` 的 `details` 仅可能出现白名单事实：`limit`、`budget`、`limit_ms`、`limit_s`、
  `app_code`、`field`、
  `task_ids`、`allowed_kinds`、`allowed_scopes`、`blocking_operation_id`、`start_operation_id`、
  `nearest_actionable_ref`、`safe_carrier_ref`、`safe_point`、`occluding_ref`、
  `reason`、`target_actions`、`ancestor_ref`、`ancestor_actions`、`scope`、`scope_ref`、
  `occluding_refs`、`ambiguous_refs`、`owner_refs`、`suggested_point`、
  `suggested_carrier_ref`、`stage`、`dispatch`、`candidate_count`；
  `nearest_actionable_ref`、`safe_carrier_ref`、`occluding_ref`、`ancestor_ref`、`scope_ref`、
  `suggested_carrier_ref` 与 `reason`/`scope`/`stage` 是字符串，`target_actions`、
  `ancestor_actions`、`occluding_refs`、`ambiguous_refs`、`owner_refs` 是字符串数组，
  `safe_point` 与 `suggested_point` 是只含非负整数 `x`、`y` 的 JSON 对象，
  `dispatch` 是只含 `focus`、`write`、`enter` 三个字符串成员的 JSON 对象，
  `candidate_count` 是非负整数；均不得字符串化。`tap`/`long_press`
  的祖先引导按原子组给出或整体省略：`nearest_actionable_ref` 与 `safe_point` 总是同时出现；
  仅当该点来自引导候选的承载节点时再附 `safe_carrier_ref`。承载可以是原目标或候选祖先的
  非阻塞后代，其 ref 只解释坐标来源，不表示它具备请求动作。`occluding_ref` 仅在基线文本
  聚焦检查或其他仍使用基线规划的错误出口出现；`tap`/`long_press` 的 ref 静态候选耗尽改用
  `occluding_refs`。新增 `reason`、作用域、动作数组与候选 ref 列表只属于该静态分支，具体
  枚举见 §6。`stage`、`reason`、`dispatch`、`candidate_count` 的 text 取值只属于 `text`
  错误，`tap`/`long_press` 不继承该组。没有可证事实时省略这些键，不以 `null` 或空字符串代替；
  已声明必出的动作数组即使为空也输出 `[]`。`mcp.*` 是 [MCP 适配器](mcp.md) 的保留命名空间，
  仅由该适配器向结果投影注入，服务端 `/api/*` 永不产生这些键；
- `recovery` 是下一个请求的投影：本次结果没有待恢复事实时整个对象省略；出现时 `entry` 与
  `reference` 必出，`input_state` 与 `cause` 按需出现。它不改变上面的动作、效果、观察、
  会话与收尾事实。
- `observation` 不是成功专属：`act` 的重验在派发前失败且已完成交付时，失败结果同样携带
  `observation`（新 O）。交付时它表示本次调用提交了一个新 O；`observation.status=failed` 的
  失败结果没有新 O。
- `effect` 只出现在首次受理的 `scroll` 结果与仍保留该原操作的 P 视图，且只在
  `outcome=executed`（含 `executed_revoked`）或 `uncertain` 时出现；`not_executed` 永不出现。
  对象缺失表示本次结果没有该事实（可能是原操作不是 `scroll`、未执行，或原操作事实已封闭、
  已淘汰而不可用），缺失不表示“未观察到位移”。
- `text_input` 只出现在已接纳且有效 `press_enter=true` 的 `text` 操作结果与仍保留该原操作的
  P 视图；`write`、`enter` 两键固定出现，枚举与合法组合见 §6。显式 `press_enter=false`、
  其他动作、认领前拒绝和未接纳调用都不出现该对象。它不替代 `outcome`、`error` 或后观察。

`recovery` 的成员：

| 成员 | 取值 | 含义 |
| --- | --- | --- |
| `entry` | `retry` / `observe` / `status` / `end` / `user` / `update` | 下一个请求应调用的入口 |
| `reference` | `P` / `O` / `none` | 该入口要带的引用类型，不带 ID |
| `input_state` | `effective` / `expired` / `revoked` / `claimed` / `retired` | `recovery` 所指观察的输入可用性。`act` 重验失败交付新 O 时指该新观察；`observation.status=failed` 的降级结果与其余分支指该操作原请求引用的 O（`status(P)` 与重放视图即该操作原请求的 O），`start` 为首个观察，`end`/`status(P)` 的收尾分支为原会话。词汇与 `status(O)` 顶层 `input_state` 一致 |
| `cause` | `post_observation_failed` / `executed_revoked` / `cleanup_pending` / `running` | 仅在本次结果不带顶层 `error` 时出现，说明看似成功或仍在运行却仍需恢复的原因 |

`entry` 的含义：

- `retry`：什么都没派发，且存在一个 `ready` 且可用的 O 可作为下一次同一工具的输入；换一个
  新的 P 调用同一工具。这里可用的 O 是结果携带的 `observation`（`act` 重验失败交付的新观察）；
  没有该成员时是该操作原请求引用的 O，重复 `P` 时即本次请求体里的 O。重复 `P`、`status(P)` 与
  冻结结果的视图不携带 O 编号，调用方须使用自己原先持有的 O。该入口总是 `reference=O`、
  `input_state=effective`。
- `observe`：观察证据作废，需要重新采集；`reference=P`。这里的 P 是已有、且其原会话仍是
  当前活动会话的操作编号（常见为 `session.origin`，也可用该会话所属的 `operation_id`）；
  它不是本次 `observe` 调用新分配的 P。请求体 `reference` 与本次调用 P 是两个字段，规则
  见 §5。已认领 `text` 的写入或 ENTER 无法确认、已认领后未派发任何原语但目标条件不再成立、
  或主动作已确认而后观察失败时，该入口直接指向一次新观察；原会话已撤销、结束、过期或
  绑定失效时改由 `end`/`status` 表达，见 §6。
- `status`：结果未定或收尾未完成；轮询对象按工具取本结果的 `operation_id`、
  `start_operation_id`、`target_reference` 或 `blocking_operation_id`。
- `end`：会话已结束、过期或被撤销；`reference` 为 P。`status(O)` 观察到会话已撤销时，也可对
  同一 O 调用 `end`，由该 O 所属会话执行收尾。
- `user`：需要人处理，继续调用 API 不会成功；`reference=none`。
- `update`：契约版本不匹配；`reference=none`。

成功结果通常不带 `recovery`；降级成功（动作已执行但后观察失败、执行期间被撤销，或 `end`
的收尾未完成）带 `cause`。`diagnose` 的工具结果不带 `recovery`。`notify` 是会话无关工具、
`app` 的 `find`/`list` 查询与 `shell` 都不产生 `retry`。`recovery` 与旧恢复字段不兼容
（破坏性契约变更）：只认识旧字段的客户端不会报错，只会静默丢失恢复提示，必须整组升级。

HTTP 状态映射（默认由 `protocol.Status` 按 code 决定；认领前的 text 静态拒绝使用 phase-specific `422`，首次结果、`status(P)` 与重复 P 一致）：

| 状态 | 触发 |
| --- | --- |
| 200 | 成功；含 `end`/`status` 的同帧 `cleanup=pending`/`unconfirmed`（退休收尾事实不是 HTTP 错误） |
| 401 | `unauthorized` |
| 409 | `contract_mismatch`、`device_busy`、`session_*`、`reference_has_no_session`、`operation_*`、`observation_*`、`node_not_found`、`node_disabled`、`stale_screen`、`target_changed`、`foreground_changed`、`foreground_unknown`、`control_revoked`、`request_closed`、`control_unavailable`、`stop_unavailable`、`mode_not_selected`、`device_locked`、`device_not_interactive`、`recovering`、`no_ready_observation`、`display_unavailable`、`text_target_uncertain`、`text_result_uncertain`、`control_ui_on_virtual_display`、`app_not_found`、`application_moved`、`task_focus_unavailable`、`task_remove_unconfirmed`、`content_unavailable`、`inspection_too_large`、`root_unavailable`、`window_action_not_supported_on_primary_display`、`task_not_found`、`task_not_tracked`、`task_target_ambiguous`、`task_root_shared`、`task_move_unconfirmed`、`close_refused_borrowed`、`kill_refused_not_owned`、`kill_refused_task_elsewhere`、`kill_refused_foreground`、`kill_refused_playback`、`kill_refused_process_shared`、`kill_unconfirmed`、`app_capacity_exceeded`、`task_lifecycle_busy` |
| 422 | `invalid_request`、`invalid_coordinates`、`ambiguous_point`、`unsupported_key`、`unsupported_node_action`、`invalid_target`、`no_safe_point`、`text_too_long`、`reference_invalid`；认领前的 text 静态输入能力/保护拒绝（`text_target_uncertain`，含首次结果、`status(P)` 与重复 P）；`app` 的未知 kind、分支外成员、`target` 形状错误与显式 `null` 也归入 `invalid_request` |
| 503 | `device_unavailable`、`display_create_failed`、`hierarchy_unavailable`、`tree_too_large`、`app_launch_failed`、`app_list_unavailable`、`shell_start_failed`、`delivery_unavailable`；以及 `observation.status=failed` 或没有新 O 的 `partial` 结果 |
| 504 | `action_result_uncertain`（输入已派发但无法确认完成）、`app_launch_unconfirmed`、`shell_result_unconfirmed`；`app` 的 `app_launch_unconfirmed` 仅用于 `open` |
| 500 | `internal_error`（接入层自身失败）；工具结果中的 500 会被改写为 409 |

接入层错误（认证、方法、契约、JSON）直接返回 `{"error":{…}}`，不带 `operation_id`。只有
`contract_mismatch` 带 `recovery`（`entry=update`、`reference=none`）；其余接入层错误不带
`recovery`。工具结果中的 `error` 若映射为 500，HTTP 层改写为 409；接入层自身的 500
保持 500。

## 3. `POST /api/start`

请求体必须为空对象，任何成员都拒绝：

```json
{}
```

启动参数没有请求字段：目标屏幕与模式来自控制 App 上报的 `selected_mode`（`virtual` 或
`primary`），尺寸来自设备逻辑屏。响应在成功时包含 `session` 与首个 `observation`：

```json
{
  "operation_id": "P1",
  "outcome": "executed",
  "session": {"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",
    "started_at":"…","expires_at":"…"},
  "observation": {"status":"complete","id":"O1","origin":"P1","revision":1,
    "captured_at":"…","actionable_until":"…",
    "quality":{"state":"stable","samples":2,"stop_reason":"stable"},
    "screen":{"width":1080,"height":2400,"rotation":0},
    "foreground":{"state":"present","package":"com.example.probe","task_id":12},
    "tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",
      "root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}
}
```

- `start` 的首个观察固定为 full（`forceFull=true`），即使已有基线。
- 首个观察带 `observation.quality`：`start` 的收敛采样最多 4 帧，状态按该次收敛结果填写，
  规则见 §11。
- 会话租期在接纳时初始化为 `started_at + 5min`；激活与首个 full 观察成功发布各按
  `max(expires_at, now + 5min)` 更新。首个观察失败但会话保留时不额外续期，也不回滚期间
  已应用的显式 `keepalive` 结果；重放或被拒绝的 `start` 不重新给已有会话五分钟。
- 会话已建立但首个采集失败：HTTP 503，`outcome=executed`，`observation.status=failed`、
  `missing=["tree"]`，`recovery` 为 `entry=observe`、`reference=P`、`input_state=retired`、
  `cause=post_observation_failed`；会话仍存在，调用方应用新的 P 调用 `observe`。
- 首个采集因 `control_ui_on_virtual_display`、`display_unavailable`、`stale_screen` 失败时，
  会话被撤销并启动收尾，响应同时带 `session`（`ended`）与 `cleanup`；`recovery` 为
  `entry=status`、`reference=P`、`input_state=revoked`、`cause=cleanup_pending`。
- 接纳失败只由当前能力与当前权威冲突产生：`409/503`、`outcome=not_executed`，`error` 可为
  `session_exists`、`device_busy`、`control_unavailable`、`stop_unavailable`、
  `mode_not_selected`、`device_locked`、`device_not_interactive`、`control_revoked`、
  `display_unavailable`、`device_unavailable` 或 `ledger_unavailable`。旧会话的
  `cleanup.state`、四项 cleanup 事实、旧 UI/shell、legacy checkpoint、退休记录容量与未停止的
  物理资源不生成 `cleanup_pending`/`cleanup_unconfirmed`/`old_resource_unconfirmed` 准入失败。
  `recovery` 按当前错误规划；逻辑交接尚未完成时可用
  `device_busy`，最多持续撤权后 5 秒。
- 虚拟屏创建结果未确认或 App 拒绝创建：503（`display_create_failed` /
  `display_unavailable`），`outcome` 为 `not_executed` 或 `uncertain`，响应可能带
  `session`（已结束）与 `cleanup`。

## 4. `POST /api/status`

请求是以下三种之一：

```json
{}
{"reference":"O1"}
{"reference":"P1"}
```

`reference` 可省略；显式 `null`、空串、非法编号都拒绝。响应分三支：

**自身状态（无 reference）**

```json
{"can_start":true,
 "session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",
   "started_at":"…","ended_at":"…","reason":"client_end"},
 "cleanup":{"state":"unconfirmed","execution":"confirmed","tasks":"confirmed",
   "recents":"not_applicable","display":"unconfirmed"}}
{"can_start":false,"blocking_code":"control_unavailable"}
```

- `can_start` 必出；`false` 时必有 `blocking_code`（可能是稳定错误码，也可能是内部原因串：
  `control_sync_pending`、`device_unavailable`、`ledger_unavailable`、`ledger_corrupt`
  等）。旧 cleanup、旧物理资源未确认与退休记录容量不是合法原因；
- 会话属于本 Agent 时带 `session` 与 `start_operation_id`。本 Agent 没有当前会话时，可带其
  最近退休会话；有同一会话的收尾记录时才带 `cleanup`，不得拼接全局或另一会话的 cleanup；
- 退休 cleanup 为 `pending` 时，可给 `recovery=entry=status`、有 `start_operation_id` 时
  `reference=P`，否则 `reference=none`。`unconfirmed` 表示自动补偿已停止，只作为同一帧的
  终态事实返回，不再产生“继续轮询”的 recovery，也不使 `can_start=false`。

**观察状态（reference=O）**

```json
{"observation_state":"ready","target_reference":"O1","input_state":"effective",
 "session":{"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",
   "started_at":"…","expires_at":"…"}}
```

`observation_state` 为 `ready`/`claimed`/`retired`/`expired`；`input_state` 为
`effective`（仍可作为输入目标）、`expired`、`revoked`，或其他观察状态原值。`expired`、
`claimed`、`retired` 时分别带 `recovery` 的 `entry=observe`、`reference=P` 与同名
`input_state`。原会话结束且 cleanup 仍为 `pending` 时，`input_state="revoked"`、
`recovery` 为 `entry=status`、`reference=O`、`cause=cleanup_pending`；cleanup 为
`unconfirmed` 时保留 revoked 事实，但不添加无限轮询入口。

**操作状态（reference=P）**

```json
{"operation_id":"P2","operation_state":"finished","outcome":"executed",
 "phase":"finished","effect":{"state":"observed"},
 "session":{"origin":"P1","state":"active","mode":"virtual",
   "agent":"local-agent","started_at":"…","expires_at":"…"}}
```

- `running` 时带 `phase` 与 `recovery`（`entry=status`、`reference=P`、`cause=running`）；
- `finished` 时带 `outcome`、`phase`，操作错误在 `error`，`recovery` 由错误码与结果事实决定；
  该视图不带 `observation`，原操作交付过新观察时 `recovery` 为 `entry=observe`、`reference=P`、
  `input_state=retired`；
- `scroll` 的操作视图复制原操作已保存的 `effect`；没有已保存值但 `executed`（含撤销后保留的
  `executed`）或 `uncertain` 时投影 `unknown`，`not_executed` 不出现；视图不重算、不附观察。
  原操作事实已封闭，或已完成记录被淘汰而不可用时，整个 `effect` 可省略，省略不表示未观察
  到位移。上方示例是 `scroll` 原操作的 P 视图；其他 P 不带 `effect`。
- 不存在的 P：`409 operation_unknown`，不返回 `not_seen`；
- 有正面“未接纳”证据的已封闭 P：`request_closed=true`、`outcome=not_executed`、`session=null`；
- 仅保留“未来接纳已封闭”证据的 P：仍为 `409 operation_unknown`；`end(P)` 会带
  `request_closed=true`、`outcome=uncertain`、`phase=reference_unknown`，`status(P)` 不据缓存
  缺失声称过去未执行；
- `end` 或“会话已结束且收尾未完成”的 P 会带 `cleanup`；该 P 结束时没有 `error`、`outcome`
  不是 `uncertain`，且同一原会话的收尾为 `pending` 时，`recovery` 为 `entry=status`、
  `reference=P`、`input_state=revoked`、`cause=cleanup_pending`；`unconfirmed` 表示自动处理
  已停止，不添加继续轮询的 `cause`。

`status` 不续租、不认领观察、不改变输入授权；唯一的副作用是查询时把已过期的 `ready`
观察标记为 `expired`。`status` 不使用 `X-Mobile-Use-Operation`，也不登记 P。

## 5. `POST /api/observe`

```json
{"reference":"O1","full":false,"image":false,"wait_ms":0}
```

| 字段 | 必填 | 类型/范围 | 说明 |
| --- | --- | --- | --- |
| `reference` | 是 | `O…`/`P…`，≤64 | 原观察或原操作；`O` 必须属于本 Agent 且属于活动会话 |
| `full` | 否 | 布尔，默认 `false` | `true` 强制本次输出 full；`false` 或省略由服务端按基线与编码成本决定 |
| `image` | 否 | 布尔，默认 `false` | `true` 请求与本次树同次采集的一张图片；图片失败不影响有效核心树 |
| `wait_ms` | 否 | 整数 0–10000，默认 `0` | 采集前等待；等待期间撤销返回 `409 control_revoked` |

输出由服务端基线状态决定：

- `observe(O1)`：若 O1 是 `ready` 的 full 候选，先把它确认为基线，然后执行一次新采集。
  新采集通常相对 O1 返回 delta；页面未变时是空差异（只有 `format`、`base`、`view`）。
- `observe(P…)`：强制 full。P 必须属于本 Agent 且其原会话仍活动。
- 引用已结束会话的 `O` → `409 session_ended`；O 不存在或不属于本 Agent →
  `409 observation_not_found`；P 已封闭 → `409 request_closed`；P 不存在 →
  `409 operation_unknown`；P 属于本 Agent 但原会话已结束或不可用 →
  `409 reference_has_no_session`。
  `observe` 不要求 O 仍为 `ready`（`claimed`/`expired` 也可再次观察），但只有
  `ready` 的 full 候选会被确认为基线。

`observe({reference:"P9"})` 的 `reference` 引用本 Agent 已有的 P9 原操作，不是本次 `observe`
的请求体 P。本次 `observe` 仍是一次新调用，响应 `operation_id` 是本次新分配或由请求头预分配
的 P；把 P9 当作本次调用的 P 只会得到重复 P 的原操作视图，不会执行新采集。

成功响应只含操作号与新观察：

```json
{"operation_id":"P2","observation":{"status":"complete","id":"O2","origin":"P1","revision":2,
 "captured_at":"…","actionable_until":"…",
 "quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"},
 "screen":{"width":1080,"height":2400,"rotation":0},
 "foreground":{"state":"unknown","reason":"display_section_missing"},
 "tree":{"format":"delta","base":"O1","view":"compact"}}}
```

`observe` 不做收敛采样，其观察的质量固定为 `unknown`/`0`/`not_sampled`；`act` 重验失败交付的
新观察同样如此。`quality` 是观察材料事实，不是输入授权：`unstable`/`unknown` 的 O 仍可作为
下一次 `act` 的输入，质量不改变 O 的 `status`、`actionable_until` 或基线确认，规则见 §11。

采集失败：HTTP 503，`observation.status=failed`、`missing=["tree"]`、`error` 为
`hierarchy_unavailable`/`display_unavailable`/`tree_too_large`/`stale_screen`/
`control_ui_on_virtual_display`；采集因撤销失败（`control_revoked`）时，响应带 `session` 与
`recovery`（`entry=end`、`reference=P`；引用 O 时 `input_state=revoked`）。

### 图片

`image=true` 时服务在同一项设备工作内先读取目标屏源图，再取得发布用层级；`act` 的收敛采样
更早完成，因此它在源图之后还会再读一次树（见 `docs/architecture.md` §4）。图片只是本次观察的
附加材料，不进入树或差异正文，也不构成第二份页面事实或图片身份协议。

- 核心树有效、图片成功：`observation.status=complete`，`observation.image` 含
  `captured_at`（源图读取开始）、`width`/`height`（输出实际尺寸）与
  `media_type=image/jpeg`；HTTP 载体 `observation.image.base64` 携带本次图片字节。
- 核心树有效、图片失败或过旧：仍发布有效 O，`status=partial`、`missing=["image"]`，
  `image.error.code` 为 `image_capture_failed`、`image_encode_failed`、`image_too_large`
  或 `image_stale`；失败图片不带尺寸、时间或字节。
- 图片成功、核心树失败：不发布 O（无 `id`/`origin`/`revision`/`actionable_until`），
  `observation.status=partial`、`missing=["tree"]`、`error` 为核心错误；`image` 带
  `read_only=true` 与同屏 `screen`，只作阅读材料，不能作为输入依据。
- 两者都失败：`status=failed`、`missing=["tree"]`，可同时带 `image.error`。
- 未请求图片：整份 `image`/`missing` 省略；服务不截图、不编码，也不返回上一次图片。

附加图片时 `actionable_until` 取源图读取开始与树采集开始中更早者加 60 秒，并受原会话到期
时间上界约束。图片按服务固定预算做一次等比缩小（不放大）与一次 JPEG 编码；超限只报
`image_too_large`，不改判核心树。图片缺失本身不产生 `recovery`。

## 5.1 `POST /api/snap`

```json
{"reference":"O1"}
```

`snap` 在活动会话绑定的屏幕上立即读取一张图，不读取节点树、不派发输入，也不发布观察。
`reference` 必填，沿用 `observe` 的 O/P 引用格式与原会话解析口径：保留中的 `expired`、
`claimed` 或 `retired` O 可以定位原会话，图片始终取当前屏。引用已结束会话的 O/P、他人的
引用、封闭 P、本次请求 P 的自引用、缺失字段、显式 `null`、重复键与未知成员按 §1 的同一规则拒绝（自引用返回 `409 operation_unknown`）。请求体上限
8 KiB；本次请求头预分配的 P 与请求体中的引用各自独立。

成功响应 HTTP 200，只含必现的 `operation_id` 与顶层 `image`：

```json
{"operation_id":"P2","image":{"captured_at":"2026-11-18T00:00:00Z",
 "width":540,"height":1200,"media_type":"image/jpeg","read_only":true,
 "screen":{"width":1080,"height":2400,"rotation":0},"base64":"…"}}
```

- `captured_at` 是源图读取开始的服务 UTC 时间；`width`/`height` 是输出 JPEG 的实际像素
  尺寸；`media_type` 固定 `image/jpeg`；`read_only` 固定为 `true`；
- `screen` 是准入时会话绑定显示的几何快照（`width`/`height`/`rotation`），用来说明缩放
  来源，旋转值是会话记录值；
- `base64` 是 JPEG 字节的 HTTP 载体，经同一有界解码路径进入 Go 客户端与 MCP 的
  `ImageContent`，不进入文本、结构化结果、树历史、恢复账本、日志、探针报告或发布清单；
- 失败使用顶层 `error`；失败、重复 P 与 `status(P)` 摘要都省略 `image`。显式 `null`
  顶层 `image` 与不合法成功图片由客户端拒绝解码；无图的重复 P 摘要可以正常解码。

`snap` 只消耗一次设备工作位：探测目标逻辑屏（至多 5 秒）、读取一次源图（至多 10 秒）、
再次探测同一逻辑屏确认 SFID，然后做一次编码与一次提交。它不分配 O、不确认基线、不续租、
不认领或退休 O，也不生成动作 `outcome`；既有 O 继续按自身有效期与 `act` 实时重验使用。
工作整体上限 25 秒，常规工作相态为 `snap.capture`、`snap.image`、`snap.commit`。两次探测
只证明探测点的表面身份一致；同尺寸旋转等像素变化由普通截图语义覆盖。

| 分支 | `phase` | 结果 |
| --- | --- | --- |
| 成功，或成功提交后响应丢失 | `finished` | 无 `error`；只有首次响应交付图片 |
| 引用、普通屏障、设备忙等接纳拒绝 | `admission` | 沿用既有错误族；原租期已到为 `409 session_expired`；未接纳的 P 不绑定会话 |
| 请求取消先于提交 | `cancelled` | 无服务端 `error`；MCP 投影 `call_cancelled` |
| 25 秒工作截止先于提交且会话仍获授权 | `work_deadline` | `503 image_capture_failed`，消息说明整次读取超时 |
| STOP、显式 `end`、会话撤权或租期在读取中失效 | `revoked` | `409 control_revoked` |
| 探测目标不可用或 SFID 不一致 | `failed` | `503 display_unavailable` |
| 会话显示绑定在读取中变化 | `failed` | `409 stale_screen` |
| 截图失败、编码失败、编码超额、图超龄 | `failed` | `503 image_capture_failed`、`image_encode_failed`、`image_too_large`、`image_stale` |

提交是唯一发布点：锁内重验 P 仍在运行、工作位仍由该 P 持有、原会话 epoch、App/模式、
STOP、原租期、请求与工作期限、会话绑定显示 ID/代次与几何快照，并检查源图读取开始距提交
不超过 15 秒。STOP、撤权、断连或截止先于该判定时丢弃候选；提交先通过时 P 保持成功，随后
断连或回包丢失只影响本次交付。`snap` 的终态省略工具级 `recovery`；`status(P)` 仍可投影
原会话的收尾事实。

## 6. `POST /api/act`

```json
{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"}}}
```

| 顶层字段 | 必填 | 说明 |
| --- | --- | --- |
| `reference` | 是 | 本次要消费的当前 ready O；旧 `observation_id` 不再是别名，按未知成员拒绝 |
| `operation` | 是 | 非 null 对象，见下表 |
| `image` | 否 | 布尔，默认 `false`；`true` 为本次后观察附加一张图片，规则见 §5 图片 |

`operation` 的六个分支（成员出现即校验，多余成员一律拒绝）：

| `type` | 允许成员 | 约束 |
| --- | --- | --- |
| `tap` | `target` + 可选 `bubble` | `target` 为 `{"ref":"n1"}` 或 `{"x":1,"y":2}`；`bubble` 只适用于 ref 目标 |
| `long_press` | `target` + 可选 `bubble` | 同上 |
| `text` | `target` + `text` + 可选 `press_enter` | `ref` 目标必须指向可确认的输入对象（坐标目标在派发时绑定焦点输入）；`text` 必填字符串，可 `""`，≤4096 个 Unicode 码点 |
| `swipe` | `from` + `to` + 可选 `duration_ms` | 两个坐标对象，起终点不能相同，不接受 `ref`；`duration_ms` 为 50–2000 的整数，省略为 300 |
| `scroll` | `container` + `direction` + 可选 `amount` | `container` 为 `{"ref":"n1"}` 或 `{"x":1,"y":2}`；`direction` 为 `up`/`down`/`left`/`right`（大小写敏感）；`amount` 为 `page`（默认）或 `half` |
| `key` | `key` | 固定白名单：`BACK`、`ENTER`、`DEL`、`TAB`、`ESCAPE`、`DPAD_UP`、`DPAD_DOWN`、`HOME`、`APP_SWITCH` |

`bubble` 与 `press_enter` 都是 presence-aware 布尔：省略表示该适用分支的有效值为 `true`，
显式 `false` 关闭；显式 `true` 与省略等价。`null`、非布尔、重复键和大小写变体按
`422 invalid_request` 拒绝。`bubble` 只适用于 `tap`/`long_press` 的 `ref` 目标；坐标目标必须
省略该成员，坐标目标携带 `bubble` 或其他 operation 携带 `bubble` 均为 `422 invalid_request`。
`press_enter` 只适用于 `text`，其他 operation 携带即为 `422 invalid_request`。旧名
`allow_ancestor_fallback` 不是别名，按未知成员拒绝。

`target`/`container`/`from`/`to` 的规则：`ref` 与坐标互斥；坐标必须同时有 `x`、`y`，为 ≥0
的整数；`ref` 不能为空或 `null`；`key`/`text` 不能是 `null`。分支成员互斥：`scroll` 不接受
`target`/`from`/`to`/`key`/`text`/`duration_ms`，其他分支也不接受 `container`/`direction`/
`amount`；`duration_ms` 只在 `swipe` 上出现。`duration_ms` 的显式 `null`、0、负数、非整数与
越界一律拒绝。`swipe` 仍是原始手势，不承诺滚动语义；表达滚动意图用 `scroll`。

`tap`/`long_press` 的 `ref` 目标有效 `bubble=true`（省略或显式 true）时：原目标自身已有请求
动作则仍走下述直接目标规划，不提升；原目标完全没有 `tap`、`long_press`、`text` 任一动作时，
服务沿公开树父链找到第一个 touch-interactive 祖先 A，且只把动作交给 A 一次。A 必须支持请求
动作、未明确禁用、bounds 有效，并能在 A 的作用域内产生唯一可证安全点；A 不存在、不可用或
不支持该动作时返回 `422 unsupported_node_action`，A 支持动作但无安全点时返回
`422 no_safe_point`，都不继续向更远祖先查找，也不认领 O。A 的公开 bounds 通过前置检查、
但规划期 raw/屏幕/公开交集为空时返回 `422 invalid_target` 且 `reason=no_geometry`、
`scope=ancestor`、`scope_ref=A`，不把子节点误报为几何错误对象。静态规划
同时绑定原 child C 与 A。认领后必须重新唯一匹配 C 和 A，证明 A 仍是 fresh C 的祖先和最近
交互祖先、C 的公开 Role 与完整 Label 未变、C 未新增任何 touch-interactive 能力、A 仍有请求
动作与有效 bounds，并在 A 作用域内重算安全点。C 或 A 丢失/歧义、C 内容被替换、出现更近交互
祖先或安全点落到另一独立交互目标时返回 `409 target_changed`，零派发；C 丢失时即使 A 仍唯一
也不能把授权解绑为 A。有效 `bubble=false` 完全沿用下述直接目标算法与错误；坐标目标不进入该
分支。

`tap`/`long_press` 的 `ref` 目标使用子树安全落点规划。服务以目标 `raw` 矩形、逻辑屏与
公开 `bounds` 的非空交集为基础，只把目标子树内 `actions` 含 `tap`、`long_press`、`text`
任一种的节点（无论 `enabled` 值）及其后代、以及 `context` 边界视为阻塞；仅含 `swipe` 的
节点不自动成为阻塞边界。候选顺序固定为：

1. 原中心；仅当它在基础矩形外时改测基础矩形中心；
2. 目标子树内的非阻塞承载节点中心，非匿名节点先于匿名节点，各自保持先序；中心越出基础
   矩形时改测该承载与基础矩形的非空交集中心；
3. 先固定基础矩形横向中心线，取纵向包含该线的阻塞区间扣除后的最长剩余区间中点；未通过时
   再固定纵向中心线，对称求横向区间。等长区间取起点较小者。

每个候选都必须避开阻塞区，并让目标子树内包含该点的最深非 root、非 `context` 节点唯一属于
目标非阻塞作用域。服务选择第一个通过的候选；没有可证候选时在认领前返回
`422 no_safe_point`，不采集、不派发，O 保持 `ready`。目标子树外同级节点不参与
阻塞或候选排序。坐标目标、`text`、`swipe`、`scroll` 与 `key` 不使用这套规划。

`tap`/`long_press` 的 `ref` 静态拒绝按认领前原因分成三个稳定 code，均为
`outcome=not_executed`；静态拒绝自身不认领 O、不采集、不派发。并发 STOP、到期或其他工作
消费仍按既有投影处理：

| code | 条件 | `reason` |
| --- | --- | --- |
| `422 invalid_target` | 当前规划对象的结构或几何不可用 | `root` / `context` / `no_geometry` |
| `422 unsupported_node_action` | 目标缺请求动作，且所选提升策略不能满足执行前提 | `target_missing_action` / `target_has_other_action` / `ancestor_missing_action` / `ancestor_invalid` / `no_interactive_ancestor` |
| `422 no_safe_point` | C 或 A 支持请求动作，但原固定候选均未通过规划谓词 | `blocked` / `ambiguous` / `owner_mismatch` / `mixed` |

`node_not_found`（409）、`node_disabled`（409）与 `hierarchy_unavailable`（503）语义不变，
不附 `reason`。上述 `reason` 只属于 `tap`/`long_press` 的 ref 静态分支；其他工具的同名 code
不继承该枚举。`invalid_target` 与 `no_safe_point` 用 `scope`（`target`/`ancestor`）和
`scope_ref` 标明实际规划对象；`unsupported_node_action` 总是带 `target_actions`，
`ancestor_missing_action`/`ancestor_invalid` 另带 `ancestor_ref` 与 `ancestor_actions`；
`no_safe_point` 的 `scope=ancestor` 另带 `ancestor_ref`。动作数组按 `tap`、`long_press`、`text`、`swipe`
排序去重；无动作时 wire 为 `[]`，不是省略或 `null`。

`no_safe_point` 的 `occluding_refs`、`ambiguous_refs` 与 `owner_refs` 各最多 3 项，只归集
原规划实际测试过的候选；分别表示对候选点有实际阻塞证据的 owner、首个失败解析层上的最深
并列节点、以及整树唯一命中但不在允许作用域的节点。多个类别同时出现时 `reason=mixed`，各
列表按自身证据出现；无证据的列表省略，不用空数组充当事实。原候选全失败且作用域有效时，
服务在基础矩形取固定 3×3 点（y 外层、x 内层，各轴 1/6、1/2、5/6 位置），跳过原候选坐标；
只有同时通过原分支谓词与整树唯一归属/source 证明的第一个点才作为 `suggested_point` 返回，
唯一 owner 是非阻塞承载节点时附 `suggested_carrier_ref`。建议点只是同一 O 上的提示，不是
执行授权：原请求仍被拒绝，坐标重试必须使用新 P 并独立通过准入与 fresh 证明；找不到完整
证明时建议组整体省略，不证明整个平面无解。

在有效 `bubble=false`，或默认提升的最近祖先不存在/不可用/不支持请求动作而返回
`422 unsupported_node_action` 的路径中，仅当目标不是 root/`context` 且不含 `tap`、
`long_press`、`text` 任一动作时尝试祖先引导；目标含任一此类动作、即使不含
请求动作，也视为交互边界，不跳过自身。服务沿唯一父链向上，遇到 `context` 即停止，并把第一个
含上述任一动作的节点作为唯一候选祖先。该祖先未明确禁用、bounds 与 source 有效、支持请求
动作且按同一规划能产生可证点时，才尝试引导；任一条件不满足即整组省略，不继续越过它寻找
更外层节点。`safe_point` 还必须通过整树单点解析并证明命中该祖先或其非阻塞承载作用域；
无法完整证明时整组省略。`safe_carrier_ref` 可指原目标或该祖先的非阻塞后代承载节点候选，只解释坐标
来源，不是可直接点击的替代 ref。该引导自身不改派祖先；默认 `bubble=true` 的目标提升是
上一段的独立契约，不是从该引导静默改写而来。`occluding_ref` 只指出目标原中心的
一个可定位阻塞 owner，不表示整个目标表面不可点；`tap`/`long_press` 的 ref 静态候选耗尽
出口不再产生该键，改由 `occluding_refs` 表达。

`scroll` 是受控滚动尝试：`container` 与 `direction` 必填，`amount` 省略为 `page`；
`direction` 是调用方声明的阅读/视口方向，不是服务证明的设备滚动轴。映射固定为：

| `direction` | 查看的内容 | 注入手势 |
| --- | --- | --- |
| `down` | 更靠后的内容 | 容器下部 → 上部 |
| `up` | 更靠前的内容 | 容器上部 → 下部 |
| `right` | 更靠右的内容 | 容器右部 → 左部 |
| `left` | 更靠左的内容 | 容器左部 → 右部 |

```json
{"reference":"O1","operation":{"type":"scroll","container":{"ref":"n1"},
 "direction":"down","amount":"page"}}
```

`container` 是容器选择器：

- `ref` 必须直接指向公开节点，且该节点 `role=scroll`、`actions` 含 `swipe`、非 root、非
  context、有 bounds、`enabled` 不为已知 `false`。服务不做“叶 ref 向上晋升”：ref 不存在时按
  既有 `node_not_found` 拒绝，存在但不是可滚动容器时返回 `422 unsupported_node_action`。
- 坐标为 `{"x":1,"y":2}` 时，服务取包含该点的全部公开候选，逐个归约到最近的可滚动祖先
  （含自身），按容器原始节点去重：恰好一个容器时使用它；出现多个不同容器时返回
  `422 ambiguous_point`，要求调用方显式给 `container.ref`；没有容器时返回
  `422 unsupported_node_action`。同一容器内的多个内容候选（例如同点同深度的 `view`/`text`）
  不是歧义。服务不自动选择“最大的滚动容器”，也不校验滚动轴。

容器 bounds 必须非空、可裁剪且有正面积。服务固定容器的正交中心坐标，在一维作用域区间中选出
最长的可证子区间，再把 `page`/`half` 的留白与 60%/30% 幅度作用在该子区间上；因此局部滚动
作用域即使不覆盖原起终点，也会缩短或移动本次轨迹，幅度不承诺整屏距离。`up`/`left` 在等长
区间中取坐标较小者，`down`/`right` 取较大者。静态阶段不可执行（没有任何可证区间，或该区间
产生的行程小于 `48px`）时，在认领前返回 `422 unsupported_node_action`，不采集、不认领，
O 保持 `ready`。

可证区间这样得到：以容器 `raw` 矩形与显示屏的交集为裁剪矩形，与容器公开矩形相交后取其滚动轴
区间作为基础区间；遍历整棵公开树，继承每个节点最近的公开滚动作用域（含自身），把不属于选定
容器、也不属于其 `raw` 外层祖先的候选沿轴投影成阻塞区间并从基础区间中扣除。没有滚动祖先的
候选不阻塞；最近作用域的来源缺失或越界时按阻塞处理，不回退到更外层。父矩形不跨越中心线时
仍要检查其伸出的子节点。容器自身或其外层包裹祖先不阻塞。

静态检查（不认领、不派发）：会话必须 `active`；O 必须为 `ready` 且未过期；ref 节点存在、
非 root、非 context、有 bounds、`enabled` 不为已知 `false`，并具备对应动作能力；`text` 还要
求 `role=input`、`actions` 含 `text`、`protected` 明确为 `false`；坐标目标必须落在目标逻辑屏
内；`HOME`/`APP_SWITCH` 仅在主屏（`display.ID==0`）可用。坐标目标（`tap`/`long_press`/`text`
的 `target`、`swipe` 的 `from`）还要在观察的公开树中解析到唯一节点：取包含该点、有 bounds、
非 root、非 context 的最深节点；同级并列候选返回 `422 ambiguous_point`，不认领、不采集；
解析不到节点时维持前台级检查。`scroll` 不在此列：它以 `container` 的解析结果与中心线可证
区间为静态授权，不要求起点叶节点可解析。

静态拒绝且 O 仍可用时（`node_not_found`、`node_disabled`、`unsupported_node_action`、
`invalid_target`、`no_safe_point`、`invalid_coordinates`、`ambiguous_point`、
`unsupported_key` 等），什么都没派发、O 未被认领，
`recovery` 为 `entry=retry`、`reference=O`、`input_state=effective`。

通过后：取得唯一工作位置 → O 单向 `ready→claimed` → 普通活动续期（`max(expires_at, now+2分钟)`，
不继承显式时长） → 实时重验（目标显示仍存在、
重新采集、前台包/任务与观察时一致；ref 目标使用下面的分层重验；坐标目标解析到唯一节点时，
仍要求完整内容身份、祖先链和原点解析一致）→ 派发本次动作所需的输入（`text` 为必要聚焦 +
显示绑定写入，并以写入调用内同节点刷新值确认；有效 `press_enter=true` 时在独立前台/读数
门禁后再至多派发一次 ENTER，任何一步失败都不重放）。

ref 目标按 Tier 1→2→3 匹配：Tier 1 要求目标 bounds 相同，祖先链 L1 不比较祖先 bounds、
Focused/Selected；Tier 2 在 L1 相同时允许受限目标微移（重叠率至少 0.50，并检查尺寸、中心与
屏内几何）；Tier 3 使用去掉可证明透明层的 L2（重叠率至少 0.60），且要求非根强锚点。
透明层的 text/description/resource 必须三项均已知为空，交互与状态均已知安全。每 Tier 都要求
旧→新及新→旧双向唯一；多候选立即拒绝，只有零候选才进入下一 Tier。目标内容/能力的已知
冲突或 presence 变化不允许靠几何猜测；动态文案改写不提供额外 Tier。

`tap`/`long_press`/`text` 的 ref 在新鲜公开树中先证明原点，仍安全则保留；原点失效时可在
已匹配载体内重算安全点。原载体失效后，只有目标自身的稳定非空 text/description 或双端唯一
resource 能独立证明身份，才可在同一目标子树内重新选点/载体；共享祖先不是该身份证据。
所有点均受同一子树安全谓词约束，不越过 blocker、不晋升到祖先。实际派发使用新鲜 frame
给出的坐标。无候选/歧义为 `409 target_changed`；预算耗尽、畸形父链或投影损坏为
`503 hierarchy_unavailable`。STOP 优先为 `control_revoked`，不交付重试 O、不再派发。

这组证明只覆盖公开树模型内的目标子树，不覆盖子树外同级或跨窗口 z 序、未发布的透明拦截层、
应用自定义触摸代理或业务回调。子树外同级重叠不导致该 `ref` 规划自拒，也不在树模型中证明
谁消费真实触摸；`executed` 只证明输入已派发，不证明 Android 实际事件消费者或应用业务结果。
祖先与坐标引导只对生成它的 O 及同一观察有效；后续调用仍须新 P、正常准入与实时重验，O 过期、
被消费、替换或撤权后不能继续使用。

`scroll` 的授权单位是同一可证明的滚动容器，不要求起点叶身份不变。容器通过同一 Tier 1–3
双向唯一匹配后，仍须有公开滚动能力、裁剪矩形和沿轴至少 0.50 的重叠；离轴两边变化不得超过
`max(8px, min(旧宽/高, 新宽/高)/50)`。允许轴向伸缩，先证明原段仍安全；失效时只在该容器
的新鲜安全区间中重算起终点，再证明整段。没有足够行程或安全区间则 `target_changed`。
ref 与坐标容器选择器都按此处理，实际 Swipe 使用新鲜 frame 与受控滚动时长。

raw `swipe` 保持原 from/to 与显式 `duration_ms`（省略默认 300ms）；坐标 tap/text 保持原点
授权，不因 ref 匹配获得改点能力。`scroll` 不证明真实滚动方向、触摸消费者或业务安全。

重验失败且已取得可用新帧并完成交付时，服务把该帧发布为新的 `ready` 观察随结果返回：
`target_changed`、`foreground_changed` 与重验阶段的 `node_not_found` 返回 `409`，结果带
`observation`（`status=complete`）与 `recovery`（`entry=retry`、`reference=O`、
`input_state=effective`）；调用方用该新 O 与新 `P` 重试同一工具。交付复用同一次读取的帧，
不额外采集、不做收敛采样，也不确认基线。未交付时结果不带 `observation`，见下段。

重验失败时的 `recovery` 按错误码保持：`display_unavailable`、`hierarchy_unavailable`、
`foreground_unknown` 为 `entry=observe`、`reference=P`、`input_state=retired`；
`control_revoked`、`session_ended` 为 `entry=end`、`reference=P`、`input_state=revoked`。
取得帧后交付门禁拒绝（`tree_too_large`、采集帧龄超限、目标虚拟屏上出现控制 App 的可见节点、
提交时工作已撤销，或新帧前台无法确认）不改变本次 `error`：仍是
`target_changed`/`foreground_changed`/`node_not_found`，只是不带 `observation`，`recovery` 为
`entry=observe`、`reference=P`、`input_state=retired`。所有失败都不派发输入。

结果分支：

| `outcome` | 含义 | 附带 |
| --- | --- | --- |
| `executed` | 输入已派发且有完成证据（`text` 以一次写入调用中同一候选节点刷新后的完整值精确相等确认；默认/true 复合 text 还要求 `write=executed` 且 `enter=executed`） | 后观察（full 或 delta）；复合 text 另带 `text_input`；若期间被撤销，则 `recovery` 为 `entry=status`、`reference=P`、`input_state=revoked`、`cause=executed_revoked`，且无后观察；已认领 `text` 的实时投影在原 cleanup 非 pending 时把该入口改为 `entry=end` 并给 `blocking_code`。`scroll` 必带 `effect`：有后树时为计算结果，撤销或无法计算时为 `unknown` |
| `not_executed` | 未派发（静态拒绝、重验失败、工作被撤销、完整写入回执证明写原语未调用等） | `error`、`recovery`；重验失败且已交付新帧时另带 `observation`。默认/true 复合 text 的已认领结果仍带初始或最后快照 `text_input`；`text` 错误另带 `stage`/`reason`/`dispatch`；`not_executed` 不带 `effect` |
| `uncertain` | 已派发但无法确认完成（传输失败、读回失败、超时） | `error`（`action_result_uncertain` / `text_target_uncertain` / `text_result_uncertain`）、`recovery`；复合 text 带当前 `text_input`，focus 已派发但两键未派发时整体仍为 `uncertain`；`text` 错误另带 `stage`/`reason`/`dispatch`。原操作已结束而其原会话仍活动时，`text` 的语义恢复为 `entry=observe`、`reference=P`、`input_state=retired`；会话/耐久/设备/忙等实时事实仍可按 §6 覆盖为 `end`/`status`/`user` 并给 `blocking_code`。`scroll` 必带 `effect=unknown`（没有可计算的后树） |

`scroll` 的 `effect` 只表示是否观察到容器内内容位移：`observed`/`not_observed`/`unknown` 的
含义与固定判据见 §11。`effect` 不改写 `outcome`、`error`、`recovery` 或 `observation.status`；
`executed + effect=unknown` 是合法结果。首次受理的 `scroll` 结果与仍保留原操作的 P 视图携带
它；`executed`/`uncertain` 时不得省略（没有可计算的结果时投影 `unknown`），`not_executed`
永不携带，只有原操作事实已封闭或已完成记录被淘汰时才整体省略。

- 每次派发只有一个窗口，`may_dispatch` 仅表示调用仍未返回。派发调用返回时，窗口一次性
  关闭为 `terminated`、`not_dispatched` 或缺少可用结束回执的持久终态 `unknown`；后者保持
  工具结果 `uncertain`，不阻止后续 `observe`、`act` 等普通调用。用 `status(P)` 查询原结果，
  用一次新 `observe` 收窄不确定性；不重放原输入。
- 输入派发后即使 `text` 读回失败也不回滚、不重放；原 O 不再归还。
- 后观察在树收敛后采集（见 §1）；收敛期间被撤销时 `outcome` 仍是 `executed`、
  `recovery` 为 `entry=status`、`reference=P`、`input_state=revoked`、`cause=executed_revoked`、
  无后观察。收敛预算耗尽不算后观察失败：照常发布最后一帧。
- `text` 的 `press_enter` 省略或有效为 true 时执行复合流程。ref 目标在认领后的新鲜帧中已满足
  安全输入条件且 `focused`，并通过完整绑定门禁（focus 过滤前锚点唯一性、严格同 TaskID、
  package/class 与 resource/description presence/值、几何）时跳过焦点点击；否则只执行一次
  必要焦点点击并在 afterTap 帧绑定。坐标目标保持原点授权下的一个聚焦与按点绑定。随后一次
  `setTextOnDisplay` 把目标框全文设为 text（不追加旧内容）。写入完成证据是：完整终止回执、
  `candidateCount=1`、`issued=true`，且固定 helper 对该唯一候选节点刷新后的完整 value 与
  `operation.text` 精确相等；`accepted` 只作为布尔回执记录，不单独否决。比较保留空格、换行、
  Unicode 与空字符串，不做 trim、归一化或包含判断；空框 hint 按 helper 行为返回空串，`null`
  永远表示未知。写前不调用 GetText，写后不以另一棵树的重新采集/重绑定/GetText 证明写入；
  提交门禁仍重验原 session/work/P、显示 ID 与 generation、STOP/终态和期限。写入候选不成立
  时不重写、不补点击、不补第二次 SetText。
- 显式 `press_enter=false` 在写入证据提交后完成：零 ENTER，不返回 `text_input`；后续失焦或
  位移不改变已确认的写入事实。默认/true 在 write 提交后、发送 ENTER 前做一次只读前台核对和
  一次严格 `getTextOnDisplay`（使用本次 set 的结构 target）：原任务上下文仍一致、候选扫描时
  原 target 唯一且 focused、同节点刷新读取的实际值仍精确等于请求，才进入 ENTER 的独立许可；
  至多发送一次 `KEYCODE_ENTER`。不能确认时保留 `write=executed,enter=not_executed`，整体为
  `uncertain`。返回值固定携带 `text_input:{"write":…,"enter":…}`，两个键都出现，枚举仅为
  `not_executed|uncertain|executed`；合法组合只有 `(N,N)`、`(U,N)`、`(E,N)`、`(E,U)`、
  `(E,E)`。ENTER 非 `not_executed` 必须已有 `write=executed`。读回成功只确认写入，ENTER 只
  确认按键原语，两者都不证明搜索、提交、导航或业务结果。`text_input.enter=uncertain` 时先
  观察当前页面，不原样重发 ENTER；`write=executed,enter=not_executed` 且原会话仍活动时，
  可取新 O 后按需单独发送 `operation.type=key,operation.key=ENTER`，不必重新填字。ref 输入
  绑定按 exact（稳定锚点与相同 bounds）、near（覆盖点击点且重叠率至少 0.80）、shifted
  （稳定锚点、同一水平带与受限尺寸的纵向位移）依次尝试；唯一性在 focus 过滤前统计，焦点仅作
  安全门禁。所有分支保持 package/class 与 resource/description 的 presence/值一致；要求
  enabled 已知为真、password 已知为假、fresh focused 已知为真。Get/Set 使用实际绑定的新鲜
  bounds，set 只派发一次。相同 TaskID 只证明任务上下文，不证明 window 身份，完全同观测签名
  的字段替换不可辨识。
- 复合 `text` 的 `text_input` 是 engine 在锁内发布的不可变快照：首次结果、运行中
  `status(P)`、重复 P 与冻结结果都复制同一对象，不按 `phase` 或 `error.details` 重建。
  冻结先到后，迟到回执不能回补；`write=executed,enter=not_executed` 不自动补发 ENTER，
  `enter=uncertain` 也不是重试授权。焦点点击不在公开 `text_input` 内，但计入整体 outcome；
  只有焦点点击、SetText 与 ENTER 都没有获得派发许可或均已确定未派发时整体才是
  `not_executed`。显式 false 的 `KnownOutcome=executed` 与 write 提交在同一短锁；复合 text
  只在 write 与 enter 均确认后设置。
- `text` 的路由、聚焦、写入、读回或 ENTER 失败在 `error.details` 固定给出：

  | 字段 | 类型与取值 |
  | --- | --- |
  | `stage` | `target` / `focus` / `write` / `verify` / `enter`；失败所在逻辑环节 |
  | `reason` | 封闭字符串枚举，见下表；调用方不解析 `message` |
  | `dispatch` | 固定 `focus`/`write`/`enter` 三键对象；各值为 `not_attempted` / `not_dispatched` / `dispatched` / `unknown`，分别表达该原语是否被调用，不等同于内容确认成功 |
  | `candidate_count` | 非负整数；仅该失败环节取得有效候选计数时出现，`0` 保留，未知时省略 |

  | `stage` | `reason` | 顶层 code / HTTP | 含义 |
  | --- | --- | --- | --- |
  | `target` | `target_not_editable` / `target_protected` | `text_target_uncertain` / 422 | ref 静态输入能力或保护拒绝；未认领，三原语均 `not_attempted` |
  | `target` | `target_unconfirmed` | `target_changed` / 409 | 原目标无法由新鲜证据确认；未派发 |
  | `focus` | `focus_unconfirmed` | `text_target_uncertain` / 409 或 `action_result_uncertain` / 504 | 终止回执后 afterTap 绑定失败用前者；回执未知或 terminated+error 用后者 |
  | `focus` | `focus_not_sent` | `device_unavailable` / 503 | 聚焦明确未派发 |
  | `write` | `no_unique_target` | `text_target_uncertain` / 409 | 完整 set 响应且候选不唯一；`dispatch.write=not_dispatched` |
  | `write` | `dispatch_not_sent` | `device_unavailable` / 503 | 写入原语确定未派发 |
  | `write` | `receipt_unknown` / `rpc_result_invalid` | `text_result_uncertain` / 409 | 无完整可靠回执或响应畸形；派发事实 `unknown` |
  | `verify` | `readback_unavailable` / `readback_mismatch` | `text_result_uncertain` / 409 | 已派发；同节点刷新值不可读或完整值不等 |
  | `enter` | `enter_target_unconfirmed` / `enter_context_unconfirmed` | `text_target_uncertain` / 409 | write 已确认，但 ENTER 目标或前台无法确认；ENTER 未派发 |
  | `enter` | `enter_not_sent` / `enter_unconfirmed` | `action_result_uncertain` / 504 | ENTER 明确未派发，或回执未知/终止但带错误 |
  | 已记录环节 | `authorization_revoked` / `deadline_exceeded` | `control_revoked` 或 `text_result_uncertain` / 409 | 撤销/归属失效用前者；仅工作期限到且原会话仍活动用后者 |

  `dispatch` 区分“尚未申请原语许可”和“已经确定未派发”；跳过的聚焦、false 的 ENTER 为
  `not_attempted`。同一个错误不混用 tap 的 `reason` 与 text 的 `reason`。前台、设备、层级、
  会话与静态 JSON 等公共错误保留各自 code 与详情，不伪造 text 设备事实。`message` 为对应
  原因的固定中文短句，不嵌入请求值、实际值、原始异常或 RPC 响应；不提供 expected/actual
  text、文本 hash、长度、节点、截图或第二套恢复动作。
- 已认领 `text` 的首次终态、finished `status(P)` 与重复 P 共用同一只读 live-context 投影。
  原会话仍是当前活动会话且与认领时 sessionEpoch/AppInstanceID/ModeGeneration/显示
  ID 与 generation 绑定一致时：写入/ENTER uncertain、已认领但未派发任何原语而目标条件不再
  成立、后观察失败分别投影为 `observe`/`P`；账本/回执耐久、会话撤销/结束/过期、startup
  recovering、设备或控制同步、需要人处理的控制条件、其他普通 work/busy 按固定优先级覆盖为
  `end`/`status`/`user` 并给 `blocking_code`（会话撤销/结束/过期或绑定失效且原 cleanup
  非 pending 时为 `end`；同 agent 的 busy 另给 `blocking_operation_id`），
  解除后恢复原语义入口。临时阻塞只改结果副本，不写回原操作的 recovery；过期会话即使
  housekeeping 尚未运行也按 `session_expired` 表达。
- 坐标目标只证明一个点：`tap`/`long_press`/`text` 证明其 `target`，`swipe` 只证明起点
  `from`；终点是方向与幅度，不参与节点身份。观察时点解析不到节点时按前台级派发。`scroll`
  不证明叶节点或单点：它证明选定容器与新鲜 frame 中的整段轨迹，坐标只是容器选择器。
- 后观察失败时：HTTP 503，但 `outcome` 仍是 `executed`，`observation.status=failed`，
  `recovery` 为 `entry=observe`、`reference=P`、`input_state=retired`、
  `cause=post_observation_failed`；不能因后观察失败改判未执行。
- 失败码：`node_not_found`、`node_disabled`、`unsupported_node_action`、`invalid_target`、
  `no_safe_point`、`invalid_coordinates`、`ambiguous_point`、`unsupported_key`、
  `invalid_request`（含超长文本）、
  `text_target_uncertain`、`text_result_uncertain`、`action_result_uncertain`、
  `target_changed`、`foreground_changed`、`foreground_unknown`、
  `display_unavailable`、`hierarchy_unavailable`、`device_unavailable`、`stale_screen`、
  `device_busy`、`session_ended`、`observation_retired`、`observation_expired`、
  `observation_claimed`、`control_revoked`。

`scroll` 不新增错误码：`container.ref` 指向不存在的节点沿用 `node_not_found`；ref 存在但不是
可滚动容器、点选择器找不到容器、中心线上没有任何可证区间或该区间行程不足返回
`unsupported_node_action`；点选择器出现多个不同滚动作用域返回 `ambiguous_point`；容器在新鲜树
中不成立，或无法证明/重算安全轨迹返回 `target_changed`。`ambiguous_point` 对 raw `swipe`
的提示是可执行的：调整起点，或改用 `scroll` 的 `container` 显式指定容器；错误码与判定不变，
raw `swipe` 仍不接受 `ref`。

## 7. `POST /api/end`

```json
{"reference":"P1"}
```

`reference` 必填，可为 O 或 P，语义是“该引用所属的原会话”。响应：

```json
{"operation_id":"P5","outcome":"executed","target_reference":"P1",
 "session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",
   "started_at":"…","ended_at":"…","reason":"client_end"},
 "cleanup":{"state":"pending","execution":"confirmed","tasks":"pending",
   "recents":"pending","display":"pending"},
 "recovery":{"entry":"status","reference":"P","input_state":"revoked",
   "cause":"cleanup_pending"}}
```

- `end` 只撤权并启动独立收尾，不等待设备工作、账本或网络；`cleanup` 通常先是
  `pending`。`pending` 时可返回 `recovery=entry=status`、`reference=P`、`input_state=revoked`、
  `cause=cleanup_pending`；`unconfirmed` 表示有限补偿已停止，只保留五项事实，不再暗示继续
  轮询就能推进。
- 重复 `end` 返回同一收尾进度，不新建第二条链。
- 对从未被接纳的 P：`request_closed=true`、`session=null`、
  `cleanup` 全为 `not_applicable`。
- 已结束且无可收尾资源的会话：`cleanup.state=complete`。
- 无法定位的 P：`409 operation_unknown`、`phase=reference_unknown`、`outcome=uncertain`、
  `request_closed=true`，返回 `session:null` 并省略 `cleanup`。该次关闭封闭该 P 的未来接纳，但不声称
  过去未执行，也不结束当前其它会话。
- 其它非法或不可用引用：`409 observation_not_found`、`operation_not_found`、
  `session_not_found` 或 `422 invalid_request`；`session_not_found` 与
  `reference_has_no_session` 可给 `recovery=entry=status/reference=P`。

## 8. `POST /v1/control/sync`

控制 App 心跳。请求字段（`internal/protocol.AppSyncRequest`）：

| 字段 | 必填 | 类型 | 说明 |
| --- | --- | --- | --- |
| `contract_version` | 是 | 字符串 | 必须为 `"20"`，否则 `409 contract_mismatch` |
| `app_instance_id` | 是 | UUID | App 进程实例；变化会撤销旧归属 |
| `app_version` | 否（App 应发送） | 字符串 | 仅发行标识 |
| `selected_mode` | 是 | `virtual`/`primary` | 用户选择 |
| `mode_generation` | 是 | 1–64 字符 | 选择代次 |
| `mode_ready` | 否（App 应发送） | 布尔 | 所选模式可用 |
| `stop_ready` | 否（App 应发送） | 布尔 | 停止通道可用 |
| `notifications_enabled` | 否（App 应发送） | 布尔 | 通知可用 |
| `background_running` | 否（App 应发送） | 布尔 | 前台服务运行中 |
| `power_interactive` | 否 | 布尔或 `null` | 未知为 `null`/省略 |
| `keyguard_locked` | 否 | 布尔或 `null` | 未知为 `null`/省略 |
| `primary_display` | 否 | 对象或 `null` | 出现时 `display_id` 必须为 0，宽/高/密度 >0，旋转 0–3，`generation` 非空；可选 `ime_hide`（布尔） |
| `display` | 否 | 对象或 `null` | 自有虚拟屏；出现时 `display_id` >0，其余同上；可选 `ime_hide`（布尔） |
| `command_results` | 否 | 数组 | 省略/null 按 `[]`；元素 `{command_id(1–64), ok(bool), error_code(string 或 null, ≤64)}` |
| `stop_pending` | 否 | 对象或 `null` | `{seq(>0), reason(≤64)}`，表示未被服务受理的本地停止 |
| `message_acks` | 否 | 数组 | 省略/null 按 `[]`；App 已保存的消息 ID，至多 32 个；ID 为 `M` + 正整数；未知/已释放 ID 忽略 |

校验规则：显式校验 `contract_version`、`app_instance_id`、`selected_mode`、
`mode_generation`、非 null 的 `primary_display`/`display`/`stop_pending`、每条
`command_results` 与 `message_acks`；其余字段缺失时按 `false`/空值处理，可用性通常因此为
`blocked`，不返回 `422`。`command_results`/`message_acks` 省略或为 `null` 时按空数组处理。
未知成员、重复键与类型混用由严格解码拒绝。

响应：

```json
{
  "contract_version": "20",
  "service_instance_id": "…",
  "control": {
    "revision": 42,
    "availability": {"state":"ready"},
    "session": {"origin":"P1","agent":"local-agent","mode":"virtual","state":"active",
      "started_at":"…","expires_at":"…"},
    "work": {"operation_id":"P2","kind":"act","phase":"act.capture"},
    "cleanup": null
  },
  "commands": [],
  "messages": [{"id":"M12","seq":12,"text":"…","created_at":"…"}],
  "stop": {"seq":7,"state":"accepted"}
}
```

- `availability.state`：`ready`/`recovering`/`blocked`；非 `ready` 时可带 `reason`；
- `session`：`null` 或 `{origin, agent?, mode, state, started_at?, expires_at?, ended_at?, reason?}`；
- `work`：`null` 或 `{operation_id, kind, phase}`；`kind` 为
  `start`/`observe`/`snap`/`wait_for`/`act`/`app`/`shell`；
  `kind=app` 时另带可选 `app_kind`，取 `find`/`list`/`open`/`claim`/`handoff`/`close`/`kill`；
- `cleanup`：`null` 或 `{state, execution?, tasks?, recents?, display?}`；只与同一
  `control.session` 同属一个原会话；
- `commands`：数组，每拍至多一条显示命令；`create_display` 仅在有 `starting` 会话且显示助手
  已就绪时下发；`close_display` 在收尾/恢复需要精确释放时下发；`cancel_create` 只按原 App
  实例与 `target_create_command_id` 撤销尚未终结的创建；
- `messages`：数组，未确认的 Agent 消息，按创建顺序，每拍至多 8 条 / 32 KiB 正文；未在
  `message_acks` 中确认的消息会在后续同步重发；App 按 `id` 去重；
- `stop`：仅当本请求携带 `stop_pending` 且服务已绑定该边界时出现；`state` 为
  `accepted`/`duplicate`；
- `control.revision` 只在可用性、会话、工作、收尾等实际控制事实变化时递增；同一
  `service_instance_id` 内单调。

## 9. `POST /v1/control/stop`

```json
{"contract_version":"20","app_instance_id":"…","stop_seq":7,"reason":"user_stop"}
```

- **不校验普通契约**：`contract_version` 字段存在但不参与准入，服务与 App 契约版本
  不一致时仍可停止；
- `app_instance_id` 必须是 UUID，`stop_seq` 必须 >0；
- `reason` 为空或超过 64 字符时按 `user_stop` 处理；
- 重复或过期 `seq` 不报错，只返回 `state=duplicate`。

```json
{"contract_version":"20","service_instance_id":"…","accepted":true,"stop_seq":7,"state":"revoked"}
```

`accepted` 恒为 `true`；`state` 为 `revoked`（绑定时撤销了活动会话）、`recorded`
（无活动会话，仅记录边界）或 `duplicate`（未生效）。重复 `seq` 同样返回
`accepted=true` 与 `state=duplicate`。响应不包含普通服务状态、命令或设备事实，也不表示
物理资源已释放。

## 10. `GET /health`

无认证，只表示进程可应答，不代表设备就绪或资源干净：

```json
{"status":"ok","contract_version":"20","service_instance_id":"…","release_version":"…"}
```

## 11. 数据对象

### Screen / Foreground

```json
"screen": {"width":1080,"height":2400,"rotation":0}
"foreground": {"state":"present","package":"com.example","task_id":12}
"foreground": {"state":"unknown","reason":"display_section_missing"}
"foreground": {"state":"none"}
```

`present` 时 `package` 可省略（未知），`task_id` 可为 `null`；`unknown` 带受控 `reason`；
`none` 表示已确认无前台应用。

### Observation 与 Quality

```json
{"status":"complete","id":"O3","origin":"P1","revision":3,"captured_at":"…",
 "actionable_until":"…",
 "quality":{"state":"unstable","samples":5,"stop_reason":"max_samples"},
 "screen":{"width":1080,"height":2400,"rotation":0},
 "foreground":{"state":"present","package":"com.example","task_id":12},
 "tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",
   "root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}
```

Observation 的编码顺序固定为 `status`、`id`、`origin`、`revision`、`captured_at`、
`actionable_until`、`quality`、`screen`、`foreground`、`tree`、`missing`、`image`、`error`。
`quality` 是对象；出现时三个成员总是编码，成员顺序固定为 `state`、`samples`、`stop_reason`：

| 成员 | 取值与规则 |
| --- | --- |
| `state` | `stable`/`unstable`/`unknown` |
| `samples` | 本次 settle 的有效可比较帧数，非负整数；未做收敛采样的观察为 `0` |
| `stop_reason` | `stable`/`max_samples`/`budget`/`unreadable`/`not_sampled`/`condition`/`timeout` |

- `samples` 受固定采样策略与刷新次数约束：`observe` 与重验失败交付的观察为 `0`；
  `act` 无图最多 5 帧，`act(image=true)` 的图片绑定刷新可再加 1 帧（最多 6）；`start` 最多
  4 帧；`app` 的五个变更 kind 后观察最多 5 帧；`wait_for` 的有效帧来自等待采样与终帧，
  见 §14。请求不能改变这些上限。
- `unknown`：`samples<2`，或本次观察没有做收敛采样（`stop_reason=not_sampled`）。
- `unstable`：`samples>=2`，且最终发布帧与紧邻前一帧的最新比较不一致。
- `stable`：最终发布帧与紧邻前一帧形成相等对，样本数达到该阶段固定的最少样本门限，且
  `stop_reason=stable`；历史上有过相等对但最终帧不同不算 `stable`。
- `stop_reason` 说明采样为何结束：`stable` 收敛停止、`max_samples` 达到帧数上限、`budget`
  达到总预算、`unreadable` 某次采样读取失败并提前结束（失败不增加 `samples`）、
  `not_sampled` 未做收敛采样、`condition` 条件等待在满足条件的帧上结束、`timeout` 条件
  等待用尽自己的等待窗口。`condition` 与 `timeout` 只由 `wait_for` 产生，且与同一对象的
  `state` 保持一致（见 §14）。
- `quality` 是观察材料事实，不是输入授权：`unstable`/`unknown` 的 O 仍可发布、仍可被认领；
  它不改变 `status`、`actionable_until`、基线确认或 O 状态机。`complete+unstable` 与
  `partial+stable` 都是合法组合；没有树的失败观察不发布 `quality`，只保留错误与图片材料。

### Result 与 Effect

```json
{"operation_id":"P7","operation_state":"finished","outcome":"executed",
 "phase":"finished","effect":{"state":"observed"},"target_reference":"O3"}
```

`effect` 只有一个成员 `state`：`observed`/`not_observed`/`unknown`。对象在结果中的位置固定为
`phase` 之后、`target_reference` 之前。只有首次受理的 `scroll` 结果与仍保留该原操作的 P 视图
在 `outcome=executed`（含 `executed_revoked`）或 `uncertain` 时出现；`not_executed` 不出现。

| 值 | 含义 |
| --- | --- |
| `observed` | 后观察中至少两个内部匹配内容单元沿请求轴同时满足屏幕位移与容器相对位移且同号对一致，同号比例达到固定门限；不要求全覆盖、不要求观察质量 `stable`、不要求与请求方向同号。只表示观察到内容位移，不证明动作因果、方向、距离或滚动成功 |
| `not_observed` | 只在观察质量为 `stable`、至少 2 个匹配内容单元、前后内容单元计数相同且 100% 唯一匹配、容器四边与沿请求轴的中心位移及尺寸变化都不超过固定容差时成立；不证明到达边界或手势失败 |
| `unknown` | 无后观察树、后观察失败或撤销、容器不唯一或已失去滚动能力、正交边或容器身份变化、匹配不足、无法解释的尺寸变化，或一致位移不足；不表示“未移动” |

固定判据由服务策略界定，不是请求字段。派发前的 `pre` 必须是本次重验读到的帧，`post` 是后
观察帧；请求轴由 `direction` 决定。

- **容器守卫**：`pre`/`post` 必须唯一匹配同一容器内容键与祖先链，且两侧公开节点都仍是
  `scroll` 容器；纵向时左右边、横向时上下边的变化都不超过 1px，且两侧矩形有正面积交集。
  轴向两边允许变化，但只有四边都不变时才可能推出 `not_observed`。
- **匹配集**：受控日志中的 `anchors` 与 `coverage` 始终按完整唯一匹配集计数，不进入结果；
  匹配不足 2 个即 `unknown/insufficient_anchors`。
- **尺寸与轴向贴边**：宽高差都不超过 1px 才算尺寸稳定。正交轴尺寸变化，或轴向尺寸变化且
  前后都不贴该轴的视口边缘，直接 `unknown/size_changed`；轴向贴边且仅轴向尺寸变化者只从
  正证据中排除，不判 `size_changed`。轴向贴边的匹配对即使尺寸恰好相等也不计入正证据，
  裁剪端点被屏幕或容器改动过的单元同样排除。
- **正证据**：对每个计入正证据的匹配对同时要求屏幕轴向位移与容器相对轴向位移都达到 2px，
  按两者的符号组合计数；`len(eligible)>=2` 且最大同号对不少于 2 个、占比不低于 75% 时为
  `observed`。只满足一个参照的单元不进任何对，但仍留在分母中。
- **负证据**：未命中正证据后依次要求容器四边不变、全部匹配对尺寸稳定；再要求观察质量
  `stable`、前后单元计数相同且 100% 唯一匹配、至少两个单元、**全部**匹配对的屏幕轴向中心
  位移不超过 1px，才返回 `not_observed`。负证据使用完整匹配集与绝对中心，不会因为正证据
  候选不足而提前退出。

这些数值只界定状态含义，不承诺滚动距离、到达边界或业务成功。负证据只能证明这些可测矩形
未见位移，不能证明原始内容静止：一个在视口内始终铺满容器的超界单元，可见矩形不变时即使
真实内容移动也会得到 `not_observed`。

### Node

公共节点是交付表示，不是服务内部完整快照。每个逻辑节点保留原 `ref`、角色、内容、源能力与
三态事实；已知几何按固定的 compact 几何策略决定是否交付，连续匿名单子链可以折成一条
`chain` 定义。逻辑节点、父子关系与顺序不因交付表示改变。

```json
{"ref":"n7","role":"button","bounds":[40,300,240,380],
 "label":["确定"],"actions":["tap"],"enabled":false}
{"chain":["n2","n3"],"role":"view","children":[{"ref":"n4","role":"text","label":["设置"]}]}
```

公开构造按值过滤空叶：默认状态、空内容、无动作的普通 View/FrameLayout 叶不发布，
单独的 resource-id 或未知状态也不构成叶语义；非空内容、真实能力、非默认状态及 image 等固有
叶角色保留。原始采集事实不变。必要的目标屏祖先按最近公开父连接，其他显示器的包装层不发布，
但其目标屏后代仍可达。context 五个状态均为 unknown，不提供动作授权。

普通节点的字段全集：`ref`、`role`、`bounds`、`label`、`actions`、`enabled`、`focused`、
`selected`、`checked`、`value`、`hint`、`protected`、`axis`、`resource`、`context`、
`children`。`chain` 定义以 `chain` 取代 `ref`，携带成员的共享字段与末成员的孩子。

| 字段 | 编码规则 |
| --- | --- |
| `ref`/`chain` | 二选一。普通节点写自己的 `ref`；`chain` 是至少两个、按原顺序排列的连续 ref 数组，成员除 `ref`/`children` 外公共字段完全相同且几何都隐藏 |
| `role` | 总是出现；属于固定集合 `root`、`group`、`text`、`button`、`input`、`checkbox`、`radio`、`switch`、`toggle`、`scroll`、`image`、`view` |
| `bounds` | 三态：四整数数组表示已知矩形；`null` 表示源未提供可用几何；compact 视图里按几何策略省略的已知矩形直接省略该成员。detailed 视图不允许隐藏，`bounds` 总是出现 |
| `label`/`actions`/`children` | 空时省略；`actions` 取 `tap`、`long_press`、`text`、`swipe` |
| `enabled` | 默认 `true`；已知 `false` 时显式 `false`；未知时显式 `null` |
| `focused`/`selected`/`protected` | 默认 `false`；已知 `true` 时显式 `true`；未知时显式 `null` |
| `checked` | 完整节点默认 `false`：已知 `false` 省略，已知 `true` 显式 `true`，未知显式 `null` |
| `value`/`hint`/`resource` | 未知时省略；显式 `null` 也解码为未知；已知值显式给出（`""` 也保留） |
| `axis` | 缺席或 `null` 为未知；已知值只能是 `x`、`y`、`both`，空串非法 |
| `context` | 仅在 `true` 时出现，表示非可见的必要祖先（`role=group`、无动作/标签、几何未知） |

合成根固定为 `role=root`、目标屏矩形、`enabled=true`、其余四个布尔已知 `false`；
虚拟屏没有任何子节点时，合法的公开树就是“只有根、没有 `children`”。`actions` 是节点的源
能力词表，不随受控滚动增加枚举；`scroll` 由 `act.operation.type=scroll` 与 `container`
表达，`actions=swipe` 不构成 raw `swipe` 授权。

完整节点（full、add、replace、inspect）缺席的布尔恢复为表中已知默认，显式 `null` 保持
unknown；来源未知不能在构造时补成默认。五个状态的比较与差分严格区分 unknown/false/true。
`set` 的缺席表示不修改，不应用完整节点默认。树与 inspection 信封均不携带 `defaults`。

compact 几何策略是固定的服务策略，不是请求字段，按以下顺序判断一个节点的已知矩形：
源矩形缺失时保持 `null`；合成根与 `actions` 非空的节点保留已知矩形；匿名普通节点（角色为
`view`/`group`、无动作、无标签、无 `value`/`hint`/`resource`/`axis`、`enabled` 已知 `true`
且 `focused`/`selected`/`protected`/`checked` 都已知 `false`）和无动作的 `text` 省略已知
矩形；其余展示节点的矩形（裁剪到目标屏幕后）若与父链上最近一个实际交付的矩形四边完全
相同，或可见面积按合成根矩形面积计算严格小于 1%，也省略。角色名本身不构成保护：只有控件
角色而没有动作的节点仍按重复与面积规则判断。面积使用整数计算；合成根矩形缺失或不可计算时
跳过面积规则。只有匿名普通节点参与 `chain` 折叠，文本或图片即使省略坐标也保持独立节点。
detailed 视图始终交付完整原矩形。

### Tree 与 Delta

```json
{"format":"full","scope":"reported_visible_with_context","view":"compact",
 "root":{…}}
{"format":"delta","base":"O1","view":"compact",
 "set":{"n7":{"label":["已发送"],"actions":[]},"n8":{"bounds":"hidden"}},
 "add":[…],"remove":["n3"],"children":{"n1":["n2","n7"]},"replace":{"n4":{…}}}
```

- `scope` 固定为 `reported_visible_with_context`：目标屏上报告可见的节点及其必要祖先，
  其他显示器的节点不发布；
- `view` 固定为 `compact`（观察）或 `detailed`（`inspect`）；
- delta 的五个块都可省略；空差异只含 `format`、`base`、`view`；
- `set` 的每个成员只携带发生变化的字段，但值必须是**完整新值**，显式保留
  `false`/`0`/`""`/`[]`/`null`；省略表示“未修改”，`null` 表示“置为未知”，
  `bounds:"hidden"` 表示“该几何按策略不交付”；
- 五个块共同声明固定基线的唯一最终树，不能按出现顺序逐步修改基线；任何冲突都整体拒绝，
  不返回部分树，也不改写基线或 delta；
- `children` 给出父节点的最终完整子列表。存续节点可跨父移动：来源父与目标父的最终 claim
  必须一致，避免双父。新父收养存续节点时，新父及通向该节点的新增祖先作为空的顶层 Add
  组装根，其最终子列表由 `children` 给出；纯新增封闭分支可内联，嵌套定义不可被再次覆盖；
- `remove` 的根必须消失，其基线后代可通过最终图逃逸并继续存活；只删除最终不可达且属于
  删除/替换旧区域的残余。显式声明的 set 目标、children 父子与完整定义都必须从原根可达；
- `replace` 是封闭区域完整定义，区域根可由最终父列表迁移，内部存续后代保持基线父关系。
  `add`/`replace` 定义根使用普通 `ref`，只有后代可折成 `chain`；
- 最终图必须单父、无环且全部可达；根变化或同 ref 角色变化退 full。仅跨父移动、增删包装层
  或 chain 分组变化不强制 full；只有合法 delta 且 `10×deltaBytes < 7×fullBytes` 才发送 delta；
- 公共 full 超过 128 KiB、单个内部快照超过 4 MiB，或该快照连同受保护历史仍超过 32 MiB 的
  留存额度时，服务返回 `503 tree_too_large`，`details.budget` 分别取 `public_full`、
  `internal_snapshot`、`retained_snapshots`，`details.limit` 为对应字节上限；错误不携带
  树内容，也不先删除历史或回滚已接纳的 P/已认领的 O。
### Image

```json
"image": {"captured_at":"…","width":540,"height":1200,"media_type":"image/jpeg"}
"image": {"read_only":true,"captured_at":"…","width":540,"height":1200,
  "media_type":"image/jpeg","screen":{"width":1080,"height":2400,"rotation":0}}
"image": {"error":{"code":"image_capture_failed","message":"未能取得本次截图"}}
```

`captured_at` 是源图读取开始的服务 UTC 时间；`width`/`height` 是输出图片的实际尺寸；
`media_type` 固定 `image/jpeg`。`read_only` 仅在没有新 O 的只读图片与 `snap` 顶层图片上
出现，并附同屏 `screen`。失败图片只带受控 `error`，不带伪造的尺寸或时间。HTTP 载体
`observation.image.base64` 是本次编码图片的 base64；MCP 入口把它投影为 `ImageContent`，
文本与结构化内容不携带该成员。

`snap` 的顶层 `image` 使用同一元数据编码顺序，但固定带 `read_only=true` 与准入显示快照
`screen`，并携带 `base64` 载体；它是该次成功结果唯一的功能载荷，只交付一次。重复 P 与
`status(P)` 摘要、失败结果都不含顶层 `image`。

### Session / Cleanup

```json
"session": {"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",
  "started_at":"…","expires_at":"…"}
"session": {"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",
  "started_at":"…","ended_at":"…","reason":"user_stop"}
"cleanup": {"state":"complete","execution":"confirmed","tasks":"confirmed",
  "recents":"not_applicable","display":"confirmed"}
```

`cleanup.error` 只在存在未确认项时出现（`pending` 或 `unconfirmed` 都可能带），`code` 可能是
`execution_unconfirmed`、`display_create_unconfirmed`、`task_remove_unconfirmed`、
`recents_cleanup_unconfirmed`、`display_release_unconfirmed`、`cleanup_budget_exhausted`、
`cleanup_identity_conflict`、`cleanup_capacity_exhausted` 或 `cleanup_restart_stopped`。

`execution` 独立于原操作的 `outcome`。UI 子步骤只在调用仍未返回时保持 `unconfirmed`；
窗口返回时关闭，即使回执不可用而记为 `unknown`，也不再单独保持该值，原 P 仍为 `uncertain`。
启动调用已经结束但任务归属未确认时也不占执行屏障。受管 shell 则要精确链被证明结束或未启动
（`confirmed`/`aborted`）才能解除 `unconfirmed`；以 `unknown` 封闭不能据此写成 `confirmed`。
后观察、显示消失或另一 shell 完成都不能代替原链终结证据。
旧 `may_dispatch` UI/legacy checkpoint 迁移后保留为原退休会话的 `execution=unconfirmed`；
未确认事实不产生永久准入门禁，也不要求重启手机、清账本或清 App 数据。当前账本不能保存
新归属时仍返回 `ledger_unavailable`。

## 12. 发现、应用、只读、通知与受管执行工具

本节列出 `inspect`、`keepalive`、`diagnose`、`app`、`notify`、`shell` 的请求与结果。

### `POST /api/inspect`

```json
{"reference":"O2"}
{"reference":"O2","node":"n7"}
{"reference":"O2","query":"确定"}
```

`node` 与 `query` 互斥。`inspect` 只读保留中的历史树，不采集、不续租、不确认基线、
不认领 O，也不需要 O 仍可输入。`reference` 必须解析为保留中的历史 O；P 仍不可用，
旧 `observation_id` 按未知成员拒绝。

- 补看固定使用 `view=detailed`：逐节点展开、不折 `chain`、不隐藏已知几何；`tree` 作用域的
  `view` 在嵌套完整树里声明，`subtree`/`query` 在 inspection 信封声明一次；
- 整树补看按 detailed 公共编码计量，受 128 KiB 上限；`node` 子树的 `{view,root}`
  内容对象与 `query` 的 `{view,matches,total,truncated}` 内容对象各受 32 KiB 上限；
- `query` 的每个命中独立按 `total=<预算前命中数>`、`truncated=<是否多于一个>` 计量；任一命中
  超过 32 KiB 时整次查询以 `409 inspection_too_large` 失败（即使它不在返回前缀内）；
  总量预算只决定完整命中的返回前缀，共享的 `view` 只计一次，前缀不足全部命中时
  置 `truncated=true`；
- `query` 在 `label`/`value`/`hint`/`resource` 上做字面 Unicode 折叠子串匹配，按原树顺序
  返回完整命中子树和祖先 `ref` 路径；`total` 为预算前命中数，返回值只含完整命中子树；
- 常见失败：`409 observation_not_found`（不存在或不属于本 Agent）、
  `409 content_unavailable`（树已被淘汰）、`409 node_not_found`、
  `409 inspection_too_large`（`details.limit` 为字节上限）。这些失败不携带 `recovery`。
  compact 观察树能交付不保证 detailed 整树也能交付；超额时不静默改用 compact 返回。

### `POST /api/keepalive`

```json
{"reference":"O1","duration_minutes":30}
```

`reference` 必填，沿用 O/P 归属解析；`duration_minutes` 可选，必须是 1～120 的 JSON
整数，省略等于 2。显式 `null`、字符串、布尔、非整数及越界都按 `422 invalid_request`
拒绝，不截断、不归一化为默认值，也不登记本次 P。

```json
{"operation_id":"Pkeep1","outcome":"executed","session":{
  "origin":"Pstart","state":"active","mode":"virtual","agent":"local-agent",
  "started_at":"…","expires_at":"…"}}
```

| 项 | 契约表述 |
| --- | --- |
| 作用 | engine 在同一状态锁内取应用时 `now`，校验原会话仍为当前 `active` 且 `now < expires_at`，然后覆盖 `expires_at = now + duration_minutes`。每个新的 P 都从本次应用时重新计时，可延长也可缩短；省略参数同样覆盖为两分钟。 |
| 起算与上限 | 1～120 分钟只约束单次显式请求；会话没有累计时长硬上限。先前显式设置的长租期不限制后续缩短，缩短后的租期也不限制普通活动再次延长。 |
| start 与其他普通活动 | `start` 初始化、激活与首观察成功发布使用五分钟；其他普通工作只在各自既有续期点取 `max(expires_at, now + 2分钟)`，不继承显式时长，也不截短更长租期。显式两分钟的默认调用可以缩短 start 的五分钟启动租期；普通活动随后仍可按最大值把它延长到 `now + 2分钟`。 |
| 不作用 | 不延长任何 O 的 `actionable_until`，不改 O 状态，不确认基线，不占普通工作位，不访问设备，不写账本正文，不延长工作或 shell 期限。 |
| 拒绝条件 | 会话 `ended` → `session_ended`；`starting` → `reference_has_no_session`；未撤权但已到/越过 `expires_at` → `session_expired`；`now == expires_at` 即视为到期。失败不修改截止时间。引用未知、外来或不能定位按既有错误族拒绝。 |
| P 与重放 | 同一可识别 P 只返回原操作视图或封闭事实，不再次重设截止时间；重放视图中的 `session` 可以反映当前租期或 ended 状态。改变时长必须使用新 P。P 记录与编号沿用现有有界保留规则，不承诺无限历史。 |
| 并发 | 更新按 engine 锁内的实际应用顺序生效；显式覆盖与普通 `max` 续期互不继承。STOP/end 先撤权时后续续租不能恢复；续租先成功也不阻止后续 STOP/end。 |
| 保证 | 成功响应返回当次锁内更新后的 `session.expires_at`。截止时间是应用时快照，不承诺任务完成或会话存活至该时刻；后续显式调用、普通活动、STOP/end、App/服务生命周期与 MCP 取消仍可改变事实。续租结果漏收不回滚已应用更新；调用方按 P 查询或创建新 P。 |
| 边界 | 到期与结束的会话不能复活；keepalive 不保住任何 O，也不延长 O 的输入期限。 |

成功响应沿用现有 `Result`/`session` 形状，不新增时长回显或剩余分钟字段；绝对期限以
`session.expires_at` 为准。首次成功不保证顶层 `phase`/`operation_state` 出现；完成的操作
在内部记为 `phase=renewed`，查询/重放已完成 P 时可出现 `operation_state=finished` 与
`phase=renewed`。

### `POST /api/diagnose`

```json
{}
{"reference":"O2"}
```

无参数查看自身近期有界事实；指定 O/P 时只查原归属。输出只有
`diagnosis.summary`、`issues`（`phase`/`code`）、白名单 `timing_ms`、必要时短 `timeline`、
`coverage` 与 `truncated`，不返回树、日志、输入、命令或输出，也不修复任何状态。
未知或他人引用是正常查询事实：`coverage=record_unavailable`，不返回顶层错误。
指定 P 且原操作仍可解析时，`diagnosis` 另带 `operation_id` 与已知 `outcome`。

### `POST /api/app`

`app` 是统一的应用生命周期入口。根对象必须带 `kind`，七个取值大小写敏感：
`find`、`list`、`open`、`claim`、`handoff`、`close`、`kill`。每个 kind 都是互斥分支，只接受
下表列出的成员；请求体上限 8 KiB。`Result` 只使用 `app` 成员承载本工具的查询或变更结果。

| kind | 必填（除 kind） | 可选与默认 | 不接受 |
| --- | --- | --- | --- |
| `find` | 无 | `query`：省略为全部；≤256 个 Unicode 码点；按本地化名称或包名做字面 Unicode 折叠子串匹配 | `reference`、`target`、`app`、`scope` |
| `list` | `reference`：当前 ready O，或能解析到活动原会话的 P | `scope=session`（默认）或 `primary`；`app`：明确包名过滤 | `target`、`query` |
| `open` | `reference`：当前 ready O；`target={app:包名}` | 无 | 顶层 `app`、`task_id` target、`scope`、`query` |
| `claim` | `reference`：当前 ready O；`target` | 无 | `scope`、`app`、`query` |
| `handoff` | `reference`：当前 ready O；`target` | `restore_foreground=false` | `scope`、`app`、`query` |
| `close` | `reference`：当前 ready O；`target` | `allow_borrowed=false` | `scope`、`app`、`query` |
| `kill` | `reference`：当前 ready O；`target={task_id}` | `allow_stop_playback=false` | 包名 `target`、`scope`、`app`、`query` |

`target` 为 `{"task_id": 正整数}` 或 `{"app": 包名}`，恰好一种；`task_id` 与 `app` 互斥，
两者都不出现、`task_id` 不是正整数或 `app` 不是明确包名都按 `422 invalid_request` 拒绝。
`open` 只接受 `target.app`；`kill` 只接受 `target.task_id`，包名先由 `list` 解析。
`claim`/`handoff`/`close` 接受 `task_id` 或 `app` 恰好一种。`target` 是选择器，不单独授予权限。
顶层 `app` 只保留给 `list` 的包名过滤。旧 `observation_id` 在所有 app kind 中按未知成员拒绝。

`find`/`list` 是查询：不消耗当前 ready O、不续租、不改变阅读基线，成功与失败都不带
`outcome`。`open`/`claim`/`handoff`/`close`/`kill` 消费当前 ready O；成功后返回原会话屏的一次
full 无图观察。主屏会话只允许 `find`/`list`/`open`；`claim`/`handoff`/`close`/`kill` 在消费 O
之前以 `409 window_action_not_supported_on_primary_display` 拒绝。`list` 的 `reference` 必须
是当前 ready O，或是指向本 Agent 活动原会话的 P；`scope=primary` 在主屏会话与 `session`
指向同一主屏，不形成第二种授权。`find` 无会话也可调用；新鲜可信的应用清单缓存可直接返回，否则读取占查询工作
位置，另一项工作占用时返回 `409 device_busy`（同一 Agent 带 `blocking_operation_id`）。
`find`/`list` 各按 32 KiB 完整条目前缀返回，`total` 为预算前数量，`truncated` 表示只返回了
完整前缀；无法完整解析任务清单时不把截断当作读取成功。

`app` 结果对象以 `kind` 判别，只带本次已经证明的成员：

- `find`：`items`（每项 `name`/`package`）、`total`（预算前匹配数）、`truncated`、
  `captured_at`；`items` 是预算内的完整条目前缀。
- `list`：`display_id`（会话虚屏或主屏 `0`）、`items`（每项 `task_id`/`package`/`ownership`，
  有原归属记录时带 `origin_display`）、`total`、`truncated`、`captured_at`、`foreground`
  （沿用 `present`/`none`/`unknown`）。任务可读但前台未知时仍返回集合与 `foreground.unknown`。
- `open`：`package`、`task_id`、`display_id`、`ownership`、`reused`、`state_preserved`。
- `claim`：`package`、`task_id`、`source_display_id`、`display_id`、`ownership=borrowed`、
  `state_preserved`。
- `handoff`：`package`、`task_id`、`source_display_id`、`display_id`、`ownership=shared`、
  `state_preserved`、`foreground_restore`（`not_requested`/`confirmed`/`unconfirmed`）。
- `close`：`display_id`、`tasks`（每项 `task_id`、`outcome`，适用时 `recents_cleaned`，
  项级拒绝或未确认可带 `error`）、`complete`。
- `kill`：`package`、`source_task_id`、`display_id`、`target_pids`、`killed_pids`；后验清单
  可读时 `remaining_pids`；`restarted` 为布尔或 `null`，表示后验出现了原集合外的新实例；
  三个 PID 数组按数值排序。

`state_preserved` 与 `restarted` 是声明 nullable 的标量：状态未知或比较证据不足时保留显式
`null`，不能填写 `false`；已知没有变化为 `true`，已知变化为 `false`。`open` 新建时
`state_preserved=false`；复用或 `claim`/`handoff` 的同一 task 与顶部 Activity 可比较且未变时
为 `true`，已知变化为 `false`，证据不足为 `null`。`open` 的成功结果必须出现 `reused` 与
`state_preserved`（后者值可为 `null`）；`claim`/`handoff` 的成功结果必须出现
`state_preserved`（值可为 `null`）；`kill` 的成功结果
必须出现 `restarted`（值可为 `null`）。
`state_preserved` 是尽力延续证据，不保证像素、滚动、网络数据、输入法或未保存表单不变。
`close.tasks[*].recents_cleaned` 适用但未确认时为 `null`，不适用时省略。读取失败的集合不
伪造 `items: []` 或 `total: 0`：首次读取失败时省略 `items`/`total`/`truncated`；`list` 的
任务清单读取失败时连同 `foreground` 一并省略，只有任务集合完整而前台独立失败才返回
`foreground.unknown`。`close` 的整体前置拒绝只保留已知 `display_id` 并省略 `tasks`/`complete`；
`kill` 的前置拒绝不伪造 `killed_pids`，只有进入执行阶段才出现该数组。其他未知标量与不适用
字段省略而不是填 `0`；`display_id=0` 是合法的主屏值。

`open` 在虚拟会话中优先复用目标屏上该包的 live 窗口（前台 task 命中优先，否则 task ID
最小），否则在目标屏新建并登记为 `owned`；主屏打开只按同屏应用处理，返回
`ownership=shared`，不登记用户任务。`claim` 把主屏 `0` 上的单个精确 live 任务移入本会话
虚屏并登记 `borrowed`，`source_display_id=0`。`handoff` 把本会话虚屏的单个任务移回主屏
`0`，登记 `shared`；默认不
恢复前台，`restore_foreground=true` 时只做一次有界尽力恢复。虚拟屏 `open`、`claim`、
`handoff` 与 `kill` 拒绝控制 App 包，控制界面不能作为虚拟屏任务目标。

`close` 在消费 O 前冻结最多 16 个本会话管理的 live 目标；零个 live 目标以 `task_not_found`
拒绝，超过 16 个以 `app_capacity_exceeded` 整体拒绝而不截断。`allow_borrowed=false` 时目标中
包含借入窗口即整体拒绝。每项独立给出 `outcome` 与 `recents_cleaned`；`complete=true` 当且仅当
全部项 `executed` 且 `recents_cleaned=true`，仅 recents 未确认不把顶层 outcome 改成失败。

`kill` 只接受本会话 live `owned` 任务，且该包在本屏没有其他窗口、在其他显示屏没有 live
task；应用在本屏前台、前台未知、前台服务或播放状态未知或正在播放（未显式
`allow_stop_playback=true`）、或 root 不可用时拒绝。派发前按 boot、PID、start ticks 复核原
进程实例，对每个原实例发送一次精确 `kill -9`；只读后验给出已结束、仍存在与新实例事实，
不 force-stop、不追杀重启后的新进程。原目标进程集合已确认空时，`target_pids=[]`、
`killed_pids=[]`、`remaining_pids=[]`、`restarted=false`，无需发信号。

`open` 的常见失败包括 `app_not_found`、`app_launch_failed`、`app_launch_unconfirmed`、
`application_moved`、`task_focus_unavailable`；其中 `app_not_found` 的 `recovery` 为
`entry=user`、`reference=none`、`input_state=effective`。`claim`/`handoff`/`close`/`kill` 的
目标选择失败按 `task_not_found`、`task_not_tracked`、`task_target_ambiguous`、
`close_refused_borrowed` 或对应的 `kill_refused_*` 返回。所有拒绝都保留已证明的身份与
作用屏，不伪造已派发事实。

每次成功变更后服务发布原会话屏的一次 full 无图观察；后观察失败保留主 outcome 与已证明字段，
`observation.status=failed` 与 `error` 表达采集失败。STOP 撤权后的迟到结果不发布新观察。

`app` 的七种 kind 在 MCP 适配器都预分配 P。HTTP 每次新调用同样分配自己的 P；`reference`
只用于定位既有会话，不是本次调用的 P。`find` 使用 detached P，不因调用时恰有全局活动会话
而绑定；`list` 的 P 绑定 `reference` 解析到的原会话。查询成功与失败都不带 `outcome`，
`status(P)`/重复 P 只返回操作摘要（`app.kind`）而不重新读取或补回集合；查询取消的摘要为
`operation_state=finished`、`phase=query_cancelled`、无 outcome/error/集合，只表示该查询
已关闭，需要集合的调用方以新 P 重做 `find`/`list`。

`app` 业务错误的 HTTP 状态：`window_action_not_supported_on_primary_display`、
`task_not_found`、`task_not_tracked`、`task_target_ambiguous`（`details.task_ids`）、
`task_root_shared`、`task_move_unconfirmed`、`close_refused_borrowed`、`kill_refused_not_owned`、
`kill_refused_task_elsewhere`、`kill_refused_foreground`、`kill_refused_playback`、
`kill_refused_process_shared`、`kill_unconfirmed`、`app_capacity_exceeded`（`details.limit`）与
`task_lifecycle_busy` 均为 `409`；请求形状错误为 `422 invalid_request`；应用清单、任务或进程
读取失败为 `503`（`app_list_unavailable`/`device_unavailable`）；`open` 的启动未确认为
`504 app_launch_unconfirmed`。

### `POST /api/notify`

```json
{"text":"消息正文"}
```

无需会话；`text` 非空且不超过 4096 个 Unicode 码点。成功返回 `outcome=executed`、
`message_id`、`saved=true` 与 `notification.state`。
`executed` 只表示消息已保存并进入控制同步队列，不表示已显示或已读。
`notification.state` 固定为 `pending`，不带 `error`；消息正文通过控制同步的 `messages`
投递给 App，App 本地标记已读且不回报。正文不进日志、错误或账本。

### `POST /api/shell`

```json
{"reference":"O1","command":"id -u","root":false}
```

`reference` 为 O/P，只定位原活动会话；可以是该会话已消费/过期/退役的 O，或首次树失败的启动 P。
`command` 非空、≤4096 个 Unicode 码点且不含 NUL；`root=false` 使用设备 shell 身份，
`root=true` 明确请求 root，不自动降级/提权重试。命令在 Android shell 环境执行，不进入 chroot。

派发前服务只置执行未知边界（命令可能已经发出，恢复与收尾按此保守处理）；当前 O 的输入授权
与已确认基线都不改变，旧树可继续 `inspect`。受管链确认结束后不采集、不发布观察：同一个 O
仍是 `ready`，可继续用于 `act` 或 `observe`。命令可能改变界面，屏幕是否仍与 O 一致由下一次
`act` 的实时重验回答；需要新树时用 `observe`（基线仍在，按 §5 的 delta 规则）。撤销
（STOP/`end`）仍按会话撤销退休 O。

```json
{"operation_id":"P12","outcome":"executed",
 "shell":{"termination":"exited","exit_code":0,"output":"…","truncated":false}}
```

`shell` 字段总是包含 `termination`、`exit_code`、`output`、`truncated`：

| 字段 | 规则 |
| --- | --- |
| `termination` | `not_started`/`exited`/`timeout`/`cancelled`/`unknown` |
| `exit_code` | 可靠自然退出时为实际整数；没有时为 `null`，不伪造 124 |
| `output` | 已取得的有界前缀；`""` 表示已知空输出，`null` 表示未取得 |
| `truncated` | 已知是否超过 64 KiB 时为布尔值；字节计数未知时 `null` |
| `output_error` | 仅输出取得失败时出现，`shell_output_unavailable` |

结果分层：自然退出（含非零与命令自行 `exit 124`）为 `executed`/`exited`；监管超时且已确认
进程组停止为 `executed`/`timeout`；STOP/end 后已确认停止为 `cancelled`（若信号前已有自然退出码
则保留 `exited`/实际码）；已派发但开始/结束无法确认为 `uncertain`/`unknown`；确认未启动为
`not_executed`/`not_started`。超时/取消后无法确认终止时，以 `uncertain`/`unknown` 封闭该链，
释放工作位置；没有停止证据不包装成 `timeout` 或 `cancelled`。

常见失败：`409 root_unavailable`、`503 shell_start_failed`、`504 shell_result_unconfirmed`、
`409 session_ended`/`reference_has_no_session`/`operation_unknown`、`409 device_busy`。
已结束但仅输出缺失时响应仍为 `executed`，错误在嵌套 `shell.output_error`；成功结果不含
`observation`，也不带 `recovery`。撤销后确认停止的结果 `recovery` 为 `entry=status`、
`reference=P`、`input_state=revoked`、`cause=executed_revoked` 并带会话视图。`shell` 不产生
`retry`。未确认的执行链只在 `run.DeadlineAt + ShellConfirmTimeout + 3s` 内关闭普通准入；
到界后以 `termination=unknown` 结案让位，普通工具照常接纳，收尾链至多再作一次精确终止尝试，
未证明进程消失前 `cleanup.execution` 保持 `unconfirmed`。退休后按有限交接处理，`status`
只查询事实，不推动补偿。命令正文、输出与异常文本不进入日志或账本。

## 13. 端点与能力边界

| 端点/能力 | 契约行为 |
| --- | --- |
| `start`、`status`、`observe`、`end` | 见 §3、§4、§5、§7 |
| `snap` | 定义于 §5.1；活动会话内立即读取一张图，不读取树、不发布观察 |
| `wait_for` | 定义于 §14；条件等待与稳定门禁，发布一个新观察 |
| `act`：`tap`、`long_press`、`swipe`、`scroll`、`key`、`text` | 定义于 §6；其他动作类型不被接受 |
| `observe`/`act` 的 `image=true` | 见 §5 图片；图片是本次观察的附加材料，不是单独工具 |
| 截图、图片块 | 观察图片在 `observation.image`，`snap` 顶层图片在 `image`；两者共用同一 `ImageMeta` 形状与 `base64` 载体，MCP `ImageContent` 承载图片字节 |
| `app`、`keepalive`、`inspect`、`notify`、`diagnose` | 定义于 §12 |
| `shell` | 定义于 §12 |
| MCP stdio / Streamable HTTP、Go client | 由独立入口提供，见 [MCP 入口](mcp.md) 与 [Go 客户端](client.md)；不改变本页 `/api/*` 契约 |
| `/v1/control/sync`、`/v1/control/stop`、`create_display`/`close_display`/`cancel_create` | 定义于 [控制端协议](control-protocol.md) |
| `selected_mode=primary` | 使用主屏（display 0），不创建虚拟屏 |

未注册的 `/api/<tool>` 路径由 Go `ServeMux` 返回默认的纯文本 `404 page not found`，不是本页
的 JSON 错误形状；客户端应先按工具目录判断能力。已定义端点中，`GET /health` 是唯一不需要
认证的端点。

## 14. `POST /api/wait_for`

`wait_for` 在当前会话的普通工作流上等待一个字面条件或整树稳定，退出后在同一工作位内做一次
验证采集并发布一个新观察。请求体上限 16 KiB：

```json
{"reference":"O1","query":"确定","timeout":15}
{"reference":"O1","absent_query":"加载中","timeout":15}
{"reference":"O1","stable":true,"timeout":5,"full":true,"image":true}
```

| 字段 | 必填 | 类型/范围 | 说明 |
| --- | --- | --- | --- |
| `reference` | 是 | `O…`/`P…`，≤64 | 原观察或原操作，解析口径与 §5 的 `observe` 相同；引用 `P` 时本次发布强制 full，与 `observe(P)` 一致 |
| `timeout` | 是 | 整数 1–40（秒） | 本次等待窗口；`stable=true` 时至少 2 |
| `query` | 否 | 字符串，≤256 个 Unicode 码点，不得为空或全空白 | 公开树中存在至少一个节点在 `label`/`value`/`hint`/`resource` 上包含该文本 |
| `absent_query` | 否 | 同上 | 帧已具备最小可读证据，且整棵公开树没有任何节点命中该文本 |
| `stable` | 否 | 布尔，默认 `false` | 要求整树连续 3 帧一致；有查询时是条件满足后的门禁，无查询时是独立触发分支 |
| `full` | 否 | 布尔，默认 `false` | `true` 强制本次发布的观察为 full |
| `image` | 否 | 布尔，默认 `false` | `true` 在同一次验证采集中附加一张图片；`matched` 与 `terminal_reuse` 复用已选帧、不重采树，`terminal_capture` 读取一次终帧 |

接入拒绝项（一律 `422 invalid_request`，不接触设备）：未知成员、重复成员、大小写变体、
尾随第二个 JSON 值、类型混用、显式 `null`；`reference` 或 `timeout` 缺失；`timeout` 不是
1–40 的整数；`stable=true` 且 `timeout<2`；`query`/`absent_query` 超过 256 个码点或为空、
全空白；两者同时出现且字面相同。折叠后相同的两个条件由引擎在准入时以同样的
`422 invalid_request` 拒绝。引用不可解析、不属于本 Agent 或指向已结束会话时，按 §5 的同一
错误族拒绝（`observation_not_found`、`operation_unknown`、`request_closed`、`session_ended`、
`reference_has_no_session` 等）。

### 条件与稳定判据

- `query` 是字面 Unicode 大小写折叠子串匹配；`absent_query` 要求该帧先通过最小可读证据
  门槛——除合成根外至少 1 个非 `context` 内容节点——且整棵树没有任何节点命中。
- 检索范围严格等于服务公布的 `reported_visible_with_context` 公开树：视口外或被隐私策略
  抑制的内容永不命中，“未命中”不等于业务界面没有变化。
- `stable=true` 按请求是否携带查询条件确定语义：无查询时它是独立触发分支（整树连续 3 帧摘要一致）；有查询
  时它是门禁（条件与连续 3 帧一致同时成立）。判据是**连续相等帧数 == 3**，不是累计采样数；
  采样最小间隔 600ms，因此 3 帧至少跨越 1.2s。
- 退出分支固定为 `query`、`absent_query`、连续 3 帧稳定、`timeout` 到期、采样上限守卫
  （80 个有效帧）。

### 结果

```json
{"operation_id":"P8",
 "observation":{"status":"complete","id":"O2","origin":"P1","revision":2,
   "captured_at":"…","actionable_until":"…",
   "quality":{"state":"stable","samples":3,"stop_reason":"stable"},
   "screen":{"width":1080,"height":2400,"rotation":0},
   "tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",
     "root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}},
 "wait":{"trigger":"query","matched":["query"],"elapsed_ms":4210}}
```

`wait` 对象在 `Result` 中固定位于 `shell` 之后、`notification` 之前（见 §2），成员顺序为
`trigger`、`matched`、`elapsed_ms`：

| 成员 | 规则 |
| --- | --- |
| `trigger` | 在进入验证采集前冻结的退出分支，取值 `query`/`absent_query`/`stable`/`timeout`；命中时优先级为 `query > absent_query`，到时与采样上限守卫都记 `timeout` |
| `matched` | 只在最终发布的那一帧上评估的单帧可判定项，固定顺序 `["query","absent_query","timeout"]`，`timeout` 仅在 `trigger=timeout` 时包含；空列表时整个成员省略，绝不写 `null`；`stable` 不进入该列表 |
| `elapsed_ms` | 自准入完成到终帧发布的耗时（毫秒），含验证采集 |

`observation.quality` 永远描述最终发布的那一帧：

- 终帧来源有三种：`matched`、`terminal_reuse`、`terminal_capture`。命中条件或稳态退出使用
  命中的候选帧（`matched`），不重采树：连续 3 帧一致为 `state=stable`、
  `stop_reason=stable`；否则 `samples>=2` 为 `unstable`、`<2` 为 `unknown`，两种情况的
  `stop_reason` 都是 `condition`。
- 到时或触达采样上限时，若最后一次轮询尝试成功、选择时该帧在 `CaptureMaxAge=15s` 内、且
  之后没有失败尝试，复用该帧（`terminal_reuse`），不新增采样：连续 3 帧一致为
  `stable`/`stable`；否则 `samples>=2` 为 `unstable`、`<2` 为 `unknown`，`stop_reason` 为
  冻结的 `timeout` 或 `max_samples`。之后发生的失败或选择时已过期都使该帧不参与复用。
- 不能复用时执行一次终帧采集（`terminal_capture`）：等待期没有任何有效帧（纯等待或全部采集
  失败）为 `unknown`/`samples=0`/`not_sampled`；有有效历史帧时把终帧并入同一比较，连续 3 帧
  一致为 `stable`/`stable`，否则 `samples>=2` 为 `unstable`、`<2` 为 `unknown`，
  `stop_reason` 为 `timeout` 或 `max_samples`。
- 复用帧的 `captured_at` 是该帧实际采集时间；`elapsed_ms` 仍计自准入完成到发布。
- 复用候选帧且 `image=true` 时，截图与候选树的跨度超过固定图片预算则按 §5 的
  `image_stale` 规则丢图：观察为 `partial`、`missing=["image"]`，树与 `wait` 不变。

`stop_reason` 的完整枚举因此是 `stable`、`max_samples`、`budget`、`unreadable`、
`not_sampled`、`condition`、`timeout`（见 §11）。

### 准入、期限与撤销

- 准入在单一状态锁内完成：解析引用、拦截已过期会话（`409 session_expired`，绝不复活）、
  普通屏障与工作位、工作记录、基线确认与 `SessionTTL=120s` 单调续租。已过期会话不被本次
  调用续租，也不被推进成 `control_revoked`。
- 等待期独占唯一普通工作位，其他普通工具立即返回 `409 device_busy`，不建普通动作队列。
- 与 `observe` 相同，服务在进入引擎前先为本次结果预留投递容量：无图 256 KiB、带图 1 MiB，
  并占用一个投递槽（同时最多 2 个）；预留失败返回 `503 delivery_unavailable`，不接触设备。
  一次长等待在整个调用期间持有该槽，直到响应写完。
- 工作上下文继承调用方的 HTTP 请求上下文：STOP 与 `end` 立即释放工作位，等待返回
  `409 control_revoked`，不发布迟到观察；MCP 取消与客户端断连同样立即关闭等待并释放工作位，
  但调用方按适配器或传输层的取消结果收尾，不保证送达该 409。
- 验证采集时目标显示不可用返回 `503 display_unavailable`；候选帧（含复用的终帧）在提交门禁
  被拒绝时沿用既有错误（`stale_screen`、`control_revoked`、`tree_too_large` 等），不发布
  新 O，也不再发起第二次终帧读取。
- 验证采集失败时结果仍带 `wait` 的 `trigger` 与 `elapsed_ms`，省略 `matched`；观察形状为
  `observation.status=failed`（`image=true` 且截图成功、核心树失败时为只读图的 `partial`，
  两者都没有观察 id），失败原因在 `observation.error`，并随结果携带 `recovery`。这样的结果
  在 HTTP 上是 `503`：顶层 `error` 只用于准入期与等待期的拒绝。
- `wait_for` 不认领任何 `O`；发布新 `O` 时把上一个 `ready` `O` 置为 `retired`。
- 时间预算：工作位期限为 `timeout + 40s`（41–80s）。验证采集整体受 35 秒的固定预留截止
  约束：单步上限（显示探测 5s、截图 10s、截图目标复核 5s、层级读取下限 15s）恰好用满该
  预留，任一步在剩余时间内没有完成，整次采集就按失败结果返回，因此最坏
  `timeout + 35s = 75s` 严格小于 80s 的工作位期限，也严格小于 90s 的
  `httpapi.WriteTimeout` 与 `client.defaultTimeout`。全局 `WriteTimeout`、客户端默认超时、
  `SessionTTL` 与 `ObservationLifetime` 都不随本工具改变。
