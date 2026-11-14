# 提案：新增条件等待与稳定采集工具 `wait_for`

状态：已实现（实现提交 `a077210f191836d5ad24f0862349c525dd16225a`，`2026-11-14`）。
实现与验证记录：[wait-for.md](../../verification/wait-for.md)。
基线提交：`a14962d5df8a52ff3fc06a1e5ba9adfddbbb5ecd`（分支 `master`，`2026-11-14`）。
适用范围：MCP 与 `/api/*` 新增第 12 个工具 `wait_for`（作用于当前会话的普通工作流等待、条件检测与新 `O` 交付）。
影响范围：`internal/protocol`、`internal/engine`、`internal/httpapi`、`internal/client`、`internal/mcp`、`internal/eventlog`、`cmd/device-service`、`android-control`、`release/versions.json`、`scripts/toolcatalog`、`scripts/mcp-probe`、`scripts/verify-m5.ps1`、`docs/`（`scripts/verify-m6.ps1` 不在本次改动与执行范围）。
契约（`/api/*` 字段与 `contract_version`）：改变（新增 `/api/wait_for`、`Result.Wait`、`observation.quality.stop_reason` 扩展 `"condition"`/`"timeout"`、第 12 个 MCP 工具 `wait_for`、`WorkWaitFor` 控制端类别；`contract_version` 由 `11` 升至 `12`，`release_version` 由 `0.11.0` 升至 `0.12.0`，`android_version_code` 由 `13` 升至 `14`）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/engine/observe.go`、`internal/engine/engine.go`、
`internal/engine/settle.go`、`internal/engine/capture.go`、`internal/engine/operations.go`、
`internal/engine/retirement.go`、`internal/android/device.go`、`cmd/device-service/main.go`、
`cmd/device-service/config.go`、`internal/protocol/requests.go`、`internal/protocol/responses.go`、
`internal/protocol/errors.go`、`internal/httpapi/server.go`、`internal/mcp/schema.go`、
`internal/mcp/server.go`、`internal/mcp/result_text.go`、`internal/mcp/server_test.go`、
`internal/mcp/result_parity_test.go`、`android-control/app/src/main/java/com/mobileuse/vnext/ControlState.java`）。

---

## 0. 摘要

当前 `act` / `start` / `open_app` 的内置收敛窗口仅 1.5s–3s，`observe` 无稳定判定且 `wait_ms` 上限仅 10s 且只能盲等；遇到冷启动、登录校验、上传导出、异步遮罩等长耗时流程时，Agent 只能外层循环反复调用 `observe`（并穿插 `inspect`），持续消耗设备 dump、Token 上下文与调度延迟，且在慢思考间隔后可能遭遇 `SessionTTL=120s` 过期。

本提案新增独立正交工具 `wait_for(reference, query?, absent_query?, timeout, stable?, full?, image?)`：以 `query`、`absent_query`（可选搭配连续 3 帧 `stable` 门禁）与必选的 `timeout`（`1..40` 秒，严守 `90s` 同步 HTTP 天花板与阶段 C 最坏 `35s` 采集余量）构成并行 OR 退出条件。阶段 B 任一分支命中即冻结退出原因 `trigger` 并停止等待，进入阶段 C 验证采集并发布新 `O`；`timeout` 到期同样作为合法睡眠分支返回 HTTP `200`（`mcp.isError=false`）、与最终发布帧对齐的 `observation.quality` 及 `Result.Wait`（`trigger` / `matched` / `elapsed_ms`）。

本提案采用**路线 Y**（不修改现有 `act` / `app.open` 动作永远发布后置观察 `O2` 的行为）；同时在 §7 中明确路线 Y 的独占工作位/投递槽与冗余树代价，以及未来配套路线 X（动作增加跳过后观察 `observe=false`）的演进效果与边界。不涉及 `act`/`app` 跳过后观察改造，不开放 `package`/正则/多选择器，不抬高 `WriteTimeout (90s)` 与 `client.defaultTimeout (90s)`。

---

## 1. 问题

### 1.1 现象与代价

1. **内置 `settle` 无法覆盖超过 3 秒的异步流程**：页面加载、表单异步提交、登录跳转或渲染动画超过 2–3 秒时，动作返回的是中间态（例如 `"加载中..."` / 转圈遮罩）的 `O`。
2. **外层轮询 `observe` 带来高 Token 与往返延迟**：`observe(wait_ms=...)` 不会因页面提前就绪而提前返回；Agent 每次试探性调用 `observe` 都会把一帧新树（及可选截图）送回模型上下文，经历完整 LLM 推理往返后才能再决定是否继续等，3 秒的等待常被外层轮询放大成十余秒和数千 Token 消耗。
3. **缺少“消失等待”与更严格收敛能力**：现有工具无法原语级等待某个阻塞文案消失（`absent_query`），也无法在观察阶段要求连续 3 帧树摘要一致。
4. **空闲思考叠加等待触发会话到期风险**：`inspect` 与 `app.find/list` 不续约会话租约；若会话剩余租约仅剩数十秒而调用长等待，未提前在单锁块内原子校验并续租会在等待中途被后台 `housekeeping` 以 `lease_expired` 撤销会话。

### 1.2 证据链

1. **动作收敛上限为 1.5s–3s，`observe` 无稳定检查**：
   - `DefaultSettlePolicies()`（[settle.go:45-51](../../../internal/engine/settle.go#L45-L51)）固定 `Start` 为 `MaxBudget: 1.5s`（静默窗口 `300ms`）、`Act` 为 `MaxBudget: 2s`（静默窗口 `400ms`）、`OpenApp` 为 `MaxBudget: 3s`（静默窗口 `600ms`），且 `MinSamples: 2`。
   - `settlePolicy`（[settle.go:95-108](../../../internal/engine/settle.go#L95-L108)）对 `"observe"` 返回空 `SettlePolicy{}`，即 `observe` 完全不做稳定性判断。
2. **`observe.wait_ms` 为定长盲等，上限 10 秒**：
   - `MaxWaitMS = 10000`（[requests.go:15](../../../internal/protocol/requests.go#L15)），`DecodeObserve` 限定 `0..10000`（[requests.go:172-174](../../../internal/protocol/requests.go#L172-L174)）。
   - `Observe` 中等待逻辑为单一 `time.NewTimer(wait_ms)` 阻塞到点才采集（[observe.go:51-60](../../../internal/engine/observe.go#L51-L60)），既不提前退出也不检测树内容。
3. **既有查询匹配与无副作用投影原语已存在，可安全复用**：
   - `nodeMatchesQuery` 与 `foldText`（[inspect.go:262-325](../../../internal/engine/inspect.go#L262-L325)）对 `Label`、`Value`、`Hint`、`Resource` 执行 Unicode 大小写折叠子串匹配，并受公开树隐私裁剪约束；另 `internal/protocol` 仅有导出函数 `Fold`（[surface.go:1386-1409](../../../internal/protocol/surface.go#L1386-L1409)），二者并不等价，不能跨包混用。
   - `Projector.Preview`（[build.go:175-179](../../../internal/observation/build.go#L175-L179)）可在不修改 `prev` 基线与 `nextRef` 计数器的前提下投影候选树。
4. **现有 `settleUntilStable` 计的是总采样数而非连续相同帧数**：
   - `settleUntilStable`（[settle.go:233](../../../internal/engine/settle.go#L233)）的判断条件为 `next.digest == run.frame.digest && run.samples >= policy.MinSamples`。若第 1、2 帧不同而第 2、3 帧相同，`samples == 3` 时仅代表最后连续 2 帧一致，并非连续 3 帧一致。
5. **会话租约后台每 1 秒收割一次**：
   - `DefaultConfig` 中 `SessionTTL = 120s`、`ObservationLifetime = 60s`、`WorkTimeout = 20s`、`CaptureMaxAge = 15s`（[engine.go:123-127](../../../internal/engine/engine.go#L123-L127)）。
   - `StartClock` 每秒触发 `housekeeping`（[engine.go:735](../../../internal/engine/engine.go#L735)），只要 `!sessionBeforeDeadline(e.session, now)` 就立刻 `revokeSessionLocked("lease_expired", now)` 并取消运行中 `work`（[engine.go:762-764](../../../internal/engine/engine.go#L762-L764)）；`workAuthorizedLocked`（[operations.go:331-337](../../../internal/engine/operations.go#L331-L337)）以及 `publishTreeAttempt` 的前置检查（[capture.go:249-253](../../../internal/engine/capture.go#L249-L253)）和提交检查（[capture.go:281-284](../../../internal/engine/capture.go#L281-L284)）同样强制校验该期限，直到 `publishTreeAttempt` 尾段才调用 `extendSessionLocked`（[capture.go:336-340](../../../internal/engine/capture.go#L336-L340)）。
6. **传输超时、设备单步采集上限、投递槽与工作上下文现状**：
   - `Device.captureTimeout()` 对单次 hierarchy dump 设有 `15s` 下限（[device.go:921-926](../../../internal/android/device.go#L921-L926)），在默认 `ADB.TimeoutSeconds = 8`（[config.go:71-73](../../../cmd/device-service/config.go#L71-L73)）下即为 `15s`；阶段 C 全套采集最坏累计可达 `ProbeDisplay(5s)`（[capture.go:111](../../../internal/engine/capture.go#L111)）+ `CaptureImage(10s)`（[capture.go:554](../../../internal/engine/capture.go#L554)）+ `verifyImageTarget(5s)`（[capture.go:569](../../../internal/engine/capture.go#L569)）+ `captureTree(15s)` = **`35s`**（无图最坏 `20s`）。
   - `httpapi.Server.WriteTimeout = 90s`（[server.go:72](../../../internal/httpapi/server.go#L72)）与 `client.defaultTimeout = 90s`（[client.go:26](../../../internal/client/client.go#L26)）当前相等；`/api/observe` 在进入引擎前抢占 `deliveryLimiter`（[server.go:216-232](../../../internal/httpapi/server.go#L216-L232)），而投递槽上限仅为 `deliveryMaxActive = 2`、`deliveryMaxBytes = 4 MiB`（[delivery.go:16-18](../../../internal/httpapi/delivery.go#L16-L18)）。
   - 通用 `acquireWork` 使用 `context.Background()`（[operations.go:254](../../../internal/engine/operations.go#L254)），HTTP 客户端断开不会自动取消工作；只有 `acquireAppQueryWork` 继承了请求上下文（[app.go:342-366](../../../internal/engine/app.go#L342-L366)）。
   - 控制端 `ServiceClient` 强校验 `CONTRACT_VERSION`（[ServiceClient.java:256](../../../android-control/app/src/main/java/com/mobileuse/vnext/ServiceClient.java#L256)），`ControlState.workText`（[ControlState.java:343](../../../android-control/app/src/main/java/com/mobileuse/vnext/ControlState.java#L343)）与 `phaseLabel`（[ControlState.java:385](../../../android-control/app/src/main/java/com/mobileuse/vnext/ControlState.java#L385)）只对枚举字面值做精确匹配。
   - 参数非法统一映射 HTTP `422 invalid_request`（[errors.go:170-175](../../../internal/protocol/errors.go#L170-L175) 与 [errors.go:212-214](../../../internal/protocol/errors.go#L212-L214)）；撤销错误码在协议线上的真实字符串为 `CodeStopped = "control_revoked"`（[errors.go:113](../../../internal/protocol/errors.go#L113)）。

### 1.3 为什么直觉会出错

1. **直觉“把条件参数直接塞进 `observe` 就行，省掉第 12 个工具”**：会让核心原子快照原语 `observe` 同时承担“即时拍一帧（毫秒盲等）”与“长时条件阻塞等待（秒级退出 + `WaitResult`）”，在工具发现层面混淆常规观察与异步等待语义。
2. **直觉“直接把 `settlePolicy` 的 `MinSamples` 设为 3 即可实现 3 帧稳定”**：`settleUntilStable` 校验的是累计采样数 `run.samples >= MinSamples` 而非连续相等帧数，必须单独追踪连续相等计数器 `consecutive == 3`。
3. **直觉“`timeout` 只要略小于 `90s`（如 `60s`）就不会撞到 HTTP 超时”**：如 §1.2 第 6 条实证，未做阶段 B 预算护航时，阶段 B 末次 dump（`15s`）+ 阶段 C 全套带图验证采集（`35s`）会在 `timeout=40s` 时就已触达 `40 + 15 + 35 = 90s`（若 `60s` 更会达 `110s`），触发客户端断链并让 MCP 按 [server.go:232-237](../../../internal/mcp/server.go#L232-L237) 下发 `end(P)` 销毁会话。
4. **直觉“`timeout` 小于 120s 就不会在等待中途撞上 `SessionTTL`”**：若前置思考或只读查询已消耗数十秒，进入等待时剩余租约可能小于 `timeout`，必须在单一状态锁内原子完成准入过期校验与单调续租。
5. **直觉“`readCapture` 能区分 `display_unavailable` 来提前中止轮询”**：`readCapture`（[capture.go:463-487](../../../internal/engine/capture.go#L463-L487)）会把所有底层抓树异常折叠成 `hierarchy_unavailable`，屏幕消失时必须通过连续失败计数触发 `ProbeDisplay` 才能早退。

---

## 2. 目标与非目标

### 目标

1. 提供聚焦、无默认隐藏等待边界的条件等待工具 `wait_for`，参数严格收敛为 `reference`、`query`、`absent_query`、`timeout`（必填，整数 `1..40` 秒）、`stable`、`full`、`image`。
2. `query`、`absent_query`、`timeout` 为并行 OR 退出分支；`stable=true` 作为 `query`/`absent_query` 命中后的连续 3 帧稳定门禁（`S-B` 方案，间隔 `600ms`，3 帧跨度 `>= 1.2s`），或在二者均省略时作为独立触发分支。
3. 严守 `90s` 同步 HTTP 天花板：`timeout <= 40s`，阶段 B 所有外部操作（含采样与早退 `ProbeDisplay`）均受 `waitDeadline` 预算前置与子 `ctx` 约束，为阶段 C 最坏 `35s` 采集留足缓冲；继承 HTTP 请求上下文，调用方断连立即释放工作位。
4. 冻结阶段 B 的退出分支为 `WaitResult.Trigger`，基于阶段 C 最终发布帧独立计算 `WaitResult.Matched`（仅含可单帧判定的分支），并将包含阶段 C 终帧在内的收敛结果投影到 `observation.quality`（与 [architecture.md:189](../../../docs/architecture.md#L189) 一致）。
5. 保护会话租约闭环：入口同一锁内拦截已过期会话（`409 session_expired`，禁复活）并单调续足 `SessionTTL=120s`，阶段 C 发布再刷新。

### 非目标

1. **不修改现有 `act` / `start` / `app` 动作的后置观察行为**（不实现 `observe=false` 跳过后观察配套，留作后续路线 X 独立提案）。
2. **不抬高全局 `httpapi.WriteTimeout (90s)`、`client.defaultTimeout (90s)` 与 `SessionTTL (120s)`**——代价是 `wait_for.timeout` 上限收口在 **`40` 秒**（见 §6.2 预算链）。
3. **不改变既有 `app.find` / `inspect` 的文本折叠规则**。
4. 不引入 `package`、正则、XPath、UIAutomator 选择器或局部子树稳定计算。
5. 不向 `eventlog`、错误信息或持久化账本写入 `query` / `absent_query` 原始文本。
6. 主机侧仅重编并原地替换 `build/` 下的 `device-mcp.exe` 与 `mcp-probe.exe`（及清单核验），不负责其他 Agent 宿主对这两个二进制的外部依赖治理与重启编排（由用户自行处理）。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持 11 个工具不变，由 Agent 外层循环调用 `observe(wait_ms)` + `inspect` | 零开发成本；零契约/文档波动 | 每次猜等都消耗全树上下文与 LLM 往返延迟；无法按“条件消失”提前返回；无 3 帧连续稳定门禁 |
| R2 扩展现有 `observe` | 在 `observe` 上叠加 `query` / `absent_query` / `stable` / `timeout` | 不增加工具总数，但仍需改 `/api/observe`、`Result` 与 `contract_version` | 单一基础抓拍工具双峰化（即时快照 vs 75s+ 长阻塞）；`wait_ms` 与 `timeout` 两套计时混淆；LLM 易误用 |
| R2b 适配器侧循环 `observe(P)` | 不改服务端/契约，在 `internal/mcp` 适配器内部循环调用 `observe(P)` 并本地匹配公开树 | 不改服务协议；工具数保持 11 个；无需升级 App 与 `contract_version` | 每轮轮询都在服务端发布并退役新 `O`（加速挤压 `MaxObservations=32` 并导致 delta 基线漂移/频繁退回 full 传输）；把条件查询业务语义从 `engine` 外溢进 MCP 适配器，**违背 `AGENTS.md` #1 唯一权威与 `/api/wait_for` HTTP 客户端复用**；每帧承受完整 JSON/base64 HTTP 往返 |
| **R3（选定，路线 Y）新增正交工具 `wait_for`** | 新增第 12 个工具 `/api/wait_for` 与 `WorkWaitFor`；保持现有动作均发布后置 `O` 不变 | `contract_version` 升至 `12`、`release_version` 升至 `0.12.0`、`android_version_code` 升至 `14`，联动 9 份文档与发布校验脚本 | 已知触发超慢冷启动的动作（如 `app.open`）仍先跑完 2–3s `settle` 并吐出一帧中间树，再进入 `wait_for`；等待期独占工作位与 1 个投递槽（见 §7.3） |
| R4（暂缓，路线 X）`wait_for` + `act`/`app` 增加 `observe=false` | 同步改造 `act`/`app` 支持跳过后观察并通过 `wait_for(P)` 接管 | 改造范围与恢复矩阵验证复杂度翻倍；调用方需事先预测动作快慢 | 盲关 `observe=false` 时遇瞬态弹窗（如权限框）会多一轮工具往返；状态与恢复入口组合数激增 |

**选择理由**：R2b 虽然表面不动公开面，但轮询期间每一帧都作为真实 `O` 进入会话历史并走 HTTP 序列化，不仅造成 `MaxObservations=32` 历史淘汰和基线破坏，更破坏“唯一权威在 `internal/engine`”的架构底线，且无法为 HTTP SDK 提供等价能力；R3 以受控的版本跃迁在引擎内实现零副作用 `Preview` 轮询与单次 `O` 提交，并自然兼容未来独立推进的路线 X。

---

## 4. 判据

### 4.1 合法性校验、文本匹配与最小可读证据

1. **输入校验（`DecodeWaitFor`，非法统一返回 `422 invalid_request`）**：
   - `reference`：必填，非 `null`，过 `checkReference`，指向当前活跃会话的 `O`（含同 epoch 已退役/过输入期 `O`）或 `P`（遵循 `resolveObserveTarget` 口径，[observe.go:96-137](../../../internal/engine/observe.go#L96-L137)）。
   - `timeout`：必填，非 `null`，整数，范围 **`1..40`**（单位：秒）；缺失、为 `null`、非整数或越界返回 `422 invalid_request`。
   - `stable` / `full` / `image`：可选布尔值，不可为 `null`，省略为 `false`。
   - **`stable=true` 下 `timeout >= 2`**：3 帧连续稳定至少跨 `2 × 600ms = 1.2s > 1s`，因此若同时传入 `stable=true` 且 `timeout < 2`，`DecodeWaitFor` 直接返回 `422 invalid_request`（不可在 1 秒内完成的矛盾组合）。
   - `query` / `absent_query`：可选字符串，不可为 `null`；若出现，Unicode 码点数必须 `<= MaxQueryRunes (256)` 且 `strings.TrimSpace(s) != ""`（注：与 `inspect` 允许空串表示“匹配所有节点”的宽松用法不同，[surface.go:429-445](../../../internal/protocol/surface.go#L429-L445)，`wait_for` 拒绝空或全空白字符串）。匹配时按**原始传入非空字符串**做 `foldText` 匹配，不做隐式裁剪以保留有意义的词间空格。
   - **矛盾查询检查**：仅当**同时传入 `query` 与 `absent_query`**（`HasQuery && HasAbsentQuery`）时，在 `DecodeWaitFor` 拒绝完全相等的 `wire.Query == wire.AbsentQuery`（`422 invalid_request`），并在 `engine` 准入前（同包可调 `foldText`）补充校验 `foldText(req.Query) == foldText(req.AbsentQuery)`，若相等则按 `finishObserveError`（附带 `error` + `recovery`、**不填 `outcome`**）返回 `422 invalid_request`。既放行 `query`/`absent_query` 均省略的纯 sleep 路径，又拦截同义折叠矛盾，且不改 `protocol.Fold` 与 `app.find` 既有行为。
2. **最小可读证据门槛（防止空白过渡帧误触发 `absent_query`）**：
   - 公开树被投影出来之后，必须至少包含 **1 个非合成根且非仅上下文装饰的可读节点**（即 `hasReadableContent(tree)`：除根节点外存在至少 1 个普通内容节点，不把“Activity 切换瞬间仅有空合成根的一帧”当作已就绪页面）。
   - 在满足上述最小可读证据的帧上，使用 `nodeMatchesQuery` 与 `foldText`（[inspect.go:262-325](../../../internal/engine/inspect.go#L262-L325)）递归检查整棵公开树（仅匹配公开节点的 `Label`、`Value`、`Hint`、`Resource`）：
     - `query` 满足：至少存在 1 个公开节点命中 `query`。
     - `absent_query` 满足：帧已通过最小可读证据门槛，且整棵公开树中**没有任何**公开节点命中 `absent_query`。
   - **可见树范围边界**：检索范围严格等于服务公布的 `reported_visible_with_context` 公开树；视口外未上报节点或被隐私策略抑制的内容**永远不会命中 `query`**，“未命中”不代表业务界面未发生变化。

### 4.2 3 帧连续稳定判据（`stable=true`）与 `observation.quality` 对齐

1. **摘要口径**：直接复用 `sampleDigestOf(&cap, projection.Tree)`（[settle.go:120-124](../../../internal/engine/settle.go#L120-L124) 与 [settle.go:297-308](../../../internal/engine/settle.go#L297-L308)），涵盖 L1 原始节点身份链、L2 规范化公开树、L3 前台包名/状态/任务与屏幕几何。
2. **连续性定义与静默窗口**：
   - 必须连续 **`Config.Wait.StableFrames = 3`** 个有效采样满足 `digest[k-2] == digest[k-1] == digest[k]`（追踪连续相等计数器 `consecutive == 3`，而非 `run.samples >= 3`）。
   - 任意相邻两帧摘要变化时 `consecutive` 重置为 `1`；中途遇到瞬态 `hierarchy_unavailable`（未采集到有效帧）时不计入有效样本且将 `consecutive` 重置为 `0`。
   - 帧间最小采样间隔设为 **`Config.Wait.MinDwell = 600ms`**（对齐 `OpenApp` 的 `600ms` 静默窗口，[settle.go:49](../../../internal/engine/settle.go#L49)），使 3 帧稳定判定至少跨越 **`1.2s`**，比 `act` 更严格且能跨过常见的过渡动画或短暂加载停顿；若单次 dump 实测耗时已超过 `Config.Wait.MinDwell`，不额外补睡（遵循 `settleUntilStable` 自适应节拍）。
3. **采样总帧数上界**：定义 **`Config.Wait.MaxSamples = 80`**（按最快 `40s / 600ms ≈ 66.7` 帧加安全余量推导），在任何真实设备上永不早于 `timeout=40s` 触发；仅作为防止异常零耗时调用方/假设备死循环的硬守卫，且**仅统计有效可读帧**（与 `settle.go:226-227` 对齐，失败帧不计入）。
4. **`observation.quality` 与最终发布帧严格对齐（[architecture.md:189](../../../docs/architecture.md#L189)）**：
   - 扩展 `QualityStop*`（[responses.go:161-168](../../../internal/protocol/responses.go#L161-L168)）增加 `QualityStopCondition = "condition"` 与 `QualityStopTimeout = "timeout"`。
   - **关键不变量**：`quality` 永远描述**最终发布出的那帧 `O`** 参与的最新比较结果：
     - **阶段 B 命中条件或稳态退出（`candidate != nil`，即 `trigger` 为 `"query"` / `"absent_query"` / `"stable"`）**：**即使 `image=true`，阶段 C 也不重采节点树**，而是在 `ProbeDisplay` 与（可选）`readImageForAcquisition` 之后直接复用 `candidate`（若截图与 `candidate.CapturedAt` 跨度超过 `ImageMaxSpan (3s)`，按既有 `staleImage` 语义丢图发布 `partial, missing=["image"]`）。因此 `quality` 与 `matched` 严格锁死在命中瞬间：`consecutive >= StableFrames` 为 `state="stable", stop_reason="stable"`，否则为 `stop_reason="condition"`（`samples >= 2` 设为 `"unstable"`，`< 2` 设为 `"unknown"`）。
     - **阶段 B 未命中条件、由超时或上限退出（`candidate == nil`，即 `trigger == "timeout"` 含 `MaxSamples` 守卫）**：阶段 C 按需先读图再 `captureTree(15s)` 取得终帧，此时按 `run.samples > 0` 分为两支：
       - **无阶段 B 有效历史帧（`run.samples == 0`）**：包括纯 sleep 及阶段 B 全部采集失败/超时未落盘任何有效可读帧的场景，固定为 `state="unknown"`, `samples=0`, `stop_reason="not_sampled"`。
       - **有阶段 B 有效历史帧（`run.samples > 0`）**：计算终帧 `final` 的 `sampleDigestOf(final)` 并与阶段 B 最后有效帧摘要 `run.lastDigest` 对比，更新 `run.samples++` 与 `consecutive`（相等则 `+1`，否则置 `1`）：
         - `consecutive >= StableFrames(3)`：`state="stable"`, `samples=run.samples`, `stop_reason="stable"`（符合 `settle.go:263-267`“稳定即报 `stable`”既有契约）；
         - 未达 3 帧稳定时：`samples >= 2` 设为 `state="unstable"`（`< 2` 设为 `state="unknown"`），`stop_reason` 仅可能为阶段 B 真实停止原因 `"timeout"` 或 `"max_samples"`（因 `"condition"` 与 `"stable"` 均已在前分支走复用路径，绝不出现 `"unstable" + "stable"` 的矛盾组合）。

### 4.3 组合退出分支（`S-B`）与 `trigger` / `matched` 解耦

| 传参组合 | 阶段 B 退出触发条件（OR 分支） | 冻结的 `wait.trigger` 取值 |
| --- | --- | --- |
| 仅 `timeout`（无查询且 `stable=false`） | 纯睡眠至 `timeout` 到期 | `"timeout"` |
| `stable=true`（无查询） | 连续 3 帧摘要一致 \| `timeout` 到期 \| `Config.Wait.MaxSamples(80)`（仅防御守卫，正常 600ms 节拍下不可达） | `"stable"` 或 `"timeout"`（触达守卫亦记 `"timeout"`） |
| 仅 `query` 或 仅 `absent_query`（`stable=false`） | 文本条件满足 \| `timeout` 到期 \| `Config.Wait.MaxSamples(80)`（仅防御守卫，正常不可达） | `"query"` / `"absent_query"` / `"timeout"` |
| `query` 与 `absent_query` 均传（`stable=false`） | `query` 满足 \| `absent_query` 满足 \| `timeout` 到期 \| `Config.Wait.MaxSamples(80)`（仅防御守卫，正常不可达） | 命中优先级 `query > absent_query > timeout` |
| 有查询且 `stable=true`（**`S-B` 门禁**） | `(query 满足 或 absent_query 满足) 且 当前连续 3 帧摘要一致` \| `timeout` 到期 \| `Config.Wait.MaxSamples(80)`（仅防御守卫，正常不可达） | `query > absent_query > timeout` |

- **`trigger` 不可改写**：`trigger` 严格表示**阶段 B 因哪个分支退出等待**（在进入阶段 C 前冻结，绝不被阶段 C 改写）。
- **`matched` 仅含可单帧判定的内容项**：在阶段 C **最终发布出的那一帧 `O`** 上独立评估当时实际满足的单帧可判定项，按固定顺序输出子集：**`["query", "absent_query", "timeout"]`**（`"timeout"` 仅当 `trigger == "timeout"` 时包含；不把时序事实 `"stable"` 混入 `matched`，是否连稳 3 帧由 `observation.quality.state == "stable"` 唯一权威表达）。若最终发布帧上没有任何一项成立或阶段 C 失败，则省略 `matched`（绝不输出 `null`）。
- **MCP 工具描述契约（必进 `internal/mcp/schema.go`）**：在 `wait_for` 描述尾部附 `treeTextGuide` 之前，用精炼的 3 句话向 Agent 说明核心契约：
  1. `timeout`（必填 `1..40` 秒，`stable=true` 时需 `>= 2`）到期仍会发布新观察并按成功返回（`is_error=false`），调用方须检查 `wait.trigger` 与 `wait.matched`（连续 3 帧稳定则由 `observation.quality.state == "stable"` 表达）；
  2. `wait.elapsed_ms` 计算自准入完成（`T0`）至终帧发布（`T2`）的全程耗时，因阶段 C 需补采/编码，实际耗时可超过 `timeout` 至多约 `35` 秒；
  3. `query`/`absent_query` 仅匹配当前公开可见树（不含视口外内容），且 `stable=true` 对整树计算摘要——若页面存在倒计时或实时进度变化请保持 `stable=false`，否则将跑满 `timeout`。

### 4.4 与既有重验/契约的关系

1. **与 `observe` 在 `session_expired` 上的显式差异**：现有 `observe` 对已过期但尚未被 `housekeeping` 撤权的会话会推进至 `workAuthorizedLocked` 并返回 `409 control_revoked`；`wait_for` 则与 `act`/`app`/`shell`/`keepalive` 对齐，在单锁原子准入内优先检查 `!sessionBeforeDeadline(session, now)`，明确拒绝 `409 session_expired`。
2. **断连释放工作槽（对齐 `app.find/list` 查询工作）**：通过新增 `acquireWaitWork` 继承调用方 HTTP `ctx`（类似 [app.go:342-366](../../../internal/engine/app.go#L342-L366)），确保客户端断开即立刻退出轮询并释放唯一设备工作槽与投递槽，不遗留僵尸占位。
3. **新 `O` 的 `actionable_until` 窗口与 `SessionTTL` 上限**：按 [capture.go:304-310](../../../internal/engine/capture.go#L304-L310)，新 `O` 的可用动作窗口计算规则为 `min(base + 60s, session.ExpiresAt)`（`captureAndPublish` 是在算出 `actionableUntil` 之后才调用 `extendSessionLocked` 刷新会话租约）。由于 `T0` 已续租到 `T0 + 120s`，即使经历最坏 `40s + 35s = 75s` 且在阶段 C 重新采集（`base ≈ T0 + 75s`），该帧新 `O` 的 `actionable_until` 受限于 `T0 + 120s` 时最多为 **`45s`**（而非完整 `60s`）；若复用阶段 B 较早帧，`base` 还可再少 up to `15s`。属既有 `capture.go` 一致契约，后续若调 `observe`/`act` 即可立刻刷回完整窗口。

---

## 5. 算法

### 5.1 时间轴与每帧预算门禁（全链路 `< 90s`）

```text
请求到达 → httpapi: DecodeWaitFor → delivery.acquire(tree:256KiB / image:1MiB)
            │
            ▼ [阶段 A: 单锁原子准入与占位 (<50ms)]
            ├─ registerOperation 后立即 setOperationReference(op, referenceKind(req.Reference))（对齐 observe.go:30）
            ├─ acquireWaitWork(op, agent, req.Reference, parentCtx, workTimeout):
            │  单一 e.mu.Lock() 内完成：resolveObserveTargetLocked + SessionActive
            │  + !sessionBeforeDeadline (否则 409 session_expired，附带完整 input_state)
            │  + ordinaryBarrierLocked & work==nil (否则 409 device_busy)
            │  + 分配 WorkWaitFor (Ctx = WithTimeout(parentCtx, timeout+40s), <= 80s)
            │  + confirmBaselineLocked(若适用) + extendSessionLocked(now, SessionTTL=120s)
            │  + work.Phase 置 "wait_for.poll"，op.Phase 置 "wait"（轮询期不再重复写相态避免控制广播）
            ▼ T0（准入完成，等待计时起点）
          [阶段 B: 等待与条件监测期 (0 ~ timeout 秒，含早退 ProbeDisplay 均受 waitDeadline 截断)]
            ├─ 路径 B1（纯 sleep：无 query/absent_query 且 stable=false）：
            │    if !pauseInterruptible(work.Ctx, timeout) → 立即退出返回 409 control_revoked
            │    否则到期冻结 trigger="timeout"，出 B
            └─ 路径 B2（条件/稳定轮询）：循环直至任一分支命中、有效帧达 Config.Wait.MaxSamples(80) 或到期限
                 ├─ 若 e.workRevoked(work) || work.Ctx.Err() != nil → 立即退出返回 control_revoked
                 ├─ remaining := time.Until(waitDeadline)；若 remaining <= 0 → 冻结 trigger="timeout"，出 B
                 ├─ 单帧采集子 ctx（取 Config.Wait.FrameTimeout 默认 16s，留 1s 余量避免与 captureTimeout 15s 边界赛跑）：
                 │    frameCtx, cancel := context.WithTimeout(work.Ctx, min(remaining, cfg.Wait.FrameTimeout))
                 ├─ readPollCapture(frameCtx, work, session)（复用共享 captureValidated）
                 │   ├─ 若返回 error，按 4 路精确分流（先 work.Ctx，再 waitDeadline，再 frameCtx，最后普通错误）：
                 │   │    1) 若 work.Ctx.Err() != nil → 立即退出返回 409 control_revoked
                 │   │    2) 否则若 time.Until(waitDeadline) <= 0 → 不计入 failStreak，冻结 trigger="timeout"，出 B（绝不因到期发起无意义 Probe）
                 │   │    3) 否则若 frameCtx.Err() != nil（单帧 FrameTimeout 上限截断但 waitDeadline 仍充裕）→
                 │   │         视为单帧瞬态超时：consecutive = 0; failStreak++（不冻结 timeout、不触发 Probe，等待后继续下一轮）
                 │   │    4) 否则（设备/树瞬态 failure，且 work/waitDeadline 仍有效）：
                 │   │         consecutive = 0；failStreak++（不计入 samples 与 Config.Wait.MaxSamples）
                 │   │         若 failStreak >= Config.Wait.ProbeOnFailStreak(3)：
                 │   │           remProbe := min(5s, time.Until(waitDeadline))
                 │   │           若 remProbe <= 0 → 冻结 trigger="timeout"，出 B
                 │   │           用 context.WithTimeout(work.Ctx, remProbe) 调 ProbeDisplay：
                 │   │             - 若 probeErr == nil && probe.Exists → 显式恢复重置 failStreak = 0，继续下一轮
                 │   │             - 若 work.Ctx.Err() != nil → 409 control_revoked
                 │   │             - 若 time.Until(waitDeadline) <= 0 → 冻结 trigger="timeout"，出 B
                 │   │             - 否则（!probe.Exists 或非上下文取消的失败）→ 立即 503 display_unavailable
                 │   │    睡 min(cfg.Wait.MinDwell, time.Until(waitDeadline)) 后下一轮（若被 work.Ctx 中断 → control_revoked）
                 │   └─ 有效可读帧：failStreak = 0; samples++
                 │        Preview + sampleDigestOf + hasReadableContent → 更新 consecutive 与 lastSample
                 │        若满足 4.3 退出判据 → 冻结 trigger（若仅因 samples>=Config.Wait.MaxSamples 则 trigger="timeout"）与 candidate（仅在命中条件或稳态时非空），出 B
                 └─ gap := cfg.Wait.MinDwell(600ms) - time.Since(lastSample.capture.CapturedAt)（对齐 settle.go:200）
                      若 gap <= 0 → 若 time.Until(waitDeadline) <= 0 则冻结 trigger="timeout" 出 B，否则立即进入下一帧
                      否则若 gap >= time.Until(waitDeadline) → 可中断等待至 waitDeadline（被 work.Ctx 中断则返回 control_revoked，否则冻结 trigger="timeout" 出 B）
                      否则可中断等待 gap（被 work.Ctx 中断则返回 control_revoked）进入下一帧
            ▼ T1（trigger 与阶段 B run 统计已冻结）
          [阶段 C: 验证采集与发布期 (独立预留 <= 35s，不计入 timeout)]
            ├─ 设 op.Phase = "capture"，work.Phase = "wait_for.capture"
            ├─ 总是先 ProbeDisplay(<=5s) 确认目标逻辑屏存活（严守 architecture.md 第 2 步）
            ├─ [若 image=true 先 readImageForAcquisition(10s+5s)]（截屏期间屏幕身份变化则 503 display_unavailable）
            ├─ 若 candidate != nil（即 trigger 为 "query" / "absent_query" / "stable"，即使 image=true 也不重采树）:
            │    复用 candidate 帧 → projectCapture → encodeImage & staleImage(ImageMaxSpan 3s，超跨度丢图降级 partial)
            │    → 沿用 candidate 命中评估 WaitResult.Matched 与阶段 B quality → publishTreeAttempt（若候选因慢图跨过 CaptureMaxAge 则标准 409 stale_screen）
            └─ 否则（candidate == nil：纯 sleep、timeout 到期或 MaxSamples 守卫退出）：
                 captureTree(15s) → encodeImage & staleImage
                 → 基于该最终发布帧计算 WaitResult.Matched 与按 4.2.4 得出对齐 quality
                 → publishTreeAttempt（条件化提交 + 再刷 120s）
            ▼ T2（释放 work，计算 elapsed_ms = T2 - T0，op.Phase = "finished"；HTTP defer 写完后释放 delivery 槽）
