# 提案：act 重验失败时交付一个新观察，并把 recovery 改为 retry

状态：已实现（实现提交 `6095a09922d4a3ddfcee97184718abd6ec224be9`，2026-11-11）。
实现与验证记录：[act-revalidation-observation-delivery.md](../../verification/act-revalidation-observation-delivery.md)。
基线提交：`1d5d71350677f526e32a04ed73c29078b512ed26`（分支 `master`，2026-11-11）。
适用范围：`POST /api/act` 与经 MCP `act` 投影的同一条路径，在**认领后、派发前**的重验阶段失败，且失败前已取得一帧可用于发布的树。
影响范围：`internal/engine`（`act.go`、`capture.go`、`recovery.go`、`engine.go`、`operations.go`）、`internal/eventlog` 白名单、`internal/engine` 测试、`docs/interface.md`、`docs/architecture.md`、`docs/client.md`、`docs/mcp.md`、`maintenance/decisions.md`、`release/versions.json` 与生成常量。
契约（`/api/*` 字段与 `contract_version`）：改变。重验阶段失败结果新增可选的 `observation`；`target_changed`、`foreground_changed` 与重验阶段 `node_not_found` 的 `recovery` 取值由 `observe/P/retired` 改为 `retry/O/effective`；`contract_version` 由 `5` 前进为 `6`。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：`internal/engine/act.go`、`internal/engine/capture.go`、`internal/engine/recovery.go`）。

---

## 0. 摘要

`act` 在认领观察之后、派发输入之前做实时重验；失败时今天只返回 `409` 与 `recovery=observe/reference=P/input_state=retired`，调用方必须自己再 `observe` 一次、再发一个新的 `act`。而这次失败的重验**已经采集过一帧树**（`internal/engine/act.go:295`），帧被丢弃。本提案把这帧发布成新的 ready 观察，随失败结果一起返回，并把 `recovery` 改为 `retry/O/effective`，让调用方用新 `O` 与新 `P` 一步重试。

明确不做：不自动再次派发，不新增请求字段，不复用旧 `P`，不放宽目标身份匹配，不在交付时做收敛采样。

## 1. 问题

### 1.1 现象与代价

`target_changed` 的恢复今天是三次调用：`act`（失败，已采集一次）→ `observe`（再采集一次）→ `act`（重验再采集一次），三个 `P`、三次采集。失败那次调用采集的帧被直接丢弃（`internal/engine/act.go:295-306`）。对连续、多应用的实机任务，每个这样的拒绝都要多一次往返和一次完整树读取。

### 1.2 证据链

- 认领发生在重验之前：`internal/engine/act.go:45-54`；重验失败返回：`internal/engine/act.go:56-62`。
- 重验自己采集一帧：`internal/engine/act.go:289-298`（`ProbeDisplay` + `dev.Capture`）；`act.go:299-306` 先比前台再证明目标。
- 失败点的错误码：`target_changed`（`act.go:314-318`、`act.go:328-356`）、`foreground_changed`/`foreground_unknown`（`act.go:376-398`）、`node_not_found`（`act.go:310-313`）。
- 认领单向、不归还：`act.go:13-14`、`act.go:242-272`；释放工作位置时退役被认领的 O：`internal/engine/operations.go:243-259`。
- 投影只按事实查表：`internal/engine/recovery.go:46-112`；认领后失败落在 `recovery.go:97-99`，即 `observe/P/retired`。
- 契约措辞：`recovery` 是下一个请求的投影（`docs/interface.md:67`）；`retry` 现定义要求“什么都没派发，观察也未被认领”（`docs/interface.md:81`）；`not_executed` 只承诺 `error` 与 `recovery`（`docs/interface.md:341`）；`target_changed` 明确写 `observe`（`docs/interface.md:333-334`）。
- 发布路径的唯一提交闸门：`internal/engine/capture.go:234-281`（重新核对会话、工作归属、显示代次与帧龄后才提交，只退役 ready 的 current）。
- 基线确认入口只有两处：`act` 认领 full O（`act.go:268-270`）与 `observe(O)`（`internal/engine/observe.go:44-49`），见 `docs/architecture.md:163-165`。
- full/delta 判据：`internal/engine/capture.go:183-198`；`observe(P)` 与 `start` 强制 full（`internal/engine/observe.go:129`、`internal/engine/start.go` 的 `captureAndPublish(..., true, ...)`）。
- 服务端从不读取请求里的 `recovery`：`internal/engine/recovery_entry_test.go:710-723`。

