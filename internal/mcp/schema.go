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
	"`op=` 只是本次调用编号参考、`at=` 只是 UTC 秒级时序提示，均不替代引擎对会话归属、显示代次与期限的权威校验。" +
	"`text`/`both` 是面向调用的紧凑投影；`structured` 保留完整审计字段与全部三态、空集合语义。\n" +
	"观察为单行 `obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [visual=<token>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=…]`：" +
	"`obs ` 后首个不含 `=` 的字段是完整 O；`status=complete` 省略，`partial`/`failed` 显式；" +
	"`fg` 写 `<包名>#<task_id>`、`<包名>`、`#<task_id>`、`present`、`none` 或 `unknown(<reason>)`；" +
	"`visual` 为视觉提示（`sparse_tree`/`image_heavy(<pct>%)`，pct 45–100），`full` 命中才带、同态 `delta` 省略；" +
	"`screen` 仅在带 `session` 或无树帧输出；" +
	"`rot` 在非零旋转时始终输出；所有 `HH:MM:SS` 均为 UTC 秒级时间；" +
	"`origin`/`revision`/`actionable_until` 只在 `structured` JSON 中保留。" +
	"`obs.image` 与顶层 `image` 同字段同顺序（仅前缀不同）；" +
	"`obs.error <code> \"<message>\"` 子行只在对应字段存在时紧跟。\n" +
	"其他块：单行 `image [<W>x<H>] [read_only] [media=<非 jpeg>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [at=HH:MM:SS]`、" +
	"`session <state> mode=… origin=… [started=…] [expires=…] [ended=…] [reason=…]`、" +
	"`wait trigger=… elapsed=<ms>ms`、`cleanup …`、`error <code> \"<message>\"`、" +
	"`recovery entry=<entry> ref=<精确 O/P 编号|O|P|none> [input=…] [cause=…]`（ref 已把可判定的抽象词解析为精确编号）。" +
	"`app.items` 以 `app_items v1 kind=find|list count=<n>` … `end app_items` 交付：" +
	"`kind=find` 行写 `<package> \"<name>\"`，`kind=list` 行写 `#<task_id> <package> <ownership>[ origin_display=<显示号>]`。" +
	"`app.tasks` 以 `app_tasks v1 count=<n>` … `end app_tasks` 交付，行写 " +
	"`#<task_id> <outcome>[ recents_cleaned=…][ error=<code> \"<message>\"[ details=<JSON>]]`。" +
	"无法无损编码的字段保留 `json v1 path=\"<绝对 JSON Pointer>\" value=<原值>`。\n" +
	"普通树：当前树由 `base` 指向的 full 加本次 delta 重建。" +
	"full 头为 `tree v5 full nodes=<n>`；delta 头为 " +
	"`tree v5 delta base=<O|_<序号>> changes=<n>`；头后空一行进入正文，均以 `end tree` 结尾。" +
	"同一 base 的每条 delta 独立应用到该 full。" +
	"当前完整 O 与真实 base 在最后一个 `_` 前同前缀且尾号为正十进制时，base 可写 `_<尾号>`；" +
	"否则写完整 O。`base=_72` 只供读树，调用仍用完整 O/P。\n" +
	"普通节点每层恰写一个 ASCII 空格，根深度为零；差分 `+ `、`= ` 只在所属定义根行出现，标记不计层级。" +
	"节点行为 `n<ref> [<role>] [l,t,r,b|x,y|@] \"label\"… value=… hint=… " +
	"tap|long_press|text|swipe enabled=… focused=… selected=… protected=… checked=… axis=… resource=… context`。" +
	"`n<ref>` 可直接用作 target.ref；标签为 JSON 字符串。" +
	"`root`/`scroll` 交付矩形 `l,t,r,b`（`right>=left`、`bottom>=top`），其余交付中心点 `x,y`；" +
	"未知写 `-`，缺失列表示隐藏。" +
	"`@` 表示与同一定义中最近已交付几何的严格祖先同形同值；无此祖先无效。" +
	"每个 `+`、`=` 子树独立判断，不继承其他定义或 base。\n" +
	"角色：`button` 默认 `tap`，`input` 默认 `text`，`scroll` 默认 `swipe`。" +
	"能按源动作顺序完整恢复时省略默认词；默认动作缺失时保留其他动作并在动作列末写 `!tap`、`!text` 或 `!swipe`。" +
	"无显式默认词或对应 `!动作` 时，其余动作须符合源顺序，再按 `tap,long_press,text,swipe` 插回默认动作；" +
	"无法恢复时写完整显式列表。出现默认词时保留显式顺序。" +
	"同一动作不可既显式又否定，其他角色的否定词和无可解析祖先的 `@` 均无效。" +
	"省略 `role` 时按 ref 后首列恢复；未隐藏几何是四元矩形、二元点或 `@`，" +
	"首项为引号标签时默认 `text`，其他列或行尾默认 `view`。" +
	"显式角色覆盖默认；首项为标签或角色词的 `view`、无标签或带几何列的 `text` 必须显式写角色。\n" +
	"complete 定义的状态位省略已知默认：`enabled=true` 省略，`enabled=false`/`enabled=null` 显式；" +
	"`focused`/`selected`/`protected`/`checked` 的已知 `false` 省略，`true`/`null` 显式。" +
	"`set` 行不同：出现的状态字段一律保留显式 `false`/`true`/`null`，未出现才表示不修改。" +
	"`~` 的 set 值保持原数组、字符串、`null` 与三态；`actions=[]`、`bounds=[x,y]`、" +
	"`bounds=[l,t,r,b]`、`bounds=null`、`bounds=hidden` 均原样表达，`@`、无括号几何和默认动作不用于 set。" +
	"`nodes` 是展开历史链后的逻辑节点数，`changes` 是五类顶层声明的总量；零变化 delta 正文为空。\n" +
	"compact 净体：全裁无标注非交互 `image` 叶；裁剪被祖先复合标签覆盖的纯被动文本叶；" +
	"纯被动文本子叶的无字可点击 `view` 升为 `button`（保留多段 `label`，清 `resource`）；" +
	"`input` 的 `label==value` 时省略 `label`；裁掉宽或高 `<=4px` 的无字露边 `view`；" +
	"`resource` 取按 `:`、`/` 切分后最后的非纯数字短段。" +
	"清理变空/单子隐藏匿名容器，`len>=2` 保持层级；" +
	"无动作 `text` 与匿名容器隐藏几何，详细补看交付矩形。" +
	"新 v5 只生成普通节点定义；读取端继续接受历史 `[n2 n3]` 与 " +
	"`chain [n2 n3] <role> ...`，链下缩进的孩子属于链尾。\n" +
	"补看使用独立语法：`inspect v3 scope=<tree|subtree|query> observation=<O> revision=<n> " +
	"captured_at=<RFC3339> tree_scope=<scope> view=detailed [root=<ref>|matches=<n> total=<n> truncated=<bool>] " +
	"nodes=<n> entries=<m>`，以 `end inspect` 结尾。" +
	"`nodes` 计展开后的逻辑节点数，`entries` 计正文定义行数。查询按每个命中子树分别累加。" +
	"inspect v3 一律使用两格缩进、`[l,t,r,b]` 矩形和显式动作词，不使用默认动作、`@` 或短 base。\n" +
	"示例 full：\n" +
	"`result v4 ok op=P7391_4\nobs O4821_72 fg=present at=22:05:11 visual=sparse_tree\n\n" +
	"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"设置\"\nend tree\nend result`\n" +
	"示例 delta：\n" +
	"`result v4 ok op=P7391_5\nobs O4821_76 fg=com.android.settings#177 at=22:05:12\n\n" +
	"tree v5 delta base=_72 changes=3\n\n~ n2 label=[\"无线设置\"]\n" +
	"+ n3 button 700,300 \"返回\"\nchildren n1 = [n2 n3]\nend tree\nend result`\n" +
	"示例 inspect：\n" +
	"`result v4 ok op=P7391_6\n\ninspect v3 scope=subtree observation=O4821_76 revision=4 " +
	"captured_at=2026-11-21T01:00:00Z tree_scope=reported_visible_with_context view=detailed root=n2 nodes=1 entries=1\n\n" +
	"n2 button [0,0,1080,2400] \"无线设置\" tap\nend inspect\nend result`\n" +
	"`button`/`input`/`scroll` 的省略动作分别是已存在的 `tap`/`text`/`swipe` 源能力；" +
	"`!动作` 表示该默认能力缺失，不能由调用方补授权。" +
	"滚动用 `operation.type=scroll` + `operation.container` 表达；raw `swipe` 是原始手势。" +
	"`scroll` 不承诺真实方向、触摸消费者或业务安全。"

