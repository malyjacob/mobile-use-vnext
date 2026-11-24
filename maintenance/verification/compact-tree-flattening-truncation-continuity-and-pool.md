# 紧凑树多子扁平化、有动作长标签截断、静止保号与基线池调整验证

实现提交 `4fd55921dea6aa3fe7eceaa50c7387dca39c4c27`（分支 `master`，基线 `7a1fe518833ae224896c174eaf2720e020bf4e45`）。Windows amd64、Go 1.27.1、`GOEXPERIMENT=jsonv2`；手机 `24117RK2CC`（Android 16）。发行 0.33.0、契约 29、versionCode 35。涉及实机：是。[归档提案](../proposals/implemented/compact-tree-flattening-truncation-continuity-and-pool.md)

分诊为取舍，提案经用户确认后实施；本轮无跳过步骤。

## 实现与单测

### 已实现（源码边界）

- **公开 label 空白净化**：`internal/observation/build.go` 的 `cleanReadableText` 仅在含固定闭集零宽/占位码点时剔除码点并把残留连续空行折叠为单个换行，随后无条件 `strings.TrimSpace`；纯空白/NBSP 归空，内部非占位空白原样。`labelParts`、`hasSemantics` 沿用该清洗决定空叶去留；`internal/observation/match.go` 的输入框描述锚点继承同一清洗；`device.Capture` 与 `RoleInput.Value` 保持原值。
- **有动作标签前置截断与 `truncated`**：`internal/observation/read.go` 新增 `compactActionableLabelMaxRunes = 250`，在 compact 下行阶段、几何与 `ancestors` 压栈之前，对非 `context`、非 `input`、`Actions` 非空且某段超过 `250` 码点的节点按 UTF-8 码点截取该段前 `250` 码点（不追加省略号/总字数、不产生半字）并置 `Truncated`；被动文本、`input.Value` 与 `inspect` 全文不变。`internal/protocol/read_tree.go` 为 `ReadNode` 增加非可空布尔 `truncated`（compact 仅 `true` 写出、缺席为 `false`、显式 `null`/非布尔拒收、`detailed` 与 compact `root`/`context` 禁止 `true`），为 `ReadSetNode` 增加 `Field[bool] truncated`（set 显式 `true`/`false`，显式 `null` 拒收）；`internal/observation/read_diff.go` 在 `equalReadNode`/`diffReadFields`/`applyReadSet`/`SetTouchesInteraction` 中双向支持截断事实。
- **多子匿名容器扁平化与后置提升**：`ProjectCompactTree` 的下行/上行顺序固定为 `露边细条 → input 标签去重 → 250 码点截断 → 几何`，上行 `image 全裁 → 祖先标签覆盖的被动文本叶裁剪 → 变空匿名容器清理 → 所有隐藏匿名容器（单子与多子）按序拍平 → 无字可点击 view 文本提升 → resource 短尾名`；原始无子 `AnonymousPlain` 合成叶保留。
- **静止页面保号**：`internal/observation/match.go` 在 `buildPublicMatchIndex` 对 prev/cur 对称预计算装饰图叶、纯装饰子树、隐藏匿名普通容器、透明单枝包装层、静态图标容器与 Stage 6 安全包装层；Stage 4 在既有直接子级内容/身份 pass 之后追加透明单枝前沿（已匹配阻断、纯装饰跳过、深度 `8`、每步计费），且不在过滤 `isUsed` 的前沿上运行无内容结构 pass；Stage 6 接受仅含纯装饰图子树的交互图标容器，并按「真叶 / 图标容器」分桶隔离，安全透明包装层限深 `8` 且展开时跳过装饰子树兄弟；原有等数、`±4px` 双射、先序与单次执行保护不变。
- **基线池与差分门槛**：`internal/engine/capture.go` 常量为 `currentBaselineRevisionSpan=12`、`backupBaselineRevisionSpan=10`、`maxBaselines=5`（1 主 + 4 备）、`maxBaselineAge=180s`、`baselineDeltaNumerator=8`/`baselineDeltaDenominator=10`（严格 `<80%`，精确 80% 不合格）；保留按原 full 创建修订计算跨度与全池 `CandidateAgainst` 选最短 delta。
- **文本与 MCP 面**：`internal/treetext/encode.go` 在 `resource=` 后、`context` 前输出 `truncated=true`（false 省略），`~` 行输出 `truncated=true|false`，块头仍为 `tree v5`；`internal/mcp/tree_text.go` 在 detailed 补看拒绝节点级 `truncated`，`isAnonymousDefaultViewChain` 要求未截断；`internal/mcp/schema.go` 的 `treeTextGuide` 同步空白净化、多子扁平化、`250` 码点截断与 `truncated` 列；`scripts/mcp-probe` 校验器与 both 正文一致性同步。
- **版本与文档**：`release/versions.json` 及 4 个生成文件升为 `0.33.0/29/35`；`README.md` 与 9 份 `docs/` 同步。

