# 提案：压缩 MCP 结果外壳、缩短 O/P 标识、解析精确恢复引用并调整截图长边至 1024

状态：提案，未实现。
基线提交：`6fe8d8bd1b408a5e7bb045d2d3394e7b8b67c994`（分支 `master`，`2026-11-22`）。
适用范围：MCP `text` / `both` 结果文本投影（stdio 与 Streamable HTTP）、服务与客户端生成的真实 `O`/`P` 标识、MCP 发现说明与工具描述、所有截屏输出（`start`/`observe`/`wait_for`/`act`/`snap` 附图）。
影响范围：`internal/engine`、`internal/client`、`internal/imaging`、`internal/mcp`、`scripts/mcp-probe`、`release/versions.json`、正式文档 `docs/` 及发布产物。
契约（`/api/*` 字段与 `contract_version`）：**不改变**（共同 `contract_version` 保持 `21`，`/api/*` JSON、控制端同步协议、`structured` 模式与 `diagnostic v1` 均不变）。MCP 文本呈现升级为 **`result v4`**，正式解除 `text` 模式与 `structured` JSON 之间“逐字段 1:1 无损回写”的约束；普通观察树沿用 `tree v4`，详细补看沿用 `inspect v3`。

本文引用的函数名、行号、常量与默认值均以基线提交 `6fe8d8b` 为准；实施前若 `HEAD` 已前进，须重新核对 `internal/mcp/result_text.go`、`internal/mcp/server.go`、`internal/mcp/schema.go`、`internal/engine/engine.go`、`internal/client/client.go`、`internal/imaging/imaging.go` 与 `scripts/mcp-probe/`。

---

## 0. 摘要

本提案落地研究报告（`.state/dsh-session-2dd44960/research/mcp-context-research-0.24.0.md`）中的两项 P0 级改造，并同步补齐精确恢复引用与工具说明配套：

1. **结果外壳与短 ID 瘦身（`result v4` + `O4821_85` / `P7391_12`）**：打破 MCP 文本必须无损复刻全部静态/审计字段的旧约束，将每步结果外壳改为单行紧凑流，把真实 `O`/`P` 标识缩短为 4 位无前导零数字前缀加单调序号；按实机轨迹测算，**外壳 token 直砍约 60%，工具入参 token 直砍约 51%，合计节省约 2.7 万 token（双向总 I/O 下降 ~13.9%）**。
2. **`recovery` 精确参考操作 `ref`**：MCP 适配器在不保存跨调用状态的前提下，依据单次调用的请求引用与服务返回事实，把 `recovery` 中的抽象占位词 `O`/`P` 确定性解析为可直接抄用的真实编号（如 `recovery entry=observe ref=P7391_1`），无法 100% 证明安全时严格回退为抽象词 `O`/`P`，消除模型回翻历史找编号的摩擦。
3. **发现说明与工具描述配套同步**：同步更新 `internal/mcp/schema.go` 的 `treeTextGuide`、`callerGuidance` 及受影响工具描述，修正 `shell` 描述里引用已省略字段 `observation.screen.display_id` 的过时指引，避免说明与新呈现范式冲突。
4. **截图长边上限 `ImageMaxLongSide` 从 `1200` 调至 `1024`**：两处常量改动让 `1080×2400` 截屏从 `540×1200` 降至 `461×1024`，直接避开视觉模型 512px 切片悬崖，单张截图 token 在 DeepSeek/Claude/新 OpenAI 降 **~27%**，在 GPT-4o/4.1 降 **62%（6 片→2 片）**。

本提案**不做**树内容净体裁剪、差分门槛调整、坐标精简或宿主端历史折叠（这些归入后续独立提案）。

---

## 1. 问题与证据链

### 1.1 现象与代价

基于 DSH 实机 4-App 长会话研究报告（`0.24.0`，204 次 mobile-use 调用，166 棵观察树）：

- **外壳“固定税”过高**：在 `0.24.0` 已榨干无损语法压缩红利之后，非树外壳仍消耗 **37,723 token**（占总输出 20.1%），调用入参消耗 **8,696 token**。其中 **60.5% 的外壳 token 和 51.4% 的入参 token** 来自每步原地复读的 41 字符 UUID 式 `O`、25 字符十六进制 `P`、纳秒级 `captured_at` / `actionable_until`、每帧重复的 `origin` / `screen` / `status=complete` / 正常 `quality` 三行，以及 `app find` 冗长的 JSON 数组。
- **`recovery` 缺少精确编号**：现行 `recovery` 只返回抽象类型 `reference=O|P|none`（`internal/protocol/responses.go:610-615`），出错时模型仍需自己去翻前面步骤或从多个 `P` 字段里辨认该传哪个编号，容易在 `device_busy`、`observe/P`、`end/P` 场景传错。
- **截图尺寸踩中视觉切片悬崖**：现行 `ImageMaxLongSide=1200` 将常见 `1080×2400` 竖屏缩为 `540×1200`，宽 `540 > 512` 且高 `1200 > 1024`，在按 512px 切片的视觉编码器下直接从 2 片暴涨到 6 片（多付 2.6 倍视觉 token），在按像素面积计费的模型下也多付 ~37% 像素。

### 1.2 源码证据链

| 现行行为 | 源码位置 |
| --- | --- |
| `result v3` 强制逐字段无损编码并配 JSON Pointer 兜底 | `internal/mcp/result_text.go:28-77`、`196-259`、`309-315` |
| MCP 适配器无跨调用状态，按调用动态投影 `text`/`both`/`structured` | `internal/mcp/server.go:91-119`、`internal/mcp/result_projection.go:5-38` |
| 引擎生成 `O<ServiceInstanceID>_<seq>` 与 `P<ServiceInstanceID>_<seq>`（41+ 字符） | `internal/engine/engine.go:982-991` |
| 客户端 `NewOperationID()` 生成 `P` + 24 位小写十六进制（25 字符） | `internal/client/client.go:678-684` |
| `validOperationID` 已接受 `P` + 2..64 位字母数字/`-`/`_`；`v4ShortDeltaBase` 按最后 `_` 前缀匹配 | `internal/engine/operations.go:14-31`、`internal/client/client.go:687-702`、`internal/mcp/tree_text.go:114-140` |
| Android 控制端严格把控制同步与 STOP 中的 `service_instance_id` 按 36 字符 UUID 解析 | `android-control/app/src/main/java/com/mobileuse/vnext/ServiceClient.java:195`、`238` |
| 引擎时间格式化使用 `RFC3339Nano`，且有单测依赖亚秒精度 | `internal/engine/engine.go:1053-1058`、`internal/engine/lease_test.go:29`、`internal/engine/quality_test.go:487` |
| `request_closed=true` 仅在 `end`/`status`/已封闭重放返回，普通成功调用从不出现 | `internal/engine/end.go:75,96,203,225`、`internal/engine/status.go:102,116`、`internal/engine/operations.go:81-84` |
| 发现说明与 13 工具描述集中定义在 `schema.go`，且 `shell` 描述直接指引读取 `observation.screen.display_id` | `internal/mcp/schema.go:28-137`、`663` |
| 截图长边上限常量硬编码 `1200`（未暴露 CLI/配置文件参数） | `internal/imaging/imaging.go:35`、`internal/engine/engine.go:216` |

### 1.3 四个容易踩中的直觉陷阱（本提案明确规避）

