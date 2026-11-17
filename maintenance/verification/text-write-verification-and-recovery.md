# text 写入确认、错误详情与恢复实现验证

实现提交：`b0e54a4fc91de239b677cc163c44c2cf5db46717`（`master`，2026-11-18）。
提案：[text-write-verification-and-recovery.md](../proposals/implemented/text-write-verification-and-recovery.md)（归档时补 §11）。
基线：`70607aa901f00a23fd718602e899716b6bb32e5f`；提案与审查记录提交为 `f72e797`。
版本：`0.18.0` / `contract_version=18` / Android `versionCode=20`。
固定 uiautomator 制品：`third_party/uiautomator/u2.jar`，SHA-256
`28361193DE3D13303543C13BDC7268C22EDECABFA4E68E6FBDFC35FFFE1406A3`。
本次涉及手机实机、整组构建/覆盖部署、主机二进制重建和 MCP 远端探针；没有修改固定 JAR。
所有验证只用本仓库源码、已声明版本依赖与固定制品，没有访问其他项目。

## 1. 分诊与范围

提案按取舍类走查：现行规范要求写前 GetText、写后独立采集/重绑定/GetText 与
`accepted=true`，问题不可能由“实现走偏”解释。用户在本轮明确要求按提案实施，因此不跳过
提案；讨论已在提案 §3 的 R1–R4 路线对比中完成。

实现范围与提案一致：`act.operation.type=text` 的 ref/坐标目标、两种 `press_enter` 分支、
text 错误详情、已结束 text 的恢复投影、版本/文档同步。未修改 12 工具、请求形状、
`text_input` 五组合法状态、固定 JAR、普通 tap/long_press/scroll 语义。

## 2. 先红用例

先写合成夹具并在基线提交上运行，确认失败后再改实现。关键失败如下：

| 用例 | 基线结果 |
| --- | --- |
| `TestTextFocusedRefSkipsFocusAndAcceptsSetReadback` | `outcome=uncertain`、`text_target_uncertain`（已聚焦 ref 仍点击并依赖写后重匹配） |
| `TestTextSetNoUniqueTargetReportsUndispatchedFacts` | `outcome=uncertain`，期望 `not_executed`；无 `details` |
| `TestTextReadbackMismatchAndUnavailable` | `error.text_target_uncertain`，期望 `text_result_uncertain` 与 verify 详情 |
| `TestTextMalformedSetResultIsAnUnknownReceipt` | `text_result_uncertain` 但 `Details` 为空，无法区分畸形回执 |
| `TestTextEnterGateRequiresFreshExactValue` | `text_input.write=uncertain`，期望写已确认、ENTER 未派发 |
| `TestTextStaticRejectionCarriesUndispatchedDetails` | 静态拒绝无 `stage/reason/dispatch`，且无 422 传输状态 |
| `TestTextFinishedUncertainPlansObserveForFirstStatusAndDuplicate` | `recovery=status/P`，期望 `observe/P(retired)` |
| `TestTextFinishedUncertainLiveOverrides` | `blocking_code` 为空，实时阻塞未投影 |
| `TestTextFreezeRecordsCurrentStageAndDeadlineReason` | 冻结错误为 `control_revoked`，期望 `text_result_uncertain`/`deadline_exceeded` |
| `TestSetTextRPCRejectsIncompleteOrContradictoryResults`（android） | 矛盾组合返回 `nil` 或普通 `u2_rpc_error`，期望 `device.ErrTextResultInvalid` |
| `TestGetTextRPCRequiresCountAndValue`（android） | 缺 `candidateCount`/`value` 被当成 0/空串接受 |

红测位于 `internal/engine/text_write_evidence_test.go`、`internal/android/text_rpc_contract_test.go`、
`internal/httpapi/text_status_test.go` 和 `internal/mcp/result_parity_test.go`。

## 3. 实现落点

