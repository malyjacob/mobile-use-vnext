# 提案：act 静态目标拒绝的稳定错误分类与可操作详情

状态：已实现（实现提交 `a02d305f29f27fdfe6b63faf356116e137432c1a`，`2026-11-18`）。
实现与验证记录：[act-static-error-taxonomy.md](../../verification/act-static-error-taxonomy.md)。
基线提交：`4c60ca1649b79d482512e20813ddef30a9ae4678`（分支 `master`，`2026-11-17`）。
适用范围：`act` 的 `tap`/`long_press` 且 `target.ref` 在认领前的静态拒绝——错误码、`message` 与
`error.details`。不改坐标目标、`text`、`swipe`、`scroll`、`key`，不改候选顺序、落点算法、
claim/重验/派发、O/P、STOP 与 `recovery` 入口。
影响范围：`internal/protocol`（错误码、状态映射、details 白名单与测试）、`internal/engine`
（`tap_scope.go`、`act.go` 的静态入口、诊断辅助与测试）、`internal/mcp`（`act` 描述、
Instructions、投影与 parity 测试）、`docs/interface.md`、`docs/mcp.md`、`docs/release.md`、
README 与相关正式文档、`release/versions.json` 与生成版本文件、整组发布清单与部署；
Android 业务逻辑不变，但共同契约升级要求手机端整组 APK/服务/MCP 适配器与主机侧二进制配套发布。
契约（`/api/*` 字段与 `contract_version`）：改变。新增 422 code `invalid_target`、
`no_safe_point`；本次 ref 静态分支的 `unsupported_node_action` 收窄为“请求动作或提升条件不满足”；
新增受控 `error.details` 键；
`occluding_ref` 由 `occluding_refs` 取代。请求字段、`Result` 外形、12 个工具与工具目录结构不变。
目标共同契约 `16 → 17`，配套发行建议 `0.17.0`、Android `version_code 19`；本提案落盘不修改
版本源。

分诊：取舍。当前共码行为与正式契约一致，本提案改变公开错误分类，不按实现缺陷处理。
本文路线、枚举与取值均为建议，仍须按 [工程流程](../../process.md) §5 确认后才能实施。
本次仅审查与修订提案，不实施、发布或部署；不读取、不修改悬置的
`maintenance/verification/tap-targeting-and-bubble-analysis.md`，也不以其中的结论作证据。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的
`HEAD` 重新核对，重点：[tap_scope.go](../../../internal/engine/tap_scope.go)、
[act.go](../../../internal/engine/act.go)、[errors.go](../../../internal/protocol/errors.go)、
[schema.go](../../../internal/mcp/schema.go)、[interface.md](../../../docs/interface.md) 与
[versions.json](../../../release/versions.json)。

---

## 0. 摘要

`act` 的 `tap`/`long_press` 在认领前至少六种原因共用 `422 unsupported_node_action`：目标结构
不可用、目标缺动作、最近祖先缺动作、祖先不可用、没有可提升祖先、目标有动作但固定候选耗尽。
调用方只看 `code` 无法区分“该换 ref”“该换动作”“该看哪个阻塞者”，容易被误导到上层祖先找目标。

本提案只改静态错误面：拆出 `invalid_target` / `unsupported_node_action` / `no_safe_point`
三个 422 code，用封闭 `reason` 细分原因，用针对性 `details` 给出目标/祖先动作、实际作用域、
阻塞者、歧义节点与可选的 `suggested_point`。候选顺序、落点谓词、`tapFrame`、claim/重验/派发、
O/P、STOP 与 `recovery` 的规则不变；静态拒绝自身不认领 O、零设备 I/O。只有原 O 仍有效时，
才适用 O 保持 `ready` 与 `retry/O/effective`；该错误不延长 O，也不恢复已被撤销或消费的授权。
坐标目标、`text`、`swipe`、`scroll`、`key` 不在本次范围。

## 1. 问题

### 1.1 现象与代价

当前 `tap`/`long_press` 的 `ref` 静态拒绝把多种原因压在同一个 code 下：

| 实际原因 | 现在的 code | 现在的 message | 现在的 details |
| --- | --- | --- | --- |
| C 是 root / context / 无 bounds | `unsupported_node_action` | 该节点不能作为操作目标 | 可能无 |
| C 缺请求动作，且没有可证提升 | `unsupported_node_action` | 目标节点不支持点击/长按 | 可能无，或有祖先引导 |
| C 只有别的 `tap`/`long_press`/`text` | `unsupported_node_action` | 目标节点不支持点击/长按 | 无 |
| 最近祖先 A 缺请求动作 | `unsupported_node_action` | 目标节点不支持点击/长按 | 无 |
| 最近祖先 A 不可用 | `unsupported_node_action` | 目标节点不支持点击/长按 | 无 |
| 没有可提升的交互祖先 | `unsupported_node_action` | 目标节点不支持点击/长按 | 无 |
| C 有请求动作，固定候选耗尽 | `unsupported_node_action` | 无法在该节点作用域内生成可证明的点击落点 | 可有 `occluding_ref` |
| A 有请求动作，A 作用域候选耗尽 | `unsupported_node_action` | 同一句 | 无 |
| 几何不成立 | `unsupported_node_action` | 目标节点没有可用几何 | 无 |

代价是可观测的：

1. **调用方无法只按 code 决策。** 看到 `unsupported_node_action` 时，它不知道是“这个节点根本
   不支持该动作”，还是“这个节点支持该动作，但当前观察里选不出安全点”。前者应换 ref/换动作，
   后者应看阻塞者、换坐标或等页面变化；两者的下一步不同。
2. **错误分类不足以支持下一步选择。** 现有 agent 指引把 `unsupported_node_action` 与祖先
   引导放在一起，没有为候选耗尽提供独立 code。这是误选祖先的风险，不能据此声称已证明某次
   真实调用的失败分支或 agent 行为；本提案不对悬置记录中的反馈归因。
3. **候选耗尽缺少可操作细节。** 现有 `occluding_ref` 只解释目标原中心的一个阻塞者；同深并列、
   混合失败、以及“固定候选失败但平面仍有可证点”的情况都没有稳定细节。
4. **message 也不足以区分。** 例如“目标节点不支持点击”同时覆盖“C 缺动作”和“最近祖先 A 缺
   动作”；“无法在该节点作用域内生成可证明的点击落点”同时覆盖直接目标耗尽和 bubble 的 A 耗尽。

### 1.2 证据链

