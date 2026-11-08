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
| 普通契约 | `/api/*` 必须带 `X-Mobile-Use-Contract: 2`，精确相等；缺失或不匹配返回 `409 contract_mismatch` |
| 预分配 P | `/api/*` 可带 `X-Mobile-Use-Operation: P…`；缺省由服务分配。`status` 不使用该头 |
| 请求体 | 必须是一个 JSON 对象；大小上限：`start`/`status`/`end` 16 KiB，`observe` 32 KiB，`act` 64 KiB，`inspect`/`keepalive`/`diagnose`/`apps`/`open_app` 8 KiB，`notify` 16 KiB，`shell` 32 KiB，`sync` 32 KiB，`stop` 8 KiB |
| 响应 | `Content-Type: application/json; charset=utf-8`，`Cache-Control: no-store` |
| 健康检查 | `GET /health` 无认证，见 §10 |

严格 JSON（`/api/*` 与 `/v1/control/sync|stop` 一致，来自 `encoding/json/v2` 与显式检查）：

- 未知成员、重复成员名、大小写变体、尾随第二个 JSON 值、类型混用 → `422 invalid_request`；
- 必填字段必须实际出现；未声明 nullable 的字段出现显式 `null` → `422 invalid_request`
  （`status` 的 `reference`、`observe` 的 `full`/`image`/`wait_ms`、`act` 的 `image`、
  `text`/`key` 的值等）；
- 请求体为空、只有空白或不是对象 → `422 invalid_request`；
- 编号规则：`O…` 为观察，`P…` 为操作；长度 1–64，首字符必须是 `O` 或 `P`。

## 2. 结果分层与错误

工具响应是一个 `Result` 对象，字段只在本次调用确有该事实时出现，固定顺序为：

```text
operation_id, operation_state, outcome, phase, target_reference, request_closed,
blocking_operation_id, can_start, blocking_code, start_operation_id,
observation_state, input_state, message_id, saved, session, observation, inspection,
apps, app, shell, notification, diagnosis, cleanup, error, next
```

四层事实互不替代：`outcome`（动作执行）、`observation.status`（观察）、`session.state`
（会话）、`cleanup.*`（物理收尾）。`error` 只在失败时出现：

```json
{"operation_id":"P2","outcome":"not_executed",
 "error":{"code":"session_ended","message":"原会话已结束"},"next":"end"}
```

- `code` 稳定，`message` 是受控中文短句，调用方只依赖 `code`；
- `details` 仅可能出现白名单事实（如 `limit`、`limit_ms`、`app_code`）；
- `next` 是恢复入口：`status`、`observe`、`end`、`user`、`update` 或省略。

HTTP 状态映射（`protocol.Status`）：

| 状态 | 触发 |
| --- | --- |
| 200 | 成功；含 `end`/`start` 的 `cleanup=pending`（收尾未完成不是 HTTP 错误） |
| 401 | `unauthorized` |
| 409 | `contract_mismatch`、`device_busy`、`session_*`、`reference_*`、`operation_*`、`observation_*`、`node_not_found`、`node_disabled`、`stale_screen`、`target_changed`、`foreground_changed`、`foreground_unknown`、`control_revoked`、`request_closed`、`cleanup_unconfirmed`、`control_unavailable`、`stop_unavailable`、`mode_not_selected`、`device_locked`、`device_not_interactive`、`recovering`、`no_ready_observation`、`display_unavailable`、`text_target_uncertain`、`text_result_uncertain`、`control_ui_on_virtual_display`、`app_not_found`、`application_moved`、`task_focus_unavailable`、`task_remove_unconfirmed`、`content_unavailable`、`inspection_too_large`、`root_unavailable` |
| 422 | `invalid_request`、`invalid_coordinates`、`unsupported_key`、`unsupported_node_action`、`text_too_long`、`reference_invalid` |
| 503 | `device_unavailable`、`display_create_failed`、`hierarchy_unavailable`、`tree_too_large`、`app_launch_failed`、`app_list_unavailable`、`shell_start_failed`、`delivery_unavailable`；以及 `observation.status=failed` 或没有新 O 的 `partial` 结果 |
| 504 | `action_result_uncertain`（输入已派发但无法确认完成）、`app_launch_unconfirmed`、`shell_result_unconfirmed` |
| 500 | `internal_error`（接入层自身失败）；工具结果中的 500 会被改写为 409 |

