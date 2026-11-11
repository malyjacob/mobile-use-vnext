# 提案：拆除工作期限与未决输入造成的会话级封锁

状态：提案，未实现。
基线提交：`015599ce73c99476dfff0a2a1f09ba7711824a0d`（分支 `master`，2026-11-11）。
适用范围：全部普通工具（`start`/`observe`/`act`/`open_app`/`apps`/`shell`）的准入、`housekeeping` 时钟、UI 输入预写与回执、受管 `shell` 的结案让位、服务重启恢复。不改变租约取值与 `keepalive` 行为；只把 `keepalive` 的既有语义写入契约面。
影响范围：`internal/engine`（`engine.go`、`ordinary_admission.go`、`ui_execution.go`、`ui_recovery.go`、`recovery.go`、`status.go`、`start.go`、`retirement.go`、`shell.go`、`ledger.go`）、`internal/mcp`（schema 描述文字）、`docs/`、`maintenance/`、`release/versions.json` 与生成常量。
契约（`/api/*` 字段与 `contract_version`）：改变。不新增/删除字段，但默认行为与执行语义变化：普通工具不再因未决输入返回 `409 device_busy`，会话不再出现 `reason=work_deadline`，受管 `shell` 超时后不再无限期占用工作位；私有账本版本 3 → 4。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的 `HEAD` 重新核对（重点：`engine.go` 的 `housekeeping`、`ordinary_admission.go`、`ui_execution.go`、`ui_recovery.go`）。

---

## 0. 摘要

一次 `adb shell` 传输抖动会让 UI 输入的回执变成 `DispatchUnknown`，预写记录停在 `may_dispatch`，于是：所有普通工作被拒（包括 `observe`）、`act` 之后第 50 秒整个会话被 `work_deadline` 撤销。这两件事都不是必需事实，而是把“一个毫秒级投递窗口”表达成“永久状态”的后果。本提案删除会话级封锁：工具级期限只冻结它自己那个 P 并释放工作位，不碰会话；输入回执未知在调用返回时即关闭窗口并记为事实；受管 `shell` 到期后结案让位，不再无限期占用工作位；恢复期那套不可达的重启要求一并删除。租约取值与 `keepalive` 行为本轮不动，只把后者的语义写进契约。

## 1. 问题

### 1.1 现象与代价

- `act` 成功返回后静默约 50 秒，会话被 `session.reason=work_deadline` 结束（真机 48 秒命中，issue #9 评论 1）。
- 输入回执未知时，`observe` 返回 `409 device_busy`；而该会话已经没有 `ready` 的 O，`act` 也无法进行，形成死锁，只能等会话被撤销（`act` 后最多 50 秒）。
- 会话被撤销即进入收尾：删除该会话登记的任务（含最近任务条目）并关闭虚拟屏，agent 打开的 app 与整段导航状态一并消失。
- **更正**：服务进程重启后，账本里可能仍有 `may_dispatch`；基线提交里那段“要求重启手机”的恢复逻辑（`resolveRecoveryUI`、`finishExecutionRecovery`、`uiRebootRequiredLocked`）**没有任何生产调用者**，只被测试引用，因此该现象当前不可达。它仍需要作为死代码与文档表述一并清理，但不应被写成现实代价。
- 代价按调用方计：一次传输抖动 = 一次会话重建 + 一次 app 重新导航；在长流程里等于任务中断。

### 1.2 证据链

