# 议题 4：动作观察重验放宽（Action Revalidation Relaxation）

状态：已实现（代码全覆盖；实机验证有限；最后实现/发行锚点提交 `049583d328e335c56d56f153b7464ea03f1ca814`，2026-11-16）。
基线提交：`6ec25c87daefbc01c99f78d968214304641d119d`（2026-11-15）。
适用范围：`act` 的 ref 点击/长按/输入/滚动重验、实际派发、O/P/STOP/错误分层；坐标动作与 raw swipe 不获得重定向能力。
影响范围：`internal/engine/{act,act_revalidation_policy,tap_scope,scroll,ui_execution}.go`、策略/假设备/运行时测试及 Android transport/RPC 消费者。
契约：改变动作重验契约。共同 `contract_version` 由 14 升至 15；不新增公开字段或错误码，保留既有 `CodeStopped`/`CodeHierarchyFailed` 族。
最后实现/发行锚点：`049583d328e335c56d56f153b7464ea03f1ca814`。
全部实现提交：`900e640fe8343f92c4e8dd82dd3df839dde20756`、`bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`、`38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`、`049583d328e335c56d56f153b7464ea03f1ca814`。
验证记录：[实现与验证](../../../verification/observation-continuity-and-revalidation.md)、[统一基准](../../../verification/observation-continuity-benchmarks.md)。
所属总览：[README.md](README.md)。
史实边界：正文设计论证及其中“提案未实现”“当前未执行”“未来”等陈述按基线写作时点保留；源码定位、行号、常量与默认值仍以基线为准，代码前进后须重新核对。实施结论以 §11 和验证记录为准。

本次修订保留“祖先链去坐标、目标分层几何、点击/滚动动态安全点、软键盘位移后的有身份约束输入绑定”四条总体路线，但不把四条路线实现成一次无差别的 `matchFreshNode` 放宽。执行目标、坐标动作、原始 swipe、滚动容器和文字焦点分别保留自己的授权证明。

Tier 4 的“任意文案变化仍视为同一目标”不进入默认实现。它可能把“删除”变成“分享”，或把安全动作变成不可逆动作，必须在实现前由用户作为独立取舍明确决定；没有决定时一律拒绝。

## 0. 审核结论

### 0.1 修订前的真实问题

下表的位置描述针对修订前原稿；源码位置均按本提案基准提交核对。

