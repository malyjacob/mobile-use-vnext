# 提案：受控 scroll 尝试、raw swipe 与观察质量分层（contract 10）

状态：已实现（实现提交 `2cc559a`，2026-11-12）。
实现与验证记录：[semantic-scroll-observation-quality.md](../../verification/semantic-scroll-observation-quality.md)。
确认记录：2026-11-12 用户已通过本提案；提案提交后由后续会话实施，结果见 §11。
基线提交：`fcfd593e6829389361f0d7c0eb59faccb3d6e378`（分支 `master`，2026-11-12）。
适用范围：`act` 的 `scroll` 新分支、`swipe` 的可选 `duration_ms`、所有成功发布的 `Observation` 的质量标记、`act scroll` 的滚动效果；HTTP/MCP/Go client/控制同步的 contract 10 对接；手机与主机整组重建；实现后的提交/推送/issue #10 闭环。
影响范围：`internal/protocol`、`internal/engine`、`internal/observation`、`internal/android`、`internal/eventlog`、`internal/mcp`、`internal/client`、`internal/httpapi`（契约、投影与测试）、`docs/interface.md`、`docs/architecture.md`、`docs/mcp.md`、`docs/release.md`、`docs/runbook.md`、`docs/client.md`、`docs/integration.md`、`docs/control-protocol.md`、`docs/phone-operations.md`、`release/versions.json` 与生成版本文件、`scripts/mcp-probe` 及发布/验证脚本、`maintenance/verification/`。
契约（`/api/*` 字段与 `contract_version`）：改变。新增 `act.operation.type=scroll` 及其 `container` 选择器、`swipe.duration_ms`、`Observation.quality`（`state`/`samples`/`stop_reason`）、`Result.effect`；`contract_version` 由 9 前进到 10。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/engine/act.go`、`capture.go`、`settle.go`、
`point.go`、`identity.go`、`internal/observation/build.go`、`internal/protocol/requests.go`、
`responses.go`、`internal/android/foreground.go`、`internal/mcp/schema.go` 与 `result_text.go`）。

---

## 0. 摘要

当前 `swipe` 只接受起点/终点，服务却把起点当作一个必须保持完整 `rawIdentity`（含 `Bounds`）
的节点来证明。滚动时正文段落移动、容器不动，于是同一段落在 11px 位移后仍被判
`target_changed`；动作后的不稳定观察又以普通 `ready` O 发布，下一次动作继续失败。

本提案把“受控滚动尝试”和“原始手势”分开：新增 `act.operation.type=scroll`，以“调用方
声明的阅读方向 + 容器 + 有界幅度”发起一次**受控滚动尝试**；服务只在几何合法、作用域可
证明的容器内选择起止点，不保证真实滚动方向、触摸消费者或业务安全。`swipe` 仍是原始手势，
只增加有界 `duration_ms`，不承诺滚动语义。同时把执行、滚动效果、观察质量拆成三个独立
事实：`outcome` 只表示派发/确认；`effect` 只表示观察到内容是否移动；`Observation.quality`
只表示树是否收敛、是否经过采样。三者互不改写。

本提案改变 `/api/*` 契约，contract 前进到 10，发行号前进到 0.10.0，Android versionCode
前进到 12；实施阶段必须重建手机整组产物、主机 `device-mcp.exe` 与 `mcp-probe.exe`，并按
整组发布顺序部署。当前阶段只写提案，不修改实现、不部署、不推送、不提前关闭 issue #10；
提交/推送/回复并关闭 issue #10 只作为实施完成后的门禁写入 §8.4/§9/§10。

---

## 1. 问题

### 1.1 现象与代价

主代理在用户授权的测试机上操作（0.9.0/契约 9、1080×2400 虚拟屏 102）。样本不是单一
知乎页面，至少包含以下对照，不能把知乎主样本当成跨应用结论：

- 知乎长回答主样本：固定 `540,2000 → 540,700` 连续 10 次，结果为
  `E R E R E R E R E R`；所有 R 都是 `target_changed`。前 8 次仍在同一正文，后 2 次涉及
  文末推荐。
- 设置场景：6 次动作均 E，6/6 收敛。
- B 站 gap 场景：4 次 E，其中后 2 次只有单帧、未形成比较。
- 内容重叠、同容器场景：静态同点存在 `view/text` 同深度候选、两层 scroll 祖先相同，
  形成“同一容器候选但叶节点歧义”的对照；本提案要求把它归约为同一容器而不是拒绝。
- 成功的 5 次后观察全部 `stable=false`；samples 为 5/5/4/3/1。前两次受 5 帧封顶，后几次
  受预算预测停止；samples=1 的那次没有形成第二帧对照，不能称稳定（本提案会把它归为
  `quality.unknown`，不是 `unstable`）。
- 公开滚动容器 `n372` 一直为 `[0,0,1080,2400]`；叶子/段落在观察发布后又移动
  11、18、48、120+ px。
- 另 4 次每次动作前 `observe(wait1500)`，全部 `executed`；另 4 次持旧后观察只等 3000ms
  不重新采集，为 `R E R E`。拒绝前后起点 text 节点的公开 ref/label 相同、bounds 不同；
  scroll ref/bounds 相同。
- 1 个 `observation_expired` 由调查代码准备导致，单独剔除。

这些是样本事实，不是跨应用普遍化：设置/B 站/内容重叠对照说明静态歧义、单帧质量和
动态移动需要在判据里分别处理。

代价不是一次错误码，而是一条循环：动作已派发并被记为 `executed`，O 已被认领；下一次动作
因内容移动被拒，返回新 O + `retry/O`；再下一次又可能被拒。每次循环消耗一次 act 工作窗口、
一次后观察和一次重新观察/重试；调用方为绕开失败，可能退化为手工猜坐标或盲目重试，进一步
扩大风险。

### 1.2 证据链

代码可确认的部分：

1. **swipe 只有 from/to，固定 300ms，静态检查不查 scrollable。**
   `Operation` 只有 `Type/Target/From/To/Key/Text`（[requests.go:49-56](../../../internal/protocol/requests.go#L49-L56)）；
   `swipe` 分支只校验 from/to、同屏、起点点解析（[act.go:232-240](../../../internal/engine/act.go#L232-L240)）；
   执行层固定 `swipeDurationMS = 300`（[foreground.go:104-110](../../../internal/android/foreground.go#L104-L110)、
   [209-212](../../../internal/android/foreground.go#L209-L212)）。
2. **swipe 起点仍走完整身份证明。**
   `revalidateActTarget` 对坐标目标调用 `provePointTarget`（[act.go:292-333](../../../internal/engine/act.go#L292-L333)）；
   `provePointTarget` 先用 `matchFreshNode` 要求完整 identity + 祖先唯一，再要求新鲜公开树
   把同一点解析到同一来源节点（[act.go:341-369](../../../internal/engine/act.go#L341-L369)）。
   `rawIdentity` 含 `Bounds`（[identity.go:27](../../../internal/engine/identity.go#L27)）。
3. **settle 摘要与重验同源，也含 bounds。**
   `sampleDigest` 的三层摘要包含节点身份链、公开树和前台/显示语境
   （[settle.go:110-124](../../../internal/engine/settle.go#L110-L124)）；
   身份指纹写入 bounds（[settle.go:293-318](../../../internal/engine/settle.go#L293-L318)）。
   滚动中的页面因此天然不收敛；预算/帧数耗尽时仍发布最后一帧（[settle.go:201-204](../../../internal/engine/settle.go#L201-L204)；
   [capture.go:317-348](../../../internal/engine/capture.go#L317-L348)）。
4. **stable/samples 只进日志，不进 O。**
   `logSettle` 明确写着“diagnostic only and never part of the published observation”
   （[settle.go:363-373](../../../internal/engine/settle.go#L363-L373)）。O 的公开形状没有质量字段
   （[responses.go:154-167](../../../internal/protocol/responses.go#L154-L167)）。
5. **后观察无论是否稳定都按普通 ready O 发布。**
   `Act` 在 `executed` 后调用 `captureAndPublish`，成功后把观察放进结果
   （[act.go:110-138](../../../internal/engine/act.go#L110-L138)）；O 的有效期从采集起点算
   （[capture.go:206-212](../../../internal/engine/capture.go#L206-L212)），不因未收敛而缩短。
6. **observe 不做收敛采样。**
   `observe` 只处理 `wait_ms` 后单次 `captureAndPublish`（[observe.go:51-70](../../../internal/engine/observe.go#L51-L70)）；
   settle 策略只对 `act`/`start`/`app` 阶段启用（[settle.go:95-107](../../../internal/engine/settle.go#L95-L107)）。
   4 次 wait1500 后成功，只能说明动作时锚点未变，不能说明 O 稳定。
7. **ref 不是稳定对象 ID。**
   连续性规则是“唯一候选才续号，歧义一律新号”（[match.go:8-30](../../../internal/observation/match.go#L8-L30)、
   [475-482](../../../internal/observation/match.go#L475-L482)）；容器 `n372` 的几何稳定不等于文章身份稳定。
8. **当前证据没有 window/activity/z-order 与滚动轴。**
   设备 `RawNode` 只有 `Scrollable bool`（[types.go:76-119](../../../internal/device/types.go#L76-L119)），
   XML 只读 `scrollable`（[hierarchy.go:201-218](../../../internal/android/hierarchy.go#L201-L218)）；
   `Axis` 字段存在于公开节点（[node.go:59-72](../../../internal/protocol/node.go#L59-L72)、
   [141](../../../internal/protocol/node.go#L141)），但 `newPublicNode` 从不赋值
   （[build.go:333-381](../../../internal/observation/build.go#L333-L381)）。

代码路径与现象一致的推断：起点叶节点 bounds 变化会直接令 `matchFreshNode` 失败；即使
`matchFreshNode` 通过，点在新树中也可能落到别的叶节点；点落空时又会走前台级回退
（[point.go:54-92](../../../internal/engine/point.go#L54-L92)、[point.go:13](../../../internal/engine/point.go#L13)，
[act.go:315-321](../../../internal/engine/act.go#L315-L321)）。这解释了“正文密集处更容易 R、
空白边距反而可能 E”的反常，也解释了容器不动而 R 仍发生。是否唯一根因仍需一次受控的
“容器不动、叶节点移动”复现；本提案不把推断写成实机结论。

### 1.3 为什么直觉会出错

- **删除 `Bounds` 或全局放宽 identity**：`identityOf` 同时服务 tap 重验与 settle
  （[identity.go:19-23](../../../internal/engine/identity.go#L19-L23)）；全局放宽会让移动后的
  tap 仍可能命中，并改变 settle 的定义。不能用一个共享判据换取 swipe 的吞吐。
- **只加 duration/延迟**：duration 只改变手势速度；不改变“起点叶节点完整身份”和
  “不稳定 O 照常授权”这两条。长 duration 甚至可能让内容在重验窗口内继续移动。
- **把后观察标记为 provisional、所有 act 都强制 observe**：这是另一条已知路线，会把
  每次动作退化成两次采集，且没有解决“如何授权受控滚动”的问题（已在前次 settle 提案中
  记录为否决路线）。
- **把 ref 当稳定对象**：同模板换文章可以保持容器 ref、bounds、class/resource 不变；
  ref 只能证明“会话内保守连续的公开单位”，不能证明文章或业务对象。
- **做一个通用 gesture DSL（路径/多点/曲线/压力）**：当前执行层只有两点的 `input swipe`
  （[foreground.go:197-219](../../../internal/android/foreground.go#L197-L219)），路径数组无法
  真实执行，除非另建执行栈；这会把一个滚动可用性问题升级为无限手势协议。

---

## 2. 目标与非目标

### 目标

- 新增 `scroll` **受控滚动尝试**：调用方表达阅读方向与幅度，服务在几何合法且作用域可证明
  的容器内选择起止点；不再要求起点叶节点完整身份不变；不保证真实方向/消费者/业务安全。
- 保留 `swipe` 作为原始手势，只增加有界 `duration_ms`；不承诺滚动语义、容器、效果。
- 发布 `Observation.quality`，区分 `stable`/`unstable`/`unknown`；发布 `Result.effect`，
  区分 `observed`/`not_observed`/`unknown`；二者与 `outcome` 独立。
- 保持 STOP、O、P、单工作位置、一次派发一个窗口、提交条件化、事实分层与 ref 保守连续性。
- 契约 10、发行 0.10.0、Android versionCode 12；实施阶段重建手机整组产物、主机 MCP
  适配器与探针，并按整组发布核验。

### 非目标

- 不证明“同一篇文章”“同一业务对象”或“没有 overlay”。当前树没有 window/activity/
  z-order，不能从 ref/bounds 推出这些结论。
- 不保证真实滚动方向、触摸消费者、业务安全、精确距离或到达边界。`Scrollable` 是 bool；
  `Axis` 没有生产者；方向由调用方声明，四向 `input swipe` 可能被 swipe-to-delete、轮播/
  翻页、系统边缘返回或下拉刷新消费。
- 不把 `effect=not_observed` 解释为失败，也不把 `executed` 解释为页面移动。
- 不新增通用 gesture/路径/多点/压力 API；不新增独立 `scroll` 工具或第二套 O/P/work。
- 不改 `identityOf` 的共享定义；不改 tap/text/ref 的完整身份重验。
- 不改 STOP 的“不等待”、O 的单向消费、P 的不重放、单工作位置与无普通队列。
- 不新增设备侧 window/activity/z-order/滚动轴/触摸消费者采集；若要对真实方向/消费者做
  保证，需另立原生 ACTION_SCROLL/轴证据等执行路线，不在本提案静默猜。
- 不保证所有 App 都能受控滚动：不暴露 `scrollable` 的容器会静态拒绝，仍可用 raw swipe。
- 首版不宣称“安全滚动”：破坏性手势只在可恢复专用 fixture/自建测试页验证，不对真实消息/
  邮件做可能删除项的实机手势。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 维持 swipe + 起点完整身份 + 不稳定 O 照常发布 | 零新增 | E/R 循环继续；调用方退化到手工坐标；每次失败消耗 act/O/observe |
| R2 参数化 raw swipe 或受控触摸 | `swipe` 增加有界 duration；或另立显式 scope/intent 的泛手势 | 小到中，保 raw 语义 | 仍要调用方自己声明 scope/容器；容器授权、效果/质量未解决；受控触摸/设备定向滚动需要另建执行能力 |
| R3 受控 scroll 尝试 + raw swipe 分开（本提案） | 新增 `scroll`；容器 + 调用方方向 + 有界幅度；raw swipe 保留并加 duration；加 quality/effect | 契约 10、容器发现、效果推断、跨应用测试、整组部署 | 无 scrollable / 嵌套轴 / 同模板换文章 / overlay 证据边界；方向/消费者不保证；效果只能推断 |
| R4 新建独立 scroll 工具 | 独立 MCP 入口，薄转发同一个 `Engine.Act` | 多一个目录/schema/取消/结果投影面，但不必复制权威 | 目录与模式面扩大；与现有 act 共用 O/P/work 时容易形成重复文案，不是“必然第二权威” |
| R5 放宽共享 identity | 去掉/弱化 bounds | 代码少 | tap 可能命中移动后的节点；settle 定义改变；违反“歧义即拒绝” |
| R6 设备定向滚动/受控触摸 | 用无障碍 scroll action 或受控 MotionEvent，而非 `input swipe` | 可能更接近语义 | 当前实现没有该执行面；需要控制 App/固定制品与轴/方向证据，另立提案；本提案不采用 |

选择 R3。理由不是“新功能更多”，而是它把授权单位从“起点叶节点”换成“可滚动容器”，
与受控滚动意图一致；同时把 raw 手势保留为低层能力，避免一个分支同时承担两种意图。R2 可作为
raw 手势保真的补充（包括泛手势 + 显式 scope/intent 的可行路线），但不能作为滚动可用性的
终态；R1/R5 不解决根因；R4 可以薄转发同一个 Engine，只是目录/文案面更大，因此选 act 分支；
R6 是另一条执行能力路线，当前没有执行面，不在本提案假装可用。设备定向滚动/受控触摸若要
做，需另立提案并补设备证据。

---

## 4. 判据

### 4.1 `scroll` 的输入

```json
{"type":"scroll","container":{"ref":"n372"},"direction":"down","amount":"page"}
```

| 字段 | 必填 | 规则 |
| --- | --- | --- |
| `type` | 是 | 固定 `scroll` |
| `container` | 是 | `{"ref":"n..."}` 或 `{"x":int,"y":int}`；语义是滚动容器选择器；显式 `null` 拒绝 |
| `direction` | 是 | `up`/`down`/`left`/`right`，大小写敏感；含义是“阅读/视口前进方向”；显式 `null` 拒绝 |
| `amount` | 否 | `page`（默认）或 `half`；不接受其他值；显式 `null` 拒绝 |
| `target`/`from`/`to`/`key`/`text`/`duration_ms` | 否 | 任一出现即静态拒绝 |

`direction` 的映射必须在文档写死，避免“scroll down”歧义：

- `down`：查看更靠后的内容；手势从容器下部向上部。
- `up`：查看更靠前的内容；手势从容器上部向下部。
- `right`：查看更靠右的内容；手势从容器右部向左部。
- `left`：查看更靠左的内容；手势从左部向右部。

`container` 解析：

- `ref`：必须**直接**指向公开节点，且该节点 `role=scroll`、`actions` 含 `swipe`、非 root、
  非 context、有 bounds、`enabled` 不为已知 false。**不做“叶 ref 向上晋升”**；ref 不是
  可滚动容器即 `422 unsupported_node_action`。这样叶节点的 enabled/几何不会左右容器授权。
- 坐标：取包含该点的**所有**公开候选节点（不是只取唯一最深叶），对每个候选找最近的
  可滚动祖先（含自身），再按容器原始节点去重：
  - 只有一个不同容器：使用它；
  - 多个不同容器（例如同点同时落在外层长列表和内层横向列表）：`422 ambiguous_point`，
    要求调用方显式给 `container.ref`；
  - 没有容器：`422 unsupported_node_action`；
  - 多个内容候选（如同点同深度的 view/text）归约到**同一个**容器时，不算歧义。
- 不接受“自动选择全屏最大的滚动容器”这类隐式猜测；点模式只做上述候选归约。
- 容器 bounds 必须非空、可裁剪、有正面积；几何可执行谓词见 §5.3，静态阶段就调用同一
  函数，不可执行则认领前拒绝。
- 不校验滚动轴：设备只有 `Scrollable bool`，`direction` 是调用方声明；嵌套横/竖容器
  无法由服务证明支持该方向。多个不同容器时宁可拒绝，不猜轴。

### 4.2 `swipe` 的输入

```json
{"type":"swipe","from":{"x":500,"y":1800},"to":{"x":500,"y":600},"duration_ms":300}
```

- `from`/`to` 必填，同屏、不能相同；`target`/`container`/`key`/`text`/`direction`/`amount` 任一出现即拒绝。
- `duration_ms` 可选，整数 50～2000，默认 300；显式 null、0、负数、非整数、越界拒绝；
  只有 `swipe` 接受该字段。
- 起点仍沿用现有“唯一最深点 + 完整 identity + 祖先 + 同点解析”的证明；raw swipe 不承诺
  滚动，因此内容移动时仍可 `target_changed`。调用方要受控滚动尝试应使用 `scroll`。
- `ambiguous_point` 的错误码/行为不变；当前 [point.go:122-125](../../../internal/engine/point.go#L122-L125)
  的“请改用 ref”对 raw swipe 不可执行，改为操作相关文案：
  “起点落在多个同级节点上；raw swipe 请调整起点，或改用 scroll 的 container 指定容器”，
  不再提示“改用 ref”（raw swipe 不接受 ref）。这只修提示，不解决 #7 的完整歧义/重验问题。

### 4.3 容器授权锚与真实注入轨迹

`scroll` 的授权单位是**观察时的容器**，不是起点叶节点；但最终注入的起点/终点也必须被证明
仍归这个容器：

1. 静态解析得到的容器原始节点保存为 `containerIndex` 与其 `rawIdentity`/祖先链；ref 模式
   直接是容器，点模式是候选归约后的唯一容器。
2. 按 **§5.3 的同一几何可执行谓词**从观察容器计算轨迹；静态不可执行立即拒绝，不认领。
3. 认领 O 后，新鲜采集只对容器做 `matchFreshNode`（完整 identity + 祖先唯一）；容器 bounds
   变化视为 `target_changed`。
4. 从新鲜容器 bounds 用**同一个** §5.3 函数重算轨迹；fresh 不可执行走 `target_changed` 的
   新帧交付，不在 claim 后另造 422 分支。
5. **校验实际注入起点和终点**（不是只校验调用方给的点）：
   - 用新鲜公开树取实际起点处的所有公开候选，映射到最近的可滚动作用域；
   - 实际起点和实际终点的**最内层**可滚动作用域都必须是选定容器 C；外层包裹祖先允许；
     C 的内层后代、不可比或其它 scroll scope 拒绝；
   - 实际终点必须仍在选定容器内；
   - ref 和 point 两种选择器都要做这一步；ref 路径也必须有真实注入轨迹校验。
6. 首版只使用容器中心线这一条固定轨迹，不搜索替代路径。若中心线起点/终点被内层 scope
   占据，拒绝并要求调用方显式给内层 `container.ref`；不以未定义的有界搜索伪装解决。
7. 前台包/task、显示代次、采集龄、提交门禁仍沿用现有检查。
8. 已发布、且位于同一容器子树内的**非滚动** overlay 可能只改点解析的最深节点、不改变
   最内层 scroll 作用域；这种 overlay 不会由 scope 证明自动排除，属于 §7.2 的证据边界。
   已发布且改变实际起终点 scroll scope 的 overlay 会被拒绝。

点模式不再要求“唯一最深叶”：同点两个同深度内容节点只要归约到同一容器，就不算歧义；
只有出现多个不同 scroll 作用域时才要求显式容器 ref。这修正了静态 B 站场景中
`view/text` 同深度候选导致的无谓拒绝，但保留“多 scroll 作用域不可猜”的拒绝。

首版不证明“未上报 overlay 不存在”，只证明已上报的 scope 关系。`scroll` 仍比 tap 的
“点=目标”更松（不看叶节点 identity），但比 raw swipe 更严（必须有唯一容器且实际起终点
仍归该容器）。

### 4.4 `Observation.quality`

新增：

```json
"quality": {"state": "unstable", "samples": 5, "stop_reason": "max_samples"}
```

| 字段 | 规则 |
| --- | --- |
| `state` | `stable`/`unstable`/`unknown` |
| `samples` | 本次 settle 的有效可比较帧数，非负整数；默认 `act` 无图最多 5 帧，`act(image=true)` 的绑定刷新可再多 1 帧（最多 6）；`start` 最多 4 帧、`app` 最多 5 帧，由固定策略界定；没有 settle 为 0 |
| `stop_reason` | `stable`/`max_samples`/`budget`/`unreadable`/`not_sampled` |

状态由**最终发布帧参与的最新比较**决定，不是“历史上有过一次相等对”：

- `unknown`：`samples < 2`，或根本没有 settle（`observe`、重验失败交付）。只有 1 帧时
  无法形成相等/不等判断，不能叫 `unstable`。
- `unstable`：`samples >= 2`，且最终发布帧没有形成一个“与紧邻前一帧三层摘要相同”的最新
  相等对。旧 pair 曾经相等、但 image 刷新后的最终帧不同且预算耗尽，不能报 `stable`。
- `stable`：最终发布帧与紧邻前一帧形成相等对，且满足 `MinSamples`；`stop_reason=stable`。
- `stop_reason=not_sampled`：`observe`/交付路径未 settle；`samples=0`。
- `unreadable`：某次采样读取失败，提前结束；如果此前已有 ≥2 帧且最终帧未相等，`unstable`；
  如果不足 2 帧，`unknown`。读取失败不增加 `samples`。
- `samples` 不设一个会与 image 刷新冲突的硬 0–5 上限；默认 `act` 5 帧 + 绑定刷新 1 帧 =
  最多 6，`start`/`app` 按各自固定策略。公开计数只受固定策略与刷新次数约束，不受请求控制。

规则：

- `start` 首个观察、`act` 后观察、`app` 五个变更后观察按现有 settle 结果填；`observe`
  和重验失败交付填 `unknown`/`not_sampled`。不新增 settle 到 `observe`，也不新增请求参数。
- `quality` 是观察材料事实，不是输入授权。`unstable`/`unknown` 的 O 仍可发布、仍可被认领；
  是否执行由动作自己的判据决定。`scroll` 不要求 `stable`；tap/text/raw 仍按各自目标证明。
- `quality` 不改变 `status`、`actionable_until`、基线确认或 O 状态机；`complete+unstable`
  与 `partial+stable` 都是合法组合。
- 失败观察没有 tree 时不发布 quality；只发布错误/图片材料。
- `act(image=true)` 的后观察刷新分支必须显式参与同一质量计算，刷新**计入** `samples`：
  - 刷新帧与收敛帧摘要相等且 `samples+1 >= MinSamples`：`stable=true`、`samples++`、
    `stop_reason=stable`；最终发布帧就是该刷新帧；
  - 刷新帧不相等：`stable=false`、`samples++`，在剩余预算内继续 settle，最终
    `stop_reason` 取 `max_samples`/`budget`/`unreadable`；旧 pair 相等不能救回 `stable`；
  - 刷新读取失败：保留旧帧与旧 run，不增加样本；质量按旧 run 如实计算；
  - 默认 `act` 无图最多 5 帧，有图刷新最多 6 帧；不扩大原 settle 预算，不取消原图树绑定
    刷新，不把“刷新相等”仍记成旧的 `stable=false`。
- 公开编码顺序固定为 `status,id,origin,revision,captured_at,actionable_until,quality,
  screen,foreground,tree,missing,image,error`；`quality` 是对象，成员固定为
  `state/samples/stop_reason`。`samples=0` 与 `stop_reason=not_sampled` 是显式事实，
  必须编码，不得用 `omitempty` 省略；`effect.state` 同理不得省略。
- `quality` 解码：对象缺失可用于 failed/旧形态；对象出现时 `state`/`samples`/`stop_reason`
  必填，显式 `null`、类型混用、未知枚举、负 `samples` 拒绝；`samples=0` 是合法值，不能被
  当成“未出现”。`effect` 解码：对象缺失表示无该事实；出现时 `state` 必填且枚举受限，
  显式 `null`/未知值拒绝。

### 4.5 `Result.effect`

首次受理的 `scroll` 结果及仍保留原 `ActAction=scroll` 的 P，在 `outcome=executed`
（含 executed_revoked）或 `uncertain` 时出现；`not_executed` 不出现。原操作事实已不可用的
closed/淘汰等短结果按 §4.6 允许省略，不能据重放 payload 补造 effect。新增：

```json
"effect": {"state": "observed"}
```

| 值 | 含义与证据 |
| --- | --- |
| `observed` | 至少 2 个匹配内容单元沿请求轴出现一致位移（≥2px、同号、至少 75% 匹配单元一致）；**不要求 100% 覆盖**，低覆盖本身不否定正证据；只表示“观察到内容位移”，不证明动作因果、方向、距离或滚动成功 |
| `not_observed` | 后观察 `quality=stable`，且**定义充分**：前后内容单元计数相同、100% 唯一匹配、没有单元中心位移 >1px、没有单元尺寸变化 >1px；不证明到达边界或手势失败 |
| `unknown` | 无后观察树、后观察失败/撤销、容器不唯一/变化、匹配不足 2 个、尺寸变化、低覆盖且未形成足够一致位移，或一致位移比例不足 75% |

判据必须可判定，不能只用“少量静止幸存锚点”证明整容器没动：

1. **前后帧**：派发前新鲜重验帧（`fresh`）与后观察的完整 capture；不额外读取设备。
2. **容器匹配**：在 post capture 中按不含 bounds 的容器内容键（package/class/resource-id/
   text/description）+ 祖先唯一匹配 pre 容器；匹配不到/不唯一，或容器 bounds 的
   left/top/right/bottom 任一变化 >1px，`unknown`。
3. **内容单元**：容器子树内、可见、非 context、非输入/密码、bounds 在容器内、有非空
   label 或公开 resource 的公开节点；只取“叶子内容单元”（其子树内不再有另一个内容单元），
   避免父子重复计数；空结构节点、纯容器、滚动容器自身不计数。
4. **匹配键**：源节点 `package/class/resource-id/text/description`，忽略 bounds；前后两侧
   都必须唯一；只有两侧都可见且裁剪后在容器内的单元参与。匹配数 `<2` -> `unknown`。
5. **覆盖**：`coverage = matched / max(preUnits, postUnits)`。`not_observed` 必须
   `preUnits == postUnits == matched >= 2`（即 100% 覆盖，没有新出现/消失/换出的内容）。
   固定标题保留、其余内容全部换出视口时 coverage 很低，必须 `unknown`，不能因为标题没动
   就报 `not_observed`。`observed` 只要求足够的**同向移动匹配单元**，不要求 100% 覆盖；
   低覆盖但有 2 个以上同向移动单元仍是正证据，只有低覆盖且没有足够一致位移才 `unknown`。
6. **位移**：用裁剪后 bounds 中心沿请求轴计算。`observed` 要求 ≥2 个匹配单元同号移动
   ≥2px，且同号比例 ≥75%；少数反向位移可以容忍（例如 3/4=75% -> observed，2/3=66.7%
   -> unknown）；只有同号比例 <75% 或没有足够移动才 `unknown`。这里只要求同号，不要求与
   请求方向同号（回弹/反向位移也是“观察到位移”）。
7. **尺寸/裁剪**：任一匹配单元宽或高变化 >1px -> `unknown`（可能是 resize、动画或裁剪）；
   `not_observed` 还要求所有匹配单元沿请求轴位移 ≤1px。
8. **质量联动**：`not_observed` 只在 `quality=stable` 时成立；`observed` 可以在
   `quality=unstable` 时成立（看到位移即可），但不得把位移写成动作因果。
9. **CPU/取消**：效果计算在 post O 提交之后、状态锁之外，只遍历前后两份已取得 capture，
   复杂度 O(nodes_pre + nodes_post)，不访问设备、不重试、不复制树；若 work ctx 已取消，
   或超过 `effectCPUBudgetMS`（初始 100ms）的纯 CPU 预算，直接 `unknown`。效果计算不得
   回写已终结的 P（见 §4.6）。
10. **公开形状**：`effect` 只含 `state`；coverage、匹配数、尺寸变化、原因只进受控日志。
    结果编码顺序固定为 `operation_id,operation_state,outcome,phase,effect,target_reference,
    request_closed,...`；`effect` 不改写 outcome/error/recovery/observation.status。
11. `executed + effect=unknown` 是合法结果，不改成 `uncertain`；`effect` 是本次结果，
    `status(P)`/冻结只复制已保存值，不重算。

固定反例：2 个固定标题保留、其余可滚动内容全部换出视口且 post stable -> `unknown`；
父子内容重复 -> 只算叶子，不重复；容器/单元 resize -> `unknown`；方向冲突 -> `unknown`；
无后树 -> `unknown`。阈值是初始校准值，未实机校准前不得写成已验证。

`effect` 是本提案新增的**有界例外**，不是通用反馈框架：只有 `scroll` 有，只有 `state`
三值，不返回命中节点、触摸消费者、业务成功、距离或边缘。设计上要避免把它扩成通用
位移/命中反馈面。

### 4.6 P 事实、冻结、去重与 effect 存在性

`status(P)`/重复 P 当前只复制 `operationState` 的短事实（`operationViewLocked`，当前
[engine.go:1061-1099](../../../internal/engine/engine.go#L1061-L1099)），不会重算、不会附 O；
`finishOperation`（[operations.go:135-164](../../../internal/engine/operations.go#L135-L164)）
也当前不保存 action/effect，冻结投影（[retirement.go:525-538](../../../internal/engine/retirement.go#L525-L538)）
只替换 Result。effect 必须进入同一份有界 op 事实，但不能把“缺失”伪装成“未移动”：

- **首次受理的 scroll**：新 P 通过静态 scroll 解析后、acquire/claim/dispatch 之前，在短锁内
  条件化记 `ActAction=scroll`（重复 P 已返回原视图，不读新 payload 补旧事实）。这样
  `dispatch uncertain`、`executed_revoked`/STOP、post 失败等早退也能被 status(P) 识别为
  scroll；早退没有计算结果时 effect 投影 `unknown`，不是 `not_observed`。
- **effect 提交**：只在 `op.State==OperationRunning`、未冻结，且同一短锁内
  `workAuthorizedLocked(work, now)`、ctx、拥有者/会话/显示代次等既有门禁仍成立时，在
  `finishOperation` 之前写入；不满足则不升级。effect 计算在锁外，提交在短锁内，不做设备
  I/O/树算法。冻结后的迟到 CPU 结果不能回写。
- **retained P**：只有仍保留 `ActAction=scroll` 的原操作，在 `executed`/`uncertain` 且没有
  已提交 effect 时投影 `unknown`；`not_executed`/未派发不出现 effect。status/重复 P 只复制
  已保存值，不附 observation、不重算、不重新采集。
- **closed tombstone**：`closedKnown=true` 的 status 返回 `not_executed`/`closed_before_admission`；
  `closedKnown=false` 的 status 返回 `operation_unknown`（[status.go:96-106](../../../internal/engine/status.go#L96-L106)）。
  同一 closed P 再次登记时，[operations.go:71-80](../../../internal/engine/operations.go#L71-L80)
  按 `closedOutcome` 返回 `not_executed`（known）或 `uncertain`（unknown），均带
  `request_closed`；两者都没有原 action/effect，允许省略。缺失不是 `not_observed`，不得用
  重放 body 猜原 action 再补 `unknown`。
- **已淘汰 finished P**：`evictOperationsLocked` 可删除 finished op
  （[operations.go:99-121](../../../internal/engine/operations.go#L99-L121)）。若现有接纳把完全
  淘汰的 P 视为新登记，就按新操作事实返回；不得声称恢复了旧 P、旧 action 或旧 effect，也
  不扩大有界去重承诺/不改 ledger。进程重启后同样只按当前实例事实回答。
- effect 不写 LEDGER；P 记录本身有界。`closed`/`request_closed`/已淘汰或原事实不可用时，
  effect 允许整体省略；省略不表示“未移动”。

### 4.7 认领顺序与 busy

静态检查通过后**先 acquire work，再在锁内重验 O/session/current 并 claim**；不是先 claim
后 acquire。随后才做设备侧新鲜重验。busy/admission 失败时 O 保持 `ready`、零采集、
零派发、零 claim；这与现有 `act.go` 的 `staticActCheckLocked` → `acquireWork` →
`claimObservationLocked` 顺序一致。静态拒绝同样不认领。重验失败不归还已 claim 的 O。

---

## 5. 算法

### 5.1 静态检查（认领前）

```text
if O not ready/current/agent/session -> existing error
if container missing -> invalid_request
if direction missing/unknown -> invalid_request
if amount present and not in {page,half} -> invalid_request
resolve container in O.Projection.Tree:
  ref -> node must be role=scroll + actions contains swipe (no ancestor promotion)
  point -> all public nodes containing point; map each to nearest scroll ancestor;
           dedupe by container raw node; exactly one scope -> use;
           >1 distinct -> ambiguous_point; none -> unsupported_node_action
if container bounds empty/not positive -> unsupported_node_action
if !scrollGeometry(container.bounds, direction, amount) -> unsupported_node_action
store container index/identity/ancestry and selector
```

不认领、不采集、不派发；静态拒绝保持 O `ready`，按现有 `retry/O` 规则返回。
`scrollGeometry` 是 §5.3 的同一个纯函数，静态与 fresh 共用。

### 5.2 工作位、认领与新鲜重验

```text
static checks (above), no claim
acquire single work
  if busy/admission error -> not_executed; O stays ready; no capture; no claim; no dispatch
lock: recheck O/session/current; claim O one-way
ProbeDisplay + Capture (existing, includes foreground)
if foreground mismatch -> existing foreground_changed
match fresh container by full rawIdentity + ancestry
if not unique -> target_changed
if !scrollGeometry(freshContainer.bounds, direction, amount) -> target_changed through delivery
compute start/end from freshContainer via same scrollGeometry
Preview fresh public tree
verify ACTUAL start and ACTUAL end:
  both points' innermost scroll scope must be the selected container C
  outer wrapping ancestors are allowed; C's inner descendants or other/incomparable scopes reject
  actual end must still be inside C
  any inner descendant or incomparable/non-ancestor scope at either point -> target_changed
if no safe path -> target_changed via existing fresh-frame delivery; no dispatch
```

`Preview` 不推进 ref 连续号；容器匹配/scope 校验只用于授权，不改变公开 ref。busy 时
O 仍是 `ready`，下一次可用同一 O + 新 P；重验失败不归还已 claim 的 O。首版不搜索替代
轨迹；中心线被内层 scope 占据就拒绝，调用方改用内层 `container.ref`。

### 5.3 轨迹（静态与 fresh 共用的纯函数）

服务端固定策略，不是请求字段。`scrollGeometry(rect, direction, amount)` 必须被静态检查与
新鲜重验调用同一份实现，禁止两处各算一套：

```text
axis   = vertical for up/down; horizontal for left/right
extent = rect.height or rect.width              // 已裁剪到屏幕的整数矩形
margin = max(24, extent/10)                     // 整数，向下取整
usable = extent - 2*margin
if usable <= 0 -> fail
travel = floor(usable * (60 for page, 30 for half) / 100)
if travel < 48 -> fail
start/end on the rect center line:
  down:  start = bottom-margin, end = start-travel
  up:    start = top+margin,    end = start+travel
  right: start = right-margin,  end = start-travel
  left:  start = left+margin,   end = start+travel
if start or end is not inside the half-open rect or screen -> fail
// 不做 clamp：clamp 会改变方向/长度；越界直接 fail
duration = scrollDurationMS (initial 400ms; service constant)
```

反例（防止静态通过、claim 后才拒）：`extent=100, margin=24, usable=52, page travel=31`
<48，必须在认领前返回 `unsupported_node_action`；half 同理。fresh 阶段同一函数失败则走
`target_changed` 的新帧交付，不出现 claim 后的第二种“几何不可执行”错误。
`scroll` 不暴露 duration/速度/路径；`swipe` 的 `duration_ms` 只影响原始手势。

### 5.4 派发、后观察、质量与效果

```text
after static scroll resolution: conditionally record ActAction=scroll (new P only)
beginUIDispatch(work, "swipe")       // 执行原语仍是 swipe，账本 kind 不变
Device.Swipe(display, start, end, scrollDurationMS)
if not dispatched -> not_executed (existing); no effect
if receipt unknown/error -> uncertain; project effect=unknown; no replay
if executed:
  captureAndPublish(..., phase="act", settle policy Act)
  if stopped before post commit -> executed + executed_revoked; project effect=unknown; no O
  if post failed -> executed + post_observation_failed; project effect=unknown
  else -> publish O with quality; compute effect(fresh, postCapture)
          conditionally commit effect before finishOperation (ActAction already recorded)
// If work was revoked or P frozen, never write back; retained scroll facts project unknown.
```

`scroll` 的 `beginUIDispatch` 记录 `kind="swipe"`，因为物理原语是 swipe；语义
`ActAction=scroll` 是 operation/log 事实，不声称当前 ledger/P 结果已有 action 字段。
`ActAction` 必须早于 claim/dispatch 记录，否则 receipt unknown、STOP、post 失败等早退的
status(P) 无法识别 scroll。有后树且工作授权仍成立时，按 §4.5 计算并在 `finishOperation`
前条件化提交 effect；无后树/STOP/后观察失败或提交门禁已失效时，依据已保存的原 action
投影 `unknown`，不为写入 unknown 绕过撤权或冻结。effect 只写有界 op 事实（`state` 三值），
不附 O/tree/坐标；提交同一短锁内还要检查工作授权/ctx/拥有者/会话/显示代次，冻结后不升级。

quality 由 settle run 投影（§4.4）：`samples<2` 为 `unknown`，`samples>=2` 未相等才
`unstable`；image 刷新相等必须置 `stable`，读取失败不增加样本。effect 按 §4.5 的
覆盖/尺寸/位移规则计算，CPU/ctx 边界见该节。

### 5.5 失败、撤销、超时、预算的“永远返回什么”

| 情形 | outcome | effect | observation | recovery |
| --- | --- | --- | --- | --- |
| 静态拒绝（无容器/参数/几何不可执行） | not_executed | 无 | 无 | 现有 retry/O；O 仍 ready |
| busy/admission 失败 | not_executed | 无 | 无 | 现有；O 仍 ready、零 claim |
| 重验容器变化/实际起终点 scope 变化/几何不可执行 | not_executed | 无 | 有可用新帧则随结果交付 | 现有 retry/O |
| 前台/显示/采集失败 | not_executed | 无 | 按现有交付规则 | 现有 observe/P 或 end |
| 派发未发生 | not_executed | 无 | 无 | 现有 |
| 派发回执未知 | uncertain | unknown | 无 | status/P |
| 已派发、后观察成功 | executed | observed/not_observed/unknown | 新 O | 现有 finished |
| 已派发、后观察失败 | executed | unknown | failed observation | observe/P + post_observation_failed |
| 已派发、后观察前 STOP | executed | unknown | 无 | status/P + executed_revoked |
| 效果计算失败/覆盖不足/CPU 边界 | executed | unknown | 正常 O | 不变 |
| P 冻结前未提交 effect | 按冻结 outcome | unknown（若 scroll 且 executed/uncertain） | 无 | status/P |

不重放、不回滚、不因 effect 改 outcome；STOP 后迟到候选不发布。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/protocol/requests.go` | `Operation` 增加 `Container`/`Direction`/`Amount`/`DurationMS`；`scroll` 与 `swipe.duration_ms` 严格解码；分支互斥 |
| `internal/protocol/encode.go` | act operation 编码增加 `scroll` 与 `duration_ms`；拒绝未知 type |
| `internal/protocol/node.go` | 不新增 `Axis` 生产者；滚动轴仍不发布 |
| `internal/protocol/responses.go` | `Observation.Quality`（`state/samples/stop_reason`）、`Result.Effect` 及固定编码顺序 |
| `internal/protocol/decode_response.go` | Observation 解码（当前 211–250）补 `quality` wire；Result 解码（319–377）补 `effect`；`Quality`/`Effect` 的 `UnmarshalJSONFrom`：显式 null、必填、类型/枚举、`samples=0` 全部校验 |
| `internal/client/client.go` | 473–481 的 `json.Unmarshal` 依赖上面的 response decoder；补客户端 roundtrip，不能只改服务端编码 |
| `internal/engine/act.go` | `scroll` 分支静态检查、工作位、认领、重验、派发、结果组装；raw swipe duration 透传；busy 不 claim |
| `internal/engine/scroll.go`（新） | 容器候选归约、容器新鲜匹配、`scrollGeometry` 共用纯函数、实际起终点 scope 校验、效果推断 |
| `internal/engine/point.go` | scroll 点候选解析可复用点工具；raw swipe 原判据保留；ambiguous_point 的 raw swipe 文案改为可执行提示（不暗改错误码） |
| `internal/engine/capture.go` | 发布时带入 quality；重验失败交付固定 `unknown`；image 刷新分支参与质量计算 |
| `internal/engine/settle.go` | 由 `settleRun` 投影 quality（samples/stop_reason）；不改采样预算/判据 |
| `internal/engine/engine.go` | `observationState` 加 quality；`operationState` 加有界 `ActAction`/`Effect`；`operationViewLocked` 复制不重算 |
| `internal/engine/operations.go` | `finishOperation` 前条件化提交 effect；冻结后拒绝升级 |
| `internal/engine/retirement.go` | `applyFrozenResult` 保留已存 effect/unknown，不接迟到 CPU |
| `internal/eventlog/log.go` | 白名单增加 scroll/effect/quality/reason 等受控键；`act.point`/`observation.settle`/`scroll.*` 带 operation_id（必要时 observation_id） |
| `internal/android/foreground.go` | `Swipe` 增加 duration 参数；默认 300；校验范围 |
| `internal/mcp/schema.go` | act schema 增加 `scroll`/`container`/`direction`/`amount`/`duration_ms`；严格描述；`treeTextGuide`/act description 明确“滚动用 `scroll.container`、`actions=swipe` 只是源能力，不是 raw swipe 授权” |
| `internal/mcp/tree_text.go` | 树块仍输出 `actions=swipe`（不新增 action 枚举）；只在工具引导/结果说明中把 scroll 作为首选滚动表达 |
| `internal/mcp/result_text.go` | 文本投影增加 `effect.*`、`observation.quality.*`（state/samples/stop_reason） |
| `internal/mcp/*_test.go` | schema/catalog/result manifest/golden 更新 |
| `internal/client` | 强类型结果包含 effect/quality；不新增加速度/等待参数 |
| `internal/httpapi` | 不新增路由；沿用 act 64 KiB 与交付预算 |
| `internal/engine/engine_test.go`（fakeDevice） | 记录 Swipe 调用/起终点/duration；拒绝=0、允许=1、同 P 重放仍 1 |
| `scripts/mcp-probe`、`scripts/build-host-tools.ps1` | contract 10、catalog、effect/quality 断言；重建两个 exe；补 clean/HEAD/phone-manifest/companion 显式门禁，不能只靠脚本返回 0 |
| `scripts/verify-manifest.ps1` | 显式要求构建树 clean、source_commit==实际 HEAD、phone manifest 完整且 companion 比对 PASS（不接受 SKIP）；现有脚本 146–153 不强制 dirty/HEAD，需新增/包装断言 |
| `scripts/deploy.ps1` | 移除旧项目兼容动作：删除 `SkipOldServiceStop` 开关及其默认块（`deploy.ps1:296-303` 的旧 `service.sh`/`pkill -f "mobile-use-service serve"`），删除 `am force-stop com.mobileuse.control`（310）；保留并精确停启本仓库 `com.mobileuse.vnext` 与 `device-service`。同步检查调用方、`docs/` 与 `maintenance/decisions.md` 旧描述，不执行旧命令 |
| `release/versions.json`、生成文件 | 0.10.0 / 10 / 12；`generate-version.ps1` 生成 Go/Java/XML/Manifest |
| `docs/*`、`maintenance/verification/*` | 契约、架构、MCP、发布、运行手册、验证记录 |

### 6.2 配置与取值

| 常量 | 初始值 | 规则 |
| --- | --- | --- |
| `scrollPageRatio` | 0.60 | 容器可用长度的 page 行程比例，服务固定 |
| `scrollHalfRatio` | 0.30 | half 行程比例 |
| `scrollEdgeMargin` | `max(24, extent/10)` | 整数、向下取整；避免贴边/系统手势区 |
| `minScrollTravel` | 48px | 静态/fresh 共用几何谓词，小于即不可执行 |
| `scrollDurationMS` | 400ms | 受控滚动尝试固定时长，初始校准值，不是请求字段 |
| `defaultSwipeDurationMS` | 300ms | 原 swipe 默认，未传 duration 时使用 |
| `minSwipeDurationMS` / `maxSwipeDurationMS` | 50 / 2000ms | 公开 `duration_ms` 边界 |
| `effectMinAnchors` | 2 | 少于 2 个唯一匹配内容单元 -> unknown |
| `effectMinDisplacementPx` | 2 | 沿请求轴的最小中心位移 |
| `effectConsensus` | 0.75 | 同号匹配单元比例 |
| `effectNoMoveTolerancePx` | 1 | stable 后观察下的“未观察到移动”阈值 |
| `effectMaxSizeDeltaPx` | 1 | 单元宽/高变化超过即 unknown |
| `effectNoMoveCoverage` | 1.00 | not_observed 要求前后内容单元 100% 唯一匹配 |
| `effectCPUBudgetMS` | 100ms | 效果纯 CPU 计算上限；超界/ctx 取消 -> unknown |

这些是服务策略，不开放为请求参数。**发布前校准**与**发布后默认行为变更**分开处理：
首次发布前的校准可回写本提案 §6.2 并重新确认；实现提交/发布后再改比例、速度、效果阈值
属于默认行为/执行语义变更，按 `docs/release.md` 与版本规则处理，不能笼统写“不改契约”。
本提案当前没有把任何阈值写成已实机校准。

### 6.3 日志与可观测性

复用 `act.*`、`observation.settle`、work/UI 事件。新增键必须先加入
`internal/eventlog/log.go` 的单一白名单（当前白名单
[log.go:63-86](../../../internal/eventlog/log.go#L63-L86)），否则当前会被静默丢弃：

| 事件 | 受控键 | 约束 |
| --- | --- | --- |
| `scroll.plan` | `operation_id`、`direction`、`amount`、`container_role`、`start_edge`、`end_edge` | 不记 ref、坐标、label、树 |
| `scroll.revalidate` | `operation_id`、`container_proven`、`start_scope_proven`、`end_scope_proven`、`quality` | 不记 raw identity |
| `scroll.effect` | `operation_id`、`state`、`anchors`、`coverage`、`dominant_axis`、`reason` | 不记文本/坐标/树；`reason` 用受控枚举 |
| `observation.quality` | `operation_id`（可取得时）、`observation_id`、`phase`、`state`、`samples`、`stop_reason` | 沿用受控枚举/数值 |
| `act.point` | `operation_id`、`state`、`proven`、`ms` | 现有事件补 operation_id，保持无坐标/ref |
| `act.delivered` | `operation_id`、`state`、`code`、`format` | `delivery.go:100` 当前缺 operation_id；补上，保持无 O/正文 |
| `observation.image` | `operation_id`（可取得时）、`observation_id`（仅已有 input O 时）、`code`、`available` | `capture.go:202` 当前缺 operation_id；禁止伪造未分配的 post O ID |

`effect` 的未观察原因只进日志，枚举至少包含 `insufficient_anchors`/`incomplete_coverage`/
`ambiguous_motion`/`size_changed`/`container_changed`/`unstable_post_frame`/
`post_observation_failed`/`cpu_budget`。日志仍禁止树、图片、输入正文、命令、异常原文、
凭据与原始响应；MCP stdout 只承载协议。`ActAction=scroll` 是内部 op/log 事实，
物理 UI kind 仍为 `swipe`；不得声称当前 P/结果已经有 action 字段。事件必须贯穿
operation_id；只有已有 input O ID 可用时才写 observation_id，不能为未分配的 post O
编造 ID。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威 | scroll 仍是 act 的一个分支，不新增工具/队列/第二份 O/P |
| STOP 不等待 | scroll 的采集/派发/后观察受 work ctx；STOP 后不发布迟到 O；已派发命令不保证撤回 |
| O 单向消费 | 静态检查→work→锁内 claim；busy/admission 失败不 claim，O 保持 ready；重验失败不归还 |
| P 不重放 | effect unknown/回执 unknown 都不重放；用 status/observe 收窄 |
| P 冻结/去重 | effect 是短 op 事实；finish 前条件化提交，冻结后不升级；status/重复 P 只复制，不附 O、不重算 |
| 单工作位置 | 容器采集、派发、后观察、effect 都在同一 work；无后台 effect 轮询 |
| 提交条件化 | 复用 publishTreeAttempt 的会话/epoch/display/work/STOP/age 检查 |
| 事实分层 | outcome/effect/quality 三分；后观察失败不改 executed |
| ref 保守连续性 | 容器匹配只用于授权；不把 n372 当跨文章对象；歧义即拒绝 |
| 实际轨迹 | 实际注入起终点必须仍归选定容器的最内层 scope；不是只校验容器或选择点 |
| 账本 | dispatch kind 仍为 `swipe`；不新增 ledger 版本或字段 |
| 日志 | 新键先入 eventlog 白名单；scroll/settle/point 事件贯穿 operation_id |
| MCP 三模式 | structured/text/both 都投影 effect/quality；同源字段清单 |

### 6.5 契约、文档与发布影响

#### 契约变化

| 面 | 变化 |
| --- | --- |
| `/api/act` | `operation.type` 增加 `scroll`；scroll 用 `container`（ref/point），不用 `target`；`swipe` 增加可选 `duration_ms` |
| `Observation` | 增加 `quality:{state,samples,stop_reason}`；`stable/unstable/unknown` |
| `Result` | `scroll` 增加 `effect:{state}`；`observed/not_observed/unknown` |
| `contract_version` | 9 → 10；`X-Mobile-Use-Contract` 与控制同步均精确 10 |
| `release_version` | 0.9.0 → 0.10.0 |
| `android_version_code` | 11 → 12 |
| MCP | 工具数仍 11；act inputSchema 扩展 scroll/container/duration；result text/structured 增加 effect/quality；`Node.actions` 仍只有 `swipe`，scroll 容器不新增 action 枚举；guide 明确用 `scroll` 表达滚动，`actions=swipe` 不是 raw 授权 |
| 账本 | 不变（v5）；UI kind 仍 `swipe` |
| raw swipe 提示 | `ambiguous_point` 的错误码不变；对 raw swipe 改为可执行提示（不能“改用 ref”），不声称 #7 整体解决 |

#### 本仓库文档与流程

本提案按本仓库 `maintenance/process.md` 推进：先确认提案，再实现；`docs/` 只写实现已经
具备的行为，未实现部分留在提案与验证记录。若实现涉及 `docs/`，提交前按本仓库现行文档
审计门禁做独立只读审计 clean；不涉及 `docs/` 则不触发该审计。提案的验收、版本、部署与
issue 记录只引用本仓库的事实。实现后同步 `docs/interface.md`/`docs/mcp.md`/`docs/architecture.md`：
`Node.actions` 仍保留 `swipe` 作为滚动容器的源能力名，但 guide 要优先引导 `scroll.container`，
避免模型只看到旧 `swipe` 提示就绕回 raw 路径。

#### 版本、提交与发行

以下步骤只在实施阶段执行，本轮不执行；提交记录必须区分实现提交、verification/归档提交，
若版本源单独提交也记录其 hash，不能混成一个“版本提交”。

1. `release/versions.json` 手改为 `0.10.0` / `10` / `12`；`source_commit` 记版本冻结
   基线（不是构建提交；release manifest 记录构建时实际 HEAD）。
2. 运行 `scripts/generate-version.ps1`，生成：
   `internal/protocol/version_generated.go`、
   `android-control/app/src/main/java/com/mobileuse/vnext/GeneratedVersion.java`、
   `android-control/app/src/main/res/values/strings_version.xml`、
   `android-control/app/src/main/AndroidManifest.xml` 的 versionCode。生成文件随实现提交
   落盘；若单独做版本提交，记录该提交 hash，但实际构建仍以最终实现提交为准。
3. 按本仓库 `maintenance/process.md` 完成实现、必要 docs 与独立审计；若实现涉及 `docs/`，
   审计 clean 后提交**实现提交**；若不涉及 `docs/`，不触发文档审计但仍提交实现与验证记录。
   实现提交才是后续实际构建 HEAD。
4. 手机整组构建必须从实现提交、在干净工作树上执行（不允许 `-SkipAndroid`）：

```powershell
./scripts/generate-version.ps1 -Check
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
```

产物为 `build/release/device-service`、`device-mcp`、`control.apk`、固定 u2/applist/
service.sh 与 manifest；清单必须记录 contract 10、release 0.10.0、Android 12、实际构建
HEAD 与 `source_dirty=false`。**不能只依赖现有脚本返回 0**：
[verify-manifest.ps1:146-153](../../../scripts/verify-manifest.ps1#L146-L153) 当前不强制
dirty=false/HEAD，必须另加显式断言或用新脚本证明构建树 clean、两处 source_commit 等于
实际 HEAD、phone manifest 完整存在（无 SkipAndroid/缺失）。

5. 主机工具重建与核验：

```powershell
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

`build/device-mcp.exe` 与 `build/mcp-probe.exe` 必须与手机清单同源实现提交、同 contract 10、
同 catalog 指纹；不得用 linux/arm64 文件改后缀冒充。
[build-host-tools.ps1:439-445](../../../scripts/build-host-tools.ps1#L439-L445) 当前在
dirty=true 一致时仍可能 PASS，334–337 在缺 phone manifest 时可能 SKIP/PASS；最终交付必须
显式断言两个 manifest 的 `source_dirty=false`、`source_commit==实际构建 HEAD`，phone
manifest 存在且 companion 比对 PASS，不接受 SKIP，不得把脚本返回 0 当成这些条件已满足。

6. 手机部署：按 `docs/release.md` §4，结束旧会话/STOP、保存退休事实、保持准入关闭，
   用 `deploy.ps1 -SkipBuild -Manifest build/release/manifest.json` 整组覆盖安装；保留
   App 私有数据、签名、配对、消息、停止意图与账本；核验 release/contract、App
   versionName/versionCode、`can_start`、手机 HTTP MCP 进程的 `/proc/<pid>/exe` SHA-256。
   部署脚本先移除旧项目兼容动作：删除 `SkipOldServiceStop` 与
   `deploy.ps1:296-303` 的旧 `service.sh`/`pkill -f "mobile-use-service serve"`，
   删除 `am force-stop com.mobileuse.control`（310）；只保留本仓库
   `com.mobileuse.vnext` 与 `device-service` 的精确停启。同步检查参数调用方、当前
   `docs/release.md`/`docs/runbook.md`/`docs/phone-operations.md` 与
   `maintenance/decisions.md` 的旧描述。

7. 主机替换：**不假定 `build/device-mcp.exe` 一定在运行**。先识别实际宿主用的是
   stdio 本地 exe，还是手机转发的 phone HTTP 入口/连接，再结束该精确进程/连接上的旧活动
   调用并替换/刷新；两个 exe 仍必须重建并核验。重启实际 DSH MCP 连接/宿主进程，重新
   发现 11 工具，用 `mcp-probe` 验证实际入口的 text/structured/both 与 contract 10。

8. 验证记录必须分别写：实际构建的实现提交 hash、版本源提交（如有）、verification 提交、
   提案归档提交；远端推送后核验远端包含实际构建提交与这些记录。不得把“已构建”写成
   “已部署/已实测”。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

- 滚动正文时不再因为起点叶节点 bounds 移动而 `target_changed`；但必须同时通过容器完整
  identity/祖先、实际起终点最内层 scope、前台/显示/采集龄/提交门禁，不是“容器 bounds
  不变即可”。
- 调用方不需要自己猜容器坐标/起止点；`scroll direction` 是调用方声明的意图，不是服务
  证明的真实方向；点模式只在同一容器候选归约上工作，多个不同 scroll 作用域要求显式 ref。
- 能区分“动作已派发”“观察到内容移动”“这棵树是否收敛”，不再把一个事实当另一个。
- `unstable`/`unknown` 后观察可见，调用方不会把它误当稳定基线；但不会被全局禁止输入。
- raw swipe 仍可做拖拽/fling/边缘手势，并可用 `duration_ms` 表达速度；其
  `ambiguous_point` 仍拒绝，只改不可执行的“改用 ref”提示，不声称 #7 整体解决。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 同一篇文章/同一业务对象 | 同模板容器/ref/bounds 可不变；树没有文章身份；容器连续性不是文章身份 |
| 没有 overlay/最顶层 | 无 window/activity/z-order；只能发现已发布且改变实际起终点 scroll scope 的 overlay；同 C 非滚动 overlay 可能放行 |
| 滚动轴/精确距离/到达边界 | 只有 `Scrollable bool`；`Axis` 无生产者；无滚动偏移；方向由调用方声明 |
| 真实方向/触摸消费者/业务安全 | 四向 `input swipe` 可能被 swipe-to-delete、轮播/翻页、系统边缘返回或下拉刷新消费；scope/effect 不能排除；横向/纵向均未从设备验证 |
| 内容移动一定等于滚动 | 动画、视频、骨架屏也可能改变 bounds；effect 只报观察到位移 |
| `effect=unknown` 是 `false`/未移动 | unknown 不伪装成 false；几何/树候选也不叫真实触摸消费者 |
| `not_observed` = 手势失败/到边界 | 只在 stable、100% 覆盖、无中心/尺寸变化时成立；不证明边界或触摸消费者 |
| 所有 App 都能受控滚动 | 不暴露 scrollable 的容器静态拒绝 |
| 安全滚动保证 | 首版只做受控滚动尝试；不保证不触发删除/翻页/返回/刷新等副作用 |
| 横向/纵向方向已实机验证 | 两个方向都未从设备验证；只按调用方声明执行，实机验收用专用 fixture |
| 容器 bounds 改变时仍可滚 | 容器移动/缩放视为 target_changed |
| raw swipe 不再 target_changed | raw 仍按起点完整身份证明；只加 duration，不改判据 |
| raw swipe 的 ambiguous_point 已修好 | 错误码/行为不变，只改不可执行的提示；#7 不因本提案解决或关闭 |
| 后观察失败仍能确认效果 | 无后树即 effect=unknown |
| 少量静止标题 + 其余内容换出 = 没动 | coverage 不完整时 `unknown`，不报 `not_observed` |
| shell 退出 0 = 会话屏移动 | shell 成功不等于 UI 移动；独立现象，本提案不冒认已修 |
| 磁盘替换 exe = 宿主已刷新 | 实际宿主进程/连接必须重启并重新发现 |
| Windows 构建证明手机账本耐久 | Windows 只作开发检查 |

### 7.3 成本

- 契约 10 使旧 0.9.0 客户端/主机适配器不兼容，必须整组升级；旧 contract 9 普通工作被
  `409 contract_mismatch` 拒绝，只有 STOP 可用。
- 每次 scroll 增加纯 CPU 的容器匹配/点重验/效果锚点匹配；不增加设备读取，但会增加少量
  结果延迟。`scrollDurationMS=400` 比 raw 300ms 多约 100ms 派发时间；后观察 settle 成本
  不变。
- O 多一个 quality 对象，Result 多一个 effect 对象；MCP text/structured 与 probe 断言增加。
- 需要跨应用实机测试和整组部署；主机宿主必须重启，不能只替换磁盘文件。

---

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望与反向对照 |
| --- | --- |
| scroll/act schema 严格 JSON | 有效 container ref/point、四方向、page/half 接受；缺 container/direction、未知 direction/amount、scroll 带 target/from/to/key/text/duration、swipe 带 container、显式 null、重复键拒绝；去掉分支互斥应失败 |
| swipe duration 边界 | 省略=300；50/2000 接受；0/负数/49/2001/浮点/null 拒绝；tap 带 duration 拒绝；去掉范围检查应失败 |
| 工作位/认领顺序 | busy/admission 失败：O 仍 ready、零 claim、零采集、零派发；同一 O 可用新 P 重试。去掉 busy 前置或先 claim 后 acquire 应失败 |
| 容器静态解析 | ref 直接 scroll 容器接受；ref 指向叶/非 scroll 拒绝（不隐式晋升）；point 同点多个内容候选但同一 scroll 作用域接受；同点两个不同 scroll scope -> ambiguous_point；point 无容器拒绝；enabled=false/无 bounds/过小容器拒绝；无容器拒绝且不认领/不采集 |
| 几何一致性 | `extent=100, margin=24, usable=52, page travel=31<48` 必须在认领前拒绝；half 同理；静态与 fresh 调用同一函数；fresh 不可执行 -> target_changed + 新帧交付；删掉共用函数/静态检查应失败 |
| 实际注入 scope | ref 和 point 都验证实际 start/end；内层 C + 外层 O：两端最内层均为 C 时通过；任一端落入 C 的内层 C2 -> target_changed；外层祖先允许；不可比/其它 scope -> target_changed；已上报 overlay 改变实际 scope -> target_changed；同 C 非滚动 overlay 可放行并记录边界 |
| 容器新鲜重验 | 容器 identity/祖先唯一才通过；容器 bounds/尺寸变化 -> target_changed；容器不动、内容移动 -> 可执行（不再看叶节点）；删掉容器匹配应失败 |
| 轨迹 | down/up/left/right 的起止顺序、page/half 比例、整数 margin/travel、半开 rect、无 clamp、过小拒绝；断言起止都在容器内；删掉方向映射应失败 |
| raw swipe | 原起点完整身份证明仍拒绝移动的叶节点；duration 透传到 argv；与 scroll 互斥；ambiguous_point 文案对 swipe 可执行（不再“改用 ref”）且错误码/行为不变；删掉原证明应失败 |
| fakeDevice.Swipe 计数 | 记录调用参数/起终点/duration；拒绝=0 次、允许=1 次、同一 P 重放仍=1 次；不得只断言 effect 状态而不计实际派发 |
| quality | 1 帧（budget/unreadable） -> unknown 且 stop_reason 对应；2 帧不等 -> unstable；相等 -> stable；max_samples/budget/unreadable/not_sampled 各自枚举；observe/交付 -> unknown/0/not_sampled；image 刷新相等必须 stable、不等再 settle、读取失败不加样本；5 帧耗尽 + image 刷新相等/不等/失败分别验证，刷新计入 samples（act 有图最多 6）；旧 pair 相等但最终刷新不同且预算耗尽 -> unstable，不能 stable；失败树无 quality；quality 不改 outcome/status/actionable_until；删掉 quality 投影或 image 相等置 stable 应失败 |
| effect | 100% 覆盖、stable、无位移 -> not_observed；固定标题保留 + 其余内容全部换出 -> unknown（不得 not_observed）；2 单元同号位移 -> observed；低覆盖但有 2 个一致移动单元 -> observed；3/4=75% -> observed，2/3=66.7% -> unknown；父子内容只算叶子；空结构节点不计数；容器/单元 resize -> unknown；方向冲突超过 25% -> unknown；<2 匹配/覆盖不足且无一致移动 -> unknown；后观察失败/撤销 -> unknown；ctx 取消/CPU 预算超界 -> unknown；effect 不改写 outcome；删掉 coverage/尺寸/75% 条件应失败 |
| ActAction 早退 | 新 P 静态 scroll 解析后、claim/dispatch 前记 ActAction=scroll；busy 后 status(P) 仍知道 action；dispatch uncertain、executed_revoked/STOP、post_failed 的首次结果与 status(P) 都不丢 action、effect=unknown；重复 P 不读新 payload；静态失败不误记；删掉早记应失败 |
| closed/淘汰边界 | closedKnown=true status not_executed/closed_before_admission 且无 effect；closedKnown=false status operation_unknown 且无 effect；再次登记同 P 时 known=not_executed/unknown=uncertain 且 request_closed、无 effect；finished 淘汰后同 P 被视为新登记时按新 payload 返回，不恢复旧 action/effect；缺失不得解释为 not_observed；不改 ledger/有界去重 |
| P 冻结/去重 | 首次结果含 effect；重复 P/status(P) 复制同一 effect、不附 O、不重算；STOP/工作到期/迟到计算竞争：同一短锁内 workAuthorized/ctx/owner/session/display 失效则不升级；冻结前未提交则 unknown，冻结后不升级；not_executed 不出现 effect；closedKnown=true/false、淘汰后不同 payload、重启边界；删掉 op 事实保存或条件化提交应失败 |
| 同模板/overlay fixture | 容器不变、叶内容换文章 -> executed（effect 可为 unknown/observed）；已发布 overlay 改变实际起终点 scope -> target_changed/ambiguous；同 C 非滚动 overlay 不伪造拒绝；未发布 overlay 不伪造检测；记录为能力边界 |
| 日志 | 新键在 eventlog 白名单内；act.point/observation.settle/scroll.*/act.delivered/observation.image 带 operation_id（必要时 observation_id）；reason/枚举受控；不记 ref/坐标/文本；不得伪造未分配 post O ID；删掉白名单键事件应丢失（反向验证） |
| STOP/工作位 | scroll 期间 STOP：不发布迟到 O；已派发保留 executed/uncertain；单工作位置 busy 不排队；O 不归还；P 不重放 |
| MCP 可发现性 | act schema 含 scroll/container/direction/amount/duration_ms；`treeTextGuide`/act description 优先说 scroll，且说明 `Node.actions=swipe` 只是源能力、不是 raw 授权；树块仍输出 `actions=swipe`，不新增 action 枚举；旧 guide 只剩 swipe 的 fixture 必须失败 |
| 版本/投影 | `/health`、`/v1/control/sync`、`/api/*` 契约 10；旧 9 拒绝；MCP 11 工具 schema/catalog；text/structured/both 的 effect/quality 同源；golden 不得从结构体反向生成预期 |
| response decode/client | `decode_response.go` 的 Observation/Result wire 读取 quality/effect；显式 null/类型/枚举/0 samples 拒绝/保留；`protocol.Result` roundtrip；fakeHTTP→GoClient→MCP text/structured/both 与 status(P) 投影都看到同值；只改 encoding 不改 decoder 的测试必须失败 |
| 发布脚本 | `generate-version -Check`、release manifest、host-tools manifest 的 0.10.0/10/12、source/catalog/artifact 哈希；显式失败用例：dirty 树、source_commit != HEAD、phone manifest 缺失/不完整、companion SKIP 都必须拒绝；旧版本/旧 catalog 必须拒绝 |

