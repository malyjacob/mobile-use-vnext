# 提案：紧凑树多子扁平化、有动作长标签截断、静止保号修复与基线池参数调整至 (12, 10, 5)（契约 29）

状态：提案，未实现。
基线提交：`e4f4d6da3241e4f262ace2aa5855806a0cf1be98`（分支 `master`，`2026-11-24`）。
适用范围：公开树标签空白净化（`internal/observation`）、静止页面保守保号（Stage 4 与 Stage 6）、紧凑观察树投影与固定基线差分（`compact` `ReadTree`/`ReadDelta`）、普通观察树文本编码（`tree v5`）、多基线池留存窗口与 `80%` 差分字节门槛（`internal/engine`）、MCP 发现说明与独立探针，以及整组发布与正式文档。
影响范围：`release/versions.json`、4 个生成版本文件、`internal/protocol`、`internal/observation`、`internal/treetext`、`internal/engine`、`internal/mcp`、`scripts/mcp-probe`、`README.md` 及 9 份正式文档 `docs/`。
契约（`/api/*` 字段与 `contract_version`）：改变（共同 `contract_version` 自 `28` 升至 `29`，`release_version` 自 `0.32.0` 升至 `0.33.0`、`android_version_code` 自 `34` 升至 `35`：`ReadNode` 与 `ReadSetNode` 新增非可空布尔字段 `truncated`；`tree v5` 保持块头不变，完整节点新增可选标记 `truncated=true`、`~` 行新增 `truncated=true|false`；默认观察行为变更涵盖公开 `label` 首尾空白清洗、`compact` 多分支隐藏匿名容器扁平化、`tree v5` 差分接受门槛调整至严格小于 `80%` 及基线池参数调整为 `(主=12, 副=10, 容量=5)`）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/observation/build.go`、`internal/observation/read.go`、`internal/observation/read_diff.go`、`internal/observation/match.go`、`internal/protocol/read_tree.go`、`internal/treetext/encode.go`、`internal/engine/capture.go`、`internal/mcp/tree_text.go`、`internal/mcp/schema.go`、`scripts/mcp-probe/result_text.go`、`scripts/mcp-probe/tree_body_consistency.go`）。

---

## 0. 摘要

在契约 `28`（`0.32.0`）完成外壳瘦身、截图缩放、基础净体与二元中心点后，观察树与差分机制仍存在四个集中瓶颈：多分支隐藏匿名容器阻断按钮提升并推深缩进、有动作超长标签防爆缺失与空白残余、静止页面图标容器与透明包装层引发 `ref` 抖动，以及副基线 `span=4` 引发“主降副瞬杀”。基于前期双会话研究报告（`.state/dsh-session-3e7a393a/research/mcp-context-research-0.32.0.md` §8）已与用户讨论收口的六项最终工程共识，本提案定案三层协同改动：

1. **Stage 1 与保号匹配（`internal/observation/build.go`、`match.go`）**：统一对公开 `label` 修剪首尾空白并将纯空白/NBSP 归空（不动 `device.Capture` 原串与 `RoleInput.Value`）；修复静止页面 `match.go` Stage 6 拒收纯装饰 `ImageView` 图标容器与 Stage 4/6 透明包装层遮挡的断层，严守“一般多子业务容器不进 Stage 6”。
2. **Stage 2 紧凑投影与契约字段（`internal/observation/read.go`、`read_diff.go`、`internal/protocol/read_tree.go`、`internal/treetext/encode.go`）**：在 `compact` 下行阶段早于结构变化，对有动作（`len(Actions) > 0`）非输入节点按单段 `250` 码点上限截断并设置独立非可空布尔标记 `truncated`（被动正文、`input.Value` 与 `inspect v3` 保持 100% 全文）；在上行阶段放开多分支隐藏匿名容器拍平（`M1`），自然释放被阻断的 `compactHoistsTextChildren` 按钮提升（`M2a`）。
3. **Engine 基线池与差分门槛（`internal/engine/capture.go`）**：在落地 `M1 + M2a` 后将 `tree v5` 差分接受门槛从 `<70%` 调至 `<80%`（止步于 `80%`，不上 `85%`），并将基线池参数从 `(主=10, 副=4, 容量=4)` 调优为 `(主=12, 副=10, 容量=5)`（`1 主 + 4 副`）。

本提案**明确不纳入**研究报告 §8.2 的全部放弃/暂缓项及正文（§1/§5.1）已暂缓的极简 `result v4` 外壳（通用兄弟文本合并 `S_plain`、混排半提升 `partial_hoist`、标签去重 `B3/B4`、盲升 `85%`、扩池至 20+ 步或按最后使用步续命、`M2b`、`M4b`、`M5a`、Base36 `ref`、去方括号、无差别 `T150`、极简 `result v4` 外壳）、DSH 宿主历史折叠与研究报告 §6 三项调用体验建议；亦不改变 `inspect v3`、`device.Capture` 与 `act` 规划重验语义。

---

## 1. 问题

### 1.1 现象与代价

根据基线 `0.32.0`（契约 `28`）在 DSH 实机双会话（主会话 `.state/dsh-session-3e7a393a/research/mcp-context-research-0.32.0.md`，133 棵观察树 / 8,828 个逻辑节点，以及同口径投影的对照会话 `dsh-session-2dd44960`，166 棵观察树）上的全量解析与离线同轨重放实测：

1. **多子匿名纯容器泛滥并阻断 347 处按钮提升**：
   - `3e7a393a` 中 `len(Children) >= 2` 的隐藏匿名容器占 `1,752` 节点（全树 `19.85%`），把单帧平均树深从 `0.24.0` 的 `8.58` 推高到 `10.09`（最高 `17` 层）；核查全会话 `90` 次 `act`/`inspect` 引用目标，`0` 次引用过这些匿名容器。
   - 对 `2,014` 个无文字 `tap` `view` 分类显示，`347` 处（`317` 无 `resource` + `30` 带 `resource`）实际只包着纯被动文本叶、中间仅隔一层多子匿名包装壳，因此无法命中只检查直接子级的文本提升。先拍平多子匿名容器再执行文本提升（`M1 + M2a`），仅此一项在 `3e7a393a` 上令节点降为 `6,208`（`-29.68%`）、最大树深 `17 → 10`、净省 `7,720` tree token（`-10.05%`）；在 `2dd44960` 上净省 `10,451` tree token（`-8.30%`）、节点 `-22.03%`、最大树深 `15 → 10`。
2. **有动作长尾炸弹与残余空白噪声**：
   - 标签文本以 `21,678` tok（`37.93%`）居 `tree v5 full` 成分首位。知乎搜索卡片 `O80 n896`（`button`，`tap`）将 `4,009` 字符科普全文塞入 `contentDescription`，单树暴涨至 `3,443` tok（`12,306` B，占全场 75 棵整树 `5.8%`）。
   - 双会话扫描 `>= 250` 字标签发现：`3e7a393a` 有动作 `1` 节点 / 无动作 `0` 节点；`2dd44960` 有动作 `0` 节点 / 无动作 `5` 节点（闲鱼 `338–480` 字商品正文）。仅截断有动作非输入节点 `250` 码点（`T250`）立省 `2,708` tok 且 100% 保全无动作正文。
   - 现行标签净化遗漏不含 `\u200b..\ufffc` 的纯空白/NBSP 叶 `5` 处（如 `\u00a0`、`\n`）与首尾空格 `262` 处（如 `"林俊杰 "`）。
3. **`tree v5` 字节门限 70%→85% 的“token 密度反转陷阱”**：
   - `tree v5 full` 实测密度 `0.3461 tok/B`，而 `delta` 高达 `0.4161 tok/B`（`+20.2%`）。在未剪枝的 `0.32.0` 树上盲目将门槛从 `70%` 升至 `85%`，虽增大了 delta 帧占比（`58 → 67` 帧），却因 `B` 比 `0.71–0.85` 的 delta 在 token 上反超整树（如 `O126` 字节比 `0.844` 但 token 比 `1.068`）及基线池饥饿级联，导致净增 **`+2,880 tok`**；即使调至 `80%` 在未剪枝树上仍净赔 `+379 tok`。
   - 只有在落地 `M1 + M2a + B1/B2 + 有动作 T250` 之后再将字节门槛调至 **`80%`**，`3e7a393a` 离线重放才拿下全档位最优净省 **`-11,703 tok`（`-15.23%`，`72F/61D`）**，`2dd44960` 省 **`-10,964 tok`（`-8.71%`，`123F/43D`）**，而 `85%` 在 `3e7a393a` 再次倒退（`-11,494 tok`）。
4. **病灶四·现象 A：静止页面图标容器与透明包装层导致 `ref` 断号（影响 62.1% 差分）**：
   - 全场 `58` 份 delta 中 `36` 份（`62.1%`）出现完全相同角色/标签/动作/坐标的节点原地 `-` 删 `+` 增换号，`12` 份（`20.7%`）属于画面零变化的纯假抖动。典型如网易云“我的”页 `O168`/`O169`/`O173`–`O175` 顶栏 `4` 个静止图标每帧全删重建（`changes=9`）、AI 助手页 `O145`–`O149` 几乎静止页稳定吐出 `changes=30~37`（`350+` tok，含顶栏 `2` 图标与 `5` 行歌曲的 `"VIP"` 文本及行尾 `n5807` 壳内 `2` 图标共 `22–25` 个节点）。
   - 根因：`match.go` 运行在未剪枝的 `Snapshot.Tree` 上：图标按钮在内部树常表现为 `ViewGroup(clickable) -> ImageView(无字装饰)`，被 Stage 6 `len(children) == 0` 拒绝；而歌曲行内的 `"VIP"` 与行尾按钮组则被未锚定的透明包装壳与已锚定父节点隔开，被 Stage 4 直接子级扫描错过。
5. **病灶四·现象 B：基线池 `backup_span=4` 引发“主降副瞬杀”与多级返回顶槽**：
   - `capture.go` 已具备遍历基线池全部有效 `full`、调用 `CandidateAgainst` 重算保号并择最短 delta 的能力；真正瓶颈在于参数 `(主=10, 副=4, 容量=4)` 过紧。当页面 A 连续使用当前基线 `>=5` 步后进入新页 B 并产生新 `full`，原主基线降级为副基线，上限瞬间从 `10` 跌为 `4` 并当场淘汰；同时 `3` 个备用槽在 2–3 级嵌套返回时把较早首页挤出。
   - 双会话分别有 `14` 帧（原整树 `12,899` tok）与 `30` 帧（原整树 `35,976` tok）属 `180s` 内文本 Jaccard `>= 0.70` 的高相似重访整树。池参数调至档位 ① `(12, 10, 5)` 后，模拟器在 `3e7a393a` 预计多回收 `+8` 帧（原整树 `6,437` tok），在 `2dd44960` 多回收 `+5` 帧（原整树 `5,786` tok），平均每帧探测仅从 `1.85/2.29` 次温和升至 `3.83/4.31` 次。

### 1.2 证据链

| 既有链路与行为 | 源码与测试位置（基线 `e4f4d6d`） |
| --- | --- |
| `cleanReadableText` 仅在含 `readableInvisibleRunes`（6 个码点）时才执行 `TrimSpace`，普通首尾空格与纯 `\u00a0`/`\n` 原样返回；`labelParts` 拼接文本与描述，`hasSemantics` 判读非空标签；`RoleInput.Value` 直取 `raw.Text` | [`cleanReadableText`](../../internal/observation/build.go#L438-L450)、[`labelParts`](../../internal/observation/build.go#L396-L416)、[`hasSemantics`](../../internal/observation/build.go#L314-L333)、[`newPublicNode`](../../internal/observation/build.go#L483-L490) |
| `ProjectCompactTree` 下行决定几何与祖先标签栈、上行固定顺序归约；`projectCompactNode` 返回单节点指针且仅在 `len(out.Children) == 1` 时绕过匿名容器；`compactHoistsTextChildren` 允许无字 `tap` `view` 携带 `Resource` 并在提升为 `button` 后置 `Resource = nil`；`equalReadNode` 逐字段判等 | [`ProjectCompactTree`](../../internal/observation/read.go#L68-L77)、[`projectCompactNode`](../../internal/observation/read.go#L171-L246)、[`AnonymousPlain`](../../internal/observation/read.go#L20-L50)、[`compactHoistsTextChildren`](../../internal/observation/read.go#L325-L341)、[`equalReadNode`](../../internal/observation/read.go#L726-L759) |
| 现有单测固定了“普通空白保留”与“多子匿名容器不拍平”行为，实施本提案时须同步切换预期（`TestGeometryStatesAreDistinct` 与 `TestAnonymousPlainPredicate` 中的原始无子 `AnonymousPlain` 叶仍应保持有效） | [`TestCleanReadableText`](../../internal/observation/build_purification_test.go#L17-L18)、[`retention_and_filter_boundaries`](../../internal/observation/purification_test.go#L177-L180)、[`never hoists...`](../../internal/observation/read_purification_test.go#L131-L159)、[`two interaction branches...`](../../internal/observation/compact_pruning_test.go#L286-L300)、[`TestCompactGeometryDuplicateRules`](../../internal/observation/read_bounds_test.go#L146-L168)、[`TestGeometryStatesAreDistinct`](../../internal/observation/read_test.go#L253-L283) |
| `ReadDiff`/`ReadApply` 通过 `sameReadOwn`/`diffReadFields`/`applyReadSet` 比较与重建树，同 `ref` 角色改变直接返回 `ErrNotRepresentable` 由引擎回退 `full`；`SetTouchesInteraction` 禁止 `delta` 修改根交互字段 | [`ReadDiff`](../../internal/observation/read_diff.go#L13-L47)、[`readApplyAdapter`](../../internal/observation/read_diff.go#L133-L142)、[`diffReadFields`/`applyReadSet`](../../internal/observation/read_diff.go#L152-L239)、[`planStructuralDelta`](../../internal/observation/diff.go#L117-L119)、[`applyStructuralDelta`](../../internal/observation/apply.go#L233) |
| `match.go` 在未剪枝 `Snapshot.Tree` 上建立 `publicMatchIndex`；Stage 4 直接子级 pass 与 `localStructure`（故意保留已匹配兄弟计入总基数）受限于直接子级/单叶约束；Stage 6 `prevStaticLeaf`/`curStaticLeaf` 硬要求 `len(children) == 0`，且 `staticLeafBucket` 未区分真叶与图标容器；`visual.go` 已定义 `visualImageLeaf`/`visualKnownDefaultState` | [`buildPublicMatchIndex`](../../internal/observation/match.go#L305-L439)、[`prevStaticLeaf`/`curStaticLeaf`](../../internal/observation/match.go#L522-L549)、[`staticLeafBucket`](../../internal/observation/match.go#L951-L959)、[`matchParentScopedStructurallyUniqueContainers`](../../internal/observation/match.go#L1306-L1390)、[`matchLocalUnderAnchoredParents`](../../internal/observation/match.go#L1833-L1904)、[`local_structure_remaining_one_rejected`](../../internal/observation/continuity_pipeline_test.go#L542-L555)、[`matchStaticGeometrySiblings`](../../internal/observation/match.go#L2063-L2264)、[`visualImageLeaf`](../../internal/observation/visual.go#L222-L259) |
| `ReadNode` 与 `ReadSetNode` 尚无 `truncated` 字段；需要注意 `Field[T].UnmarshalJSONFrom` 对非指针 `bool` 遇到 JSON `null` 时会反序列化为零值 `false` 而不会报错，因此 `ReadSetNode.Truncated` 需保持 `Field[bool]` 类型并在 `ReadSetNode.UnmarshalJSONFrom` 中显式拒收 JSON `null` | [`ReadNode`](../../internal/protocol/read_tree.go#L150-L529)、[`ReadTree.Validate`](../../internal/protocol/read_tree.go#L618-L673)、[`ReadSetNode`](../../internal/protocol/read_tree.go#L677-L697)、[`Field[T]`](../../internal/protocol/node.go#L553-L576)、[`isJSONNull`](../../internal/protocol/decode_response.go#L123) |
| `treetext` 负责 `tree v5` 编码、`ReadDelta` 门槛前 UTF-8 字节计量及节点详细信息校验；`internal/mcp/tree_text.go` 负责 `inspect v3` 文本渲染与约束检验（目前耦合完整节点列序与 `detailedRectanglesOnly`） | [`writeSetNode`](../../internal/treetext/encode.go#L159-L261)、[`writeNodeColumns`](../../internal/treetext/encode.go#L414-L462)、[`validReadNodeTreeDetails`](../../internal/treetext/encode.go#L695-L754)、[`validSetNode`](../../internal/treetext/encode.go#L767-L782)、[`isAnonymousDefaultViewChain`](../../internal/mcp/tree_text.go#L238-L254)、[`detailedRectanglesOnly`](../../internal/mcp/tree_text.go#L535-L554) |
| `engine` 基线池常量当前为 `10 / 4 / 4 / 7 / 10`；`publishTreeAttempt` 对全池逐个调 `CandidateAgainst` 与 `treeTextBaselineDelta`（`tree v5` 注释仍写 `tree v4`）；`baseline_pool_test.go`、`tree_budget_test.go` 与 `commit_test.go` 锁定旧跨度 4/10、容量 4、70% 边界及 `%3` 步长夹具；`assertReadContentConsistent` 假设 `compact`/`detailed` 同 `ref` 标签一致（除 `input label==value`） | [`baseline pool consts`](../../internal/engine/capture.go#L272-L280)、[`publishTreeAttempt`](../../internal/engine/capture.go#L332-L363)、[`baselineEligibleLocked`/`truncateBaselinesLocked`/`treeTextBaselineDelta`](../../internal/engine/capture.go#L663-L763)、[`confirmBaselineLocked`](../../internal/engine/capture.go#L1357-L1404)、[`pageReturn`](../../internal/engine/baseline_pool_test.go#L129-L203)、[`spanWindow`](../../internal/engine/baseline_pool_test.go#L359-L386)、[`split/post/cap`](../../internal/engine/baseline_pool_test.go#L606-L708)、[`commit_test.go`](../../internal/engine/commit_test.go#L304-L345)、[`tree_budget_test.go`](../../internal/engine/tree_budget_test.go#L630-L713)、[`assertReadContentConsistent`/`compactLabelsMatch`](../../internal/engine/observation_continuity_integration_test.go#L338-L446) |
| `mcp` 全量反射清单门禁（只豁免 `protocol.Field[` 等 wrapper 且精确比对 `rmfCompositeInventory`）、发现说明 `treeTextGuide`（含 `len>=2 保持层级`）、字节上限 `baselineDiscoveryBytes = 37500`、包内 `tree v5` 严格测试解析器及 `mcp-probe` 独立列序/一致性校验 | [`rmfCompositeInventory`/`scanValue`](../../internal/mcp/result_manifest_test.go#L97-L120)、[`TestResultFieldManifest`](../../internal/mcp/result_manifest_test.go#L669-L761)、[`parseNodeTailFields`](../../internal/mcp/tree_text_test.go#L2726-L2858)、[`parseV5SetLineValue`](../../internal/mcp/tree_text_test.go#L3051-L3212)、[`treeTextGuide`](../../internal/mcp/schema.go#L78-L86)、[`schema_test.go`](../../internal/mcp/schema_test.go#L29-L95)、[`validateTreeNodeTokens`/`Columns`](../../scripts/mcp-probe/result_text.go#L734-L956)、[`validateFullNodeFlags`/`validateSetNodeFlags`](../../scripts/mcp-probe/result_text.go#L1143-L1189)、[`checkTreeBodyAgainstStructured`](../../scripts/mcp-probe/tree_body_consistency.go#L82-L193) |

### 1.3 为什么直觉会出错

1. **为什么不能在未剪枝树上先调高门槛、或继续把 80% 拉高到 85%？**
   - `tree v5 delta` 单字节携带的标点与前缀 token 密度比 `full` 高 `+20.2%`（`0.4161` vs `0.3461 tok/B`），导致字节比在 `71%–85%` 的差分在 token 上往往逼近甚至超过 `100%`；同时把本该作为新基线入池的帧强行发成 `delta` 会造成**基线池饥饿级联**，迫使后续帧挂在更老基线上发巨型差分。只有先用 `M1 + M2a` 压缩 `full` 体量与结构噪音、再把字节门槛止步于 **`80%`**，才能在双会话同时取得净收益而不倒退。
2. **为什么截断不能放进 Stage 1、不能无差别截断、不能后置于 `M2a`、也不能往 `label` 尾拼 `"..."` 或总长度？**
   - 放在 Stage 1 会破坏 `wait_for(query)`、`inspect(query)` 全文匹配与 `Snapshot` 完整性；
   - 无差别截断（如 `T150`）会误伤闲鱼 `338–480` 字商品正文与知乎被动回答正文，迫使模型多花一次 `inspect` 往返；
   - 若后置于 `M2a`，则原本被动文本叶的 `350` 字内容在 `M2a` 提升成 `button` 后才突然触发截断，且多段子标签组合时序耦合；前置在 `ProjectCompactTree` 下行阶段先截断原自带动作节点再将截断结果压入 `ancestors`，既护住被动内容被提升后不被误截，也保证 `A1a` 不会因未展示文本而误删子节点；
   - 往 `label` 末尾拼接 `"..."` 或 `"(total 4009)"` 一方面无法让读者区分设备原文是否真以 `"..."` 结尾，另一方面一旦超长正文末尾有微调（如阅读数/尾部时间戳变动）会直接导致整条 `~ label=[...]` 差分抖动；独立 `truncated` 布尔标记零歧义且对 `250` 码点之外的变化保持零差分。
3. **为什么 `match.go` 现象 A 不能直接允许所有多子容器进 Stage 6 或无限制穿透全部多子匿名容器？**
   - 列表向上滑动并回收视图复用槽位时，坐标 `[l,t,r,b]` 原地重合但内容已换成新卡片：假设原树为 `scroll -> [Card1(icon1), Card2(icon2)]`，新帧为 `scroll -> [Card3(icon3), Card4(icon4)]`，若放开多子业务容器进 Stage 6 或允许穿透含文本的多子匿名层，`Card3`/`icon3` 会按旧槽位坐标偷走 `Card1`/`icon1` 的 `ref`，导致模型按老 `ref` 点中完全不同的条目，破坏保号“宁可新号、绝不串号”的底线。因此图标容器进 Stage 6 必须严格限定为**子节点全部为纯装饰图子树**（零文本、零交互、零非默认状态），多子穿透也必须限定于**所有非装饰子节点递归均为无字可交互静态图标**的透明外壳。
4. **为什么不采纳 `S_plain`、`partial_hoist`、`B3/B4`、`M2b`、`M4b`、`M5a`、Base36 `ref` 或按最后使用步无限续命？**
   - 双会话反事实测试已实证：`S_plain` 在 `M1+M2a` 后增量仅 ~2K–2.5K tok 却造成首节点出屏后 `ref` 漂移、播放时间条粘连重发、正文与广告粘连及 `2dd44960` 4 帧翻 `full`；`partial_hoist` 命中全屏 `playerControlContainer`、25 子节点评论区与 `KTKTKT` 脱钩，且高度依赖脆弱启发式护栏；`B3/B4` 省微末 token 易误伤；`M2b` 仅 +213 tok 却误杀 `O65 n20` 子 ref；`M4b` 让 `315` 个无字图标失明；Base36 反增 `+547` tok；`M5a` 丧失唯一按钮的空间布局感；按“最后使用步”续命会打破 LLM 回溯距离硬上限，而档位 ① `(12, 10, 5)` 既消除瞬杀又将回溯锁死在 12 步内。

---

## 2. 目标与非目标

### 目标

1. **标签空白清洗（B1 + B2）**：Stage 1 `cleanReadableText` 统一修剪公开可读标签首尾空白（含 `\u00a0` 等所有 `unicode.IsSpace`），将全空白标签归空并使纯空白/NBSP 无动作无状态叶不再入树，同时保持内部非占位换行原样及 `device.Capture` / `RoleInput.Value` 原值不动。
2. **有动作非输入长标签截断（T250 + `truncated`）**：Stage 2 `ProjectCompactTree` 在下行阶段早于 `ancestors` 压栈与结构拍平，对 `len(Actions) > 0 && Role != RoleInput && !Context` 节点的 `Label` 逐段按 `250` Unicode 码点上限前置截断，并在 `ReadNode`、`ReadSetNode`、`tree v5` 与 MCP 独立校验器新增独立非可空布尔字段 `truncated`；所有无动作节点、`input` 节点与 `inspect v3` 详细视图保持 100% 原文且禁止 `truncated=true`。
3. **多子匿名纯容器扁平化与后置提升（M1 + M2a）**：`ProjectCompactTree` 上行归约将所有 `GeometryHidden && AnonymousPlain && len(Children) >= 1` 的容器按序透传拍平（保留原始无子 `AnonymousPlain` 合成叶测试语义），自然释放原本被多层匿名壳阻断的 `compactHoistsTextChildren` 按钮提升（含带 `resource` 的无字 `tap` `view`），合并为单行 `button` 并清除冗余 `resource`。
4. **静止页面保号修复（病灶四·现象 A）**：在 `internal/observation/match.go` 中将仅包含纯装饰图子树（紧缩视图必裁）的静止交互图标容器纳入 Stage 6 等价静态叶（按 `decorativeContainer` 与真叶分桶隔离），并在 Stage 4 局部恢复与 Stage 6 同锚定父下安全穿透透明包装层（Stage 4 仅限单枝透明穿透走内容/身份恢复；Stage 6 单/多枝透明穿透统一仅限全无字静态图标/图标容器），消除静止页与图标组原地换号，同时以专门对抗测试证明列表回收场景 0 误配。
5. **基线池与差分门槛调优（病灶二 + 病灶四·现象 B）**：保留 `internal/engine/capture.go` 现有 `1 主 + N 副`、按创建步算跨度、`180s` 年龄、`32 MiB` 压力淘汰与全池 `CandidateAgainst` 架构，同步落地 `M1+M2a` 后将常量调为 **`currentBaselineRevisionSpan = 12`**、**`backupBaselineRevisionSpan = 10`**、**`maxBaselines = 5`**（`1 主 + 4 副`）、**`baselineDeltaNumerator = 8` / `baselineDeltaDenominator = 10`**（严格 `< 80%`）。
6. **契约、说明与整组交付同步**：推进共同 `contract_version`（`28 → 29`，`release_version` `0.32.0 → 0.33.0`，`android_version_code` `34 → 35`）、`release/versions.json` 及生成常量，同步更新 `internal/mcp/schema.go`、`scripts/mcp-probe`、`README.md` 与 `docs/`，完成测试与发布验证。

### 非目标

- **明确不纳入研究报告 §8.2 的放弃/暂缓项及正文（§1/§5.1）暂缓项**：`S_plain`（连续纯文本兄弟合并）、`partial_hoist`（混排半提升）、`B3`（WebView `view` 覆盖裁剪）、`B4`（`text`/`desc` 子串去重）、`85%` 门限、扩池至 20+ 步或改按最后使用步续命、`M2b`（合并双层可点击壳）、`M4b`（全删 `resource`）、`M5a`（唯一按钮藏坐标）、Base36 `ref`、去 `~ bounds` 方括号、无差别 `T150` 截断，以及研究报告 §1/§5.1 暂缓的极简 `result v4` 外壳。
- **不包含 DSH 宿主侧历史树/图片滑动窗口折叠与 `pwsh` 文档读取策略**（属于宿主上下文管理职责，不进入本仓库服务端/MCP 协议）。
- **不包含研究报告 §6 的三项调用体验建议**（`app` 根描述 `kind` 约束对照表、遮罩感知滚动候选排序、`app.kill` 分栏提示）：它们未进入研究报告 §8.1 最终工程共识，已确认排除出本提案、不混入本次契约变更，保持独立后续评估。
- **不修改** `device.Capture` 原始节点、`Snapshot.Tree`、`act` 静态选点与重验、`wait_for` 采样比较与字面查询、`inspect`（`inspect v3`、`view=detailed`）完整节点/全文/四元矩形输出，以及 `result v4` 块头号。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持现行契约 `28`（`<70%` / `10,4,4` / 不拍平多子容器 / 不截断 / Stage 6 仅真叶） | 零变更成本 | `3e7a393a` 级长会话继续多耗 ~15.2% 可重放树 token、静止含图标页 62% 差分出现假换号、主降副瞬杀导致重访回退 6.4K tok 级整树、长文 `contentDescription` 按钮单帧爆至 3.4K+ tok |
| R2 激进压榨路线 | 在 R4 基础上追加 `S_plain`、`partial_hoist`、`T150`、`85%` 门限与 `20` 步池 | 多套启发式护栏、跨版本测试维护成本高 | `S_plain` 引发首节点 `ref` 漂移与正文粘广告、`partial_hoist` 误吸全屏控制条、`T150` 误截商品正文、`85%` 因密度剪刀差反赔 token、长池拉长客户端内存与回溯跨度 |
| R3 不升级 `ReadNode`、`label` 尾拼 `"..."` 且不做 `match.go` 静止保号 | 只改 `M1+M2a`、`80%`、池 `(12,10,5)`，截断直接改写 `label` 末字符，不动 `match.go` | 表面不新增 `ReadNode`/`ReadSetNode` 字段 | 无法区分设备原文自带 `"..."` 与服务截断，长文尾部变动在差分中引起 `~ label` 抖动，且 `62%` 差分的静止图标假换号继续浪费 `delta` 体积并干扰调用方连续操作 |
| **R4 分层无启发式共识路线（选定）** | 按研究报告 §8.1 定案同步落地 `B1+B2`、`M1+M2a`、前置有动作 `T250`（独立 `truncated` 布尔）、`match.go` 图标容器进 Stage 6 + 安全透明包装层穿透、`(12,10,5)` 池与 `80%` 门限；升共同契约至 `29` | 需修改 `ReadNode`/`ReadSetNode` 与 `tree v5` 节点行/set 行语法，更新独立探针、文档并整组构建部署 | 若图标容器或透明穿透边界守卫不足，可能引发列表回收误配；若 `Field[bool]` 未显式拒 `null` 会破坏严格接入；两项均通过本提案 §4.2/§4.4 确定性判据与 §8.1 对抗单测闭环兜底 |

**选择理由**：R4 完整兑现研究报告 §8 与用户达成的六项工程共识——每项改动均有双会话离线重放或实机案例证据支撑、无脆弱启发式规则、相互之间存在明确的协同前提（`M1` 解锁 `M2a`，`M1+M2a` 解锁 `80%` 最优拐点，`T250` 前置避免与 `M2a` 交叉，现象 A 与现象 B 同步治好差分的伪抖动与重访回退）。

---

## 4. 判据

### 4.1 Stage 1 公开可读标签空白净化判据（B1 + B2）

1. **`cleanReadableText(s string) string` 新规则**：
   - 若 `strings.ContainsAny(s, readableInvisibleRunes)`（`U+200B`、`U+200C`、`U+200D`、`U+2060`、`U+FEFF`、`U+FFFC`）：先剔除全部上述 6 个闭集码点，再用 `readableBlankLineRun`（`\n(?:[ \t]*\n)+`）折叠连续空行残余为单个 `\n`；
   - 对上述结果（以及**不含**闭集隐形码点的原始字符串 `s`）**无条件**执行 `strings.TrimSpace`（覆盖 ASCII 空白、`\u00a0` 不间断空格、`\u0085`、`\u3000` 等所有 `unicode.IsSpace` 码点）。
   - **保真边界**：不含闭集码点的字符串中间部分（如 `"a\n\nb"`、`"\u00a0"` 中间的 `"a\u00a0b"`）保持原样；仅纯空白/NBSP/换行字符串修剪后返回 `""`。
2. **作用与豁免边界**：
   - 仅作用于 `build.go` 的 `labelParts(raw)`（先清洗 `raw.Text` 与 `raw.Description`，非空才采纳且两者清洗后不等才追加第二段）及 `match.go` 中输入框描述锚点 `cleanReadableText(raw.Description)`；
   - `hasSemantics(raw)` 因 `len(labelParts(raw)) == 0` 自动使纯空白/NBSP 无动作无状态默认叶不进入公开树、不分配 `ref`；
   - **`device.Capture.Nodes` 原始字段与 `RoleInput` 的 `node.Value` 100% 不走 `cleanReadableText`**，与 `act` 重验及 `text` 写入精确读回完全正交。

### 4.2 Stage 2 有动作非输入长标签前置截断判据（T250 + `truncated`）

1. **触发与截断规则（`internal/observation/read.go`）**：
   - 常量：`compactActionableLabelMaxRunes = 250`。
   - 触发谓词（在 `projectCompactNodes` 下行阶段、G5 细条与 G3 `input` 去重之后、`childAncestors` 压栈与递归子节点之前执行）：
     `!out.Context && out.Role != protocol.RoleInput && len(out.Actions) > 0 && len(out.Label) > 0`。
   - 对 `out.Label` 的每一个元素 `part` 逐段判定：
     - 若 `utf8.RuneCountInString(part) <= 250`：原样保留该段；
     - 若 `utf8.RuneCountInString(part) > 250`：按 UTF-8 码点遍历精确截取前 `250` 个 `rune` 对应的字节前缀（若存在非法 UTF-8 字节，按 `utf8.DecodeRuneInString` 计步，每个有效/无效序列计 1 步，绝不把一个多字节字符截成半截乱码），**不追加** `"..."`/`"…"`、**不附加**总字数、**不对**截断后第 `250` 个码点处的原有空格做二次修剪，并将 `out.Truncated = true`。
   - 被动节点（`len(Actions) == 0`，如知乎回答、商品描述、聊天正文）与所有 `RoleInput` 节点永不触发截断，`out.Truncated` 恒为 `false`。
   - 截断后的 `out.Label` 随即参与 `childAncestors` 压栈：子被动文本叶仅在被祖先截断后**实际交付出来的前 250 码点标签**覆盖时才受 `A1a` 裁剪，防止因为长标题被截断而误删原本只有后半段能看到的子文本。
2. **协议模型、严格 JSON 编解码与视图不变式（`internal/protocol/read_tree.go`）**：
   - `ReadNode` 新增 `Truncated bool`：
     - `MarshalJSONTo`：仅在 `n.Truncated == true` 时写出 `"truncated":true`（字段顺序位于 `resource` 之后、`context` 之前）；`false` 省略；
     - `UnmarshalJSONFrom`：`readNodeWire` 使用 `Truncated jsontext.Value \`json:"truncated"\``，缺席为 `false`，显式 `null` 或非布尔值返回解码错误；
     - `Clone` 与 `equalReadNode` 完整复制并逐项比对 `Truncated`。
   - `ReadTree.Validate()` 与 `inspect` 一致性：
     - `t.View == ViewDetailed`（`inspect`）：禁止任何节点 `n.Truncated == true`（返回明确错误）；
     - `t.View == ViewCompact`：禁止 `n.Role == RoleRoot || n.Context` 节点 `n.Truncated == true`；
     - `internal/mcp/tree_text.go` 的 `detailedRectanglesOnly` 同步拒绝 `node.Truncated == true`；`isAnonymousDefaultViewChain` 同步要求 `!node.Truncated`；`compactNoReadableContent` 补充 `if n.Truncated { return false }` 守卫。
   - `ReadSetNode` 新增 `Truncated Field[bool]`（`json:"truncated,omitzero"`，位于 `Resource` 之后；固定沿用 `protocol.Field[bool]` 以匹配 `internal/mcp/result_manifest_test.go` 的 `Field[` 反射豁免，不另建自定义泛型外层）：
     - `IsZero()` 包含 `!s.Truncated.Set`；
     - **显式拒绝 JSON `null`（满足硬约束 6、7）**：在 `ReadSetNode.UnmarshalJSONFrom` 中使用内部 wire 结构体（以 `Truncated jsontext.Value \`json:"truncated"\``）配合 `json.RejectUnknownMembers(true)` 与 `isJSONNull`（`internal/protocol/decode_response.go#L123`）先行拒收 `"truncated":null`，再精确解码为 `bool`，确保 `"truncated":null`、字符串 `"true"`、重复键或未知字段一律被拒绝，同时完整保留显式 `"truncated":false` 与 `"truncated":true`；
     - `diffReadFields` 在 `prev.Truncated != cur.Truncated` 时置 `change.Truncated = Field[bool]{Set: true, Value: cur.Truncated}`；`applyReadSet` 在 `change.Truncated.Set` 时赋 `node.Truncated = change.Truncated.Value`；`SetTouchesInteraction` 新增 `|| change.Truncated.Set`；
     - `ReadDelta.Validate()`（针对 `Add` / `Replace` 子树对齐 `d.View == ViewDetailed` 与 `Context || RoleRoot` 约束）、`ReadTree.Validate()` 与 `treetext.validReadNodeTreeDetails`（`internal/treetext/encode.go#L695-L754`）同步对齐 `Truncated` 禁用校验。
3. **`tree v5` 文本编码与探针语法（`internal/treetext/encode.go`、`scripts/mcp-probe/result_text.go`）**：
   - 保持块头 `tree v5 full` / `tree v5 delta` 不变（沿用契约 `28` 为 `result v4` 增补 `visual=` 而不升块头号的先例，依靠共同 `contract_version = 29` 与发布清单整组同步）；
   - 完整节点行固定列序（`writeNodeColumns`）：在 `resource="…"` 之后、`context` 之前，仅当 `node.Truncated == true` 时输出 `truncated=true`（`false` 省略）；`chain` 定义同步遵守同列序；
   - `~` set 行固定列序（`writeSetNode`）：在 `resource=` 之后，当 `set.Truncated.Set` 时输出 `truncated=true` 或 `truncated=false`（显式保留 `false`）；
   - `mcp-probe` 校验器同步更新：
     - `validateFullNodeFlags` 扩展接收 `rules.detailed` 参数，当 `rules.detailed == true` 时禁止 `truncated=`，并对 `truncated=false` 返回 `flag_default`、对 `truncated=null` 或非法值返回 `flag_invalid`；
     - `validateTreeNodeColumns` 严禁将 `"truncated"` 放进 `treeFullFlagOrder`（该表在 `axis=`/`resource=` 之前扫描），改为在 `resource=` 之后、`context` 之前单独消费 `!rules.detailed && tokens[index] == "truncated=true"`，精确锁死列序与单次出现；
     - `~` set 行允许 `truncated=true|false` 位于 `resource=` 之后，`validateSetNodeFlags` 拒绝 `truncated=null` 或其他值；
     - `checkTreeBodyAgainstStructured` 复用 `treetext.EncodeFull`/`EncodeDelta`，天然验证结构化 JSON 与文本块对 `truncated` 的逐字节一致。

### 4.3 Stage 2 多分支隐藏匿名容器扁平化与后置文本提升判据（M1 + M2a）

1. **函数签名与返回**：将 `projectCompactNode` 演进为 `projectCompactNodes(...) []*protocol.ReadNode`；`ProjectCompactTree` 取根返回首项（根永不拍平）。
2. **固定上行归约顺序**（对单个原始子节点投影所得切片按序 `append(out.Children, projected...)` 之后执行）：
   1. **Step 1（A3c）**：`!originalHasChildren && compactImageLeafCanPrune(out)` → 返回 `nil`；
   2. **Step 2（A1a）**：`!originalHasChildren && isPlainPassiveTextLeaf(out) && compactLabelCovered(out.Label, ancestors)` → 返回 `nil`；
   3. **Step 3（M1 空容器清理）**：`originalHasChildren && len(out.Children) == 0 && out.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(out)` → 返回 `nil`；
   4. **Step 4（M1 匿名容器拍平，覆盖单子与多子）**：`out.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(out) && len(out.Children) >= 1` → 直接返回 `out.Children`（将 1 个或多个孩子按原顺序透传给父节点，自身退场）。
      - **边界守卫**：对于 `!originalHasChildren && len(out.Children) == 0` 的原始无子 `AnonymousPlain` 节点（仅可能在手工/单测构造的合成 `protocol.Tree` 中出现），保留原行为返回 `[]*protocol.ReadNode{out}`，确保 `TestGeometryStatesAreDistinct` 与 `TestAnonymousPlainPredicate` 语义不破坏；真实 `Builder.Build` 不会产出此类无语义空叶；
   5. **Step 5（M2a 文本提升）**：`compactHoistsTextChildren(out)`（源码已位于拍平之后且允许带 `Resource`）无需改变自身谓词，自然捕获此前被多子匿名壳隔开的 `347` 处（`317` 无 `resource` + `30` 带 `resource`）无字可点击 `view`，提升为 `RoleButton`、按子顺序保留原生多段 `[]string` `Label`、清空 `Children` 并置 `Resource = nil`；
   6. **Step 6（A5a）**：剩余带非空 `Resource` 节点执行 `compactResourceTail`。
3. **几何与 `ancestors` 传递正确性**：`AnonymousPlain(out)` 节点必定无动作（`len(Actions) == 0`）、无文本（`len(Label) == 0`）且满足 `compactHidesBounds == true`（`GeometryHidden`），下行从未修改 `reference` 与 `childAncestors`，因此向父层拍平后所有子孙的几何（`Point`/`Rect`/`@`）与 `A1a` 覆盖判定严格不变。

### 4.4 `match.go` 静止页面保号修复判据（病灶四·现象 A）

为彻底消除网易云“我的”页 `O168`/`O169` 顶栏图标与 AI 助手页 `O145`/`O146`/`O128` 等静止页面的原地删增换号，同时严守保守保号边界（绝不在列表回收或同坐标换卡时错绑 `ref`），对 [match.go](../../internal/observation/match.go) 做以下三项对称、可预计算的确定性增强（`maxTransparentWrapperDepth = 8`）：

1. **在 `buildPublicMatchIndex` 中对称预计算 O(N) 辅助标记**（同时覆盖 `prev` 公开树和 `cur` 已发布单元，`prev`/`cur` 判定条件严格等价）：
   - `isDecorativeImageLeaf`：非 `context`、`Role == RoleImage`、无已发布子节点、`len(label) == 0`、`actions == 0`、**公开资源为空（`!pk.hasRes`，即已发布 `Resource == nil`）**、`!editable`、`!protected`、`value == nil && hint == nil && axis == ""` 且五布尔全部已知默认（`enabled == true`、`focused/selected/protected/checked == false`）。说明：`prev` 沿用 `visualImageLeaf`（`internal/observation/visual.go#L222`，检查公开 `Resource == nil`），`cur` 统一使用对应的已发布投影字段（`units[cur]` 公开签名 `!pk.hasRes` 及 `cap.Nodes[cur]` 等价发布投影属性），严禁在 `cur` 侧错查原始 `raw.ResourceID == ""`，避免带 `android:id` 的 `<ImageView>` 装饰图图标在两侧出现语义撕裂而失配；
   - `isAnonymousPlain`：非根、非 `context`、`Role ∈ {RoleView, RoleGroup}`、无 `label`、无 `actions`、无 `resource`（仅此容器谓词维持原始 `!k.hasRes` 即 `ResourceID == ""` 与公开 `!pk.hasRes` 都为空，避免吞掉带业务资源 ID 的结构容器）、`value == nil && hint == nil && axis == ""` 且五布尔已知默认。
   - `isPureDecorativeSubtree`（自底向上计算）：
     - 等于 `isDecorativeImageLeaf`；**或** `isAnonymousPlain` 且至少有 `1` 个子节点且**所有**子节点均为 `isPureDecorativeSubtree`。
   - `isTransparentSingleWrapper`：
     - `isAnonymousPlain` 且跳过所有 `isPureDecorativeSubtree` 子节点后，**恰有 1 个**非装饰子节点（记录其唯一非装饰子节点索引 `singleChild`，供 Stage 4 O(1) 穿透）。
   - `isStaticIconContainer`（Stage 6 图标容器等价静态叶判定）：
     - 非根、非 `context`、非 `editable`、非 `RoleInput`、具有正面积可见 `bounds`、`len(children) > 0`、`actions != 0` 且**所有**已发布子节点均满足 `isPureDecorativeSubtree`。
     - **为什么安全**：`compact` 投影会裁掉所有 `isPureDecorativeSubtree`（`A3c` + 空匿名壳清理），因此此类节点在紧缩树中本身就是无文本的交互图标叶子；严禁任何含文本叶、含 `input`、含非默认状态节点或含子交互控件的业务容器进入此类别。
   - `isTransparentWrapperForStage6`（自底向上计算，同时涵盖安全单枝与安全多子图标组包装层）：
     - 统一严格定义为：`isAnonymousPlain` 且非装饰子节点数 `len(nonDec) >= 1` 且**每一个**非装饰子节点均递归满足 `isStaticIconGroupMember`（即非根、非 `context`、非 `editable`、非 `RoleInput`、正面积可见 `bounds`、`actions != 0 && label == ""` 且 `len(children) == 0 || isStaticIconContainer`）或自身也是深度受限内的 `isTransparentWrapperForStage6`。
     - **严禁**：取消任意 `isTransparentSingleWrapper` 直接并入 Stage 6——无论是单子还是多子匿名容器，只要下面包含**任何**有文本节点（如列表卡片内带单层透明壳的 `"VIP"`）、被动文本叶、输入框或一般多子业务卡片容器，绝不视为 Stage 6 透明包装层，防止列表滚动槽位复用发生跨卡偷号（行内带文透明单枝仅交由 Stage 4 局部内容/身份 pass 恢复）。
2. **Stage 4 透明单枝包装层穿透局部恢复**（在 `matchLocalUnderAnchoredParents` 现有直接子级局部恢复之后、同一 `scope` 循环内执行；原有全量 direct `localStructure` 即 `matchParentScopedStructurallyUniqueContainers` 保持原地不动，绝不过滤 `isUsed`）：
   - 对当前 `scope` 内的每个已锚定父 `(anchor, curParent)`，分别收集 `anchor` 下与 `curParent` 下的**局部前沿候选**：
     - 遍历直接子节点：若节点已匹配（`baseR.oldUsed(oldPos)` / `baseR.curUsed(cur)`），停止不向下展开（确保已锚定包装层自己管自己的直接孩子，各父域候选严格隔离）；
     - 若未匹配且满足 `isTransparentSingleWrapper`，沿 `singleChild` 向下穿透至多 `maxTransparentWrapperDepth = 8` 层（每步 `budget.charge(1)`），直到遇到非 `isTransparentSingleWrapper` 节点或达到深度上限为止；若沿途节点已匹配则跳过该路（不重复发射已用节点）；
     - 其余未匹配且非 `isPureDecorativeSubtree` 的直接子节点，与穿透到达的前沿节点**共同**纳入同一局部前沿桶（防止因绕过包装层把局部多候选伪造成唯一）；
     - **仅在该混合穿透前沿上运行内容/身份 pass（`localExact`、`localResource`、`localInputAnchor`、`localPublic`）**，**绝不在过滤了 `isUsed` 的穿透前沿上跑无内容 `localStructure`**（保护 `local_structure_remaining_one_rejected` 全量直接兄弟分母守卫）；所有 pass 显式跳过无锚点输入框（`k.editable && !k.hasRes && k.anchor == ""`）并经过 `appendSingletonOffers` + `chooseBatchIfBidirectionallyUnique` 原子提交；
     - 成功匹配到下层单枝包裹子节点（如行内带单层透明壳的 `"VIP"`）后加入 `dirty` 队列，由既有 Stage 3A/3B 与 Stage 4 迭代自动把被跨过的中间单子匿名包装层向上锚定，随后再经 Stage 5/post-ordinal 保持完整家族连通性（注：像 `n5807` 这类仅包裹多子无字图标的壳将在末尾 Stage 6 直接配对其两个图标子节点、不回触发 Stage 3A/3B，且其在 `compact` 视图由 `M1` 拍平消失）。
3. **Stage 6 图标容器等价静态叶与安全包装层穿透**（`matchStaticGeometrySiblings`）：
   - `prevStaticLeaf` / `curStaticLeaf` 条件从 `len(children) == 0` 放宽为 `len(children) == 0 || isStaticIconContainer`（仍保留非根、非 `context`、非 `editable`、非 `RoleInput`、正面积 `bounds` 条件）；
   - `bucketKey` 新增 `decorativeContainer bool` 字段并在 `staticLeafBucket` 中按 `isStaticIconContainer` 赋值，保证**纯叶只与纯叶同桶、图标容器只与图标容器同桶**；
   - 在已锚定父节点下收集候选时，建立有界队列/栈按 preorder 展开：直接未匹配子节点中若满足 `isTransparentWrapperForStage6` 则向下穿透至多 `8` 层（每步 `budget.charge(1)`；遇已匹配节点停止不展开，**且在展开包装层内部 `childrenOf(it.node)` 时显式跳过 `isPureDecorativeSubtree` 兄弟装饰节点**，避免紧缩树必裁的无字装饰图进入 Stage 6；已锚定父直属独立 `RoleImage` 叶维持原有 Stage 6 语义以保零回归），搜集满足 `prevStaticLeaf` / `curStaticLeaf` 的节点按 `staticLeafBucket` 归桶；
   - 继续严格沿用 Stage 6 原生全部保护约束：同桶 `k >= 2` 且旧新等数、四边 `±4px` 严格 1:1 双射、旧序对应当前 `tin` 严格递增（不交叉、不倒序）、单次执行不回触发早阶段，并计入 `stage6Matched`。

### 4.5 多基线池窗口与 `tree v5` 80% 差分门槛判据（病灶二 + 病灶四·现象 B）

1. **`internal/engine/capture.go` 常量定值**：
   - `currentBaselineRevisionSpan = 12`（原 `10`）
   - `backupBaselineRevisionSpan = 10`（原 `4`）
   - `maxBaselines = 5`（原 `4`）
   - `maxBackupBaselines = maxBaselines - 1`（自动变为 `4`，原 `3`）
   - `maxBaselineAge = 180 * time.Second`（保持不变）
   - `baselineDeltaNumerator = 8`（原 `7`）
   - `baselineDeltaDenominator = 10`（保持不变）
2. **池与门槛不变式**：
   - 保持 `1 主 + 4 副` 架构、`span = nextRevision - b.Revision`（严格按原 `full` 创建修订算跨度，不按最后使用步无限续命）；
   - 选回备用 `full` 当次仍受 `backupBaselineRevisionSpan`（`span <= 10`）约束，发布成功后成为当前基线并从下一修订起适用 `currentBaselineRevisionSpan`（`span <= 12`）；无当前基线时至多留 `4` 份备用，为下一次确认留出主槽；
   - 差分合格条件调整为 `int64(deltaBytes) * baselineDeltaDenominator < int64(fullBytes) * baselineDeltaNumerator`（精确 `80%` 不合格）；
   - **强依赖约束**：`80%` 门槛必须在 `M1 + M2a` 剪枝生效的前提下同版本发布，不得在未剪枝树上单独开启，也不得继续提高至 `85%`。

---

## 5. 算法

### 5.1 Stage 2 `ProjectCompactTree` 下行截断与上行多子拍平伪代码

```text
const compactActionableLabelMaxRunes = 250

func projectCompactNodes(n *protocol.Node, screen compactProjection,
    ancestors [][]string, reference *protocol.Bounds) []*protocol.ReadNode {
    if n == nil {
        return nil
    }
    originalHasChildren := hasNonNilChild(n)
    out := copyReadFields(n)

    // 1. G5: 露边幽灵细条裁剪（原始无子、role=view、无可读内容/text/swipe、已知默认态、可见短边 <= 4px）
    if !originalHasChildren && compactSliverCanPrune(n, out, screen) {
        return nil
    }
    // 2. G3: input label == value 去重
    if out.Role == protocol.RoleInput && out.Value != nil &&
        len(out.Label) == 1 && out.Label[0] == *out.Value {
        out.Label = []string{}
    }
    // 3. T250: 仅对有动作非输入节点前置截断单段 Label（>250 码点），设独立 truncated 标记
    if !out.Context && out.Role != protocol.RoleInput && len(out.Actions) > 0 && len(out.Label) > 0 {
        out.Label, out.Truncated = truncateActionableLabel(out.Label, compactActionableLabelMaxRunes)
    }

    // 4. 几何判定（沿用原规则：root/scroll 四元，其余二元中心点；AnonymousPlain 与无动作 text 隐藏）
    switch {
    case n.Bounds == nil:
        out.Bounds = protocol.UnknownBounds()
    case n.Role == protocol.RoleRoot || len(n.Actions) != 0:
        out.Bounds = compactDeliveredBounds(n.Role, *n.Bounds, screen)
        reference = deliveredRect(*n.Bounds, screen)
    case compactHidesBounds(n, out, screen, reference):
        out.Bounds = protocol.HiddenBounds()
    default:
        out.Bounds = compactDeliveredBounds(n.Role, *n.Bounds, screen)
        reference = deliveredRect(*n.Bounds, screen)
    }

    // 5. 压入（可能已 T250 截断的）实际交付标签作为子级覆盖判定依据，递归投影并按序展平拼接
    childAncestors := ancestors
    if originalHasChildren && len(out.Label) > 0 {
        childAncestors = appendCompactAncestorLabel(ancestors, out.Label)
    }
    for _, child := range n.Children {
        if child == nil {
            continue
        }
        if projected := projectCompactNodes(child, screen, childAncestors, reference); len(projected) > 0 {
            out.Children = append(out.Children, projected...)
        }
    }

    // 6. 上行归约链（严格顺序）
    // Step 6.1 (A3c): 无标注非交互 image 叶全裁
    if !originalHasChildren && compactImageLeafCanPrune(out) {
        return nil
    }
    // Step 6.2 (A1a): 祖先复合标签覆盖的被动文本叶裁剪
    if !originalHasChildren && isPlainPassiveTextLeaf(out) && compactLabelCovered(out.Label, ancestors) {
        return nil
    }
    // Step 6.3 (M1-empty): 变空隐藏匿名容器清理
    if originalHasChildren && len(out.Children) == 0 &&
        out.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(out) {
        return nil
    }
    // Step 6.4 (M1-flatten): 单子与多子隐藏匿名纯容器统一透传拍平；原始无子合成叶(!originalHasChildren)保留原节点
    if out.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(out) && len(out.Children) >= 1 {
        return out.Children
    }
    // Step 6.5 (M2a): 无字可点击 view 吸收全部纯被动文本子叶升为 button（含带 resource 节点并清除 resource）
    if compactHoistsTextChildren(out) {
        hoistTextChildrenToButton(out)
    }
    // Step 6.6 (A5a): 剩余非空 resource 截取语义短尾名
    if out.Resource != nil && *out.Resource != "" {
        short := compactResourceTail(*out.Resource)
        out.Resource = &short
    }
    return []*protocol.ReadNode{out}
}
```

### 5.2 `match.go` 预计算、Stage 4 透明穿透与 Stage 6 图标容器伪代码

```text
// 1. 预计算（在 buildPublicMatchIndex 对 prev 和 cur 分别倒序 O(N) 计算）
for pos in reverse_preorder(tree):
    decLeaf[pos]     = isDecorativeImageLeaf(pos) // 两侧统一使用公开资源 !pk.hasRes 及发布投影五布尔/value/hint/axis
    anonPlain[pos]   = isAnonymousPlain(pos)      // 非根、非context、view/group、无label/actions、raw&pub均无res、五布尔已知默认
    allChildDec      = len(children[pos]) > 0 && all(c -> pureDecTree[c], children[pos])
    pureDecTree[pos] = decLeaf[pos] || (anonPlain[pos] && allChildDec)

    nonDec := [c for c in children[pos] if !pureDecTree[c]]
    if anonPlain[pos] && len(nonDec) == 1:
        transSingleNext[pos] = nonDec[0] // 供 Stage 4 内容/身份 pass O(1) 穿透；-1 表示非单枝

    iconContainer[pos] = !isRoot(pos) && !isContext(pos) && !editable(pos) && !isRoleInput(pos) &&
                         positiveBounds(bounds[pos]) && actions(pos) != 0 && allChildDec

    staticIconMember = (!isRoot && !isContext && !editable && !isRoleInput &&
                        positiveBounds(bounds) && actions != 0 && label == "" &&
                        (len(children) == 0 || iconContainer[pos]))
    // 收紧为统一谓词（覆盖单枝与多枝）：仅限全部非装饰子孙均为静态图标/图标容器的匿名包装层
    transStage6[pos] = anonPlain[pos] && len(nonDec) >= 1 &&
                       all(c -> staticIconMember(c) || transStage6[c], nonDec)

// 2. Stage 4 透明单枝穿透收集（在 matchLocalUnderAnchoredParents 原直接 pass 与 matchParentScopedStructurallyUniqueContainers 均跑完后运行；
//    frontier 仅运行 localExact / localResource / localInputAnchor / localPublic，绝不运行 localStructure）
func collectLocalFrontier(children, isUsed, isPureDec, transSingleNext, budget):
    frontier := []
    for ch in children:
        if !budget.charge(1): return nil, false
        if isUsed(ch) || isPureDec[ch]:
            continue
        cur := ch
        for depth := 0; depth < 8 && transSingleNext[cur] >= 0; depth++:
            if !budget.charge(1): return nil, false
            nxt := transSingleNext[cur]
            if isUsed(nxt):
                cur = -1
                break
            cur = nxt
        if cur >= 0 && !isPureDec[cur]:
            frontier = append(frontier, cur)
    return frontier, true

// 3. Stage 6 候选收集（在 matchStaticGeometrySiblings 内，每个已锚定父下独立展开，已匹配节点阻断展开；
//    展开 transStage6 包装层内部子节点时显式过滤 isPureDec[sub]，直接子节点保持原 RoleImage 语义）
func collectStaticFrontier(children, isUsed, isPureDec, transStage6, isStaticLeafOrIconContainer, budget):
    out := []
    type item struct{ node int; depth int }
    queue := [item{ch, 0} for ch in children]
    for head := 0; head < len(queue); head++:
        it := queue[head]
        if !budget.charge(1): return nil, false
        if isUsed(it.node): continue
        if isStaticLeafOrIconContainer(it.node):
            out = append(out, it.node)
            continue
        if it.depth < 8 && transStage6[it.node]:
            for sub in childrenOf(it.node):
                if isPureDec[sub]: continue
                queue = append(queue, item{sub, it.depth + 1})
    return out, true
```

### 5.3 边界行为、差分时间轴与失败语义

- **STOP / 撤权 / 超时 / 预算耗尽**：
  - `match.go` 的所有 Stage 4 穿透与 Stage 6 图标容器/穿透步骤均计费 `budget.charge(1)`；预算耗尽立即中止并丢弃未提交批次，按公共先序退回新分配 `ref`，绝不部分应用。
  - `capture.go` 的全池探查循环继续在每次调用 `CandidateAgainst` 前检查 `e.treeSelectionTerminated(work)`，STOP/撤权或期限到期立即退出并丢弃候选，绝不发布迟到树或推进 `Builder` 连续性。
- **有动作长标签截断边界与差分行为**：
  - 超长 `button`（4,009 字）在每次帧变化仅发生于第 `251` 码点之后时，`ProjectCompactTree` 生成完全相同的 250 码点前缀与 `truncated=true`，`ReadDiff` 产出 `changes=0` 零噪声空差异；
  - 若该 `button` 在后续帧因页面刷新变为 `<= 250` 码点短文本，`ReadDiff` 生成 `~ nX label=["短文本"] truncated=false`，`ReadApply` 与 `tree v5` 显式写入并还原 `Truncated=false`；
  - 若需要阅读被截断节点原文，调用方在保留窗口内调 `inspect({reference:O, node:"nX"})` 仍可得到 `inspect v3` 完整全文与四元矩形。
- **同 `ref` 跨帧角色翻转与无损重建**：
  - `M1 + M2a` 拍平后被提升为 `button` 的容器若在下一帧内部子级变为非纯文本退回 `view` 且保住同一 `ref`，`ReadDiff` 命中既有 `role changed` 判据返回 `ErrNotRepresentable`，`capture.go` 跳过该基线并在无其他合格基线时安全发布 `full`。
- **基线池 `(12, 10, 5)` 典型时间轴**：
  - `rev 1`：页 A 发布 `full` `O1` 并确认入池（当前基线 `O1`）；
  - `rev 2–6`：页 A 连续微调，相对 `O1` 发 `delta`（`span=1..5`）；
  - `rev 7`：跳转到页 B 发布 `full` `O7`（`activeBaselineID` 清空，`O1` 转为备用，此时 `span=6 <= 10`，**不再被瞬杀**；确认后池为 `[O7(主), O1(副)]`）；
  - `rev 8`：返回页 A，对 `O1` 计算 `span = 8 - 1 = 7 <= 10` 合格！发布 `delta base=O1`，且 `O1` 重新成为当前基线；
  - `rev 9–13`：在页 A 继续操作，`O1` 享主基线窗口 `span <= 12` 沿用至 `rev 13`（`span=12`）；`rev 14`（`span=13`）过期回退 `full`。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `release/versions.json` 及 4 个生成文件 | `release_version` 升为 `"0.33.0"`、`contract_version` 升为 `29`、`android_version_code` 升为 `35`，运行 `scripts/generate-version.ps1` 同步生成 4 个版本文件 |
| `internal/protocol/read_tree.go` 及 `geometry_visual_test.go` | `ReadNode` 新增 `Truncated bool`（位于 `resource` 与 `context` 之间，仅 `true` 编码、解码拒 `null`/非布尔）；`ReadSetNode` 新增 `Truncated Field[bool]`（在 `ReadSetNode.UnmarshalJSONFrom` 用内部 wire `jsontext.Value` + `isJSONNull` 显式拒绝 JSON `null`）；`ReadTree.Validate` 与 `ReadDelta.Validate`（`Add`/`Replace` 子树）禁 `detailed` 任何节点与 `compact` 的 `root`/`context` 节点设 `Truncated=true` |
| `internal/observation/build.go`、`build_purification_test.go`、`purification_test.go` | `cleanReadableText` 无条件执行 `strings.TrimSpace`（保留非隐形分支内部空行与 `RoleInput.Value` 原样）；更新对应单测断言并补 B1/B2 回归 |
| `internal/observation/read.go`、`read_diff.go`、`read_test.go`、`read_bounds_test.go`、`read_purification_test.go`、`compact_pruning_test.go` | 新增 `compactActionableLabelMaxRunes = 250` 前置截断与 `Truncated` 传递；`projectCompactNodes` 实现 `M1` 多子匿名容器拍平（护住原始无子合成叶）与自然释放 `M2a`；`compactNoReadableContent` 增 `!n.Truncated`；`read_diff.go`/`equalReadNode` 支持 `Truncated` 差分/应用与 `SetTouchesInteraction`；同步 `compact_pruning_test.go#L286-L300`、`read_bounds_test.go#L146-L168`（`n3` 多子 `wrapper` 拍平消失）及 `read_purification_test.go` 等多子匿名预期，补正反向单测 |
| `internal/observation/match.go`、`continuity_static_geometry_test.go`、`continuity_pipeline_test.go`（及新增单测文件） | 在 `buildPublicMatchIndex` 预计算公开口径装饰叶/装饰子树/图标容器/透明包装层标志；新增 Stage 4 透明单枝穿透内容/身份恢复（保留 `localStructure` 全量直属兄弟分母，严守 `local_structure_remaining_one_rejected`）；收紧 Stage 6 透明壳（单/多枝统一限无字静态图标/图标容器后代）与 `isStaticIconContainer` 分桶隔离、展开过滤 `isPureDec[sub]`；补全静止图标与列表回收对抗单测 |
| `internal/treetext/encode.go`、`encode_test.go` | `writeNodeColumns` 在 `resource` 后、`context` 前支持 `truncated=true`；`writeSetNode` 支持 `truncated=true\|false`；`validReadNodeTreeDetails` 同步禁 `Context \|\| RoleRoot` 设 `Truncated=true`；补齐编解码及负例测试 |
| `internal/engine/capture.go`、`baseline_pool_test.go`、`commit_test.go`、`tree_budget_test.go`、`observation_continuity_integration_test.go` | 调整池与门槛常量为 `current=12, backup=10, maxBaselines=5, numerator=8, denominator=10`（清理包内过时 `tree v4` 注释）；同步更新 `commit_test.go#L304-L309` 步长夹具（`%3 → %2` / `/3 → /2`）、`tree_budget_test.go`（`installFullFallbackPool` 预置 5 项测试留 4 淘汰第 5 项）、`baseline_pool_test.go#L359-L386, #L606-L708`（窗口 12/10 与容量 5）及 `assertReadContentConsistent`/`compactLabelsMatch` 对 `T250`/`Truncated` 的契约说明；补 12/10/5 与 `<80%` 测试 |
| `internal/mcp/tree_text.go`、`tree_text_test.go`、`schema.go`、`schema_test.go`、`result_manifest_test.go` | `detailedRectanglesOnly` 拒绝 `Truncated==true`、`isAnonymousDefaultViewChain` 要求 `!node.Truncated`；更新 `result_manifest_test.go`（`rmfCompositeInventory["ReadNode"/"ReadSetNode"]`、`rmfNode`、`rmfSetNode`，按需核对表覆盖）；更新 `tree_text_test.go`（`parseNodeTailFields` 于 `resource=` 与 `context` 间支持 `truncated=true`、`parseV5SetLineValue` 支持 `truncated=true\|false` 并入白名单）；更新 `treeTextGuide`（移除 `len>=2 保持层级`，补充公开 `label` 空白净化、`M1` 多子匿名拍平、`T250` 有动作截断与 `truncated=true\|false` 语法，不塞入内部池参数）并核对 `baselineDiscoveryBytes = 37500` |
| `scripts/mcp-probe/result_text.go`、`result_v5_test.go`、`tree_body_consistency_test.go` | `validateFullNodeFlags` 扩展接收 `rules.detailed` 并禁 `detailed` 出现 `truncated=`；`validateTreeNodeColumns` 在 `resource=` 后、`context` 前单独消费 `truncated=true`（不入 `treeFullFlagOrder`）；`validateSetNodeFlags` 严格校验 `truncated=true\|false` 并拒 `null`/非法值 |
| `README.md` 与 `docs/*.md`（9 份正式文档） | 同步契约号 `29`、发行号、工具目录指纹、公开标签空白净化、多子扁平化、`T250` + `truncated`、Stage 4/6 静止保号边界、`(12, 10, 5)` 基线池与 `<80%` 差分门槛 |

### 6.2 配置与取值

所有阈值均为包内确定性编译期常量，不新增外部配置文件或请求参数：

- `compactActionableLabelMaxRunes = 250`（单段 label 截断码点上限，仅用于 `len(Actions) > 0 && Role != RoleInput && !Context`）；
- `maxTransparentWrapperDepth = 8`（Stage 4/6 透明匿名包装层最大穿透深度）；
- `currentBaselineRevisionSpan = 12`、`backupBaselineRevisionSpan = 10`、`maxBaselines = 5`、`maxBackupBaselines = 4`、`maxBaselineAge = 180 * time.Second`、`baselineDeltaNumerator = 8`、`baselineDeltaDenominator = 10`（严格 `< 80%`）。

### 6.3 日志与可观测性

- 严格遵守日志白名单（AGENTS.md 硬约束第 10 条）：`cleanReadableText`、`T250` 截断前后的标签正文、`truncated` 具体节点 `ref` 及透明穿透节点均**不写入** `eventlog`；
- `observation.baseline`（`primary`/`historical`/`full`）与 `observation.retention`（池退出数/历史淘汰数）继续复用既有白名单字段。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威与状态锁短小 | 不变：标签清洗、保号匹配、`T250`、多子拍平、全池 `CandidateAgainst`（最多 5 份）与 `tree v5` 字节计量全部在 `e.mu` 锁外完成 |
| 原始采集与重验真实性 | 不变：`device.Capture` 原串、`RoleInput.Value`、完整 `Snapshot.Tree`、`act` 静态选点与 Tier 1–4 实时重验均保留全量未截断事实 |
| 严格接入与三态省略 | 保持且加固：`ReadNode.Truncated` 仅 `true` 写出、拒绝 `null`；`ReadSetNode.Truncated` 显式区分省略（未改）与 `false`/`true`，并显式拒绝 JSON `null`；`SetTouchesInteraction` 禁止 `delta` 改根 `truncated` |
| 保守保号安全边界 | 保持：Stage 6 严格限制图标容器只能含纯装饰图子树且按 `decorativeContainer` 与真叶分桶隔离；Stage 4 透明单枝穿透仅执行内容/身份 pass 以护全 `localStructure` 全量兄弟分母守卫；Stage 6 透明包装层（单枝与多枝）均严禁包含文本叶、输入框或一般业务容器，展开内部子节点时过滤装饰节点，且继续禁止无锚点输入框匹配与跨帧换序配对 |
| `inspect` 详细兜底契约 | 不变：`inspect v3`（`view=detailed`）保持全节点、四元矩形与 100% 完整未截断标签正文，且禁止出现 `truncated=true` |

### 6.5 契约与文档影响

- 共同 `contract_version` 升级至 `29`（同步 `release_version = "0.33.0"`、`android_version_code = 35`，HTTP `X-Mobile-Use-Contract: 29`、`GET /health`、`/v1/control/sync`、`build/release/manifest.json` 同步）；
- 全仓同步以下正式文档与 `README.md`：
  - `docs/interface.md`（接入契约 `29`、§5/§11/§12 `Node`/`ReadSetNode` 新增 `truncated` 字段与 `compact` 截断规则、`cleanReadableText` 空白修剪、`M1` 多子扁平化、`(12,10,5)` 基线池与 `<80%` 门槛、客户端/调用方留存要求）；
  - `docs/mcp.md`（更新工具目录 SHA-256 指纹、`tree v5` 节点/set 行 `truncated` 语法、`compact` 净体描述、基线池 `12`/`10`/`5` 与 `<80%`）；
  - `docs/architecture.md`（§4 Stage 1 空白修剪、Stage 2 `T250` + 多子拍平、Stage 4/6 图标容器与透明包装层穿透规则、`(12,10,5)` 池与 `<80%` 门槛、版本源同步）；
  - `docs/client.md`、`docs/integration.md`（同步契约 `29`、客户端基线保留至少覆盖当前 `12` 步与备用 `10` 步、`80%` 门槛、`compact` 截断与 `inspect` 补看）；
  - `docs/release.md`、`docs/control-protocol.md`、`docs/runbook.md`、`docs/phone-operations.md`、`README.md`（同步发行号/契约号/versionCode 与默认选树参数表述）。
- 按 AGENTS.md 第 15 条，实现提交前必须由独立只读文档审计 subagent 审核全部变更 `docs/` 并取得 `clean` 结论。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

严格区分**离线双会话同轨重放实测**与**实机预期**（不将重放或离线模拟写成实机结论）：

1. **双会话离线同轨重放实测（已由 `counterfactual_v32.py` 在真实轨迹上逐字节验证）**：
   - 主会话 `3e7a393a`（133 棵树）：落地共识可重放组合（`M1 + M2a + B1/B2 + 有动作 T250 + 80%`）后，节点总数 **`8,828 → 6,203`（`-29.7%`）**、最大树深 **`17 → 10`**、帧分布 `75F/58D → 72F/61D`、观察树总 token 从 `76,832` 降至 **`65,129`（`-15.23%`，`-11,703 tok`）**，全 `mobile-use` 输出从 `85,505` 降至 `73,802`（`-13.69%`，未计 1 处 `truncated=true` 约 +1~2 tok）；
   - 对照会话 `2dd44960`（166 棵树，投影至 `0.32.0` 口径）：节点 `-22.03%`、最大树深 `15 → 10`、观察树 token 从 `125,943` 降至 **`114,979`（`-8.71%`，`-10,964 tok`）**。
2. **静止页面与跨页重访预期收益（待实施后由单测与实机实测复核）**：
   - `match.go` 现象 A 修复：预计消除 `3e7a393a` 中 `36/58` 份 delta 里的同形图标/包装层原地换号及 `12` 份静止纯假差分；
   - 基线池 `(12, 10, 5)` 现象 B 调优：双会话模拟器预计在 `3e7a393a` 多回收 `+8` 帧高相似重访整树（原 `6,437` tok，平均探测 `3.83` 次/帧），在 `2dd44960` 多回收 `+5` 帧（原 `5,786` tok，平均探测 `4.31` 次/帧）。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| DSH 宿主多轮上下文线性累积（177 步达 `1,802` 万 token）及 Agent `pwsh` 狂扫全量文档 | 宿主上下文折叠（可降 `92.19%` 树暴露）与工具提示策略属于宿主侧职责，本服务端与 MCP 适配器不保存/裁剪跨轮历史 |
| 含图片与多列文/混排按钮的复杂卡片（如双列带播放数视频卡） | 按双会话共识已明确放弃 `S_plain` 与 `partial_hoist`，此类混排卡片仍保持独立文本子叶以避免跨语义粘连和状态重建副作用 |
| `>= 250` 字的无动作商品详情/长回答正文，以及超长 `input.Value` | 为保障模型读全文及写回真实性，被动文本节点与 `input.Value` 刻意永不截断 |
| 跨度 `> 12`（主）/`> 10`（副）、龄期 `>= 180s` 或嵌套超过 `5` 个活跃页面的重访 | 基线池出于回溯边界与 `32 MiB` 预算控制刻意锁定为 `(12, 10, 5)` + `180s` |
| 跨帧结构大改、坐标偏移 `> 4px` 或无区别重叠控件的 `ref` 抖动 | 保守保号坚持宁可发新号绝不错绑；重叠相同坐标且无内容差异的同型兄弟仍按新号处理 |
| 研究报告 §6 的三项调用体验摩擦（`app` 参数误传、`act(scroll)` 底层遮罩候选标 `executable=true`、`app.kill` 结果行分栏提示） | 未列入 §8.1 最终工程共识，本提案不修改 `app` 描述、`scroll` 候选排序与 `app.kill` 文本行 |

### 7.3 成本

- **设备、ADB 与网络 I/O**：零新增 ADB、u2 RPC 或截图读取；因观察树与差分体积下降，HTTP 与 MCP 传输字节减少。
- **服务端 CPU 与内存**：
  - `cleanReadableText` 与 `T250` 为单遍线性扫描；`projectCompactNodes` 多子拍平仅浅拷贝切片引用，减少 `ReadNode` 分配与 JSON/treetext 序列化体积，单帧反而更快；
  - `match.go` 预计算为 O(N) 线性扫描，Stage 4/6 透明穿透受深度上限 `8` 与 `Wmax` 确定性工作预算硬约束；
  - `capture.go` 基线池由 `4` 升为 `5` 后，双会话平均每帧 `CandidateAgainst` 探测从 `1.85 / 2.29` 次温和升至 `3.83 / 4.31` 次（最多 `5` 次，且候选间检查取消/期限），保留快照总内存继续受既有 `MaxRetainedSnapshotCostBytes = 32 MiB` 与 `MaxObservations = 32` 严格兜底。

---

## 8. 验证计划

### 8.1 单元测试

每项改动均配备**至少一条反向对照用例**（去掉对应实现分支时必红）：

| 用例 | 期望 |
| --- | --- |
| **反向对照 1（B1+B2 空白净化）**：构造纯 `"\u00a0"`、纯 `"\n "`、`"\u00a0林俊杰 "`、`"a\n\nb"` 与带首尾空格的 `EditText.Text` | 纯空白/NBSP 无动作默认叶被过滤不分配 `ref`；`"林俊杰 "` 净化为 `"林俊杰"`；`"a\n\nb"` 中间空行原样保留；`RoleInput.Value` 原样保留首尾空格；去掉无条件 `TrimSpace` 后前两项失败 |
| **反向对照 2（M1+M2a 扁平化与后置提升）**：构造多层多分支匿名壳、嵌套匿名壳及 `view(tap, resource="app:id/card") -> FrameLayout(匿名, 2子) -> [text("歌名"), text("歌手")]`；同时包含原始无子 `AnonymousPlain` 合成叶与带 `context`/`selected=true` 容器 | 多分支匿名壳被彻底拍平、按钮自然提升为 `button "歌名" "歌手"` 且 `Resource == nil`；原始无子 `AnonymousPlain` 合成叶与 `context`/非默认容器不被误裁误拍；去掉多子拍平后按钮无法提升、测试失败 |
| **反向对照 3（T250 前置截断与 `truncated` 不变式）**：构造 `250` 码点、`251` 码点含多字节中文有动作 `button`、无动作 `4,000` 字 `text`、`4,000` 字 `input`、`view(tap) -> text(350字)`、`button(260字复合label) -> text(第255字起子串)` | 仅 `251` 码点 `button` 精确保留前 `250` 码点且 `Truncated=true`（无 `"..."`、无半字乱码）；`250` 码点、`4,000` 字被动 `text`、`4,000` 字 `input` 与 `M2a` 提升出的 `350` 字 `button` 均保留全文且 `Truncated=false`；`A1a` 不误删未被截断后标签覆盖的子文本；`ProjectDetailedTree` 保留全文且 `Truncated=false`；去掉 `T250` 后长 `button` 保持 `4,009` 字导致用例失败 |
| **协议/`ReadDiff`/`treetext`/`mcp` 门禁与 `mcp-probe` 往返**：覆盖 `ReadNode`/`ReadSetNode` 对 `"truncated":null`、非布尔、`ViewDetailed`/`inspect v3` 中 `truncated=true` 及 `ReadDelta.Validate`/`validReadNodeTreeDetails` 的拒绝；覆盖 `TestResultFieldManifest` 与 `tree_text_test.go` 列序校验；覆盖 `false→true`、`true→false`（显式 `truncated=false`）、>250 码点尾部变动生成空差分、以及 `treetext` 与 `mcp-probe` 双通道逐行一致 | 非法 `null`/详细视图或错列 `truncated=true` 一律拒收；`ReadDiff` + `ReadApply` 精确还原 `Truncated`；`TestResultFieldManifest` 与 `mcp-probe` 独立解析 100% 通过；移除 `ReadSetNode.Truncated` 拒 `null` 校验时负例失败 |
| **反向对照 4（`match.go` 现象 A 与列表回收对抗测试）**：(a) 复现 `O168` 顶栏 4 个带 `android:id` `ImageView` 的 `ViewGroup(tap)` 图标与 `O145` 单枝 `"VIP"` 壳 + 多子图标壳 `n5807 -> [icon1, icon2]` 两帧保号（且包装层内混有兄弟 `DecoImage` 不入 Stage 6）；(b) 对抗测试：列表滚动替换槽位 `[Card1("Song A","VIP",icon1,icon2), Card2("Song B","VIP",icon3,icon4)]` → 同坐标 `[Card3("Song C","VIP",...), Card4("Song D","VIP",...)]`、滚动单枝包装 `"VIP"` 卡片替换槽位，以及已锚定 `Keep` + 未匹配 `Wrap -> Card` 的 `local_structure_remaining_one_rejected` 变体；(c) 透明壳下双无锚点 `EditText`、真叶与图标容器混桶隔离、位置互换/偏移 `5px`、预算耗尽降级 | (a) 全部图标与行内 `"VIP"` 零抖动保住原 `ref`，装饰兄弟不误入 Stage 6（去掉现象 A 修复或 `cur` 误查 `raw.ResourceID` 时必断号）；(b) 对抗回收场景与 `local_structure_remaining_one_rejected` 变体均 `0` 跨卡偷号；(c) 无锚点 `EditText`/换序/位移 `>4px`/跨类混配均分配新号、不误配 |
| **反向对照 5（`<80%` 门限与 `(12,10,5)` 池）**：构造 `deltaBytes/fullBytes` 分别为 `799/1000` 与 `800/1000` 的 `tree v5` 块；测试主跨度 `12`/`13`、主用至跨度 `6` 降副在跨度 `10` 选回并继续沿用至 `12`、备用跨度 `11` 失效、满池 `1 主 + 4 副` 及压力淘汰 | `799/1000` 发 delta、`800/1000` 退 full；跨度 `10`/`12` 与 `5` 槽按判据精确命中/淘汰；改回 `70%` 或 `10/4/4` 时对应边界测试失败 |

### 8.2 实机/系统级（AGENTS.md 第 19、24 条发布交付默认范围）

由主代理亲自执行全量测试、发布构建、实机整组覆盖部署与双入口独立探针验证（不委派 subagent 代跑或代判）：

1. **全量测试与发布构建核验**：运行全仓 Go 单元测试（`internal/engine` 单次命令超时设为 `15` 分钟，代码与文档收敛后集中执行，不反复重跑碰结果）、Android 控制 App 构建与 `scripts/build-release.ps1`，并用 `scripts/verify-manifest.ps1` 核验 `build/release/manifest.json`（确认 `release_version=0.33.0`、`contract_version=29`、`android_version_code=35` 及最新工具目录指纹）。
2. **交付一（手机整组覆盖部署）**：执行 `scripts/deploy.ps1` 将新 `device-service`、手机 `device-mcp` 与 `control.apk` 整组覆盖部署至实机（保留 App 私有消息、已读与配对配置），核验 `/health` 返回新 `release_version` 与 `contract_version="29"`。
3. **交付二（覆盖变更行为的最小实机闭环）**：在实机虚拟屏会话驱动真实应用页面完成闭环，重点观测验证：
   - 嵌套多分支匿名容器被拍平、`button` 提升生效、`tree v5 full`/`delta` 正常交付；
   - 静止含图标页面连续 `observe`（或 `wait_for`）无同形图标容器原地换号；
   - 通过 `observe` 构造跨度 `>= 5` 的页面切走并重访返回，确认从 `1 主 + 4 副` 池内命中备用基线发出 `delta`；
   - 对长标签节点或基线执行 `inspect` 返回 `inspect v3`、`view=detailed`、四元矩形与完整未截断全文。
4. **交付三（Windows 主机双 exe 替换与双入口独立探针）**：从同一干净源码提交构建替换主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，通过 `scripts/build-host-tools.ps1 -VerifyOnly` 核验主机与手机双清单一致后，分别对**主机独立 stdio MCP**与**手机 Streamable HTTP MCP**运行新 `build/mcp-probe.exe` 完成探针校验。
5. **责任边界声明**：依赖方 Agent、其托管 MCP 进程重启与重连默认由用户自行处理；独立探针仅证明本次磁盘产物与双入口契约合规，验证记录严格区分磁盘替换、独立进程验证与依赖方切换，以及已实现/已单测/已实测。

### 8.3 记录

归档时写入 `maintenance/verification/compact-tree-flattening-truncation-continuity-and-pool.md`，如实记录实现提交 hash、构建清单、全仓测试命令与输出、实机部署与闭环实际观测数据、主机双 exe 替换哈希及双入口探针结果，严禁把离线模拟写成实机结论。

---

## 9. 已定与未决

### 9.1 已定

> 注：依据 AGENTS.md 第 20 条，本提案涵盖的六项核心工程方向、边界取舍与明确放弃项已于前期研究阶段与用户完成多轮讨论并收口于 `.state/dsh-session-3e7a393a/research/mcp-context-research-0.32.0.md` §8。

| 事项 | 结论 |
| --- | --- |
| 公开标签空白净化（B1 + B2） | `cleanReadableText` 无条件 `strings.TrimSpace`（纯空白/NBSP 归空，内部非占位换行保持不变）；`device.Capture` 与 `RoleInput.Value` 不经过该修剪；跳过 `B3`/`B4` |
| 有动作长标签前置截断（T250） | 仅针对 `len(Actions) > 0 && Role != RoleInput && !Context` 在 `projectCompactNodes` 下行早于 `ancestors` 压栈按单段 `250` 码点截断；被动节点、`input` 与 `inspect` 保持全文 |
| 截断标记契约设计 | 采用独立非可空布尔字段 `ReadNode.Truncated` / `ReadSetNode.Truncated`（拒绝 JSON `null`，`set` 显式输出 `true`/`false`，`detailed` 禁 `true`），不篡改 `label` 后缀且不附总字数 |
| 结构扁平化与文本提升（M1 + M2a） | `projectCompactNodes` 拍平所有 `len(Children) >= 1` 的隐藏匿名容器，保留原始无子 `AnonymousPlain` 合成叶测试行为；自然释放后置 `compactHoistsTextChildren`（含带 `resource` 且提升后清除）；放弃 `S_plain`、`partial_hoist`、`M2b` |
| 静止页面保号修复（现象 A） | 预计算公开口径装饰叶/子树、透明包装层与纯装饰图标容器；Stage 4 透明单枝穿透仅限内容/身份 pass（深度 `<= 8`，严守 `localStructure` 全量分母）；Stage 6 接受仅包纯装饰图子树的交互图标容器并按 `decorativeContainer` 与真叶分桶隔离、透明壳（单/多枝统一限无字静态图标后代、深度 `<= 8`、展开过滤装饰子节点）；严守一般多子业务容器与带文容器不进 Stage 6 |
| 基线池与差分门槛（现象 B + 门限） | 保留 `1 主 + N 副` 与按创建步算跨度架构，参数调为 `(主=12, 副=10, 容量=5)`（即 `1 主 + 4 副`），并在 `M1+M2a` 落地后将 `tree v5` 差分接受门槛调至严格 `< 80%`（止步于 `80%`，不上 `85%`） |
| 范围边界 | 不纳入研究报告 §8.2 放弃/暂缓项与 §1/§5.1 暂缓极简 `result v4` 外壳，不纳入宿主历史折叠；研究报告 §6 三项体验建议已由用户明确确认排除在本提案之外 |
| 普通树块头版本号 | 维持 `tree v5 full` / `tree v5 delta` 块头不变（参照契约 `28` 为 `result v4` 增补 `visual=` 而不升块头号的先例，通过共同 `contract_version = 29`、工具目录指纹与整组发布清单整组同步，不升 `tree v6`；用户授权主代理拍板定案） |
| 发布版本源取值 | `release/versions.json` 升级为 `release_version = "0.33.0"`、`contract_version = 29`、`android_version_code = 35`，由 `scripts/generate-version.ps1` 同步生成 4 个版本文件（用户授权主代理拍板定案） |

### 9.2 未决

无（前述三项未决已全部关闭：普通树块头维持 `tree v5` 不变；版本源定为 `release_version = "0.33.0"` / `contract_version = 29` / `android_version_code = 35`；研究报告 §6 三项调用体验建议已由用户明确排除在本提案之外）。

---

## 10. 实施顺序

1. **版本源与协议层（步骤 1）**：将 `release/versions.json` 更新为 `release_version = "0.33.0"`、`contract_version = 29`、`android_version_code = 35` 并运行 `scripts/generate-version.ps1`；在 `internal/protocol/read_tree.go` 新增 `ReadNode.Truncated` 与 `ReadSetNode.Truncated`（严格拒 `null` 编解码与 `Validate` 视图约束），补齐 `internal/protocol` 单元测试。
2. **`internal/observation` 纯算法实现（步骤 2，依赖步骤 1）**：依次落地 `build.go`（B1+B2）、`read.go`（T250 前置截断与 `M1+M2a`）、`read_diff.go`（`Truncated` 差分/应用/根守卫）及 `match.go`（预计算、Stage 4 透明穿透、Stage 6 图标容器与安全穿透）；更新受控既有单测并补全全套正反向与列表回收对抗用例。
3. **`treetext` 与 `mcp-probe` 独立校验器（步骤 3，可与步骤 2 并行，依赖步骤 1）**：升级 `internal/treetext/encode.go` 与 `scripts/mcp-probe/result_text.go` 支持 `truncated` 列及严校验规则，补齐黄金样例与负例测试。
4. **`internal/engine` 与 `internal/mcp` 接线（步骤 4，依赖步骤 2、3）**：在 `internal/engine/capture.go` 更新 `(12, 10, 5)` 与 `<80%` 常量、清理过时 `tree v4` 注释并同步池/门槛/连续性测试；在 `internal/mcp/tree_text.go`、`result_manifest_test.go`、`tree_text_test.go` 与 `schema.go` 同步校验逻辑、反射清单与 `treeTextGuide`（核对 `baselineDiscoveryBytes` 预算）。
5. **正式文档同步、集中全测与文档审计（步骤 5，依赖步骤 4）**：更新 `README.md` 与全部 9 份 `docs/`；由主代理集中执行全仓 Go 单元测试（`internal/engine` 单次命令超时设为 `15` 分钟）；发起独立只读文档审计 subagent 审核全部变更 `docs/` 直至取得 `clean` 结论；**先提交实现（含 `docs/`）**。
6. **发布交付与独立归档（步骤 6，依赖步骤 5）**：从干净实现提交运行 `scripts/build-release.ps1` 与 `scripts/verify-manifest.ps1` → 手机整组覆盖部署（保留 App 数据）与最小实机闭环 → 构建替换主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 并核验 `-VerifyOnly` → 用新探针分别验证主机 stdio 与手机 HTTP MCP → 撰写 `maintenance/verification/compact-tree-flattening-truncation-continuity-and-pool.md`、更新 `maintenance/status.md` 与本提案 §11、移动归档至 `maintenance/proposals/implemented/`，并**除 §11 预先按归档路径写好的 `../../verification/...` 外，将正文中其余全部 `../../` 仓库相对链接重写为 `../../../`** 且跑目标存在性检查，独立提交归档。

---

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/compact-tree-flattening-truncation-continuity-and-pool.md`
（本节在归档移动之后补写，届时文件在 `implemented/` 下，故链接比新建时多一层）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| — | — |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| — | — |

---

## 附：本提案不改变的事项

- 不改变 `device.Capture` 原始节点数据、`RoleInput.Value` 原始输入字节、`Snapshot.Tree` 完整节点结构与四元 `Bounds`、`Source`/`ContextSource` 映射。
- 不改变 `act` 的 `tap`/`long_press`/`text`/`scroll`/`swipe`/`key` 静态选点规划、安全轨迹规划、Tier 1–4 实时重验及 5 个静态 `422` 错误码与 `candidates[*].bounds` 四元矩形。
- 不改变 `wait_for` 的 `query`/`absent_query` 在完整公开树上的字面折叠匹配规则、`stable=true` 内容稳定摘要规则与采样时间参数。
- 不改变 `inspect`（`inspect v3`、`view=detailed`）的完整节点保留、两格缩进、显式动作词、`[l,t,r,b]` 四元矩形输出、完整 `resource` 与 100% 未截断文本正文。
- 不改变 `Observation.visual` 四重计算口径与 `delta` 同态去重机制，不改变 `result v4` 外壳文法与省略清单。
- 不改变基线池 `180s` 年龄上限、`MaxObservations=32`、`MaxFullTreeBytes=128 KiB`、`MaxSnapshotCostBytes=4 MiB` 与 `MaxRetainedSnapshotCostBytes=32 MiB` 预算，也不改变按原 `full` 创建修订计算跨度的硬上限原则。
- 不在本仓库内实施任何 DSH 宿主上下文折叠策略或研究报告 §8.2 已明确放弃的启发式规则。
