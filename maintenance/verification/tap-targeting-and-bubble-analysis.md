# 调查记录：动作能力、落点兜底与 bubble 的关系

日期：2026-11-17。
代码基线：`4c60ca1649b79d482512e20813ddef30a9ae4678`。
环境：Windows amd64、Go `1.27.1`、`GOEXPERIMENT=jsonv2`，本仓库 fake device。
性质：代码、历史与契约调查及专项单测；调查期间未修改业务实现，未访问实机，未构建或部署发布包。
提交：2026-11-18 随契约 17 验证记录一并提交；文中代码位置、行号与结论仍以 2026-11-17 基线为准。
关联取舍：曾起草“按请求动作选择祖先”的提案，用户于 2026-11-17 决定舍弃；结论与理由见 §7。

## 1. 现象与分诊

用户提出两项相互关联的问题：

- agent 从返回树中选择带有 `tap` 的目标，为何仍可能收到 `unsupported_node_action`？
- 最近版本是否已经支持中心或原落点不可用时的动态选择，bubble 与它是什么关系？

结论：现有实现具有静态候选、fresh 动态重算/重选和 bubble 祖先提升三套相关机制。
前两套解决同一目标作用域内的落点问题，bubble 决定是否把操作作用域改到祖先。
静态 `unsupported_node_action` 既用于缺动作能力，也用于无可证候选；同一 code 不能单独区分原因。

本次没有实际 agent 调用的 O、目标 ref、对应树和完整错误消息，不能把原反馈归因到某一分支。
合成用例证明的是各分支的行为及边界；未发现由本次调查证明、需要直接修复的实现偏差。
按动作寻找祖先属于改变现行停止判据的取舍；用户已决定舍弃（§7）。扩展有限落点候选则是另一项取舍。

## 2. 历史与当前机制

### 2.1 引入顺序

| 机制 | 提交与版本 | 依据与范围 |
| --- | --- | --- |
| 静态安全落点候选 | `cc71c87`，归档 `6ec25c8`；0.14.0 / 契约 14 | [原提案](../proposals/implemented/act-tap-safe-scope-and-guidance.md) §4–5：中心 → 非阻塞 carrier → 中心线区间；当时认领后只重验固定点 |
| fresh 动态重算与目标内重选 | `900e640` 实现、`bb43c19` 交付；0.15.0 / 契约 15 | [重验提案](../proposals/implemented/observation-continuity-and-revalidation/04-action-revalidation-relaxation.md) 的 A2/A18；[现行接口](../../docs/interface.md):496–505 |
| 默认 bubble 祖先提升 | `d529ee7`；0.16.0 / 契约 16 | [原动作契约提案](../proposals/implemented/agent-reference-and-action-contract.md) §4.3；[现行接口](../../docs/interface.md):390–400 |

用户记忆中的动态选择确实存在，契约 16 继续保留契约 15 的重验机制。
静态候选的历史记录有临时复合卡片实机窗口；动态载体重选的专项成功证据以合成/假设备为主，
不能把整体发布实机通过解读成每个动态重选分支均有专门实机样本。
历史验证范围见[静态落点验证](act-tap-safe-scope-and-guidance.md):140–188 和
[观察连续性与重验验证](observation-continuity-and-revalidation.md):235–282、498–575、651–668。

### 2.2 四种对象

| 对象 | 含义 | 不保证的事项 |
| --- | --- | --- |
| agent 选择的 ref C | 授权观察中的内容/目标选择器 | 文本语义本身不证明 Android 点击消费者 |
| `actions` | 源节点报告的动作能力；`tap` 来自 clickable | 不预先承诺一定存在本服务能选出的落点 |
| 实际规划作用域 N | 直接目标是 C；bubble 时为祖先 A | A 的动作能力不证明其业务效果等同于点击 C |
| carrier 与坐标 | 非阻塞承载节点解释坐标来源；最终冻结一个点 | carrier 不是自动可操作 ref，坐标原语不证明业务完成 |

能力生成见 [build.go](../../internal/observation/build.go):472–488；MCP 动作列原样输出见
[tree_text.go](../../internal/mcp/tree_text.go):438–441。
设备接收的是 display 与坐标，见 [act.go](../../internal/engine/act.go):925–942、
[foreground.go](../../internal/android/foreground.go):94–102；设备没有 C/A 身份路由。

## 3. 实现分支、兜底与拒绝边界

### 3.1 静态阶段：同一目标内选点

[staticTapRefCheck](../../internal/engine/tap_scope.go):1030–1088 使用授权 O 的完整内部投影。
C 已具有请求动作时，直接对 C 构建 scope，不论 bubble 省略、true 或 false。

[enumerateTapCandidates](../../internal/engine/tap_scope.go):638–698 的固定顺序是：

