# P3 engine 并发/恢复测试收尾记录

状态：2026-11-09。本页只记录本轮实际执行过的测试与真实结果。验证对象是
`internal/engine` 的 Go 单元/并发模拟测试，不是实机验证；没有运行 `adb`、
`scripts/deploy.ps1` 或任何设备命令，也没有把测试通过写成实机结论。

## 1. 范围与环境

| 项 | 值 |
| --- | --- |
| 工作区 | `E:\Code\Python\mobile-use-vnext` |
| 代码提交 | 未提交（工作区；本轮不提交 git） |
| Go | `go1.27.1 windows/amd64` |
| 构建标签 | `GOEXPERIMENT=jsonv2` |
| CGO | `CGO_ENABLED=0`；PATH 中无 gcc |
| 设备/部署命令 | 未执行 |
| 改动边界 | 仅 `internal/engine` 测试文件 + 1 处最小实现修复（2 个实现文件）；未改 `docs/`、`scripts/`、Java、公共 JSON 契约 |

## 2. 修改的文件

测试侧：

- `internal/engine/engine_test.go`：把 4 处、共 8 个未命名 `device.Rect{...}` 改为
  `Left/Top/Right/Bottom` 键值字面量；把 `newHarness` 拆出
  `newHarnessWithClock(t, startClock)`，供“过期但尚未被 housekeeping 撤销”的确定性测试
  关闭 1s ticker。原 `newHarness` 行为不变。
- `internal/engine/concurrency_test.go`：`hookedDevice` 增加 `StartDisplayHelper` 钩子；
  新增 6 个测试并补强 1 个既有测试。
- `internal/engine/commands_test.go`：新增 close_display 逐代次投递/回执测试；
  增加测试内 `stringPtr` 辅助。
- `internal/engine/recovery_test.go`：新增恢复账本快照测试与 `waitRecoveryComplete` 辅助。
- `internal/engine/commit_test.go`：新增 observe full/成本门测试（引入 `strings`）。

实现侧（仅因新增测试稳定复现真实 bug 后做的最小修复）：

- `internal/engine/operations.go`：`markDispatchUnknown` 在设置
  `work.DispatchUnknown` 的同时，把 `session.ExecutionUnknown` 记到本会话（epoch 匹配时）。
- `internal/engine/cleanup.go`：`beginCleanupLocked` 的 `execUnknown` 同时认可
  `session.ExecutionUnknown` 与仍存活的 `work.DispatchUnknown`。

没有修改公共 JSON 字段、错误码、恢复语义或工作队列语义。

## 3. 新增/修改测试与证明窗口

| 测试 | 文件:行 | 证明的窗口 |
| --- | --- | --- |
| `TestSessionLeaseStopsRenewalAtHardLimit` | `concurrency_test.go:387` | 滑动续期被 `HardExpiresAt` 截断；接近硬上限不再前移；超过硬上限后 `touchSessionLocked` 不复活租约 |
| `TestExpiredSessionRejectsActAndObserveWithoutObservation` | `concurrency_test.go:417` | 租约已过期但 housekeeping 尚未撤销时：act 走请求时惰性检查，返回 `not_executed/session_expired`；observe 被拒；两者都不分配新 O，act 也未认领原 O |
| `TestStopAfterUncertainActKeepsExecutionUnconfirmed`（`stop`/`end` 子测试） | `concurrency_test.go:462` | 输入已派发但适配器返回错误、act 已返回 `uncertain` 且普通工作位已释放后，再 STOP/end：`cleanup.execution` 仍为 `unconfirmed`，`status(P)` 仍为 `uncertain`，新 start 被拒且不派发 helper/不分配 O |
| `TestStopAfterExecutedActKeepsExecutionConfirmed` | `concurrency_test.go:541` | 已确认完成的 act 之后再 STOP：`cleanup.execution` 必须仍为 `confirmed`（防止修复把已确认动作误标为未知） |
| `TestStopBeforeDisplayProbeNeverAdmitsStart` | `concurrency_test.go:567` | STOP 落在 `StartDisplayHelper` 已派发、display probe 之前：start 返回 `not_executed`、不探测显示、不发布 O、会话结束 |
| `TestStopAfterDisplayProbeDoesNotPublishStartObservation` | `concurrency_test.go:626` | STOP 落在显示 probe 成功之后、首个 full 采集提交之前：不发布 O，不留下可用会话，App 重新同步前新 start 被拒 |
| `TestStartProbeCannotReviveStoppedSession`（补强） | `concurrency_test.go:196` | STOP 落在 display probe 进行中：start 仍为 `not_executed`，不发布 O，不复活会话 |
| `TestObserveFullFlagAndDeltaCostGate` | `commit_test.go:160` | 基线已确认且树未变时：`full=false` 返回 empty delta；`full=true` 即使 delta 本可成立也强制 full；把单个 label 放大后 delta 编码不再低于 full 的 70%，`full=false` 也回退 full |
| `TestRecoveryKeepsOldOwnershipUntilResolvedThenClears` | `recovery_test.go:210` | 恢复未完成时 `ledgerRecordLocked` 仍携带旧 Session / PendingCreate / Cleanup；恢复完成后旧 Session 与 PendingCreate 清空、Cleanup 变为 `complete/confirmed`，且 `can_start=true` |
| `TestCloseCommandsDeliverPerGenerationAndAckedTargetNeverReturns` | `commands_test.go:78` | 同一 display 不同 generation 是独立队列项；每次投递至多一条新命令；非所属 App 实例的回执不关闭命令；owner 回执只关闭精确目标；负回执也关闭且不重投；已 ack 的 generation 不再生成 |

