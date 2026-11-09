# 提案：shell 不再作废当前观察，也不再后置观察

状态：提案，未实现。
基线提交：`742937fc97d0f60d892b8368ed30ce8cb3e8e7a2`（分支 `master`，2026-11-09）。
适用范围：`shell` 的派发边界与结束路径；依赖 [act-coordinate-point-proof.md](act-coordinate-point-proof.md) 先落地（坐标目标在 shell 之后的安全证明）。
影响范围：`internal/engine`（`shell.go` 为主、shell 用例）、`docs/interface.md`、`docs/mcp.md`（isError 表一行，可选）、`internal/mcp/schema.go`（可选描述）。
契约（`/api/*` 字段与 `contract_version`）：改变——`shell` 不再发布后置观察，成功结果不含 `observation`、`next` 为空；字段定义与 `contract_version` 不变。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，须按当时的
`HEAD` 重新核对（重点：`internal/engine/shell.go` 的派发边界、`finishShellResult`、
`finishShellConfirmed`、`finishShellNotStarted`，以及 `internal/engine/capture.go` 的
`useDelta` 条件）。

---

## 0. 摘要

`shell` 的派发边界会退休当前 O、清掉已确认基线；结束后再拍一张 full 树把 O 补回来。这个补发
不是“检查命令效果”，而是“把服务自己作废的 O 补一个”——命令根本没启动时也会补。本提案让
`shell` 不再作废当前 O、不再清基线、不再拍后置观察：`shell` 只报告命令事实，屏幕是否变化由
下一次 `act` 的实时重验回答。坐标目标的证明依赖 `act-coordinate-point-proof`；`ref` 目标的
既有判据不变。

## 1. 问题

### 1.1 现象与代价

1. 每条健康结束的 `shell` 都付一次 `ProbeDisplay` + 一次 `Capture` + 一份 full 树；树进 MCP 的
   `structuredContent`，上限 `MaxFullTreeBytes=128 KiB`。这与命令是否改屏无关：连续 N 条命令
   就是 N 份全树。
2. 逃生出口场景（树不健康时用 `shell` 修东西）里，后置观察失败会在成功的命令结果里塞
   `observation.status=failed` + `next=observe`，是噪音。
3. 为补回 O，`shell` 还要在工作位上多占一次采集，设备释放更晚。

把三条路线的账摆在一起（假设 shell 之后接一次 `act`）：

| 场景 | A 现状 | B 只删后置观察 | C 本提案 |
| --- | --- | --- | --- |
| `shell→act`，屏未变 | 2 次采集（后置 + act 重验），1 份 full 树 | 2 次采集（observe + act），1 轮往返，1 份 full 树 | **1 次采集（act 重验），0 份树** |
| `shell→act`，屏变了 | 2 次采集 | 2 次采集，1 轮往返 | 2 次采集 + 1 次废 act + 1 轮往返 |
| N 条 `shell` 链 | N 次采集 + N 份 full 树 | 0 | **0** |
| `shell→end` | 1 次采集 + 1 份树 | 0 | **0** |

C 的赌注是“命令大多不改屏”：赢了省一次采集和一份全树；输了多一次废 `act`。失败被 `act` 的
实时重验挡在派发之前，所以代价是“一轮”，不是“点错”（残余见 §7.2）。

### 1.2 证据链

1. 派发边界退休 O 并清基线：`shell.go:227-231`（`current.Status = retired`；`baseline = nil`）。
2. 结束路径补发一次强制 full：`finishShellResult` 的 `captureAndPublish(..., true, false, "shell")`
   （`shell.go:647`）。基线已被清，`useDelta` 也不会成立（`capture.go:183-184`），所以必然是 full。
