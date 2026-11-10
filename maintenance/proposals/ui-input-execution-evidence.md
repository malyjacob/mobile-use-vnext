# 提案：分开记录 UI 动作结果与执行结束证据，并为旧账本提供恢复路径

状态：提案，未实现。
基线提交：`ae1fbbc35f32e0dbeb2eda7a519e825f936601e9`（分支 `master`，2026-11-10）。
适用范围：`act`、`open_app`、shell 与 UI 屏障隔离、STOP/end、启动恢复与旧账本迁移。
影响范围：`internal/device`、`internal/android`、`internal/engine`、`internal/store`、恢复投影与测试；正式文档、版本生成与发布材料随实现同步。
契约（`/api/*` 字段与 `contract_version`）：请求与响应的 JSON 结构不变；改变 `cleanup.execution` 的解除判据、普通工作准入和恢复指引，拟将共同 `contract_version` 从 3 升至 4。

函数名、行号、常量与默认值均以基线为准；代码前进后须重新核对，重点是 `openapp.go`、
`act.go`、`operations.go`、`cleanup.go`、`shell.go`、`control.go` 与账本读写。
设计源头基线是 `mobile-use-platform` 的 `codex/vnext-delta-redesign` 分支
`95286cdf830bd3f29a9ad77ed82d52683fb5c45d`。

## 0. 摘要

用户已整体确认本文，并授权按项目规范实施，包括执行事实分层、旧账本恢复与版本方案。
任务关联或文字读回失败继续令原操作为 `uncertain`，但不再自动等同于输入调用仍可能在途。
每个输入子步骤单独持有身份与结束证据，shell 只能解除自己的屏障。
真实缺失结束证据时封闭普通工作；旧记录不能补造完成回执，使用持久化启动身份与后续手机重启恢复。
前台观察、显示消失、超时、重启适配器均不单独构成执行结束证据。

## 1. 问题

### 1.1 现象与代价

原报告与先红用例见 [issue](../issues/open-app-unconfirmed-permanent-lock.md) 和
[归因记录](../verification/ui-input-execution-barrier-investigation.md)。
`open_app` 的启动调用返回 accepted，随后任务关联失败时，会留下跨工作位和重启的
`ui_input` execution 屏障。即使后观察可读、物理三项确认，新 `start` 仍被拒。

同一标记也存在反向错误：先前 UI 输入为 unknown，后续 shell 正常结束会把它清为 false。
因此需要同时消除无结束依据的放行和已结束调用造成的持续阻塞。

### 1.2 证据链

| 落点 | 证据 |
| --- | --- |
| `internal/android/launch.go:59–89` | `am start -W` 的接受事实与任务归属分别取证 |
| `internal/engine/openapp.go:178–194` | accepted 后的任务关联失败仍写执行 unknown |
| `internal/engine/operations.go:311–322` | 不确定性进入单一会话布尔标记 |
| `internal/engine/act.go:448–531` | 聚焦 tap、文字 set、目标绑定与后续读回共用最终确认窗口；只读检查失败也能保留整个输入窗口的 unknown |
| `internal/engine/cleanup.go:49–86,298–325` | unknown 被归入 UI 或 shell 单一来源，并决定 cleanup 完成门 |
| `internal/engine/control.go:554–557` | 旧 execution unconfirmed 无法越过恢复门 |
| `internal/engine/shell.go:462,545,660–664` | shell 直接清整个会话标记，清理提升入口却仅识别 shell 来源 |
| `internal/engine/ledger.go:101–114` | 旧 UI 屏障只有类别，没有输入调用身份、回执或 Android boot identity |
| `internal/store/ledger.go:78–80,115–118` | 当前只接受并写入私有账本版本 1 |

诊断 fixture 的缺任务、多任务两种模拟都复现物理三项 confirmed 后的持续阻塞与恢复阻塞；
第三项先红用例证明 shell 覆盖更早 UI 标记。四个既有对照测试通过，说明 UI 保持条款是
当前规则，而非单个分支偶然走错。详见归因记录中的实际命令和证明窗口。