```

### 5.2 关键边界与错误恢复逐一行为

1. **撤销 / STOP / `end` / MCP 取消 / HTTP 断连**：
   - `work.Ctx` 直接衍生自 HTTP 请求 `parentCtx` 并叠加 `timeout + 40s`（`41s..80s`）。
   - `STOP`、`end`、客户端断连或 MCP 取消时，`work.Ctx` 立刻关闭，唤醒 `pauseInterruptible` 与进行中的 `frameCtx`/`probeCtx`，`WaitFor` 正常退出并返回 `409 control_revoked`（`work_deadline` 超时则冻结并返回 `control_revoked`，不带 `Wait` 也不带 `Observation`），绝不使用仅限 `app.find/list` 的 `query_cancelled` 语义，阶段 C 也不会发布迟到 `O`。
   - MCP 侧 `wait_for` 设定 `readOnly: false`，即 `endsOnCancel() == true`，宿主取消后会发有界 `end(P)`（5s）销毁会话（见 §7.2 提示）。
2. **准入前过期 vs 等待途中并发 `keepalive`**：
   - 准入单锁内若 `!sessionBeforeDeadline(session, now)`，直接拒绝 `409 session_expired`，绝不续租。
   - 若外部并发调用 `keepalive(minutes=1)` 把租约调短到比 `timeout` 还短，到期时 `housekeeping` 撤销会话并取消 `work.Ctx`，`wait_for` 立即退出并返回 `409 control_revoked`。
3. **阶段 C 最终验证采集失败或部分成功**：
   - 阶段 C 树失败（`hierarchy_unavailable`、`display_unavailable`、`stale_screen`、`tree_too_large` 等）：按 [observe.go:71-90](../../../internal/engine/observe.go#L71-L90) 构建 `Observation{Status: "failed", Missing: ["tree"]}`（`image=true` 且图片已读时仍保留只读截图物料），由 `planRecovery` 给出 `recovery: {entry: "observe", reference: "P"}`；此时 **`Result.Wait` 保留冻结的 `Trigger` 与 `ElapsedMS`，省略 `Matched`**。
   - 树成功发布但 `image=true` 截图失败/降级：新 `O` 正常发布（`status: "partial", missing: ["image"]`），HTTP `200`，`mcp.isError = false`，附带完整 `Result.Wait`（含 `Trigger`、`Matched`、`ElapsedMS`）。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `release/versions.json` & `scripts/generate-version.ps1` | 将 `contract_version` 从 `11` 升至 `12`，`release_version` 从 `0.11.0` 升至 `0.12.0`，`android_version_code` 从 `13` 升至 `14`，并将 `source_commit` 更新为冻结基线（即实现提交的父提交；实现提交自身的 hash 在提交前不可知，符合 [generate-version.ps1:27-39](../../../scripts/generate-version.ps1#L27-L39)）；运行生成脚本同步 Go/Java/XML |
| `internal/protocol/requests.go` | 新增 `WaitForRequest` 与 `DecodeWaitFor`（校验 `reference`、`1 <= timeout <= 40`、`stable && timeout < 2` 拒绝、非空白且 `<= 256` 码点的 `query`/`absent_query`、当 `HasQuery && HasAbsentQuery` 时拒绝 `wire.Query == wire.AbsentQuery`、`stable`/`full`/`image` 非 `null`；非法一律 `422 invalid_request`） |
| `internal/protocol/responses.go` | 新增 `WaitResult`（`Trigger string`、`Matched []string` 非空才输出且绝不写 `null`、`ElapsedMS int64`）；在 `Result` 结构体及 `MarshalJSONTo` 中将 `Wait *WaitResult`（json `"wait"`）置于 `Shell` 之后、`Notification` 之前；`QualityStop*` 新增 `"condition"` 与 `"timeout"` |
| `internal/protocol/decode_response.go` | 同步 `Result` 严格响应反序列化支持 `wait` 字段与新 `QualityStop*` 值 |
| `internal/engine/engine.go` & `cmd/device-service/{config,main}.go` | 新增 `WorkWaitFor = "wait_for"`；在 `Config` 新增 `Wait WaitPolicy`（`DefaultConfig` 默认 `MinDwell: 600ms`、`StableFrames: 3`、`MaxSamples: 80`、`ProbeOnFailStreak: 3`、`FrameTimeout: 16s`）与 `WaitPolicy.Validate()`（强校验 `MinDwell >= 0`、`StableFrames >= 2`、`MaxSamples >= StableFrames`、`ProbeOnFailStreak >= 1`、`FrameTimeout > 0`），并在 `cmd/device-service/main.go` 启动校验（对齐 `Settle.Validate()`）；同时在 `cmd/device-service/config.go` 的 `loadConfig` 校验 `ADB.TimeoutSeconds <= 15`，保证全链路 `< 80s < 90s` 预算成立；在 `operationState`（[engine.go:435](../../../internal/engine/engine.go#L435)）增加 `Wait *protocol.WaitResult`，在 `operationViewLocked`（[engine.go:1090](../../../internal/engine/engine.go#L1090)）深拷贝回填 `Wait` |
| `internal/engine/observe.go` | 拆分导出内部辅助 `resolveObserveTargetLocked(agent, reference string)` 供单锁原子准入复用，保持既有 `Observe` 对 `resolveObserveTarget` 的调用语义不变 |
| `internal/engine/operations.go` | **保留既有 `acquireWork` 的 9 处调用点不变**；新增 `acquireWaitWork(op *operationState, agent, reference string, parent context.Context, timeout time.Duration)`，在单锁内完成 `resolveObserveTargetLocked`、过期校验、barrier/work 检查、继承 `parent` 且期限 `timeout + 40s`（`41s..80s`）的 `WorkWaitFor` 分配、基线确认及 `SessionTTL` 续租；`setWorkPhase` 当 `work.Phase == phase` 时跳过重复 `broadcastAppLocked` |
| `internal/engine/retirement.go` | 确认 `applyFrozenResult`（[retirement.go:531-545](../../../internal/engine/retirement.go#L531-L545)）在 `work_deadline` 冻结时置空 `Observation`（此时 `op.Wait` 尚未落盘，冻结结果自然不带 `Wait` 也不泄露 `Observation`） |
| `internal/engine/wait_for.go`（新文件） | 实现 `Engine.WaitFor`（`registerOperation` 后即调 `setOperationReference(op, referenceKind(req.Reference))`）、准入折叠判重（仅当 `HasQuery && HasAbsentQuery` 时比较 `foldText(Query)==foldText(AbsentQuery)`，失败走 `finishObserveError` 且不发布 `outcome`）、`pollWaitConditions`（含预算前置、单帧 `min(remaining, cfg.Wait.FrameTimeout)` 子 `ctx`、`work.Ctx` -> `waitDeadline` -> `frameCtx` -> 普通错误的 4 路分流、`consecutive == StableFrames`、`hasReadableContent`、连续 `ProbeOnFailStreak` 次失败受限 `ProbeDisplay` 及恢复重置 `failStreak = 0`）、阶段 C 均走 `ProbeDisplay` + `candidate != nil` 固定复用（即使 `image=true` 亦不重采树）与按 4.2.4 对齐发布帧的 `quality` 构造 |
| `internal/engine/capture.go` | 抽出统一校验源 `captureValidated(cap, session)` 供 `readCapture` 与 `readPollCapture` 共用，消除 `CaptureMaxAge` / 控制界面检查复制；支持阶段 C 在 `ProbeDisplay` 之后复用有效候选帧（含结合 `readImageForAcquisition` + `staleImage` 降级）或补采并与 `qualityOfWait` 对齐提交 |
| `internal/engine/recovery.go` | 验证 `wait_for` 作为不认领 `O` 的读+等待工具走通用 `captureFailureCodes` / `observationDeadCodes` / `statusCodes` / `isSessionEndCode` 分支 |
| `internal/eventlog/log.go` | `allowedKeys` 白名单新增枚举键 `"trigger"`（复用 `"ms"`、`"samples"`、`"stable"`）；严禁记录 `query`/`absent_query` 原文 |
| `internal/httpapi/server.go` | 注册 `/api/wait_for` 路由（`s.agent(s.waitFor)`，请求体上限 `16 KiB`）；进入引擎前对齐 `/api/observe`（[server.go:216-232](../../../internal/httpapi/server.go#L216-L232)）申请 `delivery.acquire(deliveryReserveTree)` 或 `delivery.acquire(deliveryReserveImage)`，响应写完 `defer release()`；保持全局 `WriteTimeout: 90s` |
| `internal/client/client.go` | 新增 `Client.WaitFor(ctx, req, opts...)` 调用 `POST /api/wait_for` 并生成 `P`；保持 `defaultTimeout: 90s` |
| `internal/mcp/schema.go` & `catalog.go` | 更新 `treeTextGuide` 注释（[schema.go:16-18](../../../internal/mcp/schema.go#L16-L18) “five”→“six”）；注册第 12 个工具 `wait_for`（`readOnly: false`，附带精炼的 §4.3 3 句使用契约及 `treeTextGuide`）；`ToolCount()` 自动随 `len(toolCatalog)` 变为 `12` |
| `internal/mcp/server.go` | `prepareCall` 分发 `wait_for`；`isError` 对 `wait_for` 对齐 `observe` 口径 |
| `internal/mcp/result_text.go` | `resultFields` 白名单补充 `wait` 对象与 `wait.trigger`（token）、`wait.elapsed_ms`（literal）、`wait.matched`（json）；`encodeResultDiagnostic`（[result_text.go:343-384](../../../internal/mcp/result_text.go#L343-L384)）在保留阶段 C 失败时的 `wait` 上下文时显式透传 `wait.trigger` 与 `wait.elapsed_ms`、跳过 `wait.matched`（且阶段 C 失败已省略 `matched`） |
| `android-control/app/src/main/java/com/mobileuse/vnext/ControlState.java` | `workText` 补充 `"wait_for"` 分支（`"正在等待界面"`）；`phaseLabel` 扩展前缀匹配或显式映射 `wait_for.poll`、`wait_for.capture`、`wait_for.commit` 及既有 `captureAndPublish` 的 `.tree`/`.project`/`.image` 阶段（控制端与服务端阶段前缀约定统一） |
| `scripts/toolcatalog/main.go`、`scripts/mcp-probe/`（含 `checks.go:38-41` 的冻结 `toolNames`）、`scripts/verify-m5.ps1`、`internal/mcp/catalog_test.go`、`internal/mcp/server_test.go`、`internal/mcp/result_parity_test.go`、`scripts/mcp-probe/catalog_test.go` | 重新生成 12 工具指纹，同步更新 [verify-m5.ps1:217-218](../../../scripts/verify-m5.ps1#L217-L218)、[catalog_test.go:9-10](../../../internal/mcp/catalog_test.go#L9-L10)、[server_test.go:332-335](../../../internal/mcp/server_test.go#L332-L335)（工具列表）与 [server_test.go:358-370](../../../internal/mcp/server_test.go#L358-L370)（补充 `"wait_for": {"reference", "timeout"}` 必填映射）、[result_parity_test.go:343-346](../../../internal/mcp/result_parity_test.go#L343-L346)、[mcp-probe/catalog_test.go:34](../../../scripts/mcp-probe/catalog_test.go#L34) 与 [mcp-probe/catalog_test.go:202](../../../scripts/mcp-probe/catalog_test.go#L202) 中的 `11` 工具清单/基线哈希断言，并在 `mcp-probe` 的 smoke 套件新增 `wait_for` 探测检查。**`scripts/verify-m6.ps1` 本次不执行、也不在本轮同步清单内**（见 §9.1）：其 `510` 的 11 工具断言与 `579-582` 的「上一契约 = 10」断言未随契约 12 更新，下次启用前必须先修正。 |

### 6.2 配置与取值（含全链路预算链证明）

1. **端到端时间预算链（以最坏情况可证不撞 `90s` 为铁律）**：
   - **硬前提（启动强制校验）**：[config.go:71-73](../../../cmd/device-service/config.go#L71-L73) 默认 `ADB.TimeoutSeconds = 8`，并由 `loadConfig` 启动强制限定 **`ADB.TimeoutSeconds <= 15`**，从而在运行时绝对锁定 `captureTimeout() = max(15s, cfg) = 15s`（[device.go:921-926](../../../internal/android/device.go#L921-L926)），确保阶段 B + 阶段 C 绝不因外部超大配置反超 `work.Deadline` 与 `90s` HTTP 天花板。标准 `deploy.ps1` 固定写 `timeout_seconds = 8`（[deploy.ps1:333](../../../scripts/deploy.ps1#L333)），常规部署恒为 15s；注意该约束对**所有工具**生效——手改 `config.json` 到 `> 15` 的既有部署升级后会启动失败，须在 `docs/runbook.md` 记录（见 §6.5.7）。

| 环节 | 约束/最坏耗时 | 依据与位置 |
| --- | --- | --- |
| 用户参数 `timeout`（单位：秒，必填） | **`1..40` 秒**（`stable=true` 时 `2..40`） | 保留秒制以便匹配“多秒异步等待”（区别于毫秒级手势/观察短延迟 `wait_ms`/`duration_ms`） |
| 阶段 B 最坏越界 | **受 `waitDeadline` 截断（含 `Probe`），至多溢出 `< 100ms` 内存判定开销** | 发起 dump 前查 `remaining > 0`，单帧与失败早退 `Probe` 分别挂 `min(remaining, FrameTimeout)` 与 `min(remaining, 5s)` 子 `ctx` |
| 阶段 C 最坏总耗时 | **`<= 35s`**（复用候选带图 `<= 20s`；无图 `<= 20s`） | 补采：`ProbeDisplay(5s)` + `CaptureImage(10s)` + `verifyImageTarget(5s)` + `captureTree(15s)`；复用：省去 `captureTree(15s)` |
| 阶段 B + 阶段 C 合计最坏 | **`<= 40 + 35 = 75s`**（`+ < 1s` 编码/差异/投影 CPU） | 在任何合法配置设备慢响应下总计 `~76s` |
| 工作槽期限 `work.Deadline` | **`timeout + 40s`（`41s..80s`）** | `timeout + WorkTimeout(20s) + 20s`：严格包裹阶段 B+C (`<= ~76s`) 并留 ~4s 提交余量，且严格 `< 90s` |
| 传输与会话硬边界 | `WriteTimeout = 90s`、[server.go:72](../../../internal/httpapi/server.go#L72)<br>`client.defaultTimeout = 90s`、[client.go:26](../../../internal/client/client.go#L26)<br>`SessionTTL = 120s`、[engine.go:123](../../../internal/engine/engine.go#L123) | **`90s` HTTP/Client 与 `120s` SessionTTL 均严格大于 `work.Deadline(<=80s) > 阶段B+C(<=~76s)`**，留有 `>= 10s–14s` 安全缓冲区，不碰全局超时配置 |

2. **阶段 B 收敛默认策略（`Config.Wait WaitPolicy`，支持测试按需缩尺，不开放给调用方）**：
   - `MinDwell = 600ms`、`StableFrames = 3`、`MaxSamples = 80`（仅计有效可读帧）、`ProbeOnFailStreak = 3`、`FrameTimeout = 16s`。
   - `WaitPolicy.Validate()`：`MinDwell >= 0`、`StableFrames >= 2`、`MaxSamples >= StableFrames`、`ProbeOnFailStreak >= 1`、`FrameTimeout > 0`（从 `cmd/device-service/main.go` 启动即校验）。
3. **版本变更合理性（依据 [docs/release.md:46-48](../../../docs/release.md#L46-L48)）**：
   - 本次同时新增第 12 个冻结工具、扩展核心枚举 `observation.quality.stop_reason`（增加 `"condition"` / `"timeout"`）、扩展控制端 `work.kind` 与 `phase` 映射并需重编 Android APK，完全符合 `contract_version` 升版判据。

### 6.3 日志与可观测性

1. **允许记录的白名单字段**：复用 `"operation_id"`、`"phase"`、`"samples"`、`"stable"`、`"ms"`、`"code"`、`"observation_id"`，新增枚举键 `"trigger"`（仅限 `"query"` / `"absent_query"` / `"stable"` / `"timeout"`）。
2. **严禁记录字段**：`query`、`absent_query` 原始文本、树节点内容、匹配结果节点文本。
3. **阶段区分与 `status(P)`**：`op.Phase`（`Result.phase` / `status(P)`）为 `"wait"` -> `"capture"` -> `"finished"`；`work.Phase`（控制端展示）阶段 B 入口设置一次 `"wait_for.poll"`，阶段 C 为 `"wait_for.capture"` / `"wait_for.commit"`（及内部 `.tree`/`.project`/`.image`）。完成态 `Wait` 保存在 `operationState.Wait` 供 `status(P)` 与重复 `P` 重放输出（按架构规则省略 `observation`）。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| **唯一权威**（`internal/engine`） | 轮询、3 帧判稳、租约续期、条件化提交全收口在 `internal/engine`，不在 MCP 适配器或 HTTP 层做业务轮询 |
| **STOP 不等待** | `acquireWaitWork` 继承请求 `parentCtx` 并响应 `work.Ctx.Done()`；`STOP`/`end`/客户端断连立即唤醒退出且绝不发布迟到候选 |
| **O 单向消费** | `wait_for` 不认领旧 `O`；只在发布新 `O` 时退役旧 `ready` `O`，保持最多一个 `ready` `O` |
| **提交条件化** | 阶段 B 仅用只读 `Preview`，只有阶段 C 在 `publishTreeAttempt` 内重验会话、工作权、显示代次与 `CaptureMaxAge` 后原子提交；`readCapture`/`readPollCapture` 共享 `captureValidated` 单一校验源 |
| **锁短小** | `acquireWaitWork` 在单一 `e.mu.Lock()` 完成全部准入校验+过期拦截+续租，设备 dump 与树算法全部在锁外执行 |
| **严格接入** | `DecodeWaitFor` 拒绝未知键、重复键、大小写变体、尾随 JSON、`null`、空白/超长查询、矛盾 `query==absent_query`、`stable && timeout<2` 与越界/缺失 `timeout`，统一返回 `422 invalid_request` |
| **日志白名单** | `query`/`absent_query` 文本永不进 `eventlog` 与错误文本 |
| **租约不复活** | 单锁原子检验 `!sessionBeforeDeadline` 后才可续足 `SessionTTL=120s`，已过期会话必拒 `409 session_expired` |

### 6.5 契约与文档影响（全量清单）

同步修订全部 9 份正式文档并在提交前通过独立只读文档审计 subagent（`AGENTS.md` #15）：
1. `docs/interface.md`：§1（请求体上限 `16 KiB` 与采样对比）、§2（`Result` 固定字段顺序新增 `wait` 与 `409 session_expired`）、新增 `POST /api/wait_for` 章节、§11（`observation.quality.stop_reason` 新增 `"condition"`/`"timeout"`）、§13（端点总表新增 `/api/wait_for`），更新 `contract_version=12`。
2. `docs/mcp.md`：工具数 `11 -> 12`，`wait_for` schema/描述、`isError` 与 `observe` 同口径、取消发 `end(P)` 规则、宿主超时公式建议、`Wait` 文本/诊断投影规则。
3. `docs/architecture.md`：`WorkWaitFor` 工作类别与继承请求 `ctx`、3 帧连续稳定判据（及质量与最终发布帧对齐规则）、单锁准入防复活与续租规则、`toolcatalog` 12 工具、`Wait` 在 `Result` 中的位置与 `status(P)` 规则。
4. `docs/client.md`：新增 `Client.WaitFor` 接口与 `90s` 预算契约。
5. `docs/control-protocol.md`：`contract_version=12`、`work.kind` 枚举增加 `wait_for`、`phase` 短码/前缀规则与中文显示文案表同步 `ControlState.java`。
6. `docs/release.md`：更新版本源现状（`0.12.0`、`12`、`14`）与 12 工具清单口径。
7. `docs/runbook.md`：更新工具/端点验证表、`config.json` 的 `ADB.TimeoutSeconds <= 15` 新约束（对全部工具生效）与升级提示（`contract_version=12` 需全套件重启/重装）。
8. `docs/integration.md`：补充工作流集成示例、`timeout <= 40s`（宿主配置参考公式：`timeout <= host_timeout - 35s`（带图）/ `timeout <= host_timeout - 20s`（无图））与取消语义提示。
9. `docs/phone-operations.md`：同步 12 工具列表与手机侧 MCP/控制 App 联合版本校验说明。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **消除外层 LLM 多轮 `observe` 盲轮询**：将原本需要 3–10 轮“猜延迟 `observe` -> 全树进上下文 -> LLM 判断未就绪 -> 再调”的过程收敛成 1 次设备侧条件等待，命中即刻提前返回并直接交付最新有效 `O`。
2. **原生覆盖“条件出现”与“阻塞条件消失”**：支持等待目标按钮/文案出现（`query`），或等待全屏遮罩/加载文案退出（`absent_query`），过滤掉空白合成根切换瞬态，也可叠加 `S-B` 连续 3 帧稳定后再交付，并与发布帧的 `observation.quality` 一致表达。
3. **合法扩展等待窗口并消除等待中途租约误杀**：把有效等待从 `observe.wait_ms` 的 10s 提升到 40s（纯 sleep 不空耗 dump），且进入等待时单锁续足 `SessionTTL=120s`，避免因前置思考消耗租约而被 `lease_expired` 清理。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| **动态倒计时/实时进度条页面叠加 `stable=true`** | `S-B` 下 `sampleDigest` 覆盖整棵公开树；倒计时或进度百分比每帧变化会导致无法连稳 3 帧，调用将一直跑满 `timeout` 才返回 |
| **静态加载骨架在画面上静止超过 `1.2s` 且未配合 `query`/`absent_query`** | 无障碍树连续 3 帧（跨越 `~1.2s`）完全一致即判定稳定；静态不变的空白/骨架占位图仍会被误认稳定 |
| **纯视觉/Canvas/视频内部变化，或视口外/被隐私抑制的内容** | `query` 与 `sampleDigest` 仅作用于 `reported_visible_with_context` 公开树及前台/屏幕几何，视口外或无障碍不可见变化永远不会命中 |
| **`candidate != nil` 搭配 `image=true` 时可能触发 `image_stale`** | 为保证命中帧条件与 3 帧连续稳定不被重采破坏，阶段 C 固定复用 `candidate`（先截屏后复用）；若截屏耗时导致与命中树时刻跨度超过 `ImageMaxSpan (3s)`，图片会按既有 `staleImage` 降级为 `partial`（`missing=["image"]`） |
| **`candidate != nil` 且慢 `Probe`/慢图把候选拖过 `CaptureMaxAge`** | 阶段 C 固定复用命中帧、不重采；若 `ProbeDisplay(≤5s)` + 截图(≤15s) 后候选已超过 `CaptureMaxAge (15s)`，`publishTreeAttempt` 按既有门禁返回 `409 stale_screen`，本次不发布 `O`；调用方可改用较短 `timeout` 或 `image=false` 重试 |
| **超过 `40` 秒的超长后台任务（如下载安装包、分钟级转码）** | 受限于 `WriteTimeout (90s)` 与阶段 C 最坏 `35s` 采集缓冲，`wait_for` 单次最长等待 40 秒 |
| **MCP 宿主侧工具超时（如 60s）强制取消 `wait_for`** | `wait_for` 设定 `endsOnCancel() == true`，宿主取消后会发有界 `end(P)` **直接销毁整条会话**（通用安全上限：`timeout <= host_timeout - 35s`（带图）/ `timeout <= host_timeout - 20s`（无图）；例如 60s 默认超时宿主带图调用建议 `timeout <= 25s` 或调高宿主工具超时至 `>= 90s`） |
| **路线 Y 下已知慢启动动作的前序“冗余观察”（见 §7.3）** | 本提案不改造 `act` 与 `app.open`，动作自身仍先执行 2–3s `settle` 并向 Agent 返回中间 `O2` |

### 7.3 成本与未来演进对比（路线 Y 现状 vs 未来配套路线 X）

1. **独占工作位与投递槽成本（由设备并发路径与同端其它工具承担）**：
   - `wait_for` 执行期间（最坏可达 `~76s`）**全程独占唯一设备工作位**，期间同一会话的任何 `observe`、`act`、`start`、`app`（含 `app.find/list`）与 `shell` 请求都会立即返回 `409 device_busy`；此时唯一能抢占或中断它的外部渠道只有 `end(P)`（销毁会话）或手机控制端 `STOP`。
   - 为维持 `httpapi` “先预留投递容量、再接触设备工作”的现有契约，`/api/wait_for` 在入口预留 `256 KiB`（或带图 `1 MiB`）与 **1 个并发投递槽**（`deliveryMaxActive = 2`）并持有到 HTTP 写响应完成；当 1 路长等待正在占位、同时另 1 路慢客户端响应（`<= 15s`）还在写入尚未归还第 2 个槽时，第 3 路（包括只读观察）会立刻收到 `503 delivery_unavailable`。
2. **设备侧轮询开销**：
   - 纯 sleep 路径零轮询 dump，仅阶段 C 采 1 帧；
   - 条件轮询按 `600ms` 最小间隔及 `Config.Wait.MaxSamples(80)` 护栏，在 `40s` 最坏不命中共执行 **至多约 67 次**（`40s / 0.6s`）有效树采集（慢设备上按实测耗时更少，如每次 1s 则约 40 次）；所有中间帧仅在内存中供 `Preview` 与摘要计算，不写磁盘也不进入 LLM 上下文。
3. **版本升版耦合成本**：
   - `contract_version 11 -> 12` 导致旧版本 MCP 适配器、HTTP 客户端与控制端 App 会立即因 `409 contract_mismatch` 拒绝接入，必须成套升级服务端、客户端、控制 App APK 并重启 MCP 宿主进程。
4. **路线 Y 前序动作冗余代价 vs 未来配套路线 X 效果**：
   - **路线 Y 现状**：当动作触发后才知道需要等（例如看见 `O2` 显示 `"正在登录"`），`O2` 正是决定 `wait_for` 条件与触发增量 `delta` 的依据；但当调用方**事先已知必慢**（如重型 App 冷启动 `app.open`），仍须承担前序动作 `2–3s settle` 与一棵开屏中间树 `O2` 的耗时及上下文费用，随后再调 `wait_for` 取得最终树 `O3`。
   - **未来配套路线 X 预期效果**：后续若单独立项为 `act` / `app.open` 新增 `observe=false` 开关，写动作在确认 `executed` 后将直接跳过后置 `settle` 与树采集、仅返 `P`；调用方随即调用 `wait_for(reference=P, ...)`（因本提案的 `wait_for` 已原生支持 `P` 引用并自动 `forceFull=true` 交付全量树，**届时 `wait_for` 本身零返工**），从而彻底消掉多余的中间树，代偿成本则是需在该后续提案中补全无 `O` 时的动作恢复矩阵。

---

## 8. 验证计划

### 8.1 单元测试与门禁验证

| 用例 | 期望 |
| --- | --- |
| `DecodeWaitFor` / 配置校验 & 纯 sleep | 纯 sleep（`query`/`absent_query` 均省略且 `stable=false`）正常通过解码与执行返回 `trigger="timeout"`, `matched=["timeout"]`；缺失 `timeout`、`timeout=0/41/null`、`stable=true && timeout=1`、未知键、重复键、大小写变体、尾随 JSON、全空白/超 256 码点、`query==absent_query` 及 `foldText` 折叠相等均返回 `422 invalid_request`；`WaitPolicy.Validate()`（如 `MaxSamples < StableFrames` / `StableFrames < 2` / `FrameTimeout <= 0`）及 `loadConfig` `ADB.TimeoutSeconds > 15` 启动均拒绝 |
| `query` / `absent_query` / `timeout` OR 与 `S-B` 门禁 | 提前命中时不等满 `timeout` 立刻进入阶段 C；`query+stable=true` 下仅满足 `query` 但树未连稳 3 帧不得放行，连稳后以 `trigger="query"` 返回 |
| **反向对照：3 帧连续稳定严格性** | 构造 `frame1 != frame2 == frame3` 序列：若误用 `samples >= 3` 会提前放行，本实现必须不放行，直至出现连续第 3 帧一致才放行，且 `observation.quality` 为 `stable / 4 / stable` |
| 空白过渡帧防误判（`absent_query`） | 仅含合成根/无实质可读内容的过渡帧不触发 `absent_query`，直到出现有效内容帧或 `timeout` |
| `trigger` 冻结、候选固定复用与 `quality` 全分支对齐 | (1) `query` 或 `stable` 命中时，即使 `image=true` 阶段 C 亦固定复用 `candidate`（先截屏后复用），`\|candidate.CapturedAt - image.CapturedAt\| <= ImageMaxSpan(3s)` 时完整交付图+树且保留 `Matched` 与 `quality`；超 `3s` 则按 `staleImage` 丢图返回 `partial, missing=["image"]` 绝不重采破稳；(2) `"timeout"`/`"max_samples"` 退出才重采终帧，按 `run.samples > 0` 与阶段 B 全分支投影 `"not_sampled"` / `"stable"` / `"timeout"` / `"max_samples"`（含 `"timeout"` 兜底重采后刚好命中文案时 `Matched=["query","timeout"]`） |
| `Config.Wait.MaxSamples(80)` 守卫与预算公式断言 | 以缩尺 `Config.Wait`（如 `MinDwell=5ms, MaxSamples=8`）快速验证失败帧不计入上限、达 `MaxSamples` 按 `trigger="timeout"` 与 `stop_reason="max_samples"` 退出；另用纯推导单测断言默认策略满足 `ceil(40s / MinDwell) + margin <= MaxSamples`（`67 + margin <= 80`） |
| **反向对照：慢设备单帧 `FrameTimeout` & 预算截断 + 早退 `Probe`** | (1) 缩尺配置 `Config.Wait.FrameTimeout`（如 `50ms`）配合 `timeout=2s` 与 `200ms` 慢 dump 验证 `remaining > FrameTimeout` 时单帧截断按**瞬态失败**记 `failStreak++` 继续轮询而非提前误判全局 `timeout`；(2) 以缩尺 `MinDwell=20ms`、`timeout=2s` 在约 `1.95s` 处触发连续 3 次失败（等价覆盖真实 `timeout=40s` / `39.9s` 的 deadline 边界），单帧 `min(remaining, FrameTimeout)` 与早退 `Probe` `min(remaining, 5s)` 均被 `waitDeadline` 截断且截断不触发多余 `Probe`；`Probe` 返回 `Exists=true` 后 `failStreak` 清零；叠加预算算术测试确认 `40s + 35s + <1s < 80s < 90s` |
| **反向对照：单锁原子准入禁复活 & 续租防误杀** | (1) 已过期 1s 会话调用 `wait_for` 必拒 `409 session_expired` 不续租且因 `setOperationReference` 具备完整 `recovery.input_state`；(2) 距过期仅剩 2s 并发起并发 `housekeeping` 竞争测试下，合法 `wait_for(timeout=5)` 因在 `acquireWaitWork` 单锁内原子检查并完成续租可平稳完成并发布新 `O`；并发缩短 `keepalive` 到期则以 `control_revoked` 中止并含完整 `input_state` |
| 瞬态 `hierarchy_unavailable` 与连续 3 次失败早退 | 轮询夹杂 1–2 次读树失败清零 `consecutive` 并继续恢复；连续 `ProbeOnFailStreak` 次非截止类失败后调 `ProbeDisplay` 发现失联立刻早退 `503 display_unavailable` |
| HTTP 断连释放工作槽 & 投递槽冲突 | 客户端取消 HTTP `ctx` 立即释放 `WorkWaitFor`（正常退出返回 `control_revoked`，不走 `query_cancelled`），随后 `observe` 立即成功不报 `device_busy`；等待期其它工具必收 `409 device_busy`；1 路长等待 + 1 路慢写入响应占满 `deliveryMaxActive=2` 时第 3 路收 `503 delivery_unavailable` |
| MCP `isError` / `end(P)` / `result_text` / 诊断投影 | `wait_for` 超时带有效 `O` 时 `isError=false`；缺失 `O` 或失败时 `isError=true` 并走 `encodeResultDiagnostic`（透传 `wait.trigger`/`wait.elapsed_ms` 跳过 `matched`）；MCP 取消必发 `end(P)` 销毁会话；黄金样例校验文本与结构化输出一致性 |
| `status(P)` / 重复 `P` / `applyFrozenResult` / 控制端 & 门禁 | 正常完成后 `status(P)` 与重复 `P` 保留 `Wait` 且省略 `observation`；`work_deadline` 冻结时返回 `control_revoked` 且**不带 `Wait`、不带 `observation`**；`work.kind` 与各阶段 deadline 覆盖全部 `Work*`；审计日志含 `trigger` 不含查询原文；`ControlState` 映射 `"wait_for"` 与阶段前缀；`catalog_test` / `server_test`（含 `required` 映射）/ `result_parity_test` / `mcp-probe` / `verify-m5` 全部以 `12` 工具与 `contract_version=12` 通过（**`verify-m6` 本次不执行**，见 §9.1） |

### 8.2 手机端整组部署、主机双二进制替换与 `wait_for` 最小实机闭环

> **本次不执行 `scripts/verify-m6.ps1`**（用户排除：其 force-stop/重启演练与 notify/UI 全量回归耗时过长且具破坏性）。替代证据为：`verify-manifest.ps1`、`build-host-tools.ps1 -VerifyOnly`、`verify-m5.ps1 -SkipBuild`、手机侧 `mcp-probe -transport http-remote`、第 3 项的契约边界最小替代（`contract_mismatch`），以及第 4 项 3 个最小场景。排除理由与边界同时写入 §9.1 与验证记录。

1. **前置核验与产线构建（在已提交的干净 `HEAD` 上执行）**：
   - **前置条件**：ADB 真机在线（`adb devices` 为 `device`）；控制 App 已安装并以 `ControlService` 运行、已选模式且 `can_start=true`（`stop_ready`/通知/唤醒/解锁均就绪）；无未决 cleanup；`build/release/` 与 `build/` 未被手工改动。
   - **手机端整组构建与清单门禁**：运行 `./scripts/build-release.ps1` 生成整组 `build/release/`（`device-service`、`device-mcp`、`control.apk`、`u2.jar`、`applist.dex`、`service.sh` 与 `manifest.json`），随后运行 `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` 校验 6 产物哈希、`source_commit` 与 `HEAD` 一致（允许提交前缀）、`source_dirty=false` 与 12 工具指纹。
   - **主机侧 `build/` 双二进制重建替换**：运行 `./scripts/build-host-tools.ps1` 与 `./scripts/build-host-tools.ps1 -VerifyOnly`，仅原地构建并替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`（同步更新并校验 `build/host-tools-manifest.json` 与配套手机清单一致性），**不清空 `build/` 目录，且不涉入其他 Agent 宿主对这两个二进制的外部依赖治理与进程重启（由用户自行处理）**。若用户的宿主引用这两个二进制，必须由其重启宿主进程才能重新发现 12 工具；验证记录须写明是否重启。
