# 提案：act 点击与长按的子树安全落点规划及可操作祖先引导

状态：提案，未实现。
基线提交：`81bad647d40d2b9e13f25ff12dfb573f978e5061`（分支 `master`，`2026-11-15`）。
适用范围：`act` 的 `tap` 与 `long_press` 分支中 `target.ref` 目标的静态落点规划、认领后实时重验、派发坐标选择，以及 `422 unsupported_node_action` 的 `error.details` 结构化引导；不改坐标目标、`text`、`swipe`、`scroll`、`key` 及公开树 `Node` 结构。
影响范围：`internal/engine`（`act.go`、新增 `tap_scope.go` 及测试）、`internal/protocol`（错误详情所有权与解码测试）、`internal/client`/`internal/mcp`（端到端保真测试及 `schema.go`）、受影响的正式文档；实施发布时涉及 `release/versions.json`、版本生成物、手机整组发布产物与清单、Windows 主机 `device-mcp.exe`/`mcp-probe.exe` 构建清单及 `maintenance/verification/`。
契约（`/api/*` 字段与 `contract_version`）：改变——公开树 `Node` 与请求顶层/分支 JSON 形状不变，但 `tap`/`long_press` 的 `target.ref` 默认落点策略、候选不可证明时的静态拒绝、认领后子树落点重验语义变化，且 `422 unsupported_node_action` 的 `error.details` 新增受控引导键，属于执行语义与契约面演进；`contract_version` 从 `13` 前进为 `14`（发行号 `0.14.0`、Android `versionCode=16`）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：[act.go](../../internal/engine/act.go)、[point.go](../../internal/engine/point.go)、[scroll.go](../../internal/engine/scroll.go)、[schema.go](../../internal/mcp/schema.go)、[interface.md](../../docs/interface.md)、[architecture.md](../../docs/architecture.md)、[mcp.md](../../docs/mcp.md) 与 [versions.json](../../release/versions.json)）。

---

## 0. 摘要

复合卡片的外层容器与内部配图都可点击时，基线对容器 `ref` 固定注入中心，可能触发配图；无动作正文 `ref` 则返回没有祖先线索的 `422`。
采用已选定的 **R3 + R1**：静态按 `default_center → carrier_node → center_span` 规划子树安全点，认领后仅重验原固定点，无可证候选时认领前拒绝。
无动作节点的 `422` 可附带同一观察内验证的祖先、承载节点与坐标引导；坐标还须通过整树唯一命中及作用域归属检查。
“安全”仅指树模型内的几何与作用域证明，不保证 Android 实际事件消费者或应用业务结果。公开树 JSON 形状不变，版本与双端发布范围按已定方案推进。

## 1. 问题

### 1.1 现象与代价

分诊为**取舍**：本提案改变基线有测试保护的 `ref` 派发与重验语义，不按“实现偏离现行规范”的缺陷直接修复。本次只审核和修订提案；新行为的先红用例与实机验收留在 §8，不将设计核对称为复现或实测。

起草交接以 Issue #14 的信息流卡片和 Issue #3 的祖先引导诉求为背景；本次未独立读取这些报告，以下仅作为**合成几何示例**，不证明现场应用的事件分发或业务结果：

- 外层卡片容器 `n516`：`role=view`, `bounds=[0,303,1080,1114]`, `actions=["tap"]`，几何中心 `(540, 708)`（业务语义：进入帖子详情）；
- 卡片内正文 `n491`：`role=text`, `bounds=[181,473,1046,529]`, `actions=[]`（树模型中可作为外层容器的非交互承载区）；
- 卡片内配图按钮 `n523`：`role=button`, `bounds=[181,552,1046,993]`, `actions=["tap","long_press"]`（覆盖外层容器中心 `(540, 708)`，模型中属于竞争子作用域）。

这组几何下，基线存在以下代价或风险：

1. **点内容叶节点 `ref="n491"` 被拒且无线索**：返回 `422 unsupported_node_action`（`"目标节点不支持点击"`、`outcome=not_executed`），`error.details` 为空；调用方无从直接获知可冒泡的最近父容器 `n516` 及其安全点击区域。
2. **点外层容器 `ref="n516"` 可能误触子控件**：静态检查与身份重验可通过，派发注入 `(540, 708)`，该点处于子图按钮内；若应用由该子按钮消费事件，就需多一轮“打开大图 → 返回 → 重新定位”。`outcome=executed` 仅确认输入执行，不能证明打开了预期页面。
3. **退化为人工手算坐标**：调用方被迫在模型推理中遍历父链与兄弟矩形，手动计算避开 `n523` 的坐标（如 `{x:400, y:500}`）再发起第二次 `act`。

### 1.2 证据链

#### 通路一：`target.ref` 的派发固定取 `raw.Bounds.Center()`，无子树竞争作用域检查