反向对照必须同时验证行为与派发：把 `scroll` 的容器匹配替换回 `provePointTarget`，
叶节点移动用例必须失败；把 effect 的 coverage/stable 条件去掉，固定标题+内容换出用例
必须失败；把 tap 的完整 identity 放宽，`TestActCoordinatePointRejectsMovedBounds` 必须
失败；允许一次 scroll 时 fakeDevice.Swipe 计数必须从 0 变 1，同 P 重放仍为 1。

### 8.2 实机/系统级

本提案起草时全部未执行；以下仅是实施后的验收计划，不写成已实测。

A. 受控滚动尝试与 raw 对照（受控测试 App/长文）：

1. 同一长回答，固定滚动容器；`scroll direction=down amount=page` 连续 10 次，记录
   R/E、effect、quality；与固定 raw swipe 10 次对照，验证受控路径不再因叶节点移动 R。
2. 顶部/底部：`up`/`down` 在边界各 2 次；记录 effect=not_observed/observed/unknown，
   不宣称到达边界。
3. 嵌套容器：竖列表中的横 carousel，分别用 point 和显式 `container.ref`；记录候选归约、
   实际起终点最内层 scope、内层拒绝/改用 ref 的结果；方向不匹配时记录实际效果，不宣称
   轴证明。
