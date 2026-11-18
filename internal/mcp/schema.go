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

// treeTextGuide is the complete public syntax legend for readable tree blocks.
// It is published exactly once, at the head of the server instructions, and is
// never repeated inside a tool description.
const treeTextGuide = "可读正文在 key=value 阶段事实之后附带一个确定性树块：" +
	"块头后空一行进入正文。" +
	"full 块以 `tree v2 format=full id=<O> scope=<scope> view=<compact> nodes=<n> entries=<m>` 开头，后接节点行；" +
	"delta 块以 `tree v2 format=delta id=<O> base=<O2> view=compact scope=<scope> changes=<n>` 开头，" +
	"后接 `~`/`+`/`-`/`children`/`=` 变更行；两者均以 `end tree` 结尾。" +
	"节点行为 `缩进 n<ref> <role> <bounds> \"label\"… value=\"…\" hint=\"…\" " +
	"tap|long_press|text|swipe enabled=… focused=… checked=… axis=… resource=\"…\" context`；" +
	"`n<ref>` 可直接用作 target.ref。" +
	"匿名普通 view/group 的已知矩形在观察视图里省略该列，未知几何仍写 `-`；" +
	"连续匿名单子节点写成 `chain [n2 n3] <role>`，其中每个 ref 仍是可寻址的逻辑节点。" +
	"完整节点的状态位按已知默认省略：`enabled=true` 省略，`enabled=false`/`enabled=null` 显式；" +
	"`focused`/`selected`/`protected`/`checked` 的已知 `false` 省略，`true` 与 `null` 显式。" +
	"`set` 行不同：出现的状态字段一律保留显式 `false`/`true`/`null`，未出现才表示不修改。" +
	"`nodes` 为展开后逻辑节点数，`entries` 为普通节点或 chain 定义行数。" +
	"delta 相对 base 累计，新 delta 覆盖旧 delta，不叠加；修改行的 `bounds=hidden` 表示按策略不交付该几何。" +
	"补看块以 `inspect v2 scope=<tree|subtree|query> observation=<O> revision=<n> captured_at=<RFC3339> " +
	"tree_scope=<scope> view=detailed [root=<ref>|matches=<n> total=<n> truncated=<bool>] nodes=<n> entries=<m>` " +
	"开头，tree/subtree 后接完整详细节点行，query 后接 `match i/N path=[…]` 与命中子树，并以 " +
	"`end inspect` 结尾。" +
	"滚动用 `operation.type=scroll` + `operation.container` 表达；raw `swipe` 是原始手势，不承诺滚动语义。" +
	"树块 `tap|long_press|text|swipe` 动作列（`Node.actions`）中的 `swipe` 只是节点的源能力，不是 raw swipe 授权。" +
	"`scroll` 不承诺真实方向、触摸消费者或业务安全；`unstable`/`unknown` 的观察质量不阻止输入。"

