# MCP 入口

`cmd/device-mcp` 是设备服务的 MCP 适配进程。两种传输共用同一套工具注册、JSON Schema、
严格参数接入与结果投影；它只通过 [Go 客户端](client.md) 访问服务的 `/api/<tool>`，不直接
访问引擎、ADB 或账本，也不保存第二份会话或操作权威。默认部署还会把同一适配器安装到手机
chroot，并在 `127.0.0.1:18882` 启动 HTTP 入口；主机侧 stdio/HTTP 适配器仍可按本页运行。

## 1. 协议与进程边界

- 使用 MCP `2026-07-28` 的新协议路径：客户端以 `server/discover` 取得服务能力并协商版本；
  SDK 服务只发布 `2026-07-28`。收到 `2025-11-25` 的 `initialize` 时，SDK 按生命周期规范回答
  同一版本并期望客户端断开；该回答不是本适配器的新协议协商结果，也不对应第二套工具、
  别名或低版本协议回退。
- 工具目录固定为 13 个，与 [接口契约](interface.md) 同名同义：`start`、`status`、`observe`、
  `snap`、`wait_for`、`act`、`inspect`、`keepalive`、`end`、`app`、`notify`、`shell`、
  `diagnose`。不注册 `/health`、认证初始化、旧工具别名或旧协议回退。
- 目录指纹是工具数、注册顺序、名称、描述与入参 schema 的 canonical SHA-256。当前源码的
  工具目录指纹为 `25a09a66f54e728b1c9fc67e6898514c6ac7438b9976528ece7e36bd4f5eab43`。
  发布清单中的指纹对应其记录的源码提交。发布时从目标提交重建并核验清单。
  探针按工具名比对实际 `tools/list` 的描述和入参 schema，并核对只读注解。传入 `-manifest`
  时，探针对每份清单核对构建发行号、契约号、工具数和工具目录 SHA-256。探针将
  `server/discover` 返回的 server instructions 与构建内的固定说明源逐字节比较，并附加兼容
  `initialize` 检查。
- stdio 模式的 stdout 只承载 MCP 协议帧；日志与诊断一律写 stderr。
- 私有配置把适配进程绑定到一个可信 Agent 的服务凭据；该凭据不是工具参数，不进入日志、
  错误、结果或 MCP 正文。

## 2. 工具 Schema 与严格接入

每个工具以显式 JSON Schema 发布：`type=object`、`properties`、`required` 与
`additionalProperties=false`。可选参数缺省等于其声明默认值；`act.operation` 只允许
`tap`、`long_press`、`text`、`swipe`、`scroll`、`key` 六个分支及其声明成员。`act`、
`inspect` 与 `app` 的请求引用字段统一为 `reference`；旧 `observation_id` 是未知成员，
不提供别名。`act` 的 `reference`、`app` 的五个变更 kind 必须指向当前 ready O；
`inspect` 必须指向保留中的历史 O；`app.list` 仍接受能解析到活动原会话的 P。
`act` 与 `inspect` 的 `reference` schema 都要求以 `O` 开头。`act` 静态拒绝不认领 O，
成功认领即消费原 O。
`inspect.query` 的 schema 声明 1–256 个 Unicode 码点。
`operation.bubble` 与 `operation.auto_submit` 是 presence-aware 布尔，省略与显式 true 在该
适用分支等价；`operation.submit_ref` 只适用于 `text`，省略为 `"enter_key"`，否则必须是满足
`^n[1-9][0-9]*$` 的公开节点编号且不超过 64 个字符。`bubble` 只对 ref `tap`/`long_press`
生效，`auto_submit` 与 `submit_ref` 只对 `text` 生效；坐标目标或其他动作携带即为
`422 invalid_request`。`auto_submit=false` 时 `submit_ref` 仍做类型与语法校验，随后忽略提交
语义。已废除的 `press_enter` 不是别名，按未知成员拒绝。schema 的字段说明写明默认值，不发布
可能被误读为全类型生效的通用 `default`。

`act` 的滚动与手势输入与 [接口契约](interface.md) §6 一致：`scroll` 用 `container`（ref 或
坐标）与 `direction`（`up`/`down`/`left`/`right`）表达，可选 `amount` 为闭合四档
`step`/`half`/`page`/`full`（可用跨度 15%/30%/60%/85%，注入时长 500/450/400/300ms，最小行程
32/48/48/48px，省略为 `page`，其他值不接受）；
`swipe` 仍以 `from`/`to` 表达原始手势，并接受可选 `duration_ms`（50–2000，默认 300）。
分支成员互斥：`scroll` 不接受 `target`/`from`/`to`/`key`/`text`/`duration_ms`（滚动时长由
服务端按档位固定），其他分支也不接受 `container`/`direction`/`amount`。`operation` schema
因此暴露 `scroll`、`container`、`direction`、`amount`、`duration_ms`、`bubble`、`auto_submit`
与 `submit_ref`；服务端严格解码仍是权威。

`tap`/`long_press` 的 `ref` 目标自身已有请求动作时（无论 `bubble` 值），均走直接目标规划（4 级选点含 3×3 网格；全败时若存在唯一双向同视口可点击子容器则自动委托并双绑定重验）。原目标完全没有 `tap`、`long_press`、`text` 动作且有效 `bubble=true`（省略或显式 true）时，先在原目标视觉区内按最近 touch-interactive 祖先 `A` 规划（`bubble_ancestor`；遇竞争交互控件重合即止漂）；若该路不可行，再吸附满足局部共位与反底板守卫的唯一可点击目标 `B`（`bubble_colocated`）。静态规划同时绑定原 child 与执行目标；认领后重验两者、公开 Role 与完整 Label、关系守卫及安全点，失配返回 `409 target_changed`（附封闭 `details.reason`），零派发。有效 `bubble=false` 禁用 `bubble_ancestor` 与 `bubble_colocated`（原目标自身已有请求动作时仍走直接规划并可使用 `coextensive_child`），坐标目标必须省略 `bubble`。该解析只在一次调用内派发一个动作，不模拟 DOM 多级事件传播。

`scroll` 支持双向同视口（宽与高交集各 ≥85%）滚动栈直传不互阻、非滚动节点 `ref` 自动向上找最近滚动祖先提升、坐标选最内层并在不可行时沿命中祖先回退或唯一可执行消歧，以及在安全区间正中对称展开的 3 轴线（50%/25%/75%）居中轨迹。容器公开 `axis` 与请求方向冲突，或未知轴向的嵌套极端扁平条带与请求轴不兼容时，服务沿公开父链自动回退到兼容外层滚动祖先；父链无解时尝试唯一轴向兼容的内层容器，仍无解才返回 `422 unsupported_node_action`（`reason=axis_mismatch`），若有兼容容器进入规划但均失败则返回其规划错误。坐标 `tap`/`long_press`/`text` 使用最深命中集合证明并按请求动作唯一拥有者消歧，raw `swipe` 永不返回 `ambiguous_point`。

除参数语法、`409 node_not_found`/`node_disabled` 及 `text` 的非可编辑/受保护拒绝（`422 text_target_uncertain`）外，全 `act` 静态目标解析与规划拒绝统一收口为 5 个正交 `422` 错误码：`invalid_target`、`unsupported_node_action`、`ambiguous_point`、`no_safe_point`、`no_safe_trajectory`（`scroll` 的轴向失配为 `unsupported_node_action` + `reason=axis_mismatch`）。静态拒绝不认领 O，结果为 `not_executed`；`error.details` 携带封闭 `reason`、按出口约定的 `scope`/`scope_ref`，有候选时附至多 4 项经当前同一 O 干跑计算的 `candidates`（含 `ref`/`role`/`actions`/`bounds`/`relation`/`executable`），以及可选 `blocking_refs`（至多 3 项）。原 O 仍 ready 且 `candidates` 中存在 `executable=true` 的项时，可直接取首个 `executable=true` 的 `ref` 用同一 O 与新 P 重试，无需重观察或手算坐标；需要查看诊断子树时仍可用 `inspect({reference: 原 act 的 O, node: ref})` 补看。

非 `text` 动作在认领后重验因祖先链失配返回 `409 target_changed (reason=lineage_changed)` 时，
`error.details.changed_ref` 可给出原目标公开祖先链中第一个与新鲜帧失配且能唯一确定的公开祖先
ref；无法唯一定位、祖先是 `root` 或计算失败时省略，不编造。该字段只进入错误详情，不写入
事件日志；坐标目标与裸 `swipe` 的重验不返回 `changed_ref`。

`text` 默认 `auto_submit=true`、`submit_ref="enter_key"`：一次调用完成必要聚焦（已聚焦 ref
不重复点击）、将目标框全文设为 text、以同一次写入调用中同一节点的刷新值精确读回，随后至多
发送一次 ENTER；普通输入不需要先 tap 或另发 ENTER。显式 `operation.auto_submit=false` 只做
必要聚焦、写入并读回，不发送提交原语，也不返回 `text_input`；此时 `submit_ref` 即使出现也
只做语法校验。已知页面提交按钮时用 `operation.submit_ref="n<ref>"`：认领前按完整 `tap` 规划
校验该按钮（写前显式灰置也接受），写后重验其启用状态与安全落点，再至多点击一次；单次调用
至多一种提交原语，节点提交失败绝不补发 ENTER。

