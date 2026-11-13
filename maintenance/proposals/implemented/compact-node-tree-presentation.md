# 提案：统一压缩公共节点树并按交付表示计算预算

状态：已实现（实现提交 `4fc86cf26c40be1df099a1ea259c99ca7a8d5abc`，2026-11-13）。
实现与验证记录：[验证记录](../../verification/compact-node-tree-presentation.md)。
基线提交：`25db973434de859a5992a6da4b9124e9870cfb14`（分支 `master`，2026-11-13）。
适用范围：`start`、`observe`、`act`、`app` 变更分支的观察树；`inspect` 的全树、子树与查询；HTTP、Go 客户端及 MCP 的 `text` / `structured` / `both`；手机与 Windows 主机配套发布。
影响范围：`internal/protocol`、`internal/observation`、`internal/engine`、`internal/client`、`internal/mcp` 及对应测试；`scripts/mcp-probe`；版本源与生成文件；相关正式文档及发布验证记录。
契约（`/api/*` 字段与 `contract_version`）：改变公共树/补看响应的形状、默认值、几何省略与差分表示；请求字段不增加。发行号 `0.11.0`、共同契约号 `11`、Android versionCode `13`。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对，重点为 [树构造](../../../internal/observation/build.go)、[树差分](../../../internal/observation/diff.go)、[采集与提交](../../../internal/engine/capture.go)、[补看](../../../internal/engine/inspect.go)、[公共节点编码](../../../internal/protocol/node.go)、[MCP 树文本](../../../internal/mcp/tree_text.go)、[版本源](../../../release/versions.json)与[发布规范](../../../docs/release.md)。

## 0. 摘要

现行公开树反复输出匿名容器、矩形与 `checked=false`，增加两种 MCP 编码的字节与上下文占用；现行大小门禁又在 MCP 排版前按旧完整树计费。
选择在引擎中生成一份共同公共阅读结果：观察使用 compact，既有 `inspect` 固定提供 detailed；两种 MCP 模式严格编码同一结果。
完整节点声明 `checked=false` 默认值；匿名无动作普通 view/group 省略已知几何；连续匿名单子链使用保留全部 ref 的可逆编码。
公共 delta 在展开的精简逻辑树上比较，内部完整快照继续承担保号、输入、坐标重验与稳定性判断；公共预算和内部资源分开计算。
本次包含版本升级、手机整组发布及主机二进制替换后的实际进程/目录验证；当前仅起草提案，不修改程序、版本源或部署设备。

## 1. 问题

### 1.1 现象与代价

1. 用户提供的一次 full 结果声明 216 个节点，包含多段连续匿名 view，重复矩形与已知 `checked=false`。原始响应、节点树、应用内容与截图不写入本提案或仓库；本提案只使用合成示例。
2. 非可操作节点不等于无信息：文本、图片、状态与父子分组仍帮助解释界面；无动作节点的几何还可能参与坐标命中。因此不能直接删除所有无动作节点。
3. 当前 `tree_too_large` 在旧完整公开树 JSON 超 128 KiB 时产生。仅改 MCP 排版不能让这道服务门禁受益。
4. 补看以旧 JSON 计单元和总预算；full/delta 按旧编码大小比较；delta 的 replace 决策另用固定节点成本估计，均需要核对计费对象。
5. 尚未完整转换或测量用户样本，不声明已节省的字节、token、CPU 或采集时间比例。

分诊为**取舍**：改变公开表示、默认值、预算判据及发布契约，不将现有行为描述为实现偏离规范的缺陷。讨论已经在本会话完成，未跳过讨论与提案。

### 1.2 证据链

