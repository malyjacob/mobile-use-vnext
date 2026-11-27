package mcp

import "encoding/json"

// toolDefinition is one registered MCP tool. The input schema is explicit and
// is never inferred from a Go type: the protocol decoders, not the SDK, decide
// admission. readOnly tools never allocate or send an operation ID and never
// issue an end when their caller disappears.
type toolDefinition struct {
	name        string
	description string
	schema      json.RawMessage
	readOnly    bool
	// snap marks the one-tool image read that still receives a pre-allocated
	// operation ID, never issues an end on cancellation and publishes the
	// public read-only annotation.
	snap bool
}

func (d toolDefinition) readOnlyHint() bool {
	return d.readOnly || d.snap
}

// treeTextGuide is the complete public syntax legend for readable results and
// tree blocks.
// It is published exactly once, at the head of the server instructions, and is
// never repeated inside a tool description.
const treeTextGuide = "结果文本：text/both 以 `result v4 <ok|err> [op=…] [state=…] [outcome=…] [phase=…]` 开头、`end result` 结尾。" +
	"常规成功（`ok` 且无 `state=`）省略 `outcome=executed`；非 `executed`、错误结果与带 `state=` 的重放摘要一律显式输出 `outcome=…`。" +
	"`op=`/`at=` 只是编号/UTC 时序提示，不替代引擎对会话/显示代次/期限的校验。" +
	"`text`/`both` 是面向调用的紧凑投影；`structured` 保留完整审计字段与全部三态、空集合语义。\n" +
	"观察为单行 `obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [visual=<token>] [change=<token>] [hint=<token>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=…]`：" +
	"`obs ` 后首个不含 `=` 的字段是完整 O；`status=complete` 省略，`partial`/`failed` 显式；" +
	"`fg` 写 `<包名>#<task_id>`、`<包名>`、`#<task_id>`、`present`、`none` 或 `unknown(<reason>)`；" +
	"`visual` 为视觉提示：`loading`/`image_heavy(<pct>%)`（45–100）/`media_surface`/`sparse_tree`。`loading` 与 `wait_for` 终帧的 `sparse_tree` 在 `delta` 也保留，其余同态 `delta` 省略。" +
	"`change` 仅见于 `act` 常规成功且比较前后树：`unchanged` 前后紧凑树与完整树均相同，`detail_only` 紧凑树相同但完整树细节有变，`matched_base` 前后树内容或公开语境有变且零差异命中历史基线。" +
	"`hint` 只由已发布字段推导。`loading` 提示 `hint=wait_for`；`sparse_tree` 在非 `wait_for` 终帧且 `change` 非 `unchanged`/`detail_only` 时也提示等待，即使带图。" +
	"若 `wait_for` 终帧仍稀疏，或稀疏且 `change=unchanged`/`detail_only`，无有效图时转 `hint=snap`。其他视觉提示或 `unchanged`/`detail_only` 也仅在无有效图时提示截图。" +
	"`hint=snap` 用同一 O 截图核对；`hint=wait_for` 用 wait_for 等条件或稳定。" +
	"`screen` 仅在带 `session` 或无树帧输出；" +
	"`rot` 在非零旋转时始终输出；所有 `HH:MM:SS` 均为 UTC 秒级时间；" +
	"`origin`/`revision`/`actionable_until` 只在 `structured` JSON 中保留。" +
	"`obs.image` 与顶层 `image` 同字段同顺序；" +
	"`obs.error <code> \"<message>\"` 子行只在字段存在时紧跟。\n" +
	"其他块：单行 `image [<W>x<H>] [read_only] [media=<非 jpeg>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [at=HH:MM:SS]`、" +
	"`session <state> mode=… origin=… [started=…] [expires=…] [ended=…] [reason=…]`、" +
	"`wait trigger=… elapsed=<ms>ms`、`cleanup …`、`error <code> \"<message>\"`、" +
	"`recovery entry=<entry> ref=<精确 O/P 编号|O|P|none> [input=…] [cause=…]`（ref 已是精确编号）。" +
	"`app.items` 以 `app_items v1 kind=find|list count=<n>` … `end app_items` 交付：" +
	"`kind=find` 写 `<package> \"<name>\"`，`kind=list` 行写 `#<task_id> <package> <ownership>[ origin_display=<显示号>]`。" +
	"`app.tasks` 以 `app_tasks v1 count=<n>` … `end app_tasks` 交付，行 " +
	"`#<task_id> <outcome>[ recents_cleaned=…][ error=<code> \"<message>\"[ details=<JSON>]]`。" +
	"诊断有计数时输出 `diagnosis.error_counts=<JSON>`，排在 `diagnosis.issues=` 之后、`diagnosis.timing_ms=` 之前。" +
	"无法无损编码的字段保留 `json v1 path=\"<绝对 JSON Pointer>\" value=<原值>`。\n" +
	"普通树：当前树=`base` 指向的 full+本次 delta。" +
	"full 头为 `tree v5 full nodes=<n>`；delta 头为 " +
	"`tree v5 delta base=<O|_<序号>> changes=<n>`；头后空行进入正文、以 `end tree` 结尾。" +
	"同一 base 的每条 delta 独立应用到该 full。" +
	"base 可写 `_<尾号>`：与真实 base 在最后 `_` 前同前缀、尾号为正十进制；" +
	"否则写完整 O。`base=_72` 只供读树，调用用完整 O/P。\n" +
	"普通节点每层恰写一个 ASCII 空格，根深度为零；差分 `+ `、`= ` 只在定义根行出现、不计层级。" +
	"节点行为 `n<ref> [<role>] [l,t,r,b|x,y|@] \"label\"… value=… hint=… " +
	"tap|long_press|text|swipe enabled=… focused=… selected=… protected=… checked=… axis=… resource=… [truncated=true] context`。" +
	"`truncated=true` 只在 compact 有动作非 `input` 节点截断时出现，`false` 省略、detailed 禁止。" +
	"`n<ref>` 即 target.ref；标签是 JSON 串。" +
	"`root`/`scroll` 交付矩形 `l,t,r,b`（`right>=left`、`bottom>=top`），其余交付中心点 `x,y`；" +
	"未知写 `-`，缺失列表示隐藏。" +
	"`@` 表示与同一定义中最近已交付几何的严格祖先同形同值；无此祖先无效。" +
	"每个 `+`、`=` 子树独立判断，不继承其他定义/base。\n" +
	"角色：`button` 默认 `tap`，`input` 默认 `text`，`scroll` 默认 `swipe`。" +
	"动作可按源顺序完整恢复时省略默认词；默认动作缺失时保留其余动作并在动作末写 `!tap`、`!text` 或 `!swipe`。" +
	"无默认词或对应 `!动作` 时其余动作须符合源顺序，再按 `tap,long_press,text,swipe` 插回默认动作；" +
	"无法恢复时写完整显式列表；出现默认词时保留顺序。" +
	"动作不可既显式又否定；其他角色否定词与无祖先 `@` 无效。" +
	"省略 `role` 时按 ref 后首列恢复；未隐藏几何是四元矩形、二元点或 `@`，" +
	"首项为引号标签时默认 `text`，其他列或行尾默认 `view`。" +
	"显式角色覆盖默认；首项为标签或角色词的 `view`、无标签或带几何列的 `text` 必须显式写角色。\n" +
	"complete 定义的状态位省略已知默认：`enabled=true` 省略，`enabled=false`/`enabled=null` 显式；" +
	"`focused`/`selected`/`protected`/`checked` 的已知 `false` 省略，`true`/`null` 显式。" +
	"`set` 行不同：出现的状态字段一律保留显式 `false`/`true`/`null`，未出现才表示不修改；" +
	"`truncated=true|false` 同样显式保留，省略表示未修改。" +
	"`~` 保留原数组/串/`null`/三态；`actions=[]`、`bounds=[x,y]|[l,t,r,b]|null|hidden` 原样，" +
	"`@`、无括号几何与默认动作不用于 set。" +
	"`nodes` 计展开链后节点数，`changes` 计五类顶层声明；零变化 delta 正文为空。\n" +
	"compact 净体：全裁无标注非交互 `image` 叶；裁剪被祖先复合标签覆盖的纯被动文本叶；" +
	"纯被动文本子叶的无字可点击 `view` 升为 `button`（保留多段 `label`，清 `resource`）；" +
	"`input` 的 `label==value` 时省略 `label`；裁掉宽/高 `<=4px` 无字露边 `view`；" +
	"`resource` 取按 `:`、`/` 切分后最后的非纯数字短段。" +
	"所有隐藏匿名容器（无文本/动作/`resource`、状态全默认、至少 1 子）按序扁平化透传，" +
	"原始无子合成叶例外；变空的匿名容器清理。" +
	"公开 `label` 首尾空白净化（含 NBSP/纯空白归空）；" +
	"有动作非 `input` 节点单段 `label` 超 `250` 码点截断并置 `truncated=true`" +
	"（被动文本/`input.Value` 保持全文，`inspect` 补看全文）。" +
	"无动作 `text` 与匿名容器隐藏几何，详细补看交付矩形。" +
	"新 v5 只生成普通节点定义；读取端继续接受历史 `[n2 n3]` 与 " +
	"`chain [n2 n3] <role> ...`，链下缩进的孩子属于链尾。\n" +
	"补看语法：`inspect v3 scope=<tree|subtree|query> observation=<O> revision=<n> " +
	"captured_at=<RFC3339> tree_scope=<scope> view=detailed [root=<ref>|matches=<n> total=<n> truncated=<bool>] " +
	"nodes=<n> entries=<m>`，以 `end inspect` 结尾。" +
	"`nodes` 计展开后逻辑节点数，`entries` 计正文行数；查询按命中子树累加。" +
	"inspect v3 一律使用两格缩进、`[l,t,r,b]` 矩形和显式动作词，不使用默认动作、`@` 或短 base。\n" +
	"示例 full：\n" +
	"`result v4 ok op=P7391_4\nobs O4821_72\n\ntree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"设置\"\nend tree\nend result`\n" +
	"示例 delta：\n" +
	"`result v4 ok op=P7391_5\nobs O4821_76\n\ntree v5 delta base=_72 changes=3\n\n~ n2 label=[\"a\"]\n" +
	"+ n3 button 700,300 \"返回\"\nchildren n1 = [n2 n3]\nend tree\nend result`\n" +
	"示例 inspect：\n" +
	"`result v4 ok op=P7391_6\n\ninspect v3 scope=subtree observation=O4821_76 revision=4 " +
	"captured_at=2026-11-21T01:00:00Z tree_scope=reported_visible_with_context view=detailed root=n2 nodes=1 entries=1\n\n" +
	"n2 button [0,0,1080,2400] \"a\" tap\nend inspect\nend result`\n" +
	"`button`/`input`/`scroll` 省略动作是既有 `tap`/`text`/`swipe` 能力；" +
	"`!动作` 表示该能力缺失，不能补授权。" +
	"`scroll` 不承诺方向、触摸者或业务安全。"