// callerGuidance is the cross-tool part of the server instructions. It is
// written for the calling agent: session progression, O/P identity, tool
// selection, lease rules, text semantics and recovery. Tool-specific
// constraints stay in the tool descriptions.
const callerGuidance = "会话与推进：首次可直接 start({})。已有 active 会话时沿用，只有会话状态不明才 status({})。" +
	"active/starting 或 can_start=false 时不要 start；会话失效先按 recovery 处理。\n" +
	"有可用 O 时直接 act；act 或 wait_for 返回新 O 时从 `obs` 行取完整 `O`（structured 中为 `observation.id`）。" +
	"无可用 O、要看外部变化或 `recovery entry=observe` 时才 observe。\n\n" +
	"O 与 P：O 是观察，默认约 60 秒输入期限（以会话租期截断），精确截止时间见 structured JSON。" +
	"P 是调用的幂等/查询编号，新工作另有新 P。" +
	"app.find 与 notify 可脱离会话，notify 无 reference。请求引用统一用 reference。\n" +
	"- 当前 ready O：act、app 的五个变更 kind。保留中的历史 O：inspect。\n" +
	"- 活动原会话的 O/P：observe、snap、wait_for、keepalive、shell。app.list 接受 ready O 或可定位活动原会话的 P。\n" +
	"- end 的 O/P 定位原会话。status、diagnose 可用 O/P 查原事实，省略时查自身。\n" +
	"- 静态拒绝的 ref 若不在 compact，可用 " +
	"inspect({reference:原 act 的 O,node:诊断 ref}) 补看；O 过期后仍可能可读。\n" +
	"start 租期五分钟；普通活动在续期点只增不减，至少续至两分钟后。" +
	"status、inspect、diagnose、snap、app 查询不续租。keepalive 可重设并缩短租期，不延长 O。\n\n" +
	"选择工具：observe 单次采集、snap 只看画面、wait_for 等公开树条件、inspect 补看历史树、diagnose 查健康。" +
	"wait_for 以 wait.trigger/wait.matched 说明退出分支与终帧命中项；有新 O 就用它继续。\n\n" +
	"输入：act 认领 ready O 才消费。点按/长按默认 bubble=true，自动按 3×3 网格选点；" +
	"无动作目标按视觉区锚定最近交互祖先，竞争遮挡时停止外扩并自动吸附唯一局部共位可点击控件。" +
	"operation.bubble=false 只点原目标，坐标省略 bubble。坐标目标用最深命中集合，" +
	"仅在无法按请求动作唯一消歧时报 ambiguous_point；raw swipe 不报 ambiguous_point。" +
	"scroll 可直接传同视口嵌套滚动栈任一层，非滚动节点自动找最近滚动祖先，坐标取最内层滚动容器；" +
	"轨迹居中并尝试 50%/25%/75% 三条轴线避让；amount 四档 step 15%/500ms、half 30%/450ms、page 60%/400ms（默认）、full 85%/300ms，scroll 不接受时长参数；" +
	"轴向不兼容时自动回退兼容外层滚动祖先，无解报 422 unsupported_node_action（reason=axis_mismatch）。" +
	"静态 422（invalid_target、unsupported_node_action、ambiguous_point、no_safe_point、no_safe_trajectory）不消费 O；" +
	"取 error.details.candidates 中第一个 executable=true 的 ref，用同一 O 和新 P 重试，无需重观察或手算坐标。" +
	"text 默认 auto_submit=true、submit_ref=\"enter_key\"：写入全文并读回后至多一次 ENTER；" +
	"仅填字时 operation.auto_submit=false，不发送 ENTER。" +
	"已知提交按钮用 operation.submit_ref=\"n<ref>\"，一次调用完成填写与提交。" +
	"text_input 报告 write 与 submit；write=executed 且 submit 未派发时 outcome=partial（text_submit_not_executed）。" +
	"业务结果用页面事实核对。\n\n" +
	"恢复：优先用 `recovery` 行给出的精确 `ref=` 编号调用对应 `entry`；" +
	"`ref=` 是抽象词 `O`/`P` 时按场景取 `op`、`blocking_operation_id`、`start_operation_id` 或 `session.origin`。" +
	"retry/O 表示尚未派发，用新观察的完整 O 或仍适用的原 O 重发。" +
	"observe/P 用原活动会话的具体归属 P（session.origin 或同会话 operation_id）；请求中的 reference 用实际编号。" +
	"status/P 的编号：当前动作用 operation_id，忙用 blocking_operation_id，自身恢复用 start_operation_id，收尾用 target_reference 或刚查询的 P。" +
	"status(O) 撤销时对同一 O 调 end；end/reference=P 用原会话的归属 P。user 处理阻塞，update 配套升级，两者均不传引用。\n" +
	"已认领 text 的整体 outcome=uncertain 或 partial，且给出 `recovery entry=observe ref=<P>` 时直接 observe 核对当前框值/页面。" +
	"partial 表示写入已确认、提交未派发：结果附带新 O 时直接用它继续；否则按 `recovery` 的 P 观察。" +
	"text_input.submit=uncertain 时先观察；write=executed、submit=not_executed 且原会话活动时取新 O 后可按需单发 ENTER。" +
	"不要原样重发，也不要自动 end/restart。\n" +
	"通常 uncertain 先用 status 跟踪原 P，running 继续跟踪，finished 且会话活动时再 observe。" +
	"not_executed 表示未派发；executed 后仅观察、图片或清理失败保留执行事实。device_busy 时跟随阻塞 P 串行。" +
	"任务完成后 end，pending cleanup 按 recovery 查询。"

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
		name: "start",
		description: "建立新会话并返回第一个 full 观察。请求体必须是空对象；" +
			"目标屏幕与模式来自控制端当前选择，不接受尺寸、模式或凭据参数。" +
			"成功时返回 session；首采成功时从 `obs` 行取得首个完整 `O` 与会话屏 " +
			"`screen=<W>x<H>@<display_id>`，租期以 `session` 行的 `expires` 为准；重放原 start 不重新获得五分钟。" +
			"O 是可消费观察，P 是该次操作的幂等/查询编号。" +
			"首次返回 full；普通树头为 `tree v5 full`，delta 头为 `tree v5 delta base=… changes=…`，" +
			"用 base 指向的 full 加本次 delta 重建当前树。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "required": [],
  "additionalProperties": false,
  "description": "start 不接受任何成员。"
}`),
	},
	{
		name: "status",
		description: "只读查询自身准入状态、给定 O 的状态或给定 P 的操作状态，不访问设备、不续租、不认领观察。" +
			"reference 省略时查询自身；带 O 可确认该观察是否仍可用，带 P 可查该操作的事实或清理进度。" +
			"按返回头行的 `state=`、`can_start=`、`blocking_operation_id=` 与 `recovery ref=` 决定下一步" +
			"（structured 中为 operation_state、can_start、blocking_operation_id 与 recovery.reference）。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "可选的 O 或 P 编号；省略表示查询自身状态。"
    }
  },
  "required": [],
  "additionalProperties": false,
  "description": "status 的可选 reference 为 O… 或 P…，长度不超过 64。"
}`),
		readOnly: true,
	},
	{
		name: "observe",
		description: "对给定 O/P 所指原会话执行一次新采集并发布一个新观察。" +
			"reference 必填，指向本 Agent 仍活动的原会话；full=true 强制 full；" +
			"image=true 在同一采集中附加 ImageContent；wait_ms 是单次采集前的延迟毫秒数（0–10000，默认 0）；等待公开树条件应选 wait_for。" +
			"成功返回新观察：从 `obs` 行取得完整 O；采集失败按 `recovery entry=observe ref=<P>` 恢复" +
			"（structured 中为 recovery.entry 与 recovery.reference）。" +
			"看到 `base=_72` 时，用 `72` 替换当前 O 的末尾序号得到指定 full 的完整编号；" +
			"同一 full 上的多个差分各自应用。缺少该 full 时，以适用的活动会话 O/P 再调用 observe 并设置 full=true。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "必填的 O 或 P 编号，必须指向本 Agent 仍活动的原会话。"
    },
    "full": {
      "type": "boolean",
      "default": false,
      "description": "true 强制本次返回 full，忽略既有基线。"
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "true 时附加一个同一采集的 ImageContent；不改变树内容或观察事实。"
    },
    "wait_ms": {
      "type": "integer",
      "minimum": 0,
      "maximum": 10000,
      "default": 0,
      "description": "采集前等待毫秒数，0–10000。"
    }
  },
  "required": ["reference"],
  "additionalProperties": false,
  "description": "observe 需要 reference；full、image、wait_ms 均可选。"
}`),
	},
	{
		name: "snap",
		description: "对给定 O/P 所指原会话绑定的屏幕立即读取一张图，不读取节点树，也不发布观察。" +
			"reference 必填，指向本 Agent 仍活动的原会话；图片始终取当前屏。" +
			"成功只返回 operation_id 与单行 `image <W>x<H> read_only screen=<W>x<H>[@<display_id>] [at=<HH:MM:SS>]` 元数据，" +
			"并以一个 ImageContent 交付 JPEG。" +
			"snap 不分配 O、不续租、不认领观察；同一 P 重放或 status(P) 只返回无图摘要。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "必填的 O 或 P 编号，必须指向本 Agent 仍活动的原会话。"
    }
  },
  "required": ["reference"],
  "additionalProperties": false,
  "description": "snap 需要 reference，不接受其他成员。"
}`),
		snap: true,
	},
	{
		name: "wait_for",
		description: "轮询当前会话的公开树条件。reference 与 timeout 必填，timeout 为 1..40 秒（stable=true 时至少 2）。" +
			"\nquery/absent_query 按 label/value/hint/resource 做字面折叠子串匹配；同时提供时任一条件成立即可满足查询条件。" +
			"stable=true 要求内容稳定（忽略 bounds 与无动作叶子文本）连续 3 帧一致。倒计时或实时进度页面也可使用该模式。" +
			"\n发布新观察时调用成功，包括 wait.trigger=timeout；从 `obs` 行取得完整 O。" +
			"看到 `base=_72` 时，用 `72` 替换当前 O 的末尾序号得到指定 full 的完整编号；缺少该 full 时设置 full=true 再 observe。" +
			"同一 full 上的多个差分各自应用。" +
			"wait.matched 包含终帧命中的条件，超时时还包含 timeout。" +
			"最终未发布新 O 时按观察失败处理，并按 `recovery entry=… ref=…` 恢复（structured 中为 recovery.entry 与 recovery.reference）。" +
			"elapsed_ms 含终帧采集，实际耗时可超过 timeout 至多约 35 秒。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "必填的 O 或 P 编号，必须指向本 Agent 仍活动的原会话。"
    },
    "query": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256,
      "description": "可选的字面存在条件，须非空、不能全为空白。与 absent_query 折叠后须不同。"
    },
    "absent_query": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256,
      "description": "可选的字面缺失条件，须非空、不能全为空白。当前公开树须有可读内容。"
    },
    "timeout": {
      "type": "integer",
      "minimum": 1,
      "maximum": 40,
      "description": "必填的等待上限秒数，1..40；stable=true 时至少 2。"
    },
    "stable": {
      "type": "boolean",
      "default": false,
      "description": "true 时要求内容稳定（忽略 bounds 与无动作叶子文本）连续 3 帧一致；有查询时它是条件满足后的稳定门禁，无查询时它是独立触发分支。"
    },
    "full": {
      "type": "boolean",
      "default": false,
      "description": "true 强制本次发布 full，忽略既有基线。"
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "true 时在同一验证采集中附加一个 ImageContent；可复用帧不重采树。"
    }
  },
  "required": ["reference", "timeout"],
  "additionalProperties": false,
  "description": "wait_for 需要 reference 与 timeout；query、absent_query、stable、full、image 均可选。"
}`),
	},
	{
		name: "act",
		description: "对当前 ready O 执行交互。reference 必填且用当前 ready O 的完整 `O`；节点目标用 `n<ref>`。" +
			"静态拒绝不认领 O，认领即消费原 O。image=true 随观察附加 ImageContent。" +
			"\n分支：tap/long_press 用 target；text 用 target 与 text；key 用 key 白名单。scroll 用 container/direction，可选 amount；swipe 用 from/to，可选 duration_ms。" +
			"\n树中 `button`/`input`/`scroll` 省略的默认动作分别是既有 `tap`/`text`/`swipe`；" +
			"`!tap`/`!text`/`!swipe` 表示该默认能力缺失。`@` 表示与最近一个有已交付几何的祖先同形同值；需要坐标时沿该祖先读取。" +
			"\n点按与长按：ref 默认 bubble=true，自动按 3×3 网格选点。无动作目标先按视觉区锚定最近交互祖先；" +
			"存在竞争控件遮挡时停止向祖先其他区域漂移，并自动吸附唯一的局部共位可点击控件。" +
			"显式 bubble=false 只点原目标，不做提升或吸附；坐标目标省略 bubble。" +
			"坐标 tap/long_press/text 使用最深命中集合，并仅在多个交互目标无法按请求动作唯一消歧时报 ambiguous_point；raw swipe 不返回 ambiguous_point。" +
			"静态拒绝均为 not_executed：invalid_target 表示目标结构或几何不可用，unsupported_node_action 表示目标及合法关联对象缺少动作能力，" +
			"ambiguous_point 表示坐标存在不可消歧的竞争目标，no_safe_point 表示点击安全点无法证明，no_safe_trajectory 表示滚动安全轨迹无法证明。" +
			"error.details 提供 reason、scope/scope_ref、candidates 与可选 blocking_refs；" +
			"candidates 项含 ref、role、actions、bounds、relation、executable。" +
			"candidates 非空时，直接取第一个 executable=true 的 ref，用同一 O、新 P 重试，无需重观察或手算坐标。" +
			"诊断 ref 若因 compact 不在普通树中，可用 inspect({reference:原 O,node:诊断 ref}) 补看详细子树。" +
			"\n文本：默认 auto_submit=true、submit_ref=\"enter_key\"。聚焦、全文写入、同节点精确读回，随后至多一次 ENTER；写后提交失败不补发 ENTER。" +
			"普通输入无需先 tap 或另发 ENTER。仅填字、草稿、多行、清空或逐字段编辑时显式 operation.auto_submit=false，只写入并读回，submit_ref 忽略。" +
			"已知提交按钮用 operation.submit_ref=\"n<ref>\"：认领前按 tap 同一静态规划校验（写前灰置也接受），写后重验启用与点击安全点，再至多点击一次。" +
			"默认分支返回 text_input:{write,submit}，auto_submit=false 时省略。" +
			"write=executed 且提交确定未派发时整体 outcome=partial，错误码 text_submit_not_executed，当场交付新 ready O；" +
			"发布失败时不附带 O，按 recovery 的 P 观察；提交已派发但回执未知为 outcome=uncertain。" +
			"文本错误看 details.stage（target/focus/write/verify/submit）、reason 与 dispatch（focus/write/submit），区分 not_attempted/not_dispatched/dispatched/unknown。" +
			"已认领 text 的整体 outcome=uncertain（也可能仅已派发聚焦）或 partial（写入确认、提交未派发），且给出 `recovery entry=observe ref=<P>` 时，" +
			"直接 observe 核对当前框值/页面。reference 使用原活动会话的具体归属 P（session.origin 或属于该会话的 operation_id）。避免原样重发。" +
			"\n滚动：用 operation.type=scroll + container。amount 四档 step 15%/500ms（最小 32px）、half 30%/450ms、page 60%/400ms（默认）、full 85%/300ms，scroll 不接受 duration_ms。" +
			"ref 可直接指向同视口嵌套滚动栈任一层；非滚动节点自动向上找最近滚动祖先。" +
			"坐标容器自动选择该点所在最内层滚动容器，规划失败时回退外层滚动祖先；" +
			"轴向不兼容时自动回退兼容外层滚动祖先，没有则返回 422 unsupported_node_action（reason=axis_mismatch）。" +
			"轨迹在安全区间居中起止，并依次尝试 50%/25%/75% 三条轴线避让。" +
			"swipe 是原始手势；树节点的 `swipe` 仅表示源能力，手势仍按当前 O 准入。" +
			"执行事实见 outcome。scroll 不承诺真实方向、触摸消费者或业务安全。" +
			"\n恢复：重验目标变化可返回 target_changed；若附新观察，从 `obs` 行取得完整 O，直接用新 O 和新 P 重试。" +
			"通常 uncertain 先查询原 P，再按 recovery 处理；executed 后用观察判断业务结果。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^O",
      "description": "必填的当前 ready O 编号。静态拒绝不认领 O；一旦认领，原 O 即被消费。"
    },
    "operation": {
      "type": "object",
      "properties": {
        "type": {
          "type": "string",
          "enum": ["tap", "long_press", "text", "swipe", "scroll", "key"],
          "description": "动作类型；scroll 用 container/direction/amount，swipe 用 from/to/duration_ms。"
        },
        "target": {
          "description": "tap/long_press/text 的目标：ref 或 x/y 坐标，二选一。",
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
          "description": "仅 tap/long_press 且 target 为 ref 时适用；省略按 true 处理。true 自动 3×3 选点，按目标视觉区锚定最近交互祖先；竞争遮挡时停止外扩并自动吸附唯一的局部共位可点击控件。false 只点原目标，不做提升或吸附。坐标目标和其他动作须省略。"
        },
        "auto_submit": {
          "type": "boolean",
          "description": "仅 text 适用；省略按 true 处理。true 表示写入并精确读回后按 submit_ref 至多提交一次；false 只写入并读回，提交语义与 submit_ref 一并忽略。其他动作须省略。"
        },
        "submit_ref": {
          "type": "string",
          "minLength": 1,
          "maxLength": 64,
          "pattern": "^(enter_key|n[1-9][0-9]*)$",
          "description": "仅 text 且 auto_submit=true 时适用；省略按 \"enter_key\" 处理。\"enter_key\" 表示至多按一次 ENTER；n<ref> 表示写后点击该提交按钮，至多一次且失败不补发 ENTER。其他动作须省略。"
        },
        "container": {
          "description": "scroll 的容器选择器：ref 或 x/y 坐标，二选一。ref 可为任意公开节点：自身为滚动容器时直接使用，否则自动向上找最近 role=scroll 祖先；同视口嵌套滚动栈可直接传任一层。x/y 取该点最内层滚动容器，规划失败时回退外层滚动祖先；容器轴向与请求方向不兼容时自动回退兼容外层滚动祖先，无则返回 422 unsupported_node_action（reason=axis_mismatch）。",
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
          "description": "scroll 必填的阅读/视口前进方向，大小写敏感；不证明设备滚动轴。"
        },
        "amount": {
          "type": "string",
          "enum": ["step", "half", "page", "full"],
          "default": "page",
          "description": "scroll 可选幅度：step（15%，500ms，最小行程 32px）、half（30%，450ms）、page（默认，60%，400ms）、full（85%，300ms）；不接受其他值，scroll 不接受 duration_ms。"
        },
        "from": {
          "type": "object",
          "properties": {
            "x": {"type": "integer", "minimum": 0},
            "y": {"type": "integer", "minimum": 0}
          },
          "required": ["x", "y"],
          "additionalProperties": false,
          "description": "swipe 起点坐标。"
        },
        "to": {
          "type": "object",
          "properties": {
            "x": {"type": "integer", "minimum": 0},
            "y": {"type": "integer", "minimum": 0}
          },
          "required": ["x", "y"],
          "additionalProperties": false,
          "description": "swipe 终点坐标，不能与 from 相同。"
        },
        "duration_ms": {
          "type": "integer",
          "minimum": 50,
          "maximum": 2000,
          "description": "swipe 可选手势时长毫秒，50–2000，默认 300；其他分支出现即拒绝。"
        },
        "key": {
          "type": "string",
          "enum": ["BACK", "ENTER", "DEL", "TAB", "ESCAPE", "DPAD_UP", "DPAD_DOWN", "HOME", "APP_SWITCH"],
          "description": "key 分支的固定白名单。"
        },
        "text": {
          "type": "string",
          "maxLength": 4096,
          "description": "text 分支要写入的字符串，可为空，最多 4096 个 Unicode 码点；目标框接收全文而不是追加。"
        }
      },
      "required": ["type"],
      "additionalProperties": false,
      "description": "按 type 提供该分支的成员。"
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "true 时在动作后的同一次采集中附加一个 ImageContent。"
    }
  },
  "required": ["reference", "operation"],
  "additionalProperties": false,
  "description": "act 需要 reference 与 operation；image 可选。"
}`),
	},
	{
		name: "inspect",
		description: "只读查看保留中的历史观察树，不采集、不续租、不确认基线、不认领 O。" +
			"reference 必填且必须指向保留中的历史 O；node 与 query 互斥，query 按字面 Unicode 折叠子串匹配。" +
			"act 静态拒绝中的诊断 ref 若不在 compact 普通树，用原 act 请求的 O 和该 ref 作 node 补看详细子树。" +
			"补看要求同一调用方的原 O 内容仍保留，O 过期后仍可能可读。" +
			"返回 inspect v3 块：tree 为完整详细树，subtree 为指定 root 的子树，query 为命中节点的路径与子树。" +
			"inspect v3 始终使用两格缩进、方括号矩形和显式动作词，不使用普通树 v5 的默认动作、`@` 或短 base。" +
			"历史树被淘汰（content_unavailable）时用 observe 取新观察；结果过大（inspection_too_large）时改用更窄的 node 或 query。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^O",
      "description": "必填的历史 O 编号。"
    },
    "node": {
      "type": "string",
      "minLength": 1,
      "description": "可选节点 ref；与 query 互斥。"
    },
    "query": {
      "type": "string",
      "minLength": 1,
      "maxLength": 256,
      "description": "可选非空字面查询，最多 256 个 Unicode 码点；与 node 互斥。"
    }
  },
  "required": ["reference"],
  "additionalProperties": false,
  "not": {"required": ["node", "query"]},
  "description": "inspect 需要 reference；node 与 query 至多出现一个。"
}`),
		readOnly: true,
	},
	{
		name: "keepalive",
		description: "重设给定 O/P 所指当前活动会话的租约截止时间，不延长 O 的输入期限（60 秒）或工作期限。" +
			"reference 必填，须仍能定位当前活动且未到期的原会话；duration_minutes 为 1～120 的整数，默认 2。" +
			"显式续期会覆盖原截止时间，较小的值或省略参数可缩短已有长租约。" +
			"已结束或已到期不可恢复：ended 返回 session_ended，starting 返回 reference_has_no_session，" +
			"已过期限返回 session_expired；确认会话状态用 status，不要对终态会话重试。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "本 Agent 的 O 或 P，必须仍能定位当前活动且未到期的原会话；不要求 O 仍可用于输入，不复活已到期或结束会话。"
    },
    "duration_minutes": {
      "type": "integer",
      "minimum": 1,
      "maximum": 120,
      "default": 2,
      "description": "从本次更新应用时起重新计时的分钟数，整数 1～120，省略为 2；覆盖原截止时间，所以较小值或省略参数可缩短已有长租约。后续普通活动仍可延长。"
    }
  },
  "required": ["reference"],
  "additionalProperties": false,
  "description": "keepalive 需要 reference；duration_minutes 可选，省略也重设为两分钟。"
}`),
	},
	{
		name: "end",
		description: "结束给定 O/P 的原会话：先撤权并封闭派发，再启动独立收尾。" +
			"reference 必填，语义是该引用所属的原会话；返回 `session ended …` 与 `cleanup …` 行。" +
			"cleanup 通常先 pending，已派发的 Android 命令不保证撤回；未完成时按 `recovery` 行查询。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "必填的 O 或 P 编号，语义是该引用所属的原会话。"
    }
  },
  "required": ["reference"],
  "additionalProperties": false,
  "description": "end 需要 reference。"
}`),
	},
	{
		name: "app",
		description: "按 kind 发现应用、查询任务或变更应用生命周期。" +
			"\nfind 无需会话，query 可按应用名称或包名做字面折叠子串过滤。" +
			"\nlist 查询原会话的 live 任务，reference 用 ready O 或定位活动原会话的 P。scope=session 列会话任务，scope=primary 列主屏候选，可按 app 包名过滤。find/list 省略 outcome。" +
			"\n变更分支使用当前 ready O。主屏会话只允许 find/list/open，其余分支在消费 O 前拒绝。" +
			"\n- open 用 target={app:包名} 复用同包窗口或新建。" +
			"\n- claim 将主屏的精确 live 任务移入本会话虚屏并登记 borrowed。" +
			"\n- handoff 将本会话虚屏的单个任务移回主屏 0，可选恢复前台。" +
			"\n- close 关闭本会话虚屏目标窗口，可显式允许关闭 borrowed。" +
			"\n- kill 用 task_id 终止本会话同屏唯一的 live owned 任务，并连同其任务窗口容器一并移除；虚拟屏上处于当前前台也可终止，但该应用在主屏或其他显示屏仍有任务时拒绝，可显式允许停止播放。" +
			"\nclaim/handoff/close 的 target 用 task_id 或包名二选一，仍须通过服务准入。" +
			"find/list 的 items 以 `app_items v1` 块、close 的 tasks 以 `app_tasks v1` 块交付，空集合输出 count=0，与未获得集合区分。" +
			"变更结果按 outcome 与 recovery 处理。已执行后尝试发布原会话屏 full 无图观察；" +
			"有返回的新观察时从 `obs` 行取得完整 O 继续操作。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["find", "list", "open", "claim", "handoff", "close", "kill"],
      "description": "必填且大小写敏感的互斥分支。"
    },
    "query": {
      "type": "string",
      "maxLength": 256,
      "description": "find 可选：本地化名称或包名子串；省略表示全部，最多 256 个 Unicode 码点。"
    },
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "list 可为 ready O 或能解析到本 Agent 活动原会话的 P；open/claim/handoff/close/kill 必须是当前 ready O。"
    },
    "scope": {
      "type": "string",
      "enum": ["session", "primary"],
      "default": "session",
      "description": "list 可选：session 列原会话虚屏任务（默认）；primary 列主屏候选元数据。"
    },
    "app": {
      "type": "string",
      "minLength": 1,
      "maxLength": 255,
      "pattern": "^[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)*$",
      "description": "仅 list 接受：明确的包名过滤。open 的包名放在 target.app，其他 kind 不接受顶层 app。"
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
          "description": "明确的应用包名；按包名精确匹配，零个或多个候选时拒绝。"
        }
      },
      "additionalProperties": false,
      "description": "open 只接受 {app}；claim/handoff/close 接受 {task_id} 或 {app} 恰好一种；kill 只接受 {task_id}。"
    },
    "restore_foreground": {
      "type": "boolean",
      "default": false,
      "description": "handoff 可选：true 时尽力恢复原前台；默认 false。"
    },
    "allow_borrowed": {
      "type": "boolean",
      "default": false,
      "description": "close 可选：true 才允许关闭借入窗口；默认 false 整体拒绝。"
    },
    "allow_stop_playback": {
      "type": "boolean",
      "default": false,
      "description": "kill 可选：true 只越过已知前台服务/播放拒绝；默认 false。"
    }
  },
  "required": ["kind"],
  "additionalProperties": false,
  "description": "app 的扁平根只声明成员集合；每个 kind 的必填与禁止成员由服务端严格解码矩阵执行，跨 kind 成员返回 invalid_request。"
}`),
	},
	{
		name: "notify",
		description: "保存一条消息并交给控制端显示。无需活动会话，无 reference 参数。text 必填、非空、最多 4096 码点。" +
			"结果合并为单行 `notify [id=…] [saved=…] [state=pending]`。" +
			"executed 只表示消息已保存并进入控制同步队列，不表示已显示或已读；" +
			"notification.state 固定为 pending（文本写作 state=pending），不需要重发。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "text": {
      "type": "string",
      "minLength": 1,
      "maxLength": 4096,
      "description": "必填的非空消息正文，最多 4096 个 Unicode 码点。"
    }
  },
  "required": ["text"],
  "additionalProperties": false,
  "description": "notify 需要非空 text。"
}`),
	},
	{
		name: "shell",
		description: "在给定 O/P 原会话的 Android shell 作用域内执行一条受管命令。reference 与 command 必填。" +
			"root=true 明确请求 root。命令不进入 chroot，可改变界面且不返回新树。" +
			"\n受管命令不绑定会话显示屏：input 一类命令默认作用于 display 0（主屏），在虚拟屏会话里回执仍是 executed/exited、exit_code=0，但会话屏不会移动。" +
			"要驱动会话屏须自行带 input -d <display_id>，该 `display_id` 见 `start` 首帧 `obs` 行的 " +
			"`screen=<W>x<H>@<display_id>`（也可从 `snap` 的 `image` 或 `app(list)` 的 `display=` 取得）。" +
			"带错 id 同样 exit 0 且无任何提示，唯一验证办法是 observe 对比。" +
			"\n成功返回 shell.termination 和 exit_code，非零退出仍是完成事实；output_unavailable 只表示输出不可读。" +
			"预期改屏时用 observe 核对。" +
			"\n结果 uncertain 时按 recovery 查原 P。即使 status(P) 已显示 finished，命令终止未确认仍可能使普通工作返回 device_busy，同 Agent 可见 blocking_operation_id。" +
			"status({}) 的 blocking_code 可显示 shell_pending。遇到该忙阻塞，短暂退避后在原会话仍活动时再 observe。can_start 用于新会话判断。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "必填的 O 或 P 编号，只定位原活动会话。"
    },
    "command": {
      "type": "string",
      "minLength": 1,
      "maxLength": 4096,
      "description": "必填的非空命令，最多 4096 个 Unicode 码点且不能含 NUL。"
    },
    "root": {
      "type": "boolean",
      "default": false,
      "description": "true 明确请求 root；false 使用设备 shell 身份。"
    }
  },
  "required": ["reference", "command"],
  "additionalProperties": false,
  "description": "shell 需要 reference 与 command；root 可选，默认 false。"
}`),
	},
	{
		name: "diagnose",
		description: "只读查看自身近期有界事实；指定 O/P 时只查原归属，不返回树、日志、输入、命令或输出，" +
			"也不修复任何状态。reference 省略表示查看自身近期记录。" +
			"coverage=record_unavailable 表示近期记录不可用，改用 status 取当前事实。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reference": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "可选的 O 或 P 编号；省略表示查看自身近期记录。"
    }
  },
  "required": [],
  "additionalProperties": false,
  "description": "diagnose 的 reference 可选。"
}`),
		readOnly: true,
	},
}
