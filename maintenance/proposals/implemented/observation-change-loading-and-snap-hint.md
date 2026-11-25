# 提案：动作树变化事实、加载与媒体视觉分类及观察级 snap/wait_for 提示（契约 30）

状态：已实现（实现提交 `31019462c98bb57d641caacd21aec0ad7dcf30fc`，`2026-11-25`）。
实现与验证记录：[observation-change-loading-and-snap-hint.md](../../verification/observation-change-loading-and-snap-hint.md)。
基线提交：`2628b106a14b661479236f7045deb43552f0ecd8`（分支 `master`，`2026-11-25`）。
适用范围：`act` 动作后树变化客观判定（`internal/engine`）、页面加载/媒体面/稀疏树视觉分类（`internal/observation`）、观察级只读提示 `tree_change`/`hint` 与 `visual` 扩充（`internal/protocol`、`internal/engine`）、MCP `result v4` `obs` 行投影与发现说明/工具描述（`internal/mcp`）、独立探针（`scripts/mcp-probe`）、正式文档（`docs/`）、整组发布交付以及 GitHub Issue `#44`/`#45`/`#46` 推送与关闭收口。
影响范围：`release/versions.json`、4 个生成版本文件、`internal/protocol`、`internal/observation`、`internal/engine`、`internal/mcp`、`scripts/mcp-probe`、`README.md` 及相关 `docs/` 正式文档。
契约（`/api/*` 字段与 `contract_version`）：改变（共同 `contract_version` 自 `29` 升至 `30`，`release_version` 自 `0.33.0` 升至 `0.34.0`，`android_version_code` 自 `35` 升至 `36`：`Observation.visual` 闭集扩充为 `loading`、`image_heavy(<pct>%)`、`media_surface`、`sparse_tree`，且 `loading` 在 `delta` 中不省略；`Observation` 新增可选非可空字符串成员 `tree_change`（`unchanged`/`detail_only`/`matched_base`）与 `hint`（`snap`/`wait_for`）；`result v4` 的 `obs` 行在 `visual=` 后新增可选 `change=<token>` 与 `hint=<token>`，外壳版本 `result v4` 与普通树块头 `tree v5` 保持不变）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/observation/visual.go`、`internal/observation/read.go`、`internal/observation/build.go`、`internal/protocol/responses.go`、`internal/protocol/decode_response.go`、`internal/protocol/read_tree.go`、`internal/engine/act.go`、`internal/engine/capture.go`、`internal/engine/settle.go`、`internal/engine/wait_for.go`、`internal/engine/delivery.go`、`internal/engine/app.go`、`internal/engine/text_partial.go`、`internal/mcp/result_text.go`、`internal/mcp/result_text_test.go`、`internal/mcp/result_manifest_test.go`、`internal/mcp/schema.go`、`internal/mcp/schema_test.go`、`internal/mcp/catalog_test.go`、`scripts/mcp-probe/result.go`、`scripts/mcp-probe/result_v4.go`、`scripts/mcp-probe/tree_body_consistency.go`、`scripts/mcp-probe/catalog.go`）。

---

## 0. 摘要

围绕实机连续暴露的三个观察与恢复痛点（GitHub Issue **#44**、**#45**、**#46**），本提案在不引入额外设备截图 I/O、不新增请求字段、不破坏 `recovery` 协议状态机单一语义的前提下，于观察投影层统一落地三项正交、诚实、低开销的能力：

1. **动作树变化客观事实（解决 #44）**：`act` 常规成功（`outcome=executed` 并发布后观察）时，在引擎锁外对比被消费前帧 `obs` 与动作后发布帧（忽略 `ref`），按需输出 `Observation.tree_change`（文本 `change=`）：精确区分“前后完整公开树零变化（`unchanged`）”、“仅底层完整公开树有细节变化而 compact 未变（`detail_only`）”与“页面实际已变化并恰好零差异命中历史基线（`matched_base`）”，破除多基线池下的 `changes=0` 真假混淆与隐性零变化盲区，且绝不伪造未证明的像素/表面变化事实。
2. **加载态、媒体渲染面与校准稀疏树分类（解决 #45）**：利用已免费采集的原始 `device.Capture.Nodes` 类名特征（可见 `ProgressBar` 与大面积 `SurfaceView`/`TextureView`/`VideoView`/`GLSurfaceView`/`PlayerView`）和 `settle` 的 `quality.state == unstable`，将 `Observation.visual` 扩充为互斥优先级闭集 `loading > image_heavy(<pct>%) > media_surface > sparse_tree`，温和校准 `sparse_tree` 阈值以覆盖 4–6 节点视频/直播初始页，并规定瞬态 `loading` 在 `delta` 上永不静默省略。
3. **观察级只读核对建议与调用指引（解决 #46）**：保持 `protocol.Recovery`（6 元协议状态机）纯净，新增由已发布 `visual`/`tree_change` 与是否已交付有效图片唯一推导的可选 `Observation.hint`（文本 `hint=snap|wait_for`），在静止无树变化/视觉盲区主动提示 `snap`（且不消费、不发布 `O`），在加载过渡态主动提示 `wait_for`，已带有效图时自动抑制冗余 `hint=snap`；同步补齐 MCP `callerGuidance`/`act`/`snap` 指引。

本提案**明确不做**动作前后自动双截图/像素哈希比对、**不做**中文业务词典硬编码 `modal` 分类、**不将 `snap` 混入 `protocol.Recovery.Entry`**，也**不对静态错 ref 的 `node_not_found` 盲目提示 `snap`**。实施完成并归档后，将按完整发布交付范围推送 `origin`，并在 `#44`/`#45`/`#46` 逐一附实现与实机结果回复后关闭。

---

## 1. 问题

### 1.1 现象与代价

1. **#44（`act` `outcome=executed` 后出现 `changes=0`，三类状态互相混淆）**：
   - 虚拟屏实机中多次出现 `act` 返回 `outcome=executed` 且树块为 `tree v5 delta base=_X changes=0`（如网易云设置页点击开关后弹出未入树“流量提醒”浮层、点击更多菜单实际已展开、知乎搜索页点击关闭层、B 站倍速/菜单动作）。
   - 同时存在两类反向/隐性混淆：
     - **多基线回落伪装成零变化**：`O1` 为主页面（基线），`O2` 进入覆盖层（相对 `O1` 有 `changes > 0`），`O3` 点击关闭退回主页面并零差异命中历史基线 `O1`，结果同样输出 `changes=0`，调用方易误以为“没关闭”而多按一次 `BACK` 退出目标页；
     - **隐性零变化**：若 `O2` 本身相对 `base=O1` 已有 `changes=k`，`O3` 点击无树反馈后与 `O2` 完全一致，但因 `delta` 相对固定 `base=O1` 编码，输出仍是 `changes=k`，调用方更难一眼识别动作未引起公开树变化。
   - *可观测代价*：调用方要么重复点击把已展开的开关/菜单反向关掉，要么被迫额外调用 `observe(full=true)` 或每步默认带图，白耗数百至数千 token 与一次完整采集往返。
2. **#45（视频/直播/加载页稀疏树漏报，且无法区分“仍在加载”与“纯视觉静止”）**：
   - B 站视频页与直播间刚打开时初始树常仅返回 4–6 个功能节点（如返回按钮、进度文本 `0.0`、进度条容器或一条非 WLAN 提示），等待 1.5s–4.7s 后才展开为 39–47 个节点。
   - 现行 `Observation.visual` 的 `sparse_tree` 阈值严格为 `textRunes < 20 && interactive < 5`，初始页达到 5–6 个控件或 20–29 字短提示即直接漏报；且 `result v4` 省略了 `observation.quality`，调用方无从判断当前稀疏页是 `settle` 未收敛仍在加载（应 `wait_for`），还是已经静止且画面主要位于 `SurfaceView`/`TextureView`（应 `snap`）。
   - *可观测代价*：调用方将加载过渡页误判为采集失败或页面空白，出现无谓返回、重试 `app(open)` 或过早截一张加载中黑屏。
3. **#46（树信息不足或 `changes=0` 时缺少主动 `snap` 提示）**：
   - `snap` 具有只读、立即、不发布新 `O`、不消费当前 `O` 的低侵入优势，但常规成功的 `act`/`observe`/`wait_for` 不带 `recovery`，`obs` 行也不直接提示应调用 `snap` 还是 `wait_for`，调用方只能凭经验猜测。

### 1.2 证据链

