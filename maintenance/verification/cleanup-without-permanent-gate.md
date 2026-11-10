# Issue #2：有限善后与取消永久准入门禁

日期：2026-11-11。实现提交：`a6507cc4cec67e6e22b7799ddf93c9315f051caa`。
此记录区分主机测试、源码审查、发布构建和手机实测；不以计划代替结果。

## 1. 分诊、授权与源头

recent-only 未删除和恢复不推进属于实现缺陷；旧资源未知永久封锁准入属于已批准的设计取舍。
用户整体确认 [提案](../proposals/implemented/cleanup-without-permanent-gate.md)，授权实现、发布、更新 build 并部署；
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
| `go test ./internal/engine -count=1 -timeout 240s` | 全量 engine 回归通过，耗时 `172.083s` |
| `GOOS=linux GOARCH=arm64 go build ./...` | 通过 |
| `./android-control/test.ps1` | DisplayAttemptGateTest、MessageListTest 通过 |
| `./android-control/build.ps1` | 编译、对齐和 APK v3 签名核验通过 |

独立代码审查发现并关闭两个直接相关缺口：Save 期间淘汰记录仍可能派发；只检查 current 而未检查其它 retired 的 ID 复用。
修正后原审查者只读复检通过。账本/恢复测试另发现 boot promotion 丢失复核义务，保留红灯后修正。
既有测试中“永不重发关闭”“旧未知永远不可新 start”“顶层字段保留旧责任”等断言按已批准契约改为有限退休语义，保留不重放、未知事实和原路由检验。

## 3. 发布与手机验证

- 独立只读文档审计首轮给出 `NEEDS_FIX`；修正 `mode_ready` 的字段说明后由同一审计者复检为
  `CLEAN`。实现提交后才重建发布清单。
- 从实现提交执行 `./scripts/build-release.ps1`，生成 release `0.5.0`、contract `5`、
  Android `0.5.0` / versionCode `7`，`source_dirty=false`。`verify-manifest.ps1` 通过：
  6 个产物的 SHA-256/大小与清单一致，工具目录为 12 个工具、指纹
  `a42fc3247361d1dadadce49a28214053947cd8ecacc967f81ec756a81d7a325b`。
- 通过 `./scripts/deploy.ps1 -SkipBuild` 部署到 `17efb04a`；使用 `adb install -r` 覆盖安装并保留
  App 私有数据，未卸载、未清库、未重启手机。安装后的 App 为 `versionName=0.5.0`、
  `versionCode=7`；服务及手机 HTTP MCP 进程的 `/proc/<pid>/exe` 均与清单中的
  `device-mcp` / `device-service` 哈希一致。
- 部署后手机侧 stdio MCP 仍持有旧二进制；重启该适配器子进程后，新进程和 HTTP 进程的
  SHA-256 均为 `2bf0bc771498bbf3b48c0d80d49ba9e804ee24aadb4381e36b453f1fdcf8c828`。
  重启对象仅为 MCP 适配器进程。
- 正常手机闭环通过：`start`、打开可控 probe、点击和 `end` 均成功；cleanup 的
  execution/tasks/recents/display 均为 `confirmed`，旧任务的 live 与 recents 命中均为 false，
  `can_start=true`。
- recents 故障注入通过：end 在 33ms 内撤权并返回；注入的 recents 读取执行 1 次失败后，
  原 cleanup 保持 `state=unconfirmed` 与 `error.code=cleanup_budget_exhausted`，没有被改写成
  confirmed。旧物理显示尚未确认释放时，第一次新 start 以当前 `display_create_failed` 失败，
  紧接着的有限重试成功；新会话为 active，旧 cleanup 完成回执未改写新会话。原配置恢复后
  再执行服务重启、start/end，`can_start=true`。
- 整个过程中的手机 boot ID 始终等于部署前保存值；没有手机重启操作。

## 4. 证明边界

- 原始 ROM recent-only 窗口未能在本次受控验收中稳定构造；live 已消失而 recents 仍命中的
  recent-only 删除由专项代码测试证明，当前记录不把它冒充为手机实测。
- Windows 与合成 boot identity 不证明手机重启、断电或真实在途 Binder 的物理隔离。
- 旧显示尚未让出底层资源时，本次新 start 仍可能以当前创建失败结束；随后重试可准入。
  这符合提案允许的“允许尝试不保证系统资源分配成功”，永久历史债务门禁未复现。
- 新会话可准入不意味着旧 Android 命令被撤回；主屏/root 的迟到影响仍是批准取舍的代价。
- 原始设备响应、树、截图、凭据、生产账本和私有辅助脚本不进入仓库。