1. **不能直接把 `ServiceInstanceID` 改成 4 位短串**：Android 控制 App 在 `ServiceClient.java:195,238` 用 `uuid(...)` 强校验 `service_instance_id`，缩短它会直接把控制同步和 STOP 通道打挂。正确做法是保持 `ServiceInstanceID` 为完整 36 字符 UUID，仅在引擎内部派生一个 4 位数字标签专门用于拼接 `O`/`P`。
2. **不能直接去掉引擎 `formatTime` 的纳秒**：`/api/*` 契约与既有引擎单测（如 `lease_test.go`、`quality_test.go`）依赖亚秒时间戳比较。正确做法是引擎继续输出 `RFC3339Nano`，仅在 MCP `result v4` 文本投影层截断为 UTC `HH:MM:SS`。
3. **不能把 `request_closed=true` 当作“默认噪声”省略**：报告里误将其归入常规重复字段，但实查代码确认 `request_closed` 仅出现在 `end` 封闭、已封闭 `P` 重放和 `status(P)` 封闭查询，属于关键控制事实，必须原样保留。
4. **不能对 `recovery` 盲填当前 `op` 或优先填 `session.origin`**：
   - 跨 Agent `device_busy` 未暴露 `blocking_operation_id` 时若盲填自身 `op`，会误导模型跟踪被拒的空操作；
   - `act`/`shell` 返回 `uncertain` 或 `executed_revoked` 且附带已结束 `session` 时，若优先填 `session.origin` 会丢掉当前未决操作的 `op`；
   - 无 `session` 的 `session_ended`/`session_expired`/`control_revoked` 下若对 `end/P` 盲填 `op`，在已启动新会话时存在误杀新会话的高危风险（`operations.go:100-101` 会在准入瞬间把 `op.SessionOrigin` 绑到当前活动会话）。

---

## 2. 目标与非目标

### 目标

1. MCP `text`/`both` 输出全面切换到 `result v4` 紧凑流格式，覆盖全部 13 个工具及其正常、降级、错误、取消、三态与空集合分支。
2. 真实 `O`/`P` 标识统一采用 4 位无前导零数字前缀 + 单调序号（`O4821_85`、`P7391_12`），MCP 不维护伪别名映射表。
3. `result v4` 的 `recovery` 行在确定安全时直接给出精确目标 `O`/`P` 编号，不确定时严格回退为抽象词 `O`/`P`/`none`。
4. `internal/mcp/schema.go` 的发现说明和工具描述与 `result v4` 呈现范式 100% 一致，消除过时字段指引。
5. `ImageMaxLongSide` 默认上限从 `1200` 调整为 `1024`。
6. `internal/mcp` 单测与 `scripts/mcp-probe` 同步升级为 `result v4` 语义一致性校验，确保任何未来新增的 `protocol.Result` 字段都必须经过显式分类（投影或声明省略），不会静默丢失。

### 非目标

- 不修改 `/api/*` HTTP 字段名、枚举值、错误码与 `contract_version`（保持 `21`）。
- 不修改 `structured` 模式的完整 JSON 载体与 `diagnostic v1` 失败诊断文本。
- 不修改 `tree v4`、`inspect v3`、`shell v1`、`timeline v1` 块内语法，不做树节点净体裁剪或坐标精简。
- 不修改差分 70% 门槛，不在 MCP 适配器内保存跨调用会话状态。
- 不修改 Android 控制 App 业务代码（仅通过 `scripts/generate-version.ps1` 递增版本号并随发布包整组构建）。

---

## 3. 路线对比与核心取舍

### 3.1 MCP 文本是否打破与 `structured` 的 1:1 无损等价

| 路线 | 做法 | 收益 | 代价与风险 |
| --- | --- | --- | --- |
| A. 维持 1:1 无损 | 只改分隔符与缩进，保留所有字段 | 文本可逐字节还原为 `Result` JSON | `0.24.0` 已触底，继续抠语法几乎无收益 |
| **B. 正式分层（选定）** | `/api/*` 与 `structured` 保持完整无损；MCP `text`/`both` 的 `result v4` 面向模型可读性与上下文效率，显式省略固定/冗余元信息 | 砍掉 ~60% 外壳 token；未来演进不再被“无损回写”捆死 | 纯 `text` 消费者无法从单条文本还原纳秒时间戳、`actionable_until`、`revision`、`session.agent` 等字段；需要完整审计数据时明确走 `structured` 或 `both` 模式 |

**决策理由**：MCP `text` 通道的核心读者是大模型，不是机器审计归档。需要完整机器结构的调用方本来就有 `structured`/`both` 模式和 `/api/*` HTTP 接口；让 `text` 摆脱无损包袱，才拿得到实质的上下文红利。

### 3.2 短 `O`/`P` 标识方案

| 路线 | 做法 | 收益 | 代价与风险 |
| --- | --- | --- | --- |
| A. MCP 伪别名表 | 后端保留长 UUID/24-hex，MCP 维护 `O1↔UUID` 映射 | 后端零改动 | 违反 MCP 适配器无状态设计；跨进程/重启/并发重启即失步 |
| **B. 真实短 ID：`O4821_85` + `P7391_12`（选定）** | 服务与客户端直接生成 4 位无前导零数字前缀 + `_` + 递增序号；`ServiceInstanceID` 保持 36 字符 UUID | 零状态、双向同省（输出 + 入参合计省 ~1.2 万 token）、重启后天然带实例区分、`v4ShortDeltaBase` 零改动复用 | 4 位空间共 9,000 个取值：无账本冷启动或仅 MCP 重启而服务未重启时，存在 `1/9000`（0.011%）跨重启前缀撞车概率 |

**决策理由**：4 位数字前缀（`1000..9999`）在长度极短（仅 8–9 字符）与跨重启辨识度之间取得最佳平衡。对设备服务重启，我们在恢复账本载入时主动避让旧实例标签，将常规带账本重启的碰撞率压到 **0%**；对仅 MCP 重启场景，`0.011%` 概率在单设备个人手机工具上完全可接受。

### 3.3 `recovery` 精确引用方案

| 路线 | 做法 | 收益 | 代价与风险 |
| --- | --- | --- | --- |
| A. 保持抽象 `O`/`P` | `recovery` 只写类型词，模型自己找编号 | 零逻辑风险 | 每次出错模型都要在长上下文里检索和判断该传哪个 ID，容易用错 |
| **B. 路线 R1：MCP 单次调用无状态确定性解析（选定）** | 不改 `contract_version`，MCP 在单次 `execution` 中带上本次 `requestRef`，按白名单确定性把 `O`/`P` 解析为真实 ID；不满足条件严格回退 `O`/`P` | 覆盖 95%+ 高频恢复场景，零契约破坏、零跨调用状态、无误杀风险 | 极少数无上下文失败（如跨 Agent `device_busy`、无 `session` 的 `session_ended`）仍显示抽象 `P`，需在工具说明中写明 |
| C. 引擎改契约新增 `recovery.target_id` | 提升 `contract_version` 至 22，由引擎直接返回精确 ID | 连无 `session` 边缘分支也可在引擎内补全 | 触发整组协议升级与 App 协议同步，收益与改动面不成比例 |

**决策理由**：路线 R1 在不升 `contract_version` 的前提下吃掉了绝大部分体验红利，而且每条边界都有明确安全红线，未来如真有必要再平滑演进也不冲突。

---

## 4. 详细设计规范

### 4.1 短真实 `O`/`P` 标识生成规则

