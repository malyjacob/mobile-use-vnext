# UI 输入执行屏障：归因与模拟复现

日期：2026-11-10。
代码基线：`ae1fbbc35f32e0dbeb2eda7a519e825f936601e9`。
范围：读取 issue、代码与设计源头，运行 Go 模拟用例；本记录不包含实现修复或实机验证。
环境：Windows/amd64，`go1.27.1`，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0`。

## 1. 现象、触发条件与分诊

报告见 [open-app-unconfirmed-permanent-lock.md](../issues/open-app-unconfirmed-permanent-lock.md)。
虚拟屏 `open_app` 已返回启动接受事实，但无法从任务列表唯一关联新任务时，操作返回
`uncertain/app_launch_unconfirmed`。后续观察可读，结束时 task、recents、display 都确认，
execution 仍为 `unconfirmed`，新会话被拒。重新加载同一账本后仍被拒。

分诊：永久保留 UI 执行屏障符合现行正式文档与既有测试，属于需要先讨论/提案的设计取舍。
依据是 [architecture.md:209](../../docs/architecture.md#收尾链) 的 UI 输入不确定性保持条款，
以及 `TestShellRecoveryPromotesShellChainCleanup/ui_input_uncertainty_stays_sticky`。
不能把仅凭显示/任务释放就清掉 execution 写成现行契约要求；设计源头
`mobile-use-platform` 的 `codex/vnext-delta-redesign` 分支
`docs/architecture/vnext-interface-spec.md:396–399` 也明确禁止这种推断。

同类核查另外发现一项实现缺陷：后续 shell 的完成证据会清掉先前 UI 动作留下的不确定性。
该缺陷与当前 UI 不确定性保持条款冲突，不能作为解除生产阻塞的操作办法。

## 2. 先红的模拟用例与实际命令

用例保存为 [ui-input-execution-barrier-repro.go.txt](fixtures/ui-input-execution-barrier-repro.go.txt)。
通过 Go overlay 注入测试文件，不修改 `internal/engine`，也不让诊断用例进入普通测试集合。
它使用仓库已有 fake device/store；不连接 Android，不执行真实设备命令。

在仓库根目录运行的 PowerShell 步骤：

```powershell
$root = (Get-Location).Path
$overlay = @{
    Replace = @{
        (Join-Path $root 'internal/engine/zz_issue_ui_input_barrier_repro_test.go') =
            (Join-Path $root 'maintenance/verification/fixtures/ui-input-execution-barrier-repro.go.txt')
    }
}
$overlayPath = Join-Path $root '.state/issue-ui-input-barrier-overlay.json'
[System.IO.Directory]::CreateDirectory((Join-Path $root '.state')) | Out-Null
[System.IO.File]::WriteAllText($overlayPath, ($overlay | ConvertTo-Json -Depth 3),
    (New-Object System.Text.UTF8Encoding($false)))
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
go test -overlay .state/issue-ui-input-barrier-overlay.json ./internal/engine -run '^TestIssue' -count=1 -v -timeout 45s
```

实际结果：退出码 `1`，以下三项都到达目标断言后失败：

| 用例 | 真实观察 | 断言性质 |
| --- | --- | --- |
| `TestIssueOpenAppPhysicalCleanupDoesNotReleaseUIBarrier/missing_task` | 启动接受；五次任务读为空；后观察包含 task 77；物理三项 confirmed；`can_start=false/cleanup_unconfirmed`；恢复后 `old_resource_unconfirmed` | 展示可用性期望与现行规则的冲突，不宣称现行契约要求放行 |
| `TestIssueOpenAppPhysicalCleanupDoesNotReleaseUIBarrier/multiple_tasks` | 多个新任务立即使关联失败；后续与上一项相同 | 同上 |
| `TestIssueShellMustNotEraseEarlierUIUncertainty` | UI 不确定性在后续 shell 完成前为 true，完成后为 false | 现行 UI 保持条款下的缺陷复现 |

前两项同时检查原 `P` 的 outcome 在后观察和收尾之后仍为 `uncertain`。模拟恢复使用引擎
生成的账本对象和 fake store，不提供文件落盘或断电耐久证据。

对照命令：

```powershell
go test ./internal/engine -run '^(TestOpenAppLaunchUnknownIsUncertain|TestStopAfterUncertainActKeepsExecutionUnconfirmed|TestRecoveryPreservesUnconfirmedExecutionAndRecents|TestShellRecoveryPromotesShellChainCleanup)$' -count=1 -v -timeout 45s
```

实际结果：退出码 `0`，四个顶层测试及各子测试通过。它们证明当前测试要求保留 UI 屏障，
以及 shell 只应按自己的证据解除自己的屏障；不构成全部引擎行为的回归结论。

## 3. 根因与证据层次

基线中的代码落点：

| 位置 | 事实与因果 |
| --- | --- |
| `internal/android/launch.go:59–92` | `am start -W` 返回 `Status: ok` 后给出 `LaunchAccepted`；这不单独证明任务归属 |
| `internal/engine/openapp.go:189–194` | 即使前一步为 accepted，任务关联失败仍调用 `markDispatchUnknown` |
| `internal/engine/operations.go:311–322` | `markDispatchUnknown` 同时设置工作与会话的不确定标记，后者超出工作位寿命 |
| `internal/engine/cleanup.go:49–61` | 会话标记令结束记录的 execution 保持 unconfirmed |
| `internal/engine/cleanup.go:78–86` | 非 shell 来源被归为 `ui_input` |
| `internal/engine/cleanup.go:298–325` | 物理收尾三项确认也不会覆盖已有 execution unconfirmed；因此 state 仍为 unconfirmed |
| `internal/engine/control.go:554–557` | 账本 execution 未确认时提前返回 `old_resource_unconfirmed` |
| `internal/engine/shell.go:660–664`、`internal/engine/shell_recovery.go:117–124` | 现有解除入口只处理 `shell_chain` |
| `internal/engine/shell.go:462`、`:545` | shell 未启动或确认结束时直接把会话的单一 ExecutionUnknown 清为 false，不区分更早的 UI 来源 |

需要修正对 issue 线索的理解：

- `verifyLaunchedTask` 的等待是顺序累加的 `200+400+800+1200=2600ms`，另加最多五次任务查询
  的 I/O 时间；不是完整确认预算只有 1.2 秒。多个匹配任务会在当前轮立即返回失败。
- 任务列表使用 `am stack list`（`internal/android/foreground.go:80`）；虚拟屏 foreground 使用
  `dumpsys activity activities`（同文件 `:45–50`）。两者都是 ADB 事实通路，观察中的 foreground
  不能描述成只来自无障碍树或 GUI。
- 后观察看到目标 App 不足以改判原操作的完成事实；本次没有证据证明报告当时是缺任务、
  多任务、解析差异或其它原因。模拟用例仅证明前两类输入能走到同一持久阻塞链。

## 4. 改动与处理边界

没有实现修复提交。新增内容仅为本调查记录与合成诊断 fixture；原 issue 保持原样。
未修改服务、App、固定 JAR、发行版本、正式文档、设计源头或生产账本。

按 [process.md §3–5](../process.md#3-分析缺陷的归因)，先确认问题定义与路线，再形成提案；
涉及判据和时序的实现需在提案确认之后进行。本记录不是方案批准或实现完成声明。

## 5. 同类核查结论

- `act.go:67` 与 `openapp.go` 中启动、复用置前、主屏分支均调用同一
  `markDispatchUnknown`，共同受到会话级不确定性保持的影响。
- UI 动作和 shell 都写同一个 `session.ExecutionUnknown`；shell 的两处清除不能区分来源。
  已用先红模拟用例证明“后续 shell 覆盖先前 UI 标记”的路径。
- `cleanupExecReasonLocked` 根据结束当时的工作/活动 shell 选择单一来源，不记录多项未决事实。
  并存来源的完整行为尚未做专项模拟，不能宣称全部组合已覆盖。
- 恢复阶段对 `execution` 与 `recents` 的守卫是独立条件，不能用清除一个 execution 标记
  推导其它未确认项已满足。

## 6. 未覆盖项与原因

- 原报告设备上的具体启动失败条件：当前已有恢复屏障；没有绕过它创建会话，也没有清账本
  或 App 私有数据取代取证。
- 原始 `am stack list` 与 `dumpsys` 的差异：没有抓取或入库个人设备原始输出。
- 修复、部署、实机解除、旧账本迁移与持久化耐久性：尚无确认的设计与实现，本调查不包含这些结论。
- 全量测试与文档审计：没有改动产品代码或 `docs/`，只运行与调查路径有关的模拟和对照测试。
