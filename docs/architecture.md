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
  server instructions（完整树语法一次加跨工具调用规则），由 `server/discover` 与兼容
  `initialize` 发布；13 个工具描述不内联树引导。
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
  `min(captured_at + 60s, session.expires_at)`。
- 保留上限 `MaxObservations=32`；会话的 O、基线 O、已认领 O 受保护，其余按采集时间淘汰。

### P（操作）

`P` 的公开状态为 `running → finished`；不存在的 P 返回 `409 operation_unknown`。

- 每个工具在任何设备副作用之前登记 P
  （`registerOperation`），可用 `X-Mobile-Use-Operation` 预分配；客户端传入的 P 按原值登记。
  缺省时服务生成带当前服务实例前缀的不透明 `P…`。格式为 `P` + 字母/数字/`-`/`_`，总长 ≤64；
  调用方不得解析编号中的数值，也不得跨服务实例复用编号。
- 同一 P 重复到达只返回原操作的公开视图，不再次派发（包括已失败的操作）。
- 已完成操作的公开视图（重复 P、`status(P)` 与冻结结果）不带 `observation`；原操作交付过新
  观察时，视图中的 `recovery` 退化为 `observe`/`P`，不指向该视图无法定位的 O。
- P 记录上限 `MaxOperations=256`；淘汰只针对最旧的已完成、且不被会话/工作/O 保护的记录。
- 已结束或显式封闭的 P（`end` 对未接纳/已结束的引用等）进入有界 `closed` 集合（最多 512）。
  有“未接纳”正面证据时返回 `request_closed=true`、`outcome=not_executed`；记录淘汰后只剩
  “未来接纳已封闭”证据时，`end(P)` 返回 `409 operation_unknown`、`phase=reference_unknown`、
  `outcome=uncertain` 与 `request_closed=true`，不声称原动作未执行，也不结束其它会话。
- 服务生成的观察与 P 编号包含当前服务实例前缀，并在该实例内单调分配；会话自身有 `epoch`，
  提交与恢复都用它识别“原会话”。

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
- `open` 在虚屏优先复用同包 live 任务（先前台、否则最小 task ID），否则新建并登记 owned；
  主屏 open 返回 shared、不登记。`claim` 要求主屏 live task 精确且 root 只有单个 leaf；
  `handoff` 每次只交还一个任务，默认不恢复前台。`close` 冻结不超过 16 个本会话管理的 live
  目标，逐项记录 outcome 与独立 recents 核验；`complete` 仅在全部关闭且 recents 全确认时
  为真。`kill` 仅对本会话 live owned 且同屏唯一可归因、非前台、无已知 FGS/播放（或显式
  允许）的应用执行精确 PID `kill -9`，不 force-stop、不追杀重启。
- 五个变更成功提交后统一做一次原会话屏 full 无图后观察；后观察失败保留 executed 与
  `observation.status=failed`，不改写主 outcome；STOP 撤权后的迟到候选不发布。

### 受控滚动与原始手势

`act.operation.type=scroll` 是受控滚动尝试：调用方声明阅读方向与幅度，服务在几何合法且作用域
可证明的容器内选择起止点。授权单位是同一可证明的滚动容器与新鲜轨迹，不是起点叶节点：

- 容器在观察的公开树中解析：`ref` 必须直接是 `role=scroll`、`actions` 含 `swipe` 的节点，
  服务不做祖先晋升；坐标候选归约到最近的可滚动祖先，出现多个不同滚动作用域时要求调用方
  显式给 `container.ref`；
- 静态规划固定容器正交中心坐标，用一次整树遍历把每个节点归约到最近的公开滚动作用域，把
  具有可用最近滚动作用域、且不属于选定容器及其 `raw` 外层祖先的候选投影成轴上的阻塞区间；
  没有滚动祖先的候选不阻塞，最近作用域来源未知或越界时按阻塞处理且不回退外层。再从
  「裁剪矩形 ∩ 公开矩形」的基础区间中扣除；最长可证区间决定 `page`/`half` 的留白与幅度，
  等长区间按手势起点侧取定；
