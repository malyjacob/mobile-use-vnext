# 拆除工作期限与未决输入造成的会话级封锁：实现与验证记录

- 实现提交：`96e4c6b4e645fb0eeee28a84ca705bb865bcaded`（2026-11-11，含契约 6→7 与
  0.7.0 / versionCode 9 的版本源前进；未拆独立版本提交，理由见提交信息）。
- 提案：[remove-work-ui-deadline-lockdown.md](../proposals/implemented/remove-work-ui-deadline-lockdown.md)。
- 环境：Windows 主机（Go 1.27.1，`GOEXPERIMENT=jsonv2`）构建；手机侧 Linux/arm64 chroot
  服务；Android 控制 App 0.7.0（versionCode 9）；设备序列 `17efb04a`；会话为 `virtual` 模式。
- 本记录涉及实机。下文把“已实现 / 已单测 / 已实测”分开写；单测通过不等于实机结论。

## 1. 分诊与先红复现

分诊：取舍（现行规范的会话判据本身导致问题），按提案推进，不按缺陷修补。

复现用例（基线 `1666bad`，保存为 `.go.txt` 以免被编译）：
[fixtures/work-ui-deadline-lockdown-repro.go.txt](fixtures/work-ui-deadline-lockdown-repro.go.txt)。

```text
$env:GOEXPERIMENT='jsonv2'; go test ./internal/engine/ -run 'TestRepro' -v
--- FAIL: TestReproWorkUISessionLockdown
    zz_repro_deadline_lockdown_test.go:31: session ended after a successful act: state=ended reason=work_deadline
--- FAIL: TestReproUnknownUIReceiptBlocksObservation
    zz_repro_deadline_lockdown_test.go:52: observe blocked after an unknown receipt:
    &{... BlockingOperationID:Preprereceipt ...}
FAIL
```

两条基线断言正是提案 §1 的证据：成功的 `act` 之后会话被 `work_deadline` 撤销；回执未知后
`observe` 被 `device_busy` 拒绝（`blocking_operation_id` 指向原 `act`）。

## 2. 已实现（代码落点）

- 会话判据：`internal/engine/engine.go`（`housekeeping`）删除 `work_deadline` 撤销段；
  只剩租约与外部撤权路径，`launchCleanup` 的原因取 `session.Reason`。
- 工作位兜底：`internal/engine/retirement.go` 新增 `detachWorkLocked`；`freezeWorkLocked`
  保留（`phase=work_deadline`，`KnownOutcome` 优先）。
- 输入窗口：`internal/engine/ui_execution.go` 删除 `uiDispatchState.deadline`；`recordUIReceipt`
  在调用返回时一次性写 `terminated`/`not_dispatched`/`unknown` 并持久化，未知回执不再提前
  返回；新增 `ui.window_closed` 事件（仅日志）。`ordinaryBarrierLocked` 与
  `availabilityLocked` 不再检查 UI。`uiPendingLocked` 收窄为“调用尚未返回”。
- 受管 shell：`internal/engine/shell.go` 新增 `shellBarrierLimit`/`shellBarrierLocked`/
  `shellUnprovenLocked`/`closeOutUnresolvedShell` 与 `ShellUnknown` 状态、`provenGone` 判据；
  `finishShellTimeout`/`finishShellRevoked` 在终止无法确认时结案让位；`housekeeping` 与
  `confirmUnresolvedShell` 在 `DeadlineAt + ShellConfirmTimeout + 3s` 后关闭屏障；
  `retireLocked` 把孤儿身份交给收尾链，`resolveRetiredExecution` 对该链只再尝试一次精确
  终止；`beginCleanupLocked`/`confirmRetiredShellLocked`/`persistUIReceipt` 以
  `provenGone` 判定 `cleanup.execution`，`unknown` 结案不报 `confirmed`。
- 恢复死代码：删除 `resolveRecoveryUI`、`finishExecutionRecovery`、`uiRebootRequiredLocked`
  及其独占 helper 与对应测试；`ui_input_unconfirmed`/`ui_input_reboot_required` 的 reason
  映射从 `recovery.go`、`status.go`、`start.go` 清除。
