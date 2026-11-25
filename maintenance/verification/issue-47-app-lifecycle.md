# app 分支宽容归一化与按用户整包强停验证

实现提交 `0479df7eb87511725717712c6cae2369fc7027b3`（分支 `master`，基线 `d65c97653adb262e750b1e8879e1937a3b8035d7`）。Windows amd64、Go 1.27.1、`GOEXPERIMENT=jsonv2`；手机 Android 16 / SDK 36（仅 user 0）。发行 0.35.0、契约 31、versionCode 37。涉及实机：是。[归档提案](../proposals/implemented/issue-47-app-lifecycle.md)

分诊为契约取舍；提案已经用户确认后实施，本轮无跳过步骤。

## 实现与单测

### 已实现（源码边界）

- **协议与客户端**：`internal/protocol/surface.go` 的 `DecodeApp` 让七个 kind 共享严格封闭的已声明顶层成员；根对象与 `target` 嵌套仍拒绝未知键、重复键、大小写变体、尾随值与显式 `null`，`target` 内 `task_id`/`app` 只做类型检查；仅生效成员进入 `AppRequest`。`list` 及五个变更分支缺失/空 `reference` 返回 `422 invalid_request`，文案点名分支与 `reference`，`details.field="reference"`。`kill` 生效 target 接受 `task_id` 或包名。`AppView` 增加 `android_user_id`、`target_task_ids`、`remaining_task_ids`、`package_state`，编码顺序固定；`internal/client` 的 typed Encode 只写生效成员并静默忽略冗余值。
- **Android 适配层**：`am stack list` 解析 leaf/root `userId`（冲突则 `HasUserID=false`），新增 `TaskSnapshot.Complete` 与 unknown leaf 保留；`PackageIdentity` 由 `dumpsys package` 的 `Packages:` 段、目标用户 `installed=true`/`firstInstallTime`/signatures 与 `cmd package list packages -U --user <id>` 完整 uid 双向校验组成；`PackageProcesses` 按 Android 用户与完整 uid 枚举进程；`ForceStopPackage` 只执行一次 `am force-stop --user <id> <pkg>`，非零退出作为已完成事实。所有原始包名、uid、签名、命令与设备输出不入日志。
- **引擎**：新增 `app_eligibility.go`，关闭资格按 `(package, Android user)` 合并、最多 64 条、满载淘汰最早，绑定会话 epoch、显示 ID/代次、`BootIdentity`、安装身份与 5 分钟单调时钟期限；查询不续期；会话撤权、显示代次失效、handoff、外部使用或安装身份变化撤销。`close` 在认领后、逐项删除前按用户读取安装身份并带补充读取子截止，在确认移除 owned 任务的同一 epoch 锁提交里建立资格；borrowed/未确认/旧 epoch 不建立，身份未知时 close 成功但不建立资格。`kill` 两入口冻结全设备同包 live 目标，要求同屏目标全部 owned，他屏仍有同包任务拒绝；空任务集只凭唯一有效关闭资格进入；锁外读取任务快照、安装身份、进程、前台服务、播放、root 与执行身份，认领后派发前重验，一次 force-stop，按回执分 `not_executed`/`uncertain`/`executed`，最终有界采样同包同用户任务与进程并据证据清理注册与资格。
- **结果与投影**：`kill` 结果输出 `package`、`android_user_id`、`source_task_id`（仅 task 入口）、`target_task_ids`、`display_id`、`remaining_task_ids`、`target_pids`、`killed_pids`、`remaining_pids`、`restarted`（不可比较显式 null）与 `package_state`（`stopped_at_check`/`still_present`/`unknown`）；集合完整且空才 `[]`，未知省略；删除契约 30 的空原进程集预填规则。MCP 文本新增 `user=`、`target_tasks=`、`remaining_tasks=`、`package_state=` 与固定行序，工具描述发布七行矩阵；独立探针同步新字段、包级 kill、关闭后包强停与主屏重开拒绝场景。
- **版本与文档**：`release/versions.json` 与 4 个生成文件升为 `0.35.0/31/37`；`README.md` 与 9 份 `docs/` 同步契约 31。

### 已单测（可复跑）