1. [act.go:246–276](../../internal/engine/act.go#L246-L276) 的 `staticActContentCheck` 对 `target.UseRef` 只检查节点非 root、非 context、`Bounds != nil`、未禁用且 `hasAction(node, ActionTap/ActionLongPress)`，不检查 `raw.Bounds.Center()` 是否落在子树内其他具备交互能力的子控件上。
2. [act.go:379–391](../../internal/engine/act.go#L379-L391) 的 `revalidateActTarget` 对 `target.UseRef` 只执行 `matchFreshNode(&capture, obs.Projection.Capture, raw)`（完整内容身份 + 祖先链匹配），不校验实际点击落点在新鲜公开树中的子树归属。
3. [act.go:488–494](../../internal/engine/act.go#L488-L494) 的 `dispatchAct` 对 `target.UseRef` 固定执行 `x, y = raw.Bounds.Center()`，直接将外包框几何中心交给 `e.dev.Tap`/`LongPress`。
4. 同类对照：[scroll.go:180–251](../../internal/engine/scroll.go#L180-L251)（`scrollSafeSpans`）、[静态规划](../../internal/engine/scroll.go#L518-L533) 与 [原轨迹重验](../../internal/engine/scroll.go#L667-L697) 已具备“扣除竞争作用域、静态规划、重验原计划”的机制；tap/long_press ref 尚无对应子树作用域证明。
5. 边界核查：[act_point_test.go:404–422](../../internal/engine/act_point_test.go#L404-L422)（`TestActRefTargetStillUsesIdentityOnly`）保护子树外同级重叠不使 `ref` 自拒。本提案只避让目标子树内的竞争后代；不据此声称无障碍父子关系必然等于 Android 实际触摸路由。
6. 参考已归档的 [scroll-safe-trajectory-and-viewport-effect.md §4、§7、§11](implemented/scroll-safe-trajectory-and-viewport-effect.md)：复用静态规划、固定计划重验、半开区间减法、source/祖先环保护和条件交付原则；不移植 scroll 的整树滚动作用域归约、方向平局规则或 `effect` 判据。当前代码以本提案基线为准，归档提案中的旧行号不作为当前代码位置。

#### 通路二：`422 unsupported_node_action` 缺少可操作祖先与安全承载区线索

1. [act.go:251–265](../../internal/engine/act.go#L251-L265) 在目标节点不支持 `tap`/`long_press` 或不能作为操作目标时，直接返回 `protocol.NewError(422, protocol.CodeNodeUnsupported, ...)`，未调用 `WithDetail` 附带任何结构化上下文。
2. [decode_response.go:107–123](../../internal/protocol/decode_response.go#L107-L123) 以 `map[string]any` 解码 `details`；[result_text.go:50](../../internal/mcp/result_text.go#L50) 将其整体作为 JSON 文本输出，`encodeResultDiagnostic` 也复用该编码。外层信封可承载嵌套对象，但现有泛型门禁不等于新键已通过端到端验收。
3. [errors.go:27–32](../../internal/protocol/errors.go#L27-L32) 的 `APIError.Public()` 共享 `Details`，而 [engine.go:1166–1168](../../internal/engine/engine.go#L1166-L1168) 的重复 P / `status(P)` 视图直接共享 `op.Error`；[result_value.go:30–36](../../internal/mcp/result_value.go#L30-L36) 的取消投影只复制外层 map。本提案须补新嵌套对象的所有权门禁，不能宣称基线已经深拷贝，见 §4.6。
4. [delivery.go:76–82](../../internal/engine/delivery.go#L76-L82) 的交付白名单不含 `hierarchy_unavailable`；[act.go:720–725](../../internal/engine/act.go#L720-L725) 的 `lineageOf` 没有环保护。新 ref 路径须分别明确失败交付与匹配前的拓扑守卫。

### 1.3 为什么直觉会出错

- **“对不支持点击的叶节点 `n491` 直接静默晋升去点父容器 `n516`”**：违反已定的不静默晋升边界。若用户选择了容器内的装饰文本，静默晋升可能在调用方不知情下触发外层动作；若不解决容器中心遮挡，晋升后仍可能命中子图 `n523`。
- **“只在 `422` 错误里返回 `nearest_actionable_ref: "n516"`，不改 `n516` 的落点逻辑”**：这会制造诱导误点的陷阱——调用方听从错误提示改传 `target={ref:"n516"}`，下一拍就会因为 `n516` 盲点几何中心 `(540, 708)` 而直接误开子图 `n523`。
- **“对 `target.ref` 直接套用全局 `resolvePoint(Tree.Root, x, y)` 要求目标必须是全屏最深节点”**：这会把子树内的正常文本承载子节点（如按钮或卡片内的 `text`）也当成遮挡，还会破坏 [act_point_test.go:404–422](../../internal/engine/act_point_test.go#L404-L422) 中同级全屏层与按钮重叠时的 `ref` 语义。正确边界是**只把目标子树内部的竞争交互后代（及其子树）视为阻塞区，而把目标子树内部的非交互后代视为归属目标的冒泡承载区**。
- **“在公开树每个 `Node` 上新增 `nearest_tap_ref` 或 `center_occluded` 字段”**：会让整棵树（含紧凑视图 `view=compact`）的每个节点膨胀字节开销，侵占 128 KiB 公共预算。

## 2. 目标与非目标

**目标**

- **G1（容器 `ref` 安全落点规划）**：目标 `C` 合法且原中心满足完整子树安全谓词时仍用 `raw.Bounds.Center()`；否则按三级顺序选点。中心越界、竞争遮挡或子树内同深度命中歧义都可能触发后续候选。
- **G2（候选不可证明时静态拒绝）**：三级候选均不能通过时，认领前返回 `422 unsupported_node_action`，若有可定位的中心阻塞者则附 `occluding_ref`；零设备 I/O、本操作不消费 `O`。这不等于证明整个二维表面都被遮挡。
- **G3（新鲜树重验固定落点）**：认领 `O` 后，在 `revalidateActTarget` 中不仅证明 `C`（及所选承载子节点，若有）的 `matchFreshNode` 身份与祖先链未变，还通过不推进保号的 `Preview` 证明**静态规划的固定坐标 `(plan.x, plan.y)`** 在新鲜树中仍归属 `C` 的子树安全作用域；不满足则返回 `409 target_changed` 并按既有门禁交付新观察，绝不在重验阶段重新改选坐标。
- **G4（`422` 结构化祖先与安全区引导）**：当 `tap`/`long_press` 的 `target.ref` 因节点自身不支持该动作（或为无动作能力的非根公开节点）被 `422 unsupported_node_action` 拒绝时，若沿父链存在未被中间交互节点隔断、可用且具备安全落点的最近可操作祖先 `A`，在 `error.details` 中同时附带 `nearest_actionable_ref`、`safe_point` 与（当落点来自承载子节点时的）`safe_carrier_ref`。
- **G5（保持树结构与双端发布闭环）**：公开树 `Node` JSON 形状不变；推进 `contract_version` 至 `14`，完成手机端整组构建、清单核验、部署与最小实机验证闭环，并更新构建主机侧 `device-mcp.exe` 与 `mcp-probe.exe`。

**非目标**

- 不对不支持动作的 `target.ref` 做静默祖先晋升派发（点 `n491` 仍明确返回 `422 unsupported_node_action`）。
- 不改变坐标目标 `{x, y}`、`text`、`swipe`、`scroll`、`key` 的判据与行为。
- 不解决跨同级（sibling）/跨窗口 z-order 遮挡或未发布透明拦截层（保留 [act_point_test.go:404–422](../../internal/engine/act_point_test.go#L404-L422) 的同级 `ref` 行为）。
- 不新增请求字段，不改变公开树 `Node`/`ReadNode`/`ReadDelta` 的字段定义。
- 主机侧只更新构建 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 及构建清单，不重启或修改依赖它的外部 Agent 宿主进程。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R0 维持现状 / 仅改文档 | 代码不变，仅在文档提示避免直接点大卡片 `ref` | 零代码成本 | 点 `n491` 仍无祖先提示，点 `n516` 仍静默误开子图 `n523`，调用方仍须手算坐标 |
| R1 仅在 `422` 附带祖先线索 | 点 `n491` 报 `422` 时在 `details` 给 `nearest_actionable_ref`；`ref` 派发仍固定取 `Bounds.Center()` | 改动最小 | 调用方按提示改点 `ref="n516"` 会直接命中 `(540, 708)` 的子图 `n523`，形成诱导误点陷阱 |
| R2 中心遮挡静态拒 + R1 线索 | 点 `n516` 发现中心被 `n523` 遮挡时静态报 `422` 并给安全坐标；点 `n491` 也报 `422` 给安全坐标 | 需升级契约与静态检查 | 消除了静默误点，但对中心被配图占据的卡片，`target.ref` 永远无法直接点击，必须先错一轮再退回 `{x, y}` 坐标 |
| **R3 + R1 子树安全落点规划 + 祖先线索（选定）** | 对 `target.ref`（`n516`）在子树内确定性规划避开竞争子控件的安全落点并重验；对无动作叶节点（`n491`）报 `422` 并返回 `nearest_actionable_ref`、`safe_carrier_ref` 与 `safe_point` | 静态规划 + 新鲜树 `Preview` 校验（纯 CPU，零额外采集）；升契约版本 `14` | 子树被竞争子控件 100% 填满或中心线/子节点均无安全点时静态报 `422`；跨同级 z 序遮挡仍不在树证明范围内 |

选择 **R3 + R1**：复用已落地 scroll 的“静态规划、重验原计划”原则，但 tap/long_press 的阻塞范围限于目标子树。引导只提供同一观察中的可证选项，下一次调用仍受期限、显示、归属与实时重验约束。

## 4. 判据

### 4.1 输入有效性、交互边界与子树作用域

仅对 `tap` / `long_press` 且 `target.UseRef == true` 生效。全部计算基于不可变 `Projection.Tree`（`*protocol.Node`）及其同帧 `Capture` / `Source`，不使用紧凑阅读树反推几何。

1. **有界校验与失败映射**：
   - 访问 raw 节点前检查映射实际存在及 `0 <= index < len(Capture.Nodes)`；以切片索引为准，不将零值索引当成“找到”，不按相同 bounds 猜来源。
   - 新路径在静态规划及调用 `matchFreshNode` / `Preview` **之前**校验相关输入：raw root 合法、根父为 `-1`，所有可能被匹配扫描访问的 raw 父链最终到达该 root，child 索引合法且与 parent 互相对应；以 visited/三色或至多节点数的步数守卫拒绝自环、多节点环、断链、重复挂载。公开树同样检查节点/ref 唯一、无指针环、无多父，并校验涉及的非 context 节点 source 与 raw 祖先关系一致。
   - 校验不能依赖基线无环保护的 `lineageOf`，也不能先交给可能遍历坏图的 `Preview`。这是新 ref 路径的局部前置守卫，不扩大坐标、text、swipe、scroll 的判据；若需改共用匹配函数，另行核查调用方语义。
   - 静态目标 source 缺失/越界沿用 `409 node_not_found`；其它结构/映射损坏为 `503 hierarchy_unavailable`，本操作不认领、不采集。R1 仅因无法构造引导而遇到这些问题时，保留原 `422` 并省略引导。新鲜 raw/公开图拓扑损坏或不可投影为 `503 hierarchy_unavailable`；图结构有效但目标/承载的身份、source 映射或落点归属不可证为 `409 target_changed`，作用域内其它 source 不能证明时也拒绝该固定点。
2. **已知交互节点 `isTouchInteractive(M)`**：非 root、非 context 且 actions 含 `tap`、`long_press`、`text` 任一种。判据对两种请求动作相同；不因 `enabled=false` 豁免竞争节点。仅有 `swipe` 不进入该集合。这是公开能力词表上的保守模型，不把 editable 启发式或动作位解释为真实事件分发证明。
3. **未知边界 `M.Context`**：基线 [build.go:373–378](../../internal/observation/build.go#L373-L378) 清空 context 的动作与几何，且 [engine.Projection](../../internal/engine/engine.go#L276-L280) 没有 `ContextSource`。因此不能从 `actions=[]` 推出其非交互，也不能读取不存在的 `Source[M.Ref]`。本提案将 context 视为保守边界：不作承载候选，其所有具有正面积公开 bounds 的后代都阻塞；context 自身无可用矩形，不凭空扩大成整个 `C.Bounds`。R1 沿父链遇到 context 则停止引导。
4. **基础几何**：目标 `C` 必须非 root、非 context、有正面积 bounds、未被明确禁用、含请求动作且 source 有效。令 `raw = &Capture.Nodes[Source[C.Ref]]`，`screen = Capture.Display.Bounds()`，`baseRect = raw.Bounds ∩ screen ∩ rectFromBounds(C.Bounds)`。显示尺寸必须正且与本 O 的屏幕一致；空交集不能取中心。使用 `Bounds`，不以 `RawBounds` 替代基线坐标。
5. **`collectSubtreeTapScope(C)`**：从 `C.Children` 开始先序 DFS，继承 `inBlocked` 与最近边界 owner；`blockedHere = inBlocked || isTouchInteractive(M) || M.Context`。
   - `blockedHere` 为真时，记录所有非 context、有正面积 bounds 后代的矩形及其最近边界 owner；继续遍历子树。后代 bounds 可伸出其父 bounds，**不得因父矩形不命中或为空而剪枝**。不遍历 `C` 子树外同级节点作为 R3 阻塞者。
   - 否则将非 context、有正面积 bounds 且 source 有效的 `M` 按先序加入 `carriers`，并继续遍历；无效 source 不得悄悄跳过，按第 1 条失败。
   - 用于点测试与区间减法的阻塞矩形先裁至 `baseRect`，去空；继承 owner、深度与先序序号用于确定性诊断。root 不作为交互边界或承载节点。

### 4.2 子树安全落点谓词（`provesSubtreeTapPoint`）

点 `pt=(x,y)` 满足该谓词，当且仅当：

1. `baseRect.Contains(x,y)`，所有矩形与区间使用半开边界；
2. 无任何 `blockedRects` 包含该点；
3. 在以 `C` 为根的公开子树中，最深非 context、非 root、有 bounds 且包含该点的节点**唯一**，并归属 `C` 的非阻塞作用域。

`resolveSubtreePoint` 将 **`C` 自身（深度 0）纳入候选**，其后代逐个独立检查；不能直接调用会排除深度 0 的 `resolvePoint(C,...)`。同深度承载节点重叠也使该点不可证明，因此“没有交互遮挡”本身不足以保证保留原中心。这里刻意比基线 ref 严；只保证子树外同级重叠不导致 R3 自拒。

### 4.3 三级确定性安全落点规划（`planTapTarget`）

按以下顺序生成有限候选，选**第一个**通过 §4.2 的点。R3 所有级别均只用子树谓词，不按整树结果重排、优先或拒绝候选。

`tapPlan` 仅为本次调用的不可变计划，保存点、strategy、目标 raw 来源、可选承载 raw/ref 与 CPU 计时；沿静态检查→重验→派发显式传递。不将它写入公共树、会话或恢复账本，不形成第二份执行权威。

1. **`default_center`**：先测 `raw.Bounds.Center()`；仅当它在 `baseRect` 外时，改测非空 `baseRect` 中心。原中心位于基础矩形且通过完整谓词时保留基线坐标；若它在矩形内但被阻塞/歧义，则进入第二级。
2. **`carrier_node`**：将先序 `carriers` 稳定拆为 2a 非匿名、2b 匿名，每轮内部保持先序。直接在 `*protocol.Node` 上定义 `anonymousPlainNode(M)`，等价于 [read.go:17–43](../../internal/observation/read.go#L17-L43)：
   ```text
   M != nil && !M.Context && M.Role in {view, group}
   && len(M.Label)==0 && len(M.Actions)==0
   && M.Value==nil && M.Hint==nil && M.Resource==nil && M.Axis==""
   && M.Enabled!=nil && *M.Enabled
   && 对 Focused/Selected/Protected/Checked：指针非 nil 且值为 false
   ```
   `nil` 是未知，不能当作已知默认 false；空字符串指针也不同于 nil。2a 为该谓词取反，2b 为真。无需构造 `ReadNode`；不承诺 compact 会逐节点原样显示几何（可能还有折链/打包）。用字段对照测试防止与阅读谓词漂移。
   对每个 `M`，先求 `mClip = M.Bounds ∩ baseRect`；空则跳过，非空先测 `M.Bounds` 中心，仅当中心不在 `baseRect` 时改测 `mClip` 中心。不因未命中而试任意其它偏移。通过时保存 `carrierRef=M.Ref` 与其有效 raw 来源。
3. **`center_span`**：前两级均无通过候选时执行，即使存在 carriers 但其中心均失败，也进入本级。
   - 3a 固定 `x=baseRect.Left+baseRect.Width()/2`，把横向包含 x 的阻塞矩形投影至纵向；复用 `subtractScrollSpans` 的裁剪、去空、排序、合并、相减语义，取最长剩余 `[s,e)`，等长取较小 s，测试 `(x,s+(e-s)/2)`。
   - 3b 仅在 3a 没通过时固定 `y=baseRect.Top+baseRect.Height()/2`，对称求横向最长区间，等长取较小 s，测试 `(s+(e-s)/2,y)`。
   - 只测试各轴最长区间中点；最长中点因命中歧义失败时不枚举其它区间。通过时 `carrierRef=""`。

承载节点出界时先求有效交集，绝不对空矩形调用 Center 后误选 `(0,0)`。候选去重保持首次出现顺序；区间/坐标计算避免整数溢出，无法表示则按无效几何拒绝，不能环绕到屏幕内。

三级均失败，静态返回 `422 unsupported_node_action`，受控文案为“无法在该节点作用域内生成可证明的点击落点”或“无法在该节点作用域内生成可证明的长按落点”。不能把候选搜索失败写成“整个容器完全遮挡”。若原 `raw.Bounds.Center()` 被已记录阻塞矩形覆盖，`occluding_ref` 取这些矩形对应的**最近边界 owner**，优先 owner 深度最大者、平局取先序最早者；context owner 也可诊断。原中心无阻塞证据（如仅歧义/几何退化）时省略该键。

### 4.4 认领后新鲜树重验判据

保持既有 `ProbeDisplay + Capture`、前台、显示代次、工作授权与派发门禁；复用单次新鲜采集：

1. 在 §4.1 的有界结构校验之后，执行 `matchFreshNode(fresh, observed, plan.targetRaw)`；不唯一或身份/祖先链失配为 `409 target_changed`。`carrier_node` 计划同样唯一匹配 `plan.carrierRaw`。
2. 选 `projector := work.Projector`，nil 时回退 `e.projector`，仍 nil 则 `503 hierarchy_unavailable`；调用不推进连续号的 `projector.Preview(&capture)`。错误、nil 投影、缺失树/根/采集或无效结构均为 `503 hierarchy_unavailable`，错误正文不含原始异常。
3. 通过匹配到的 **fresh raw 索引**找 fresh 公开目标，不能要求 fresh ref 等于旧 ref；映射不唯一也不可选 map 遍历遇到的第一个。公开目标必须仍非 root/非 context、bounds 有效、未禁用、含请求动作且 source 对应同一 fresh raw。`carrier_node` 的 fresh 承载节点也必须仍公开、归属该目标的非阻塞承载区，固定点在其 fresh bounds 与目标基础矩形内；承载消失/变为 context/被移动到交互边界下均失配。
4. 在 fresh 目标子树上重建同一作用域空间，只证明 **`(plan.x,plan.y)`** 满足 §4.2；不运行候选选择、不改变 strategy/坐标。不要求其它位置的最长区间或候选排名保持不变。节点失配、映射失配或点证明失败为 `409 target_changed`。
5. 返回已读的 `&capture` 与受控错误给原 `Act` 路径。`409 target_changed` 可走既有 `deliverRejection`，但仅发布门禁成功才交付新 O；`503 hierarchy_unavailable` **不在交付白名单**，不交付 O，恢复为 `observe/P/retired`。不为本提案扩大白名单，也不额外重采。

测试夹具应分别注入工作/会话 projector 与回退 projector；[act_point_test.go:240–254](../../internal/engine/act_point_test.go#L240-L254) 只替换 `e.projector` 的旧坐标用例仍保留原语义，不能照抄后误以为覆盖了 `work.Projector` 分支。

### 4.5 `422 unsupported_node_action` 的祖先与坐标引导（R1）

只在非 root 的 ref 目标 `N` 因 context/缺 bounds/无请求动作而返回原 `422` 时尝试。保持基线优先级：root/context/缺 bounds 的 422 先于禁用检查；具有可用几何且显式禁用者仍返回 `409 node_disabled`。R3 搜索失败的 `422` 只附可证的 `occluding_ref`，不再晋升祖先。

1. **从 N 自身检查边界，再沿唯一路径向上**：若 `N.Context` 或 `isTouchInteractive(N)`，停止引导（例如 N 仅 long_press/text，而请求 tap，不得跳过 N 自身去推荐外层）。否则从 parent(N) 向上，在 root 前遇到 context 即停止，遇到第一个已知交互祖先记为 `A`。`A` 禁用、无 bounds、source 无效或不支持请求动作都停止，不能越过它寻找更外层。
2. **先证明祖先 ref 可规划**：对 `A` 按 §4.3 运行 R3，必须成功。这样不会因单独找到一个推荐点，就给出其默认 ref 规划实际必败的祖先。引导过程纯 CPU，不递归附加引导、不认领、不采集。
3. **引导点使用更严的双重谓词**：`provesGuidancePoint(A,pt)` 同时要求 §4.2 成立，且 `(hitRef,state):=resolvePoint(Tree.Root,x,y)` 满足 `state==pointUnique`、hitRef 有有效 source、命中节点为 A 或 A 非阻塞承载区中的后代。仅“整树唯一”不够：子树外更深节点也可能唯一胜出，必须校验归属。`pointNone` 和 `pointAmbiguous` 均不能输出 safe_point。
4. **确定性选引导点**：优先测试 N 作为 A 承载节点的中心（使用 §4.3 的非空相交及越界中心回退规则）；失败再按 §4.3 同一候选顺序逐个测试双重谓词，不能只测试 R3 首个计划就提前停止。R1 中每个“通过即停止”均以双重谓词为准，例如纵向最长中点虽子树安全但全局不通过，仍测试横向最长中点；不枚举次长区间。该筛选仅服务 R1；不得反馈影响 R3 的策略或顺序。均失败则省略整组祖先引导；即便祖先 ref 可规划，也不输出一个缺少可证坐标的半组。
5. **详情键及语义**：通过时同时附 `nearest_actionable_ref=A.Ref`（string）和 `safe_point={"x":int,"y":int}`；若点由承载候选提供，再附 `safe_carrier_ref`（string）。承载 ref 仅解释点的来源，可能没有请求动作，**不能被描述成可直接点击的替代 ref**。default_center/center_span 不附该键；绝不以 null/空字符串代替省略。

R1 坐标与下一次对 A 的 ref 调用可能选在不同位置，但两者在同一个旧 O 上都通过各自静态判据；下一拍仍须新 P 与正常实时重验。引导仅对生成它的 O 有效；O 过期、被并发操作消费、被替换或撤权后不能继续使用。P 重放/status(P) 中保留的提示是历史事实，不是当前准入证明。

### 4.6 嵌套 `error.details` 的类型、所有权与投影门禁

- 服务端生成 `safe_point` 使用 `map[string]any{"x":intX,"y":intY}`，与外层 Details 的递归 JSON 对象形态一致；禁止把嵌套对象字符串化。HTTP 客户端解码后对象为 `map[string]any`，数字按现有 json/v2 的 any 解码为 `float64`，不能断言仍是 `map[string]int` 或 Go int。显示像素范围的整数须精确往返，含 x/y=0；公共语义是整数坐标，不是 Go 动态类型保持。
- 生成完成后不再原地修改任何 Details。实施时在 `APIError.Public()` 与 `operationViewLocked` 的错误投影边界建立独立所有权：至少对新增嵌套 map、外层 map 及已有受控数组做递归复制，避免首次结果、保留的 op.Error、重复 P 与 status(P) 共享可变对象。只复制受控 JSON 值，不以 JSON 往返复制改变既有 `[]int` 等内存类型；不把任意树或正文引入锁内。此复制不改变任何错误键或其它分支的业务判据。
- `canonicalResult` 的取消处理只追加外层 `mcp.*`，当前浅复制在“不写嵌套对象”的前提下可保留；须用含 safe_point 的取消夹具证明不修改源 Result/保留操作。后续若改嵌套对象必须先复制，不得把顶层复制宣传为深拷贝。
- 扩充协议解码、真实 HTTP 客户端、MCP result manifest/parity 夹具：同一嵌套结果经 HTTP → 客户端 → canonical JSON 后，both/text 的 `result v2` 可逆解析与 canonical JSON 等价；structured 的 StructuredContent 等价，且错误 `diagnostic v1` 的 `error.details` 完整包含新键。diagnostic 是白名单摘录，不要求整个 Result 能逆还原。
- 复用 [result_text.go:225–228](../../internal/mcp/result_text.go#L225-L228) 的 canonical 比较口径（保留数值、不做有损浮点重写），覆盖键插入顺序、零坐标、可选键省略和取消扩展。补重复 P/status(P) 不重派发、提示不重算及修改某一返回视图的嵌套值不污染其它视图/历史记录的测试。通过这些门禁前不作“已保真”的验证结论。

## 5. 算法

```text
静态检查（staticActContentCheck，锁外纯 CPU，不认领、不采集）：
  if operation.Type in {"tap", "long_press"} && operation.Target.UseRef:
      有界校验公开树拓扑，避免查找先进入环；失败按 §4.1 映射
      node, path := findPublicNodeWithPath(T_obs.Tree.Root, Target.Ref)
      if node == nil: -> 409 node_not_found
      if node.Role == root: -> 422 unsupported_node_action
      if node.Context || node.Bounds == nil:
          return attachTapGuidance(422 unsupported_node_action, T_obs, node, path, action)
      if node.Enabled == false: -> 409 node_disabled
      if !hasAction(node, action):
          return attachTapGuidance(422 unsupported_node_action, T_obs, node, path, action)
      校验 raw 拓扑与 source（目标来源缺失 409，其它损坏 503）
      plan, err := planTapTarget(T_obs, node, action)
      if err != nil: return err（无候选 422，可选 occluding_ref，见 §4.3）

取得普通工作位（既有）→ 认领 O（既有）→ ProbeDisplay + Capture（既有）→ 前台检查（既有）

实时重验（revalidateActTarget）：
  if tapPlan != nil:
      校验 T_new 的 raw 拓扑，再调用匹配/Preview；无效 -> 503，无新 O
      freshTarget, ok := matchFreshNode(T_new, T_obs.Capture, tapPlan.targetRaw)
      if !ok: -> 409 target_changed（尝试交付 T_new）
      if tapPlan.carrierRaw != nil:
          if _, ok := matchFreshNode(T_new, T_obs.Capture, tapPlan.carrierRaw); !ok:
              -> 409 target_changed（尝试交付 T_new）
      projector := work.Projector，nil 时回退 e.projector
      if projector == nil: -> 503 hierarchy_unavailable（无新 O）
      fp, err := projector.Preview(T_new)
      if err != nil || fp 结构无效: -> 503 hierarchy_unavailable（无新 O）
      freshPublic := 按 raw index 唯一反查公开节点（拒绝重复映射）
      if !validActionablePublicNode(freshPublic, action) || 承载证明失败 ||
         !provesSubtreeTapPoint(fp, freshTarget, freshPublic, tapPlan.x, tapPlan.y):
          -> 409 target_changed（尝试交付 T_new）

派发（dispatchAct）：
  使用 (tapPlan.x, tapPlan.y) 注入一次 Tap 或 LongPress（既有回执门禁与后观察流程不变）
```

时间轴：

```text
t0 observe：T_obs，O ready
t1 act 静态：
   - 若 target.ref 无动作 -> 422 unsupported_node_action（可选已验证引导），本操作不消费 O
   - 若 target.ref 有动作但无安全点 -> 422 unsupported_node_action (+ occluding_ref)，O 仍 ready
   - 若 target.ref 规划出 (plan.x, plan.y) -> 进入 t2
t2 claim：O ready -> claimed
t3 Capture：读取实时帧 T_new
t4 有界拓扑守卫 + matchFreshNode：证明 targetRaw（及 carrierRaw）身份与祖先链唯一
t5 Preview(T_new) + provesSubtreeTapPoint：证明原固定点 (plan.x, plan.y) 仍归属目标子树安全作用域
t6 dispatch：注入 (plan.x, plan.y)
```

撤销、失败、超时行为：

- **t1 候选失败/无动作的静态拒绝**：`outcome=not_executed`、`422 unsupported_node_action`，不占设备工作位、不认领 `O`。没有并发消费、过期或撤权时，O 仍 ready，`recovery=retry/O/effective` 表示可改正请求，不承诺原样重试成功。结构无效的 `503` 沿用现有恢复规划，不伪装为可用引导。
- **t2–t5 期间 STOP/会话结束**：返回 `409 control_revoked` 或 `session_ended`，`outcome=not_executed`，未派发输入。
- **t4–t5 重验失败**：`outcome=not_executed`；`409 target_changed` 仅在 `deliverRejection` 门禁通过时交付新 O 与 `retry/O`，否则 `observe/P/retired`；`503 hierarchy_unavailable` 始终无新 O，`observe/P/retired`。旧 O 均不归还。
- **期限/预算**：复用现有观察、工作及派发期限，不新增自动重试或延长租约；CPU 循环有节点数边界，耗尽当前工作期限后不得派发或提交迟到候选。STOP 先撤权，最终派发/发布仍复核原会话、显示代次、工作拥有者与期限，不等待本地规划结束。
- **永不静默改写目标 `ref`**，**永不在重验阶段改选坐标**，**派发前失败永不返回 `executed` 或 `uncertain`**。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/tap_scope.go`（新增） | 实现 `tapPlan`、有界拓扑/source 校验、作用域收集、Node 匿名判据、候选枚举、子树/引导点谓词、规划与祖先引导 |
| `internal/engine/act.go` | 接入不可变 `tapPlan`、固定点重验/派发与 `422` 引导；调整内部函数签名及调用方，保留其它动作分支；记录白名单诊断日志 |
| `internal/protocol/errors.go`、`internal/engine/engine.go` | 错误投影建立嵌套受控详情的独立所有权，更新 Details 注释白名单；不改 JSON 形状 |
| `internal/engine/act_tap_scope_test.go`（新增） | 覆盖三级落点规划、全遮挡静态 422、动态子控件遮挡 409、承载子节点变化 409、祖先引导 `error.details`、中间交互边界阻断及同级 `ref` 不误拒回归 |
| `internal/mcp/schema.go` | 更新 `act` 工具描述，说明 `tap`/`long_press` 的 `target.ref` 子树安全落点机制与 `422 unsupported_node_action` 的 `error.details` 引导字段 |
| 协议/客户端/MCP 相关测试 | 新嵌套详情的 HTTP 解码、三模式/取消保真、重复 P/status(P) 视图隔离；扩充 result manifest/parity 夹具 |
| `docs/interface.md`、`docs/architecture.md`、`docs/mcp.md` | 同步 `tap`/`long_press` 子树安全落点规划、固定落点重验、`422` `error.details` 字段规范及当前版本号说明 |
| `release/versions.json` 及生成文件 | 唯一版本源推进至 `release_version="0.14.0"`、`contract_version=14`、`android_version_code=16`，由 `scripts/generate-version.ps1` 同步 Go/Java/XML/Manifest |
| 构建清单与验证记录 | 运行 `scripts/build-release.ps1`、`scripts/verify-manifest.ps1`、`scripts/deploy.ps1` 与 `scripts/build-host-tools.ps1`，写入 `maintenance/verification/act-tap-safe-scope-and-guidance.md` |

### 6.2 配置与取值

- 无新增配置项或请求参数。
- 候选优先级固定为：`default_center` → `carrier_node`（先非匿名 2a、后匿名 2b，先序）→ `center_span`（先纵向中心线 3a、后横向中心线 3b，最长区间，等长取小坐标侧）。
- `error.details` 新增受控字段名固定为：
  - `nearest_actionable_ref`（string）
  - `safe_carrier_ref`（string，可选）
  - `safe_point`（`{"x": int, "y": int}`）
  - `occluding_ref`（string，可选，仅当 R3 无候选且有原中心阻塞证据；选择规则见 §4.3）

### 6.3 日志与可观测性

- 复用 `internal/eventlog/log.go` 现有白名单键（`operation_id`、`action`、`mode`、`proven`、`ms`），不新增可能携带用户正文的键：
  - 事件名 `act.tap_plan`：`operation_id`、`action`（`tap`/`long_press`）、`mode`（`default_center`/`carrier_node`/`center_span`/`rejected`）、`proven`（bool）、`ms`（规划及重验的纯 CPU 耗时，不含设备 I/O）。
- 仅实际进入 R3 规划的首次 P 记录一条：静态失败时 `rejected/false`；规划成功后在退出重验阶段时记录所选 mode，`proven` 仅指固定点重验通过，busy/撤权等未重验出口为 false。重复 P/status(P) 不重新规划或补写事件；无动作的 R1 拒绝不伪造一次 R3 计划。
- 坐标数值、`ref` 编号（`nearest_actionable_ref`/`safe_carrier_ref`/`occluding_ref`）、节点标签与树内容一律不写入日志。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威 | 子树作用域规划、重验与祖先引导均在 `internal/engine` 完成，客户端与 MCP 不做二次树推导 |
| STOP 不等待 | 规划与 `Preview` 均为有界内存纯 CPU 操作，不增加设备 I/O，不改变撤权与封闭派发路径 |
| O 单向消费 | 静态拒绝发生在 `acquireWork` 与 `claimObservationLocked` 之前，本操作不消费 O；并发、过期、撤权仍可使旧 O 失效；认领后重验失败不归还 O |
| 状态锁短小 | 不可变投影树遍历与 `Preview` 均在 `e.mu` 锁外执行 |
| 事实分层 | 静态拒绝与重验失败均为 `not_executed`；不因安全落点改选而改变 `executed`/`uncertain` 判据 |
| 保号连续性 | 新鲜树校验使用 `Projector.Preview`，绝不推进会话的 `Builder.prev` 或 `nextRef` |

### 6.5 契约与文档影响

1. **契约版本升级**：按 [docs/release.md §2](../../docs/release.md#L45-L47)，`tap`/`long_press` 的默认落点行为、静态拒绝条件、重验判据及 `422` 错误详情结构发生变化，`contract_version` 从 `13` 升级为 `14`，`release_version` 升为 `0.14.0`，`android_version_code` 升为 `16`。
2. **正式文档同步范围**：
   - [docs/interface.md](../../docs/interface.md) §1（`error.details` 白名单及键类型/省略规则）、§2（422 说明）、§6（静态规划、固定点重验、引导及历史提示有效性），避免只加分支说明却与总白名单冲突；
   - [docs/architecture.md](../../docs/architecture.md) §3（补充 `tap`/`long_press` 的子树作用域与安全落点规划说明）；
   - [docs/mcp.md](../../docs/mcp.md) §1/§2（更新工具目录指纹与 `act` 描述说明）；
   - 同步 [docs/control-protocol.md](../../docs/control-protocol.md)、[docs/release.md](../../docs/release.md)、[docs/runbook.md](../../docs/runbook.md)、[docs/phone-operations.md](../../docs/phone-operations.md)、[docs/client.md](../../docs/client.md) 与 [README.md](../../README.md) 中的当前版本/示例；按真实工具目录重新生成 MCP 指纹，不手填推测值。实施前检索其余正式文档，仅修改实际受影响的描述。
3. **文档审计门禁**：所有 `docs/` 修改在实现提交前交由独立只读文档审计 subagent 审核并取得 clean 结论。

## 7. 预期效果与能力边界

### 7.1 能治什么

- **复合卡片避开模型内的子按钮**：§1 合成例中 `act(tap, target={ref:"n516"})` 的静态计划为 `(613,501)`；新鲜帧仍可证明时派发该点，不再派发被配图占据的原中心。是否进入帖子详情属于实机业务观察。
- **无承载子节点但有留白区间的卡片容器**：如文本直接写在容器 `label` 上、下半部被子按钮占据的卡片，通过中心线安全区间（`center_span`）自动点中上半部留白中点。
- **无动作正文获得可验证引导**：同一合成树中返回 `nearest_actionable_ref="n516"`、`safe_carrier_ref="n491"`、`safe_point={"x":613,"y":501}`。在 O 未失效且新鲜证明通过的前提下，可改用祖先 ref 或该坐标；无需自行推算矩形差集。
- **派发前动态阻塞拦截**：新鲜树的竞争边界覆盖固定点时返回 `409 target_changed`，零输入；可交付时提供新 O，交付失败不改变未执行事实。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 跨同级（sibling）或跨窗口浮层覆盖 `ref` 目标 | 公开树无同级 z 序；为不误伤悬浮按钮压列表（`TestActRefTargetStillUsesIdentityOnly`），阻塞集只取目标子树内部的竞争交互后代 |
| 容器表面被子交互控件 100% 铺满（无任何非遮挡承载节点或中心线缝隙），或仅四角有极窄L形边角空隙 | 不做任意二维像素网格穷举；三级候选均不满足时静态返回 `422 unsupported_node_action` |
| 自定义绘制控件（Canvas/WebView/纯图片）内部把不同子区域硬编码成不同业务动作，但未在无障碍树发布子 `Node` | 公开树中只有一个整体节点，无子作用域数据可供区分 |
| 可见但无语义、无保留子树的纯透明触摸拦截层 | 按 `observation/build.go` 规则未发布到公开树，树判据不可见 |
| 应用自定义拦截、触摸代理、无障碍虚拟层级、坐标过滤或私有业务回调 | 无障碍父子关系和 actions 不等于真实事件消费者，树证明与 executed 均不证明期望业务结果 |
| context 后代实际不拦截触摸，或同深度非交互承载节点重叠 | 前者按未知边界阻塞，后者按子树命中歧义拒绝；可能保守放弃物理上可点击的点 |
| ref 可规划，但所有引导候选均受外部同级重叠/更深外部节点影响 | R3 可继续，R1 严格双重谓词不成立就省略整组引导，不改变坐标目标判据 |
| 非最长区间或非候选位置仍有空间 | 只枚举三级规定候选，不穷举二维空间或每个区间；422 不证明全部空间不可点 |

### 7.3 成本

- **设备 I/O**：零额外设备采集。静态拒绝仍为零 I/O；认领后重验复用既有的单次 `Capture`。
- **CPU/空间**：令整帧节点数 N、目标子树节点数 S、阻塞矩形数 B、候选数 K（至多与 S 同阶）。有界拓扑/source 索引可预建为 O(N) 时间/空间；作用域收集 O(S)，区间减法 O(B log B)。逐候选的子树命中及阻塞测试朴素为 O(K(S+B))；R1 若逐候选执行整树解析还可能 O(KN)，不能宣称总计只有单次线性遍历。身份匹配与 Preview 仍有既有成本。所有工作受现有树/快照额度和工作期限约束；本次没有测得 `<2ms` 或设备 P50/P95，不作性能保证。
- **日志**：仅进入 R3 的首次请求按 §6.3 输出一条白名单事件。

## 8. 验证计划

### 8.1 单元测试

以下新行为用例尚未编写/执行；实施前先红，实现后转绿并保留红绿摘要。基线回归只证明既有行为，不替代新判据验收。测试断言输出坐标、错误、O/P 状态、I/O 计数及独立谓词，避免仅重复实现步骤。

| 用例组 | 可观测验收与反向对照 |
| --- | --- |
| 常规中心与子树根候选 | 无后代的合法按钮仍派发 raw.Bounds.Center；直接证明 `resolveSubtreePoint` 包含深度 0 的 C；同深度非交互重叠触发后续候选或 422，不伪称常规中心绝对不变 |
| 合成复合卡片 | §1 几何精确派发 `(613,501)`；**先红对照：基线派发 `(540,708)`**。tap/long_press 均覆盖，long_press 的外层显式具备对应动作 |
| 匿名 Node 字段等价与排序 | 2a 优先 2b；各自保持先序。逐字段对照 ReadNode 谓词，覆盖未知 nil、显式 false、checked=nil/false、空字符串指针、仅 swipe、context；不在生产路径转换 ReadNode |
| 候选中心规则 | 中心出 baseRect 才测交集中心；carrier 与 C 无交集则跳过，含左/上负数、部分屏外、子 bounds 超出 C、奇数边长与零面积，不能生成伪 `(0,0)` |
| 中心线区间 | 无 carrier 及有 carrier 但中心失败都可到第三级；纵向优先横向、最长/等长靠上或靠左、1px/半开相切、重复/嵌套/相邻阻塞、乱序、最长中点歧义不枚举次长区间；独立逐整数点核对合成小矩形 |
| 静态拒绝与诊断 | 全遮挡和仅候选歧义分别 422/not_executed；Probe/Capture/Tap/LongPress 增量为零；无并发时 O ready。occluding_ref 按最近边界 owner 的深度/先序稳定选择，仅歧义无该键 |
| 竞争与 context 边界 | enabled=false、仅 long_press、仅 text 都阻塞两种请求；context 无 source/无自身 bounds 时其有 bounds 后代仍阻塞；父 bounds 不含点而后代伸出也不能漏检；仅 swipe 不自动成为已知交互边界 |
| raw/公开拓扑与 source | 目标映射缺失/越界 409 node_not_found；相关其它映射损坏、root/parent/child 断链、自环/多节点环、多父、重复 ref/来源反查不唯一都保守失败且有界返回。fresh 环必须在 matchFreshNode/Preview 前被挡住；删除守卫后该用例应失败或被测试超时捕获 |
| 动态固定点阻塞 | fresh 新增/扩大竞争后代覆盖原点，409/not_executed、零输入；**删除 fresh 子树谓词则必须失败**，不能只靠目标身份失配使对照变绿 |
| 动态承载与目标 | 目标/承载身份不唯一、承载移动/消失/变 context 或移入竞争作用域均 409，不重选点；新出现更优候选或原点外变化但原证据有效时仍派发原点一次 |
| projector 选择与失败 | 工作 projector 与 e.projector 故意不同时使用工作实例；nil 回退可用；两个均 nil、Preview error/nil/坏投影为 503/not_executed、零输入、无新 O、observe/P/retired。旧坐标 projector 注入用例保持通过 |
| 失败帧交付与撤权 | 409 且门禁通过才有新 O/retry；帧龄/预算/未知前台/STOP/旧 work 失败无新 O，不归还旧 O；沿用 act_delivery 的反向对照与重复 P 不重交付门禁 |
| 同级 ref 兼容 | TestActRefTargetStillUsesIdentityOnly 继续通过；外部同级重叠不得重排任何一级的 R3 候选或使其自拒 |
| R1 正文引导 | 合成正文 422 携带祖先 n516、承载 n491、safe_point `(613,501)`；以两个独立会话/独立 O 或独立夹具分别测试 ref 与坐标下一拍，不能同一 O 连续成功两次 |
| R1 N 自身及祖先边界 | N 自身仅 long_press/text、N 或父链 context、最近交互祖先禁用/缺几何/无对应动作时不越界；无祖先、祖先 ref 不可规划时无整组引导 |
| R1 全局归属漏洞 | 同深度外部覆盖为 ambiguous，子树外更深节点为 unique，两者均不可输出该点；R3 本身仍可成功。删掉 hit 所属校验时，“更深外部唯一”对照必须失败 |
| R1 候选与可选键 | N 中心不可用时按统一候选序继续；首个 R3 点全局失败但较后候选通过时输出后者；全部失败无整组；center_span/default_center 无 carrier 键，carrierRef 不能被当成具备动作 |
| 嵌套详情 HTTP/客户端 | 实际 HTTP 解码得到嵌套对象而非字符串，x/y=0 不丢失；int → float64 → JSON 数值相等，可选键严格省略；不做错误的 Go 动态类型相等断言 |
| 三种 MCP 模式及取消 | 扩充 manifest/parity 夹具；both/text 的 result v2 逆解析等价，structured 的 canonical 数据等价且 diagnostic 保留 details；取消追加 mcp.* 不改源结果，字典插入顺序不影响输出，不宣称 diagnostic 可还原完整 Result |
| P 事实与所有权 | 同 P 不重派发/不重算提示；status(P) 无采集。修改首次/重放/status 返回视图的嵌套 map、受控数组不会污染 op.Error 或其它视图；O 退休后的提示仍仅是历史值 |
| 日志与事实分层 | 仅白名单枚举/ID/布尔/耗时，无 ref/坐标/树/异常文本；规划失败、busy、STOP、重验失败的 proven 不伪真；执行后后观察失败不改执行事实 |

### 8.2 实机/系统级

以下均为实施计划，本次审核未构建发布、部署或执行新行为实机验证。只测本次真实变化路径及版本/发布必要门禁，不扩大设备或功能矩阵。

1. **版本生成、相关包回归与目标构建**，在独立 PowerShell 中执行：
   ```powershell
   ./scripts/generate-version.ps1
   ./scripts/generate-version.ps1 -Check
   $env:GOEXPERIMENT = 'jsonv2'
   go test ./internal/engine/... ./internal/protocol/... ./internal/httpapi/... ./internal/client/... ./internal/mcp/...
   go vet ./internal/engine/... ./internal/protocol/... ./internal/httpapi/... ./internal/client/... ./internal/mcp/...
   $env:GOOS = 'linux'
   $env:GOARCH = 'arm64'
   $env:CGO_ENABLED = '0'
   go build ./...
   ```
   不带着交叉环境在 Windows 运行测试或后续主机工具；使用新 shell 或恢复环境。核对 Go/Java/XML 生成物全部来自唯一版本源。
2. **正式文档审计**：固定受影响的 docs，交独立只读 subagent 审核；等待结论、修正并复检至 clean 后提交实现。审计时不修改正在审计的文件，不把尚未返回当成失败。
3. **实现提交后的完整构建与核验**，在干净工作树（source_dirty=false）的新 shell 执行：
   ```powershell
   ./scripts/build-release.ps1
   ./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
   ./scripts/build-host-tools.ps1
   ./scripts/build-host-tools.ps1 -VerifyOnly
   ./scripts/deploy.ps1
   ```
   手机整组包含服务、手机 MCP、控制 APK 与固定制品；禁止 SkipAndroid、跳过清单、卸载或清库。清单/设备共用契约 14，发行 0.14.0，App versionCode=16；主机两 exe 及 host-tools-manifest 与完整手机清单同源、干净且指纹相符。部署手机端后使其服务/适配器实际加载新产物；主机侧只构建并校验两 exe，不操作外部 Agent 进程，也不宣称外部宿主已经重新发现新工具。
4. **版本边界最小检查**：旧 `13` 普通 API/控制同步被新服务以 `409 contract_mismatch` 拒绝，新 `14` 可正常受理；最小 STOP 保持独立于普通契约。使用新的合法会话覆盖发布后的恢复屏障，不把端口启动等同于干净证明；不扩展无关恢复故障矩阵。
5. **最小实机验证**：同一设备/显示模式下寻找有外层动作、无动作正文及竞争子按钮的可控页面，记录包/版本、显示尺寸、发布身份与实际尝试数：
   - 正文 tap 被 422 拒绝且 O 未消费，有可证候选才出现完整祖先/坐标引导；分别在独立 O 上测试改传祖先 ref 与 safe_point，两者不复用已经消费的 O。
   - 点击含竞争子控件的容器 ref，确认派发点避开该子控件；同时记录输入 outcome 与后观察业务结果，不能仅用 executed 证明进入预期页面。
   - 至少一项 long_press ref 样本覆盖相同规划/固定点重验；页面没有合法长按祖先时记录不具备条件，不用 tap 样本替代。
   - 固定点动态覆盖、原点外变化、context、环与视图隔离以确定性合成测试为主，不依赖第三方应用偶然时序，也不为此扩大安装范围。实机无合适页面/设备时如实记录未执行及原因，不宣告实机闭环完成。

### 8.3 记录

实现后的验证记录写入 `maintenance/verification/act-tap-safe-scope-and-guidance.md`，包含分诊、实现提交、环境、实际命令/红绿结果、相关回归、发布与双清单核验、实机样本、未执行项及原因、正式文档审计结论。原始树、截图、日志与个人信息不入库；正文只保存必要的合成几何和受控结果摘要。

2026-11-15 本次提案审查仅对基线运行以下已有用例（Go 1.27.1、windows/amd64、GOEXPERIMENT=jsonv2），三组命令均通过；它们不覆盖拟新增行为，也不构成实机结论：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/engine -run '^(TestActRefTargetStillUsesIdentityOnly|TestActCoordinatePointRejectsUnreadableFreshProjection|TestActRevalidationDeliversFreshObservation|TestActDuplicateAfterDeliveryDoesNotPromiseTheObservation)$' -count=1
go test ./internal/protocol -run '^TestDecodeErrorResultWithDetails$' -count=1
go test ./internal/mcp -run '^(TestAgentResultLosslessParity|TestResultFieldManifest|TestCanonicalJSONDeterministic|TestCancellationDetailsParity)$' -count=1
```

## 9. 已定与未决

### 9.1 已定前提与修订判据

路线、动作范围、树形状、版本与发布边界来自用户已确认前提。下列具体判据是本次审查后的提案内容，随修订提案确认生效，不冒称已被单独确认或已经落地。

| 事项 | 结论 |
| --- | --- |
| 方案路线 | 采用 **R3 + R1**：`tap`/`long_press` 的 `target.ref` 子树安全落点规划 + 新鲜树固定落点重验 + `422` `error.details` 结构化引导 |
| 公开树 `Node` JSON 形状 | **保持不变**，不新增节点级字段 |
| `422` `error.details` 字段 | 祖先与 safe_point 同组可选，carrier 仅解释来源；R3 无候选且有中心阻塞证据才附 occluding_ref，不声称全表面遮挡 |
| 阻塞作用域范围 | 目标 C 子树内 tap/long_press/text 边界及其后代，context 按未知边界保守阻塞后代；不把外部同级节点计入 R3 阻塞或候选排序 |
| R1 与全局解析 | 双重谓词要求 pointUnique 且实际命中归属 A 非阻塞作用域；不能用 !=pointAmbiguous 代替，也不能只看 unique；无可证点则整组省略 |
| 祖先搜索 | N 自身也是边界检查起点；不跨已知交互或未知 context 边界，不静默晋升 |
| 退化与拓扑 | 非空相交后才取中心，深度 0 的 C 可命中，所有父链/索引有界；结构不可用与目标失配按 §4.1 区分 |
| projector 与交付 | work.Projector 优先、nil 才回退；Preview 失败 503 不交付新 O；409 只有既有交付门禁成功才有新 O |
| 详情所有权 | 统一嵌套 JSON 形态，承认 any 解码的数值类型转换；对发布/保留/视图的详情建立独立所有权并用三模式与重放测试证明 |
| 重验原则 | 只证明静态选定的固定坐标 `(plan.x, plan.y)` 在新鲜树中仍归属目标子树安全作用域，不在重验阶段改选坐标 |
| 契约与版本 | `contract_version` 升至 `14`，`release_version` 升至 `0.14.0`，`android_version_code` 升至 `16` |
| 发布与验证边界 | 手机端完成整组构建、部署与最小实机验证闭环；主机侧只更新构建 `device-mcp.exe` 与 `mcp-probe.exe` |

### 9.2 未决

没有悬空的算法选项。交接中的路线前提保持；本次仅审核修订提案，进入实现前由用户确认修订内容，不能将此任务当作实现、提交、发布或实机验收授权。实现前须复核 HEAD 和版本源；如发生版本占用冲突，由执行者在动版本前报告并协调，不能静默改写已指定的 0.14.0 / 14 / 16。

## 10. 实施顺序

1. 确认修订提案并按当时 HEAD 复核证据、版本与脚本；保留本提案未实现状态。
2. 编写 §8.1 的新行为用例并先红，记录原中心误派发、动态落点阻塞、R1 更深外部唯一命中与嵌套视图污染的反向对照；几何/规划与详情传输夹具可并行准备。
3. 实现 `tap_scope.go` 与 Act 接线、错误详情所有权，逐组转绿；不改其它动作判据。
4. 推进唯一版本源至 0.14.0 / 14 / 16 并生成；更新 MCP 描述/实际指纹、§6.5 全部受影响正式文档与 README，运行相关回归和 linux/arm64 构建。
5. 固定全部变更的 docs，发起独立只读审计，等待结论并修正/复检至 clean；实现提交说明注明结论。
6. 先提交实现，再在同一干净源码提交上整组构建、核验手机和主机清单、部署手机并执行 §8.2 最小闭环；主机只构建/校验两 exe。失败修正后重新走受影响门禁，不绕过 source_dirty 或清单检查。
7. 将本提案移入 `maintenance/proposals/implemented/`、补写 §11、重写相对链接，并编写 `maintenance/verification/act-tap-safe-scope-and-guidance.md` 单独提交归档。

## 11. 实现结果（归档时补写）

实现提交：`<待实现提交后填写>`。实现与验证记录：`../../verification/act-tap-safe-scope-and-guidance.md`（归档移动到 implemented/ 后的相对路径，本次不预建验证结论）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- 公开树 `Node`、`ReadNode`、`ReadTree`、`ReadDelta` 的 JSON 形状与紧凑编码规则；
- 不对无动作能力的 `target.ref` 做静默祖先晋升派发；
- 坐标目标 `{x, y}` 的 `resolvePoint` 最深节点解析与重验判据；
- `text`、`swipe`、`scroll`、`key` 分支的参数、判据与执行流程；
- 子树外同级重叠不导致 R3 ref 自拒的边界（`TestActRefTargetStillUsesIdentityOnly`）；
- `O` 的单向消费、STOP 撤权、重验失败交付门禁与日志白名单边界。
