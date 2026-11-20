# MCP 工具面实机覆盖测试（0.23.0，虚拟屏模式）

- 记录时间：2026-11-21（Asia/Shanghai）。
- 本记录涉及实机：是。共三次虚拟屏会话，主测试会话 `session.mode=virtual`、`display_id=130`。
- 本记录**不涉及代码变更**，是探索性覆盖记录：只登记本轮实际走到的公开分支与拒绝分支，
  不把“设计推演”写成“验证结论”。
- 按 AGENTS.md 第 11 条，原始截图、节点树、日志与个人信息不入库。本记录只写到应用与页面
  结构层级，不摘录账号、观看/收听/搜索历史等个人内容。

## 1. 记录性质与范围

本轮由人工发起，用真实手机和真实第三方应用，从外部调用方视角把 13 个 MCP 工具的公开分支
尽量走一遍，回答“哪些入口确实可用、哪些拒绝是设计好的、哪些边界还没测到”。

覆盖应用：哔哩哔哩（`tv.danmaku.bili`）、X（`com.twitter.android`）、网易云音乐
（`com.netease.cloudmusic`）。全程虚拟屏，未使用主屏会话。

## 2. 环境与身份

| 项 | 值 |
| --- | --- |
| 设备 | `24117RK2CC`，Android 16（API 36） |
| 屏幕 | 物理 `1440x3200`，override `1080x2400`，rotation `0` |
| 会话模式 | `virtual`；主测试 `display_id=130`，补充核对 `display_id=131/132` |
| 手机 App | `com.mobileuse.vnext`：`versionName=0.23.0`、`versionCode=25`（`dumpsys package` 实测） |
| 发行源 | `release/versions.json`：`release_version=0.23.0`、`contract_version=21`、`android_version_code=25`、`source_commit=a6b653d86e8af600ec3395876b5bd81c8bff14f9` |
| 代码检出头 | `befea30da02e807a38d4ef5c618c3880dc111512`（本轮只使用已部署的主机 stdio 适配器，未重新构建、未替换产物） |
| 主机适配器 | DSH 托管的 `mcp__mobile-use__*` stdio MCP 入口；本轮未采集主机进程 PID/哈希 |
| 网络状况 | 设备处于无可用外网状态（Play Store 提示“未连接到互联网”，应用内有多处网络重试提示）；该限制影响部分应用业务结果，不影响工具 UI 分支覆盖 |

主测试会话：`Peb32788d6e865907f0b3c606`，`started_at=2026-11-20T16:09:32Z`、
`ended_at=2026-11-20T16:30:13Z`，共 174 个 observation 修订（`revision=1–174`）。
补充会话一：`P0f08ace9dc27395b8c5b7f6c`，用于核对手机 App 版本；补充会话二：
`Pf16cecdd3676052dfda82b34`，用于补测 `act` 的 `image=true`。三次会话均以
`session.state=ended`、`cleanup.state=complete` 收尾（`cleanup.execution/tasks/recents/display`
均为 `confirmed`）。

## 3. 覆盖矩阵

13 个工具全部触达，7 个 `app.kind` 全部触达。