### 已单测（可复跑）

- `GOEXPERIMENT=jsonv2 go build ./...` 与 `go vet ./...` 均退出 `0`。
- 非 engine 的 15 个包 `go test -count=1` 全部通过：`cmd/device-mcp` 2.132s、`internal/android` 14.758s、`internal/client` 4.924s、`internal/device` 0.037s、`internal/eventlog` 0.038s、`internal/httpapi` 3.787s、`internal/imaging` 2.039s、`internal/mcp` 5.306s、`internal/observation` 5.556s、`internal/protocol` 0.436s、`internal/store` 1.555s、`internal/treetext` 0.045s、`scripts/mcp-probe` 15.308s；`cmd/device-service` 与 `scripts/toolcatalog` 无测试文件。
- 引擎全量：`GOEXPERIMENT=jsonv2 go test -count=1 -timeout 15m ./internal/engine` **PASS，`777.450s`**。
- 实现过程中首次引擎全量运行暴露一例夹具与新备份窗口不一致：`TestI02FixedBaselineDeltaChain` 的 after-full 帧仍取 `checked=false`，在 10 步备用窗口下旧 full 的 delta 更短而被选中；夹具改为 `checked=true` 使刚确认的新 full 成为最短候选后通过（契约行为未改）。另一次引擎全量中 `TestBaselineSelectionFallsBackWhenShortestHasNoRetentionPlan` 偶发失败，单独重复 10 次与最终全量均通过，未改该用例逻辑；记为已知偶发性，后续可另立缺陷排查。
- 新增/更新覆盖：`TestCompactActionableLabelTruncation`、`TestReadDeltaTruncationTransitions`、`TestReadApplyRejectsRootTruncationSet`、`TestReadNodeTruncatedWireDecodeMatrix`、`TestReadTreeValidateTruncatedViewRules`、`TestReadSetNodeTruncatedThreeStates`、`TestReadDeltaValidateTruncatedDefinitions`、`TestEncodeFullTruncatedColumnOrder`、`TestEncodeDeltaSetTruncatedThreeStates`、`TestV5TruncatedColumnGrammar`、`TestBothModeComparesTruncatedTreeBody`、`TestTreeTextV5TruncatedColumn`、`TestTreeTextInspectRejectsTruncatedLabel`、`TestContinuityStaticIconContainers`、`TestContinuityTransparentVIPFrontier`、`TestContinuityTransparentWrappersRejectRecycledCards`、`TestContinuityTransparentStage6RejectsUnsafeMembers`、`TestDeltaCostGateExactByteRatioBoundary` 与基线池 12/10/5 窗口用例；各项均带去掉对应实现分支即红的反向对照。
- 工具目录指纹保持 `df6afaa50d401e0498bd39a63959cfbf394a4c4ed4c210ce852b2bc48a7f22fc`（本次只改 server instructions，未改工具描述与 schema）；发现说明 `ServerInstructions()` 由 `treeTextGuide` 压缩后实测总量 `37498 < 37500`，`baselineDiscoveryBytes` 保持 `37500`。

### 已审（独立只读）

- `docs/` 与 `README.md` 共 10 个文件按 AGENTS 第 15 条由独立、只读 subagent 审计；首轮 7 项（截断规则条件化、inspect 与 query 的 `truncated` 区分、透明穿透边界、投影阶段顺序、空白清洗条件、措辞与列归类）修正后复检 CLEAN；结论：`docs/README 文档审计：CLEAN（10 个文件；链接、版本与禁用措辞检查，以及契约 29 文档事实核对均通过）`。

## 发布、部署与双入口探针

