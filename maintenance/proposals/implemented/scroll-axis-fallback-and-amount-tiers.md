# 提案：受控滚动轴向兼容选择、父级自动回退与四档精细幅度

状态：已实现（实现提交 `38c7b8258d30398cd5e5bfb016fb6964b091eee2`，`2026-11-23`）。
实现与验证记录：[scroll-axis-fallback-and-amount-tiers.md](../../verification/scroll-axis-fallback-and-amount-tiers.md)。
基线提交：`8c494a35179cb6d27e139c5d60e98f37d4229505`（分支 `master`，`2026-11-23`）。
适用范围：`observation` 公开树构建中 `role=scroll` 节点的 `axis` 投影、`act` 的 `scroll` 分支静态容器轴向兼容判定、父级自动回退与唯一内层兼容兜底、认领后实时重验的轴向兼容守卫、`scroll.amount` 四档幅度枚举与联动手势时长，以及 MCP 工具描述与发现说明同步。
影响范围：`internal/observation`（`build.go` 及测试）、`internal/protocol`（`requests.go`、`encode.go` 及测试）、`internal/engine`（`scroll.go`、`act_target_shared.go` 及测试）、`internal/mcp`（`schema.go`、`schema_test.go`）、`scripts/mcp-probe`（`catalog.go` 及测试）、受影响的正式文档（`docs/interface.md`、`docs/architecture.md`、`docs/mcp.md`、`docs/integration.md`、`docs/client.md`、`docs/release.md`、`docs/runbook.md`、`docs/control-protocol.md`、`docs/phone-operations.md`、`README.md`）；实施发布时涉及 `release/versions.json` 及版本生成物、手机整组构建/清单/覆盖部署、主机侧 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 及 `maintenance/verification/`。Android 控制 App 业务逻辑与固定 `uiautomator` JAR 不变。
契约（`/api/*` 字段与 `contract_version`）：改变——`act.operation.amount` 枚举由 `page|half` 扩充为 `step|half|page|full`（省略仍默认 `page`），并按档位联动手势时长（`500ms`/`450ms`/`400ms`/`300ms`）与 `step` 最小行程（`32px`）；公开树 `role=scroll` 节点在满足确定性类名或子项单轴展布证据时自动投影 `axis`（`"x"`/`"y"`）；`scroll` 静态解析引入 A+C 轴向兼容判定与向兼容父滚动容器的自动回退，无可回退兼容容器时返回 `422 unsupported_node_action`（新增封闭 `reason="axis_mismatch"`）并附可执行 `candidates`；共同 `contract_version` 拟由 `26` 前进至 `27`（配套建议 `release_version="0.31.0"`、`android_version_code=33`）；本提案落盘不修改版本源。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：[build.go](../../../internal/observation/build.go)、[scroll.go](../../../internal/engine/scroll.go)、[act_target_shared.go](../../../internal/engine/act_target_shared.go)、[requests.go](../../../internal/protocol/requests.go)、[encode.go](../../../internal/protocol/encode.go)、[schema.go](../../../internal/mcp/schema.go)、[catalog.go](../../../scripts/mcp-probe/catalog.go)、[interface.md](../../../docs/interface.md)、[versions.json](../../../release/versions.json)）。

---

## 0. 摘要

