# 提案：加固 app 跨屏启动归属核验、虚拟屏前台彻底终止与未确认恢复收敛

状态：已实现（实现提交 `cabfd40c627c2b829cd00a20dc1730605cce1044`，2026-11-22）。
发布交付与验收见[验证记录](../../verification/app-window-task-lifecycle-hardening.md)。
基线提交：`00394dd55898dd1faea6eb218db7f0f3ccacad5b`（分支 `master`，`2026-11-22`）。
适用范围：`POST /api/app`、Go 客户端与 MCP `app` 工具的 `open`、`kill` 及变更分支 `uncertain` 恢复投影；覆盖 Issue #35 与 Issue #39。
影响范围：`internal/engine/openapp.go`、`internal/engine/app_kill.go`、`internal/engine/app.go`、`internal/engine/recovery.go`、`internal/engine/openapp_test.go`、`internal/engine/app_lifecycle_test.go`、`internal/engine/recovery_entry_test.go`、`internal/engine/internal_uncertain_test.go`、`internal/engine/ui_execution_test.go`、`internal/mcp/schema.go`、`internal/mcp/schema_test.go`、`internal/mcp/result_parity_test.go`、`scripts/mcp-probe/app_suite.go`、`scripts/mcp-probe/app_suite_test.go`、`scripts/mcp-probe/probe_test.go`、`docs/interface.md`、`docs/mcp.md`、`docs/integration.md`、`docs/architecture.md`、`docs/client.md`、`docs/control-protocol.md`、`docs/phone-operations.md`、`docs/release.md`、`docs/runbook.md`、`README.md`、`release/versions.json` 及版本生成产物。
契约（`/api/*` 字段与 `contract_version`）：改变。`/api/app` 请求与响应 JSON 字段名和结构不变，但两项执行与恢复语义发生变化：一是虚拟屏下 `app.kill` 不再拒绝本屏前台自有（`owned`）任务，并在确认进程结束（或确认进程集本为空）后同步移除该任务窗口容器（`RemoveTask`）与会话任务登记（仍拒绝主屏/他屏有任务、同屏多窗口或非 `owned` 目标）；二是 `app` 变更分支的 `finished + outcome=uncertain` 恢复投影 `recovery.entry` 从 `status` 收敛为 `observe`。按 [release.md](../../../docs/release.md#L51-L52) 的执行语义变化规则，`contract_version` 由 `23` 前进为 `24`（拟 `release_version` `0.27.0` → `0.28.0`、`android_version_code` `29` → `30`，实施冻结时核对未被占用）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：`internal/engine/openapp.go`、`internal/engine/app_kill.go`、`internal/engine/app.go`、`internal/engine/recovery.go`、`internal/mcp/schema.go`、`scripts/mcp-probe/app_suite.go`）。

---

## 0. 摘要

聚焦 Issue #35 与 Issue #39 暴露的窗口任务生命周期三处失调，做三项最小且闭环的语义与实现修正：

1. **修复跨屏同包下 `app.open` 新建归属核验与后验兜底收编（#39）**：`appOpen` 改用单次全设备 `TaskList` 建立启动前已知任务基线（并从中过滤出目标屏候选供 `selectReusableTask` 复用），`verifyLaunchedTask` 只匹配启动前全设备均不存在的新 `TaskID` 并区分目标屏与他屏，彻底消除主屏既有同包任务（含刚 `handoff` 出去的任务）造成的伪多任务歧义；若虚拟屏 `LaunchApp` 回执未知或 2.6 秒轮询期内任务未出齐，但在进入 `finishAppUncertainWork` 后置观察完成后确认目标虚拟屏出现唯一新建同包任务，仍通过返回 `bool` 的条件化 helper `rememberOwnedTask` 将其登记为 `owned`，并按已证实事实与登记结果回填 `AppView` 字段（保持 `outcome=uncertain` 不改判）。
2. **彻底化虚拟屏 `app.kill`：允许终止虚拟屏前台自有应用并同步拆除任务窗口壳（#35）**：在虚拟屏模式下取消 `killGuard` 的本屏前台拒绝（`kill_refused_foreground`），保留主屏/他屏同包任务拒绝（`kill_refused_task_elsewhere`）、同屏多窗口共享拒绝（`kill_refused_process_shared`）、非 `owned` 拒绝（`kill_refused_not_owned`）与 FGS/播放保护；在确认原进程全部结束（或确认原进程集合为空）后，同一工作内检查并执行 `RemoveTask` 移除任务容器壳并从 `session.Tasks` 移除，消除残留死壳及退栈后的「空树前台」污染。
3. **消除 `app` 终态 `uncertain` 恢复入口自指死循环（#39）**：将 `recovery.go` 中 `appPlan` 对 `OutcomeUncertain` 的映射从 `entry=status, reference=P` 修正为 `entry=observe, reference=P, input_state=retired`（与原设计文档 [unified-app-lifecycle.md](unified-app-lifecycle.md#L280) 及 `mcp-probe` 矩阵一致），保留 `CodeTaskLifecycleBusy` 走 `status/P`。

明确不做：不在只读 `app.list` 中自动收编陌生任务；不改变 `app.close`、`app.claim`、`app.handoff`、`act`/`shell` 的现有语义；不放开主屏上的 `kill`/`close`/`claim`/`handoff`；不修改 Android 控制 App 业务逻辑或固定 `uiautomator` 制品。

---

## 1. 问题

### 1.1 现象与代价

1. **Issue #39（`handoff` 后 `open` 同包必现 `uncertain` 且产生幽灵 `shared` 任务）**：
   - 虚拟屏会话执行 `app.handoff` 将任务 `#213`（`tv.danmaku.bili`）移回主屏 `display=0` 后，紧接着在虚拟屏 `display=174` 调用 `app.open target.app=tv.danmaku.bili`。
   - 手机已成功在虚拟屏创建新任务 `#214` 且同帧后置观察已经完整采集到前台页面（`fg=tv.danmaku.bili#214`，约 110 个节点），但本次调用却返回 `504 app_launch_unconfirmed`、`outcome=uncertain`，`app` 对象缺失 `task_id`、`display_id` 与 `ownership`。
   - `result.recovery` 给出 `entry=status ref=<本次 op> input=retired`；调用方按恢复指引调 `status(op)`，只回放 `state=finished outcome=uncertain phase=dispatch`，`recovery` 继续自指同一 `op`，陷入死循环。
   - 由于 `#214` 未进入 `session.Tasks`，后续 `app.list` 将其标为 `shared`，`app.kill` 报 `409 kill_refused_not_owned`，`app.close`/`app.handoff` 报 `409 task_not_tracked`，`app.claim` 因只查主屏也无法接管，成为「看得见、点得着、管不了」的幽灵任务。
2. **Issue #35（`app.kill` 前台操作死结与死壳污染虚拟屏）**：
   - 虚拟屏只有网易云 `#201` 在前台时，`app.kill target={task_id:201}` 被 `409 kill_refused_foreground` 拒绝；调用方被迫先 `open` 另一个应用 `#202` 切走前台，再 `kill` `#201` 才得到 `executed`。
   - `kill` 成功后只结束了 PID，`#201` 的 `TaskRecord` 容器仍在，导致 `app.list` 仍列出 `#201 com.netease.cloudmusic owned`。
   - 随后调用方 `app.close` 关闭 `#202` 后，虚拟屏任务栈将栈底死壳 `#201` 顶回前台，观察返回 `obs ... fg=com.netease.cloudmusic#201` 与 `tree v4 full nodes=1`（仅有 `n1 root`），在树结构上与「正常应用白屏加载中」完全不可区分。

### 1.2 证据链

| 断言 | 基线证据（`file:line`） |
| --- | --- |
| `appOpen` 启动前只调用单屏 `TasksOnDisplay(work.Ctx, session.Display.ID)` 采样 `preTasks`，丢弃了主屏及其他显示屏的同包任务 | [openapp.go:58](../../../internal/engine/openapp.go#L58)、[launch.go:292-307](../../../internal/android/launch.go#L292-L307) |
| `verifyLaunchedTask` 用单屏 `preTasks` 构造 `known`，却在轮询中调用全设备 `e.dev.TaskList(work.Ctx)` 过滤 `!known[task.TaskID]`；主屏既有任务 `#213` 与虚屏新任务 `#214` 同时命中导致 `len(matches) > 1` 在第 0ms 直接返回 `nil, false, false` | [openapp.go:477-522](../../../internal/engine/openapp.go#L477-L522)（`known` 在 `:479-482`，全设备 `TaskList` 在 `:496`，多匹配拒绝在 `:514-515`） |
| 若主屏有 1 个既有同包任务而虚拟屏未及时出现新任务，`len(matches) == 1` 且 `DisplayID != session.Display.ID` 会将主屏既有任务误标为 `moved = true`（`application_moved`） | [openapp.go:508-513](../../../internal/engine/openapp.go#L508-L513)、[openapp.go:241-248](../../../internal/engine/openapp.go#L241-L248) |
| `LaunchUnknown`（`:220-226`）与 `verifyLaunchedTask` 返回 `!ok`（`:234-240`）直接进入 `finishAppUncertainWork`，跳过 `:250` 的 `e.rememberTask(session, *task, true)` | [openapp.go:220-256](../../../internal/engine/openapp.go#L220-L256) |
| `finishAppUncertainWork` 在 `:267` 调用 `appPostObservation` 并于 `:272` 完成操作，但未结合最新任务列表将本屏已落地的唯一新任务补记进 `session.Tasks` 或回填 `AppView` | [app.go:248-275](../../../internal/engine/app.go#L248-L275)；对应字段规则已在原提案 [unified-app-lifecycle.md:219](unified-app-lifecycle.md#L219) 定义 |
| 未进入 `session.Tasks` 的虚拟屏任务在 `appList` 默认给 `shared`，在 `appClose`/`appHandoff` 报 `task_not_tracked`，在 `appKill` 报 `kill_refused_not_owned`，`appClaim` 只读主屏 `0` | [app_query.go:224-231](../../../internal/engine/app_query.go#L224-L231)、[app_close.go:232-236](../../../internal/engine/app_close.go#L232-L236)、[app_transfer.go:199](../../../internal/engine/app_transfer.go#L199)、[app_transfer.go:576-579](../../../internal/engine/app_transfer.go#L576-L579)、[app_kill.go:187-192](../../../internal/engine/app_kill.go#L187-L192) |
| `appPlan` 将 `OutcomeUncertain` 映射为 `RecoveryEntryStatus`；`statusOperationLocked` 对 `OperationFinished` 操作调用 `operationViewLocked` 原样回放 `op.Recovery`，形成 `status(op)` 自指死循环 | [recovery.go:270-272](../../../internal/engine/recovery.go#L270-L272)、[status.go:111-154](../../../internal/engine/status.go#L111-L154)、[engine.go:1277-1281](../../../internal/engine/engine.go#L1277-L1281) |
| 与之矛盾的是，原提案与 MCP/probe 已按 `observe/P` 验收 `app` 终态 `uncertain`：`resolveObserveRecoveryP` 显式识别 `outcome=="uncertain"` | [unified-app-lifecycle.md:280](unified-app-lifecycle.md#L280)、[result_text.go:1770-1779](../../../internal/mcp/result_text.go#L1770-L1779)、[app_matrix_test.go:184](../../../scripts/mcp-probe/app_matrix_test.go#L184)、[app_matrix_test.go:427](../../../scripts/mcp-probe/app_matrix_test.go#L427) |
| `appKill` 在 `KillProcess` 与 `awaitKillVerification` 确认进程结束后直接调用 `finishAppExecuted`，从未调用 `RemoveTask` 或 `removeSessionTaskLocked`，残留死任务壳继续出现在 `am stack list` | [app_kill.go:105-162](../../../internal/engine/app_kill.go#L105-L162)、[dumpsys.go:258-303](../../../internal/android/dumpsys.go#L258-L303)、[process.go:477-491](../../../internal/android/process.go#L477-L491) |
| `killGuard` 读虚拟屏 `Foreground` 并在匹配 `task.Package` 时以 `kill_refused_foreground` 拒绝；而主屏禁止已由 `appModeGuardLocked` 保障，跨屏同包已由 `otherDisplay` 拒绝 | [app.go:132-142](../../../internal/engine/app.go#L132-L142)、[app_kill.go:193-222](../../../internal/engine/app_kill.go#L193-L222) |
| `KillProcess` 发信号前要求通过 `/proc/<pid>/stat` 重验 `StartTicks`，若进程已消失则返回 `instance_gone`（`DispatchNotDispatched`） | [process.go:459-469](../../../internal/android/process.go#L459-L469) |

### 1.3 为什么直觉会出错

- **“先 `RemoveTask` 再 `KillProcess` 也行”**：错误。Android `am stack remove` 在无后台服务时可能促使系统提前回收进程，导致紧随其后的 `KillProcess` 在 `/proc/<pid>/stat` 校核阶段命中 `instance_gone`（[process.go:464-466](../../../internal/android/process.go#L464-L466)），将本可确认的清理打成 `kill_unconfirmed`。必须**先终止并核验原进程实例，再拆除任务窗口壳**。
- **“既然在虚拟屏上，`app.list` 和 `killGuard` 直接把本屏所有未登记任务当作 `owned` 即可”**：错误。`app.list` 是纯只读查询（[app_query.go:160](../../../internal/engine/app_query.go#L160)），且现有契约测试显式断言未登记任务在 `list` 中保持 `shared`、未 `owned` 的任务在 `kill` 中被 `kill_refused_not_owned` 拒绝（[app_lifecycle_test.go:41-65](../../../internal/engine/app_lifecycle_test.go#L41-L65)、[394-404](../../../internal/engine/app_lifecycle_test.go#L394-L404)）；更重要的是，若应用因 `singleTask` 把用户主屏既有任务吸到了虚拟屏，盲目认领为 `owned` 会导致 `end` 收尾误删用户主屏任务。正确做法是从源头用全设备 `preAllTasks` 修复 `app.open` 的新建归属判定与兜底收编。
- **“把 `verifyLaunchedTask` 改成只过滤 `task.DisplayID == session.Display.ID` 且不用改 `preTasks`”**：不完整。若不持有全设备 `preAllTasks`，就无法区分「目标屏没有新任务且他屏新起了一个任务（`application_moved`）」与「他屏本来就有一个旧任务」，也无法拦截主屏旧任务被 `singleTask` 隐式移入虚拟屏时被误当成新建 `owned` 任务。
- **“干脆禁止主屏也有任务的 `open` 即可”**：违背多屏独立操作能力——用户完全可能在主屏挂着微信/B站的同时，在虚拟屏单独开一个同包任务，跨屏并存本来就是虚屏会话的核心使用场景。
- **“`app` `uncertain` 既然已经返回了后置观察 `O_next`，`recovery` 应该直接给 `entry=retry, ref=O`”**：错误。`retry` 语义是「什么都没派发，用同一套参数重调同一工具」，对已进入派发的 `open`/`kill`/`claim`/`handoff`/`close` 返回 `retry` 会诱导调用方重放副作用。正确入口是 `observe/P`（告知先观察/查询核对现场、不要重放；若响应已附带新 `O_next` 则调用方可直接使用）。

---

## 2. 目标与非目标

**目标：**

1. **跨屏同包 `app.open` 精确归属（#39）**：无论主屏或其他屏幕是否存在同一包名（包括刚 `handoff` 出去的任务），虚拟屏 `app.open` 在目标屏拉起唯一新任务时必定返回 `outcome=executed`、`ownership=owned` 与精确 `task_id`/`display_id`，并写入 `session.Tasks`。
2. **`app.open` 未确认时同步留痕已知事实（#39）**：当 `LaunchApp` 回执未知或超时未能及时完成归属轮询，但在后置观察前/后已能从实时 `TaskList` 证明目标虚拟屏上多出唯一新任务时，将其登记进 `session.Tasks`（`owned`）并在 `uncertain` 结果的 `app` 对象内返回 `task_id`、`display_id`、`ownership=owned`、`reused=false`、`state_preserved=false`；始终不因后验观察改判 `OutcomeUncertain`。
3. **虚拟屏 `app.kill` 彻底终止与前台可用（#35）**：在虚拟屏模式下，允许直接对本屏前台唯一 `owned` 任务调用 `app.kill`；若该应用在主屏或其他屏有任何 live 任务，仍以 `409 kill_refused_task_elsewhere` 拒绝；确认原进程实例终止（或进程集已空）后，自动移除 `source_task_id` 对应的窗口壳并从 `session.Tasks` 清除，使 `app.list` 与虚拟屏后续观察不再出现死任务壳与空树假前台。
4. **`app` 终态 `uncertain` 恢复收敛（#39）**：`app` 五个变更 kind 以 `OperationFinished + OutcomeUncertain` 结束时，首次响应与 `status(P)` 一致返回 `recovery: {entry: "observe", reference: "P", input_state: "retired"}`，彻底消除 `status(P)` 自指死循环。

**非目标：**

- 不改变 `app.list` 的纯只读属性，不在只读查询中自动把未登记任务改写为 `owned`。
- 不允许 `app.kill` 在主屏模式运行，不取消 `kill_refused_task_elsewhere`、`kill_refused_process_shared`、`kill_refused_not_owned`、`kill_refused_playback`（未传 `allow_stop_playback=true`）与 `root_unavailable` 保护。
- 不将 `app.kill` 改为 `am force-stop`，不自动追杀应用后台重起的新实例；不强制把 `RecentTasks` 未确认作为 `kill` 失败判据。
- 不改变 `app.close`、`app.claim`、`app.handoff` 的权限模型、字段形状与 `end` 默认收尾行为。
- 不改变 `act`、`shell`、`wait_for`、`observe`、`snap` 的任何行为（Issue #36 不在本提案范围内）。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 | 结论 |
| --- | --- | --- | --- | --- |
| R0 维持现状 | 不改动代码与契约 | 零 | #35 与 #39 实机必现；`handoff` → `open` 产生幽灵任务且 `status` 死循环；`kill` 留死壳且前台无法直杀 | 否决 |
| R1 最小补丁（仅修 `preTasks` + `recovery`） | 只改 `openapp.go` 跨屏过滤与 `recovery.go`，不动 `app.kill` | 极小，不改 `kill` 语义 | #35 依然存在：虚拟屏单应用前台死结仍在，`kill` 后死壳仍留 `app.list` 并以 `nodes=1` 污染退栈前台 | 否决 |
| **R2 本提案选定路线** | 1) `appOpen` 全设备 `preAllTasks` 基线 + 迟到唯一新任务收编；2) 虚拟屏允许前台 `owned` 任务直杀并在进程结束后移除任务壳与登记；3) `appPlan` `uncertain` 收敛为 `observe/P` | 需前进 `contract_version`（23→24），同步 `open` 两个 `hookedDevice` 测试、`kill` 前台测试、MCP 描述、探针与文档 | 若未处理 `Android` 随进程死亡自动销毁 `TaskRecord` 的情况，`RemoveTask` 可能因任务已消失返回非零错误（已由前置 `TaskList` 检查规避） | **选定** |
| R3 引入死任务协议态 | 保留 `kill` 留下任务壳，新增 `dead` 枚举并在观察层区分死壳白屏 | 需新增 `AppTaskItem` / `Foreground` 协议字段与解析逻辑，仍要求调用方先绕过前台再 `kill` + `close` | 协议膨胀；`kill` 一次任务仍需 3 步绕行（切前台 → `kill` → `close`），违背用户「彻底关闭」意图 | 否决 |

---

## 4. 判据

### 4.1 `app.open` 启动前基线、跨屏归属核验与未确认收编

1. **全设备启动前基线**：`appOpen` 预检阶段在获取普通工作位后，调用一次全设备 `preAllTasks, preErr := e.dev.TaskList(work.Ctx)`（失败返 `503 device_unavailable`）。从中按 `task.DisplayID == session.Display.ID` 过滤得到 `preDisplayTasks` 供 `selectReusableTask` 判断复用；同时将完整的 `preAllTasks` 传入 `finishOpenVirtual` 与 `finishOpenPrimary`（两者统一调用 `verifyLaunchedTask`）。
2. **新任务精确归属（`verifyLaunchedTask`）**：
   - 将 `preAllTasks` 中全部 `task.TaskID > 0` 记入 `known` 集合。
   - 在原 5 轮有界轮询（`0, 200ms, 400ms, 800ms, 1200ms`）中读取 `TaskList`：
     - **每轮重置候选**：只要当轮 `TaskList` 读取成功（`err == nil`），先重置 `candidate = nil, moved = false`，再基于当轮最新快照计算新增集合，避免前序轮次落过 `len(elsewhere) == 1` 后、后续轮次退化为多他屏歧义（`len(elsewhere) > 1`）或任务已消失时残留旧 `candidate`。若某轮 `TaskList` 短暂读取失败（`err != nil`），直接重试下一轮。
   - 筛选**同时满足** `task.Package == app && task.TaskID > 0 && !known[task.TaskID]` 的新增任务，按是否位于目标屏拆分为两集：
     - `onTarget`：`task.DisplayID == session.Display.ID`；
     - `elsewhere`：`task.DisplayID != session.Display.ID`。
   - 判定规则：
     - `len(onTarget) == 1`：唯一确定为本次新建目标任务，立即返回 `&onTarget[0], false, true`；
     - `len(onTarget) > 1`：目标屏同时新增多个同包任务，归属存在歧义，保持现行安全约束返回 `nil, false, false`（进入 `CodeAppLaunchUnknown`）；
     - `len(onTarget) == 0 && len(elsewhere) == 1`：记下当轮候选 `candidate = &elsewhere[0], moved = true` 并继续等待后续轮询；若全部轮询结束仍无 `onTarget` 且最终有效 `candidate != nil`，返回 `candidate, true, true`（触发 `CodeAppMoved`）；
     - `len(onTarget) == 0 && len(elsewhere) != 1`：因当轮已重置 `candidate`，不残留旧候选，继续轮询直到结束，最终返回 `nil, false, false`。
   - **主屏既有任务隔离**：启动前已存在于 `display=0` 或其他屏的任务在 `known` 中，绝不会混入 `onTarget` 或 `elsewhere`；即使目标应用因 `singleTask` 将主屏原有任务隐式移到了虚拟屏，由于它在 `known` 中，也不会被误判为新建 `owned` 任务登记入 `session.Tasks`。主屏 `finishOpenPrimary` 同样使用 `preAllTasks` 运行 `verifyLaunchedTask`（主屏成功仍按 `shared` 不登记，也不进入虚拟屏兜底收编）。
3. **虚拟屏 `uncertain` 分支下的后验唯一任务收编（`reconcileOpenVirtualTask`）**：
   - **适用条件**：仅当 `session.Mode == "virtual"` 且 `finishOpenVirtual` 因 `LaunchUnknown`/`launchErr != nil`（`openapp.go:220`）或 `verifyLaunchedTask` 返回 `!ok`（`openapp.go:234`）进入 `finishAppUncertainWork` 时触发；`finishOpenPrimary` 不执行兜底登记。
   - **主动作终态不改判**：`Outcome` 严格保持 `OutcomeUncertain`（`504 app_launch_unconfirmed`），绝不因后置观察或后验任务列表成功而改写为 `executed`（遵守硬约束第 8 条）。
   - **已证实落点与条件化登记（`rememberOwnedTask`）**：在 `appPostObservation` 完成之后、`finishOperation` 冻结结果之前，若工作与原会话仍有效（`!e.detachedWorkRevoked(work)` 且 `e.sessionForWork(work) != nil` 且 `session.Mode == "virtual"`），执行一次只读 `TaskList` 核对：
     - 若存在**唯一**新增同包任务落在目标虚拟屏（`len(onTarget) == 1`，依然排除 `known` 中的旧任务）：
       1. 在 `AppView` 上填充已证实的启动/落点物理事实：`task_id = onTarget[0].TaskID`、`display_id = session.Display.ID`、`reused = false`、`state_preserved = false`（与 [unified-app-lifecycle.md:219](unified-app-lifecycle.md#L219) 一致）；
       2. 在引擎短锁内调用返回 `bool` 的 `e.rememberOwnedTask(work, session, onTarget[0])` 执行条件化提交：重验工作未撤销、原会话未结束且 `Epoch` 未变、`Mode == "virtual"`、`!e.transferProtectsLocked(onTarget[0].TaskID)`，若任务尚未入表则校验 `len(session.Tasks) < maxTrackedTasks`（与既有 `rememberTask` 容量约束一致）后记为 `TaskOwned` 并异步落盘，若已在表则校验其为 `TaskOwned`——全部满足才返回 `true`；
       3. **仅当 `rememberOwnedTask` 返回 `true` 时**，才在 `AppView` 上回填 `ownership = "owned"`；若因并发撤销、迁移保护或容量上限等原因返回 `false`，省略 `ownership`，不得伪造自有声明。
     - 若 `len(onTarget) == 0 && len(elsewhere) == 1` 且 `view.TaskID == nil`：只填充 `task_id = elsewhere[0].TaskID`，保持 `display_id` 与 `ownership` 省略。
     - 若 `len(onTarget) != 1`（零个或多任务歧义）：保持原样，不伪造 `task_id`/`display_id`/`ownership`。

### 4.2 `app.kill` 虚拟屏前台许可、跨屏拒绝与任务窗口移除

1. **前置与近派发守卫（`killGuard`）**：
   - 按固定先后顺序执行并近派发重验以下守卫：
     1. `appModeGuardLocked`：主屏模式在消费 O 前以 `409 window_action_not_supported_on_primary_display` 拒绝；
     2. `ControlPackage`：控制 App 拒绝（`409 control_ui_on_virtual_display`）；
     3. 直播任务在场：`TaskList` 验证 `live != nil && live.DisplayID == session.Display.ID && live.Package == task.Package`（否则 `409 task_not_tracked`）；
     4. **先判自有所有权**：在锁内校验 `entry != nil && entry.Ownership == TaskOwned`（否则 `409 kill_refused_not_owned`）；
     5. **再判迁移保护**：同一持锁校验 `!e.transferProtectsLocked(task.TaskID)`（否则 `409 task_lifecycle_busy`，与 `revalidateCloseTarget` 一致；排在 `kill_refused_not_owned` 之后，避免非自有任务误报生命周期忙）；
     6. 跨屏同包禁止：`candidate.Package == task.Package && candidate.TaskID != task.TaskID && candidate.DisplayID != session.Display.ID`（包括主屏 `0` 在内的任一他屏仍有该包任务 → `409 kill_refused_task_elsewhere`）；
     7. 同屏共享进程禁止：`candidate.Package == task.Package && candidate.TaskID != task.TaskID && candidate.DisplayID == session.Display.ID`（`409 kill_refused_process_shared`）；
     8. FGS / 媒体播放保护：`!req.AllowStopPlayback` 且存在已知或未知的 FGS / 播放（`409 kill_refused_playback`）；
     9. root、boot 身份、应用 UID 与 `PackageProcesses` 精确实例集合核验。
   - **移除的守卫**：删除 `app_kill.go:214-222` 对虚拟屏 `e.dev.Foreground(work.Ctx, session.Display.ID)` 的 `kill_refused_foreground` 拒绝。虚拟屏上处于前台的唯一 `owned` 任务（且主屏/他屏无任务）可直接 `kill`。
   - **错误码枚举保留**：`protocol.CodeKillForeground` 常量、HTTP `409` 状态映射与 `appRefusalCodes` 表项保留，不打破协议封闭码表向后可读性。
2. **执行顺序与彻底销毁判据（`appKill` 与 `killDispatched` 分层）**：
   - 记录 `killDispatched := len(final.instances) > 0`。
   - **第一步（进程终止）**：
     - 若 `len(final.instances) == 0`：`processesGone = true`，**先不预填** `killed_pids`/`remaining_pids`/`restarted`（延后至确认壳已消失或实际派发 `RemoveTask` 后再填，防止尚未发任何设备指令就撤销时在 `not_executed` 视图中泄露已终止统计字段）。
     - 若 `len(final.instances) > 0`：对每个 `instance` 发一次精确 `KillProcess(work.Ctx, instance.PID, instance.StartTicks)`，再由 `awaitKillVerification(work, final)` 核验原实例全部消失，得 `processesGone = readOK && len(killed) == len(final.instances) && allTerminated`；不追杀重启产生的新实例。
   - **第二步（窗口壳与会话登记移除，`removeKilledTask` + `forgetKilledTask`）**：
     - 若 `!processesGone`（此时 `killDispatched == true`）：**不派发 `RemoveTask`**，保持 `session.Tasks` 登记不变并以 `CodeKillUnconfirmed`（`OutcomeUncertain`）结束。
     - 当 `processesGone == true`：
       - 若进入移除前 `e.workRevoked(work)` 已为真：不派发 `RemoveTask`；
         - 若 `!killDispatched`（零信号且未派发 `RemoveTask`，设备端无任何已派发副作用）：走 `finishAppError` 按工作撤销码（如 `CodeStopped`）以 `OutcomeNotExecuted` 收口，并省略 `killed_pids`/`remaining_pids`/`restarted`；
         - 若 `killDispatched`（原进程已被发信号终止）：保留 `session.Tasks` 并以 `OutcomeUncertain` + `CodeKillUnconfirmed` 收口。
       - 否则读取一次 `tasks, listErr := e.dev.TaskList(work.Ctx)`：
         - 若 `listErr == nil` 且 `task.TaskID` 已不在 `tasks` 中（说明 Android 在最后一个进程死亡时已自动销毁了该 `TaskRecord`）：若 `!killDispatched` 则补齐 `target_pids=[]`/`killed_pids=[]`/`remaining_pids=[]`/`restarted=false`；调用 `forgetKilledTask(epoch, task.TaskID)` 移除登记并持久化，得 `executed`。
         - 若 `task.TaskID` 仍在 `tasks` 中（或 `listErr != nil`）：派发前再次重验 `!e.workRevoked(work)`（撤销处理同上，区分 `!killDispatched` → `OutcomeNotExecuted` 与 `killDispatched` → `OutcomeUncertain`），随后调用一次 `removeRes, removeErr := e.dev.RemoveTask(work.Ctx, task.TaskID)`：
           - 当且仅当 `removeRes.Receipt == device.DispatchTerminated && removeErr == nil` 时确认任务壳已拆除：若 `!killDispatched` 则补齐空 PID 数组与 `restarted=false`；调用 `forgetKilledTask(epoch, task.TaskID)` 移除登记并异步持久化，得 `executed`。
           - 若回执为 `DispatchNotDispatched`：不因 `TaskList` 追认回执（遵守 [unified-app-lifecycle.md:151](unified-app-lifecycle.md#L151) 规则），保持 `session.Tasks` 登记不变；若 `!killDispatched`（未杀进程且 `RemoveTask` 也未进入派发，设备零副作用）：按 `finishAppError` 返回 `OutcomeNotExecuted`（撤销返回撤销码、超时/传输中断按既有预派发失败码映射，省略 `killed_pids`/`remaining_pids`/`restarted`）；若 `killDispatched`：进入 `OutcomeUncertain`（`CodeKillUnconfirmed`）。
           - 若回执为 `DispatchUnknown` 或带错（已进入派发但结果未确认）：保持 `session.Tasks` 登记不变，若 `!killDispatched` 则补齐 `target_pids=[]`/`killed_pids=[]`/`remaining_pids=[]`/`restarted=false`，进入 `OutcomeUncertain`（`CodeKillUnconfirmed`）。
       - **`forgetKilledTask(epoch, taskID)` 条件化删表**：短锁内参照 [app_transfer.go:677](../../../internal/engine/app_transfer.go#L677) 严格校验 `e.session != nil && e.session.Epoch == epoch && e.session.State != SessionEnded`，满足时从 `e.session.Tasks` 删除 `taskID` 并调用 `e.persistAsync()`，防止会话已结束或切换代次后跨会话误删。
   - **第三步（终态与后置观察）**：
     - `processesGone && taskRemoved` → `finishAppExecuted(work, op, view)`（随后的 `appPostObservation` 采集的是已移除死壳后的干净虚拟屏）；
     - `!processesGone` → `finishAppUncertainWork(..., CodeKillUnconfirmed, "已派发进程终止，但无法确认原实例全部结束", nil)`；
     - `processesGone && !taskRemoved && killDispatched` → `finishAppUncertainWork(..., CodeKillUnconfirmed, "已终止应用进程，但无法确认任务窗口已移除", nil)`；
     - `processesGone && !taskRemoved && !killDispatched`（且 `RemoveTask` 已进入派发但回执未知/失败）→ `finishAppUncertainWork(..., CodeKillUnconfirmed, "确认无运行进程，但无法确认任务窗口已移除", nil)`。

### 4.3 `app` 终态 `uncertain` 恢复投影

- `recovery.go` 的 `appPlan` 将 `in.Code == protocol.CodeTaskLifecycleBusy` 与 `in.Outcome == OutcomeUncertain` 分离：
  - `in.Code == protocol.CodeTaskLifecycleBusy`（前置拒绝，任务仍有旧搬移/删除责任在途）继续返回 `{entry: "status", reference: "P", input_state: in.InputState}`；
  - `in.Outcome == OutcomeUncertain`（`open`、`claim`、`handoff`、`close`、`kill` 五个变更分支已认领并派发但未确认）固定返回 `{entry: "observe", reference: "P", input_state: effectiveSafeState}`，其中 `effectiveSafeState` 取 `recoveryInputRetired(in.InputState)`，若为 `effective` 则归一为 `retired`（严格满足不变量 I4：`entry=observe` 时 `input_state` 不得为 `effective`）。
- 操作还在运行中（`op.State == OperationRunning`）调用 `status(P)` 时，`statusOperationLocked`（[status.go:118-124](../../../internal/engine/status.go#L118-L124)）仍优先返回 `{entry: "status", reference: "P", cause: "running"}`；一旦操作进入 `OperationFinished`，`status(P)` 回放 `op.Recovery` 即收敛为 `observe/P/retired`。

---

## 5. 算法

### 5.1 `app.open` 与未确认收编流程

```text
appOpen(ctx, op, req):
  t0  [锁内] appStaticCheckLocked + controlPackage 检查（未消费 O）
  t1  acquireWork(op, WorkAppAction)
  t2  preAllTasks, preErr := dev.TaskList(work.Ctx)     // 单次全设备快照
      if preErr != nil -> finishAppError(503 device_unavailable)
      preDisplayTasks := filterByDisplay(preAllTasks, session.Display.ID)
      foreground, _ := dev.Foreground(work.Ctx, session.Display.ID)
      reuse := selectReusableTask(preDisplayTasks, app, session.Display.ID, foreground)
      if reuse == nil -> ResolveLauncher(...)
      if virtual && newTask -> appCapacityRefusalLocked(...)
  t3  [锁内] claimObservationLocked（O: ready -> claimed）
  t4  ProbeDisplay + detachedWorkRevoked 重验
  t5  finishOpenVirtual / finishOpenPrimary:
        - reuse != nil: FocusTask -> verifyForeground -> rememberTask(virtual) -> finishAppExecuted
        - reuse == nil:
            beginUIDispatch("launch") -> LaunchApp -> recordUIReceipt
            if LaunchRejected / LaunchNotStarted -> finishAppError(503 app_launch_failed)
            if LaunchUnknown / DispatchUnknown / err != nil:
                markDispatchUnknown(work)
                // virtual 走 finishAppUncertainOpen；primary 保持 shared 不收编，直接 finishAppUncertainWork
                finishUncertainForMode(...)
            else:
                // virtual 与 primary 共用 preAllTasks 基线；每轮 TaskList err==nil 先重置 candidate=nil, moved=false
                task, moved, ok := verifyLaunchedTask(work, session, app, preAllTasks)
                if !ok -> markDispatchUnknown; finishUncertainForMode(...)
                if moved -> clearDispatchUnknown; finishAppUncertainWork(..., CodeAppMoved, ...)
                clearDispatchUnknown; rememberTask(virtual); finishAppExecuted(...)

finishAppUncertainOpen(work, op, session, app, preAllTasks, view, code, message):
  1. pubObs, postPhase := appPostObservation(work, op)  // 采集原虚屏 full 无图观察
  2. if session != nil && session.Mode == "virtual" && !detachedWorkRevoked(work) && sessionForWork(work) != nil:
       reconcileOpenVirtualTask(work, session, app, preAllTasks, view):
         all, err := dev.TaskList(work.Ctx)
         if err == nil:
           onTarget, elsewhere := diffNewPackageTasks(all, preAllTasks, app, session.Display.ID)
           if len(onTarget) == 1 && !workRevoked(work):
             view.TaskID = &onTarget[0].TaskID
             view.DisplayID = &session.Display.ID
             view.Reused = false
             view.StatePreserved = false; view.HasStatePreserved = true
             if rememberOwnedTask(work, session, onTarget[0]):  // 锁内重验工作/会话/epoch/保护/容量并返回 bool
               view.Ownership = TaskOwned
           else if len(onTarget) == 0 && len(elsewhere) == 1 && view.TaskID == nil:
             view.TaskID = &elsewhere[0].TaskID
  3. 组装 Result（Outcome 保持 OutcomeUncertain，Error 保持 code/message），finishOperation 写入 op.Recovery（observe/P/retired）
```

时间轴（Issue #39 复现场景）：

```text
t=0   handoff(#213) 完成：#213 在 display=0，display=174 无任务
t=1   appOpen(tv.danmaku.bili) 获取 preAllTasks = [#213@d0, #212@d174]
      preDisplayTasks = [#212@d174] -> reuse = nil -> known = {212, 213}
t=2   LaunchApp(display=174) -> Status: ok（#214 在 display=174 启动）
t=3   verifyLaunchedTask 读全设备 TaskList -> [#212@d174, #213@d0, #214@d174]
      过滤 !known -> 仅 #214（onTarget=[#214], elsewhere=[]）
t=4   rememberTask(#214, owned) -> finishAppExecuted -> 返回 outcome=executed, task_id=214, display_id=174, ownership=owned
```

### 5.2 `app.kill` 流程

```text
appKill(ctx, op, req):
  t0  [锁内] appStaticCheckLocked + appModeGuardLocked（主屏拒 409） + target.UseTask 检查
  t1  acquireWork(op, WorkAppAction)
  t2  resolveAppTarget(session.Display.ID, req.Target)
  t3  plan, err := killGuard(...)   // 校验 owned、transferProtectsLocked、无他屏任务、无同屏 sibling、
                                    // 无未授权 FGS/播放、root、UID 与 PID+StartTicks；不再检查本屏 Foreground
  t4  [锁内] claimObservationLocked
  t5  final, err := killGuard(...) + sameProcessSet(plan.instances, final.instances)
  t6  进程终止阶段:
        killDispatched := len(final.instances) > 0
        if !killDispatched:
          processesGone = true   // 暂不填 killed_pids/remaining_pids/restarted，避免未派发即撤销泄露字段
        else:
          逐个 KillProcess(pid, startTicks)
          remaining, readOK := awaitKillVerification(work, final)
          processesGone = readOK && len(killed) == len(final.instances) && allTerminated
  t7  窗口壳移除阶段:
        if !processesGone:
          finishAppUncertainWork(..., CodeKillUnconfirmed, "已派发进程终止，但无法确认原实例全部结束")
        else if workRevoked(work):
          if !killDispatched:
            finishAppError(OutcomeNotExecuted, 工作撤销码)   // 省略 killed_pids/remaining_pids/restarted
          else:
            finishAppUncertainWork(..., CodeKillUnconfirmed, "已终止应用进程，但工作已撤销未能移除任务窗口")
        else:
          // 先查 TaskList，不存在则 forgetKilledTask(epoch, task.TaskID)；
          // 存在或读失败则重验 !workRevoked 后调 RemoveTask：
          //   - DispatchTerminated && err==nil -> forgetKilledTask，补齐 !killDispatched 空数组后 finishAppExecuted
          //   - DispatchNotDispatched -> 若 !killDispatched 走 finishAppError(OutcomeNotExecuted) 并省略统计字段；否则 CodeKillUnconfirmed
          //   - DispatchUnknown / 带错 -> 保留登记；若 !killDispatched 补齐空数组并以 "确认无运行进程，但无法确认任务窗口已移除" 走 CodeKillUnconfirmed；
          //     若 killDispatched 则以 "已终止应用进程，但无法确认任务窗口已移除" 走 CodeKillUnconfirmed
```

撤销、超时、预算耗尽行为：
- 若在 `t6` 前发生 STOP/撤销：`killGuard` 或 `sameProcessSet` 后若未进 `KillProcess` 则 `not_executed`；若已进入 `KillProcess` 期间被撤销：`allTerminated=false` → `!processesGone` → 进入 `finishAppUncertainWork`（`detachedWorkRevoked` 为真时不发布迟到观察）。
- 若在 `t6` 完成但 `t7` 前（或 `RemoveTask` 派发前）被撤销：`removeKilledTask` 不派发 `RemoveTask`，保留 `session.Tasks` 交由原会话独立 `cleanup` 在 5 秒逻辑交接预算内执行最终拆任务与关屏；若 `killDispatched == false`（零设备副作用），本调用以 `OutcomeNotExecuted` 收口（省略 `killed_pids`/`remaining_pids`/`restarted`）；若 `killDispatched == true`，本调用以 `OutcomeUncertain` + `CodeKillUnconfirmed` 收口。旧会话状态已由撤销/退场逻辑经 `addSessionIfEnded` 等退休记录机制落盘保留，绝不阻塞新会话准入（遵守硬约束第 9 条）。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| [internal/engine/openapp.go](../../../internal/engine/openapp.go) | `appOpen` 改用一次 `e.dev.TaskList(work.Ctx)` 取 `preAllTasks` 并过滤出 `preDisplayTasks`，传入 `finishOpenVirtual` 与 `finishOpenPrimary`；`verifyLaunchedTask` 每轮 `TaskList` `err == nil` 先重置 `candidate`/`moved` 再按 `!known[task.TaskID]` 区分 `onTarget` 与 `elsewhere`；`finishOpenVirtual` 的 `LaunchUnknown` 与 `!ok` 路径改调带 `reconcileOpenVirtualTask` 回调的 `finishAppUncertainOpen`（锁内 `rememberOwnedTask` 返回 `bool` 决定是否写入 `ownership = "owned"`，保持 `OutcomeUncertain`）；`finishOpenPrimary` 不执行兜底登记 |
| [internal/engine/app.go](../../../internal/engine/app.go) | 为 `finishAppUncertainWork` 抽出一层可复用的收口 helper（或可选 `afterPostObs` 回调），使 `finishAppUncertainOpen` 能在 `appPostObservation` 后、`finishOperation` 冻结前执行一次 `reconcileOpenVirtualTask` |
| [internal/engine/app_kill.go](../../../internal/engine/app_kill.go) | `killGuard` 保持先判 `entry.Ownership == TaskOwned`（`kill_refused_not_owned`）再新增 `e.transferProtectsLocked(task.TaskID)`（`CodeTaskLifecycleBusy`），删除本屏 `Foreground` 检查（原 `:214-222`）；`appKill` 记录 `killDispatched := len(final.instances) > 0`，并在 `processesGone` 后调用 `removeKilledTask` + `forgetKilledTask`（含会话代次与活跃状态守卫），严格区分 `!killDispatched` 预派发撤销/未派发的 `OutcomeNotExecuted`、无运行进程但移壳未确认的 `OutcomeUncertain` 与全部确认的 `OutcomeExecuted` |
| [internal/engine/recovery.go](../../../internal/engine/recovery.go) | `appPlan` 将 `:270-272` 拆开：`CodeTaskLifecycleBusy` 保留 `RecoveryEntryStatus`；`in.Outcome == OutcomeUncertain` 返回 `RecoveryEntryObserve` + `RecoveryReferenceP` + 非 `effective` 的 `retired` |
| [internal/mcp/schema.go](../../../internal/mcp/schema.go) | 更新 `app` 工具描述 `:592`：`kill` 去掉“非前台”隐含前置，明确虚拟屏可将当前前台唯一 `owned` 任务连同任务窗口彻底终止并移除，但若应用在主屏或其他屏仍有任务则拒绝（保留 `"live owned"` 约束词汇） |
| [internal/mcp/schema_test.go](../../../internal/mcp/schema_test.go)、[internal/mcp/result_parity_test.go](../../../internal/mcp/result_parity_test.go) | 更新 `schema.go:592` 描述后同步更新 `schema_test.go:25` 的 `baselineDiscoveryBytes`（现基线 `36250`）与 `result_parity_test.go:527` 的 `TestCatalogFingerprintBaseline` 指纹基线；保持 `schema_test.go:671-680`（尤其 `:675`）对 `"live owned"` 等关键词校验通过 |
| [internal/engine/openapp_test.go](../../../internal/engine/openapp_test.go) | 新增：1) 主屏存在同包既有任务（模拟 `handoff` 后 `open`）时虚拟屏 `open` 一次性 `executed` 且 `owned`；2) 同场景中 `LaunchApp` 仅在他屏新建任务仍报 `application_moved`；3) `singleTask` 主屏旧任务若留在已知集合不误登记为 `owned`；4) `LaunchUnknown` / 超时后置观察前任务落地时 `outcome=uncertain` 仍保留并经 `rememberOwnedTask` 登记 `owned`、回填 `task_id`/`display_id`/`ownership`（含并发撤销/保护导致登记失败时省略 `ownership` 的对照）。更新 `:235` `TestAppOpenLaunchUnknownIsUncertain` 的 `recovery` 断言为 `observe/P/retired` |
| [internal/engine/app_lifecycle_test.go](../../../internal/engine/app_lifecycle_test.go) | 改造 `TestAppKillRefusesUnownedForegroundAndGuards`：将原 `foreground` 拒绝用例改为「虚拟屏前台 `owned` 任务可直接 `kill`，不仅终止进程而且移除任务壳、清理 `session.Tasks`、后置观察返回 `fg=none`」；新增「虚拟屏前台 `owned` 任务在主屏 `display=0` 仍有同包任务时以 `kill_refused_task_elsewhere` 拒绝」、「进程已死但 `RemoveTask` 失败/未确认时整体为 `uncertain (kill_unconfirmed)` 且保留 `session.Tasks` 登记」、「`processesGone` 前置检查已无任务壳时跳过 `RemoveTask` 并清理 `session.Tasks`」、「`kill` 遇到 `transferProtectsLocked` 时在非 `owned` 之后拒绝 `task_lifecycle_busy`」、「零运行实例时未进派发即撤销/未派发返回 `not_executed` 且省略统计字段，而 `RemoveTask` `DispatchUnknown` 返回 `uncertain` 并使用 `确认无运行进程，但无法确认任务窗口已移除`」；核查已有 `TestAppKillTerminatesExactInstances` 与 `TestAppKillConfirmedEmptyProcessSetIsExecutedWithoutSignal` 验证任务已从设备与 `session.Tasks` 清除 |
| [internal/engine/recovery_entry_test.go](../../../internal/engine/recovery_entry_test.go) | 更新 `:195-200` 为 `RecoveryEntryObserve`；补充 `app` 全部五个变更 kind（`open`/`claim`/`handoff`/`close`/`kill`）的 `OutcomeUncertain` 测试用例与反向对照 |
| [internal/engine/internal_uncertain_test.go](../../../internal/engine/internal_uncertain_test.go)、[internal/engine/ui_execution_test.go](../../../internal/engine/ui_execution_test.go) | 两处 `hookedDevice.tasks` 原依赖 `appOpen` 仅在启动后调用 `TaskList`（`:31-42` 与 `:35-44`）；同步调整为仅在 `launchCalls > 0`（启动派发后）返回脚本化的缺失/多任务歧义，保持对 `open_app_missing_task_attribution` 与 `open_app_multiple_task_attribution` 的严格覆盖 |
| [scripts/mcp-probe/app_suite.go](../../../scripts/mcp-probe/app_suite.go)、[scripts/mcp-probe/app_suite_test.go](../../../scripts/mcp-probe/app_suite_test.go)、[scripts/mcp-probe/probe_test.go](../../../scripts/mcp-probe/probe_test.go) | `app_suite.go` 在受控虚屏 `app_kill` 执行后新增校验：`app.list` 不再包含该 `task_id`；若执行了 `kill`，在进入 `app_close` 前重新 `open` 受控目标并走完 `app_close` 闭环（替代原 `SKIP app_close`）；同步 `app_suite_test.go` 与 `probe_test.go` 的 fake 桩响应，将 `probe_test.go:733-743`（`fakeAdapter.handle`）与 `:2187-2207`（`fakeRecoveryService`）固定返回 `task_id: 42` 的 `app.list` 桩改为随 `kill` → `list` → 重新 `open` → `close` 演进的有状态桩，并更新 `:2624` 的 `PASS app_kill_refused` 断言 |
| [release/versions.json](../../../release/versions.json) 与生成脚本产物 | 实施时将 `contract_version` 升至 `24`、`release_version` 拟升至 `0.28.0`、`android_version_code` 升至 `30`，运行 `./scripts/generate-version.ps1` 重新生成 Go/Java/XML 版本文件（不手改生成文件） |
| `docs/` 与 [README.md](../../../README.md) | 见 §6.5 |

### 6.2 配置与取值

- 不新增配置项与请求旋钮。
- 复用现有工作期限（`WorkAppAction = 2*WorkTimeout + 10s`，默认 50 秒）、`verifyLaunchedTask` 5 轮间隔（`0/200/400/800/1200ms`）与 `awaitKillVerification` 4 轮间隔（`0/200/400/800ms`）。
- 删除 `killGuard` 的 `Foreground` 检查不引入任何额外可绕过参数；跨屏/同屏/播放保护保持现有布尔开关与硬约束。

### 6.3 日志与可观测性

- 不新增或放宽事件日志字段白名单；沿用 `android.tasks.read`、`android.process.kill`、`android.task.remove`、`app.*` 既有受控枚举与数值/耗时日志。
- 不向日志写入包名、任务标题、PID 命令行参数、窗口树或异常原文。

### 6.4 与既有不变式的关系

| 硬约束 / 不变式 | 本提案下的保持与影响 |
| --- | --- |
| 1. 唯一权威 | 保持。所有任务归属、护栏、移除与兜底对账仅在 `internal/engine` 内发生 |
| 2. STOP 不等待 | 保持。`RemoveTask` 与 `reconcileOpenVirtualTask` 前均重验 `workRevoked`，STOP 立即撤销工作位，不等待后续收尾 |
| 3. 单向消费 | 保持。`O` 仍只认领一次；`open`/`kill` 的后续检查都在已认领 `O` 的同一次工作内完成 |
| 4. 提交条件化 | 保持。`rememberOwnedTask`、`forgetKilledTask` 及后置观察均校验会话 epoch、活跃状态与撤销状态 |
| 5. 状态锁短小 | 保持。所有 `TaskList` / `KillProcess` / `RemoveTask` 均在锁外执行 |
| 6. 严格接入 | 保持。请求与响应结构无新增任意字段 |
| 7. 三态与省略 | 保持。`reused`、`state_preserved`、`restarted`、`target_pids`/`killed_pids`/`remaining_pids` 维持原有三态与省略语义；`open uncertain` 仅在真实证明落点且 `rememberOwnedTask` 成功时才输出 `ownership="owned"`，`kill` 未进任何派发时 `not_executed` 省略统计数组 |
| 8. 事实分层 | **严格强化**：`reconcileOpenVirtualTask` 在进入 `uncertain` 后只补齐已证实的任务事实与登记，绝不把 `OutcomeUncertain` 改判为 `OutcomeExecuted`；`kill` 区分无运行实例的预派发撤销（`not_executed`）与已派发移除未确认（`uncertain`），仅进程与窗口壳双确认才报 `executed` |
| 9. 恢复屏障 | 保持。不引入任何基于旧物理资源的永久准入门禁 |
| 10. 日志白名单 | 保持。不新增非白名单日志 |

### 6.5 契约与文档影响

实施阶段须同步修改下列文档，并在实现提交前由独立只读文档审计 subagent 审至 `clean`：

- [docs/interface.md](../../../docs/interface.md)：
  - `:18` 协议头示例 `X-Mobile-Use-Contract` 从 `23` 更新到 `24`；
  - `:1333-1338`、`app.kill` 准入说明删除「应用在本屏前台……拒绝」，补充说明在虚拟屏可直接终止本屏前台 `owned` 任务（主屏/他屏仍有该包任务时仍以 `kill_refused_task_elsewhere` 拒绝），且在确认原进程全部结束（或原进程集为空）后同步移除该任务窗口容器及会话登记；
  - `:1340-1345` 核对并更新 `open`/`kill` 失败、恢复与已证明身份字段规则说明（确保 `open uncertain` 与 `kill` 收口表述与现行行为一致）；
  - `:1357-1365` 补充标注 `kill_refused_foreground` 保留在 `409` 受控错误码词表中，现行虚拟屏实现已不再因本屏前台拒绝；
  - 共同 `contract_version` 样例与字段说明（`:848, 875, 911, 921, 934` 等处）从 `23` 同步到 `24`。
- [docs/architecture.md](../../../docs/architecture.md)：
  - `:154-159` 更新 `open` 的全设备启动前基线核验及 `kill` 去掉非前台限制并同步拆任务窗口；
  - `:671-672` 同步版本与契约号。
- [docs/integration.md](../../../docs/integration.md)：
  - `:44, 313, 321, 569, 755` 各处示例与要求中的 `X-Mobile-Use-Contract` 从 `23` 更新至 `24`；
  - `:734` `kill_refused_*` 行去掉「前台」拒绝原因，保留非 owned、跨屏/同屏冲突、FGS/播放与 root；
  - `:737` `504 app_launch_unconfirmed` 改为：返回 `recovery entry=observe ref=<P>` 时按 `observe` / `app.list` 核验（仅传输超时未知操作结果时先用 `status(P)` 确认是否已完成）。
- [docs/mcp.md](../../../docs/mcp.md)：`:17-18`（尤其 `:18` 固化的 tool catalog fingerprint `24e29ffad62cd998e1ac5fc26fcf4479e672806132ddce55ad3785794ebc6f0b`）随 `internal/mcp/schema.go:592` 描述调整同步更新为新指纹，并核对工具说明与版本一致性。
- [docs/release.md](../../../docs/release.md)：`:38-41`（含 `:40` 冻结 `source_commit`）、`:76-77`、`:85` 随 `release/versions.json`（`contract_version=24`、`release_version=0.28.0`、`android_version_code=30`）同步升级。
- [README.md](../../../README.md)：`:10-11` 与 `:167`（`X-Mobile-Use-Contract: 24`）同步版本、Android versionCode 与契约号。
- [docs/client.md](../../../docs/client.md#L22)、[docs/control-protocol.md](../../../docs/control-protocol.md#L26-L158)、[docs/phone-operations.md](../../../docs/phone-operations.md#L124-L257)、[docs/runbook.md](../../../docs/runbook.md#L122-L274)：同步 `contract_version=24`、`release_version` 与 `android_version_code`。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **解决 Issue #39**：`handoff`（或主屏本来就有同包应用）后在虚拟屏 `app.open` 同包应用，可直接命中 `executed`（`ownership=owned`、`task_id` 精确）；即使极端慢启动导致 `LaunchUnknown` / 超时进入 `uncertain`，只要在后置观察结束前已落地虚屏且满足 `rememberOwnedTask` 条件化入账，就能登记 `owned` 并回填 `task_id`/`display_id`/`ownership`（登记未成功时不伪造 `ownership`），不再生成无法 `close`/`kill` 的 `shared` 幽灵任务；且所有 `app` 终态 `uncertain` 均通过 `observe/P` 恢复，告别 `status(op)` 死循环。
2. **解决 Issue #35**：虚拟屏单应用在前台时可直接 `app.kill`；`app.kill` 成功后同时消灭进程和窗口壳，任务立即从 `app.list` 消失，再也不在后续 `close` 退栈时出现只有 `n1 root` 的「僵尸假前台」。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 应用在主屏 `0`（或其他非本会话屏幕）仍有存活任务时，调用 `app.kill` 仍会拒绝 | 本方案显式保持跨屏保护（`kill_refused_task_elsewhere`）：Android 同包默认共享进程，强杀会连带破坏用户主屏上的应用 |
| `kill` 成功后，应用被系统的 JobScheduler、推送/广播或闹钟服务在后台拉起新进程 | `kill` 坚持精确杀死原核验 PID 实例，不使用 `force-stop` 禁运行，也不无限追杀后续新进程；`restarted` 字段如实报告 |
| 非 `app.open` 触发、或 `app.open` 超时很久之后应用才在虚拟屏悄悄自发创建的全新独立 Task | `app.list` 保持纯只读不篡改所有权，这类极晚异步任务仍按 `shared` 展示（但在会话 `end` 时仍会被 `cleanup` 按私有屏规则清理） |
| 第三方应用在虚拟屏后台自己静默崩溃（未调用 `app.kill`/`app.close`）且 Android 保留了其任务壳，退栈后出现无界面 `n1 root` | 本方案治理的是 `app.kill` 自身制造的死壳；未执行 `kill` 时的系统自发崩溃仍留给调用方按 `fg` 与后续 `app.close`/`app.kill` 清理 |
| 目标应用开启了 `singleTask`/`singleInstance` 且系统强行把主屏 `display=0` 原有任务搬进虚拟屏 | 引擎为保护用户主屏任务安全，通过 `known` 基线识别后拒绝将其冒充 `owned` 登记；此类特殊应用仍需依赖 `claim`/`handoff` 显式管理 |

### 7.3 成本

- **`app.open`**：启动前将 `TasksOnDisplay` 改为一次 `TaskList`（在 `internal/android` 底层两者同样执行一次 `am stack list`，实机净开销为 **0 新增 ADB 命令**；在未确认兜底分支多执行 1 次 `TaskList`，约 +50~150ms，仅在异常 `uncertain` 路径触发）。
- **`app.kill`**：在确认原进程已死后，增加 1 次只读 `TaskList` 检查与至多 1 次 `RemoveTask`（`am stack remove`，约 +50~200ms）。由于不再要求调用方「先 open 垫背应用 → 再 kill → 再 close」，端到端总耗时与 token 消耗大幅下降。
- **契约升级代价**：`contract_version` 从 `23` 前进至 `24`，实施时需整组重建并部署手机服务端、控制 App、手机 MCP 适配器与 Windows 主机工具。

---

## 8. 验证计划

### 8.1 单元测试（实施阶段由主代理在 `GOEXPERIMENT=jsonv2` 下集中执行）

| 用例 | 期望与反向对照（含先红复现） |
| --- | --- |
| 复现 #39：`TestAppOpenWithExistingTaskOnPrimaryDisplay`（先红） | 主屏 `display=0` 已有同包任务 `#213` 时，虚拟屏 `open` 同包应在 `display=12` 创建 `#214` 并返回 `executed` + `ownership=owned`；去掉全设备 `preAllTasks` 基线即复现退化为 `app_launch_unconfirmed` |
| #39 跨屏防误归属：`TestAppOpenMovedPrimaryTaskNotOwned` | 若 `singleTask` 把已知主屏任务 `#213` 移入 `display=12` 且无新任务，`verifyLaunchedTask` 与 `reconcileOpenVirtualTask` 不得把它误登记为 `owned` |
| #39 他屏新建：`TestAppOpenNewTaskOnPrimaryReportsMoved` | 主屏已有 `#213` 且 `LaunchApp` 又在主屏新建 `#215` 时，正确报 `CodeAppMoved`（`#215`），不会误指 `#213` |
| #39 慢启动兜底：`TestAppOpenUncertainReconcilesLateTaskAsOwned` | `LaunchUnknown` 或 `verifyLaunchedTask` 超时后，若 `TaskList` 在后置观察前/后已出现唯一新建 `#214` 于 `display=12`，结果保持 `outcome=uncertain`，但 `rememberOwnedTask` 返回 `true`、`session.Tasks` 含 `#214 (owned)` 且 `AppView` 填充 `task_id`/`display_id`/`ownership`；反向对照：并发撤销/迁移保护/容量受限使 `rememberOwnedTask` 返回 `false` 时省略 `ownership`，多任务歧义 `{77, 78}`（`internal_uncertain_test.go` / `ui_execution_test.go`）维持 `task_id` 省略且不登记 |
| 复现 #35：`TestAppKillForegroundVirtualTaskRemovesShell`（先红） | 虚拟屏前台 `owned` 任务直接 `kill` 返回 `executed`，PID 被杀、`RemoveTask` 被调用、`session.Tasks` 清除、`app.list` 为空且后置观察 `fg=none`；主屏 `display=0` 有同包任务时仍拒绝 `kill_refused_task_elsewhere` |
| #35 进程已死无信号但移除壳：`TestAppKillEmptyProcessSetRemovesTaskShell` | 进程列表为空时发 0 次信号并调用 `RemoveTask` 清壳；若前置 `TaskList` 证明 Android 已自动销毁任务壳，则跳过 `RemoveTask` 并清理 `session.Tasks`；若 `!killDispatched` 且未进 `RemoveTask` 即被撤销或回执 `DispatchNotDispatched`，则返回 `not_executed` 并省略 `killed_pids`/`remaining_pids`/`restarted` |
| #35 移除失败守卫：`TestAppKillRemoveUnknownStaysUncertainAndKeepsRegistration` | `KillProcess` 成功但 `RemoveTask` 返回 `DispatchUnknown` 时，返回 `uncertain (kill_unconfirmed)`（`已终止应用进程，但无法确认任务窗口已移除`）且保留 `session.Tasks`；`len(instances) == 0` 且 `RemoveTask` 返回 `DispatchUnknown` 时返回 `uncertain (kill_unconfirmed)`（`确认无运行进程，但无法确认任务窗口已移除`）并保留 `session.Tasks`（反向对照：不伪造 `executed` 或偷清登记） |
| 恢复收敛：`TestRecoveryAppUncertainIsObserve` | `open`/`claim`/`handoff`/`close`/`kill` 在 `OutcomeUncertain` 下经 `planRecovery` 与 `status(P)` 均输出 `entry=observe, reference=P, input_state=retired`；`CodeTaskLifecycleBusy` 保留 `status/P`；全套 I1–I9 不变式通过 |
| MCP 与 Probe 单测 | `internal/mcp`（含 `schema_test.go` `"live owned"`/`baselineDiscoveryBytes` 与 `result_parity_test.go` `TestCatalogFingerprintBaseline`）、`scripts/mcp-probe`（含更新后有状态 `probe_test.go`）、`protocol` 全量单测通过；`generate-version.ps1 -Check` 通过；交叉构建 `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` 通过 |

### 8.2 实机/系统级与发布默认范围（按 [AGENTS.md](../../../AGENTS.md) 第 19、24 条由主代理亲自执行）

实施阶段按默认发布范围完成三项交付与最小闭环实机验证：

1. **整组构建、清单核验与手机覆盖部署**：
   - 执行 `./scripts/generate-version.ps1 -Check`、`./scripts/build-release.ps1`、`./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`；
   - 执行 `./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json`（保留 App 私有数据、消息与账本，禁止卸载或 `pm clear`），核验手机 `device-service`、控制 APK `versionCode=30` 及手机 MCP `/proc/<pid>/exe` 哈希与清单一致。
2. **Windows 主机二进制构建替换与清单核验**：
   - 执行 `./scripts/build-host-tools.ps1` 并替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，执行 `./scripts/build-host-tools.ps1 -VerifyOnly` 证明与手机发布清单配套一致。
   - 依赖的 Agent 托管 MCP 进程和重连由用户处理，不作为独立探针通过门禁。
3. **独立探针与最小实机闭环**：
   - 分别用新 `build/mcp-probe.exe` 通过 `-transport stdio`（`device-mcp.exe`）与 `-transport http-remote`（手机 18883→18882 转发入口）在 `text`/`structured`/`both` 下跑通 `-suite app-lifecycle`（覆盖 `app.open` → `app.list` → 虚拟屏前台 `app.kill` → 验证 `app.list` 无残留任务 → 重 `open` → `app.close`）；
   - 在受控测试目标（或用户确认的受控虚拟屏场景）实机复核 Issue #39（`handoff` 至主屏后 `open` 同包一次成功为 `owned`、无 `status` 死循环）与 Issue #35（虚拟屏前台任务直接 `kill` 后 `app.list` 消失、退栈后无 `nodes=1` 死壳假前台，同时验证主屏有同包任务时仍正确返回 `kill_refused_task_elsewhere`）。

### 8.3 记录

实施与验证结束后按 [process.md](../../process.md#L172-L201) 执行「先提交实现、再归档提案」：将本文移入 `maintenance/proposals/implemented/` 并重写相对链接，补充 §11 实现结果，并将实机、构建与探针完整记录写入 `maintenance/verification/app-window-task-lifecycle-hardening.md`。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论与来源 |
| --- | --- |
| 本次会话范围 | 用户明确本次会话只完成提案并交审阅通过，具体实现留待后续会话 |
| 目标范围 | 用户明确聚焦 Issue #35 与 Issue #39（应用窗口/任务/进程生命周期），暂不含 #36 |
| `app.kill` 前台与窗口壳策略 | 用户明确认可：`app.kill` 要彻底关闭（终止进程并移除窗口容器使包从 `app.list` 与窗口栈消失）；虚拟屏正在前台的 `owned` 应用可以直接 `kill`，但只要该应用在主屏（或其他屏）仍有任务则仍拒绝 |
| 跨屏 `app.open` 与恢复收敛 | 用户赞同以全设备启动前基线修复跨屏新建核验、兜底收编延迟落地任务，并将 `app` `uncertain` 恢复入口统一收敛为 `observe/P` |
| 例行技术选择（供复核） | `app.list` 保持纯只读；`app.kill` 先终止核验进程再移除任务壳；`RemoveTask` 未知回执不通过 `TaskList` 追认；`contract_version` 23→24、拟 `release_version` 0.28.0 / `android_version_code` 30 |

### 9.2 未决

- **提案确认**：由用户在审阅本文后确认是否按此方案进入下一会话实施（在此之前状态保持为 `提案，未实现`）。

---

## 10. 实施顺序

1. **核对基线与契约版本**：确认 `HEAD` 与 `release/versions.json`，先写齐复现 #35 / #39 的先红单测与反向对照测试（并调整 `internal_uncertain_test.go`/`ui_execution_test.go` 的 `hookedDevice.tasks` 时序门控）。
2. **实现引擎改造**：依次落地 `internal/engine/openapp.go` + `app.go`（全设备 `preAllTasks`、每轮重置候选的 `verifyLaunchedTask` 与带 `rememberOwnedTask` 条件登记的 `reconcileOpenVirtualTask`）、`internal/engine/app_kill.go`（移除虚屏前台限制、保持 `kill_refused_not_owned` 先于 `task_lifecycle_busy`、`killDispatched` 分层与 `removeKilledTask`/`forgetKilledTask`）、`internal/engine/recovery.go`（`appPlan` 终态 `uncertain` 收敛为 `observe/P`）。
3. **更新 MCP 描述、指纹基线与 Probe 套件**：更新 `internal/mcp/schema.go:592`（保持 `"live owned"` 约束）、`internal/mcp/schema_test.go:25`（`baselineDiscoveryBytes`）、`internal/mcp/result_parity_test.go:527`（`TestCatalogFingerprintBaseline`），以及 `scripts/mcp-probe/app_suite.go`、`app_suite_test.go` 与 `probe_test.go`（有状态 `fakeAdapter`/`fakeRecoveryService` 桩与断言），验证 `text`/`structured`/`both` 三模式。
4. **版本生成与正式文档同步**：更新 `release/versions.json` 并运行 `generate-version.ps1`；同步 §6.5 所列 `docs/`（含 `docs/mcp.md:18` 目录指纹、`docs/interface.md:18` 与 `:1333-1365`、`docs/integration.md` 全文 `X-Mobile-Use-Contract` 与恢复说明、`docs/release.md` 等）及 `README.md`。
5. **集中验证与只读文档审计**：主代理亲自跑齐定向/全量 Go 单测（`GOEXPERIMENT=jsonv2`）与 `linux/arm64` 交叉编译；提交前由独立只读文档审计 subagent 审阅全部 `docs/` 改动至 `clean`。
6. **提交实现与发布三项交付**：提交实现代码与文档 → 整组构建并部署手机（保留数据）→ 构建替换主机 exe 并用独立探针验证 stdio 与手机 HTTP MCP → 实机复核 #35/#39 闭环 → 写入验证记录并单独提交提案归档。

---

## 11. 实现结果

实现提交：`cabfd40c627c2b829cd00a20dc1730605cce1044`。实现与验证记录：[app-window-task-lifecycle-hardening.md](../../verification/app-window-task-lifecycle-hardening.md)。

### 11.1 已落地

| 提案条目 | 实现与证据 |
| --- | --- |
| §4.1 全设备启动前基线与跨屏归属 | [openapp.go](../../../internal/engine/openapp.go) 的 `appOpen` 改为单次 `TaskList` 取 `preAllTasks`，并以 `tasksOnDisplay` 过滤复用集合；`verifyLaunchedTask` 用 `diffNewPackageTasks` 每轮重置候选，只认启动后新增且落在目标屏的唯一同包任务。实机 `handoff` 后同包重开一次 `executed`、`ownership=owned`、`task=235 display=197` |
| §4.1.3 未确认收编 | [openapp.go](../../../internal/engine/openapp.go) 的 `finishOpenUncertain`、`reconcileOpenVirtualTask`、`rememberOwnedTask` 与 [app.go](../../../internal/engine/app.go) 的 `finishAppUncertainWorkAfter` 钩子：后置观察后一次只读核对，仅在唯一新建任务且条件化登记成功时写 `ownership=owned`，主 outcome 保持 `uncertain` |
| §4.2 虚拟屏前台终止与任务窗口移除 | [app_kill.go](../../../internal/engine/app_kill.go) 的 `killGuard` 删除本屏 `Foreground` 拒绝并把 `owned` 判定排在迁移保护之前；`appKill` 以 `killDispatched` 分层，`removeKilledTask`、`finishKillRemovalRevoked`、`forgetKilledTask`、`fillEmptyKillFacts` 完成壳移除与条件化登记删除。实机前台受控应用 `killed_pids=1`，`app.list` 随即不含该任务 |
| §4.3 终态 uncertain 恢复收敛 | [recovery.go](../../../internal/engine/recovery.go) 的 `appPlan`：`task_lifecycle_busy` 保持 `status/P`，`OutcomeUncertain` 改为 `observe/P/retired`（`effective` 归一为 `retired`）。五个变更 kind 的 `status(P)` 回放一致，运行中仍先返回 `running` |
| §6.1 MCP 描述与指纹 | [schema.go](../../../internal/mcp/schema.go) 的 `app` kill 描述与 [result_parity_test.go](../../../internal/mcp/result_parity_test.go) 指纹基线；`go run ./scripts/toolcatalog` 与 [docs/mcp.md](../../../docs/mcp.md) 一致为 `6a22be013e8653b79f1c46e6c5c9212309454eee1b4f377836b81936b49c95e8` |
| §6.1 独立探针 | [app_suite.go](../../../scripts/mcp-probe/app_suite.go) 新增 `app_kill_removed` 与 kill 后重开→close 闭环；stdio 与手机 HTTP 的 `app-lifecycle` 在 `both`/`text`/`structured` 下均 `RESULT PASS` |
| §6.1 版本源与文档 | [versions.json](../../../release/versions.json) 发行 0.28.0、契约 24、versionCode 30，生成产物由 `generate-version.ps1` 重写；`docs/` 与 README 同步，独立只读文档审计两轮后复核 clean |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 收编入口命名 | 实现为 `finishOpenUncertain`（提案写作 `finishAppUncertainOpen`），行为一致 |
| `baselineDiscoveryBytes` | 未修改：kill 描述变长后实测 `total=35526 < 36250`，断言未失败 |
| 测试门控方式 | 两个既有归因子用例的 `hookedDevice.tasks` 按“启动是否已派发”（`launchCalls>0`）门控，而非按 `TaskList` 调用序号，避免分支相关脆弱 |
| 探针额外输出 | 拒绝路径新增 `SKIP app_kill_removed`（明确“未验证移除”），并新增拒绝路径与移除失败两个反向用例，未扩大实现范围 |
| `RemoveTask` 未派发的预派发码 | 沿用 [app_close.go](../../../internal/engine/app_close.go) 既有映射返回 `409 control_revoked` 的 `not_executed`，未新造错误码 |
| 后验收编时机 | 仅在 `appPostObservation` 之后、`finishOperation` 冻结之前执行一次，未在前置额外读取，避免多余设备读 |
| 恢复收敛的清理范围 | 未改 `statusOperationLocked`/`operationViewLocked`，运行中 `status(P)` 语义保持 |
| 系统自发崩溃留下的死壳 | 仍不在范围内：未调用 `kill` 的系统崩溃残留由调用方按 `fg` 与后续 `app.close`/`app.kill` 清理 |
| 实机未确认 `open` 触发 | 线上时序难以稳定制造 `LaunchUnknown`，该分支由单元测试与后验收编用例覆盖 |

---

## 附：本提案不改变的事项

- `app.find` / `app.list` 的纯只读性质、32 KiB 预算与不自动收编非自有任务的语义不变。
- `app.claim` / `app.handoff` / `app.close` 的目标选择规则、转移责任池 `task_transfers` 与 `allow_borrowed` 默认值不变。
- 主屏模式（`mode=primary`）下 `claim`/`handoff`/`close`/`kill` 的拒绝规则与 `app.open` 的 `shared` 不登记语义不变。
- `act`、`wait_for`、`observe`、`snap`、`inspect`、`shell`、`notify`、`keepalive`、`start`、`end`、`status`、`diagnose` 的接口与语义不变。
- 账本格式版本号（v5）与存储容量分区上限不变；控制 App 协议字段与通道行为不变。
