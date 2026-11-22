# app 跨屏归属、虚拟屏 kill 彻底终止与 uncertain 恢复收敛验证记录

2026-11-22（Asia/Shanghai）。涉及手机实机。实现提交 `cabfd40c627c2b829cd00a20dc1730605cce1044`，发行 0.28.0、共同契约 24、Android versionCode 30。[归档提案](../proposals/implemented/app-window-task-lifecycle-hardening.md)

## 1. 分诊与审查

Issue #39 归因为 `appOpen` 只用单屏 `TasksOnDisplay` 采样启动前基线，`verifyLaunchedTask` 却用全设备 `TaskList` 过滤，主屏既有同包任务使 `len(matches) > 1` 立即失败；未确认分支又不收编已落地的唯一新任务，产生 `shared` 幽灵任务；`app` 终态 `uncertain` 的恢复入口自指 `status(P)`。Issue #35 归因为 `killGuard` 的虚拟屏前台拒绝，以及 `appKill` 只终止进程、从不移除 `TaskRecord`，退栈后出现只有 `n1 root` 的假前台。两者均落在已确认规范内，按 [提案](../proposals/implemented/app-window-task-lifecycle-hardening.md) R2 落地。

先红复现：`TestAppKillForegroundVirtualTaskRemovesShell` 实现前返回 `not_executed` / `kill_refused_foreground`；`TestAppOpenWithExistingTaskOnPrimaryDisplay` 实现前返回 `uncertain` / `app_launch_unconfirmed` 且 `app.task_id` 缺失。

独立只读文档审计两轮（`docs/` 10 个文件）：首轮 1 blocking + 4 advisory，复检 3 advisory。发现项由主代理逐条修订：空进程集统计字段限定到完成分支、删除"唯一 `live owned`"歧义、`open` 段补全设备基线、`uncertain` 后观察措辞、`kill` 新分支结果与 `control_revoked` 条件范围。修订后复核 **clean**，含禁用措辞、契约号/指纹/端口一致性与链接检查。

子代理模型按用户指定名单平均使用，均为思考强度 `max`：`zenmux/deepseek/deepseek-v4.1-flash` 3 次（open 路径、recovery+MCP、docs 第一批），`ollama-cloud/deepseek-v4.1-flash:1017` 4 次（kill 路径、探针套件、docs 第二批、独立审计）。全量测试、构建、部署、探针与实机复核均由主代理亲自执行。

## 2. 源码验证与反向对照

Windows x64、Go 1.27.1、JDK 17。实际执行：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go vet ./cmd/... ./internal/... ./scripts/...
go test -count=1 -timeout 15m ./cmd/... ./internal/... ./scripts/...
./android-control/test.ps1
./scripts/generate-version.ps1 -Check
go run ./scripts/toolcatalog
git -c core.safecrlf=false diff --check
```

- `go vet` exit 0。全量测试 exit 0：`internal/engine` **656.610 秒**，其余包全部 `ok`（`android` 13.325、`client` 2.240、`mcp` 0.529、`scripts/mcp-probe` 16.770 等）。`./...` 刻意不用，避免历史被忽略目录 `build/investigation-text-uncertain` 污染。
- Android 控制 App 单测 `DisplayAttemptGateTest`、`MessageListTest` 通过。`generate-version.ps1 -Check` 输出 `release=0.28.0 contract=24 android_version_code=30`。`toolcatalog` 输出 `{"count":13,"sha256":"6a22be013e8653b79f1c46e6c5c9212309454eee1b4f377836b81936b49c95e8"}`。`git diff --check` 干净。
- 新增覆盖：#39 跨屏归属（主屏既有同包任务时仍 `executed`+`owned`）、`singleTask` 误搬不误登记、他屏新建报 `application_moved`、迟到唯一任务收编与"登记失败省略 `ownership`"/"多任务歧义不回填"两个反向对照；#35 前台自有任务直杀并移壳、主屏同包仍 `kill_refused_task_elsewhere`、`RemoveTask` 未确认保持 `uncertain` 且保留登记、壳已消失跳过移除、`transferProtectsLocked` 排在 `not_owned` 之后、空进程集三种收口；恢复收敛五个 kind 均 `observe/P/retired`，`task_lifecycle_busy` 反向对照仍 `status/P`，运行中 `status(P)` 仍 `running`。
- 两个既有归因子用例 `open_app_missing_task_attribution` / `open_app_multiple_task_attribution` 的 `hookedDevice.tasks` 门控改为"启动已派发后"，覆盖与断言未削弱。

## 3. 整组构建与部署

在干净实现提交上执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins 'http://127.0.0.1:3000'
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

- 发布清单 `PASS`：`release=0.28.0 contract=24 android=0.28.0/30 artifacts=6 tool_catalog=13/6a22be01…`，`source_commit=cabfd40c…`、`source_dirty=False`。APK v3 签名通过。
- 覆盖部署保留 App 私有数据（`adb install -r`，前后未卸载）：`device-service`、`u2.jar`、`applist.dex`、`service.sh`、`config.json`、`device-mcp`、`mcp-config.json`、`connection.json` 逐项 SHA-256 匹配；App `versionName=0.28.0 versionCode=30`；`/health` `release=0.28.0 contract=24`；App 配对、恢复屏障与 STOP 可用性均 `PASS`；手机 HTTP MCP `PASS listen=127.0.0.1:18882 url=http://127.0.0.1:18883/mcp`。
- 主机工具已磁盘替换：`device-mcp.exe` sha256 `5202fe7f…`、`mcp-probe.exe` sha256 `b5b45376…`；`-VerifyOnly` 与发布清单一致，`device-mcp.exe -version` 输出 `0.28.0`。部署脚本提示旧主机 stdio 适配器进程仍持有旧映像，宿主重启与工具重新发现按 [AGENTS.md](../../AGENTS.md) 第 19 条由依赖方处理。

