# shell 保留当前观察实现与验证记录

状态：2026-11-09。本页记录提案
[shell 不再作废当前观察，也不再后置观察](../proposals/implemented/shell-keeps-observation.md)
的实现落点、单元测试与实机验证。**实机部分已在 `24117RK2CC` 上执行**；未构造或未观察到的
场景在 §6 明确列出，不把单元测试或代码路径检查写成实机结论。

## 1. 环境与边界

| 项 | 值 |
| --- | --- |
| 实现提交 | `8ee3eda76fb253cdcd54cd80e67e6b40734ffcca` |
| 工作区 | `E:\Code\Python\mobile-use-vnext` |
| Go | `go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2` |
| 构建目标 | `linux/arm64`，`CGO_ENABLED=0` |
| 发布 | `0.4.0`；`contract_version=2`；Android `versionName=0.4.0`、`versionCode=3` |
| 清单 | `source_commit=8ee3eda76fb253cdcd54cd80e67e6b40734ffcca`，`source_dirty=false` |
| 产物 | `device-service` SHA-256 `bc6788cd…`、`device-mcp` `4a2e7557…`、`control.apk` `dda29e43…`；工具目录 `12/b44f8909…` |
| 设备 | `24117RK2CC`（`ro.serialno=17efb04a`），Android 16（API 36），arm64 |
| 部署 | chroot `/data/local/chroots/debian13`；服务 `127.0.0.1:18880`；主机转发 `18881` |
| 改动边界 | `internal/engine`（`shell.go`、`shell_test.go`）、`internal/mcp/schema.go`、`internal/mcp/server_test.go`、`docs/interface.md`、`docs/mcp.md`、`README.md`、`scripts/verify-p4.ps1` |

请求/结果字段定义与 `contract_version` 未变。可观察行为变化只有一条：成功的 `shell` 结果
不再含 `observation`，`next` 为空；同一个 O 在命令结束后仍是 `ready`。实现提交的 `docs/`
变更经独立只读文档审计：首轮返回两条 findings（§1 一处指代丢失、§12 一处章节引用错误），
按最小修正改后复检为 clean。

## 2. 实现落点

| 提案条目 | 落点 |
| --- | --- |
| 派发边界不退休 O、不清基线 | `internal/engine/shell.go` 的 `Shell` 派发边界：只置 `work.DispatchUnknown` 与 `session.ExecutionUnknown` |
| 结束不采集、不发布、不分配 O | `internal/engine/shell.go`：`finishShellResult` 去掉 `captureAndPublish`、`result.Observation`、`next=observe` 与 `post_observation_failed`；`finishShellConfirmed` 只读输出、提升收尾屏障 |
| 未启动不带后置观察 | `internal/engine/shell.go`：`finishShellNotStarted` 去掉 `observe` 参数与五个调用点 |
| 撤销/不确定路径不变 | `revokeSessionLocked` 仍退休 O；`finishShellUncertain` 不发布观察、保持受管链屏障 |
| 结果形状 | 成功：仅 `shell` 事实；撤销后确认停止：`next=status` 并带会话视图；未启动/不确定：既有错误码 |
| 时钟不延长 | `internal/engine/capture.go` 的 `ActionableUntil` 未改；`shell` 只续租会话，不续租 O |
| MCP 描述 | `internal/mcp/schema.go` 的 `shell` 描述加“命令可能改变界面且不会返回新树；若预期会改屏，随后应 observe” |
| 正式文档 | `docs/interface.md` §1 与 §12；`docs/mcp.md` §3 的 isError 行改为“`act`/`open_app` 的后树” |
| 反向对照与用例 | `internal/engine/shell_test.go`：`TestShellKeepsReadyObservation`、`TestShellChainPerformsNoCapture`、`TestShellThenActRevalidatesTheSameObservation`、`TestShellThenActRejectsAChangedTarget`、`TestShellThenActRejectsExpiredObservation`、`TestShellNeedsNoTreeForItsResult`、`TestShellStartOutcomes/not_started`；`internal/mcp/server_test.go` 的 shell 用例补“无 `observation`” |
| 验证脚本 | `scripts/verify-p4.ps1` 的 shell 段改为断言无 `observation`、`next` 为空，并用 `status(O)` 断言 O 仍 `ready`/`effective` |

