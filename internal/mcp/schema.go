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

// treeTextGuide is appended to the five tool descriptions whose results may
// carry a tree block. It is shared verbatim so the catalog has one syntax
// legend, not five drifting variants.
const treeTextGuide = "可读正文在 key=value 阶段事实之后附带一个确定性树块：" +
	"full 块以 `tree v1 format=full id=<O> scope=<scope> nodes=<n>` 开头，后接节点行；" +
	"delta 块以 `tree v1 format=delta id=<O> base=<O2> scope=<scope> changes=<n>` 开头，" +
	"后接 `~`/`+`/`-`/`children`/`=` 变更行；两者均以 `end tree` 结尾。" +
	"节点行为 `缩进 n<ref> <role> <bounds> \"label\"… value=\"…\" hint=\"…\" " +
	"tap|long_press|text|swipe enabled=… focused=… checked=… axis=… resource=\"…\" context`，" +
	"`n<ref>` 可直接用作 target.ref。delta 相对 base 累计，新 delta 覆盖旧 delta，不叠加。" +
	"补看块以 `inspect v1 scope=<tree|subtree|query> observation=<O> revision=<n> … nodes=<n>` " +
	"开头，tree/subtree 后接节点行，query 后接 `match i/N path=[…]` 与命中子树，并以 " +
	"`end inspect` 结尾。`omitted` 表示整块超过文本上限未渲染，可用 inspect 的 node/query 收窄。"

// toolCatalog is the complete public tool surface. Order is stable for tests
// and for clients that display tools in registration order.
var toolCatalog = []toolDefinition{
	{
		name: "start",
		description: "建立新会话并返回第一个 full 观察。请求体必须是空对象；" +
			"目标屏幕与模式来自控制端当前选择，不接受尺寸、模式或凭据参数。" +
			"启动初始化、激活与首观察成功发布使用五分钟租期。正常启动且无并发更新时，" +
			"首观察成功发布将租期推至发布时起五分钟，已有更晚期限则保留；以响应 session.expires_at 为准，网络交付会消耗剩余时间。" +
			"后续 observe/act/open_app/shell 等仅在既有续期点取 max(当前截止时间, now + 2分钟)，不截短启动租期。" +
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
		name: "act",
		description: "消费一个 ready 观察并派发 tap、long_press、text、swipe 或 key。" +
			"operation 只接受对应分支的成员；image=true 在动作后的同一次采集中附加 ImageContent。" +
			"坐标目标会在派发前按点解析节点并证明未变化，变化时返回 target_changed。" +
			"重验失败且已取得新帧时，结果另带一个新观察与 retry 入口：用该 O 加新 P 重试，不必再 observe。" +
			"派发回执不可用时结果记为 uncertain，本次调用即关闭该窗口，不阻塞后续 observe 或其他普通调用，应以一次新 observe 收窄不确定性。" +
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
          "enum": ["tap", "long_press", "text", "swipe", "key"],
          "description": "动作类型。"
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
      "description": "tap/long_press 接受 target；text 接受 target 与 text；swipe 接受 from 与 to；key 接受 key。"
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
		name: "apps",
		description: "只读发现本机可启动应用，可按本地化名称或包名做字面 Unicode " +
			"折叠子串过滤；不返回应用列表正文以外的执行事实。",
		schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "maxLength": 256,
      "description": "可选的本地化名称或包名子串；省略表示列出全部。"
    }
  },
  "required": [],
  "additionalProperties": false,
  "description": "apps 的 query 可选，最多 256 个 Unicode 码点。"
}`),
	},
	{
		name: "open_app",
		description: "消费一个 ready 观察并在其会话的显示上打开指定包名的应用。" +
			"app 只接受明确包名；返回 app 身份与后置观察。" +
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
    "app": {
      "type": "string",
      "minLength": 1,
      "maxLength": 255,
      "pattern": "^[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)*$",
      "description": "必填的 Android 包名。"
    }
  },
  "required": ["observation_id", "app"],
  "additionalProperties": false,
  "description": "open_app 需要 observation_id 与 app。"
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
