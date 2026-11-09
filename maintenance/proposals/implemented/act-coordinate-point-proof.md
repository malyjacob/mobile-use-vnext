# 提案：act 的坐标目标按“点解析节点”证明派发前未变化

状态：已实现（实现提交 `2f43ef1d8c6150dde3d1038ce5ed2d31caeecfac`，2026-11-09）。
基线提交：`742937fc97d0f60d892b8368ed30ce8cb3e8e7a2`（分支 `master`，2026-11-09）。
适用范围：`act` 的坐标目标——`tap`/`long_press`/`text` 的 `{x,y}` 目标，以及 `swipe` 的起点；不改 `ref` 目标、`observe`、`shell`。
影响范围：`internal/engine`（`act.go`、新增 `point.go` 与用例）、`internal/protocol`（新增错误码）、`docs/interface.md`。
契约（`/api/*` 字段与 `contract_version`）：改变——新增 422 错误码 `ambiguous_point`；请求/结果字段不变，`contract_version` 不变。
实现与验证记录：[act-coordinate-point-proof.md](../../verification/act-coordinate-point-proof.md)。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的
`HEAD` 重新核对（重点：`internal/engine/act.go` 的 `Act`、`staticActContentCheck`、
`revalidateActTarget`、`matchFreshNode`，以及 `internal/engine/engine.go` 的 `Projector` 接口）。

---

## 0. 摘要

`act` 的坐标目标在派发前只做“显示存在、采集成功、前台包/任务未变”的检查：同一个 App 内换页、
几何变化、或被同包浮层盖住时，坐标仍会照发，结果是静默点错。本提案在观察的公开树里把
`(x,y)` 解析成唯一节点，再要求新鲜树的公开投影里同一个点解析到同一个节点（完整内容身份 +
祖先链）；解析不到节点时维持现状（前台级），解析有歧义时在静态检查阶段返回
`422 ambiguous_point`。不改请求字段、不增加设备读取、不改变 `ref` 目标与 `shell`。

## 1. 问题

### 1.1 现象与代价

坐标目标在屏幕变化后没有任何节点级检查：

- 同 App 内换页（同 task）时，前台包与任务号都不变，坐标点按照发；
- 旋转或 `wm size` 之后，坐标合法性仍按观察时的 `obs.Screen` 判断，重验不比对现场几何；
- 同包浮层盖住目标时，前台仍是同一个包/任务，坐标点按照发。

这三种情况的共同点是**没有错误码、没有拒绝、没有信号**：响应是 `executed`，agent 无法区分
“点对了”与“点错了”。这比被拒绝更糟——拒绝会让 agent 观察一次，静默误点会把后续动作带到
错误的状态上。

### 1.2 证据链