| 断言 | 证据 |
| --- | --- |
| 三条期限字段与 `work_deadline` 撤销由同一提交引入 | `a6507cc`（提案 `cleanup-without-permanent-gate.md` 的实现提交）；`git log -S work_deadline` 只有它 |
| 到期直接撤销会话 | `engine.go:693-696` |
| 工作期限值与 act 的 50 秒吻合 | `operations.go:207-208`（`2*WorkTimeout+10s`）、`operations.go:222` |
| UI token 的期限取自 act 的 work 期限，且会话存活期间从不摘除 | `ui_execution.go:84-90`；`e.ui` 仅在 `retirement.go:128-130`、`ui_recovery.go:14/234` 被清空 |
| 已确认终态的 token 仍会触发撤销 | 条件是 `e.ui != nil`，未限定“未决”；引擎自己的判据是 `uiPendingLocked()`（`ui_execution.go:28-30`） |
| 未决输入阻断全部普通工作 | `ordinary_admission.go:14-24`；`observe` 走同一 `acquireWork`（`observe.go:35`） |
| 未决期间新会话也起不来 | `engine.go:534-541` 返回 `AvailabilityRecovering`/`ui_input_unconfirmed` |
| 回执未知使 token 永久停留 | `ui_execution.go:124-128` 提前返回，不改状态 |
| `DispatchUnknown` 的真实来源是 `adb shell` 传输失败 | `foreground.go:171` → `runShellV2`；`execution.go:147-152` |
| 恢复期要求重启手机 | `ui_recovery.go:36-161`、`ui_execution.go:268-329`；`grep` 无生产调用者，仅测试引用，当前不可达 |
| 投递窗口的物理尺度 | tap/key 瞬时、swipe 300ms、长按 700ms（`foreground.go:15`、`foreground.go:110`） |
| 盲目重试本已被 O 单向性挡住 | `act` 认领 `ready → claimed`，`releaseWork` 转 `retired`（`operations.go:247-250`）；回执未知分支不发布新 O（`act.go:78-86`） |

### 1.3 为什么直觉会出错

1. **“阻塞能防重复执行”**：不能。O 是单向消耗品，回执未知后会话里没有 `ready` 的 O，agent 必须先 `observe` 才能再 `act`——“先看一眼再动手”已经被 O 生命周期保证。屏障额外做到的只是禁止 `observe`，即禁止唯一能消除不确定的动作。
2. **“保守总是更安全”**：不成立。阻塞把唯一可用的取证路径关掉，到期又把物理状态拆掉；它没有降低风险，只是把风险从“可能重复一次”换成“确定丢掉整段工作”。
3. **“重启手机能证明没有在途输入”**：即便存在这条要求（当前不可达，见 §1.1 更正），它也是**要求证明**，不是**要求风险归零**。恢复屏障本身（等待 App 同步、恢复扫描）已经远超手势的毫秒级窗口，窗口在恢复开始时早已关闭。
4. **“不确定就该升级处理”**：把局部未决升格为全局事实，是本提案要拆掉的那一步。

## 2. 目标与非目标

**底线（用户 2026-11-11 明确，判据的最终依据）**

除外部不可抗力（显式 `end`、用户 STOP、App 实例变化、设备不可达、服务进程退出/重启、租约到期）以外，任何内部原因——注入回执未知、动作 `uncertain`、工作期限、输入期限、账本里一条未清的预写——都不得撤销会话、不得让会话无法继续使用、不得让整个服务锁死到只能重启手机。每个阻断都必须写明解除条件；没有解除条件的阻断不允许存在。

**目标（可判定）**

1. 会话只能因 `client_end`、STOP、租约到期、App 重启、服务退出、启动阶段失败而结束；工具级期限不得改变 `session.state` 或 `session.reason`。
2. 未决输入不得阻止 `observe`；普通工具的准入不再检查未决输入。
3. 删除不可达的恢复期重启要求及其死代码；恢复的实际路径与解除条件不变（App 同步 + 设备就绪）。
4. `act` 成功返回后，静默任意时长（在租约内）会话仍为 `active`。
5. 受管 `shell` 在 `ShellTimeout` + 终止确认预算到期后必须结案并让出工作位；之后无论那条进程是否仍在运行，都不得阻挡任何普通工具。

**非目标（本轮不做）**