接入层错误（认证、方法、契约、JSON）直接返回 `{"error":{…}}`，按需附带 `next`，不带
`operation_id`。工具结果中的 `error` 若映射为 500，HTTP 层改写为 409；接入层自身的 500
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
    "screen":{"width":1080,"height":2400,"rotation":0},
    "foreground":{"state":"present","package":"com.example.probe","task_id":12},
    "tree":{"format":"full","scope":"reported_visible_with_context",
      "root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}
}
```

- `start` 的首个观察固定为 full（`forceFull=true`），即使已有基线。
- 会话已建立但首个采集失败：HTTP 503，`outcome=executed`，`observation.status=failed`、
  `missing=["tree"]`，`next="observe"`；会话仍存在，调用方应改用 `observe(P…)` 恢复。
- 首个采集因 `control_ui_on_virtual_display`、`display_unavailable`、`stale_screen` 失败时，
  会话被撤销并启动收尾，响应同时带 `session`（`ended`）与 `cleanup`。
- 接纳失败：`409/503`，`outcome=not_executed`，`error` 为
  `session_exists`、`device_busy`、`control_unavailable`、`stop_unavailable`、
  `mode_not_selected`、`device_locked`、`device_not_interactive`、`control_revoked`、
  `cleanup_unconfirmed`、`display_unavailable` 等；也可能是内部阻塞原因串（如
  `cleanup_pending`、`recovery_pending`、`control_sync_pending`、`ledger_unavailable`），
  `next` 视错误而定。
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
{"can_start":true}
{"can_start":false,"blocking_code":"cleanup_unconfirmed",
 "session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",
   "started_at":"…","ended_at":"…","reason":"client_end"},
 "cleanup":{"state":"unconfirmed","execution":"confirmed","tasks":"confirmed",
   "recents":"not_applicable","display":"unconfirmed"}}
```

- `can_start` 必出；`false` 时必有 `blocking_code`（可能是稳定错误码，也可能是内部原因串：
  `recovery_pending`、`old_resource_unconfirmed`、`control_sync_pending`、
  `ledger_unavailable`、`ledger_corrupt`、`no_display_recorded`、`cleanup_pending`）；
- 会话属于本 Agent 时带 `session` 与 `start_operation_id`；会话已结束但收尾未完成时，
  同一 Agent 也会看到 `lastSession`；
- 有收尾记录时带 `cleanup`；存在待处理的恢复记录时 `next="status"`。

**观察状态（reference=O）**

```json
{"observation_state":"ready","target_reference":"O1","input_state":"effective",
 "session":{"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",
   "started_at":"…","expires_at":"…"}}
```

`observation_state` 为 `ready`/`claimed`/`retired`/`expired`；`input_state` 为
`effective`（仍可作为输入目标）、`expired`、`revoked`，或其他观察状态原值。`expired`
时 `next="observe"`，会话结束则 `input_state="revoked"`、`next="status"`。

**操作状态（reference=P）**

```json
{"operation_id":"P2","operation_state":"finished","outcome":"executed",
 "phase":"finished","session":{"origin":"P1","state":"active","mode":"virtual",
   "agent":"local-agent","started_at":"…","expires_at":"…"}}
```

- `running` 时带 `phase` 与 `next="status"`；
- `finished` 时带 `outcome`、`phase`，操作错误在 `error`，`next` 由错误决定；
- 从未见过的 P：`{"operation_id":"P9","operation_state":"not_seen"}`；
- 已封闭的 P：`request_closed=true`、`outcome=not_executed`、`session=null`；
- `end` 或“会话已结束且收尾未完成”的 P 会带 `cleanup` 与 `next="status"`。

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
  新采集通常相对 O1 返回 delta；页面未变时是空差异（只有 `format` 与 `base`）。
