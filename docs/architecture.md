# 架构

本文描述设备服务的进程分层、唯一权威、会话与观察/操作生命周期、full/delta 的提交条件、
工作位置与并发、STOP 与收尾、恢复屏障、账本以及事实分层。

## 1. 进程与包分层

```text
cmd/device-service
  ├─ 私有配置、进程锁、信号、启动顺序、退出
  ├─ internal/httpapi    认证与 /api/<tool>、/v1/control/*
  ├─ internal/engine     唯一业务权威（会话、O/P、工作位置、STOP、收尾、恢复、消息队列）
  ├─ internal/android    ADB、u2 JSON-RPC、固定 JAR、root 显示助手
  ├─ internal/observation 公开树构造、保号、full/delta 纯算法
  ├─ internal/imaging    图片等比缩小与单次 JPEG 编码（纯算法）
  ├─ internal/store      私有恢复账本与消息文件（原子文件、单写者）
  └─ internal/eventlog   有界字段白名单日志

cmd/device-mcp
  ├─ 私有配置、stdio / Streamable HTTP 传输、认证与 Origin 门禁
  ├─ internal/mcp        13 工具注册、schema、严格接入、结果与图片投影、取消包装
  └─ internal/client     强类型 HTTP 客户端（发送前 P、结构化结果/错误、图片字节）
```

依赖方向由 `cmd` 组装时固定：

- 请求方向是 `httpapi → engine →（经注入的 Device 接口）android`；`engine` 同时使用
  `observation` 的纯算法、`imaging` 的受控错误分类、`store` 的账本与 `eventlog` 的事件汇。
  反向依赖不存在：`httpapi` 不访问 ADB、不保存会话、不判定接纳。
- `engine` 不导入 `internal/android`、`internal/store`；设备、账本、投影、差分与图片编码
  都是 `cmd` 注入的小接口（`Device`、`Store`、`Projector`、`Differ`、`ImageEncoder`）。
- `internal/device` 只放原始事实类型（`Capture`、`ImageSource`、`RawNode`、`Display`、
  `TextTarget` 等），无 I/O、无运行状态。
- `internal/observation` 与 `internal/imaging` 是纯算法：前者输入一次 `device.Capture`
  输出不可变公开树或差异，后者输入一次 `device.ImageSource` 输出一张有界 JPEG；都不接触
  会话、账本与设备。
- `internal/android` 只执行有界设备 I/O 并返回受控事实；不决定观察什么、操作什么。
- `internal/mcp` 只依赖 `protocol` 与 `client`，不导入 `engine`、`android`、`store`，不因
  同仓而绕过认证与服务状态；`internal/client` 只依赖 `protocol`。该包以单一固定说明源生成
  server instructions（`text`/`both` 的 `result v4` 紧凑文本、`tree v4` 与 `inspect v3` 的完整
  文法一次加跨工具调用规则；`both`/`structured` 的 `structuredContent` 保持完整 `Result` JSON），
  由 `server/discover` 与兼容 `initialize` 发布；13 个工具描述不内联树引导。
- `internal/store` 实现 `engine.Store` 与 `engine.MessageStore`，分别持久化 `engine.Ledger`
  与有界 Agent 消息。两者是不同文件、不同语义：账本只含归属与资源证据，消息文件只含待
  投递正文，正文不进入账本。
- `internal/eventlog` 只接收批准字段；队列有界（1024），满时丢弃并计数，不阻塞 STOP。

进程启动顺序（`cmd/device-service/main.go`）：读取并校验私有配置 → 在固定运行目录取得
`flock` 进程锁 → 创建账本存储 → 构造 android 适配器 → `engine.StartRecovery` → 启动时钟 →
监听 HTTP → 后台 `Prepare` + `EnsureU2`，成功后才 `SetDeviceReady(true)`。第二个实例拿不到
锁时退出（退出码 3），不会接触账本、App 或设备。

## 2. 唯一权威

会话、租约、O/P 接纳与关闭、输入授权、普通设备工作位置、收尾进度与恢复判定只由
`internal/engine` 的互斥状态持有。HTTP 层、控制 App 与其他接入适配器都不保存第二份执行
权威：

- 控制 App 只拥有自己的虚拟屏物理资源与显示生命周期，执行服务下发的
  `create_display`/`close_display`/`cancel_create`；它不能接纳会话、签发观察或自行执行输入。
- HTTP 认证通过后，工具请求仍必须由 engine 的准入、认领、提交条件逐项通过。
- 客户端持有的 O/P 编号只是引用；编号形状不授予任何权限，归属由认证身份与服务端记录决定
  （`operationState.Agent`、`observationState.Agent`）。

engine 的状态锁只做有限内存检查与转换：锁内不等待 ADB、u2 RPC、磁盘或树算法。设备 I/O、
树构造、delta 计算、账本写入都在锁外完成，提交时重新加锁复核。

## 3. 会话与 O/P 生命周期

### 会话

`starting → active → ended`（`sessionState.State`）。

- `start` 先登记 P、取得工作位置，再创建 `starting` 会话；虚拟屏要等 App 创建回执、App
  上报的显示描述、以及 `dumpsys`/SurfaceFlinger 的显示存在与 SFID 三者一致后才转 `active`。
- `start` 初始化、激活与首个 full 观察成功发布使用 `StartSessionTTL=5min`：接纳时以
  `started_at + 5min` 初始化，后两个续期点取 `max(expires_at, now + 5min)`。其他普通工作只在
  既有触发点（`act` 认领、`wait_for` 准入、成功发布新 O、`shell` 派发边界）按
  `SessionTTL=120s` 取 `max(expires_at, now + 120s)`，不继承显式 `keepalive` 时长，也不截短
  更长的剩余租期。
  `status` 查询与其他只读调用不续租。
- `keepalive` 是显式重设：engine 在同一状态锁内取应用时 `now`，校验原会话仍为当前 `active`
  且 `now < expires_at`，再覆盖为 `expires_at = now + duration_minutes`。时长是 1～120 的整数，
  省略为两分钟；每个新 P 都可延长或缩短原截止时间。120 分钟只约束单次请求，会话没有累计
  时长硬上限；`now == expires_at` 已视为到期，过期或结束的会话不能复活。
- 会话只因外部原因或启动阶段失败结束：显式 `end`（`client_end`）、STOP（`reason` 原样记入，
  独立 STOP 缺省或超长时用 `user_stop`）、
  租约到期（`lease_expired`）、App 实例变化（`app_restarted`）、
  服务退出/重启（`service_shutdown`；恢复旧归属记 `service_restarted`），以及
  `display_create_unconfirmed` 等启动失败。原因写入 `session.reason`；工具级期限不是会话事实，
  不撤销或冻结会话。
- 控制视图只投影当前会话；新会话开始后，旧会话不再占用当前槽位，只作为 engine 的有界
  历史记录。`status()` 可给出本 Agent 最近的原会话，`status(O)`/`status(P)` 只按其仍保留的
  原归属路由查询，缺失时不改绑到其它会话。公开 `cleanup` 必须与同一帧中的 `session` 同属
  一个原会话，不能拼入另一会话或全局清理状态。

> 除外部不可抗力（`end`、STOP、App 实例变化、设备不可达、服务退出/重启、租约到期）外，任何内部原因都不得撤销会话、不得让会话无法继续使用、不得让服务锁死到只能重启手机。

> 该保证约束工具级期限与内部未决事实：工作期限和已关闭输入窗口不撤销活动会话，也不阻止后续
> 普通工作。启动阶段失败、设备/App 不可达、恢复账本不可写与当前能力门禁属于外部资源条件，
> 仍按各自契约拒绝新工作或结束启动阶段的原会话（包括激活后的首个观察失败）。

### O（观察）

`O` 的公开状态为 `ready`、`claimed`、`retired`、`expired`。

- 每次成功提交发布一个新 O；新 O 发布时，上一个 `ready` 的 O 立即转 `retired`。同一会话
  最多一个 `ready` O。
- `act` 在静态检查通过、取得工作位置后把 O 从 `ready` 单向改为 `claimed`。认领后即使未派发
  任何输入也不归还；静态拒绝发生在认领之前。
- 过期由时钟（每秒）与请求时的惰性检查共同处理；有效期
  `min(本次采集最早读取起点 + 60s, session.expires_at)`。
- 保留上限 `MaxObservations=32`。当前 O、认领中的 O 和仍在窗口内的基线受保护，其余按采集时间淘汰。

### P（操作）

`P` 的公开状态为 `running → finished`；不存在的 P 返回 `409 operation_unknown`。

- 每个工具在任何设备副作用之前登记 P
  （`registerOperation`），可用 `X-Mobile-Use-Operation` 预分配；客户端传入的 P 按原值登记。
  缺省时服务生成 `P<4位>_<seq>`：4 位无前导零数字的实例标签加实例内单调序号（如
  `P7312_12`）。格式为 `P` + 字母/数字/`-`/`_`，总长 ≤64；调用方不得解析编号中的数值，
  也不得跨服务实例复用编号。
- 同一 P 重复到达只返回原操作的公开视图，不再次派发（包括已失败的操作）。
- 已完成操作的公开视图（重复 P、`status(P)` 与冻结结果）不带 `observation`；原操作交付过新
  观察时，视图中的 `recovery` 退化为 `observe`/`P`，不指向该视图无法定位的 O。