1. 目标原中心；中心越出 base 时改用 base 中心。
2. 非阻塞 carrier 的中心/裁剪中心，非匿名先于匿名，各自保持先序。
3. 纵向中心线剩余最长区间中点，再试横向中心线剩余最长区间中点。

[chooseTapPlan](../../internal/engine/tap_scope.go):726–750 选择首个通过子树谓词的候选。
目标子树中的 `tap/long_press/text` 节点及其后代参与阻塞；仅 `swipe` 不自动阻塞。
context 没有自身公开几何，其后代的可用矩形参与阻塞，不能把 context 说成一块已知完整矩形。

中心不可用不立即拒绝，但所有枚举候选失败后，认领前返回 `422 unsupported_node_action`，
消息为“无法在该节点作用域内生成可证明的点击落点”。此时没有 fresh 采集、没有派发，O 保持 ready。
这条路径不会因为 bubble=true 而再去寻找祖先。

### 3.2 fresh 阶段：保持目标，动态重算/重选

[revalidateTapTargetFrame](../../internal/engine/act.go):638–813 先证明同一目标仍然唯一、
动作与来源有效，再对 fresh 目标重建 scope：

1. 原坐标仍可证，且原载体约束仍成立（或目标有独立内容身份）时保留原点，见 :768–778。
   新出现更优的安全替代点不会抢走仍有效的原点。
2. 原点不可用时，已匹配的原 carrier 若仍是目标子树内的非阻塞承载节点，尝试在同 carrier
   重算，见 :735–763、780–790。这里尝试的是 carrier 的枚举中心，不是任意二维搜索。
3. 若原计划没有 carrier 绑定，可在同一目标 scope 继续按既有候选顺序选点。
   若原计划绑定了 carrier，而前两项不能继续使用，只有 `match.independentContent` 成立才可
   解绑并在同一目标子树选其他点/载体，见 :793–813。

独立内容只接受目标自身稳定非空 text/description 或两棵树中均唯一的 resource，
共享列表祖先的标识不算，见 [independentTargetContent](../../internal/engine/act_revalidation_policy.go):598–614。
即使原 carrier 仍能匹配但同载体候选不可用，代码在 :793 的 gate 也允许独立目标进入最后重选，
并非只在 carrier 节点物理消失时才可能重选。

[fixedCarrierSafePoint](../../internal/engine/act_revalidation_policy.go):824–838 与
[chooseUnboundTapPlan](../../internal/engine/act_revalidation_policy.go):847–860 执行这些选点。
无独立身份证据、无候选、目标丢失/歧义等按各自路径拒绝，通常为 `409 target_changed`；
预算、拓扑或投影损坏为 `503 hierarchy_unavailable`。

这些全在第一个设备原语之前。最终坐标写入 frame 后只派发一次；派发未确认不换点重试，
不通过后观察追改原执行事实，见 [dispatchAct](../../internal/engine/act.go):907–966。

### 3.3 当前 bubble：选择祖先作用域，再在其中选点

| 条件 | 当前行为 |
| --- | --- |
| C 自身支持本次请求动作 | 直接目标；同一 C 内的静态候选与动态重选照常生效；bubble 不参与 |
| C 缺请求动作，bubble=false | 不提升；可返回纯祖先引导；O 不因静态拒绝被认领 |
| C 缺请求动作且有其他 tap/long_press/text | 当前不提升，也不给这类 C 祖先引导 |
| C 无三类 touch 动作，bubble 省略或 true | 选第一个含任一 tap/long_press/text 的祖先；它若不支持本次动作就拒绝，不再向上找 |
| C 仅含 swipe | 当前已经可以提升；仅 swipe 既不是该选择的交互边界，也不自动阻塞落点 |

默认值与适用成员见 [requests.go](../../internal/protocol/requests.go):118–170。
坐标目标和其他 operation 显式携带 bubble 都无效；text 聚焦明确关闭 bubble。

bubble 静态 plan 同时保存 C 与 A，scope 以 A 为目标，先试 C 中心，再试 A 的既有候选。
[revalidateBubbleTapFrame](../../internal/engine/act.go):501–636 是独立分支：重验 C+A，
确认 C 的 Role/完整 Label 未变且仍无 touch 动作、A 仍是最近交互祖先，然后重新计算 A 内落点。
它没有套用 direct 的固定 carrier 解绑 gate，也不在 fresh 时换另一个祖先。

静态 A 有但无点时直接返回裸无落点 422，不生成无法证明的 safe_point。
纯 422 guidance 自身不派发，且当前遇 context 停止；bubble 的祖先选择会跳过 context。
二者当前这一差异见 [tap_scope.go](../../internal/engine/tap_scope.go):803–805、926–929。