- 认领后容器走 Tier 1–3 双向唯一匹配；轴向伸缩仍需足够重叠，离轴两边受固定容差限制。
  新鲜树先证明原段；若失效，只在同一容器中重新归约安全区间并规划/证明整段。实际
  `scrollFrame` 的起终点与受控时长进入设备 Swipe；没有安全段则 `target_changed`；
- 静态不可执行在认领前拒绝，新鲜不成立走重验失败的交付路径。

raw `swipe` 仍是原始手势：起点 `from` 继续走完整内容身份、祖先链与同点解析证明，只增加有界
`duration_ms`，不承诺滚动语义。`scroll` 不证明真实滚动方向、触摸消费者、业务安全或到达边界；
设备事实仍只有 `Scrollable bool`，公开 `axis` 没有生产者。

### tap/long_press 的子树安全落点

`tap`/`long_press` 的 `ref` 目标在有效 `bubble=true`（省略或显式 true）时，先判断原目标自身
是否有请求动作：有动作时仍走直接目标规划；完全没有 `tap`/`long_press`/`text` 动作时，沿公开
父链找到第一个 touch-interactive 祖先 A，只把本次动作规划给 A 一次。A 不存在、不可用或
不支持请求动作时静态返回 `422 unsupported_node_action`，A 支持动作但没有可证安全点时返回
`422 no_safe_point`，都不继续寻找更远祖先。source 映射缺失或层级损坏仍按各自的
`409 node_not_found`/`503 hierarchy_unavailable` 拒绝，不降级为可执行提升。静态 plan 同时
绑定原 child C 与 A；认领后
分别 Tier 1–3 匹配两者，并要求 A 仍是 fresh C 的祖先和最近交互祖先、C 的公开 Role 与完整
Label 未变、C 未新增 touch-interactive 能力、A 仍支持动作，随后在 A 作用域内重算安全点。
C 或 A 丢失/歧义、C 内容被替换、出现更近交互祖先或点落到另一独立交互目标时返回
`target_changed`，零派发；C 丢失时不能只凭 A 唯一而解绑授权。`bubble=false` 保持直接目标
算法与错误；坐标目标不进入该分支。

`tap`/`long_press` 的 `ref` 目标在静态阶段规划一次固定落点：以目标 raw 矩形、逻辑屏与公开
bounds 的非空交集为基础，只在目标子树内扣除已知交互节点及其后代和 context 边界，依次测试
原中心或交集中心、承载节点中心、中心线最长可证区间中点。候选点必须同时避开阻塞区，并让
目标子树内包含该点的最深非 root、非 context 节点唯一属于目标非阻塞作用域。没有可证候选时
在认领前返回 `422 no_safe_point`；`reason` 区分 `blocked`/`ambiguous`/`owner_mismatch`/`mixed`，
有界 ref 列表分别给出阻塞 owner、首个失败解析层的最深并列节点与整树唯一但越界的 owner。
目标结构或几何不可用则由 `422 invalid_target` 的 `reason=root`/`context`/`no_geometry`
表达。候选全失败后另做固定 3×3 网格探测，只有同时通过分支谓词与整树唯一归属/source 证明的
点才作为 `suggested_point` 返回，并可附 `suggested_carrier_ref`；它是提示而非执行授权，
不改变本次拒绝。`invalid_target`/`no_safe_point` 只覆盖 `tap`/`long_press` 的 ref 静态分支；
`unsupported_node_action` 仅在该分支按新分类收窄，`scroll` 等既有用法不变。`occluding_ref`
不再出现在该静态候选耗尽出口。

