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
  "contract_version": "4",
  "app_instance_id": "0b0b8c0e-...",
  "app_version": "0.4.3",
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
| contract_version | 是 | 共同普通契约；必须等于 `"4"` |
| app_instance_id | 是 | 本次 App 进程实例，UUID；重建即变化 |
| app_version | 是 | 仅发行标识，不参与能力协商 |
| selected_mode | 是 | `virtual` 或 `primary`；App 重建默认 virtual |
| mode_generation | 是 | 用户选择代次，UUID；模式切换即变化 |
| mode_ready | 是 | 所选模式是否可用（显示/权限/主屏确认） |
| stop_ready | 是 | 通知 + 前台服务等停止通道均可用 |
| notifications_enabled | 是 | 系统通知关闭时为 false |
| background_running | 是 | 前台服务仍在运行 |
| power_interactive / keyguard_locked | 否 | `true`/`false`；未知为 `null` 或省略 |
| primary_display | virtual 模式应有 | 主屏实际逻辑尺寸；出现时校验形状（无效返回 `422 invalid_request`）；`display_id` 必须为 0；virtual 模式缺失不单独阻断准入 |
| display | 有自有虚拟屏时 | 本次 App 实际持有的虚拟屏 |
| command_results | 否 | 对上一拍命令的回执；省略/null 按 `[]` 处理 |
| stop_pending | 否 | 未被服务受理的本地停止意图 |
| message_acks | 否 | App 已保存的消息 ID，至多 32 个；省略/null 按 `[]` 处理；未知或已释放的 ID 忽略 |

`primary_display.generation` 必须等于本次同步中的 `mode_generation`。App 不得把缓存描述当作
物理存在；`display` 只在系统仍报告该显示时出现。

响应：

```json
{
  "contract_version": "4",
  "service_instance_id": "d0e...",
  "control": {
    "revision": 42,
    "availability": {"state": "ready"},
    "session": {"origin":"P1","agent":"agent-short","mode":"virtual","state":"active",
      "started_at":"…","expires_at":"…"},
    "work": {"operation_id":"P2","kind":"act","phase":"dispatch"},
    "cleanup": {"state":"complete","execution":"confirmed","tasks":"not_applicable",
      "recents":"not_applicable","display":"confirmed"}
  },
  "commands": [
    {"id":"3b1...","kind":"create_display","width":1080,"height":2400,"density_dpi":420}
  ],
  "messages": [
    {"id":"M12","seq":12,"text":"…","created_at":"…"}
  ],
  "stop": {"seq":7,"state":"accepted"}
}
```

- `control.revision` 在同一 `service_instance_id` 下单调递增；只有实际控制事实变化才递增。
- `availability.state` 为 `ready` / `recovering` / `blocked`，`reason` 为受控短码。
- `session` 为 `null` 或 {origin, agent, mode, state, started_at, expires_at/ended_at, reason}。
- `work` 为 `null` 或 {operation_id, kind, phase}，`kind` 为 `start`/`observe`/`act`/`apps`/`open_app`/`shell`；
  不得携带节点、坐标、输入正文或命令。
- `cleanup` 为 `null` 或 {state, execution, tasks, recents, display}；只表达最低摘要。
- `commands` 只包含显示生命周期命令，每拍至多一条；重复投递同一命令直到收到回执。
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
```

- `create_display` 前服务已通过 ADB 启动 root helper；App 收到命令后通过既有
  Binder 助手创建虚拟屏，先确认 IME HIDE，再在下一次同步中报告 `display`。
- `close_display` 只释放属于 `target_app_instance_id` + `target_display_id` +
  `target_generation` 的精确资源；不关闭用户主屏 task。
- 回执 `{command_id, ok, error_code}` 只确认 App 本地处理结果，不证明物理资源已释放；
  服务仍核验 `dumpsys display`/SurfaceFlinger 与 App 在同步中报告的 `display` 事实。
- App 不执行任意 shell、intent、键值或坐标；命令集合固定为上述两种。

## 4. 独立 STOP

`POST /v1/control/stop`

```json
{"contract_version":"4","app_instance_id":"...","stop_seq":7,"reason":"user_stop"}
```

响应（最小，不含普通服务状态、消息或设备命令）：

```json
{"contract_version":"4","service_instance_id":"...","accepted":true,"stop_seq":7,"state":"revoked"}
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
| cleanup.state=pending/unconfirmed 或本机释放未确认 | 控制已停止 · 资源清理未确认 |
| availability=recovering | 正在核验上次资源 |
| availability=blocked | 控制不可用 · 查看原因 |
| availability=ready，session=null | 虚拟屏 · 待命 |
| session.state=starting | Agent · 正在准备 |
| session.state=active，work=null | Agent · 会话有效，无设备操作 |
| session.state=active，work.kind=act | Agent · 正在操作 |
| session.state=active，work.kind=open_app | Agent · 正在打开应用 |
| session.state=active，work.kind=observe | Agent · 正在观察 |
| session.state=active，work.kind=apps | Agent · 正在读取应用清单 |
| session.state=active，work.kind=shell | Agent · 正在执行命令 |

主文案按上表自上而下取第一条成立的事实（本地停止/停止未同步 > 失鲜 > 清理风险 > 活动工作
/会话 > 待命）。当前会话持有的虚拟屏本身不是清理风险；只有停止后仍持有、释放未确认或服务
报告清理未确认时才进入清理风险。完整界面在短文案之外展开原会话、本机记录的最近一次已结束
会话/设备工作、停止同步、模式选择、权限与清理五项；这些历史只用于说明，不改变授权、准入
或主文案。

## 6. 失鲜与重启

- App 每秒同步一次；连续 6 秒没有成功响应即判失鲜，界面进入未知，不保留缓存中的
  active 运行状态。
- 服务实例变化时，App 先使旧命令与旧回执失效，保持不可准入，等待新的恢复结果；
  不得把 `revision` 归零当作旧响应。
- App 重建默认 virtual；主屏授权不持久化，必须重新由用户确认。
- 服务重启撤销旧输入授权，不重放旧命令；App 的待同步停止意图在服务实例变化后仍保留，
  直到取得原意图的确认或明确服务已换实例。
- 服务重启后未确认的 Agent 消息按原 ID 继续投递；App 的消息与本地已读属于 App 私有数据，
  不因服务实例变化或 App 重建被清空，也不会恢复任何旧命令或主屏授权。