- P 记录上限 `MaxOperations=256`；淘汰只针对最旧的已完成、且不被会话/工作/O 保护的记录。
- 已结束或显式封闭的 P（`end` 对未接纳/已结束的引用等）进入有界 `closed` 集合（最多 512）。
  有“未接纳”正面证据时返回 `request_closed=true`、`outcome=not_executed`；记录淘汰后只剩
  “未来接纳已封闭”证据时，`end(P)` 返回 `409 operation_unknown`、`phase=reference_unknown`、
  `outcome=uncertain` 与 `request_closed=true`，不声称原动作未执行，也不结束其它会话。
- 服务生成的观察编号为 `O<4位>_<seq>`，与 `P` 共用同一实例标签；两者都在该实例内单调分配，
  `service_instance_id` 始终保持完整 36 字符 UUID。会话自身有 `epoch`，提交与恢复都用它
  识别“原会话”。

### app（统一应用生命周期）

`app` 是应用发现、任务归属与进程终止的唯一入口，`kind` 为 `find`/`list`/`open`/`claim`/
`handoff`/`close`/`kill`。查询与变更共用同一会话、O/P 与普通工作权威，不增加第二份任务表：

- `find`/`list` 是查询：不消费 O、不续租、无 outcome；`find` 使用 detached P，`list` 绑定
  `reference` 解析到的原会话。`open`/`claim`/`handoff`/`close`/`kill` 消费当前 ready O；
  静态拒绝不消费，认领后不归还。主屏会话只允许 `find`/`list`/`open`。请求引用统一为
  `reference`；`open` 的操作目标固定为 `target.app`，`list` 的顶层 `app` 只是查询过滤。
- task 归属分三类：`owned` 是会话虚屏中可确认的自有任务（虚拟屏 open 新建或复用的窗口）；
  `borrowed` 是显式从主屏 `0` claim 进本会话虚屏的任务；`shared` 是主屏 open/list 或已
  handoff 交给主屏的任务。`shared` 不进入会话删除名单；主屏 open 不登记用户任务。
- 迁移责任池是账本根级 `task_transfers`：每次 claim/handoff 在派发前写入方向（`in`/`out`）
  与 `prepared`/`may_dispatch`/`terminated`/`not_dispatched`/`unknown`/`released` 状态。
  所有删除候选（当前会话任务、冻结快照、`checkedTaskIDs`、本屏 live 自动采纳、recent-only
  与恢复重试）都查询同一保护池；出站未确认或已交出的任务不得被任何清理路径重新认领或按
  ID 删除。确认未派发后才解除排除；同一 task 有旧 `RemoveTask`/`MoveTask` 责任未结束时，
  再 claim 返回 `task_lifecycle_busy`，只限制该任务。
- `open` 启动前以一次全设备任务快照为基线：目标屏子集只决定复用（先前台、否则最小
  task ID），归属只认启动后新增且落在目标屏的唯一同包任务，任何屏的既有任务都不会被当作
  本次新建。虚拟屏未确认时，后置观察后的任务核对回填已证实的唯一落点，仅在登记成功时写
  owned，否则省略 ownership；主 outcome 仍为 uncertain。主屏 open 返回 shared、不登记。
  `claim` 要求主屏 live task 精确且 root 只有单个 leaf；`handoff` 每次只交还一个任务，
  默认不恢复前台。`close` 冻结不超过 16 个本会话管理的 live 目标，逐项记录 outcome 与
  独立 recents 核验；`complete` 仅在全部关闭且 recents 全确认时为真。`kill` 仅对本会话
  同屏唯一可归因、无已知 FGS/播放（或显式允许）的 live owned 应用执行精确 PID
  `kill -9`；虚拟屏上处于当前前台的 `live owned` 任务同样可终止（前台不再是拒绝原因），
  确认原进程实例全部结束（或原进程集为空）后同步移除任务窗口容器与会话登记，进程与窗口
  壳都确认才报 executed。不 force-stop、不追杀重启。
- 五个变更成功提交后统一做一次原会话屏 full 无图后观察；后观察失败保留 executed 与
  `observation.status=failed`，不改写主 outcome；STOP 撤权后的迟到候选不发布。

### 受控滚动与原始手势

`act.operation.type=scroll` 是受控滚动尝试：调用方声明阅读方向与幅度，服务在几何合法且作用域
可证明的容器内选择起止点。授权单位是同一可证明的滚动容器与新鲜轨迹，不是起点叶节点：

- 公开树构建期为每个非 `context`、有公开几何的 `role=scroll` 节点投影 `axis`：先按子项单轴排布
  （取非 `context`、正面积的直接子项，唯一非滚动子项时向内展开一层，至多采样前 16 项；主轴中心
  跨度至少 24px、主轴位移与跨度都不少于交轴 2 倍，且至少半数相邻子项在交轴有区间重叠），未判定
  时回退类名强特征（`HorizontalScrollView` 为 `x`；`NestedScrollView`/`ListView`/`AbsListView`
  或非横向 `ScrollView` 为 `y`；`ViewPager` 默认 `x`，含 `Vertical` 为 `y`）；子项证据优先于
  类名，证据不足或双向时省略。该投影只登记公开轴向事实，不改变 ref 保号签名；
- 静态与新鲜重验前均执行有界公开视图与原始拓扑校验，结构或映射损坏返回 `503 hierarchy_unavailable`；
- 容器选择先做轴向兼容判定：`up`/`down` 需纵向能力、`left`/`right` 需横向能力；公开 `axis`
  与请求轴冲突为不兼容，显式支持请求轴（含 `both`）为兼容且豁免扁平几何判据；`axis` 未知时，
  处于非同视口外层滚动祖先内、正交边不短于请求轴边 2 倍、请求轴跨度严格小于该祖先与屏幕同轴
  跨度较大者 25% 的极端扁平条带为不兼容；独立滚动容器不因自身外框比例自拒；
- 容器在观察的公开树中解析：`container.ref` 若自身是有效 `role=scroll`（含 `swipe`）则直接作为锚点
  （轴向不兼容时沿公开父链回退到最近兼容且可规划的外层滚动祖先，父链无解时尝试唯一轴向兼容的
  内层子滚动容器，仍无解返回 `422 unsupported_node_action`（`reason=axis_mismatch`）并附
  `candidates`；兼容祖先进入规划但均失败时返回其规划错误），若自身是 `role=scroll` 但缺 `swipe` 报
  `422 unsupported_node_action`；若为非滚动
  节点则沿公开父链跳过 `context` 止于 `root` 查找最近 `role=scroll` 祖先并自动提升，最近祖先轴向
  不兼容时继续向更外层兼容祖先提升，整条链无兼容祖先时返回 `422 unsupported_node_action`
  （`reason=axis_mismatch`、`scope=ancestor`）。坐标 `container={x,y}` 收集命中点上的全部有效滚动
  容器并归约父子链保留最内层集合 `I`：最内层轴向不兼容或轨迹被阻时沿命中祖先由近及远回退兼容
  容器，全部不兼容返回 `422 unsupported_node_action`（`reason=axis_mismatch`、`scope=point`）；
  多个互不为祖先的平级容器只在轴向兼容且恰 1 个可执行时唯一消歧，否则返回 `422 ambiguous_point`
  并附 `candidates`；
- 静态规划按 `50% → 25% → 75%` 固定顺序探测 3 条正交轴线：将自身、`raw` 外层祖先及满足双向同视口
  （双方宽与高交集均 ≥85%）的嵌套滚动子容器豁免阻塞，其余具有独立滚动作用域或未知来源的节点投影成
  轴上阻塞区间；在最长安全区间正中对称展开四档轨迹（`scrollGeometry` 显式接收选定轴线坐标，不回弹
  中点）：`step`/`half`/`page`/`full` 取可用跨度的 15%/30%/60%/85%，最小行程
  32px/48px/48px/48px，注入时长固定为 500/450/400/300ms；`scroll` 不接受 `duration_ms`。仅直接
  `container.ref` 3 轴全败时检查唯一可执行非同视口内层子滚动兜底；否则返回
  `422 no_safe_trajectory`（`trajectory_blocked` / `insufficient_travel`）并附 `candidates` 与可选 `blocking_refs`；
- 认领后容器走 Tier 1–4 双向唯一匹配，校验新鲜 `isScrollContainer`、`sameScrollScope`（轴向重叠与离轴容差）及公开 `axis` 未与请求方向显式冲突（冲突按 `409 target_changed`、`reason=capability_changed` 零派发）；Tier 4 的滚动容器证据要求唯一资源，或两侧可读文案存在交集（或相同非空资源且双向覆盖 ≥85%），不要求容器内全部文案不变；
  新鲜树按相同双向同视口规则先证明原段，失效时在同一容器内按 3 轴线与居中规则重规划；失败返回
  `409 target_changed` 并附封闭归因 `details.reason`（`target_missing`/`target_ambiguous`/`lineage_changed`/`capability_changed`/`scroll_scope_changed`/`trajectory_blocked`）；
- 静态不可执行在认领前拒绝，新鲜不成立走重验失败的交付路径。单次 `act(scroll)` 最多派发一次手势，不按派发后观察补滑。

坐标 `tap`/`long_press`/`text` 与 raw `swipe` 都以最深命中集合为授权单位：认领前在观察公开树中解析原点，认领后在新鲜帧重新取得命中集合，并按动作类型分别证明。集合匹配使用有界二分图最大匹配（Kuhn 增广路，命中集合顶点数上限 64），不要求逐边一一对应。