- `observe(P…)`：强制 full。P 必须属于本 Agent 且其原会话仍活动。
- 引用已结束会话的 O/P → `409 session_ended`；O 不存在或不属于本 Agent →
  `409 observation_not_found`；P 已封闭 → `409 request_closed`；P 不存在 →
  `409 operation_unknown`；P 属于本 Agent 但原会话已不可用 → `409 reference_has_no_session`。
  `observe` 不要求 O 仍为 `ready`（`claimed`/`expired` 也可再次观察），但只有
  `ready` 的 full 候选会被确认为基线。

成功响应只含操作号与新观察：

```json
{"operation_id":"P2","observation":{"status":"complete","id":"O2","origin":"P1","revision":2,
 "captured_at":"…","actionable_until":"…","screen":{"width":1080,"height":2400,"rotation":0},
 "foreground":{"state":"unknown","reason":"display_section_missing"},
 "tree":{"format":"delta","base":"O1"}}}
```

采集失败：HTTP 503，`observation.status=failed`、`missing=["tree"]`、`error` 为
`hierarchy_unavailable`/`display_unavailable`/`tree_too_large`/`stale_screen`/
`control_ui_on_virtual_display`；会话若已结束，响应带 `session` 与 `next="status"`。

### 图片

`image=true` 时服务在同一项设备工作内先读取目标屏源图，再读取层级；图片只是本次观察的
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
`image_too_large`，不改判核心树。图片缺失本身不强制 `next=observe`。

## 6. `POST /api/act`

```json
{"observation_id":"O1","operation":{"type":"tap","target":{"ref":"n1"}}}
```

| 顶层字段 | 必填 | 说明 |
| --- | --- | --- |
| `observation_id` | 是 | 本次要消费的 ready O |
| `operation` | 是 | 非 null 对象，见下表 |
| `image` | 否 | 布尔，默认 `false`；`true` 为本次后观察附加一张图片，规则见 §5 图片 |

`operation` 的五个分支（成员出现即校验，多余成员一律拒绝）：

| `type` | 允许成员 | 约束 |
| --- | --- | --- |
| `tap` | `target` | `target` 为 `{"ref":"n1"}` 或 `{"x":1,"y":2}` |
| `long_press` | `target` | 同上 |
| `text` | `target` + `text` | `ref` 目标必须指向可确认的输入对象（坐标目标在派发时绑定焦点输入）；`text` 必填字符串，可 `""`，≤4096 个 Unicode 码点 |
| `swipe` | `from` + `to` | 两个坐标对象，起终点不能相同，不接受 `ref` |
| `key` | `key` | 固定白名单：`BACK`、`ENTER`、`DEL`、`TAB`、`ESCAPE`、`DPAD_UP`、`DPAD_DOWN`、`HOME`、`APP_SWITCH` |

`target`/`from`/`to` 的规则：`ref` 与坐标互斥；坐标必须同时有 `x`、`y`，为 ≥0 的整数；
`ref` 不能为空或 `null`；`key`/`text` 不能是 `null`。

静态检查（不认领、不派发）：会话必须 `active`；O 必须为 `ready` 且未过期；ref 节点存在、
非 root、非 context、有 bounds、`enabled` 不为已知 `false`，并具备对应动作能力；`text` 还要
求 `role=input`、`actions` 含 `text`、`protected` 明确为 `false`；坐标必须落在目标逻辑屏内；
`HOME`/`APP_SWITCH` 仅在主屏（`display.ID==0`）可用。

通过后：取得唯一工作位置 → O 单向 `ready→claimed` → 延长租约 → 实时重验（目标显示仍存在、
重新采集、前台包/任务与观察时一致、ref 在新鲜树中按“完整内容身份 + 祖先链”唯一匹配）→
派发本次动作所需的输入（`text` 为焦点点击 + 显示绑定写入，任何一步失败都不重放）。

结果分支：

| `outcome` | 含义 | 附带 |
| --- | --- | --- |
| `executed` | 输入已派发且有完成证据（`text` 以独立读回确认） | 后观察（full 或 delta）；若期间被撤销，则 `next="status"` 且无后观察 |
| `not_executed` | 未派发（静态拒绝、重验失败、工作被撤销等） | `error`、`next` |
| `uncertain` | 已派发但无法确认完成（传输失败、读回失败、超时） | `error`（`action_result_uncertain` / `text_target_uncertain` / `text_result_uncertain`）、`next="status"` |

