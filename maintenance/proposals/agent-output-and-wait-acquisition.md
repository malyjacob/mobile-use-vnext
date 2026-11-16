# 提案：压缩 Agent 输出并复用 `wait_for` 已采样终帧

状态：提案，未实现。
基线提交：`4fe270de7cd979cba9659a6662677b111028d4c6`（分支 `master`，`2026-11-17`）。
适用范围：`device-mcp` 默认结果模式、MCP `server/discover`/兼容 `initialize` 返回的 instructions 与固定工具目录描述、`wait_for` 的 timeout/max_samples 终帧选择，以及 #21 落地后的 full/delta 策略取证。
影响范围：`cmd/device-mcp`、`internal/mcp`、`internal/engine`、相关测试与探针、MCP/架构/接口/发布文档和最终共同发布生成物。
契约（`/api/*` 字段与 `contract_version`）：不增加、删除或改名 `/api/*` 字段；默认结果投影、MCP 公共说明、目录文本和 `wait_for` 终帧时序改变。两批只做一次正式发布，共同将 `contract_version` 从 `15` 升到 `16`。

本文函数名、行号、常量和默认值均以基线提交为准；实现前若 HEAD 前进，须重新核对 `cmd/device-mcp/main.go`、`internal/mcp/server.go`、`internal/mcp/schema.go`、`internal/mcp/catalog.go`、`internal/engine/wait_for.go`、`internal/engine/capture.go` 与 `docs/mcp.md`。

---

## 0. 摘要

本提案处理 Agent 视角下三类直接成本：`both` 默认投影重复交付同一结果、MCP 公共说明重复且描述混入内部算法、`wait_for` 已取得最新成功帧后仍可能重读设备。

第一批共同目标是 #21 默认 text、#16 instructions/描述瘦身和 #22 终帧复用；三包可独立开发，#21 与 #22 无互赖，#20 仅在 #21 的目标 text 模式下取证。第一批内部顺序是推荐实施顺序，不等于所有工作必须串行。

#20 不预先承诺 `6/50`：跨度和比例分开评估，未达到门槛就保留 `12/70/180s`，且不阻塞第二份提案。第一批完成验证与 #20 保留/调整结论后，按用户选择进入 [agent-reference-and-action-contract.md](agent-reference-and-action-contract.md)；该分批阶段是验收门禁，不是技术硬依赖。

#16、#21、#22 与两批最终只形成一个正式契约、目录指纹和 manifest 发布，不发布中间态。本文不修改任何实现、`docs/`、版本源或清单。

## 1. 问题

分诊：#16、#20、#21、#22 均为取舍。当前实现与现行文档相符；本提案改变表示策略或终帧时序，不把调用成本问题当作已证明的实现缺陷。来源分别为 [#16](https://github.com/malyjacob/mobile-use-vnext/issues/16)、[#20](https://github.com/malyjacob/mobile-use-vnext/issues/20)、[#21](https://github.com/malyjacob/mobile-use-vnext/issues/21)、[#22](https://github.com/malyjacob/mobile-use-vnext/issues/22)。

### 1.1 现象与代价

- 未显式配置时，`device-mcp` 返回 `both`：同一 Result 同时投影为完整文本和 structuredContent。若宿主把两通道都交给模型，带树观察会进入两份等价事实；具体 Token 增量取决于宿主和 tokenizer。
- 同一 `treeTextGuide` 被拼进六个工具描述；宿主若不读取 server instructions，只能读取六份重复内容，而 `act`、`keepalive` 等描述又夹带内部算法，稀释调用时机、参数约束和恢复动作。
- `wait_for` 的条件轮询已经取得成功投影帧；timeout 或 max_samples 时当前路径丢弃候选并重读设备。慢 dump 下额外 acquisition 可触发外部宿主超时，MCP 取消随后会发起 end(P)。
- delta 允许同一会话在 11 次发布内继续引用旧基线。调用方历史 delta 不会因新 delta “覆盖”而从上下文删除，过长跨度和较高比例可能增加重建距离；更频繁 full 也可能增加当前 token 与延迟，因此必须取证。

### 1.2 证据链