| 编号 | 修订前原稿位置 | 原稿问题 | 当前源码证据 | 最小反例或触发序列 | 本稿修正 |
| --- | --- | --- | --- | --- | --- |
| A1 | `04:11-14, 50-58, 74-107, 333-382` | 原稿把 `Act` 描述成先有 `planTapTarget`/`planScrollTarget`，并引用 `captureActiveWindow`、`textAnchor`、`buildTapScope`、`effectiveCarrierForTarget` 等不存在或签名错误的入口。`planTapTarget` 在当前源码中确实存在，但只有定义、没有调用处。 | 实际入口是 [`Act`](../../../../internal/engine/act.go#L15)、静态准入 [`staticActCheckLocked`](../../../../internal/engine/act.go#L26)、认领 [`claimObservationLocked`](../../../../internal/engine/act.go#L342)、重验 [`revalidateActTarget`](../../../../internal/engine/act.go#L379)、点击重验 [`revalidateTapTarget`](../../../../internal/engine/act.go#L429) 和派发 [`dispatchAct`](../../../../internal/engine/act.go#L615)。点击静态入口是 [`staticTapRefCheck`](../../../../internal/engine/tap_scope.go#L906)，滚动静态入口是 [`resolveScrollPlan`](../../../../internal/engine/scroll.go#L452)。 | 按原稿伪代码搜索函数会得到零调用处，无法确定真实锁边界、O/P 认领和 runtime 消费点。 | 全部改为 HEAD 的入口和函数职责；新增策略函数明确插在认领后、`beginUIDispatch` 前。 |
| A2 | `04:74-96, 289-315` | 原稿把 `carrierRaw` 当成可点击祖先，并在祖先链变化时向上重新寻找载体。后续收紧又改成“原 carrier 消失一律拒绝”，撤掉了提案希望支持的动态载体路线。 | `tapPlan.carrierRaw` 是安全子载体，字段定义见 [`tap_scope.go:26`](../../../../internal/engine/tap_scope.go#L26)，子载体类型见 [`tap_scope.go:79`](../../../../internal/engine/tap_scope.go#L79)。`collectTapScope` 只遍历 `input.target.Children`，见 [`tap_scope.go:468`](../../../../internal/engine/tap_scope.go#L468)；候选和落点证明也只接受目标子树内的安全载体，见 [`tap_scope.go:551`](../../../../internal/engine/tap_scope.go#L551) 和 [`tap_scope.go:634`](../../../../internal/engine/tap_scope.go#L634)，完整选择入口见 [`chooseTapPlan`](../../../../internal/engine/tap_scope.go#L722)。 | 稳定 item `item_42` 的原图片载体 A 被移除，新图片 B 仍在同一 target scope 且安全。只绑定 A 会错误拒绝；但若只看目标位置，另一个同类大按钮或全尺寸交互覆盖可能被误点。 | 禁止向上搜索和穿过 blocker。原静态点仍可证明时优先保留；原点失效后，原 carrier 仍匹配且安全时优先沿用。原 carrier 已失效时，只有目标双向唯一、公开 action 仍有效且有独立强内容身份，才可在同一 target scope 内重选安全 carrier/点。没有独立目标身份时拒绝。 |
| A3 | `04:134-151, 393-441` | 原稿的 `coreCapabilityMatch`、L2 和文本伪代码直接把缺失的 `Has*` 当普通 `false/true`。 | `RawNode` 明确保存 `HasEnabled/HasFocused/HasPassword/HasClickable/HasLongClick/HasScrollable` 等 presence，见 [`device/types.go:76`](../../../../internal/device/types.go#L76)；公开投影用 `triBool` 保留 nil，见 [`build.go:477`](../../../../internal/observation/build.go#L477)；动作只由已知正向能力产生，见 [`build.go:440`](../../../../internal/observation/build.go#L440)。文本当前还要求已知 focused/enabled/non-password，见 [`act.go:798`](../../../../internal/engine/act.go#L798)。 | 源帧没有 `HasFocused`，原稿归一成 false；新帧唯一获焦，归一成 true，于是错误地认为目标焦点状态已确认。 | 执行匹配始终消费 `RawNode.Has*`。known-known 相等才算已知相等；known-unknown 是失配；unknown-unknown 不产生正向证据。 |
| A4 | `04:214-233` | 原稿 L2 跳过所有“无 resource-id、不可滚动”的祖先，未排除文本、点击、长按、编辑和 password/protected 边界；收紧版又只看三个字符串是否为空。 | 公开动作来自源能力，见 [`build.go:440`](../../../../internal/observation/build.go#L440)；protected 来自 password，见 [`build.go:363`](../../../../internal/observation/build.go#L363)；点击 scope 本身把 tap/long_press/text 视为交互边界，见 [`tap_scope.go:418`](../../../../internal/engine/tap_scope.go#L418)；源 presence 与值分开保存在 `TextSet/DescSet/ResourceSet`，见 [`types.go:76`](../../../../internal/device/types.go#L76)。 | 旧 wrapper 的 `TextSet=false`，新帧同值字符串为空或 presence 缺失；只比较 `Text==""` 会把未知内容当成已知空。 | `TextSet && Text==""`、`DescSet && Description==""`、`ResourceSet && ResourceID==""` 三者同时成立才允许视为三项内容为空；任一 presence 缺失均不透明。交互能力仍需已知安全。 |
| A5 | `04:214-233, 275-279` | 原稿允许 L2 只匹配合成根，且 Tier 2 在多个候选中取最大 overlap。 | 根是合成阅读节点，不承载源控件业务身份。README 已规定几何最大值不能在多候选中胜出，见 [README §2.3](README.md)。 | 同 App、同 task 的另一个页面有两个同 class/text 控件，bounding box 分别重叠 0.81 和 0.79；最大者胜出即错误派发。 | 每个 Tier 必须恰好一个候选；零个继续下一层，多个立即拒绝。Tier 3 还必须保留至少一个非根语义边界锚点。 |
| A6 | `04:279` | Tier 4 允许仅因 `coreCapabilityMatch` 就忽略任意文本变化。 | `Text` 是源业务标签，当前完整身份包含 Text，见 [`identity.go:8`](../../../../internal/engine/identity.go#L8)。 | 旧按钮文案“删除”，新按钮文案“分享”，位置、class、resource-id 和能力相同；自动 Tier 4 会接受。 | 默认路径只实现 Tier 1–3。Tier 4 保留为未决取舍；若未来启用，只能使用经用户批准的受限转换集合。 |
| A7 | `04:387-390` | 原稿 `shifted` 的全屏“唯一同类型获焦输入框”不构成目标身份。 | 当前 `bindFocusedText` 的精确分支使用 resource/class/description，弱分支要求原点和几何重叠，见 [`act.go:798`](../../../../internal/engine/act.go#L798)。`RawNode` 没有 Hint 字段。 | 点击输入框 A 后页面自动把同 class 的搜索框 B 聚焦；全屏只有 B 获焦，原稿会绑定 B。 | 删除 `anyFocusedSameType`。shifted 必须有非空 ResourceID 或 Description 锚点，并在旧帧和新帧各自唯一；只放宽纵向位移。 |
| A8 | `04:333-382` | 原稿引用不存在的 `axisBounds`、`intervalOverlapRatio`、三参数 `scrollGeometry` 和 `planScrollTarget`。 | `scrollPlan` 只有 `start/end`，见 [`scroll.go:325`](../../../../internal/engine/scroll.go#L325)；实际几何函数为 `scrollGeometry(rect, screen, direction, amount)`，见 [`scroll.go:352`](../../../../internal/engine/scroll.go#L352)；实际安全区间 helper 见 [`scroll.go:180`](../../../../internal/engine/scroll.go#L180)、[`scroll.go:255`](../../../../internal/engine/scroll.go#L255)、[`scroll.go:279`](../../../../internal/engine/scroll.go#L279) 和 [`scroll.go:289`](../../../../internal/engine/scroll.go#L289)。 | 仅比较轴向区间重叠会把横轴已移到另一列的同内容容器当成同一个；按原稿签名无法编译。 | 使用真实 clipped rect 和 helper；同时要求轴向连续、离轴边缘稳定，并对整条新轨迹做 `provesSegment`。 |
| A9 | `04:333-382` | 原稿声称更新 plan 后 `dispatchScroll` 会使用新轨迹，但没有说明当前 dispatch 消费的是 `scrollFrame`。 | 当前派发直接使用 [`scrollFrame.start/end`](../../../../internal/engine/scroll.go#L716)，不是本地重建的 plan 副本。 | 重算只写入局部 `*scrollPlan`，传入 `dispatchScroll` 的 frame 仍是旧端点，实际设备仍收到旧轨迹。 | 重验生成新的不可变 `scrollFrame`，重算端点写入该 frame，`dispatchScroll` 只消费该 frame。 |
| A10 | `04 §2（修订前约 :153-166）` | 原稿称 settle 后重验 100% 通过，并写成统一 150ms。 | 当前策略是 act 400ms、start 300ms、open-app 600ms，见 [`settle.go:45`](../../../../internal/engine/settle.go#L45)；摘要为严格 19 字段，见 [`settle.go:310`](../../../../internal/engine/settle.go#L310)；未知前台仍拒绝动作，见 [`act.go:588`](../../../../internal/engine/act.go#L588)。 | 两帧完全一致只能证明采样到的源树未变，不能排除前景 unknown、L2 根-only、多个同身份候选或焦点在帧后变化。 | settle 完全不变。动作重验是独立门禁，不承诺任何 100% 或 300ms/600ms 关系。 |
| A11 | `04:74-107, 289-441` | 原稿把 O/P、STOP、Preview、source mapping 和最终派发门禁省略在示意算法外。 | 单向认领见 [`act.go:342`](../../../../internal/engine/act.go#L342)；Preview 不推进 ref 连续性，见 [`build.go:171`](../../../../internal/observation/build.go#L171)；最终派发门禁见 [`ui_execution.go:86`](../../../../internal/engine/ui_execution.go#L86)；工作撤权条件见 [`operations.go:420`](../../../../internal/engine/operations.go#L420)。 | 匹配函数单独写对仍可能在 STOP 后派发，或把 Preview 当成会话候选提交。 | 算法明确保留静态准入、一次认领、Preview 只读、锁外计算、`beginUIDispatch` 最终重检和拒绝观察交付路径。 |

### 0.2 与分册 01 的状态语义交叉

04 的动作安全逻辑必须直接使用 `device.RawNode` 的 presence 和值，不能消费“把未知归一成默认值”的阅读投影。

分册 01 可以讨论完整节点 wire 对**已知默认值**的省略，也可以让完整节点解码把缺席成员还原为契约默认；这不等价于把源节点未知事实改成已知事实。以下做法必须拒绝：

- 在 Build 前把 `HasFocused=false` 解释为未聚焦；
- 在动作匹配前把 `HasEnabled=false` 解释为已启用；
- 把 `HasPassword=false` 解释为非密码；
- 用公开 `Node.Focused == nil` 覆盖源 `RawNode.HasFocused`；
- 用合成根或 context 的默认状态推断其源控件状态。

当前分册 01 的“真实未知仍为 null、源 Has* 不受阅读精简影响”与本稿一致。若实施时 01 改成语义上归一 `RawNode.Has*`，04 必须先回退该项；动作授权不能用 wire 默认值补证据。

### 0.3 确定问题与待定取舍

确定问题包括 A1–A11 中的错误入口、错误载体方向、未知事实伪造、多候选最大值、无身份焦点绑定、错误滚动签名、runtime 未消费重算值和 settle 保证，以及两轮复检发现的 A12–A20。

以下属于需要确认的阈值或产品取舍：

- Tier 2/3 的 overlap、尺寸和中心偏移阈值；
- 滚动容器离轴连续性的像素/百分比容差；
- 是否永久不做 Tier 4；
- 是否允许 Description-only 锚点参与 shifted，还是只允许 ResourceID；
- 是否允许同 App 同 task 下的纯透明壳替换在没有任何非根 anchor 时仍使用 Tier 3。本稿建议不允许。

实现者不得把这些取舍静默变成宽松默认。

### 0.4 主审复检追加修正

| 编号 | 复检发现 | 本稿修正 | 对应反例或验收 |
| --- | --- | --- | --- |
| A12 | 修订示意仍在进入 Tier switch 前无条件比较 L1，导致 Tier 3 无法处理“透明 wrapper 改变 L1、但 L2 与非根 anchor 仍相同”的情况；同时只检查目标旧节点的 outgoing 数量，两个旧节点仍可能抢配同一个 fresh 节点。 | Tier 1/2 比较 L1；Tier 3 独立要求 L2、非根 anchor 和几何成立，不读取 L1。每个 Tier 都必须同时满足旧→新 outgoing 唯一和 fresh→旧 incoming 唯一。 | N3 验证 L1 不同但 Tier 3 成功；N5 验证 L1 变化后不能靠 Tier 1 先命中、且 root-only L2 被 Tier 3 拒绝；N33 验证两个旧节点争用同一 fresh点时双向不唯一而拒绝。 |
| A13 | `exact` 与最后执行的 `shifted` 曾都以稳定 anchor、尺寸和水平带为条件，`shifted` 实际不可达；无 anchor 的 `near` 又缺少 class 与资源/描述 presence 的来源身份约束。 | `exact` 改为完整来源 presence + anchor + 完全相等的 Bounds；`near` 检查完整来源 presence 和原点击点/矩形交叠；只有二者都无候选后才执行独立纵向放宽的 `shifted`，shifted 同样校验完整来源 presence。所有 anchor 计数先于 focused 过滤，并限制在目标 DisplayID。 | N34 验证无 anchor 旧目标消失后，不同来源字段即使占据旧点且唯一获焦也不能改绑；N35 验证 focus 前存在两个同来源候选时拒绝；N36 明确同 task、同签名替换不可辨识。 |
| A14 | `TaskID` 曾被写成“相同窗口”的证明。现有 `RawNode`/`device.Capture` 没有 window token，同一 task 可以切换页面或替换同签名节点。 | 接口和文字统一降格为“同前台 task 上下文”；TaskID 只与该事实一起使用，不承诺窗口身份。该限制保留为匹配能力边界。 | N36 和文字绑定验收必须覆盖“同 TaskID、不同页面/同签名替换”，不得以 TaskID 或全屏唯一焦点宣称目标已证明。 |
| A15 | 上一版为双向唯一构造 `original × fresh` 全量边集；20k×20k 可达 4 亿对，而且目标和点击载体各重跑一次。长祖先链也没有确定性的匹配工作量上限。 | 每个 Tier 只做两次定向扫描：固定 `want` 扫描 fresh 求唯一 outgoing；成功后固定该 fresh 反扫 original 求唯一 incoming。lineage 仅按实际比较到的索引惰性缓存。候选访问、祖先遍历和 lineage 比较均有确定性上限，并在扫描点检查 `ctx`；预算耗尽映射既有层级错误。 | N37 用 20k×20k 零/单候选计数排除 N²；N38 验证预算耗尽返回 `CodeHierarchyFailed` 且不尝试下一 Tier；N33 仍由反扫 incoming 计数拒绝。 |
| A16 | N21 只写“anchor 缺失或不同”，没有区分可观察差异与完全同签名替换。 | N21 只在候选不在原点击点/原几何，或 class、anchor、来源 presence 等可观察身份有异时承诺拒绝。若全部可观察签名一致，保留为 N36 已声明的不可辨识边界，不能靠唯一焦点伪造拒绝能力，也不能反过来承诺总能识别替换。 | N21 验证可观察差异拒绝；N36 只验证实现如实保留“同 task、非 window proof”的能力边界，不要求对完全同签名替换承诺总拒绝或总成功。 |
| A17 | matcher 把取消、预算耗尽、畸形树和普通无候选/歧义都压成统一的“不匹配”，调用层容易统一映射成 `target_changed`。Probe/Capture 的错误分支也没有在解释设备错误前重新检查撤权。 现有 `protocol.CodeStopped` 的值为 `control_revoked`，见 [`errors.go:177`](../../../../internal/protocol/errors.go#L177)；层级失败为 `hierarchy_unavailable`，见 [`errors.go:174`](../../../../internal/protocol/errors.go#L174)。撤权判断见 [`workRevoked`](../../../../internal/engine/operations.go#L386)，最终派发门禁见 [`beginUIDispatch`](../../../../internal/engine/ui_execution.go#L86)。可交付的 rejected frame 只包含 target/foreground/node-not-found，见 [`deliverableRejection`](../../../../internal/engine/delivery.go#L76)。 | STOP 恰在扫描末尾到达，matcher 已有一个候选，却返回 `target_changed`；调用层再把它当普通业务拒绝交付 retry。 | matcher 和调用层显式区分 unique/none/ambiguous/cancelled/budget/malformed；撤权用既有 `CodeStopped`，预算/畸形树用既有 `CodeHierarchyFailed`。target/scroll 的 none/ambiguous 映射 `target_changed`；carrier 的 none/ambiguous 只有在独立目标身份成立时进入安全解绑，否则同样拒绝。撤权、预算和层级失败都不 delivery、不 `markDelivered`、不生成 retry 新 O。 |
| A18 | 当前 `revalidateTapTarget` 在任何 carrier 失配时直接拒绝，因而无法在同 target 子树内安全换用另一个非阻塞载体。 carrier 只是安全子载体，不是点击祖先；动态候选只能来自目标子树。现有目标选择由 [`chooseTapPlan`](../../../../internal/engine/tap_scope.go#L722) 完成，且所有点仍由 [`provesSubtreeTapPoint`](../../../../internal/engine/tap_scope.go#L551) 证明。 | 稳定 item 的图片 A 消失、图片 B 仍安全；只绑定 A 会拒绝合法重试。反例是目标只靠 A 区分，换 B 后无法证明目标身份。 | 目标双向唯一、公开 action 有效且有独立强内容身份时允许安全解绑；优先原点，其次原 carrier，再在 target scope 内重选。原 carrier 是唯一目标身份时拒绝；全尺寸交互 blocker 覆盖时 `chooseTapPlan` 必须失败。 |
| A19 | `transparentWrapper` 只检查 `Text/Description/ResourceID` 的值是否为空，没有要求相应 presence 位明确存在。 三个 presence 与值分开存储在 [`types.go:76`](../../../../internal/device/types.go#L76)。`false` 不能为动作身份伪造“已知空字符串”。 | `TextSet=false, Text=""` 的 wrapper 被当成透明层，可能吞掉未知内容边界。 | 三项都必须 `Set=true && value==""`；任一 presence 缺失、任一值非空或任一能力未知/不安全都保持为边界。 |
| A20 | `lineageL1` 每次 cache miss 都 `make(map[int]struct{}, len(capture.Nodes))`。深度 1 的 20k 个候选仍会反复预留整棵树大小的 visited 容量，而且这部分分配未进入预算。 当前 visited 只用于一次祖先回溯，见 [`act.go:866`](../../../../internal/engine/act.go#L866) 的基线形式；提案需要 cycle-safe，但不能按全树容量反复分配。 | 20k 个同层候选全部在深度 1 失配，若每次预留 20k map bucket，会产生与候选数乘树大小相关的巨大临时分配。 | visited map 改为首次访问时才创建、容量随实际深度增长；每次首次创建都发生在一次已计费 lineage step 之后。N37 记录候选/lineage 操作计数及 lineage 相关总 allocation count/bytes，包括 visited map、lineage slice 和 memo backing store；不把候选扫描线性说成总分配恒定。 |

## 1. 当前真实调用链

### 1.1 `Act` 的总顺序

当前 [`Act`](../../../../internal/engine/act.go#L15) 的顺序是：

1. `registerOperation` 处理 P 去重；
2. 在状态锁内执行静态准入，见 [`act.go:26`](../../../../internal/engine/act.go#L26)；
3. 静态内容规划和错误在认领前完成，静态拒绝不认领 O；
4. `acquireWork` 取得唯一普通工作位置，见 [`act.go:59`](../../../../internal/engine/act.go#L59)；
5. `claimObservationLocked` 完成 `ready -> claimed(P)`，见 [`act.go:65`](../../../../internal/engine/act.go#L65)；
6. 认领后抓取 live capture 并做重验，见 [`act.go:76`](../../../../internal/engine/act.go#L76)；
7. 重验拒绝时，只有既有可交付错误族可使用已经读到的帧执行 `deliverRejection`；撤权、层级失败、预算和畸形树不交付。原 O 不归还 ready，见 [`delivery.go:9`](../../../../internal/engine/delivery.go#L9)；
8. 成功后才进入 dispatch，见 [`act.go:107`](../../../../internal/engine/act.go#L107)；
9. 每个真实输入 primitive 都必须经过 `beginUIDispatch`，见 [`ui_execution.go:86`](../../../../internal/engine/ui_execution.go#L86)。

本提案只替换第 6 步中的目标证明策略和第 8 步实际消费的重验结果，不改变上述权限顺序。

### 1.2 当前节点身份的严格点

[`rawIdentity`](../../../../internal/engine/identity.go#L8) 包含 Bounds、文本、能力、password、enabled/checked/selected/focused 及各自 presence。`identityOf` 同时被 settle 的层级摘要使用，不能为动作宽松匹配直接改写。

[`lineageOf`](../../../../internal/engine/act.go#L866) 把每个祖先的完整 `rawIdentity`（包括 Bounds）加入链；[`matchFreshNode`](../../../../internal/engine/act.go#L888) 要求目标完整身份和整条祖先链都完全相等，并且候选唯一。

因此，旧稿的痛点诊断在方向上成立：

- 祖先 1px 伸缩会让目标失配；
- 目标自身微移会让精确 Bounds 失配；
- Focused/Selected 改变会改变完整身份；
- 多框架包装层变化会改变祖先序列。

但修复不能把 settle 身份和所有 `matchFreshNode` 调用一起放宽。旧 `lineageOf` 还没有 visited 集合；畸形 parent cycle 可导致无限回溯。新策略必须做 cycle-safe、有长度上界的祖先遍历。

### 1.3 点击载体是子节点

[`collectTapScope`](../../../../internal/engine/tap_scope.go#L468) 从目标节点开始，只遍历其 children。非交互、非 context、有 source mapping 和正几何的后代成为 `tapCarrier`。交互节点成为 blocker/owner，而不是可用的穿透载体。

[`provesSubtreeTapPoint`](../../../../internal/engine/tap_scope.go#L551) 只证明一个点：

- 在目标 raw/public 交集内；
- 不落入目标子树中的 blocker；
- 深命中节点是 target，或 scope 中的安全 carrier。

它不证明全屏 z-order，也不证明目标子树外的 touch consumer。这个限制必须保留在提案和测试中。

### 1.4 滚动当前仍是固定轨迹

静态 [`resolveScrollPlan`](../../../../internal/engine/scroll.go#L452) 在认领前用 `scrollSafeSpans`、`longestSpan`、`safeRect` 和 `scrollGeometry` 生成固定 `start/end`。认领后的 [`revalidateScrollTarget`](../../../../internal/engine/scroll.go#L619) 只证明原轨迹整段仍安全，注释还明确表示不会重新选最长区间或重算端点，见 [`scroll.go:515`](../../../../internal/engine/scroll.go#L515) 和 [`scroll.go:685`](../../../../internal/engine/scroll.go#L685)。

本提案要改变这一契约。实施时不能只删除固定点比较，还必须更新正式 docs、MCP 工具说明、相关注释和测试，把“重验原固定轨迹”改成“重验原轨迹，必要时在同一已匹配容器内生成新的可证明轨迹”。

当前正式契约仍明确写着不重新规划固定轨迹，见 [`docs/interface.md:475`](../../../../docs/interface.md#L475)；MCP `act` 说明也写着认领后只重验固定点，见 [`internal/mcp/schema.go:181`](../../../../internal/mcp/schema.go#L181)。本提案不能把源码和这些契约分开改。

### 1.5 文字输入当前焦点门禁

[`bindFocusedText`](../../../../internal/engine/act.go#L798) 只接受：

- `Visible == true`；
- `HasFocused && Focused`；
- `Editable == true`；
- `HasEnabled && Enabled`；
- `HasPassword && !Password`。

这些门禁必须保留。`RawNode` 没有 Hint；`protocol.Node.Hint` 也不是当前源模型的可证明字段。任何 shifted 方案不得引用不存在的 `RawNode.Hint`。

## 2. 共同安全不变式

1. **执行权威不迁移**：动作仍只由 `internal/engine` 的当前 work、session、display generation、O claim 和 P 决定。
2. **静态拒绝不认领**：动作类型、字段组合、目标基础能力仍在认领前检查。
3. **认领单向**：同一 O 只从 ready 到 claimed 一次；认领后不归还。并发 `Act` 只能有一个认领胜者。
4. **同一 P 不派发两次**：匹配重算不重放 primitive。
5. **锁内不做搜索**：capture、Preview、祖先链、候选枚举、几何规划和日志写入都在状态锁外。
6. **Preview 不提交**：重建公开树只用于本次证明，不推进会话 ref 高水位。
7. **source mapping 必须完整**：目标/容器/载体的 public ref 必须映射回同一 fresh raw node；损坏映射是 hierarchy failure。
8. **唯一候选才匹配**：每个 Tier 恰好一个候选；零个才降级，多个立即拒绝。不得按遍历顺序、ordinal、map 顺序、最大 overlap 或全局焦点唯一性决胜。
9. **未知不伪造**：known-known 值必须相等；known-unknown 失配；unknown-unknown 无正向证据。动作能力必须已知为真。
10. **前台必须可确认**：`foregroundMatches` 的 present/task 检查和 unknown 拒绝保持不变。
11. **坐标动作原点授权**：坐标 tap/long_press/text 仍证明原 `(x,y)` 在新帧解析到同一来源节点；不重定向点。raw swipe 永远使用原 from/to。
12. **引用点击仅限目标子树**：可重算落点，但不得上溯目标祖先或穿过 blocker。原静态点优先；原点失效后原 carrier 可安全复用时优先沿用。只有目标双向唯一且有独立强内容身份，才可在 carrier 失配后于同一 target scope 内解绑并重选。
13. **滚动容器身份和轨迹分离**：先唯一匹配容器，再允许同容器轴向伸缩；轨迹必须整段证明。不能以轨迹长度反推容器身份。
14. **文字焦点不能代替身份**：唯一获焦不是稳定 anchor。焦点绑定只能在同 Display、同前台 task 上下文、同类型和稳定非空身份 anchor 双向唯一时跨键盘纵向位移。
15. **最终门禁不可省**：每个 tap/swipe/text primitive 前仍调用 `beginUIDispatch`。STOP 后不得派发；已经派发后的未知保留为 uncertain。
16. **事实分层**：后观察成功不改写已确认 executed；后观察失败不改写已确认的 dispatch receipt；text 的 tap、set、readback 分别记录。
17. **settle 保持独立**：不改变 19 字段 `rawIdentity`、层级摘要、400/300/600ms 策略或采样数。
18. **日志白名单**：最多记录 Tier、匹配模式、枚举和布尔；不记录文本、resource-id、bounds、坐标、ref、原始异常或树。
19. **匹配工作量有界且终止原因可区分**：每个 Tier 只做两次定向扫描，不构造全节点笛卡尔边集；候选访问和 lineage 工作由确定性预算限制，并检查 `ctx`。unique/none/ambiguous/cancelled/budget/malformed 必须分开；不得按墙钟、遍历偶然顺序或剩余预算选择候选。

## 3. 通用身份与分层匹配算法

新增的动作匹配策略应放在独立文件或独立函数中，例如 `act_revalidation_policy.go`。`identity.go` 的 `identityOf` 继续只服务 settle；不得为了动作匹配给它增加字段或改变比较语义。

以下伪代码把阈值写成常量，具体数值仍需在实施前作为取舍确认。

```go
type tri struct {
	known bool
	value bool
}

type triCompare int

const (
	triUnknown triCompare = iota
	triEqual
	triMismatch
)

func compareTri(a, b tri) triCompare {
	switch {
	case a.known && b.known && a.value == b.value:
		return triEqual
	case a.known && b.known:
		return triMismatch
	case a.known != b.known:
		// 一侧缺失不是“默认值相等”。
		return triMismatch
	default:
		return triUnknown
	}
}

func knownTrue(t tri) bool  { return t.known && t.value }
func knownFalse(t tri) bool { return t.known && !t.value }

type optionalString struct {
	set   bool
	value string
}

func optionalStringOf(raw *device.RawNode) (text, desc, resource optionalString) {
	text = optionalString{set: raw.TextSet, value: raw.Text}
	desc = optionalString{set: raw.DescSet, value: raw.Description}
	resource = optionalString{set: raw.ResourceSet, value: raw.ResourceID}
	return
}

func compareOptional(a, b optionalString) triCompare {
	switch {
	case a.set && b.set && a.value == b.value:
		return triEqual
	case a.set && b.set:
		return triMismatch
	case a.set != b.set:
		// 已知值/空值或已知值/未知值都不是同一个来源事实。
		return triMismatch
	default:
		return triUnknown
	}
}

// structureKey 是有结构顺序的源节点内容键，不含 Bounds、Focused、Selected。
// 它不替代 settle 的 rawIdentity，只服务动作目标匹配。
type structureKey struct {
	Package, Class string
	Text, Description, Resource optionalString

	Clickable, LongClickable, Scrollable tri
	Enabled, Checked, Password, Checkable tri
	Editable bool
}

func structureKeyOf(raw *device.RawNode) structureKey {
	text, desc, resource := optionalStringOf(raw)
	return structureKey{
		Package: raw.Package,
		Class:   raw.ClassName,
		Text: text, Description: desc, Resource: resource,
		Clickable: tri{raw.HasClickable, raw.Clickable},
		LongClickable: tri{raw.HasLongClick, raw.LongClickable},
		Scrollable: tri{raw.HasScrollable, raw.Scrollable},
		Enabled: tri{raw.HasEnabled, raw.Enabled},
		Checked: tri{raw.HasChecked, raw.Checked},
		Password: tri{raw.HasPassword, raw.Password},
		Checkable: tri{raw.HasCheckable, raw.Checkable},
		Editable: raw.Editable,
	}
}

// sameStructure 返回“没有已知冲突”和“是否得到正向身份证据”。
// unknown/unknown 既不冲突，也不贡献证据。
func sameStructure(a, b *device.RawNode) (compatible, evidence bool) {
	ka, kb := structureKeyOf(a), structureKeyOf(b)
	compatible = true
	if ka.Package != kb.Package || ka.Class != kb.Class || ka.Editable != kb.Editable {
		return false, false
	}
	evidence = ka.Package != "" || ka.Class != ""

	consumeOptional := func(x, y optionalString, nonEmptyAnchor bool) bool {
		switch compareOptional(x, y) {
		case triMismatch:
			compatible = false
		case triEqual:
			if nonEmptyAnchor && x.value != "" {
				evidence = true
			}
		}
		return compatible
	}
	consumeTri := func(x, y tri) bool {
		switch compareTri(x, y) {
		case triMismatch:
			compatible = false
		case triEqual:
			evidence = true
		}
		return compatible
	}

	if !consumeOptional(ka.Text, kb.Text, true) ||
		!consumeOptional(ka.Description, kb.Description, true) ||
		!consumeOptional(ka.Resource, kb.Resource, true) {
		return false, false
	}
	for _, pair := range [][2]tri{
		{ka.Clickable, kb.Clickable},
		{ka.LongClickable, kb.LongClickable},
		{ka.Scrollable, kb.Scrollable},
		{ka.Enabled, kb.Enabled},
		{ka.Checked, kb.Checked},
		{ka.Password, kb.Password},
		{ka.Checkable, kb.Checkable},
	} {
		if !consumeTri(pair[0], pair[1]) {
			return false, false
		}
	}
	return true, evidence
}

type lineageEntry struct {
	parent int
	key    structureKey
	root   bool
}

// lineageL1 保留每个祖先的不含 Bounds 结构键，顺序固定为近到远。
// visited 和 depth 上限把畸形 parent cycle 变成匹配失败，而不是死循环。
func lineageL1(capture *device.Capture, index int,
	budget *matchBudget) ([]lineageEntry, bool) {
	if capture == nil || index < 0 || index >= len(capture.Nodes) {
		budget.failMalformed()
		return nil, false
	}
	// 不在 cache miss 时按整树大小预分配。map 只在确有祖先时创建，
	// 后续 bucket 随实际深度增长；创建点位于一次已计费 lineage step 之后，N37 统计实际 bytes。
	var seen map[int]struct{}
	var out []lineageEntry
	for parent, depth := capture.Nodes[index].Parent, 0; parent >= 0; depth++ {
		if !budget.consumeLineage(depth) {
			return nil, false
		}
		if parent >= len(capture.Nodes) || len(out) >= len(capture.Nodes) {
			budget.failMalformed()
			return nil, false
		}
		if seen == nil {
			seen = make(map[int]struct{})
		}
		if _, duplicate := seen[parent]; duplicate {
			budget.failMalformed()
			return nil, false
		}
		seen[parent] = struct{}{}
		node := &capture.Nodes[parent]
		out = append(out, lineageEntry{
			parent: parent,
			key:    structureKeyOf(node),
			root:   node.Parent < 0,
		})
		parent = node.Parent
	}
	return out, true
}

// transparentWrapper 只接受“能证明没有任何自身语义或交互边界”的包装层。
func transparentWrapper(raw *device.RawNode) bool {
	if raw == nil || raw.Parent < 0 {
		return false
	}
	// 只有来源明确提供了空值，才能把内容键认定为“已知为空”。
	// presence 缺失、空字符串默认值或非空值都不能证明 wrapper 无内容边界。
	if !raw.ResourceSet || raw.ResourceID != "" {
		return false
	}
	if !raw.TextSet || raw.Text != "" {
		return false
	}
	if !raw.DescSet || raw.Description != "" {
		return false
	}
	if raw.Editable {
		return false
	}

	// 必须已知为 false；未知不能让包装层变成“看似透明”。
	if !knownFalse(tri{raw.HasClickable, raw.Clickable}) ||
		!knownFalse(tri{raw.HasLongClick, raw.LongClickable}) ||
		!knownFalse(tri{raw.HasScrollable, raw.Scrollable}) ||
		!knownFalse(tri{raw.HasPassword, raw.Password}) ||
		!knownFalse(tri{raw.HasCheckable, raw.Checkable}) {
		return false
	}
	if !knownTrue(tri{raw.HasEnabled, raw.Enabled}) ||
		!knownFalse(tri{raw.HasFocused, raw.Focused}) ||
		!knownFalse(tri{raw.HasSelected, raw.Selected}) ||
		!knownFalse(tri{raw.HasChecked, raw.Checked}) {
		return false
	}
	return true
}

// lineageL2 只移除可证明透明的包装层，保留每一个文本、交互、编辑、
// protected/password、状态边界和根。
func lineageL2(capture *device.Capture, index int,
	budget *matchBudget) ([]lineageEntry, bool) {
	all, ok := lineageL1(capture, index, budget)
	if !ok {
		return nil, false
	}
	out := make([]lineageEntry, 0, len(all))
	for _, entry := range all {
		if budget == nil || !budget.checkActive() {
			return nil, false
		}
		if entry.root {
			out = append(out, entry)
			continue
		}
		if entry.parent < 0 || entry.parent >= len(capture.Nodes) {
			budget.failMalformed()
			return nil, false
		}
		if transparentWrapper(&capture.Nodes[entry.parent]) {
			continue
		}
		out = append(out, entry)
	}
	return out, true
}

func strongLineageAnchor(entry lineageEntry) bool {
	key := entry.key
	return (key.Resource.set && key.Resource.value != "") ||
		(key.Text.set && key.Text.value != "") ||
		(key.Description.set && key.Description.value != "") ||
		knownTrue(key.Clickable) ||
		knownTrue(key.LongClickable) ||
		knownTrue(key.Scrollable) ||
		key.Editable ||
		knownTrue(key.Password) ||
		knownTrue(key.Checkable)
}

func rectValidOnScreen(rect, screen device.Rect) bool {
	return !rect.Empty() && rect.Left >= screen.Left && rect.Top >= screen.Top &&
		rect.Right <= screen.Right && rect.Bottom <= screen.Bottom
}

func sizeCompatible(a, b device.Rect) bool {
	aw, ah := a.Width(), a.Height()
	bw, bh := b.Width(), b.Height()
	if aw <= 0 || ah <= 0 || bw <= 0 || bh <= 0 {
		return false
	}
	return abs(aw-bw) <= max(8, max(aw, bw)/2) &&
		abs(ah-bh) <= max(8, max(ah, bh)/2)
}

func geometryCompatible(a, b, screen device.Rect, minOverlap float64) bool {
	if !rectValidOnScreen(a, screen) || !rectValidOnScreen(b, screen) ||
		!sizeCompatible(a, b) || overlapRatio(a, b) < minOverlap {
		return false
	}
	ax, ay := a.Center()
	bx, by := b.Center()
	return abs(ax-bx) <= max(8, max(a.Width(), b.Width())/2) &&
		abs(ay-by) <= max(8, max(a.Height(), b.Height())/2)
}

// 数值是建议起点，实施前作为独立取舍确认。
const (
	actTier2MinOverlap = 0.50
	actTier3MinOverlap = 0.60
)

type actMatch struct {
	raw      *device.RawNode
	tier     int
	independentContent bool
}

// matcher 的终止原因必须在进入客户端错误映射前保持分开。
type matchGate uint8

const (
	matchProceed matchGate = iota
	matchCancelled
	matchBudgetExhausted
	matchMalformed
)

type actProbeState uint8

const (
	actProbeNone actProbeState = iota
	actProbeUnique
	actProbeAmbiguous
	actProbeCancelled
	actProbeBudgetExhausted
	actProbeMalformed
)

type actProbeResult struct {
	candidateIndex     int
	independentContent bool
	state              actProbeState
}

// 初始上限用于防止病态长树；最终数值由基准和内存约束确认，不能改成
// 依赖墙钟的先到先得预算。
const (
	maxActLineageDepth = 4096
	maxActLineageSteps = 1 << 20
)

type matchBudget struct {
	ctx context.Context

	candidateVisits int
	candidateLimit  int
	lineageSteps    int
	lineageLimit    int
	stop            matchGate
}

func (b *matchBudget) fail(reason matchGate) bool {
	if b != nil && b.stop == matchProceed {
		b.stop = reason
	}
	return false
}

func (b *matchBudget) failMalformed() bool {
	return b.fail(matchMalformed)
}

func (b *matchBudget) consumeCandidate() bool {
	if !b.checkActive() {
		return false
	}
	if b.candidateVisits >= b.candidateLimit {
		return b.fail(matchBudgetExhausted)
	}
	b.candidateVisits++
	return true
}

func (b *matchBudget) checkActive() bool {
	if b == nil || b.ctx == nil || b.ctx.Err() != nil {
		return b.fail(matchCancelled)
	}
	return b.stop == matchProceed
}

func (b *matchBudget) consumeLineage(depth int) bool {
	if !b.checkActive() {
		return false
	}
	if depth >= maxActLineageDepth || b.lineageSteps >= b.lineageLimit {
		return b.fail(matchBudgetExhausted)
	}
	b.lineageSteps++
	return true
}

func (b *matchBudget) terminalState() actProbeState {
	if b == nil {
		return actProbeMalformed
	}
	switch b.stop {
	case matchCancelled:
		return actProbeCancelled
	case matchBudgetExhausted:
		return actProbeBudgetExhausted
	case matchMalformed:
		return actProbeMalformed
	default:
		return actProbeNone
	}
}

type lineageAtTier struct {
	entries []lineageEntry
	ok      bool
}

// 每个 Tier 新建一个 matcher。缓存只保存本轮真正比较过的节点，
// 不预先为 original/fresh 全树计算 L1/L2。
type tierMatcher struct {
	ctx      context.Context
	original *device.Capture
	fresh    *device.Capture
	tier     int
	screen   device.Rect
	budget   matchBudget

	oldLines map[int]lineageAtTier
	newLines map[int]lineageAtTier
}

func newTierMatcher(ctx context.Context,
	original, fresh *device.Capture, tier int) *tierMatcher {
	// 定向法每个 Tier 最多比较 len(fresh)+len(original) 个节点对。
	candidateLimit := len(fresh.Nodes) + len(original.Nodes)
	return &tierMatcher{
		ctx: ctx, original: original, fresh: fresh, tier: tier,
		screen: fresh.Display.Bounds(),
		budget: matchBudget{
			ctx: ctx, candidateLimit: candidateLimit,
			lineageLimit: maxActLineageSteps,
		},
		oldLines: make(map[int]lineageAtTier),
		newLines: make(map[int]lineageAtTier),
	}
}

func (m *tierMatcher) lineage(capture *device.Capture,
	memo map[int]lineageAtTier, index int) (lineageAtTier, bool) {
	if cached, ok := memo[index]; ok {
		return cached, cached.ok
	}
	var cached lineageAtTier
	if m.tier == 3 {
		cached.entries, cached.ok = lineageL2(capture, index, &m.budget)
	} else {
		cached.entries, cached.ok = lineageL1(capture, index, &m.budget)
	}
	memo[index] = cached
	return cached, cached.ok
}

func (m *tierMatcher) sameLineageKeysBudgeted(
	a, b []lineageEntry) (equal, complete bool) {
	if len(a) != len(b) {
		return false, true
	}
	for i := range a {
		if !m.budget.consumeLineage(0) {
			return false, false
		}
		if a[i].key != b[i].key {
			return false, true
		}
	}
	return true, true
}

func (m *tierMatcher) hasStrongNonRootAnchorBudgeted(
	chain []lineageEntry) (found, complete bool) {
	for i := range chain {
		if !m.budget.consumeLineage(0) {
			return false, false
		}
		if !chain[i].root && strongLineageAnchor(chain[i]) {
			return true, true
		}
	}
	return false, true
}

// edgeAtTier 判断单条旧->新候选关系。
// complete=false 表示取消、畸形树或预算耗尽；具体原因保留在 budget.stop。
func (m *tierMatcher) edgeAtTier(oldIndex, newIndex int) (matched, complete bool) {
	if !m.budget.consumeCandidate() {
		return false, false
	}
	oldNode := &m.original.Nodes[oldIndex]
	newNode := &m.fresh.Nodes[newIndex]
	if oldNode.DisplayID != newNode.DisplayID {
		return false, true
	}
	compatible, _ := sameStructure(oldNode, newNode)
	if !compatible {
		return false, true
	}

	// 先做不依赖 lineage 的便宜检查，避免为明显失配节点构造祖先链。
	switch m.tier {
	case 1:
		if oldNode.Bounds != newNode.Bounds {
			return false, true
		}
	case 2:
		if !geometryCompatible(
			oldNode.Bounds, newNode.Bounds, m.screen, actTier2MinOverlap) {
			return false, true
		}
	case 3:
		if !geometryCompatible(
			oldNode.Bounds, newNode.Bounds, m.screen, actTier3MinOverlap) {
			return false, true
		}
	default:
		return false, m.budget.failMalformed()
	}

	oldLineage, ok := m.lineage(m.original, m.oldLines, oldIndex)
	if !ok {
		return false, false
	}
	newLineage, ok := m.lineage(m.fresh, m.newLines, newIndex)
	if !ok {
		return false, false
	}
	equal, complete := m.sameLineageKeysBudgeted(
		newLineage.entries, oldLineage.entries)
	if !complete {
		return false, false
	}
	if !equal {
		return false, true
	}

	if m.tier == 3 {
		// Tier 3 到这里才检查 L2 非根 anchor，全程不读取 L1。
		found, complete := m.hasStrongNonRootAnchorBudgeted(newLineage.entries)
		if !complete {
			return false, false
		}
		return found, true
	}
	return true, true
}

func stableNonEmptyContent(aSet bool, a string, bSet bool, b string) bool {
	return aSet && bSet && a != "" && a == b
}

func knownUnprotected(a, b *device.RawNode) bool {
	// 非编辑节点的 password 属性与内容身份无关；编辑节点则必须两侧都
	// 明确报告 non-password，未知不能被当成“未受保护”。
	if a.Editable || b.Editable {
		return a.HasPassword && !a.Password &&
			b.HasPassword && !b.Password
	}
	return true
}

func sameTargetResource(node, want *device.RawNode, resourceID string) bool {
	return resourceID != "" && node.DisplayID == want.DisplayID &&
		node.Package == want.Package && node.ResourceSet &&
		node.ResourceID == resourceID
}

// 独立目标内容身份仅取 target 自有、旧/新一致的非空内容，或两树唯一资源。
// 共享祖先属于列表/页面上下文，不能为其中某一个 target 的载体解绑作证。
func (m *tierMatcher) independentTargetContent(
    oldIndex, newIndex int, oldResourceCount, newResourceCount int) (bool, bool) {
    want := &m.original.Nodes[oldIndex]
    candidate := &m.fresh.Nodes[newIndex]
    sameText := knownUnprotected(want, candidate) &&
        stableNonEmptyContent(want.TextSet, want.Text, candidate.TextSet, candidate.Text)
    sameDesc := knownUnprotected(want, candidate) &&
        stableNonEmptyContent(want.DescSet, want.Description, candidate.DescSet, candidate.Description)
    uniqueResource := want.Package != "" && want.ResourceSet &&
        want.ResourceID != "" && oldResourceCount == 1 && newResourceCount == 1
    return sameText || sameDesc || uniqueResource, true
}

// probeWant 严格做两次定向扫描，不保存 outgoing/incoming 列表。
func (m *tierMatcher) probeWant(wantIndex int) actProbeResult {
	want := &m.original.Nodes[wantIndex]
	resourceID := ""
	if want.ResourceSet {
		resourceID = want.ResourceID
	}
	newResourceCount := 0
	candidate := -1
	for newIndex := range m.fresh.Nodes {
		if sameTargetResource(&m.fresh.Nodes[newIndex], want, resourceID) {
			newResourceCount++
		}
		matched, complete := m.edgeAtTier(wantIndex, newIndex)
		if !complete {
			return actProbeResult{state: m.budget.terminalState(), candidateIndex: -1}
		}
		if matched {
			if candidate >= 0 {
				return actProbeResult{state: actProbeAmbiguous, candidateIndex: -1}
			}
			candidate = newIndex
		}
	}
	if candidate < 0 {
		return actProbeResult{state: actProbeNone, candidateIndex: -1}
	}

	oldResourceCount := 0
	incomingCount := 0
	soleOldIndex := -1
	for oldIndex := range m.original.Nodes {
		if sameTargetResource(&m.original.Nodes[oldIndex], want, resourceID) {
			oldResourceCount++
		}
		matched, complete := m.edgeAtTier(oldIndex, candidate)
		if !complete {
			return actProbeResult{state: m.budget.terminalState(), candidateIndex: -1}
		}
		if matched {
			incomingCount++
			soleOldIndex = oldIndex
			if incomingCount > 1 {
				return actProbeResult{state: actProbeAmbiguous, candidateIndex: -1}
			}
		}
	}
	if incomingCount == 0 || soleOldIndex != wantIndex {
		m.budget.failMalformed()
		return actProbeResult{state: actProbeMalformed, candidateIndex: -1}
	}
	if !m.budget.checkActive() {
		return actProbeResult{state: m.budget.terminalState(), candidateIndex: -1}
	}
	independent, complete := m.independentTargetContent(
		wantIndex, candidate, oldResourceCount, newResourceCount)
	if !complete {
		return actProbeResult{state: m.budget.terminalState(), candidateIndex: -1}
	}
	return actProbeResult{
		candidateIndex: candidate, independentContent: independent,
		state: actProbeUnique,
	}
}

// matchActTarget 只供已认领的 ref 动作目标或已明确授权的滚动容器使用。
// revoked 只在 Tier 边界调用，所有搜索和 lineage 工作都在状态锁外。
func matchActTarget(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode) (actMatch, actProbeState) {
	if ctx == nil || original == nil || fresh == nil || want == nil {
		return actMatch{}, actProbeMalformed
	}
	originalIndex := indexOf(original, want)
	if originalIndex < 0 {
		return actMatch{}, actProbeMalformed
	}

	for tier := 1; tier <= 3; tier++ {
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{}, actProbeCancelled
		}
		matcher := newTierMatcher(ctx, original, fresh, tier)
		probe := matcher.probeWant(originalIndex)
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{}, actProbeCancelled
		}
		switch probe.state {
		case actProbeNone:
			continue
		case actProbeUnique:
			return actMatch{
				raw: &fresh.Nodes[probe.candidateIndex], tier: tier,
				independentContent: probe.independentContent,
			}, actProbeUnique
		case actProbeAmbiguous:
			return actMatch{}, actProbeAmbiguous
		case actProbeCancelled:
			return actMatch{}, actProbeCancelled
		case actProbeBudgetExhausted:
			return actMatch{}, actProbeBudgetExhausted
		default:
			return actMatch{}, actProbeMalformed
		}
	}
	return actMatch{}, actProbeNone
}
```

实现细节补充：

- `lineageL2` 的最终实现必须让 `lineageEntry` 携带 raw parent index，直接调用 `transparentWrapper`；上面的示意不能根据 `structureKey` 猜父节点。
- `sameLineageKeysBudgeted` 比较的只是 `structureKey`，不比较 Bounds、Focused 和 Selected；该比较和 Tier 3 的非根 anchor 扫描都逐项消耗 lineage 预算。
- Tier 1、2、3 都要求 `sameStructure` 没有已知冲突。未知-未知字段不贡献身份证据，也不会被补成默认值。
- Tier 1/2 使用 L1。Tier 3 独立使用 L2 + 非根 anchor，不再前置比较 L1；因此 L1 因透明 wrapper 增删而不同、L2 仍相同时可以成功。
- L2 序列必须至少有一个非根边界。只有根和透明 wrapper 时，Tier 3 不成立。
- 多候选时不回退到 Tier 4，也不进入下一 Tier。
- Tier 4 不进入 `matchActTarget` 的默认循环。

通用候选关系必须在每个 Tier 上同时满足：

1. 目标旧节点在该 Tier 恰好有一条 outgoing edge；
2. 目标 fresh 节点在该 Tier 恰好有一条 incoming edge，且旧端就是目标旧节点。

这防止两个同身份旧节点虽然各自只看到一个 fresh 候选，却同时申请同一个 fresh 节点。每个 Tier 的第一轮最多访问 fresh `Nc` 次，第二轮最多访问 original `Np` 次，因此候选比较为 `O(Np+Nc)`；lineage 构造和比较为 `O((Np+Nc)*D)`，`D` 是实际访问到的祖先链长度。实现可按 DisplayID/Package/Class 建确定性签名桶，但不得依赖 map 遍历顺序；lineage 只对通过便宜筛选并真正参与比较的节点做惰性缓存。visited map、lineage slice 和 memo backing store 的 allocation count/bytes 统一计数，且 visited 创建点对应已计费的 lineage step；不声称总分配恒定。目标和点击载体各调用一次 matcher，最坏是两次有界线性扫描，而不是两幅 20k 节点树的笛卡尔积。任何 `ctx` 取消、撤权、预算耗尽、畸形祖先链或不完整扫描都必须立即失败并保留终止类别：cancelled 映射既有 `CodeStopped`，budget/malformed 映射既有 `CodeHierarchyFailed`；target/scroll 的 none/ambiguous 映射 `target_changed`，carrier 的 none/ambiguous 仅在独立目标身份下允许安全解绑。不能发布部分映射或尝试下一 Tier。

### 3.1 Probe/Capture 与 matcher 的 STOP 边界

目标、carrier 和 scroll 共用以下调用顺序。Probe/Capture 返回后先检查撤权，再解释设备错误：

```go
func (e *Engine) captureForActRevalidation(work *workState,
	obs *observationState) (*device.Capture, *sessionState, *protocol.APIError) {
	if work == nil || work.Ctx == nil || e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	session := e.sessionForWork(work)
	if session == nil {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}

	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if err != nil || !state.Exists {
		return nil, nil, protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
	}

	capture, err := e.dev.Capture(work.Ctx, session.Display)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "实时目标重验失败")
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	return &capture, session, nil
}
```

- revalidateActTarget 的 ProbeDisplay/Capture 前缀改为上述顺序，再把 capture 交给 target/carrier matcher；scroll 使用相同检查顺序并共用 matcher 状态映射。
- probeActNode 在进入 matcher 前、matcher 每个 Tier 结束和返回调用层时检查 e.workRevoked(work) 与 work.Ctx.Err()。STOP 已到即使第一轮已有唯一候选也不得返回 unique。
- Probe 调用返回 err/!Exists 时，撤权优先映射既有 CodeStopped/control_revoked；未撤权才映射既有 CodeDisplayUnavailable。
- Capture 返回错误时，撤权优先映射 CodeStopped；未撤权才映射既有 CodeHierarchyFailed/hierarchy_unavailable。
- matcher 的 budget/malformed 使用同一 CodeHierarchyFailed；target/scroll 的 none/ambiguous 映射 target_changed，carrier 的 none/ambiguous 只可在独立目标身份下安全解绑。本提案不新增错误码或 recovery cause。
- 上述检查覆盖 Probe/Capture 之后的 topology、foreground、Preview、source mapping、scope 构造和点选择终止分支。调用层在把任一非撤权错误写入结果前再做一次撤权复核；同一线性化点上撤权优先于业务拒绝。
- 以上检查均在普通工作状态锁外；workRevoked 自身取得的短状态锁只读取撤权事实。

### 3.2 Tier 4 的未决取舍

Tier 4 默认关闭。实现前必须由用户或明确的契约负责人决定以下路线之一：

1. **移除 Tier 4**：动态文案变化一律要求新观察。最安全，建议作为默认。
2. **受限转换**：只有稳定非空 ResourceID/Description、同前台 task 上下文、同能力和同 L1，并且文本差异属于预先批准的封闭格式集合时才匹配。普通动词、危险词、数值外的任意文案不得进入。

若选择第 2 条，至少还要明确：

- 谁维护允许的转换集合；
- 是否只允许纯 ASCII 数字/日期 token 变化；
- 当转换结果无法解析时是否一律拒绝；
- 是否提供独立指标统计“仅 Tier 4 导致成功”的次数；
- 哪些动作必须永久禁用 Tier 4。

在用户没有明确决定前，实现必须停在 Tier 3。这个未决项不能阻塞 Tier 1–3，也不能作为实现者自行放宽的开关。

## 4. 点击与长按

### 4.1 分场景授权

| 输入 | 身份证明 | 落点授权 |
| --- | --- | --- |
| `tap/long_press` + `target.ref` | `matchActTarget`，唯一 Tier 1–3 | 原 carrier 安全时优先沿用；失效时仅在有独立强内容身份后，于同一目标子树内保留原点或重选安全 carrier/点；不得找祖先或穿 blocker |
| `tap/long_press` + point | 保留 `provePointTarget` 的原始点解析和来源比较 | 原 `(x,y)` 固定，不重定位 |
| `text` + `target.ref` | `matchActTarget`，并保留文本能力/焦点门禁 | 焦点 tap 使用 `tapFrame` 中 fresh 目标的已证明点；写入及 readback 目标由焦点绑定确认并使用新 bounds |
| `text` + point | 原 point proof | 原 `(x,y)` 固定；无 ref anchor 时不能启用 shifted |
| raw `swipe` | 无节点替换 | 原 from/to 固定，永不重算或滚动语义化 |
| `scroll` | 独立的容器匹配 + 容器能力 | 同容器内可重算整条安全轨迹 |

不要把 `matchActTarget` 直接替换掉所有 `matchFreshNode` 调用。共享调用还包括坐标 proof 和滚动容器，必须由调用点显式选择策略。

### 4.2 点击重验伪代码

```go
type tapFrame struct {
	capture  *device.Capture
	raw      *device.RawNode
	x, y     int
	carrier  *device.RawNode
	tier     int
	strategy string
}

// pointOwner 只解释已经由 provesSubtreeTapPoint 证明的点。
// target 自身区域允许 carrier=nil；其它命中必须是该 scope 中的安全 carrier。
func pointOwner(scope *tapScope, x, y int) (*tapCarrier, bool) {
	if scope == nil || !provesSubtreeTapPoint(scope, x, y) {
		return nil, false
	}
	hit, state := resolvePoint(scope.target, x, y)
	if state != pointUnique || hit == nil {
		return nil, false
	}
	if hit == scope.target {
		return nil, true
	}
	carrier := scope.carrierByNode[hit]
	return carrier, carrier != nil
}

func pointHitsFixedCarrier(scope *tapScope, x, y int,
	fixedCarrier *tapCarrier) bool {
	if scope == nil || fixedCarrier == nil ||
		!provesSubtreeTapPoint(scope, x, y) {
		return false
	}
	hit, state := resolvePoint(scope.target, x, y)
	if state != pointUnique || hit == nil {
		return false
	}
	return scope.carrierByNode[hit] == fixedCarrier
}

// fixedCarrierSafePoint 只重算原 carrier 内的点，不改变载体身份。
func fixedCarrierSafePoint(scope *tapScope, targetRaw *device.RawNode,
	fixedCarrier *tapCarrier) (int, int, string, bool) {
	if scope == nil || fixedCarrier == nil {
		return 0, 0, "", false
	}
	for _, candidate := range enumerateTapCandidates(scope, targetRaw) {
		if candidate.carrier != fixedCarrier {
			continue
		}
		if pointHitsFixedCarrier(scope, candidate.x, candidate.y, fixedCarrier) {
			return candidate.x, candidate.y, candidate.strategy, true
		}
	}
	return 0, 0, "", false
}

// chooseUnboundTapPlan 只在同一个 collectTapScope 结果中选点。
// 先保留原静态点；否则 chooseTapPlan 可从 target 自身区域或另一安全后代选点。
// 所有候选仍经过 subtree blocker 证明，不允许上溯祖先。
func chooseUnboundTapPlan(scope *tapScope, input *tapInput,
	target *protocol.Node, action protocol.Action,
	preferX, preferY int) (*tapPlan, *protocol.APIError) {
	if carrier, ok := pointOwner(scope, preferX, preferY); ok {
		return &tapPlan{
			x: preferX, y: preferY, strategy: inputPlanStrategyForPoint(scope, carrier),
			action: string(action), targetRef: target.Ref, targetRaw: input.targetRaw,
			carrierRef: carrierRef(carrier), carrierRaw: carrierRaw(carrier),
		}, nil
	}
	return chooseTapPlan(scope, input, target, action, time.Now())
}

func inputPlanStrategyForPoint(scope *tapScope, carrier *tapCarrier) string {
	if carrier != nil {
		return tapModeCarrierNode
	}
	return tapModeDefaultCenter
}

func carrierRef(carrier *tapCarrier) string {
	if carrier == nil || carrier.node == nil {
		return ""
	}
	return carrier.node.Ref
}

func carrierRaw(carrier *tapCarrier) *device.RawNode {
	if carrier == nil {
		return nil
	}
	return carrier.raw
}

func stoppedActRevalidation() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
}

// probeActNode 在 matcher 的每个 Tier 边界和调用返回处重新检查撤权。
// none/ambiguous 由场景解释：target/scroll 拒绝，carrier 可在独立身份下安全解绑。
func (e *Engine) probeActNode(work *workState, original, fresh *device.Capture,
	want *device.RawNode) (actMatch, actProbeState) {
	revoked := func() bool { return e.workRevoked(work) }
	if work == nil || work.Ctx == nil || work.Ctx.Err() != nil || revoked() {
		return actMatch{}, actProbeCancelled
	}
	match, state := matchActTarget(work.Ctx, revoked, original, fresh, want)
	if work.Ctx.Err() != nil || revoked() {
		return actMatch{}, actProbeCancelled
	}
	return match, state
}

func (e *Engine) revalidateTapTarget(work *workState, obs *observationState,
	fresh *device.Capture, plan *tapPlan) (*tapFrame, *protocol.APIError) {
	if work == nil || work.Ctx == nil || fresh == nil || obs == nil ||
		obs.Projection == nil || obs.Projection.Capture == nil ||
		plan == nil || plan.targetRaw == nil {
		return nil, tapHierarchyError()
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, stoppedActRevalidation()
	}

	topology, apiErr := validateTapRawTopology(fresh)
	if apiErr != nil {
		return nil, apiErr
	}

	match, matchState := e.probeActNode(
		work, obs.Projection.Capture, fresh, plan.targetRaw)
	switch matchState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, tapTargetChanged()
	default:
		return nil, tapHierarchyError()
	}
	targetRaw := match.raw

	projector := work.Projector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return nil, tapHierarchyError()
	}

	// Preview 只是纯 CPU 投影，不推进 O/ref 连续性。
	projection, err := projector.Preview(fresh)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, stoppedActRevalidation()
	}
	if err != nil || projection == nil || projection.Capture == nil ||
		projection.Tree == nil || projection.Tree.Root == nil || projection.Source == nil {
		return nil, tapHierarchyError()
	}
	view, apiErr := buildTapPublicView(projection.Tree, fresh, projection.Source)
	if apiErr != nil {
		return nil, apiErr
	}

	targetIndex := indexOf(fresh, targetRaw)
	if targetIndex < 0 {
		return nil, tapHierarchyError()
	}
	switch checkTapSourceMapping(view, topology, targetIndex) {
	case tapSourceTargetMissing:
		return nil, tapTargetChanged()
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	targetPublic := view.byRaw[targetIndex]
	if !validTapPublicTarget(targetPublic, protocol.Action(plan.action)) {
		return nil, tapTargetChanged()
	}

	input := &tapInput{
		view: view, capture: fresh, topology: topology,
		target: targetPublic, targetRaw: targetRaw, targetIndex: targetIndex,
	}
	scope, apiErr := collectTapScope(input)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil {
		if apiErr.Code == protocol.CodeHierarchyFailed {
			return nil, apiErr
		}
		return nil, tapTargetChanged()
	}

	originalPointCarrier, originalPointOK := pointOwner(scope, plan.x, plan.y)

	var fixedCarrier *tapCarrier
	if plan.carrierRaw != nil {
		carrierMatch, carrierState := e.probeActNode(
			work, obs.Projection.Capture, fresh, plan.carrierRaw)
		switch carrierState {
		case actProbeUnique:
			carrierIndex := indexOf(fresh, carrierMatch.raw)
			if carrierIndex < 0 {
				return nil, tapHierarchyError()
			}
			if carrierIndex != targetIndex &&
				topology.isAncestor(targetIndex, carrierIndex) {
				if carrierPublic := view.byRaw[carrierIndex]; carrierPublic != nil &&
					!carrierPublic.Context && carrierPublic.Role != protocol.RoleRoot &&
					carrierPublic.Bounds != nil {
					fixedCarrier = scope.carrierByRef[carrierPublic.Ref]
				}
			}
		case actProbeCancelled:
			return nil, stoppedActRevalidation()
		case actProbeBudgetExhausted, actProbeMalformed:
			return nil, tapHierarchyError()
		case actProbeNone, actProbeAmbiguous:
			// carrier 失配不直接决定 target；有独立目标身份时走安全解绑。
		default:
			return nil, tapHierarchyError()
		}
	}

	// 原静态点在新 target scope 仍可证明时优先保留，即使 carrier 已换成另一个安全节点。
	// 原 carrier 已失效时必须先有独立目标内容身份。
	if originalPointOK &&
		(plan.carrierRaw == nil || fixedCarrier != nil || match.independentContent) {
		return &tapFrame{
			capture: fresh, raw: targetRaw, x: plan.x, y: plan.y,
			carrier: carrierRaw(originalPointCarrier), tier: match.tier,
			strategy: inputPlanStrategyForPoint(scope, originalPointCarrier),
		}, nil
	}

	// 原点失效但原 carrier 仍安全时，优先在同一 carrier 内重算。
	if fixedCarrier != nil {
		if x, y, strategy, ok := fixedCarrierSafePoint(
			scope, targetRaw, fixedCarrier); ok {
			return &tapFrame{
				capture: fresh, raw: targetRaw, x: x, y: y,
				carrier: fixedCarrier.raw, tier: match.tier, strategy: strategy,
			}, nil
		}
	}

	// 运行到这里说明原 carrier 不存在、已失效，或无法再提供可证明点。
	// 原 plan 依赖 carrier 时，安全解绑必须由独立 target 内容身份支持。
	if plan.carrierRaw != nil && !match.independentContent {
		return nil, tapTargetChanged()
	}
	unbound, apiErr := chooseUnboundTapPlan(
		scope, input, targetPublic, protocol.Action(plan.action), plan.x, plan.y)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil || unbound == nil || unbound.strategy == tapModeRejected {
		return nil, tapTargetChanged()
	}
	if !provesSubtreeTapPoint(scope, unbound.x, unbound.y) {
		return nil, tapTargetChanged()
	}
	return &tapFrame{
		capture: fresh, raw: targetRaw, x: unbound.x, y: unbound.y,
		carrier: unbound.carrierRaw, tier: match.tier, strategy: unbound.strategy,
	}, nil
}
```

规则说明：

- 原静态点在 fresh target scope 内仍可证明时优先保留；原点失效后，原 carrier 仍唯一匹配且是 target 后代安全载体时优先沿用，并在同一 carrier 内重算。
- carrier 失配/歧义/消失/不再安全时，target 已双向唯一且公开 action 有效仍不足以解绑；还需 target 自身稳定非空 text/desc，或两树唯一的 resource。列表/页面祖先锚点不能替代 target 自有身份。编辑节点的 text/desc 身份要求旧、新两侧 `HasPassword` 都明确且为 false；unknown 不能作为该身份证据。
- 解绑后只重新使用当前 target 的 `collectTapScope`。先保留 fresh 中仍可证明的原静态点，否则通过 `chooseTapPlan` 使用 target 自身区域或另一安全后代。新 carrier、点和 strategy 必须进入 `tapFrame` runtime。
- 不得上溯 target 祖先，不得穿过 blocker，不得从 target scope 外选点。
- carrier 是原计划唯一目标身份、且 target 没有独立内容身份时拒绝。
- matcher 的 cancelled 映射既有 `CodeStopped`/`control_revoked`，budget/malformed 映射既有 `CodeHierarchyFailed`/`hierarchy_unavailable`。target/scroll 的 none/ambiguous 映射 `target_changed`；carrier 的 none/ambiguous 只有在独立目标身份成立时安全解绑。
- `provesSubtreeTapPoint` 只证明 subtree 内无 blocker。它不证明全屏 z-order；本提案不扩大这个断言。

**载体解绑的能力边界**：本稿不把共享祖先或仅后代内容当作 target 的独立强身份。
匿名 target 若只能通过原 carrier 区分，carrier 丢失后仍拒绝；target 自有稳定内容或唯一
resource 已足以保留 item_42 的 A→B 安全解绑。若只有其它 descendant anchor 可区分卡片，
本稿仍保留原 carrier 验证，不直接复用分册 02 的阅读 ref 当执行权威。要扩大该边界，需
另行定义并审核旧/新同 target 子树、排除原 carrier 子树的双向唯一锚点及预算/source/owner
证明，不能由实现者用 fresh 单侧内容或共享列表 ID 临时补证。

### 4.3 实际 runtime 消费

当前 [`dispatchAct`](../../../../internal/engine/act.go#L615) 对 ref tap/long_press 读取 `tap.x/tap.y`。实施时应改为显式接收 `tapFrame`，或确保同一个可变 plan 实例被 dispatch 消费；`text` 分支也必须把该 frame 传给 focus tap，不能因为转到 `dispatchText` 就退回 raw center。

推荐不可变接口：

```go
frame, apiErr := e.revalidateTapTarget(work, obs, fresh, plan)
if apiErr != nil { ... } // 不派发
outcome, apiErr := e.dispatchAct(work, obs, operation, fresh, raw, frame)
```

`dispatchAct` 对 ref tap/long_press/text 的焦点 tap 必须使用 `frame.x/frame.y`。`text` 的 `Get/SetTextOnDisplay` 使用焦点绑定后的 fresh bounds；坐标 text 仍使用原坐标。不得重新读取静态 `operation.Target.X/Y`，也不得在 dispatch 内再次调用 planning。

## 5. 滚动容器与动态轨迹

### 5.1 容器证明

`scroll` 先唯一匹配容器，再证明轨迹。两件事不能互换：

1. `matchActTarget` 必须恰好产生一个容器候选；
2. fresh 公开节点还必须满足 `isScrollContainer`，即 role、上下文、bounds、swipe action 和已知 enabled 条件；
3. 新旧容器必须有实质轴向连续性和稳定离轴边缘；
4. 只有容器成立后才调用 `scrollSafeSpans` 生成安全区间；cancelled/budget/malformed 不算 none，不能进入下一 Tier。

仅比较轴区间重叠不够。例如纵向列表 A 从屏幕左侧移到右侧、class/text 完全相同，轴向区间仍重叠，但实际已经换了列或容器。

建议的离轴容差：

```go
const scrollCrossAxisTolerance = max(8, min(containerWidth, containerHeight)/50)
```

具体 8px/2% 是待确认取舍。无论数值如何，“离轴边缘必须稳定或落在明确小容差内”不能删除。

### 5.2 重验伪代码

```go
func intervalOverlapRatio(a, b scrollSpan) float64 {
	if a.length() <= 0 || b.length() <= 0 {
		return 0
	}
	overlap := min(a.end, b.end) - max(a.start, b.start)
	if overlap <= 0 {
		return 0
	}
	return float64(overlap) / float64(min(a.length(), b.length()))
}

func sameScrollScope(oldRect, freshRect, oldScreen, freshScreen device.Rect,
	direction string) bool {
	if oldRect.Empty() || freshRect.Empty() {
		return false
	}
	oldClipped := oldRect.Intersect(oldScreen)
	freshClipped := freshRect.Intersect(freshScreen)
	if oldClipped.Empty() || freshClipped.Empty() {
		return false
	}

	switch direction {
	case protocol.ScrollDirectionUp, protocol.ScrollDirectionDown:
		oldAxis := scrollSpan{oldClipped.Top, oldClipped.Bottom}
		freshAxis := scrollSpan{freshClipped.Top, freshClipped.Bottom}
		if intervalOverlapRatio(oldAxis, freshAxis) < 0.5 {
			return false
		}
		tol := max(8, min(freshClipped.Width(), oldClipped.Width())/50)
		return abs(oldClipped.Left-freshClipped.Left) <= tol &&
			abs(oldClipped.Right-freshClipped.Right) <= tol

	case protocol.ScrollDirectionLeft, protocol.ScrollDirectionRight:
		oldAxis := scrollSpan{oldClipped.Left, oldClipped.Right}
		freshAxis := scrollSpan{freshClipped.Left, freshClipped.Right}
		if intervalOverlapRatio(oldAxis, freshAxis) < 0.5 {
			return false
		}
		tol := max(8, min(freshClipped.Height(), oldClipped.Height())/50)
		return abs(oldClipped.Top-freshClipped.Top) <= tol &&
			abs(oldClipped.Bottom-freshClipped.Bottom) <= tol
	}
	return false
}

func (e *Engine) revalidateScrollTarget(work *workState,
	obs *observationState, plan *scrollPlan) (*device.Capture, *scrollFrame, *protocol.APIError) {
	if obs == nil || obs.Projection == nil || obs.Projection.Capture == nil ||
		plan == nil || plan.container == nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}
	if work == nil || work.Ctx == nil || e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	session := e.sessionForWork(work)
	if session == nil {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}

	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if err != nil || !state.Exists {
		return nil, nil, protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
	}

	capture, err := e.dev.Capture(work.Ctx, session.Display)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "实时目标重验失败")
	}
	if err := foregroundMatches(obs.Foreground, capture.Foreground); err != nil {
		return &capture, nil, err
	}

	match, matchState := e.probeActNode(
		work, obs.Projection.Capture, &capture, plan.container)
	switch matchState {
	case actProbeUnique:
	case actProbeCancelled:
		return &capture, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	case actProbeBudgetExhausted, actProbeMalformed:
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	case actProbeNone, actProbeAmbiguous:
		return &capture, nil, targetChangedScroll()
	default:
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}
	freshContainer := match.raw
	freshIndex := indexOf(&capture, freshContainer)
	if freshIndex < 0 {
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}

	oldScreen := obs.Projection.Capture.Display.Bounds()
	newScreen := capture.Display.Bounds()
	newClipped := freshContainer.Bounds.Intersect(newScreen)
	if !sameScrollScope(plan.container.Bounds, freshContainer.Bounds,
		oldScreen, newScreen, plan.direction) {
		return &capture, nil, targetChangedScroll()
	}

	projector := work.Projector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"公开树构造失败")
	}
	projection, err := projector.Preview(&capture)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return &capture, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if err != nil || projection == nil || projection.Tree == nil ||
		projection.Capture == nil {
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"公开树构造失败")
	}

	containerPublic := publicNodeByRawIndex(projection, freshIndex)
	if containerPublic == nil || containerPublic.Bounds == nil ||
		!isScrollContainer(containerPublic) {
		return &capture, nil, targetChangedScroll()
	}

	space, ok := scrollSafeSpans(projection, freshIndex, newClipped, plan.direction)
	if !ok {
		return &capture, nil, targetChangedScroll()
	}

	start, end := plan.start, plan.end
	if !space.provesSegment(start, end, newClipped, newScreen) {
		span, hasSpan := space.longestSpan(plan.direction)
		if !hasSpan {
			return &capture, nil, targetChangedScroll()
		}
		safe := space.safeRect(newClipped, span)
		var geometryOK bool
		start, end, geometryOK = scrollGeometry(
			safe, newScreen, plan.direction, plan.amount)
		if !geometryOK || !space.provesSegment(start, end, newClipped, newScreen) {
			return &capture, nil, targetChangedScroll()
		}
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return &capture, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}

	// frame 是 runtime 的唯一轨迹输入；不修改仅存在于本次栈上的 plan 副本。
	return &capture, &scrollFrame{
		capture: &capture,
		projection: projection,
		container: freshContainer,
		containerIndex: freshIndex,
		direction: plan.direction,
		amount: plan.amount,
		start: scrollPoint{X: start.X, Y: start.Y},
		end:   scrollPoint{X: end.X, Y: end.Y},
	}, nil
}
```

[`dispatchScroll`](../../../../internal/engine/scroll.go#L716) 必须使用返回 frame 的 `start/end`。测试应在假设备的 `Swipe` 调用参数上断言新值，不能只断言 `revalidateScrollTarget` 返回成功。

### 5.3 最大安全区间的边界

`longestSpan` 在容器身份已经唯一后，是原滚动规划器既有的确定性区间选择，不是节点候选匹配的 tie-break。它不能用于在多个同身份容器之间选择，也不能在多个 Tier 候选之间决胜。

重算后仍必须满足：

- 起终点在 screen、clipped raw rectangle、public container 内；
- 整条闭合 segment 落在同一个 provable span；
- 方向与 amount 保持不变；
- 不扩成 raw swipe 的任意语义。

本提案不证明第三方 overlay、全局 z-order 或所有 touch consumer。它只证明现有公开树和容器中心线能够看到的冲突。

## 6. 文字输入的焦点绑定

### 6.1 保留的硬门禁

旧 `want` 和候选 fresh 节点都必须满足以下来源门禁；旧目标只额外允许尚未获焦：

- `Visible`；
- `Editable`；
- `HasEnabled && Enabled`；
- `HasPassword && !Password`。

候选 fresh 节点还必须满足 `HasFocused && Focused`。任一 presence 缺失都拒绝。不能把 unknown 当 false 或 true。

`RawNode.Editable` 是源投影中的类名启发，不是另一个 `HasEditable` presence 位。因此绑定之后仍必须执行现有 display-bound `Get/SetTextOnDisplay`，并保持 `CandidateCount == 1` 的最终设备证明。

### 6.2 身份锚点与 task 上下文

shifted 只适用于 ref 输入目标，并且 `want` 必须有稳定非空 anchor：

- `ResourceSet && ResourceID != ""`；或
- `DescSet && Description != ""`。

anchor key 至少包含 `Package + ClassName + anchor kind + anchor value`。要求：

- anchor 在旧帧中唯一；
- anchor 在 fresh 帧中唯一；
- package/class 以及 Resource/Description 的 presence 和值必须与旧目标相同；可编辑输入的身份不接受 password presence 未知；
- 唯一性统计不能先过滤“已获焦”，否则两个同 anchor 字段中恰好一个获焦会被误当唯一；
- fresh 的 `Foreground` 为 present，Package 相同，TaskID 已知且与最初重验相同；
- DisplayID 相同；
- 只放宽纵向位置；
- 宽度、高度在小容差内稳定；
- 左右边缘分别落在明确的小容差内，不能用 80% 重叠掩盖明显的横向位移。

anchor 不包含输入框当前 `Text`/value。set 成功本身会改变该值；用可变正文做 readback 身份会错误地把正确写入判成目标变化。

TaskID 只证明前台 task 上下文，不是 window token。`RawNode` 和 `device.Capture` 都没有可比较的窗口 ID，因此本提案不能声称“已证明同一个窗口”。同一 task 内切到新页面，或观测数据在同一位置被同 package/class/anchor/几何的另一输入框替换，仍可能不可辨识；这些事实必须如实保留，不能把 TaskID 相等写成更强的窗口保证。

`RawNode.Hint` 不存在，不能加入。若未来要使用 hint，必须先单独提案修改源模型和设备采集。

### 6.3 绑定伪代码

```go
type textBindingMode string

const (
	textExact   textBindingMode = "exact"
	textNear    textBindingMode = "near"
	textShifted textBindingMode = "shifted"
)

type textBinding struct {
	raw    *device.RawNode
	mode   textBindingMode
	anchor optionalString
	anchorKind string
}

func textCandidateSafe(node *device.RawNode) bool {
	return node != nil && node.Visible &&
		node.HasFocused && node.Focused && node.Editable &&
		node.HasEnabled && node.Enabled &&
		node.HasPassword && !node.Password
}

func stableTextAnchor(raw *device.RawNode) (kind string, anchor optionalString, ok bool) {
	if raw.ResourceSet && raw.ResourceID != "" {
		return "resource", optionalString{true, raw.ResourceID}, true
	}
	if raw.DescSet && raw.Description != "" {
		return "description", optionalString{true, raw.Description}, true
	}
	return "", optionalString{}, false
}

func anchorCount(capture *device.Capture, want *device.RawNode,
	kind string, anchor optionalString) int {
	if capture == nil || capture.Display.ID != want.DisplayID {
		return 0
	}
	count := 0
	for i := range capture.Nodes {
		node := &capture.Nodes[i]
		if node.DisplayID != want.DisplayID || !node.Editable ||
			node.Package != want.Package || node.ClassName != want.ClassName {
			continue
		}
		k, a, ok := stableTextAnchor(node)
		if ok && k == kind && a == anchor {
			count++
		}
	}
	return count
}

// 所有 near/shifted 候选都保留 package/class 以及 resource/description 的
// presence 和值。当前 Text/value 可变，故意不进入 readback 身份。
func sameTextSourcePresence(want, node *device.RawNode) bool {
	if want == nil || node == nil {
		return false
	}
	if want.Package != node.Package || want.ClassName != node.ClassName {
		return false
	}
	if want.ResourceSet != node.ResourceSet ||
		(want.ResourceSet && want.ResourceID != node.ResourceID) {
		return false
	}
	if want.DescSet != node.DescSet ||
		(want.DescSet && want.Description != node.Description) {
		return false
	}
	return true
}

// 统计时不看 focus/enabled/password，只判断同一来源 presence 和原点击几何。
// 唯一获焦不能把两个同来源编辑框缩成一个候选。
func noAnchorNearCount(capture *device.Capture, want *device.RawNode,
	tapX, tapY int) int {
	if capture == nil || capture.Display.ID != want.DisplayID {
		return 0
	}
	count := 0
	for i := range capture.Nodes {
		node := &capture.Nodes[i]
		if node.DisplayID != want.DisplayID || !node.Editable ||
		!sameTextSourcePresence(want, node) {
			continue
		}
		if node.Bounds.Contains(tapX, tapY) &&
			overlapRatio(node.Bounds, want.Bounds) >= 0.8 {
			count++
		}
	}
	return count
}

func sameHorizontalBand(a, b device.Rect) bool {
	if a.Width() <= 0 || b.Width() <= 0 ||
		a.Height() <= 0 || b.Height() <= 0 {
		return false
	}
	tolX := max(8, max(a.Width(), b.Width())/50)
	return abs(a.Left-b.Left) <= tolX &&
		abs(a.Right-b.Right) <= tolX &&
		abs(a.Width()-b.Width()) <= max(8, max(a.Width(), b.Width())/5) &&
		abs(a.Height()-b.Height()) <= max(8, max(a.Height(), b.Height())/5)
}

// 只证明同前台 task 上下文。TaskID 不是 window token，RawNode 也没有
// window token，因此这里不声称已经证明同一个窗口。
func sameTextTaskContext(expected, fresh device.Foreground) bool {
	return foregroundMatches(expected, fresh) == nil &&
		expected.State == device.ForegroundPresent &&
		fresh.State == device.ForegroundPresent &&
		expected.TaskID != nil && fresh.TaskID != nil &&
		*expected.TaskID == *fresh.TaskID
}

// original 是上一帧，fresh 是 tap 后或 readback 帧。
// expected 是最初动作重验已经确认的前台 task 上下文。
func bindFocusedText(original, fresh *device.Capture, want *device.RawNode,
	expected device.Foreground, tapX, tapY int) (textBinding, bool) {
	if original == nil || fresh == nil || want == nil {
		return textBinding{}, false
	}
	if !want.Visible || !want.Editable ||
		!want.HasEnabled || !want.Enabled ||
		!want.HasPassword || want.Password {
		return textBinding{}, false
	}
	if original.Display.ID != fresh.Display.ID ||
		want.DisplayID != fresh.Display.ID ||
		!sameTextTaskContext(expected, fresh.Foreground) {
		return textBinding{}, false
	}
	var exact, near []*device.RawNode
	kind, anchor, hasAnchor := stableTextAnchor(want)
	anchorUnique := false
	if hasAnchor {
		anchorUnique = anchorCount(original, want, kind, anchor) == 1 &&
			anchorCount(fresh, want, kind, anchor) == 1
		if !anchorUnique {
			return textBinding{}, false
		}
	} else if noAnchorNearCount(original, want, tapX, tapY) != 1 ||
		noAnchorNearCount(fresh, want, tapX, tapY) != 1 {
		return textBinding{}, false
	}

	for i := range fresh.Nodes {
		node := &fresh.Nodes[i]
		if node.DisplayID != want.DisplayID || !textCandidateSafe(node) ||
			!sameTextSourcePresence(want, node) {
			continue
		}

		if hasAnchor {
			k, a, ok := stableTextAnchor(node)
			if !ok || k != kind || a != anchor {
				continue
			}
			// exact 是强绑定：身份 anchor 相同且 Bounds 完全不变。
			if want.Bounds == node.Bounds {
				exact = append(exact, node)
			}
		}

		// near 只在候选仍覆盖原点击点并与旧矩形有足够交叠时成立。
		if node.Package == want.Package &&
			node.Bounds.Contains(tapX, tapY) &&
			overlapRatio(node.Bounds, want.Bounds) >= 0.8 {
			near = append(near, node)
		}
	}

	switch {
	case len(exact) == 1:
		return textBinding{raw: exact[0], mode: textExact,
			anchor: anchor, anchorKind: kind}, true
	case len(exact) > 1:
		return textBinding{}, false
	case len(near) == 1:
		return textBinding{raw: near[0], mode: textNear}, true
	case len(near) > 1:
		return textBinding{}, false
	}

	// 到这里才考虑键盘只改变纵向位置。
	if !hasAnchor {
		return textBinding{}, false
	}
	if !anchorUnique {
		return textBinding{}, false
	}

	var shifted []*device.RawNode
	for i := range fresh.Nodes {
		node := &fresh.Nodes[i]
		if node.DisplayID != want.DisplayID ||
			!textCandidateSafe(node) ||
			!sameTextSourcePresence(want, node) {
			continue
		}
		k, a, ok := stableTextAnchor(node)
		if !ok || k != kind || a != anchor ||
			!sameHorizontalBand(want.Bounds, node.Bounds) {
			continue
		}
		shifted = append(shifted, node)
	}
	if len(shifted) != 1 {
		return textBinding{}, false
	}
	return textBinding{raw: shifted[0], mode: textShifted,
		anchor: anchor, anchorKind: kind}, true
}
```

三级的职责现在互斥：

- `exact`：完整来源 presence 相同、稳定 anchor 相同且 `Bounds` 完全相等，是最高优先级的强绑定；
- `near`：完整来源 presence 相同，再要求稳定 anchor 相同，或旧目标本来无 anchor 时仍覆盖原 `(tapX,tapY)` 且与旧矩形足够交叠；anchor/来源统计在过滤 focused 前完成；
- `shifted`：只在 exact/near 都无候选后，使用完整来源 presence、稳定 anchor、稳定尺寸和水平带，仅放宽纵向位置。

目标消失后，如果另一个候选的 package/class、Resource/Description presence 或值不同，却拥有相同 anchor 或占据旧点击点，exact/near/shifted 都必须拒绝。若替换者连全部可观察来源 presence、class、anchor、geometry 都完全相同，现有 `RawNode` 数据本身不可辨识；实现不能根据“只有它获焦”把它证明成原目标，只能把该情况作为能力边界，并依赖 set/readback 的设备候选数与不确定结果如实收窄。

### 6.4 `input_text` 派发修正

当前 [`dispatchText`](../../../../internal/engine/act.go#L681) 的顺序应保留为：

1. 对 ref 目标使用 `tapFrame.x/y` 中的已证明安全焦点，对坐标目标使用原坐标；写入目标在绑定后使用 fresh bounds；
2. `beginUIDispatch("tap")`；
3. 派发 focus tap；
4. 若 STOP/revocation，立即返回 uncertain；
5. capture `postTap`，重新确认前景；
6. `bindFocusedText`；
7. 只有绑定成功才 `GetTextOnDisplay`；
8. 只有 count=1 且目标仍可确认才 `beginUIDispatch("text")`；
9. `SetTextOnDisplay`；
10. readback 时继续用同一 anchor/geometry binding，不重新 tap。

焦点 tap 已经派发后，绑定失败、前景改变、focus 切换或 readback 失败都是 `uncertain`，不能重开已结束的 tap，也不能再点一次“唯一获焦”字段。

坐标 `text` 没有 ref `want` 时只保留原有 point-based weak binding，不启用 shifted。这样不会从原坐标授权外借用另一个输入框。

## 7. O/P、STOP、Preview 与派发集成

完整集成保持以下顺序。下面展开与本议题有关的控制流；派发后的观察、effect 和 recovery 分支仍沿用当前 `Act` 对应段落，不另造 helper。

```go
func (e *Engine) Act(ctx context.Context, agent, p string,
	req *protocol.ActRequest) (finalResult *protocol.Result,
	finalErr *protocol.APIError) {

	op, duplicate, apiErr := e.registerOperation(p, agent, "act")
	if apiErr != nil { return nil, apiErr }
	if duplicate != nil { return duplicate, nil }
	defer func() { e.applyFrozenResult(op, &finalResult) }()
	e.setOperationReference(op, protocol.RecoveryReferenceO)

	// 1. 静态准入与静态内容规划：仍在 ready 认领前。
	e.mu.Lock()
	staticErr := e.staticActCheckLocked(agent, req.ObservationID, &req.Operation, time.Now())
	staticObs := e.observations[req.ObservationID]
	e.mu.Unlock()

	var point *pointCheck
	var scroll *scrollPlan
	var tap *tapPlan
	if staticErr == nil {
		point, scroll, tap, staticErr =
			staticActContentCheckWithTap(staticObs, &req.Operation)
	}
	if staticErr != nil {
		return e.finishWithError(op, "admission", staticErr), nil
	}

	// 2. 唯一普通工作位置。
	work, busy := e.acquireWork(op, WorkAct)
	if busy != nil { return e.finishWithError(op, "admission", busy), nil }
	defer e.releaseWork(work)

	// 3. 状态锁内只做有限内存检查和 ready -> claimed 单向转换。
	e.mu.Lock()
	session := e.session
	obs := e.observations[req.ObservationID]
	claimErr := e.claimObservationLocked(op, agent, work, obs, session, time.Now())
	e.mu.Unlock()
	if claimErr != nil { return e.finishWithError(op, "claim", claimErr), nil }

	// 4. 认领后才做设备读取、Preview、祖先比较、几何和焦点搜索。
	var fresh *device.Capture
	var tapFrame *tapFrame
	var raw *device.RawNode
	var scrollFrame *scrollFrame
	var proofTook time.Duration
	var revalidateErr *protocol.APIError

	if scroll != nil {
		fresh, scrollFrame, revalidateErr = e.revalidateScrollTarget(work, obs, scroll)
	} else {
		fresh, raw, tapFrame, proofTook, revalidateErr =
			e.revalidateActTarget(work, obs, &req.Operation, point, tap)
		_ = proofTook // 继续使用现有 logTapPlan 时长白名单
	}

	if revalidateErr != nil {
		// STOP 是这一段的优先终止类别；调用层再复核一次，避免 topology、
		// foreground、Preview、source mapping 或选点错误抢先写成业务拒绝。
		if e.workRevoked(work) || work.Ctx.Err() != nil {
			revalidateErr = stoppedActRevalidation()
		}
		// 原 O 保持 claimed/retired。撤权、层级/预算/畸形树都不交付 rejected
		// frame；只有 deliverableRejection 的既有错误族可生成只读新 O。
		revalidateErr.Outcome = OutcomeNotExecuted
		var public *protocol.Observation
		if deliverableRejection(revalidateErr.Code) &&
			!e.workRevoked(work) && work.Ctx.Err() == nil {
			public = deliverRejection(
				e, work, session, obs, fresh, revalidateErr.Code)
		}
		if public != nil {
			e.markDelivered(op)
		}
		result := e.finishWithError(op, "revalidate", revalidateErr)
		if public != nil {
			result.Observation = public
		}
		return result, nil
	}

	// 5. 每个真实 device primitive 前由 dispatch* 内部再次竞争 STOP
	// 并持久化 may-dispatch。
	e.setOperationPhase(op, "dispatch")
	var outcome string
	var dispatchErr *protocol.APIError
	if scrollFrame != nil {
		outcome, dispatchErr = e.dispatchScroll(work, scrollFrame)
	} else {
		outcome, dispatchErr = e.dispatchAct(
			work, obs, &req.Operation, fresh, raw, tapFrame)
	}

	if outcome == OutcomeUncertain {
		e.markDispatchUnknown(work)
		result := newResult(op)
		result.Outcome = OutcomeUncertain
		result.Error = dispatchErr.Public()
		if scroll != nil {
			result.Effect = &protocol.Effect{State: protocol.EffectUnknown}
		}
		result.Recovery = e.finishOperation(op, OutcomeUncertain, "dispatch", dispatchErr)
		e.addSessionIfEnded(result)
		return result, nil
	}
	if outcome == OutcomeNotExecuted {
		dispatchErr.Outcome = OutcomeNotExecuted
		return e.finishWithError(op, "dispatch", dispatchErr), nil
	}

	e.clearDispatchUnknown(work)
	e.noteKnownOutcome(op, OutcomeExecuted)
	e.setOperationPhase(op, "post_observation")
	if e.workRevoked(work) {
		result := newResult(op)
		result.Outcome = OutcomeExecuted
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	session = e.sessionForWork(work)
	if session == nil {
		result := newResult(op)
		result.Outcome = OutcomeExecuted
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}

	public, postObs, postErr := e.captureAndPublish(
		work.Ctx, work, session, false, req.Image, "act")
	if postErr != nil && postErr.Code == protocol.CodeStopped {
		result := newResult(op)
		result.Outcome = OutcomeExecuted
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}

	result := newResult(op)
	result.Outcome = OutcomeExecuted
	if postErr != nil {
		if public != nil {
			result.Observation = public
		} else {
			result.Observation = &protocol.Observation{
				Status: "failed", Missing: []string{"tree"}, Error: postErr.Public(),
			}
		}
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(
			op, OutcomeExecuted, "post_observation_failed", nil)
		return result, nil
	}
	result.Observation = public
	if scroll != nil {
		state := e.inferScrollEffect(work, scrollFrame, postObs, public, scroll.direction)
		if e.commitScrollEffect(work, op, state) {
			result.Effect = &protocol.Effect{State: state}
		} else {
			result.Effect = &protocol.Effect{State: protocol.EffectUnknown}
		}
	}
	result.Recovery = e.finishOperation(op, OutcomeExecuted, "finished", nil)
	return result, nil
}
```

`dispatchAct`/`dispatchText`/`dispatchScroll` 内部仍调用 `beginUIDispatch`。上面的伪代码没有把该门禁前移成“树证明成功即可派发”。

必须保留的负向属性：

- 两个并发 P 对同一 O 认领时只有一个成功；
- 同一 P 第二次调用只返回原结果；
- STOP 在 `beginUIDispatch` 前发生时不产生 primitive，也不 delivery、不 `markDelivered`、不生成 retry 新 O；
- STOP 在 primitive 派发后发生时，结果保持 uncertain，不伪称撤回；
- Preview 失败不推进 ref，也不发布候选；
- 被拒绝的重验帧只有通过 deliverableRejection、未撤权且通过 commit 门禁时才能发布；cancelled/budget/malformed 不 delivery；
- 状态锁内不等待 ADB/RPC/磁盘，不运行 L1/L2/geometry 搜索。

## 8. settle 关系

本提案不改动：

- [`DefaultSettlePolicies`](../../../../internal/engine/settle.go#L45) 的 act 400ms、start 300ms、open-app 600ms；
- 最小/最大采样数；
- 19 字段 `rawIdentity`；
- `hierarchyDigest` 的逐节点父 fingerprint 链；
- `screenDigest` 的前景与屏幕摘要；
- `foregroundMatches` 对 unknown 的拒绝。

settle 只说明“当前采样帧在这个严格摘要上相同”。settled 后仍可能因为以下原因在动作重验拒绝：

- foreground 在 settle 后变成 unknown；
- fresh source mapping 损坏；
- 多个同身份候选；
- L2 只有根和透明 wrapper；
- 文字焦点状态未知；
- 点击 target subtree 新增 blocker；
- 滚动原轨迹被新 blocker 截断；
- Display/work/session/STOP 门禁失败。

因此 README 不得再声称 settled 帧会 100% 通过 relaxed revalidation；本提案也不扩展 settle 摘要来“保证一致”。实现时应修正 [`identity.go:19`](../../../../internal/engine/identity.go#L19) 的旧注释，因为动作匹配将不再与 settle 使用完全相同身份。

## 9. 负例与验收矩阵

下列“触发序列”都是设计级反例或测试步骤；标注的预期不是本会话设备实测。

| 编号 | 触发序列 | 预期 |
| --- | --- | --- |
| N1 | 旧 O 中按钮及其祖先内容不变；fresh 中仅祖先 Bounds 改变 1px | Tier 1 目标仍唯一，允许派发 |
| N2 | 目标按钮自身从 `(100,100,300,180)` 移到 `(100,108,300,188)`，原中心仍在目标内且 L1 不变 | Tier 2 唯一匹配，保留原点或重算目标子树叶点 |
| N3 | fresh 在目标与原有带文本祖先之间插入一层可证明透明的 wrapper，且目标非根语义 anchor 在旧/新 L2 中唯一相同；因此 L1 不同、L2 相同 | Tier 1/2 因 L1 不同跳过，Tier 3 独立按 L2 匹配成功 |
| N4 | fresh wrapper 的三项内容 presence/值矩阵：任一 `TextSet/DescSet/ResourceSet=false`；三项均 true 但任一值非空；三项均 true 且全为空但任一交互/状态能力未知或非安全 | 所有内容 presence 缺失或内容非空情形、以及能力非安全情形都不是透明层；旧 L2 不匹配，拒绝。仅三项均 true 且值为空、交互/状态均已知安全时才允许过滤 |
| N5 | 同 App/task 新页面相对旧链增删透明 wrapper，明确造成 L1 不同；过滤透明 wrapper 后 L2 只有根，即使目标 class/text/bounds 碰巧相同 | Tier 1/2 因 L1 不同不能先命中；Tier 3 因无非根 anchor 拒绝 |
| N6 | 两个 fresh 候选同身份，overlap 分别为 0.81 和 0.79 | 立即 `target_changed`，不得取 0.81 |
| N7 | 旧文案“删除”变“分享”，其 resource-id、class、bounds、能力相同 | 默认 Tier 1–3 都拒绝；Tier 4 未决 |
| N8 | 原 carrier 是目标卡片内安全图片，fresh 中同图片上移 12px，仍唯一且非交互 | 匹配同一 carrier，重算 carrier 内安全点；不得上溯卡片 |
| N9 | 稳定按钮 `item_42` 有独立非空 text/description/唯一 resource；原图片 carrier A 消失，或 A 变成 context/交互边界，而同一 target scope 中图片 B 安全。分别构造原点仍可证明和原点落入 blocker、B 内仍有安全点两个变体 | 允许安全解绑：前一个变体保留原点；后一个变体 chooseTapPlan 选 B。tapFrame 携带实际点以及 B 或 nil carrier；失效的 A 不得继续被当作可点击载体，且不得点击祖先或穿过 blocker |
| N10 | 原 carrier A 消失或变成 context/交互边界。变体一：目标没有独立内容身份，只有 A 能证明业务身份；变体二：目标有独立内容身份，但 A 不再安全且目标剩余区域被全尺寸交互 overlay 覆盖 | 变体一拒绝；变体二只有在 N9 的独立身份成立后才可尝试同一 target scope 内重选，全尺寸 blocker 下 chooseTapPlan 无安全候选，仍拒绝；任何情况下都不得点击祖先或穿过 blocker |
| N43 | 列表祖先 resource=list，匿名 target 自身无 text/description/resource；旧 carrier A 消失，新 B 占原 slot，即使 L1/L2 与几何唯一 | 共享祖先不是独立 target 身份，禁止载体解绑；不能靠 list ID 使 N10 失效 |
| N11 | fresh 给目标增加更安全候选，但原静态点仍证明有效 | 继续使用原点，不因新候选自动改点 |
| N12 | ref 目标自身移动，原点落在 blocker，目标子树有唯一安全中心 | 仅在目标子树内重算，并把新 x/y 传到假设备 `Tap` |
| N13 | 坐标 tap 原 `(x,y)` 在 fresh 解析到另一节点，即使附近有同 identity 目标 | 拒绝，不把坐标改到附近目标 |
| N14 | raw swipe `from=(10,20),to=(30,40)`，fresh 容器/内容变化 | 只使用原 from/to，或由既有 STOP/设备门禁拒绝；不转换为中心线轨迹 |
| N15 | 纵向 scroll 容器从 `[200,2200]` 扩为 `[120,2300]`，左右边缘不变，原容器唯一 | 允许重验；若原 segment 不安全，在同一容器生成新安全 segment |
| N16 | 纵向 scroll 容器横向移动 80px，其他文本相同 | 离轴不稳定，拒绝 |
| N17 | scroll 原 segment 中间新增 blocker，但同一容器另有唯一可证明安全 span | 重算 safeRect 和 `scrollGeometry`；假设备收到的 Swipe 参数等于新 frame |
| N18 | scroll 所有 span 都小于 `minScrollTravel` 或新 geometry 失败 | 拒绝，不缩短成未证明手势 |
| N19 | 两个 fresh scroll 容器同 class/text，轴向都重叠 | `matchActTarget` 多候选，拒绝，不使用最长轨迹决胜 |
| N20 | 无 ResourceID、位于屏幕下部的输入框 tap 后键盘上移 500px，但 Description anchor 在旧/新帧唯一 | shifted 可绑定；set/readback 使用新 bounds |
| N21 | 点击输入框 A 后 A 消失；B 唯一获焦，但 B 与 A 的 package/class、稳定 anchor、resource/description presence 或值有异，或者 B 不满足 near 的原点/overlap，也不满足 shifted 的尺寸/水平带条件 | `sameTextSourcePresence` 在 exact/near/shifted 前拒绝任何上述来源差异；几何不符的 B 同样不绑定。该行只承诺可观察差异；若 B 与 A 的全部可观察签名和几何都相同，转 N36，不能承诺识别替换 |
| N22 | 旧帧和 fresh 各有两个同 anchor 输入框，其中恰好一个获焦 | anchor 不唯一，拒绝 |
| N23 | 旧 `want` 或 fresh 输入框的 `HasEnabled=false`、`HasPassword=false`；或 fresh 的 `HasFocused=false` | 缺少正向已知事实，拒绝绑定；不能把来源未知补成 enabled/non-password，也不能把 fresh 未知补成已聚焦 |
| N24 | tap 后绑定成功，第一次 readback 前焦点切到另一个同类型字段 | 同一 anchor/geometry 不再唯一，返回 text uncertain；不重 tap |
| N25 | STOP 在 ProbeDisplay、Capture、空候选 Tier 结束或匹配期间到达，但在 `beginUIDispatch` 前 | primitive 调用数 0；结果为既有 CodeStopped/control_revoked，不 delivery、不 markDelivered、不生成 retry 新 O；原 O 已 claimed/retired |
| N26 | text focus tap 已派发，STOP 后尝试 set/readback | tap 保留 uncertain/executed 证据；不得伪称撤回或重放 tap |
| N27 | Preview 构建失败或 source map 损坏 | 不派发，返回既有 hierarchy/target 错误族 |
| N28 | malformed parent cycle 输入 `lineageL1` | 有界失败，不允许 goroutine 卡死 |
| N29 | 并发两个 Act 使用同一 O | 仅一个 claim winner；另一个 observation retired/claimed 错误 |
| N30 | 同一 P 重放 | 只返回第一次记录结果，不再次派发 |
| N31 | settle 两帧相同但 foreground unknown | settle 可完成，动作重验仍拒绝 |
| N32 | 旧目标 disabled 已知 true，fresh disabled 已知 false | `sameStructure` 失配并拒绝；不得用未知默认掩盖状态变化 |
| N33 | 旧帧有两个同身份节点 A/B，fresh 只有 C；Tier 2 中 A→C、B→C 各自一条 outgoing，但 C 有两条 incoming | 双向不唯一，A/B 都拒绝；不得按调用顺序让 A 抢到 C |
| N34 | 无 anchor 的旧输入框 A 消失；同 package 的另一个编辑框 B 使用不同 class，或 Description presence/值不同，并占据原点击点且获焦 | `sameTextSourcePresence` 拒绝，不得因 B 是唯一获焦字段而绑定 |
| N35 | fresh 有两个同 package/class、同 resource/description presence 的编辑框都覆盖旧点，只有一个获焦 | focus 前统计为 2，拒绝；唯一获焦不能缩成唯一来源 |
| N36 | 同 TaskID 下切到新页面，新输入框与旧输入框 package/class/anchor/geometry 全部相同 | 现有 `RawNode`/`Capture` 无法辨别替换；实现不得声称同 window proof，也不能仅凭相同签名承诺总拒绝。测试验证证据文字与结果分类如实保留该边界，后续设备 `CandidateCount`/readback 只能补充当次 RPC 事实 |
| N37 | N37a：`Np=Nc=20,000`，零/单候选在扫描末尾；N37b：两侧均为 20k 深度 1 节点且全部 ancestor-mismatch；记录候选比较、lineage 构造/比较，以及 visited map、lineage slice、old/new memo backing store 的 allocation count 和 bytes | N37a 对每个 matcher，单 Tier 候选比较不超过 `Np+Nc`，三级不超过 `3*(Np+Nc)`；target+carrier 两次 matcher 合计不超过 `6*(Np+Nc)`，不得出现 `Np*Nc`；N37b 每次 visited map 只随实际深度分配，不得按完整 capture size 预分配。用操作计数配合 `testing.AllocsPerRun`/`-benchmem` 报告 lineage 相关总分配，不虚称总分配恒定 |
| N38 | 人为把某条祖先链设到深度上限之外，或把 lineage 工作量预算压到唯一候选之前耗尽 | 立即返回既有 CodeHierarchyFailed/hierarchy_unavailable，不尝试下一 Tier、不派发 primitive、不 delivery、不 markDelivered；重复运行和改变无关节点顺序得到同一结论 |
| N39 | STOP 在两次定向扫描之间、第一轮找到唯一 outgoing 后、反扫 incoming 结束前或每 Tier 返回调用层前取消 `work.Ctx`/撤权 | probeActNode 在 Tier 边界和返回处先检查撤权，返回 control_revoked 对应的 CodeStopped；即使已有唯一候选也不得返回 successful frame |
| N40 | STOP 在 ProbeDisplay/Capture 返回后、第一轮扫描末尾、第二轮扫描末尾或空候选 Tier 末尾到达 | 调用层先检查 workRevoked/work.Ctx，返回既有 CodeStopped/control_revoked；primitive=0、observation delivery=0、markDelivered=false、新 O=0，不进入下一 Tier |
| N41 | 祖先深度达到预算上限、候选预算耗尽或 parent cycle/越界导致 malformed | budget/malformed 映射既有 CodeHierarchyFailed/hierarchy_unavailable，不尝试下一 Tier；target/scroll 的 none/ambiguous 返回 target_changed，carrier 的 none/ambiguous 只允许独立身份下的安全解绑 |
| N42 | ref `text` 的原静态点落入 fresh blocker，同一 target scope 中存在唯一安全点并成功绑定焦点目标 | 假设备收到的第一次 `Tap` 必须等于 `tapFrame.x/y`，不能退回 raw center；随后 `GetTextOnDisplay` 和 `SetTextOnDisplay` 使用绑定后的 fresh bounds，readback 继续使用同一身份约束 |

N21 与 N36 的边界必须分别测试：前者要求实现拒绝可观察身份或几何已经变化的字段；后者要求文档、日志和错误语义不得把完全同签名替换伪称为“已识别”。不能为了满足 N21 而增加“唯一焦点即原目标”的回退。

验收还要覆盖：

- `lineageL1/L2` 对同一输入确定性输出；覆盖 L1 不同、L2 相同且非根 anchor 成立的 Tier 3 成功用例；
- 所有 Tier 在同一层有 0/1/>1 候选时分别降级/成功/拒绝，并验证旧→新 outgoing 与 fresh→旧 incoming 双向唯一；
- N37a 必须按 matcher 断言每 Tier 只做定向扫描，候选访问不超过 `Np+Nc`，target+carrier 合计不超过 `6*(Np+Nc)`，且 lineage 访问只发生在通过便宜筛选的相关候选上；N37b 必须记录 20k 深度 1 ancestor-mismatch 下 visited map、lineage slice 和 memo backing store 的 allocation count/bytes，证明没有按整棵树容量逐候选预分配；
- N38 的 budget/malformed 必须返回既有 `CodeHierarchyFailed`；N39/N40 的 cancelled/撤权必须优先返回既有 `CodeStopped`。两类都不得 delivery、`markDelivered` 或生成 retry 新 O；不得继续下一 Tier；
- matcher 只在已认领工作的锁外执行，状态锁内不得读取或构造 lineage；
- target/carrier 的匹配结果分别映射；target 的 ProbeDisplay/Capture 返回后先检查 workRevoked/work.Ctx，再解释 display/hierarchy 错误；scroll 使用相同顺序；
- N4 必须覆盖 TextSet/DescSet/ResourceSet 的 `false、true+空、true+非空` 三态；任一 false 都不透明，只有三者 true+空且交互状态安全才允许过滤；
- N9/N10 必须覆盖 item_42 的 A 消失后 B 安全成功、无独立 target 身份拒绝、全尺寸 overlay 拒绝，以及所有解绑点仍在同一 target scope；
- `bindFocusedText` 分别覆盖 exact 的 Bounds 相等、near 的原点/overlap、shifted 的纯纵向移动；三者统一拒绝 package/class/resource/description 来源差异，anchor 统计在 focused 过滤前完成；
- anchor 和 text 候选的统计都限制在目标 DisplayID；
- known-known、known-unknown、unknown-unknown 三个状态矩阵；
- `Preview` 前后 `builder.nextRef` 与旧 O 的 ref 集合不变；
- 日志不含 label/resource-id/ref/x/y/text；
- 测试失败时的错误码仍属于现有错误族，不新增客户端错误码。

## 10. 实施计划

### 10.1 先红复现与策略单测

1. 在 HEAD 上添加最小失败用例：
   - 祖先 Bounds 改变、目标完整内容不变；
   - 目标 8px 位移；
   - 透明 wrapper 插入导致 L1 不同、L2 相同并成功，以及语义 wrapper 替换仍拒绝；
   - 两个旧节点争用同一个 fresh 候选的逆向不唯一；
   - 20k×20k 零/单候选访问计数、20k 深度 1 ancestor-mismatch 的 allocation count/bytes、长链预算耗尽和扫描中取消；
   - root-only L2；
   - 两个 Tier 候选最大/次大 overlap；
   - malformed parent cycle；
   - carrier moved；A 消失后 B 安全且 target 有独立内容身份；无独立身份、context/interactive carrier、全尺寸 overlay；
   - keyboard shift 的正确和错误聚焦；无 anchor 的来源 class/description presence 变化；无 anchor 多候选只聚焦一个；
   - TextSet/DescSet/ResourceSet 的 false/true-empty/true-nonempty 矩阵；
   - STOP 在 Probe/Capture 返回、空候选 Tier、两轮扫描末尾；
   - scroll 轴向伸缩、离轴移动、整段中断、runtime params。
2. 新增 `act_revalidation_policy.go`（名称可调整）和对应测试。状态机至少保留 unique/none/ambiguous/cancelled/budget/malformed；保留 `identity.go` 的 settle 身份函数，不把 Tier 或 L2 加入 19 字段摘要。
3. 为 tri-state、known-unknown、unknown-unknown、L1/L2 序列和两次定向扫描的双向唯一性写表驱动测试；用计数器断言没有 `Np*Nc` 全表探测，并记录 visited map 与 lineage cache 的实际分配。

### 10.2 接入 ref 目标

1. 在 [`revalidateActTarget`](../../../../internal/engine/act.go#L379) 中只对 ref 目标调用新 matcher；ProbeDisplay/Capture 返回后先检查 workRevoked/work.Ctx，再解释 display/hierarchy 错误。
2. 坐标目标继续走原 point proof；raw key/swipe 不获得重定位能力。
3. 将 `revalidateTapTarget` 的结果改为 frame 或等价显式结构；`revalidateActTarget` 相应返回 `capture + raw + tapFrame + 时长 + error`，避免在调用层丢失重算点。
4. `dispatchAct` 使用 frame 中的重算坐标。
5. 保留 `validateTapRawTopology`、`Preview`、`checkTapSourceMapping`、`validTapPublicTarget` 和 `collectTapScope`；carrier 失配时仅在 target 有独立内容身份后复用同一 scope 的 `chooseTapPlan`。
6. 替换当前刻意固定旧行为的测试：
   - [`act_tap_scope_test.go:639`](../../../../internal/engine/act_tap_scope_test.go#L639) 的“新安全候选不抢原点”继续保留；
   - [`act_tap_scope_test.go:652`](../../../../internal/engine/act_tap_scope_test.go#L652) 中 carrier moved 改为成功重算；disappeared 时分别覆盖“独立 target 身份 + B 安全成功”和“无独立身份拒绝”，context/interactive/full overlay 仍拒绝；
   - `TestActTapCardKeepsFixedPointWhenFreshMovesOutsideElement` 改为同 carrier/target 内重算。

### 10.3 接入 scroll

1. 在 [`revalidateScrollTarget`](../../../../internal/engine/scroll.go#L619) 中先使用唯一容器 matcher，再检查 `sameScrollScope`；Probe/Capture 和 Tier 边界的撤权检查与 target/carrier 共用。
2. 使用真实 `scrollSafeSpans(projection, freshIndex, clipped, direction)`。
3. 原 `provesSegment` 通过时保持原轨迹；失败时使用 `longestSpan`、`safeRect`、四参数 `scrollGeometry` 重算，并再次 `provesSegment`。
4. 返回包含实际新 `start/end` 的 `scrollFrame`。
5. 在 [`scroll_revalidate_test.go:61`](../../../../internal/engine/scroll_revalidate_test.go#L61) 和 [`scroll_revalidate_test.go:180`](../../../../internal/engine/scroll_revalidate_test.go#L180) 基础上分离：
   - 轴向伸缩/移动允许；
   - 离轴移动超容差拒绝；
   - 原 segment 局部中断时可重算；
   - 全部 span 不安全仍拒绝；
   - fake device 收到新 frame 坐标。
6. 同步 [`scroll.go:515`](../../../../internal/engine/scroll.go#L515)、[`scroll.go:685`](../../../../internal/engine/scroll.go#L685) 的旧“不重算”注释，并验证 budget/malformed 为 hierarchy、cancelled 为 CodeStopped。

### 10.4 接入 text

1. 将 `bindFocusedText` 扩展为接受上一帧 capture、fresh capture、want 和原 tap 点，或者引入等价的 binding state；实现互斥的 exact（anchor + Bounds 相等）、near（完整来源身份 + 原点和 overlap）、shifted（anchor + 完整来源身份 + 水平带 + 纵向位移）路径。
2. 保留 known focused/enabled/non-password 门禁。
3. 删除任何全屏唯一同类型获焦回退。
4. 在 tap 后和每次 readback 前重新核对 foreground/task 上下文，并明确该证据不是 window token。
5. 将 `tapFrame` 传给 `dispatchText`，focus tap 使用重算点；将绑定结果携带到 `textTargetOf`，确保 `Get/SetTextOnDisplay` 使用新 bounds。
6. 保持 [`TestTextFocusReadFailureDoesNotReopenEndedTap`](../../../../internal/engine/ui_execution_test.go#L292) 的 uncertain 语义。

### 10.5 门禁、日志和契约

1. 保持静态准入、work acquire、claim、Preview、dispatch 的先后。
2. 新增日志只允许 `tier`、`mode`、`ambiguous`、`proved` 等枚举/布尔/时长；不输出 anchor 值或坐标。
3. 保持 `beginUIDispatch` 的持久化 may-dispatch 与 STOP 竞争路径；Probe/Capture/matcher 的撤权分支在 delivery 前终止当前 P。
4. 保持 `deliverRejection` 只在既有可交付错误族、已取得可用帧、未撤权且通过既有 commit 门禁时发布只读新 O；cancelled/budget/malformed 不交付、不 `markDelivered`、不生成 retry 新 O。Probe/Capture 的设备错误必须在撤权复检之后分类。

实现落地后才同步正式文档和版本，本次不修改它们：

- [`docs/interface.md`](../../../../docs/interface.md) 的动作重验、fixed trajectory 和错误恢复说明；
- [`internal/mcp/schema.go`](../../../../internal/mcp/schema.go) 的 act 工具说明，尤其固定点/固定轨迹措辞；
- 相关源码注释和测试；
- 按仓库流程同步 contract version、生成常量和发布清单，且只手改 `release/versions.json`。

## 11. 保留的总体路线与确认边界

本稿保留并修正了四条路线：

1. **祖先链去坐标**：L1 去掉 Bounds/Focused/Selected；L2 只跳过可证明透明的包装层，并保留非根 anchor。
2. **分层几何**：Tier 1–3 只在唯一候选时成功，不使用最大重叠决胜。
3. **动态点击/滚动安全区**：点击限定 target subtree；原 carrier 可安全复用时优先沿用，失效时仅在目标双向唯一且有独立强内容身份后重新选择安全 carrier/点。滚动限定同容器、轴向连续、离轴稳定，并整段证明新轨迹。
4. **有身份约束的 shifted text**：只放宽键盘导致的纵向位置，必须同 Display、同前台 task 上下文、同类型、稳定 size/horizontal band 和双向唯一非空 anchor；TaskID 不等价于 window token。

Tier 4 仍未决，默认关闭。实现者需要在获得用户确认后选择“永久移除”或“受限转换”；未确认时只实现 Tier 1–3。

本文件仍为“提案，未实现”。错误分类只复用既有 `CodeStopped` 与 `CodeHierarchyFailed`；STOP 不 delivery、不生成 retry 新 O。本次审核基于源码、静态推演和现有测试契约，不把任何拟议行为写成已实现或已实测，也没有修改 product code、`docs/`、版本、其它提案或设备状态。

### 11.1 已落地

| 提案条目 | 代码落点 | 验证范围 |
| --- | --- | --- |
| Tier 1–3、L1/L2、透明 wrapper 与三态身份 | `internal/engine/act_revalidation_policy.go` 的 `compareTri`、`lineageL1`、`transparentWrapper`、`lineageL2`、`tierMatcher`、`matchActTarget` | V16、V19；N1–N7、N31–N33、N37–N41 |
| 点击/长按、点击载体与安全 frame | `internal/engine/act.go` 的 `revalidateActTarget`、`revalidateTapTargetFrame`、`dispatchAct`；`tap_scope.go` 的 `collectTapScope`、`chooseTapPlan`、`validTapPublicTarget`；`act_revalidation_policy.go` 的 `tapFrame` | V17、V19；N8–N13、N42、N43 |
| scroll 同容器动态安全轨迹 | `internal/engine/scroll.go` 的 `scrollSafeSpans`、`sameScrollScope`、`revalidateScrollTarget`、`dispatchScroll` | V18–V19；N14–N19 |
| text focus binding、shifted 与来源/焦点门禁 | `internal/engine/act.go` 的 `bindFocusedText`、`sameTextSourcePresence`、`anchorCount`、`textTargetOf`、`dispatchText`；`act_revalidation_policy.go` 的 `stableNonEmptyContent`、`sameTargetResource`、`independentTargetContent` | V18–V19；N20–N24、N34–N36、N42 |
| O/P、STOP、Preview、派发与错误分层 | `internal/engine/act.go` 的 `Act`、`staticActCheckLocked`、`claimObservationLocked`、`captureForActRevalidation`、`probeActNode`；`ui_execution.go` 的 `beginUIDispatch` | V19、I04–I08；N25–N30、N37–N41 |
| Android transport/RPC 参数消费 | `internal/android` 的受管 Tap、Swipe、Get、SetText 路径及对应 fake/RPC 精确断言 | V20、I07 |

本册实现提交为 `900e640fe8343f92c4e8dd82dd3df839dde20756`，最后由发行锚点
`049583d328e335c56d56f153b7464ea03f1ca814` 覆盖。公共记录见
[实现与验证](../../../verification/observation-continuity-and-revalidation.md)与
[统一基准](../../../verification/observation-continuity-benchmarks.md)。

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 代码状态 | 默认范围内的 Tier 1–3、tap/carrier、scroll、text、O/P/STOP 与 Android 消费路径代码全覆盖；设备覆盖有限另行记录。 |
| Tier 4 | 未执行，保持默认关闭。用户确认范围没有选择“任意动态文案变化永久移除”或“受限转换”；Tier 1–3 已覆盖。 |
| D03 | 27 case 中 24 pass、3 fail。唯一失败为 `d03_scroll_full_blocker_reject` 三轮；fixture 把全尺寸 blocker 作为所选 `ScrollView` 的兄弟 overlay，blocker 没有滚动祖先，而现行契约明确这类候选不阻塞，因此该负例越出受控滚动安全范围。原报告 `result=fail` 和 3 个 failed checks 必须保留，不改写为通过。 |
| D03 primitive gap | `service_internal_primitive_count_unavailable` 保留；不能用 fixture 中的 blocker tap 冒充服务内部 primitive 计数。 |
| D04 | 18 case 为 3 fail、15 gap。三个 `d04_text_focus_readback` 的唯一 false check 为 `focus_window_observed`；A 值精确、B 不变，来源和多数读回检查通过，但键盘前后均为 hidden。原报告 `result=fail` 保留。 |
| D04 focus gap | 键盘顶起位移窗口未构造；`focus_shift_window_unproven`、`d04_focus_shift_race` 与精确 focus race 仍是覆盖缺口。15 个 source 变体或 duplicate 绑定断言不能替代该窗口。 |
| D04 primitive gap | `d04_primitive_count` 未实测，不能由其他检查推导。 |
| D05 最新复跑 | 第 1 轮全部 pass；第 2 轮直接新 `start` 后以 `display_create_failed` 中止，原会话 `end` confirmed。总结果为 `1 pass / 1 fail`，不能写成三轮通过。 |
| D05 旧报告 | 旧报告三轮保持原样 `0 pass / 3 fail`。每轮仅 `wait_p_outcome_not_executed` false；实际撤权以 `control_revoked` 表达，其余 26 个 checks、原 P/O 撤权、wait P 的 busy 指向、新 P/O 互异重入和 end confirmed 均成立。该解释只纠正 driver 强求 wait outcome 的判据，不改写原报告 fail。 |
| D05 STOP gap | 精确 capture、preview、post-dispatch STOP 注入及 primitive 精确计数未实测；`capture_preview_or_post_dispatch_stop_not_injectable` 保留。 |
| D06 与宿主 | 3 轮 full→固定 full 基线后的单 delta→BACK executed→inspect current O→end executed 闭环通过；不能扩大为滚动、多 delta 或完整动作矩阵。实际外部 MCP 宿主刷新/工具重发现由用户自行处理，未独立确认，按用户明确接受记录。 |
