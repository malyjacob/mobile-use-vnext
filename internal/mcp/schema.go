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

// treeTextGuide is the complete public syntax legend for readable tree blocks.
// It is published exactly once, at the head of the server instructions, and is
// never repeated inside a tool description.
const treeTextGuide = "树块：当前树由 base 指向的 full 加本次 delta 重建。同一 base 的每条 delta 单独应用到该 full。\n" +
	"可读正文在 key=value 阶段事实之后附带一个确定性树块：" +
	"块头后空一行进入正文。" +
	"full 块以 `tree v3 format=full id=<O> scope=<scope> view=<compact> nodes=<n> entries=<m>` 开头，后接节点行；" +
	"delta 块以 `tree v3 format=delta id=<O> base=<O2> view=compact scope=<scope> changes=<n>` 开头，" +
	"后接 `~`/`+`/`-`/`children`/`=` 变更行；两者均以 `end tree` 结尾。\n" +
	"普通节点行为 `缩进 n<ref> [<role>] [<bounds>] \"label\"… value=\"…\" hint=\"…\" " +
	"tap|long_press|text|swipe enabled=… focused=… selected=… protected=… checked=… axis=… resource=\"…\" context`；" +
	"`n<ref>` 可直接用作 target.ref。" +
	"省略 `role` 时按 ref 后首个剩余列恢复：未隐藏几何 `[l,t,r,b]` 或 `-`、`value=`、`hint=`、" +
	"`tap`/`long_press`/`swipe`、状态、`axis=`、`resource=`、`context` 或行尾默认 `view`；" +
	"隐藏几何后直接是引号标签时默认 `text`。仅当恢复出的角色等于原角色时才省略，其他角色始终显式；" +
	"任何普通节点也可显式写角色，显式角色覆盖默认。" +
	"首项为引号标签或角色词（包括输入动作 `text`）的 `view` 必须显式写 `view`；" +
	"无标签或带几何列的 `text` 必须显式写 `text`。" +
	"观察视图的几何列：根和有动作的节点交付已知矩形；无动作的 `text` 与匿名普通 view/group 省略矩形；" +
	"其余展示节点与父链上最近交付的矩形四边相同、或裁剪到屏幕后小于屏幕面积 1% 时省略；" +
	"未知几何仍写 `-`，缺失几何列表示按策略隐藏，详细补看交付原矩形；" +
	"合法匿名默认 `view` 链在隐藏几何且其余共享字段原本无输出时写成 `[n2 n3]`；" +
	"`group` 链和带输出字段的链仍写成 `chain [n2 n3] <role> ...`，其中每个 ref 仍是可寻址的逻辑节点。" +
	"完整节点的状态位按已知默认省略：`enabled=true` 省略，`enabled=false`/`enabled=null` 显式；" +
	"`focused`/`selected`/`protected`/`checked` 的已知 `false` 省略，`true` 与 `null` 显式。" +
	"`set` 行不同：出现的状态字段一律保留显式 `false`/`true`/`null`，未出现才表示不修改。" +
	"`nodes` 为展开后逻辑节点数，`entries` 为普通节点或 chain 定义行数。" +
	"修改行的 `bounds=hidden` 表示按策略不交付该几何。\n" +
	"补看块以 `inspect v3 scope=<tree|subtree|query> observation=<O> revision=<n> captured_at=<RFC3339> " +
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
const callerGuidance = "会话与推进：首次可直接 start({})。已有 active 会话时沿用，只有会话状态不明才 status({})。" +
	"active/starting 或 can_start=false 时不要 start；会话失效先按 recovery 处理。\n" +
	"有可用 O 时直接 act；act 或 wait_for 返回新 O 时直接用该 observation.id。" +
	"没有可用 O、需要查看外部变化或 recovery.entry=observe 时才 observe。\n\n" +
	"O 与 P：O 是观察，observation.actionable_until 是其输入期限。P 是调用的幂等/查询编号，新工作另有新 P。" +
	"app.find 与 notify 可脱离会话，notify 无 reference。请求引用统一用 reference。\n" +
	"- 当前 ready O：act、app 的五个变更 kind。保留中的历史 O：inspect。\n" +
	"- 活动原会话的 O/P：observe、snap、wait_for、keepalive、shell。app.list 接受 ready O 或可定位活动原会话的 P。\n" +
	"- end 的 O/P 定位原会话。status、diagnose 可用 O/P 查原事实，省略时查自身。\n" +
	"start 租期五分钟；普通活动在续期点只增不减，至少续至两分钟后。" +
	"status、inspect、diagnose、snap、app 查询不续租。keepalive 可重设并缩短租期，不延长 O。\n\n" +
	"选择工具：observe 单次采集，snap 只看画面，wait_for 等公开树条件，inspect 补看历史树，diagnose 查健康。" +
	"wait_for 以 wait.trigger 说明退出分支，以 wait.matched 说明终帧命中项；有新 O 就用它继续。" +
	"observation.quality=unstable/unknown 本身不阻止输入。\n\n" +
	"输入：act 认领 ready O 才消费。ref 点按/长按默认 bubble=true，最多提升到最近交互祖先一次；" +
	"operation.bubble=false 只点原目标，坐标省略 bubble。no_safe_point 按 act 给出的点提示调整，避免继续向更远祖先找目标。" +
	"text 默认 press_enter=true，写入全文并精确读回后至多发送一次 ENTER。仅填字时显式 operation.press_enter=false，不发送 ENTER。" +
	"text_input 分别报告 write 与 enter，业务结果用页面事实核对。\n\n" +
	"恢复：retry/O 表示尚未派发，用结果的新 observation.id 或仍适用的原 O 发起新调用。" +
	"observe/P 用原活动会话的具体归属 P，可用 session.origin 或同会话 operation_id。请求中的 reference 使用实际编号。" +
	"status/P 按目标取编号：当前动作用 operation_id，忙用 blocking_operation_id，" +
	"自身恢复用 start_operation_id，收尾用 target_reference 或刚查询的 P。" +
	"status(O) 撤销时对同一 O 调 end；end/reference=P 用原会话的归属 P。user 处理阻塞，update 配套升级，两者均不传引用。\n" +
	"已认领 text 的整体 outcome=uncertain，且结果给出 recovery.entry=observe、recovery.reference=P 时，直接 observe 核对当前框值/页面。" +
	"text_input.enter=uncertain 时先观察；write=executed、enter=not_executed 且原会话活动时，取新 O 后可按需单发 ENTER。\n" +
	"通常 uncertain 先用 status 跟踪原 P，running 时继续跟踪，finished 且会话活动时再 observe。避免原样重发或自动 end/restart。" +
	"not_executed 表示未派发；executed 后如仅观察、图片或清理失败，保留执行事实。device_busy 时跟随阻塞 P 串行。" +
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
			"成功时返回 session，首采成功时给出首个 O。启动租期为五分钟，以 session.expires_at 为准；重放原 start 不重新获得五分钟。" +
			"O 是可消费观察，P 是该次操作的幂等/查询编号。" +
			"full 是完整树；用 delta 声明的 base full 加本次 delta 重建当前树。",
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
		description: "轮询当前会话的公开树条件。reference 与 timeout 必填，timeout 为 1..40 秒（stable=true 时至少 2）。" +
			"\nquery/absent_query 按 label/value/hint/resource 做字面折叠子串匹配；同时提供时任一条件成立即可满足查询条件。" +
			"stable=true 还要求整树连续 3 帧一致。倒计时或实时进度页面可保持 stable=false。" +
			"\n发布新 observation.id 时调用成功，包括 wait.trigger=timeout。wait.matched 包含终帧命中的条件，超时时还包含 timeout。" +
			"最终未发布新 O 时按观察失败处理。elapsed_ms 含终帧采集，实际耗时可超过 timeout 至多约 35 秒。",
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
		description: "对当前 ready O 执行交互。reference 必填。静态拒绝不认领 O，认领即消费原 O。image=true 可随动作后观察附加 ImageContent。" +
			"\n分支：tap/long_press 用 target；text 用 target 与 text；key 用 key 白名单。scroll 用 container/direction，可选 amount；swipe 用 from/to，可选 duration_ms。" +
			"\n点按与长按：ref 默认 bubble=true。仅在原节点没有 tap、long_press、text 任一动作时，向最近的交互祖先提升一次。" +
			"该祖先不支持本动作或无安全点时停止提升。显式 bubble=false 只点原目标，坐标目标省略 bubble。" +
			"静态拒绝均为 not_executed：invalid_target 表示目标结构或几何不可用，unsupported_node_action 表示动作或提升前提不满足，no_safe_point 表示有动作但安全点无法证明。" +
			"详情可给 reason、阻塞或歧义 refs、nearest_actionable_ref 与 safe_point。no_safe_point 带 error.details.suggested_point 且原 O 仍 ready 时，" +
			"用同一 O、新 P 与 operation.target={x,y} 尝试坐标分支，省略 bubble，并独立通过准入和重验。" +
			"\n文本：默认 press_enter=true。必要聚焦（已聚焦 ref 跳过）、全文写入、同节点刷新精确读回，随后至多一次 ENTER。" +
			"普通输入无需先 tap 或另发 ENTER。仅填字、草稿、多行、清空或逐字段编辑时显式 press_enter=false，只写入并读回。默认分支返回 text_input:{write,enter}，false 时省略。" +
			"文本错误看 details.stage/reason/dispatch，区分 not_attempted/not_dispatched/dispatched/unknown。" +
			"已认领 text 的整体 outcome=uncertain（也可能仅已派发聚焦），且实际结果给出 recovery.entry=observe、recovery.reference=P 时，" +
			"直接 observe 核对当前框值/页面。reference 使用原活动会话的具体归属 P，如 session.origin 或属于该会话的 operation_id。避免原样重发。" +
			"\n滚动：用 operation.type=scroll + container，嵌套时按想滚动的区域选对应容器 ref。swipe 是原始手势。树节点的 `swipe` 仅表示源能力，手势仍按当前 O 准入。" +
			"返回的 effect.state 以 observed/not_observed/unknown 报告内容位移证据；执行事实见 outcome。scroll 不承诺真实方向、触摸消费者或业务安全。" +
			"\n恢复：重验目标变化可返回 target_changed；若附新 observation.id，直接用新 O 和新 P 重试。unstable/unknown 的 observation.quality 不阻止输入。" +
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
          "description": "仅 tap/long_press 且 target 为 ref 时适用；省略按 true 处理；false 只点原目标。坐标目标和其他动作须省略。"
        },
        "press_enter": {
          "type": "boolean",
          "description": "仅 text 适用；省略按 true 处理；false 禁止发送 ENTER，并省略 text_input。其他动作须省略。"
        },
        "container": {
          "description": "scroll 的滚动容器选择器：ref 或 x/y 坐标，二选一。ref 必须直接指向 role=scroll 节点（不做祖先晋升）；x/y 选择该点所在的滚动容器。",
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
			"返回 inspect v3 块：tree 为完整详细树，subtree 为指定 root 的子树，query 为命中节点的路径与子树。" +
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
		description: "按 kind 发现应用、查询任务或变更应用生命周期。" +
			"\nfind 无需会话，query 可按应用名称或包名做字面折叠子串过滤。" +
			"\nlist 查询原会话的 live 任务，reference 用 ready O 或定位活动原会话的 P。scope=session 列会话任务，scope=primary 列主屏候选，可按 app 包名过滤。find/list 省略 outcome。" +
			"\n变更分支使用当前 ready O。主屏会话只允许 find/list/open，其余分支在消费 O 前拒绝。" +
			"\n- open 用 target={app:包名} 复用同包窗口或新建。" +
			"\n- claim 将主屏的精确 live 任务移入本会话虚屏并登记 borrowed。" +
			"\n- handoff 将本会话虚屏的单个任务移回主屏 0，可选恢复前台。" +
			"\n- close 关闭本会话虚屏目标窗口，可显式允许关闭 borrowed。" +
			"\n- kill 用 task_id 终止本会话 live owned 且同屏唯一可归因的应用，可显式允许停止播放。" +
			"\nclaim/handoff/close 的 target 用 task_id 或包名二选一，仍须通过服务准入。" +
			"变更结果按 outcome 与 recovery 处理。已执行后尝试发布原会话屏 full 无图观察。",
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
		description: "在给定 O/P 原会话的 Android shell 作用域内执行一条受管命令。reference 与 command 必填。" +
			"root=true 明确请求 root。命令不进入 chroot，可改变界面且不返回新树。" +
			"\n受管命令不绑定会话显示屏：input 一类命令默认作用于 display 0（主屏），在虚拟屏会话里回执仍是 executed/exited、exit_code=0，但会话屏不会移动。" +
			"要驱动会话屏须自行带 input -d <display_id>，该 id 见 observation.screen.display_id。" +
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
