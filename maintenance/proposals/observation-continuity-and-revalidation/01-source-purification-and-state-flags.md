# 分册 01：源头净化（空叶过滤）与状态位表达精简

状态：提案，未实现。

所属总览：[README.md](README.md)

基线提交：`6ec25c87daefbc01c99f78d968214304641d119d`

---

## 0. 摘要

本分册解决 Issue `#15` 的第一层问题：公开树在进入引用连续性、差分和 MCP 投影前，
仍会保留大量没有可读、可操作或非默认状态证据的空叶；同时，完整的 compact/full
节点树携带固定 `defaults {"checked":false}` 声明，并存在已知默认状态被重复输出的空间。

本分册保留两条总体路线：

1. **源头按值净化**：修正 `project` 使用的 `hasSemantics`，按实际属性值和角色判断一个
   无保留后代的可见叶是否值得发布。不能因为来源“提供了某个布尔属性”就视为有语义。
2. **状态输出精简但不伪造事实**：移除 `ReadTree`、`ReadDelta`、`InspectionView` 以及
   MCP 文本中的冗余 `defaults`；完整节点的已知默认状态可以省略，真实 unknown 必须显式
   输出 `null`，`delta.set` 中显式 `false`/`true`/`null` 全部保留。

本提案不把 unknown 归一为默认值，也不把 compact 投影改成有损事实模型。

---

## 1. 问题与真实源码证据链

### 1.1 证据链一：当前空叶判断依据属性 presence，而不是语义值

真实基线没有草稿中出现的 `filterTree`、`roleOf` 或“`FrameLayout` 被映射成
`RoleGroup`”逻辑。实际投影链是：

