# 提案：简化 text 写入确认并补齐错误详情与恢复指引

状态：提案，未实现。
基线提交：`70607aa901f00a23fd718602e899716b6bb32e5f`（分支 `master`，2026-11-18）。
适用范围：`act.operation.type=text` 的 ref/坐标目标、两种 `press_enter` 分支、错误结果、原 P 的查询与重放。
影响范围：`internal/engine`、`internal/android`、`internal/protocol`、`internal/mcp`、相关测试/探针、版本生成与正式文档；复用固定 uiautomator JAR。
契约（`/api/*` 字段与 `contract_version`）：改变。调整 text 写入完成证据，增加 text 错误详情枚举与终态恢复映射；请求形状、12 工具与 `text_input` 的公开适用范围不变。以本基线为准，实施目标为共同契约 18。

本文引用的函数名、行号、常量与默认值均以该基线提交为准。实现前代码若前进，须重新核对
`act.go`、`text_stage.go`、`recovery.go`、固定 JAR 哈希与 `release/versions.json`。
本提案没有获得实施确认，不改变现行规范。独立审查结论不代替用户对提案的确认。

---

## 0. 摘要

将 text 的写入成功判据收敛为：对唯一授权目标调用了一次写入原语，固定 JAR 在该调用中刷新同一节点并读到与请求完全相等的完整值，且 engine 最终提交门禁通过。
已聚焦 ref 跳过焦点点击；其他目标保留必要的一次聚焦及绑定。删除写前 GetText，以及为再次证明写入而进行的写后整树采集、重绑定和 GetText。
`accepted` 保留为 Android 回执事实，不单独否决完整读回证据；读回未知、值不符或回执不完整均不确认成功，也不重写文字。
补充受控 `error.details`，明确失败环节、原因和文字派发事实；运行中的 P 继续 status，已结束且仍需核对现场的 text 引导 observe。
`press_enter=false` 在写入证据提交后完成；默认/true 的 ENTER 保留独立授权、目标检查与阶段事实。

## 1. 问题

### 1.1 现象与代价

调用方反馈：显式 `press_enter=false` 时偶发文字已经进入输入框，结果却为
`outcome=uncertain`、`text_target_uncertain`，消息为“无法唯一绑定目标输入对象”。
现场根因尚未实机确认；这份提案解决已定位的结构性确认窗口，并完善不能确认时的表达。

分诊为**取舍类**：现行规范明确要求焦点点击、写前唯一性读、写后独立采集再绑定及精确读回，
实现与该规范基本一致。更换完成证据、减少环节及调整恢复入口须经过提案确认。
调查中的合成注入证明的是可达路径，不把它写成现场根因或先红缺陷用例。

### 1.2 证据链

