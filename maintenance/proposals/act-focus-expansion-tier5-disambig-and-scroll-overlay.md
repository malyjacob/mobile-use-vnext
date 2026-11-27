# 提案：补齐展开输入框聚焦绑定、Tier 2/3 容器歧义转 Tier 5 与遮罩感知的滚动候选（含 Issue #48）

状态：提案，未实现。
基线提交：`a1e2c7a55e02677a48e55b0601862921d7a90b8e`（分支 `master`，`2026-11-27`）。
适用范围：`act` 的三类核心执行与静态规划路径——① `text(ref)` / 坐标 `text` 的点击获焦与展开框绑定（`stage=focus`）；② `act` 认领后 ref 目标重验的 Tier 2/3 同构容器歧义消歧；③ `scroll` 全部静态 `422` 候选的跨窗口隔离与遮罩/浮层覆盖感知过滤排序（覆盖 GitHub Issue #48）。不改变 `observe`/`snap`/`wait_for`/`app`/`shell`/`notify`/`diagnose`，不修改固定制品 `u2.jar` 与 MCP 13 工具 schema。
影响范围：`internal/engine/{act.go,act_revalidation_policy.go,act_target_shared.go,scroll.go}`、相关单元测试、`release/versions.json` 及生成版本常量、`README.md` 与 `docs/{interface.md,architecture.md,integration.md,client.md,control-protocol.md,phone-operations.md,release.md,runbook.md}`（并核对 `docs/mcp.md` 无漂移）、归档与验证记录及 GitHub Issue #48 回复关闭。
契约（`/api/*` 字段与 `contract_version`）：改变共同行为契约，不增删 `/api/*` 请求/响应字段、错误码、`error.details.candidates` 6 字段结构、`error.details.reason` 闭集或 MCP 13 工具 schema 与目录指纹；因 `text` 聚焦绑定/复查、`act` 重验消歧与 `scroll` 静态候选过滤排序语义变化，发版时通过单一版本源 `release/versions.json` 将版本由 `0.37.0` / `contract_version=33` / `android_version_code=39` 升级为 `0.38.0` / `contract_version=34` / `android_version_code=40`，并同步生成 Go/Java/XML 常量与发布清单。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[act.go](../../internal/engine/act.go)、[act_revalidation_policy.go](../../internal/engine/act_revalidation_policy.go)、[act_target_shared.go](../../internal/engine/act_target_shared.go)、[scroll.go](../../internal/engine/scroll.go)、[point.go](../../internal/engine/point.go)、[text_submit.go](../../internal/engine/text_submit.go)、[hierarchy.go](../../internal/android/hierarchy.go)、[log.go](../../internal/eventlog/log.go)、[versions.json](../../release/versions.json)）。

---

## 0. 摘要

基于 `0.37.0`（契约 `33`）本地会话研究记录（`.state/dsh-session-10ca6f7e/research/mcp-context-research-0.37.0.md` 阶段 2，所需事实已完整自包含于 §1.1–§1.2）与远程 **Issue #48**，本提案在不修改固定 `u2.jar`、不扩张 `/api/*` 与 `candidates` 既定字段结构、不放宽核心安全红线的前提下，将三项同属 `internal/engine` `act` 链路的高价值韧性缺口合并于契约 `34` 一次收口：

1. **`text` 展开输入框聚焦绑定与有界只读复查（路线 1B + 1C）**：在 `bindFocusedText` 为双帧唯一自身锚点新增横向展开/多行撑高几何守卫，并为无自身锚点的匿名输入框新增受限的最近非窗口根、非滚动父容器双帧唯一 `resource-id` 锚点兜底；同时在 `focusTextRef` 与 `focusTextPoint` 中引入至多 2 次短退避只读 `Capture` 复查（`60ms/120ms`，复查窗口上限 `1200ms`，严禁二次 `Tap`，遇 STOP/切任务/读帧报错立即中止），消除折叠评论栏展开导致的 `focus_unconfirmed` 误杀。
2. **`act` 复验 Tier 2/3 无自有文案容器歧义转入 Tier 5 消歧（路线 2B）**：仅对无自有可读文案、含子节点且允许平移的 `actMatchTapLike` 容器，在 Tier 2/3 因相邻同构列表卡片几何重叠触发 `actProbeAmbiguous` 时不再直接短路，改为直入 Tier 5 由 `sameHorizontalBand` + `L1/L2` + 双向唯一 `E3 subtreeEvidence` + `tier >= 4` 全树遮挡证明裁决。
3. **遮罩/浮层感知的 `scroll` 静态候选过滤与排序（路线 3B-1，关闭 Issue #48）**：保持 `scrollSafeSpansAt` 既有保守派发边界不变（核实固定 `u2.jar` 的 `getWindowRoots()` 使用 `java.util.HashSet`，顶层窗口顺序不代表真实 z 序，因此不引入基于顶层下标的派发假设）；在 `scroll` 全部 16 处静态 `422` 候选出口引入不依赖窗口下标序的跨窗口等值隔离（O1）、顺序无关的无子无文案遮罩叶判定（O2）与区分实体浮层面板和空心透明 HUD 包装层的同窗后序浮层覆盖判定（O3），直接排除被前景浮层/遮罩覆盖的背景候选并优先排序同前景层候选，彻底杜绝 `executable=true` 误导调用方关闭抽屉/弹窗。

本提案明确**不**修改固定 `u2.jar`、**不**改动 `scrollSafeSpansAt` 的跨窗口或单窗口派发安全边界、**不**二次派发聚焦点击或写原语、**不**放宽有自有文案/叶子节点的多候选歧义红线、**不**改动 `candidates` 6 字段契约或 MCP 工具 schema。

---

## 1. 问题

### 1.1 现象与代价

三项问题均在真实设备完整会话中出现清晰可观测的错误码与额外调用代价：

1. **折叠转展开输入框聚焦必败（`10ca6f7e` `seq=186`）**：
   - 在小黑盒评论页 `O55` 对底栏左侧半宽输入胶囊 `n1345`（中心 `x=304, y=2290`，父容器 `n1343 resource="vg_editor_container"`）调用 `act(text, ref=n1345, auto_submit=false)`，点击后评论框正常弹出遮罩并横向展开为全宽编辑器（`x=540, y=2096, focused=true`），服务端却返回 `409 outcome=uncertain`、`code=text_target_uncertain`、`details={stage:"focus", reason:"focus_unconfirmed", dispatch:{focus:"dispatched", write:"not_attempted", submit:"not_attempted"}}`（本场 140 次调用唯一一次 `uncertain`）。
   - 调用方被迫追加 `observe(P5737_1)` 取回 64 节点整树 `O56` 核对，再原样重试成功——单次故障多耗 **2 次工具调用 / 1,319 I/O tok / 146,554 tok-step**。
2. **动态列表卡片轻微平移触发 `target_ambiguous`（`10ca6f7e` `seq=86`）**：
   - 在小黑盒推荐流 `O35` 点击帖子卡片 `n75`（`540,1084 tap resource="epoxy_model_group_child_container"`，子树含唯一标题 `"被肥鱼神力吓哭了"` 与作者 `"Ken20020702"`）；认领后新鲜帧 `O36` 中列表上移 `154px`（`n75` 移至 `540,930`，水平位置与子树文案 100% 不变），却返回 `409 target_changed`、`details.reason="target_ambiguous"` 并交付 717 tok 整树 `O36`（错误结果总计 802 tok），迫使重试——单次多耗 **1 次调用 / 825 I/O tok / 108,900 tok-step**。
3. **前景浮层/侧边抽屉打开时 `scroll` 静态候选误推底层页面为 `executable=true`（`3e7a393a` `seq=679-685` / 远程 Issue #48）**：
   - 在网易云音乐打开侧边抽屉 `O5641_167` 后请求 `act(scroll, container={ref:"n7628"}, direction="down", amount="half")`，服务端返回 `422 no_safe_trajectory`（`reason=trajectory_blocked`、`scope_ref="n7628"`），并在 `error.details.candidates` 将被抽屉遮住的底层主页面全屏滚动容器 `n7405` 与 `n7416`（`[0,0,1080,2248]`）排在首位且标为 **`executable=true`**。
   - 调用方按文档“取首个 `executable=true` 重试”对 `n7405` 重试后，手势返回 `executed` 但直接把侧边抽屉关闭，未滚动抽屉内容，浪费 2 步操作并破坏当前交互上下文。同样问题在非滚动模态弹窗/遮罩（如 `10ca6f7e` `O56` `n1507 v_mask` 盖住底层 `n1360/n1418`）下调用 `scroll` 触发 `not_scrollable`/`no_scroll_container` 时也会经 `collectPageScrollCandidates` 复现。

### 1.2 证据链

三项问题各自对应的源码通路均已核实到具体文件与行号；分诊结论均为**取舍/规范类变更**（现有代码忠实执行契约 33 规范，须经提案与契约升级同步演进，不得在实现里暗改语义）：

#### 证据链 A：折叠转展开输入框聚焦绑定与单次 `Capture`

