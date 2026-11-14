# 提案：受控滚动中心线安全子区间与联动视口效果推断

状态：已实现（实现提交 `badc8564af0f4196e04cb1648c35a044d2a7036c`，2026-11-15）。
实现与验证记录：[scroll-safe-trajectory-and-viewport-effect.md](../../verification/scroll-safe-trajectory-and-viewport-effect.md)。
基线提交：`85621e71e17b51da3361f8a677be900a8a46de2e`（分支 `master`，2026-11-14）。
适用范围：`act` 的 `scroll` 分支静态轨迹规划、认领后实时重验与后观察 `effect` 推断。
影响范围：[scroll.go](../../../internal/engine/scroll.go) 与相关测试、[interface.md](../../../docs/interface.md)、[architecture.md](../../../docs/architecture.md)；实现发布时涉及 [versions.json](../../../release/versions.json)、版本生成物、正式文档中的当前版本说明、发布清单与 `maintenance/verification/`。
契约（`/api/*` 字段与 `contract_version`）：请求/响应 JSON 形状及错误码集合不变；默认轨迹、静态拒绝时机、整段作用域证明及 `effect` 判据改变，属于执行语义变更。按当前基线拟将 `contract_version` 从 `12` 前进为 `13`，实施时重新核对唯一版本源并按当时的下一个契约号发布，不预占其他变更的版本号。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对，重点为滚动实现、动作入口、相关测试、正式接口与发布规范。
本文仅修订设计；没有新增实现、执行先红用例或补做实机验证，不把以下预期写成验证结论。

---

## 0. 摘要

现行滚动在认领 `O` 后才检查实际起终点的滚动作用域，静态已存在的冲突也会消耗一次重采并返回
`409 target_changed`。效果推断则因容器任一边或任一匹配单元尺寸变化而返回 `unknown`。
本提案在固定中心线上选择最长连续可证子区间，静态不可执行时认领前拒绝，并在新鲜树中证明
**原计划整段轨迹**仍可用。后观察允许容器沿请求轴伸缩，以不触及轴向视口边缘的尺寸稳定单元
同时以屏幕和容器相对坐标判断 `observed`，排除仅视口变化或整体跟随移动；`not_observed`
保留基线的可测矩形判据及裁剪盲区。字段不增加，执行语义与契约版本同步更新。

## 1. 问题

### 1.1 现象与代价