- `GOEXPERIMENT=jsonv2 go build ./...` 与 `go vet ./...` 均退出 `0`。
- 非 engine 包 `go test -count=1` 全部通过：`cmd/device-mcp` 0.824s、`internal/android` 11.348s、`internal/client` 0.853s、`internal/device` 0.038s、`internal/eventlog` 0.645s、`internal/httpapi` 0.713s、`internal/imaging` 1.184s、`internal/mcp` 1.342s、`internal/observation` 1.616s、`internal/protocol` 0.782s、`internal/store` 1.908s、`internal/treetext` 0.705s、`scripts/mcp-probe` 16.669s；`cmd/device-service` 与 `scripts/toolcatalog` 无测试文件。
- 引擎全量：`GOEXPERIMENT=jsonv2 go test -count=1 -timeout 15m ./internal/engine` **PASS，`813.608s`**。
- 新增/更新覆盖：协议侧的七分支冗余归一化、list 缺 reference 的 `details.field`、嵌套 `target` 严格性、kill 双入口、`AppView` 新字段顺序/省略/空数组/`restarted=null`；Android 侧的真实形状 `am stack list`/`dumpsys package`/`pm list packages -U` 解析、unknown leaf、缺 userId、跨用户、重装身份变化、空签名、多 `Package [` 段、force-stop 回执；引擎侧关闭资格建立/合并/64 条淘汰/5 分钟到期/查询不续期/显示代次/会话撤权/安装身份变化/外部使用/handoff 撤销，kill 两入口、多自有窗口、空任务资格、主屏重开拒绝、STOP 前后、回执 unknown/非零/未派发、空原进程集、后验未知、`still_present`/`restarted`、空资格与迁移保护；MCP 侧的 app 行序、新字段投影、七行矩阵与清单门禁；探针侧三模式编解码、包级 kill 与反向控制。
- `./scripts/generate-version.ps1 -Check` 输出 `release=0.35.0 contract=31 android_version_code=37`。

### 已审（独立只读）

- `README.md` 与 `docs/` 共 10 个文件由独立、只读 subagent 审计：首轮 8 项阻断发现（顶层成员数量、`target` 值域分层、生效 target 互斥、断句、architecture/control-protocol 旧“进程终止”措辞、README 窗口语义、mcp 摘要顺序与未知表达）与非阻断建议全部修正后复检 **CLEAN**；版本面 0.35.0/31/37 与 `release/versions.json`、生成常量、Android manifest 一致，链接锚点可解析，无禁用措辞。

## 发布、部署与双入口探针

从干净实现提交 `0479df7eb87511725717712c6cae2369fc7027b3` 执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-probe.ps1
./scripts/build-host-tools.ps1
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins http://127.0.0.1:3000
```

- 发布清单与主机清单同源干净（`source_commit=0479df7eb87511725717712c6cae2369fc7027b3`、`source_dirty=false`）；`release=0.35.0`、`contract=31`、`android=0.35.0/37`、`tool_catalog=13/ee88b255e393d2538d68a0fd01aa9da816ccf8d5754a2436b99d642fc52ac427`；`verify-manifest` PASS。
- 手机产物：`device-service` `f3932a27…` 9,568,416 B；`device-mcp` `8b62309c…` 9,371,808 B；`control.apk` `8aff225f…` 103,965 B；`u2.jar` `28361193…`；`applist.dex` `63818f32…`；`service.sh` `4b568a9a…`。
- 主机产物：`device-mcp.exe` `7736f408…` 10,306,048 B；`mcp-probe.exe` `7258abdd…` 10,118,144 B；`build-host-tools.ps1` 输出 `RESULT PASS`。
- 手机整组覆盖部署 PASS（`adb install -r`，保留 App 私有数据）：安装 App 为 `versionName=0.35.0`、`versionCode=37`；`/health` 返回 `release_version=0.35.0`、`contract_version=31`；配对、恢复屏障与 STOP 可用性均 PASS；手机 MCP HTTP 适配器运行于 `127.0.0.1:18882`（主机 `18883`），运行映像 SHA-256 与清单一致。
- 首次部署未带 `-McpAllowedOrigins`，手机 HTTP 探针的 `origin_allowed` 按部署默认策略返回 `403`；按既有验证参数带 `http://127.0.0.1:3000` 重新执行同一 `-SkipBuild` 部署后通过。这是部署参数问题，不是制品问题。
- 独立探针（新 `build/mcp-probe.exe`，同一双清单）：
  - 主机 stdio：`-transport stdio -binary build/device-mcp.exe -suite app-lifecycle -result-mode text` 为 `RESULT PASS`，13 工具、目录指纹与 7 个 app 分支通过；其中 `app_kill_package` 因一次受控 reopen 返回 `task_focus_unavailable` 被 SKIP，`app_kill_primary_refused` 因主屏无受控任务 SKIP，其余执行分支 PASS。`-suite smoke -result-mode structured` 亦 `RESULT PASS`。
  - 手机 Streamable HTTP：`-transport http-remote -suite app-lifecycle -result-mode text` 为 `RESULT PASS`；观测到 `app_kill`（executed、`still_present`、target/killed PID）与 `app_kill_package`（executed、`target_tasks=empty`、`stopped_at_check`）真实执行，`app_kill_primary_refused` 因主屏无受控任务 SKIP。
