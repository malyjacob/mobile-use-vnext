# 提案：校准稀疏树提示、补齐无交互祖先坐标、精简 MCP 工具目录并补强 app/diagnose 诊断摘要（契约 35）

状态：提案，未实现。
基线提交：`34209ef545302036f8efc194856d0a46f54880ef`（分支 `master`，`2026-11-28`）。
适用范围：观察视觉分类与提示路由（`internal/observation`、`internal/engine`）、`act` 静态无交互祖先拒绝详情（`internal/engine`、`internal/protocol`）、MCP 13 工具发现说明与 Schema 瘦身及 `result v4` 投影（`internal/mcp`）、`app.close`/`app.kill` 安全拒绝参数提示与变更 `status(P)`/重复 `P` 摘要重放（`internal/engine`、`internal/protocol`）、`diagnose()` 会话过滤与错误码聚合（`internal/engine`、`internal/protocol`、`internal/mcp`）、独立探针（`scripts/mcp-probe`）、正式文档（`docs/`）与整组发布交付。不包含研究报告阶段 1（宿主侧上下文治理）、树微剪枝（`N1`/`N2a`/`N3`）与 `inspect(query)` 路径格式改动。
影响范围：`release/versions.json` 及 4 个生成版本文件、`internal/observation/{visual.go,visual_test.go}`、`internal/protocol/{errors.go,surface.go,decode_response.go,*_test.go}`、`internal/engine/{capture.go,tap_scope.go,app.go,app_close.go,app_kill.go,engine.go,diagnose.go,*_test.go}`、`internal/mcp/{schema.go,schema_test.go,catalog_test.go,result_text.go,result_text_test.go,result_text_v4_test.go,result_manifest_test.go,result_parity_test.go,app_test.go}`、`scripts/mcp-probe/{result.go,result_v4.go,catalog.go,probe_test.go,*_test.go}`、`README.md` 与 `docs/{interface.md,mcp.md,architecture.md,client.md,control-protocol.md,integration.md,phone-operations.md,release.md,runbook.md}`。
契约（`/api/*` 字段与 `contract_version`）：改变（通过单一版本源 [release/versions.json](../../release/versions.json) 将版本由 `0.38.0` / `contract_version=34` / `android_version_code=40` 升级为 `0.39.0` / `contract_version=35` / `android_version_code=41`：`Observation.visual=sparse_tree` 阈值与 `Observation.hint` 路由演进；`error.details` 白名单新增 `target_center` 与 `retry_parameter`；`status(P)` 与重复 `P` 对 5 种变更 `app` 保留终态定格 `AppView`；`DiagnosisView` 新增可选 `error_counts` 且无参 `diagnose()` 有活动会话时聚焦当前会话并按 `(phase, code)` 去重 `issues`；`result v4` 增 `diagnosis.error_counts=<JSON>` 行，外壳头 `result v4` 与 `tree v5` 语法不变；MCP 13 工具发现目录瘦身并刷新工具目录 SHA-256 指纹）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[visual.go](../../internal/observation/visual.go)、[capture.go](../../internal/engine/capture.go)、[wait_for.go](../../internal/engine/wait_for.go)、[tap_scope.go](../../internal/engine/tap_scope.go)、[errors.go](../../internal/protocol/errors.go)、[surface.go](../../internal/protocol/surface.go)、[decode_response.go](../../internal/protocol/decode_response.go)、[app.go](../../internal/engine/app.go)、[app_close.go](../../internal/engine/app_close.go)、[app_kill.go](../../internal/engine/app_kill.go)、[engine.go](../../internal/engine/engine.go)、[operations.go](../../internal/engine/operations.go)、[diagnose.go](../../internal/engine/diagnose.go)、[schema.go](../../internal/mcp/schema.go)、[result_text.go](../../internal/mcp/result_text.go)、[result_v4.go](../../scripts/mcp-probe/result_v4.go)）。

---

## 0. 摘要

继契约 34 落地研究报告（`.state/dsh-session-10ca6f7e/research/mcp-context-research-0.37.0.md`）阶段 2 的三项 `act` 韧性修复后，本提案将报告**阶段 3（3 项 P1）**与**阶段 4（3 项 P2，不含微剪枝）**合并于契约 35 一次收口，在零新增请求参数、零新增设备读取与严守事实分层/日志白名单的前提下，系统性补齐信号精度、交互兜底、发现底座成本与诊断/状态可读性：

1. **稀疏树校准与防循环提示路由（议题 1）**：将 `visual=sparse_tree` 门限由 `textRunes < 30 && interactive < 7` 收紧至 `textRunes < 20 && interactive < 4`（消除完整小弹窗误报）；并将 `sparse_tree` 的 `hint` 路由按来源分流——非 `wait_for` 终帧且无 `unchanged`/`detail_only` 时优先提示 `hint=wait_for`，而在 `wait_for` 终帧（已等待仍稀疏，且 `delta` 亦保留 `visual=sparse_tree`）或叠加无树变化时于无有效图前提下回落 `hint=snap`，彻底打破加载过渡误导与纯自绘静止页死循环。
2. **无交互祖先静态拒绝附带客观几何中心（议题 2）**：在 `422 unsupported_node_action (reason=no_interactive_ancestor)` 且目标具有屏内裁剪几何时，复用既有计算在 `error.details` 附带客观字段 `target_center: [x, y]`（不承诺落点安全、不认领 `O`），消除 Hybrid/WebView 隐藏坐标被动文本项的额外 `inspect` 或盲猜成本。
3. **MCP 13 工具目录与发现说明瘦身（议题 3）**：在保住契约 19 单工具自包含防误用红线与 `app` 7-kind 矩阵的前提下，消除单工具内 `tool.description`/`property.description`/根 `schema.description` 的多重机械复述与跨工具图例照抄，将 `tools/list` 压减 25%–35%（`27,369 B → <= 20,500 B`）、发现总量由距上限仅余 74 B 的 `39,926 B` 压至 `< 31,000 B` 并收紧 `baselineDiscoveryBytes`。
4. **`app` 安全拒绝精确参数提示、`status(P)` 变更摘要重放与 `diagnose()` 会话过滤聚合（议题 4–6）**：在 `close_refused_borrowed` 与已知播放/前台服务的 `kill_refused_playback`（明确排除未知态拒绝）附 `error.details.retry_parameter`；为 5 种变更 `app`（`open`/`claim`/`handoff`/`close`/`kill`）在终态原子保存不可变定格 `AppView` 快照供 `status(P)`/重复 `P` 重放（`find`/`list` 与运行态保持仅 `app.kind`）；无参 `diagnose()` 在存在当前未结束本 Agent 会话时默认聚焦当前会话，操作级 `issues` 按 `(phase, code)` 去重，并新增受控计数表 `diagnosis.error_counts`（`result v4` 输出 `diagnosis.error_counts=<JSON>`）。

本提案明确**不做**宿主侧滑窗折树/速查卡（阶段 1）、**不做**任何树微剪枝（`N1`/`N2a`/`N2b`/`N3`）、**不改** `inspect(query)` 完整 `detailed` 路径、**不新增**任何工具请求参数、**不改** `tree v5` 语法、`recovery` 6 元状态机或固定 `u2.jar`。

---

## 1. 问题

### 1.1 现象与代价

六项问题均来自 `0.37.0` 完整实机会话 `10ca6f7e`（140 次 `mobile-use` 调用、115 棵观察树、143 步总账单 1,555 万 Prompt token）与当前 `HEAD` `34209ef` 实测数据：

1. **`visual=sparse_tree` 误报完整弹窗且 `hint=snap` 采纳率 0/6**：
   - `O77`（B 站“选择收藏夹”底部弹窗：12 节点、6 个交互控件、26 字，含“选择收藏夹 / 新建收藏夹 / 默认收藏夹 / 完成”）命中 `visual=sparse_tree hint=snap`，Agent 直接忽略并精准点了 `完成`，使 `sparse_tree` 精确率降至 75%（3/4）。
   - 3 帧真实空树加载过渡页（`O85` 1n/0r/0i、`O91` 2n/0r/1i、`O132` 2n/11r/0i）均收到 `hint=snap`，但 Agent 3 次全部正确选择 `wait_for(stable=true)` 等待内容渲染出 64–85 节点（本场 `hint=snap` 0/6 采纳）。
2. **混排 WebView 被动文本遇 `no_interactive_ancestor` 缺坐标兜底**：
   - 网易云听书榜单 `O120`（`seq=526/531`）中被动文本叶 `n6047` 在 compact 树隐藏了几何且无交互祖先、`candidates` 为空，返回 `422 unsupported_node_action (reason=no_interactive_ancestor)` 后无法直接发起坐标点按，只能借邻近节点坐标试错或额外调用 `inspect`（本场多耗 1 次调用 / 102 tok）。
3. **MCP 13 工具 Schema 固定底座膨胀并已触及预算红线**：
   - 实跑当前 `HEAD` 的 `TestDiscoveryByteBudget` 测得：`instructions=12,557 B`、`tools_list=27,369 B`（`descriptions=14,030 B`、`schemas=12,252 B`，其中 `act` 独占 `8,946 B / 32.7%`、`app` 占 `5,046 B / 18.4%`），**总计 `39,926 B`，距 `baselineDiscoveryBytes = 40,000 B` 仅剩 `74 B`**；
   - 在 `10ca6f7e` 实机 143 步中，13 个 `mobile-use` 工具 Schema 占 `7,748 tok/步`，累计暴露 **1,107,964 tok-step（占全会话 Prompt 总账单 7.13%）**，单轮成本杠杆超过全部剩余树微剪枝收益 2.3–3.2 倍。
4. **`app.close` / `app.kill` 安全拒绝未结构化告知重试参数名**：
   - `seq=677`（`close_refused_borrowed`）与 `seq=687`（`kill_refused_playback`）均返回空 `details`，调用方需回查 Schema 确认确切布尔覆写参数名，且无法从结构化详情区分 `kill_refused_playback` 的“状态未知不可越过”与“已知播放/前台服务可显式越过”。
5. **`status(P)` 查 `app` 变更只输出裸 `app <kind>`**：
   - `seq=697` 调用 `status(P5737_127)` 查询刚完成的 `app kill`，只得到 `outcome=executed` 与裸 `app kill`，丢失首次结果已证明的 `pkg`/`target_tasks`/`killed_pids`/`package_state` 等核心事实（回包丢失或事后核查时无法得知清理结论）。
6. **无参 `diagnose()` 混入历史会话记录且重复错误挤占截断**：
   - `seq=657` 在会话 `P5737_1`（22:52–23:07）调用 `diagnose({})`，结果混入上一场已结束会话 `Pdev1`（22:47）的失败记录，且同 `(phase, code)` 逐条堆叠快速撞上 `maxDiagnosisEntries = 8` 触发 `truncated=true`，缺少错误码汇总计数。

### 1.2 证据链

全部六项的源码通路均已在基线 `34209ef` 核实到具体文件与行号：

