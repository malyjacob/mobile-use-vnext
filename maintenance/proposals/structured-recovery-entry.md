# 提案：用结构化 `recovery` 取代 `next`，作为唯一的恢复入口

状态：提案，未实现。
基线提交：`1a6edd5edc8886817e67be06e051c3c6d84c8e58`（分支 `master`，2026-11-09）。
适用范围：全部 `/api/*` 工具结果、HTTP 接入层错误与 MCP 文本投影；覆盖 `act`、`open_app`、
`observe`、`start`、`end`、`status`、`keepalive`、`apps`、`shell`、`inspect`、`notify`、
`diagnose`，以及控制同步响应里唯一一处 `contract_mismatch`
（[control.go:18](../../internal/engine/control.go#L18)，不是工具结果，只把 `update` 换成同一个
`recovery` 对象）。Android 控制 App 不解析工具结果，只随 `contract_version` 重建。
影响范围：`internal/protocol`（`Result`/`APIError`/编码/解码/错误表）、`internal/engine`
（`next` 写入点最集中）、`internal/httpapi`（接入层错误）、`internal/mcp`（文本投影与兜底）、
`docs/`（7 个文件）、`release/versions.json` 与 4 个生成产物、10 个测试文件。全仓可数落点：
50 处 `.WithNext(...)` 与 46 处 `.Next =`/`Next:` 写入（含 `NewError` 与 `WithNext` 自身实现
2 处），逐文件分布见 §6.1。
契约（`/api/*` 字段与 `contract_version`）：改变——删除 `result.next`；新增
`result.recovery{entry,reference,input_state,cause}`；`contract_version` `2` → `3`。
发行单位（不是契约）：`release_version` `0.4.0` → `0.4.1`（patch）、`android_version_code`
`3` → `4`；结论与理由见 §9.1。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的
`HEAD` 重新核对（重点：`internal/protocol/responses.go` 的 `Result`、`internal/protocol/errors.go`
的 `NewError`/`nextFor`、`internal/engine/` 下全部 `next` 写入点、`internal/mcp/render.go` 与
`internal/mcp/server.go`）。

---

## 0. 摘要

`next` 用一个枚举同时表达“操作未定”“观察作废”“会话结束”“需要人”“需要升级”五类互不相干的
事实，却只说入口、不说引用形态与旧观察是否还能用；它还有静态表与 50 处手写覆盖两个来源，静态表
里 3 个码已经没有对应常量、5 个真实码又不在表内，且恰好对最常见的“零成本重试”窗口不置一词。
本提案删除 `next`，以结构化对象
`recovery{entry,reference,input_state,cause}` 取代：`entry` 增加 `retry`，`reference` 区分
`P`/`O`，`input_state` 复用 `status` 既有词汇，`cause` 只在不带 `error` 的成功降级路径出现。
不保留 `next` 镜像，不做服务端决策，不引入自动重试。

## 1. 问题

### 1.1 现象与代价

1. **语义混轴**：`status`/`observe`/`end` 分指操作、观察、会话三种生命周期；`user`/`update`
   根本不是恢复入口，而是“外部前置条件不满足”。调用方拿到一个字符串，无法判断自己在恢复
   哪一层。
2. **信息不足**：只说入口，不说“带哪种引用”。`observe(P)` 与 `observe(O)` 是两条不同路径
   （[observe.go:104-131](../../internal/engine/observe.go#L104-L131)），文档只能用散文写“应改用
   `observe(P…)`”（[interface.md:108](../../docs/interface.md#L108)）。
3. **恰好漏掉最常见的窗口**：静态拒绝（`node_not_found`、`ambiguous_point`、`unsupported_key`、
   `invalid_coordinates` 等）的 `next` 为空，而这正是唯一“换一个 P、拿同一个 O 立刻重试”的
   零成本路径。测试已把这个空值写成断言（[act_point_test.go:294-295](../../internal/engine/act_point_test.go#L294-L295)），
   说明是设计而非疏漏。
4. **双源，而且已经漂移**：`nextFor` 静态表（[errors.go:58-79](../../internal/protocol/errors.go#L58-L79)）
   加 50 处手写 `.WithNext(...)`。表里的 `operation_running`、`observation_not_ready`、
   `tree_unavailable` 三个字符串已经没有对应 `Code*` 常量；反过来 `hierarchy_unavailable`、
   `display_unavailable`、`stale_screen`、`target_changed`、`foreground_changed` 不在表内，
   只能靠手写。可数落点：`grep -rn "\.WithNext(" internal/ | wc -l` = 50，
   `grep -rn "\.Next = \|Next: " internal/ | wc -l` = 46（其中 2 处是 `NewError` 与
   `WithNext` 自身实现，不是调用点）。
5. **手写覆盖会盖掉别的事实**：`revalidateActTarget` 会返回 `session_ended`/`control_revoked`
   （[act.go:279-285](../../internal/engine/act.go#L279-L285)），但调用点在设置 `Next` 时无条件
   写成 `observe`（[act.go:55-60](../../internal/engine/act.go#L55-L60)），于是“会话已撤销”被
   改写成“重新观察”。`start` 也有同类问题：首个观察失败触发撤销、会话已结束，响应仍写
   `next=observe`（[start.go:218-236](../../internal/engine/start.go#L218-L236)）。
6. **不可验证**：现有断言只比较字符串本身，不验证“按提示走下一步”是否可行
   （[image_test.go:545](../../internal/engine/image_test.go#L545)、
   [openapp_test.go:169](../../internal/engine/openapp_test.go#L169)）。`next=observe` 在会话
   已结束时照样指向一次必然失败。
7. **成功降级无信号**：后置观察失败时 `outcome=executed`、`op.Error=nil`
   （[act.go:113-128](../../internal/engine/act.go#L113-L128)），阶段其实已经记进 `op.Phase`
   （[act.go:126](../../internal/engine/act.go#L126)、
   [operations.go:132-145](../../internal/engine/operations.go#L132-L145)），但 `diagnose` 只在
   `op.Error != nil` 或 `outcome=uncertain` 时列问题
   （[diagnose.go:196-210](../../internal/engine/diagnose.go#L196-L210)），于是“动作成功、
   观察失败”在运维视图里完全隐形——缺的不是记录，是筛选条件。

### 1.2 证据链

1. `Result.Next` 的定义与序列化：[responses.go:437](../../internal/protocol/responses.go#L437)、
   [responses.go:647-654](../../internal/protocol/responses.go#L647-L654)（非空才写）。
2. 客户端解码：[decode_response.go:345](../../internal/protocol/decode_response.go#L345)；
   文本投影：[render.go:322-323](../../internal/mcp/render.go#L322-L323)。
3. 静态映射表：[errors.go:58-79](../../internal/protocol/errors.go#L58-L79)；手写 `.WithNext(...)`
   50 处、`.Next =`/`Next:` 写入 46 处，逐文件分布见 §6.1。
4. 静态拒绝不带 `next`（含 `ambiguous_point`）：
   [act.go:186-220](../../internal/engine/act.go#L186-L220)、
   [act_point_test.go:294-295](../../internal/engine/act_point_test.go#L294-L295)。
5. 成功后置观察失败仍为 `executed` 且 `op.Error` 为空：
   [act.go:113-128](../../internal/engine/act.go#L113-L128)、
   [image_test.go:542-547](../../internal/engine/image_test.go#L542-L547)。
6. `diagnose` 只看 `op.Error`/`uncertain`：
   [diagnose.go:196-210](../../internal/engine/diagnose.go#L196-L210)；被排除的
   `post_observation_failed` 阶段本身已经落库
   （[act.go:126](../../internal/engine/act.go#L126)、
   [operations.go:139-140](../../internal/engine/operations.go#L139-L140)）。
7. 接入层错误也带 `next`：[httpapi/server.go:489-498](../../internal/httpapi/server.go#L489-L498)；
   MCP 传输层用 `NextForCode` 兜底：[mcp/server.go:293-305](../../internal/mcp/server.go#L293-L305)。
8. `contract_version` 前进规则：“只在改变普通请求/响应必需形状、默认行为、核心枚举或执行语义时
   前进”（[release.md:42-44](../../docs/release.md#L42-L44)）。本提案同时改变响应形状与核心
   枚举，故必须前进。
9. 静态表已经漂移：`nextFor` 分支里的 `operation_running`、`observation_not_ready`、
   `tree_unavailable` 没有对应 `Code*` 常量（[errors.go:60-77](../../internal/protocol/errors.go#L60-L77)
   对照 [errors.go:82-158](../../internal/protocol/errors.go#L82-L158)）；`hierarchy_unavailable`
   （[errors.go:123](../../internal/protocol/errors.go#L123)）、`display_unavailable`
   （[errors.go:121](../../internal/protocol/errors.go#L121)）不在表内。
10. 同一个 code 在不同路径得到不同提示：`session_not_found` 在
    [end.go:52-56](../../internal/engine/end.go#L52-L56) 省略、在
    [end.go:140-143](../../internal/engine/end.go#L140-L143) 给 `status`；`app_launch_failed`
    两处都不写 `Next`（[openapp.go:184-186](../../internal/engine/openapp.go#L184-L186)、
    [openapp.go:234-236](../../internal/engine/openapp.go#L234-L236)），调用方只能看到空串。
11. `Result` 的成员与序列化顺序：[responses.go:425-448](../../internal/protocol/responses.go#L425-L448)、
    [responses.go:639-655](../../internal/protocol/responses.go#L639-L655)；`next` 是最后一个成员，
    `recovery` 直接顶替该位置，其余成员顺序不动。

### 1.3 为什么直觉会出错

- **“保留 `next`，再加几个字段就行”**：双源仍在，且 `next` 与 `recovery.entry` 会长期并存，
  产生第二个真相。本次明确删除，不留镜像。
- **“把 `next` 扩成自然语言短句”**：不可判定、不可测、必然漂移，且与“`message` 是受控短句、
  调用方只依赖 `code`”的既有分层冲突（[interface.md:60](../../docs/interface.md#L60)）。
- **“让 `next` 表示服务端下一步动作”**：把决策权搬到服务端，等于引入隐式状态机，违背
  “设备同时只有一项普通工作、不建普通动作队列”。
- **“`next` 为空就表示可以重试”**：空值已经表示“无恢复入口”，再叠一层会立即产生歧义。
- **“只改 `act`/`open_app`”**：`status`、`end`、`keepalive`、`shell`、`apps`、`observe`、
  `inspect`、控制同步（[control.go:18](../../internal/engine/control.go#L18)）与两个接入层都在写
  `next`，只改两个工具会留下混合契约，调用方要同时理解两套语义。

## 2. 目标与非目标

**目标**

- G1 单一来源：工具结果的恢复入口只由 `planRecovery(...)` 一个纯函数决定；接入层错误只由
  `AccessRecovery(...)` 的四码表决定，两者作用域不重叠，也不互相调用；删除 `NextForCode` 与
  全部手写 `WithNext`。
- G2 可判定：调用方仅凭结果就能构造下一个请求（入口 + 引用 + 旧观察是否可用）。
- G3 覆盖零成本重试：静态拒绝必须给出 `entry=retry`、`reference=O`、`input_state=effective`。
- G4 覆盖全部工具与两个接入层，不留下第二套语义。
- G5 删除 `next`，不保留镜像；`contract_version` 前进一格。发行号与 versionCode 见 §9.1。
- G6 可测试：§4.5 的十条不变式各有对应用例，含反向对照。

**非目标**

- 不改变任何超时、预算、settle、重验、派发时序。
- 不新增请求字段，不改变请求的严格接入规则。
- 不改变 `uncertain` 的终态性质，不引入自动重试或重放。
- 不改变 MCP 工具集合、名称与参数 schema。
- 不让服务端读取或执行 `recovery`。
- 不修改 Android App 的 Java 逻辑（仅重新生成版本常量与字符串）。
- 不解决“动作是否真的生效”这个不可判定问题（见 §7.2）。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 维持 `next` | 0 | 语义混轴、双源、无重试入口、不可验证 |
| R2 保留 `next` + 补字段 | 加 `next_reference` 与 `input_state` | 小 | 双源长期并存；`next` 与新字段可能互相矛盾 |
| R3 删 `next`，扁平三字段 | `recovery_entry`、`recovery_reference`、`cause` + 复用 `input_state` | 中 | 三个字段散落在扁平 `Result` 里，与 `status` 的 `input_state` 语义重叠，读起来仍要拼装 |
| R4 删 `next`，结构化 `recovery` | 一个对象四个成员，`input_state` 与 `status` 同词汇 | 中 | 需要一次全量迁移；老客户端读不到提示 |
| R5 删 `next`，只留 `code` | 客户端用 `AccessRecovery` 自行派生 | 最小 | 恢复了“入口”却丢了“引用”和“可用性”；success 降级路径没有 code 可派生 |

选择 **R4**。理由：

1. 这是唯一能一次表达“入口 + 引用 + 旧观察可用性 + 原因”的形状；R3 需要调用方从三个平铺
   字段拼出同一个判断，且 `input_state` 与 `status` 的同名字段会产生“这是谁的 input_state”
   的歧义。
2. 删除 `next` 而不留镜像，是本次的核心决定：**不允许两个真相**。迁移期只允许一个版本，
   不做双写。
3. R5 的失败模式最直接：`executed + 后置观察失败` 没有 error code，无从派生。
4. R2 只是把 R1 的问题推后一个版本，且会制造 `next` 与 `recovery.entry` 不一致的可能。

R2/R3/R5 不是永久否决：任一被重新提出，都必须回到本提案修订并重新确认（process.md §5），
不能作为实现期的临时偏离。

## 4. 判据

### 4.1 线上形状

```json
{
  "operation_id": "P7a1",
  "outcome": "executed",
  "observation": {"status": "failed", "missing": ["tree"], "error": {"code": "tree_too_large", "message": "…"}},
  "recovery": {
    "entry": "observe",
    "reference": "P",
    "input_state": "retired",
    "cause": "post_observation_failed"
  }
}
```

- `recovery` 出现在 `Result` 的固定顺序位置：`…, diagnosis, cleanup, error, recovery`（顶替原
  `next` 的位置，[responses.go:639-655](../../internal/protocol/responses.go#L639-L655)）。
- 省略的成员不写 `null`：`MarshalJSONTo` 只在非零时输出，`error` 缺失就没有 `error` 键
  （[responses.go:451-655](../../internal/protocol/responses.go#L451-L655)）。
- `recovery` 对象缺失表示“本次结果没有待恢复事实”；它存在时 `entry` 与 `reference` 必出现，
  `input_state` 与 `cause` 各自按 §4.2 的条件出现。

### 4.2 字段定义

| 成员 | 取值 | 出现条件 | 含义 |
| --- | --- | --- | --- |
| `entry` | `retry` / `observe` / `status` / `end` / `user` / `update` | 对象存在时必出现 | 下一个请求应调用的入口 |
| `reference` | `P` / `O` / `none` | 对象存在时必出现 | 该入口要带的引用类型 |
| `input_state` | `effective` / `expired` / `revoked` / `claimed` / `retired` | 仅当本次请求引用了一个可解析的 O | 该 O 的输入可用性，词汇与 `status(O)` 完全一致 |
| `cause` | `post_observation_failed` / `executed_revoked` / `cleanup_pending` / `running` | 仅当 `error` 缺失且确有降级/轮询事实 | 为什么“看似成功/在跑”仍需要恢复 |

`reference` 只声明引用类型，不带 ID。取哪个 ID 取决于 `entry`：

- `entry=status`：`P` 就是本结果的 `operation_id`，即轮询对象；端点自身用
  `start_operation_id`，`status(O)` 用 `target_reference`，`device_busy` 用
  `blocking_operation_id`。
- `entry=retry`/`observe`：`P`（或 `O`）属于**下一个请求**——`P` 是调用方新生成的幂等键。
  不能直接把本结果的 `operation_id` 当 `P` 再发一次：`registerOperation` 命中已有 P 时返回
  重复视图而不重新执行（[operations.go:65-69](../../internal/engine/operations.go#L65-L69)）。

`entry` 的语义边界：

- `retry`：**什么都没发生**。调用同一工具、使用新的 `P`、复用同一个 `O`。仅当 `claimed=false`
  且未派发时出现。
- `observe`：观察证据作废，必须重新采集。一律 `reference=P`，且这里的 `P` 是下一个请求的新
  幂等键；已消耗的 `O` 不可再用，而 `P` 只要不重复就能继续用
  （[observe.go:122-130](../../internal/engine/observe.go#L122-L130)）。
- `status`：结果未定或收尾未完成。
- `end`：会话已结束/过期/被撤销。`reference` 用 `P`；只有 `status(O)` 的撤销分支用 `O`
  （该结果里没有这个会话的 P，而 `end` 本来就接受 O，[end.go:107-118](../../internal/engine/end.go#L107-L118)）。
- `user`：需要人介入，任何 API 调用都不会成功。
- `update`：契约版本不匹配。

`input_state` 与 `status(O)` 的顶层 `input_state` 同源同词汇
（[status.go:88-118](../../internal/engine/status.go#L88-L118)）：会话结束 → `revoked`；`ready`
且在 `ActionableUntil` 内 → `effective`；`ready` 已超期 → `expired`；其余保留
`claimed`/`retired`。本次请求没有引用 O、或引用的 O 完全不可解析时不出现（表格里的 `—`）。

### 4.3 省略规则

| 情形 | `recovery` |
| --- | --- |
| 动作成功且发布了新观察 | 省略 |
| 只读工具成功（`observe`/`inspect`/`apps`/`status`/`diagnose`/`notify`/`keepalive`） | 省略 |
| `diagnose` 的任何结果（它不返回 `error`，也没有可推进的引用，[diagnose.go:29-41](../../internal/engine/diagnose.go#L29-L41)） | 省略 |
| 接入层参数错误（`invalid_request`、`unauthorized`、`forbidden`、方法不许、JSON 语法） | 省略 |
| 确定性体积拒绝 `inspection_too_large`：同样的叶子单元再读一次仍超预算，出路是改 `node`/`query` | 省略 |
| 重复 `P` 返回原视图 | 沿用按 `op` 重算的结果（I8），不另写 |
| 其余失败与降级 | 输出 |

`cause` 永远不与 `error` 同时出现；`input_state` 只描述本次请求引用的那个 O，`reference` 描述
恢复入口要带的引用类型，两者可以不同（`user`/`none`/`effective` 合法：入口不需要引用，但旧 O
仍然可用）。

### 4.4 与既有契约的关系

- **更严**：同一组输入（`tool`、`layer`、`code`、`phase`、`outcome`、`claimed`、`input_state`、
  `cleanup`，见 §6.2）只能映射到唯一 `recovery`，由函数封闭，不再允许路径手写。
- **更松**：`retry` 让静态拒绝有了明确重试语义，调用方不必再靠“`next` 为空”猜测。
- **不改**：`outcome`、`observation.status`、`session.state`、`cleanup.*` 四层事实互不替代；
  `recovery` 只是第五层“下一步”的投影，不参与任何判定。
- **不进判据**：`message`、`error.details`、`observation.id`/`image`、`session` 视图、
  `cleanup.*` 的细节都不参与推导——它们是本次事实，不是下一步。`phase` 只用来判别两个
  `cause` 与 `shell` 子表；`claimed` 只区分“O 是否已被本次请求消费”。

### 4.5 不变式

| 编号 | 不变式 |
| --- | --- |
| I1 | `entry` 只取六个值之一 |
| I2 | `reference` 只取 `P`/`O`/`none` |
| I3 | `entry=retry` ⇒ `reference=O` 且 `input_state=effective`（retry 的前提正是 O 未被消费） |
| I4 | `entry=observe` ⇒ `reference=P`；若本次请求引用的 O 可解析，则 `input_state ∈ {claimed,retired,expired}`，不得为 `effective`（否则入口应是 `retry`） |
| I5 | `entry=end` ⇒ `reference ∈ {P,O}`；引用 P 时 ID 是本结果的 `operation_id`，引用 O 时只出现在 `status(O)` 的撤销分支（结果里没有该会话的 P）；若引用 O 则 `input_state=revoked` |
| I6 | `entry ∈ {user,update}` ⇒ `reference=none` |
| I7 | `cause` 出现 ⇒ 本次结果 `error` 缺失 |
| I8 | 同一组输入（`tool`、`layer`、`code`、`phase`、`outcome`、`claimed`、`input_state`、`cleanup`）映射到唯一输出；`layer=access` 由 `AccessRecovery` 单独封闭，`layer=api` 由 `planRecovery` 封闭 |
| I9 | `recovery` 缺失 ⇔ §4.3 的省略情形 |
| I10 | 服务端不读取请求里的 `recovery`；它只被序列化 |

## 5. 算法

### 5.1 决策顺序

```text
planRecovery(in):                                   # in 的成员见 §6.2
  if in.layer == "access":                          return AccessRecovery(in.code)   # §5.2 末表
  if in.tool == "diagnose":                         return omit                      # 从不返回 error
  if in.tool == "end":                              return endPlan(in)               # §5.2 end
  if in.tool == "status":                           return statusPlan(in)            # §5.2 status
  if in.tool == "start":                            return startPlan(in)             # §5.2 start
  if in.code == contract_mismatch:                  return {update, none}
  if in.tool ∈ {apps, notify}:                      return discoveryPlan(in)         # 会话无关
  if in.tool == "shell":                            return shellPlan(in)             # §5.2 shell
  if in.code ∈ {session_ended, session_expired, control_revoked}:
                                                    return {end, P, in.input_state}
  if in.code ∈ observationDeadCodes:                return {observe, P, in.input_state}
  if in.code ∈ statusCodes:                         return {status, P, in.input_state}
  if in.code ∈ captureFailureCodes:                 return {observe, P, in.input_state}
  if in.outcome == uncertain:                       return {status, P, retired}
  if in.phase == "post_observation_failed":         return {observe, P, retired, post_observation_failed}
  if in.phase == "executed_revoked":                return {status, P, revoked, executed_revoked}
  if in.claimed and in.code != "":                  return {observe, P, in.input_state}  # 认领后失败
  if in.outcome == not_executed and !in.claimed and
     in.tool ∈ {act, open_app}:                     # 只有会认领 O 的工具才有 retry
      if in.code ∈ userCodes:                       return {user, none, effective}
      return {retry, O, effective}                  # 关键分支
  return omit
```

`end`/`status`/`start` 三个工具各自的子表先于任何通用规则：它们要么自带生命周期（`end` 之后
会话必然不活动、`status` 本来就可以没有会话），要么不以既有会话为前提（`start`）。
`observationDeadCodes`、`statusCodes`、`captureFailureCodes`、`userCodes` 四张表写在 §6.2。

要求：函数**全序**（任何输入都有确定输出或明确省略），纯 CPU，不读设备、不加锁外等待。
`in.phase` 的两个特殊值分别来自 `act`/`open_app` 的 `post_observation_failed`
（[act.go:126](../../internal/engine/act.go#L126)）与 `executed_revoked`
（[act.go:88](../../internal/engine/act.go#L88)），`start` 的首个观察失败记的是
`first_observation_failed`（[start.go:223](../../internal/engine/start.go#L223)），按同一
`cause` 处理。

### 5.2 全量映射表

**`act`**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| `session_ended` / `session_expired` / `control_revoked`（STOP） | `end` | P | revoked | — |
| `observation_not_found`（未知/他人 O） | `observe` | P | —（O 不可解析） | — |
| `observation_retired`/`observation_claimed`/`observation_expired`/`no_ready_observation` | `observe` | P | retired/claimed/expired | — |
| `device_busy` | `status` | P（`blocking_operation_id`） | effective | — |
| `node_not_found`/`node_disabled`/`unsupported_node_action`/`invalid_coordinates`/`ambiguous_point`/`unsupported_key`/`invalid_request`（请求结构，如缺 `target`/`from`/`to`） | `retry` | O | effective | — |
| `target_changed`/`foreground_changed`/`foreground_unknown` | `observe` | P | retired | — |
| `display_unavailable`/`hierarchy_unavailable`/`stale_screen`/`tree_too_large`/`control_ui_on_virtual_display`（认领后重验） | `observe` | P | retired | — |
| `action_result_uncertain`/`text_target_uncertain`/`text_result_uncertain` | `status` | P | retired | — |
| 成功后置观察失败（含 `tree_too_large`、`control_ui_on_virtual_display`） | `observe` | P | retired | `post_observation_failed` |
| 成功但期间撤销 | `status` | P | revoked | `executed_revoked` |
| 成功且发布新观察 | 省略 | — | — | — |

**`open_app`**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| `session_*` / `observation_*` / `control_revoked` | 同 `act` | | | |
| `invalid_request`（`app` 缺失，[openapp.go:41](../../internal/engine/openapp.go#L41)） | `retry` | O | effective | — |
| `app_not_found` | `user` | none | effective | — |
| 启动入口解析失败（503 `device_unavailable`，claim 前，[openapp.go:71-72](../../internal/engine/openapp.go#L71-L72)） | `retry` | O | effective | — |
| 任务列表不可读（503 `device_unavailable`，claim 前，[openapp.go:79-82](../../internal/engine/openapp.go#L79-L82)） | `retry` | O | effective | — |
| `display_unavailable`（claim 后，[openapp.go:100-103](../../internal/engine/openapp.go#L100-L103)） | `observe` | P | retired | — |
| `app_launch_failed`（claim 后） | `observe` | P | retired | — |
| `task_focus_unavailable`/`app_launch_unconfirmed`/`application_moved` | `status` | P | retired | — |
| 成功后置观察失败（`control_ui_on_virtual_display` 只可能出现在这一步） | `observe` | P | retired | `post_observation_failed` |
| 成功但期间撤销 | `status` | P | revoked | `executed_revoked` |
| 成功且发布新观察 | 省略 | — | — | — |

**`observe` / `inspect`**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| `session_ended`/`session_expired` | `end` | P | revoked | — |
| `request_closed`/`operation_unknown`/`reference_no_session` | `status` | P | — | — |
| `observation_not_found`（O 未知或不属于本 agent） | `observe` | P | —（O 不可解析） | — |
| `hierarchy_unavailable`/`display_unavailable`/`stale_screen`/`tree_too_large` | `observe` | P | — | — |
| `content_unavailable`（inspect 的树已被淘汰） | `observe` | P | retired | — |
| `inspection_too_large` | 省略 | — | — | — |
| 成功 | 省略 | — | — | — |

`inspect` 读的是已发布证据，O 可能早已 `retired`；给它 `retry/O` 会违反 I3，所以体积类拒绝不
提供恢复入口——出路是改 `node`/`query`（§4.3）。

**`start`**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| 已有活动会话（`session_exists`，带 `start_operation_id`） | `end` | P | — | — |
| 显示创建/绑定失败、`mode_not_selected`、`control_unavailable`、`device_locked`、`device_not_interactive` | `user` | none | — | — |
| 设备或旧资源未就绪（`device_busy`、`recovering`、`cleanup_unconfirmed`、`ledger_*`） | `status` | P | — | — |
| 首个观察失败但会话仍活动 | `observe` | P | retired | `post_observation_failed` |
| 首个观察失败并触发撤销（`control_ui_on_virtual_display`/`display_unavailable`/`stale_screen`，[start.go:224-236](../../internal/engine/start.go#L224-L236)） | `status` | P | revoked | `cleanup_pending` |
| 成功 | 省略 | — | — | — |

最后两行是同一段代码的两个分支：原来的实现两行都写 `next=observe`
（[start.go:222](../../internal/engine/start.go#L222)），撤销分支因此指向一个已经不存在的会话。

**`end`**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| 收尾 `pending`/`unconfirmed`（[end.go:98-102](../../internal/engine/end.go#L98-L102)） | `status` | P | revoked | `cleanup_pending` |
| 已封闭 P（[operations.go:74-77](../../internal/engine/operations.go#L74-L77)）/ 无会话（`session_not_found`、`reference_no_session`） | `status` | P | — | — |
| 引用不可解析（`observation_not_found`、`operation_not_found`、`invalid_request`） | 省略 | — | — | — |
| 收尾 `complete` / `not_applicable` | 省略 | — | — | — |

`end` 是唯一可能“会话已不活动但入口不是 `end`”的工具：它本身就是终点，重发只会得到同一份
收尾视图。`session_not_found` 在 [end.go:52-56](../../internal/engine/end.go#L52-L56) 与
[end.go:140-143](../../internal/engine/end.go#L140-L143) 两条路径上原本一个省略、一个给
`status`，实现时统一到上表第二行。

**`status`**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| 自身存在恢复记录（`e.recovery != nil`，[status.go:48-50](../../internal/engine/status.go#L48-L50)） | `status` | P（`start_operation_id`，无则 none） | — | — |
| `status(O)`：`ready` 且未过期 | 省略 | — | （顶层 `input_state=effective` 已表达） | — |
| `status(O)`：会话已结束 | `end` | O | revoked | — |
| `status(O)`：过期 / 已消费 / 已退役 | `observe` | P | expired/retired/claimed | — |
| `status(P)`：running | `status` | P | — | `running` |
| `status(P)`：finished 且带 `error` | 按 `code` 走 §5.1 | P | — | — |
| `status(P)`：finished 且 `outcome=uncertain` | `status` | P | — | — |
| `status(P)`：会话已结束且收尾 `pending`/`unconfirmed`（[status.go:80-83](../../internal/engine/status.go#L80-L83)） | `status` | P | revoked | `cleanup_pending` |
| 其余 | 省略 | — | — | — |

第一行不写 `cause`：`cause` 只用于“看似成功/在跑”的降级，恢复记录本身是确定的轮询事实。

**`keepalive` / `apps` / `notify` / `shell` / 接入层**

| 触发 | `entry` | `reference` | `input_state` | `cause` |
| --- | --- | --- | --- | --- |
| keepalive：`reference_no_session`/`operation_unknown`/`operation_not_found` | `status` | P | — | — |
| keepalive：`session_ended`/`session_expired` | `end` | P | revoked | — |
| keepalive：`observation_not_found` | `observe` | P | —（O 不可解析） | — |
| apps：`device_busy`/`device_unavailable`/`app_list_unavailable` | `status` | P | — | — |
| apps：`control_revoked` | `end` | P | — | — |
| apps：`invalid_request` | 省略 | — | — | — |
| notify：`operation_unknown`/`operation_not_found`/`device_unavailable` | `status` | P | — | — |
| notify：`invalid_request` | 省略 | — | — | — |
| shell：`root_unavailable` | `user` | none | — | — |
| shell：`control_revoked`/`session_ended`/`session_expired` | `end` | P | revoked | — |
| shell：`observation_not_found` | `observe` | P | —（O 不可解析） | — |
| shell：`invalid_request`（reference 类型无效） | 省略 | — | — | — |
| shell：`shell_start_failed`/`shell_result_unconfirmed`/`device_unavailable`/`request_closed`/`operation_unknown`/`operation_not_found`/`reference_no_session` | `status` | P | — | — |
| shell：成功 | 省略 | — | — | — |
| 接入层：`contract_mismatch` | `update` | none | — | — |
| 接入层：`unauthorized`/`forbidden`/`invalid_request`/方法不许/JSON 语法 | 省略 | — | — | — |
| MCP：传输失败 / `call_cancelled` | `status` | P | — | — |

`shell` 不产生 `retry`：执行链的未知状态只能经 `status` 确认，重试会与未确认的执行链竞争。
`apps`/`notify` 是会话无关工具，没有 O 可复用，所以也不产生 `retry`。四张表（状态、采集失败、用户、
观察已死）见 §6.2。表按工具覆盖 §6.1 列出的全部 `next` 写入点；将来新增 code 而漏列时，会落到
§5.1 的默认分支，由 T1 的表驱动用例暴露缺口。

各表 `input_state` 一列写的是“本次请求确实引用了一个可解析的 O”时的取值；`shell`、`keepalive`、
`status`、`observe`/`inspect` 的引用可能是 P，这种行不输出 `input_state`（§4.2）。

### 5.3 边界行为

- **时间轴对齐**：静态拒绝（认领前，O 仍 `ready`）→ `retry`；认领后（O 已 `retired`）→
  `observe`；派发后结果不可判 → `status`；会话消失 → `end`。四个入口与 O 的生命周期一一对应，
  也是 §4.5 那组不变式的来源。
- **会话在结果组装期间结束**：以组装时刻实际产出的 `session_ended`/`control_revoked` 码为准；
  端点本身是 `end` 时仍走 §5.2 的 `end` 子表（`cleanup_pending` 优先于一切）。
- **超时**：工作超时沿用各工具已有的失败分支（`outcome=uncertain` 或 `not_executed`），
  `recovery` 不新增超时语义，也不延长任何 deadline。
- **预算耗尽**：`tree_too_large` 不改变 `outcome`，认领后落 `observe`；`inspection_too_large`
  省略；`image_too_large` 只出现在 `observation.error` 里，不进 §4.4 的判据。
- **`P` 已被淘汰**（`MaxOperations` 驱逐）：`reference=P` 会得到 `operation_unknown`。
  这是已知边界，见 §7.2。
- **同一结果既有 `error` 又有 `cause`**：`cause` 不输出（I7），信息由 `error.code` 承担。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/protocol/responses.go` | 删除 `Result.Next`（:437）与序列化块（:647-654）；新增 `Recovery` 结构、`Result.Recovery` 与 `MarshalJSONTo` 分支（置于 `error` 之后，仍是最后一项） |
| `internal/protocol/errors.go` | 删除 `APIError.Next`（:17）与 `WithNext`（:215-219）；`NewError` 不再预填（:45）；`NextForCode`/`nextFor`（:55-79）收窄为 `AccessRecovery(code) *Recovery`，只覆盖四个接入层码 |
| `internal/protocol/decode_response.go` | 删除 `Next` 成员与赋值（:345、:376）；新增 `recovery` 对象解码。响应解码本来就忽略未知成员（[decode_response.go:86-96](../../internal/protocol/decode_response.go#L86-L96)），删字段不会让旧客户端报错 |
| `internal/engine/recovery.go`（新增） | `planRecovery`、`recoveryInput`、`endPlan`/`statusPlan`/`startPlan`/`discoveryPlan`/`shellPlan` 与四张码表 |
| `internal/engine/act.go` | 删除 7 处 `result.Next=`（:59/:71/:78/:87/:96/:108/:125）、3 处 `Next:` 字面量（:539/:546/:553）与 19 处 `WithNext`；失败分支走 `finishWithError`，不可判分支（:66-75）保留手写 `Result`，但 `recovery` 由同一个 `planRecovery` 生成 |
| `internal/engine/openapp.go` | 删除 5 处 `result.Next=`（:407/:410/:417/:427/:448）与 9 处 `WithNext`；`finishOpenAppSuccess`/`finishOpenAppUncertain` 调用同一个 `planRecovery` |
| `internal/engine/observe.go` | 删除 3 处 `result.Next=`（:85/:87/:146）与 4 处 `WithNext`；`finishObserveError` 走统一入口 |
| `internal/engine/start.go` | 删除 `result.Next = "observe"`（:222）；首个观察失败按会话是否仍活动分成 `observe` 与 `status/cleanup_pending` 两支 |
| `internal/engine/end.go` | 删除 2 处 `result.Next=`（:99/:101）与 2 处 `WithNext`；收尾状态映射为 `cleanup_pending` |
| `internal/engine/status.go` | 删除 7 处 `result.Next=`（:49/:74/:78/:83/:107/:112/:115）；三个视图各自产出 `recovery` |
| `internal/engine/keepalive.go` | 删除 1 处 `result.Next=`（:115）与 5 处 `WithNext` |
| `internal/engine/apps.go` | 删除 1 处 `result.Next=`（:155）与 1 处 `WithNext`；`appsUnavailable` 不再自带入口 |
| `internal/engine/shell.go` | 删除 2 处 `result.Next=`（:629/:633）与 8 处 `WithNext`（含 :471 把 `NextForCode` 直接传给 `WithNext`）；保留“shell 不 retry” |
| `internal/engine/operations.go` | 删除 3 处 `Next:` 字面量（:76/:162/:312）与 `finishWithError` 里的 `result.Next = apiErr.Next`（:337）；`finishWithError` 改为调用 `planRecovery`，结果同时写入 `Result.Recovery` 与 `operationState.Recovery`（供 `status(P)` 重放） |
| `internal/engine/engine.go` | `operationViewLocked` 的重放视图（:936）改调 `planRecovery(op)`，不再用 code 表 |
| `internal/engine/control.go` | 控制同步 `contract_mismatch`（:18）改 `Recovery{update, none}`；控制同步响应的其余字段不动 |
| `internal/httpapi/server.go` | `writeAPIError`（:489-498）输出 `recovery`：一律走 `AccessRecovery(code)`；契约不匹配给 `update`，其余接入层码省略。引擎已算好的工具结果不经此处 |
| `internal/mcp/render.go` | 删除 `next=` 行（:322-323）；新增 `recovery.entry`/`recovery.reference`/`recovery.input_state`/`recovery.cause` 四行 |
| `internal/mcp/server.go` | 三处直写 `Next`（:140/:156/:230）与两处兜底（:293-305）改 `Recovery`；取消时固定 `status` |
| `internal/client` | `grep -rn Next internal/client/` 为空；解码走 `protocol.DecodeResponse`，无需改动（`client_test.go` 的两处 fixture 除外，见“测试”） |
| `internal/store` | 只有 `NextSeq`（消息序号），与 `next` 无关；不改 |
| `release/versions.json` | `contract_version` 3；`release_version` 0.4.1、`android_version_code` 4（按 §9.1 已定结论写入） |
| 生成产物 | `internal/protocol/version_generated.go`、`GeneratedVersion.java`、`strings_version.xml`、`AndroidManifest.xml`（`scripts/generate-version.ps1` 生成，不手改） |
| 测试 | 10 个文件、32 处 `next` 相关引用（23 处 `.Next` 断言、7 处 JSON fixture、2 处 `NextForCode`）改为 `recovery`；新增 `recovery_test.go` |

**通用路径怎么带 `recovery`**：不给 `APIError` 加字段，也不要求 50 个调用点各写一个入口。
`finishWithError`（[operations.go:329-344](../../internal/engine/operations.go#L329-L344)）本身就是
所有工具失败的唯一漏斗：它手里有 `op`（含 `Kind` 与新增的 `Claimed`）、`phase`、
`apiErr.Code`/`Outcome`，足以填满 `recoveryInput`，于是调用一次 `planRecovery`，把结果写进
`Result.Recovery`，同时记到 `operationState.Recovery` 供 `status(P)` 重放（同一 `P` 前后必须
一致，I8）。六个非漏斗路径——`act`/`open_app` 的 `uncertain`、后置观察失败、`executed_revoked`，
`start` 的首个观察失败，以及 `status`/`end`/`keepalive`/`apps`/`shell`/`observe` 各自的
`finish*` 函数——各自调用同一个 `planRecovery`，都不构造入口字面量。

理由是：把载体挂在 `APIError` 上会留下“谁负责填”的隐性约定，而错误对象在 50 个调用点都可能
被构造，漏填就退化成空提示；挂在 `operationState` 上则只记录“已经返回过什么”，是纯事实，
不参与推导，也不会被下一次请求重新解释。

引擎以 `(nil, apiErr)` 返回的路径（`registerOperation` 拒绝、`inspect` 的解析错误）没有
operation 记录，`reference=P` 没有锚点：这类错误由 `httpapi.writeAPIError` 用
`AccessRecovery` 处理，四个接入层码之外一律省略。

**`claimed` 从哪来**：`operationState` 增加 `Claimed bool` 与 `Recovery *Recovery`，前者由
`claimObservationLocked`（[act.go:244-269](../../internal/engine/act.go#L244-L269)）在
`ready → claimed` 成功时置位（该函数已经有 `work.ClaimedO`，但 `work` 在派发结束后即释放，
不能用于 `status` 重放），后者由 `finishWithError` 或各 `finish*` 写入。`input_state` 不落库，
按 `claimed`、`observation_expired`、会话状态即时推导。`planRecovery` 只读这些字段，不碰设备、
不加锁。

**为什么不是把完整映射放进 `protocol`**：`protocol` 不 import 任何 `internal` 包
（`grep -rn "mobile-use-vnext/internal" internal/protocol/` 为空），放进去不会循环依赖；但工具级
恢复依赖 `claimed`/`phase`/`cleanup`，这些只有 `engine` 有。所以 `protocol` 只留
`AccessRecovery`（`unauthorized`/`forbidden`/`invalid_request`/`contract_mismatch`），其余全部由
`engine.planRecovery` 决定；`engine` 不得调用 `AccessRecovery` 生成工具结果，两个方向都不越界。

### 6.2 配置与取值

- 无新增配置项。六个 `entry`、三个 `reference`、五个 `input_state`、四个 `cause` 都是
  `protocol` 包内的编译期常量，禁止字符串字面量散落。
- 输入固定为 `recoveryInput`：`tool`、`layer`（`api`/`access`）、`code`、`phase`、`outcome`、
  `claimed`、`input_state`、`cleanup`（`none`/`pending`/`unconfirmed`/`complete`）。函数只读这组
  值，不读 `Result` 本身，避免“读自己写的”循环。
- 四张码表写在 `internal/engine/recovery.go`，由 §8.1 T1 的表驱动用例覆盖：
  - `observationDeadCodes`：`observation_not_found`、`observation_retired`、
    `observation_claimed`、`observation_expired`、`no_ready_observation`、`content_unavailable`；
  - `statusCodes`：`device_busy`、`request_closed`、`operation_unknown`、`operation_not_found`、
    `reference_no_session`；
  - `captureFailureCodes`：`hierarchy_unavailable`、`display_unavailable`、`stale_screen`、
    `tree_too_large`、`control_ui_on_virtual_display`；
  - `userCodes`：`device_locked`、`device_not_interactive`、`stop_unavailable`、
    `mode_not_selected`、`control_unavailable`、`app_not_found`、`root_unavailable`、
    `display_create_failed`。
- `AccessRecovery` 只认四个码：`contract_mismatch` → `{update, none}`；`unauthorized`、`forbidden`、
  `invalid_request` → 省略；其它 code 也省略（接入层产生不了工具级 code）。不在 `protocol` 里
  重复工具级码表。

### 6.3 日志与可观测性

- 不新增日志事件，不改变白名单；`recovery` 只进响应，不进日志。
- `diagnose` 的可观测缺口（§1.1 第 7 条）不在本提案内解决，但 `cause` 字段为其提供了
  机器可读的入口，见 §9.1（U1 已关闭）。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 动作事实不改判 | `recovery` 只描述下一步，永不修改 `outcome` |
| `O` 单向消费 | `input_state` 直接投影 `claimed/retired`，不新增状态 |
| 状态锁短小 | `planRecovery` 纯 CPU、O(1)、不做 I/O；可在锁内调用 |
| 严格接入 | 请求面零改动；响应面删除一个成员、新增一个对象 |
| 三态与省略 | `recovery` 缺失即“无事实”，与 `omitempty` 无关，用手写 `MarshalJSONTo` 控制 |
| 事实分层 | 新增第五层“下一步”，不与执行/观察/会话/收尾互相替代 |
| 恢复屏障 | 响应里的 `recovery` 与引擎内部的 `e.recovery`（`*Ledger`，[engine.go:237](../../internal/engine/engine.go#L237)）不是同一事实：前者是下一步投影，后者是账本恢复态。两者不复用类型、不互相赋值 |

### 6.5 契约与文档影响

| 文档 | 需要修订的位置 |
| --- | --- |
| `docs/interface.md` | §2 字段顺序（:46-49）、示例里的 `next`（:57）、`next` 定义（:62）、接入层错误（:76）、`observe` 恢复语（:108、:116）、`end`（:149）、`status`（:161、:171-175）、`start`（:215）、图片（:237）、坐标（:278）、`act` 三态表（:285-287、:291、:298）、示例（:320）、`open_app`（:590）、`shell`（:648）；`contract_version` 字面量 5 处（:338、:365、:398、:408、:421） |
| `docs/client.md` | 结果字段清单（:57） |
| `docs/mcp.md` | 结果投影清单里的 `next`（:36） |
| `docs/control-protocol.md` | `contract_version` 字面量 5 处（:22、:45、:68、:132、:138） |
| `docs/phone-operations.md` | 控制同步示例（:208） |
| `docs/runbook.md` | `/health` 说明（:143）、错误表（:202） |
| `docs/release.md` | 清单示例（:69） |

`docs/` 只写实现后具备的行为；本提案阶段不修改 `docs/`。上表 7 个文件与 §7.3、§10 的文件数一致。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 恢复入口单源化：50 处手写 + 1 张已漂移的静态表 → 1 个纯函数，接入层只留 4 个码。
- 静态拒绝获得 `retry`，调用方不必猜“空 `next` 是什么意思”。
- 失败结果自带 `input_state`，判断“旧 O 还能不能用”不再需要额外一次 `status(O)` 往返
  （典型节省 1 次 RTT）。
- `executed + 后置观察失败` 首次获得机器可读原因（`cause`）。
- 每条映射可由表驱动测试穷举，恢复路径第一次可验收。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 动作是否真的生效 | `uncertain` 是信息论边界，`recovery` 只给 `status`，不改变结论 |
| STOP 后已派发命令的撤回 | 不可能撤回，`recovery` 不伪称 |
| 会话 UI 已变时旧 `O` 的复用 | 旧 `O` 依旧必须消耗；`recovery` 只把这件事说清楚 |
| `P` 被 `MaxOperations` 淘汰后的恢复 | `reference=P` 会得到 `operation_unknown`；此时只能 `observe` 一个仍在保留窗口的 O，或先 `end` 再 `start`——直接 `start` 会撞 `session_exists`（[start.go:33-38](../../internal/engine/start.go#L33-L38)） |
| 旧客户端读不到提示 | 删除 `next` 是破坏性变更；响应解码忽略未知成员（[decode_response.go:86-96](../../internal/protocol/decode_response.go#L86-L96)），旧客户端不会报错，只会静默丢掉 `recovery`，必须整组升级 |
| `diagnose` 里看见后置观察失败 | 本提案只在响应里给 `cause`，`diagnose` 聚合按 §9.1 留到实现提交后单独做 |

### 7.3 成本

- 响应体：每个带恢复的结果增加约 90–120 字节（四个短键值），成功路径零增量。
- 设备 I/O：零增量。
- 往返：典型失败路径减少 1 次 `status(O)`。
- 迁移成本：客户端与 MCP 提示词需改读 `recovery`；文档 7 个文件、测试 10 个文件 / 32 处引用。

## 8. 验证计划

### 8.1 单元测试

| 编号 | 用例 | 期望 |
| --- | --- | --- |
| T1 | `TestRecoveryCoversEveryWritePoint` | `internal/` 下每个 `next` 写入点可达的 code 都有确定映射或明确省略；`AccessRecovery` 只覆盖四个接入层码 |
| T2 | `TestRecoveryRetryKeepsObservation` | 静态拒绝 → `retry/O/effective`；随后用新 `P` 重试同一 `O` 成功 |
| T3 | `TestRecoveryObserveAfterClaim` | `target_changed` → `observe/P/retired` |
| T4 | `TestRecoveryPostObservationFailed` | `outcome=executed` + `cause=post_observation_failed` + `input_state=retired` |
| T5 | `TestRecoveryUncertainIsStatus` | 三类不确定码均 → `status/P/retired` |
| T6 | `TestRecoverySessionEndIsEnd` | 会话结束/撤销 → `end/P/revoked` |
| T7 | `TestRecoveryOmittedOnSuccess` | `act`/`observe` 成功结果不含 `recovery` |
| T8 | `TestRecoveryInvariants` | I1–I9 逐条断言，含 `retry ⇒ reference=O`、`observe ⇒ input_state≠effective`、`end ⇒ revoked` |
| T9 | `TestRecoveryIsNotReadByEngine` | 反向对照：把 `planRecovery` 改为恒返回空，T2–T6 必须失败 |
| T10 | `TestResultJSONHasNoNext` | 全量结果序列化后不含 `"next"`；含 `"recovery"` 时它是最后一个成员 |
| T11 | `TestContractVersionMatchesGenerated` | `release/versions.json` 与 `ContractVersion` 一致且为 `3` |
| T12 | `TestRecoveryRejectedOnRequest` | 反向对照：请求体带 `recovery` 必须被严格接入拒绝（`invalid_request`），证明服务端只写不读（I10） |

T9 与 T12 是反向对照：去掉实现（或把只读误做成可写）后必须失败，防止“测试跟着实现走”。
T1 的表必须按 §6.1 的 50 个 `.WithNext(...)` 与 46 个 `.Next =`/`Next:` 写入逐点枚举，不能只按
`Code*` 常量枚举——`app_launch_failed`、`display_unavailable` 这类码在不同路径上可达不同结论。

### 8.2 实机/系统级

| 场景 | 操作 | 验收 |
| --- | --- | --- |
| 静态拒绝 | `act` 目标节点已删除 | 409（`node_not_found`）/ 422（`ambiguous_point`）+ `retry/O/effective`；同一 `O` 换 `P` 重试成功 |
| 后置观察失败 | 构造树超限 | 503、`outcome=executed`、`cause=post_observation_failed`、`observe/P/retired` |
| 不确定 | 断开服务与 App 的传输后 `act` | 504、`status/P/retired`、`end` 后 `cleanup.execution=unconfirmed` |
| 收尾未完成 | `end` 后立即 `status` | `status` + `cleanup_pending` |
| `start` 撤销分支 | 虚拟屏上露出控制 UI 后 `start` | 503、`session.state=ended`、带 `cleanup`、`status/P/revoked/cleanup_pending`（不是 `observe`） |
| 契约不匹配 | 用 `X-Mobile-Use-Contract: 2` 调 `/api/act` | 409 `contract_mismatch` + `update/none` |
| 成功路径 | 正常 `act` | 无 `recovery` 字段 |

无设备时明确写“未执行”，不得用单测代替。

### 8.3 记录

写入 `maintenance/verification/structured-recovery-entry.md`：实现提交、构建命令、设备型号与
系统版本、每条场景的真实响应片段（脱敏）、未执行项与原因。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 是否保留 `next` | 不保留，不做镜像，单版本迁移 |
| `recovery` 形状 | 四成员对象，置于 `error` 之后 |
| `entry` 取值 | `retry`/`observe`/`status`/`end`/`user`/`update` |
| `reference` 取值 | `P`/`O`/`none`；`retry` 必须是 `O` |
| `input_state` 词汇 | 与 `status(O)` 完全一致 |
| `cause` 取值 | 四值，仅在无 `error` 时出现 |
| `contract_version` | 2→3，规则强制（[release.md:42-44](../../docs/release.md#L42-L44)：响应形状与核心枚举同时改变） |
| 控制 App | 不解析 `recovery`；仅随契约版本重建 |
| `recovery` 是否进控制同步响应 | 否；控制协议不是工具结果，实现时加断言 |
| `status(self)` 无 `start_operation_id` 时 | `reference=none`（I2 允许） |
| `shell` | 不产生 `retry`；`root_unavailable` 是唯一 `user`，`control_revoked`/`session_ended`/`session_expired` 是 `end` |
| `android_version_code` 为何前进 | [release.md:26-44](../../docs/release.md#L26-L44) 与 `scripts/generate-version.ps1`、`scripts/verify-manifest.ps1` 只要求 `versions.json` / `AndroidManifest.xml` / 清单三者一致，没有“契约前进就必须升 versionCode”的规则；本次 App 二进制与编译期 `CONTRACT_VERSION` 都变，升到 4 是为了让已安装产物可区分，不是契约要求 |
| `inspection_too_large` 为何省略 | `inspect` 读的是已发布证据，O 可能已 `retired`，给 `retry/O` 会违反 I3；唯一出路是改 `node`/`query` |
| `release_version` | 0.4.0 → 0.4.1（patch）。需求已指定 patch 级别；`release_version` 只标识产物，与整数契约号无关（[release.md:30-31](../../docs/release.md#L30-L31)） |
| 迁移窗口 | 整组强制升级，不提供 `next` 兼容读取；回退只能整组回退 |
| U1 `diagnose` 聚合 | 已由人确认不纳入本提案：`cause` 只进响应；`diagnose` 把 `op.Phase=post_observation_failed` 纳入 Issues 作为实现提交后的独立变更，唯一改动点是 [diagnose.go:207](../../internal/engine/diagnose.go#L207) 的筛选条件（该阶段已由 [operations.go:139-140](../../internal/engine/operations.go#L139-L140) 落库） |

### 9.2 未决

无。评审时已关闭全部未决项：原 U1（`diagnose` 聚合）按人确认不纳入本提案，见 §9.1；
`release_version`、`android_version_code`、迁移窗口与 `inspection_too_large` 的取舍也已关闭，
同样见 §9.1。

## 10. 实施顺序

1. **协议层**（可并行）：`Recovery` 结构、`AccessRecovery`、编码/解码、删除 `Next`。
2. **引擎层**：新增 `recovery.go`；逐工具替换写入点；删除全部 `WithNext`。
3. **接入层**：`httpapi`、`mcp` 投影与兜底。
4. **测试**：T1–T12 与 10 个测试文件里的 32 处既有引用迁移。
5. **版本与生成**：`release/versions.json`（按 §9.1 结论）→ 跑 `scripts/generate-version.ps1` → 校验生成产物。
6. **文档**：§6.5 七个文件，过 AGENTS.md 第 15 条独立只读审计。
7. **实机验证**：§8.2 七个场景，写验证记录。

1–3 可并行但必须同一次提交；4 与 5 依赖 1–3；6 依赖 4–5；7 依赖 6。

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/structured-recovery-entry.md`

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- 不改变任何请求字段、请求的严格 JSON 接入规则与认证。
- 不改变 `outcome`、`observation.status`、`session.state`、`cleanup.*` 的语义与取值。
- 不改变超时、预算、settle 策略、重验顺序与派发线性化点。
- 不改变 `uncertain` 的终态性质，不引入重试、重放或回滚。
- 不改变 MCP 工具集合、名称、参数 schema 与图片载体。
- 不改变控制 App 的同步协议与 STOP 最小载体。
- 不改变日志白名单与事件集合。
