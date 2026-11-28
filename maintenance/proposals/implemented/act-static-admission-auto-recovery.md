# 提案：act 全分支静态准入高置信自动恢复与 Issue #52 闭环

状态：已实现（`0.41.0` / 契约 37）。
基线提交：`43807f245b5d740731f119b5f4f4d7f56884fcb8`（分支 `master`，`2026-11-28`）。
适用范围：`act` 工具（`tap` / `long_press` / `text` / `text.submit_ref` / `scroll` / 坐标目标）认领前静态准入阶段（`phase=admission`）及其配套认领后重验联动（`phase=revalidate`）；关联并闭环 GitHub Issue #52（`https://github.com/malyjacob/mobile-use-vnext/issues/52`）。
影响范围：`internal/engine`（`scroll.go`、`scroll_overlay.go`、`tap_scope.go`、`point.go`、`act.go`、`act_target_shared.go`、`act_revalidation_policy.go`、`act_rebind.go`、`act_recovery.go`、`act_rejection_diagnostics.go` 及单元测试）、`internal/protocol/errors.go`（白名单注释同步）、`release/versions.json` 及 4 个生成版本产物、`docs/{interface,architecture,mcp,integration,runbook,client,control-protocol,phone-operations,release}.md` 及 `README.md`（按实际变更执行第 15 条独立只读文档审计）、`maintenance/verification/act-static-admission-auto-recovery.md` 与整组发布交付物。
契约（`/api/*` 字段与 `contract_version`）：**行为契约变化**——不增删 `/api/*` 请求/响应顶层字段、不改变 `Result.auto_recovery` 枚举与语义（静态解析沿用既有静默“弱保软”语义，不写 `Result.auto_recovery`）、不改变错误码表与 `candidates` 6 字段结构、不改变 MCP 13 工具 schema 与目录指纹；演进 9 项静态准入与重验联动语义，并在 `422 text_target_uncertain`（`stage="target"`、`reason="target_not_editable"`）拒绝且存在合法候选输入框时补充既有白名单键 `error.details.candidates`。通过单一版本源 `release/versions.json` 拟将版本由 `0.40.0 / contract_version=36 / android_version_code=42` 升级为 `0.41.0 / contract_version=37 / android_version_code=43`（本提案落盘不修改版本源）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[scroll.go](../../../internal/engine/scroll.go)、[scroll_overlay.go](../../../internal/engine/scroll_overlay.go)、[tap_scope.go](../../../internal/engine/tap_scope.go)、[point.go](../../../internal/engine/point.go)、[act.go](../../../internal/engine/act.go)、[act_target_shared.go](../../../internal/engine/act_target_shared.go)、[act_revalidation_policy.go](../../../internal/engine/act_revalidation_policy.go)、[act_rebind.go](../../../internal/engine/act_rebind.go)、[act_recovery.go](../../../internal/engine/act_recovery.go)、[act_rejection_diagnostics.go](../../../internal/engine/act_rejection_diagnostics.go)、[errors.go](../../../internal/protocol/errors.go)、[versions.json](../../../release/versions.json)）。

---

## 0. 摘要

继契约 36 落地认领后重验自动恢复后，本提案针对认领前静态准入阶段（`phase=admission`、零设备 I/O、基于同一已发布 `O`）采用 **R2 全 `act` 高置信静态自动恢复路线**：系统性补齐 9 项因 Android 层级嵌套、纯被动节点重叠、边缘安全带漏采样及异动作同视口覆盖造成的静态误拒——包括 **Issue #52 同界异轴内嵌滚动容器（A1）**、非滚动包装壳唯一内层滚动下探（A2）、缺 `swipe` 的 `role=scroll` 节点向兼容滚动祖先提升（A3）、`text(ref)` 包装壳/占位子/共位输入框定向绑定（D1，失败时补附 `candidates`）、同目标纯被动子节点重叠消歧（E1）、2D 残余安全区求点（E2）、`text(ref)` 与带小角标同视口子容器委托（E3）、`target_has_other_action` 向下唯一同视口子委托（B1）以及共位与坐标嵌套拥有者最内层消歧（E4/F1）——并同步对齐认领后重验防线。所有静态恢复继续保持**静默“弱保软”语义**（不污染 `Result.auto_recovery`，不增删工具与 schema），严守无被动文字盲点、无跨区盲滚、无密码框/显式禁用绕过红线。

---

## 1. 问题

### 1.1 现象与代价

分诊结论：**取舍 / 规范演进**（现行静态拒绝与重验行为忠实执行契约 36 规范，须经提案与契约升级演进，不得在实现里暗改语义）。

真实设备与规则审计中观测到以下 9 类可避免的静态准入拒绝，均发生在未认领 `O`、零设备 I/O 的 `phase=admission` 阶段，每次拒绝至少迫使调用方多付出 1 轮大模型推理与 1 次 `act` 调用往返：

1. **Issue #52（`scroll` 同界异轴内嵌滚动容器报 `422 axis_mismatch`）**：
   - 小黑盒首页观察 `O8219_13`（`1080×2400@294`）中，外层横向分页容器 `n31 scroll [0,245,1080,2242] axis=x` 包裹唯一同界纵向列表 `n37 scroll @ axis=y`。调用方请求 `act(scroll, container={ref:"n31"}, direction="down", amount="page")` 时，服务端返回 `422 unsupported_node_action`（`reason="axis_mismatch"`），并在 `error.details.candidates[0]` 给出 `{"ref":"n37","executable":true}`；调用方随后原样换成 `n37` 用同一 `O8219_13` 重试即成功，白耗 1 轮调用。
2. **非滚动外层容器壳报 `422 not_scrollable` / `axis_mismatch`（A2）**：
   - 调用方传入侧边抽屉外层 `ViewGroup`、Tab 内容页 `FrameLayout` 或居中弹窗壳请求 `scroll(ref)` 时，因该节点既非 `role=scroll`、向上又无兼容滚动祖先，直接返回 `422 not_scrollable` 并于 `candidates` 列出其内部唯一滚动子容器，迫使再发一轮。
3. **`NestedScrollView > RecyclerView(scrollable=false)` 报 `422 target_missing_action` / `ancestor_missing_action`（A3）**：
   - Android 嵌套滚动中内层列表常将 `scrollable` 置为 `false`（仍带 `role=scroll` 但缺 `swipe` 动作），由外层 `NestedScrollView` 实际承接滚动。调用方指向内层列表或其内部文本项时，静态检查遇到缺 `swipe` 的未禁用 `role=scroll` 即停步拒绝，而实际上拖动该位置物理事件会自然落给外层兼容祖先。
4. **`text(ref)` 指向输入框包装壳或内部占位文本报 `422 text_target_uncertain (target_not_editable)` 且无 `candidates`（D1）**：
   - 搜索栏外层可点击包装容器、输入框内占位 `TextView`、或扁平布局下盖在 `EditText` 上的占位标签没有 `role=input`，既被静态拒绝，又因 `staticTextError` 不附带 `candidates`，导致调用方甚至拿不到同帧可用输入框列表。
5. **同目标纯被动子节点重叠报 `422 no_safe_point (ambiguous)`（E1）**：
   - 可点击卡片内同时叠放同大小的封面 `ImageView` 与标题 `TextView`（两者均无交互能力），`tap(ref)` 时 `provesSubtreeTapPoint` 因最深节点并列 2 个而直接拒死，而相同位置传坐标 `tap(x,y)` 反而能通过 `pointSet` 单拥有者规则直接点击。
6. **4 级固定采样遗漏局部安全带报 `422 no_safe_point (blocked)`（E2）**：
   - 卡片中间 `10%–90%` 被子按钮覆盖、但四周留有足够宽度的安全空白区时，中心、载体、两条中心线与 `3×3` 网格（`1/6, 1/2, 5/6`）仍可能全部撞中阻塞子控件而报错。
7. **`TextInputLayout` 与含小角标的同视口子容器报 `422 no_safe_point (blocked)`（E3）**：
   - `text(ref)` 写死禁止 `coextensive_child`，导致外层输入容器包裹同视口 `EditText` 必败；而 `tap`/`long_press` 的 `coextensive_child` 硬要求 `len(blockers)==1`，只要同视口可点击子容器旁多出 1 个小删除/角标按钮即整卡拒绝。
8. **异动作容器包裹同视口子控件报 `422 unsupported_node_action (target_has_other_action)`（B1）**：
   - 外层容器 `C` 仅有 `tap`（而请求 `long_press`），其内部唯一同视口顶层子容器 `B` 明确支持 `long_press` 时，仍因 `hasAction(C, action)` 为假且 `isTouchInteractive(C)` 为真而被直接拒绝。
9. **共位与坐标嵌套拥有者假歧义报 `colocated_ambiguous` / `ambiguous_point`（E4/F1）**：
   - `bubble_colocated` 遇外层背景容器（`inChild=false`）与正下方子按钮（`inChild=true`）同时覆盖 `C` 中心时判冲突；坐标目标最深命中两叶分别归属可点父卡片与其中可点子按钮时，虽按 Android 事件分发必然命中内层按钮，仍报 `422 ambiguous_point (competing_targets)`。

### 1.2 证据链

全部 9 项静态入口与认领后重验耦合点均已在基线 `43807f245b5d740731f119b5f4f4d7f56884fcb8` 逐行核实：

