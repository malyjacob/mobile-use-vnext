# 动作树变化事实、加载/媒体分类与观察级 snap/wait_for 提示验证

实现提交 `31019462c98bb57d641caacd21aec0ad7dcf30fc`（分支 `master`，基线 `18fb2b1fb5e78774e5ffc570413517a823a18085`）。Windows amd64、Go 1.27.1、`GOEXPERIMENT=jsonv2`；手机 `24117RK2CC`（Android 16 / SDK 36）。发行 0.34.0、契约 30、versionCode 36。涉及实机：是。[归档提案](../proposals/implemented/observation-change-loading-and-snap-hint.md)

分诊为取舍，提案经用户确认后实施；本轮无跳过步骤。

## 实现与单测

### 已实现（源码边界）

- **协议与严格解码**：`internal/protocol/responses.go` 为 `Observation` 增加可选非可空 `TreeChange`（`tree_change`）与 `Hint`（`hint`），编码顺序固定为 `visual → tree_change → hint → tree`，空串省略；新增 `TreeChangeUnchanged/DetailOnly/MatchedBase`、`HintSnap/HintWaitFor` 与 `ValidObservationVisual/ValidTreeChange/ValidObservationHint`。`decode_response.go` 逐字段拒绝显式 `null` 与空串。
- **页面视觉分类**：`internal/observation/visual.go` 的 `ClassifyVisual` 按 `loading > image_heavy(<pct>%) > media_surface > sparse_tree` 输出闭集：`loading` 使用可见 `ProgressBar` 类名（简单类名以 `ProgressBar` 结尾且不含 `SeekBar`，裁屏后宽高均 `>4px`）或 `quality=unstable` 且 `stop_reason != condition`；`media_surface` 使用目标显示可见、非根的 `SurfaceView`/`TextureView`/`VideoView`/`PlayerView` 矩形（剔除 `<5%` 碎片、至多取面积最大 16 个、精确并集 `>=25%`）；`sparse_tree` 校准为可读文字 `<30` 且可交互 `<7`；`image_heavy` 保持既有四重过滤。`InferVisualHint` 保留为 `ClassifyVisual(tree, nil, fg, nil)` 的兼容入口。
- **忽略 `ref` 的树内容判等**：`internal/observation/read.go` 新增 `EqualCompactContent`（比较 `View`、忽略 `Ref`、历史 `Chain` 等价展开、无链零分配快路径）与 `EqualPublicTreeContent`（忽略 `Ref`、区分 `nil` 与已知几何），既有 `ReadEqual` 仍比较 `Ref`。
- **引擎接线**：`internal/engine/capture.go` 的 `captureAndPublish`/`publishTreeAttempt` 新增 `previousObs`；`ClassifyVisual` 在基线候选循环前锁外单次计算，被正常 `act` 消费的前帧语境（前景公开投影与屏幕宽/高/旋转）与内容判等（紧凑树 + 完整公开树）在候选循环后锁外完成；提交门禁通过后锁内 O(1) 派生 `tree_change`（`unchanged`/`detail_only`/`matched_base`，常规内容变化省略）、`visual`（`loading` 在 delta 永不省略，其余同态省略）与 `hint`（`loading → wait_for`；无有效图片且 `unchanged`/`detail_only` 或 `visual` 非空 → `snap`；其余省略）。仅 `act.go` 常规成功传被消费 `obs`，`observe`/`start`/`wait_for`/`app`/`act.delivery`/`text` partial 与无树失败观察均无 `tree_change`。
- **MCP 与探针**：`internal/mcp/result_text.go` 在 `obs` 行 `visual=` 后顺序输出 `change=`/`hint=`，非法 token 回退 `json v1 path="/observation/tree_change"`/`"/observation/hint"`，并补齐 v3/v4 反射登记与 canonical fixture；`schema.go` 同步 `treeTextGuide`/`callerGuidance`/`act`/`snap` 指引；`scripts/mcp-probe` 独立校验列序、闭集、无树禁现与“有效图抑制 `hint=snap`”。
- **版本源与文档**：`release/versions.json` 与 4 个生成文件升为 `0.34.0/30/36`；`README.md` 与 9 份 `docs/` 同步契约 30 行为。

### 已单测（可复跑）

