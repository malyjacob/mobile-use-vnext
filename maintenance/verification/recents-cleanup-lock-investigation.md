# Issue #2：recents 收尾阻塞的归因与设备恢复

日期：2026-11-11（Asia/Shanghai）。**涉及真实手机检查和恢复**；缺陷用例使用 fake device/store。
代码基线：`33241c57080ce4667b0b168f309358b4bd1d9959`，分支 `master`。
环境：Windows/amd64、PowerShell、Go `1.27.1`，`GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`；
手机运行原有 Android + Debian 13 chroot `/data/local/chroots/debian13`。
服务版本 `0.4.3`，`contract_version=4`，Android versionCode `6`。

## 1. 现象、触发条件与分诊

[远程 issue #2](https://github.com/malyjacob/mobile-use-vnext/issues/2) 报告虚拟屏会话结束后，
`execution/tasks/display=confirmed`，只有 `recents=unconfirmed`，使服务持续拒绝新会话。
报告会话的旧 task 为 `2761`，display 为 `17`。Issue 没有评论。
GitHub 连接器返回 404；实际通过本机 `gh issue view 2 --repo malyjacob/mobile-use-vnext`
读取 issue 正文和元数据。

分诊分为两层：

- **实现问题**：旧 task 仅剩 recents 索引时，已登记的 original ID 只参与核验，不作为删除
  目标；物理收尾失败后缺少通用重验，恢复路径也会直接保留旧 recents 未确认状态。
- **设计取舍**：所有适用清理项确认后才允许新会话，是现行正式契约本身的规定，见
  `docs/architecture.md:232`、`:236` 和 `internal/engine/engine.go:515`、`:627`。
  用户本次明确要求研究取消这类永久封锁；不能将取消门禁直接包装成维持原语义的缺陷修复。

上一次 UI 执行屏障修复已经遇到实机 recents 残留，见
[ui-input-execution-evidence.md §5](ui-input-execution-evidence.md#5-附加输入检查与最终状态)。
当时明确未扩展通用物理收尾范围。本记录不包含新方案批准或实现。

## 2. 先红复现与对照

诊断用例：[recents-cleanup-lock-repro.go.txt](fixtures/recents-cleanup-lock-repro.go.txt)。
通过 Go overlay 加载，不修改 `internal/engine`，不将故意失败的调查期望加入普通测试集合。

在仓库根目录实际执行：

```powershell
$taskRoot = (Get-Location).Path
$mapping = @{ Replace = @{
    (Join-Path $taskRoot 'internal/engine/zz_issue2_recents_repro_test.go') =
        (Join-Path $taskRoot 'maintenance/verification/fixtures/recents-cleanup-lock-repro.go.txt')
} }
[System.IO.File]::WriteAllText(
    (Join-Path $taskRoot '.state/issue2-recents-overlay.json'),
    ($mapping | ConvertTo-Json -Depth 3),
    (New-Object System.Text.UTF8Encoding($false)))
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
go test -overlay .state/issue2-recents-overlay.json ./internal/engine -run '^TestIssue2' -count=1 -v -timeout 45s
```

实际结果：退出码 `1`，三条用例全部到达目标行为断言后失败，无编译错误或测试超时。

| 用例 | 真实观察 | 证明边界 |
| --- | --- | --- |
| `TestIssue2RecentOnlyOwnedTaskIsRemoved` | live 为空，original/recents 含 task 41；`tasks=true recents=false checked=[41] remove_calls=[]` | 确定性漏清；不证明真实 Bilibili 当时落在此分支 |
| `TestIssue2LiveCleanupRechecksAfterTransientRecentsReadFailure` | 首次 recents 不可读，其余项确认；恢复可读且为空，三轮心跳、housekeeping、RetryRecovery 和再次 end 后仍 `cleanup_unconfirmed` | 正常进程内缺乏物理收尾复核；不定义未来重试时间策略 |
| `TestIssue2RestartRechecksStaleUnconfirmedRecents` | 从恢复快照恢复旧 recents 未确认，设备事实为空、UI 已终止、同 boot；三次 RetryRecovery 后仍 `old_resource_unconfirmed` | 启动恢复把旧未知固化；不提供真实文件落盘/崩溃耐久证据 |

对照实际执行：

```powershell
go test ./internal/engine -run '^(TestCleanupDoesNotRestartAfterCompletion|TestRecoveryPreservesUnconfirmedExecutionAndRecents|TestRestartRetainsPhysicalRecheckAfterTerminalEvidence)$' -count=1 -v -timeout 45s
```

退出码 `0`，三个顶层测试及子测试通过。它们覆盖既有幂等、保留旧未知与执行结束后必须复核
的规则，不证明长期物理收尾能收敛。本次没有运行全量回归。

## 3. 根因与证据层次

以下行号以代码基线为准：

| 位置 | 事实与因果 |
| --- | --- |
| `internal/engine/cleanup.go:174` | 在读取和移除 live tasks 之前将 `close_display` 入队；App 可并行释放显示 |
| `android-control/app/src/main/java/com/mobileuse/vnext/RootDisplayMain.java:213` | 虚拟屏使用 `DESTROY_CONTENT_ON_REMOVAL`，关闭显示可能先使 live task 脱离活动列表 |
| `internal/engine/cleanup.go:431` | original task ID 仅加入核验集合 `removed` |
| `internal/engine/cleanup.go:447`、`:450`、`:458` | live 为空就退出清扫；真正 RemoveTask 只遍历 live tasks，且丢弃删除错误 |
| `internal/engine/cleanup.go:473` | recents 不可读和仍含旧 ID 都变成 `recentsOK=false`，公共错误不能区分两类起因 |
| `internal/engine/cleanup.go:44` | 同一 session 的 CleanupStarted 防止创建第二条收尾链 |
| `internal/engine/end.go:77` | 已结束 session 只有 cleanup 不存在时才考虑创建；已有 unconfirmed 不重新推进 |
| `internal/engine/engine.go:696`、`:711` | 通用恢复调度仅面向 `e.recovery != nil`；正常结束失败不会自动成为 startup recovery |
| `internal/engine/ui_execution.go:180` | 物理重验依赖 needsExecutionRecheck；普通 recents 失败不会设置该义务 |
| `internal/engine/ui_execution.go:211` | 一次执行终结后的物理复核也会清除复核义务；它不是通用物理收尾重试器 |
| `internal/engine/control.go:566` | 普通恢复分支发现账本 recents 未确认就直接返回，不读取新 recents 事实 |
| `internal/engine/engine.go:515`、`:627` | 未完成清理被提升为全局不可用和新会话拒绝 |
| `internal/engine/start.go:101`、`:109` | 新启动替换单个 session，而 cleanup 仍只有一个槽位；不能仅删除准入 if 就假定旧归属和新会话并存正确 |
| `internal/engine/cleanup.go:488`、`:492` | 完成旧收尾时按 App 实例过滤 create_display，未绑定原 session/命令 ID；若直接允许新会话与旧清理重叠，旧清理可能移除新创建命令。此项为静态影响分析，没有运行修改准入后的竞态用例 |

本次证明的是**清理责任无法推进、且未知状态被放大为持续准入封锁**。
首次 Android 清理失败究竟由 close/task 竞态、recents 读取失败、实际索引残留还是其它设备
行为引起，现有证据不能唯一确定；公共错误合并了这些分支。

## 4. 改动边界与实机恢复

没有实现修复提交，也没有构建或部署新产物。新增内容仅为本记录和合成诊断 fixture。
正式文档、设计源头、AGENTS.md、产品代码、版本号均未修改；没有提交或推送。

用户明确告知手机已连接并授权尝试恢复。实际过程：

1. MCP `status` 和 `diagnose` 初始均为 `transport_unavailable`；`adb devices` 可见设备，
   但 `adb forward --list` 为空。重建 `18881→18880`、`18883→18882` 后可访问原服务。
2. 只读检查生产账本、live task、recents 和 status，仅输出允许的状态/ID/布尔。
   当时 `session=ended`、display 17、owned ID 2761；UI 为 `terminated` 且仍在同一 boot。
   旧 ID 已不在 live 和 recents；账本仍为 execution/tasks/display confirmed、recents
   unconfirmed，服务返回 `old_resource_unconfirmed`。这直接证实新鲜设备事实未解除旧屏障。
3. 执行一次 `adb reboot`；随后 `sys.boot_completed=1`。以账本 UI boot 身份对比，新 boot
   确实不同。没有删除或手改账本，没有卸载、停用、清除 App 数据或重新配对。
4. 运行既有 `/data/local/chroots/start-debian13.sh /bin/true` 挂载 chroot；
   `adb tcpip 5555`；在 chroot 设置 `TMPDIR=/tmp` 后运行 `adb start-server`、
   `adb connect 127.0.0.1:5555`；使用原 `service.sh start` 启动服务，启动控制 App 的
   `ControlService`，重建两条主机转发。
5. 服务先返回 `control_sync_pending`，App 同步后既有 boot-boundary 分支完成物理复核。
   生产账本变为 `cleanup=complete`、四项 confirmed、旧 session/task/UI 责任清空，
   `can_start=true`。这是新启动边界加设备事实复核的结果，不是把重启本身当作清理确认。
6. 使用原 `device-mcp -transport http -config /opt/mobile-use-vnext/mcp-config.json`
   后台启动手机适配器。当前 Codex MCP `status` 实际返回 `can_start=true`。
7. 通过当前 MCP 执行一轮不打开目标 App 的 `start → end → status`：新 virtual 会话
   `start=executed/active`，`end=executed/ended`；最终 `can_start=true`、cleanup complete，
   execution/tasks/display confirmed、recents not_applicable。没有遗留活动测试会话。

私有恢复脚本留在忽略的 `.state/`，凭据、设备身份、生产账本、原始响应、树和日志未加入
仓库。本次没有把“无需重启服务即可自愈”写成实测结论；现行产品仍需修订。

## 5. 同类核查

- 普通 end、STOP、租约到期、App 实例变化和退出都复用同一独立收尾，因此普通物理项失败
  的责任推进缺口不限于 Bilibili 或 MCP 入口。
- 执行证据重验和 startup recovery 是两套条件不同的路径；一条能重验 recents 不等于所有
  recents 未确认都能进入它。真实手机恢复前已经处于 startup recovery，单纯服务重启不能
  被认为是稳定出口。
- `removeRecoveryTasks` 同样只读取 live tasks、忽略 RemoveTask 错误，没有 recent-only
  补偿；`verifyClosedExecutionTarget` 只读核验，不负责删除。
- engine fake 的 RemoveTask 同时删除 live task 和 recents，掩盖了真实系统两类事实分离的
  窗口；本次第一条用例特意从 live 已为空的状态开始。
- `RecentTaskIDs` 在 shell 返回成功后即认为结果 known，解析零条也视为空集合；格式变化、
  部分输出和能力识别属于同类事实质量问题，本次没有证明手机出现此类输出。
- 相邻 `mobile-use-platform` 的 `codex/vnext-delta-redesign` 源头仍要求全部适用项确认，
  同时 `task-lifecycle.md` 描述拆屏后 recents 的后台精确清理。
  取消永久准入封锁涉及源头、本地正式契约及恢复责任模型，需按流程讨论与修订，不能暗改。
- 两名独立 subagent 使用 `ollama-cloud/deepseek-v4.1-flash:1017`、思考强度 `max`，分别
  只读核查 Android 路径与设计历史；未修改文件或操作设备。它们不构成正式 docs 审计。

## 6. 未覆盖项与原因

- 未还原 Bilibili 原完整交互，未实机构造 close/task 竞态或再次故意锁死设备；用户当前首先
  需要恢复使用，已有先红 fake 用例与真实旧状态证据。
- 未在本机 ROM 上验证 recent-only RemoveTask：现场旧 ID 已消失，没有对不再拥有的裸 task
  ID 发送破坏性删除，也没有另造用户任务。
- 未验证新准入/后台补偿设计、多个遗留会话、ID 复用、债务容量、旧执行链隔离、账本迁移；
  本次没有相应实现。
- 未测试真实输入中断、任意 root shell 的终止、掉电耐久性或延迟分布；本次恢复不能替代
  这些证据。
- 未运行完整回归、版本生成、发布清单、文档审计：没有改产品代码或 docs，也没有发布。
  只执行所列路径专项、gofmt 和差异检查。
