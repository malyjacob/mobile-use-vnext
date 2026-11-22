# 提案：统一树连续性多基线、抗重构目标重验与文本受控提交

状态：已实现，0.29.0 / 契约 25 / Android versionCode 31 已覆盖部署，双入口探针与虚拟屏回归已执行。实机证据边界见 §11.2。
实现提交：`758f2e59e2f925814ce867decf90932b728d4d5e`。
基线提交：`9245067183fede385d04ddc832de5d5514efed94`（分支 `master`，`2026-11-23`）。
适用范围：`internal/observation` 保号连续性、引擎有界多基线 delta 选择与留存保护、`act` 的 ref/坐标/裸 `swipe` 实时目标重验与 `target_changed` 归因、`act(text)` 的 `auto_submit`/`submit_ref` 接入、写后重绑定与提交重验、`outcome="partial"` 事实分层、现场观察交付及 MCP/客户端/探针契约。
影响范围：`internal/observation`（`match.go`、`build.go` 及测试）、`internal/engine`（`engine.go`、`capture.go`、`delivery.go`、`observe.go`、`operations.go`、`act.go`、`act_revalidation_policy.go`、`act_target_shared.go`、`point.go`、`scroll.go`、`tap_scope.go`、`text_stage.go`、`text_recovery.go`、`ui_execution.go`、`recovery.go`、`retirement.go`、`cleanup.go`、`start.go` 及测试）、`internal/protocol`（`requests.go`、`encode.go`、`responses.go`、`decode_response.go`、`errors.go` 及测试）、`internal/eventlog`（`log.go` 及测试）、`internal/httpapi`（测试）、`internal/client`（测试）、`internal/mcp`（`schema.go`、`result_text.go`、`server.go` 及测试）、`cmd/device-service`（`main.go`）、`scripts/mcp-probe`（`checks.go`、`result.go`、`result_v4.go`、`result_semantics.go`、`probe_test.go` 等）、受影响的正式文档（`docs/architecture.md`、`docs/interface.md`、`docs/mcp.md`、`docs/client.md`、`docs/control-protocol.md`、`docs/integration.md`、`docs/phone-operations.md`、`docs/release.md`、`docs/runbook.md`、`README.md`）；实施发布时涉及 `release/versions.json` 及版本生成物、手机整组构建/清单/覆盖部署、主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 及 `maintenance/verification/`。固定 `u2.jar` 与 Android 控制 App 业务逻辑不变。
契约（`/api/*` 字段与 `contract_version`）：改变（破坏性升级）——
1. 增量基线从单一基线升级为有界多基线池，**全部基线的 delta 跨度统一收紧为 `revision - base.Revision <= 4`**（`base` 可指向近期任一已确认 `full`）；
2. `act` 拒绝码 `409 target_changed` 的 `error.details` 新增可选 `changed_ref`（归因到具体变动的公开祖先）；Ref 重验新增 Tier 4 抗祖先重建匹配，坐标/裸 `swipe` 解除 19 字段严格 `rawIdentity` 绑定；
3. `act(text)` 请求侧废除 `operation.press_enter`，替换为 presence-aware 布尔 `auto_submit`（默认 `true`）与字符串 `submit_ref`（默认 `"enter_key"` 或公开节点 `n<digits>`，`auto_submit=false` 时宽容忽略提交语义）；
4. 全局 `outcome` 新增第 4 个封闭值 **`partial`**（仅表达 `write=executed && submit=not_executed` 且零未知物理副作用，活动会话当场交付新 `ready O`）；
5. 复合 `text` 的 `text_input`、错误详情 `stage`/`dispatch` 与原因枚举由 `enter` 统一更名为 `submit`，新增错误码 `409 text_submit_not_executed`。
共同契约拟从 `24` 升级为 `25`（配套建议 `release_version="0.29.0"`、`android_version_code=31`）；本提案落盘不修改版本源。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[match.go](../../../internal/observation/match.go)、[build.go](../../../internal/observation/build.go)、[capture.go](../../../internal/engine/capture.go)、[act_revalidation_policy.go](../../../internal/engine/act_revalidation_policy.go)、[act.go](../../../internal/engine/act.go)、[point.go](../../../internal/engine/point.go)、[tap_scope.go](../../../internal/engine/tap_scope.go)、[text_stage.go](../../../internal/engine/text_stage.go)、[text_recovery.go](../../../internal/engine/text_recovery.go)、[ui_execution.go](../../../internal/engine/ui_execution.go)、[recovery.go](../../../internal/engine/recovery.go)、[retirement.go](../../../internal/engine/retirement.go)、[requests.go](../../../internal/protocol/requests.go)、[responses.go](../../../internal/protocol/responses.go)、[schema.go](../../../internal/mcp/schema.go)、[result_text.go](../../../internal/mcp/result_text.go)、[versions.json](../../../release/versions.json)）。

---

## 0. 摘要

本提案统一根治远程 Issue **#29**（静止列表行整片重编号空转 delta、卡片页连续退 `full`、跨页 `BACK` 全换号）、**#36**（祖先根重建/兄弟重排导致未变 ref 目标误拒 `lineage_changed`、视频进度秒跳导致裸 `swipe` 连续 `point_changed`）与 **#30**（`text` 写入已确认但因搜索框缩框导致 ENTER 目标未确认、误标 `uncertain` 并丢弃现场观察）。三类故障的共同根源是系统内四套割裂的节点/树比较机制把 Android 视图树的易变实现噪声（模板 `resource-id`、远端祖先重建、被动文案跳动、写后清除按钮引发的水平微缩）当成了本质身份门禁。

本提案采取**一次性破坏性契约升级（`24 → 25`）正本清源**：
1. **观察连续性与多基线池（#29）**：泛化后代投票至所有未决容器（含模板 `resource-id`/重复 `label`）并支持自底向上跨重包裹锚定；补充静止屏同父非输入叶子的紧几何收尾消歧；引入有界确认 `full` 基线池（全部基线统一 `revision - base.Revision <= 4` 且受淘汰保护可供 `inspect` 回看），让 `A → B → BACK 回 A` 直接命中轻量 delta。
2. **抗重构目标重验（#36）**：Ref 目标新增受控 **Tier 4**（自身结构兼容 + 强自身/子树语义身份 + 双向紧几何锁定 + 全树双向唯一 + 后续防遮挡落点复验），真实因祖先拒绝时附 `changed_ref`；坐标与裸 `swipe` 告别 19 字段全等 `rawIdentity`——裸 `swipe` 改做结构与防新遮挡证明（忽略被动叶子文案跳动），坐标点击/输入保留自身文案严格一致而祖先与几何对齐 Ref 体系。
3. **文本受控提交与 `partial` 事实分层（#30）**：以 `auto_submit`（默认 `true`）+ `submit_ref`（默认 `"enter_key"` 或节点 `ref`，`auto_submit=false` 时宽容忽略提交）取代 `press_enter`；`enter_key` 遇 `candidate_count=0` 自动做单次写后 `Capture` 重绑定再 live 复核；节点 `submit_ref` 复用完整 `tap` 规划、放行写前灰置按钮、支持软键盘垂直平移匹配与至多 1 次 150ms 复采；`text_input`/`stage`/`dispatch` 统一为 `submit`；引入 **`outcome="partial"`** 与 `409 text_submit_not_executed`，在写确认而提交确定未派发时当场交付新 `ready O`。

明确不做：不改 `act` 后观察 `settle` 的 19 字段收敛判据、不改固定 `u2.jar` 与 Android 控制 App 业务逻辑、不新增调用方宽松重试参数、不在 delta 语法中引入 `alias`/`rename`。

---

## 1. 问题

### 1.1 现象与代价

分诊结论：**取舍（架构规范升级）**。涉及的三类现象均有现行规范与单元测试背书（保守保号排除带资源容器与静态几何、单基线与 `< 12` 窗口、Tier 1–3 全祖先等值、坐标 19 字段 `rawIdentity`、写后沿用旧 bounds 的 `GetTextOnDisplay` 与 3 态 `outcome`），属于规范本身过于保守且各模块标准割裂，须经统一提案推进契约升级，不得在实现中暗改语义。

三个关联 Issue 的可观测现场代价：

1. **Issue #29（观察保号漂移、空转 delta 与连续退 `full`）**：
   - 知乎搜索首页静止状态下连续观察，屏幕内容与坐标逐字节不变，delta 却报 **`changes=86`**：17 个带模板 `resource-id`（`search_find`）的可点击行被整片换号，尽管它们内部的文本子节点（`n2139`/`n2140` 等）保持原 ref 不变。
   - 哔哩哔哩搜索结果页连续 3 次同页观察均退回 `full`，delta 机制整体失效。
   - 知乎视频搜索页 `A（69 节点，O_85）→ 点进播放器 B（5 节点）→ BACK 回 A（O_90）→ 再 BACK（O_92）`，3 次观测同一未变化列表页，每次整树换号并重发约 69 行 `full`（合计 ~210 行），无法复用 A 页编号与上下文。
2. **Issue #36（未变目标误拒 `target_changed`）**：
   - 网易云音乐播放列表对 `n4097` 执行 `long_press`：仅因播放页根容器重建（`n4064 → n3989`）与目标 2px 几何微抖，返回 `409 target_changed`（`outcome=not_executed`），原样用交付新 O 重试即成功。
   - 知乎点赞出现 Toast 后紧接着点评论按钮 `n453`，以及知乎回答底栏点“赞同”移除兄弟容器 `n820` 后紧接着点“收藏” `n828`：`n828` 自身完全未出现在随后交付的 delta 变更集中（bounds 与 label 逐字节相同），仍被 `409 target_changed (reason=lineage_changed)` 拦下；且 `details` 未指明是哪一层祖先变化。
   - 知乎内置视频播放器（仅 5 个节点）连续两次裸 `swipe`，页面结构与几何完全稳定，唯一变化是进度文本 `n1486` 每秒跳动（`11.0 → 26.0 → 46.0`），两次均被 `409 target_changed (reason=point_changed)` 拒杀。
3. **Issue #30（`text` 写入已确认但 ENTER 目标未确认，降级 `uncertain` 且强制额外观察）**：
   - 网易云音乐搜索框已 `focused=true`，调用 `act(text, "周杰伦")`（默认 `press_enter=true`）：`SetTextOnDisplay` 写入并精确读回确认 `write=executed`，但紧接着返回 `409 text_target_uncertain`、`outcome=uncertain`、`text_input write=executed enter=not_executed`、`details={"stage":"enter","reason":"enter_target_unconfirmed","candidate_count":0,"dispatch":{"focus":"not_attempted","write":"dispatched","enter":"not_attempted"}}`、`recovery entry=observe ref=P... input=retired`。
   - 调用方被迫多花一整轮 210 节点的全量 `observe` 只为确认“字已写入、未回车”并拿到搜索按钮/联想列表的 ref；且无机制在已知页面提交按钮时直接一次性写字并点击提交。

### 1.2 证据链

按四条独立代码通路逐一核对基线 `9245067183fede385d04ddc832de5d5514efed94`：

