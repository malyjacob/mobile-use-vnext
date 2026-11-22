# 提案：wait_for 内容稳定判据与 text/工具描述不再披露 quality/effect

状态：已实现（实现提交 `6202abe40a18ccbfae5d0975b7173509eba50d26`，2026-11-22）。
发布交付与验收见[验证记录](../../verification/wait-content-stability-and-text-omit.md)。
基线提交：`61d0b5e0830afccbe4fa607bcae7286a90dd7425`（分支 `master`，2026-11-22）。
适用范围：`wait_for stable=true` 的稳定判据与 `observation.quality` 语义；MCP `text`/`both`
文本投影；MCP 工具描述面。
影响范围：`internal/engine/wait_for.go`、`internal/engine/wait_content.go`（新增）、
`internal/mcp/result_text.go`、`internal/mcp/schema.go`、`internal/mcp/catalog.go`、
`internal/mcp/server.go`、`scripts/mcp-probe`、`docs/`、`README.md`、`release/versions.json`。
契约（`/api/*` 字段与 `contract_version`）：`/api/*` 字段形状不变；`wait_for stable=true` 的
默认判据从整树改为内容稳定，按 `docs/release.md:51-52` 的默认行为变化规则，
`contract_version` 必须由 `22` 前进为 `23`。MCP `text`/`both` 文本与**整个工具描述面**
不再出现 `quality`/`effect`/`观察质量`/`滚动效果`；`structured` 保留原对象，只作为机器契约。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时
的 `HEAD` 重新核对（重点：`internal/engine/wait_for.go`、`internal/mcp/schema.go`、
`internal/mcp/result_text.go`、`scripts/mcp-probe/result_semantics.go`、
`scripts/mcp-probe/checks.go`）。

---

## 0. 摘要

只做两件事：

1. `wait_for **stable=true**` 使用**内容稳定**判据：公开树忽略所有 `bounds`，并忽略无动作
   叶子的 `label`/`value`/`hint` 变化；不加请求参数，不改 `act` 的 settle 判据。
   `stable=false` 的条件等待仍用原整树摘要，质量语义不变。
2. MCP `text`/`both` 的文本不再出现 `quality=`/`effect=`；**整个工具描述面**
   （`CatalogSnapshot()` 的全部 13 个 tool 描述/schema 属性说明 + `ServerInstructions()`）
   不再出现 `quality`/`effect`/`观察质量`/`滚动效果`。`structured` 字段保持原样，只作为
   机器契约；工具描述可以保留 `structured` 这个模式名，但不得提上述四个词。

最终提交推送后，用 `gh issue comment` + `gh issue close` 回复并关闭 #33/#40/#41；回复中
写明本轮范围、未做项与提交/验证链接。

---

## 1. 问题

### 1.1 现象

- **#33**：网易云播放页的旋转封面、实时进度文本、波形持续变化，`wait_for stable=true`
  要求连续 3 帧整树摘要相等（`wait_for.go:287-320`），该页永远不成立，等于恒定超时。
- **#40/#41**：`quality`/`effect` 在 text 与工具描述里持续暴露，调用方无法决策，反而制造
  噪声；`structured` 里这两个对象仍有各自的机器契约。

### 1.2 证据