## 3. 执行的命令与真实结果

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/engine -run 'TestShell' -count=1`（实现前，基线） | **红**（7 个）：`TestShellKeepsReadyObservation`（“shell must not publish an observation”）、`TestShellChainPerformsNoCapture`（首个 `shell` 结果带 `observation`）、`TestShellThenActRevalidatesTheSameObservation`、`TestShellThenActRejectsAChangedTarget`、`TestShellThenActRejectsExpiredObservation`（三条都因 `shell` 结果带 `observation` 先失败；基线随后还会因 O 被退休而拒绝 `act`）、`TestShellStartOutcomes/not_started`（“unstarted command must not publish an observation”）、`TestShellNeedsNoTreeForItsResult`（“a broken tree changed the shell result”，`next=observe`） |
| `go test ./internal/engine -run 'TestShell' -count=1`（实现后） | `ok github.com/malyjacob/mobile-use-vnext/internal/engine 24.112s` |
| `go test ./... -count=1` | 全部 `ok`；`internal/engine` 137.5s，其余包均通过 |
| `go vet ./...` | 无输出 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过 |
| `powershell -File scripts/build-release.ps1` | 成功；`release=0.4.0 contract=2 android=3 source_commit=8ee3eda… source_dirty=False`；工具目录 `count=12 sha256=b44f8909…` |
| `powershell -File scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS`；6 个产物、`tool_catalog=12/b44f8909…` |
| `powershell -File scripts/deploy.ps1 -SkipBuild` | 成功；`version match`/`App pairing`/`recovery barrier`/`STOP availability` 均 `PASS`；phone MCP HTTP adapter `PASS` |
| `python .state/verify-shell-keeps-observation.py` | `PASS: 32 checks`；结果见 §4 与 §5 |
| `powershell -File scripts/verify-p4.ps1 -Section Shell` | `PASS verify-p4`：`shell: normal/output/exit/timeout`、`shell: STOP`、`shell: PASS`（更新后的脚本断言无 `observation`、`next` 为空、`status(O)` 仍 `ready`/`effective`） |

实现提交前的实现阶段另有一次同样的发布构建与部署（`source_dirty=true`，代码与本记录相同），
用于先验证脚本自身；本页数字全部取自 `source_dirty=false` 的干净构建与随后的正式运行。

## 4. 实机用例与结果

设备上由服务真实完成 `start`、`open_app`、`observe`、`act`、`shell`、`end`；会改屏的命令通过
`shell` 内的 `input -d <display>` 注入目标虚拟屏，因此命令之后屏幕确实变化。

| 用例 | 操作 | 真实结果 |
| --- | --- | --- |
| 正常命令 | `shell` 执行 `printf shell-ok` | HTTP 200、`executed`/`exited`/`exit_code=0`；输出含标记；**无 `observation`**、`next` 为空；响应 107 B |
| O 仍可用 | 同一 O 立即 `status(O)` | `observation_state=ready`、`input_state=effective` |
| 链式命令 | 同一 O 连续 3 条 `true` | 3/3 HTTP 200、`executed`；均无 `observation`、`next` 为空；每条 99 B；之后 `status(O)` 仍 `ready`/`effective` |
| `shell` 后 `act`（未变屏） | 用同一 O 的 `ref` 点击 `TAP` | HTTP 200、`executed`；设备计数 `taps` 0→1，只派发一次 |
| `shell` 改屏后 `ref act` | `shell` 注入一次复选框点击（`checked` false→true），再用旧 O 的 `ref` 点同一复选框 | HTTP 409、`not_executed`、`target_changed`、`next=observe`；未派发输入 |
| 改屏只发生一次 | 随后 `observe` | 复选框只被切换一次（`checked=true`），`taps` 仍为 1 |
| `shell` 后 `observe` | `observe(O, full=false)` | HTTP 200、`status=complete`；本机按既有编码门禁回退 `full`（见 §6） |
| O 在命令期间过期 | 同一 O 连续 3 条 `sleep 22`（共约 68 s）后再 `act` | 3/3 命令 `executed` 且无观察；`act` HTTP 409、`not_executed`、`observation_expired`；`taps` 不变 |
| 大树（设置首页） | `open_app(com.android.settings)`、`observe` full（128 节点）后 `shell true`，再 `observe` | 命令无观察（99 B）；随后 `observe` HTTP 200、`status=complete`、128 节点 |
| 结束 | `end` | 收尾完成、`can_start=true` |

## 5. 实机指标

| 指标 | 结果 |
| --- | --- |
| `shell` 响应字节 | 107 B（带输出）/ 99 B（其余 3 条链式），中位 99 B |
| 同页 full 树响应字节 | 探测 App 1274 B / 9 节点；设置首页 11533 B / 128 节点 |
| `shell` 延迟（含命令本身） | 中位 1260 ms；`sleep 22` 三条 22.7–22.9 s |
| `shell` 后第一次 `act` | 未变屏 1/1 `executed`；改屏 1/1 `target_changed`；过期 1/1 `observation_expired` |
| O 状态 | 单条与链式 `shell` 之后均 `ready`/`effective` |
| 事件日志 | 9 条 `shell.dispatch`、9 条 `shell.finish`，全部 `phase="finished"`；无 `post_observation_failed` |
| 端到端清理 | 每次会话 `end` 后 `cleanup=complete`、`can_start=true` |

响应字节与延迟只报告本次构建的实测值；没有部署改动前构建做 A/B 对照（§6）。

## 6. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 实机“`shell` 后 `observe` 返回 delta” | 未观察到：本机连续两次采集之间，探测页与设置页都会由既有保号给一个容器节点重新分配 ref（实测 `n4`→`n10`，其子节点 ref 不变），既有 delta 编码按 ref 识别节点，属于“不可表达”而回退 full。该回退在**没有任何 `shell`** 的对照序列里同样复现，因此与本次改动无关。基线保留由单元测试断言（`e.baseline` 指针在命令前后相同） |
| 实机“树坏时 `shell`”（逃生出口） | 未构造：需要杀 u2 或破坏层级读取，而 `EnsureU2` 只在服务启动时调用，恢复只能重启服务；单元测试用 `captureErrs` 覆盖“树不可读时命令结果仍干净、O 仍 `ready`” |
| 响应字节/延迟的改动前 A/B | 未执行：需要回退部署旧构建；本页只给当前值，并以同页 full 树字节作为被删除的后置观察成本参照 |
| MCP 传输层 `shell` | 未单独通过 MCP 宿主发起：行为在引擎/HTTP 路径完成，MCP 只做投影；`internal/mcp/server_test.go` 断言 `shell` 结果正文不含 `observation` |
| STOP/撤销与 SIGKILL 恢复 | 既有 P3/P4 记录与 `shell_stop_test.go`/`shell_recovery_test.go` 覆盖；本轮部署脚本的 `recovery barrier` 与 `STOP availability` 为 `PASS`，未重新做破坏性重启演练 |
| `go test -race` | 未执行：Windows 宿主无 C 编译器，`CGO_ENABLED=0` 目标下不可用；与既有记录一致 |
| 大树的 `shell` 后 `act` 拒绝率 | 未在设置首页构造改屏后 `act`；探测 App 的改屏场景已覆盖同一判据 |

## 7. 结论

- **已实现**：`shell` 派发不动 O/基线、结束不采集不发布、未启动无观察、成功结果无
  `observation` 且 `next` 为空；MCP 描述与 `docs/interface.md`/`docs/mcp.md` 同步。
- **已单测**：同一 O 与基线保持、零采集、零新 O、成功形状、树坏时命令结果不受影响、
  `shell` 后 `act` 成功/`target_changed`/`observation_expired`、N 条链式命令零采集、
  `not_started` 与撤销/不确定路径。
- **已实测**：单条与链式命令结果无观察、O 仍 `ready`/`effective`；`shell` 后 `act` 未变屏
  成功、改屏 `target_changed`、过期 `observation_expired` 且未派发输入；`shell` 改屏只生效
  一次；`end` 收尾完成；更新后的 `verify-p4 -Section Shell`（含 STOP）为 `PASS`。
- 未构造或未观察到的场景已在 §6 列出；它们不改变“实现已落地”的结论，只界定实机证据边界。
