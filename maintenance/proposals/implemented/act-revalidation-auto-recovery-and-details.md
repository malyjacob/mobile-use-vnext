# 提案：act 重验失败详情统一与零派发高置信自动恢复

- 日期：2026-11-28
- 状态：已实现（契约 36，0.40.0；实现提交 `c12d28876cf663d9d527e99eaba827bb3a426815`，2026-11-28）。
- 验证记录：[2026-11-28-act-revalidation-auto-recovery-and-details.md](../../verification/2026-11-28-act-revalidation-auto-recovery-and-details.md)
- 基线提交：`8dc872e3bbfe3fed1c54b23e4946421bdfe7f417`（基于本仓库当前 `HEAD` 源码与 [`docs/`](../../../docs/) 现行契约核对）
- 适用范围：`act` 工具（`tap` / `long_press` / `text` / `scroll`）在认领观察 `O` 后、设备动作派发前的零派发重验阶段（`phase=revalidate`）；关联 GitHub Issue #43（本提案实施、发布并推送后回复闭环关闭），明确排除 GitHub Issue #52（静态拒绝 `422 unsupported_node_action` / `reason=axis_mismatch`，纳入后续优化议程）。
- 影响范围：`release/versions.json` 及版本生成产物、`internal/protocol`、`internal/engine`、`internal/eventlog`、`internal/client`、`internal/mcp`、`scripts/mcp-probe`、`docs/{interface,mcp,runbook}.md`、`maintenance/verification/` 及发布交付物。
- 契约（`/api/*` 字段与 `contract_version`）：**变化**。统一 `phase=revalidate` `409 target_changed` `error.details` 字段并破坏性移除 `text` 重验专属的 `target_unconfirmed` 覆写；新增顶层 `Result.auto_recovery`（及 `result v4` 对应文本投影）；[`release/versions.json`](../../../release/versions.json) 已由 `0.39.0 / contract_version: 35 / android_version_code: 41` 升为 `0.40.0 / contract_version: 36 / android_version_code: 42`。

---

## §0 一页看懂（摘要）