- `cmd/device-mcp/main.go:106-123` 在无 flag、无环境变量时返回 `ResultModeBoth`；`internal/mcp/result_mode.go:12-19` 是三值 enum，`internal/mcp/result_projection.go:12-36` 显示 both 同时生成文本与 structured，text 仍保留完整文本和图片。
- `internal/mcp/schema.go:16-40` 定义公共 tree guide，`:54`、`:89`、`:131`、`:206`、`:336`、`:439` 六处重复拼接；`internal/mcp/server.go:58-63` 当前没有 `Instructions`。
- `internal/mcp/catalog.go:40-75` 的目录指纹只覆盖工具数、有序名称、描述和输入 schema，不覆盖 server instructions；说明迁移后指纹格式不变，但必须单独验证初始化说明。
- `internal/engine/wait_for.go:17-24` 固定 `waitCaptureReserve=35s`；`:86-119` 用 timeout 截止轮询后另建验证采集上下文；`:296-320` 每轮已执行 Capture 与只读 Preview。
- `internal/engine/wait_for.go:190-199`、`:282-288` 在 timeout/max_samples 退出时返回 nil 候选；`:364-371` 随即 captureTree。带图时 `:336-357` 仍先探测显示并读取图片。
- `docs/interface.md:1221-1229` 明确规定命中帧复用、到时或采样上限重读终帧；`:1255-1259` 明确最坏预算为 `timeout+35s=75s`。本提案保留该上限，timeout 仍只表示轮询窗口。
- `internal/engine/wait_for.go:462-520` 当前 `reused` 布尔只描述命中候选；直接把最后采样塞入该参数会把 timeout/max_samples 回退伪装成 condition/stable，因此必须区分来源。
- `internal/engine/capture.go:258-276` 当前以同会话、`revision-baseline.Revision < 12`、基线龄 `<180s` 与严格 `<70%` 编码比选择 delta；`:279-296` 在唯一提交门禁重验会话、工作归属、显示代次和 capture 龄。
- `internal/engine/act.go:373-381`、`internal/engine/observe.go:42-48`、`internal/engine/operations.go:355-360` 表明 full O 只有后续认领或读取确认后才成为基线；自动发出 full 不等于立即换基线。
- `maintenance/decisions.md:102-110` 与 `docs/architecture.md:321-323` 将 `12/180s/70%` 记录为既有取舍；现有结构 benchmark 最大 delta/full 比仅 `7.361%`（`maintenance/verification/observation-continuity-benchmarks.md:263,273`），不能证明收紧到 45%–50% 对真实页面有收益。
- `docs/release.md:46-47` 规定默认行为、核心枚举和执行语义变化也要推进 contract_version。本提案虽不新增 `/api/*` 字段，仍改变默认结果模式、公共 MCP 文本和 wait 终帧时序。
- `docs/interface.md:118-119` 把 `observe` 的 `recovery.reference=P` 写成“下一个请求的新幂等键，不能复用本结果的
  `operation_id`”，与同文 `:293-303` 的 `observe(P)` 语义冲突。实际实现先在
  `internal/engine/observe.go:21-35` 独立登记本次调用的新 P，再在 `:105-143` 用
  `req.Reference` 解析已有 O/P；P 分支要求该 P 属于当前 Agent 且原会话就是当前活动会话，并强制 full。
  `internal/mcp/server.go:554-565` 也证明 MCP 原样保留 body `reference`，另以
  `client.WithOperationID` 传入本次新 P。未来文档同步必须把二者明确分开，不能把这条文档消歧扩大为
  第二份提案的字段或工具变更。

Issue 关系与采纳：

| Issue | 关系 | 本提案边界 |
| --- | --- | --- |
| #16 Catalog 去重 | 第一批；描述风格先定 | 完整 guide、O/P、租期总则移入 instructions；六描述去重并保留恢复契约；start 留短兼容提示；不假定真实宿主读取 instructions；指纹格式不变，另验说明。 |
| #17 统一 reference | 第二批共享迁移 | 由第二份提案决定；本文不改 `/api/*` 字段或兼容别名。 |
| #18 app 简化/拆分 | 第二批工具面演进 | 用户选择保留 12 工具、单 app 扁平 Schema，并统一 open.target.app；查询 P 和取消语义保持。 |
| #19 祖先回退 | 第二批动作契约 | 由第二份提案决定字段、默认值和证明边界；本文不改 act 行为。 |
| #20 delta 策略 | 第一批证据评估；数值待确认 | #21 text 为取证硬依赖；分别评估跨度和比例。基线 `12/70`，候选可含 `6` 或 `4`、`50` 或 `45`，`180s` 不动；不达标保留 `12/70`，不阻塞第二份提案。 |
| #21 默认 text | 第一批 | 只改 configuredResultMode 无配置 fallback；保留 enum 零值、显式三模式、header、图片和事实等价。 |
| #22 wait 终帧复用 | 第一批；可独立并行 | 仅复用最新成功、无后续失败且两次 freshness 都通过的原始 capture；不改 35 秒预算、timeout 含义、队列或重试。 |
| #23 text 提交建议 | 第二批动作契约 | 用户选择 press_enter，明确只按一次 ENTER；第二份提案定义阶段事实，本文不改 act schema/执行流。 |

### 1.3 为什么直觉会出错

“默认 text”改变公开默认行为，“无字段变化”也不能推出“契约语义不变”。“复用最后一帧”若只看非空，会发布设备读取已失败后的旧成功帧；直接塞入 candidate 又会破坏 quality。缩短 delta 跨度可能减少重建距离，也可能输出更大 full 并增加当前 token、序列化时间与宿主超时风险，必须按完整任务轨迹判定。

## 2. 目标与非目标

目标：

- 无 flag、环境变量或请求头选中模式时，stdio/Streamable HTTP 使用 text。
- 完整公共说明集中到 server instructions，六工具描述只保留调用方决策所需内容。
- 对最新轮询尝试成功、没有后续失败且仍在 `CaptureMaxAge=15s` 内的帧，避免 timeout/max_samples 的第二次设备层级读取。
- 保持新 O、旧 ready O 退休、quality、trigger、matched、图片/树关系及统一提交门禁准确。
- 在 #21 text 模式为 #20 提供可复核的聚合证据与保留/调整判据。

非目标：