4. 无 scrollable 的 App：`scroll` 静态拒绝，raw swipe 仍可用；记录能力边界；raw swipe 的
   `ambiguous_point` 只验证可执行文案，不宣称 #7 解决。
5. `observe(wait1500)` 后立即 scroll vs 旧后观察等待 3000ms 后 scroll；记录 R/E 与
   quality/effect，验证“等待不修复旧 O”。
6. STOP 在 scroll settle/派发期间：确认无迟到 O、outcome 与 effect 分层。
7. 破坏性手势只在可恢复专用 fixture/自建测试页验证：覆盖 ItemTouchHelper/swipe-to-delete、
轮播/翻页、系统边缘返回、下拉刷新；不对真实消息/邮件执行可能删除项的手势，不制造真实
不可恢复副作用。横向/纵向方向都按未验证处理。

B. 质量与效果：

1. 静态长文、动画页、视频页各 3 次 scroll；记录 quality stable/unstable/unknown 分布、
   effect observed/not_observed/unknown、P50/P95；不宣称固定成功率。
2. 同模板换文章：保持容器/模板，切换文章后 scroll；记录容器匹配成功但文章身份不可证；
   effect 允许 observed/unknown；不得在结果中声称同一文章。
3. overlay：通知/底部弹层出现后 scroll；已发布且改变实际起终点 scope 的 overlay 应拒绝；
   同 C 非滚动 overlay 可能放行；未发布/透明 overlay 不伪造检测；记录实际树暴露。
