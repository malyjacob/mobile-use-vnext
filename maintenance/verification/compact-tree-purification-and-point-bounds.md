# 紧凑读树净化、二元中心点与轻量视觉提示验证

实现提交 `9e3f0190dd36046803a501dfe21824c810d7bf39`（分支 `master`，基线 `efe3180069bd0352cff78c78ee4b7ca02566e491`）。Windows amd64、Go 1.27.1、`GOEXPERIMENT=jsonv2`；手机 `24117RK2CC`。发行 0.32.0、契约 28、versionCode 34。涉及实机：是。[归档提案](../proposals/implemented/compact-tree-purification-and-point-bounds.md)

分诊为取舍，提案经用户确认后实施；本轮无跳过步骤。

## 实现与单测

### 已实现（源码边界）

- **公开 label 净化（G1）**：`internal/observation/build.go` 的 `cleanReadableText`、`labelParts`、`hasSemantics`、`retainsIntrinsicLeafRole` 清洗 `U+200B`/`U+200C`/`U+200D`/`U+2060`/`U+FEFF`/`U+FFFC`，折叠空行并决定公开叶是否保留；`internal/observation/match.go` 的 `signature`/`publicSignature` 对输入框描述锚点使用同一净化结果。`device.Capture` 与 `input.value` 未改动。
- **compact 几何（X2 + G5）**：`internal/protocol/read_tree.go` 新增 `GeometryPoint`、复用既有 `Point` 并新增 `PointBounds`、`ReadTree.Validate` 的 compact/detailed 视图几何约束；`internal/observation/read.go` 的 `compactDeliveredBounds`、`compactCenter`、`compactMidpoint` 交付 `root`/`scroll` 四元矩形与其余角色二元中心点，`compactSliverCanPrune` 裁剪 `<=4px` 露边细条。
- **compact 净体（G3/A3c/A1a/G2b/A5a）**：`internal/observation/read.go` 的 `projectCompactNode`、`compactImageLeafCanPrune`、`isPlainPassiveTextLeaf`、`compactLabelCovered`、`compactHoistsTextChildren`、`compactResourceTail` 实现 `input` 标签去重、无标注非交互 `image` 全裁、祖先复合标签覆盖文本叶裁剪、变空匿名容器清理、单子隐藏匿名容器绕过、无字可点击 `view` 提升与 `resource` 短尾名。
- **视觉提示**：`internal/observation/visual.go` 的 `InferVisualHint`、`visualImageHeavy`、`collectVisualStats`、`unionRectArea` 实现四重图片口径、精确并集与 `sparse_tree` 判据；`internal/engine/capture.go` 的 `visualHint`、`publicCandidate`、`publishTreeAttempt`、`observationVisual`、`previousVisualKindLocked` 与 `internal/engine/engine.go` 的 `observationState.VisualKind` 负责提交期计算与 full/delta 去重；`internal/protocol/responses.go`、`decode_response.go` 编解码 `Observation.visual`。
- **`tree v5`**：`internal/treetext/encode.go` 输出 v5 头、点/矩形几何、`~ bounds=[x,y]` 与同 `Kind` `@`，并保留历史链读取兼容。
- **MCP 投影与发现面**：`internal/mcp/result_text.go` 注册并在 `obs` 行投影 `visual=`，`internal/mcp/schema.go` 同步 `treeTextGuide`、工具描述与 `tree v5`/点坐标/净体说明，`internal/mcp/tree_text.go` 接受点几何。
- **独立探针**：`scripts/mcp-probe/result_text.go`、`result_v4.go`、`result_semantics.go`、`tree_body_consistency.go` 同步 v5 语法、`visual` 语义与正文/结构化一致性校验。
- **版本与正式文档**：`release/versions.json` 及生成的 Go/Java/XML 常量升至 0.32.0/28/34；九份 `docs/` 与 `README.md` 同步。

### 已单测（可复跑）

- 首次执行 `go test -count=1 -timeout 15m ./...` 时 `internal/engine` 以 `784.085s` 通过；当时 `internal/mcp` 与 `scripts/mcp-probe` 仍在迁移、未通过。冻结后对其余包集中复跑全部通过，`scripts/mcp-probe` 以 `15.264s` 通过。
- 新增与更新的测试覆盖：
  - `internal/observation/build_purification_test.go`、`read_purification_test.go`、`read_bounds_test.go`、`compact_pruning_test.go`、`visual_test.go`：标签净化、点/矩形视图不变式、祖先覆盖与词边界、露边细条、`input` 去重、`image` 全裁、容器提升/清理、`resource` 短尾名、角色翻转不可差分、视觉四重口径与边界；
  - `internal/protocol/geometry_visual_test.go`、`decode_response_test.go`、`encode_test.go`：`GeometryPoint` 编解码、视图约束、`Observation.visual` 编码顺序与空串拒绝；
  - `internal/engine/visual_test.go`：full 直出、同态 delta 省略、基线回退、无树观察省略提示，已发布树的部分观察保留提示；
  - `internal/treetext/encode_test.go`：v5 头、点/矩形/`@`、`~ bounds`、历史链兼容；
  - `internal/mcp/{tree_text,result_text,schema}_test.go`、`scripts/mcp-probe/result_v5_test.go`、`tree_body_consistency_test.go`、`geometry_columns_test.go`：点几何、`visual` 行、v5 正文与 JSON 回退、双清单/发现说明一致性。