| 条目 | 代码落点 |
| --- | --- |
| 写入完成证据：完整终止回执、唯一候选、`issued=true`、同节点刷新值精确相等；`accepted` 不单独否决 | `internal/engine/act.go` `dispatchText`；`internal/android/text.go` `textSetResult` |
| 已聚焦 ref 的完整绑定快路径；其余目标一次必要聚焦 | `internal/engine/act.go` `dispatchText`/`focusTextRef`/`focusTextPoint` |
| 删除写前 GetText 与写后重新采集/重绑定/GetText | `internal/engine/act.go`（旧延迟循环移除） |
| ENTER 前一次只读前台核对与一次严格 GetText；至多一次 ENTER | `internal/engine/act.go` `dispatchTextEnter` |
| false 的 write 与 `KnownOutcome` 同锁提交；两种分支都保存有界 `textStage` | `internal/engine/text_stage.go` `commitTextWrite`/`initTextStageLocked` |
| dispatch 三原语事实、`currentStage` 与冻结原因 | `internal/engine/text_stage.go`、`internal/engine/ui_execution.go`、`internal/engine/retirement.go` |
| `stage/reason/dispatch/candidate_count` 与阶段内 code/HTTP | `internal/engine/act.go` `textError`/`staticTextError`/`decorateTextRevalidateError`；`internal/engine/operations.go` |
| 静态 text 拒绝的 phase-specific 422（首次结果、`status(P)`、重复 P） | `internal/protocol/responses.go` `Result.HTTPStatus`（不序列化）；`internal/engine/operations.go`、`internal/engine/engine.go`；`internal/httpapi/server.go` |
| 已结束 text 的 `observe/P(retired)` 与实时覆盖 | `internal/engine/recovery.go`、`internal/engine/text_recovery.go`、`internal/engine/status.go` |
| 严格 set 组合与 get 成员 presence | `internal/android/text.go`；`internal/device/errors.go` `ErrTextResultInvalid` |
| 日志白名单与 `engine.text.finished` | `internal/eventlog/log.go`、`internal/engine/text_recovery.go` |
| MCP 指南/描述 | `internal/mcp/schema.go`；工具目录指纹 `82da6a0e…` |
| 文档、版本与生成文件 | `docs/*`、`README.md`、`release/versions.json`、`internal/protocol/version_generated.go`、Android 生成文件 |

独立只读文档审计在最终提交前返回 **CLEAN**；审计发现并修正了版本引用、目录指纹、
text 恢复 `end` 语义、静态 422 覆盖范围和 `press_enter` 描述漂移。

## 4. 单元测试与构建

环境：Windows 构建主机；Go 1.27/jsonv2；`GOOS=linux GOARCH=arm64 CGO_ENABLED=0` 发布构建。
执行：

```text
GOEXPERIMENT=jsonv2 go test ./cmd/... ./internal/... -count=1 -timeout 900s
```

结果：全部 PASS；`internal/engine` 538.119s。定向新增用例、
`internal/android`、`internal/protocol`、`internal/mcp`、`internal/httpapi`、`internal/client`
与 `internal/store` 均通过。`go test ./...` 会尝试编译被 `.gitignore` 的
`build/investigation-text-uncertain` overlay 夹具（依赖 `-overlay` 注入 engine 包），
不属于发布包测试范围；正式测试以 `./cmd/... ./internal/...` 为准。

反向对照：红测在基线上失败、实现后转绿并保留为回归。android 用例同时验证负候选数、
唯一但未 issued、非唯一却 issued/accepted/value、缺字段/类型混用都保持未知回执，不变成
`0/false/""` 或成功。

## 5. 整组部署

执行 `scripts/deploy.ps1`（默认构建并部署 Go 服务、控制 App、手机 MCP 适配器与固定 JAR）：

```text
release=0.18.0 contract=18 android=0.18.0/20
source_commit=b0e54a4fc91de239b677cc163c44c2cf5db46717 source_dirty=False
tool_catalog=12/82da6a0ef0d577b214095d9cb22e2585812827c3ddc28ef16d453bf8ab262538
device-service sha256=81a85ad085cee06f546ea1dd10425e95909a3ded1ae329353fbc599a5bb4e162
device-mcp sha256=f6ae6ea4cd9795e2bcd9d18273d2f30f15bcb8b7ada3b9eb31205e770e291d6f
control.apk sha256=7e5c85fbda83d69f700a854005073fdb2754a899cd21078f850b1f9f91a7bf5a
u2.jar sha256=28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3
```

部署后核验：服务 `/health` release/contract 匹配；`can_start=true`；App 版本
`com.mobileuse.vnext 0.18.0 (20)`；服务、手机 MCP、`u2.jar`、`applist.dex`、`service.sh`
与配置文件逐一按清单 SHA-256 匹配；手机 HTTP MCP 在 `127.0.0.1:18882` 回答 401。
设备：Lenovo `24117RK2CC`（zorn），Android 16/API 36，已连接 `adb`。

主机二进制随后用 `scripts/build-host-tools.ps1` 重建并替换：

```text
device-mcp.exe sha256=fc8ddecb776f6aa775cccce229f0453a1e17eba3615b9c359c1ef27f17df729a
mcp-probe.exe  sha256=3d15b0aee8dcd8fa6dfd2950dd97a78eb0b591a973d40e8a93080c65c977abee
host-tools manifest: PASS（companion 清单、source_commit、契约、目录指纹一致）
```

旧 `device-mcp.exe` 仍被宿主进程占用时保留为 `build/.device-mcp.exe.previous-*` 备份；
按用户说明，依赖该进程的 Agent 由用户自行重连，本轮不重启外部宿主。