4. 后观察失败/图片刷新路径：记录 effect=unknown，outcome 仍 executed；图片刷新相等/
   不等/读取失败分别记录 quality 的 stable/unstable/unknown。
5. shell 独立现象对照：`shell exited 0` 后不据此宣称会话屏移动；如需验证滚动，另用
   scroll/observe 取证，不把 shell 结果并入本提案。

C. 契约、构建与部署：

1. 版本源 0.10.0/10/12；`generate-version -Check`、完整 `build-release.ps1`、
   `verify-manifest.ps1` PASS；另显式断言构建树 clean、清单 `source_dirty=false`、
   `source_commit` 等于实际构建 HEAD、phone manifest 完整存在（非 SkipAndroid/缺失）；
   不接受现有脚本返回 0 或 companion SKIP 作为充分条件。版本源/生成文件必须在契约 10
   的测试转绿之前完成，不能先要求全部测试绿再生成。
2. 手机整组部署：旧会话/STOP 与 5 秒交接、覆盖安装、App versionName/versionCode、健康
   contract 10、`can_start`、手机 HTTP MCP `/proc/<pid>/exe` 哈希；保留消息/已读/配对/
   账本。
3. 主机：先识别实际入口是 stdio 本地 exe 还是手机转发 phone HTTP 连接，不假定
   `build/device-mcp.exe` 一定在运行；两个 exe 仍 `build-host-tools.ps1` 重建 +
   `-VerifyOnly` PASS；替换/刷新实际宿主/连接后，**通过实际 DSH MCP 连接**重新
   `tools/list`，按现有 canonical 规则计算 catalog 指纹并与配套
   `manifest.tool_catalog.sha256` 比对；断言 `act` schema 含
   `scroll`/`container`/`direction`/`amount`/`duration_ms`。再经该真实连接做一次
   scroll，确认返回 effect/quality；不得用 `mcp-probe` 直连新 exe 代替真实宿主验收。
   text/structured/both 的 contract 10、effect/quality 投影同样应通过该连接或同源
   probe 验证并分开记录。
