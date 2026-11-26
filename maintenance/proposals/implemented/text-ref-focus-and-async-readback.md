# 提案：解除 text(ref) 对 tap 能力的绑定并增加写后有界异步读回确认

状态：已实现（契约 32）。实现提交：`964c93a4f2b08894db54a1fbac5b192b7a6ca941`。
基线提交：`1259cba7145d8b26536d963d7cf860ed3d3ae85c`（分支 `master`，`2026-11-27`）。
验证记录：[text-ref-focus-and-async-readback](../../verification/text-ref-focus-and-async-readback.md)。
适用范围：`act.operation.type=text` 的 `target.ref` 静态校验、候选干跑与认领后重验，以及 ref 与坐标 `text` 在 `SetTextOnDisplay` 派发后的写后读回确认（含 `auto_submit=false` 与 `auto_submit=true`）；覆盖 GitHub Issue #49 与 #50。
影响范围：`internal/engine`、`internal/mcp/schema.go`、相关单测、`release/versions.json` 及生成版本文件、同步的 `docs/`；不修改固定制品 `third_party/uiautomator/u2.jar`（SHA-256 `28361193DE3D13303543C13BDC7268C22EDECABFA4E68E6FBDFC35FFFE1406A3`）。
契约（`/api/*` 字段与 `contract_version`）：改变。`/api/act` 请求形状与成功响应结构不新增字段，但改变 `text(ref)` 静态拒绝规则（不再要求 `ActionTap`；阻塞非输入子节点归入 `blocking_refs`）、写入完成证据语义（在 `SetTextOnDisplay` 同步读回未匹配时允许有界只读二次读回与 `candidate_count=0` 至多一次严格重绑定读回）以及 MCP 工具描述与工具目录指纹；基线版本 `0.35.0` / `contract_version=31` / Android `versionCode=37`，实施目标升级为 `0.36.0` / `contract_version=32` / Android `versionCode=38`。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[tap_scope.go](../../../internal/engine/tap_scope.go)、[act.go](../../../internal/engine/act.go)、[text_stage.go](../../../internal/engine/text_stage.go)、[text_submit.go](../../../internal/engine/text_submit.go)、[schema.go](../../../internal/mcp/schema.go)、[versions.json](../../../release/versions.json)、[u2 README](../../../third_party/uiautomator/README.md)）。

---

## 0. 摘要

针对 `act(type="text")` 在富文本编辑器场景下的两个高频阻塞（Issue #49 与 #50），本提案在不修改固定 `u2.jar`、不增加请求或成功响应字段的前提下，完成两项正交收口：

1. **解除 `text(ref)` 对 `ActionTap` 的强制绑定（Issue #49）**：`text(ref)` 的静态准入、候选选拔与认领后重验只校验 `RoleInput + ActionText + !Protected + Enabled`，不再要求节点声明 `ActionTap`（`clickable=true`）；保留子树 4 级安全点规划与阻塞诊断（非输入阻塞子节点归入 `blocking_refs`），已聚焦目标走既有零点击快路径，未聚焦目标点按安全点后仍由 `afterTap` + `bindFocusedText` 严格验证获焦。
2. **增加 `stage=verify` 有界只读异步读回确认（Issue #50）**：同步读回精确相等时零额外等待、零额外 RPC；仅当 `SetTextOnDisplay` 已返回完整终止回执（`CandidateCount=1`、`Issued=true`）但 `0ms` 刷新值尚不等于请求文本时，执行最多 3 次短退避只读 `GetTextOnDisplay`（`40ms` / `80ms` / `150ms`，受 `1000ms` 墙钟上限与 `work.Ctx` 立即中断约束）；若期间因输入框重排/撑高导致 `GetTextOnDisplay(oldTarget)` 返回 `CandidateCount=0`，允许至多 1 次 `Capture` + `rebindPostWriteTextTarget` + `GetTextOnDisplay(rebound)` 严格核对。不发送第二次 `SetTextOnDisplay`，不放宽全文精确相等要求，不改变非 `text` 动作与既有的提交门禁。

## 1. 问题

### 1.1 现象与代价

两条调用链在同一实机场景（主屏 `primary` 模式，知乎「写回答」正文富文本编辑器）上稳定复现：

