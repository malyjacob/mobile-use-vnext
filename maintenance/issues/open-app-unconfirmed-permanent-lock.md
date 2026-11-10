# Issue：虚拟显示下 `open_app` 实际成功却被判 `unconfirmed`，并使整机 `can_start` 永久锁死

状态：问题报告（仅描述现象与可能原因，未实现修复）。
基线提交：`ae1fbbc35f32e0dbeb2eda7a519e825f936601e9`（分支 `master`，2026-11-10）。
触发环境：设备 `virtual`（虚拟显示）模式，经主机 stdio MCP 适配器调用。

---

## 1. 现象

在一次 `virtual` 模式会话中：

1. `start` 建立会话成功（`mode=virtual`）。
2. 首次 `open_app com.twitter.android` 返回：

   ```
   error.code=app_launch_unconfirmed
   error.message="已派发应用启动，但无法确认任务归属"
   ```

   但**紧随其后的 `observe` 明确显示 Twitter 已在前台**：
   `observation.foreground={"state":"present","package":"com.twitter.android","task_id":2737}`，
   完整 UI 树可读。也就是说：**应用实际上已经成功启动。**

3. 该会话随后被正常浏览、点按、`end` 结束。`end` 返回：

   ```
   cleanup.execution=unconfirmed
   cleanup.error={"code":"execution_unconfirmed","message":"存在未确认结束的已派发输入"}
   cleanup.tasks=confirmed  cleanup.recents=confirmed  cleanup.display=confirmed
   ```

4. **此后任何新 `start` 永久失败**：

   ```
   status(): can_start=false, blocking_code=cleanup_unconfirmed
   start     → error.code=cleanup_unconfirmed "原资源收尾未确认"
   ```

   `cleanup.state` 停在 `unconfirmed`，只有 `execution` 一项未确认，其余三项均 confirmed。

5. **重启/重载主机 stdio MCP 适配器进程无效**；切换虚拟↔主屏显示模式也无效。
   服务重启后 `StartRecovery` → `driveRecovery` 读到同一条持久化记录，仍卡在
   `cleanup_unconfirmed`。可观测事实：`diagnosis` 显示 `cleanup` 阶段持续 `execution_unconfirmed`，
   `tasks/recents/display` 均 confirmed。

**代价**：一次「其实成功的」`open_app` 即可让整台设备进入永久不可再次 `start` 的状态，
直到人工清掉设备侧恢复账本 / App 私有数据，或重启手机且账本被清。没有任何工具 API 出口可解除。

---

## 2. 可能原因（证据链，均以基线提交为准）

### 2.1 起点：`open_app` 的启动确认判据过于严格，且与「前台事实」不是同一证据源

`internal/engine/openapp.go:324-369` `verifyLaunchedTask` 判定「启动成功」的唯一依据是：

- 轮询 `TaskList`（ADB 侧任务列表），在 0/200/400/800/1200ms 内找到一个
  **「包名匹配、taskID 不在 preTasks、且 `DisplayID == session.Display.ID`」的唯一新任务**；
- 恰好一个且在目标屏 → `ok=true`；`matches>1` 或 5 轮都没有唯一新任务 → `ok=false`。

而调用处 `internal/engine/openapp.go:189-194`：`ok=false` 时 `markDispatchUnknown(work)` 并返回
`CodeAppLaunchUnknown`（即观测到的 `app_launch_unconfirmed`）。

**可能的具体未命中原因（需实机抓 `TaskList` 原始返回才能定性是哪一条）**：

- **R-①** 虚拟显示模式下，`TaskList` 返回的新任务 `DisplayID` 不等于 `session.Display.ID`
  （ADB 侧把虚拟屏任务报成 display 0 或其它值）。若唯一匹配但 display 不符，会走 `moved=true`
  分支（`openapp.go:195-199`，错误码应为 `CodeAppMoved`）——但本次报的是「无法确认任务归属」，
  更像是 R-②/R-③。
- **R-②** 1.2s 窗口内 `TaskList` 未吐出唯一新任务（冷启动慢、或虚拟屏任务不在 ADB 可见集合内）
  → `matches` 为空 → `ok=false`。
- **R-③** 启动瞬间产生多个新 taskID（如分身/多任务）→ `matches>1` → 直接 `ok=false`。