4. 跨应用对照：知乎长文、设置类列表、WebView/信息流各至少一条受控路径；不以单页
   成功率替代跨应用结论；设置场景 6/6 收敛、B 站 4E（后 2 单帧）等样本只作对照。
5. 部署脚本范围检查（静态/模拟，不执行旧命令）：确认 `deploy.ps1` 不再引用
   `SkipOldServiceStop`、`mobile-use-service`、旧 `service.sh` 或
   `com.mobileuse.control`；本仓库 `com.mobileuse.vnext` 与 `device-service` 的精确停启
   仍在。检查调用方/文档无残留；不在真实设备上运行旧命令，不触碰旧项目。

### 8.3 记录

实现后写入 `maintenance/verification/semantic-scroll-observation-quality.md`：实现提交、
版本源提交（如有）、verification/归档提交、构建方式、设备/环境、实际命令与退出码、手机/
主机清单与哈希、契约 10 配对、受控 scroll/raw/quality/effect 的实机结果、同模板/overlay/
边界/无 scrollable 的边界证据、先红反向对照、部署保留资源、宿主刷新与未执行项/原因。
原始树/截图/日志/凭据不入库；区分代码实现、单元测试、手机实测、主机入口实测。

### 8.4 交付门禁（实施完成后才执行）

本提案起草时只到“提案，未实现”；以下步骤在起草轮都不执行，不得提前创建实现 hash、部署记录、
评论或关闭 issue。

