# 控制端协议

本页定义手机内控制 App 与设备服务之间的最小配对协议：模式、显示资源、命令回执、最低控制
视图、消息中心投递与独立 STOP。Agent 工具接口见 [接口契约](interface.md)。本协议与普通业务
契约共用一个 `contract_version`；STOP 载体独立于普通契约。

## 1. 传输与身份

- 全部请求为 `POST`，JSON，`Authorization: Bearer <control_token>`。
- 服务只监听 `127.0.0.1`。App 从私有 `files/connection.json` 读取 `base_url` 与 token。
- 控制身份与 Agent 身份分离：`control_token` 只能访问 `/v1/control/*`；`agent_token`
  只能访问 `/api/*`。两者互不代替。
- 普通同步必须精确匹配 `contract_version`。不匹配返回 `409 contract_mismatch`，不解析
  普通命令、不建立准备事实。最小 STOP 不要求普通契约匹配。
- 普通同步的契约不匹配错误体带 `recovery`（`entry=update`、`reference=none`）；同步成功响应
  不是工具结果，不增加 `recovery` 成员。控制 App 不解析工具结果，只按 `contract_version`
  重建。

## 2. 心跳/控制同步

`POST /v1/control/sync`

```json
{
  "contract_version": "20",
  "app_instance_id": "0b0b8c0e-...",
  "app_version": "0.20.0",
  "selected_mode": "virtual",
  "mode_generation": "8f0b2e2e-...",
  "mode_ready": true,
  "stop_ready": true,
  "notifications_enabled": true,
  "background_running": true,
  "power_interactive": true,
  "keyguard_locked": false,
  "primary_display": {"display_id":0,"width":1080,"height":2400,"density_dpi":420,"rotation":0,"generation":"8f0b2e2e-...","ime_hide":false},
  "display": null,
  "command_results": [{"command_id":"3b1...","ok":true,"error_code":null}],
  "stop_pending": {"seq":7,"reason":"user_stop"},
  "message_acks": ["M12"]
}
```

字段约束：

| 字段 | 必填 | 含义 |
| --- | --- | --- |
| contract_version | 是 | 共同普通契约；必须等于 `"20"` |
| app_instance_id | 是 | 本次 App 进程实例，UUID；重建即变化 |
| app_version | 否（App 应发送） | 仅发行标识，不参与能力协商 |
| selected_mode | 是 | `virtual` 或 `primary`；App 重建默认 virtual |
| mode_generation | 是 | 用户选择代次，UUID；模式切换即变化 |
| mode_ready | 否（App 应发送） | 所选模式有效且没有待同步的本地 STOP；不表示虚拟显示已创建或权限已就绪 |
| stop_ready | 否（App 应发送） | 通知 + 前台服务等停止通道均可用 |
| notifications_enabled | 否（App 应发送） | 系统通知关闭时为 false |
| background_running | 否（App 应发送） | 前台服务仍在运行 |
| power_interactive / keyguard_locked | 否 | `true`/`false`；未知为 `null` 或省略 |
| primary_display | virtual 模式应有 | 主屏实际逻辑尺寸；出现时校验形状（无效返回 `422 invalid_request`）；`display_id` 必须为 0；virtual 模式缺失不单独阻断准入；可选 `ime_hide`（布尔） |
| display | 有自有虚拟屏时 | 本次 App 实际持有的虚拟屏；可选 `ime_hide`（布尔） |
| command_results | 否 | 对上一拍命令的回执；省略/null 按 `[]` 处理 |
| stop_pending | 否 | 未被服务受理的本地停止意图 |
| message_acks | 否 | App 已保存的消息 ID，至多 32 个；省略/null 按 `[]` 处理；未知或已释放的 ID 忽略 |

`AppSyncRequest` 没有字段 presence。上表五个“否（App 应发送）”字段缺失时，字符串按空值、
布尔按 `false` 处理；可用性通常因此为 `blocked`，但不会仅因缺字段返回 `422`。服务对必填
存在性显式校验的是 `contract_version`、`app_instance_id`、`selected_mode` 和
`mode_generation`。

`primary_display.generation` 必须等于本次同步中的 `mode_generation`。App 不得把缓存描述当作
物理存在；`display` 只表示当前 attempt，且只在系统仍报告该显示时出现。退休 attempt 不回退为
`display`；`display=null` 也不表示设备上已没有旧残留。`mode_ready` 只表达当前模式与当前控制
能力，不因 retired history 或本地 `cleanupPending()` 变假。

响应：

```json
{
  "contract_version": "20",
  "service_instance_id": "d0e...",
  "control": {
    "revision": 42,
    "availability": {"state": "ready"},
    "session": {"origin":"P1","agent":"agent-short","mode":"virtual","state":"active",
      "started_at":"…","expires_at":"…"},
    "work": {"operation_id":"P2","kind":"act","phase":"act.capture"},
    "cleanup": null
  },
  "commands": [],
  "messages": [
    {"id":"M12","seq":12,"text":"…","created_at":"…"}
  ],
  "stop": {"seq":7,"state":"accepted"}
}
```