- 输入派发后即使 `text` 读回失败也不回滚、不重放；原 O 不再归还。
- `text` 先点击目标取得焦点，再做显示绑定的 `getTextOnDisplay` 唯一性确认、
  `setTextOnDisplay`，最后以独立采集重新绑定并读回；读回值必须精确等于写入值才判
  `executed`。
- 后观察失败时：HTTP 503，但 `outcome` 仍是 `executed`，`observation.status=failed`，
  `next="observe"`；不能因后观察失败改判未执行。
- 失败码：`node_not_found`、`node_disabled`、`unsupported_node_action`、
  `invalid_coordinates`、`unsupported_key`、`invalid_request`（含超长文本）、
  `text_target_uncertain`、`target_changed`、`foreground_changed`、`foreground_unknown`、
  `display_unavailable`、`hierarchy_unavailable`、`stale_screen`、`device_busy`、
  `session_ended`、`observation_retired`、`observation_expired`、`observation_claimed`、
  `control_revoked`。

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
 "next":"status"}
```

- `end` 只撤权并启动独立收尾，不等待设备工作、账本或网络；`cleanup` 通常先是
  `pending`，用 `status` 轮询到 `complete` 或 `unconfirmed`。
- 重复 `end` 返回同一收尾进度，不新建第二条链。
- 对从未被接纳的 P：`request_closed=true`、`session=null`、
  `cleanup` 全为 `not_applicable`。
- 已结束且无可收尾资源的会话：`cleanup.state=complete`。
- 未知/非法引用：`409 observation_not_found`、`operation_not_found`、
  `session_not_found` 或 `422 invalid_request`。

## 8. `POST /v1/control/sync`

控制 App 心跳。请求字段（`internal/protocol.AppSyncRequest`）：

| 字段 | 必填 | 类型 | 说明 |
| --- | --- | --- | --- |
| `contract_version` | 是 | 字符串 | 必须为 `"2"`，否则 `409 contract_mismatch` |
| `app_instance_id` | 是 | UUID | App 进程实例；变化会撤销旧归属 |
| `app_version` | 是 | 字符串 | 仅发行标识 |
| `selected_mode` | 是 | `virtual`/`primary` | 用户选择 |
| `mode_generation` | 是 | 1–64 字符 | 选择代次 |
| `mode_ready` | 是 | 布尔 | 所选模式可用 |
| `stop_ready` | 是 | 布尔 | 停止通道可用 |
| `notifications_enabled` | 是 | 布尔 | 通知可用 |
| `background_running` | 是 | 布尔 | 前台服务运行中 |
| `power_interactive` | 否 | 布尔或 `null` | 未知为 `null`/省略 |
| `keyguard_locked` | 否 | 布尔或 `null` | 未知为 `null`/省略 |
| `primary_display` | 否 | 对象或 `null` | 出现时 `display_id` 必须为 0，宽/高/密度 >0，旋转 0–3，`generation` 非空 |
| `display` | 否 | 对象或 `null` | 自有虚拟屏；出现时 `display_id` >0，其余同上 |
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
  "contract_version": "2",
  "service_instance_id": "…",
  "control": {
    "revision": 42,
    "availability": {"state":"ready"},
    "session": {"origin":"P1","agent":"local-agent","mode":"virtual","state":"active",
      "started_at":"…","expires_at":"…"},
    "work": {"operation_id":"P2","kind":"act","phase":"dispatch"},
    "cleanup": {"state":"complete","execution":"confirmed","tasks":"not_applicable",
      "recents":"not_applicable","display":"confirmed"}
  },
  "commands": [{"id":"…","kind":"create_display","width":1080,"height":2400,"density_dpi":420}],
  "messages": [{"id":"M12","seq":12,"text":"…","created_at":"…"}],
  "stop": {"seq":7,"state":"accepted"}
}
```

- `availability.state`：`ready`/`recovering`/`blocked`；非 `ready` 时可带 `reason`；
- `session`：`null` 或 `{origin, agent?, mode, state, started_at?, expires_at?, ended_at?, reason?}`；
- `work`：`null` 或 `{operation_id, kind, phase}`；`kind` 为 `start`/`observe`/`act`/`apps`/`open_app`/`shell`；
- `cleanup`：`null` 或 `{state, execution?, tasks?, recents?, display?}`；
- `commands`：数组，每拍至多一条显示命令；`create_display` 仅在有 `starting` 会话且显示助手
  已就绪时下发，`close_display` 在收尾/恢复需要精确释放时下发；