- **坐标 `tap`/`long_press`/`text`**：单边兼容要求目标自身源结构（包、类、文案、描述、资源与全部能力/启用/密码/可编辑三态）无已知冲突，新矩形在屏内且仍包含原点。匹配分两级：优先要求几何相交（重叠率至少 0.50）且原始祖先链 L1 或 L2 对齐；祖先链重建时，允许以双向面积覆盖均 ≥80% 的紧几何，加 E1 自身非空可读文案/描述、E2 全树双向唯一的非空资源、或 E3 有界子树可读文案指纹（输入/点击目标要求两侧指纹非空且排序后完全相等）之一作为抗重建回退。通过后还要在新鲜命中集合上重新归约最近交互拥有者，确认原点未引入新的竞争交互目标；任一条件不成立返回 `409 target_changed`（`reason=point_changed`）。
- **raw `swipe`**：只证明起点区域的源结构与防新遮挡，不要求无动作被动叶子的文案不变。新帧命中集合必须与旧集合等大且全部映射到互异有效节点；每个旧节点与新节点须同显示、同包、同类、同可编辑性，资源与点击/长按/滚动/启用/密码/勾选三态无冲突；任一侧具备动作能力时，文案与描述也不得冲突，两侧均为无动作被动叶子时忽略其文案与描述。新矩形必须在屏内、仍包含起点，且与旧矩形尺寸兼容、重叠率 ≥0.60、面积比不超过 4 倍。`Focused`、`Selected`、`Checked` 与祖先包装层差异不参与判定。任何新增更深层弹窗、对话框或浮层改变命中集合长度或节点结构时立即拒绝。raw `swipe` 永不返回 `ambiguous_point`：观察时命中超过 64 个节点则不做集合证明，只保留前台级校验；观察时命中为空时，新鲜帧同一点也必须仍为空，空白点被新节点或浮层覆盖同样拒绝。`tap`/`long_press`/`text` 对最深命中集合归约最近交互拥有者，≤1 个拥有者或恰 1 个满足请求动作能力的拥有者时直接消歧执行，否则返回 `422 ambiguous_point` 并附排序 `candidates`。`scroll` 不证明真实滚动方向、触摸消费者、业务安全或到达边界；设备事实仍只有 `Scrollable bool`，公开 `axis` 只是类名与子项排布得到的投影，不证明设备真实滚动轴。

### tap/long_press 的子树安全落点

`tap`/`long_press` 的 `ref` 目标自身已有请求动作时（无论 `bubble` 值），走直接目标 4 级规划
（`default_center` → `carrier_node` → `center_span` → `grid_3x3`，均经 `provesSubtreeTapPoint` 证明，
`grid_3x3` 通过即直接执行）；4 级均败时若阻塞 `C` 的顶层交互边界中恰有 1 个满足双向同视口（≥85%）、
未禁用、具备请求动作且自身通过直接 4 级规划的子容器 `B`，
则自动委托 `C → B`（`coextensive_child`）并在认领后双绑定重验 `C` 与 `B`。

当原目标 `C` 完全不含 `tap`/`long_press`/`text` 动作且有效 `bubble=true` 时，按两步顺序解析：
- 步 A（`bubble_ancestor`）：查找最近交互祖先 `A`，先在 `cClip = rect(C) ∩ scopeA.base` 内选点（均须通过
  `provesGuidancePoint`）；若无通过点且有竞争交互控件相交 `cClip`（或 `cClip` 为空），立即中止向 `A`
  无关区域的搜索以防漂移，转入步 B；仅在 `cClip` 非空且无竞争交互重合时才补测 `A` 自身 4 级候选；
- 步 B（`bubble_colocated`）：在非 `C` 祖先的公开可点击节点中筛选满足局部共位与反底板守卫（非 `C` 后代须
  （包含 `C` 中心或与 `C` 重合率 ≥50%）且裁剪面积 <50% 屏幕；`C` 后代须与 `C` 双向同视口，或（与 `C` 重合率 ≥50%
  且裁剪面积 <50% 屏幕））的目标 `B`，且 `B` 须先在 `rect(C) ∩ scopeB.base` 内通过局部视觉点证明，局部选点失败
  后再按 `B` 自身直接 4 级候选证明；折叠双向同视口祖先-后代后若唯一（或唯独 1 个包含 `C` 中心）则吸附 `C → B`；
  多个冲突且无法以中心唯一消歧时返回 `422 no_safe_point`（`reason=colocated_ambiguous`）。

`bubble=false` 禁用 `bubble_ancestor` 与 `bubble_colocated`（直接 `ref` 目标自身已有请求动作时仍走直接规划并可使用
`coextensive_child`）。`text(ref)` 焦点规划不做祖先提升、共位吸附与 `coextensive_child` 委托，先过统一
`invalid_target`/`node_disabled`，再做可编辑/受保护校验（`422 text_target_uncertain`），最后复用直接
4 级 `tap` 焦点规划。除参数语法、节点存在/禁用及 `text` 自身输入能力/保护拒绝外，`act` 静态目标解析与规划拒绝统一收口为 5 个正交 `422` 错误码（`invalid_target`、`unsupported_node_action`、
`ambiguous_point`、`no_safe_point`、`no_safe_trajectory`），有候选时附统一 `candidates`（至多 4 项，干跑计算 `executable`，
不含文本/标签）与可选 `blocking_refs`（至多 3 项）。

认领后 ref 目标使用有界 Tier 1–4 双向唯一匹配（L1 不比较祖先 bounds、Focused/Selected；Tier 1 保持目标
bounds；Tier 2 允许受限微移；Tier 3 仅移除已知空透明层并要求非根强锚点）。Tier 4 只在 Tier 1–3 全部零候选
且观察到祖先链失配时进入：要求同显示、`sameStructure` 无已知冲突、矩形在屏内且双向面积覆盖均 ≥80%、
尺寸差不超过 `max(8, 较大边/5)`、中心位移不超过 `max(16, 较大边/4)`，并满足任一强语义证据：E1 自身非空文案/描述精确相等；E2 同屏同包全树双向
唯一的非空资源；E3 有界子树（每侧至多 64 个后代）可读文案指纹——点击/长按/输入目标要求两侧非空且排序后
完全相等，滚动容器要求唯一资源或至少 1 项文案重合（或相同非空资源且双向覆盖 ≥85%）。正向与反向扫描都
要求全树唯一；Tier 4 命中的点击/长按/输入执行目标实际落点还须通过全树最深命中与外部竞争交互拥有者检查（`tapFrameTier4OcclusionProven`），
Tier 1–3 保持原有目标子树与双绑定关系边界。命中后分别按直接目标、`coextensive_child`、
`bubble_ancestor`（禁止跨模式切回 `bubble_colocated`）或 `bubble_colocated` 双绑定复验原节点、关系守卫与安全点；
实际 `tapFrame` 冻结新鲜坐标/载体供派发使用。重验失配返回 `409 target_changed` 并附封闭 `details.reason`；
`reason=lineage_changed` 时可附可选 `changed_ref`，按原目标公开祖先链自近及远给出第一个与新鲜帧失配、
且能唯一确定的非 `root` 祖先 ref。`bubble_ancestor` 的最近交互祖先变更时，该字段指向原先的交互祖先。
无法唯一定位时省略 `changed_ref`。该字段不进入事件日志。`text` 重验失败仍保持 `stage`/`reason=target_unconfirmed`/
`dispatch` 专属结构；预算耗尽、畸形树或投影损坏返回 `503 hierarchy_unavailable`；撤权优先返回
`control_revoked`，不交付新 O。判据细节见[接口契约](interface.md)。

该证明只覆盖公开树模型的目标子树，不覆盖子树外同级或跨窗口 z 序、未发布的透明触摸层、
应用自定义事件拦截或私有业务回调。树证明与 `executed` 都不证明 Android 实际事件消费者或
期望业务结果。

### text 的复合输入阶段

`text` 只在 `auto_submit` 有效为 `true` 时拥有公开的复合提交语义：engine 在成功认领 O 的同一状态锁内
初始化私有 `textStage`，并发布公开不可变 `text_input:{write,submit}` 快照；`auto_submit=false` 只执行
写入并读回，省略 `text_input`，提交语义与 `submit_ref` 一并忽略（`submit_ref` 的语法仍按接口校验）。`textStage` 保存累计
focus/write/submit 原语状态、三个原语的 dispatch 事实（`not_attempted`/`not_dispatched`/`dispatched`/`unknown`）、
认领时的 sessionEpoch/AppInstanceID/ModeGeneration/显示 ID 与 generation 不可变绑定，以及
`currentStage`（target/focus/write/verify/submit）。焦点 tap、SetText 与提交原语各自在创建
`may_dispatch` 令牌的同一短锁内把原语推进为 `uncertain`、dispatch 推进为 `unknown`；
持久化与最终授权失败回退为确定未派发，回执返回后再按终止/确定未派发/不可用分别推进。
ref 目标在新鲜帧中已聚焦且通过完整绑定时跳过焦点 tap，其他目标只做一次必要聚焦。

写入完成证据是一次 SetText 的完整终止回执、`candidateCount=1`、`issued=true`，以及固定
helper 对同一候选节点刷新后的完整 value 与请求精确相等；`accepted` 只是框架回执，不单独
否决。engine 不做写前 GetText，也不以另一棵树的重新采集、重绑定或 GetText 证明写入。候选成立后
在同一短锁提交 `write=executed`；`auto_submit=false` 同时提交 `KnownOutcome=executed`，复合分支
保留提交的独立门禁。

复合提交在 write 已确认后进入 `stage=submit`，至多执行一种提交原语：