- 账本：`internal/engine/ledger.go` 版本 3→4；`internal/store/ledger.go` 读 1/2/3/4、写 4，
  并把 `ui.state=unknown` 作为 v4 证据（旧版本中出现即拒绝）。
- 契约面：`internal/mcp/schema.go` 的 `act`/`keepalive` 说明；`docs/architecture.md`、
  `docs/interface.md`、`docs/control-protocol.md`、`docs/release.md`、`docs/runbook.md`、
  `docs/phone-operations.md`、`docs/integration.md` 同步（契约 7、账本 v4、保证句）。
- 版本源：`release/versions.json` = 0.7.0 / 7 / 9；生成常量由
  `scripts/generate-version.ps1` 产出，未手改。

## 3. 已单测

全量（实现提交后）：

| 命令 | 实际结果 |
| --- | --- |
| `go build ./...` | 退出 0 |
| `go vet ./...` | 退出 0 |
| `go test ./... -count=1` | 全部包 `ok`；`internal/engine` 194.5s、`internal/store` 6.0s、`internal/mcp` 5.5s |

新增用例：

| 位置 | 断言 |
| --- | --- |
| `internal/engine/deadline_lockdown_test.go` `TestWorkDeadlineDetachesOnlyTheWorkPosition` | 推过 `work.Deadline` 后会话 `active`、`reason` 为空；P 冻结为 `work_deadline/not_executed`；观察仍 `ready`；被脱离的 handler ctx 已取消；下一次 `observe` 成功 |
| `deadline_lockdown_test.go` `TestUnknownUIReceiptPersistsClosedWindow` | 未知回执后账本 `ui.state=unknown`；只记一条 `ui.window_closed state="unknown"`；`uiPendingLocked()=false` |
| `deadline_lockdown_test.go` `TestShellStartAmbiguityReleasesAdmissionAtTheBound` | 未确认启动在预算内仍 `device_busy`；把记录的 `DeadlineAt` 推到界外后 `housekeeping` 释放屏障、`shell=nil`、孤儿 `ShellUnknown/unknown`、会话仍 `active`、`observe` 成功 |
| `deadline_lockdown_test.go` `TestShellTimeoutCloseOutKeepsExecutionUnconfirmed` | 超时且终止未确认：结果 `uncertain/shell_unknown`、工作位释放、会话 `active`；`end` 后 `cleanup.execution≠confirmed`；收尾链恰好再终止一次 |
| `internal/engine/internal_uncertain_test.go`（表驱动 6 行） | `act` 回执未知、shell 启动不可读、shell 超时未确认、shell 结束原因未知、`open_app` 关联失败（缺任务/多任务）：均 `uncertain`、会话 `active`/无 reason、随后普通调用可用（shell 启动行允许在有限屏障内 busy，界外放开） |
| `internal/engine/concurrency_test.go` `TestStopAfterUnknownReceiptKeepsOutcomeAndClosesWindow` | STOP/end 后 `cleanup.execution=confirmed`（窗口已关），P 仍 `uncertain`，账本窗口状态 `unknown`，新会话可开始 |
| `internal/engine/ui_execution_test.go` `TestUnknownUIReceiptAllowsSubsequentOrdinaryWork` | 未知回执后 `observe` 发布新 O、`shell` 执行、第二个 `start` 返回 `session_exists`，`status(P)` 仍 `uncertain` |
| `internal/store/ledger_v4_test.go` | v4 顶层/退休项 `unknown` 读写往返、修订号前进、v1/v2/v3 拒绝 v4 证据、v3/v4 校验一致性 |
| `internal/mcp/result_parity_test.go` | 工具目录指纹更新为 `79247d58…`；12 工具与 schema 不变 |