分诊为**取舍**：现行实现的端点作用域证明与保守效果判据有正式规范和测试支持，不能把它们
简单归类为实现偏离。依据为 [接口 §6](../../../docs/interface.md#L396-L425)、
[接口 §11](../../../docs/interface.md#L677-L690) 与 [发布规范](../../../docs/release.md#L45-L47)。

本次审核已通过认证的 GitHub CLI 读取 [Issue #12](https://github.com/malyjacob/mobile-use-vnext/issues/12)
和 [Issue #13](https://github.com/malyjacob/mobile-use-vnext/issues/13)，两者均声明基线为
`85621e7`、发行 `0.12.0`、契约 `12`。#12 报告 X 首页 `down/page` 连续返回
`409/not_executed`、新观察 `delta changes=0`、单次约 3–5 秒；#13 报告底边 `2242↔2400`
的两次 `down/page`、`up/half` 均 `executed + effect=unknown`。这些是可追溯的现场报告，
不是本次复现结果，也没有足够数据证明共识锚点数量、性能分布或跨应用结论。
以下将报告中的非敏感矩形转为**合成几何示例**核对，不入库原始树或截图：

- 主列表 `[0,0,1080,2242]`，同级滚动 Tab 栏 `[23,169,1058,304]`。基线 `page` 的
  `margin=224`、`travel=1076`：`up` 的起终点为 `(540,224)→(540,1300)`，起点与 Tab
  冲突；`down` 为 `(540,2018)→(540,942)`，仅此 Tab 不会导致端点冲突。若 `down` 也失败，
  需另有卡片内滚动容器等证据，不能由这组矩形推出两个方向均必败。Issue #12 将 `down`
  终点估为 `y≈224` 的具体归因与基线算法不符，须在实施复现时核对真实冲突作用域。
  `delta changes=0` 只说明公开树未变，不证明 raw 容器/祖先身份或像素画面完全相同；
  需结合现有 `container_proven/start_scope_proven/end_scope_proven` 区分容器失配与作用域冲突。
- 主容器底边从 `2242` 变为 `2400` 或反向收缩，四边守卫必然返回 `unknown`；
  这证明算法的拒绝条件，不证明该场景已经观察到足够的共同内容锚点。

仓内已有有限实机证据：[受控滚动验证记录 §4.3](../../verification/semantic-scroll-observation-quality.md#L143-L171)
记录 Settings 中 5 次 `scroll` 全部 `executed`，折叠标题场景为 `container_changed`，
边缘单元场景为 `size_changed`，未取得实机 `observed`。该记录支持效果推断存在能力缺口，
不支持“所有信息流恒为 unknown”或 X 首页的成功率、耗时结论。

### 1.2 证据链

#### 通路一：静态规划与重验阶段的作用域校验错位

1. [act.go:26–61](../../../internal/engine/act.go#L26-L61) 在锁外调用
   `staticActContentCheck`，随后取得工作位并认领 `O`；
   [act.go:297–299](../../../internal/engine/act.go#L297-L299) 将 `scroll` 转至 `resolveScrollPlan`。
2. [scroll.go:227–243](../../../internal/engine/scroll.go#L227-L243) 以目标 raw bounds 与显示屏
   交集调用 `scrollGeometry`，不检查公开树作用域。
   [scroll.go:74–139](../../../internal/engine/scroll.go#L74-L139) 只做固定比例、最小行程与矩形包含检查。
3. 认领后的 [scroll.go:335–344](../../../internal/engine/scroll.go#L335-L344) 先执行
   `ProbeDisplay` 和 `Capture`，再于 [scroll.go:390–397](../../../internal/engine/scroll.go#L390-L397)
   对新算起终点调用 `scrollScopeMatches`；失败一律为 `target_changed`。
   [act.go:74–88](../../../internal/engine/act.go#L74-L88) 尝试交付刚读取的帧。
4. [scroll_revalidate_test.go:134–204](../../../internal/engine/scroll_revalidate_test.go#L134-L204)
   的内层作用域与点选择器夹具，观察帧和重验帧相同，也断言 `target_changed`。
   同类还有 [同级覆盖层测试](../../../internal/engine/scroll_revalidate_test.go#L250-L282)。
   它们证明静态冲突会进入重验；本次未运行新行为的先红用例，其位置与期望见 §8。
5. 同类核查：基线 `scrollScopeMatches` 只被用于端点，无法证明两端之间没有其他滚动作用域。
   本提案既然按整段剔除静态冲突，实时重验也必须检查原段中部，不能沿用仅端点判断。

#### 通路二：容器四边与全部匹配单元的尺寸守卫

1. [scroll.go:548–571](../../../internal/engine/scroll.go#L548-L571) 的 `matchScrollContainer`
   以内容键与祖先内容键唯一匹配，不比较 bounds；之后
   [scroll.go:741–746](../../../internal/engine/scroll.go#L741-L746) 对公开容器四边施加 `1px` 守卫。
   容器联动伸缩超过容差即返回 `unknown/container_changed`。
2. [scroll.go:595–655](../../../internal/engine/scroll.go#L595-L655) 将公开叶单元矩形裁剪至容器，
   [scroll.go:768–774](../../../internal/engine/scroll.go#L768-L774) 对所有唯一匹配单元检查宽高。
   一个边缘单元因裁剪改变尺寸，也会否决其他尺寸稳定单元的位移证据。
3. [scroll_effect_test.go:375–408](../../../internal/engine/scroll_effect_test.go#L375-L408)
   固化了单元尺寸和容器变化返回 `unknown`；
   [共识测试](../../../internal/engine/scroll_effect_test.go#L232-L315) 则覆盖 `2` 个锚点、
   `75%` 共识、低覆盖正证据和不稳定后帧仍可 `observed`。
4. 同类核查：裁剪后尺寸相等不等于物理刚体。静止大单元被整体移动但等长的视口裁剪时，
   裁剪矩形中心会随视口移动，即使没有内容平移也可能出现同号位移。因此不能只按裁剪宽高
   不变筛选锚点，必须排除沿请求轴贴边的正证据候选；不能把任意内部变形都称作边缘裁剪。

### 1.3 为什么直觉会出错

- 只前移原端点判断：可消除无谓重采，但静态可避让的 Tab 栏也可能直接使 `scroll` 不可用。
- 静态检查整段、新鲜只查端点：若新滚动覆盖层仅落在段中部，原安全证明已经失效却仍可派发。
- 新鲜树重新挑最长区间并要求与旧区间相等：远离实际轨迹的变化可能改变最长区间，造成无谓拒绝；
  若直接派发新选区间，又改变了观察时授权的固定计划。应只重验旧段，不重新选择替代轨迹。
- 放宽所有边或删除尺寸检查：正交轴重排与中心的尺寸偏移会混入位移证据。
  中心差满足 `Δcenter = Δposition + Δsize/2`，不能把变形单元直接用于测量。
- 过滤掉所有变形单元后只看剩下的两个：内部大规模重排可能被隐藏；只有轴向边缘存在裁剪歧义的
  单元允许从正证据中排除，无法解释的内部变形仍返回 `unknown`。
- 只因 JSON 字段不变就保留契约 `12`：与发布规范对默认行为和执行语义的版本要求不符。

## 2. 目标与非目标

目标：

- 静态无法生成合法轨迹时，以 `422 unsupported_node_action` 拒绝，零设备采集、不认领 `O`。
- 正交中心坐标固定，在一维作用域区间中确定性选择最长安全子区间；认领后证明原起终点及其间
  **整段**仍在选定容器的可证作用域内，不因其他区域变化重新规划。
- 支持沿请求轴联动伸缩、轴向边缘单元裁剪场景中的保守 `observed` 正证据；无法解释的内部
  单元变形、正交轴变化仍为 `unknown`。
- `not_observed` 仍使用**全部**匹配单元，要求 `stable`、容器四边差 `<=1px`、至少两个单元、
  前后单元计数相等且 100% 唯一匹配、全部宽高差和轴向中心位移均 `<=1px`。

非目标：

- 不新增请求/响应成员，不改变 raw `swipe`、容器选择器规则、设备 `Scrollable bool` 或公开 `axis`。
- 不做二维绕行、偏移配置、自动降级为 raw `swipe`、重试或额外设备采集。
- 不放松派发前容器的完整身份与祖先链证明；容器自身或祖先身份变化仍可导致 `target_changed`。
- 不把 `observed` 解释为动作因果、期望方向、距离、消费者、业务安全或到达边界的证明。
  文中“安全”只指公开树上可证的滚动作用域与几何，不能检测未发布覆盖层或证明实际触摸路由。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 维持现状 | 静态只算几何，重验只查端点；效果维持全局守卫 | 无实现成本 | 静态冲突重复重采，联动视口与边缘裁剪缺少正证据 |
| R2 只前移端点拒绝 | 静态不可证明端点就 `422`；效果不变 | 最小改动 | 可避让的局部冲突仍不可滚动，效果缺口保留 |
| R3 中心线区间 + 固定整段重验 + 保守正证据（本提案） | 选最长一维可证区间，实时重验原段；放宽轴向视口，用内部尺寸稳定候选判断位移 | 区间算法、判据与契约升级；可能缩短手势，排除边缘锚点 | 中心线碎片化或内部锚点不足时拒绝/unknown，不能解决所有布局 |
| R4 二维搜索或公开偏移参数 | 搜索多列轨迹或让调用方给偏移 | 多候选策略或公开字段增加 | 仍不能证明实际消费者，还引入轨迹选择与兼容成本 |

选择 R3，作为待确认的实现方向。它保留服务端确定性几何和容器授权，补齐静态与新鲜证明的
对称性。R4 不纳入本提案；中心线可用率的实机数据不足以支持引入二维策略。

## 4. 判据

### 4.1 共用作用域区间与静态轨迹

设 `want` 是选定容器 raw 索引，`clipped = raw.Bounds ∩ screen`，`pub` 是其公开矩形。
容器必须继续满足基线 `isScrollContainer` 和有效 source 映射。

1. 固定正交坐标：纵向为 `x=clipped.Left+clipped.Width()/2`，横向为
   `y=clipped.Top+clipped.Height()/2`。基础轴区间取 `clipped ∩ pub` 对应轴的半开区间。
   若矩形为空或固定正交坐标不在该交集中，失败。
2. 共用纯函数 `scrollSafeSpans` 遍历整棵公开树，候选条件与基线
   [scrollScopeMatches](../../../internal/engine/scroll.go#L417-L473) 一致：`depth>0`、非 context、
   非 root、有正面积 bounds，且正交范围包含固定坐标。不能仅遍历目标子树，否则会漏同级覆盖层；
   也不能因父矩形不含中心线就跳过子树，基线对每个候选独立检查。
   DFS 必须先在**所有节点**上更新最近滚动作用域，再判断当前节点是否为几何候选；
   父 scroll 不含中心线但子 bounds 伸出时，子节点仍继承该父 scroll，而不是选定容器。
3. 对每个候选，沿公开路径找最近的 `isScrollContainer`（含自身），并校验对应 source 索引。
   无滚动祖先者忽略；作用域为 `want` 或其 raw 外层祖先者允许；内层后代、不可比作用域或最近
   滚动作用域 source 缺失/越界者，将**该候选自身 bounds**在轴上的区间记为阻塞区间。
   遇到不可用 source 不回退至更外层作用域；对 source 越界的处理比基线端点函数更保守。
   继承最近作用域可避免逐节点反复扫描祖先路径。raw 祖先集合的构造须有索引与环保护，
   无法建立合法祖先关系时保守失败，不做无界遍历。
4. 阻塞区间先裁至基础区间，去空、排序、合并（相交或相邻均合并），从基础区间减去其并集。
   所有矩形与区间采用半开边界；以目标自身公开矩形保证可用区间有选定容器作用域，
   不把“只没有冲突但未找到目标”的点判为可用。
5. 取最长安全区间 `[s,e)`；等长时 `down/right` 选坐标较大者，`up/left` 选较小者。
   用 `[s,e)` 替换 `clipped` 的滚动轴边界得到 `safeRect`，正交边界仍取 `clipped`，调用
   既有 `scrollGeometry(safeRect, screen, direction, amount)`。基础区间不再被端点 clamp。
6. 最后以 `scrollSegmentMatches` 复核：两端在屏幕、raw 裁剪矩形和公开容器中，正交坐标为
   固定中心坐标，且存在安全区间 `[s,e)` 满足 `s <= min(axis(start),axis(end))` 且
   `max(axis(start),axis(end)) < e`。这等价于闭线段完全落在一个安全半开区间中，包含终点检查。
   两端还须满足基线端点作用域谓词的语义；生产实现复用本次遍历的作用域归约结果，
   不再逐候选扫描祖先链。测试以独立的逐点判定交叉核对，非法 source 场景按本提案更严规则判断。

`page/half` 现在相对**选定安全区间**的可用长度计算，仍用 `60%/30%`、留白 `max(24,extent/10)`
和最小行程 `48px`；因此局部阻塞即使不覆盖旧端点，也可能缩短或移动新轨迹。它不承诺整屏距离。
若最长区间产生行程 `<48px` 或任何证明失败，静态返回 `422 unsupported_node_action`，文案拟为
“无法在该容器中心线上生成可证明的滚动轨迹”，避免误称已经证明设备不支持该方向。

### 4.2 静态拒绝与动态重验分流

- 静态阶段在不可变 `obs.Projection` 上运行 §4.1，认领前失败不取设备工作位、不采集、不认领 `O`。
  在没有其他操作、撤销或过期的前提下，原 `O` 仍为 `ready`；既有恢复投影不变。
  `retry/O/effective` 表示仍可使用该观察提交修正后的请求，不承诺原样重试会成功；
  不采纳 Issue #12 建议中另改静态失败恢复入口的部分，也不新增自动重试。
- 认领后仍先 `ProbeDisplay + Capture`，沿用前台校验、显示/工作归属与
  `matchFreshNode` 的完整容器身份和祖先链匹配。公开容器必须仍满足 `isScrollContainer`。
- 对新鲜树调用同一 `scrollSafeSpans`，但**不重新选择最长区间或重算起止点**。
  以 `scrollSegmentMatches` 证明原 `plan.start→plan.end` 整段可用。新增冲突覆盖起点、终点
  **或中部**均拒绝；仅在原段之外变化，即便改变了最长区间，也不单独造成拒绝。
- 上述目标/轨迹证明失败为 `409 target_changed/not_executed`。旧 `O` 已单向消费；只有
  `deliverRejection` 的发布门禁成功，才随结果交付新 `O` 与 `retry/O`，否则沿用既有恢复入口。
  不能保证每次重验失败都能交付新观察。前台、显示、采集和 STOP 错误保留原映射。

### 4.3 联动视口与尺寸稳定候选的 `effect`

`pre` 必须是派发前新鲜帧，`post` 为后观察帧，不能用旧 `O` 代替 `pre`。轴由请求方向决定。

1. **容器守卫**：沿用 `matchScrollContainer` 的唯一内容键与祖先内容键匹配，并要求前后公开
   容器有效且均为 `isScrollContainer`。纵向时左右边差均 `<=1px`，横向时上下边差均 `<=1px`；
   前后矩形须有正面积交集。否则 `unknown/container_changed`。
   允许轴向两边变化，但记录 `containerBoundsUnchanged`（四边差均 `<=1px`）用于负证据。
   不新增轴向位移量或交集比例门限；此选择只允许内容位移观察，不保证是折叠栏或滚动造成。
2. **完整匹配集**：沿用 `scrollContentUnits` 的叶单元、保护与裁剪规则及 `matchScrollUnits`
   双侧唯一键匹配。`matched` 不因边缘过滤而改变；`Anchors=len(matched)`，
   `Coverage=100*len(matched)/max(len(preUnits),len(postUnits))`，分母为零时计 `0`。
   匹配不足 `2` 个时 `unknown/insufficient_anchors`。
3. **尺寸和轴向边缘分类**：
   - 对每个匹配对检查裁剪后的宽高，`allUnitsRigid` 表示**全部**匹配对的宽高差均 `<=1px`。
     这只是既有尺寸稳定判据，不宣称物理刚体。
   - 定义 `touchesAxisEdge(rect, viewport)`：轴向低边距或高边距 `<=1px`；纵向只看
     `Top/Bottom`，横向只看 `Left/Right`。正交轴铺满容器不算轴向贴边。
   - 正交轴尺寸差 `>1px`，或轴向尺寸差 `>1px` 且前后均不贴轴向边缘，仍全局返回
     `unknown/size_changed`。内部伸缩不能借“过滤锚点”绕过守卫。
   - `eligible` 只含宽高差均 `<=1px` 且**前后均不贴轴向边缘**的匹配对。
     轴向贴边者即使尺寸恰好相等也不用于正证据，以排除等尺寸裁剪窗口中心漂移。
     贴边且仅轴向尺寸变化者可被排除，但不据此声称已证明变形由裁剪引起。
     eligible 的轴向端点须与服务做屏幕/容器裁剪前的单元一致，轴心不受这些裁剪改变；
     不满足者也排除。这里不宣称 Android 已返回的源矩形从未受系统裁剪。
4. **`observed` 的双参照位移证据**：允许轴向视口变化后，仅屏幕位移会把“内容随容器整体移动”
   算作内容推进；仅相对容器的位移又会把“只改变视口低边、内容屏幕坐标没动”算作推进。
   因此对每个 `eligible` 同时计算：
   - `absoluteDelta = post.axisCenter - pre.axisCenter`；
   - `relativeDelta = (post.axisCenter - postOrigin) - (pre.axisCenter - preOrigin)`，
     纵向 origin 取各帧容器 `Top`，横向取 `Left`。
   只有两者绝对值均 `>=2px` 时才计入移动数，按 `(sign(absoluteDelta), sign(relativeDelta))`
   分入四个桶；零移动或仅一个参照满足门限者不进桶，但仍保留在 `len(eligible)` 分母中。
   `dominant` 取最大桶计数，仅当 `len(eligible)>=2`、`dominant>=2` 且
   `dominant*100 >= len(eligible)*75` 才返回 `observed`。同一批单元必须在两个参照内分别同号；两个参照的符号之间无须相同。
   不要求 `quality=stable`、全覆盖、相同位移量或与请求方向符号一致。
   容器 origin 完全不变时，逐单元位移判断退化为原规则；因贴边过滤使分母可能从 matched
   变为 eligible，整体结果并不保证与基线相同。origin 在 `1px` 容差内的抖动也执行双门限，
   接近 `2px` 门限时可能更保守。随容器移动但相对不动、只有视口变化但绝对不动均不能触发正证据。
5. **负证据与回退**：未命中正证据后，依次检查容器四边不变、全部匹配尺寸稳定；失败分别为
   `container_changed`、`size_changed`。若 `quality=stable`、前后单元计数相同且 100% 唯一
   匹配、至少两个单元、**全部 matched** 的屏幕轴向中心差绝对值 `<=1px`，返回 `not_observed`。
   负证据保留基线的裁剪矩形与绝对中心语义，不切换到相对中心；这只能证明这些可测矩形未见位移，
   不能证明 raw 内容静止，裁剪掩盖移动的既有局限明确保留在 §7.2。
   即使内部 `eligible` 不足两个，也不能提前退出而破坏旧版合法的静止边缘场景。
   其余依次按覆盖不足、后帧不稳定、有效正证据候选不足、运动不一致返回
   `incomplete_coverage`、`unstable_post_frame`、`insufficient_anchors`、`ambiguous_motion`。
6. **预算与提交**：沿用上下文取消与 `100ms` CPU 预算，在匹配、提取、分类、统计各阶段及返回
   非 `unknown` 前检查；耗尽返回 `unknown/cpu_budget`，不追加采集。推断在状态锁外，结果仍经
   `commitScrollEffect` 的归属/撤销门禁，失败投影 `unknown`，不改写已确认的 `outcome`。

## 5. 算法

### 5.1 静态规划

```text
resolve container using existing ref/point rules
validate source index and isScrollContainer(container)
clipped = raw.Bounds ∩ screen
spans = scrollSafeSpans(projection, want, clipped, screen, direction)
best = longest(spans, tieBreak = start-side of gesture)
if no best: 422 unsupported_node_action
safeRect = replaceAxisSpan(clipped, best)
start, end, ok = scrollGeometry(safeRect, screen, direction, amount)
if !ok || !scrollSegmentMatches(projection, want, start, end, spans): 422
return immutable plan(start, end, container, direction, amount)
```

`scrollSafeSpans` 用 DFS 继承最近公开滚动作用域，raw 祖先集合只构造一次；遍历、区间排序与减法
均在状态锁外。它与段证明共用作用域归类，不复制另一套“最近祖先”规则。

### 5.2 认领后实时重验

```text
existing STOP / session / display / foreground checks; ProbeDisplay + Capture
freshContainer = matchFreshNode(fresh, observed, plan.container)
if not unique: 409 target_changed
projection = projector.Preview(fresh)  // 不推进 ref 连续号
validate fresh public scroll container and its source
spans = scrollSafeSpans(projection, freshIndex, clipped, screen, plan.direction)
startProven/endProven = endpoint scope proofs from the shared traversal
// 基线 scrollScopeMatches 语义；非法 source 按本提案更严规则处理
proven = scrollSegmentMatches(projection, freshIndex, plan.start, plan.end, spans)
logScrollRevalidate(containerProven, startProven, endProven, proven, quality)
if !proven: target_changed, try existing fresh-frame delivery
return scrollFrame(fresh, projection, freshContainer, plan.start, plan.end)
```

新鲜区间无须与旧区间相等，只需能容纳原整段。始终只派发一次原计划手势。

### 5.3 后观察效果推断

```text
result = unknown/post_observation_failed
validate pre/post; check ctx/deadline
match unique container; check public capability, orthogonal edges and positive overlap
containerBoundsUnchanged = all four edge differences <= 1
extract preUnits/postUnits; match unique keys; preserve full-set log counters
if len(matched) < 2: unknown/insufficient_anchors

eligible = []; allUnitsRigid = true; maxMoveAll = 0
for each pair in matched (with budget checks):
  sizesStable = width and height differences <= 1
  allUnitsRigid &= sizesStable
  preEdge/postEdge = touchesAxisEdge(pair bounds, respective viewport)
  if orthogonal size changed || (axis size changed && !preEdge && !postEdge):
    return unknown/size_changed
  absoluteDelta = axisCenter(post) - axisCenter(pre)
  maxMoveAll = max(maxMoveAll, abs(absoluteDelta))  // 原负证据保持绝对坐标
  if sizesStable && !preEdge && !postEdge: append eligible

buckets = four zero counters for (absoluteSign, relativeSign)
for pair in eligible (with budget checks):
  absoluteDelta = axisCenter(post) - axisCenter(pre)
  relativeDelta = (axisCenter(post)-postOrigin) - (axisCenter(pre)-preOrigin)
  if abs(absoluteDelta)>=2 && abs(relativeDelta)>=2:
    buckets[sign(absoluteDelta), sign(relativeDelta)]++
check ctx/deadline
if len(eligible)>=2 && max(buckets)>=2 && max(buckets)*100>=len(eligible)*75: observed
if !containerBoundsUnchanged: unknown/container_changed
if !allUnitsRigid: unknown/size_changed
if stable && full unique coverage && len(matched)>=2 && maxMoveAll<=1:
  check ctx/deadline; return not_observed
return unknown using ordered fallback in §4.3
```

### 5.4 时序与失败边界

```text
静态 CPU 规划 → 取得普通工作位 → claim O → 新鲜重验 → 派发门禁 → 一次 swipe
               静态拒绝不 claim                 ↓
                                      后观察发布 → 锁外 effect → 条件提交
STOP/end 可在任一点先撤权；原 O 不回 ready，迟到候选不越过既有发布/提交门禁。
```

| 情形 | outcome / 错误或 effect | O 与设备事实 |
| --- | --- | --- |
| 静态中心线无可执行区间 | `not_executed` / `422 unsupported_node_action`，无 effect | 本操作不认领、不采集、不派发；不保证与并发撤销/过期无关的永久 ready |
| 静态通过但普通工作位忙 | 既有 `device_busy`，无 effect | 不排队、不认领 |
| 新鲜容器失配或原整段任一点出现冲突 | `not_executed` / `409 target_changed`，无 effect | 旧 O 已消费；可用新帧只在交付门禁成功时发布 |
| 显示/采集/前台校验失败、派发前取消或超时 | 沿用对应现行错误；未派发为 `not_executed` | 不伪造新 O，不自动重试 |
| 派发结束回执不完整 | `uncertain` / `effect=unknown` | 关闭本次派发窗口，不重放；后观察不补判 executed |
| 已确认派发，后观察失败或 STOP | 仍为 `executed` / `effect=unknown` | 无可发布后树或按既有失败观察交付，主 outcome 不回退 |
| 后树可用，推断预算耗尽/提交门禁失效 | `executed` / `effect=unknown` | 不因推断失败改观察质量、错误或恢复入口 |
| 后树可用且满足 §4.3 | `executed` / 对应三态 effect | effect 与 observation.quality 独立 |

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| [scroll.go](../../../internal/engine/scroll.go) | 共用作用域区间、静态最长区间、原段重验；保守 eligible 分类、双参照正证据、独立负证据与预算守卫 |
| [scroll_test.go](../../../internal/engine/scroll_test.go) | 静态拒绝零 I/O/不认领；区间几何、四方向、幅度、两类选择器与边界用例 |
| [scroll_revalidate_test.go](../../../internal/engine/scroll_revalidate_test.go) | 区分静态可避让、静态不可执行、动态端点/中部遮挡；保留 P、STOP、失败交付回归 |
| [scroll_effect_test.go](../../../internal/engine/scroll_effect_test.go) | 轴向视口变化、边缘裁剪与内部变形对照，保持原负证据测试 |
| [interface.md](../../../docs/interface.md)、[architecture.md](../../../docs/architecture.md) | 明确安全区间相对幅度、整段授权、重验不替代规划、effect 判据与局限 |
| [versions.json](../../../release/versions.json) 及版本生成物 | 实施时以唯一版本源推进共同契约号，通过生成脚本同步；发行号与 Android versionCode 按发布规则处理 |
| 正式文档版本说明、构建/发布产物 | 同步当前契约说明与清单，重建同版本服务/App/适配器并核验目录指纹；不手改多处常量 |

这里只列拟实施落点。本次提案审核不修改正式文档、实现或版本源，也不预生成发布产物。

### 6.2 配置与取值

沿用现有固定常量，不新增配置或请求字段：

- `scrollPagePercent=60`、`scrollHalfPercent=30`、`scrollEdgeMarginMin=24`、
  `scrollEdgeMarginDivisor=10`、`minScrollTravel=48`、`scrollDurationMS=400`。
- `effectMinAnchors=2`、`effectMinDisplacementPx=2`、`effectConsensusPercent=75`、
  `effectNoMoveTolerancePx=1`、`effectMaxSizeDeltaPx=1`、`effectCPUBudgetMS=100`。

轴向贴边判据复用 `effectMaxSizeDeltaPx=1`，只处理已有几何容差，不引入未经验证的比例阈值。
改这些判据需修订提案并确认；不能在实现时以性能调优名义暗改。

### 6.3 日志与可观测性

复用 `scroll.plan`、`scroll.revalidate`、`scroll.effect`。`scroll.revalidate` 拟增加整体
`proven` 布尔值，该键已在 [日志白名单](../../../internal/eventlog/log.go#L80-L91) 中；
`start_scope_proven` 与 `end_scope_proven` 仍仅表示端点，不能冒充整段证明。

效果日志的 `anchors/coverage` 保持完整匹配集语义，不改成 eligible 数量；`insufficient_anchors`
也可表示足够匹配中缺少两个内部正证据候选。reason 沿用已有枚举。诊断计数/原因仅进日志，
响应仍只有 `effect.state`。不记录坐标、ref、阻塞区间、输入、树或标签正文。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威 | 规划、重验和效果均在 `internal/engine`；不增加端侧授权缓存 |
| STOP 不等待 | 不增加设备往返或锁内算法；取消后沿用撤权、派发与候选提交门禁 |
| O 单向消费 | 静态不可执行不认领；认领后无论重验结果都不归还 |
| 状态锁短小、单工作位 | 不可变投影的 CPU 遍历在锁外；不排队，不在静态规划时占设备工作位 |
| 提交条件化 | 新鲜拒绝帧、后观察与 effect 仍按各自原门禁发布/提交 |
| 事实分层 | `outcome`、`effect`、quality、会话终态与物理收尾互不改判 |

### 6.5 契约与文档影响

这是语义升级，不能以“字段不变”规避 [契约版本规则](../../../docs/release.md#L45-L47)。
基线版本源为 [release/versions.json](../../../release/versions.json) 中的 `12`；拟升级到 `13`，
实施前重核若其他变更已占用则取下一个版本。正式文档只在实现具备后同步，
所涉 `docs/` 在提交前必须由独立只读 subagent 审计并修正至 clean。

实施时至少逐项改写以下基线位置，不能只将“起终点”替换为“整段”：

- [interface.md:396](../../../docs/interface.md#L396-L398)、
  [interface.md:420](../../../docs/interface.md#L420-L425) 与
  [architecture.md:168](../../../docs/architecture.md#L168-L170)：静态阶段使用区间与几何规划；
  认领后**不重算起终点**，仅在新鲜区间上证明原固定整段。
- [interface.md:406](../../../docs/interface.md#L406-L407)、
  [interface.md:469](../../../docs/interface.md#L469-L485) 与
  [architecture.md:159](../../../docs/architecture.md#L159-L170)：同步静态作用域授权、整段证明与拒绝时机。
- [interface.md:683](../../../docs/interface.md#L683-L690) 与
  [architecture.md:501](../../../docs/architecture.md#L501-L511)：补内部 eligible、轴向贴边排除、
  正交边界、双参照位移与完整负证据，明确负证据的裁剪盲区。
- 当前版本号说明检查范围包括 [interface.md](../../../docs/interface.md)、
  [control-protocol.md](../../../docs/control-protocol.md)、[runbook.md](../../../docs/runbook.md)、
  [phone-operations.md](../../../docs/phone-operations.md)、[release.md](../../../docs/release.md)、
  [architecture.md](../../../docs/architecture.md) 与 [README.md](../../../README.md)。
  审核还发现 [README.md:10](../../../README.md#L10-L17) 的契约/发行号与工具数已落后于基线；
  实施发布时一并核对，历史验证记录中的旧版本号保留其历史含义。

版本号说明与生成物、manifest、MCP 工具目录指纹保持一致，不声称只修改两个正式文档即可发布。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 合成示例中仅有 Tab `[169,304)` 时，最长安全区间为 `[304,2242)`，长度 `1938`，
  `margin=193`、`page travel=931`。`down` 为 `(540,2049)→(540,1118)`，
  `up` 为 `(540,497)→(540,1428)`；如新鲜证明通过，均可派发。它同时改变了原本可执行的
  `down` 默认轨迹，是本提案明确接受的兼容代价。
- 静态全阻塞或分割后行程不足的场景跳过一次 `ProbeDisplay + Capture`；实际节省耗时随设备而异。
- 容器仅沿请求轴伸缩、边缘单元裁剪，仍有至少两个内部尺寸稳定候选满足 §4.3 的双参照
  `75%` 共识时，可给出 `observed`。是否在 Settings 或信息流中具备这些候选需实测。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 中心线被碎片化，侧边仍有缝隙 | 不做二维搜索，最长一维区间行程不足就静态拒绝 |
| 另一 scroll 仅覆盖段中部，Android 实际可能继续由起点容器捕获手势 | 本提案不能证明真实事件路由，仍保守检查整段，可能缩短或拒绝基线可派发的轨迹 |
| 前后内容换血、唯一匹配不足两个或只剩边缘单元 | 内部正证据不足；不能靠裁剪中心伪造内容位移 |
| 内部匹配单元自身展开、折叠或正交轴尺寸改变 | 无法解释的变形仍全局 unknown，不用少数幸存候选掩盖 |
| 联动栏只改变容器边界，没有一致内部位移 | `unknown`；容器变化时禁止推导 not_observed |
| 容器或祖先在派发前变化 | 完整身份/祖先链匹配仍会拒绝；效果规则只放宽派发后的比较 |
| 非滚动覆盖层、未发布窗口、手势拦截 | 只证明公开树中滚动作用域，不证明 z-order 或真实消费者；普通无 scroll 祖先的覆盖候选仍按基线忽略 |
| 自动动画、布局重排同时产生屏幕与容器相对位移 | 双参照可排除单纯整体跟随和单纯视口变化，仍不能证明动作因果；observed 不证明滚动成功 |
| 超视口单元的真实移动被裁剪掩盖，可见矩形始终覆盖整个容器 | 基线可能在 stable+全覆盖下返回 not_observed：裁剪尺寸/中心相同不证明 raw 内容静止。本提案不增加 rawRect 或收紧该负证据，明确保留此既有局限；若要改变，须另行确认负证据范围 |
| 边缘单元有真实位移但没有足够内部候选 | 正证据采用保守排除，可能更多 unknown；不靠放宽负证据补偿 |
| 真实相对运动恰与容器位移抵消，屏幕位移小于2px | 双参照门限保守返回 unknown；两帧不能把这种情况与仅视口变化可靠区分 |
| 相对同向运动在屏幕绝对坐标中跨过零位移、被分到不同符号桶 | 四桶共识比只看相对符号更保守，可能 unknown；本提案接受该代价，不在实现时合并桶 |

### 7.3 成本

- 设备 I/O：不新增采集；静态拒绝减少一次重验，正常后观察与收敛流程不变。
- CPU：设公开节点数为 `N`、阻塞区间数为 `B`，继承作用域的遍历 `O(N)`，排序减法
  `O(B log B)`，辅助空间 `O(N+B)`（含祖先集合/遍历栈）。不能声称是无排序的单遍线性算法。
- 效果分类为匹配集上的线性附加工作；完整推断继续使用 `100ms` 预算，耗尽为 unknown。
  本次未测得 `<1ms`、`<0.2ms` 或设备 P50/P95，不给未测性能保证。
- 日志：每次重验增加一个白名单布尔字段；不增加带正文事件或采样设备工作。

## 8. 验证计划

### 8.1 单元测试

以下为拟新增/调整用例，尚未执行。新行为用例在修改实现前先红，保留红/绿输出摘要；
不能把已有断言旧行为的绿测试当成新设计验证。既有测试只调整确实改变的期望。

| 用例组 | 可观测验收与反向对照 |
| --- | --- |
| 静态全阻塞、碎片区间与最小行程 | `422/not_executed`，probe/capture/swipe 增量全为零，非并发条件下 O 保持 ready，无 effect；安全区间长度 page 的127/128px、half 的207/208px分别产生47/48px行程，覆盖门限两侧；删除静态检查会消费 O 或发起采集 |
| Tab 与局部内层/同级覆盖 | 覆盖四方向、page/half；Tab 示例精确断言 §7.1 的新坐标；原 up 先红，原 down 作为默认轨迹改变的兼容对照 |
| 区间代数和作用域一致性 | 相交/相邻/嵌套/重复阻塞、乱序、零长、半开相切、仅1px冲突、等长平局、奇数边长、屏外裁剪；受控合成小矩形中逐整数点与端点作用域谓词交叉核对，防止只测同一实现 |
| 作用域边界 | 外层祖先允许、内层/不可比拒绝；无 scroll 祖先者忽略；最近 scope 映射缺失/越界保守阻塞；context/root/禁用条件与基线一致；子 bounds 伸出父 bounds 时仍被检查 |
| selector 不变 | ref 与唯一点选择都验证原轨迹；点上多个 scope 仍 ambiguous_point，不自动选大容器；失败零派发 |
| 动态原段重验 | 分别在 start/end/中部新增冲突，均 `409/not_executed`、零 swipe；仅中部遮挡为必需反向对照，删除整段检查必须失败 |
| 原段之外变化 | 改变最长区间但未影响原段，且容器/祖先身份不变时仍只派发原 start/end 一次；无重新规划 |
| 动态失败交付 | 可交付时新 O 与 retry/O；撤销/帧龄/树门禁拒绝时无新 O，沿用既有错误与恢复；旧 O 不归还 |
| 轴向视口伸缩 | Top/Bottom 与 Left/Right 变化、扩张和收缩均覆盖；满足双参照同桶共识为 observed；正交变化超过1px或零面积交集为 unknown/container_changed |
| 容器与内容的参照系 | 顶边349→0，单元轴区间500–700/900–1100整体减349时abs=-349、rel=0，unknown/container_changed；再叠加相对-50时observed；若内容屏幕坐标不动，则abs=0、rel=349，仍unknown。横向转置、纯容器平移、底边伸缩且Top固定均覆盖 |
| 双参照抵消与四桶 | 相对位移与容器位移抵消使abs<2时unknown；相对同向却跨绝对零点分桶、不达75%时unknown；固定origin且无贴边排除时2/2、3/4可observed，2/3为unknown |
| 后容器身份/能力 | 内容键或祖先链变化、容器匹配不唯一、后公开容器禁用/失去 scroll 能力时均 unknown/container_changed，不用内容位移越过守卫 |
| 轴向边缘裁剪 | 两个内部候选同向移动，另一个贴边轴向尺寸变化，仍 observed；仅一个内部候选时 unknown；横向转置也覆盖 |
| 等尺寸裁剪漂移 | 两个静止大单元被平移等长视口裁剪、裁剪尺寸相等且中心同向变化，仍 unknown；删除轴向贴边排除必须失败 |
| 内部变形与共识 | 两个移动幸存候选加一个内部变形单元仍 unknown/size_changed；同一(absSign,relSign)桶3/4恰达75%可observed，2/3或2:2分桶为unknown；不要求同位移量/请求符号 |
| 负证据不放宽 | stable+全覆盖+全部尺寸/位移容差可 not_observed，包括 eligible 不足两个但全部单元静止的边缘场景；容器变化、裁剪尺寸变化、固定标题残留、重复键、缺失/不稳定 quality 均不能证明未移动 |
| 保留裁剪负证据局限 | 容器0–2400，两个轴向超界单元如-100–2500/-50–2550，移动50后可见矩形仍0–2400；stable+全覆盖仍not_observed。作为明确的基线盲区对照保留，不把它当作raw静止的正确性证明 |
| 取消、预算、结果保留 | 阶段和最终预算耗尽均 unknown；STOP 后不提交迟到 effect；uncertain、后观察失败仍 unknown；status(P)/同P重放复制原 effect、只派发一次 |
| 日志边界 | 整段拒绝即便两端标志为 true，整体 proven 为 false；anchors/coverage 仍为完整匹配集；不输出坐标/树/输入正文 |

### 8.2 实机/系统级

先执行受影响路径测试，再做契约升级涉及的协议、HTTP、客户端、MCP 回归；
不为本文审核运行无关全量设备矩阵。实施时只手改唯一版本源后，先生成再检查（PowerShell）：

```powershell
./scripts/generate-version.ps1
./scripts/generate-version.ps1 -Check
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/engine/... ./internal/protocol/... ./internal/httpapi/... ./internal/client/... ./internal/mcp/...
$env:GOOS = 'linux'
$env:GOARCH = 'arm64'
$env:CGO_ENABLED = '0'
go build ./...
```

命令在独立构建 shell 执行或完成后恢复环境；不可带着 `GOOS=linux/GOARCH=arm64` 在 Windows
直接运行后续主机测试。检查 Go/Java 生成常量、Android 版本资源与 manifest 均来自版本源，
见 [版本生成脚本](../../../scripts/generate-version.ps1#L8-L17)。

正式文档审计 clean、实现提交存在后，在该**已提交干净树**重新构建并核验：

```powershell
./scripts/generate-version.ps1 -Check
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
# 若实际使用 Windows 主机适配器，还须：
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

[发布构建](../../../scripts/build-release.ps1#L1-L26) 默认包含控制 APK；不能用 `-SkipAndroid`
代替发布构建。[清单核验](../../../scripts/verify-manifest.ps1#L6-L16) 要求源码提交与干净树，
因此不能把修改中的开发树核验失败绕过或写成通过；开发交叉构建也不代替提交后的产物核验。
按清单核验共同契约、产物哈希/大小和目录指纹，覆盖安装保留数据，重启实际 MCP 宿主发现工具。

契约边界用例必须覆盖：旧 `12` 的普通请求/控制同步对新服务返回 `409 contract_mismatch`，
新共同契约号的普通工作可受理，最小 STOP 继续独立于普通契约；Android 控制 App 与实际适配器
均加载新构建，不能仅检查磁盘上的版本。若实施时选定号不是 `13`，以实际版本源替换新值。
这些是拟实施校验，不是本次审核已经执行的发布或设备测试。

实机验收至少覆盖一个联动栏页面和一个带局部滚动覆盖的页面；可用 Settings、专用合成页面或
现场报告应用，明确包/版本、显示模式、尺寸、构建与请求。每个方向/幅度样本记录实际尝试数、
`executed/target_changed/422`、`effect` 三态、可公开的匹配计数与耗时摘要，不存原始树/截图。

- 静止且存在足够安全区间：目标为零次因同一静态冲突引起的 `target_changed`；不能要求所有布局
  都必须 executed。静态不足区间则应零重验/零派发。
- 有足够内部共同候选且达到共识的后帧：应取得 `observed` 样本；不要求“肉眼滚动了就 observed”。
- 只有容器伸缩、静止内容或内部变形：不得误报 not_observed/observed；动态中部遮挡不派发的
  可控验证以合成系统夹具为主，避免依赖现场应用偶然时序。
- 耗时只比较同环境下静态拒绝所省重验，以及新规划自身 CPU，给出样本量与测量口径；不外推
  固定 3–5 秒收益或跨应用成功率。

本次提案审核未执行实机步骤；实施时若无设备，验证记录明确写“未实机执行”，不宣告实机验收通过。

### 8.3 记录

实现后的记录写入 `maintenance/verification/scroll-safe-trajectory-and-viewport-effect.md`：
分诊、实现提交、环境与命令、先红/后绿用例、实际回归和构建结果、实机样本/未执行原因、
性能摘要与正式文档审计结论。本文审核不预创建含占位验证结论的记录。

## 9. 已定与未决

### 9.1 已定（本文建议，随提案确认生效）

| 事项 | 建议结论 |
| --- | --- |
| 中心线与选择 | 固定正交中心坐标，最长连续安全区间，平局按手势起点侧优先 |
| 幅度 | 留白和60%/30%相对安全区间计算，允许改变原本可执行场景的默认坐标 |
| 静态失败 | `422 unsupported_node_action`，本操作不认领、不采集；不声称证明了真实设备轴 |
| 动态证明 | 新鲜树共用区间谓词，仅重验原整段，不选替代轨迹；端点或中部新增冲突均 target_changed |
| 效果正证据 | 正交容器边界稳定且视口相交；尺寸稳定、两帧轴向均不贴边；屏幕/容器相对位移均过门限，同一符号对桶至少2个且占eligible的75% |
| 变形与负证据 | 仅轴向贴边变形允许从正证据排除；内部/正交变形 unknown；not_observed 全部匹配集判据不变 |
| 诊断 | 完整匹配计数保留；整体轨迹使用已有白名单 proven 布尔值 |
| 版本 | 语义改变需升级共同契约；当前基线拟12→13，实施前重核唯一版本源 |

### 9.2 未决

没有留给实现阶段自行决定的算法判据。**路线与上述语义变更尚待用户确认后才能进入实现**；
本次“审核并修订提案”不视为已批准实现。实现时由执行者按最新版本源确定未占用的契约号及
发布编号，并在实现提交/验证记录中写明，不把编号协调当成放宽判据的理由。

## 10. 实施顺序

1. 确认修订提案；按当前 HEAD 重核证据和版本源。
2. 为静态不可行、Tab 向上避让、动态中部遮挡、联动视口、边缘裁剪、等尺寸裁剪伪位移和
   双参照对照编写新行为用例并先红；其余反向对照可与夹具设计并行。
3. 实现共用区间、固定原段重验与效果分类/双参照/预算，逐组转绿，保留 O/P 与 STOP 既有回归。
4. 推进唯一版本源并生成，同步实现具备的正式文档与版本说明；执行相关包测试和交叉构建。
5. 固定所涉正式文档，发起独立只读审计并等待结论；修正发现项后复检到 clean，提交实现。
6. 从已提交干净树重建完整发布产物并按 §8.2 核验 manifest、实际契约边界与宿主；具备设备时
   执行实机验收。失败须修正并重新走受影响门禁，不能用开发树产物补记成功。
7. 实现提交和真实验证结果具备后，另行移入 `implemented/`、重写相对链接、补写 §11 和验证
   记录，再提交归档；未执行实机项按流程记录原因，不把主机检查写成实机结论。

## 11. 实现结果

实现提交 `badc8564af0f4196e04cb1648c35a044d2a7036c`（2026-11-15，契约 12→13、发行
0.13.0、Android versionCode 15）。实现与验证记录：
[scroll-safe-trajectory-and-viewport-effect.md](../../verification/scroll-safe-trajectory-and-viewport-effect.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 共用中心线作用域归约（固定正交坐标、整树继承最近作用域、祖先索引与环保护、不做父矩形剪枝） | [scroll.go](../../../internal/engine/scroll.go)（`scrollScopeSpace`、`scrollSafeSpans`、`rawAncestorSet`） |
| 阻塞区间半开裁剪/去空/排序/合并/相减与最长区间平局规则 | `scroll.go`（`subtractScrollSpans`、`longestSpan`、`scrollSpan`） |
| 静态最长区间规划、幅度相对安全区间、`422` 认领前拒绝与提案文案 | `scroll.go`（`resolveScrollPlan`、`safeRect`、`provesSegment`、`unsupportedScrollTrajectory`） |
| 整段证明谓词（屏幕/裁剪/公开容器/中心线/同一安全区间） | `scroll.go`（`provesPoint`、`provesSegment`） |
| 认领后原段重验：不重选区间、不重算端点、公开容器能力复核 | `scroll.go`（`revalidateScrollTarget`） |
| 联动视口容器守卫（正交边、正面积交集、四边不变标志） | `scroll.go`（`orthogonalScrollEdgesStable`、`rectsEqualWithin`、`computeScrollEffect`） |
| 尺寸与轴向贴边分类、裁剪端点排除 | `scroll.go`（`scrollEligibleUnit`、`touchScrollAxisEdge`、`scrollContentUnit.axisClipped`） |
| 双参照位移与四桶共识 | `scroll.go`（`computeScrollEffect`、`scrollSign`） |
| 独立负证据（容器四边、全部匹配集、绝对中心）与有序回退 | `scroll.go`（`computeScrollEffect`） |
| `scroll.revalidate` 整体 `proven` 布尔值 | `scroll.go`（`logScrollRevalidate`）；键已在 `internal/eventlog` 白名单 |
| 静态拒绝零 I/O / 不认领、区间几何与四方向幅度、区间代数、逐点交叉核对、作用域边界 | [scroll_test.go](../../../internal/engine/scroll_test.go) |
| 静态可避让、动态起点/终点/中段冲突、原段外变化不重规划、端点与整段日志分离 | [scroll_revalidate_test.go](../../../internal/engine/scroll_revalidate_test.go) |
| 轴向伸缩、参照系、四桶、内部变形、后容器能力、等尺寸裁剪漂移、负证据与裁剪盲区 | [scroll_effect_test.go](../../../internal/engine/scroll_effect_test.go) |
| 契约号与发行号推进、正式文档与 README 同步 | `release/versions.json`、生成常量、[interface.md](../../../docs/interface.md)、[architecture.md](../../../docs/architecture.md)、[release.md](../../../docs/release.md)、[control-protocol.md](../../../docs/control-protocol.md)、[runbook.md](../../../docs/runbook.md)、[phone-operations.md](../../../docs/phone-operations.md)、[client.md](../../../docs/client.md)、[integration.md](../../../docs/integration.md)、[README.md](../../../README.md) |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 「等尺寸裁剪漂移」的反向对照形式 | 实现偏离：夹具中的静止超界单元在平移等长视口下容器相对位移恒为 0，双参照门限本身就会否决它，无法只靠参照对制造伪 `observed`。因此该用例在断言 `unknown` 之外，另直接断言 `scrollEligibleUnit` 对贴边单元判否，这才是「删除轴向贴边排除必然失败」的反向对照。 |
| `eligible` 的「裁剪前单元」锚点 | 实现细化：以捕获的 `RawBounds`（为空时回退 `Bounds`）作为屏幕/容器裁剪前的矩形，与裁剪后的单元比较轴端点是否被改动；提案只写「与服务做裁剪前的单元一致」。 |
| 静态几何缺失时的文案 | 实现细化：容器无可用几何、与公开矩形无交集、正交中心不在交集内仍用既有「滚动容器没有可用几何」；只有「无最长区间」与「行程不足/整段证明失败」使用提案文案「无法在该容器中心线上生成可证明的滚动轨迹」。两者同为 `422 unsupported_node_action`。 |
| 联动栏页面取得实机 `observed` | 未执行：X 的容器确实按现场报告沿轴伸缩（实测 `2242↔2400`），但 X 的虚拟化列表前后内容换血、容器身份/祖先链也随之变化，实测落在 `insufficient_anchors` 与 `container_changed`，未取得该场景的 `observed`。Settings 取得了 4 次实机 `observed`，但没有容器伸缩。 |
| 「带局部滚动覆盖」的受控合成页实机样本 | 未执行：本轮按用户指定的部署范围只装了整组发布产物与两个主机二进制，未安装验证探针 App；该场景由合成用例（同级覆盖、内层作用域、静态全阻塞 `422`）与 Zhihu/X 的真实嵌套作用域样本覆盖。 |
| 现场报告的 Tab 栏精确坐标 | 未执行实机复现：没有与报告几何一致的可用页面；§7.1 的精确坐标由合成用例断言（四方向 × page/half）。 |
| 契约边界实机用例 | 已执行：旧 `12` 的 `/api/*` 返回 `409 contract_mismatch`（`recovery=update`），缺头同样拒绝，`13` 受理，最小 STOP 在旧契约头下仍被接纳（结果见验证记录）。 |

---

## 附：本提案不改变的事项

- JSON 字段/类型/枚举与错误码集合不增加；这不代表执行语义或契约号不变。
- `scrollGeometry` 的常量与 raw `swipe` 起点身份证明、duration 规则不变。
- `not_observed` 的 stable、完整唯一匹配、容器四边、全部单元尺寸与轴向位移容差不变。
- O 单向消费、P 有界去重、单工作位、STOP 立即撤权、条件发布与事实分层不变。