- `submit_ref="enter_key"`：先核对前台任务与观察时相同，再用写前目标做一次精确
  `GetTextOnDisplay`；候选唯一且完整值相等时直接进入 ENTER 门禁。仅当传输无错且
  `candidate_count=0` 时，允许一次写后 `Capture` 重绑定：新帧须同显示、同前台任务且拓扑完整；
  候选须为同包同类、可见、可编辑、已知聚焦/启用且非密码的输入框，完整值等于请求文本。有资源或
  描述锚点时，该锚点在写前帧与写后帧中都须唯一；无锚点时，全局满足安全条件且值相等的候选须唯一。
  几何接受键盘顶起的同水平带、清除按钮导致的同垂直带内水平伸缩，或重叠率 ≥0.50。重绑定后须用
  新目标再做一次精确读回，全部通过才发送至多一次 `ENTER`。
- `submit_ref="n…"`：认领前按完整 `tap` 规划器（等效 `bubble=true`）静态验证提交节点，并允许该
  节点在写前显式 `enabled=false`；其他结构、能力与安全落点判据不变。写后重新采集新鲜帧，核对同
  显示与同前台任务，并对计划涉及的执行节点（含 `coextensive_child`、`bubble_ancestor`、
  `bubble_colocated` 的委托节点）走 Tier 1–4 与安全落点重验；键盘顶起导致仅 Y 轴平移时允许同水平
  带的唯一匹配回退。写前显式禁用的执行目标必须在新帧中显式启用。首次帧仍显示禁用或布局尚未落定
  时，可在可中断等待 150ms 后复采一次；全部通过才发送至多一次 `Tap`。节点提交失败绝不补发 `ENTER`。

`text_input` 的合法组合只有 `(not_executed, not_executed)`、`(uncertain, not_executed)`、
`(executed, not_executed)`、`(executed, uncertain)`、`(executed, executed)`；`submit` 非
`not_executed` 必须已有 `write=executed`。整体 outcome 由累计事实推导：写入已确认且无需提交或提交
已确认时为 `executed`；写入已确认、自动提交确定未派发（`dispatch.submit` 为
`not_attempted`/`not_dispatched`）且 focus/write 均无未知派发时为 `partial`；任一原语可能已到达
Android 时为 `uncertain`；全部确定未派发时为 `not_executed`。`partial` 只表达“已确认写入、确定
未提交、没有未知物理副作用”，不是降级的 `executed`。

`partial` 使用 `409 text_submit_not_executed`。原会话仍活动时，engine 复用最后一次合法写后帧
（无图时）或执行一次标准采集，发布一个新的 `ready O` 随结果返回；首次结果在该 O 有效时省略
`recovery`。重复 P、`status(P)`、冻结结果与原帧不可交付时稳定投影 `observe(P, retired)`，并叠加
实时会话/耐久/设备/忙覆盖；冻结、撤权与交付失败不发布新 O，交付失败的结果可带
`observation.status=failed` 的占位材料而不带 O 编号。写入已确认但提交已派发而回执未知时
保持 `uncertain`，绝不发布 `partial`。

冻结从累计事实推导整体 outcome：全部原语未获派发许可才是 `not_executed`，写入确认且提交确定未
派发为 `partial`，其余（包括焦点 tap 已派发但写入未派发、写入未确认或提交回执未知）为
`uncertain`；原会话被撤销用 `409 control_revoked`，仅工作期限到期且原会话仍活动用
`409 text_submit_not_executed`。`currentStage` 与 dispatch 快照随冻结错误一起投影，reason 为
`authorization_revoked` 或 `deadline_exceeded`。status(P)、重复 P 与冻结结果只复制最后一次已提交
快照，迟到回执不能回补，也不持久化为普通动作历史。已认领 text 的恢复投影在只读结果副本上叠加实时
会话/耐久/设备/忙覆盖；临时阻塞不写回原操作保存的语义入口。

### 条件等待（wait_for）

`wait_for` 是普通工作（`WorkWaitFor`）：先在唯一工作位上等待一个字面条件或内容稳定，再在同一
工作位内做一次验证采集并发布一个新 `O`。

- 准入在单一状态锁内完成：引用解析、已过期会话拦截（`409 session_expired`，绝不复活）、
  普通屏障与工作位、工作记录、当前 `full` O 的基线确认与 `SessionTTL=120s` 单调续租。已过期会话不被本次
  调用续租，也不被推进成 `control_revoked`。
- 等待期只采样、不发布：每帧读一次目标屏层级并用只读投影计算摘要，不发布、不推进 ref 连续号、
  不确认基线。`stable=true` 比较屏幕语境与公开树内容。摘要忽略所有节点的 `bounds` 与
  `ref`，以及无动作叶子的 `label`、`value` 和 `hint`。`stable=false` 的条件等待仍以
  原始身份链、完整公开树与屏幕语境比较帧。采样最小间隔 `MinDwell=600ms`；稳定判据是连续相等帧数达到 `StableFrames=3`，
  不是累计采样数，因此 3 帧至少跨越 1.2s；`MaxSamples=80` 只计有效可读帧，
  `ProbeOnFailStreak=3` 决定连续失败后何时重探目标显示，
  `FrameTimeout=16s` 把单次慢读限成瞬态失败。这些值是服务策略，进程启动时校验，调用方不能设置。
- 退出分支（`query`、`absent_query`、连续 3 帧稳定、`timeout` 到期、采样上限守卫）在进入
  验证采集前冻结为 `wait.trigger`；命中优先级为 `query > absent_query`，到时与守卫都记
  `timeout`。
- 验证采集先 `ProbeDisplay`，需要图片时先读图。终帧来源固定为三种：条件或稳态退出的
  命中候选（`matched`）直接复用；到时或采样上限退出时，若最后一次轮询尝试成功、该帧在
  `CaptureMaxAge=15s` 内且之后没有失败尝试，复用该帧（`terminal_reuse`）；不能复用时只做
  一次终帧读取（`terminal_capture`）。所有来源都经 `projectCapture` 与
  `publishTreeAttempt` 的会话、工作权、显示代次与采集龄门禁；复用帧在提交门禁过期时返回
  既有 `stale_screen`，不发布新 O，也不再读一次终帧。
- `observation.quality` 永远描述最终发布的那一帧：命中候选保持
  `condition`/`stable` 口径（连稳 3 帧为 `stable`/`stable`，否则 `stop_reason=condition`）；
  `terminal_reuse` 不新增样本，连稳 3 帧为 `stable`/`stable`，否则 `samples>=2` 为
  `unstable`、`<2` 为 `unknown`，`stop_reason` 为冻结的 `timeout` 或 `max_samples`；
  `terminal_capture` 把终帧并入同一比较：没有有效帧为 `unknown`/`0`/`not_sampled`，连稳
  3 帧为 `stable`/`stable`，否则 `stop_reason` 为 `timeout` 或 `max_samples`。
- `wait.matched` 只在最终发布帧上判定，只含 `query`、`absent_query`、`timeout`；`stable`
  由 `observation.quality.state` 表达。空列表时整个成员省略。`wait.elapsed_ms` 计自准入
  完成到终帧发布。
- `wait_for` 不认领旧 `O`，只在新 `O` 发布时把上一个 `ready` `O` 置为 `retired`。

### 只读截图（snap）

`snap` 是普通工作（`WorkSnap`）中的单张图片读取，不读树、不派发输入、不发布 `O`：

- 准入在单一状态锁内完成 P 登记或去重、原引用解析、活动会话与原租期、App/模式、设备、
  普通屏障、请求未取消与工作位空闲检查。通过后才把 P 绑定到目标会话 origin，固定会话
  epoch 与显示 ID/代次/几何快照，设置 `WorkSnap`；接纳拒绝的 P 不获得会话归属。
- 工作上下文继承调用方请求，整体上限 25 秒。锁外先 `ProbeDisplay`（至多 5 秒）取得目标
  逻辑屏的实时 SFID，按该 SFID 读取一次源图（至多 10 秒），再探同一逻辑屏确认 SFID；
  缺失或不一致即丢图。随后做一次编码。层级 RPC、树构建与差异计算都不参与。
- 提交是唯一发布点：短锁内重验 P 仍运行、工作位仍由该 P 持有、原会话 epoch、App/模式、
  STOP、原租期、请求与工作期限、会话绑定显示 ID/代次与几何快照，并检查源图读取开始距提交
  不超过 `CaptureMaxAge=15s`。STOP/撤权、断连或截止先于该判定时丢弃候选；提交先通过时 P
  保持成功，随后断连或回包丢失只影响本次交付。
- 成功只交付一次顶层 `image`；重复 P 与 `status(P)` 只重放有界操作摘要。`snap` 不确认基线、
  不续租、不认领或退休 `O`，也不生成动作 `outcome`。终态省略工具级 `recovery`。
- 请求取消只传播到本次读取，不撤销会话；STOP/`end` 先撤权、取消读取并触发独立收尾，原工作
  的逻辑交接上限仍为 5 秒。未提交分支按会话撤权/租期、请求取消、工具截止的顺序冻结为
  `revoked`、`cancelled` 或 `work_deadline`；晚到的读取结果只能使用冻结摘要。

## 4. full/delta 与提交条件

一次采集（`captureAndPublish`）的固定步骤：

1. 复核工作未撤销、会话仍属于该工作；
2. `ProbeDisplay` 确认目标逻辑屏存在；
3. `start` 的首个观察、`act` 的后观察与 `app` 五个变更 kind 的后观察先做收敛采样：逐帧
   `Capture`，只有当连续两帧的三层摘要全部相同才停止——节点内容身份链（严格的
   `identityOf`，与 ref 动作的分层匹配分开）、公开树的规范编码、前台状态/包/任务与显示几何。采样间隔取上一帧的实测
   耗时，相邻两帧的采集起点间隔不小于该阶段的静默窗口，帧数与总墙钟分别有上限；预算耗尽
   照常发布最后一帧，不判采集失败。当前取值（静默窗口/总预算）：`act` 400ms/2s、
   `start` 300ms/1.5s、`app.open` 600ms/3s（其余 app 变更沿用 `act` 档）；它们是服务端固定
   策略，不是请求字段；