- `messages`：数组，未确认的 Agent 消息，按创建顺序，每拍至多 8 条 / 32 KiB 正文；未在
  `message_acks` 中确认的消息会在后续同步重发；App 按 `id` 去重；
- `stop`：仅当本请求携带 `stop_pending` 且服务已绑定该边界时出现；`state` 为
  `accepted`/`duplicate`；
- `control.revision` 只在可用性、会话、工作、收尾等实际控制事实变化时递增；同一
  `service_instance_id` 内单调。

## 9. `POST /v1/control/stop`

```json
{"contract_version":"2","app_instance_id":"…","stop_seq":7,"reason":"user_stop"}
```

- **不校验普通契约**：`contract_version` 字段存在但不参与准入，服务与 App 契约版本
  不一致时仍可停止；
- `app_instance_id` 必须是 UUID，`stop_seq` 必须 >0；
- `reason` 为空或超过 64 字符时按 `user_stop` 处理；
- 重复或过期 `seq` 不报错，只返回 `state=duplicate`。

```json
{"contract_version":"2","service_instance_id":"…","accepted":true,"stop_seq":7,"state":"revoked"}
```

`accepted` 恒为 `true`；`state` 为 `revoked`（绑定时撤销了活动会话）、`recorded`
（无活动会话，仅记录边界）或 `duplicate`（未生效）。重复 `seq` 同样返回
`accepted=true` 与 `state=duplicate`。响应不包含普通服务状态、命令或设备事实，也不表示
物理资源已释放。

## 10. `GET /health`

无认证，只表示进程可应答，不代表设备就绪或资源干净：

```json
{"status":"ok","contract_version":"2","service_instance_id":"…","release_version":"…"}
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

### Node

```json
{"ref":"n7","role":"button","bounds":[40,300,240,380],
 "label":["确定"],"actions":["tap"],"enabled":false}
```

字段全集：`ref`、`role`、`bounds`、`label`、`actions`、`enabled`、`focused`、`selected`、
`checked`、`value`、`hint`、`protected`、`axis`、`resource`、`context`、`children`。

| 字段 | 编码规则 |
| --- | --- |
| `ref`/`role` | 总是出现；`role` 属于固定集合 `root`、`group`、`text`、`button`、`input`、`checkbox`、`radio`、`switch`、`toggle`、`scroll`、`image`、`view` |
| `bounds` | 总是出现，`[left,top,right,bottom]` 或 `null`（未知/context 节点） |
| `label`/`actions`/`children` | 空时省略；`actions` 取 `tap`、`long_press`、`text`、`swipe` |
| `enabled` | 默认 `true`；已知 `false` 时显式 `false`；未知时显式 `null` |
| `focused`/`selected`/`protected` | 默认 `false`；已知 `true` 时显式 `true`；未知时显式 `null` |
| `checked`/`value`/`hint`/`axis`/`resource` | 未知时省略；已知值显式给出（`false`/`""` 也保留） |
| `context` | 仅在 `true` 时出现，表示非可见的必要祖先（`role=group`、`bounds=null`、无动作/标签） |

合成根固定为 `role=root`、目标屏矩形、`enabled=true`、其余布尔已知 `false`；虚拟屏没有
任何子节点时，合法的公开树就是“只有根、没有 `children`”。

### Tree 与 Delta

```json
{"format":"full","scope":"reported_visible_with_context","root":{…}}
{"format":"delta","base":"O1","set":{"n7":{"label":["已发送"],"actions":[]}},
 "add":[…],"remove":["n3"],"children":{"n1":["n2","n7"]},"replace":{"n4":{…}}}