- 租约的取值（`SessionTTL=120s`、`SessionHardLimit=1h`）与 `keepalive` 的行为不变；本轮只把它的既有语义写进契约面，不改行为、不改取值。
- `shell` 的时间常量取值：`ShellTimeout=30s`、`ShellStartTimeout=3s`、`ShellConfirmTimeout=5s`、`WorkShell=65s` 保持不变。
- 不新增 per-call 超时参数（`/api/shell` 仍只有 `reference`/`command`/`root`）。超时是服务策略，不是请求参数；若将来需要更长的命令，走后台任务模型，不与本轮混做。
- 账本设备身份不匹配的判定（`ui_recovery.go:96-112`）只存在于不可达的 `resolveRecoveryUI` 内，随死代码一并删除；删除前后实际行为相同。
- 输入重复执行的外部副作用防护（发送/支付类）：由调用方用“看一眼再决定”处理，本提案不新增幂等机制。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 维持现状 | 0 | 每次传输抖动丢一次会话与 app 状态；act 后 50 秒必死 |
| R2 只删会话撤销 | `engine.go:693-696` 删除，其余保留 | 小 | 未决输入仍锁死 `observe`，死锁依旧，只是会话不再死（更糟：永久阻塞） |
| R3 完整拆除（选定） | 删会话撤销 + UI 屏障 + 重启死代码；期限降级为“冻结 P + 脱离工作位”；回执未知即关闭窗口；受管 `shell` 到点结案让位 | 中：账本新增一个终态、一个孤儿槽、契约前进 | 两条输入可能落在同一帧；被放弃的 `shell` 可能与后续工作物理重叠 |
| R4 只调大期限 | `WorkAct` 50s → 300s | 小 | 死锁时间变长，问题性质不变 |

选 R3。R2 会把“到期死”换成“永久阻塞”，比现状更差；R4 只是把症状推远。

## 4. 判据

- **会话终止判据**只保留：显式 `end`、STOP、租约到期（含硬上限）、App 实例变化、服务退出、启动阶段失败。`housekeeping` 不得因 `e.work` 或 `e.ui` 的期限调用 `revokeSessionLocked`。
- **工作位兜底**：`work.Deadline` 到点时执行 `freezeWorkLocked` + 脱离工作位（`e.work = nil`、取消 ctx）。不改变会话、不改变任何 O、不触发收尾。
- **未决输入**的定义收窄为“调用尚未返回”：`may_dispatch` 且未收到任何回执。回执一旦返回（含 `DispatchUnknown`），窗口即关闭，记为 `unknown` 终态并持久化。
- **准入**：`ordinaryBarrierLocked` 不再检查 UI；`availabilityLocked` 不再因 UI 返回 `ui_input_unconfirmed`。
- **受管 shell 的时间上界**：屏障最多持续到 `run.DeadlineAt + ShellConfirmTimeout + 3s`（当前约 38 秒）。到期仍在运行、或终止无法确认时，结案为 `termination=unknown` 并让出工作位；保留一个**孤儿槽**（单槽，新者覆盖）供收尾再尝试终止一次。
- **让位不得声称干净**：孤儿槽存在时，`session.ExecutionUnknown` 保持为真；会话结束时 `cleanup.execution` 不得报 `confirmed`。
- **恢复**：删除不可达的重启要求与其判据；恢复的实际路径仍是 `driveRecovery → recoveryRetire`（`retirement.go:267-376`），解除条件不变。

### 4.1 同类核查：所有撤销与阻断的解除条件

| 撤销会话的触发 | 类别 | 处置 |
| --- | --- | --- |
| `end`（`client_end`） | 外部 | 保留 |
| 用户 STOP | 外部 | 保留 |
| 租约到期 / 硬上限 | 内部定时器 | 本轮不动（另案处理，不在本提案范围内） |
| 工作期限 / UI 期限（`work_deadline`） | 内部 | **删除** |
| App 实例变化（`app_restarted`） | 外部 | 保留 |
| 服务退出 / 进程重启（`service_shutdown`、`service_restarted`） | 外部 | 保留 |
| `start` 阶段失败（显示不可用、首帧失败） | 外部（设备/App 无法产出首帧） | 保留 |

