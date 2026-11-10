# act 重验失败交付新观察：实现与验证记录

- 实现提交：`6095a09922d4a3ddfcee97184718abd6ec224be9`（2026-11-11）。
- 版本提交：`d7e1f76`（`0.6.0` / `contract_version=6` / `versionCode=8`）。
- 提案：[act-revalidation-observation-delivery.md](../proposals/implemented/act-revalidation-observation-delivery.md)。
- 环境：Windows 主机（Go 1.27，`GOEXPERIMENT=jsonv2`）构建；手机侧 Linux/arm64 chroot 服务；
  Android 控制 App 0.6.0（versionCode 8）；设备序列 `17efb04a`；会话为 `virtual` 模式。

本记录把“已实现 / 已单测 / 已实测”分开写。实机验证脚本 `.state/verify-delivery.py` 是验证
harness，不入库，只输出代码、格式与计数，不输出树、坐标、文本、令牌或原始日志。

## 1. 构建与静态检查

| 命令 | 实际结果 |
| --- | --- |
| `go test ./...`（实现提交前，含新增用例） | 全部包 `ok`；随后在契约 6 常量下再次全量通过 |
| `go vet ./...` | 退出 0 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 退出 0 |
| `scripts/build-release.ps1` | `release=0.6.0 contract=6 android=8 source_commit=d7e1f76 source_dirty=False` |
| `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | PASS：`artifacts=6`、`tool_catalog=12/cebf1afa…` |

## 2. 已单测

`internal/engine/act_delivery_test.go`：

| 用例 | 断言 |
| --- | --- |
| T13 重验失败交付 | 结果带 `observation`（新 O、`complete`），delta 的 `base` 等于失败请求引用的 O；`retry/O/effective`；旧 O `retired`、新 O `ready` 且为 current；被拒的 `act` 零派发；用新 O + 新 P 重试派发一次 |
| T14 新帧前台不可读 | 不交付、无 `observation`，`observe/P/retired` |
| T15 无帧 | `hierarchy_unavailable` 与 `display_unavailable` 均不交付，投影保持 |
| T16 反向对照 | 关闭交付 seam 后同一拒绝不带 `observation`、退回 `observe/P/retired` |
| T17 前台变化 | `foreground_changed` 交付成功，delta `base` 等于失败 O |
| T18 撤销闸门 | 读取帧后置 `work.Revoked`，交付不发布，当前观察不变 |
| T19 重复 P 重放 | 重放视图不带 `observation`，`retry` 降级为 `observe/P/retired` |
| T20 静态拒绝的重复 P | `ambiguous_point` 的 `retry/O/effective` 保留 |

`internal/engine/recovery_entry_test.go`：投影表新增“交付”和“交付但 uncertain”两条；
`TestRecoveryDeliveryAfterClaim`（原 T3）改为断言交付；`TestContractVersionMatchesGenerated`
同步到契约 6。

`internal/mcp/result_text_test.go`：`TestTextCodecDeliveredObservationOnError` —— 错误结果携带
交付观察时，`result v1 is_error=true` 下仍渲染树块且无损往返。

## 3. 已实测：部署

`scripts/deploy.ps1 -SkipBuild` 真实结果：

- `/health`：`release_version=0.6.0`、`contract_version=6`；
- 已安装 App：`package=com.mobileuse.vnext`、`versionName=0.6.0`、`versionCode=8`（`adb install -r`，保留私有数据）；
- 部署后逐个比对清单哈希：`device-service`、`device-mcp`、`u2.jar`、`applist.dex`、`service.sh`、
  `config.json`、`mcp-config.json`、`connection.json` 全部 match；
- 手机侧 HTTP MCP 适配器：401 探针 + 唯一进程 + `/proc/<pid>/exe` 哈希等于清单；
- `can_start=true`。

部署脚本报告：仍有一个由宿主托管的手机侧 stdio 适配器进程运行旧二进制，需由宿主重启加载
新文件；本次未重启任何 agent 宿主进程（按要求只重建适配器）。

## 4. 已实测：实机行为

脚本迭代三轮（前两轮是 harness 的 O 追踪与选点缺陷），下表是最终一轮的真实输出：

场景 A（probe app，9 节点）：

- 通过 `adb shell input -d <display> tap` 改变 checkbox 状态后，用旧 O 的同一坐标 `act`；
- 得到 `409 target_changed`、`outcome=not_executed`；
- 结果带 `observation`（新 O、`status=complete`、`format=full`）与
  `recovery`（`entry=retry`、`reference=O`、`input_state=effective`）；
- 交付的 O 为 `ready`；其树中 checkbox 为 `checked=true`，注入前为 `false` ⇒ 只有注入的那一次
  点击，被拒的 `act` 零派发；
- 用交付的 O + 新 P 立即重试（中间没有 `observe`）⇒ `200 executed`；重试后再观察的 button
  tap 计数仍为 `0`；
- 用失败的旧 O 再 `act` ⇒ `409 observation_retired`、`recovery=observe/P/retired`。

场景 B（`com.android.settings`，128 节点）：

- `open_app` ⇒ `observe(full)` ⇒ 通过 adb 滚动列表 ⇒ 用旧 O 的旧坐标 `act`；
- 得到 `409 target_changed`，结果带完整新 O（`ready`）与 `retry/O/effective`。

收尾：`end` 首响 `cleanup.state=pending`；轮询后 `can_start=true`，准入屏障正常恢复。

## 5. 已实测：MCP 适配器

- 主机适配器重建：`go build -trimpath -buildvcs=false -o build/device-mcp.exe ./cmd/device-mcp`，
  `sha256=e3f0238cac6633fee0b52fde9055a2c4e7f2a6b320e6f4746174d2eb2797e5dc`；
- `./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe
  -transport stdio -result-mode both -timeout 180` ⇒ `RESULT PASS`：
  连接、12 工具、schema（`additionalProperties=false`、必填数）、`status`、错误结果、
  `start`、`observe`、`apps`、`shell`、`timeline`、`inspect`、`act`（key=BACK，后观察 JPEG
  540×1200 / 10793 字节）、`end`、取消恢复全部通过；
- 手机侧适配器由部署脚本启动并核验 PID 与运行二进制哈希（见 §3）。

## 6. 未覆盖项与原因

- **交付为 delta 的实机样本**：两次场景交付都是 `full`（9 节点小树与整体滚动的差分不满足
  “可编码且收益 < 70%”的既有规则）。delta 与 `base` 规则由 T13/T17 覆盖，未在实机取得样本。
- **`foreground_changed` 的实机交付**：未构造前台切换场景；由 T17 覆盖。
- **MCP 文本模式“错误结果带树块”的实机样本**：探针不制造 `target_changed`；由
  `TestTextCodecDeliveredObservationOnError` 覆盖。
- **主机侧 agent 宿主重启**：按要求只重建适配器，未重启 DSH/Codex 等宿主进程，也未改动其配置；
  旧 stdio 宿主进程仍会使用旧二进制，直到下次重启。
- **交付路径的工作截止冻结竞态**：未构造分钟级工作超时；`applyFrozenResult` 只在
  `phase=work_deadline` 时替换结果并清空 `observation`，属既有超时路径。
