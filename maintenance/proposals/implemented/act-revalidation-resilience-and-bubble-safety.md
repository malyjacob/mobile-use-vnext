# 提案：增强动作实时重验抗外围扰动能力并保持 bubble 与意图安全边界

状态：提案，未实现。
基线提交：`43767ab597ce189d605e6be705ed40a81386b978`（分支 `master`，`2026-11-27`）。
适用范围：`act`（ref `tap`/`long_press`/`text` 及其 `coextensive_child`/`bubble_ancestor`/`bubble_colocated` 路径、`scroll`、坐标 `tap`/`long_press`）认领后的实时重验阶段与拒绝观察交付；不改变静态准入、raw `swipe`、`key`、`text` 写后提交机制或 `settle` 采样。
影响范围：`internal/engine/{act.go,act_revalidation_policy.go,act_target_shared.go,point.go,scroll.go}`、`internal/eventlog/log.go`（注释同步）、`release/versions.json`（发版版本源）及生成常量、`docs/{architecture.md,interface.md}`（及必要时 `docs/integration.md`）、对应单元测试与验证记录。
契约（`/api/*` 字段与 `contract_version`）：不新增或修改 `/api/*` 请求/响应字段、错误码、`error.details.reason` 枚举或 MCP 13 工具 schema；由于动作重验行为契约变化，发版时按 `release/versions.json` 单一版本源同步递增共同 `contract_version`（`32 → 33`）、`release_version`（`0.36.0 → 0.37.0`）与 `android_version_code`（`38 → 39`），并通过脚本重新生成 Go/Java/XML 常量与发布清单。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：`internal/android/hierarchy.go`、`internal/engine/{act.go,act_revalidation_policy.go,act_target_shared.go,point.go,scroll.go,delivery.go,text_submit.go}`、`internal/eventlog/log.go`）。

---

## 0. 摘要

当前 `act` 在认领观察 `O` 后执行实时重验时，目标节点自身与操作意图未变，却常因外围显示窗口闪现、容器内次要数字/装饰变动、顶部横幅导致的目标平移或渲染瞬态未稳而返回 `409 target_changed`，迫使调用方用交付的新 `O` 重试。本提案选定**路线 C（系统性外围解耦 + 有界瞬态单次复检）**，在不放松目标自身业务语义与交互归属、不迁移 `O/P`/`STOP` 权威且严守 `bubble` 双绑定防线的前提下，通过 6 项正交改进显著降低误杀率：

1. 在动作重验的祖先链构造中剥离多窗口 `displayRootNode` 合成显示根；
2. 仅在 Tier 3 与 Tier 5 的 `L2` 祖先链比较中对非目标祖先忽略 `Text`/`Description`/`Checked` 噪点（保持 `L1` 与坐标 Tier A 严格不变）；
3. 为 Tier 4 `E3` 与直点/`coextensive_child` 子载体安全解绑引入“纯数字骨架守恒”判定；
4. 新增受限的 Tier 5（同祖先链 + 同水平带平移找回），并统一将 `tier >= 4` 纳入全树遮挡与 `bubble` 同步防线；
5. 为坐标 `tap`/`long_press` 增加原地最近交互拥有者守恒兜底（不改坐标）；
6. 在 `Act` 重验阶段增加至多 1 次 `150ms` 可中断瞬态短复检，第 2 帧仍失败时交付第 2 帧。

本提案明确**不**放宽目标自身非数字文案/能力冲突、**不**以全局唯一焦点或最大重叠率猜测多候选目标、**不**改动 `settle` 采样或 `text` 写后提交逻辑。

---

## 1. 问题

### 1.1 现象与代价

调用方拿到 `ready` 观察 `O` 并发起 `act` 后，即便目标元素仍在屏幕上且业务语义与能力未变，重验阶段仍高频因外围扰动返回 `HTTP 409`、`outcome=not_executed`、`error.code=target_changed`：