### 1.3 为什么直觉会出错

- 看到 App 在前台不能证明原启动调用具有唯一、同屏的任务归属；原 `P` 保持 uncertain。
- task、recents、display 均已释放，也不能证明一个丢失回执的输入调用已经结束。
- `am` 已返回可信退出结果而任务列表关联失败，是“调用已结束、目标动作结果未完整确认”。
- 更晚的 shell 结束只证明这个 shell 的链结束；不能解除另一个 UI 调用的责任。
- 旧账本缺少原调用身份，不能在升级时把类别字符串转换成完成证据。

## 2. 目标与非目标

目标：accepted 后任务关联失败、聚焦完成后的只读绑定失败、set 完成后的读回失败，均不再
制造无结束途径的执行屏障；原动作的结果仍按原证据判定。对真实未确认调用封闭普通工作，
并提供不清账本、不清 App 私有数据的明确恢复步骤。

范围不包含调整任务关联的唯一性/同屏判据或 2.6 秒累计等待策略，不增加重放、强制清屏障
工具或后台动作队列，不追认历史业务成功，也不通过重启 App/适配器代替 Android 重启证据。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 最小改动 | 调整启动确认窗口或查询来源，保持原执行标记 | 改动较少 | 减少触发概率；不能处理文字、置前、真实丢失回执与旧记录 |
| R2 本提案 | 输入子步骤独立记录结束证据，结果与屏障分别提交；旧记录用启动身份恢复 | 每个子步骤增加持久化；旧缺证记录可能需一次手机重启 | 证据读取或持久化失败时仍阻塞，明确要求运维处理 |
| R3 全面受管 UI runner | 给每个 ADB/RPC 副作用增加设备侧持久回执与精确探测/终止 | 扩展 runner、固定 JAR 或服务端 RPC，制品与时序更复杂 | 仍需处理旧无身份记录；服务端任务或回执异常时仍不能凭观察放行 |

选择 R2。它解决报告中的正常返回后误粘屏障，且给真正丢失回执保留可执行的恢复路径。
首版不同时引入 R3；若实机证明真正丢回执频繁到不能接受重启代价，再单独提案扩展受管回执。

## 4. 判据

### 4.1 两种独立事实

`outcome` 仍表示工具承诺的主动作是否完整确认；输入结束证据只表示本次受控的输入调用
不再在途，不表示 Android 应用全部异步行为已结束，也不表示业务任务完成。

适配器返回纯事实 `DispatchReceipt`，枚举零值必须为 unknown；它不持有输入授权或会话状态。
由引擎把事实绑定到不可变的 `(session epoch, P, step, boot identity)`：

| receipt | 可接受的依据 | 对 execution 的影响 |
| --- | --- | --- |
| `not_dispatched` | 在远端调用前明确失败，或引擎在实际派发线性化点前撤销 | 仅解除这一个子步骤 |
| `terminated` | 本次 ADB shell v2 调用正常收到远端命令退出结果；或本次同步文字 RPC 收到匹配且可校验的完整方法结果 | 仅解除这一个子步骤；退出非零、accepted=false 或动作后验失败不自动变成“仍在途” |
| `unknown` | 已可能派发，但取消、超时、连接中断、报文不完整或终止证明缺失 | 保留本次责任，不自动重放 |