复合 text 的 `text_input` 是 `{write,submit}` 快照，在首次结果、运行中 `status(P)`、重复 P
与冻结结果中保持同一份；合法组合只有 `(not_executed,not_executed)`、
`(uncertain,not_executed)`、`(executed,not_executed)`、`(executed,uncertain)` 与
`(executed,executed)`。写入确认只证明写入原语完成，提交原语只证明对应按键或点击已派发；
两者都不证明搜索、提交、导航或业务结果。

text 失败（含 `outcome=partial` 与 `outcome=uncertain`）的 `error.details` 固定给出
`stage`（`target`/`focus`/`write`/`verify`/`submit`）、`reason` 与
`dispatch.focus/write/submit`（`not_attempted|not_dispatched|dispatched|unknown`），
可选 `candidate_count` 保留 `0`；MCP 的 structured/text/both 三种模式都保留同一组嵌套字段
（文本写作 `error.details=<紧凑 JSON>`），调用方按枚举决策而不解析 `message`。
`submit_ref` 自身不存在返回 `409 node_not_found`；结构、能力或无安全落点的静态拒绝沿用 5 个
`422` 码与 `candidates`，`details.scope_ref` 指向 `submit_ref` 或其祖先，失败不认领 O。

`write=executed` 且提交确定未派发且无未知物理副作用时整体 `outcome=partial`，顶层错误码
`text_submit_not_executed`（HTTP `409`；期间 STOP 撤权为 `control_revoked`）。原会话仍活动且
现场观察发布成功时，首次结果当场附带新的 `ready O` 并省略 `recovery`；重复 P、`status(P)`、
冻结与现场观察失败在会话仍活动且无实时阻塞时输出 `recovery entry=observe ref=<P>`，撤权或
实时阻塞按 3.2 的优先级覆盖。已认领 `text` 的整体 `outcome=uncertain`（含仅 `focus`
已派发而 `write`/`submit` 尚未派发）且给出 `recovery entry=observe ref=<P>` 时，直接
`observe` 核对当前框值/页面；`partial` 且结果附带有效新 O 时直接用它继续，没有有效 O 时按
同一 `recovery` 观察。这个特定恢复覆盖通用的 `uncertain` 先 `status` 规则。当 `ref=` 是
抽象词 `P` 而不是具体编号时，使用原活动会话的具体归属 P（如 `session.origin` 或已知属于该
会话的 `operation_id`），不能把枚举值 P 当作编号。实时会话/耐久/设备/忙阻塞才覆盖为
`end`/`status`/`user` 并给 `blocking_code`。字段与合法组合见 [接口契约](interface.md) §6。

公共树语法与调用规则由 server instructions 发布：`server/discover` 与兼容 `initialize`
返回同一份说明，包含一份完整树图例，以及会话推进、O/P、`reference`、租期和跨工具恢复。
工具描述给出用途、关键参数分流、动作边界及独立理解该工具所需的恢复提示。
`start` 描述简述 O/P 和 full/delta。每条 delta 都单独应用到 `base` 指向的完整树；`base` 可
指向服务端基线池中的当前基线（跨度 1–12）或备用 full（跨度 1–10），而不是只能指向上一帧。
图例以 `tree v5`、`inspect v3` 标识普通树与详细补看的独立语法，并说明普通节点在
`n<ref>` 后按首列恢复角色：
不带引号的合法角色词是显式角色，JSON 字符串字面量默认为 `text`，几何、`value=`、`hint=`、
`tap`/`long_press`/`swipe`、状态或属性标记或行尾默认为 `view`；只有恢复结果等于原角色且
首项不会被误读为角色词时才省略。当前 compact full 和 delta 输出普通节点定义。读取历史
`[n2 n3]` 或 `chain [n2 n3] <role> ...` 时，按顺序展开，缩进孩子归最后一个 ref。
图例还说明 `obs` 行的可选 `visual`/`change`/`hint` 列、节点标志列（含 compact 截断时的
`truncated`）、块头后的空行、几何省略、状态默认与 compact 净体；
几何列在 `root`/`scroll` 上是四元矩形，其他角色是二元中心点。`hint=snap` 时用同一 O 调
`snap` 核对画面；`hint=wait_for` 时用 `wait_for` 等待条件或稳定；`change=matched_base` 表示
前后树内容或公开语境有变、只是差分零差异命中历史基线，不是动作没生效。三者都只是观察事实或
只读建议，不是输入授权，也不替代页面事实。
普通 `tree v5` 完整头只写
`nodes`，差分头只写 `base` 与 `changes`；块头 `view=` 以及 `nodes`/`entries` 两项计数
属于 `inspect v3` 补看头。动作列的 `swipe` 表示节点的源能力，受控滚动使用
`operation.type=scroll` 和 `container`。嵌套容器可按要滚动的区域选择 ref。

调用参数在进入客户端前由 `internal/protocol` 的严格解码器接入：未知成员、重复键、大小写
变体、尾随第二个 JSON 值、类型混用与未声明 `null` 都被拒绝，且不产生设备调用。参数错误
返回 `IsError=true` 与 `invalid_request`，按所选结果模式投影。

`keepalive` 的 schema 声明可选的整数 `duration_minutes`（1～120，省略为 2），适配器按参数
是否存在选择 SDK 的默认调用或含时长调用，完整透传到服务；不会在本地补默认值、丢弃显式
时长或改写成单调续租。server instructions 与参数说明明确：显式续期覆盖原截止时间且可
缩短，省略也重设为两分钟；普通活动在续期点取 `max(现有截止时间, now + 2 分钟)`，不继承
本次时长；start 使用五分钟租期，会话没有累计时长硬上限。调用取消仍按第 6 节撤销授权，
长租约不覆盖取消边界。

`tools/list` 为 `status`、`inspect`、`diagnose` 和 `snap` 发布
`annotations.readOnlyHint=true`。前三项查询无本次 P，`snap` 仍预分配 P 并占用工作位。
注解表达操作意图，实时查询的结果仍取自调用时的状态。

`snap` 的 schema 只有一个必填 `reference`（`O…`/`P…`，≤64）。它与 `observe` 共用
引用解析口径，额外拒绝本次请求 P 的自引用。它只读取一张图，不读取树或发布观察；`obs` 行
`hint=snap` 指的就是用同一 O 这样只读核对画面（不消费 O、不发布新 O、不续租）。
成功时结果是顶层 `image` 元数据加一个 `ImageContent`；`result v4` 以单行
`image <W>x<H> read_only screen=<W>x<H>[@<display_id>] [at=<HH:MM:SS>]` 输出同一元数据（不含 base64），
`structuredContent` 在顶层 `image` 携带同组成员，三种结果模式都只发送一个 `ImageContent`。
`isError=false` 要求完整图片实际交付；
重复 P 的无图摘要、失败、准入或传输错误均为 `isError=true`。取消见第 6 节。

`observe.wait_ms` 是单次采集前的延迟毫秒数；等待公开树条件使用 `wait_for`。`wait_for` 的
schema 以 `reference` 与 `timeout` 为必填，`query`、`absent_query`、`stable`、`full`、`image`
可选。`timeout` 是 1–40 的整数，`stable=true` 时至少 2。两个等待条件在 schema 中均为
1–256 个码点，运行时也拒绝全空白。同时提供时任一条件命中即可满足查询条件。
`stable=true` 时还须满足稳定判据。成功时用新 `obs` 行的完整 `O` 继续（`structured` 中为
`observation.id`）。超时若发布新 O，`isError=false`，`wait.matched` 含 `timeout` 及终帧
命中的条件（文本写作 `matched=`）。命中条件或满足稳定状态时复用候选帧。到时或采样上限
退出时，若最新成功轮询帧仍在 `CaptureMaxAge=15s` 内且之后没有失败尝试，则复用该帧。否则
做一次终帧采集。`stable=true` 要求连续 3 帧内容摘要一致，忽略节点的 `bounds`、`ref` 与
无动作叶子的文本。两个条件仍匹配当前完整公开可见树。`wait.elapsed_ms` 计自准入完成至终帧
发布（文本写作 `elapsed=<ms>ms`），验证采集整体受 35 秒固定预留约束，实际耗时可超过
`timeout` 至多约 35 秒。参数在适配器侧按同一严格解码器接入，
解码器可判定的非法组合在发出 HTTP 请求前被拒；两个条件折叠后相同由引擎准入在请求发出后
拒绝，不接触设备。

`app` 的根 schema 是一个扁平、封闭对象：根声明
`properties={kind,query,reference,scope,app,target,restore_foreground,allow_borrowed,
allow_stop_playback}`、`required=["kind"]` 与 `additionalProperties=false`；`target` 也是
扁平对象 `{task_id,app}`、`additionalProperties=false`。根内不使用 `allOf`、`if`/`then`/
`else`、`not` 或 `oneOf`。根字段可见不代表每个 kind 都接受；服务端 `DecodeApp` 的封闭
kind 成员矩阵是互斥权威：