- 即使服务通过 [`deliverRejectedObservation`../../../internal/engine/delivery.go#L25-L74) 随错交付了新 `O`，调用方仍被迫多做一次决策与工具往返，消耗额外模型推理耗时与延迟；
- 每次重试都要重新走一轮 `captureForActRevalidation`（含 `ProbeDisplay` 与 `Capture` 两次设备读取）、公开树预览投影与规划逻辑，若重验失败原因本属短动画瞬态（`150ms` 内即可稳定），一次即拒导致无效失败。

### 1.2 证据链

按实际调用路径逐条核对当前源码：

1. **多窗口合成显示根导致全树祖先链抖动**：[`connectDisplayWindows`../../../internal/android/hierarchy.go#L268-L288) 在仅 1 个顶层窗口时保持该窗口为根；一旦屏幕上出现第 2 个顶层窗口（输入法、Toast、悬浮窗、系统弹窗），就会追加一个由 [`displayRootNode`../../../internal/android/hierarchy.go#L255-L262) 构造的合成节点作为 `Parent=-1` 的根。[`lineageL1`../../../internal/engine/act_revalidation_policy.go#L272-L311) 与 [`lineageL2`../../../internal/engine/act_revalidation_policy.go#L343-L368) 直接遍历至 `parent == -1`，[`sameLineageKeysBudgeted`../../../internal/engine/act_revalidation_policy.go#L634-L648) 首先比较 `len(a) != len(b)`，导致外窗闪现时全树节点 Tier 1–3 全部因祖先链长度差 1 失配，全部被迫跌入高门槛的 Tier 4。
2. **直点 / `coextensive_child` 的 `carrier` 绑死与解绑门禁过窄**：在 [`resolveDirectTapScopeFrame`../../../internal/engine/act.go#L1451-L1549)（直点与 [`revalidateCoextensiveChildFrame`../../../internal/engine/act.go#L1109-L1111) 共用）中，当静态规划记录了内部非交互子载体 `plan.carrierRaw != nil` 而新鲜帧里该子载体未匹配（`fixedCarrier == nil`）时，[`act.go:1495`../../../internal/engine/act.go#L1495) 与 [`act.go:1525`../../../internal/engine/act.go#L1525) 仅在 `match.independentContent` 为真时允许保留原点或重选安全点。而 [`independentTargetContent`../../../internal/engine/act_revalidation_policy.go#L744-L757) 只承认目标自身非空 `Text`/`Description` 或全树双端唯一 `ResourceID`。常见列表卡片容器自身无文案且复用列表项 `ResourceID`，即使容器本身已被 Tier 1–3 双向唯一证明、且只是内部角标/时间副文本刷新，也会直接返回 `scope_blocked`。
3. **同祖先链纯平移跌出 Tier 1–3 且无法进入 Tier 4**：[`edgeAtTier`../../../internal/engine/act_revalidation_policy.go#L679-L696) 在 Tier 1 要求精确 `Bounds`，Tier 2/3 要求 `overlapRatio >= 0.50` / `0.60`；不满足时提前返回 `matched=false, lineageMismatch=false`。[`matchActTargetWithOptions`../../../internal/engine/act_revalidation_policy.go#L868-L908) 仅在 `sawLineageMismatch == true` 时才进入 Tier 4（[`act_revalidation_policy.go:901`../../../internal/engine/act_revalidation_policy.go#L901)）。因此，页面上方横幅/提示条伸缩或列表头部插入元素导致目标纯平移超过自身高度 `50%` 时，即便祖先链 L1 完全一致且全树唯一，也会在 Tier 1–3 直接以 `target_missing` 结束。对比来看，`text` 的 `submit_ref` 已有 [`sameHorizontalBand`../../../internal/engine/act.go#L2182-L2191) 与 [`matchKeyboardShiftedSubmitNode`../../../internal/engine/act_revalidation_policy.go#L1347-L1393) 支持纵向位移找回，但普通 `tap`/`long_press`/`text`/`scroll` 均未具备该路径。
4. **祖先链 L2 对非核心祖先属性过脆，且点击 Tier 4 E3 不容忍纯数字变动**：[`structureKeyOf`../../../internal/engine/act_revalidation_policy.go#L89-L103) 将 `Text`、`Description`、`Checked` 全部纳入键比较，[`lineageL2`../../../internal/engine/act_revalidation_policy.go#L343-L368) 虽移除 [`transparentWrapper`../../../internal/engine/act_revalidation_policy.go#L316-L339)，但对保留下来的非目标祖先仍要求完整 `structureKey` 相等，父容器标题计数或选中态变化即判 `lineage_changed`。同时，[`subtreeEvidence`../../../internal/engine/act_revalidation_policy.go#L1201-L1232) 在 `actMatchTapLike` 模式下调用 [`sameReadableMultiset`../../../internal/engine/act_revalidation_policy.go#L1153-L1164)，要求两侧子树可读文案排序后 100% 相等，卡片中“2分钟前 → 3分钟前”或角标数字跳变即破坏 E3。
5. **坐标动作在点内非交互装饰层变化时一刀切**：[`provePointTarget`../../../internal/engine/act.go#L1597-L1606) 要求新鲜帧在 `(x, y)` 上的最深命中集合 `len(freshNodes) == len(observedIndices)`，且 [`proveTargetSet`../../../internal/engine/point.go#L590-L623) 对每个命中节点逐一按自身 `sameStructure` 与几何配对。哪怕交互拥有者 [`nearestPointInteractiveOwner`../../../internal/engine/point.go#L237-L260) 完全没变、仅内部装饰角标/提示层增减改变最深深度，也会立即返回 `point_changed`。
6. **单帧瞬态未稳即直接拒绝**：[`Engine.Act`../../../internal/engine/act.go#L104-L142) 在 `revalidate` 阶段只执行一次抓取与重验；相比之下，[`dispatchTextSubmitTap`../../../internal/engine/text_submit.go#L558-L600) 已采用 [`textSubmitMaxCaptures`../../../internal/engine/text_submit.go#L191)` = 2`、[`textSubmitRetryDelay`../../../internal/engine/text_submit.go#L188)` = 150ms` 与 [`pauseInterruptible`../../../internal/engine/settle.go#L283) 吸收动画瞬态。
7. **`bubble` 双绑定与全树遮挡校验的联动边界**：[`revalidateBubbleTapFrame`../../../internal/engine/act.go#L833-L979)、[`revalidateCoextensiveChildFrame`../../../internal/engine/act.go#L983-L1112) 与 [`revalidateColocatedTapFrame`../../../internal/engine/act.go#L1116-L1292) 分别负责三种双绑定重验；全树遮挡函数 [`tapFrameTier4OcclusionProven`../../../internal/engine/act_target_shared.go#L238-L255) 目前在 [`act.go:964`../../../internal/engine/act.go#L964)、[`act.go:1266`../../../internal/engine/act.go#L1266)、[`act.go:1284`../../../internal/engine/act.go#L1284) 与 [`directFrameOcclusionProven`../../../internal/engine/act.go#L1448) 中硬编码判断 `tier == 4`（或 `tier != 4`），若后续引入 Tier 5 而不同步升级判断与 `bubble_colocated` 同步位移防线，将出现遮挡漏检或双节点错位。

### 1.3 为什么直觉会出错

1. **为什么不能无条件删掉 `independentContent` 的 `carrier` 解绑门禁？**
   若一个无自身文案/唯一 ID 的可点击外框内仅有一个子节点 `TextView("确认")`，静态规划把 `carrier` 绑在该子节点上；当原地文案变成 `"取消"` 时，外框自身仍可通过 Tier 1！若无条件解绑失效 `carrier`，重验会忽略子节点业务语义翻转并直接点击 `"取消"`，违背调用方意图。
2. **为什么点击 E3 不能直接照搬 `scroll` 的 `sharesReadableText`（共享 ≥1 条文案即通过）？**
   列表卡片 A（`["商品A", "加入购物车"]`）滑走后，卡片 B（`["商品B", "加入购物车"]`）若占到原区域，两者仍共享 `"加入购物车"`。对 `scroll` 而言容器是外层列表、允许内部条目流动；对 `tap` 而言，“共享 1 条动作按钮文案”会导致跨卡片串台误点。
3. **为什么不能为了过 L2 放宽 `transparentWrapper` 对缺失 `Has*` presence 的要求？**
   规则第 7 条与先前回溯分析已确立：`Has* = false` 表示来源未报该能力而非已知安全；把未知存在位归一为安全布尔值会让真实交互边界被吞并，破坏安全不变量。
4. **为什么 Tier 5 平移找回不能只放宽几何而忽略遮挡与双绑定一致性？**
   一旦节点跨出了原位置，原本停留在新位置的浮层/底部栏/兄弟交互控件可能刚好覆盖新矩形。若不把 `tier >= 4` 全面纳入全树最深命中与外部竞争拥有者校验，且不对 `bubble_colocated` 校对子节点与共位节点的位移一致性，平移后可能直接点中遮挡层或错位节点。

---

## 2. 目标与非目标

### 目标

1. 在保证调用方点击/滚动/输入对象业务意图不跑偏的前提下，消除由合成显示根抖动、非目标祖先非核心属性变化、容器内纯数字/装饰层刷新、同祖先链纵向平移、以及 150ms 内瞬态动画导致的 `target_changed` 误杀。
2. 保持与 `bubble_ancestor`、`bubble_colocated`、`coextensive_child` 的全部关系守卫与全树遮挡不变量兼容，不引入跨模式漂移、不触发双节点失步或遮挡穿透。
3. 保持 `O` 单向消费、`P` 单次派发、`STOP` 零等待撤权、日志白名单及既有错误码契约不变。

### 非目标

1. **不放宽目标自身结构硬门槛**：目标自身非数字 `Text`/`Description`、`Package`、`ClassName`、`Editable`、`Password`、`Checked` 与能力三态（除 `submit_ref` 既有的 disabled→enabled 外）变化仍必须拒绝。
2. **不引入多候选猜测**：任何 Tier 出现 ≥2 个候选立即报 `target_ambiguous`；不按坐标最近、遍历顺序或最大重叠率决胜。
3. **不改变坐标授权的原点语义**：坐标 `tap`/`long_press`/`text` 永不重定向坐标；raw `swipe` 永不放宽至拥有者兜底。
4. **不修改 `settle` 采样、静态准入规划、`text` 写后提交逻辑（`text_submit.go`）与 `observation` 连续性算法**。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持当前 Tier 1–4、严格 L1/L2、严格 E3、严格坐标集合与单帧重验 | 无代码/契约成本 | 外窗闪现、角标/时间跳变、横幅平移、装饰层微调与短动画持续触发 `target_changed`，调用方频繁无效重试 |
| R2 路线 A（精准去噪） | 仅做合成显示根剥离 + `carrier` 安全解绑 + Tier 5 平移找回 | 改动最小；但对父容器文案变化、卡片数字微变、坐标装饰微变与渲染瞬态仍无解 | 若父容器标题带计数或卡片含倒计时，仍需多次重抓 `O` |
| R3 路线 B（系统性外围解耦） | 在 R2 基础上增加 Tier 3 祖先去噪、E3 数字骨架守恒与坐标拥有者守恒 | 覆盖结构与非业务文案噪点，但不处理动画/异步刷新刚好卡在单次抓取帧上的瞬态 | 列表滚动刚停稳后一帧、弹框刚退场前 100ms 抓取到旧边界时仍一次即拒 |
| R4 路线 C（选定方案） | 在 R3 基础上增加重验阶段至多 1 次 `150ms` 可中断瞬态短复检 | 首次命中无额外耗时；仅对可恢复 `target_changed` 最多增加 `150ms` + 1 次抓帧成本 | 若某 App 动画耗时 >150ms 且仍未稳定，第 2 帧依然拒绝并交付第 2 帧，不无限等待 |

**选择理由**：用户已确认采用较彻底的 R4（路线 C），并重点关注 `bubble` 联动安全。R4 将静态结构抗噪（部件 1–5）与时间域短瞬态抗抖动（部件 6）结合，且第 1 帧通过时零额外开销，最能以最小公开面变化达到降低重验误杀的效果。

---

## 4. 判据

以下逐项给出可判定的机器规约与安全性边界：

### 4.1 合成显示根归一化（部件 1）
- **定义** `isSyntheticDisplayRoot(raw *device.RawNode) bool`：当且仅当 `raw != nil && raw.Parent == -1` 且 `raw.Package == ""` 且 `raw.ClassName == ""` 且 `!raw.TextSet && !raw.DescSet && !raw.ResourceSet` 且 `!raw.Editable` 且 `raw.HasEnabled && raw.Enabled` 且 `HasClickable/HasLongClick/HasScrollable/HasChecked/HasSelected/HasFocused/HasCheckable/HasPassword` 全部为 `false`。
- **规则**：`lineageL1` 沿 `Parent` 上溯时，若祖先满足 `isSyntheticDisplayRoot`，则跳过该层并停止（不把该合成节点加入 `lineage` 切片），并在 `len(lineage) > 0` 时将该切片最后一个实际窗口根节点的 `root` 标记置为 `true`。若目标本身即是合成显示根或直接挂在合成显示根下的顶层窗口节点，则返回空链（`len(lineage) == 0`）并由调用方按正常校验处理。
- **范围边界**：仅用于 `act_revalidation_policy.go`、`act.go`（`sameRawLineageKeys`）、`act_target_shared.go`（`changedPublicAncestorRef`）；不修改 `identity.go` / `settle.go` 的 `rawIdentity`。

### 4.2 Tier 3 / Tier 5 祖先链 L2 去噪（部件 2）
- **进判据字段**（对非目标的 L2 祖先构造 `ancestorStructureKey`）：`Package`、`Class`、`Resource`（`optionalString` 含 `set` 与 `value`）、`Editable`、`Clickable`、`LongClickable`、`Scrollable`、`Enabled`、`Password`、`Checkable`。
- **不进 L2 祖先判据字段**：`Text`、`Description`、`Checked`（以及既有的 `Bounds`、`Focused`、`Selected`）。
- **强锚点收紧**：由于 `ancestorStructureKey` 忽略了 `Text` 与 `Description`，Tier 3 与 Tier 5 的非根强锚点校验（`hasStrongNonRootAnchorBudgeted`）仅采信 `Resource`、`Clickable`、`LongClickable`、`Scrollable`、`Editable`、`Password`、`Checkable`，不得把被忽略的 `Text`/`Description` 当作强锚点。
- **严格隔离**：
  - Tier 1 与 Tier 2 仍使用完整 `L1`（不忽略祖先 `Text`/`Description`/`Checked`）；
  - 保留原有严格 `lineageL2` 给 `sameRawLineageKeys`（[`act.go:733-760`../../../internal/engine/act.go#L733-L760)，供 [`point.go:505`../../../internal/engine/point.go#L505) 与 [`act.go:796`../../../internal/engine/act.go#L796) 调用），Tier 3 与 Tier 5 专用去噪版 `lineageL2ForAct`；
  - `transparentWrapper` 仍在原始节点上按三态严格判定，缺失 `Has*` 或含 `Text`/`Description` 仍不透明；
  - 目标节点自身、以及 `bubble_ancestor`/`bubble_colocated`/`coextensive_child` 中任何被匹配节点自身仍强制完整 `sameStructureForAct`；
  - `bubble_ancestor` 仍校验 `nearestInteractiveAncestor(view, childPublic) == ancestorPublic` 且 `childPublic` 的 `Role`、`Label` 不变且仍非交互。

### 4.3 纯数字骨架守恒与 `carrier` 安全解绑（部件 3）
- **纯数字/动态数值 token 判定** `isDynamicNumericToken(s string) bool`：
  - `s` 非空，且包含至少一个 ASCII 数字 `'0'..'9'`；
  - `s` 中每个 rune 仅能为 ASCII 数字 `'0'..'9'` 或有限分隔/计量符集合 `{'.', ',', ':', '+', '-', '/', '%', ' '}`（排除字母与汉字，避免把 `"A1"/"A2"` 误判为纯数字）。
- **数字骨架规范化** `numericSkeleton(s string) string`：将 `s` 中每段连续 ASCII 数字 `[0-9]+` 折叠为单个 `'#'`（例如 `"2分钟前"` → `"#分钟前"`，`"第12/30页"` → `"第#/#页"`）。
- **子树骨架匹配** `subtreeSkeletonMatch(oldTexts, newTexts []string) bool`：
  1. 过滤掉两侧满足 `isDynamicNumericToken` 的条目（容忍纯数字角标/计数增删）；
  2. 对保留的非空条目按 `numericSkeleton` 映射后分别排序，要求两侧切片非空、长度相等且逐项精确相等；
  3. **强非数字锚点硬条件**：在两侧原始条目中，至少存在 1 条不含任何数字（Unicode 数字判定 `unicode.IsDigit` 为假）的非空可读文案在旧帧与新帧中均出现（精确相等）。该条彻底阻止 `"选项1"→"选项2"` 或 `"3月1日"→"3月2日"` 仅靠纯数字骨架碰撞通过。
- **适用位置**：
  - `subtreeEvidence`（`actMatchTapLike`）：先校验 `sameReadableMultiset`；未过时再校验 `subtreeSkeletonMatch`（共用同一 `readableSubtree` 64 节点上限与 `evidenceBudget`）。`actMatchScroll` 保持既有 `sharesReadableText` / 资源覆盖规则不变。
  - 直点 / `coextensive_child` 的 `carrier` 解绑（`resolveDirectTapScopeFrame`）：当 `plan.carrierRaw != nil && fixedCarrier == nil` 时，放行条件从 `match.independentContent` 扩展为 `match.independentContent || match.independentSubtree`（即在目标自身无独立内容时，只要目标子树在当前匹配中满足精确多集相等或 `subtreeSkeletonMatch` 即可解绑失效子载体并在同一 `target` 子树安全区内保留原点或重算安全点）。

### 4.4 Tier 5 同祖先链 + 同水平带平移找回（部件 4）
- **零值策略安全**：`actMatchOptions` 保持“零值即普通动作完整默认策略”（与 [`act.go:534, 552, 1301`../../../internal/engine/act.go#L534) 的 `tapFrameOptions{}` 构造约定一致），因此新增反向开关 **`disableTranslation bool`**（零值为 `false` 即默认启用 Tier 5），并提供 `.withoutTranslation()` 专供部件 5 坐标拥有者兜底禁用 Tier 5。
- **准入条件**：仅当 `matchActTargetWithOptions` 中 **Tier 1、Tier 2、Tier 3 与 Tier 4（若触发）全部返回 `actProbeNone`** 且 `!options.disableTranslation` 时才运行 Tier 5。任一前面 Tier 返回 `actProbeAmbiguous`、`actProbeCancelled`、`actProbeBudgetExhausted` 或 `actProbeMalformed` 时绝不运行 Tier 5。
- **单边配对条件** `tier5Edge(oldIndex, newIndex)`：
  1. 同 `DisplayID`，且 `sameStructureForAct` 兼容；
  2. `rectValidOnScreen(newNode.Bounds, screen)` 且 `sameHorizontalBand(oldNode.Bounds, newNode.Bounds)`（沿用已有定义：左右边缘差 `<= max(8, maxW/50)`，宽高差 `<= max(8, maxSide/5)`）；
  3. 祖先链对齐：**L1 相等**，或 **`lineageL2ForAct` 相等且包含至少 1 个非根强锚点**（复用 `lineageL1` / 部件 2 的 `lineageL2ForAct` 与非文本非根强锚点检查）；
  4. 强语义身份：满足 `tier4SelfEvidence`（E1）、`tier4UniqueResource`（E2）或 `subtreeEvidence`（E3，含部件 3 数字骨架）之一；
  5. 正向扫描与反向扫描**全树双向唯一**，并共享 `matchBudget`（同一 `candidateLimit`、`lineageLimit`、`evidenceLimit`）。
- **遮挡与 `bubble` 联动防线**：
  1. `directFrameOcclusionProven`（`act.go:1448`）、`revalidateBubbleTapFrame`（`act.go:964`）从 `tier == 4` / `tier != 4` 升级为 `tier >= 4` / `tier < 4`；`revalidateColocatedTapFrame`（`act.go:1266, 1284`）升级为 **`matchColocated.tier >= 4 || matchChild.tier >= 4`**，确保任一绑定命中 Tier 4/5 时均强制通过 `tapFrameTier4OcclusionProven`（全树最深命中必须在执行目标子树内，且无外部交互拥有者覆盖落点）；
  2. `coextensive_child` 若 `matchParent.tier >= 4 || matchDelegate.tier >= 4`，在调用 `resolveDirectTapScopeFrame` 时或在其出口显式确保 `tapFrameTier4OcclusionProven` 被执行；
  3. `bubble_colocated` 若 `matchChild.tier == 5 || matchColocated.tier == 5`，额外基于双方 `raw.Bounds` 校验双节点纵向位移一致性（记 `childCenterY`、`colocCenterY` 为各自 `raw.Bounds` 中心纵坐标，`maxH = max(oldChildH, freshChildH, oldColocH, freshColocH)`）：
     `abs((freshChildCenterY - oldChildCenterY) - (freshColocCenterY - oldColocCenterY)) <= max(16, maxH/4)`；
     若位移不一致（说明只有一个节点漂移），按 `targetChangedReasonChildChanged` 拒绝；
  4. `scroll` 容器允许通过 Tier 5 找回后，仍必须通过既有 `sameScrollScope`（轴向重叠 `>= 0.50` 与离轴容差）与轨迹安全证明。
- **日志**：`actTargetLog` 允许记录 `tier` 1–5；命中 Tier 5 返回 `actMatch{tier: 5, independentContent: ..., independentSubtree: ...}`。

### 4.5 坐标 `tap`/`long_press` 原地交互拥有者守恒兜底（部件 5）
- **适用范围**：仅限坐标 `tap` 与 `long_press`（不含 `text`、不含 raw `swipe`、不含 `emptyPoint`、不含 `len(freshNodes) == 0`）；**坐标 `(x, y)` 永远保持请求原点不变**。
- **触发条件**：当 `provePointTarget` 中因 `len(freshNodes) != len(observedIndices)` 或 `proveTargetSet` 的二分图配对失败，准备返回 `point_changed` 时，在未撤权、无 `hierarchy_unavailable` 错误、且**非多交互拥有者拒绝（[`point.go:618-620`../../../internal/engine/point.go#L618-L620)）与非新帧映射损坏（[`act.go:1609-1622`../../../internal/engine/act.go#L1609-L1622)）**的前提下，尝试唯一拥有者守恒检查：
  1. 从 `obs.Projection` 构造 `oldView`（`collectTapPublicView(obs)`）并取得旧点命中 `oldHits := resolvePointSet(obs.Projection.Tree.Root, x, y)`，要求 `collectPointOwners(oldView, oldHits)` 恰有 **1 个**交互拥有者 `oldOwner`、`pointOwnerMatchesOperation(oldOwner, operation)` 为真、**`oldHits` 中的每一个节点都是 `oldOwner` 本身或其公开后代**、且 `externalInteractiveOwnersAt(oldView, oldOwner, x, y)` 为空；
  2. 要求新帧 `freshNodes` 非空、`collectPointOwners(freshView, freshNodes)` 恰有 **1 个**交互拥有者 `freshOwner`、`pointOwnerMatchesOperation(freshOwner, operation)` 为真、**`freshNodes` 中的每一个节点都是 `freshOwner` 本身或其公开后代**（防止新帧在点上混入未归属该拥有者的兄弟/外层节点）、且 `externalInteractiveOwnersAt(freshView, freshOwner, x, y)` 为空；
  3. 解析 `oldOwner` 与 `freshOwner` 对应的源索引 `oldOwnerIdx` / `freshOwnerIdx`（缺失或损坏映射均不通过），校验 `freshOwnerRaw.Bounds` 有效在屏且包含 `(x, y)`；
  4. 用 `matchActTargetWithOptions` 传入 `defaultActMatchOptions().withoutTranslation()`（**禁用 Tier 5**，仅允许 Tier 1–4，即要求原位置重叠/紧几何，不允许远距离漂移换人）匹配 `oldOwnerRaw`，必须返回 `actProbeUnique` 且 `indexOf(fresh, match.raw) == freshOwnerIdx`（若返回 `cancelled`/`budget`/`malformed` 按既有终端错误处理）；
  5. 若 `oldOwnerRaw` 不具备 E1 自身非空 `Text`/`Description` 与 E2 全树唯一 `ResourceID`，须额外通过 `subtreeEvidence(obs.Projection.Capture, oldOwnerIdx, fresh, freshOwnerIdx, actMatchTapLike, budget)`（含数字骨架守恒），确保未把无文案容器底下的子业务内容偷偷换掉。
- **拒绝语义**：兜底任一条件不满足，仍统一返回 `409 target_changed`（`reason=point_changed`）。

### 4.6 有界瞬态单次复检（部件 6）
- **常量**：`actRevalidateMaxAttempts = 2`，`actRevalidateRetryDelay = 150 * time.Millisecond`。
- **触发条件** `isTransientRevalidateError(apiErr *protocol.APIError) bool`：
  - `apiErr != nil && apiErr.Code == protocol.CodeTargetChanged`；
  - 在调用 `decorateTextRevalidateError` **之前**检查 `apiErr.Details["reason"]`，仅以下 7 个枚举允许再复检 1 次：`target_missing`、`lineage_changed`、`child_changed`、`scope_blocked`、`scroll_scope_changed`、`trajectory_blocked`、`point_changed`。
  - **明确绝不复检**：`control_revoked`、`session_ended`、`foreground_changed`、`foreground_unknown`、`display_unavailable`、`hierarchy_unavailable`、`node_not_found`、以及 `target_changed` 中 `reason` 为 `capability_changed` 或 `target_ambiguous` 或空值。
- **执行规约**：
  1. `Engine.Act` 中将 `revalidateScrollTarget` / `revalidateActTarget` 包裹在最多 2 次的 `attempt` 循环内；
  2. 第 1 次成功则立即跳出并进入 `dispatch`（0ms 额外开销）；
  3. 第 1 次失败且满足 `isTransientRevalidateError(revalidateErr)` 且 `attempt == 0` 且 `!e.workRevoked(work) && work.Ctx.Err() == nil` 时：
     - 调用 `pauseInterruptible(work.Ctx, actRevalidateRetryDelay)`；
     - 唤醒后先检查 `e.workRevoked(work) || work.Ctx.Err() != nil`，若已撤权则立即返回 `stoppedActRevalidation()`（零等待、不抓第 2 帧、不交付）；
     - 否则进入第 2 次重验：重新执行 `captureForActRevalidation` 与整套目标证明；
  4. 第 2 次重验若覆盖了 `fresh`（`attempt == 1` 返回了非空 `fresh2`），使用第 2 帧作为后续 `dispatch` 与拒绝交付的帧；若第 2 次抓帧返回 `display_unavailable` / `hierarchy_unavailable` / `control_revoked`，则按第 2 次的真实错误返回且不交付旧的第 1 帧（`deliverableRejection` 本身已排除这三类错误码）；
  5. 退出循环后，若仍失败且 `req.Operation.Type == "text"`，再调用 `decorateTextRevalidateError(op, revalidateErr)` 保持对外 `stage=target, reason=target_unconfirmed` 契约不变。

---

## 5. 算法

### 5.1 Ref 目标匹配总控伪代码（部件 1–4）

```text
func matchActTargetWithOptions(ctx, revoked, original, fresh, want, options):
    check STOP / validActCapture / indexOf(original, want)
    sawLineageMismatch = false

    // Tier 1-3: 使用归一化后的 lineageL1/lineageL2ForAct（部件1剥离合成显示根；部件2仅在Tier3对非目标祖先用ancestorStructureKey）
    for tier in 1..3:
        check STOP
        probe = newTierMatcher(..., tier, options).probeWant(wantIndex)
        if probe.state == unique:
            return actMatch{raw, tier, independentContent, independentSubtree: probe.independentSubtree}, unique
        if probe.state in {ambiguous, cancelled, budgetExhausted, malformed}:
            return ..., probe.state
        if probe.lineageMismatch:
            sawLineageMismatch = true

    // Tier 4: 仅当 sawLineageMismatch == true，紧几何 >=80% + E1/E2/E3（E3含部件3数字骨架）
    if sawLineageMismatch:
        check STOP
        probe = newTierMatcher(..., 4, options).probeWantTier4(tier4TightGeometry, options.mode, wantIndex)
        if probe.state == unique:
            return actMatch{raw, tier: 4, independentContent: true, independentSubtree: true}, unique
        if probe.state != none:
            return ..., probe.state

    // Tier 5 (部件 4): 仅当 !options.disableTranslation && Tier 1-4 全为 none
    if !options.disableTranslation:
        check STOP
        probe = newTierMatcher(..., 5, options).probeWantTier5(options.mode, wantIndex)
        if probe.state == unique:
            return actMatch{raw, tier: 5, independentContent: true, independentSubtree: true}, unique
        if probe.state != none:
            return ..., probe.state

    return actMatch{lineageMismatch: sawLineageMismatch}, none
```

### 5.2 重验与瞬态复检时间轴（部件 6）

```text
Act 认领 O (ready -> claimed(P))
   │
   ▼ [attempt = 1]
   captureForActRevalidation -> fresh1
   ├─ foregroundMatches 失败 ---------> 不重试 -> deliver(fresh1)? -> 409 foreground_changed/unknown
   ├─ capability_changed / ambiguous -> 不重试 -> deliver(fresh1)? -> 409 target_changed
   ├─ STOP / hierarchy / display -----> 不重试 -> 不交付 -> 409 control_revoked / 503
   ├─ 成功 ---------------------------> [0ms 额外开销] -> dispatch(frame1)
   └─ 瞬态白名单 target_changed ------> pauseInterruptible(150ms)
                                           ├─ 期间 STOP -> 立即退出 409 control_revoked（不抓第2帧、不交付）
                                           ▼ [attempt = 2]
                                           captureForActRevalidation -> fresh2
                                           ├─ 成功 -----> dispatch(frame2)
                                           └─ 失败 -----> (若text: decorate) -> deliver(fresh2)? -> 409/503
```

### 5.3 异常、撤销、超时与预算耗尽行为

- **撤权（STOP）**：`pauseInterruptible`、`matchBudget` 每步扫描、`Preview` 前后、`tier5`/`pointOwner` 兜底及 `dispatch` 前的 `beginUIDispatch` 全部实时检查 `workRevoked` 与 `ctx.Err()`；一旦撤权，**永远返回 `409 control_revoked`**，绝不降级为 `target_changed`，且不交付 `O`、不派发输入。
- **预算耗尽与畸形树**：Tier 5 与坐标拥有者兜底均纳入 `matchBudget`（上限与既有 `candidateLimit`/`lineageLimit`/`evidenceLimit` 同阶，不构造 $O(N^2)$ 边集）；耗尽或发现环路/损坏映射时**永远返回 `503 hierarchy_unavailable`**，不退化重试、不交付 `O`。
- **硬件/抓取失败**：`ProbeDisplay` 失败返回 `503 display_unavailable`；`Capture` 失败返回 `503 hierarchy_unavailable`。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/act_revalidation_policy.go` | 新增 `isSyntheticDisplayRoot` 并在 `lineageL1` 跳过（含 `len(lineage) > 0` 置 `root=true` 守卫）；新增 `ancestorStructureKey` 与供 Tier 3/5 使用的 `lineageL2ForAct`（保持 `lineageL2` 原样服务 `sameRawLineageKeys`），并限制去噪链的非根强锚点不采信 `Text`/`Description`；新增 `isDynamicNumericToken`/`numericSkeleton`/`subtreeSkeletonMatch` 接入 `subtreeEvidence`；在 `actMatch`/`actProbeResult`/`actMatchOptions` 增加独立子树证据标识与反向开关 `disableTranslation`（零值默认启用 Tier 5，并提供 `.withoutTranslation()` 给坐标拥有者兜底禁用 Tier 5）；实现 `probeWantTier5`；将 `independentSubtree` 计算并入 `probeWant` 正反唯一命中后。 |
| `internal/engine/act.go` | 在 `Act` 封装最多 2 次 `revalidate` 瞬态短复检循环（每次重试重置 `fresh`/`tapFrame`/`frame`/`revalidateErr`，确保失败时只交付第 2 帧、不残留第 1 帧；将 `decorateTextRevalidateError` 移至循环后），并在 `actTargetLog` 与 `logActPoint` 中传入真实 `attempt`；`directFrameOcclusionProven`、`revalidateBubbleTapFrame`、`revalidateCoextensiveChildFrame` 将 `tier == 4` 改为 `tier >= 4`，`revalidateColocatedTapFrame` 升级为 `matchColocated.tier >= 4 || matchChild.tier >= 4`；`revalidateColocatedTapFrame` 增加 Tier 5 双节点基于 `raw.Bounds` 的纵向同步位移守卫；`resolveDirectTapScopeFrame` 允许 `independentContent || independentSubtree` 安全解绑失效 `carrier`；`provePointTarget` 失配时按严格触发条件对坐标 `tap`/`long_press` 接入拥有者守恒兜底。 |
| `internal/engine/act_target_shared.go` | `actTargetLog` 将 `tier` 有效范围由 `<= 4` 扩至 `<= 5`，增加受控 `attempt` 计数输出；新增 `colocatedSyncShiftOK` 等轻量守卫辅助。 |
| `internal/engine/point.go` | 新增 `provePointOwnerFallback`，实现严格单拥有者 + 全树后代归属 + `externalInteractiveOwnersAt` 空集 + Tier 1–4 唯一匹配 + 无自有文案时子树证据验证；全部共享 `pointProof.budget` 与 `STOP` 检查。 |
| `internal/engine/scroll.go` | `revalidateScrollTarget` 中对 `act.target` / `scroll.revalidate` 保持同步记录（必要时透传 `attempt` 计数）。 |
| `internal/eventlog/log.go` | 同步 `tier` 注释说明为 `1-5`（`"tier"` 与 `"attempt"` 已在 `allowedKeys` 白名单中，无需新增键）。 |
| `release/versions.json` 及生成产物 | 发版时更新 `release_version`、`contract_version`、`android_version_code`，执行版本常量生成与发布清单构建脚本。 |
| `docs/{architecture.md,interface.md,integration.md}` | 同步描述合成根归一化、Tier 3 L2 祖先去噪、数字骨架守恒、Tier 5 平移找回、坐标拥有者守恒、`tier>=4` 遮挡与瞬态单次复检机制；变更后按 `AGENTS.md` 第 15 条完成只读文档审计。 |

### 6.2 配置与取值

所有阈值均作为引擎内部常量，不暴露为请求参数或外部环境变量：

- `actRevalidateMaxAttempts = 2`、`actRevalidateRetryDelay = 150 * time.Millisecond`：直接对齐仓库 `textSubmitMaxCaptures = 2` 与 `textSubmitRetryDelay = 150ms` 的实测经验窗口，兼顾短动画收尾与 `STOP` 响应速度。
- `numericSkeleton` 与 `sameHorizontalBand`：完全复用 `sameHorizontalBand` 既有几何容差（水平边差 `<= max(8, maxW/50)`，宽高差 `<= max(8, maxSide/5)`）；`bubble_colocated` 双节点同步位移容差 `max(16, max(oldChildH, freshChildH, oldColocH, freshColocH)/4)` 与 Tier 4 中心位移容差保持同量级。

### 6.3 日志与可观测性

- 仅复用 [`internal/eventlog/log.go`../../../internal/eventlog/log.go#L63-L108) 中**已存在的白名单键**：`"tier"`（范围扩为 `1–5`，并更新注释）、`"attempt"`（`1` 或 `2`，通过 `actTargetLog`、`logActPoint` 与 `logScrollRevalidate` 透传，区分首帧通过与短复检结果）、`"proven"`、`"outcome"`、`"reason"`、`"ms"`。
- 绝不向日志或错误响应写入节点 `text`、`resource-id`、骨架串、`ref`、坐标或树内容。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威（`internal/engine`） | 无变化：所有匹配、短复检、撤权与派发仍在 `Engine.Act` 唯一控制下。 |
| STOP 不等待 | 保持：150ms 延迟使用 `pauseInterruptible`，`STOP` 到达立即唤醒并返回 `409 control_revoked`。 |
| 单向消费与提交条件化 | 保持：`O` 仍在 `revalidate` 前从 `ready -> claimed(P)`；`Preview` 不推进连续性；第 2 帧交付只在未撤权且通过 `deliverRejectedObservation` 门禁时发布。 |
| 状态锁短小 | 保持：所有匹配、`Preview`、`150ms` 延迟与复采全在 `e.mu` 锁外进行。 |
| 事实分层 | 保持：未派发仍为 `not_executed`；坐标动作不改原点；`settle` 19 字段 `rawIdentity` 不变。 |

### 6.5 契约与文档影响

- `/api/*` JSON 请求体、响应体结构、错误码及 `error.details.reason` 枚举均无增删；MCP 13 工具入参 schema 不变。
- `act` 实时重验行为放宽涉及共同契约语义变更，按 `AGENTS.md` 第 13 条通过 `release/versions.json` 递增共同 `contract_version`（`32 → 33`），同步更新 [`docs/architecture.md`../../../docs/architecture.md)、[`docs/interface.md`../../../docs/interface.md)（及 [`docs/integration.md`../../../docs/integration.md) 如涉及重验说明），并在实现提交前通过独立只读文档审计。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. 输入法、状态栏、Toast 或系统悬浮窗闪现导致 `connectDisplayWindows` 包入/撤出合成显示根，引起全屏目标 `lineage_changed` 误杀。
2. 列表卡片内角标、时间戳、计数等纯数字信息或无文案图标/子载体变化，引起直点/`coextensive_child` 的 `scope_blocked` 或 Tier 4 E3 误杀。
3. 父容器标题数字/选中态变更导致 `lineage_changed`。
4. 顶部横幅弹出/收起或列表头插入项使目标沿纵向平移超过 `50%` 高度（仍处于同祖先链与同水平带且全树唯一），引起 `target_missing`。
5. 坐标点击点下方卡片内部装饰层增减（交互拥有者未变且无外部新遮挡）引起 `point_changed`。
6. 在 `150ms` 内可完成渲染/落位的短动画引发的单帧瞬态 `target_changed`。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 目标自身非数字业务文案、类型、可编辑性或能力变更（如“确认”变“取消”、“关注”变“已关注”、按钮变灰） | 属于调用方语义目标本身改变，按核心安全红线必须拒绝并交付新帧 |
| 同构列表中多个相同文案/结构卡片同时满足平移条件（或无唯一锚点） | 违反双向唯一性约束，必须报 `target_ambiguous`，绝不猜测其中一个 |
| 节点发生显著横向位移或跨列重排（超出 `sameHorizontalBand` 水平容差） | Tier 5 故意限定同水平带防横向误触相邻列控件；超出时必须返回并交付新帧让上层决策 |
| 目标无自身非空非数字文案且子树内全是纯数字文本（如纯数字键盘按键变换数字、时间选择器滚轮 `"10"→"11"`） | `subtreeSkeletonMatch` 强制要求至少 1 条不含数字的不变可读文案，纯数字变换按真实变化拒绝（反之，若卡片同屏共现不变的非数字静态标签如 `"选择"` 与编号 `"套餐1"→"套餐2"`，后者按动态编号骨架设计边界放行） |
| 坐标点击点坐标本身偏离目标矩形，或原坐标落入新出现的外部交互控件/弹窗 | 坐标动作严守原点授权与防遮挡边界，不自动追随移动的控件 |
| 超过 `150ms` 才稳定的长转场动画或异步加载、以及 `bubble_ancestor` 中交互祖先与子节点关系已变更 | 超出单次 `150ms` 有界复检窗口或已发生父子交互归属变化，必须停止并交付第 2 帧 |

### 7.3 成本

- **首帧成功路径**：不触发 Tier 5、不进入拥有者兜底、不进入第 2 次复检，额外 CPU 为合成根指针判断与（仅 Tier 3 命中失败路径的）`ancestorStructureKey` 构造，额外 I/O 延迟 **0ms**，额外日志 **0 条**。
- **命中 Tier 5 / 拥有者兜底路径**：与现有 Tier 4 同阶，受共用 `matchBudget` 线性限制（`O(N)` 扫描 + 最多 64 个后代收集），无额外设备抓帧。
- **触发瞬态复检路径**：最多增加 `150ms` 可中断等待 + `1` 次 `ProbeDisplay`/`Capture` + `1` 次 `Preview` 投影；对已确定非瞬态错误（前台变化、能力变化、歧义、`STOP`）立即短路，**0ms** 额外等待。

---

## 8. 验证计划

### 8.1 单元测试

在 `internal/engine` 中按模块补充单测（并在发布构建前执行全量单测）；**包含明确反向对照用例**（去掉对应实现分支时测试必失败）：

| 用例 | 期望 |
| --- | --- |
| `TestRevalidateSyntheticDisplayRootNormalization`（含反向对照） | 模拟第 2 顶层窗口插入 `displayRootNode` 导致全树深度 +1：无独立内容/低重叠目标仍可通过 Tier 1–3 命中执行（去掉 `isSyntheticDisplayRoot` 后因长度变化退化失败） |
| `TestRevalidateTier3AncestorNoiseToleranceAndBoundaries` | 非目标祖先 `Text`/`Description`/`Checked` 变更时 Tier 3 通过；祖先缺失 `Has*` 仍不透明；`bubble_ancestor` 下祖先自身变化或 `C` 变交互/改文案仍拒绝 |
| `TestRevalidateNumericSkeletonE3AndCarrierUnbind`（含反向对照） | 卡片内 `"2分钟前"→"3分钟前"` 或角标增减时，Tier 4 E3 与直点/`coextensive_child` `carrier` 解绑成功；纯数字替换 `"10"→"11"`、业务词变化 `"确认"→"取消"`、列表卡 `"商品A"→"商品B"` 全部拒绝 |
| `TestRevalidateTier5HorizontalBandTranslationAndGuards`（含反向对照） | 同祖先链纵向平移超 50% 重叠在 Tier 5 命中并按新坐标派发；平移后落入外部交互遮挡层时被 `tapFrameTier4OcclusionProven` 拦截为 `scope_blocked`；横向移出带外或出现双候选时拒绝；`bubble_colocated` 单节点漂移超阈值时报 `child_changed`；`scroll` 容器重叠处于 `0.50..0.60`+包装层时经 Tier 5 与 `sameScrollScope` 通过，而平移 `>50%` 时被 `sameScrollScope` 拒绝为 `scroll_scope_changed` |
| `TestPointTargetOwnerConservationFallback`（含反向对照） | 坐标 `tap`/`long_press` 点内装饰子节点增减但唯一交互拥有者同框且无外部遮挡时在原 `(x,y)` 执行；出现外部新遮挡、拥有者变为另一卡片或子树业务文案替换时报 `point_changed`；raw `swipe` 与坐标 `text` 不进入该兜底 |
| `TestActRevalidateTransientSingleRetryAndStop`（含反向对照） | 首帧因瞬态 `target_missing`/`point_changed` 失败、150ms 后第 2 帧恢复时成功执行并记 `attempt=2`；第 2 帧仍失败时交付的是第 2 帧而非第 1 帧；`capability_changed`/`foreground_changed`/`target_ambiguous` 不重试（0 次等待）；`150ms` 窗口内到达 `STOP` 立即返回 `control_revoked`、零抓第 2 帧、零交付、零派发 |
| `TestEventLogWhitelistSync` | 验证记录 `tier=5` 与 `attempt=2` 正常入账且不触发白名单丢弃或文本/ref 泄露 |

### 8.2 实机/系统级

按 `AGENTS.md` 第 19 条执行**默认发布交付范围**（由主代理亲自执行；没有真机或设备离线时记录为未执行并停步回报）：

1. **发版组装与清单核验**：在已提交的干净源码树更新 `release/versions.json` 并运行 `scripts/generate-version.ps1`、`scripts/build-release.ps1` 与 `scripts/verify-manifest.ps1`，核验 `build/release/manifest.json`（含 `contract_version=33`、产物 SHA-256 与工具目录指纹）。
2. **手机整组覆盖部署**：核验清单后对已连接 Android 设备执行整组覆盖部署（含手机端控制 App、核心服务与手机 HTTP MCP 适配器，保留 App 私有数据）。
3. **最小实机闭环**：在真实设备上覆盖本次变更路径：
   - 外窗（输入法/通知或系统窗口）出现前后对原 `O` 的普通控件执行 `ref tap`；
   - 带动态时间/计数子节点的列表卡片 `carrier` 变化后执行 `ref tap`（含 `bubble` 子节点场景）；
   - 顶部元素伸缩/滚动后纵向平移控件与受控 `scroll` 动作验证；
   - 原地坐标 `tap` 验证与 `observe`/`act`/`status` 基础闭环，记录实机 `target_changed` 拒绝与成功样本。
4. **Windows 主机工具替换与双端独立探针验证**：使用同一源码提交重新构建并替换 Windows 主机的 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验主机清单后用本次新产物运行独立探针，分别验证 **主机 stdio MCP** 与 **手机 HTTP MCP**。
5. **依赖方责任边界**：宿主 Agent 自身的配置、托管 MCP 进程重启与重连默认由用户处理，不作为独立探针或本仓库交付门禁。

### 8.3 记录

- 实现提交后，按流程在 `maintenance/verification/act-revalidation-resilience-and-bubble-safety.md` 写入完整验证记录，严格分开记录**已实现 / 已单测 / 已实机验证 / 磁盘替换 / 独立探针验证**，注明实现提交 hash、构建环境、实际执行命令、样本与真实结果；同步按需更新 `maintenance/status.md`。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 整体路线 | 采用路线 C（部件 1–5 静态抗外围扰动解耦 + 部件 6 单次 `150ms` 瞬态短复检） |
| 动态文案边界 | 仅容忍纯数字/时间/计数/角标 token 与无文案装饰层变化；非数字业务文案必须严格不变，且至少存在 1 条不含数字的不变可读文案 |
| 坐标兜底边界 | 坐标 `tap`/`long_press` 不改坐标；仅在同一唯一交互拥有者（Tier 1–4 证明且无外部遮挡）内部装饰/数字变化时允许原地放行 |
| `bubble` 与全树遮挡升级 | `tier >= 4`（含 Tier 4/5）统一过 `tapFrameTier4OcclusionProven`；`bubble_colocated` 在 Tier 5 命中时额外校验双节点纵向位移差 |
| 瞬态复检预算与白名单 | 最多 2 帧、间隔 `150ms`、可被 `STOP` 中断；仅限 7 类瞬态 `target_changed` reason；第 2 帧仍失败交付第 2 帧 |
| 发布交付范围 | 按 `AGENTS.md` 第 19 条默认全套（清单核验 + 手机整组保留数据部署 + 最小实机闭环 + Windows 双 exe 构建替换 + stdio/HTTP 双端独立探针；宿主 Agent 重连由用户处理） |

### 9.2 未决

无悬空未决项（所有阈值、边界与交付范围已在提案确认前关闭）。

---

## 10. 实施顺序

1. **基线核对**：按当时 `HEAD` 重新核对 `act.go`、`act_revalidation_policy.go`、`act_target_shared.go`、`point.go`、`scroll.go`、`eventlog/log.go` 的精确行号与调用关系。
2. **核心策略实现（可先落地并同步补单测）**：
   - 2a. `act_revalidation_policy.go`：部件 1（合成显示根归一化）、部件 2（Tier 3 祖先去噪）、部件 3（数字骨架 `subtreeSkeletonMatch`）、部件 4（Tier 5 平移找回与 `independentSubtree` 输出）。
   - 2b. `act.go` / `act_target_shared.go` / `point.go`：`directFrameOcclusionProven` 与双绑定 `tier >= 4` 遮挡校验升级、`bubble_colocated` 同步位移守卫、`carrier` 解绑扩展、部件 5（坐标拥有者守恒兜底）与部件 6（`Act` 瞬态单次复检循环及 `attempt` 日志）。
   - 2c. `eventlog/log.go` 注释同步与 `scroll.go` 日志兼容调整。
3. **单元测试与反向对照验证**：编写并运行 `internal/engine` 与 `internal/eventlog` 新旧单测及全量包测试（主代理亲自执行，单次引擎全量测试超时设为 15 分钟）。
4. **文档同步与独立只读审计**：同步更新 `docs/architecture.md`、`docs/interface.md`（及必要时 `docs/integration.md`），由独立只读文档审计 subagent 审核至 `clean` 结论。
5. **版本源更新与实现提交**：按 `release/versions.json` 单一版本源递增版本（`release_version=0.37.0`、`contract_version=33`、`android_version_code=39`）并运行生成脚本，执行完整编译与全量测试通过后完成**实现提交**。
6. **发布组装与默认交付验证（基于干净的实现提交）**：主代理亲自执行 `build-release.ps1`、`verify-manifest.ps1`、手机整组覆盖部署（保留私有数据）、最小实机闭环、Windows 主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 替换、以及 stdio / HTTP 双端独立探针验证。
7. **提案归档与验证记录提交**：将本提案移至 `maintenance/proposals/implemented/act-revalidation-resilience-and-bubble-safety.md`，头部写入实现提交 hash 与状态，重写相对链接，补写 §11，落地 `maintenance/verification/act-revalidation-resilience-and-bubble-safety.md` 并核对 `maintenance/status.md` 后单独提交归档。

---

## 11. 实现结果

实现提交：`69e66cef0892dc017c7a8830c7f13339d8e0087e`。实现与验证记录：
[act-revalidation-resilience-and-bubble-safety](../../verification/act-revalidation-resilience-and-bubble-safety.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 部件 1 合成显示根归一化（§4.1） | `internal/engine/act_revalidation_policy.go`：`isSyntheticDisplayRoot` 按 §4.1 判定；`lineageL1` 命中该层即终止，并把最后一个真实窗口根条目的 `root` 置真。`sameRawLineageKeys` 与 `changedPublicAncestorRef` 都经 `lineageL1`，因此 `changed_ref` 不再可能指名合成显示根。 |
| 部件 2 Tier 3/5 祖先去噪（§4.2） | 同文件：`ancestorStructureKey`/`ancestorStructureKeyOf`、`lineageEntryForAct`、`lineageL2ForAct`、`strongLineageAnchorForAct`、`sameLineageKeysForActBudgeted`、`hasStrongNonRootAnchorBudgeted`、`sameAnchoredLineageForAct`；Tier 3 经 `tierMatcher.lineage` 使用去噪 L2，Tier 1/2 仍用完整 `lineageL1`，`lineageL2` 原样保留给 `sameRawLineageKeys`，`transparentWrapper` 未改。 |
| 部件 3 数字骨架守恒（§4.3） | 同文件：`isDynamicNumericToken`、`numericSkeleton`、`subtreeSkeletonMatch`；`subtreeEvidence` 的 tap-like 分支在 `sameReadableMultiset` 失败后复用同一份 `readableSubtree` 结果再做骨架判定；`independentSubtree` 由 `probeWant` 在正反唯一命中后计算，Tier 4/5 唯一结果恒为真。 |
| 部件 4 Tier 5 平移找回（§4.4） | 同文件：`tier5Edge`（同显示、`sameStructureForAct`、`rectValidOnScreen`、`sameHorizontalBand`、L1 或去噪 L2 + 非根强锚点、`tier4Evidence`）、`probeWantTier5`、`probeWantWithEvidence`（正反扫描共享预算）；`matchActTargetWithOptions` 的 tier 4→5 选择与 `disableTranslation` 开关；`actMatchOptions.withoutTranslation()`。 |
| 部件 4 遮挡与 `bubble` 联动（§4.4 防线） | `internal/engine/act.go`：`directFrameOcclusionProven` 改为 `match.tier < 4`；`revalidateBubbleTapFrame` 按执行祖先 `>= 4` 触发；`revalidateCoextensiveChildFrame` 在父绑定 `>= 4` 时追加同一证明；`revalidateColocatedTapFrame` 改为任一绑定 `>= 4`，并在任一绑定命中 Tier 5 时校验共位纵向同步位移。`internal/engine/act_target_shared.go`：`colocatedSyncShiftOK`（任一高度 ≤0 即拒绝，容差 `max(16, maxH/4)`）。 |
| 部件 3 carrier 安全解绑（§4.3 末条） | `internal/engine/act.go` `resolveDirectTapScopeFrame`：两处门禁改为 `match.independentContent \|\| match.independentSubtree`。 |
| 部件 5 坐标拥有者守恒兜底（§4.5） | `internal/engine/point.go`：`provePointOwnerFallback` 及 `pointFallbackOwner`、`pointFallbackRawOwner`、`pointFallbackHierarchyError`、`pointFallbackViewError`；`internal/engine/act.go` `provePointTarget` 的两处接入点（命中集合长度失配、`proveTargetSet` 的 `point_changed`）。 |
| 部件 6 有界瞬态单次复检（§4.6） | `internal/engine/act.go`：`actRevalidateMaxAttempts = 2`、`actRevalidateRetryDelay = 150ms`、`isTransientRevalidateError`（七类闭集）、`Act` 的 attempt 循环（暂停前清空四个输出、暂停后再查撤权、`decorateTextRevalidateError` 移到循环之后）。 |
| 日志与白名单（§6.3） | `internal/engine/act_target_shared.go` `actTargetLog`（tier ≤5、attempt 1–2）；`internal/engine/act.go` `logActPoint`（attempt）；`internal/engine/scroll.go` `revalidateScrollTarget` 透传 attempt；`internal/eventlog/log.go` 注释同步为 1–5。 |
| 版本源（§6.5） | `release/versions.json` 手改为 `0.37.0`/`33`/`39`；`scripts/generate-version.ps1` 重写 `internal/protocol/version_generated.go`、`GeneratedVersion.java`、`strings_version.xml`、`AndroidManifest.xml`。 |
| 文档（§6.1 末行） | `docs/{architecture,interface,mcp,integration,runbook,release,client,control-protocol,phone-operations}.md` 与 `README.md` 的版本号与重验语义同步。 |
| 单元测试（§8.1） | 新增 `internal/engine/act_revalidation_proposal_tier_test.go`、`act_revalidate_transient_test.go`、`act_point_owner_fallback_test.go`；迁移 `act_point_test.go`、`act_delivery_test.go`、`quality_test.go`、`observation_hint_test.go`、`recovery_entry_test.go`、`scroll_revalidate_test.go`、`act_tap_scope_test.go`、`act_proposal_point_reasons_test.go`、`act_revalidation_matrix_test.go` 中绑在单帧契约上的 fixture/断言。 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| §7.3「首帧成功路径额外 CPU 仅合成根指针判断」 | 实际还会在目标自身无可读文案时做一次有界子树遍历以得出 `independentSubtree`（`probeWant`）。§6.1 与 §4.3 要求把该标识并入唯一命中后计算供 carrier 解绑消费，故按规范实现；遍历每侧至多 64 个后代、纯 CPU、无设备 I/O，`act.target` 日志仍为 0 条。 |
| §4.3「骨架仅进 `actMatchTapLike`」 | 新增 `actMatchTapLikeExact` 模式，写后提交路径**整体**保持精确可读多重集（不只是键盘同水平带回退），由 `submitTapFrameOptions = defaultActMatchOptions().withExactTapLikeEvidence().withoutTranslation()` 固化；`probeWant` 改用 `matcher.opts.mode` 使该口径真正生效。理由：§2.4 要求不修改 `text` 写后提交逻辑。 |
| §4.4 Tier 5 的适用范围 | 提案只写 ref 目标；实现因共用匹配器，`scroll` 容器同样可获得 Tier 5 找回，但仍必须通过既有 `sameScrollScope` 与轨迹安全证明。 |
| §4.5「全部共享 `pointProof.budget`」 | 坐标兜底内部的 ref 匹配经 `matchActTargetWithOptions` 使用其自带有界预算（上限同为 `len(orig)+len(fresh)`），`pointProof.budget` 继续承担资源普查与子树证据。未合并为同一计数器，因为 `matchBudget` 不是并发安全的外部计数器；两条路径各自有界，未扩大总成本。 |
| §4.5 触发条件「非多交互拥有者拒绝」 | 除兜底自身的单拥有者规则外，在 `provePointTarget` 的 `proveTargetSet` 出口显式跳过该情形，使代码与文字逐条对齐（行为等价，避免一次无谓的兜底尝试）。 |
| §8.1 第 5 行反向对照「子树业务文案替换即 `point_changed`」 | 按 §4.5 步骤 5，子树守恒只在拥有者**没有**自有可读文案时才检查；拥有者自带稳定 `Text` 时其被动后代按装饰对待。若对该情形强制子树守恒，会让新增的被动叶节点造成多集不等而拒绝，反过来破坏 §7.1.5「点内装饰层增减应放行」的主目标。因此该反向对照收窄为「无自有文案拥有者的子树业务文案替换必须拒绝」，由 `TestPointOwnerFallback` 的 `subtree_business_text_replaced` 与 `resource_present_but_not_unique_still_rejects` 覆盖；拥有者自带文案一侧的拒绝由 `TestProposalPointSetFreshFrameDriftReturnsPointChanged/owner_text_changed` 覆盖。 |
| §6.1「`scroll.revalidate` 必要时透传 attempt」 | `act.target` 已携带 `attempt`，`scroll.revalidate` 的字段集合保持原样，未新增 `attempt` 键，避免无谓扩面。 |
| §8.1 末段「发布构建前执行全量单测」 | 按用户决定未执行 `go test ./...` 与 `internal/engine` 整包（引擎包是最耗时部分）。变更路径以定向运行覆盖，证据与边界见验证记录。 |
| `release/versions.json` 的 `source_commit` | 未重新钉到本次实现提交，沿用 `d65c976…`。该字段的文档语义是「版本号冻结时的基线提交（仅供校验）」，发布清单的 `source_commit` 记录实际构建 HEAD，因此不影响发布核验；此前两次发版同样未逐版重钉。 |
| §8.2 手机整组部署、最小实机闭环、Windows 双工具替换与双端独立探针 | 归档提交时尚未执行：未跟踪文件会让发布脚本判定 `source_dirty=true`，发布构建必须在干净树上进行，故这些步骤在归档之后的同一源码提交上执行，结果记入验证记录的发布段。 |


| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- 不改变 `O` 认领状态机（`ready -> claimed(P) -> retired`）、`P` 幂等性、`STOP` 零等待撤权优先级或 `deliverRejectedObservation` 的交付门禁与错误码集合。
- 不改变静态准入阶段的任何规划与 `422` 报错分类。
- 不改变 `identity.go` 与 `settle.go` 的 19 字段 `rawIdentity`、采样时长（400/300/600ms）及稳定性判据。
- 不改变 `text_submit.go` 写后读回及 `submit_ref` 提交流程、`raw swipe` 起点集合证明及 `key` 派发逻辑。
- 不新增任何 `/api/*` 字段、错误码、`error.details.reason` 枚举、MCP 工具 schema 字段或 `eventlog` 白名单键。