3. 命令**根本没启动**时也会补：[shell.go:257](../../internal/engine/shell.go#L257) 的
   `finishShellNotStarted(..., observe=true)`；测试明确断言 “healthy scope still needs one full”
   （[shell_test.go:217](../../internal/engine/shell_test.go#L217)）。
4. `act` 要求当前 O 仍 `ready`：`act.go:250-251`。所以只删补发、保留退休，下一次 `act` 必被
   `observation_retired` 挡住。
5. full 树进 `structuredContent`：`server.go:129`（`StructuredContent: structuredContent(...)`）、
   `server.go:369-375`；上限见 [architecture.md:123](../../docs/architecture.md#L123)。
6. 代码自己的分类已经把 `shell` 归为“不是 UI 获取”：`settle.go:34` 的注释与 settle 提案都
   把 `shell` 排除在收敛采样之外；但结果里仍然带一棵 UI 树。
7. 后置观察只在“健康确认结束”时发生：`finishShellResult` 在 `workRevoked` 时跳过
   （`shell.go:632-639`），`finishShellUncertain` 不传补发（`shell.go:499`）。撤销/不确定路径
   本来就不发观察。

### 1.3 为什么直觉会出错

- **“后置观察是在检查命令有没有改屏”**：不是。它是把第 1 条证据里被自己退休的 O 补回来；
  命令没启动时也补，就是反证。
- **“删掉它就省一次采集”**：不省。`shell→act` 的那次采集会挪到 `observe` 里；它省的是
  **全树负载**和**链式 shell 的重复采集**，不是所有场景下的采集次数。
- **“不退休 O 就是信任 agent”**：不是。`act` 在派发前会实时重验；坐标路径由提案 1 补上
  “点解析节点”的证明。失败模式是被拒绝的 `act`，不是错误输入（残余见 §7.2）。
- **“shell 之后必须先 observe 才安全”**：那是路线 B。它更保守，但每次 UI 续作都多一轮往返，
  且在 `shell→act` 场景并不省采集。

## 2. 目标与非目标

**目标**

- G1 `shell` 不改变当前 O 的状态：不退休、不发布、不清基线。
- G2 `shell` 的结果只含 `shell` 事实与会话视图；成功时无 `observation`、无 `next`。
- G3 设备工作位在命令结束后立即释放，不再为后置观察多占一次采集。
- G4 STOP/收尾的“命令可能已执行”事实不变（`DispatchUnknown`/`ExecutionUnknown`）。
- G5 失效模式可判定：屏幕变了 → 下一次 `act` 的 `target_changed`；O 过期 →
  `observation_expired`；会话结束 → `session_ended`。

**非目标**

- 不改变 `ref` 目标的判据，不解决 `ref` 遮挡。
- 不改变坐标判据（那是提案 1 的范围）。
- 不改变 `observe`/`start`/`open_app`/`act`。
- 不改变 `shell` 的终止、输出、恢复与账本语义。
- 不新增请求字段。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 现状 | 派发退休 O + 清基线；结束强制 full | 每条命令一份全树 + 一次采集 | 成本固定支付；结果耦合树健康 |
| R2 只删后置观察 | 派发退休 O；结束不补发 | `shell→act` 必须 observe，多一轮；采集不省 | 链式 shell 省树，但每次 UI 续作都要多一轮 |
| R3 不再作废 O + 不补发 | 派发不动 O/基线；结束不采集 | 屏幕变了时多一次废 `act` | `ref` 遮挡残余；WebView；依赖提案 1 |
| R4 请求字段 `observe:false` | 调用方声明是否要后置观察 | 新增公开字段；两条路径都要测 | 多一条“信我”模式，引擎无法验证 |

选择 R3。它不是“在不改变任何不变量的前提下省成本”——它**有意改掉**“任意命令之后不得继续
用旧 O”这条不变量，代价用 `act` 的实时重验兜住，残余写进 §7.2。R2 在 `shell→act` 场景严格
更差；R1 是成本本身；R4 把一次可由重验兜底的判断变成请求字段，扩大公开面。

## 4. 判据

### 4.1 “shell 不改变 O”

- 派发前：不把当前 `ready` O 改成 `retired`，不清 `e.baseline`；`e.current` 仍指向该 O；只置
  `work.DispatchUnknown=true`、`session.ExecutionUnknown=true`，并把 run 置为 `dispatched`
  （`shell.go:232-237`）。
- 结束后：不调用 `captureAndPublish`，不分配新 O，不写 `result.Observation`，不设
  `next=observe`；成功时 `next` 为空。
- 例外只有两条既有路径：时钟到期（`ActionableUntil`）与会话撤销（STOP/end 会退休 O）。
  `shell` 自己不会制造第三种。

### 4.2 结果形状

| 路径 | `outcome` | `observation` | `next` |
| --- | --- | --- | --- |
| 健康确认结束（`exited`/`timeout`/`cancelled`） | `executed` | 不出现 | 空 |
| 确认未启动 | `not_executed` | 不出现 | 既有（`status`/`user` 等） |
| 已派发但结束不可确认 | `uncertain` | 不出现 | `status` |
| 被撤销后确认停止 | `executed` | 不出现 | `status` |

### 4.3 不延长的时钟

O 的 `ActionableUntil` 仍是 `min(captured_at + 60s, session.expires_at)`（`capture.go:253-260`），
不因为“刚跑完命令”而续期。命令耗时直接从剩余窗口里扣；过期就让下一次 `act` 返回
`observation_expired`。

### 4.4 与提案 1 的依赖

`shell→act` 的坐标目标在 C 下仍能安全拒绝，靠的是提案 1 的“点解析节点”证明；`ref` 目标靠
既有的身份 + 祖先链唯一匹配。**提案 1 未落地时，C 的坐标路径仍有既有的前台级缺口**，不应单独
上线（顺序见 §10）。

## 5. 算法

```text
t0 O ready，baseline B（可能为 nil）
t1 shell 派发：O 保持 ready；B 保持；DispatchUnknown/ExecutionUnknown 置位
t2 命令运行：工作位被占，不会有并发 act
t3 确认结束：不采集；结果 = shell 事实 + 会话视图；清 unknown 边界
t4 agent 选择：
   - act(O)：实时重验（ref 身份/坐标点）→ 派发，或 target_changed
   - observe(O/P)：相对 B 的 delta 或 full
   - 再来一条 shell
```

分支：

- 被撤销：`revokeSessionLocked` 退休 O（`engine.go:724-727`，既有）；`shell` 返回
  `executed_revoked`/`cancelled`，无观察。
- 不确定：无观察，受管链屏障保持关闭（既有）。
- 到期：O 按 `ActionableUntil` 过期；下一次 `act` 返回 `observation_expired`。
- 未启动：O 保持 ready；结果 `not_executed`，无观察。

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/shell.go` | 派发边界去掉 retire/清基线；`finishShellResult` 去掉 `attemptPost`、后置采集、`Observation`、`next=observe`、`post_observation_failed`；`finishShellConfirmed` 调用调整；`finishShellNotStarted` 去掉 `observe` 参数；注释同步 |
| `internal/engine/shell_test.go` | 改写 Case 1（正常结束）、Case 4（未启动）、Case 10（后置失败）；新增 keeps-observation / no-capture / act-after-shell / expired |
| `internal/engine` 其他 shell 用例 | 检查 `fake_shell_test.go`、`shell_stop_test.go`、`shell_recovery_test.go` 是否断言了 `result.Observation` |
| `internal/mcp/server_test.go` | 若已有 shell 结果形状断言，补“无 `observation`” |
| `docs/interface.md` | §1 的后观察清单、§12 的派发边界/示例/失败说明 |
| `docs/mcp.md` | §3 的 isError 行措辞（可选） |
| `internal/mcp/schema.go` | `shell` 描述加一句“命令可能改变界面；若预期会改屏，随后应 observe”（可选） |

实现细节：`finishShellResult` 仍保留 `workRevoked`/`work == nil` 时 `next=status` 的分支与 phase
选择；只删后置观察相关部分（`captureAndPublish`、`Observation`、`next=observe`、
`post_observation_failed`）。

### 6.2 配置与取值

无新增配置、无新增请求字段。

### 6.3 日志与可观测性

- 不新增事件；`shell.dispatch`/`shell.finish` 的既有字段不变；
- `post_observation_failed` 这个 phase 不再出现；
- `shell` 不再触发 `observation.*` 事件；
- 命令正文、输出、异常文本仍然不进入日志。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威 | 不变；O 状态仍只由 engine 改 |
| O 单向 | 不变；`shell` 不再制造 `ready→retired` |
| 提交条件化 | 不变；`shell` 不再提交观察 |
| 事实分层 | `executed` 不再附带观察；`act` 的拒绝是下一次独立事实 |
| 工作位 | 命令结束即释放；不再为后置观察多占一次采集 |
| STOP/收尾 | `DispatchUnknown`/`ExecutionUnknown` 不变；撤销仍退休 O |
| 恢复屏障 | 账本/受管 runner 逻辑不变 |
| 日志白名单 | 无新增字段；删除一个 phase |

### 6.5 契约与文档影响

- `shell` 成功结果不再含 `observation`，`next` 为空；请求/字段定义不变；
- `docs/interface.md` §1 删掉“shell 的后观察只读取一次树”，改成“`shell` 不发布观察”；
- §12 的“派发前撤销输入授权、清基线、置全量恢复边界；结束后默认后置一次 full”整段重写为
  “置执行未知边界、不动 O/基线；不发布观察”；
- §12 的结果示例去掉 `observation`；失败说明删掉“后观察失败…next=observe”；
- `docs/mcp.md` §3 的 isError 行可按“后树仅 `act`/`open_app`”调整措辞（可选）；
- 这是可观察行为变化：读取 `shell.observation` 的调用方必须处理“没有该字段”；
- `contract_version` 不升（§9.1）。

## 7. 预期效果与能力边界

### 7.1 能治什么

- N 条 `shell` 链不再 N 份全树；成功结果只剩命令事实与会话；
- 逃生出口场景不再因为树坏而给命令结果加失败观察；
- 设备工作位更早释放；
- 屏幕未变时，下一条 `act` 可以直接使用旧 O，由实时重验兜底；
- 需要新树时，`observe` 仍按既有基线规则返回 delta 或 full。基线只是 delta 的编码参照，描述的
  是客户端已持有的树，不是当前屏幕，所以跨过一条命令仍然成立；差异过大或不可表达时，既有的
  70% 门禁会退回 full（[architecture.md:150-156](../../docs/architecture.md#L150-L156)）。

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| `ref` 遮挡 | agent 不再拿到命令后的新树；同包浮层不改变底下节点的 identity，`ref` act 可能穿透。若实机出现可复现误点，回到本提案修订（例如 shell 后第一次 `ref` act 要求 observe） |
| WebView/canvas | 树只描述容器；提案 1 的点证明只到容器 |
| 身份巧合 | 同一点上恰好有同身份、同位置的节点 |
| O 在命令期间过期 | 不延长 `ActionableUntil`；下一次 `act` 返回 `observation_expired` |
| 显示几何代次变化但节点 `Bounds` 没变 | `act` 不比较 `Display.Generation`；这是既有缺口 |
| 客户端依赖 `shell.observation` | 行为变化；文档写明 |
| 控制视图 | `shell` 期间 O 仍显示 `ready`；`work.kind=shell` 已表达“正在执行命令” |
| 坐标路径未先落提案 1 | 只剩前台级证明（既有缺口） |

### 7.3 成本

- 省：每条健康结束的 `shell` 一次 `ProbeDisplay` + 一次 `Capture` + 一份 full 树（≤128 KiB）+
  工作位占用；
- 增：屏幕变了时，下一次 `act` 多一次废 act（一次采集 + 一轮往返）；链式 shell 无增量；
- 承担者：省的是 `shell` 调用方；多一轮由判断错的 agent 支付。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 正常结束 | 当前 O 仍是同一个、`ready`；无新 O；采集次数不变；结果无 `observation`、`next` 为空 |
| 正常结束 | `e.baseline` 保持不变 |
| 确认未启动 | O 仍 `ready`，无观察 |
| 被撤销 | O 由 revoke 退休（既有），无观察 |
| 结束不可确认 | 无观察；受管链屏障保持 |
| `shell` 后 `act`（屏未变） | 成功派发 |
| `shell` 后 `act`（屏变了） | `target_changed`、不派发 |
| `shell` 后 `act`（O 过期） | `observation_expired` |
| N 条 `shell` | 0 次采集、0 份观察 |
| 反向对照 | `keeps-ready` 用例在基线上失败（基线退休 O 并发布新 O）；实现后必须通过 |

### 8.2 实机/系统级

- 3–5 条 `shell` 链：结果无全树、响应字节与延迟下降；
- `shell` 后直接 `act`（未变屏）：成功；
- `shell input keyevent` 后 `ref` act：`target_changed`；
- `shell` 后 `observe`：delta 或 full 正常；
- 树坏时 `shell`：结果干净（无 `observation.status=failed`）；
- 指标：`shell` 响应字节、`shell` 延迟、`shell` 后第一次 `act` 的拒绝率、人工观察的误点；
  统计用宿主侧工具调用记录（agent transcript），不新增服务事件；
- 没有设备时明确写“未执行”。

### 8.3 记录

写入 `maintenance/verification/shell-keeps-observation.md`：实现提交、环境、实际命令、真实结果、
未执行项与原因。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 派发边界 | 不退休 O、不清基线；保留 `DispatchUnknown`/`ExecutionUnknown` |
| 结束 | 不采集、不发布观察 |
| 结果 | 成功时无 `observation`、`next` 为空 |
| 时钟 | 不延长 `ActionableUntil` |
| 撤销/不确定 | 路径不变 |
| 依赖 | 提案 1（act 坐标点证明）先落地 |
| 契约版本 | 不升 `contract_version`（字段定义未变；行为变化写进文档） |
| 日志 | 不新增事件；减少一个 phase |

### 9.2 未决

| 事项 | 谁定、何时 |
| --- | --- |
| `shell` 的 MCP 描述是否加“若预期会改屏，随后应 observe” | 实现者，落 `docs/`/schema 时；默认加 |
| `docs/mcp.md` §3 的 isError 行是否改措辞（“后树仅 act/open_app”） | 实现者，落 `docs/` 时；默认改，避免把 shell 说成还有后树 |

## 10. 实施顺序

1. 提案 1 实现并验证（坐标点证明）。
2. `shell` 派发边界 + 收尾路径 + 测试。
3. 文档 + 审计 + 实现提交。
4. 实机验证记录。
5. 两份提案分别归档（先实现、后归档；归档头写实现提交 hash）。

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`maintenance/verification/shell-keeps-observation.md`
（归档移动后链接为 `../../verification/shell-keeps-observation.md`；§11 在归档时补写）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- `shell` 的请求字段、终止/输出/截断/root 语义；
- `shell` 的受管执行链、STOP、恢复与账本；
- `observe`/`start`/`open_app`/`act` 的判据（提案 1 除外）；
- `ref` 目标的实时重验；
- 公开树的编码、full/delta、settle；
- `contract_version`；
- 日志白名单之外的内容。
