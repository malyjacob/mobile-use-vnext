# UI 输入结束证据与旧账本恢复：验证记录

日期：2026-11-10～2026-11-11（Asia/Shanghai）。**涉及真实手机验证**；模拟、文件系统与设备输入证据分别列出。
分诊：原 issue 的永久 UI 屏障来自旧规范，按用户整体确认的 R2 提案实施；没有跳过讨论或提案。

- 最终实现提交：`884adf44ee12b24edf82efb10368ca68d46a4b48`。
- 首个实现提交：`993c39e9dc8cec89e2b9519da237c0be6540267f`；第二个提交修复独立复检发现的持久化复核义务与 shell 预写/STOP 窗口。
- 设计源头：`mobile-use-platform` / `codex/vnext-delta-redesign` 的 docs-only 提交 `10718fd58ddbe2e2b858a4b008deadae3692a5cf`。
- [归因与先红记录](ui-input-execution-barrier-investigation.md)、[原 issue](../issues/open-app-unconfirmed-permanent-lock.md)、[归档提案](../proposals/implemented/ui-input-execution-evidence.md)。

## 1. 环境与证明边界

构建机为 Windows/amd64、PowerShell、Go 1.27.1，`GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`；目标为 Linux/arm64。
实机为现有 root Android 手机及 Debian 13 chroot `/data/local/chroots/debian13`，保留原配对、账本与 App 私有数据。
设备身份、boot ID、凭据、原始 API 响应、树和输入正文未入库。

发布为 `0.4.3 / contract 4 / Android versionCode 6`。正式发布清单的源码提交为最终实现提交，
`source_dirty=false`，6 个产物、12 个工具；工具目录 SHA-256 为
`a42fc3247361d1dadadce49a28214053947cd8ecacc967f81ec756a81d7a325b`。
最终 `device-service` SHA-256 为 `926931143de43d93de7bc19f63025d923a5cb09ab9cd69fbe8177a51487bb46a`。

## 2. 先红、同类核查与自动验证

原始缺任务、多任务及 shell 清除旧 UI 不确定性的先红证据见归因记录。实现后的回归包括：

| 用例/路径 | 真实结果与证明窗口 |
| --- | --- |
| `TestOpenAppAttributionFailureDoesNotRetainUIExecution` | 启动结束但关联失败，原 P uncertain；不保留已结束调用的屏障 |
| `TestUnknownUIBlocksSubsequentOrdinaryWork`、`TestOrdinaryAdmission*` | unknown UI、shell 和账本不安全统一封闭普通入口；暖 apps 缓存不能绕过 |
| `TestLateUITerminationRechecksPhysicalCleanup` | STOP 后接受原 token 的终结，重新读取物理事实，不改 P、不发布 O |
| `TestUIPreWriteDoesNotDelayStopOrDispatchAfterRevocation` | 门控 Save 时 STOP 不等待，撤权后不派发 |
| `TestUITerminalWriteFailurePreservesExecutedOutcome` | 终结写失败不反转已确认 outcome；恢复入口与准入屏障保留 |
| `TestTerminalEvidenceSavesPhysicalRecheckObligation` | UI/shell 两分支先红；终结快照必须携带持久化复核义务 |
| `TestRestartRetainsPhysicalRecheckAfterTerminalEvidence` | 旧 confirmed recents 仍含受检任务时不得放行；旧 `control.go` overlay 也按预期失败 |
| `TestShellPreWriteStopRaceTerminatesUnstartedChain` | Save 成功/失败两分支先红再绿；Prepare/Start 均为 0，记录 not_started 后 cleanup 可完成 |
| legacy/v2/store 专项 | checkpoint 耐久性、同设备/不同 boot、同步升级不丢责任、字段严格接入与快照独立性通过 |

同类核查覆盖 launch/focus/tap/long_press/swipe/key/text、聚焦 tap 与文字 set 的子步骤，
并检查 shell 责任隔离、普通入口、迟到物理证据和恢复保存窗口。

最终执行命令（均在仓库根目录，环境变量如上）：

```powershell
go test ./... -count=1 -timeout 360s
go vet ./...
$env:GOOS='linux'; $env:GOARCH='arm64'; go build ./...
python maintenance/verification/fixtures/ui-execution-mutations.py
./scripts/generate-version.ps1 -Check
git diff --check
```

结果：全量 PASS，engine `168.896s`；vet、交叉构建、版本生成检查和差异检查通过。
三个 overlay 分别去掉 checkpoint 耐久性、稳定设备身份、boot 边界守卫，均到达预期的行为失败断言；不修改原源码。

独立只读审查：Mencius 对 7 份正式文档及补丁中的 `architecture.md` 复检均为 clean；Goodall 最终实现复检剩余阻塞 0。
审查者没有修改文件；未中断或重启审计。实机故障脚本另经 Plato 只读检查为 clean。

## 3. 手机文件系统与隔离测试

用以下命令生成 ARM64 测试二进制，并由私有 runner 推入 chroot 的隔离目录
`/var/lib/mobile-use-vnext/verification-ui-evidence`，使用该目录作为 TMPDIR：