1. 按本仓库 `maintenance/process.md` 完成实现 + 必要 docs；若涉及 `docs/`，做独立只读文档
   审计 clean。
2. 从实际实现提交构建手机整组与主机两个 exe；`verify-manifest` 与 host-tools
   `-VerifyOnly` 通过；**另显式断言**构建树 clean、两个 manifest 的
   `source_dirty=false`、`source_commit==实际构建 HEAD`、phone manifest 完整存在且
   companion 比对 PASS（不接受 SKIP）；手机整组部署、主机实际入口刷新、11 工具重发现；
   **通过实际 DSH MCP 连接**按 canonical 规则重算 catalog 指纹并与配套 manifest 比对，
   断言 act schema 含 scroll/container/direction/amount/duration_ms，经该连接做一次
   scroll 看到 effect/quality；受控 scroll/raw/effect/quality 实机范围完成。
3. 提交真实 verification 记录；另一次提交归档提案并补 §11、实现 hash、验证链接、
   相对链接。记录必须区分实现提交、版本源提交（如有）、verification 提交、归档提交。
4. 按约定分支推送（不 force）；核验远端包含实际构建提交与 verification/归档记录。
5. 回复 issue #10，必须如实写明与原诉求的差异，不能声称原封不动满足：
   - 原 issue 期望旧 raw swipe 10 次 0R、原 shape 不变且无需契约 bump；本提案改为
     contract 10 新增 `scroll`，raw swipe 仍保留严格起点证明，不能拿旧 raw 验收冒充完成。
   - 动态重验失败会消费旧 O 并交付新 O；只有静态拒绝不认领、O 保持 ready。
   - 容器连续性不证明同一文章；同模板换文章仍可能通过容器授权。
   - `shell exited 0` 不保证会话屏移动，是独立现象，本提案不冒认已修。
   - 报告实际迁移/验证结果、版本/破坏性升级、实测范围与未执行边界。
