# 结构化 `recovery` 入口：实现与验证记录

实现提交：`d1ca0fb4b21d2e6af5dbe364acd3f30b8dd646b5`（2026-11-10）。
提案：[structured-recovery-entry.md](../proposals/implemented/structured-recovery-entry.md)。
本记录区分**已实现**、**已单测**与**已实测**；未执行项在第 6 节逐条写明原因。

## 1. 实现范围

- `Result.next` 与 `APIError.Next`/`WithNext` 删除；`Result` 末尾新增
  `recovery{entry,reference,input_state,cause}`（`internal/protocol/responses.go`）。
- 工具结果只由 `internal/engine/recovery.go` 的 `planRecovery` 一个纯函数决定；接入层错误
  只由 `protocol.AccessRecovery` 决定，仅 `contract_mismatch` 给 `update/none`。
- `internal/engine` 的全部写入点（`act`/`open_app`/`observe`/`start`/`end`/`status`/
  `keepalive`/`apps`/`shell`/`operations`/`engine`/`control`）不再写字面量入口；
  `operationState` 记录 `Reference`、`Claimed`、`Recovery` 供重复 P 与 `status(P)` 重放。
- `internal/httpapi` 的接入层错误输出 `AccessRecovery`；MCP 文本投影改为
  `recovery.entry/reference/input_state/cause`，适配器传输失败/取消固定 `status/P`。
- `contract_version` `2`→`3`，`release_version` `0.4.1`，`android_version_code` `4`。
- 文档：`docs/interface.md`、`client.md`、`mcp.md`、`control-protocol.md`、
  `phone-operations.md`、`runbook.md`、`release.md`、`integration.md`。

## 2. 构建与部署

```text
pwsh -NoProfile -File scripts/build-release.ps1
pwsh -NoProfile -File scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
pwsh -NoProfile -File scripts/deploy.ps1 -SkipBuild
```

构建结果（真实输出摘录）：

```text
release=0.4.1 contract=3 android=4 source_commit=d1ca0fb4b21d2e6af5dbe364acd3f30b8dd646b5 source_dirty=False
tool catalog: count=12 sha256=b44f890980ff6eb38fef2b4c3015e5aa4d6fc5dbeb1ebd96fdd03f16f6c5b5c
artifact device-service sha256=19580a4991fa750eeb69b5a363ba0d74356eaa74988051c742a4473ae380a9ac bytes=8192160
artifact device-mcp sha256=a54e955b599da7121ba5b54210294ce788c44b3ab145b6cc0a4c9528303343b8 bytes=9109664
artifact control.apk sha256=c4caf3116bdbdba1b5cd43fae24031049c10cd36cede67d62b7711dd6f2dd6ff bytes=99869
```

部署结果：`PASS manifest=... source_dirty=False`；`installed App: package=com.mobileuse.vnext
versionName=0.4.1 versionCode=4`；`version match: PASS`、`App pairing: PASS`、
`recovery barrier: PASS`、`STOP availability: PASS`、手机 MCP HTTP 适配器运行。

## 3. 单元测试（已单测）

```text
GOEXPERIMENT=jsonv2 go test ./...
```

全部包通过（`internal/engine` 约 144 s；无 `-race`，原因见第 6 节）。新增
`internal/engine/recovery_entry_test.go`：

| 编号 | 用例 | 结果 |
| --- | --- | --- |
| T1 | `TestRecoveryCoversEveryWritePoint`：按事实组合枚举全部原 `next` 写入点，含同一 code 在 claim 前后的不同结论；四张码表非空；`AccessRecovery` 只认四个接入层码 | PASS |
| T2 | `TestRecoveryRetryKeepsObservation`：静态拒绝 `retry/O/effective`，换新 P 复用同一 O 成功 | PASS |
| T3 | `TestRecoveryObserveAfterClaim`：`target_changed` → `observe/P/retired` | PASS |
| T4 | `TestRecoveryPostObservationFailed`：`executed` + `cause=post_observation_failed` + `retired` | PASS |
| T5 | `TestRecoveryUncertainIsStatus`：三类不确定码 → `status/P/retired` | PASS |
| T6 | `TestRecoverySessionEndIsEnd`：会话结束 → `end/P/revoked` | PASS |
| T7 | `TestRecoveryOmittedOnSuccess`：`start`/`act`/`observe` 成功省略 | PASS |
| T8 | `TestRecoveryInvariants`：I1–I9 逐条断言（含 `retry⇒O/effective`、`observe⇒≠effective`、`end⇒revoked`、`cause⇒无 error`、同输入同输出） | PASS |
| T9 | `TestRecoveryIsNotReadByEngine`：关闭 planner 后同一路径不再产出投影（反向对照） | PASS |
| T10 | `TestResultJSONHasNoNext`：序列化无 `"next"`，`recovery` 是最后一个成员，解码回环 | PASS |
| T11 | `TestContractVersionMatchesGenerated`：`release/versions.json`、`ContractVersion=="3"`、发行号一致 | PASS |
| T12 | `TestRecoveryRejectedOnRequest`：请求体带 `recovery` 被严格接入拒绝（反向对照） | PASS |
| 补充 | `TestStatusExpiredObservationKeepsExpiredInputState`：`status(O)` 过期保持 `input_state=expired` | PASS |