| 工具 | 分支 | 结果 |
| --- | --- | --- |
| `start` | 空请求新建会话 + 首采 | 已实测：三次虚拟屏会话均返回首个 `full`，`session.mode=virtual` |
| `status` | 无参 | 已实测：`can_start=false`、`blocking_code=session_exists`、`session.*` |
| `status` | `O` | 已实测：`observation_state=ready`、`input_state=effective` |
| `status` | `P` | 已实测：`operation_state=finished`、`outcome=executed`、`phase=ended`、`cleanup.*` |
| `observe` | 默认 | 已实测：多次得到 `full` 与 `delta` |
| `observe` | `full=true` | 已实测：强制 `full`，`quality=unknown/0/not_sampled` |
| `observe` | `image=true` + `wait_ms` | 已实测：同批返回 JPEG `540x1200` |
| `observe` | `P` 指向活动会话 | 已实测：用于 `text` uncertain 后的核对入口 |
| `act` | `tap`（ref） | 已实测：大量成功；含 bubble 提升 |
| `act` | `tap`（坐标） | 已实测：`no_safe_point` 建议点重试成功、截图导航坐标成功 |
| `act` | `long_press`（ref） | 已实测：B 站视频卡成功打开“反馈/我不想看”面板 |
| `act` | `long_press`（ref，tap-only 节点） | 已实测（拒绝分支，见 §5） |
| `act` | `text`（默认 `press_enter=true`） | 已实测：`write=executed`、`enter=executed`（X 搜索）；另有分阶段 uncertain（见 §5） |
| `act` | `text`（`press_enter=false`） | 已实测：写后同节点精确读回 |
| `act` | `text`（空串清空） | 已实测：读回为提示态 |
| `act` | `scroll`（`container.ref`，`amount=page/half`） | 已实测：`effect=observed`/`unknown` 均有样本 |
| `act` | `scroll`（嵌套容器/内层容器） | 已实测 |
| `act` | `scroll`（中心线不可证容器） | 已实测（拒绝分支，见 §5） |
| `act` | `swipe`（raw，`duration_ms`） | 已实测：竖向滚动、横向滑动等 |
| `act` | `swipe`（起点歧义） | 已实测（拒绝分支，见 §5） |
| `act` | `key` `BACK` / `ENTER` / `DEL` / `TAB` / `ESCAPE` / `DPAD_DOWN` | 已实测 |
| `act` | `key` `HOME` | 已实测（拒绝分支，见 §5） |
| `act` | `image=true` | 已实测：动作后同批返回 JPEG `540x1200` |
| `snap` | — | 已实测：只返回 `operation_id` + 图片元数据，不发布 `O` |
| `wait_for` | `query` + `stable=true` | 已实测：`trigger=query`、`elapsed_ms=1537` |
| `wait_for` | `query` + `stable=true` 超时 | 已实测：`trigger=timeout`、`matched=["query","timeout"]`、`elapsed_ms=15169` |
| `wait_for` | `absent_query` | 已实测：`trigger=absent_query`、`elapsed_ms=535` |
| `inspect` | `node`（子树） | 已实测：detailed 视图补回 compact 省略的小节点 |
| `inspect` | `query` | 已实测：`matches=1 total=1`，带 `path=[…]` |
| `app.find` | `query`（三个应用） | 已实测：均返回 `app.total=1` |
| `app.list` | `scope=session` | 已实测：随 open/close 实时增减；`app` 过滤成功 |
| `app.list` | `scope=primary` | 已实测：`display_id=0`，含 `ownership=shared` 与 `foreground` |
| `app.open` | 三个应用 | 已实测：`reused=false` 新建与 `reused=true` 复用均有样本 |
| `app.handoff` | `task_id`（虚拟屏 → 主屏） | 已实测：`display_id=0`、`ownership=shared`、`state_preserved=true` |
| `app.claim` | `task_id`（主屏 → 虚拟屏） | 已实测：`ownership=borrowed`、`state_preserved=true` |
| `app.close` | 借入窗口 + `allow_borrowed=true` | 已实测：`recents_cleaned=true`、`app.complete=true` |
| `app.close` | 非本会话共享任务 | 已实测（拒绝分支，见 §5） |
| `app.close` | 本会话 owned 窗口 | 已实测：`recents_cleaned=true`、`app.complete=true` |
| `app.kill` | 前台服务/播放中（默认） | 已实测（拒绝分支，见 §5） |
| `app.kill` | `allow_stop_playback=true` | 已实测：`target_pids`/`killed_pids`/`remaining_pids`/`restarted` |
| `shell` | 非 root | 已实测：设备型号、`wm size`、Android 版本 |
| `shell` | `root=true` | 已实测：`id` 返回 `uid=0(root)` |
| `notify` | — | 已实测：`saved=true`、`notification.state=pending` |
| `keepalive` | `duration_minutes=10` | 已实测：`expires_at` 延长到调用后 10 分钟 |
| `diagnose` | 无参 | 已实测：`issues` + `timeline`、`coverage=own_records`、`truncated=true` |
| `end` | — | 已实测：`cleanup.state=pending` → 轮询 `status(P)` 后四项 `confirmed`、`cleanup.state=complete` |

观察质量三态均取得：`stable`、`unstable`（`budget`/`max_samples`）、`unknown`
（`not_sampled`）。树形态取得 `full` 与 `delta`，含 `~`/`+`/`-`/`children`/`=` 与
`changes=0` 样本。

## 4. 实机操作轨迹

按“像人一样用手机”的顺序，不写个人内容：

