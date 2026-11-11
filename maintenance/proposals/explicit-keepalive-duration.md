# 提案：显式重设会话租期并取消累计时长硬上限

状态：提案，未实现。
基线提交：`d3161e7b8a479b29ef8b5412ba21d0a0416453b0`（分支 `master`，2026-11-11）。
适用范围：HTTP/MCP `start` 与 `keepalive`、Go 客户端、现有自动续期点及会话期限检查。
影响范围：`internal/protocol`、`internal/engine`、`internal/client`、`internal/mcp`、相关测试、共同版本源及所涉正式文档。
契约（`/api/*` 字段与 `contract_version`）：改变。`start` 租期改为五分钟；`keepalive` 增加可选 `duration_minutes`，显式续租由单调延长改为覆盖截止时间，取消一小时硬上限；返回值沿用现有 Result/session 形状，拟将共同契约从 7 升至 8。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对，重点为下文证据链、客户端转发路径、工具目录及共同版本源。

## 0. 摘要

Agent 长时间思考或等待用户时，可能来不及在 120 秒内续租，导致会话到期并清理虚拟屏操作现场。
保留到期回收，允许每个新的显式 `keepalive` 指定 1～120 分钟，从应用更新时的服务端当前时间重设截止时间，省略时为 2 分钟。
`start` 的初始化、激活与首观察发布使用五分钟租期；其他普通活动仍在现有触发点按 `max(原截止时间, now + 2分钟)` 单调续期，不继承显式时长。显式 `keepalive` 可以延长或缩短，包括主动缩短刚建立的五分钟启动租期。
取消会话累计时长的一小时硬上限；到期不可复活、STOP/end、观察有效期、基线与单次工作期限仍各自生效。
接口契约、工具描述和参数说明必须直接交代“重设”“省略也会缩短”“普通活动可再次延长”，避免调用方把它理解为只增不减或固定的最晚终止时刻。

## 1. 问题

### 1.1 现象与代价

使用方反馈：思考或等待用户回复超过当前租期时，会话被撤销；虚拟屏任务与显示进入清理，下次继续需要重新建立会话、打开应用并恢复页面。
现行 `keepalive` 只能把截止时间单调推到 `min(now + 120s, started_at + 1h)`。它既不能提前申请更长的等待窗口，也不能取消一小时累计限制。
本项分诊为取舍：上述行为符合当前契约，问题来自生命周期策略；此次只形成提案，不将新行为写成正式契约或实测结论。

### 1.2 证据链

