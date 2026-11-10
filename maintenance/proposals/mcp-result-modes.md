# 提案：MCP 结果模式与文本/结构化信息等价

状态：提案，未实现。
基线提交：`4cb11dbcdf319ea445c9be3a97e3b34deff32664`（分支 `master`，2026-11-10）。
适用范围：`internal/mcp` 的 12 个公开工具及未来工具的 MCP 结果投影；`cmd/device-mcp` 的 stdio 模式选择；Streamable HTTP 的每请求模式头。影响所有 MCP 工具调用方，但不改变工具目录、入参 schema、`/api/*` 字段或 `contract_version`。
影响范围：`internal/mcp/server.go`、`render.go`、`tree_text.go`、`http.go`，新增 `result_mode.go`、`result_value.go`、`result_text.go` 及对应测试；`cmd/device-mcp/main.go`；`docs/mcp.md`、`docs/runbook.md`、`docs/interface.md`；`maintenance/decisions.md`、`maintenance/verification/`；`scripts/mcp-probe/*`。
契约（`/api/*` 字段与 `contract_version`）：不改变。MCP 结果投影新增 `both`/`text`/`structured` 三种模式；`structuredContent` 仍为 `Result` JSON，取消事实通过 `error.details` 的保留键承载；`both`/`text` 的文本格式版本化为 `result v1`。`both` 的文本格式变化是 MCP 文本投影的 breaking change（通道组合不变）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/mcp/server.go`、`internal/mcp/render.go`、
`internal/mcp/tree_text.go`、`internal/mcp/http.go`、`internal/protocol/responses.go`、
`internal/protocol/surface.go`、`internal/protocol/node.go`、`cmd/device-mcp/main.go`、
`docs/mcp.md`、`docs/interface.md`）。

---

> **硬契约（长期）**：`AgentResult = (canonical A, CallToolResult.IsError, ImageContent)`。
> `text` 的完整编码 = `result v1`（A + `is_error` 头）+ `ImageContent`；`structured` 的完整编码
> = `structuredContent`（A）+ `CallToolResult.IsError` + `ImageContent`；`diagnostic v1` 只是
> 错误可读性辅助，不属于完整编码。`both` 同时提供两种完整编码与同一 `ImageContent`。两者必须
> 无损还原同一个 `AgentResult`，agent 得到的事实集合**完全相同**，格式可以不同；不得有任何
> agent-visible 事实只存在于一侧。派生显示/计数
> （`nodes`/`changes`/`events`/`items`/`output_bytes`/`tree_scope` 等）不是独立的 `AgentResult`
> 事实，必须可由 A 计算，允许只出现在可读块。此契约不随模式、工具、结果分支或后续优化变化；
> 新增/修改任何工具、字段或优化都必须同时进入两种编码，否则视为提案/实现缺陷。

## 0. 摘要

MCP 适配器目前只有一套结果投影：`Content` 里的 `TextContent` 是阶段事实的可读摘要，
`StructuredContent` 才是完整 `Result`。只把 `TextContent` 交给模型的调用方因此看不到
`shell.output`、`apps.items`、`diagnosis.timeline`；树块和补看块还会在 24/32 KiB 处主动
省略。反过来，`TextContent` 里的 `cancelled`/`cancellation` 又不在结构化结果中，两个通道
并不等价。

本提案增加**结果模式** `both`（默认，兼容现状）、`text`（只发文本+图片）、`structured`
（只发结构化内容+图片，错误/取消保留最小诊断文本）；HTTP 用请求头选择，stdio 用启动参数或
环境变量选择。`both`/`text` 的文本改为确定性的 `result v1`：可读块优先，JSON/JSON Pointer
兜底，**不因大小或格式问题省略 agent 可见事实**，并以 `end result` 暴露宿主截断。
`structuredContent` 保持 `Result` JSON 形状；取消事实写入 `error.details` 的 `mcp.*`
保留键，使两种模式承载同一事实集。**长期硬契约**：`text` 与 `structured` 是同一个
`AgentResult = (A, IsError, ImageContent)` 的两个无损编码，且必须携带同一 `ImageContent`；
agent 得到的事实集合完全相同，格式可以不同；任何后续工具、字段或优化不得只进入一侧。

不改变 `/api/*`、`contract_version`、工具目录/描述/入参 schema、`isError` 非取消 per-tool
判据（取消按 G13 override）、图片载体、引擎、会话、O/P；日志白名单只增加 `result_mode`
枚举字段，不增加正文/内容。不新增 `outputSchema`；不引入公开文本解析 API（解码器只作
测试 oracle）。新增字段/新工具**不会自动**进入 canonical JSON 或文本：必须更新
`MarshalJSONTo`、字段清单、文本 codec 与 fixture，否则 `TestCanonicalJSONCoversManifest`
失败。

## 1. 问题

### 1.1 现象与代价

一次真实 MCP 调用中：

- `apps(query="设置")` 的正文只有 `apps.items_count`、`apps.total`、`apps.truncated`、
  `apps.captured_at`；命中的 `name`/`package` 只在 `structuredContent.apps.items` 里。
  只读文本的调用方无法据此选择 `open_app` 的包名。
- `shell(reference=O1, command="echo hello-mcp && id -u")` 的正文只有
  `shell.termination`、`shell.exit_code`、`shell.truncated`、`shell.output_bytes`；
  命令输出只在 `structuredContent.shell.output` 里。只读文本的调用方看不到命令结果。
- `diagnose` 的正文没有 `diagnosis.timeline`；结构化结果有。
- 树块超过 24 KiB、补看块超过 32 KiB 时，正文输出 `tree v1 omitted` /
  `inspect v1 omitted`；结构化结果仍有完整树。ref 格式异常时正文也整块省略，结构化结果仍有树。
- 取消时正文追加 `cancelled=true` / `cancellation=…`；如果服务结果本身带错误，
  `structuredContent` 没有这两个事实。
- 正文的 `key=value` 标量行直接拼接字符串；`error.message` 等一旦含换行/等号/控制字符，
  正文就不再是可安全解析的行式语法。只有树块做了 JSON 字符串转义。
- 调用方无法选择“只要文本”或“只要结构化”：只读文本的宿主仍会收到 `structuredContent`
  （冗余），支持结构化的宿主仍会收到文本（冗余）；反过来，前者需要的内容又缺失。
- admission 错误走 `invalid_request: …` 特判（[server.go:99–105](../../internal/mcp/server.go#L99-L105)），
  不在现有 `renderText`/树块路径；它也没有模式选择。

代价不是“展示不好看”，而是工具在特定调用方上不可用：`shell` 只剩退出码，`apps` 只剩
计数，`diagnose` 丢时间线；同时两种通道的事实集不一致，无法用“换一个通道”补救。

### 1.2 证据链

1. **文本投影只写阶段事实。** `renderText` 的注释明确不包含树节点、shell 输出和应用名列表
   （[render.go:12–16](../../internal/mcp/render.go#L12-L16)）；`apps` 分支只写计数与时间
   （[render.go:214–221](../../internal/mcp/render.go#L214-L221)），`shell` 分支只写终止
   元数据与 `output_bytes`（[render.go:235–255](../../internal/mcp/render.go#L235-L255)），
   `diagnose` 分支不写 `Timeline`（[render.go:264–289](../../internal/mcp/render.go#L264-L289)）。
2. **结构化投影才是完整 `Result`。** `handle` 只把 `renderText` 的结果放进
   `TextContent`（[server.go:107–114](../../internal/mcp/server.go#L107-L114)），完整
   `Result` 走 `StructuredContent`（[server.go:134–137](../../internal/mcp/server.go#L134-L137)），
   编码函数是 `structuredContent`（[server.go:390–402](../../internal/mcp/server.go#L390-L402)）。
3. **适配器侧存在主动省略。** 观察树块上限 24 KiB、补看块上限 32 KiB
   （[tree_text.go:12–21](../../internal/mcp/tree_text.go#L12-L21)）；超限整块省略
   （[tree_text.go:109–130](../../internal/mcp/tree_text.go#L109-L130)、
   [tree_text.go:132–156](../../internal/mcp/tree_text.go#L132-L156)、
   [tree_text.go:158–227](../../internal/mcp/tree_text.go#L158-L227)）；ref 校验失败也整块
   省略（[tree_text.go:236–256](../../internal/mcp/tree_text.go#L236-L256)）。这些省略只影响
   正文，`StructuredContent` 仍有完整树。`treeTextBlock.finish` 在超限时返回 `omitted`
   字符串，`renderTreeText` 没有“是否省略”的返回信号（[tree_text.go:94–107](../../internal/mcp/tree_text.go#L94-L107)）。
4. **取消事实只在正文。** `cancelled`/`cancellation` 由 `renderText` 追加
   （[render.go:337–342](../../internal/mcp/render.go#L337-L342)），但 `execution.cancelled`
   不进入 `Result`；`structuredContent` 只看到 `Result`，服务错误存在时没有这两个事实。
   只读调用若已取得服务结果且结果无错误，`cancelled()` 不会补 `call_cancelled`
   （[server.go:241–256](../../internal/mcp/server.go#L241-L256)），canonical 结果需要显式
   定义该分支。
5. **正文不是可安全解析的语法。** `line` 直接写字符串
   （[render.go:25–30](../../internal/mcp/render.go#L25-L30)）；`error.message`、`diagnosis`
   摘要等没有统一 JSON 转义。树块单独使用 JSON 字符串字面量
   （[tree_text.go:543–599](../../internal/mcp/tree_text.go#L543-L599)），标量行没有。
6. **测试把缺口固定成契约。** `shell` 测试断言正文不含 `boom`
   （[server_test.go:737](../../internal/mcp/server_test.go#L737)），`apps` 测试断言正文不含
   `设置`（[server_test.go:780](../../internal/mcp/server_test.go#L780)）；admission 断言
   `invalid_request: ` 前缀（[server_test.go:429](../../internal/mcp/server_test.go#L429)）。
7. **文档把正文定义为阶段事实。** `docs/mcp.md` §3 只承诺 `key=value` 阶段事实与树块
   （[mcp.md:33–48](../../docs/mcp.md#L33-L48)），没有承诺 shell/apps 内容；`/api/*` 的
   `shell`/`apps` 结果却明确包含 `output`/`items`
   （[interface.md:665–706](../../docs/interface.md#L665-L706)、
   [interface.md:611–629](../../docs/interface.md#L611-L629)）。
8. **这是既有设计选择，不是偶然回归。** D32 只覆盖树块
   （[decisions.md:355–366](../../maintenance/decisions.md#L355-L366)），树提案明确把 shell
   输出排除在非目标外（[mcp-tree-text.md:104](implemented/mcp-tree-text.md#L104)），范围也
   不含 `apps`（[mcp-tree-text.md:6](implemented/mcp-tree-text.md#L6)）。
9. **上游规范要求文本兼容。** MCP Tools 规范规定返回 structured content 的工具 SHOULD 在
   `TextContent` 中同时返回序列化结果
   （[MCP Tools · Structured Content](https://modelcontextprotocol.io/specification/draft/server/tools#structured-content)）；
   树提案也引用了这条（[mcp-tree-text.md:49–52](implemented/mcp-tree-text.md#L49-L52)）。树块
   补上了树，但没有补 shell/apps/timeline，也没有消除已有的省略。
10. **`/api/*` 数据本身是完整的。** `protocol.Result` 定义了 `Apps.Items`、`Shell.Output`、
    `Diagnosis.Timeline` 等字段（[responses.go:496–523](../../internal/protocol/responses.go#L496-L523)），
    `AppsView`/`ShellView`/`DiagnosisView` 的 marshaler 也写出这些字段
    （[surface.go:291–354](../../internal/protocol/surface.go#L291-L354)、
    [surface.go:411–472](../../internal/protocol/surface.go#L411-L472)、
    [surface.go:648–807](../../internal/protocol/surface.go#L648-L807)）。缺失发生在 MCP
    投影层，不在服务契约。
11. **未来字段不会自动出现。** `Result` 与嵌套类型都是手工 `MarshalJSONTo`
    （[responses.go:526](../../internal/protocol/responses.go#L526)、
    [surface.go:299](../../internal/protocol/surface.go#L299)、
    [surface.go:672](../../internal/protocol/surface.go#L672)、
    [node.go:627](../../internal/protocol/node.go#L627)）；`AppEntry`/`DiagnosisEvent` 没有
    自定义 marshaler（[surface.go:284–287](../../internal/protocol/surface.go#L284-L287)、
    [surface.go:665–670](../../internal/protocol/surface.go#L665-L670)）。新增 agent-visible
    字段必须同时改 marshaler、canonical 清单、文本 codec 与 fixture，否则 canonical JSON
    本身就不会包含它。
12. **`error.details` 的文档边界目前只按 `/api/*` 写。** `docs/interface.md` 说
    `details` 仅可能出现白名单事实（[interface.md:62](../../docs/interface.md#L62)），
    `docs/mcp.md` 说 `structuredContent` 是“与服务同形的 `Result` JSON”
    （[mcp.md:36](../../docs/mcp.md#L36)）；新增 `mcp.*` 保留键必须把这两处边界写清。

当前文本与结构化结果的差异（同一 `Result`）：

| 事实 | 当前 `TextContent` | 当前 `StructuredContent` | 结论 |
| --- | --- | --- | --- |
| `shell.output` | 无，只有 `output_bytes` | 有 `output` | 文本缺失 |
| `apps.items` | 无，只有计数 | 有 `items` | 文本缺失 |
| `diagnosis.timeline` | 无 | 有 `timeline` | 文本缺失 |
| 观察树/补看内容 > 24/32 KiB | `omitted` | 全量 | 文本主动丢失 |
| ref 格式异常 | `omitted reason=ref_format` | 全量 | 文本主动丢失 |
| `cancelled` / `cancellation` | 有 | 服务错误存在时没有 | 结构化缺失 |
| `shell.output_bytes` / `apps.items_count` / `inspection.match_count` | 有 | 无，但可由字段计算 | 派生事实，允许 |
| `diagnosis.issues` 拼接 / `timing_ms` 展开 / `error.details` 展开 | 有 | 原始数组/对象 | 派生视图，允许 |
| 任意字符串 | 原样写入 | JSON 转义 | 文本不可安全解析 |
| 图片字节 | 无（走 `ImageContent`） | 无（走 `ImageContent`） | 一致 |
| 图片元数据 | 有 | 有 | 一致 |

### 1.3 为什么直觉会出错

- **“把 shell.output/apps.items/timeline 补进文本就行”**：树块/补看块的 24/32 KiB 省略、
  ref 格式省略、取消事实不对称、字符串未转义仍在；新字段仍会静默漏掉。补三个字段只能
  消除当前三个现象，不能建立等价性。
- **“把 `structuredContent` 原样复制进文本”**：对只读文本的宿主确实完整，但对同时支持
  两种通道的宿主是 1:1 冗余；树、shell 输出、apps 列表会被重复送进模型。规范允许这种
  兼容兜底，但不是我们要的唯一形态；需要一个可选择的模式，而不是默认双份。
- **“只做 structured 模式”**：支持结构化内容不等于会把结构化内容交给模型。部分宿主保留
  `structuredContent` 只用于程序化调用，普通模型调用仍只投影 `content`；只做 structured
  会让这些宿主彻底看不到结果。能力选择必须是显式的展示选择，不能由“支持 structuredContent”
  推导。
- **“改 `structuredContent` 为 `{result, execution}` 包装”**：能干净承载取消等执行事实，
  但会破坏所有按 `Result` 解码的结构化消费方，也破坏“与服务同形”的既有文档承诺。本提案
  选择把执行事实放进 `Result.Error.Details` 的保留键，保持外层形状。
- **“给工具加 `outputSchema` 让宿主校验”**：MCP 规定一旦发布 `outputSchema`，服务端必须
  提供符合该 schema 的 `structuredContent`；`text` 模式不发 `structuredContent`，会直接
  违反规范。公共目录必须继续不发布 `outputSchema`，除非重新定义模式。
- **“用模式做脱敏/截断开”**：如果 `text` 模式省略某个敏感字段而 `structured` 模式包含它，
  agent 看到什么就取决于它连了哪个模式；这是不可接受的安全属性。模式只能改格式，不能改
  事实集。
- **“新字段会自动出现在 canonical JSON/文本里”**：错误。当前所有结果类型都是手工
  marshaler；新增字段必须改 marshaler、清单、codec 与 fixture，否则测试必须失败。

## 2. 目标与非目标

### 2.1 目标

| 编号 | 目标（可判定） |
| --- | --- |
| **G0（硬契约，长期）** | `text` 与 `structured` 是同一个 `AgentResult = (A,IsError,ImageContent)` 的两个无损编码，含同一 `ImageContent`；agent 从中得到的事实集合完全相同，格式可以不同；任何 agent-visible 字段/事实不得只存在于一侧；`both` 同时提供两种完整编码；此契约不随工具/字段/优化变化 |
| G1 | 支持 `both`、`text`、`structured` 三种结果模式；`both` 为默认；非法模式在工具执行前被拒绝 |
| G2 | `text` 模式只发 `TextContent` + `ImageContent`；不发 `StructuredContent` |
| G3 | `structured` 模式只发 `StructuredContent` + `ImageContent`；成功结果不发完整文本，`isError` 保留最小诊断文本（`diagnostic v1`，不是完整 `result v1`） |
| G4 | `both` 模式同时发完整文本、完整结构化内容与图片；两种表示对同一 `Result` 承载相同事实 |
| G5 | `text` 与 `structured` 的事实集等价：存在 canonical JSON `A`，`DecodeText(EncodeText(A,isError)) == (A,isError)`，`DecodeStructured(A) == A`；`is_error` 是 envelope-only，不进入 `A` |
| G6 | `text` 模式不做适配器侧省略；可读块能无损表示时完整输出；不能表示/非法时用 JSON/JSON Pointer 兜底承载完整值 |
| G7 | `text`/`both` 有 `result v1 is_error=<bool>` 头与 `end result` 尾；`structured` 最小诊断有 `diagnostic v1 is_error=<bool>` 头与 `end diagnostic` 尾；缺失对应尾部表示宿主截断；服务端永不输出半份完整文本 |
| G8 | `isError`（非取消分支）、图片内容、工具目录、入参 schema、`/api/*`、`contract_version` 在所有模式下不变 |
| G9 | 未来工具/字段：必须更新 `MarshalJSONTo`、canonical 清单、文本 codec 与 fixture；`TestCanonicalJSONCoversManifest` 覆盖 `Result` 及嵌套类型，未更新即失败；运行时对未知 composite 使用 `json v1` 兜底 |
| G10 | 模式不是脱敏开关；两种模式承载同一事实集；脱敏若需要，必须同时作用于两种模式 |
| G11 | `structured` 的最小诊断文本覆盖 per-tool `isError` 的可解释字段（`is_error`、`outcome`、`operation_state`、`observation.status`、`error`、`recovery` 等），不承诺完整内容 |
| G12 | `is_error` 在 `result v1`/`diagnostic v1` 头中显式出现，使文本型宿主在无 `error`/`outcome` 的失败分支也知道调用失败 |
| G13 | 取消是 execution-level override：`isError = execution.isError(tool) || execution.cancelled`；取消结果一律 `is_error=true`，与 canonical 的 `call_cancelled` error 一致；非取消分支仍按 per-tool 判据 |

### 2.2 非目标

- 不新增 MCP 工具、不新增工具参数、不改变工具名称/描述/入参 schema/工具数量。
- 不改变 `/api/*` 字段、错误码、`contract_version` 或引擎/会话/O/P/设备行为。
- 不改变 `isError` 的非取消 per-tool 判据（[server.go:408–424](../../internal/mcp/server.go#L408-L424)）；取消是 execution-level override（G13）。
- 不改变图片载体：图片字节只在 `ImageContent`，不进入文本或结构化内容。
- 不新增 `outputSchema`。
- 不提供公开的文本解析 API；`DecodeText` 只作为测试 oracle 存在。
- 不实现模式协商扩展、MCP 能力位或宿主白名单；模式由部署/请求显式选择。
- 不在服务端对 agent 可见事实做大小截断；宿主截断是宿主限制，由 `end result`/`end diagnostic` 暴露。
- 除新增模式与 `both` 文本格式外，不改变工具行为、目录、schema 或 `/api/*`。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 维持现有文本摘要 + 结构化全量 | 0 | 只读文本宿主永远看不到 shell/apps/timeline；树块省略继续存在 |
| R2 只补三个字段 | 在 `renderText` 增加 shell/apps/timeline | 小 | 树/补看省略、取消不对称、字符串转义、未来字段仍会漏；无模式选择 |
| R3 文本改为 canonical JSON | `TextContent` 直接发 `protocol.JSON(Result)` | 小；1:1 最容易 | 模型可读性差、token 大；失去树块/shell/apps 可读性；默认 both 仍冗余 |
| **R4 模式 + `result v1`（选定）** | `both`/`text`/`structured`；文本用可读块 + JSON 兜底；测试 decoder 证明等价 | 中；新增 codec/decoder/测试/文档 | 文本格式变化；大结果文本很大；实现面最大 |
| R5 两个独立适配器 | 一个只发文本、一个只发结构化，两个端口/进程 | 高；两套部署/发布/测试 | 模式选错静默降级；一个 HTTP 进程无法同时服务两类客户端 |
| R6 `{result, execution}` 包装 | `structuredContent` 改成包装对象 | 中 | 破坏所有按 `Result` 解码的结构化消费方与“同形”文档承诺 |
| R7 发布 `outputSchema` | 让宿主按 schema 校验 | 小 | `text` 模式不发结构化内容，直接违反 MCP 规范 |

**选 R4。** 理由：

- R1/R2 不能建立两种表示的事实等价，只是延迟下一次同类问题。
- R3 是最便宜的 1:1 方案，但把“文本模式”退化成 JSON；树块/shell/apps 的可读性收益消失，
  且默认 `both` 仍要双份。它可以作为 R4 的**实现阶段 1**（先建立 JSON 等价基线），但不作为
  最终文本形态。
- R5 的部署/发布/测试成本高于收益；同一 HTTP 端点按请求头选择模式已经能服务混合客户端。
- R6 的干净执行模型不值得破坏 `structuredContent` 形状；`error.details` 保留键可以承载取消
  事实且不改变外层。
- R7 与 `text` 模式互斥；公共目录保持无 `outputSchema`。

## 4. 判据

### 4.1 等价判据

对一次工具执行，定义 canonical JSON `A`：

> **硬契约（长期）**：`AgentResult = (canonical A, CallToolResult.IsError, ImageContent)`。
> `text` 的完整编码 = `result v1`（A + `is_error` 头）+ `ImageContent`；`structured` 的完整编码
> = `structuredContent`（A）+ `CallToolResult.IsError` + `ImageContent`；`diagnostic v1` 只是
> 错误可读性辅助，不属于完整编码。`both` 同时提供两种完整编码与同一 `ImageContent`。两者
> 必须无损还原同一个 `AgentResult`；格式可以不同，事实集合不能不同。派生显示/计数
> （`nodes`/`changes`/`events`/`items`/`output_bytes`/`tree_scope` 等）不是独立 `AgentResult`
> 事实，必须可由 A 计算，允许只出现在可读块。

```text
A = deterministic_json(result_without_image_base64)
    + 若 execution.cancelled:
        ensure result.error
        error.details["mcp.cancelled"] = true
        error.details["mcp.cancellation"] = execution.cancellation
```

- `result_without_image_base64`：把 `observation.image.base64` 清空；其余 `Result` 字段保持。
- `ensure result.error`：若 `Result.Error != nil`，保留其 `code`/`message`/其他 `details`；
  若 `Result.Error == nil`，合成 `Error{Code: "call_cancelled", Message: execution.cancellation}`。
  这覆盖“只读取消 + 已取得服务结果 + 服务结果无错误”的分支（[server.go:241–256](../../internal/mcp/server.go#L241-L256)）。
- **取消的 `isError` override**：`isError = execution.isError(tool) || execution.cancelled`。只要
  取消，`is_error`/`CallToolResult.IsError` 为 true，和 canonical 合成的 `call_cancelled` error
  一致；非取消分支仍按 per-tool 判据。这个 override 只改变取消结果，不改变正常结果。
- `deterministic_json`：固定用 `encoding/json/v2` 的 `json.Deterministic(true)` 或等价的
  有序 `jsontext` 编码；必须覆盖 `Result` 自定义 marshaler 中的 map（`Error.Details`、
  `Delta.Set`/`Children`/`Replace`、`Diagnosis.TimingMS`）。同一 `A` 两次编码逐字节相同。
- `mcp.*` 是 MCP 适配器保留命名空间；`/api/*` 永不产生这些键；服务自身的 `details` 键不受
  影响。`docs/interface.md` 的 details 白名单只约束 `/api/*`（见 §6.5）。
- `structuredContent` 的 JSON 就是 `A`。
- `text` 的 `result v1` 是 `A` 的确定性文本编码；文本头另带 envelope 的 `is_error`（不在 `A`
  内，来自 `CallToolResult.IsError`）。`is_error` 是 envelope-only；`A` 不复制它。
- `ImageContent` 仍由 `execution.image` 决定；三者相同；`A` 只含图片元数据。
- `TestCanonicalJSONCoversManifest` 比较的是**注入 `mcp.*` 之前的 base Result JSON**；
  `error.details.mcp.*` 是 adapter-owned，不作为 `Result` 结构字段参与 manifest 比较，另有
  `TestAPINeverEmitsMCPDetails`/`TestCancellationDetailsParity` 覆盖。

必须成立：

| 判据 | 定义 |
| --- | --- |
| **硬契约（G0）** | 对同一 `AgentResult`，`text` 编码与 `structured` 编码都能无损还原 `(A,IsError,ImageContent)`；不存在任何 agent-visible 字段/事实只在一侧；格式差异不算差异；`both` 同时提供两种完整编码 |
| 文本往返 | `DecodeText(EncodeText(A, isError)) == (A, isError)`（按 §4.2 的比较口径；`isError` 来自头） |
| 结构化往返 | `DecodeStructured(A) == A` |
| 模式等价 | 对同一 fixture，`text` 的 `A`、`structured` 的 `A`、`both` 的文本 `A` 与结构化 `A` 全部相等 |
| 存在性 | `text` 模式 `StructuredContent == nil`；`structured` 模式没有完整文本；`both` 两者都有 |
| 完整性 | `text` 中每个 agent 可见路径都存在；不存在 `omitted` 造成的缺失 |
| 尾部 | `text`/`both` 的非空文本以 `end result` 结束；`structured` 的最小诊断以 `end diagnostic` 结束；缺失即宿主截断 |
| 确定性 | 同一 `(A,isError)` 两次编码逐字节相同；不读取请求参数、时间、随机数、map 迭代顺序 |
| 转义 | 任意文本/设备来源字符串必须是 JSON 字符串；受控 ID/枚举/时间戳可用受限 token；无裸 CR/控制字符 |
| 模式安全 | 非法模式不产生设备调用；模式不改变 `IsError`（含取消 override）、图片和事实集 |
| 未来字段 | `TestCanonicalJSONCoversManifest` 覆盖 `Result` 及嵌套类型的 agent 可见字段（注入 `mcp.*` 之前的 base JSON）；未更新即失败；未知 composite 运行时 `json v1` 兜底 |
| 最小诊断 | `structured` 的 `isError` 文本以 `diagnostic v1 is_error=true` 开头、`end diagnostic` 结束；至少含 `operation_id`、`error`/`recovery`/`outcome`/`observation.status` 中存在的部分，不要求完整内容 |

比较口径：

- JSON 对象按成员比较，不按字节；数组顺序必须相同；map 无固有顺序。
- 数字按 JSON 数值比较；整数不得被 float 化（解码使用 `json.Number` 或等价机制）。
- `null`、缺失、空数组、空字符串、`false`、`0` 必须区分；编码器从 canonical JSON 生成，
  因此 presence 由 `Result` 的 marshaler 决定。
- `ImageContent` 与 `IsError` 在 `CallToolResult` 层单独比较；`text` 头中的 `is_error` 必须与
  `CallToolResult.IsError` 一致。
- 派生展示（`shell.output_bytes`、`apps` 计数、`inspection.nodes` 等）可以出现在块头，
  但不是 `A` 的字段；解码器可以校验，不得把它们当成额外事实。

### 4.2 不进判据

| 项 | 原因 |
| --- | --- |
| 文本的行宽、缩进、可读性 | 只保证语法、完整性与确定性 |
| `TextContent` 与 `StructuredContent` 的字节相同 | 格式可以不同，事实必须相同 |
| 图片 base64 | transport-only，走 `ImageContent` |
| 日志、内部执行标志（`service`/`admission`） | 不是 agent 可见事实；认证/传输错误由 `Result.Error` 表达 |
| 宿主截断后的内容 | 服务端不截断；宿主截断由 `end result`/`end diagnostic` 暴露，属于宿主边界 |
| `outputSchema`/工具描述变化 | 本提案明确不改 |
| 工具自身的 `isError` 判据（非取消分支） | 保持不变；取消按 G13 override；只要求三模式一致 |
| `result v1` 的具体 golden 字节 | 实现时固定；不得改变事实集/语法规则 |

### 4.3 与既有契约的关系

- **硬契约（长期）**：`text` 与 `structured` 是同一个 `AgentResult` 的两个无损编码，事实集
  完全相同；任何工具/字段/优化不得只进入一侧。格式差异允许，事实差异不允许。
- `/api/*`、`contract_version`、工具目录、入参 schema、`isError`（非取消分支）、图片载体、
  引擎状态机不变；取消按 G13 override 为 `is_error=true`（MCP 结果行为变化，发布说明标注）。
- `structuredContent` 外层仍是 `Result` JSON；新增的 `mcp.*` 只在取消时出现在
  `error.details`。`docs/interface.md` 的 details 白名单只约束 `/api/*`；
  `docs/mcp.md` 明确 MCP `structuredContent` 的 `error.details` 可含 `mcp.*` 保留键。
  `Details` 是 `map[string]any`，严格解码器不会因新增键拒绝。
- `both`/`text` 的文本从当前“`key=value` + 树块”升级为 `result v1`：受控 token 尽量保留，
  任意字符串 JSON，增加 shell/apps/timeline 块与 `end result`，删除 `omitted`。这是 MCP
  文本投影的格式变化（breaking），不是 `/api/*` 契约变化；`docs/mcp.md`/`docs/runbook.md`
  同步改写，发布说明标注。
- 工具目录指纹（`CatalogFingerprint`）只覆盖名称/描述/入参 schema；本提案不改这些，指纹
  不变。发布产物仍按发行流程重建，清单记录新发行号/源码提交与同一指纹；可选记录支持的
  `result_modes`/默认模式。
- `both` 仍是默认模式，兼容“不设置模式”的现有调用方；但文本格式变化。需要旧文本的调用方
  应固定适配器版本；本提案不保留第二套旧文本 codec。

## 5. 算法

### 5.1 模式选择

```text
ResultMode = both | text | structured

HTTP:
    header = X-Mobile-Use-Result-Mode
    absent  -> default (both)
    invalid / multiple values / surrounding whitespace -> 400 before tools/call;
               no service call, no O/P allocation
    valid   -> AddTool 从 CallToolRequest.Extra.Header 读取（SDK v1.8.0 在 Streamable HTTP
               请求上填充 Header；stdio 的 Extra 为 nil，使用默认模式）

stdio:
    flag -result-mode  >  env MOBILE_USE_MCP_RESULT_MODE  >  default (both)
    explicit empty / invalid -> 启动失败（exit 2），不启动 MCP
```

- 头名固定 `X-Mobile-Use-Result-Mode`；值只接受 `both`、`text`、`structured`，大小写敏感，
  不接受空串/前后空白/别名。用 `request.Header.Values(name)` 检查恰好一个值；`Header.Get`
  只取第一个值，不足以拒绝多值。
- 头是适配器本地投影选项，不传给设备服务，不作为授权依据；认证与 Origin 规则不变。
- HTTP 每请求独立；同一进程可同时服务不同模式的客户端。stdio 每进程固定。
- `flag` 包无法区分“未设置”和“显式设为空”；用 `flag.Visit` 或 sentinel 记录是否显式设置。
  显式空值/非法值启动失败，不回退 env。
- `NewServer` 增加 `DefaultResultMode`（零值 = `both`），`cmd/device-mcp` 解析后传入。
- `both` 是默认值：现有调用方不设置模式时通道组合兼容（文本格式按 §6.6 变化）。

### 5.2 canonical 结构化值

```text
canonical(execution):
    result = execution.result or {}
    clear observation.image.base64
    if execution.cancelled:
        if result.error == nil:
            result.error = {code: "call_cancelled", message: execution.cancellation}
        result.error.details["mcp.cancelled"] = true
        result.error.details["mcp.cancellation"] = execution.cancellation
    return deterministic_json(result)
```

- `deterministic_json`：固定 `json.Deterministic(true)` 或等价有序 `jsontext` 编码；自定义
  marshaler 中的 map 必须排序（`Error.Details`、`Delta.Set`/`Children`/`Replace`、
  `Diagnosis.TimingMS`）。若某个自定义 marshaler 不尊重确定性选项，canonicalizer 负责
  重新规范化/排序。
- `structuredContent` = canonical bytes（`both`/`structured`）。
- `text` encoder 输入同一份 canonical bytes；`text` 模式不把 bytes 发出去，只发编码后的文本。
- `both`/`text`/`structured` 共用同一个 canonical 值，避免两套事实源。
- 取消时 `details` 的键名固定为 `mcp.cancelled` / `mcp.cancellation`；值类型分别为 bool/string。
- `structuredContent` 不新增顶层字段；`error.details` 的保留键是唯一执行事实入口。

### 5.3 `result v1` 文本编码

#### 5.3.1 总体与字段分类

```text
result v1 is_error=<true|false>
<item>*
end result

item := field-line | json-fallback | block
```

- `is_error` 来自 `CallToolResult.IsError`，不在 canonical `A` 内；文本头显式给出，解码器
  与 `CallToolResult.IsError` 比较。
- `result v1` 必须是第一行；`end result` 必须是最后一行。缺失/重复头部或尾部 = 无效文本。
- **item 可交错**：按 canonical `A` 的 `Result` 成员顺序输出；遇到 `observation`/`inspection`/
  `apps`/`shell`/`diagnosis` 时可在该位置输出块，之后仍可继续输出 `app`/`notification`/
  `error`/`recovery` 等字段行。不要求“所有字段行在块之前”，encoder 不需要缓冲块。
- **空行规则**：
  1. 字段行/`json v1` item 之间无空行；
  2. 字段/`json v1` 后接块：恰好一个空行；
  3. 块后接块：恰好一个空行；
  4. 块后接字段/`json v1`/`end result`：无空行；
  5. `end result` 前不加空行。
  6. `result v1` 头后接块：恰好一个空行；头后接字段/`json v1`：无空行。
  `tree v1`/`inspect v1` 块内部的“块头 + 空行 + 正文”是块语法的一部分，不受外层空行规则影响。
- `<field-line>` 的值按字段清单分为三类（字段清单是 encoder/decoder/tests 共用的唯一
  `resultFieldKind` 表，不允许两套分类）：
  - `token`：受控 ID/枚举/时间戳/整数/布尔/`null`，只允许 ASCII 非空白、非 `=`、非 `"`、
    非 `\` 的受限 token；例如 `operation_id=P1`、`outcome=executed`、`shell.exit_code=0`、
    `session.state=active`、`observation.captured_at=2026-…Z`。
  - `json-string`：任意文本/设备来源字符串，必须是 JSON 字符串字面量；例如
    `error.message="原会话已结束"`、`diagnosis.summary="…"`、`app.package="com.android.settings"`。
  - `json`：对象/数组/数字等复合值，紧凑 JSON；例如 `error.details={"limit":5}`、
    `observation.screen={"width":1080,…}`、`diagnosis.issues=[…]`。
- **树块/补看块是例外**：块头继续使用受限 token（`id=O3`、`scope=…`、`captured_at=…`、
  `role`、`actions` 等），块内 label/value/hint/resource 使用 JSON 字符串。这是既有
  `tree v1`/`inspect v1` 语法的一部分，不违反“任意文本必须 JSON”的规则。
- 受控 token 只用于字段清单标记为 token 的字段；未标记/未来字段一律 JSON 或 JSON 兜底。
  token 字段若值为空串或不满足 token 语法（含空白/`=`/`"`/`\`），改用 JSON 字符串编码；
  decoder 对 token 字段也接受 JSON 字符串，因此不会因一个异常 ID 丢失事实或撕裂语法。
- 顶层/已知嵌套字段使用 `key=value`（如 `operation_id=P1`、`session.state=active`）；
  未知成员、含歧义字符的 key、或整体 composite 不可读时使用
  `json v1 path="<json-pointer>" value=<json>`；`path` 是 JSON 字符串字面量，内容是 RFC 6901
  JSON Pointer，例如 `"/observation/tree"`、`"/apps/items/0"`、`"/diagnosis/timeline/0"`；
  `~0`/`~1` 按 RFC 6901 转义。
- 每个 JSON 路径只出现一次；树块/补看块头中的 `id`/`scope`/`base`/`format`/`nodes`/`changes`/
  `tree_scope`/`events`/`items`/`output_bytes` 的“A 事实/派生校验/仅显示”归类见 §5.3.2 表；
  decoder 只校验派生项，不单独设路径。`observation.id` 是观察 ID 的唯一来源；
  `inspection` 的 `origin` 行只在 `inspect` 块模式出现，整体 `/inspection` 兜底时不再输出。
- 字段顺序固定：顶层按 `Result.MarshalJSONTo` 的顺序；块内按 §5.3.2 的固定顺序；map 按
  key 排序；数组保持顺序。
- **没有 `omitted`**。可读块不可表示/非法时改用 `json v1`，不丢数据。

#### 5.3.2 可读块

**树块（沿用现有语法，但取消大小阈值）。** `observation.tree` 为 full/delta、ref 合法、
`Observation.ID` 非空、`Tree.Scope`/`Delta.Base` 非空、`TreeValue` 恰好一个 full/delta、
`Tree.Format` 合法、且没有可读语法未覆盖的成员时，输出现有 `tree v1 …` / `end tree`；
`inspection` 为 tree/subtree/query、`ObservationID`/`CapturedAt` 非空、`Scope` 合法、ref 合法、
字段可表示时，输出现有
`inspect v1 …` / `end inspect`。**不再有 24/32 KiB 适配器上限**：可读块通常比 JSON 更小，
能表示就完整输出；只有非法 ref、未知成员、scope/id/base 为空等不可表示情况才走
`json v1 path="/observation/tree"` 或 `json v1 path="/inspection"`。宿主截断由 `end tree`/
`end inspect`/`end result` 暴露；服务端不省略。现有树块语法、JSON 字符串转义、确定性渲染
不变。

**shell 块。**

```text
shell v1 termination=<token> exit_code=<token> truncated=<token> output_bytes=<n|null>
output <json-string>          # 0..n 行
output_error <json>            # 可选
end shell
```

- `output` 为 `null` 时块头 `output_bytes=null`，没有 `output` 行。
- `output` 为字符串时，每个 chunk 是从上一个 `\n` 之后到下一个 `\n`（含）的 JSON 字符串；
  空串输出一行 `output ""`；无换行的大输出是一个 chunk；`\r\n` 保留在 chunk 内（JSON 转义）。
  解码按顺序拼接，并用 `output_bytes` 校验。
- 未知 `ShellView` 成员用 `json v1 path="/shell/<key>"` 兜底。

**apps 块。**

```text
apps v1 items=<n> total=<token> truncated=<token> captured_at=<token>
item <json-object>            # 完整 canonical item，顺序与 Items 一致
end apps
```

- `item` 是该数组元素的**完整 canonical JSON 对象**，不是固定 `{name,package}`；未来字段
  自动保留。`items=0` 时没有 `item` 行；解码得到空数组。
- 未知 `AppsView` 成员用 `json v1 path="/apps/<key>"` 兜底。

**timeline 块。**

```text
timeline v1 events=<n>
event <json-object>           # 完整 canonical event，顺序与 Timeline 一致
end timeline
```

- `event` 是完整 canonical JSON 对象，不是固定 `{at,phase,code}`；未来字段自动保留。
  仅当 `diagnosis.timeline` 存在且非空时输出。
- 未知 `DiagnosisView` 成员用 `json v1 path="/diagnosis/<key>"` 兜底。

**块头派生元数据表。** decoder 对下表“派生/校验”列只校验，不设 JSON 路径；不得把它们当成
`A` 的额外事实：

| 来源 | `A` 事实 | 派生/校验（不设路径） |
| --- | --- | --- |
| `tree v1` 头（full） | `observation.id` + `observation.tree.format/scope/root` | 头 `id`/`format`/`scope` 只校验；`nodes` 仅显示/校验；`scope` 只在 full 变体存在 |
| `tree v1` 头（delta） | `observation.id` + `observation.tree.format/base/set/add/remove/children/replace` | 头 `id`/`format`/`base`/`scope` 只校验；`scope` 为派生显示（delta 无 A `scope`，不设 `observation.tree.scope`）；`nodes`/`changes` 仅显示/校验；`base` 只在 delta 变体存在 |
| `inspect v1` 头 | `inspection.observation_id/revision/captured_at/scope`、`tree`/`root`/`matches`/`total`/`truncated`；`inspection.origin` 行 | `nodes` 仅显示/校验；`tree_scope` 仅在 `scope=tree` 时映射 `inspection.tree.scope`，`subtree`/`query` 时为派生显示，不设 `inspection.tree_scope` |
| `shell v1` 头 | `shell.termination/exit_code/output/truncated/output_error` | `output_bytes` 仅显示/校验 |
| `apps v1` 头 | `apps.items/total/truncated/captured_at` | `items` 计数仅显示/校验 |
| `timeline v1` 头 | `diagnosis.timeline` | `events` 计数仅显示/校验 |
| `result v1` 头 | `CallToolResult.IsError`（envelope） | `is_error` 只与 envelope 比较，不进入 `A` |

**observation 头部。**

`observation.status`、`id`、`origin`、`revision`、`captured_at`、`actionable_until` 用 token；
`screen`、`foreground`、`missing`、`image`、`error` 用 JSON；`tree` 走树块/JSON 兜底；未知成员
走 JSON Pointer。`observation.id` 是唯一 ID 来源；树块头 `id` 只做校验。

**inspection 头部。**

`inspection.origin=<token>` 单独一行；`observation_id`、`revision`、`captured_at`、`scope`、
`tree_scope`、`root`、`matches`、`total`、`truncated` 由 `inspect v1` 块头/正文承载；
`tree`/`root`/`matches` 走块；未知成员走 JSON Pointer。若整体不可读/不可表示，走
`json v1 path="/inspection"`，此时不再输出 `inspection.origin` 行。

**其他顶层字段。**

`session.*`、`cleanup.*`、`app.*`、`notification.*`、`error.*`、`recovery.*` 及其余标量/对象
按字段清单输出嵌套 `key=value`（token/json-string/json）；未知成员走 JSON Pointer。例如
`error.code=session_ended`、`error.message="原会话已结束"`、`error.details={"limit":5}`。

#### 5.3.3 编码伪代码

```text
EncodeText(A, isError):
    emit "result v1 is_error=" + bool_token(isError)
    for key in A.members in Result order:       # 用 jsontext.Decoder 逐成员读取，不转 map
        switch key:
        case "observation": encodeObservation(A.observation)
        case "inspection": encodeInspection(A.inspection)
        case "apps": encodeApps(A.apps)
        case "shell": encodeShell(A.shell)
        case "diagnosis": encodeDiagnosis(A.diagnosis)
        default: encodeKnownField(key, A[key])  # 按字段清单 token/json-string/json
    emit "end result"
```

- 顶层成员顺序用 `jsontext.Decoder` 逐成员读取，或固定字段表；不能先
  `unmarshal` 到 `map[string]any`（会丢顺序）。
- `encodeObservation`：已知头部字段按清单；`tree` 可读则块，否则 `json v1`；未知成员
  `json v1`。
- `encodeInspection`：可读则 `inspect` 块 + `origin`，否则整体 `json v1`。
- `encodeApps`/`encodeShell`：按 §5.3.2；块头派生计数只用于可读性/校验。
- `encodeDiagnosis`：已知字段按清单；`timeline` 块/JSON 兜底；未知成员 JSON Pointer。
- `compact_json` 不换行、不 HTML 转义、无效 UTF-8 按现有树块规则替换为 U+FFFD；保证单行。
- 任意 composite（`Node`、`SetNode`、`Delta`、`InspectionMatch`、`AppEntry`、`DiagnosisEvent`
  等）只要出现可读语法未覆盖的成员，就对该 composite 整体走 `json v1`。

#### 5.3.4 解码器（测试 oracle）

```text
DecodeText(text):
    require first line "result v1 is_error=<bool>"
    require last line "end result"
    obj = {}
    for each line/block:
        switch:
        case key=value (token/json by manifest): set obj[path] = value
        case tree/inspect/shell/apps/timeline block: reconstruct and set path
        case json v1 path="<pointer>" value=<json>: set obj at pointer = json
        else: error
    reject duplicate paths
    return obj, is_error
```

- 解码器只用于测试，不发布；严格拒绝未知语法/重复路径/缺尾。
- `DecodeText` 只接受 `result v1`；`diagnostic v1` 由 `TestStructuredMinimalErrorText` 做最小
  检查，不进入 `A` parity（它本来就不是完整 A）。
- 树/补看块复用现有测试解析器（[tree_text_test.go:675–1298](../../internal/mcp/tree_text_test.go#L675-L1298)）。
- shell/apps/timeline 块解析器新增；JSON Pointer 解析支持对象与数组（`/apps/items/<i>`、
  `/diagnosis/timeline/<i>`）与 `~0`/`~1` 转义。
- 解码后的对象与 canonical JSON 按 §4.1 比较；`is_error` 与 `CallToolResult.IsError` 比较。

### 5.4 admission

- 删除 `handle` 的 `invalid_request: …` 文本特判（[server.go:99–105](../../internal/mcp/server.go#L99-L105)）；
  admission 结果与普通结果一样进入 canonical `A` 与模式投影。
- `text`/`both`：`result v1 is_error=true`，`error.code=invalid_request`，
  `error.message=<json-string>`；`structured`：`structuredContent` + 最小诊断文本。
- 不产生设备调用、不分配 O/P；HTTP 非法模式仍在工具执行前 400。
- `TestStrictAdmission` 改为断言 `result v1`/`error.code`/`is_error`，不再断言
  `invalid_request: ` 前缀。

### 5.5 取消

- `execution.cancelled=true` 时，canonical 按 §5.2 保证 `Error` 存在；已有服务错误保留，
  否则合成 `call_cancelled`，message 用 `execution.cancellation`。
- **取消 override**：`isError = execution.isError(tool) || execution.cancelled`。取消结果一律
  `is_error=true` / `CallToolResult.IsError=true`，与 canonical 的 `call_cancelled` error 一致；
  非取消分支仍按 per-tool 判据。只读 `status`/`inspect`/`diagnose` 已取得服务结果且无错误时，
  也不允许出现“`is_error=false` + error 字段”的矛盾。
- 取消事实写入 `error.details.mcp.*`；文本在 `error.*` 行中可见；不再额外写顶层
  `cancelled=`/`cancellation=` 行。这样避免“正文有、结构化没有”的不对称。
- 只读取消（有/无服务结果）与工作取消同一规则；`isError` 按上述 override。
- `docs/mcp.md` §6 的取消说明改为：取消事实在 `error.details.mcp.*`；文本在 `error` 行中可见；
  取消的 `is_error` 为 true。

### 5.6 模式投影

```text
isError = execution.isError(tool) || execution.cancelled
A = canonical(execution)
text = EncodeText(A, isError)        # 仅 text/both；result v1
structured = A                       # 仅 structured/both
images = execution.image -> ImageContent(s)

text:
    Content = []sdk.Content{}
    Content = append(Content, TextContent{text})
    Content = append(Content, images...)
    StructuredContent = nil

both:
    Content = []sdk.Content{}
    Content = append(Content, TextContent{text})
    Content = append(Content, images...)
    StructuredContent = structured

structured:
    Content = []sdk.Content{}
    Content = append(Content, images...)
    if isError:
        Content = append([]sdk.Content{TextContent{minimal_error_text(A, isError)}}, Content...)
    StructuredContent = structured
```

- `Content` 必须先初始化非 nil 空切片，再 append；成功且无图片时编码为 `[]`，不是 `null`。
- `minimal_error_text` 使用**独立头** `diagnostic v1 is_error=true` 与尾 `end diagnostic`，不冒充
  完整 `result v1`/`end result`。它编码 §2.1 G11 列出的可解释字段：`operation_id`、
  `operation_state`、`outcome`、`phase`、`target_reference`、`request_closed`、
  `blocking_operation_id`、`can_start`、`blocking_code`、`start_operation_id`、
  `observation_state`、`input_state`、`message_id`、`saved`、`observation.status`/`id`、
  `error`、`recovery`、`session`、`cleanup`（存在时）；不编码 observation 树/screen/
  foreground/image/missing、inspection、apps、app、shell、notification、diagnosis 全集。
  它只用于错误/取消可读性，不承担完整投影职责；不要求解码回完整 `A`，`end diagnostic`
  只表示“这份最小诊断没有被宿主截断”，不表示“完整 A”。
- `diagnostic v1` 的字段/空行规则与 `result v1` 的 field-line 相同；它不包含块，因此没有
  块前空行；`end diagnostic` 前不加空行。
- `structured` 成功且无图片时 `Content=[]`（非 nil）；若某宿主拒绝空 content，该宿主应使用
  `both`/`text`，不是本模式的契约。
- `both` 的文本与结构化内容都来自同一个 `A`；测试强制二者解码相等。
- `isError` 与图片在所有模式一致；模式不改变 `IsError` 规则（含取消 override）。

### 5.7 失败、超限与截断

- canonical JSON 编码失败或文本编码失败（理论上只有类型错误/非法值）：返回受控的
  `internal_error` 结果；文本/结构化均不含原始错误字符串。
- 可读块不可表示/非法：`json v1` 兜底，仍然完整；**任何路径都不输出 `omitted`**。
- 文本总大小没有服务端上限；服务端不截断 agent 可见事实。服务端上限仍是观察树 128 KiB、
  shell 64 KiB、apps 32 KiB、inspect 全树 128 KiB / subtree/query 32 KiB；`text`/`both` 的大结果
  文本可能接近这些量级，`both` 还会同时带结构化副本。宿主可能截断；`end result`/`end diagnostic`
  缺失即表示“这份文本不完整”，调用方应改用 `structured`（若宿主支持）或 `inspect`/更窄的
  调用收窄。服务端不能恢复宿主已经丢掉的内容。
- 验收必须包含合成的 128 KiB 观察树、128 KiB inspect 全树、32 KiB subtree/query、64 KiB shell、
  32 KiB apps fixture，并记录实际文本字节数与宿主截断观测；不能只测“刚超过旧 24/32 KiB 阈值”。
- 模式非法：HTTP 400，stdio 启动失败；不进入 `run`，不分配 O/P，不调用服务。
- 不存在新增超时、重试或后台任务；结果投影在本次调用返回前同步完成。

### 5.8 未来字段

- **不是自动的**。新增 agent-visible 字段必须：
  1. 更新对应 `MarshalJSONTo`，使 canonical JSON 包含该字段；
  2. 更新字段清单（token/json-string/json/block/transport-only/derived）；
  3. 更新文本 codec 的已知字段/块，或依赖 `json v1` 兜底；
  4. 加 fixture/parity 测试。
- `TestCanonicalJSONCoversManifest` 用反射/清单比较 `protocol.Result` 及嵌套类型与 canonical
  JSON 成员；缺一即失败。
- 对可读 composite：出现可读语法未覆盖的成员时，整体走 `json v1`；`item`/`event` 使用完整
  canonical JSON 对象，天然保留未来字段。
- `TestFutureFieldFallback` 用带额外 JSON 成员的 fixture（不改 `protocol` 类型）证明兜底保留
  数据。
- `TestCanonicalJSONDeterministic` 用 `Error.Details`/`Delta` map fixture 连跑多次比较字节。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/mcp/result_mode.go` | 新增 `ResultMode` 类型、`ParseResultMode`、`String`、HTTP 头/stdio 环境变量常量 |
| `internal/mcp/result_value.go` | 新增 canonical JSON 构造、确定性编码、取消 `details` 注入、图片 base64 清理 |
| `internal/mcp/result_text.go` | 新增 `result v1`/`diagnostic v1` encoder；树/补看块复用 `tree_text.go`；新增 shell/apps/timeline 块、**单一** `resultFieldKind` 表、JSON Pointer 兜底；该表由 encoder/decoder/`TestResultFieldManifest` 共用 |
| `internal/mcp/result_text_test.go` | 新增测试 decoder、round-trip、golden、presence/转义/大值/ref 兜底、空行/块交错、无 `omitted` |
| `internal/mcp/result_parity_test.go` | 新增模式矩阵、字段清单/反射、fixture parity、反向对照、未来字段兜底 |
| `internal/mcp/server.go` | `Options.DefaultResultMode`；`adapter` 保存默认模式；`AddTool` 从 `CallToolRequest.Extra.Header` 读取模式；`handle` 按模式组装 `Content`/`StructuredContent`；取消 `isError` override；admission 统一走 canonical；移除 `invalid_request:` 特判 |
| `internal/mcp/render.go` | 现有 `renderText` 的字段投影逻辑被 `result_text.go` 取代/拆分；`detailValue`/派生行按需保留；不再承担完整文本职责 |
| `internal/mcp/tree_text.go` | 保留树块语法/渲染器；删除 24/32 KiB 上限与 `omitted`；`renderTreeText`/`renderInspectionText` 改为 `(block string, ok bool)`；不可表示时 `ok=false`；测试解析器保留在 `tree_text_test.go` |
| `internal/mcp/http.go` | 校验 `X-Mobile-Use-Result-Mode`（`Header.Values` 恰好一个，拒绝多值/空白）；非法 400；合法值由 `AddTool` 从 `CallToolRequest.Extra.Header` 读取；不注入 context；不改变 auth/Origin |
| `cmd/device-mcp/main.go` | 新增 `-result-mode` flag、`flag.Visit` 检测显式设置、读取 `MOBILE_USE_MCP_RESULT_MODE`、校验并传入 `Options.DefaultResultMode` |
| `internal/mcp/server_test.go` | 更新全部文本断言/golden；admission、取消直测、shell/apps 旧断言；新增模式用例 |
| `internal/mcp/http_test.go` | 新增 header 模式、非法/多值 header、跨请求模式隔离 |
| `internal/mcp/tree_text_test.go` | `omitted` 用例改为大块完整输出/`ok=false` 兜底；保留 tree/inspect round-trip |
| `scripts/mcp-probe/*` | 探针默认 `both`；可选 `-result-mode`/header；text 模式新增文本块检查，不替换既有 structured 检查；图片检查不变 |
| `docs/mcp.md` | §3 结果投影、模式表、`result v1` 语法、结构化形状、取消 details、无省略、尾部/截断、模式安全、`is_error`、字段分类 |
| `docs/runbook.md` | §10 增加 HTTP header / stdio flag/env 说明与示例 |
| `docs/interface.md` | §2 `details` 白名单限定为 `/api/*`；MCP `structuredContent` 可含 `mcp.*` 保留键 |
| `maintenance/decisions.md` | 新增 D33：模式与文本/结构化等价；D32 的 24/32 KiB 省略口径被本提案取代，树块语法保留 |
| `maintenance/verification/mcp-result-modes.md` | 实现后写验证记录（实现提交、环境、命令、结果、未覆盖项） |
| `release/versions.json` | 不立即改；发布时按发行流程递增 `release_version`，`contract_version` 不变 |
| `release/manifest` 生成 | 产物仍按发行流程重建；`CatalogFingerprint` 不变；清单记录新发行号/源码提交，可选记录支持的 `result_modes`/默认值 |

### 6.2 配置与取值

| 项 | 取值 | 规则 |
| --- | --- | --- |
| 默认模式 | `both` | 零值即 `both`；现有调用方不设置时通道组合兼容 |
| HTTP 头 | `X-Mobile-Use-Result-Mode` | 只接受 `both`/`text`/`structured`；`Header.Values` 恰好一个；缺失取默认；多值/空白/非法 400 |
| stdio flag | `-result-mode` | 优先级最高；用 `flag.Visit` 判断显式设置；显式空/非法启动失败（exit 2） |
| stdio env | `MOBILE_USE_MCP_RESULT_MODE` | flag 未显式设置时读取；显式空/非法启动失败；未设置用默认 |
| 请求头转发 | 不转发 | 仅适配器本地投影；不进入服务请求、不进入日志原值 |
| 模式粒度 | HTTP 每请求；stdio 每进程 | HTTP 不建立模式会话状态；同一进程可混合 |
| 配置优先级 | flag > env > 默认 | HTTP 头 > 默认；无配置文件字段 |

### 6.3 日志与可观测性

- `mcp.tool` 每次调用记录 `result_mode` 枚举（`both`/`text`/`structured`）；低基数，不记录
  正文、树、shell 输出、apps 名称、消息正文或原始 header 值。
- `mcp.start` 记录默认 `result_mode`（stdio 默认/HTTP 默认）；HTTP 每请求模式由 `mcp.tool`
  记录，不额外新增 mode 日志/指标。
- 非法 HTTP header 在 `http.go` 返回 400；不进入 `mcp.tool`，不记录原始 header 值。
- stdio 非法模式在启动时写 stderr 并以 exit 2 退出；不启动 MCP。
- `eventlog` 白名单只增加 `result_mode` 枚举字段；不增加正文、树、输出等内容；
  `TestNoContentInLogs` 同时覆盖 `mcp.tool` 与 `mcp.start`。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| **硬契约（G0，长期）** | `text`/`structured` 是同一 `AgentResult = (A,IsError,ImageContent)` 的两个无损编码，含同一 `ImageContent`；事实集完全相同；`TestAgentResultLosslessParity` + `TestCanonicalJSONCoversManifest` + 全工具 fixture 强制；任何只改一侧的优化视为缺陷 |
| 唯一权威（engine） | 不变；模式只影响适配器结果投影 |
| STOP/取消 | 取消仍走 `cancelled()`；canonical 合成/补 `error.details.mcp.*`；取消 `is_error=true`（G13） |
| 单向消费 O/P | 不变；模式选择不触碰 O/P |
| 提交条件化 | 不变；投影在结果返回前完成，不改变提交 |
| 严格接入 | 模式头/flag 按白名单校验；非法值在工具执行前拒绝 |
| 三态/省略 | 文本 `result v1` 从 canonical JSON 生成，presence 与 `Result` marshaler 一致；无 `omitted` |
| 事实分层 | 文本/结构化同源 canonical JSON；`IsError`、图片独立表达；`is_error` 头与 envelope 一致 |
| 日志白名单 | 只增加 `result_mode` 枚举字段；不记录内容 |
| 文档边界 | `docs/` 只写已实现行为；提案阶段不写实现结论 |
| 单一版本源 | `contract_version` 不变；`release_version` 发布时递增；指纹不变 |

### 6.5 契约与文档影响

- `docs/mcp.md` §3 重写：
  - 模式表与选择方式；默认 `both`；非法模式。
  - `structuredContent` 形状：`Result` JSON；取消时 `error.details.mcp.*`；`mcp.*` 是 MCP 层
    保留键，`/api/*` 永不产生。
  - `result v1 is_error=...` / `end result` 与 `diagnostic v1 is_error=true` / `end diagnostic`
    两套头尾；字段分类（token/json-string/json）、块/空行规则、JSON Pointer 兜底、无省略、
    确定性、大小/截断边界。
  - `both`/`text`/`structured` 的 `Content`/`StructuredContent` 组合；`isError`（含取消
    override）/图片一致；`isError` 表增加“取消（含 act executed 后取消）| true”，并说明
    取消只改 `isError`，不改变已确认的 `outcome` 事实。
  - 模式不是脱敏开关；同一事实集。
  - **硬契约段落**：`AgentResult = (A, IsError, ImageContent)`；`text` 完整编码 =
    `result v1` + `ImageContent`；`structured` 完整编码 = `structuredContent` +
    `CallToolResult.IsError` + `ImageContent`；`diagnostic` 是辅助 TextContent，不属于完整编码；
    两种编码必须还原同一 `AgentResult`（含同一 `ImageContent`），事实集合完全相同，格式可以
    不同；派生计数（`nodes`/`changes`/`events`/`items`/`output_bytes`/`tree_scope`）不是独立
    事实，必须可由 A 计算。这是长期契约，后续工具/字段/优化不得只进入一侧。
  - 无 `outputSchema`；公共工具目录与指纹不变。
- `docs/runbook.md` §10 增加 header/flag/env 示例；手机 HTTP 适配器无需部署变更（默认 both）。
- `docs/interface.md` §2 修改 `details` 白名单为“仅约束 `/api/*`”；MCP `structuredContent`
  的 `error.details` 允许 `mcp.*` 保留键。`/api/*` 字段/错误码不变，且永不产生 `mcp.*`。
- `maintenance/decisions.md` 新增 D33；D32 的 24/32 KiB 上限/`omitted` 被本提案的
  “可表示即完整可读块，否则 JSON 兜底”取代，树块语法/确定性保留。
- `maintenance/status.md` 在实现/验证后更新 MCP 行；本提案阶段不改。
- `release/versions.json` 发布时递增 `release_version`；`contract_version` 不变；`source_commit`
  由发布脚本生成；发布清单仍重建并记录新发行号/同一指纹。
- 文档审计：实现提交涉及 `docs/` 时必须由独立只读 subagent 审核后再提交（AGENTS.md 第 15 条）。

### 6.6 兼容性与迁移

- `both` 的通道组合不变（仍有 `TextContent` + `StructuredContent` + `ImageContent`），但文本
  格式变为 `result v1`。这是 **MCP 文本投影的 breaking change**；发布说明必须写明。
- 取消的 `is_error` 由 per-tool 结果改为一律 true（G13）；非取消分支不变。这是 MCP 结果
  行为变化，发布说明必须写明。
- 需要旧文本的调用方应固定适配器版本；本提案不保留旧文本 codec。
- 测试/探针迁移清单：
  - `internal/mcp/server_test.go`：全部文本断言/golden；至少包括 `TestStrictAdmission`
    （`:429`）、`TestStartProjectionWithFullTree`（`:501–508`）、取消直测（`:846`、`:886`）、
    shell/apps（`:733–736`、`:757–759`、`:777–779`）等；用
    `grep -nE 'requireTestContains|requireTestNotContains' internal/mcp/server_test.go` 逐条迁移。
  - `internal/mcp/tree_text_test.go`：**所有** `renderTreeText`/`renderInspectionText` 直测调用
    都要适配 `(block, ok)`：`TestTreeTextFullGolden`、`TestTreeTextDeltaGolden`、
    `TestTreeTextDeltaExplicitValues`、`TestTreeTextInspectTree/Subtree/Query`、
    `TestTreeTextIsPure`、`TestTreeTextRoundTrip`、`TestTreeTextCounts`、`TestTreeTextEscapes`、
    `TestTreeTextOmitsWholeBlock`（改名/改大块完整输出）、`TestTreeTextTrailer`、
    `TestTreeTextEmptyTree`、`TestTreeTextNoTree`、`TestTextCarriesTreeBlock`（`:582–651`）；
    同时迁移 `\n\ntree v1`/`\n\ninspect v1` 空行断言。
  - `internal/mcp/result_text_test.go`：`TestTextCodecRoundTrip` 使用
    `DecodeText(EncodeText(A,isError)) == (A,isError)`；新增空行/块交错 golden。
  - `scripts/mcp-probe/probe_test.go`：文本 fixture 与 `treeBlockText` 前缀。
  - `scripts/mcp-probe/checks.go`：`treeBlockText` 的 `tree v1` 前缀检查适配块前空行规则。
  - `scripts/mcp-probe/result.go`：`structuredContent` 仍按 `Result` 解码；取消 `mcp.*`
    在 `error.details` 内，未知字段忽略；text 模式不依赖 `structuredContent`。
- 兼容性测试：`TestBothTextFormatChanged` 固定新格式；`TestLegacyTextNotAccepted` 明确缺少
  `result v1`/`end result` 的旧文本不被当作完整 `result v1`（如需过渡，另立提案）。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 只读文本的调用方能看到 `shell.output`、`apps.items`、`diagnosis.timeline`、完整树/补看内容，
  不再因适配器侧上限/`omitted`/ref 问题丢失信息。
- 结构化调用方拿到完整 `Result`（含取消 details），不再依赖文本补充。
- `both`/`text`/`structured` 三种模式的 agent 可见事实集一致；模式只改格式。
- **硬契约**：`text` 与 `structured` 是同一个 `AgentResult = (A,IsError,ImageContent)` 的两个
  无损编码，含同一 `ImageContent`；派生计数不是独立事实，可由 A 计算；后续工具/字段/优化
  若只改一侧，会被 parity/manifest 测试拒绝，而不是靠人工记忆。
- 支持结构化的调用方可用 `structured` 避免文本冗余；只读文本的调用方可用 `text` 避免结构化
  冗余；未知调用方默认 `both` 保持兼容。
- 新增字段/工具时，必须更新 marshaler/清单/codec/fixture；`TestCanonicalJSONCoversManifest`
  与 `json v1` 兜底保证不会静默遗漏。
- 宿主截断可由 `end result`/`end diagnostic` 发现，而不是把半份结果当完整结果。
- `is_error` 头让无 `error`/`outcome` 的 per-tool 失败在文本中也可见。

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| 宿主只读 `content` 却选择 `structured` 模式 | 该模式的契约就是由结构化消费方使用；只读文本宿主应选 `text`/`both` |
| 宿主截断很长的 `TextContent`（含 128 KiB 观察树/128 KiB inspect 全树/32 KiB subtree/query/64 KiB shell/32 KiB apps） | 服务端不截断；`end result`/`end diagnostic` 只能暴露截断，不能恢复被宿主丢掉的内容；调用方应改用 `structured` 或 `inspect` 收窄 |
| 宿主拒绝空 `content` 的成功结构化结果 | 某些宿主实现限制；此类宿主应使用 `both`/`text`，或由宿主修复 |
| `/api/*` 行为变化 | 本提案不改服务契约 |
| 图片字节进入文本/结构化内容 | 仍只走 `ImageContent`；base64 不进入 `A` |
| 敏感字段的脱敏 | 模式不是脱敏开关；如需要，必须在两种模式之前统一脱敏 |
| 未来工具发布 `outputSchema` 后仍用 `text` 模式 | MCP 要求有 `outputSchema` 时必须提供结构化结果；公共目录暂不发布 `outputSchema` |
| 文本可读性等同旧版 | 受控 token 尽量保留，但任意字符串 JSON、增加头/尾/块；旧文本不是版本化解析契约 |
| 新字段自动出现 | 手工 marshaler/清单必须更新；测试会失败而不是静默通过 |

### 7.3 成本

- 实现：新增模式/值/文本 codec/decoder/测试，约数百行；`renderText` 与 `tree_text.go` 需要
  重构；这是本提案的主要成本。
- 文本体积：不再省略树/补看；可读块通常比 JSON 小，但大结果文本仍可能到服务端上限的
  64/128 KiB 量级；`both` 默认同时带文本 + 结构化副本；只读文本调用方/默认 both 调用方
  承担 token 成本。验收必须记录实际文本字节数，并以 128 KiB 观察树/128 KiB inspect 全树/
  32 KiB subtree/query fixture 验证。
- `both` 默认仍双份；支持结构化的调用方应选择 `structured`，只读文本调用方选择 `text`。
- 运行期：每个结果多做一次 canonical JSON 编码/文本编码；相对设备 I/O 可忽略。
- 发布：`CatalogFingerprint` 不变；发行号递增；文档/测试/验证记录增加。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| `TestResultModeParse` | 三值接受；空/大小写/别名/空白拒绝 |
| `TestResultModeDefaultAndPrecedence` | `flag.Visit` 区分未设置/显式空；flag > env > default；非法启动失败 |
| `TestHTTPResultModeHeader` | 三值生效；缺失默认 both；`Header.Values` 多值/空白/非法 400；跨请求互不影响；auth/Origin 不变 |
| `TestStructuredModeOmitsFullText` | 成功 `Content` 非 nil 空切片（JSON `[]`）/只有图片；`StructuredContent` 完整；`isError` 有 `diagnostic v1` 最小诊断文本 |
| `TestTextModeOmitsStructuredContent` | `StructuredContent == nil`；文本完整；图片仍走 `ImageContent` |
| `TestBothModeParity` | 同一 fixture 的文本解码与结构化解码相等；`is_error` 与 `IsError` 一致 |
| **`TestAgentResultLosslessParity`** | 硬契约：每个 fixture 的 `text` 编码与 `structured` 编码都无损还原同一 `(A,IsError,ImageContent)`；`structured` 侧只从 `StructuredContent` + `CallToolResult.IsError` + `ImageContent` 还原，`diagnostic v1` 不参与 A 还原（另由 `TestStructuredMinimalErrorText` 覆盖）；不存在任何 agent-visible 字段/事实只在一侧；`both` 同时提供两种完整编码 |
| `TestTextCodecRoundTrip` | 每个工具/分支 fixture 的 `DecodeText(EncodeText(A,isError)) == (A,isError)` |
| `TestTextCodecPresence` | `null`/缺失/`[]`/`""`/`false`/`0` 区分；与 `Result` marshaler 一致 |
| `TestTextCodecEscapes` | token 仅受限字符；任意字符串 JSON；引号/反斜杠/换行/制表/控制字符/Unicode/非 UTF-8；无裸 CR；树块裸 token 例外 |
| `TestTextCodecLargeBlock` | 128 KiB 观察树、128 KiB inspect 全树、32 KiB subtree/query、64 KiB shell、32 KiB apps：可读块完整输出，无 `omitted`，解码完整；记录实际文本字节数 |
| `TestTextCodecUnrepresentableFallback` | 非法 ref/未知 Node 成员/空 scope 等：`json v1` 兜底，不省略，解码与结构化一致 |
| `TestTextCodecMetadataPaths` | full/delta 两种树：`observation.id` 与树块头 `id` 不重复设路径；delta 头 `scope` 不设 `observation.tree.scope`；`inspection.origin` 只在 `inspect` 块出现；整体 `/inspection` 兜底不含 origin；`tree_scope` 在 subtree/query 为派生；`nodes/changes/events/items/output_bytes` 只校验不设路径 |
| `TestTextCodecBlockInterleaving` | shell 块后仍有 error/recovery 行；start 的 session 行后接 observation 块、再继续字段行；diagnosis timeline 块后接 error 行；apps 块后接 timeline 块（block→block）恰好一个空行；首 item 为块时头后恰好一个空行；块前后空行规则符合 §5.3.1 |
| `TestTextCodecTrailer` | 缺 `end result`/重复头/重复路径失败；任何路径无 `omitted`；`diagnostic v1` 缺 `end diagnostic` 失败 |
| `TestCanonicalJSONDeterministic` | `Error.Details`/`Delta` map fixture 连跑多次字节相同 |
| `TestCanonicalJSONCoversManifest` | 比较注入 `mcp.*` 之前的 base Result JSON 与字段清单；缺一失败；`error.details.mcp.*` 作为 adapter-owned 单独测试 |
| `TestFutureFieldFallback` | 带额外 JSON 成员的 composite 走 `json v1`/item/event 完整对象；解码保留数据 |
| `TestAdmissionParity` | admission 走 `result v1`/`error.code=invalid_request`/`is_error=true`；三模式一致 |
| `TestAPINeverEmitsMCPDetails` | `/api/*` 结果/HTTP fixture 不含 `details.mcp.*`；`mcp.*` 只在 MCP 适配器层注入 |
| `TestReadOnlyCancelWithoutError` | 只读取消 + 已有服务结果无错误：合成 `call_cancelled` + `mcp.*`；`IsError=true`、`is_error=true`、三模式一致 |
| `TestCancellationDetailsParity` | work/read-only × 有服务结果无错误 / 无服务结果 / 服务错误存在：`error.details.mcp.*` 都在；`is_error`/`CallToolResult.IsError` 一致；text/structured 的 `A` 相等；与 `end`/`recovery` 事实一致；“无服务结果”分支同时由既有 `TestCancellationWorkToolDirect` 覆盖 |
| `TestStructuredMinimalErrorText` | 覆盖 `observe` 无 O、`act not_executed/uncertain`、`apps nil`、admission 等 per-tool `isError`；文本以 `diagnostic v1 is_error=true` 开头、`end diagnostic` 结束、含可解释字段 |
| `TestAllToolResultFixturesParity` | 12 工具及下列分支三模式等价；每分支至少一个 fixture |
| `TestResultFieldManifest` | 每个字段已分类/有 fixture；与 encoder/decoder 共用同一 `resultFieldKind` 表；新字段未分类失败 |
| `TestReverseControl` | 删除 shell.output 文本编码、删除结构化字段、删除尾部、删除 admission 统一、删除取消 `isError` override、只从 text 或 structured 一侧删除 agent-visible 字段（如 shell.output）六类改动各自使对应测试失败（最后一类必须使 `TestAgentResultLosslessParity`/`TestCanonicalJSONCoversManifest` 失败） |
| `TestCatalogFingerprintUnchanged` | 工具目录指纹与基线一致；无 `outputSchema`；描述/schema 未变 |
| `TestNoContentInLogs` | `mcp.tool` 只含 `result_mode`/既有白名单字段；`mcp.start` 含默认 `result_mode`；不含正文/输出/名称/原始 header |

分支矩阵至少覆盖：

- admission 错误（缺字段/未知字段/类型错误/null）。
- `status`：`not_seen`、`request_closed`、`can_start`、blocking。
- `start`：成功 full 树、已建会话但首个观察失败（`isError=false`）。
- `observe`：full/delta/无树/只读图/`missing`/无 O。
- `act`：`executed`、`not_executed`、`uncertain`、`target_changed`、带树/图；另加
  `outcome=executed` + `cancelled`（验证 G13 override 与 outcome 事实分层）。
- `inspect`：tree/subtree/query/空/`truncated`/`inspection_too_large`/`content_unavailable`。
- `keepalive`：active/ended/expired/无会话。
- `end`：cleanup pending/unconfirmed/失败。
- `apps`：空/正常/truncated/error。
- `open_app`：reused/state_preserved/错误。
- `notify`：saved/unavailable。
- `shell`：exited/非零/timeout/cancelled/not_started/unknown/output null/output_error/truncated。
- `diagnose`：own/reference/unavailable/timeline/truncated。
- 取消：work/read-only，有/无服务结果，有/无 `Error`。
- 图片：成功/只读/失败/无图。
- 大值：树/补看/shell/apps 上限附近。

### 8.2 实机/系统级

- 构建：`GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`；主机
  Windows 构建/单测；手机 HTTP 适配器按发布清单部署。
- 模式：
  - HTTP：带 `X-Mobile-Use-Result-Mode: text|structured|both` 调用 12 工具；无 header 走默认；
    非法/多值 header 400；确认服务端未收到该 header。
  - stdio：`-result-mode`/env 两种入口；显式空/非法启动失败；默认 both。
- 内容：
  - `text`：`shell` 输出、`apps` item、`diagnose.timeline`、`observe` 大树/`inspect` 大补看可见；
    无 `structuredContent`；图片 `ImageContent` 正常；`is_error` 可见；128 KiB 观察树/
    128 KiB inspect 全树/32 KiB subtree/query 完整可读块，记录实际文本字节数。
  - `structured`：`shell.output`/`apps.items`/`diagnosis.timeline`/树完整；成功无完整文本；
    `isError` 有 `diagnostic v1` 最小诊断（含 per-tool 可解释字段）；图片正常。
  - `both`：文本解码与结构化解码一致；大树/大补看完整可读块；无 `omitted`；记录文本+结构化
    双份的字节成本。
- 边界：服务端上限附近（64 KiB shell、32 KiB apps、128 KiB 观察树/128 KiB inspect 全树、
  32 KiB subtree/query）；宿主截断由 `end result`/`end diagnostic` 缺失被发现；截断后应改用
  `structured` 或 `inspect` 收窄；非法 ref/未知 composite 走 JSON 兜底。
- 取消：work/read-only，有/无服务结果，有/无 `Error`；`is_error=true`、
  `error.details.mcp.*` 与 `end`/`recovery` 事实一致。
- 探针：`scripts/mcp-probe` 默认 `both` 保持既有 structured 检查；显式 `text`/`structured`
  只增加投影检查，不替换既有检查；text 模式检查 shell/apps/timeline 文本。
- 未执行项：`-race`（Windows 无 C 编译器）、外部宿主截断/空 content 行为如无合适宿主则记录
  未执行，不写成已验证。

### 8.3 记录

实现后写 `maintenance/verification/mcp-result-modes.md`，包含：

- 实现提交（多个提交时列最后一个与 §11.1 全部）；
- 环境、构建命令、Go/JSON 版本、传输方式；
- 单测/探针/实机命令与真实结果；
- 已实现 / 已单测 / 已实测分层；
- 未执行项与原因；
- 与提案不同或未执行项。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| **硬契约（G0，长期）** | `AgentResult = (A,IsError,ImageContent)`；`text` 完整编码 = `result v1` + `ImageContent`；`structured` 完整编码 = `structuredContent` + `CallToolResult.IsError` + `ImageContent`；`diagnostic` 辅助，不属于完整编码；两种编码事实集完全相同、含同一 `ImageContent`；派生计数不是独立事实；任何工具/字段/优化不得只进一侧；由 `TestAgentResultLosslessParity`/`TestCanonicalJSONCoversManifest`/全工具 fixture 强制，长期不改 |
| 模式 | `both`（默认）/`text`/`structured` |
| HTTP 选择 | `X-Mobile-Use-Result-Mode` 每请求；`Header.Values` 恰好一个；非法/多值/空白 400 |
| stdio 选择 | `-result-mode` / `MOBILE_USE_MCP_RESULT_MODE`；`flag.Visit` 区分显式空；flag > env > both；非法启动失败 |
| 文本格式 | `text`/`both` 用 `result v1 is_error=…`/`end result`；`structured` 最小诊断用 `diagnostic v1 is_error=…`/`end diagnostic`；token/json-string/json；item 可交错；块前恰好一个空行、块后无额外空行；无 `omitted` |
| 结构化形状 | 仍为 `Result` JSON；取消事实在 `error.details.mcp.*`；不新增顶层字段 |
| 等价口径 | `DecodeText(EncodeText(A,isError)) == (A,isError)`、`DecodeStructured(A) == A`；`is_error` envelope-only；canonical 语义/presence/null/顺序 |
| 树/补看大小 | 取消 24/32 KiB 适配器上限；可表示即完整可读块，不可表示/非法才 JSON 兜底 |
| 树/补看 API | `renderTreeText`/`renderInspectionText` 返回 `(block, ok)`；`!ok` 时 `json v1` |
| 取消事实 | `mcp.cancelled`/`mcp.cancellation` 保留键；只读取消无错误时合成 `call_cancelled`；取消一律 `is_error=true`（G13） |
| admission | 删除 `invalid_request:` 特判；统一走 canonical + 模式投影 |
| 最小诊断文本 | `structured` 的 `isError` 文本用 `diagnostic v1`/`end diagnostic`，含 G11 字段；不要求完整内容 |
| `both` 文本 | 与 `text` 共用 `result v1`；不保留旧文本 codec；格式变化写入发布说明 |
| 未来字段 | 必须更新 marshaler/manifest/codec/fixture；`TestCanonicalJSONCoversManifest` 比较注入 `mcp.*` 前的 base JSON；强制 |
| `outputSchema` | 不发布；未来需发布时重新提案 |
| 模式安全 | 不是脱敏/截断开关；三模式事实集相同 |
| 目录/契约 | 不改工具目录/描述/schema/`/api/*`/`contract_version`；指纹不变 |
| `mcp.*` 命名 | 固定为 MCP 适配器保留命名空间；`/api/*` 永不产生；文档明确 |
| 文本 decoder | 仅测试 oracle，不公开；只解析 `result v1`（`diagnostic v1` 由独立最小检查） |
| 日志 | `mcp.tool` 每次记录 `result_mode`；`mcp.start` 记录默认；不记录原始 header/内容 |

### 9.2 未决

| 项 | 谁来定 / 何时定 | 说明 |
| --- | --- | --- |
| `structured` 成功 `Content=[]` 的宿主接受度 | 实现/验证 agent，实机验证时 | 协议允许非 nil 空数组；若发现宿主拒绝，记录并让宿主改用 `both`/`text`，不改变模式契约 |
| 大文本（128 KiB 观察树/128 KiB inspect 全树/32 KiB subtree/query）宿主截断行为 | 验证 agent，实机验证时 | 服务端不截断；记录实际文本字节数、`end result`/`end diagnostic` 缺失的观测，以及“改用 structured/inspect”的建议 |
| `result v1` 具体 golden 字节 | 实现 agent，实现前 | 语法/字段分类已在 §5.3 定；golden 只固定格式，不改变事实集 |
| 发布清单是否记录 `result_modes` | 人，发布时 | 指纹不变；可选增加支持模式/默认值，不改变发布契约 |

未决项不影响 §2 的目标和 §4 的判据；实现前只允许在 golden/发布清单字段层面收敛，不得改变
模式、事实集、无省略、结构化形状或 `/api/*`。

## 10. 实施顺序

0. **硬契约门禁（分阶段，可编译）**：
   - **0a 红测**：用当前 `both` 路径写 `TestCanonicalJSONCoversManifest`（base Result）+
     `TestBothModeParity` 骨架；断言“字段只进一侧”“`IsError`/`ImageContent` 不一致”必须
     失败；当前文本不完整应红。
   - **0b 绿测**：先用当前文本已完整承载的最小 fixture（如 `status` 无树/无 shell）验证
     “格式不同但事实相同”、`both` 双份、同一 `ImageContent` 必须通过；此时
     `TestAgentResultLosslessParity` 只覆盖当前 `both` 的字段/图片一致性。
   - **0c 扩三模式**：step 3（文本 codec）/step 5（模式投影）落地后，把
     `TestAgentResultLosslessParity` 扩到 `text`/`structured`/`both`；完整三模式矩阵在此之后，
     不在 step 0 引用未来编码器。
   该门禁是 G0 的长期门禁；后续任何 codec/字段/优化都必须保持绿。
1. **模式骨架**：`ResultMode`、解析、HTTP header 校验（`Header.Values`）、`AddTool` 从
   `Extra.Header` 读取、stdio flag/env（`flag.Visit`）、`Options.DefaultResultMode`；加模式
   选择测试，不改变结果投影。
2. **canonical 值**：确定性 JSON、清 base64、取消 `details`/只读合成、取消 `isError` override；
   加结构化取消、`IsError` 一致性与确定性测试。
3. **文本 codec 基线**：先实现 `result v1 is_error`/`diagnostic v1` 两套头尾、字段分类、
   item 交错/空行规则、`key=value`/`json v1` 兜底；加 decoder 与全字段 fixture parity
   （此时可暂时没有可读块，文本是 JSON 风格）。
4. **可读块**：接入现有树/补看块（`(block, ok)`、无大小上限）；新增 shell/apps/timeline 块；
   加 golden/128 KiB 大值/不可表示/无 `omitted` 测试。
5. **模式投影**：`handle` 按模式组装非 nil `Content`/`StructuredContent`；`structured` 最小
   `diagnostic v1`；`isError` 取消 override；admission 统一；加三模式矩阵。
6. **清理旧路径**：移除 `renderText` 单文本路径/`invalid_request:` 特判/`omitted`；更新
   `server_test.go` 全部文本断言、取消直测、`tree_text_test.go` 全部 `(block, ok)` 直测调用、
   `scripts/mcp-probe/probe_test.go`/`checks.go`。
7. **探针/文档/决策**：更新 `scripts/mcp-probe`、`docs/mcp.md`、`docs/runbook.md`、
   `docs/interface.md`、`maintenance/decisions.md`；跑文档审计门禁。
8. **验证**：全量 Go 测试、构建、HTTP/stdio 实机、模式/边界/取消；写验证记录；按流程
   先实现提交、再归档提案；发布版本按发行流程递增。

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/mcp-result-modes.md`（归档到
`implemented/` 后该相对链接生效；当前为占位）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- `/api/*` 字段、错误码、`contract_version`、引擎/会话/O/P/设备行为。
- **`AgentResult` 硬契约**：`AgentResult = (A,IsError,ImageContent)`；`text` 完整编码 =
  `result v1` + `ImageContent`，`structured` 完整编码 = `structuredContent` +
  `CallToolResult.IsError` + `ImageContent`；`diagnostic` 辅助；两种编码事实集完全相同、
  含同一 `ImageContent`，格式可以不同；派生计数不是独立事实；不提供弱化、关闭或绕过它的
  模式/配置。
- 工具目录、工具名称、描述、入参 schema、工具数量、`CatalogFingerprint`。
- `isError` 的非取消 per-tool 判据；取消按 G13 override 为 true。
- 图片载体：`ImageContent` 与图片元数据；base64 不进入文本/结构化内容。
- 日志白名单只增加 `result_mode` 枚举字段；不记录正文、树、shell 输出、apps 名称、消息正文或凭据。
- 除新增模式与 `both` 文本格式外，不改变工具行为、目录、schema 或 `/api/*`。
- `outputSchema`：仍不发布。
- `scripts/build-release.ps1`/发布清单的字段与指纹；发行号按发行流程递增。