1. **要解决的问题**：在 `act` 认领 `O` 后的零派发重验阶段（`phase=revalidate`），目前存在两类摩擦：
   - **可恢复目标被过早拒绝**（Issue #43 实机 Case B/C）：当 `text` 输入框在瞬态重试后原 `ref` 仍失配但同一可信父 `resource-id` 锚点（或唯一静态非数字锚点）下仅存唯一合法输入框时，或当 `tap`/`long_press` 的直接可点击卡片容器在短稳等待后原 `ref` 失配但唯一非模板可读锚点与安全区仍证明为同一控件时，现行重验仅尝试一次原目标 `150ms` 瞬态复查后即返回 `409 target_changed`。
   - **`409 target_changed` `error.details` 跨 `kind` 不统一且缺少新 `O` 恢复上下文**：[`decorateTextRevalidateError`](../../../internal/engine/act.go#L2142-L2160) 把 `text` 的真实重验原因覆写为 `stage=target`、`reason=target_unconfirmed`、`dispatch=...`，抹掉 `changed_ref` 与根因；同时所有 `kind` 的 `409 target_changed` 均未在交付新 `O` 后向调用方提供基于新 `O` 的候选 `candidates` / 阻塞节点 `blocking_refs`；内部即便经 `150ms` 重试恢复，调用方在成功响应中也没有任何显式标记。
2. **选定路线（R2）**：
   - **统一 `409 target_changed` `error.details`（不保留 `text` `target_unconfirmed` 向后兼容）**：移除 `text` 在 `phase=revalidate` 对 `CodeTargetChanged` 的 `decorateTextRevalidateError` 覆写；所有 `kind` 共用同一套 `phase="revalidate"` 封闭 `details`（沿用 [`targetChangedReason*`](../../../internal/engine/act_target_shared.go#L62-L72) 9 个既有原因常数 + `kind` + 可选 `original_ref`/`changed_ref`/`recovery_attempted`，以及**仅在首响应成功交付新 `O` 时基于 `publishedObs.Projection` 计算、且绝不写入 `op.Error`** 的 `candidates`/`blocking_refs`）。已进入设备发送的 `dispatchText` 阶段错误（`focus`/`write`/`verify`/`submit` 及 `409 text_submit_not_executed`）完全不变。
   - **零派发高置信自动恢复（`transient_retry` + `text_rebind` + `container_rebind`）**：保留现行 [`isTransientRevalidateError`](../../../internal/engine/act.go#L20-L34) 与 `150ms` 瞬态复查优先机会；若 Attempt 2 仍返回可恢复的目标失配，在**零额外抓屏、零额外等待、`dispatch` 之前、同一已认领 `O` 内**，执行最多 1 次纯内存高置信重锚；仍失败则退回统一 `409 target_changed`。
   - **显式顶层字段 `Result.auto_recovery`**：无论经 `transient_retry`、`text_rebind` 还是 `container_rebind` 进入真实派发，均在 `Result` 顶层与 `MCP result v4` 返回 `auto_recovery`（`strategy`、可选 `original_ref`/`recovered_ref`、`attempts`），无需查询诊断或日志即可知悉。
3. **非目标与后续议程**：拒绝 R3（跨容器/坐标空白点兜底盲点）；`scroll` 重验自动切换容器、`app` 动作及 `text` 派发后阶段不纳入本次恢复；**静态准入拒绝的自动恢复（含 GitHub Issue #52：`422 unsupported_node_action` / `reason=axis_mismatch`）明确排除出本提案实现范围，正式纳入后续“静态拒绝自动恢复”独立优化议程**。

---

## §1 问题、现场证据与直觉陷阱

### §1.1 现场问题与分诊边界

GitHub Issue #43（含 2026-11-27 基于 `0.38.0 / contract 34` 实机反馈的 Case A/B/C）反映的摩擦经代码核查分诊如下：

1. **Case B（`text` 重验瞬态/容器换号失配 → 本提案范围）**：`act(kind=text, target.ref=...)` 认领 `O` 后，因弹层、软键盘挤压或局部子树重排导致目标节点在 Attempt 2 仍出现 `target_missing` / `lineage_changed`。若按父 `resource-id` 锚点或唯一静态非数字锚点可唯一定位同一可写节点，应能在零派发阶段直接完成定向重锚并输入。
2. **Case C（`tap`/`long_press` 容器封装重排 → 本提案范围）**：卡片容器局部包裹变化或父链结构调整导致原节点在 Attempt 2 仍失配（`target_missing` / `lineage_changed`），但新树上携带同一全屏唯一非数字文案且骨架守恒的直接可点击容器仍是同一个操作目标，应可在重新完成直接规划与 Tier-4 遮挡证明后直接点击。
3. **跨 `kind` 重验 `error.details` 碎片化与静默恢复不可见（本提案核心改造）**：
   - 现行 `text` 重验失败会丢失具体原因与 `changed_ref`；非 `text` 失败又未利用刚刚交付给调用方的新观察 `O` 直接给出可替代的 `candidates` / 遮挡节点 `blocking_refs`；
   - 现行 `150ms` 瞬态重试成功时，返回体毫无迹象告知调用方这次动作经过了一次延迟复验。
4. **Case A / GitHub Issue #52（静态准入拒绝 → 分诊排除并延后）**：Issue #52 反馈外层横向滚动容器 `n31` 与内部同界纵向列表 `n37` 命中 [`resolveScrollRefPlan`](../../../internal/engine/scroll.go#L1026-L1029) 与 [`tryUniqueInnerScrollFallback`](../../../internal/engine/scroll.go#L1388-L1453) 第 `1406` 行 `!isCoextensiveRect(wantRect, descRect)` 守卫，返回 `422 unsupported_node_action`（`reason=axis_mismatch`）。该问题发生在认领 `O` 之前的静态准入检查（`phase=admission`），与 `phase=revalidate` 语义边界截然不同，已按用户决策排除于本提案，纳入后续独立议程。

### §1.2 现有代码定位

| 关注点 | 现有位置 | 现行行为与核查事实 |
|---|---|---|
| 重验循环与瞬态白名单 | [`actRevalidateMaxAttempts`](../../../internal/engine/act.go#L14-L15)、[`isTransientRevalidateError`](../../../internal/engine/act.go#L20-L34)、[`Act`](../../../internal/engine/act.go#L127-L177) | `actRevalidateMaxAttempts = 2`、`actRevalidateRetryDelay = 150ms`。现行仅对 `target_missing`、`lineage_changed`、`child_changed`、`scope_blocked`、`scroll_scope_changed`、`trajectory_blocked`、`point_changed` 做一次 `150ms` 后 Attempt 2 同目标复查（[`act_revalidate_transient_test.go`](../../../internal/engine/act_revalidate_transient_test.go#L33-L35) 已锁死该行为）；`target_ambiguous` 与 `capability_changed`（见 [`act_tap_scope_test.go`](../../../internal/engine/act_tap_scope_test.go#L635)）以及 `409 foreground_changed` 均不重试，直接失败。 |
| 封闭重验原因枚举 | [`targetChangedReason*`](../../../internal/engine/act_target_shared.go#L62-L72)、[`revalidateScrollTarget`](../../../internal/engine/scroll.go#L1748-L1904) | 全非 `text` 重验已统一使用 9 个原因：`child_changed`、`target_missing`、`target_ambiguous`、`lineage_changed`、`capability_changed`、`scroll_scope_changed`、`trajectory_blocked`、`scope_blocked`、`point_changed`（`revalidateScrollTarget` 已完全接入其中）。注：`foreground_changed` 是 [`APIError.Code`](../../../internal/protocol/errors.go#L190) 而不是 `details.reason`；`blocked` 仅用于静态 `422 no_safe_point`（[`tap_scope.go`](../../../internal/engine/tap_scope.go#L30)）。 |
| `text` 错误详情覆写 | [`Act` 装饰点](../../../internal/engine/act.go#L158)、[`decorateTextRevalidateError`](../../../internal/engine/act.go#L2142-L2160)、[`textReasonTargetUnconfirmed`](../../../internal/engine/text_stage.go#L46) | `revalidateActTarget` 算出精确 `409 target_changed` 原因后，`decorateTextRevalidateError` 把 `Details` 强行覆写为 `stage="target"`、`reason="target_unconfirmed"`、`candidate_count=0`、`dispatch={focus:not_attempted,write:not_attempted,submit:not_attempted}`。 |
| 拒绝后新观察发布与存储隔离 | [`deliverRejectedObservation`](../../../internal/engine/delivery.go#L25-L74)、[`publishTreeAttempt`](../../../internal/engine/capture.go#L573)、[`finishWithError`](../../../internal/engine/operations.go#L540-L558)、[`operationViewLocked`](../../../internal/engine/engine.go#L1295-L1360)、[`statusOperationLocked`](../../../internal/engine/status.go#L94-L154)、[`applyFrozenResult`](../../../internal/engine/retirement.go#L626-L646) | `publishTreeAttempt` 已返回 `(public, publishedObs, publishErr)`，但 `deliverRejectedObservation` 当前只返回 `public` 并丢弃 `publishedObs`。`finishWithError` 将 `apiErr.Public()` 写入持久化 `op.Error`。 |
| 文本、点击与滚动规划/匹配原语 | [`text_focus_binding.go`](../../../internal/engine/text_focus_binding.go#L16-L160)、[`act_revalidation_policy.go`](../../../internal/engine/act_revalidation_policy.go#L1153-L1424)、[`tap_scope.go`](../../../internal/engine/tap_scope.go#L552-L838,L1823-L2085)、[`scroll.go`](../../../internal/engine/scroll.go#L796,L1484-L1513)、[`device/types.go`](../../../internal/device/types.go#L79-L115) | - `device.RawNode` 不含 `Role`，父链查找使用 `uniqueTextParentAnchor`（`maxParentAnchorDepth = 8`、`DisplayID`、`Package`、`ClassName`、`ResourceID`、`!isRawWindowRoot && !textParentAnchorBoundary`）；`freshTextTargetSafe(raw)` 接收 `*device.RawNode`；`staticTextRefCheck` 是整树规划器而非单候选校验器。<br>- `sameReadableMultiset` / `subtreeSkeletonMatch` 是 **100% 全等匹配**，`readableSubtree` 只收集后代而不含节点自身 `Text`/`Description`。<br>- `collectTapFallbackCandidates`、`collectTextFallbackCandidates` 与 `collectScrollAncestorAndPageCandidates` 均要求非空且归属当前 `view` 的 `target *protocol.Node`（依赖 `view.info[target]` 指针）。 |
| 协议、日志、MCP 与探针 | [`Result`](../../../internal/protocol/responses.go#L815-L1089)、[`decode_response.go`](../../../internal/protocol/decode_response.go#L512-L578)、[`errors.go`](../../../internal/protocol/errors.go#L10-L29)、[`eventlog/log.go`](../../../internal/eventlog/log.go#L63-L108)、[`result_text.go`](../../../internal/mcp/result_text.go#L22-L129,L323-L529,L2065-L2117)、[`result_manifest_test.go`](../../../internal/mcp/result_manifest_test.go#L99-L122)、[`scripts/mcp-probe/`](../../../scripts/mcp-probe/) | `Result` 无 `auto_recovery`；`eventlog` 使用单数 `"attempt"` 且严禁任何 `ref` 入日志，当前没有 `"strategy"`；`v4ResultFields` 使用 `v4Token`/`v4Object`/`v4Literal` 且由清单 + `rmfResultFixtures()` 与探针 `result_semantics.go` 强制门禁。 |

### §1.3 为什么直觉会出错

1. **直觉陷阱 1：“Attempt 1 遇到 `target_missing` / `lineage_changed` 就该跳过 `150ms` 立即重锚”**。事实上现行 `isTransientRevalidateError`（[`act.go#L20-L34`](../../../internal/engine/act.go#L20-L34)）将这两类设为可等待瞬态错误并在 [`act_revalidate_transient_test.go`](../../../internal/engine/act_revalidate_transient_test.go#L33-L35) 验证“动画抖动 100ms 后原 `ref` 在 Attempt 2 复现即可直接恢复”。若 Attempt 1 跳过等待直接换绑新节点，反而会把短暂瞬态抖动变成没必要的 `ref` 更换甚至误命中别处。因此先保住 `150ms` 同目标 `transient_retry` 机会，Attempt 2 依然失配时再基于到手最新的 `fresh2` 零额外等待执行 1 次纯内存 `rebind`，才是最小代价兼顾两者。
2. **直觉陷阱 2：“在失败时直接复用 `collectTapFallbackCandidates` / `collectTextFallbackCandidates` 就能给新 `O` 填 `candidates`”**。这两个函数依赖归属同一 `tapPublicView` 的非空 `target *protocol.Node` 指针（查询 `view.info[target]`）并硬性剔除矩形不相交节点；在 `target_missing`、坐标动作（无 `target.ref`）、或键盘弹出导致坐标系/视图矩形全移的场景下，新 `O` 中根本没有对应公共节点或交集为空，直调必返 `nil`（把旧 `target` 传进去则因指针不匹配新 `view.info` 直接失效）。必须区分“新帧存在唯一 raw 命中节点”与“无节点 / 坐标 / 失配”两条路径。
3. **直觉陷阱 3：“直接把 `candidates` / `blocking_refs` 放进 `revalidateErr.Details`”**。`finishWithError` 会把 `apiErr.Public()` 写入 `op.Error` 持久化，供后续不带 `observation` 的 `status(P)` 或同 `P` 幂等重放读取，直接写入会导致重放响应携带失效的新帧局部 `ref`。

---

## §2 范围与非目标

### §2.1 本次包含（In Scope）

1. **统一 `act` `phase=revalidate` `409 target_changed` `error.details`（破坏性统一）**：移除 `text` 在重验阶段对 `CodeTargetChanged` 的 `target_unconfirmed` 覆写；全 `kind`（`tap`/`long_press`/`text`/`scroll`）统一产出标准化 `phase="revalidate"` `error.details`，并在首响应成功交付新 `O` 时基于 `publishedObs` 安全注入隔离版 `candidates`/`blocking_refs`。
2. **零派发 `phase=revalidate` 高置信自动恢复**：覆盖 `transient_retry`、`text_rebind`（针对 `kind=text` + `ref`）与 `container_rebind`（针对 `kind=tap|long_press` 纯 `direct` `ref`），严格消耗单一已认领 `O` 并在 `dispatch` 前完成。
3. **顶层显式 `Result.auto_recovery`**：贯穿 `protocol`、`engine`（含 `op` 持久态与重放）、`client`、`mcp`（`result v4` 行与清单测试）、`eventlog`（去敏日志事件 `act.auto_recovery`）、`scripts/mcp-probe` 与 `docs/`；`versions.json` 升为 `0.40.0 / contract 36 / android 42`。
4. **交付与闭环**：手机整组覆盖部署 + 最小实机闭环 + Windows 主机产物同提交替换与双入口 `mcp-probe` 校验 + 验证记录 + 先代码后归档提交 + 推送远程 + 回复关闭 GitHub Issue #43。

### §2.2 本次明确不包含（Out of Scope）

1. **静态准入拒绝的自动恢复（含 GitHub Issue #52 `422 unsupported_node_action` / `reason=axis_mismatch`）**：明确排除于本次修改，已纳入后续“静态拒绝自动恢复”优化议程。
2. **R3 宽泛语义/原坐标兜底盲点**。
3. **`scroll` 重验阶段跨容器自动换滚或换轴**。
4. **`text` 派发后阶段**（`dispatchText` 内的 `focus`/`write`/`verify`/`submit` 及 `409 text_submit_not_executed`、`submit_target_unconfirmed` 等）。
5. **`app` 生命周期动作与只读查询工具**。

---

## §3 备选路线对比

| 维度 | R1：仅统一 `details` + 原瞬态重试 | **R2（选定）：统一 `details` + 零派发高置信定向重锚 + `auto_recovery`** | R3（已否决）：宽语义 / 坐标兜底点击 |
|---|---|---|---|
| 契约统一性 | 全 `kind` 消除 `text` 信息遮蔽，交付 `O` 附带 `candidates` | **同 R1** | 同 R1 |
| Case B/C 零往返恢复率 | 低（Attempt 2 `ref` 变动即拒绝） | **高（有唯一锚点与安全证明时单次调用直接成功）** | 最高但误操作不可控 |
| 误点/越权风险 | 极低 | **低（零派发重验 + 双向唯一 + 反模板非数字锚点 + 全量重新规划及 Tier-4 遮挡证明）** | **高**：同款列表/插屏广告极易错点 |
| 调用方知情权 | 静默重试无标示 | **经 `Result.auto_recovery` 顶层显式告知策略、原/新 `ref` 与尝试次数** | 同 R2 |

**选型结论**：采用 **R2**；宁可对任何不确定情况退回统一的 `409 target_changed` 并奉上有效 `candidates`，也绝不妥协 R3 的误点风险。

---

## §4 判据（何时允许恢复、何时必须拒绝或终止）

1. **硬约束优先终止条件（任何时刻满足即刻退出，禁止任何重试或重锚）**：
   - 会话撤权、`STOP` / `op.Revoked`、终态冻结、`work.Deadline` 到期、显示代次变更（`CodeStopped` / `work_deadline`）；
   - `foregroundMatches` 失败（顶层错误码 `409 foreground_changed`）。
2. **`transient_retry` 触发与成功判据**：
   - 完全保持现行 [`isTransientRevalidateError`](../../../internal/engine/act.go#L20-L34) 白名单不变（仅允许 `target_missing`、`lineage_changed`、`child_changed`、`scope_blocked`、`scroll_scope_changed`、`trajectory_blocked`、`point_changed` 进入 Attempt 2 等待 `150ms`；`target_ambiguous` 与 `capability_changed` 在 Attempt 1 立即失败）；
   - Attempt 2 使用**原始请求绑定**重验通过 -> 进入 `dispatch` 并记 `auto_recovery.strategy = "transient_retry"`（`attempts = 2`）。
3. **定向重锚触发前门禁（`Attempt 2` 仍返回 `409 target_changed` 且满足）**：
   - `max_rebind = 1`（基于手上最新的 `fresh2`，不再等待或抓第 3 帧）；
   - 仅当 `reason ∈ {target_missing, lineage_changed}` 时才进入定向重锚；若 Attempt 2 为 `capability_changed`、`scope_blocked`、`scroll_scope_changed`、`trajectory_blocked`、`point_changed`、`child_changed`、`target_ambiguous`，**直接拒绝**（原节点还在但失去能力/被遮挡/安全点失效/冒泡子变化/多解歧义时，绝不越权换绑）；
   - 目标模式限制：`text_rebind` 仅限 `kind == "text"` 且请求传入 `target.ref`；`container_rebind` 仅限 `kind ∈ {"tap", "long_press"}`、请求传入 `target.ref` 且原计划为纯直接模式（`plan.bubbleAncestorRaw == nil && plan.colocatedRaw == nil && plan.delegateRaw == nil`——明确排除 `coextensive_child`、`bubble_ancestor` 与 `bubble_colocated`）。
4. **高置信与反模板唯一性判据**：
   - `text_rebind`：稳定父 `resource-id` 锚点（优先）或全树唯一非数字静态标签（兜底），均要求候选数量在 `fresh2` 严格 `== 1`、满足 `isValidTextPublicTarget` & `freshTextTargetSafe(raw)`、水平投影重叠 `>= 0.5`、并经全新直接选点规划 + `tapFrameTier4OcclusionProven` 证明无遮挡；若 `>= 2` 则收口为 `reason="target_ambiguous"` 拒绝。
   - `container_rebind`：原目标 `T_orig`（节点自身 `Text`/`Description` 与后代 `readableSubtree` 合计）必须含至少 1 条不含数字的非空静态文本且在 **`obs` 与 `fresh2` 全树出现频次均严格为 `1`**（反模板封杀重复“购买/删除”按钮与纯数字卡片）；新树候选 `C` 与 `T_orig` 骨架数字归一化后满足**新增 2/3 多重集重叠判据（`inter * 3 >= union * 2`）**、满足 `horizontalOverlapRatio >= 0.5` 与面积比 `[0.25, 4.0]`、全树严格唯一（`len == 1`，`>= 2` 则立即拒绝为 `target_ambiguous`），并通过全新直接规划 + 无条件 `tapFrameTier4OcclusionProven` 证明安全点击。

---

## §5 算法、状态机与契约设计

### §5.1 统一 `409 target_changed` `error.details` 与交付 `O` 候选计算

1. **移除 `text` 重验 `target_unconfirmed` 覆写**：
   - [`Act`](../../../internal/engine/act.go#L158) 移除重验错误时对 [`decorateTextRevalidateError`](../../../internal/engine/act.go#L2142-L2160) 的调用；[`textReasonTargetUnconfirmed`](../../../internal/engine/text_stage.go#L46) 若无其他调用点可清理，而 `dispatchText` 内各后段错误（`stage=focus|write|verify|submit`、`submit_target_unconfirmed` 等）完整保留。
   - 当 `text` 在 `phase=revalidate` 拒绝时，`write="not_attempted"`（与请求的 `submit="not_attempted"`）仍由顶层 `Result.TextInput` 表达，`error.details` 忠实输出下表统一契约。
2. **统一 `phase=revalidate` `error.details` 契约**：

| 字段 | 条件 | 类型与规则 |
|---|---|---|
| `phase` | 必选 | `"revalidate"` |
| `kind` | 必选 | `"tap"` / `"long_press"` / `"text"` / `"scroll"` |
| `reason` | 必选 | 封闭集合 `act_target_shared.go#L62-L72`（`child_changed`、`target_missing`、`target_ambiguous`、`lineage_changed`、`capability_changed`、`scroll_scope_changed`、`trajectory_blocked`、`scope_blocked`、`point_changed`） |
| `original_ref` | 请求含 `target.ref` 或 `container.ref` 时必选；**任意纯坐标操作（`tap`/`long_press`/`text`/`scroll` 使用 `{x, y}`）一律省略** | 请求原始 `ref` 字符串 |
| `changed_ref` | 仅当 `reason="lineage_changed"` 且唯一确定断裂公开祖先时可选 | 同现行 [`attachChangedRef`](../../../internal/engine/act_target_shared.go#L264-L281) |
| `recovery_attempted` | 当本次实际进入过至少一种恢复尝试（如 `transient_retry`、`text_rebind`、`container_rebind`）但最终仍失败时出现；首轮直拒省略 | `[]string`，如 `["transient_retry","container_rebind"]` |
| `candidates` | **仅限首响应成功交付新 `O`（`publishedObs != nil`）且存在合格候选时出现，且仅存在于即时返回体而不落入 `op.Error`** | 按下方双路径逻辑基于 `publishedObs.Projection` 计算，格式复用 [`buildStaticCandidates`](../../../internal/engine/act_target_shared.go#L581-L660) |
| `blocking_refs` | **仅限首响应成功交付新 `O` 且在新帧定位到遮挡节点时出现，且不落入 `op.Error`** | 由 `publishedObs.Projection` 映射的公共阻塞 `ref` 切片 |

3. **交付 `O` 上 `candidates` / `blocking_refs` 的双路径安全计算**：
   - 将 [`deliverRejectedObservation`](../../../internal/engine/delivery.go#L25-L74) 与 `deliverRejection` 返回值扩展为 `(*protocol.Observation, *observationState)`，由 [`publishTreeAttempt`](../../../internal/engine/capture.go#L573) 直接交出已公开的 `publishedObs` 与 `newView := buildTapPublicView(publishedObs.Projection.Tree)`。
   - **路径 A（新帧 `fresh` 存在唯一命中节点 `freshRawIdx`）**：覆盖原 `ref` 目标存在但失效的 `scope_blocked`、`capability_changed`、`scroll_scope_changed`、`trajectory_blocked`。通过 `publicNodeByRawIndex(publishedObs.Projection, freshRawIdx)` 取得属于 `newView` 的合法非空 `freshTarget *protocol.Node`；若 `freshTarget == nil`（如节点失去公开映射触发 `capability_changed`），回退至路径 B 兜底。取得 `freshTarget != nil` 后，随后复用 `collectTapScope` + `diagnoseTapStaticFailure` + `collectDirectBlockingDiagnostics`（或滚动 `planScrollOnAxes` 产出的遮挡节点）生成 `blocking_refs`，并用 `freshTarget` 调用现有 `collectTapFallbackCandidates` / `collectTextFallbackCandidates` / `collectScrollAncestorAndPageCandidates` 生成 `candidates`。
   - **路径 B（新帧无唯一节点：`target_missing` / `target_ambiguous` / `lineage_changed` / `child_changed` 或所有无 `ref` 的坐标操作）**：不得把旧 `O` 的 `*protocol.Node` 指针或旧 `ref` 传给 `collect*FallbackCandidates`。改为新增不依赖 `view.info[target]` 的只读收集器（如 `collectTapFallbackCandidatesByRect`、`collectTextFallbackCandidatesByRect` 与传入空排除串的 `collectPageScrollCandidates(publishedObs, newView, "")`）：
     1. 比较 `obs.Screen` 与 `publishedObs.Screen` 的宽高与旋转角：若一致，则以旧 `target` 公共边界（或原请求坐标点）在新屏上的中心距离与相交面积做主排序；若尺寸/旋转变化、或按矩形相交筛选结果为空，**退化为全页合法可执行节点排序候选**（按控件可读性/面积/次序截断），不再因 `inter.Empty()` 直接吞掉候选；
     2. 对于明确有坐标遮挡的坐标 `tap`/`point_changed`，在新屏同一坐标处经 `externalInteractiveOwnersAt` / `blockingOverlayNodes` 解析 `blocking_refs`。

### §5.2 显式结果标记：`Result.auto_recovery`

1. **JSON 结构体（`internal/protocol/responses.go`）与固定成员序**：
   - 新增 `AutoRecoveryView`：
     - `Strategy string`（`"transient_retry"` | `"text_rebind"` | `"container_rebind"`，必选非空）
     - `OriginalRef string`（请求使用 `ref` 时必选；**纯坐标请求为 `transient_retry` 时省略**）
     - `RecoveredRef string`（请求使用 `ref` 时必选：`transient_retry` 等于 `OriginalRef`，`*_rebind` 为新公共 `ref`；**纯坐标请求省略**）
     - `Attempts int`（>= 1，经 `transient_retry` 或 Attempt 2 后续 `rebind` 时固定记 `2`）
   - 在 `Result` 中放置于 **`TextInput` 之后、`TargetReference` 之前**（对应 `MarshalJSONTo` / `UnmarshalJSONFrom` 严格顺序，`UnmarshalJSONFrom` 使用 `nullableMember`/`readObject` 并拒绝未知键/重复键/非法的 `null`）。
2. **`MCP result v4` 投影规则**：
   - 在 `internal/mcp/result_text.go`（`encodeResultText`）处于**第 1 节“控制标量”末尾、`obs` 行之前**输出单行：
     `auto_recovery strategy=<strategy> [original_ref=<ref>] [recovered_ref=<ref>] attempts=<n>`
   - 在 `v4ResultFields` 注册 `auto_recovery`（`v4Object`）、`auto_recovery.strategy`/`original_ref`/`recovered_ref`（`v4Token`）、`auto_recovery.attempts`（`v4Literal`）；在 `resultFields` 注册对应 `fieldObject`/`fieldToken`/`fieldLiteral`；在 `result_manifest_test.go` 更新 `rmfCompositeInventory` 与 `rmfResultFixtures()`，并在 `scripts/mcp-probe/result_v4.go` 更新字段顺序 `lastRank` 表。

### §5.3 零派发 `phase=revalidate` 自动恢复状态机

```text
进入 phase=revalidate（仅消耗当前已认领的同一个 O）：
  检查 STOP / revoked / work_deadline / display / foregroundMatches -> 异常立即退出

  Attempt 1: capture(fresh1) -> revalidateActTarget
    ├─ 通过 -> dispatch (auto_recovery = nil)
    ├─ foreground_changed / STOP / work_deadline -> 立即退出
    ├─ !isTransientRevalidateError (target_ambiguous / capability_changed)
    │    -> 立即失败并交付新 O（不重试、不 rebind、无 recovery_attempted）
    └─ isTransientRevalidateError (含 target_missing / lineage_changed 等)
         -> 等待 actRevalidateRetryDelay (150ms，可随时被 STOP / ctx / deadline 打断)
         -> Attempt 2: capture(fresh2) -> revalidateActTarget
              ├─ 通过 -> 记录 candidateAutoRecovery = {transient_retry, attempts:2} -> 进入 dispatch 提交点
              ├─ foreground_changed / STOP / work_deadline -> 立即退出
              └─ 仍为 target_changed -> 记录 attempted = ["transient_retry"]
                   ├─ 若满足 §4 门禁 (reason ∈ {target_missing, lineage_changed} 且模式匹配):
                   │    基于 fresh2（零再等待、零再抓屏）执行最多 1 次 text_rebind / container_rebind
                   │      ├─ 成功并通过全量安全点+遮挡证明 -> candidateAutoRecovery = {text_rebind|container_rebind, origRef, recRef, attempts:2} -> dispatch
                   │      └─ 失败（含多候选转 target_ambiguous 或遮挡转 scope_blocked）-> attempted 追加对应策略名 -> 走统一 409 拒绝与交付新 O
                   └─ 不满足门禁 -> 直接走统一 409 拒绝与交付新 O
```

> **提交时机（Minor 1）**：`op.AutoRecovery` 与 `result.AutoRecovery` 仅在进入派发时于 `e.mu` 内由 `setOperationPhase(op, "dispatch")`（同时重验 `op.State == OperationRunning && e.work == work && e.workAuthorizedLocked(...) && work.Ctx.Err() == nil`）写入；写入后由 `newResult(op)`（`operations.go:517`）、`operationViewLocked(op)`（`engine.go:1295`）与 `statusOperationLocked`（`status.go:94`）深拷贝回放。若 `rebind` 后瞬时触发 `STOP` 或超期导致未进入 `dispatch`，`op.AutoRecovery` 保持为空。

### §5.4 策略 A：`text_rebind`

1. **支路 A1（稳定父 `resource-id` 锚点 + 唯一可用子输入框）**：
   - 使用原始 `RawNode` 与公开树的分层原语：沿原目标父链在 `maxParentAnchorDepth = 8` 内通过 `uniqueTextParentAnchor` 语义（要求 `!isRawWindowRoot && !textParentAnchorBoundary` 且具非空 `ResourceID`）查找最近的唯一可辨识父容器 `P_orig`——要求 `(Package, ClassName, ResourceID)` 在 `obs` 原始节点与对应公共 `(Role, Resource)` 均唯一匹配（为适应 Case B 弹层/键盘挤压引起的父框缩放，定位 `P_orig -> P_fresh` **不要求** [`tightGeometryCompatible`](../../../internal/engine/act_revalidation_policy.go#L1153) `<20%` 严格边框相近，而是基于 `(Package, ClassName, ResourceID)` 全树唯一性直接对应）。
   - 在 `fresh2` 中以 `P_fresh` 为根枚举所有可输入后代（含 `P_fresh` 自身若可编辑），筛选同时满足公共视图 [`isValidTextPublicTarget(C)`](../../../internal/engine/tap_scope.go#L1807-L1821) 与原始节点 `freshTextTargetSafe(rawC)` 的节点：
     - 若数量 `>= 2`：中止重锚，将错误归因为 `reason="target_ambiguous"`；
     - 若数量 `== 1`：进一步检查 `C` 与 `P_fresh`（及 `obs` 上 `P_orig` 水平跨度）满足 `horizontalOverlapRatio >= 0.5`；
     - 然后构建针对 `C` 的新 `tapInput` 执行 [`collectTapScope`](../../../internal/engine/tap_scope.go#L552) + [`chooseTapPlan`](../../../internal/engine/tap_scope.go#L817) + 无条件 [`tapFrameTier4OcclusionProven`](../../../internal/engine/act_target_shared.go#L241-L258)（选点规划或遮挡证明不通过均按现行 `ref` 目标语义收口为 `scope_blocked`）；全部通过后再核验 `foregroundMatches` 与 `STOP`，绑定 `C.Ref` 并带入 `dispatchText`（`focus`/`write`/`verify`/`submit` 原样执行）。
2. **支路 A2（无可用父 `resource-id` 时的全树唯一静态非数字锚点）**：
   - 仅在支路 A1 无符合条件父容器且原输入框与候选均非密码字段时启用；
   - 取原目标自身（或直属不可拆分占位文本中）的非空、**不含数字**精确文本（排除动态计数/金额/时间与已输入值本身易变动的部分），要求该精确字符串在 **`obs` 全树出现次数严格为 `1` 且在 `fresh2` 全树非密码可见 `Text`/`Description` 中出现次数严格为 `1`**；
   - 筛选同时满足 `isValidTextPublicTarget(C)`、`freshTextTargetSafe(rawC)`、`horizontalOverlapRatio >= 0.5` 与面积比 `[0.25, 4.0]` 的唯一候选（`>= 2` 转 `target_ambiguous`），并经与支路 A1 相同的全新直接规划 + Tier-4 遮挡证明门禁后进入 `dispatchText`。

### §5.5 策略 B：`container_rebind`

1. **严格限定对象**：仅 `kind ∈ {"tap", "long_press"}`、传入 `target.ref` 且原静态规划为纯直接目标（`plan.bubbleAncestorRaw == nil && plan.colocatedRaw == nil && plan.delegateRaw == nil`）；`coextensive_child`、`bubble_ancestor` 与 `bubble_colocated` 均禁止进入 `container_rebind`。
2. **文本采集与反模板双全树唯一性**：
   - 修复现行 `readableSubtree` 不包含节点自身文本的局限：为 `T_orig` 与每个候选 `C` 定义 `nodeWithSubtreeReadable(node)`，合并采集**节点自身非空 `Text`/`Description` + 后代 [`readableSubtree`](../../../internal/engine/act_revalidation_policy.go#L1296)**（跳过密码与无效空白）；
   - 从 `T_orig` 采集集剔除 [`isDynamicNumericToken`](../../../internal/engine/act_revalidation_policy.go#L1367) 后必须至少剩 1 条精确静态文本；且至少有 **1 条静态无数字文本在 `obs` 全树出现频次严格为 `1` 并在 `fresh2` 全树出现频次同样严格为 `1`**；
   - 若 `T_orig` 自身静态非数字标签原本非空，则候选 `C` 自身静态非数字文本不得变成完全不相干词条。
3. **新增 2/3 多重集覆盖与几何约束（不混用现行 100% 全等函数名）**：
   - 将原目标与候选集分别剔除纯数字项并用 [`numericSkeleton`](../../../internal/engine/act_revalidation_policy.go#L1381) 归一化后构成字符串多重集 `M_orig` 与 `M_cand`；计算多重集交集大小 `inter = Σ min(M_orig[s], M_cand[s])` 与并集大小 `union = Σ max(M_orig[s], M_cand[s])`，明确新增并要求 **`inter * 3 >= union * 2`**（`sameReadableMultiset` 仍维持其 100% 全等语义供旧路径使用，两者互不混淆）；
   - 同时要求候选满足 [`validTapPublicTarget`](../../../internal/engine/tap_scope.go#L922-L940)（支持请求的 `tap`/`long_press`）、非 `RoleRoot`、非全屏兜底大框、与原目标框满足 `horizontalOverlapRatio >= 0.5` 且面积比在 `[0.25, 4.0]`；
   - 在全 `fresh2` 公共树上必须**严格仅有 `1` 个候选 `C`** 满足全部条件；若 `>= 2` 立即以 `target_ambiguous` 退出。
4. **全新安全选点与无条件 Tier-4 遮挡证明（绝不复用旧坐标与 `resolveDirectTapScopeFrame`）**：
   - 不使用绑定旧 `plan.x/y/carrierRaw` 的 [`resolveDirectTapScopeFrame`](../../../internal/engine/act.go#L1503-L1600)；而是以 `C` 为目标新建直接 `tapInput`，通过 `collectTapScope` + `chooseTapPlan`（仅直接模式，禁止 `bubble` 漂移或 `coextensive_child`）计算新鲜落点 `(x, y)`，再**无条件**调用 [`tapFrameTier4OcclusionProven(viewFresh, C, x, y)`](../../../internal/engine/act_target_shared.go#L241-L258) 证明无外部悬浮遮挡且最深命中一致；未通过则统一收口为 `scope_blocked`。

---

## §6 工程落地设计

### §6.1 分文件改动矩阵

| 模块 / 文件 | 改动职责 |
|---|---|
| [`release/versions.json`](../../../release/versions.json) & [`scripts/generate-version.ps1`](../../../scripts/generate-version.ps1) | `release_version` -> `0.40.0`、`contract_version` -> `36`、`android_version_code` -> `42`（运行脚本同步更新 `internal/protocol/version_generated.go`、`GeneratedVersion.java`、`strings_version.xml`、`AndroidManifest.xml`） |
| [`internal/protocol/errors.go`](../../../internal/protocol/errors.go#L10-L29)、[`responses.go`](../../../internal/protocol/responses.go#L815-L1089)、[`decode_response.go`](../../../internal/protocol/decode_response.go#L512-L578) | `APIError.Details` 白名单纳管 `phase`、`kind`、`original_ref`、`recovery_attempted`、`candidates`、`blocking_refs`；新增 `AutoRecoveryView` 与 `Result.AutoRecovery`（严格 JSON 编解码，排在 `text_input` 后、`target_reference` 前） |
| [`internal/engine/delivery.go`](../../../internal/engine/delivery.go#L14-L74) | `deliverRejectedObservation` 与 `deliverRejection` 返回 `(*protocol.Observation, *observationState)`，透传已发布 `publishedObs` |
| [`internal/engine/engine.go`](../../../internal/engine/engine.go#L567-L624,L1295-L1360)、[`operations.go`](../../../internal/engine/operations.go#L196-L240,L517-L558)、[`status.go`](../../../internal/engine/status.go#L94-L154) | `operationState` 增存 `AutoRecovery *protocol.AutoRecoveryView`；在 `setOperationPhase(op, "dispatch")` 成功锁内提交，并通过 `newResult`、`operationViewLocked`、`statusOperationLocked`、`applyFrozenResult` 一致回放 |
| [`internal/engine/act.go`](../../../internal/engine/act.go#L127-L177,L2142-L2160)、[`act_target_shared.go`](../../../internal/engine/act_target_shared.go#L62-L95)、`internal/engine/act_rebind.go`（拟新增）、[`tap_scope.go`](../../../internal/engine/tap_scope.go#L1823-L2000)、[`scroll.go`](../../../internal/engine/scroll.go#L1484-L1513) | 1) 移除重验 `CodeTargetChanged` 的 `decorateTextRevalidateError`；<br>2) 统一构建 `phase=revalidate` `error.details`（含全坐标安全路径）与基于 `publishedObs` 的双路径 `candidates`/`blocking_refs` 收集器；<br>3) 实现 `text_rebind` 与 `container_rebind`；<br>4) 先 `finishWithError` 落盘无悬垂 `ref` 的 `op.Error`，再在首响应上叠加 `publishedObs` 的 `candidates`/`blocking_refs` |
| [`internal/eventlog/log.go`](../../../internal/eventlog/log.go#L63-L108) | 仅新增白名单键 `"strategy"`，复用意义一致的现有单数键 `"attempt"` 与 `"reason"`/`"outcome"`/`"ms"` 记录 `"act.auto_recovery"`；**严禁写入 `original_ref` / `recovered_ref` 等任何节点 `ref` 或文本正文** |
| [`internal/client/`](../../../internal/client/errors.go) | 确认客户端强类型解码支持 `Result.AutoRecovery` 与统一 `Details`，补充单测 |
| [`internal/mcp/result_text.go`](../../../internal/mcp/result_text.go#L22-L129,L323-L529,L2065-L2117)、[`result_manifest_test.go`](../../../internal/mcp/result_manifest_test.go#L99-L122)、[`result_text_v4_test.go`](../../../internal/mcp/result_text_v4_test.go)、[`result_parity_test.go`](../../../internal/mcp/result_parity_test.go#L538-L545)、[`schema.go`](../../../internal/mcp/schema.go#L296) | 投影 `auto_recovery` 行并注册 `v4ResultFields`（`v4Object`/`v4Token`/`v4Literal`）、`resultFields`（`fieldObject`/`fieldToken`/`fieldLiteral`）、`rmfCompositeInventory` 与 `rmfResultFixtures()`；如需微调 `schema.go` 指引则同步更新 `TestCatalogFingerprintBaseline` |
| [`scripts/mcp-probe/result.go`](../../../scripts/mcp-probe/result.go)、[`result_v4.go`](../../../scripts/mcp-probe/result_v4.go)、[`result_semantics.go`](../../../scripts/mcp-probe/result_semantics.go)、[`probe_test.go`](../../../scripts/mcp-probe/probe_test.go) | 探针同步解析、校验与顺序检验 `auto_recovery`（含 `lastRank` 与 `both` 语义比对器） |

### §6.2 协议字段序、解码与防悬垂隔离顺序

`Act` 重验失败时的精确执行顺序如下（严禁颠倒）：

1. 完成重验与可选重锚判定，得到不包含 `candidates`/`blocking_refs` 的纯净 `revalidateErr`；
2. 若需交付新观察，调用 `public, publishedObs = deliverRejection(...)`（此时 `publishedObs` 与 `public` 同属最新已公开观察 `O_new`）；
3. 调用 `result := e.finishWithError(op, "revalidate", revalidateErr)`：将纯净的 `revalidateErr.Public()` 持久写入 `op.Error`（确保 `status(P)` 与同 `P` 重放永不含任何即时 `ref`）；
4. **仅当 `public != nil && publishedObs != nil` 时**，在本次返回的局部 `result.Error.Details` 副本上注入基于 `publishedObs.Projection` 计算的 `candidates` 与 `blocking_refs`。

### §6.3 事件日志与脱敏边界

- 新增日志事件 `act.auto_recovery`，仅记录白名单标量与枚举：`strategy`、`attempt`（单数，复用现有 [`log.go#L63-L108`](../../../internal/eventlog/log.go#L63-L108) 键）、`outcome`、`reason`、`ms`、`operation_id`、`observation_id`；
- 按 [`AGENTS.md`](../../../AGENTS.md) 第 10 条与 [`log.go#L105`](../../../internal/eventlog/log.go#L105) 硬约束：**`original_ref`、`recovered_ref`、`changed_ref`、节点树与任何文本均不得进入 `eventlog`**。

### §6.4 与既有不变式的关系

| 既有硬约束 / 不变式 | 本方案下的保持方式 |
|---|---|
| 唯一权威（`internal/engine`） | 自动恢复状态机、`AutoRecovery` 字段提交与 `O` 生命周期全部留在 `internal/engine`，HTTP/MCP/Client 不存第二份状态 |
| 单向消费 `O` 与提交条件化 | 整个 `phase=revalidate`（含 `transient_retry` 与至多 1 次 `rebind`）仅消费当前同一已认领 `O`，派发前仍重验会话/显示代次/`STOP`/`foregroundMatches` |
| `STOP` 不等待 | 等待 `150ms` 与内存重锚前后均随时响应撤权、`STOP` 与 `work.Deadline`，立即终止并零派发 |
| 状态锁短小 | `rebind` 纯内存树扫描与交付 `O` 上 `candidates` 收集全部在锁外完成；`e.mu` 内仅更新 `op.AutoRecovery` 与阶段标记 |
| 事实分层与防悬垂隔离 | 先 `finishWithError` 写入干净 `op.Error`，首响应专属 `candidates`/`blocking_refs` 永不落盘到 `status(P)`，已恢复且进入派发则长期保持 `Result.auto_recovery` |

### §6.5 文档与契约检查清单

实施阶段需同步修订以下正式文档并在提交前通过 [`AGENTS.md`](../../../AGENTS.md) 第 15 条**独立只读文档审计**：

| 文档位置 | 须更新内容 |
|---|---|
| [`docs/interface.md`](../../../docs/interface.md#L61-L105) 第 61–105 行、第 842–888 行、第 994–1005 行 | 1) L61–68 在 `Result` 字段顺序表中加入 `auto_recovery`（位于 `text_input` 后、`target_reference` 前）；<br>2) L86–105 在 `error.details` 白名单纳管 `phase`、`kind`、`original_ref`、`recovery_attempted`、`candidates`、`blocking_refs`，移除“重验阶段 `stage`/`reason=target_unconfirmed`/`dispatch` 专属 `text`”表述；<br>3) L842–888 与 L994–1005 改为全 `kind` 统一 `phase=revalidate` `409 target_changed` 契约，标明首响应交付新 `O` 才附 `candidates`/`blocking_refs` 且不入 `status(P)`，`text` 未派发状态看 `Result.text_input`。 |
| [`docs/mcp.md`](../../../docs/mcp.md#L57-L80) 第 57–80 行、第 348–391 行、第 440–455 行 | 1) 取消 `changed_ref`“非 `text` 动作”限定；<br>2) L348–366 `result v4` 固定输出顺序（控制标量末、`obs` 前）补充 `auto_recovery strategy=... [original_ref=...] [recovered_ref=...] attempts=...` 投影规则与 L387–391 示例。 |
| [`docs/runbook.md`](../../../docs/runbook.md#L287) 第 287 行 | 补充利用统一 `details.candidates` / `details.blocking_refs` 与理解 `Result.auto_recovery` 的操作指引。 |

---

## §7 预期效果、不能治什么与成本

### §7.1 预期效果与防线

1. **消除不必要的客户端往返**：Issue #43 Case B（稳定父 `resource-id`/唯一静态非数字锚点输入框重号）与 Case C（直接可点卡片包装层变动但唯一非数字卡标题+2/3 骨架守恒）在一次 `act` 调用内安全自愈，不再迫使大模型二次读树重发；
2. **报错完全透明且无失真**：无论何种 `kind` 在 `phase=revalidate` 失败，调用方均拿到统一结构 `details`（准确 `reason` + 新 `O` 有效 `candidates`/`blocking_refs`）；自动恢复成功时，无需事后查诊断即可经 `Result.auto_recovery` 清楚知道更换了哪个 `ref`。

### §7.2 不能治什么（诚实边界）

1. **不能治静态准入错误（含 GitHub Issue #52）**：认领 `O` 之前的 `422 unsupported_node_action`（`axis_mismatch`）、`422 invalid_target`、`422 no_safe_trajectory`、`422 no_safe_point` 均不在本次治域，交后续独立议程处理。
2. **不能治 `scroll` 重验换容器或改轴**：滚动容器丢失或能力丧失直接返回含滚动候选的 `409 target_changed`。
3. **不能治模板化重复控件、纯图标与纯数字卡片**：存在多个同款按钮（如两个“购买”）或仅靠纯数字/图标识别的控件会在全树频次或唯一性校验被安全拦截（或收口为 `target_ambiguous`）。
4. **不能治 `coextensive_child` 与 `bubble_*` 委托链路**：此类容器已属间接代理，不叠加 `container_rebind` 以防二重漂移。
5. **不能治真实跳页（`409 foreground_changed`）、全遮挡（`scope_blocked`）、能力丧失（`capability_changed`）、`STOP`/超时，以及 `dispatchText` 派发后失败**。

### §7.3 实现与运行成本

- 重锚匹配只在 `Attempt 2` 失败后于内存公共/原始视图执行单次线扫（典型公共树 < 200 节点，耗时 < 1ms），不新增任何 ADB RPC 或截屏往返；
- 不增加引擎状态锁持锁时间（所有树检索与候选收集均在锁外执行）。

---

## §8 测试策略、实机验证、发布交付与闭环

按 [`AGENTS.md`](../../../AGENTS.md) 第 24 条，所有全量 Go 测试、发布构建、实机部署与验证均由**主代理亲自执行与判定**。

### §8.1 Go 单元测试（含明确反控用例）

1. **统一详情与破坏性切换验证**：
   - `tap` / `long_press` / `text` / `scroll` 在 `phase=revalidate` 均输出 `phase="revalidate"`、`kind`、`reason`、`original_ref`（纯坐标省略 `original_ref`与 `recovered_ref`）；
   - 验证 `text` 重验失败再无 `target_unconfirmed` 与 `details.dispatch`，而 `Result.TextInput.Write="not_attempted"` 保留、且 `lineage_changed` 可带 `changed_ref`；更新受此破坏性变更影响的旧断言测试；`dispatchText` 后段 `submit_target_unconfirmed` 不受影响。
2. **双路径交付 `O` 候选与反悬垂 `ref` 反控**：
   - 有唯一命中节点（如 `scope_blocked`）与无唯一节点（`target_missing` / 纯坐标 `point_changed` / 分辨率或键盘位移下）均能在交付新 `O` 上安全生成非空 `candidates`/`blocking_refs`；
   - **反控用例**：对同一失败 `P` 调用 `status(P)` 与同 `P` 重放，断言 `Result.Observation == nil` 且 `Result.Error.Details` 不含 `candidates` 与 `blocking_refs`。
3. **自动恢复正向与反控用例**：
   - 正向：`transient_retry`（`attempts: 2`）；`text_rebind`（支路 A1 父容器缩放下的稳定 `resource-id` + 支路 A2 无 `resource-id` 唯一静态文本）；`container_rebind`（卡片自文本+子文本骨架 >= 2/3、徽标计数 `"3赞"->"4赞"` 微变且 Tier-4 无遮挡通过）；同时验证 `Result.auto_recovery` 在直接响应与后续 `status(P)` 重放中一致呈现；
   - **反控用例**：
     - 全树出现 2 个同名锚点（双“购买”按钮）绝不触发 `container_rebind`，收口 `target_ambiguous`；
     - 仅有数字文案或无静态唯一文本绝不触发重锚；
     - `container_rebind` 候选被浮窗遮挡时绝不点击，返回 `scope_blocked` 且 `auto_recovery == nil`；
     - `coextensive_child`、`bubble_ancestor`、`bubble_colocated` 与 `scroll` 绝不触发 `container_rebind`；
     - `150ms` 等待期间遇到 `STOP` / `work_deadline` / `foreground_changed` 立即退出，`auto_recovery == nil`。
4. **协议、MCP 清单、探针与日志白名单全套测试**：
   - 严格 JSON 解码测试；`TestResultFieldManifest`、`TestAllToolResultFixturesParity`、`TestCanonicalJSONCoversManifest`、`TestCatalogFingerprintBaseline`（若 `schema.go` 有改动）；`scripts/mcp-probe` `probe_test.go`；`eventlog` 验证 `"act.auto_recovery"` 只记白名单键且不含任何 `ref`。

### §8.2 发布交付、双入口探针、远程推送与 Issue #43 闭环

按 [`AGENTS.md`](../../../AGENTS.md) 第 19 条与第 24 条以及用户确认范围执行：

1. **构建与清单核验**：修改 `release/versions.json` -> 运行 `scripts/generate-version.ps1` -> `scripts/build-release.ps1` -> `scripts/verify-manifest.ps1`。
2. **手机整组覆盖部署与最小实机闭环**：覆盖部署真机全套产物（含手机 HTTP MCP，**保留 App 私有数据**）；运行覆盖本次改动行为的最小实机闭环（包含：重验失败统一 `409 target_changed` 附带新 `O` 与有效 `candidates`、实机/受控漂移重验恢复与 `Result.auto_recovery` 输出）。
3. **Windows 主机产物替换与双入口独立探针**：同一源码提交构建并替换 Windows 主机的 `build/device-mcp.exe` 与 `build/mcp-probe.exe`；核验主机清单后分别使用新 `build/mcp-probe.exe` 验证 **主机 stdio MCP** 与 **手机 HTTP MCP**（**明确边界**：宿主 Agent 配置、托管 MCP 进程重启与重连由用户处理）。
4. **文档审计、分步提交、远程推送与 Issue 闭环**：`docs/` 变更先经独立只读文档审计 subagent 审结至 `clean`；落盘验证记录后再按**先提交实现、后补填 §11 并移入 `implemented/` 提交归档**顺序提交；最后推送远程并在 GitHub Issue #43 回复总结（写明已落地版本 `0.40.0`/`contract 36`、核心能力、Issue #52 已单独分诊纳入后续静态拒绝议程）后关闭 Issue #43。

### §8.3 验证记录落盘位置

- 实现与实机交付完成后，验证记录将写入 `maintenance/verification/2026-11-28-act-revalidation-auto-recovery-and-details.md`（区分磁盘替换、独立探针进程验证、最小实机闭环与宿主 Agent 接管边界）。

---

## §9 已定与未决

### §9.1 已定事项

1. 路线选择 **R2**：统一 `409 target_changed` `error.details`（不向后兼容 `text` `target_unconfirmed`）+ 零派发 `transient_retry` / `text_rebind` / `container_rebind` + 顶层 `Result.auto_recovery`；拒绝 R3。
2. 保留 `isTransientRevalidateError` 与 `150ms` Attempt 2 优先机会；Attempt 2 仍 `target_missing`/`lineage_changed` 时执行最多 1 次纯内存高置信 `rebind`。
3. 本次不改静态准入（Issue #52 明确列入后续优化议程）；实施包含发布、双端交付、推远程与回复关闭 GitHub Issue #43。

### §9.2 未决事项

| 事项 | 责任人 | 截止节点 | 现时状态 |
|---|---|---|---|
| `Result.auto_recovery` 字段命名与策略枚举（`transient_retry` / `text_rebind` / `container_rebind`）、以及破坏性移除 `text` 重验 `target_unconfirmed` 的最终批准 | 用户 | **提案确认门禁（实施前）** | 已确认：用户于 2026-11-28 直接指派实施本提案，字段、策略与破坏性移除按 §5.2 执行 |
| `internal/mcp/schema.go` 第 296 行 `act` 工具说明文字是否微调以补充 `candidates` / `auto_recovery` 提示（影响 `CatalogFingerprint` 基线） | 主代理（依据审校与简洁约束） | **实施第 3 步 MCP 同步阶段前** | 已定：保持 `schema.go` 与工具说明不变；由 `result v4` 行与 `docs/` 表达，`CatalogFingerprint` 基线不变 |
| `scroll` 重验阶段 `candidates` 定位参考当原容器失配时是否额外展示全页同向候选排序优先级 | 主代理（实现 `scroll_overlay`/候选收集阶段按源码单测收口） | **实施第 2 步引擎改造阶段** | 已定：同框优先；无交集、尺寸或旋转变化时降级全页合法可执行候选 |

> 确认记录：用户于 2026-11-28 直接指派本提案的实施，视为对 §9.1 已定事项与上表取舍的确认；实现偏差按 §11.2 记录。

---

## §10 实施顺序与改动文件清单

1. **版本与协议**：更新 `release/versions.json` 并运行 `scripts/generate-version.ps1`；修改 `internal/protocol/{errors,responses,decode_response}.go`。
2. **引擎与日志**：修改 `internal/engine/{delivery,engine,operations,status,act,act_target_shared,tap_scope,scroll}.go`，新增 `internal/engine/act_rebind.go`，更新 `internal/eventlog/log.go`。
3. **客户端、MCP 与探针**：修改 `internal/client/`、`internal/mcp/{result_text,result_manifest_test,result_text_v4_test,result_parity_test,schema}.go`、`scripts/mcp-probe/{result,result_v4,result_semantics,probe_test}.go`。
4. **文档与审计**：同步 `docs/{interface,mcp,runbook}.md` 并执行第 15 条独立只读文档审计至 `clean`。
5. **全量验证、发布、归档、推送与闭环**：主代理亲跑全量 Go 测试（15m 超时）-> 构建校验 release -> 手机部署与最小实机闭环 -> 主机产物替换与双入口 `mcp-probe` 验证 -> 写 `maintenance/verification/` -> 先提交实现 -> 再回填 §11 并移入 `maintenance/proposals/implemented/` 提交归档 -> 推送远程 -> 回复并关闭 GitHub Issue #43。

---

## §11 执行记录与偏差说明

### §11.1 已落地实现摘要

- 关联实现提交：`c12d28876cf663d9d527e99eaba827bb3a426815`（提案入库提交 `2e19762`）。
- 发布与验证：`0.40.0 / contract_version 36 / android_version_code 42`；集成测试、发布构建、手机整组部署、实机最小闭环与双入口探针结果见[验证记录](../../verification/2026-11-28-act-revalidation-auto-recovery-and-details.md)。

| 提案条目 | 代码落点 |
|---|---|
| 统一 `phase=revalidate` `409 target_changed` details；移除 text `target_unconfirmed` | [`act.go`](../../../internal/engine/act.go)、[`act_recovery.go`](../../../internal/engine/act_recovery.go)、[`act_rejection_diagnostics.go`](../../../internal/engine/act_rejection_diagnostics.go) |
| 首响应交付新 O 的隔离 `candidates`/`blocking_refs` | [`delivery.go`](../../../internal/engine/delivery.go)、[`act_rejection_diagnostics.go`](../../../internal/engine/act_rejection_diagnostics.go)、[`act_target_shared.go`](../../../internal/engine/act_target_shared.go) |
| `Result.auto_recovery` 与 dispatch 闸门提交/回放 | [`responses.go`](../../../internal/protocol/responses.go)、[`decode_response.go`](../../../internal/protocol/decode_response.go)、[`act_recovery.go`](../../../internal/engine/act_recovery.go)、[`operations.go`](../../../internal/engine/operations.go)、[`engine.go`](../../../internal/engine/engine.go) |
| `transient_retry` / `text_rebind` / `container_rebind` | [`act.go`](../../../internal/engine/act.go)、[`act_rebind.go`](../../../internal/engine/act_rebind.go)、[`act_revalidation_policy.go`](../../../internal/engine/act_revalidation_policy.go) |
| 事件日志 `act.auto_recovery` 与 `strategy` 白名单 | [`internal/eventlog/log.go`](../../../internal/eventlog/log.go) |
| `result v4`、client、mcp-probe 同步 | [`result_text.go`](../../../internal/mcp/result_text.go)、[`scripts/mcp-probe`](../../../scripts/mcp-probe/) |
| 版本 / 文档 / README | [`release/versions.json`](../../../release/versions.json)、[`docs/`](../../../docs/)、[`README.md`](../../../README.md) |

### §11.2 与提案差异、未执行项与审阅响应留痕

- **用户确认**：2026-11-28 用户直接指派本提案实施，视为对 §9.1 已定事项与 §9.2 待确认项（字段命名、三策略、破坏性移除 `text` `target_unconfirmed`）的确认。
- **A1 旧侧唯一性**：提案只明写 fresh2 父下唯一可用输入；实现为防“旧父双栏、请求栏消失、另一栏留下”误写，额外要求旧侧原始 `Editable` 与公共有正面积 `role=input` 的 raw 索引并集唯一且即原目标。祖先容器仍含这些栏，不能靠上查恢复；全部不合格时按 A2 门禁转全树静态文本。
- **A2 输入值排除与占位证据**：按提案“排除已输入值”，可编辑输入框自身 `Text` 不充当静态锚点；仅取 `Description` 与可见、与输入框有正面积交集的直属不可拆分非编辑子文本。
- **容器身份锚**：容器重锚的身份唯一文本排除后代可编辑 `Text` 值；可读骨架仍可含该值参与 2/3 数字骨架比较与全树频次统计，二者分离。
- **零候选与歧义**：`target_ambiguous` 只用于满足筛选条件的候选 `>=2` 或双树重复模板锚点实际可见重复；一般零候选/无唯一静态锚点保留 Attempt 2 原始 `reason`，仅记录 `recovery_attempted`，不伪造歧义。
- **`recovered_ref`**：取 Attempt 2 预览中新帧的公共 `ref`；公共投影连续性可能复用与 `original_ref` 相同的字符串，故仅用于诊断，后续操作必须基于随返新 O 的树重新定位或重新 observe，不能直接作为下一次 `act` 的 ref。
- **`swipe` 边界**：统一 `phase=revalidate` details 与 `auto_recovery` 只覆盖 `tap`/`long_press`/`text`/`scroll`；raw `swipe` 的瞬态复检保持既有坐标点证明错误形状，不发布 `auto_recovery`。
- **`text_input` wire 值**：重验失败未派发时顶层 `Result.text_input.write/submit` 为协议既有值 `not_executed`；`not_attempted` 仍只属于 `error.details.dispatch` 三键。
- **滚动词典**：拒绝诊断的匹配路径与矩形/全页回退路径统一复用静态滚动候选的浮层/跨顶层窗口/同窗遮罩过滤；坐标 `point_changed` 仅在有可确认原 owner 的外部交互竞争节点时输出 `blocking_refs`。
- **`schema.go`**：按 §9.2 决定保持工具说明与目录指纹不变，`auto_recovery` 由 `result v4` 行与 `docs/` 表达。
- **独立实现审查响应**：审查发现的 6 类问题均已修正并加反控：A1 禁用/折叠/未公开旧同胞、A2 可编辑正文与占位几何、容器身份锚、滚动遮挡过滤、坐标 blocker 过度输出、重锚后已聚焦 fast path（改用 fresh2 作锚点基准，仍校验原 TaskID/前台，`submit_ref` 沿用原 O 计划）；`TestActRebind`、`TestActRecovery`、`TestActRevalidate` 等相关定向用例通过。
- **未在实机构造**：`text_rebind` 与 `container_rebind` 的成功路径仅由单测覆盖；实机三种 d04 漂移均直接重验通过，未触发恢复标记；实机恢复证据为 `transient_retry`（见验证记录 §5）。这是如实缺口，不是已实测结论。
- **Issue #43**：实现与归档推送后回复并关闭；闭环结果补记于验证记录与后续维护提交。

---

## 附：明确不改变的事项

1. **派发后 `text` 阶段契约不变**：`dispatchText` 内部的 `focus`/`write`/`verify`/`submit` 错误码（含 `409 text_submit_not_executed`、`submit_target_unconfirmed`、`submit_context_unconfirmed`）及部分成功 `partial` 语义保持不变。
2. **既有 9 个 `targetChangedReason*` 常数集合不变**，不扩造异构错误理由名。
3. **单一权威、单向消费 `O` 与 `STOP` 不等待原则不变**：不在 `httpapi`/`mcp`/`client` 保留状态副本，也不为自动恢复延长已撤销工作。
4. **`eventlog` 零文本/零 `ref` 白名单不变**。

---

## 附录：后续优化议程（静态拒绝自动恢复 / GitHub Issue #52）

- **前置证据与现状**：GitHub Issue #52 记录了在 `scroll(direction=down)` 静态准入时，外层横向可滚动容器 `n31` 与内部同界（coextensive）纵向可滚动列表 `n37` 因 [`resolveScrollRefPlan`](../../../internal/engine/scroll.go#L1026-L1029) 与 [`tryUniqueInnerScrollFallback`](../../../internal/engine/scroll.go#L1388-L1453) 第 `1406` 行 `!isCoextensiveRect(wantRect, descRect)` 守卫被拦截并返回 `422 unsupported_node_action`（`reason=axis_mismatch`）。
- **后续议程内容**：待本提案合并发布并关闭 Issue #43 后，单独发起“静态准入拒绝自动恢复”专项讨论与提案，系统研究 `422 axis_mismatch`（同延异向内嵌滚动子容器）、`422 invalid_target`、`422 no_safe_trajectory` 等在未认领 `O` 的 `phase=admission` 拒绝场景是否、在何种确定性守卫下允许静态解析改绑或交由调用方重发，以及是否统一复用 `Result.auto_recovery`。