当前 `act(scroll)` 在 `container.ref` 指向横向滚动条（如顶部 Tab 栏 `n194`）并请求纵向滚动（`down`/`up`）时，既不检查容器轴向与请求方向的兼容性，也不向可执行的纵向父滚动容器（如外层主列表 `n188`）回退，只要横条高度 ≥ `128px`（满足 `48px` 最小行程）就会向横条派发一段无意义的纵向短手势并返回 `outcome=executed`（远程 [Issue #42](https://github.com/malyjacob/mobile-use-vnext/issues/42)）。同时，`scroll.amount` 仅有 `page`（`60%`）和 `half`（`30%`）两档且固定 `400ms` 时长，缺少防惯性微调与长距快翻档位。

本提案采取 **“A+C 轴向兼容判定 + 父级自动回退 + 四档精细幅度与时长联动”** 路线：
1. **公开 `Node.Axis` 确定性投影（A）+ 规划期嵌套正交扁平几何守卫（C）**：在 `observation` 构建期基于明确类名（如 `HorizontalScrollView` vs `ScrollView`/`NestedScrollView`/`ListView`）与子项单轴展布走势为 `role=scroll` 节点填充 `axis="x"|"y"`（不确定时留空 `""`）；在 `scroll` 规划期同时校验公开 `axis` 与嵌套扁平几何（处于更大滚动容器内且正交宽高比 ≥ `2:1`、请求轴跨度 < 参考跨度 `25%` 的短横条/窄竖条），拦截轴向失配。
2. **轴向失配时自动向父级回退（顺应“弱保软”）**：显式 `container.ref`（直接滚动容器或非滚动子节点提升）、坐标 `container={x,y}` 遇到轴向不兼容的滚动容器时，自动沿公开父链回退到最近一个轴向兼容且可生成安全轨迹的父滚动容器直接执行，免去调用方额外重走；仅在父链与唯一内层兼容后代均无解时，认领前静态返回 `422 unsupported_node_action`（`reason="axis_mismatch"`）并附页面可执行 `candidates`。
3. **四档精细滚动幅度与联动时长**：将 `scroll.amount` 扩充为 `step`（`15%`、`500ms`、最小行程 `32px`）、`half`（`30%`、`450ms`、`48px`）、`page`（默认，`60%`、`400ms`、`48px`）、`full`（`85%`、`300ms`、`48px`），同步对接 MCP 工具说明与发现指引（严守不披露 `quality`/`effect` 的描述面门禁）。
4. **明确不做**：不在单次 `act` 内派发两次手势（不做派发后因未移动而二次滚父容器），不在 `Result` JSON 或 MCP `result v4` 文本中新增 `used_ref`/`selection`/`movement`/`attempts` 等额外字段，不修改 `u2.jar` 固定制品。

---

## 1. 问题

### 1.1 现象与代价

分诊为**取舍**：现行契约（[docs/interface.md:565-612](../../../docs/interface.md#L565-L612)）明确规定“`direction` 是调用方声明的阅读/视口方向，不是服务证明的设备滚动轴”且“`ref` 节点本身为有效可滚动容器时直接对其规划”，当前行为符合现行规范，但在嵌套异轴滚动页面与精细滚动场景中产生显著的空转代价：

1. **Issue #42（横向滚动条上请求纵向 `down` 静默空转）**：
   - 在 `1080×2400` 虚拟屏实机测试中，页面顶部为短横向 Tab 滚动条 `n194`，外层为纵向内容主列表 `n188`（二者呈嵌套父子关系）。调用方发起 `{"type":"scroll","container":{"ref":"n194"},"direction":"down","amount":"page"}` 时，服务返回 `outcome=executed`，但页面内容完全未移动（`tree v4 delta ... changes=0`）；随后调用方手动改为外层 `n188` 才正常滚动。
   - **触发门槛低**：对 `amount="page"`（`60%`），现行 `scrollTravelForExtent`（[scroll.go:516-538](../../../internal/engine/scroll.go#L516-L538)）要求 `usable = extent - 2*max(24, extent/10)` 且 `usable * 60 / 100 >= 48`。只要横向 Tab 条或横向轮播带的高度 **≥ `128px`**（`128 - 48 = 80`，`80 * 60 / 100 = 48px`），就能在纵向算出一条 `48px+` 的迷你轨迹并直接派发！
   - **三条通路同时受影响**：
     - 显式 `container.ref` 指向横向容器 `n194`：直接对 `n194` 派发纵向迷你滑动；
     - 显式 `container.ref` 指向 `n194` 内的普通文本节点（如某个 Tab 标题）：`nearestScrollAncestor` 找到 `n194` 即停止，同样对 `n194` 派发纵向迷你滑动；
     - 坐标 `container={x,y}` 落在 `n194` 区域：最内层容器 `I[0]` 为 `n194`，只要 `n194` 高度 ≥ `128px` 能算出轨迹，就不会触发 `resolveScrollPointPlan` 的父链回退。
   - **模型视角代价更隐蔽**：自 `0.27.0`（[wait-content-stability-and-text-omit.md](wait-content-stability-and-text-omit.md)）起，MCP `text`/`both` 文本结果已将 `effect` 显式过滤（`v4Omitted`）。调用方在 `text` 模式下只能看到 `result v4 ok` 与 `changes=0` 的空 delta，无法区分“是当前容器已经滚到底了”还是“选错了横向容器根本没滚起来”，往往需要多花 1～2 轮额外调用甚至误以为页面到底。
2. **滚动幅度粒度粗糙且时长单一**：
   - 目前 `amount` 仅支持 `page`（`60%`）和 `half`（`30%`），手势时长恒为 `400ms`（[scroll.go:16-21](../../../internal/engine/scroll.go#L16-L21)）。
   - 在需要微调（如滚出被底部栏挡住半截的按钮、调整日期/时间滚轮 Picker、小高度浮层列表）时，即使是 `half`（全屏约 `576px`）也经常一次滚过头；而在长文或长评论区快速拉到底时，`page`（`60%`）又偏慢。此外，固定 `400ms` 在大跨度下与小跨度下速度差异大，小跨度微调需要更慢的手势（`500ms`）抑制松手后的惯性抛滑（fling），大跨度快翻（`85%`、`300ms`）则需要更干脆的推进。

### 1.2 证据链

#### 通路一：`Node.Axis` 已有协议定义但无生产者，`scroll` 规划完全忽略轴向
1. **协议与呈现层已支持 `Axis`，但投影层恒留空**：[internal/protocol/node.go:59-72, 141](../../../internal/protocol/node.go#L59-L72) 定义了 `Axis`（`AxisX="x"`、`AxisY="y"`、`AxisBoth="both"`），`ReadNode`、`ReadDelta`、`treetext`（[internal/treetext/encode.go:442-445](../../../internal/treetext/encode.go#L442-L445)）均已支持序列化 `axis=`；但设备采集 `device.RawNode` 只有 `HasScrollable`/`Scrollable bool`（[internal/device/types.go:109-110](../../../internal/device/types.go#L109-L110)），且 [internal/observation/build.go:406-455](../../../internal/observation/build.go#L406-L455) 的 `newPublicNode` 从未对 `node.Axis` 赋值。因此运行时所有公开节点的 `Axis` 均为 `""`。
2. **`resolveScrollRefPlan` 不检查轴向也不回退父容器**：[internal/engine/scroll.go:869-871](../../../internal/engine/scroll.go#L869-L871) 只要 `isScrollContainer(target)` 为真，立即调用 `planScrollContainer(obs, view, operation, target, "ref", scrollModeDirect, true, false)`；[scroll.go:1090-1145](../../../internal/engine/scroll.go#L1090-L1145) 的 `planScrollContainer` 仅在 3 条轴线均失败时尝试内层后代 `tryUniqueInnerScrollFallback`，绝不向上查找父级滚动容器。
3. **非滚动节点祖先提升只看最近一层 `role=scroll`**：[internal/engine/scroll.go:881-906](../../../internal/engine/scroll.go#L881-L906) 调用 `nearestScrollAncestor(view, target)`（[scroll.go:1213-1233](../../../internal/engine/scroll.go#L1213-L1233)），遇到第一个 `parent.Role == protocol.RoleScroll` 即返回，不检查该祖先是否支持当前 `direction` 对应的滚动轴。
4. **`planScrollOnAxes` 与 `tryPlanScrollBase` 只看请求方向，不看容器轴向或正交扁平比**：[internal/engine/scroll.go:673-763](../../../internal/engine/scroll.go#L673-L763) 的 `planScrollOnAxes` 仅由 `scrollDirectionVertical(direction)` 决定轨迹方向；[scroll.go:767-797](../../../internal/engine/scroll.go#L767-L797) 的 `tryPlanScrollBase`（用于 `buildStaticCandidates` 计算 `executable` 及坐标公共祖先评分）同样不检查容器轴向，导致报错返回的 `candidates` 里也可能把横向 Tab 条标成对 `down` 请求 `executable=true`。

#### 通路二：滚动幅度常量、时长与协议/MCP 校验链
1. **引擎幅度与时长写死两档**：[internal/engine/scroll.go:15-21](../../../internal/engine/scroll.go#L15-L21) 硬编码 `scrollPagePercent = 60`、`scrollHalfPercent = 30`、`minScrollTravel = 48`、`scrollDurationMS = 400`；[scroll.go:88-97](../../../internal/engine/scroll.go#L88-L97) 的 `normalizeScrollAmount` 与 [scroll.go:516-538](../../../internal/engine/scroll.go#L516-L538) 的 `scrollTravelForExtent` 只处理 `page` 和 `half`；[scroll.go:1657-1658](../../../internal/engine/scroll.go#L1657-L1658) 的 `dispatchScroll` 固定传入 `scrollDurationMS`。
2. **协议解码与编码锁死两档**：[internal/protocol/requests.go:87-90, 744-759](../../../internal/protocol/requests.go#L87-L90) 的 `parseScrollAmount` 与 [internal/protocol/encode.go:342-344](../../../internal/protocol/encode.go#L342-L344) 仅允许 `page` 或 `half`。
3. **MCP Schema、工具说明与探针检查**：[internal/mcp/schema.go:455-460](../../../internal/mcp/schema.go#L455-L460) 定义 `amount.enum` 为 `["page", "half"]`；[internal/mcp/schema_test.go:27, 354-396](../../../internal/mcp/schema_test.go#L27) 设有 `baselineDiscoveryBytes = 37500` 体积上限与 `TestDiscoveryOmitsObservationInternals`（严禁出现 `quality`/`effect`/`观察质量`/`滚动效果`/`observed`/`not_observed`/`unstable` 等词）；[scripts/mcp-probe/catalog.go:130-170](../../../scripts/mcp-probe/catalog.go#L130-L170) 的 `checkActScrollSchema` 校验 live `act` 工具的 `scroll` 属性结构。

### 1.3 为什么直觉会出错

- **“在一次 `act(scroll)` 里如果发现 `effect=not_observed`（没滚起来），再自动去滑一次父容器”**：
  1. 违反现行架构“每次派发只有一个 UI 派发窗口”（[docs/interface.md:754](../../../docs/interface.md#L754)、[scroll.go:1654-1668](../../../internal/engine/scroll.go#L1654-L1668) `beginUIDispatch` → `confirmDispatch`）；
  2. 计算 `effect` 必须等第一次滑动做完完整的 `post-observation` 收敛采样（最坏 `2s`），二次派发+二次收敛采样会让单次 `act` 时延翻倍；
  3. 更致命的是，当一个正常的纵向内层列表**真的已经滑到底部边界**时，Android XML 没有任何可靠属性区分“到底了”与“滑错了容器”，派发后回退会把外层页面误滑走，直接违背 Issue #42 验收方向第 4 条（“纵向列表已到底后再次 `down`：不回退到页面外层容器”）。必须在**静态规划阶段（派发前）**完成轴向兼容判断与父级回退！
- **“在 `Result` JSON 里新增 `requested_ref`/`used_ref`/`selection`/`movement`/`attempts` 等字段”**：
  违反 [process.md](../../../maintenance/process.md) §6 最小侵入原则，且 MCP `text` 模式为节省上下文 Token 已经过滤了 `effect` 与 `quality`；当静态阶段自动回退到兼容父容器并成功滚动后，调用方直接从返回的 `tree v4 delta` 中就能看到滚出的新列表项（与现有的 `scroll_ancestor_lift`、`scroll_inner_fallback`、`coextensive_child` 完全一致，均不污染 `Result` 顶层字段）。
- **“只靠 `ClassName` 判断 `Axis`，或只靠容器高宽比判断 `Axis`”**：
  现代 Android 应用大量使用 `androidx.recyclerview.widget.RecyclerView` 或自定义 `ViewGroup` 承载横向 Tab 和纵向列表，单看类名区分不出 `RecyclerView` 的方向；反之，正方形横向轮播图（如 `1080×900`）高宽比并不扁平，单看容器外框高宽比也拦不住。必须把 **A（类名 + 子项排布走势 → 公开 `Node.Axis`）** 与 **C（嵌套正交扁平几何守卫）** 结合起来。
- **“把所有 `role=scroll` 节点都强行猜一个 `axis=x` 或 `axis=y`”**：
  当 `RecyclerView` 刚加载完只有 0～1 个可见子项、或二维网格/双向画布（如表格、地图）子项在 X/Y 都有展开时，强猜单轴会导致合法方向被误拒。证据不足时必须保持 `axis=""`（未知，按轴向兼容放行，仅由 C 的极端扁平嵌套几何守卫拦截明显的短横条/窄竖条）。

---

## 2. 目标与非目标

### 目标

- **G1（公开 `Node.Axis` 确定性投影 —— 方案 A）**：在 `observation` 构建公开树时，对每个非 `context` 的 `role=scroll` 节点，按确定性类名与子项单轴排布走势推断并填充 `Node.Axis`（`"x"` 或 `"y"`；无把握或双向时保持 `""`）。`tree v4`、`inspect v3`、JSON full/delta 自动输出该 `axis` 属性，让调用方在读树时即可看到滚动轴向。
- **G2（A+C 轴向兼容判定与静态自动回退）**：
  - 将 `up`/`down` 映射为请求轴 `y`，`left`/`right` 映射为请求轴 `x`。
  - 容器若存在公开 `Axis` 且与请求轴冲突（`axis="x"` 请求 `y`，或 `axis="y"` 请求 `x`），或满足 **C（嵌套正交扁平几何守卫）**，则判定为**轴向不兼容**。
  - 显式 `container.ref`（直接滚动容器或非滚动节点祖先提升）遇到轴向不兼容时，**自动沿公开父链向上回退到最近一个轴向兼容且可生成安全轨迹的父滚动容器**（`mode="scroll_axis_fallback"`）；若父链无解，直接容器仍可尝试唯一内层轴向兼容子容器兜底；若均无解，认领前静态返回 `422 unsupported_node_action`（`reason="axis_mismatch"`），并在 `error.details.candidates` 中附页面上对当前方向可执行的滚动容器（`executable=true`）。
  - 坐标 `container={x,y}` 与 `buildStaticCandidates` 的 `tryPlanScrollBase` 统一接入同一轴向兼容门禁。
- **G3（四档精细滚动幅度与时长联动）**：将 `scroll.amount` 扩充为 `step`（`15%`、`500ms`、最小行程 `32px`）、`half`（`30%`、`450ms`、`48px`）、`page`（默认，`60%`、`400ms`、`48px`）、`full`（`85%`、`300ms`、`48px`）。
- **G4（工具说明、探针与文档同步）**：同步更新 [internal/mcp/schema.go](../../../internal/mcp/schema.go) 的 `callerGuidance`、`act` 工具描述及 `container`/`amount` 参数说明，更新 [scripts/mcp-probe/catalog.go](../../../scripts/mcp-probe/catalog.go) 的 `checkActScrollSchema`（校验 `amount.enum` 四档），更新正式文档并通过独立只读文档审计。
- **G5（契约升级与整组发布交付）**：推进 `contract_version 26 → 27`，完成全量测试、手机整组覆盖部署、最小实机闭环、Windows 主机 `device-mcp.exe` 与 `mcp-probe.exe` 替换及双入口独立探针验证；推送后回复并关闭 Issue #42。

### 非目标

- **不做同次 `act` 内的二次手势派发**：不实现“派发一次滑动后若 `effect=not_observed` 再自动滑父容器”，严守单次 UI 派发窗口，杜绝纵向列表到底后误滚外层页面。
- **不新增 `Result` 顶层或嵌套响应字段**：不添加 `requested_ref`、`used_ref`、`selection`、`requested_axis_supported`、`movement`、`attempts` 等字段；保持 MCP `result v4` 紧凑外壳与 `effect`/`quality` 在 `text` 模式下的省略规则。
- **不修改 `third_party/uiautomator/u2.jar` 与 Android 控制 App 业务代码**：不改动固定 JAR 的 XML dump 属性。
- **不接受任意百分比数字作为 `amount`**：`amount` 保持严格闭合的四值字符串枚举，拒绝数字、百分比字符串或未知词。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持 `Node.Axis=""`、不校验轴向、不向父级回退、`amount` 仅 `page`/`half` | 零改动 | Issue #42 横向 Tab 条请求 `down` 继续静默派发无效手势（`executed` 但 `changes=0`）；微调场景持续滚过头 |
| R2 仅改 `u2.jar` 读取 Android 无障碍 Action + 轴不匹配直接报 422 | 修改 smali 补丁导出 `AccessibilityAction`；显式 `ref` 轴不匹配直接报 422，不自动回退父容器 | 需重编/核验固定 `u2.jar` 制品；调用方传了内层 ref 仍需多花一轮 422 重试 | 定制 View 未必标准上报四向 `AccessibilityAction`；违背“意图明确时一次做对”的弱保软原则 |
| R3 派发后看 `effect=not_observed` 再二次派发滚父容器 | 先向原容器派发手势，采完后观察若未移动再在同一 `act` 内向父容器派发第二次手势 | 打破单次派发窗口硬约束；单次 `act` 耗时翻倍；结果/账本状态机复杂化 | 内层纵向列表真正到底部边界时，会误触发二次派发把外层页面滑走 |
| **R4 A+C 静态轴向兼容判定 + 父级自动回退 + 四档精细幅度（选定）** | `observation` 投影确定性 `Node.Axis`（A）+ 规划期嵌套正交扁平几何守卫（C）；轴向失配时静态自动沿父链回退到兼容父滚动容器执行（无兼容父容器才报 422 `axis_mismatch` + `candidates`）；`amount` 扩充为 `step`/`half`/`page`/`full` 四档并联动手势时长 | 共同契约升级（`26 → 27`）；公开树 `role=scroll` 节点新增已定义的 `axis` 输出；需同步单测、探针与文档 | 纯自绘、仅 0～1 个子项且非嵌套扁平条带的未知通用滚动容器无法静态判定单轴（保持 `axis=""` 放行，见 §7.2） |

**选择理由**：R4 在静态阶段（零设备 I/O、认领前）通过 A+C 双重证据准确识别横向 Tab 条、横向轮播带与纵向列表的轴向差异，并直接自动回退到外层兼容父容器一次做对，既消除了无效手势与额外重试轮次，又完全不触碰单次派发窗口红线，同时补齐了 `step`/`half`/`page`/`full` 四档精细滚动控制。

---

## 4. 判据

本节所有面积与比例乘法均使用 `int64` 整数运算，所有矩形与区间使用半开边界 `[Left, Right)`、`[Top, Bottom)`。

### 4.1 方案 A：公开树 `role=scroll` 节点的 `Axis` 确定性推断

在 [internal/observation/build.go](../../../internal/observation/build.go) 的 `buildAgainst` 组装完公开树父子连接（`148-163` 行）之后、生成 `Snapshot` 之前，对公开树中每个满足 `!node.Context && node.Role == protocol.RoleScroll && node.Bounds != nil` 的节点 `node`（对应原始节点 `raw = &cap.Nodes[i]`），按以下两级确定性规则推断 `node.Axis`：

1. **第 1 级：原生类名强特征（`inferScrollAxisFromClass(raw.ClassName)`）**：
   - 若 `strings.Contains(class, "HorizontalScrollView")` → 返回 `protocol.AxisX`（`"x"`）；
   - 若 `strings.Contains(class, "NestedScrollView")`、`strings.Contains(class, "ListView")`、`strings.Contains(class, "AbsListView")`，或（`strings.Contains(class, "ScrollView")` 且 `!strings.Contains(class, "Horizontal")`） → 返回 `protocol.AxisY`（`"y"`）；
   - 若 `strings.Contains(class, "ViewPager")`：若类名同时含 `Vertical` 则返回 `protocol.AxisY`，否则返回 `protocol.AxisX`（Android 标准 `ViewPager` / `ViewPager2` 默认横向翻页；若子项排布在第 2 级呈现出明确纵向单轴走势，则以第 2 级子项实测排布覆盖）。
2. **第 2 级：子项单轴排布走势（`inferScrollAxisFromChildren(node)`，适用于 `RecyclerView`、通用自定义滚动容器及校验 `ViewPager`）**：
   - **采样层选定**：取 `node.Children` 中所有非 `context`、有正面积 `Bounds` 的直接公开子节点切片 `items`。若 `len(items) == 1` 且该唯一子节点 `only` 不是滚动容器（`only.Role != protocol.RoleScroll`），则向内展开一层取 `only.Children` 中非 `context`、有正面积 `Bounds` 的子节点作为 `items`（兼容 `ScrollView`/`RecyclerView` 内包一层单根内容 `ViewGroup` 的常见结构）。
   - 若 `len(items) < 2`：无法由子项排布推断，保留第 1 级结果（无第 1 级结果则为 `""`）。
   - **单轴走势计算**（对 `items[0..k-1]`，最多取前 `16` 个子项以保证常数级有界开销）：
     - 计算相邻子项中心点的位移绝对值累加：`sumDX = Σ |centerX(items[j]) - centerX(items[j-1])|`，`sumDY = Σ |centerY(items[j]) - centerY(items[j-1])|`；
     - 计算全体采样子项中心的包络跨度：`spanX = max(centerX) - min(centerX)`，`spanY = max(centerY) - min(centerY)`；
     - 统计相邻子项在正交轴上的区间重叠对数：`overlapYCount`（相邻两项 `[Top, Bottom)` 有正交集的对数）与 `overlapXCount`（相邻两项 `[Left, Right)` 有正交集的对数），记总相邻对数 `pairs = len(items) - 1`。
     - **横向单轴判据（`AxisX`）**：当 `spanX >= 24`、`sumDX >= 2 * sumDY`、`spanX >= 2 * spanY`，且 `overlapYCount * 2 >= pairs`（至少半数相邻子项处于同一水平带）时 → 判定为 `protocol.AxisX`（`"x"`）。
     - **纵向单轴判据（`AxisY`）**：当 `spanY >= 24`、`sumDY >= 2 * sumDX`、`spanY >= 2 * spanX`，且 `overlapXCount * 2 >= pairs`（至少半数相邻子项处于同一垂直列）时 → 判定为 `protocol.AxisY`（`"y"`）。
   - 若第 2 级命中明确单轴，使用第 2 级结果；否则沿用第 1 级结果；两级均未命中时保持 `node.Axis = ""`（未知，不在公开树输出 `axis` 字段）。
3. **对保守保号（`match.go`）与 compact 裁剪（`read.go`）的兼容性核查**：
   - [internal/observation/match.go:59-85](../../../internal/observation/match.go#L59-L85) 的 `matchKey` 与 `publicKey` 比较的是 `role`/`class`/`pkg`/`resource`/`label`/`anchor`/`actions`/`editable`/`protected`，`Axis` 不改变保号签名，不会引起既有 ref 断号；
   - [internal/observation/read.go:19-37](../../../internal/observation/read.go#L19-L37) 的 `AnonymousPlain` 仅针对 `RoleView` 和 `RoleGroup`（`RoleScroll` 本身带有 `ActionSwipe`，不是匿名普通壳），因此为 `RoleScroll` 填充 `Axis` 不会影响匿名容器折叠或几何隐藏。

### 4.2 方案 A+C：滚动容器轴向兼容判定（`scrollContainerAxisCompatible`）

记请求方向 `direction` 对应的请求轴：
- `up` / `down` → 请求纵向滚动（`vertical = true`，需要 `y` 轴能力）；
- `left` / `right` → 请求横向滚动（`vertical = false`，需要 `x` 轴能力）。

给定观察状态 `obs`、公开视图 `view`、候选滚动容器公开节点 `node` 及请求方向 `direction`，按以下 A、C 两步判定 `node` 是否与 `direction` **轴向兼容**：

1. **A 判据（公开 `node.Axis` 显式检查）**：
   - 若 `vertical == true`（`up`/`down`）且 `node.Axis == protocol.AxisX` → **不兼容（返回 `false`）**；
   - 若 `vertical == false`（`left`/`right`）且 `node.Axis == protocol.AxisY` → **不兼容（返回 `false`）**；
   - 若 `node.Axis == protocol.AxisY`（当 `vertical == true`）、`node.Axis == protocol.AxisX`（当 `vertical == false`）或 `node.Axis == protocol.AxisBoth` → A 判据通过，继续检查 C 判据是否被显式 `Axis` 豁免：
     - **豁免规则**：当 `node.Axis` 已由 §4.1 明确推断为**支持当前请求轴**（即 `vertical && (node.Axis == AxisY || node.Axis == AxisBoth)`，或 `!vertical && (node.Axis == AxisX || node.Axis == AxisBoth)`）时，说明其类名或子项排布已证实它是该轴向的滚动容器（例如一个小高度的纵向滚轮选择器 `ScrollView` / 纵向子项列表），直接判定**兼容（返回 `true`）**，不被 C 几何规则误杀！
2. **C 判据（未知轴向 `node.Axis == ""` 时的嵌套正交扁平几何守卫）**：
   - 当 `node.Axis == ""`（例如子项尚未加载或子项只有 1 个的横条/竖条）时，检查 `node` 是否存在非同视口的外层滚动祖先 `outer`（沿公开父链存在满足 `isScrollContainer(outer)` 且 `!isCoextensiveRect(nodeClipped, outerClipped)` 的祖先）：
     - 若不存在此类外层滚动祖先 → **兼容（返回 `true`）**（独立滚动容器不因扁平比自拒）；
     - 若存在外层滚动祖先 `outer`，取最邻近的非同视口外层滚动祖先裁剪矩形 `outerRect` 与屏幕矩形 `screen`：
       - **纵向请求（`up`/`down`）遇横向扁平条带**：若 `nodeRect.Width() >= 2 * nodeRect.Height()`（宽至少是高的 2 倍），且 `nodeRect.Height() * 4 < max(outerRect.Height(), screen.Height())`（高度严格小于外层滚动容器或屏幕高度的 `25%`） → **不兼容（返回 `false`）**；
       - **横向请求（`left`/`right`）遇纵向细长条带**：若 `nodeRect.Height() >= 2 * nodeRect.Width()`（高至少是宽的 2 倍），且 `nodeRect.Width() * 4 < max(outerRect.Width(), screen.Width())`（宽度严格小于外层滚动容器或屏幕宽度的 `25%`） → **不兼容（返回 `false`）**。
   - 其余情况 → **兼容（返回 `true`）**。

> **性质保证**：
> - 若 `n194` 是顶部横向 Tab 条（无论它因有 ≥2 个横向 Tab 被 A 标为 `axis="x"`，还是只有 1 个子项 `axis=""` 但满足 `1080×140` 扁平嵌套在外层列表 `n188` 内被 C 命中），请求 `down`/`up` 时均会被准确判定为**轴向不兼容**；请求 `left`/`right` 时则**完全兼容**。
> - 若 `n200` 是一个小高度但明确为纵向的组件（如 `ScrollView` 或内含纵向排列子项的 Picker，A 已标为 `axis="y"`），请求 `down`/`up` 时因豁免规则**保持兼容**，不会被 C 误拦。

### 4.3 轴向失配时的父级自动回退、子容器兜底与静态 `422 axis_mismatch`

1. **基础单目标干跑 `tryPlanScrollBase` 接入轴向兼容守卫**：
   - 在 [internal/engine/scroll.go:767-797](../../../internal/engine/scroll.go#L767-L797) 的 `tryPlanScrollBase(obs, view, node, direction, amount)` 中，在 `isScrollContainer(node)` 之后立即检查 `scrollContainerAxisCompatible(obs, view, node, direction)`：不兼容直接返回 `false`。
   - 这样 `buildStaticCandidates` 计算 `executable` 以及坐标模式的公共祖先筛选，都会自动将轴向不兼容的容器标为 `executable=false`。
2. **显式 `container.ref` 指向可滚动容器 `target`（`isScrollContainer(target) == true`）**：
   - **情况 1：`target` 与 `operation.Direction` 轴向兼容**：
     - 保持现有行为：调用 `planScrollContainer(obs, view, operation, target, "ref", scrollModeDirect, true, false)`；
     - 在 `tryUniqueInnerScrollFallback`（[scroll.go:1147-1211](../../../internal/engine/scroll.go#L1147-L1211)）扫描子树内后代滚动容器时，增加 `scrollContainerAxisCompatible(obs, view, node, operation.Direction)` 过滤，确保内层兜底只考虑轴向兼容的后代容器。
   - **情况 2：`target` 与 `operation.Direction` 轴向不兼容（Issue #42 核心路径）**：
     - **第 1 步（沿公开父链自动回退 `scroll_axis_fallback`）**：从 `target` 沿公开父链向上（跳过 `context`、止于 `root` 之前）依次检查每个满足 `isScrollContainer(parent)` 且 `scrollContainerAxisCompatible(obs, view, parent, operation.Direction)` 的滚动祖先 `parent`；对首个能通过 `planScrollContainer(obs, view, operation, parent, "ref", scrollModeAxisFallback, false, false)` 生成可证明安全轨迹的兼容祖先，**直接选用该祖先返回 `scrollPlan`**（认领后重验该选定祖先 `parent`，不绑定原横向子条 `target`）。若父链上存在兼容祖先进入 `planScrollContainer` 但均规划失败，记录首个兼容祖先的规划错误 `firstCompatErr`（如 `422 no_safe_trajectory`）。
     - **第 2 步（唯一内层兼容子容器兜底）**：若父链上没有成功规划的兼容滚动祖先，尝试在 `target` 子树内调用带轴向兼容过滤的 `tryUniqueInnerScrollFallback`（覆盖外层是横向 ViewPager、内层唯一可见页是纵向 RecyclerView 且调用方传了外层 ViewPager ref 请求 `down` 的常见场景！）。若恰有 1 个折叠后唯一的轴向兼容可执行内层子滚动容器，自动委托给它（`mode="scroll_inner_fallback"`）。
     - **第 3 步（认领前静态拒绝）**：若父链与唯一内层兜底均无解：
       - 若第 1 步中曾有轴向兼容的父滚动祖先进入规划但因轨迹阻塞/行程不足失败（`firstCompatErr != nil`），返回该 `firstCompatErr`（与坐标模式的回退错误优先级保持一致）；
       - 否则（整条父链与内层均无轴向兼容的可滚动容器），在认领 `O` 前返回：
         - HTTP `422`，`code="unsupported_node_action"`；
         - `message="滚动容器轴向与请求方向不匹配"`；
         - `details.reason = "axis_mismatch"`（新增封闭枚举常量 `staticReasonAxisMismatch = "axis_mismatch"`）；
         - `details.scope = "target"`，`details.scope_ref = target.Ref`，`details.target_actions = tapPublicActions(target)`；
         - `details.candidates`：收集祖先与页面滚动容器（`collectScrollAncestorAndPageCandidates(obs, view, target)`，经 `buildStaticCandidates` 干跑后，轴向兼容且可规划的候选 `executable=true` 自动排在前面，调用方可直接取 `candidates[0].ref`）。
3. **显式 `container.ref` 指向非滚动节点 `target`（`target.Role != protocol.RoleScroll`）的祖先提升**：
   - 沿公开父链向上查找所有 `parent.Role == protocol.RoleScroll`（非 `context`、非 `root`）的滚动祖先链 `scrollAncestors`：
   - 若 `len(scrollAncestors) == 0`：保持现有 `422 unsupported_node_action`（`reason="not_scrollable"`, `scope="target"`, `scope_ref=target.Ref`）+ 页面 `candidates`。
   - 若最近滚动祖先 `nearest = scrollAncestors[0]` 自身禁用或无公开几何：保持既有契约，直接返回 `422 unsupported_node_action`（`reason="ancestor_invalid"`, `scope="ancestor"`, `scope_ref=nearest.Ref`, `ancestor_ref=nearest.Ref`）+ 页面 `candidates`；若 `nearest` 缺 `swipe`，直接返回 `reason="ancestor_missing_action"`（不因轴向回退改变非轴向失配场景下既有 `ancestor_invalid` / `ancestor_missing_action` 的契约语义）。
   - 若 `nearest` 有效（`isScrollContainer(nearest)` 为真且有公开裁剪几何）：
     - 从 `nearest` 开始沿 `scrollAncestors` 依次检查每个有效滚动祖先 `A`（若后续外层 `A` 禁用/无几何/缺 `swipe` 则停止或跳过，只取满足 `isScrollContainer(A)` 且有公开几何者）：
     - 若 `A` 满足 `scrollContainerAxisCompatible(obs, view, A, operation.Direction)`：对首个此类兼容祖先 `A` 调用 `planScrollContainer(obs, view, operation, A, "ref", mode, false, true)`（若 `A == nearest` 则 `mode = scrollModeAncestorLift`；若 `A` 跳过了近层轴向不兼容的滚动祖先，则 `mode = scrollModeAxisFallback`）。若该兼容祖先规划成功则直接返回 `plan`；若失败则继续尝试更外层兼容滚动祖先，若所有兼容祖先均规划失败则返回首个兼容祖先的规划错误（如 `422 no_safe_trajectory` 或 `422 invalid_target(no_geometry)`）。
     - 若 `A` **轴向不兼容**：**不在此停下报错，继续沿父链向上查找更外层的兼容滚动祖先**！
     - 若遍历完父链，没有任何有效滚动祖先与请求方向轴向兼容（全部因轴向不兼容被跳过）：返回 `422 unsupported_node_action`（`reason="axis_mismatch"`, `scope="ancestor"`, `scope_ref=nearest.Ref`, `ancestor_ref=nearest.Ref`, `target_actions=tapPublicActions(target)`, `ancestor_actions=tapPublicActions(nearest)`），并附页面可执行 `candidates`。
4. **坐标 `container={"x": x, "y": y}` 解析（`resolveScrollPointPlan`）**：
   - 在收集包含 `(x, y)` 的可滚动容器集合 `H` 与最内层集合 `I` 后：
   - 当 `len(I) == 1` 时：若最内层 `I[0]` 轴向兼容且 `planScrollContainer` 成功，则选用 `I[0]`（`scroll_point_innermost`）；若 `I[0]` **轴向不兼容**或轨迹被阻，沿 `I[0]` 在 `H` 中的滚动祖先由近及远查找首个**轴向兼容且可规划**的父容器并自动回退（`scroll_point_fallback`）。若 `I[0]` 及 `H` 中所有祖先均因轴向不兼容被跳过（没有任何一个轴向兼容容器进入轨迹规划），返回 `422 unsupported_node_action`（`reason="axis_mismatch"`, `scope="point"`, `target_actions=tapPublicActions(I[0])`）并附页面可执行 `candidates`；若有兼容容器进入轨迹规划但轨迹被阻，保持返回首个兼容容器或回退链的 `422 no_safe_trajectory`。
   - 当 `len(I) >= 2`（平级竞争滚动容器）时：仅对满足 `scrollContainerAxisCompatible` 的候选运行 `planScrollOnAxes` 统计可执行者 `I_exec`；若恰有 1 个轴向兼容且可执行的容器（例如同一点上横向轮播与纵向列表交叉，请求 `down` 时只有纵向列表兼容可执行），则唯一消歧直接选用（`scroll_point_disambiguated`）！
5. **认领后实时重验（`revalidateScrollTarget`）**：
   - `plan.container` 在静态阶段已绑定为实际选定的执行容器（例如回退后的外层 `n188`）。
   - 在 [internal/engine/scroll.go:1570-1596](../../../internal/engine/scroll.go#L1570-L1596) 校验新鲜帧公开容器 `containerPublic` 时，除现有的 `isScrollContainer(containerPublic)` 与 `sameScrollScope` 外，若新鲜帧上 `containerPublic.Axis` 变为与 `plan.direction` 显式冲突（例如 `vertical && containerPublic.Axis == protocol.AxisX` 或 `!vertical && containerPublic.Axis == protocol.AxisY`；因 `sameScrollScope` 已锁定容器几何未突变，重验期仅检查 `containerPublic.Axis` 显式冲突），则按 `409 target_changed`（`reason="capability_changed"`, `"滚动容器已失去请求方向的滚动能力，本次未派发"`）拒绝并按既有规则交付新帧，绝不向已改变轴向的容器派发手势。

### 4.4 四档精细滚动幅度与联动时长（`scroll.amount`）

将 `scroll.amount` 的闭合枚举从 2 档扩充为 4 档（省略或 `""` 归一化为默认 `"page"`）：

| `amount` 枚举值 | 占安全区间可用跨度比例 (`percent`) | 最小行程下限 (`minTravel`) | 注入手势时长 (`duration_ms`) | 满足最小行程的最小容器单轴跨度 (`minExtent`) | 设计定位 |
| --- | --- | --- | --- | --- | --- |
| `"step"` | **`15%`** | **`32px`** | **`500ms`** | `266px`（`266 - 2*26 = 214`，`214*15/100 = 32px`） | 微调慢滑：抑制松手惯性（fling），用于滚出半遮挡控件、滚轮 Picker、小步位移 |
| `"half"` | **`30%`** | **`48px`** | **`450ms`** | `208px`（`208 - 48 = 160`，`160*30/100 = 48px`） | 半屏精读：卡片流、表单分段浏览 |
| `"page"`（默认） | **`60%`** | **`48px`** | **`400ms`** | `128px`（`128 - 48 = 80`，`80*60/100 = 48px`） | 常规翻页：信息流、常规列表推进 |
| `"full"` | **`85%`** | **`48px`** | **`300ms`** | `105px`（`105 - 48 = 57`，`57*85/100 = 48px`） | 大跨度快翻：长文章、长评论区快速推进 |

- **计算公式保持一致**：
  - `margin = max(24, extent / 10)`，`usable = extent - 2 * margin`；
  - `travel = int(int64(usable) * int64(percent) / 100)`；
  - 要求 `usable > 0 && travel >= minTravel(amount)`，起终点仍围绕安全区间中点 `mid = tapSafeAverage(safeStart, safeEnd)` 对称展开（`low = mid - travel/2`，`high = low + travel`）。
- **时长传递**：`dispatchScroll` 根据 `frame.amount` 通过 `scrollDurationForAmount(frame.amount)` 取得对应毫秒数（`500`/`450`/`400`/`300`）传给 `e.dev.Swipe`。`operation` 请求体中 `scroll` 分支依然**不接受** `duration_ms` 字段（`duration_ms` 仍仅属于 `swipe`），时长完全由服务端按 `amount` 档位受控决定。

---

## 5. 算法

### 5.1 公开树 `Node.Axis` 推断（`internal/observation/build.go`）

```text
function populateScrollAxes(cap *device.Capture, public []*protocol.Node, order []int):
  for each rawIdx in order:
    node = public[rawIdx]
    if node == nil || node.Context || node.Role != RoleScroll || node.Bounds == nil:
      continue
    classAxis = inferScrollAxisFromClass(cap.Nodes[rawIdx].ClassName)
    childAxis = inferScrollAxisFromChildren(node)
    if childAxis != "":
      node.Axis = childAxis
    else:
      node.Axis = classAxis
```

### 5.2 静态 `scroll` 解析与自动回退（`internal/engine/scroll.go`）

```text
function resolveScrollRefPlan(obs, view, operation, ref):
  target = findPublicNodeBounded(tree.Root, ref)
  validate existence (409), non-root/non-context/has-geometry (422), enabled (409)

  if isScrollContainer(target):
    if scrollContainerAxisCompatible(obs, view, target, operation.Direction):
      return planScrollContainer(obs, view, operation, target, "ref", scrollModeDirect, allowInner=true, lifted=false)
    // 目标容器轴向不兼容：第 1 步沿父链向兼容父滚动容器回退
    var firstCompatErr *protocol.APIError
    for parent in scrollAncestorsOf(view, target):
      if isScrollContainer(parent) && scrollContainerAxisCompatible(obs, view, parent, operation.Direction):
        plan, err := planScrollContainer(obs, view, operation, parent, "ref", scrollModeAxisFallback, allowInner=false, lifted=false)
        if err == nil:
          return plan, nil
        if firstCompatErr == nil:
          firstCompatErr = err
    // 第 2 步：尝试唯一内层兼容子滚动容器兜底（如横向 ViewPager 内嵌唯一可见纵向 RecyclerView）
    if innerPlan := tryUniqueInnerScrollFallback(obs, view, operation, target, targetClippedRect); innerPlan != nil:
      return innerPlan, nil
    // 第 3 步：若有兼容父容器进入过规划但轨迹失败，返回该规划错误；否则返回 422 axis_mismatch
    if firstCompatErr != nil:
      return nil, firstCompatErr
    return 422 unsupported_node_action(reason="axis_mismatch", scope="target", scope_ref=target.Ref,
      target_actions=tapPublicActions(target), candidates=buildStaticCandidates(ancestorAndPageScrolls))

  if target.Role == RoleScroll:
    // role=scroll 但缺 swipe
    return 422 unsupported_node_action(reason="target_missing_action", ...)

  // 非滚动节点：沿公开父链寻找滚动祖先
  ancestors = allScrollRoleAncestors(view, target)
  if len(ancestors) == 0:
    return 422 unsupported_node_action(reason="not_scrollable", ...)

  nearest = ancestors[0]
  if nearest disabled or no public geometry:
    return 422 unsupported_node_action(reason="ancestor_invalid", scope="ancestor", scope_ref=nearest.Ref, ancestor_ref=nearest.Ref, ...)
  if !hasAction(nearest, ActionSwipe):
    return 422 unsupported_node_action(reason="ancestor_missing_action", scope="ancestor", scope_ref=nearest.Ref, ancestor_ref=nearest.Ref, ...)

  var firstCompatErr *protocol.APIError
  for idx, anc in ancestors:
    if !isScrollContainer(anc) || !hasPublicGeometry(anc):
      continue
    if !scrollContainerAxisCompatible(obs, view, anc, operation.Direction):
      continue
    mode = scrollModeAncestorLift
    if idx > 0:
      mode = scrollModeAxisFallback
    plan, err := planScrollContainer(obs, view, operation, anc, "ref", mode, allowInner=false, lifted=true)
    if err == nil:
      return plan, nil
    if firstCompatErr == nil:
      firstCompatErr = err

  if firstCompatErr != nil:
    return nil, firstCompatErr
  return 422 unsupported_node_action(reason="axis_mismatch", scope="ancestor", scope_ref=nearest.Ref,
    ancestor_ref=nearest.Ref, target_actions=..., ancestor_actions=..., candidates=pageScrollCandidates)
```

### 5.3 调用时间轴（以 Issue #42 场景为例）

```text
T0  Caller 请求 act(scroll, container={ref:"n194"}, direction="down", amount="page")
T1  静态阶段（锁外，未认领 O）：
    - n194 为顶部横向 Tab 条（axis="x" 或 1080x140 扁平嵌套在 n188 内）
    - scrollContainerAxisCompatible(n194, "down") == false
    - 沿公开父链找到外层纵向列表 n188（axis="y"），兼容且 3 轴线规划出居中轨迹 (540,1896)->(540,744)
    - 生成 scrollPlan{container: raw(n188), mode: "scroll_axis_fallback", amount: "page"}
T2  取得工作位，认领 O（ready -> claimed）
T3  实时重验 revalidateScrollTarget：
    - 采样新鲜帧，匹配 n188，校验 sameScrollScope 与轴向未冲突，验证安全轨迹
T4  单次派发 dispatchScroll：
    - 注入 Swipe(540, 1896 -> 540, 744, duration=400ms)
T5  后观察收敛采样与发布：
    - 发布新观察 O_next（tree v4 delta 携带 n188 列表项整体上移的新增/移除变化）
    - structured JSON 携带 effect.state="observed"（text 模式按既有规则省略 effect）
```

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/observation/build.go` | 新增 `populateScrollAxes`、`inferScrollAxisFromClass`、`inferScrollAxisFromChildren`，在 `buildAgainst` 组装完公开子节点后为满足确定性条件的 `role=scroll` 节点设置 `node.Axis`（`AxisX` / `AxisY`）。 |
| `internal/observation/build_test.go` | 新增 `Node.Axis` 推断单测：覆盖 `HorizontalScrollView`、`ScrollView`/`NestedScrollView`/`ListView`、`ViewPager`、横向 `RecyclerView`（子项水平展开）、纵向 `RecyclerView`（子项垂直展开）、单层包装壳下子项展开、子项 `<2` 或二维网格保持 `Axis=""`。 |
| `internal/protocol/requests.go` | 新增 `ScrollAmountStep = "step"`、`ScrollAmountFull = "full"`；`parseScrollAmount` 允许 `step`、`half`、`page`、`full`（错误信息更新为 `"amount 只接受 step、half、page 或 full"`）。 |
| `internal/protocol/encode.go` | `Operation.MarshalJSONTo` 的 `"scroll"` 分支校验 `op.Amount` 支持 `step`、`half`、`page`、`full`。 |
| `internal/protocol/protocol_test.go`、`encode_test.go` | 覆盖 `step`/`half`/`page`/`full` 四档编解码及非法值拒绝。 |
| `internal/engine/act_target_shared.go` | 新增 `scrollModeAxisFallback = "scroll_axis_fallback"` 与 `staticReasonAxisMismatch = "axis_mismatch"`。 |
| `internal/engine/scroll.go` | 1. 新增 `scrollStepPercent = 15`、`scrollFullPercent = 85`、`minScrollStepTravel = 32` 及四档时长常量（`500`/`450`/`400`/`300ms`），更新 `normalizeScrollAmount`、`scrollTravelForExtent`、`scrollDurationForAmount` 与 `dispatchScroll`；<br>2. 新增 `scrollContainerAxisCompatible`（A+C 判据）；<br>3. 更新 `tryPlanScrollBase`、`resolveScrollRefPlan`、`resolveScrollPointPlan`、`tryUniqueInnerScrollFallback`、`revalidateScrollTarget`、`newScrollUnsupportedError` 支持轴向兼容检查、父级自动回退与 `axis_mismatch` 拒绝。 |
| `internal/engine/scroll_test.go`、`act_proposal_scroll_coord_test.go` | 新增 Issue #42 先红复现与回归测试：横向 Tab 条显式 ref 请求 `down` 自动回退外层纵向列表、横向 Tab 条请求 `right`/`left` 正常使用自身、无兼容父容器时返回 `422 unsupported_node_action`（`reason=axis_mismatch` + `candidates`）、非滚动子节点跳过横向父条提升到外层纵向列表、坐标点在横向条上请求 `down` 自动回退外层列表、纵向列表到底不误回退、四档 `amount`（`step`/`half`/`page`/`full`）行程与时长验证及反向对照。 |
| `internal/mcp/schema.go` | 更新 `callerGuidance`、`act` 工具描述、`container` 与 `amount` 参数 schema（`amount.enum` 改为 `["step", "half", "page", "full"]`，说明四档比例与轴向不匹配自动回退外层滚动祖先 / `axis_mismatch`）。严格遵守不出现 `quality`/`effect`/`观察质量`/`滚动效果` 等禁止词，且总发现字节低于 `baselineDiscoveryBytes`。 |
| `internal/mcp/schema_test.go` | 更新 `act` 参数与描述断言，验证四档 `amount` 与轴向回退说明，确认 `TestDiscoveryOmitsObservationInternals` 与 `TestDiscoveryByteBudget` 通过。 |
| `scripts/mcp-probe/catalog.go`、`checks.go` | `checkActScrollSchema` 增加 `amount.Enum` 精确匹配 `["step", "half", "page", "full"]` 的校验。 |
| `release/versions.json` 及生成物 | 实施发布时更新 `contract_version: 26 → 27`、`release_version: "0.30.0" → "0.31.0"`、`android_version_code: 32 → 33`，运行 `scripts/generate-version.ps1`。 |
| `docs/` 与 `README.md` | 同步 `docs/interface.md`、`docs/architecture.md`、`docs/mcp.md`、`docs/integration.md`、`docs/client.md`、`docs/release.md`、`docs/runbook.md`、`docs/control-protocol.md`、`docs/phone-operations.md`、`README.md` 中的 `axis` 投影规则、`scroll` 轴向回退与 `axis_mismatch`、四档 `amount` 与契约版本 `27`。 |

### 6.2 配置与取值

所有阈值均为服务端受控确定性常量，不新增请求配置项：
- 子项单轴展布阈值：最少有效子项数 `2`，最大采样子项数 `16`，最小主轴中心跨度 `24px`，主轴/正交轴位移比与跨度比均为 `2:1`，正交轴相邻区间重叠率 `>= 50%`；
- 嵌套正交扁平几何守卫（C）：正交边与请求轴边之比 `>= 2:1`，请求轴跨度严格小于 `max(最近非同视口滚动祖先同轴跨度, 屏幕同轴跨度)` 的 `25%`（`* 4 < refExtent`）；
- `scroll.amount` 四档：
  - `step`：`15%` 可用跨度，`minTravel = 32px`，`duration = 500ms`；
  - `half`：`30%` 可用跨度，`minTravel = 48px`，`duration = 450ms`；
  - `page`（默认）：`60%` 可用跨度，`minTravel = 48px`，`duration = 400ms`；
  - `full`：`85%` 可用跨度，`minTravel = 48px`，`duration = 300ms`。

### 6.3 日志与可观测性

- 复用现有 `scroll.plan` 事件的 `mode` 白名单字段（[scroll.go:2176-2178](../../../internal/engine/scroll.go#L2176-L2178)），新增枚举值 `"scroll_axis_fallback"`，表示因原目标或近层祖先轴向不兼容而自动回退到兼容外层滚动祖先。
- `scroll.plan` 的 `amount` 字段自然记录 `"step"` / `"half"` / `"page"` / `"full"`。
- 不新增任何日志键，不记录节点文案、坐标或树结构（符合硬约束第 10 条）。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| #1 唯一权威 / #3 单向消费 | 不变。轴向判定与父级回退全部在静态规划阶段（认领 `O` 之前）完成；静态拒绝 `422 axis_mismatch` 不认领 `O`。 |
| #4 提交条件化 / #5 状态锁短小 | 不变。`Node.Axis` 推断与 `scroll` 规划均在锁外纯内存数据结构上运行，常数有界，不持锁等待。 |
| #6 严格接入 / #7 三态与省略 | 不变。`amount` 仅接受闭合四值字符串枚举；`Node.Axis` 未知时为空串省略，在 `ReadDelta.Set` 中按既有 `Field[*Axis]` 三态规则编码。 |
| #8 事实分层（单次派发窗口） | 完全遵守。每次 `act(scroll)` 最多派发一次 `Swipe`，绝不在派发后因 `effect=not_observed` 进行二次派发。 |
| MCP 文本与工具描述面门禁 | 完全遵守。`result v4` 文本继续省略 `effect` 与 `observation.quality`；`schema.go` 工具描述与 `ServerInstructions` 绝不出现 `quality`/`effect`/`观察质量`/`滚动效果` 等禁止词。 |

### 6.5 契约与文档影响

1. `docs/interface.md`：
   - §1 / §8 / §9 / §10：契约号更新为 `27`；
   - §6：`act.scroll` 的 `amount` 说明更新为 `step`（15%，500ms，最小行程 32px）、`half`（30%，450ms）、`page`（默认，60%，400ms）、`full`（85%，300ms）；`container` 解析规则补充 A+C 轴向兼容判定、自动向兼容父滚动容器回退（`scroll_axis_fallback`）及无兼容容器时的 `422 unsupported_node_action`（`reason=axis_mismatch`）；静态 `422` 错误码表中 `unsupported_node_action` 的 `reason` 列加入 `axis_mismatch`；
   - §11 `Node`：说明 `role=scroll` 节点的 `axis` 由服务端按确定性类名与子项单轴排布投影为 `x` 或 `y`（未知或双向时省略）。
2. `docs/architecture.md`：同步观察投影的 `axis` 推断与 `scroll` 轴向兼容回退、四档幅度策略及契约号 `27`。
3. `docs/mcp.md`、`docs/integration.md`、`docs/client.md`、`docs/release.md`、`docs/runbook.md`、`docs/control-protocol.md`、`docs/phone-operations.md`、`README.md`：同步四档 `amount`、轴向回退说明与版本/契约号 `27`。
4. 所有 `docs/` 变更在提交前必须通过 [AGENTS.md](../../../AGENTS.md) 第 15 条的独立只读文档审计 subagent 审核并取得 CLEAN 结论。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

- **根治 Issue #42 及其三类同源场景**：在存在顶部横向 Tab 栏、中部横向卡片轮播与外层纵向主列表的嵌套页面上，无论调用方传了横向容器 `ref`、横向容器内的文本子节点 `ref`，还是点在横向容器上的 `{x,y}` 坐标，只要请求 `down`/`up`，都会在静态阶段自动回退到外层纵向主列表直接滚动，不再派发无效手势。
- **读树即知滚动方向**：常规 `HorizontalScrollView`、`ScrollView`、`NestedScrollView`、`ListView`、`ViewPager` 及子项已加载的单轴 `RecyclerView` 在 `tree v4` 中直接显示 `axis=x` 或 `axis=y`。
- **无兼容父容器时精准报错并给可执行候选**：若页面只有横向滚动容器而调用方请求 `down`，认领前直接报 `422 unsupported_node_action`（`reason="axis_mismatch"`），不消费 `O`；若页面旁支存在纵向滚动容器，会在 `candidates` 中以 `executable=true` 给出。
- **精细微调与长距快翻**：调用方可用 `amount="step"`（`15%`、`500ms` 慢滑）微调滚出半遮挡元素或拨动滚轮，用 `amount="full"`（`85%`、`300ms`）快速翻阅长文。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 通用 `View` / `RecyclerView` 仅含 0～1 个可见子项、类名无方向特征，且外框并非嵌套扁平条带（如近正方形 `1080×1000`）时，静态阶段仍可能允许向任意方向尝试滚动 | 既无 A 的类名/多子项排布证据，也未触发 C 的 `<25%` 扁平条带守卫，服务端不盲猜单轴，保持 `axis=""` 放行 |
| 内层纵向子列表本身与 `down` 轴向兼容，但该内层子列表已经滑到底部时，再次传该内层 ref 请求 `down` 不会自动改滚外层父页面 | 轴向兼容时不回退，且服务端不做派发后二次补滑；这是为了保证“到底即停、不误滚外层页面”的确定性边界，调用方若想滚外层应改传外层 ref |
| 应用自定义手势拦截（如内部重写 `onInterceptTouchEvent` 动态吞掉纵向滑动，或浮层透明 View 拦截触摸） | 公开无障碍树不暴露应用内部触摸分发回调与未发布透明层，`executed` 只证明受控轨迹已派发 |
| `step` 档在极矮容器（请求轴跨度 `< 266px`）上仍会报 `422 no_safe_trajectory (insufficient_travel)` | `step` 要求 `15%` 可用区间达到 `32px` 最小物理行程，低于 `32px` 的极短滑动在 Android 上极易被系统识别为点击或未跨过 `touchSlop` |

### 7.3 成本

- **CPU 与内存开销**：`populateScrollAxes` 在每次构建公开树时仅对 `role=scroll` 节点检查类名与至多前 `16` 个子项的坐标，耗时为微秒级（`< 0.05ms`），零额外设备 I/O；`scrollContainerAxisCompatible` 仅为常数级属性与父链几何比较。
- **上下文体积**：`tree v4` 中仅 `role=scroll` 且推断出明确单轴的节点增加 ` axis=x` 或 ` axis=y`（每棵树通常 1～3 个滚动容器，共约 15～25 字节），远低于节省掉的一次无效 `act(scroll)` 往返。

---

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| **先红复现 1（Issue #42 核心：显式 ref 指向横向 Tab 条请求 `down`，外层有纵向父列表）** | 不向横向 Tab 条 `n194` 派发手势；静态自动回退选中外层纵向列表 `n188`（`mode="scroll_axis_fallback"`），派发 `n188` 的纵向居中轨迹并返回 `executed` |
| **先红复现 2（显式 ref 指向横向 Tab 条请求 `right` / `left`）** | 轴向兼容，直接使用横向 Tab 条 `n194`（`mode="scroll_direct"`）派发横向轨迹 |
| **先红复现 3（显式 ref 指向横向容器请求 `down`，且父链与内层均无兼容纵向容器）** | 认领前静态拒绝 `422 unsupported_node_action`，`details.reason="axis_mismatch"`、`scope="target"`、`scope_ref=n194`；`O` 保持 `ready`、零设备采集/派发；若页面旁支有纵向滚动容器，`candidates[0]` 给出该容器且 `executable=true` |
| **先红复现 4（显式 ref 指向横向 Tab 条内的普通文本子节点请求 `down`）** | 祖先提升自动跳过轴向不兼容的 `n194`，直接提升到外层兼容纵向列表 `n188` 并成功执行；若父链只有 `n194`，返回 `422 unsupported_node_action`（`reason="axis_mismatch"`, `scope="ancestor"`, `scope_ref=n194`, `ancestor_ref=n194`） |
| **先红复现 5（坐标 `container={x,y}` 落在横向 Tab 条上请求 `down`）** | 最内层 `n194` 因轴向不兼容跳过，自动沿 `H` 回退到外层纵向列表 `n188`（`mode="scroll_point_fallback"`）；平级交叉容器按轴向兼容唯一消歧 |
| **先红复现 6（C 判据兜底：`Axis=""` 的单子项未知类名扁平横条 `1080×150` 嵌套在 `1080×2200` 纵向列表内请求 `down`）** | 被 C 判据识别为轴向不兼容，自动回退到外层纵向列表 `n188` |
| **边界守卫（纵向列表自身请求 `down` / 已到底部 / 小高度纵向 Picker `Axis="y"`）** | 纵向列表与小高度 `ScrollView`（`Axis="y"`）请求 `down` 时均保持直接规划自身，绝不误回退到外层父容器 |
| **外层横向 `ViewPager` 内嵌唯一可见纵向 `RecyclerView` 请求 `down`** | 父链无纵向容器时，通过唯一内层兼容子滚动容器兜底（`mode="scroll_inner_fallback"`）选中内层纵向 `RecyclerView` |
| **公开树 `Node.Axis` 推断单测** | `HorizontalScrollView`→`x`；`ScrollView`/`NestedScrollView`/`ListView`→`y`；横向子项 `RecyclerView`→`x`；纵向子项 `RecyclerView`→`y`；单层非滚动包装壳下子项展开正确推断；`<2` 子项通用容器保持 `""` |
| **四档 `amount`（`step`/`half`/`page`/`full`）协议、行程与时长单测** | `step`=`15%`/`500ms`/`minTravel=32px`；`half`=`30%`/`450ms`/`48px`；`page`=`60%`/`400ms`/`48px`；`full`=`85%`/`300ms`/`48px`；省略默认 `page`；非法字符串/数字/null 报 `422 invalid_request` |
| **实时重验轴向变化守卫** | 认领后新鲜帧上容器 `Axis` 变为与请求方向冲突时，返回 `409 target_changed`（`reason="capability_changed"`），零派发并交付新帧 |
| **MCP 工具说明与探针单测** | `CatalogSnapshot()` 与 `ServerInstructions()` 包含四档 `amount` 与轴向回退说明，不含 `quality`/`effect`/`观察质量`/`滚动效果` 等禁止词，总字节 `< baselineDiscoveryBytes` |
| **反向对照** | 去掉 `scrollContainerAxisCompatible` 或去掉 `populateScrollAxes` 后，上述先红复现用例 1、3、4、5、6 必须全部失败 |

### 8.2 实机/系统级

按 [AGENTS.md](../../../AGENTS.md) 第 19、24 条由主代理亲自执行：
1. **手机整组覆盖部署**：干净源码提交构建发布包，通过 `scripts/verify-manifest.ps1` 核验 `build/release/manifest.json`，执行手机整组覆盖部署（含手机端服务、APK 与手机 HTTP MCP 适配器，保留 App 私有数据）。
2. **Windows 主机二进制替换**：用同一源码提交构建并替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验 `build/release/host-manifest.json`。
3. **双入口独立探针验证**：运行 `build/mcp-probe.exe` 分别验证主机 stdio MCP 与手机 HTTP MCP，确认 `contract_version=27`、13 工具目录指纹匹配、`act_scroll_schema`（含四档 `amount`）及全部常规检查 PASS。
4. **覆盖变更行为的最小实机闭环（1080×2400 虚拟屏）**：
   - 在含顶部横向 Tab 滚动条与外层纵向主列表的真实应用页面（如哔哩哔哩或知乎首页）上，采集观察树：验证横向 Tab 容器节点已投影 `axis=x`、纵向主列表已投影 `axis=y`；
   - 显式传入横向 Tab 容器 `ref` 请求 `direction="down", amount="page"`：验证自动回退到外层纵向列表并产生真实列表内容位移（返回 `delta` 中 `changes > 0`，新旧列表项更替）；
   - 显式传入同一横向 Tab 容器 `ref` 请求 `direction="right", amount="half"` 及 `amount="step"`：验证正常在横向 Tab 条上滚动；
   - 测试 `amount="step"` 与 `amount="full"` 在纵向列表上的实际滚动幅度差异。

### 8.3 记录

验证结果写入 `maintenance/verification/scroll-axis-fallback-and-amount-tiers.md`，明确区分“已实现 / 已单测 / 已实测”以及“磁盘替换 / 独立进程验证 / 依赖方切换（由用户处理）”。实现提交并推送后，通过 `gh issue comment 42` 与 `gh issue close 42 --reason completed` 回复并关闭 Issue #42。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 轴向识别路线 | 采用 A（`observation` 构建期按类名+子项单轴走势填充 `Node.Axis`）+ C（规划期嵌套正交扁平几何守卫）结合方案，不改 `u2.jar`。 |
| 轴向不兼容时的动作 | 优先沿公开父链自动回退到最近的轴向兼容且可规划的父滚动容器直接执行（`scroll_axis_fallback`）；父链无解时尝试唯一内层兼容子容器兜底；仍无解才在认领前报 `422 unsupported_node_action`（`reason="axis_mismatch"`）并附 `candidates`。 |
| 单次派发与结果外壳 | 单次 `act(scroll)` 最多派发一次手势，不做派发后 `effect=not_observed` 的二次补滑；不新增 `Result` 顶层字段；保持 MCP `text` 模式对 `effect`/`quality` 的省略。 |
| `scroll.amount` 精细度 | 扩充为四档闭合枚举：`step`（`15%`、`500ms`、`minTravel=32px`）、`half`（`30%`、`450ms`、`48px`）、`page`（默认，`60%`、`400ms`、`48px`）、`full`（`85%`、`300ms`、`48px`）。 |
| 工具说明对接 | 同步更新 `internal/mcp/schema.go` 与 `scripts/mcp-probe/catalog.go`，严守描述面不出现 `quality`/`effect`/`观察质量`/`滚动效果` 等禁止词。 |
| 契约版本 | 推进 `contract_version: 26 → 27`（配套 `release_version: 0.30.0 → 0.31.0`，`android_version_code: 32 → 33`）。 |

### 9.2 未决

无悬空未决项。

---

## 10. 实施顺序

1. **协议与 `Node.Axis` 投影**：在 `internal/protocol` 扩充 `ScrollAmountStep`/`ScrollAmountFull` 编解码；在 `internal/observation/build.go` 实现 `populateScrollAxes`，补齐 `internal/protocol` 与 `internal/observation` 单元测试。
2. **引擎 A+C 轴向回退与四档幅度**：在 `internal/engine/act_target_shared.go` 与 `internal/engine/scroll.go` 实现四档行程/时长、`scrollContainerAxisCompatible`、父链自动回退、内层兼容过滤与 `422 axis_mismatch` 错误构造；编写先红测试并验证转绿与反向对照。
3. **MCP 工具说明与探针对接**：更新 `internal/mcp/schema.go`、`internal/mcp/schema_test.go` 与 `scripts/mcp-probe/catalog.go`，运行 `internal/mcp` 与 `scripts/mcp-probe` 测试。
4. **版本源、正式文档同步与独立文档审计**：更新 `release/versions.json` 并运行 `scripts/generate-version.ps1`；同步 `docs/` 与 `README.md`；发起**独立、只读**的文档审计 subagent 审核全部变更的 `docs/` 文件，等待并处理至取得 CLEAN 结论（AGENTS.md 第 15、17、21 条）。
5. **全量测试与实现提交**：主代理亲自运行全量 Go 单元测试（引擎测试单次超时设为 15 分钟），全部通过后创建实现提交。
6. **整组构建、实机部署、双探针与最小实机闭环**：从干净实现提交运行 `scripts/build-release.ps1`、`scripts/verify-manifest.ps1`、手机整组覆盖部署、`scripts/build-host-tools.ps1` 替换两个 Windows exe，运行主机 stdio 与手机 HTTP 独立探针及实机闭环验证。
7. **归档、推送与 Issue 闭环**：将本提案移入 `maintenance/proposals/implemented/`、重写相对链接、补齐 §11，编写 `maintenance/verification/scroll-axis-fallback-and-amount-tiers.md`，提交归档并推送到远程仓库，最后回复并关闭 Issue #42。

---

## 11. 实现结果

实现提交：`38c7b8258d30398cd5e5bfb016fb6964b091eee2`（`2026-11-23`，分支 `master`）。
实现与验证记录：[scroll-axis-fallback-and-amount-tiers.md](../../verification/scroll-axis-fallback-and-amount-tiers.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| G1 公开 `Node.Axis` 确定性投影（§4.1、§5.1） | `internal/observation/build.go`：`populateScrollAxes`、`inferScrollAxisFromClass`、`inferScrollAxisFromChildren`，在 `buildAgainst` 组装父子连接后、构造 `Snapshot` 前调用；`internal/observation/scroll_axis_test.go` |
| G2 A+C 轴向兼容判定（§4.2） | `internal/engine/scroll.go`：`scrollContainerAxisCompatible`、`nearestNonCoextensiveScrollAncestorRect` |
| G2 `ref` 直连容器的父链回退、唯一内层兜底与静态 `422 axis_mismatch`（§4.3.2、§5.2） | `internal/engine/scroll.go`：`resolveScrollRefPlan` |
| G2 非滚动 `ref` 的祖先提升跳过不兼容近层祖先（§4.3.3） | `internal/engine/scroll.go`：`scrollRoleAncestors`（替代 `nearestScrollAncestor`）、`resolveScrollRefPlan` |
| G2 坐标路径的轴向过滤、回退与唯一消歧（§4.3.4） | `internal/engine/scroll.go`：`resolveScrollPointPlan` |
| G2 `candidates.executable` 与坐标公共祖先筛选接入同一门禁 | `internal/engine/scroll.go`：`tryPlanScrollBase`；`internal/engine/act_target_shared.go`：`buildStaticCandidates` 复用 |
| G2 唯一内层兼容过滤 | `internal/engine/scroll.go`：`tryUniqueInnerScrollFallback` |
| G2 认领后实时轴向重验（§4.3.5） | `internal/engine/scroll.go`：`revalidateScrollTarget`（`409 target_changed`、`reason=capability_changed`，零派发） |
| G2 `axis_mismatch` 封闭 reason、`scroll_axis_fallback` mode 与错误文案 | `internal/engine/act_target_shared.go`：`staticReasonAxisMismatch`、`scrollModeAxisFallback`；`internal/engine/scroll.go`：`newScrollUnsupportedError` |
| G3 四档幅度、行程与联动手势时长（§4.4） | `internal/protocol/requests.go`：`ScrollAmountStep/Full`、`parseScrollAmount`；`internal/protocol/encode.go`；`internal/engine/scroll.go`：`scrollStepPercent`、`scrollFullPercent`、`minScrollStepTravel`、四档时长常量、`normalizeScrollAmount`、`scrollTravelForExtent`、`scrollDurationForAmount`、`dispatchScroll` |
| G4 MCP schema、工具说明与发现面（§6.1） | `internal/mcp/schema.go`（`amount.enum` 四档、`container`/`amount` 描述、`callerGuidance`、`act` 描述）、`internal/mcp/schema_test.go`、`internal/mcp/server_test.go`、`internal/mcp/result_parity_test.go`（目录指纹基线） |
| G4 探针四档校验 | `scripts/mcp-probe/catalog.go`：`checkActScrollSchema`（`act_amount_enum_mismatch`）；`scripts/mcp-probe/catalog_test.go` 负向用例 |
| G5 契约升级、整组发布与实机闭环 | `release/versions.json`（`0.31.0`/`27`/`33`）与生成物；`docs/*`、`README.md`；验证记录“发布与实机” |
| 测试 | `internal/engine/scroll_axis_amount_test.go`（实现者单测）、`internal/engine/scroll_axis_issue42_test.go`（独立作者端到端回归）、`internal/observation/scroll_axis_test.go`、`internal/protocol/{protocol,encode}_test.go`、`internal/mcp/schema_test.go`、`scripts/mcp-probe/catalog_test.go` |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| `ServerInstructions` 用“scroll 不接受时长参数”而非字面 `duration_ms` | `internal/mcp` 的 stdio 帧泄漏守卫把 initialize 帧中含 `duration_ms` 的文本判为诊断泄漏；字面 `duration_ms` 保留在 `amount` 参数说明与 `act` 工具描述（`tools/list`）。`TestStdioFramingCarriesOnlyMCPFrames` 与 `TestDiscoveryByteBudget` 均通过，行为无差异。 |
| 探针 `checkActScrollSchema` 用集合相等（`sameStringSet`）而非有序逐值比较 | 与既有 `direction` 校验一致；`enum` 顺序由 `internal/mcp` 的 `TestActAmountSchemaPublishesFourTiers` 钉死；集合相等在长度相同且期望值互异时等价于精确四档集合。 |
| 多最内层全部轴向不兼容时的错误 | 提案未点名；实现统一返回 `422 unsupported_node_action`（`reason=axis_mismatch`、`scope=point`），`target_actions` 取 `innermost[0]`，与 `unsupported_node_action` 必出 `target_actions` 的细节契约一致。 |
| 唯一内层兜底保留既有 `!isCoextensiveRect` 过滤 | 与目标同视口的内层容器不被委托；沿用既有语义，未按轴向放宽。 |
| 既有引擎测试的 8 处最小修正 | `full` 由“非法值”样例变为合法档（改 `wide`）；`half` 期望注入时长 400→450ms；两处竞争容器 fixture 改为显式轴/未知轴以保留原覆盖意图；`trajectory_blocked` 的三条并排 blocker 改为单条宽 blocker（否则子项排布被投影为 `AxisX` 并抢先命中 `capability_changed`）。断言意图未变。 |
| 发现面压缩既有措辞 | 加入四档与轴向回退后为守住 `baselineDiscoveryBytes=37500`（未提高），对 `callerGuidance`/`act` 描述做同义压缩；`docs/mcp.md` 的工具目录指纹随 schema 文本更新为 `3a9ad87c…b98b`。 |
| 实机闭环方向覆盖 | 实机只执行 `down`/`right`（含坐标、候选重试与 step/full 幅度对比）；`up`/`left`、多层连续跳级与 `firstCompatErr` 分支由对称逻辑单测覆盖，未做实机。 |
| 文档审计修正 | 独立只读审计发现 `docs/interface.md` 对 `candidates` 附加范围的断言过强（`root`/`context`、`409` 与越界不附候选；`no_safe_trajectory`/`ambiguous_point` 关系不同），已按最小修正收窄并复检 CLEAN。 |

---

## 附：本提案不改变的事项

- `tap`、`long_press`、`text`、`swipe`、`key` 的静态解析、实时重验与派发逻辑；
- `scroll` 的 3 轴线（`50% → 25% → 75%`）探测顺序、双向 ≥85% 同视口滚动栈免阻塞规则与安全区间居中展开公式；
- `scroll` 的 `effect` 后观察推断算法及 MCP `result v4` 文本对 `effect`/`observation.quality` 的显式省略；
- O/P 单向状态机、单次 UI 派发窗口、STOP 不等待、恢复屏障、日志白名单、Android 控制 App 业务代码与固定 `u2.jar` 制品。