### 3.4 “按动作找祖先”的实际影响（已舍弃）

曾起草的提案要把上表的“首个任意 touch 交互祖先”改成“首个支持请求动作的祖先”，这是目标范围选择，
不能解释成增加直接目标的落点兜底。

当被跨过的最近交互祖先 A 具有另一个 tap/long_press/text 时，A 及其后代仍在更高祖先 B 的
阻塞集合里，C 中心也被阻塞。能派发的候选只能落在 A 之外的 B 区域，可能远离 C；能力支持不证明
该坐标的业务效果等同于调用方对 C 的意图。仅 swipe 和无动作节点与此不同，不能混为阻塞边界。

“C+A 绑定”保证身份与授权选择器继续对应，不是 Android 把触摸交给 A 的证明。
当时草案已把这些边界写开；用户仍决定舍弃（§7）。不能用扩大 bubble 代替直接目标的候选覆盖问题。

## 4. 有限候选的合成边界诊断

本次新增临时 `TestTapCandidateCoverageDiagnostic`，使用现有 tap harness：

| 元素 | 合成 bounds 与能力 |
| --- | --- |
| 目标卡片 | `[100,300,900,1100]`，tap + long_press |
| 纵向交互子节点 | `[450,300,550,1100]`，tap + long_press |
| 横向交互子节点 | `[100,650,900,750]`，tap + long_press |
| 对角区域测试点 | `(250,450)` |

两个交互子节点形成十字，目标两条中心线都被占据，且没有非阻塞后代 carrier。
诊断直接调用现有 `provesSubtreeTapPoint(scope,250,450)` 得到 true；
`enumerateTapCandidates` 却只产生目标中心一个候选，该点失败。
实际 Act 返回 `not_executed/unsupported_node_action`，派发 0，O 为 ready。

因此“候选耗尽”不等于“整个矩形不存在可通过现有谓词的点”。它也不证明 Android 真实触摸安全，
只比较现有树模型内的谓词与枚举覆盖范围。这个行为符合当前固定顺序候选契约，不能直接标成
实现缺陷；若要扩大搜索，应单独评估成本、确定性与契约。

临时诊断文件在运行完成后已清理，没有留下常驻测试或业务代码改动。
复现可用 `tapCardWithChildrenStep` 构造上表三个节点，先断言该对角点谓词为 true，
再调用 `enumerateTapCandidates` 和 ref Act，检查上述结果。

## 5. 本次实际验证与同类核查

### 5.1 实际命令与结果

以下均在本记录基线执行，退出码均为 0。

静态候选与拒绝边界：

```sh
GOEXPERIMENT=jsonv2 go test ./internal/engine -run '^(TestActTapCompositeCardDispatchesCarrierPoint|TestActLongPressCompositeCardDispatchesCarrierPoint|TestActTapRefPlainButtonKeepsCenter|TestActTapUsesVerticalCenterSpanWithoutCarrier|TestActTapUsesHorizontalCenterSpanTieBreak|TestActTapFullyOccludedCardRejectsBeforeClaim|TestActTapAmbiguousCardRejectsWithoutOccludingRef)$' -count=1 -v
```

7 个顶层用例通过，包结果 `5.187s`。证明中心失败仍可经 carrier/中心线选点，完全阻塞或歧义仍拒绝。

动态重算、重选与上述临时覆盖诊断：

```sh
GOEXPERIMENT=jsonv2 go test ./internal/engine -run '^(TestActRevalidationTapCarrier|TestActTapCardRejectsFreshCompetingChildAtFixedPoint|TestActTapCardRejectsFreshCarrierIdentityChange|TestActTapCardKeepsPlannedPointWhenFreshAddsSafeAlternative|TestTapCandidateCoverageDiagnostic)$' -count=1 -v
```

全部通过，包结果 `14.290s`。`TestActRevalidationTapCarrier` 的 9 个叶用例覆盖同载体位移重算、
独立目标保留原点与改选第二 carrier、匿名目标/全遮挡拒绝、共享祖先不算独立身份、有效原点优先、
原点被新子节点占用后重算，以及坐标目标不追踪。
关键正例见 [act_revalidation_test.go](../../internal/engine/act_revalidation_test.go):771–825、1004–1040。

bubble 与祖先引导：

```sh
GOEXPERIMENT=jsonv2 go test ./internal/engine -run '^(TestActBubbleDefaultsToOneInteractiveAncestor|TestActBubbleFreshFrameRequiresChildAndAncestor|TestActTapContentGuidanceStopsAtNOrNearestAncestorBoundary|TestActTapContentGuidanceIncludesAncestorAndCarrierPoint|TestActLongPressContentGuidanceAndIndependentNextTurn)$' -count=1 -v
```