- 反向对照覆盖“去掉净体/点/视觉实现会红”的谓词级用例；文档审计修正后复检 CLEAN。
- 工具目录指纹为 `df6afaa50d401e0498bd39a63959cfbf394a4c4ed4c210ce852b2bc48a7f22fc`，发现说明 `37488` 字节。

### 已审（独立只读）

- `docs/` 按 AGENTS 第 15 条由独立、只读 subagent 审核；首次提出 3 项修正及示例/指南校正，修正后复检 CLEAN。提交说明记录“九份正式文档经独立只读审计修正复检 clean”。

## 发布、部署与双入口探针

从干净实现提交 `9e3f019` 执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins http://127.0.0.1:3000
```

- 发布清单与主机清单同源干净（`source_commit=9e3f0190dd36046803a501dfe21824c810d7bf39`、`source_dirty=false`）；六个手机产物与两个主机产物的 SHA-256/大小核验通过，`verify-manifest` 与 `build-host-tools -VerifyOnly` 均 PASS。
- 手机整组覆盖部署 `PASS`，保留 App 私有数据；部署后 `/health` 为 `release=0.32.0 contract=28`，Android 为 `0.32.0/34`，配对状态保留。
- 主机两个 exe 由 `build-host-tools.ps1` 替换，哈希从生成的 `build/host-tools-manifest.json` 读取并与清单一致。
- 独立探针使用新产物与同一双清单：
  - 主机 stdio：`build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/contract25-verification/stdio.private.json -suite smoke -manifest build/release/manifest.json -manifest build/host-tools-manifest.json` 分别以 `-result-mode both`、`-result-mode text`、`-result-mode structured` 执行，三种结果模式均 `RESULT PASS`；13 工具、双清单、`initialize` 返回的发现说明逐字节一致、`tree v5 full` 与 `inspect v3` 均通过。
  - 手机 HTTP：同配置改 `-transport http-remote`、`-config .state/contract25-verification/http-remote.private.json`、`-result-mode both`，`RESULT PASS`；手机侧鉴权检查与工具目录/发现说明核验通过。手机当前部署为 HTTP `both`。
- 手机旧 stdio 适配进程与 Windows 旧映像仍被占用，依赖宿主未切换；按分工由用户处理。

## 实机行为

1. **契约 28 分帧检查（已实测）**：`python .state/contract28-check.py --verbose --require-evidence all` 覆盖 14 帧、详细树 823 节点、紧凑树 274 节点；确认 177 个二元点与 26 个四元矩形、5 次 `view` 文本提升、196 张无标注图片裁剪（另 6 张随父容器消失）、8 个 `resource` 短尾名、5 个 `input` 标签去重；`tap` 4 次、`text` 2 次、`scroll` 2 次，11 个 `delta`，1 次 `subtree` 相等；`violations={}`，会话 `ended`、`cleanup=pending`。
2. **文本差分与基线复用（已实测）**：主代理通过新 stdio `both` 复用 `.state/contract26-baseline-check.py`（内存中将块头更新为 `tree v5`，并逐帧断言实际文本块格式等于结构化 `format`）。设置页首个 full `O5641_46` 为 `941` UTF-8 字节；随后 6 帧 `O5641_47..52` 均返回 `43` 字节的 `tree v5 delta`，引用同一 full；`inspect(base)` 成功，随后 `end`。
3. **视觉分类**：本次样本未触发 `image_heavy`/`sparse_tree`；分类判据由 `internal/observation/visual_test.go` 与 `internal/engine/visual_test.go` 覆盖，未把单测写成实机命中。
4. **清理**：`end` 后 `cleanup=pending`，未声称已确认物理收尾；原始树、页面文本与凭据未写入仓库。

## 未执行项与原因

- 视觉提示的真实页面命中样本未取得；本次实机样本没有满足判据的页面，四重口径与 full/delta 去重由单测覆盖。
- 手机 HTTP 的 `text`/`structured` 模式未单独运行；当前手机部署为 `both`，主机三种结果模式已分别 PASS。
- `-race` 竞态检查未执行：Windows 宿主无 C 编译器、cgo 不可用。
- 依赖方托管 MCP 进程与外部宿主的重连未操作；主机/手机独立探针只证明本次产物与入口。
- 既有非门禁 `budget_cutoff` 基准场景与 `scripts/verify-m6.ps1` 未改、未执行；发布以双清单核验、部署与独立探针为准。