- 不缩短 35 秒验证预留，不把 timeout 解释成整次工具调用硬上限，不承诺复用后耗时严格等于 timeout。
- 不绕过 STOP、end、租约、工作期限或显示代次检查，不增加公开调参字段、普通队列、失败重试或跨调用缓存。
- 不采用 #17/#18/#19/#23 的字段、工具拆分或动作时序；不把原始树、截图、日志、输入正文或宿主原始响应写入仓库。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持 both、六份 guide、timeout 终帧重读 | 现状稳定 | 上下文与慢设备尾采集成本持续；#20 没有目标模式证据。 |
| R2 只改 MCP 输出 | 只做 #16/#21 | 改动面小 | 宿主超时与 #22 收益未验证。 |
| R3 只改 wait | 保留 both/重复描述，仅复用采样帧 | 时序优化直接 | 默认双份 payload 抵消部分 token 收益；quality 来源易错。 |
| R4 第一批组合、给 #20 设门禁 | #21/#16/#22 并行开发；#20 等 #21 后分开测跨度和比例 | 需要协调三包与一次共同发布 | 门禁不严会把推测数值写成契约；本提案以保留现值和后续确认消除。 |
| R5 立即实施全部八 issue | 一次改完工具、动作和协议字段 | 决策面最大 | 多个契约变化偶合，无法独立归因验证失败。 |

选择 R4。#21、#16、#22 是独立工作包，#20 只硬依赖 #21 的 text 证据环境。动作/引用契约在第二份提案单独评审；第一阶段验收后进入第二阶段是用户选择，不是技术调用依赖。

## 4. 判据

### 4.1 默认结果模式

1. `-result-mode` 优先于环境变量，两者均未提供时进程 fallback 为 text；HTTP 请求头仍覆盖该次调用的进程默认模式。
2. `ResultModeBoth` 仍是 enum 零值和可显式选择值；三模式解析、header 严格性和错误码不变。
3. `both`、`text`、`structured` 的 canonical Result 事实相等；图片仍由同一 ImageContent 通道传递。

### 4.2 公共说明与描述

1. instructions 恰好包含一次完整 tree guide、O/P 引用总则和租期总则。
2. start/observe/wait_for/act/inspect/app 的描述不再内联 guide，但保留参数约束、常见错误码和恢复动作。
3. act 的内部落点/Tier/绑定算法与 keepalive 的内部续期比较/历史去重细节移回正式文档；act 保留重验失败后用新 O 重试等恢复契约，keepalive 保留 duration_minutes 范围、默认值、显式续期可缩短租期、错误与恢复动作。
4. start 加一句短兼容提示：宿主未提供 server instructions 时，O 是可消费观察、P 是操作；full 是完整树，delta 只相对其 base 且新 delta 覆盖旧 delta。
5. 目录指纹继续覆盖名称、描述和 schema；instructions 必须另验正式 server/discover 响应，兼容 initialize 作为附加检查，不改变指纹 JSON 形状，也不把宿主一定展示作为前提。
6. A 阶段只能发布与现行 Schema/执行流一致的说明：`act`/`inspect`/`app` 新输入仍使用各自现行字段，
   不得提前把请求字段统一为 `reference`，也不得出现 `bubble`、`press_enter` 或“text 默认发送
   ENTER”；`recovery.reference` 是已有响应字段，不属于这项禁用。A 文案必须在 B 落地时按
   §4.2.1 的替换项更新后才能继续使用。
7. 所有面向调用方的文案以 caller-agent 为读者。每句话至少帮助调用方选择工具、填写参数、
   解释结果或选择下一步恢复；不服务这四类判断的内容放入工程文档。
8. `instructions` 只写跨工具共同规则；全部 12 个工具的 description 按需写独有用途、关键参数/
   默认、结果或动作边界和最少恢复路径；schema 已清楚表达的类型、范围和默认值不重复成段说明，只补常见混淆及
   动作不证明业务效果的边界。
9. engine 锁/账本、Tier/匹配评分、落点枚举、内部函数名、RPC 与其他实现细节不进入公共文案。
   文案不追求覆盖全部错误码或算法，只保留会改变下一次调用的信息。

#### 4.2.1 #16 Instructions 候选与 A/B 边界

下面是同一份公共调用指引候选正文。实际 Instructions 由“既有完整 treeTextGuide + 此正文”
组装，完整树语法只保留一份；此处不把简述当作完整图例。正文先服务当前 12 工具与
`observation_id`/`text` 行为；B 落地后按紧随其后的替换项更新同一份正文。