1. **引擎侧（`internal/engine/engine.go`）**：
   - `Config.ServiceInstanceID` **继续保持完整 36 字符 UUID**，控制同步、STOP 响应、`/health`、账本 `service_instance_id` 字段一律不变。
   - `Engine` 新增内部字段 `shortInstanceTag string`（4 位十进制字符串，范围 `"1000"`–`"9999"`）：
     - `deriveShortInstanceTag(instanceID string) int`：当 `instanceID != ""` 时取 `SHA-256(instanceID)` 前 4 字节大端 `uint32 % 9000 + 1000`；当 `instanceID == ""` 时从 `crypto/rand` 取 `[1000, 9999]` 均匀随机值（读随机失败则回退 `time.Now().UnixNano()`）。
     - **账本重启避让（精确落点 `internal/engine/control.go:457-464`）**：`cmd/device-service/main.go:99` 在启动 HTTP 服务前执行 `StartRecovery(ctx)`；在 `e.store.Load()` 返回后的 `e.mu.Lock()` 临界区内（与 `restoreRecoveryLocked(record)` 同锁，早于 `e.recovering` 解除及任何 `O`/`P` 分配），调用 `avoidLedgerInstanceTagsLocked(record)`。空账本（`record == nil`）直接跳过；非空账本时，收集旧主记录 `record.ServiceInstanceID`、全部多条退休记录 `record.Retired[*].Record.ServiceInstanceID`（与当前 `ServiceInstanceID` 不同者）派生的 4 位标签，以及各记录 `Session.Origin` 中形如 `P<4位数字>_...` 的 4 位标签；仅在 `e.nextObservation == 0 && e.nextOperation == 0` 时，若当前 `shortInstanceTag` 命中该集合，则按 `(tag - 1000 + 1) % 9000 + 1000` 递增避开（最多迭代 9,000 次）。由此保证正常带账本重启的相邻实例标签碰撞率为 **0%**，且运行期绝不中途改变前缀。
   - `nextObservationIDLocked()` 返回 `"O" + e.shortInstanceTag + "_" + strconv.FormatInt(e.nextObservation, 10)`（例：`O4821_85`）。
   - `nextOperationIDLocked()` 返回 `"P" + e.shortInstanceTag + "_" + strconv.FormatInt(e.nextOperation, 10)`（例：`P4821_12`）。
   - 兼容性核验：`v4ShortDeltaBase`（`internal/mcp/tree_text.go:114-140`）要求最后一个 `_` 前前缀逐字节一致且尾号为正十进制整数，`O4821_85` 与 `O4821_72` 自动产出 `base=_72`；`validOperationID`（`internal/engine/operations.go:14-31`）要求 `P` 开头且总长 2..64 位，`P4821_12` 原生通过。

2. **客户端侧（`internal/client/client.go`）**：
   - `NewOperationID()` 改为进程级惰性初始化 4 位前缀 + 原子递增序号：
     - 首次调用在 `sync.Mutex` 保护下，用 `crypto/rand` 通过无偏拒绝采样（或 `uint64 % 9000` 在 64 位空间偏差 `< 10^-15`）生成 `[1000, 9999]` 的 4 位标签并缓存；若 `crypto/rand` 读取失败，返回错误（与现行失败行为一致，不静默降级）。
     - 每次调用以 `atomic.Uint64.Add(1)` 取得序号，返回 `"P" + prefix + "_" + strconv.FormatUint(seq, 10)`（例：`P7391_12`）。
   - 性质：单 MCP 进程生命周期内碰撞率为 **0%**，并发调用完全线程安全且无锁竞争（初始化后纯原子自增）。

3. **单测防抖（`internal/engine/retirement_isolation_test.go:228`）**：
   - `TestGeneratedReferencesDoNotRebindAfterRestart` 现用两个随机 `NewUUID()` 直接比对，在 4 位标签下有 `1/9000` 的 CI 偶发失败风险。改为使用两个确认派生出不同标签的固定 UUID 测试隔离性，并新增一条专门测试“两代服务实例即便刻意构造为同 4 位派生标签，在载入旧账本后仍会自动避让、不复用旧 `O`/`P`”的确定性用例。

---

### 4.2 `result v4` 完整紧凑流文法

`result v4` 仅用于 MCP `text` 与 `both` 模式的文本内容（`internal/mcp/result_text.go`），行尾统一 `\n`，禁止裸 `\r` 与多余空行。

#### 4.2.1 头行与尾行

```text
result v4 <ok|err> [op=<P>] [state=<operation_state>] [outcome=<outcome>] [phase=<phase>]
...
end result
```

- `<ok|err>`：`isError == false` 写 `ok`，`isError == true` 写 `err`。
- `op=<P>`：只要 `operation_id != ""` 一律保留（不省略）。
- `state=<operation_state>`：非空时输出（如 `running`、`finished`）。
- `outcome=<outcome>`：**仅当 `!isError && outcome == "executed" && operation_state == ""` 时省略**；凡错误结果、非 `executed`（`not_executed`/`uncertain`）、或带有 `operation_state` 的 `status(P)`/幂等重放摘要，一律显式输出 `outcome=...`。
- `phase=<phase>`：非空时输出。
- **显示但不授权原则**：头行中的 `op=` 仅作为本次调用的操作编号参考，正文中的 `at=` 仅作为 UTC 秒级时序提示；二者均不替代或放宽 `internal/engine` 对会话归属、显示代次与期限的权威校验。

#### 4.2.2 显式省略清单（仍完整保留在 `/api/*` 与 `structured` JSON）

| 原 JSON 路径 | `result v4` 处理 | 理由 |
| --- | --- | --- |
| `outcome`（仅 `!isError && outcome=="executed" && operation_state==""`） | 省略 | 头行 `ok` 已唯一确定常规执行成功 |
| `observation.status`（仅 `"complete"`） | 省略 | 默认正常态；`partial`/`failed` 或未知值一律显式输出 |
| `observation.origin` | 省略 | 同会话恒为 `start` 时的 `session.origin`，避免每帧重复 |
| `observation.revision` | 省略 | 内部递增版本号，调用方操作与恢复均不依赖 |
| `observation.actionable_until` | 省略 | 固定规则为 `captured_at + 60s`（受会话租期截断），由 `serverInstructions` 统一说明；超时由服务端精确裁定 |
| `observation.quality`（当 `state=="stable"`，或 `state=="unknown" && stop_reason=="not_sampled"`） | 省略整对象 | 正常稳定或未采样无需刷屏；`unstable(...)` 与其他 `unknown(...)` 均显式输出 |
| `observation.quality.samples` | 始终省略 | 非默认质量行只输出 `quality=<state>(<stop_reason>)`，采样次数仅供审计，保留在 `structured` JSON |
| `observation.screen`（当顶层无非空 `session` 且 `observation.tree != nil` 时省略宽高与 `display_id`） | 条件省略 | 正常带树帧可由 `start` 首帧及 `root` 得知尺寸；但 **`rotation != 0` 永远不省**，输出 `rot=<1\|2\|3>` |
| `observation.image.media_type` / `image.media_type`（仅 `"image/jpeg"`） | 省略 | 固定格式 JPEG；若出现非 JPEG 则输出 `media=<type>` |
| `session.agent` | 省略 | 单 Agent 固定名称，无决策价值 |
| `inspection.origin` | 省略 | 补看头已带 `observation=<O>`，不重复会话起点 |
| 所有时间戳的日期与纳秒部分 | 截断为 UTC `HH:MM:SS` | 模型只需秒级时序与相对间隔；非合法 RFC3339 值原样保留不丢数据 |