归档提交后，按仓库既有做法在归档 HEAD 重新执行 `scripts/build-release.ps1`、
`scripts/verify-manifest.ps1`、`deploy.ps1 -SkipBuild` 与 `scripts/build-host-tools.ps1`：
归档只改 `maintenance/`，重建产物的 SHA-256 与上表一致；两份清单的 `source_commit`
记录归档 HEAD，实现源码仍是 `b0e54a4`，部署与探针在最终清单上复跑通过。

## 6. 实机最小闭环

使用本仓库探针 App `com.mobileuse.probe` 的受控输入框，通过部署后的 HTTP API
（`127.0.0.1:18881`，contract 18）执行 `start → app.open → text → observe → end`。
脚本与日志位于被忽略的 `build/live_text_test.py`、`build/live_text_test.log` 等；记录不保存
token 或输入正文以外的设备材料。

| 步骤 | 实际结果 |
| --- | --- |
| `status` | `can_start=true` |
| `start` | `outcome=executed`，首个 full O |
| `app.open com.mobileuse.probe` | 成功，树中找到唯一 `role=input` + `text` 动作节点 |
| ref + `press_enter=false`，`闭环A1` | `executed`；无 `text_input`；后观察完整值 `闭环A1` |
| ref + `press_enter=false`，`😀非BMP` | `executed`；完整值 `😀非BMP`（非 BMP 精确相等） |
| ref + 默认/true，`闭环B2` | `executed`；`text_input={write:executed,enter:executed}`；ENTER 后完整值仍为 `闭环B2` |
| ref + `press_enter=false`，空串 | `executed`；后观察回到空框的 hint 显示（`输入文字…`），与初始显示一致 |
| `end` | `executed`，cleanup pending（由后续清理收敛） |

空框：固定 helper 的 set 刷新值必须为空串才能提交 `executed`；公共树把 hint 作为
EditText 文本显示，因此后观察显示 hint 而不是空串。这符合提案“set 内 value 是完成证据，
后观察不是完成证据”的分层。

真实格式化边界的受控负例：向同一单行输入框写入 `"行一\n行二"`，应用把换行改为空格，
set 刷新值为 `"行一 行二"`，与请求不等。实际返回：

```text
409 outcome=uncertain
code=text_result_uncertain
details.stage=verify
details.reason=readback_mismatch
details.candidate_count=1
details.dispatch={focus:dispatched, write:dispatched, enter:not_attempted}
text_input 不出现（press_enter=false）
后观察 value=行一 行二
```

没有第二次 SetText、没有补点按、没有发送 ENTER；调用方可据 `reason` 判断是应用格式化而非
目标丢失。该结果与提案 §7.2 的“应用格式化/截断不确认成功”一致，不作为缺陷。

## 7. MCP 远端探针

手机 HTTP MCP 适配器重启后，用重建的 `build/mcp-probe.exe -transport http-remote` 连接
主机转发 `18883`，并同时比对发布清单与主机清单：

```text
service_remote PASS contract=18 release=0.18.0
instructions PASS bytes=6914 source_match=true（discover 与兼容 initialize）
tools=12 catalog=identical catalog_manifest=两份清单 sha256=match
catalog_fingerprint 82da6a0e… tools=12
status can_start=true
start/observe/wait_for/shell/inspect/act(BACK)/end/cancel PASS
RESULT PASS
```

首次远端探针因部署未配置浏览器 Origin 白名单而在此项失败；使用与当前部署一致的空白名单
配置复跑后 PASS。该差异是部署配置，不是协议或 text 实现。

## 8. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 异步更新与立即重建控件的实机比例统计 | 本仓库探针 App 只有同步单行输入框；未新增会改变发布范围的 fixture。对应路径由合成测试覆盖（读回 null/不等/回执未知），没有写成实机结论 |
| 明确多行输入框的精确相等成功 | 探针输入框是单行；执行了单行上的换行格式化负例并如实记录，未声称多行成功 |
| 实机 STOP/超时落在 target/focus/write/verify/enter 各阶段 | 实机无法精确注入各窗口；由合成测试覆盖（含 deadline 冻结与迟到回执） |
| 稳定场景每用例重复 10 次 | 本轮只做一次最小闭环；重复稳定性统计未执行，不宣称统计证明 |
| primary 模式与其他设备/App 矩阵 | 当前部署为 virtual 模式；未扩大设备/应用矩阵 |
| 外部 MCP 宿主的 tools/list 重连 | 按用户说明由用户自行重连；本轮只验证手机 HTTP 适配器与主机二进制清单 |
| 浏览器 Origin 白名单的部署值 | 部署脚本默认空白名单；未把探针专用 Origin 写入发布配置 |

## 9. 结论与边界

已实现、已单测、已整组部署、已在连接设备上完成最小 text 闭环与格式化负例；固定 JAR
哈希未变。未把合成测试写成实机结论，也未把一次闭环写成统计稳定性证明。提案 §11 的实现
结果与归档随独立提交记录。