```powershell
go test -c ./internal/engine -o .state/ui-evidence-engine-tests-linux-arm64
go test -c ./internal/store -o .state/ui-evidence-store-tests-linux-arm64
python .state/run-ui-evidence-device-tests.py
python .state/run-ui-evidence-final-store-probes.py
go run .state/check-locked-ledger-compat.go
```

这些构建命令使用 `GOOS=linux GOARCH=arm64`；兼容性程序使用宿主目标。
engine 的 UI/legacy/终结恢复及 shell 预写 STOP 专项通过（`11.630s`），store 专项通过（`0.330s`）。
engine 使用 fake device，**不是实际 Android 输入或 STOP 的证据**；store 使用真实手机文件系统。

最终提交的独立探针还证明：保存 may_dispatch 后 `os.Exit(77)`，新进程可读回；保存 terminated 后同样可读回；
人为制造 rename 目标冲突后 Save 失败，原 may_dispatch 文件保留。只操作合成隔离账本。
这是进程退出和文件系统错误证据，**不是掉电耐久测试或真实输入派发中途崩溃测试**。
探针源码见 [退出窗口](fixtures/ui-execution-ledger-exit.go.txt) 与 [写失败](fixtures/ui-execution-ledger-write-failure.go.txt)。
原生产 v1 私有快照可载入、隔离升级为 v2 且保留旧责任，原快照字节不变。

## 4. 原锁死手机的恢复与故障注入

实际构建、核验与部署命令：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/deploy.ps1 -SkipBuild -PrepareUIRecovery
python .state/ui-evidence-recovery.py check
python .state/ui-evidence-recovery.py reboot
./scripts/deploy.ps1 -SkipBuild
python maintenance/verification/fixtures/verify-ui-execution-device.py
```

准备部署先使用首个实现提交，随后由最终提交重新构建并覆盖准备部署。两次部署间没有手机重启，
checkpoint 保持不变；v1 的 Session/Cleanup 事实保留，UI execution 仍 unconfirmed，
只有 checkpoint 成功落盘后才返回 `ui_input_reboot_required / recovery.entry=user / reference=none`。

第一次手机重启后确认稳定设备身份相同、boot 身份改变。重新进入现有 chroot，启用 Android TCP ADB，
以 `TMPDIR=/tmp` 启动 chroot ADB daemon 并连接 `127.0.0.1:5555`。
一次普通部署因该 daemon 尚未恢复而超时于 `control_sync_pending`；恢复依赖后，服务完成原物理资源核验，
实测 `can_start=true`、cleanup complete，execution/tasks/recents/display 均 confirmed。
没有删除或手改生产账本，没有卸载控制 App、清库或重配对。

故障注入脚本临时包装服务的 ADB 可执行路径：在专用 virtual 会话中，实际启动 `com.android.settings` 一次，
隐藏之后恰好五次任务列表读取。实测原 P 为 `uncertain/app_launch_unconfirmed`，后 observe 可读并看到目标包，
status(P) 仍 uncertain，end 后准入恢复。包装器计数为 1；finally 恢复原私有配置和正常服务。
这证明新结束证据能解除该类 execution 屏障，**不证明原报告当时的任务关联失败具体属于哪种原因**。

## 5. 附加输入检查与最终状态

受控 `com.mobileuse.probe` 的 tap 计数、中文 text 读回和 BACK 均实际成功。
该额外检查的 end 留下真实 recents 残留，结果是 execution confirmed、recents unconfirmed；未误报 ready。
按用户“不要扩大范围”的指示，没有修改通用物理收尾或增加恢复接口。
只临时停用并重新启用测试包以清除本次测试残留，保留私有数据，再通过一次手机重启及既有 boot 边界路径重新核验。
这次重启属于测试清理，不是原 v1 checkpoint 恢复必须的第二次重启。

最终再次执行 `deploy.ps1 -SkipBuild`：完整清单、运行服务与手机 HTTP 适配器哈希匹配，
配对同步、STOP 通道、恢复屏障全部通过，`can_start=true`。没有扩大产品修复范围。

主机新构建的 `build/device-mcp-0.4.3.exe` 经独立 stdio 进程验证：initialize 版本 0.4.3、
tools/list 12 项、text 模式 status 返回 can_start=true。Codex 配置已指向此文件，原 args 保留。
当前 Codex 会话仍运行旧适配器，实际工具调用返回 contract_mismatch；需重启 Codex 才能加载新进程，不能把独立探针写成当前宿主已刷新。

## 6. 未执行与限制

- 未实机构造输入回执丢失、设备断连和输入进行中 STOP：避免为验收再次人为制造未知执行责任；相关撤权与门控由模拟用例覆盖。
- 未进行真实输入中途杀进程或掉电；隔离探针不替代这些证据。
- 未给出新落盘路径额外耗时、P50/P95 或实机 in-flight STOP 延迟：没有同环境前后基线，附加输入检查也未形成完整的收尾成功样本。
- 原动作 uncertain 不被追认；任务关联判据、采样预算和固定 JAR 没有放宽。
- 通用物理收尾和已登记的 App 实例同步乱序不在本 issue 修复范围，没有继续扩展实现。