| 断言 | 证据 |
| --- | --- |
| `wait_for` 的稳定门禁对每个轮询帧用 `sampleDigestOf`（L1 原始身份链 + L2 公开树 + L3 语境）计算 `consecutive`，要求连续 3 帧相等 | [wait_for.go:287-320](../../../internal/engine/wait_for.go#L287-L320)、[settle.go:298-308](../../../internal/engine/settle.go#L298-L308) |
| L1/L2 都含 `bounds`，动画页永不收敛 | [settle.go:346-371](../../../internal/engine/settle.go#L346-L371)（bounds 在 354-357）、[settle.go:317-342](../../../internal/engine/settle.go#L317-L342) |
| text 的 `obs` 行会输出非默认 `quality=`，结果会输出 `effect=` | [result_text.go:895-905](../../../internal/mcp/result_text.go#L895-L905)、[result_text.go:388-395](../../../internal/mcp/result_text.go#L388-L395)、[docs/mcp.md:293-294](../../../docs/mcp.md#L293-L294) |
| 工具描述面不止 `tools/list`：`treeTextGuide` 与 `callerGuidance` 在 `ServerInstructions()`，经 `initialize`/`server/discover` 发布 | [schema.go:31-38](../../../internal/mcp/schema.go#L31-L38)、[schema.go:104](../../../internal/mcp/schema.go#L104)、[schema.go:127](../../../internal/mcp/schema.go#L127)、[schema.go:154](../../../internal/mcp/schema.go#L154)、[server.go:63](../../../internal/mcp/server.go#L63) |
| `CatalogSnapshot()` 只覆盖 13 个 tool 的 description/schema，不含 `treeTextGuide`/`callerGuidance` | [catalog.go:26-37](../../../internal/mcp/catalog.go#L26-L37) |
| `wait_for` 描述与 `stable` 参数说明仍写“整树连续 3 帧一致” | [schema.go:277](../../../internal/mcp/schema.go#L277)、[schema.go:315](../../../internal/mcp/schema.go#L315) |
| probe 的 both 模式要求 structured 的非省略字段在 text 中出现；text-only 仍检查 quality/effect | [result_semantics.go:218-292](../../../scripts/mcp-probe/result_semantics.go#L218-L292)、[result_semantics.go:319-345](../../../scripts/mcp-probe/result_semantics.go#L319-L345)、[checks.go:1026-1050](../../../scripts/mcp-probe/checks.go#L1026-L1050)、[result.go:534-556](../../../scripts/mcp-probe/result.go#L534-L556) |

### 1.3 为什么不做完整方案

`basis`/`change`/`ignore_refs`/`effect.reason`/确认采样/局部后帧证明会引入大量新字段与
判据，远超本次所需。用户已确认最小范围：只改 `wait_for stable=true` 判据、text 投影与
工具描述面；`structured` 形状保持不变。

---

## 2. 目标与非目标

### 目标

1. 含持续几何动画或无动作实时文本的页面上，`wait_for stable=true` 能在 `timeout` 内返回
   `trigger=stable`；`stable=false` 的条件等待行为不变。
2. `text`/`both` 的文本不再出现 `quality=` 与 `effect=`；**整个工具描述面**
   （`CatalogSnapshot()` + `ServerInstructions()`）不再出现 `quality`/`effect`/
   `观察质量`/`滚动效果`。
3. `act` 的 settle 判据、`structured` 的 `quality`/`effect` 形状、O/P 生命周期、STOP、
   提交门禁全部不变；生产端解码/校验不变。
4. 最终提交推送后，回复并关闭 #33/#40/#41。

### 非目标

- 不新增 `basis`、`change`、`reason`、`anchors`、`coverage`、`stable_scope`、
  `ignore_refs`、`ignore_roles` 或确认采样。
- 不拆 `budget`；不改 `not_observed` 对 `quality=stable` 的依赖；不改 `effect` 的三态。
- 不改变 `act` 的 `quality` 语义；`act` 在动画页仍可能是 `unstable(budget)`。
- 不改变 `stable=false` 条件等待的 `quality` 判定（仍用整树摘要）。
- 不改变 `query`/`absent_query` 的检索范围（仍是完整公开树）。
- 不改变 Android App 业务逻辑、固定 `uiautomator` 制品、图片链路或树编码。

---

## 3. 路线

| 路线 | 做法 | 代价 | 结论 |
| --- | --- | --- | --- |
| R0 不动 | 保持现状 | 零 | #33 仍恒超时；工具描述仍暴露 quality/effect |
| **R1 内容稳定 + text/工具描述省略（选定）** | `wait_for stable=true` 用内容摘要；text 与工具描述面隐藏 quality/effect；structured 不变 | 判据语义变化、text/工具描述投影变化、契约版本前进 | **选定** |
| R2 完整方案 | `basis`/`change`/`ignore`/`effect.reason`/局部证明 | 契约面大、实现与验证成本高 | 否决 |

---

## 4. 判据

### 4.1 `wait_for stable=true` 的内容稳定摘要

新增 `contentDigestOf(capture, tree) ([32]byte, error)`，**只在 `req.Stable` 时使用**；
`stable=false` 的条件等待仍用原 `sampleDigestOf`。

1. 复制公开树；所有节点的 `bounds` 置空、`ref` 置空（不参与摘要）。
2. 对**无动作叶子**（`len(children)==0 && len(actions)==0`，按 `actions` 而不是 `role`）
   把 `label`/`value`/`hint` 置空；`role`/`resource`/`actions`/`axis`/五个三态/`context`/
   结构顺序全部保留。
3. `L3` 仍取前台状态/包/任务与显示宽高/旋转（复用 `screenDigest`）。
4. 摘要 = `SHA-256("wait-content" || L3 || json.Marshal(filteredTree))`；编码与
   `sampleDigestOf` 的 `json.Marshal(tree)` 同源，不引入新的 canonical 编码函数。

`waitFrame` 的 `digest` 与 `waitRun.lastDigest` 类型改为 `[32]byte`：
- `stable=true`：取 `contentDigestOf`；
- `stable=false`：取 `sha256(L1||L2||L3)`（由 `sampleDigestOf` 结果压缩，等价于现有
  三字段相等比较）；
- 摘要计算失败（`json.Marshal` 失败）按一次失败轮询处理：不增加 `samples`、`consecutive`
  清零，与现有 `sampleDigestOf` 失败路径一致；
- `terminal_reuse` / `terminal_capture` 用同一 `req.Stable` 选择摘要；`waitQualityOf` 不变。

`observation.quality` 的形状仍是 `state`/`samples`/`stop_reason`：
- `stable=true` 的 `state` 相对于内容摘要；`stable` 表示内容稳定，不表示整树逐字节相等；
- `stable=false` 的 `state`/`stop_reason` 与今天完全一致；
- `stop_reason` 的现有枚举不变（`stable`/`max_samples`/`budget`/`unreadable`/
  `not_sampled`/`condition`/`timeout`）；`samples` 的计数含义不变。

`act`/`start`/`app.*` 的 settle 仍用原 `sampleDigest`（L1+L2+L3），完全不动。

### 4.2 text 投影

`result v4` 的文本（`text` 与 `both` 的文本部分）：

- 删除 `obs` 行的 `quality` 渲染块（`result_text.go:895-905`）；`quality` 已在
  `result_text.go:972` 的子字段跳过列表，不需要 `handled["quality"]`；
- `effect` 块保留 `handled["effect"] = true`，但不输出 `effect=` 行（否则
  `result_text.go:518-524` 会走 `json v1 /effect` 兜底）；
- `quality`/`effect` 仍在 `structured` JSON 中，形状与当前完全一致；
- `both` 的 structured 部分保留这两个对象；只有文本部分省略。

`v4ResultFields` 把 `observation.quality` 与 `effect` 改为 `v4Omitted` 并补省略理由；
**同时修改 `v4ScanType`：父字段为 `v4Omitted` 时把整个子树标为 `coveredBy`**（与
`v4JSON`/`v4Block` 同），因此保留 `observation.quality.state/stop_reason/samples` 与
`effect.state` 的现有分类条目，不删除、不逐个补理由；`samples` 已有理由。
`v4OmittedResultFields` 增加 `observation.quality`/`effect` 两项；`TestResultFieldManifest`
之外增加“非默认 quality/effect 也不出现在 text”的实际渲染断言。

### 4.3 整个工具描述面

禁止面不是只有 `tools/list`：

- `CatalogSnapshot()`：13 个 tool 的 `description` 与每个 input schema 的属性 `description`；
- `ServerInstructions()`：`treeTextGuide` 与 `callerGuidance`，经 `initialize`/
  `server/discover` 发布。

两处序列化后做子串扫描，`quality`/`effect`（大小写不敏感）与中文 `观察质量`/`滚动效果`
命中即失败。工具描述可以保留 `structured` 这个模式名（例如“structured 供机器消费”），
但不得出现上述四个词，也不得引导调用方从 structured 读取 `quality`/`effect`。

`wait_for` 的 `description` 与 `stable` 参数说明必须同步改为“内容稳定（忽略 bounds 与
无动作叶子文本）连续 3 帧一致”，不能继续写“整树连续 3 帧”。

### 4.4 与 O/P、STOP、act 的关系

- 不改变 O/P 生命周期、输入授权、`actionable_until`、恢复入口、STOP 与提交门禁。
- `act` 的 settle、后观察、`target_changed`、交付帧全部不变。
- `quality`/`effect` 仍是观察/结果事实，只是不进入 text/工具描述；`structured` 仍可按
  原样读取。

---

## 5. 算法

### 5.1 `wait_for`

```text
basis = req.Stable ? content : tree
for each poll frame:
   digest = basis == content ? contentDigestOf(capture, tree)
                            : treeDigestHash(sampleDigestOf(capture, tree))
   consecutive = (digest == lastDigest) ? consecutive+1 : 1
   samples++
   lastDigest, lastFrame = digest, frame
   queryHit/absentHit/stableHit = ...
   if req.Stable && conditioned: require condition && consecutive >= 3
   if req.Stable && !conditioned: require consecutive >= 3
   if !req.Stable && condition: require condition
   ...
```

`terminal_reuse` / `terminal_capture` 的质量仍按同一 `consecutive`/`samples` 计算；只是
`req.Stable` 时比较对象换成内容摘要，条件等待仍用整树摘要。

时间轴（`MinDwell=600ms`，`stable=true`）：

```text
t=0     content A
t=900   content B     → consecutive=1
t=1800  content B     → consecutive=2
t=2700  content B     → consecutive=3，trigger=stable
```

### 5.2 text

```text
renderObservationV4:
  删除 quality 渲染块（result_text.go:895-905）；quality 已在 972 的子字段跳过列表
renderResultV4:
  if effect present: handled["effect"] = true; 不输出 effect=
```

`structured` 渲染不做任何改动；`both` 只影响文本部分。

---

## 6. 实现方案

### 6.1 文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/wait_content.go`（新增） | `contentDigestOf(capture, tree) ([32]byte, error)`：`Node.Clone` 后清空 `bounds`/`ref`、清空无动作叶子的文本、`json.Marshal`、拼接 L3、SHA-256；失败返回 error |
| `internal/engine/wait_for.go` | `waitFrame.digest`/`waitRun.lastDigest` 改为 `[32]byte`；`sampleWaitFrame`/`absorbWaitTerminalFrame` 按 `req.Stable` 选摘要；`terminal_reuse` 同；`waitQualityOf` 不变；`stable=false` 仍用 `sampleDigestOf` 压缩 |
| `internal/mcp/result_text.go` | 删除 quality 渲染块（895-905）；`effect` 置 `handled["effect"]=true` 不输出行；`v4ResultFields` 的 `observation.quality`/`effect` 改 `v4Omitted` 并补理由；子字段条目保留 |
| `internal/mcp/result_manifest_test.go` | `v4ScanType` 让 `v4Omitted` 父覆盖整个子树；`v4OmittedResultFields` 补 `observation.quality`/`effect` 理由；新增非默认 quality/effect 不出现于 text 的渲染断言 |
| `internal/mcp/schema.go` | 删除全部 13 个 tool 的 description、input schema 属性说明、`treeTextGuide`、`callerGuidance` 里的 `quality`/`effect`/`观察质量`/`滚动效果`；`wait_for` 描述与 `stable` 参数说明改为内容稳定 |
| `internal/mcp/schema_test.go`、`catalog_test.go`、`server_test.go` | 全量扫描 `CatalogSnapshot()` + `ServerInstructions()`；替换 96/99/105/306、catalog_test.go:38、server_test.go:560/563 的旧正向断言；断言 `wait_for` 描述不再写“整树” |
| `scripts/mcp-probe` | `text` 不检查 quality/effect；`both` 文本省略、structured 原样，`compareResultTextToStructured`/`normalizeObservationQuality`/`checkQualityTextLine`/`checkScrollEffect` 按此改写；反向对照改为“出现即失败” |
| `scripts/mcp-probe` 实机 | `tools/list` + `initialize`/`server/discover` instructions 全量扫描 |
| `docs/` | 见 6.4 |
| `README.md`、`release/versions.json` | 手改 `versions.json` 的 `contract_version` 22→23；运行 `scripts/generate-version.ps1` 生成 Go/Java/XML 常量；不改脚本 |

### 6.2 取值

- 不新增请求/策略字段。
- 无动作叶子的判定固定为 `len(children)==0 && len(actions)==0`；按 `actions` 而非 `role`。
- `contentDigest` 只在 `req.Stable` 时计算；`stable=false` 不计算，行为不变。
- 契约版本 22→23 是默认行为变化所要求的前进；不前进属于显式偏离，需要用户确认并记录
  兼容风险（`docs/release.md:51-52`）。
- 发行号按 `release/versions.json` 实施时核对，不预占；README 的 release 示例同步。

### 6.3 日志

不新增日志键；`phase=wait_for` 已能定位路径。不记录树、差异正文、ref、坐标、标签、截图
或异常字符串。

### 6.4 文档

按 `file:line` 同步：

- `docs/mcp.md:122-125`（wait 文本省略默认质量/整树摘要）、`:186-190`（text 输出非默认
  quality）、`:264`（effect 标量）、`:293-294`（quality 省略表）、`:301-305`（effect
  100% 保留）、`:380`/`:389-390`（obs 语法）；工具描述面删除四个词。
- `docs/mcp.md:199-202` 的 structured 错误 `diagnostic v1` 仍含 `effect.state`：该路径
  保持不变，不在本次删除范围。
- `docs/architecture.md:256`、`:263`（wait 质量/3 帧门禁）。
- `docs/integration.md:403`（`stable=true` 要求整树）。
- `docs/interface.md` §14（wait 判据与 quality 语义）；§11:983-984 补一句：`wait_for
  stable=true` 的相等基准是内容摘要，`stable=false` 的条件等待仍用整树摘要。
- `docs/client.md:22`、`README.md:10-11`（写死 `"22"`/release 0.26.0）；契约前进后同步。
- 所有出现 `contract_version=22` 的正式文档同步到 23（interface/control-protocol/
  runbook/release/phone-operations/architecture/integration/client/README）。
- 涉及 `docs/` 的提交先由独立、只读的文档审计 subagent 复核（AGENTS.md 第 15 条）。

### 6.5 不变式

| 不变式 | 影响 |
| --- | --- |
| 唯一权威 / 状态锁 / STOP / O-P / 提交门禁 | 不变 |
| `act` settle ⊇ 重验 | 不变：`act` 仍用 L1+L2+L3 |
| 事实分层 | `quality`/`effect` 仍是事实，只是 text/工具描述不投影 |
| 三态与省略 | `structured` 形状不变；text 的 `quality`/`effect` 按 `v4Omitted` 声明 |
| 日志白名单 | 不新增键 |

---

## 7. 效果与边界

### 7.1 能治

- 旋转封面、波形、进度条等几何动画：`wait_for stable=true` 不再被 bounds 拖死。
- 播放进度、倒计时等无动作叶子文本：其变化不再参与 `stable=true` 的内容摘要。
- text 与工具描述不再出现 `quality`/`effect`/`观察质量`/`滚动效果`；structured 仍可按
  原样读取（只给机器）。

### 7.2 不能治

| 现象 | 原因 |
| --- | --- |
| 有动作节点自身的持续文本（label 直接在该可点击节点上） | 有动作，仍参与摘要 |
| 按钮/加载提示等无动作 text 子叶子 | 其文本被忽略；需要文本语义时用 `query`/`absent_query` |
| `role=button`/`checkbox`/`input` 但 `actions` 为空的禁用/无能力控件 | 按 `actions` 判定，其文本也会被忽略；不要用 role 推断可交互性 |
| 无动作但有 children 的容器文本 | 不是叶子，文本仍参与摘要，可能继续拖死 wait |
| 节点出现/消失（`Visible` 切换） | 节点集合变化仍改变摘要 |
| wait 返回后目标自身仍在移动 | 内容稳定不保证 `bounds` 不变；下一次 ref 动作可能 `target_changed`，按现有交付新帧重试 |
| `act` 在动画页的 `quality` 仍非 stable | `act` 的 settle 判据未变；本轮明确不做 |
| `#40` 的结构化 `effect=unknown` 仍无原因 | 本轮不加 `effect.reason`；text/工具描述已不再显示 |
| `#41` 的 `unknown(budget)`/`unstable(budget)` 仍共用原因 | 本轮不拆 `budget`；text/工具描述已不再显示 |
| `not_observed` 仍要求 `quality=stable` | 本轮不改该依赖 |
| #40 的 5 次 `effect=unknown` 的锚点问题 | 本轮不改正证据策略（来源见 #40） |

### 7.3 成本

- 每次 `stable=true` 的 wait 采样多一次 O(n) 的树复制与 JSON 哈希；`stable=false` 只多
  一次常数级压缩哈希（96 字节），不复制树、不增加设备读取。
- text 结果少 quality/effect；structured 字节不变。
- 工具描述面字符串更短；`tools/list`/instructions 扫描只在测试/探针。
- `stable=true` 在动画页通常更早命中，读取次数减少。

---

## 8. 验证

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 动画页：旋转封面 bounds 抖动 + 无动作进度文本变化，按钮稳定 | `stable=true` 内容摘要 3 帧相等并命中；对照旧 `sampleDigest` 超时/不命中（反向） |
| 有动作节点自身的 label 变化 | 内容摘要仍不相等，wait 不命中（边界） |
| 禁用按钮（`role=button`、`actions=[]`）文本变化 | 按无动作叶子忽略；与 role 无关 |
| 无动作但有 children 的容器文本变化 | 仍参与摘要（边界） |
| `stable=false` 条件等待 | 仍用整树摘要；`quality` 的 state/samples/stop_reason 与今天一致 |
| `act` 回归 | `act` 的 settle 仍用原三层摘要；质量/后观察不变 |
| text 投影 | `obs` 行无 `quality=`；结果无 `effect=`；structured 仍有；`both` 只文本省略 |
| 工具描述面 | `CatalogSnapshot()` + `ServerInstructions()` 序列化后不含 `quality`/`effect`/`观察质量`/`滚动效果`；`wait_for` 描述写“内容稳定”而非“整树” |
| v4 字段表 | `observation.quality`/`effect` 声明为 `v4Omitted`；`v4ScanType` 让 `v4Omitted` 父覆盖子树，子字段条目保留；非默认值也不出现在 text |
| probe | text 不检查 quality/effect；both 文本省略、structured 原样；structured 仍校验原对象 |
| 反向对照 | 去掉内容摘要回到整树摘要，动画页用例必须失败；去掉 text 过滤，text 用例必须失败 |

### 8.2 实机

- 网易云播放页：`start` → `wait_for(stable=true, timeout=10)` ×3；期望 ≤5s 内
  `trigger=stable`，text 结果无 quality/effect。
- 哔哩哔哩/知乎：`scroll`；text 无 `effect=`；用 `both` 验证同一响应的文本省略与
  structured 保留，或另发一次 `structured` 调用（不同 P/O）。
- `act` 质量：记录 structured 的 `unstable(budget)` 仍存在，确认本轮未改 act。
- 工具目录：主机 stdio 与手机 HTTP MCP 的 `tools/list` 与 `initialize`/`server/discover`
  instructions 全量扫描，不出现四个词；structured 结果仍可解码原对象。
- 契约版本按最终决定（必须 23；以 `release/versions.json` 与清单为准）。

### 8.3 记录与 issue

- 验证记录：`maintenance/verification/wait-content-stability-and-text-omit.md`，注明代码
  提交、构建、设备/显示、实际命令、真实结果、**未执行项与原因**；分开“已实现/已单测/
  已实测”。
- **最终提交并推送后**：
  - #33：`gh issue comment 33` + `gh issue close 33 --reason completed`；回复写 wait 内容
    稳定、text/工具描述省略、structured 不变、提交 hash/验证记录。
  - #40/#41：建议 `--reason "not planned"`；回复写明 text/工具描述面已修复，structured 的
    `effect.reason`、`budget` 拆分、`not_observed` 依赖、确认采样、ignore 参数本轮未做，
    以及提交 hash/验证记录。若用户要求 `completed`，回复必须明确“部分完成”。
  - 用 `gh issue view <n> --json state,comments` 核对状态与回复正文。

### 8.4 发布交付（AGENTS.md 第 19 条）

- 核验主机清单后，用本次产物做手机整组覆盖部署：含手机 MCP，保留 App 私有数据；不卸载、
  不清库。
- 用**同一源码提交**构建并替换 Windows 主机的 `build/device-mcp.exe` 与
  `build/mcp-probe.exe`。
- 用本次新产物分别运行独立探针，验证主机 stdio 与手机 HTTP MCP；依赖的 Agent、其托管
  MCP 进程与重连由用户处理。
- 验证记录区分磁盘替换、独立进程验证与依赖方切换；未执行项写原因。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| `wait_for` 判据 | `stable=true` 内容稳定；`stable=false` 仍整树；无参数 |
| 无动作叶子 | `len(children)==0 && len(actions)==0`；按 actions，不按 role |
| contentDigest | 只在 `req.Stable` 计算；`stable=false` 不计算 |
| text/both | 文本省略 `quality`/`effect`；structured 保留 |
| 工具描述面 | `CatalogSnapshot()` + `ServerInstructions()` 全量不出现四个词；`structured` 模式名可保留 |
| `act` | settle 与 quality 完全不变 |
| 日志 | 不新增键 |
| 本轮不做 | `basis`/`change`/`reason`/`budget` 拆分/`not_observed` 依赖/确认采样/ignore 参数 |
| 契约版本 | 默认行为变化，必须 22→23 |
| issue | push 后回复并关闭；#33 completed，#40/#41 建议 not planned |

### 9.2 未决

| 事项 | 谁来定 | 什么时候定 |
| --- | --- | --- |
| 契约版本是否保持 22 | 用户 | 确认时；若保持 22，属于显式偏离，需记录旧契约调用方静默接受新语义的兼容风险 |
| #40/#41 的关闭 reason 与回复措辞 | 用户/实现 agent | push 后、关闭前；回复列全未做项与工具描述变更 |
| 发行号/Android versionCode 的具体值 | 实现 agent | 实施发布时按 `release/versions.json` 核对 |

---

## 10. 实施顺序

1. `wait_content.go` + `wait_for.go` 内容摘要；`stable=false` 回归；单测与反向对照。
2. `result_text.go` text 过滤、v4 字段表；MCP 渲染测试。
3. `schema.go` 工具描述面清理（13 tool + `treeTextGuide`/`callerGuidance`）；`CatalogSnapshot()`
   + `ServerInstructions()` 全量扫描；`wait_for` 描述改内容稳定。
4. probe 的 text/both/structured 与反向对照。
5. `docs/` 行级同步；文档审计。
6. 契约版本/整组构建/清单核验/手机部署/主机 exe/双入口探针/实机验证；未执行项记录。
7. 验证记录；`git push origin master`。
8. `gh issue comment` + `gh issue close` #33/#40/#41；`--json state,comments` 核对。
9. 提案归档（§11）。

---

## 11. 实现结果

### 11.1 已落地

| 提案条目 | 实现与证据 |
| --- | --- |
| 内容稳定判据与终帧 | [wait_content.go](../../../internal/engine/wait_content.go) 的 `waitDigestOf`、`contentDigestOf` 与 [wait_for.go](../../../internal/engine/wait_for.go) 的轮询及终帧比较。网易云播放进度变化期间 3 次稳定等待通过，每次 1.66–1.76 秒 |
| 条件等待与动作回归 | `stable=false` 条件等待仍用原三层摘要。[wait_content_test.go](../../../internal/engine/wait_content_test.go) 覆盖含条件的门禁、采样复用和终帧。B 站 `act(scroll)` 的结构化质量仍为 `unstable/2/budget` |
| MCP 投影 | [result_text.go](../../../internal/mcp/result_text.go) 的省略清单、`encodeResultText`、`renderObservationV4`；B 站同一 `both` 响应的文本省略两个整对象，结构化 `effect.state=observed`，采样质量保留 |
| 工具描述与独立探针 | [schema.go](../../../internal/mcp/schema.go) 的工具表与 `ServerInstructions`，探针 [catalog.go](../../../scripts/mcp-probe/catalog.go)、[checks.go](../../../scripts/mcp-probe/checks.go) 与 [result_v4.go](../../../scripts/mcp-probe/result_v4.go)。主机 stdio 和手机 HTTP 均通过 `tools/list`、`server/discover`、`initialize` |
| 发布与文档 | [版本源](../../../release/versions.json) 发行 0.27.0、契约 23、Android versionCode 29。独立正式文档审计复检 CLEAN。手机完整包覆盖部署并保留私有数据，同提交构建的两个 Windows exe 已替换并核验 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 实现审查提出所有终帧摘要错误均不计样 | 内容摘要路径采纳，摘要失败清零且不增加采样。原整树摘要路径保留既有行为，维持提案对 `stable=false` 的兼容要求。处理理由已回复审查者 |
| 知乎单独复测 | 未执行。提案允许 B 站或知乎，B 站已完成同一响应双投影闭环 |
| 外部宿主管理的 MCP 进程切换 | 未执行。按交付边界由依赖方重启并重新发现工具 |
| Issue 原建议中的 delta 动画几何变化省略、细分结构化原因与自定义过滤 | 未执行。按已确认范围保留 delta、现有结构化字段、`budget` 分支与 `not_observed` 证据门槛 |

---

## 附：本提案不改变的事项

- `act` 的 settle、实时重验、`target_changed`、交付帧；
- `structured` 的 `quality`/`effect` 字段形状与枚举；生产端解码/校验；
- `stable=false` 条件等待的整树质量判定；
- `budget`/`not_observed`/`effect` 原因、O/P、STOP、恢复入口；
- 公开树、delta、compact、ref 保号、图片链路、Android App 与固定制品；
- 日志白名单的隐私边界。