6. #10 只有在实现提交、成功部署/实测、版本与远端记录齐备后才关闭：先确认回复已发布并
   取得评论链接，再以 completed 关闭，最后回读 issue 的 closed 状态、关闭原因与评论链接。
   任一步失败不宣称完成，不提前关闭；不使用 closing 关键字提前关联关闭；#7 不顺带关闭/重开。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 主路线 | 受控 `scroll` 尝试 + raw `swipe` 分开；不新建工具，不搞通用 gesture DSL；独立工具可薄转发同一 Engine，本次因目录/文案面选择 act 分支 |
| 滚动选择器 | `container`：ref 必须直接是 scroll 容器，不隐式晋升；point 取全部候选归约同一容器，多个不同 scroll scope 要求 ref |
| 滚动授权 | 容器完整 identity/祖先 + 实际注入起终点最内层 scope + 前台/显示/采集龄/提交门禁；不看起点叶节点 |
| 认领顺序 | 静态检查 → acquire work → 锁内重验并 claim；busy/admission 不 claim，O 保持 ready |
| 不稳定 O | 发布并标记 quality；scroll 可在容器/实际轨迹证明成立时执行；tap/text/raw 仍按各自目标证明，不设全局 stable 门 |
| 质量 | `Observation.quality` state/samples/stop_reason；<2 帧 unknown，≥2 不等 unstable；image 刷新相等置 stable |
| 效果 | `effect` 公开 observed/not_observed/unknown；not_observed 需 stable + 100% 覆盖 + 无位移/尺寸变化；不承诺方向/距离/边界 |
| P 冻结 | `ActAction`/`Effect` 为有界 op 事实；finish 前条件化提交；冻结后不升级；status/重复 P 只复制、不附 O、不重算 |
| raw swipe | 保留原起点证明；只增加 50–2000ms duration；ambiguous_point 文案改为可执行，不声称 #7 解决 |
| MCP 引导 | `Node.actions` 仍保留 `swipe` 作为滚动容器源能力；不新增 action 枚举；guide 优先用 `scroll.container`，说明 `actions=swipe` 不是 raw 授权 |
| 日志 | 新键先入 eventlog 白名单；scroll/settle/point 事件带 operation_id；不记 ref/坐标/正文 |
| 证据上限 | 接受当前无 axis/z-order/scroll offset；同文章/无 overlay/精确距离/边界不作为保证 |
| 版本 | 0.10.0 / contract 10 / Android versionCode 12 |
| 交付 | 手机整组 + 主机 device-mcp/mcp-probe 重建、部署、实际宿主/连接刷新；实际 DSH MCP 连接 `tools/list` catalog 指纹与 manifest 比对、act schema 含 scroll 字段、经该连接 scroll 看到 effect/quality；11 工具重发现；部署脚本移除旧项目兼容停启，只保留本仓库 `com.mobileuse.vnext`/`device-service` |
| 最终门禁 | 实现提交 + 部署/实测 + 远端核验 + verification/归档 + issue #10 回复并关闭；本轮不执行 |
| 不变式 | STOP/O/P/单工作位/一次派发窗口/提交条件化/ref 保守连续性不变 |

### 9.2 未决

接口判据、字段、默认值、错误/恢复、质量/效果投影、版本与交付范围均在本提案给出具体
建议，没有留给实现者临时猜测的产品语义。