| 阻断准入的原因 | 解除条件 | 类别 | 处置 |
| --- | --- | --- | --- |
| 未决输入 `device_busy` / `ui_input_unconfirmed` | 原来只有期限杀会话 | 内部 | **删除** |
| 工作位被占 `device_busy` | 该 handler 返回，或兜底冻结后脱离工作位 | 内部 | 保留兜底（自动解除） |
| 受管 shell 未终结 `shell_pending` | 进程消失，或到达 `DeadlineAt + 8s` 结案让位；孤儿槽留给收尾 | 设备事实 + 时间上界 | 保留，但加上界（见 §4） |
| 恢复屏障 `recovering` | 旧归属解析完成 + App 同步 | 外部 | 保留 |
| `ledger_unavailable` | 账本恢复可写 | 外部资源 | 保留 |
| `ui_input_unconfirmed`（账本设备身份不匹配） | 无（只在不可达的 `resolveRecoveryUI` 内） | 死代码 | **随死代码删除**；删除前后实际行为相同 |
| `session_exists` | 会话结束（`end`/STOP/租约） | 设计 | 保留 |
| STOP 边界未同步 `stopped` | App 下一次同步 | 外部 | 保留 |
| `ui_input_reboot_required` | 无（死代码，无调用者） | 内部 | **删除代码与 reason 映射**，不改变实际行为 |
| `legacyUI` 检查点未确认 | 无（同上） | 内部 | **删除代码与 reason 映射** |

> 说明：`resolveRecoveryUI`（`ui_recovery.go:36-161`）与 `finishExecutionRecovery`（`ui_execution.go:268-329`）在基线提交里没有任何生产调用者；`uiRebootRequiredLocked`（`ui_execution.go:54`）只被前者调用。恢复的实际路径只有 `driveRecovery` → `recoveryRetire`（`retirement.go:267-376`）：等 App 同步与设备就绪后一次性把旧归属转成退休记录并启动有界收尾，随后 `recovering=false`，不存在等待某个未知事实的循环。

## 5. 算法