1. **静态入口只按一个 code 返回。** `staticTapRefCheck`
   （[tap_scope.go:1030-1088](../../../internal/engine/tap_scope.go#L1030-L1088)）对 root/context/
   无 bounds、缺动作、bubble 祖先问题分别返回 `422 unsupported_node_action`；直接目标的候选
   耗尽走 `tapNoCandidateError`
   （[tap_scope.go:135-141](../../../internal/engine/tap_scope.go#L135-L141)），bubble 的 A 候选
   耗尽走同一函数（[tap_scope.go:1013-1016](../../../internal/engine/tap_scope.go#L1013-L1016)）。
2. **缺动作引导与候选耗尽走不同出口。** `tapUnsupportedWithGuidance`
   （[tap_scope.go:771-784](../../../internal/engine/tap_scope.go#L771-L784)）在“目标缺动作”时
   尝试 `tapGuidanceFor`（[tap_scope.go:786-848](../../../internal/engine/tap_scope.go#L786-L848)）；
   直接目标候选耗尽则直接返回 `tapNoCandidateError`，只可能附 `occluding_ref`
   （[tap_scope.go:746-749](../../../internal/engine/tap_scope.go#L746-L749)）。bubble 的
   `planBubbleTapTarget` 在 `chooseBubbleTapPoint` 失败时返回同一句无落点错误，但没有任何
   details（[tap_scope.go:1013-1016](../../../internal/engine/tap_scope.go#L1013-L1016)）。
3. **静态规划确实是有限的。** `enumerateTapCandidates`
   （[tap_scope.go:638-698](../../../internal/engine/tap_scope.go#L638-L698)）只产生中心、carrier
   中心与两条中心线候选；`chooseTapPlan`
   （[tap_scope.go:726-751](../../../internal/engine/tap_scope.go#L726-L751)）逐个调用
   `provesSubtreeTapPoint`（[tap_scope.go:555-598](../../../internal/engine/tap_scope.go#L555-L598)）。
   所有候选失败后统一返回同一个错误出口，不区分失败类别。
4. **全遮挡与同深并列都能复现。** `TestActTapFullyOccludedCardRejectsBeforeClaim`
   （[act_tap_scope_test.go:950](../../../internal/engine/act_tap_scope_test.go#L950)）构造卡片与
   同 bounds 的 `ImageButton`；`TestActTapAmbiguousCardRejectsWithoutOccludingRef`
   （[act_tap_scope_test.go:978](../../../internal/engine/act_tap_scope_test.go#L978)）构造两个
   bounds 相同的非交互子节点。两者在当前实现都返回 `not_executed/unsupported_node_action`、
   零 probe/capture/派发、O 保持 `ready`；前者带 `occluding_ref`，后者不带。两条用例于
   `2026-11-17` 在本次基线重新执行通过，包结果 `1.529s`；准确命令见本节末。用例证明现有
   共码、未认领与零设备 I/O，不证明拟议的分类或建议点已具备。
5. **有限候选不能证明平面无解。** 从 `enumerateTapCandidates` 的有限枚举与
   `provesSubtreeTapPoint` 的逐点判断，只能推出“枚举点全部失败”，不能推出“基础矩形所有点
   都失败”。这是算法边界分析；本次未新增或运行十字遮挡复现，不引用悬置记录的坐标结论。
6. **静态拒绝的恢复语义是统一的。** 这些错误都发生在 claim 之前，`planRecovery` 对 act 的
   `not_executed` 且未认领的普通分支返回 `retry/O/effective`
   （[recovery.go:120-129](../../../internal/engine/recovery.go#L120-L129)）；O 仍 `ready`，
   没有后观察。该静态拒绝自身不消费 O，不保证并发 STOP、到期或其他调用之后 O 仍有效。
   新增 code 不应改变既有恢复、冻结与终态投影。
7. **MCP 目前向调用方公开的是同一个 code。** `act` 描述只写“目标不支持该动作返回 422
   `unsupported_node_action`，details 可带 `nearest_actionable_ref` 与 `safe_point`”
   （[schema.go:229-246](../../../internal/mcp/schema.go#L229-L246)），没有区分“缺动作”与
   “候选耗尽”，也没有告诉调用方后者不要向祖先找目标。
8. **正式契约同样把两种语义写在同一段。** [interface.md:390-428](../../../docs/interface.md#L390-L428)
   把 bubble 祖先失败与直接目标候选耗尽都描述为 `422 unsupported_node_action`；
   [interface.md:469-480](../../../docs/interface.md#L469-L480) 只约定静态拒绝的 recovery。
9. **共享函数也被引导与 fresh 重验调用。** `tapGuidanceFor` 调用 `chooseTapPlan`
   （[tap_scope.go:815-835](../../../internal/engine/tap_scope.go#L815-L835)）；
   `chooseUnboundTapPlan` 的 fresh 回退也调用它
   （[act_revalidation_policy.go:847-859](../../../internal/engine/act_revalidation_policy.go#L847-L859)）。
   bubble 重验的作用域或落点失败转为 `target_changed`
   （[act.go:611-623](../../../internal/engine/act.go#L611-L623)）。不能直接在共享函数里启用新诊断。
10. **检查顺序有语义。** 入口先校验公开树，再查 ref、root、context/缺 bounds、disabled、
    动作/提升；只有进入规划才校验 source/raw 拓扑与基础矩形
    （[tap_scope.go:1030-1087](../../../internal/engine/tap_scope.go#L1030-L1087)、
    [tap_scope.go:389-418](../../../internal/engine/tap_scope.go#L389-L418)）。不能把所有几何或 source
    检查前移，再声称只改变错误表达。
11. **ref 与坐标的证明不同。** 直接 ref 只检查子树；bubble 还调用整树 `resolvePoint`
    （[tap_scope.go:874-896](../../../internal/engine/tap_scope.go#L874-L896)）。坐标目标按整树最深唯一
    节点绑定 source，再在 fresh 帧证明同一节点
    （[point.go:97-130](../../../internal/engine/point.go#L97-L130)、
    [act.go:822-847](../../../internal/engine/act.go#L822-L847)）。只通过直接 ref 谓词的点，可能在同一 O
    上就被坐标路径判歧义或绑定到外部节点。
12. **目录指纹与发布门禁有明确边界。** `CatalogFingerprint` 包含工具数量、名称、描述与输入
    schema，不包含 Instructions（[catalog.go:36-68](../../../internal/mcp/catalog.go#L36-L68)）。手机清单要求
    已提交源码与 `source_dirty=false`，主机工具也要求配套手机清单
    （[verify-manifest.ps1:174-196](../../../scripts/verify-manifest.ps1#L174-L196)、
    [build-host-tools.ps1:18-32](../../../scripts/build-host-tools.ps1#L18-L32)）。正式构建、核验与部署
    必须排在实现提交之后。

本次仅重跑现有基线用例，PowerShell 命令如下；退出码为 0，两个用例均 PASS：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/engine -run '^(TestActTapFullyOccludedCardRejectsBeforeClaim|TestActTapAmbiguousCardRejectsWithoutOccludingRef)$' -count=1 -v
```

### 1.3 为什么直觉会出错

- **“只改 message 就够了”**：message 是给人读的短句，不是稳定分支面；调用方要拿 ref、枚举和
  数组做下一步决策，必须进 `details`。
- **“只给 `unsupported_node_action` 加一个 `reason`”**：按 code 分支的客户端仍然无法区分
  “缺动作”与“候选耗尽”。这次明确要求 code 级别可区分，所以至少要拆出 `no_safe_point`。
- **“候选耗尽就扩大搜索或直接派发”**：会改变现有落点契约、确定性和成本；本提案只报告，
  不改变派发。`suggested_point` 是提示，不是授权。
- **“`invalid_target` 继续用 `unsupported_node_action`”**：root/context/无几何不是能力不匹配，
  独立分类可直接指出实际规划对象的问题；但保留的 `ancestor_invalid` 仍需如实描述提升前提失败，
  不把剩余 `unsupported_node_action` 绝对化为纯能力问题。
- **“每个 reason 一个 code”**：码表会明显膨胀，调用方要维护更多分支；`reason` 是更合适的
  二级分类。
- **“只过 ref 谓词就建议坐标重试”**：直接 ref 没有整树归属保证。建议点还须在同一观察上证明
  整树唯一命中 C/A 或其非阻塞 carrier；即便如此，新调用仍须独立完成准入、认领与 fresh 证明。

## 2. 目标与非目标

**目标**

- G1 静态拒绝按原因分成三个稳定 code：`invalid_target`、`unsupported_node_action`、
  `no_safe_point`；保留 `node_not_found`、`node_disabled`、`hierarchy_unavailable`。
- G2 本次三个 code 用封闭 `reason` 细分；`reason` 是稳定枚举，不随文案变化。
- G3 `details` 给出调用方做下一步所需的最小事实：目标/祖先动作、作用域 ref、实际阻塞者、
  歧义节点、可选 best-effort 落点。
- G4 候选耗尽时，除 reason/refs 外提供有界 `suggested_point`；不改变候选顺序、不改变派发、
  不把它写成保证。
- G5 静态拒绝自身不认领、不派发、不发布观察；零设备 I/O。原 O 仍有效时沿用
  `recovery=retry/O/effective`，并发撤权/到期/消费的处理不变。
- G6 实施时同步契约、文档、测试与版本；发布按清单完成配套升级。当前审查不执行升级或部署。

**非目标**

- 不改 `enumerateTapCandidates` 的顺序、`provesSubtreeTapPoint` 的通过条件、`tapFrame` 的冻结
  内容或 `dispatchAct` 的坐标来源。
- 不改 bubble 的祖先选择：仍然是“目标完全没有三类触摸动作时，最近一个 touch-interactive
  祖先”，不按请求动作继续向上找。
- 不改坐标目标 `{x,y}` 的 `resolvePoint`/`provePointTarget`，不改 `text`、`swipe`、`scroll`、
  `key` 的任何错误或行为。
- 不拆 `node_not_found` 的“ref 不存在”与“source 映射缺失”双语义；不重做 `scroll` 的
  `unsupported_node_action`。
- 不新增请求字段、工具、恢复入口或 Android 业务逻辑。
- 不证明 Android 实际触摸消费者、z 序、透明拦截层或业务回调。
- 不处理其他提案的删除或归档，不读取或补写悬置的验证文件。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 只加 `reason` | 保留单一 `unsupported_node_action`，details 加原因 | 改动最小，不动 code 表 | 按 code 分支的客户端仍无法区分“缺动作”与“候选耗尽” |
| R2 只拆 `no_safe_point` | 新增 `no_safe_point`，`invalid_target` 继续共用旧 code | 中等，覆盖主要误导 | root/context/无几何仍与能力不匹配共码 |
| **R3 三码 + reason + 定向 details + best-effort `suggested_point`（建议）** | 拆 `invalid_target`/`unsupported_node_action`/`no_safe_point`；`reason` 细分；details 按原因给 refs；错误路径加有界探针 | 中高：契约、文档、测试、版本与发布成本 | 新 code 对旧客户端是破坏性变化；建议点还须整树证明，增加错误路径 CPU，且不保证业务结果 |
| R4 每个 reason 一个 code | 码表按原因爆炸 | 最高 | 调用方分支膨胀，码表难维护 |
| R5 扩大候选搜索或直接派发建议点 | 改 `enumerateTapCandidates`/`dispatch` | 改变执行语义与成本 | 与“只改错误面”的目标冲突，可能引入新的落点契约 |

建议 R3，保留原提案的三码和建议点方向，但收紧证据、分类边界与提示证明。R1 是最小改动，
适合接受单一 code 的调用方；R2 不区分目标结构问题；R4 增加码表维护成本；R5 改变执行策略。
是否承担 R3 的公开面与发布成本，由用户在实现前确认，本次修订不视为批准。

## 4. 判据

### 4.1 code 与 reason

只适用于 `tap`/`long_press`、`target.UseRef == true` 的认领前内容检查。新增错误仍为
`outcome=not_executed`，不附新观察、不认领、不派发。本节不覆盖入口的会话/O/请求错误。

| code | HTTP | 本次分支的含义 | `reason` |
| --- | --- | --- | --- |
| `invalid_target`（新） | 422 | 当前规划对象的结构或几何不可用 | `root` / `context` / `no_geometry` |
| `unsupported_node_action`（保留） | 422 | 目标缺请求动作，且所选提升策略不能满足执行前提 | `target_missing_action` / `target_has_other_action` / `ancestor_missing_action` / `ancestor_invalid` / `no_interactive_ancestor` |
| `no_safe_point`（新） | 422 | C 或 A 支持请求动作，但原固定候选均未通过规划谓词 | `blocked` / `ambiguous` / `owner_mismatch` / `mixed` |
| `node_not_found` | 409 | 公开 ref 不存在，或进入规划后目标 source 缺失/越界 | 不变，不附本次 `reason` |
| `node_disabled` | 409 | C 的 `enabled` 明确为 false | 不变，不附本次 `reason` |
| `hierarchy_unavailable` | 503 | 已进入的检查发现公开树、raw 拓扑、映射或投影损坏 | 不变，不附本次 `reason` |

边界：

- `unsupported_node_action` 的收窄只发生在本次分支；`scroll`、`text` 等既有错误不强加
  `reason`。`ancestor_invalid` 包含祖先禁用或无有效 bounds，不能把该 code 简称为纯能力不匹配。
- `no_safe_point` 的稳定含义是“原固定候选未通过”，不表示整个平面无解、没有 Android 触摸
  消费者，或建议点可以直接派发；即使返回 `suggested_point`，原请求仍被拒绝。
- 不按 HTTP 状态概括恢复动作。`node_not_found`、`node_disabled` 也可能是原 O 仍可用的静态
  拒绝；409 不必然表示 O 已失效，422 也不保证 O 在并发变化后仍有效。调用方先看 `recovery`，
  再用 code/reason 调整参数，新工作使用新 P。
- 本次错误只描述原观察上的规划事实。到期、STOP、冻结、会话终态或 O 被其他工作消费时，
  沿用既有 Engine 投影；不因错误或建议点恢复授权。

### 4.2 静态判定树

保留基线的检查顺序；特别是不能把“非空但面积无效的 bounds”或全部 source/raw 检查提前到
能力/提升分支之前。下列 `build`/`collect` 的失败按原检查顺序传播，再在静态边界映射已知几何
错误；不按 message 文本或任意 422 猜原因。

```text
view = collectTapPublicView(O)        # 原有失败原样返回
C = view.byRef[请求 ref]
if C 不存在: 409 node_not_found
if C 是 root: 422 invalid_target(reason=root, scope=target, scope_ref=C)
if C 是 context: 422 invalid_target(reason=context, scope=target, scope_ref=C)
if C.bounds == nil:
    422 invalid_target(reason=no_geometry, scope=target, scope_ref=C, 原有 guidance?)
if C.enabled 明确为 false: 409 node_disabled
if C 有请求动作:
    input = buildTapInput(O, view, C)  # source、拓扑失败仍走原分支
    scope = collectTapScope(input)
    if 已知几何错误: 422 invalid_target(reason=no_geometry, scope=target, scope_ref=C)
    if 其他错误: 原样返回
    plan = 原直接目标规划
    if 成功: return 原 plan
    return no_safe_point(scope=target, scope_ref=C, 失败诊断与可选建议点)

if effective bubble && !isTouchInteractive(C):
    A = nearestInteractiveAncestor(view, C)
    if A == nil: unsupported_node_action(reason=no_interactive_ancestor)
    if A 明确 disabled 或公开 bounds 无效:
        unsupported_node_action(reason=ancestor_invalid, ancestor_ref=A)
    if A 缺请求动作:
        unsupported_node_action(reason=ancestor_missing_action, ancestor_ref=A)
    按原 planBubbleTapTarget 顺序构建 A input、检查 C source、构建 A scope
    if 已知几何错误: invalid_target(reason=no_geometry, scope=ancestor, scope_ref=A)
    if 其他错误: 原样返回
    plan = 原 bubble 规划
    if 成功: return 原 plan
    return no_safe_point(scope=ancestor, scope_ref=A, ancestor_ref=A, 失败诊断与可选建议点)

if isTouchInteractive(C): unsupported_node_action(reason=target_has_other_action)
return unsupported_node_action(reason=target_missing_action, 原有 guidance?)
```

`nearestInteractiveAncestor` 仍只选最近一个含 `tap`/`long_press`/`text` 的祖先，跳过 context，
不越过第一个交互祖先（[tap_scope.go:913-933](../../../internal/engine/tap_scope.go#L913-L933)）。
祖先引导 `tapGuidanceFor` 遇到 context 则停止，两者不能合并成同一个查找规则。
`isTouchInteractive` 仍不包含单独的 `swipe`。因此 C 只有 `swipe` 且不提升时，仍属于
`target_missing_action`，但 `target_actions` 必须如实为 `["swipe"]`。

C 的非空、零面积 bounds 若尚未走到几何规划，不用新几何检查压过现有 disabled/动作/提升分支。
A 公开 bounds 有效、但 raw 与屏幕/公开 bounds 的交集为空时，标明 `scope=ancestor` 的
`invalid_target(no_geometry)`；不能把原 child 误报为几何错误对象。

### 4.3 message

message 是受控短句；code 与 details 是机器分支面，不解析 message。文案只插入封闭动作名、
作用域名和动作枚举，不插入 label、正文、原始异常或设备输出。

| 条件 | message 模板 |
| --- | --- |
| `invalid_target` / `root` | `root 节点不能作为操作目标` |
| `invalid_target` / `context` | `context 节点不能作为操作目标` |
| `invalid_target` / `no_geometry` | `{对象}没有可用几何` |
| `unsupported_node_action` / `target_missing_action` | `目标节点不含点击、长按或文本输入能力` |
| `unsupported_node_action` / `target_has_other_action` | `目标节点不支持{请求动作}，公开动作：{目标动作}` |
| `unsupported_node_action` / `ancestor_missing_action` | `最近的交互祖先不支持{请求动作}，公开动作：{祖先动作}` |
| `unsupported_node_action` / `ancestor_invalid` | `最近的交互祖先不可用` |
| `unsupported_node_action` / `no_interactive_ancestor` | `没有可提升的交互祖先` |
| `no_safe_point` / `blocked` | `未能从{对象}的固定候选中证明{请求动作}落点：候选点均受阻塞` |
| `no_safe_point` / `ambiguous` | `未能从{对象}的固定候选中证明{请求动作}落点：候选点均有归属歧义` |
| `no_safe_point` / `owner_mismatch` | `未能从{对象}的固定候选中证明{请求动作}落点：候选点归属不符` |
| `no_safe_point` / `mixed` | `未能从{对象}的固定候选中证明{请求动作}落点：存在多类失败` |

`{对象}` 按 scope 取“目标节点”或“提升祖先”；动作按 `tap`、`long_press`、`text`、`swipe`
固定顺序映射为“点击、长按、文本输入、滑动”。空动作列表文案为“无”，wire 仍为 `[]`。
不把“均”扩展到未测试的坐标；列表截断不改变全部候选的 reason 归集。

### 4.4 details

| key | 类型 | 出现条件 | 说明 |
| --- | --- | --- | --- |
| `reason` | string | 本次三个 code 的静态出口 | 封闭枚举，见 §4.1；不要求其他工具同名 code 都带该键 |
| `target_actions` | `[]string` | 本次 `unsupported_node_action` | C 的完整公开动作，按 `tap`/`long_press`/`text`/`swipe` 排序、去重；空数组必须为 `[]` |
| `ancestor_ref` | string | `ancestor_missing_action` / `ancestor_invalid` / `no_safe_point(scope=ancestor)` | 选中的最近交互祖先 A |
| `ancestor_actions` | `[]string` | `ancestor_missing_action` / `ancestor_invalid` | A 的完整公开动作，排序与空数组规则同上 |
| `scope` | `target` / `ancestor` | `invalid_target` / `no_safe_point` | 本次结构检查或规划的实际对象 |
| `scope_ref` | string | 与 `scope` 同时出现 | C 或 A 的 ref；祖先几何错误由它标明对象 |
| `occluding_refs` | `[]string`，最多 3 个 | 至少一个原候选归为 `blocked` | 对这些候选有实际阻塞证据的 owner |
| `ambiguous_refs` | `[]string`，最多 3 个 | 至少一个原候选归为 `ambiguous` | 首个失败解析层上的最深并列节点 |
| `owner_refs` | `[]string`，最多 3 个 | 至少一个原候选归为 `owner_mismatch` 且能定位 owner | 实际命中但不在允许作用域的节点；不伪造空 ref |
| `suggested_point` | `{x,y}`，非负整数 | `no_safe_point` 且探针完整证明通过 | 仅表示该 O 上的提示，不是执行授权 |
| `suggested_carrier_ref` | string | 建议点的唯一公开 owner 是非阻塞 carrier | 必须与建议点一起出现；解释归属，不表示该 ref 有请求动作 |
| `nearest_actionable_ref` / `safe_point` / `safe_carrier_ref` | 既有 | 仍在基线 `tapUnsupportedWithGuidance` 尝试引导的位置 | 通过条件与原子组语义不变，包括原有缺 bounds 分支；不因新诊断扩大尝试范围 |

约束：

- ref 列表只归集**原规划实际测试过的候选**，不混入探针失败或整棵树的其他节点。逐候选先
  取事实，再跨候选去重、排序、截断；出现两类及以上失败才是 `mixed`，同一类多个 owner 不是。
- 阻塞 owner 每点只取最深者，同深取先序较早者，沿用 `occludingTapRef` 的选择规则；最终
  `occluding_refs` 按 owner 深度降序、公开先序升序取前 3。其他两个列表按公开先序取前 3。
- 对歧义只报告首个失败的解析层：先子树，子树唯一且合法后才有 bubble 的整树歧义。不是
  收集所有与该点相交的节点。列表可截断，但不得宣称是完整歧义集合。
- 无证据的 ref 列表省略，不输出 `null`、`[]` 或空字符串充当事实。已声明必出的动作数组
  则不同：即便为空也输出非 nil 的 `[]`，HTTP、MCP 与 P 视图均保留。
- `owner_mismatch` 的健康输入正例是 bubble 整树唯一命中外部节点。source 缺失、坏图、
  不可遍历等是原有错误，不塞进该枚举；直接目标不得凭空附外部 `owner_refs`。
- 旧引导组仍为 `nearest_actionable_ref` 与 `safe_point` 同时出现，可选
  `safe_carrier_ref`；新建议组为 `suggested_point` 与可选 `suggested_carrier_ref`。
  两组适用出口不同，不互换，不拼成一个“可直接执行的替代目标”。
- `occluding_ref` 在本次静态候选耗尽的 wire 出口由 `occluding_refs` 取代，不保留别名；
  不要求删除仍服务于内部基线规划的辅助函数，也不改其他工具的错误。

### 4.5 候选耗尽的诊断与 `suggested_point`

只在原规划失败后诊断。令 N 为公开节点数，K 为原规划尝试的候选数；直接目标最多为 carrier
数量 + 3，bubble 还可能先测试 child 中心。K 随树规模增长，**不是最多 9**。bubble 的诊断必须
覆盖原 child 中心及其裁剪回退、随后原祖先候选；不能只重放 `enumerateTapCandidates(A)`。

对每个原候选，按原谓词的短路顺序归类：

1. 点落入 `scope.blocked` → `blocked`，取该点阻塞 owner。
2. 否则，子树最深节点并列 → `ambiguous`，取这些并列节点。
3. 子树唯一命中 C/A 或合法 carrier 后，直接目标的原谓词通过；bubble 继续整树解析：并列
   → `ambiguous`；唯一但不是 A/carrier → `owner_mismatch`，记录命中节点。
4. 在健康 scope 上，基础矩形内至少有 C/A 参与解析，且非阻塞后代有 carrier 映射。空候选、
   无 owner、坏 source 等不能靠 `mixed` 掩盖；实施时用不变量测试覆盖，若发现基线可达的
   其他拒绝原因，须先修订本表与 reason 再实现，不能自行改接受条件或吞成成功。
5. 所有候选都统计完，单一类别取该类别，多类别取 `mixed`。三个 ref 列表的上限不截断分类。

诊断应复用谓词内核或只在失败路径启用的诊断模式，证明通过条件与原 bool 谓词逐点一致。
不得维护一套接受条件不同的“近似诊断”。共享规划函数的正常调用不默认生成诊断或建议点。

建议点采用固定有界探针，不新增请求参数或未定义的时间预算：

- 只在原固定候选全失败、scope 有效后，对 `scope.base` 取 3×3 点，y 外层、x 内层，各轴按
  `L + floor(W/6)`、`L + floor(W/2)`、`L + floor(5W/6)`，W 为半开区间宽度。用商余拆分避免
  `5*W` 溢出；去重，跳过原规划测试过的坐标，最多检查 9 个不同新点。
- 每个点先满足原分支规划谓词；直接 ref 还必须通过与 `provesGuidancePoint` 等价的整树
  唯一归属/source 检查，bubble 本已要求该检查。唯一 owner 必须为 C/A 或其合法 carrier。
  同一 O 上整树歧义、外部唯一命中、source 无法绑定的点一律不建议。
- 第一个完整通过的点为 `suggested_point`；若唯一 owner 是 carrier，附
  `suggested_carrier_ref`。这是网格命中点的归属说明，不伪称它来自原 carrier 中心候选。
- 所有探针失败则整体省略建议组；窄矩形去重后可能不足 9 点。省略不证明平面无解。
- 探针不加入 `tapPlan`/`tapFrame`，不改原 422，不触发自动认领、重试或派发。调用方选择
  坐标重试时须新 P；O 必须仍有效，坐标路径重新准入并在 fresh 帧证明。
- 这些检查仅访问不可变投影，均在状态锁外，不做 I/O；不创建普通 work，也不延长租约或 O。
  STOP/终态不等待探针。冻结的 P 不得被迟到的诊断结果覆盖，沿用既有结果冻结机制。

## 5. 算法

```text
Act 原有注册、会话/O 静态准入
    -> 锁外执行原 ref 内容检查与规划，保留原检查顺序
    -> 成功：原 plan 原样进入 acquireWork / claim / fresh / dispatch
    -> 非本提案覆盖的错误：原样传播
    -> 本次静态拒绝：
         root/context/已知几何失败：构造 invalid_target + 对象 scope
         缺动作/提升前提失败：构造 unsupported_node_action + 原因/动作/祖先事实
         固定候选耗尽：
             只在该静态出口重放原候选并按同一内核归集 reason/refs
             最多 9 个网格新点，完整证明后附建议组
             构造 no_safe_point
         按基线位置决定是否保留原有 guidance 原子组
    -> 原 finishWithError / P 冻结投影；不 acquireWork、不 claim、不读取设备
```

时序：`准入快照 → 锁外规划 → 失败诊断/可选探针 → 原结果结束路径`。
期间 O 可能到期、被其他调用认领或随 STOP 退休；诊断只说明快照，不抢占工作位置或重新发布 O。
原错误/P 结果仍服从既有终态与冻结规则；`retry/O/effective` 不能作为将来输入有效性的保证。

不能把新错误构造和探针无条件装进 `chooseTapPlan`、`collectTapScope`、
`chooseBubbleTapPoint` 或 `provesGuidancePoint`。这些函数仍被祖先引导与 fresh 重验复用：
引导失败仍整组省略；fresh 失败仍沿用 `target_changed`/`hierarchy_unavailable`，不返回
`no_safe_point`，不搜索建议点。需要区分内部失败时用封闭内部类型/分支信息，不解析错误文案。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/protocol/errors.go` | 新增 `CodeInvalidTarget`、`CodeNoSafePoint` 与 `Status` 422 映射；扩展受控 details 注释；明确本次 reason 不覆盖其他工具的同名 code |
| `internal/engine/tap_scope.go` | 只在静态出口映射分类、归集候选失败与构造建议点；保留共享规划的通过条件与默认成本；`occluding_ref` 只在新静态 wire 出口迁移 |
| `internal/engine/act.go` | 保持 `staticActContentCheckWithTap` 的分支入口与顺序；只接收新的静态错误；`tapPlan`/`tapFrame`/`dispatchAct` 不改 |
| `internal/engine/act_revalidation_policy.go`、`act.go` 的 fresh 分支 | 作为共享调用核查边界，不默认修改；测试证明无新诊断/探针、原 fresh 错误和执行坐标不变 |
| `internal/engine/act_tap_scope_test.go` | 更新全遮挡/歧义/context 阻塞用例；新增 code/reason/details/探针用例 |
| `internal/engine/action_contract_test.go` | bubble 祖先缺动作/不可用/无祖先、`target_has_other_action`、显式 false 回归 |
| `internal/engine/tap_static_error_test.go`（新） | 诊断归集、列表去重/上限/顺序、建议点、零设备 I/O、recovery |
| `internal/protocol/errors_test.go` | 新 code 状态映射、details 深拷贝（数组/嵌套点） |
| `internal/mcp/schema.go` 与测试 | `act` 描述与 Instructions 的调用方指引；说明静态拒绝不消费 O、只按 recovery 选择恢复入口；目录指纹与 Instructions 分别验证 |
| `internal/mcp/result_parity_test.go`、`result_manifest_test.go` | 新 details 的文本/结构化投影 parity |
| `docs/interface.md` | 409/422/503 码表、details 白名单、act 静态检查、bubble 段、result 分支 |
| `docs/mcp.md` | caller 指引：`no_safe_point` 不要向祖先找目标；details 用法 |
| `docs/release.md` | 按实际版本源同步具体版本示例；既有通用发布规范不重复扩写 |
| `README.md` | 如 `act` 行或错误说明涉及，按实际改；不新增大段 |
| `release/versions.json` 与生成文件 | 实施前复核建议值 `0.17.0` / `17` / `19`；只改版本源，再运行 `scripts/generate-version.ps1` |
| `scripts/build-release.ps1`、`verify-manifest.ps1`、`build-host-tools.ps1`、`deploy.ps1` | 复用既有构建/核验/部署入口，不因错误分类新增脚本语义 |
| `maintenance/verification/act-static-error-taxonomy.md` | 实现后按 §8.3 写记录 |

### 6.2 配置与取值

- 不新增配置项；code/reason 为封闭枚举，不接受请求自定义。
- 每类 ref 列表最多 3 项，足够提供有限的下一步选择；它是响应容量上限，不是完整失败统计。
- 网格固定 3×3，作为有限提示成本取值，不能声称已证明覆盖真实页面。坐标及去重规则按 §4.5，
  不能只用“现有中点算法”代替 1/6 与 5/6 的定义。增大网格或改比例须修订提案并重新确认。
- 不引入额外超时/预算参数，不把“预算不足”留为未定义的省略条件。既有交付预算照常生效。
- 版本建议值 `0.17.0` / `17` / `19`；实施前复核版本源与并行发布计划。`source_commit`
  按现有脚本定义填写版本冻结基线，不等同于发布清单记录的构建 HEAD。

### 6.3 日志与可观测性

- 沿用 `act.tap_plan` 的既有事件与 `action`/`mode`/`proven`/`ms`；未产生 plan 的出口不为
  日志而伪造 plan。不新增坐标、ref、树或正文日志。
- code/reason 为响应事实，不新增事件或日志键；不能把 `APIError.Error()` 或 details map
  整体写入日志。错误 details 允许传递不等于日志允许记录。
- `suggested_point` 坐标不写日志、不写账本、不进发布清单；
- 既有 `act.point`/`act.tap_plan` 的白名单不变。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| Engine 唯一权威 | 错误分类与诊断仍在 Engine；HTTP/MCP 不重算 |
| O 单向消费 | 静态拒绝自身不认领/退休 O；不延长 O，不把并发已消费或已退休的 O 返还为 ready |
| 同一 P 不二次派发 | 错误路径零派发；成功路径仍只一次 |
| 提交条件化与 STOP | 诊断纯 CPU 且不占 work；STOP 独立撤权与收尾，迟到提示不成为授权或覆盖已冻结 P |
| 短状态锁 | 诊断与探针在状态锁外；不等待设备/磁盘/网络 |
| 事实分层 | 静态错误不声称设备/业务结果；`suggested_point` 只是提示 |
| 日志白名单 | 不新增树/正文/坐标日志；错误 details 不是日志 |
| 单一版本源 | 版本只在 `release/versions.json` 手改，生成文件由脚本产出 |

### 6.5 契约与文档影响

- 新增 code、details、祖先几何的 scope 与 `occluding_ref` → `occluding_refs` 都是 wire 变化。
  基线 `contract_version=16`，建议下一共同契约为 `17`；实施前若版本已前进，按实际发布计划
  重新确定，不能回退或复用已占用的契约号。普通接入版本不匹配沿用
  `409 contract_mismatch`；不要将该规则扩展到独立的最小 STOP 等通道。
- 配套发行建议 `0.17.0`、Android `version_code 19`；控制 App 无业务逻辑变化，但共同契约与
  生成版本常量随整组发布更新。
- 手机端：服务、控制 APK、手机 MCP 适配器整组构建/清单核验/覆盖安装；保留 App 私有数据，
  不卸载、不清库。
- 使用主机适配器时，按 `scripts/build-host-tools.ps1` 重建两个 exe 与主机清单，再执行
  `-VerifyOnly`；仅替换 exe 或查看 `-version` 不足以证明配套。实际 MCP 宿主须重启相应进程/
  连接并重新发现工具，用户宿主由用户操作，单纯替换磁盘文件或重连旧适配器不算完成。
- `CatalogFingerprint` 随 `act` **工具描述**变化而变化；它不包含 Instructions，后者须独立
  核对。保持 12 个工具与输入 schema 不变，不能据指纹一致声称宿主已刷新 Instructions。
- 手机与主机正式清单均在实现提交后的干净源码上生成并核验；开发构建不可冒充发布产物。
  归档提交会改变 HEAD，届时不得用旧清单对新 HEAD 冒称通过。
- 涉及 `docs/` 的实现提交必须由独立、只读的文档审计 subagent 审核；发现项修正并复检后
  才能提交。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 在本次 ref 静态分支，调用方按 `code` 可区分：规划对象结构不可用（`invalid_target`）、
  请求动作/提升前提不满足（`unsupported_node_action`）、有动作但固定候选耗尽（`no_safe_point`）。
- `reason` 让同一 code 下的目标缺动作、祖先缺动作、祖先不可用、无祖先、阻塞、歧义、
  归属不成立、混合失败都能稳定区分。
- `occluding_refs`/`ambiguous_refs` 给出有证据的有限集合，减少把候选耗尽误读成缺能力的风险；
  不承诺改变或保证 agent 行为。
- 候选耗尽时，`suggested_point` 可提供原 O 上同时通过作用域与整树归属证明的提示。是否发起
  坐标新动作由调用方决定，须先按 recovery 处理授权，再独立重验。
- 目标是保持成功路径与执行语义不变；需要回归证明，不能因声明“只改错误面”免除共享路径核查。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| `no_safe_point` 仍然可能返回 | 固定候选全部失败；这是拒绝，不是“已找到替代点” |
| 没有 `suggested_point` | 有限网格可能漏解，整树证明也可能不通过；省略不证明平面无解 |
| `suggested_point` 经坐标 `act` 仍失败 | O 可能到期/失效，或 fresh 帧变化；原 O 上可证明不等于后续授权或执行保证 |
| 点到 `suggested_point` 但业务不符合预期 | 树模型只证明几何/归属，不证明 Android 触摸消费者或业务回调 |
| 跨同级/跨窗口 z 序、透明拦截层、WebView/canvas 内部 | 不在公开树证明范围 |
| `node_not_found` 仍同时表示 ref 不存在与 source 映射缺失 | 本次不拆；属内部不变量，另行评估 |
| `scroll` 仍用 `unsupported_node_action` 表示容器/轨迹问题 | 本次只改 tap/long_press ref 静态路径 |
| 坐标目标、`text`、`swipe`、`scroll`、`key` 的错误不变 | 非目标 |
| 旧客户端按 `unsupported_node_action` 判断“无落点” | 新 code 是破坏性变化；必须整组升级并改分支 |
| 错误诊断增加 CPU | 原 K 个候选诊断与最多 9 个新网格点都会遍历树；列表上限不是 CPU 上限 |
| 祖先被禁用/无 bounds 的具体原因 | `ancestor_invalid` 只给 ref/actions；enabled/bounds 由调用方从树中读取 |
| bubble 的 A 公开 bounds 有效但基础交集为空 | 公开 bounds 不能保证 raw/屏幕交集非空；按 `invalid_target(no_geometry,scope=ancestor,scope_ref=A)` 如实指出对象 |

### 7.3 成本

- 成功路径：不启用诊断、列表归集或网格搜索，不额外遍历树。不承诺未经测量的“零额外分配”；
  实现应复用已有候选与内核，不为诊断在所有成功调用中保存完整失败轨迹。
- 失败路径：重放 K 个原候选会增加与现有规划同阶的工作，逐点扫描树的部分为 `O(KN)`；
  最多 9 个网格点另增 `O(9N)`，直接 ref 提示比原判据多一次整树归属检查。另计原候选生成/
  区间处理成本；不能声称总共只多 9 次遍历，也不宣称固定毫秒上限。
- 内存随已有树、候选与遍历状态增长；ref 输出每类最多 3 个，可在归集中保留有序前三项，
  不必持有全部诊断树。所有新增工作仅 CPU，不新增设备读取、RPC、日志正文或账本写入。
- 响应：新增 details 是小数组/一个点对象，受现有结果交付预算约束；不含树、标签或正文。
- 发布：共同契约升级导致整组构建、清单、手机覆盖安装与主机二进制替换；这是一次真实的
  版本成本，不只是文案改动。

## 8. 验证计划

以下是实现后的验收要求，当前只执行了 §1.2 所列的两个基线用例；没有实现新错误，也没有
新增分类的先红结果、性能测量、实机或发布结论。只有提案修订无需构建/部署产品。

### 8.1 单元测试

分类用例覆盖 `tap` 与 `long_press`，并按适用分支覆盖省略/显式 true/显式 false 的 bubble。
先用旧实现可编译的字符串期望写新行为断言，确认实际结果先红；不以“新常量尚不存在导致
编译失败”代替复现。基线符合旧契约，先红用例证明拟议差异，不标作已确认的实现缺陷。

| 用例 | 期望 |
| --- | --- |
| 全遮挡卡片（同 bounds 可点击子节点） | `no_safe_point/blocked`、target scope、正确 `occluding_refs`；未认领、零设备 I/O |
| 同深并列卡片 | `no_safe_point/ambiguous`，仅归集最深并列节点；无 `occluding_refs` |
| 多个原候选分别阻塞/歧义 | `mixed`，两类列表出现；同一候选被阻塞且存在并列时只记首个失败 `blocked` |
| bubble child 中心失败、随后祖先候选失败 | 原 child 中心及裁剪回退计入诊断；原顺序与重复点行为不改变 |
| bubble 整树唯一命中外部兄弟 / 同深并列 | 分别 `owner_mismatch` 与 `ambiguous`，正确 owner/并列 refs，零派发 |
| 多于 3 个 refs、跨候选重复 owner | 全部原候选参与 reason 统计；列表去重并按 §4.4 排序截断，稳定可重现 |
| C 缺动作且 bubble=false，可证祖先 | `target_missing_action`、`target_actions=[]`，原有 guidance 原子组保留 |
| C 只有 swipe，未提升 | `target_missing_action`、`target_actions=["swipe"]`；不误报为其他 touch 动作 |
| C 有其他 touch 动作，缺请求动作 | `target_has_other_action`；不提升；动作数组完整、去重、有序 |
| 默认/显式 true 提升，无 A / A 缺动作 / A 禁用或 bounds 无效 | 分别 `no_interactive_ancestor` / `ancestor_missing_action` / `ancestor_invalid`，祖先事实准确；不找更远祖先 |
| A 公开 bounds 有效但 raw/屏幕交集为空 | `invalid_target/no_geometry`、`scope=ancestor`、`scope_ref=A` |
| root / context / bounds=nil / 已进入规划的空几何 | `invalid_target` 相应 reason、target scope；原有缺 bounds 引导按基线判据保留 |
| 多个条件同时失败 | 保留公开树→ref→root→context/缺 bounds→disabled→动作/提升→source/几何的原检查顺序；零面积非空 bounds 不被提前拒绝 |
| ref/source 缺失、坏 raw 拓扑、坏投影 | 仍为既有 `node_not_found` 或 `hierarchy_unavailable`，不混入候选失败或 `mixed` |
| 诊断与 bool 谓词对照 | 在健康合成 scope 上逐点比较直接/bubble 接受结果；所有原失败点可归类，不用 `mixed` 掩盖未知原因 |
| 网格正例 | 新建十字遮挡合成页，先证明原候选耗尽，再证明至少一个网格新点通过作用域与整树/source 检查；返回行优先首点，原请求仍拒绝 |
| 网格反例 | 完全覆盖、整树歧义或外部唯一命中时不返回相应点；找不到完整证明则省略建议组 |
| 网格整数边界 | 1px/窄矩形、非零起点、大整数、重复分位点与原候选重合；非负且在半开 bounds 内，最多 9 个不同新点，无溢出 |
| 建议点的 carrier | carrier owner 时才附 `suggested_carrier_ref`，仅命中 C/A 时省略；不从“网格点来自某个中心”推断 |
| 成功与 fresh 对照 | 普通按钮、卡片、bubble、原有 guidance 与 fresh 回退接受结果、派发坐标、重验错误不变；成功、引导内规划与 fresh 内规划不启用新诊断/探针 |
| O/P 与恢复 | 无并发变化时 O 仍 ready、`retry/O/effective`；同 P 返回原结果，不能改参数重派；新 P 才发起新动作 |
| STOP/到期/冻结并发 | 新增 CPU 工作不占 work、不持长锁、不阻塞 STOP；不恢复已撤权/消费 O，不覆盖冻结 P；按既有恢复/终态规则检查 |
| 协议与投影 | 新 code 的 HTTP 422 映射；空动作数组编码为 `[]`；ref 列表/嵌套点在 HTTP、MCP 文本/结构化结果、重复 P 与 `status(P)` 中保真且深拷贝 |
| MCP 指引与指纹 | 描述说明三类静态错误及有限提示；12 工具与输入 schema 不变；工具描述变化改变目录指纹，Instructions 独立验证 |
| 旧键迁移与范围反例 | 本次静态 wire 不再出现 `occluding_ref`；坐标/text/swipe/scroll/key 的错误、引导失败省略与 fresh `target_changed` 不被新映射污染 |

至少保留分类及网格证明的反向对照：回退分类映射后新 code/reason 断言失败；去掉整树校验后，
建议点的外部命中/歧义反例失败。若重构内核，原谓词逐点等价与共享调用回归是实施门禁。

建议命令（PowerShell；实现时按实际测试名和改动范围收敛）：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/engine -run 'Tap|LongPress|Bubble|Guidance|StaticError|Recovery' -count=1
go test ./internal/protocol ./internal/mcp ./internal/httpapi ./internal/client -run 'Error|Details|Schema|Contract|Act|Result|Catalog|Instructions' -count=1
powershell.exe -NoProfile -File scripts/generate-version.ps1 -Check
```

交叉构建另在 `GOEXPERIMENT=jsonv2`、`GOOS=linux`、`GOARCH=arm64`、`CGO_ENABLED=0` 环境
执行 `go build ./...`；不能在该环境中尝试运行 linux 测试二进制。对代表性大树记录 N、K、
原规划/新增诊断耗时与分配，分别报告成功、全失败、bubble 和网格路径；不把合成测量写成实机
延迟保证，不无依据承诺零分配或固定毫秒预算。

### 8.2 实机/系统级

当前未执行。单元合成树是分类、禁用/无 bounds、原候选归集与网格边界的主要验证方式，
不能假定真实 UI 能稳定构造每一种公开树形态。系统级与发布验证须分别记录：

- 接入层：以可控合成 Engine 结果验证实际 HTTP/MCP 传输的 code、details 数组/对象、空数组与
  投影，不为验证改公共接口。旧契约普通请求应 `409 contract_mismatch`；最小 STOP 通道不扩大
  到普通契约检查中。
- 实机样本：仅选择设备上可重复构造的遮挡/歧义/提升/提示页面，对照实际 O 与目标 ref；
  验收 code/reason/details 与该帧事实一致。原 O 仍有效且无并发时确认 ready；不认领、不派发、
  无后观察。设备 I/O 的严格计数用 harness 证明，不仅凭“页面没变化”断言零派发。
- 有建议点时，只有 recovery 允许、O 仍有效且确实需要该动作才以新 P 发坐标请求。分别记录
  提示证明、后续准入/fresh 拒绝、执行事实与页面结果，不把它们合成一个 success。
- 发布：实现提交后的干净树运行 `build-release.ps1`，再显式带
  `-Manifest build/release/manifest.json` 核验；使用主机适配器时另运行
  `build-host-tools.ps1` 与 `build-host-tools.ps1 -VerifyOnly`。核验源码 HEAD、共同版本、
  APK 身份、产物 SHA-256/大小和工具目录；再按 `deploy.ps1` 覆盖安装，保留私有数据。
- 最小闭环限于本次错误/成功路径、配套契约、App 配对、当前准入与恢复入口、实际 MCP 进程的
  工具重发现。记录已有 STOP/退休 cleanup 事实，不扩展无关设备/功能矩阵。无法构造的页面、
  未执行部署、未刷新用户宿主逐项写明，不声称整组升级完成。

### 8.3 记录

实现后写 `maintenance/verification/act-static-error-taxonomy.md`：分诊、代码提交、环境、
先红断言与真实结果、同类/共享路径核查、测试/构建命令、性能数据、清单/部署与可构造实机样本、
未执行项及原因、docs 审计结论。记录本次变化，不读取或补写悬置的验证文件。

实现提交前可写当时真实执行的单测/开发构建记录，不能预写发布成功；实现提交后再追加正式
清单与部署结果。归档记录必须指明产物实际构建的实现 HEAD，不能用归档提交号替代。

## 9. 已定与未决

### 9.1 已定

本表是本版提案收敛的建议，不表示用户已确认实现。

| 事项 | 结论 |
| --- | --- |
| 分诊 | 取舍：基线符合现行共码契约，拟改变公开错误面 |
| 建议路线 | R3：三码 + 封闭 reason + 定向 details + 需整树证明的有限建议点 |
| code | 建议 `invalid_target`、`no_safe_point`；原 `unsupported_node_action` 在本分支保留动作/提升前提失败，其他分支不动 |
| reason/scope | 按 §4.1；结构错误也标明实际 scope，保持原检查优先级 |
| 列表与网格 | 各类 refs 最多 3 项；固定 3×3 网格最多 9 个新点；并非搜索完备性或 CPU 总量保证 |
| 执行边界 | 不改候选/接受条件、frame、claim/fresh/dispatch、O/P、STOP 与恢复规则；新增诊断限静态失败出口 |
| 提示边界 | 原请求仍拒绝；提示不占 work、不延长授权；坐标重试须新 P 与独立证明 |
| 范围 | 只修订本提案；其他操作错误、node_not_found 拆分与 scroll 分类均为非目标，不悬空留作本次实施项 |
| 悬置材料 | 不查看、不修改指定验证文件；不删除/归档其他提案，不借本次审查宣告既往取舍 |
| 版本建议 | 基线 0.16.0 / contract 16 / Android 18；建议下一组 0.17.0 / 17 / 19，实施前复核 |
| 发布门禁 | 实现提交后干净树构建/核验，再部署；实际 MCP 宿主刷新需单列结果 |

### 9.2 未决

| 事项 | 谁定 / 何时定 |
| --- | --- |
| 是否接受 R3 的范围、code/reason/details、网格提示与版本成本 | 用户在实施前按流程 §5 明确确认本版提案；本次审查修订不视为实现批准 |
| 实际发行号与是否并入其他发布 | 实施者在动版本源前核对 HEAD 与发布计划；若已占用建议值，先更新提案并由用户确认受影响契约，不自行复用或回退 |
| 实机可构造样本与发布窗口 | 实施者在系统验证前列出实际设备/页面条件；发布操作者确定窗口。不可构造或未获部署指令时，记录未执行，不阻止如实报告单测结果 |

message 的最终短句可在 §4.3 模板内校订；枚举、网格取值、分类优先级、提示证明或公开字段
发生变化，则先修订并重新确认，不留给“实现评审时看着办”。

## 10. 实施顺序

1. 用户确认本版提案；实施前复核 HEAD、版本源、共享调用与 §9.2。当前阶段到提案修订为止。
2. 在旧实现上加入可编译的新行为断言并记录先红，覆盖分类、顺序、原候选诊断与提示的整树反例；
   不先改实现再补“先红”记录。
3. 实现 protocol 码表/details 与 Engine 静态出口映射、等价诊断、建议点。共享规划默认关闭
   诊断；fresh、guidance 与其他 operation 用回归保护。通过本次路径测试与反向对照。
4. 同步 MCP 指引与正式 docs；单点更新实际版本源并生成常量。版本、code、scope、错误白名单、
   命令和链接完成自检后，交独立只读文档审计 subagent；审计期间不修改被审的 docs，等待结论，
   发现项修正后复检为 clean。
5. 完成对应单测、交叉构建、版本生成检查与真实记录。**先提交实现**，提案仍是“提案，未实现”；
   提交说明写分诊、变更边界、先红/回归结果与文档审计结论。不得为正式清单核验跳过干净树门禁。
6. 已安排发布时，在该实现提交的干净树上构建手机整组与所用主机工具、生成并核验清单，再部署
   与执行 §8.2 最小闭环；逐项记录实际结果。测试/构建/发布失败则保留真实失败，不提前宣称完成。
7. 实现提交存在后，补验证记录与 §11：落地条目、代码落点、实现 hash、偏离/未执行项与理由；
   移入 `implemented/`、重写相对链接并检查目标。再单独提交归档；只有验证结论变化才同步 status。
   归档前若发现判据/契约/时序需要改变，回到提案修订与确认，不靠 §11 事后授权。

步骤 3 中 protocol 与测试准备可在依赖明确后并行；审计期间仅处理审计文件外的工作。
正式清单、部署依赖实现提交；归档依赖真实实现/验证结果，不能与它们倒序或合并。

## 11. 实现结果（归档时补写）

实现提交：`a02d305f29f27fdfe6b63faf356116e137432c1a`（`2026-11-18`）。
验证记录：[act-static-error-taxonomy.md](../../verification/act-static-error-taxonomy.md)。
正式整组构建、部署与实机验证追加于 `2026-11-18`，见验证记录 §8。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 新增 `invalid_target`、`no_safe_point` 与 422 映射；扩展 details 白名单注释 | [internal/protocol/errors.go](../../../internal/protocol/errors.go)：`CodeInvalidTarget`、`CodeNoSafePoint`、`Status` |
| 直接目标静态分类、baseline 检查顺序 | [internal/engine/tap_scope.go](../../../internal/engine/tap_scope.go)：`staticTapRefCheck` |
| `unsupported_node_action` 封闭 reason、`target_actions`/`ancestor_*` | `tap_scope.go`：`newTapStaticUnsupportedError`、`tapPublicActions` |
| `no_safe_point` 候选归集、`occluding_refs`/`ambiguous_refs`/`owner_refs` | `tap_scope.go`：`diagnoseTapStaticFailure`、`classifyTapStaticPoint`、`tapStaticCandidatePoints`、`tapStaticBubbleCandidatePoints` |
| 固定 3×3 建议点与整树归属/source 证明 | `tap_scope.go`：`tapStaticGridPoints`、`tapFiveSixths`、`tapSuggestStaticPoint` |
| bubble 祖先范围分类与诊断 | `tap_scope.go`：`planBubbleTapTarget`、`newTapNoSafePointError` |
| text 聚焦保持基线错误 | `tap_scope.go`：`staticTapRefCheckLegacy`、`planBubbleTapTargetLegacy`；`staticTextRefCheck` 调用前者 |
| `act` 描述与 Instructions 的三码/建议点指引 | [internal/mcp/schema.go](../../../internal/mcp/schema.go) |
| 契约、静态检查、details 与恢复说明 | [docs/interface.md](../../../docs/interface.md)、[docs/architecture.md](../../../docs/architecture.md)、[docs/mcp.md](../../../docs/mcp.md)、README |
| 契约 17 与发行 0.17.0 / Android 19 | [release/versions.json](../../../release/versions.json) 与生成常量 |
| 分类、建议点、列表边界、谓词等价与投影测试 | [internal/engine/tap_static_error_test.go](../../../internal/engine/tap_static_error_test.go)、`act_tap_scope_test.go`、`internal/protocol/errors_test.go`、`internal/mcp/result_parity_test.go`、`result_manifest_test.go` |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| text 聚焦错误 | 未接入新分类。`staticTextRefCheck` 使用基线实现副本，保持提案“不改 text 错误”的范围；新分类只覆盖 `tap`/`long_press`。 |
| 实机分类、禁用/无 bounds、祖先与建议点样本 | 已执行（2026-11-18，设备 `17efb04a`，见验证记录 §8.5/§8.6）。覆盖全遮挡、歧义、混合、十字遮挡、外部 owner 反例、缺动作引导、仅 long_press、无祖先、祖先缺动作/禁用与根 ref；`context` 与 `no_geometry` 无法由受控页面发布，仍仅由合成单测覆盖。 |
| 发布清单、整组构建/核验、部署与手机覆盖安装 | 已执行（2026-11-18，构建 HEAD `5e10d07`、`source_dirty=false`）。`build-release.ps1`、`verify-manifest.ps1`、`deploy.ps1 -SkipBuild` 均通过；App 安装为 0.17.0/versionCode 19，手机产物哈希与清单一致，见验证记录 §8.2/§8.3。 |
| 主机适配器重建/`-VerifyOnly` 与外部 MCP 宿主刷新 | 主机工具已重建并通过 `-VerifyOnly`，新 stdio 与手机 HTTP 探针均 PASS；外部 `codex.exe` 宿主仍运行旧 stdio 镜像，其 `tools/list` 重发现未执行，需用户重启宿主，见验证记录 §6/§8.7。 |
| 实机大树性能与分配矩阵 | 未执行。只记录 1002 节点/100 原候选的合成失败路径基准；不把它写成实机延迟或零分配保证。 |
| 其他工具同名 code | 未改。`scroll` 等既有 `unsupported_node_action` 语义不变；`occluding_ref` 仍可能由基线文本聚焦检查产生，但不再出现在本次 tap/long_press 静态候选耗尽出口。 |

---

## 附：本提案不改变的事项

- 请求字段、`act` 的 operation 分支、`bubble`/`press_enter` 的 presence 规则；
- `enumerateTapCandidates` 的候选顺序、`provesSubtreeTapPoint`/`provesGuidancePoint` 的
  通过条件、`tapFrame` 的冻结内容与 `dispatchAct` 的坐标来源；
- bubble 的祖先选择：C 完全没有三类触摸动作时，仍只提升到最近一个 touch-interactive 祖先；
- 坐标目标、`text`、`swipe`、`scroll`、`key` 的输入、执行与错误；
- O 单向消费、P 幂等、Engine 唯一权威、STOP 不等待、条件化提交、事实分层；
- 12 个工具、请求 schema 与结果信封；本提案所列错误 code/message/details 是明确变化的公开面；
- 日志白名单、账本与私有数据边界；
- Android 业务逻辑；App 只随共同契约与生成版本常量参与整组发布。
