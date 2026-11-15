# 提案总览：观察树净化、引用连续性、结构差分与动作重验

状态：提案，未实现。
基线提交：`6ec25c87daefbc01c99f78d968214304641d119d`（`master`，2026-11-15）。
适用范围：`start`、`observe`、`act`、`app`、`wait_for` 的观察投影，以及 `inspect` 的历史详细阅读视图。
影响范围：`internal/observation/`、`internal/protocol/`、`internal/mcp/`、`internal/engine/`，实施时同步相关正式文档与版本制品。
契约：改变。移除阅读表示的 `defaults`；扩展五类 delta 块的结构组合语义；放宽有证据的动作重验。实施时统一递增 `release/versions.json` 中的 `contract_version`。

关联议题：草稿引用 Issue #15（骨架屏空节点和长列表 full 输出膨胀）。本次审核以仓库源码与正式文档为证据，未执行宿主输出阈值测量、设备采集或实机操作，不把议题描述当成本次实测结果。
所有源码定位均以该基线为准，代码前进后须重新核对。本目录保持提案状态；本次修订不批准产品实现，也不把未来行为提前写入 `docs/`。

分诊：这是涉及现行判据、结构解释与动作时序的取舍型提案审核。文中发现的相邻实现问题目前只有静态证据；实施时仍须各自先红复现、定位根因并核查同类，不能把本次提案修订当成已经完成的缺陷修复。

## 0. 分册与总体路线

保留四个相互配合的设计方向，由四项设计分册和一份统一实施/验证计划组成。01–04 给出算法和边界，05 组织跨册依赖、验证与交付出口，本页提供导航和共同接口。

| 分册 | 内容 | 必须守住的边界 |
| --- | --- | --- |
| [01 源头净化与状态位](01-source-purification-and-state-flags.md) | 过滤无自身语义、无保留子孙的 view/group 空叶；精简状态默认值与 `defaults` | 输出精简不伪造未知事实；完整节点与 `set` 的省略语义分开 |
| [02 引用连续性](02-ref-matching-and-continuity.md) | 唯一容器、内容锚点、子孙投票、局部恢复、有证据的序号兜底 | 双向唯一、公开投影索引、会话 ref 高水位；缺证据就新号 |
| [03 结构差分 B2](03-structural-delta-diff-and-apply.md) | add 截断定义 + children 重挂接；删除旧容器前保护迁出子树 | 五块声明整体原子解释；不新增 move/update/root；往返无损 |
| [04 动作重验](04-action-revalidation-relaxation.md) | 祖先链去坐标、局部几何放宽、匿名包装层消除、点击/滚动重算、输入焦点绑定 | ref 连续性不授予输入权；几何和焦点不能独自证明身份 |
| [05 实施与验证计划](05-implementation-and-validation-plan.md) | 阶段依赖与出口、单元/系统/基准/实机矩阵、命令入口、待确认项、提交/发布/归档顺序 | 计划未执行；先红与反向对照留证，manifest 核验与已验证的最后实现提交对齐，归档提交只作后存证 |

Compact 树继续只折叠连续匿名单子链 `chain`，不增加跨兄弟区间折叠。净化删除无语义空叶；仍有内容的匿名包装层保留逻辑 ref，只在阅读投影中隐藏几何或折链。**单子链折叠不删除逻辑节点，不会自行触发父引用变化。**

## 1. 现有路径与问题边界