// callerGuidance is the cross-tool part of the server instructions. It is
// written for the calling agent: session progression, O/P identity, tool
// selection, lease rules, text semantics and recovery. Tool-specific
// constraints stay in the tool descriptions.
const callerGuidance = "会话与推进：首次可直接 start({})；已有 active 会话时沿用，只有会话状态不明才 status({})。" +
	"active/starting 或 can_start=false 时不要 start；会话失效先按 recovery 处理，满足启动条件后再新建。" +
	"有可用 O 时直接 act；act 或 wait_for 返回新 O 时直接用该 observation.id。" +
	"只有没有可用 O、需要查看外部变化，或 recovery.entry=observe 时才 observe。\n\n" +
	"O 与 P：O 是一次观察，act 会消费 ready O，并以 observation.actionable_until 判断期限。" +
	"P 是调用的幂等/查询编号，每次新工作调用另有新 P；app 的 find 和 notify 的 P 可能没有会话。" +
	"请求引用统一用 reference：act 和 app 的变更 kind 必须是当前 ready O，inspect 必须是保留中的历史 O；" +
	"observe、wait_for、keepalive、end、shell、status 保持各自已有的 O/P 范围。" +
	"start 使用五分钟租期；普通活动在续期点只增不减，至少续至两分钟后；" +
	"status、inspect、app 查询不续租。keepalive 可重设并可缩短租期，但不延长 O。\n\n" +
	"选择工具：status 查状态；observe 新采集；snap 立即看当前画面；wait_for 等条件；act 消费 O；inspect 查历史详细树；" +
	"app 管应用生命周期；shell 执行命令且可能改屏；diagnose 查健康。" +
	"wait_for 到时是正常返回，检查 wait.trigger、wait.matched 后用返回的 O 继续；" +
	"observation.quality=unstable/unknown 本身不阻止输入，不要仅为追求 stable 重复 observe。\n\n" +
	"act 的 ref 点按/长按默认 bubble=true：原目标完全没有 tap、long_press、text 任一动作时，可交给最近的交互祖先做一次目标提升；" +
	"该祖先不支持本动作或不能生成唯一安全落点时就按恢复指引调整调用，不继续向更远祖先查找。" +
	"ref 静态拒绝按 code 区分：invalid_target 表示目标结构或几何不可用；" +
	"unsupported_node_action 表示请求动作或提升前提不满足；" +
	"no_safe_point 表示目标或祖先支持动作但固定候选都不可证明，此时不要向更远祖先找目标；" +
	"details 的 reason、阻塞/歧义 refs 与建议坐标只提供调用线索，建议坐标不是执行授权。" +
	"三类静态拒绝都不消费 O，按 recovery 用新 P 重试。" +
	"坐标目标不传 bubble；需要只点原目标时显式 operation.bubble=false。" +
	"一次调用只派发一个动作，不沿祖先逐层点击。\n\n" +
	"text 默认 press_enter=true，一次调用完成必要聚焦（已聚焦 ref 不重复点击）、把目标框全文设为 text，并以同一次写入调用中同一节点的实际刷新值精确读回；" +
	"读回一致后至多发送一次 ENTER，普通输入无需先 tap，也无需另发 ENTER。返回的 text_input 分别报告 write 与 enter 阶段。" +
	"仅填字、草稿、多行编辑、逐字段填写或清空但不提交时显式 operation.press_enter=false，只写入不发送 ENTER，也不返回 text_input。" +
	"ENTER 只证明按键原语，不证明搜索、提交或导航成功；业务结果由后观察中的页面事实确认。" +
	"text 错误的 details 固定给出 stage、reason 与 dispatch.focus/write/enter（not_attempted/not_dispatched/dispatched/unknown），" +
	"据此区分尚未派发、目标不唯一、读回不可用或不等、ENTER 未派发或未知；不要解析 message。" +
	"已认领 text 的整体 outcome=uncertain，且结果自身的 recovery.entry=observe、recovery.reference=P 时，按该结果恢复直接 observe 核对当前框值/页面。这个特定恢复覆盖通用的 uncertain 先 status 规则，不要原样重发。" +
	"text_input.enter=uncertain 时先观察当前页面，不要原样重发 ENTER；write=executed,enter=not_executed 且原会话仍活动时，" +
	"可取新 O 后按需单独发送 key=ENTER，不必重新填字。\n\n" +
	"恢复：retry/O 表示尚未派发，改正参数后用同一工具重试；结果带新 observation.id 就用它，否则用原 O。" +
	"observe/P 的 body reference 用已有 O/P 所指活动会话的 P（常见为 session.origin，也可用所属 operation_id）；" +
	"本次新调用另生新 P。status/P 按目标取 ID：当前动作用 operation_id，busy 用 blocking_operation_id，" +
	"自身恢复用 start_operation_id，收尾用 target_reference 或刚查询的 P；status(O) 撤销时对同一 O 调 end。" +
	"end/reference=P 用拥有目标会话的 P；user 按提示处理阻塞原因，update 需配套升级，两者不传引用。\n\n" +
	"not_executed 表示没有派发，参数错误可直接更正。uncertain 表示可能已派发：先 status；running 时继续跟踪；" +
	"finished 后仍不确定且会话活动时，用 observe 查看现状，再决定新动作；不要原样重发，也不要自动 end/restart。" +
	"executed 表示已确认派发；后观察、图片或清理失败不触发重打。device_busy 时跟随 blocking_operation_id 串行。" +
	"任务完成后正常 end，pending cleanup 按 recovery 查询；不要把普通 422、busy 或 uncertain 当成废弃整个会话。"

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
			"成功时返回 session 与首个 O，启动租期为五分钟，以 session.expires_at 为准；重放原 start 不重新获得五分钟。" +
			"宿主未提供 server instructions 时：O 是可消费观察，P 是该次操作的幂等/查询编号；" +
			"full 是完整树，delta 只相对其 base 且新 delta 覆盖旧 delta。",
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
			"按返回的 operation_state、can_start、blocking_operation_id 与 recovery 决定下一步。",
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
			"成功返回新 observation.id；后续动作或等待直接使用它。",
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
			"成功只返回 operation_id 与顶层 image 元数据，并以一个 ImageContent 交付 JPEG。" +
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
		description: "轮询当前会话的公开树条件；wait.trigger=timeout 也可以成功返回。" +
			"reference 与 timeout 必填；timeout 是 1..40 秒的轮询窗口（stable=true 时至少 2）。" +
			"query/absent_query 按 label/value/hint/resource 做字面包含匹配，只覆盖当前公开可见树；" +
			"stable=true 要求整树连续 3 帧一致，页面有倒计时或实时进度时保持 stable=false。" +
			"成功时检查 wait.trigger 和 wait.matched，直接用返回的新 observation.id 继续。" +
			"elapsed_ms 计自准入完成至终帧发布，实际耗时可超过 timeout 至多约 35 秒。",
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
      "maxLength": 256,
      "description": "可选的字面条件：公开树中存在至少一个节点在 label/value/hint/resource 上包含该文本。"
    },
    "absent_query": {
      "type": "string",
      "maxLength": 256,
      "description": "可选的字面条件：公开树中不存在任何节点在 label/value/hint/resource 上包含该文本。"
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
      "description": "true 时要求整树连续 3 帧一致；有查询时它是条件满足后的稳定门禁，无查询时它是独立触发分支。"
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
		description: "基于当前 ready 观察派发 tap、long_press、text、swipe、scroll 或 key，成功后返回动作后的观察。" +
			"reference 必填且必须是当前 ready O。静态拒绝不认领 O；一旦认领，原 O 即被消费。" +
			"operation 只接受对应分支成员：tap/long_press 用 target；text 用 target 与 text；" +
			"scroll 用 container、direction 与可选 amount（默认 page）；swipe 用 from/to 与可选 duration_ms；key 用 key 白名单。" +
			"tap/long_press 的 ref 目标默认 bubble=true：原目标完全没有 tap、long_press、text 任一动作时只向最近交互祖先提升一次；" +
			"该祖先不支持本动作时不再向更远祖先查找。坐标目标不带 bubble，显式 false 只点原目标。" +
			"text 默认 press_enter=true，已包含必要聚焦（已聚焦 ref 不重复点击）、全文写入、同节点刷新精确读回和至多一次 ENTER；普通输入无需先 tap 或另发 ENTER。" +
			"仅写入、草稿、多行或逐字段填写时显式 press_enter=false。" +
			"省略/true 的 text 结果固定带 text_input:{write,enter}；显式 false 不返回该对象。" +
			"text 失败按 details.stage/reason/dispatch 区分失败环节与原语派发（not_attempted/not_dispatched/dispatched/unknown），不要解析 message；" +
			"已认领 text 的整体 outcome=uncertain，且结果自身的 recovery.entry=observe、recovery.reference=P 时，直接 observe 核对当前框值/页面；" +
			"reference 用原活动会话的具体归属 P（如 session.origin 或已知属于该会话的 operation_id），不能把枚举 P 当编号，也不要原样重发。" +
			"image=true 在动作后的同一采集中附加 ImageContent。" +
			"滚动用 operation.type=scroll + container 表达，swipe 是原始手势，只承诺手势而不承诺滚动语义。" +
			"树块动作列里的 `swipe` 只是节点的源能力，不是 raw swipe 授权；" +
			"`scroll` 不承诺真实方向、触摸消费者或业务安全。" +
			"目标不支持该动作返回 422 unsupported_node_action，目标结构或几何不可用返回 422 invalid_target，" +
			"目标或提升祖先具备请求动作但固定候选都无法证明时返回 422 no_safe_point；" +
			"后者不要向更远祖先查找替代目标，details 的 reason、阻塞或歧义 refs 与可选建议坐标提供调用线索，" +
			"建议坐标不是执行授权，仍需新 P 独立准入。" +
			"这三类静态拒绝都是 not_executed，不消费 O，按结果带的 recovery 处理；" +
			"details 可带 nearest_actionable_ref 与 safe_point；" +
			"坐标目标或树变化返回 target_changed。" +
			"重验失败且结果带新 observation.id 时，用该 O 加新 P 重试，不必再 observe。" +
			"`unstable`/`unknown` 质量（observation.quality）不阻止输入。" +
			"outcome=uncertain 时先 status 跟踪，不原样重发；executed 只证明派发，不证明业务成功；其余 outcome 按结果带的 recovery 与 Instructions 的规则继续。",
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
          "description": "tap/long_press/text 的目标：ref 或 x/y 坐标，二选一。ref 目标在认领后按当前树重验或重算；坐标目标只保留原点授权。",
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
          "description": "仅 tap/long_press 且 target 为 ref 时适用；省略按 true 处理。true 允许在原目标完全没有 tap、long_press、text 任一动作时只向最近交互祖先提升一次，false 只点原目标。坐标目标必须省略，其他动作携带即被拒绝。"
        },
        "press_enter": {
          "type": "boolean",
          "description": "仅 text 适用；省略按 true 处理。true 在同节点刷新精确读回后至多发送一次 ENTER 并返回 text_input；false 只做必要聚焦、写入并读回，不发送 ENTER，也不返回 text_input。其他动作携带即被拒绝。"
        },
        "container": {
          "description": "scroll 的滚动容器选择器：ref 或 x/y 坐标，二选一。ref 必须直接指向 role=scroll 节点（不做祖先晋升）；x/y 取该点所在的滚动容器。认领后需唯一确认同一容器，轨迹按当前安全区间重验或重算。",
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
          "enum": ["page", "half"],
          "default": "page",
          "description": "scroll 可选幅度：page（默认）或 half；不接受其他值。"
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
      "description": "tap/long_press 接受 target 与按分支解释的 bubble；text 接受 target、text 与按分支解释的 press_enter；scroll 接受 container、direction 与可选 amount；swipe 接受 from、to 与可选 duration_ms；key 接受 key。"
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
			"返回 inspect v2 块：tree 为完整详细树，subtree 为指定 root 的子树，query 为命中节点的路径与子树。" +
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
      "maxLength": 256,
      "description": "可选字面查询；与 node 互斥。"
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
		description: "重设给定 O/P 所指当前活动会话的租约截止时间，不延长 O 的 actionable_until 或工作期限。" +
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
			"reference 必填，语义是该引用所属的原会话；返回 session.state=ended 与 cleanup。" +
			"cleanup 通常先 pending，已派发的 Android 命令不保证撤回；未完成时按 recovery 查询。",
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
		description: "统一应用生命周期入口，按 kind 选择互斥分支。" +
			"find：无会话应用发现，query 按本地化名称或包名做字面折叠子串过滤；" +
			"list：查询本 Agent 原会话的 live 任务（reference 必填，可为 ready O 或能解析到活动原会话的 P），" +
			"scope=session（默认）列会话虚屏任务，scope=primary 列主屏候选，app 可作包名过滤；find/list 都没有 outcome。" +
			"open：消费当前 ready O，用 target={app:包名} 在会话屏复用同包窗口或新建；" +
			"claim：把主屏精确 live 任务移入本会话虚屏并登记 borrowed；" +
			"handoff：把本会话虚屏的单个任务移回主屏 0，restore_foreground 默认 false；" +
			"close：关闭本会话虚屏目标窗口，allow_borrowed 默认 false；" +
			"kill：终止本会话 live owned 且同屏唯一可归因应用，target 只接受 task_id，allow_stop_playback 默认 false。" +
			"open/claim/handoff/close/kill 的 reference 必须是当前 ready O。" +
			"claim/handoff/close 的 target 为 task_id 或包名二选一；target 只是选择器，不单独授予权限。" +
			"模式来自原会话：主屏会话只允许 find/list/open，其余变更 kind 在消费 O 前拒绝。" +
			"五个变更 kind 的 outcome 为 executed/not_executed/uncertain，成功后返回原会话屏 full 无图观察；" +
			"失败时按结果带的 recovery 与 outcome 继续。",
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
		description: "保存一条消息并交给控制端显示。text 必填、非空、最多 4096 码点。" +
			"executed 只表示消息已保存并进入控制同步队列，不表示已显示或已读；" +
			"notification.state 固定为 pending，不需要重发。",
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
		description: "在给定 O/P 原会话的 Android shell 作用域内执行一条受管命令并返回终止事实。" +
			"reference 与 command 必填；command 非空、最多 4096 码点且不能含 NUL；" +
			"root=true 明确请求 root，不自动降级或提权重试。命令不进入 chroot，也不返回新树。" +
			"命令可能改变界面；若预期改屏，随后用 observe 取新观察。" +
			"成功返回 shell.termination 与 exit_code，非零退出仍是成功事实；output_unavailable 只表示输出不可读，不改变执行事实。",
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