- 磁盘替换、独立进程验证与依赖方切换分别记录：探针启动本次主机 exe 与连接本次手机适配器进程；依赖 Agent 的 MCP 进程与重连未操作，由用户处理。部署脚本两次提示手机 MCP stdio 旧映像可能仍被其他进程占用，宿主必须实际重启才能重新发现工具。

## 实机行为（虚拟屏会话）

受控应用为验证专用 `com.mobileuse.probe`（0.1.0，`adb install -r` 安装，不属于发布产物）。结构化 HTTP 直调服务（令牌来自私有配置，未入库）；独立设备采样使用 `adb shell ps -A -o PID,UID,NAME` 与 `adb shell am stack list`，采样时点为手机本地时间：

1. **虚屏前台强停（已实测，第一场景）**：会话屏 `display_id=268`，`app.open` 得 owned task `344`，前台任务即为 344。`app.kill`（`target.task_id=344`）返回 `outcome=executed`、`package=com.mobileuse.probe`、`android_user_id=0`、`source_task_id=344`、`target_task_ids=[344]`、`target_pids=[13678]`、`killed_pids=[13678]`、`remaining_pids=[]`、`restarted=false`、`remaining_task_ids=[344]`、`package_state=still_present`。22:49:35 采样：进程 0 个，任务 344 仍在 `am stack list`（随后的读取中该窗口已消失，证明结果只声称采样时点事实）。
2. **close 后按包停止仍运行的后台（已实测，第二场景）**：重新 `app.open` 得 task `346`，在受控页点击“开始后台播放”，`dumpsys activity services` 观察到 `ProbePlaybackService isForeground=true foregroundId=4211`；`app.close`（task 346）返回 `executed`、`recents_cleaned=true`、`complete=true`。22:50:43 采样：窗口 0 个、进程 1 个（PID 6815）。随后 `app.kill`（`target.app`、`allow_stop_playback=true`）返回 `executed`、`target_task_ids=[]`、`target_pids=[6815]`、`killed_pids=[6815]`、`remaining_pids=[]`、`remaining_task_ids=[]`、`restarted=false`、`package_state=stopped_at_check`；22:50:47 采样：进程 0 个、任务 0 个。
3. **用户主屏重开时拒绝（已实测，第三场景）**：在会话仍有 ready O 时以 `adb shell am start -n com.mobileuse.probe/.ProbeActivity --display 0` 在主屏创建 task `347`，再在虚屏 `app.open` 得 owned task `348`。`app.kill`（`target.app`）返回 `409 kill_refused_task_elsewhere`、`outcome=not_executed`，未派发 force-stop；`am stack list` 仍列出 347/348 两个任务；事后 `app.list` 用同一 O 仍成功，证明认领前拒绝未消费 ready O。清理：`end` 返回 `cleanup=pending`，随后 `status` 为 `can_start=true`、`cleanup=complete`；按验证边界 `adb shell am force-stop com.mobileuse.probe` 清走受控应用，最终 0 进程/0 任务。

## 未执行项与原因

- 未实测其他 Android 用户的强停：本机仅存在 `user 0`；跨用户、重装身份变化与缺 userId 由真实形状 fixture 的单测覆盖，未写成实机结论。
- 未实测关闭资格的 5 分钟真实等待与 64 条真实淘汰；由锁内状态单测覆盖。
- 未实测多个 owned 窗口的整包强停；由引擎单测覆盖，实机最小闭环只验证单窗口与 close 后后台。
- 主机 stdio app-lifecycle 的 `app_kill_package` 因受控 reopen `task_focus_unavailable` SKIP；同一路径已由手机 HTTP app-lifecycle `PASS` 与上述第二场景手动闭环覆盖。
- 未执行 `-race`：Windows 宿主无 C 编译器、cgo 不可用。
- 依赖方 Agent 托管的 MCP 进程与重连、旧 stdio 映像切换未操作；独立探针只证明本次磁盘产物与入口，不能代替宿主重启。