- [`project`](../../../internal/observation/build.go#L215-L259) 后序计算哪些节点进入公开树；
- [`hasSemantics`](../../../internal/observation/build.go#L297-L302) 判断叶自身是否有语义；
- [`classifyRole`](../../../internal/observation/build.go#L307-L343) 根据真实能力、状态和类名映射
  public role；
- [`newPublicNode`](../../../internal/observation/build.go#L364-L413) 才把通过过滤的节点投影成
  `protocol.Node`。

基线 `hasSemantics` 为：

```go
func hasSemantics(raw *device.RawNode) bool {
	return raw.TextSet || raw.DescSet || raw.HasEnabled || raw.HasChecked ||
		raw.HasSelected || raw.HasFocused || raw.HasClickable ||
		raw.HasLongClick || raw.HasScrollable || raw.HasCheckable ||
		raw.HasPassword || raw.Editable
}
```

这里的问题是任意属性“出现”都算语义，而不是属性值提供了可读、可操作或非默认状态事实。
UIAutomator 常见的普通空节点可能同时带有：

```text
text=""
content-desc=""
enabled="true"
checked="false"
selected="false"
focused="false"
clickable="false"
long-clickable="false"
scrollable="false"
checkable="false"
password="false"
```

因此，即使一个 `View`/`FrameLayout` 没有任何文本、动作、非默认状态或保留后代，当前
`hasSemantics` 仍会因 `TextSet`、`HasEnabled` 等 presence 位返回真，并被发布成：

```text
n10 view
n11 view
...
```

在 compact 中，这类已知默认节点满足 `AnonymousPlain`，其 bounds 被隐藏，但不提供任何
可读内容或动作，只会消耗节点数、引用数、差分大小和 MCP 上下文。

### 1.2 证据链二：非目标 display 包装层会断开目标后代

[`project`](../../../internal/observation/build.go#L238-L254) 对
`DisplayID != cap.Display.ID` 的节点直接跳过；但其目标 display 后代仍可成为已发布 unit。
然而 [`Build` 的组装步骤](../../../internal/observation/build.go#L119-L145)只复制 raw
直接子节点中已有公开节点的项，并未把跨越未发布包装层的后代挂到最近公开祖先。
[`nearestPublishedParent`](../../../internal/observation/match.go#L590-L610)目前只供匹配
建立父索引，不能修复公开树本身。

最小反例：

```text
root(display=0)
  wrapper(display=1)
    child(display=0, visible=true, text="A")
```

`wrapper` 不发布，`child` 有文本而发布；组装时 root 只查看直接子 `wrapper`，得到
`root.children=[]`。随后 `Source` 仍会记录 `child` 的 ref，而 `Refs` 从成树根收集，不含
该 ref，形成 source 索引与公开树/ref 列表不一致。修复仍沿用现有路线：在 `project` 决定 unit 后，为每个
已发布普通节点和 context 计算 `nearestPublishedParent`，以公开父映射组装 children；
synthetic root 作为所有最近公开祖先为 raw root 的节点父节点。非目标 display 包装层继续
不发布、不生成 context。验收捕获必须断言目标 child 直接成为 root 子节点，且
`Refs`、`Source`、`ContextSource` 与树中可达 ref 集合完全一致。

### 1.3 证据链三：来源与内部状态本来就是三态

[`RawNode`](../../../internal/device/types.go#L76-L119) 明确把 `Has*` 与对应值分开：

```go
HasEnabled  bool
Enabled     bool
HasChecked  bool
Checked     bool
HasSelected bool
Selected    bool
HasFocused  bool
Focused     bool
HasPassword bool
Password    bool
```

`protocol.Node`/`protocol.ReadNode` 又用 `*bool` 表示三态：

```text
nil   = 来源未提供，真实 unknown
false = 已知 false
true  = 已知 true
```

当前 [`newPublicNode`](../../../internal/observation/build.go#L384-L410) 通过
[`triBool`](../../../internal/observation/build.go#L477-L485) 保留这一区别；[`AnonymousPlain`](../../../internal/observation/read.go#L17-L39)
也要求五个状态均为非 nil 的已知默认值时，节点才可能成为 anonymous plain。

因此不能以“统一序列化”为理由，在构建阶段把所有 nil 改成已知默认值。这会把来源事实
改写为合成事实。

### 1.4 证据链四：`ReadNode` 已具备正确的 complete-node wire 模型

[`ReadNode.MarshalJSONTo`](../../../internal/protocol/read_tree.go#L203-L397) 当前行为是：

- `enabled=true` 省略，`enabled=false` 显式，`enabled=null` 表示 unknown；
- `focused/selected/protected/checked=false` 省略，`true` 或 `null` 显式；
- `value/hint/resource` 的 nil 在完整节点编码中省略，解码缺席或 null 均为 nil；已知空
  字符串仍显式输出。该可选字符串规则与五个布尔状态的 unknown 显式 null 规则不同。
  `axis` 的 absent/null 表示 unknown，非空有效枚举才表示已知。

[`ReadNode.UnmarshalJSONFrom`](../../../internal/protocol/read_tree.go#L421-L526) 在完整节点中
把成员缺席还原成已知默认值，把显式 `null` 还原成 nil：

```text
absence enabled                     -> known true
absence focused/selected/protected -> known false
absence checked                    -> known false
explicit null                      -> unknown
```

这是 wire 层消费规则，不得反向用于 `RawNode` 或 Build 阶段的候选状态。

内部 [`protocol.Node`](../../../internal/protocol/node.go#L192-L356) 的 enabled/focused/
selected/protected 已采用相近模型，但 checked 当前仍是“nil 省略、false 显式”，与本分册
统一后的完整节点格式不同。本分册要把 `Node.checked` 对齐到 `ReadNode.checked`。

### 1.5 证据链五：状态相等当前会吞掉 unknown/default 跃迁

[`equalTriBool`](../../../internal/observation/tree.go#L97-L106) 目前把 nil 当作调用方传入的
已知默认值。该函数被：

- [`Equal`](../../../internal/observation/tree.go#L25-L95)；
- [`diffFields`](../../../internal/observation/diff.go#L439-L488)；
- [`ReadEqual`](../../../internal/observation/read.go#L286-L341)；
- [`diffReadFields`](../../../internal/observation/read_diff.go#L655-L702)。

共同使用。

最小反例：基线 `Focused=nil`，当前 `Focused=false`。默认化比较认为两者都是 false，不生成
`set.focused`；Apply 后仍是 nil。公开 `ReadDiff` 最后会因规范字节不一致拒绝该 delta 并回退
full，内部 `Diff` 也可能因为 `Equal` 同样使用默认化比较而错误通过。

`checked` 当前通过 [`equalOptionalBool`](../../../internal/observation/tree.go#L108-L113)
精确比较，反而是正确方向。不能按草稿把 checked 改成 `equalTriBool(..., false)`，否则
`checked:null -> false` 和反向跃迁都会被吞掉。

### 1.6 证据链六：移除 `defaults` 涉及多个生产路径

除协议编码类型外，当前还存在以下真实调用点：

- [`ReadDiff`](../../../internal/observation/read_diff.go#L15-L49) 给 delta 填写 `Defaults`；
- [`engine inspect subtree`](../../../internal/engine/inspect.go#L120-L148) 与
  [`query`](../../../internal/engine/inspect.go#L220-L239) 的精确字节预算对象包含
  `protocol.Defaults`；
- [`InspectionView` 编码](../../../internal/protocol/surface.go#L1058-L1147)与
  [`响应解码`](../../../internal/protocol/decode_response.go#L521-L600)要求 envelope 或嵌套树
  携带 defaults；
- [`MCP 工具说明`](../../../internal/mcp/schema.go#L16-L38)和
  [`tree text`](../../../internal/mcp/tree_text.go#L65-L84)固定声明 defaults；
- [`mcp-probe` 文本解析](../../../scripts/mcp-probe/result_text.go#L172-L269)及
  [`结果字段检查`](../../../scripts/mcp-probe/result.go#L317-L360)依赖 defaults。

只删除 `protocol.Defaults` 或只删除 MCP 一行会破坏编译、预算一致性和文本回放检查。

---

## 2. 核心不变量与三层语义

### 2.1 事实层

`device.RawNode` 保留来源事实；普通可见节点的 `protocol.Node` 按相应 presence 投影。
context 按定义不公开自身状态，五个指针为 nil 表示该阅读节点的状态不可用，即使私有 raw
已知也不据此授予动作；synthetic root 的已知默认则来自合成节点定义。下列对应关系用于普通节点：

- 对应 `Has* == false` 时，状态指针为 nil；
- 对应 `Has* == true` 时，状态指针指向实际布尔值；
- 构建器不得因为 wire 默认规则把 nil 改写为 false/true。

### 2.2 完整节点 wire 层

适用于：

- full `ReadTree`；
- delta 的 `ReadNode` 形式 `add`/`replace` 完整定义；
- inspect 的 tree/subtree/query 详细节点；
- 若 `protocol.Node` 仍作为完整节点序列化，则也必须遵守同一矩阵。

| 字段 | 已知默认 | 非默认已知值 | unknown |
| --- | --- | --- | --- |
| `enabled` | `true` 省略 | `false` 显式 | `null` 显式 |
| `focused` | `false` 省略 | `true` 显式 | `null` 显式 |
| `selected` | `false` 省略 | `true` 显式 | `null` 显式 |
| `protected` | `false` 省略 | `true` 显式 | `null` 显式 |
| `checked` | `false` 省略 | `true` 显式 | `null` 显式 |

完整节点解码器只在输入是一个完整节点时：

- 成员缺席 -> 已知默认值；
- 显式 null -> unknown。

这是一条 wire 消费规则，不是 source 归一化规则。

### 2.3 `delta.set` 修改层

- `Set=false`：字段缺席，保持基线；
- `Set=true, Value=false`：明确改为 false；
- `Set=true, Value=true`：明确改为 true；
- `Set=true, Value=nil`：明确改为 unknown，wire 输出 null。

`set` 不应用完整节点的缺席默认，也不使用 `omitempty` 混淆未修改与赋值。

---

## 3. 源头净化算法

### 3.1 判定顺序

`project` 继续按后序处理。对每个目标 display 上的子节点：

1. 若节点有至少一个已保留、且 parent 链有效的后代，则作为结构容器保留；
2. 否则，若是可见叶，只有 `hasSemantics(raw)` 为真才保留；
3. 不可见叶不发布；
4. 不可见但位于目标 display、且有保留后代的祖先成为 context；
5. 不属于目标 display 的节点不发布，即使其子树含目标 display 后代。

`hasSemantics` 只处理“没有保留后代”的可见叶，不应重新决定已有保留后代的容器。

本提案继续按源字符串的精确非空值判断，不新增 `TrimSpace` 或标签清洗规则。只含空白的
已知字符串仍可保留，并作为源值完整交付；这属于当前精确内容边界，不由实现者临时改判。

### 3.2 修正后的谓词

建议实现为：

```go
func hasSemantics(raw *device.RawNode) bool {
	if raw.TextSet && raw.Text != "" {
		return true
	}
	if raw.DescSet && raw.Description != "" {
		return true
	}
	if (raw.HasClickable && raw.Clickable) ||
		(raw.HasLongClick && raw.LongClickable) ||
		(raw.HasScrollable && raw.Scrollable) ||
		raw.Editable ||
		(raw.HasCheckable && raw.Checkable) {
		return true
	}
	if (raw.HasEnabled && !raw.Enabled) ||
		(raw.HasChecked && raw.Checked) ||
		(raw.HasFocused && raw.Focused) ||
		(raw.HasSelected && raw.Selected) ||
		(raw.HasPassword && raw.Password) {
		return true
	}
	return retainsIntrinsicLeafRole(classifyRole(raw))
}

func retainsIntrinsicLeafRole(role protocol.Role) bool {
	switch role {
	case protocol.RoleImage,
		protocol.RoleInput,
		protocol.RoleButton,
		protocol.RoleCheckbox,
		protocol.RoleRadio,
		protocol.RoleSwitch,
		protocol.RoleToggle,
		protocol.RoleScroll,
		protocol.RoleText:
		return true
	}
	return false
}
```

当前 `classifyRole` 只会在已有动作、文本或 checkable 证据之外额外产生 `RoleImage` 的
“无文本叶”情况；其余固有角色通常已被前述值或能力判据覆盖。显式列出各角色是为了让
谓词独立于 `classifyRole` 的未来细节，而不是重新引入 presence 位。

### 3.3 resource-only 边界

非空 `resource-id` 本身不是可读内容、动作或状态，不能单独让一个可见叶通过过滤。

当前 [`needsResourceHint`](../../../internal/observation/build.go#L415-L423) 只对“无标签且
可点击”的节点发布 resource hint；一个既无动作、无内容、无固有叶角色、也无保留后代的
resource-only 普通节点本来就没有可发布的 resource，继续保留只会制造空节点。

resource-id 仍可用于：

- 已被其他证据保留下来的节点在内部 continuity 中作为源锚点；
- 无标签 interactive 节点的公开 source hint；
- input 的源身份匹配。

resource-only 节点若因 `RoleImage` 等固有叶角色被保留，则保留的是角色语义，不是
resource-id 语义。

### 3.4 unknown 状态边界

未知状态本身不是叶语义：

- `HasEnabled=false`、`HasChecked=false` 等只表示来源未给出状态；
- 一个既无内容、动作、固有叶角色或保留后代的普通叶，即使五个状态全 unknown，仍应过滤；
- 一旦节点因其他证据保留，未知状态必须原样保存为 nil，并在完整 wire 中输出 null。

不能把“状态未知”解释成默认，也不能把“有状态属性但值为默认”解释成语义。

### 3.5 固有叶角色边界

以下角色可以在没有文本或动作时仍具有叶语义：

- `image`：图像像素本身可能是内容；
- `input`：可编辑能力；
- `button`：已由 clickable 或 label 支撑；
- `checkbox`/`radio`/`switch`/`toggle`：checkable 能力；
- `scroll`：滚动能力；
- `text`：已由 label 支撑。

`image` 是本谓词中最宽的例外：无描述、无动作的 `ImageView` 仍可能被保留，因为它可能有
可见图像内容。这不会解决“骨架屏由无语义 ImageView 组成”的场景；若 Issue 实际也包含这类
占位图，需要另立证据和判据。本提案不凭类名之外的像素事实猜测图片是否有业务意义。

### 3.6 非目标 display 祖先边界

[`project`](../../../internal/observation/build.go#L238-L253) 对
`DisplayID != cap.Display.ID` 的节点始终不发布，即使它是目标 display 内容的祖先包装层。

目标 display 中保留下来的节点必须通过 raw parent 链连接到最近的已发布祖先；公开树不会
为了保持其他 display 的原始包装层而创建 context。基线注释表达了这一规则，但
[`Build`](../../../internal/observation/build.go#L119-L145)仍只挂 raw 直接子节点，
[`nearestPublishedParent`](../../../internal/observation/match.go#L590-L610)也只在匹配阶段
使用，因此实施时需要按 §1.2 修正组装，而不是把注释当成已实现事实。验收必须同时证明
目标后代可达、没有为非目标包装层生成 context，以及树、ref 列表和 source 索引闭包一致。

`inTree` 的后序含义也要保持一致：它记录子树是否含已发布节点，不只记录该 raw 节点自身
是否发布。经过不发布的非目标 display 包装层时，仍应把 `childInTree` 向其 raw 父节点
传播，不能在 `continue` 前丢掉后代信息。否则 `目标 display 不可见祖先 -> 非目标包装层
-> 目标文本叶` 会遗漏应保留的 context；只修改最后的挂载还不能恢复该祖先。新增此反例，
分别断言目标祖先成为 context、非目标包装层不发布、文本叶挂在该 context 下。

组装采用与分册 02 相同的公开父索引，保持 raw 先序中的兄弟相对顺序，不能按 map 顺序或
后序追加 children。示意顺序为：

```text
units, subtreeHasPublished = projectPostorder(capture)
    // 对非目标包装层也传播子树标志，但 units[wrapper].published=false
public[rootRaw] = newSyntheticRoot()
for i in validRawPreorder(capture):
    if i != rootRaw && units[i].published:
        public[i] = newPublicNodeWithoutChildren(i)
for i in validRawPreorder(capture):
    if i == rootRaw || !units[i].published: continue
    p = nearestPublishedParent(capture, units, i)
    require p == rootRaw || units[p].published
    public[p].children.append(public[i])
collect Refs and source maps from the same completed public topology
require treeRefs == Refs == keys(Source) union keys(ContextSource)
require Refs has no duplicate and equals public tree preorder
require keys(Source) intersect keys(ContextSource) == empty
require Source[root.ref] == capture.Root
require each ordinary ref maps only in Source to its actual raw index
require each context ref maps only in ContextSource to its actual raw index
```

该组装问题是有独立反例的实现路径问题。实施时先使该反例失败，再修复组装；本次只把它
纳入与净化和公开父索引直接相关的边界，不声称已经运行过该复现或完成修复。

ref 集合相等不足以证明来源映射正确：同一个 context ref 同时出现在两张表，或 context
只在正确的表中却指向另一个 raw index，都必须在验收中失败。跨 display 包装层的最小
用例应包含目标 display 上的不可见 context 与普通文本子节点，分别核对两张表的 raw
index，再采下一帧确认 context 通过 ContextSource 参与连续性。

### 3.7 context 边界

只有同时满足以下条件的节点才成为 context：

- 属于目标 display；
- `Visible=false`；
- 有至少一个已保留后代；
- 自身没有作为普通节点发布。

context 固定为：

- `role=group`；
- `context=true`；
- `bounds=nil`；
- 无 label/actions/value/hint/axis/resource；
- 五个状态指针保持 nil，不伪造已知默认；
- 只进入 `ContextSource`，不进入 `Source`，不可作为动作目标。

### 3.8 不变式 I-1 的准确范围

构建完成并对 `protocol.Node` 做 compact 投影后，若一个节点满足
[`AnonymousPlain`](../../../internal/observation/read.go#L17-L39)，则该节点必须拥有至少一个
子节点。

这是关于“符合匿名谓词的普通 view/group”的限定不变式：

- context 被 `AnonymousPlain` 显式排除，不靠未知状态规避；
- root 角色不满足谓词；
- 任一状态 unknown 的节点不满足谓词；
- image/input/checkable 等非 view/group 角色不满足谓词；
- 仍可能保留无描述图像等视觉叶，不应宣称“所有无语义外观叶都已消除”。

正确的 I-1 表述是：

> **I-1（限定）：任何构建后满足 `AnonymousPlain` 的 `view`/`group` 节点都是容器，
> 而不是可发布的匿名普通叶。**

不能把它扩大为“公开树绝不存在无文本叶”或“所有视觉占位都已过滤”。

I-1 也只约束公开投影，不约束原始设备事实。分册 04 的
[`matchFreshNode`](../../../internal/engine/act.go#L886-L916)与
[`lineageOf`](../../../internal/engine/act.go#L866-L886)消费
`device.Capture.Nodes` 及 raw parent 链，因此空 `FrameLayout` 即使不进入公开树，仍可
留在重验原始事实中供 L2 判断；不能为了满足 I-1 去改写或过滤 `Capture.Nodes`。验收应分别
证明：公开树按 I-1 不含匿名普通叶；同一 capture 的 raw parent 链与 04 所需的祖先事实未被
源头过滤器改写。

---

## 4. 状态、序列化与差分的一致性

### 4.1 构建与 root/context

- 普通节点继续由 `triBool(raw.HasX, raw.X)` 生成状态；
- synthetic root 保留现有已知 `enabled=true`、`focused/selected/protected=false`；
- synthetic root 新增 `checked=false`，因为 root 是合成结构节点而不是未知输入来源；
- context 不套用上述合成默认，五个状态继续 nil。

root 的 checked 变为已知 false 后，完整节点 wire 会按默认规则省略它；不会再输出
`checked=null`。context 和真实未知状态仍输出 null。

### 4.2 `Node` 与 `ReadNode`

`ReadNode` 当前矩阵基本正确，保留。

`Node` 需要：

- `enabled`/`focused`/`selected`/`protected` 保持现有的“已知默认省略、unknown null”；
- `checked=false` 从“显式 false”改为“省略”；
- `checked=nil` 从“省略”改为“显式 null”；
- 解码时成员缺席恢复已知 false，显式 null 仍是 nil。

这样，内部完整树、`add`/`replace` 完整定义与公开阅读节点对 checked 使用同一语义。

### 4.3 full / add / replace / inspect

这四个路径共享完整节点状态矩阵：

- full observation full tree；
- delta 的 `add` 定义；
- delta 的 `replace` 定义；
- inspect 的 detailed tree、subtree 和 query match node。

不得只修 full 而让 add/replace 继续使用不同的 checked 缺省，也不得让 inspect detailed
节点变成另一套状态格式。

### 4.4 `set`

`SetNode`/`ReadSetNode` 继续使用 [`Field[T]`](../../../internal/protocol/node.go#L558-L583)：

- 只有 `Set=true` 才编码，保留 false/0/""/[]/null；
- `IsZero` 只代表整个 set 条目没有字段，不改变单个字段的三态。

[`applySet`](../../../internal/observation/apply.go#L289-L334)和
[`applyReadSet`](../../../internal/observation/read_diff.go#L499-L542)已经按“出现即完整新值”
执行；保留这一结构，不把 null 替换成默认。

### 4.5 Equal、ReadEqual 与 diff

状态比较必须严格区分 nil/false/true。建议将默认化 helper 改为精确 optional compare：

```go
func equalOptionalBool(a, b *bool) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}
```

对 `Enabled/Focused/Selected/Protected/Checked` 在以下位置统一使用：

- `Equal`；
- `sameLogicalNode`；
- `ReadEqual`；
- `diffFields`；
- `diffReadFields`；
- 其他以这些字段判断“未变化”的浅比较。

变化时仍把当前指针原样写入 set：

```text
unknown -> false : set false
false   -> unknown: set null
unknown -> true  : set true
true    -> unknown: set null
```

完整节点解码后的缺省默认已经变成已知指针，因此 exact compare 不会把合法的 wire 缺省
误判为 unknown。

现有 [`TestEqualCanonicalDefaults`](../../../internal/observation/delta_test.go#L563-L586)
把“内部指针为 nil”和“已知默认值”断言为相等，这正是需要推翻的旧规则。实施时应把它改成
两项事实：内部 `nil != true/false`；只有把完整节点 wire 的成员缺席解码后，才得到已知
默认指针。不能通过保留默认化比较来让旧测试继续通过。

### 4.6 Pack / Unpack / ReadDiff / ReadApply

- `AnonymousPlain` 继续要求五个状态已知且为默认，unknown 节点不隐藏几何、不参与 chain；
- `PackTree`/`PackDelta` 只折满足谓词的节点，不改写状态；
- `UnpackTree` 展开 chain 后仍保留 nil；
- `ReadDiff` 用严格比较生成 set，再 `PackDelta`；
- `ReadApply` 保持 set null，再 `PackTree`；
- `ReadDiff` 的最终 `ReadEqual` 和规范 JSON 字节比较都必须能证明：unknown/default 跃迁
  已通过显式 set 往返，而不是被“视为相等”。

`ReadApply` 不需要有损 read 投影；它继续解释相同的完整节点与 set 语义。

### 4.7 MCP 文本

完整节点行继续采用矩阵：

- `enabled=false` 或 `enabled=null` 显式，已知 true 省略；
- `focused/selected/protected/checked` 的 true/null 显式，已知 false 省略；
- `checked=null` 是真实 unknown，不能统一删除。

`set` 行中任何出现的状态字段都写明确值，包括 `checked=false`、`focused=true`、
`enabled=null` 等。

移除每个树块 header 后的固定：

```text
defaults {"checked":false}
```

但完整节点 schema 仍保留“缺席 checked 表示已知 false”的 wire 解码语义。删除的是冗余
声明行，不是三态事实。

---

## 5. 移除 `Defaults` 的完整实施边界

### 5.1 协议类型与响应解码

需要同步修改：

- `internal/protocol/read_tree.go`：删除 `Defaults` 类型和 `ReadTree`/`ReadDelta` 字段；
- `internal/protocol/surface.go`：删除 `InspectionView.Defaults`；
- `internal/protocol/decode_response.go`：tree/subtree/query 不再要求 envelope 或嵌套树
  defaults；
- `internal/protocol/responses.go`：保持完整节点现有字段顺序，不把 defaults 变成其他
  默认提示的替代品。

`ReadTree.UnmarshalJSONFrom` 仍验证 format/scope/view/root；`ReadDelta.UnmarshalJSONFrom`
仍验证 format/base/view、ref 和块形状。删除 defaults 不削弱严格 shape validation。

### 5.2 差分与观察构造

- `ReadDiff` 不再写 `delta.Defaults`；
- 空差异继续只含 `format`、`base`、`view`；
- `PackTree`/`PackDelta`/`UnpackTree` 不再传播 defaults；
- 原有 70% delta 选择、原子 Apply 和最终精确还原校验保持不变。

与[分册 03](03-structural-delta-diff-and-apply.md)的 `ReadDiff` 规划保持一致：
不再向 delta 填入 `Defaults`，也不能把“当前 schema 默认值”换个函数名重新填回 delta；
空差异和非空差异都只由
`format/base/view` 加五类变更块表达。验收时全仓搜索 `planned.Defaults`、
`currentSchemaDefaults` 与 `protocol.Defaults` 应无生产或测试残留，并分别对空差异和非空
差异执行 `ReadApply`、`ReadEqual` 与 canonical bytes 还原。

### 5.3 inspect 精确预算

[`inspectionSubtree`](../../../internal/engine/inspect.go#L120-L148) 的 unit 内容对象从：

```text
{view, defaults, root}
```

改为：

```text
{view, root}
```

具体触点包括 `internal/engine/inspect.go:136` 的匿名预算结构体字段。

[`queryEmptyBytes`](../../../internal/engine/inspect.go#L220-L239) 从：

```text
{view, defaults, matches, total, truncated}
```

改为：

```text
{view, matches, total, truncated}
```

具体触点包括 `internal/engine/inspect.go:226` 的查询预算结构体字段。

`inspectionTree` 已经直接编码完整 detailed tree，删除 ReadTree.defaults 后会自然使用新预算，
但仍须增加边界测试，禁止用旧常量手工扣减。

### 5.4 MCP、工具目录与消费者

`internal/observation/read_test.go:71` 也有对旧 defaults 输出的正断言，应改为编码不含
默认声明；同时保留 checked true/null、缺席解码 false 以及 Pack/ReadApply 的覆盖。

同步覆盖：

- `internal/mcp/tree_text.go`：不再输出 defaults 行，更新 treeTextBlock 长度计算；
- `internal/mcp/schema.go`：删除 `schema.go:20` 的工具说明文字
  `块头后固定一行 ... defaults ...`，改为“块头后空一行进入正文”；
- `internal/mcp/result_manifest_test.go`：字段清单移除 ReadTree/ReadDelta/InspectionView/Defaults；
- `internal/mcp/tree_text_test.go`、`server_test.go`、`result_text_test.go` 与
  `result_parity_test.go`：所有块头、golden、structured/text parity fixture 删除 defaults，
  同时保留完整节点 `checked=true/null` 与 set 显式值的覆盖；
- `scripts/mcp-probe/result.go`：结构化比较不再期待 defaults；
- `scripts/mcp-probe/result_text.go`：文本解析不再消费 defaults 行；
- `scripts/mcp-probe/probe_test.go` 与 `result_modes_test.go`：JSON 与文本 fixture 同步更新。

`schema.go` 属于 MCP 工具目录输入。工具描述变化会改变工具目录指纹；发布仍按既有构建脚本
生成并验证 `build/release/manifest.json` 中的指纹。禁止手改 manifest 或手工填写新指纹，
也不得跳过 manifest 核验。

### 5.5 契约与正式文档

移除公开响应成员并改变工具说明是 contract/工具目录变化：

- 只从 `release/versions.json` 修改共同 `contract_version`，再由脚本生成常量；
- 更新 `docs/interface.md`、`docs/mcp.md`、`docs/architecture.md` 中 defaults、完整节点矩阵、
  inspect 文本和空 delta 的现行描述；
- 不得把尚未实现的行为提前写进 `docs/`。

---

### 5.6 字节预算联动

不调整现有默认上限，按新 wire 的实际 canonical 编码重新计量：

| 预算 | 真实路径 | 默认上限与影响 |
| --- | --- | --- |
| `MaxFullTreeBytes`（观察） | `internal/engine/capture.go:523-535` 的 compact 编码 | 128 KiB；Defaults 删除、过滤与状态编码共同改变大小 |
| `MaxFullTreeBytes`（inspect） | `inspectionTree` 的 detailed 编码 | 128 KiB；与 compact 独立验边界，不能相互替代 |
| `MaxInspectionUnitBytes` | subtree/query 两个内容对象 | 32 KiB；同步移除预算对象的 Defaults |
| `MaxSnapshotCostBytes` | `internal/observation/read.go:403` 的 raw capture + 内部 Tree | 4 MiB；Node.checked 的 nil 从省略变显式 null 会增大，已知 false 改省略会缩小 |
| `MaxRetainedSnapshotCostBytes` | `capture.go:369-398` 的唯一留存快照总和 | 32 MiB；新单项成本改变留存计划，但不改变原有提交/保留门禁 |

因此“输出精简”不保证内部快照成本单向降低。按实际编码字节数核对边界，但保留各类
预算原有的拒绝、截断和淘汰机制，不能把它们统一改成超限即拒绝：

- compact observation、detailed tree、subtree 与单项 SnapshotCost：实际计量为 N 时，
  上限 N 可接纳，N-1 按各自错误族拒绝。
- query 单命中：按 `unitBase + hitBytes` 计量，边界内接纳，超限整次拒绝。
- query 总体对象：每个单命中都可容纳时，多个命中总量超限应返回能容纳的最大完整前缀，
  保留实际 total 并设置 `truncated=true`；不能因总体超限改报 inspection_too_large。
  边界测量须分别计入完整/截断信封、逗号分隔符及实际布尔编码，不能按旧 Defaults 字节
  数手工扣减，也不能仅测单命中就声称覆盖了查询总量。
- 留存总量：总值为 N、上限 N 时可接纳；上限 N-1 时先按既有规则规划淘汰最旧未受保护
  快照，能容纳就原子提交并淘汰。只有受保护集合加新快照仍放不下才按 retained_snapshots
  拒绝，且失败不得删除历史。

分别覆盖以上精确边界和分支，不能用一个 16 字节的极小上限失败测试代替。
`TestSnapshotCostIsDeterministic` 除重复计算相等，还应覆盖 checked=nil/false/true 的
成本变化；需要提高上限时须另给证据与取舍，不能暗改。

## 6. 受影响测试与新增验收

本节描述实施时必须具备的测试，不代表当前已经运行或通过。

### 6.1 源头过滤

新增或扩展：

- 全默认空 `View`、`FrameLayout`：即使 `TextSet/HasEnabled/HasClickable` 等 presence 位齐全，
  也被过滤；
- 空 `TextView`、空自定义 View：过滤；
- 有文本或描述：保留；
- clickable/long-clickable/scrollable/editable/checkable：保留；
- `enabled=false`、`checked=true`、`focused=true`、`selected=true`、`password=true`：保留；
- 无文本 `ImageView`：按本提案固有叶角色规则保留；
- resource-only 非交互普通节点：过滤；
- 无自身语义但有一个保留子节点：作为容器保留；
- 无自身语义且所有子节点均被过滤：容器也过滤；
- 目标 display 不可见但有保留后代的祖先：context；
- 非目标 display 祖先：不 publish，也不生成 context；其目标 display 后代直接挂到最近
  已发布祖先（最小例中直接挂到 synthetic root），并断言树可达 ref、`Refs`、`Source` 与
  `ContextSource` 闭包一致；
- context 五个状态仍 nil；
- synthetic root `Checked` 为已知 false。

现有 [`TestBuildSyntheticRoot`](../../../internal/observation/build_test.go#L38-L71)、
[`TestBuildVisibleFilteringAndContextAncestor`](../../../internal/observation/build_test.go#L73-L128)、
[`TestBuildRoleAndActionMapping`](../../../internal/observation/build_test.go#L130-L187) 和
[`TestBuildCheckedValueAndDefaults`](../../../internal/observation/build_test.go#L189-L264)
需要扩展而不是删除 unknown 断言。

[`TestActTapCarrierNodePrefersNamedBeforeAnonymous`](../../../internal/engine/act_tap_scope_test.go#L1078-L1107)
当前用无子空 `FrameLayout` 作为 anonymous carrier。实施时给它增加一个真实保留子节点，使其
成为符合 I-1 的匿名容器；测试意图仍是“具名 carrier 优先”。

### 6.2 Compact / Pack / ReadEqual

扩展 [`read_test.go`](../../../internal/observation/read_test.go)：

- `AnonymousPlain` 对五个状态任一 nil 都返回 false；
- unknown 节点不隐藏 bounds、不参与 chain；
- 只含已知默认状态的匿名 `view/group` 可以隐藏几何并参与 chain；
- `PackDelta` 对 add/replace 使用相同规则；
- `ReadEqual` 区分 nil/false/true；
- 把旧 `TestEqualCanonicalDefaults` 改成断言内部 nil 与已知默认不相等，另测完整 wire 缺席
  解码为已知默认；
- I-1 测试只断言满足 `AnonymousPlain` 的节点有子节点，不泛化到 image/context/未知状态。

### 6.3 完整节点 wire 矩阵

对 `Node`、`ReadNode`、full、add、replace 和 inspect 三个 scope 覆盖同一矩阵：

| 输入内部值 | 完整 wire | 解码后 |
| --- | --- | --- |
| enabled true | 省略 | 已知 true |
| enabled false | false | 已知 false |
| enabled nil | null | unknown |
| focused false | 省略 | 已知 false |
| focused true | true | 已知 true |
| focused nil | null | unknown |
| checked false | 省略 | 已知 false |
| checked true | true | 已知 true |
| checked nil | null | unknown |

同步修改 [`protocol_test.go`](../../../internal/protocol/protocol_test.go)、
[`decode_response_test.go`](../../../internal/protocol/decode_response_test.go) 和
[`surface_test.go`](../../../internal/protocol/surface_test.go) 中依赖 defaults 的断言。

### 6.4 Delta set 三态往返

新增至少以下 ReadDiff/ReadApply 用例：

- base `enabled=null` -> current `enabled=true`；
- base `enabled=true` -> current `enabled=null`；
- base `enabled=null` -> current `enabled=false`；
- base `focused=false` -> current `focused=null`；
- base `selected=null` -> current `selected=false`；
- base `checked=null` -> current `checked=false`，必须输出 `"checked":false`；
- base `checked=false` -> current `checked=null`，必须输出 `"checked":null`；
- 同一 set 中 false、true、null 和空字符串/空数组同时存在；
- 字段缺席只表示保持基线，不继承此前另一个 delta。

每个用例都要求：

1. `ReadApply(base, delta)` 成功；
2. `ReadEqual` 为真；
3. 规范 JSON 字节完全相等；
4. 去掉严格状态比较后测试会失败。

空 delta 与非空 delta 还要分别覆盖新的 `format/base/view` 外壳；分册 03 的伪代码不得再出现
`planned.Defaults` 或 `currentSchemaDefaults`。

### 6.5 预算、MCP 与发布验证

- subtree/query 内容对象不再含 defaults；
- compact observation 与 detailed inspect 分别按 128 KiB 边界测量，subtree/query
  分别按 32 KiB 边界测量；按 §5.6 分别覆盖单项拒绝、query 最大完整前缀和留存淘汰/
  受保护集合拒绝，不能把 query 总量或有可淘汰项的留存超限误写成一律拒绝；
- 删除 defaults 使原本刚好越界的候选进入边界时，预算测试必须随之更新；
- `internal/engine/inspect.go:136` 与 `internal/engine/inspect.go:226` 的预算结构体均同步删除
  defaults；
- MCP full/delta/inspect 文本块 header 后直接进入正文；
- `internal/mcp/schema.go:20` 不再声明固定 defaults 行；
- full 的 unknown 状态仍显示 null；
- set 的 false/true/null 全部显示；
- `internal/mcp/tree_text_test.go`、`server_test.go`、`result_text_test.go`、
  `result_parity_test.go`、`result_manifest_test.go` 及 `scripts/mcp-probe/{probe,result_modes}_test.go`
  的 defaults fixture 均同步更新；
- `scripts/mcp-probe` 能从文本回放结构化结果，不再期待 defaults 行；
- 工具目录指纹由现有发布脚本重新生成；
- `scripts/verify-manifest.ps1` 对最终 manifest 的源码、契约、产物和工具目录指纹核验通过。

### 6.6 验证边界

上述测试分为：

- 纯单元测试：过滤、三态、wire、Pack、diff/apply、预算和文本；
- 假设备集成测试：observe/act/wait_for/inspect 的 full/delta 路径；
- 真机验证：只有在实现后实际执行设备流程时才能记录“已实测”。

本提案当前仍是“提案，未实现”。在实现和实际运行前，不得把任何用例写成“已通过”、
“已验证”或“已实测”。

---

## 7. 不能解决的问题与实施限制

- 本提案不会猜测无标签、无动作 `ImageView` 的业务含义；它按固有 image 角色保留，
  因此不能保证消除全部骨架屏占位图。
- resource-only 非交互节点不会被保留，但这不表示所有资源节点都被删除；有内容、动作、
  状态或固有角色证据的 resource 节点仍可保留。
- `checked=null` 不会从所有输出中消失。已知默认的 checked 会省略，真实未知仍以 null
  表达；删除 null 会违反三态契约。
- 过滤会改变首次公开树的节点集合和 ref 分配结果，这是预期行为；会话内仍遵守既有保守
  连续性规则，旧节点被过滤后不会复活。
- 本提案不通过“全填默认”消除 delta 状态跃迁。分册 03 的结构差分必须能在严格三态比较下
  表达 unknown/default 的 set 变化，规划结果不再携带 Defaults。
- 分册 02 的匹配输入会少掉无语义空叶；不得依赖这些已过滤节点继续持有或消费 ref。
- 分册 04 的身份重验消费原始 `Capture.Nodes`，源头过滤不移除其中的空包装层；I-1 只约束
  构建后的公开 `AnonymousPlain` 节点。实施 04 时仍从完整 raw parent 链取证，不把公开过滤
  当成设备事实已变化。

---

## 8. 推荐实施顺序

1. 先补源头过滤与非目标 display 断链的先红复现：presence 默认属性齐全的空
   `View`/`FrameLayout` 当前会被保留；目标 display 后代跨过非目标 display 包装层后当前
   无法从公开根到达。
2. 修正 `hasSemantics`，按最近已发布 raw 祖先组装公开 children，同时补
   container/context/non-target-display/resource-only/image 边界测试。
3. 保持 Build 阶段三态，补 synthetic root checked=false 和 context unknown 测试。
4. 修正 `protocol.Node.checked`，统一完整节点 wire，并补 full/add/replace/inspect 矩阵。
5. 把 `Equal`、`ReadEqual`、`diffFields`、`diffReadFields` 改为严格三态比较，替换
   `TestEqualCanonicalDefaults`，补 set 往返。
6. 删除 `Defaults`，同步协议验证、ReadDiff、engine inspect 两处预算、MCP schema/tree text、
   分册 03 规划伪代码、manifest、probe 和工具目录指纹验证。
7. 更新 contract、正式文档和发布检查；最终文档审计及验证记录按仓库流程单独完成。

本分册的落地目标不是增加新公开字段，而是修正来源过滤并让现有树、差分和阅读表示在
“已知默认省略、真实 unknown=null、set 显式赋值”三层上保持一致。
