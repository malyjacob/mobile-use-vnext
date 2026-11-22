# 提案：统一 act 静态目标解析策略与错误返回契约

状态：已实现（实现提交 `b305e23675d1b55c2f05ca8a904e579e400c72fd`，2026-11-22）。
构建、实机与双入口探针结果见[验证记录](../../verification/act-target-resolution-and-error-contract.md)。
基线提交：`c38a43eb2d4821ab2993ee9bba27ce77f042ae51`（分支 `master`，`2026-11-22`）。
适用范围：`act` 全分支（`tap`、`long_press`、`scroll`、`swipe`，以及 `text` 的焦点点击/坐标解析）的静态目标解析、对应的认领后实时重验、静态 `422` 错误码与 `error.details` 契约、`409 target_changed` 的归因 `reason`，以及 MCP 工具说明与 `serverInstructions`。不改变 `key`、`text` 的写入/回车状态机与 `text_input`、公开树 `Node`/`ReadNode` 形状、O/P 生命周期、STOP 与恢复入口规则。
影响范围：`internal/engine`（`scroll.go`、`tap_scope.go`、`point.go`、`act.go`、`act_revalidation_policy.go` 及测试）、`internal/protocol`（`errors.go` 及测试）、`internal/httpapi`（`app_test.go` 错误详情线测试）、`internal/client`（客户端保真测试）、`internal/mcp`（`schema.go`、`schema_test.go`、`result_manifest_test.go`、`result_parity_test.go`）、`scripts/mcp-probe`（如涉及目录与错误详情校验）、受影响的正式文档（`docs/interface.md`、`docs/architecture.md`、`docs/mcp.md`、`docs/integration.md`、`docs/release.md`、`docs/runbook.md`、`docs/phone-operations.md`、`docs/control-protocol.md`、`docs/client.md`、`README.md`）；实施发布时涉及 `release/versions.json` 及版本生成物、手机整组构建/清单/覆盖部署、主机侧 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 及 `maintenance/verification/`。Android 控制 App 业务逻辑与固定 uiautomator JAR 不变。
契约（`/api/*` 字段与 `contract_version`）：改变（破坏性清理）——新增 `422` 错误码 `no_safe_trajectory`；统一全 `act` 静态 `422` 码表与 `error.details` 契约（以统一 `candidates` 与可选 `blocking_refs` 取代 9 个历史引导/诊断键：`occluding_ref`、`occluding_refs`、`ambiguous_refs`、`owner_refs`、`nearest_actionable_ref`、`safe_point`、`safe_carrier_ref`、`suggested_point`、`suggested_carrier_ref`）；为非 `text` 的 `409 target_changed` 新增封闭 `details.reason`；升级 `scroll`/`tap`/`long_press`/`point`/`swipe` 的静态解析与重验语义。共同契约拟从 `21` 升级为 `22`（配套建议 `release_version="0.26.0"`、`android_version_code=28`）；本提案落盘不修改版本源。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[scroll.go](../../../internal/engine/scroll.go)、[tap_scope.go](../../../internal/engine/tap_scope.go)、[point.go](../../../internal/engine/point.go)、[act.go](../../../internal/engine/act.go)、[act_revalidation_policy.go](../../../internal/engine/act_revalidation_policy.go)、[errors.go](../../../internal/protocol/errors.go)、[schema.go](../../../internal/mcp/schema.go)、[interface.md](../../../docs/interface.md)、[versions.json](../../../release/versions.json)）。

---

## 0. 摘要

当前 `act` 的目标解析用“排他 DOM 树”的洁癖规则去套“多层重叠协作”的 Android 界面，并把历史演进中的四套错误返回方言、15 个重叠 `details` 键和 100 多行过期副本堆在同一路径上，直接引发了远程 Issue #27（外层同矩形滚动容器静态 422 拒死、贴底边起滑）、#28（坐标歧义不返回候选、嵌套滚动坐标必败、装饰重叠拒死）与 #31（`bubble` 越过共位兄弟按钮向大祖先空白处漂移并报 `no_safe_point`）。

本提案采用**“弱保软”正本清源路线**：
1. **解析升级（意图明确时一次做对）**：`scroll` 引入双向同视口滚动栈归约（≥85% 覆盖率不互相阻塞）、非滚动容器祖先提升、坐标选最内层、安全区间居中轨迹与 3 轴线探测；`tap`/`long_press` 将 3×3 网格扶正为第 4 级正常执行候选，把 `bubble` 严格锚定到目标视觉区并直接吸附唯一的局部共位可点击控件（双绑定重验）；坐标目标升级为“最深命中集合防漂移证明”，支持基于动作能力的唯一拥有者自动消歧，并让 raw `swipe` 彻底摆脱 `ambiguous_point`。
2. **错误清理（遇拒时一次给全线索）**：全 `act` 静态拒绝统一收口为 5 个正交 `422` 错误码（新增 `no_safe_trajectory`），砍掉 9 个历史引导/诊断键，全部改用统一的 `candidates`（`[]map[string]any`，含 `ref`/`role`/`actions`/`bounds`/`relation`/`executable`）+ 可选 `blocking_refs`；删除 `staticTapRefCheckLegacy` 过期副本，补齐 `point`/`scroll` 的有界拓扑与环守卫；为 `409 target_changed` 补充归因 `reason`；同步更新 MCP 工具说明与 `serverInstructions`。
3. **明确不做**：不碰 #30（`text` 写已确认但回车未确认的复合结果语义）、#34（图标开关状态可读性）与 #36（Tier 3 放宽祖先链重建匹配）。

## 1. 问题

### 1.1 现象与代价

分诊为**取舍**：当前多数拒绝行为都有正式契约与单元测试保护，但这套保守契约持续导致正常界面首轮必败、多轮无效往返以及错误返回格式碎片化。本次顺应明确的产品取向——**“弱保软”**（系统按确定性机制优先帮调用方把明显意图做成；确需拒绝时提供完整、清晰、可直接执行的候选列表）——做一次跨 `act` 的契约清理与语义升级。

三个关联 Issue 及连带债务的现场代价如下：

1. **Issue #27（同矩形嵌套滚动静态拒死 + 贴底边起滑失效）**：
   - 知乎信息流存在 `FrameLayout(scroll) > ViewGroup(scroll) > RecyclerView(scroll)` 的三层同矩形结构（`n56 > n57 > n66`，矩形同为 `[0,363,1080,2265]`）。对最外层 `n56` 或中层 `n57` 调用 `act.scroll` 时，静态检查直接返回 `422 unsupported_node_action`（`"无法在该容器中心线上生成可证明的滚动轨迹"`），且 `details` 为空；调用方被迫逐层试错 3 次才命中 `n66`。
   - 问题页上 `n3578`（`[0,1458,1080,2400]`）同样静态 422；改点内层 `n3581` 后虽派发，但轨迹贴近屏幕最底端起滑（`y=2306`，距底边 `2400` 仅 `94px`，容易踩中底部固定工具栏或系统手势区），返回 `effect=not_observed`，最终靠原始 `swipe (540,1900)→(540,900)` 一次滚成功。
2. **Issue #28（坐标歧义不返回候选 + 嵌套必败 + 装饰重叠拒死）**：
   - `act.scroll container={x:540,y:1900}` 返回 `422 ambiguous_point`，`details` 为空。只要页面存在任何嵌套滚动容器，坐标滚动在数学上 **100% 必败**。
   - `act.tap target={x:56,y:140}` 落在多个同深度节点上（例如图标与背景/角标并列），即使只有一个可点击或需要重选，也直接报空 `422 ambiguous_point`，没有候选 `ref`，破坏了“不知 `ref` 但知位置”的坐标输入通道。
3. **Issue #31（`bubble` 忽视共位兄弟按钮并存在远端误点隐患）**：
   - 网易云音乐播放页中，无动作文本节点 `n5233`（`"999+"`）视觉上压在兄弟/堂兄弟评论按钮 `n5232`（`[915,1675,1028,1787]`）上；两者共同的交互父祖先 `n5223` 是整条播放控制栏。
   - 默认 `bubble=true` 时，引擎盲目沿父链爬上 `n5223`，把正下方的 `n5232` 当成阻塞者；随后引擎试图在 `n5223` 其它空白区域找安全点——本次凑巧 `n5223` 被子控件占满才报 `422 no_safe_point`（否则就会**静默点到几百像素外的大容器背景**！）。报错时 `n5232` 被混在 `occluding_refs=["n5230","n5232","n5253"]` 里，不标注它就是重合的评论按钮，调用方仍须人工对照树与坐标。