1. 坐标目标只比前台：`revalidateActTarget`（[act.go:269](../../../internal/engine/act.go#L269)）在
   `foregroundMatches` 之后直接返回（`act.go:292-294`）；`foregroundMatches`
   （`act.go:307-329`）只比 State/Package/TaskID，`device.Foreground` 没有 Activity 或窗口字段
   （[types.go:69-74](../../../internal/device/types.go#L69-L74)）。
2. 坐标合法性用的是观察时的屏幕：`staticActContentCheck` 里 `screen := obs.Screen`
   （`act.go:174`）；重验只确认显示存在（`act.go:279-284`），`DisplayState` 只有 `Exists`/`SFID`
   （[types.go:153-156](../../../internal/device/types.go#L153-L156)）。
3. 派发用的是请求里的字面坐标：`dispatchAct` 只对 `ref` 目标改用新鲜节点的 bounds 中心
   （`act.go:344-354`）。
4. 节点身份不含可见性与 z 序：[identity.go:24-34](../../../internal/engine/identity.go#L24-L34)。
   所以即使走 `ref`，浮层也不会改变底下节点的 identity（这是另一个洞，见 §7.2）。
5. 现有机制只有“目标节点”这一种证明：`matchFreshNode`
   （[act.go:588-616](../../../internal/engine/act.go#L588-L616)）与 `lineageOf`/`sameLineage`
   （`act.go:566-584`）。坐标没有节点可证，于是什么都没做。
6. 这个失败模式不是理论上的：settle 提案 §1.4 已记录，agent 在被 `target_changed` 拒绝后会
   倾向改用坐标目标；坐标路径正是“跳过 ref 唯一性证明”的那条路
   （[act-post-observation-settle.md §1.4](act-post-observation-settle.md)）。

### 1.3 为什么直觉会出错

- **“前台没变就够了”**：不够。同包换页、浮层、几何变化都不改包名/任务。
- **“让坐标 act 必须用新观察”**：把每次坐标动作变成一次额外往返，而且观察之后屏幕仍可能再变；
  它没有回答“这个点现在是什么”。
- **“比对整屏摘要”**：太严。时钟、进度条、动画都会让摘要变化，会把正常的坐标动作大量误拒。
- **“坐标和 ref 套同一个判据”**：不行。ref 的目标是 agent 指定的；坐标的目标要先把点解析
  出来，而解析本身有歧义。两者的“拒绝歧义”代价不同（见 §3 的选择理由与 §7.2）。

## 2. 目标与非目标

**目标**

- G1 坐标目标在派发前必须证明：同一个点在观察树与新树里解析到同一个节点；证明不了就拒绝。
- G2 判据可判定、可单测：唯一、歧义、无节点三种结果各有确定行为。
- G3 不新增请求字段、不增加设备读取、不改变 `ref` 目标的行为。
- G4 误拒只发生在“点本来就歧义”或“点上的节点确实变了”的情况。

**非目标**

- 不解决 `ref` 目标的遮挡（需要 z 序/窗口层级，树里没有）。
- 不解决 WebView/canvas 内部内容变化（树本来不描述）。
- 不做像素比较，不引入截图。
- 不改 `observe`/`start`/`open_app`/`shell` 的任何行为。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 维持前台级 | 0 | 静默点错，无法区分 |
| R2 坐标 act 要求新观察 | 旧 O 一律拒绝坐标目标 | 每次多一轮往返 | 观察后仍可能变；没回答“点是什么” |
| R3 整屏摘要 | 观察与现场的三层摘要对比 | 一次投影 + 摘要 | 时钟/动画误拒，不可用 |
| R4 点→节点 + 新鲜树重解析 | 观察树解析、新树重解析、比较 | 一次 `Preview` + O(公开节点数) 遍历 | 歧义点被拒（改用 ref）；无节点时无证明 |
| R5 禁止坐标目标 | 只允许 `ref` | 0 设备成本 | 打掉 WebView/canvas 等真实用途，范围过大 |

选择 R4：它是唯一“成本一次投影、判据与既有 `identityOf` 同源、且不扩大公开面”的路线。
R1 是基线；R2 只是把问题推迟；R3 的误拒不可接受；R5 越界。R4 的关键优势是失败条件的直接
否定：我们把“这个点的意思没变”定义成“解析到的节点没变”，而不是“画面看起来没变”。

## 4. 判据

### 4.1 定义

- `T`：一次观察的公开投影（`engine.Projection`：`Tree`、`Capture`、`Source`，
  [engine.go:181-185](../../../internal/engine/engine.go#L181-L185)）。
- `resolve(T.Tree, x, y)`：在公开树上取“包含 `(x,y)` 的、非 root、非 context、`Bounds != nil`
  的节点里最深的那一个”。
  - 半开区间：`Bounds.Contains`（[node.go:86-88](../../../internal/protocol/node.go#L86-L88)）。
  - “最深”按树深度；后代严格更深。
  - 若最深的候选不止一个（它们必然互不为祖先），结果为 `ambiguous`；没有候选为 `none`。
- 观察侧：`R_obs = resolve(T_obs.Tree, x, y)`；`T_obs` 是 agent 实际读到的那次投影。
- 新鲜侧：`R_new = resolve(Preview(T_new).Tree, x, y)`；`Preview` 是不推进保号的投影
  （[engine.go:187-194](../../../internal/engine/engine.go#L187-L194)，settle 采样同款用法
  [settle.go:143-159](../../../internal/engine/settle.go#L143-L159)）。

### 4.2 通过条件

坐标 act 在派发前必须同时满足：

1. 观察侧唯一：`R_obs` 存在且不歧义（否则见 §4.3）；
2. `matchFreshNode(T_new, T_obs.Capture, R_obs.raw)` 找到唯一节点 `R_match`
   （完整内容身份 + 祖先链，现有判据）；
3. 新鲜侧唯一：`R_new` 存在且不歧义；
4. 同一：`R_new` 就是 `R_match`（同一 raw 下标）。

任何一条不满足 → `409 target_changed`、`next=observe`、不派发。观察侧无节点（§4.3）时跳过
1/2/3/4，只保留前台检查，并记一条事件。

### 4.3 观察侧的三种结果

| `resolve(T_obs.Tree)` | 行为 | 理由 |
| --- | --- | --- |
| 唯一 | 走 §4.2 | 正常 |
| 歧义 | `422 ambiguous_point`，不认领、不采集 | 点本来就没指定节点；agent 应改用 `ref` |
| 无 | 维持前台级，记 `state=none` | WebView/canvas 等树不描述的区域，不能一概拒绝 |

### 4.4 进判据与不进判据

- 进：`identityOf` 的全部字段（含 `Bounds`，因此位移会失败）、祖先链、点解析的唯一性。
- 不进：`Visible`（公开树里非 context 节点本来就只在 `raw.Visible` 时发布，见
  [build.go:201-208](../../../internal/observation/build.go#L201-L208)）、z 序（树里没有）、像素、
  前台（继续保留，不替代）。
- 明确不把 `Visible` 加进 `identityOf`：那会同时改变 `ref` 重验与 settle 判据，范围远大于本提案。

### 4.5 与 ref 判据的关系

`ref` 目标的判据不变。坐标的新判据是在“节点级证明”之前多了一步“点解析”，因此严格更强，
不会放宽任何现有门禁。反过来，`ref` 目标不套用点解析（原因见 §7.2）。

### 4.6 swipe 与 text

- `swipe`：只证明起点 `from`。手势的语义目标是起点；终点是方向/幅度，不是节点身份。若起点
  无节点，维持现状。
- `text` 的坐标目标：证明焦点点击的那个点；之后 `getTextOnDisplay`/`setTextOnDisplay` 的绑定与
  读回不变（[act.go:409-471](../../../internal/engine/act.go#L409-L471)）。

## 5. 算法

```text
静态（不认领、不采集）：
  if 操作有坐标目标（tap/long_press/text 的 target，或 swipe 的 from）:
      r, state := resolve(T_obs.Tree, x, y)
      state == ambiguous  -> 422 ambiguous_point
      state == none       -> unproven = true
      state == unique     -> obsRaw = T_obs.Capture.Nodes[T_obs.Source[r]]

认领 O（既有）→ 采集新树（既有）→ 前台检查（既有）

if unproven: 跳过下面的证明，按现状派发
matched, ok := matchFreshNode(T_new, T_obs.Capture, obsRaw)
if !ok: 409 target_changed
fp := Preview(T_new)
if fp 失败: 503 hierarchy_unavailable
r2, state2 := resolve(fp.Tree, x, y)
if state2 != unique || fp.Source[r2] != indexOf(T_new, matched): 409 target_changed
派发（坐标仍用请求里的 x/y）
```

时间轴：

```text
t0 observe：T_obs，O ready
t1 act 静态：resolve(T_obs) → 唯一/歧义/无
t2 claim：ready → claimed
t3 Capture：T_new
t4 matchFreshNode：身份 + 祖先链唯一
t5 Preview + resolve(T_new)：点必须仍到 R_match
t6 dispatch：x/y
```

静态检查的顺序保持既有语义：先 `inScreen`（越界仍是 `invalid_coordinates`），再做点解析
（歧义才报 `ambiguous_point`）。

撤销、失败、超时：

- t1 之前被撤销：既有 `stopped`/`session_ended`，不派发。
- t2 之前被撤销：`claim` 失败，不派发。
- t3–t5 失败：`not_executed`；O 已被 claim（不归还），不派发；`target_changed` 与
  `hierarchy_unavailable` 都带 `next=observe`（`act.go:53-56` 的既有行为）。
- t1 的歧义：`not_executed`，O 仍是 `ready`（静态拒绝不认领）。
- 永不返回 `executed` 而没有派发；永不返回 `uncertain`，因为证明全在派发之前。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/point.go`（新增） | 纯函数 `resolvePoint(tree *protocol.Node, x, y int) (ref string, state pointState)`；`pointState` 为 `unique`/`ambiguous`/`none` |
| `internal/engine/act.go` | `staticActContentCheck` 增加歧义拒绝（坐标目标与 `swipe.from`）；`revalidateActTarget` 增加新鲜侧证明 |
| `internal/protocol/errors.go` | 新增 `CodeAmbiguousPoint = "ambiguous_point"` 并映射 422（`errors.go:167-169`） |
| `internal/engine/point_test.go`、`internal/engine/act_point_test.go`（新增） | 解析器与引擎路径的用例 |
| `docs/interface.md` | §2 的 422 清单、§6 的静态检查/通过后/失败码 |

### 6.2 配置与取值

无新增配置、无新增请求字段。解析规则是固定判据，不开放给调用方。

### 6.3 日志与可观测性

新增白名单事件 `act.point`（每次坐标目标决策点记一条）：`state`（`unique`/`ambiguous`/`none`）、
`proven`（bool）、`ms`（本次证明耗时）。不记坐标、不记 ref、不记节点文本或树。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 静态拒绝不认领 | 歧义拒绝发生在 `staticActContentCheck`（`act.go:27-32`），在 `acquireWork`/claim 之前 |
| O 单向 | 不变 |
| 提交条件化 | 不变（新证明在派发前，不涉及发布） |
| 事实分层 | 新拒绝是 `not_executed`；`executed` 仍只在派发后出现 |
| 工作位 | 不变；`Preview` 是纯 CPU，不占设备 |
| 保号 | `Preview` 不推进保号（`engine.go:187-194` 的契约） |
| 设备同时一项工作 | 不变；`Preview` 不碰设备 |

### 6.5 契约与文档影响

- 新增 422 错误码 `ambiguous_point`：`docs/interface.md` §2 的 422 清单与 §6 的失败码都要登记；
- §6 的静态检查与“通过后”段落要写清“点解析 + 新树重解析”；
- 请求/结果字段与 `contract_version` 不变（§9.1）；
- 不改 `docs/architecture.md`（它不描述 act 的重验）；
- MCP 的 `isError` 表不需要改：`not_executed` 仍是 `true`。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 同包换页、页面内导航之后的坐标点击；
- 节点 `Bounds` 实际发生变化（位移、尺寸变化）之后的坐标点击；
- 目标节点消失或出现重复身份；
- 同包浮层盖住该点，且浮层在公开树里可见且有语义（弹窗、菜单、BottomSheet 等）；
- 以上都发生在派发前，返回 `target_changed` 而不是静默误点。

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| `ref` 目标的遮挡 | `ref` 没有点语义；树里没有 z 序，“合法重叠”和“遮挡”在数据上不可分，拒绝重叠会误伤悬浮按钮压列表这类正常布局 |
| WebView/canvas 内部内容变化 | 树只暴露容器节点；点解析到容器，容器 identity 不变，判据会“通过但没有证明” |
| 纯透明拦截层 | 可见但无语义、无保留子树的节点不发布（`build.go:201-208`）；判据看不到它。它通常只吞掉触摸，不触发别的动作 |
| 观察时就已经歧义的点 | 直接 422；agent 必须改用 `ref`（这是设计） |
| 点解析不到任何节点 | 回退前台级；没有证明 |
| 点上的节点身份不变但内容/像素变了 | 判据基于树，不看像素 |
| 证明之后、输入到达之前的变化 | 所有 `act` 共有的残余 |
| `swipe` 的终点 | 只证起点（§4.6） |
| 显示几何代次变化但节点 `Bounds` 没变 | `act` 的重验本来就不比较 `Display.Generation`（`act.go:279-284` 只看 `Exists`）；这是既有缺口，本提案不修，只在 `Bounds` 变化时拦住 |

### 7.3 成本

- 每次坐标 act：一次 `Preview` 公开树投影 + 两次 O(公开节点数) 遍历；纯 CPU，不增加设备读取。
- `ref` act：零额外成本。
- 日志：每次坐标 act 一条白名单事件。
- 承担者：坐标 act 的调用方；这是“用一点 CPU 换派发前的节点级证明”。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 解析器：唯一/歧义/无节点 | 三种状态正确；半开区间边界 |
| 坐标 act：观察后同点被浮层覆盖 | `target_changed`、不 tap |
| 坐标 act：目标位移/消失 | `target_changed`、不 tap |
| 坐标 act：节点 `Bounds` 变化 | `target_changed` |
| 坐标 act：观察时点歧义 | `422 ambiguous_point`、O 仍 ready、无采集 |
| 坐标 act：点无节点 | 按现状派发（前台级），记 `state=none` |
| 坐标 act：新树同点无节点 | `target_changed` |
| `text` 坐标目标 | 焦点点击前证明；绑定/读回不变 |
| `swipe` 起点 | 只证起点；终点变化不拒 |
| 反向对照 | “浮层覆盖必须拒绝”的用例在基线上是红的（基线会派发并返回 `executed`）；实现后必须转绿 |

### 8.2 实机/系统级

- 场景：同包内导航、节点 `Bounds` 变化、同包 Dialog/BottomSheet、WebView 内点按；
- 指标：构造遮挡/几何变化的拒绝率（期望 100%）、正常坐标点按的误拒率（期望接近 0）、
  `ref` act 无回归、坐标 act p95 增量（期望 < 10ms，实机校准）；
- 没有设备时明确写“未执行”。

### 8.3 记录

写入 `maintenance/verification/act-coordinate-point-proof.md`：实现提交、环境、实际命令、
真实结果、未执行项与原因。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 解析规则 | 最深、非 root、非 context、`Bounds` 非空；同级多候选 = 歧义 |
| 观察侧歧义 | 静态 `422 ambiguous_point`，不认领 |
| 新鲜侧不一致 | `409 target_changed`、`next=observe` |
| 无节点 | 回退前台级并记事件，不拒绝 |
| 精确身份 | 新树解析结果必须就是 `matchFreshNode` 的节点，不放宽到同子树 |
| `swipe` | 只证起点 |
| `text` | 证焦点点击点 |
| 日志 | 只记 `state`/`proven`/`ms`，不记坐标/ref/文本 |
| 错误码 | 新增 `ambiguous_point`（422） |
| 契约版本 | 不升 `contract_version`（字段未变；新增错误码登记在文档） |

### 9.2 未决

| 事项 | 谁定、何时 |
| --- | --- |
| 是否把 `swipe` 终点也纳入 | 人，提案确认时；默认不纳入（§4.6） |
| 是否在 MCP `act` 工具描述里加一句“坐标点会被证明” | 实现者，落 `docs/` 时；默认加一句，避免调用方误解 |
| 若实机误拒率不可接受，是否放宽到“同子树” | 实现者，实机验证后；届时回到本提案修订并重新确认 |

## 10. 实施顺序

1. `point.go` 纯解析器 + 单元测试（可独立验证）。
2. 静态歧义拒绝 + `ambiguous_point` + 文档登记。
3. 新鲜侧证明 + 用例（浮层/位移/无节点）。
4. 实机验证 → 文档审计 → 实现提交。

（1–2 可以同一提交；3 独立；4 在最后。）

## 11. 实现结果

实现提交：`2f43ef1d8c6150dde3d1038ce5ed2d31caeecfac`。实现与验证记录：
[act-coordinate-point-proof.md](../../verification/act-coordinate-point-proof.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 点解析：最深、非 root、非 context、有 bounds；同级并列 = 歧义；无候选 = 无节点 | `internal/engine/point.go`：`resolvePoint`、`pointState`、`coordinatePoint`；`internal/engine/point_test.go` |
| 观察侧三种结果；歧义在静态检查阶段返回 422，不认领、不采集 | `internal/engine/act.go`：`staticActContentCheck`、`resolveObservedPoint`；`internal/protocol/errors.go`：`CodeAmbiguousPoint` 与 422 映射；`docs/interface.md` §2/§6 |
| 新鲜侧证明：`matchFreshNode` 唯一匹配后，同一坐标在 `Preview` 的新鲜公开树中仍解析到同一 raw 节点 | `internal/engine/act.go`：`revalidateActTarget`、`provePointTarget`、`indexOf`；复用 `Projector.Preview` |
| `swipe` 只证起点；`text` 证焦点点击点；`ref` 判据不变 | `internal/engine/point.go`：`coordinatePoint`；`internal/engine/act.go` 的 ref 分支之前执行点证明；`act_point_test.go` |
| 白名单事件 `act.point`：`state`/`proven`/`ms`，不记坐标、ref、文本或树 | `internal/engine/act.go`：`logActPoint`；`internal/eventlog/log.go` 新增 `proven` 键；`TestActCoordinatePointRecordsDecisionFacts` |
| 文档与 MCP 描述 | `docs/interface.md` §2/§6；`internal/mcp/schema.go` 的 `act` 描述 |
| 反向对照与回归 | 变化场景在基线返回 `executed`、无 `act.point`；实现后转绿。`go test ./...`、`go vet ./...`、Linux/arm64 构建通过 |
| 实机验证 | `maintenance/verification/act-coordinate-point-proof.md`：正常坐标动作、同包状态变化、同包几何变化、同包导航变化、`ref` 回归、`text`/`swipe`/`long_press`、`act.point` 耗时 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 实施顺序 | 提案 §10 写作“实机验证 → 文档审计 → 实现提交”；实际为“实现提交 → 干净发布构建 → 实机验证 → 归档”。理由：发布清单要记录 `source_dirty=false`，必须从实现提交构建；实现提交仍先于归档，符合流程。判据、契约与时序未变 |
| 实机 `422 ambiguous_point` | 未执行：探测 App（9 个公开节点）与设置首页（128 个公开节点）没有同级重叠的最深候选，现有界面无法构造观察时歧义。单元测试覆盖三态、静态拒绝、O 仍 ready 且无额外采集 |
| 实机“点无节点”回退 | 未执行：探测 App 的公开树有覆盖全屏的非 root 容器，屏幕内每个点都会解析到节点；设置首页未做逐点回退扫描。单元测试覆盖 `state=none` 与前台级派发 |
| 实机同包 Dialog/BottomSheet | 未执行：探测 App 没有此类表面，未用生产 App 或破坏性操作构造；单元测试用全屏同级浮层与按钮内深层新节点覆盖拒绝路径 |
| 实机 WebView/canvas 内部内容 | 未执行：没有 WebView 测试页；这是提案 §7.2 的已知边界，判据只到容器身份 |
| 大树 p95 | 只测了 9 与 128 个公开节点的树；实机 `act.point` 均落在整数毫秒的 0，低于事件分辨率，未对更大树采样 |
| 放宽到“同子树” | 不需要：实机正常坐标动作 8/8 成功、0 误拒，没有出现需要放宽判据的误拒 |
| MCP 传输层坐标动作 | 未单独通过 MCP 宿主发起；坐标证明在引擎/HTTP 路径完成，MCP 只做工具投影与描述。发布清单的工具目录指纹已随描述更新并核验 |
| `go test -race` | 未执行：Windows 宿主无 C 编译器，`CGO_ENABLED=0` 目标下不可用；与既有记录一致 |

---

## 附：本提案不改变的事项

- `ref` 目标的判据与行为；
- `observe`、`start`、`open_app`、`shell`；
- 请求/结果字段；
- `contract_version`；
- 公开树编码、full/delta、settle；
- 设备读取次数（坐标 act 不增加设备读取）；
- 日志白名单之外的内容。