### 1.3 为什么直觉会出错

1. **“加个请求开关，默认关闭”**：与“服务只写 `recovery`、从不读请求里的 `recovery`”冲突（`internal/engine/recovery_entry_test.go:710-723`），也把服务端策略交给调用方（`maintenance/process.md` §10）。本提案是无条件的服务端策略。
2. **“把认领挪到重验之后，让 O 保持 ready 就能复用”**：违反硬约束 3“认领后不归还”，并且等于允许用陈旧目标重发。
3. **“失败后等服务端再重验一次（settle 后重证）”**：本提案针对的失败多数来自页面真的变了，等“同身份”重新出现不是可依赖的；那只是把往返换成服务端等待，并隐藏延迟。
4. **“放宽身份匹配以减少误拒”**：那是另一条取舍（目标证明的判据），会引入错点，不属于本提案。

## 2. 目标与非目标

目标：

- 认领后、派发前的重验失败，若已取得可用新帧，失败结果同时携带该帧发布出的新 ready `O`。
- `recovery` 在该分支为 `retry`/`O`/`effective`，调用方用新 `P` 一步重试。
- 不交付的分支（无帧、前台不明、撤销、超限、超龄）保持今天的 `observe`/`P`/`retired`，行为不变。

非目标：

- 不自动重发动作，不产生第二个 `P`。
- 不改变静态拒绝（认领前）的行为与 `retry` 语义。
- 不改变 `open_app` 的重验：它只做 `ProbeDisplay`，没有可交付的帧（`internal/engine/openapp.go:99-108`）。
- 不引入收敛采样，不新增请求字段，不改 `delta` 结构与 MCP 工具 schema。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持 `observe`/`P` | 每次拒绝多一次往返 + 一次重复采集 | 语义正确，但调用方要复制恢复逻辑 |
| R2 交付新 O（选中） | 发布失败帧为新 O，`retry`/`O`/`effective` | 失败路径多一次投影（纯 CPU）；契约变化与版本前进 | 交付的帧可能仍在动；下一次 `act` 的重验会再拒绝并再交付，形成自我修正 |
| R3 服务端重证 | 失败后 settle 一帧重新证明 | 失败路径最多 +2s | 页面确实变了时不成立；把延迟藏进失败路径 |
| R4 请求开关 | 新增请求字段选择策略 | 契约面扩大 | 违反“服务不读 recovery 请求”，两套行为契约 |
| R5 认领前移 | 重验通过后才认领 | 实现小 | 违反单向消费；允许用陈旧目标重试 |

选择 R2：它是唯一同时在“不隐藏派发”“不新增请求字段”“调用方少一次往返”三点上成立的路线。R3 可作为将来的独立取舍，若实机数据表明存在稳定的“回归同身份”窗口。

## 4. 判据

交付条件（全部成立才交付）：

1. 失败发生在 `act` 的 `revalidate` 阶段（`internal/engine/act.go:56-62`），且 `outcome=not_executed`；
2. 错误码属于 `target_changed`、`foreground_changed`、`node_not_found`；
3. 重验阶段已经取得一帧（`fresh != nil`）；
4. 该帧 `foreground.state` 属于 `present`、`none`。`unknown` 不交付：下一次 `act` 会再报 `foreground_unknown`，交付只会形成拒绝↔交付循环；
5. 目标虚拟屏上没有控制 App 的可见节点（与 `internal/engine/capture.go:347-354` 同一判据）；
6. 公开树构造与 `MaxFullTreeBytes` 体积校验通过（`internal/engine/capture.go:358-374`）；
7. 提交闸门全部成立：会话 `active`、工作归属未撤销、显示代次未变、帧龄小于 `CaptureMaxAge`（`internal/engine/capture.go:234-247`）。