认领后 ref 目标使用有界 Tier 1–3 双向唯一匹配：L1 不比较祖先 bounds、Focused/Selected，
Tier 1 保持目标 bounds，Tier 2 允许受限微移；Tier 3 只移除三项内容均已知空且状态/能力已知
安全的透明层，并要求非根强锚点。先证明原点；失败后可在原安全载体内重算。载体失效时必须
有目标自身独立的稳定内容或双端唯一 resource，才可在同一目标子树安全重选；不向祖先扩权。
实际 `tapFrame` 冻结新鲜坐标/载体供派发使用，坐标输入仍保持原点证明。无候选/歧义返回
`409 target_changed`；预算耗尽、畸形树或投影损坏返回 `503 hierarchy_unavailable`；
撤权优先返回 `control_revoked`，不交付新 O。判据细节见[接口契约](interface.md)。

`422` 的祖先引导只对自身不含 `tap`/`long_press`/`text` 动作的
非 root、非 `context` 目标尝试；父链上的第一个交互节点是唯一候选，不满足动作、几何、source
或双重可证点条件时整组省略。`nearest_actionable_ref` 与 `safe_point` 原子出现，
`safe_carrier_ref` 仅在该点来自承载节点时附上；它们都只在同一观察内通过子树及整树归属证明
时给出，只解释可选坐标，不自动派发。默认 `bubble=true` 的一次 C→A 目标提升是独立契约；
该引导不把授权静默解绑为祖先。

该证明只覆盖公开树模型的目标子树，不覆盖子树外同级或跨窗口 z 序、未发布的透明触摸层、
应用自定义事件拦截或私有业务回调。树证明与 `executed` 都不证明 Android 实际事件消费者或
期望业务结果。

### text 的复合输入阶段

`text` 在任何有效 `press_enter` 分支下，engine 都在成功认领 O 的同一状态锁内初始化私有
`textStage`；仅有效 `press_enter=true` 另有公开不可变 `text_input:{write,enter}` 快照。
`textStage` 保存累计 focus/write/enter 原语状态、三个原语的 dispatch 事实
（`not_attempted`/`not_dispatched`/`dispatched`/`unknown`）、认领时的
sessionEpoch/AppInstanceID/ModeGeneration/显示 ID 与 generation 不可变绑定，以及
`currentStage`（target/focus/write/verify/enter）。焦点 tap、SetText 与 ENTER 各自在创建
`may_dispatch` 令牌的同一短锁内把原语推进为 `uncertain`、dispatch 推进为 `unknown`；
持久化与最终授权失败回退为确定未派发，回执返回后再按终止/确定未派发/不可用分别推进。
ref 目标在新鲜帧中已聚焦且通过完整绑定时跳过焦点 tap，其他目标只做一次必要聚焦。

写入完成证据是一次 SetText 的完整终止回执、`candidateCount=1`、`issued=true`，以及固定
helper 对同一候选节点刷新后的完整 value 与请求精确相等；`accepted` 只是框架回执，不单独
否决。engine 不再做写前 GetText，也不在写后重新采集/重绑定/GetText。候选成立后在同一短锁
提交 `write=executed`；显式 false 同时提交 `KnownOutcome=executed`，复合分支保留 ENTER 的
独立门禁。ENTER 只在 write 已确认、一次只读前台核对与一次同目标 GetText 均确认任务上下文与
完整值后，经同一 work/P、同一显示绑定和最终归属门禁发送一次；两键均确认时在同一临界区写入
`enter=executed` 与 `KnownOutcome=executed`。冻结从累计事实推导整体 outcome：全部原语未获
派发许可才是 `not_executed`，两键 executed（或 false 的 write executed）才是 `executed`，
其余（包括焦点 tap 已派发但两键未派发）均为 `uncertain`；`currentStage` 与 dispatch 快照
随冻结错误一起投影，reason 为 `authorization_revoked` 或 `deadline_exceeded`。
status(P)、重复 P 与冻结结果只复制最后一次已提交快照，迟到回执不能回补，也不持久化为普通
动作历史。已认领 text 的恢复投影在只读结果副本上叠加实时会话/耐久/设备/忙覆盖；临时阻塞
不写回原操作保存的语义入口。

