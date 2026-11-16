# 议题 3：树比较与增量差分（Delta Diff & Apply 路线 B2）

状态：已实现（代码全覆盖；实机验证有限；最后实现/发行锚点提交 `049583d328e335c56d56f153b7464ea03f1ca814`，2026-11-16）。
基线提交：`6ec25c87daefbc01c99f78d968214304641d119d`（2026-11-15）。
适用范围：compact full/delta 规划、运行时 delta 选择、内部 `Node` 与公开 `ReadNode` 两端 Apply、固定基线增量。
影响范围：`internal/observation/{diff,apply,read_diff}.go`、`internal/engine/capture.go`、delta/read 测试与基准。
契约：改变。共同 `contract_version` 由 14 升至 15；五块 delta 的结构组合与原子应用语义变化，不增加顶层字段。
最后实现/发行锚点：`049583d328e335c56d56f153b7464ea03f1ca814`。
全部实现提交：`900e640fe8343f92c4e8dd82dd3df839dde20756`、`bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`、`38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`、`049583d328e335c56d56f153b7464ea03f1ca814`。
验证记录：[实现与验证](../../../verification/observation-continuity-and-revalidation.md)、[统一基准](../../../verification/observation-continuity-benchmarks.md)。
所属总览：[README.md](README.md)。
史实边界：正文设计论证及其中“提案未实现”“当前未执行”“未来”等陈述按基线写作时点保留；源码定位、行号、常量与默认值仍以基线为准，代码前进后须重新核对。实施结论以 §11 和验证记录为准。

## 1. 问题诊断与源码归因

### 1.1 真实协议与运行时路径

本提案只使用仓库当前已经存在的五块 delta，不新增顶层字段：

- `set`：对存续节点逐字段给出完整新值；
- `add`：给出一棵新增节点的完整定义；
- `remove`：给出基线中的结构删除根；
- `children`：给出父节点最终、完整的直接子 ref 列表；
- `replace`：以地图键作为基线 ref 锚点，给出该锚点的新完整节点定义。