```

- `scope` 固定为 `reported_visible_with_context`：目标屏上报告可见的节点及其必要祖先，
  其他显示器的节点不发布；
- delta 的五个块都可省略；空差异只含 `format` 与 `base`；
- `set` 的每个成员只携带发生变化的字段，但值必须是**完整新值**，显式保留
  `false`/`0`/`""`/`[]`/`null`；省略表示“未修改”，`null` 表示“置为未知”；
- `children` 给出父节点的完整子列表；`add` 的新子树必须被某条 `children` 挂接；
  `remove` 删除基线中的整棵子树；
- 角色变化、跨父移动、根替换等不可表达的变化不会出现在 delta 中，服务会改发 full。

### Image

```json
"image": {"captured_at":"…","width":540,"height":1200,"media_type":"image/jpeg"}
"image": {"read_only":true,"captured_at":"…","width":540,"height":1200,
  "media_type":"image/jpeg","screen":{"width":1080,"height":2400,"rotation":0}}
"image": {"error":{"code":"image_capture_failed","message":"未能取得本次截图"}}
```

`captured_at` 是源图读取开始的服务 UTC 时间；`width`/`height` 是输出图片的实际尺寸；
`media_type` 固定 `image/jpeg`。`read_only` 仅在没有新 O 的只读图片上出现，并附同屏
`screen`。失败图片只带受控 `error`，不带伪造的尺寸或时间。HTTP 载体
`observation.image.base64` 是本次编码图片的 base64；MCP 入口把它投影为 `ImageContent`，
文本与结构化内容不携带该成员。

### Session / Cleanup

```json
"session": {"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",
  "started_at":"…","expires_at":"…"}
"session": {"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",
  "started_at":"…","ended_at":"…","reason":"user_stop"}
"cleanup": {"state":"complete","execution":"confirmed","tasks":"confirmed",
  "recents":"not_applicable","display":"confirmed","error":{"code":"…","message":"…"}}
```

`cleanup.error` 只在存在未确认项时出现（`pending` 或 `unconfirmed` 都可能带），`code` 可能是
内部原因串：`execution_unconfirmed`、`display_create_unconfirmed`、`task_remove_unconfirmed`、
`recents_cleanup_unconfirmed`、`display_release_unconfirmed`。

## 12. 发现、应用、只读、通知与受管执行工具

本节列出 `inspect`、`keepalive`、`diagnose`、`apps`、`open_app`、`notify`、`shell` 的请求与结果。

### `POST /api/inspect`

```json
{"observation_id":"O2"}
{"observation_id":"O2","node":"n7"}
{"observation_id":"O2","query":"确定"}
```

`node` 与 `query` 互斥。`inspect` 只读保留中的历史树，不采集、不续租、不确认基线、
不认领 O，也不需要 O 仍可输入。

- 整树补看受完整公开树上限（128 KiB）；`node` 子树与 `query` 单个命中受 32 KiB 上限；
- `query` 在 `label`/`value`/`hint`/`resource` 上做字面 Unicode 折叠子串匹配，按原树顺序
  返回完整命中子树和祖先 `ref` 路径；`total` 为预算前命中数，只给完整单位时显式
  `truncated`；
- 常见失败：`409 observation_not_found`（不存在或不属于本 Agent）、
  `409 content_unavailable`（树已被淘汰）、`409 node_not_found`、
  `409 inspection_too_large`（`details.limit` 为字节上限）。

### `POST /api/keepalive`

```json
{"reference":"O1"}
```

O/P 必须属于本 Agent 并定位一个仍 `active` 的原会话；只续租会话活动期，不延长任何 O 的
`actionable_until`，不改变 O 状态，不占工作位置、不访问设备。成功返回
`outcome=executed` 与新的 `session.expires_at`。重复 P 只返回原操作视图，不会再次续租。
已结束/过期会话返回 `session_ended`/`session_expired`；P 没有原会话返回
`reference_has_no_session`。

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

### `POST /api/apps`

```json
{}
{"query":"设置"}
```

`query` 对本地化应用名或包名做字面 Unicode 折叠子串匹配，不是正则或选择器。成功返回：

```json
{"operation_id":"P7","apps":{"items":[{"name":"设置","package":"com.android.settings"}],
  "total":1,"truncated":false,"captured_at":"…"}}