1. [build.go:215](../../../internal/observation/build.go#L215) 决定发布哪些源节点；当前 [hasSemantics:297](../../../internal/observation/build.go#L297) 将多种 `Has*` 当作保留证据。字段“存在”与字段“携带有用值”需要分别判断，不能以草稿中并不存在的 `filterTree/keepOwn/roleOf` 归因。
2. [match.go:77](../../../internal/observation/match.go#L77) 的身份签名和 [matchRefs:184](../../../internal/observation/match.go#L184) 的容器优先匹配影响跨帧保号。资源 ID 是视图模板身份的证据，未必是列表条目业务身份的证据；新路线把内容证据前置，并保留同父受限退避。
3. [diff.go:83](../../../internal/observation/diff.go#L83) 与 [diff.go:111](../../../internal/observation/diff.go#L111) 对“旧节点进入新父”和“待删子树含幸存者”回退 full；`read_diff.go` 有对应规划与应用实现。它们是现行保守语义，B2 是配套修改生成端与消费端的取舍，不能只删除守卫。
4. 实际公共观察路径是 [capture.go:240](../../../internal/engine/capture.go#L240) 经 `Differ` 对 compact `ReadTree` 计算 `ReadDelta`，不是先算内部 `Delta` 再机械转换。内部 `Diff/Apply` 仍需相同结构语义与回归保障。[inspect.go:33](../../../internal/engine/inspect.go#L33) 只读取历史详细树，不采集、不保号、不产生 delta。
5. 动作入口在 [act.go:379](../../../internal/engine/act.go#L379)、[act.go:429](../../../internal/engine/act.go#L429) 与 `scroll.go`；当前 [matchFreshNode:888](../../../internal/engine/act.go#L888) 要求完整身份及祖先链。放宽必须沿原有工作、前台、源映射和派发门禁接入。

这四处会共同影响输出成本与动作成功率，但不能推导出“任何列表变化必定全量换号”或“修完必定避免宿主落盘”。输出仍受真实节点数、内容长度和宿主阈值约束；无唯一证据时换号、无法无损表达时回退 full 都是有效结果。

## 2. 路线与共同判据

| 路线 | 做法 | 代价与失败模式 |
| --- | --- | --- |
| 最小改动 | 只过滤空叶、移除固定提示，保持匹配/差分/重验策略 | 改动小；重复模板、包装层变化和严格几何重验仍可能产生 full 或拒绝 |
| 本提案 | 四分册协同，分别建立可验证不变量 | 生成端、应用端、客户端和动作回归工作增加；缺证据时仍保守回退 |

建议采用第二条，按依赖分步验证，不把“总体方向成立”当作每一层放宽均安全的证明。

### 2.1 状态精简与事实保留

- 移除 `ReadTree`、`ReadDelta`、`InspectionView` 的 `defaults` 字段和 MCP 固定提示行，默认值由共同契约定义。
- 完整节点省略已知默认：`enabled=true`，其余四个状态位 `false`。真实未知仍为 `null`，不因省字节改成已知 true/false。合成节点只有在定义上已知时才填默认；context 的“不可动作”不能推出其源控件已启用或未选中。
- `set` 的未出现表示不修改；出现时原样表达 `false/0/""/[]/null`。完整节点的省略规则不能复制给 `set`。
- 状态归一化、比较、序列化、解码、compact 匿名资格与 Apply 必须采用同一判据。源 `Has*` 和输入授权证据不受阅读精简影响。

### 2.2 三种身份不能互相代替

`matchRefs` 建立阅读连续性；动作重验确认本次派发目标；`settle` 描述采样帧是否收敛。这三个结果各回答一个问题：保号不等于可以点击，唯一获焦不等于是请求目标，连续帧相同也不排除重复候选、未知能力或遮挡。

`settle.go` 保持现有严格摘要与采样策略，不改成放宽后的动作身份。[settle.go:45](../../../internal/engine/settle.go#L45) 的默认最小间隔分别为 act 400ms、start 300ms、open-app 600ms，不能写成统一 150ms。新重验字段若不在现有 19 字段层级摘要中，须明确其独立验证路径；不能宣称“重验是摘要真子集，收敛帧必然 100% 通过”。

### 2.3 唯一性与保守退出

每个匹配阶段都先形成候选关系，再双向消歧；候选数为一但同一个旧节点被两个新节点申请，不构成唯一匹配。不得用遍历顺序、map 顺序、最大几何重叠或 ordinal 回填制造证据。阶段产生的证据须注明来源，弱序号推断不能反过来充当强锚点。

重验缺证据或有歧义时，按既有错误族拒绝，并沿既有失败观察交付流程处理；不新增 `TARGET_NOT_FOUND/PRECONDITION_FAILED` 等错误码。已认领 O 不归还，同一 P 不二次派发。

### 2.4 Delta 是最终声明

只使用 `set/add/remove/children/replace` 五块。`add` 是完整节点定义数组，没有 `parent/index` 属性；挂载由 `children` 的完整子列表声明。根 ref 变化和同 ref 角色变化继续回退 full。B2 改变的是允许哪些结构组合以及如何原子应用，**即使字段名不变，也属于共同契约变化**。

两份应用器都应验证 final graph：ref 唯一、引用存在、单一父节点、无环、全节点从原根可达；允许的暂时脱离不能在最终结果中留下孤儿。替换优化只处理封闭区域，不吞入跨边界迁移。成功后必须满足逻辑相等与公共 canonical JSON 相等。

## 3. 工程不变式与影响面

| 不变式 | 实施约束 |
| --- | --- |
| 唯一权威、STOP、普通工作位置 | 不引入客户端或 App 第二份执行权威；不增普通动作队列；保持撤权优先、有限逻辑交接 |
| 候选与提交 | 不仅保留 `Preview/Build` 区分；正式采集仍用 `Candidate`，直到 `publishTreeAttempt` 最终门禁才 commit。Preview、被丢弃采样与失败候选不推进会话 ref 高水位 |
| 状态锁 | 树投影、匹配、差分、图校验和重验搜索在锁外；锁内保留原有有限状态检查 |
| 单向 O/P | 静态拒绝不认领；认领后仍只允许一次派发；重验拒绝不使旧 O 重新 ready |
| 事实分层 | 主动作 uncertain 不因后观察成功改判；text 点击获焦与实际写入/读回分别保留证据 |
| 严格协议 | 新旧共同契约不混用；明确完整节点与 set 的 nullable 边界，不放宽未知字段、重复键、类型和大小写校验 |
| 日志 | 沿用批准事件/计数；不记录标签、树、差分、输入正文、原始响应或异常字符串 |
| 版本 | 只手改 `release/versions.json`，通过 `scripts/generate-version.ps1` 生成常量；发布仍检查 manifest 与实际 MCP 进程工具发现 |

实施所需正式文档至少涉及 `docs/interface.md`、`docs/mcp.md`、`docs/architecture.md`，并按全库引用核对客户端、集成与发布说明。`internal/engine/inspect.go` 的两处字节预算结构也要移除 `Defaults`，不能只删协议结构导致预算继续多算。`internal/mcp/schema.go` 的工具说明同样声明了固定 defaults 行，须同步修改；工具目录指纹随工具说明变化，发布时按既有脚本和 manifest 核验。版本当前为 contract 14，目标值在实施时按当时版本源统一递增，本次不改版本文件。

字节预算也须联动核对：compact full、detailed full、inspect unit、内部 `SnapshotCost` 和总留存成本分别按新实际编码验收。`Node.checked=nil` 改为显式 null 可能增加内部成本，不能从输出精简推导留存成本必降；本提案不暗调现有预算上限。

## 4. 实施依赖与验收

统一执行入口为[分册 05：实施顺序、验证计划与交付门禁](05-implementation-and-validation-plan.md)。其中：

- §6–§7 明确落点、整组交付、版本与预算/能力边界；
- §8 列出 V01–V20 单元/算法、I01–I08 假设备/并发、B01–B06 基准、D01–D06 实机矩阵，附命令和记录要求；
- §9 明确实现前待确认项的建议、决定者和期限；
- §10 给出 S0–S10 的具体动作、依赖、可并行范围和阶段出口，区分提交前检查、实现提交后的完整制品核验与固定实现提交的发行锚点；
- §11 规定先提交实现、再补实际结果与归档，未执行项逐条说明原因。

01–04 的局部验收清单仍需映射到统一计划；05 不覆盖或放宽其算法判据。若执行顺序或验收口径出现冲突，先在提案内修订，不在实施中静默择一。计划中的命令和设备场景当前均未执行，不能用前轮基线回归或静态复审代替新方案验收。

## 5. 本次审核的结论边界

本次工作是提案修订，不是实现缺陷已修复的证明。源码基线核对、反例推演、链接检查和提案复审分别记录；没有产品代码测试、构建或设备操作，就不写“已单测/已实测”。未来缺陷实施仍遵循先红复现，未来契约/时序取舍仍须用户确认。

建议保留空叶净化、内容优先的五阶段连续性、B2 结构重组和有身份约束的动作放宽。几何近似、动态文案、焦点回退和序号兜底都只能在各自证据边界内工作，不能从“唯一候选”推导出不可观察的业务身份。确认前尚存的取舍由各分册明确列出，用户在实现前决定；实现者不得将未确认项默认为开放。

本次修订的基线检查由专题审查者在 Windows 仓库环境执行：

- `GOEXPERIMENT=jsonv2 go test ./internal/observation -count=1` 通过；实际 PowerShell 命令与三项定向回归见分册 03 的基线记录。
- `go test ./internal/engine -run 'TestActTapCompositeCardDispatchesCarrierPoint|TestActTapCardKeepsFixedPointWhenFreshMovesOutsideElement|TestActTapCardRejectsFreshCarrierMutationsWithoutReselecting|TestScrollContainerMovedRejected|TestScrollDynamicSegmentConflictRejected|TestConcurrentClaimHasSingleWinner|TestUIPreWriteDoesNotDelayStopOrDispatchAfterRevocation|TestTextFocusReadFailureDoesNotReopenEndedTap' -count=1` 通过（13.118s）。该次命令未显式设置 GOEXPERIMENT；这是八项定向测试，不是完整引擎测试。

以上仅核对现有基线，不证明提案中的新算法通过。新增算法、全部联动路径、性能基准、构建与实机验证仍属于实施验收。


## 6. 修订审核记录（2026-11-15）

以下记录前轮总览与四项设计分册的审查范围；本次新增 05 的计划编写不等于已执行其中的验证。

- 分册 01、04 由 `ollama-cloud/deepseek-v4.1-flash:1017` 专题审查者负责，分册 02、03 由 `workbuddy/deepseek-v4.1-flash` 专题审查者负责，四位均配置 max。
- 作者之外的复检：WorkBuddy 复审 01 与 04，Ollama 复审 03，主审复核 02 的公开索引、候选集合、全局唯一计数、高水位和预算，并整合跨册接口。01、03、04 的独立复检发现均经修订后取得 clean。
- 审核范围包括真实基线定位、最小反例推演、候选/提交与 O/P/STOP 边界、wire 还原、预算和消费者；未审核模板格式。clean 是本次静态审查未再发现反例，不是形式证明或新方案测试通过。
- 前轮检查覆盖当时总览与01–04设计分册的本地链接、行号范围、代码围栏、状态与基线标记、行尾空白和变更范围。开发检查环境为 Go 1.27.1、Windows/amd64；基线测试与未执行项见 §5。
- 仅修订本目录；保留“提案，未实现”和原有未跟踪状态，没有产品实现、版本修改、提交或归档。Tier 4 动态文案放宽仍是用户在实施前决定的独立取舍，默认关闭。

- 本次补充：新增分册05统一实施与验证计划，并更新本页索引与执行入口；两个指定模型各一位对计划只读复检，发现修正后均返回clean。六份文档的268个本地链接、状态/基线标记、围栏、05表格及变更范围检查通过；本轮未执行计划中的测试、构建、发布或设备步骤。