| 议题 | 基线源码与测试落点 | 核实事实 |
| :--- | :--- | :--- |
| **1. `sparse_tree` 与 `hint`** | [visual.go:51-52,109-111](../../internal/observation/visual.go#L51-L52)、[observation/visual_test.go:198-210,306-308,334-335,355-374,391-404](../../internal/observation/visual_test.go#L306-L308)、[capture.go:493-507,576-596](../../internal/engine/capture.go#L493-L507)、[wait_for.go:407,505-506](../../internal/engine/wait_for.go#L407)、[observation_hint_test.go:14-36,102-135](../../internal/engine/observation_hint_test.go#L14-L36)、[engine/visual_test.go:105-160,268](../../internal/engine/visual_test.go#L105-L160)、[quality_test.go:268](../../internal/engine/quality_test.go#L268) | `visualSparseMaxTextRunes=30`、`visualSparseMaxInteractive=7`；`observationVisual` 仅对 `loading` 在 `delta` 免省略；`observationHint` 仅 `visual=="loading"` 出 `wait_for`，其余 `visual!=""` 一律出 `snap`；`wait_for` 调 `publishTreeAttempt` 固定传 `phase="wait_for"`（唯一使用该 phase 的发布入口）；`scripts/mcp-probe/tree_body_consistency.go:37,55` 仅校验 `hint` 闭集、无树禁现与有效图禁 `snap`（允许有效图配 `wait_for`）；上述既有单测夹具需同步更新预期 |
| **2. `target_center`** | [tap_scope.go:544-550,1691-1719,2236-2237,2366-2370,2473](../../internal/engine/tap_scope.go#L2236-L2237)、[text_submit.go:207-230](../../internal/engine/text_submit.go#L207-L230)、[tap_static_error_test.go:315-328](../../internal/engine/tap_static_error_test.go#L315-L328)、[errors.go:9-28](../../internal/protocol/errors.go#L9-L28)、[schema_test.go:478-488](../../internal/mcp/schema_test.go#L478-L488) | `planBubbleOrColocated`（含 `text_submit.go:207-230` 对 `submit_ref` 的复用调用）已提前算好 `childRect, hasChildRect := publicNodeClippedRect(child, screen)` 与 `centerX, centerY, hasCenter := tapSafeCenter(childRect)`，但 `bubbleStepANoAncestor` 仅调 `newTapStaticUnsupportedError` 与 `attachStaticCandidates`，未附坐标；`tap_scope.go:2473` 只检查未裁屏正尺寸，完全离屏节点可到达此处并使 `hasCenter == false`；历史废弃键表 `retiredActDetailKeys` 含 `safe_point`/`suggested_point`（语义为安全建议），不含客观坐标 `target_center` |
| **3. MCP 目录瘦身** | [schema.go:28-792](../../internal/mcp/schema.go)、[schema_test.go:25,30-1298](../../internal/mcp/schema_test.go#L25)、[catalog_test.go:36-47](../../internal/mcp/catalog_test.go#L36-L47)、[result_parity_test.go:554](../../internal/mcp/result_parity_test.go#L554)、[mcp-tool-descriptions-governance.md](implemented/mcp-tool-descriptions-governance.md#L56-L60) | 实跑 `TestDiscoveryByteBudget` 得 `instructions=12557`、`tools_list=27369`（`descriptions=14030`、`schemas=12252`）、`total=39926 < 40000`；`act` 的 `scroll`/`bubble`/`text` 在 `callerGuidance`、`act.description`、`operation.*.description` 存在 3–4 重复述；13 工具 Schema 根级 `"description"` 全部机械复述 `required`/`properties`；`base=_72` 替换步骤在 4 处重复；全仓唯一硬编码目录 SHA-256 指纹位于 `result_parity_test.go:554` |
| **4. `retry_parameter`** | [app_close.go:48-55](../../internal/engine/app_close.go#L48-L55)、[app_kill.go:342-349](../../internal/engine/app_kill.go#L342-L349)、[errors.go:9-28,232,236](../../internal/protocol/errors.go#L9-L28)、[engine.go:1305-1310](../../internal/engine/engine.go#L1305-L1310) | `app_close.go:52`（`close_refused_borrowed`）无 `details`；`app_kill.go:344-346`（未知状态拒绝）先于 `req.AllowStopPlayback` 判断、传参也不可越过，而 `app_kill.go:347-349`（已知前景服务或播放）在 `!req.AllowStopPlayback` 时拒绝；`operationViewLocked` 通过 `CloneErrorDetails` 自动重放 `op.Error.Details` |
| **5. `status(P)` 保留 `app` 摘要** | [engine.go:574-576,1294-1304](../../internal/engine/engine.go#L1294-L1304)、[app.go:91-100,183-294](../../internal/engine/app.go#L183-L294)、[operations.go:184-240](../../internal/engine/operations.go#L184-L240)、[retirement.go:626-646](../../internal/engine/retirement.go#L626-L646)、[app_lifecycle_test.go:431-449](../../internal/engine/app_lifecycle_test.go#L431-L449)、[surface.go:543-589](../../internal/protocol/surface.go#L543-L589) | `operationState` 仅存 `AppKind`，`operationViewLocked` 硬编码返回 `&protocol.AppView{Kind: op.AppKind}`（`TestAppDuplicatePRepublishesOnlyKind` 显式断言旧行为）；`finishAppExecuted` 有 4 处 `finishOperation` 出口（`app.go:231,236,245,248`）；`finishAppUncertainWorkAfter` 的 `after()` 钩子（如 `openapp.go:309`）会就地补充 `view`；`applyFrozenResult` 对已因 `work_deadline` 冻结的 `op` 重放 `operationViewLocked(op)`；`AppView` 依赖 6 个集合 `Has*` 标志区分已知空切片 `[]` 与未知省略 |
| **6. `diagnose()` 过滤与聚合** | [diagnose.go:184-262,341-360](../../internal/engine/diagnose.go#L184-L262)、[operations.go:96-105](../../internal/engine/operations.go#L96-L105)、[start.go:16,85-87](../../internal/engine/start.go#L16)、[surface.go:1215-1374](../../internal/protocol/surface.go#L1215-L1374)、[decode_response.go:978-1040](../../internal/protocol/decode_response.go#L978-L1040)、[result_text.go:76-80,1418-1479,2102-2105](../../internal/mcp/result_text.go#L1418-L1479)、[result_manifest_test.go:1095-1108,1680-1723](../../internal/mcp/result_manifest_test.go#L1095-L1108)、[scripts/mcp-probe/result_v4.go:549-569](../../scripts/mcp-probe/result_v4.go#L549-L569)、[scripts/mcp-probe/probe_test.go:1646-1684](../../scripts/mcp-probe/probe_test.go#L1646-L1684)、[readonly_test.go:840-1032](../../internal/engine/readonly_test.go#L840-L1032) | `diagnoseOwnLocked` 遍历全部同 Agent 操作不区分 `SessionOrigin`，同 `(phase, code)` 逐条塞入 `Issues` 至上限 8；所有绑定会话操作（含活动态重复 `start`）注册时均写 `op.SessionOrigin = e.session.Origin`，detached 操作（`app.find`、`notify`、准入前被拒的 `snap`）`SessionOrigin == ""` 并带 `CreatedAt`；`DiagnosisView` 无聚合计数成员，MCP 侧存在 `v4ResultFields`（L77）与 `resultFields`（L2103）两张反射清单注册表 |

### 1.3 为什么直觉会出错

1. **为什么不能把所有 `sparse_tree` 一律改成 `hint=wait_for` 而不处理 `wait_for` 终帧的 `delta` 同态省略？**
   - 若不区分来源，纯 Canvas/自绘静止页在 `wait_for` 后仍输出 `hint=wait_for`，会诱导调用方死循环等待；
   - 若仅在 `observationHint` 切为 `snap` 却忘了 `observationVisual` 的 `delta` 同态省略，那么从 `O85 (full, sparse_tree, hint=wait_for)` 调用 `wait_for` 得到同页 `O86 (delta changes=0)` 时，`publishedVisual` 会被省略为 `""`，导致 `observationHint` 看不到 `sparse_tree` 从而根本不发 `hint=snap`！让 `wait_for` 终帧的 `sparse_tree` 与 `loading` 一样在 `delta` 免省略，才能保证 `O86` 清晰输出 `visual=sparse_tree hint=snap`，同时维持“`hint` 严格由已发布字段推导、不凭空无因出现”的契约不变式。
2. **为什么 `no_interactive_ancestor` 暴露坐标不能叫 `suggested_point`/`safe_point`，也不能给 `no_safe_point`？**
   - 遵循 AGENTS.md 硬约束 #8（事实分层）：对无交互祖先节点，服务端未证明点中该处安全或有业务响应，命名 `target_center` 仅客观陈述目标裁剪框几何中心；`no_safe_point` 恰恰已证明中心区域被遮挡或归属歧义，附带 `target_center` 会反向诱导误点。
3. **为什么不能为了极限省字节把工具关键规则全删光只留 `serverInstructions`？**
   - 契约 19 配对回放已证实部分宿主会剥离或弱化 `serverInstructions` 仅注入 `tools/list`。瘦身必须主要靠消除单工具内 2–4 重复述、删根级机械 `schema.description` 与跨工具长图例照抄，而非牺牲单工具自包含防误用红线。
4. **为什么不能对 `app_kill.go:345`（状态未知）也附 `retry_parameter: "allow_stop_playback"`？**
   - [app_kill.go:344-346](../../internal/engine/app_kill.go#L344-L346) 在 `!services.Known || !media.Known` 时根本不检查 `req.AllowStopPlayback`，传 `true` 依然拒绝。只给 L348（已知前台服务或播放）附 `retry_parameter`，才能保证该字段 100% 诚实可执行。
5. **为什么不能把 `find`/`list` 集合也缓存进 `op.App`，也不能在 `finishOperationLocked` 锁外提前写 `op.App`？**
   - `find`/`list` 的 `items` 单次可达 32 KiB 且包含本地化应用名与易变实时列表，缓存会导致内存膨胀与陈旧列表冒充实时快照；
   - 若在锁外先写 `op.App`，已因 5 秒 `work_deadline` 冻结的迟到 `app` 回调会污染 `applyFrozenResult` 重放的冻结摘要，必须在锁内检查 `op.State != OperationFinished` 后原子写入。
6. **为什么 `diagnose()` 当前会话过滤不能只查 `op.SessionOrigin == session.Origin`？**
   - 活动会话期间发生的 detached 操作（如 `app.find`、`notify` 与准入前被拒的 `snap`；活动态重复调 `start` 被拒已在 `start.go:16` 绑定了 `e.session.Origin`）注册时 `op.SessionOrigin == ""`；必须同时纳入 `op.SessionOrigin == "" && !activeSession.StartedAt.IsZero() && !op.CreatedAt.Before(activeSession.StartedAt)`，才能既屏蔽上一场旧会话的历史噪音（且防范零时间戳回流），又不丢失当前会话期间的独立失败。

---

## 2. 目标与非目标

**目标**：
1. 将 `visual=sparse_tree` 校准至 `textRunes < 20 && interactive < 4`，并使 `observationVisual`/`observationHint` 形成无循环闭环：非 `wait_for` 终帧且无无树变化时 `sparse_tree -> hint=wait_for`；`wait_for` 终帧保留 `visual=sparse_tree` 并在 `!hasValidImage` 时回落 `hint=snap`。
2. `422 unsupported_node_action (reason=no_interactive_ancestor)` 在目标有有效屏内裁剪几何时，于 `error.details` 附带客观字段 `target_center: [x, y]`，零额外设备读、不认领 `O`。
3. 在不损失单工具防误用关键语义与 `app` 7-kind 矩阵的前提下，将 MCP 13 工具 `tools/list` 压减 25%–35%（`<= 20,500 B`），发现总量降至 `< 31,000 B` 并收紧 `baselineDiscoveryBytes` 至 `31,000`，刷新目录 SHA-256 指纹。
4. `close_refused_borrowed` 与已知播放/前台服务触发的 `kill_refused_playback` 在 `error.details` 分别附带 `retry_parameter: "allow_borrowed"` / `"allow_stop_playback"`；未知态 `kill_refused_playback` 明确省略。
5. 5 种变更 `app`（`open`/`claim`/`handoff`/`close`/`kill`）在正常终态原子保存不可变定格 `AppView` 摘要供 `status(P)` 与重复 `P` 重放；`find`/`list`、`running` 与 `work_deadline` 冻结后迟到回调保持仅 `app.kind`。
6. 无参 `diagnose()` 在本 Agent 存在未结束会话时默认聚焦当前会话（含该会话期间的 detached 操作），操作级 `issues` 按 `(phase, code)` 去重，并在 `DiagnosisView` 新增可选 `error_counts`（按批准控制码统计当前窗口全部匹配失败/不确定次数），全链路支持严格 JSON 编解码、`result v4` 渲染与 `mcp-probe` 独立验证。
7. 同步 `release/versions.json`（`0.39.0` / 契约 `35` / `versionCode 41`）、正式文档 `docs/`（通过独立只读文档审计）与完整三项发布交付。

**非目标**：
- 不包含研究报告阶段 1（DSH 宿主滑窗折叠、速查卡、宿主侧工具裁剪）；
- 不包含任何公开树微剪枝（`N1`/`N2a`/`N2b`/`N3`），不改变 `tree v5` 语法、`match.go` 保号或多基线池 `(12, 10, 5) @ 80%`；
- 不改变 `inspect` 完整 `view=detailed` 路径或 `inspect v3` 语法；
- 不新增任何 `/api/*` 请求参数（包括不给 `diagnose` 加 `scope` 参数、不给 `observe`/`act` 加提示开关）；
- 不修改 `protocol.Recovery` 6 元状态机、不修改固定制品 `third_party/uiautomator/u2.jar`、不向 `internal/eventlog` 新增白名单字段；
- 不把进程重启后未持久化的 `app` 变更详情伪造回 `status(P)`。

---

## 3. 路线对比

六项议题均在讨论阶段对比了保守与激进方案；用户已确认合并打包为单一提案并授权按以下选定路线定稿：

| 议题 | 未选路线（含“不做”） | 选定路线 | 选定理由与不选他路之代价 |
| :--- | :--- | :--- | :--- |
| **1. 稀疏树与提示** | **1A** 不做（保留 `<30/<7` 与全 `snap`）；<br/>**1B** 仅收紧 `<20/<4` 仍全出 `snap` | **1C 收紧 `<20/<4` + 防循环 `wait_for`/`snap` 分流 + `wait_for` 终帧 `delta` 保留 `sparse_tree`** | 1A 留存弹窗误报与过渡页 0% 采纳；1B 仍误导空树过渡页截白屏；1C 同步解决误报、过渡页推荐与纯 Canvas 静止页防死循环 |
| **2. 无交互祖先坐标** | **2A** 不做；<br/>**2C** 用 `target_bounds` 或扩至 `target_missing_action` | **2B 仅 `no_interactive_ancestor` 附 `target_center: [x, y]`** | 与 `tree v5` 普通节点交付中心点一致且最紧凑；`target_missing_action`（`bubble=false`）应优先用 `bubble=true` 或 `candidates`，不诱导绕行 |
| **3. MCP 目录瘦身** | **3A** 仅抬高 40 KB 上限；<br/>**3C** 激进删光单工具语义全移全局指令 | **3B 去重 + 电报化压减 `tools/list` 25%–35%，同步微瘦 `callerGuidance`，保住自包含红线** | 3A 放任 7.13% 固定税恶化；3C 在剥离 `serverInstructions` 的宿主上诱发误用（违背契约 19 教训）；3B 兼顾降本与单工具可用性 |
| **4. `app` 拒绝参数提示** | **4A** 不做；<br/>**4B** 仅改中文 `message` 不加结构化字段 | **4C `error.details.retry_parameter`，仅附于 `app_close.go:52` 与 `app_kill.go:348`** | 结构化、可编程区分 L345 未知态拒与 L348 已知可越过拒，且 `status(P)` 自动重放 |
| **5. `status(P)` `app` 摘要** | **5A** 保持仅 `app.kind`；<br/>**5B-2** 为重放造精简子集 | **5B-1 5 种变更 kind 原子保存完整定格 `AppView` 快照；`find`/`list` 与 `running` 保持仅 `app.kind`** | 与首次结果的 `app` 对象 100% 同构、无第三套子集心智负担，体积极小（无用户文本/无大集合）且与 `op.Wait`/`op.Effect`/`op.TextInput` 设计一致 |
| **6. `diagnose()` 过滤与聚合** | **6A-0** 不做；**6A-2** 加请求参数 `scope`；<br/>**6B-①** 改 `DiagnosisIssue` 加 `count` 不加顶层计数 | **6A-1 零新增参数默认聚焦当前未结束会话 + 操作级 `issues` 按 `(phase, code)` 去重 + 新增 `diagnosis.error_counts`** | 遵守“服务端能判定就不加请求字段”原则（且契合议题 3 瘦身）；保持 `DiagnosisIssue` `{phase,code}` 纯净不破坏旧解析，同时在截断时仍提供完整错误码分布 |

*触发回看条件*：若未来实机频繁出现“活动会话期间需无参一次扫全进程历史旧会话失败”的强需求，再另开案评估给 `diagnose` 增加 `scope` 参数；若出现大量需矩形宽高的被动文本落点场景，再评估补充 `target_bounds`。

---

## 4. 判据

以下逐项给出可由代码与单元测试直接验证的精确规约。

### 4.1 稀疏树阈值、`delta` 保留与 `hint` 路由（议题 1）

1. **`internal/observation/visual.go` 阈值常量**：
   - `visualSparseMaxTextRunes = 20`（由 `30` 收紧）；
   - `visualSparseMaxInteractive = 4`（由 `7` 收紧）；
   - `ClassifyVisual` 优先级顺序 `loading > image_heavy(<pct>%) > media_surface > sparse_tree` 及前三类全部阈值保持不变。
2. **`delta` 视觉分类去重（`observationVisual(delta bool, hint visualHint, previousKind string, fromWaitFor bool) string`）**：
   - `hint.kind == ""` → `""`；
   - `!delta`（`full` 观察）→ `hint.raw`；
   - `delta == true`：
     - `hint.kind == observation.VisualKindLoading` → `hint.raw`（不变）；
     - **`fromWaitFor && hint.kind == observation.VisualKindSparseTree`** → `hint.raw`（**新增**：`wait_for` 终帧若仍为稀疏树，即使以 `delta` 发布也必须显式保留 `visual=sparse_tree`，以便与回落的 `hint=snap` 一致）；
     - 其余：`hint.kind != previousKind` 时输出 `hint.raw`，同态省略返回 `""`。
3. **观察级提示推导（`observationHint(visual, treeChange string, hasValidImage, fromWaitFor bool) string`）**：
   - 调用方按 `fromWaitFor := (phase == "wait_for")` 传入（[wait_for.go:407](../../internal/engine/wait_for.go#L407) 是唯一传入 `"wait_for"` 的发布入口）。
   - 互斥优先判据：
     1. `visual == observation.VisualKindLoading` → `"wait_for"`（即使 `hasValidImage == true`）；
     2. `visual == observation.VisualKindSparseTree`：
        - 若 `!fromWaitFor && treeChange != protocol.TreeChangeUnchanged && treeChange != protocol.TreeChangeDetailOnly` → **`"wait_for"`**（即使 `hasValidImage == true`，过渡空树首要建议仍是等待）；
        - 否则，若 `!hasValidImage` → **`"snap"`**（`wait_for` 终帧仍稀疏，或动作后无树变且仍稀疏，引导截图打破循环）；
        - 否则（已带有效图）→ `""`；
     3. `!hasValidImage && (visual != "" || treeChange == protocol.TreeChangeUnchanged || treeChange == protocol.TreeChangeDetailOnly)` → `"snap"`（覆盖 `image_heavy(<pct>%)`、`media_surface` 与普通页无树变）；
     4. 其余 → `""`。
   - **不变式保持**：`hasValidImage == true` 时永不输出 `hint="snap"`；`matched_base` 无 `visual` 时永不出 `hint`；无树失败观察的 `visual`/`tree_change`/`hint` 全为空；绝不出现 `visual==""` 且 `tree_change==""` 却带 `hint` 的悬空状态。

### 4.2 `no_interactive_ancestor` 的客观 `target_center`（议题 2）

- **触发条件**：仅在 [tap_scope.go:2366-2370](../../internal/engine/tap_scope.go#L2366-L2370) `planBubbleOrColocated` 落入 `case bubbleStepANoAncestor` 返回 `422 unsupported_node_action (reason=no_interactive_ancestor)`，且同函数 L2236-2237 已算出的 `hasCenter == true` 时生效。该规划器同时服务于普通 `tap`/`long_press` 与 [text_submit.go:207-230](../../internal/engine/text_submit.go#L207-L230) 的 `text(submit_ref)`（静态拒绝、不认领 `O`），因此 `submit_ref` 指向无交互祖先被动节点时亦按同一规则附带客观 `target_center`。
- **字段形状与语义**：
  - 键名：`error.details["target_center"]`；
  - 值类型：`[]int{centerX, centerY}`（JSON 为长度 2 的非负整数数组 `[x, y]`，由 `publicNodeClippedRect(child, screen)` 裁屏后再经 `tapSafeCenter` 得出，保证 `0 <= centerX < screen.Right` 且 `0 <= centerY < screen.Bottom`）；
  - `hasCenter == false`（[tap_scope.go:2473](../../internal/engine/tap_scope.go#L2473) 仅校验未裁屏正尺寸，完全离屏的正尺寸节点可真实到达此处并使 `publicNodeClippedRect` 返回空交集）时**必须省略**，绝不写 `null` 或 `[0, 0]`；
  - **严格排他**：`no_safe_point`、`invalid_target`、`ambiguous_point`、`no_safe_trajectory`、`target_missing_action`、`target_has_other_action`、`ancestor_missing_action`、`ancestor_invalid`、`axis_mismatch`、`not_scrollable` 均**不**输出 `target_center`；
  - **事实分层**：仅陈述目标自身在当前屏幕内的客观几何中心，不认领 `O`，不承诺坐标点击安全或业务有效；调用方据此发起 `act(tap/long_press, target={x, y})`（须省略 `bubble`）仍走独立坐标准入与认领后重验。
- **白名单登记**：在 [errors.go](../../internal/protocol/errors.go#L9-L28) 注释的 `Details` 白名单与 `docs/interface.md` 静态拒绝节正式登记 `target_center`。

### 4.3 MCP 13 工具目录与发现说明瘦身（议题 3）

- **量化验收判据**（以 [TestDiscoveryByteBudget](../../internal/mcp/schema_test.go#L1164-L1202) 同口径 UTF-8 字节计量，注意源码断言为严格小于 `total < baselineDiscoveryBytes`）：
  - `tools_list`（13 工具序列化 JSON 数组）：自 `27,369 B` 降至 **`<= 20,500 B`**（净降幅 **>= 25.1%**，目标区间 25%–35%，即 `~17,800–20,500 B`）；
  - `total`（`len(instructions) + len(toolsJSON)`）：自 `39,926 B` 降至 **`< 31,000 B`**；
  - `baselineDiscoveryBytes` 常量由 `40000` 收紧至 **`31000`**（防后续回弹）。
- **必须保留的语义守卫（单工具自包含防误用红线）**：
  1. **结构零缩水**：13 个工具名称、顺序、JSON Schema 全部类型/枚举/范围/`minLength`/`maxLength`/`pattern`/`required`/`additionalProperties: false`/`oneOf`/`not` 与 4 项 `readOnlyHint=true` 注解 100% 保持不变；
  2. **`treeTextGuide` 完整图例**：唯一发布于 `ServerInstructions()`，保留 `result v4`、`obs`（含同步后的 `sparse_tree` / `hint` 规则）、`diagnosis.error_counts=`、`app_items v1`/`app_tasks v1`、`tree v5`、`inspect v3`、3 个可解码应用示例（`TestDiscoveryTreeExamplesDecodeAndApply` 必过）以及 [catalog_test.go:36-47](../../internal/mcp/catalog_test.go#L36-L47) 的 `TestCatalogSnapshotCarriesResultV4Discovery` 断言；
  3. **单工具关键自包含边界**（即使宿主剥离 `serverInstructions` 也不缺）：
     - `start`：空对象、首次 `full`、从 `obs` 取 `O` 与 `screen=<W>x<H>@<display_id>`、重放不重获五分钟；
     - `status`：只读不续租、`state=`/`can_start=`/`blocking_operation_id=`/`recovery ref=`、`app` 变更保留定格摘要；
     - `observe`：`reference` 必填、`wait_ms`（0–10000）仅单次延迟、等条件选 `wait_for`、`full=true` 强制 full；
     - `snap`：供 `hint=snap` 只读核对、不读树/不消费 O/不发布新 O/不续租、重放无图摘要；
     - `wait_for`：`timeout` 1..40（`stable=true` 至少 2）、`query`/`absent_query` 任一成立、`stable=true` 内容稳定（忽略 bounds 与无动作叶文本）连续 3 帧、`wait.trigger=timeout` 发布新 O 仍成功、`elapsed_ms` 可超 `timeout` 至多约 35 秒；
     - `act`：6 分支参数、默认 `bubble=true`（3×3、视觉区锚定祖先、局部共位吸附）、坐标省略 `bubble` 与 `raw swipe` 不报 `ambiguous_point`、5 个静态 `422` 码不消费 O 及取首个 `candidates[].executable=true` 用同一 O 新 P 重试、`no_interactive_ancestor` 可附 `target_center`、`text` 默认 `auto_submit=true`/`submit_ref="enter_key"`（仅填字设 `auto_submit=false`，不另发 ENTER、`text_input`/`partial`/`uncertain` 避免原样重发按 `recovery` 观察）、`scroll` 四档 `amount`（step/half/page/full，不接受 `duration_ms`）与轴向回退 `axis_mismatch`、`target_changed` 附新 O 用新 O 重试、`change=`/`hint=` 短说明；
     - `inspect`：`node`/`query` 互斥、O 过期仍可能可读、补看 `act` 诊断 `ref`、`content_unavailable`/`inspection_too_large`；
     - `keepalive`：`duration_minutes` 1～120 默认 2、覆盖可缩短、不延 O 输入期限（60 秒）、终态码不重试；
     - `end`：先撤权再收尾、`cleanup` 常先 pending、不保证撤回已派发命令；
     - `app`：**完整保留 7-kind 矩阵（7 行独立分行）**与“接受其余已声明顶层成员但仅生效成员入服务”宽容说明、`list` 必带 `reference`、`kill` 整包单次 `force-stop` 与 `package_state` 分层字段、拒绝 `retry_parameter` 短提示；
     - `notify`：无需会话无 `reference`、`executed` 仅表示保存入队、`state=pending` 不重发；
     - `shell`：不绑定会话屏、驱动虚屏须带 `-d <display_id>`（取自 `start`/`snap`/`app(list)`）、`root=true`、`exit_code`、uncertain 后 `shell_pending`/`blocking_operation_id` 退避；
     - `diagnose`：只读不修复、无参有活动会话时聚焦当前会话并带 `error_counts`、`coverage=record_unavailable` 改用 `status`；
  4. **禁词门禁维持**：继续严禁 `quality`/`effect`/`unstable`/`observed`/`not_observed`/内部算法词（`Tier`/`default_center` 等）与历史废弃键（`retiredActDetailKeys`）进入发现文本。
- **删减方法**：① 删除 13 个工具 JSON Schema 顶层的机械复述 `"description"`；② 同一工具内每个参数约束只在 `tool.description` 或 `property.description` 出现一次（Schema 字段 `description` 收敛为 1 句短语，不重复 `enum`/`min`/`max`/`default` 已表达的结构约束）；③ 移除 `observe`/`wait_for`/`act`/`inspect` 中大段照抄 `treeTextGuide` 的 `base=_72` 算术步骤与 `inspect v3` 缩进细则；④ 去除 `structured 中为 ...` 与冗余过渡词；⑤ 同步精简 `callerGuidance` 中与工具描述完全重叠的长句。

### 4.4 `app.close` / `app.kill` 安全拒绝的 `retry_parameter`（议题 4）

- **白名单登记**：在 [errors.go](../../internal/protocol/errors.go#L9-L28) 登记 `retry_parameter`（受控非空字符串，仅允许声明存在的请求参数名）。
- **精确触发点（仅 2 处）**：
  1. [app_close.go:52](../../internal/engine/app_close.go#L52)：`target.ownership == TaskBorrowed && !req.AllowBorrowed` 返回 `409 close_refused_borrowed` 时附加 `.WithDetail("retry_parameter", "allow_borrowed")`；
  2. [app_kill.go:348](../../internal/engine/app_kill.go#L348)：`(services.Foreground || media.Playing) && !req.AllowStopPlayback` 返回 `409 kill_refused_playback` 时附加 `.WithDetail("retry_parameter", "allow_stop_playback")`。
- **严格排除点**：
  - [app_kill.go:345](../../internal/engine/app_kill.go#L345)（`serviceErr != nil || mediaErr != nil || !services.Known || !media.Known`）返回 `409 kill_refused_playback` 时**严禁**附加 `retry_parameter`；
  - 其余 `app` 拒绝（`kill_refused_not_owned`、`kill_refused_task_elsewhere`、`control_ui_on_virtual_display`、`root_unavailable`、`window_action_not_supported_on_primary_display`、`task_lifecycle_busy` 等）均不附加。
- **重放一致性**：因 `finishOperationLocked` 保存 `op.Error = apiErr.Public()` 且 `operationViewLocked` 使用 `protocol.CloneErrorDetails`，`status(P)` 与重复 `P` 自动保留相同的 `retry_parameter` 且不共享底层 map。

### 4.5 `status(P)` 与重复 `P` 的 `app` 变更定格摘要（议题 5）

1. **适用范围**：
   - **保留定格 `AppView` 的 5 种变更 kind**：`open`、`claim`、`handoff`、`close`、`kill`（含 `executed`、`uncertain`、`not_executed` 终态，各自只保留首次结果已证明的字段，不补造未证明字段）；
   - **继续仅返 `&protocol.AppView{Kind: op.AppKind}` 的情形**：
     - 查询 kind：`find`、`list`（永不缓存 `items`/`total`/`truncated`/`captured_at`/`foreground`）；
     - 进行中操作：`op.State == OperationRunning`；
     - 因 5 秒 `work_deadline` 退休冻结的终态（`op.Phase == "work_deadline"`，此时冻结时尚无已定格 `view`，迟到回调不得回填）；
     - 进程重启后从持久化账本重建的旧操作记录（内存无 `op.App`，绝不伪造）；
     - MCP 适配器未取得服务结果的传输失败/取消合成结果（按既定边界只从请求补 `app.kind`）。
2. **数据隔离与原子冻结**：
   - `operationState` 新增 `App *protocol.AppView`；
   - `cloneAppSummary(view *protocol.AppView) *protocol.AppView`：对 `view == nil` 或 `view.Kind ∈ {AppKindFind, AppKindList, ""}` 返回 `nil`；对其余 5 种变更 kind 完整深拷贝全部已设置字段——包括字符串（`Kind`/`Package`/`Ownership`/`ForegroundRestore`/`PackageState`）、全部 `*int`/`*bool` 标量指针（`DisplayID`/`TaskID`/`SourceDisplayID`/`AndroidUserID`/`SourceTaskID`/`Reused`/`StatePreserved`/`Complete`/`Restarted`）、全部三态与集合存在标志（`HasStatePreserved`/`HasRestarted`/`HasTasks`/`HasTargetTaskIDs`/`HasRemainingTaskIDs`/`HasTargetPIDs`/`HasKilledPIDs`/`HasRemainingPIDs`）、经 `slices.Clone` 保留非 nil 空切片语义的 5 个 `[]int` 切片、以及 `Tasks` 切片内每项的 `RecentsCleaned`/`HasRecentsCleaned` 与 `Error`（含 `protocol.CloneErrorDetails`），确保 `op.App`、首次返回、`status(P)` 与重复 `P` 互不共享可变切片或 map，且完整保持 `[]` vs 省略与 `null` vs 省略的三态边界；
   - 统一封装 `finishAppOperation(op, outcome, phase, apiErr, view)` 在 `e.mu` 内检查 `if op.State == OperationFinished { return op.Recovery }`，仅当本次赢得终态转换时执行 `op.App = cloneAppSummary(view)` 并调用 `e.finishOperationLocked(...)`；
   - `finishAppError`、`finishAppExecuted` 的全部 4 处终态出口（[app.go:231,236,245,248](../../internal/engine/app.go#L225-L250)）以及 `finishAppUncertainWorkAfter`（必须在 `after()` 钩子如 `reconcileOpenVirtualTask` 完成后）全部统一改调 `finishAppOperation`，确保含 `executed_revoked` 在内的所有正常终态与 `after()` 回填字段一并原子定格；
   - `operationViewLocked`：当 `op.State == OperationFinished && op.App != nil` 时设 `result.App = cloneAppSummary(op.App)`；否则若 `op.AppKind != ""` 设 `result.App = &protocol.AppView{Kind: op.AppKind}`。
3. **既有不附观察不变式**：`status(P)` 与重复 `P` 依然永不附带 `observation`，也不执行任何设备读。

### 4.6 `diagnose()` 当前会话过滤、去重 `issues` 与 `error_counts`（议题 6）

1. **请求面不变**：不新增任何请求参数，沿用 `{}` 与 `{"reference":"O…|P…"}`；`diagnoseReferenceLocked`（带 `reference` 查单条 O/P）行为与输出结构完全不变（`error_counts` 为空省略）。
2. **无参 `diagnoseOwnLocked` 会话作用域过滤**：
   - 判定当前未结束本 Agent 会话：`activeSession := e.session` 当且仅当 `e.session != nil && e.session.Agent == agent && e.session.State != SessionEnded`（覆盖 `starting` 与 `active`）；
   - 当 `activeSession != nil` 时，遍历 `e.operations`（已筛 `op.Agent == agent`）时仅保留满足以下任一条件的操作：
     - `op.SessionOrigin == activeSession.Origin`（含本会话内常规操作及活动态重复 `start` 拒绝）；或
     - `op.SessionOrigin == "" && !activeSession.StartedAt.IsZero() && !op.CreatedAt.Before(activeSession.StartedAt)`（捕获当前会话期间发生的 detached 操作如 `app.find`、`notify`、准入前被拒的 `snap`，并以 `!StartedAt.IsZero()` 防范零时间戳夹具导致历史操作回流）；
   - 当 `activeSession == nil`（尚无会话或会话已结束）时，保留本 Agent 全部近期操作（与既有行为 100% 兼容）；
   - 全局事实（`droppedUnconfirmed`、`availabilityLocked`、最近会话 `cleanup` 与 `timing_ms`）保持不变。
3. **错误码计数聚合 `DiagnosisView.ErrorCounts`**：
   - `protocol.DiagnosisView` 新增 `ErrorCounts map[string]int`（JSON 键 `"error_counts"`，位于 `issues` 之后、`timing_ms` 之前）；
   - 统计口径：仅在无参 `diagnoseOwnLocked` 中，对当前过滤窗口内**全部**失败或不确定操作（`op.State != OperationRunning && (op.Error != nil || op.Outcome == OutcomeUncertain)`）按非空控制码计数——有 `op.Error != nil && op.Error.Code != ""` 时计 `op.Error.Code`，否则若 `op.Outcome != ""` 计 `op.Outcome`（如 `"uncertain"`）；**不受 `maxDiagnosisEntries = 8` 截断影响**；
   - 空 map（0 条匹配失败/不确定操作）或 `diagnoseReferenceLocked` 路径下省略 `"error_counts"`（绝不写 `{}` 或 `null`）；
   - 序列化按 key 字典序稳定编码（`sort.Strings(keys)`），满足字节确定性；
   - 严格响应解码（[decode_response.go:978-1019](../../internal/protocol/decode_response.go#L978-L1019)）：`ErrorCounts member[map[string]int] \`json:"error_counts"\`` 拒收 `null`，并在存在时校验非空、每个键非空且计数 `> 0`（不接受空对象或非正计数）。
4. **操作级 `issues` 按 `(phase, code)` 去重**：
   - 在 `diagnoseOwnLocked` 中，全局高优先级问题（`retired_cleanup_dropped`、`availability`、`cleanup`）仍固定置顶；
   - 对 `failed` 切片按 `(operationFinishedAt, op.ID)` 升序确定性排序（解决随机序 map 遍历下同时间戳抖动），再从最新往前扫，用 `seenOpIssue[protocol.DiagnosisIssue]bool` 按 `(Phase, Code)` 去重：相同 `(phase, code)` 只占 1 个 `view.Issues` 槽位（保留最新不同问题），最多填满 `maxDiagnosisEntries = 8`；
   - **`truncated` 判定**：仅当“去重后的不同操作级 `(phase, code)` 种类数超过 `view.Issues` 剩余槽位”，或 `len(events) > maxDiagnosisEntries`（`timeline` 丢弃了较旧事件），或 `trimDiagnosisBudget` 触发字节预算裁剪时置 `view.Truncated = true`；
   - `DiagnosisIssue` 维持既有 `{phase, code}` 两字段结构不变；`timeline` 维持既有按时间升序保留最新 8 条事件不变。
5. **字节预算裁剪（`trimDiagnosisBudget`）**：
   - 维持 `maxDiagnosisBytes = 16 * 1024`，超限时沿用既有顺序优先裁剪：① 丢弃最旧 `Timeline[0]`；② 从队首裁剪 `Issues[0]`；③ 极端兜底清空 `ErrorCounts = nil`，每次裁剪均置 `Truncated = true`，保证输出永不越界。
6. **`result v4` 文本投影、双清单注册与 `mcp-probe` 独立解析**：
   - [internal/mcp/result_text.go](../../internal/mcp/result_text.go#L76-L80) 与 [result_text.go:2102-2105](../../internal/mcp/result_text.go#L2102-L2105)：**同时**在 `v4ResultFields` 注册 `"diagnosis.error_counts": v4JSON` 并在 `resultFields` 注册 `"diagnosis.error_counts": fieldJSON`（分别供 `TestResultFieldManifest` 与 `TestCanonicalJSONCoversManifest` 检查）；`renderDiagnosisV4` 在 `diagnosis.issues=...` 之后、`diagnosis.timing_ms=...` 之前渲染 `diagnosis.error_counts=<compactJSON>`（若值为 `null` 则回退 `json v1 path="/diagnosis/error_counts"`），并将 `"error_counts"` 加入已知字段跳过表；
   - [internal/mcp/result_manifest_test.go:1095-1108,1680-1723](../../internal/mcp/result_manifest_test.go#L1095-L1108)：在 `rmfResultFixtures` 的诊断夹具与 `rmfDeterministicResult` 中补充 `ErrorCounts`（后者按迭代轮换插入序以锁死字典序字节确定性）；
   - [scripts/mcp-probe/result_v4.go](../../scripts/mcp-probe/result_v4.go#L549-L563)：在节段 21 支持 `diagnosis.error_counts=` 并解码至 `["diagnosis", "error_counts"]`；[scripts/mcp-probe/result.go](../../scripts/mcp-probe/result.go#L124-L126) 的 `diagnosisView` 同步补充 `ErrorCounts map[string]int`；[scripts/mcp-probe/probe_test.go:1646-1684](../../scripts/mcp-probe/probe_test.go#L1646-L1684) 的 `writeFakeDiagnosis` 同步输出 `diagnosis.error_counts=`；
   - 严格满足 AGENTS.md #10 日志白名单：`error_counts` 只含批准控制码与正整数，绝不携带节点文本、包名、命令、日志或原始异常字符串。

---

## 5. 算法

### 5.1 观察发布与防循环提示路由（议题 1）

```text
func publishTreeAttempt(work, session, phase, tree, base, selection, status, image, quality, previousObs):
    // 1. 锁外计算 visual 分类（新阈值 sparse_tree: textRunes < 20 && interactive < 4）
    chosenKind, chosenRaw := observation.ClassifyVisual(tree.projection.Tree, &tree.capture, ...)
    // 2. 锁外计算基线 delta 与 previousObs 树对比（不变）
    ...
    // 3. 锁内提交门禁（STOP/撤权/显示代次/CaptureMaxAge/留存预算）
    e.mu.Lock()
    ... // 任一门禁失败或撤权直接解锁返回，零状态污染
    fromWaitFor := (phase == "wait_for")
    publishedVisual := observationVisual(delta != nil, {chosenKind, chosenRaw},
                                         e.previousVisualKindLocked(delta), fromWaitFor)
    publishedTreeChange := ... // 不变
    hasValidImage := (image != nil && image.Error == nil)
    publishedHint := observationHint(publishedVisual, publishedTreeChange, hasValidImage, fromWaitFor)
    ... // 写入 e.current / e.baselines / e.observations 并返回
```

时间轴示意（纯 Canvas 静止页防死循环）：

```text
act / app / start / observe
  └─ 首次命中 sparse_tree (<20 字且 <4 交互)
       └─ 发布 full O1: visual=sparse_tree hint=wait_for
            │
            ▼ 调用方执行 wait_for(O1, stable=true)
               ├─ 页面加载出内容 (64 节点) ─► 发布 O2: 无 visual / 无 hint (正常继续)
               └─ 仍是纯自绘空树 (delta changes=0)
                    └─ fromWaitFor=true 保留 visual=sparse_tree
                       └─ 发布 O2: visual=sparse_tree hint=snap (打破循环!)
```

异常分支行为：STOP/撤销/采集失败/超时/预算耗尽均按既有提交条件化直接丢弃候选、不发布观察、不写 `VisualKind`；无树失败观察恒省略 `visual`/`tree_change`/`hint`。

### 5.2 `app` 变更终态原子定格（议题 5）

```text
func (e *Engine) finishAppOperation(op *operationState, outcome, phase string,
                                    apiErr *protocol.APIError, view *protocol.AppView) *protocol.Recovery:
    e.mu.Lock()
    defer e.mu.Unlock()
    if op == nil { return nil }
    if op.State == OperationFinished {
        // 已被 work_deadline 或其他并发路径冻结，绝不覆盖 op.App 或终态
        return op.Recovery
    }
    op.App = cloneAppSummary(view) // 仅 open/claim/handoff/close/kill 返回深拷贝；find/list 返回 nil
    return e.finishOperationLocked(op, outcome, phase, apiErr)
```

- `finishAppError`、`finishAppExecuted` 的全部 4 处终态出口（[app.go:231,236,245,248](../../internal/engine/app.go#L225-L250)）与 `finishAppUncertainWorkAfter`（在 `after()` 执行完成后）全部统一改调 `finishAppOperation`；
- `finishAppQuerySuccess`、`finishAppQueryError`、`appQueryClosed`、`appQueryCancelled` 继续走 `finishOperation`（`op.App` 恒为 `nil`）；
- 若操作在运行中遇到 5 秒 `work_deadline` 冻结，冻结逻辑先置 `op.State = OperationFinished, op.Phase = "work_deadline"`（`op.App == nil`），迟到回调调 `finishAppOperation` 时短路返回，随后 `defer applyFrozenResult` 按 `operationViewLocked` 返回干净的仅含 `app.kind` 冻结视图。

### 5.3 `diagnoseOwnLocked` 会话过滤、去重与计数（议题 6）

```text
func (e *Engine) diagnoseOwnLocked(agent string, now time.Time) *protocol.DiagnosisView:
    // 1. 收集全局高优先级问题：droppedUnconfirmed / availability / cleanup（不变）
    // 2. 确定会话过滤窗口
    var activeSession *sessionState
    if e.session != nil && e.session.Agent == agent && e.session.State != SessionEnded {
        activeSession = e.session
    }
    // 3. 过滤同 Agent 操作
    for op in e.operations:
        if op.Agent != agent { continue }
        if activeSession != nil {
            inSession := (op.SessionOrigin == activeSession.Origin) ||
                         (op.SessionOrigin == "" && !activeSession.StartedAt.IsZero() &&
                          !op.CreatedAt.Before(activeSession.StartedAt))
            if !inSession { continue }
        }
        // 分流 running 与 failed (op.Error != nil || op.Outcome == OutcomeUncertain)
    // 4. 聚合全量 error_counts（不受 maxDiagnosisEntries=8 截断；仅计非空受控码）
    counts := map[string]int{}
    for op in failed:
        code := op.Outcome
        if op.Error != nil && op.Error.Code != "" { code = op.Error.Code }
        if code != "" { counts[code]++ }
    if len(counts) > 0 { view.ErrorCounts = counts }
    // 5. failed 按 (operationFinishedAt, op.ID) 升序确定性排序，倒序按 (phase, code) 去重填入 view.Issues（上限 8）
    seen := map[protocol.DiagnosisIssue]bool{}
    uniqueOverflow := false
    for i := len(failed)-1; i >= 0; i--:
        code := failed[i].Outcome
        if failed[i].Error != nil && failed[i].Error.Code != "" { code = failed[i].Error.Code }
        issue := {Phase: failed[i].Phase, Code: code}
        if seen[issue] { continue }
        seen[issue] = true
        if len(view.Issues) < maxDiagnosisEntries {
            view.Issues = append(view.Issues, issue)
        } else {
            uniqueOverflow = true
        }
    if uniqueOverflow { view.Truncated = true }
    // 6. 构建 timeline（按时间升序取最新 8 条；溢出置 Truncated=true）并设置 Summary
    // 7. 锁外 trimDiagnosisBudget(view, 16 KiB)：先裁 Timeline[0]，再裁 Issues[0]，最后兜底清 ErrorCounts
```

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| :--- | :--- |
| `release/versions.json` 及 4 个生成版本文件 | 升至 `release_version="0.39.0"`、`contract_version=35`、`android_version_code=41`，运行 `scripts/generate-version.ps1` 同步 Go/Java/XML 常量 |
| `internal/observation/visual.go`、`visual_test.go` | `visualSparseMaxTextRunes` 改为 `20`、`visualSparseMaxInteractive` 改为 `4`；同步 `visual_test.go`（含 L306-308、L334-335、L355-374、L391-404 夹具下调至 `<20/<4`）并补充边界单测（覆盖 `O77` 6 交互/26 字弹窗不误报、`O85/O91/O132` 空树命中及边界值 19/20、3/4） |
| `internal/engine/capture.go`、`observation_hint_test.go`、`visual_test.go`、`quality_test.go` | `observationVisual` 与 `observationHint` 增加 `fromWaitFor bool` 参数；在 `publishTreeAttempt` 按 `phase == "wait_for"` 传入；更新既有测试（含 `observation_hint_test.go:27,110`、`visual_test.go:144,160,268` 与 `quality_test.go:268`）并补齐 `wait_for` 终帧 `delta` 仍稀疏回落 `hint=snap`、首次稀疏带图仍出 `hint=wait_for` 等回归与反向对照测试 |
| `internal/engine/tap_scope.go`、`tap_static_error_test.go`、`act_tap_scope_test.go` | `planBubbleOrColocated` 的 `bubbleStepANoAncestor` 分支在 `hasCenter` 为真时附加 `target_center: []int{centerX, centerY}`（`text(submit_ref)` 复用生效）；更新并新增单测验证隐藏坐标被动文本输出精确裁剪中心、完全离屏正尺寸节点省略 `target_center` 且原 `O` 保持 `ready`，反向验证 `no_safe_point`/`target_missing_action` 不附 `target_center` |
| `internal/protocol/errors.go`、`surface.go`、`decode_response.go` 及协议单测 | `errors.go` Details 白名单登记 `target_center` 与 `retry_parameter`；`DiagnosisView` 新增 `ErrorCounts map[string]int`（按 key 字典序序列化、空时省略；严格解码拒 `null`/空对象/空键/非正计数）；补齐编解码往返与非法值单测 |
| `internal/engine/app_close.go`、`app_kill.go`、`app_kill_v31_test.go`、`app_lifecycle_test.go` | `app_close.go:52` 附 `retry_parameter="allow_borrowed"`，`app_kill.go:348` 附 `retry_parameter="allow_stop_playback"`，`app_kill.go:345` 保持省略；补齐回归与反向对照测试 |
| `internal/engine/engine.go`、`app.go`、`app_lifecycle_test.go`、`app_kill_v31_test.go` | `operationState` 新增 `App *protocol.AppView`；在 `app.go` 新增 `cloneAppSummary` 与 `finishAppOperation`（复用 `operations.go` 既有 `finishOperationLocked`，无需改 `operations.go`）；更新 `finishAppError`、`finishAppExecuted` 全部 4 处退出点、`finishAppUncertainWorkAfter` 与 `operationViewLocked`；更新 `TestAppDuplicatePRepublishesOnlyKind` 并新增 5 种变更 `status(P)`/重复 `P` 重放、`find`/`list` 与 `work_deadline` 冻结不泄漏、三态/空切片与深拷贝隔离等单测 |
| `internal/engine/diagnose.go`、`readonly_test.go` | `diagnoseOwnLocked` 实现活动会话过滤（含会话期 detached 操作与 `!StartedAt.IsZero()` 守卫）、`(operationFinishedAt, op.ID)` 确定性排序、`(phase, code)` 去重与全量 `ErrorCounts` 聚合；`trimDiagnosisBudget` 增加 `ErrorCounts` 兜底裁剪；新增跨会话隔离、会话期 detached 保留、零时间戳防护、无活动会话回退、`uniqueOverflow` 真反向对照与完整计数等单测 |
| `internal/mcp/result_text.go`、`result_text_test.go`、`result_text_v4_test.go`、`result_manifest_test.go` | 在 `result_text.go` 的 `v4ResultFields`（L77）与 `resultFields`（L2103）双表注册 `diagnosis.error_counts`；`renderDiagnosisV4` 在 `diagnosis.issues=` 后输出 `diagnosis.error_counts=` 并更新跳过表；同步 `decodeResultText` 与 `result_manifest_test.go:1095-1108,1680-1723` 夹具 |
| `internal/mcp/schema.go`、`schema_test.go`、`catalog_test.go`、`result_parity_test.go` | 按 §4.3 精简 13 工具描述、字段 Schema 描述与 `callerGuidance`，同步新增契约项说明；收紧 `baselineDiscoveryBytes = 31000`；重构绑定冗长旧句式的子串断言为语义守卫测试，守住 `catalog_test.go:36-47` 并刷新 `result_parity_test.go:554` 硬编码 SHA-256 目录指纹 |
| `scripts/mcp-probe/result.go`、`result_v4.go`、`result_modes_test.go`、`probe_test.go`、`catalog.go` | `result_v4.go` 支持 `diagnosis.error_counts=` 解析与双模一致性校验；`result.go` 的 `diagnosisView` 增 `ErrorCounts`；`probe_test.go:1646-1684` 的 `writeFakeDiagnosis` 同步输出 `diagnosis.error_counts=`；验证目录与指纹一致性 |
| `README.md` 与 `docs/{interface.md,mcp.md,architecture.md,client.md,control-protocol.md,integration.md,phone-operations.md,release.md,runbook.md}` | 同步契约 `35`、`0.39.0` 与六项新行为规约（`docs/mcp.md` 同步语义与 `result v4` 投影说明，工具目录指纹由清单与 `result_parity_test.go:554` 校验），提交前通过 AGENTS.md 第 15 条独立只读文档审计 |

### 6.2 配置与取值

所有新增/调整阈值均为编译期确定性常量，不增加配置项或请求参数：

| 常量 | 变更前 | 变更后 | 依据 |
| :--- | :---: | :---: | :--- |
| `visualSparseMaxTextRunes` | `30` | `20` | 排除 26 字完整弹窗（`O77`）等轻模态层，精准匹配 0–19 字空树/加载过渡页（`O85`/`O91`/`O132`） |
| `visualSparseMaxInteractive` | `7` | `4` | 排除 4–6 交互控件的完整小弹窗；常规 4–6 节点视频/直播初始页已由 `loading`/`media_surface` 承接 |
| `baselineDiscoveryBytes` | `40000` | `31000` | 压减 `tools_list` 25%–35%（`<= 20,500 B`）并微瘦 `callerGuidance` 后设防回弹门禁 |
| `maxDiagnosisEntries` / `maxDiagnosisBytes` | `8` / `16 KiB` | `8` / `16 KiB`（不变） | 去重 `issues` + 新增 `error_counts` 已在既有有界容量内解决重复挤占问题 |

### 6.3 日志与可观测性

- **严守 AGENTS.md 第 10 条日志白名单**：不新增日志事件，不扩充 `internal/eventlog/log.go` 白名单键；
- `target_center`（两整数坐标）与 `retry_parameter`（固定参数名）只进受控响应 `error.details`，不进日志；
- `diagnosis.error_counts` 只含白名单错误码/终态枚举与正整数计数，严禁进入任何节点文本、包名、命令、输出或异常字符串。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| :--- | :--- |
| **#1 唯一权威 / #3 单向消费 / #4 条件化提交** | 不变：全部计算仍在 `internal/engine` 与 `internal/observation` 内；`no_interactive_ancestor` 保持静态拒绝不认领 `O`；`op.App` 仅在终态锁内原子冻结 |
| **#2 STOP 不等待 / #5 状态锁短小** | 不变：零新增设备 I/O 或等待；`cloneAppSummary` 与 `diagnoseOwnLocked` 均为有限内存 O(N) 扫描（操作数受 `MaxOperations` 有界） |
| **#6 严格接入 / #7 三态省略** | 不变：不放宽任何请求校验；`AppView` 深拷贝完整保留 `HasStatePreserved`/`StatePreserved`、`HasRestarted`/`Restarted` 与 `HasRecentsCleaned`/`RecentsCleaned` 三态 null 语义；`DiagnosisView.ErrorCounts` 严拒显式 `null` |
| **#8 事实分层** | 强化：`target_center` 明示客观几何而非安全点证明；`app_kill.go:345` 未知态不伪造 `retry_parameter`；`status(P)` 仅重放已证明定格事实、不附观察也不伪造重启前缺失记录 |
| **#10 日志白名单 / #14 消息边界** | 不变：不泄露正文或敏感字段；`notify` 边界完全不变 |
| **#13 单一版本源 / #15 文档审计 / #19 发布交付** | 严格按单一版本源升契约 35，提交前由独立只读 subagent 审计全部受影响 `docs/`，发布执行完整三项默认交付 |

### 6.5 契约与文档影响

- **共同契约变化**：`contract_version` 由 `34` 升至 `35`，`release_version` 由 `0.38.0` 升至 `0.39.0`，`android_version_code` 由 `40` 升至 `41`（Java 控制 App 仅同步版本常量与清单契约号，不改业务行为）。
- **需同步的正式文档**：
  - [docs/interface.md](../../docs/interface.md)：更新 `sparse_tree` 阈值与 `delta`/`hint` 规则、`unsupported_node_action (no_interactive_ancestor)` 的 `target_center`、`app` 拒绝的 `retry_parameter`、`status(P)`/重复 `P` 的 `app` 变更摘要边界、`diagnose` 当前会话过滤/去重/`error_counts` 及版本号；
  - [docs/mcp.md](../../docs/mcp.md)：更新 `result v4` 的 `diagnosis.error_counts=` 行、`obs` 行 `hint` 规则、`app` 重放摘要口径与工具发现瘦身说明（字面 SHA-256 指纹仅在 `result_parity_test.go:554` 与发布清单中核验）；
  - [docs/architecture.md](../../docs/architecture.md)、[docs/client.md](../../docs/client.md)、[docs/control-protocol.md](../../docs/control-protocol.md)、[docs/integration.md](../../docs/integration.md)、[docs/phone-operations.md](../../docs/phone-operations.md)、[docs/release.md](../../docs/release.md)、[docs/runbook.md](../../docs/runbook.md) 与 [README.md](../../README.md)：同步 `0.39.0` / 契约 `35` 版本表述及对应行为/示例，确保零漂移。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. 消除 `O77` 类 4–6 交互控件、20–29 字完整模态弹窗的 `sparse_tree` 误报，并使空树过渡页首次给出正确的 `hint=wait_for`、`wait_for` 后仍稀疏时自动转 `hint=snap`；
2. 遇 WebView/Hybrid 隐藏坐标被动文本项报 `no_interactive_ancestor` 且无可用候选时，单次报错即可凭 `target_center` 直接发起坐标点按，省去额外 `inspect` 或试错；
3. 13 个 MCP 工具 `tools/list` 瘦身 25%–35%，释放 ~9 KB 发现预算余量并显著削减全会话每步固定上下文税（按 `10ca6f7e` 143 步折算预计节省 ~28 万–39 万 tok-step）；
4. `app.close`/`app.kill` 安全拒绝直接给出可执行覆写参数名，且准确隔离不可靠参数越过的未知态；
5. `status(P)` 与重复 `P` 查 5 种 `app` 变更可直接拿到 `pkg`/`task_id`/`target_task_ids`/`killed_pids`/`package_state`/`complete` 等核心定格事实；
6. 活动会话期间无参 `diagnose()` 不再被上场旧会话历史污染，重复同类错误不再刷爆 8 槽并附带完整 `error_counts`。

### 7.2 不能治什么

| 现象 | 原因 |
| :--- | :--- |
| 宿主侧开局全量读文档与历史观察树在长对话中的累积暴露 | 属研究报告阶段 1（宿主侧上下文治理），不在服务/MCP 协议职权范围内 |
| `tree v5` 单帧节点文本进一步微剪枝（`N1`/`N2a`/`N3`）与 `inspect(query)` 20 层完整 `detailed` 路径 | 本次明确不改公开树剪枝与 `detailed` 树完整层级语义 |
| 无 `ProgressBar`/无 `SurfaceView`/非 `unstable` 且恰好有 4–6 交互控件的极少数自定义视频初始页 | 阈值收紧至 `<20 && <4` 后不再标 `sparse_tree`，优先保证不误报完整小弹窗 |
| 纯自绘静止页首帧先收到 1 次 `hint=wait_for` | 无像素/渲染引擎内部信号时，首帧无法区分“刚切页加载白屏”与“纯自绘静止页”，需 1 次 `wait_for` 终帧自动转 `hint=snap` |
| `target_center` 坐标正上方存在无关悬浮遮挡控件 | `target_center` 仅是目标裁剪几何中心，不替代坐标准入安全证明，后续坐标点击仍按坐标规则校验 |
| 进程重启后查重启前已结束 `app` 变更的 `status(P)` 详情 | `op.App` 为内存定格快照、不写入恢复账本，重启后诚实退回仅 `app.kind` |
| 活动会话期间无参 `diagnose({})` 一次拉取上一场已结束会话的失败明细 | 无参默认聚焦当前会话；跨会话排查历史记录须传旧 `O`/`P` 给 `diagnose({reference})` |
| 下游宿主对工具 Schema 的二次改写、系统提示缓存与托管 MCP 进程重连 | 宿主投影策略与进程重连归宿主/用户侧控制，服务端只保证原始 MCP 目录准确与紧凑 |

### 7.3 成本

- **CPU/内存**：`sparse_tree`/`hint`/`target_center`/`retry_parameter` 零额外开销；`op.App` 每条已完成 `app` 变更记录多占约几百字节内存（总操作数受 `MaxOperations` 有界）；`diagnoseOwnLocked` 仍在锁内单趟 O(N) 扫描 `e.operations`；
- **设备 I/O**：全部 6 项均为 **0 次新增设备读取**；
- **发布/升级成本**：升级共同契约 `34 → 35` 并刷新 MCP 目录指纹，需按默认发布范围完成手机整组覆盖部署与 Windows 主机二进制替换，并在用户侧重启宿主 MCP 进程以重新发现精简后的工具目录。

---

## 8. 验证计划

### 8.1 单元测试

所有测试启用 `GOEXPERIMENT=jsonv2`，由主代理亲自执行；每项新增判据均配至少一条**反向对照**用例（去掉实现即失败）：

| 模块 / 用例 | 期望与反向对照 |
| :--- | :--- |
| `internal/observation`：`visual_test.go` | ① 模拟 `O77`（26 字 / 6 交互）不命中 `sparse_tree`（反向对照：旧阈值 30/7 会误报）；② 覆盖 19 字/3 交互命中、20 字或 4 交互边界不命中、`O85`/`O91`/`O132` 空树全命中，并同步下调 `TestClassifyVisualLoading`/`RawCaptureFilters`/`MediaSurface`/`Priority` 夹具至 `<20 && <4` |
| `internal/engine`：`observation_hint_test.go`、`visual_test.go`、`quality_test.go`、`wait_for_test.go` | ① 非 `wait_for` 首次 `sparse_tree`（含带图与不带图，覆盖 `start`/`observe`/`app open`/`act.delivery`）输出 `hint=wait_for`；② `act` 后 `sparse_tree + unchanged/detail_only` 无图输出 `hint=snap`、有图抑制 `snap`；③ **`wait_for` 终帧 `delta changes=0` 仍为 `sparse_tree`** 时保留 `visual=sparse_tree` 且无图输出 `hint=snap`、有图抑制 `snap`（反向对照：去掉 `fromWaitFor` 免省略则 `visual`/`hint` 均丢失） |
| `internal/engine`：`tap_static_error_test.go`、`act_tap_scope_test.go` | ① 隐藏坐标被动文本无交互祖先触发 `422 unsupported_node_action (reason=no_interactive_ancestor)`（含 `text(submit_ref)` 复用路径）时返回精确屏内裁剪 `target_center: [x, y]` 且原 `O` 保持 `ready`；② 完全离屏正尺寸节点到达 `no_interactive_ancestor` 时 `hasCenter == false` 省略 `target_center`；③ 反向对照：`no_safe_point`、`target_missing_action`（`bubble=false`）、`ancestor_missing_action` 均不附 `target_center` |
| `internal/engine`：`app_lifecycle_test.go`、`app_kill_v31_test.go` | ① `close_refused_borrowed` 带 `details.retry_parameter="allow_borrowed"`、`kill_refused_playback`（已知播放/前台服务）带 `details.retry_parameter="allow_stop_playback"`，且 `status(P)` 一致重放；② 反向对照：`app_kill.go:345` 未知状态 `kill_refused_playback` **不含** `retry_parameter`；③ 5 种变更 kind（`open`/`claim`/`handoff`/`close`/`kill`，含 `executed_revoked` 路径与 `kill` 空切片 `[]` vs 省略三态）的 `status(P)` 与重复 `P` 完整重放定格 `AppView` 且深拷贝隔离（改返回副本不污染后续 `status(P)`）、不附 `observation`；④ `find`/`list`、`OperationRunning` 与 `work_deadline` 冻结后迟到回调保持仅 `app.kind` |
| `internal/engine`、`internal/protocol`：`readonly_test.go`、`decode_response_test.go` | ① 构造两场连续会话（旧会话失败 + 新会话活跃）：无参 `diagnose()` 仅含当前会话及期间 detached 失败，不含旧会话失败；零时间戳 `StartedAt.IsZero()` 下不回流历史 detached 操作；结束后无参回退展示近期记录；传旧 `reference` 仍查原记录；② 同 `(phase, code)` 发生 10 次时 `len(issues)==1`、`error_counts[code]==10`、`truncated==true`（来自 `timeline` 上限 8）；③ **`uniqueOverflow` 真反向对照**：1 条全局问题（如 `availability`）+ 8 条不同 `(phase, code)` 的失败操作（使 `len(events)==8` 不触发 `timeline` 截断，仅 `issues` 溢出 8 槽置 `truncated=true`，去掉 `uniqueOverflow` 必红），且 `error_counts` 仍全量准确；④ `trimDiagnosisBudget` 小预算下依次裁 `Timeline[0]` → `Issues[0]` → `ErrorCounts` 并置 `truncated=true`；⑤ `Decode` 严格拒收 `error_counts: null`、空对象、空键与 `<=0` 计数 |
| `internal/mcp`：`schema_test.go`、`catalog_test.go`、`result_text_v4_test.go`、`result_manifest_test.go`、`result_parity_test.go` | ① `TestDiscoveryByteBudget` 实证 `tools_list <= 20,500 B` 且 `total < 31,000 B`；② `TestDiscoveryTreeExamplesDecodeAndApply`、`TestCatalogSnapshotCarriesResultV4Discovery`、`TestAppDescriptionPublishesBranchMatrix`、`TestCatalogFingerprintBaseline`、单工具自包含关键语义守卫与禁词门禁全绿；③ `diagnosis.error_counts=` 的 `result v4` 双向编解码、`both`/`text`/`structured` 一致性、`TestResultFieldManifest` 与 `TestCanonicalJSONCoversManifest`（含 `rmfDeterministicResult` 轮换序）全通过 |
| `scripts/mcp-probe`：`go test ./...` | 独立验证 `diagnosis.error_counts=` 解析（含 `writeFakeDiagnosis`）、双模一致性与目录/只读标记/指纹校验 |

代码与文档收敛后，由主代理亲自集中运行全量 Go 测试（含引擎测试，单次命令超时设 15 分钟）与 `linux/arm64`、`CGO_ENABLED=0` 交叉构建检查。

### 8.2 实机 / 系统级与发布交付（AGENTS.md 第 19、24 条默认范围）

按默认发布交付范围，全部由主代理亲自执行并如实分层记录（区分“已实现 / 已单测 / 磁盘替换 / 独立进程验证 / 实机实测 / 依赖方由用户处理”）：

1. **同一源码提交整组构建与双清单核验**：
   - 手机发布包：运行 `./scripts/build-release.ps1` 构建 `device-service`、`device-mcp`、控制 App APK、`u2.jar` 与 `build/release/manifest.json`，再运行 `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` 核验全部 6 项产物、共同契约 `35`、`0.39.0`、SHA-256 与工具目录指纹；
   - Windows 主机工具：运行 `./scripts/build-host-tools.ps1` 构建并替换 `build/device-mcp.exe`、`build/mcp-probe.exe` 与 `build/host-tools-manifest.json`，再运行 `./scripts/build-host-tools.ps1 -VerifyOnly` 核验主机清单并与 `build/release/manifest.json` 交叉校对源码 commit、契约号与工具目录指纹。
2. **手机整组覆盖部署**：
   - 双清单核验通过后，运行 `./scripts/deploy.ps1` 覆盖部署手机服务、手机 MCP 适配器与控制 App（**保留 App 私有数据，严禁卸载/清库**），确认 `GET /health` 返回 `release_version=0.39.0`、`contract_version=35`。
3. **双入口独立探针验证**：
   - 使用本次新构建的 `build/mcp-probe.exe`（传入 `-manifest build/release/manifest.json -manifest build/host-tools-manifest.json`）分别针对 **Windows 主机 stdio（`build/device-mcp.exe`）** 与 **手机 Streamable HTTP MCP** 运行独立探针，核验 13 工具、`readOnlyHint`、精简后发现说明与新工具目录指纹、以及 `result v4` 与 `diagnosis` 投影。
4. **覆盖变更行为的最小实机闭环**：
   - 验证正常页面/轻弹窗不误报 `sparse_tree`，空树过渡页按新 `hint` 路由响应；
   - 触发一次 `app` 变更（如 `open`/`close`/`kill`），实测安全拒绝（若构造出 `close_refused_borrowed` 或已知播放 `kill_refused_playback`）的 `retry_parameter`，并调用 `status(P)` 实测返回定格 `app` 摘要而非裸 `app <kind>`；
   - 在活动会话内调用 `diagnose({})` 实测仅聚焦当前会话并正确呈现 `error_counts`（无失败时省略，有受控静态/业务拒绝时精确计数）；
   - 如能在实机界面触发无交互祖先被动文本，实测 `no_interactive_ancestor` 返回 `target_center` 并可凭该坐标独立点击（若当次界面未自然出现该类 WebView 节点，则在验证记录中诚实注明“已单测验证，实机未自然触发该类无交互祖先页面”）。
5. **责任边界**：
   - 独立探针只证明本次磁盘替换后的新产物与服务/HTTP/stdio 入口；依赖本 MCP 的外部 Agent 配置、其托管 MCP 进程重启与重连默认由用户处理，不作为本仓库交付门禁。

### 8.3 记录

实现提交与实机交付完成后，先提交实现（含经独立只读审计 clean 的 `docs/`），再单独提交归档：将本提案移至 `maintenance/proposals/implemented/signal-precision-schema-slimming-and-diagnostics.md`、重写相对链接并做断链检查、补写 §11，同时写入 `maintenance/verification/signal-precision-schema-slimming-and-diagnostics.md` 并按需更新 `maintenance/status.md`。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| :--- | :--- |
| 打包范围 | 阶段 3（3 项）+ 阶段 4（3 项，不含微剪枝与 `inspect` path）合并为一个提案，随契约 `35`（`0.39.0` / `versionCode 41`）一次发布（用户已确认） |
| 议题 1 路线与阈值 | 选 **1C**：`sparse_tree` 收紧至 `textRunes < 20 && interactive < 4`；非 `wait_for` 终帧且无无树变化时 `sparse_tree -> hint=wait_for`；`wait_for` 终帧 `delta` 保留 `visual=sparse_tree` 并于 `!hasValidImage` 回落 `hint=snap` |
| 议题 2 路线与字段 | 选 **2B**：仅 `422 unsupported_node_action (reason=no_interactive_ancestor)` 且 `hasCenter` 为真时附客观字段 `error.details.target_center: [x, y]` |
| 议题 3 路线与门禁 | 选 **3B**：去重 + 电报化压缩 `tools/list` 25%–35%（`<= 20,500 B`）、`total < 31,000 B`，收紧 `baselineDiscoveryBytes = 31000`，保留单工具自包含防误用红线、`catalog_test.go:36-47` 断言与 `app` 7-kind 矩阵 |
| 议题 4 路线与边界 | 选 **4C**：`error.details.retry_parameter` 仅附于 `app_close.go:52`（`"allow_borrowed"`）与 `app_kill.go:348`（`"allow_stop_playback"`），`app_kill.go:345` 未知态明确省略 |
| 议题 5 路线与边界 | 选 **5B-1**：5 种变更 `app` 在终态锁内原子保存深拷贝 `AppView` 供 `status(P)` 与重复 `P` 重放；`find`/`list`、`running`、`work_deadline` 冻结及重启后恢复记录保持仅 `app.kind` |
| 议题 6 路线与结构 | 选 **6A-1 + 去重 `issues` + `error_counts`**：零新增请求参数，有未结束本 Agent 会话时默认聚焦当前会话（含期间 detached 操作）；操作级 `issues` 按 `(phase, code)` 去重；新增可选 `DiagnosisView.ErrorCounts`（`result v4` 行 `diagnosis.error_counts=<JSON>`） |
| 发布交付范围 | 执行 AGENTS.md 第 19 条默认三项交付（手机整组覆盖部署留数据、最小实机闭环、Windows 两个 exe 替换 + 独立探针双入口验证），依赖方重连由用户处理 |

### 9.2 未决

- **本提案确认**：需经独立只读 subagent 审阅草案并收敛发现项后，呈交用户最终确认“按此提案实施”（确认前不进入代码实现）。除此之外无悬空设计未决项。

---

## 10. 实施顺序

1. **版本源升级**：修改 `release/versions.json` 为 `0.39.0` / `contract_version=35` / `android_version_code=41`，执行 `scripts/generate-version.ps1` 生成 4 个版本文件。
2. **协议层扩展（与步骤 3 可部分并行）**：
   - 更新 `internal/protocol/{errors.go,surface.go,decode_response.go}` 登记 `target_center`/`retry_parameter`、新增 `DiagnosisView.ErrorCounts` 编解码与单测。
3. **观察层与引擎层实现（可按模块并行开发后汇合单测）**：
   - 议题 1：改 `internal/observation/visual.go` 与 `internal/engine/capture.go`（`observationVisual`/`observationHint` 防循环路由）及对应单测；
   - 议题 2：改 `internal/engine/tap_scope.go`（`target_center`）及 `act_tap_scope_test.go`；
   - 议题 4 & 5：改 `internal/engine/{app_close.go,app_kill.go,app.go,engine.go}`（`retry_parameter`、`cloneAppSummary`、`finishAppOperation`、`operationViewLocked`）及对应单测；
   - 议题 6：改 `internal/engine/diagnose.go`（当前会话过滤、去重、`ErrorCounts`、预算裁剪）及 `readonly_test.go`。
4. **MCP 投影、目录瘦身与探针同步**：
   - 更新 `internal/mcp/result_text.go`（`v4ResultFields` L77 与 `resultFields` L2103 双表注册及渲染）与 `result_text*`/`result_manifest_test.go`（L1095-1108、L1680-1723 夹具）支持 `diagnosis.error_counts=`；
   - 按 §4.3 精简 `internal/mcp/schema.go`，收紧 `baselineDiscoveryBytes = 31000`，更新 `schema_test.go`/`catalog_test.go` 并刷新 `result_parity_test.go:554` 新工具目录 SHA-256 指纹；
   - 同步 `scripts/mcp-probe/{result.go,result_v4.go,catalog.go,probe_test.go}` 及探针测试。
5. **集中回归与文档同步**：
   - 主代理亲自运行全量 Go 测试（含引擎测试，超时 15 分钟）与 `linux/arm64` 交叉构建；
   - 同步更新 `README.md` 与 `docs/` 全部受影响正式文档（确保契约 `35`、`0.39.0` 与全部行为规约零漂移）。
6. **独立只读文档审计门禁**：
   - 发起独立只读文档审计 subagent 审核所有改动的 `docs/`，等待结论、修正全部发现项并复检至 clean。
7. **实现提交**：
   - 提交全部代码、测试与 `docs/` 改动（此时本提案仍保持“提案，未实现”），获得实现提交 hash。
8. **整组发布交付（由主代理亲自执行）**：
   - 从该干净实现提交依次运行 `./scripts/build-release.ps1`、`./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`、`./scripts/build-host-tools.ps1`、`./scripts/build-host-tools.ps1 -VerifyOnly`；
   - 运行 `./scripts/deploy.ps1` 覆盖部署手机整组产物（保留 App 私有数据）并运行最小实机闭环；
   - 用新 `build/mcp-probe.exe`（带 `-manifest build/release/manifest.json -manifest build/host-tools-manifest.json`）分别验证主机 stdio 与手机 HTTP MCP。
9. **提案归档提交**：
   - 移动本提案至 `maintenance/proposals/implemented/`、重写全部相对链接并校验无断链、更新状态头与 §11、写入 `maintenance/verification/signal-precision-schema-slimming-and-diagnostics.md` 并更新 `maintenance/status.md`，独立提交归档。

---

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/signal-precision-schema-slimming-and-diagnostics.md`
（本节在归档移动之后补写，届时文件在 `implemented/` 下，故链接比新建时多一层）。

### 11.1 已落地

| 提案条目 | 落点 |
| :--- | :--- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| :--- | :--- |

---

## 附：本提案不改变的事项

- 研究报告阶段 1 的宿主侧治理（速查卡、DSH 历史树滑窗折叠、非移动宿主工具裁剪）；
- 公开树紧凑投影与微剪枝规则（不做 `N1`/`N2a`/`N2b`/`N3`），`tree v5` 块头与节点/差分语法，`match.go` 跨帧保号，多基线池 `(12, 10, 5) @ 80%`；
- `inspect` 完整 `view=detailed` 语义与 `inspect v3` 语法；
- 所有 13 个 `/api/*` 端点的请求参数集合、严格 JSON 接入校验与 `app` 7-kind 扁平 Schema 结构约束；
- `protocol.Recovery` 6 元状态机、`Observation.tree_change` 判定、`act` 聚焦/重验/`scroll` 遮罩逻辑（契约 34 成果保持原样）；
- `status(P)` 与重复 `P` 永不附带 `observation`、`find`/`list` 永不缓存列表、进程重启后不伪造未持久化内存字段的边界；
- 固定制品 `third_party/uiautomator/u2.jar`、`internal/eventlog` 日志白名单键集合、`notify` 消息边界与 Java 控制 App 业务行为（仅同步版本常量）。
