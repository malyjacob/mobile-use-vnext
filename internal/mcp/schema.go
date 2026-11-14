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
}

// treeTextGuide is appended to the six tool descriptions whose results may
// carry a tree block. It is shared verbatim so the catalog has one syntax
// legend, not six drifting variants.
const treeTextGuide = "可读正文在 key=value 阶段事实之后附带一个确定性树块：" +
	"块头后固定一行 `defaults {\"checked\":false}`，随后一个空行再写正文。" +
	"full 块以 `tree v2 format=full id=<O> scope=<scope> view=<compact> nodes=<n> entries=<m>` 开头，后接节点行；" +
	"delta 块以 `tree v2 format=delta id=<O> base=<O2> view=compact scope=<scope> changes=<n>` 开头，" +
	"后接 `~`/`+`/`-`/`children`/`=` 变更行；两者均以 `end tree` 结尾。" +
	"节点行为 `缩进 n<ref> <role> <bounds> \"label\"… value=\"…\" hint=\"…\" " +
	"tap|long_press|text|swipe enabled=… focused=… checked=… axis=… resource=\"…\" context`；" +
	"`n<ref>` 可直接用作 target.ref。" +
	"匿名普通 view/group 的已知矩形在观察视图里省略该列，未知几何仍写 `-`；" +
	"连续匿名单子节点写成 `chain [n2 n3] <role>`，其中每个 ref 仍是可寻址的逻辑节点。" +
	"`checked` 默认 `false`（省略），只有 `checked=true` 与 `checked=null` 显式出现。" +
	"`nodes` 为展开后逻辑节点数，`entries` 为普通节点或 chain 定义行数。" +
	"delta 相对 base 累计，新 delta 覆盖旧 delta，不叠加；修改行的 `bounds=hidden` 表示按策略不交付该几何。" +
	"补看块以 `inspect v2 scope=<tree|subtree|query> observation=<O> revision=<n> captured_at=<RFC3339> " +
	"tree_scope=<scope> view=detailed [root=<ref>|matches=<n> total=<n> truncated=<bool>] nodes=<n> entries=<m>` " +
	"开头，tree/subtree 后接完整详细节点行，query 后接 `match i/N path=[…]` 与命中子树，并以 " +
	"`end inspect` 结尾。" +
	"滚动用 `operation.type=scroll` + `operation.container` 表达；raw `swipe` 是原始手势，不承诺滚动语义。" +
	"树块 `tap|long_press|text|swipe` 动作列（`Node.actions`）中的 `swipe` 只是节点的源能力，不是 raw swipe 授权。" +
	"`scroll` 不承诺真实方向、触摸消费者或业务安全；`unstable`/`unknown` 的观察质量不阻止输入。"