不交付并保持现值：`display_unavailable`、`hierarchy_unavailable`、`foreground_unknown`、`control_revoked`、`tree_too_large`、帧龄超限、控制 UI 出现在目标虚拟屏。

full/delta：`forceFull = !obs.Full`，其中 `obs` 是本次失败请求引用的（已被认领的）观察。

- 引用 full O 时，认领已经把它确认为基线（`internal/engine/act.go:268-270`），因此 delta 的 `base` 恰好是调用方刚刚使用的那棵树；其余判据沿用 `internal/engine/capture.go:183-198`。
- 引用 delta O 时不确认基线，delta 的 `base` 会退回到更早的确认基线，要求调用方把补丁应用到更早的树；此时改发 full，保证交付的观察自描述。

交付的观察不确认基线、不改变 `e.baseline`、不改变 `inspect` 对历史树的可见性；`actionable_until` 从该帧的 `CapturedAt` 起算并按会话到期截断（`internal/engine/capture.go:250-260`）。

## 5. 算法

```text
act(observation_id, operation, image):
  ... 静态检查 → 取工作位置 → 认领 O → 更新租约
  fresh, raw, err := revalidateActTarget(...)       # 失败时也返回已取得的帧
  if err != nil:
      public := deliverRejectedFrame(work, session, obs, fresh, err.Code)   # §4 的 1–7
      if public != nil:
          markDelivered(op)                         # 供 planner 读取的事实
      result := not_executed(op, phase="revalidate", err)
      if public != nil:
          result.observation = public
          result.recovery = planRecovery(...)       # retry / O / effective
      return result
  ... 派发（唯一一次）→ 后观察
```

