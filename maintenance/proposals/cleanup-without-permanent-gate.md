# 提案：修复 recents 漏清，并取消旧会话清理失败造成的永久准入封锁

状态：提案，未实现。
确认：2026-11-11 用户明确要求按本提案及项目规范实现、发布 0.5.0 并部署手机；范围、风险及数值整体确认。追加约束：实现和部署不重启手机，不卸载、不清 App 数据。
基线提交：`33241c57080ce4667b0b168f309358b4bd1d9959`（分支 `master`，2026-11-11）。
适用范围：虚拟屏/主屏会话的 end、STOP、到期、取消、启动失败和进程恢复；新会话准入、旧执行责任与物理收尾。
影响范围：`internal/engine`、`internal/android`、`internal/device`、`internal/store`、`internal/protocol`、`internal/mcp`、`internal/client`、`cmd/device-service`、控制 App、发行版本与相关正式文档。
契约（`/api/*` 字段与 `contract_version`）：改变。建议共同 `contract_version=5`；12 个工具和 Agent 请求/响应字段不增加，改变准入、清理状态、历史引用和恢复语义；内部控制增加精确取消创建命令，私有账本升级为版本 3。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须重新核对。
重点是 `cleanup.go`、`control.go`、`start.go`、`operations.go`、`ui_execution.go`、`shell*.go`、`VirtualDisplayHost.java` 和账本校验。
设计源头为相邻 `mobile-use-platform` 仓库 `codex/vnext-delta-redesign` 分支，基线
`10718fd58ddbe2e2b858a4b008deadae3692a5cf`。本提案不修改源头或本地正式契约；批准后先以 docs-only 变更修订源头，再实现。

## 0. 摘要

Issue #2 将一次 recents 清理失败放大为整台服务长期无法建立新会话；现场旧任务已经消失，旧账本状态仍阻止准入。
同时修复 recent-only task 未被删除、普通收尾失败不重验和恢复固化旧未知这三条路径。
取消“旧 cleanup 未全部确认，所以无限期禁止新会话”的规则；执行结果和清理事实仍如实保留。
旧会话撤权后进入独立、有限期的善后记录；新会话与旧任务、显示、执行回执、命令和树构造状态分属不同归属。
补偿到期、身份不足或容量不足时停止主动清理并保留失败摘要，不假报 confirmed，也不重新封锁准入。
这是一项明确偏向可用性的取舍：已派发的未知设备命令可能产生迟到影响，本提案不声称将其撤回或完全隔离。

## 1. 问题

### 1.1 现象与代价