| 事实 | 基线位置 |
| --- | --- |
| 默认滑动租期 120 秒、累计硬上限一小时 | [engine.go:63](../../internal/engine/engine.go#L63)、[engine.go:100](../../internal/engine/engine.go#L100) |
| `keepalive` 请求只声明 `reference`，无时长成员 | [surface.go:48](../../internal/protocol/surface.go#L48)、[surface.go:216](../../internal/protocol/surface.go#L216) |
| HTTP 解码后交给 engine；engine 显式续租复用普通续期函数 | [server.go:298](../../internal/httpapi/server.go#L298)、[keepalive.go:33](../../internal/engine/keepalive.go#L33) |
| 普通续期受硬上限截断，且只增不减 | [engine.go:819](../../internal/engine/engine.go#L819) |
| 初始会话也设置硬期限；公共授权同时检查两个期限 | [start.go:84](../../internal/engine/start.go#L84)、[operations.go:292](../../internal/engine/operations.go#L292) |
| 会话越线后，时钟撤权并启动清理 | [engine.go:705](../../internal/engine/engine.go#L705)、[engine.go:730](../../internal/engine/engine.go#L730) |
| 虚拟屏清理尝试移除原任务/最近任务并请求关闭显示；主屏不清用户任务 | [cleanup.go:283](../../internal/engine/cleanup.go#L283)、[cleanup.go:63](../../internal/engine/cleanup.go#L63) |
| 相同 P 返回原操作视图，不再次续租；已有测试确认重放可投影更新后的会话截止时间 | [keepalive.go:23](../../internal/engine/keepalive.go#L23)、[readonly_test.go:429](../../internal/engine/readonly_test.go#L429) |
| keepalive 首次成功响应构造 operation_id/outcome/session；newResult 不自动填 operation_state/phase | [keepalive.go:46](../../internal/engine/keepalive.go#L46)、[operations.go:351](../../internal/engine/operations.go#L351) |
| session 对活动会话投影 expires_at，对 ended 投影 ended_at/reason；时间格式为 UTC RFC3339Nano | [engine.go:885](../../internal/engine/engine.go#L885)、[responses.go:283](../../internal/protocol/responses.go#L283) |
| 重放/查询原 P 的已完成视图包含 operation_state/phase，并重投影原会话当前事实 | [engine.go:1031](../../internal/engine/engine.go#L1031) |
| SDK 重建只含 `reference` 的请求；MCP 也只传该成员，新增字段必须贯通两层 | [client.go:252](../../internal/client/client.go#L252)、[server.go:494](../../internal/mcp/server.go#L494) |
| 正式契约和工具说明均承诺单调续租及一小时硬上限 | [interface.md:639](../../docs/interface.md#L639)、[schema.go:234](../../internal/mcp/schema.go#L234) |
| 共同契约号为 7；默认行为或执行语义变化应推进契约版本 | [versions.json:1](../../release/versions.json#L1)、[release.md:19](../../docs/release.md#L19)、[release.md:42](../../docs/release.md#L42) |

现有自动续期点分别是：`start` 激活、`act` 成功认领后、成功发布新 O，以及受管 `shell` 的派发边界。依据为 [start.go:275](../../internal/engine/start.go#L275)、[act.go:48](../../internal/engine/act.go#L48)、[capture.go:294](../../internal/engine/capture.go#L294)、[shell.go:307](../../internal/engine/shell.go#L307)。`open_app` 认领本身没有直接续期调用，成功发布后观察才经过公共发布续期点，见 [openapp.go:86](../../internal/engine/openapp.go#L86)。本提案不新增或移动这些触发点。

### 1.3 为什么直觉会出错

- 把显式续租与普通续期共用一个 `max` 更新，会使调用方无法缩短已设置的长租约。
- 把显式传入的 120 分钟记成后续自动续期时长，会使普通操作不断把截止时间推到两小时后，偏离“比较剩余时间与两分钟”的约定。
- 把省略参数理解为“维持当前租期”，会在长租约后发出一次默认调用时意外缩短到两分钟。
- 只去掉显式 `keepalive` 的硬上限截断，公共工作授权和初始会话仍会按一小时终止。
- 只改 MCP schema 或服务端解码，现有 MCP/SDK 转发会丢弃时长字段。

## 2. 目标与非目标

目标：显式时长可控且可缩短；自动活动保留长租约；有效会话可连续续租超过一小时；所有入口采用同一份期限权威；调用方仅凭契约和工具描述即可预知默认调用、重放和并发更新的结果。

非目标：取消空闲回收、增加后台心跳、保证任意长等待、暂停或接管会话、跨服务/App 重启保留界面、改变 O/P 消费与恢复协议、增加工具数量、扩大普通动作的续期触发范围。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 保持现状 | 固定 120 秒滑动期限与一小时硬上限 | 无实现成本，调用方继续承担高频保活 | 思考/用户等待跨过期限就清理；持续保活也无法超过一小时 |
| R2 本提案 | start 五分钟，显式重设 1～120 分钟，其他自动活动按两分钟只延长，取消累计硬上限 | 默认调用可缩短，必须明确说明并推进契约版本 | 未提前申请足够时间或等待超出租期时仍清理 |
| R3 长期显式会话 | 取消空闲到期，依靠 end/STOP 释放 | 遗弃会话长期占用，需要额外主动管理 | Agent 消失但无人释放，资源与当前会话持续占用 |
| R4 控制权到期、保留界面 | 另设暂停/恢复与资源生命周期 | 新状态、归属和恢复语义较多 | 资源仍可能长期占用，接管与旧工作隔离复杂 |

选择 R2，落实用户给出的时长、覆盖及最大值规则。R3/R4 只有在另行明确需要无限等待或自动接管时再评估。

## 4. 判据

1. 请求为 `{"reference":"O1","duration_minutes":30}`；`reference` 沿用原归属解析规则。`duration_minutes` 是可选 JSON 整数，闭区间 1～120，省略为 2；显式 `null`、字符串、布尔、非整数及越界均拒绝，不截断或纠正。
2. 新的显式操作在 engine 状态锁内取 `now`，检查原会话仍属于调用方、仍为当前 `active`，且 `now < expires_at`；通过后同锁执行 `expires_at = now + duration_minutes × 1分钟`。
3. 新调用无需等待原期限，既可延长也可缩短。省略时长同样覆盖为 `now + 2分钟`。每次使用本次参数，不保存可被后续调用继承的“上次时长”。
4. `start` 接纳新会话时，以 `now + StartSessionTTL` 初始化期限，发行默认 `StartSessionTTL = 5分钟`；激活与首观察成功发布两个既有续期点均使用 `max(expires_at, now + 5分钟)`，正常启动且无并发显式更新时，首观察发布后的期限从发布时起五分钟；若现有期限更晚则保留，并以返回视图实际的 expires_at 为准。首观察失败但会话保留时，不触发额外续期，保留当时现有 ExpiresAt（包括期间已应用的显式 keepalive 更新），不回滚到激活时的旧期限。其他普通活动只在既有触发点执行 `expires_at = max(expires_at, now + SessionTTL)`，发行默认 `SessionTTL = 2分钟`，不继承显式时长；因此不会截短五分钟启动租期。上述更新均不能复活已到期会话，不改变 start 创建/工作期限。重复 P 的 start 不重新建立五分钟租期，因 session_exists 等拒绝的新 start 也不续期已有会话。
5. engine 只有一个权威 `ExpiresAt`；取消 `SessionHardLimit`、`HardExpiresAt` 及相关截止判据。120 分钟约束单次显式请求，不约束会话累计寿命。
6. `now == expires_at` 已到期，即使时钟尚未清理也拒绝续租。`ended` 返回 `session_ended`，未撤权但已越线返回 `session_expired`，`starting` 返回 `reference_has_no_session`。失败不修改截止时间。
7. “每次刷新”指每个新的 P。既有可识别 P 重试只读原操作视图或封闭事实，不重设截止时间；修改参数要创建新操作。重放视图可投影原会话的当前事实，不承诺返回首次响应中的旧截止时间。沿用现有 P 记录保留、封闭、服务实例与编号使用边界，不新增无限历史去重保证；长会话不等于服务永久保存其全部历史 P，调用方不得把旧编号作为新操作编号反复复用。
8. 并发更新按 engine 锁内的实际应用顺序生效。显式调用覆盖当时的截止时间，普通续期再按最大值规则执行。STOP/end 若先撤权，后来的续租不能恢复；续租若先成功，也不阻止后来的 STOP/end。
9. `keepalive` 不占普通工作位置、不排普通动作队列、不访问设备或新增账本写入；普通工作正在运行时仍可处理。接口不承诺零等待或绕过认证、契约、容量、关闭等既有接入边界。
10. 首次成功响应返回当次锁内更新后的 `session.expires_at`。后来的显式调用、自动活动、STOP/end、服务/App 生命周期或 MCP 取消可以改变后续事实；该值不承诺任务必定完成或会话必定存活至此。
11. 不延长 O 的 `actionable_until`、不改 O 状态、不确认基线、不延长工作/shell 期限。O/P 必须仍可解析；引用本身不授予新权限。

## 5. 算法

```text
start 接纳新会话时:
    ExpiresAt = now + StartSessionTTL  // 默认五分钟
    激活、首观察发布的既有续期点沿用 ordinary_touch(WorkStart, now)

keepalive(reference, duration_minutes omitted = 2):
    严格校验请求和时长
    登记本次 P；若是重放，返回原操作视图
    lock:
        now = 服务端当前时间
        解析、校验原会话及 now < ExpiresAt
        若失败：不修改租期，复制错误与原 session 视图
                unlock
                完成本次 P，返回 not_executed 与原错误
        ExpiresAt = now + duration_minutes * 1分钟
        复制此次 session 视图
    unlock
    完成本次 P（记录内部 phase=renewed），返回 executed 与视图

ordinary_touch(workKind, now):
    在原触发点持锁检查 active 且 now < ExpiresAt
    ttl = StartSessionTTL if workKind == WorkStart else SessionTTL
    ExpiresAt = max(ExpiresAt, now + ttl)

session_before_deadline(session, now):
    return now < session.ExpiresAt
```

| 时间 | 新操作 | 结果截止时间 | 含义 |
| --- | --- | --- | --- |
| 10:00 | `keepalive(30)` | 10:30 | 从本次服务端应用时间重设 |
| 10:10 | 普通活动续期 | 10:30 | 剩余 20 分钟大于默认两分钟 |
| 10:11 | `keepalive(1)` | 10:12 | 主动缩短，不等待原期限 |
| 10:11:30 | 普通活动续期 | 10:13:30 | 一分钟不是禁止后续延长的上限 |
| 10:12 | `keepalive(120)` | 12:12 | 新显式操作再次覆盖 |
| 10:13 | 不带时长的 `keepalive()` | 10:15 | 省略等于显式 2，可显著缩短 |
| 10:14 | 重放上一行同一个 P | 10:15 | 重放不重新计时 |

时钟到期仍撤权并启动同一条有界收尾链；工作超时仍只处理原 P；请求在应用前越线则拒绝，不因较早到达 HTTP/MCP 入口而取得追溯续期资格。续租结果漏收不回滚已应用更新；调用方按 P 查询，或创建新的显式操作。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 计划改动 |
| --- | --- |
| [surface.go](../../internal/protocol/surface.go)、[encode.go](../../internal/protocol/encode.go) | 增加 `DurationMinutes` 与显式存在标记，严格区分省略、null、0 与有效整数；编码省略时不补 null，原引用校验沿用 |
| [keepalive.go](../../internal/engine/keepalive.go) | 与普通单调续期分开，锁内取时、校验、覆盖并复制视图；保留 P 重放与错误规划 |
| [engine.go](../../internal/engine/engine.go)、[start.go](../../internal/engine/start.go)、[operations.go](../../internal/engine/operations.go) | 删除累计硬期限字段、初始化、clamp 和公共授权检查；增加 StartSessionTTL=5分钟，start 初始化/激活使用它；其他普通续期仍按 SessionTTL=2分钟取最大值 |
| [capture.go](../../internal/engine/capture.go)、[act.go](../../internal/engine/act.go)、[shell.go](../../internal/engine/shell.go) | 公共发布按工作类型区分 WorkStart 的五分钟与其他工作的两分钟；不新增自动续期边界或继承显式时长 |
| [client.go](../../internal/client/client.go) | 保留默认 `Keepalive(ctx, reference, opts...)`，新增专用 `KeepaliveWithDuration(ctx, reference, durationMinutes, opts...)`，共用强类型请求发送；不把时长加进所有工具的通用调用选项 |
| [server.go](../../internal/mcp/server.go)、[schema.go](../../internal/mcp/schema.go) | 根据参数是否存在调用对应 SDK 路径，完整透传；新增整数 schema/边界/默认值及 §6.5 文案，工具名称和数量保持 |
| 所涉 protocol/engine/client/MCP 测试 | 替换一小时硬上限预期，覆盖新语义和完整转发，保留到期不可复活、STOP 与 P 幂等测试 |
| [versions.json](../../release/versions.json) 与生成常量 | 实现时统一推进契约号并按发布规范生成 Go/Java/XML；不在此次提案修改版本 |

### 6.2 配置与取值

显式时长单位固定分钟，默认 2、最小 1、最大 120，属于公共契约；不新增单位、无限期、负数哨兵或客户端时钟参数。
`start` 独立使用内部 `StartSessionTTL`，发行默认 300 秒；其初始化、激活、首观察成功发布均采用这项策略。其他普通续期继续使用内部 `SessionTTL`，发行默认 120 秒；显式 keepalive 省略值仍按公共默认 2 分钟解析，可以主动截短启动租期。测试可以使用受控时间或较短内部策略，不把测试策略暴露为请求参数。
直接 engine/SDK 调用也必须拒绝显式无效时长，不把 0 当默认。有效默认只来自参数省略。

### 6.3 日志与可观测性

复用操作 P、`phase=renewed`、成功/失败与会话终态事实。以响应的 `session.expires_at` 和后续 `status` 验收，不引入请求正文、引用正文或设备内容日志；本提案不要求新增日志事件或账本字段。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威、短状态锁 | 同一把锁持有单个截止时间；锁内只做查找、校验、时间运算与视图复制 |
| STOP 不等待 | 续租不占工作位且不做 I/O；STOP/end 仍可独立撤权 |
| 提交条件化 | 候选发布仍复核原会话/工作/显示/当前 ExpiresAt；取消累计硬期限不取消会话到期检查 |
| O 单向消费与基线确认 | 显式续租不修改 O；普通动作原认领/确认规则保留 |
| 执行、观察、会话与收尾分层 | `executed` 只说明续期更新已应用，不声称原设备工作完成或现场可跨重启保存 |
| 严格接入与 P 幂等 | 新字段按声明严格校验；旧 P 不再次更新，即使参数不同 |
| 既有取消包装 | MCP 新 P 调用取消仍按原 `end(P)` 规则处理；不以长租约覆盖取消边界 |

### 6.5 契约与文档影响

本节为待实现草稿，只有实现具备后才同步正式文档并执行独立文档审计。

**接口契约草稿：**

> `keepalive(reference, duration_minutes=2)` 将引用定位的当前活动会话的租约截止时间，重设为服务端应用本次更新时的当前时间加指定分钟数。`duration_minutes` 是 1～120 的整数，省略等于 2。每个新的操作均覆盖原截止时间，可延长也可缩短；省略参数也可能把原长租约缩短到两分钟。
>
> `start` 建立新会话时初始化五分钟租期，激活和首观察成功发布时均按 `max(当前截止时间, now + 5分钟)` 续期；其他普通活动仅在各自既有续期点更新为 `max(当前截止时间, 服务端当前时间 + 2分钟)`，因此不会截短长租约，但可再次延长已缩短的租约。不继承最近一次显式续租时长；显式默认两分钟的 keepalive 可以主动截短五分钟启动租期。会话没有累计时长硬上限，120 分钟是单次显式请求范围。
>
> 续租必须在现有截止时间之前应用，已到期或结束的会话不能恢复。无需等待旧租约到期，不占普通工作位置。相同可识别 P 重试不再次更新；改变租期须使用新操作。P 记录与封闭事实沿用现有有界保留及编号使用规则，不提供无限历史去重。成功返回当次更新后的 `session.expires_at`，重放可显示原会话的当前事实，后续调用和撤权事件仍可改变会话事实。
>
> 本操作只修改会话期限，不延长任何 O 的 `actionable_until`，不改变 O 状态、不确认基线、不延长工作期限、不访问设备或新增账本写入。STOP/end、App/服务生命周期及 MCP 取消规则仍适用。

**MCP 工具 description 草稿：**

> 重设给定 O/P 所指当前活动会话的租约截止时间。duration_minutes 为 1～120 的整数，默认 2；每个新操作从服务端应用更新时计时并覆盖原截止时间，可延长或缩短。省略参数也会重设为两分钟，可能缩短已有长租约或 start 的五分钟启动租期。start 使用五分钟续期；其他普通动作仅在既有续期点取 max(现有截止时间, now+2分钟)，不会缩短长租约，也不继承本次时长；它们可再次延长已缩短的租约。无会话累计时长硬上限。调用不等待旧租约到期、不占普通工作位；已过期或结束不可恢复，同一可识别 P 重试不再次更新。只改会话期限，不延长 O 或工作期限、不确认基线；STOP/end、App/服务生命周期与 MCP 取消规则仍适用。成功返回本次 operation_id、outcome=executed 和 session，session.expires_at 是当次更新后的绝对截止时间；继续保留会话须在当前截止时间前成功续期。

**start 工具相关描述草稿：**

> 建立新会话；启动初始化、激活与首观察成功发布使用五分钟租期。正常启动且无并发更新时，成功首观察发布将租期推至发布时起五分钟，已有更晚期限则保留；以响应 session.expires_at 为准，网络交付会消耗剩余时间。后续 observe/act/open_app/shell 等仅在既有续期点按两分钟取最大值，不截短启动租期。显式 keepalive 可重设并缩短它。五分钟不延长单次 start 工作期限，也不延长首个 O 的独立输入有效期；重放原 start 操作不重新获得五分钟。

**参数 description 草稿：**

- `reference`：本 Agent 的 O 或 P，必须仍能定位当前活动且未到期的原会话；不要求 O 仍可用于输入，不复活已到期或结束会话。
- `duration_minutes`：从本次更新应用时起重新计时的分钟数，整数 1～120，省略为 2；覆盖原截止时间，所以较小值或省略参数可缩短已有长租约。后续普通活动仍可延长。

**keepalive 返回值（沿用现有 Result/session 形状）：**

首次成功沿用 HTTP 200，业务结果包含本次 `operation_id`、`outcome=executed` 和更新时的 `session`；不增加独立的 `keepalive` 响应对象、时长回显或剩余分钟字段。绝对期限以 `session.expires_at` 为准。MCP 保留现有结果模式，下面 JSON 表示结构化业务结果，不替换 MCP 外层协议。

以下是新语义的说明示例，不是实测记录：假定服务端在 `2026-11-11T02:00:00Z`（北京时间 10:00）应用请求：

```json
{"reference":"O1","duration_minutes":30}
```

首次成功业务结果为：

```json
{
  "operation_id": "Pkeep1",
  "outcome": "executed",
  "session": {
    "origin": "Pstart",
    "state": "active",
    "mode": "virtual",
    "agent": "local-agent",
    "started_at": "2026-11-11T01:59:00Z",
    "expires_at": "2026-11-11T02:30:00Z"
  }
}
```

| 字段 | 契约含义 |
| --- | --- |
| `operation_id` | 本次 keepalive 的 P，用于查询或识别重试；与建立会话的 P 分开 |
| `outcome` | `executed` 表示本次租期重设已应用，主动缩短同样算 executed；不表示设备动作执行或原任务完成 |
| `session.origin` | 原会话的 start 操作 P；续租不新建会话、不改变 origin |
| `session.state` / `mode` / `agent` | 更新时原会话的状态、模式与归属；成功时该快照为 active |
| `session.started_at` | 原会话建立时间，续租不重置 |
| `session.expires_at` | 本次更新后的绝对截止时间，使用现有 UTC RFC3339Nano 格式，不是相对分钟数；示例为北京时间 10:30 |

首次成功响应不新增 `operation_state` 或顶层 `phase` 的必出保证。完成的 P 在内部仍记 `phase=renewed`，其含义为期限更新已应用，包含缩短；查询/重放已完成 P 时仍可出现 `operation_state=finished`、`phase=renewed`。重放不再次修改期限，其中的 session 可以反映后来的期限或 ended 状态，不能把历史 outcome=executed 解释成当前又续租成功。

| 分支 | 返回与期限效果 |
| --- | --- |
| 参数/类型/范围不合法 | 沿用接入层 `422 invalid_request`，直接返回 error，不登记本次 P、不修改期限 |
| ended / 越线 / starting | 已登记操作返回 `outcome=not_executed`、对应 `error` 和现有 `recovery`；若原会话可定位则携带其视图，不修改期限 |
| 引用未知、外来或不能定位会话 | 按原错误族返回；不能定位时不承诺 session 成员，不改绑其他会话 |
| ended 会话视图 | 使用 `ended_at`、`reason`，省略 `expires_at`，不借用历史截止时间表示会话仍活动 |
| 重复可识别 P | 返回原操作视图或封闭事实，不再次重设；已完成视图的 session 可能是当前事实 |

keepalive 不发布新 O，首次正常成功不返回观察树，也不因续租创建 cleanup；查询/重放看到 ended 会话时，沿用原视图可能携带的 cleanup。成功截止时间是应用时的快照，后续显式更新、普通续期或撤权仍可改变它。

**正式文档同步范围：**

- [interface.md](../../docs/interface.md)：请求字段、默认/校验、覆盖与最大值公式、错误/幂等/并发、无累计硬上限、示例与期限保证措辞。
- [architecture.md](../../docs/architecture.md)：会话生命周期和单个期限权威，去除一小时约束，区分显式覆盖与自动单调续期。
- [mcp.md](../../docs/mcp.md)、[client.md](../../docs/client.md)：参数和 SDK 路径、完整透传、调用取消仍可撤权、默认调用可能缩短。
- [release.md](../../docs/release.md) 及实际出现契约号/工具目录指纹的正式材料：仅按实现后的共同版本与产物更新，历史维护记录不重写为新规则。

升级是语义变更：旧调用方即使只传 `reference`，也不能再假定显式调用只增不减。共同契约号从 7 推进到 8，仍按现有精确匹配策略整组升级，不用新增字段可选来掩盖默认行为变化。

## 7. 预期效果与能力边界

### 7.1 能治什么

调用方可在思考/等待前申请最长两小时，长租约不被普通活动截短；之后可主动缩短，无需等旧租期结束。持续续租的有效会话可以跨过累计一小时。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 长时间静默超出已有租期后仍清理 | start 提供五分钟初始窗口；其他普通续期按两分钟，显式省略也重设为两分钟，均保留到期回收 |
| 用户回复超出已申请期限，或静默超过单次最长 120 分钟 | 不提供无限期保留，也无后台续租 |
| 续租成功但 O 已不能操作，需要重新观察 | O 有独立输入有效期，续租不延长它 |
| STOP/end、服务或 App 重启、MCP 调用取消后会话早于截止时间结束 | 长租约不覆盖这些终止边界 |
| 刚缩成一分钟，随后动作又将期限推后 | 普通活动最大值规则允许再次延长，一分钟不是会话最晚终止承诺 |
| 长租约后一次默认调用导致期限缩短 | 这是显式覆盖的契约；必须由工具说明和示例直接告知 |
| 续租结果漏收或重放显示的期限与首次响应不同 | 更新与网络交付分离，重放只读原操作/原会话事实 |

### 7.3 成本

每次更新仍为一次短状态锁内操作，无新增设备读取、磁盘写入或后台心跳。允许较长空闲保留会增加遗弃会话占用时间；调用方承担选择合理时长和在完成后显式结束的责任。新增严格字段与 SDK/MCP 转发、共同契约升级和所涉验证是主要工程成本。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| start 初始化、激活与首观察发布 | 均采用五分钟策略；无并发重设时首观察发布后的期限为发布时起五分钟；首观察失败且会话保留则不额外续期、不回滚，保留当时现有期限（含期间显式更新），不延长工作超时 |
| start 后普通 observe/act 续期，以及重复/被拒 start | 普通两分钟最大值更新不截短启动租期；重放或拒绝 start 不重新给已有会话五分钟 |
| start 后显式 keepalive 省略时长 | 可将启动的五分钟剩余租期主动缩短为当前时起两分钟 |
| 省略、显式 1、2、120 | 分别重设为 now+2/1/2/120 分钟，响应时间与锁内更新一致；首次业务返回 operation_id/outcome/session，与现有形状一致 |
| keepalive 响应归属与状态分支 | origin/started_at 不重置；active 给 expires_at，ended 给 ended_at/reason 不给 expires_at；重放/查询可有 operation_state/phase 且反映当前 session |
| 长租约后省略参数或指定更小值 | 截止时间缩短；反向对照：复用旧 `touchSessionLocked` 的单调更新必须失败 |
| 连续不同 P 的显式调用、工作位被占时调用 | 不等旧期限、不占工作位，每个新操作独立覆盖 |
| 普通续期面对 30 分钟/30 秒剩余时间 | 前者保留原期限，后者补足默认两分钟；不继承显式时长 |
| 显式缩短后普通续期 | 仍可延长到 now+2 分钟 |
| 会话累计超过一小时但 ExpiresAt 仍在未来 | keepalive、工作授权和候选发布仍允许；反向对照：保留旧硬期限授权检查必须失败 |
| now 等于/超过 ExpiresAt、已 ended、starting、外来/未知引用 | 分别按原错误族拒绝，不复活、不改变期限、不错误改绑 |
| 请求在入口到达后等待状态锁跨过旧 expiry | 锁内取时后拒绝，证明不使用过时入口时间复活 |
| 相同 P 重放，含更改时长的有效请求 | 不再次修改期限，保持现有视图投影语义 |
| 显式更新与普通续期、STOP/end 的受控交错 | 满足各自应用顺序；撤权后不能恢复或提交迟到候选 |
| null、字符串、布尔、非整数、0、负数、121，及未知/重复/大小写字段 | 严格拒绝，不归一化为默认值；SDK/直接 engine 显式无效值也拒绝 |
| HTTP 与 MCP/SDK 时长透传 | 缺省保持缺省，显式 1/120 不被重建请求丢弃；schema 与接入边界一致 |
| 观察/基线/工作期限不变 | 续租前后 O 的输入截止、状态、基线与工作期限均不因显式更新变化 |
| 到期回收与重复 end | 仍只撤权并建立原会话同一条收尾链，不因新时长重启清理预算 |

只验证真实变更路径；使用受控调用时刻或现有测试 harness，不靠实等一小时证明累计上限删除。表中均为计划，尚未执行。

### 8.2 实机/系统级

未执行。实现后用同一设备/模式做最小必要检查：

1. 无并发更新时核对 start 首观察发布对应的五分钟期限；通过实际 MCP 请求设置长租约，再调用普通观察，确认返回/查询的截止时间未被截短；再显式缩短和默认重设，确认按服务端当前时间覆盖。对照 operation_id、session.origin、started_at、expires_at，确认续租保持原会话归属。
2. 检查单次时长 120 的截止时间跨过 `started_at+1h`；跨累计一小时的授权语义由受控时间单测证明，不把这一现场检查写成已经实等一小时。
3. 用实际一分钟租期，在期满前检查活动、期满后检查 `lease_expired` 与原虚拟屏清理事实；未知物理收尾如实记 `unconfirmed`。
4. 设置长租约后主动 STOP，确认仍立即撤权；不因长租约延后 STOP。
5. 发布必要检查按同一构建清单、共同契约、工具目录指纹完成；不扩展无关设备或功能矩阵。

验收以实际 `session.expires_at`、`session.state/reason`、工具字段透传和分层 `cleanup` 为准，不以无错误响应替代全部结论。

### 8.3 记录

实现后写入 `maintenance/verification/explicit-keepalive-duration.md`，记录实现提交、环境、实际命令与结果；分别列出实现、单测、系统/实机事实及未执行原因。此次只有静态取证与提案，不生成验证通过结论。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论与来源 |
| --- | --- |
| start 租期 | 用户补充指定五分钟；本提案在初始化、激活和首观察发布使用五分钟，其他普通续期仍两分钟 |
| 显式范围、默认值、起算与覆盖 | 用户指定 1～120 分钟、默认 2、从 now 计时、可反复立即更新且可缩短 |
| keepalive 返回值 | 用户要求说明；本提案沿用 operation_id/outcome/session，明确 expires_at 及失败/重放分支，不新增时长回显字段 |
| 普通续期与显式时长的关系 | 用户指定比较剩余租期与普通续期时长取最大值；不继承显式时长 |
| 累计硬上限 | 用户指定取消 |
| 契约/工具描述重点 | 用户要求特别说明微妙语义；§6.5 给出可评审文案 |
| 参数名与存在语义 | 本提案建议 `duration_minutes`，省略默认而非 0/null 默认；用户可在整体确认时复核 |
| 单一截止时间、锁内应用顺序、到期不可复活、P 幂等 | 沿用现有权威和终态边界；锁内取 now 是保证本提案起算定义的实施约束 |
| SDK 形状与版本 | 本提案建议保留默认方法并新增专用含时长方法；共同契约拟 7→8，具体发行号按实现时发布规范确定 |

### 9.2 未决

无悬空的行为分支。提案整体及上述建议细节由用户在实现开始前确认；此次不把讨论方向或文案审阅当作已经授权实现。

## 10. 实施顺序

1. 用户确认提案；按仓库要求核对并先以 docs-only 变更修订设计源头仓库的对应会话/接口规范，再开展本仓库实现。若当前环境无法定位设计源头，记录具体缺失，在该前置完成前不暗改实现语义。
2. 重核 HEAD、相关字段、触发点和共同版本；补充针对显式缩短与取消累计上限的反向对照用例。
3. 顺序完成 protocol → engine → SDK/MCP 接入，保持字段和行为完整贯通；运行真实变更路径单测及必要回归。
4. 实现具备后同步正式契约、架构、SDK/MCP 文档和版本生成产物；发起独立只读 docs 审计，修正并复检至 clean。
5. 按实际可用环境执行所涉系统/实机检查及发布必要验证，记录执行与未执行事实；提交实现时提案状态仍保持“提案，未实现”。
6. 实现提交落盘后再追加 §11、写实现 hash 和验证记录，移入 implemented 并重写链接；单独提交归档。

## 附：本提案不改变的事项

- 工具名仍为 `keepalive`，不新增 `keepactive` 别名或额外工具。
- O 的单向消费、基线确认、full/delta、输入实时重验、普通工作互斥和 STOP/end 顺序。
- 服务重启恢复、App 显示生命周期、原归属资源清理及最长五秒逻辑交接。
- MCP 取消包装、原有 P 重放与有界记录保留、认证身份和引用权限。