- **实施启动**：用户已于 2026-11-12 通过本文，并明确本会话只提交规范与提案、不进入
  实施；后续会话另行启动实施，结果见 §11。
- **发布前校准**：`scrollPageRatio/scrollHalfRatio/scrollDurationMS/effect*` 的初始值可在
  首次发布前按 §8.2 A/B 校准；改变判据/字段/时序或公开 effect 语义必须回写提案并重新
  确认。实现提交/发布后再改比例、速度、效果阈值属于默认行为/执行语义变更，按
  `docs/release.md:45-47` 与版本规则处理，不能笼统写“不改契约”。
- **现场条件**：实施负责人在构建/部署前核对当时 HEAD、版本号是否被占用、实际主机 MCP
  入口（stdio 本地 exe 或手机转发 phone HTTP）与宿主、私有配置、设备/App 配对状态；
  变化时更新事实与命令，凭据不入库。
- **本仓库流程**：实现按 `maintenance/process.md`；`docs/` 只写已实现行为；若改 `docs/`，
  提交前独立只读审计 clean；不把 docs-only、提交或推送作为实现前置。
- **issue #10**：只在实现/实测/版本/远端/verification/归档齐备后回复并关闭；原 issue 的
  “旧 raw swipe 10 次 0R、shape 不变、无需契约 bump”不作为本提案的验收承诺。动态重验
  失败消费旧 O 并交付新 O、静态拒绝不认领、容器不证明同文章、shell exited0 不证明移动
  都必须如实写入回复。#7 不连带关闭/重开；本轮不发评论。

---

## 10. 实施顺序

1. 用户确认本提案；核对基线与版本占用；按本仓库 `maintenance/process.md` 推进。
2. 先改 `release/versions.json` 为 0.10.0/10/12 并运行 `generate-version.ps1`，生成 Go/
   Java/XML/Manifest；此步先于契约 10 的测试断言，避免“测试先绿、版本后生成”的顺序错误。
3. 写失败的新契约/行为测试：container schema、MCP guide/`Node.actions=swipe` 映射、认领顺序/
   busy、几何谓词、实际注入 scope、quality samples/stop_reason、effect coverage、P effect
   冻结、日志白名单；确认现有代码红。
4. protocol + MCP schema/text + client + probe 的类型/投影；保持工具数 11。
5. engine：`scroll.go` 容器候选归约/匹配/`scrollGeometry`/实际 scope/效果；`act.go` 分支；
   `capture.go`/`settle.go` quality；`engine.go`/`operations.go`/`retirement.go` effect 保存；
   raw duration；不改共享 identity、O/P/work/STOP 顺序。
6. 单元测试、反向对照、交叉构建；`fakeDevice.Swipe` 计数/轨迹/duration；更新 docs 只在
   实现具备后写。
7. 完成发布核验门禁与部署脚本修改，移除旧项目兼容停启（`SkipOldServiceStop`/旧
   `service.sh`/`mobile-use-service`/`com.mobileuse.control`），保留本仓库精确停启；
   此时只做开发/静态检查，不把提交前的脏树试构建当作正式发布证据。
8. 若本次涉及 `docs/`，独立只读文档审计 clean；提交**实现提交**（提案仍“未实现”）。
9. 从干净的实际实现提交完整构建 release 与 host-tools，核验版本源/生成文件/两套清单及
   clean/HEAD/companion 门禁；按 §8.2 部署手机、替换/刷新实际主机入口/连接、重启宿主；
   通过实际 DSH MCP 连接重算 catalog 指纹、断言 act schema 并做一次 scroll；
   跑受控 scroll/raw/quality/effect 与三模式验收；记录 verification。
10. 提交 verification；另做提案归档：补 §11、实现 hash、验证记录、相对链接；部分落地写
    缺口，不以“已构建”冒充“已部署/已实测”。
11. 按约定分支推送（不 force）；核验远端包含实际构建提交、verification 与归档记录。
12. 回复 issue #10（迁移/验证、版本/破坏性升级、实测范围/边界、与原诉求差异），
    确认评论发布后以 completed 关闭，再回读 closed 状态/关闭原因/评论链接；任一步失败
    不宣称完成、不提前关闭；#7 不顺带关闭/重开。本轮不执行第 1 步之后的任何实现/部署/推送/评论。

---

## 11. 实现结果

实现提交：`2cc559a`（正式文档）；实现按顺序为 `273bda4`（协议与版本源）、`c939f79`
（发布/部署脚本门禁）、`383bca5`（MCP/client 投影）、`90fedf1`（engine 观察质量）、
`4563621`（probe）、`65385a2`（verify-m6 上一契约）、`552acea`（engine scroll/effect）、
`2cc559a`（文档）。实现与验证记录：
[semantic-scroll-observation-quality.md](../../verification/semantic-scroll-observation-quality.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 契约 10：`scroll` 的 container/direction/amount、`swipe.duration_ms`、`quality`/`effect` 线格式与严格解码 | `internal/protocol/requests.go`、`encode.go`、`responses.go`、`decode_response.go` |
| 版本源 0.10.0 / 契约 10 / versionCode 12 | `release/versions.json` 与生成 Go/Java/XML/Manifest |
| 静态容器解析、点候选归约、共用几何谓词 | `internal/engine/scroll.go`（`resolveScrollPlan`、`scrollContainerAtPoint`、`scrollGeometry`） |
| 认领后容器完整身份/祖先、实际起终点最内层 scope、fresh 新帧交付 | `internal/engine/scroll.go`（`revalidateScrollTarget`、`scrollScopeMatches`）、`act.go` |
| 受控轨迹派发（物理原语/账本仍 `swipe`，400ms） | `internal/engine/scroll.go`（`dispatchScroll`）、`act.go` |
| raw swipe duration 与 `ambiguous_point` 可执行提示 | `internal/engine/act.go`、`point.go`、`internal/android/foreground.go` |
| `Observation.quality`：settle stop_reason、image 刷新计入样本、交付/observe not_sampled、失败树无 quality | `internal/engine/settle.go`（`qualityOf`）、`capture.go`、`delivery.go` |
| `Result.effect`：容器匹配、叶子内容单元、覆盖/尺寸/同号位移、CPU/ctx 预算 | `internal/engine/scroll.go`（`computeScrollEffect` 等） |
| `ActAction`/`Effect` P 事实、条件化提交、status/重复 P 只复制、冻结不升级 | `internal/engine/engine.go`（`operationState`、`operationViewLocked`）、`operations.go` |
| 受控日志事件与白名单 | `internal/engine/act.go`、`scroll.go`、`capture.go`、`delivery.go`、`internal/eventlog/log.go` |
| MCP schema/guide/结果投影、client 解码 | `internal/mcp/schema.go`、`result_text.go`、`internal/client/client_test.go` |
| probe 契约 10、scroll schema、quality/effect 断言 | `scripts/mcp-probe/` |
| 发布门禁、开发-only `-AllowDirtySource`、部署只停本仓库、旧项目兼容清理 | `scripts/verify-manifest.ps1`、`build-host-tools.ps1`、`deploy.ps1`、`verify-m6.ps1` |
| 正式文档与文档审计 | `docs/*.md`、`README.md`、`maintenance/decisions.md`；独立只读审计 CLEAN |
| 单元/回归与反向对照 | `internal/engine/scroll_*_test.go`、`quality_test.go`、`internal/protocol/*_test.go`、`internal/mcp/*_test.go`、`internal/client/client_test.go`、`internal/android/transport_test.go`、`scripts/mcp-probe/*_test.go` |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| companion 门禁位置 | 提案 §6.1 把“phone manifest 完整 + companion PASS”列在 `verify-manifest.ps1`；实现把 companion 比对放在 `build-host-tools.ps1 -VerifyOnly`，因为发布顺序先构建手机整组（`verify-manifest`）再构建主机工具，`verify-manifest` 时 host-tools manifest 尚不存在。两处合计覆盖提案门禁；`verify-manifest` 仍要求干净树、HEAD 前缀、`source_dirty=false` 与完整手机产物。 |
| `-AllowDirtySource` | 提案未列出该开关。实现新增开发-only 参数：默认拒绝脏树；显式使用时清单记 `source_dirty=true`，且永远不能通过 `-VerifyOnly`。正式门禁不依赖它。 |
| `verify-m6.ps1` | 契约 10 后其“上一发行契约”断言必须从 8 改为 9；只改常量与帮助文本，不改核验流程。 |
| `scroll.revalidate` 的 `quality` 字段 | 实现记录重验时原观察的 quality state（受控枚举）；提案只要求该事件带 quality、不记录 raw identity，未规定取哪个观察的质量。 |
| 实际起终点 scope 判定 | 实现要求每个候选的最近滚动作用域为 C 或 C 的外层祖先，且至少一个候选归约为 C；内层/不可比/其它 scope 拒绝。这是对“最内层必须是 C”与“外层包裹祖先允许”两条判据的合并读法；字面集合相等会误杀允许的外层祖先情形。 |
| `effect` 提交门禁失效时的首个结果 | 计算成功但 `commitScrollEffect` 门禁不再成立（撤销/冻结）时，首个结果给 `effect=unknown`，不声明保留 P 无法投影的事实。 |
| 实机部署与验收 | 已执行（有限）：手机整组部署与 App/服务/MCP 哈希、`can_start` 核验通过；Windows DSH 宿主重连、11 工具、act schema 与 `mcp-probe` catalog 比对通过；Settings 实机 5 次受控 scroll 全部 `executed`、0 次 `target_changed`，`effect` 得到 `not_observed`/`unknown`，未取得实机 `observed`。跨应用、长文/嵌套/overlay/STOP 时序、阈值校准与手机侧另一旧 stdio 宿主重启未做；见验证记录 §4.1–§4.3、§7。 |
| issue #10、推送 | 已执行：`master` 已推送到 `origin`（未 force），远端包含实现提交与验证记录；issue #10 已回复差异与实测范围并以 `COMPLETED` 关闭（<https://github.com/malyjacob/mobile-use-vnext/issues/10#issuecomment-6827481941>），回读 `CLOSED/COMPLETED`；#7 未关闭、未重开。 |
| 提案头部状态 | 原“提案，未实现”与“本会话不进入实施”按流程更新为“已实现”并补 §11；原确认日期与确认事实保留。 |

---

## 附：本提案不改变的事项

- 不改变 `tap`/`long_press`/`text`/`key` 的既有字段与判据。
- 不改变 `swipe` 的起点完整身份证明；只增加 `duration_ms`，并把 ambiguous_point 的
  不可执行提示改为操作相关提示，不改变错误码/行为。
- 不解决 #7，也不在本轮回复/关闭/重开 #7。
- 不改变 `observe` 不 settle、`start`/`act`/`app` 的 settle 预算与帧数策略。
- 不改变 O 的 `ready/claimed/retired/expired` 状态机、60 秒有效期与基线确认入口；
  静态拒绝/busy 不认领，动态重验失败消费旧 O 并交付新 O。
- 不改变 P 的登记、去重、`uncertain` 与不重放规则；新增 effect 只是有界 op 事实。
- 不改变 STOP/end 的撤权、5 秒逻辑交接、收尾事实与不等待。
- 不改变单工作位置、无普通动作队列、受管 shell 的跨调用屏障。
- 不改变 ledger v5、消息队列、图片编码、App 控制界面与主屏权限。
- 不把 `axis`、window/activity/z-order、滚动偏移新增为公开事实。
- 不新增调用方 timeout/TTL/settle/速度/路径参数。
- 不声称 `shell exited 0` 等同于会话屏移动。
- 提案起草本轮不实现、不部署、不推送、不创建实现 hash、不发 issue 评论、不提前关闭 #10；
  实施结果见 §11。