> **硬边界**：除上表列出的项目外，其余所有 `protocol.Result` 成员均在 `result v4` 中明确投影，绝不静默丢弃。尤其是 `request_closed`、`target_reference`、`blocking_operation_id`、`can_start`、`blocking_code`、`start_operation_id`、`observation_state`、`input_state`、`effect`、`text_input`、`wait`、`cleanup`、`error`、`recovery` 及 `app` 的全部三态/空集合语义，100% 保留。

#### 4.2.2a 统一词法、转义与异常兜底规则

- **双引号字符串负载**：语法中所有用双引号标示的文本负载（`error` / `*.error` 的 `<message>`、`diagnosis` 的 `summary`、`app_items` 的 `<name>`）均采用标准 JSON 字符串编码（`encoding/json` 单行双引号字符串，严格转义 `\n`、`\r`、`\"`、`\\` 及控制字符，允许空串 `""`），绝不会拆行或伪造成其他键值。
- **token 安全校验**：所有不带引号的 token 位置（如 `<code>`、`<state>`、`<pkg>`、`<O>`、`<P>`）须满足 `resultToken`（非空且不含空白、`=`、`"`、`\` 或控制字符）；若出现不合法 token，该成员回退走 `json v1 path="..." value=...`。
- **意外 `null` 与扩展字段兜底**：除显式允许的 `session=null` 与可空三态字段（`state_preserved`、`restarted`、`recents_cleaned`）外，任何意外为 `null`、类型不匹配、或在 `app.items` / `app.tasks` / 嵌套对象中携带未来未定义字段的值，均回退输出 `json v1 path="<RFC6901 pointer>" value=<compact JSON>`，确保前向兼容不丢信息。

#### 4.2.3 逐对象紧凑流语法（固定顺序）

1. **动作与状态控制标量**（各自仅在存在时单行输出）：
   - `effect=<state>`
   - `text_input write=<write> enter=<enter>`
   - `target_reference=<ref>`
   - `request_closed=<true|false>`（永不省略）
   - `blocking_operation_id=<P>`
   - `can_start=<true|false>`
   - `blocking_code=<code>`
   - `start_operation_id=<P>`
   - `observation_state=<state>`
   - `input_state=<state>`

2. **通知聚合行（`notify` 工具：`message_id` / `saved` / `notification`）**：
   - 只要三者至少存在其一，合并为一行：
     `notify [id=<message_id>] [saved=<true|false>] [state=<notification.state>]`
   - 若 `notification.error != nil`，紧跟子行：`notify.error <code> "<message>"`（有 `details` 时补 `notify.error.details=<compact JSON>`）。

3. **顶层独立截图（`snap` 工具：`image`）**：
   - `image [<W>x<H>] [read_only] [media=<non-jpeg>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [at=<HH:MM:SS>]`
   - 注：`display_id == nil` 时不写 `@<display_id>`（严禁把 `nil` 当 `0`）；`image.error != nil` 时紧跟 `image.error <code> "<message>"`（及可选 `image.error.details=<JSON>`）。

4. **会话行（`session`）**：
   - 存在且为显式 `null`：`session=null`
   - 非空对象：`session <state> mode=<mode> origin=<origin> [started=<HH:MM:SS>] [expires=<HH:MM:SS>] [ended=<HH:MM:SS>] [reason=<reason>]`

5. **观察行与子块（`observation`）**：
   - 单行摘要：
     `obs [<id>] [status=<partial|failed|...>] [fg=<compact>] [at=<HH:MM:SS>] [quality=<state>(<stop_reason>)] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=<a,b>]`
   - **解析与非空约束**：`obs ` 后首个字段不含 `=` 时必为 `<id>`，其余字段均为 `key=value`；`status=partial` 与 `status=failed` 永不省略（例如采集失败且无 `id` 时输出 `obs status=failed missing=tree`，绝不会伪装为空结果）。若异常/合成数据中出现 `status=="complete"` 且 `id==""` 并导致整行无其他字段，必须显式输出 `obs status=complete`（禁止产出裸 `obs` 空行）。
   - `fg` 编码规则：
     - `state=="present"`：`package` 与 `task_id` 均有 → `<package>#<task_id>`；仅 `package` → `<package>`；仅 `task_id` → `#<task_id>`；皆无 → `present`。
     - `state=="none"` → `none`。
     - `state=="unknown"` → `unknown(<reason>)`（`reason` 为空时写 `unknown`）。
     - 若 `package` 或 `reason` 含空白、`=`、`"`、`\` 或括号，用 JSON 字符串转义或回退 `json v1`，保证词法无歧义。
   - `screen` 与 `rot` 无状态规则：
     - 仅当顶层 `result` 携带非空 `session`（例如 `start` 首帧）**或** `observation.tree == nil`（无树降级/失败帧）时，输出 `screen=<W>x<H>[@<display_id>]`。
     - 无论 `screen=` 是否省略，只要 `screen != nil && screen.rotation != 0`，一律输出 `rot=<rotation>`（Android `Surface.ROTATION_*` 为 `1|2|3`）。
   - `obs` 附带子行（仅在对应字段存在时紧跟输出）：
     - `obs.image [<W>x<H>] [read_only] [media=<non-jpeg>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [at=<HH:MM:SS>]`（与顶层 `image` 共用同一渲染器，按字段存在条件输出）
     - `obs.image.error <code> "<message>"`（及可选 `obs.image.error.details=<JSON>`）
     - `obs.error <code> "<message>"`（及可选 `obs.error.details=<JSON>`）
   - 若 `observation.tree != nil`：空一行 + **沿用现行 `tree v4 ... end tree`**。

6. **详细补看（`inspection`）**：
   - 不再输出前置 `inspection.origin=...` 行；空一行 + **沿用现行 `inspect v3 ... end inspect`**。

7. **应用生命周期（`app`）**：
   - 单行头：
     `app <kind> [pkg=<package>] [task=<task_id>] [src_task=<source_task_id>] [src_display=<source_display_id>] [display=<display_id>] [ownership=<ownership>] [reused=<true|false>] [state_preserved=<true|false|null>] [fg_restore=<foreground_restore>] [complete=<true|false>] [target_pids=<JSON数组>] [killed_pids=<JSON数组>] [remaining_pids=<JSON数组>] [restarted=<true|false|null>] [total=<n>] [truncated=<true|false>] [fg=<compact>] [at=<HH:MM:SS>]`
   - **三态与空数组保真**：`state_preserved`、`restarted` 仅在 JSON 缺席时省略，显式 `null`/`false`/`true` 原样输出；`target_pids`/`killed_pids`/`remaining_pids` 仅在 JSON 缺席时省略，存在时写紧凑整数数组（如 `[101,102]` 或 `[]`），严格区分“缺席”与“空数组”。
   - **`app.items` 正式块**（仅当 JSON 中 `items` 字段存在时输出；存在且为空数组时仍输出 `count=0` 块，区分“查询无结果”与“未获得集合”）：
     - `kind=find`（空一行开始）：
       ```text
       app_items v1 kind=find count=<n>
       <package> "<name>"
       end app_items
       ```
     - `kind=list`（空一行开始）：
       ```text
       app_items v1 kind=list count=<n>
       #<task_id> <package> <ownership>[ origin_display=<d>]
       end app_items
       ```
   - **`app.tasks` 正式块**（`kind=close`，仅当 JSON 中 `tasks` 字段存在时输出，空数组同样输出 `count=0` 块）：
     ```text
     app_tasks v1 count=<n>
     #<task_id> <outcome>[ recents_cleaned=<true|false|null>][ error=<code> "<message>"[ details=<compact JSON>]]
     end app_tasks
     ```

8. **受管命令（`shell`）**：空一行 + **沿用现行 `shell v1 ... end shell`**。

9. **条件等待（`wait`）**：
   - `wait trigger=<trigger> [matched=<a,b>] elapsed=<elapsed_ms>ms`

10. **诊断（`diagnosis`）**：
    - `diagnosis summary="<summary>" coverage=<coverage> truncated=<true|false> [op=<operation_id>] [outcome=<outcome>]`
    - `diagnosis.issues=<compact JSON>`（若存在且非空，或保留 `[]` 以显式表明无问题）
    - `diagnosis.timing_ms=<compact JSON>`（若存在）
    - 若 `timeline` 非空：空一行 + **沿用现行 `timeline v1 ... end timeline`**。

11. **收尾清理（`cleanup`）**：
    - `cleanup <state> [exec=<execution>] [tasks=<tasks>] [recents=<recents>] [display=<display>]`
    - 若 `cleanup.error != nil`：`cleanup.error <code> "<message>"`（及可选 `cleanup.error.details=<JSON>`）。

12. **顶层错误（`error`）**：
    - `error <code> "<message>"`
    - 若 `error.details != nil`：`error.details=<compact JSON>`

13. **恢复指引（`recovery`）**：
    - `recovery entry=<entry> ref=<exact O/P|O|P|none> [input=<input_state>] [cause=<cause>]`（解析规则见 §4.3）。

14. **未知/未来扩展字段兜底**：
    - 任何未登记字段或不可安全词法化的值，沿用 `json v1 path="<RFC6901 pointer>" value=<compact JSON>`。

#### 4.2.4 典型输出前后对比

**常规 `act` 成功帧（最高频场景）**：

```text
# 优化前（result v3，14 行外壳）
result v3 is_error=false
operation_id=Pc970182be2c17026ce1adf98
outcome=executed
observation
 status=complete
 id=O04821a7b-3f12-4c9a-9e1d-8a2b3c4d5e6f_85
 origin=Pc970182be2c17026ce1adf98
 revision=85
 captured_at=2026-11-21T22:08:55.867164298Z
 actionable_until=2026-11-21T22:09:55.867164298Z
 quality.state=stable
 quality.samples=1
 quality.stop_reason=stable
 screen={"width":1080,"height":2400,"rotation":0,"display_id":148}
 foreground={"state":"present","package":"tv.danmaku.bili","task_id":177}

tree v4 delta base=_72 changes=3
...
end tree
end result

# 优化后（result v4，仅 2 行外壳）
result v4 ok op=P7391_12
obs O4821_85 fg=tv.danmaku.bili#177 at=22:08:55

tree v4 delta base=_72 changes=3
...
end tree
end result
```

**`start` 首帧（自动带出 `session` 与 `screen`）**：

```text
result v4 ok op=P7391_1
session active mode=virtual origin=P7391_1 started=22:05:10 expires=22:10:10
obs O4821_1 fg=none at=22:05:11 screen=1080x2400@148

tree v4 full nodes=42
...
end tree
end result
```

**`app find` 查询（紧凑块替代冗长 JSON 数组）**：

```text
result v4 ok op=P7391_3
app find total=2 truncated=false at=22:05:20

app_items v1 kind=find count=2
tv.danmaku.bili "哔哩哔哩"
com.zhihu.android "知乎"
end app_items
end result
```

---

### 4.3 `recovery` 精确参考操作 `ref` 解析规范（路线 R1）

#### 数据流（零跨调用状态）

- 在 `internal/mcp/server.go` 的单次调用结构 `preparedCall` 与 `execution` 中增加一个只活在本次调用内的 `requestRef string`（由 `prepareCall` 从已通过严格解码的请求中提取：`status`/`observe`/`snap`/`wait_for`/`act`/`inspect`/`keepalive`/`end`/`app(list/open/claim/handoff/close/kill)`/`shell`/`diagnose` 的 `Reference`）。
- `projectExecution` 仅在调用 `encodeResultText` 生成 `result v4` 时把 `e.requestRef` 传入文本编码器；**不修改 `e.result.Recovery.Reference`**，确保 `/api/*` 与 `structured` JSON 中的 `recovery.reference` 仍严格保持 `"O"|"P"|"none"`。

#### 确定性解析算法

记辅助谓词：`isObsID(s)` 表示 `len(s) >= 2 && s[0] == 'O'` 且满足 `resultToken(s)`；`isOpID(s)` 表示 `len(s) >= 2 && s[0] == 'P'` 且满足 `resultToken(s)`；`errCode` 优先取 `result.Error.Code`，当顶层 `result.Error == nil` 且 `result.Observation != nil && result.Observation.Error != nil` 时取 `result.Observation.Error.Code`（覆盖 `observe.go:78-89` 与 `wait_for.go:167-182` 采集失败把错误放在 `observation.error` 的真实路径），否则为空串。

1. **`recovery.Reference == "none"`** → 恒返回 `"none"`。
2. **`entry == "retry" && reference == "O"`**：
   - 若 `result.Observation != nil && isObsID(result.Observation.ID)`（例如 `act` 重验失败并交付新观察 `O`）→ `result.Observation.ID`；
   - 否则若 `isObsID(requestRef)` → `requestRef`；
   - 否则回退 `"O"`。
3. **`(entry == "status" || entry == "end") && reference == "O"`**：
   - 若 `isObsID(result.TargetReference)` → `result.TargetReference`；
   - 否则若 `isObsID(requestRef)` → `requestRef`；
   - 否则回退 `"O"`。
4. **`entry == "status" && reference == "P"`**（按严格优先级逐条判定）：
   1. **设备忙优先**：若 `errCode == "device_busy"`，有 `isOpID(result.BlockingOperationID)` 则返回 `result.BlockingOperationID`，**否则立即回退 `"P"`**（红线 1：跨 Agent 忙时绝不填自身 `op` 或 `session.origin`）。
   2. **查询/引用目标异常**：若 `errCode` 属于 `{"reference_has_no_session", "operation_unknown", "operation_not_found"}`，或者（`errCode == "request_closed"` 且 `result.RequestClosed == nil`）：有 `isOpID(result.TargetReference)` 返回它，否则有 `isOpID(requestRef)` 返回 `requestRef`，否则回退 `"P"`（如 `observe(P_old)` 报 `reference_has_no_session`，应 `status(P_old)` 查旧会话终态，而非查失败 `observe` 自己的 `op`）。
   3. **当前未决/撤销/封闭/待清理操作优先于 `session.origin`**：若 `isOpID(result.OperationID)` 且满足以下任一条件，返回 `result.OperationID`：
      - `result.Outcome == "uncertain"`
      - `result.OperationState == "running"`
      - `result.Phase == "executed_revoked"`
      - `result.RequestClosed != nil && *result.RequestClosed`（自身 `op` 被封闭或 `status(P)` 封闭结果）
      - `recovery.Cause` 属于 `{"running", "executed_revoked", "cleanup_pending"}`
      - `errCode` 属于 `{"call_cancelled", "transport_unavailable", "invalid_response", "task_lifecycle_busy"}`
      - `result.Cleanup != nil && (result.Cleanup.State == "pending" || result.Cleanup.State == "unconfirmed")`
   4. 若 `isOpID(result.StartOperation)` → `result.StartOperation`（如无参 `status()` 处于恢复或待清理状态）。
   5. 若 `result.Session != nil && isOpID(result.Session.Origin)` → `result.Session.Origin`。
   6. 若 `isOpID(result.TargetReference)` → `result.TargetReference`。
   7. 否则回退 `"P"`（红线 4：被准入拒绝的 `start` 报 `recovering`/`device_unavailable`/`ledger_unavailable` 等时，绝不返回被拒 `start` 自身的 `op`）。
5. **`entry == "observe" && reference == "P"`**：
   1. 若 `result.Session != nil && isOpID(result.Session.Origin)` → `result.Session.Origin`（覆盖 `status(O)` 过期/已消费/已退役 与 `start` 首帧失败）。
   2. 否则若 `isOpID(requestRef)` 且 `errCode` 不属于 `{"operation_unknown", "operation_not_found", "request_closed", "reference_has_no_session"}` → `requestRef`（如 `observe(P_origin)` 采集失败，直接给出原 `P_origin`）。
   3. 否则若 `isOpID(result.OperationID)` 且满足以下任一已由引擎证明绑定到当前活动会话的条件，返回 `result.OperationID`：
      - `recovery.Cause == "post_observation_failed"` 或 `result.Phase == "post_observation_failed"`
      - `result.Outcome == "uncertain"`（如已认领 `text` 动作返回 `observe/P`）
      - `errCode` 属于 `{"observation_expired", "observation_claimed", "observation_retired", "hierarchy_unavailable", "stale_screen", "tree_too_large", "control_ui_on_virtual_display", "display_unavailable"}`（均已通过活动会话校验：`act.go:254-281`、`app.go:116-127`、`snap.go:132-148`）。
   4. 否则回退 `"P"`（红线 2：对 `observation_not_found`、`no_ready_observation`、`content_unavailable` 严禁填 `op`）。
6. **`entry == "end" && reference == "P"`**：
   1. 若 `result.Error != nil && result.Error.Details != nil`，且 `details["start_operation_id"]` 为合法 `isOpID` 字符串（`start` 报 `session_exists` 同 Agent 场景，`start.go:36-38`）→ 该 `start_operation_id`。
   2. 否则若 `result.Session != nil && isOpID(result.Session.Origin)` → `result.Session.Origin`。
   3. 否则若 `isOpID(result.StartOperation)` → `result.StartOperation`。
   4. 否则若 `isOpID(requestRef)` → `requestRef`。
   5. 否则回退 `"P"`（红线 3：无 `session` 的 `session_ended`/`session_expired`/`control_revoked` 严禁填 `op`，防止误结束已开启的新会话）。

#### 特殊路径核验（`wait_for` / `keepalive` / `shell` / 取消 / 幂等重放）

- **`wait_for` 与 `observe` 采集失败**：`wait_for.go:161-183` 与 `observe.go:72-90` 均已通过会话校验，失败时把错误写在 `observation.error`（顶层 `result.Error == nil`）且 `recovery` 为 `observe/P`；上述 `errCode` 提取规则可准确命中 `captureFailureCodes` 并解析为 `op`（若原请求传入 `P` 则先命中 `requestRef`）。
- **`keepalive` 与 `shell`**：`keepalive.go:62-105` 与 `shell.go:390-431` 均先解析并校验 `reference` 归属再判定会话结束/过期；`shell` 运行超时或 `uncertain` 命中 `status/P` → `op`；`keepalive`/`shell` 报 `session_ended`/`session_expired` 命中 `end/P` → `session.origin`（或已校验的 `requestRef`）。
- **调用取消（`server.go:239-369`）**：工作工具取消时由适配器生成 `call_cancelled` 与 `status/P`（或融合 `end` 的 `operation_unknown`/`cleanup_pending`），本规则解析为 `ref=<op>`，且绝不把 `cancelled` 或 `uncertain` 提升为 `executed`；`app(find/list)` 与只读工具取消时无 `recovery`，不输出恢复行。
- **幂等重放与 `status(P)`**：重放结果带有 `operation_state`，头行始终保留 `state=` 与显式 `outcome=`；恢复引用仅按返回快照中的事实解析，后续调用仍须完整通过引擎的原会话/显示代次/期限校验。

---

### 4.4 工具描述与发现说明配套同步（`internal/mcp/schema.go`）

为避免说明与 `result v4` 产生误导或矛盾，以下位置须同步更新：

1. **`treeTextGuide`（`schema.go:28-85`）**：
   - 将结果头尾图例更新为 `result v4 <ok|err> [op=…] [state=…] [outcome=…] [phase=…]` … `end result`，说明常规成功省略 `outcome=executed`。
   - 将观察组图例更新为单行 `obs <O> [status=…] [fg=…] [at=HH:MM:SS] [quality=…] [screen=W×H[@id]] [rot=1|2|3] [missing=…]`，说明所有 `HH:MM:SS` 均为 UTC 秒级时间、`screen` 仅在带 `session` 或无树帧输出、`rot` 在非零旋转时始终输出、`origin`/`revision`/`actionable_until`/默认 `quality` 仅在 `structured` JSON 中保留。
   - 补充 `app_items v1` 与 `app_tasks v1` 块语法，以及 `recovery entry=… ref=…`（已解析为精确 `O`/`P` 编号或抽象词 `O`/`P`/`none`）。
   - 更新内嵌示例中的短 ID（如 `O4821_72` / `O4821_76` / `P7391_4`），去掉过时的 `inspection.origin=` 示例行。
2. **`callerGuidance`（`schema.go:93-125`）**：
   - 将“O 的 `observation.actionable_until` 是其输入期限”改为说明“O 默认约 60 秒输入期限（以会话租期截断），文本省略该字段，精确截止时间见 `structured` 或以服务端过期反馈为准”。
   - 将 `recovery` 规则更新为：“优先直接使用 `recovery` 给出的精确 `ref=` 编号调用对应 `entry`；当 `ref=` 为抽象词 `O`/`P` 时，再按场景取对应 `O`、`op`、`blocking_operation_id`、`start_operation_id` 或 `session.origin`”。
3. **逐工具描述同步**：
   - `start`（L144）：把“从 `observation` 组的 `id` 取得首个完整 O”改为“从 `obs` 行取得首个完整 `O` 与会话屏 `screen=<W>x<H>@<display_id>`，租期以 `session` 的 `expires` 为准”。
   - `status`（L163）：对齐 `result v4` 的 `state=`/`can_start=`/`blocking_operation_id=`/`recovery ref=`。
   - `observe` / `wait_for` / `act`（L179 / L243 / L322）：将 `recovery.entry=observe、recovery.reference=P` 对齐为 `recovery entry=observe ref=<P>` 并保持与 `structured` 字段语义一致。
   - `snap`（L226）：对齐单行 `image <W>x<H> read_only screen=…` 描述。
   - `keepalive`（L502）：将“不延长 O 的 `actionable_until`”改为“不延长 O 的输入期限（60 秒）或工作期限”。
   - `end` / `app` / `notify`：同步 `session`、`app_items`/`app_tasks`、`notify` 行表述。
   - **`shell`（L663，关键修正）**：把“该 id 见 `observation.screen.display_id`”改为“该 `display_id` 见 `start` 首帧 `obs` 行的 `screen=<W>x<H>@<display_id>`（也可从 `snap` 的 `image` 或 `app(list)` 的 `display=` 取得）”。
4. **目录指纹联动**：工具描述改动会自然更新 `CatalogFingerprint()`（`internal/mcp/catalog.go:59`），构建清单与 `mcp-probe` 同步使用新指纹，按 `AGENTS.md` #19 核验。

---

### 4.5 截图长边上限 `ImageMaxLongSide`：`1200 → 1024`

- `internal/imaging/imaging.go:35`：`defaultMaxLongSide = 1024`（原 `1200`）。
- `internal/engine/engine.go:216`：`ImageMaxLongSide: 1024`（原 `1200`）。
- 缩放行为保持不变：等比缩小且永不放大（`1080×2400` 按长边 1024 缩放后为 `461×1024`；横屏 `2400×1080` 为 `1024×461`；长边已 `≤ 1024` 的图片不缩放）。
- 配套检查并更新 `internal/imaging/imaging_test.go`、`internal/engine/image_test.go`、`internal/engine/snap_test.go:114,639-640`、`internal/engine/wait_for_test.go:640`、`internal/mcp/snap_test.go`、`docs/interface.md:383,1070-1071` 中硬编码 `1200` / `540×1200` 的断言与示例为 `1024` / `461×1024`。

---

## 5. 实现影响范围与不变式核查

### 5.1 文件改动清单

| 类别 | 文件 | 改动概要 |
| --- | --- | --- |
| 引擎短 ID 与截图上限 | `internal/engine/engine.go`、`internal/engine/control.go`（或恢复载入点） | 派生 `shortInstanceTag`、恢复账本标签避让、`nextObservationIDLocked`/`nextOperationIDLocked` 输出 `O<tag>_<seq>`/`P<tag>_<seq>`、`ImageMaxLongSide=1024` |
| 客户端短 `P` | `internal/client/client.go` | `NewOperationID()` 改为进程级 4 位随机前缀 + 原子序号 `P<tag>_<seq>` |
| 图像默认上限 | `internal/imaging/imaging.go` | `defaultMaxLongSide = 1024` |
| MCP `result v4` 与 `recovery` 解析 | `internal/mcp/server.go`、`internal/mcp/result_projection.go`、`internal/mcp/result_text.go` | 单次调用透传 `requestRef`；升级编码器为 `result v4`；实现 `recovery` 精确 `ref` 确定性解析；保持 `diagnostic v1` 与 `structured` 不变 |
| 工具发现与说明 | `internal/mcp/schema.go` | 更新 `treeTextGuide`、`callerGuidance` 及受影响工具描述（含 `shell` `display_id` 指引） |
| 单测与门禁 | `internal/engine/retirement_isolation_test.go`、`internal/engine/image_test.go`、`internal/engine/snap_test.go`、`internal/engine/wait_for_test.go`、`internal/imaging/imaging_test.go`、`internal/client/client_test.go`、`internal/mcp/result_*_test.go`、`internal/mcp/snap_test.go`、`internal/mcp/schema_test.go`、`internal/mcp/server_test.go` | 适配短 ID、1024 上限、`result v4` 解析与显式省略清单元测试门禁（`result_manifest_test.go` 改为校验“投影集合 + 声明省略集合 = 全量 `protocol.Result` 成员”）、`recovery` 精确解析全分支与红线测试（含 `observe`/`wait_for` 的 `observation.error` 路径） |
| 外部探针 | `scripts/mcp-probe/result_text.go`、`result.go`、`app_view.go`、`result_modes_test.go`、`app_matrix_test.go`、`probe_test.go` | 升级探针文本解析器为 `result v4`，在 `both` 模式下校验语义一致性与显式省略边界 |
| 版本源与生成常量 | `release/versions.json` + `scripts/generate-version.ps1` 生成文件 | `release_version`: `0.24.0 → 0.25.0`，`contract_version`: 保持 `21`，`android_version_code`: `26 → 27` |
| 正式文档 | `docs/mcp.md`、`docs/integration.md`、`docs/architecture.md`、`docs/client.md`、`docs/interface.md`、`docs/runbook.md`、`docs/release.md`、`docs/control-protocol.md`、`README.md` | 同步 `result v4` 规范与示例、短 `O`/`P` 形态、`recovery` 精确 `ref`、截图 `1024` 上限、`shell` 的 `display_id` 获取说明、版本号（提交前须通过 `AGENTS.md` #15 独立只读文档审计） |

### 5.2 与既有硬约束（`AGENTS.md`）的关系

| 硬约束 | 影响与核查结论 |
| --- | --- |
| #1 唯一权威 | 无破坏。MCP 不持跨调用状态，`requestRef` 仅存单次调用栈；`O`/`P` 裁决仍在 `internal/engine`。 |
| #3 单向消费 / #4 条件化提交 | 无破坏。仅缩短 `O`/`P` 字符串形态，不改变状态机与校验逻辑。 |
| #6 严格接入 / #7 三态与省略 | 无破坏。`/api/*` JSON 不变；`result v4` 对 `state_preserved`/`restarted`/`recents_cleaned` 及 `app.items`/`app.tasks`/`*_pids` 严格区分“缺席 / `null` / `false` / 空集合”。 |
| #8 事实分层 | 无破坏。`outcome`、`obs.status`、`session`、`cleanup`、`recovery` 各自独立表达，错误与非 `executed` 永不省略 `outcome`。 |
| #9 恢复屏障 | 增强。`StartRecovery` 载入旧账本后对 4 位实例标签执行避让，防止相邻重启实例标签重合。 |
| #10 日志白名单 | 无破坏。日志继续只记 ID、事件与枚举，不记录正文。 |
| #13 单一版本源 | 遵循。仅手改 `release/versions.json`（`0.25.0` / `21` / `27`），其余由脚本生成并经清单核验。 |

---

## 6. 预期效果、代价与能力边界

### 6.1 预期收益

| 优化项 | 预期收益 | 依据 |
| --- | --- | --- |
| 短真实 `O`/`P` 标识（`O4821_85` + `P7391_12`） | 204 步长会话双向节省约 **1.1 万～1.2 万 token**（输出 + 入参合计） | 研究报告 `O85` + 6 位 `P` 实测省 12,162 token；本方案多 4 位数字前缀（每条多 ~1 token），保留绝大多数红利 |
| `result v4` 紧凑外壳 + `app_items`/`app_tasks` | 外壳输出再省约 **1.5 万～1.6 万 token**（外壳总降幅 **~60%**） | 研究报告实测数据（剔除误判的 `request_closed=true`，实际几乎无差额） |
| `recovery` 精确 `ref` | 消除模型出错时回查历史找 `O`/`P` 的思考消耗与传错率 | 覆盖重试、过期重采、设备忙跟踪、未决跟踪、重名会话关闭等核心异常 |
| `ImageMaxLongSide` `1200 → 1024` | DeepSeek/Claude/新 OpenAI 单图省 **~27%**；GPT-4o/4.1 单图省 **62%（6 片→2 片）** | 1080×2400 实测 `540×1200 → 461×1024` |

### 6.2 代价与风险（明确接受与缓解）

| 风险 / 代价 | 影响程度 | 缓解方式 |
| --- | --- | --- |
| 纯 `text` 消费者不再与 `structured` 1:1 无损等价 | 预期设计取舍 | 文档与 `serverInstructions` 明确声明分层；需要完整机器审计字段的调用方使用 `structured`/`both` 模式或 `/api/*` |
| 已有解析 `result v3` 的外部脚本会失效 | 仅限直接解析文本外壳的客户端 | `result v4` 明确升版号（头行 `result v4 ok\|err`），旧解析器可立刻识别版本变化；仓库内 `mcp-probe` 同步升级 |
| 仅重启 MCP（服务未重启）时 `P<4位>_1` 有 `1/9000`（0.011%）概率撞上服务内存里的历史 `P` | 极低（个人单机场景可接受） | 单进程内 0% 碰撞；服务端常规带账本重启通过旧标签避让达到 0% 碰撞 |
| 截图宽从 `540px` 降至 `461px`（缩 14.7%） | 极小字号可读性略降 | 常规 UI 按钮与正文完全清晰，且操作以无障碍树文本为主、截图为辅 |
| `both` 模式下 `text` 省略字段与 `structured` 存在预期差异 | 需更新测试与探针口径 | `result_manifest_test.go` 与 `mcp-probe` 改为“显式白名单投影 + 显式声明省略”双集合完备校验 |

### 6.3 明确不能治的问题

| 现象 | 原因 | 后续归属 |
| --- | --- | --- |
| 观察树正文仍占总输出 ~80% | 本提案不动树节点结构与裁剪策略 | 下一份独立提案（树净体六件套 + 差分 85% 门槛） |
| 200 步长对话累计上下文滚雪球 | 历史树文本停留在宿主对话窗口中 | 宿主端历史树/截图滑动折叠 |
| 页面重访导致 73.5% 仍是整树 | 单基线机制在切页后 `ref` 断连 | 后续 P2 页面记忆保号与多基线池 |

---

## 7. 验证计划与发布交付范围

### 7.1 单元测试与反向对照

| 用例类别 | 验收内容 |
| --- | --- |
| 短 ID 生成与避让 | 验证 `O<4位>_<seq>` / `P<4位>_<seq>` 格式、无前导零、`v4ShortDeltaBase` 自动缩写 `base=_<seq>`、`client.NewOperationID()` 并发无碰撞、恢复账本命中同标签时确定性避让、Android 控制端 `service_instance_id` 仍为 36 字符 UUID |
| `result v4` 编码与显式省略门禁 | 覆盖全部 13 工具及正常/错误/降级/取消/三态（`null`/`false`/`true`/缺席）/空集合（`count=0`、`[]`）/非零 `rotation`/未知字段 `json v1` 兜底；`TestResultFieldManifest` 反向对照：新增未分类字段时必须失败 |
| `recovery` 精确 `ref` 全矩阵与四条红线 | 逐条验证 `retry/O`、`status/O`、`end/O`、`status/P`、`observe/P`、`end/P` 精确命中，以及 4 条安全红线（跨 Agent `device_busy`、`observation_not_found`、无 `session` 的 `session_ended`、被拒 `start`）必须回退抽象 `P`；反向对照：`act` `uncertain` 带 `session` 时不得误填 `session.origin` |
| 截图尺寸上限 | `1080×2400` 编码输出精确为 `461×1024`，`2400×1080` 输出 `1024×461`，`800×600` 保持原尺寸不放大 |
| 发现说明与探针 | `internal/mcp/schema_test.go`、`catalog_test.go` 与 `scripts/mcp-probe` 全套单测通过，确认无残留 `result v3` 或 `observation.screen.display_id` 过时引用 |

### 7.2 发布交付默认范围（遵循 `AGENTS.md` #19）

本提案涉及发行产物变化（`0.25.0` / `contract_version=21` / `android_version_code=27`），实施阶段完成后必须交付三项内容并记录至 `maintenance/verification/compact-result-envelope-and-image-sizing.md`：

1. **手机整组覆盖部署**：执行 `scripts/build-release.ps1` 与 `scripts/verify-manifest.ps1`，核验 `build/release/manifest.json` 后覆盖部署至手机（含手机侧 `device-mcp` 与 `control.apk`，保留 App 私有配对与配置数据）。
2. **覆盖变更行为的最小实机闭环**：在实机上验证短 `O`/`P` 标识、`start` 首帧 `obs` 带 `screen=<W>x<H>@<display_id>`、常规 `act`/`observe` 输出 `result v4` 紧凑外壳、至少一条 `recovery` 精确 `ref` 路径、`app(find/list)` 紧凑块，以及一次截屏输出分辨率确为长边 `≤ 1024`（`461×1024`）。
3. **Windows 主机产物替换与双入口探针验证**：基于同一干净源码提交构建并替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验主机清单后用本次新 `mcp-probe.exe` 分别验证 **Windows 主机 stdio MCP** 与 **手机 Streamable HTTP MCP**（记录中严格区分磁盘替换、独立进程验证与宿主重连交接）。

---

## 8. 已定、未决与实施顺序

### 8.1 已定

| 事项 | 结论 |
| --- | --- |
| 打破 `text` 与 `structured` 无损等价 | 已定：升级 `result v4`，保持 `contract_version=21` 与 `structured` 完整 JSON |
| 短 ID 形态 | 已定：`O4821_85` + `P7391_12`（4 位无前导零数字前缀 + `_` + 递增序号） |
| 保留 `op=` 与 `at=` | 已定：保留 `op=<P>`（所有带 `operation_id` 调用）与 `at=<HH:MM:SS>`（UTC 秒级） |
| `recovery` 精确 `ref` | 已定：路线 R1，MCP 单次调用无状态确定性解析 + 四条安全红线 |
| 配套工具说明同步 | 已定：同步 `schema.go` 发现说明与全部受影响工具描述 |
| `ImageMaxLongSide` | 已定：`1200 → 1024` |
| 版本号规划 | 已定：`release_version=0.25.0`、`contract_version=21`、`android_version_code=27` |

### 8.2 未决

无（所有设计与边界已在确认前收敛关闭；实施中如遇未预见的外部兼容冲突再单独确认）。

### 8.3 实施顺序

1. **底层短 ID 与截图上限**（可并行）：更新 `internal/engine`（`shortInstanceTag`、恢复避让、`ImageMaxLongSide=1024`）、`internal/client`（`NewOperationID`）、`internal/imaging`（`defaultMaxLongSide=1024`）及对应单测。
2. **MCP `result v4` 与精确 `recovery`**（依赖步骤 1 的短 ID 样例，也可并行开发）：更新 `internal/mcp/server.go`、`result_projection.go`、`result_text.go`、`schema.go` 及全部 `internal/mcp/*_test.go`。
3. **外部探针适配**：更新 `scripts/mcp-probe/` 解析器、语义校验与单测。
4. **版本源与正式文档**：更新 `release/versions.json`，执行 `scripts/generate-version.ps1`，同步 `docs/` 与 `README.md`，通过 `AGENTS.md` #15 独立只读文档审计。
5. **交叉编译、构建发布、实机闭环与双入口探针验收**：按 §7.2 完成三项交付，写入验证记录并提交实现；实现提交落盘后另行提交提案归档（补写 §9 实现结果并移入 `maintenance/proposals/implemented/`）。

---

## 9. 实现结果（归档时补写）

待实施完成后按 `AGENTS.md` #16 补写实现提交 hash、验证记录链接、已落地项及与提案差异项，并移入 `maintenance/proposals/implemented/`。

---

## 附：本提案不改变的事项

- `/api/*` HTTP 路径、请求/响应 JSON 字段结构、错误码枚举与 `contract_version=21`。
- Android 控制端同步协议、`service_instance_id` 的 36 字符 UUID 格式与最小 STOP 载体。
- MCP `structured` 模式的完整 `Result` JSON 与 `diagnostic v1` 诊断文本。
- `tree v4`、`inspect v3`、`shell v1`、`timeline v1` 块内语法、树节点裁剪策略与差分 70% 门槛。
- 引擎内部 `formatTime` 的 `RFC3339Nano` 精度、会话租期、O 的 60 秒输入期限与单向消费状态机。
- 私有恢复账本格式版本与日志白名单策略。