`terminated` 不能由本地 `adb` 进程被杀、上下文取消、任意 HTTP 200 或“现在没看到进程”推导。
ADB 后端必须确认设备支持 shell v2，并对传输错误和设备命令退出分别分类；不支持时输入
预检拒绝，不降级使用没有退出状态的通路。AOSP 的
[服务说明](https://android.googlesource.com/platform/packages/modules/adb/+/refs/heads/main/docs/dev/services.md)
明确区分 shell v2 的远端退出状态能力；它本身不证明当前部署已通过该项验证。

文字 RPC 的完成证据只来自本次 HTTP 交换的同步方法返回：校验 JSON-RPC 标识、响应 ID 与
必需结果成员的实际存在/类型。固定 JAR 的 `issued/accepted` 含义以
[制品说明](../../third_party/uiautomator/README.md) 为准；正文不进入证据账本。
报文缺失或不完整继续 unknown，不用读回正文、树或后发 RPC 补造旧调用回执。

### 4.2 屏障与所有权

- UI 子步骤按序执行；前一子步骤没有结束证据时不派发下一步。
- 已释放普通工作位但仍有 unknown UI 子步骤时，不接纳 `start/observe/act/apps/open_app/shell`。
  它们立即 busy，不排队；`status/inspect/diagnose/end/STOP` 继续可用。
- 同时最多一个未知 UI 子步骤；保留独立 UI 责任与现有 shell runner，不能由 shell 写 UI 责任。
  旧/异常记录若包含两类责任，两类都必须分别解决。
- shell 的原结果、输出与回执规则保持；其完成只移除匹配 runner 的责任。
- 原 `P` 的 uncertain 不因结束证据、后观察、收尾或重启而改判；证据提升仅作用于 execution。

### 4.3 收尾提交

`cleanup.execution=confirmed` 当且仅当原会话所有已可能派发的 UI 子步骤与 shell 责任都有
各自的结束/未派发证据，或者满足 §4.4 的设备启动边界证据。
此外，所有适用的物理资源项必须有有效证据，cleanup 才能 complete。

STOP 期间可独立执行物理收尾，但在 unknown 输入之后读到的晚到完成回执，会使早于该完成
边界的物理核验失效。引擎须对同一原归属补做有界的只读核验，再提交 complete；不因单独
提升 execution 就立即放行。旧 task 或 recents 仍存在时保留相应 unconfirmed。
这不是再次派发原动作，也不创建第二条破坏性清理链。

### 4.4 启动边界与旧记录

新 UI 责任在派发前记录目标设备的稳定私有身份与 Android `boot_id`；不同服务实例、PID、
App instance 或 display generation 都不能代替 boot identity。

同一设备上，已持久化的责任 boot identity 与当前 boot identity 不同，可证明旧 Android
用户态调用不再运行。此时只解除对应 execution 责任，其余恢复条件仍逐项核验。
`boot_id` 的启动内稳定性依据
[Linux 内核文档](https://docs.kernel.org/admin-guide/sysctl/kernel.html#random)；此处的解除规则
是基于设备重启边界的设计推论，仍需在目标 Android 环境实测。

版本 1 的 `ui_input` 未确认记录没有旧 boot identity。版本 1 中尚未结束的会话又没有完整的
输入终结记录时，同样按 legacy 未决责任迁移；不能把缺少新字段当作没有发生过输入：

1. 载入后保持准入关闭，不假定已经重启过，也不使用账本墙钟时间或 PID 推算。
2. 在同一固定运行目录锁下读取可信设备身份和当前 boot identity，写入版本 2 的
   `legacy_ui_checkpoint`；保留全部旧 Session/Cleanup/Stop/Shell/任务事实。
3. 只有 checkpoint 持久化成功，才投影 `blocking_code=ui_input_reboot_required`。
   用户以正常运维方式重启手机；服务不会自动重启手机。
4. 后续同设备 boot identity 改变，才可解除这份 checkpoint 覆盖的 legacy UI execution。
   普通同步、服务重启和恢复重试不得覆盖初始 checkpoint。
5. 恢复完成仍要求配对 App 新鲜、模式/STOP 通道可用、原物理资源经终结边界后的复核满足条件、
   账本可写；旧三项 confirmed 不能代替这次复核。

当前 issue 的 execution-only 屏障（物理三项已确认）是这条迁移路径的明确适用对象。
物理资源仍未确认的旧记录不会被一并放行；跨 boot 不向可能复用编号的新显示发送旧
`close_display`，也不按裸 task ID 关闭新主屏窗口。无法证明原资源归属时继续阻塞并单独处置。

## 5. 算法

### 5.1 输入子步骤

```text
准备子步骤：确认原会话/代次/工作拥有者/期限；生成不可变 token
  → 记录 may_dispatch + 目标设备/boot identity
  → 锁外 persistSync，失败则不派发
  → 锁内重新核验授权，与 STOP 在同一派发边界竞争
  → 锁外调用一次适配器
  → 将 receipt 绑定回原 token；不要求原会话仍有输入授权
      not_dispatched/terminated：只解除该 token 的执行责任
      unknown：保留责任，不再发起本工具的其它输入步骤
  → 按原工具规则独立确定 outcome/后观察
  → 锁外保存完成证据；保存成功前不开放下一普通工作
```

`may_dispatch` 的预写覆盖“落盘后、实际调用前崩溃”的保守窗口；进程内能证明未跨实际边界
时可以记 not_dispatched。进程重启后若缺终结记录，仍按可能派发处理。

多个已证实完成的子步骤可将前一步终结与下一步 may_dispatch 合并为一次预写；不能跨过
未决子步骤，不能把未来将收到的回执提前写成完成。终结保存失败不反转已确认的主 outcome，
但账本不安全期间维持准入关闭，并返回对应恢复指引。

### 5.2 各工具的窗口

| 路径 | 输入子步骤与解除时机 | 主结果 |
| --- | --- | --- |
| 新建 `open_app` | launch 返回终结回执后先结束该调用责任，再核验唯一同屏 task | 关联失败仍 uncertain；不重新添加执行责任 |
| 复用 `open_app` | focus 的终结回执与后续 foreground 核验分开 | foreground 未确认仍按原规则 uncertain |
| 普通 `act` | tap/long_press/swipe/key 的本次终结回执 | 保持既有主动作证据规则 |
| `act(text)` 聚焦 | tap 独立结束，之后只读 capture/目标绑定/GetText | 只读失败不会重新打开已结束的 tap 责任 |
| `act(text)` 写入 | set 单独预写与派发；方法返回后结束调用责任，再做独立读回 | 读回不匹配或失败保持 uncertain；不重复 set |
| shell | 原 runner 身份与终结证据 | 仅修改自身责任；不清 UI 标记 |

### 5.3 STOP、迟到结果与恢复

STOP/end 仍先撤权、关闭派发、取消工作并发起独立收尾，立即返回；不等待预写或终结落盘。
任何处于落盘等待的工作返回后都重验授权，不能越过 STOP 派发。

迟到回执可通过精确 token 补充原执行结束事实；不能发布 O、复活原会话、改变已提交 outcome，
也不能解除新会话或另一 token。物理核验使用证据代次，最终提交前重验原归属和该代次。

运行恢复沿用后台 housekeeping，无新的普通动作队列。unknown 回执缺失时，原进程内没有
其它可靠结束来源就准备启动 checkpoint；旧记录按 §4.4 处理。读取启动身份、持久化或资源
核验超时都维持关闭，不以“等够了”放行。

## 6. 实现方案

### 6.1 接口与文件

| 文件族 | 改动 |
| --- | --- |
| `internal/device/types.go` | 增加纯事实 receipt 与目标设备/boot identity 类型，unknown 为零值 |
| `internal/android/{device,foreground,launch,text,rpc}.go` | 变更输入方法的内部返回，区分远端终结与语义失败；读取和校验 boot identity、shell v2 能力；校验文字 RPC 的完成报文 |
| `internal/engine/{engine,operations,act,openapp}.go` | 引擎拥有子步骤 token、预写与独立提交；统一普通工作屏障 |
| `internal/engine/{cleanup,control,recovery,recovery_retry}.go` | late evidence 后的物理重验、启动身份恢复与指引投影；保留原归属 |
| `internal/engine/{shell,shell_recovery}.go` | 移除清整个会话布尔值的路径，只消费匹配 shell 责任 |
| `internal/engine/ledger.go`、`internal/store/ledger.go` | 账本 v2、v1 读取与有界迁移、immutable snapshot 与恢复 checkpoint |
| 对应测试与 `maintenance/verification/` | 把先红诊断改成批准判据下的回归用例；记录反向对照和实际验证 |

内部输入接口改为携带 receipt：普通输入及 focus 返回 `(DispatchReceipt, error)`；launch 和
text 的原事实结构携带同一 receipt。适配器不新增会话表，也不依据 receipt 决定执行授权。

v2 最多一个 UI 责任：`P`、有界 step 编号、输入类型枚举、原会话/显示代次、私有目标设备身份、
boot identity 与 pending/终结状态。与原 shell 字段独立。legacy checkpoint 保存原归属键、
目标设备身份与初始 boot identity；不保存请求、输入、命令、文字读回或原始响应。
保持账本 64 KiB 上限；未知格式或身份冲突保持 blocked。

### 6.2 配置与取值

- 复用 `LedgerTimeout=3s`、`CleanupTimeout=12s` 与既有普通工作上限；不新增客户端超时参数。
- boot identity 读取使用既有有界设备超时；正常会话建立时取得，输入预写绑定该身份。
  当前会话绑定失效或设备重连身份未知时停止输入，重新核验后才可继续。
- 终结后的物理只读复核至多使用一次既有 `CleanupTimeout` 窗口；超时返回未确认，不无限等待。
- 私有账本格式升至 2；读取 1 和 2，只有正确解码并保留责任后才写 2。旧二进制拒绝 2，禁止
  覆盖降级；回滚须使用兼容 v2 的修复构建，不删账本。
- 拟发行 `0.4.3`、共同契约 `4`、Android versionCode `6`。实施时仅修改
  `release/versions.json`，再运行仓库现有生成/构建/清单核验流程；基线前进则重新核对取值。

### 6.3 日志与可观测性

复用 `cleanup.*`、`recovery.*`、`ledger.save_failed` 与操作阶段事件；需要新增时仅允许
`P/step/kind/state/evidence` 等受控 ID、枚举、布尔和数值，并同步事件白名单。
不记录目标设备稳定身份、原始 boot 文件、命令、输入、回执正文或异常字符串。

`status.can_start`、`blocking_code`、`cleanup` 五项与 `recovery` 继续承担公开解释：

| 情况 | 公开表现 |
| --- | --- |
| 当前 UI 调用仍缺终结证据 | `can_start=false`，内部原因 `ui_input_unconfirmed`；其它普通调用立即 `device_busy` |
| checkpoint 已持久化，等待用户重启 | `blocking_code=ui_input_reboot_required`，`recovery.entry=user/reference=none` |
| checkpoint/终结证据保存失败 | 账本不安全原因优先，`recovery.entry=status`；不提示已具备重启恢复证明 |
| execution 已确认但物理项未确认 | 沿用相应 cleanup 阻塞；不能转成可开始 |
| 全部条件成立 | `can_start=true`；原 P 的 uncertain 保持 |

无新增 Agent 工具、force 参数或恢复清除 API；手机重启是由用户控制的运维动作。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 引擎唯一权威 | receipt 为设备事实，授权、屏障、提交和恢复仍由引擎持有 |
| STOP 不等待 | 不等设备、证据保存或物理复核；等待结束后再次核验派发权 |
| O 单向消费/P 不重发 | 沿用；只读失败不归还 O，恢复不重放输入 |
| 条件提交/状态锁短小 | 所有 I/O 在锁外；token/代次与原归属在锁内有限重验 |
| 执行、观察、终态、物理事实分层 | outcome 不改判；execution 只接受自己的终结证据 |
| 恢复先于准入 | 预写责任、终结持久化与设备启动证据都进入恢复门 |
| 日志与消息边界 | 不引入正文持久化或 Agent 已读通路 |
| 签名、配对与版本单源 | 覆盖安装保留数据；共同版本生成并核验 manifest |

### 6.5 契约与文档影响

确认提案后，先以独立 docs-only 变更修订设计源头的 `vnext-interface-spec.md`、
`concurrency-and-observation-commit.md`、`task-lifecycle.md` 与相关恢复说明中受影响的定义；
仅修改实际涉及的条款，保留主动作 uncertain 不追认的原则。

本仓库 `docs/architecture.md`、`docs/interface.md`、`docs/runbook.md`、
`docs/phone-operations.md` 及客户端/接入文档的版本或准入表述，在对应实现具备后同步。
所有改动的 `docs/` 在提交前经过独立只读审计；审计期间不修改正在审计的文件。
本提案本身不提前更新正式文档或版本常量。

## 7. 预期效果与能力边界

### 7.1 能治什么

正常返回的启动调用被任务关联误判拖入永久 execution 阻塞；聚焦/写入完成后只读检查失败
造成的同类粘住；shell 错清 UI 责任；缺少原身份的旧 execution-only 账本没有合法恢复路径。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 原 open_app 仍返回 uncertain | 本提案不放松唯一同屏任务关联，也不改判已有 P |
| 真实断连/超时缺回执，需要重启手机 | 首版没有增加设备侧 UI 持久回执与精确终止 runner |
| 升级前已重启过但缺 checkpoint，仍要求再重启一次 | 旧记录没有可验证的历史 boot identity，不能补造 |
| reboot 后仍有 recents、任务归属或 App 配对问题 | 启动边界只解决旧执行调用，不解决全部资源/配对证据 |
| 无法读取稳定设备身份/boot identity | 不能确认是同一设备的不同启动；保持 blocked |
| 旧记录丢失归属、来源不明或彼此矛盾 | 无法仅靠 checkpoint 恢复物理所有权，需要单独归因；不加入强制放行 |
| 已确认输入仍引发稍后的 App 异步行为 | 本方案界定受控调用结束，不承诺业务静止或撤回已派发命令 |

### 7.3 成本

单步 UI 操作增加一次派发前耐久写与一次最终证据耐久写；文字两步可合并相邻状态转换，
通常为三次写。实际耗时需在手机目标文件系统测量，不能用 Windows 结果推断。
持久化耗时进入既有工作预算；证据保存失败会降低可用性而不伪造动作结果。
每次会话/必要重连增加有界身份读取；晚到结束证据后可能增加一次物理复核窗口。
旧无证记录的恢复成本由一次用户安排的手机重启及 App 重新同步承担。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| accepted + 无/多个关联 task | 原 P uncertain；后观察可读；end 后物理项确认则 execution/cleanup 可完成 |
| focus 正常返回 + foreground 不明 | 任务结果 uncertain；focus 不再作为在途输入 |
| 聚焦完成 + capture/bind/GetText 失败 | 不写文字；原 P uncertain；无未决 tap 责任 |
| set 完成 + 读回失败/不匹配 | 不重复 set，不追认 outcome，不制造未知调用 |
| 真正传输失败或取消 | unknown；封闭后续普通工作，不排队，STOP/status 可用 |
| shell 与 UI 来源隔离 | shell 不得清 UI token；普通准入拒绝在未知 UI 后启动 shell；旧双来源记录分别恢复 |
| STOP 位于预写等待/实际派发/返回之后 | STOP 不等待；失权后不再派发；迟到结果无 O，无 outcome 改判 |
| 物理证据早于迟到终结 | 不凭旧三项确认放行，必须有终结边界后的只读复核 |
| 另一 P/step/epoch 的旧回执 | 不影响当前责任或 cleanup |
| 预写失败/终结写失败/崩溃窗口 | 不漏掉可能派发责任；已确认 outcome 不反转；账本不安全时不准入 |
| v1 execution-only 记录 | 第一次只建 checkpoint；同 boot 重试/重启不放行；checkpoint 不被重写 |
| v1/v2 + 同设备 boot 改变 | 只解除覆盖的执行责任；物理/配对/STOP/账本任一不满足仍拒绝 |
| 身份读取失败、设备不符、boot 格式错误 | 不当作 absent/changed；不解除责任 |
| 跨 boot 的显示/task 编号复用 | 不把旧关闭命令应用于新归属或主屏任务 |
| v2 round trip、未知版本、64 KiB 上限 | 责任与 checkpoint 无损；未知/超限拒绝；快照不可被并发修改 |

反向对照必须包含：去掉“outcome 与 receipt 分离”时原报告回归用例失败；去掉来源隔离时
shell 覆盖用例失败；去掉 checkpoint 持久化/同设备/boot 变化任一守卫时相应负例失败。
不把诊断 fixture 中仅凭物理三项的可用性期望当作任意 unknown 都能释放的测试规格。

### 8.2 实机/系统级

尚未执行；实施后分别验证：

1. 同版本服务/App/stdio 适配器与发布清单一致，固定 JAR 哈希仍匹配，不卸载/清库。
2. 使用可控故障注入让启动已 accepted 后的任务关联失败：证明原 P uncertain，后观察可读，
   收尾完成后可重新 start；每个 P 的启动次数恰好一次。
3. 真实断连/取消窗口：普通工作即时拒绝；STOP 响应不等待持久化/设备；不自动重放原输入。
4. 复用原 execution-only 账本：先确认 checkpoint 耐久保存，再由用户安排一次手机重启；
   重启后设备身份匹配、boot 改变、App 同步与资源核验完成才 `can_start=true`。
5. 在 Linux/arm64 运行目录测试写失败、进程崩溃和重启；分别核验 may_dispatch 与完成写窗口。
6. 记录正常单步/文字路径额外落盘耗时及实际 STOP 延迟；原输入/回执正文不入库。

仅对本次变更路径执行专项验证；按工程流程完成必要回归，不将模拟测试写成设备结论。

### 8.3 记录

沿用现有归因记录，实施后新增 `maintenance/verification/ui-input-execution-evidence.md`，
记录实现提交、环境、真实命令、结果与未执行原因。实现提交在前，提案归档在后；归档时补
§11、实现 hash 和正确的相对链接，不能以当前提案代替验证记录。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 用户确认的路线 | 执行事实分层，保留原动作 uncertain，设计旧账本恢复 |
| 提案选择 | R2；子步骤 receipt、来源隔离、普通工作屏障、v2 账本与设备启动 checkpoint |
| 任务成功判据 | 保持唯一同屏关联；不改等待策略，不用前台替代 |
| 旧缺证记录 | 不补造回执；checkpoint 成功后要求用户安排手机重启 |
| 升级与运维 | 不自动重启手机，不清账本/私有数据，不新增 force API；同签名覆盖升级 |
| 版本方案 | 拟共同契约 4 / 发行 0.4.3 / versionCode 6，正式修改仅走单源生成 |

### 9.2 未决

没有悬空的技术选项。用户已在实施前整体确认本文具体判据、重启恢复代价和版本方案，
并授权完成实现与实机部署；路线为 R2。

## 10. 实施顺序

1. 确认本文具体契约；先在设计源头落独立 docs-only 修订，完成对应文档审查。
2. 添加批准判据下的先红回归，保留 outcome、来源、STOP 和旧账本的反例。
3. 实现 adapter receipt 与能力/身份事实，完成包级测试；不引入执行权威。
4. 实现引擎子步骤、来源隔离、预写与普通工作屏障；加入迟到证据/物理复核测试。
5. 实现账本 v2 与启动 checkpoint 恢复；完成写失败、崩溃和版本兼容测试。
6. 更新单一版本源并生成常量；同步已具备行为的正式文档，运行必要回归。
7. 构建/清单核验/覆盖部署，按 §8.2 做真实设备验证；未执行项如实记录。
8. 独立只读审计涉及的 `docs/`，修正并复检；提交实现，提案状态仍保持未实现。
9. 实现提交存在后归档提案并补 §11 与实现 hash；单独提交归档与分层验证记录。

## 11. 实现结果（归档时补写）

尚无实现提交；本节在实现提交存在并移入 `implemented/` 后补写。

### 11.1 已落地

归档时列出提案条目与实际代码落点。

### 11.2 与提案不同或未执行

归档时逐项记录差异与未执行验证的理由；涉及判据、契约或时序的变化先重新确认。

---

## 附：本提案不改变的事项

- 引擎唯一权威、STOP 优先、O 单向消费、同 P 不重复派发。
- 主动作无完整证据保持 uncertain；后观察不为旧动作补判。
- HTTP 严格接入、Node/delta 三态与图片的一次采集事实。
- notify 正文私有存储、App 本地已读边界和日志白名单。