```text
act 派发输入
  T0  预写 may_dispatch（账本）         ← 事实，保留
  T1  input 调用返回
        终态回执 terminated/not_dispatched → 记该状态，持久化
        未知回执 DispatchUnknown          → 记 unknown，持久化
  T2 之后：无任何普通准入检查；observe/act 正常进行；该 P 的 outcome 仍按原证据判定

handler 卡死（超过 work.Deadline）
  冻结该 P（outcome/phase/error 定死）
  脱离工作位（e.work = nil、cancel ctx）
  会话、O、收尾：全部不动

受管 shell
  0s      派发：记录 runner 身份与预写责任
  30s     ShellTimeout 到点 → 停止等待，终止精确进程组
  30–38s  终止确认预算（ShellConfirmTimeout + 3s）
  38s     仍未确认 → 结案 termination=unknown，身份存入孤儿槽，让出工作位
  之后    屏障消失；收尾链可再尝试终止一次；进程死活都不再阻挡普通工具

服务重启恢复
  账本里未清的预写与旧归属 → 转成退休记录 + 有界收尾；不阻塞新会话

会话终止
  只由 end / STOP / 租约 / App 实例变化 / 服务退出 / 启动失败 触发
```

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/engine.go` | 删除 `housekeeping` 中 `work_deadline` 撤销段；work 兜底改调新 helper；`availabilityLocked` 删除 `ui_input_unconfirmed` 分支；`launchCleanup` 的原因改用 `session.Reason` |
| `internal/engine/ordinary_admission.go` | 删除未决输入屏障；保留受管 `shell` 屏障（解除条件加上界，见 §4） |
| `internal/engine/ui_execution.go` | 删除 `uiDispatchState.deadline`；`recordUIReceipt` 对未知回执写 `unknown` 终态并持久化，不再提前返回；删除不可达的 `finishExecutionRecovery`、`uiRebootRequiredLocked` |
| `internal/engine/ui_recovery.go` | 删除不可达的 `resolveRecoveryUI` 及其独占 helper（`uiStateMayDispatch`、`uiExecutionNeedsPromotion`、`uiIdentityBoundary`、`legacyUIResponsibility`、`saveUIRecoveryCandidate`、`commitUIRecoveryCandidateLocked`、`cloneUIRecoveryLedger`）与对应测试；保留仍被恢复与收尾使用的 `restoreUIRecoveryLocked` |
| `internal/engine/recovery.go`、`status.go`、`start.go` | 清理 `ui_input_reboot_required` 的 reason 映射（该字符串不会再有生产者） |
| `internal/engine/retirement.go` | 新增“脱离工作位”helper；`freezeWorkLocked` 保留 |
| `internal/engine/shell.go`、`internal/engine/engine.go` | 新增孤儿槽字段；`confirmUnresolvedShell` 与 `finishShellTimeout`/`finishShellRevoked` 在 `DeadlineAt + ShellConfirmTimeout + 3s` 后结案为 `termination=unknown` 并清空 `e.shell`，身份转入孤儿槽；`beginCleanupLocked` 在孤儿槽存在时不得报 `execution=confirmed` |
| `internal/engine/ledger.go` | `ledgerVersion` 3 → 4，UI 状态取值增加 `unknown` |
| `internal/mcp/schema.go` | `act` 说明补充：回执未知时结果记为 `uncertain`，可继续观察；`keepalive` 的工具说明改写为 §6.5 的契约语义 |
| `docs/architecture.md`、`docs/interface.md`、`docs/mcp.md`、`docs/phone-operations.md` | 同步删除“未决输入阻塞普通工作”“到期撤销会话”“重启手机解除”的表述 |
| `release/versions.json` | 发行与契约号前进（见 §9.1） |

### 6.2 配置与取值

- `work.Deadline` 不再作为会话判据；取值沿用各工具的既有预算（`operations.go:201-215`），语义改为“工作位兜底”。
- `uiDispatchState.deadline` 删除，不再有第二个期限源。
- `shell` 的四个常量保持基线取值：`ShellTimeout=30s`、`ShellStartTimeout=3s`、`ShellConfirmTimeout=5s`、`WorkShell=65s`。上界推导：启动 23s + 执行 30s + 终止确认 8s = 61s，小于 65s 的工作位预算。
- 不新增任何对外字段、配置项或请求参数，包括 per-call 超时参数。

### 6.3 日志与可观测性

- 复用 `cleanup.start` 的 `reason`，改为写 `session.Reason`，消除“日志永远说 `lease_expired`”的矛盾。
- 新增 `ui.window_closed`（`state=terminated|not_dispatched|unknown`），只进事件日志，不进响应。
- 不新增正文、命令或输入内容。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威（engine 持有会话与授权） | 不变 |
| 单向消费 `ready → claimed → retired` | 不变；本提案依赖它保证“先看再动手” |
| 迟到结果不改判 P | 不变；`freezeWorkLocked` + `applyFrozenResult` 保留 |
| STOP/end 先撤权、不等待 | 不变 |
| 事实分层（执行/观察/终态/物理收尾） | 增强：投递窗口由时间事实表达，不再由状态表达 |
| 状态锁短小 | 不变；脱离工作位在锁内只做指针与标记操作 |

### 6.5 契约与文档影响

本次契约变化的实质不是“多了几条行为差异”，而是把一条**保证**写进契约：

> 除外部不可抗力（`end`、STOP、App 实例变化、设备不可达、服务退出/重启、租约到期）外，任何内部原因都不得撤销会话、不得让会话无法继续使用、不得让服务锁死到只能重启手机。

- 该条作为规范性语句写入 `docs/architecture.md`（会话与准入）与 `docs/interface.md`（错误与恢复），不再只存在于 `maintenance/`。
- 行为差异是它的推论：不再因未决输入 `device_busy`；`session.reason` 不再出现 `work_deadline`；受管 `shell` 超时后不再无限期占用工作位；账本版本 4。
- `ui_input_reboot_required` 及其 reason 映射作为死代码清理，不构成行为变化。
- 契约前进依据 `docs/release.md:43-44`：默认行为与执行语义变化，且保证本身进入契约。
- `docs/` 涉及架构、接口、MCP 与运行手册四处，须走独立只读文档审计。

**`keepalive` 的契约语义（同一轮写入 `docs/interface.md` 与 MCP 工具说明）**

| 项 | 契约表述 |
| --- | --- |
| 作用 | 把当前活动会话的滑动租约推到 `min(now + 120s, started_at + 1h)`；只增不减 |
| 不作用 | 不延长任何 O 的 `actionable_until`，不改 O 状态，不确认基线，不占工作位，不访问设备，不写账本正文 |
| 拒绝条件 | 会话 `ended` → `session_ended`；`starting` → `reference_has_no_session`；已越线 → `session_expired`；重复 P 只回原视图、不再续租 |
| 保证 | 本提案生效后，时间型撤销只剩租约一族：在 1 小时硬上限内，只要相邻两次续租（`keepalive` 或被接纳的普通工作）间隔不超过 120 秒，会话不会因任何内部原因结束 |
| 边界 | 硬上限 1 小时不可延长；跨过硬上限只能重新 `start`；`keepalive` 不能保活超过硬上限，也不能保住任何 O |

这五行是“名字叫 keepalive、行为是 renew”的正式交代：契约面只承诺租约，不承诺“调用后永不结束”。

### 6.6 发布与部署范围

整组发布，四件产物都要重建；缺一件就会立刻出现契约不匹配：

| 产物 | 原因 | 动作 |
| --- | --- | --- |
| `device-service`（linux/arm64） | 契约 7 与行为变化 | `scripts/build-release.ps1` |
| `control.apk` | 编译期 `CONTRACT_VERSION` 随版本源生成 | 同上；覆盖安装，保留 App 私有数据 |
| 手机侧 `device-mcp`（linux/arm64） | 二进制内含契约常量，旧二进制仍按 6 发请求 | 同上；`deploy.ps1` 装入 chroot |
| 主机侧 `build/device-mcp.exe` | 同上；issue #9 的宿主用的就是它 | `go build -o build/device-mcp.exe ./cmd/device-mcp` |

- `release/versions.json`：`release_version` 0.7.0、`contract_version` 7、`android_version_code` 9；其余常量由 `scripts/generate-version.ps1` 生成，不手改。
- 部署：先 `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`，再 `scripts/deploy.ps1 -SkipBuild`；覆盖安装保留 App 私有数据。
- 主机 MCP 宿主必须**重启适配进程**才会重新发现工具（`docs/release.md` §4 第 5 步）；stdio 宿主用 `build/device-mcp.exe -transport stdio -config .state/mcp-config.json -result-mode text` 复核。
- 验收：`/health` 的 release/contract 与清单一致；`/api/status` 的 `can_start=true`；MCP 工具目录仍为 12 个；`keepalive` 返回的 `session.expires_at` 能被下一次工具调用兑现。

## 7. 预期效果与能力边界

### 7.1 能治什么

- `act` 之后不再有 50 秒自杀。
- 回执未知后可以立即 `observe`，用一帧新树收敛不确定。
- 受管 `shell` 最多占用工作位约 38 秒，之后无论进程死活都让位；收尾仍能再尝试终止一次。
- 删除死代码与两个期限源，`housekeeping` 只剩租约一条会话判据。
- 服务重启后的“重启手机”要求作为死代码清除（该路径本就不可达）。
- **`keepalive` 的语义随之变得确切**：拆完之后，时间型撤销只剩租约这一族——滑动期限（`keepalive` 能延长）与 1 小时硬上限（谁也不能延长）。不再有第三个期限在旁边把会话打死，“续了约却仍被别的期限打死”的矛盾消失。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 输入被重复执行 | 本提案不提供幂等；窗口关闭只意味着“不会再有新的投递”，已经投递的那一次仍是既成事实 |
| 回执未知后立刻 act 造成“两条输入落在同一帧”的归属模糊 | 接受；O 的单向消耗要求先 observe，实际路径上由看得见的一帧兜底 |
| 卡死的 handler 被脱离后，设备上仍有旧命令在跑 | 接受；与 AGENTS 第 5 条已接受的“旧会话命令不保证撤回”同类 |
| 被放弃的受管 `shell` 可能仍在设备上运行 | 接受；到点让位是刻意的，孤儿槽只保证收尾能再试一次终止、且不谎报 `confirmed` |
| 租约到期仍会结束会话并拆除资源 | 本轮不改；见 §2 非目标 |

### 7.3 成本

- 删除两个期限、一个全局屏障与一段死代码；代码净减少。
- 新增一个 UI 终态、一个孤儿槽与一次账本版本前进。
- 每次回执未知多一次账本写（本来也要写终态，只是过去写成“不写”）。
- `shell` 到点让位后，收尾可能多一次终止尝试；那条进程若仍在跑，与下一次普通工作发生物理重叠的风险由调用方承担。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| `act` 成功 → 时间推过 `work.Deadline` → `housekeeping()` | `session.State == active`，`reason` 为空；该 P 的 `outcome` 不变（反向对照：恢复撤销即失败） |
| `act` 回执 `DispatchUnknown` → 立即 `observe` | `observe` 成功，发布新 O；原 P `outcome=uncertain` |
| handler 卡死不返回 → 工作位兜底到点 | 会话 `active`；`e.work == nil`；新 `observe` 可取到工作位；冻结的 P 不被迟到结果改写 |
| 账本 `may_dispatch` + 服务重启 | 恢复不进入任何“重启手机”要求，`can_start` 随 App 同步恢复为真 |
| 受管 `shell` 人为超时（`sleep 70`）且终止无法确认 | 约 38 秒后会话仍 `active`、工作位可被新工作取得；该 P `outcome` 不变；`cleanup.execution` 不得为 `confirmed` |
| 内部 `uncertain` 穷举（表驱动：`act` 回执未知、`shell` 四条 uncertain 路径、`open_app` 关联失败） | 每一条都不改变 `session.state`、不改变 `session.reason`，且下一次普通调用不返回 `device_busy` |
| `legacyUI` / `may_dispatch` 账本 + 服务重启 | 恢复路径与基线一致；删除死代码后仍能完成恢复并放开准入 |
| `end` / STOP / 租约到期 | 行为与基线一致（防止误删会话终止路径） |

### 8.2 实机/系统级

- 部署后按 issue #9 的最简复现：`start` → `act` → 静默 60 秒 → `status()`，要求 `session.state=active`。
- 用一次真实 `input` 调用制造回执未知（或在服务侧注入 `DispatchUnknown`），随后 `observe` 必须成功。
- 重启服务进程（不重启手机），要求普通准入恢复。
- 用一条人为超预算且杀不掉的 `shell`（例如注入终止失败）确认：约 38 秒后普通工具恢复可用，`status` 如实报告存在未确认结束的命令。
- 主机侧 stdio 适配器（issue #9 的宿主形态）：用重建后的 `build/device-mcp.exe -transport stdio -config .state/mcp-config.json -result-mode text` 跑一轮工具目录 + `act` + `keepalive`，确认契约 7 被接受、12 个工具齐全、`keepalive` 返回的 `expires_at` 在随后静默期内不再被别的期限推翻。
- 手机侧 HTTP 适配器（`127.0.0.1:18882/mcp` 与主机转发的 `18883/mcp`）同样核对契约与工具目录。

### 8.3 记录

写入 `maintenance/verification/remove-work-ui-deadline-lockdown.md`，注明实现提交、环境、命令与真实结果；单测与实机结论分开写。

### 8.4 收尾：回复并关闭 issue #9

归档并推送之后，在 issue #9 留言并关闭。留言只写已发生的事实：

1. `work_deadline` 撤销会话的机制已删除：会话不再因工具级期限（`e.work.Deadline`、`e.ui.deadline`）结束，`session.reason` 不再出现 `work_deadline`。
2. 未决输入不再阻塞 `observe` 或任何普通工具；回执未知在调用返回时即结案为 `uncertain`。
3. 受管 `shell` 保留屏障但加上界（约 38 秒）；到点结案让位，`cleanup.execution` 不会被谎报为 `confirmed`。
4. `keepalive` 的语义因此变得确切：时间型撤销只剩租约一族——滑动期限（`keepalive` 能延长）与 1 小时硬上限（不可延长）；不再有第二个期限在旁边把会话打死。
5. 版本 0.7.0 / 契约 7 / versionCode 9，部署与实机验证记录链接。
6. 明确保留的风险：重复执行仍由调用方用“看一眼再决定”处理；本版本不提供幂等。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 底线 | 不得为了“不出错”而锁死后续操作；违反者一律拆除（用户 2026-11-11 明确） |
| 重复执行风险 | 接受；两条输入不可怕，观察一次即可 |
| 工作位兜底 | 保留，但只冻结 P、脱离工作位，不碰会话 |
| 受管 `shell` 屏障 | 保留但设时间上界；到点结案让位，身份转入孤儿槽（方案 A：能再试一次终止，且不谎报 confirmed） |
| `shell` 时间常量 | 保持 30s / 3s / 5s / 65s；不新增 per-call 超时参数（用户 2026-11-11 决定） |
| 账本版本 | 3 → 4（UI 状态取值扩大） |
| 契约 | 6 → 7（默认行为与执行语义变化） |
| 契约即底线 | §6.5 的保证句写入 `docs/`，成为契约的一部分，而不只是内部行为 |
| `keepalive` 契约面 | 语义按 §6.5 的表写入 `docs/interface.md` 与 MCP 工具说明；只承诺租约，不承诺保活超过硬上限 |
| 发布范围 | 服务、APK、手机侧 `device-mcp`、主机侧 `device-mcp.exe` 四件齐发（见 §6.6） |
| 发行 | 0.7.0 / Android versionCode 9（沿用整组发布流程） |
| 收尾 | 归档并推送后，在 issue #9 说明变化并关闭 |

### 9.2 未决

本轮无未决项。租约取值与 `keepalive` 行为不在本提案范围内，按 §2 非目标处理。

## 10. 实施顺序

1. 先红用例：`act` 成功后推过工作期限，断言会话仍 `active`（基线必红）。
2. 删除 `housekeeping` 的会话撤销与 `uiDispatchState.deadline`，加入工作位脱离 helper。
3. 删除 UI 准入屏障；`availabilityLocked` 去掉 `ui_input_unconfirmed` 分支。
4. 回执未知写 `unknown` 终态并持久化；账本版本与状态取值同步。
5. 受管 `shell` 的到点结案与孤儿槽；`beginCleanupLocked` 不得谎报 confirmed。
6. 删除不可达的恢复期重启要求及其 helper 与 reason 映射。
7. 跑变更所涉回归（`internal/engine` 全量、`internal/mcp` schema 与结果测试）。
8. 同步 `docs/`（含 §6.5 的保证句），独立只读文档审计。
9. 实现提交。
10. 版本源前进到 0.7.0 / 契约 7 / versionCode 9，生成常量；`build-release.ps1` + `verify-manifest.ps1`。
11. `deploy.ps1 -SkipBuild` 整组部署（保留 App 私有数据）；重建主机侧 `build/device-mcp.exe` 并重启宿主 MCP 进程。
12. 按 §8.2 实机验证，写验证记录；提案归档（`implemented/`，§11 注明实现提交）。
13. 推送后回复并关闭 issue #9（见 §8.4）。

## 11. 实现结果（归档时补写）

---

## 附：本提案不改变的事项

- 租约 `SessionTTL=120s`、`SessionHardLimit=1h` 与 `keepalive` 行为。
- STOP/`end` 的撤权、5 秒逻辑交接与独立收尾链。
- `may_dispatch` 预写本身、`freezeWorkLocked`、`applyFrozenResult`。
- `shell` 的执行预算常量与 `WorkShell`；shell 屏障仍然存在，只是解除了无限期等待。
- `apps` 的独立工作类别；12 个工具名、请求 schema 与结果字段。
