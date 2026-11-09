# 提案：MCP 文本结果附带确定性可读树块

状态：提案，未实现。
基线提交：`dd503b19f0566be9a59fa49c425f4df667c1e098`（分支 `master`，2026-11-10）。
适用范围：`internal/mcp` 的结果投影（`TextContent`），影响 `start`、`observe`、`act`、`open_app`、`inspect` 五个工具的可读正文；`status`、`end`、`keepalive`、`apps`、`notify`、`shell`、`diagnose` 不产生树块。
影响范围：`internal/mcp/render.go`、新增 `internal/mcp/tree_text.go`、`internal/mcp/server.go`、`internal/mcp/schema.go`、`internal/mcp/*_test.go`、`scripts/mcp-probe/checks.go`、`docs/mcp.md`、`docs/interface.md`、`maintenance/decisions.md`。
契约（`/api/*` 字段与 `contract_version`）：不改变。MCP 工具的入参 schema 与 `structuredContent` 形状也不改变；只改变 `TextContent` 的内容，以及五个工具 description 的说明文字。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/mcp/render.go`、`internal/mcp/server.go`、
`internal/mcp/schema.go`、`internal/engine/capture.go`、`internal/engine/inspect.go`、
`internal/protocol/node.go`、`docs/mcp.md`、`docs/interface.md`）。

---

## 0. 摘要

MCP 规范要求返回结构化内容的工具**同时**把序列化结果放进 `TextContent`，而当前适配器的
可读正文按设计**不含任何节点**（[render.go:12-15](../../internal/mcp/render.go#L12)）。在不渲染
`structuredContent` 的宿主里，模型看不到页面：`observe`/`act` 的正文关于树只说明
`format` 与 `base`，`inspect` 的正文只有观察头、计数与错误，没有命中内容。

本提案在 `internal/mcp` 增加一个**纯函数渲染器**，把同一次结果里的树/差异/补看内容投影成
确定性的行式文本块，追加在现有 `key=value` 正文之后；`structuredContent` 保持原样。
树块分三种：观察 `full`、观察 `delta`、补看 `inspect`（`tree`/`subtree`/`query`）。
超预算整块省略并显式标注，渲染器永不输出半棵树；块尾有终止行，宿主截断可被发现。

不做：不改 `/api/*`、不改工具入参 schema、不在适配器里解析 delta、不做文本解析 API、
不改 full/delta 的选择策略。

## 1. 问题

### 1.1 现象与代价

- 一次 `act` 的 MCP 文本正文约 300 字节，包含 **0 个节点**；同一次结果的
  `structuredContent` 含完整树或差异。
- `observe` 的正文有观察头（`status`/`id`/`revision`/`screen`/`foreground`），但**关于树**
  只有 `observation.tree.format` 与 `observation.tree.base`，无法据此选择任何一个 `ref`。
- `inspect` 的正文有 `observation_id`/`revision`/`scope`/`total`/`truncated`/`match_count`
  （[render.go:190-211](../../internal/mcp/render.go#L190)），但**没有任何命中内容**。
- 直接后果：在只把 `TextContent` 交给模型的宿主里，`act` 的 `target.ref` 无从选择，只能
  盲试坐标；`inspect` 的命中内容完全不可见。
- 这不是“体验差”，而是**工具在特定宿主上不可用**；且不可用与否取决于宿主实现，服务端
  无法感知。

### 1.2 证据链

1. **规范要求**：MCP 工具规范规定“返回 structured content 的工具 SHOULD 同时在 `TextContent`
   中返回序列化结果”（[Tools · Structured Content](https://modelcontextprotocol.io/specification/draft/server/tools#structured-content)）。
2. **宿主现实**：部分框架把 `structuredContent` 当应用侧数据，不交给模型
   （[LangChain 论坛讨论](https://forum.langchain.com/t/why-can-the-model-see-the-structured-content-returned-by-the-mcp-tool/3076)）。
3. **当前实现**：`renderText` 只输出阶段事实，注释明确“never includes … tree nodes”
   （[render.go:13-15](../../internal/mcp/render.go#L13)）；`structuredContent` 才是全量
   （[server.go:129](../../internal/mcp/server.go#L129)）。
4. **文档承诺**：`docs/mcp.md` §3 把可读正文定义为 `key=value` 行，并明确正文不含树
   （[docs/mcp.md:35-44](../../docs/mcp.md#L35)）。
5. **体积**：按 `Node` 的默认省略规则手写一棵 50 节点的 Android 形态合成树估算：紧凑 JSON
   4350 B，行式文本 2236 B，约 **1.95×**，约 45 B/节点（合成样本估算，不是基准）。
6. **delta 语义**：基线只能是 full 树（[capture.go:580-587](../../internal/engine/capture.go#L580)）；
   差异永远相对该基线，不是相对上一次观察（[capture.go:187](../../internal/engine/capture.go#L187)）；
   五个块是对最终状态的原子声明（[apply.go:20-25](../../internal/observation/apply.go#L20)）。
7. **inspect 语义**：只读、不采集、不认领（[inspect.go:40-43](../../internal/engine/inspect.go#L40)）；
   对 delta 观察返回解析后的完整投影树（[engine.go:879-884](../../internal/engine/engine.go#L879)、
   [inspect.go:89-96](../../internal/engine/inspect.go#L89)）。

### 1.3 为什么直觉会出错

- **“把结构化 JSON 直接塞进 `TextContent`”**：规范允许，但对模型是纯噪声——重复的键名
  （`"ref"/"role"/"bounds"`）比节点内容还长；估算文本小一半（§1.2 第 5 条）。而且两份同构
  内容没有增量价值。
- **“把 delta 展开成整树再渲染”**：适配器是无状态投影层（[server.go:75-80](../../internal/mcp/server.go#L75)），
  手里只有本次 `Result`，**没有 base 树**。展开要么再发一次 `inspect`（多一次往返，且需要
  自己实现 `Apply`），要么在适配器存第二份页面事实。两条都越界。
- **“只改 `inspect`”**：主循环（`observe`/`act`）仍然看不见页面，等于止血贴贴在小腿上。
- **“把 `structuredContent.observation.tree` 换成字符串”**：破坏与 HTTP 同形的 `Result`
  （[docs/mcp.md:38](../../docs/mcp.md#L38)）、`protocol.Node` 的类型解码与所有机器消费方，
  且模型该读的文本仍要单独渲染。
- **“给 delta 的 `~` 行加 role 以便识别节点”**：`Delta.Set` 是 `map[ref]SetNode`，
  `SetNode` 没有 `role`（[node.go:587-600](../../internal/protocol/node.go#L587)）；适配器没有
  基线树，**拿不到这个字段**。要加只能改 `/api/*` 契约（见 §9.1）。

## 2. 目标与非目标

### 2.1 目标

| 编号 | 目标（可判定） |
| --- | --- |
| G1 | 每个带观察或补看结果的工具结果，其 `TextContent` 恰好含一个树块（无树时为 0 个） |
| G2 | 树块是 `Result` 与编译期常量的纯函数：同一输入渲染两次逐字节相同 |
| G3 | 文本可被测试用解析器还原为与 `structuredContent` 等价的集合（§4.1 定义比较口径） |
| G4 | 超出文本预算时整块省略并输出 `omitted` 行；任何时候都不输出半棵树 |
| G5 | 非省略块以 `end tree` / `end inspect` 结尾；缺失即表示宿主截断 |
| G6 | 工具入参 schema、`structuredContent`、`/api/*` 字段与 `contract_version` 均不变 |
| G7 | 计数自洽：full/inspect 的 `nodes=` 等于正文节点行数；delta 的 `changes=` 等于块声明数 |

### 2.2 非目标

- 不在 HTTP 服务、SDK 或控制端渲染任何文本树；
- 不新增 MCP 工具、不新增入参字段、不新增 `outputSchema`；
- 不在适配器里实现 `Apply`、不缓存基线树、不做跨调用状态；
- 不改变 full/delta 的选择策略、预算或引擎行为；
- 不把文本块设计成可被程序解析的协议（不提供解析器、不做版本协商）；
- 不处理图片字节、截图或 shell 输出。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 文档要求宿主渲染 `structuredContent` | 0 | 文本型宿主永远看不见树，主循环不可用 |
| R2 只改 `inspect` | 仅补看块 | 小，无 schema 变更 | `observe`/`act` 仍不可用 |
| R3 全量追加（选定） | 观察 full/delta + 补看三型都渲染，追加在同一 `TextContent` | 契约文档改写、双表示漂移风险、每步文本变大 | 模型误读累计语义；宿主截断 |
| R4 适配器解析 delta | 用 `inspect` 取基线并把 delta 展开成整树 | 每步多一次 HTTP；需实现 `Apply`；需缓存或二次读取 | 适配器持有第二份页面事实；延迟与失败面增加 |
| R5 替换结构化字段 | `observation.tree` 改成字符串 | 高 | 破坏 HTTP/MCP 同形、类型解码与机器消费 |

**选 R3。** R1 不解决可用性；R2 只覆盖冷路径；R4 把无状态投影层变成有状态层，且每步
O(节点数) token，等于在文本通道放弃 delta 的全部收益；R5 是双输。

**R4 的触发条件**（写清是为了以后不必重新论证）：若实机采样显示模型对“累计 delta”的误读
导致 `target_changed`/错误点击率显著上升，再评估 R4；届时它是渲染器输入从 delta 换成 full
树的局部改动，不影响本提案的语法与预算。

## 4. 判据

### 4.1 进判据（比较口径）

| 判据 | 定义 |
| --- | --- |
| 块存在性 | `result.Observation.Tree != nil` → 观察块；`result.Inspection != nil` → 补看块；二者互斥且至多一个 |
| 完整性 | 非省略块以 `end tree` / `end inspect` 结尾 |
| 计数自洽 | full：正文节点行数 == `nodes=`；inspect：所有块节点行数之和 == `nodes=`；delta：`len(set)+len(add)+len(remove)+len(children)+len(replace)` == `changes=` |
| 一致性（full） | 测试解析器还原的节点序列（含顺序、role、bounds、label、value、hint、actions、全部 flag）与 `Tree.Full.Root` 逐字段相等 |
| 一致性（delta） | 解析器还原的 `set` 字段值、`add` 子树、`remove` 列表、`children` 映射、`replace` 子树，与 `protocol.Delta` 逐字段相等；`add`/`remove` 顺序一致 |
| 一致性（inspect） | 每个 `match i/N` 的 `path` 与子树，与 `InspectionView.Matches[i]` 的 `Path`/`Node` 相等；`matches`/`total`/`truncated` 与视图字段相等 |
| 纯函数 | 同一 `Result` 两次渲染字节相同；不读取请求参数、时间、随机数、map 迭代顺序 |
| 预算 | 块总字节（含块头、正文、终止行）≤ 上限；超出则输出预算 `omitted` 行，正文为空（§5.5；`reason=ref_format` 的兜底省略不受此判据约束） |
| 转义 | 所有设备来源字符串以 JSON 字符串字面量形式出现；输出不含裸控制字符，不含 CR |
| 确定性排序 | `set`/`children`/`replace` 按 ref 排序；`add`/`remove` 保持切片顺序；节点按树序 |

### 4.2 不进判据

| 项 | 原因 |
| --- | --- |
| `origin`、`session`、`cleanup`、`image`、`recovery` | 已有 `key=value` 行，树块不重复 |
| 请求里的 `query` 原文 | 不在 `Result` 内；写进块头会破坏纯函数（§9.1） |
| 节点在屏幕上的视觉位置/可点击性推断 | 树块只表达协议字段，不做业务推断 |
| 文本的行宽、对齐 | 只保证语法与确定性，不做排版美化 |

### 4.3 与既有契约的关系

**只增不减**。`structuredContent` 一字不改，机器消费方不受影响；`TextContent` 从“只有阶段
事实”变成“阶段事实 + 树块”，属于投影内容扩展，不是契约版本变化。工具入参 schema 不变，
因此不需要 `contract_version` 迁移。工具 description 会变长，这会改变
`CatalogFingerprint`（[catalog.go:29-33](../../internal/mcp/catalog.go#L29)），发布清单在下次
发布时重新生成，见 §6.5。

## 5. 算法

### 5.1 总流程

```text
handle(result, cancelled, cancellation, admission):
    if admission:
        text = "invalid_request: …"            # 既有，不变；准入结果没有树/补看内容
    else:
        text = renderText(result, cancelled, cancellation)    # 既有，不变
        block = renderTreeText(result)                        # 新增，纯函数
        if block != "":
            if text != "":
                text = trimRight(text, "\n") + "\n\n"
            text += block
    content = [TextContent{text}]
    if image != nil: content += ImageContent
```

块与 `key=value` 正文之间恰好一个空行。块自身以 `\n` 结尾，无行尾空格，无 `\r`。
渲染在结果返回前同步完成，没有超时、重试或撤销分支：取消结果若无树/补看内容，直接不输出块。

### 5.2 观察块：full

```text
tree v1 format=full id=O3 scope=reported_visible_with_context nodes=7

n1 root [0,0,1080,2400]
  n2 group [0,0,1080,220]
    n3 button [24,60,144,180] "返回" tap
    n4 text [300,80,780,160] "设置"
  n5 scroll [0,220,1080,2400] axis=y
    n12 group -
      n13 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
end tree
```

节点行语法（固定列序）：

```text
node-line := indent ref-token SP role SP bounds [SP label]… [SP value] [SP hint] [SP action]… [SP flag]…
indent    := 2×depth 个空格
ref-token := "n" nonzero 0*DIGIT              # nonzero := "1" … "9"；无前导零，见 §5.7
bounds    := "[" l "," t "," r "," b "]" | "-"
label     := JSON 字符串字面量（label 数组按顺序各写一个）
value     := "value=" JSON 字符串
hint      := "hint=" JSON 字符串
action    := tap | long_press | text | swipe
flag      := "enabled=" bool|"null" | "focused=" … | "checked=" … | "axis=" x|y|both |
             "resource=" JSON 字符串 | context
```

字段映射（与 `Node.MarshalJSONTo` 的省略规则一一对应，
[node.go:192-355](../../internal/protocol/node.go#L192)）：

| `Node` 字段 | 文本 | 缺席含义 |
| --- | --- | --- |
| `ref` / `role` | 恒出 | — |
| `bounds` | `[l,t,r,b]` / `-` | 不缺席；`-` 表示 `null` |
| `label[]` | 每个一个 `"…"` | 空数组 |
| `value` / `hint` | `value="…"` / `hint="…"` | 未知（`""` 显式出现） |
| `actions[]` | 裸词 | 无可执行能力 |
| `enabled` | `enabled=false` / `enabled=null` | `true` |
| `focused`/`selected`/`protected` | `k=true` / `k=null` | `false` |
| `checked` | `checked=true/false` | 未知 |
| `axis` | `axis=y` | 未知 |
| `resource` | `resource="…"` | 未知 |
| `context` | 裸词 `context` | 非 context 节点 |
| `children` | 换行 + 缩进 | 叶子 |

空树（虚拟屏只有合成根）是合法输出：`nodes=1`、一行根、`end tree`。

### 5.3 观察块：delta

```text
tree v1 format=delta id=O4 base=O3 scope=reported_visible_with_context changes=6

~ n8 checked=false
~ n7 label=["WLAN 已关闭"] actions=["tap"]
+ n14 group [0,460,1080,580]
  n15 text [48,490,700,550] "移动网络" tap
- n16
children n5 = [n6 n9 n14]
= n12 group [0,1800,1080,2100]
  n91 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
end tree
```

`changes` 定义：**五个块的顶层声明条数之和**——
`len(set) + len(add) + len(remove) + len(children) + len(replace)`。一个 `set` 条目即使改了
多个字段也只计 1。上例为 2+1+1+1+1=6。delta 块没有 `nodes=`。

块顺序固定 **set → add → remove → children → replace**，与 `protocol.Delta` 字段顺序一致
（[node.go:610-618](../../internal/protocol/node.go#L610)）。

**`~` 行语法：**

```text
set-line   := "~" SP ref-token SP set-field (SP set-field)*
set-field  := "label=" (string-array | "null")
            | "actions=" (action-array | "null")
            | "bounds=" (set-bounds | "null")
            | ("enabled"|"focused"|"selected"|"checked"|"protected") "=" ("true"|"false"|"null")
            | ("value"|"hint"|"resource") "=" (string | "null")
            | "axis=" ("x"|"y"|"both"|"null")
set-bounds := "[" l "," t "," r "," b "]"     # set 行不用 "-"；未知一律写 null
string-array := "[" [ string ("," string)* ] "]"
action-array := "[" [ action-string ("," action-string)* ] "]"
action-string := quote action quote
quote        := 单个双引号字符（U+0022）
action       := tap | long_press | text | swipe
string       := JSON 字符串字面量
```

`label=null` / `actions=null` 表示“标签/动作变为未知”。当前 `Diff` 用
`append([]string{}, …)` 只产出非 nil 空切片，因此生产路径给的是 `label=[]`
（[diff.go:444-448](../../internal/observation/diff.go#L444)）；渲染器仍按 `Field` 的线上语义
接受 null，保证与 `protocol.Delta` 逐字段等价。

字段按 `SetNode` 声明顺序出现（label、actions、bounds、enabled、focused、selected、
checked、value、hint、protected、axis、resource，[node.go:587-600](../../internal/protocol/node.go#L587)）。
**只出现实际变化的字段**；每个值都是完整新值，必须保留显式 `false`/`""`/`[]`/`null`
（AGENTS.md 第 7 条）：

| 变化 | 文本示例 |
| --- | --- |
| label 清空 / 变成两个标签 | `label=[]` / `label=["确定","OK"]` |
| label 变成未知（仅手工构造的 delta） | `label=null` |
| actions 清空 / 增加 | `actions=[]` / `actions=["tap","text"]` |
| actions 变成未知（仅手工构造的 delta） | `actions=null` |
| bounds 变成未知 | `bounds=null`（set 行没有 `bounds=-`） |
| enabled 变成 false / 未知 | `enabled=false` / `enabled=null` |
| focused 变成 false | `focused=false` |
| checked 变成 false / 未知 | `checked=false` / `checked=null` |
| value 变成空串 / 未知 | `value=""` / `value=null` |
| axis 变成未知 | `axis=null` |
| resource 变成未知 | `resource=null` |
| 一行多个字段 | `~ n7 label=[] actions=[] enabled=null value=""` |

**已知限制：`~` 行不带 `role`。** `SetNode` 没有该字段（[node.go:587-600](../../internal/protocol/node.go#L587)），
适配器也没有基线树；带上它只能改 `/api/*` 契约。因此 `~` 行的可读性依赖 `base` 在上下文中
（§7.2）。`Label` 是有的，但只在它变化时以 `label=` 出现。

**其余块：**

- `+` / `=`：完整子树，根行带标记，子节点缩进 +2；`+` 的挂接位置由 `children` 行给出；
- `-`：消失的子树根；
- `children <ref> = [<ref> …]`：父节点的完整当前子列表，顺序有意义；
- 空差异：只有块头与 `changes=0`，正文为空，仍以 `end tree` 结尾；
- **累计语义**：同一个 `base` 下的后续 delta 重复包含此前已播报的变化。新 delta **覆盖**
  旧 delta，不得叠加。块头的 `base=` 是唯一锚点，它可能比模型最近一次 full 更旧。

### 5.4 补看块：inspect

**整树：**

```text
inspect v1 scope=tree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context nodes=3

n1 root [0,0,1080,2400]
  n5 scroll [0,220,1080,2400] axis=y
    n7 button [40,600,240,680] "确定" tap
end inspect
```

**子树：**

```text
inspect v1 scope=subtree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context root=n5 nodes=2

n5 scroll [0,220,1080,2400] axis=y
  n7 button [40,600,240,680] "确定" tap
end inspect
```

**查询：**

```text
inspect v1 scope=query observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context matches=2 total=3 truncated=true nodes=4

match 1/2 path=[n1 n5]
n7 button [40,600,240,680] "确定" tap

match 2/2 path=[n1 n6]
n9 group [0,700,1080,900]
  n10 text [40,720,600,780] "确定"
  n11 button [40,800,240,880] "取消" tap
end inspect
```

- `scope=` 是补看模式（`inspection.scope`）；`tree_scope=` 是树的可见性口径
  （`inspection.tree.scope`；子树/查询用协议常量，[node.go:496](../../internal/protocol/node.go#L496)）。
  两个“scope”不合并，避免语义混淆；
- `observation=`、`revision=`、`captured_at=` 来自 `InspectionView`；
- `root=` 仅子树；`matches=`/`total=`/`truncated=` 仅查询；`matches` 是**实际返回条数**，
  `total` 是预算前命中数；
- `match i/N` 的 N 是本次返回条数；`path=[…]` 是 root..parent（不含命中节点，
  [inspect.go:175-193](../../internal/engine/inspect.go#L175)）；
- 命中允许嵌套，父命中已含子命中时仍会各自出现一次（与 JSON 一致，不去重）；
- 根节点没有 `label`/`value`/`hint`/`resource`，因此 `path=[]` 在实践中不会出现；
  渲染器遇到空路径仍输出 `path=[]`（忠实于视图，不伪造祖先）；
- `nodes=` 是正文节点行数之和；嵌套命中重复计数（与文本行数一致）；
- 块头**不含请求里的 `query` 原文**（§9.1）。

### 5.5 预算

| 块类型 | 上限（块头 + 正文 + 终止行） | 超限行为 |
| --- | --- | --- |
| 观察块（full/delta） | 24 KiB | `tree v1 omitted …`，无正文、无终止行 |
| 补看块（三种 scope） | 32 KiB（整块上限，不含 `key=value` 正文与 `structuredContent`） | `inspect v1 omitted …`，无正文、无终止行 |

**预算省略行**（`bytes` 必须大于 `limit`；`use=` 是固定建议，由块类型决定，与内容无关）：

```text
tree v1 omitted format=full id=O3 nodes=612 bytes=27112 limit=24576 use=inspect
tree v1 omitted format=delta id=O4 base=O3 changes=120 bytes=24880 limit=24576 use=inspect
inspect v1 omitted scope=tree observation=O2 revision=4 nodes=612 bytes=38211 limit=32768 use=query
```

计数口径：UTF-8 字节。`bytes` 是本可渲染块的总字节；`nodes` 是正文本应包含的节点行数；
`changes` 同 §5.3。渲染器先写入受上限约束的缓冲区，溢出后继续计数但不保留内容，最后只输出
省略行。

三种“不完整”互不替代：

| 现象 | 含义 | 触发者 | 文本表现 |
| --- | --- | --- | --- |
| `truncated=true` | 引擎按 32 KiB 总预算截断了命中**条数**，返回的每条仍完整 | 引擎（query） | 块头字段 |
| `omitted` | 块超过文本上限，整块未渲染 | 渲染器 | `omitted` 行，无正文、无终止行 |
| 缺 `end tree` / `end inspect` | 输出在宿主侧被切断 | 宿主 | 正文可能停在任意行 |

`reason=ref_format` 的省略行是**契约违背兜底**，不受预算约束，因此不带 `bytes`/`limit`/`use`。

为什么补看比观察宽：补看是冷路径，且是观察块被省略后的恢复手段。取同一上限只是把
“观察说 omitted、补看也说 omitted”的死角从 >24 KiB 推到 >32 KiB；超过 32 KiB 时两者
仍会同时省略，只能靠 `query`/`node` 收窄。

为什么不是照搬引擎的 128 KiB：128 KiB JSON ≈ 60 KiB 文本 ≈ 2 万 token，已贴近常见宿主的
单次工具输出上限。被宿主**静默截断**比我们自己**显式省略**危险得多。

24 KiB 按 45 B/节点估算约覆盖 540 个节点；实际 Android 树含 `resource` 时会更高，覆盖数更少。

### 5.6 失败与边界

| 情况 | 行为 |
| --- | --- |
| `Observation.Tree == nil`（`status=failed`、采集失败） | 不输出块 |
| `Result.Inspection == nil` 且 `Result.Observation == nil`（含准入错误、取消且无结果） | 不输出块；准入错误仍有带 `Error` 的 `Result`，判据是“无树/补看”而不是“无 Result” |
| `Inspection.Tree/Root/Matches` 全空 | 只输出块头与终止行（`nodes=0`） |
| 非法 UTF-8 | 该字节序列替换为 U+FFFD 后按 JSON 转义 |
| 控制字符 | 转义为 `\n`、`\t`、`\r` 或 `\u00XX` |
| 超深树 | 渲染器用显式栈迭代遍历（不递归），避免栈溢出；深度不设业务上限，由字节预算兜底 |
| ref 不匹配 `^n[1-9][0-9]*$`（§5.7） | 整块省略：`tree v1 omitted reason=ref_format id=… [base=…]` / `inspect v1 omitted reason=ref_format observation=… scope=…`（不受预算约束，不带 `bytes`/`limit`/`use`） |
| 空差异 delta | `changes=0`，正文为空，保留终止行 |

### 5.7 转义、ref 与排序

**转义**：渲染器自带转义函数，不依赖 `encoding/json` 的 HTML 转义行为——
`"` → `\"`，`\` → `\\`，`\n`/`\t`/`\r` 用短转义，其余 `< 0x20` 用 `\u00XX`，非法 UTF-8
替换为 U+FFFD；`<`、`>`、`&` **保持原样**（不转成 `\u003c`）。这样 golden 字节与 Go 版本、
`GOEXPERIMENT` 无关。

**ref**：公开 ref 由 observation builder 单调分配为 `n1`、`n2`…（`observation/doc.go`），
文本按原样输出，不加引号。渲染前统一校验块内**所有** ref 都匹配 `^n[1-9][0-9]*$`
（builder 从 `n1` 起分配、无前导零，因此数值唯一，不需要并列 tie-break）；
只要有一个不匹配，整块省略并标 `reason=ref_format`（服务端契约违背，不静默产出可能撕裂
语法的文本）：

- full：根与全部节点；
- delta：`set` 的 key、`add` 子树的全部节点、`remove` 元素、`children` 的 key 与全部 value、
  `replace` 的 key 与其子树全部节点；
- inspect：树/子树/命中子树的全部节点，以及 `path` 的每个元素。

**排序**：`set`、`children`、`replace` 是 Go map，按 ref 排序后再输出：解析 `n` 后的十进制
数值并按升序排列（ref 唯一，不存在同值；形态已由上面的校验保证，不再保留字典序回退）。
`add` 与 `remove` 保持 `protocol.Delta` 切片顺序，不重排；节点始终按树序（`Node.Children`
顺序），不排序。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/mcp/tree_text.go`（新增） | `renderTreeText(*protocol.Result) string`；full/delta/inspect 三个写入器；显式栈遍历；自带 JSON 转义；ref 校验与排序；预算缓冲 |
| `internal/mcp/server.go` | `handle` 在 `renderText` 之后追加块（§5.1） |
| `internal/mcp/render.go` | 更新文件头注释：正文现在可以包含树块；`renderText` 本身不改 |
| `internal/mcp/schema.go` | `start`、`observe`、`act`、`open_app`、`inspect` 五个 description 追加同一段语法图例与使用规则 |
| `internal/mcp/tree_text_test.go`（新增） | 见 §8.1 |
| `internal/mcp/server_test.go` | 端到端断言；**把 [server_test.go:508](../../internal/mcp/server_test.go#L508) 的 `requireTestNotContains(text, "n1")` 改为断言含根节点行** |
| `scripts/mcp-probe/checks.go` | 新增 observe/act 文本块断言，并新增一次 `inspect` 调用断言补看块（当前 `runChecks` 不调用 inspect） |
| `docs/mcp.md` | §3 重写正文定义，新增 §3.1 树块语法与预算 |
| `docs/interface.md` | §12 补两处补看预算口径（§6.5） |
| `maintenance/decisions.md` | 新增 D32：MCP 文本树投影 |

`scripts/mcp-probe/precovery.go` **不改**：p-recovery 模式直接用 HTTP 客户端，不建立 MCP 会话，
拿不到 `TextContent`。

### 6.2 配置与取值

| 常量 | 值 | 依据 | 谁可改 |
| --- | --- | --- | --- |
| `maxObservationTreeTextBytes` | `24 * 1024` | 约 540 节点（45 B/节点估算）；低于常见宿主单次输出上限 | 代码常量，随提案修订 |
| `maxInspectionTextBytes` | `32 * 1024` | 补看是冷路径与恢复手段，略宽于观察块 | 同上 |

不新增配置项、不开放每次调用的格式化/裁剪参数（沿用 D20 的“固定策略、不开放旋钮”取向）。

### 6.3 日志与可观测性

- 不新增日志事件；树块只进响应，不进 `eventlog`（AGENTS.md 第 10 条）；
- 现有 `mcp.tool` 事件继续只记 `tool`、`duration_ms`、`is_error`、`code`；
- 省略与截断不单独记日志：`omitted` 已在响应中可见，日志记树大小会违反白名单。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威（第 1 条） | 无影响：适配器只投影本次 `Result`，不新增状态 |
| 单向消费（第 3 条） | 无影响：不认领、不改变 O/P 状态 |
| 三态与省略（第 7 条） | 文本必须保留显式 `false`/`0`/`""`/`[]`/`null`，不得用“缺席”混淆 |
| 事实分层（第 8 条） | 树块是既有事实的呈现，不产生新事实，不改写 `outcome` |
| 日志白名单（第 10 条） | 树与标签正文不得进日志 |
| 文档边界（第 11 条） | 语法与预算写入 `docs/mcp.md`；决策写入 `maintenance/decisions.md` |
| 文档审计（第 15 条） | 实现提交前对涉及的 `docs/` 做独立只读审计 |

### 6.5 契约与文档影响

1. `docs/mcp.md` §3：把“可读正文 = `key=value` 行”改为“阶段事实 + 可选树块”，并新增
   §3.1（节点行、delta 五块与 `~` 字段、inspect 三 scope、预算、三种“不完整”）。
2. `docs/interface.md` §12 **补两处现有行为的口径**（不改行为）：
   - 查询的**总量**也受 32 KiB 限制，不只是“单个命中”
     （[inspect.go:152-159](../../internal/engine/inspect.go#L152)）；
   - 任一命中超 32 KiB 会让整次查询失败，即使它不在返回前缀内
     （[inspect.go:140-150](../../internal/engine/inspect.go#L140)）。
   整树 128 KiB 与代码默认值一致（[engine.go:114](../../internal/engine/engine.go#L114)），
   该项不改；生产路径未开放外部配置（[main.go:77-79](../../cmd/device-service/main.go#L77)）。
3. `maintenance/decisions.md` D32：记录本次取舍，并**明写扩展 D21 的“可读 `key=value` 正文”
   条款**（[decisions.md:208](../../maintenance/decisions.md#L208)），避免 D21 变为过期表述。
4. 五个工具的 description 变长会改变 `CatalogFingerprint`
   （[catalog.go:29-33](../../internal/mcp/catalog.go#L29)）；`build/release/manifest.json`
   在下次发布时由 `scripts/build-release.ps1` 重新生成，本提案不手改清单。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 只渲染 `TextContent` 的宿主里，模型第一次能看见页面、选择 `ref`、核对动作结果；
- `inspect` 的整树/子树/查询内容在文本中可读；**未省略时**与结构化内容同源同量；
- delta 仍保持 O(变化) 的文本成本，不必每步重发整树；
- 超预算行为从“静默缺失”变成“显式 `omitted` + 固定 `use=` 建议”。

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| 宿主仍可能截断输出 | 适配器无法控制宿主；只能靠缺失终止行暴露 |
| 模型仍可能把累计 delta 读成“刚刚又变了” | 文本只能给 `base=` 与描述规则，不能强制理解 |
| `~` 行里看不出节点是什么 | `SetNode` 没有 role；未变化的 label 不出现 |
| 超过约 540 节点的页面，观察块会整块省略 | 固定 24 KiB 上限；需用 `inspect` + `query` 收窄 |
| 补看无法给出子树的祖先路径 | `InspectionView.Root` 只有节点，没有 `path` |
| 文本不能被程序可靠解析 | 本提案不提供解析器、不做格式协商 |
| 图片仍可能对纯文本宿主不可见 | 图片走独立 `ImageContent`，不在本提案范围 |

### 7.3 成本

| 项 | 量级 |
| --- | --- |
| full 块 | 约 45 B/节点（合成样本）；50 节点约 2.2 KiB，24 KiB 约 540 节点 |
| delta 块 | 每条约 40–80 B；典型单字段变化 < 100 B |
| 文本上限 | 观察 24 KiB；补看 32 KiB |
| 适配器 CPU | 一次树遍历 O(n)，无 I/O、无锁；成本由调用方的上下文承担 |
| 实现面 | 新增渲染器约 300 行 + 测试；五个 description；文档两处 |

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| `TestTreeTextFullGolden` | 固定 `Result` 的完整字节比对（含块头、缩进、终止行、`nodes=7`） |
| `TestTreeTextDeltaGolden` | 五块齐全的固定字节比对（`changes=6`） |
| `TestTreeTextDeltaExplicitValues` | 12 个 `SetNode` 字段的显式 `false`/`""`/`[]`/`null` 全部可表达，含 `label=null`、`actions=null` |
| `TestTreeTextInspectTree/Subtree/Query` | 三种 scope 的块头与正文；`path`、`matches/total/truncated` |
| `TestTreeTextIsPure` | 同一 `Result` 渲染 100 次字节相同；经 JSON 往返后仍相同 |
| `TestTreeTextRoundTrip` | 测试解析器还原 full/delta/inspect，与结构化内容逐字段相等（§4.1 口径） |
| `TestTreeTextCounts` | full/inspect 的 `nodes=`、delta 的 `changes=` 与正文一致 |
| `TestTreeTextEscapes` | `"`、`\`、换行、制表、`=`、`[`、`]`、`<`、`>`、`&`、CJK、非法 UTF-8 |
| `TestTreeTextOmitsWholeBlock` | full/delta/inspect 三种超预算都输出 `omitted`（`bytes>limit`），无节点行、无终止行 |
| `TestTreeTextRejectsMalformedRef` | 节点、`set` key、`children` key、`path` 元素各含空格/换行，以及前导零 `n01` → `reason=ref_format`，不产生正文 |
| `TestTreeTextTrailer` | 非省略块均以终止行结尾 |
| `TestTreeTextEmptyTree` | 只有合成根时 `nodes=1` |
| `TestTreeTextNoTree` | `status=failed`、取消且无结果、准入错误三种情况都不输出块 |
| `TestTextCarriesTreeBlock`（端到端） | `start`/`observe`/`act`/`open_app`/`inspect` 的 `TextContent` 含块；`structuredContent` 不变 |

反向对照（去掉实现必须失败）：

1. 删除 `handle` 中的 `renderTreeText` 调用 → `TestTextCarriesTreeBlock` 失败；
2. 删除预算检查 → `TestTreeTextOmitsWholeBlock` 失败；
3. 删除终止行 → `TestTreeTextTrailer` 失败；
4. 删除 ref 校验 → `TestTreeTextRejectsMalformedRef` 失败。

（另设变异用例“节点写入少写一个子节点”，用于证明 `TestTreeTextRoundTrip` 能发现内容缺失；
它不是必须的反向对照，只是加强。）

### 8.2 实机/系统级

| 项 | 口径 | 状态 |
| --- | --- | --- |
| 真实 MCP 宿主调用 `observe`/`act`/`inspect` | 文本含块且终止行存在；`structuredContent` 与改动前一致；记录宿主名称、版本、传输方式（stdio/HTTP） | 未执行 |
| token 对比 | 同一屏 full 块与旧文本的字符数，按“字符数/3.5”与宿主 tokenizer 两种口径各记一次 | 未执行 |
| 适配器省略 | 构造 > 24 KiB 的树，断言输出 `omitted` 且无终止行 | 未执行 |
| 引擎截断 | 构造超过 32 KiB 总预算的 query 命中集，断言文本块头为 `truncated=true` 且正文只含完整命中 | 单元测试覆盖；实机未执行 |
| 宿主截断 | 构造**略低于** 24 KiB 的块（如 23 KiB），在单次工具输出上限更小的宿主/测试客户端上运行，断言缺少终止行且**没有** `omitted` 行 | 未执行 |
| probe 回归 | `scripts/mcp-probe` 全绿，新增块断言通过 | 未执行 |

### 8.3 记录

写入 `maintenance/verification/mcp-tree-text.md`，注明实现提交、环境、命令与真实结果；
实机未执行项逐条标注“未执行”。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 载体 | 追加在同一 `TextContent`，空行分隔；不开第二个 content block |
| 替换还是追加 | 追加。`structuredContent` 一字不改 |
| delta 是否展开 | 不展开；适配器无 base 树，展开需要越界的状态或额外往返 |
| delta 锚点 | 块头 `base=`；新 delta 覆盖旧 delta，不叠加；累计语义写进工具 description |
| `~` 行内容 | 只列变化字段；**不带 role**；12 个字段的显式值语法见 §5.3 |
| 字段顺序 | ref → role → bounds → label… → value → hint → action… → flag… |
| 缩进 | 2 空格/层；`match` 下的子树不额外缩进 |
| 终止行 | `end tree` / `end inspect`；缺失即视为宿主截断 |
| 预算 | 观察 24 KiB、补看 32 KiB；超限整块省略，不截断 |
| `query` 原文 | 不进块头：只在请求参数里，写进去会破坏“文本是 `Result` 的纯函数” |
| 工具 | `observe`/`act`/`start`/`open_app`/`inspect`；`open_app` 的强制 full 照常渲染 |
| schema | 不新增参数；五个工具 description 追加同一段图例；`contract_version` 不变 |

块头字段全集（`[]` 表示按 scope 出现）：

| 块 | 字段 |
| --- | --- |
| full | `tree v1 format=full id=<O> scope=<tree scope> nodes=<n>` |
| delta | `tree v1 format=delta id=<O> base=<O> scope=<tree scope> changes=<n>` |
| inspect | `inspect v1 scope=<tree\|subtree\|query> observation=<O> revision=<n> captured_at=<RFC3339> tree_scope=<tree scope> [root=<ref>] [matches=<n> total=<n> truncated=<bool>] nodes=<n>` |
| 省略（观察） | `tree v1 omitted format=<full\|delta> id=<O> [base=<O>] [nodes=<n>\|changes=<n>] bytes=<n> limit=<n> use=inspect` |
| 省略（补看） | `inspect v1 omitted scope=<mode> observation=<O> revision=<n> nodes=<n> bytes=<n> limit=<n> use=query` |
| 非法 ref | `tree v1 omitted reason=ref_format id=<O> [base=<O>]` / `inspect v1 omitted reason=ref_format observation=<O> scope=<mode>` |

### 9.2 未决

| 事项 | 谁定 | 何时 |
| --- | --- | --- |
| 宿主实际截断阈值与 24/32 KiB 取值是否合适 | 实现者（实机/宿主采样） | §8.2 执行时；超限则调常量并记入验证记录 |
| 是否因误读率升级为 R4（解析 delta 为整树） | 项目负责人 | 实机采样出现显著 `target_changed`/错误点击后 |
| MCP `query` 的空串 schema 缺口（引擎 422、schema 允许） | 另行提案 | 不在本提案范围 |

## 10. 实施顺序

1. `tree_text.go`：自带转义、ref 校验、排序、预算缓冲、full 写入器（显式栈）；
   `TestTreeTextFullGolden`、`TestTreeTextEscapes`、`TestTreeTextRejectsMalformedRef`。
2. delta 写入器（五块、`~` 全字段、空差异、`changes=`）；`TestTreeTextDeltaGolden`、
   `TestTreeTextDeltaExplicitValues`。
3. inspect 写入器（三 scope、`path`、`matches/total/truncated`）；三组 golden 与
   `TestTreeTextRoundTrip`。
4. `server.go` 接线；端到端 `TestTextCarriesTreeBlock`；更新
   [server_test.go:508](../../internal/mcp/server_test.go#L508)；三种超预算用例；反向对照 1。
5. `schema.go` 五个 description；`catalog_test.go` 全绿（指纹只校验长度与确定性）。
6. `scripts/mcp-probe/checks.go`：observe/act 块断言 + 新增 inspect 调用。
7. `docs/mcp.md`、`docs/interface.md`、`decisions.md` D32。
8. 全量回归 `GOEXPERIMENT=jsonv2 go test ./...` → 独立只读文档审计（AGENTS.md 第 15 条）
   → 修正 → 实现提交（提交说明注明审计结论）。
9. 实机验证 → `maintenance/verification/mcp-tree-text.md`；发布时重新生成清单。

步骤 1–3 可并行于 5–7 的文档草稿；4 依赖 1–3；8 依赖 7。

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/mcp-tree-text.md`
（本节在归档移动之后补写，届时文件在 `implemented/` 下，故链接比新建时多一层）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- `/api/*` 的请求/响应字段、错误码与 `contract_version`；
- MCP 工具名、数量、入参 schema、`isError` 判据；
- `structuredContent` 的形状与内容；
- full/delta 的选择策略、`MaxFullTreeBytes`、`inspectionUnitBytes`；
- 引擎的会话、观察、工作位、STOP 与恢复语义；
- HTTP 客户端、控制 App、账本；
- 图片字节的载体与 `ImageContent` 的位置；
- 日志事件与字段白名单。