[Issue #2](https://github.com/malyjacob/mobile-use-vnext/issues/2) 中，Bilibili 虚拟屏会话 end 成功，
execution/tasks/display confirmed，recents unconfirmed；新 start 返回 `cleanup_unconfirmed`。
轮询 status 不能推进收尾，整个服务无法继续使用。

本次实机检查时，旧 task 2761 已不在 live 或 recents，服务仍为 `old_resource_unconfirmed`。
保留账本并执行一次手机重启后，既有 boot-boundary 路径完成复核，准入恢复；随后新 MCP start/end 成功。
因此“等待更久”“多调用 status”以及“只重启适配器”均不能作为此类状态的产品出口。

分诊：漏清和恢复推进缺口是实现问题；永久准入封锁由现行规范直接规定，取消它属于设计取舍。
用户先认可问题定义并要求起草，随后于 2026-11-11 整体确认本文并授权实现、发布和部署。

### 1.2 证据链

复用[归因与实机恢复记录](../verification/recents-cleanup-lock-investigation.md)和
[先红 fixture](../verification/fixtures/recents-cleanup-lock-repro.go.txt)，不把推演当作新的验证结论。

| 基线位置 | 事实及影响 |
| --- | --- |
| `internal/engine/cleanup.go:174` | 先入队 close_display，再读取/移除 live tasks；显示关闭与任务读取可竞争 |
| `internal/engine/cleanup.go:431`、`:447`、`:458` | original ID 仅加入核验集合；live 为空就结束删除；只对 live ID 调用 RemoveTask 并忽略错误 |
| `internal/engine/cleanup.go:473` | recents 不可读与旧 ID 仍在索引都变成相同布尔失败 |
| `internal/engine/cleanup.go:44`、`internal/engine/end.go:77` | 同一 session 收尾只启动一次；已有 unconfirmed 不因再次 end 推进 |
| `internal/engine/engine.go:696`、`internal/engine/ui_execution.go:180` | 通用恢复只针对 startup record；物理复核只因执行结束证据触发，普通 recents 失败没有通用复核义务 |
| `internal/engine/control.go:566` | startup recovery 直接保留旧 recents 未确认，不读取当前 recents 事实 |
| `internal/engine/engine.go:515`、`:627` | cleanup 的任何未确认项会拒绝新会话 |
| `internal/engine/start.go:101`、`:109` | 单个当前 session/cleanup 槽位不能承载新会话与旧善后并存 |
| `internal/engine/cleanup.go:488`、`:492` | 旧收尾按 App instance 过滤 create_display；直接放开重叠会影响同实例新创建命令 |
| `internal/engine/capture.go:362`、`internal/engine/start.go:128` | 树构造器由引擎共享；放弃旧 work 后不能继续让其 Build/Preview 修改新会话构造状态 |
| `android-control/app/src/main/java/com/mobileuse/vnext/VirtualDisplayHost.java:73`、`:183` | retiring 非空拒绝新创建；descriptor 会回退到旧 retiring 显示，可能把门禁移到 App 或误绑定旧屏 |
| `android-control/app/src/main/java/com/mobileuse/vnext/ControlService.java:274` | mode_ready 还依赖 cleanupPending 为 false；只修改 engine 或 create 拒绝条件仍会被旧清理挡住 |
| `android-control/app/src/main/java/com/mobileuse/vnext/RootDisplayConnection.java:99` | CREATE 使用同步 Binder transact；仅更换外层 executor 不能保证旧调用会返回 |
| `internal/engine/ordinary_admission.go:14`、`:27` | 未决 UI/shell 也会封闭普通入口；只取消 cleanup 门禁不能满足结束会话后不再永久阻塞的目标 |
| `docs/architecture.md:232`、`:236` | 所有适用项确认才 complete，unconfirmed 对应全局 blocked，是需要修订的规范 |

三条 fixture 均已在基线上到达预期失败断言：recent-only ID 的 RemoveTask 次数为零；一次读取失败后不收敛；恢复快照在设备事实为空时仍阻塞。
旧成功路径和保留未知的三个对照测试通过。Android 原事故最初为何失败仍未唯一定位。

同类核查覆盖 end、STOP、到期、退出、App 实例切换、启动恢复、UI 终结复核和 shell 恢复。
旧实机验证已经遇到 recents 残留，见[既有记录 §5](../verification/ui-input-execution-evidence.md#5-附加输入检查与最终状态)。

### 1.3 为什么直觉会出错

- 增加重试只能降低故障概率，不能消除最终失败时的永久门禁。
- 只删 can_start 的判断会遗失旧归属、复用旧显示、让旧完成回调清掉新 work/命令，或让 App 继续拒绝创建。
- 将超时写成 complete/confirmed 会混淆“放弃继续等待”和“设备已经清理完成”。
- recents-only ID 不能只比较不删除；但跨 boot、归属丢失或与新资源冲突时，也不能按裸 ID 盲删。
- 把未确认记录永久固定保留，容量满时再禁止 start，会以另一个错误码重新制造同种锁死。

## 2. 目标与非目标

目标：

1. recent-only 旧任务在归属足够时进入精确删除和重读核验；读取失败与实际残留可区分。
2. end/STOP 仍先撤权并立即返回；新 start 不等待旧 tasks/recents/display 清理完成。
3. 已结束会话的未知 UI/shell、late-create 和旧恢复责任只经过有限交接窗口，不能永久占住工作位或准入。
4. 旧清理、回执、树算法、控制命令、结果路由与新会话隔离；旧 P 不重放、不改绑，不覆盖新 O/P。
5. 补偿与持久记录均有总量和时间上限；失败和淘汰不生成 confirmed，也不因债务容量满封锁新会话。
6. 服务恢复后可带着未确认善后重新开始；不要求用户清账本、清 App 数据或重启手机。
7. status 可同时表达 can_start=true 与旧 cleanup unconfirmed，并说明自动处理是否已停止。

非目标：消除所有 Android/OEM 清理异常、追认 uncertain 动作、回滚应用业务、搬回主屏用户任务、
自动手机重启、支持旧工具契约降级、引入新的 Agent 工具、通用 root 资源回滚或完整沙箱。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R0 维持现状 | 保留完整 cleanup 门禁 | 无新实现 | 一次未知即可长期停用全设备 |
| R1 最小修补 | 修 recent-only 删除、增加重验，仍要求清理完成才准入 | 改动小 | 重试耗尽、task/display/执行证据失败仍会锁死 |
| R2 只删除门禁判断 | 直接忽略 cleanup 状态 | 表面最少 | 单槽位、旧回调、命令过滤和 App retiring 会互相干扰，甚至清新资源 |
| R3 本提案 | 新权威与旧善后分离；有界交接与补偿；耗尽后停止主动处理，允许新会话 | 跨 engine/App/账本/契约改动，需要明确承认迟到设备影响 | 物理残留可能积累，底层资源耗尽仍会使本次操作失败 |
| R4 每会话完整执行隔离 | 额外独立进程/设备命名空间或强执行沙箱 | 超出当前手机与产品范围 | Android 全局输入、共享系统资源和任意 root 派生影响仍不能仅靠应用层消除 |

选择 R3。R1 的漏清修复和重验作为 R3 的一部分；不把重试成功当作取消门禁的前提。
R4 不纳入本次实现，也不以先完成 R4 为理由延后解除永久封锁。

## 4. 判据

### 4.1 新会话准入

准入只检查当前服务能力和当前权威冲突：内核运行目录锁已取得、账本可载入且新归属能保存、
当前 App/模式/STOP 同步有效、设备准备完成、交互/锁屏条件满足、没有当前活动会话或当前普通工作。
旧责任的逻辑交接窗口最多 5 秒；窗口到期不再作为拒绝理由。

不再参与新 start 准入的项目：旧 cleanup.state、旧 execution/tasks/recents/display 未确认、
旧自动补偿未完成、旧物理资源仍存在、旧善后记录容量、旧 legacy UI 是否已取得手机重启证据。
相应 cleanup_pending/cleanup_unconfirmed/old_resource_unconfirmed/ui_input_reboot_required 等原因不得因旧会话持续返回。

仍可拒绝的具体例子：新的会话正在执行、当前 STOP 意图尚未同步确认、手机当前锁屏、当前 ADB 不通、
账本当前不可写。它们由当前事实决定，不能从旧 cleanup 推导；capability 恢复后须重新评价。
旧保存失败不能把 ledgerUnsafe 永久固定：engine 后台对当前完整快照做有限单次重试，成功后解除当前存储故障。
这不延长旧补偿预算，也不把磁盘恢复成功当成旧资源释放。

### 4.2 事实与处理状态

- cleanup.state 和四项事实继续使用现有枚举，不新增公开 followup。
- state=pending：有界补偿尚在运行；四项各自保留当前证据，不能为了表示正在重试而将未知改为 confirmed。
- state=complete：适用事实全部确认，且没有晚到执行边界使物理证据失效。
- state=unconfirmed：主动补偿已停止，仍有未确认事实；不是“还要无限轮询”的同义词。
- 尝试轮数 0～3、最后阶段 none/execution/tasks/recents/display 和停止原因保留在私有记录与白名单日志；cleanup.error.code/diagnose 说明受控失败阶段。
- 停止补偿不阻止晚到可信原回执更新原事实；不重新派发、不延长期限、不改原 P outcome。
  新终结边界使旧物理证据过时时，相应项退回 unconfirmed；没有新的充分物理证据就不能 complete。
- 活动会话内未决输入仍禁止继续派发同会话的下一输入。end/STOP 可立即开始交接；若调用未自行返回，工作期限到达时自动撤销原会话并进入交接，不保留无限 running。

未决 token 保留原 work 的绝对期限；即使 handler 已返回 uncertain、e.work 已释放，engine 时钟仍在该期限撤销原会话。
keepalive、status、同步或后来的调用均不能延长该 token 的期限。这里只封闭原派发链，不追认原 P。

### 4.3 交接与风险接受

交接完成的条件是**旧逻辑派发权和提交权已封闭**，不是旧 Android 命令已被证实停止。
从撤权时刻起取消原上下文、禁止旧流程下一子步骤、撤销未派发创建/输入命令；对精确 shell/helper 请求停止。
最多等待 5 秒取得终结证据；提前返回可提前释放工作位。到期由 engine 脱离旧 work/UI/shell 与当前槽位，记录未知事实。

旧逻辑 goroutine 即使迟到也只能操作原记录或私有候选。已跨派发边界的 Android 输入、精确清理命令、同步 RPC 或任意 root 派生进程可能继续影响设备。
因此本提案保留“一个当前普通工作”的逻辑串行性，但**不再承诺未知旧设备调用与新工作在物理时间上绝不重叠**。
这项风险接受覆盖虚拟屏和用户已确认的主屏共享，不暗中对主屏保留无限旧责任门禁。

### 4.4 破坏性清理资格

清理 worker 每次派发前重验原记录、boot、App instance、原创建 command、display generation、目标 ID、补偿期限和当前资源冲突。
原任务从 live 消失不丧失同 boot 下的 recents 清理资格；原台账已登记并且未被新资源认领的精确 ID 可被重验和清理。
跨 boot、归属不足、发现任务移动到主屏/其它归属、ID 被新会话使用时，不派发删除，停止该目标的主动补偿并保留 unconfirmed。
无证据时不能按包名、PID、display 数字或“当前显示”扩大目标。超时、容量淘汰和新会话开始均不构成 confirmed 证据。

## 5. 算法

### 5.1 状态与归属

Engine 保存一个当前 session/work；撤权后生成独立 retired record，键为原 service instance + origin + epoch。
record 冻结原 agent、mode、boot、App instance、创建命令、display/generation、精确任务和 UI/shell token。
收尾记录不是另一份输入权威；只有 engine 能调度其精确清理。

```text
当前会话 active
  → end/STOP/到期/失败：撤权、退休旧归属、启动有界交接
  → 最迟撤权后 5 秒：当前槽位释放，允许新 start

原 retired record
  → 有界补偿：cleanup pending
  → 事实全确认：cleanup complete
  → 到期、身份冲突、容量淘汰：cleanup unconfirmed，停止主动处理
```

停止与新启动的时间轴：

```text
t0       原会话撤权；end 返回；旧补偿独立运行
t0..5s   仅旧在途逻辑交接可能造成短暂 device_busy，不等待物理清理
≤t0+5s   新会话可尝试准入；当前能力未就绪则报告该能力原因
≤t0+12s  原补偿完成或停止；不延长交接 busy，不重新封锁新会话
```

### 5.2 撤权、发布与迟到结果

```text
retire(original):
  锁内撤权/退役 O/封闭派发，冻结 retired record，取消原 work
  为交接和补偿设置只减不增的 deadline；请求路径不等落盘或设备
  锁外保存/调度精确停止与补偿

detach(original, expectedWork):
  只在 e.work == expectedWork 时释放当前位置
  从当前 session/UI/shell 槽位转移原责任；不清掉后来的新位置
  新 start 的首次副作用前，连同旧事实保存一份完整新账本

lateResult(originalToken, candidate):
  定位原 retired record；不存在则丢弃，不回退取 current
  允许更新原终结/物理事实；已结束 P 的 outcome 保持冻结
  不发布 O、不续租、不对新会话 Build/Reset、不发下一业务子步骤
```

当前 worker 与 retired worker 使用独立投影器实例和有界私有候选；同一新会话内仍由普通位置串行。
不能只在全局 projector.Build 前检查一次授权后继续共享 builder。新建 session 必须创建独立 builder；
`Build/Preview/Reset` 和观察保号绑定该实例，旧算法即使尚未退出也不能修改新 builder。

设备 I/O 通过支持取消/期限的调用边界执行。engine 监控器到期须使原请求退出 running 并冻结已知结果，
不依赖普通 handler 的 defer 才释放 e.work；未知派发为 uncertain，已证实 executed 不反转。
迟到适配器结果不能重入原普通流程。不能用无上限新 goroutine 包装器隐藏永久挂起；相关连接/子进程须有取消与退出期限。
Android/Binder 本身不返回属于能力边界，不能把物理终止伪造为成功。

适配器的主机子进程使用 context 取消与有限 WaitDelay；RPC 使用请求 context 并关闭取消调用的连接，
不把永不返回的外部 I/O 放进一个长期持锁的共享 client。树算法仍必须有输入/规模上限；旧算法只持有原实例。
不为每次 end 新建一个等待原 goroutine 的长期监控者，交接由单个 engine 时钟检查截止时间。

### 5.3 任务、recents 与显示补偿

每条记录总预算 12 秒，最多三轮，轮间等待 200ms、800ms；每次 I/O 使用剩余总预算，单次上限 2 秒。
预算在退休时开始，不因 App 心跳、status/end、错误、重启或重试刷新。

1. 精确请求停止旧 shell/helper/未完成创建，但不把停止请求当作终结证据。
2. 取得原 display 上的新鲜 live tasks 和可辨识的 recents 结果。可安全认领的 live ID 加入原记录。
3. 对原已登记 ID 与当前 live ID 的并集逐项检查归属；live 存在或 recents 命中时均可成为 RemoveTask 目标。
   recent-only 分支必须实际删除，不能只比对。删除返回只记调用事实，重新读取才确认 tasks/recents。
4. 有界任务处理后请求精确 close_display；若显示已先关闭，仍执行步骤 2～3 的 recent-only 路径。
   STOP 的 App 本地立即释放不等待任务处理，因此正确性不能依赖“任务一定先于拆屏”。
5. 分别核验 execution、live tasks、recents、显示。任何阶段不可读都记 unknown，不能当空集合。
6. 可安全重试且仍有预算时进入下一轮。预算耗尽、身份冲突或无法再验证目标时提交 state=unconfirmed，取消尚未派发的补偿。

同一 record 只有一个补偿 worker；再次 end 只读取它，status 保持只读。
每轮控制命令使用新的 command ID，但目标 generation/创建身份始终指向原创建；不能被 App 的旧去重记录或
engine 的永久 closeAttempted 标记吞掉合法后续尝试。新 create 和当前 STOP 同步优先于旧待发送补偿；
旧命令按 record/command 精确移除，不按 App 实例批量过滤。
ADB 清理派发使用可取消的后台维护许可；当前普通工作优先，补偿不排在普通请求前、不占普通队列。
新普通工作到达后取消尚未派发的清理；已派发的精确命令可能结束较晚，结果仍只归原 record。
因设备持续繁忙错过补偿窗口时停止处理，不为保证清理成功阻塞新工作。

### 5.4 App 与显示归属

- 新 create 的 command ID 同时作为该 display generation；modeGeneration 只表示用户模式代次，不能作为物理 attempt 身份。
  Java attempt、engine session、账本和关闭目标使用同一个值。旧账本继续保存原 generation，不改写旧事实以伪造新创建。
  这是简化新身份的设计选择，不据此声称当前版本所有创建都会因 generation 不一致失败。
- retiring 非空不再拒绝新 create。当前 descriptor 只表示新 current；不回退到旧 retiring descriptor。
  不新增 retired_displays 同步字段；旧资源仍可能存在由原 cleanup 未确认事实与 App 本地历史详情表达，null current descriptor 不等于全设备无残留。
- mode_ready 不再因 retiring/cleanupPending 为 false；它只表达用户当前模式确认和当前控制能力。清理状态单独展示。
- close_display 沿用 App instance + display ID + generation 的现有目标字段；新 generation 即 create command ID，
  无需再增加重复的 target_create_command_id；旧完成回调不得释放/替换新 current。
- 未回报 display ID 的创建由内部 cancel_create 命令按原 App instance + create command 撤销；不新增 Agent 工具。
  撤销命令只封闭该 attempt 的后续派发，不声称已发送的 CREATE 被撤回。
- allocation worker、release worker 绑定 attempt。CREATE/VERIFY 改为每 attempt helper 的异步请求/回调，
  App 不在单线程 allocation 队列中无限等待同步 Binder；完成/失败由主线程 token 校验，超时撤销仍由主线程计时。
  CLOSE 沿用精确目标的单向发送。原 helper 在系统 Binder 中卡住不占新 attempt 的 App 等待线程，迟到成功只归原 token。
- 已派发 CREATE 尚无终结证据时，不能因逻辑 retire/5 秒到期就释放该调用仍可能使用的 ImageReader/Surface/capability。
  异步设计由 per-attempt helper 持有实际创建所需的 Surface/token 生命周期，App 只保留可取消回调和当前 descriptor；
  相关资源在 CREATE 终结或 helper death/release barrier 后释放。不能以丢弃 App 历史明细冒充这些资源已释放。
- retiring 明细和 listener/未开始任务同样有 8 条上限；到期或淘汰只丢弃主动处理资格并保留受控计数，
  不因历史上限拒绝新 create，不继续无限增加 executor 线程；取消已经派发的命令可能在设备端稍后完成。
  helper 尚未退出的真实资源成本明确保留，系统资源耗尽属于本次创建能力失败，不将历史记录数量另作门禁。
- App 只提供物理事实、执行 engine 指向的命令和本地 STOP；不新增由 App 决定新会话可否准入的 cleanup 门禁。
- 补偿停止后不再主动重试旧 release；晚到成功回调和 display removed 事件可更新事实，不重新创建旧资源。

### 5.5 恢复、容量与丢弃

账本版本 3 分离当前归属和 retired 记录。记录持有独立 cleanup/UI/shell/待释放命令及已消耗预算。
启动取得固定运行目录锁后读取原记录；旧 session 均转 retired，不恢复 O、主屏授权或普通派发权。
v1/v2 的未知 execution 保留 unconfirmed；不再要求先取得 checkpoint 或手机 boot 变化才能尝试新会话。
boot 变化仍可作为对应终结证据，跨 boot 的物理清理保持只读，不按旧裸 ID 删除新资源。

迁移和下一次 start 的完整归属保存必须成功；失败显示 ledger_unavailable，不把旧事实抹掉以换取放行。
新 ledger 无法被旧服务解释，发布回滚前须使用支持版本 3 的兼容产物；不提供手改或清空账本回滚。

保留最多 8 条 retired 明细，且明细合计最多 24 KiB；当前归属预留 24 KiB，整个账本继续最多 64 KiB。
先移除最旧 complete；再移除最旧 unconfirmed；必要时先停止最旧 pending 并转 unconfirmed，再移除它。
丢弃前取消其未派发命令，迟到结果无记录则丢弃；对外累计 dropped_unconfirmed，不保留可用于再次破坏性清理的裸 ID。
对应保留中的 operation/observation 路由保存原 agent、origin/epoch、终态与 cleanup 快照；不另建无限历史仓库。
retired 明细淘汰不抹掉仍在现有缓存中的终态路由。所有路由都不可用时，status(P)/end(P) 使用 409 operation_unknown，
diagnose 使用 coverage=record_unavailable；status(O) 沿用 observation_gone。不得换成新会话，也不得声称旧 P 从未接纳。
未知 end(P) 仍在现有有界关闭集合中按原 Agent 封闭该 P 的未来准入，但必须标注原接纳历史未知；
不能返回 closed_before_admission 或把原 outcome 伪造为 not_executed。该关闭标记不能结束当前别的会话。
原有具备正面未接纳证据的关闭分支保持；仅靠“缓存中没找到”不再足以进入该分支。
淘汰计数饱和而不溢出，只进入日志/diagnose 的受控问题说明，不新增 status 摘要字段，也不把容量不足变为 start 拒绝。

同一进程按单调时钟扣减 deadline，attempts 与停止状态落盘。服务再次启动时，已有版本 3 retired 记录
若仍 pending 则先持久化转为 unconfirmed，不恢复它的主动补偿；保留原事实和计数，晚到可信回执仍可更新事实。
旧版本或原 current 转退休的记录仅获得一次新预算，该退休/迁移快照须先保存再派发补偿。
保存后即使在首次尝试前崩溃，再启动也只保留 unconfirmed，不重复授予预算。这样不依赖墙钟单调性，也不因反复重启无限续命。
迁移候选包含 budget_issued 标记和已用 attempts，单写者将它与原事实一并保存；读 1/2/3、只写 3，嵌套记录深拷贝。
退休进展由一个有界合并写入通知驱动，不为每条心跳/重试无限创建 persistAsync goroutine；新副作用前的同步保存仍走同一串行写入者。
当前归属、所有保留 retired 进展与停止/淘汰计数必须来自同一完整快照，不能用旧 worker 的局部快照覆盖新会话。
停止后的 unconfirmed 不妨碍新 start；本次不增加人工重启补偿的接口。

## 6. 实现方案

### 6.1 接口与文件

下列新增文件名为职责建议，实现时可在同一文件族内调整，不扩大公开工具：

| 文件/包 | 改动 |
| --- | --- |
| `internal/engine/retirement.go`（新增）、`engine.go`、`operations.go` | 当前与 retired 归属、交接监控、有限保留、原 work 条件释放与普通准入 |
| `cleanup.go`、`end.go`、`control.go`、`recovery_retry.go` | 每原归属单 worker、有界补偿、recent-only 删除、恢复重验、原命令过滤 |
| `ui_execution.go`、`ui_recovery.go`、`shell.go`、`shell_recovery.go` | 当前与 retired token 路由、迟到证据、不再把退休未知当全局屏障 |
| `start.go`、`capture.go`、`settle.go`、`act.go`、`cmd/device-service` 投影适配 | 每会话独立 projector factory/实例；旧候选不修改新构造状态 |
| `status.go`、`diagnose.go`、`recovery.go`、结果投影 | 按 reference/origin 取 session/cleanup；区分仍在补偿和补偿停止 |
| `internal/device`、`internal/android/launch.go` | typed recents 读取状态、删除调用事实与核验分离；无原始输出/错误字符串穿透 |
| `internal/engine/ledger.go`、`internal/store/ledger.go` | 版本 3、v1/v2 读取迁移、深拷贝、容量与单写者快照 |
| `internal/protocol`、`internal/client`、`internal/mcp` | 沿用结果字段，修订清理/recovery/未知引用语义与投影；12 个工具及 Agent 请求字段不变 |
| `VirtualDisplayHost.java`、`RootDisplayConnection.java`、`RootDisplayProtocol.java`、`RootDisplayMain.java`、`ControlService.java`、`ServiceClient.java`、控制状态展示 | 当前/旧显示区分、异步 attempt 回调、精确释放/取消创建、有界本地处理、mode_ready 无旧 cleanup 门禁 |
| `internal/eventlog` | 有限事件/字段，记录失败阶段和补偿停止原因 |
| `release/versions.json`、版本生成与发布脚本 | 建议发行 0.5.0 / contract 5 / Android versionCode 7；仅版本源手改，生成其余常量 |

### 6.2 配置与取值

| 策略 | 建议值 | 依据与校验 |
| --- | --- | --- |
| 旧逻辑交接总上限 | 5s | 复用已有 shell 确认时间量级；从撤权开始，不等日志/物理释放；必须 >0 且有限 |
| 每 retired 补偿总时间 | 12s | 沿用 CleanupTimeout 量级，改为整条总预算，不再另加隐含尾部等待 |
| 最大补偿轮数/间隔 | 3；200ms、800ms | 有限吸收异步索引延迟；所有等待计入总预算；不是实测最优值 |
| 每次补偿设备 I/O | min(剩余预算, 2s) | 防止单个读取耗尽多轮预算；超时只产生 unknown |
| 明细条数/总字节 | 8 / 24 KiB | 让当前归属和 64 KiB 账本始终有独立预留；任一达到就停止并压缩最旧项 |
| 每记录 task ID | 最多 256 | 沿用当前清扫发现上限；超限停止扩大清理，保留未确认事实，不截断后继续宣称完成 |

均为内部固定策略，不增加调用方参数或 force/skip_cleanup 开关。由本提案确认这些默认值，之后按同环境证据修订。
新会话本身的操作期限、树/图片预算、STOP 同步新鲜度和用户模式确认沿用现行值。

### 6.3 日志与可观测性

复用 cleanup.start/complete/unconfirmed，新增 cleanup.attempt、cleanup.stopped、retirement.detached、retirement.dropped。
仅记录原 origin/P、display/task ID、枚举 phase/code/state、attempt、计量和耗时。
为 execution/tasks/recents/display 等必要字段显式扩展白名单；不能以调用了 Event 就假定字段已经输出。
正常 status/心跳不重复 WARN；失败阶段每次尝试最多一条，停止原因一次。

recents 读取区分 transport_unavailable / nonzero_exit / format_unrecognized / known_empty / known_present。
清理错误分别使用受控 code，例如 recents_read_unavailable、recents_cleanup_unconfirmed、task_remove_unconfirmed、cleanup_identity_conflict、cleanup_budget_exhausted。
原始命令、输入、树、包内文字、日志正文、截图、凭据、原始响应和异常字符串不得进入日志、恢复账本或发布清单。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威 | 保持；retired record 不具备普通派发权 |
| STOP/end 不等待 | 保持；交接和补偿锁外独立执行，end 不等 5s/12s |
| O 单向消费、P 不重放 | 保持；退休/迟到结果不能复活 O 或重新发原动作 |
| 提交条件化、状态锁短小 | 保持，并把原 record/attempt/projector 归属加入检查 |
| 一项普通工作 | 保持一个当前逻辑工作；承认旧未知物理调用可能重叠，修订原物理独占承诺 |
| 主事实/观察/终态/物理收尾分层 | 保持；新增处理状态不替代四项物理事实 |
| 旧资源全部确认才准入 | 取消；旧未确认只报告，不决定新会话是否可尝试 |
| 已取得运行目录锁才能触账本/设备 | 保持；新归属无法保存仍不得开始新副作用 |
| 旧责任固定保留至确认 | 改为有界善后；停止/淘汰必须明确，不伪造已确认 |
| 严格 JSON、版本唯一源、消息边界 | 保持；不扩大消息、输入或 Agent 已读通道 |

### 6.5 契约与文档影响

响应建议：

```json
{
  "can_start": true,
  "cleanup": {
    "state": "unconfirmed", "execution": "confirmed", "tasks": "confirmed",
    "recents": "unconfirmed", "display": "confirmed",
    "error": {"code": "recents_cleanup_unconfirmed", "message": "最近任务清理尚未确认"}
  }
}
```

cleanup 总是属于响应定位的原 session/origin。status() 有当前会话时给当前会话；无当前会话时可给本 Agent 最近退休会话，
同时展示该原会话的 ended 视图。不能定位同帧 session 的归属时省略 cleanup，不拼另一个全局 cleanup。
status(P/O) 与重复 end 始终取原记录或原历史快照，绝不能拼上当前另一个会话的 cleanup。
diagnose 在现有 issues/events 结构中给出本 Agent 的受控退休/淘汰说明；尝试数进白名单日志，不新增 status 摘要字段。
ControlView.cleanup 同样只属于其 session；Java ControlState 的旧 cleanupRisk 不得把历史 unconfirmed 变成主状态 blocked、禁用输入或覆盖当前会话文案。
App 本地 retiring 只作为历史详情和已知残留提示，不成为模式准备条件，也不表示服务已授权新的清理。
不新增列出历史/清理工具，不暴露 private boot/device 身份。

cleanup pending 的 end/status 保留 recovery.entry=status；unconfirmed 表示自动处理已停，不再把 status 轮询表述成恢复使用的必要步骤。
原 P 的 uncertain 恢复语义和明确错误仍按自己的事实保留，不能因新 start 可用而抹掉原错误。
收尾失败不把已成功 end 的 outcome 改成 not_executed，也不变成新 start 的顶层 cleanup_unconfirmed。
新请求仍有当前能力失败时，使用该失败的稳定错误码；交接期间使用现有 409 device_busy，并带原阻塞 P（仅对有权 Agent）。
HTTP/MCP 只投影 engine 结果，不能自行根据 cleanup 再判 blocked 或引导无限轮询。

完整事实查询和成功 end 仍为 HTTP 200、MCP 非错误，cleanup 自身未确认不改变这一点；
未知引用属于顶层错误，MCP isError=true。未知引用的具体响应：status(P) 为 409 operation_unknown；end(P) 在封闭未来接纳后仍为 409 operation_unknown，
phase=reference_unknown、outcome=uncertain、request_closed=true，session/cleanup 不捏造。
request_closed 只证明该原 P 已被封闭未来准入，不证明它过去未执行；关闭集合保留 known_not_admitted 标记，重复查询和登记保持同一证据层次。
只有具备未接纳的正面证据时才可使用 closed_before_admission/not_executed；不能从 operation 被淘汰推导该事实。
该兼容性变化需覆盖客户端取消包装：未知 end 不能被当成旧资源已释放，更不能转去结束当前其它会话。

Agent JSON 字段集合不增加，已有必填/省略/null、类型和枚举校验保持；错误/状态/recovery 表与 Go/Java/MCP 文本投影同步修订。
内部控制契约只新增 cancel_create；该分支的 target_create_command_id 必须实际出现且非空，
其它命令拒绝混入该字段。close_display 沿用现有目标字段；App 同步的 display 只表示当前 attempt。
root helper 的异步回调协议随 APK 一起更新；offer/capability/取消继续绑定原 App instance 和 create command，不能信任任意 Binder 回调。
共同契约升级，旧 App/适配器不得静默混用；发布核验清单和实际宿主重启要求沿用。

批准后的源头 docs-only 修订覆盖：`new-project-architecture.md`、`vnext-interface-spec.md`、
`cache-retention-budgets-and-restart.md`、`concurrency-and-observation-commit.md`、`shell-execution-and-recovery.md`、
`startup-and-response-recovery.md`、`control-app-and-breaking-release.md` 中的旧资源门禁及 descriptor 约定。
`task-lifecycle.md` 只对与本 vNext 路径有关的 STOP/旧 recents 清理条款做一致性修订，不引入其其它未采用工具。

实现时同步本地 `docs/architecture.md`、`interface.md`、`integration.md`、`client.md`、`runbook.md`、
`phone-operations.md`、`release.md` 及实际受影响的工程说明；删除“未确认就必须重启/无限等待”的操作建议。
AGENTS.md 第 5/9 条的独占/恢复门禁与本提案一致修订，在 maintenance/decisions.md 记录明确取舍；
现行流程的先红、提案确认、独立只读 docs 审计和先实现后归档顺序保持。

## 7. 预期效果与能力边界

### 7.1 能治什么

一次 recents 读失败、残留、旧任务漏清、display 释放不确认或旧执行缺终结证据，不再让已结束会话无限期占用整个服务。
有足够归属且设备正常时，recent-only 删除与复核提高真实清理成功率；持续失败则在预算内结束自动处理。
服务重启不会恢复旧派发权，也不会把旧 unconfirmed 再变成永久新准入门禁。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 旧输入、文字 RPC、root shell 在新会话开始后继续产生影响 | 已派发设备命令无法由撤权/超时保证撤回；本提案接受该风险 |
| 系统显示/进程/内存资源耗尽，新的 start 仍失败 | 允许尝试不等于保证分配成功；报告本次创建失败，不把历史清理失败当永久门禁 |
| 未确认资源不再自动清理，需用户在 Android 侧处理 | 补偿到期/身份不足/明细淘汰后主动停止，优先避免误删新归属 |
| App/ADB/STOP/账本当前不可用仍不能开始 | 新会话确实缺少当前能力；不是旧清理锁死，也不能伪装成可运行 |
| OEM 的 recents 输出/命令行为不同或暂时不可读 | 新结构区分原因，但不承诺消除底层异常 |
| 原 Bilibili 事故的最初触发无法还原 | 已取得的是当前状态、静态路径和模拟复现，不是原时间窗完整设备证据 |
| 任意原生调用永不返回造成底层线程/系统资源泄漏 | 取消和逻辑脱离不能回收 Android 内核中的不可中断执行；不许在服务中通过无限建 worker 掩盖 |

### 7.3 成本

正常会话仍有一个当前工作位；退休时多一条有界记录和必要快照保存。每会话独立 builder 增加短期内存占用，旧候选随 worker 结束释放。
失败路径最多三轮、12 秒后台读取/删除/核验，争用时主动让出；交接最坏 5 秒，end 本身不等待。
账本明细总量受条数和字节双限，日志按尝试数有界。实际耗时和内存增量需要同环境测量，当前没有基准数字。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 既有三条 Issue2 先红用例移入正式回归 | recent-only 实际派发删除；当前/恢复路径不固化旧未知；均由红转绿 |
| recents 读取失败、非零退出、格式无法识别、真实空集合 | 四类事实可区分；未知不伪造成空集合/confirmed |
| 删除命令退出成功但 recents 仍含目标 | cleanup 仍 unconfirmed；新会话仍可尝试 |
| 三轮持续失败/预算耗尽 | state unconfirmed、计数有界、can_start 不由旧记录封锁；再次 end/status 不重启补偿或引导无限轮询 |
| end/STOP 与慢设备、慢账本、日志阻塞竞争 | 撤权立即完成；请求不等交接和补偿 |
| 未知 UI/shell/late-create 在交接期间不返回 | 最迟 5 秒释放当前逻辑位置，原 P 不无限 running；unknown 不变 confirmed |
| 旧 callback/cleanup/releaseWork 晚于新 start | 不改新 session/work/O/命令，不发布旧 O，不改原已冻结 outcome |
| 旧 Build/Preview 被门控在新 session 创建之后完成 | 使用原 builder，不能改变新节点保号/基线；对照去掉 builder 隔离必须失败 |
| 旧 cleanup 过滤命令 | 只能删原 command/attempt，不能删同 App 的新 create_display |
| App retiring 存在、旧 descriptor 未消失、旧 create 回调迟到 | 新 attempt 能开始；旧显示不冒充 current；旧 close 不影响新 generation |
| 新普通工作插入后台清理、持续繁忙 | 清理不挡普通准入；未派发清理取消/到期停止；已派发结果只归原记录 |
| 同 boot recent-only / 新归属冲突 / 跨 boot | 仅第一类且归属充分时允许精确删除；其余停止破坏性补偿 |
| v1/v2 恢复含 legacy/UI/shell 未确认 | 保存后转 retired，新准入不要求手机重启；未知事实仍存在，原动作不重放 |
| 迁移写失败、进程退出窗口、并发快照保存 | 保留旧责任，新副作用未开始；重启不刷新期限/轮数，不覆盖新归属 |
| 补偿前保存退休记录后立即退出、墙钟回拨再启动 | 原 retired 的 pending 先保存为 unconfirmed，不再授予预算；无无限补偿或旧门禁 |
| 8 条或 24 KiB 容量满、历史路由被淘汰 | 停止/压缩旧项而非拒绝新 start；不误报 complete，旧 reference 不指向新会话 |
| status(P/O)/重复 end 与新会话并存 | session/cleanup 路由原归属，诊断内容受 Agent 权限隔离 |
| operation/observation 与 retired 明细先后淘汰 | 未知 P 不冒充未接纳；未知 end 只封闭该 P 的未来准入，绝不结束另一个会话 |
| strict JSON/schema/MCP 投影 | 新字段零值/枚举一致；请求仍拒绝未知字段/null；不新增工具或自动动作重试 |

缺陷回归保留真实先红证据；新守卫至少用去掉归属/期限/builder 隔离的 overlay 做有意义反向对照。
需要修订既有“永久保留屏障”的测试期望，并注明它是已批准契约变化，不能只删掉测试。

### 8.2 实机/系统级

以下均为计划，**本提案对应实现尚未进行实机验证**。已有手机恢复只证明现行版本恢复路径。

1. 可控测试应用正常启动/操作/end，确认任务、recents 和显示实际清理，并核对新会话实际能 start。
2. 包装 ADB 仅令有界次数的 recents 读取失败，或令旧清理后验持续显示合成未确认；不隐藏真实普通输入，finally 恢复正常通路。
   end 后无需重启服务/手机或改账本，即可新 start；持续故障时补偿停止、旧事实仍 unconfirmed。
3. 在专用虚拟屏构造 live 已消失、recents 仍含旧 owned ID 的受控状态，验证本机 ROM 的 recent-only 删除与重读。
   无法稳定构造时记录未执行及原因，不以 fake 结论替代。
4. 新会话 active 时让原释放回执/旧清理完成，确认新屏/新任务/O 不受影响。
5. 带 pending/unconfirmed 和多个历史记录重启服务，原 pending 保存为 unconfirmed，恢复当前 App 后建立新会话；不手机重启、不删账本、不清 App 私存。
6. legacy 与未知执行使用隔离合成账本/受控 runner 验证，避免在用户主屏制造真实迟到点击或任意 root 命令。
   未覆盖真实在途风险必须单列，不宣称由模拟证明物理隔离。

验收指标：端到端 start 实际成功；旧物理未知不成为拒绝码；当前能力就绪后旧交接阻塞 ≤5 秒；
补偿派发在退休后 ≤12 秒停止、轮数 ≤3；end/STOP 门控测试不等待设备/磁盘；无旧动作重放和新资源误清；账本 ≤64 KiB。
计时使用单调时钟和明确起止事件，不能把 App 本来不在线的时间算作旧交接造成的阻塞。
构建、手机文件系统专项与真实设备行为分别记录；发布前按 manifest 验证产物，并重启实际 MCP 宿主发现契约 5。

### 8.3 记录

实现验证写入 `maintenance/verification/cleanup-without-permanent-gate.md`，注明实现提交、环境、
实际命令/结果、先红转绿、反向对照、手机与模拟的证明边界、未执行项及原因。
本次归因记录继续保留，不用未来实现结果覆盖原始失败证据。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 用户确认的方向 | 同时修复当前 bug，取消旧会话清理失败导致的永久准入门禁 |
| 提案范围建议 | 覆盖物理清理及退休后的未知执行/创建链，避免改名后继续无限封锁；不提供强制跳过开关 |
| 事实语义 | 放行/停止补偿不等于 confirmed；原 P 不追认、不重放 |
| 实现路线建议 | R3：当前与 retired 分离，有界交接/补偿，归属不足停止处理 |
| 数值建议 | 交接 5s、补偿总计 12s/最多三轮、8 条且 24 KiB 明细；均需随本提案整体确认 |
| 公开面建议 | 12 工具及 Agent 字段不增加，复用清理状态/错误/recovery；共同契约 5；内部新增 cancel_create |
| 版本建议 | release 0.5.0、Android versionCode 7、账本 3；实施前复核版本源和分支基线 |
| 本次授权边界 | 用户整体确认本文并授权按规范实现、发布、更新 build 与部署；禁止手机重启 |

### 9.2 未决

| 事项 | 谁决定、何时关闭 |
| --- | --- |
| 接受未知旧设备调用与新会话物理重叠（含主屏/root）的范围，以及 5s 后脱离策略 | 2026-11-11 用户整体确认；旧命令不保证停止，按本文范围接受物理重叠风险 |
| 数值、未知引用语义、迁移/容量策略是否按本文采用 | 2026-11-11 用户整体确认本文默认值、引用语义、迁移与容量策略 |
| 源头最终修订文件集合及本地直接受影响文档 | 实现前按批准稿逐条扫描、由独立只读审计复核；不是延后决定产品判据 |

前两项已在用户整体确认时关闭。技术细节发现与本文判据/时序冲突时，回到提案修订并重新确认。

## 10. 实施顺序

1. 确认本文范围、风险接受与具体值；状态继续保持“提案，未实现”。
2. 先在设计源头完成本主题 docs-only 修订；同步计划修改的本地 AGENTS/决策约束，避免实现暗改语义。
3. 复核三个先红用例与基线；补新旧归属/迟到 callback/builder/App retiring 的必要先红用例。
4. 定义 contract 5 与 ledger 3 的完整 schema/迁移；engine retired 模型和 App attempt 隔离可在接口固定后分开实现。
5. 实现归属路由、交接、准入、命令过滤、builder 隔离；再接入 recent-only 修复和有限补偿。不能先放开门禁再补隔离。
6. 完成当前与恢复的 UI/shell/legacy 分支、容量停止/淘汰与所有投影；运行专项、反向对照，再做规定回归。
7. 同步实际具备的本地 docs；委派独立只读文档审计，等待结论，修正后复检为 clean。
8. 实现提交落盘（提案状态不提前改）；生成版本/构建 manifest，按批准范围部署和实机验收，记录真实结果。
9. 最后归档提案：写实现 hash、补 §11、移动到 implemented、重写相对链接，另提交归档与验证记录。

## 11. 实现结果（归档时补写）

尚无实现提交；本节不声明任何实现或验收完成。归档时填写实现 hash 和实际验证记录链接。

### 11.1 已落地

| 提案条目 | 代码落点 |
| --- | --- |
| — | 提案阶段，未实现 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 实现、发布与本提案验收 | 未执行；当前仅获授权起草提案 |

---

## 附：本提案不改变的事项

- STOP 立即撤权；已派发 Android 命令不保证撤回。
- 不恢复旧会话输入权，不重放原 P，不把后观察或清理成功改成原动作成功证据。
- 主屏只在用户当前确认后共享；结束不自动关闭用户已有应用任务。
- HTTP/MCP/App/客户端不新增执行权威或自行访问设备；日志、消息与隐私边界保持。
- 版本唯一源、清单核验、独立文档审计和先实现提交再归档要求保持。