撤销与超时：所有提交都在既有提交闸门内完成；STOP 之后的工作不再发布（硬约束 2 与 4），迟到的交付候选被丢弃，结果由既有冻结路径返回。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/capture.go` | 把 `captureAndPublish` 的 delta 判定与提交块抽成 `publishTreeAttempt(work, session, phase, tree, forceFull, status, image)`，两条路径共用同一提交闸门；`captureAndPublish` 改为调用它，行为不变。 |
| `internal/engine/act.go` | `revalidateActTarget` 在失败时返回已取得的帧；新增 `deliverRejectedFrame`；失败分支在交付成功后挂上 `observation` 并标记 `op.Delivered`；更新 `target_changed` 与 `foreground_*` 的受控 message，去掉“请重新观察”，改为“本次未派发”，下一步由 `recovery` 表达。 |
| `internal/engine/engine.go` | `operationState` 增加 `Delivered bool`。 |
| `internal/engine/operations.go` | `finishOperation` 把 `op.Delivered` 传入 `recoveryInput`。 |
| `internal/engine/recovery.go` | `recoveryInput` 增加 `Delivered bool`；planner 在 `Claimed && Code != ""` 分支之前增加交付分支：`Delivered` 且 `outcome=not_executed` 时返回 `retry/O/effective`。 |
| `internal/eventlog/log.go` | 字段白名单增加 `format`（枚举 `full`/`delta`）。 |
| 测试 | 新增 `internal/engine/act_delivery_test.go`；更新 `internal/engine/recovery_entry_test.go` 的投影表与不变量。 |
| `docs/` | 见 §6.5。 |

### 6.2 配置与取值

不新增配置项。复用 `CaptureMaxAge`、`MaxFullTreeBytes`、`ObservationLifetime`、`WorkTimeout`；交付不改变任何超时预算，也不延长工作位置占用。

### 6.3 日志与可观测性

新增事件 `act.delivered`，字段 `state`（`published`/`skipped`/`failed`）、`code`、`format`、`phase`。不记录观察 ID、树内容、坐标或文本。交付是否发生只从响应与这条枚举日志可见，不新增响应成员。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 单向消费 `ready→claimed→retired` | 不变。旧 O 仍由 `releaseWork` 退役；交付的新 O 成为唯一的 ready current（`internal/engine/capture.go:264-277` 只退役 ready 的 current）。 |
| 同一 P 不派发两次 | 不变。失败尝试从未派发；交付是只读；重试必须是新 P。 |
| 提交条件化 | 复用同一提交闸门；STOP 后的交付候选不发布。 |
| 状态锁短小 | 公开树构造、delta 与编码都在锁外，锁内只做既有内存检查。 |
| 事实分层 | 不变。`outcome` 仍是 `not_executed`，`observation` 是独立一层，`error` 与 `recovery` 保持原义。 |
| 基线唯一确认入口 | 不变。交付不确认基线；`docs/architecture.md:163-165` 的两处入口不变。 |
| 严格请求接入 | 不变。没有新增请求字段。 |

### 6.5 契约与文档影响

`docs/interface.md`：

- §1 采集点：三个自动观察点改为四个，新增“`act` 重验失败的交付观察”，并说明它不做收敛采样（`docs/interface.md:35-38`）。
- §2：`retry` 定义改为“什么都没派发，且存在一个 ready、可用的 O 可作为下一次同一工具的输入；结果带 `observation` 时就是它，否则是本次请求引用的 O”；`input_state` 补一句指代规则（结果带 `observation` 时指该新 O）；说明 `observation` 不是成功专属（`docs/interface.md:67-89`）。
- §6：结果分支表 `not_executed` 行补 `observation` 组合；`target_changed` 段落改写并列出不交付清单（`docs/interface.md:333-341`）。

`docs/architecture.md` §4：full/delta 增加 `forceFull = !obs.Full` 一条，并说明交付不确认基线。

`docs/client.md`：错误结果上的 `observation` 必须解码、不得丢弃；`retry` 不等于“必须用请求里的 O”。

`docs/mcp.md`：说明错误结果也可能带树块（投影按成员通用渲染，无 schema 变化）。

`maintenance/decisions.md`：记录契约 5→6 的理由（同一错误码的 `recovery` 取值与结果形状改变）。

`release/versions.json`：`release_version` 0.6.0、`contract_version` 6、`android_version_code` 8，生成常量随源码提交；整组重装发布。

## 7. 预期效果与能力边界

### 7.1 能治什么

- 重验因页面变动失败后，调用方一次 `act`（新 P）就能在交付的新 O 上重试，少一次 `observe` 往返与一次重复采集。
- 交付的树是 delta 时，直接给出“手里那棵树到现在变了什么”，比整页重读更省 token。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 目标真的换了业务对象（列表换位、内容替换） | 交付只是新证据；能否重试由调用方重新选点决定，服务端不做语义猜测。 |
| ref 目标被非遮挡浮层或动画影响导致的误拒 | 身份与祖先链证明本身不含遮挡判断（`internal/engine/identity.go:8-17`），本提案不放宽判据。 |
| 坐标目标在动画中帧上选点失败 | 交付的帧可能仍处于动画中；`act` 的下一次重验是唯一守卫。 |
| `observe` 与 `open_app` 的类似失败 | 前者是只读工具，后者重验不采集，均无帧可交付。 |
| 已经派发后的 `uncertain` | 不在本提案范围，仍按 `status` 恢复。 |

### 7.3 成本

失败路径多一次公开树构造与编码（纯 CPU，毫秒级），响应体积增加一个观察：delta 时通常很小，full 时受 `MaxFullTreeBytes` 限制。成功路径零成本，超时预算不变。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 引用 full O，重验目标位移导致失败 | 结果带 `observation`，`tree.format=delta` 且 `base` 等于失败请求引用的 O；`recovery=retry/O/effective`；旧 O `retired`，新 O `ready` 且为 current |
| 引用 delta O 的同一场景 | 交付观察为 `tree.format=full` |
| `foreground_changed` | 交付成功，`recovery=retry/O/effective` |
| `foreground_unknown` | 不交付，`observe/P/retired`，无 `observation` |
| `display_unavailable` / `hierarchy_unavailable` | 不交付，`observe/P/retired` |
| 交付过程中 STOP | 不发布；结果按冻结路径返回 |
| 用交付的新 O 与新 P 重试 | 派发一次并成功；失败尝试本身零派发（设备 tap 计数 0/1） |
| 投影表 | 新增 `Delivered=true` 用例；`retry` 仍满足 `reference=O`、`input_state=effective` |
| 反向对照 | 关闭交付（测试注入）后，前两条断言必须失败 |

### 8.2 实机/系统级

1. 部署 0.6.0 后 `GET /health` 的 `release_version=0.6.0`、`contract_version=6`；
2. 复现一次重验拒绝（建立会话与观察后改变页面再 `act`），核对响应带 `observation` 且 `recovery=retry/O/effective`；
3. 用交付的新 O 与新 P 立即重试，核对 `outcome=executed` 与后观察；
4. 主机 `build/device-mcp.exe` 重建后跑 `scripts/mcp-probe`（stdio，`start`/`observe`/`act`/`end` 与错误路径），核对 12 工具及文本/结构化投影；
5. 手机侧 HTTP MCP 适配器由 `scripts/deploy.ps1` 拉起，核验 PID 与运行二进制的哈希。

### 8.3 记录

写入 `maintenance/verification/act-revalidation-observation-delivery.md`：实现提交、构建方式、设备与环境、实际命令、真实结果、未执行项与原因。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 交付时机 | 认领后、派发前的重验失败 |
| 帧来源 | 复用重验已取得的那一帧；不重新采集、不做收敛采样 |
| full/delta | `forceFull = !obs.Full` |
| 是否标记来源 | 不在公开面标记，只写日志 |
| 触发码 | `target_changed`、`foreground_changed`、重验阶段 `node_not_found` |
| 版本 | 契约 5→6、发行 0.6.0、versionCode 8 |

### 9.2 未决

无。实现中若发现判据冲突，按 `maintenance/process.md` §6 回到本提案修订并重新确认。

## 10. 实施顺序

1. 先写红的验收用例（交付成功、不交付分支、反向对照），确认失败；
2. 抽取 `publishTreeAttempt`，跑 `go test ./internal/engine` 证明既有行为不变；
3. 实现交付与投影改动，用例转绿；
4. 补投影表与 MCP 投影用例，跑全量 `go test ./...`；
5. 更新 `docs/` 与 `decisions.md`，过独立只读文档审计；
6. 提交实现；
7. 版本前进并生成常量、提交；
8. 运行 `scripts/build-release.ps1` 与 `scripts/verify-manifest.ps1`；
9. 运行 `scripts/deploy.ps1` 部署并核验；
10. 重建主机 `build/device-mcp.exe`，跑探针与实机验证；
11. 归档提案、写验证记录、更新 `maintenance/status.md` 并提交。

## 11. 实现结果

实现提交：`6095a09922d4a3ddfcee97184718abd6ec224be9`（2026-11-11）。版本提交：
`d7e1f76`（0.6.0 / 契约 6 / versionCode 8）。实现与验证记录：
[act-revalidation-observation-delivery.md](../../verification/act-revalidation-observation-delivery.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| §4 交付判据（码集、前台已知、无控制 UI、投影与体积、提交闸门） | `internal/engine/delivery.go` 的 `deliverRejectedObservation`、`deliverableRejection`；提交闸门由 `internal/engine/capture.go` 的 `publishTreeAttempt` 提供 |
| §4 `forceFull = !obs.Full` | `internal/engine/delivery.go` |
| §5 重验失败时保留已读取的帧并在交付成功后挂 `observation` | `internal/engine/act.go` 的重验失败分支、`markDelivered` |
| §6.1 投影：`Delivered` 事实 → `retry/O/effective` | `internal/engine/recovery.go` 的 `planRecoveryFor`、`internal/engine/operations.go`、`internal/engine/engine.go` |
| §6.1 复用重验已读取的那一帧，不重新采集、不做收敛采样、不确认基线 | `revalidateActTarget` 在失败时返回该帧；交付路径直接 `projectCapture` + `publishTreeAttempt` |
| §6.3 日志 | `internal/eventlog/log.go` 增加 `format` 枚举键；`act.delivered` 只写 `state`/`code`/`format` |
| §6.5 文档 | `docs/interface.md`、`docs/architecture.md`、`docs/client.md`、`docs/mcp.md`、`docs/integration.md`、`docs/runbook.md`；`maintenance/decisions.md` 的 D35 |
| §8.1 测试 | `internal/engine/act_delivery_test.go`（T13–T20）、`recovery_entry_test.go` 的投影表与 T3、`internal/mcp/result_text_test.go` 的交付观察文本投影 |
| MCP 描述与目录指纹 | `internal/mcp/schema.go` 的 act 描述；`internal/mcp/result_parity_test.go` 的指纹基线随契约 6 前进 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 提案 §2/§4 的不交付清单把“撤销”并入 `observe/P/retired` | 实现保持原投影：`control_revoked`/`session_ended` 仍为 `end/P/revoked`，只有取得帧后的交付门禁拒绝才是 `observe/P/retired`。`docs/interface.md` §6 已按代码写明；属提案措辞的精度问题，判据未变。 |
| 提案 §6.1 把交付逻辑放在 `act.go` | 拆到新文件 `internal/engine/delivery.go`，判据不变，便于与 `capture.go` 的发布闸门并列阅读。 |
| 提案 §4 的 forceFull 说明未限定 delta 条件 | delta 仍受“确认基线 + 基线龄/修订差 + 可编码 + 70% 收益”约束（`publishTreeAttempt`），不满足时交付 full；文档已补“满足上述条件时”。 |
| 提案 §8.1 的“交付过程中 STOP”测试 | 未加测试注入点；直接调用 `deliverRejectedObservation` 并在读取帧后置 `work.Revoked`，断言不发布（T18）。 |
| 提案未列：已完成视图的降级 | 重复 `P`、`status(P)` 与冻结结果的视图不带 `observation`，不能承诺 `retry`；`recoveryReplay` 把它降级为 `observe/P/retired`（T19/T20），`docs/architecture.md` 与 `docs/interface.md` 已写明。 |
| 提案 §8.1 的“交付过程中 STOP”与实机 delta 样本 | STOP 由 T18 覆盖；实机两次场景（9 节点与 128 节点）交付均为 `full`，未取得 delta 样本，delta 与 `base` 规则由 T13/T17 覆盖，原因见验证记录 §6。 |

---

## 附：本提案不改变的事项

- 不新增请求字段，不改变严格接入规则与 `X-Mobile-Use-Operation` 的语义。
- 不改变 `start`、`observe`、`end`、`status`、`inspect`、`keepalive`、`diagnose`、`apps`、`open_app`、`notify`、`shell` 的响应分支。
- 不改变静态拒绝（认领前）的 `retry`/`O`/`effective` 与 `input_state` 语义。
- 不改变 delta 五类块结构、MCP 工具清单与 schema、`result_mode`。
- 不改变账本字段、恢复屏障、STOP 与收尾语义。
- 不改变图片链路；交付的观察不带图片。