- `GOEXPERIMENT=jsonv2 go build ./...` 与 `go vet ./...` 均退出 `0`。
- 非 engine 的 15 个包 `go test -count=1` 全部通过：`cmd/device-mcp` 5.142s、`internal/android` 18.119s、`internal/client` 8.363s、`internal/device` 1.982s、`internal/eventlog` 1.258s、`internal/httpapi` 8.229s、`internal/imaging` 5.403s、`internal/mcp` 7.622s、`internal/observation` 5.698s、`internal/protocol` 4.991s、`internal/store` 4.900s、`internal/treetext` 2.792s、`scripts/mcp-probe` 17.730s；`cmd/device-service` 与 `scripts/toolcatalog` 无测试文件。
- 引擎全量：`GOEXPERIMENT=jsonv2 go test -count=1 -timeout 15m ./internal/engine` **PASS，`789.469s`**；新增 `TestWaitForConditionCue` 定向 `PASS`（2.147s）。
- 新增/更新覆盖：`TestObservationVisual` 四分支与优先级、`TestClassifyVisualLoading/RawCaptureFilters/MediaSurface/MediaSurfaceHugeScreen/Priority`、`TestEqualCompactContent`/`TestEqualCompactContentChain`/`TestEqualPublicTreeContent`（含零分配与忽略 `Ref` 反向对照）、`TestActTreeChangeMatrix`（同基线空 delta、跨旧基线重复两个 changes、完整树几何变化、返回旧基线、常规变化）、`TestPublishFullTreeChangeIgnoresRefRenumbering`、`TestObservationHint`、`TestActUnchangedTreeImageHint`（有效图抑制 `snap`、图片失败保留 `snap`）、`TestObserveLoadingDeltaKeepsWaitForHint`、`TestActRejectionDeliveryKeepsVisualHintAndRetry`、`TestActTextPartialDeliveryKeepsVisualHint`、`TestActStaticMissingRefCarriesNoObservationHint`、`TestWaitForConditionCue`（第二帧 `unstable+condition` → `sparse_tree`/无 `change`/`snap`；采样上限 `unstable+max_samples` → `loading`/`wait_for`，`-overlay` 分别删除 `condition` 守卫与传入 `nil` quality 时用例变红）、`TestObservationVisualStatesFullAndDeltaDedup`、`internal/mcp` 的 obs 行列序/回退/反射清单/发现禁词与字节门禁、`scripts/mcp-probe` 的三字段枚举/列序/无树/有效图/both 一致性。
- 工具目录 `go run ./scripts/toolcatalog` 输出 `count=13`、SHA-256 `25a09a66f54e728b1c9fc67e6898514c6ac7438b9976528ece7e36bd4f5eab43`；发现说明实测总量 `38149 B < 38200 B`（instructions 12454 + tools_list 25695）。

### 已审（独立只读）

- 实现只读审查 subagent 复核协议/观察/引擎/MCP/探针与提案一致性，2 项发现（MCP 指引把 `unchanged|detail_only` 合称“公开树未变”、`wait_for` 早停 cue 覆盖不足）均已修正并补反向对照；其余逐项核对通过，无高严重度问题。
- `docs/` 与 `README.md` 共 10 个文件按 AGENTS 第 15 条由独立、只读 subagent 审计：首轮 3 项（README 旧两值 `visual`、`hint` 漏 `detail_only`、`tree_change` 省略语义缺失）修正后复检 **CLEAN**（版本/契约/指纹、Observation 字段与编码顺序、`visual` 四值与阈值、`tree_change`/`hint` 判据与省略语义、`obs` 行列序、链接与锚点、禁用措辞均通过）。

## 发布、部署与双入口探针

