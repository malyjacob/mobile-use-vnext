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
  ├─ internal/mcp        12 工具注册、schema、严格接入、结果与图片投影、取消包装
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
  同仓而绕过认证与服务状态；`internal/client` 只依赖 `protocol`。
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
  既有触发点（`act` 认领、成功发布新 O、`shell` 派发边界）按 `SessionTTL=120s` 取
  `max(expires_at, now + 120s)`，不继承显式 `keepalive` 时长，也不截短更长的剩余租期。
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

## 4. full/delta 与提交条件

一次采集（`captureAndPublish`）的固定步骤：

1. 复核工作未撤销、会话仍属于该工作；
2. `ProbeDisplay` 确认目标逻辑屏存在；
3. `start` 的首个观察、`act` 与 `open_app` 的后观察先做收敛采样：逐帧 `Capture`，只有当
   连续两帧的三层摘要全部相同才停止——节点内容身份链（与实时重验同一个 `identityOf`）、
   公开树的规范编码、前台状态/包/任务与显示几何。采样间隔取上一帧的实测耗时，相邻两帧的
   采集起点间隔不小于该阶段的静默窗口，帧数与总墙钟分别有上限；预算耗尽照常发布最后一帧，
   不判采集失败。当前取值（静默窗口/总预算）：`act` 400ms/2s、`start` 300ms/1.5s、
   `open_app` 600ms/3s；它们是服务端固定策略，不是请求字段；
4. 取得发布用层级与前台：有收敛采样时用收敛帧，否则 `Capture` 读取一次；两者在同一次读取
   中取得；
5. 采集龄超过 `CaptureMaxAge=15s` → `stale_screen`；
6. 虚拟屏上出现控制 App 的可见节点 → `control_ui_on_virtual_display`，采集失败；
7. `observation.Builder` 构造公开树（合成根、必要祖先、保守保号）；
8. 始终先编码一份完整树；超过 `MaxFullTreeBytes=128 KiB` → `tree_too_large`；
9. 在锁内重验：会话仍 `active`、`epoch` 不变、工作未被撤销、仍持有工作位置、显示 ID 与
   `generation` 未变、采集未过期；
10. 通过后 `session.Revision++`，分配 O，发布，退休上一个 ready O，续租。

第 9–10 步与 full/delta 选择抽成单一发布步骤 `publishTreeAttempt`（第 7–8 步由
`projectCapture` 完成）：普通采集与 `act` 重验失败交付的帧共用同一提交门禁。交付路径在
派发前失败时复用重验已经读取的那一帧，不重新采集、不做收敛采样、不确认基线，只把该帧投影、
校验并提交为新的 ready O。

### 图片附加材料

`observe(image=true)` 与 `act(image=true)` 的后观察在同一次采集中增加一次目标屏源图读取：
先确认显示身份，以运行时 SurfaceFlinger 标识读取源图，成功读到源图后再确认同一显示身份，
然后读取层级；图片编码在树读取之后执行一次。图片与树共享同一个提交门禁：核心树失败不发布
O，图片成功也不单独发布；核心树有效而图片失败只把本次观察标为 `partial`，不改变树与 O
的有效性。

`act(image=true)` 的收敛采样在源图读取之前完成：采样插在图与树之间会把两者的跨度撑过固定
预算。源图读取之后再读一次树作为发布用层级，使图片与树描述同一时刻附近的状态；这次读取
与收敛帧不一致时，在剩余预算内继续收敛，预算耗尽则发布该帧，并在事件日志记 `stable=false`
（不进入观察结果）。

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
- delta 无法表达（角色变化、跨父移动、根变化等）时 `Diff` 返回错误，采集改用 full。
- `act` 重验失败交付的观察按 `forceFull = !obs.Full` 决定，`obs` 为本次失败请求引用的观察：
  引用 full O 时它在认领时已被确认为基线，满足上述条件时交付若为 delta，其 `base` 就是调用方
  刚刚使用的那棵树；引用 delta O 时基线更早，改发 full，避免要求调用方把补丁应用到更早的树。

**基线的唯一确认入口**：只接受被接纳的 `observe(O)` 对该 O 的确认，或 `act` 对 full O 的
认领确认。即：`start` 的首个 full 只是候选；`observe(O1)` 成功后才把 O1 升为基线，
并把空差异 delta 返回给调用方。客户端必须保留基线树，按 `delta.base` 应用到本地副本。

delta 的五类块：`set`（逐字段完整新值，显式保留 `false`/`0`/`""`/`[]`/`null`）、`add`、
`remove`、`children`（完整子列表）、`replace`（整段替换）。空差异只含 `format` 与 `base`。

## 5. 工作位置与并发

- 设备同时只有一项当前普通工作（`start`/`observe`/`act`/`apps`/`open_app`/`shell`）。冲突立即返回
  `409 device_busy`，不建普通动作队列；同一 Agent 会得到 `blocking_operation_id`。
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
  `WorkTimeout + 15s`（35s）、`act`/`open_app` 约 50s、`apps` 约 30s、`shell` 约 65s；具体超时是服务策略，不是请求参数。
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
- 任务补偿以原已登记 ID 与当前 live task ID 的并集为候选，每次删除前重验 boot、App 实例、
  原 display/generation、目标 ID 与当前资源冲突。live 或 recents 命中的原归属 ID 实际派发
  `RemoveTask`，删除返回不等于成功，必须重读实时任务与 `dumpsys activity recents`。
  跨 boot、归属不足、任务移到主屏/其它归属或 ID 已被新资源使用时停止该目标的破坏性补偿。