// toolCatalog is the complete public tool surface. Order is stable for tests
// and for clients that display tools in registration order.
var toolCatalog = []toolDefinition{
	{
		name: "start",
		description: "建立新会话并返回第一个 full 观察。请求体必须是空对象；" +
			"目标屏幕与模式来自控制端当前选择，不接受尺寸、模式或凭据参数。" +
			"启动初始化、激活与首观察成功发布使用五分钟租期。正常启动且无并发更新时，" +
			"首观察成功发布将租期推至发布时起五分钟，已有更晚期限则保留；以响应 session.expires_at 为准，网络交付会消耗剩余时间。" +
			"后续 observe/act/app/shell 等仅在既有续期点取 max(当前截止时间, now + 2分钟)，不截短启动租期。" +
			"显式 keepalive 可重设并缩短它。五分钟不延长单次 start 工作期限，也不延长首个 O 的独立输入有效期；" +
			"重放原 start 操作不重新获得五分钟。" +
			treeTextGuide,
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
		description: "只读查询自身准入状态、O 状态或 P 状态。reference 省略时查询自身；" +
			"status 不登记 P、不续租、不认领观察。",
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
		description: "对给定 O/P 的原会话执行一次新采集并返回新观察。full=true 强制 full；" +
			"image=true 在同一次采集中附加一个 ImageContent（与树事实同源）；" +
			"wait_ms 是采集前等待毫秒数（0–10000，默认 0）。" +
			treeTextGuide,
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
		name: "wait_for",
		description: "等待当前会话的界面条件并在条件满足或 timeout 到时后返回一个新观察。" +
			"timeout 必填、1..40 秒（stable=true 时需 >= 2）：到时同样按成功返回（isError=false）并发布验证采集得到的那个观察；" +
			"调用方须检查 wait.trigger 与 wait.matched，连续 3 帧稳定由 observation.quality.state=stable 表达。" +
			"wait.elapsed_ms 计自准入完成至终帧发布，因需补采或复用验证帧，实际耗时可超过 timeout 至多约 35 秒。" +
			"query 与 absent_query 只匹配当前公开可见树（不含视口外内容），且 stable=true 对整树计算摘要——" +
			"页面若有倒计时或实时进度变化请保持 stable=false，否则会跑满 timeout。" +
			treeTextGuide,
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
      "description": "true 时在同一验证采集中附加一个 ImageContent；条件命中帧会复用而不重采。"
    }
  },
  "required": ["reference", "timeout"],
  "additionalProperties": false,
  "description": "wait_for 需要 reference 与 timeout；query、absent_query、stable、full、image 均可选。"
}`),
	},
	{
		name: "act",
		description: "消费一个 ready 观察并派发 tap、long_press、text、swipe、scroll 或 key。" +
			"operation 只接受对应分支的成员；image=true 在动作后的同一次采集中附加 ImageContent。" +
			"滚动用 operation.type=scroll + container 表达：direction 声明阅读方向，amount 选 page 或 half；" +
			"swipe 是原始手势，只接受 from/to 与可选 duration_ms，不承诺滚动语义。" +
			"坐标目标会在派发前按点解析节点并证明未变化，变化时返回 target_changed。" +
			"重验失败且已取得新帧时，结果另带一个新观察与 retry 入口：用该 O 加新 P 重试，不必再 observe。" +
			"派发回执不可用时结果记为 uncertain，本次调用即关闭该窗口，不阻塞后续 observe 或其他普通调用，应以一次新 observe 收窄不确定性。" +
			"观察的 `unstable`/`unknown` 质量（observation.quality）不阻止输入。" +
			treeTextGuide,
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "observation_id": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "必填的 ready O 编号。"
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
        "container": {
          "description": "scroll 的滚动容器选择器：ref 或 x/y 坐标，二选一。ref 必须直接指向 role=scroll 节点（不做祖先晋升）；x/y 取该点所在的滚动容器。",
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
          "description": "text 分支要写入的字符串，可为空，最多 4096 个 Unicode 码点。"
        }
      },
      "required": ["type"],
      "additionalProperties": false,
      "description": "tap/long_press 接受 target；text 接受 target 与 text；scroll 接受 container、direction 与可选 amount；swipe 接受 from、to 与可选 duration_ms；key 接受 key。"
    },
    "image": {
      "type": "boolean",
      "default": false,
      "description": "true 时在动作后的同一次采集中附加一个 ImageContent。"
    }
  },
  "required": ["observation_id", "operation"],
  "additionalProperties": false,
  "description": "act 需要 observation_id 与 operation；image 可选。"
}`),
	},
	{
		name: "inspect",
		description: "只读查看保留中的历史观察树，不采集、不续租、不确认基线、不认领 O。" +
			"node 与 query 互斥；query 是字面 Unicode 折叠子串匹配。" +
			treeTextGuide,
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "observation_id": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
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
  "required": ["observation_id"],
  "additionalProperties": false,
  "not": {"required": ["node", "query"]},
  "description": "inspect 需要 observation_id；node 与 query 至多出现一个。"
}`),
		readOnly: true,
	},
	{
		name: "keepalive",
		description: "重设给定 O/P 所指当前活动会话的租约截止时间。duration_minutes 为 1～120 的整数，默认 2；" +
			"每个新操作从服务端应用更新时计时并覆盖原截止时间，可延长或缩短。" +
			"省略参数也会重设为两分钟，可能缩短已有长租约或 start 的五分钟启动租期。" +
			"start 使用五分钟续期；其他普通活动仅在既有续期点取 max(现有截止时间, now + 2分钟)，" +
			"不会缩短长租约，也不继承本次时长；它们可再次延长已缩短的租约。" +
			"无会话累计时长硬上限，120 分钟只约束单次显式请求。调用不等待旧租约到期、不占普通工作位；" +
			"已到期或结束不可恢复：ended 返回 session_ended，starting 返回 reference_has_no_session，已过期限返回 session_expired。" +
			"同一可识别 P 重试不再次更新，改变租期须用新操作；P 记录仍有界保留，不提供无限历史去重。" +
			"只改会话期限，不延长 O 的 actionable_until 或工作/shell 期限，不改变 O 状态、不确认基线、不访问设备或新增账本写入；" +
			"STOP/end、App/服务生命周期与 MCP 取消规则仍适用。" +
			"首次成功返回本次 operation_id、outcome=executed 和 session，session.expires_at 是当次更新后的绝对截止时间；" +
			"重放可投影原会话的当前事实，后续更新或撤权仍可改变会话事实；继续保留会话须在当前截止时间前成功续期。",
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
			"返回的 cleanup 通常先是 pending；已派发的 Android 命令不保证撤回。",
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
			"find：无会话应用发现，可选 query（最多 256 码点）按本地化名称或包名做字面 Unicode 折叠子串过滤；成功/失败均无 outcome。" +
			"list：查询本 Agent 原会话（reference 必填）的 live 任务；scope=session（默认）列原会话虚屏任务，" +
			"scope=primary 列主屏候选；app 可选作明确包名过滤；成功/失败均无 outcome。" +
			"open：消费当前 ready O，在会话屏复用同包窗口或新建；主屏只打开、不登记用户任务。" +
			"claim：把主屏精确 live 任务移入本会话虚屏并登记 borrowed。" +
			"handoff：把本会话虚屏的单个任务移回主屏 0，默认不恢复前台（restore_foreground=false）。" +
			"close：关闭本会话虚屏目标窗口，默认拒绝 borrowed 窗口（allow_borrowed=false）。" +
			"kill：终止本会话 live owned 且同屏唯一可归因任务对应的应用精确进程；target 只接受 task_id，" +
			"默认拒绝已知前台服务或播放（allow_stop_playback=false）。" +
			"claim/handoff/close 的 target 为 {task_id: 正整数} 或 {app: 包名} 二选一；kill 只接受 task_id。" +
			"target 是选择器，不单独授予权限。模式来自原会话：主屏会话只允许 find/list/open，" +
			"claim/handoff/close/kill 在消费 O 前拒绝。" +
			"五个变更 kind（open/claim/handoff/close/kill）的主 outcome 为 executed/not_executed/uncertain，" +
			"成功后返回原会话屏 full 无图观察。" +
			treeTextGuide,
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
      "description": "list 必填：本 Agent 活动原会话的 O 或 P。"
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
      "description": "明确 Android 包名：open 必填；list 可作过滤。"
    },
    "observation_id": {
      "type": "string",
      "minLength": 1,
      "maxLength": 64,
      "pattern": "^[OP]",
      "description": "open/claim/handoff/close/kill 必填的当前 ready O 编号。"
    },
    "target": {
      "description": "claim/handoff/close：task_id 与 app 恰好一种；kill 只接受 task_id。",
      "oneOf": [
        {
          "type": "object",
          "properties": {
            "task_id": {"type": "integer", "minimum": 1, "description": "精确 leaf task ID。"}
          },
          "required": ["task_id"],
          "additionalProperties": false
        },
        {
          "type": "object",
          "properties": {
            "app": {
              "type": "string",
              "minLength": 1,
              "maxLength": 255,
              "pattern": "^[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)*$",
              "description": "按包名精确匹配；零个或多个候选时拒绝。"
            }
          },
          "required": ["app"],
          "additionalProperties": false
        }
      ]
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
  "allOf": [
    {
      "if": {"properties": {"kind": {"const": "find"}}, "required": ["kind"]},
      "then": {"not": {"anyOf": [
        {"required": ["reference"]}, {"required": ["observation_id"]}, {"required": ["target"]},
        {"required": ["app"]}, {"required": ["scope"]}, {"required": ["restore_foreground"]},
        {"required": ["allow_borrowed"]}, {"required": ["allow_stop_playback"]}
      ]}}
    },
    {
      "if": {"properties": {"kind": {"const": "list"}}, "required": ["kind"]},
      "then": {"required": ["reference"], "not": {"anyOf": [
        {"required": ["observation_id"]}, {"required": ["target"]}, {"required": ["query"]},
        {"required": ["restore_foreground"]}, {"required": ["allow_borrowed"]},
        {"required": ["allow_stop_playback"]}
      ]}}
    },
    {
      "if": {"properties": {"kind": {"const": "open"}}, "required": ["kind"]},
      "then": {"required": ["observation_id", "app"], "not": {"anyOf": [
        {"required": ["reference"]}, {"required": ["scope"]}, {"required": ["target"]},
        {"required": ["query"]}, {"required": ["restore_foreground"]},
        {"required": ["allow_borrowed"]}, {"required": ["allow_stop_playback"]}
      ]}}
    },
    {
      "if": {"properties": {"kind": {"const": "claim"}}, "required": ["kind"]},
      "then": {"required": ["observation_id", "target"], "not": {"anyOf": [
        {"required": ["reference"]}, {"required": ["scope"]}, {"required": ["app"]},
        {"required": ["query"]}, {"required": ["restore_foreground"]},
        {"required": ["allow_borrowed"]}, {"required": ["allow_stop_playback"]}
      ]}}
    },
    {
      "if": {"properties": {"kind": {"const": "handoff"}}, "required": ["kind"]},
      "then": {"required": ["observation_id", "target"], "not": {"anyOf": [
        {"required": ["reference"]}, {"required": ["scope"]}, {"required": ["app"]},
        {"required": ["query"]}, {"required": ["allow_borrowed"]},
        {"required": ["allow_stop_playback"]}
      ]}}
    },
    {
      "if": {"properties": {"kind": {"const": "close"}}, "required": ["kind"]},
      "then": {"required": ["observation_id", "target"], "not": {"anyOf": [
        {"required": ["reference"]}, {"required": ["scope"]}, {"required": ["app"]},
        {"required": ["query"]}, {"required": ["restore_foreground"]},
        {"required": ["allow_stop_playback"]}
      ]}}
    },
    {
      "if": {"properties": {"kind": {"const": "kill"}}, "required": ["kind"]},
      "then": {"required": ["observation_id", "target"], "not": {"anyOf": [
        {"required": ["reference"]}, {"required": ["scope"]}, {"required": ["app"]},
        {"required": ["query"]}, {"required": ["restore_foreground"]},
        {"required": ["allow_borrowed"]}
      ]}}
    }
  ],
  "description": "app 需要 kind；各分支只接受自己的成员，服务严格解码为权威。"
}`),
	},
	{
		name: "notify",
		description: "保存一条消息并尝试交给控制端显示。executed 只表示消息已保存，" +
			"不表示已读或已完成通知交付。",
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
			"命令不进入 chroot；root=true 明确请求 root，不自动降级或提权重试。" +
			"命令可能改变界面且不会返回新树；若预期会改屏，随后应 observe。",
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
		description: "只读查看自身近期有界事实；指定 O/P 时只查原归属。" +
			"不返回树、日志、输入、命令或输出，也不修复任何状态。",
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
