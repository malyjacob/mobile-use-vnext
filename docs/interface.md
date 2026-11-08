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
| 普通契约 | `/api/*` 必须带 `X-Mobile-Use-Contract: 1`，精确相等；缺失或不匹配返回 `409 contract_mismatch` |
| 预分配 P | `/api/*` 可带 `X-Mobile-Use-Operation: P…`；缺省由服务分配。`status` 不使用该头 |
| 请求体 | 必须是一个 JSON 对象；大小上限：`start`/`status`/`end` 16 KiB，`observe` 32 KiB，`act` 64 KiB，`sync` 32 KiB，`stop` 8 KiB |
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
observation_state, input_state, session, observation, cleanup, error, next
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
| 409 | `contract_mismatch`、`device_busy`、`session_*`、`reference_*`、`operation_*`、`observation_*`、`node_not_found`、`node_disabled`、`stale_screen`、`target_changed`、`foreground_changed`、`foreground_unknown`、`control_revoked`、`request_closed`、`cleanup_unconfirmed`、`control_unavailable`、`stop_unavailable`、`mode_not_selected`、`device_locked`、`device_not_interactive` |
| 422 | `invalid_request`、`invalid_coordinates`、`unsupported_key`、`unsupported_node_action` |
| 501 | `unsupported_feature`（`image=true`） |
| 503 | `device_unavailable`、`display_unavailable`、`display_create_failed`、`hierarchy_unavailable`、`tree_too_large`；以及 `observation.status=failed` 的结果 |
| 504 | `action_result_uncertain`（输入已派发但无法确认完成） |
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
| `image` | 否 | 布尔，默认 `false` | `true` 直接返回 `501 unsupported_feature` |
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

## 6. `POST /api/act`

```json
{"observation_id":"O1","operation":{"type":"tap","target":{"ref":"n1"}}}
```

| 顶层字段 | 必填 | 说明 |
| --- | --- | --- |
| `observation_id` | 是 | 本次要消费的 ready O |
| `operation` | 是 | 非 null 对象，见下表 |
| `image` | 否 | 布尔，默认 `false`；`true` 返回 `501 unsupported_feature` |

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
| `contract_version` | 是 | 字符串 | 必须为 `"1"`，否则 `409 contract_mismatch` |
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
| `command_results` | 是 | 数组 | 可为 `[]`；元素 `{command_id(1–64), ok(bool), error_code(string 或 null, ≤64)}` |
| `stop_pending` | 否 | 对象或 `null` | `{seq(>0), reason(≤64)}`，表示未被服务受理的本地停止 |

校验规则：显式校验 `contract_version`、`app_instance_id`、`selected_mode`、
`mode_generation`、非 null 的 `primary_display`/`display`/`stop_pending` 与每条
`command_results`；其余字段缺失时按 `false`/空值处理，可用性通常因此为 `blocked`，不返回
`422`。`command_results` 省略或为 `null` 时按空数组处理。未知成员、重复键与类型混用由
严格解码拒绝。

响应：

```json
{
  "contract_version": "1",
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
  "stop": {"seq":7,"state":"accepted"}
}
```

- `availability.state`：`ready`/`recovering`/`blocked`；非 `ready` 时可带 `reason`；
- `session`：`null` 或 `{origin, agent?, mode, state, started_at?, expires_at?, ended_at?, reason?}`；
- `work`：`null` 或 `{operation_id, kind, phase}`；`kind` 为 `start`/`observe`/`act`；
- `cleanup`：`null` 或 `{state, execution?, tasks?, recents?, display?}`；
- `commands`：数组，每拍至多一条显示命令；`create_display` 仅在有 `starting` 会话且显示助手
  已就绪时下发，`close_display` 在收尾/恢复需要精确释放时下发；
- `stop`：仅当本请求携带 `stop_pending` 且服务已绑定该边界时出现；`state` 为
  `accepted`/`duplicate`；
- `control.revision` 只在可用性、会话、工作、收尾等实际控制事实变化时递增；同一
  `service_instance_id` 内单调。

## 9. `POST /v1/control/stop`

```json
{"contract_version":"1","app_instance_id":"…","stop_seq":7,"reason":"user_stop"}
```

- **不校验普通契约**：`contract_version` 字段存在但不参与准入，服务与 App 契约版本
  不一致时仍可停止；
- `app_instance_id` 必须是 UUID，`stop_seq` 必须 >0；
- `reason` 为空或超过 64 字符时按 `user_stop` 处理；
- 重复或过期 `seq` 不报错，只返回 `state=duplicate`。

```json
{"contract_version":"1","service_instance_id":"…","accepted":true,"stop_seq":7,"state":"revoked"}
```

`accepted` 恒为 `true`；`state` 为 `revoked`（绑定时撤销了活动会话）、`recorded`
（无活动会话，仅记录边界）或 `duplicate`（未生效）。重复 `seq` 同样返回
`accepted=true` 与 `state=duplicate`。响应不包含普通服务状态、命令或设备事实，也不表示
物理资源已释放。

## 10. `GET /health`

无认证，只表示进程可应答，不代表设备就绪或资源干净：

```json
{"status":"ok","contract_version":"1","service_instance_id":"…","release_version":"…"}
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
内部原因串：`execution_unconfirmed`、`display_create_unconfirmed`、`owned_tasks_present`、
`display_release_unconfirmed`。

## 12. 端点与能力边界

| 端点/能力 | 契约行为 |
| --- | --- |
| `start`、`status`、`observe`、`end` | 见 §3、§4、§5、§7 |
| `act`：`tap`、`long_press`、`swipe`、`key`、`text` | 定义于 §6；其他动作类型不被接受 |
| `observe`/`act` 的 `image=true` | 返回 `501 unsupported_feature`；本契约不定义图片载荷 |
| 截图、图片块、MCP `ImageContent` | 不在本契约范围 |
| `open_app`、`apps`、`keepalive`、`inspect`、`notify`、`shell`、`diagnose` | 路由未定义；请求返回 `404 page not found` |
| MCP stdio / Streamable HTTP、Go client | 服务不提供 MCP 端点或 Go client |
| `/v1/control/sync`、`/v1/control/stop`、`create_display`/`close_display` | 定义于 [控制端协议](control-protocol.md) |
| `selected_mode=primary` | 使用主屏（display 0），不创建虚拟屏 |

未注册的 `/api/<tool>` 路径由 Go `ServeMux` 返回默认的纯文本 `404 page not found`，不是本页
的 JSON 错误形状；客户端应先按工具目录判断能力。已定义端点中，`GET /health` 是唯一不需要
认证的端点。