### 条件等待（wait_for）

`wait_for` 是普通工作（`WorkWaitFor`）：先在唯一工作位上等待一个字面条件或整树稳定，再在同一
工作位内做一次验证采集并发布一个新 `O`。

- 准入在单一状态锁内完成：引用解析、已过期会话拦截（`409 session_expired`，绝不复活）、
  普通屏障与工作位、工作记录、基线确认与 `SessionTTL=120s` 单调续租。已过期会话不被本次
  调用续租，也不被推进成 `control_revoked`。
- 等待期只采样、不发布：每帧读一次目标屏层级并用只读投影计算摘要，不发布、不推进 ref 连续号、
  不确认基线。采样最小间隔 `MinDwell=600ms`；稳定判据是连续相等帧数达到 `StableFrames=3`，
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
7. `observation.Builder` 构造完整的内部公开树（合成根、必要祖先、保守保号）；投影、折链、
   序列化与计量都在锁外完成；
8. 从内部树生成 compact 阅读表示并按它的规范 JSON 计量：保留合成根与 `actions` 非空节点的
   矩形，省略无动作 `text` 与匿名普通节点的矩形，其余展示节点在矩形与父链最近交付者四边
   相同或裁剪后可见面积不足屏幕 1% 时省略；超过 `MaxFullTreeBytes=128 KiB`
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

公开 delta 相对已确认基线的同一 compact 阅读表示计算：先展开双方 chain，按原 ref 比较逻辑
节点，再把 `add`/`replace` 定义折回 chain 并校验 `Apply` 守恒。70% 判据用有界整数关系
`10×deltaBytes < 7×fullBytes`；等值退 full。构造器在投影阶段只产生候选，提交门禁通过后
才推进 `prev` 与 ref 计数器；被拒绝的候选不改变下一次发布树的 ref。

净化仅改变公开投影：默认空叶不发布，原始事实仍供动作重验使用；非目标 display 包装层不发布，
目标后代按最近公开祖先挂接并保持 raw 先序。Source 与 ContextSource 互斥且完整覆盖公开 ref，
context 只承载阅读祖先关系、不提供动作能力。五个状态保留来源三态，合成根使用已知默认。

连续性在公开父索引上按五阶段运行：唯一匿名结构；六个局部/全局内容与身份 pass；基于完整、
无冲突后代配对集合的父容器投票；新锚点下局部恢复（与投票交替）；最后一次零位移 ordinal
恢复及局部收尾。ordinal 需要进入该阶段前已有的同 rank 独立强固定点，不能相互造证据。
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

full/delta 的选择（`useDelta`）：

- `observe(P)` 强制 full；
- 否则仅在“已有确认基线、基线属于同一会话、`revision - baseline.revision < 12`、
  基线采集龄 `< 180s`”时尝试 delta；
- 差分必须可编码，且编码长度 `< full 长度 × 70%`；否则退回 full。
- delta 无法表达（同 ref 角色变化、根变化或非法最终图）时 `Diff` 返回错误，采集改用 full。
- `act` 重验失败交付的观察按 `forceFull = !obs.Full` 决定，`obs` 为本次失败请求引用的观察：
  引用 full O 时它在认领时已被确认为基线，满足上述条件时交付若为 delta，其 `base` 就是调用方
  刚刚使用的那棵树；引用 delta O 时基线更早，改发 full，避免要求调用方把补丁应用到更早的树。

**基线的唯一确认入口**：只接受被接纳的 `observe(O)` 对该 O 的确认，或 `act` 对 full O 的
认领确认。即：`start` 的首个 full 只是候选；`observe(O1)` 成功后才把 O1 升为基线，
并把空差异 delta 返回给调用方。客户端必须保留基线树，按 `delta.base` 应用到本地副本。