- `control.revision` 在同一 `service_instance_id` 下单调递增；只有实际控制事实变化才递增。
- `availability.state` 为 `ready` / `recovering` / `blocked`，`reason` 为受控短码。
- `session` 为 `null` 或 {origin, agent, mode, state, started_at, expires_at/ended_at, reason}。
- `work` 为 `null` 或 {operation_id, kind, phase, app_kind?}，`kind` 为
  `start`/`observe`/`snap`/`wait_for`/`act`/`app`/`shell`；`kind=app` 时 `app_kind` 为七种 kind 之一
  （`find`/`list`/`open`/`claim`/`handoff`/`close`/`kill`）；不得携带节点、坐标、输入正文或命令。
- `cleanup` 为 `null` 或 {state, execution, tasks, recents, display}；只表达最低摘要。
- `commands` 只包含显示生命周期命令，每拍至多一条；每个命令 ID 只投递一次；原预算内的后续清理尝试使用新 ID。
- `messages` 只包含尚未确认的 Agent 消息，按创建顺序，每拍至多 8 条且正文合计至多 32 KiB；
  必须始终是数组。未确认消息在后续同步中重发；App 收到 `message_acks` 后释放对应项。
- `stop` 只在本次请求携带了 `stop_pending` 时出现，确认服务已绑定该停止边界。

App 必须拒绝无法解析、未知 kind、缺字段或 `contract_version` 不匹配的响应；不得用默认
`idle/ready` 补齐。

### 消息投递

- 服务端有界保存 `notify` 消息（256 条 / 1 MiB 正文），未确认项跨服务重启保留在独立私有
  文件；消息正文不进入日志、错误、恢复账本或发布清单。
- App 按消息 ID 去重，写入自己的私有有界存储后在下一次同步确认；确认只表示“已保存到本机”，
  不表示已显示或已读。App 不向服务或 Agent 回报已读。
- 消息正文只用于消息中心与系统通知；控制视图、通知停止入口与日志不携带正文。

## 3. 显示命令

```json
{"id":"...","kind":"create_display","width":1080,"height":2400,"density_dpi":420}
{"id":"...","kind":"close_display","target_app_instance_id":"...","target_display_id":12,
 "target_generation":"..."}
{"id":"...","kind":"cancel_create","target_app_instance_id":"...",
 "target_create_command_id":"..."}
```

- `create_display` 的 `id` 同时是该 attempt 的 display generation；Java attempt、engine
  session、账本与关闭目标使用同一个值。服务在命令就绪前通过 ADB 启动该 attempt 的 root
  helper；CREATE/VERIFY 使用带 token 的异步请求/回调，App 主线程只校验当前或退休 attempt，
  不因 Binder 永久不返回而把新 attempt 排在同一阻塞队列后面。
- App 收到 `create_display` 后由 attempt helper 创建虚拟屏，先确认 IME HIDE，再在下一次同步
  中报告当前 `display`。retiring 非空不拒绝新 create；同一 create command 不得重复创建。
- `close_display` 只释放属于 `target_app_instance_id` + `target_display_id` +
  `target_generation` 的精确资源，可命中当前或退休 attempt；不关闭用户主屏 task。
- `cancel_create` 只按 `target_app_instance_id` + `target_create_command_id` 封闭该次创建
  尝试的本地派发与后续发布；成功只表示 App 本地处理结果，不证明已经跨派发边界的 CREATE
  被撤回。其它命令不得携带 `target_create_command_id`。
- 回执 `{command_id, ok, error_code}` 只确认 App 本地处理结果，不证明物理资源已释放；
  服务仍核验 `dumpsys display`/SurfaceFlinger 与 App 在同步中报告的 `display` 事实。
- App 不执行任意 shell、intent、键值或坐标；命令集合固定为上述三种。

## 4. 独立 STOP

`POST /v1/control/stop`

```json
{"contract_version":"20","app_instance_id":"...","stop_seq":7,"reason":"user_stop"}
```

响应（最小，不含普通服务状态、消息或设备命令）：

```json
{"contract_version":"20","service_instance_id":"...","accepted":true,"stop_seq":7,"state":"revoked"}
```

- App 先在本地提高停止代次、拒绝旧命令/回调恢复权限，再在独立线程发起本请求；不等待
  普通心跳、设备工作、日志/账本或资源清理。
- `stop_seq` 是 App 进程内单调序号；重建后保留原意图。重复发送同一意图只取得同一确认。
- 服务收到后立即撤权、封闭之后的派发、取消原工作并触发独立收尾。`accepted` 只表示停止
  边界已绑定，不代表物理资源已释放。