// callerGuidance is the cross-tool part of the server instructions. It is
// written for the calling agent: session progression, O/P identity, tool
// selection, lease rules, text semantics and recovery. Tool-specific
// constraints stay in the tool descriptions.
const callerGuidance = "首次可直接 start({})；沿用 active 会话，状态不明才 status({})。active/starting 或 can_start=false 时勿 start。" +
	"有 ready O 就 act；无可用 O 或需查看外部变化时 observe。新 O 取 `obs` 行完整编号（structured 为 observation.id）。\n\n" +
	"O 是观察，默认约 60 秒输入期限（以会话租期截断），精确截止时间见 structured JSON；P 是幂等/查询编号，新工作用新 P。请求引用统一用 reference。\n" +
	"当前 ready O 用于 act 和 app 五个变更 kind；历史 O 用于 inspect；活动原会话 O/P 用于 observe、snap、wait_for、keepalive、shell、app.list；end 定位原会话；status/diagnose 可省略引用；app.find/notify 可无会话。\n" +
	"静态拒绝的 ref 若不在 compact，可用 inspect({reference:原 act 的 O,node:诊断 ref}) 补看；O 过期后仍可能可读。\n" +
	"start 租期五分钟；普通活动续期只增不减，至少续至两分钟后。status、inspect、diagnose、snap、app 查询不续租。缺 delta 基线时用 observe(full=true)。\n\n" +
	"恢复按 `recovery` 行给出的精确 `ref=` 编号调用 `entry`；抽象 O/P 按场景换成实际编号。retry/O 表示尚未派发，用新 O 或仍适用的原 O；observe/P 用原活动会话 session.origin 或同会话 P。" +
	"status/P 取本次 operation_id、阻塞 blocking_operation_id、自身恢复 start_operation_id，或收尾 target_reference/刚查的 P。status(O) 撤销时对同一 O 调 end；end/P 用原会话 P；user/update 无引用。\n" +
	"通常 uncertain 先用 status 查原 P，running 继续跟踪，finished 且会话活动时 observe。已认领 text 的 partial/uncertain 按 `recovery entry=observe` 核对框值或页面；有新 O 则沿用。" +
	"text_input.submit=uncertain 先观察；写入确认且未提交时，可在原会话活动、取得新 O 后按需单发 ENTER。避免原样重发或自动 end/restart。" +
	"not_executed 表示未派发；executed 后仅观察、图片或清理失败保留执行事实。device_busy 先处理阻塞 P。任务完成后 end，pending cleanup 按 recovery 查询。"