```text
会话与推进：首次可直接 start({})；已有 active 会话时沿用，只有会话状态不明才 status({})。
active/starting 或 can_start=false 时不要 start；会话失效先按 recovery 处理，满足启动条件后
再新建。有可用 O 时直接 act；act 或 wait_for 返回新 O 时直接
用该 observation.id。只有没有可用 O、需要查看外部变化，或 recovery.entry=observe 时才 observe。

O 是一次观察，act 会消费 ready O，并以 observation.actionable_until 判断期限。P 是调用的
幂等/查询编号，每次新工作调用另有新 P；app 的 find 和 notify 的 P 可能没有会话。
工具 reference 指向已有 O/P。start 使用五分钟租期；普通活动在续期点只增不减，至少续至
两分钟后；status、inspect、app 查询不续租。keepalive 可重设并可缩短租期，但不延长 O。

选择工具：status 查状态；observe 新采集；wait_for 等条件；act 消费 O；inspect 查历史详细树；
app 管应用生命周期；shell 执行命令且可能改屏；diagnose 查健康。wait_for 到时是正常返回，
检查 wait.trigger、wait.matched 后用返回的 O 继续；observation.quality=unstable/unknown
本身不阻止输入，不要仅为追求 stable 重复 observe。

text 会把目标框全文设为 text，不追加旧内容；普通输入无需先 tap。该调用会聚焦、写入并精确
读回，但不发送 ENTER。需要提交时，用 text 返回的新 O 另行调用 act，并设
operation.type=key、operation.key=ENTER；结果 uncertain 时不要盲重发。

恢复：retry/O 表示尚未派发，改正参数后用同一工具重试；结果带新 observation.id 就用它，
否则用原 O。observe/P 的 body reference 用已有 O/P 所指活动会话的 P（常见为 session.origin，
也可用所属 operation_id）；本次新调用另生新 P。status/P 按目标取 ID：当前动作用 operation_id，
busy 用 blocking_operation_id，自身恢复用 start_operation_id，收尾用 target_reference 或刚查询的
P；status(O) 撤销时对同一 O 调 end。end/reference=P 用拥有目标会话的 P；user 按提示处理
阻塞原因，update 需配套升级，两者不传引用。

not_executed 表示没有派发，参数错误可直接更正。uncertain 表示可能已派发：先 status；running
时继续跟踪；finished 后仍不确定且会话活动时，用 observe 查看现状，再决定新动作；不要原样重发，
也不要自动 end/restart。executed 表示已确认派发；后观察、图片或清理失败不触发重打。device_busy
时跟随 blocking_operation_id 串行。任务完成后正常 end，pending cleanup 按 recovery 查询；
不要把普通 422、busy 或 uncertain 当成废弃整个会话。
```

B 落地后，保留上述 O/P、租期、树、恢复与会话规则，只把动作字段和两段输入行为更新如下；这就是
B 的最终默认指引，不复制第二份全文：

- `act` 的 `observation_id` 改为 `reference`；`inspect` 与 `app` 变更 kind 的同类输入同步使用
  `reference`，响应 observation id、`target.ref` 与 `recovery.reference=O/P/none` 不变。
- `tap`/`long_press` 的 ref 目标使用 `operation.bubble`，默认 `true`。原目标已有对应动作时直接执行；
  原目标没有动作时，可交给最近交互祖先处理一次；该祖先不支持本动作时按恢复指引调整调用，
  不继续向更远祖先查找。`bubble=false`
  只允许直接目标，坐标目标和其他动作不带该字段。旧名 `allow_ancestor_fallback` 不得出现。
- `text` 使用 `operation.press_enter`，默认 `true`。省略时一次调用完成聚焦、把目标框全文设为 text、
  精确读回，并在读回成功后至多发送一次 ENTER；普通输入无需先 tap，也无需另发 ENTER。
  仅填字、草稿、多行编辑、逐字段填写、清空但不提交时显式写 `press_enter=false`，此时只写入。
- `text_input.enter=uncertain` 时先观察当前页面是否已完成目标，再决定新动作，不仅凭 unknown
  原样重发 ENTER；`enter=not_executed` 且 `write=executed`、原会话仍活动时，取新 O 后可按需
  单独调用 `operation.type=key, operation.key=ENTER`，不必重新填字。按键执行与业务目标是否
  达成分别判断，业务结果由后观察中的具体页面事实确认。

### 4.3 wait 终帧来源

轮询结果必须有三种来源：`matched`、`terminal_reuse`、`terminal_capture`。terminal_reuse 同时满足：

1. 存在最新成功 Capture+Preview 帧；
2. 该帧是最后一次轮询尝试，之后没有慢读、device 错误、Preview/摘要失败或显示探针失败；
3. 选择时和 publishTreeAttempt 提交时，`now-capture.CapturedAt <= cfg.CaptureMaxAge`；
4. 轮询停止原因是冻结的 timeout/max_samples，没有 STOP/end、租约到期、工作丢失或显示 ID/generation 变化。

纯 sleep、从未成功、最后一次成功后又有失败、选择时已过期，均使用 terminal_capture。选择后因显示探测、图片或投影耗时而在提交门禁过期，则沿用 stale 错误且不发布新 O，不再发起第二轮终帧读取。终帧读取失败按现有失败/只读图语义返回，不发布更早已成功但不新鲜的帧。

### 4.4 #20 取证门禁

`180s` 不变，`12/70` 是默认保留值；可测跨度 `12/6/4`、比例 `70/50/45%`，但不预先承诺。两维必须分开测。

在相同模型、宿主、上下文保留策略与同类 10/20 轮任务下成对对比：任务成功率、delta 误用率、base 锚点距离、每轮/累计 payload、full 频率和 p95 输出/调用延迟。优先采用宿主/模型的实际任务输入与输出 Token 用量；只有 tokenizer 时明确计算范围，不把工具正文 Token 冒充含历史上下文的整任务成本。没有 Token 证据时只记录字节，不能据此宣称通过 Token 门槛。任何采用都要求任务质量不降、误用不增加；此外须满足累计 Token 不增且误用或距离改善，或累计 Token 下降。延迟显著回退也须先交由用户或维护者评估，不能仅凭单项收益自动采用。证据不足、质量下降或收益不稳定就保留 `12/70`。最终默认数值由用户或维护者修改前确认；#20 未调整不阻塞第二份提案。