| 基线落点 | 核对事实 |
| --- | --- |
| [act.go:979–1053](../../internal/engine/act.go#L979-L1053) | 两种 press_enter 都无条件点击聚焦，再采集/绑定，再 GetText，最后 SetText；false 仅跳过 ENTER/公开复合阶段 |
| [act.go:1065–1128](../../internal/engine/act.go#L1065-L1128) | SetText 的 `Value` 未用于完成判断；随后重新采集、绑定、GetText。候选数不为 1 立即返回 target uncertain，延迟循环只在值尚不相等/未知时继续 |
| [act.go:1166–1177](../../internal/engine/act.go#L1166-L1177) | 两种 text uncertain 构造器仅给 code/message，没有阶段或具体原因 |
| [text.go:81–135](../../internal/android/text.go#L81-L135)、[text.go:210–241](../../internal/android/text.go#L210-L241) | 固定 set 响应要求四成员齐全，返回 `CandidateCount/Value/Issued/Accepted`；正常解析后 receipt 为 terminated，传输/畸形响应不伪造结束回执 |
| 固定 JAR，SHA-256 `28361193DE3D13303543C13BDC7268C22EDECABFA4E68E6FBDFC35FFFE1406A3`；classes8.dex 的 DisplayBoundTextService 方法 | setTextOnDisplay 的指令偏移 0x0047 调用 performAction，0x004b 调用 readValue；readValue 的 0x0000 调用 refresh，0x0011 后读取 getText。候选计数在动作前取得，写后值来自同一节点刷新；refresh 失败返回 null，value 不是请求回显 |
| [固定制品说明](../../third_party/uiautomator/README.md#patch-contents) | 候选须在指定 display 且 focused/editable/visible/enabled/non-password，并精确匹配结构属性与可见 bounds；`issued` 与 `accepted` 分别表达调用发生和框架布尔回执 |
| [text_stage.go:19–61](../../internal/engine/text_stage.go#L19-L61)、[act.go:79–81](../../internal/engine/act.go#L79-L81) | 当前私有累计 text 阶段只在有效 press_enter=true 时初始化；false 的诊断/冻结若要使用派发事实，需要同步保存有界私有证据 |
| [recovery.go:96–98](../../internal/engine/recovery.go#L96-L98)、[engine.go:1200–1207](../../internal/engine/engine.go#L1200-L1207) | uncertain 默认指向 status；finished P 通常重放原恢复投影，因此不能指望重复 status 将冻结的 text 改判成功 |
| [errors.go:9–48](../../internal/protocol/errors.go#L9-L48)、[interface.md:428–444](../../docs/interface.md#L428-L444) | 统一存在 error.details，但 tap/long_press 的 reason 只适用其 ref 静态拒绝，不能直接套入 text |
| [text.go:235–239](../../internal/android/text.go#L235-L239)、[eventlog/log.go:63–98](../../internal/eventlog/log.go#L63-L98) | set 日志提供 issued/accepted，但两键不在事件字段白名单，会被过滤 |

本次只读调查运行了 text 相关定向测试，并通过未跟踪的 Go overlay 注入了：写后 RPC 候选数为
0/2、值已改变而 accepted=false、写后采集时焦点丢失。前三类得到 target uncertain，最后一类
得到 result uncertain；输入一次、ENTER 零次。该结果仅作机制证据。
调查制品在被忽略的 `build/investigation-text-uncertain/`，不作为提案读者或构建的前置依赖。
实施测试须用仓库内的合成夹具重建证据，固定 JAR 行为用该哈希的真实制品验证。

### 1.3 为什么直觉会出错

树采集与实时 RPC 不是同一快照。对写后输入框重新按 bounds/focus 匹配，会把文字已经写入后
发生的动画、布局或焦点变化引入完成判断。延长整条链还会增加新的变化窗口。
另一方面，只看 `accepted=true`、树中出现某段文字或请求回显，也不足以证明目标框全文等于请求。
本方案采用一次写入调用中**同一候选节点刷新后的实际值**，没有删除目标授权或精确比较。

## 2. 目标与非目标

目标：

- 明确写入成功所需的最小证据，并减少获取这些证据的往返和焦点干扰。
- 所有路径至多一次 SetText；同一 P 不因错误、查询或重放再次派发。
- agent 无须解析中文消息，即可区分目标问题、写入未知、完整值不符与 ENTER 问题。
- 区分 running 与 finished P 的恢复步骤；查询冻结事实不会形成无限 status 指引。
- 使 false 分支的首次结果、冻结、status 和重复 P 拥有一致的文字派发证据。

非目标：取消所有必要聚焦、对非 editable 对象强行输入、放宽密码保护、修改字符串或证明业务提交。
不引入 OCR、剪贴板、IME 或 shell 输入替代路径，不新增 caller 可调等待参数，不修改固定 JAR。
不修改普通 tap/long_press/scroll 的执行与恢复语义。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 保持现行流程，只补详情 | 原有独立采集/读回全部保留 | 实施量小，确认往返与重复聚焦不减少 | 已写入后的匹配失效继续发生 |
| R2 在原链增加有界重采集 | 对候选 0 等再采集、绑定、读回 | 更多等待与状态分支 | 持续重排/失焦仍无法确认，扩大环境变化窗口 |
| **R3 本提案** | 利用固定 set 的同节点刷新值；仅在必要时聚焦；补详情与恢复 | 改完成判据、累计事实与调用说明；要求实机验证即时读回 | 异步值尚未可读或节点被重建时仍可能 uncertain |
| R4 改造固定 JAR | 新 RPC 整合定位/写入/等待读取，或支持未聚焦对象 | 新固定制品、构建与兼容性成本；仍须保留 engine 授权 | 控件兼容性及设备端复合动作可能扩大 STOP 边界 |

选择 R3：已有固定制品提供实际读回，无须通过更多设备动作取得重复证据。
R4 不属于本提案；若 R3 的实测暴露不能接受的即时读取限制，须另提固定制品变更，不能在实施中补入隐藏重试。

## 4. 判据

### 4.1 目标与聚焦

保留静态检查、O 的认领、前台/显示重验、ref 匹配与坐标原点证明。
只有 ref 在认领后的新鲜帧中已经满足安全输入条件且 `HasFocused && Focused`，并通过下述完整绑定门禁，才跳过焦点 tap：

```text
bindFocusedText(obs.Projection.Capture, fresh, frame.raw,
                obs.Foreground, frame.x, frame.y)
```

从成功 binding 的 raw 生成 set target，不直接从 frame.raw 生成。
该入口重验 focus 过滤前的锚点唯一性、严格同 TaskID、属性 presence 与几何；失败后仅走既有一次 focus
tap/afterTap 绑定路径，该路径也不能绕过唯一性与任务检查。未聚焦/焦点未知 ref 及所有坐标 text 同样走该路径。
坐标路径不因附近存在一个输入框而静默变成 ref 授权。

保留 package/class、resource/description 的 presence/值、enabled/password/display/任务上下文
以及既有 exact/near/shifted 判据；本提案不把它们放宽。跳过 tap 后到 SetText 之间若失焦，
JAR 的候选检查拒绝写入，engine 不补 tap、不补第二次 SetText。

### 4.2 写入完成证据

一次 SetText 的写入完成候选必须同时满足：

1. RPC 无错误，结构完整且来自声明的固定制品；`Receipt=terminated`。
2. `CandidateCount=1` 且 `Issued=true`，说明对本次结构授权唯一匹配的节点调用了写入原语。
3. `Value != nil`，且其完整字符串与 `operation.text` **精确相等**。
4. engine 在提交时仍确认原 session/work/P、显示 ID 与 generation、STOP/终态和期限有效。

`Value` 来自固定 JAR 对该候选节点执行后的 refresh/getText，不是 tree 中的 text、请求回显或
之后的公共 observation。比较保留空格、换行、Unicode 与空字符串；不 trim、归一化或按包含关系判定。
空框的 hint 按固定 helper 的 `isShowingHintText` 行为返回空串；null 永远表示未知。

`Accepted` 作为布尔回执记录，不单独否决上述证据。即便原值早已相等，仍要求一次已派发的
SetText 和实际刷新值；不新增“无需派发即 executed”路径。
候选成立后在同一状态锁中提交 `write=executed`；false 同时提交 `KnownOutcome=executed`，
避免 STOP/冻结落在两个独立提交之间。锁内不做字符串比较、RPC 或树计算。

删除写前 GetText，以及写后的 Capture→重绑定→GetText/延迟循环。保留工具用于展示页面的后观察；
后观察不是写入完成证据，也不会推翻已提交的 write。

### 4.3 未知、未派发与畸形回执

| 本次 set 证据 | 文字派发事实 | 写入阶段 | 后续 |
| --- | --- | --- | --- |
| 尚未申请写入派发许可 | not_attempted | not_executed | 不读回、不重发 |
| 已申请最终许可但被拒，或明确 `not_dispatched` | not_dispatched | not_executed | 不读回、不重发 |
| 完整有效响应，候选数 0 或大于 1，且 issued/accepted=false、value=null | not_dispatched | not_executed | 目标未确认；不补点击/写入 |
| 唯一候选且 issued=true，value=null | dispatched | uncertain | readback_unavailable |
| 唯一候选且 issued=true，value 非 null 但不相等 | dispatched | uncertain | readback_mismatch |
| 传输错误、无可用结束回执或响应畸形 | unknown | uncertain | 保留未知，不用后观察改判 |

固定制品 set 正常形状只有“非唯一则不调用”与“唯一则调用”两种。负候选数、唯一却 issued=false、
非唯一却 issued=true/accepted=true/value 非 null 等矛盾组合视为畸形；不据此确认没有写入。
Android 解码层须校验这些组合，不能把缺字段/畸形响应转换成 0、false 或空串。

整体 not_executed 还要求焦点 tap 与 ENTER 也均尚未尝试或明确未派发。
仅焦点 tap 已派发而 Set 尚未申请许可时，整体仍 uncertain，详情为 `dispatch.write=not_attempted`；
已经申请许可后被拒，或完整 set 响应证明未调用写原语时，才为 `dispatch.write=not_dispatched`。

Tap/ENTER 使用 shell 回执，与 set 的 issued 事实分开处理：

| Tap/ENTER 回执 | 对应 dispatch | 对应原语确认状态 | 结果 |
| --- | --- | --- | --- |
| terminated 且 error=nil | dispatched | executed | focus 继续绑定；ENTER 可按复合门禁提交成功 |
| terminated 且 error 非 nil（如非零退出） | dispatched | uncertain | focus_unconfirmed / enter_unconfirmed，整体 uncertain；不再次派发 |
| not_dispatched，无论是否同时返回 error | not_dispatched | not_executed | focus_not_sent / enter_not_sent，整体按累计事实计算 |
| unknown，或无法取得可用回执 | unknown | uncertain | focus_unconfirmed / enter_unconfirmed，整体 uncertain |

terminated 只证明调用已经结束，不能在 error 非 nil 时当作成功；同时它也不能被改写成 unknown 派发。

### 4.4 ENTER 的独立边界

false 分支在 §4.2 提交后完成，零 ENTER、无公开 text_input，后续失焦/位移不改变该写入事实。

默认/true 仍返回 `text_input:{write,enter}`。write 提交后，发送 ENTER 前做一次只读前台核对和
一次严格 `GetTextOnDisplay`：原任务上下文仍一致，Get 候选扫描时原 target 唯一且 focused，
随后同节点刷新读取的实际值仍精确等于请求。Get 的刷新步骤不保证焦点在之后保持不变。
Get 使用本次 set 的结构 target，不在失败后重新点按、移动匹配或重写文字。
不能确认时保持 `write=executed,enter=not_executed`，整体 uncertain，详情 `stage=enter`。

只读核对通过后，ENTER 继续经过 beginTextEnter 与最终独立派发门禁，至多一次。
ENTER 不是 set 的一部分；读回后到按键之间仍有正常 Android 环境变化窗口，不声称原子性。
ENTER 回执未知或 terminated+error 保留 `(executed,uncertain)`，但 dispatch 分别为 unknown/dispatched；
明确未派发保留 `(executed,not_executed)`。
两键均提交才为整体 executed。ENTER 不证明搜索、提交或导航成功。

### 4.5 有界私有事实与冻结

所有已认领 text（包含 false）在 op 上保存有界 textStage：有效 press_enter、累计 focus/write/enter、
三个原语的 dispatch 事实、原 sessionEpoch/AppInstanceID/ModeGeneration/显示 ID 与 generation 的不可变绑定、
currentStage、最后已提交失败原因及可选候选数。绑定在认领时从原 session/work 固定，不从后来的会话快照猜测。
各 dispatch 初始为 not_attempted；在各自 may_dispatch 的同一短锁内改为 unknown；
仅及时完整回执或明确未通过最终授权，能改为 dispatched 或 not_dispatched。
跳过的 focus 保持 not_attempted。false 不公开 TextInput，true 保持既有五种组合。

currentStage 在原 op 的短锁内更新，初始为 target，并按以下边界记录：

| 边界 | currentStage | 与派发事实的关系 |
| --- | --- | --- |
| 认领完成、开始目标重验/无 tap 绑定 | target | 三原语尚未尝试 |
| 进入必要 focus 许可申请，直至 afterTap 绑定结束 | focus | may_dispatch 同锁更新 dispatch.focus；采集/绑定不会产生新原语 |
| 进入 write 许可申请，直至 set RPC 返回并取得有效回执 | write | may_dispatch 同锁更新 dispatch.write |
| set 完整有效且唯一已 issued，开始比较实际 Value | verify | 及时提交 dispatched 和 currentStage=verify；不在锁内比较正文 |
| write=executed 后，进入 ENTER 只读门禁及许可申请 | enter | 门禁前 enter 为 not_attempted；ENTER may_dispatch 同锁改 unknown |

所有阶段更新都重验同一 work/P/会话/显示与期限，不更新已冻结 op。写入候选计算完后的一次成功提交
同时写入该阶段的最终事实；若冻结先发生，currentStage 保持最后及时记录的环节。
冻结直接读 currentStage，用 authorization_revoked 或 deadline_exceeded 作为 reason，
不需要先出现一次失败，也不解析 work.Phase。主动作完成后进入后观察，沿用 §4.5 的成功冻结规则，
不因 currentStage 停在 verify/enter 而伪造该环节错误。

该记录是 engine 原操作事实，不是另一份授权。错误详情只从冻结的同一份事实投影，不从消息或
work.Phase 推断派发；状态/重复 P 不重新运行目标算法。静态 text 能力拒绝可直接给出未派发事实，
不需要先认领 O。

STOP 先撤权、取消；设备调用和刷盘不能阻塞 STOP。超过既有逻辑交接上限后冻结原 P，晚到回执只能
按现行规则关闭物理窗口，不能补写 P、详情、text_input、KnownOutcome 或公共观察。
冻结须区分 work 截止与原 session 撤销；写入尚未确认时给相应原因。
若 false 的 KnownOutcome 已为 executed，或 true 两键已确认，冻结保留 executed：
原会话撤销按 executed_revoked 表达；仍活动但后观察失败按 post_observation_failed 表达，不再制造 text 写入错误。

### 4.6 text 错误详情

仅 text 专属的目标/聚焦/写入/确认/ENTER 错误，以及 text 执行中的撤销/截止，增加以下核心组。
严格 JSON 解析、认证、busy、会话/O 接纳等公共错误保留各自契约，不伪造 text 设备事实。
tap/long_press 的 reason 枚举和作用域不被继承。

| 字段 | 类型与取值 | presence 与语义 |
| --- | --- | --- |
| stage | target / focus / write / verify / enter | 核心组固定出现；是失败的逻辑环节，verify 可在 set RPC 内完成，不代表额外动作 |
| reason | 下表封闭字符串枚举 | 核心组固定出现；agent 可依赖该枚举，不解析 message |
| dispatch | 固定 focus/write/enter 三键对象；各值为 not_attempted / not_dispatched / dispatched / unknown | 核心组固定出现；分别表达各原语是否调用，不等同于内容确认成功或整体 outcome；跳过聚焦、false 的 ENTER 为 not_attempted |
| candidate_count | 非负整数 | 仅本次失败环节获得有效候选计数时出现；0 保留；未知省略，不用写前计数代替 ENTER 检查计数 |

| stage | reason | 顶层 code / HTTP | 含义 |
| --- | --- | --- | --- |
| target | target_not_editable / target_protected | text_target_uncertain / 422 | 既有静态输入能力/保护拒绝；未认领，文字未派发 |
| target | target_unconfirmed | target_changed 等原重验 code / 原 HTTP | 原目标无法由新鲜证据确认；不声称被删除或被遮挡 |
| focus | focus_unconfirmed | text_target_uncertain 或 action_result_uncertain / 409 或 504，按下面规则 | tap 已有终止回执但 afterTap 采集/绑定失败用前者；tap 回执未知用后者 |
| focus | focus_not_sent | device_unavailable / 503 | focus 明确未派发；dispatch.focus=not_dispatched，整体按累计事实 |
| write | no_unique_target | text_target_uncertain / 409 | 有完整 set 响应且候选不唯一；candidate_count 可为 0；dispatch.write=not_dispatched |
| write | dispatch_not_sent | device_unavailable / 503 | 写入原语确定未派发；整体按累计原语计算 |
| write | receipt_unknown / rpc_result_invalid | text_result_uncertain / 409 | set 调用无完整可靠回执，派发事实 unknown |
| verify | readback_unavailable / readback_mismatch | text_result_uncertain / 409 | 已派发，原节点刷新不可读或完整值不等 |
| enter | enter_target_unconfirmed / enter_context_unconfirmed | text_target_uncertain / 409 | write 已确认，但 ENTER 目标或前台无法确认；ENTER 未派发 |
| enter | enter_not_sent / enter_unconfirmed | action_result_uncertain / 504 | ENTER 明确未派发对应 E,N；回执未知或 terminated+error 对应 E,U，dispatch 按 §4.3 分别表达 |
| 当前已记录环节 | authorization_revoked / deadline_exceeded | control_revoked 或 text_result_uncertain / 409 | STOP/归属撤销用前者；仅工作期限到但原会话仍活动用后者；outcome 按累计事实 |

`target_unconfirmed` 只附加在能够明确归属 text 目标重验的错误；前台/设备等公共错误原 code 与详情保留。
focus 的 terminated+error 与 unknown 均为 action_result_uncertain / 504、reason=focus_unconfirmed；
terminated+nil 后 afterTap 采集/绑定失败才为 text_target_uncertain / 409。不能把 shell 非零退出当作未知派发。
同一个错误不混用 tap 的 reason 与 text reason。已知阶段中，撤销/截止 reason 优先于尚未发生的读回失败。
dispatch 区分尚未申请原语许可和已经取得“确定未派发”的结论；例如 focus=dispatched、write=not_attempted、
enter=not_attempted 明确解释“只点击过，整体 uncertain，尚未写字”。静态拒绝三键均为 not_attempted。
message 为对应原因的固定中文短句，不嵌入请求值、实际值、原始异常或 RPC 响应。
不增加 expected/actual text、文本 hash/前缀、完整节点、截图、猜测的遮挡 ref、retryable 或第二套 next_action。

以下为拟议片段，不代表基线已有该响应。只读回值不符时，调用方能知道文字原语已经调用，
并直接取得新观察入口；没有正文泄露，也没有承诺可安全原样重试：

```json
{
  "operation_id": "PtextExample",
  "outcome": "uncertain",
  "error": {
    "code": "text_result_uncertain",
    "message": "目标输入框的完整内容与请求不一致",
    "details": {
      "stage": "verify",
      "reason": "readback_mismatch",
      "dispatch": {"focus": "not_attempted", "write": "dispatched", "enter": "not_attempted"}
    }
  },
  "recovery": {"entry": "observe", "reference": "P", "input_state": "retired"}
}
```

### 4.7 recovery 配套

所有投影继续只由 `planRecovery` 和现有 durability/live-session 覆盖规则产生。
增加来自原 op 的 action 类型、终态与原会话可用性事实作为纯 planner 输入；不读取 details、不执行设备 I/O。

| 原操作事实 | recovery | 调用方动作 |
| --- | --- | --- |
| running text，结果尚未封闭 | status/P，cause=running（无 error 时） | 查询该 operation_id；不派发第二份输入 |
| text 静态拒绝，O 未认领且仍可用 | retry/O，input_state=effective | 修正目标/参数后以新 P 使用仍有效的 O；不是原请求自动重放 |
| 重验拒绝且交付新 O | 首次 retry/O；查询/重放依既有 delivered 降级为 observe/P | 首次使用交付的 observation.id；不能从无树的重放结果推造新 O |
| finished text 的写入/ENTER uncertain；原会话仍活动、无更高优先级阻塞 | **observe/P，input_state=retired** | 新 P 做一次 observe，查看完整值/页面再决定；不循环 status、不原样重发 |
| claimed 后未派发任何原语但目标等条件不再成立 | observe/P，input_state=retired | 获取新 O 后重新决策 |
| 主动作 executed，后观察失败 | observe/P，cause=post_observation_failed，无 error | 仅补观察，不重写/重按 |
| 原会话已撤销/结束/过期 | 沿用会话终态/收尾投影，input_state=revoked | 不给 observe/retry 授权；pending cleanup 仍 status，终态入口按现行规则 |

本提案为已认领 text 的首次终态、finished status(P) 和重复 P 新增同一个只读 live-context 投影；
仅在有恢复事项时应用（uncertain、claimed 拒绝、后观察失败、执行后撤销等）。完整成功且无恢复事项
的结果不因随后另一工作占位而凭空新增恢复；未认领的静态拒绝/公共接纳错误沿用现有投影。
现有 status(P) 尚未具备全部这些覆盖，实施必须补齐，不能只修改文案。
在 e.mu 内从当前事实构造纯 planner 输入，传入同一次采样的 now/比较结果，不在 planner 读取时钟：

- 原会话用 op.SessionOrigin 查找；只有当前 e.session 与 textStage 的 origin/epoch/不可变绑定仍匹配、State=active，且
  now 严格早于 ExpiresAt 才可 observe。快照或已退休会话不能证明活动授权；即使 housekeeping 尚未运行也检查过期。
- cleanup 只读该原 origin 的当前 cleanup；不读取其他 origin 的旧 cleanup 作为阻塞。
- 耐久读 ledgerUnsafe、blockedReason 的账本阻塞与当前 UI closed-but-undurable；恢复读 recovering/recovery。
- 设备准备读 deviceReady；控制同步/交互条件按 appFreshLocked/appReadyLocked 的已知事实判定。
- busy 从当前 e.work 和有界 shellBarrierLocked(now) 推导；忽略此原操作自身尚未 release 的 work，
  也忽略已越过既有退休交接上限、不应再占普通位置的旧 work。

先据原会话可用性计算 input_state（原已 claim 为 retired，原会话失效为 revoked），然后按下表从上到下选一个入口：

| 优先级 | 锁内事实 | live recovery / 辅助字段 |
| --- | --- | --- |
| 1 | 账本/当前回执耐久阻塞 | status/P，保留已计算 input_state，blocking_code=ledger_unavailable 或 ledger_corrupt |
| 2 | 原会话撤销、结束、过期、不再是当前会话或绑定已失效 | 原 cleanup pending 为 status/P；否则 end/P；input_state=revoked；blocking_code 为 control_revoked/session_expired/session_ended 中有证据的值 |
| 3 | recovering 或 recovery 记录仍在处理 | status/P，blocking_code=recovering |
| 4 | deviceReady=false，或当前 App 同步未知/过期 | status/P，blocking_code=device_unavailable 或 control_unavailable |
| 5 | appReadyLocked 已知 stop/mode/电源/锁屏/显示条件需用户处理 | user/none，blocking_code 为该 helper 返回的受控原因 |
| 6 | 另一个当前普通 work 或有界 shell 确实占位 | status/P，blocking_code=device_busy；同 agent 时返回 blocking_operation_id，否则省略该 ID |
| 7 | 无上述阻塞 | 使用原 text 语义终态投影：uncertain 为 observe/P；成功/静态拒绝等仍按主表 |

running P 保持 status/P，不因该表提前宣称终结。对于优先级 6，caller 有 blocking_operation_id 时跟踪该 P，
没有时可继续查询原 P 观察占位条件变化；这不是等待原语义 uncertain 翻转。
上述 blocking_code/blocking_operation_id 是本次已有 Result 辅助字段的实时投影，不修改原 error、详情、outcome
或 op.Recovery；阻塞解除后恢复原 observe 等入口。
finishOperation 只在 op.Recovery 保存一次基础语义投影；first/status/duplicate 共用投影入口，在结果副本上依次
做 delivered 重放降级和 live-context/耐久覆盖。须将当前写回 op.Recovery 的耐久覆盖移到该只读投影层，
否则临时阻塞会污染保存的 observe 基础入口。planner 仍是唯一 recovery 构造点。
身份/期限判定不会由只读 status 续租或改写会话历史；发现 ExpiresAt 已到时，以 blocking_code=session_expired
和 input_state=revoked 表达当前授权失效，不篡改原 P 的历史会话快照。
错误结果不附 recovery.cause；无错误的 pending cleanup/running/降级成功才附相应已有 cause。

物理收尾未知本身不新增普通准入屏障；只把已完成的语义 uncertain 当作 status 必然会变化是错误的。
text 专用终态分支放在通用 uncertain→status 之前，并保证不改变其他动作。
认领过的原 O 永远不可 retry；表中的 delivered 例外只允许对已经交付的新 O 给 retry/O。
`recovery.cause` 继续只用于无 error 的运行中/降级成功，不把 reason 塞入 cause。

finished P 的首次结果、status(P)、重复 P 复制同一详情和语义终态；不重新读设备。
仅允许既有的 delivered 重放降级及上述只读 live-context 高优先级覆盖改变恢复入口。
例如原 P 返回后会话被 STOP，status(P) 应按其原 session 的最新撤销事实覆盖 observe，不能沿用旧的活动会话建议。

agent 获得新观察后：目标值完整相等即可基于当前页面继续任务，但不回写原 uncertain；
值不符则根据任务决定纠正输入；`write=executed,enter=not_executed` 可在新 O 上按需要单独 ENTER；
enter=uncertain 先检查业务页面，不自动重复 ENTER。

## 5. 算法

```text
register P and record action=text
static admission -> claim O -> init private textStage for both press_enter branches
revalidate target using current engine rules

if ref and focused known true and full bindFocusedText(original O, fresh, frame.raw) succeeds:
    target = textTargetOf(successful binding.raw)
else:
    authorize one focus tap -> record its receipt
    capture once -> bind target under current rules
    fail honestly if focus evidence unavailable; never repeat tap

authorize one SetText -> dispatch.write becomes unknown atomically with may_dispatch
set = SetTextOnDisplay(target, request.text)   # fixed helper writes once and refreshes same node
record physical receipt independently
validate response and derive candidate outside lock
commit timely dispatch facts / error OR exact write evidence under original ownership gate
    false: commit write executed and KnownOutcome executed together
    true: commit write executed, leave enter not_executed

if effective press_enter:
    probe original foreground/task and GetText once against the original set target
    if target/context/value unconfirmed: finish E,N without dispatching ENTER
    authorize and dispatch one ENTER; commit receipt and composite outcome

if main action executed: existing post-observation acquisition/publication
finish atomically with matching details + pure recovery plan
```

所有等待受原 work/session 期限和取消约束，不新增 text 延迟、循环、写入重试或外部队列。
无完整 set 结果时不进入“观察后再确认原动作”的分支。比较/解码得到的候选不是公共状态，
STOP 或冻结先获胜就丢弃候选；物理回执仍按独立规则收尾。

## 6. 实现方案

### 6.1 接口与文件

| 文件/模块 | 改动 |
| --- | --- |
| internal/engine/act.go | 聚焦分支、删除重复读回、采用 set.Value、ENTER 前目标门禁、受控错误构造 |
| internal/engine/text_stage.go / engine.go | 私有累计事实覆盖 false；三原语 dispatch 与 press_enter；false 成功原子提交；公开 text_input 仍只 true |
| internal/engine/ui_execution.go / retirement.go / operations.go | 派发、明确未派发、冻结与迟到回执的同源事实提交；错误详情与 outcome 一致 |
| internal/engine/recovery.go / status.go | text 终态 observe、原会话撤销覆盖，保持纯 planner 与其他动作行为 |
| internal/android/text.go | 严格校验 set 组合及 ENTER 用 get 的计数/值 presence；沿用原 RPC，不自动重试 |
| internal/protocol/errors.go / decode_response.go | 扩展详情白名单与类型/枚举说明；保留通用深拷贝与显式零值 |
| internal/mcp/schema.go / result_text.go / 投影测试 | Caller Instructions、tool 描述、详情三模式保真、取消包装不污染原错误 |
| internal/eventlog/log.go / internal/android/text.go | 将 issued/accepted 纳入批准布尔字段；增加有界 text 诊断事件 |
| android-control/test-target 与相关探针 | 仅为实际需要的输入/焦点 fixture 和真实制品验证增加测试支持 |
| release/versions.json、生成脚本输出与 manifest/probe | 协调契约/版本/指纹，实施时执行，不在提案阶段修改 |

### 6.2 配置与取值

- SetText 上限 1，必要 focus tap 上限 1，ENTER 上限 1；失败后不重新启动原语。
- false 额外确认 Get/Capture 为 0；true 追加一次前台核对和一次 Get 作为 ENTER 门禁。
- 无 text 专属延迟和轮询预算，沿用 work/session 与单 RPC 超时；STOP 逻辑交接仍最长 5 秒。
- 详情阶段/原因均为封闭枚举，candidate_count 非负整数且仅已知时出现。
- 基线版本为 0.17.0 / contract 17 / Android versionCode 19；实施拟升级为 0.18.0 / 18 / 20。
  只编辑 release/versions.json，再生成 Go/Java/XML；若实施基线已推进，先重定版本目标。

### 6.3 日志与可观测性

保留 android.text.set_done 的 count/issued/accepted/ms，批准 issued/accepted 两个布尔键。
增加一条 engine.text.finished 事件，字段仅 operation_id、stage、reason、focus_dispatch、write_dispatch、enter_dispatch、outcome、
可选 count；stage/reason 只在 text 错误存在时出现，成功事件省略二者，不发明 none/success 原因。
新增键必须加入白名单，值取上述闭集。不记录输入/实际值、长度、hash、节点或原始响应。
主动作已确认但后观察失败按既有事件表达，不写成 readback_mismatch。
日志事件不决定 outcome，不等待日志入队/落盘，不进入恢复账本。错误详情先从 op 快照产生，再投影日志。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威 | JAR 只返回本次原语事实；没有跨请求输入归属或恢复决策 |
| STOP 不等待、迟到不发布 | 候选按原门禁提交；不在设备端合并 focus/ENTER 等额外写入 |
| O 单向消费、P 至多一次 | 复用既有认领/幂等；观察恢复不回收 O、不重派发 P |
| 锁内有限内存操作 | 比较、解码、前台与 RPC 全在锁外；锁内只提交已算候选 |
| 执行/观察/收尾分层 | set 内同节点实时读回作为本次完成证据；后观察不改判，收尾未知不伪造准入屏障 |
| 字段省略与显式零值 | count=0 必须保留，未知省略；value=null 不当成空字符串 |
| 消息与日志白名单 | 没有文本正文、猜测原因或原始错误进入详情/事件/账本 |

### 6.5 契约与文档影响

实施时同步 docs/interface.md 的 text 流程、完成证据、详情与 recovery；docs/architecture.md 的累计阶段/
冻结；docs/mcp.md、docs/integration.md、docs/client.md 的消费说明；docs/runbook.md 的受控诊断。
扩展 caller 指南与工具描述会改变工具目录指纹，发布必须经 manifest 核对，并重启实际 MCP 进程。
共同契约递增，App 因共同版本生成而重建；不新增 App 行为、配对或数据迁移。
固定 JAR 哈希不变。HTTP/MCP/客户端的 error.details 都须保真，不能只在可读文本里增加建议。
提案阶段不修改 docs；实施提交前按 AGENTS.md 进行独立只读 docs 审计。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 避免完整写入后因再次定位输入框而产生的确认失败。
- 已聚焦 ref 不重复点击，减少键盘/光标/布局受输入影响的机会。
- 完整读回一致时不再被 accepted=false 单独否决。
- 未确认时可区分文字未派发、已派发但值不符、回执未知和 ENTER 未完成。
- 原 P 已结束时，直接引导获取新现场证据。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 控件异步提交，set 内 refresh 暂时返回旧值/null | 本方案没有等待/重试；仍返回 uncertain 并给新观察入口 |
| 控件立即重建，原 AccessibilityNodeInfo 无法 refresh | 无同节点完整读回证据，不能用其他框相同文字替代 |
| 应用对输入做格式化、截断或过滤 | 完整值不相等仍不确认；不修改调用方目标值 |
| 输入框只在屏幕上显示一部分文字 | 判据为可访问节点的完整 value，非像素可见性/OCR 或滚动到全文 |
| 页面在读回后再次修改值、第三方同时操作 | 只证明本次采样时的值，不证明持久性或因果排他性 |
| 完全相同观测签名的对象替换、window 身份不可辨识 | 现有结构身份不是永久对象 ID；不声称消除该边界 |
| 默认 ENTER 因写后位移/失焦无法确认目标 | 写入仍 E，ENTER N；不通过补点按恢复焦点 |
| 传输超时但实际文字已写入 | 缺完整结束回执仍未知；后观察不改写原 P |
| 搜索/提交/导航未达预期 | text 完成事实不等于业务结果 |

### 7.3 成本

以首轮读回即可成功、忽略后观察为口径，基线每次 text 为 1 tap、3 次树采集（含重验）、2 Get、1 Set。
false 的已聚焦 ref 为 0 tap、1 次树采集、0 Get、1 Set；其他 false 为 1 tap、2 次树采集、0 Get、1 Set。
true 在同一写入路径上增加一次前台核对、1 Get 和成功时 1 ENTER。
Capture 内部不止一个设备往返，以上只统计逻辑调用，不能当作时延实测。
减少的是重复确认成本；ENTER 门禁失败会更明确地保留 E,N，而非替调用方再次聚焦。
每个 op 新增有界枚举/布尔/可选计数，记录随现有 P 容量淘汰，不保留 text 正文。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 已聚焦 ref、false、set.Value 精确相等 | executed；0 tap / 1 Set / 0 Get；不进入写后整树确认；无 text_input |
| 已聚焦但重复 anchor，或 TaskID 缺失/变化 | 不走无 tap 快路径；既有聚焦/绑定也不得绕过该安全门禁；不错误写入 |
| 未聚焦 ref / 坐标 false | 至多一次 tap，一次 afterTap 绑定；1 Set / 0 Get |
| 唯一已派发、accepted=false、完整值相等 | executed；与 accepted=true 使用同一提交门禁 |
| value=null / 不相等 / 空串 / hint / 空白换行 / 非 BMP | 未知与不一致分开；空串与完整 Unicode 按精确值处理 |
| 候选 0/多、未 issued | 明确文字未派发；已 focus 时整体 uncertain，未 focus 时整体 not_executed；计数 0 不省略 |
| 缺字段/类型错误/矛盾成员 | receipt unknown 与 rpc_result_invalid，不误报未派发/成功 |
| 写入候选就绪后 STOP/显示代次变化/冻结 | 不提交迟到成功，不发 ENTER，不发布迟到观察 |
| false 写入成功提交与冻结竞争 | write 与 KnownOutcome 原子一致；后观察失败/撤销保留 executed |
| true：ENTER 前失焦、位移、值变化、任务变化或 Get 失败 | write E、enter N、整体 uncertain；不补 tap/set，详情区分 target/context |
| ENTER 未派发/回执未知/成功 | 分别 E,N / E,U / E,E，原语上限不变 |
| Focus / ENTER 为 terminated+error | dispatch 为 dispatched，确认状态 uncertain；分别 focus_unconfirmed / enter_unconfirmed，不重复原语 |
| Focus / ENTER 为 not_dispatched+error | dispatch=not_dispatched，原语 N；focus_not_sent / enter_not_sent，整体按累计事实 |
| STOP 落在 target/focus/write/verify/enter 各阶段 | 使用已及时记录的 currentStage，撤销/截止原因优先，不依赖 work.Phase |
| focus 已派发但 Set 尚未申请许可 / 最终许可被拒 | 整体 uncertain，dispatch.write 分别为 not_attempted / not_dispatched，false/true 一致 |
| text finished uncertain / running / 静态拒绝 | 分别 observe/P / status/P / retry/O；新 P 与旧 O 的用法明确 |
| finished 后 STOP/过期/换会话、ledger unsafe、cleanup pending | 高优先级投影覆盖；过期不依赖 housekeeping；不得继续指向可用 observe/retry |
| finished 后 device unavailable/recovering/busy/App 交互阻塞及解除 | 按 live-context 优先级投影 status/user 与 blocking 字段；解除后回到 observe；不改写原 op.Recovery |
| 其他 origin 的 cleanup_unconfirmed | 不成为本 text 所属活动会话的准入屏障，不制造 status 循环 |
| status(P)、重复 P、冻结与取消包装 | 详情/text_input 深拷贝、终态不变，无第二次设备输入；MCP 取消扩展不污染原错误 |
| HTTP→client→MCP structured/text/both | stage/reason/dispatch 三原语/count=0 与省略语义一致；新 JSON 字段不丢失 |
| 日志/账本隐私 | 只包含批准 ID/枚举/布尔/计数；无请求/读回正文和原始异常 |
| 非 text 动作恢复回归 | 原有 tap/long_press/scroll/shell/app 恢复表不改变 |

实施时在对应包添加合成夹具，**先证明基线失败**：聚焦 ref 0 tap、accepted=false+实际完整值应成功、
写后再匹配失败不应否定 set 内已取得的值、finished text 应 observe 等均应在基线变红。
再实现并运行这些用例；不把旧 overlay 的“当前行为为 uncertain”测试当成先红修复证明。
对 set 内 actual value 使用真实固定制品探针验证，不能仅以 fakeDevice 回显输入作为证明。

### 8.2 实机/系统级

本提案编写阶段未操作设备。实施后的实机验证限定本次 text 路径，使用本仓库测试 App/受控合成值：

1. 验证固定 JAR 的唯一匹配、refresh/getText、空框 hint、清空/多行/非 BMP 的真实返回值；记录制品哈希。
2. 聚焦/未聚焦 ref、坐标与 false 分支，逐项核对完整值、键盘/焦点状态及输入原语计数。
3. true 的正常 ENTER、受控焦点切换、任务/布局变化，核对 E,N/E,U/E,E 与业务页面分别表达。
4. 用受控 fixture 提供同步、异步更新与立即替换控件各一组，报告即时读回不符/不可读比例；不把它们写成“全部修复”。
5. 正常稳定场景每种明确用例重复 10 次，逐次记录成功/失败枚举；预期稳定同步输入 10/10 完整相等，
   异步/重建场景按实际结果报告。重复次数用于发现时序问题，不宣称统计证明。
6. STOP/迟到回执以确定性注入为主；实机无法精确注入的窗口如实注明，不扩大设备/应用矩阵。

验收：每个 P Set≤1、focus tap≤1、false ENTER=0、true ENTER≤1；明确目标不唯一时不写入；
稳定同步值相等用例无多余确认失败；已知失败详情与真实分支一致；无其他阻塞的 finished uncertain 直接给 observe，
无需先查询同一个已结束 P；不承诺一次 observe 必然成功，也不要求业务必然成功或将原 uncertain 改判。

### 8.3 记录

实施与验证记录写入 `maintenance/verification/text-write-verification-and-recovery.md`，注明代码提交、
Go 1.27/jsonv2 构建、实际命令、固定 JAR 哈希、环境/设备、已单测/已实测与未执行项原因。
Windows 测试只支持开发检查，不作为 Linux 运行账本耐久证据。
本次提案审查过程另记于 maintenance，不能冒充实施/实机验证。

## 9. 已定与未决

### 9.1 已定

以下为本提案提出供用户整体审阅的确定选择，不表示已获得实施确认。

| 事项 | 结论 |
| --- | --- |
| 路线 | R3，复用固定 set 的同节点实时读回，不修改 JAR |
| 输入范围 | 只跳过已聚焦 ref 的 tap；坐标不扩大授权 |
| 完成判断 | 完整回执、唯一已调用、刷新值精确相等、原归属提交门禁；accepted 非独立否决 |
| 默认 ENTER | 保留，增加明确的独立目标/上下文门禁，失败保留已确认 write |
| 诊断 | stage/reason/dispatch 核心三字段与可选计数；dispatch 包含三原语事实；无正文、retryable 或第二套恢复动作 |
| 恢复 | running/status，finished uncertain/observe，高优先级终态/耐久/阻塞继续覆盖 |
| 参数与预算 | 不新增参数、轮询或隐式重试；上限与时限见 §6.2 |
| 版本 | 实施拟共同升级 0.18.0 / contract 18 / Android 20，版本仅修改唯一源 |
| 文档边界 | 当前只写 maintenance；用户确认后实施并同步 docs，经独立只读文档审计 |

### 9.2 未决

没有交给实施阶段临时决定的算法/枚举/预算选项。
用户在本提案审阅时决定是否接受整套完成判据与恢复变化；未确认前不实施。
异步控件的兼容性是 §8.2 的实测门槛：如结果不能接受，实施者须携真实证据回提案讨论，
由用户确认修改方向；不得自行加等待、换 JAR 或恢复旧的多轮确认。

## 10. 实施顺序

1. 用户确认提案；若基线前进，先重新核对证据与版本目标。
2. 把写入证据、派发/冻结、详情与 recovery 场景写成合成测试，记录基线先红；准备真实固定制品探针。
3. 修改 Android 组合校验、engine 私有事实与原子提交；实施 text 主路径及 ENTER 门禁。
4. 完成同源错误详情与 planner/status 配套，保持通用恢复优先级；同步 MCP 指南与投影/取消测试。
5. 运行所涉定向测试/反向对照/必要回归，执行 §8.2，依据真实结果决定能否按本提案验收。
6. 同步正式 docs、生成共同版本并组装/核验 manifest；发布部署只在另外获得该操作授权时执行。
7. 独立只读 docs 审计，修正并复检 clean；先提交实现，提案状态保持“提案，未实现”。
8. 实现提交存在后补 §11、验证记录和审查记录，移入 implemented 并重写链接，单独提交归档。

## 11. 实现结果（归档时补写）

尚无实现提交。本节保留模板位置，确认和实现前不填写已落地结论或虚构 hash。
归档时按维护流程补实现提交与验证记录链接，并重写本文件全部相对链接。

### 11.1 已落地

无。本提案尚未实施。

### 11.2 与提案不同或未执行

无实施偏离可记录。实机验证、版本生成、发布、部署与 docs 修改均未执行，因为当前仅获授权起草和审查提案。

---

## 附：本提案不改变的事项

- 12 工具、请求字段、presence-aware 默认值、全文替换语义与 4096 Unicode 码点上限。
- O 单向消费、P 去重、单工作位、严格 JSON 接入与原会话/显示绑定。
- false 不公开 text_input；默认/true 的五种合法阶段组合。
- 密码保护、原始树/文本隐私、日志白名单与消息存储边界。
- 后观察、业务结果、会话终态、物理窗口/收尾事实分别表达。
- 固定 JAR 字节与哈希、运行目录锁、有限退休交接和耐久门禁。