4. 取得发布用层级与前台：有收敛采样时用收敛帧，否则 `Capture` 读取一次；两者在同一次读取
   中取得；该次收敛结果投影为 `observation.quality`：`samples` 是本次有效可比较帧数，
   `state` 由最终发布帧参与的最新比较决定；`observe` 与重验失败交付不做收敛采样，填
   `unknown`/`0`/`not_sampled`。质量是观察材料事实，不改变 O 状态、输入授权与提交门禁；
5. 采集龄超过 `CaptureMaxAge=15s` → `stale_screen`；
6. 虚拟屏上出现控制 App 的可见节点 → `control_ui_on_virtual_display`，采集失败；
7. `observation.Builder` 构造完整的内部公开树（合成根、必要祖先、保守保号、`role=scroll` 节点的
   `axis` 投影）。动作规划、重验与详细补看读取完整快照。compact 投影、序列化与计量都在锁外完成；
8. compact 投影先按[几何策略](interface.md#node)决定每个节点的交付几何，再省略符合条件
   的小型无标注图片，清理因此变空的匿名容器，并绕过裁剪后恰有一个孩子的隐藏匿名容器。
   留存节点沿用裁剪前的公开几何。输出普通节点定义并按规范 JSON 计量，
   超过 `MaxFullTreeBytes=128 KiB`
   → `tree_too_large`（`details.budget=public_full`）；同一快照的内部计费对象
   （`raw_capture` + 完整内部树）超过 `MaxSnapshotCostBytes=4 MiB`
   → `tree_too_large`（`details.budget=internal_snapshot`）；
9. 在锁内重验：会话仍 `active`、`epoch` 不变、工作未被撤销、仍持有工作位置、显示 ID 与
   `generation` 未变、候选 capture 自身的显示 ID/代次与当前显示一致、采集未过期；
10. 对唯一留存快照做有限 dry-run；连同受保护集合仍超过
    `MaxRetainedSnapshotCostBytes=32 MiB` → `tree_too_large`
    （`details.budget=retained_snapshots`），且不先删除历史；可容纳时一次应用淘汰、扣费、
    提交构造器连续性并发布新 O。

第 9–10 步与 full/delta 选择抽成单一发布步骤 `publishTreeAttempt`（第 7–8 步由
`projectCapture` 完成）：普通采集与 `act` 重验失败交付的帧共用同一提交门禁。交付路径在
派发前失败时复用重验已经读取的那一帧，不重新采集、不做收敛采样、不确认基线，只把该帧投影、
校验并提交为新的 ready O。

`wait_for` 的验证采集直接进入同一 `publishTreeAttempt`，不经过本节的收敛采样：等待期的只读
采样不发布、不推进基线；命中或稳态退出复用候选帧，到时或采样守卫退出时若最新成功帧仍新鲜
且之后没有失败，复用该帧，否则只读一次终帧。它的 `observation.quality` 由等待比较得出
（见 §3 条件等待）。

净化仅改变公开投影：默认空叶不发布，原始事实仍供动作重验使用；非目标 display 包装层不发布，
目标后代按最近公开祖先挂接并保持 raw 先序。Source 与 ContextSource 互斥且完整覆盖公开 ref，
context 只承载阅读祖先关系、不提供动作能力。五个状态保留来源三态，合成根使用已知默认。

连续性在公开父索引上按六阶段运行：唯一匿名结构；六个局部/全局内容与身份 pass；基于完整、
无冲突后代配对集合的父容器投票（3A 同锚定父优先、3B 经后代最近公共祖先自底向上跨重包裹锚定，
不再要求容器匿名）；新锚点下局部恢复（与投票交替）；最后一次零位移 ordinal 恢复及局部收尾
（ordinal 桶按 `role`/`actions`/`protected`/`hasRes`/`resource`/`label` 分组，可交互时含
`class`/`pkg`，且需要进入该阶段前已有的同 rank 独立强固定点，不能相互造证据）；最后单次静止几何
收尾，只处理同已锚定父下未匹配、非 `context`、非 `editable`、无公开子节点的叶子，要求同桶旧新
数量相等且至少 2 个、四边差均不超过 4 像素、严格一对一且旧序对应当前序严格递增；桶键为
`role`/`actions`/`protected`/`hasRes`/`resource`/`label`，可交互时另含 `class`/`pkg`。静止几何阶段
不匹配容器或输入框，因此不会在列表整批换内容复用槽位时让新卡片偷走旧按钮 ref，也不会让两个
无锚点输入框串号。
resource 与非输入 label 同时参与内容身份；局部 resource-only 回退只在同一锚定父下双向唯一时
退让文案。全局唯一性始终按完整两侧树统计，不能因先匹配/消耗重复项制造唯一。无锚点输入仅
允许同父双向唯一匹配。未知/歧义/预算截断降低保号率并分配新号，不猜测目标。

匹配工作预算为 `Wmax=128×(Np+Nc+1)+65_536`，其中 Np/Nc 为两帧公开节点数；未完成批次整体
丢弃。新号按公开先序从会话高水位单调分配；Candidate/Preview 丢弃不推进 prev 或高水位，
最终提交门禁通过后才更新连续性，旧快照保持不变。

### 图片附加材料

`observe(image=true)` 与 `act(image=true)` 的后观察在同一次采集中增加一次目标屏源图读取：
先确认显示身份，以运行时 SurfaceFlinger 标识读取源图，成功读到源图后再确认同一显示身份，
然后读取层级；图片编码在树读取之后执行一次。图片与树共享同一个提交门禁：核心树失败不发布
O，图片成功也不单独发布；核心树有效而图片失败只把本次观察标为 `partial`，不改变树与 O
的有效性。

`act(image=true)` 的收敛采样在源图读取之前完成：采样插在图与树之间会把两者的跨度撑过固定
预算。源图读取之后再读一次树作为发布用层级，使图片与树描述同一时刻附近的状态；这次读取
与收敛帧不一致时，在剩余预算内继续收敛，预算耗尽则发布该帧。刷新帧计入 `samples`，其最新
比较决定 `observation.quality.state`；历史相等对不会把最终发布帧改判为 `stable`。

- 图片来源必须绑定本次目标逻辑屏与运行时标识；不读取主屏代替，不跨屏跟随。
- 源图尺寸必须与目标显示几何一致；只做一次等比缩小（不放大）与一次 JPEG 编码，输出
  媒体类型固定 `image/jpeg`。
- 图片读取开始与树采集开始的跨度受固定预算约束；超跨度、编码失败或超字节预算只报
  `image_*` 错误，不重试、不换质量、不改判主动作。
- 附加图片时 `actionable_until` 从两者更早的实际读取起点计算，编码或交付时间不能延长。
- 图片与树都不进入账本；图片字节不参与 full/delta 编码与比例判断。
- 图片只作为本次结果的附加材料交付，不保留历史图片，也不提供图片补取工具。

### full/delta 与基线池

公开 delta 相对基线池中任一已确认的 compact full 计算。基线池只保存同一会话 origin、同一目标
显示 ID/代次且采集龄严格小于 `180s` 的完整观察，最多 4 条。令
`span = 本次待发布 revision - full.revision`。当前基线在 `span=1..10` 时可用，其他备用 full
仅在 `span=1..4` 时可用；无当前基线时至多保留 3 条备用，为下一次确认留出位置。成功确认一份
full，或成功发布引用某份 full 的 delta 后，该 full 成为当前基线。成功发布 full 清空当前指针。
本次因备用窗口而选回的 full 仍受 `span<=4` 限制，提交成功后从下一次发布起按当前基线的
`span<=10` 使用。

普通 observation 发布在锁外读取池快照，对每份合格 full 独立构造候选，提交时在短锁内重验：

1. 按上一棵已发布树投影本次采集，作为默认 full。默认 full 不推进正式连续性，只保存提交所需的
   候选状态。
2. 对每份窗口合格 full，用该 full 的内部投影对同一份采集重新匹配节点号，调用只读
   `CandidateAgainst` 生成候选 compact full，再从该 full 计算 delta，并完成重建校验。最终发布
   的内部快照、公开树、差分与编号状态必须全部来自同一候选。
3. 对候选 full 和 delta 分别使用正式的 `tree v4` 普通树编码，计量从 `tree v4 ...` 块头到
   `end tree` 行末换行的 UTF-8 字节；实际短 base、字段转义、缩进与换行都计入，`result` 信封
   与图片不计入。只有 `10×deltaBytes < 7×candidateFullBytes` 的候选合格，精确等于 70% 也不合格。
4. 合格候选按 delta 树块绝对字节数升序排列，长度相同优先当前基线，再优先 revision 较大的
   full。按该顺序尝试可留存的方案；没有合格 delta 可提交时发布默认 full。

任一候选超出公开树或内部快照预算时只跳过该候选。普通历史淘汰仍不能容纳所选方案时，报告超预算
的未选备用基线可以退出池，其观察进入普通历史淘汰集合后重新做有界留存规划；仍放不下则尝试下一
候选，最后尝试默认 full。提交门保护当前观察、认领中的观察、所选 delta 引用的 full 和提交成功后
仍在池内的全部 full。每次成功提交后，池按新的当前基线、revision 窗口、采集龄与显示归属裁剪；
容量仍超出时按 revision 淘汰最旧的未选备用。池退出与普通历史淘汰分别记账，退出池的观察仍可能
作为普通历史保留并供 `inspect` 读取，但该 full 不再参与 full/delta 选择。

候选投影、差分、文本编码和排序在锁外完成。引擎在提交短锁内规划有界留存预算，并重新核对原
会话、显示代次、工作拥有者、STOP/终态、期限、采集时效、预览 O 与候选基线；STOP 后到达的候选
不得发布。一次提交原子应用所选方案的留存淘汰、基线池、revision、O 与连续性状态。构造器在投影
阶段只产生候选，提交门禁通过后才推进 `prev` 与 ref 计数器；被拒绝的候选不改变下一次发布树的
ref。

`start`、强制 full 的 app 后观察、`observe(P)`、`wait_for(P)` 与 `full=true` 走强制 full 路径。
普通 `observe`、`wait_for`、`act` 后观察和文本 partial 交付使用全池选择。`act` 重验失败的交付
观察固定不进入全池选择：引用的 `obs` 是 `full` O 时只能使用该次认领的 full，引用的 `obs` 是
delta O 时交付 full。`start`、会话重建与收尾完成清空或过滤基线池。

差分规划先展开任何兼容输入中的历史链，再按原 ref 比较节点；输出普通 `add`/`replace` 定义，
并校验重建树的逻辑相等与规范 JSON 字节相等。可读文本块使用 `result v4` 的 `obs` 观察行、
`tree v4` 普通树和 `inspect v3` 详细补看编码；`both`/`structured` 的 JSON 保持完整 `Result`。
普通角色与动作按文本语法恢复。full/delta 候选选择按实际 `tree v4` 树块 UTF-8 字节计量，公开
树与内部快照仍按各自的 JSON 容量预算校验。

**基线的唯一确认入口**：只有被接纳的 `observe(O)`、`wait_for(O)` 或 `act` 对 `full` O 的认领确认
可以把当前 `full` 观察加入基线池；`start` 的首个 `full`、`act` 后观察和重验失败交付的 `full` 只是
候选。确认要求观察仍是当前留存观察、属于原会话 origin 与显示代次、在窗口内，并把它的 compact
公开树与完整内部投影一并保存。凡仍可能作为 `delta.base` 发出的 O 都保留完整树，
`inspect(reference=baseID)` 在其仍在池内时可读。客户端必须保留所有被引用的基线树，按
`delta.base` 应用到本地副本。

delta 无法表达（同 ref 角色变化、根变化或非法最终图）时 `Diff` 返回错误，该候选跳过；默认
full 仍可发布。基线与 delta 只证明两者可重建且差分满足文本门槛，不证明两个页面身份相同。

delta 的五类块：`set`（逐字段完整新值，显式保留 `false`/`0`/`""`/`[]`/`null`）、`add`、
`remove`、`children`（完整子列表）、`replace`（整段替换）。空差异只含 `format`、`base`、`view`。

五块是原子最终图声明。新增父收养存续节点时拆为顶层空 Add 组装根，并用 children 给出完整
最终子列表；存续节点跨父迁移必须消除旧父 claim。remove 根必须消失，其基线后代可经最终
可达边存活；不可达残余只允许来自删除/替换旧区域。Replace 根可迁移，其内部存续后代保持
基线父关系。双父、循环、孤儿及不可达显式声明整体拒绝。Node/Read 共用规划与应用器，Read
应用时先展开基线和 `add`/`replace` 定义中的历史链，按最终图重建普通节点 full。五个布尔
严格比较 unknown/false/true，完整节点省略已知默认，set 保留显式 null/false。阅读树、
inspect 与文本块不携带 defaults 声明。

## 5. 工作位置与并发

- 设备同时只有一项当前普通工作（`start`/`observe`/`snap`/`wait_for`/`act`/`app`/`shell`）。
  冲突立即返回 `409 device_busy`，不建普通动作队列；同一 Agent 会得到
  `blocking_operation_id`。
- 会话撤权后，原 work、UI 与 shell 进入独立退休记录。旧逻辑交接从撤权起最多等待 5 秒；
  原调用提前取得终结证据时提前释放。到期仍无终结证据时，engine 冻结原操作结果、取消原上下文
  并脱离当前工作位置，最多以 `device_busy` 影响新工作到该边界。
- 交接完成只封闭旧逻辑派发权和提交权，不证明已派发的 Android 输入、shell/root 派生进程、
  RPC 或精确清理命令已经停止。本规范接受这些旧调用与新会话发生物理时间重叠的风险；
  它们的结果只能更新原退休记录，不能发布新 O、延长租约或操作新会话。
- 每次 UI 派发只拥有一个输入窗口：`may_dispatch` 表示调用尚未返回；返回时一次性记为
  `terminated`、`not_dispatched` 或 `unknown`。已关闭窗口不构成屏障，`unknown` 不阻止
  `observe`、`act` 等普通工具；用一次新 `observe` 收窄不确定性，不重放原输入，也无第二个 UI 期限。
- 普通工具之间唯一保留的跨调用执行屏障是受管 shell，最长至
  `run.DeadlineAt + cfg.ShellConfirmTimeout + 3s`；
  默认 30 秒执行预算结束后再约 8 秒。到界或超时/撤销后的终止无法确认时，以
  `termination=unknown` 封闭该链并释放工作位置；该不确定性本身不改变会话状态（超时/到界
  不会结束活动会话，STOP/end 撤销后仍为 `ended`）；精确 runner 身份移入
  单个内存孤儿槽，较新的封闭链替换旧项。收尾链至多再作一次精确终止尝试，未证明进程消失前
  不删除 runner 目录。`status`、`inspect`、`diagnose`、`end` 与 STOP 保持可用。
- `work.Deadline` 到期只冻结该 P（`phase=work_deadline`）：默认 `outcome=not_executed`，
  派发未知时为 `uncertain`，已有确认 outcome 时保留；取消其上下文并令 `e.work=nil`。
  会话、所有观察与收尾链不因该工作期限改变。
- 工作上下文按类型设上限：`start` = `StartTimeout + WorkTimeout`（40s）、`observe` =
  `WorkTimeout + 15s`（35s）、`wait_for` = `timeout + 40s`（41–80s）、`act` 与 `app` 的
  五个变更 kind 约 50s、`app` 的 `find`/`list` 查询 `WorkTimeout + 10s`（30s）、`shell`
  约 65s；具体超时是服务策略，不是请求参数。
- `wait_for` 使用条件等待工作（`WorkWaitFor`），其上下文继承调用方请求 `ctx`：客户端断连、
  MCP 取消、STOP 与 `end` 都立即关闭等待并释放工作位，不发布迟到观察；STOP 与 `end` 把
  `409 control_revoked` 交付给调用方，MCP 取消与客户端断连由适配器或传输层收尾。同一次等待的
  验证采集整体受 35 秒固定预留截止约束，与 `timeout + 40s` 的工作期限和固定的 90s 同步 HTTP
  上限共同构成调用预算。
- `app` 的 `find`/`list` 使用查询工作（`WorkAppQuery`），其上下文继承调用方 `ctx`，可被请求
  取消；取消后只关闭查询的发布资格与读取，`phase=query_cancelled`、无 outcome、无 error、
  无集合，普通工作位置最迟在 5 秒逻辑交接内释放。查询工作期限到期为 `phase=query_timeout`、
  `error=device_unavailable`，同样无 outcome。五个变更 kind 使用变更工作（`WorkAppAction`），
  按普通 O 认领/派发与 STOP 规则处理；MCP 对查询取消不发 `end`，对五个变更取消保留有界
  `end(P)`。
- `end`、STOP、`status` 不占普通工作位置，也不等待普通工作完成。
- 控制同步 `/v1/control/sync` 不占工作位置，不做设备 I/O；只更新 App 事实、处理命令回执、
  停止意图与消息确认，至多下发一条显示命令与一批未确认消息。

## 6. STOP、end 与收尾

### 停止边界

两个入口都归结为 `bindStopLocked`：

- 普通同步携带 `stop_pending`（`seq` 单调、`reason`）；
- 独立 `POST /v1/control/stop`（最小载体，不校验普通 `contract_version`）。

绑定条件：同一 `app_instance_id` 的 `seq` 必须严格大于已记录值；否则忽略（HTTP 响应仍为
`accepted=true`，`state=duplicate`）。成功绑定时：

1. 记录停止边界（`stopBoundary`，并写入账本 `stop`）；
2. 若存在未结束会话：`state=ended`、`reason` 为接受的停止原因（仅独立 STOP 的空值或超长
   原因改用 `user_stop`，同步 `stop_pending.reason` 原样保留）、
   会话的 ready O 退休、工作标记撤销并取消上下文；
3. 锁外启动独立收尾链；
4. 不等待设备工作、日志/账本刷盘或网络；已派发的 Android 命令不保证撤回。

停止后 `start` 仍会被拒绝，直到同一 App 实例在停止边界之后成功同步一次且不再携带
`stop_pending`；期间 `status.blocking_code=control_revoked`。

### end

`end(reference)` 解析 O 或 P 的原会话：撤权 → 建立唯一 `pending` 收尾记录 → 返回
`executed` 与当时的 `cleanup`。第二次 `end` 只观察同一条收尾链，不创建第二条破坏性链。
对从未被接纳的 P，`end` 返回 `request_closed=true` 且 `cleanup` 全为
`not_applicable`。已结束会话且尚无收尾记录时，`primary` 模式或“无显示且无创建结果”直接
记为 `not_applicable`，否则补建收尾。

P 已无法定位时，`end(P)` 返回 `409 operation_unknown`、`phase=reference_unknown`、
`outcome=uncertain`、`request_closed=true`，返回 `session:null` 并省略 `cleanup`。该关闭标记只封闭
该 P 的未来接纳，不证明过去未执行，也不会结束当前其它会话。

### 收尾链

`cleanup` 固定五元组：`state`（`pending`/`complete`/`unconfirmed`）、`execution`、
`tasks`、`recents`、`display`（每项可为 `pending`/`confirmed`/`unconfirmed`/`not_applicable`）。

- `pending` 表示该退休记录的有界自动补偿仍在运行，各项保留当时证据。
  `complete` 表示适用事实均已确认，且没有晚到执行边界使旧物理证据失效。
  `unconfirmed` 表示主动补偿已经停止且仍有未确认事实；它不表示仍应无限轮询，也不把未知
  改写成 `confirmed`。
- 每条退休记录从撤权时开始拥有总计 12 秒预算，最多三轮，轮间等待分别为 200 ms、800 ms；
  每次设备 I/O 使用剩余总预算且单次不超过 2 秒。预算不因心跳、`status`、重复 `end`、
  错误或进程重启而刷新。
- `execution`：UI 子步骤只在调用仍未返回时保持 `unconfirmed`；调用返回即关闭窗口，
  缺少可用结束回执时持久记为 `unknown`，不再单独使 `execution=unconfirmed`。
  受管 shell 则须精确链被证明结束或未启动（`confirmed`/`aborted`）才解除 `unconfirmed`；
  以 `unknown` 封闭并释放工作位置不证明链已消失，不能据此写成 `confirmed`。
  每个 UI 子步骤由引擎绑定原会话、P、step 和设备启动身份；主 outcome、任务归属、
  文字读回与调用终结分别判定，原 P 的 `uncertain` 不追认。shell 只解除自己的 runner 责任；
  迟到 UI 返回只补原执行事实，不发布 O 或恢复输入权。
- 迟到终结晚于原物理核验时，原物理确认失效并保存复核义务。旧任务 ID 留作 recents
  核验证据；只有原补偿仍运行且预算足够时才继续核验。主动补偿停止后，可信迟到回执
  仍可更新原事实，但不重新派发、不延长期限、不改原 P outcome。
- `primary` 模式：`tasks`/`recents`/`display` 全为 `not_applicable`，从不关闭用户应用。
- 虚拟屏且显示身份未知：只接纳与原创建命令身份匹配的描述；无匹配描述时保留
  `unconfirmed`。只有派发前参数拒绝可证明未创建；空 descriptor 或创建超时不能证明资源释放。
- 显示身份已知：App 实例未更换时下发精确 `close_display`（带
  `target_app_instance_id`/`target_display_id`/`target_generation`）。有界任务处理后请求
  精确关闭；即使显示已先关闭，仍执行原 ID 的 recent-only 路径。
- 任务补偿以原已登记 ID 与当前 live task ID 的并集为候选，先排除根级 `task_transfers`
  保护池中的任务，再每次删除前重验 boot、App 实例、原 display/generation、目标 ID 与当前
  资源冲突。live 或 recents 命中的原归属 ID 实际派发 `RemoveTask`，删除返回不等于成功，
  必须重读实时任务与 `dumpsys activity recents`。跨 boot、归属不足、任务移到主屏/其它归属、
  已交出或仍受迁移保护、或 ID 已被新资源使用时停止该目标的破坏性补偿。
- 任务全部消失且最近任务项也消失时 `tasks=confirmed`、`recents=confirmed`；仍有任务、
  不可读或身份冲突时相应记 `unconfirmed` 并进入有限重试或停止。无证据时不能按包名、PID、
  display 数字或“当前显示”扩大目标。
- 虚拟会话在内存维护受管窗口登记（`taskEntry`：task id、root task、包名、display、
  generation、ownership（`owned`/`borrowed`/`shared`）、origin_display、proven，上限 16）；
  它是每次动作前由实时任务列表核对的缓存，不跨会话或重启继承控制权；主屏会话不建立该
  登记。已交出或仍受迁移保护的任务不会因本屏扫描、旧快照或 `checkedTaskIDs` 重新变成 owned。
- `state=complete` 仅当 `execution`/`display`/`tasks`/`recents` 均不为 `pending` 或
  `unconfirmed`。完成时清除当前会话槽位、基线与显示命令；已发布 O/P 只按其有界缓存与仍可
  解析的原会话路由返回事实，不能改绑到新会话。
- `cleanup.pending`/`unconfirmed` 不决定 `availability` 或 `can_start`。同步仍可按原目标投递
  未逾期的 `close_display`/`cancel_create`；新普通工作到达后取消尚未派发的维护命令，不排在
  其后等待。状态响应可同时给出 `can_start=true` 与原退休会话的未确认事实。
- App 的当前 `display` 只描述当前 attempt，不回退到 retired attempt。retiring/旧 cleanup
  非空不阻止新 `create_display`；旧资源是否存在由原 cleanup 事实和 App 本地历史分别表达，
  `display=null` 不等于全设备无残留。
- 服务退出（SIGINT/SIGTERM）：撤权并启动收尾，等待已接纳的后台工作至多 25s，然后同步写一次
  账本；进程退出本身从不被当作资源已释放。

## 7. 恢复屏障

进程取得固定运行目录内核锁之后，才接触账本、App 与设备。`StartRecovery` 读取账本：

- 账本损坏/不可读 → `blockedReason=ledger_corrupt`，可用性 `blocked`，不接纳新会话；
- 无记录 → 等待 App 心跳、当前模式/STOP 与设备能力就绪后可开始；端口就绪不等于设备就绪；
- 有记录 → 要求 App 心跳、当前能力与设备就绪，再把旧 session 及其 UI/shell/create/cleanup
  责任转入有界收尾的退休记录，包括 `may_dispatch`/`unknown` UI 记录与旧 shell 身份；
  不恢复旧 O/P、主屏授权或普通派发权。旧记录中的 boot、App 实例、原创建命令、display
  generation、精确任务 ID 与私有预算随记录保存。恢复不要求重启手机；旧归属退休且当前能力
  就绪、账本可保存新归属后开放准入。
- 读取版本 1–5。版本 1–4 的记录按原格式升级到 v5；版本 3/4/5 中已发出预算且仍为 `pending`
  的退休记录，启动时先持久化为 `unconfirmed`，不授予第二次补偿预算。current 或旧格式记录
  转成退休记录时获得一次新预算；迁移快照必须先保存，再允许任何补偿派发。
- 载入旧账本后、解除恢复屏障前，服务收集旧主记录与全部退休记录的服务实例标签，以及会话
  `origin` 中的 `P<4位>_...` 标签；本次进程的 4 位 O/P 标签若与之重合，按固定顺序避让到第一
  个未占用值。标签只在此时可能调整，开始分配 O/P 后固定不变。
- boot 变化可作为对应终结证据；跨 boot 的物理清理保持只读，不按旧裸 ID 删除可能属于新资源
  的对象。旧 shell runner 只在完整身份匹配时探测或终止，不重放命令。
- 旧 `may_dispatch` UI/legacy checkpoint 保留为 `execution=unconfirmed` 的原归属证据；
  旧 UI 的窗口已关闭且无未证明结束的 shell 或 legacy 责任时，不再单独保持该值。
  未确认事实不产生永久准入封锁。

`can_start`（`status` 返回）只投影当前服务能力和当前权威冲突：内核目录锁已取得、账本可载入
且新归属可保存、无当前普通工作、无活动会话、App 心跳新鲜且
`background_running`/`notifications_enabled`/`stop_ready` 为真、模式就绪且代次匹配、设备
交互且未锁屏、无未确认停止意图。旧 cleanup 五元组、旧执行/任务/recents/display、旧补偿是否
停止、旧退休记录容量与 legacy UI 是否等过启动边界都不参与判断。逻辑交接最多以
`device_busy` 影响准入 5 秒。任一当前条件不满足时返回 `can_start=false` 与 `blocking_code`；
端口已监听、`/health` 返回 `ok` 都不是干净证明。

## 8. 账本

`internal/store` 把单个 JSON 对象写在 `<state_dir>/recovery-ledger.json`：

- 格式名 `mobile-use-vnext-ledger`，写版本 5；读版本 1–5，修订号单调递增；完整编码 ≤64 KiB；
- 单写者：包内互斥 + 进程锁；`Save` 先 fsync 临时文件，再 rename 覆盖，再 fsync 父目录
  （Windows 跳过目录 fsync，因此 Windows 不提供同等耐久证据）；
- 版本 5 在版本 4 的当前归属与退休明细上增加根级 `task_transfers` 迁移/排除池，并给任务记录
  `root_task_id`、`ownership`（`owned`/`borrowed`/`shared`）、`origin_display`、`generation`、
  `proven`。迁移记录含 operation_id、direction、state、epoch、boot/App 实例、包名、
  leaf/root task、源/目标 display 与 source generation；不保存请求、树或自由文本。退休记录
  **不得嵌入** `task_transfers`；
- 容量分区（按实际序列化计）：完整账本 ≤64 KiB；retired 数组 ≤8 项且 ≤24 KiB；base（不含
  retired 与 task_transfers）≤20 KiB；task_transfers 全局 ≤16 项、每项 ≤1000 B、数组 ≤16 KiB；
  根对象拼接开销 ≤512 B。独立分区不能替代完整编码门禁；
- 退休池仅在超过 8 条或序列化集合超过 24 KiB 时进入 `trimRetiredLocked` 容量门禁。门禁
  成立时，先压缩一条尚未压缩且单条编码超过 21 KiB 的退休记录：停止其 cleanup、收缩归属
  摘要，然后重新检查容量门禁；不存在这类记录时才实际驱逐。
- 驱逐顺序为 `complete` → `unconfirmed` → `pending`，仅适用于实际移除记录；同状态先移除
  开始时间更早者。被压缩或驱逐的未完成清理终止为 `unconfirmed` 并计入
  `dropped_unconfirmed`，不得写成 `complete`。只有
  不存在任何仍可删除同一目标的引用时，才回收对应 `task_transfers` 项；
  `prepared`/`may_dispatch`/`unknown` 的出站记录不得因腾空间被丢弃；
- 内容只有归属与资源证据：原 service instance、origin/agent/mode/state/App 实例/boot/device
  身份、模式代次、原 create command、显示 ID/SFID/generation/尺寸/密度/旋转/开始时间、
  待创建/待关闭命令、收尾五元组、停止边界、虚拟屏受管任务登记（≤16）、受管 shell runner
  身份与阶段、一个 UI 子步骤责任、legacy checkpoint、收尾核验过的任务 ID（≤256）与物理复核
  义务 `execution_recheck_required`；
- 明确不含：请求体、节点树、图片、输入正文、命令输出、异常字符串；
- UI 窗口状态为 `may_dispatch`、`terminated`、`not_dispatched`、`unknown`；`may_dispatch` 表示
  调用仍未返回，`unknown` 表示窗口已关闭但没有可用结束回执，是私有账本的持久终态，不改判
  原工具的 `uncertain`；
- `start` 在启动显示助手前同步写一次（原归属 + 创建意图），会话激活后再写一次；其余进度
  异步写。每个 UI 输入子步骤额外预写 `may_dispatch`，成功后重新竞争派发边界；调用返回后
  只记录一次关闭状态。窗口关闭不阻止后续普通工作；写失败仍置 `ledgerUnsafe`，可用性为
  `blocked`（原因 `ledger_unavailable`）；STOP 不等这些写入；
- 版本 1–4 升级保留原 Session/Cleanup/Stop/Shell/task 事实；版本 1–4 的任务仅按旧格式的
  原拥有关系升级为 `owned`，绝不根据包名推断 `borrowed` 或交出。v5 专属字段不得出现在标为
  v1–v4 的账本中；加载 v5 后禁止丢字段降存 v4。**首次 0.9.0 写入 v5 后不支持回退到 0.8.0**，
  只能使用同为 v5 的候选或前滚修复；不得删除账本、清 App 数据或忽略迁移字段。

账本只由 engine 读取与生成，HTTP 层与 App 都不写账本。

## 9. 事实分层与日志

同一次请求可能同时表达多层事实，互不替代：

| 层 | 字段 | 含义 |
| --- | --- | --- |
| 动作执行 | `outcome` | `executed` / `not_executed` / `uncertain` / `partial`；只描述本次输入是否已派发并有完成证据。`partial` 只由复合 `text` 产生，表示写入已确认、提交确定未派发且没有未知物理副作用，不是降级的 `executed` |
| 滚动效果 | `effect.state` | `observed` / `not_observed` / `unknown`；只在 `scroll` 结果与仍保留该原操作的 P 视图中出现，只表示是否观察到容器内内容位移，不表示动作因果、方向、距离或到达边界。`observed` 要求屏幕位移与容器相对位移同时成立且同号对达到门限，`not_observed` 用完整匹配集与容器四边不变判据，二者都不证明原始内容静止 |
| 观察 | `observation.status` | `complete` / `partial` / `failed`；后观察失败不改写已确认的 `executed` |
| 条件等待 | `wait.trigger` / `wait.matched` / `wait.elapsed_ms` | 等待因哪个分支退出、最终发布帧上成立哪些单帧条件、整次调用耗时；不表示页面之后不再变化，也不替代 `observation.status` |
| 观察质量 | `observation.quality` | `state` / `samples` / `stop_reason`；表示按该阶段的摘要判据是否收敛、经过多少有效采样，不限制输入授权 |
| 图片 | `observation.image` | 本次附图的元数据或受控错误；图片失败不改写核心树与主动作 |
| 会话 | `session.state` | `starting` / `active` / `ended` |
| 物理收尾 | `cleanup.*` | 显示/任务/执行链是否确认释放 |

`outcome`、`effect`、`wait` 与 `observation.quality` 各自独立：`executed + effect=unknown`
是合法结果；`unstable`/`unknown` 的观察仍可发布、仍可被认领，是否执行由动作自己的判据决定；
`effect`、`wait` 与 `quality` 都不改写 `outcome`、`observation.status`、错误或恢复入口，也
不改变 O 的 `status`、`actionable_until` 与基线确认。`partial` 同样不改写后观察、`effect` 或
`cleanup` 事实；只判 `executed` 的调用方必须把它当作未完成动作。`wait` 在 `Result` 中固定位于
`shell` 之后、`notification` 之前；`status(P)` 与重复 `P` 的视图保留已保存的 `wait`，并按既有
规则省略 `observation`。

事件日志只记白名单键（ID、枚举、布尔、计数、时长），值经过单行截断；不记录树、差异、输入
正文、命令、原始响应或异常文本。每个 UI 窗口关闭只记一条 `ui.window_closed`，
`state=terminated|not_dispatched|unknown`；该事件不进入任何工具响应。受控滚动的规划、重验与
效果事件只带 `operation_id`（必要时 `observation_id`）与受控枚举/布尔/计数，不记 ref、坐标、
标签或树；`scroll.revalidate` 的 `start_scope_proven`/`end_scope_proven` 只描述两端，
整段证明由独立的 `proven` 布尔值表达；`scroll.effect` 的 `anchors`/`coverage` 仍按完整
匹配集计数；观察质量事件只记可取得的 `operation_id`、`observation_id`、阶段与受控枚举/数值。
目标重验与 delta 选择只允许记录命中层级 `tier`（1–4）和 `baseline_pool`（`primary`/`historical`/`full`）。
`primary` 表示当前基线候选胜出，`historical` 表示备用 full 候选胜出，`full` 表示未选 delta；
该事件只在成功提交后记录，不包含候选 ID、树或差分。
text 完成事件记 `focus_dispatch`/`write_dispatch`/`submit_dispatch` 与受控 `stage`/`reason`/`count`；
`changed_ref`、输入正文与读回值绝不进入日志。
新增键必须先进入 `internal/eventlog` 的单一白名单，否则事件被静默丢弃。

## 10. 消息投递与发布边界

### 消息

`notify` 只保存消息并把它放入控制同步队列（`notification.state=pending`）。消息队列有界
（256 条 / 1 MiB 正文），未确认项持久化在 `<state_dir>/messages.json`，与恢复账本分离；
服务重启后按原 ID 重发，App 按 ID 去重并确认已保存。确认只释放服务端队列，不表示显示或
已读；App 的已读是本机事实，不回报 Agent。消息正文不进入日志、错误、账本、恢复记录、
工具结果投影或发布清单；消息存储故障不改变 STOP、清理与准入。

### 发布

`release/versions.json` 是发行号、共同契约号与 Android versionCode 的唯一来源；当前为
`release_version=0.31.0`、`contract_version=27`、`android_version_code=33`，构建生成
Go/Java 常量。`scripts/build-release.ps1` 产出整组产物与 `build/release/manifest.json`
（源码提交、Go 版本、MCP SDK/协议、各产物 SHA-256/大小、Android 包名/版本、工具目录指纹）。
工具目录固定为 `scripts/toolcatalog` 投影的 13 个工具，指纹是该目录注册顺序、名称、描述与
入参 schema 的 canonical SHA-256；描述或 schema 变化都会改变指纹，因此清单必须在对应源码
树上重建。该指纹不覆盖 server instructions；`mcp-probe` 把 live `server/discover` 返回的
说明与构建内固定说明源逐字节比较，并附加兼容 `initialize` 检查。
清单必须来自干净的已提交工作树：`scripts/verify-manifest.ps1` 拒绝已有跟踪改动的工作树、
`source_commit` 不是实际 `HEAD` 前缀、`source_dirty=true`、`complete=false`（部分构建）与
缺失必需产物的清单，并重算产物哈希、Android 身份与工具目录指纹。

主机侧工具由 `scripts/build-host-tools.ps1` 用同一版本源构建，并在
`build/host-tools-manifest.json` 记录发行/契约、源码身份、工具目录 count/sha256 与两个
exe 的大小/SHA-256。`-VerifyOnly` 只核验而不构建，并且是发布门禁：清单与当前工作树都必须
在 `HEAD` 且 `source_dirty=false`，完整手机发布清单 `build/release/manifest.json` 必须存在
并与本次主机发布互为 companion（发行、契约、工具目录一致，手机清单的 `source_commit` 必须
是主机清单提交的前缀，且手机清单 `source_dirty=false`），companion 比对必须 PASS；清单缺失、
部分构建、非 companion 或比对 SKIP 都失败。`-AllowDirtySource` 只用于开发构建：清单记录 `source_dirty=true`，永远不能通过
`-VerifyOnly`。

部署按清单核验后覆盖安装，服务与 App 的契约必须精确相等；App 私有数据保留，不使用卸载/
清库解决签名或配对。部署脚本只停启本仓库的 `com.mobileuse.vnext` 控制 App 与
`device-service`（含手机侧 MCP 适配器进程），不包含对其他项目的兼容停启。MCP 宿主必须重启
实际进程并重新发现工具，替换磁盘文件不等于发布完成。
发布与回退的受控顺序见[发布与回退](release.md)。