## 5. 算法

### 5.1 启动结果模式

```text
if explicit -result-mode: use parsed value
else if env present: use parsed env value
else: ResultModeText
```

请求头仍覆盖本次进程默认值；三模式事实生成和图片附件路径不变。

### 5.2 公共说明迁移

```text
server.Instructions = complete tree guide + O/P rules + lease rules
six descriptions = tool-specific constraints + recovery, without tree guide
start.description += one short compatibility hint
```

### 5.3 wait 时间轴

```text
T0                     admission
T0..poll_deadline       timed polling; record lastFrame and latest-attempt status
poll exit               matched | timeout | max_samples | revoked/error
selection               matched -> matched frame
                        timeout/max_samples -> terminal_reuse if predicate passes, else nil
verification            ProbeDisplay, optional image; existing 35s reserve
tree preparation        projectCapture -> Candidate/commit closure; Preview never commits
commit                  publishTreeAttempt rechecks ownership/generation/age/revocation
publish                 new O; retire prior ready O
```

terminal_reuse 不新增设备 capture，不把同一帧第二次计入 samples；选择器失败不引入重试循环，直接沿用现有终帧读取一次。

### 5.4 quality 与 wait 事实

- wait.trigger 在验证采集前冻结；max_samples 在公开契约中仍投影为 `trigger=timeout`。
- wait.matched 在最终实际发布帧上计算；terminal_reuse 不改写冻结 trigger。
- matched 候选保持现行 condition/stable 口径。
- terminal_reuse 不追加样本；按 run 已包含的比较计算：连续达到稳定门槛时返回 stable/stable，否则 samples>=2 为 unstable、其余 unknown，stop reason 为冻结的 timeout/max_samples。
- terminal_capture 继续吸收新终帧并按 waitQualityOf 生成 quality。
- captured_at 保留实际使用帧的采集时间；O 的身份和 revision 仍由 engine 在提交时分配，旧 ready O 同步退休。elapsed_ms 仍从准入完成计至终帧发布，不由帧龄替代。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `cmd/device-mcp/main.go` | configuredResultMode 无配置 fallback 改为 ResultModeText。 |
| `cmd/device-mcp/main_test.go` | 默认 text；显式 flag/env 优先级和非法值测试保持。 |
| `internal/mcp/result_mode.go` | 不改 enum 零值、三值解析和 header；仅按测试需求补断言。 |
| `internal/mcp/server.go` | 设置 ServerOptions.Instructions；不持有会话权威。 |
| `internal/mcp/schema.go` | 公共 guide 从六描述移除；同步精简 keepalive 内部算法说明；保留约束/恢复；start 加短提示。 |
| `internal/mcp/schema_test.go`、`catalog_test.go`、`result_parity_test.go` | 验证去重、instructions、指纹语义和三模式事实等价。 |
| `scripts/mcp-probe/` | 必验 live server/discover.instructions；附加兼容 initialize.instructions 检查；目录指纹格式/比较逻辑不改。 |
| `internal/engine/wait_for.go` | 记录最新尝试/成功帧；引入来源类型；选择与提交双 freshness；分型 quality。 |
| `internal/engine/wait_for_test.go` | 覆盖复用、最新失败、过期、guard、image、STOP/代次和新 O/退休。 |
| `internal/engine/capture.go` | 所有来源继续经 publishTreeAttempt；只有 #20 通过时才另改阈值。 |
| `docs/mcp.md`、`docs/architecture.md`、`docs/interface.md` | 同步 text 默认、instructions/描述与 wait 终帧时空语义。 |
| `docs/integration.md`、`docs/runbook.md` | 同步手机/主机默认 text、覆盖方式、宿主 instructions 与完整调用预算说明。 |
| `docs/release.md`、`release/versions.json` | 只在两批完成后共同发布更新；本提案不修改。 |

### 6.2 配置与取值

- 结果模式默认 text；显式 both/structured 不变；不新增配置项。
- waitCaptureReserve=35s，CaptureMaxAge=15s，MinDwell=600ms 不变。复用不得绕过 dwell 或重复累计同一帧。
- #20 当前 `12/70/180s` 不变。任何 `4/6`、`45/50` 必须经过 §4.4 门禁和后续确认；不增加公开阈值/freshness/重试字段。

### 6.3 日志与可观测性

复用现有 result_mode、wait phase、samples、quality stop reason 和 duration。若增加终帧来源日志，只允许枚举 `matched|terminal_reuse|terminal_capture`；不记录树、capture 内容、输入正文、命令或原始响应。instructions 检查走测试/探针；#20 只保存聚合结果，原始材料不入库。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威 | MCP 只选表示、装配 instructions 和调用 client，不缓存会话、基线或 wait 帧。 |
| STOP 不等待 | 复用来源仅属于本次 work；STOP/end 先撤权并关闭 work，迟到选择不得提交。 |
| 提交条件化 | 所有来源都经 publishTreeAttempt，重验会话/epoch/owner/期限/显示 ID/generation/capture 龄。 |
| 单一工作位 | 不新增队列、重试池、后台采集或跨调用缓存。 |
| Preview 不提交 | 轮询 Preview 不改 ref continuity；terminal reuse 仍经 projectCapture 生成 commit。 |
| 事实分层 | tree 成功、image stale、执行和会话终态分别表达；new O/旧 O 退休不因复用改变。 |
| 严格接入 | `/api/*` 字段集合不变；result verbosity 不进入请求声明。 |
| 公共目录身份 | 描述/schema 变化更新目录指纹；instructions 另设初始化验证。 |