2. **手机端整组实机部署**：
   - 运行 `./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json` 完成整组下发：停止旧版服务/适配器/App、覆盖安装 `control.apk`（**不卸载、不清库**保留 App 私有数据）、同步 chroot 内服务与手机侧 `device-mcp`、启动 `ControlService` 与端口转发（`18881->18880`、`18883->18882`）并核验 `/health` 的 release/contract 为 `0.12.0`/`12`。
3. **工具探针（不含 `verify-m6`）**：
   - **主机侧**：`./scripts/verify-m5.ps1 -SkipBuild`（内部先跑 `build-host-tools.ps1 -VerifyOnly`，再以 stdio / 主机 HTTP / P-recovery 三种 transport 跑 `mcp-probe`，并断言工具数为 `12`；该项要求先按 §6.1 把 `verify-m5.ps1:217-218` 的 `11` 改为 `12`）。
   - **手机侧**：`./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode text`；该配置由操作者按 [runbook.md:395-397](../../../docs/runbook.md#L395-L397) 准备（`transport=http`、`service.base_url=http://127.0.0.1:18881`、agent/control 令牌），**私有文件不入库、不打印**。断言 `tools/list` 为 12 个工具、`wait_for` schema 完整，并跑一次 `wait_for(timeout=1)` 探针（`is_error=false`、`trigger="timeout"`、`matched=["timeout"]`、返回新 `O`）。
   - **契约边界的最小替代（约 1 秒，不依赖 `verify-m6`）**：带 `X-Mobile-Use-Contract: 11` 调一次 `POST /api/status {}` 期望 `409 contract_mismatch`，带 `12` 期望正常；`/v1/control/sync` 携带 `contract_version=11` 期望 `409`、`12` 期望被接受。该项只覆盖 `verify-m6` 中与本次契约升版直接相关的部分，不覆盖其 notify/UI 与重启演练。
4. **`wait_for` 最小实机闭环（3 项，均需先建立会话并取得引用）**：
   - **统一前置**：`start` 取得会话与 `Ostart`；打开目标应用（`app.open(app=<包名>, observation_id=Ostart)`）取得成功后的 `Oopen`，或对 `Ostart` 执行一次 `observe` 取得 `Obase`。之后每次 `wait_for` 用上一步发布的 `O` 作为 `reference`，每次调用由适配器分配新的 `P`。
   1. **virtual 模式条件提前命中**：在"先显示 `加载中`（约 2–3s）再出现目标按钮（约 4–8s 完成）"的过渡界面上依次执行 `wait_for(reference=Obase, absent_query="加载中", timeout=15)`，再用其发布的 `O` 执行 `wait_for(reference=<上一步 O>, query=<目标文案>, stable=true, timeout=15)`。期望：两次均 **< 12 秒（不跑满 15s）** 返回 HTTP `200`、`is_error=false`；`trigger` 分别为 `absent_query` / `query`；发布的 `O` 树含目标节点且不含占位文案；第二次 `observation.quality.state="stable"` 且 `wait.matched` 含 `query`。
   2. **primary 模式 `stable=true` 表征**：在控制端切到 primary 模式（操作者操作），`end` 掉 virtual 会话后重新 `start`，执行 `wait_for(reference=<O>, stable=true, timeout=5)`。此项只做**表征记录**（SystemUI/状态栏时钟是否使整树无法连稳 3 帧），不设通过/失败门槛；如实记录 `trigger`、`elapsed_ms` 与 `quality.state`，完成后切回 virtual 模式并 `end`。
   3. **`image=true` 与 `STOP` 实时撤权**：在 virtual 会话中执行 `wait_for(reference=<O>, timeout=3, image=true)`，校验纯 sleep 返回 `trigger="timeout"`、`matched=["timeout"]`、`quality=unknown/0/not_sampled`，图片成功时为 `complete`、超 `ImageMaxSpan` 时为 `partial`；随后以新 `O` 另起 `wait_for(timeout=15)`，在等待中途按下控制端 `STOP`，校验 **<= 1 秒内返回 `409 control_revoked`**，并用 `status(P)` 与一次 `diagnose` 确认没有迟到的新 `O` 发布。
   - 若任一项暴露缺陷：回到 §10 第 2–5 步修复并重新提交，再重建清单（干净 `HEAD`）与部署，然后重跑本节。

### 8.3 记录

- 写入 `maintenance/verification/wait-for.md`，注明实现提交 hash、全量 Go 测试与构建命令（`GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0`）、`generate-version.ps1` / `toolcatalog` / `verify-manifest.ps1` / `build-host-tools.ps1 -VerifyOnly` 清单指纹、`deploy.ps1` 实机部署结果、`verify-m5.ps1 -SkipBuild` 与手机侧 `mcp-probe -transport http-remote` 的真实输出、以及 §8.2 三项最小闭环的逐项结果与执行边界；**开头声明：本次不执行 `verify-m6.ps1`（用户排除）及其理由、真机部分是否执行、用户宿主进程是否已重启**。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 整体路线 | **路线 Y**（新增正交第 12 工具 `wait_for`，暂不修改现有动作后置观察；未来可无损叠加路线 X `observe=false`） |
| `timeout` 与 `90s` 预算链 | `timeout` 必填整数 **`1..40` 秒**（`stable=true` 时 `2..40`）；启动强制 `ADB.TimeoutSeconds <= 15`；阶段 B 采样与早退 `ProbeDisplay` 均受 `waitDeadline` 截断（单帧 `min(remaining, Config.Wait.FrameTimeout(16s))` 仅作瞬态超时）；阶段 C 固定最坏 `35s`；`work.Deadline = timeout + 40s (<= 80s) < 90s`（不动全局 `90s` HTTP/Client 超时） |
| 条件关系与 `stable` 角色 | `query` / `absent_query` / `timeout` 为并行 OR；`stable=true` 采用 **`S-B` 门禁**（无查询作独立稳定分支，有查询作整树连续 3 帧稳定门禁，`Config.Wait` 默认 `MinDwell=600ms` 跨越 `>= 1.2s`、`MaxSamples=80` 仅计有效帧、`FrameTimeout=16s`，MCP 描述精炼为 3 句契约） |
| 文本匹配与可读证据 | 复用 `nodeMatchesQuery` + `foldText`（仅公开可见树，不改 `app.find`）；匹配前须过非空合成根可读证据门槛；拒绝空白串及 `foldText` 相等的矛盾查询（`422 invalid_request`） |
| `trigger`/`matched`/`quality`/`elapsed_ms` | `trigger` 在阶段 B 冻结；阶段 C 对 `candidate != nil` 固定复用（含 `image=true`，超 `ImageMaxSpan` 降级 `partial` 不破稳）；`matched` 仅含帧可判定项（`query`/`absent_query`/`timeout`）；`observation.quality` 按复用/`"timeout"` 重采对齐投影（扩展 `"condition"`/`"timeout"`）；`elapsed_ms` 锁定 **`T0 -> T2` 整次执行耗时** |
| 租约、并发与取消 | `L1`（`setOperationReference` + `acquireWaitWork` 继承请求 `ctx`，单锁原子解析+过期拦截 `409 session_expired`+续足 `SessionTTL=120s`，阶段 C 再刷新）；撤销返回 `409 control_revoked`；MCP 取消发 `end(P)`；`diagnostic v1` 透传 `wait.trigger`/`wait.elapsed_ms` |
| 版本与发布 | 推进 `contract_version: 11 -> 12`、`release_version: 0.11.0 -> 0.12.0`、`android_version_code: 13 -> 14`（同步 `source_commit` 为父提交），覆盖全部 9 份 `docs/` 与校验脚本 |
| 验证范围（`verify-m6` 归档） | **本次不执行 `scripts/verify-m6.ps1`**（用户排除：force-stop/重启演练与 notify/UI 全量回归耗时过长、具破坏性）；替代证据 = §8.2 的 `verify-manifest` + `build-host-tools -VerifyOnly` + `verify-m5 -SkipBuild` + 手机侧 `http-remote` 探针 + 契约边界最小替代（`contract_mismatch`）+ 3 项最小闭环。该脚本的 11 工具断言（`510`）与「上一契约 = 10」断言（`579-582`）**不随本次契约 12 更新**，也不在本轮同步清单内；下次启用前必须先修正，否则一跑即失败。 |

### 9.2 未决

- 无未决设计项。

---

## 10. 实施顺序

1. **版本与协议层（并行准备）**：更新 `release/versions.json`（`0.12.0` / `12` / `14` / `source_commit`）并运行 `scripts/generate-version.ps1`；在 `internal/protocol` 实现 `DecodeWaitFor`、`WaitResult`、`Result.Wait`（置于 `Shell` 与 `Notification` 之间）、`QualityStopCondition`/`QualityStopTimeout` 及严格编解码测试。
2. **引擎与日志层**：拆分 `resolveObserveTargetLocked` 与 `captureValidated` 单一校验源；实现 `Config.Wait WaitPolicy` + `WaitPolicy.Validate()`（含 `cmd/device-service/main.go` 与 `config.go` 的 `ADB.TimeoutSeconds <= 15` 启动校验）、`acquireWaitWork`（保留既有 9 处 `acquireWork` 不变，增加各 `work.Kind` 超时单测）、`WorkWaitFor`、`operationState.Wait` / `operationViewLocked`、`wait_for.go` 轮询/预算截断/4 路错流/连续 3 帧判稳/`ProbeDisplay` 早退/阶段 C 候选固定复用与 `"timeout"` 重采对齐 `quality` 投影、`setOperationReference`，以及 `eventlog` 白名单和全套引擎正反向单测。
3. **传输与客户端层**：在 `internal/httpapi` 注册 `/api/wait_for`（`16 KiB` 上限 + `delivery` 槽位预留）与并发测试；在 `internal/client` 新增 `Client.WaitFor` 与测试。
4. **MCP、控制端与工具脚本层**：注册第 12 个工具与 `treeTextGuide` 注释更新、`result_text` 白名单与诊断固定输出、更新 `ControlState.java` 中 `"wait_for"` 与带前缀阶段的受控中文文案并编译校验；同步 `scripts/toolcatalog`、`scripts/mcp-probe`、`scripts/verify-m5.ps1`、`catalog_test.go`、`server_test.go`、`result_parity_test.go`（`scripts/verify-m6.ps1` 本次不执行、不更新）。
5. **文档、审计门禁与实现提交**：更新 §6.5 列出的全部 9 份 `docs/` 文件；提交前派发独立只读文档审计 subagent 取得 `clean` 结论；运行全量 Go 单元测试与 `ControlState.java` / `linux/arm64` 构建检查，先提交实现（形成干净 `HEAD`，满足清单脚本门禁）。
6. **整组部署、主机替换与最小实机闭环（`verify-m6` 不执行）**：在干净 `HEAD` 上依次执行 `./scripts/build-release.ps1`、`./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`、主机侧原地重建替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`（`./scripts/build-host-tools.ps1` + `-VerifyOnly`，不管理外部 Agent 依赖），以及 `./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json` 完成手机端整组部署；随后运行 `verify-m5.ps1 -SkipBuild`、手机侧 `mcp-probe -transport http-remote` 与 §8.2 三项最小闭环。**本次不运行 `verify-m6.ps1`**（理由见 §9.1）；若闭环暴露缺陷，回到第 2–5 步修复并重新提交，再重建清单与部署后重跑本节。
7. **验证记录与提案归档**：将上述构建、部署与实机闭环真实结果写入 `maintenance/verification/wait-for.md`；将本提案移入 `implemented/` 并补写 §11 后提交验证记录与归档。

---

## 11. 实现结果

实现提交：`a077210f191836d5ad24f0862349c525dd16225a`（`2026-11-14`）。
实现与验证记录：[wait-for.md](../../verification/wait-for.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| §4.1 严格接入、文本匹配、最小可读证据 | `internal/protocol/requests.go`（`WaitForRequest`、`DecodeWaitFor`、`checkWaitQuery`、`Min/MaxWaitTimeoutSeconds`、`MinStableWaitTimeoutSeconds`）；`internal/protocol/encode.go`（`WaitForRequest.Encode`）；`internal/engine/wait_for.go`（准入折叠判重、`hasReadableContent`/`hasContentNode`、`treeMatchesFolded`/`nodeMatchesFolded` 复用 `nodeMatchesQuery`） |
| §4.2 3 帧连续稳定与 `quality` 对齐 | `internal/engine/wait_for.go`（`waitRun`、`pollWaitConditions` 的连续相等计数、`waitQualityOf`、`absorbWaitTerminalFrame`）；`internal/engine/settle.go`（复用 `sampleDigestOf`）；`internal/protocol/responses.go`（`QualityStopCondition`/`QualityStopTimeout`） |
| §4.3 组合退出分支与 `trigger`/`matched` 解耦 | `internal/engine/wait_for.go`（`conditionHit`、`waitConditionTrigger`、`waitMatched`）；`internal/protocol/responses.go`（`WaitResult`、`WaitTrigger*`） |
| §4.4 与既有重验/契约的关系 | `internal/engine/operations.go`（`acquireWaitWork` 单锁准入、继承请求 `ctx`、`timeout + waitWorkReserve`）；`internal/engine/observe.go`（`resolveObserveTargetLocked`）；`internal/engine/capture.go`（`captureValidated` 单一校验源） |
| §5.1 时间轴与每帧预算门禁 | `internal/engine/wait_for.go`（`WaitFor`、`sampleWaitFrame` 的单帧 `min(remaining, FrameTimeout)` 子 `ctx`、4 路错误分流、`probeOnFailTimeout` 早退探测、`waitCaptureReserve` 的 35s 验证采集截止）；`internal/engine/capture.go`（`readPollCapture`） |
| §5.2 撤销/STOP/断连与阶段 C 失败 | `internal/engine/wait_for.go`（`stoppedWaitError`、采集失败分支保留 `trigger`/`elapsed_ms` 并省略 `matched`） |
| §6.1 `Engine.WaitFor` 与文件改动 | `internal/engine/wait_for.go`；`internal/engine/engine.go`（`WorkWaitFor`、`WaitPolicy`、`DefaultWaitPolicy`、`Validate`、`operationState.Wait`、`operationViewLocked`）；`internal/engine/operations.go`（`setOperationWait`、`setWorkPhase` 去重）；`internal/eventlog/log.go`（`trigger`） |
| §6.1 `Config.Wait` 启动校验与 `ADB.TimeoutSeconds ≤ 15` | `cmd/device-service/main.go`（`engineConfig.Wait.Validate()`）；`cmd/device-service/config.go`（`maxADBTimeoutSeconds`） |
| §6.1 传输与客户端 | `internal/httpapi/server.go`（`/api/wait_for`、16 KiB、投递槽 256 KiB/1 MiB）；`internal/client/client.go`（`Client.WaitFor`） |
| §6.1 MCP、控制端与工具脚本 | `internal/mcp/schema.go`（第 12 工具与描述）、`internal/mcp/server.go`（`prepareCall`、`isError`）、`internal/mcp/result_text.go`（`wait` 投影与 diagnostic 透传）；`android-control/.../ControlState.java`（`wait_for` 文案、`phaseLabel`/`prefixedPhaseLabel`/`hasPhasePrefix`）；`scripts/mcp-probe`（12 工具、`wait_for` 探针、假适配器/假服务）；`scripts/verify-m5.ps1`（count=12） |
| §6.2 配置取值与预算链 | `internal/engine/engine.go`（`DefaultWaitPolicy`、`waitWorkReserve = 40s`）；`internal/engine/wait_for.go`（`waitCaptureReserve = 35s`） |
| §6.3 日志与可观测性 | `internal/eventlog/log.go`（`trigger` 白名单）；`internal/engine/wait_for.go`（`wait.done` 事件、`logQuality`）；`op.Phase` = `wait`/`capture`/`finished`，`work.Phase` = `wait_for.poll`/`.capture`/`.tree`/`.image`/`.project`/`.commit` |
| §6.4 与既有不变式的关系 | 同上；提交仍唯一经 `publishTreeAttempt` |
| §6.5 契约与 9 份 `docs/` | `release/versions.json`（0.12.0/12/14）与生成常量；`docs/interface.md`、`mcp.md`、`architecture.md`、`client.md`、`control-protocol.md`、`release.md`、`runbook.md`、`integration.md`、`phone-operations.md` |
| §8.1 单元测试与门禁用例 | `internal/engine/wait_for_test.go`（16 例：纯 sleep、query 复用、3 帧严格性反向对照、空白过渡帧、MaxSamples 守卫、单帧超时瞬态、失败探测早退、过期拒绝与续租、断连释放、device_busy、折叠矛盾、status 重放、P 引用强制 full、镜像复用）；`internal/protocol/protocol_test.go`（严格接入 22 例与编解码往返）、`decode_response_test.go`；`internal/httpapi/wait_for_test.go`（16 KiB、严格接入、投递槽、契约头、方法）；`internal/mcp/server_test.go`/`catalog_test.go`/`result_parity_test.go`/`result_manifest_test.go`；`scripts/mcp-probe/catalog_test.go`/`probe_test.go`/`result_modes_test.go` |
| §8.2 手机整组部署与主机双二进制 | `build-release.ps1` + `verify-manifest.ps1`（`source_dirty=false`、6 产物、12 工具指纹）、`build-host-tools.ps1` 与 `-VerifyOnly`（`build/device-mcp.exe`、`build/mcp-probe.exe`）、`deploy.ps1 -SkipBuild`（保留 App 私有数据）、`verify-m5.ps1 -SkipBuild`、手机侧 `mcp-probe -transport http-remote`、`wait_for` 最小实机闭环 |
| §8.3 记录 | `maintenance/verification/wait-for.md` |
| `AGENTS.md` 目录描述 | `internal/mcp/` 一行由 11 改为 12 工具 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| `captureValidated` 的签名 | 实现为 `(cap device.Capture, session *sessionState) (device.Capture, *protocol.APIError)`，返回补好 `Display`/`CapturedAt` 的采集而不是只返回错误：`readCapture` 与 `readPollCapture` 都需要这份归一化结果，返回错误码不足以表达。 |
| `readPollCapture` 的形态 | 实现为 `readCapture` 的兄弟函数、不带 phase 参数：轮询帧不能每帧广播新的工作相态，等待期相态在准入时一次设定。 |
| 阶段 C 复用候选帧的方式 | 阶段 B 只保存候选 `device.Capture` 与摘要（用 `Preview`，不推进 ref 连续号）；阶段 C 用同一份 capture 重新做一次 `Candidate` 投影（`projectCapture`）再提交。设备侧没有第二次读树，最终 `commit` 仍只在 `publishTreeAttempt` 内执行，等价于“复用候选帧”。 |
| `acquireWaitWork` 的返回值 | 多返回一个 `forceFull`：`P` 引用必须像 `observe(P)` 一样强制 full，而 §5.1 的伪代码没有体现这一点。 |
| 阶段 C 的 35s 上限 | §5.1 只把 35s 写进预算推导；实现加了 `waitCaptureReserve = 35s` 的显式截止，使 `timeout + 35s < 80s < 90s` 成为可执行不变量（单步上限之和恰好用满该预留）。 |
| §8.2 第 1 项的场景形态 | 提案设想“先显示加载中、再出现目标按钮”的过渡界面。本次实机用的是受控测试目标 `com.mobileuse.probe` 的真实页面：`query`+`stable=true` 提前命中（1.65s）、`absent_query` 首帧命中（0.47s）、纯 sleep+image、等待期 `end` 撤销。另外记录了一条边界事实：同一页面上位于视口外的文案不会进入公开树，因此 `query` 永不命中（提案 §4.1 已声明该边界）。 |
| §8.2 第 3 项的 STOP 来源 | 控制 App 的 `com.mobileuse.vnext.STOP` 服务动作不导出（`am startservice` 被权限拒绝），无法用 shell 模拟按键。本次改由 Agent 侧 `end(P)` 触发同一撤销路径（`revokeSessionLocked` → 取消 `work.Ctx`）：等待在 `end` 后约 50ms 返回 `409 control_revoked`，不带 `wait` 也不带 `observation`，`status(P)` 与 `diagnose` 均无迟到 `O`。 |
| §8.2 第 2 项 primary 模式表征 | 未执行：切换主屏模式需要操作者在 App 界面点击（`SELECT_PRIMARY` 同样不导出），本次不改动用户主屏。已用 virtual 模式的 `stable=true` 运行完成同一表征（`query`+`stable` 得 `stable/3/stable`，静态页面跑满 `timeout` 得 `stable/26/stable`）。 |
| `scripts/verify-m6.ps1` | 按提案 §9.1 未同步、未执行。`docs/runbook.md` 也不再把它当作常规发布门禁；它的 11 工具与旧契约断言仍待单独修正。 |
| 文档章节位置 | `docs/interface.md` 把 `wait_for` 放在文末 §14（不重排既有 §6–§13 编号），而不是紧跟 §5 `observe`。 |
| 额外做的 | `internal/mcp/schema.go` 的工具描述把“到时仍会发布新观察”收紧为“按成功返回并发布验证采集得到的那个观察”，并把 `wait.matched`/`elapsed_ms` 的语义写清；这改变了工具目录指纹（`04177607…72b1e9`）。另在 `AGENTS.md` 目录描述里把 `internal/mcp` 的工具数更新为 12。 |

---

## 附：本提案不改变的事项

- `act`、`start`、`app` 的后置观察生成、默认 `settle` 策略（`1.5s/2s/3s`）及 `O` 单向消费规则保持不变（未来路线 X 另行立项）。
- `observe` 现有参数（含 `wait_ms` `0..10000`）与语义保持不变，确保向后兼容；`app.find` / `inspect` 的既有文本折叠与匹配语义保持不变。
- `act` 实时节点与祖先链重验规则不变，不放宽任何安全或事实分层门禁。
- `httpapi.Server.WriteTimeout (90s)`、`client.defaultTimeout (90s)`、`SessionTTL (120s)`、`ObservationLifetime (60s)` 全局配置值保持不变。
- `eventlog` 不输出节点树、查询字符串、输入正文、命令与异常原始文本的禁令保持不变。