// serverInstructions is the complete public caller guidance: the fixed tree
// syntax legend exactly once followed by the cross-tool rules. It is the single
// source both the SDK server options and the discovery tests read.
const serverInstructions = treeTextGuide + "\n\n" + callerGuidance

// ServerInstructions returns the deterministic server instructions published by
// server/discover and the compatibility initialize handshake. It is a pure
// source accessor: probes and tests compare live discovery against it instead of
// duplicating the text.
func ServerInstructions() string {
	return serverInstructions
}

// toolCatalog is the complete public tool surface. Order is stable for tests
// and for clients that display tools in registration order.
var toolCatalog = []toolDefinition{
	{
		name:        "start",
		description: "建立会话，参数为空对象，模式和屏幕取自控制端。首采成功返回 `tree v5 full`，从 `obs` 行取完整 O 与 `screen=<W>x<H>@<display_id>`；租期见 `session` 行的 `expires`。重放原 start 不重新获得五分钟。O 是可消费观察，P 是幂等/查询编号；后续 delta 应用到其 base 所指 full。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "required": [],
  "additionalProperties": false
}`),
	},
	{
		name: "status",
		description: "只读查询，不访问设备、不续租、不认领 O。无参查自身准入，传 O 查观察，传 P 查操作或收尾。" +
			"按 `state=`、`can_start=`、`blocking_operation_id=`、`recovery ref=` 推进。status(P) 可重放 open/claim/handoff/close/kill 的终态定格 app 摘要；find/list 或无定格时仅 app.kind，重放不附观察。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    }
  },
  "required": [],
  "additionalProperties": false
}`),
		readOnly: true,
	},
	{
		name: "observe",
		description: "单次采集并发布新观察。reference 必填，指向本 Agent 活动原会话。full=true 强制 full；wait_ms（0–10000 毫秒）仅延迟本次采集，等待公开树条件应选 wait_for。" +
			"从 `obs` 行取完整 O；采集失败按 `recovery entry=observe ref=<P>` 恢复。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    },
    "full": {
      "type": "boolean",
      "default": false
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "随同一次采集附加 ImageContent。"
    },
    "wait_ms": {
      "type": "integer",
      "minimum": 0,
      "maximum": 10000,
      "default": 0
    }
  },
  "required": ["reference"],
  "additionalProperties": false
}`),
	},
	{
		name: "snap",
		description: "立即截图，供 `hint=snap` 只读核对。不读取树、不消费 O、不发布新 O、不续租。" +
			"reference 指向本 Agent 活动原会话；返回 `image <W>x<H> read_only screen=<W>x<H>[@<display_id>] [at=<HH:MM:SS>]` 与 JPEG ImageContent。同一 P 重放或 status(P) 仅无图摘要。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    }
  },
  "required": ["reference"],
  "additionalProperties": false
}`),
		snap: true,
	},
	{
		name: "wait_for",
		description: "轮询当前会话的公开树条件。timeout 为 1..40 秒（stable=true 时至少 2）。" +
			"query/absent_query 按 label/value/hint/resource 作字面折叠子串匹配，任一条件成立即可。" +
			"stable=true 要求内容稳定（忽略 bounds 与无动作叶子文本）连续 3 帧；有查询时须同时满足查询条件和稳定门槛。" +
			"发布新 O 即成功，包括 wait.trigger=timeout；从 `obs` 行取完整 O。wait.matched 含终帧命中项，超时时追加 timeout。未发布按 `recovery entry=… ref=…` 恢复。elapsed_ms 含终帧采集，可超 timeout 至多约 35 秒。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "本 Agent 活动原会话的 O/P。"
    },
    "query": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256,
      "description": "存在条件，须含非空白字符，与 absent_query 折叠后须不同。"
    },
    "absent_query": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256,
      "description": "缺失条件，须含非空白字符，且公开树有可读内容。"
    },
    "timeout": {
      "type": "integer",
      "minimum": 1,
      "maximum": 40
    },
    "stable": {
      "type": "boolean",
      "default": false
    },
    "full": {
      "type": "boolean",
      "default": false,
      "description": "强制发布 full。"
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "在验证采集中附加 ImageContent；可复用帧不重采树。"
    }
  },
  "required": ["reference", "timeout"],
  "additionalProperties": false
}`),
	},
	{
		name: "act",
		description: "用 reference 指定当前 ready O。静态拒绝不认领 O，认领即消费。`change=` 比较前后树（`matched_base` 为有变、零差异命中历史基线），`hint=` 建议截图或等待。" +
			"\n按 operation.type 只传所属分支字段。tap/long_press 用 target；text 用 target 与 text；key 用 key 白名单；scroll 用 container/direction，可选 amount；swipe 用 from/to，可选 duration_ms。" +
			"\n树中 button/input/scroll 省略动作代表 tap/text/swipe；`!tap`/`!text`/`!swipe` 表示缺该能力。`@` 沿最近有已交付几何的祖先读取。树节点的 `swipe` 仅表示源能力；swipe 是原始手势，仍须用当前 O 准入。" +
			"\n点按/长按 ref（`n<ref>`）默认 bubble=true，按 3×3 网格选点；无动作目标按视觉区锚定最近交互祖先，竞争遮挡时停止外扩并吸附唯一局部共位可点击控件。bubble=false 只点原目标；其他动作省略 bubble。" +
			"坐标 tap/long_press/text 用最深命中集合，按动作无法唯一消歧时报 ambiguous_point；raw swipe 不报 ambiguous_point。" +
			"\n静态 422（invalid_target/unsupported_node_action/ambiguous_point/no_safe_point/no_safe_trajectory）均为 not_executed，原 O 可用于重试。五个码依次表示目标结构或几何不可用、缺少可用动作、坐标歧义、无法证明安全点、无法证明安全轨迹。" +
			"error.details 含 reason、scope/scope_ref、candidates 和可选 blocking_refs；候选含 ref、role、actions、bounds、relation、executable。取首个 candidates[].executable=true 的 ref，用同一 O、新 P 重试，无需重观察；诊断 ref 可用原 O 调 inspect(node)。" +
			"unsupported_node_action（reason=no_interactive_ancestor）且目标有屏内裁剪几何时，error.details 可含 target_center: [x, y]。它仅是裁剪中心，无安全或业务响应保证；text 的 submit_ref 静态校验也适用。坐标点按/长按一律省略 bubble，仍须独立准入与重验。" +
			"\n文本：默认 auto_submit=true、submit_ref=\"enter_key\"，聚焦并全文写入一次，精确读回后至多一次 ENTER，无需预先 tap 或另发 ENTER。text(ref) 可用于无 tap 能力的输入，写后 1 秒内至多 3 次只读确认，覆盖短时异步写入与几何变化。" +
			"仅填字、清空、草稿或多行用 auto_submit=false，只写并读回，忽略 submit_ref。submit_ref=\"n<ref>\" 时认领前按 tap 规则静态校验按钮（写前可灰置），写后重验启用和点击安全，再至多点击一次，失败不补 ENTER。" +
			"自动提交分支返回 text_input:{write,submit}。写入确认而提交未派发为 partial（text_submit_not_executed），有新 O 则沿用；派发结果未知为 uncertain，可能仅派发了聚焦。" +
			"已认领 text 的 partial/uncertain 如给出 `recovery entry=observe ref=<P>`，用原活动会话的归属 P（session.origin 或同会话 operation_id）直接 observe 核对当前框值/页面，避免原样重发。文本错误看 details.stage（target/focus/write/verify/submit）、reason 与 dispatch（focus/write/submit：not_attempted/not_dispatched/dispatched/unknown）。" +
			"\n滚动用 operation.type=scroll + container。ref 可指同视口嵌套滚动栈任一层，非滚动节点自动向上找最近滚动祖先；坐标首选该点最内层滚动容器，规划失败回退外层滚动祖先。" +
			"轴向不兼容时回退兼容外层滚动祖先，无则 422 unsupported_node_action（reason=axis_mismatch）。轨迹居中，按 50%/25%/75% 避让。scroll 不接受 duration_ms。执行事实见 outcome。scroll 不承诺真实方向、触摸消费者或业务安全。" +
			"\n恢复：target_changed 若附新观察，从 `obs` 行取完整 O，用新 O 和新 P 重试。通常 uncertain 先查原 P，再按 recovery 处理；executed 后用观察核对页面。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^O",
      "description": "静态拒绝不认领 O；认领即消费。"
    },
    "operation": {
      "type": "object",
      "properties": {
        "type": {
          "type": "string",
          "enum": ["tap", "long_press", "text", "swipe", "scroll", "key"]
        },
        "target": {
          "oneOf": [
            {
              "type": "object",
              "properties": {
                "ref": {"type": "string", "minLength": 1, "maxLength": 64}
              },
              "required": ["ref"],
              "additionalProperties": false
            },
            {
              "type": "object",
              "properties": {
                "x": {"type": "integer", "minimum": 0},
                "y": {"type": "integer", "minimum": 0}
              },
              "required": ["x", "y"],
              "additionalProperties": false
            }
          ]
        },
        "bubble": {
          "type": "boolean",
          "description": "tap/long_press 的节点选点。"
        },
        "auto_submit": {
          "type": "boolean",
          "description": "仅适用 text。"
        },
        "submit_ref": {
          "type": "string",
          "minLength": 1,
          "maxLength": 64,
          "pattern": "^(enter_key|n[1-9][0-9]*)$",
          "description": "text 的提交目标，仅自动提交时使用。"
        },
        "container": {
          "description": "目标容器的角色为 role=scroll。",
          "oneOf": [
            {
              "type": "object",
              "properties": {
                "ref": {"type": "string", "minLength": 1, "maxLength": 64}
              },
              "required": ["ref"],
              "additionalProperties": false
            },
            {
              "type": "object",
              "properties": {
                "x": {"type": "integer", "minimum": 0},
                "y": {"type": "integer", "minimum": 0}
              },
              "required": ["x", "y"],
              "additionalProperties": false
            }
          ]
        },
        "direction": {
          "type": "string",
          "enum": ["up", "down", "left", "right"],
          "description": "视口前进方向。"
        },
        "amount": {
          "type": "string",
          "enum": ["step", "half", "page", "full"],
          "default": "page",
          "description": "step 15%/500ms（最小 32px），half 30%/450ms，page 60%/400ms，full 85%/300ms。"
        },
        "from": {
          "type": "object",
          "properties": {
            "x": {"type": "integer", "minimum": 0},
            "y": {"type": "integer", "minimum": 0}
          },
          "required": ["x", "y"],
          "additionalProperties": false
        },
        "to": {
          "type": "object",
          "properties": {
            "x": {"type": "integer", "minimum": 0},
            "y": {"type": "integer", "minimum": 0}
          },
          "required": ["x", "y"],
          "additionalProperties": false,
          "description": "终点不能与 from 相同。"
        },
        "duration_ms": {
          "type": "integer",
          "minimum": 50,
          "maximum": 2000,
          "description": "手势时长（毫秒），默认 300。"
        },
        "key": {
          "type": "string",
          "enum": ["BACK", "ENTER", "DEL", "TAB", "ESCAPE", "DPAD_UP", "DPAD_DOWN", "HOME", "APP_SWITCH"]
        },
        "text": {
          "type": "string",
          "maxLength": 4096,
          "description": "空串清空输入。"
        }
      },
      "required": ["type"],
      "additionalProperties": false
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "在动作后的同一次采集中附加 ImageContent。"
    }
  },
  "required": ["reference", "operation"],
  "additionalProperties": false
}`),
	},
	{
		name: "inspect",
		description: "只读查看保留中的历史 O，不采集、不续租、不确认基线、不认领 O。node 与 query 互斥；query 按字面 Unicode 折叠子串匹配。" +
			"act 静态拒绝时，用原 act 请求的 O 和诊断 ref 作 node 补看详细子树；O 过期后仍可能可读。" +
			"返回 inspect v3：整树、指定子树或命中路径与子树。content_unavailable 用 observe 取新观察；inspection_too_large 改用更窄的 node 或 query。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^O"
    },
    "node": {
      "type": "string",
      "minLength": 1
    },
    "query": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256
    }
  },
  "required": ["reference"],
  "additionalProperties": false,
  "not": {"required": ["node", "query"]}
}`),
		readOnly: true,
	},
	{
		name: "keepalive",
		description: "重设本 Agent 活动且未到期的原会话租约，自更新起计 duration_minutes（1～120，默认 2）。覆盖截止时间，可缩短租约；不延 O 的输入期限（60 秒）或工作期限。" +
			"session_ended、session_expired 为终态，勿重试；starting 返回 reference_has_no_session，用 status 确认状态。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    },
    "duration_minutes": {
      "type": "integer",
      "minimum": 1,
      "maximum": 120,
      "default": 2
    }
  },
  "required": ["reference"],
  "additionalProperties": false
}`),
	},
	{
		name:        "end",
		description: "结束 reference 所属原会话，先撤权并封闭派发，再独立收尾。返回 `session ended …` 与 `cleanup …`。cleanup 通常先 pending；已派发命令不保证撤回，按 `recovery` 查询。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    }
  },
  "required": ["reference"],
  "additionalProperties": false
}`),
	},
	{
		name: "app",
		description: "发现应用、查询任务或变更生命周期。" +
			"\n分支矩阵（kind｜必填（另均需 kind）｜生效选项｜消费 ready O）：" +
			"\nfind｜无｜query｜否" +
			"\nlist｜reference｜scope、app｜否" +
			"\nopen｜reference、target.app｜无｜是" +
			"\nclaim｜reference、target｜无｜是" +
			"\nhandoff｜reference、target｜restore_foreground｜是" +
			"\nclose｜reference、target｜allow_borrowed｜是" +
			"\nkill｜reference、target.task_id 或 target.app｜allow_stop_playback｜是" +
			"\n七个分支都接受其余已声明顶层成员，但只按上表生效；冗余成员不进入服务端请求。" +
			"\nfind 无需会话。list 必须 reference（活动会话 O 或能定位本 Agent 活动原会话的 P）；缺省或空串报 invalid_request，details.field=reference。find/list 省略 outcome。" +
			"\n变更用当前 ready O；主屏会话仅允 find/list/open，其余在消费 O 前拒绝。open 复用同包窗口或新建；claim 将主屏 live 任务借入虚屏；handoff 将虚屏单任务移回主屏 0；close 关闭虚屏窗口（按包名可匹配多个托管窗口）。" +
			"claim/handoff/close/kill 的 target 恰好选 task_id 或 app；open 只接受 app。close_refused_borrowed 带 error.details.retry_parameter=\"allow_borrowed\"。" +
			"\nkill 的 task 入口须精确匹配 live owned 任务；两入口均要求全部同包 live 任务为本会话自有，无 live 任务仅包名入口可凭 close 确认移除自有任务后保留至多 5 分钟的资格。" +
			"按核实的 Android 用户整包只执行一次 force-stop。任务归属、控制 App、root、安装身份或任务枚举未通过即拒绝。已知播放/前台服务的 kill_refused_playback 带 retry_parameter=\"allow_stop_playback\"，状态未知时拒绝且省略该字段。" +
			"\nkill 摘要按执行、任务/归属、进程、前台观察分层，报告已证实的 pkg、user、src_task、target_tasks、remaining_tasks、target_pids、killed_pids、remaining_pids、restarted、package_state。集合已知为空写 []，未知省略；restarted 不可比较写 null。" +
			"\nfind/list 用 `app_items v1`，close 用 `app_tasks v1`，count=0 表示已知空集合。变更看 outcome 与 recovery；执行后尝试 full 无图观察，从 `obs` 行取新 O。" +
			"status(P)/重复 P 重放 open/claim/handoff/close/kill 的终态定格 app 摘要（仅已保存事实，不附观察）；find/list、运行中或无定格摘要时仅 app.kind。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["find", "list", "open", "claim", "handoff", "close", "kill"]
    },
    "query": {
      "type": "string",
      "maxLength": 256,
      "description": "应用名称或包名的字面折叠子串；省略列全部。"
    },
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    },
    "scope": {
      "type": "string",
      "enum": ["session", "primary"],
      "default": "session",
      "description": "session 列会话任务，primary 列主屏候选。"
    },
    "app": {
      "type": "string",
      "minLength": 1,
      "maxLength": 255,
      "pattern": "^[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)*$",
      "description": "按包名过滤。"
    },
    "target": {
      "type": "object",
      "properties": {
        "task_id": {
          "type": "integer",
          "minimum": 1,
          "description": "精确 leaf task ID。"
        },
        "app": {
          "type": "string",
          "minLength": 1,
          "maxLength": 255,
          "pattern": "^[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)*$",
          "description": "包名精确匹配；claim/handoff 的候选须唯一。"
        }
      },
      "additionalProperties": false
    },
    "restore_foreground": {
      "type": "boolean",
      "default": false,
      "description": "尽力恢复原前台。"
    },
    "allow_borrowed": {
      "type": "boolean",
      "default": false,
      "description": "允许关闭借入窗口。"
    },
    "allow_stop_playback": {
      "type": "boolean",
      "default": false,
      "description": "允许停止已知前台服务或播放。"
    }
  },
  "required": ["kind"],
  "additionalProperties": false
}`),
	},
	{
		name:        "notify",
		description: "保存消息并送控制端队列。无需活动会话，无 reference 参数。executed 只表示消息已保存入队，显示和已读均未确认。`notify [id=…] [saved=…] [state=pending]`，无需重发。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "text": {
      "type": "string",
      "minLength": 1,
      "maxLength": 4096
    }
  },
  "required": ["text"],
  "additionalProperties": false
}`),
	},
	{
		name: "shell",
		description: "在 O/P 所指活动原会话的 Android shell 执行受管命令，可改屏，命令不进入 chroot，不返回新树。root=true 请求 root。" +
			"\n命令不绑定会话屏；input 默认作用于主屏 0，驱动虚屏须用 input -d <display_id>。ID 取自 `start` 首帧 `obs` 行的 `screen=<W>x<H>@<display_id>`、`snap` 的 `image` 或 `app(list)` 的 `display=`。未指定或用错 ID 也可 exit_code=0，改屏须用 observe 核对。" +
			"\nshell.termination/exit_code 报告进程结果，非零退出仍是完成；output_unavailable 仅表示输出不可读。" +
			"uncertain 按 recovery 查原 P；status(P) 即使 finished 仍可能有 device_busy（blocking_operation_id），status({}) 的 blocking_code 可为 shell_pending。短暂退避，原会话活动时再 observe；can_start 用于新会话。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    },
    "command": {
      "type": "string",
      "minLength": 1,
      "maxLength": 4096,
      "description": "命令不能含 NUL。"
    },
    "root": {
      "type": "boolean",
      "default": false
    }
  },
  "required": ["reference", "command"],
  "additionalProperties": false
}`),
	},
	{
		name: "diagnose",
		description: "只读查看有界诊断，不修复状态，不返回树、日志、输入、命令或输出。指定 O/P 查原归属；无参默认聚焦本 Agent 未结束会话（含会话期间独立发起的操作），否则查自身近期记录。" +
			"无参的操作级 issues 按 (phase, code) 去重；可选 error_counts 按受控码汇总窗口内全部失败/不确定次数，独立于 issues 截断。coverage=record_unavailable 时改用 status。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]"
    }
  },
  "required": [],
  "additionalProperties": false
}`),
		readOnly: true,
	},
}