**核心矛盾**：GUI 层（无障碍树 / `foreground`）已看到 App 真正在前台，但引擎的「确认」走的是
另一条 ADB `TaskList` 通道且要求唯一 + display 精确匹配。两条证据源不一致；当 ADB 通道确认不了时，
引擎按「输入可能已到达设备」保守记为 unknown —— 这一步本身是安全取向，但见 2.2，它没有释放路径。

### 2.2 关键：`ui_input` 来源的执行不确定被设计成永久粘住、无释放路径

1. `markDispatchUnknown` 把不确定性钉进会话：`internal/engine/operations.go:311-323`
   —— 置 `work.DispatchUnknown=true` 且 `e.session.ExecutionUnknown=true`（会话级，唯一的清除点在
   shell 确认路径 `shell.go:462/545`，普通 UI 动作不会清）。

2. `end` 时据此写记录：`internal/engine/cleanup.go:49-61`
   —— `ExecutionUnknown` 为真时 `cleanup.Execution=unconfirmed`，且
   `ExecReason = cleanupExecReasonLocked()`。

3. `cleanupExecReasonLocked` 在非 shell 链时一律返回 `"ui_input"`：
   `internal/engine/cleanup.go:78-86`。

4. **恢复阶段 `ui_input` 永远无法被晋升为 confirmed**：
   - `internal/engine/control.go:554-557`：`driveRecovery` 一旦看到
     `cleanup.Execution/CleanupUnconfirmed` 就 `setRecoveryReason("old_resource_unconfirmed"); return`，
     永远走不到 `finishRecovery`。
   - 唯一能提升 execution 的 `promoteOrphanShellCleanup`（`internal/engine/shell_recovery.go:117-124`）
     **只处理 `ExecReason == "shell_chain"`**，`ui_input` 直接 return。
   - `confirmCleanupBarrierLocked`（`internal/engine/shell.go:660-662`）同样只看 `shell_chain`。

5. `can_start` 依赖 cleanup 完成：`internal/engine/engine.go:599-605`
   —— `cleanup.State != CleanupComplete` 即返回 `cleanup_unconfirmed`。而 cleanup 永远到不了 complete
   （第 4 步卡死），故 `can_start=false` **永久成立**。

### 2.3 为什么重载/重启/切模式都无效

恢复账本（`LedgerCleanup{Execution, ExecReason:"ui_input", ...}`，`internal/engine/ledger.go:101-114`）
是持久化在设备侧的私有存储。重启只是重新 `StartRecovery` → 读到同一条 `exec_reason=ui_input,
execution=unconfirmed`，在 `control.go:554` 处再次 return。**这是写入账本的持久状态，重启只重读，
不会自愈。** 这与 `internal/engine/cleanup.go:74-77` 的注释意图一致：
「UI input uncertainty keeps today's sticky behavior and is never rewritten by a later observation.」

---

## 3. 影响与性质

- **性质**：代码按注释意图工作（安全优先、宁可锁死），因此更像**可用性缺陷 / 设计取舍**
  而非纯逻辑 bug。但「一个实际成功的 `open_app` 就能永久锁死服务且无运维出口」是明确的可用性问题。
- **概率**：虚拟显示 + ADB `TaskList` 的组合下 `verifyLaunchedTask` 拿不到唯一+display 归属的概率不低，
  该死锁可能**常态化触发**。
- **缺口**：无任何工具 API 口可「强制确认/清除屏障」，只能清设备侧账本或 App 私有数据。

---

## 4. 待确认（给出可能原因，供正式取证的线索）

- 需在一次干净会话里抓 `dumpsys activity activities` / `TaskList` 原始返回，确认新 Twitter task 的
  `displayId` 到底报什么，以在 R-①/②/③ 中定性。
- 需确认 `open_app` 成功判据能否以「`foreground` 事实」为准（放宽 `verifyLaunchedTask`），
  以及 `ui_input` unknown 是否应引入「物理资源已全部回收（display/tasks/recents 均 confirmed）」
  时的释放路径。

---

## 附：本 issue 不包含

- 不提出具体修复代码或提案（修复路线待正式提案，遵循 `maintenance/process.md`）。
- 不复现完整复现用例（可另行补充先红单测以钉住 R-②/R-③）。