| kind | 必填（除 kind） | 可选与默认 |
| --- | --- | --- |
| `find` | 无 | `query`（≤256 码点） |
| `list` | `reference`：ready O 或活动原会话 P | `scope=session`（默认）或 `primary`、`app`（包名过滤） |
| `open` | `reference`（当前 ready O）、`target={app}` | 无 |
| `claim` | `reference`（当前 ready O）、`target` | 无 |
| `handoff` | `reference`（当前 ready O）、`target` | `restore_foreground=false` |
| `close` | `reference`（当前 ready O）、`target` | `allow_borrowed=false` |
| `kill` | `reference`（当前 ready O）、`target={task_id}` | `allow_stop_playback=false` |

`target` 为 `{"task_id": 正整数}` 或 `{"app": 包名}`，恰好一种；`open` 只接受 `target.app`，
`kill` 只接受 `target.task_id`，`claim`/`handoff`/`close` 接受两者之一。顶层 `app` 只保留给
`list` 过滤。`find`/`list` 是查询，成功与失败均无 `outcome`；结果各按 32 KiB 完整条目前缀
返回，`total`/`truncated` 说明预算前数量与截断。其余五个 kind 认领时消费当前 ready O，
已执行后尝试发布原会话屏的 full 无图观察。查询与变更的 P 绑定、IsError 与取消差异见第 3、6 节。