反向对照：删除/绕过实现后失败的方向由 `TestUnknownUIReceiptAllowsSubsequentOrdinaryWork`、
`TestStopAfterUnknownReceiptKeepsOutcomeAndClosesWindow`、`TestWorkDeadlineDetachesOnlyTheWorkPosition`
与 `TestShellTimeoutCloseOutKeepsExecutionUnconfirmed` 覆盖（基线红见 §1；这些用例在基线语义
下分别得到 `device_busy`、`cleanup.execution=unconfirmed`、会话被撤销与无限 barrier）。

## 4. 已实测：发布与部署

| 命令 | 实际结果 |
| --- | --- |
| `scripts/build-release.ps1` | `release=0.7.0 contract=7 android=9 source_commit=96e4c6b source_dirty=False`；`tool catalog: count=12 sha256=79247d58…`；六个产物均产出 |
| `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | PASS：`artifacts=6`、工具目录 12/`79247d58…`、`source_dirty=False` |
| `scripts/deploy.ps1 -SkipBuild` | 部署就绪；`version match: PASS (health release=0.7.0 contract=7)`；`App pairing: PASS`；`recovery barrier: PASS`；`STOP availability: PASS`；手机 HTTP MCP 适配器 SHA-256 与清单一致 |
| 覆盖安装 | `installed App: package=com.mobileuse.vnext versionName=0.7.0 versionCode=9`（`adb install -r`，保留 App 私有数据） |
| 设备账本 | `recovery-ledger.json` 首个 `"version":4`（11921 字节，含退休记录） |

部署脚本提示：仍有一个由宿主托管的手机侧 stdio 适配器进程运行旧二进制，需宿主重启才能
加载新文件；本次未重启任何 agent 宿主进程。

## 5. 已实测：设备行为

所有调用经 `http://127.0.0.1:18881/api/*`，`X-Mobile-Use-Contract: 7`。

**A. issue #9 最简复现：act 后静默 60 秒**

- `start`（新 P）→ `executed`，会话 `virtual`/`active`，首个观察 `complete`；
- `observe(P)` → 新 O；`act(key=BACK)` → `executed`，后观察 `complete`；
- 静默 60 秒后 `status()`：`session.state=active`、`reason` 为空。
  基线会在 `act` 后约 50 秒以 `work_deadline` 撤销会话（§1）。

**B. keepalive 只续租约**

- `keepalive(P)` → `executed`，`expires_at` 前移；
- 70 秒后再查：会话仍 `active`，`expires_at` 与 keepalive 返回值一致（未被其它期限推翻）；
- 同一 P 重放：只回原视图，`expires_at` 未再次前移。

**C. 受管 shell 超预算**

- `shell(reference=P, command="sleep 70")`：32 秒返回，`outcome=executed`、
  `termination=timeout`（runner 自身确认了终止）；
- 紧接着 `observe`：0 秒返回并发布新 O ⇒ 工作位已释放，会话仍 `active`；
- `end`：`client_end`，收尾约 2 秒完成（`execution/tasks/display=confirmed`、
  `recents=not_applicable`），`can_start=true`。
- 说明：本机 runner 总能确认终止，未取得 `termination=unknown` 的实机样本；
  “终止无法确认 → 结案让位 + 孤儿槽 + 不谎报 confirmed”由 §3 的单元测试覆盖。

**D. 服务进程重启（不重启手机）**

- 会话 `active` 时 `service.sh stop` 再 `start`：服务 pid 从 32093 变为 9043；
- `/health`：`status=ok`、`release_version=0.7.0`、`contract_version=7`；
- `status()`：`can_start=true`，旧会话 `ended`/`reason=service_restarted`、
  `cleanup.state=unconfirmed`（如实保留重启丢失的物理证据）；
- 随后 `start`：`executed`，新会话正常建立 ⇒ 恢复不要求重启手机。

**E. 部署后首个 start 的启动阶段失败**

部署后第一次 `start` 在 App 冷启动期间超过 40 秒启动预算，被工作位兜底冻结为
`outcome=not_executed`、`phase=work_deadline`、`error=control_revoked`；服务进程与账本不受影响，
随后重试 `start` 立即 `executed`。该现象属启动阶段（App 未就绪），与本次删除的会话级期限
无关；记录在此以免与 `work_deadline` 混淆：**会话级撤销已不存在，被冻结的只是那一次 start 的 P。**