| 既有链路与行为 | 源码与文档位置（基线 `2628b10`） |
| --- | --- |
| 层级 XML 仅提供节点属性（含 `ClassName`/`Bounds`/`Visible`）与 `Foreground`，不含 `AccessibilityWindowInfo.type`，且默认采集不含像素对比 | [`hierarchy.go`](../../../internal/android/hierarchy.go#L200-L232)、[`device/types.go`](../../../internal/device/types.go#L79-L128) |
| Stage 1 公开树构造中，无文字/无动作/无非默认状态的 `ProgressBar`/`SurfaceView`/`TextureView`/`VideoView` 因 `hasSemantics == false` 不进入公开树，但完整保存在 `device.Capture.Nodes` 中 | [`build.go`](../../../internal/observation/build.go#L237-L390) |
| `act` 认领时已持有不可变前置观察 `obs`（含 `Foreground`/`Screen`/`Compact`/`Projection.Tree`），重验读取 `fresh`，派发确认后经 `captureAndPublish` 发布后观察，除 `scroll` 算 `effect` 外不对其他动作计算前后树变化；统一 `app` 后观察入口在 `app.go`，`act` `text` 的 `partial` 交付路径在 `text_partial.go` | [`act.go`](../../../internal/engine/act.go#L82-L160)、[`act.go`](../../../internal/engine/act.go#L207-L249)、[`act.go`](../../../internal/engine/act.go#L622-L666)、[`app.go`](../../../internal/engine/app.go#L208)、[`text_partial.go`](../../../internal/engine/text_partial.go#L35)、[`text_partial.go`](../../../internal/engine/text_partial.go#L92) |
| `settleCapture` 已在 `start`/`act`/`app` 变更路径产出 `quality`（`stable`/`unstable`/`unknown`），并在 `captureAndPublish` 将其传入 `publishTreeAttempt`；`result v4` 文本显式省略整份 `observation.quality` | [`settle.go`](../../../internal/engine/settle.go#L45-L51)、[`settle.go`](../../../internal/engine/settle.go#L250-L279)、[`settle.go`](../../../internal/engine/settle.go#L298-L308)、[`capture.go`](../../../internal/engine/capture.go#L174-L266)、[`result_text.go`](../../../internal/mcp/result_text.go#L134-L143) |
| `wait_for(stable=false)` 第 2 帧及以后首次命中条件（`samples >= 2` 且 `consecutive < StableFrames`）立即以 `StopReason=condition` 早停，产出 `Quality{State:"unstable", StopReason:"condition"}` | [`wait_for.go`](../../../internal/engine/wait_for.go#L292-L325)、[`wait_for.go`](../../../internal/engine/wait_for.go#L552-L570) |
| 多基线池（`1主+4副`）在 `publishTreeAttempt` 中跨历史基线选最短 `tree v5` delta；`publicCandidate` 在每个基线候选上重复调用 `InferVisualHint(projection.Tree, cap.Foreground.State)`；`observationVisual` 对全部 `visual` 种类统一做 `delta` 同态省略；`ReadDelta.IsEmpty()` 已存在（不判空接收者） | [`capture.go`](../../../internal/engine/capture.go#L299-L586)、[`capture.go`](../../../internal/engine/capture.go#L1061-L1114)、[`capture.go`](../../../internal/engine/capture.go#L1276-L1310)、[`read_tree.go`](../../../internal/protocol/read_tree.go#L887-L890) |
| `InferVisualHint` 现仅支持 `image_heavy(<pct>%)` 与 `sparse_tree`（阈值 `textRunes < 20 && interactive < 5`），未读取 `Capture.Nodes` 类名与 `quality`；`equalReadNode` 已实现完整 `ReadNode` 字段比较（含 `Ref`），`ReadEqual` 先展开历史 `Chain` 再比较 | [`visual.go`](../../../internal/observation/visual.go#L16-L93)、[`visual.go`](../../../internal/observation/visual.go#L158-L210)、[`visual.go`](../../../internal/observation/visual.go#L284-L351)、[`read.go`](../../../internal/observation/read.go#L661-L748) |
| `protocol.Recovery` 为 6 元协议状态机入口（`retry`/`observe`/`status`/`end`/`user`/`update`），常规成功固定返回 `nil`，JSON `reference` 仅为抽象词 `"O"|"P"|"none"`；`target_changed` 认领后拒发免费交付新 `O` 并输出 `recovery entry=retry ref=<新O>` | [`responses.go`](../../../internal/protocol/responses.go#L602-L642)、[`recovery.go`](../../../internal/engine/recovery.go#L57-L150)、[`delivery.go`](../../../internal/engine/delivery.go#L25-L74)、[`result_text.go`](../../../internal/mcp/result_text.go#L1524-L1562) |
| `Observation` 现有可选 `Visual string` 字段；`decode_response.go` 对 `observation.visual` 拒收显式 `null` 与空串；`result_text.go`、`result_text_test.go` 与 `result_manifest_test.go` 强制全字段反射登记与反向解码；`mcp-probe` 独立验证 `obs` 行列序、token 合法性与无树禁现约束 | [`responses.go`](../../../internal/protocol/responses.go#L312-L456)、[`decode_response.go`](../../../internal/protocol/decode_response.go#L356-L404)、[`result_text.go`](../../../internal/mcp/result_text.go#L33-L143)、[`result_text.go`](../../../internal/mcp/result_text.go#L813-L967)、[`result_text_test.go`](../../../internal/mcp/result_text_test.go#L868-L945)、[`result_manifest_test.go`](../../../internal/mcp/result_manifest_test.go#L97-L218)、[`result_v4.go`](../../../scripts/mcp-probe/result_v4.go#L1034-L1183)、[`tree_body_consistency.go`](../../../scripts/mcp-probe/tree_body_consistency.go#L18-L66) |
| MCP 发现说明 `treeTextGuide`、`callerGuidance`、`act`/`snap` 描述当前无 `change`/`hint` 与 `snap`/`wait_for` 分流规则；`baselineDiscoveryBytes = 37500` 在契约 29 实测总量已达 `37498 B`（仅余 2 B），且门禁严禁发现文本出现 `quality`/`unstable`/`effect` 等内部词 | [`schema.go`](../../../internal/mcp/schema.go#L28-L158)、[`schema.go`](../../../internal/mcp/schema.go#L258-L280)、[`schema.go`](../../../internal/mcp/schema.go#L342-L375)、[`schema_test.go`](../../../internal/mcp/schema_test.go#L22-L31)、[`schema_test.go`](../../../internal/mcp/schema_test.go#L134-L200)、[`schema_test.go`](../../../internal/mcp/schema_test.go#L401-L422)、[`catalog_test.go`](../../../internal/mcp/catalog_test.go#L29-L47)、[`catalog.go`](../../../scripts/mcp-probe/catalog.go#L74-L108) |

### 1.3 为什么直觉会出错

1. **为什么不能输出 `surface_changed=true/false` 或 `evidence=visual_only`？**
   - 遵循 AGENTS.md 硬约束第 8 条（事实分层，不得伪造未证明事实）：仅做 uiautomator 层级采集时，服务端客观上无法区分“动作未生效”、“仅像素变化”与“未进入无障碍树的浮层/Toast”。若在每次 `act` 前后自动增加两次截图并做像素比较，会显著拉长每步延迟并在虚拟屏动效/状态栏时间跳动下产生大量假阳性。服务端零额外 I/O 能诚实证明的只有**树层面的变化事实**，视觉确认交由调用方按提示主动 `snap`。
2. **为什么不能机械地对所有 `changes=0` 输出 `unchanged` 或提示 `snap`？**
   - 在契约 26/29 的 `1主+4副` 多基线池下，页面从覆盖层退回历史基线页同样会产生 `changes=0`。此时页面相对动作前消费的 `obs` 发生了**实质性内容切换**；若盲目标记“无变化”并提示 `snap`，不仅语义失实，还会在最常见的“关闭弹窗/返回上级”路径上制造大量误报与多余截图。
3. **为什么不能把 `snap` 直接加进 `protocol.Recovery.Entry`？**
   - 现行 `recovery` 是单槽位、确定性的**协议状态机恢复入口**：常规成功结果严格省略 `recovery`，`recovery != nil` 代表存在失败、运行中或降级成功待处理；且 `/api/*` JSON 中 `recovery.reference` 严格为抽象词 `"O"|"P"|"none"`（而非 `#46` 示意的 `"Pxxxx"`）。
   - 若将可选视觉核对 `snap` 塞入 `recovery`：一方面会打破“成功省略 `recovery`”的不变式，诱使客户端对幂等点击或有效新 `O` 强制插入多余 `snap`；另一方面在 `target_changed` 免费交付新 `O` 时会与既有的 `recovery entry=retry ref=<新O>` 发生单槽位互斥冲突。将只读核对建议放在 `Observation.hint` 上，既与 `recovery` 零冲突，又可在 `target_changed` 交付稀疏/加载新帧时同时保留 `recovery entry=retry` 与 `hint=snap|wait_for`。
4. **为什么不做中文业务关键词 `modal` 分类，也不对静态 `node_not_found` 提示 `snap`？**
   - u2 XML 不携带 `AccessibilityWindowInfo.type`，靠硬编码“非 WLAN / 版权 / 登录 / 提示”等业务词表判断 `modal` 极易跨语言、跨应用误报或漏报，且一旦弹窗进了公开树，其正文与按钮本身已清晰可见；若未进公开树，动作后自然落入 `change=unchanged + hint=snap`。
   - 认领前静态 `node_not_found`（引用了当前 `O` 中不存在的 `ref`）属于参数/引用错误，原 `O` 依然 `ready`，盲目提示 `snap` 无法修正错传的 `ref`。

---

## 2. 目标与非目标

### 目标

1. **动作树变化客观事实（#44）**：为 `act` 常规成功（`outcome=executed` 并发布后观察）新增锁外树内容比较，在 `Observation.tree_change`（`obs` 行 `change=`）按闭集精确区分 `unchanged`、`detail_only`、`matched_base`；常规有变化场景（非空 delta 或 full）省略该字段（零开销）。
2. **页面加载/媒体/稀疏视觉分类（#45）**：升级 `internal/observation/visual.go` 的分类器，利用现有 `Capture.Nodes` 的可见 `ProgressBar` 与大面积媒体视图类名及 `quality.state == unstable`，以严格优先级输出 `loading > image_heavy(<pct>%) > media_surface > sparse_tree`；温和放宽 `sparse_tree` 至 `textRunes < 30 && interactive < 7` 覆盖 4–6 节点视频/直播初始页；规定 `loading` 在 `delta` 中永不省略，其余三类保持同态省略；将视觉分类移至 `publishTreeAttempt` 锁外单次计算（消除多基线候选循环内重复计算）。
3. **观察级只读核对提示（#46）**：在 `Observation.hint`（`obs` 行 `hint=`）由 `(publishedVisual, publishedTreeChange, hasValidImage)` 确定性推导出 `wait_for` 或 `snap`（已成功交付有效图片时自动抑制 `hint=snap`，`matched_base` 不误触发 `hint=snap`），不修改 `protocol.Recovery` 的 6 元协议状态机。
4. **全链路协议、投影、探针与说明对齐**：更新 `release/versions.json`（`0.34.0` / 契约 `30` / `versionCode 36`）与 4 个生成文件、严格 JSON 编解码、`result v4` 渲染与反射清单门禁、MCP `serverInstructions`/`act`/`snap` 描述、`scripts/mcp-probe` 独立解析与一致性门禁，以及正式文档 `docs/`（通过独立只读文档审计）。
5. **整组发布、归档与 Issue 收口**：按 AGENTS 第 19、24 条完成默认三项发布交付与最小实机闭环，先提交实现再独立提交归档，推送 `origin` 并在 `#44`/`#45`/`#46` 附实现提交、验证结论与能力边界回复后关闭。

### 非目标

- **不新增任何请求参数**（不增加截屏开关、变化检测开关或阈值参数），**不增加任何 ADB / u2 / 截图设备 I/O**。
- **不伪造未证明的像素或窗口事实**：不输出 `surface_changed`、`evidence=visual_only` 或基于中文业务词典猜测的 `modal`。
- **不修改 `protocol.Recovery` 的 6 元 `entry` 枚举、省略规则或 `reference` 抽象词契约**。
- **不修改 `Result.effect`（保持 `scroll` 专属与 `result v4` 省略）**，不修改 `status(P)` 视图（`status(P)` 仍不附 `observation`）。
- **不修改 `tree v5` 普通树块头/正文语法、`inspect v3` 补看语法、`result v4` 外壳头版本号**，也不修改 `act` 规划/重验、`wait_for` 采样判据与多基线池 `(12, 10, 5)` / `<80%` 参数。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 最小改动（不改 `/api/*` 契约） | 仅微调 `sparse_tree` 阈值并在 MCP `callerGuidance` 补充文案引导 | 不升契约号，仅改常量与工具目录指纹 | 无法区分 #44 的“真未变”与“回落历史基线导致的 `changes=0`”；无法传达 `settle` `unstable` 加载态；长上下文中纯指令文案衰减明显，普通设置页 `changes=0` 仍无行内信号 |
| **R2 观察/提示层统一收敛（选定）** | 保持 `recovery` 纯净；扩展 `Observation.visual`（新增 `loading`/`media_surface` 并校准 `sparse_tree`），新增可选 `Observation.tree_change` 与 `Observation.hint`，在 `result v4` `obs` 行极简投影 `change=`/`hint=` 并同步 MCP 引导；升共同契约至 `30` | 升 `contract_version=30` 并整组构建部署；零新增设备 I/O，CPU 仅增加一次 O(N) 内存树比对（`<1ms`） | 自定义非标准加载圈/播放器类名会退化为通用 `sparse_tree`/`unstable`；未进无障碍树的纯视觉浮层仍需调用方按 `hint=snap` 截一次图确认；已进树的中文业务弹窗不自动标注 `modal` |
| R3 扩展 `protocol.Recovery` 直出 `entry=snap` | 在 R2 判据基础上，将 `snap` 纳入 `Recovery.Entry` 并在常规成功结果上输出 `recovery` | 升 `contract_version=30`；打破“常规成功省略 `recovery`”不变式，与 `target_changed` 既有 `retry/O` 产生单槽位冲突，全套 `recovery` 单测与探针需重构 | 调用方易将 `recovery entry=snap` 误作强制阻塞步骤，在幂等点击或已有新 `O` 时被迫插入多余截图；`target_changed` 无法同时表达 `retry` 与 `snap` |

**选择理由**：R2 严守事实分层与 `recovery` 协议状态机纯净性，以极低上下文成本（常规页 0 token，触发时 `obs` 行仅 +2–4 token）把 #44（破除真假 `changes=0` 与隐性零变化）、#45（区分加载中与纯视觉媒体/稀疏页）和 #46（主动且无冲突地分流 `snap` 与 `wait_for`）收束在同一套正交机制中。若未来实机证明调用方仍普遍忽略 `obs` 行的 `hint=snap` 且出现明确强恢复需求，再另行评估更重协议改造。

---

## 4. 判据

### 4.1 动作前后树变化判据（`Observation.tree_change`）

仅当本次发布源自 **`act` 常规成功**（`outcome == "executed"` 且成功发布后观察 `postObs`，传入被本次 `act` 消费的不可变前置观察 `previousObs != nil`）时参与判定；`start`、`observe`、`wait_for`、`app`、`act.delivery` 及一切无树失败观察恒为空字符串（省略）。

1. **前置语境判等**：
   - `sameForeground(a, b device.Foreground)`：先比较 `a.State == b.State`，再按 `observationPublicLocked`（`capture.go:1286-1297`）公开投影口径按态分支比较：
     - `present`：比较 `a.Package == b.Package && equalIntPtr(a.TaskID, b.TaskID)`；
     - `unknown`：比较归一化后的 `Reason`（空串归一化为 `"foreground_unknown"`）；
     - `none`（及其他态）：无额外公开前景字段；
   - `sameScreenGeometry(a, b device.Display)`：`a.Width == b.Width && a.Height == b.Height && a.Rotation == b.Rotation`。
2. **忽略 `ref` 的树内容判等（在 `e.mu` 锁外执行）**：
   - `EqualCompactContent(a, b *protocol.ReadTree) bool`：两者非空、`a.View == b.View`、根非空，按逻辑树逐节点递归比较 `Role`、`Context`、`Truncated`、`Bounds.Equal`、`Label`、`Actions`、五个三态布尔（`Enabled`/`Focused`/`Selected`/`Protected`/`Checked`）、`Value`、`Hint`、`Resource`、`Axis` 与 `Children` 数量及顺序；**逻辑展开历史 `Chain`（普通无链树走零分配快路径，仅遇到 `len(Chain) > 0` 时按 `ReadEqual` 同口径等价展开）后比较，唯独忽略 `Ref`**（忽略 `Ref` 避免保号跨基线重算导致的假差异）。
   - `EqualPublicTreeContent(a, b *protocol.Tree) bool`：两者非空、根非空且递归逐节点比较 Stage 1 完整公开树的 `Role`、`Context`、四元 `Bounds`（含 `nil` 区分）、`Label`、`Actions`、五个三态布尔、`Value`、`Hint`、`Resource`、`Axis` 与 `Children` 数量及顺序；**唯独忽略 `Ref`**。
   - **进入判据与不进入判据的字段**：
     - 进入判据：前景公开投影字段、屏幕宽高/旋转、公开节点结构/顺序/可见文本/值/提示/资源/截断/动作/三态/轴向/几何。
     - 不进入判据：`Ref`、`Observation.ID`、`Revision`、`CapturedAt`、`Quality`、`Image`。
3. **最终输出值判定（在 `publishTreeAttempt` 锁内 O(1) 判定）**：
   - 设 `contextSame = sameForeground(prev.Foreground, curCapture.Foreground) && sameScreenGeometry(prev.Screen, curDisplay)`；
   - 设 `compactSame = contextSame && observation.EqualCompactContent(prev.Compact, curCompact)`；
   - 设 `fullSame = compactSame && observation.EqualPublicTreeContent(prev.Projection.Tree, curProjection.Tree)`；
   - 设 `emptyDelta = (delta != nil && delta.IsEmpty())`，直接复用 `internal/protocol/read_tree.go:887` 已有的 `ReadDelta.IsEmpty()`（该方法不判空接收者，故调用前必先判 `delta != nil`）。
   - 判定表（互斥完备）：
     - `compactSame && fullSame` → `"unchanged"`（动作前后完整公开树零变化）；
     - `compactSame && !fullSame` → `"detail_only"`（紧凑树零变化，但底层完整公开树有细节变化）；
     - `!compactSame && emptyDelta` → `"matched_base"`（动作前后公开树实际已变化，仅因本次 delta 零差异命中历史基线 `base` 而呈 `changes=0`）；
     - `!compactSame && !emptyDelta` → `""`（常规有变化，省略）。
4. **与既有重验/契约的关系**：只读派生事后观察属性，不放宽或收紧任何 `act` 重验条件、不改变 `outcome`、不影响 `scroll` 的 `Result.effect` 计算与持久化。

### 4.2 页面视觉与加载分类判据（`Observation.visual`）

在 `internal/observation/visual.go` 将分类入口演进为纯函数 `ClassifyVisual(tree *protocol.Tree, capture *device.Capture, foregroundState string, quality *protocol.Quality) (kind, value string)`（若需兼容既有纯树单测调用，可保留 `InferVisualHint(tree, fg)` 薄封装转调 `ClassifyVisual(tree, nil, fg, nil)`）：

1. **前置条件**：`tree != nil && tree.Root != nil && tree.Root.Bounds != nil`，且 `screenArea = width * height > 0`；不满足返回 `"", ""`。
2. **Stage 1 全树统计量**（沿用契约 28 口径）：
   - `totalTextRunes`：非 `root`、非 `context` 节点未被祖先复合标签覆盖的 `Label` 各段、非受保护 `Value` 与 `Hint` 中的非空白 Unicode 码点数；
   - `interactiveCount`：非 `root`、非 `context`、具有正面积裁屏矩形且 `len(Actions) > 0` 的节点数。
3. **原始采集 `capture.Nodes` 强结构扫描**（当 `capture != nil` 时执行，跳过 `i == capture.Root` 与 `build.go:249` 一致，仅扫描 `node.DisplayID == capture.Display.ID && node.Visible` 的节点，并将 `node.Bounds` 与 `*tree.Root.Bounds` 求交裁屏得正面积矩形 `r`；所有面积占比比较统一复用既有的 128 位防溢出助手 `areaBelowPercent` / `areaAtLeastPercent`）：
   - **加载指示器 `hasProgressIndicator`**：存在满足 `isProgressBarClass(node.ClassName)` 且 `r.Width() > 4 && r.Height() > 4` 的节点。
     - `isProgressBarClass(class string)`：取最后一个 `.` 后的简单类名 `simple`（无 `.` 则为原串）；当 `strings.HasSuffix(simple, "ProgressBar") && !strings.Contains(simple, "SeekBar")` 时为 `true`（命中 `android.widget.ProgressBar`、`ContentLoadingProgressBar`、`SwipeProgressBar` 等，精确排除视频/音乐播放进度拖动条 `SeekBar`/`AbsSeekBar`）。
   - **媒体渲染面并集面积 `mediaUnionArea`**：收集满足 `isMediaSurfaceClass(node.ClassName)` 的裁屏矩形 `r`（过滤碎片：`areaBelowPercent(area(r), screenArea, visualMinImagePercent)` 为真时跳过，即 `< 5%`；至多按面积降序取前 `16` 个），复用精确离散化 `unionRectArea` 计算并集面积。
     - `isMediaSurfaceClass(class string)`：取简单类名 `simple`；当满足以下后缀之一为 `true`：`"SurfaceView"`（覆盖 `SurfaceView`/`GLSurfaceView`）、`"TextureView"`、`"VideoView"`、`"PlayerView"`（覆盖 `PlayerView`/`StyledPlayerView`）。
4. **互斥优先级判定（固定顺序）**：
   1. **`loading`**（`VisualKindLoading = "loading"`，`value = "loading"`）：
      - `foregroundState != device.ForegroundNone`，且满足以下任一条件：
        - `(a)` `hasProgressIndicator && totalTextRunes < 80 && interactiveCount < 8`；
        - `(b)` `quality != nil && quality.State == protocol.QualityUnstable && quality.StopReason != protocol.QualityStopCondition && totalTextRunes < 40 && interactiveCount < 8`（排除 `wait_for(stable=false)` 条件命中早停 `wait_for.go:559-563`，保留 `settle` 未收敛与 `wait_for` 超时/上限未稳）。
   2. **`image_heavy(<pct>%)`**（`VisualKindImageHeavy = "image_heavy"`）：
      - 100% 沿用契约 28 现有四重过滤判据（`5% <= area < 90%`、非背景承载图 `< 3` 控件、前 `64` 张并集 `>= 45%`、`totalTextRunes < 150`，`pct` 四舍五入并夹逼在 `[45, 100]`）。
   3. **`media_surface`**（`VisualKindMediaSurface = "media_surface"`，`value = "media_surface"`）：
      - `foregroundState != device.ForegroundNone && areaAtLeastPercent(mediaUnionArea, screenArea, visualMinMediaSurfacePercent) && totalTextRunes < 80 && interactiveCount < 10`。
   4. **`sparse_tree`**（`VisualKindSparseTree = "sparse_tree"`，`value = "sparse_tree"`）：
      - `foregroundState != device.ForegroundNone && totalTextRunes < 30 && interactiveCount < 7`（相对契约 28 的 `< 20` / `< 5` 校准至 `< 30` / `< 7`，覆盖 #45 的 4–6 节点视频/直播初始页）。
   5. 否则返回 `"", ""`。
5. **`delta` 发布去重规则（`observationVisual`）**：
   - `full` 观察（`delta == nil`）：原样输出 `hint.raw`（空则省略）；
   - `delta` 观察（`delta != nil`）：
     - 若 `hint.kind == ""`：返回 `""`；
     - 若 `hint.kind == VisualKindLoading`：**原样输出 `"loading"`**（瞬态加载提示永不在 `delta` 中静默省略，确保 `wait_for` 超时或手动 `observe` 仍处于加载中时持续可见）；
     - 其余三类（`image_heavy`/`media_surface`/`sparse_tree`）：沿用现有规则，仅当 `hint.kind != previousKind` 时输出，同态省略。

### 4.3 观察级只读核对提示判据（`Observation.hint`）

`Observation.hint` 不做第二套启发式判断，完全由本次最终发布决定的 `(publishedVisual, publishedTreeChange, hasValidImage)` 唯一推导（其中 `hasValidImage = (image != nil && image.Error == nil)`）：

1. 若 `publishedVisual == "loading"` → `hint = "wait_for"`（页面仍在加载/过渡，即使带图或同时树未变，首要动作仍是等待加载完成）；
2. 否则，若 `!hasValidImage` 且满足以下任一条件 → `hint = "snap"`：
   - `publishedTreeChange == "unchanged"` 或 `publishedTreeChange == "detail_only"`；
   - `publishedVisual != ""`（即 `image_heavy(<pct>%)`、`media_surface` 或 `sparse_tree`）。
3. 否则 → `hint = ""`（省略）。

**衍生不变式**：
- `matched_base` 且无 `publishedVisual` 时，`hint` 必为空（绝不对正常回到历史基线误报 `snap`）；
- 本次观察已成功附带同一采集的完整图片（`hasValidImage == true`）时，`hint` 永不为 `"snap"`（不劝调用方重复截同屏图，但客观事实 `visual` 与 `tree_change` 保持原样）；
- 无树失败观察（`status == "failed"` 或仅含只读图的 `status == "partial"` 且无 `tree`）的 `visual`、`tree_change`、`hint` 均为空（省略）。

### 4.4 协议编码、解码与 `result v4` 语法判据

1. **JSON 成员与严格解码（`internal/protocol/responses.go`、`decode_response.go`）**：
   - `Observation` 字段顺序：`status`、`id`、`origin`、`revision`、`captured_at`、`actionable_until`、`quality`、`screen`、`foreground`、`visual`、`tree_change`、`hint`、`tree`、`missing`、`image`、`error`。
   - 三个字段均为可选非可空字符串：空串不序列化；解码时若出现显式 `null` 或空串 `""` 返回错误（与既有 `observation.visual` 同口径）。
   - 提供共享受控校验函数 `ValidObservationVisual`、`ValidTreeChange`、`ValidObservationHint`，供 `internal/protocol`/`internal/engine`/`internal/client` 测试共享；`scripts/mcp-probe` 继续独立维护 `valid*Token` 黑盒副本，不耦合 `internal/protocol` 校验函数。
2. **`result v4` 文本投影（`internal/mcp/result_text.go`）**：
   - `obs` 行固定字段顺序：
     `obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [visual=<token>] [change=<token>] [hint=<token>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=…]`
   - `v4ResultFields` 新增 `"observation.tree_change": v4Token` 与 `"observation.hint": v4Token`；`renderObservationV4` 在 `visual=` 之后顺序输出 `change=<tree_change>` 与 `hint=<hint>`，并将 `"tree_change"`、`"hint"` 加入已知字段跳过表（防误触发 `json v1` fallback）；若成员为非合法 token 或 `null` 则照现有规则回退 `json v1 path="/observation/tree_change"` 或 `"/observation/hint"`。
   - 不改变 `result v4` 头行与 `tree v5` 块头。
3. **`mcp-probe` 独立校验（`scripts/mcp-probe/result_v4.go`、`tree_body_consistency.go`）**：
   - `decodeObs` 顺序表：`status(0) < fg(1) < at(2) < visual(3) < change(4) < hint(5) < quality(6, 禁止出现) < screen(7) < rot(8) < missing(9)`；
   - `validVisualHintToken` 接受 `"loading"`、`"media_surface"`、`"sparse_tree"`、`"image_heavy(45..100%)"`；
   - `validTreeChangeToken` 接受 `"unchanged"`、`"detail_only"`、`"matched_base"`；
   - `validObservationHintToken` 接受 `"snap"`、`"wait_for"`；
   - `checkObservationHints`（扩展原 `checkObservationVisual`）在全部模式校验：`visual`/`tree_change`/`hint` 类型必须为字符串且属于闭集、无树观察禁止出现任一字段，且当 `observation.image` 存在且无 `error` 时禁止出现 `hint == "snap"`。

---

## 5. 算法

### 5.1 `publishTreeAttempt` 锁外计算与锁内单点提交

```text
func publishTreeAttempt(work, session, phase, tree, base, selection,
                        status, image, quality, previousObs):
    // 1. 锁内快照基线池与 previewObservationID（既有逻辑，不变）
    ...

    // 2. 锁外一次性计算页面视觉/加载分类（与 ref 编号无关，不需在基线循环里重复计算）
    chosenVisual := classifyVisual(tree.projection.Tree, &tree.capture,
                                   tree.capture.Foreground.State, quality)

    // 3. 锁外一次性计算 act 动作前后树内容判等（仅 previousObs != nil，O(N) 纯内存）
    compactSame, fullSame := false, false
    if previousObs != nil:
        if sameForeground(previousObs.Foreground, tree.capture.Foreground) &&
           sameScreenGeometry(previousObs.Screen, tree.capture.Display):
            compactSame = observation.EqualCompactContent(previousObs.Compact, tree.compact)
            if compactSame && previousObs.Projection != nil && tree.projection != nil:
                fullSame = observation.EqualPublicTreeContent(previousObs.Projection.Tree,
                                                              tree.projection.Tree)

    // 4. 锁外遍历基线池候选，计算 tree v5 最短合格 delta（既有逻辑；不再重复算 visual）
    ...

    // 5. 锁内唯一提交门禁（重验会话 active、工作授权、STOP、显示代次、CaptureMaxAge、留存预算）
    e.mu.Lock()
    ... // 任一门禁失败或撤销立即解锁返回，零状态污染，不发布观察

    // 6. 锁内 O(1) 决定对外发布的 visual / tree_change / hint
    publishedVisual := observationVisual(delta != nil, chosenVisual,
                                         e.previousVisualKindLocked(delta))
    publishedTreeChange := ""
    if previousObs != nil:
        switch:
        case compactSame && fullSame:
            publishedTreeChange = protocol.TreeChangeUnchanged
        case compactSame:
            publishedTreeChange = protocol.TreeChangeDetailOnly
        case delta != nil && delta.IsEmpty():
            publishedTreeChange = protocol.TreeChangeMatchedBase
    hasValidImage := image != nil && image.Error == nil
    publishedHint := observationHint(publishedVisual, publishedTreeChange, hasValidImage)

    // 7. 更新 e.current / e.baselines / e.observations 并投影返回 protocol.Observation
    ...
    e.mu.Unlock()
    return public, obs, nil
```

### 5.2 时间轴与异常分支行为

```text
act(O_prev, op)
  ├─ 静态检查 / 认领前拒绝 ──► not_executed, 无 postObs, 无 tree_change/visual/hint
  ├─ 认领 O_prev → 重验 fresh
  │    └─ 失败 (target_changed 等) ──► deliverRejection(previousObs=nil)
  │         ├─ 发布新 ready O (quality=not_sampled), tree_change=""
  │         ├─ 若新帧自身命中 visual (loading/image_heavy/media_surface/sparse_tree) → 附 visual 与 hint
  │         └─ recovery 保持 entry=retry ref=<新O> (互不冲突)
  ├─ 派发 op
  │    ├─ uncertain / not_executed ──► 不走常规 post_observation，不设 tree_change
  │    ├─ partial (text_partial.go) ──► 交付现场新 O 时传 previousObs=nil (tree_change="")，本帧若命中 visual 仍按规附带 visual/hint
  │    └─ executed ──► settleCapture (产出 quality) → 可选读图 → publishTreeAttempt(previousObs=O_prev)
  │         ├─ STOP / 撤销 / 超时 / 预算耗尽 ──► 丢弃候选，不发布观察，不修改 VisualKind
  │         ├─ 树采集失败 ──► failedObservation (无 tree)，visual/tree_change/hint 全为空
  │         └─ 提交成功 ──► 发布新 ready O + (visual, tree_change, hint)；recovery 为 nil
```

- **撤销 / 失败 / 超时 / 预算耗尽**：与既有提交条件化完全一致，锁外候选直接丢弃，不更新 `e.current` 或 `VisualKind`；无树失败观察永不携带 `visual`、`tree_change` 或 `hint`。
- **`observe` / `wait_for` / `start` / `app`**：调用 `captureAndPublish(..., previousObs=nil)`，因此 `tree_change` 恒为空；`visual` 与 `hint` 正常按本帧事实与 `quality` 计算（例如 `wait_for` 超时仍处于 `loading` 时稳定给出 `visual=loading hint=wait_for`）。
- **`status(P)` 与重复 `P` 重放**：保持现行契约不变——不附 `observation`，亦不在 `operationState` 额外持久化观察级 `tree_change`/`hint`。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `release/versions.json` 及 4 个生成版本文件 | `release_version` 升至 `"0.34.0"`，`contract_version` 升至 `30`，`android_version_code` 升至 `36`，运行 `scripts/generate-version.ps1` 同步生成常量 |
| `internal/protocol/responses.go`、`decode_response.go` | `Observation` 新增 `TreeChange string`（`"tree_change"`）与 `Hint string`（`"hint"`），同步 `MarshalJSONTo`/`UnmarshalJSONFrom`（拒收 `null` 与空串）；新增闭集常量与校验函数 `ValidObservationVisual`/`ValidTreeChange`/`ValidObservationHint`；直接复用 `internal/protocol/read_tree.go:887` 既有的 `ReadDelta.IsEmpty() bool`（不改动 `read_tree.go`，调用前先判 `delta != nil`） |
| `internal/observation/visual.go`、`read.go`、`doc.go` | 演进为 `ClassifyVisual(tree, capture, foregroundState, quality)`（实现 `loading` / `image_heavy(<pct>%)` / `media_surface` / `sparse_tree` 四分支与 `ProgressBar`/`SurfaceView` 等类名扫描，跳过 `capture.Root` 裁屏并复用 `areaBelowPercent`/`areaAtLeastPercent`，校准 `sparse_tree` 为 `<30`/`<7`）；新增 `EqualCompactContent(a, b *protocol.ReadTree) bool`（支持等价展开历史 `Chain`，忽略 `Ref`）与 `EqualPublicTreeContent(a, b *protocol.Tree) bool`（忽略 `Ref`） |
| `internal/engine/capture.go`、`act.go`、`observe.go`、`start.go`、`wait_for.go`、`delivery.go`、`app.go`、`text_partial.go`（及测试辅助 `quality_test.go`、`baseline_selection_integration_test.go`） | `captureAndPublish` / `publishTreeAttempt` 支持可选 `previousObs *observationState`（仅 `act.go` 常规成功后传入被消费的 `obs`，`text_partial.go` 等其余路径均传 `nil`）；将 `ClassifyVisual` 移至 `publishTreeAttempt` 锁外单次调用（移除 `publicCandidate` 与基线候选循环内的重复调用，并同步 `baseline_selection_integration_test.go` 解包）；更新 `observationVisual` 使 `loading` 不在 `delta` 省略；新增 `observationHint` 推导 `hint`；透传三字段至 `observationPublicLocked` |
| `internal/mcp/result_text.go`、`result_text_test.go`、`result_text_v4_test.go`、`result_manifest_test.go` | `v4ResultFields` 注册 `observation.tree_change` 与 `observation.hint`；`renderObservationV4` 在 `visual=` 后依次渲染 `change=` 与 `hint=` 并加入跳过表；`result_text_test.go` 的 `decodeResultV4ObservationLine` 补齐 `change`/`hint`；补齐 `result_manifest_test.go` 反射清单测试夹具 |
| `internal/mcp/schema.go`、`schema_test.go`、`catalog_test.go` | 更新 `treeTextGuide`（`obs` 行 `visual`/`change`/`hint` 语法与规则）、`callerGuidance` 与 `act`/`snap` 描述（清晰说明 `change=unchanged`/`detail_only`/`matched_base`、`visual=loading`/`media_surface`/`sparse_tree`、`hint=snap`/`wait_for` 以及 `snap` 不消费也不发布 `O`，且严禁出现 `quality`/`unstable`/`effect`/`observed`/`not_observed` 等内部禁词）；优先精简冗余措辞并将 `baselineDiscoveryBytes` 透明调整至 `38200`（上限增量 `<= +700 B`）；同步更新 `schema_test.go` 与 `catalog_test.go` 断言 |
| `scripts/mcp-probe/result.go`、`result_v4.go`、`tree_body_consistency.go` 及探针测试 | `observationView` 补充 `TreeChange`/`Hint`；`decodeObs` 支持 `change=` 与 `hint=` 严格列序；扩展 `validVisualHintToken` 并新增 `validTreeChangeToken`/`validObservationHintToken`（独立黑盒副本）；扩展 `checkObservationHints` 校验三字段合法性、无树禁现与“有效图抑制 `hint=snap`”不变式 |
| `docs/interface.md`、`docs/mcp.md`、`docs/architecture.md`、`docs/client.md`、`docs/integration.md`、`docs/release.md`，以及版本同步涉及的 `docs/control-protocol.md`、`docs/phone-operations.md`、`docs/runbook.md` 与 `README.md` | 同步契约 `30`、`Observation.visual`/`tree_change`/`hint` 规范、`obs` 行新列序、最新工具目录 SHA-256 指纹与 `snap`/`wait_for` 协作边界，并同步其余文档与 `README.md` 中的版本号/版本示例 |

### 6.2 配置与取值

全部阈值为 `internal/observation/visual.go` 包内编译期确定性常量（无外部配置，无请求参数）：

| 常量 | 取值 | 依据 |
| --- | --- | --- |
| `visualLoadingMaxTextRunes` | `80` | 有显式 `ProgressBar` 时允许携带简短加载/非 WLAN 提示文，同时排除正文丰富的正常列表页底部小刷新圈 |
| `visualLoadingMaxInteractive` | `8` | 排除常规完整页面的角落加载指示器 |
| `visualUnstableSparseTextRunes` | `40` | 无 `ProgressBar` 仅靠 `quality=unstable` 判加载时采用更严格字数上限，防动态列表误报 |
| `visualUnstableSparseInteractive` | `8` | 覆盖初始加载中的 4–7 控件页面，排除完整信息流 |
| `visualMinMediaSurfacePercent` | `25` | 视频半屏/横屏/直播/地图播放器常规至少占屏 25%–100%，过滤小角标/迷你悬浮播放条 |
| `visualMaxMediaRects` | `16` | 媒体视图并集面积计算采样上限，保证 O(1) 级耗时 |
| `visualMediaMaxTextRunes` | `80` | 覆盖带标题与短互动提示的媒体页，排除嵌入视频卡片的图文评论/信息流 |
| `visualMediaMaxInteractive` | `10` | 覆盖视频/直播页外围播放控制按钮组，排除复杂常规页面 |
| `visualSparseMaxTextRunes` | `30` | 相对契约 28 的 `20` 温和上调，覆盖 #45 短提示（如 `0.0` + 标题/流量短句）而不误报普通对话框 |
| `visualSparseMaxInteractive` | `7` | 相对契约 28 的 `5` 温和上调，精确覆盖 #45 实测的 4–6 功能节点初始页 |
| `visualMinImagePercent` 等 `image_heavy` 常量 | 保持 `5 / 90 / 3 / 64 / 45 / 150` 不变 | 沿用契约 28 双会话实测已验证口径 |

### 6.3 日志与可观测性

- 严格遵守 AGENTS.md 硬约束第 10 条日志白名单与最小侵入原则：**不新增日志事件，不扩展 `internal/eventlog/log.go` 白名单键**（完全复用现有 `observation.baseline`、`observation.quality`、`observation.settle` 事件），不记录任何节点文本、坐标、`ClassName` 原串、包内私有信息或树内容。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威与状态锁短小（硬约束 1、5） | 不变：`ClassifyVisual` 与 `EqualCompactContent`/`EqualPublicTreeContent` 全部在 `e.mu` 锁外完成，锁内仅 O(1) 布尔/字符串组合 |
| 提交条件化与 STOP 不等待（硬约束 2、4） | 不变：撤销或提交门禁失败立即丢弃候选，不发布观察、不污染 `VisualKind` |
| 严格接入与三态省略（硬约束 6、7） | 保持：`visual`/`tree_change`/`hint` 省略即无该事实，显式 `null` 与空串被严格拒绝 |
| 事实分层（硬约束 8） | 加强：`outcome`（执行）、`tree_change`（公开树前后对比）、`visual`（单帧视觉/加载特征）、`hint`（只读建议）与 `recovery`（协议状态机）五者严格正交，绝不以树结果伪造像素结论 |
| 发现字节预算、禁词门禁与工具目录指纹 | 更新 `schema.go` 解释 `visual=loading`/`change`/`hint` 时严格避开 `quality`/`unstable`/`effect`/`observed`/`not_observed` 等内部禁词（`schema_test.go:401-422`、`catalog.go:100-108`）；重新核验 `CatalogFingerprint()` 并同步 `docs/mcp.md`、`docs/release.md` 与发布清单；`baselineDiscoveryBytes` 透明控增在 `38200` 内 |

### 6.5 契约与文档影响

- 共同 `contract_version` 由 `29` 升至 `30`（`X-Mobile-Use-Contract: 30` 与控制端同步协议同步升级）。
- 同步修订的正式文档：`docs/interface.md`（§1 契约号、§2 事实分层、`Observation` 成员 `visual`/`tree_change`/`hint` 闭集与判据）、`docs/mcp.md`（工具目录指纹、`obs` 行新列序与语法、`callerGuidance` 分流说明）、`docs/architecture.md`（观察分类与锁外树对比流水线）、`docs/client.md`（`observation.visual`/`tree_change`/`hint` 说明）、`docs/integration.md`（契约号、`obs` 行与 `visual` 闭集）、`docs/release.md`（版本与指纹说明），并同步 `docs/control-protocol.md`、`docs/phone-operations.md`、`docs/runbook.md` 与 `README.md` 中的版本号及版本示例。提交前必须通过 AGENTS.md 第 15 条独立只读文档审计至 `clean`。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **彻底消除 #44 的三类 `changes=0` 混淆**：
   - 动作后树真无变化 → `change=unchanged hint=snap`，明确告知“动作已派发但树未变，建议 `snap` 核对画面”；
   - 动作后仅底层完整树有细微变化 → `change=detail_only hint=snap`；
   - 动作后页面退回历史基线导致 `changes=0` → `change=matched_base`（无 `hint=snap`），避免误判无效果或误按多余 `BACK`。
2. **精准覆盖 #45 的视频/直播/加载初始稀疏页**：
   - 有 `ProgressBar` 或稀疏且 `settle` `unstable` → `visual=loading hint=wait_for`（且在 `delta` / `wait_for` 超时帧持续可见）；
   - 稳定后大面积 `SurfaceView`/`TextureView`/`VideoView` 媒体页 → `visual=media_surface hint=snap`；
   - 4–6 节点纯稀疏页 → 稳定命中 `visual=sparse_tree hint=snap`，不再因 `interactive == 5/6` 或 20–29 字短提示漏报。
3. **无冲突、低开销落地 #46 的主动 `snap` 提示**：
   - `obs` 行内联极短 `hint=snap` / `hint=wait_for`，常规页 0 token 开销，不污染 `recovery`，不与 `target_changed` 的 `recovery entry=retry` 冲突，且在本次已交付有效图片时自动抑制冗余 `hint=snap`。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 不调用 `snap` 就直接分辨“未入树浮层已弹出”与“点击完全无效” | 遵循零额外截图与硬约束第 8 条，仅凭无障碍层级客观上无法观测像素变化，必须由调用方按 `hint=snap` 做一次只读截图核对 |
| 使用非标准自定义类名（既不含 `ProgressBar` 也不含 `SurfaceView`/`TextureView`/`VideoView`/`PlayerView`）且 `settle` 已 `stable` 的高节点数自绘页面；或播放器使用非 `SeekBar` 且高 `> 4px` 的横向系统 `ProgressBar` 作播放进度条且页面控件 `< 8` | 原始无障碍树既无标准类名也非稀疏树时，服务端无法仅凭树识别自定义指示器/自绘区域；后者在满足控件数阈值时可能被归入 `loading`（可在后续结合更多样本校准） |
| 已完整进入无障碍树的业务语义弹窗（非 WLAN / 版权 / 登录提醒）自动打 `modal` 标签 | u2 XML 缺少 `AccessibilityWindowInfo.type`，且本提案刻意不引入脆弱的中文业务词典；此类弹窗正文与按钮已直接在树中可读 |
| `observe` / `wait_for` 自身前后帧之间的 `tree_change` 对比 | `tree_change` 严格限定为 `act` 常规成功因果对比；只读观察不表达动作因果，避免与 `act` 混淆 |
| 外部 MCP 宿主忽略 `obs` 行 `hint` 或缓存旧工具描述 | 宿主模型决策与托管进程重启归宿主与用户管理，不在本服务控制面内 |

### 7.3 成本

- **设备、ADB 与网络 I/O**：零新增 ADB、u2 RPC、显示探测或截图读取。
- **服务端 CPU 与内存**：
  - `ClassifyVisual` 移出基线候选循环改为单次调用，原本每帧最多 5 次的树遍历降为 1 次，新增一次对 `capture.Nodes` 的线性扫描（典型 `< 0.1ms`）；
  - `EqualCompactContent` / `EqualPublicTreeContent` 仅在 `act` 常规成功时于锁外执行一次单遍短路比较（页面有变化时往往在根节点/首层即刻短路，耗时 `< 0.05ms`；完全相同时整树单遍 `< 0.2ms`），零额外持久化内存。
- **上下文开销**：常规有变化页面 0 字节新增；触发提示的帧在 `obs` 行增加约 2–4 个 token；MCP 发现总字节增量受 `baselineDiscoveryBytes <= 38200` 硬约束。

---

## 8. 验证计划

### 8.1 单元测试

每项核心判据均配备**至少一条反向对照用例**（去掉实现分支必红）：

| 用例 | 期望 |
| --- | --- |
| **反向对照 1（#44 四态树变化矩阵）**：(a) `act(tap)` 前后完全同树（含同基线 `changes=0`、跨旧基线重复 `changes=2` 与 `full` 三种交付形态）；(b) `compact` 树不变但 Stage 1 被裁装饰图或微调矩形变化；(c) `O1(主页) → O2(搜索浮层, delta) → act(关闭) → O3(回主页, delta base=O1 changes=0)`；(d) 常规有变化 `act`；(e) `observe`/`wait_for`/`start`/`app`/`act.delivery` | (a) 均输出 `tree_change="unchanged"` 且 `hint="snap"`；(b) 输出 `tree_change="detail_only"` 且 `hint="snap"`；(c) 输出 `tree_change="matched_base"` 且 `hint=""`（绝不误报 `unchanged`/`snap`）；(d)(e) `tree_change=""`；去掉前后帧对比后 (a)/(b)/(c) 均失败 |
| **反向对照 2（#44 忽略 `ref` 抖动、历史 `Chain` 展开与图片抑制）**：(a) 动作前后树内容与结构全同但部分节点 `ref` 编号不同；(b) `EqualCompactContent` 比较单节点 `Ref` vs 2 节点历史 `Chain`，以及 2 节点 `Chain` vs 等价 2 层展开普通节点；(c) `act(image=true)` 树未变且图片成功交付 vs `act(image=true)` 树未变但图片独立失败 | (a) 仍准确判为 `tree_change="unchanged"`；(b) 单节点与 2 节点 `Chain` 判 `false`、2 节点 `Chain` 与等价展开普通节点判 `true`（忽略 `Ref`）；(c) 图片成功时保留 `tree_change="unchanged"` 但 `hint=""`（抑制冗余 `snap`），图片失败时保留 `hint="snap"` |
| **反向对照 3（#45 `loading` / `media_surface` / 校准 `sparse_tree`）**：(a) 含可见 `android.widget.ProgressBar` 且低内容页 vs 含 `android.widget.SeekBar` 的普通播放页 vs 富文本列表底部 `ProgressBar`；(b) 无 `ProgressBar` 但 `quality=unstable`（非 `condition`）+ 5 节点稀疏页 vs `quality=unstable` + 富文本列表 vs `wait_for(query/absent_query, stable=false)` 第 2 帧命中静态紧凑页（`QualityUnstable + QualityStopCondition`）；(c) 占屏 `35%` 可见 `TextureView`/`SurfaceView` + 6 个播放控件页 vs 占屏 `20%` 小视频卡片 + 120 字信息流 + 超大屏 `areaBelowPercent`/`areaAtLeastPercent` 防溢出用例；(d) 无媒体类的 6 控件 25 字视频初始页 vs 7 控件或 30 字页；(e) 优先级：`loading > image_heavy > media_surface > sparse_tree`；(f) `delta` 连续两帧 `loading` 持续输出 vs 连续两帧 `media_surface` 同态省略 | 全部精确命中期望分类与 `hint`（`loading → wait_for`，其余 `→ snap`）；`SeekBar`、富文本列表小菊花、小视频卡片及 `wait_for` 条件早停均不误报 `loading`；去掉 `StopReason != QualityStopCondition` 守卫、`loading` 不省略特判或恢复旧 `<20`/`<5` 阈值时对应用例必红 |
| **反向对照 4（#46 `target_changed` / `partial` / `recovery` 零冲突）**：(a) `act` 认领后 `target_changed` 免费交付的新帧恰好为 `sparse_tree` / `loading`；(b) `text` `outcome=partial`（`text_partial.go`）交付现场新 `O`；(c) 静态错 ref `node_not_found`；(d) 无树失败观察与只读图降级观察 | (a) 同时输出 `recovery(entry=retry, ref=O)` 与新观察上的 `visual`+`hint`，互不覆盖；(b) `tree_change=""` 且保留新帧自身 `visual`/`hint`；(c) 不输出 `hint=snap`；(d) `visual`/`tree_change`/`hint` 全为空 |
| **协议、`result v4` 反射清单、发现说明与 `mcp-probe` 独立门禁**：覆盖 `Observation` 的 `tree_change`/`hint` 编解码与拒 `null`/空串；`TestResultFieldManifest` 全量反射检查与 `decodeResultV4ObservationLine`；`result v4` `obs` 行 `visual`/`change`/`hint` 顺序与非法值 `json v1` fallback；`schema_test.go`（含 `baselineDiscoveryBytes <= 38200` 及 `TestDiscoveryOmitsObservationInternals` 禁词检查）与 `catalog_test.go`；`mcp-probe` 独立解析、乱序拒绝、非法枚举拒绝、无树禁现拒绝、`hasValidImage` 与 `hint=snap` 互斥拒绝及 `both` 模式双通道一致性 | 全套单测绿灯；构造非法列序/非法值/无树携带/有效图附 `hint=snap`/发现文本含禁词负例均被门禁与探针精确拒收 |

### 8.2 实机/系统级（AGENTS.md 第 19、24 条发布交付默认范围 + 推送与 Issue 收口）

由主代理亲自执行全量测试、发布构建、实机部署、实机闭环、主机双产物替换与双入口独立探针验证（不委派 subagent 代跑或代判）：

1. **全量测试与发布构建核验**：集中运行全仓 Go 单元测试（`internal/engine` 单次命令超时设为 `15` 分钟，不反复重跑碰结果）、Android 控制 App 构建与 `scripts/build-release.ps1`，用 `scripts/verify-manifest.ps1` 核验 `build/release/manifest.json`（确认 `release_version=0.34.0`、`contract_version=30`、`android_version_code=36` 及最新工具目录 SHA-256 指纹）。
2. **交付一（手机整组覆盖部署）**：执行 `scripts/deploy.ps1` 覆盖部署新 `device-service`、手机 `device-mcp` 与 `control.apk`（保留 App 私有数据与配对配置），核验 `/health` 报告 `release_version="0.34.0"`、`contract_version="30"`。
3. **交付二（覆盖变更行为的最小实机闭环）**：在实机虚拟屏会话驱动真实应用页面，重点观测并记录真实结果（不存原始截图/树入库）：
   - 触发一次 `act` 后公开树无变化（如设置页点击无树变化的区域/开关），验证返回 `change=unchanged hint=snap`，随即用同一 `O` 调 `snap` 核对画面且确认该 `O` 未被消费（仍可继续 `act`）；
   - 触发一次多基线返回路径（如打开子层/弹窗后返回原页并零差异命中历史基线），验证返回 `change=matched_base` 且**无** `hint=snap`；
   - 触发一次媒体/稀疏或加载页面（如视频页或弱树页面），验证 `visual=loading|media_surface|sparse_tree` 与 `hint=wait_for|snap` 符合预期（如实机未能稳定构造全部四个子类，须在验证记录中明确区分“已实测”与“仅单测覆盖”）。
4. **交付三（Windows 主机双 exe 替换与双入口独立探针）**：从同一干净实现提交运行 `scripts/build-host-tools.ps1` 替换主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，用 `-VerifyOnly` 核验双清单一致，并分别对**主机独立 stdio MCP**与**手机 Streamable HTTP MCP**运行新 `build/mcp-probe.exe` 完成三模式探针验证。
5. **责任边界声明**：依赖方 Agent、其托管 MCP 进程重启与重连默认由用户自行处理；验证记录严格区分磁盘替换、独立进程验证、依赖方切换以及已实现/已单测/已实测。
6. **推送、回复与关闭 Issue（#44、#45、#46）**：实现提交与归档提交均落盘且核验完毕后，推送 `master` 至 `origin`，并分别在 GitHub Issue **#44**、**#45**、**#46** 下回复实现提交 hash、归档验证记录路径、已落地契约 30 行为、实机/单测实证结果及明确未纳入边界，随后以 `COMPLETED` 关闭三个 issue。

### 8.3 记录

归档时写入 `maintenance/verification/observation-change-loading-and-snap-hint.md`，注明实现提交 hash、环境/设备信息、实际执行命令、清单哈希、双入口探针结果、最小实机闭环真实观测结果及三个 issue 的回复与关闭记录。

---

## 9. 已定与未决

### 9.1 已定

> 注：经 2026-11-25 对话讨论路线与边界后，用户明确授权主代理全权拍板技术细节并直接起草涵盖推送、回复与关闭 `#44`/`#45`/`#46` 的完整提案。

| 事项 | 结论 |
| --- | --- |
| 架构路线 | 选定 **R2**：保持 `protocol.Recovery` 6 元协议状态机纯净，在 `Observation` 上通过 `visual` + `tree_change` + `hint` 统一收敛 #44/#45/#46 |
| #44 事实边界 | 零额外截图 I/O；仅表达可证树变化事实 `unchanged` / `detail_only` / `matched_base`（常规变化省略），绝不伪造 `surface_changed` 或 `evidence=visual_only` |
| #45 分类范围 | 仅使用确定性结构特征（可见 `ProgressBar`、`quality=unstable`、大面积 `SurfaceView`/`TextureView`/`VideoView`/`PlayerView` 与校准后 `<30`/`<7` 稀疏树），不做中文业务关键词 `modal` 分类；`loading` 在 `delta` 永不省略 |
| #46 提示位置与边界 | 观察级只读建议 `Observation.hint`（`snap`/`wait_for`）由 `(publishedVisual, publishedTreeChange, hasValidImage)` 确定性推导；`target_changed` 交付新 `O` 时保留 `recovery=retry/O` 并在新帧命中视觉盲区时叠加 `hint`；静态错 ref `node_not_found` 不盲目提示 `snap`；已交付有效图时抑制 `hint=snap` |
| 契约与版本源 | `release/versions.json` 升级为 `release_version = "0.34.0"`、`contract_version = 30`、`android_version_code = 36`；外壳 `result v4` 与树块头 `tree v5` 保持不变 |
| MCP 发现预算 | 同步更新 `treeTextGuide`/`callerGuidance`/`act`/`snap` 描述，优先精简冗余并将 `baselineDiscoveryBytes` 上限透明调整至 `38200`（增幅 `<= +700 B`） |
| 交付与 Issue 收口 | 包含默认三项发布交付、独立归档提交、推送 `origin`，以及在 `#44`/`#45`/`#46` 逐一附实证回复后以 `COMPLETED` 关闭 |

### 9.2 未决

无（全部设计与交付范围已关闭定案）。

---

## 10. 实施顺序

1. **版本源、提案入库与协议层（步骤 1）**：确认本提案后，本提案文件（保持 `状态：提案，未实现。`）须在步骤 5 实现提交前单独提交或随步骤 5 实现提交一并跟踪；更新 `release/versions.json` 至 `0.34.0 / 30 / 36`（并按版本源约定同步 `source_commit`）且运行 `scripts/generate-version.ps1`；在 `internal/protocol/responses.go`、`decode_response.go` 新增 `Observation.TreeChange`/`Hint` 与闭集校验（直接复用 `internal/protocol/read_tree.go:887` 既有的 `ReadDelta.IsEmpty()`），补齐 `internal/protocol` 单测。
2. **`internal/observation` 纯算法实现（步骤 2，依赖步骤 1）**：升级 `visual.go` 为 `ClassifyVisual`（4 类优先级、跳过 `capture.Root` 的原始类名扫描与 `areaBelowPercent`/`areaAtLeastPercent`、校准阈值）并在 `read.go` 实现 `EqualCompactContent`（等价展开历史 `Chain`、忽略 `Ref`）与 `EqualPublicTreeContent`（忽略 `Ref`）；补齐正反向与边界单测。
3. **`internal/engine` 接线（步骤 3，依赖步骤 2）**：在 `capture.go`、`act.go`、`observe.go`、`start.go`、`wait_for.go`、`delivery.go`、`app.go`、`text_partial.go`（及测试辅助 `quality_test.go`、`baseline_selection_integration_test.go`）接入 `previousObs`、`ClassifyVisual` 锁外单次计算、`loading` 不省略去重与 `observationHint` 推导；补齐四态树变化、`wait_for` 早停、`partial`、`target_changed` 共存、图片抑制与撤销边界单测。
4. **`internal/mcp` 与 `scripts/mcp-probe` 同步（步骤 4，可与步骤 3 部分并行，依赖步骤 1）**：更新 `result_text.go`、`result_text_test.go`、`result_text_v4_test.go`、`result_manifest_test.go`、`schema.go`、`schema_test.go`、`catalog_test.go` 与 `scripts/mcp-probe/{result,result_v4,tree_body_consistency}.go` 及探针单测，确认工具目录指纹、`baselineDiscoveryBytes <= 38200` 与发现禁词门禁全部通过。
5. **正式文档同步、集中全测与文档审计（步骤 5，依赖步骤 3、4）**：更新 `README.md` 与受影响 `docs/`（`interface.md`、`mcp.md`、`architecture.md`、`client.md`、`integration.md`、`release.md` 及版本号同步涉及的 `control-protocol.md`、`phone-operations.md`、`runbook.md`）；主代理集中运行全仓 Go 单元测试（`internal/engine` 超时 `15` 分钟）；发起独立只读文档审计 subagent 审核全部变更 `docs/` 至 `clean`；确认工作区无未跟踪提案文件残留后**先提交实现（含 `docs/` 与保持 `提案，未实现` 状态的提案文件）**，形成干净实现提交 `HEAD`（`source_dirty=false`）。
6. **发布交付、独立归档、推送与 Issue 关闭（步骤 6，依赖步骤 5）**：
   - 在干净实现提交 `HEAD`（`source_dirty=false`）上依次执行 `scripts/build-release.ps1` + `scripts/verify-manifest.ps1` → 手机整组覆盖部署（保留 App 私有数据）→ 最小实机闭环 → 构建替换 Windows 主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 并 `-VerifyOnly` → 主机 stdio 与手机 HTTP 双入口独立探针；
   - 全部发布与探针核验通过后，再撰写 `maintenance/verification/observation-change-loading-and-snap-hint.md`、更新 `maintenance/status.md`、将本提案改为 `已实现` 并补全 §11、移入 `maintenance/proposals/implemented/` 且重写正文相对链接（`../../` → `../../../`，保留 §11 指向 `../../verification/...`）并校验目标存在性，**独立提交归档**；
   - 将实现提交与归档提交推送至 `origin`；
   - 分别在 GitHub Issue **#44**、**#45**、**#46** 评论回复实现提交 hash、验证结论与能力边界，并以 `COMPLETED` 关闭三个 issue。

---

## 11. 实现结果

实现提交：`31019462c98bb57d641caacd21aec0ad7dcf30fc`（`2026-11-25`，分支 `master`）。
实现与验证记录：[observation-change-loading-and-snap-hint.md](../../verification/observation-change-loading-and-snap-hint.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 协议字段、编码顺序与闭集校验（§4.4.1、§6.1） | `internal/protocol/responses.go`：`Observation.TreeChange`/`Hint`、编码顺序 `visual → tree_change → hint → tree`、`TreeChangeUnchanged/DetailOnly/MatchedBase`、`HintSnap/HintWaitFor`、`ValidObservationVisual/ValidTreeChange/ValidObservationHint`；`internal/protocol/decode_response.go`：三字段可选非空解码并拒绝显式 `null` 与空串；`internal/protocol/observation_classification_test.go` |
| 视觉分类四分支与阈值（§4.2、§6.2） | `internal/observation/visual.go`：`ClassifyVisual` 优先级 `loading → image_heavy → media_surface → sparse_tree`、`ProgressBar`/`SeekBar` 类名判定、媒体面后缀/5% 碎片/16 个上限/精确并集、阈值 80/8、40/8、25、80/10、30/7 与 `InferVisualHint` 兼容入口；`internal/observation/visual_test.go` |
| 忽略 `ref` 的树内容判等（§4.1.2、§6.1） | `internal/observation/read.go`：`EqualCompactContent`（`View`、忽略 `Ref`、历史 `Chain` 等价展开、无链零分配快路径）、`EqualPublicTreeContent`；`internal/observation/read_content_equal_test.go` |
| 引擎锁外比较与锁内派生（§5.1、§5.2） | `internal/engine/capture.go`：`publishTreeAttempt`/`captureAndPublish` 新增 `previousObs`、锁外 `ClassifyVisual` 与 `sameForeground`/`sameScreenGeometry` + 内容判等、锁内 `observationVisual`（`loading` 不省略）、`observationHint`、`tree_change` 四态判定与 `observationPublicLocked` 三字段投影；`internal/engine/act.go` 仅常规成功传被消费 `obs`，`observe`/`start`/`wait_for`/`app`/`act.delivery`/`text` partial 全传 `nil`；`tree_change_test.go`、`observation_hint_test.go`、`wait_for_test.go` |
| result v4、MCP 说明与独立探针（§4.4.2、§4.4.3、§6.1） | `internal/mcp/result_text.go`：`visual` 后新增 `change=`/`hint=`、不可打印 token 的 `json v1` 回退与跳过表、v3/v4 反射登记；`internal/mcp/schema.go`：`treeTextGuide`/`callerGuidance`/`act`/`snap` 指引；`scripts/mcp-probe/result.go`、`result_v4.go`、`tree_body_consistency.go`：独立列序、闭集、无树禁现与有效图抑制 `snap` 校验；`schema_test.go`、`result_manifest_test.go`、`result_text_test.go`、`result_parity_test.go` |
| 版本源、文档与整组发布（§6.5、§8.2） | `release/versions.json` 与 4 个生成文件 `0.34.0/30/36`；`README.md` 与 9 份 `docs/`；发布清单、手机整组覆盖部署、主机双 exe、双入口独立探针与验证记录 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| `equalIntPtr` 助手不存在 | 基线仓库无该函数；`sameForeground` 直接实现等价的可空 `task_id` 比较，并与公开前景投影共享未知 `reason` 归一化。提案 §4.1.1 的语义未变。 |
| 树内容判等的位置 | 提案 §5.1 伪码把判等放在基线候选循环之前；实现放在候选循环之后、提交锁之前。两者都在 `e.mu` 锁外，判定输入与结果不变，理由是与候选选择共用同一份只读快照。 |
| 发现说明预算与目录指纹 | `baselineDiscoveryBytes` 由 `37500` 调整为 `38200`（提案已定），实测总量 `38149 B`；因 `act`/`snap` 描述变化，工具目录指纹由 `df6afaa…` 变为 `25a09a66…`，`result_parity_test.go` 基线与 `docs/mcp.md` 同步；发现说明禁词门禁保持。 |
| 实机未构造 `loading`/`media_surface` | 已实测 `unchanged + hint=snap`、`snap` 不消费 O、`matched_base` 无 `hint`、`sparse_tree + hint=snap`（见验证记录）。`loading`/`media_surface` 未能在本机稳定构造：相机预览 `SurfaceView` 未进入 uiautomator 树、B 站视频卡片/分区页在虚拟屏未稳定进入播放器、本地视频播放器任务 `app.claim` 返回 `task_not_found`；两者判据由 `internal/observation` 与 `internal/engine` 单测覆盖，验证记录区分“已实测”与“仅单测覆盖”。 |
| 仅单测覆盖的边界 | `previousObs.Projection == nil` 的防御分支、`image_heavy` 不需要前台非 `none` 的边界、`wait_for` 第二帧 `unstable+condition` 与采样上限 `unstable+max_samples` 的 cue 反向对照，均只由单测覆盖；正常已发布观察都有投影，不受该防御分支影响。 |

---

## 附：本提案不改变的事项

- 不改变 `protocol.Recovery` 的 6 元 `entry` 词表（`retry`/`observe`/`status`/`end`/`user`/`update`）、抽象 `reference` 规则（`"O"|"P"|"none"`）与常规成功省略 `recovery` 的契约。
- 不改变 `Result.effect`（保持 `scroll` 专属与 `result v4` 省略）与 `status(P)` 操作视图不携带 `observation` 的语义。
- 不改变 `device.Capture` 原始节点、`Snapshot.Tree` 完整公开树节点集、`compact` 净体/二元中心点/T250 截断策略、`match.go` 保号逻辑与多基线池 `(12, 10, 5)` / `<80%` 差分门槛。
- 不改变 `act` 的静态规划、Tier 1–4 实时重验、5 个静态 `422` 错误码与 `text` 写入/提交语义，不改变 `wait_for` 的采样条件与稳定摘要，不改变 `snap` 的只读/不发布 `O`/不消费 `O` 语义。
- 不改变 `result v4` 头行格式、`tree v5` 普通树块语法与 `inspect v3` 详细补看语法。
- 不新增任何工具请求参数，不增加任何 ADB、u2 RPC 或截屏设备 I/O。