从干净实现提交 `4fd55921dea6aa3fe7eceaa50c7387dca39c4c27` 执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins http://127.0.0.1:3000
```

- 发布清单与主机清单同源干净（`source_commit=4fd55921dea6aa3fe7eceaa50c7387dca39c4c27`、`source_dirty=false`）；`release=0.33.0`、`contract=29`、`android=0.33.0/35`、`tool_catalog=13/df6afaa50d401e0498bd39a63959cfbf394a4c4ed4c210ce852b2bc48a7f22fc`。
- 手机产物：`device-service` `198512a8…` 9,502,880 B；`device-mcp` `745ec62e…` 9,371,808 B；`control.apk` `81b32106…` 103,965 B；`u2.jar` `28361193…`；`applist.dex` `63818f32…`；`service.sh` `4b568a9a…`。`verify-manifest` PASS。
- 主机产物：`device-mcp.exe` `89d5c1f7…` 10,301,952 B；`mcp-probe.exe` `f8cf26aa…` 10,065,920 B；`build-host-tools -VerifyOnly` 对双清单复核 PASS。
- 手机整组覆盖部署 PASS（保留 App 私有数据）：安装 App 为 `versionName=0.33.0`、`versionCode=35`；`/health` 返回 `release_version=0.33.0`、`contract_version=29`；配对、恢复屏障与 STOP 可用性均 PASS；手机 MCP HTTP 适配器运行于 `127.0.0.1:18882`（主机 `18883`）。部署脚本提示手机 MCP stdio 旧映像仍被 1 个进程占用，依赖方进程重启与切换由用户处理。
- 独立探针（新 `build/mcp-probe.exe`，同一双清单）：
  - 主机 stdio：`-transport stdio -binary build/device-mcp.exe -suite smoke`，`-result-mode both`、`text`、`structured` 三种模式均 `RESULT PASS`；13 工具、双清单发行/契约/工具目录指纹一致，`start`/`observe` 的 `tree_block=v5`、`inspect` 的 `inspect v3` 完整块均通过。
  - 手机 Streamable HTTP：`-transport http-remote -config …/http-remote.private.json -suite smoke -result-mode both` 为 `RESULT PASS`，13 工具、清单与指纹、`tree v5`/`inspect v3` 均通过。

## 实机行为（虚拟屏会话）

以 `com.android.settings` 真实页面执行最小闭环（结构化 HTTP 直调；结果为 `.state/contract29-closure.json`，未入库）：

1. **多子扁平化与文本提升（已实测）**：页面 compact full 中 `hidden_anonymous_with_children=0`，即不再存在带孩子的隐藏匿名普通容器；`multi_label_buttons=3`，其中 `button ["搜索系统设置项","搜索"]`、`button ["WLAN","已关闭"]`、`button ["蓝牙","已开启"]` 是多段 label 提升结果，`max_depth=3`。
2. **公开 label 空白（已实测）**：该 3 帧 compact full 的全部 label 段首尾空白计数为 `0`，未出现纯空白 label。
3. **静止页面零差分（已实测）**：确认 full 基线与随后一次 `observe` 返回 `delta base=<同一 full>`、`changes=0`；列表与顶栏图标 `ref` 未发生原地删增换号。
4. **备用基线选回（已实测）**：页面 A full `O1861_33`（revision 3）→ 进入页面 B full `O1861_35`（revision 5）并在 B 连续 `observe` 到 revision 8 → `BACK` 返回页面 A，revision 9 发布 `delta base=O1861_33`、`changes=0`，**跨度为 6**（超过旧 4 步备用窗口、落在新 10 步窗口内），证明 `1 主 + 4 副` 池内备用 full 被选回。
5. **`inspect` 全文与四元矩形（已实测）**：对 `O1861_33` 的 `n11` 做 `inspect`，返回 `scope=subtree`、`view=detailed`、5 个节点全部为四元矩形、无节点级 `truncated`，label 样本 `["搜索系统设置项","搜索"]`；整树补看 112 节点、同样无 `truncated`。
6. **清理**：`end` 返回 `session.ended`、`cleanup.state=pending`；未声称物理清理已完成。

## 未执行项与原因

- 实机访问的 4 个页面样本未出现超过 `250` 个码点的有动作标签，`truncated=true` 的实机命中未取得；`250` 码点边界、被动/`input` 全文、`tree v5` 节点行与 set 行 `truncated` 语法、以及 both 模式正文/JSON 一致性由 `internal/observation`、`internal/treetext`、`internal/mcp` 与 `scripts/mcp-probe` 单测覆盖，未把单测写成实机命中。
- 备用窗口实机样本只覆盖跨度 6；跨度 10 边界与跨度 11 失效由 `internal/engine` 单测覆盖。
- 手机 MCP 的 `text`/`structured` 结果模式未单独运行；当前手机部署为 `both`，主机 stdio 三种模式均已 PASS。
- `-race` 未执行：Windows 宿主无 C 编译器、cgo 不可用。
- 依赖方托管的 MCP 进程与其重连未操作；主机旧 `device-mcp.exe` 映像仍被占用并保留为备份，独立探针使用本次新产物直接启动，只证明磁盘产物与双入口契约。
- `scripts/verify-m6.ps1` 与既有非门禁基准场景未改、未执行；发布以双清单核验、整组部署与独立探针为准。