## 6. 已实测：MCP 适配器

主机 stdio（新构建 `build/device-mcp.exe`，由探针实际拉起）：

```text
./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe \
  -transport stdio -result-mode both -timeout 180
RESULT PASS
```

14 项全通过：连接、12 工具、schema（`additionalProperties=false`、必填数）、`status`、
错误结果、`start`、`observe`（图片 540×1200 / 10793 字节）、`apps`、`shell`、timeline、
`inspect`、`act`（key=BACK + 后观察图片）、`end`、取消恢复。

手机 HTTP 适配器（主机转发 `127.0.0.1:18883/mcp`，新二进制）：

```text
./build/mcp-probe.exe -config .state/mcp-config-18883.json -transport http -result-mode both -timeout 180
RESULT PASS
```

18 项全通过，含 401（缺 token / agent token 混用）与 403（未放行 Origin）、放行 Origin 200、
12 工具、`start`/`observe`/`act`/`end` 与取消恢复。

两次探针在“`end` 后立即第二次 `start`”的取消检查上各自出现过一次
`second_start_failed code=display_create_failed`（服务侧 `details.app_code=display_create_timeout`）；
实测把第二次 `start` 推迟约 3 秒即成功，属 App 侧虚拟屏创建/释放的时序，不是服务契约或本次
期限改动；等待收尾完成后重跑探针即 `RESULT PASS`（同上）。

**宿主 MCP 进程重启与端到端**

部署脚本提示宿主托管的旧 stdio 适配器仍在运行；用连接器重新连接
（`mcp_connector_reconnect`，key `json-mobile-use-vnext`，配置指向重建后的
`build/device-mcp.exe`）后，Host 报告 `已注册 12 个工具`，旧适配器对契约 7 的
`contract_mismatch` 随之消失。随后经该宿主完成一轮端到端：

| 步骤 | 实际结果 |
| --- | --- |
| `start` | `executed`，会话 `virtual`/`active`，首个观察 `complete`（1 节点空屏），`expires_at=11:37:22.76Z` |
| `keepalive(Pstart)` | `executed`，`expires_at` 前移到 `11:37:26.71Z`（只增不减） |
| `status()` | `can_start=false`/`session_exists`，会话仍 `active`，`expires_at` 与 keepalive 返回值一致 |
| `end(Pstart)` | `executed`，`client_end`，`cleanup.state=pending`、`execution=confirmed` |
| `status()` | `cleanup.state=complete`、`can_start=true` |

## 7. 未执行项与原因

- **真实 input 回执未知的实机注入**：发布二进制没有注入面；输入走 chroot 内的 `adb`，
  要制造 `DispatchUnknown` 必须打断 adbd，而这会同时切断恢复观察与本次验证本身。该路径由
  §3 的单元测试（窗口关闭、`observe` 可用、账本 `unknown`）与基线红用例覆盖，实机未构造。
- **`termination=unknown` 的实机样本**：本机 runner 能确认超时终止，未构造终止失败样本；
  由 `TestShellTimeoutCloseOutKeepsExecutionUnconfirmed` 与 `internal_uncertain_test.go` 覆盖。
- **租约到期**：本轮不改租约取值与行为，未重复构造 120 秒到期；既有用例覆盖。

## 8. 结论与保留风险

- 会话不再因工具级期限结束；`session.reason` 不再出现 `work_deadline`。
- 已关闭的输入窗口不再阻止 `observe`/`act` 等普通工具；未知回执是持久事实而不是屏障。
- 受管 shell 的屏障有明确时间上界，到界让位且不谎报 `cleanup.execution=confirmed`。
- 恢复不需要重启手机；旧归属转退休记录后即放开准入。
- 保留风险（与提案一致）：输入重复执行不提供幂等；被放弃的 shell 可能与后续工作物理重叠；
  回执未知后立即 `act` 依赖调用方先 `observe` 收敛。