全部通过，包结果 `11.785s`，包含 C/A 丢失、歧义、内容/Role/能力变化、最近祖先变化与安全点阻塞的拒绝。

presence、显式 false、适用分支与 MCP 接入：

```sh
GOEXPERIMENT=jsonv2 go test ./internal/protocol ./internal/mcp -run '^(TestActEffectiveBooleanMembers|TestActEffectiveDefaults|TestActEncoderPreservesExplicitFalse|TestActEncoderRejectsInapplicableMembers|TestActPrepareCallSharesBooleanBranchValidation)$' -count=1
```

两包通过，结果分别为 `1.240s` 与 `0.058s`。

### 5.2 同类核查

- tap 与 long_press 共享静态 planner 和 direct/bubble 重验代码；本次静态长按和长按引导测试通过。
  direct 动态重选矩阵主要使用 tap，不能宣称 long_press 的每个动态分支均有专项覆盖。
- bubble=false 不关闭直接目标的静态候选和 fresh 重选；它只关闭祖先提升。
- text 聚焦强制关闭 bubble，保持自己的输入授权与写入/读回链；本次未扩展 text 测试矩阵。
- 仅 swipe 不构成 touch 边界；context 自身无公开几何，阻塞说明须精确到其后代。
- 静态无动作、静态无候选、fresh 证明失败与设备派发失败是不同分支；不能仅看一个错误 code 归因。
- 动态 frame 的 strategy 不作为公共结果字段；现有 `act.tap_plan` 记录静态 plan 的 mode，
  proven 表示重验证明通过，不表示设备执行成功，不能靠该日志还原所有动态选点过程。
- 按动作选择祖先的草案曾补明上述分流、swipe/context、坐标派发、能力与语义的区别；该方向已舍弃（§7），未实现。

## 6. 未覆盖项与原因

- 无 agent 实际失败调用的成对证据，未归因其真实错误到缺动作、候选耗尽、身份门槛或 bubble。
- 本次未使用实机、未验证第三方应用触摸消费者或业务效果；fake device 只记录派发参数与次数。
- 未新增发布包、未改版本、未部署；调查期间未提交，文件于 2026-11-18 随契约 17 验证记录提交。
  Windows 单测不提供账本耐久结论。
- 未扩展二维候选搜索、未放宽 carrier 身份门槛、未实现按动作选择祖先；该提案已舍弃（§7）。
- 未改 `docs/`。本记录是维护调查与实际验证结果，不替换正式契约；曾起草的提案已舍弃。

## 7. 取舍结论：不采纳“按请求动作选择祖先”

用户于 2026-11-17 决定舍弃曾起草的 `bubble-first-supporting-ancestor` 提案，保留“最近一个
touch-interactive 祖先”作为默认提升边界。理由按强度排列：

1. **被跳过的 A 仍是落点阻塞边界。** 提案保留 `isTouchInteractive` 的并集判据；当 A 只有
   `long_press` 而更高祖先有 `tap` 时，A 的矩形与它的后代（包括 C）都在更高祖先作用域的
   阻塞区，C 中心必然不可用，能派发的点只能落在 A 之外，可能远离 C。提案 §4.2 已写明
   “可能远离 C”，这是明确取舍，但与“点 C/最近祖先”的默认语义冲突。
2. **能力匹配不等于语义拥有者。** A 是 `text` 输入框时，跳过它去点更外层 `tap` 容器可能
   让输入框失焦或触发背景动作；A 只支持长按时，外层的 tap 回调与 C 的内容没有可证关系。
   公开树只能证明能力与几何，不能证明业务语义等价。
3. **默认行为应可预测。** 调用方想点更高节点时可以直接使用它的 ref，`bubble=false` 也能
   关闭提升；默认只应承诺“目标最近的可交互容器”，不应替调用方跨过语义边界。
4. **原反馈没有可归因的真实页面证据。** 本次调查没有取得 agent 实际失败调用的 O、目标 ref、
   对应树和完整错误消息；为一个未归因的现象改变默认执行语义，风险大于收益。

因此默认行为不变：C 自身有请求动作时走直接目标；C 完全没有三类动作且有效 `bubble=true` 时
只提升到最近的 touch-interactive 祖先；该祖先不支持请求动作时仍返回静态 422。当前 422 的
多语义问题另行处理：新的静态错误分类提案见
[act-static-error-taxonomy.md](../proposals/implemented/act-static-error-taxonomy.md)。

本记录的 §3.4 与 §4 保留为舍弃前的分析证据：它们分别说明“跨过 A 后落点会离开 C”和
“候选耗尽不等于平面无解”。调查记录保留，已舍弃的提案文件删除。