## 4. 独立探针与实机

探针配置由部署状态生成，置于 Git 忽略的 `.state/app-window-hardening/`，未入库、未打印凭据。

```powershell
./build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/app-window-hardening/stdio.private.json -result-mode both -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
./build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/app-window-hardening/stdio.private.json -result-mode <text|structured> -suite app-lifecycle -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
./build/mcp-probe.exe -transport http-remote -config .state/app-window-hardening/http-remote.private.json -result-mode <both|text|structured> -suite app-lifecycle -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

- stdio `smoke`（`both`）`RESULT PASS`：13 工具目录与两份清单指纹一致、`instructions` 双协议、`assert schema`、`status`、`start`（`session_mode=virtual`、`tree v4`）、`app.find`/`app.list` 两 scope、`observe`/`snap`/`wait_for`/`shell`/`inspect`/`act`/`end`/`cancel` 全通过。
- `app-lifecycle` 套件在 stdio 的 `both`/`text`/`structured` 与手机 `http-remote` 的 `both`/`text`/`structured` 共 6 次均为 `RESULT PASS`。每次闭环为 `app_open`（`ownership=owned`）→ `app_list_controlled` → `app_kill`（`target_pids=1 killed_pids=1 restarted=false`）→ `app_kill_removed`（`remaining=0`）→ 重开 → `app_close`。实机任务号依次为 220/222/224（stdio）与 226/228/230（http-remote），重开后为 221/223/225/227/229/231。`SKIP app_claim` 因受控目标无主屏任务，属预期分支。
- Issue #35 实机：受控应用处于虚拟屏前台时 `app_kill` 直接 `executed` 并杀掉唯一原实例，`app.list` 随即不含该任务，说明任务窗口容器已在同一工作内移除；不再需要"先 `open` 垫背应用"。
- Issue #39 实机（独立强类型客户端，临时程序位于 Git 忽略的 `build/` 下且已删除）：`app_open_virtual` 建 `task=234 display=197 ownership=owned` → `app_handoff` 移回主屏 → 同包再次 `app_open` 得 `task=235 display=197 ownership=owned reused=false`（**一次 `executed`，无 `uncertain`、无幽灵任务**）→ 会话 `list` 标记 `owned` → 跨屏保护下 `kill` 返回 `409 kill_refused_task_elsewhere`、`outcome=not_executed`、`recovery=none` → 关闭虚拟窗口 → `claim` 回 `#234`（`borrowed`）→ `close`（`allow_borrowed=true`）→ `end` `executed`。全程 `RESULT PASS`。
- 手机并行通道：Agent 托管的 MCP 适配器仍为旧进程，`status` 返回 `contract_mismatch`、`recovery entry=update`，与"整组同契约"要求一致；该进程重连由依赖方处理，独立探针已单独证明本次入口。

## 5. 未执行项

| 项 | 理由 |
| --- | --- |
| 依赖方 MCP 宿主重启与工具重新发现 | 按 [AGENTS.md](../../AGENTS.md) 第 19 条由用户/依赖方处理；独立探针只证明本次产物与入口 |
| `kill` 后 Android 侧 `am stack list` 原始对比 | 以 `app.list` 中任务消失（`remaining=0`）作为可观察判据，未额外抓取 `dumpsys` 原文 |
| 极细时序"他屏候选出现后消失又出现" | 提案未要求；`verifyLaunchedTask` 每轮重置候选已由确定性用例覆盖 |
| 未确认 `open` 的实机触发 | 线上时序难以稳定制造 `LaunchUnknown`；该分支由单元测试与后验收编用例覆盖 |
| 账本 v5 的历史锚点（0.8.0/0.9.0）与探针 `http-remote` 私有配置语义 | 与本次变更无关，仅做文本一致性核对 |
