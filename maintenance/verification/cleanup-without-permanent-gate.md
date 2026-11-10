# Issue #2：有限善后与取消永久准入门禁

日期：2026-11-11。此记录区分主机测试、源码审查、发布构建和手机实测。
实现提交与手机结果在实施提交落盘、设备验收后补记；本记录不以计划代替结果。

## 1. 分诊、授权与源头

recent-only 未删除和恢复不推进属于实现缺陷；旧资源未知永久封锁准入属于已批准的设计取舍。
用户整体确认 [提案](../proposals/cleanup-without-permanent-gate.md)，授权实现、发布、更新 build 并部署；
明确禁止实现和部署过程重启手机。覆盖安装保留 App 私有数据，不卸载、不清库。

原始三条先红与事故归因见 [调查记录](recents-cleanup-lock-investigation.md)，历史 fixture 保持不变。
本次在实现前将其复制到 `internal/engine/retired_cleanup_test.go`，三个用例再次到达原失败断言。
归因仍以调查基线为准，不声称还原了原 Bilibili 事故的唯一初始触发。

设计源头 `mobile-use-platform/codex/vnext-delta-redesign` 的 docs-only 提交为
`b12e8254ae1f0f353155addaffb54454a519a963`。独立只读审计修正冲突后返回 CLEAN。

## 2. 实现与相关回归

- 当前会话与退休记录分离；5 秒逻辑交接不等待物理清理，原 UI/shell/P outcome 与新 work/O 隔离。
- 旧任务 ID 与 live ID 合并处理，recent-only 实际删除并分别重读；读取失败不视为空。
- 从退休起 12 秒、最多三轮补偿；停止或淘汰保留 unconfirmed，不把未知提升为 confirmed。
- ledger 3 兼读 v1/v2，最多 8 条/24 KiB 退休详情，总计 64 KiB；容量淘汰不成为新准入条件。
- App current/retired attempt 分离；CREATE/VERIFY 异步回调，精确取消创建，有限队列与记录。
- 历史 end/status/diagnose 只投影原归属；未知 P 封闭未来接纳，但不谎称过去未执行。
- 发行源为 0.5.0 / contract 5 / Android versionCode 7；生成文件由脚本更新。

主机环境：Windows、PowerShell、Go 1.27 系列、`GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`。
主机测试不构成手机文件系统断电耐久或真实 Binder 取消证据。

已执行的必要命令及结果：

| 命令 | 真实结果 |
| --- | --- |
| `go test ./internal/engine -run '^TestIssue2' -count=1 -v -timeout 25s` | 三条原始路径由红转绿；短暂 recents 失败在原有限 worker 内重试 |
| `go test ./internal/engine -run '^(TestRetired\|TestNewSession\|TestIssue2)' -count=1 -v -timeout 30s` | 清理失败后新 start、历史路由、交接/迟到 UI、容量及 builder 隔离通过 |
| `go test ./internal/engine -run '^(TestDiscardedCleanup\|TestRetiredDisplayReuse)' -count=1 -timeout 15s` | 淘汰后的旧 worker 不派发、其它退休归属占用的 ID 不删除 |
| `go test ./internal/engine -run '^TestV2UIRecoveryCrossBootPromotesOnlyExecution$' -count=1 -v -timeout 15s` | 合成 boot-boundary 仅提升 execution，保留原 UI may_dispatch 与物理复核义务 |
| `go test ./internal/store ./internal/protocol ./internal/client ./internal/mcp -count=1 -timeout 60s` | 通过；限于账本/契约与受影响投影的包级回归 |
| `go test ./internal/android -run '^(TestRecents\|TestRecentTaskIDs\|TestTasksOnDisplay\|TestLaunchFocusAndRemoveDispatch)' -count=1 -timeout 30s` | 通过；索引空/不可识别/截断与删除适配路径 |
| `GOOS=linux GOARCH=arm64 go build ./...` | 通过 |
| `./android-control/test.ps1` | DisplayAttemptGateTest、MessageListTest 通过 |
| `./android-control/build.ps1` | 编译、对齐和 APK v3 签名核验通过 |

独立代码审查发现并关闭两个直接相关缺口：Save 期间淘汰记录仍可能派发；只检查 current 而未检查其它 retired 的 ID 复用。
修正后原审查者只读复检通过。账本/恢复测试另发现 boot promotion 丢失复核义务，保留红灯后修正。
既有测试中“永不重发关闭”“旧未知永远不可新 start”“顶层字段保留旧责任”等断言按已批准契约改为有限退休语义，保留不重放、未知事实和原路由检验。

## 3. 发布与手机验证

发布、最终引擎回归、正式文档审计与手机验收结果在实际执行后补记。
设备验收限定：可控 probe 的 start/open/操作/end、recents 读取故障期间新 start、原归属隔离、带退休记录的服务重启。
不在用户主屏制造未知迟到点击，不运行任意 root 业务命令，不重启手机。

## 4. 证明边界

- 原始 ROM recent-only 窗口能否稳定构造须据实记录；fake 删除不替代手机结果。
- Windows 与合成 boot identity 不证明手机重启、断电或真实在途 Binder 的物理隔离。
- 新会话可准入不意味着旧 Android 命令被撤回；主屏/root 的迟到影响仍是批准取舍的代价。
- 原始设备响应、树、截图、凭据、生产账本和私有辅助脚本不进入仓库。