- 服务按 `app_instance_id` 记录最高 `stop_seq` 与最低确认；旧代次不会作用于新会话。
- 该端点不使用普通 `contract_version` 校验；服务与 App 契约版本不一致时，本地停止仍可送达。

## 5. 最低控制视图文案映射

App 只能从 `control` 与本机事实构造界面，不显示 Agent step，也不自行推断任务完成。
控制视图采用以下最小映射：

| 事实 | 主文案 |
| --- | --- |
| 本地 stop pending 未确认 | 已阻止本地旧命令 · 停止待同步 |
| 连接失鲜 | 连接中断 · 服务状态未知 |
| availability=recovering | 正在核验当前控制能力 |
| availability=blocked | 控制不可用 · 查看原因 |
| availability=ready，session=null | 虚拟屏 · 待命 / 主屏共享 · 待命（按选定模式） |
| session.state=ended | 上次会话已结束（附受控原因） |
| session.state=starting | Agent · 正在准备 |
| session.state=active，work=null | Agent · 会话有效，当前无设备操作 |
| session.state=active，work.kind=act | Agent · 正在操作 |
| session.state=active，work.kind=app，app_kind=find | Agent · app · 查找应用 |
| session.state=active，work.kind=app，app_kind=list | Agent · app · 列出任务 |
| session.state=active，work.kind=app，app_kind=open | Agent · app · 打开应用 |
| session.state=active，work.kind=app，app_kind=claim | Agent · app · 认领任务 |
| session.state=active，work.kind=app，app_kind=handoff | Agent · app · 交还任务 |
| session.state=active，work.kind=app，app_kind=close | Agent · app · 关闭窗口 |
| session.state=active，work.kind=app，app_kind=kill | Agent · app · 终止进程 |
| session.state=active，work.kind=observe | Agent · 正在观察 |
| session.state=active，work.kind=snap | Agent · 正在截图 |
| session.state=active，work.kind=wait_for | Agent · 正在等待界面 |
| session.state=active，work.kind=shell | Agent · 正在执行命令 |

主文案按本地停止待同步、失鲜、当前可用性、当前会话/工作、待命的顺序决定；历史清理风险只进入详情。完整界面在短文案之外展开原
会话、本机记录的最近一次已结束会话/设备工作、停止同步、模式选择、权限与清理五项；这些历史
只用于说明，不改变授权、准入或主文案。

`work.phase` 只映射受控短码，未知值显示“未知”且不回显服务原文。相态有两种形态：裸短码
（`admitted`、`read`、`dispatch`、`capture`、`observe`、`running`、`finished`）与 `<tool>.<step>`
（例如 `act.settle.tree`、`app.open.capture`、`wait_for.poll`）。`wait_for` 的映射为：

| `work.phase` | 显示文案 |
| --- | --- |
| `wait_for.poll` | 等待条件 |
| `wait_for.capture`、`wait_for.tree`、`wait_for.image`、`wait_for.project` | 验证采集 |
| `wait_for.commit` | 提交观察 |

`snap` 的映射为：

| `work.phase` | 显示文案 |
| --- | --- |
| `snap.capture` | 读取屏幕 |
| `snap.image` | 读取截图 |
| `snap.commit` | 提交截图 |

其余工具前缀（`start`、`observe`、`act`、`app`、`open_app`、`shell`，含 `app.open`
这类工具加子步骤的前缀）按步骤短码映射：`capture` 采集中、`tree` 读取节点树、`image`
读取截图、`project` 构造公开树、`commit` 提交观察、`settle` 等待稳定、`read` 读取中。
未列出的前缀或步骤一律回落“未知”。阶段文案只用于说明当前步骤，不参与授权、准入或停止判定。

## 6. 失鲜与服务/App 重建

- App 每秒同步一次；连续 6 秒没有成功响应即判失鲜，界面进入未知，不保留缓存中的
  active 运行状态。
- 服务实例变化时，App 先使旧命令与旧回执失效，保持不可准入，等待新的恢复结果；
  不得把 `revision` 归零当作旧响应。
- App 重建默认 virtual；主屏授权不持久化，必须重新由用户确认。
- 服务重启撤销旧输入授权，不重放旧命令；App 的待同步停止意图在服务实例变化后仍保留。
  只有同一 App 实例、同一 `seq` 且被服务受理的原意图得到确认时才清除 pending；服务实例
  变化本身不作废该 pending 意图。
- 服务重启后的旧 session 转退休记录；版本 3/4/5 中已发出预算且仍为 `pending` 的旧补偿先持久化为 `unconfirmed`。
  retired/旧 cleanup 不作为 App 的模式准备或新 create 门禁，当前 descriptor 与当前能力就绪
  后可建立新 attempt。
- 服务重启后未确认的 Agent 消息按原 ID 继续投递；App 的消息与本地已读属于 App 私有数据，
  不因服务实例变化或 App 重建被清空，也不会恢复任何旧命令或主屏授权。