4. **连带历史债务**：
   - `tap`/`long_press` 静态 `422` 堆砌了两代共 15 个零散 `details` 键（旧引导组 4 个 + 新诊断组 5 个 + 分类作用域 6 个）；`scroll` 把“非滚动容器”“无几何”“轨迹被阻”混为一个裸 `unsupported_node_action`；`point` 与 `target_changed` 的 `details` 全空。
   - [tap_scope.go](../../../internal/engine/tap_scope.go#L1722-L1836) 仍留着 100 余行过期副本 `staticTapRefCheckLegacy` 与 `planBubbleTapTargetLegacy`，导致 `text(ref)` 焦点点击对 `root`/`context`/无几何返回旧版裸 `unsupported_node_action` 与已被取代的 `occluding_ref`。
   - `tapSuggestStaticPoint` 的 3×3 网格明明已经用整树严格谓词证明了安全落点，却只在错误路径上输出 `suggested_point` 而不直接执行，白白强制调用方多一轮往返。

### 1.2 证据链

#### 通路一：滚动嵌套互锁、贴边起滑与错误空壳
1. **后代滚动容器一刀切阻塞**：[scroll.go:180-251](../../../internal/engine/scroll.go#L180-L251) 的 `scrollSafeSpans` 在 `222-243` 行把每个公开节点的最近 `isScrollContainer`（非 `want` 且非 `want` 的 raw 祖先）记入 `blocked`。当 `want` 包裹同矩形子滚动容器时，`blocked` 覆盖 `100%` 基础区间，可用区间为空。
2. **坐标滚动把嵌套父子全判歧义**：[scroll.go:539-574](../../../internal/engine/scroll.go#L539-L574) 的 `scrollContainerAtPoint` 遍历整棵树，把包含 `(x, y)` 的每一个公开候选规约到各自最近的 `isScrollContainer` 并写入 `scopes` 集合。只要存在外层包内层的滚动结构，`len(scopes) >= 2`，`572` 行必然返回裸 `422 ambiguous_point`。
3. **单一中心线 + 贴边起滑**：[scroll.go:206](../../../internal/engine/scroll.go#L206) 只取正交中点一条线；[scroll.go:352-418](../../../internal/engine/scroll.go#L352-L418) 的 `scrollGeometry` 在 `393, 403` 行内部写死以 `rect` 中点作正交坐标，并在 `394-411` 行把 `down`/`up`/`right`/`left` 的起点钉在容器边缘内侧 `margin`，容器贴底边时极易踩中底部固定栏或导航手势条。
4. **`scroll` 错误无细节、缺全树环守卫，而 `effect` 天然兼容子树**：[scroll.go:488-529, 576-587](../../../internal/engine/scroll.go#L488-L587) 将非滚动容器、无几何与无安全轨迹全部映射为无 `details` 的 `422 unsupported_node_action`（空投影或损坏映射在 `477, 507` 行也报 `422`）；`scrollSafeSpans`（`217` 行）与 `scrollContainerAtPoint`（`542` 行）未做全树拓扑环校验；[scroll.go:919-964](../../../internal/engine/scroll.go#L919-L964) 的 `scrollContentUnits` 本就递归收集容器整棵子树的内容单元，天然支持同视口滚动栈的 `effect` 计算。

#### 通路二：`bubble` 远端漂移、共位兄弟盲区、网格闲置与过期副本
1. **父链盲爬**：[tap_scope.go:959-979](../../../internal/engine/tap_scope.go#L959-L979) 的 `nearestInteractiveAncestor` 只沿 `parent` 向上寻找第一个 `isTouchInteractive` 节点，对几何共位的兄弟/堂兄弟按钮完全不可见。
2. **锚点脱靶漂移**：[tap_scope.go:999-1034](../../../internal/engine/tap_scope.go#L999-L1034) 的 `chooseBubbleTapPoint` 在 `child` 中心被阻塞或失败后（`1004-1020` 行），直接在 `1027` 行退回到 `enumerateTapCandidates(scope, ancestorRaw)` 遍历整个大祖先容器寻找落点，没有限制在 `child` 附近且不因竞争控件阻塞而中止漂移。
3. **算得出却不点**：[tap_scope.go:1332-1367](../../../internal/engine/tap_scope.go#L1332-L1367) 的 `tapSuggestStaticPoint` 仅在 `chooseTapPlan` / `chooseBubbleTapPoint` 全部失败后的 `diagnoseTapStaticFailure`（`1369-1432` 行）中运行 3×3 网格，并用 `provesGuidancePoint` 证明点可用，却只把坐标写入错误体。
4. **过期副本仍在服务且与新路径割裂**：[act.go:297-298, 366-370](../../../internal/engine/act.go#L297-L370) 与 [tap_scope.go:1821-1836](../../../internal/engine/tap_scope.go#L1821-L1836) 的 `staticTextRefCheck` 对 `root`/`context`/缺几何返回旧版裸 `unsupported_node_action`，其余焦点规划则调用 `staticTapRefCheckLegacy`（`1722-1780` 行）与 `planBubbleTapTargetLegacy`（`1782-1815` 行）继续产生 `occluding_ref`。

#### 通路三：坐标单节点独裁与信息截留
1. **同深度多节点一律拒死**：[point.go:58-92](../../../internal/engine/point.go#L58-L92) 的 `resolvePoint` 只在 `bestCount == 1` 时返回 `pointUnique`；`bestCount > 1` 在 [point.go:122-131](../../../internal/engine/point.go#L122-L131) 直接返回无 `details` 的 `422 ambiguous_point`，既不区分节点是否具备点击能力，也不返回命中的 `ref`；连不接受 `ref` 的原始 `swipe` 起点也会因此被拒。
2. **新鲜重验仅限单索引、缺拓扑环守卫且未优先用 `work.Projector`**：[act.go:477, 834-860](../../../internal/engine/act.go#L834-L860) 的 `provePointTarget` 只在 `pointUnique` 时运行，且在 `Preview` 前直接调用未带环守卫的 `lineageOf`（[act.go:1657-1663](../../../internal/engine/act.go#L1657-L1663)），并在 `847` 行直接读取 `e.projector` 而忽略 `work.Projector`。

#### 通路四：`act` 错误契约割裂、`target_changed` 零归因与 MCP 指引过时
1. **details 键膨胀且 `CloneErrorDetails` 要求受控容器**：[errors.go:9-22](../../../internal/protocol/errors.go#L9-L22) 列出的白名单键在 `act` 静态拒绝上横跨两代 15 个键；[errors.go:60-111](../../../internal/protocol/errors.go#L60-L111) 的 `CloneErrorDetails` 递归复制 string-keyed map、slice 与 array（不递归结构体），因此新契约对象须使用 `map[string]any` 切片承载。
2. **重验 `target_changed` 缺少原因**：[act.go:862-865](../../../internal/engine/act.go#L862-L865)、[scroll.go:782-785](../../../internal/engine/scroll.go#L782-L785)、[tap_scope.go:151-153](../../../internal/engine/tap_scope.go#L151-L153) 与 `revalidateTapTargetFrame` / `revalidateBubbleTapFrame` 的全部 `target_changed` 出口均无 `details.reason`（唯一例外是 `text` 在 [act.go:1346-1361](../../../internal/engine/act.go#L1346-L1361) 的 `decorateTextRevalidateError` 补充 `stage`/`reason`/`dispatch`，且其 `1347` 行遇到 `apiErr.Details != nil` 会早退）。
3. **MCP 工具说明与指引**：[schema.go:128-131, 328-352, 392-421](../../../internal/mcp/schema.go#L128-L131) 仍按旧保守语义及旧键 `nearest_actionable_ref`/`safe_point`/`suggested_point`/`suggested_carrier_ref` 指导模型。

### 1.3 为什么直觉会出错

- **“只在 422 报错里补候选 ref，不改解析策略”**：知乎嵌套列表、同深度图标+角标、共位按钮仍会在第 1 次调用必败，只是从“盲猜 3 轮”变成“固定失败 1 轮再重试”，白耗整轮大模型推理。
- **“把所有后代滚动容器一律视为不阻塞，或单向按父矩形算 85%”**：不设覆盖门槛会把列表内横滑条、Tab 栏吞掉导致 #12 复发；而只算单向 `inter/rOut >= 85%` 在 `clipChildren=false` 越界子节点上会把全屏子节点误判成小父节点的同视口，必须做**双向 85%**。
- **“`bubble` 只要是祖先可点，在祖先内随便找个空位点就行”**：在 #31 这种 `ConstraintLayout` 扁平布局里，目标文字刚好盖在兄弟按钮上，此时往大容器其他位置飘移极易点中空白背景或触发无关操作。
- **“共位吸附不做面积守卫，只要中心重合就吸附”**：一旦静态文字下面压着一个全屏/半屏透明点击遮罩，或小按钮恰落在大卡片壳中心，无守卫吸附会变成替调用方误点无关区域。
- **“保留旧的 9 个 details 键，只额外叠加一个 `candidates`”**：同一个错误对象同时塞两套语义相反的建议点与阻塞数组，只会让文本/结构化响应膨胀并继续诱导模型写分支猜旧键。

## 2. 目标与非目标

**目标**

- **G1（滚动“弱保软”解析与居中多线轨迹）**：支持双向同视口滚动栈归约（后代与父节点裁剪矩形双向 ≥85% 覆盖不阻塞）、非滚动节点自动向上提升至最近滚动祖先、坐标选择最内层滚动容器、安全区间内居中起止点与 3 轴线探测（`50% → 25% → 75%`），以及直接滚动容器失败时唯一可执行内层子滚动容器的自动兜底。
- **G2（点击/长按/聚焦“弱保软”解析与视觉锚定）**：直接 `tap`/`long_press(ref)` 升为 4 级（含 `grid_3x3` 直接派发）+ 唯一双向同视口可点击子容器兜底；`bubble=true` 时 `C → A` 严格锚定在 `C` 视觉范围内且在竞争遮挡时止漂，随后识别唯一的局部共位可点击控件 `B`（双绑定 `C+B` 派发与重验）。
- **G3（坐标目标命中集合防漂移与智能消歧）**：坐标解析保留最深同深度节点集合 `S`；`swipe` 彻底取消 `ambiguous_point`；`tap`/`long_press`/`text` 仅在出现 ≥2 个可交互竞争者且无法按请求动作唯一消歧时才报 `ambiguous_point`，否则直接放行并在通过拓扑守卫后的派发前证明整个命中集合未漂移。
- **G4（统一 `act` 静态错误码与极简 `candidates` 契约）**：全 `act` 静态目标拒绝统一为 5 个正交 `422` 错误码（新增 `no_safe_trajectory`）；废除 9 个历史 `details` 键，全面统一为 `reason`、`scope`/`scope_ref`、`candidates`（至多 4 项，含 `executable`）、可选 `blocking_refs`（至多 3 项），以及 `unsupported_node_action` 必带的动作/祖先字段；彻底删除 `staticTapRefCheckLegacy`，让 `text(ref)` 焦点点击复用统一规划器并共享统一有界拓扑守卫。
- **G5（`409 target_changed` 归因）**：为非 `text` 的 `act` 重验 `target_changed` 附带封闭枚举 `details.reason`，保留 `text` 自有的 `stage`/`reason`/`dispatch` 契约不受干扰。
- **G6（MCP 说明与指引同步）**：同步更新 [schema.go](../../../internal/mcp/schema.go) 的 `callerGuidance`、`act` 工具描述与 `bubble`/`container` 参数说明，使调用方明确理解自动解析边界与基于 `candidates[0]` 的零重观察重试路径。
- **G7（契约升级与整组交付）**：推进 `contract_version 21 → 22`、`release_version 0.25.0 → 0.26.0`、`android_version_code 27 → 28`，按规范完成测试、文档审计、干净构建、清单核验、手机整组部署、最小实机闭环、主机工具替换及独立探针验证。

**非目标**

- 不改变 Issue #30（`text` 写入已确认但 ENTER 目标不可确认时的 `outcome` 与 `text_input` 分层语义）。
- 不改变 Issue #34（图标开关无障碍属性投影与点击后组件状态差异）。
- 不放宽 Issue #36（Tier 1–3 重验中祖先链根节点重建的节点匹配判据；本次仅暴露 `target_changed` 的 `reason=lineage_changed` 以便精确诊断）。
- 不改变 `key` 动作、`text` 写入/读回/ENTER 状态机、`swipe` `duration_ms`、公开树 `Node`/`ReadNode` 形状、`effect` 三态、O/P 生命周期、STOP、恢复入口映射、Android 控制 App 业务逻辑与固定 uiautomator JAR。
- 依赖方（外部 Agent 宿主进程、配置与重连）由用户处理，不作为本仓库交付门禁。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 维持现状或仅改文档 | 保持现有规则与错误键，仅在文档提醒调用方改选内层 ref | 零契约变动 | #27/#28/#31 持续高频多轮失败与退化为 raw `swipe` |
| R2 最小补丁（仅给 3 个 422 补候选） | 保留现有拒绝判据与 9 个旧 `details` 键，只为 `scroll`/`point`/`no_safe_point` 追加候选字段 | 契约增量兼容，改动较小 | 首轮调用依然必败；`bubble` 远端漂移隐患不除；`details` 与 `tap_scope.go` 历史包袱继续膨胀 |
| R3 激进无守卫盲发 | 静态遇冲突直接盲发坐标中心，跳过新鲜树坐标或集合重验 | 几乎无静态拒绝 | 极易在换页、弹层遮挡或全屏底板覆盖时静默误点，违背事实与安全底线 |
| **R4 “弱保软”智能解析 + `act` 错误大清理（选定）** | 在静态阶段按 Android 布局规律做确定性智能消歧与居中规划，认领后保持完整新鲜帧证明；静态拒绝统一为 5 个正交 `422` 码 + `candidates` 契约并废除 9 个历史键与旧副本 | 破坏性契约升级（`21 → 22`）；需更新旧保守测试用例；极少数多层非常规布局下系统自动选择可能与调用方主观意图不同（需通过后观察复核） | Android 未暴露 z 序或滚动轴时仍存在固有限制（见 §7.2），且无法在静态阶段证明动态业务回调 |

选择 **R4**：它兼顾了**一次调用直接成功**（弱保软：不把确定无疑的 Android 分层布局当歧义或冲突拒死）与**失败即拿精确可执行线索**（正本清源：用一套最小正交的 `candidates` 替代 15 个碎片化键），同时通过新鲜帧上的 `C+B` 双绑定、`pointSet` 精确集合相等和 `sameScrollScope` 守住防误点安全门禁。

## 4. 判据

本节所有距离/面积比较统一使用 `int64` 避免整数乘法溢出，所有矩形与区间使用半开边界 `[Left, Right)`、`[Top, Bottom)`。`scroll`、`tap`/`long_press`/`text(focus)` 与 `point` 的公开树查找与新鲜拓扑访问均先运行有界图/映射校验（`collectTapPublicView` / `validateTapRawTopology` / `checkTapSourceMapping` 与带访问上限的 `findPublicNode` / `lineageOf`），发现结构或映射损坏时静态直接返回 `503 hierarchy_unavailable`（零认领、零采集），新鲜帧损坏同样返回 `503 hierarchy_unavailable`（不交付新 `O`）。

### 4.1 滚动解析、同视口栈、居中轨迹与 3 轴线规划（`scroll`）

1. **双向同视口定义 `isCoextensive(a, b)`**：取两节点公开 bounds（或各自 raw 与屏幕裁剪后的正面积矩形 `rA`、`rB`），交集 `inter = rA ∩ rB`。当且仅当交集非空且对**双方**同时满足以下四式时为同视口（防止 `clipChildren=false` 的越界全屏子控件误判为小容器的同视口）：
   - `int64(inter.Width()) * 100 >= int64(rA.Width()) * 85` 且 `int64(inter.Height()) * 100 >= int64(rA.Height()) * 85`
   - `int64(inter.Width()) * 100 >= int64(rB.Width()) * 85` 且 `int64(inter.Height()) * 100 >= int64(rB.Height()) * 85`
2. **`container.ref` 解析（静态顺序）**：
   - 目标不存在 → `409 node_not_found`；`root` / `context` / 缺 bounds 或屏内裁剪为空 → `422 invalid_target`（`reason=root|context|no_geometry`，缺几何时若存在可用祖先或页面滚动容器则附 `candidates`）；显式 `enabled=false` → `409 node_disabled`。
   - 若目标满足 `isScrollContainer(R)`，直接以其为锚点容器 `want`（若 `scrollSafeSpansAt` 中发现非空同视口滚动栈，日志 `mode` 记为 `scroll_coextensive`，否则为 `scroll_direct`）。
   - 若目标自身 `Role == RoleScroll` 但缺 `swipe` 能力 → `422 unsupported_node_action`（`reason=target_missing_action`, `scope="target"`, `scope_ref=R.Ref`，附 `target_actions` 与祖先/页面可执行滚动 `candidates`）。
   - 若目标为普通内容/交互节点（`Role != RoleScroll`），沿公开父链跳过 `context`（遇 `root` 停止）寻找**最近一个 `Role == RoleScroll` 祖先 `A`**：
     - 不存在 → `422 unsupported_node_action`（`reason=not_scrollable`, `scope="target"`, `scope_ref=R.Ref`, `target_actions=...`），`candidates` 列页面上可执行的 `role=scroll` 容器（`relation="page_scroll"`，按裁剪面积降序、先序升序，至多 4 项）。
     - `A` 显式禁用或公开 bounds 无效 → `422 unsupported_node_action`（`reason=ancestor_invalid`, `scope="ancestor"`, `scope_ref=A.Ref`, `ancestor_ref=A.Ref`, `ancestor_actions=...`, `target_actions=...`），附页面其它可执行滚动 `candidates`。
     - `A` 缺 `swipe` → `422 unsupported_node_action`（`reason=ancestor_missing_action`, `scope="ancestor"`, `scope_ref=A.Ref`, `ancestor_ref=A.Ref`, `ancestor_actions=...`, `target_actions=...`），附 `candidates`。
     - `A` 具备公开滚动条件但进入规划后 `raw` 裁剪交集为空 → `422 invalid_target`（`reason=no_geometry`, `scope="ancestor"`, `scope_ref=A.Ref`, `ancestor_ref=A.Ref`），附页面可执行 `candidates`。
     - `A` 有效 → **自动提升至 `A`**（`mode=scroll_ancestor_lift`）。认领后仅重验 `A` 本身身份与 `sameScrollScope`（不要求原列表项 `R` 身份不变，因为 `R` 随列表滚动，操作锚点是容器 `A`）。
3. **`container={x, y}` 坐标解析**：
   - 在屏内前提下收集所有公开 bounds 包含 `(x, y)` 且 source 有效的 `isScrollContainer` 节点集合 `H`。
   - 若 `H` 为空 → `422 invalid_target`（`reason=no_scroll_container`, `scope="point"`），`candidates` 列页面可执行 `role=scroll` 容器（`relation="page_scroll"`，按裁剪面积降序、先序升序，至多 4 项）。
   - **父子链路归约**：若 `P ∈ H` 存在任一严格后代 `D ∈ H`，从候选顶端移除 `P`，仅保留每个祖先-后代链条的**最内层节点**构成最内层集合 `I`（此步自然折叠 `n56 > n57 > n66` 同视口栈为 `n66`，同时符合手指点中列表内嵌横滑条的直觉）。
   - 若 `len(I) == 1`：尝试在 `I[0]` 上用 3 轴线基础规划器规划；成功则选用（`mode=scroll_point_innermost`）。若失败，沿 `I[0]` 在 `H` 中的滚动祖先由近及远尝试规划，命中首个可执行祖先则自动回退选用（`mode=scroll_point_fallback`）；若均失败则返回最内层或失败容器的 `422 no_safe_trajectory`（不触发内层兜底，避免跳回不包含 `(x, y)` 的旁支容器）。
   - 若 `len(I) >= 2`（互不为祖先的平级/交叉滚动容器）：统计 `I` 中对当前 `(direction, amount)` 可执行者 `I_exec`；若 `len(I_exec) == 1` 则唯一消歧选用（`mode=scroll_point_disambiguated`）；否则返回 `422 ambiguous_point`（`reason=competing_scroll_containers`, `scope="point"`），`candidates` 列出 `I`（`relation="competing"`）及 `H` 中可作为公共回退的可执行共同滚动祖先 `P`（`relation="ancestor"`，若有），按 `executable=true` 优先、祖先优先于不可执行项 / 裁剪面积升序排序，至多 4 项。
4. **同视口滚动栈免阻塞 + 3 轴线探测**：
   - 对选定容器 `want`，构造 `ancestors`（`want` 的所有 raw 祖先，含环保护）与 `coextensive`（`want` 的所有满足 `isScrollContainer` 且与 `want` 满足双向 `isCoextensive` 的 raw 后代）。
   - `scrollSafeSpansAt(projection, want, clipped, direction, orthogonalCoord)` 允许作用域为 `want`、`ancestors` **或 `coextensive`** 的候选通过；仅把 `scrollScopeUnusable` 或非同视口内层/外部滚动作用域记入阻塞区间。
   - 按 `clipped` 的正交边跨度依次计算 3 个探测坐标（并跳过不在 `shared = clipped ∩ public` 正交范围内及与前值重复的坐标）：
     - 垂直滚动：`x1 = clipped.Left + clipped.Width()/2`（50%）、`x2 = clipped.Left + clipped.Width()/4`（25%）、`x3 = clipped.Left + int(int64(clipped.Width())*3/4)`（75%）
     - 水平滚动：`y1 = clipped.Top + clipped.Height()/2`（50%）、`y2 = clipped.Top + clipped.Height()/4`（25%）、`y3 = clipped.Top + int(int64(clipped.Height())*3/4)`（75%）
     取**第一条**能通过下述居中 `scrollGeometry` 与 `provesSegment` 的轴线与最长安全段。
5. **居中轨迹计算 `scrollGeometry(safeRect, screen, direction, amount, orthogonalCoord)`**：
   - 沿用现有常量（`scrollPagePercent=60`、`scrollHalfPercent=30`、`scrollEdgeMarginMin=24`、`scrollEdgeMarginDivisor=10`、`minScrollTravel=48`、`scrollDurationMS=400`）。
   - 函数显式接收选定的 `orthogonalCoord int`，**不再**在函数内重算 `rect` 正交中点（防止覆盖 25%/75% 探测坐标）。
   - 在选定安全区间 `[safeStart, safeEnd)`（长 `extent`）内，计算 `margin = max(24, extent/10)`、`usable = extent - 2*margin`、`travel = usable * percent / 100`，要求 `usable > 0 && travel >= 48`。
   - **起止点不再贴边，改为安全区间正中对称展开**：令 `mid = tapSafeAverage(safeStart, safeEnd)`、`low = mid - travel/2`、`high = low + travel`：
     - `down`：`start=(orthogonalCoord, high) → end=(orthogonalCoord, low)`；`up`：`start=(orthogonalCoord, low) → end=(orthogonalCoord, high)`；
     - `right`：`start=(high, orthogonalCoord) → end=(low, orthogonalCoord)`；`left`：`start=(low, orthogonalCoord) → end=(high, orthogonalCoord)`。
     - 复核 `insideScrollRect(start, safeRect)`、`insideScrollRect(end, safeRect)` 与 `screen`。
6. **直接容器无可行轨迹时的唯一可执行内层子滚动兜底**：
   - **仅当请求通过直接 `container.ref` 进入**（`R` 本身就是 `isScrollContainer`，不用于 `scroll_ancestor_lift` 与坐标 `scroll_point_fallback`）且 `want` 在 3 条轴线上均失败时：扫描 `want` 子树内所有非同视口后代 `isScrollContainer`，对其中通过**直接 3 轴线基础规划器（禁止再递归内层兜底）**的可执行子容器按同视口祖先-后代折叠去重。若折叠后**恰有 1 个**逻辑可执行子滚动容器 `D`，自动委托给 `D`（`mode=scroll_inner_fallback`）。
   - 否则静态返回 `422 no_safe_trajectory`（`scope="target"`, `scope_ref=want.Ref`）：若 3 条轴线中至少有一条产生正长度安全区间且全部仅因 `usable <= 0` 或 `travel < 48` 失败则 `reason="insufficient_travel"`，否则 `reason="trajectory_blocked"`。`candidates` 收集子树内及阻塞外部的 `isScrollContainer`（子女/后代标 `"child"`、外部重叠标 `"overlapping"`，`executable=true` 优先、裁剪面积降序、先序升序，至多 4 项）；若存在不可用 `source` 或非滚动阻塞边界，可附 `blocking_refs`（至多 3 项）。
7. **认领后实时重验**：
   - 先过既有显示/前景、有界原始拓扑校验（`validateTapRawTopology` 失败返回 `503 hierarchy_unavailable`）、`matchActTarget`（Tier 1–3）与 `sameScrollScope`。
   - 新鲜帧使用相同的双向 `isCoextensive` 规则先在 `plan.orthogonalCoord` 上调用 `provesSegment` 校验原 `plan.start → plan.end`；若不成立，则在新鲜帧同一容器内按 `50% → 25% → 75%` 与居中 `scrollGeometry` 重新规划一条合法整段轨迹。失败则返回 `409 target_changed` 并附 `details.reason`（见 §4.4.3）。

### 4.2 点击、长按与 `text(ref)` 焦点规划（`tap` / `long_press` / `text(focus)`）

1. **直接目标 `target.ref = C`（自身具备请求动作）**：
   - **4 级静态候选（均使用 `provesSubtreeTapPoint`）**：
     1. `default_center`（原 `raw` 中心；出 `base` 时改测 `base` 中心）；
     2. `carrier_node`（非阻塞承载子节点：非匿名先序 → 匿名先序）；
     3. `center_span`（垂直中心线最长区间中点 → 水平中心线最长区间中点）；
     4. **`grid_3x3`**（在 `base` 上按 `1/6, 1/2, 5/6` 交点 `y` 外层、`x` 内层去重生成，跳过前三级已测坐标，**通过即直接用于派发，不再拒成 422**）。
   - **同视口可点击子容器兜底（第 5 步，仅 `tap`/`long_press`，不用于 `text` 焦点）**：若 4 级候选均失败，检查阻塞 `C` 的顶层交互边界中是否存在**恰有 1 个**对 `C` 满足双向 `isCoextensive(B, C)`、未禁用、具备请求动作且自身通过直接 4 级静态规划的子容器 `B`；若存在则自动委托给 `B`（`mode=coextensive_child`），认领后双绑定 `C` 与 `B`。
   - 若仍失败 → `422 no_safe_point`（`reason=blocked|ambiguous|mixed`, `scope="target"`, `scope_ref=C.Ref`），`candidates` 列出阻塞中具备请求动作且未禁用的子节点（`relation="child"`，按距 `C` 中心距离升序、裁剪面积升序，`executable=true` 优先，至多 4 项）；`context`/非同类动作阻塞者及 `ambiguous` 造成的同深并列非交互节点进 `blocking_refs`（至多 3 项）。
2. **无动作目标 `target.ref = C` 且有效 `bubble=true`（仅 `tap`/`long_press`）**：
   - 若 `isTouchInteractive(C)` 为真但缺本次请求动作（如仅有 `long_press` 时请求 `tap`），保持交互边界不跨越，返回 `422 unsupported_node_action`（`reason=target_has_other_action`, `scope="target"`, `scope_ref=C.Ref`, `target_actions=...`），并附重叠或最近可操作 `candidates`（若有）。
   - 若 `!isTouchInteractive(C)`，按顺序执行以下两步：
     - **步 A：视觉区锚定的最近交互祖先（`C → A`）**：
       - 查找 `A = nearestInteractiveAncestor(view, C)`（跳过 `context`、遇 `root` 停）。若 `A` 有效且支持请求动作，构造 `A` 的作用域 `scopeA` 并取 `cClip = rect(C) ∩ scopeA.base`。
       - 若 `!cClip.Empty()`，在 `cClip` 内按序测试：`C` 中心（越出 `cClip` 时取 `cClip` 中心）→ `cClip` 纵/横中心线最长非阻塞段中点 → `cClip` 3×3 网格，每点均须通过 `provesGuidancePoint(view, A, scopeA, x, y)`。命中即确定计划 `C → A`（`mode=bubble_ancestor`）。
       - 若 `cClip` 为空或其中无通过点，取 `probeRect = cClip`（若空则取 `rect(C)`），检查**是否有任一竞争交互控件（含 `scopeA` 内阻塞者或外部整树同深/更深命中交互节点）相交 `probeRect`**：
         - **若 `cClip` 非空且无竞争交互控件相交 `probeRect`**：允许按 `A` 自身 4 级候选补测（均须通过 `provesGuidancePoint`），通过则仍为 `bubble_ancestor`；
         - **若 `cClip` 为空，或存在竞争交互控件相交 `probeRect`**：**立即中止 `A` 内其它区域搜索，严禁向 `A` 的无关空白处漂移**，直接转入步 B。
     - **步 B：局部共位可点击目标吸附（`C → B`，专治 #31）**：
       - 在整棵公开树中筛选候选 `B`，须同时满足：
         1. `B != C` 且 `B` 不是 `C` 的祖先（祖先链由步 A 负责）；
         2. `validTapPublicTarget(B, action)`（非 root/context、已启用、正面积 bounds、含请求动作）；
         3. **强共位与防误吸条件**：
            - 若 `B` **不是 `C` 的后代**（兄弟/堂兄弟/外层重合控件）：`rect(B).Contains(cCenter)` **或** `area(rect(C) ∩ rect(B)) * 100 >= area(rect(C)) * 50`，**且** `area(clipped(B)) * 100 < area(screen) * 50`（反半屏以上背景底板守卫）；
            - 若 `B` **是 `C` 的后代**（非可点击包装容器内部的按钮）：须满足双向 `isCoextensive(B, C)`，**或** `area(rect(C) ∩ rect(B)) * 100 >= area(rect(C)) * 50` 且 `area(clipped(B)) * 100 < area(screen) * 50`（防止半屏卡片壳因中心恰压了一个 1% 面积的小按钮而被误吸）；
         4. `B` 自身可通过直接 4 级规划（优先在 `rect(C) ∩ scopeB.base` 内用中心/中心线/3×3 网格选点；交集无安全点时再退回 `B` 的 4 级安全点）。
       - 对匹配的 `B` 集合做一次双向同视口祖先-后代折叠（若 `B1` 是 `B2` 祖先且 `isCoextensive(B2, B1)`，合并保留 `B2`）。
       - 若折叠后**恰有 1 个** `B` → **自动吸附并规划 `C → B`**（`mode=bubble_colocated`）。
       - 若有 **≥ 2 个** → 统计其中严格包含 `C` 中心点的候选：若**有且仅有 1 个**候选包含 `C` 中心点则选中该候选；否则不盲猜，静态返回 `422 no_safe_point`（`reason=colocated_ambiguous`, `scope="target"`, `scope_ref=C.Ref`），`candidates` 列出冲突 `B`（按包含中心优先、重合率降序、面积升序、先序升序，至多 4 项，`relation` 按是否子节点标 `"child"` 或 `"overlapping"`）。
       - 若步 B 为空：
         - 当步 A 因 `A == nil` / `A` 不可用 / `A` 缺动作失败时，返回 `422 unsupported_node_action`（见 §4.4.2 精确 `scope`/`scope_ref` 表），同时收集虽超 `50%` 屏幕或其它重合的可点击节点并经 `buildCandidates` 真实干跑计算 `executable` 后收入 `candidates`；
         - 当步 A 的 `A` 自身有效但被竞争控件堵死时，返回 `422 no_safe_point`（`reason=blocked|ambiguous|owner_mismatch|mixed`, `scope="ancestor"`, `scope_ref=A.Ref`, `ancestor_ref=A.Ref`），`candidates` 优先列出与 `C` 重合/阻塞的可操作节点（干跑算 `executable`），`blocking_refs` 补充非本动作阻塞者与同深并列平局节点。
3. **有效 `bubble=false`**：
   - 不做祖先提升、不做共位吸附；若目标缺动作返回 `422 unsupported_node_action`（`reason=target_missing_action|target_has_other_action`, `scope="target"`, `scope_ref=C.Ref`），并在 `candidates` 中自动探测并附上最近可用交互祖先（`relation="ancestor"`）与共位可点击节点（`relation="overlapping"` / `"child"`）供调用方一眼取用。
4. **`text(ref)` 焦点点击**：
   - 删除 `staticTapRefCheckLegacy` 与 `planBubbleTapTargetLegacy`；调整 [act.go:297-298](../../../internal/engine/act.go#L297-L298)，使 `text(ref)` 先过统一 `invalid_target`（`root`/`context`/`no_geometry` + `candidates`）与 `409 node_disabled`，再做 `text` 的 `role=input && hasAction(text)` / `!protected` 检查（失败保持 `422 text_target_uncertain` + `stage/reason/dispatch`），最后复用本节第 1 条的直接 4 级 `tap` 规划（`bubble=false`，禁用 `coextensive_child`）：若该输入框无 `"tap"` 能力则报 `422 unsupported_node_action`（`reason=target_has_other_action`, `target_actions`），若焦点位置被阻则报 `422 no_safe_point` + `candidates`。

### 4.3 坐标目标“命中集合防漂移证明”与智能消歧（`point`）

1. **`resolvePointSet(tree, x, y)`**：返回公开树中所有非 `root`、非 `context`、正面积 bounds 包含 `(x, y)` 且处于**最大深度 `bestDepth`** 的公开节点切片 `S`（保持先序）。
2. **静态分支判据**：
   - `len(S) == 0`：`pointNone`，保持仅校验前台行为。
   - `len(S) == 1`：`pointUnique`，保持现有单节点证明。
   - `len(S) >= 2`：
     - **`swipe`（`from`）**：**永不返回 `422 ambiguous_point`**。当 `len(S) <= maxPointHitSet`（`64`）时接受为 `pointSet`（保存全部 `S` 节点的有效 raw 索引）；若极端页面 `len(S) > 64`，降级为 `pointNone`（保持前台级校验），保证常数有界且不阻断 `swipe`。
     - **`tap` / `long_press` / `text`**：
       - 若 `len(S) > maxPointHitSet`（`64`）：静态返回 `422 ambiguous_point`（`reason=competing_targets`, `scope="point"`），`candidates` 返回前 4 个拥有者/命中者。
       - 对 `S` 中每个节点向根回溯（跳过 `context`、止于 `root` 前）查找其最近触控交互拥有者（含自身，看 `isTouchInteractive`），去重为集合 `U`：
         - 若 `len(U) <= 1`（例如“1 个按钮 + 1 个同深度纯装饰图标”或“同一按钮下两个重叠文本节点”）：不存在交互歧义，**接受为 `pointSet`**。
         - 若 `len(U) >= 2`：过滤出具备本次请求能力且可操作的拥有者子集 `U_match`（`tap`/`long_press` 要求 `validTapPublicTarget`；`text` 要求 `role=input`、`hasAction(text)`、`enabled!=false` 且 `protected==false`）：
           - **若 `len(U_match) == 1`**：**唯一匹配动作消歧通过**，接受为 `pointSet`（`mode=point_disambiguated`）。
           - **否则（`len(U_match) >= 2` 或 `len(U_match) == 0` 且存在 ≥2 竞争交互拥有者）**：静态拒绝 `422 ambiguous_point`（`reason=competing_targets`, `scope="point"`），`candidates` 列出竞争拥有者（优先取 `U_match`，若空取 `U`，`relation="competing"`，按 `executable=true` 优先、裁剪面积升序、先序升序，至多 4 项）。
3. **认领后新鲜帧重验（`provePointTarget`）**：
   - **先导校验**：对 `point.state == pointUnique || point.state == pointSet` 均先运行 `validateTapRawTopology(fresh)`（畸形返回 `503 hierarchy_unavailable`），并在 `lineageOf`（[act.go:1657-1663](../../../internal/engine/act.go#L1657-L1663)）增加索引越界/环 visited 守卫，防止畸形环无限循环。
   - **Projector 一致性**：修复 [act.go:847](../../../internal/engine/act.go#L847) 忽略 `work.Projector` 的不一致——优先使用 `work.Projector`，`nil` 时回退 `e.projector`，均无或 `Preview` 失败则返回 `503 hierarchy_unavailable`。
   - **集合相等证明**：在 `act.go:477`（重验入口）与 `act.go:136`（事件日志）均对 `pointUnique` 和 `pointSet` 统一接入 `provePointTarget`：对 `point.indices` 中每个观察 raw 节点索引运行 `matchFreshNode(fresh, obsCapture, obsRaw)`，要求均唯一匹配到互不相同的新鲜 raw 索引组成 `WantSet`；在 `Preview(fresh)` 公开树上运行 `resolvePointSet(fp.Tree.Root, x, y)` 得新鲜映射索引集合 `FreshSet`（每一命中均有有效映射且数量与 `WantSet` 一致）。**当且仅当 `FreshSet == WantSet`（无序集合严格相等）**时通过；任何增删浮层、深层新节点、移位或失配返回 `409 target_changed`（`reason=point_changed`；`text` 由 `decorateTextRevalidateError` 按 §4.4.3 覆盖为 `text` 专属详情）。

### 4.4 统一 `act` 错误返回体系

#### 1. 静态拒绝 `422` 统一码表（全 `act` 分支通用）

除参数语法校验（`invalid_request` / `invalid_coordinates` / `unsupported_key` / `text_too_long`）及节点存在/禁用状态（`409 node_not_found` / `409 node_disabled`）外，全 `act` 静态目标解析与规划拒绝统一收口为以下 **5 个正交 `422` 错误码**（`outcome=not_executed`、不认领 `O`、零设备 I/O）：

| 错误码 | 适用分支 | 唯一语义 | `reason` 封闭枚举 |
| --- | --- | --- | --- |
| `invalid_target` | `tap` / `long_press` / `text(focus)` / `scroll` | 对象结构或几何本身不可用 | `root` / `context` / `no_geometry` / `no_scroll_container` |
| `unsupported_node_action` | `tap` / `long_press` / `text(focus)` / `scroll` | 目标有效，但自身与合法关联对象均不具备本次动作能力 | `target_missing_action` / `target_has_other_action` / `not_scrollable` / `ancestor_missing_action` / `ancestor_invalid` / `no_interactive_ancestor` |
| `ambiguous_point` | 坐标 `tap` / `long_press` / `text` / `scroll` | 坐标落在 ≥2 个真正竞争的可操作目标上，无法唯一消歧 | `competing_targets` / `competing_scroll_containers` |
| `no_safe_point` | `tap` / `long_press` / `text(focus)` | 目标具备能力（或共位冲突），但未能证明安全落点 | `blocked` / `ambiguous` / `owner_mismatch` / `mixed` / `colocated_ambiguous` |
| `no_safe_trajectory`（新增） | `scroll` | 滚动容器合法，但 3 条轴线均无满足最小行程的安全轨迹 | `trajectory_blocked` / `insufficient_travel` |

*注：`text` 自身的非可编辑/受保护拒绝继续使用 `422 text_target_uncertain`（保留 `stage/reason/dispatch` 契约）；仅 `text(ref)` 的静态结构几何/无 `tap` 能力/焦点阻塞使用上表。*

#### 2. 极简 `error.details` 契约、`scope`/`relation` 全表与旧键清理

- **废除的 9 个历史键（不再于任何 `act` 出口产生）**：`occluding_ref`、`occluding_refs`、`ambiguous_refs`、`owner_refs`、`nearest_actionable_ref`、`safe_point`、`safe_carrier_ref`、`suggested_point`、`suggested_carrier_ref`。
- **统一后的静态拒绝 `error.details` 白名单字段**：

| 字段 | 类型 | 出现规则 | 说明 |
| --- | --- | --- | --- |
| `reason` | `string` | 上述 5 码静态出口必出 | 封闭枚举，机器分支主依据 |
| `scope` | `string` | 上述 5 码静态出口必出 | `"target"` / `"ancestor"` / `"point"` |
| `scope_ref` | `string` | `scope` 为 `"target"` 或 `"ancestor"` 时必出；`"point"` 时省略 | 实际检查/规划对象的公开 `ref` |
| `target_actions` | `[]string` | `unsupported_node_action` 必出 | 按 `tap, long_press, text, swipe` 排序去重；空数组输出 `[]` |
| `ancestor_ref` | `string` | `ancestor_missing_action` / `ancestor_invalid` / `scope="ancestor"` 的 `invalid_target` 或 `no_safe_point` 时必出 | 提升祖先的 `ref` |
| `ancestor_actions` | `[]string` | `ancestor_missing_action` / `ancestor_invalid` 时必出 | 祖先完整公开动作列表，空输出 `[]` |
| `candidates` | `[]map[string]any` | 有可操作或竞争替代对象时出现（至多 4 项）；无候选时省略 | 运行时**必须**用 `[]map[string]any` 承载（不可放结构体切片），保证 `CloneErrorDetails` 深拷贝 |
| `blocking_refs` | `[]string` | 存在不具备请求动作的阻塞边界（`context`/他类动作节点/`ambiguous` 同深并列非交互节点）时出现（至多 3 项） | 纯解释阻塞或歧义来源 |

**全部 422 静态拒绝出口 → `scope` / `scope_ref` / `Candidate.relation` 精确映射表**：

| 静态拒绝出口 | `scope` | `scope_ref` | `candidates[].relation` 取值 |
| --- | --- | --- | --- |
| `invalid_target`（`root`/`context`/`no_geometry` 目标本身） | `"target"` | 原目标 `ref` | 若缺几何：`ancestor` / `overlapping` / `page_scroll` |
| `invalid_target`（`no_geometry` 祖先几何无效） | `"ancestor"` | 祖先 `A.Ref`（兼填 `ancestor_ref`） | `page_scroll`（`scroll`）或无 |
| `invalid_target`（`no_scroll_container`） | `"point"` | 省略 | `page_scroll` |
| `unsupported_node_action`（`target_*` / `not_scrollable` / `no_interactive_ancestor`） | `"target"` | 原目标 `ref` | `ancestor` / `overlapping` / `child` / `page_scroll` |
| `unsupported_node_action`（`ancestor_missing_action` / `ancestor_invalid`） | `"ancestor"` | 祖先 `A.Ref`（兼填 `ancestor_ref`） | `overlapping` / `child` / `page_scroll` |
| `ambiguous_point`（`competing_targets` / `competing_scroll_containers`） | `"point"` | 省略 | `competing`（加上可选 `ancestor` 共同回退祖先） |
| `no_safe_point`（直接目标或 `colocated_ambiguous`） | `"target"` | 目标 `C.Ref` | `child` / `overlapping` |
| `no_safe_point`（`bubble_ancestor` 失败） | `"ancestor"` | 祖先 `A.Ref`（兼填 `ancestor_ref`） | `overlapping` / `child` |
| `no_safe_trajectory` | `"target"` | 容器 `want.Ref` | `child`（子树内滚）/ `overlapping`（外部阻塞滚） |

**`Candidate` 对象固定字段（绝不含 `label`/`value`/`text` 正文）**：

```json
{
  "ref": "n5232",
  "role": "button",
  "actions": ["tap"],
  "bounds": [915, 1675, 1028, 1787],
  "relation": "overlapping",
  "executable": true
}
```

- `bounds`：固定 `[]int`（`[left, top, right, bottom]`，屏内裁剪后公开矩形）。
- `relation`：封闭枚举 `"child"` | `"ancestor"` | `"overlapping"` | `"competing"` | `"page_scroll"`。
- `executable`：服务端在当前同一 `O` 上先对入围对象去重并按 `relation` 专属优先级排序截取前 `16` 项，逐项调用**单目标基础规划器（禁止递归调用 `buildCandidates`）**计算出的真实可行性（对 `text` 目标须校验 `role=input`、`hasAction(text)`、`enabled!=false`、`protected==false` 及 4 级焦点 `tap`；对 `tap`/`long_press` 须校验 `validTapPublicTarget` 及 4 级选点；对 `scroll` 须校验 `isScrollContainer` 及 3 轴居中规划），最终按 `executable=true` 优先截取前 4 项返回，保证调用方取首个 `executable: true` 的 `ref` 在同 `O` 有效期内重试**必定通过静态准入**。

#### 3. `409 target_changed` 归因 `details.reason` 与优先级判定表

非 `text` 动作在认领后实时重验返回 `409 target_changed` 时，按以下精确分支顺序附加 `details.reason`（不额外读设备；原始/公开拓扑损坏一律先返回 `503 hierarchy_unavailable`）：

| `reason` | 精确触发分支与优先级 |
| --- | --- |
| `child_changed` | 在 `bubble_ancestor` / `bubble_colocated` 中，原触发子节点 `C` 的 `probeActNode` 返回 `None`/`Ambiguous`、公开 Role/完整 Label 变化、变成 `isTouchInteractive`、未映射公开节点，或与 `B` 失去共位与反底板守卫（按 §4.2.2 判据） |
| `target_missing` | `matchActTarget` 返回 `actProbeNone` 且 `lineageMismatch == false`（适用于直接 `tap`/`long_press`、`scroll` 容器、及 `C` 通过后的 `A`/`B`） |
| `target_ambiguous` | `matchActTarget` 返回 `actProbeAmbiguous`（适用于直接目标、`scroll` 容器、及 `C` 通过后的 `A`/`B`） |
| `lineage_changed` | `matchActTarget` 返回 `actProbeNone` 且 `lineageMismatch == true`（出向扫描存在结构与几何均兼容、仅因祖先链或强锚点被拒的节点）；或 `bubble_ancestor` 中 `nearestInteractiveAncestor` 变为其他节点 |
| `capability_changed` | 节点匹配成功，但在新鲜公开树中缺失映射、`validTapPublicTarget` / `isScrollContainer` 失效、`role` 变化、`bubble_colocated` 中 `B` 变为 `C` 祖先，或 `coextensive_child` 中 `B` 失去 `C` 顶层唯一子边界/双向同视口关系 |
| `scroll_scope_changed` | 滚动容器几何超出 `sameScrollScope` 容差（离轴边变化或轴向重叠 < 0.50） |
| `trajectory_blocked` | 滚动容器与作用域有效，但原轨迹失效且 3 条轴线均无法在新鲜帧重规划出可证安全段 |
| `scope_blocked` | 目标（及 `coextensive_child` 的 `B`）匹配且能力有效，但固定点、固定载体（含 `carrierRaw != nil && !match.independentContent` 门禁）及兜底安全区均未通过安全谓词 |
| `point_changed` | 坐标 `pointUnique` / `pointSet` 在新鲜帧中的最深命中集合或映射不再与期望集合相等 |

*注：对于 `req.Operation.Type == "text"`，[act.go:1346-1361](../../../internal/engine/act.go#L1346-L1361) 的 `decorateTextRevalidateError` 须把重验 `CodeTargetChanged` 的详情替换为 `text` 专属的 `stage`/`reason`（`target_unconfirmed`）/`dispatch` 结构（不再因前序详情非空而早退），确保不破坏既有 `text` 状态机解析。*

#### 4. 受控中文错误消息与 MCP 指引同步

- 为每组 `code` + `reason` 提供简洁清晰的受控中文 `message`，绝不夹带节点文本或标签。
- 同步改写 [schema.go](../../../internal/mcp/schema.go) 中的 `callerGuidance`、`act` 工具描述与 `bubble`/`container` 参数描述：删除对 `nearest_actionable_ref`、`safe_point`、`suggested_point`、`suggested_carrier_ref` 与旧保守禁忌的引用，明确告知模型：
  1. 滚动支持同视口嵌套栈直传、非滚动节点自动向上找滚动容器、轨迹自动居中避开顶底边；
  2. 点击支持 3×3 自动选点、`bubble` 视觉锚定并自动吸附唯一局部共位按钮；
  3. 遇到静态 422（`invalid_target`/`unsupported_node_action`/`ambiguous_point`/`no_safe_point`/`no_safe_trajectory`）时，直接取 `error.details.candidates` 中第一个 `executable=true` 的 `ref` 用同一 `O`（新 `P`）重试，无需重观察或手算坐标。

### 4.5 `Result` 投影、所有权与边界保护

- `protocol.CloneErrorDetails` 递归深拷贝 `[]map[string]any`、`[]string`、`[]int`；在 `internal/protocol/errors_test.go`、`decode_response_test.go`、`internal/httpapi/app_test.go`、`internal/client/client_test.go`、`internal/mcp/result_manifest_test.go`、`internal/mcp/result_parity_test.go` 中用含 `candidates` 的真实嵌套切片更新所有权与 HTTP/MCP `result v4` 往返测试（注意 Go HTTP 客户端解码 `bounds` 后在 `map[string]any` 内数字为 `float64`，整数值精确往返）。
- `Result` 顶层字段顺序、普通树 `tree v4`、`inspect v3` 语法和 `result v4` 编码器（`error.details` 按 `v4JSON` 原样编码）保持不变。

## 5. 算法

### 5.1 滚动静态规划与新鲜重验

```text
resolveScrollPlan(obs, op):
  view = collectTapPublicView(obs); topology = validateTapRawTopology(obs.Capture) // 损坏->503
  if op.Container.UseRef:
    R = findPublicNodeBounded(Root, Ref) // 不存在->409 node_not_found
    check root/context/no_geometry/disabled
    if isScrollContainer(R): want = R; allowInnerFallback = true
    else if R.Role == RoleScroll: -> 422 unsupported_node_action(target_missing_action, scope=target)
    else: lift R -> nearest RoleScroll ancestor A (check valid/enabled/swipe/rawClip); want = A
  else: // coordinate container
    if !inScreen: -> 422 invalid_coordinates
    H = all valid isScrollContainer nodes containing (x, y)
    if H empty: -> 422 invalid_target(no_scroll_container, scope=point, candidates=pageScrolls)
    I = maximal-depth elements in H (drop any ancestor present in H)
    if len(I) == 1:
      want = I[0]; if fail -> walk hit ancestors in H as fallback; if all fail -> 422 no_safe_trajectory
    else:
      I_exec = [c in I where tryPlanScrollBase(c) succeeds]
      if len(I_exec) == 1: want = I_exec[0]
      else: -> 422 ambiguous_point(competing_scroll_containers, scope=point, candidates=I + commonScrollAncestors)

  tryPlanScrollWithAxes(want):
    build raw ancestors + bidirectional coextensive descendants (>=85% W & H mutual overlap)
    for coord in orthogonalFractions(clipped, [1/2, 1/4, 3/4]) (deduped & inside shared):
      spans = scrollSafeSpansAt(projection, want, clipped, direction, coord)
      best = longestSpan(spans)
      start, end, ok = scrollGeometry(safeRect, screen, dir, amt, coord) // centered at mid
      if ok && spans.provesSegment(start, end, clipped, screen): return scrollPlan
    if allowInnerFallback:
      if unique collapsed executable non-coextensive descendant D in want:
        return scrollPlan(D, mode=scroll_inner_fallback)
    return 422 no_safe_trajectory(insufficient_travel or trajectory_blocked, candidates, blocking_refs)

revalidateScrollTarget(work, obs, plan):
  validateTapRawTopology(fresh) // 失败->503 hierarchy_unavailable
  match container via matchActTarget -> None: lineageMismatch? lineage_changed : target_missing; Ambiguous: target_ambiguous
  check sameScrollScope (-> scroll_scope_changed) & valid fresh isScrollContainer (-> capability_changed)
  compute fresh spans with bidirectional coextensive rule:
    if provesSegment(plan.start, plan.end): keep segment
    else replan across [50%, 25%, 75%] with centered scrollGeometry(coord)
    if all fail: -> 409 target_changed(reason=trajectory_blocked)
```

### 5.2 点击/长按/聚焦静态规划与共位重验

```text
planDirectTap(obs, view, C, action, allowCoextensiveChild):
  try 4 tiers: default_center -> carrier_node -> center_span -> grid_3x3 (all provesSubtreeTapPoint)
  if ok: return tapPlan
  if allowCoextensiveChild && unique top-level enabled blocker B inside C:
     if B supports action && isCoextensive(B, C) && planDirectTap(B, false) succeeds:
       return tapPlan(targetRaw=C.raw, delegateRaw=B.raw, mode=coextensive_child)
  return 422 no_safe_point(reason, scope=target, scope_ref=C, candidates=blocking action children, blocking_refs=other/tied blockers)

planBubbleOrColocated(obs, view, C, action):
  A = nearestInteractiveAncestor(view, C)
  if A != nil && validTapPublicTarget(A, action):
    cClip = rect(C) ∩ A.base
    if !cClip.Empty():
      if safe point found in cClip (center/lines/3x3) via provesGuidancePoint:
        return tapPlan(C, A, mode=bubble_ancestor)
      if !hasCompetingControlOverlap(view, A, cClip):
        if safe point found in A's 4 tiers via provesGuidancePoint:
          return tapPlan(C, A, mode=bubble_ancestor)
    // cClip empty OR competing control overlaps probeRect -> stop A drift, run Step B!

  // Step B: co-located local target B
  collect B != C where !isAncestor(B, C) && validTapPublicTarget(B, action):
    if !isDescendant(B, C): require (B.Contains(cCenter) || overlap >= 50% C) && clippedArea(B) < 50% screen
    else: require isCoextensive(B, C) || (overlap >= 50% C && clippedArea(B) < 50% screen)
    require direct 4-tier tap plan on B succeeds (preferring points inside C ∩ scopeB.base first)
  collapse bidirectional co-extensive ancestor-descendant pairs in matched Bs
  if len == 1: return tapPlan(targetRaw=C.raw, colocatedRaw=B.raw, mode=bubble_colocated)
  if len >= 2 && exactly one candidate contains cCenter: return tapPlan(C, B, mode=bubble_colocated)
  if len >= 2: return 422 no_safe_point(colocated_ambiguous, scope=target, scope_ref=C, candidates=Bs)
  if A issue exists: return 422 unsupported_node_action(scope, scope_ref, ..., candidates=dryRun(overlapping))
  return 422 no_safe_point(scope=ancestor, scope_ref=A, ancestor_ref=A, candidates=overlapping/blocking, blocking_refs)

revalidateCoextensiveChildFrame(C, B):
  validateTapRawTopology(fresh) -> 503
  match C (-> target_missing/ambiguous/lineage_changed), match B (-> target_missing/ambiguous/lineage_changed)
  require validTapPublicTarget(freshC) && validTapPublicTarget(freshB) (-> capability_changed)
  require freshB is top-level blocking boundary of freshC && isCoextensive(freshB, freshC) (-> capability_changed)
  prove fixed point or replan inside freshB's direct scope (-> scope_blocked)

revalidateBubbleAncestorFrame(C, A): // 禁止跨模式切回 bubble_colocated
  match C (-> child_changed on None/Ambiguous/Role/Label/touch-action); match A (-> target_missing/ambiguous/lineage_changed)
  require nearestInteractiveAncestor(freshC) == freshA (-> lineage_changed) && validTapPublicTarget(freshA) (-> capability_changed)
  run visual-anchored chooseBubbleTapPoint(freshC, freshA) (-> scope_blocked)

revalidateColocatedTapFrame(C, B):
  match C (-> child_changed on None/Ambiguous/Role/Label/touch-action); match B (-> target_missing/ambiguous/lineage_changed)
  require !isAncestor(freshB, freshC) && validTapPublicTarget(freshB) (-> capability_changed)
  require co-location & anti-backdrop guard hold between freshC and freshB (-> child_changed)
  if plan.x,plan.y valid in freshB scope (and inside freshC ∩ scopeFreshB.base when originally in C): keep
  else replan in freshC ∩ scopeFreshB.base first, then freshB's 4 tiers (-> scope_blocked)
```

### 5.3 坐标解析与 `buildCandidates`

```text
resolveObservedPoint(obs, op):
  view = collectTapPublicView(obs); topology = validateTapRawTopology(obs.Capture) // 损坏->503
  S = resolvePointSet(Root, x, y)
  if len(S) == 0: return pointNone
  if op.Type == "swipe":
    if len(S) > 64: return pointNone // 永远不对 swipe 返回 ambiguous_point
    return pointSet(indices(S))
  if len(S) > 64: return 422 ambiguous_point(competing_targets, scope=point, candidates=top4)
  owners = unique nearest isTouchInteractive (self or ancestor) for nodes in S
  if len(owners) <= 1: return pointSet(indices(S))
  matching = [u in owners matching op capability & enabled/unprotected]
  if len(matching) == 1: return pointSet(indices(S), mode=point_disambiguated)
  return 422 ambiguous_point(competing_targets, scope=point, candidates=rank(matching or owners))

buildCandidates(baseList, op):
  // Non-recursive: calls single-target base planner only; deduplicate & sort, then cap at 16 dry-runs
  evaluate executable via base planner without calling buildCandidates
  sort by: executable desc, relation-specific tie-breakers, preorder asc
  truncate to max 4 items; construct []map[string]any without label/text/value
```

### 5.4 时序、撤销、预算与失败边界

```text
t0 observe：T_obs，O ready
t1 act 静态（锁外纯 CPU，零 I/O）：
   - 有界拓扑/映射损坏 -> 503 hierarchy_unavailable，不认领 O
   - 静态 422/409（含 candidates 预演）-> outcome=not_executed，O 仍 ready
   - 解析出 scrollPlan / tapPlan / pointCheck -> 进入 t2
t2 acquireWork -> claimObservationLocked：O ready -> claimed
t3 Capture + validateTapRawTopology(T_new)
t4 集合/双绑定身份与祖先/作用域重验（不推进保号 Preview）-> 失败 409 target_changed(reason) + 可交付新 O
t5 dispatch：注入一次 Tap / LongPress / Swipe / Text focus -> 后观察提交
```

- **静态拒绝路径**：完全处于 `e.mu` 锁外、零设备 `ProbeDisplay`/`Capture`/`Tap`/`Swipe` 调用、不调用 `acquireWork`、不认领 `O`。原 `O` 若未因并发、过期或撤权失效则保持 `ready`（`recovery=retry/O/effective`）。
- **实时重验失败路径**：沿用既有一读帧与 `deliverRejection` 白名单门禁（仅 `target_changed`/`foreground_changed`/`node_not_found` 可交付已读帧；`503 hierarchy_unavailable` 与 STOP 撤销不交付新 `O`）。
- **STOP 与期限**：`matchActTarget`、`scrollSafeSpansAt`、候选干跑与 `provePointTarget` 均受既有树规模边界与 `work.Ctx` 取消门禁保护；STOP 优先撤权并拒绝迟到候选提交，不等待本地规划。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/protocol/errors.go` | 新增 `CodeNoSafeTrajectory = "no_safe_trajectory"` 及其 HTTP 422 映射；更新 `APIError.Details` 白名单注释为统一词汇表 |
| `internal/engine/scroll.go` | 接入统一有界拓扑守卫、实现双向 `isCoextensive`、同视口免阻塞、非滚动 `ref` 提升、坐标最内层/回退/消歧、居中 `scrollGeometry(orthogonalCoord)`、3 轴线探测、唯一内层子容器兜底、422 候选与 `target_changed` `reason` |
| `internal/engine/tap_scope.go` | 4 级直接规划（含 `grid_3x3`）、`coextensive_child`、视觉锚定 `bubble_ancestor`、`bubble_colocated`、统一 `candidates`/`blocking_refs` 构造；删除 `staticTapRefCheckLegacy` 与 `planBubbleTapTargetLegacy`，`text(ref)` 焦点接入新规划器 |
| `internal/engine/point.go` | 接入统一有界拓扑守卫，实现 `resolvePointSet`、`swipe` 免歧义、`tap`/`long_press`/`text` 唯一动作拥有者消歧与 `422 ambiguous_point` `candidates` |
| `internal/engine/act.go` | 更新 `act.go:136, 297-298, 477, 834-860, 1657-1663`：接入 `pointSet` 集合重验（先拓扑校验与加环守卫 `lineageOf`，优先 `work.Projector`）、`coextensive_child` 与 `bubble_colocated` 双绑定重验、非 `text` 的 `target_changed` `reason` 附着；`decorateTextRevalidateError` 替换并保留 `text` 专属结构 |
| `internal/engine/act_revalidation_policy.go` | 在 `tierMatcher` 记录结构与几何兼容但被祖先链/强锚点拒绝的 `lineageMismatch` 信号 |
| `internal/mcp/schema.go` | 重写 `callerGuidance`、`act` 工具描述及 `bubble`/`container` 参数描述 |
| `internal/engine/*_test.go`、`internal/protocol/*_test.go`、`internal/httpapi/app_test.go`、`internal/client/client_test.go`、`internal/mcp/*_test.go` | 更新旧保守断言与旧细节键测试（含 `decode_response_test.go`、`httpapi/app_test.go`）；新增全场景先红后绿测试与反向对照；校验 `CloneErrorDetails` 与三模式/取消 `result v4` 保真 |
| `scripts/mcp-probe/*` | 若涉及目录或错误断言则同步更新 |
| `docs/interface.md`、`docs/architecture.md`、`docs/mcp.md`、`docs/integration.md`、`docs/release.md`、`docs/runbook.md`、`docs/phone-operations.md`、`docs/control-protocol.md`、`docs/client.md`、`README.md` | 按代码实际落点同步契约、规则、错误表、MCP 指引与版本号/目录指纹 |
| `release/versions.json` 及生成文件 | 统一由 `scripts/generate-version.ps1` 推进至 `0.26.0` / `contract_version=22` / `android_version_code=28` |

### 6.2 配置与取值

无新增请求字段或外部配置项。以下工程常量锁定在 `internal/engine` 内：

| 常量 | 取值 | 依据与作用 |
| --- | --- | --- |
| `coextensiveMinPercent` | `85` | 双向至少 85% 宽与高重合，兼容包装 padding 差异同时防止越界子容器与半屏分栏误判 |
| `colocatedMinOverlapPercent` | `50` | 共位按钮至少覆盖原无动作目标中心或其一半面积 |
| `colocatedMaxScreenPercent` | `50` | 防止跨兄弟共位误吸半屏以上背景面板 |
| `scrollOrthogonalFractions` | `[1/2, 1/4, 3/4]` | 固定 3 轴线探测，兼顾居中优先与局部中柱避让 |
| `maxStaticCandidates` | `4` | 兼顾决策覆盖率与 128 KiB 响应预算，预演干跑上限 `16` 个入围者 |
| `maxBlockingRefs` | `3` | 在阻塞者无法进 `candidates` 或同深平局时解释原因 |
| `maxPointHitSet` | `64` | 限制坐标集合防漂移重验的节点上限 |

### 6.3 日志与可观测性

- 复用 `internal/eventlog/log.go` 现有白名单事件与字段（`act.point`、`act.tap_plan`、`scroll.plan`、`scroll.revalidate`），利用既有键 `mode`、`state`、`proven`、`reason`、`ms` 记录新策略分流（如 `scroll_direct`、`scroll_coextensive`、`scroll_ancestor_lift`、`scroll_point_innermost`、`scroll_point_fallback`、`scroll_point_disambiguated`、`scroll_inner_fallback`、`grid_3x3`、`coextensive_child`、`bubble_ancestor`、`bubble_colocated`、`point_set`、`point_disambiguated`）以及 `target_changed` 的 `reason`。
- 不新增任何含 `ref`、坐标、矩形、标签或树内容的日志键；`candidates` 只进受控错误响应，不写入事件日志或恢复账本。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威 | 全部分析、消歧、居中计算与预演都在 `internal/engine` 完成；客户端/MCP/App 不重算 |
| STOP 不等待 | 全部静态规划、有界拓扑环守卫与 `executable` 预演在 `e.mu` 锁外纯 CPU 有界执行，零设备 I/O |
| O 单向消费 | 静态 422 拒绝不取工作位、不认领 `O`；认领后重验失败照常不归还 `O` |
| 状态锁短小 | 遍历、共位筛选、集合重验与 `Preview` 全部在 `e.mu` 锁外执行 |
| 事实分层 | 自动选点/吸附只解决“把输入打到哪”，不把 `executed` 伪称为业务成功或滚到底 |
| 保号连续性 | 所有新鲜帧树投影只使用不推进 `Builder.prev`/`nextRef` 的 `Projector.Preview` |
| 日志白名单 | 只记录封闭枚举/布尔/耗时，绝不记录 ref、坐标或节点文本 |
| 单一版本源 | 仅在 `release/versions.json` 手改版本号，Go/Java/XML 由 `scripts/generate-version.ps1` 生成 |

### 6.5 契约与文档影响

- 共同契约由 `21` 升至 `22`，配套建议 `release_version="0.26.0"`、`android_version_code=28`。Android 控制 App 无业务代码改动，但随共同契约同步重编 APK 与发布清单，保持端侧/主机整组版本精确相等。
- 实施前须在 `docs/` 中检索并同步所有受影响文档（包含 `docs/integration.md`）；涉及 `docs/` 的变更在提交前必须交由**独立、只读**的文档审计 subagent 审核并取得 clean 结论（`AGENTS.md` #15）。

## 7. 预期效果与能力边界

### 7.1 能治什么

- **Issue #27 彻底解决**：知乎 `n56 > n57 > n66` 同矩形滚动容器从任何一层发起 `scroll` **1 次直接成功**；问题页 `n3581` 居中起滑后自动避开屏幕底端固定工具栏与系统手势区，同时 `50%/25%/75%` 三线扫描避开局部中柱；若对非滚动列表项 `ref` 调 `scroll` 也能自动上提至所在滚动容器。
- **Issue #28 彻底解决**：同视口或子滚动坐标 `scroll` **1 次直接选中对应最内层容器成功**；图标+装饰叠放的 `tap`/`long_press` 坐标点击通过命中集合证明 **1 次直接成功**；raw `swipe` 彻底告别 `ambiguous_point`；遇到真正平级竞争按钮或竞争滚动区时，同一次 `422` 直接给出按可执行性排好序的 `candidates`（含 `ref`/`role`/`actions`/`bounds`/`executable`），零重观察 1 轮纠错。
- **Issue #31 彻底解决**：网易云 `"999+"`（`n5233`）默认点击不再向播放大容器 `n5223` 盲目漂移，而是自动识别唯一局部共位评论按钮 `n5232` 并双绑定执行，**1 次成功**；多按钮共位冲突时直接拒绝并在 `candidates[0..N]` 按重叠优先级列出候选。
- **`act` 债务清零**：全分支静态 422 收口为 5 码与 1 套极简 `details` 契约，删除过期 legacy 副本，让 `text(ref)` 焦点与 `tap` 同轨，并使 `409 target_changed` 具备清晰排障归因。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 跨同级或跨窗口浮层遮挡 `ref` 目标（z-order 未知） | Android 无障碍树无公开同级 z 序与未发布透明拦截层，直接 `ref` 仍按子树作用域防误伤，只保树模型内安全 |
| 内层局部滚动容器只支持横向，却收到坐标纵向 `scroll` | uiautomator 仅暴露 `Scrollable bool`，不暴露滚动轴方向；若该内层容器不参与 Android NestedScrolling 且吃掉垂直触摸，可能返回 `executed + effect=not_observed`，此时需改传外层 `ref` 或避开该子控件坐标 |
| “弱保软”自动选点与调用方隐藏意图偶发不一致 | 当画面同时存在多个重叠局部可点击控件而其中一个独占中心、或父级是普通包装容器时，自动吸附/最内层选择会直接执行；调用方需通过后观察事实核对业务结果 |
| 纯 Canvas/WebView 内部控件或空树点击 | 无公开子节点可解析时保持前台级校验，不具备节点级防错能力 |
| #30（`text` 写入确认但回车不可确认）、#34（图标开关无可读状态）、#36（Tier 3 祖先链重建放宽） | 分别涉及 `text_input` 状态机、无障碍属性能力与派发前身份放宽边界，明确留待各自独立提案 |

### 7.3 成本

- **设备 I/O**：零额外设备读取；静态解析与候选干跑全部基于已持有的不可变投影完成。
- **CPU/内存**：正常路径增加少量树扫描与共位过滤（同视口/共位集合均为 `O(N)` 线性遍历，`N` 为单帧公开节点数）；静态拒绝路径最多对 `16` 个入围候选调用单目标基础规划器，有界可控。
- **发布成本**：破坏性契约升级（`21 → 22`）需重编发布全套产物（含手机控制 APK/服务/MCP 与 Windows 主机 `device-mcp.exe`/`mcp-probe.exe`），并同步更新旧保守单元测试。

## 8. 验证计划

### 8.1 单元测试

所有新行为在实施前必须先写红测试，确认基线复现失败后再实现转绿，并保留反向对照：

| 用例组 | 可观测验收与反向对照 |
| --- | --- |
| 滚动同视口栈与局部避让（#27） | 构造 `n56 > n57 > n66` 同矩形嵌套，调外层 `n56` 一次 `executed`（**去掉同视口放行则 422 失败**）；构造 `clipChildren=false` 越界全屏子控件验证双向 85% 拒绝误判；同时构造含顶部 Tab 栏/中部横滑卡片的列表验证局部避让；50%/50% 半屏拼装容器静态拒绝并返回 `no_safe_trajectory` + `candidates` |
| 滚动居中、3 轴线与祖先提升 | `n3581 [0,1458,1080,2400]` 验证 `down` 轨迹居中为 `(540,2155)→(540,1703)`；中心线被窄中柱阻塞时切换到 `25%`/`75%` 轴线且 `scrollGeometry` 保持所选轴线坐标不回弹 50%；非滚动子节点 `container.ref` 自动提升到滚动祖先成功且跳过列表项身份校验；`role=scroll` 缺 `swipe` 报 `unsupported_node_action` + `candidates` |
| 坐标滚动（#28） | 嵌套同矩形栈坐标 `scroll` 一次命中内层成功；列表内小横滑条点中时优先内层，内层行程不足时自动回退外层滚动祖先；平级交叉竞争容器中恰 1 个可执行时自动消歧，≥2 个可执行时报 `ambiguous_point` + `candidates`（含共同可行祖先） |
| 点击 4 级与同视口子容器 | 十字阻塞卡片经 `grid_3x3` **直接 `executed`**（反向对照：关闭 Tier 4 则退回 422）；仅含双向同视口同动作子容器的外壳 `C` 自动委托并双绑定重验成功；越界全屏子容器被双向守卫拦截 |
| 视觉锚定 `bubble` 与共位吸附（#31） | 构造网易云 `"999+"` (`n5233`) 压 `n5232` 场景（且 `n5223` 旁边故意留空白区！）：验证一次点中 `n5232` 而**绝不漂移到 `n5223` 空白处**（反向对照：去掉共位吸附与止漂守卫时要么误点大容器空白、要么 422）；验证 `n5232` 超过 `50%` 屏幕或小按钮位于半屏包装容器中心时不被误吸；验证多按钮共位冲突返回 `no_safe_point(colocated_ambiguous)` + 排序 `candidates`；验证新鲜帧 `C` 变文或分离触发 `409 target_changed(child_changed)`；验证 `bubble=false` 返回 `candidates` |
| `text(ref)` 去副本与独立保留 | 删除 `staticTapRefCheckLegacy` 后验证 `text(ref)` 的 `root`/`context`/无几何报 `invalid_target`，缺 `"tap"` 报 `unsupported_node_action`，焦点阻塞报 `no_safe_point` + `candidates`；非可编辑/受保护保持 `422 text_target_uncertain`；认领后 `409 target_changed` 仍保留 `text` 自有 `stage/reason/dispatch` |
| 坐标集合防漂移与消歧（#28） | 同深度按钮+装饰图标、同按钮内双文本均一次 `executed`；raw `swipe` 落在同深度重叠节点或 >64 节点上均不报 `ambiguous_point`；真双按钮叠放按动作唯一匹配自动消歧，≥2 匹配报 `422 ambiguous_point` + `candidates`；新鲜帧集合增加浮层/少节点/换节点均 `409 target_changed(point_changed)`；新鲜帧原始父链有环返回 `503 hierarchy_unavailable` |
| 错误契约、所有权、`result v4` 与 MCP | 校验 9 个旧键全链路归零；`CloneErrorDetails`、`decode_response_test`、`httpapi/app_test`、`client_test`、MCP `result_manifest_test`/`result_parity_test` 验证 `candidates`（`[]map[string]any`）在取消与三模式下无损往返；`target_changed` 9 种 `reason` 矩阵覆盖；`schema_test` 验证新 `act` 描述、指引与 `CatalogFingerprint` |

### 8.2 实机/系统级

遵循 `AGENTS.md` 第 19 条默认发布交付范围：

1. **版本生成与主机测试交叉编译校验**（PowerShell）：
   ```powershell
   ./scripts/generate-version.ps1
   ./scripts/generate-version.ps1 -Check
   $env:GOEXPERIMENT = 'jsonv2'
   go test ./internal/protocol/... ./internal/httpapi/... ./internal/engine/... ./internal/client/... ./internal/mcp/...
   $env:GOOS = 'linux'; $env:GOARCH = 'arm64'; $env:CGO_ENABLED = '0'
   go build ./...
   ```
2. **文档同步与独立文档审计**：修改 `docs/` 并交由独立只读文档审计 subagent 审核至 `clean`，先完成实现提交。
3. **干净提交整组构建、清单核验、手机部署与主机工具重建**：
   ```powershell
   ./scripts/build-release.ps1
   ./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
   ./scripts/deploy.ps1
   ./scripts/build-host-tools.ps1
   ./scripts/build-host-tools.ps1 -VerifyOnly
   ```
   手机整组覆盖部署保留 App 私有数据（不卸载、不清库）。
4. **独立探针与最小实机闭环**：
   - 运行本次新构建的 `build/mcp-probe.exe` 分别验证 Windows 主机 stdio MCP 与手机 HTTP MCP 入口及清单工具指纹/指令一致性。
   - 在真机上验证覆盖本次变更行为的最小闭环：含嵌套滚动列表的外层 `ref` 与坐标 `scroll`、含共位标签的图标/按钮点击、重叠坐标点与静态拒绝返回 `candidates` 后按 `candidates[0].ref` 直接同 `O` 重试成功。
   - **责任边界**：依赖该适配器的外部 Agent 宿主进程、其托管配置与重连由用户处理；验证记录须严格区分磁盘产物替换、独立探针验证与依赖方切换。

### 8.3 记录

实施与全部实机/探针验证完成后写入 `maintenance/verification/act-target-resolution-and-error-contract.md`，记录实现提交、环境、实际执行命令、红绿摘要与真机观察。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 总体策略 | 采纳“弱保软”：优先按确定性布局规律自动解析并在新鲜帧守住防漂移证明，确需拒绝时一次给足 `candidates` |
| 同视口覆盖阈值 | **双向** `85%` 宽且 `85%` 高重叠 |
| 共位吸附条件与反底板阈值 | 覆盖目标中心或交集 `>=50%`；外部共位须 `<50%` 屏；后代须双向同视口或（交集 `>=50%` 且 `<50%` 屏）；多中心包含者须唯一命中中心 |
| 滚动轨迹与轴线 | 安全区间正中对称展开；`scrollGeometry` 显式接收正交坐标；固定探测 `50% → 25% → 75%`；支持非滚动 `ref` 提升与直接容器唯一可执行内层子滚动兜底 |
| 3×3 网格定位 | 扶正为直接 `tap`/`long_press` 第 4 级执行候选，不再只用于报错提示 |
| 坐标目标证明 | 升级为最深命中集合 `pointSet`（上限 `64`）+ 唯一动作拥有者消歧；raw `swipe` 永不报 `ambiguous_point` |
| 静态拒绝码与旧键清理 | 统一 5 个正交 `422` 码（新增 `no_safe_trajectory`）；废除 9 个历史 `details` 键；统一 `candidates`（`[]map[string]any`，最多 4 项含 `executable`）+ 可选 `blocking_refs`（最多 3 项） |
| 过期副本与环守卫 | 彻底删除 `staticTapRefCheckLegacy` 与 `planBubbleTapTargetLegacy`；`scroll`/`point`/`lineageOf` 全部接入有界环与拓扑校验 |
| 动态重验归因 | 非 `text` 的 `409 target_changed` 附带封闭 `details.reason`；`text` 覆盖保留 `stage/reason/dispatch` |
| 契约与版本 | 共同契约升级 `21 → 22`（`0.26.0` / `android_version_code=28`） |
| 明确排除范围 | #30、#34、#36（Tier 3 祖先链重建放宽）不纳入本提案 |

### 9.2 未决

无悬空决策；待用户确认本提案后按 §10 实施。

## 10. 实施顺序

1. **协议与错误基础**：修改 `internal/protocol/errors.go` 新增 `CodeNoSafeTrajectory` 与白名单注释，更新 `internal/protocol/errors_test.go` 与 `decode_response_test.go` 验证 `candidates`（`[]map[string]any`）深拷贝、零旧键与状态码映射。
2. **先红用例建设**：在 `internal/engine` 新增复现 #27、#28、#31 及统一错误码的单元测试，确认在基线代码上先红。
3. **`scroll` 解析与重验**：修改 `internal/engine/scroll.go` 接入有界拓扑守卫、双向 `isCoextensive`、同视口免阻塞、非滚动 `ref` 提升、坐标最内层/回退/消歧、居中 `scrollGeometry(orthogonalCoord)`、3 轴线、唯一内层兜底、`no_safe_trajectory`、`candidates` 与 `target_changed` 归因。
4. **`point` 集合与消歧**：修改 `internal/engine/point.go` 与 `internal/engine/act.go`（`provePointTarget`、`lineageOf`、`act.go:136, 477`），实现 `resolvePointSet`、`swipe` 免歧义、唯一动作拥有者消歧、有界拓扑校验与集合相等重验（含优先 `work.Projector` 修复）。
5. **`tap`/`long_press`/`text(focus)` 与 `target_changed` 统一**：修改 `internal/engine/tap_scope.go`、`act.go` 与 `act_revalidation_policy.go`，扶正 `grid_3x3`、实现视觉锚定 `bubble` + `bubble_colocated` + `coextensive_child`、删除过期副本（含更新 `act.go:297-298`）、全量切换到 `candidates` 契约并接入 9 项 `target_changed` 归因。
6. **MCP 说明、测试与文档**：更新 `internal/mcp/schema.go`、`internal/httpapi/app_test.go`、`internal/client/client_test.go`、`internal/mcp/*_test.go`、`scripts/mcp-probe/*` 与全量受影响 `docs/`（含 `docs/integration.md`）；跑通全套单元测试与 Linux/arm64 交叉编译检查。
7. **版本冻结与文档审计**：更新 `release/versions.json` 为 `0.26.0` / `22` / `28` 并运行 `scripts/generate-version.ps1 -Check`；提交实现前将变更的 `docs/` 交由独立只读文档审计 subagent 审核至 `clean`。
8. **实现提交、整组构建部署与实机闭环**：提交实现与文档 → 在干净提交上执行 `build-release.ps1`、`verify-manifest.ps1`、`deploy.ps1`、`build-host-tools.ps1 -VerifyOnly` → 用新 `mcp-probe.exe` 分别验证主机 stdio 与手机 HTTP 入口 → 完成最小实机闭环。
9. **归档提交**：将本提案移入 `maintenance/proposals/implemented/`、更新相对链接、头部填入实现提交 hash 并补全 §11、写入 `maintenance/verification/act-target-resolution-and-error-contract.md` 并按需更新 `maintenance/status.md` 后单独提交归档。

## 11. 实现结果（归档时补写）

实现提交：`b305e23675d1b55c2f05ca8a904e579e400c72fd`（短 hash `b305e23`）。
实现与验证记录：[act-target-resolution-and-error-contract.md](../../verification/act-target-resolution-and-error-contract.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| §4.5 统一静态 `422` 码表与零旧键细节契约 | [errors.go](../../../internal/protocol/errors.go) 新增 `CodeNoSafeTrajectory` 与 422 状态映射、更新白名单注释；[act_target_shared.go](../../../internal/engine/act_target_shared.go) 统一构造经当前 `O` 干跑计算的 `candidates`（`[]map[string]any`，至多 4 项含 `executable`）与可选 `blocking_refs`（至多 3 项）；全链路移除 9 个历史引导/诊断键；[errors_test.go](../../../internal/protocol/errors_test.go)、[decode_response_test.go](../../../internal/protocol/decode_response_test.go)、[app_test.go](../../../internal/httpapi/app_test.go)、[client_test.go](../../../internal/client/client_test.go)、[result_manifest_test.go](../../../internal/mcp/result_manifest_test.go)、[result_parity_test.go](../../../internal/mcp/result_parity_test.go) 验证深拷贝与三模式/取消无损透传。 |
| §4.1 `scroll` 同视口栈、祖先提升、居中轨迹与坐标消歧 | [scroll.go](../../../internal/engine/scroll.go) 与 [act_target_shared.go](../../../internal/engine/act_target_shared.go) 接入有界拓扑环守卫、双向 `≥85%` `isCoextensive` 同视口免阻塞、非滚动 `ref` 最近 `role=scroll` 祖先提升、坐标最内层/祖先回退/唯一可执行消歧、安全区间正中对称 `scrollGeometry(orthogonalCoord)` 与 `50% → 25% → 75%` 3 轴线探测、唯一可执行内层子滚动兜底、`422 no_safe_trajectory` 与重验 `target_changed` 归因。 |
| §4.2 `tap`/`long_press`/`text(focus)` 与视觉锚定冒泡/共位吸附 | [tap_scope.go](../../../internal/engine/tap_scope.go) 与 [act.go](../../../internal/engine/act.go) 将 `grid_3x3` 扶正为第 4 级正常执行候选，新增同视口阻塞子容器委托（`coextensive_child`）、视觉锚定 `bubble_ancestor` 与反底板守卫 `bubble_colocated` 双绑定重验，彻底删除 `staticTapRefCheckLegacy` 与 `planBubbleTapTargetLegacy`，`text(ref)` 焦点接入统一校验与直接 4 级规划（禁用祖先提升/共位/子容器委托）。 |
| §4.3 坐标目标最深命中集合与 `swipe` 豁免 | [point.go](../../../internal/engine/point.go) 与 [act.go](../../../internal/engine/act.go) 接入有界拓扑环守卫与带环检查的 `lineageOf`，实现 `resolvePointSet`（上限 64）、raw `swipe` 永不报 `ambiguous_point`、唯一动作拥有者自动消歧与 `pointSet` 集合相等重验（优先使用 `work.Projector`）。 |
| §4.4 非 `text` `409 target_changed` 封闭归因 | [act.go](../../../internal/engine/act.go)、[act_revalidation_policy.go](../../../internal/engine/act_revalidation_policy.go)、[scroll.go](../../../internal/engine/scroll.go)、[tap_scope.go](../../../internal/engine/tap_scope.go) 落地 9 项封闭 `details.reason`；`decorateTextRevalidateError` 替换并保留 `text` 专属 `stage`/`reason=target_unconfirmed`/`dispatch` 结构。 |
| §4.6 MCP 说明与目录指纹 | [schema.go](../../../internal/mcp/schema.go) 更新 `callerGuidance`、`act` 工具描述及 `bubble`/`container` 参数说明，13 工具目录指纹更新为 `a18acde933ad4358ee0b6d5ec28ca2e4dcba643e33453de31c436763e561c54e`；[schema_test.go](../../../internal/mcp/schema_test.go)、[server_test.go](../../../internal/mcp/server_test.go) 同步回归。 |
| §6.1 版本源、正式文档与测试套件 | [versions.json](../../../release/versions.json) 推进至 `0.26.0` / 契约 `22` / `android_version_code=28` 并生成 Go/Java/XML 常量；同步更新 `README.md` 与 9 份 `docs/` 正式文档并通过独立只读文档审计 `CLEAN`；新增 4 个提案测试文件（`act_proposal_point_reasons_test.go`、`act_proposal_scroll_coord_test.go`、`act_proposal_tap_bubble_test.go`、`act_target_resolution_proposal_test.go`）并完成 7 份引擎既有测试适配。 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 独立只读文档审计重发起与二审闭合 | 首次发起的 `docs_audit_v22`（`auto/deepseek-flash`）停留 `pending_init` 超 35 分钟无响应，按 `AGENTS.md` #17 记录原因后在不中断原任务的前提下改由 `docs_audit_v22_final`（`ollama-cloud/deepseek-v4.1-flash:1017`，`max`）完成两轮审计：初审 9 项发现（`docs/mcp.md:18` 目录指纹未刷新，以及 `docs/architecture.md`/`docs/interface.md`/`docs/mcp.md` 8 处边界措辞偏差）全部闭合后复检 `CLEAN`。 |
| 依赖 Agent 宿主托管进程重启与重连 | 未执行。按 `AGENTS.md` #19 与提案 §8.2 由用户处理；本次已完成 Windows `build/device-mcp.exe` 与 `build/mcp-probe.exe` 磁盘替换（保留旧运行映像备份 `.device-mcp.exe.previous-42a48154a8ad4a8590cd9531d1b0dbaa`）以及独立探针/闭环验证。 |
| 第三方复杂页面真机构造与 `primary` 模式 | 实机最小闭环在虚拟屏系统设置页（`com.android.settings`）经 Windows 主机 stdio 与手机 HTTP MCP 双入口完整验证：非滚动同视口外包装 `422 unsupported_node_action` + `candidates[0].ref` 同 `O` 重试、非滚动叶节点祖先提升滚动、坐标居中滚动、`409 node_disabled` 保 `O`、`bubble=false` 422 `candidates[0].ref` 同 `O` 重试、`bubble=true` 提升点击、重叠节点坐标点击与 raw `swipe`。跨兄弟共位按钮（`bubble_colocated`）、真双向嵌套滚动栈、多候选 `422 ambiguous_point`、`422 no_safe_trajectory`、`409 target_changed` 9 种 `reason` 与 `primary` 模式未在真机第三方应用单独构造，由 `internal/engine` 全量单元测试覆盖。 |

---

## 附：本提案不改变的事项

- `act` 的 `key` 分支、`text` 的写入/读回/ENTER 状态机与 `text_input` 字段语义；
- 公开树 `Node`/`ReadNode`/`ReadDelta` 结构、`tree v4` / `inspect v3` / `result v4` 文本协议格式；
- `scroll` 的 `effect` 计算规则与三态定义；
- 会话租期、O 的单向消费与认领后不归还规则、STOP 不等待撤权及物理收尾；
- `app`、`observe`、`snap`、`wait_for`、`inspect`、`shell`、`notify`、`status`、`diagnose`、`start`、`end`、`keepalive` 的行为与接口；
- Android 控制 App 业务逻辑与固定 uiautomator JAR 制品。