### 6.5 契约与文档影响

本提案改变默认行为和 wait 执行时序，但没有 `/api/*` 字段形状变化。按 `docs/release.md:46-47`，不能写成“契约语义不变”。实施期间不独立发布；两批全部完成并验证后只做一次正式发布，contract_version 从 15 升到 16。`release/versions.json` 仍是唯一手改版本源。

#16 实现提交同步 `docs/interface.md` 与 `docs/mcp.md` 时必须修正
`docs/interface.md:118-119`：`observe` 的 body `reference` 是已有 O/P 引用；当
`recovery.entry=observe, reference=P` 时使用拥有活动原会话的已有 P，常见为
`session.origin` 或所属 `operation_id`。MCP/HTTP 为这次新调用另生成 P，二者不是同一字段。
该结论须与现文 `:293-303` 和 `internal/engine/observe.go:21-35,105-143` 一致。B 未落地时，
docs 不写 `reference` 统一名、`bubble`、`press_enter` 或两个默认 `true`。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 默认调用不再向文本 Agent 重复交付 structured 树，程序化宿主仍可显式选择。
- 公共协议说明只出现一次，工具描述更聚焦调用决策，start 提供最小后备提示。
- 常见 timeout/max_samples 路径在最新成功帧仍新鲜时少一次设备层级读取。
- #20 从推测阈值改为可复核、可保留现值的目标模式证据决策。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| wait 总耗时一定不超过 timeout | 正式预算仍是 timeout+35s；显示探测、图片、投影和提交仍需时间。 |
| 宿主一定展示 instructions | 宿主可能忽略或不转发；只能验证并保留 start 短提示。 |
| 复用避免所有树算法 | 最新帧 Preview 后，发布仍需 Candidate/projectCapture 产生可提交投影。 |
| 旧历史 delta 自动消失 | 服务端不控制调用方历史；覆盖语义只用于重建。 |
| 收紧阈值必然降低 token | full 增加当前 payload；必须由 #20 门禁判断。 |
| 复用掩盖 device/display failure | 后续失败、过期、撤销和代次变化都会挡住旧帧提交。 |

### 7.3 成本

#21 要求程序化 structured 宿主显式配置；#16 把公共说明迁入正式 server/discover.instructions 并移除六次目录重复，兼容 initialize.instructions 仅作附加；#22 省一次层级 capture，但仍需显示探测、图片读取（若请求）、投影与提交；#20 需要任务回放和聚合统计，若保留现值则无生产策略变更成本。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 无 flag/env 启动 | text；Both 仍为零值且可显式选择。 |
| flag/env/header 优先级与非法值 | 精确优先级、大小写和重复值拒绝不变。 |
| 三模式 canonical Result+image | 事实等价；text 无 StructuredContent，但保留完整文本和图片。 |
| discovery 与兼容 initialization | 正式 server/discover 的 Instructions 非空，完整 guide/O/P/租期只出现一次；兼容 initialize 另验同源说明。 |
| 全部 12 个工具描述 | 按需说明用途、关键参数/默认、结果或动作边界和最少恢复；不重复公共规则和实现细节。六工具不含内联 guide，keepalive 无内部比较/去重算法，duration/default 与 start 短提示等必要语义保留。 |
| instructions 旁路验证 | CatalogFingerprint 格式不变；初始化说明另有确定性比较/语义检查。 |
| A instructions 边界 | 含一次完整 tree/O/P/租期说明；不出现用于 `act`/`inspect`/`app` 新输入的 `reference` 统一名、`bubble`、`press_enter` 或默认 ENTER；允许已有 `recovery.reference`。text 明确“全文设置”、内部聚焦与精确读回，提交仍需单独 key。 |
| B instructions 替换 | `act`/`inspect`/`app` 使用 `reference`；`bubble`、`press_enter` 的 Schema/default/Instructions 都为 true；不出现旧 `allow_ancestor_fallback`；普通 text 无额外 tap/ENTER，五类不提交场景显式 false。 |
| bubble 语义 | ref child 有对应动作时不提升；无动作时先找第一个交互祖先，它须支持本动作才提升，不支持也不越过；false 只走直接目标；坐标和其他动作拒绝该字段。 |
| text 语义 | 明确目标框接收完整值而非 append；普通输入省略 `press_enter` 后至多一次 ENTER；显式 false 零 ENTER；列全填字、草稿、多行编辑、逐字段填写和清空不提交五类场景。 |
| recovery 语义 | retry/O 使用结果新 `observation.id` 或原 O；observe 的 body P 指向已有活动会话，新调用 P 另生；status 覆盖 operation_id/blocking_operation_id/start_operation_id/target_reference 分支；end 覆盖目标会话 P 与 status(O) 原 O；`entry` 仅 retry/observe/status/end/user/update，反向断言无 start；非会话 P 不保证可恢复会话。 |
| normal-path 语义 | quality unstable/unknown 不禁止输入；wait timeout 正常继续；uncertain 先 status 再按需 observe，不自动 end/start；executed 不因后树/图片失败重打；start 与 end 条件按 §4.2.1 文案。 |
| timeout 最新成功、无后续失败 | 不新增 Capture；terminal_reuse；trigger/quality/new O 正确。 |
| max_samples 最新成功、无后续失败 | 复用；公开 trigger 仍 timeout，stop reason 依实际比较准确。 |
| 最新成功后有失败 | 不复用旧成功帧，执行一次 terminal_capture。 |
| 失败后又成功 | 可复用最后成功帧，不使用失败前旧帧。 |
| 纯 sleep 或从未成功 | 仍执行一次终帧采集。 |
| 选择时 15 秒内、提交时过期 | stale，不发布新 O。 |
| 反向对照：恢复 both fallback / 去掉 terminal_reuse | 无配置默认测试 / timeout 的 Capture 次数测试分别失败。 |
| STOP/end/owner/generation/期限变化 | 迟到复用不提交，已派发事实不伪称撤回。 |
| image 成功与 stale | tree 成功、image stale 独立为 partial。 |
| #20 反向对照 | 去掉候选判据后边界用例由绿变红；默认值明确保留或变更。 |