既有覆盖核对（未重复新增）：

- 旧实例 close_display 丢弃与超时：`TestDisplayCommandsDiscardOldInstanceAndTimeout` 已覆盖。
- 不同 generation 不合并、同 target 去重、ack 不重排：`TestCloseCommandDeduplicatesExactTargetAndNeverRequeuesAck` 已覆盖；新测试补上了“逐条投递 + 错误实例回执 + 负回执”的缺口。
- `TestObserveFullForcesFullAfterBaselineAcknowledgement` 已覆盖 `full=true` 强制 full；新测试把 `full=false` 的成本路径与成本回退放在同一条基线上。
- `TestStopDuringDispatchedActKeepsUncertain` 已覆盖“STOP 时工作仍在飞”的窗口；新测试补的是“act 已返回 uncertain、工作位已释放”的窗口。

## 4. 发现的真实 bug 与最小修复

**现象**：act 已把输入派发给 Android，但适配器结果未知（例如 tap 调用返回错误）时，act 返回
`uncertain` 并释放普通工作位。之后用户 STOP/end，`cleanup.execution` 被记为 `confirmed`，
即使该输入可能已经到达设备。新增测试在修复前稳定失败：

```text
concurrency_test.go:498: cleanup.execution="confirmed", want unconfirmed for an unknown dispatched input
```

**根因**：`markDispatchUnknown` 只设置 `work.DispatchUnknown`；`releaseWork` 释放工作位后该
标志消失。`beginCleanupLocked` 只检查当时仍存活的 work，`sessionState.ExecutionUnknown`
（字段注释即“survives release of an uncertain ordinary work slot”）从未被赋值。

**修复**：

1. `markDispatchUnknown` 在 epoch 匹配时同时置 `session.ExecutionUnknown`。
2. `beginCleanupLocked` 用 `session.ExecutionUnknown || 存活 work 的 DispatchUnknown` 判定
   execution 是否 `unconfirmed`。

**修复后行为**：未知输入的 STOP/end 记录 `execution=unconfirmed`，P 保持 `uncertain`；
已确认完成的 act 仍记录 `execution=confirmed`。该修复只影响收尾的 execution 事实，
不改 JSON 契约、不改恢复流程、不重派发输入。

相关实现位置：`internal/engine/operations.go:258`、`internal/engine/cleanup.go:42`。

## 5. 最终命令与真实结果

执行顺序即下表顺序；全部在最终工作区状态上重跑。

```text
GOEXPERIMENT=jsonv2 go vet ./...
# exit 0，无输出

GOEXPERIMENT=jsonv2 go test ./... -count=1 -timeout 180s
# ok  internal/android      3.134s
# ok  internal/engine      25.259s
# ok  internal/observation  0.086s
# ok  internal/protocol     0.045s
# 其余包 [no test files]；exit 0

GOEXPERIMENT=jsonv2 go test ./internal/engine/ -count=5 -timeout 300s
# ok  github.com/malyjacob/mobile-use-vnext/internal/engine  138.108s
# exit 0
```

`-race` 未执行：本机 `CGO_ENABLED=0`、PATH 无 gcc，实测
`go test -race ./internal/engine/` 直接报
`go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`。因此本轮没有竞态探测器结论，
只有 `-count=5` 的重复运行稳定性证据。

## 6. 边界与仍存疑点

1. **不是实机验证**：全部结论来自 fake device / fake App / 内部状态钩子。没有 ADB、
   没有部署、没有 Windows 上的落盘耐久性证据，也没有证明真实 Android 回执时序。
2. **observe 的过期语义**：当前 `resolveObserveTarget` 不检查租约期限；过期但未撤销的会话上，
   observe 会先通过工作准入，可能在 `observe.go:45-50` 确认 full 基线，随后因
   `workRevoked` 以 `control_revoked` 失败。测试已证明它不产生新 O，但错误码不是
   `session_expired`，且拒绝请求仍可能写内部基线。未改实现，需产品/契约确认后再决定。
3. **进程崩溃窗口**：本轮修复只覆盖进程存活时的 STOP/end。若 act 返回 uncertain 后、
   STOP/end 落账前进程崩溃，账本没有持久化“该动作结果未知”，恢复后仍可能无从判断。
   这是既有恢复账本的信息边界，不在本轮改动范围内。
4. **会话级粘滞**：一次 uncertain act 会让该会话之后任意 STOP/end 的
   `execution=unconfirmed`，即使之后还有确认成功的 act。按“事实分层、不因后观察改判”的
   规格这是预期，但产品侧最好确认这一强语义。
5. **close_display 投递顺序**：`pendingCommandsLocked` 每次同步至多投递一条“新”命令，因此
   generation-b 可能在 generation-a 尚未 ack 时被投递；它们没有合并，但也不是严格的
   “单条在飞”模型。文档只承诺最多一条/次同步，若期望更严格需另行确认。
6. **测试耗时**：`TestStopBeforeDisplayProbeNeverAdmitsStart` 约 3s，原因是显示身份未知时
   收尾链按设计最多等 4s 的 App 描述窗口，测试清理在 3s 超时后返回；这是测试清理成本，
   不是产品路径延迟。
7. **race**：如第 5 节所述不可用；Windows 上若要 `-race`，需要可用的 cgo 工具链并显式
   开启 `CGO_ENABLED=1`。