内部类型见 [internal/protocol/node.go:610-618](../../../../internal/protocol/node.go#L610-L618)；
公开 compact 类型见
[internal/protocol/read_tree.go:683-693](../../../../internal/protocol/read_tree.go#L683-L693)。
`add` 的最终父位置只能由父节点的 `children` 完整列表建立。根 ref 是差分作用域的锚点；
`Diff` 与 `ReadDiff` 都拒绝根 ref 变化
（[internal/observation/diff.go:57-62](../../../../internal/observation/diff.go#L57-L62)、
[internal/observation/read_diff.go:53-56](../../../../internal/observation/read_diff.go#L53-L56)）。
根 ref 变化仍直接退化为 full。

运行时差分对象是 compact `ReadTree` 与 `ReadDelta`。
`Engine.Differ` 的公共接口只接收 `ReadTree`
（[internal/engine/engine.go:296-299](../../../../internal/engine/engine.go#L296-L299)）；
`publishTreeAttempt` 调用 `ReadDiff`，失败或超过 70% 阈值时改发 compact full
（[internal/engine/capture.go:258-276](../../../../internal/engine/capture.go#L258-L276)）。
`ReadDiff` 自己负责展开 chain、规划、重新打包和往返校验
（[internal/observation/read_diff.go:9-48](../../../../internal/observation/read_diff.go#L9-L48)）。

`inspect` 不使用 delta。它从留存观察读取内部完整树，并总是投影为 detailed full：
[internal/engine/inspect.go:36-76](../../../../internal/engine/inspect.go#L36-L76)、
[internal/engine/inspect.go:98-117](../../../../internal/engine/inspect.go#L98-L117)。
本提案后续提到 Read 路径时，只指 `observe` / `act` / `wait_for` 等使用的
public compact observation。

### 1.2 两道现存硬拦截

第一，存续节点直接或间接迁入新增父容器时，规划器拒绝：

```go
if p := curParent[ref]; p != "" && baseNodes[p] == nil {
	return nil, notRepresentable("node %q now lives inside new node %q", ref, p)
}
```

见 [internal/observation/diff.go:72-85](../../../../internal/observation/diff.go#L72-L85) 与
[internal/observation/read_diff.go:65-75](../../../../internal/observation/read_diff.go#L65-L75)。

第二，一个基线节点从当前帧消失，但其旧子树内仍有存续节点时，规划器拒绝：

```go
if hasSurvivingDescendant(baseNodes[ref], kind) {
	return nil, notRepresentable("subtree %q holds surviving nodes and cannot be removed", ref)
}
```

见 [internal/observation/diff.go:101-115](../../../../internal/observation/diff.go#L101-L115) 与
[internal/observation/read_diff.go:91-103](../../../../internal/observation/read_diff.go#L91-L103)。

当前 Apply 会递归删除整个 remove 子树
（[internal/observation/apply.go:357-367](../../../../internal/observation/apply.go#L357-L367)），
因此第二道拦截保护的是现有“remove 删除整棵基线子树”的解释。B2 不能只删除守卫；
还必须把 remove 改为“删除旧结构区域中未通过最终有效图保留的残余节点”。

### 1.3 已有父间跨父移动不能被误伤

现有实现没有一揽子禁止 `oldParent != newParent` 的守卫。当新旧父都继续存在时，
两个父分别发布最终 `children` 列表即可表达跨父移动：

- 内部树的既有回归：
  [internal/observation/delta_test.go:308-337](../../../../internal/observation/delta_test.go#L308-L337)；
- compact chain 成员的既有回归：
  [internal/observation/read_test.go:335-361](../../../../internal/observation/read_test.go#L335-L361)。

因此 B2 必须把“现有父 → 现有父”纳入统一的最终拓扑模型，不得在实现时新增
“父必须相同”的检查。当前源码注释和正式文档仍把“跨父移动”概括为不可表达，
这是旧表述；本提案实施阶段要同步修正，但本分册不直接修改 `docs/`。

### 1.4 现有 Apply 的真实结构

当前 Apply 已经分别维护：

- `removeRoots` 与 `replaceRoots`；
- `definitionSet`；
- Replace 的独立展开顺序；
- Add 的独立展开顺序。

见 [internal/observation/apply.go:49-150](../../../../internal/observation/apply.go#L49-L150)；
ReadApply 同样如此，
[internal/observation/read_diff.go:312-401](../../../../internal/observation/read_diff.go)。
[internal/observation/delta_test.go:416-435](../../../../internal/observation/delta_test.go#L416-L435)
也已经覆盖 Replace 单独生效。

真正的 Apply 限制是：

- Add 展开后只把 Add 定义根登记到根级 `nodes`
  （[internal/observation/apply.go:132-150](../../../../internal/observation/apply.go#L132-L150)）；
- `definitionSet` 包含定义内所有 ref；
- 当前不允许 `children` 指向任何完整定义内节点，
  （[internal/observation/apply.go:185-193](../../../../internal/observation/apply.go#L185-L193)、
  [internal/observation/read_diff.go:431-438](../../../../internal/observation/read_diff.go)）。

所以 B2 若直接把截断定义根放进 Add，再把同一根放进 `children`，会被 Apply 拒绝。
本提案必须显式定义“可被 children 接管的顶层新增组装根”。

### 1.5 现有 Replace 不能承担跨父落位

当前 Replace 安装节点时依赖基线旧父槽位：
[internal/observation/apply.go:125-129](../../../../internal/observation/apply.go#L125-L129)。
这在未迁移的封闭区域中是正确的，但如果 B2 先摘除迁移节点，再把 `parent[ref]`
清空，Replace 就无法安装。

Replace 的区域封闭判据没有要求区域根自身仍处于基线父节点下，
见 [internal/observation/diff.go:117-165](../../../../internal/observation/diff.go#L117-L165) 与
[internal/observation/read_diff.go:105-137](../../../../internal/observation/read_diff.go#L105-L137)。
因此“区域足够大而选择 Replace，同时区域根跨父迁移”是规划器可能真实生成的组合。
B2 必须继续支持这个封闭优化，但 Replace 只替换该 ref 的节点内容；
最终父位置必须由统一 claims 与 `children` 决定，不能再依赖旧父槽位。

## 2. 最终拓扑原子解释

### 2.1 Delta 是声明，不是执行脚本

线的五个块共同声明固定基线的唯一最终树。块出现顺序只用于规范编码和可读文本；
不能把 `remove → add → children → replace` 理解为客户端逐步修改指针的脚本。

Apply 可以为了校验而在内存中先展开定义、计算 claims、计算删除闭包、再实例化树，
但任何一个声明冲突都必须在返回树之前整体拒绝。不能先删一部分、再发现错误并留下
部分结果。

### 2.2 基本术语与不变式

设：

- `B` 为基线逻辑树，`C` 为当前逻辑树；
- `B.refs` 与 `C.refs` 是各自逻辑 ref 集；
- `removed = B.refs - C.refs`；
- `added = C.refs - B.refs`；
- `survived = B.refs ∩ C.refs`；
- `baseChildren[R]` 是从基线拓扑读取的直接子 ref 列表；
- `baseParent[X]` 是从基线拓扑读取的唯一父 ref；
- `currentChildren[R]` 是当前完整树中的最终直接子 ref 列表；
- `currentParent[X]` 是当前完整树中的唯一父 ref。

所有 ref 在各自树中必须唯一。根 ref 必须相同。存续 ref 的角色不能变化。
一个节点最终最多有一个父；每个非根节点必须恰好有一个父；所有节点必须从根可达；
最终图不得含自环或更大的环。

新增节点和 Replace 定义中的节点必须作为完整节点字段出现。`set` 只修改完整定义之外
的存续节点；`children` 只建立最终父关系，不携带节点字段。

### 2.3 claims 从基线拓扑读取

Apply 的第一步是复制并索引基线拓扑，得到 `baseChildren` 和 `baseParent`。
后续所有“旧父是谁”的判断只从该基线索引读取，不使用在删除或替换过程中被改写的
父映射。

最终 child claim 的来源优先级固定为：

1. 显式 `children[P]`：完整覆盖 P 的最终子列表；
2. P 位于 Replace 完整定义内：使用定义中的 children；
3. P 位于 Add 完整定义内且没有显式覆盖：使用定义中的 children；
4. 其它存续节点：暂用 `baseChildren[P]`。

这里的第 4 项仍是基线拓扑上的一个**最终声明**，不是“先按旧槽位安装、之后再移动”。
如果存续节点的目标父与原父不同，来源父和目标父都必须由 `children` 发布最终完整列表，
从而删除来源父的旧 claim；只给目标父加 claim 会留下双父并在全图校验中失败。

显式 `children` 是唯一能把存续节点挂到新父的块。Add/Replace 定义内部的 children 只描述
封闭定义自身的结构；Replace 区域根或顶层 Add 根的**外部父位置**始终由某个父节点的
`children` claim 决定。

在删除任何节点前，必须完成以下冲突检查：

- 显式 children 的父与子 ref 都已知；
- 同一 child list 中没有重复 ref；
- 同一 child 没有被两个仍然有效的父声明为子；
- 显式覆盖不会同时保留旧父列表中的旧 claim；
- Add/Replace 定义根、定义内 ref 与基线存续 ref 不发生非法重复定义；
- 预期最终图中不存在循环。

“旧父与新父不同”本身不是冲突。只有当旧父的最终 child list 没有通过显式覆盖、
Replace 定义或结构删除被替换掉时，才形成双父冲突。

所有集合的选择、闭包遍历、删除和诊断都按 `base.Order`、当前树前序或排序后的 ref 进行；
map 只用于常数时间查询。实现不得因 Go map 的迭代顺序改变输出的 delta、首个错误或
接受的最终拓扑。

### 2.4 从最终可达图推导残余删除

`removeRoots` 取基线前序中的最高消失节点；判断所有祖先，不只判断直接父。即使某个中间
祖先迁出并继续存活，其更深处消失节点仍由最高删除区域覆盖，不重复发嵌套 remove。
Replace 根之间的重叠同样按基线位置判断。

令 `eligibleOld` 为所有 remove 基线闭包，以及各 Replace 根的基线严格后代的并集。
它只表示哪些旧节点允许成为残余，不直接判定这些节点已删除。删除根本身必须消失，
其严格后代可由最终图直接或间接保留。

先按 `explicit children > definition children > baseline children` 建立候选边图：

- 继承的基线 child list 自动去掉指向任何 remove 根的边；
- 显式 children 或完整定义指向 remove 根，一律冲突；remove 根不能自称逃逸；
- 其余候选边暂不按旧区域截断；Replace 定义照常覆盖其内部 child list；
- 从原根检查候选图的单父、无环并得到 `live`；
- 所有完整定义 ref、显式 children 的父/子、set 目标都必须在 `live`；
- 不可达节点只有在 `eligibleOld` 中、且未被完整定义重建时才可作为残余删除；
- 其余不可达节点是非法孤儿，不得静默丢弃。

于是：

```text
deleted = candidateRefs - live
require deleted ⊆ eligibleOld
require deleted ∩ completeDefinitionRefs = ∅
require removeRoots ⊆ deleted
```

例如基线 `root -> D -> [A,B]` 变为 `root -> A -> B`：`children[root]=[A]`、
`children[A]=[B]` 可让 A、B 都从根可达，虽然 B 的新父 A 仍在 D 的基线闭包内。
不能要求每个幸存者的直接新父都位于旧闭包外，也不能只保留逃逸节点的基线子树。

又如 `root -> D -> A -> R` 变为 `root -> A -> R'`：`replace[R]` 可以通过迁出的 A
继承原有 A→R 边而可达，R 不必单独获得闭包外的显式父 claim。

该求法没有“两个删除区域互相认领便可自保”的循环：没有从原根可达的显式父、显式子或
完整定义均直接拒绝。验证 final graph 后才物化结果，始终不修改基线或返回半棵树。

`remove:[D]` 单独出现可以合法表示删除整个 D 子树。消费端不知道调用方还想保留哪个
未声明节点，不能把“遗漏幸存者重挂接”一概当成 Apply 可检测的错误：若 set/定义/显式
children 声明某节点仍在但该节点不可达，则 Apply 拒绝；若没有这类声明，但规划器结果
遗漏了 current 中的幸存者，则 Diff 的最终等值校验拒绝该 delta 并回退 full。

本节改变的是结构删除与最终重挂接的共同解释，必须同步正式契约；没有新增 wire 字段。

### 2.5 新增组装边界

定义：

```text
hasSurvivor(N) =
    N 的后代中存在 B.refs 中的 ref

needsAssembly(N) =
    N ∉ B.refs
    且 hasSurvivor(N) == true
```

一个新增节点只要通向存续节点，它自身以及从首个新增祖先到该边界之间的新增祖先链
都不能把存续子树内联进自己的完整定义。

采用以下前沿规则：

- 纯新增且不含任何存续后代的封闭分支，继续内联在最近的 Add 定义中；
- 任何 `needsAssembly == true` 的新增节点都是顶层 Add 根；
- 如果新增节点 P 的父本身是组装边界，则 P 即使自己纯新增，也作为独立顶层 Add 根，
  因为 P 的父必须通过 children 组装；
- 组装边界 Add 根的字段完整，但内联 `children` 为空；
- 其最终子列表由 `children[P]` 给出；
- 顶层 Add 根的位置由父的 `children` 给出。

这与“children 不能修改完整定义内部节点”的现有 Apply 约束兼容：
children 的父只能是基线节点或顶层 Add 组装根，不寻址嵌套定义内部的随意节点。

示例：旧 `n1 -> [n15]`，新 `n1 -> [n90 -> [n15,n91]]`。

```json
{
  "add": [
    {"ref":"n90","role":"group"},
    {"ref":"n91","role":"button"}
  ],
  "children": {
    "n1": ["n90"],
    "n90": ["n15", "n91"]
  }
}
```

如果出现 `n1 -> n90 -> n91 -> [n15,n92]`，`n90` 与 `n91` 都是组装边界；
`n92` 是边界父下的独立 Add 根：

```json
{
  "add": [
    {"ref":"n90","role":"group"},
    {"ref":"n91","role":"group"},
    {"ref":"n92","role":"button"}
  ],
  "children": {
    "n1": ["n90"],
    "n90": ["n91"],
    "n91": ["n15", "n92"]
  }
}
```

把所有通向幸存节点的边界拆成顶层 Add 根，会多几个 Add 条目，但避免引入
“允许 arbitrary nested definition node 被 children 二次寻址”的新协议语义。

### 2.6 Replace 的封闭区域与迁移

保留现状的区域优化：如果一个存续 ref 的当前子树只包含基线存续后代，且这些后代
在区域内仍保持各自的基线父关系，则该区域可作为封闭 Replace 候选。

区域候选不要求区域根自身仍位于基线父下。选中后：

- `replace[R]` 携带当前 R 子树的完整定义；
- 若 R 仍在基线父下，且父列表没有其它变化，可以由基线 child claim 保留原位置；
- 若 R 迁移到现有父或新增组装父，则来源父和目标父都必须发布最终 `children`；目标列表
  认领 R，来源列表不再认领 R；
- Apply 只用 Replace 定义替换 R 的内容；R 的外部父位置由统一 claims 决定，不调用
  “在原父槽位替换”式的安装；
- 对定义内除 R 以外的基线 ref，其父关系必须与基线相同；Replace 不能借封闭优化把
  其他存续节点跨父迁移到区域内部或外部；
- 若 R 位于某个 remove 闭包内，只要通过最终有效边直接或经迁出的祖先间接从根可达，
  就可保留；不要求 R 的直接父在基线闭包外，再由 Replace 内容重建；
- 若 remove 根位于所选 Replace 的基线闭包内，则两块对同一旧区域作冲突声明，直接拒绝；
  planner 应已把这种 remove 根吸收到 Replace 闭包，不再发布冗余 `remove`。

### 2.7 Node 与 Read 的映射

`protocol.Delta` 与 `protocol.ReadDelta` 共享同一组拓扑不变量，但适配层并不“完全同构”：

- `ReadDiff` 先把 wire chain 展开为逻辑 `ReadNode`，规划完成后再打包 Add/Replace；
- `ReadNode.Bounds` 是三态几何，内部 `Node.Bounds` 可以为 `nil`；
- `ReadNode` 有 `Chain`，内部 `Node` 没有；
- `ReadDiff` 最终同时比较展开后的逻辑树与 canonical wire bytes；
- `ReadDelta` 必须保持 compact view。阶段 1 已确定公开 delta 只含
  `format/base/view` 和五块，不再携带 `defaults`；
- `enabled`、`focused`、`selected`、`checked`、`protected` 五个 bool 在三层都严格保留
  `nil / false / true`：`set` 用 `Field.Set` 区分“未修改”，字段出现后再用指针值区分
  explicit null 与 false/true。完整节点中 `enabled` 缺席为 known true，其余四个缺席为
  known false，显式 null 为 unknown；完整 `Node.checked` 与 `ReadNode.checked` 采用同一
  规则；
- `ReadDiff`、`ReadApply` 与最终逻辑等值必须直接比较三态，不能把 nil 与 false、省略与
  explicit false 归并。

基线比较器仍存在按字段默认值把 nil 折成 bool 的逻辑
（[internal/observation/tree.go:97-112](../../../../internal/observation/tree.go#L97-L112)）。
阶段 1 已要求统一三态后，B2 的 `sameOwnFields`、`diffLogicalFields`、`ReadDiff` 和最终
等值不得复用旧的 default-collapsing 比较；wire 省略只在编码/解码边界映射为契约默认，
进入 planner 的逻辑值仍必须是 strict tri-state。

因此实现应共享“分类、assembly、claims、闭包、全图校验”算法，而不是复制静态的
`byRef` 伪代码，也不能把 `inspect` 列为消费者。

## 3. 完整规划伪代码

下面用一个逻辑树适配层表达两种表示共同的规划。实际 Go 实现应分别用
`protocol.Node` 与 `protocol.ReadNode` 提供索引、字段差异和克隆函数；
Read 版本在进入以下算法前必须已经 `UnpackTree`，完成后对 Add/Replace 调用
`PackDelta`。

### 3.1 索引与分类

```go
type planNode struct {
	Ref      string
	Role     protocol.Role
	Children []string
	Own      logicalFields
}

type planIndex struct {
	Root     string
	Order    []string
	Nodes    map[string]planNode
	Children map[string][]string
	Parent   map[string]string
}

func indexPlanTree(root string, walk func(func(planNode))) planIndex

// Every decision and diagnostic below iterates Order or an explicitly sorted
// slice. Maps are lookup tables only.
```

`Order` 必须是树的前序，`Children` 必须保持 wire 中的兄弟顺序。索引阶段同时拒绝空 ref、
重复 ref、空 child 和父索引不一致；不能先索引一棵损坏图，再期望全图校验兜底。
`sameOwnFields` 与 `diffLogicalFields` 必须逐字段精确比较五个 bool 的三态；默认落盘规则
可以影响 wire 省略，不能影响 planner 是否认为值发生变化。

```go

func planStructuralDelta(base, current planIndex) (*logicalDelta, error) {
	if base.Root == "" || current.Root == "" {
		return nil, notRepresentable("tree root is missing")
	}
	if base.Root != current.Root {
		return nil, notRepresentable("root ref changed")
	}
	if err := validateUniqueRefs(base); err != nil {
		return nil, err
	}
	if err := validateUniqueRefs(current); err != nil {
		return nil, err
	}

	kind := make(map[string]nodeKind, len(base.Order))
	for _, ref := range base.Order {
		cur, ok := current.Nodes[ref]
		if !ok {
			kind[ref] = kindGone
			continue
		}
		if cur.Role != base.Nodes[ref].Role {
			return nil, notRepresentable("role of %q changed", ref)
		}
		if sameOwnFields(base.Nodes[ref], cur) &&
			sameChildren(base.Children[ref], current.Children[ref]) {
			kind[ref] = kindKeep
		} else {
			kind[ref] = kindChange
		}
	}
	return planBlocks(base, current, kind)
}
```

### 3.2 选择 Replace 区域并标记覆盖

```go
func chooseReplaceRegions(base, current planIndex,
	kind map[string]nodeKind) (
	chosen, currentCovered, baseCovered map[string]bool,
) {

	replaceable := make(map[string]bool, len(current.Order))
	currentCount := make(map[string]int, len(current.Order))
	survivorCount := make(map[string]int, len(base.Order))

	// currentCount 与区域内部 parent 关系自底向上计算。
	for i := len(current.Order) - 1; i >= 0; i-- {
		ref := current.Order[i]
		count := 1
		closed := base.Nodes[ref].Ref != ""
		for _, child := range current.Children[ref] {
			count += currentCount[child]
			if _, ok := base.Nodes[child]; !ok ||
				base.Parent[child] != ref || !replaceable[child] {
				closed = false
			}
		}
		currentCount[ref] = count
		if ref != current.Root {
			replaceable[ref] = closed
		}
	}

	// survivorCount 只数基线后代，不把区域根自己重复计算。
	for i := len(base.Order) - 1; i >= 0; i-- {
		ref := base.Order[i]
		count := 0
		for _, child := range base.Children[ref] {
			if kind[child] != kindGone {
				count++
			}
			count += survivorCount[child]
		}
		survivorCount[ref] = count
		if ref != base.Root {
			replaceable[ref] = replaceable[ref] &&
				survivorCount[ref] == currentCount[ref]-1
		}
	}

	chosen = map[string]bool{}
	var choose func(ref string, parentCovered bool)
	choose = func(ref string, parentCovered bool) {
		if parentCovered {
			return
		}
		if replaceable[ref] &&
			currentSubtreeSize(current, ref) >= replaceMinNodes &&
			estimatedReplaceSaving(current, base, ref, kind) > replaceGain {

			chosen[ref] = true
			for _, child := range current.Children[ref] {
				choose(child, true)
			}
			return
		}
		for _, child := range current.Children[ref] {
			choose(child, false)
		}
	}
	choose(current.Root, false)
	currentCovered = markCurrentReplaceCoverage(
		current.Root, chosen, current.Children)
	baseCovered = markBaselineReplaceCoverage(base, chosen)
	return chosen, currentCovered, baseCovered
}

func markCurrentReplaceCoverage(root string, chosen map[string]bool,
	children map[string][]string) map[string]bool {

	covered := map[string]bool{}
	var walk func(ref string, inside bool)
	walk = func(ref string, inside bool) {
		inside = inside || chosen[ref]
		if inside {
			covered[ref] = true
		}
		for _, child := range children[ref] {
			walk(child, inside)
		}
	}
	walk(root, false)
	return covered
}

func markBaselineReplaceCoverage(base planIndex,
	chosen map[string]bool) map[string]bool {

	covered := map[string]bool{}
	var walk func(ref string)
	walk = func(ref string) {
		covered[ref] = true
		for _, child := range base.Children[ref] {
			walk(child)
		}
	}
	for _, ref := range base.Order {
		if chosen[ref] {
			walk(ref)
		}
	}
	return covered
}
```

区域根自身的 `base.Parent[ref] != current.Parent[ref]` 不参与封闭性判断。
这样保留现有 Replace 优化，也允许区域根后续通过 claims/children 迁移。
`currentCovered` 用来阻止 Replace 定义内节点再次进入 Add/set；`baseCovered` 还覆盖当前树中
已经消失的基线后代，用来阻止这些节点被另一个 `remove` 根重复声明。

### 3.3 计算 remove 根

```go
func collectRemoveRoots(base planIndex,
	kind map[string]nodeKind,
	baseCovered map[string]bool) []string {

	var roots []string
	insideRemovedRegion := map[string]bool{}
	for _, ref := range base.Order {
		// 前序继承全部祖先的区域状态；中间祖先仍存活也不清掉覆盖。
		insideRemovedRegion[ref] = insideRemovedRegion[base.Parent[ref]]
		if kind[ref] != kindGone || ref == base.Root || baseCovered[ref] {
			continue
		}
		if insideRemovedRegion[ref] {
			// 最高消失祖先会覆盖这里；Remove 不允许嵌套根。
			continue
		}
		roots = append(roots, ref)
		insideRemovedRegion[ref] = true
	}
	return roots
}
```

这里不能因为 `containsSurvivorInBaseSubtree` 为 true 而返回不可表示。
逃逸和残余闭包由 Apply 的 claims 规则验证。

### 3.4 计算新增组装边界与顶层 Add 根

```go
func computeSurvivorFlags(base, current planIndex) map[string]bool {
	hasSurvivor := map[string]bool{}
	for i := len(current.Order) - 1; i >= 0; i-- {
		ref := current.Order[i]
		found := false
		for _, child := range current.Children[ref] {
			if _, exists := base.Nodes[child]; exists {
				found = true
			}
			if hasSurvivor[child] {
				found = true
			}
		}
		hasSurvivor[ref] = found
	}
	return hasSurvivor
}

/*
 * returns:
 *   assembly[ref]  当前新增节点是否必须成为组装边界
 *   addRoots       需要出现在 delta.add 顶层的 ref
 */
func computeAddFrontier(base, current planIndex,
	currentCovered map[string]bool) (assembly map[string]bool,
	addRoots []string) {

	hasSurvivor := computeSurvivorFlags(base, current)
	assembly = map[string]bool{}

	for _, ref := range current.Order {
		if _, exists := base.Nodes[ref]; exists || currentCovered[ref] {
			continue
		}
		if hasSurvivor[ref] {
			assembly[ref] = true
		}
	}

	for _, ref := range current.Order {
		if _, exists := base.Nodes[ref]; exists || currentCovered[ref] {
			continue
		}
		parent := current.Parent[ref]
		if parent != "" {
			_, parentIsBase := base.Nodes[parent]
			if !parentIsBase && !assembly[parent] {
				// parent 是纯新增封闭定义中的节点。该定义已在更早的
				// 顶层 Add 处完整内联，当前 ref 不再单独成为 Add 根。
				continue
			}
		}
		addRoots = append(addRoots, ref)
	}
	return assembly, addRoots
}

func cloneAddDefinition(base, current planIndex,
	ref string, assembly map[string]bool) planNode {

	out := cloneOwnFields(current.Nodes[ref])
	if assembly[ref] {
		out.Children = nil
		return out
	}

	// 纯新增封闭分支不含存续节点；完整内联其全部新增后代。
	for _, child := range current.Children[ref] {
		out.Children = append(out.Children,
			cloneAddDefinition(base, current, child, assembly))
	}
	return out
}
```

`addRoots` 包含每棵纯新增封闭分支的最高新增节点，以及组装前沿必须拆开的新增节点。
若某新增节点已被纯新增封闭父定义内联，它不会再次出现在顶层 `add` 中，避免同一 ref
被定义两次。

### 3.5 生成 children

```go
func collectChildBlocks(base, current planIndex,
	covered, assembly map[string]bool,
	addRoots []string, removeRoots []string) map[string][]string {

	needs := map[string]bool{}
	removed := setOf(removeRoots)

	// 每个顶层 Add 根必须由一个最终父列表挂接；根 ref 不会新增。
	for _, ref := range addRoots {
		parent := current.Parent[ref]
		if parent == "" {
			continue
		}
		needs[parent] = true
	}

	// 组装边界的内联 children 为空，因此必须由最终完整列表接管。
	for _, ref := range current.Order {
		if assembly[ref] {
			needs[ref] = true
		}
	}

	for _, ref := range current.Order {
		if _, exists := base.Nodes[ref]; !exists {
			continue
		}
		if covered[ref] {
			continue
		}
		if sameChildren(base.Children[ref], current.Children[ref]) {
			continue
		}

		// 只删除根时，Remove 已能断开旧父指向该根的边。
		// 但被重挂接的存续节点仍不在 removed 集合中，因此会触发 children。
		if childListChangeIsOnlyRemovedRoots(
			base.Children[ref], current.Children[ref], removed) {
			continue
		}
		needs[ref] = true
	}

	out := map[string][]string{}
	for _, ref := range current.Order {
		if !needs[ref] {
			continue
		}
		list := append([]string(nil), current.Children[ref]...)
		if list == nil {
			list = []string{}
		}
		out[ref] = list
	}
	return out
}
```

`children[P] = []` 必须编码成存在且为空的覆盖，不能被当作“未修改”。

### 3.6 生成 set、Add、Remove 与 Replace

```go
func planBlocks(base, current planIndex,
	kind map[string]nodeKind) (*logicalDelta, error) {

	chosen, covered, baseCovered :=
		chooseReplaceRegions(base, current, kind)
	removeRoots := collectRemoveRoots(base, kind, baseCovered)
	assembly, addRoots := computeAddFrontier(base, current, covered)
	childBlocks := collectChildBlocks(
		base, current, covered, assembly, addRoots, removeRoots)

	delta := &logicalDelta{}
	delta.Remove = append(delta.Remove, removeRoots...)

	for _, ref := range addRoots {
		delta.Add = append(delta.Add,
			cloneAddDefinition(base, current, ref, assembly))
	}
	delta.Children = childBlocks

	for _, ref := range base.Order {
		if kind[ref] == kindGone || covered[ref] {
			continue
		}
		change, changed := diffLogicalFields(
			base.Nodes[ref], current.Nodes[ref])
		if changed {
			delta.Set[ref] = change
		}
	}

	for _, ref := range current.Order {
		if chosen[ref] {
			delta.Replace[ref] =
				cloneCurrentSubtree(current, ref)
		}
	}

	if err := validatePlannedRefOwnership(
		base, current, chosen, covered, baseCovered,
		assembly, addRoots, childBlocks); err != nil {
		return nil, err
	}
	return delta, nil
}
```

`validatePlannedRefOwnership` 静态检查：

- 每个新增 ref 恰好由一个顶层 Add 定义或 Replace 定义引入；
- 封闭新增分支中没有基线 ref 被错误内联；
- 每个顶层 Add 根有最终父 claim；
- 存续节点跨父迁移时，来源父不再认领、目标父认领该节点；Replace 区域根迁移也遵守同一
  claims，且互不依赖 map 遍历顺序。

规划完成后，内部 `Diff` 调用 `Apply` 并比较 `Equal`；ReadDiff 对打包后的
ReadDelta 调用 `ReadApply`，比较逻辑树和 canonical JSON bytes。

## 4. 完整 Apply 伪代码

下面是内部树和 Read 树共用的申请/校验模型。具体字段复制、几何三态和 chain
打包由适配函数处理。

```go
type expandedDefinitions struct {
	AddOrder      []string
	ReplaceOrder  []string
	Nodes         map[string]logicalOwn
	Children      map[string][]string
	Owner         map[string]string
	RegionOf      map[string]string
	AssemblyRoots map[string]bool
}

func expandDefinitions(base planIndex, delta logicalDelta,
	explicitChildren map[string][]string) (expandedDefinitions, error) {

	out := expandedDefinitions{
		Nodes:         map[string]logicalOwn{},
		Children:      map[string][]string{},
		Owner:         map[string]string{},
		RegionOf:      map[string]string{},
		AssemblyRoots: map[string]bool{},
	}

	claim := func(def *logicalNode, owner, regionRoot string) error {
		if def == nil || def.Ref == "" {
			return conflict("definition has no ref")
		}
		if _, duplicate := out.Owner[def.Ref]; duplicate {
			return conflict("duplicate definition", def.Ref)
		}
		out.Owner[def.Ref] = owner
		if owner == "replace" {
			out.RegionOf[def.Ref] = regionRoot
		}
		out.Nodes[def.Ref] = cloneLogicalOwn(def)
		out.Children[def.Ref] = childRefs(def.Children)
		return nil
	}

	var visitAdd func(def *logicalNode, topLevel bool) error
	visitAdd = func(def *logicalNode, topLevel bool) error {
		if err := claim(def, "add", ""); err != nil {
			return err
		}
		if _, exists := base.Nodes[def.Ref]; exists {
			return conflict("add ref already exists in baseline", def.Ref)
		}
		// AddOrder 是全部 Add 定义的确定性前序，不只记录顶层根。
		out.AddOrder = append(out.AddOrder, def.Ref)
		_, hasOverride := explicitChildren[def.Ref]
		if topLevel && len(def.Children) == 0 && hasOverride {
			out.AssemblyRoots[def.Ref] = true
		} else if hasOverride {
			return conflict(
				"children can only override an empty top-level add root",
				def.Ref)
		}
		for _, child := range def.Children {
			if child == nil {
				return conflict("nil nested add definition", def.Ref)
			}
			if err := visitAdd(child, false); err != nil {
				return err
			}
		}
		return nil
	}

	var visitReplace func(regionRoot string, def *logicalNode,
		parentInDefinition string, topLevel bool) error
	visitReplace = func(regionRoot string, def *logicalNode,
		parentInDefinition string, topLevel bool) error {

		if err := claim(def, "replace", regionRoot); err != nil {
			return err
		}
		if _, exists := explicitChildren[def.Ref]; exists {
			return conflict("children cannot override replace definition", def.Ref)
		}
		if baseNode, survives := base.Nodes[def.Ref]; survives {
			if def.Role != baseNode.Role {
				return conflict("replace changes surviving role", def.Ref)
			}
			if topLevel {
				if def.Ref != regionRoot {
					return conflict("replace key and definition ref differ", def.Ref)
				}
			} else {
				if !isDescendantOrSelf(
					def.Ref, regionRoot, base.Parent) {
					return conflict(
						"replace definition crosses its baseline region", def.Ref)
				}
				if base.Parent[def.Ref] != parentInDefinition {
					return conflict(
						"replace definition changes surviving parent", def.Ref)
				}
			}
		}
		if topLevel {
			out.ReplaceOrder = append(out.ReplaceOrder, def.Ref)
		}
		for _, child := range def.Children {
			if child == nil {
				return conflict("nil nested replace definition", def.Ref)
			}
			if err := visitReplace(
				regionRoot, child, def.Ref, false); err != nil {
				return err
			}
		}
		return nil
	}

	for _, def := range delta.Add {
		if err := visitAdd(def, true); err != nil {
			return expandedDefinitions{}, err
		}
	}
	for _, root := range sortedKeys(delta.Replace) {
		def := delta.Replace[root]
		if def == nil {
			return expandedDefinitions{}, conflict(
				"replace has no definition", root)
		}
		if _, exists := base.Nodes[root]; !exists {
			return expandedDefinitions{}, conflict(
				"replace root is not in baseline", root)
		}
		if err := visitReplace(root, def, "", true); err != nil {
			return expandedDefinitions{}, err
		}
	}
	return out, nil
}

func applyStructuralDelta(base logicalTree, delta logicalDelta) (logicalTree, error) {
	baseIndex := indexLogicalTree(base)

	// Step 1: 先校验块形状和区域关系。此阶段还没有篡改任何 child list。
	removeRoots, err := validateRemoveRoots(baseIndex, delta.Remove)
	if err != nil {
		return logicalTree{}, err
	}
	replaceRoots := sortedKeys(delta.Replace)
	if err := validateReplaceRoots(baseIndex, replaceRoots); err != nil {
		return logicalTree{}, err
	}
	if err := rejectNestedRegions(
		removeRoots, baseIndex.Parent, "remove"); err != nil {
		return logicalTree{}, err
	}
	if err := rejectNestedRegions(
		replaceRoots, baseIndex.Parent, "replace"); err != nil {
		return logicalTree{}, err
	}

	// Step 2: 展开完整的 Add/Replace 定义。展开同时拒绝跨定义重复 ref、
	// 跨 Replace 基线边界、定义内部改变存续父关系和 surviving role 变化。
	defs, err := expandDefinitions(
		baseIndex, delta, delta.Children)
	if err != nil {
		return logicalTree{}, err
	}

	// Step 3: 内容表仍是基线的克隆；完整定义覆盖其中的内容。这里不删除节点，
	// 也不通过旧父槽位安装任何东西。
	nodes := cloneLogicalOwn(baseIndex.Nodes)
	for _, ref := range defs.AddOrder {
		nodes[ref] = defs.Nodes[ref]
	}
	for _, ref := range defs.ReplaceOrder {
		// ReplaceOrder 递归遍历完整定义；适配器可在展开时保留扁平顺序。
		for _, definedRef := range definitionPreorder(ref, defs.Children) {
			nodes[definedRef] = defs.Nodes[definedRef]
		}
	}

	// Step 4: children 是显式最终声明。它可覆盖未被完整定义拥有的基线父，
	// 以及顶层 Add 的空组装根；其他完整定义节点不得被二次寻址。
	explicitParents := map[string]string{}
	for _, parent := range sortedKeys(delta.Children) {
		list := delta.Children[parent]
		if _, exists := nodes[parent]; !exists {
			return logicalTree{}, conflict("children parent unknown", parent)
		}
		_, baselineParent := baseIndex.Nodes[parent]
		overridableBaseline :=
			baselineParent && defs.Owner[parent] == ""
		if !defs.AssemblyRoots[parent] && !overridableBaseline {
			return logicalTree{}, conflict(
				"children cannot override this parent", parent)
		}
		seen := map[string]bool{}
		for _, child := range list {
			if _, exists := nodes[child]; !exists {
				return logicalTree{}, conflict("children child unknown", child)
			}
			if seen[child] {
				return logicalTree{}, conflict("duplicate child", child)
			}
			seen[child] = true
			if prev, claimed := explicitParents[child];
				claimed && prev != parent {
				return logicalTree{}, conflict(
					"child has two explicit parents", child)
			}
			explicitParents[child] = parent
		}
	}

	// Step 4.5: remove 与 Replace 可以在基线位置包含迁出的幸存根，
	// 但不能让同一 ref 同时作 remove/replace，也不允许在 Replace 旧区域
	// 另发 remove 根。这里的基线区域检查不推断“直接父必须在闭包外”。
	if err := validateStructuralRegions(
		baseIndex, removeRoots, replaceRoots); err != nil {
		return logicalTree{}, err
	}
	removeSet := refSet(removeRoots)
	eligibleOld := baselineDeletionEligibility(
		baseIndex, removeRoots, replaceRoots)

	// Step 5: 建立候选最终边。只有继承基线边可自动去掉 remove 根；
	// 显式或完整定义指向 remove 根必须拒绝，不得静默让 remove 失效。
	finalChildren := map[string][]string{}
	for _, parent := range sortedKeys(nodes) {
		var list []string
		fromBaseline := false
		switch {
		case hasKey(delta.Children, parent):
			list = delta.Children[parent]
		case hasKey(defs.Children, parent):
			list = defs.Children[parent]
		default:
			list = baseIndex.Children[parent]
			fromBaseline = true
		}
		seen := map[string]bool{}
		for _, child := range list {
			if removeSet[child] {
				if fromBaseline { continue }
				return logicalTree{}, conflict(
					"explicit or definition edge targets remove root", child)
			}
			if _, exists := nodes[child]; !exists || child == parent || seen[child] {
				return logicalTree{}, conflict("invalid child declaration", child)
			}
			seen[child] = true
			finalChildren[parent] = append(finalChildren[parent], child)
		}
	}

	// Step 6: 只从原根推导 live；不得让闭包内互相认领的节点自己证明存续。
	// 沿根可达边检查单父和 DFS 颜色。未从根可达的隐式基线残余不贡献父权。
	live := map[string]bool{}
	color := map[string]uint8{} // 1 gray, 2 black
	parentClaim := map[string]string{}
	var visit func(string) error
	visit = func(ref string) error {
		if color[ref] == 1 { return conflict("cycle", ref) }
		if color[ref] == 2 { return conflict("ref reached twice", ref) }
		if removeSet[ref] { return conflict("remove root remains reachable", ref) }
		if _, exists := nodes[ref]; !exists { return conflict("missing node", ref) }
		color[ref], live[ref] = 1, true
		for _, child := range finalChildren[ref] {
			if child == baseIndex.Root { return conflict("root has a parent", child) }
			if _, exists := parentClaim[child]; exists {
				return conflict("child has two live parents", child)
			}
			parentClaim[child] = ref
			if err := visit(child); err != nil { return err }
		}
		color[ref] = 2
		return nil
	}
	if err := visit(baseIndex.Root); err != nil { return logicalTree{}, err }

	// Step 7: 所有显式声明都必须实际落在 live 中。不可达的显式环、
	// 被删父的空 children、孤立 Add 和替换定义均不能被忽略。
	for _, ref := range sortedKeys(defs.Nodes) {
		if !live[ref] { return logicalTree{}, conflict("definition is unreachable", ref) }
	}
	for _, parent := range sortedKeys(delta.Children) {
		if !live[parent] { return logicalTree{}, conflict("children parent is deleted", parent) }
		for _, child := range delta.Children[parent] {
			if !live[child] { return logicalTree{}, conflict("children child is deleted", child) }
		}
	}
	for _, ref := range sortedKeys(delta.Set) {
		if !live[ref] { return logicalTree{}, conflict("set ref does not survive", ref) }
		if _, baseline := baseIndex.Nodes[ref]; !baseline || defs.Owner[ref] != "" {
			return logicalTree{}, conflict("set must target an undefined baseline survivor", ref)
		}
		if ref == baseIndex.Root && setTouchesInteraction(delta.Set[ref]) {
			return logicalTree{}, conflict("set cannot add interaction fields to root", ref)
		}
	}

	// Step 8: 只有旧结构区域中未从根可达、未被定义重建的旧节点可成为残余。
	// 其他孤儿即使没有显式声明，也必须拒绝。
	for _, ref := range sortedKeys(nodes) {
		if live[ref] { continue }
		if !eligibleOld[ref] || defs.Owner[ref] != "" {
			return logicalTree{}, conflict("unreachable node outside deletion residue", ref)
		}
		delete(nodes, ref)
		delete(finalChildren, ref)
	}

	// Step 9: 拓扑已整体合法后才落地字段 set，再物化一棵新树。
	for _, ref := range sortedKeys(delta.Set) {
		applyLogicalSet(nodes[ref], delta.Set[ref])
	}
	if err := validateSurvivingRoles(baseIndex, nodes); err != nil {
		return logicalTree{}, err
	}
	if nodes[baseIndex.Root].Role != protocol.RoleRoot {
		return logicalTree{}, conflict(
			"result root role changed", baseIndex.Root)
	}

	var materialize func(ref string) (logicalNode, error)
	materialize = func(ref string) (logicalNode, error) {
		node := nodes[ref]
		next := make([]logicalNode, 0, len(finalChildren[ref]))
		for _, child := range finalChildren[ref] {
			childNode, err := materialize(child)
			if err != nil {
				return logicalNode{}, err
			}
			next = append(next, childNode)
		}
		node.Children = next
		return node, nil
	}
	root, err := materialize(baseIndex.Root)
	if err != nil {
		return logicalTree{}, err
	}
	return logicalTree{
		Format: base.Format,
		Scope:  base.Scope,
		Root:   root,
	}, nil
}
```

`expandDefinitions` 的扁平 `Nodes/Children` 只是 Apply 内部的不可变展开结果，不是新增
delta 块。顶层 Add 根只有“定义自身 children 为空且存在对应显式 children 键”时才是
`assemblyRoot`；其他 Add/Replace 内节点始终 closed。Replace 定义中除区域根外的基线 ref
必须仍在本 region 且保持基线父关系，因此封闭优化不会偷带跨边界迁移。

`definitionPreorder`、`sortedKeys`、`baselineClosure`、`withoutRef` 和 `hasKey` 都以树前序、
兄弟列表顺序或排序后的 ref 工作。Add 定义仍按 wire 顺序发布；Replace map 按排序 key
处理。固定顺序只用于输出和诊断，不表示五块之间存在脚本式先后依赖。

### 4.1 基线区域检查与删除资格

```go
func validateStructuralRegions(base planIndex, removeRoots, replaceRoots []string) error {
    for _, removeRoot := range removeRoots {
        for _, replaceRoot := range replaceRoots {
            if isDescendantOrSelf(removeRoot, replaceRoot, base.Parent) {
                return conflict("remove lies inside or equals replace region", removeRoot)
            }
        }
    }
    // Replace 在 remove 的旧闭包内本身不是冲突；最终 root reachability 决定其存续。
    return nil
}

func baselineDeletionEligibility(base planIndex, removes, replaces []string) map[string]bool {
    eligible := map[string]bool{}
    for _, root := range removes {
        order, _ := baselineClosure(root, base.Children)
        for _, ref := range order { eligible[ref] = true }
    }
    for _, root := range replaces {
        order, _ := baselineClosure(root, base.Children)
        for _, ref := range order {
            if ref != root { eligible[ref] = true }
        }
    }
    return eligible
}
```

区域索引只读基线。remove 根不能为原根，Replace 根必须已存在，remove/Replace 各自的根
不能嵌套，沿用入口校验。基线位置被 remove 包住的 Replace 根可经逃逸祖先间接可达，
不要求 `explicitParent[replaceRoot]` 必须存在。

### 4.2 全图可达与声明完整性

最终有效图的 root traversal 同时给出 live 和单父/无环证明。不可达的候选图部分不能被
当作“另一个有效根”：所有显式声明和完整定义必须可达；只有没有这类声明的基线删除
残余可以不遍历、不物化。若某个 old source parent 仍从根可达、且遗漏了移除旧 child 的
children 覆盖，它与目标父对同一 child 的两个 live claim 仍会拒绝。

该解释同时涵盖闭包内部的兄弟重新嵌套、逃逸祖先中的 Replace、旧区域之间迁移及多层
新增组装边界，而不依赖 `topmostEscapes` 或把基线子树不变当作幸存前提。

### 4.3 Add/Replace definition 的可覆盖边界

Apply 必须把定义内节点分成两类：

- `closedNode`：完整定义已经闭合，字段和 children 都由定义给出；
- `assemblyRoot`：顶层 Add 根的字段完整，但 children 被显式留给 `children`。

`children` 可以覆盖两类父：第一，未被任何 Add/Replace 完整定义拥有的基线存续父，
用来表达既有父间移动和存续节点重挂接；第二，`assemblyRoot`。线格式没有专门标出
“这是截断的 assemblyRoot”，因此 Apply 采用可验证形状：Add 定义根位于 `delta.add`
顶层、其内联 children 为空，且存在显式 `children[root]`。满足该形状时，显式列表接管
该根 children；如果 Add 根已有内联 children，又与 `children` 同时声明，则拒绝。
嵌套 Add 节点与 Replace 定义内节点始终是 `closedNode`，即使其 children 为空也不能
被覆盖。

如果 `children` key 指向普通嵌套 `closedNode` 或 Replace 定义内节点，仍返回不可表示。
这样既解除 B2 的核心限制，又避免把 children 变成 arbitrary pointer mutator。它不禁止
旧父与新父都是基线节点时的跨父移动；相反，来源父和目标父都必须发布各自最终列表。

`replace` 根默认是 `closedNode`；当区域根自身迁移时，它的**外部父位置**由
`children[newParent]` 决定，但 `replace[root].children` 仍是替换后节点自身的完整
child list。两者不是同一层语义。

## 5. ReadDiff / ReadApply 适配

```go
func ReadDiff(baseID string, base, current *protocol.ReadTree) (*protocol.ReadDelta, error) {
	if baseID == "" {
		return nil, notRepresentable("delta base id is empty")
	}
	if base == nil || current == nil ||
		base.Root == nil || current.Root == nil {
		return nil, notRepresentable("read tree is missing")
	}
	if base.View != protocol.ViewCompact ||
		current.View != protocol.ViewCompact {
		return nil, notRepresentable("only compact observations share a delta base")
	}

	logicalBase := UnpackTree(base)
	logicalCurrent := UnpackTree(current)
	planned, err := planReadStructuralDelta(logicalBase, logicalCurrent)
	if err != nil {
		return nil, err
	}
	planned.Format = "delta"
	planned.Base = baseID
	planned.View = protocol.ViewCompact
	// 阶段 1 已移除默认值封套；公开 delta 头只有 format/base/view。

	packed := PackDelta(planned)
	if err := packed.Validate(); err != nil {
		return nil, notRepresentable("packed delta is invalid: %v", err)
	}
	applied, err := ReadApply(base, packed)
	if err != nil {
		return nil, notRepresentable("delta does not apply: %v", err)
	}
	if !ReadEqual(applied, current) {
		return nil, notRepresentable("delta does not reconstruct current")
	}
	if !bytes.Equal(mustJSON(applied), mustJSON(current)) {
		return nil, notRepresentable("delta changes canonical wire bytes")
	}
	return packed, nil
}
```

`planReadStructuralDelta` 与内部 planner 共用第 3 节的拓扑阶段，只把字段差异替换为：

- `ReadSetNode` 的显式三态字段，尤其是
  `enabled/focused/selected/checked/protected` 的“未设置 / explicit null / false / true”；
- `ReadBounds` 的 known/hidden/unknown；
- `ReadNode.Chain` 只在 wire 打包阶段出现。

`ReadApply` 先校验 compact view，展开输入 ReadDelta 中的 chain，调用第 4 节同等
Apply 流程，最后对结果 `PackTree`。完整节点的 `checked` 缺席解码为 false、显式 null
保持 unknown；`set.checked` 则只有成员缺席才表示不修改。ReadDiff 必须用严格三态比较生成
`ReadSetNode`，ReadApply 后 `ReadEqual` 和 canonical JSON 比较必须在 nil/false/true 上逐项
相等。`inspect` 不经过该函数。

## 6. 涉及修改的文件

| 文件 | 具体变更 |
| --- | --- |
| [internal/observation/diff.go](../../../../internal/observation/diff.go) | 删除“存续节点进入新增父”和“删除区域含存续后代”两个硬返回；实现 assembly frontier、remove 根和现有 Replace 区域根可迁移的统一规划。 |
| [internal/observation/read_diff.go](../../../../internal/observation/read_diff.go) | 在 `UnpackTree` 后的逻辑 Read 树上复用相同拓扑规划；保持 Add/Replace 打包、逻辑等值和 canonical bytes 校验。 |
| [internal/observation/apply.go](../../../../internal/observation/apply.go) | 改为基线 claims 驱动的原子 Apply；区分 `closedNode` 与 `assemblyRoot`；先检查冲突与闭包，再实例化最终图；Replace 不再依赖旧父槽位。 |
| [internal/observation/read_diff.go](../../../../internal/observation/read_diff.go) | `applyReadDelta` 同样使用 claims、残余闭包和全图校验；保留三态字段与 chain 适配。 |
| [internal/observation/delta_test.go](../../../../internal/observation/delta_test.go) | 增加内部树的 wrapper、unwrap、跨父 Replace、循环、多父、孤儿和序列化回归。 |
| [internal/observation/read_test.go](../../../../internal/observation/read_test.go) | 增加 compact/chain wrapper、显式空 children、wire 严格往返和 canonical bytes 回归。 |

按已完成的阶段 1 接口，公开 `ReadDelta` 最终只保留 `format/base/view` 和五块；
B2 自身不增加任何顶层字段。内部 `Delta` 的既有五块也保持原形状。

## 7. 不能治什么

- 根 ref 变化仍不能表达；服务改发 full。
- 存续 ref 的角色变化仍不能表达；服务改发 full，不能借 Replace 改角色。
- 递归深度或节点数超预算仍由既有树预算拒绝；B2 不放宽预算。
- 错误引用、重复 ref、双父、循环和孤儿仍必须拒绝；B2 不把损坏 delta 当作补丁。
- B2 不改变 STOP、工作所有权、提交条件或 Android 命令不可撤回的事实。

## 8. 验收用例

### 8.1 内部 Node 路径

1. `TestDeltaSurvivingNodeMovesIntoNewParentContainer`
   - 旧 `n1 -> n15`；
   - 新 `n1 -> n90 -> [n15,n91]`；
   - 断言 Add/Children 五块组合可 Apply 且完全相等。

2. `TestDeltaDeepAssemblyFrontier`
   - 新链 `n90 -> n91 -> [n15,n92]`；
   - 断言 n90、n91、n92 都是顶层 Add 根，children 逐层挂接；
   - 每个 Add 定义只携带节点自身字段。

3. `TestDeltaOldContainerRemovedWhileDescendantSurvives`
   - 旧 `n1 -> n10 -> [n15,n16]`；
   - 新 `n1 -> n15`；
   - 断言 `remove:[n10]` 只删除残余 n10/n16，n15 经 `children[n1]` 保留。

4. `TestDeltaExistingParentCrossMoveStillUsesChildren`
   - 保留并强化现有 `TestDiffCrossParentMoveUsesChildren`；
   - 明确断言两个父都发布最终 child list；
   - 实现中不得出现 blanket `oldParent == newParent` 检查。

5. `TestDeltaMovedReplaceRegionDoesNotUseOldParentSlot`
   - 旧 `root -> [A -> [R], B]`；
   - 新 `root -> [A, B -> [R]]`；
   - R 子树足够大，使 planner 选择 Replace；
   - 断言 Replace 与 children 同时存在且 Apply 成功。

6. `TestDeltaRemoveResidueUsesFinalReachability`
   - `remove:[D]` 单独表示删掉整个旧 D 子树，允许成功；
   - 若遗漏 current 中幸存节点的重挂接，Diff 自校验必须失败并回退 full；
   - 若同时 set/定义/显式 children 声明了不可达幸存节点，Apply 必须拒绝；
   - 父还在旧闭包内不是拒绝理由，`root -> D -> [A,B]` 到 `root -> A -> B` 必须成功。

7. `TestDeltaApplyRejectsCycleMultiParentAndOrphan`
   - 分别构造自环、跨子树环、同一 child 出现在两个 parent list、未挂接新增定义和
     不可达存续节点；
   - 所有情况都必须在返回树之前失败。

8. `TestDeltaChildrenEmptyListIsExplicit`
   - 父节点最终 child list 为空时，wire 必须保留 `children:{"nx":[]}`；
   - 不能被解释为“没有 children 块”。

9. `TestDeltaReplaceAbsorbsRemovedInteriorWithoutDuplicateRemove`
   - 构造足够大的封闭 Replace 区域，其中一项旧后代被删、另一项字段改变；
   - 断言被删的旧后代不另发 Remove，字段变化不再重复出现在 set，Apply 后精确等于
     current；若区域包含真正新增后代，则按封闭判据不选 Replace，而走 Add/children 路径。

10. `TestDeltaMovedReplaceRequiresSourceAndTargetClaims`
    - 只发布目标父的新 children、保留来源父旧 child claim 时，必须在移除/替换任何内容前
      以双父拒绝；
    - 同时发布来源父和目标父最终列表时成功，证明 Replace 根迁移不依赖旧父槽位。

11. `TestDeltaExistingParentCrossMoveIsNotBlanketRejected`
    - 用不同 map 插入顺序重复构造相同 delta；
    - 两边都成功、输出块与首错稳定，且只靠两个 `children` claim 完成迁移。

12. `TestDeltaFiveBoolStateMapping`
    - 对 `enabled/focused/selected/checked/protected` 分别覆盖 nil、false、true 的 set 与
      full/Add/Replace 定义往返；
    - `set` 必须区分字段缺席和显式 null；完整节点的 `checked` 缺席解码为 false，显式 null
      保持 unknown；Apply 前后逐状态相等。

13. `TestDeltaHighestRemovedAncestorAcrossSurvivingMiddle`
    - 基线 `root -> D -> A -> X`，current `root -> A`；
    - 只生成最高 remove 根 D，X 被覆盖；children[root]=[A]、children[A]=[]，不得生成嵌套 remove。

14. `TestDeltaReplaceClosurePropagatesDeepParentChange`
    - 基线 `root -> A -> [B,C]`，current `root -> A -> B -> C`；
    - 即使 A 子树达到 Replace 阈值，也不能选 A 为封闭 Replace；改用 children 成功还原。

15. `TestDeltaRemoveRootAndDeadParentClaimsRejected`
    - remove D 与 children[root]=[D] 冲突；remove D 与 children[D]=[] 同样冲突；
    - 不可达的显式 children 环、孤立 Replace 或 Add 定义不得互相自保。

16. `TestDeltaReplaceSurvivesThroughEscapingAncestor`
    - 基线 `root -> D -> A -> R`，current `root -> A -> R'`；
    - remove D、children[root]=[A]、replace[R]=R'；允许 R 通过 A 的继承边存续。

### 8.2 Public Read 路径

本节须同时镜像内部 Node 第 6、13–16 项的最终可达、嵌套 remove 和深层 Replace 反例。

1. `TestReadDeltaWrapperAddAndUnwrap`
   - 与内部 wrapper/unwrap 对偶；
   - 最终检查 `ReadEqual` 与 canonical JSON。

2. `TestReadDeltaChainBoundaryWithAssemblyRoot`
   - 基线包含 `chain [n2 n3]`；
   - 新 wrapper 将 chain 成员或其后代迁移；
   - planner 必须在 `UnpackTree` 后按逻辑 ref 工作，完成后仍能合法打包。

3. `TestReadDeltaWireRoundTripAssembly`
   - 对生成的 ReadDelta 执行严格 JSON encode/decode；
   - 对解码结果执行 `ReadApply`；
   - 与当前 compact tree 比较逻辑等值和 canonical bytes。

4. `TestReadDeltaStandaloneAddRootChildrenOverride`
   - Add 根内联 children 为空；
   - `children[addRoot]` 给出完整子列表；
   - 解码后仍允许该顶层组装根被覆盖。

5. `TestReadDeltaNestedClosedDefinitionRejectsChildren`
   - 反向对照：children 指向纯新增封闭定义内部节点时仍拒绝；
   - 证明 B2 只开放顶层 assembly root。

6. `TestReadDeltaHeaderAndStrictWireRoundTrip`
   - 解码后的公开 delta 头只允许 `format/base/view`，加五块 `set/add/remove/children/replace`；
     不得出现默认值封套或位置字段；
   - 把含 assembly Add、Replace 根迁移、显式空 children 的结果做严格 JSON
     encode/decode；解码后再 `ReadApply`，并与 current 做逻辑和 canonical bytes 双重比较。

7. `TestReadDeltaFiveBoolStatesSurviveWire`
   - 五状态字段分别经过 `ReadSetNode` 的 absent/null/false/true 矩阵；
   - `ReadDiff` 生成的 delta、JSON 往返后的 delta、`ReadApply` 的结果都必须保留严格的
     nil/false/true 等价，不能因默认值处理把 unknown 与 false 合并。

8. `TestMalformedReadDeltaRejectsClosedOverrideAndOrphan`
   - 分别构造“children 覆盖嵌套 closed definition”“Add 根无最终父”“Replace 迁移只保留
     旧父槽位”“自环/多父/孤儿”；
   - 每个反例都必须在 `ReadApply` 返回任何树之前失败。

### 8.3 可观测验收指标

- 两个目标结构事件不再因 `node now lives inside new node` 或
  `subtree holds surviving nodes` 返回 `ErrNotRepresentable`。
- 生成 delta 的 Apply/ReadApply 往返逐字段相等；Read 版本 canonical bytes 也相等。
- 五个 bool 的 nil/false/true 在 set、完整定义、JSON 往返和 ReadApply 后逐项相等。
- 任何非法多父、循环或孤儿都不会产生部分树。
- 现有父间跨父移动、角色变化退 full、根变化退 full 的既有行为不变。
- 只有当 delta 编码 `< full × 70%` 时运行时才采用；否则仍使用 compact full。

## 9. 实施顺序

1. 先加入内部 Node 的 wrapper、unwrap、跨父 Replace、负例测试；
2. 实现基线索引、claims、顶层 assembly frontier 与全图校验；
3. 加入 Read compact/chain 与严格 wire 往返测试；
4. 在 `ReadDiff` 接入共享拓扑规划，保持 70% 判据和 full fallback；
5. 同步正式文档和契约版本变更，再由独立只读文档审计；
6. 单测通过后按真实变更路径做包级回归；是否实机验证按实施范围和发布需要记录。

## 10. 基线实测记录

本分册修订时真实执行：

```text
$env:GOEXPERIMENT='jsonv2'; go test ./internal/observation -count=1
ok github.com/malyjacob/mobile-use-vnext/internal/observation
```

另定向执行：

```text
$env:GOEXPERIMENT='jsonv2'; go test ./internal/observation -run 'TestDiffCrossParentMoveUsesChildren|TestDiffRejectsMoveIntoOtherParentUnderRemovedRegion|TestReadDeltaChildrenReferenceChainMember' -count=1 -v
```

结果为三个测试全部通过。这些只证明基线已有行为和现有回归通过，
不表示 B2 已实现或 B2 用例已通过。

## 11. 实现结果

实现提交：`900e640fe8343f92c4e8dd82dd3df839dde20756` 首次落地，最后由发行锚点
`049583d328e335c56d56f153b7464ea03f1ca814` 覆盖。公共记录见
[实现与验证](../../../verification/observation-continuity-and-revalidation.md)与
[统一基准](../../../verification/observation-continuity-benchmarks.md)。

### 11.1 已落地

| 提案条目 | 代码落点 | 验证范围 |
| --- | --- | --- |
| §2–§3 最终图规划、assembly frontier、remove 根与 Replace 封闭性 | `internal/observation/diff.go` 的 `Diff`、`planStructuralDelta`、`chooseReplaceRegions`、`collectRemoveRoots`、`computeAddFrontier`、`collectChildBlocks`、`validatePlannedOwnership` | 内部 Node 的 wrapper/unwrap、跨父移动、存活穿透、最高 remove 祖先、显式空 children 与封闭 Replace；V12–V15 |
| §4 基线 claims 驱动的原子 Apply 与最终图拒绝 | `internal/observation/apply.go` 的 `Apply`、`applyStructuralDelta`、`validateRemoveRoots`、`validateReplaceRoots`、`expandDefinitions`、`finalChildList` | 精确还原、不可变性，以及循环、多父、孤儿、remove 根冲突和孤立定义的整树拒绝；V12–V15 |
| §5 公开 Read 路径 | `internal/observation/read_diff.go` 的 `ReadDiff`、`planReadStructuralDelta`、`ReadApply`、`diffReadFields`、`applyReadSet` | compact/chain、严格 wire 往返、五 bool 三态与 canonical bytes；V15 |
| 运行时选择与回退 | `internal/engine/capture.go` 的 `captureAndPublish`、`publishTreeAttempt`、`projectCapture` | 固定 full 基线上的 delta 选择、`< full × 70%` 门禁、根/ref/角色变化回退 full；I01–I03、B03/B06、D02/D06 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 代码状态 | 本册默认算法和运行时选择路径代码全覆盖；设备场景覆盖有限，不能把未构造场景写成算法未实现。 |
| D02 | 3 轮均为 `gap`、`fail=0`、`result=partial`；相对固定已确认 full 基线的 delta、ReadApply 和 ref 检查通过，但 `native_scroll_unavailable` 未覆盖，不能写成 D02 整体 PASS。 |
| D06 | 3 轮均为固定 full 基线后的单个 delta，经 BACK、inspect 与 end 闭环通过；没有覆盖多 delta、滚动或复杂重挂矩阵。 |
| 历史行号链接 | 正文中两处 `read_diff.go` 基线行号已超出当前重构文件范围；归档时仅将 href 收敛到现存文件，label 与头部基线语境保持原样，不伪造当前行号。 |
| 设计边界 | 未增加顶层 delta 字段；根 ref/角色变化仍回退 full；70% 门禁、非法最终图拒绝和原子应用语义均保留。 |