审计修正前，T1 的 `executed_revoked` 用例显式传入 `revoked`，掩盖了引擎派生 `retired` 的
偏差；修正为传入引擎派生值后仍要求 `revoked`，该用例在修正前会失败。`status(O)` 的
`expired` 分支同样由新增引擎级用例覆盖。

## 4. 实机环境（已实测）

- 手机：24117RK2CC（`zorn`），Android 16（API 36），Magisk root。
- 运行时：Debian 13 chroot `/data/local/chroots/debian13`；服务目录
  `/opt/mobile-use-vnext`；主机经 `adb forward tcp:18881 tcp:18880` 访问。
- 版本：`/health` 返回 `release_version=0.4.1`、`contract_version=3`；App
  `versionName=0.4.1`、`versionCode=4`。
- 驱动：`.state/verify-recovery.ps1` 从 `.state/deploy.json` 读取令牌（不打印），只输出
  状态码与枚举字段；不采集树、正文或令牌。
- 实机验证使用最终干净构建（`source_dirty=False`，提交 `d1ca0fb`）。第 5 节两行标注为
  “前一构建”的启动失败分支观测于审计修正前的工作树构建；该构建与最终提交只差
  `executed_revoked` 的 `input_state` 强制与 `status(O)` 的 `expired` 分支，不经过这两条路径。

## 5. 实机场景与真实结果（脱敏）

| 场景 | 实际响应（枚举与布尔） | 结论 |
| --- | --- | --- |
| 成功路径 | `start` 200 `executed`、无 `recovery`；`status(O)` 200 无 `recovery`；`act(key)` 200 `executed` 无 `recovery`；`observe` 200 无 `recovery` | 成功省略 ✓ |
| 静态拒绝 | `act` 未知 ref → 409 `node_not_found`、`outcome=not_executed`、`recovery={retry,O,effective}`；随后 `status(O)` 无 `recovery`；换新 P 用同一 O 重试 → 200 `executed`、无 `recovery` | 零成本重试真实可用 ✓ |
| 已有活动会话 | `start` → 409 `session_exists`、`recovery={end,P}` | start 子表 ✓ |
| 收尾未完成 | `end` → 200，`cleanup=pending/confirmed`、`recovery={status,P,revoked,cleanup_pending}`；紧随 `status(P)` 同形；收尾完成后 `status` 无 `recovery` | 收尾优先、完成后省略 ✓ |
| 会话结束后 | `act` → 409 `session_ended`、`recovery={end,P,revoked}`；`status(O)` → `recovery={end,O,revoked}`；`keepalive(P)` → 409 `session_ended`、`recovery={end,P}` | O/P 引用形态正确 ✓ |
| 契约不匹配 | `X-Mobile-Use-Contract: 2` → 409 `contract_mismatch`、`recovery={update,none}`；契约 3 → 200；控制同步契约 2 → 409，错误体 `recovery={update,none}`，同步成功响应无 `recovery` | 接入层与控制同步 ✓ |
| 未知观察 | `observe(O999999)` → 409 `observation_not_found`、`recovery={observe,P}`（无 `input_state`） | O 不可解析时省略 ✓ |
| 后观察失败 | 并发终止 u2 后 `act(key)` → 503、`outcome=executed`、`observation.status=failed`、`observation.error=hierarchy_unavailable`、`recovery={observe,P,retired,post_observation_failed}` | 降级成功带 cause ✓ |
| start 首个观察失败（前一构建） | u2 不可用时 `start` → 503、`outcome=executed`、`session=active`、`recovery={observe,P,retired,post_observation_failed}` | start 活动分支 ✓ |
| start 显示创建失败（前一构建） | 创建期间停掉控制 App → 503、`outcome=uncertain`、`session=ended`、`error=display_create_failed`、`recovery={user,none}` | start 用户介入分支 ✓ |
| 恢复屏障期间 status | 旧账本 `cleanup.execution=unconfirmed` 时 `status` → `recovery={status,P}`（存在 `start_operation_id`）；收尾完成后无 `recovery` | status 自身恢复记录 ✓ |