| 通路 | 基线源码与测试落点 | 核实事实 |
| :--- | :--- | :--- |
| **A1（Issue #52）** | [scroll.go:1024-1029](../../../internal/engine/scroll.go#L1024-L1029)、[scroll.go:1388-1453](../../../internal/engine/scroll.go#L1388-L1453)、[scroll_overlay.go:259-295](../../../internal/engine/scroll_overlay.go#L259-L295)、[scroll_axis_amount_test.go:564-599](../../../internal/engine/scroll_axis_amount_test.go#L564-L599) | `axis_mismatch` 第 2 步调 `tryUniqueInnerScrollFallback`；`L1406` 硬检查 `!isCoextensiveRect(wantRect, descRect)` 拦截同界子容器，而 `L1439-L1444` 本已具备同视口折叠逻辑；且 `tryUniqueInnerScrollFallback` 未调用 `scroll_overlay.go` 遮罩过滤。注意 `isScrollCandidateOverlayOccluded(ctx, want, ...)` 对非空 `want` 会用 `wantDepth <= commonAncestorDepth(want, scrim)` 豁免外层遮罩叶，必须传 `want = nil` 才不放行外部遮罩。 |
| **A2（非滚动壳内探）** | [scroll.go:1048-1055](../../../internal/engine/scroll.go#L1048-L1055)、[scroll.go:1099-1106](../../../internal/engine/scroll.go#L1099-L1106) | `len(ancestors)==0` 或全部兼容祖先失败/无兼容祖先时直接收集 `page_scroll` 候选并返回 `not_scrollable` / `axis_mismatch`，从不检查自身子树是否存在唯一可执行滚动子容器。 |
| **A3（缺 `swipe` 滚动提升）** | [scroll.go:1041-1047](../../../internal/engine/scroll.go#L1041-L1047)、[scroll.go:1056-1085](../../../internal/engine/scroll.go#L1056-L1085)、[act_proposal_scroll_coord_test.go:112-163](../../../internal/engine/act_proposal_scroll_coord_test.go#L112-L163) | `target.Role==RoleScroll` 缺 `ActionSwipe` 在 `L1041` 直拒；非滚动节点遇 `nearest` 缺 `ActionSwipe` 在 `L1066` 停止，尽管 `scrollRoleAncestors` 已收集完整祖先链且 `L1077` 在循环中过滤 `isScrollContainer(ancestor)`。 |
| **D1（`text(ref)` 定向绑定与候选）** | [tap_scope.go:2528-2573](../../../internal/engine/tap_scope.go#L2528-L2573)、[act.go:2153-2171](../../../internal/engine/act.go#L2153-L2171)、[tap_scope.go:1906-2000](../../../internal/engine/tap_scope.go#L1906-L2000)、[act_rebind.go:69](../../../internal/engine/act_rebind.go#L69)、[act_recovery.go:64-76](../../../internal/engine/act_recovery.go#L64-L76) | `L2565-2567` 遇 `target.Role != RoleInput \|\| !hasAction(target, ActionText)` 直接调用 `staticTextError(textReasonTargetNotEditable)`；`staticTextError` 只输出 `stage/reason/dispatch`，未调用已存在的 `collectTextFallbackCandidates`（`L1906`）；`act_rebind.go:69` 用 `req.TargetRef` 查 `oldView.byRef`，且 `act_recovery.go:70` 对 `text` 未排除 `delegateRaw != nil`。 |
| **E1（被动子重叠）** | [tap_scope.go:635-678](../../../internal/engine/tap_scope.go#L635-L678)、[tap_scope.go:840-863](../../../internal/engine/tap_scope.go#L840-L863)、[tap_scope.go:895-920](../../../internal/engine/tap_scope.go#L895-L920)、[act_tap_scope_test.go:349-374,1046](../../../internal/engine/act_tap_scope_test.go#L349-L374)、[tap_static_error_test.go:159-219,546-552,653-687](../../../internal/engine/tap_static_error_test.go#L159-L219) | `provesSubtreeTapPoint`（`L671`）强制 `bestCount == 1`；`provesGuidancePoint`（`L902`）强制 `state == pointUnique`；`tryPlanTapDirectBase`（`L857-861`）只枚举原 4 级+单命中；同目标下两个重叠被动 `TextView`/`ImageView` 均在 `scope.carrierByNode` 内却被拦截。 |
| **E2（2D 残余安全区）** | [tap_scope.go:726-793](../../../internal/engine/tap_scope.go#L726-L793)、[tap_scope.go:981-1019](../../../internal/engine/tap_scope.go#L981-L1019) | `enumerateTapCandidates` 与 `tapVisualRegionPoints` 在 `3×3` 网格（`tapStaticGridPoints`）后即终止，无阻塞区差集二维矩形求点。 |
| **E3（同视口子委托）** | [tap_scope.go:2571](../../../internal/engine/tap_scope.go#L2571)、[tap_scope.go:1210-1233](../../../internal/engine/tap_scope.go#L1210-L1233)、[tap_scope.go:2120-2150](../../../internal/engine/tap_scope.go#L2120-L2150) | `L2571` 固定传 `allowCoextensiveChild=false`；`L1221` 硬要求 `len(blockers) == 1`。 |
| **B1（异动作同视口子委托）** | [tap_scope.go:2506-2521](../../../internal/engine/tap_scope.go#L2506-L2521) | `!hasAction(target, action)` 时仅在 `!isTouchInteractive(target)` 进入冒泡，否则直接返回 `target_has_other_action`，不探测同视口子容器。 |
| **E4 / F1（共位与坐标最内层消歧）** | [tap_scope.go:2335-2377](../../../internal/engine/tap_scope.go#L2335-L2377)、[point.go:179-208](../../../internal/engine/point.go#L179-L208)、[scroll.go:1140-1152](../../../internal/engine/scroll.go#L1140-L1152) | `L2340-2349` 只统计 `containsCenter` 总数；`point.go:190-191` 只判 `len(matching) == 1`；而 `scroll.go:1140-1152` 已具备最内层归约先例。 |
| **认领后重验强耦合点** | [act_revalidation_policy.go:1710-1774](../../../internal/engine/act_revalidation_policy.go#L1710-L1774)、[act_target_shared.go:241-258](../../../internal/engine/act_target_shared.go#L241-L258)、[act.go:1142,1163,1180-1187,1357-1364,1608-1618](../../../internal/engine/act.go#L1142-L1187)、[point.go:617-621](../../../internal/engine/point.go#L617-L621)、[act.go:1748-1751](../../../internal/engine/act.go#L1748-L1751)、[act_rejection_diagnostics.go:167-175](../../../internal/engine/act_rejection_diagnostics.go#L167-L175) | `pointOwner`/`tapFrameTier4OcclusionProven` 以及 `resolveDirectTapScopeFrame:1616`/`revalidateColocatedTapFrame:1362` 二次选点均要求单命中；`revalidateCoextensiveChildFrame` 要求 `freshParent` 具原动作且 `len(blockers)==1`，且 `L1183` 仅在 `tier>=4` 查外部覆盖；`proveTargetSet`/`provePointTarget` 要求未归约 `len(matching)==1`。 |

### 1.3 为什么直觉会出错

1. **“让 `tap`/`long_press` 像 `scroll` 一样跳过 `target_has_other_action` 或 `ancestor_missing_action` 向上冒泡”**：
   在 Android `View.onTouchEvent` 中，只要子节点具备 `CLICKABLE` 或 `LONG_CLICKABLE`（含禁用态），它会消费 `ACTION_DOWN` 阻断向父级冒泡；且 [`collectTapScope`](../../../internal/engine/tap_scope.go#L583) 已把 `isTouchInteractive` 节点视为父级阻塞区。越级向上会把点击飘移到父容器无关空白处，故 `B1` 只能**向下委托同视口子容器**，严禁向上越级。
2. **“Issue #52 直接删掉 `scroll.go:1406` 的 `!isCoextensiveRect` 即可”**：
   `tryUniqueInnerScrollFallback` 当前没有任何遮罩/跨窗隔离。如果不接入 [`scrollOverlayContext`](../../../internal/engine/scroll_overlay.go#L259-L295) 并以 `want = nil` 禁止外层遮罩豁免，放开同视口内层回退与 A2 包装壳下探后，在模态遮罩页传外层容器就会穿透滚到底层滚动容器。
3. **“把 `no_interactive_ancestor` 自动转成被动中心点击，或者把非滚动/非可点容器里唯一的 `<50%` 小按钮也吸走”**：
   前者会让静态标题误点直接消费 `O` 并消耗 1~2s 后观察（且与契约 35 刚确立的 `target_center` 客观提示语义相悖）；后者会把信息卡内唯一的 `"X"` 关闭/删除按钮当成整卡操作目标误点。
4. **“只改静态规划函数，不动 `phase=revalidate`”**：
   E1（被动多命中、含重验二次重算落点）、B1/E3（同视口子委托与角标外覆盖）、F1（坐标嵌套拥有者）、D1（`text` 解析后与 `text_rebind` 的 ref 对齐及 `delegateRaw` 拦截）在重验期有直接对等守卫；若不同步修改，会出现“静态准入放行并消费 `O`，随后认领后重验 100% 失败返回 `409`”。
5. **“给 Issue #52 的 422 报错新增 `resolved_container`/`fallback_ref`/`safe_to_retry` 字段”**：
   契约 22 已统一清理历史碎片键并建立正交 6 字段 `candidates` 契约；且在 Issue #52 场景下系统自动直选 `n37` 一次执行成功，真正失败的多候选场景用既有 `candidates` 即可决策，无需膨胀白名单键。

---

## 2. 目标与非目标

### 2.1 目标

1. **A1（关闭 Issue #52）**：`scroll(ref)` 在 `axis_mismatch` 路径调用 `tryUniqueInnerScrollFallback` 时允许双向同视口（`>=85%`）轴向兼容内层子容器，并叠加全量遮罩/浮层过滤；一次调用直接滚动 `n37` 而不报 `422 axis_mismatch`。
2. **A2（非滚动壳内探唯一滚动子容器）**：`scroll(ref)` 对非滚动节点向上查找滚动祖先无解时，在满足遮罩过滤、同视口折叠且占主体（`>=50%` 覆盖）前提下，委托子树内唯一可执行且轴向兼容的滚动容器。
3. **A3（缺 `swipe` 的未禁用 `role=scroll` 向上兼容提升）**：`scroll(ref)` 遇到未禁用但缺 `ActionSwipe` 的 `role=scroll` 节点（目标自身或最近滚动祖先）时，若其最近滚动祖先亦非显式禁用/无几何，则继续沿 `scrollRoleAncestors` 向上寻找最近有效且轴向兼容的滚动祖先规划；无解时保留原语义错误。
4. **D1（`text(ref)` 定向绑定与候选补充）**：针对 `target_not_editable`，按“非大屏包装壳唯一 `RoleInput` 后代（D1a）→ 非交互占位子最近交互祖先为唯一合法输入框（D1b）→ 非交互目标唯一共位合法输入框（D1c）”三条确定性路径绑定并聚焦写入，严禁绕过密码/禁用框；未命中时在 `422 text_target_uncertain (target_not_editable)` 附带 `candidates`（非空时）。
5. **E1（同目标纯被动子节点重叠消歧）**：保留单命中优先，允许落点全部最深命中均属于当前目标非阻塞被动载体且无外部交互拥有者覆盖时通过静态、干跑 `executable` 与重验证明。
6. **E2（2D 残余安全区求点）**：在固定 4 级采样之后、借助极大自由矩形（`>=16×16px`）补算确定性安全点，消除有安全边距但 `3×3` 失手的误拒。
7. **E3（同视口子委托补强）**：`text(ref)` 放开仅针对合法可输入同视口子框的委托（E3a）；`coextensive_child` 允许在唯一同视口子容器旁存在 `<=25%` 面积的小型非同视口交互角标（E3b），并强制外部交互拥有者零覆盖证明。
8. **B1（`target_has_other_action` 向下唯一同视口子委托）**：`bubble=true`（默认）且 `isTouchInteractive(target)` 缺当前请求动作时，按 E3b 规则尝试委托向下唯一支持该动作的同视口子容器；不向上冒泡。
9. **E4 / F1（共位与坐标嵌套拥有者最内层消歧）**：`bubble_colocated` 优先 `inChild && containsCenter` 及嵌套最内层；坐标 `tap`/`long_press`/`text` 对匹配请求动作的交互拥有者集合做祖先-后代最内层归约，静态、诊断与重验全链路一致。
10. **发布与闭环**：按 `AGENTS.md` 第 19、24 条完成契约 `36 → 37`（`0.41.0` / `versionCode 43`）整组发布、双端产物替换、双入口 `mcp-probe` 校验、文档审计、先实现后归档提交、推送远程并回复关闭 GitHub Issue #52。

### 2.2 非目标

- **不改变 `Result.auto_recovery` 契约**：静态恢复沿用既有“弱保软”静默语义，绝不写 `Result.auto_recovery`；
- **不改变 MCP 13 工具 schema 与目录指纹**，不新增请求参数或 `error.details` 白名单键名；
- **不自动恢复硬红线出口**：`invalid_request`、`invalid_coordinates`、`unsupported_key`、`session_*`、`observation_*`、`node_not_found`、`node_disabled`、`root`/`context`/`no_geometry`、`target_protected`、显式 `bubble=false`、`text.submit_ref` 转 `enter_key`、密码/禁用框、同级多解真歧义；
- **不纳入第二梯队敏感项**：不做 `no_interactive_ancestor` 自动被动中心点按，不做 `no_scroll_container` 全页盲滚，不做 `scroll(step)` 最小行程下限钳位（若后续需要可单独提案）；
- **不修改固定制品 `third_party/uiautomator/u2.jar`、Android 控制 App 业务逻辑或 `internal/eventlog` 白名单键集合**。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| :--- | :--- | :--- | :--- |
| R1 最小高频收口 | 仅做 A1（Issue #52）+ D1（`text` 定向绑定）+ E1（被动重叠）+ E4/F1（嵌套消歧） | 改动小，无需 2D 残余矩形算法 | `NestedScrollView>RecyclerView`、抽屉壳滚动、边缘安全带、`TextInputLayout` 壳、带角标同视口子框及异动作叠层仍持续静态拒绝 |
| **R2（选定）全 `act` 高置信静态恢复 + 静默弱保软** | 一次收口第一梯队 9 项（A1/A2/A3 + D1 + E1/E2/E3 + B1 + E4/F1）并对齐重验与干跑防线；`target_not_editable` 未恢复时补 `candidates`；沿用静默“弱保软”语义，不写 `Result.auto_recovery` | 需修改 `internal/engine` 多文件与重验路径，需同步修订 `docs/` 与更新 5 处旧保守测试断言，升级共同契约 `36 → 37` | 仍不能跨越无遮罩全页盲滚或点击无无障碍能力声明的纯自绘被动控件；同级真歧义仍须退回调用方选 `candidates` |
| R3 激进容错 | R2 + `no_interactive_ancestor` 自动被动中心点按 + `no_scroll_container` 自动全页唯一容器滚动 | 进一步减少 422 次数 | 误点静态标题会消费 `O` 并消耗 1~2s 后观察；小弹窗无遮罩时坐标滚动易滚走底层主页面；与契约 35 `target_center` 语义冲突 |

**选型与可见性结论（用户已确认）**：
1. 采纳 **R2**；
2. 调用方可见性保持既有**静态“弱保软”静默语义**（与 `bubble_ancestor`、`bubble_colocated`、`coextensive_child`、`scroll_axis_fallback`、`scroll_inner_fallback` 一致，`Result.auto_recovery` 仍专属于 `phase=revalidate` 活帧恢复）；
3. `text(ref)` 在 `D1` 未命中而返回 `422 text_target_uncertain (stage="target", reason="target_not_editable")` 且存在合法可输入节点时，补充既有白名单键 `error.details.candidates`（与 `no_geometry` 一致，由主代理决定纳入）。

---

## 4. 判据

本节所有面积与比例判断均使用 `int64` 整数运算（严禁浮点误差），矩形与区间使用半开边界 `[Left, Right)`、`[Top, Bottom)`。所有策略均在**锁外**基于当前 `ready` 观察的不可变投影执行，**不认领 `O`、不触发采集与设备 I/O、不阻断 `STOP`**。

### 4.1 滚动静态自动恢复（A1 / A2 / A3）

1. **内部滚动兜底参数化与遮罩感知**：
   扩展 [`tryUniqueInnerScrollFallback`](../../../internal/engine/scroll.go#L1388) 为带策略参数 `(allowCoextensive bool, minCoverPercent int64)` 并接入 [`newScrollOverlayContext`](../../../internal/engine/scroll_overlay.go#L32)：
   - 扫描 `want` 子树内全部满足 `isScrollContainer(node)` 且 `scrollContainerAxisCompatible(obs, view, node, operation.Direction)` 的严格后代 `node`（含裁剪面积正值与有效 `source`）；
   - **同视口约束**：若 `!allowCoextensive && isCoextensiveRect(wantRect, descRect)` → 跳过（保持原同轴轨迹失败路径不变）；
   - **面积占比约束**：若 `minCoverPercent > 0 && !rectOverlapAtLeast(wantRect, descRect, minCoverPercent)` → 跳过（防止大非滚动页面壳误委托给仅占局部角落的小横条/小列表）；
   - **遮罩/浮层隔离**：调用 **`isScrollCandidateOverlayOccluded(overlayCtx, nil, node, operation.Direction, operation.Amount)`**（明确传 `want = nil`，防止 [`scroll_overlay.go:274-284`](../../../internal/engine/scroll_overlay.go#L274-L284) 因 `wantDepth <= commonAncestorDepth(want, scrim)` 豁免位于 `want` 之外或跨窗口的模态遮罩叶）；返回真则剔除。同时检查该后代是否与任意多窗口中的其他非己顶层窗口存在无遮罩重叠的限制沿用既有 `scrollSafeSpansAt` 与 `scrollOverlayContext` 保守规则；
   - 对能通过 `planScrollOnAxes(..., "ref", scrollModeInnerFallback)` 的候选，执行既有的双向同视口祖先-后代折叠（保留最内层）；**当且仅当折叠后总数严格 `== 1` 时委托返回该唯一 `scrollPlan`**，否则返回 `nil`。
2. **A1（Issue #52，`axis_mismatch` 路径）**：
   - 在 [`resolveScrollRefPlan`](../../../internal/engine/scroll.go#L1026-L1029) 中，当 `isScrollContainer(target)` 且 `!scrollContainerAxisCompatible(...)`、沿父链向上回退未找到成功计划时，调用 `tryUniqueInnerScrollFallback(..., allowCoextensive=true, minCoverPercent=0)`（优先允许同视口且轴向兼容的唯一可执行内层子容器，如横向 `ViewPager` 内同界纵向 `RecyclerView`）；
   - 同轴容器但 3 轴线轨迹失败路径（[`planScrollContainer:1377`](../../../internal/engine/scroll.go#L1377)）保持 `allowCoextensive=false, minCoverPercent=0`，避免同视口自身再次循环落回本已被证明同视口免阻塞的子容器。
3. **A2（非滚动包装壳唯一内层滚动下探）**：
   - 在 [`resolveScrollRefPlan`](../../../internal/engine/scroll.go#L1048-L1106) 对非滚动 `target` 仍优先执行向上查找滚动祖先与祖先提升；
   - 当且仅当向上未得到任何可执行的兼容滚动祖先计划（即 `len(ancestors) == 0`，或所有有效祖先均因 `axis_mismatch` 被跳过且 `firstCompatErr == nil`）时，仅在 `targetRect` 有效的前提下调用 `tryUniqueInnerScrollFallback(..., allowCoextensive=true, minCoverPercent=50)`（要求唯一可执行子滚动容器覆盖 `targetRect` 至少 `50%` 面积，且通过 `want = nil` 的遮罩/浮层过滤）；
   - 注意：当 `nearest` 滚动祖先禁用/无几何（`ancestor_invalid`）或有轴向兼容祖先进入规划但因轨迹阻塞失败（`firstCompatErr != nil`）时，**不触发** A2，保持既有失败归因。
4. **A3（缺 `swipe` 的未禁用 `role=scroll` 向兼容祖先提升）**：
   - 前提门禁：`target.Enabled != nil && !*target.Enabled`（已处理于 `L992` 返回 `409 node_disabled`）及最近滚动祖先禁用/无几何（`ancestor_invalid`）**绝不跳过**；
   - 当 `target.Role == protocol.RoleScroll && !hasAction(target, protocol.ActionSwipe)`（`L1041`）时：先取 `ancestors := scrollRoleAncestors(view, target)`，若 `len(ancestors) > 0` 且最近祖先 `ancestors[0]` 显式禁用或无裁剪几何，按 `ancestor_invalid` 停步返回（绝不跨过禁用首层祖先提升至更外层 `ancestors[1]`）；若最近祖先未禁用且有几何，则与非滚动节点 `nearest` 缺 `ActionSwipe`（`L1066`）统一沿 `ancestors` 向上遍历：
     - 跳过不满足 `isScrollContainer(anc)`、无裁剪几何或 `!scrollContainerAxisCompatible(obs, view, anc, operation.Direction)` 的祖先；
     - 对兼容且可规划的祖先调用 `planScrollContainer(..., scrollModeAncestorLift, false, true)`，首个成功即返回该 `scrollPlan`；
     - 失败收口优先级：若遍历期间至少有 1 个兼容祖先进入 `planScrollContainer` 但均失败 → 返回首个 `firstCompatErr`；否则若向上至少跳过了一个因 `axis_mismatch` 不兼容的有效滚动祖先（`isScrollContainer(anc)` 且有几何）→ 返回 `422 unsupported_node_action (axis_mismatch)`；否则按原入口返回 `422 unsupported_node_action (target_missing_action` 或 `ancestor_missing_action)` + 既有过滤排序后的 `candidates`。

### 4.2 `text(ref)` 静态定向绑定与候选补充（D1）

在 [`staticTextRefCheck`](../../../internal/engine/tap_scope.go#L2565-L2570) 先通过 `target` 存在、`!root`、`!context`、有正几何（否则 `no_geometry`）、`enabled != false`（否则 `409 node_disabled`）检查；若 `target.Role == RoleInput && hasAction(target, ActionText)` 则检查 `protected == false`（否则直接报 `target_protected`，**绝不恢复也不附 `candidates`**）。
仅当 `target.Role != RoleInput || !hasAction(target, ActionText)`（即原 `target_not_editable` 出口）时，按顺序执行三条高置信定向解析：

1. **D1a（非大屏包装壳向下找唯一 `RoleInput`）**：
   - 若 `target.Role != RoleInput`（不绕过本身已是 `RoleInput` 但显式缺少 `ActionText` 的受限/只读输入框）且 `rectAreaLessThanPercent(targetRect, screen, 50)`（屏幕半面积以下容器才视作输入包装壳，防全屏页容器）；
   - 沿 `target` 严格公开后代遍历，**遇到 `node.Context` 时整棵子树剪枝不向下进入**（与 `collectTapScope`、`topLevelTapScopeBlockers` 一致），不跨 `root`：统计满足 `node.Role == RoleInput` 的节点集合 `inputs`（**包含禁用、受保护或缺 `text` 动作的所有 `RoleInput`，反歧义与反绕过密码框**）。
   - 仅当 `len(inputs) == 1`、记 `C := inputs[0]` 且 `isValidTextPublicTarget(C)`、与 `targetRect` 有正面积交集（`!cRect.Intersect(targetRect).Empty()`），且对 `C` 运行直接聚焦规划（含 §4.3 的 E1/E2/E3a）成功时 → 选用 `C`。
2. **D1b（非交互占位子节点向上找最近合法输入框）**：
   - 若 `D1a` 未命中且 `!isTouchInteractive(target)`（仅针对纯展示文本/图标占位，不越过本身可点/可输控件）：
   - 取 `anc := nearestInteractiveAncestor(view, target)`（跳过 `context`、止于 `root`）。仅当 `anc != nil && isValidTextPublicTarget(anc)` 且对 `anc` 运行直接聚焦规划成功时 → 选用 `anc`（若 `anc` 为普通按钮或受保护/禁用输入框，**立即停手，绝不向更高层祖先找**）。
3. **D1c（非交互目标共位吸附唯一合法输入框）**：
   - 若 `D1a`、`D1b` 均未命中且 `!isTouchInteractive(target)`：
   - 扫描全树满足 `node != target && !node.Context && !isPublicAncestor(view, node, target) && node.Role == protocol.RoleInput && colocatedMatchGuard(view, target, node, screen)` 的全部 `RoleInput`（同样包含禁用/受保护框计数防绕过）；
   - 仅当总数严格 `== 1`、记 `B := matches[0]` 且 `isValidTextPublicTarget(B)`、并对 `B` 运行直接聚焦规划成功时 → 选用 `B`。
4. **绑定结果与失败候选**：
   - D1 任一分支成功时，返回的 `tapPlan` 以实际解析出的输入节点 `resolved` 作为 `targetRef = resolved.Ref` 与 `targetRaw = raw(resolved)`；
   - 若三条均未命中，保持返回 `422 text_target_uncertain`（`stage="target"`、`reason="target_not_editable"`、`dispatch={focus:"not_attempted", write:"not_attempted", submit:"not_attempted"}`），并通过 `collectTextFallbackCandidates(view, target, screen)` + `buildStaticCandidates` 计算可选输入框列表；仅当非空时写入 `error.details["candidates"]`（需在 [`internal/protocol/errors.go`](../../../internal/protocol/errors.go#L45-L53) 同步登记该出口允许可选 `candidates`）。

### 4.3 落点安全证明、残余安全区与同视口子委托（E1 / E2 / E3 / B1）

1. **E1（同目标纯被动子节点重叠消歧）**：
   - 保留 [`provesSubtreeTapPoint`](../../../internal/engine/tap_scope.go#L635) 与 [`provesGuidancePoint`](../../../internal/engine/tap_scope.go#L895) 既有单最深命中（`bestCount == 1` / `pointUnique`）行为不变，作为优先判定；
   - 新增被动多命中谓词：
     - `provesSubtreeTapPointPassive(scope, x, y)`：点在 `scope.base` 内、不命中任何 `scope.blocked`、经 [`tapSubtreePointOwners`](../../../internal/engine/tap_scope.go#L1276) 所得最深命中 `ties` 满足 `len(ties) >= 1` 且对每个 `hit ∈ ties`，要么 `hit == scope.target`、要么 `scope.carrierByNode[hit] != nil`（全部属于本目标非阻塞被动载体，没有任何非载体节点混入）；
     - `provesGuidancePointPassive(view, ancestor, scope, x, y)`：在 `provesSubtreeTapPointPassive(scope, x, y)` 基础上，要求 `len(externalInteractiveOwnersAt(view, ancestor, x, y)) == 0`，且整树 `resolvePointSet(view.tree.Root, x, y)` 非空并全部命中 `ancestor` 本身或 `scope.carrierByNode` 内拥有有效 `source` 的节点。
   - 被动多命中通过时，`tapPlan.carrierRef = ""`、`tapPlan.carrierRaw = nil`（不绑定单一脆弱载体）；
   - 同步更新干跑评估器 [`tryPlanTapDirectBase`](../../../internal/engine/tap_scope.go#L840-L863)（对齐 `chooseTapPlan` 的 E1/E2 规则，保证 `candidates[].executable` 与真实静态准入 100% 一致）。
2. **E2（2D 残余安全区求点 `residualPatchPoints`）**：
   - 适用：`base \ ∪ blocked` 平面上固定 4 级采样全败的直接目标、载体目标与 `bubble` 视觉区；
   - **算法边界常数**：离散坐标点数上限 `tapResidualMaxCoords = 128`（包含 `region` 与所有相交 `blocked` 矩形的去重 X、Y 边界；若超出上限，直接跳过该级不退化为慢扫描）、最小安全块尺寸 `tapResidualMinPatchPx = 16`（要求自由矩形 `Width >= 16 && Height >= 16`，杜绝 `1–2px` 边缘细缝）、最大输出点数 `tapResidualMaxCandidates = 8`；
   - **生成与排序**：在 `[0..len(xs)-2] × [0..len(ys)-2]` 自由网格单元（未被任何 `blocked` 覆盖）上以直方图最大矩形在 `O(|xs|·|ys|)` 内枚举极大自由矩形（或去重后的最大自由块），滤除 `Width < 16 || Height < 16` 者；按 `min(w, h)` 降序 → `w*h` 降序 → `Top` 升序 → `Left` 升序排序，取前至多 8 个矩形的安全中心 `tapSafeCenter(rect)`（战略名 `tapModeResidualPatch = "residual_patch"`）。
   - **两级总探测顺序（单命中严格优先于被动多命中）**：
     - 直接目标 [`chooseTapPlan`](../../../internal/engine/tap_scope.go#L817)：Pass 1 原有 4 级（`default_center` → `carrier_node` → `center_span` → `grid_3x3`）经 `provesSubtreeTapPoint` → Pass 2 `residual_patch` 经 `provesSubtreeTapPoint` → Pass 3 原有 4 级 + `residual_patch` 经 `provesSubtreeTapPointPassive`；
     - `bubble_ancestor` [`chooseBubbleTapPoint`](../../../internal/engine/tap_scope.go#L1047)：在 `cClip` 内先试原有 `tapVisualRegionPoints`（单命中）→ `cClip` 残余安全区（单命中）→ `cClip` 被动多命中；无竞争遮挡补测 `A` 自身范围时亦同；
     - `bubble_colocated` [`chooseColocatedTapPoint`](../../../internal/engine/tap_scope.go#L1101)：先试 `childRect ∩ scope.base` 视觉点（单命中 → 残余安全区单命中 → 被动多命中），再委托候选 `B` 的 3-pass 直接规划（确保静态放行的 E1/E2 共位点在重验 `chooseColocatedTapPoint` 时对称可恢复）。
3. **E3（同视口子委托补强）**：
   - **E3a**：[`staticTextRefCheck`](../../../internal/engine/tap_scope.go#L2571) 向 [`planDirectTapRef`](../../../internal/engine/tap_scope.go#L2087) 传入 `allowCoextensiveChild = true`；但在 `action == ActionText` 分支下，仅允许委托给满足 `isValidTextPublicTarget(delegate)`（即非密码、未禁用、`RoleInput`、含 `ActionText`）的唯一同视口子容器，绝不委托给非输入按钮或密码框。
   - **E3b**：重构 [`isTopLevelCoextensiveBlockerAllowed`](../../../internal/engine/tap_scope.go#L1210) 与 [`planDirectTapRef:2120-2150`](../../../internal/engine/tap_scope.go#L2120-L2150) 为统一辅助函数 `findUniqueCoextensiveBlocker(scope, view, parent, screen, action, allowInitiallyDisabled)`：
     - 从 `blockers := topLevelTapScopeBlockers(scope, parent)` 中筛选同时满足 `!b.Context` 且 `publicNodeClippedRect(b, screen)` 有效、并与 `parentRect` 满足 `isCoextensiveRect(bRect, parentRect)` 的成员集合 `coext`；
     - 要求 `len(coext) == 1`（存在且仅存在 1 个与 `parent` 双向 ≥85% 同视口的顶层阻塞者 `delegate`）；
     - 其余每个 `other ∈ blockers \ {delegate}` 须满足 **`!other.Context` 且 `publicNodeClippedRect(other, screen)` 成功**（任何无几何或 `context` 阻塞者直接拒绝，不按面积 0 放行），且裁剪面积 `<= 25%` 父面积（`rectArea64(otherRect) * 4 <= rectArea64(parentRect)`），防止把左右并排分栏误判为同视口包裹；
     - `delegate` 必须满足对应动作资格（`ActionText` 用 `isValidTextPublicTarget`，否则用 `validTapPublicTargetAllowed`）；
     - 当 `len(blockers) > 1`（存在小角标兄弟）时，`delegatePlan` 选出的落点 `(x, y)` 必须额外通过 `len(externalInteractiveOwnersAt(view, delegate, x, y)) == 0`，证明未踩在小角标或任何外部交互节点上。
4. **B1（`target_has_other_action` 向下唯一同视口子委托）**：
   - 在 [`staticTapRefCheckWithOptions`](../../../internal/engine/tap_scope.go#L2506-L2521) 中，当 `!hasAction(target, action) && isTouchInteractive(target) && operation.EffectiveBubble()` 时，构建 `target` 的作用域 `scope` 并调用同一 `findUniqueCoextensiveBlocker` + 子委托直接规划（需同时满足上述 `len(blockers) > 1` 时的 `externalInteractiveOwnersAt == 0`）；
   - 命中时构造 `tapPlan{..., strategy: tapModeCoextensiveChild, targetRef: target.Ref, targetRaw: input.targetRaw, delegateRaw: delegateInput.targetRaw, delegateParentOtherAction: true}`；
   - 未命中（如无同视口子容器、有 `>25%` 竞争子区块、或子容器不支持目标动作、或显式 `bubble=false`）时，原样返回 `422 unsupported_node_action (reason="target_has_other_action")`；**绝不向 `target` 祖先冒泡**。

### 4.4 共位与坐标嵌套拥有者消歧（E4 / F1）

1. **E4（`bubble_colocated` 局部嵌套/内层消歧）**：
   - 在 [`planBubbleOrColocated:2338-2377`](../../../internal/engine/tap_scope.go#L2338-L2377) 中，当 `len(folded) >= 2 && centerCount != 1` 时：
     1. 若存在 `containsCenter && inChild` 成员（落点在目标 `C` 裁剪区内的共位节点）构成非空子集 `inChildCenter`：若 `len(inChildCenter) == 1` → 直接选中；否则对 `inChildCenter` 移除任何存在严格公开后代也在 `inChildCenter` 内的祖先，若剩余最内层数量 `== 1` → 直接选中；
     2. 否则对全体 `containsCenter` 集合移除任何存在严格公开后代也在该集合内的祖先，若剩余最内层数量 `== 1` → 直接选中；
     3. 仍 `>= 2`（例如两个平级互不为祖先的子按钮同时包含 `C` 中心）→ 按既有逻辑返回 `422 no_safe_point (reason="colocated_ambiguous")` + `candidates`。
2. **F1（坐标目标嵌套交互拥有者最内层消歧）**：
   - 新增辅助函数 `innermostPointOwners(view *tapPublicView, owners []*protocol.Node) []*protocol.Node`：移除存在任一严格公开后代也在 `owners` 内的祖先节点；
   - 在 [`resolveObservedPoint`](../../../internal/engine/point.go#L190-L208) 中，对 `matching := filterMatchingPointOwners(owners, operation)`：
     - 若 `len(matching) == 1` → 保持原样通过；
     - 若 `len(matching) >= 2` → 计算 `inner := innermostPointOwners(view, matching)`，当且仅当 `len(inner) == 1` 时消歧通过（`pointModeDisambiguated`）；
     - 仍 `len(inner) >= 2` 或 `len(matching) == 0` → 返回 `422 ambiguous_point (reason="competing_targets")`，`candidates` 列出竞争项（若 `len(inner) >= 2` 优先列 `inner`）。

### 4.5 认领后重验联动（必须同步修改的防线）

1. **E1 / E2 重验全路径对齐（`pointOwner`、`resolveDirectTapScopeFrame`、`revalidateColocatedTapFrame` & `tapFrameTier4OcclusionProven`）**：
   - [`pointOwner`](../../../internal/engine/act_revalidation_policy.go#L1748-L1761) 在单节点 [`resolveTapScopePoint`](../../../internal/engine/act_revalidation_policy.go#L1710) 失败后，若点满足 `provesSubtreeTapPointPassive(scope, x, y)`，允许返回 `(nil, true)`（注意：`pointHitsFixedCarrier` 仍严格保持单载体不变，不因 E1 放宽固定载体身份证明）；
   - [`resolveDirectTapScopeFrame:1616`](../../../internal/engine/act.go#L1616) 在 `chooseUnboundTapPlan` 之后，以及 [`revalidateColocatedTapFrame:1362`](../../../internal/engine/act.go#L1362) 在 `chooseColocatedTapPoint` 之后，将强要求 `provesSubtreeTapPoint(scope, x, y)` 改为 `provesSubtreeTapPoint(scope, x, y) || provesSubtreeTapPointPassive(scope, x, y)`（同时 `chooseUnboundTapPlan`/`chooseTapPlan`/`chooseColocatedTapPoint` 已按 §4.3 覆盖 E1/E2），避免原本静态由 E1 放行、新帧发生合法重选点时在二道闸门被误拒为 `409 scope_blocked`；
   - [`tapFrameTier4OcclusionProven`](../../../internal/engine/act_target_shared.go#L241-L258) 保持单命中 `state == pointUnique` 优先；当 `state == pointAmbiguous` 且无外部交互拥有者时，通过 `resolvePointSet(view.tree.Root, x, y)` 检查最深命中非空且每个 `hit` 均满足 `hit == target || (isPublicAncestor(view, target, hit) && !isTouchInteractive(hit) && hasValidDirectNonInteractivePath)`（即 `hit` 到 `target` 之间无任何 `isTouchInteractive` 或 `context` 遮挡边界，与 `scope.carrierByNode` 一致），通过则视作 Tier 4+ 无遮挡。
2. **B1 / E3 重验对齐（`revalidateCoextensiveChildFrame`）**：
   - 在 [`act.go:1142`](../../../internal/engine/act.go#L1142) 对 `freshParent` 校验：仅当 `plan.delegateParentOtherAction == true`（B1）时，只要 `freshParent` 非 root、非 context、有几何、未显式禁用且满足 `isTouchInteractive(freshParent)` 即允许保持父约束；对于普通点击及 E3a（`ActionText`），静态准入本身已要求 `target` 满足对应动作/可编辑性，故保持既有 `framePublicTargetError(freshParent, action, false, opts)` 不放宽；
   - 在 [`act.go:1163`](../../../internal/engine/act.go#L1163) 复核同一 `findUniqueCoextensiveBlocker` 逻辑，确保新鲜帧上唯一同视口阻塞者仍为 `freshDelegate`、其余阻塞者非 `context` 且裁剪面积 `<= 25%`；
   - **关键无条件外覆盖校验**：当新鲜帧 `parentBlockers` 数量 `> 1` 时，[`act.go:1180`](../../../internal/engine/act.go#L1180) 取得 `frame` 后**无条件（不限 `matchParent.tier >= 4`）**检查 `len(externalInteractiveOwnersAt(view, freshDelegate, frame.x, frame.y)) == 0`，未通过按 `scope_blocked` 拒绝，杜绝 `tier == 1..3` 时小角标移动覆盖落点导致误点。
3. **F1 重验与诊断对齐（`proveTargetSet`、`provePointTarget` & `changedPointBlockingRefs`）**：
   - [`proveTargetSet`](../../../internal/engine/point.go#L617-L621) 与 [`provePointTarget`](../../../internal/engine/act.go#L1748-L1751) 中，判断新帧存在多个交互拥有者时，改用 `len(innermostPointOwners(view, filterMatchingPointOwners(owners, operation))) != 1`；若最内层归约后仍恰有唯一匹配请求动作的拥有者，则与静态阶段一致放行；
   - [`changedPointBlockingRefs`](../../../internal/engine/act_rejection_diagnostics.go#L167-L175) 同步使用 `innermostPointOwners(oldView, oldOwners)` 求 `expected`，避免 F1 场景重验失败 `point_changed` 时漏附 `blocking_refs`。
4. **D1 与契约 36 重验 `text_rebind` 的对齐**：
   - D1 静态解析完成后，`tapPlan.targetRef` 为实际被选中的 `EditText` ref（如从包装壳 `n10` 解析到内部 `n11`，`tap.targetRef == "n11"`、`tap.targetRaw == raw(n11)`）；
   - [`act.go:174-175`](../../../internal/engine/act.go#L174-L175) 调用 `rebindActTarget` 时，把 **`tap.targetRef`** 传给 `planActRebind` 用以查找 `oldView.byRef[req.TargetRef]`（保证与 `req.OrigRaw` 一致，不因静态 D1 换节点而使 Attempt 2 `text_rebind` 直接失配）；同时 `error.details.original_ref` 与 `Result.auto_recovery.original_ref` 若发生动态重验恢复，继续忠实回显调用方原始请求中的 `actOriginalRef(&req.Operation)`（即 `n10`），不泄露内部解析细节；
   - **对称排除 E3a `delegateRaw`**：在 [`act_recovery.go:70-71`](../../../internal/engine/act_recovery.go#L70-L71)（`canRebindActTarget`）与 [`act_rebind.go:47-48`](../../../internal/engine/act_rebind.go#L47-L48)（`planActRebind`）对 `kind == "text"` 同步增加 `tap.delegateRaw == nil` 门禁（与 `tap`/`long_press` 一致），避免 E3a 委托过的父容器误入 `text_rebind` 并将 `target_missing` 污染为 `scope_blocked`。

---

## 5. 算法

### 5.1 静态准入与重验联动总流程

```text
Act(ctx, agent, P, req) [未认领 O，零设备 I/O]:
  staticActCheckLocked -> 校验会话 Active/Deadline、O 归属/Ready/ActionableUntil
  staticActContentCheckWithTap(staticObs, &req.Operation):
    ├─ scroll: resolveScrollPlan
    │    ├─ ref 为有效滚动容器:
    │    │    ├─ 轴向兼容 -> planScrollContainer(..., allowInner=true, allowCoextensive=false)
    │    │    └─ 轴向不兼容 -> 1) 父链 compat 祖先 (scroll_axis_fallback)
    │    │                     2) A1: tryUniqueInnerScrollFallback(allowCoextensive=true, minCover=0, overlayWant=nil)
    │    │                     3) 有 compat 错 -> 返 firstCompatErr；否则 422 axis_mismatch + candidates
    │    ├─ ref 为 role=scroll 但缺 swipe:
    │    │    └─ A3: 若未禁用且最近滚动祖先未禁/有几何 -> 沿 scrollRoleAncestors 找兼容祖先规划；否则按 ancestor_invalid/compat/axis/target_missing_action 收口
    │    └─ ref 为非滚动节点:
    │         └─ 1) A3: 沿 scrollRoleAncestors 跳过未禁用但缺 swipe 者，向首个兼容祖先规划
    │            2) A2: 若无向上兼容规划(且非 ancestor_invalid/firstCompatErr) -> tryUniqueInnerScrollFallback(allowCoextensive=true, minCover=50, overlayWant=nil)
    │            3) 否则返原错误 + 遮罩过滤后的 candidates
    ├─ text(ref): staticTextRefCheck
    │    ├─ 检查存在(409) / !root / !context / geometry(422) / enabled(409)
    │    ├─ RoleInput && ActionText -> 检查 !protected -> planDirectTapRef(allowCoextensiveChild=true, E1/E2/E3a)
    │    └─ 非法输入目标 -> D1(D1a 包装壳向下跳过context -> D1b 占位子向上 -> D1c 共位唯一输入框)
    │         ├─ 命中 -> 以 resolved 输入框运行直接规划并写入 tapPlan{targetRef: resolved.Ref, targetRaw: resolvedRaw}
    │         └─ 未命中 -> 422 text_target_uncertain(stage=target, reason=target_not_editable) + 可选 candidates
    ├─ tap / long_press(ref): staticTapRefCheckWithOptions
    │    ├─ hasAction -> planDirectTapRef (Pass1 4级单命中 -> Pass2 E2残余区单命中 -> Pass3 E1被动多命中 -> E3b 同视口子委托)
    │    └─ !hasAction ->
    │         ├─ isTouchInteractive && bubble=true -> B1 向下唯一同视口子委托(含 E3b+外覆盖检查)；无解返 target_has_other_action
    │         ├─ !isTouchInteractive && bubble=true -> planBubbleOrColocated (Step A 含 E1/E2 -> Step B 含 E1/E2 与 E4 内层消歧)
    │         └─ bubble=false -> 422 unsupported_node_action + candidates
    └─ coordinate(tap/long_press/text/swipe): resolveObservedPoint
         └─ F1 对 matching owners 做 innermostPointOwners 归约 -> len==1 放行，否则 422 ambiguous_point

任何阶段静态拒绝: 零设备 I/O、不认领 O、O 保持 ready，返回 recovery(entry=retry, reference=O, input_state=effective)。
静态通过 -> acquireWork -> claimObservationLocked(ready->claimed) -> phase=revalidate (执行 §4.5 同步守卫 + 契约 36 零派发重验恢复)。
```

### 5.2 E2 2D 残余安全区算法（`residualPatchPoints`）

```text
func residualPatchPoints(scope *tapScope, region device.Rect) []tapStaticPoint:
  1. 收集矩形边界 X 坐标集 xs = {region.Left, region.Right} 与 Y 坐标集 ys = {region.Top, region.Bottom}
     对每个 b ∈ scope.blocked: 取 inter = b.rect.Intersect(region); 若非空, 将 inter.{Left,Right} 加入 xs, inter.{Top,Bottom} 加入 ys
  2. 排序去重 xs, ys; 若 len(xs) < 2 || len(ys) < 2 || len(xs) > 128 || len(ys) > 128 -> return nil
  3. 构造 (len(ys)-1) × (len(xs)-1) 布尔自由网格 free[r][c] (该格子中心未落入任一 blocked.rect)
  4. 按行扫描直方图 (每格存连续自由高度 pixels_y 或行高度合并)，借助单调栈在 O(|xs|·|ys|) 算出全自由矩形:
     - 仅保留实际像素宽 >= 16 && 高 >= 16 的矩形
     - 用极大子矩形判据去冗余，保留合并后最大自由块
  5. 按 min(w, h) 降序 -> w*h 降序 -> Top 升序 -> Left 升序排序
  6. 取前至多 8 个矩形的 tapSafeCenter(rect) 作为候选点返回（外层调用方去重并经安全谓词验证）
```

### 5.3 撤销、失败与预算耗尽边界

- 静态阶段发生在 `acquireWork` 之前，纯 CPU 内存遍历，`capture == nil`、拓扑损坏或环路返回 `503 hierarchy_unavailable`；
- 离散坐标系边界超 `128` 时 E2 安全降级跳过（`return nil`），不触发无界分配或超时；
- 认领后若 `STOP` / `work.Deadline` / 前台切换到达，按契约 36 优先返回 `409 control_revoked` / `foreground_changed`，不延续任何重验动作。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| :--- | :--- |
| [release/versions.json](../../../release/versions.json) & [scripts/generate-version.ps1](../../../scripts/generate-version.ps1) | 升级为 `release_version: "0.41.0"`、`contract_version: 37`、`android_version_code: 43`，运行生成脚本更新 Go/Java/XML 常量 |
| [internal/engine/scroll.go](../../../internal/engine/scroll.go) | A1/A2/A3：`tryUniqueInnerScrollFallback` 支持 `(allowCoextensive, minCoverPercent)` 并以 `want = nil` 接入 `scrollOverlayContext`；`resolveScrollRefPlan` 接入 A1/A2/A3 逻辑 |
| [internal/engine/tap_scope.go](../../../internal/engine/tap_scope.go) | D1（`resolveEditableTextTarget` + 失败补 `candidates`）、E1（`provesSubtreeTapPointPassive` / `provesGuidancePointPassive` / `tryPlanTapDirectBase` 对齐）、E2（`residualPatchPoints` 与 3-pass 选点，含 `chooseColocatedTapPoint`）、E3a/E3b（`findUniqueCoextensiveBlocker`）、B1（`delegateParentOtherAction`）、E4（`bubble_colocated` `inChild` & 最内层消歧） |
| [internal/engine/point.go](../../../internal/engine/point.go) | F1（`innermostPointOwners` 并在 `resolveObservedPoint` 与 `proveTargetSet` 接入） |
| [internal/engine/act.go](../../../internal/engine/act.go)、[act_target_shared.go](../../../internal/engine/act_target_shared.go)、[act_revalidation_policy.go](../../../internal/engine/act_revalidation_policy.go)、[act_rebind.go](../../../internal/engine/act_rebind.go)、[act_recovery.go](../../../internal/engine/act_recovery.go)、[act_rejection_diagnostics.go](../../../internal/engine/act_rejection_diagnostics.go) | 4 处重验联动对齐：`pointOwner`、`resolveDirectTapScopeFrame`、`revalidateColocatedTapFrame`、`tapFrameTier4OcclusionProven`、`revalidateCoextensiveChildFrame`（含 `len(parentBlockers)>1` 无条件外覆盖检查）、`provePointTarget`、`changedPointBlockingRefs`，以及 D1 后传递 `tap.targetRef` 给 `rebindActTarget` 与 `canRebindActTarget`/`planActRebind` 拦截 `text` `delegateRaw != nil` |
| [internal/protocol/errors.go](../../../internal/protocol/errors.go) | 更新包注释说明 `text_target_uncertain (stage=target, reason=target_not_editable)` 允许附带既有白名单键 `candidates` |
| [internal/engine/*_test.go](../../../internal/engine/) | 新增 A1/A2/A3/D1/E1/E2/E3/B1/E4/F1 正向、反控与反向对照用例；按新契约更新 5 处既有旧保守测试夹具并为之补齐外部同深回归用例 |
| `docs/{interface,architecture,mcp,integration,runbook,client,control-protocol,phone-operations,release}.md` 与 `README.md` | 同步 9 项静态解析演进、`target_not_editable` 可选 `candidates` 及版本引用 `36 -> 37` / `0.40.0 -> 0.41.0` |

### 6.2 配置与取值

| 常数 | 取值 | 依据与不可绕过边界 |
| :--- | :--- | :--- |
| `scrollInnerFallbackMinCoverPercent`（仅 A2） | `50`（`50%`） | 与共位/遮罩半面积门限一致，确保非滚动大壳只委托占据主体区域的唯一滚动子容器 |
| `tapResidualMaxCoords`（E2） | `128` | 限制离散网格规模 `<= 127×127`，保证极复杂子树下 CPU `< 0.5ms` |
| `tapResidualMinPatchPx`（E2） | `16` | 避免手指落入 `1–2px` 极窄缝隙造成真实设备物理误触 |
| `tapResidualMaxCandidates`（E2） | `8` | 常数级候选验证上限，防止无意义遍历 |
| `coextensiveMinorBlockerMaxPercent`（E3b/B1） | `25`（`1/4`） | 容许角标/小按钮共存，同时坚决拦截半屏/三分栏等分栏结构误当同视口 |

以上均为引擎内部固定常数，不暴露请求参数，不接受环境变量覆写。

### 6.3 日志与可观测性

- **零新增 `eventlog` 白名单键**：复用 [`internal/eventlog/log.go`](../../../internal/eventlog/log.go#L63-L108) 既有 `"mode"` 与 `"strategy"` 键；
- E2 命中时在既有 `act.tap_plan` 记录 `mode="residual_patch"`；F1 命中保留 `mode="point_disambiguated"`；A1/A2 命中保留 `mode="scroll_inner_fallback"`；A3 命中保留 `mode="scroll_ancestor_lift"`；B1/E3 命中保留 `mode="coextensive_child"`；
- 严格遵守 `AGENTS.md` 第 10 条：不记录任何 `ref`、坐标、文本或树内容。

### 6.4 与既有不变式的关系

| 不变式 | 影响与保持方式 |
| :--- | :--- |
| 唯一权威（`internal/engine`） | 全部静态解析与重验联动均在 `internal/engine` 内完成，HTTP/MCP/Client/App 不存状态 |
| 单向消费 `O` 与静默“弱保软” | 静态阶段只读不可变 `O`，不认领、不采集、不写 `Result.auto_recovery`；拒绝时 `O` 保持 `ready` |
| 状态锁短小 | 全部解析在 `staticActCheckLocked` 释放 `e.mu` 之后执行，零持锁等待 |
| `STOP` 不等待 | 静态零等待；认领后重验联动随时检查 `workRevoked`/`Ctx.Err()`，立即终止 |
| 事实分层与防绕过 | 不跳过密码框、显式禁用控件、外部交互遮挡或同级多解真歧义；`candidates` 干跑保证 `executable=true` 在同一 `O` 上可用 |

### 6.5 契约与文档影响

正式文档须在实现阶段同步修订并通过 `AGENTS.md` 第 15 条**独立只读文档审计**（禁过程性词、全量链接/版本号一致性）：

| 文档 | 拟修订内容 |
| :--- | :--- |
| [docs/interface.md](../../../docs/interface.md#L506-L755) | 同步 `tap`/`long_press`/`text(ref)`/`scroll`/`point` 静态解析规则（A1/A2/A3、D1 及 `target_not_editable` 可选 `candidates`、E1/E2/E3/B1、E4/F1）与对应 `phase=revalidate` 说明、`contract_version=37` |
| [docs/architecture.md](../../../docs/architecture.md#L198-L327) | 同步静态规划与重验联动架构描述及版本表述 |
| [docs/mcp.md](../../../docs/mcp.md#L57-L85) | 同步 `act` 静态能力说明与 `contract_version=37` |
| [docs/integration.md](../../../docs/integration.md#L187-L195)、[docs/runbook.md](../../../docs/runbook.md#L278)、[docs/client.md](../../../docs/client.md#L22)、[docs/control-protocol.md](../../../docs/control-protocol.md#L26-L158)、[docs/phone-operations.md](../../../docs/phone-operations.md#L124-L257)、[docs/release.md](../../../docs/release.md#L39-L86) 与 [README.md](../../../README.md) | 同步 `contract_version=37` / `0.41.0` / `version_code=43` 版本引用与排障说明 |

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **直接关闭 GitHub Issue #52**：外层横向 `ViewPager` 包裹同界唯一纵向 `RecyclerView` 时，请求 `scroll(direction=down)` 一次调用直接滚动内层列表；
2. 抽屉/页壳 `not_scrollable`（A2）、`NestedScrollView > RecyclerView(scrollable=false)`（A3）、`TextInputLayout` / 搜索栏包装壳 / 占位标签（D1+E3a）、图文重叠被动卡片（E1）、带中心阻塞的边缘安全区（E2）、带小角标的同视口子容器（E3b）、异动作同视口叠层（B1）、共位内层与坐标嵌套拥有者冲突（E4/F1）均在单次 `act` 内完成安全执行；
3. `D1` 未命中时 `target_not_editable` 能返回有效 `candidates`，消除调用方盲猜成本。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| :--- | :--- |
| 同级互不为祖先的多候选真歧义（如两个同排按钮、两个输入框、多分页同时存在可兼容列表） | 无法在不猜意图的前提下保证唯一性，必须返回 `422` 与 `candidates` 交由调用方确认 |
| 目标含密码框/禁用输入框、或 `target_protected` | 严守隐私安全与显式禁用不可绕过红线 |
| `bubble=false` 时的 `target_missing_action` / `target_has_other_action` | 尊重调用方显式关闭交互委托的指令 |
| 纯自绘/WebView 无任何无障碍交互能力节点（`no_interactive_ancestor`） | 保持契约 35 `target_center` 客观提示边界，不自动盲点静态文本 |
| 坐标落在非滚动区域（`no_scroll_container`） | 防止居中弹窗/卡片无遮罩时误滚底层主页面 |
| 自由安全缝宽或高 `< 16px`、或离散边界超 `128` 的极端阻塞区域 | 物理手指与单帧复杂度下限限制，拒绝低质量边缘窄缝点击 |
| 静态解析后发生真实页面跳转或界面剧变（超 `phase=revalidate` / `act_rebind` 边界） | 静态恢复不代替跨帧验证，认领后交由契约 36 重验保障防误点 |

### 7.3 成本

- **延迟与设备 I/O**：静态准入阶段增加 0 次 ADB/u2 调用、0 次抓屏；常规树节点 `< 200`，A1/A2/D1/E1/E2/E3/B1/E4/F1 额外纯内存扫描耗时 `< 1ms`（E2 离散网格受 `128` 上限约束，单次最坏 `< 0.5ms`）；
- **状态锁与日志**：全在锁外计算；零新增 `eventlog` 键，几乎零额外日志量。

---

## 8. 验证计划

按 [`AGENTS.md`](../../../AGENTS.md) 第 19、24 条，所有全量测试、发布构建、实机部署与验证均由**主代理亲自执行与判定**。

### 8.1 单元测试

| 用例类别 | 覆盖目标与预期（含反控与反向对照） |
| :--- | :--- |
| **A1 / Issue #52** | 同界横向 `ViewPager` + 唯一纵向 `RecyclerView` 请求 `down` 直接 `executed`（`left/right` 仍走 `ViewPager`）；存在 2 个同界纵向子容器或仅同轴失败时**反向对照**仍拒绝；带弹窗外层遮罩/跨窗遮罩时不穿透（去掉实现必红） |
| **A2** | 非滚动页壳/抽屉壳含唯一主体滚动容器（`>=50%`）自动委托；`<50%` 角落小条或被外层遮罩覆盖时坚决拒绝 |
| **A3** | `NestedScrollView > RecyclerView(scrollable=false)` 无论传 `RecyclerView` 还是其子文本均自动提升至 `NestedScrollView`；禁用节点（`enabled=false`）或首层祖先禁用仍返 `409 node_disabled`/`ancestor_invalid`；外层无兼容祖先保留原 `target_missing_action`/`ancestor_missing_action` |
| **D1** | D1a（包装壳唯一定向输入框）、D1b（占位子唯一输入祖先）、D1c（扁平共位唯一输入框）均成功写入，且衔接 Attempt 2 `text_rebind` 成功；**反控**：大屏容器（`>=50%`）、壳内含额外受保护/禁用 `RoleInput`（总数=2）、`RoleInput` 自身缺 `ActionText`、`target_protected`、假搜索按钮均不恢复；失败时 `target_not_editable` 有合法输入框则附 `candidates`（`target_protected` 不附） |
| **E1** | 被动图文重叠卡片静态、干跑 `executable`、`resolveDirectTapScopeFrame`/`revalidateColocatedTapFrame` 偏移重选点及 Tier 4 重验均通过；**反控**：存在外部同深非载体节点及固定载体点被遮挡时必拒绝 |
| **E2** | 中心十字/`3×3` 被阻塞但外圈有 `>=16×16px` 安全块时 `mode="residual_patch"` 直接点击并重验通过；**反控**：仅留 `8×8px` 窄缝或边界超 `128` 时仍报 `422 no_safe_point (blocked)` |
| **E3 & B1** | `text(ref)` 同视口子输入框委托成功（非输入子容器拒绝，且 `delegateRaw != nil` 不误入 `text_rebind`）；带 `<25%` 小角标同视口子容器点击+重验通过（`>25%` 分栏及新帧 `tier=1` 小角标覆盖落点均拒绝）；`target_has_other_action` `bubble=true` 向下同视口子容器成功（`bubble=false` 与向上祖先均拒绝） |
| **E4 & F1** | `bubble_colocated` `inChild` 内层优先选中，平级竞争仍 `colocated_ambiguous`；坐标命中父卡+子按钮静态（`point_disambiguated`）与重验均放行，`point_changed` 仍附正确 `blocking_refs`，互不为祖先双按钮仍报 `ambiguous_point` |
| **既有旧保守用例同步（5 处）** | 更新 [`act_proposal_scroll_coord_test.go:112-163`](../../../internal/engine/act_proposal_scroll_coord_test.go#L112-L163)（适配 A3，同时保留外层无兼容祖先测 `target_missing_action`）、[`act_tap_scope_test.go:1046`](../../../internal/engine/act_tap_scope_test.go#L1046)、[`tap_static_error_test.go:159-219`](../../../internal/engine/tap_static_error_test.go#L159-L219) 及 [`tap_static_error_test.go:546,653-687`](../../../internal/engine/tap_static_error_test.go#L653-L687)（将 `ambiguous`/`mixed` 与被裁剪对象可 `inspect` 回归改为外部同深非载体节点，留存守卫） |

### 8.2 实机/系统级、发布交付、推送与 Issue #52 闭环

1. **全量 Go 测试**：主代理在改动与文档收敛后集中运行全量 `GOEXPERIMENT=jsonv2 go test ./...`（超时设为 `15m`），确保全部通过（无跳过/放宽）。
2. **发布构建与清单核验**：`release/versions.json` 升为 `0.41.0 / contract_version: 37 / android_version_code: 43` → 运行 `scripts/generate-version.ps1` → `scripts/build-release.ps1` → `scripts/verify-manifest.ps1`。
3. **手机整组覆盖部署与最小实机闭环**：覆盖部署真机全套产物（含手机 HTTP MCP，**保留 App 私有数据**）；在有真机可用条件下执行覆盖 Issue #52（外层横向容器内唯一纵向列表直接 `scroll down`）及本次变更路径的最小实机闭环（若真机不可用则如实记录 `未执行` 及原因）。
4. **Windows 主机产物替换与双入口独立探针**：同一源码提交构建替换 Windows `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验主机清单后分别使用新 `build/mcp-probe.exe` 验证 **主机 stdio MCP** 与 **手机 HTTP MCP**（明确边界：宿主 Agent 配置、托管进程重启与重连默认由用户处理）。
5. **文档审计、分步提交、推送远程与闭环 Issue #52**：`docs/` 变更先经独立只读文档审计 subagent 审结至 `clean`；落盘验证记录后再按**先提交实现+文档、后补填 §11 并移入 `maintenance/proposals/implemented/` 提交归档**两步提交；执行 `git push` 推送远程；最后通过 `gh issue comment 52` 与 `gh issue close 52` 回复总结已落地版本 `0.41.0 / contract 37`、Issue #52 与 R2 静态自动恢复详情及实测情况并关闭 Issue #52。

### 8.3 记录

验证结果写入 `maintenance/verification/act-static-admission-auto-recovery.md`，写明代码提交、构建方式、环境/设备、实际命令与真实结果（严格区分磁盘替换、独立探针验证、单测与实机闭环结论）。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| :--- | :--- |
| 路线与覆盖范围 | 采纳 **R2**（第一梯队 9 项：A1/A2/A3 + D1 + E1/E2/E3 + B1 + E4/F1 + 认领后重验联动），排除第二梯队敏感项 |
| 调用方可见性 | 保持既有**静态“弱保软”静默语义**；不写入 `Result.auto_recovery`，不修改 `AutoRecoveryView` 枚举 |
| `target_not_editable` 候选 | `D1` 未命中返回 `422 text_target_uncertain (stage=target, reason=target_not_editable)` 时，存在合法候选输入框则在 `error.details` 补充 `candidates` |
| 交付与 Issue 闭环 | 含完整三项发布交付、远程推送与回复关闭 GitHub Issue #52 |

### 9.2 未决

| 事项 | 责任人 | 截止节点 | 现时状态 |
| :--- | :--- | :--- | :--- |
| 提案全文确认（批准按本提案实施） | 用户 | 实施开始前 | 待用户确认 |

---

## 10. 实施顺序

1. **版本与协议准备**（可与第 2 步前置阅读并行）：确认或预备 `release/versions.json` 升级方案，更新 `internal/protocol/errors.go` 白名单注释。
2. **核心静态准入与重验联动改造**：
   - 2a：`internal/engine/scroll.go`（A1/A2/A3 及其 `want=nil` 遮罩/覆盖守卫）；
   - 2b：`internal/engine/tap_scope.go`（D1 + 候选补充、E1 + `tryPlanTapDirectBase`、E2 2D 残余安全区与 3-pass、E3a/E3b、B1、E4）；
   - 2c：`internal/engine/point.go`（F1）；
   - 2d：`internal/engine/{act,act_target_shared,act_revalidation_policy,act_rebind,act_recovery,act_rejection_diagnostics}.go`（重验与诊断联动对齐、`text_rebind` `targetRef` 对齐与 `delegateRaw` 拦截）。
3. **单元测试补齐与 5 处旧夹具校准**：先写复现/反向对照用例，按包验证 `internal/engine` 与 `internal/protocol`。
4. **文档同步与独立只读文档审计**：修订全量受影响 `docs/*.md` 与 `README.md`，执行 `AGENTS.md` 第 15 条审计至 `clean`。
5. **版本升级、全量测试、发布交付、归档、推送与闭环 Issue #52**：修改 `release/versions.json` 并生成版本文件 → 主代理亲跑全量 `go test ./...`（15m 超时）→ 构建与清单校验 → 手机部署与最小实机闭环 → 主机产物替换与双入口 `mcp-probe` 验证 → 写验证记录 → 提交实现+文档 → 归档本提案并补 §11 提交 → `git push` → 回复并关闭 GitHub Issue #52。

---

## 11. 实现结果

实现提交：`e0d92d1`（主实现 `d9ee0c1`）。实现与验证记录：[验证记录](../../verification/act-static-admission-auto-recovery.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 轴不兼容内层委托（Issue #52） | `internal/engine/scroll.go`；`internal/engine/act_static_scroll_recovery_test.go` |
| 非滚动包装壳下探 | 同上 |
| 缺 `swipe` 的 `role=scroll` 向上提升 | 同上；`act_proposal_scroll_coord_test.go` |
| 文本定向绑定与失败候选 | `internal/engine/tap_scope.go`；`act_static_tap_recovery_test.go` |
| 被动多命中、2D 残余安全区、同视口子委托、异动作向下委托、共位最内层消歧 | `internal/engine/tap_scope.go`；`act_static_tap_recovery_test.go` |
| 坐标嵌套拥有者最内层归约 | `internal/engine/point.go`、`act_rejection_diagnostics.go`；`act_point_disambiguation_test.go` |
| 认领后重验联动 | `internal/engine/act.go`、`act_target_shared.go`、`act_revalidation_policy.go`、`act_rebind.go`、`act_recovery.go`；`act_static_recovery_revalidation_test.go` 等 |
| 版本 0.41.0 / 契约 37 / versionCode 43 | `release/versions.json` 与 4 个生成产物 |
| 受控实机夹具 | `android-control/test-target/.../ObservationFixtureActivity.java`（d05–d10） |
| 文档与审计 | `README.md` 与 9 份 `docs/`；独立只读审计 AUDIT CLEAN |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 文本定向绑定硬停止 | 比提案 §4.2 字面更严格：包装壳子树 `RoleInput` ≥2 直接拒绝；唯一输入不可用（不合格、无正交集、聚焦失败）也直接拒绝；占位子存在最近交互祖先但绑定失败即停止，不再尝试共位吸附。理由：避免在已发现歧义或受限目标后改绑无关外部输入框。 |
| 共位吸附收紧 | 仅扫描目标公开祖先/后代之外的外部共位 `RoleInput`，且目标面积 `<50%` 屏幕；先按共位与反底板守卫计数（含禁用、受保护、缺 `text`），唯一后才验证合法性。理由：防止绕过包装壳全子树计数与半屏门槛。 |
| 被动多命中重验整树证明 | 认领后保留被动多命中点时，除目标子树证明外还要求整树同点命中与零外部交互拥有者（`provesGuidancePointPassive`），与静态准入一致；比提案 §4.5 字面（仅子树）更严格。 |
| 残余安全区来源复验 | 计划记录内部 `residual`/`delegateResidual`；重验保留原残余点或委托最终点时再查外部交互拥有者。理由：残余点静态准入额外证明过无外部覆盖，重验不得丢失该前提。 |
| 同视口父/子双证明 | 多阻塞者外覆盖检查与父绑定 Tier 4/5 整树证明改为相互独立执行（提案要求"无条件追加"外覆盖），两者同时生效。 |
| 异动作委托父禁用门 | `text.submit_ref` 的静态 B1 拒绝显式禁用父；执行子初始灰置仍可规划。理由：避免静态放行后写前启用迁移只作用于执行子，导致先写后必然失败。 |
| `bubble=false` 与同视口子委托 | 保持 `coextensive_child` 不依赖 `bubble` 语义（基线在 `bubble=false` 下也允许单阻塞者委托）；多阻塞者放宽作为已批准例外，并有不依赖 `bubble` 的用例锁定。 |
| A3 可达性 | 正常投影同时派生 `role=scroll` 与 `swipe`，A3 为线格式/合成树防御分支；真实 `scrollable=false` 列表由普通祖先提升覆盖，并以新增真实首帧用例作为证据。 |

---

## 附：本提案不改变的事项

- 不改变 `Result.auto_recovery` 的字段结构、3 个封闭策略枚举及“仅限 `phase=revalidate` 活帧恢复”的语义边界；
- 不改变 MCP 13 工具数量、名称、JSON Schema、`CatalogFingerprint` 与 `result v4` 行格式；
- 不改变 `internal/eventlog/log.go` 的白名单键集合；
- 不改变 `swipe`、`key` 行为与 `text` 派发后阶段（`focus`/`write`/`verify`/`submit`）的协议契约；
- 不改变 `third_party/uiautomator/u2.jar` 与 `android-control/` 控制 App 业务逻辑（仅同步版本号生成产物）。