| 落点 / 现场证据 | 核实事实 |
| --- | --- |
| [act.go:1872-1896](../../internal/engine/act.go#L1872-L1896) | `dispatchText` 对 ref 目标先在已聚焦时走零点击快路径（`bindFocusedText(obs.Projection.Capture, fresh, want, obs.Foreground, tapX, tapY)`，注意此处 `want` 为 `fresh` 中节点指针）；未聚焦则调 `focusTextRef` |
| [act.go:1999-2046](../../internal/engine/act.go#L1999-L2046)、[act.go:2050-2096](../../internal/engine/act.go#L2050-L2096) | `focusTextRef` 与 `focusTextPoint` 在 `Tap` 返回 `DispatchTerminated` 后均仅做 **1 次立即 `Capture`**，没有任何聚焦收敛等待；失败直接报 `409 text_target_uncertain (stage=focus, reason=focus_unconfirmed)`，`Tap` 终止后撤权返回 `409 control_revoked` 并保持 `OutcomeUncertain` |
| [act.go:2275-2284](../../internal/engine/act.go#L2275-L2284)、[act.go:2299-2380](../../internal/engine/act.go#L2299-L2380) | `bindFocusedText` 虽在 focus 过滤前完成双帧 `anchorCount == 1` 与 `sameTextTaskContext` 检查，但：① 有自身锚点时 `shifted` 路径在 `L2370` 硬性调用 `sameHorizontalBand`（左右边容差 `±2%`、宽高差上限 `±20%`），拦截一切横向展开/多行撑高输入框；② 无自身锚点（`!hasAnchor`）时 `L2320-L2323` 在 `noAnchorNearCount(fresh, ...) != 1` 时即直接 `return false`，既不支持位移也不支持父容器锚点 |
| [act_revalidation_policy.go:317-323,359-363](../../internal/engine/act_revalidation_policy.go#L359-L363) | 点击输入框弹出软键盘后，采集可能由单窗口切换为多窗口（`connectDisplayWindows` 追加 `isSyntheticDisplayRoot`），导致原窗口根的 `Parent` 由 `-1` 变为 `syntheticRoot`，因此父锚点逻辑必须统一识别真实窗口根 |
| [build.go:474-477,509-514](../../internal/observation/build.go#L474-L477) | `newPublicNode` 对带文本的 `input` 隐藏公开 `resource` 显示，因此紧缩树样本 `O55/O56` 不能证明 `n1345` 自身必有 `resource-id`；同时具备自身锚点展开（1B）与唯一父容器 `resource-id` 兜底（1C）才能覆盖两类真实控件 |
| [act_revalidation_matrix_test.go:471-473](../../internal/engine/act_revalidation_matrix_test.go#L471-L473)、[act_revalidation_test.go:1387-1390](../../internal/engine/act_revalidation_test.go#L1387-L1390) | 两处现有单测 `source_mismatch_horizontal_width`（`Right: 900→500`）与 `width_changed_on_shift`（匿名框 `Right: 980→500`）均测试**大幅缩窄**须被拒绝 |
| [ui_execution_test.go:295-323](../../internal/engine/ui_execution_test.go#L295-L323) | `TestTextFocusReadFailureDoesNotReopenEndedTap` 注入 `Capture` 传输失败并要求立即以 `uncertain` 收口且不重开已终止 tap |
| [docs/interface.md:901-905](../../docs/interface.md#L901-L905) | 现行契约 33 明确限定 `shifted` 为“同一水平带与受限尺寸的纵向位移” |

#### 证据链 B：Tier 2/3 无自有文案容器歧义短路阻塞 Tier 5

| 落点 / 现场证据 | 核实事实 |
| --- | --- |
| [act_revalidation_policy.go:501-515,838-881](../../internal/engine/act_revalidation_policy.go#L838-L881) | Tier 2（`overlap >= 0.50`）与 Tier 3（`overlap >= 0.60`）的 `edgeAtTier` 只比较 `sameStructureForAct`、`geometryCompatible` 与祖先链，完全不检查子树可读文案 |
| [act.go:2431-2444](../../internal/engine/act.go#L2431-L2444) | `overlapRatio` 以**两矩形中较小面积**为分母；推荐流中紧邻的贴边裁切短卡（如 `O35` 顶部 `n105`）与平移后的长卡 `n75` 局部交叠即达到 `>= 0.50`，令 `probeWant`（[act_revalidation_policy.go:920-974](../../internal/engine/act_revalidation_policy.go#L920-L974)）在正向或反向扫描中发现 ≥2 条同构边并返回 `actProbeAmbiguous` |
| [act_revalidation_policy.go:1056-1057,1066-1099](../../internal/engine/act_revalidation_policy.go#L1056-L1057) | `matchActTargetWithOptions` 遇到 `actProbeAmbiguous` 无条件直接 `return`，从未进入 [L1525-L1554](../../internal/engine/act_revalidation_policy.go#L1525-L1554) 的 Tier 5（`sameHorizontalBand` + `L1/L2` + `E1/E2/E3` 双向唯一）；另 `L1099` 在返回 `actProbeNone` 时透传 `sawLineageMismatch`，需防止 Tier 3 歧义转入 Tier 5 失败后误标 `lineage_changed` |
| [act.go:20-34](../../internal/engine/act.go#L20-L34) | `isTransientRevalidateError` 将 `target_ambiguous` 排除在 150ms 瞬态复检之外 |
| [act.go:590-601,1007-1010,1155-1158,1211-1215,1319-1340,1498-1501](../../internal/engine/act.go#L1498-L1501)、[point.go:795](../../internal/engine/point.go#L795) | `submitTapFrameOptions`（`act.go:592`）与坐标拥有者兜底 `provePointOwnerFallback`（`point.go:795`）均置 `disableTranslation=true`；凡命中 `tier >= 4`（含 Tier 5）的目标均自动强制通过 `tapFrameTier4OcclusionProven` 全树遮挡证明，且 `bubble_colocated` 命中 Tier 5 自动触发 `colocatedSyncShiftOK` |
| [act_revalidation_matrix_test.go:275-286](../../internal/engine/act_revalidation_matrix_test.go#L275-L286)、[docs/interface.md:715,735](../../docs/interface.md#L715) | 现有矩阵测试中 `tierOriginalCapture` 目标节点为带自身文本的叶子按钮（无子节点）；现行规范要求任一 Tier 多候选立即拒绝、Tier 5 仅在零候选后运行 |

#### 证据链 C：遮罩/浮层感知的 `scroll` 静态候选过滤与排序（Issue #48）

| 落点 / 现场证据 | 核实事实 |
| --- | --- |
| [scroll.go:260-419](../../internal/engine/scroll.go#L260-L419) | `scrollSafeSpansAt` 遍历整棵公开树，对所有 `scope == scrollScopeNone`（一切非滚动节点，含顶层弹窗遮罩 `v_mask`/`shadow`、抽屉非滚动顶栏 `n7613..n7624`）在 `L373` 直接忽略 |
| [scroll.go:989,1038,1045,1053,1063,1070,1105,1137,1200,1255,1327,1348,1354,1367,1373,1383](../../internal/engine/scroll.go#L1381-L1385)、[scroll.go:1515-1588](../../internal/engine/scroll.go#L1515-L1588) | `scroll.go` 共 16 处静态 `422` 出口调用 `buildStaticCandidates`；其中 `collectNoSafeTrajectoryCandidates` 将 `failure.blockingScrollNodes`（底层 `n7405`/`n7416`/`n7026`/`n7479`）仅按面积降序与先序排序，未感知前景浮层或遮挡关系 |
| [act_target_shared.go:581-660,693-735](../../internal/engine/act_target_shared.go#L581-L660) | `collectPageScrollCandidates` 扫描全树 `isScrollContainer` 仅按面积降序排布；`buildStaticCandidates` 对 `scroll` 仅调用 `tryPlanScrollBase`（即仅走 `scrollSafeSpansAt`）计算 `executable`，再按 `executable=true` 置顶截取前 4 项 |
| `3e7a393a` 实机现场（`O5641_167`，网易云侧边抽屉） | `n7400`（DrawerLayout）有两子分支：`[n7402(主页面 n7405/n7416 0,0,1080,2248), n7611(抽屉 0,0,864,2400，含顶栏 y=0..273、实体滚动列表 n7628 0,273,864,2214（单节点即占 n7611 面积 80.9%）与底栏 y=2214..2400)]`。`n7405` 的 3 条探测轴线（`x=540,270,810`）100% 落在抽屉 `n7611` 之下，却因 `scrollSafeSpansAt` 忽略非滚动顶栏 `y=0..273` 而凑出 `65px >= 48px` 的假安全段，获得 `executable=true` 并排在 `candidates[0]` |
| `10ca6f7e` 实机现场（`O56` 小黑盒遮罩、`O77` B 站弹窗） | `O56` 中无子无文案点击遮罩 `n1507 (tap resource="v_mask")` 在 XML 先序位于底层 `n1360/n1418 (scroll)` **之前**（由 `elevation`/布局绘制序抬高），`O77` 中 `n2799 (tap resource="shadow")` 亦为全屏无子无文案点击遮罩；证明单窗口内不能盲目把“先序在后=视觉在上”当作派发级真理 |
| 固定制品 `third_party/uiautomator/u2.jar`（`classes8.dex`）、[hierarchy.go:201-288](../../internal/android/hierarchy.go#L201-L288)、[types.go:79-119](../../internal/device/types.go#L79-L119) | `u2.jar` 的 `AccessibilityNodeInfoDumper.getWindowRoots()` 使用 **`java.util.HashSet`** 收集 `getRootInActiveWindow()` 与 `getWindows()` 根节点后直接 `toArray()` 序列化，且 XML 不含 `window-id`/`window-layer`/`elevation`/`drawingOrder`。因此：**多顶层窗口下，合成显示根 `isSyntheticDisplayRoot` 下的顶层分支代表不同窗口（可做等值隔离 `winA != winB`），但子窗口下标序是 `HashSet` 桶序而非真实 z 序**，绝不能以 `winA < winB` 推断谁在顶层 |
| [protocol/errors.go:21-24](../../internal/protocol/errors.go#L21-L24)、[docs/interface.md:568-577,654-660,755-759](../../docs/interface.md#L568-L577) | `candidates` 每项固定 6 字段（`ref`/`role`/`actions`/`bounds`/`relation`/`executable`），`candidates[i].executable == true` 承诺同一 O 上重试必过静态准入；树模型不作为真实 z 序派发证明 |

### 1.3 为什么直觉会出错

1. **为什么不能在 `bindFocusedText` 里直接删掉几何守卫、只要全屏唯一 `focused=true` 就绑定？**
   点击后若触发页面跳转或聚焦落到同页另一个不相关控件，“唯一焦点”无法证明那是调用方指定的目标框；契约 18 红线要求先于 focus 过滤在双帧证明稳定身份锚点唯一，且几何守卫必须拒绝大幅缩窄或水平完全无交叠的异位替换。
2. **为什么不能在 `focusTextRef` 失败时补发第二次 `Tap`？**
   第一次 `Tap` 已终止生效，点击展开的输入框可能正在动画中，二次 `Tap` 会落到已变化的坐标上（例如点中遮罩 `v_mask` 把刚打开的评论框关掉），破坏“至多一次焦点点击”的不变式。
3. **为什么不能让所有 Tier 1–3 `actProbeAmbiguous` 一律落入 Tier 5，或者在 Tier 2 歧义后顺延走 Tier 3？**
   - 有自有文案或无子节点的控件（如两个 `"确定"` 按钮）在 Tier 1–3 已用尽自身语义，进入 Tier 5 毫无新证据且违背 `tier1/2/3_ambiguous` 红线；
   - Tier 2（`0.50`）出现兄弟碰撞后若继续走 Tier 3（`0.60`，不查 `E3`），可能仅凭重叠率差值在未校验子树文案的情况下误中错误卡片；直跳 Tier 5 强制走 `E3` + `tier >= 4` 全树遮挡反而更严更安全。
4. **为什么不能按合成显示根子窗口顺序放行跨窗口滚动，也不能把任意后序兄弟外框或任意前序可点节点当遮罩？**
   - 固定 `u2.jar` 的 `getWindowRoots()` 使用 `HashSet`，顶层窗口顺序本身就是散列桶序；按下标 `w1 < w2` 判定顶层/底层会在真机上随机反置，锁死前景弹窗却放行底层页面；
   - Android 常见“全屏透明 HUD 兄弟包装层”（`FrameLayout [0,0,1080,2400]`，内仅含顶栏+底栏+FAB，实体控件占比仅 ~15%），若不区分“实体浮层面板”与“空心透明包装层”，直接拿包装层整框当遮罩会误杀正常页面的 `page_scroll` 候选；
   - 播放页常见“前序全屏可点视频播放器容器 + 后序弹出评论列表”，若前序遮罩不限定为“无子无文案遮罩叶”，会把前景评论列表误当成被播放器遮挡而排除。

---

## 2. 目标与非目标

**目标**：
1. 折叠转展开输入框（无论 `EditText` 自身有双帧唯一 `resource-id`/`content-desc` 锚点，还是自身匿名但最近非窗口根、非滚动父容器具备双帧唯一 `resource-id` 且父下仅 1 个可编辑子框）在点击聚焦后可一次准确绑定并完成写入，消除 `focus_unconfirmed` 误杀。
2. `focusTextRef` 与 `focusTextPoint` 在首次抓帧聚焦未落位时，通过至多 2 次有界可中断只读 `Capture`（`60ms/120ms`，复查窗口上限 `1200ms`）吸收短时展开/切焦动画；不发二次 `Tap`，遇 STOP/切任务/采集报错立即收口，且不截断在途 `Capture`。
3. 无自有可读文案且有子节点的 `actMatchTapLike` 容器在 Tier 2/3 因同构兄弟卡片几何重叠出现 `actProbeAmbiguous` 时，跳过 Tier 3/4 直入 Tier 5 由 `E3 subtreeEvidence` 双向唯一性与 `tier >= 4` 全树遮挡证明完成消歧；Tier 5 无命中时清除 `sawLineageMismatch` 准确归因为 `target_missing`。
4. 在 `scroll` 全部 16 处静态 `422` 候选出口引入跨窗口等值隔离（O1，不依赖 `HashSet` 窗口顺序）、无子无文案外部遮罩叶判定（O2）与区分实体浮层面板和空心 HUD 包装层的同窗后序浮层覆盖判定（O3），直接排除被前景浮层/遮罩覆盖的后层页面候选并优先排序同前景层候选，彻底关闭 Issue #48。

**非目标**：
- 不修改固定制品 `third_party/uiautomator/u2.jar`（因此不引入依赖 `window-layer` 的多窗口 `scrollSafeSpansAt` 派发放行）；
- 不新增或修改 `/api/*` 请求/响应字段、错误码、`error.details.candidates` 6 字段集合、`error.details.reason` 闭集或 MCP 13 工具 schema；
- 不改变 `scrollSafeSpansAt` 的现有保守派发规则；
- 不在 `focusTextRef` / `focusTextPoint` 中派发第二次 `Tap`，也不将未确认获焦改判为 `executed`；
- 不允许 `text` 点击后销毁原框并弹出不同 `resource-id` 的独立新弹窗输入框绕过锚点校验；
- 不对有自有可读文案节点、叶子节点、Tier 1 歧义、`actMatchScroll`、`submit_ref` 或坐标拥有者兜底开放 Tier 2/3 歧义转 Tier 5；
- 不在本提案内包含研究报告阶段 1（宿主侧治理）及阶段 3/4 事项（`sparse_tree`/`target_center`/Schema 瘦身/微剪枝）。

---

## 3. 路线对比

### 3.1 议题 1（展开输入框聚焦绑定与有界复查）

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| 1A 什么都不做 | 保持 `sameHorizontalBand` 与单次 `Capture` | 无改动 | 所有半宽折叠评论条展开必报 `focus_unconfirmed` (`uncertain`) |
| 1B 仅自身唯一锚点展开 + 有界只读复查 | 放宽 `hasAnchor && anchorUnique` 的横向展开/增高守卫，并加 `[60ms, 120ms]` 有界聚焦复查 | 改动小，不引入父锚点 | 若某 App 评论 `EditText` 自身未设 `resource-id`/`content-desc`、仅父容器（如 `vg_editor_container`）有唯一 `id`，仍报 `focus_unconfirmed` |
| **1B + 1C（选定）** | 在 1B 基础上，为 `!hasAnchor` 的匿名 `EditText` 增加最近非窗口根、非滚动父容器双帧唯一 `resource-id`（且父下双帧各恰 1 个可编辑节点 + 全屏唯一聚焦）兜底；`focusTextRef` 与 `focusTextPoint` 共用有界只读复查 | 新增约百行纯 CPU 父锚点校验；真无法获焦场景多耗至多 `180ms` 休眠与至多 2 次 `Capture`（复查窗口上限 `1200ms`）后仍诚实报 `uncertain` | 点击假输入框后销毁原节点并新建不同 `resource-id` 弹窗框、或点击后 3 帧仍未获焦的慢控件仍停在 `uncertain` |

**选择理由**：用户确认采纳 **1B + 1C**。紧缩树对带文案 `input` 隐藏了 `resource`，同时覆盖自身锚点与唯一父容器锚点可消除匿名展开框盲区；全部锚点唯一性均在 focus 过滤前计数，并守住 `noAnchorNearCount(fresh) > 1` 拒绝、不缩窄与水平重叠几何防线，不违背契约 18 红线。

### 3.2 议题 2（Tier 2/3 无自有文案容器歧义消歧）

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| 2A 什么都不做 | 保持 Tier 1–3 任何 `actProbeAmbiguous` 立即中止 | 无改动 | 动态列表无字卡片微移一旦与其他卡片重叠 ≥50% 较小面积即误报 `target_ambiguous` |
| **2B 无自有文案有子容器在 Tier 2/3 歧义直入 Tier 5（选定）** | 仅当 `tier∈{2,3}`、`!disableTranslation`、`mode==actMatchTapLike` 且 `!hasOwnReadableText(want) && len(want.Children)>0` 时，遇 `actProbeAmbiguous` 跳出 Tier 1–3 直入 Tier 5（由 `E3` + `tier>=4` 全树遮挡裁决）；Tier 5 返回 `none` 时清除 `sawLineageMismatch` 如实返 `actProbeNone`（`target_missing`） | 仅多一次共享 `matchBudget` 的 Tier 5 扫描，无额外抓帧 | 两张卡片子树可读文案也完全相同（真歧义，仍报 `target_ambiguous`）或纯图片无文案卡片（E3 无据，仍拒绝） |
| 2C 改 `overlapRatio` 为双向覆盖/IoU | 收紧 Tier 2/3 几何门限 | 牵动全仓 Tier 2/3 几何契约与既有测试，且等大卡片移动 55% 仍会碰撞 | 伤及合理裁切控件且不能根治等大卡片互叠 |

**选择理由**：用户确认采纳 **2B**。该路径精准面向无自有语义信息的容器节点，借契约 33 已完备的 Tier 5 `E3` 与全树遮挡防线做终裁，零回归风险。

### 3.3 议题 3（遮罩/浮层感知的 `scroll` 静态候选与 Issue #48）

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| 3A 什么都不做 | 保持 `collectNoSafeTrajectoryCandidates`/`collectPageScrollCandidates` 仅按面积排序、不检遮挡 | 无改动 | 网易云抽屉 `n7628` 与弹窗遮罩下持续将底层页面标为 `executable=true` 诱导点关浮层 |
| **3B-1 静态候选跨窗口等值隔离 + 遮罩叶/实体后序浮层排除 + 同前景优先（选定）** | 保持 `scrollSafeSpansAt` 与 `u2.jar` 不变；在 `scroll` 全部 16 处静态 `422` 候选出口执行：① **O1 跨窗口等值隔离**（`want != nil` 时排除不同顶层窗口的候选，不依赖 `HashSet` 窗口序）；② **O2 无子无文案外部遮罩叶判定**（顺序无关，捕获 `v_mask`/`shadow`/`touch_outside`）；③ **O3 同窗后序实体浮层面板判定**（以精确 2D 并集占比 `>=50%` 区分实体抽屉面板与空心全屏透明 HUD 包装层，对空心包装层递归子控件）；④ 保留候选按同前景层（公共祖先深度）优先排序；保持 `candidates` 6 字段不变 | 不改固定 `u2.jar`、不扩大 `candidates` 协议面、不引入错误窗口顺序假设或单窗口 z 序派发猜测 | 单窗口 `DrawerLayout` 抽屉 `n7628` 仍保守返回 `no_safe_trajectory`（但不再误推底层 `n7405`/`n7416` 为 `executable=true`，调用方可明确选用 raw `swipe`）；无点击遮罩且以多窗口形式弹出、但坐标请求无 `want` 时因 `u2.jar` 无 `window-layer` 无法识别跨窗上下序 |
| 3B-2 保留被遮挡候选并扩展 `occluded`/新 `relation` 字段 | 扩展 `candidates` 对象字段或 `relation` 枚举保留后层候选 | 改变 `candidates` 线缆契约与 client/MCP 解析测试，且增加错误返回 token | 即使标 `executable=false`，前景无可滚时仍把背景页塞进候选列表，不如直接排除干净 |
| 3C 改造固定 `u2.jar` 输出 `window-layer` 或在单窗口 `scrollSafeSpansAt` 猜兄弟 z 序 | ① 补丁 `u2.jar` 改 `HashSet` 为有序层并输出 `window-layer`；或 ② 单窗口按先序放行跨兄弟滚动 | 方案 ① 需重建、校验并重新发布固定 `u2.jar`（触发条件：若后续出现大量无遮罩多窗口弹窗需要直接受控滚动，再单独开案补丁 `u2.jar`）；方案 ② 违背“不在树模型中猜单窗口 z 序”边界 | 方案 ② 在高 `elevation` 前序滚动浮层上会反向漏检遮挡 |

**选择理由**：用户已确认采用 3B-1 路线关闭 Issue #48；审阅阶段直接反汇编核查固定 `u2.jar` 发现 `getWindowRoots()` 使用 `HashSet`，据此果断将 O1 收口为 **100% 顺序无关的跨窗口等值隔离（`winOf[cand] != winOf[want]`）** 并保持 `scrollSafeSpansAt` 保守不变，同时用实体面板面积并集与遮罩叶规则修补空心 HUD 与播放页前序容器边界，做到零假前提、零派发风险、完整满足 Issue #48 全部目标。

---

## 4. 判据

以下逐项给出可由代码与单测直接判定的精确规约。

### 4.1 展开输入框聚焦绑定与有界只读复查（议题 1）

#### 4.1.1 展开几何守卫 `expandedTextGeometry(oldRect, freshRect device.Rect) bool`
当且仅当以下全部成立时返回 `true`：
1. `oldRect.Width() > 0 && oldRect.Height() > 0 && freshRect.Width() > 0 && freshRect.Height() > 0`；
2. **不缩窄 / 不显著缩高**：
   - `tolW := max(8, oldRect.Width()/5)`、`tolH := max(8, oldRect.Height()/5)`；
   - `freshRect.Width() >= oldRect.Width() - tolW` 且 `freshRect.Height() >= oldRect.Height() - tolH`；
3. **水平投影覆盖守卫**：
   - `interW := min(oldRect.Right, freshRect.Right) - max(oldRect.Left, freshRect.Left)`；
   - `interW > 0 && interW*2 >= oldRect.Width()`（新框在 X 轴投影至少覆盖原框宽度的一半；允许左半宽胶囊向右或向两侧展开为全宽，禁止横向跳到屏幕另一侧互不相交的独立输入框）；
4. Y 轴位置与高度增幅不受 `sameHorizontalBand` 的 `±20%` 上限约束（容忍软键盘顶起与多行编辑器增高）。

#### 4.1.2 真实窗口根统一判定 `isRawWindowRoot(capture *device.Capture, index int) bool`
与 `lineageL1`（[act_revalidation_policy.go:359-363](../../internal/engine/act_revalidation_policy.go#L359-L363)）严格对齐：当 `index < 0 || index >= len(capture.Nodes)` 返回 `true`；否则当节点自身为 `capture.Root`、`Parent == -1`、`isSyntheticDisplayRoot(node)`、或其 `Parent`（有效下标）满足 `isSyntheticDisplayRoot(&capture.Nodes[node.Parent])` 时返回 `true`。由此保证点击聚焦导致单窗口切多窗口（软键盘/悬浮窗出现）时，1C 对真实窗口根的排除在双帧完全一致。

#### 4.1.3 `bindFocusedText` 绑定顺序与 1B / 1C 判据
前置条件不变：`original`/`fresh`/`want` 非空、`want` 可见/可编辑/启用/非密码、同 `Display.ID`、且 `sameTextTaskContext(expected, fresh.Foreground)` 为真。

1. **分支 A：自身存在稳定锚点（`hasAnchor == true`，即 `want` 具有非空 `ResourceID` 或非空 `Description`）**：
   - 必须在 focus 过滤前满足双帧唯一：`anchorCount(original, want, kind, anchor) == 1 && anchorCount(fresh, want, kind, anchor) == 1`；否则立即返回 `false`。
   - 依次尝试：
     - `exact`（同 `Bounds`）→ `near`（包含 `(tapX, tapY)` 且 `overlapRatio >= 0.8`）→ `shifted`（`sameHorizontalBand`）→ **`expanded`（新增，1B）**；
   - `expanded` 候选必须满足：`node.DisplayID == want.DisplayID`、`textCandidateSafe(node)`、`sameTextSourcePresence(want, node)`、`stableTextAnchor(node)` 与 `(kind, anchor)` 精确一致、`!node.Bounds.Empty() && rectValidOnScreen(node.Bounds, fresh.Display.Bounds())`、且 `expandedTextGeometry(want.Bounds, node.Bounds)` 为真；仅当匹配候选恰有 `1` 个时绑定成功，返回 `textBinding{raw: cand, mode: textExpanded, anchor: anchor, anchorKind: kind, capture: fresh}, true`。
2. **分支 B：自身无稳定锚点（`!hasAnchor`）**：
   - 仍要求 `noAnchorNearCount(original, want, tapX, tapY) == 1`（点击前该位置附近不得有重复匿名输入框，防 focus 过滤消歧）；若不为 `1` 立即返回 `false`。
   - 计算 `freshNear := noAnchorNearCount(fresh, want, tapX, tapY)`：
     - 若 **`freshNear > 1`**：立即返回 `false`（严禁多匿名重叠候选穿透到 1C，避免与无父锚点信息的 `textTargetOf` 冲突）；
     - 若 `freshNear == 1`：优先按既有 `near` 路径绑定（`mode = textNear`），命中即返回；
   - 若 `near` 未绑定（即 `freshNear == 0`，或 `freshNear == 1` 且原点处无安全聚焦命中），且 **`wantIdx := indexOf(original, want)` 有效（`>= 0`）**（确保只对属于 `original` 的 `want` 启用，不对零点击快路径跨帧指针误触发），进入 **父容器唯一 `resource-id` 锚点兜底（1C）**：
     1. 沿 `original.Nodes[wantIdx].Parent` 向上回溯（步数上限 `maxParentAnchorDepth = 8`，`seen` 集合防环）：
        - 遇越界、`isRawWindowRoot(original, parent)`、或中间祖先满足 `Editable || (HasScrollable && Scrollable)` 时停止；
        - 取遇到的**第一个**满足 `anc.DisplayID == want.DisplayID && anc.Package == want.Package && anc.ResourceSet && anc.ResourceID != ""` 的祖先作为父锚点 `parentKey = (DisplayID, Package, ClassName, ResourceID)`；未找到则失败返回 `false`。
     2. **双帧父锚点唯一性**：在 `original` 与 `fresh` 全树中匹配 `parentKey`（且 `!isRawWindowRoot`、非 `Editable`、非 `Scrollable`）的节点各必须**恰有 `1` 个**（记为 `oldParentIdx` 与 `freshParentIdx`）。
     3. **父下可编辑子框唯一性（focus 过滤前计数，带 `seen` 防环与越界保护）**：从 `oldParentIdx` 与 `freshParentIdx` 各自向下搜索深度差 `<= maxParentAnchorDepth`、且不跨越任何中间 `Scrollable` 或 `Editable` 边界的可编辑后代中，与 `want` 同 `(DisplayID, Package, ClassName)` 的节点总数在两侧均必须**恰有 `1` 个**（且 `original` 侧那唯一 1 个必为 `wantIdx`）。
     4. **安全、聚焦与几何门禁**：`fresh` 侧那唯一可编辑后代 `cand` 必须同时满足：
        - `sameTextSourcePresence(want, cand)`（保持无 `resource-id`/无 `description` 的 source presence 一致）；
        - `textCandidateSafe(cand)`（可见、可编辑、启用、非密码、`focused=true`）；
        - `!cand.Bounds.Empty() && rectValidOnScreen(cand.Bounds, fresh.Display.Bounds())`；
        - `sameHorizontalBand(want.Bounds, cand.Bounds) || expandedTextGeometry(want.Bounds, cand.Bounds)`；
        - `fresh` 在 `want.DisplayID` 上满足 `textCandidateSafe` 的聚焦输入框总数恰为 `1`（全屏唯一安全聚焦框）。
     5. 全部通过则返回 `textBinding{raw: cand, mode: textParentAnchor, capture: fresh}, true`。

#### 4.1.4 有界只读聚焦复查（`focusTextRef` 与 `focusTextPoint`）
- 常量/变量（参考 `textVerifyDelays` 设计为包内 `var` 以便单测覆写，不对外暴露）：
  - `var textFocusPollDelays = []time.Duration{60 * time.Millisecond, 120 * time.Millisecond}`（总休眠至多 `180ms`，含首帧共至多 3 次 `Capture`）；
  - `const textFocusRecheckMaxElapsed = 1200 * time.Millisecond`（**从第 1 次 `Capture` 返回且未绑定后开始计时**，避免第 1 次正常真机 `Capture` 耗去 300–500ms 后截断复查；且单次 `e.dev.Capture` 直接传 `work.Ctx`，不用几十毫秒残余子 deadline 中途掐死在途 RPC，与 `dispatchTextSubmitTap` [text_submit.go:565](../../internal/engine/text_submit.go#L565) 一致）。
- 一次 `act(text)` 至多派发 **1 次**聚焦 `Tap`；仅在 `Tap` 返回 `DispatchTerminated && tapErr == nil` 且未撤权后进入聚焦读帧循环：
  - 第 1 次抓取（`attempt = 1`，`0ms`）：立即 `Capture`（不睡眠）；若绑定成功直接进入 `stage=write`（0ms 额外等待）。
  - 每次 `Capture` 返回后的即时中止条件（**不睡眠、不再重试**）：
    1. `e.workRevoked(work) || work.Ctx.Err() != nil` → 返回 `409 control_revoked`（`reason=authorization_revoked`，**因聚焦 `Tap` 已终止，整体保持 `OutcomeUncertain`**，与 [act.go:2035-2037](../../internal/engine/act.go#L2035-L2037) 一致）；
    2. `captureErr != nil` → 返回 `409 text_target_uncertain`（`stage=focus, reason=focus_unconfirmed, outcome=uncertain`）；
    3. `!textSubmitCaptureTrusted(&afterTap, session.Display) || !e.textSubmitDisplayCurrent(work, session.Display)` → 返回 `focus_unconfirmed`；
    4. 上下文变化：`focusTextRef` 检查 `!sameTextTaskContext(fresh.Foreground, afterTap.Foreground)`，`focusTextPoint` 检查 `foregroundMatches(fresh.Foreground, afterTap.Foreground) != nil` → 返回 `focus_unconfirmed`；
    5. `focusTextRef` 中 `validateTapRawTopology(&afterTap)` 失败 → 返回 `focus_unconfirmed`。
  - 若上述检查全过但本帧绑定未成功（例如聚焦/展开动画尚未完成），且还有剩余退避步（`60ms`、`120ms`）与剩余复查窗口（`time.Until(recheckDeadline) > 0`）：
    - 调用 `pauseInterruptible(work.Ctx, min(delay, remaining))`；若被 STOP/取消唤醒，立刻返回 `409 control_revoked`（`OutcomeUncertain`）；
    - 若 `time.Until(recheckDeadline) <= 0`，直接跳出返回 `focus_unconfirmed`；
    - 否则调用 `e.dev.Capture(work.Ctx, session.Display)` 执行下一次只读抓取与绑定检查。
  - 全部尝试耗尽仍未绑定 → 返回 `409 outcome=uncertain, code=text_target_uncertain, stage=focus, reason=focus_unconfirmed`。

### 4.2 Tier 2/3 无自有文案容器歧义转入 Tier 5（议题 2）

在 [act_revalidation_policy.go](../../internal/engine/act_revalidation_policy.go#L1018-L1100) 定义确定性准入函数 `canDisambiguateViaTier5(want *device.RawNode, tier int, options actMatchOptions) bool`：
- 返回 `true` 当且仅当以下 4 条同时成立：
  1. `tier == 2 || tier == 3`（Tier 1 歧义永不进入）；
  2. `!options.disableTranslation`（`submit_ref` 与坐标拥有者兜底禁用 Tier 5，保持立即返回 `actProbeAmbiguous`）；
  3. `options.mode == actMatchTapLike`（仅适用于常规点击/长按/输入目标；`actMatchScroll` 与 `actMatchTapLikeExact` 不进入）；
  4. `want != nil && !hasOwnReadableText(want) && len(want.Children) > 0`（无自有可读文本/描述的有子容器；带自有文本或无子节点的控件保持立即返回 `actProbeAmbiguous`）。
- 触发行为：
  - 在 `for tier := 1; tier <= 3; tier++` 中收到 `actProbeAmbiguous` 且 `canDisambiguateViaTier5` 为真时，置 `disambiguateInTier5 = true` 并**立即 `break`** 退出 Tier 1–3 循环；
  - 跳过 Tier 4（`tier == 4` 要求 `sawLineageMismatch && !disambiguateInTier5`），直接运行既有 Tier 5（`probeWantTier5(options.mode, originalIndex)`）；
  - Tier 5 终态映射：
    - `actProbeUnique` → 返回 `actMatch{raw, tier: 5, independentContent: true, independentSubtree: true, stats}`（后续自动进入 `tier >= 4` 全树遮挡证明 `tapFrameTier4OcclusionProven` 与 `bubble_colocated` 同步位移守卫）；
    - `actProbeAmbiguous` → 返回 `actProbeAmbiguous`（映射 `target_ambiguous`，不复检）；
    - `actProbeNone` → 终态返回 **`actMatch{stats: stats, lineageMismatch: sawLineageMismatch && !disambiguateInTier5}, actProbeNone`**（清除此前 Tier 1/2 可能置位的 `sawLineageMismatch`，避免误报 `lineage_changed` 与 `changed_ref`，准确映射 `target_missing` 并允许 `Act` 既有的至多 1 次 `150ms` 瞬态复检）；
    - `cancelled` / `budgetExhausted` / `malformed` → 原样返回对应终态。

### 4.3 遮罩/浮层感知的 `scroll` 静态候选过滤与排序（议题 3 / Issue #48）

**保持 `scrollSafeSpansAt` 现有派发逻辑完全不变**。本节规则统一封装为 `buildFilteredScrollCandidates(obs, view, operation, want, entries)` 并接入 [scroll.go](../../internal/engine/scroll.go) 的**全部 16 处** `buildStaticCandidates` 静态 `422` 出口（包括 `ancestor_invalid`/`ancestor_missing_action`/祖先 `axis_mismatch` 出口统一传 `want = target` 以保持最贴近用户目标的前景上下文；坐标无单目标出口传 `want = nil`），且不改变 `candidates` 既有的 6 字段契约。

单次调用先在 `O(N)` 内对 `view` 预计算每个公开节点的深度、公开父链、所属顶层窗口号 `winID`（当原始 `capture.Root` 为 `isSyntheticDisplayRoot` 且有 ≥2 个顶层窗口时，从原始 `capture.Nodes[capture.Root].Children` 推导并经 `projection.Source` 及后代原始源映射到每个公开节点；单窗口全为 `0`），确保即使 `observation/build.go` 折叠透明窗口根，也能精确识别任意两公开节点是否属于同一顶层窗口，**绝不跨越不同顶层窗口比较兄弟顺序**。

#### 4.3.1 关键辅助谓词
1. **无子无文案遮罩叶 `isBackdropScrimNode(node *protocol.Node) bool`**：
   - `node != nil && !node.Context && node.Role != protocol.RoleRoot && isTouchInteractive(node) && !isScrollContainer(node) && len(node.Label) == 0 && node.Value == nil`；
   - 且其子树（`node.Children` 有界遍历）内**不包含**任何带 `Actions`、非空 `Label` 或非空 `Value` 的后代节点（精准覆盖 `v_mask`/`shadow`/`touch_outside` 及其可能包裹的纯装饰底图，绝不误判带播放控制栏/文字的视频播放器容器）。
2. **实体浮层面板 `isSolidOverlayPanel(node *protocol.Node, nodeRect device.Rect, screen device.Rect) bool`**：
   - 若 `isTouchInteractive(node) || isScrollContainer(node)`：返回 `true`；
   - 否则从 `node.Children` 收集顶层有效内容矩形（有界 DFS：遇 `isTouchInteractive(d) || isScrollContainer(d) || (len(d.Children) == 0 && !anonymousPlainNode(d))` 时记录 `d` 在 `nodeRect ∩ screen` 内的裁剪矩形并**停止向该分支更深递归**），用纯整数 2D 扫描线求精确并集面积 `rectUnionArea64(rects)`（避免重叠子节点重复计数），当且仅当 **`rectUnionArea64(rects) * 100 >= rectArea64(nodeRect) * 50`** 时返回 `true`。
   - *对照效果*：网易云抽屉 `n7611 [0,0,864,2400]`（含 `n7628` 滚动区占 80.9% + 顶底栏，并集 ~85%）→ **实体面板**；Android 常见全屏透明 HUD 包装层 `[0,0,1080,2400]`（仅含顶栏+底栏+FAB，并集 ~14%）→ **空心包装层（非实体）**。

#### 4.3.2 排除判定 `isScrollCandidateOverlayOccluded(ctx, want, cand, direction, amount) bool`
对每个候选滚动容器 `cand`（裁剪矩形 `candRect`），满足以下任一条即判为被前景浮层/遮罩覆盖并**直接排除出 `candidates`**：

1. **（O1）跨顶层窗口等值隔离（顺序无关，免疫 `HashSet`）**：
   - 若 `multiWindow == true && want != nil` 且 `winID[cand] != winID[want]`：候选与请求目标不属于同一顶层窗口，直接排除。
2. **（O2）顺序无关的外部遮罩叶覆盖**：
   - 遍历公开树中所有非 `root`、非 `context`、`E != cand`、与 `cand` 互不为祖先/后代、且满足 `isBackdropScrimNode(E)` 与**无 `role=scroll` 祖先**的节点 `E`：
     - 若 `eRect`（裁剪到屏幕）满足 `rectOverlapAtLeast(candRect, eRect, 50)`，且若 `want != nil` 时不违背同前景分支保护（即不存在 `commonAncestorDepth(want, cand) > commonAncestorDepth(want, E)`），则将 `eRect` 加入 `maskRects`。
   - *覆盖场景*：小黑盒 `O56 n1507 v_mask`（同窗前序 `elevation` 抬高）、B 站 `O77 n2799 shadow`、以及多窗口 Dialog 的 `touch_outside` 点击遮罩（即使 `want == nil` 且 `HashSet` 乱序也能命中）；同时绝不误伤视频播放器容器上的前景评论列表。
3. **（O3）同窗口后序实体浮层兄弟覆盖（递归穿透空心 HUD 包装层）**：
   - 沿 `cand` 的公开父链向上遍历每个祖先 `L`（包含 `Tree.Root` 以兼容透明窗口根被折叠的场景）；设 `candBranch` 为 `L` 下包含 `cand` 的直接子分支，枚举 `L` 下所有排在 `candBranch` **之后**且**同顶层窗口**（`!multiWindow || winID[S] == winID[cand]`，绝不跨 `HashSet` 窗口序比较异窗兄弟）的同级兄弟 `S`（`childIdx(S) > childIdx(candBranch)`），对每个 `S` 执行 `collectLaterOverlay(S)`：
     - 若 `S` 无有效裁剪矩形 `sRect` 或不包含任何交互/滚动节点（`!subtreeHasInteractiveOrScroll(S)`），跳过；
     - 若 `isSolidOverlayPanel(S, sRect, screen)` 为真（实体前景面板或交互/滚动控件）：
       1. 若 `want != nil` 且 `want` 等于 `S` 或为 `S` 的公开后代，且 `rectOverlapAtLeast(candRect, sRect, 50)` → **直接排除 `cand`**（防侧边抽屉右侧露出 20% 遮罩条时横向滚动误推底层）；
       2. 将 `sRect` 加入 `maskRects`（不再向 `S` 内部重复展开）；
     - 若 `isSolidOverlayPanel` 为假（空心透明 HUD 包装层）：不把 `sRect` 加入 `maskRects`，也不以整框触发 O3-1，而是对其每个子节点递归调用 `collectLaterOverlay(child)`（只把内部真实的顶栏、底栏、FAB 或实体子弹窗计入 `maskRects` / O3-1）。
4. **O2 + O3 联合轴向覆盖判定**（当 `maskRects` 非空时）：
   - 计算 `cand` 在请求方向 3 条探测轴线（`50% / 25% / 75%`）上的安全区间，满足以下**任一**条件即排除 `cand`：
     - **条件 ①（因浮层失活）**：不加 `maskRects` 时（仅按既有 `scrollSafeSpansAt`）至少有 1 条轴线可满足请求 `amount` 最小行程，但叠加 `maskRects` 扣除后 3 条轴线均无法满足；
     - **条件 ②（大面积全轴遮蔽）**：仅从 `cand` 自身基础区间扣除 `maskRects`（不计其他滚动阻塞）后，3 条轴线均无法满足请求 `amount` 最小行程，**且** `maskRects` 与 `candRect` 交集的精确 2D 并集面积覆盖 `candRect` **`>= 50%`**（从而同时排除已被背景滚动阻塞或轴向不兼容但被抽屉/`v_mask` 大面积覆盖的 `n7026`/`n7479`/`n1360`/`n1418`，且绝不误删同层仅被底部 6% `miniPlayBar` 轻触的不可执行候选）。

#### 4.3.3 同前景层优先排序与 `executable` 一致性
- 任何未被 O1/O2/O3 排除的候选仍经既有 `tryPlanScrollBase` 计算 `executable`（保证取首个 `executable=true` 在同一 O 上重试必过静态准入）。
- 当 `want != nil` 时，对保留候选按 **公共祖先深度 `commonAncestorDepth(view, want, cand)` 降序**（同前景分支优先于跨分支）稳定排序，其次保持原出口相对顺序（`no_safe_trajectory` 仍子容器/面积降序、`page_scroll` 面积降序），最后交由 `buildStaticCandidates` 按 `executable=true` 优先截取前 4 项。
- 当全部候选均被 O1/O2/O3 排除（前景抽屉/弹窗无可滚容器）时，`candidates` 为空并在 JSON 中省略，绝不回退背景页面。

---

## 5. 算法

### 5.1 聚焦绑定与有界只读复查（议题 1）

```text
func bindFocusedText(original, fresh, want, expectedFg, tapX, tapY):
    check want safe & same display & sameTextTaskContext(expectedFg, fresh.Foreground)
    kind, anchor, hasAnchor := stableTextAnchor(want)
    if hasAnchor:
        if anchorCount(original, want, kind, anchor) != 1 ||
           anchorCount(fresh, want, kind, anchor) != 1:
            return false
        if unique exact match -> return (exact, anchor, kind, true)
        if unique near match  -> return (near, anchor, kind, true)
        if unique shifted (sameHorizontalBand) match -> return (shifted, anchor, kind, true)
        if unique expanded (expandedTextGeometry && rectValidOnScreen) match:
            return (expanded, anchor, kind, true)
        return false

    // !hasAnchor
    if noAnchorNearCount(original, want, tapX, tapY) != 1:
        return false
    freshNear := noAnchorNearCount(fresh, want, tapX, tapY)
    if freshNear > 1:
        return false // 预聚焦多候选歧义，禁止穿透 1C
    if freshNear == 1:
        if unique near match -> return (near, true)
    // 1C: 仅当 want 属于 original 时尝试最近非窗口根非滚动父容器唯一 resource-id 兜底
    if wantIdx := indexOf(original, want); wantIdx >= 0:
        return bindFocusedTextByParentAnchor(original, fresh, wantIdx, want)
    return false

func focusTextWithRecheck(work, op, session, fresh, isRef, bindFn):
    send at most 1 Tap -> if not_dispatched: 503; if err/!terminated: 504 uncertain
    started := time.Now()
    attempt := 0
    mode, outcome, reason := "none", OutcomeUncertain, textReasonFocusUnconfirmed
    defer func() { e.logTextFocus(op, mode, attempt, outcome, reason, time.Since(started)) }()

    var recheckDeadline time.Time
    for step := 0; step <= len(textFocusPollDelays); step++:
        if e.workRevoked(work) || work.Ctx.Err() != nil:
            outcome, reason = "stopped", textReasonAuthorizationRevoked
            return 409 control_revoked (OutcomeUncertain)
        if step > 0:
            if step == 1 { recheckDeadline = time.Now().Add(textFocusRecheckMaxElapsed) }
            rem := time.Until(recheckDeadline)
            if rem <= 0 { break }
            if !pauseInterruptible(work.Ctx, min(textFocusPollDelays[step-1], rem)) ||
               e.workRevoked(work) || work.Ctx.Err() != nil:
                outcome, reason = "stopped", textReasonAuthorizationRevoked
                return 409 control_revoked (OutcomeUncertain)
            if time.Until(recheckDeadline) <= 0 { break }

        attempt++
        afterTap, err := e.dev.Capture(work.Ctx, session.Display)
        if e.workRevoked(work) || work.Ctx.Err() != nil:
            outcome, reason = "stopped", textReasonAuthorizationRevoked
            return 409 control_revoked (OutcomeUncertain)
        if err != nil { break } // 采集错误不睡眠、立即收口 focus_unconfirmed
        if !textSubmitCaptureTrusted(&afterTap, session.Display) ||
           !e.textSubmitDisplayCurrent(work, session.Display) { break }
        if isRef && !sameTextTaskContext(fresh.Foreground, afterTap.Foreground) { break }
        if !isRef && foregroundMatches(fresh.Foreground, afterTap.Foreground) != nil { break }
        if isRef && validateTapRawTopology(&afterTap) != nil { break }

        if bound, ok := bindFn(&afterTap); ok:
            mode = string(bound.mode) // point 路径显式记 "point"
            outcome, reason = "proven", ""
            return bound, nil

    return 409 text_target_uncertain (stage=focus, reason=focus_unconfirmed, OutcomeUncertain)
```

时间轴示意：
- **常规同步聚焦（T0）**：`Tap` → `attempt=1` 立即 `Capture` 命中 `exact`/`near`/`shifted`/`expanded`/`parent_anchor`/`point` → 进入 `SetTextOnDisplay`（0ms 额外等待）。
- **动画延迟展开（T1）**：`Tap` → `attempt=1` 尚未 `focused=true` → `pause(60ms)` → `attempt=2` 命中 `expanded`/`parent_anchor` → 进入 `SetTextOnDisplay`（仅 1 次 `Tap`）。
- **STOP 到达（T2）**：无论在 `pauseInterruptible` 还是 `Capture` 前后，立即返回 `409 control_revoked`（保持 `OutcomeUncertain`）并由 `defer` 打 `outcome=stopped` 日志，绝不进入写入阶段。
- **采集报错 / 任务切换（T3）**：第 1 帧报错或切走前台任务，**0ms 额外等待**立即返回 `409 focus_unconfirmed`。

### 5.2 Tier 2/3 容器歧义转入 Tier 5（议题 2）

```text
func matchActTargetWithOptions(ctx, revoked, original, fresh, want, options):
    sawLineageMismatch := false
    disambiguateInTier5 := false
    for tier := 1..3:
        check STOP
        probe := newTierMatcher(..., tier, options).probeWant(originalIndex)
        switch probe.state:
        case actProbeNone:
            if probe.lineageMismatch { sawLineageMismatch = true }
            continue
        case actProbeUnique:
            return match(tier), actProbeUnique
        case actProbeAmbiguous:
            if canDisambiguateViaTier5(want, tier, options):
                disambiguateInTier5 = true
                break // 直接退出 Tier 1-3，跳过 Tier 3 与 Tier 4，直入 Tier 5
            return stats, actProbeAmbiguous
        default: // cancelled / budgetExhausted / malformed
            return stats, probe.state

    if sawLineageMismatch && !disambiguateInTier5:
        // Tier 4 保持原逻辑不变
        ...
    if !options.disableTranslation:
        check STOP
        probe := newTierMatcher(..., 5, options).probeWantTier5(options.mode, originalIndex)
        // unique -> 返回 tier=5（自动触发 tier>=4 全树遮挡证明与 bubble_colocated 同步位移守卫）
        // ambiguous -> actProbeAmbiguous
        // none -> 落出循环返回 lineageMismatch: sawLineageMismatch && !disambiguateInTier5 (即 target_missing)
        // cancelled / budget / malformed -> 原样返回
```

### 5.3 `scroll` 静态候选遮罩感知过滤与排序（议题 3）

```text
func buildFilteredScrollCandidates(obs, view, operation, want, entries):
    // 单次 O(N) 预计算：窗口编号 winID、窗口根 windowRoot、深度、父链子序号、实体面板缓存
    ctx := newScrollOverlayContext(obs, view)
    kept := []
    for entry in entries:
        if isScrollCandidateOverlayOccluded(ctx, want, entry.node, operation.Direction, operation.Amount):
            continue // O1(跨窗等值隔离) / O2(遮罩叶) / O3(同窗后序实体浮层或递归子遮挡) 排除
        kept = append(kept, entry)
    if want != nil:
        stableSort kept by ctx.commonAncestorDepth(want, entry.node) desc
    return buildStaticCandidates(obs, view, operation, kept) // 仍按 executable=true 优先截取前 4 项
```

**异常、撤销、超时与预算耗尽行为**：
- 所有 `focus` 复查、Tier 5 消歧与 `scroll` 静态候选过滤均运行在 **`e.mu` 锁外**；
- 静态 `scroll` 候选过滤不执行任何设备 I/O、不认领 `O`，拓扑损坏仍一律返回 `503 hierarchy_unavailable`；
- Tier 5 消歧共享既有 `matchBudget`（`candidateLimit`/`lineageLimit`/`evidenceLimit`），预算耗尽或树畸形**永远返回 `503 hierarchy_unavailable`**；
- STOP 在任意步骤**永远优先返回 `409 control_revoked`**，不派发、不交付迟到候选。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| [internal/engine/act.go](../../internal/engine/act.go) | 在 `bindFocusedText` 新增 `textExpanded`、`textParentAnchor`、`textPoint`、`expandedTextGeometry`、`isRawWindowRoot`、`nearestParentResourceAnchor`、`bindFocusedTextByParentAnchor`（`expanded` 保留 `anchor`/`anchorKind`，Branch B 拦截 `freshNear > 1`）；将 `focusTextRef` 与 `focusTextPoint` 接入有界只读复查 `textFocusPollDelays`/`textFocusRecheckMaxElapsed`、前置显示/前景/拓扑校验与 `defer logTextFocus` |
| [internal/engine/act_revalidation_policy.go](../../internal/engine/act_revalidation_policy.go) | 新增 `canDisambiguateViaTier5`；在 `matchActTargetWithOptions` 中允许满足条件的容器于 Tier 2/3 `actProbeAmbiguous` 后跳出并直入 Tier 5（跳过 Tier 4），且终态 `actProbeNone` 返回 `lineageMismatch: sawLineageMismatch && !disambiguateInTier5` |
| [internal/engine/scroll.go](../../internal/engine/scroll.go) | 保持 `scrollSafeSpansAt` 不变；新增 `scrollOverlayContext`、`rectUnionArea64`、`isBackdropScrimNode`、`isSolidOverlayPanel`、`isScrollCandidateOverlayOccluded`（O1/O2/O3）与 `buildFilteredScrollCandidates`，统一接入 `scroll.go` 全部 16 处 `buildStaticCandidates` 静态 `422` 出口（祖先失败出口传 `want = target`） |
| [internal/engine/act_target_shared.go](../../internal/engine/act_target_shared.go) | 复用公共矩形面积/重叠辅助；保持 `buildStaticCandidates` 输出固定 6 字段与既有 `candidates` 排序截取契约不变 |
| 单元测试（`internal/engine/`） | 新增先红复现、反向对照与边界回归用例，覆盖 1B/1C 展开聚焦、多窗口根一致、有界复查与 STOP 中断、Tier 2/3 歧义转 Tier 5（含 Tier 3 `none` 归因 `target_missing`）与遮挡/双绑定守卫、O1/O2/O3 候选排除（含网易云抽屉 `O5641_167`、小黑盒 `O56`、空心透明 HUD、视频播放器前序容器与多窗口乱序测试） |
| [release/versions.json](../../release/versions.json) 及生成产物 | 仅改单一版本源为 `0.38.0` / `contract_version=34` / `android_version_code=40`，运行 `scripts/generate-version.ps1` 更新 Go/Java/XML 常量 |
| [README.md](../../README.md) 与 `docs/`（`interface.md`、`architecture.md`、`integration.md`、`client.md`、`control-protocol.md`、`phone-operations.md`、`release.md`、`runbook.md`；核对 `mcp.md`） | 同步契约 `34` 行为描述（`text` 展开聚焦与有界复查、Tier 2/3 无字容器歧义转 Tier 5、`scroll` 静态候选跨窗口隔离与前景遮罩过滤排序）及全量版本号引用（`0.38.0` / `34` / `40`）；实现提交前通过独立只读文档审计 |

### 6.2 配置与取值

所有阈值均内置于 `internal/engine`，**不暴露为请求参数或外部环境变量**：
- `var textFocusPollDelays = []time.Duration{60 * time.Millisecond, 120 * time.Millisecond}`（包内变量便于单测加速）、`const textFocusRecheckMaxElapsed = 1200 * time.Millisecond`（从 `attempt=1` 返回后开始计时）：覆盖真机 `Capture` 常规 150–400ms 耗时与展开动画窗口，单次 `Capture` 直接走 `work.Ctx` 不被短子 deadline 掐断；
- `maxParentAnchorDepth = 8`：与 `match.go` 透明包装穿透深度（8 层）保持一致，防止遍历越界爬到页面级根容器；
- `expandedTextGeometry` 不缩水容差 `max(8, oldSide/5)`（20%）、水平投影重叠 `>= 50%` 原宽：与 `sameHorizontalBand` 原有 20% 尺寸容差一致，确保 `source_mismatch_horizontal_width`（缩窄 50%）与异侧跳转均被拒绝；
- O2/O3 实体面板与大面积遮罩并集面积门槛 `50%`：与现有 `colocatedMinOverlapPercent = 50` 保持同尺度，精准区分实体抽屉/全屏遮罩（80%–100%）与空心透明 HUD（~14%）、底栏 `miniPlayBar`（~6%）及交叉列表（~35.5%）；
- 版本规划：`0.37.0 / contract_version=33 / android_version_code=39` → `0.38.0 / contract_version=34 / android_version_code=40`。

### 6.3 日志与可观测性

- 全部复用 [internal/eventlog/log.go](../../internal/eventlog/log.go#L63-L108) **已有白名单键**，无需新增日志键，绝不写入文本、节点 `ref`、`resource-id`、坐标或树内容：
  - 新增 `Debug` 事件 `engine.text.focus`（由 `defer` 统一记录）：`operation_id`、`mode`（`"exact"`/`"near"`/`"shifted"`/`"expanded"`/`"parent_anchor"`/`"point"`/`"none"`）、`attempt`（`1..3`）、`outcome`（`"proven"`/`"uncertain"`/`"stopped"`）、`reason`（失败/撤销时为闭集 `focus_unconfirmed`/`authorization_revoked`）、`ms`；
  - Tier 2/3 歧义转 Tier 5 命中后，既有 `act.target` 事件照常记录 `outcome="proven", tier=5, attempt=1|2`；
  - `scroll` 候选过滤为纯静态拒绝逻辑，不增加冗余日志事件。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威（`internal/engine`） | 保持：全部聚焦复查、Tier 5 消歧与静态候选过滤均由 `engine` 唯一判定 |
| STOP 不等待 | 保持：`focus` 复查全部经 `pauseInterruptible(work.Ctx, ...)`，`STOP` 到达立即唤醒并返回 `409 control_revoked`（保持 `OutcomeUncertain`） |
| 单向消费与至多一次原语 | 保持：`scroll` 静态拒绝仍不认领 `O`；`focusTextRef`/`focusTextPoint` 仍至多派发 1 次 `Tap`，复查全程只读 |
| 提交条件化与状态锁短小 | 保持：`Capture`、睡眠、父锚点扫描、Tier 5 扫描与遮挡计算全部在 `e.mu` 锁外执行 |
| 严格接入与三态省略 | 保持：不放宽 `/api/<tool>` 入参校验，不改变 `delta.set` 三态语义与 `candidates` 6 字段契约 |
| 事实分层 | 保持：聚焦复查仍未确认时诚实返回 `outcome=uncertain (focus_unconfirmed)`，绝不伪称成功；`scroll` 仍不承诺真实触摸消费者或业务效果 |
| 日志白名单与单一版本源 | 保持：仅用既有白名单枚举/计数/时长键；版本号只在 `release/versions.json` 手改并由脚本生成 |

### 6.5 契约与文档影响

- `/api/*` JSON 请求/响应字段、错误码、`error.details.candidates` 6 字段结构及 MCP 13 工具 schema 与目录指纹**均不变**；
- 共同行为契约随 `release/versions.json` 递增至 `contract_version=34`（`release_version=0.38.0`、`android_version_code=40`）；
- 必改正式文档：[README.md](../../README.md)、[docs/interface.md](../../docs/interface.md)、[docs/architecture.md](../../docs/architecture.md)、[docs/integration.md](../../docs/integration.md)、[docs/client.md](../../docs/client.md)（含共同契约版本号）、[docs/control-protocol.md](../../docs/control-protocol.md)、[docs/phone-operations.md](../../docs/phone-operations.md)、[docs/release.md](../../docs/release.md)、[docs/runbook.md](../../docs/runbook.md)；核对 `docs/mcp.md` 无版本号与语义漂移则不改；实现提交前通过 `AGENTS.md` 第 15 条独立只读文档审计。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **展开式输入框一次写入成功**：点击底栏半宽评论胶囊后横向展开/多行撑高（无论自身带唯一 `resource-id`/`content-desc`，还是自身匿名但父容器如 `vg_editor_container` 双帧唯一）且在复查窗口内获焦的输入框，单次 `act(text)` 直接完成聚焦、写入与读回确认，消除 `focus_unconfirmed` 误杀与额外 `observe` 往返。
2. **动态列表卡片平移免受兄弟重叠误杀**：RecyclerView / Epoxy 等同构列表卡片微移触发 Tier 2/3 几何重叠时，经 Tier 5 双向唯一子树指纹与全树遮挡证明直接完成点击，消除 `target_ambiguous` 误报与整树错误重试。
3. **关闭 Issue #48，杜绝浮层遮挡下的滚动候选误导**：
   - 多窗口场景下，调用方针对某窗口内节点请求 `scroll` 静态失败时，绝不跨窗推荐另一顶层窗口的滚动容器（免疫 `u2.jar` `HashSet` 乱序）；带点击遮罩的多窗口 Dialog 下即便是坐标请求也会被 O2 排除底层候选；
   - 单窗口侧边抽屉（如网易云 `O5641_167`）或全屏点击遮罩（如小黑盒 `O56 v_mask` / B 站 `O77 shadow`）打开时，被遮挡的底层页面滚动容器（`n7405`/`n7416`/`n7026`/`n7479`/`n1360`/`n1418` 等）被精准排除，不再标为 `executable=true` 诱导调用方点关浮层；前景浮层无可滚容器时明确返回空 `candidates`，不回退背景页；同时不误杀带空心透明 HUD 包装层或底部 `miniPlayBar` 的正常页面以及视频播放器上的前景评论列表。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 点击折叠输入条后，App 销毁原节点并新建一个**不同 `resource-id` 或不同类名**的独立弹窗输入框 | 无法在无障碍树层面证明新旧节点为同一输入控件；按契约 18 红线不凭“全屏唯一焦点”盲绑，仍诚实返回 `focus_unconfirmed` |
| 匿名展开框连最近非窗口根、非滚动父容器也没有双帧唯一 `resource-id`（或父下有 ≥2 个同类 `EditText`，或点击后原位附近出现 ≥2 个重叠匿名框） | 缺少双帧可证身份锚点，为防串框误写仍返回 `focus_unconfirmed` |
| 点击聚焦后动画耗时超过 3 次 `Capture` 或 `1200ms` 复查窗口仍未报告 `focused=true` | 聚焦复查受有限步数与时间窗口约束，超时后诚实返回 `uncertain` |
| 列表中相邻两张无字卡片连子树可读文案也完全一致，或均为纯图片无任何可读文案 | 前者为真实歧义（Tier 5 仍报 `target_ambiguous`），后者 E3 无语义证据（Tier 5 报 `target_missing`），均绝不猜测 |
| 多窗口顶层弹窗内 `scroll` 被底层窗口容器阻塞，或无点击遮罩的多窗口场景下坐标 `no_scroll_container` 无法判别窗口上下序 | 核实固定 `u2.jar` 的 `AccessibilityNodeInfoDumper.getWindowRoots()` 使用 `java.util.HashSet` 且未输出 `window-layer`，XML 顶层顺序不代表真实 z 序；本提案不修改 `u2.jar`，故保持 `scrollSafeSpansAt` 保守不变（如需多窗口确定 z 序，须另案补丁 `u2.jar`） |
| 单窗口侧边抽屉 `n7628` 自身请求受控 `scroll` 仍返回 `422 no_safe_trajectory`；或单窗口对抽屉区域发**坐标 `scroll`** 时若底层存在未阻塞缝隙导致 `competing` 仅剩 1 个可执行项（[scroll.go:1212-1260](../../internal/engine/scroll.go#L1212-L1260)） | Android 无障碍 XML 不提供单窗口兄弟真实 z 序，本提案故意不改动 `scrollSafeSpansAt` 的单窗口派发逻辑；静态 `422` 候选不再误推底层 `n7405`/`n7416`，调用方可明确使用 raw `swipe` 滚动抽屉 |
| 单窗口内被 `elevation` 抬到滚动容器之上、但自身**非 `clickable`** 且排在滚动容器**之前**的纯静态遮罩面板 | 既不满足后序兄弟规则（O3）也不属于触摸遮罩叶（O2），无障碍树中与普通背景静态底图不可区分 |

### 7.3 成本

- **常规成功路径**：`focus` 首帧命中 0ms 额外等待；Tier 1–3 唯一命中 0 额外扫描；`scroll` 正常派发路径 0 额外开销。
- **展开/慢聚焦路径**：首帧命中 0ms；慢动画场景最多增加 `180ms` 休眠与 2 次只读 `Capture`（复查窗口上限 `1200ms`），省去外部 1 次 `observe` + 1 次重试 `act`（~1,319 tok 与数秒模型往返）。
- **Tier 2/3 歧义消歧路径**：仅在无自有文案容器撞出歧义时多跑 1 次共享 `matchBudget` 的纯 CPU Tier 5 扫描，0 额外抓帧。
- **遮罩感知 `scroll` 候选过滤**：仅在静态 `422` 拒绝时触发，单次 `O(N)` 预计算 + 精确整数 2D 并集（公开节点通常 <150，微秒级），0 设备 I/O、0 额外响应字节。
- **日志**：每次聚焦流程最多新增 1 条几十字节的有界 `Debug` 事件 `engine.text.focus`。

---

## 8. 验证计划

### 8.1 单元测试

由主代理在代码与文档收敛后亲自执行全量测试（`GOEXPERIMENT=jsonv2 go test ./cmd/... ./internal/... ./scripts/... -count=1 -timeout 900s`），并确保每项至少含一条**先红反向对照用例**（去掉对应实现分支时测试必失败）：

| 用例 | 期望 |
| --- | --- |
| **先红对照 1B-A**：`TestTextFocusExpandedOwnAnchor` | 半宽折叠框（双帧唯一 `resource-id` 或 `description`）点击后横向展开为全宽且纵向位移，基线报 `focus_unconfirmed`；实现后命中 `textExpanded`（保留 `anchor`/`anchorKind`）并一次 `executed`；反向对照：宽度大幅缩窄（`900→500`）与水平完全无交叠异框仍拒绝 |
| **先红对照 1C-A**：`TestTextFocusExpandedParentResourceAnchor` | 匿名折叠框（无自身 `resource-id`/`desc`）嵌套在双帧唯一 `vg_editor_container`（含 1 层透明包装，且模拟 `afterTap` 弹出软键盘变为多窗口合成根）下展开后获焦，基线报 `focus_unconfirmed`；实现后命中 `textParentAnchor` 并一次 `executed`；反向对照：父容器不唯一、父为窗口根（含多窗口根）、父为 `ScrollView`、父下有 2 个 `EditText`（即使仅 1 个聚焦）、`noAnchorNearCount(fresh) > 1`、缩窄或屏幕存在第 2 个聚焦框均必须拒绝 |
| **先红对照 1-Poll**：`TestTextFocusBoundedRecheckAndStop` | `focusTextRef` 与 `focusTextPoint` 首帧未聚焦、第 2 帧（覆写短延迟后）获焦时一次 `executed`（`tap == 1`，`engine.text.focus` 分别记对应 `mode` 与 `mode="point"`）；`Capture` 报错或前台切换时立即终止零额外等待；退避期间 `STOP` 到达立即返回 `409 control_revoked`（保持 `OutcomeUncertain`，记 `outcome=stopped`）且不发写入 |
| **先红对照 2B-A**：`TestRevalidateTier2AmbiguousContainerDisambiguatesInTier5` | 复现小黑盒 `O35→O36` 推荐流 3 张同构 `epoxy_model_group_child_container` 卡片平移 `154px` 且贴边短卡重叠 ≥50%：基线在 Tier 2 报 `target_ambiguous`；实现后直入 Tier 5 凭唯一子树文案命中并执行 |
| **2B 安全边界矩阵**：`TestRevalidateTier23AmbiguousSafetyGuards` | ① 有自有文案按钮或无子节点叶子在 Tier 2/3 歧义仍立即报 `target_ambiguous`（不进 Tier 5）；② Tier 1 歧义不进 Tier 5；③ `disableTranslation=true`（`submit_ref`/坐标拥有者兜底）与 `actMatchScroll` 仍报 `target_ambiguous`；④ 消歧命中 Tier 5 后若新落点被外部按钮遮挡，被 `tapFrameTier4OcclusionProven` 拦截为 `scope_blocked`；⑤ 两张卡片子树文案也相同时 Tier 5 仍报 `target_ambiguous`；⑥ Tier 3（含前置 `sawLineageMismatch=true`）歧义转 Tier 5 且目标卡片文案被替换时，返回 `target_missing`（**而非** `lineage_changed`，且无 `changed_ref`） |
| **先红对照 3-O1**：`TestScrollStaticCandidatesCrossWindowIsolationOrderIndependent` | 合成显示根下 `[弹窗窗口, App 主窗口]` 与反转顺序 `[App 主窗口, 弹窗窗口]` 两种 `HashSet` 排列：① 请求弹窗内节点 `not_scrollable`/`no_safe_trajectory` 时，均**排除** App 主窗口滚动容器（与排列顺序无关）；② 弹窗内自带点击遮罩叶时，坐标 `no_scroll_container` 经 O2 在两种排列下均排除 App 主窗口滚动容器 |
| **先红对照 3-O2O3-Issue48**：`TestScrollOverlayOccludedCandidatesExcluded` | ① 复现网易云抽屉 `O5641_167`（`n7400=[n7402(n7405/n7416/n7026/n7479), n7611(实体抽屉含顶栏+n7628+底栏)]`）：`scroll(n7628)` 的 `no_safe_trajectory` **排除全部 4 个**底层容器（无 `executable=true` 底层候选）；抽屉内按钮纵滑/横滑 `not_scrollable`/`axis_mismatch` 均不回退底层页；② 复现小黑盒 `O56`（前序无子无文案 `v_mask` 盖住后序 `n1360`/`n1418`）：`not_scrollable` 排除 `n1360` 与 `n1418`；③ 抽屉内存在第 2 个未遮挡滚动容器时同前景层优先；④ **反误杀对照**：空心透明全屏 HUD 包装层（顶栏+底栏+FAB，实体占比 ~14%）下点击顶栏/底栏触发 `not_scrollable`/`no_scroll_container` 仍保留主列表为 `executable=true`；前序全屏可点视频播放器（含子控件/标签）不误杀后序前景评论列表；正常页面底部 `miniPlayBar`（6% 高度）与既有 `competing`/`half-screen`/`issue42` 候选全部保持 `executable=true` |
| **日志与回归**：`TestEventLogWhitelistAndExistingMatrix` | 验证 `engine.text.focus` 字段全部命中白名单、零文本/ref 泄露；全部现有 `act`/`scroll`/`text`/`protocol`/`client`/`mcp` 回归通过 |

### 8.2 实机/系统级

按 `AGENTS.md` 第 19 条默认发布交付范围与用户要求（含推送与回复关闭 Issue #48），由主代理亲自执行（无真机或设备离线时记录为未执行并停步汇报）：

1. **发版组装与双清单核验**：在已提交且工作区干净的源码树（见 §10 顺序，避免未跟踪提案触发 `source_dirty=true`）执行版本源更新（`0.38.0 / 34 / 40`）、`scripts/generate-version.ps1`、`scripts/build-release.ps1`、`scripts/verify-manifest.ps1` 与主机工具清单核验，核对产物 SHA-256、契约号与工具目录指纹。
2. **手机整组覆盖部署**：核验清单后对已连接 Android 设备覆盖部署控制 App、核心服务与手机 HTTP MCP 适配器，保留 App 私有数据。
3. **覆盖变更行为的最小实机闭环**：
   - 在真机虚拟屏会话中验证基础闭环（`start → observe → act → end`）；
   - 覆盖本次变更行为实测：
     - **展开输入框 `text(ref)`**：在支持折叠转展开评论栏的真实 App（如小黑盒评论栏或同类展开输入框）上执行 `act(text, auto_submit=false)`，验证单次调用直接 `executed`（无需追加 `observe`），并核查 `engine.text.focus` 日志；
     - **Issue #48 侧边抽屉 / 遮罩 `scroll` 候选**：在网易云音乐打开侧边抽屉（及/或遮罩弹窗）执行受控 `scroll`，验证返回的 `422` 不再将底层被遮挡页面滚动容器（原 `n7405`/`n7416` 类）标为 `executable=true`，不发生误导性关抽屉；
     - **列表卡片点击复验**：在信息流列表验证卡片 `ref tap` 闭环（动态 `154px` 精确重叠竞态如实机无法稳定复现，在验证记录中如实区分“已单测/已实测”）。
4. **Windows 主机工具替换与双端独立探针**：同一干净源码提交构建并替换 Windows 主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验主机清单后用新产物运行独立探针分别验证 **主机 stdio MCP** 与 **手机 HTTP MCP**。依赖方 Agent 配置、托管进程重启与重连默认由用户处理。
5. **推送与关闭 Issue #48**：完成实现、归档与实机验证后，推送提交至远程仓库，在 GitHub **Issue #48** 回复完整根因、契约 `34` 的跨窗口隔离与遮罩/实体浮层候选排除规则、设计取舍说明（不改 `u2.jar` 与 `candidates` 6 字段、不猜单窗口 z 序放行抽屉派发）、实机验证结果与实现/归档链接，并关闭 Issue #48 且复核状态为 `CLOSED`，最后将回复与关闭记录补入验证记录并提交推送。

### 8.3 记录

实现与验证结果写入 `maintenance/verification/act-focus-expansion-tier5-disambig-and-scroll-overlay.md`，严格区分已实现 / 已单测 / 已实测、磁盘替换 / 独立进程验证 / 依赖方切换，以及 Issue #48 回复关闭记录。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 打包范围 | 阶段 2 三项（议题 1、议题 2、议题 3 / Issue #48）合并为一案、同一次契约迭代（`0.38.0 / contract_version=34 / android_version_code=40`） |
| 议题 1 路线 | 1B（自身双帧唯一锚点横向展开/增高守卫）+ 1C（匿名输入框最近非窗口根非滚动父容器双帧唯一 `resource-id` 锚点兜底，含多窗口根一致与 `freshNear > 1` 拒绝）+ 有界只读聚焦复查（`[60ms, 120ms]`、复查窗口上限 `1200ms`，同时覆盖 `focusTextRef` 与 `focusTextPoint`，零二次 `Tap`） |
| 议题 2 路线 | 2B（仅 `tier∈{2,3}`、`!disableTranslation`、`mode==actMatchTapLike`、`!hasOwnReadableText(want) && len(want.Children)>0` 在 `actProbeAmbiguous` 后跳出 Tier 1–3 直入 Tier 5；Tier 5 `none` 清除 `sawLineageMismatch` 返 `target_missing`） |
| 议题 3 路线 | 保持 `scrollSafeSpansAt` 与固定 `u2.jar` 不变；在 `scroll` 全部 16 处静态 `422` 候选出口实施 O1（跨窗口等值隔离，免疫 `HashSet` 顺序）+ O2（顺序无关无子无文案遮罩叶）+ O3（同窗后序实体浮层面板并集 `>=50%` 判定、递归穿透空心 HUD 包装层）+ 同前景层优先排序；保持 `candidates` 6 字段不变 |
| 发布与 Issue 处理 | 按 `AGENTS.md` 第 19 条默认范围交付（干净树构建、手机整组部署、最小实机闭环、Windows 两枚 exe 替换与双端独立探针），随后推送并回复关闭远程 Issue #48 |

### 9.2 未决

无悬空未决项。全部路线、边界取值与发布范围均已通过前置讨论与审阅收敛确认。

---

## 10. 实施顺序

1. **编写先红复现用例（`internal/engine`）**：分别针对 1B/1C 展开聚焦与慢切焦、2B 列表卡片 Tier 2/3 歧义、3 O1/O2/O3 跨窗与网易云抽屉/`v_mask`/空心 HUD/视频播放页场景编写先红单元测试，确认基线必红。
2. **核心实现（三模块内部解耦，可顺序或分文件推进）**：
   - `internal/engine/act.go`：落地 1B/1C 绑定、有界只读聚焦复查与 `defer logTextFocus`；
   - `internal/engine/act_revalidation_policy.go`：落地 `canDisambiguateViaTier5`、直入 Tier 5 控制流与终态 `lineageMismatch` 清理；
   - `internal/engine/scroll.go` 与 `internal/engine/act_target_shared.go`：落地 `scrollOverlayContext`、`rectUnionArea64`、O1/O2/O3 排除与 `buildFilteredScrollCandidates` 接入全部 16 处出口。
3. **单测验证与反向对照**：确认全部先红用例由红转绿，补全边界与反向对照测试。
4. **版本源与正式文档同步**：更新 `release/versions.json` 至 `0.38.0 / 34 / 40`，运行 `scripts/generate-version.ps1`，同步更新 `README.md` 与 `docs/` 必改清单。
5. **全量测试（主代理亲测）**：执行 `GOEXPERIMENT=jsonv2 go test ./cmd/... ./internal/... ./scripts/... -count=1 -timeout 900s` 并确认交叉编译 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` 通过。
6. **独立只读文档审计门禁**：发起独立只读 subagent 按 `AGENTS.md` 第 15 条审核本次涉及的 `docs/` 与 `README.md`，修正全部发现项至 `CLEAN`。
7. **实现提交**：提交源码 + `release/versions.json` 生成文件 + `docs/`（此时提案状态仍为“提案，未实现”）。
8. **归档提交（形成干净 HEAD 供发布构建）**：将本提案移入 `maintenance/proposals/implemented/`、重写相对链接、补 §11 与头部实现 hash、撰写 `maintenance/verification/act-focus-expansion-tier5-disambig-and-scroll-overlay.md`（记录已单测结论）、更新 `maintenance/status.md` 并提交，确保工作区无未跟踪文件（`source_dirty=false`）。
9. **整组发布、实机部署与双端探针（主代理亲测）**：基于干净 HEAD 执行发布包构建与双清单核验、手机整组覆盖部署（保留 App 私有数据）、最小实机闭环、Windows 主机 `device-mcp.exe`/`mcp-probe.exe` 替换与主机 stdio / 手机 HTTP 独立探针。
10. **补记实机交付、推送与关闭 Issue #48**：将发布清单指纹、实机闭环与双端探针证据补入验证记录与 `maintenance/status.md` 后提交并推送至远程；在 GitHub Issue #48 回复完整根因、契约 `34` 方案取舍、验证结论与链接并关闭（复核 `CLOSED`），随后补记 Issue 关闭记录并提交推送。

---

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/act-focus-expansion-tier5-disambig-and-scroll-overlay.md`
（本节在归档移动之后补写，届时文件在 `implemented/` 下，故链接比新建时多一层）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| （归档时补写） | — |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| （归档时补写） | — |

---

## 附：本提案不改变的事项

- 不修改固定制品 `third_party/uiautomator/u2.jar` 及其 SHA-256 校验约束；
- 不改变 `scrollSafeSpansAt` 的现有保守轨迹证明规则；
- 不改变 `/api/*` 请求/响应 JSON 字段、严格入参解码规则、错误码列表、`error.details.candidates` 6 字段契约及 MCP 13 工具 schema 与目录指纹；
- 不改变 `O` 单向消费、`P` 单次派发、`STOP` 零等待撤权、恢复账本格式、消息边界与日志白名单键集合；
- 不改变 `text` 写后异步读回（`verifyTextWriteReadback`）与 `submit_ref`（`enter_key` / 节点提交）的既有契约；
- 不改变 `act` 坐标 `tap`/`long_press`/`text` 与 raw `swipe` 的原点证明策略；
- 不改变 `observation` 树投影、`tree v5` 语法、`match.go` 连续性算法与多基线池 `(12, 10, 5) @ 80%`；
- 不处理研究报告阶段 1（宿主侧速查卡/滑窗折叠）及阶段 3/4 事项（`sparse_tree`/`hint`、`target_center`、Schema 瘦身、`app`/`status`/`diagnose` 细节与树微剪枝）。