静态文案测试只断言必要公开契约：字段/默认/枚举、A 禁用的未来字段、tree guide 只出现一次、五类 text 非提交场景、恢复入口与 ID 选择、关键正常流。不得快照整段 Instructions/description、锁死句式/段落顺序，或用目录指纹代替语义断言。

反向对照至少证明：恢复旧 both fallback 后默认 text 用例失败；去掉 terminal_reuse 分支后 Capture 次数用例失败；去掉 latest-attempt/freshness 守卫后失败后旧帧或过期帧用例失败。#20 仅在实际修改策略时运行对应候选边界反向对照；保留现值时不新增虚构实现测试。

#### 8.1.1 文案体积预算

以变更前后同一 MCP 构建、同一 SDK 与同一 canonical 提取方式读取实际 `server/discover`：基线是 A 实施前当前代码，A 和最终 B 各自都与这份同一基线比较，不能把 A 的新体积当作 B 的基线。

- 总量：`instructions` 的完整 UTF-8 字节（必须包含完整 tree guide）加完整 `tools/list` 发布载荷的 UTF-8 字节。后者覆盖 12 个工具的固定名称/顺序、全部 descriptions 与 input schemas；只移入 Instructions 但未减少总量不算通过。
- 分项：分别记录 `instructions_utf8_bytes`、`tools_list_utf8_bytes`、12 个 description 的 UTF-8 小计与 12 个 input schema 的 UTF-8 小计。名称/顺序和完整 tools/list 载荷另做确定性断言，分项只解释变化来源。
- 门槛：A 总量严格小于当前基线，最终 B 总量也严格小于同一当前基线；不另设没有证据的固定 KiB 上限。预算不足时只精炼表达，不得删除关键功能、默认、恢复入口或 ID 选择。
- Token：只有宿主与基线和候选使用同一 tokenizer 时，才以相同文本边界另行记录真实 token 并注明 tokenizer 名称/版本；UTF-8 字节数不得称为 token。

### 8.2 实机/系统级

1. stdio 与 Streamable HTTP 验证默认 text、显式三模式、图片和 header；必验正式 server/discover.instructions，再查兼容 initialize；记录真实宿主是否展示，不把展示作为工具可用前提。
2. 慢 hierarchy 设备比较 timeout/max_samples 的 Capture 次数、p95、new O、quality、trigger 和旧 O 退休；验证 STOP/end 无迟到 O。
3. 运行目录指纹、release manifest 与 live catalog 比对；必验正式 server/discover.instructions，附加验证兼容 initialize.instructions；manifest tool_catalog 形状不变。
4. #20 在 #21 text 模式回放代表性长任务，聚合记录成功率、误用、锚点距离、每轮/累计 token（无 tokenizer 则只写字节）、full 频率和 p95。
5. Instructions 行为回放使用同一模型、宿主和固定任务，分别记录 A/B 的工具轨迹：首个 start 后直接 text，使用返回新 O 继续且不重复 observe；两阶段都无需提前 tap，A 搜索提交仍需一条独立 ENTER，B 默认 text 无额外 ENTER；B 的草稿/多行/逐字段/清空用 false，默认与 false bubble 的选择符合目标；quality unknown、wait timeout 不回退为 end/start。故障轨迹分别覆盖可更正的 422、blocking_operation_id、uncertain→status→observe、executed 后观察失败、detached P 和会话终态。断言工具顺序、参数、是否 end/start 与调用数，不把说明文本匹配冒充模型行为结论。

无代表性设备或真实宿主时明确记“未执行”，不能用单测替代实机结论。

### 8.3 记录