从干净实现提交 `31019462c98bb57d641caacd21aec0ad7dcf30fc` 执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins http://127.0.0.1:3000
```

- 发布清单与主机清单同源干净（`source_commit=31019462c98bb57d641caacd21aec0ad7dcf30fc`、`source_dirty=false`）；`release=0.34.0`、`contract=30`、`android=0.34.0/36`、`tool_catalog=13/25a09a66f54e728b1c9fc67e6898514c6ac7438b9976528ece7e36bd4f5eab43`。
- 手机产物：`device-service` `7d8f1791…` 9,502,880 B；`device-mcp` `853fadae…` 9,371,808 B；`control.apk` `32de8d90…` 103,965 B；`u2.jar` `28361193…`；`applist.dex` `63818f32…`；`service.sh` `4b568a9a…`。`verify-manifest` PASS。
- 主机产物：`device-mcp.exe` `aaf57ee9…` 10,305,024 B；`mcp-probe.exe` `8ff2f9aa…` 10,071,552 B；`build-host-tools -VerifyOnly` 对双清单复核 PASS。
- 手机整组覆盖部署 PASS（保留 App 私有数据）：安装 App 为 `versionName=0.34.0`、`versionCode=36`；`/health` 返回 `release_version=0.34.0`、`contract_version=30`；配对、恢复屏障与 STOP 可用性均 PASS；手机 MCP HTTP 适配器运行于 `127.0.0.1:18882`（主机 `18883`）。部署脚本提示手机 MCP stdio 旧映像仍被 1 个进程占用，依赖方进程重启与切换由用户处理。
- 独立探针（新 `build/mcp-probe.exe`，同一双清单）：
  - 主机 stdio：`-transport stdio -binary build/device-mcp.exe -suite smoke`，`-result-mode both`、`text`、`structured` 三种模式均 `RESULT PASS`；13 工具、双清单发行/契约/工具目录指纹一致，`tree_block=v5`、`inspect v3` 完整块均通过。
  - 手机 Streamable HTTP：`-transport http-remote -config …/http-remote.private.json -suite smoke -result-mode both` 为 `RESULT PASS`，13 工具、清单与指纹均通过。

## 实机行为（虚拟屏会话）

以 `com.android.settings` 等真实页面执行最小闭环（结构化 HTTP 直调；结果为 `.state/contract30-closure.json`、`.state/contract30-visual*.json`，未入库）：

1. **常规成功无树变化（已实测）**：`act` 坐标点按后发布 `O8831_5`，`tree_change=unchanged`、`hint=snap`，`changes=0`；`start` 与 `app.open` 的新观察均无 `tree_change`。
2. **`snap` 不消费 O（已实测）**：对 `O8831_5` 调 `snap` 返回 `image/jpeg 461×1024 read_only`；随后用同一 `O8831_5` 再次 `act` 被正常受理（产出 `O8831_6`），未出现 `observation_claimed`/`retired`/`gone`。
3. **历史基线返回（已实测）**：页面 A full `O8831_7` → 点击进入页面 B（`O8831_8` 为 full，随后 delta `base=O8831_8 changes=0`）→ `BACK` 返回页面 A 发布 `O8831_12`：`delta base=O8831_7`、`changes=0`、`tree_change=matched_base`、**`hint` 缺席**（未误报 `unchanged`/`snap`）。
4. **稀疏页视觉分类（已实测）**：`com.duokan.reader` 首帧 `O8831_51` 为 `full`、`quality=stable/3/stable`、6 个节点、28 个非空白可读码点、4 个可交互控件，输出 `visual=sparse_tree`、`hint=snap`。
5. **`loading`/`media_surface` 未构造（仅单测覆盖）**：本机尝试相机（预览 `SurfaceView` 未进入 uiautomator 节点树）、B 站首页/分区/视频卡片（虚拟屏未稳定进入播放器）、本地 mp4 播放器（`app.claim` 返回 `task_not_found`）与设置更新页（页面文字与控件超出阈值）均未稳定得到这两类；对应判据、阈值与 `delta` 规则由 `internal/observation`、`internal/engine` 单测与探针闭集校验覆盖。
6. **清理**：`end` 返回 `session.ended`、`cleanup.state=pending`；未声称物理清理已完成。

## 未执行项与原因

- `loading` 与 `media_surface` 未取得实机命中（原因见上）；未把单测覆盖写成实机结论。
- `-race` 未执行：Windows 宿主无 C 编译器、cgo 不可用。
- 手机 MCP 的 `text`/`structured` 结果模式未单独运行；当前手机部署为 `both`，主机 stdio 三种模式均已 PASS。
- 依赖方托管的 MCP 进程与其重连未操作；主机旧 `device-mcp.exe` 映像仍被占用并保留为备份，独立探针使用本次新产物直接启动，只证明磁盘产物与双入口契约。
- 实机验证为虚拟屏模式的最小闭环，不覆盖主屏模式、外部宿主上下文与宿主侧提示消费行为；这些边界仍由原提案 §7.2 的能力边界约束。