```

`items` 总是数组；`total` 是预算前匹配数；`truncated` 表示只返回了完整条目前缀；
`captured_at` 是设备清单的实际读取时间（缓存命中时保持原时间）。`apps` 不返回
`outcome`，不占 UI 输入权、不续租。设备清单读取占普通工作位置；另一项工作占用时，
只有仍可信的新鲜缓存可直接返回，否则 `409 device_busy`。无会话时可调用。
常见失败：`503 app_list_unavailable`、`409 control_revoked`、`409 device_busy`。

### `POST /api/open_app`

```json
{"observation_id":"O4","app":"com.android.settings"}
```

`app` 只接受明确包名。它和 `act` 一样消费当前 `ready` O；静态/只读拒绝（包名不存在、
控制界面出现在虚拟屏）发生在认领之前，不消费 O。

- 虚拟屏：优先复用目标屏上该包已有的 live 窗口（`reused=true`），否则在目标屏新建窗口；
  不搬走/认领用户主屏任务；新语境后置一次 full 观察；
- 主屏：按普通同屏应用打开，不登记/不关闭用户任务；
- 返回 `app`（`package`、`task_id`、`display_id`、`reused`、`state_preserved`）与
  `observation`；只有已证明的任务才返回 `app`；
- 常见失败：`409 app_not_found`（next=user）、`503 app_launch_failed`、
  `504 app_launch_unconfirmed`、`409 application_moved`、`409 task_focus_unavailable`。
  派发后无法证明任务归属为 `uncertain`，不重放。

虚拟会话收尾按精确任务 id 移除目标屏任务并核验 `dumpsys activity recents`，再关闭显示；
`cleanup.tasks`/`cleanup.recents` 分别表达。主屏收尾不关闭用户应用。

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

派发前服务原子地撤销原会话当前 O 的输入授权、清除已确认基线并置全量恢复边界；旧树可继续
`inspect`，但旧 full 的迟到确认不能解除该边界。受管链确认结束、原作用域复核通过后，默认
后置一次 full 观察（不取图）。

```json
{"operation_id":"P30","outcome":"executed",
 "shell":{"termination":"exited","exit_code":0,"output":"…","truncated":false},
 "observation":{"status":"complete","id":"O31","origin":"P_start","tree":{"format":"full","scope":"reported_visible_with_context","root":{…}}}}
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
`not_executed`/`not_started`。超时/取消只报告已确认的终止；没有停止证据不包装成 timeout。

常见失败：`409 root_unavailable`、`503 shell_start_failed`、`504 shell_result_unconfirmed`、
`409 session_ended`/`reference_has_no_session`/`operation_unknown`、`409 device_busy`。
已结束但仅输出缺失时响应仍为 `executed`，错误在嵌套 `shell.output_error`；后观察失败不改写
命令执行事实，`next=observe`。命令正文、输出与异常文本不进入日志或账本。

## 13. 端点与能力边界

| 端点/能力 | 契约行为 |
| --- | --- |
| `start`、`status`、`observe`、`end` | 见 §3、§4、§5、§7 |
| `act`：`tap`、`long_press`、`swipe`、`key`、`text` | 定义于 §6；其他动作类型不被接受 |
| `observe`/`act` 的 `image=true` | 见 §5 图片；图片是本次观察的附加材料，不是单独工具 |
| 截图、图片块 | HTTP/SDK 用同一 `observation.image` 元数据与 `base64` 载体；MCP `ImageContent` 承载图片字节 |
| `open_app`、`apps`、`keepalive`、`inspect`、`notify`、`diagnose` | 定义于 §12 |
| `shell` | 定义于 §12 |
| MCP stdio / Streamable HTTP、Go client | 由独立入口提供，见 [MCP 入口](mcp.md) 与 [Go 客户端](client.md)；不改变本页 `/api/*` 契约 |
| `/v1/control/sync`、`/v1/control/stop`、`create_display`/`close_display` | 定义于 [控制端协议](control-protocol.md) |
| `selected_mode=primary` | 使用主屏（display 0），不创建虚拟屏 |

未注册的 `/api/<tool>` 路径由 Go `ServeMux` 返回默认的纯文本 `404 page not found`，不是本页
的 JSON 错误形状；客户端应先按工具目录判断能力。已定义端点中，`GET /health` 是唯一不需要
认证的端点。