| 应用 | 走过的主要路径 | 覆盖到的重点工具分支 |
| --- | --- | --- |
| 哔哩哔哩 | 推荐流上下滚动 → 视频播放页 → 点赞/取消点赞 → 收藏夹选择 → 评论面板 → 发布测试评论 → 分享面板复制链接 → 搜索页输入、历史/热搜、横向滑动标签 → 按播放量排序 → 搜索结果视频详情 → 投币弹窗/滑动 → 关注 → 简介/评论 Tab → 我的页 → 历史记录 → 发布面板 → 关注页 → 会员购 → 消息中心 → 直播页 → 进入直播间 → 首页视频卡长按反馈面板 | `open`/`scroll`/`tap`/`coordinate tap`/`long_press`/`text`/`key`/`swipe`/`snap`/`inspect` |
| X | For You 时间线滚动 → 点赞/取消 → 转帖/撤销 → 书签/取消 → 分享表复制链接 → 展开长帖 → 发帖 Composer 输入（网络失败保存草稿） → 探索搜索输入并回车 → 通知页 → 私信列表/会话 → 个人资料页 → 导航抽屉 → handoff/claim/close | `app.open`/`handoff`/`claim`/`close`/`text`+ENTER/`wait_for`/`observe(image)`/`inspect` |
| 网易云音乐 | 首页推荐 → 每日推荐播放页 → 播放/暂停/上一首/下一首 → 红心 → 评论页 → 更多菜单收藏 → 新建歌单并输入名称 → 搜索页输入（实时联想）→ 歌手页 → 播放热门歌曲 → 播放队列当前/历史 → 歌词页 → 心动页快速收听 → 播客页 → 听书页 → 福利/会员页 → 我的页/歌单页 → 测试歌单确认 | `open`/`tap`/`text`/`key`(DEL/TAB/ESCAPE/DPAD_DOWN)/`scroll`/`swipe`/`snap`/`kill` |

跨应用部分还覆盖：`app.find`/`app.list` 两种 `scope`、`shell`（root/非 root）、`notify`、
`diagnose`、`keepalive`、`end` 与 cleanup 收敛。

## 5. 行为观测与错误分支

本轮所有失败均带明确错误码，未出现“静默做了”或“静默没做”。

### 5.1 保护性拒绝（全部 `outcome=not_executed`）

| 触发条件 | 错误码 | 关键字段/重试结果 |
| --- | --- | --- |
| 外层 `scroll` 容器中心线无法证明轨迹 | `unsupported_node_action` | `reason` 指向中心线不可证；改用内层容器后成功 |
| 对只含 `tap` 的节点做 `long_press` | `unsupported_node_action` | `reason=target_has_other_action`、`target_actions=["tap"]` |
| 对无交互动作的文本节点做 `tap`（bubble 提升） | `unsupported_node_action` | `reason=no_interactive_ancestor` |
| 对非可编辑按钮做 `text` | `text_target_uncertain` | `stage=target`、`reason=target_not_editable`，`write/enter=not_attempted` |
| raw `swipe` 起点落在多个同级节点 | `ambiguous_point` | 提示调整起点或改用 `scroll.container`；移动起点后成功 |
| 虚拟屏下 `key=HOME` | `unsupported_key` | “HOME/APP_SWITCH 需要共享主屏” |
| 关闭非本会话共享任务 | `task_not_tracked` | “该任务不属于本会话” |
| 终止有前台服务/播放的应用（未授权） | `kill_refused_playback` | 提示需要显式允许；`allow_stop_playback=true` 后成功 |
| 快照到派发之间目标变化（动态计数/Toast） | `target_changed` | 附新 `O`，`recovery.entry=retry`；新 `O` 重试成功 |
| 目标点上的节点已变化 | `target_changed` | 与上同类，零派发 |
| `no_safe_point`（多候选归属歧义） | `no_safe_point` | 附 `ambiguous_refs`、`suggested_carrier_ref`、`suggested_point`；按建议坐标重试成功 |
| 节点不存在（旧树 ref 混用） | `node_not_found` | 重新观察后使用新 ref 成功 |

### 5.2 `text` 分阶段结果

- 在两个“边输边搜”的搜索框（B 站、网易云）上，`press_enter=true` 返回
  `outcome=uncertain`、`text_input.write=executed`、`text_input.enter=not_executed`、
  `dispatch.enter=not_attempted`，`recovery.entry=observe` + `recovery.reference=P`。
  `observe(P)` 证实文字已写入；随后单独发送 `key=ENTER` 成功触发搜索。