- 任务全部消失且最近任务项也消失时 `tasks=confirmed`、`recents=confirmed`；仍有任务、
  不可读或身份冲突时相应记 `unconfirmed` 并进入有限重试或停止。无证据时不能按包名、PID、
  display 数字或“当前显示”扩大目标。
- 虚拟会话在内存维护受管窗口登记（`taskEntry`：task id、包名、display、generation、
  owned、proven，上限 16）；它是每次动作前由实时任务列表核对的缓存，不跨会话或重启
  继承控制权；主屏会话不建立该登记。
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
- 读取版本 1/2/3/4。版本 3/4 中已发出预算且仍为 `pending` 的退休记录，启动时先持久化为
  `unconfirmed`，不授予第二次补偿预算。current 或旧格式记录转成退休记录时获得一次新预算；
  迁移快照必须先保存，再允许任何补偿派发。
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

- 预算 64 KiB；格式名 `mobile-use-vnext-ledger`，读版本 1/2/3/4、写版本 4，修订号单调递增；
- 单写者：包内互斥 + 进程锁；`Save` 先 fsync 临时文件，再 rename 覆盖，再 fsync 父目录
  （Windows 跳过目录 fsync，因此 Windows 不提供同等耐久证据）；
- 版本 3 分离当前归属与 `retired` 明细。内容只有归属与资源证据：原 service instance、
  origin/agent/mode/state/App 实例/boot/device 身份、模式代次、原 create command、
  显示 ID/SFID/generation/尺寸/密度/旋转/开始时间、待创建/待关闭命令、收尾五元组、停止边界、
  虚拟屏受管任务 id 登记（≤16）、受管 shell runner 身份与阶段、一个 UI 子步骤责任、legacy
  checkpoint、收尾核验过的任务 ID（≤256）与物理复核义务 `execution_recheck_required`；
- 退休明细最多 8 条，其序列化集合最多 24 KiB；整个账本仍最多 64 KiB。容量达到边界时停止或
  压缩退休项，不把容量不足变成新会话拒绝码，也不能把淘汰写成 `complete`。
- 明确不含：请求体、节点树、图片、输入正文、命令输出、异常字符串；
- UI 窗口状态为 `may_dispatch`、`terminated`、`not_dispatched`、`unknown`；`may_dispatch`
  表示调用仍未返回，`unknown` 表示窗口已关闭但没有可用结束回执，是私有账本的持久终态，
  不改判原工具的 `uncertain`。
- `start` 在启动显示助手前同步写一次（原归属 + 创建意图），会话激活后再写一次；其余进度
  异步写。每个 UI 输入子步骤额外预写 `may_dispatch`，成功后重新竞争派发边界；调用返回后
  只记录一次关闭状态。窗口关闭不阻止后续普通工作；写失败仍置 `ledgerUnsafe`，可用性为
  `blocked`（原因 `ledger_unavailable`）；STOP 不等这些写入。
- 版本 1/2/3 升级保留原 Session/Cleanup/Stop/Shell/task 事实；缺失的新字段不充当结束证据。
  `legacy_ui_pending` 保留旧责任，迁移后作为退休记录中的未确认事实。回退只能使用兼容版本 4
  的产物；不通过降级覆盖或清库恢复。

账本只由 engine 读取与生成，HTTP 层与 App 都不写账本。

## 9. 事实分层与日志

同一次请求可能同时表达四层事实，互不替代：

| 层 | 字段 | 含义 |
| --- | --- | --- |
| 动作执行 | `outcome` | `executed` / `not_executed` / `uncertain`；只描述本次输入是否已派发并有完成证据 |
| 观察 | `observation.status` | `complete` / `partial` / `failed`；后观察失败不改写已确认的 `executed` |
| 图片 | `observation.image` | 本次附图的元数据或受控错误；图片失败不改写核心树与主动作 |
| 会话 | `session.state` | `starting` / `active` / `ended` |
| 物理收尾 | `cleanup.*` | 显示/任务/执行链是否确认释放 |

事件日志只记白名单键（ID、枚举、布尔、计数、时长），值经过单行截断；不记录树、差异、输入
正文、命令、原始响应或异常文本。每个 UI 窗口关闭只记一条 `ui.window_closed`，
`state=terminated|not_dispatched|unknown`；该事件不进入任何工具响应。

## 10. 消息投递与发布边界

### 消息

`notify` 只保存消息并把它放入控制同步队列（`notification.state=pending`）。消息队列有界
（256 条 / 1 MiB 正文），未确认项持久化在 `<state_dir>/messages.json`，与恢复账本分离；
服务重启后按原 ID 重发，App 按 ID 去重并确认已保存。确认只释放服务端队列，不表示显示或
已读；App 的已读是本机事实，不回报 Agent。消息正文不进入日志、错误、账本、恢复记录、
工具结果投影或发布清单；消息存储故障不改变 STOP、清理与准入。

### 发布

`release/versions.json` 是发行号、共同契约号与 Android versionCode 的唯一来源，构建生成
Go/Java 常量；`scripts/build-release.ps1` 产出整组产物与 `build/release/manifest.json`
（源码提交、Go 版本、MCP SDK/协议、各产物 SHA-256/大小、Android 包名/版本、工具目录指纹）。
部署按清单核验后覆盖安装，服务与 App 的契约必须精确相等；App 私有数据保留，不使用卸载/
清库解决签名或配对。MCP 宿主必须重启实际进程并重新发现工具，替换磁盘文件不等于发布完成。
发布与回退的受控顺序见[发布与回退](release.md)。