| 环节 | 基线证据 | 对设计的约束 |
| --- | --- | --- |
| 发布节点 | [build.go:214–221](../../../internal/observation/build.go#L214-L221)、[264–270](../../../internal/observation/build.go#L264-L270) | 属性存在或有保留子节点即可保留可见 view，不能凭角色判断它可删 |
| checked 来源 | [build.go:362–379](../../../internal/observation/build.go#L362-L379) | 普通角色也公开源提供的 checked，false 是事实 |
| 完整 Node 编码 | [node.go:123–145](../../../internal/protocol/node.go#L123-L145)、[290–297](../../../internal/protocol/node.go#L290-L297) | 旧 checked 默认未知；新默认值须在共同 DTO 编解码中定义 |
| delta presence | [node.go:558–600](../../../internal/protocol/node.go#L558-L600) | set 省略为沿用基线，显式 false/null/空数组必须保留 |
| 差分守恒 | [diff.go:20–53](../../../internal/observation/diff.go#L20-L53)、[apply.go:102–176](../../../internal/observation/apply.go#L102-L176) | 先还原逻辑节点再应用差分；replace 的键与定义根须一致 |
| 原完整树门禁 | [capture.go:402–417](../../../internal/engine/capture.go#L402-L417) | 需要在新公共 full 编码之后检查交付预算 |
| full/delta 选择 | [capture.go:250–264](../../../internal/engine/capture.go#L250-L264) | 同一基线、同一公共编码比较大小，70% 是严格小于 |
| replace 成本 | [diff.go:344–396](../../../internal/observation/diff.go#L344-L396) | 旧固定节点权重不能代表新 chain/defaults 的真实成本 |
| 坐标与输入 | [point.go:54–80](../../../internal/engine/point.go#L54-L80)、[act.go:246–275](../../../internal/engine/act.go#L246-L275)、[394–425](../../../internal/engine/act.go#L394-L425) | 最深节点命中及实时重验继续使用完整树；ref 不向祖先晋升 |
| 滚动容器 | [scroll.go:163–168](../../../internal/engine/scroll.go#L163-L168)、[246–283](../../../internal/engine/scroll.go#L246-L283) | 不能合并真实滚动节点或借 chain 改变容器解析 |
| 补看与预算 | [inspect.go:40–42](../../../internal/engine/inspect.go#L40-L42)、[99–171](../../../internal/engine/inspect.go#L99-L171)、[175–207](../../../internal/engine/inspect.go#L175-L207) | 历史只读、不确认基线；查询路径含所有原 ref；任一大命中会令查询失败 |
| 内部共享引用 | [engine.go:383–412](../../../internal/engine/engine.go#L383-L412)、[capture.go:625–632](../../../internal/engine/capture.go#L625-L632) | 不能原地修改共享树以生成 compact 或 detailed |
| 构造器隐性持有 | [build.go:136–147](../../../internal/observation/build.go#L136-L147) | Build 会推进 prev，预算拒绝/STOP 后必须释放候选并回滚连续性 |
| 稳定性 | [settle.go:109–123](../../../internal/engine/settle.go#L109-L123)、[297–307](../../../internal/engine/settle.go#L297-L307)、[identity.go:8–33](../../../internal/engine/identity.go#L8-L33) | 隐藏公共几何不能让完整运行事实的变化被判稳定 |
| 两种编码等价 | [mcp.md:67–81](../../../docs/mcp.md#L67-L81)、[result_text.go:216–253](../../../internal/mcp/result_text.go#L216-L253) | text 必须可逆还原 structured 的同一 canonical Result；现有 JSON 保真检查不能替代新文本逆解析测试 |
| MCP 渲染边界 | [tree_text.go:17–20](../../../internal/mcp/tree_text.go#L17-L20)、[41–45](../../../internal/mcp/tree_text.go#L41-L45) | 渲染无状态、不展开历史 delta、不另设文本截断门禁 |
| 内部现有限制 | [hierarchy.go:13–18](../../../internal/android/hierarchy.go#L13-L18)、[rpc.go:17–19](../../../internal/android/rpc.go#L17-L19)、[engine.go:125–127](../../../internal/engine/engine.go#L125-L127) | 解析深度 50、节点 20000、RPC 响应 64 MiB、保留观察数 32，与交付预算分别约束 |
| 发布与版本 | [versions.json:2–5](../../../release/versions.json#L2-L5)、[release.md:19–47](../../../docs/release.md#L19-L47)、[128–160](../../../docs/release.md#L128-L160) | 当前 0.10.0/10/12；共同契约升级要求 App/服务/客户端/MCP 配套 |

### 1.3 为什么直觉会出错

- 只在 text 中删字段会破坏模式等价；本方案将公共结果本身精简，structured 同步变化。
- 在 set 中把 false 当默认值省略，会使基线中的 true 留存；默认值只用于完整节点定义。
- 直接从公开拓扑删容器会改变坐标深度、同深歧义、孩子关系与 ref 证据；本方案保留所有逻辑节点。
- 仅过滤旧 delta 的 bounds 无法判断没有在 set 中出现的 actions/label，也补不出“矩形没变但现在需要展示”的转换。
- 将未知与隐藏都表示为 nil，会混同源未取得几何与按策略不交付几何。
- 给旧 128 KiB 门禁换名字但仍在精简前拒绝，无法兑现交付预算收益；只检查压缩后大小又约束不到内部完整快照。

## 2. 目标与非目标

目标：

1. text 与 structured 承载同一公共 Result，规范化后逐成员、值、presence 及数组顺序一致；both 是同一结果的两份编码。
2. 保留原 ref、父子关系、顺序、内容、源能力及三态；只按明确规则省略公共几何、压缩重复表达。
3. compact full 与基于同一 compact 基线的 delta 可以相互校验；chain 拆并不引入新的逻辑变化。
4. 让 128/32 KiB 与 70% 判据按新交付表示生效，内部资源使用独立计费代理与边界。
5. 输入、滚动、保号、质量、STOP、O/P 接纳与迟到提交门禁继续依赖完整运行事实。
6. 复用既有 inspect 提供完整投影几何和逐节点展开，不增加请求参数；完成配套版本与双端发布验证。

非目标：

- 不删除匿名叶或容器，不拼接业务文本，不猜测按钮名称、选中状态、滚动轴或图像语义。
- 不缩写 ref、角色、动作词或 resource，不裁剪长文本或按内容去重。
- 不按模型 tokenizer 决定服务接纳，不增加模型依赖；真实 token 仅作为可选测量指标。
- 不减少设备源采集内容，不降低实时重验与稳定性摘要要求，不改动作时限、租约和采样预算。
- 不改变恢复账本格式、消息、配对、配置字段、固定 JAR/DEX 或其他项目。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 维持现状 | 保留旧 Node、树文本与预算 | 无实现成本 | 冗余和大小门禁问题继续存在 |
| R2 只做文本简写 | MCP 渲染时省略 false/矩形、折链 | 修改集中 | 与 structured 不等价，服务预算不受益，delta 无足够上下文 |
| R3 共同公共阅读树（选择） | 完整内部快照 → 展开公共逻辑树 → 统一公共 wire；两种模式同源 | 新 DTO、编解码、投影差分、独立资源预算与契约发布 | 若隐藏/未知混同、污染共享树或漏计候选，会产生语义或资源错误；本提案逐项约束 |
| R4 裁剪运行树 | 删除匿名节点并重新挂接子节点，输入同时改用裁剪树 | 影响保号、输入与重验的大范围改造 | 改变点命中与身份判据，超出本次呈现目标 |

选择 R3。R2 仅在所有简写都可无损还原旧 Result 时才成立，但匿名 bounds 的有意省略已超出该范围。R4 不作为本提案后续阶段隐含执行。

## 4. 判据

### 4.1 三层表示与所有权

| 层 | 定义 | 使用方 |
| --- | --- | --- |
| T：内部完整快照 | 现有安全投影完整树、Capture、Source、保号连续性；不可变 | 引擎输入、重验、稳定性、内部资源和 detailed 投影 |
| P(T)：公共 compact 逻辑树 | 每个原 ref 仍为独立逻辑节点，保留真实父子关系；几何可为 hidden/unknown/known | 公共 delta 的 Diff/Apply/Equal |
| W(P(T))：公共编码 | 对完整节点省略默认字段并规范折链；HTTP 与 MCP structured 的共同 DTO，text 可逆编码它 | 客户端与 Agent |

新增 `ReadNode` / `ReadTree` / `ReadDelta` 等公共 DTO；不通过直接修改旧 `protocol.Node.MarshalJSONTo` 将隐藏几何或新默认值渗入完整运行模型。

所有阅读投影都是纯函数，不修改 T、Capture、映射或上一帧；任何持有公共 DTO 的组件都不因此获得输入或会话权威。

### 4.2 匿名节点及几何省略

`anonymous_plain(n)` 同时满足：

1. `role` 为 view 或 group；不是 root、context、输入、滚动、图片、文本或勾选类角色。
2. actions 为空；label 为空；value、hint、resource 均不存在；axis 未知/省略。已知空字符串也算字段存在，不在此处消除。
3. enabled 已知 true；focused、selected、protected、checked 均已知 false。任何未知布尔或非默认状态均保留独立节点及几何。

几何按以下顺序投影：

1. 内部 bounds 未知 → 公共 unknown，显式 null；不把源未知归为有意隐藏。
2. bounds 已知且满足 anonymous_plain → compact 隐藏该矩形。
3. 其余情况 → 保留已知矩形。

此规则覆盖匿名 view/group 的容器和叶节点。匿名叶仍保留 ref；不得顺便删除。保持 source 已有的屏内裁剪矩形，不扩大到原始屏外 bounds。

### 4.3 默认值与几何状态

每份公共完整树或补看内容声明固定 `defaults={"checked":false}`。此声明不可被远端任意扩展成其他默认值策略；完整节点其余默认值沿用现行契约。

| 字段位置 | 省略 | 显式值 |
| --- | --- | --- |
| 完整 ReadNode.checked | 已知 false | true 或 null；原未知必须 null，包括根和 context |
| delta.set.checked | 沿用 base 的值 | false、true、null 均保留 |
| compact 完整节点 bounds | hidden：按策略不交付已知几何 | null=unknown；四整数数组=known |
| detailed 完整节点 bounds | 不允许省略 | null=unknown；四整数数组=known |
| delta.set.bounds | 沿用 base 的几何状态 | 受控字符串 `"hidden"`、null、四整数数组 |

Go 内部公共几何使用显式状态加矩形，而非仅用 `*Bounds`；set 再叠加 Field presence。因此完整几何有三态，修改操作另有“未设置”态。

角色、动作、标签、resource、状态变更导致几何展示判据变化时，公共 delta 必须描述新的几何状态，即使内部矩形数值未变。若变化后已经不满足隐藏条件，不能继续沿用 hidden。

### 4.4 chain 规则

chain 只是一组完整节点的公共编码，不是新的逻辑节点或可操作 ref。

- 节点定义二选一：普通 `ref` 或 `chain:[ref1,...,refN]`，不能同时出现；N 至少 2，ref 全局唯一且符合既有 n+非零数字规则。
- chain 成员都满足 anonymous_plain，公共几何均为 hidden；除 ref/children 外全部公共字段及状态相同。
- 除末节点外，每个成员在真实逻辑树中恰好一个孩子，且为下一个成员；children 属于末 ref。末节点允许有多个孩子。
- 不把同级节点合并，不跨真实分叉、功能/内容/特殊状态节点或 context 折链。
- full 的合成根、delta add 与 replace 的定义根保持普通 ref 锚点；只压其后代，不跨定义边界。
- 对每个定义根的后代，按先序取最长合法连续链；长度不足 2 或实际 wire 不更短则用普通节点。相同逻辑树必须产生相同分组及字节。
- 内部矩形不同但 P 后都 hidden 的节点可以共享 chain；detailed 仍能还原各自原矩形。
- ref 属于 chain 不代表有任何动作能力；所有动作仍按原 ref 的完整运行事实判断。

### 4.5 公共形状与文本版本

公共 full 新增固定 `view="compact"|"detailed"`、`defaults={"checked":false}`。观察仅发布 compact；inspect 仅发布 detailed。scope 继续为 reported_visible_with_context，表示同一逻辑节点范围，不承诺公开每个内部字段。

以下仅为合成结构示例：

```json
{
  "format": "full",
  "scope": "reported_visible_with_context",
  "view": "compact",
  "defaults": {"checked": false},
  "root": {
    "ref": "n1", "role": "root", "bounds": [0,0,400,800], "checked": null,
    "children": [{
      "chain": ["n2","n3"], "role": "view",
      "children": [{"ref":"n4","role":"button","bounds":[20,200,180,260],"label":["确认"],"actions":["tap"]}]
    }]
  }
}
```

公共 delta 保留 format/base/set/add/remove/children/replace，新增相同 view/defaults；view 固定 compact。set 使用原 ref；add/replace 的完整定义可在其后代使用 chain。未知 profile/defaults 不按旧格式猜测解码。

文本采用 `result v2`、`tree v2`、`inspect v2`，以明确区别旧稳定文本契约；shell/timeline/json 兜底及最小 diagnostic 无语法变化，保留各自 v1。终止行保持 end result/tree/inspect。

```text
tree v2 format=full id=O1 scope=reported_visible_with_context view=compact nodes=4 entries=3
defaults {"checked":false}

n1 root [0,0,400,800] checked=null
  chain [n2 n3] view
    n4 button [20,200,180,260] "确认" tap
end tree
```

- 完整节点的几何 hidden 时没有矩形列；unknown 仍用 `-`，不得用 `-` 表示有意隐藏。
- 修改行用 `bounds=hidden` 表达 JSON 的受控字符串，用 `bounds=null` 表达 unknown。
- `nodes` 为展开后逻辑节点数；`entries` 为普通节点或 chain 定义行数。inspect 嵌套命中仍按各自子树重复计数；detailed 中两者相同。
- delta `changes` 仍为五块顶层声明条数之和，不按 set 字段数或 chain 内 ref 数计费。
- 设备文本继续 JSON 字符串转义；没有新的字符串缩写、文本截断或业务解释。
- result v2 的其余字段顺序、标量/复合值分类、JSON Pointer 兜底和空行规则沿用现行 result v1；完整节点列顺序沿用原规则，仅增加 ref/chain 分支与可省略的 hidden 几何列。defaults 行固定紧跟块头，随后一个空行再写正文；query 只在整个 inspect 块声明一次。
- text 解码必须恢复同一 W(P(T)) canonical Result；不能只凭旧的 JSON→typed→JSON 保真检查认定新文本无损。
- 未知未来成员或不能表示的合法复合结果仍整体使用完整 json v1 兜底，不漏字段、不另添结果事实。

### 4.6 inspect 固定为详细出口

不增加请求参数，也不改变 node/query 的互斥、字面查询规则。

1. tree/subtree/query 都使用 detailed：保留现有安全投影的全部字段及已知 bounds，逐节点展开，不输出 chain；checked 继续使用新默认值规则。
2. detailed 不是 RawNode 导出，不额外发布 class/package、未公开的 resource、密码正文或屏外原始矩形。
3. tree scope 的 view/defaults 只放在嵌套完整 tree；subtree/query 放在 inspection 信封一次，避免相同元数据重复声明。文本块头与 defaults 行取自同一事实位置。
4. node 指向 chain 中间 ref 时，从该原 ref 开始返回真实完整子树；query 的 path 保留 root..parent 的所有原 ref。
5. inspect 不采集、不续租、不认领 O、不确认或替换 compact 基线；没有新 O。`observe(full=true)` 仍只强制完整 compact 格式，不切换到 detailed。
6. detailed 超预算则返回 inspection_too_large，不静默改回 compact。compact 全树能交付，不保证 detailed 全树也能交付；可用 node/query 收窄。

## 5. 算法

### 5.1 候选到提交

```text
读取源 Capture（沿用当前显示绑定、时间与控制界面检查）
  → 在可回滚的构造器候选中保号并构造完整 T
  → 计算单快照内部成本，超额则丢弃候选
  → P(T)，生成并计量 W(P(T)) 的公共 compact full
  → 公共 full 超 128 KiB 则丢弃候选（即使 delta 可能很小）
  → 从原确认基线 B 计算 Diff(P(B),P(T))，校验 Apply 守恒
  → 编码合法 delta，与新 full 按 70% 比较，失败或不划算则 full
  → 锁内重验 session/work/STOP/epoch/display/期限
  → 对留存预算作有限内存 dry-run，选择非保护历史受害者
  → 可容纳时原子驱逐/计账/提交构造器连续性/发布新 O；否则无副作用拒绝
```

投影、折链、序列化、计量与差分都在状态锁外；锁内只做有限 metadata 检查和指针/计数转换。提交后的快照和 DTO 不可变。

Builder 必须提供一次构造的可提交候选，或等价的 clone+commit 方案：候选期间不得永久推进已发布 prev/nextRef；预算失败、STOP、过期或提交重验失败时释放候选并保留原连续性。不得 Preview 后再 Build 重算同一树以掩盖问题。活动会话的已提交连续性引用与 current 共享；旧会话/历史对象的构造器引用另按 §5.4 释放或计费，不能假定它们都属于当前快照。

本节的“无副作用拒绝”仅限候选提交：不回滚请求接纳时已登记的 P、已认领 O 的单向消费，或已发生的 full 基线确认。仅不产生候选的新发布、历史淘汰及连续性提交；已 claimed 的 O 不恢复 ready。

### 5.2 delta 与 chain 的关系

```text
Bwire → Expand → P(B)
T     → Project → P(T)
D = Diff(P(B), P(T))
Apply(P(B), ExpandDefinitions(D)) == P(T)
Pack(Apply(...)) == 当前公共 full 的规范节点编码
```

- delta 永远相对声明的 base，不叠加上一份 delta；同 base 新 delta 覆盖旧 delta。
- view 或默认规则不同的对象不能互为基线；detailed inspect 不参与确认。
- 链内节点改变只用原 ref 的 set/children/remove 表达。应用端先展开，再应用，再规范压链，无 chain split/merge 指令。
- 若 n3 从匿名节点获得 tap、矩形没变，仍发 `~ n3 actions=["tap"] bounds=[20,200,180,260]`；反向回到匿名普通状态时发 `~ n3 actions=[] bounds=hidden`。
- label/value/hint/resource/特殊状态出现导致不再匿名时，也要补齐新的几何状态。
- 仅内部隐藏矩形变化可得到空公共 delta；完整内部质量摘要与输入证据仍反映变化。
- 保留现有角色变化、跨父移动、根变化不可表达时退 full 的规则；仅 chain 分组变化不触发这些回退。

replace 候选仍服从现有闭包与不重叠判据；成本函数改为匹配新 canonical 编码的字节成本，计入默认值、定义锚点、chain 与声明开销。通过缓存字段/子树成本控制算法开销，不为每个节点重新编码整棵树，不穷举所有差分组合；测试用实际编码长度对照成本函数。

### 5.3 公共预算

共同口径为相应**公共内容对象的规范 UTF-8 JSON 字节数**，与 MCP 结果模式无关；不把文本行数、逻辑节点数或估计 token 当字节预算。JSON 的键、标点、转义和元数据实际计入。

| 内容 | 计费对象 | 上限/判据 |
| --- | --- | --- |
| 观察 full | 新完整 tree 对象，含 format/scope/view/defaults/root/chain | ≤128 KiB，任何发布 O 均先满足 |
| delta 选择 | 新完整 delta 对象与上项完整 tree 对象 | delta < full×70%，否则 full |
| detailed inspect 全树 | detailed 完整 tree 对象 | ≤128 KiB |
| detailed inspect 子树 | `{view,defaults,root}` 内容对象 | ≤32 KiB |
| detailed query 单命中 | `{view,defaults,matches:[match],total,truncated}` 内容对象；match 含 node/path | 每个命中独立可返回时须≤32 KiB |
| detailed query 总量 | 相同对象，matches 为最大可容纳的原序完整前缀 | ≤32 KiB，共享 view/defaults 只计一次 |

query 先确定全部匹配数 N。独立计量第 i 个命中时使用 `total=N`、`truncated=(N>1)`；这只是单元预算候选，不把第 i 个命中当作实际前缀返回。选择实际前缀 k 时使用 `total=N`、`truncated=(k<N)` 并计量最终对象；N=0 时返回空 matches、total=0、truncated=false。数字位数及 true/false 长度都参与字节计算。用缓存单元大小与增量编码开销选前缀，避免反复编码增长前缀造成二次复杂度。

query 检查所有命中，任一个独立返回都超额则整次失败，即使它位于最终前缀之外。total 是预算前命中数，truncated 只描述完整前缀截取；有命中时，不靠返回空 matches 绕过一个无法容纳的命中。

70% 比较采用有界整数关系 `10×deltaBytes < 7×fullBytes`，避免浮点取整改变边界。

这些预算只覆盖表中内容对象，不包含外层操作/观察元数据、截图字节或两种编码的重复传输。MCP 保持不另设文本截断门禁；text/structured 的真实传输字节分别测量，但服务接纳按共同公共对象判定。

### 5.4 内部资源预算与留存

以下是本提案建议的**初始取值**，没有实测内存保证：

- `MaxSnapshotCostBytes = 4 MiB`：每个完整候选快照。
- `MaxRetainedSnapshotCostBytes = 32 MiB`：引擎历史持有的唯一快照成本之和。
- `MaxObservations = 32`：沿用观察记录条数上限。

SnapshotCost 定义为固定内部计量对象 `{raw_capture,full_tree}` 的实际确定性 JSON 字节数；raw_capture 包含完整 Capture，full_tree 为原完整内部安全投影，不能用 compact 代替。使用有上限的计数 writer，计数超单项额度即中止，不保存或记录计量正文。若长期缓存公共 compact 逻辑树或编码字节，将该缓存的规范编码/实际字节加入同一快照成本；优先按需生成，避免额外常驻缓存。

这是可重复的资源计费代理，不是 Go heap/RSS 的精确字节：映射索引、ref 列表、对象/分配器开销及进行中采集/输出暂存不与 JSON 等量。本提案不把 32 MiB 宣称为进程内存硬上限；结合现有深度/节点/RPC 与工作期限边界控制候选，并在系统级记录实际峰值。不能为通过代理预算删除运行证据。

唯一快照集合覆盖所有引擎长期持有者：观察表、current、baseline、active/last/retired session 的连续性/Projector，以及历史 O 的 Projector；相同底层快照只计一次，独立复制或新增缓存必须计费。基线中 [退休会话](../../../internal/engine/retirement.go#L105)及 [lastSession](../../../internal/engine/cleanup.go#L442-L452)仍会持有 session，因此不能仅在 observations 删除后就扣除所有成本。

实现时分离历史材料和构造器生命周期：历史 O 只需完整快照及标量元数据，不应长期保存可变 Projector；输入预览/重验使用相应 work 持有的候选上下文。活动会话只保存已提交连续性，终态 session/lastSession/retired session 释放不再承担连续性的长期引用。不得对尚被旧 work 使用的 Builder 调用 Reset 或竞争改写；通过所有者句柄分离/断开引擎引用，让旧 work 保留其自己的在途对象。若仍有引擎长期 holder 无法释放，就继续纳入去重计费，不把它伪称已释放。在途局部对象与长期留存计费边界分别记录，不将逻辑交接完成伪称物理 goroutine 已退出。

总留存保护 current、确认基线及仍被输入 work 使用的 claimed 观察，按最老顺序选择其他历史对象驱逐，沿用现有观察淘汰后的 observation_not_found 语义。终态的旧 continuity 引用不得仅因 cleanup pending/unconfirmed 而作为永久保护对象阻塞新会话；清理责任无需保留 UI 构造器。

驱逐必须先 dry-run：若候选连同保护集合仍装不下，返回 `tree_too_large`，不先删除历史、不退休旧 ready O、不提交构造器。所有受害者选择、扣费、插入及 current 切换在最终授权重验后一次应用；树大小计算不在锁内。

超额错误复用 tree_too_large，通过受控 `details.budget` 区分 `public_full`、`internal_snapshot`、`retained_snapshots`，并给出数值 limit；不携带树内容。既有解析响应、节点和深度限制不放宽。

### 5.5 失败、撤销与时间

| 情况 | 行为 |
| --- | --- |
| 公共 full、内部单项或保护集合容量超额 | 明确失败；不交付残缺树、不发布新 O、不推进连续性 |
| delta 不可表达或编码不划算 | 使用已经验证且有预算的公共 full |
| detailed inspect 超额 | inspection_too_large；不修改基线、不切换视图 |
| STOP/会话终态/显示代次或 work 变化 | 原提交门禁拒绝，丢弃候选及临时 DTO；不等待树工作完成后才撤权 |
| 时间预算耗尽/采样不收敛 | 沿用现行质量事实与采样策略；不因 compact 看起来相同而改判 stable |
| 未知可合法保留的响应成员 | MCP 使用完整 JSON 兜底，保留 presence 和顺序语义 |

settle 的完整 L1/L2/L3 摘要继续读取 T 与源身份链；不将 L2 改成有损 P(T)，也不删 L1 中的 bounds/状态。`quality.stop_reason=budget` 继续表示采样时间预算，不表示树字节预算。

## 6. 实现方案

### 6.1 接口与文件

| 文件/文件族 | 改动 |
| --- | --- |
| [node.go](../../../internal/protocol/node.go)、拟新增 `internal/protocol/read_tree.go` | 保留完整运行模型；增加公共阅读 DTO、三态几何、chain 定义、新默认值与严格编解码 |
| [surface.go](../../../internal/protocol/surface.go)、[decode_response.go](../../../internal/protocol/decode_response.go)、[responses.go](../../../internal/protocol/responses.go) | Observation/Inspection 切换公共 DTO；inspection 元数据作用域与 detailed 编解码；请求形状保持 |
| [build.go](../../../internal/observation/build.go)、[match.go](../../../internal/observation/match.go) | 候选式构造与连续性提交/丢弃；原匹配判据保持，避免失败候选长期持有 |
| [diff.go](../../../internal/observation/diff.go)、[apply.go](../../../internal/observation/apply.go)、拟新增阅读投影/编码算法文件 | P、Expand、Pack、公共差分/守恒、真实编码成本；完整运行算法不共用有损 DTO |
| [engine.go](../../../internal/engine/engine.go)、[capture.go](../../../internal/engine/capture.go)、[inspect.go](../../../internal/engine/inspect.go)、[retirement.go](../../../internal/engine/retirement.go)、[cleanup.go](../../../internal/engine/cleanup.go) | 内外预算、候选提交、全部长期 holder 留存计账及构造器生命周期分离、compact/detailed 分流、同基线差分 |
| [settle.go](../../../internal/engine/settle.go)、[point.go](../../../internal/engine/point.go)、[act.go](../../../internal/engine/act.go)、[scroll.go](../../../internal/engine/scroll.go) | 保持完整输入/稳定性证据，补充隔离与回归用例，避免误用公共隐藏几何 |
| [客户端](../../../internal/client/client.go)及协议测试 | 使用新公共树类型、默认值、chain 和几何状态；同 contract 配套，不自动旧格式降级 |
| [result_text.go](../../../internal/mcp/result_text.go)、[tree_text.go](../../../internal/mcp/tree_text.go)、[result_value.go](../../../internal/mcp/result_value.go) | result/tree/inspect v2；两模式同源、完整兜底、无状态渲染及文本逆解析测试 |
| [schema.go](../../../internal/mcp/schema.go)、[catalog.go](../../../internal/mcp/catalog.go)及目录/等价测试 | 11 工具保持，更新共同语法图例、inspect 说明与目录指纹期望 |
| [探针入口](../../../scripts/mcp-probe/main.go)与 `scripts/mcp-probe` 解码/检查文件 | 支持新公共响应/文本，验证两种编码、链、差分与配套目录 |
| [版本源](../../../release/versions.json)、[生成脚本](../../../scripts/generate-version.ps1)的四个输出目标 | 唯一来源前进 0.11.0/11/13，生成常量和 App 版本；不手改多处版本 |
| [发布构建](../../../scripts/build-release.ps1)、[主机构建](../../../scripts/build-host-tools.ps1)、[清单核验](../../../scripts/verify-manifest.ps1)、[部署](../../../scripts/deploy.ps1) | 复用现有入口及清单；不为契约 11 另写硬编码版本，不新增配置迁移 |

### 6.2 配置与取值

| 项 | 建议值 | 依据/修改权限 |
| --- | --- | --- |
| public full、detailed 全树 | 128 KiB | 额度沿用，计算对象改变 |
| detailed 子树、查询 | 32 KiB | 额度沿用，明确共享头与完整命中计费 |
| 单快照/总留存 SnapshotCost | 4 MiB / 32 MiB | 与交付解耦的提案初值；需在实施验收记录实际峰值，非 RSS 承诺 |
| 观察条数 | 32 | 沿用 |
| delta/full 大小比 | 严格 <70% | 沿用，改用新公共编码 |
| delta 基线年龄、修订跨度 | <180s、<12 | 沿用；确认入口不变 |
| 源解析深度/节点/RPC | 50 / 20000 / 64 MiB | 沿用，不因公共压缩放宽 |
| text 包装/树/补看版本 | result v2 / tree v2 / inspect v2 | 新语法明确分版 |
| 发行/共同契约/Android code | 0.11.0 / 11 / 13 | 当前 0.10.0 / 10 / 12 的配套升级 |

预算为服务端固定策略，Agent 不增加预算或视图参数；inspect 请求保持原形状。阈值与判据的变更必须作为提案修订重新确认，不由实现随意调大。

### 6.3 日志与可观测性

不默认新增生产事件或树统计日志。树、差异、输入、源采集、计量 JSON、图片和设备文本不进入日志、账本、发布清单或提交。

测试与私有测量工具输出可审阅的汇总数值：旧/新 canonical 字节、text 字节、逻辑节点数/编码项数、投影/编码耗时与真实进程峰值；原始采集仅留 gitignored 私有位置。验证记录只保存必要的匿名汇总与环境。若实现确需新增生产数值事件，须明确白名单后单独纳入本提案复核，不能记录任意字段或异常字符串。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威 | P/W 为只读事实表示；HTTP/MCP 不建立第二份 O/P 或基线执行权威 |
| STOP 不等待、迟到提交重验 | 保持撤权顺序；候选及公共 DTO 可丢弃，连续性不推进 |
| O 单向消费与唯一 ready | 候选失败不额外退休已有 ready，也不回滚已接纳 P、已 claimed 的 O 或已确认基线；成功发布沿用原转换 |
| 短状态锁 | 树算法、编码、计量锁外；留存 dry-run 只遍历有界 metadata |
| 三态与 set presence | 新几何状态明确；false/null/空值变更均保留 |
| 输入与观察事实分层 | 公共空差分不意味着屏幕/完整树不变，不改变 outcome 或 quality |
| 隐私与安全投影 | detailed 只恢复已有安全公开字段，不导出 RawNode 或密码 |
| 严格接入与单一版本源 | 请求保持严格；新响应由共同 contract 11 配套；版本仅由唯一源生成 |
| 历史只读 | inspect 不确认基线，不生成观察，不恢复已过期输入权 |

### 6.5 契约与文档影响

本次改变普通公共响应，必须前进共同 contract，不能仅改工具描述或文本版本而保持 contract 10。

实现时同步 [接口契约](../../../docs/interface.md)、[MCP](../../../docs/mcp.md)、[架构](../../../docs/architecture.md)、[客户端](../../../docs/client.md)、[发布](../../../docs/release.md)、[运行手册](../../../docs/runbook.md)、[接入](../../../docs/integration.md)、[手机操作](../../../docs/phone-operations.md)，以及 [控制端协议](../../../docs/control-protocol.md)中版本说明的真实受影响部分。正式文档只写落地行为，过程/测量/未执行项留在 maintenance；最终提交前完成独立只读 docs 审计并修正、复检。

兼容性：旧 contract 10 的普通 API/客户端/App sync 由既有精确版本检查拒绝；不协商降级、不重解释旧 Node。最小 STOP 的服务接纳继续不依赖普通 contract，但不承诺旧 App 能解析新版本确认。进程重启后旧观察不得被当作新实例的有效输入。

主机配置没有需要迁移的契约字段。主机只更新 Windows 适配器二进制及配套探针/清单元数据，保留既有私有配置、凭据、transport、端口、Origin、result-mode 和宿主启动参数。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 两种模式都减少已知 false、匿名矩形和连续容器对象的重复编码。
- 深树减少展示缩进与编码项数，仍能通过原 ref 追溯和详细补看。
- 预算对新的共同交付表示生效，同样 128/32 KiB 可容纳更多有效公开内容。
- 匿名隐藏几何的单独变化不再产生公共 delta 噪声；功能/内容出现时完整补回公共几何状态。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 长文本、图片节点、真实功能节点仍占较大预算 | 本提案保留内容和功能几何，不以丢失可读信息换压缩 |
| 相同 bounds 的两个 scroll 仍各占一项 | 它们是不同输入容器，不能按矩形相同合并 |
| detailed inspect 仍可能超预算 | compact 与 detailed 交付内容不同，额度分别计算 |
| 采样仍可能 unstable/budget | 时间与完整运行事实未精简，不通过隐藏信息获得稳定判定 |
| 内部采集和内存未按公共字节同比下降 | 源 Capture 与完整输入证据仍保留，新增投影也有成本 |
| both 仍比单模式传输更多 | 同一结果发送两种完整编码，模式默认值本次不改 |
| 旧客户端不能继续无改造读取新树 | 公共形状和默认值升级，需共同 contract 11 |

### 7.3 成本

每次观察增加一次只读投影、规范折链与编码计量；公共差分取代旧交付差分，内部输入树仍存在。计数编码避免保存内部计量正文，公共编码可复用当前已编码结果，避免重复序列化。

不增加设备采集轮次或网络读取；不新增默认生产日志。内部留存额度允许比原 128 KiB 公共门禁更大的完整对象，因此需要独立快照/总留存边界及构造器失败回滚。CPU、RSS 和延时收益仅由实测决定，不能从文本缩短直接推导。

## 8. 验证计划

### 8.1 单元测试

本节是计划，当前未执行程序测试。至少包括以下真实改变路径及反向对照：

| 用例 | 期望 |
| --- | --- |
| false/true/unknown 完整节点；root/context 未知 | false 两种编码省略，true/null 保留，解码一致；缺省声明固定 |
| set false/null/空字符串/空数组 | 所有显式赋值保留，省略只表示沿用基线 |
| hidden/unknown/known 全方向转换 | 三态两两不等；源未知不被标 hidden；detailed 不允许隐藏 |
| 匿名、文本、图片、功能、特殊/未知状态节点 | 判据逐项覆盖；不凭 role=view 推断动作，不丢内容/状态 |
| 已知空 value/hint/resource | 不满足匿名条件，presence 不被抹掉 |
| 真实连续链、同级相同节点、分叉、context、定义根 | 仅合法后代链压缩；保留全部 ref/深度/顺序，定义根保持普通锚点 |
| 同链成员几何不同但公共都 hidden | compact 可折，detailed 恢复各自原几何且 T 不变 |
| chain 成员动作/label/resource/state 改变 | 原 ref 修改后 Expand+Apply+Pack 等于当前 full，不需 split/merge 指令 |
| 获得动作但矩形没变；反向失去动作 | 公共 delta 分别补 known 与 hidden，不依赖 raw bounds 是否变化 |
| children/remove 引用 chain 中间 ref | 语义与展开树一致，无人工 ref |
| 同基线 D1 false、D2 无该字段 | D2 恢复基线值，不沿用 D1；inspect 插入不换基线 |
| 真角色/跨父变化与纯 chain 分组变化 | 前者按既有不可表达规则退 full，后者不导致无谓退 full |
| 内部完整树 >128 KiB、compact <128 KiB 且内部成本允许 | 可发布；删除新预算位置后该用例必须失败（反向对照） |
| 公共 full >128 KiB、delta 很小 | 拒绝发布，不借 delta 绕过完整公共基准上限 |
| 内部单项超额、public 很小 | 拒绝；无新 O；原 ready/连续性不变，无 Builder.prev 候选引用 |
| 总留存：共享引用、受害者不足、保护集合 | 唯一对象一次计费；可驱逐时原子应用；不足时不先删历史 |
| 旧会话结束、current/baseline 清除、新会话推动旧 O 淘汰，但 retired/lastSession 仍留存 | 无未计费 Builder.prev 或历史 O.Projector 引用；不与旧在途 work 竞争 Reset；旧 cleanup 未确认不永久保护 UI 快照 |
| act 后树超额、observe 接纳时已确认基线后候选失败 | 不恢复已消费 O 的 ready，不删除已登记 P，不回滚已确认基线 |
| STOP/到期/显示变化发生在投影或计费后 | 无迟到发布，释放候选；原连续性不推进；不等待树算法才撤权 |
| 128/32 KiB 与 4/32 MiB 边界 | 等于上限通过，超过拒绝；编码转义、默认元数据与路径纳入 |
| 70% 严格比较与 replace 成本 | 阈值等值退 full；大小取新 canonical 编码；估算与实际局部字节对照 |
| detailed 全树超额、链中间 ref 子树 | 明确太大或完整展开；不静默 compact，不确认基线 |
| query 原路径、前缀外大命中、共享头、N 跨数字位数及 true/false 长度差 | 路径完整；任一大命中整次失败；总量只完整前缀，单元 total=N、truncated=(N>1)，前缀 truncated=(k<N)，最终元数据一次准确计费 |
| hidden 几何单独变化 | 公共 delta 可空，但完整 L1/L2 摘要变化；点命中、重验仍使用完整树 |
| 公共投影与 detailed 前后内部深等 | 不原地修改 T/Capture/Source/基线，含受保护输入 |
| text→canonical 与 structured 对照 | chain、几何状态、null、默认值、所有非树成员及数组顺序相等；不能仅依赖旧 typed guard |
| 未知成员与不可表示的合法复合对象 | 完整 JSON 兜底，不漏数据、不错误宣称无损 |
| 合成高冗余 fixture 的字节与确定性 | 固定 30 层可压链和 100 个已知 false 匿名节点，包含根和叶的总深度不超过现行 50；text 与 canonical JSON 均比旧表示少至少 25%，重复渲染/投影字节一致 |

另以单独移除 checked 省略、几何展示状态转换、候选回滚实现，分别证明对应测试失败。测试使用合成数据，不把用户提供的原始树转成提交 fixture。

建议实现期针对包运行：

```text
GOEXPERIMENT=jsonv2 go test ./internal/protocol ./internal/observation ./internal/engine ./internal/client ./internal/mcp ./scripts/mcp-probe
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

实际 Windows 命令按 PowerShell 设置环境变量，记录真实执行形式；这些示意不宣称已经运行。

### 8.2 实机/系统级

当前全部未执行，以下为实施后的验收计划。只覆盖本次树路径和配套发布必要检查，不扩大为无关功能矩阵。

1. 在同一份私有不可变 Capture 上离线比较旧表示与新 P/W，避免用两个不同时间界面声称压缩比。记录 compact canonical/text 字节、逻辑节点/项数、投影和编码耗时；可获得可靠 tokenizer 时再单独记 tokenizer 名和 token 数，不作为接纳算法。
2. 真机检查匿名链、文字/图片、可点击无标签 view、嵌套 scroll、原始特殊标签、根/context 未知与勾选状态。验证 compact 省略与 detailed 展开，原 ref 动作和坐标重验结果正确。
3. 在手机 HTTP MCP 与实际使用的主机适配器路径检查 text/structured/both；三种模式同一事实模型，原始采集/响应仅留私有位置。默认观察 full、同基线 delta、inspect 插入、重验失败交付和错误恢复各做最小闭环。
4. 选择真实冗余页面做同 Capture 对比：两种交付编码都应严格缩小；记录真实比例，不承诺所有页面固定节省率。非冗余页面检查没有内容/状态损失。记录新投影延时及进程峰值，核对内部成本与淘汰行为，不能把成本代理当 RSS。
5. 记录普通动作采样时间与 quality，确认仅隐藏几何的变化不会被人为判 stable，不因本次压缩放宽采样额度。
6. 验证 contract 10 普通 API 与 App sync 被 contract 11 拒绝；最小 STOP 服务仍接纳，不声称旧 App 必能解析确认。新版本就绪后开启新会话，不复用旧 O/P 输入权或回放旧动作。

发布构建/校验计划（在实现、正式文档审计和实现提交完成后的干净树执行）：

```powershell
./scripts/generate-version.ps1 -Check
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

- 不使用 SkipAndroid 或 AllowDirtySource 通过发布门禁；手机包包含 device-service、device-mcp、control.apk、u2.jar、applist.dex、service.sh 六项。
- 两份清单必须匹配同组发行/契约/源码与新工具目录；清单中的构建 HEAD 与版本源 source_commit 的冻结基线含义分别记录。
- 如果主机 build 输出路径正是运行路径，构建写入前就识别并停止精确旧适配器，或先在本仓库干净发布工作区构建再核验替换；不能依靠覆盖正在运行镜像完成升级。

手机部署复用既有有效参数和私有配置，在受控窗口执行已核验清单的部署入口，例如：

```powershell
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json
```

部署前用旧版本 end/STOP、暂停新普通工作、等待最多 5 秒旧逻辑交接并保留未知物理事实；手机按清单整组安装，APK 覆盖安装保留私有数据。核验 health 版本、App versionName/code、配对/准入、手机实际 HTTP MCP 进程镜像哈希和工具目录；不重启手机，不卸载/清库/删账本。

主机只替换实际使用的 `device-mcp.exe`，以及配套发布验证工具 `mcp-probe.exe`；后者不是适配器运行依赖。更新清单作为核验元数据，不迁移或重写配置/凭据/端口/transport/启动参数。不复制 Linux 服务或 APK 作为 Windows 程序，不修改 DSH 源码。

stdio 路径由实际宿主重启连接以 spawn 新 exe；HTTP 路径停止精确旧进程，以原命令和原配置启动新 exe 后重连。主机和手机实际 MCP 宿主都重新发现目录；工具数仍 11，但 description/schema 及目录指纹必须与本组清单一致。仅磁盘替换、页面刷新、tools/list 数量或 list_changed 都不足以证明完成。

主机验证使用 [主机清单核验入口](../../../scripts/build-host-tools.ps1)和既有私有配置直接运行更新后的探针；探针的 transport/binary 选择与现有连接一致，使用重复 manifest 参数检查配套，例如：

```text
build/mcp-probe.exe -config <既有私有配置路径> -binary <实际新适配器路径> -transport <既有路径对应模式> -result-mode text -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

随后对 structured/both 检查相同新契约。上述占位符不作为真实执行命令或验证结论。

对已经运行的手机或主机 HTTP 入口，使用 `-transport http-remote`，探针不启动或停止适配器；使用与目标入口对应的既有合法 HTTP 配置。[http 模式会自行启动适配器，而 http-remote 连接既有入口](../../../scripts/mcp-probe/main.go#L156-L198)。若真实有效部署通过命令行将 stdio 配置覆盖为 HTTP、现有探针拒绝该配置，则通过实际宿主连接完成检查；也可先补充探针不写配置的等价覆盖能力并验证，但不能为通过探针而重写私有配置。`http` 模式仅用于明确空闲端口上的临时产物验证，不代表已运行入口验收。stdio 探针验证新产物，实际宿主换载及目录刷新另行确认。

不直接调用 [verify-m5 包装](../../../scripts/verify-m5.ps1#L184-L204)：它即使 SkipBuild 也会重写私有配置，不符合只换二进制边界。当前 [verify-m6](../../../scripts/verify-m6.ps1#L579-L594)硬编码上一契约 9，本提案不以整套该脚本为门禁，使用本次最小的 10→11 拒绝验证；如实施者选择复用，先修订相关断言/说明并验证，不借此扩大消息或重启演练范围。

发布完成分别报告：已安装版本、运行镜像哈希、App 配对/准入、旧 cleanup 保留事实、实际目录刷新、两端树闭环与未执行项。失败时停止升级并保持受控准入；回退只使用已核验整组配套产物，保留配置/账本，不混用新旧 contract。

### 8.3 记录

实现后写 `maintenance/verification/compact-node-tree-presentation.md`：实现提交、环境、实际命令、两份清单与运行镜像核验、真实对比指标、执行过的用例及未执行项/原因。原始树、响应、截图、命令中的凭据和私有路径不入库。

本提案本身只经过源码/文档取证与提案审阅；不把设计推演、单元测试计划或探针存在写成已验证。实现提交先于提案归档，归档记录真实 hash；归档后若再次对当前 HEAD 核验主机清单，须按其精确 HEAD 要求重建配套产物，不能篡改旧清单适配新的文档提交。

## 9. 已定与未决

### 9.1 已定

本表中用户已认可的方向与实现 agent 提出的具体取值分列；本次整体提案仍等待确认。

| 事项 | 结论及来源 |
| --- | --- |
| text/structured 同一精简 Result | 用户明确要求；不存在 text 精简而 structured 保留完整的分叉 |
| checked 完整节点默认 false | 用户认可；未知显式 null，set 的 false 不省略 |
| 匿名无动作节点省略公共几何 | 用户认可；本提案细化为普通已知默认状态 view/group，包含叶节点 |
| 保留内部完整输入事实 | 用户要求合理运行；本提案不删除运行树节点或修改命中/重验 |
| chain 原 ref 可逆、公共 delta 对 P(B)/P(T) | 会话讨论方向；本提案明确逻辑树与编码分层、根锚点和规范分组 |
| inspect 固定 detailed、无新请求参数 | 实现 agent 的最小公开面建议，提案确认时一并确认 |
| hidden/unknown/known 与 delta sentinel | 实现 agent 的具体 wire 建议；不混同 nil/省略语义 |
| 交付额度不提高，计费对象前移 | 用户认可；保持 128/32 KiB 与 70%，按新公共编码计费 |
| 内部成本 4/32 MiB、候选回滚与原子留存 | 实现 agent 提出的有界初值及资源边界，非实测内存结论 |
| 0.11.0/11/13 与文本 v2 | 实现 agent 按现行版本规则提出；公共变化需要配套升级 |
| 双端发布 | 用户要求；手机整组升级，主机只换二进制/清单并精确重启、刷新目录，配置保留 |
| 本轮交付 | 仅起草提案，未实现、未更改版本源、未部署 |

### 9.2 未决

技术方案给出完整推荐，没有“实施后再定”的判据、阈值或接口空缺。整体方案（包括 9.1 标明的具体建议）由用户在实现前审阅确认；确认后即作为实施契约。

若用户选择另一种 inspect 形式、内部额度或公共 chain/几何表示，先修订对应章节再确认。真实压缩比、token 和运行成本属于验证结果，按 §8 记录，不作为悬空设计事项。

## 10. 实施顺序

1. 用户审阅并确认本提案；若基线前进，先复核证据及版本目标。当前阶段不改源码、不发布。
2. 实现完整模型与公共 DTO 隔离、默认值及三态几何；并行开发独立的文本逆解析/字段守恒测试。
3. 实现候选式 Builder、投影与规范 chain，先证明内部不可变、失败/STOP 后连续性与引用回滚。
4. 实现展开公共逻辑树的 Diff/Apply、展示状态转换及规范编码成本，覆盖链内 ref 和固定基线反例。
5. 接入引擎公共预算与内部 4/32 MiB 留存；详细 inspect 固定展开；复核原输入/质量路径未误用公共几何。
6. 接入 HTTP/客户端及 MCP v2，同步工具图例/目录、探针和三模式等价测试，运行改变路径的回归与反向对照。
7. 只在版本源设置 0.11.0/11/13 与冻结基线，运行生成器，纳入四个生成目标；同步真实受影响的正式文档。
8. 由独立只读 subagent 审计本次 docs，修正并复检后提交实现；提案仍为“提案，未实现”，不抢先归档。
9. 在干净已提交源码身份构建并核验手机完整包与 Windows 工具，按 §8.2 执行受控双端部署、精确重启和实际目录/最小闭环验证。不得把构建、安装、目录刷新或业务闭环合并伪称一个成功。
10. 写真实验证记录；实现提交落盘后按流程补 §11、移动到 implemented、重写相对链接并单独提交归档。归档记录与发布构建所用源码身份分别清楚标注。

## 11. 实现结果

实现提交：`4fc86cf26c40be1df099a1ea259c99ca7a8d5abc`（2026-11-13）。提案落盘提交
`2781ffb`；基线提交 `25db973`。实际执行命令、用例与未执行项见
[验证记录](../../verification/compact-node-tree-presentation.md)。

### 11.1 已落地

| 提案条目 | 代码落点 |
| --- | --- |
| §4.1 公共阅读 DTO、三态几何、默认值与严格编解码 | `internal/protocol/read_tree.go`：`ReadNode`、`ReadTree`、`ReadDelta`、`ReadSetNode`、`Defaults`、`ReadBounds`、`ReadTreeValue`、`Validate`；内部 `Node`/`Tree`/`Delta` 保持运行模型不变 |
| §4.1 响应接线、inspection 元数据作用域 | `internal/protocol/surface.go` 的 `InspectionView`/`InspectionMatch`、`responses.go` 的 `Observation.Tree`、`decode_response.go` 的 tree/inspection 解码 |
| §4.2/§4.3 匿名判据与几何三态 | `internal/observation/read.go` 的 `AnonymousPlain`、`projectReadNode`（compact/detailed） |
| §4.4 chain 规则与可逆折链 | `internal/observation/read.go` 的 `PackTree`、`PackDelta`、`packSubtree`、`chainShorterThanOrdinary`、`UnpackTree`；定义根保持普通 `ref` |
| §5.2 公共差分、Apply 守恒与字节成本 | `internal/observation/read_diff.go`：`ReadDiff`、`ReadApply`、`expandReadDelta`、`readCosts.regionSaving`；`cmd/device-service/main.go` 的 `treeDiffer` 改用 `ReadDiff` |
| §5.1 候选式构造与连续性回滚 | `internal/observation/build.go` 的 `Builder.Candidate` + commit；`engine.Projector.Candidate`；`engine.projectCapture` 与 `publishTreeAttempt` 在唯一提交点调用 commit |
| §5.3 公共预算与 70% 判据 | `internal/engine/capture.go` 的 `projectCapture`（compact 128 KiB）、`publishTreeAttempt`（`10×delta<7×full`）、`budgetError` |
| §5.4 内部 4/32 MiB、留存 dry-run 与原子淘汰 | `internal/engine/capture.go` 的 `MaxSnapshotCostBytes`/`MaxRetainedSnapshotCostBytes`、`planRetentionLocked`；`observation.SnapshotCost` |
| §4.6/§5.3 inspect 固定 detailed 与 128/32 KiB 内容对象 | `internal/engine/inspect.go` 的 `inspectionTree`、`inspectionSubtree`、`inspectionQuery`、`queryEmptyBytes` |
| §5.4 历史持有者与投影器释放 | `internal/engine/engine.go` 移除 `observationState.Projector`；`retirement.go` 的 `retireLocked` 断开 `session.Projector`；`act.go`/`scroll.go` 改用 work/引擎投影器 |
| §4.5 MCP 文本 v2 与两模式同源 | `internal/mcp/tree_text.go`（tree/inspect v2、defaults 行、chain、隐藏几何、`bounds=hidden`）、`result_text.go`（`result v2`）、`schema.go`（共享图例） |
| §8.1 文本逆解析与守恒测试 | `internal/mcp/tree_text_test.go` 的 v2 解析器与 `TestTreeTextRoundTrip`；`result_manifest_test.go` 的公共成员清单 |
| 探针配套 | `scripts/mcp-probe/result_text.go`（v2 oracle、defaults 行、view）、`result.go`（头部比较字段）、`checks.go`（v2 前缀） |
| §6.2 版本与生成目标 | `release/versions.json`（0.11.0/11/13）与 `scripts/generate-version.ps1` 的 Go/Java/XML/Manifest 四个输出；`scripts/verify-m6.ps1` 的上一契约断言修订为 10 |
| §6.5 正式文档 | `docs/interface.md`、`mcp.md`、`architecture.md`、`client.md`、`integration.md`、`release.md`、`runbook.md`、`control-protocol.md`、`phone-operations.md` |

反向对照：去掉 `checked` 省略、关闭匿名几何隐藏、让候选立即提交、公共差分不比较几何状态
时，对应测试分别失败；恢复后通过。详见验证记录 §4。

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 候选式构造的接口形状 | 采用 `Builder.Candidate(capture) (snapshot, commit, error)` 与 `Projector.Candidate`，由引擎在唯一提交点调用 `commit`。与 §5.1 的 clone+commit 等价，未另建预览对象 |
| 留存计费边界 | 唯一快照成本按观察记录的 `Cost` 累计；`baseline` 只保存 compact 阅读表示。退休会话断开 `session.Projector` 引用，但不对仍被在途 work 持有的投影器调用 `Reset`，避免与预览竞争。在途局部对象不入长期计费，与 §5.4 的边界一致 |
| 历史观察不再保存投影器 | `observationState.Projector` 字段被删除而不是保留为 nil；点命中证明使用引擎默认投影器，因为它只需投影的 `Source` 映射、不依赖 ref 连续性 |
| 生产侧文本解析 | 未新增；文本逆解析只在测试与探针侧实现。产品代码只编码，未知成员继续走 `json v1` 兜底 |
| 配置命名 | §6.2 表中的 `MaxObservationBytes` 未采用；实际为 `MaxFullTreeBytes`、`MaxInspectionUnitBytes`、`MaxSnapshotCostBytes`、`MaxRetainedSnapshotCostBytes` |
| 整组发布构建、清单核验、主机工具构建 | **已执行**（后续补充）：干净树 `3a4f579` 构建 `0.11.0`/契约 11/versionCode 13 整组产物与 `manifest.json`；`verify-manifest.ps1` PASS；`build-host-tools.ps1` 与 `-VerifyOnly` PASS，两份清单 source_commit/契约/目录指纹一致 |
| 手机整组部署与实机验收 | **已执行**（后续补充）：`deploy.ps1 -SkipBuild` 原子替换 6 个产物并逐项 SHA-256 一致，`adb install -r` 覆盖安装保留私有数据；`/health` 为 0.11.0/11，`can_start=true`，手机 HTTP MCP 镜像与清单一致。实机完成契约 10 的普通 API 头与控制同步拒绝、最小 STOP 在契约不匹配下仍被接纳、`start→full`、同基线空 delta、inspect 插入不改基线、query 与 end；stdio 与手机 HTTP 两个 MCP 入口的三模式探针均 PASS。`act` 重验失败交付的实机样本未构造（无可靠、不干扰用户的注入方式），`verify-m6.ps1` 由用户排除 |
| 真实 Capture 压缩比、token、耗时与进程峰值 | **未执行**：用户明确排除；原始采集与树不入库，未做离线新旧表示对比。合成高冗余 fixture 只证明 ≥25%，不外推真实比例 |
| `verify-m6.ps1` 运行 | **未执行**：用户明确排除该脚本的破坏性重启演练。只把它的上一契约断言从 9 修订为 10，未运行 |
| 契约 10 拒绝的实机闭环 | **已执行**（后续补充）：`/api/status` 带契约 10 头与控制同步 body `contract_version="10"` 均返回 `409 contract_mismatch`；最小 STOP 仍接纳。现场 App 已随整组升级，未用旧 App 二进制联调 |
| 主机宿主加载新适配器镜像 | **未执行**：按用户要求由其自行恢复宿主依赖。新 `build/device-mcp.exe` 已由 stdio 探针实际拉起并 PASS，运行中的宿主子进程仍执行旧镜像直到宿主重启 |


---

## 附：本提案不改变的事项

- engine 的唯一执行权威、O/P 单向状态、STOP 先撤权与既有提交门禁。
- 设备采集身份、逻辑显示绑定、输入能力判定、ref 不晋升、实时重验和质量采样预算。
- 原内容与能力来源、密码保护、源未知事实、屏内 bounds 定义及日志隐私白名单。
- 工具数量与请求字段、结果模式选择方式、图片分层、shell 与消息正文边界。
- 恢复账本 v5、App 私有数据、配对/凭据和主机配置；不要求操作其他仓库。