## 6. 未执行 / 未覆盖（及原因）

| 项 | 原因 |
| --- | --- |
| `act` 的 `uncertain`（`status/P/retired`）实机构造 | 需要输入已派发但完成证据丢失。终止 u2 的时序在本机只能稳定命中重验前（`hierarchy_unavailable`、`not_executed`）或后观察（`post_observation_failed`、`executed`），未能稳定命中派发调用本身；单元测试 T5 覆盖三类不确定码的映射，实机已用 `start` 的 `display_create_failed/uncertain → user/none` 验证 `uncertain` 不改变“入口由工具/阶段决定”的分层 |
| `start` 首个观察失败并触发撤销（`status/P/revoked/cleanup_pending`） | 需要首观察因 `control_ui_on_virtual_display`/`display_unavailable`/`stale_screen` 失败；停 App 的时序命中了更早的 `display_create_failed`，未能在激活后、首采集前稳定制造该分支；T1 的 `start first observation revoked` 用例覆盖映射 |
| `executed_revoked`（act/open_app 成功期间撤销）实机 | 需要 STOP/end 与派发后的后观察窗口竞争；本机未构造稳定窗口。单元测试 T1（引擎派生 `retired` 仍强制 `revoked`）与 shell STOP 用例覆盖 |
| `status(O)` 的 `expired` 实机 | 需要等待或篡改输入期限；单元测试新增引擎级用例覆盖，实机只验证了 `effective`/`revoked` |
| MCP 文本投影的实机断言 | 未启动真实 MCP 宿主调用；`internal/mcp/server_test.go` 覆盖 `recovery.entry/reference/input_state/cause` 投影与传输失败兜底。部署验证只检查适配器监听与清单哈希 |
| `go test -race` | Windows 宿主无 C 编译器，`CGO_ENABLED=0` 目标不可用；与既有记录一致 |
| §8.2 的“断开服务与 App 的传输后 act → 504” | 见 `act uncertain` 行；实际终止 u2 得到的是 503/`hierarchy_unavailable` 或 503/后观察失败，二者分别验证了“认领后采集失败”和“后观察失败”两条相邻路径 |

## 7. 开发期账本处理（不属于提案行为）

部署/验证期间，设备上残留了更早开发会话的 `cleanup.execution=unconfirmed`
（`exec_reason=ui_input`）。该状态按设计不可自动证实，恢复屏障保持 `can_start=false`。为在
干净会话上验证，按既有开发处置方式停止服务、把 `recovery-ledger.json` 备份为
`recovery-ledger.json.dev-repair-<时间戳>` 后移除，再启动服务；备份留在设备状态目录，未入库。
该操作不改变提案语义，也不作为“已验证”的一部分：它只是让验证从干净会话开始。破坏性场景
（终止 u2、停控制 App）后同样重启服务并做同一备份/移除，以恢复 `can_start=true`。

## 8. 结论

- **已实现**：`next` 删除、结构化 `recovery` 单源、全部工具与两个接入层、契约 3/发行 0.4.1。
- **已单测**：T1–T12、全部既有用例迁移后通过，含两个反向对照。
- **已实测**：成功省略、静态 `retry` 与同 O 重试、会话结束 `end`、收尾 `cleanup_pending`、
  契约不匹配 `update/none`、未知观察 `observe/P`、后观察失败 `cause`、start 两个失败分支、
  status 自身恢复记录；未执行项见第 6 节。
- 文档审计：独立只读审计返回 6 项（4 项本次漂移、2 项既有），修正后复检 `AUDIT CLEAN`。