写入 `maintenance/verification/agent-output-and-wait-acquisition.md`，注明实现提交、构建方式、设备/宿主、实际命令和真实结果，并分别标明已实现、已单测、已实测。记录 A 与最终 B 各自相对同一当前基线的 §8.1.1 字节分项、总量及可选 tokenizer 结果。若 #20 未通过，记录保留 `12/70/180s` 与聚合证据，不写成第一批失败。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 工作包 | #16/#21/#22 为第一批，#20 同批条件评估；#17/#18/#19/#23 进入第二份提案。 |
| 默认模式 | 只改 device-mcp fallback；enum 零值、header、显式模式和图片路径保持。 |
| Instructions | 面向 caller 的跨工具完整 guide+O/P+租期进入 server instructions；description 保留工具独有用途、默认、约束和最少恢复；start 短提示；不假定宿主展示。 |
| 文案体积 | 以 A 实施前完整 `Instructions` + `tools/list` 的 UTF-8 为同一基线，A 与最终 B 的总量均须严格下降；分别记录各分项，可选 tokenizer 另记真实 token。 |
| 指纹 | 仍覆盖名称/描述/schema；格式不变；instructions 另验。 |
| wait timeout | timeout 仍是轮询窗口；35 秒验证预留与 timeout+35s 上限不变。 |
| wait 复用 | 只复用最新成功、无后续失败、两次门禁均在 15 秒内的原始 capture；其他回退现读。 |
| 树与 O | Preview 不提交；仍经 projectCapture/publishTreeAttempt 发布一个新 O 并退休旧 ready O。 |
| #20 | `12/70/180s` 暂为现值；180s 不动；候选不预先承诺。 |
| 发布 | A 完成验证与 #20 保留/调整结论后进入 B；两批完成后一次正式发布，共同 contract 15→16。 |

### 9.2 未决

- #20 是否修改跨度、比例或两者：由用户或维护者在 #21 落地后的聚合证据完成时决定；未达 §4.4 门槛就保留现值。任何默认修改须在实现提交前确认。
- 第二份提案的请求引用统一、单 app 扁平化、open.target.app、默认 bubble/press_enter 及显式 false 关闭方式见关联提案；两份必须共同评审，任何判据偏离均回到提案阶段确认，不以第一份替代第二份的具体契约。

## 10. 实施顺序

### 10.1 依赖类型

| 依赖 | 类型 | 约束 |
| --- | --- | --- |
| #21 → #20 取证 | **硬依赖** | #20 的 token/字节、full 频率和误用证据必须在 #21 目标 text 模式采集；此前只能研究，不能据此改默认。 |
| 第一份 → 第二份 | **用户选择的分批验收门禁** | A 完成验证和 #20 保留/调整结论后进入 B；不是第二份提案代码逻辑上的技术互斥。 |
| #16 → 第二份描述调整 | **推荐顺序** | 先确定 instructions/六描述风格，减少第二批再次改同一目录文本；不是逻辑硬依赖。 |
| #17 → #18/#19/#23 | **共享迁移实施顺序** | 因 request/Schema 命名迁移共享写集而先做 #17；这些能力逻辑上并不依赖字段必须叫 reference。 |
| #22 与 #16/#21 | **可并行** | 分包开发、独立测试；均不缓存 engine 权威，合并时只协调公共文档和发布。 |
| #19 与 #23 | **写集冲突管理** | 两者共享 `internal/engine/act.go` 等实现面；必须顺序合并，或先隔离清晰接口后再并行。 |
| 两批最终发布 | **唯一发布门禁** | 只形成一个正式 contract、目录指纹和 manifest 发布。 |

### 10.2 推荐顺序

1. **A1**：#21 与 #16、#22 可分包并行；#21 先建立 text 证据环境，#16 先定描述风格，#22 独立实现最新尝试/freshness/quality。
2. **A2**：#20 在 #21 完成后分别测跨度和比例；#22 不参与该依赖判断。
3. **A3**：完成第一批测试、实机/宿主验证和 #20 保留/调整记录；#20 不通过仍算第一阶段正常结论。
4. **A4**：按用户门禁进入 [agent-reference-and-action-contract.md](agent-reference-and-action-contract.md)。#16 已成为该批描述迁移的风格基准。
5. **B1**：先做 #17 的共享 request/Schema 迁移，再安排 #18/#19/#23；该顺序是减少冲突的推荐实施顺序，不声称能力逻辑依赖重命名。
6. **B2**：#19 与 #23 顺序合并；若改为并行，必须先隔离 act 请求接口、任务状态和测试写集。
7. **发布门禁**：两批功能路径通过验证后，在 `release/versions.json` 一次冻结共同新版本并生成 Go/Java/XML 常量，随后同步正式文档、完成相关检查及独立文档审计；实现提交和提案归档按仓库流程分开。最后从干净已提交源码构建、生成并核验唯一新契约/目录/产物 manifest；不在 A 或 B 中间发布，也不在清单核验后再改版本。

## 11. 实现结果（归档时补写）

归档时补写。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| 归档时填写 | 填写真实实现提交、文件与函数落点 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 归档时填写 | 包括 #20 保留现值、偏离和未执行验证的真实理由 |

---

## 附：本提案不改变的事项

- 不改变 `/api/*` 请求、响应或错误字段集合。
- 不改变 engine 的会话、O/P、租约、STOP、工作归属或唯一提交权威。
- 不改变 waitCaptureReserve=35s、MaxWaitTimeoutSeconds=40 或 CaptureMaxAge=15s。
- 不缩短或延长 timeout 的轮询含义，不新增普通队列、自动重试或跨调用缓存。
- 不改变 Preview 的只读连续性语义，不让采样帧绕过 Candidate/commit 与统一提交门禁。
- 不单独修改 release/versions.json；共同发布时才由唯一版本源升级。
- 不在本提案采用 #17/#18/#19/#23 的工具名、字段名、默认值或执行时序。
- 不把原始树、截图、日志、输入正文或宿主原始响应写入仓库。