- X 的搜索框上同一调用返回 `write=executed`、`enter=executed`，搜索页正常进入。
  说明 `ENTER` 的确认取决于控件形态，调用方应在边输边搜控件上优先 `press_enter=false`。
- `text` 写入后同节点精确读回、字符计数变化、发送键由 `enabled=false` 变 `true` 均有样本。

### 5.3 应用生命周期

- `app.open` 首次启动偶发 `app_launch_unconfirmed` / `outcome=uncertain`，但 `foreground`
  已显示目标包，`observe` 后即可继续；这是启动确认与前台事实的分层，不是启动失败。
- `handoff` 将 X 从虚拟屏移到主屏，`ownership=shared`、`state_preserved=true`；`claim`
  再移回虚拟屏，`ownership=borrowed`、页面状态保留；`close`（`allow_borrowed=true`）成功清理。
- `app.close` 对 owned 窗口返回 `recents_cleaned=true`；对不属于本会话的共享任务返回
  `task_not_tracked`。
- `app.kill`：默认因前台服务/播放被拒；显式 `allow_stop_playback=true` 后返回
  `target_pids`、`killed_pids`、`remaining_pids=[]`、`restarted=false`。随后任务记录仍在，
  关闭另一应用后该任务又出现在前台；本轮只记录现象，未定性。
- `end`：返回 `cleanup.state=pending`；按 `status(P)` 轮询后 `cleanup.execution/tasks/recents/
  display` 均为 `confirmed`、`cleanup.state=complete`。补充会话 `recents=not_applicable` 也正常。

### 5.4 其他现象

- `delta` 以最近一张 `full` 为基准；清空输入后返回 `changes=0` 表示“回到基准形态”，不是
  “动作无效”。
- 视频/直播的 SurfaceView 内容不会完整出现在公开树中，`snap` 图片可补足画面事实；这是
  应用渲染层与可访问树的边界。
- 设备无外网：X 的点赞/书签等 UI 计数可先变化并伴随网络错误 Toast；发帖进入草稿提示；
  Play Store 显示离线错误。上述为应用与网络环境结果，不是 MCP 工具缺陷。
- `diagnose` 在本轮记录到的近期问题覆盖：`retired_cleanup_dropped`、`text_target_uncertain`、
  `unsupported_node_action`、`no_safe_point`、`node_not_found`、`target_changed`、
  `ambiguous_point`、`kill_refused_playback`、`unsupported_key`。

## 6. 未覆盖项与原因

| 项 | 原因 |
| --- | --- |
| `primary` 模式下的完整会话行为 | 用户指定虚拟屏模式；`HOME`/`APP_SWITCH` 只能覆盖拒绝分支 |
| `app.close` 借入窗口未带 `allow_borrowed` 的拒绝分支 | 本轮直接使用了显式允许路径 |
| `app.list` 的 `truncated=true` | 设备任务数未触发预算截断 |
| `inspect` 的 `content_unavailable` / `inspection_too_large` | 历史树未被淘汰、最大树未超限 |
| `text` 多行写入 / `verify/readback_mismatch` | 三个应用输入框均为单行 |
| `shell` 超时 / 输出截断 / `termination=unknown` | 已有专门记录，本轮未重复 |
| `notify` 送达 App 消息中心 | 只验证到 `saved=true` / `pending`，未在手机端查看消息中心 |
| `wait_for` 的 `image=true` 与无查询 `stable` 分支 | 未构造 |
| `diagnose` 指定 `O`/`P` | 只用了无参形式 |
| `end` 期间的并发 `act` 竞态 | 未构造 |

## 7. 结论

- 13 个 MCP 工具在虚拟屏模式下全部可用，7 个 `app.kind` 全部走通。
- 高频动作（tap、scroll、swipe、text、back、snap）在三个应用上取得了多轮样本；错误分支
  均以明确错误码返回，并给出可执行的重试/恢复入口。
- `no_safe_point`、`target_changed`、`text_target_uncertain` 等保护性拒绝在本轮多次出现，
  按返回的 `suggested_point`、新 `O` 或 `recovery=observe` 处理后均能继续，未造成会话卡死。
- 网络不可用是本次应用业务结果的主要外部变量；工具层的 UI 覆盖与生命周期操作不受影响。