`shell` 的 `status(P)` 可以在终止未确认时返回 `operation_state=finished` 与
`outcome=uncertain`。受管命令的有限屏障仍可能让新的普通工作返回 `device_busy`。
`status({})` 可报告 `blocking_code=shell_pending`。遇到该阻塞可短暂退避，原会话仍活动时
再观察现状。屏障期限见 [运维说明](runbook.md#7-常见故障)。
受管命令不绑定会话显示屏：`input` 一类命令默认作用于 display 0（主屏），要驱动会话屏须
自带 `input -d <display_id>`。该 `display_id` 取自 `start` 首帧 `obs` 行的
`screen=<W>x<H>@<display_id>`；`snap` 的 `image` 与 `app(list)` 的 `display=` 给出同一
编号。

## 3. 结果投影

结果模式只选择表示形式，三种模式承载同一份事实与相同图片：

| 模式 | `content` | `structuredContent` |
| --- | --- | --- |
| `both` | 紧凑 `result v4` 文本与图片 | 完整 `Result` JSON |
| `text`（默认） | 紧凑 `result v4` 文本与图片 | 不发送 |
| `structured` | 图片；`isError=true` 时附最小 `diagnostic v1` 文本 | 完整 `Result` JSON |

文本与结构化两种表示正式分层：`text`/`both` 的 `result v4` 是按 3.1 的投影顺序与省略清单
呈现的紧凑投影，面向调用方阅读与上下文效率，不承诺与结构化 JSON 逐字段互相还原；
`/api/*` 与 `both`/`structured` 的 `structuredContent` 完整无损保留 `Result` JSON，含全部
审计字段、三态与空集合语义。完整时间戳、`observation.origin`、`observation.revision`、
`observation.actionable_until`、`observation.quality`、`effect`、`session.agent` 与
`inspection.origin` 只在 `structured`/`both` 的 JSON 中保留。

完整结果是 `(canonical A, CallToolResult.IsError, ImageContent)`。`A` 是去除图片 base64
载体后的确定性 `Result` JSON；取消时还包含本节规定的 `error.details.mcp.*`。`result v4`
与 `structuredContent` 都取自同一份 `A`：结构化编码保留每个成员与三态，文本投影只按 3.1 的
显式清单省略固定或冗余元信息，其余成员一律显式投影或按 `json v1` 兜底。图片只在
`ImageContent` 中发送。`structured` 成功且无图时 `content=[]`；不接受空 content 的宿主
应使用 `both` 或 `text`。模式不用于脱敏或截断，不发布 `outputSchema`。

`effect` 与 `observation.quality` 取自同一份 `Result`，适配器不重算或补投影这些事实：
`effect` 只由 `scroll` 的操作结果与保留的 P 视图携带，带树的观察都携带
`observation.quality`（未采样或重验交付为 `unknown`/`0`/`not_sampled`），只有没有树的失败
观察不携带它。`structured`/`both` 的 `structuredContent` 携带完整对象；`text` 与
`both` 的 `result v4` 文本省略两个整对象。成员与取值见 [接口契约](interface.md) §11。

复合 `text` 的 `text_input`、`wait_for` 的 `wait`、`snap` 的顶层 `image` 与 `app` 结果同样
取自这一份 `Result`；文本行与块语法见 3.2。显式 `auto_submit=false` 和非法组合不会产生
`text_input` 对象；`outcome=partial` 的 `text_input` 固定为
`write=executed`、`submit=not_executed`。首次结果、运行中 `status(P)`、重复 P 与冻结结果使用
同一快照。
`wait.matched` 为空时服务端已省略该成员，适配器不补 `null`、不补空数组；验证采集失败时仍
保留 `wait.trigger` 与 `wait.elapsed_ms`，不输出 `wait.matched`。失败、重复 P 与
`status(P)` 摘要没有顶层 `image`，也不会被适配器补成空对象。

`structured` 的错误文本使用 `diagnostic v1 is_error=true` / `end diagnostic`，只包含
操作号、操作状态、outcome、phase、目标引用、准入与消息状态、`effect.state`、
`text_input.write`/`text_input.submit`、观察 status/id、会话、收尾、条件等待的
`wait.trigger` 与 `wait.elapsed_ms`、错误与恢复等存在的解释字段；它不代表完整 `A`。
所有字段均从同一份 `A` 生成。`app` 只在 diagnostic 中补充 `app.kind`，不展开 `items`、
`tasks`、PID 或其他业务事实；`wait.matched` 不进入 diagnostic：失败结果没有可判定那一帧
的已发布观察。

树、图片与 shell 输出的事实来源都是服务的同一份结果。base64 只出现在 HTTP 载体
`observation.image.base64` 与 MCP `ImageContent.data`；可读正文与 `structuredContent`
都不包含图片字节，也不把 base64 混进树正文。服务未返回图片时，结果不虚构图片元数据。

`isError` 只表达“本次工具调用是否达成请求”，不改写已确认事实：

| 事实 | isError |
| --- | --- |
| 主动作 `executed`，仅后树/图片/清理独立失败（`act`/`app` 的后树；含 shell 自然非零） | `false` |
| 主动作 `not_executed` / `uncertain` / `partial` | `true` |
| 纯 `observe` 有合法核心 O，仅缺图 | `false` |
| 纯 `observe` 没有合法核心 O（含只有只读图） | `true` |
| `wait_for` 发布出合法核心 O（含到期未命中） | `false`（按 `observe` 同口径） |
| `wait_for` 没有发布新 O（含验证采集失败、只有只读图、准入或传输错误） | `true` |
| `snap` 完整交付顶层图片与一个 `ImageContent` | `false`；失败、重复 P 无图摘要与取消为 `true` |
| `status` 正常给出 `request_closed` 或其他查询事实 | `false` |
| `status`/`end` 返回 `operation_unknown` | `true`；`end` 可同时封闭该 P 的未来接纳 |
| `diagnose` 证据不足或覆盖范围有限 | `false` |
| `inspect` 内容不可用、准入/协议/传输错误 | `true` |
| `start` 已建立会话但首个观察失败 | `false`（主操作 `executed`） |
| `act` 已派发但完成未知 | `true`（`uncertain`） |
| `act(text)` 写入已确认、提交确定未派发 | `true`（`partial`）；固定带 `text_input`；正常提交未派发错误为 `text_submit_not_executed`，撤权为 `control_revoked`；交付成功时另带现场新 O |
| `app` 的 `find`/`list`：首次完整查询或已明确标记的无 error 操作摘要 | `false`；读取、接入或传输错误为 `true`，查询取消调用本身为 `true` |
| `app` 的五个变更 kind：主 outcome `executed` | `false`；`not_executed`/`uncertain` 为 `true` |
| `app.close` 仅 `complete=false`（recents 未确认）、`app.handoff` 仅前台恢复未确认、主动作后树失败 | 不改写主 outcome 与 isError |
| MCP 调用取消（含已确认 `executed` 的结果） | `true`，保留原执行事实 |

`status(P)` 的 `query_cancelled` 摘要在没有 `error` 时 `IsError=false`，只表示摘要已提供，
不代表原查询成功或包含集合；查询调用本身的取消仍按上一行记为 `true`。

### 3.1 `result v4` 外壳、投影顺序与省略清单

`text`/`both` 的文本以头行开始、以 `end result` 结束，行尾统一 `\n`，不写裸 `\r` 与多余
空行：

```text
result v4 <ok|err> [op=<P>] [state=<operation_state>] [outcome=<outcome>] [phase=<phase>]
...
end result
```

- `<ok|err>`：`isError=false` 写 `ok`，`isError=true` 写 `err`。
- `op=<P>`：`operation_id` 非空时一律输出，不省略。
- `state=<operation_state>`：非空时输出，例如 `running`、`finished`。
- `outcome=<outcome>`：仅当结果为 `ok`、值为 `executed` 且头行没有 `state=` 时省略；
  错误结果、`not_executed`、`uncertain`、`partial` 与带 `state=` 的 `status(P)`/幂等重放摘要
  一律显式。
- `phase=<phase>`：非空时输出。
- 头行取值不是合法 token 时，改在头行之后按实际成员名兜底，例如
  `json v1 path="/operation_state" value=…`。
- `op=` 只是本次调用编号参考、`at=` 只是 UTC 秒级时序提示；两者都不替代或放宽
  `internal/engine` 对会话归属、显示代次与期限的权威校验。

可见标识形如 `O4821_85` 与 `P7391_12`：`O`/`P` 后接 4 位十进制实例标签（`1000`–`9999`，
无前导零）、`_` 与单调序号。`/api/*`、`structuredContent` 与文本使用同一编号，适配器不维护
别名或第二套映射。

成员按下列固定顺序投影，各自仅在该成员存在时出现，字段行与块按此顺序交错：

1. 动作与状态控制标量：`text_input write=<write> submit=<submit>`、
   `target_reference=<ref>`、`request_closed=<true|false>`（永不省略）、
   `blocking_operation_id=<P>`、`can_start=<true|false>`、`blocking_code=<code>`、
   `start_operation_id=<P>`、`observation_state=<state>`、`input_state=<state>`。
2. `notify` 聚合行及其错误子行。
3. 顶层 `image` 行及其错误子行。
4. `session` 行。
5. `obs` 行、其 `obs.image`/`obs.error` 子行与普通树块。
6. `inspection` 补看块。
7. `app` 行与 `app_items v1`、`app_tasks v1` 块。
8. `shell v1` 块。
9. `wait` 行。
10. `diagnosis` 行、`diagnosis.issues`/`diagnosis.timing_ms` 行与 `timeline v1` 块。
11. `cleanup` 行及其错误子行。
12. `error` 行。
13. `recovery` 行。
14. 其余未登记成员逐个按 `json v1` 兜底。

块之后仍可有后续行，例如 `shell` 块之后仍有 `wait`、`cleanup`、`error` 与 `recovery` 行。

显式省略清单如下；被省略的值仍完整保留在 `/api/*` 与 `structuredContent`：

| `Result` 路径 | 文本处理 | 条件与理由 |
| --- | --- | --- |
| `outcome` | 省略 | 仅 `ok`、`executed` 且头行无 `state=`；头行 `ok` 已唯一确定常规执行成功 |
| `observation.status` | 省略 | 仅 `complete`；`partial`、`failed` 与其他值一律显式 |
| `observation.origin` | 始终省略 | 同会话恒等于 `start` 时的 `session.origin`，避免每帧重复 |
| `observation.revision` | 始终省略 | 内部递增版本号，操作与恢复都不依赖 |
| `observation.actionable_until` | 始终省略 | 由本次采集的最早读取时刻加 60 秒输入期限推导，并受会话租期截断；截止由服务端精确裁定 |
| `observation.quality` | 始终省略整对象 | 完整对象由结构化结果提供 |
| `effect` | 始终省略整对象 | 完整对象由结构化结果提供 |
| `observation.screen` | 条件省略 | 仅当顶层 `session` 为空或缺席且观察带树；`screen` 存在且 `rotation != 0` 时 `rot=<1\|2\|3>` 恒输出 |
| `image.media_type`、`observation.image.media_type` | 省略 | 仅 `image/jpeg`；非 JPEG 写 `media=<type>` |
| `session.agent` | 始终省略 | 单一固定 Agent 名称，无决策价值 |
| `inspection.origin` | 始终省略 | 补看头已带 `observation=<O>` |
| 所有时间戳的日期与纳秒部分 | 截断为 UTC `HH:MM:SS` | 秒级时序足够；非合法 RFC3339 值原样保留，非法 token 走 `json v1` |

除上表列出的成员外，`Result` 的其余成员都在 `result v4` 中显式投影，不静默丢弃：
`request_closed` 永不省略；`target_reference`、`blocking_operation_id`、`can_start`、
`blocking_code`、`start_operation_id`、`observation_state`、`input_state`、
`text_input`、`wait`、`cleanup`、`error`、`recovery` 与 `app` 的全部三态、缺席和空集合语义
100% 保留。

词法与转义规则：

- 不带引号的 token（`<code>`、`<state>`、`<pkg>`、`<O>`、`<P>` 等）须非空且不含
  `<= U+0020` 的字符、`=`、`"`、`\`、`U+007F` 或非 ASCII 字符；不合法时该成员回退
  `json v1 path="…" value=…`。
- 双引号文本负载（`error` 与各 `*.error` 的 `<message>`、`diagnosis` 的 `summary`、
  `app_items` 的 `<name>`）使用单行 JSON 字符串：严格转义 `\n`、`\r`、`"`、`\` 与控制字符，
  允许空串 `""`，不拆行、不伪造成其他键。
- 除 `session=null` 与 `state_preserved`/`restarted`/`recents_cleaned` 的可空三态外，意外
  `null`、类型不匹配或未登记成员都回退到 `json v1`，不静默丢弃。
- 无法无损编码或无法安全词法化的值使用
  `json v1 path="<绝对 RFC 6901 JSON Pointer>" value=<紧凑 JSON>`；`~`、`/` 分别转义为
  `~0`、`~1`，每个事实路径只编码一次，例如：

  ```text
  json v1 path="/observation/tree" value={"format":"full","scope":"","root":null}
  ```

- 块前恰好一个空行；块后接行时没有额外空行，相邻块之间恰好一个空行。
- 块头的 `nodes`、`changes`、`events`、`output_bytes` 仅为派生计数。普通 full 恢复时使用
  固定 `scope=reported_visible_with_context`，该字段不写入 `tree v5` 头；delta 不携带也不
  恢复 `scope`。`inspect` 的 subtree/query 头中 `tree_scope` 仍是派生显示，不新增 `A` 的
  成员。

`result v4` 的文法稳定；普通树与详细补看正文分别由块头 `tree v5`、`inspect v3`
标识独立语法。三层版本按块头分派，`structuredContent` 保持同一份事实集，未知成员一律走
绝对 JSON Pointer 兜底。取消统一标为 `isError=true`。工具名称、描述、入参 schema 与目录
指纹随发布清单核验，描述变更会改变目录指纹，部署必须按清单重新核验。

### 3.2 对象行

**复合 `text` 的 `text_input` 与 `partial`。** 有效 `auto_submit=true` 的复合 `text` 结果在
控制标量行输出 `text_input write=<write> submit=<submit>`；两个值都属于
`not_executed|uncertain|executed`，且 `submit` 非 `not_executed` 时 `write` 必为
`executed`。`write=executed`、`submit=not_executed` 且提交确定未派发时整体
`outcome=partial`、`isError=true`，错误行通常为
`error text_submit_not_executed "文字已写入，但提交未执行"`（期间 STOP 撤权时按
`control_revoked` 投影），错误详情给出 `stage=submit`、封闭 `reason` 与三键 `dispatch`。
原会话仍活动且现场观察交付成功时，首次结果附带新的 `ready O` 并省略 `recovery`；重复 P、
`status(P)`、冻结或现场观察失败在会话仍活动且无实时阻塞时输出
`recovery entry=observe ref=<P>`，撤权或实时阻塞按本节优先级覆盖。例如：

```text
result v4 err op=P7391_14 outcome=partial
text_input write=executed submit=not_executed
obs O4821_77 fg=com.android.settings at=22:05:13

tree v5 full nodes=3

n1 root 0,0,1080,2400
 n2 input 540,120 "搜索" value="周杰伦" hint="输入关键词"
 n3 button 990,120 "搜索"
end tree
error text_submit_not_executed "文字已写入，但提交未执行"
error.details={"stage":"submit","reason":"submit_target_unconfirmed","dispatch":{"focus":"not_attempted","write":"dispatched","submit":"not_attempted"}}
end result
```

**`notify`。** `message_id`、`saved` 与 `notification.state` 至少存在其一时合并为一行，
`notification.error` 存在时紧跟错误子行：

```text
notify [id=<message_id>] [saved=<true|false>] [state=<notification.state>]
notify.error <code> "<message>"
notify.error.details=<紧凑 JSON>
```

`notification` 为 `null` 或空对象时按 `json v1 path="/notification"` 兜底；其余成员分别按
`/message_id`、`/saved`、`/notification/state`、`/notification/<name>` 兜底，
`notification.error` 不可表示时按 `/notification/error` 兜底。

**顶层 `image` 与 `obs.image`。** 两者共用同一渲染器，只在前缀不同；各成员按存在性输出：

```text
image [<W>x<H>] [read_only] [media=<非 jpeg>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [at=HH:MM:SS]
image.error <code> "<message>"
image.error.details=<紧凑 JSON>
```

`display_id` 缺失时不写 `@<display_id>`，绝不把缺失当 `0`；`read_only=false` 不输出标记；
`media_type` 为 `image/jpeg` 时不输出 `media=`；`base64` 从不进入文本。`width`/`height`
是输出 JPEG 的像素尺寸：图片等比缩小且永不放大，长边不超过 `1024`（`1080×2400` 输出
`461×1024`）。宽高不是整数、只给出一侧非零值、`read_only`/`media_type` 类型不符、
`rotation` 超出 `0..3`、或时间值不可表示时，整对象按 `json v1 path="/image"`（`obs` 内为
`/observation/image`）兜底；未知成员按 `/image/<name>` 或 `/observation/image/<name>`
兜底。`obs` 内的图片写 `obs.image …` 与 `obs.image.error …`。

**`session`。** 显式 `null` 写 `session=null`；非空对象写：

```text
session <state> mode=<mode> origin=<origin> [started=HH:MM:SS] [expires=HH:MM:SS] [ended=HH:MM:SS] [reason=<reason>]
```

`agent` 始终省略，空字符串时间与空 `reason` 不输出。`state`、`mode`、`origin` 不是合法
token，或时间值既非合法 RFC3339 也不是安全 token 时，整对象按 `json v1 path="/session"`
兜底；未知成员按 `/session/<name>` 兜底。

**观察行。** 单行摘要，`obs ` 之后首个不含 `=` 的字段是完整 `O`：

```text
obs [<O>] [status=<partial|failed|…>] [fg=<前景>] [at=HH:MM:SS] [visual=<loading|image_heavy(<pct>%)|media_surface|sparse_tree>] [change=<unchanged|detail_only|matched_base>] [hint=<snap|wait_for>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=<a,b>]
```

- `status=complete` 通常省略；整行没有其他字段时写 `obs status=complete`，不产出裸 `obs`
  行。`partial`、`failed` 与其他值一律显式输出；采集失败且没有观察编号时仍有
  `obs status=failed missing=tree` 之类的摘要。
- `fg` 按 `foreground.state` 编码：`package` 与 `task_id` 都有写 `<package>#<task_id>`，
  只有 `package` 写 `<package>`，只有 `task_id` 写 `#<task_id>`，都没有写 `present`；
  `none` 写 `none`；`unknown` 写 `unknown(<reason>)`，`reason` 为空时写 `unknown`。
- `visual` 仅在观察携带该字段时输出，取值为 `loading`、`image_heavy(<pct>%)`（45–100）、
  `media_surface` 或 `sparse_tree`，判据与 `structuredContent` 完全一致（见
  [接口契约](interface.md#observation-与-quality)）。`full` 观察输出本次分类；`delta` 中
  `loading` 不省略，其余三类仅在同会话、同显示代次的分类变化时输出，同态省略。适配器不
  重算、不补默认值。
- `change` 映射 `observation.tree_change`，只在 `act` 常规成功（比较动作前后树内容与公开语境）
  时出现：`unchanged` 前后紧凑树与完整树均相同，`detail_only` 紧凑树相同但完整树细节有变，
  `matched_base` 前后树内容或公开语境有变、差分零差异命中历史基线，不是动作没生效。常规
  内容变化（发布非空差分或 full）时省略该列；`change=unchanged` 不要求本次差分为空，可与
  非空历史基线差分同时出现。`start`、`observe`、`wait_for`、`app`、`act.delivery`、`text`
  的 `partial` 交付与无树观察省略该列。
- `hint` 映射 `observation.hint`，由已发布的 `visual`、`change` 与是否交付有效图片推导：
  `snap` 表示用同一 O 只读截图核对（不消费 O、不发布新 O、不续租）；`wait_for` 表示页面
  可能仍在加载或过渡，应等待条件或稳定；`matched_base` 且无 `visual` 时不提示。
- 例如 `obs O4821_80 fg=com.example.gallery at=22:05:18 visual=image_heavy(52%) hint=snap`。
- `screen` 仅在顶层 `session` 非空或观察没有树时输出；`screen` 存在时，无论 `screen=` 是否
  省略，`rotation != 0` 都一律输出 `rot=<rotation>`。
- `missing` 以逗号连接，空列表省略。
- 子行仅在对应字段存在时紧跟：`obs.image …`（与顶层 `image` 同字段同顺序）、
  `obs.image.error …`、`obs.error <code> "<message>"`，各自的详情写作
  `<前缀>.details=<紧凑 JSON>`。
- 成员无法表示为合法 token，或观察为 `null`、无法读取时，按 `/observation/<成员>` 或
  `json v1 path="/observation"` 兜底。树仅在所属完整 O 可读且类型化往返无损时进入可读
  块，否则写 `json v1 path="/observation/tree" value=…`。

`start` 首帧、`observe`/`wait_for`/`act`/变更类 `app` 返回的新观察，其完整 O 都取 `obs`
行；看到 `base=_72` 只用于定位读树基线，需要调用时用完整 O/P 或当前 ready O；`inspect`
始终使用完整历史 O 与实际节点 ref。

**`inspection`。** 不写前置 `inspection.origin=` 行；空一行后沿用 3.3 的
`inspect v3 … end inspect`。补看块无法无损编码时整值按 `json v1 path="/inspection"`
兜底。

**`app`。** 单行头按固定字段顺序输出（`kind` 必填）：

```text
app <kind> [pkg=<package>] [task=<task_id>] [src_task=<source_task_id>] [src_display=<source_display_id>] [display=<display_id>] [ownership=<ownership>] [reused=<true|false>] [state_preserved=<true|false|null>] [fg_restore=<foreground_restore>] [complete=<true|false>] [target_pids=<数组>] [killed_pids=<数组>] [remaining_pids=<数组>] [restarted=<true|false|null>] [total=<n>] [truncated=<true|false>] [fg=<前景>] [at=HH:MM:SS]
```

- `state_preserved`、`restarted` 的显式 `null` 原样写 `null`，与缺席严格区分。
- `target_pids`、`killed_pids`、`remaining_pids` 存在时写紧凑整数数组，严格区分缺席与 `[]`。
- 空字符串 token 成员不输出；数字成员显式 `null`、类型不匹配或其他不可表示情况使整对象按
  `json v1 path="/app"` 兜底；未知成员按 `/app/<name>` 兜底。
- `items` 存在时输出 `app_items v1` 块，空数组同样输出 `count=0`，与“未获得集合”区分：

  ```text
  app_items v1 kind=find count=<n>
  <package> "<name>"
  end app_items
  ```

  ```text
  app_items v1 kind=list count=<n>
  #<task_id> <package> <ownership>[ origin_display=<显示号>]
  end app_items
  ```

- `tasks` 存在时输出 `app_tasks v1` 块，空数组同样输出 `count=0`：

  ```text
  app_tasks v1 count=<n>
  #<task_id> <outcome>[ recents_cleaned=<true|false|null>][ error=<code> "<message>"[ details=<紧凑 JSON>]]
  end app_tasks
  ```

- 块内单个条目无法表示时该条目整值按 `json v1 path="/app/items/<i>"`（或
  `"/app/tasks/<i>"`）兜底；条目内的未知成员按 `/app/items/<i>/<name>`、
  `/app/tasks/<i>/<name>` 兜底。`items`/`tasks` 不是数组时按 `/app/items`、`/app/tasks`
  兜底。

适配器严格解码成功并预分配 P、但服务结果未知时，只能从已接纳的请求补 `app.kind` 作为调用
元数据；不得补 `task_id`、`display_id`、PID、`ownership` 或其他设备事实，也不得声称服务已
登记或已 finished。适配器自身的参数接入拒绝不分配 P，也不补 `app`。

**`wait`。** 条件等待的终态写在单行：

```text
wait trigger=<trigger> [matched=<a,b>] elapsed=<elapsed_ms>ms
```

`matched` 为空时省略；`trigger` 不是合法 token、`elapsed_ms` 不是整数、`matched` 不可表示
或出现未知成员时，整值按 `json v1 path="/wait"` 兜底。验证采集失败时 `matched` 不出现。

**`diagnosis`。** 首行后按存在性输出两个 JSON 行，`timeline` 非空时以空行接
`timeline v1 … end timeline`：

```text
diagnosis summary="<summary>" coverage=<coverage> truncated=<true|false> [op=<operation_id>] [outcome=<outcome>]
diagnosis.issues=<紧凑 JSON>
diagnosis.timing_ms=<紧凑 JSON>
```

`summary` 使用双引号 JSON 字符串；`issues`/`timing_ms` 为 `null` 时按
`/diagnosis/issues`、`/diagnosis/timing_ms` 兜底；`summary`、`coverage`、`truncated` 或
可选 `op`/`outcome` 不可表示时整值按 `json v1 path="/diagnosis"` 兜底；未知成员按
`/diagnosis/<name>` 兜底。`timeline` 为空数组时不输出块，其内容仍是 `structuredContent`
的事实；`timeline` 为 `null` 或不可表示时按 `/diagnosis/timeline` 兜底。

**`cleanup`。** 单行报告与可选错误子行：

```text
cleanup <state> [exec=<execution>] [tasks=<tasks>] [recents=<recents>] [display=<display>]
cleanup.error <code> "<message>"
cleanup.error.details=<紧凑 JSON>
```

成员无法表示为合法 token 时整值按 `json v1 path="/cleanup"` 兜底，`cleanup.error` 不可
表示时按 `/cleanup/error` 兜底；未知成员按 `/cleanup/<name>` 兜底。

**`error`。** 顶层错误行与可选详情行：

```text
error <code> "<message>"
error.details=<紧凑 JSON>
```

`code`/`message` 不可表示时整值按 `json v1 path="/error"` 兜底，`details` 为 `null` 时按
`/error/details` 兜底；未知成员按 `/error/<name>` 兜底。

**`recovery`。** 单行给出后续应调用的入口与目标引用：

```text
recovery entry=<entry> ref=<精确 O/P|O|P|none> [input=<input_state>] [cause=<cause>]
```

`ref=` 在单次调用内按本次请求引用与本次结果事实确定性解析；无法证明安全时严格回退为抽象词
`O`、`P` 或 `none`。解析只发生在文本投影层：`structured`/`both` 的 JSON
`recovery.reference` 仍严格是 `"O"`、`"P"` 或 `"none"`，适配器不保存跨调用状态。错误码
优先取顶层 `error.code`，不可得时取 `observation.error.code`（`observe`/`wait_for`
采集失败路径）。合法 `O`/`P` 指以对应字母开头、长度不少于 2 且满足 token 规则的值。

1. `reference=none`：恒为 `none`。
2. `entry=retry` 且 `reference=O`：本次观察的合法 `O`、否则本次请求引用（合法 `O`），
   否则抽象词 `O`。
3. `entry=status`/`end` 且 `reference=O`：`target_reference`（合法 `O`）、否则请求引用、
   否则抽象词 `O`。
4. `entry=status` 且 `reference=P`：按下列顺序取首个成立者。
   1. 错误码 `device_busy`：`blocking_operation_id` 是合法 `P` 时用它，否则回退 `P`（红线 1）。
   2. 错误码属于 `reference_has_no_session`、`operation_unknown`、`operation_not_found`，
      或错误码为 `request_closed` 而结果没有可解析的 `request_closed` 布尔值：
      `target_reference`（合法 `P`）、否则请求引用、否则回退 `P`。
   3. 本次 `operation_id` 是合法 `P` 且满足任一条件：`outcome=uncertain`、
      `outcome=partial`、`operation_state=running`、`phase=executed_revoked`、
      `request_closed=true`、`cause` 属于 `running`/`executed_revoked`/`cleanup_pending`、
      错误码属于 `call_cancelled`/`transport_unavailable`/`invalid_response`/
      `task_lifecycle_busy`/`text_submit_not_executed`、
      `cleanup.state` 为 `pending` 或 `unconfirmed`。
   4. `start_operation_id`（合法 `P`）。
   5. `session.origin`（合法 `P`）。
   6. `target_reference`（合法 `P`）。
   7. 否则回退 `P`（红线 4）。
5. `entry=observe` 且 `reference=P`：
   1. `outcome=partial` 或错误码为 `text_submit_not_executed`，且本次 `operation_id` 是
      合法 `P`：用它；该语义入口优先于 `session.origin`。
   2. `session.origin`（合法 `P`）。
   3. 本次请求引用是合法 `P` 且错误码不在 `operation_unknown`、`operation_not_found`、
      `request_closed`、`reference_has_no_session` 之内：用它。
   4. `operation_id` 是合法 `P` 且满足任一条件：`cause` 或 `phase` 为
      `post_observation_failed`、`outcome=uncertain` 或 `partial`、错误码属于
      `observation_expired`/`observation_claimed`/`observation_retired`/
      `hierarchy_unavailable`/`stale_screen`/`tree_too_large`/
      `control_ui_on_virtual_display`/`display_unavailable`/`text_submit_not_executed`。
   5. 否则回退 `P`（红线 2）。
6. `entry=end` 且 `reference=P`：
   1. `error.details.start_operation_id`（合法 `P`）。
   2. `session.origin`（合法 `P`）。
   3. `start_operation_id`（合法 `P`）。
   4. 本次请求引用（合法 `P`）。
   5. 否则回退 `P`（红线 3）。
7. 其他 entry/reference 组合：`ref=` 保持服务给出的抽象词。

四条安全红线即上述回退边界：跨 Agent 忙时没有 `blocking_operation_id` 就不填本次 `op` 或
`session.origin`；`observation_not_found`、`no_ready_observation`、`content_unavailable`
不填本次 `op`；无 `session` 的 `session_ended`/`session_expired`/`control_revoked` 对
`end/P` 不填 `op`；被准入拒绝的 `start`（`recovering`、`device_unavailable`、
`ledger_unavailable` 等）不返回被拒 `start` 自身的 `op`。任何成员不可表示时整值按
`json v1 path="/recovery"` 兜底。

### 3.3 树块

树块只投影本次 `Result` 中已有的树或补看内容：不新增采集、不展开 delta、不缓存基线，
也不读取请求参数、时间或随机数；同一个 `Result` 重复渲染逐字节相同。

在 `result v4` 文本里（`text`/`both` 模式），错误结果同样按成员投影：`act` 重验失败交付的
新观察会带树块，头行的 `err` 与树块可以出现在同一个结果里。交付与否不改变 `isError` 的
判定：`act` 看 `outcome` 是否为 `executed`，`observe` 与 `wait_for` 看是否发布出核心 O，
其余工具按 `error` 与各自的查询事实。
`structured` 模式的错误文本是 `diagnostic v1`，不含树块。

**普通观察树。** 完整块只投影 compact full，差分只投影 compact delta。块头之后是一个
空行、节点行，最后是 `end tree`：

```text
result v4 ok op=P7391_12
obs O4821_72 fg=com.android.settings#177 at=22:05:11

tree v5 full nodes=8

n1 root 0,0,1080,2400
 n2 button 540,120 "设置"
  n3 button 84,120 "返回"
  n4 input 606,120 "搜索设置" value="" hint="输入关键词"
 n5 scroll 0,300,1080,2400 axis=y
  n6 switch 966,360 "启用" tap checked=true
  n8 "网络"
 n9 group 540,2100 "详情"
end tree
end result
```

普通节点行固定列序为
`缩进 n<ref> [<role>] [<x,y>|<l,t,r,b>|@] "label"… value="…" hint="…" 动作词 标志`。
读取 `n<ref>` 后，首项为不带引号的合法角色词时使用该显式角色；首项为 JSON 字符串字面量时
默认 `text`，该字符串是第一个 label；首项为几何（`x,y`、`l,t,r,b`、`@` 或 `-`）、`value=`、
`hint=`、普通动作（`tap`/`long_press`/`swipe`）、状态或属性标记、或行尾时默认 `view`。
发送端只有在省略后按上述判据恢复出原角色、且首项不会被误读为显式角色词时才省略角色：
无几何且有 label 的 `text` 省略 `text`；无 label 或带几何列（`x,y`/`l,t,r,b`/`@`/`-`）的
`text` 显式写 `text`；首项为几何、`value=`、`hint=`、普通动作、状态或属性标记、或行尾的
`view` 省略 `view`，首项为 label 或角色词（包括输入动作 `text`）的 `view` 显式写 `view`；
其他角色始终显式。引号内的角色词仍是 label。

只有 `button`、`input`、`scroll` 具有对应默认动作，分别为 `tap`、`text`、`swipe`；其他
角色没有可推断的默认动作。完整显式动作列表保留原顺序与重复项。省略默认词后，其余动作
必须是严格源顺序 `tap → long_press → text → swipe`，再按其在该顺序中的位置插回默认动作。
默认动作缺失时，保留其余源动作并在动作列末尾写 `!tap`、`!text` 或 `!swipe`。若其余动作
不是严格源序，或无法按规则完整恢复，写完整显式列表。出现对应否定词时不再补回默认动作；
同一动作不能既显式出现又被否定，其他角色的否定词无效。`!动作` 只表示源能力缺失，不授予
动作。受控滚动不增加动作词，以 `act.operation.type=scroll` 和 `operation.container` 表达。

标志为 `enabled=false|null`、
`focused`/`selected`/`protected` 的 `true|null`、`checked=true|null`、
`axis=x|y|both`、`resource="…"`、`truncated=true` 与 `context`。`truncated` 固定位于
`resource=` 之后、`context` 之前，只在 compact 观察的有动作标签被截断时出现，`false` 或
未截断时省略，`root`/`context` 与详细补看不输出该列；节点行例如
`n7 button 540,120 "产品说明" resource="title" truncated=true`。`axis` 由服务端投影：非
`context`、有公开几何的 `role=scroll` 节点按类名强特征（横向 `ScrollView`、
`NestedScrollView`/`ListView`/`AbsListView`、`ViewPager` 等）与子项单轴排布（至多前 16 项、
主轴中心跨度 ≥24px、主轴位移与跨度都不少于交轴 2 倍、至少半数相邻子项在交轴重叠）判为
`x` 或 `y`，子项证据优先；证据不足或双向时不输出该标志。compact 中保留的 `resource` 是
服务截取的语义短尾名；detailed 补看保留完整 source resource。设备来源字符串按 JSON 字符串
字面量转义，`<`、`>`、`&` 保持原样；缺省值按协议省略规则不输出。

几何列与节点定义一一对应：`root` 与 `scroll` 的已知几何写无括号的 `l,t,r,b`，四边均为有符号
十进制整数并满足 `right>=left`、`bottom>=top`；其余角色的已知几何写无括号的 `x,y` 二元中心
点，恒落在原矩形内并满足 `left<=x<right`、`top<=y<bottom`。源未提供几何写 `-`；观察视图里
按 compact 几何策略隐藏的已知几何直接没有该列，`-` 不表示隐藏。同一串行化定义中，普通节点
的已知几何与最近一个有已知几何的严格祖先同 `Kind` 且数值完全相等时写 `@`：四元矩形只缩写
相同矩形，二元中心点只缩写相同中心点；隐藏或未知几何不会清除该祖先。每个 `+`、`=` 子树
独立维护祖先，不从其他定义、base 或前一条结果继承；无可解析祖先时 `@` 无效。历史链定义
不参与 `@` 缩写，已知几何仍显式写出。

compact 几何策略保留合成根、`scroll` 容器与 `actions` 非空节点的几何：`root`/`scroll` 写
四元矩形，其余写二元中心点。无动作 `text` 与匿名普通 `view`/`group` 的已知几何被省略；
其余展示节点在裁剪到屏幕后，若四元矩形与父链上最近交付者四边完全相同，或该可见面积小于
屏幕面积的 1%，也省略。按[接口策略](interface.md#node)，露边细条裁剪与 `input` 标签去重
在决定几何前执行；上行阶段再执行无标注非交互 `image` 全裁、祖先复合标签覆盖的被动文本叶
裁剪、变空匿名容器清理、隐藏匿名容器扁平化、无字可点击 `view` 提升为 `button` 与
`resource` 短尾名提取。隐藏匿名
容器按裁剪后全部孩子的原顺序透传扁平化，单子与多子一致；被扁平化释放的纯文本子叶随后参与
`button` 提升。有动作、非 `input`、非 `context` 节点的 `label` 段超过 `250` 个 Unicode
码点时前置截取前 `250` 个码点并标记 `truncated=true`，未超长段不截断、不置位，也不追加
省略号或总字数；无动作的被动正文与 `input` 的 `value` 保持全文。被动文本提升为 `button`
生成的 label（无论段数）不再二次截断，保持全文且 `truncated` 缺席。公开 `label` 在构造时仅在含
固定闭集零宽与占位码点时剔除这些码点并把残留连续空行折叠为单个换行，随后无条件去掉首尾
空白（纯空白与 `U+00A0` 归空，内部非占位换行与 `input.value` 原样保留）。
text、structured 和 both 里的 compact 节点集合相同。
详细补看交付完整快照的节点、完整未截断的 `label` 与已知原矩形，节点定义不输出
`truncated`（`query` 顶层的预算字段除外）。

新 `tree v5` 只生成普通节点定义。读取端继续接受兼容历史的 `[n2 n3]` 匿名默认 `view` 链和
`chain [n2 n3] <role> ...` 定义；按顺序展开后，缩进的孩子归链尾，链内编号均可引用。
full 块头的 `nodes` 计展开历史链后的逻辑节点数。完整节点的缺席布尔恢复为已知默认，显式
`null` 保持未知；`checked` 默认 `false`。set 的缺席只表示未修改。树与补看块均不输出
defaults 行。

delta 块头为 `tree v5 delta base=<O|_<序号>> changes=<n>`，正文按
`set → add → remove → children → replace` 顺序输出。当前 O 来自同一结果的 `obs` 行，
不在块头重复。`base` 指向服务端基线池中的近期任一已确认 `full`：同一会话 origin、同一目标
显示 ID/代次且采集龄 `< 180s`，池中保留 1 条当前基线与至多 4 条备用 full（共 5 条）。当前
基线可在跨度 1–12 内作为 base，其他备用 full 可在跨度 1–10 内作为 base；备用 full 被成功
选回后，从下一次发布起成为当前基线。服务对每份合格 full 使用该 full 自己的节点编号重新
匹配同一采集，再将候选 full 与 delta 分别按本节的正式 `tree v5` 普通树块编码。比较范围是
`tree v5 ...` 块头至 `end tree` 行末
换行的 UTF-8 字节，包含实际短 base、转义、缩进和换行，不包含 `obs`、`result` 信封或图片。
只有 `10×deltaBytes < 8×candidateFullBytes` 的候选合格，精确等于 80% 不合格；合格候选取
delta 树块字节最少者，长度相同优先当前基线，再取 revision 较大的 full。

`observe(P)` 与 `full=true` 强制 full；凡作为 `base` 发出的 O 都在池内受留存保护，
`inspect(reference=base)` 可回看。客户端必须保留所有被引用的完整树；当前基线可在跨度
1–12 内继续成为 base，备用 full 在跨度 1–10 内可用，不能按修订数提前淘汰仍可能被引用的
full。按 `base` 独立应用，
不能叠加在上一份 delta 上。只有当前 O 与真实 base 都能在最后一个下划线处分成逐字节相同的
`O<实例>` 前缀和规范正十进制序号时，base 才写成 `_<序号>`；否则写完整 O。短 base 只用于
读树，不能作为调用引用。`changes` 是五个块的顶层声明条数之和：一个 `set` 条目即使改了
多个字段也只计 1。零变化 delta 仍保留 `base` 与 `changes=0`，正文为空。

```text
result v4 ok op=P7391_13
obs O4821_76 fg=com.android.settings#177 at=22:05:12

tree v5 delta base=_72 changes=6

~ n4 label=["无线设置"]
~ n5 bounds=[0,300,1080,1800]
+ n11 button "返回上页" !tap
 n12 "网络"
- n8
children n1 = [n2 n5 n11 n9]
= n9 group 540,2100 "详情"
 n10 input @ "备注" value="" hint="输入备注"
end tree
end result
```

备用 full 被成功选回时，同一个列表页可以只输出零变化差分：

```text
result v4 ok op=P7391_15
obs O4821_90 fg=com.zhihu.android at=22:05:20

tree v5 delta base=O4821_85 changes=0

end tree
end result
```

`~` 行只列实际变化的字段，字段值保留显式 `false`、`""`、`[]` 与 `null`；字段顺序为
`label`、`actions`、`bounds`、`enabled`、`focused`、`selected`、`checked`、`value`、
`hint`、`protected`、`axis`、`resource`、`truncated`。`truncated` 显式写 `true` 或
`false`（显式 `false` 表示从截断恢复为未截断），缺席表示未修改；`truncated=null` 与非法
值被拒绝，例如 `~ n7 truncated=false`。`bounds=hidden` 表示该几何按策略不交付，
`bounds=null` 表示置为未知；已知 `bounds` 按节点角色写 `[x,y]`（非 `root`/`scroll`）或
`[l,t,r,b]`（`root`/`scroll`），set 不使用 `@` 或无括号几何。`SetNode` 没有 `role`，
`~` 行也不输出 `role`。`+` 与 `=` 后输出普通 v5 节点定义及其子树；前缀只出现在所属定义
根行，不计入缩进层级。`@`、无括号几何和默认动作用于这些普通定义，不应用于 `~` 的 set 值。
`add` / `remove` 保持结果中的顺序；`set`、`children`、`replace` 按 ref 数值升序输出。
同一个 `base` 下的新 delta 覆盖旧 delta，不叠加；块头 `base=` 是唯一锚点。

**补看块。** 块头为
`inspect v3 scope=<tree|subtree|query> observation=<O> revision=<n> captured_at=<RFC3339> tree_scope=<scope> view=detailed [root=<ref>] [matches=<n> total=<n> truncated=<bool>] nodes=<n> entries=<m>`，
随后是一个空行与正文。
`tree` 输出整树，`subtree` 额外带 `root=`，`query` 按命中顺序输出
`match i/N path=[…]` 与完整命中子树，命中之间以空行分隔。`matches` 是本次返回条数，
`total` 是预算前命中数，`truncated=true` 表示引擎只返回了完整条目前缀；该 `truncated`
是查询预算事实，与 compact 的有动作标签截断列无关，补看块不输出节点级截断。`nodes` 计展开后的
逻辑节点数，`entries` 计正文定义行数。查询按每个命中子树分别累加两项计数。`path` 是
root..parent 的 ref 序列，
不含命中节点本身。补看固定使用 detailed 视图：逐节点展开、不折 chain、不隐藏已知几何。
`inspect v3` 使用两格缩进、`[l,t,r,b]` 方括号矩形和显式动作词，不使用普通 v5 的默认动作、
`@` 或短 base。其完整节点行仍按首列恢复角色；例如带已知几何的 `view` 节点省略 `view`，
带几何的 `text` 节点保留 `text`，未知几何仍写 `-`。

**预算与完整性。** MCP 不设文本大小上限，不产生 `omitted`。可读块能无损表示时完整输出；
非法 ref、缺少必要元数据、未知成员或其他不可表示情况以完整 `json v1` 兜底。树 ref 必须
满足 `n` 加非零十进制数字。服务的观察树与 detailed 全树上限为 128 KiB，subtree/query 为
32 KiB；引擎返回的 `truncated` 仍保留。块分别以 `end tree` / `end inspect` 结束。
缺失 `end result` 或 `end diagnostic` 表示对应文本被宿主截断；应改用宿主支持的
`structured`，或通过 `inspect` 的 node/query 收窄。`both` 同时发送文本与结构化两种表示，
会增加传输与上下文占用。

### 3.4 shell 与 timeline 块

`shell v1` 块自带头行；`timeline v1` 块前接 `diagnosis` 行与可选的
`diagnosis.issues`/`diagnosis.timing_ms` 行，块前恰好一个空行：

```text
shell v1 termination=exited exit_code=0 truncated=false output_bytes=6
output "hello\n"
end shell

timeline v1 events=1
event {"at":"2026-11-10T03:00:00Z","phase":"session","code":"active"}
end timeline
```

shell 的 `output` 按换行分块，每块包含原换行并编码为 JSON 字符串，拼接后保留原输出；空串
有一行 `output ""`，`null` 对应 `output_bytes=null` 且无 output 行。`output_error` 使用
完整 JSON。timeline 的 event 为完整 canonical 对象，保持数组顺序与未来成员；timeline 为空
数组时不输出块（该空集合只保留在 `structuredContent`），非数组或不可表示时按 JSON 兜底。
shell 的额外成员按 JSON Pointer 输出。这些内容只进入响应，不写日志。

## 4. stdio

```text
device-mcp -transport stdio -config <private-config>
device-mcp -transport stdio -config <private-config> -result-mode text
```

- 每条工具调用在独立请求上下文中执行；宿主发送 `notifications/cancelled` 时，本次调用
  上下文被取消，由取消包装按第 6 节处理。
- 进程退出、stdin 关闭与连接断开都不证明设备动作已撤回或原执行链已结束。
- 默认模式按显式 `-result-mode` > `MOBILE_USE_MCP_RESULT_MODE` > `text` 选择；
  值仅接受 `both`、`text`、`structured`。显式空值、前后空白、大小写变体或非法值启动失败，
  退出码为 `2`，不会回退到环境变量。

## 5. Streamable HTTP

```text
device-mcp -transport http -config <private-config>
```

- 端点以 `POST` 承载请求与 SSE 响应；stateless 模式下 `GET`/`DELETE` 返回 `405`，不建立
  跨请求的 MCP 会话。进程只监听私有配置中的 loopback 地址，路径不作为授权依据。
- 每个请求必须携带 `Authorization: Bearer <mcp_token>`；缺失或错误返回 `401`，不进入
  工具调用。该令牌是宿主侧 MCP 凭据，与服务 `agent_token` 分离。
- 携带 `Origin` 的请求与私有配置的 `allowed_origins`（`scheme://host[:port]`）在忽略末尾
  一个或多个 `/` 后精确比较；其余差异返回 `403`。不含 `Origin` 的非浏览器客户端按
  已认证请求处理。
- 每请求使用独立临时 MCP 会话，不继承其他调用方的 MCP 会话、工具参数或结果；不跨 Agent
  串用归属。
- 每请求可用 `X-Mobile-Use-Result-Mode: both|text|structured` 覆盖进程默认模式；无此头时
  使用默认值。该头必须恰好一个值，空值、多值、逗号、前后空白与大小写变体返回 `400`，在
  工具执行前拒绝。它只在适配器内使用，不发送给设备服务，也不影响认证与归属。
- `2026-07-28` 的一次 POST 就是完整请求生命周期。客户端关闭响应的 SSE 流时，服务请求
  上下文被取消，按第 6 节处理；SSE 断开本身不证明设备动作已撤回。

## 6. 取消、P 与漏收边界

有本次新 P 的工具调用（`start`、`observe`、`snap`、`wait_for`、`act`、`keepalive`、`end`、
`app`、`notify`、`shell`）在取消时：

1. 停止等待本次调用结果；
2. 对需要 `end` 的分支用独立的有界上下文调用 `end(P)`（上限 5 秒），不等待设备工作完成；
   若服务已不认识原 P，没有原结果时保留其 `operation_unknown`、`request_closed=true`、
   `uncertain` 事实；已知原执行事实优先；
3. 保留取消前已取得的服务结果；没有结果时不伪造 `not_executed`；
4. 返回结构化取消结果，说明 P 与已发起的有界 `end(P)`，但不声称设备动作已撤回、资源已释放
   或原执行链已结束。未知 `end` 只封闭该 P 的未来接纳，绝不能用来结束当前其它会话。

`app` 整工具不是只读，七个 kind 都预分配 P，但取消策略按 kind 区分：

- `find`/`list` 查询取消不调用 `end`。结果保留已预分配的 `operation_id`，省略 `outcome`
  和 `recovery`。已取得的服务结果保留 `app.kind` 和业务集合等已有事实。未取得服务结果时，
  仅补上 `app.kind`，省略集合字段。没有已知服务错误时生成 `error.code=call_cancelled`；
  已有服务错误则保留原 code/message/details。两种情况都在 `error.details` 注入
  `mcp.cancelled=true` 与受控文案 `mcp.cancellation="查询已取消；未发起 end"`。
- 五个变更 kind（`open`/`claim`/`handoff`/`close`/`kill`）按通用步骤 2 发起有界 `end(P)`，
  保留真实的 end 文案、session/cleanup 与恢复入口；不能把查询的“未发起 end”文案复制给
  变更，也不能省略实际发起的 end。

`wait_for` 不是只读工具：它按通用步骤 2 发起有界 `end(P)`，取消同时关闭等待上下文并释放
普通工作位。取消保留取消前已取得的 `wait` 事实，也不声称新观察已发布。

`snap` 预分配 P，但取消不调用 `end`：只撤销本次读取。即使适配器已经拿到成功提交结果，
取消分支也清空顶层 `image` 元数据与 `ImageContent`，返回 `call_cancelled`，不交付图片。
原会话仍活动；读取的其余撤销与收尾由服务端 STOP/end 语义处理。

`status`、`inspect`、`diagnose` 无本次 P，取消只中断读取，也不调用 `end`。调用参数中的
`reference` 只定位原观察或操作。未取得服务结果的取消错误省略 `operation_id` 和
`recovery`。已取得服务结果时，保留服务给出的原编号及恢复信息。

取消结果一律 `isError=true`。适配器保留已有服务错误，缺少错误时合成 `call_cancelled`；`snap`
是唯一例外：取消分支无条件返回 `call_cancelled`，并丢弃已取得的图片。适配器还在
`error.details` 注入保留键 `mcp.cancelled=true`、`mcp.cancellation=<受控说明>`，文本与
结构化两种表示都包含它们。此命名空间只由 MCP 适配器使用，`/api/*` 永不产生。文本无额外
顶层 `cancelled` 行；取消不改写原 `outcome`、观察与收尾证据。

漏收分两段：

- 服务到适配进程的本次 HTTP 结果丢失时，有预分配 P 的调用可返回带 P 的结构化错误，
  宿主随后按该 P 查询原事实。无本次 P 的三项查询若返回 `transport_unavailable` 且没有
  服务结果，则省略 `operation_id` 和 `recovery`，连接恢复后可用原参数重查。
- MCP 到宿主的整份工具结果丢失且宿主没有 P 时，不保证定位该次操作；只能用已有的 O/P 或
  `status()` 恢复自身原会话，不能用最近一次操作代替，也不把 MCP 请求 ID 当作公开恢复
  编号。

## 7. 配置与运行

主机适配器私有配置只存在于部署机，权限 0600，不入库：

```json
{
  "service": {"base_url": "http://127.0.0.1:18881", "agent_token": "<agent-token>"},
  "transport": "stdio",
  "http": {
    "listen": "127.0.0.1:18882",
    "token": "<mcp-token>",
    "allowed_origins": ["http://127.0.0.1:3000"]
  }
}
```

- `service.base_url` 必须是 loopback；`service.agent_token` 只用于访问服务；
  `http.token` 与 `agent_token` 必须不同。
- 未提供 `http.listen` 时不能以 `http` 传输启动；`allowed_origins` 只接受
  `scheme://host[:port]`。
- 宿主侧工具超时必须覆盖一次调用的完整预算：验证采集整体受 35 秒固定预留截止约束，因此带图
  与无图调用都按 `timeout <= host_timeout - 35s` 预留。宿主在到点时取消调用，适配器按第 6
  节发起有界 `end(P)`，被取消的等待不保证产出新观察。
- 构建与启动方式见 [运行手册](runbook.md) 的 MCP 章节。