delta 的五类块：`set`（逐字段完整新值，显式保留 `false`/`0`/`""`/`[]`/`null`）、`add`、
`remove`、`children`（完整子列表）、`replace`（整段替换）。空差异只含 `format`、`base`、`view`。

五块是原子最终图声明。新增父收养存续节点时拆为顶层空 Add 组装根，并用 children 给出完整
最终子列表；存续节点跨父迁移必须消除旧父 claim。remove 根必须消失，其基线后代可经最终
可达边存活；不可达残余只允许来自删除/替换旧区域。Replace 根可迁移，其内部存续后代保持
基线父关系。双父、循环、孤儿及不可达显式声明整体拒绝。Node/Read 共用规划与应用器，Read
额外展开/打包 chain 并校验规范编码。五个布尔严格比较 unknown/false/true，完整节点省略
已知默认，set 保留显式 null/false；阅读树、inspect 与文本块不携带 defaults 声明。

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
| 动作执行 | `outcome` | `executed` / `not_executed` / `uncertain`；只描述本次输入是否已派发并有完成证据 |
| 滚动效果 | `effect.state` | `observed` / `not_observed` / `unknown`；只在 `scroll` 结果与仍保留该原操作的 P 视图中出现，只表示是否观察到容器内内容位移，不表示动作因果、方向、距离或到达边界。`observed` 要求屏幕位移与容器相对位移同时成立且同号对达到门限，`not_observed` 用完整匹配集与容器四边不变判据，二者都不证明原始内容静止 |
| 观察 | `observation.status` | `complete` / `partial` / `failed`；后观察失败不改写已确认的 `executed` |
| 条件等待 | `wait.trigger` / `wait.matched` / `wait.elapsed_ms` | 等待因哪个分支退出、最终发布帧上成立哪些单帧条件、整次调用耗时；不表示页面之后不再变化，也不替代 `observation.status` |
| 观察质量 | `observation.quality` | `state` / `samples` / `stop_reason`；只表示树是否收敛、经过多少有效采样，不限制输入授权 |
| 图片 | `observation.image` | 本次附图的元数据或受控错误；图片失败不改写核心树与主动作 |
| 会话 | `session.state` | `starting` / `active` / `ended` |
| 物理收尾 | `cleanup.*` | 显示/任务/执行链是否确认释放 |

`outcome`、`effect`、`wait` 与 `observation.quality` 各自独立：`executed + effect=unknown`
是合法结果；`unstable`/`unknown` 的观察仍可发布、仍可被认领，是否执行由动作自己的判据决定；
`effect`、`wait` 与 `quality` 都不改写 `outcome`、`observation.status`、错误或恢复入口，也
不改变 O 的 `status`、`actionable_until` 与基线确认。`wait` 在 `Result` 中固定位于 `shell`
之后、`notification` 之前；`status(P)` 与重复 `P` 的视图保留已保存的 `wait`，并按既有规则
省略 `observation`。

事件日志只记白名单键（ID、枚举、布尔、计数、时长），值经过单行截断；不记录树、差异、输入
正文、命令、原始响应或异常文本。每个 UI 窗口关闭只记一条 `ui.window_closed`，
`state=terminated|not_dispatched|unknown`；该事件不进入任何工具响应。受控滚动的规划、重验与
效果事件只带 `operation_id`（必要时 `observation_id`）与受控枚举/布尔/计数，不记 ref、坐标、
标签或树；`scroll.revalidate` 的 `start_scope_proven`/`end_scope_proven` 只描述两端，
整段证明由独立的 `proven` 布尔值表达；`scroll.effect` 的 `anchors`/`coverage` 仍按完整
匹配集计数；观察质量事件只记可取得的 `operation_id`、`observation_id`、阶段与受控枚举/数值。
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
`release_version=0.20.0`、`contract_version=20`、`android_version_code=22`，构建生成
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