#### 通路一：观察保号连续性与单基线模型（#29）
| 断言 | 代码落点 |
| --- | --- |
| 无文案但可点击的节点因 `needsResourceHint` 发布 `resource`，导致 `publicSignature` 置 `hasRes=true`，进而 `hasPublicIdentity(pk)==true` | [build.go:452-457](../../../internal/observation/build.go#L452-L457)、[match.go:144-164](../../../internal/observation/match.go#L144-L164)、[match.go:1740-1742](../../../internal/observation/match.go#L1740-L1742) |
| `isStructurallyMatchable` 要求 `!hasPublicIdentity(pk) && k.label == ""`，导致带模板 `resource-id`（如 17 个 `search_find` 行）的容器被 `curAnonymousContainer`/`prevAnonymousContainer` 排除 | [match.go:438-457](../../../internal/observation/match.go#L438-L457)、[match.go:488-490](../../../internal/observation/match.go#L488-L490) |
| Stage 2 桶匹配因 17 行签名完全相同（`len != 1`）全部跳过；Stage 3（`matchContainersByDescendants`）与 Stage 5（`matchOrdinalContainersZeroShift`）又因 `curAnonymousContainer==false` 全部跳过，尽管其文本子节点已在 Stage 2 全部保号 | [match.go:1006-1038](../../../internal/observation/match.go#L1006-L1038)、[match.go:1163](../../../internal/observation/match.go#L1163)、[match.go:1412](../../../internal/observation/match.go#L1412)、[match.go:1579-1595](../../../internal/observation/match.go#L1579-L1595) |
| Stage 3 强制要求 `parentOf(cur)` 必须已先锚定（`st.matchedOld(st.idx.parentOf(cur))`）；任何靠近根部的容器失锚或包装层变动都会阻断下方全部容器的后代投票与 Stage 4 局部恢复，级联换号导致 delta 超过 70% 退化为 `full` | [match.go:1415-1418](../../../internal/observation/match.go#L1415-L1418)、[capture.go:267](../../../internal/engine/capture.go#L267) |
| 全流水线无静止几何收尾阶段，同父下两个位置分离且纹丝未动的相同按钮/图标每帧必重发新号（由既有保守测试钉死） | [match.go:22-23](../../../internal/observation/match.go#L22-L23)、[continuity_test.go:167-201](../../../internal/observation/continuity_test.go#L167-L201)、[continuity_pipeline_test.go:494-511](../../../internal/observation/continuity_pipeline_test.go#L494-L511) |
| 引擎仅保留单一 `e.baseline` 与单一 `Builder.prev`，窗口固定为 `revision-baseline.Revision < 12` 与 `180s`；离开 A 页进入 B 页再 `BACK` 回 A 页时，B 页已覆盖 `prev` 与 `baseline`，必然整树换号并重发 `full` | [engine.go:383](../../../internal/engine/engine.go#L383)、[capture.go:251-273](../../../internal/engine/capture.go#L251-L273)、[capture.go:775-783](../../../internal/engine/capture.go#L775-L783)、[build.go:43-49](../../../internal/observation/build.go#L43-L49) |
| `inspect` 本就支持按 ID 读取 `e.observations` 中任意仍留存的历史观察（含 `retired`/`claimed`/`expired`），且 `planRetentionLocked`/`pruneObservationsLocked` 已有 `protected` 集合保护基线不被淘汰 | [inspect.go:88-103](../../../internal/engine/inspect.go#L88-L103)、[capture.go:374-385](../../../internal/engine/capture.go#L374-L385)、[capture.go:742-753](../../../internal/engine/capture.go#L742-L753) |

#### 通路二：Ref 目标重验与 `lineage_changed`（#36 正文 + 补充 A）
| 断言 | 代码落点 |
| --- | --- |
| `matchActTargetWithLimits` 仅有 Tier 1–3，显式无 Tier 4；每个 Tier 的 `edgeAtTier` 都必须通过 `sameLineageKeysBudgeted(newLineage, oldLineage)` 要求祖先链长度与每个祖先的 `structureKey` 逐字段全等 | [act_revalidation_policy.go:493-507](../../../internal/engine/act_revalidation_policy.go#L493-L507)、[act_revalidation_policy.go:557-581](../../../internal/engine/act_revalidation_policy.go#L557-L581)、[act_revalidation_policy.go:701-751](../../../internal/engine/act_revalidation_policy.go#L701-L751) |
| Tier 3 的 `lineageL2` 始终保留 raw 根节点（`entry.root`），且 `transparentWrapper` 只剥离文本/描述/资源全显式为空且全部交互与状态标志均显式为默认值的包装层；任意带 `resource-id`、交互标志或未上报属性的祖先重建、或根容器重建（`n4064→n3989`）与父级兄弟重排导致祖先属性变化时，Tier 1–3 全部返回 `lineageMismatch=true` | [act_revalidation_policy.go:170-266](../../../internal/engine/act_revalidation_policy.go#L170-L266) |
| 拒绝出口 `probeTargetChangedError` 仅写 `reason=lineage_changed`，无 `changed_ref` 定位具体变化祖先；紧接着 `deliverRejectedObservation` 投影同一帧却常证明目标自身未出现在 delta 变更集内 | [act_target_shared.go:83-91](../../../internal/engine/act_target_shared.go#L83-L91)、[act.go:121-132](../../../internal/engine/act.go#L121-L132)、[delivery.go:25-71](../../../internal/engine/delivery.go#L25-L71) |

#### 通路三：坐标与裸 `swipe` 重验（#36 补充 B）
| 断言 | 代码落点 |
| --- | --- |
| `provePointTarget` 与 `matchFreshPointSet` 不区分裸 `swipe` 与坐标点击/输入，统一用 19 字段全等 `identityOf`（含 `Bounds`、`Text`、`Description`、`Focused`、`Selected`）和完整原始祖先链 `lineageOf` 要求无序最深命中集合逐字段相等 | [act.go:1320-1443](../../../internal/engine/act.go#L1320-L1443)、[act.go:2247-2274](../../../internal/engine/act.go#L2247-L2274)、[identity.go:8-34](../../../internal/engine/identity.go#L8-L34) |
| 视频播放器中只要命中节点或其祖先存在秒跳进度文本（如 `n1486` `"11.0"→"26.0"`）、1px 动画或焦点翻转，`matchFreshPointSet` 立即失配返回 `409 target_changed (reason=point_changed)` | [act.go:1422-1437](../../../internal/engine/act.go#L1422-L1437)、[act.go:1445-1447](../../../internal/engine/act.go#L1445-L1447) |

#### 通路四：`act(text)` 写后 ENTER 确认与复合事实分层（#30）
| 断言 | 代码落点 |
| --- | --- |
| `SetTextOnDisplay` 前按写前 `bound.raw` 冻结 `target`（含精确 `Bounds` 与 `Description`）；固定 `u2.jar` 的 `DisplayBoundTextService` 对 `getTextOnDisplay` 要求 `focused`/`editable`/`visible`/`enabled`/`!password` 且 `package`/`resourceId`/`class`/`description`/`bounds.toShortString()` 全部精确相等 | [act.go:1618](../../../internal/engine/act.go#L1618)、[act.go:2236-2242](../../../internal/engine/act.go#L2236-L2242)、[text.go:14-36](../../../internal/android/text.go#L14-L36)、[u2 README:80-83](../../../third_party/uiautomator/README.md#L80-L83) |
| `SetTextOnDisplay` 成功确认 `write=executed` 后，搜索框内联出现 `×` 清除按钮或软键盘顶起导致 `EditText.Bounds` 变化，`dispatchTextEnter` 复用旧 `target` 调 `GetTextOnDisplay` 必然得到 `candidate_count=0`，且无重绑定回退，直接返回 `OutcomeUncertain` + `enter_target_unconfirmed` | [act.go:1700-1709](../../../internal/engine/act.go#L1700-L1709)、[act.go:1850-1859](../../../internal/engine/act.go#L1850-L1859) |
| `textOverallOutcome` 只要未全部完成且任一原语已派发就一律判 `OutcomeUncertain`；`Act` 收到 `OutcomeUncertain` 后执行 `markDispatchUnknown(work)`、**跳过后观察**、按 `recovery.go:100-108` 返回 `entry=observe, ref=P, input=retired` | [text_stage.go:123-140](../../../internal/engine/text_stage.go#L123-L140)、[act.go:146-159](../../../internal/engine/act.go#L146-L159)、[recovery.go:100-108](../../../internal/engine/recovery.go#L100-L108) |
| 请求参数仅支持 `press_enter`（无 `submit_ref`），`text_input`、错误详情 `stage`/`dispatch`、原语回退 [ui_execution.go:172-185](../../../internal/engine/ui_execution.go#L172-L185)、冻结逻辑 [retirement.go:247-282](../../../internal/engine/retirement.go#L247-L282)、日志白名单 [log.go:101-102](../../../internal/eventlog/log.go#L101-L102)、MCP 文本与恢复解析 [result_text.go:529-545, 1735, 1773](../../../internal/mcp/result_text.go#L529-L545) 及探针 [result.go:378-393](../../../scripts/mcp-probe/result.go#L378-L393)、[result_semantics.go:627, 659](../../../scripts/mcp-probe/result_semantics.go#L627) 均硬编码为 `enter` 与 3 态 `outcome` | [requests.go:130-177](../../../internal/protocol/requests.go#L130-L177)、[responses.go:670-725](../../../internal/protocol/responses.go#L670-L725)、[decode_response.go:466-492](../../../internal/protocol/decode_response.go#L466-L492)、[schema.go:349-407](../../../internal/mcp/schema.go#L349-L407) |

### 1.3 为什么直觉会出错

- **“在 delta 里加 `alias nB = nA` 语法来掩盖换号”**：会破坏 delta 极简 5 块原子最终图模型，把编号混乱的复杂度甩给所有调用方与客户端重建器；从保号算法根源把带模板 `resource-id` 的容器和静态分离叶子保住，delta 天然就是 `changes=0`，零新增语法。
- **“Stage 6 紧几何也允许匹配列表卡片容器或无锚点输入框”**：当列表整批换内容但复用相同槽位坐标（如 `A,B → C,D`，对应既有回归 `all_content_replaced_with_no_fixed_point_gets_fresh_refs`）时，若用几何锚定了卡片容器，会顺带让新卡片里的 `"赞"` 按钮偷走旧卡片按钮的 ref；两个无锚点且值全改的 `EditText` 同样不可按几何串号。因此 Stage 6 必须严格限定为**非输入、无公开子节点的叶子**。
- **“彻底取消 Ref 重验的祖先校验，只要同文案就点”**：列表里有 10 张卡片都含 `"收藏"` 或页面瞬间弹出系统确认框带 `"确定"` 时，取消祖先校验而不加“紧几何 + 全树双向唯一 + 自身/子树强语义 + 防遮挡落点”四重硬闸，会立刻引发严重误点。
- **“把裸 `swipe` 的坐标证明直接全删，只查前台”**：一旦页面在观察后瞬间弹出全屏广告或交互对话框，只查前台（同包同 TaskID 内的弹窗！）会在新弹窗上盲滑；按结构拓扑与防新遮挡证明，既能免疫进度秒跳，又能拦住弹窗劫持。
- **“修改固定 `u2.jar` 放宽 `getTextOnDisplay` 匹配”**：引入固定二进制制品重编、签名/哈希切换与设备端兼容风险；在 Go 引擎侧于 `candidate_count=0` 时做一次受控 `Capture` 重绑定再用新 `Bounds` 调现有 `GetTextOnDisplay` 刷新确认，零改动固定 JAR 且复用完整的 `u2.jar` live 刷新语义。
- **“`press_enter` 与 `submit_ref` 做互斥校验，或给 `submit_ref` 加自动回车回退”**：LLM 极易自动填入 schema 默认值，字段互斥会制造大量无意义 `422 invalid_request`；而点击按钮失败后静默补按回车存在重复提交/重复下单风险。用 `auto_submit` 总开关 + `submit_ref`（默认 `"enter_key"`）并在 `auto_submit=false` 时宽容忽略提交语义，单次至多一种提交，最安全也最顺手。
- **“把 `write=executed, enter=not_executed` 标成 `outcome=executed`”**：会让只判 `outcome=="executed"` 或 MCP `isError==false` 的调用方误以为搜索/表单已提交；独立新增 `outcome="partial"` 并使 MCP `isError=true`（附带 `text_input` 与最新现场观察），事实分层最诚实。

---

## 2. 目标与非目标

### 目标

- **G1（#29 同页保号稳定）**：带模板 `resource-id`/重复 `label` 的列表行容器在后代匹配时稳定保号；靠近根部发生包装层变动时不触发子树级联换号；静止屏同已锚定父下空间分离且未移动的非输入同名叶子稳定保号，同时守住“整批换内容复用槽位不偷号”与“双无锚点输入框不串号”不变式。静止页面不再产生空转 delta 或退化 `full`。
- **G2（#29 有界多基线回返）**：服务端维护有界确认 `full` 基线池，**全部基线统一收紧为 `revision - base.Revision <= 4`（且 `< 180s`）**；活动基线受留存淘汰保护可供 `inspect` 回看；跨页 `A → B → BACK 回 A` 在窗口内自动命中历史基线输出轻量 delta（如 `changes=0`），不复活同页已消失元素，未胜出试探不污染 ref 高水位。
- **G3（#36 Ref 抗祖先重建与归因）**：Ref 重验新增受控 **Tier 4**，在祖先根重建、Toast 增删或父级兄弟重组但目标自身与几何稳定且全树双向唯一时一次成功；真因祖先变化拒绝时在 `error.details` 附精确 `changed_ref`（可确定时）。
- **G4（#36 坐标与裸 `swipe` 分层证明）**：裸 `swipe` 在进度文本/被动展示叶子跳动或远端壳重建时一次成功，遇新浮层/弹窗/切页仍拦截 `point_changed`；坐标 `tap`/`long_press`/`text` 保留目标自身文案与能力严格一致，祖先与几何对齐 Ref 体系。
- **G5（#30 `act(text)` 高成功率提交与 `partial` 事实分层）**：
  - 请求侧以 `auto_submit`（默认 `true`）+ `submit_ref`（默认 `"enter_key"` 或公开节点 ref，`auto_submit=false` 时宽容忽略）取代 `press_enter`；
  - `enter_key` 路径遇 `candidate_count=0` 自动触发单次写后 `Capture` 重绑定（容忍清除按钮水平内缩与键盘垂直位移）并经 `GetTextOnDisplay` 确认后派发回车；
  - 节点 `submit_ref` 静态复用完整 `tap` 规划器（等效 `bubble=true`，同时支持 ref `text` 与坐标 `text`）并放行写前 `enabled=false` 按钮，写后新鲜重验支持 Tier 1–4 + 软键盘垂直平移（`sameHorizontalBand`）+ 至多 1 次 150ms 异步复采；
  - 响应与错误详情统一为 `submit` 命名（`text_input:{write,submit}`、`stage=submit`、`dispatch:{focus,write,submit}`）；新增 `outcome="partial"` 与 `409 text_submit_not_executed`，在写确认而提交确定未派发且原会话活动时当场交付新 `ready O`（首次返回在无实时阻塞时省略 `recovery`，重放/`status(P)` 与未交付场景稳定投射 `observe(P, retired)` 且经完整实时 live overlay 覆盖）。
- **G6（契约升级与整组交付）**：共同契约由 `24` 升级为 `25`（建议 `0.29.0` / `versionCode=31`），同步更新 `protocol`/`client`/`mcp`/`mcp-probe`/正式文档，完成全量单测、独立文档审计、手机整组覆盖部署、最小实机闭环、主机 `device-mcp.exe`/`mcp-probe.exe` 替换与双入口探针验证，并关闭远程 Issue `#29`、`#30`、`#36`。

### 非目标

- 不改变 `act`/`start`/`app.*` 的后观察 `settle` 判据（仍为 19 字段 L1+L2+L3）；不改变 `wait_for` 内容稳定判据。
- 不修改固定 `u2.jar`、不修改 Android 控制 App 业务逻辑（仅同步生成的 `contract_version`/`versionName`/`versionCode`）。
- 不在 delta 语法中新增 `alias`/`rename` 块，不在 full 帧中新增 `renumbered` 字段。
- 不新增调用方可控的“宽松重试开关”参数（如 `relaxed`），不在 `text` 失败时做启发式猜测搜索按钮的自动点击。
- 不改变 O 单向消费、STOP 不等待、锁内短小、恢复屏障、日志白名单、应用生命周期与受管 shell 语义。
- 不改动未涉及的远程 Issue（如 #26/#32/#34/#37/#38）。依赖 Agent 宿主进程重启与配置由用户处理。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 仅补报错提示与文档规避 | 不改保号、重验与提交判据，只在错误体补说明或文档建议改用 `scroll`/手动分两步输入 | 零契约变动 | #29 仍空转 86 条/连续 full；#36 仍首轮拒杀；#30 搜索框仍必报 `uncertain` 并多耗一轮全量 `observe` |
| R2 兼容式小补丁 | 仅放宽 Stage 3 匿名限制、保持单基线与 `< 12` 窗口、保持 `press_enter` 并与 `submit_ref` 互斥、保留 3 态 `outcome` 仅附带观察 | 契约改动较小，兼容旧请求字段 | 跨页 BACK 仍每帧重发 69 行 full；LLM 填默认值撞互斥 `422`；`write=executed, submit=not_executed` 仍混在 `uncertain` 里误导决策；`text_input.enter` 装按钮点击语义别扭 |
| R3 激进放宽（去祖先/去坐标证明/改 `u2.jar`） | 取消 Ref 祖先与坐标证明、强制用最近坐标保号、重编固定 `u2.jar` | 改固定制品与签名链；安全边界塌陷 | 列表滚换后误偷 ref、弹窗劫持时静默误点/盲滑、写后失焦盲发回车触发误操作 |
| **R4 统一架构根治 + 破坏性契约升级（选定）** | 按“本质身份 / 局部安全边界 / 易变噪声”三层模型重构保号+多基线池（`<=4`）、Ref Tier 4+坐标/裸 `swipe` 分层证明、`auto_submit`/`submit_ref`+`outcome="partial"`+现场交付 | 一次破坏性契约升级（`24 → 25`），需全链路同步 `protocol`/`client`/`mcp`/`probe`/文档与单测 | 极端长距离动画（如按钮水平+垂直同时大幅移位）、动态写后才凭空创建的按钮或无任何强语义锚点的纯空壳重构仍需依赖交付的新 `O` 再操作（见 §7.2） |

选择 **R4**：三个 Issue 均直指同一套过严比较体系；在一次破坏性升级里把多基线、抗重构证明与 `auto_submit`/`submit_ref`/`partial` 一次设计到位，既彻底消除反复出现的多轮浪费与误报，又避免留下兼容别名与半吊子命名债务，同时用四重硬闸与双向唯一守住防误点/防错号底线。

---

## 4. 判据

本节所有面积/距离计算统一使用 `int64` 防溢出，矩形均为半开区间 `[Left, Right)`、`[Top, Bottom)`。

### 4.1 观察保号连续性与有界多基线池（#29）

#### 1. 泛化容器身份兼容与自底向上后代投票（Stage 3）
定义两容器的自身签名兼容 `sameContainerIdentity(prevK, curK matchKey, prevPK, curPK publicKey) bool`：
- 任一侧为 `editable` 输入框 → `false`（输入框由内容/输入锚点通道处理）；
- 两侧均满足 `isStructurallyMatchable(k, pk)`（匿名无 label 无已发布 resource）→ `sameShape(prevK, curK)`；
- 否则（存在 `resource`、`label` 或 `protected`）→ 仅当 `sameUnit(prevK, curK)` 或（两侧均为非匿名公开身份且 `samePublicUnit(prevPK, curPK)`，且若 `actions != 0` 且无 `label` 时 `hasRes` 与 `resource` 必须一致）成立。

Stage 3（`matchContainersByDescendants`）不再要求 `curAnonymousContainer`/`prevAnonymousContainer`；任何未匹配且有公开子节点（`len(children) > 0`）、非合成根、非 `editable` 的容器均参与投票，分两层在**同一快照 `baseR`** 下收集提案（仍经 `chooseBatchIfBidirectionallyUnique` 统一提交并计入既有 `workBudget`）：
- **3A（同锚定父优先）**：若 `parentOf(cur)` 已锚定到 `anchor`，且 `rc`（`cur` 子树内已匹配后代对，非空）的所有旧位置均落入 `anchor` 下同一个直接未匹配子容器 `expectedPrev`，满足 `sameContainerIdentity` 且 `samePairSet(rc, rp)` → 提出 `(expectedPrev, cur)`。
- **3B（自底向上跨重包裹锚定）**：若 `parentOf(cur)` 尚未锚定且 `len(rc) >= 1`：
  1. 在旧树中沿 `prevParent` 求 `rc` 全部旧位置的最近公共祖先 `lca`（若 `lca == prevRoot` 或 `lca < 0` 跳过）；
  2. 沿 `lca` 向上（止于 `prevRoot` 之前）收集所有未匹配且满足 `samePairSet(rc, rp)` 的旧容器 `prevChain`；沿 `cur` 向上（止于 `root` 之前，遇已匹配父停）收集所有未匹配且其子树已匹配后代集合仍恰好等于 `rc` 的当前容器 `curChain`；
  3. 仅当在 `(prevChain, curChain)` 中满足 `sameContainerIdentity(p, cur)` 的旧候选**恰有 1 个**（`prevCand`），且 `curChain` 中满足 `sameContainerIdentity(prevCand, c)` 的当前候选**恰有 1 个**且正是 `cur` 时，提出 `(prevCand, cur)`。
  4. 多层完全同签名的嵌套单链壳（候选 ≥ 2）不在此步猜测，留待锚定扩展后的 Stage 4 局部恢复或外层锚定收敛。

Stage 5（`matchOrdinalContainersZeroShift`）同步解除“仅限匿名容器”的限制：按 `containerOrdinalBucket(k, pk)`（含 `role`/`actions`/`protected`/`hasRes`/`resource`/`label`，可交互时含 `class`/`pkg`）对同锚定父下的未匹配非输入容器分组，仍严格要求旧新同数量 `k >= 2` 且桶内已有 ≥ 1 个来自非 ordinal 阶段的同 rank 独立固定点。

#### 2. 静止屏同父非输入同名叶子紧几何收尾（Stage 6）
在 Stage 1–5 与 post-ordinal 局部恢复全部完成后，若 `budget` 未耗尽，执行**单次** `matchStaticGeometrySiblings`（不回触发 Stage 3/5 或局部容器恢复）：
- 遍历所有已锚定配对的父节点 `(anchor, parentCur)`；
- **硬范围限定**：仅纳入两侧未匹配、**非 `context`、非 `editable`（排除输入框）、无公开子节点（`len(children) == 0`，仅限叶子，绝不匹配容器）**且具有正面积屏幕内 `Bounds` 的直接公开子节点；
  - 由此严格守住两项既有防串号不变式：① 列表整批换内容复用槽位坐标时，卡片容器绝不会借几何锚定并导致内部按钮偷号（[continuity_pipeline_test.go:599-625](../../../internal/observation/continuity_pipeline_test.go#L599-L625)）；② 同父双无锚点输入框值变化时绝不串号（[continuity_pipeline_test.go:80-101](../../../internal/observation/continuity_pipeline_test.go#L80-L101)）。
- 按 `staticLeafBucket(k, pk)`（`role`, `actions`, `protected`, `hasRes`, `resource`, `label`；`actions != 0` 时追加 `class`, `pkg`）分组；
- **硬条件（全部 AND）**：
  1. 同桶旧新未匹配数量相等且 `k >= 2`（数量不等立即跳过，保护增删歧义测试 `TestContinuityNewDuplicateDoesNotStealRef`）；
  2. 定义 `sameStaticBounds(a, b)`：四边差值均 `<= 4` 像素（`|Left|<=4 && |Top|<=4 && |Right|<=4 && |Bottom|<=4`）；
  3. 桶内每个 `oldPos` 在桶内 `cur` 中满足 `sameStaticBounds` 的候选**恰有 1 个**，且每个 `cur` 也恰被 **1 个** `oldPos` 命中（严格 1-to-1 双射；若两兄弟本身重叠在同一矩形则互相多命中，自动放弃）；
  4. 按旧侧 `prevPos` 升序排列的配对，其对应当前侧 `tin[cur]` 必须严格单调递增（保序，防换位）。
- 通过 `chooseBatchIfBidirectionallyUnique` 批量提交（因只处理叶子，不再产生新的 dirty 容器）。

#### 3. 有界确认 `full` 多基线池（`revision - base.Revision <= 4`）
- **配置常量**：
  - `maxBaselineRevisionSpan = 4`（取代原 `< 12`；适用**全部**基线，包括最新主基线与历史回返基线）；
  - `maxBaselineAge = 180 * time.Second`（不变）；
  - `maxBaselines = 4`（池中同时保留的已确认 `full` 条目硬上限）；
  - 主基线 delta 体积阈值保持 `10 * deltaBytes < 7 * fullBytes`（`< 70%`）；
  - 历史基线回返试探的 delta 体积阈值更严格：`10 * deltaBytes < 5 * fullBytes`（`< 50%`），避免两个仅偶然局部相似的不同页面强行走历史 delta。
- **入池与确认**：
  - 唯一确认入口不变：仅被接纳的 `observe(O)`、`wait_for(O)` 或 `act` 认领的 `obs.Full == true` 观察可调用 `confirmBaselineLocked(obs)` 入池；未确认的 `full`（如刚由 `start` 或某次 `act` 后观察产出但尚未被调用方引用的 `full`）绝不入池。
  - 入池时保留 `ObservationID`、`Origin`、`Revision`、`CapturedAt`、`Tree`（compact `ReadTree`）与 `Projection`（含 `Tree`、`Capture`、`Source`、`ContextSource`）。
  - 每次提交新观察推进 `session.Revision` 或入池前，按 `Origin == session.Origin && nextRev - b.Revision <= 4 && now - b.CapturedAt < 180s` 清理过期条目，并按 `Revision` 降序截断至 `maxBaselines`。
- **留存淘汰保护与 `inspect` 保证**：
  - `planRetentionLocked` 与 `pruneObservationsLocked` 先按当前窗口清理过期基线，再将池中全部剩余活动基线的 `ObservationID` 纳入 `protected` 集合；从而凡可作为 `delta.base` 发出的 O 必在 `e.observations` 中保留完整树，宿主要回看时调用 `inspect(reference=baseID)` 必可读。
- **基线选择与 `activeBaselineID` 规则**：
  - `e.activeBaselineID` 记录当前 `b.prev` 所对齐的基线 ID：确认某 `full` O 且 `obs == e.current` 时设为 `obs.ID`；发布基于 `B` 的 delta 时设为 `B`；发布 `full` 时置空（直到新 `full` 被确认）；结束/重建会话时清空池与 `activeBaselineID`。
  - **Step 1（主路径）**：使用基于 `b.prev` 的默认候选 `tree`。当 `!forceFull` 且活动基线（优先 `activeBaselineID`，空时取池中最新有效基线）在窗口内（`nextRev - b.Revision <= 4 && age < 180s`），计算 `ReadDiff`；若成功且 `< 70% fullBytes` → 直接胜出，**绝不进入 Step 2**。
  - **Step 2（历史回返试探）**：仅在 `!forceFull`、非 `act` 重验失败交付（`allowPoolFallback=true`）且 Step 1 未产出有效 delta 时运行。遍历池中其余仍在窗口内的确认 `full`（按 `Revision` 降序，至多 3 条）：
    - 调用 `Projector.CandidateAgainst(b.Projection, &capture)` 生成无副作用只读候选，构建 `compact` 并校验 `MaxFullTreeBytes` 与 `MaxSnapshotCostBytes`；
    - 调用 `ReadDiff(b.ObservationID, b.Tree, candCompact)`，仅接受满足 `10 * deltaBytes < 5 * candCompactBytes` 的候选；
    - 选 `deltaBytes` 最小者（字节相同选 `Revision` 更大者）作为本次唯一待提交候选。
  - **Step 3（兜底 `full`）**：两步均未命中时，沿用 Step 1 基于 `b.prev` 的 `tree` 发布 `full`，维持同页最大局部连续性。

### 4.2 Ref 目标重验 Tier 4 与 `changed_ref` 归因（#36 正文 + 补充 A）

保留 Tier 1–3 判据不变。仅在 `matchActTargetWithLimits` 中 Tier 1–3 全部返回 `actProbeNone` 且 `sawLineageMismatch == true` 时，进入 **Tier 4（抗祖先重建证明）**：

1. **前置门禁**：`oldNode.DisplayID == newNode.DisplayID` 且 `sameStructure(oldNode, newNode)` 兼容（包名、类名、文案、描述、资源 ID、可编辑与全部能力/勾选/启用/密码三态无冲突）。
2. **双向紧几何锁定 `tightGeometryCompatible(a, b, screen)`**：
   - `rectValidOnScreen(a, screen) && rectValidOnScreen(b, screen)`；
   - `inter := a.Intersect(b)` 非空，且**双向面积覆盖均 ≥ 80%**：`area(inter)*100 >= area(a)*80 && area(inter)*100 >= area(b)*80`（防止大容器覆盖小按钮被单向误判）；
   - 尺寸差 `|aw-bw| <= max(8, max(aw,bw)/5)` 且 `|ah-bh| <= max(8, max(ah,bh)/5)`；
   - 中心位移 `|ax-bx| <= max(16, max(aw,bw)/4)` 且 `|ay-by| <= max(16, max(ah,bh)/4)`。
3. **强自身或子树语义身份 `tier4SemanticEvidence`（三选一，排除无锚点纯空壳）**：
   - **E1（自身可读文案）**：`knownUnprotected(oldNode, newNode)` 且非空 `Text` 或非空 `Description` 精确相等；
   - **E2（全局唯一资源 ID）**：非空 `ResourceID` 且同屏同包下在 `original` 与 `fresh` 全树中计数均为 `1`；
   - **E3（容器子树稳定可读内容指纹）**：仅当节点自身无非空文案且有子节点时，在有界 DFS（每侧最多访问 64 个后代节点，计入 `matchBudget`）内收集所有可见、非受保护后代的非空 `Text` 与 `Description`（按先序组成多集合）：
     - 若原节点是 `tap`/`long_press`/`text` 目标（如网易云歌单行 `n4097`）：要求原侧与新侧收集到的非空可读文案列表**非空且按字符串排序后完全相等**；
     - 若原节点是 `scroll` 容器：要求满足 E2，或两侧非空可读文案集合非空且至少有 1 项重合（或两侧均具备相同非空 `ResourceID` 且几何双向覆盖 ≥ 85%）。
4. **全树双向唯一**：
   - 正向扫描 `fresh.Nodes`：恰有 1 个候选通过 (1)+(2)+(3)；≥ 2 个立即返回 `actProbeAmbiguous`；
   - 反向扫描 `original.Nodes`：恰有 1 个原节点通过 (1)+(2)+(3) 且就是 `wantIndex`；≥ 2 个立即返回 `actProbeAmbiguous`。
5. **后续安全复验不变**：命中 Tier 4 后仍须通过 `Preview(fresh)`、能力校验（`validTapPublicTarget` / `isScrollContainer` + `sameScrollScope`）、`collectTapScope` 与 `provesSubtreeTapPoint`/`provesSegment`；对于 `bubble`/`coextensive`/`colocated` 双绑定，仍分别重验两节点及新鲜帧关系守卫（如 `isTopLevelCoextensiveBlocker`、`nearestInteractiveAncestor`、`colocatedMatchGuard`）。
6. **`changed_ref` 归因（仅 `409 target_changed` 且 `reason="lineage_changed"` 时可选附带）**：
   - 当 `probeActNode` 返回 `lineage_changed` 时，按原目标在 `obs.Projection.Tree` 中的公开祖先链自近及远查找**第一个**与新鲜帧失配的公开祖先（查找原公开祖先对应的 raw 索引时须同时兼容 **`obs.Projection.Source`** 与 **`obs.Projection.ContextSource`**，因公开祖先可为 `context` 节点）；若能确定唯一非空且非 `root` 的公开祖先 ref，写入 `error.details["changed_ref"]`；若 `bubble_ancestor` 因最近交互祖先变更拒绝，写原 `ancestorPublic.Ref`（无法唯一定位则省略 `changed_ref`，绝不编造）。

### 4.3 坐标目标与裸 `swipe` 重验（#36 补充 B）

`provePointTarget`（[act.go:1320-1383](../../../internal/engine/act.go#L1320-L1383)）保留前置有界拓扑校验（`validateTapRawTopology(fresh)`）、`work.Projector.Preview(fresh)` 与 `resolvePointSet(freshTree.Root, x, y)`，按 `operation.Type` 分两条明确判据（不再调用 19 字段 `identityOf` + 完整 `lineageOf`）；两条路径的集合匹配均使用**有界二分图最大匹配（Kuhn 增广路，顶点数 `N <= maxPointHitSet = 64`）**检验是否存在大小为 `N` 的完备 1-to-1 匹配，避免同一坐标上两个相同被动装饰叶子因存在对称边而被逐边度=1 策略误拒：

1. **裸 `swipe`（`operation.Type == "swipe"`）——区域结构与防新遮挡证明**：
   - 要求 `len(freshNodes) == len(observedIndices)` 且所有 `freshNodes` 均映射至互异有效 raw 索引（任何新增更深层弹窗、对话框、浮层遮住 `from=(x,y)` 均会改变最深命中集合长度或节点结构，立即拦截 `point_changed`）。
   - 定义单边兼容 `swipePointNodeCompatible(oldRaw, newRaw *device.RawNode, x, y int, screen device.Rect) bool`：
     - `oldRaw.DisplayID == newRaw.DisplayID`、`oldRaw.Package == newRaw.Package`、`oldRaw.ClassName == newRaw.ClassName`、`oldRaw.Editable == newRaw.Editable`；
     - `compareOptional(old.Resource, new.Resource) != triMismatch`；
     - `Clickable`、`LongClickable`、`Scrollable`、`Enabled`、`Password`、`Checkable` 均 `compareTri != triMismatch`；
     - **文本与描述策略**：若任一侧具备动作能力（`Clickable || LongClickable || Scrollable || Editable`），要求 `Text` 与 `Description` 无冲突；若两侧均为**无动作被动节点**（四项交互能力均非 true，如视频播放页进度文本 `n1486`），**明确忽略 `Text` 与 `Description` 变化**；
     - **忽略项**：`Focused`、`Selected`、`Checked` 与祖先链包装层差异；
     - **几何防劫持**：`newRaw.Bounds` 在屏内有效、包含 `(x, y)`，且与 `oldRaw.Bounds` 满足 `sizeCompatible` 及几何覆盖（`overlapRatio >= 0.60` 且两矩形面积比不超过 4 倍，防止小装饰节点被全屏透明遮罩替换）。
   - 在 `(observedIndices, freshIndices)` 二分图上用 `swipePointNodeCompatible` 建边并求最大匹配；匹配数 `< len(observedIndices)` 时返回 `409 target_changed (reason=point_changed)`。

2. **坐标 `tap` / `long_press` / `text`——目标语义严格 + 抗祖先重建证明**：
   - 同样要求 `len(freshNodes) == len(observedIndices)` 且所有 `freshNodes` 映射有效互异；
   - 定义单边兼容 `pointTargetNodeCompatible(original, oldIdx, fresh, newIdx, x, y, screen, budget)`：
     - **严格保留目标自身全部结构身份**：`oldRaw.DisplayID == newRaw.DisplayID` 且 `sameStructure(oldRaw, newRaw)`（严格比对 `Text`、`Description`、`ResourceID` 及全部能力与勾选/启用/密码标志，**绝不忽略点击/输入目标自身文案**）；
     - `newRaw.Bounds` 在屏内有效且仍包含 `(x, y)`；
     - **两级匹配**：
       - 优先级 A：几何兼容（`overlap >= 0.50`）且 `sameRawLineageKeys(L1 或 L2)` 成立；
       - 优先级 B（抗祖先重建回退）：满足 `tightGeometryCompatible(oldBounds, newBounds, screen)`（双向 ≥ 80% 覆盖）且满足强语义证据（`E1` 自身非空文案 / `E2` 唯一资源 / `E3` 非空子树文案指纹）；
   - 在 `(observedIndices, freshIndices)` 二分图上求最大匹配，要求存在大小为 `len(observedIndices)` 的完备匹配；且对 `freshNodes` 重新执行 `collectPointOwners` + `filterMatchingPointOwners`，确保新帧在 `(x, y)` 上**未引入新的竞争交互拥有者**（仍满足 ≤1 个拥有者或恰 1 个匹配本次动作的拥有者）。失配返回 `409 target_changed (reason=point_changed)`（`text` 经 `decorateTextRevalidateError` 映射为 `stage=target, reason=target_unconfirmed`）。

### 4.4 `act(text)` 的 `auto_submit`、`submit_ref`、`outcome="partial"` 与写后提交判据（#30）

#### 1. 请求字段与宽容接入规则
- 废除 `operation.press_enter`（任何操作类型携带均按 `protocol.Invalid` 返回 **`422 invalid_request`**）。
- 仅 `operation.type == "text"` 接受以下两个可选字段（其他分支携带按 `protocol.Invalid` 返回 **`422 invalid_request`**）：
  | 字段 | 类型 | 缺省值 | 严格语法校验（不合法返回 `422 invalid_request`） |
  | --- | --- | --- | --- |
  | `auto_submit` | `boolean` | `true` | 不接受 `null` 或非布尔值 |
  | `submit_ref` | `string` | `"enter_key"` | 不接受 `null`、非字符串、空串或超 64 字符；必须等于 `"enter_key"` 或满足公开节点编号语法 `^n[1-9][0-9]*$` |
- **宽容逻辑**：
  - `EffectiveAutoSubmit() == false`（显式 `auto_submit=false`）：在通过上述类型与语法校验后，**完全忽略 `submit_ref`**（不查找节点、不静态规划、不执行提交、响应省略 `text_input`）；
  - `EffectiveAutoSubmit() == true`（省略或显式 `true`）：
    - `EffectiveSubmitRef() == "enter_key"` → 回车提交路径；
    - `EffectiveSubmitRef() == "n..."` → 点击提交按钮路径。
  - 单次 `text` 调用至多执行一种提交原语，绝不在按钮提交失败后自动补发 `ENTER`（防重复提交）。

#### 2. `submit_ref` 节点静态规划（认领前）
仅当 `EffectiveAutoSubmit() && op.IsSubmitTap()` 运行，**同时适用于 ref `text` 与坐标 `text`**（在 `staticActContentCheckWithTap` 统一汇合，零设备 I/O、失败不认领 `O`）：
- 复用 [tap_scope.go](../../../internal/engine/tap_scope.go) 的完整 `tap` 静态规划体系（**等效 `bubble=true`**，支持直接 4 级安全点 + `coextensive_child` + `bubble_ancestor` + `bubble_colocated`），并通过显式配置项 `allowInitiallyDisabled=true`（绝不改变普通 `tap`/`long_press` 行为）作以下专门扩展：
  1. **放行写前灰置按钮（`enabled=false`）**：`allowInitiallyDisabled` 须穿透 `tap_scope.go` 全部四处禁用门禁——`staticTapRefCheck`（[L2439](../../../internal/engine/tap_scope.go#L2439)）、`planBubbleOrColocated` 步 A（[L2139](../../../internal/engine/tap_scope.go#L2139)）、`validTapPublicTarget`（[L927](../../../internal/engine/tap_scope.go#L927)）与 `isTopLevelCoextensiveBlocker`（[L1198](../../../internal/engine/tap_scope.go#L1198)）；静态阶段不报 `409 node_disabled`，正常完成几何、动作能力与安全落点规划，并在 `submitPlan` 上记录 `initiallyDisabled`（写前实际执行目标是否显式 `enabled=false`）；
  2. **静态错误归因清晰**：`submit_ref` 自身不存在报 `409 node_not_found`，结构/能力/无安全点沿用既有 5 码 `422` 体系（`invalid_target`/`unsupported_node_action`/`no_safe_point` + `candidates`），并在 `details` 中保留准确 `scope_ref`（指向 `submit_ref` 或其祖先）。

#### 3. 写后提交确认判据（`write=executed` 提交之后）
- **路径 A：`submit_ref == "enter_key"`**：
  1. 只读校验 `sameTextTaskContext(obs.Foreground, foreground)`；
  2. **快路径**：用写前 `target` 调 `GetTextOnDisplay`；若 `readErr == nil && CandidateCount == 1 && Value != nil && *Value == operation.Text` → 通过；
  3. **单次写后重绑定回退**：当快路径无传输错误且 `CandidateCount == 0` 时，执行一次 `e.dev.Capture(work.Ctx, session.Display)` 得 `afterWrite`：
     - 校验 `sameTextTaskContext(obs.Foreground, afterWrite.Foreground)`；
     - 在 `afterWrite.Nodes` 中筛选满足全部安全条件（`DisplayID` 一致、`Visible && Editable && HasFocused && Focused && HasEnabled && Enabled && HasPassword && !Password`、`Package == bound.raw.Package`、`ClassName == bound.raw.ClassName`、`TextSet && Text == operation.Text`）且满足以下**双向唯一锚点**与几何条件之一的候选：
       - 锚点匹配（写前帧 `boundCapture` 与 `afterWrite` 同屏同包同 class 可编辑节点均须唯一）：原 `bound.raw` 有 `ResourceID` 时要求写前帧与 `afterWrite` 中同 `ResourceID` 计数均为 `1`（允许 `Description` 随文本更新）；无 `ResourceID` 但有 `Description` 时要求两帧同 `Description` 计数均为 `1`；无锚点时要求 `afterWrite` 全局仅 1 个满足安全条件且 `Text == operation.Text` 的候选；
       - 几何兼容（三选一）：
         - `sameHorizontalBand(oldBounds, newBounds)`（键盘顶起垂直平移）；
         - `sameVerticalBandResize(oldBounds, newBounds)`（搜索框右侧出现 `×`/搜索按钮导致水平内缩或微扩：`|old.Top-new.Top| <= tolY && |old.Bottom-new.Bottom| <= tolY && |old.Height()-new.Height()| <= max(8, maxH/5)`，其中 `tolY = max(8, maxH/5)`，且水平交集宽 `>= min(old.Width(), new.Width()) / 2`）；
         - `overlapRatio(oldBounds, newBounds) >= 0.50`；
     - 仅当候选**恰有 1 个**时，用新节点构造 `freshTarget := textTargetOf(rebound, session.Display)`，再调一次 `GetTextOnDisplay(work.Ctx, freshTarget)` 确认 `CandidateCount == 1 && Value != nil && *Value == operation.Text` → 通过，进入唯一一次 `Key("ENTER")` 派发门禁！
     - 任一步未通过 → 保留 `afterWrite` 供现场交付，安全中止提交（`submit=not_executed, dispatch.submit=not_attempted`），归因 `submit_context_unconfirmed` 或 `submit_target_unconfirmed`（附实际 `candidate_count`）。

- **路径 B：`submit_ref == "n..."`（节点点击提交）**：
  1. 采新鲜 `Capture`（`afterWrite`），校验 `sameTextTaskContext(obs.Foreground, afterWrite.Foreground)`；
  2. 对 `submitPlan`（涵盖直接目标、`coextensive_child`、`bubble_ancestor`、`bubble_colocated` 全部四类模式及其 `targetRaw`/`bubbleAncestorRaw`/`colocatedRaw`/`delegateRaw` 绑定节点）执行写后提交重验：
     - **节点匹配**：先运行含 Tier 4 的 `probeActNode`（其中 `sameStructureForSubmit` 对 `submitPlan` 涉及的执行目标节点允许 `Enabled` 从 `false → true` 单向跃迁，绝不允许 `true → false` 或其他能力/文案冲突）；
     - **软键盘垂直平移回退**：若上述任一绑定节点仅因 Y 轴大幅平移返回 `actProbeNone`（且原节点具备强自身或子树语义身份 E1/E2/E3），执行有界 `matchKeyboardShiftedSubmitNode`：要求 `DisplayID` 一致、`sameStructureForSubmit`、`sameHorizontalBand(oldBounds, newBounds)`（左右边界与宽高在既有容差内，仅 Y 轴平移）且**全树双向唯一**；
     - **新鲜启用状态与落点**：经 `Preview(afterWrite)` + `buildTapPublicView` 校验新鲜执行目标的启用门禁与能力：
       - 若 `submitPlan.initiallyDisabled == true`（写前显式禁用）：新鲜帧**必须显式启用**（`freshRaw.HasEnabled && freshRaw.Enabled` 且 `targetPublic.Enabled != nil && *targetPublic.Enabled`），确保确实观测到了禁用→启用跃迁；
       - 否则：沿用既有 `validTapPublicTarget` 口径（`targetPublic.Enabled == nil || *targetPublic.Enabled`）；
       - 校验 `tap` 能力、双绑定关系守卫成立，并经 `collectTapScope` + `provesSubtreeTapPoint` 算得安全落点 `(sx, sy)`（优先复用未位移安全点，否则按 4 级候选重新在新鲜帧选点）；
  3. **单次异步短复采**：仅当首次写后帧发现目标**仍处于 `enabled=false`**（App 尚在异步启用按钮）或因键盘过渡尚未落稳而未找到匹配/安全点（非上下文切换、非撤权、非拓扑损坏），在可中断等待 **`150ms`** 后做**至多 1 次** `Capture` 重跑步骤 1–2；
  4. 通过后进入 `beginTextSubmit` → `beginUIDispatchStage("tap", "submit")` → 发送至多 1 次 `Tap(displayID, sx, sy)`；
  5. 未通过 → 保留最后一次合法 `afterWrite`，安全中止提交（`submit=not_executed, dispatch.submit=not_attempted`），按确切原因归因：`submit_context_unconfirmed` / `submit_disabled` / `submit_scope_blocked` / `submit_target_unconfirmed`。

#### 4. `outcome="partial"`、命名统一与恢复投影矩阵
- **`outcome` 四值定义（仅复合 `text` 产生 `partial`）**：
  - `write == executed && (!autoSubmit || submit == executed)` → **`executed`**
  - `write == executed && autoSubmit && submit == not_executed && dispatch.submit ∈ {not_attempted, not_dispatched}` → **`partial`**（无任何未知物理副作用）
  - 全部原语均未曾申请派发或均确定未派发（`focus/write/submit` 均为 `not_attempted`/`not_dispatched`）→ **`not_executed`**
  - 其余情形（焦点已发而写未确认、写回执未知/读回不符、或提交已申请派发许可且结果未知 `submit == uncertain`）→ **`uncertain`**
- **公开结构更名**：
  - `TextInputView`：`{"write":"<state>","submit":"<state>"}`（JSON 键与 `result v4` 文本 `text_input write=<w> submit=<s>` 同步更名；合法 5 态顺序不变：`submit != not_executed` 必须 `write == executed`）；
  - `error.details`：`stage` 枚举 `target/focus/write/verify/submit`；`dispatch` 三键 `{focus, write, submit}`；
  - 新增封闭错误码：**`text_submit_not_executed`**（HTTP `409`），用于非撤销下的 `outcome="partial"`；
  - `submit` 阶段封闭 `reason` 枚举：`submit_target_unconfirmed`、`submit_context_unconfirmed`、`submit_disabled`、`submit_scope_blocked`、`submit_not_sent`、`submit_unconfirmed`（以及冻结时的 `authorization_revoked`、`deadline_exceeded`）。
- **`partial` 的 `op.Recovery` 持久化与实时投影规则（统一首次、重放与冻结）**：
  - `planRecoveryFor` 对已认领 `text` 且 `Outcome == OutcomePartial` **始终持久化基础语义恢复 `observe(P, retired)`**（写入 `op.Recovery`，绝不存 `nil`），保证 `applyLiveTextRecoveryLocked`（[text_recovery.go:34-37](../../../internal/engine/text_recovery.go#L34-L37)）永不因 `base == nil` 早退，Priority 1–6 的实时会话/耐久/设备/忙覆盖对首次响应与重放一律生效；
  - 仅在 `applyLiveTextRecoveryLocked` 走到 **Priority 7（会话健康且无任何实时阻塞）** 时：若 `op.Outcome == OutcomePartial` 且本次返回视图**确实附带了已发布的有效 `ready O`**（`result.Observation != nil && result.Observation.ID != "" && result.Observation.Status != "failed"`），将本次返回副本的 `result.Recovery` 置为 `nil`（省略）；
  - 其余所有情形——包括首次响应后观察失败（无有效 `Observation.ID`）、重复 `P`、`status(P)` 与冻结重放（`result.Observation == nil`）——在 Priority 7 均自然输出 `observe(P, retired)`，并由 MCP/Probe 的 `resolveObserveRecoveryP`/`resolveStatusRecoveryP` 按 `outcome == "partial"` 解析为具体 `operation_id`。

  | 情形 | `outcome` | 顶层错误码 / HTTP | `observation` | 首次 `recovery` | 重复 P / `status(P)` `recovery` |
  | --- | --- | --- | --- | --- | --- |
  | 写确认、提交未派发、会话活动且成功发布新 `ready O` | `partial` | `text_submit_not_executed` / `409` | **附带新 `ready O`**（`ID != ""`） | **`nil`（省略）**（若并发出现 Priority 1–6 阻塞则按实时规则覆盖） | `observe(P, retired)`（受实时 live overlay 覆盖） |
  | 写确认、提交未派发、会话活动但现场观察发布失败 | `partial` | `text_submit_not_executed` / `409` | `status=failed`（或无 `ID` 的只读图 `partial`） | `observe(P, retired)`（受实时 live overlay 覆盖） | 同左 |
  | 写确认、提交未派发，期间 STOP 撤权或工作期限冻结 | `partial` | 撤权用 `control_revoked` / `409`；仅工作期限到期用 `text_submit_not_executed` / `409` | 无（不发布） | 经 `applyLiveTextRecoveryLocked` 投影（撤权为 `end`/`status`，仅超时活动会话为 `observe(P, retired)`） | 同左 |
  | 写确认、提交已派发但回执未知（`submit=uncertain`） | `uncertain` | `action_result_uncertain` / `504` | 无 | `observe(P, retired)`（受 live overlay 覆盖） | 同左 |

---

## 5. 算法

### 5.1 观察保号与多基线选择流程

```text
matchRefs(prevSnap, cap, units):
  Stage 1: matchUniqueContainers (top-down from root)
  Stage 2: matchContentPasses (6 local/global passes)
  Loop until fixed point or budget exhausted:
    Stage 3A: matchContainersByDescendants_AnchoredParent (any non-editable container, sameContainerIdentity)
    Stage 3B: matchContainersByDescendants_BottomUp (unanchored parent -> LCA -> unique chain pair)
    Stage 4:  matchLocalUnderAnchoredParents(dirty, postOrdinal=false)
  Stage 5: matchOrdinalContainersZeroShift (generalized non-editable container bucket + independent fixed point)
  Post-5:  matchLocalUnderAnchoredParents(dirty, postOrdinal=true)
  Stage 6: matchStaticGeometrySiblings (anchored parent, non-context, !editable, LEAF ONLY, equal count>=2, ±4px bijection, order-preserving)
  Allocate fresh monotonic refs in public preorder for remaining unmatched nodes

publishTreeAttempt(work, session, phase, defaultTree, allowPoolFallback, forceFull, ...):
  [short lock] verify session active & work authorized
               nextRev = session.Revision + 1
               prune stale entries from e.baselines (>4 rev or >=180s or wrong origin)
               snapshot poolSnapshot = copy(e.baselines), activeID = e.activeBaselineID
  [unlock]
  chosenTree = defaultTree
  chosenDelta = nil
  if !forceFull && len(poolSnapshot) > 0:
    // Step 1: primary path against activeBaseline (or newest in pool)
    primary = find(poolSnapshot, activeID) ?? poolSnapshot[0]
    if d := tryDiff(primary, defaultTree.compact); d != nil && 10*len(d) < 7*len(defaultTree.compactBytes):
      chosenDelta = d
    else if allowPoolFallback:
      // Step 2: historical page-return probe (strictly < 50% full, pick smallest delta)
      bestBytes = +Inf
      for b in poolSnapshot where b != primary:
        if workRevoked(work) || work.Ctx.Err() != nil: return stopped
        candTree, err := projectCaptureAgainst(work, b.Projection, defaultTree.capture)
        if err != nil: continue // over budget or invalid -> skip candidate safely
        if d := tryDiff(b, candTree.compact); d != nil && 10*len(d) < 5*len(candTree.compactBytes):
          if len(d) < bestBytes:
            bestBytes, chosenTree, chosenDelta = len(d), candTree, d
  [short lock] re-verify session, epoch, work, display generation, capture age
               prune e.baselines for nextRev
               planRetentionLocked(chosenTree.cost) with all active baselines protected
               apply victims, chosenTree.commit()
               if chosenDelta != nil: e.activeBaselineID = chosenDelta.Base
               else:                  e.activeBaselineID = ""
               publish obs & return
```

**多基线跨页回返时间轴示例**：

```text
rev=85  O_85 (Page A, 69 nodes) -> full (n1359..n1448)
        act(O_85, tap) -> confirmBaseline(O_85); pool=[O_85(85)], active=O_85
rev=86  O_86 (Page B, 5 nodes)  -> Step 1 >70%, Step 2 empty -> full (n1449..n1453); active=""
        act(O_86, BACK) -> confirmBaseline(O_86); pool=[O_86(86), O_85(85)], active=O_86
rev=87  O_87 (Page A, 69 nodes) -> Step 1 vs O_86 fails -> Step 2 probes O_85 (87-85=2<=4)
                                -> matches n1359..n1448, changes=0 (<50%) -> COMMIT!
                                -> delta base=_85 changes=0; prev=O_87; active=O_85
rev=88  O_88 (Page A scrolled)  -> Step 1 uses active=O_85 (88-85=3<=4) -> small delta base=_85
rev=90  O_90 (Page A later)     -> 90-85=5 > 4 -> O_85 pruned -> full (new anchor)
```

- **异常/撤销/预算行为**：
  - `matchRefs` 预算耗尽：未完成批次整体丢弃，安全降级为单调新号，绝不产生部分冲突配对。
  - Step 2 试探期间遭遇 STOP/取消：立即返回 `409 control_revoked`，零提交、`b.prev` 与 `b.nextRef` 零污染。
  - Step 2 某基线候选超出 `MaxFullTreeBytes`/`MaxSnapshotCostBytes`：仅跳过该历史基线，不影响兜底返回 Step 1 结果。
  - `delivery.go` 重验失败交付传入 `allowPoolFallback=false` 且仅在 `obs.Full` 时尝试以该 `obs` 为基线。

### 5.2 Ref 重验 Tier 1–4 与坐标/裸 `swipe` 重验

```text
matchActTargetWithLimits(ctx, revoked, original, fresh, want, limits):
  for tier = 1..3:
    probe = tierMatcher(tier).probeWant(wantIdx)
    if Unique -> return match(tier)
    if Ambiguous / Cancelled / BudgetExhausted / Malformed -> return immediately
    if None && probe.lineageMismatch -> sawLineageMismatch = true
  if sawLineageMismatch:
    probe4 = tierMatcher(4).probeWantTier4(wantIdx, isScroll)
    // Tier 4: sameStructure + tightGeometry(bidir>=80%) + evidence(E1|E2|E3) + global bidirectional unique
    if Unique -> return match(tier=4)
    if Ambiguous / Cancelled / BudgetExhausted / Malformed -> return immediately
  return None (lineageMismatch=sawLineageMismatch)
```

- **预算与取消**：Tier 4 沿用同一 `matchBudget`（每次候选访问、E3 子树 DFS 至多 64 节点均记账，每步检查 `ctx.Err()` 与 `revoked()`）；预算耗尽/畸形树返回 `503 hierarchy_unavailable`，STOP 优先返回 `409 control_revoked` 且不交付新 O。
- **`changed_ref` 计算**：仅在最终决定返回 `409 target_changed (reason=lineage_changed)` 时在锁外纯 CPU 跑一次有界祖先核对（最多遍历公开祖先深度 ≤ 64，同时兼容 `Source` 与 `ContextSource`），失败或不确定则省略 `changed_ref`，永不 panic 或覆盖主错误。

### 5.3 `act(text)` 复合写入与受控提交流程

```text
dispatchText(work, op, session, obs, req, fresh, focusFrame, submitPlan):
  1. Target/Focus/Write/Verify (existing strict path; unchanged safety gates)
     -> commitTextWrite(work, op): write=executed (if !autoSubmit -> KnownOutcome=executed, return OutcomeExecuted)
  2. If !autoSubmit: return OutcomeExecuted, nil
  3. Enter stage="submit":
     if submitRef == "enter_key":
       check foreground task -> fail => return partial(submit_context_unconfirmed, nilCapture)
       res = GetTextOnDisplay(oldTarget)
       if res ok && count==1 && *res.Value == req.Text: goto DispatchEnter
       if res err != nil || count != 0:
         return partial(submit_target_unconfirmed, count, nilCapture)
       // Fallback rebind once on count==0
       afterWrite = Capture(display) -> fail => return partial(submit_target_unconfirmed, nil)
       if !sameTextTaskContext(obs.Foreground, afterWrite.Foreground):
         return partial(submit_context_unconfirmed, afterWrite)
       rebound, ok = rebindPostWriteTextTarget(boundCapture, bound.raw, afterWrite, req.Text)
       if !ok: return partial(submit_target_unconfirmed, count=0, afterWrite)
       res2 = GetTextOnDisplay(textTargetOf(rebound))
       if res2 err != nil || res2.CandidateCount != 1 || res2.Value == nil || *res2.Value != req.Text:
         return partial(submit_target_unconfirmed, res2.CandidateCount, afterWrite)
     DispatchEnter:
       beginTextSubmit -> beginUIDispatchStage("key", "submit") -> Key(display, "ENTER")
       not_dispatched => return partial(submit_not_sent, afterWrite)
       terminated && err==nil => confirmTextSubmit -> return OutcomeExecuted
       else => commitTextReceipt(uncertain) -> return OutcomeUncertain(action_result_uncertain, submit_unconfirmed)

     else (submitRef == "n..."):
       for attempt = 0..1:
         if attempt == 1:
           if !pauseInterruptible(work.Ctx, 150ms):
             return partialOrStopped(work, op)
         afterWrite = Capture(display) -> fail => return partial(submit_target_unconfirmed, nil)
         if !sameTextTaskContext(obs.Foreground, afterWrite.Foreground):
           return partial(submit_context_unconfirmed, afterWrite)
         sf, reason, retryable := revalidateTextSubmitPlan(work, obs, afterWrite, submitPlan)
         if sf != nil: goto DispatchSubmitTap(sf)
         if !retryable || attempt == 1:
           return partial(reason, afterWrite)
     DispatchSubmitTap(sf):
       beginTextSubmit -> beginUIDispatchStage("tap", "submit") -> Tap(display, sf.x, sf.y)
       not_dispatched => return partial(submit_not_sent, afterWrite)
       terminated && err==nil => confirmTextSubmit -> return OutcomeExecuted
       else => commitTextReceipt(uncertain) -> return OutcomeUncertain(action_result_uncertain, submit_unconfirmed)

deliverTextPartialObservation(work, session, partialCapture, wantImage):
  // Reuse partialCapture ONLY when !wantImage and partialCapture passes all delivery guards:
  // ForegroundPresent/ForegroundNone, no visible control UI on virtual display, capture age <= CaptureMaxAge.
  if !wantImage && partialCaptureValidForDelivery(partialCapture, session, cfg):
    tree, err := e.projectCapture(work, "act.partial", *partialCapture)
    if err == nil:
      quality := &protocol.Quality{State: QualityUnknown, Samples: 0, StopReason: QualityStopNotSampled}
      public, _, pubErr := e.publishTreeAttempt(work, session, "act.partial", tree, ..., quality, allowPoolFallback=true)
      if pubErr != nil && pubErr.Code == protocol.CodeStopped: return nil, pubErr
      if pubErr == nil && public != nil && public.ID != "": return public, nil
    else if err.Code == protocol.CodeStopped:
      return nil, err
  // Otherwise fall back to standard captureAndPublish(work.Ctx, work, session, false, wantImage, "act")
  public, _, postErr := e.captureAndPublish(...)
  return public, postErr

Act() handling of OutcomePartial:
  if outcome == OutcomePartial:
    e.clearDispatchUnknown(work) // zero unknown dispatch!
    if e.workRevoked(work) || e.sessionForWork(work) == nil:
      errToSave = partialErr
      if errToSave == nil || errToSave.Code != protocol.CodeStopped:
        errToSave = e.textError(op, 409, protocol.CodeStopped, "原工作已撤销", textStageSubmit, textReasonAuthorizationRevoked, OutcomePartial, nil)
      finishOperation(op, OutcomePartial, "partial_revoked", errToSave) // no observation
      return result
    public, postErr := e.deliverTextPartialObservation(work, session, partialCapture, req.Image)
    if postErr != nil && postErr.Code == protocol.CodeStopped:
      errToSave = e.textError(op, 409, protocol.CodeStopped, "原工作已撤销", textStageSubmit, textReasonAuthorizationRevoked, OutcomePartial, nil)
      finishOperation(op, OutcomePartial, "partial_revoked", errToSave)
      return result
    // Mark delivered ONLY when a real ready O was published (ID != "" and Status != "failed")
    if postErr == nil && public != nil && public.ID != "" && public.Status != "failed":
      e.markDelivered(op)
    finishOperation(op, OutcomePartial, "partial", partialErr)
    if public != nil: result.Observation = public
    return result // defer applyLiveTextProjection omits Recovery on Priority 7 when valid ready O attached
```

**冻结与 STOP 交互**：
- `freezeWorkLocked` / `freezeTextLocked`（[retirement.go:202-282](../../../internal/engine/retirement.go#L202-L282)）调用更新后的 `textOverallOutcome(op.TextStage)`：
  - 若写入已确认且 `dispatchSubmit ∈ {not_attempted, not_dispatched}`：冻结为 `OutcomePartial`——原会话被撤销用 `409 control_revoked` + `reason=authorization_revoked`；原会话活动且仅工作期限到期用 **`409 text_submit_not_executed`** + `reason=deadline_exceeded`（不再硬编码 `CodeTextResultUnknown`），不发布观察；
  - 若 `dispatchSubmit == unknown` 或写入未确认：保持 `OutcomeUncertain`（超时用 `CodeTextResultUnknown`）；
  - `details.dispatch` 输出 `{"focus":..., "write":..., "submit":...}`；
  - 晚到回执永远不得回写已冻结 P、不发布观察、不改写 `text_input`。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/observation/match.go` | 泛化 Stage 3 `sameContainerIdentity` + 3A/3B 自底向上 LCA 锚定；泛化 Stage 5 `containerOrdinalBucket`；新增 Stage 6 `matchStaticGeometrySiblings`（仅限非 `context`、`!editable`、无公开子节点的叶子，±4px 双射+保序）；更新 `matchStats` |
| `internal/observation/build.go` | 新增 `Builder.CandidateAgainst(prev *Snapshot, cap *device.Capture) (*Snapshot, func(), error)`，纯只读试探、仅 `commit()` 更新 `b.prev`/`b.nextRef` |
| `internal/observation/*_test.go` | 补充模板 `resource-id` 列表行、根壳重建自底向上、静止分离叶子保号、重叠/滚动/增删/换内容槽位/双无锚点输入框不串号及 `CandidateAgainst` 无污染测试；同步更新 `continuity_test.go:167`（`TestContinuityAmbiguousDuplicatesGetNewRefs`）与 `continuity_pipeline_test.go:494-511`（`ambiguous_buttons_under_same_anchored_card_get_fresh_refs`） |
| `cmd/device-service/main.go` | `engineProjection` 透传 `ContextSource`；`observationProjector` 实现 `CandidateAgainst` |
| `internal/protocol/requests.go`、`encode.go` | 废除 `PressEnter`/`press_enter`（未知字段报 `422 invalid_request`）；新增 `AutoSubmit`/`HasAutoSubmit`、`SubmitRef`/`HasSubmitRef`、`SubmitRefEnterKey = "enter_key"`、严格解码与 `auto_submit=false` 宽容语义、`EffectiveAutoSubmit`/`EffectiveSubmitRef`/`IsSubmitTap` |
| `internal/protocol/responses.go`、`decode_response.go` | 新增 `OutcomePartial = "partial"` 常量；`TextInputView.Enter` 更名为 `Submit`（JSON 键 `"submit"`）；校验 5 态顺序不变 |
| `internal/protocol/errors.go` | 新增 `CodeTextSubmitNotExecuted = "text_submit_not_executed"`（HTTP `409`）；`APIError.Details` 注释白名单登记 `changed_ref` 与 `submit` 阶段枚举 |
| `internal/engine/engine.go` | 新增 `OutcomePartial = "partial"`；`Projection` 增 `ContextSource`；`Projector` 增 `CandidateAgainst`；`Engine` 以 `baselines []*baselineState` + `activeBaselineID string` 取代单一 `baseline`；`operationState.TextStagePressEnter` 改为 `TextStageAutoSubmit`；`operationViewLocked` 对 `OutcomePartial` 保留 `op.Recovery` 走统一 `applyLiveTextRecoveryLocked` |
| `internal/engine/capture.go`、`delivery.go`、`observe.go`、`operations.go`、`cleanup.go`、`start.go` | 实现多基线池确认/裁剪（`<= 4` rev、`< 180s`、容量 `4`）、全池 `protected` 淘汰保护、`publishTreeAttempt` 两步选择（主基线 `< 70%`、历史基线 `< 50%`）、`delivery.go` 固定 `allowPoolFallback=false`；更新 `start`/`cleanup` 清空池 |
| `internal/engine/act_revalidation_policy.go`、`act_target_shared.go` | 新增 Tier 4 抗祖先重建匹配（`tightGeometryCompatible` + E1/E2/E3 语义证据 + 全树双向唯一）；新增 `changed_ref` 归因计算（兼查 `Source` 与 `ContextSource`）；新增 `sameStructureForSubmit` 与 `matchKeyboardShiftedSubmitNode` |
| `internal/engine/act.go`、`point.go`、`scroll.go`、`tap_scope.go` | `provePointTarget` 重构为有界二分图匹配的裸 `swipe` 结构防遮挡证明 + 坐标点击/输入语义严格抗祖先重建证明；`staticActContentCheckWithTap` 对 ref/坐标 `text` 统一集成 `planTextSubmitRef`（`allowInitiallyDisabled` 穿透 `tap_scope.go` L927/L1198/L2139/L2439 四处禁用检查）；`dispatchText` 集成 `enter_key` 写后单次重绑定与 `submit_ref` 写后重验（覆盖直接/coextensive/bubble/colocated 全部绑定节点 + 键盘垂直平移 + 1 次 150ms 复采）；`Act` 接入 `OutcomePartial` 与 `deliverTextPartialObservation`（带完整交付守卫与 `ID != "" && Status != "failed"` 判据） |
| `internal/engine/text_stage.go`、`text_recovery.go`、`ui_execution.go`、`recovery.go`、`retirement.go` | `enter` 全量更名为 `submit`（含 `ui_execution.go:179` `clearTextPrimitive` 的 `case "submit"`）；`textOverallOutcome` 引入 `OutcomePartial`；`planRecoveryFor` 对 `OutcomePartial` 持久化 `observe(P, retired)`，由 `applyLiveTextRecoveryLocked` Priority 7 在附带有效新 `O` 时省略 `Recovery`；`freezeTextLocked` 同步 `submit` 与 `OutcomePartial` 超时错误码 `CodeTextSubmitNotExecuted` |
| `internal/engine/*_test.go` | `engine_test.go:403-430`（`testProjector` 实现 `CandidateAgainst`）；迁移直接读写 `e.baseline` 的测试（`tree_budget_test.go`、`shell_test.go`、`snap_test.go`、`commit_test.go`、`lease_test.go`、`readonly_test.go`、`observation_continuity_integration_test.go`）；迁移/重构被 Tier 4 放行的旧用例（`act_proposal_point_reasons_test.go:507-540`、`act_revalidation_test.go:431-519`）并补充无 E1/E2/E3 负例；补充 #29/#30/#36 全矩阵单测 |
| `internal/eventlog/log.go` | `enter_dispatch` 更名为 `submit_dispatch`；新增受控整数/枚举键 `tier`、`baseline_pool`（绝不记录 ref、文本或树） |
| `internal/mcp/schema.go`、`result_text.go`、`server.go` | `act` schema 废除 `press_enter`，新增 `auto_submit`/`submit_ref`（遵守分支成员不在 JSON Schema 发布 `"default"` 的规范，仅在 `description` 说明默认值）；更新 `callerGuidance` 与 `act` 描述；`result_text.go` 渲染 `text_input write=… submit=…` 与 `outcome=partial`，并在 `resolveStatusRecoveryP`（L1735）与 `resolveObserveRecoveryP`（L1773）加入 `outcome == "partial"` 与 `text_submit_not_executed`；`server.go` 保持 `act` `isError` 为 `Outcome != "executed"` |
| `scripts/mcp-probe/*` | 同步 `checks.go:384-394` 分支成员列表（`bubble`/`auto_submit`/`submit_ref` 无 JSON Schema `"default"`）；同步 `result.go`/`result_v4.go` 的 `text_input.{write,submit}` 与 `outcome=partial`；同步 `result_semantics.go:627, 659` 恢复 P 解析（纳入 `partial` 与 `text_submit_not_executed`）及 `probe_test.go` |
| `release/versions.json` 与生成物 | 实施阶段手改 `contract_version: 24 → 25`（建议 `release_version: "0.29.0"`、`android_version_code: 31`）并运行 `scripts/generate-version.ps1` |
| `docs/*`、`README.md` | 实施阶段同步更新正式文档并过独立只读文档审计 |

### 6.2 配置与取值

| 参数 / 常量 | 取值 | 依据 | 谁可修改 |
| --- | --- | --- | --- |
| `maxBaselineRevisionSpan` | `4`（含，即 `nextRev - b.Revision <= 4`） | 用户已确认；4 步覆盖高频同页与 `A→B→BACK→A`，且避免引用久远上下文 | 服务端固定常量，调用方不可设 |
| `maxBaselines` | `4` | 与 4 步滑窗天然一致；最多占 `4 × 4 MiB = 16 MiB < 32 MiB` 留存预算 | 服务端固定常量 |
| `maxBaselineAge` | `180s` | 沿用现有 delta 基线最大时间跨度 | 服务端固定常量 |
| 主基线 / 历史回返 delta 阈值 | 主 `< 70%` / 历史 `< 50%` | 历史回返提高门槛防止相似但不同页面强行差分 | 服务端固定常量 |
| Stage 6 静止几何容差 | 四边各 `<= 4px`（仅非输入叶子） | 覆盖亚像素渲染抖动，远小于列表滚动位移；排除容器与输入框防串号 | 服务端固定常量 |
| Tier 4 双向紧几何覆盖 | 双方各 `>= 80%` | 防止大容器吃掉小控件；覆盖 2px 抖动与微排 | 服务端固定常量 |
| Tier 4 E3 子树扫描上限 | 每侧至多 `64` 节点 | 保证有界 CPU 与确定性预算 | 服务端固定常量 |
| `submit_ref` 写后异步短复采 | 至多 `1` 次，`150ms` 间隔 | 覆盖 App 写后异步启用按钮/键盘布局收敛，受 STOP/Ctx 立即取消 | 服务端固定常量 |
| `submit_ref` 键盘回车字面量 | `"enter_key"` | 符合仓库小写 `snake_case` 契约风格，永不与 `^n[1-9][0-9]*$` 冲突 | 协议常量 |

### 6.3 日志与可观测性

严格遵守 AGENTS.md 硬约束 #10（日志白名单，绝不写入 ref、文本、坐标、树或异常字符串）：
- `internal/eventlog/log.go`：`enter_dispatch` 更名为 `submit_dispatch`；复用现有 `stage`、`reason`、`count`、`outcome`、`format`、`base`、`mode`、`proven`；新增受控整数/枚举键 `tier`（1–4，仅记录命中层级）与 `baseline_pool`（枚举 `primary`/`historical`/`full`），均不含任何业务内容。
- `engine.text.finished` 输出 `focus_dispatch`、`write_dispatch`、`submit_dispatch`、`outcome`、`stage`、`reason`、`count`。
- 诊断字段 `changed_ref` 只进入 HTTP/MCP 受控 `error.details`，绝不写入事件日志。

### 6.4 与既有不变式的关系

| 不变式 | 影响与保障 |
| --- | --- |
| 唯一权威（硬约束 #1） | 多基线池、`activeBaselineID`、Tier 4 重验与 `textStage` 均只由 `internal/engine` 持有；`observation` 仍为纯算法 |
| STOP 不等待（硬约束 #2） | 多基线试探、Tier 4、写后重绑定与 150ms 等待全在锁外并监听 `work.Ctx` 与 `workRevoked`，STOP 立即取消并冻结，迟到候选不提交 |
| O 单向消费（硬约束 #3） | `O` 仍严格 `ready → claimed → retired`；`partial` 交付的是新 `ready O`，旧 `O` 绝不归还；静态拒绝不认领 |
| 提交条件化（硬约束 #4） | 历史基线试探只用 `CandidateAgainst`，所有候选统一经 `publishTreeAttempt` 单一短锁提交门禁后才推进 `Builder` 连续性 |
| 状态锁短小（硬约束 #5） | 树匹配、多基线试探、Tier 4、二分图匹配、写后重绑定、`changed_ref` 均在锁外执行；锁内只做内存指针/切片操作 |
| 严格接入（硬约束 #6） | 废除 `press_enter`（未知字段 `422 invalid_request`）；`auto_submit`/`submit_ref` 拒绝 `null`/错类型/非法格式/非 `text` 分支；仅在类型语法均合法且 `auto_submit=false` 时忽略提交语义 |
| 事实分层（硬约束 #8） | 新增 `outcome="partial"` 精确分离“已确认部分完成”与“物理未知 `uncertain`”；后观察失败绝不改写已确认的 `write=executed` 或 `executed` |
| 日志白名单（硬约束 #10） | 新增/更名字段全为枚举/整数；`changed_ref`、输入文字与读回值绝不入日志 |
| 单一版本源（硬约束 #13） | 共同契约 `24 → 25` 与发行号仅在 `release/versions.json` 手改并由脚本生成 |

### 6.5 契约与文档影响

- **共同契约**：`24 → 25`（破坏性升级：多基线窗口 `<= 4`、`press_enter` → `auto_submit`/`submit_ref`、`text_input.{write,submit}`、`outcome="partial"`、`text_submit_not_executed`、`changed_ref`）。
- **需同步的正式文档**（实施提交前须通过独立只读文档审计）：
  - `docs/architecture.md`（保号 6 阶段、多基线池与 `<= 4` 窗口、Tier 4 与坐标/裸 `swipe` 重验、`text` 提交与 `partial`）
  - `docs/interface.md`（`act(text)` 请求/响应/`text_input`/`outcome` 4 值/`text_submit_not_executed`/`changed_ref`、delta 多基线窗口 `<= 4`、契约 `"25"`）
  - `docs/mcp.md`、`docs/client.md`、`docs/control-protocol.md`、`docs/integration.md`、`docs/phone-operations.md`、`docs/release.md`、`docs/runbook.md`、`README.md`（同步契约号 `"25"`、发行示例、`act` 与 `result v4` 说明）。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **#29 同页空转与连续 full**：知乎搜索首页 17 个 `search_find` 行、B 站/知乎视频网格卡片壳及其内部重复子节点稳定保号，静止页 delta 由 `changes=86` / 连续 `full` 降为 `changes=0`。
2. **#29 跨页往返**：`列表页 A → 点进详情/播放器 B → BACK 回 A`（4 个 revision 内）直接命中历史基线输出 `tree v4 delta base=<A> changes=0`（2 行替代 69~200 行 full），且原 A 页 ref 保持可用；宿主压缩上下文时可经 `inspect(baseID)` 无损回看。
3. **#36 Ref 误拒与归因**：网易云播放页根重建、知乎点赞 Toast 闪现、知乎底栏移除兄弟容器 `n820` 时的 `tap`/`long_press` 一次成功；真正结构巨变拒绝时提供 `changed_ref` 定位。
4. **#36 裸 `swipe` 与坐标误拒**：视频播放器进度文本秒跳时裸 `swipe` 一次成功；坐标点击/输入免疫无关根重建与瞬态焦点翻转。
5. **#30 搜索框写后提交与 `partial` 零浪费**：网易云等带 `×` 清除按钮的搜索框写字后经单次重绑定一次完成 `write+ENTER`；已知提交按钮时可经 `submit_ref` 一步完成填字+点击（兼容空框灰置与键盘顶起位移）；即使提交确实不能执行，也以 `outcome="partial"` + `write=executed submit=not_executed` 诚实表达并当场交付新 `ready O`，省去额外整树 `observe`。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 跨页往返超过 4 个 revision 或超过 180 秒 | 超过有界滑窗后旧基线已自动退出池，必须重新输出 `full` 防止长跨度上下文漂移 |
| 同页内两个完全重合在同一矩形（差 ≤ 4px）且签名完全相同的兄弟叶子，或无唯一内容锚点的空容器列表 | 空间或内容不可区分，为防误偷号仍保守分配新号 |
| 目标自身无任何文案、无唯一 `resource-id`、子树也无任何可读文案，且其祖先链发生了重建 | 缺乏任何可证身份证据（E1/E2/E3 均不满足），Tier 4 拒绝盲猜，仍报 `target_changed` 并交付新 `O` |
| 写字前页面尚未创建的动态提交按钮（调用前根本没有对应 `ref`） | `submit_ref` 只能引用当前 `O` 已存在的节点；此类场景可显式 `auto_submit=false` 或利用默认 `enter_key`（若无法回车则返回 `partial` 并当场交付含新按钮的 `O`） |
| 写入后输入框被 App 主动改写/格式化文本（如自动加空格、截断、前缀替换）或主动失焦且非 `submit_ref` 路径 | 读回精确相等与聚焦安全门禁不可放宽；前者维持 `readback_mismatch`/`uncertain`，后者安全停在 `partial` 并交付现场 `O` |
| `submit_ref` 在软键盘弹出时同时发生大幅水平位移与垂直位移（超出 `sameHorizontalBand`）且无重叠 | 无法与页面完全切换后的同名按钮安全区分，将安全停在 `partial` 并交付新 `O` |
| 持续动画页上 `act` 后观察 `settle` 耗满 2 秒预算 | 本提案明确不改 `settle` 的 19 字段收敛判据 |

### 7.3 成本

- **观察保号（纯 CPU，锁外）**：Stage 3 泛化与 Stage 6 均受现有 `workBudget`（`O(Np + Nc)` 量级）硬约束，常规 100~300 节点页面增加 `< 2ms`。
- **多基线试探（纯 CPU + 内存，锁外）**：仅在跨页/主路径未命中 `< 70%` 时触发，至多试探 3 条历史基线（每条 ~1–3ms）；留存内存中最多额外保护 3 份近期确认 `full` 快照（总计 ≤ 16 MiB，严格在既有 `32 MiB` 预算内且超 4 步自动释放）。
- **Tier 4、二分图匹配与 `changed_ref`（纯 CPU，锁外）**：Tier 4 仅在 Tier 1–3 失配且 `lineageMismatch` 时触发，扫描 `O(Np + Nc)`（E3 子树每节点有界 ≤ 64），单次 `< 2ms`；Kuhn 匹配上限 64 节点 `< 0.1ms`；成功时省去一整轮 LLM 推理 + 一次完整 `act` 往返（数秒）。
- **写后重绑定与 `submit_ref` 复采（设备 I/O，锁外）**：`enter_key` 快路径零额外开销；仅 `candidate_count=0` 时增加 1 次 `Capture`（~150–350ms）+ 1 次 `GetTextOnDisplay`（~20ms）；`submit_ref` 仅写后首次未就绪时至多等待 150ms + 1 次复采；失败时复用已采帧直接交付新 `O`，反而省去调用方一整轮 `observe`（~500ms + 大量 Token）。
- **代价承担方**：纯 CPU 与短暂 I/O 由单次操作的工作窗口承担（总计远小于 `WorkAct` 50s 上限），不阻塞 `STOP`/`end`/`status`。

---

## 8. 验证计划

### 8.1 单元测试

| 分类 | 用例 | 期望 |
| --- | --- | --- |
| #29 保号 | 17 个带相同模板 `resource-id`（`search_find`）的可点击行，内部文本唯一，连续两帧构造 | 全部 17 行保持原 ref，Compact `ReadDiff` 返回 `changes=0` |
| #29 保号（反向对照） | 整批换内容复用槽位（`A,B → C,D`）；双无锚点输入框值变化；两行文本互换或新增重复模板行 | 换内容槽位与双无锚点输入框全部发新号不串号；文本互换的容器跟随对应后代正确配对；无唯一后代且新旧数量不等时不偷号 |
| #29 自底向上 | 根容器外新插 1 层非透明包装壳（或换 class/resource），内部列表与卡片未变 | 列表与卡片通过 Stage 3B 自底向上保号，内部重复子节点经 Stage 4 恢复 |
| #29 静止几何 | 同锚定父下 2 个空间分离相同非输入叶子（静止 / ±3px 抖动） vs 同矩形重叠 vs 滚动位移 40px vs 旧 1 变新 2 | 静止与 ±3px 保号；重叠、滚动、数量变化均分配新号 |
| #29 多基线池 | `A(rev=1 full) → B(rev=2 full) → BACK A(rev=3)`；以及在 `rev=3` 后同页微改得 `rev=4`；以及跨过 4 步 `rev=6` | `rev=3` 返回 `delta base=O_1 changes=0`；`rev=4` 继续以 `O_1` 为 `base`；`rev=6` 滑出 4 步窗口退回 `full`；试探丢弃不污染 `nextRef`；`inspect(O_1)` 在池保护期内恒可读 |
| #29 多基线反向对照 | 同页内一个临时 Banner 消失（`rev=2` delta）后再出现（`rev=3`） | 因 Step 1 主路径命中 `< 70%`，不触发 Step 2，Banner 获得新 ref 不复活 |
| #36 Ref Tier 4 | 网易云根重建+2px 抖动（无自身文案歌单行 E3）、知乎底栏兄弟容器移除（自身文案 E1）、唯一 resource 控件（E2） | 全部经 Tier 4 一次 `executed` |
| #36 Tier 4 安全负例（反向对照） | 列表双卡片同文案（双向不唯一）、目标被新浮层遮挡、目标移动导致双向覆盖 < 80%、纯匿名空壳无 E1/E2/E3 | 全部拒绝 `409 target_changed`（`target_ambiguous` / `scope_blocked` / `lineage_changed`），绝不误点 |
| #36 `changed_ref` | 真实祖先结构变更拒绝 `lineage_changed`（含 `Source` 祖先与 `ContextSource` 祖先两类） | `error.details.changed_ref` 精确指向变动的最近公开祖先 ref，且不写入事件日志 |
| #36 裸 `swipe` 与坐标 | 视频页无动作叶子文本跳动 `11.0→26.0` 下裸 `swipe`（含双同质装饰叶子二分图匹配）；同页弹出遮挡层覆盖 `from`；坐标 `tap` 文案由“确认”变“取消” vs 仅根重建 | 进度秒跳与双装饰叶子裸 `swipe` `executed`；弹窗遮挡拦 `point_changed`；坐标 `tap` 根重建 `executed` 而文案变化必拦 `point_changed` |
| #30 接入协议 | `press_enter` 报 `422 invalid_request`；`auto_submit`/`submit_ref` 合法与非法组合（含 `auto_submit=false` 同时传 `"enter_key"` 或 `"n99"` 宽容忽略；传 `null`/空串/非法串/非 `text` 分支报 `422 invalid_request`） | 严格符合 §4.4.1 表与宽容规则 |
| #30 `enter_key` 重绑定 | `SetTextOnDisplay` 成功确认后 `GetTextOnDisplay(oldBounds)` 返回 `count=0`，`afterWrite` 中同框水平内缩 80px 且聚焦值相等（含双帧锚点唯一性负例） | 唯一锚点触发单次重绑定，调 `GetTextOnDisplay(freshBounds)` 确认后发送 1 次 `ENTER`，返回 `outcome=executed` + `text_input:{write:executed,submit:executed}`；非唯一锚点安全停在 `partial` |
| #30 `submit_ref` | 按钮写前 `enabled=false`→写后 150ms 变 `enabled=true`（含直接/`bubble`/`coextensive`/`colocated` 四模式及坐标 `text` 组合）；按钮随键盘垂直上移 800px | 均一次 `executed`；写后仍 `enabled=false`、缺失 `HasEnabled`（`initiallyDisabled=true`）或上下文切换时安全停在 `outcome=partial` + `409 text_submit_not_executed` + 当场交付新 `ready O` |
| #30 `partial` 与冻结/重放/失败交付 | `partial` 首次有效交付 vs 后观察失败（含无 `ID` 只读图占位） vs 重复 `P` / `status(P)` vs 并发 live blocker vs STOP 撤权/超时冻结 vs 提交回执未知 | 首次有效交付 `ID != ""` 且无实时阻塞时 `recovery=nil`；后观察失败或重放/`status(P)` 稳定返回 `observe(P, retired)` 且 live overlay 生效；撤权返回 `409 control_revoked`、仅超时返回 `409 text_submit_not_executed` 且无 `O`；提交回执未知保持 `outcome=uncertain` |
| MCP / Probe / Client | `result v4` 文本渲染/解码、`both` 模式一致、`isError`（`partial` 为 `true`）、`resolveStatusRecoveryP`/`resolveObserveRecoveryP` 解析 `partial` 为具体 `operation_id`、`tools/list` schema（无 `"default"`）、`eventlog` 白名单 | 全链路无损往返，`press_enter`/`enter_dispatch` 无残留 |

### 8.2 实机/系统级

由主代理在实现提交并完成全量单测、独立只读文档审计后亲自执行（按 AGENTS.md 第 19、24 条）：
1. **整组构建与清单核验**：运行 `scripts/build-release.ps1` 构建契约 25 全套产物，运行 `scripts/verify-manifest.ps1` 核验干净提交、SHA-256、Android 版本与工具目录指纹。
2. **手机整组覆盖部署**：覆盖部署 `device-service`、`device-mcp`、`control.apk` 等全组产物并**保留 App 私有数据**，重启服务与手机 MCP 适配器并确认 `/health` 为 `contract_version=25`。
3. **主机工具替换**：同一源码提交构建替换 Windows 主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe`。
4. **双入口独立探针**：使用本次新 `build/mcp-probe.exe` 分别验证主机 stdio MCP 与手机 Streamable HTTP MCP（核验工具目录、`auto_submit`/`submit_ref`、`result v4`、基础会话闭环）。
5. **覆盖变更行为的最小实机闭环（虚拟屏真实 App 回归）**：
   - **#29 闭环**：知乎搜索首页连续观察确认列表行保号且 delta 为 `changes=0`（无 86 条空转）；知乎视频页/B 站搜索页 `列表 → 点进详情/播放 → BACK 回列表`（4 步内）验证返回轻量 `delta base=<列表O>` 且 `inspect(base)` 可读；
   - **#36 闭环**：知乎视频播放页进度跳动时裸 `swipe` 一次成功；知乎回答底栏点“赞同”后紧接点“收藏”一次成功（无 `lineage_changed` 误拒）；
   - **#30 闭环**：网易云音乐搜索框填字默认 `auto_submit=true`（`enter_key`）验证写后重绑定一次成功提交；验证显式 `submit_ref`（含可点击按钮/子节点）一步填字并点击提交；验证 `auto_submit=false` 宽容附带 `submit_ref` 时只写不提。
6. **责任边界**：外部依赖 Agent 的配置、托管 MCP 进程重启与重连由用户处理，不作为本仓库交付门禁。

### 8.3 记录

实施与验证结束后，按流程写入 `maintenance/verification/tree-continuity-target-proof-and-text-submit.md`，分开写明“已实现 / 已单测 / 已实测”，注明实现提交 hash、构建清单核验、磁盘替换、独立进程探针与实机闭环真实输出，并将本提案移入 `maintenance/proposals/implemented/` 补写 §11。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 提案范围与拆分 | 合并 #29、#30、#36 为一份统一提案，做一次破坏性契约升级（`24 → 25`） |
| #29 跨页回返与基线窗口 | 采用有界确认 `full` 多基线池（路线 A-2），全部基线统一收紧至 `revision - base.Revision <= 4`（池上限 4、`180s`），活动基线受保护可供 `inspect` 回看 |
| #29 静止几何收尾 | 允许在无几何阶段之后，对同锚定父、非 `context`、非 `editable`、无公开子节点的叶子，在旧新同数量 ≥ 2、±4px 空间分离且保序时进行紧几何双射保号 |
| #36 重验放宽边界 | Ref 新增受控 Tier 4（四道硬闸）+ 真拒绝时可选 `changed_ref`；裸 `swipe` 升级为结构与防新遮挡证明（忽略被动叶子文案跳动，有界二分图匹配）；坐标点击/输入保留目标文案严格一致、祖先与几何对齐 Ref；不新增宽松重试开关 |
| #30 请求与响应契约 | `press_enter` 替换为 `auto_submit`（默认 `true`）+ `submit_ref`（默认 `"enter_key"` 或 `n<digits>`，`auto_submit=false` 宽容忽略提交语义）；`text_input`/`stage`/`dispatch` 统一为 `submit`；新增 `outcome="partial"` 与 `409 text_submit_not_executed`，活动会话当场交付新 `ready O` |
| 不改动项 | 不改 `act` 后观察 `settle`、不改固定 `u2.jar`、不改 Android 控制 App 业务逻辑 |

### 9.2 未决

无悬空未决项。唯一需在实施构建前核对的常规操作项是 `release/versions.json` 的实际版本号基线（确认仍为 `0.28.0 / 24 / 30` 后由实现者推进为 `0.29.0 / 25 / 31`）。

---

## 10. 实施顺序

1. **阶段 1：协议与错误契约（`internal/protocol`、`internal/eventlog`）**
   - 替换 `press_enter` 为 `auto_submit`/`submit_ref`（解码、编码、宽容逻辑）；新增 `OutcomePartial`、`CodeTextSubmitNotExecuted`、`changed_ref` 白名单；`TextInputView` 改 `Submit`；`eventlog` 改 `submit_dispatch` 与 `tier`/`baseline_pool`；补齐协议层单测。
2. **阶段 2：观察保号与多基线只读候选（`internal/observation`、`cmd/device-service/main.go`，可与阶段 1 并行）**
   - 实现泛化 Stage 3A/3B（自底向上）、Stage 5、Stage 6（静止非输入叶子紧几何）与 `Builder.CandidateAgainst`；更新 `observationProjector`；补齐保号与反向对照单测。
3. **阶段 3：引擎多基线池与目标重验（`internal/engine`）**
   - 落地 `baselines` 池、`activeBaselineID`、全池淘汰保护、`publishTreeAttempt` 两步选择；
   - 落地 Ref Tier 4、`changed_ref`（兼查 `Source`/`ContextSource`）、`provePointTarget` 有界二分图匹配的裸 `swipe`/坐标分层证明；更新受影响的既有引擎测试并补新单测。
4. **阶段 4：引擎 `act(text)` 受控提交与 `partial` 交付（`internal/engine`）**
   - 落地 `planTextSubmitRef`（穿透 `tap_scope.go` 四处禁用门禁、同时覆盖 ref/坐标 `text`）、`enter_key` 写后单次重绑定（双帧锚点唯一）、`submit_ref` 写后重验（覆盖全四类绑定模式 + 键盘垂直平移 + 1 次 150ms 复采）、`ui_execution.go` `case "submit"`、`OutcomePartial`、`deliverTextPartialObservation`、`recovery`/`text_recovery`/`retirement` 投影；补齐文本全矩阵单测。
5. **阶段 5：适配器与探针（`internal/httpapi`、`internal/client`、`internal/mcp`、`scripts/mcp-probe`）**
   - 同步 MCP schema（无分支 `"default"`）、指引、`result v4` 渲染/解析、`resolveStatusRecoveryP`/`resolveObserveRecoveryP` 纳入 `partial`、`isError`、客户端与探针断言。
6. **阶段 6：版本源、文档同步、全量测试与独立文档审计**
   - 更新 `release/versions.json` 并运行 `scripts/generate-version.ps1`；同步全部受影响 `docs/` 与 `README.md`；
   - 由主代理亲自运行全量 Go 测试（引擎单次命令超时 15 分钟）与交叉编译检查；
   - 发起独立只读文档审计 subagent 审阅所有修改的 `docs/`，取得 clean 结论后提交实现。
7. **阶段 7：发布构建、实机交付、验证记录与归档**
   - 由主代理亲自运行 `build-release.ps1`、`verify-manifest.ps1`、手机整组覆盖部署（保留 App 私有数据）、主机 `device-mcp.exe`/`mcp-probe.exe` 替换、双入口独立探针与 #29/#30/#36 最小实机闭环；
   - 单独提交提案归档（移入 `implemented/`、重写相对链接、补 §11）、写入 `maintenance/verification/tree-continuity-target-proof-and-text-submit.md` 与 `maintenance/status.md`；推送后回复并关闭远程 Issue `#29`、`#30`、`#36`。

---

## 11. 实现结果

实现提交 `758f2e59e2f925814ce867decf90932b728d4d5e`。命令、产物与实机事实见 [验证记录](../../verification/tree-continuity-target-proof-and-text-submit.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 树保号与多基线 | [保号流水线](../../../internal/observation/match.go) 泛化后代投票、跨包装锚定、序位配对和紧几何收尾。[多基线提交](../../../internal/engine/capture.go) 使用同一提交时快照判定基线资格与留存保护。历史试探保留原 ref 高水位，成功提交后才写入连续性状态。 |
| 目标证明与归因 | [Ref 证明](../../../internal/engine/act_revalidation_policy.go) 增加 Tier 4。[共享重验](../../../internal/engine/act_target_shared.go) 在允许时归因 `changed_ref`。[坐标和裸手势](../../../internal/engine/point.go) 使用各自的匹配判据与有界双射。 |
| 文本受控提交与部分完成 | [提交路径](../../../internal/engine/text_submit.go) 实现默认 ENTER 的单次重绑定和指定节点点击。[部分完成](../../../internal/engine/text_partial.go) 按原授权交付现场 O，[实时恢复](../../../internal/engine/text_recovery.go) 统一处理首次响应与重放。 |
| 契约与发布 | [版本源](../../../release/versions.json) 升至 `0.29.0 / 25 / 31`。HTTP、Go client、MCP schema/`result v4` 与探针同步，手机整组覆盖部署及 Windows 双工具替换通过同源清单核验。 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| #29 原附件跨 5 个修订 | 保留已确认的 4 revision 上限。原 `O85 → O90` 回返仍交付 `full`。本次知乎与 B 站回返均跨 3 个修订并命中历史基线。 |
| Tier 4 最终防遮挡 | 采纳独立审阅意见，Tier 4 实际落点补做全树最深命中和竞争拥有者检查。实机知乎回答连续点击命中 Tier 1。祖先重建后的 Tier 4 命中与外部遮挡拒绝由定向测试覆盖。 |
| 网易云写后几何重绑定 | 默认 ENTER、显式 `submit_ref`、仅写入均在实机成功。默认 ENTER 的日志能确认一次提交，未区分快路径与零候选后的重绑定。控件缩框、重绑定唯一性、再次精确读回、`partial` 与撤权时序由 Go 测试覆盖。 |
| 依赖方 MCP 宿主 | 主机磁盘与独立探针已切换。部署检测到依赖方仍运行旧 stdio 进程。按 §8.2 由用户处理宿主重启和重连。 |
| 正式文档审计 | 独立只读审计发现三处差异，分别统一动态祖先归因 `changed_ref`、按提案限定基线确认入口、明确留存保护只覆盖窗口内基线。修正后复检 clean。 |

---

## 附：本提案不改变的事项

- 会话状态机、租约、O 单向消费、P 幂等与淘汰上限、STOP 优先与 5 秒逻辑交接。
- 恢复屏障、账本格式、应用生命周期（`app` 7 个 kind）与受管 `shell` 行为。
- `act`/`start`/`app.*` 的后观察 `settle` 判据与 `wait_for` 内容稳定判据。
- `tap`/`long_press`/`scroll` 的静态 422 5 码体系与 `candidates` 对象结构。
- `observe`/`snap` 的图片采集、缩放、单次 JPEG 编码与预算。
- 固定 `u2.jar` 与 `applist.dex` 制品，以及 Android 控制 App 的业务逻辑（仅更新生成的版本常量）。
- `tree v4` 的 5 块 delta 语法与 `inspect v3` 详细补看格式。
- 日志字段白名单原则（不记树、文本、坐标、ref、凭据或原始异常）。