1. **Issue #49（`text(ref)` 被迫充当“点击目标”）**：
   - 公开树正确投影出正文框 `n31 input actions=["text"]`（无 `tap`），但调用 `act(type="text", target={"ref":"n31"}, auto_submit=false)` 时，接口静态返回 `422 unsupported_node_action`、`reason="target_has_other_action"`、`target_actions=["text"]`、中文提示 `"目标节点不支持点击，公开动作：文本输入"`，并把重叠的标题输入框 `n36` 当成建议候选。
   - 即使该输入框（或其聚焦态）已满足文本输入条件，只要不具备 `ActionTap`，调用方就完全无法通过公开 `ref` 写入，只能改猜坐标、消耗额外观察与重试。
   - **分诊结论**：现行实现完全忠实于契约 24 规范（见 [act-target-resolution-and-error-contract.md](act-target-resolution-and-error-contract.md#L189-L190) 与 [architecture.md](../../../docs/architecture.md#L245-L247)），根因落在现行规范把“聚焦点规划”等价成了“要求 `clickable=true`”，属于**取舍/规范类**变更。

2. **Issue #50（同步 `0ms` 读回与编辑器异步落地的时序冲突）**：
   - 改用中心坐标 `(539, 604)` 调用 `act(type="text", target={"x":539,"y":604}, text="诊断测试草稿：一二三四五", auto_submit=false)`，结果返回 `409`、`outcome="uncertain"`、`code="text_result_uncertain"`、`details.stage="verify"`、`details.reason="readback_mismatch"`、`details.candidate_count=1`、`details.dispatch={focus:"dispatched",write:"dispatched",submit:"not_attempted"}`。
   - 紧接着调用 `observe(P)`，新观察中正文框（ref 变为 `n117`）`focused=true`、`value="诊断测试草稿：一二三四五"` 已精确一致。
   - 每次写入都迫使调用方多消费一轮 `observe` 调用，且 `uncertain` 使调用方不敢安全重试，无法在 `text` 调用本身获得 `executed` 事实。
   - **分诊结论**：现行实现忠实于契约 18 规范（见 [text-write-verification-and-recovery.md](text-write-verification-and-recovery.md#L437-L441) 与 [interface.md](../../../docs/interface.md#L800-L806)），问题源于现行规范禁止写入后任何二次只读核对，属于**取舍/规范类**变更，按用户共识与 #49 合并提案推进。

### 1.2 证据链

#### 证据链 A：Issue #49（`text(ref)` 强绑定 `ActionTap`）

| 落点 / 现场证据 | 核实事实 |
| --- | --- |
| [hierarchy.go:213,218](../../../internal/android/hierarchy.go#L213-L218) | `Clickable` 来自 XML `clickable` 属性；`Editable` 按 `strings.HasSuffix(ClassName, "EditText")` 推导 |
| [build.go:354-357,531-546](../../../internal/observation/build.go#L531-L546) | `Editable=true` 投影为 `role=input` 与 `actions=["text"]`；仅当 `Clickable=true` 才附带 `tap` |
| [tap_scope.go:2543-2558](../../../internal/engine/tap_scope.go#L2543-L2558) | `staticTextRefCheck` 在校验 `RoleInput + ActionText + !Protected` 后，硬性检查 `if !hasAction(target, protocol.ActionTap)` 并构造 `ActionTap` 的 `target_has_other_action` 错误 |
| [tap_scope.go:865-880](../../../internal/engine/tap_scope.go#L865-L880)、[tap_scope.go:1800-1814](../../../internal/engine/tap_scope.go#L1800-L1814) | `tryPlanTextDirectBase` 与 `isValidTextPublicTarget` 同样要求 `hasAction(node, ActionText) && hasAction(node, ActionTap)`，使 `actions=["text"]` 输入框既不可执行也不被纳入 `text` 候选 |
| [tap_scope.go:1995-2069](../../../internal/engine/tap_scope.go#L2015-L2067) | `collectDirectBlockingDiagnostics` 的 `occludingNodes`（L2015）与 `ambiguousNodes`（L2064）两处均调用 `validTapPublicTarget(owner, action)` 分流阻塞子树子节点；若 `action` 设为 `ActionText`，具备 `ActionText` 但非 `RoleInput` 或受保护的异常节点必须统一改由同一步骤的文本候选资格判断过滤，非输入阻塞控件只进 `blocking_refs` |
| [act.go:599-608,643-665,1414-1418](../../../internal/engine/act.go#L599-L608) | `revalidateActTarget` 要求 `tap != nil` 并调用 `revalidateTapTargetFrame`；后者在 `revalidateTapTargetFrameWith` 内部调用 `framePublicTargetError` 拿 `plan.action`（当前为 `"tap"`）再次通过 `validTapPublicTarget` 拦截无 `ActionTap` 的目标；公开节点投影在 `revalidateTapTargetFrameWith` 返回后不再保留 |
| [act.go:1782-1800](../../../internal/engine/act.go#L1782-L1800) | `dispatchText` 本身已支持新鲜帧 `focused=true` 经 `bindFocusedText` 零点击直写，但静态检查与重验不看焦点，在之前就拦截了请求 |
| [point.go:288-300](../../../internal/engine/point.go#L288-L300) | 同类核查：坐标 `text` 路径的 `pointOwnerMatchesOperation` 只检查 `RoleInput + ActionText + Enabled + !Protected`，不要求 `ActionTap`，形成 `ref` 拒而坐标通的割裂 |
| [act_proposal_tap_bubble_test.go:491-544](../../../internal/engine/act_proposal_tap_bubble_test.go#L491-L544) | 单测 `input_without_tap_action` 将“缺 `tap` 报 `target_has_other_action`”固化为回归断言；`focus_blocked_returns_candidates` 把阻塞 `Button` 放入 `candidates`（因旧测试误用 `ActionTap` 当作文本规划动作） |
| 实机复现（2026-11-27，主屏） | 知乎正文 `EditText` 原始 XML 为 `class="android.widget.EditText"`、`clickable="false"`、`focusable="true"`、`focused="true"`、`enabled="true"`、`password="false"`、`bounds="[0,441][1079,768]"`；HTTP `/api/act` 对 `n31` 返回 `422 unsupported_node_action/target_has_other_action` |

#### 证据链 B：Issue #50（`0ms` 同步刷新读回与异步提交冲突）

| 落点 / 现场证据 | 核实事实 |
| --- | --- |
| [act.go:1830-1895](../../../internal/engine/act.go#L1830-L1895) | 全仓唯一产生 `stage="verify"` + `reason="readback_mismatch"` 的位置：`SetTextOnDisplay` 返回后直接比较 `if *result.Value != operation.Text`，不等待也不做二次读回 |
| `u2.jar` (`classes8.dex` `DisplayBoundTextService`) | `setTextOnDisplay` 在指令偏移 `0x0047` 调用 `performAction(ACTION_SET_TEXT)` 后，紧跟在 `0x004b` 调用 `readValue(node)`（零等待）；`readValue` 调 `refresh()` → `isShowingHintText()` → `getText()`；`matches` 要求 `package`/`resourceId`/`class`/`contentDescription`/`getVisibleBoundsInScreen.toShortString()` 全相等 |
| [build.go:480-487](../../../internal/observation/build.go#L480-L487)、[read.go:162-165](../../../internal/observation/read.go#L162-L165)、[encode.go:431-433](../../../internal/treetext/encode.go#L431-L433) | 公开树中 `RoleInput.Value` 原样保留字节，不做 trim 或归一化 |
| [text_submit.go:177-219,289-358](../../../internal/engine/text_submit.go#L177-L219) | `enter_key` 提交路径已具备 `rebindPostWriteTextTarget`（当写后 `GetTextOnDisplay` 返回 `CandidateCount=0` 时，至多做一次 `Capture` 严格重绑定 + `GetTextOnDisplay`），但 `verify` 阶段未复用 |
| [text-write-verification-and-recovery.md](text-write-verification-and-recovery.md#L437-L441) | 原契约 18 明确将“控件异步提交导致 `set` 内 `refresh` 返回旧值/`null`”列为未治项，验证记录也标注未实测异步更新控件 |
| 实机直连 `u2.jar` 取证（2026-11-27，主屏知乎编辑器） | 1）写前 `getTextOnDisplay`（`49.9ms`）返回 `candidateCount=1`、旧值 `'诊断测试草稿：一二三四五'`，排除 `isShowingHintText()` 常驻误判；2）随后 `setTextOnDisplay`（`21.5ms`）返回 `candidateCount=1, issued=true, accepted=true`，但 **`0ms` 读回 `value` 仍为写入前的旧值 `'诊断测试草稿：一二三四五'`**；3）紧接着在 `20.6ms` 发起的第一次只读 `getTextOnDisplay`（耗时 `62.9ms`）及后续 `60/120/250/500ms` 轮询均返回 `candidateCount=1` 且文本已切换为新写入内容（注：诊断脚本直传 raw UTF-8 给 `u2.jar` NanoHTTPD 导致该次写入字符在服务端按单字节解码为 36 个 `\ufffd`，与写前 12 字旧值明显区分，时间窗口事实清晰成立） |

### 1.3 为什么直觉会出错

- **针对 #49**：
  1. 直观以为“只删掉 `staticTextRefCheck` 里的 `!hasAction(target, ActionTap)` 就能写入”——**不行**：后续 `planDirectTapRef`、`revalidateActTarget`、`revalidateTapTargetFrameWith`（`framePublicTargetError`）、`tryPlanTextDirectBase` 和 `isValidTextPublicTarget` 全部持有 `ActionTap` 硬校验，必须成体系地切换到 `ActionText`；
  2. 直观以为“无 `tap` 的 `input` 就该打开 `bubble_ancestor` 向上找可点击容器”——**危险**：知乎正文框 `n31` 上方有兄弟标题框 `n36`，开启冒泡/共位吸附可能把焦点点到无关容器或邻近控件上；保留禁止冒泡并只规划当前 `input` 子树内的安全聚焦点才是最小且安全的路径。
- **针对 #50**：
  1. 直观以为“`readback_mismatch` 是因为节点编号从 `n31` 变成了 `n117`”——**不是**：`verify` 阶段根本不读公开树，真正的根因是 `setTextOnDisplay` 内的 `0ms` 同步 `refresh()` 读到了知乎编辑器异步落地前的旧值；
  2. 直观以为“改用普通后观察 `observe` 里看到新字符串就算 `executed`，或者失败时重发一次 `SetTextOnDisplay`”——**违反核心不变式**：后观察不是执行证据，重发 `SetTextOnDisplay` 在部分追加/异步场景下可能造成重复输入；只有只读 `GetTextOnDisplay` 精确匹配才守得住“至多一次写原语 + 精确全文相等”边界。

## 2. 目标与非目标

**目标**：
1. 公开动作仅含 `text`（不含 `tap`）的可编辑、未禁用、非受保护 `input` 节点，支持通过 `act(type="text", target={"ref": ...})` 直接写入（已聚焦走零点击路径，未聚焦先在目标子树安全落点点击一次并严格验证获焦）。
2. `stage=verify` 在 `SetTextOnDisplay` 已确认唯一派发（`CandidateCount=1`、`Issued=true`）但 `0ms` 刷新值尚未精确等于请求文本时，在不重发写原语的前提下，通过有界只读确认覆盖异步提交与写后因内容撑高导致的 `bounds` 变化场景，在确认精确全文一致时直接返回 `write=executed`。
3. 保持所有真实失败分支（格式化/截断导致值不等、多候选、传输超时、无法获焦、STOP 中断）的保守判定与受控 `error.details`，不增加调用方参数与冗余成功响应字段。

**非目标**：
- 不修改固定制品 `third_party/uiautomator/u2.jar`；
- 不发送第二次 `SetTextOnDisplay`，不新增重试写入或回滚；
- 不放宽全文精确相等要求（禁止 trim、前缀匹配、包含匹配或用旁白字数推断成功）；
- 不开启 `text(ref)` 的 `bubble_ancestor`、`bubble_colocated` 或 `coextensive_child`；
- 不对非 `RoleInput`、受保护（`protected=true` 或未知）或禁用的节点放行写入；
- 不更改 `auto_submit`、`submit_ref`（`enter_key`/`n<ref>`）及普通 `tap`/`long_press`/`scroll`/`swipe` 的现有语义；
- 不在成功结果中新增冗余字段（如 `verification`）。

## 3. 路线对比

### 3.1 Issue #49 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持 `text(ref)` 强制要求 `ActionTap` | 无改动 | 所有 `clickable=false` 的富文本/自定义 `EditText` 持续报自相矛盾的 `target_has_other_action` |
| R2 仅放行已聚焦 `text(ref)` | 静态校验仅当 `target.Focused==true` 时豁免 `ActionTap`；未聚焦报 `needs_focus` | 需新增 `needs_focus` 原因，调用方仍需手算坐标或盲猜祖先点一次 | 未聚焦时依旧无法通过单次 `act(text)` 完成输入；且 `bounds` 变化或复杂嵌套时调用方手算坐标不如子树安全点规划可靠 |
| **R3 本提案（选定）** | 解除 `text(ref)` 对 `ActionTap` 的强制绑定，统一用 `ActionText` 做静态校验、候选判定与重验；仍保留子树 4 级安全点规划与 `afterTap` 严格获焦证明 | 需同步调整 `tap_scope.go`、`act.go` 中 `ActionTap` 到 `ActionText` 的分流与已有 `input_without_tap_action` 等测试口径 | 若目标自身 `focusable=false` 且被不可触摸透明层接管，点击一次后 `afterTap` 找不到 `focused=true` 会诚实返回 `focus_unconfirmed`（符合事实） |

选择 **R3**：实机已验证知乎正文 `EditText` 点其几何中心即可获焦，坐标 `text` 路径本来也不要求 `ActionTap`；R3 让 `ref` 与坐标路径语义一致，同时不削弱任何安全性与聚焦证明。

### 3.2 Issue #50 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| S1 什么都不做 | 只采信 `SetTextOnDisplay` 的 `0ms` 返回值 | 无改动 | 所有异步落地超过 `0ms` 的编辑器持续报 `readback_mismatch`，迫使额外 `observe` |
| S2 仅加固定间隔 `GetTextOnDisplay` 轮询 | `verify` 阶段短退避轮询 `GetTextOnDisplay(oldTarget)`，不做写后重绑定 | 改动极小 | 一旦长文写入把输入框撑高导致 `bounds` 变化，严格匹配 `bounds` 的 `GetTextOnDisplay` 立即 `CandidateCount=0`，依然假失败 |
| **S3 本提案（选定）** | 不改 `u2.jar`；`0ms` 不相等/不可读时执行至多 3 次可中断短退避只读 `GetTextOnDisplay`（`40ms/80ms/150ms`，总墙钟上限 `1000ms`）；遇 `CandidateCount=0` 时允许至多 1 次严格 `Capture` + `rebindPostWriteTextTarget` + `GetTextOnDisplay(rebound)` | 异步场景多 1–3 次只读 RPC（约 40–300ms）；写后若遇异步不符的格式化输入，延迟增加至多约 270ms 睡眠 + 3 次只读读回；须同步更新 `internal/engine/text_write_evidence_test.go` 两处显式 `get == 0` 断言（`TestTextReadbackMismatchAndUnavailable` 与 `TestTextAlreadyFocusedStillRequiresExactSetValue`）及其 `Value: nil` 分支的 `getSteps` 夹具 | 若编辑器超过 `1000ms` 才更新无障碍文本、或在 `Capture` 重绑定时失去焦点/同屏存在多个无锚点相同文本的聚焦框，仍会诚实停在 `uncertain` |
| S4 改造固定 `u2.jar` | 在 `DisplayBoundTextService.setTextOnDisplay` 内部增加 `Thread.sleep` 循环与宽松 bounds 匹配 | 须重建、重签名/校验 `u2.jar`，扩大设备端阻塞边界与 STOP 取消盲区 | **触发重评估条件**：若日后出现大量 `isShowingHintText()` 在非空时仍常驻 `true` 的异常控件导致引擎侧 `GetTextOnDisplay` 永远读回 `""`，才考虑立项 S4 |

选择 **S3**：主屏实机已证明 `GetTextOnDisplay` 在数十毫秒内即可精确读回，且 `rebindPostWriteTextTarget` 现成代码已在 `text_submit.go` 中经过严格验证；S3 保持 `u2.jar` 零改动且完全受 `work.Ctx` 即时取消保护。

## 4. 判据

### 4.1 `text(ref)` 静态准入、候选与认领后重验判据

1. **静态目标准入（`staticTextRefCheck`）**：
   - 按顺序校验：公开树视图与原始拓扑合法性（`503 hierarchy_unavailable`）→ 目标节点存在（`409 node_not_found`）→ 非 `root`/非 `context`/有正面积几何（`422 invalid_target`，无几何附 `collectTextFallbackCandidates`）→ 未禁用（`409 node_disabled`）→ `target.Role == RoleInput && hasAction(target, ActionText)`（否则 `422 text_target_uncertain`、`reason="target_not_editable"`）→ `target.Protected != nil && !*target.Protected`（否则 `422 text_target_uncertain`、`reason="target_protected"`）。
   - **不再校验 `hasAction(target, ActionTap)`**。上述全部通过后，调用 `planDirectTapRef(obs, view, operation, target, protocol.ActionText, false, started, tapPlanOptions{})` 在目标子树内规划唯一无遮挡聚焦安全落点（禁用 `coextensive_child`、禁用冒泡与共位吸附）。
   - 注：因 `RoleInput` 节点可能本身已带 `focused=true` 且不需要 `tap`，仍保留 4 级安全点静态规划的原因在于：① 认领前无法预知认领后新鲜帧是否仍保持焦点，必须确保万一需点按聚焦时有可证明的安全落点；② 防止向被子按钮完全覆盖、无法通过点击重新获焦的假安全框发出无效工作。
2. **候选选拔与阻塞诊断**：
   - `isValidTextPublicTarget(node)` 与 `tryPlanTextDirectBase(obs, view, node)` 移除 `hasAction(node, protocol.ActionTap)` 条件，只要求 `RoleInput`、有效几何、未禁用、`Protected != nil && !*Protected`、`hasAction(ActionText)`（且 `tryPlanTextDirectBase` 以 `protocol.ActionText` 调 `tryPlanTapDirectBase`）。
   - `collectDirectBlockingDiagnostics` 的 `occludingNodes` 与 `ambiguousNodes` 两个循环统一抽取并使用同一候选资格判断函数（`ActionText` 用 `isValidTextPublicTarget(owner)`，`ActionTap`/`ActionLongPress` 用 `validTapPublicTarget(owner, action)`）：同边界内的合法文本输入子节点进 `candidates`，阻塞该输入区域的 `Button` 或异常节点只进 `blocking_refs`。
3. **认领后重验与聚焦**：
   - `revalidateTapTargetFrameWith` 内部由 `framePublicTargetError` 按 `protocol.Action(plan.action)` 一次性完成公开节点能力复验：当 `action == protocol.ActionText` 且 `!opts.submit` 时直接调用 `isValidTextPublicTarget(node)`（校验 `RoleInput`、`ActionText`、正面积几何、未禁用、`Protected != nil && !*Protected`），不扩张 `tapFrame` 结构也不在 `revalidateActTarget` 重复投影；`revalidateActTarget` 继续校验 `freshTextTargetSafe(frame.raw)`（可见、可编辑、启用、非密码），并将 `e.actTargetLog(..., actProbeUnique, ...)` 移至该检查通过之后，避免拒绝帧误记 `unique`。
   - `dispatchText` 保持既有聚焦分支：若 `frame.raw` 在新鲜帧已 `Focused` 且通过 `bindFocusedText`，走零点击快路径；否则走 `focusTextRef` 按 `frame.x, frame.y` 至多点一次并经 `afterTap` + `bindFocusedText` 证明唯一获焦。

### 4.2 `stage=verify` 写入完成证据与有界确认判据

一次 `SetTextOnDisplay` 返回后，必须先依次通过以下前置条件才允许进入确认分支：
- `result.Receipt == device.DispatchTerminated` 且 `err == nil`；
- `result.CandidateCount == 1` 且 `result.Issued == true`；
- 在引擎状态锁内成功执行 `noteTextWriteDispatched(work, op)`（写入 `dispatch.write = "dispatched"`、`currentStage = "verify"`）。

上述前置成立后，写入完成证据判定按以下确定性规则执行：

1. **同步快路径**：若 `result.Value != nil && *result.Value == operation.Text`，直接成立写入完成证据，进入 `commitTextWrite`，不等待、不调用 `GetTextOnDisplay`、不调用 `Capture`。
2. **有界异步轮询（仅当 `result.Value == nil || *result.Value != operation.Text`）**：
   - 定义初始失败状态：`lastReason` 取 `readback_mismatch`（当 `result.Value != nil`）或 `readback_unavailable`（当 `result.Value == nil`），`lastCount = 1`（`hasCount = true`）。
   - 固定退避序列 `textVerifyDelays = [40ms, 80ms, 150ms]`（共 3 步），总墙钟预算 `textVerifyMaxElapsed = 1000ms`（从 `noteTextWriteDispatched` 完成后开始计时）。
   - 每步先检查停止/取消与预算：若 `e.workRevoked(work) || work.Ctx.Err() != nil` 立即按撤销收口；若当前已用时间 `>= textVerifyMaxElapsed` 则结束轮询返回最新 `uncertain`。否则调用 `pauseInterruptible(work.Ctx, delay)`；等待被中断则立即按撤销收口。
   - 对原 `target = textTargetOf(bound.raw, session.Display)` 执行 1 次只读 `GetTextOnDisplay`：
     - 若 `readErr != nil`：停止轮询。若因 `workRevoked`/`Ctx.Err()` 导致，返回撤销错误；否则以 `stage=verify`、`reason=readback_unavailable`、**省略 `candidate_count`** 收口为 `uncertain`。
     - 若 `read.CandidateCount == 1`：
       - 若 `read.Value != nil && *read.Value == operation.Text`：**写入证据成立**，跳出循环进入 `commitTextWrite`。
       - 否则更新 `lastCount = 1`、`hasCount = true`，若 `read.Value == nil` 记 `lastReason = readback_unavailable`，若 `*read.Value != operation.Text` 记 `lastReason = readback_mismatch`，进入下一步。
     - 若 `read.CandidateCount > 1`：存在多候选歧义，立即停止轮询，返回 `stage=verify`、`reason=readback_unavailable`、`candidate_count = read.CandidateCount` 收口为 `uncertain`。
     - 若 `read.CandidateCount == 0`：目标可能因内容增高/键盘布局调整发生几何变化，进入**至多 1 次**严格写后重绑定分支（见第 3 条），执行完毕后无论成功与否均退出轮询，不再对旧目标循环。
3. **写后 `CandidateCount == 0` 的单次严格重绑定验证（`textVerifyMaxRebinds = 1`）**：
   - 仅当某次 `GetTextOnDisplay` 无传输错误且 `CandidateCount == 0` 时进入，且整个 `verify` 阶段至多进入一次；**只要未确认成功，本分支直接返回确定的失败原因与 `candidate_count`（不回落到轮询前暂存的旧原因/计数）**：
     1. 先复查 `workRevoked`/`Ctx.Err()`、`obs != nil` 及 `time.Since(started) >= textVerifyMaxElapsed`：已撤销/取消按撤销收口；`obs == nil` 或已超墙钟预算直接返回 `uncertain`（`stage=verify`、`reason=readback_unavailable`、**省略 `candidate_count`**）。否则调用 `e.dev.Capture(work.Ctx, session.Display)`；若 `Capture` 返回错误，除 STOP/取消按撤销收口外，一律返回 `uncertain`（`stage=verify`、`reason=readback_unavailable`、**省略 `candidate_count`**）。
     2. 校验新帧上下文与拓扑：`textSubmitCaptureTrusted(&afterWrite, session.Display)`、`e.textSubmitDisplayCurrent(work, session.Display)`、`validateTapRawTopology(&afterWrite)`、`sameTextTaskContext(obs.Foreground, afterWrite.Foreground)`；任一失败则立即终止，统一返回 `uncertain`（`stage=verify`、`reason=readback_unavailable`、**省略 `candidate_count`**），不重试。
     3. 调用现有严格绑定函数 `rebindPostWriteTextTarget(bound.capture, bound.raw, &afterWrite, operation.Text)`（要求同屏、同 TaskID、同包同类、可见/可编辑/聚焦/启用/非密码、有锚点则写前后均唯一、无锚点则安全且值相等候选全局唯一、几何满足同水平带/同垂直带伸缩/重叠率 ≥0.50、`TextSet && Text == operation.Text`）。若未唯一命中，立即收口为 `uncertain`（`stage=verify`、`reason=readback_unavailable`、`candidate_count = 0`）。
     4. 对唯一重绑定节点 `rebound` 执行 1 次只读 `GetTextOnDisplay(textTargetOf(rebound, session.Display))`：
        - 若 `read2Err == nil && read2.CandidateCount == 1 && read2.Value != nil && *read2.Value == operation.Text`：成立写入完成证据，并将 `bound.raw = rebound`、`bound.capture = &afterWrite` 同步返回给 `dispatchText`，供后续 `auto_submit=true` 的 `enter_key` 提交门禁直接使用更新后的绑定。
        - 否则按以下确定映射直接收口为 `uncertain`（`stage=verify`）：若 `read2Err != nil` → `readback_unavailable` 且**省略 `candidate_count`**；若 `read2.CandidateCount != 1` → `readback_unavailable` 且 `candidate_count = read2.CandidateCount`；若 `read2.Value == nil` → `readback_unavailable` 且 `candidate_count = 1`；若 `*read2.Value != operation.Text` → `readback_mismatch` 且 `candidate_count = 1`。
4. **提交门禁与复合分支衔接**：
   - 所有比较（包括精确相等）仍在 `e.mu` 锁外执行；最终通过 `e.commitTextWrite(work, op)` 在锁内重验原 session/work/P、显示代次与期限。STOP 优先，迟到候选绝不提交。
   - `auto_submit=true` 时，进入 `dispatchTextEnterSubmit` 或 `dispatchTextSubmitTap` 的既有逻辑不变。

## 5. 算法

### 5.1 `text(ref)` 静态与重验主干

```text
staticTextRefCheck(obs, op):
  validate view & raw topology -> 503 hierarchy_unavailable
  target = view.byRef[op.Target.Ref] -> nil => 409 node_not_found
  check root / context / bounds -> 422 invalid_target (+ collectTextFallbackCandidates)
  check enabled -> 409 node_disabled
  check Role==RoleInput && hasAction(ActionText) -> 422 text_target_uncertain(target_not_editable)
  check Protected != nil && !*Protected -> 422 text_target_uncertain(target_protected)
  # 不再要求 ActionTap；直接 4 级安全点规划，bubble=false，allowCoextensiveChild=false
  return planDirectTapRef(obs, view, op, target, ActionText, false, started, {})

revalidateActTarget(...):
  frame = revalidateTapTargetFrame(obs, fresh, tapPlan{action: ActionText})
  # framePublicTargetError 在 revalidateTapTargetFrameWith 内对 ActionText 调用 isValidTextPublicTarget
  # （一次性校验 RoleInput、ActionText、geometry、enabled、Protected != nil && !*Protected）
  require freshTextTargetSafe(frame.raw)
  actTargetLog(operationID, "proven", frame.tier)
```

### 5.2 `verifyTextWriteReadback` 伪代码与时间轴

```text
verifyTextWriteReadback(work, op, session, obs, &bound, text, setResult):
  # 此时已通过 CandidateCount==1 && Issued==true 且提交了 currentStage=verify
  if setResult.Value != nil && *setResult.Value == text:
      log(direct, count=1)
      return true, nil

  lastReason := readback_mismatch (if setResult.Value != nil else readback_unavailable)
  lastCount := 1
  started := time.Now()

  for i, delay := range [40ms, 80ms, 150ms]:
      if workRevokedOrCancelled(work): return false, stoppedVerifyError()
      if time.Since(started) >= 1000ms: break
      if !pauseInterruptible(work.Ctx, delay): return false, stoppedVerifyError()
      if workRevokedOrCancelled(work): return false, stoppedVerifyError()
      if time.Since(started) >= 1000ms: break

      read, readErr := dev.GetTextOnDisplay(work.Ctx, textTargetOf(bound.raw, session.Display))
      if workRevokedOrCancelled(work): return false, stoppedVerifyError()
      if readErr != nil:
          return false, uncertainVerifyError(readback_unavailable, nil)

      if read.CandidateCount == 1:
          if read.Value != nil && *read.Value == text:
              log(poll, attempt=i+1, count=1)
              return true, nil
          lastCount = 1
          lastReason = readback_mismatch (if read.Value != nil else readback_unavailable)
          continue

      if read.CandidateCount > 1:
          c := read.CandidateCount
          return false, uncertainVerifyError(readback_unavailable, &c)

      # read.CandidateCount == 0: 仅允许一次严格写后重绑定验证；失败直接返回确定错误，不回落 lastReason
      rebound, afterCapture, err := verifyViaPostWriteRebind(work, op, session, obs, bound, text, started)
      if err != nil:
          return false, err
      bound.raw = rebound
      bound.capture = afterCapture
      log(rebind, attempt=i+1, count=1)
      return true, nil

  return false, uncertainVerifyError(lastReason, &lastCount)
```

时间轴示意：
- **同步控件（T0）**：`SetTextOnDisplay` (`0ms` 返回精确相等) → `commitTextWrite`（0 次等待，0 次 `GetTextOnDisplay`）。
- **异步控件（T1，如知乎编辑器）**：`SetTextOnDisplay` (`0ms` 返回旧值) → `pause(40ms)` → `GetTextOnDisplay` (约 40–100ms 读回新值相等) → `commitTextWrite`。
- **写后撑高/重排控件（T2）**：`SetTextOnDisplay` (`0ms` 返回旧值/`nil`) → `pause(40ms)` → `GetTextOnDisplay(oldTarget)` 返回 `CandidateCount=0` → `Capture` + `rebindPostWriteTextTarget` → `GetTextOnDisplay(rebound)` 精确相等 → 更新 `bound` → `commitTextWrite`。
- **真实格式化/截断（T3）**：`SetTextOnDisplay` (返回格式化串) → 3 次轮询（`40ms` + `80ms` + `150ms`）仍得格式化串 → 返 `409 outcome=uncertain, stage=verify, reason=readback_mismatch, candidate_count=1`。
- **STOP / 期限到（T4）**：无论落在 `pauseInterruptible`、RPC 前后或锁提交前，只要 `workRevokedOrCancelled(work)` 为真，立即中止轮询并按 `authorization_revoked` / `deadline_exceeded` 冻结或返回，绝不延迟提交成功。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| [tap_scope.go](../../../internal/engine/tap_scope.go) | `staticTextRefCheck` 移除 `!hasAction(target, ActionTap)` 校验，改为传 `protocol.ActionText` 进 `planDirectTapRef`；`isValidTextPublicTarget` 与 `tryPlanTextDirectBase` 移除 `ActionTap` 要求；`collectDirectBlockingDiagnostics` 对 `occludingNodes` 与 `ambiguousNodes` 统一按 `ActionText`/`isValidTextPublicTarget` 分流，非输入阻塞节点归入 `blocking_refs` |
| [act.go](../../../internal/engine/act.go) | `framePublicTargetError` 在 `action == protocol.ActionText` 时调用 `isValidTextPublicTarget` 完成新鲜公开节点复验；`revalidateActTarget` 将 `actTargetLog` 移至 `freshTextTargetSafe` 通过之后；`dispatchText` 在 `noteTextWriteDispatched` 后调用 `verifyTextWriteReadback`，支持写后 `bound` 更新供 `dispatchTextEnterSubmit` 复用 |
| [text_submit.go](../../../internal/engine/text_submit.go) | 将 `verifyViaPostWriteRebind` 抽为清晰辅函数与现有 `rebindPostWriteTextTarget` / `textSubmitCaptureTrusted` / `textSubmitDisplayCurrent` 共享守卫，不重复写第二套重绑定逻辑 |
| [schema.go](../../../internal/mcp/schema.go) | 同步 `callerGuidance` 与 `act` 描述（说明 `text(ref)` 不要求节点带 `tap`，写后读回覆盖短时异步更新与几何变化） |
| [act_proposal_tap_bubble_test.go](../../../internal/engine/act_proposal_tap_bubble_test.go)、[text_write_evidence_test.go](../../../internal/engine/text_write_evidence_test.go)、其他受影响测试 | 更新 `input_without_tap_action`（改为无 `tap` 可直写）、`focus_blocked_returns_candidates`（非输入阻塞进 `blocking_refs`）及 `text_write_evidence_test.go` 两处显式 `get == 0` 断言（`TestTextReadbackMismatchAndUnavailable` 与 `TestTextAlreadyFocusedStillRequiresExactSetValue`，并显式脚本化 `Value: nil` 的 `getSteps` 避免回退到 `fakeDevice` 空串；同文件 L466/514/685 三用例走完 270ms 退避但结论不变）；新增 #49 与 #50 先红回归、边界与 STOP 测试 |
| [versions.json](../../../release/versions.json) 及版本生成脚本输出 | 仅修改 `versions.json` 到 `0.36.0` / `contract_version=32` / `android_version_code=38`，运行 `scripts/generate-version.ps1` 更新 Go/Java/XML 常量 |
| [README.md](../../../README.md) 与 `docs/`（`interface.md`、`architecture.md`、`mcp.md`、`integration.md`、`client.md`、`control-protocol.md`、`runbook.md`、`phone-operations.md`、`release.md`） | 同步 `text(ref)` 无 `tap` 聚焦规划、`stage=verify` 有界只读确认契约与全量版本/契约号引用（`0.36.0` / `32` / `38`），实施提交前通过独立只读文档审计 |

### 6.2 配置与取值

- `textVerifyDelays = []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 150 * time.Millisecond}`：依据主屏知乎实机取证（`20.6ms` 起约 `83ms` 内已落地），首跳 `40ms` 覆盖绝大多数单帧异步提交，总等待仅 `270ms`，最坏仍远低于普通动作交互节奏；
- `textVerifyMaxElapsed = 1000 * time.Millisecond`：覆盖最多 3 次只读 RPC 与 1 次 `Capture` 在低负载或稍慢设备上的墙钟耗时上限；
- `textVerifyMaxRebinds = 1`：单次 `verify` 最多一次严格写后重绑定；
- 以上常量全部内置于 `internal/engine`，**不作为请求参数或服务端配置暴露给调用方**；
- 版本规划：由 `0.35.0` / `contract=31` / `versionCode=37` 升级为 `0.36.0` / `contract=32` / `versionCode=38`。

### 6.3 日志与可观测性

- 在 `verifyTextWriteReadback` 收口处发射一条有界 `Debug` 事件 `engine.text.verify`（复用 [internal/eventlog/log.go](../../../internal/eventlog/log.go#L63-L108) **已有白名单键**，不新增输入内容、长度、hash 或 ref）：
  - 字段：`operation_id`、`mode`（`"direct"` / `"poll"` / `"rebind"`）、`attempt`（`0..3`）、`outcome`（`"executed"` / `"uncertain"` / `"stopped"`）、`reason`（失败时为闭集 `readback_mismatch` / `readback_unavailable` / `authorization_revoked`）、`count`（已知时附）、`ms`（`verify` 阶段耗时毫秒）。
- `engine.text.finished` 保持既有字段与契约不变。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威（`internal/engine`） | 所有静态规划、轮询调度、重绑定判定与最终写提交均由 `engine` 持有；设备/HTTP/MCP 不保存第二份状态 |
| STOP 不等待 | 等待全部走 `pauseInterruptible(work.Ctx, gap)`；`work.Ctx` 取消或会话撤权后立即退出并中止提交，不等待迟到结果 |
| 单向消费与至多一次写 | 静态拒绝不认领 O；已认领进入 `verify` 前 `SetTextOnDisplay` 只调用过 1 次，轮询与重绑定全程只读，绝不发第二次 `SetTextOnDisplay` |
| 提交条件化与状态锁短小 | RPC、睡眠、`Capture`、树比较、字符串比对全部在 `e.mu` 锁外；只有 `noteTextWriteDispatched` 与 `commitTextWrite` 持短锁并重验原 session/work/P、显示代次与期限 |
| 事实分层 | 不采信普通后观察 `observe`；只采信 `SetTextOnDisplay` 或严格同目标/严格唯一重绑定 `GetTextOnDisplay` 的精确相等完整值 |
| 日志白名单与消息边界 | `engine.text.verify` 与错误详情绝不记录输入正文、读回正文、节点树或原始异常 |
| 单一版本源 | 仅修改 `release/versions.json`，其余常量由脚本生成，发布由清单指纹校验 |

### 6.5 契约与文档影响

- `contract_version` 递增至 `32`，同步更新：
  - [docs/interface.md](../../../docs/interface.md)：`text(ref)` 不要求 `ActionTap`；`text` 写入完成证据说明（同步快路径 + 有界只读退避轮询 + `CandidateCount=0` 至多一次严格重绑定）。
  - [docs/architecture.md](../../../docs/architecture.md)：更新 `text(ref)` 焦点规划说明与 `text` 复合输入阶段验证说明。
  - [docs/mcp.md](../../../docs/mcp.md) 与 [docs/integration.md](../../../docs/integration.md)：同步 `text(ref)` 适用范围与写后异步读回行为。
  - [README.md](../../../README.md)、[docs/client.md](../../../docs/client.md)、[docs/control-protocol.md](../../../docs/control-protocol.md)、[docs/runbook.md](../../../docs/runbook.md)、[docs/phone-operations.md](../../../docs/phone-operations.md)、[docs/release.md](../../../docs/release.md)：同步 `0.36.0` / `contract_version=32` / Android `versionCode=38` 版本与示例引用，避免硬编码版本漂移。
- 更新 `internal/mcp/schema.go` 会改变工具目录指纹，需按 `AGENTS.md` 第 13/19 条重新生成版本、构建发布清单并重启宿主 MCP 进程。

## 7. 预期效果与能力边界

### 7.1 能治什么

- Issue #49：公开动作仅含 `text`（无 `tap`）的 `input` 节点（无论已聚焦还是未聚焦）均可通过 `act(type="text", target={"ref": ...})` 正常写入，不再出现自相矛盾的 `target_has_other_action`。
- Issue #50：像知乎富文本编辑器这样在 `performAction(ACTION_SET_TEXT)` 后约数十毫秒内异步落地到无障碍节点、或因内容增加导致输入框撑高（`bounds` 变化）的控件，可在单次 `act(text)` 内获得精确读回并直接返回 `executed`，免去额外 `observe` 调用与不确定性重试风险。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 编辑器异步提交超过 `1000ms` 墙钟上限仍未更新无障碍文本 | 本方案受 `270ms` 总睡眠与 `1000ms` 墙钟上限约束，不引入无边界长等待；超时后仍诚实返回 `uncertain` |
| 应用主动对输入做格式化、截断、过滤或追加隐藏字符 | 判据仍严格要求精确全文相等，不以包含/前缀匹配糊弄通过；真实格式化仍返回 `readback_mismatch` |
| 写后出现无稳定锚点且同包同类、同聚焦、相同文本的多个输入框 | `rebindPostWriteTextTarget` 严格要求全局唯一，多候选时宁可诚实返回 `uncertain` 也不猜 |
| 写入导致控件立即失去焦点、不可见、被禁用或发生跨任务切换 | `textWriteCandidateSafe` 与 `sameTextTaskContext` 强制要求聚焦/可见/启用/同 TaskID，条件不成立时不确认 |
| 异常自定义控件在已写入非空文本后仍误报 `isShowingHintText() == true` | 固定 `u2.jar` 的 `readValue` 在该标记为真时返回 `""`；本提案不改 `u2.jar`，此类控件仍会报 `readback_mismatch` |
| 文本框写后撑高且同时被软键盘上推，导致新旧框重叠率 `< 0.50` 且上下边均超出容差 | `rebindPostWriteTextTarget` 的三路几何守卫不放宽大跨度位移，仍会保守停在 `uncertain` |
| 无 `tap` 的 `input` 节点表面 100% 被其他可点击子控件铺满（无任何安全聚焦落点） | `text(ref)` 仍禁用冒泡与共位吸附，此时静态返回 `422 no_safe_point` 并把遮挡子控件列在 `blocking_refs` |
| `SetTextOnDisplay` 自身传输超时或返回畸形/未知回执 | 尚未证明唯一派发，按既有规则禁止进 `verify` 轮询或重发，直接保留 `uncertain` |

### 7.3 成本

- 同步输入框：0 额外延迟、0 额外 RPC、0 额外 `Capture`。
- 典型异步输入框（如知乎编辑器）：通常在第 1 步 `40ms` 后的一次只读 `GetTextOnDisplay` 即确认（约增加 60–120ms 耗时，少一轮外部 `observe` 往返）。
- 写后重排撑高场景：增加 1 次 `Capture` + 1 次 `GetTextOnDisplay(rebound)`。
- 真实不可恢复的格式化不符：至多增加 `270ms` 睡眠 + 3 次只读 `GetTextOnDisplay`（总上限 `1000ms`）。
- 事件日志：每次 `text` 写入确认最多增加 1 条几十字节的有界 `Debug` 事件。

## 8. 验证计划

### 8.1 单元测试

全部测试由主代理亲自执行 `GOEXPERIMENT=jsonv2 go test ./cmd/... ./internal/... ./scripts/... -count=1 -timeout 900s`。

| 用例 | 期望 |
| --- | --- |
| **先红对照 #49-A**：`actions=["text"]`（无 `tap`）的未聚焦 `input` ref 写入（`auto_submit=false` 与 `true`） | 基线报 `422 target_has_other_action`；实现后走 1 次安全点 focus tap → `afterTap` 绑定 → `SetTextOnDisplay` → `executed` |
| **先红对照 #49-B**：`actions=["text"]`（无 `tap`）的已聚焦 `input` ref 写入 | 基线报 `422 target_has_other_action`；实现后跳过 focus tap（`tap=0, set=1`）→ `executed` |
| **#49 边界-阻塞隔离**：`actions=["text"]` 输入框中心被可点击 `Button` 遮挡，边缘有空隙 vs 100% 被遮挡 | 有空隙时绕开按钮安全聚焦并写入；100% 被遮挡时静态报 `422 no_safe_point`，`Button` 在 `blocking_refs` 而不在 `candidates`；且始终不冒泡/共位漂移到邻近输入框 |
| **#49 边界-重验防护**：认领时 `actions=["text"]` 目标在新鲜帧失去 `Editable` 或变成 `Protected` 或无 `ActionText` | 认领后零派发返回 `409 target_changed`（`reason=target_unconfirmed`）并交付新帧 |
| **先红对照 #50-A**：`SetTextOnDisplay` `0ms` 返回旧值或 `nil`（`CandidateCount=1, Issued=true`），随后第 1 或第 2 次 `GetTextOnDisplay` 返回精确全文 | 基线报 `409 readback_mismatch/readback_unavailable`；实现后在轮询内返回 `executed`（`set=1`，不发第二次写）；`auto_submit=true` 正常接续提交 |
| **先红对照 #50-B**：`SetTextOnDisplay` `0ms` 返回旧值，随后 `GetTextOnDisplay` 返回 `CandidateCount=0`（模拟撑高后 `bounds` 变化），`Capture` + `rebindPostWriteTextTarget` 命中唯一新框且 `GetTextOnDisplay(rebound)` 精确相等 | 基线报 `409 readback_mismatch`；实现后单次重绑定读回确认返回 `executed`；若 `auto_submit=true` + `enter_key`，使用更新后的 `bound` 正常触发 ENTER |
| **#50 反向守卫-真实格式化不符**：`SetTextOnDisplay` 与全部 3 次 `GetTextOnDisplay` 均返回格式化串 `"a b"`（请求 `"a\nb"`） | 仍返回 `409 outcome=uncertain`、`stage=verify`、`reason=readback_mismatch`、`candidate_count=1`、`set=1`、`key=0` |
| **#50 反向守卫-多候选 / 无效重绑定**：轮询期间 `GetTextOnDisplay` 返回 `CandidateCount=2`；或 `CandidateCount=0` 后存在两个无锚点相同文本聚焦框 | 立即停在 `409 outcome=uncertain`、`stage=verify`、`reason=readback_unavailable` |
| **#50 STOP 中断**：`SetTextOnDisplay` 返回旧值进入 `pauseInterruptible` 后立即触发 STOP / `Ctx` 取消 | 立刻中断等待，不继续后续 `GetTextOnDisplay` 与 `commitTextWrite`，按 `control_revoked`/`authorization_revoked` 收口 |
| **回归与工具目录**：同步快路径仍 `tap=0, set=1, get=0`；普通 `tap`/`long_press`/`scroll` 与既有 `text` 恢复测试、MCP 工具目录与探针同步测试全部通过 | 全套 `./cmd/... ./internal/... ./scripts/...` 全绿 |

### 8.2 实机/系统级

按 `AGENTS.md` 第 19 条与用户本次要求，实施完成后由主代理亲自执行完整发布交付与实机验证：

1. **构建与手机整组覆盖部署**：执行版本生成与发布构建，运行 `scripts/verify-manifest.ps1` 核验清单（含固定 `u2.jar` 哈希一致、`contract_version=32`、工具目录指纹），按既有部署流程覆盖部署手机端全组产物（`device-service`、手机 `device-mcp`、控制 App、`u2.jar` 等，**保留 App 私有数据**）。
2. **最小实机闭环（`virtual` 虚拟屏模式）**：
   - 在已连接设备的**虚拟屏**上先用探针 App 覆盖既有同步输入、非 BMP 与格式化负例回归；
   - 随后在**虚拟屏**会话内自行打开知乎（`com.zhihu.android`）并导航进入「写回答」编辑器页面，实机执行并记录：① 对无 `tap` 的 `input` ref（未聚焦与已聚焦）直接 `act(type="text", auto_submit=false)` 成功写入并返回 `executed`；② 写入导致异步落地（及长文/多行撑高）场景返回 `executed` 且无需额外 `observe`；③ `auto_submit=false` 零 ENTER、零误触提交按钮。
3. **主机工具替换与双入口独立探针**：在同一源码提交上构建并替换 Windows 主机的 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验主机伴随清单后，分别用新 `mcp-probe.exe` 独立验证**主机 stdio** 与**手机 HTTP MCP** 入口（验证 `contract_version=32`、新 `instructions`/工具目录指纹一致与基本闭环；依赖 Agent 的托管进程与重连由用户处理）。
4. **远端推送与 Issue 收尾**：上述全部验证通过、归档并复验清单后，推送远端仓库，并在 GitHub Issue #49 与 #50 回复完整根因、修复提交、实机验证边界并关闭两个 Issue。

### 8.3 记录

实施与验证结果写入 `maintenance/verification/text-ref-focus-and-async-readback.md`，严格区分已实现 / 已单测 / 已实测 / 磁盘替换 / 独立探针结果与未执行项原因。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 拆合策略 | #49 与 #50 合并为一个提案推进并同步发版 |
| #49 目标与聚焦策略 | 解除 `text(ref)` 对 `ActionTap` 的强制绑定，统一用 `ActionText`；保留子树 4 级安全点规划与 `afterTap` 严格获焦证明；禁用冒泡与共位吸附 |
| #50 读回确认策略 | 不改固定 `u2.jar`；在 `engine` 的 `stage=verify` 增加至多 3 次可中断短退避只读 `GetTextOnDisplay`（`40ms/80ms/150ms`，总墙钟上限 `1000ms`），以及 `CandidateCount=0` 时至多 1 次严格 `Capture` + `rebindPostWriteTextTarget` + `GetTextOnDisplay(rebound)` |
| 响应与参数增量 | 不新增请求字段，不在成功结果中新增 `verification` 等冗余字段，失败沿用现有 `error.details` 枚举 |
| 共同版本目标 | `0.36.0` / `contract_version=32` / Android `versionCode=38` |
| 最小实机闭环环境 | 在**虚拟屏（`virtual` 模式）**上执行：先测探针 App，再于虚拟屏会话内自行打开知乎并导航进入「写回答」页验证 #49 与 #50 |
| 收尾范围 | 包含推送远端、回复并关闭 GitHub Issue #49 与 #50 |

### 9.2 未决

无悬空未决项；本草案经独立只读审阅收敛并获用户确认后即按第 10 节顺序实施。

## 10. 实施顺序

1. 编写 #49 与 #50 先红回归单测（`internal/engine`）并运行确认在基线上失败；
2. 实施 `internal/engine/tap_scope.go`、`act.go`、`text_submit.go` 的代码改动（步骤 1 后顺序执行）；
3. 更新受影响的既有旧契约单测与 `internal/mcp/schema.go` caller 指南，运行定向测试；
4. 编辑 `release/versions.json` 为 `0.36.0` / `32` / `38` 并运行 `scripts/generate-version.ps1`；
5. 同步更新 [docs/interface.md](../../../docs/interface.md)、[docs/architecture.md](../../../docs/architecture.md)、[docs/mcp.md](../../../docs/mcp.md)、[docs/integration.md](../../../docs/integration.md)、[README.md](../../../README.md)、[docs/client.md](../../../docs/client.md)、[docs/control-protocol.md](../../../docs/control-protocol.md)、[docs/runbook.md](../../../docs/runbook.md)、[docs/phone-operations.md](../../../docs/phone-operations.md) 与 [docs/release.md](../../../docs/release.md)；
6. 主代理亲自执行全量测试 `GOEXPERIMENT=jsonv2 go test ./cmd/... ./internal/... ./scripts/... -count=1 -timeout 900s` 并确认全部通过；
7. 发起独立、只读的文档审计 subagent 审核本次涉及的 `docs/`（含上述全部升级文档），修正发现项直至取得 `CLEAN` 结论；
8. 提交 1：将处于 `状态：提案，未实现。` 的本提案文件与实现、测试、版本生成、`README.md` 及同步后的 `docs/` 一并纳入 git 提交（保证工作树完全干净无 untracked 文件，使步骤 9 发布构建的 `source_dirty=false` 且清单校验通过）；
9. 主代理亲自执行整组发布构建、清单核验、手机覆盖部署（保留私有数据）、**虚拟屏**实机闭环（探针 App + 虚拟屏内自行打开知乎并进入「写回答」页验证 #49/#50）、主机工具替换与双入口 `mcp-probe.exe` 独立验证；
10. 归档提案到 `maintenance/proposals/implemented/text-ref-focus-and-async-readback.md`（重写相对链接、更新头部实现 hash、补全 §11），编写 `maintenance/verification/text-ref-focus-and-async-readback.md`，按需更新 `maintenance/status.md`，提交 2 归档提交；
11. 在归档提交 HEAD 按仓库惯例复建核验发布包并确认一致；
12. 推送远端分支并在 GitHub 回复及关闭 Issue #49 与 #50。

## 11. 实现结果

实现提交：`964c93a4f2b08894db54a1fbac5b192b7a6ca941`。实现与验证记录：
[text-ref-focus-and-async-readback](../../verification/text-ref-focus-and-async-readback.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| §4.1 静态准入解除 `ActionTap` | `internal/engine/tap_scope.go:2506` 删除 `hasAction(target, ActionTap)` 拒绝，改以 `role=input`+`text`+几何/启用/保护判定，并以 `protocol.ActionText` 进 `planDirectTapRef`（`bubble` 关闭、无 `coextensive_child`）。 |
| §4.1 候选资格与阻塞诊断 | `isValidTextPublicTarget`（`tap_scope.go:1800`）只按 `text` 能力；`tryPlanTextDirectBase`（`:865`）同样；新增 `directBlockingCandidate`（`:1986`）供 `collectDirectBlockingDiagnostics` 两处 owner 分类共用，`tap`/`long_press` 分支保持原表达式。 |
| §4.1 认领后重验 | `internal/engine/act.go:599` 的 `framePublicTargetError` 对非 submit 的 `text` 帧调用 `isValidTextPublicTarget`；`revalidateActTarget`（`:626`）把 `unique` 日志移到 `freshTextTargetSafe` 通过之后。 |
| §4.2 `stage=verify` 有界确认 | `internal/engine/text_submit.go:12` 定义 `textVerifyDelays=[40,80,150]ms`、`textVerifyMaxElapsed=1000ms`、`textVerifyMaxRebinds=1`；`:19` `verifyTextWriteReadback` 实现快路径 + 轮询 + 多候选失败 + 单次重绑定分派；`:79` `verifyViaPostWriteRebind` 复用 `rebindPostWriteTextTarget`/`textSubmitCaptureTrusted`/`textSubmitDisplayCurrent`/`validateTapRawTopology`/`sameTextTaskContext`，成功回写 `bound.raw`/`bound.capture`。退避与 RPC 共用从 `work.Ctx` 派生的绝对截止子上下文，`1000ms` 覆盖 RPC 本身；撤销仍以 `CodeStopped`+`authorization_revoked` 收口。 |
| §4.2 写入完成证据接线 | `internal/engine/act.go:1887` 在 `noteTextWriteDispatched` 之后调用 `verifyTextWriteReadback(&bound, …)`，成功才 `commitTextWrite`；`auto_submit` 两条提交路径与 `enter_key` 复用更新后的绑定。`setTextOnDisplay` 仍是唯一调用点。 |
| §6.3 有界事件 | `internal/engine/text_submit.go:153` 发射 `Debug` 事件 `engine.text.verify`，字段 `operation_id`/`mode`/`attempt`/`outcome`/`reason`/`count`/`ms` 全部为 `internal/eventlog` 既有白名单键。 |
| §6.1 MCP 描述 | `internal/mcp/schema.go` caller 指南与 `act` 描述补充「`text(ref)` 不要求节点带 `tap`」与「写后读回覆盖短时异步落地与几何变化」两条事实；工具目录指纹随之为 `13/5578041a…`。 |
| §6.1 版本与文档 | `release/versions.json` 与 4 个生成文件升为 `0.36.0/32/38`；`README.md` 与 9 份 `docs/` 同步两条契约与全部版本引用。 |
| §8.1 先红用例 | 新增 `internal/engine/text_ref_focus_test.go`（7 例）与 `internal/engine/text_verify_readback_test.go`（10 例）；改写 `act_proposal_tap_bubble_test.go` 两个旧子用例、`text_write_evidence_test.go` 六处旧断言。先红证据、实现后绿测与全量单次结果见验证记录。 |
| §8.2 发布交付 | 干净实现提交上的整组发布构建与清单核验 PASS；手机整组覆盖部署（保留 App 私有数据）；虚拟屏实机闭环（探针 App 同步写/非 BMP/默认提交、知乎无 `tap` 正文写入、异步首跳确认、长文撑高单次重采集）；主机 `device-mcp.exe`/`mcp-probe.exe` 替换与 stdio/http-remote 双入口独立探针均 PASS。 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| `verifyTextWriteReadback` 的落点 | 提案 §6.1 把主循环写在 `act.go`、辅助函数写在 `text_submit.go`；实现把两者一起放在 `text_submit.go`（`act.go` 只保留调用）。理由：与既有 `dispatchTextEnterSubmit` 的写后重绑定共用同一组守卫与错误构造，集中在一处避免两套流程漂移，`act.go` 只保留一个接线点。 |
| 发现字节预算不扩 | 提案未提该限制。首轮指南使 `internal/mcp` 的 discovery 预算超限（`41161 > 40000`）；未按上一个提案的先例抬高上限，而是把对外指南收敛为两条调用方事实（不要求 `tap`、写后读回覆盖短时异步落地与几何变化），详细契约留在 `docs/`。 |
| `engine.text.verify` 的 `outcome` 取值 | 日志里的 `stopped` 是事件专用枚举（区分 `executed`/`uncertain`），不是协议 `outcome`；未新增协议取值。 |
| 未实测项 | 100% 遮挡的 `no_safe_point`/`blocking_refs`、部分遮挡绕开、真实格式化负例、STOP 打断退避的实机时序未构造，均由单测覆盖；未跑 `-race`（Windows 无 cgo）。 |
| 依赖方进程 | 依赖 Agent 托管的 MCP 进程与重连未操作（`start` 仍返回 `409 contract_mismatch`），实机验证改用 HTTP 直调与独立探针；宿主重启由用户处理。 |


---

## 附：本提案不改变的事项

- 固定制品 `third_party/uiautomator/u2.jar` 的代码与 SHA-256 哈希；
- `act(type="text")` 的请求参数形状与 `auto_submit`/`submit_ref` 语法及提交分支行为；
- 坐标 `text` 的原点证明与 `focusTextPoint` 流程（仅共享其后 `stage=verify` 写后读回确认）；
- `SetTextOnDisplay` 单次调用上限（至多 1 次，永不重发写原语）；
- 写入精确全文相等判据与受保护输入框拦截规则；
- 非 `text` 动作（`tap`/`long_press`/`scroll`/`swipe`/`key`）与其它工具契约；
- 已有 `error.code`、`error.details` 键名与闭集枚举、`text_input` 合法状态组合及 `recovery` 投影规则。
