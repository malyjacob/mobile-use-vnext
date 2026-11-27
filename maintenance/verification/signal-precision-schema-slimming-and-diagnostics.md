# 验证记录：契约 35 稀疏树与提示路由、`target_center`、MCP 目录瘦身、`app` 拒绝/重放与 `diagnose` 过滤聚合

状态：已实现、已单测、已发布（`0.39.0` / 契约 `35` / versionCode `41`）、已实机验证（有限缺口见 §8）。
实现提交：`c60769edaddfc6b7489ec95e8d2bc60d3b59bd9a`（分支 `master`）。
基线提交：`34209ef545302036f8efc194856d0a46f54880ef`。
提案：[signal-precision-schema-slimming-and-diagnostics](../proposals/implemented/signal-precision-schema-slimming-and-diagnostics.md)。

## 1. 实现提交与范围

| 提交 | 内容 |
| --- | --- |
| `521ac07` | 提案原文入库，状态保持“提案，未实现” |
| `7e2efea` | 版本源升至 `0.39.0` / `35` / `41`，生成 Go/Java/XML 常量 |
| `132a79a`（merge `b0c656b`） | 议题 4/5：`app` 安全拒绝 `retry_parameter`、5 种变更终态 `AppView` 定格重放 |
| `d982f66`（merge `f345cb5`） | 议题 6：`diagnose` 当前会话过滤、`(phase, code)` 去重、`error_counts` 与严格编解码 |
| `625dbf2`（merge `b7da36a`） | 探针解析 `result v4` 的 `diagnosis.error_counts=` |
| `b3a0d60`（merge `fa7ae9c`） | 议题 3：MCP 13 工具目录与发现说明瘦身、预算门禁收紧 |
| `fda418e`（merge `a095a82`） | MCP `result v4` 投影双清单注册与渲染 |
| `4309a7c`（merge `2bcb905`） | 议题 1：`sparse_tree` 阈值收紧与防循环 `hint` 路由、`wait_for` 终帧 `delta` 保留 |
| `be4acf7`（merge `f1a35e7`） | 议题 2：`no_interactive_ancestor` 附客观 `target_center` |
| `3480a96` | 集成修正：恢复全局指引中的 `inspect({reference:原 act 的 O,node:诊断 ref})` 举例（独立探针指令校验要求，`tools/list` 指纹不变） |
| `c60769e`（merge `ec9a5d9`） | 正式文档同步（经独立只读审计 `AUDIT CLEAN`） |

六项议题的公开契约行为：

1. `visual=sparse_tree` 门限 `textRunes < 20 && interactive < 4`；`loading` 与 `wait_for` 终帧的 `sparse_tree` 在 `delta` 均保留；非 `wait_for` 终帧且 `tree_change` 非 `unchanged`/`detail_only` 的稀疏帧提示 `hint=wait_for`（即使带图），否则无有效图提示 `hint=snap`、有图不提示；有有效图永不 `snap`。
2. `422 unsupported_node_action (reason=no_interactive_ancestor)` 且目标有屏内裁剪几何时，`error.details.target_center: [x, y]`；仅该 reason；不承诺安全，静态拒绝不认领 `O`。
3. `close_refused_borrowed` 附 `details.retry_parameter="allow_borrowed"`；已知前台服务/播放的 `kill_refused_playback` 附 `"allow_stop_playback"`；状态未知的同类拒绝不带。
4. `status(P)` 与重复 `P` 对 `open`/`claim`/`handoff`/`close`/`kill` 重放终态定格 `AppView`；`find`/`list`、运行态、`work_deadline` 冻结与重启后无快照记录仅 `app.kind`；重放不附观察、不做设备读。
5. 无参 `diagnose()` 在存在本 Agent 未结束会话时聚焦该会话（含会话期间 `SessionOrigin==""` 的 detached 操作，`StartedAt` 为零时不回流）；操作级 `issues` 按 `(phase, code)` 去重；`error_counts` 统计完整过滤窗口、不受 8 条截断；`result v4` 在 `diagnosis.issues=` 后、`diagnosis.timing_ms=` 前输出 `diagnosis.error_counts=<JSON>`。
6. MCP 13 工具名称/顺序/Schema 非描述结构/4 个 `readOnlyHint` 不变；发现说明去重压缩。

关键落点：

| 项 | 落点 |
| --- | --- |
| 稀疏阈值与分类 | `internal/observation/visual.go:51-52` |
| `fromWaitFor`、`observationVisual`/`observationHint` | `internal/engine/capture.go:495,581,606` |
| `target_center` | `internal/engine/tap_scope.go:1729,2390` |
| `retry_parameter` | `internal/engine/app_close.go:54`、`internal/engine/app_kill.go:349` |
| `cloneAppSummary` / `finishAppOperation` | `internal/engine/app.go:189,244` |
| `operationState.App` / `operationViewLocked` | `internal/engine/engine.go:577,1295-1302` |
| 诊断过滤、去重、计数、裁剪 | `internal/engine/diagnose.go:203-275,338,386` |
| `DiagnosisView.ErrorCounts` 与严格编解码 | `internal/protocol/surface.go:1215-1218,1241`、`internal/protocol/decode_response.go:978` |
| `result v4` 双清单与渲染 | `internal/mcp/result_text.go:77,1418-1456,2109-2110` |
| 发现预算门禁 | `internal/mcp/schema_test.go:24-25,733` |
| 探针解析 | `scripts/mcp-probe/result.go:124-126`、`scripts/mcp-probe/result_v4.go:556` |

## 2. 单元测试

全部为 Windows `go1.27.1 windows/amd64`，每条命令前设置 `$env:GOEXPERIMENT='jsonv2'`。子代理只运行精确 `-run` 的定向用例；全量由主代理统一执行。

| 范围 | 命令 | 结果 |
| --- | --- | --- |
| 引擎全量（单独执行，超时 1000s） | `go test ./internal/engine -count=1 -timeout 1000s` | `ok internal/engine 901.711s`（exit 0，单次运行） |
| 非引擎全部包 | `go test <go list ./... 去掉 internal/engine> -count=1 -timeout 600s` | 全部 `ok`：`cmd/device-mcp 1.312s`、`internal/android 11.587s`、`internal/client 0.228s`、`internal/device 0.032s`、`internal/eventlog 0.038s`、`internal/httpapi 0.083s`、`internal/imaging 0.494s`、`internal/mcp 0.577s`、`internal/observation 0.802s`、`internal/protocol 0.074s`、`internal/store 1.170s`、`internal/treetext 0.044s`、`scripts/mcp-probe 15.338s`；`cmd/device-service` 与 `scripts/toolcatalog` 无测试文件 |
| 交叉构建 | `$env:GOOS='linux'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'; go build ./...` | exit 0 |

先红与反向对照证据（子代理报告，主代理抽查 diff 与测试内容）：

- 议题 1：`TestWaitForTerminalSparseFrameKeepsVisual` 等；临时删除 `fromWaitFor` 保留分支后两个子用例同时丢失 `visual`/`hint`（红），恢复后过；阈值边界 19 字/3 交互命中、20 字或 4 交互不命中、`O77` 形态不命中、`O85`/`O91`/`O132` 空树命中。
- 议题 2：临时令 `hasCenter=false` 后 5 条 `target_center` 断言与既有 `no_interactive_ancestor` 断言全红；反向覆盖 `no_safe_point`、`target_missing_action`、`ancestor_missing_action` 等不带该键。
- 议题 4/5：基线 `TestAppDuplicatePRepublishesClaimSummary`、`TestAppOpenReplaysReconciledUncertainFacts` 先红（只回 `kind`）；实现后 13 条 `TestApp*` 用例通过，含 `status(P)`/重复 `P` 深拷贝隔离、`tasks: []` vs 省略、`work_deadline` 冻结不回填、未知态 `kill_refused_playback` 不带 `retry_parameter`。
- 议题 6：基线两项先红（旧会话混入、重复问题占槽）与解码 6 个非法计数子用例先红；`uniqueOverflow` 反向对照（时间线恰好 8 条时仍须 `truncated=true`）通过；协议整包与引擎定向通过。
- 议题 3：结构基线指纹证明 13 工具名称/顺序、全部非 `description` Schema 字段与 4 个只读注解与修改前一致；`TestDiscoveryTreeExamplesDecodeAndApply`、`TestCatalogSnapshotCarriesResultV4Discovery`、`TestAppDescriptionPublishesBranchMatrix`、`TestCatalogFingerprintBaseline`、禁词与退役键门禁全部通过。
- `result v4` 投影：改前 `TestResultFieldManifest`/`TestCanonicalJSONCoversManifest` 明确报 `DiagnosisView.ErrorCounts` 未注册/未分类；改后通过。

## 3. 发现目录字节与结构

`go test ./internal/mcp -run '^TestDiscoveryByteBudget$' -count=1 -v` 实测：

```text
act discovery bytes: description=3801 schema=2472
app discovery bytes: description=2384 schema=1282
discovery bytes: instructions=10172 tools_list=18247 descriptions=10521 schemas=6642 total=28419 baseline=31000
```

- `tools_list` 由 `27,369 B` 降至 `18,247 B`（净降 `9,122 B` / `33.33%`），满足提案 `<= 20,500 B`；
- 发现总量 `28,419 B < 31,000 B`（`instructions` 包含集成修正补回的 1 行指引，仍远低于门禁）；
- 门禁常量：`toolsListByteBudget=20500`、`baselineDiscoveryBytes=31000`（`internal/mcp/schema_test.go:24-25`）；
- 工具目录指纹：`13/19cb4aa389e227cda892d727a270aa118c57951d0434c282169a819c6526ebae`，与发布清单、主机清单和双端探针一致。

## 4. 构建与双清单核验（干净树 `c60769e`）

| 命令 | 真实结果 |
| --- | --- |
| `./scripts/build-release.ps1` | `release=0.39.0 contract=35 android=41 source_commit=c60769edaddfc6b7489ec95e8d2bc60d3b59bd9a source_dirty=False`；工具目录 `13/19cb4aa3…` |
| 产物 SHA-256 | `device-service 5fd901d6… bytes=9633952`、`device-mcp 9099956b… bytes=9371808`、`control.apk b5bf3e9a… bytes=103965`、`u2.jar 28361193… bytes=3306229`（与基线相同）、`applist.dex 63818f32… bytes=4316`、`service.sh 4b568a9a… bytes=3643` |
| `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS`，`artifacts=6`、`head=c60769e`、`source_dirty=False`、`tool_catalog=13/19cb4aa3…` |
| `./scripts/build-host-tools.ps1` | `RESULT PASS`；`device-mcp 1e22374b… bytes=10300416`、`mcp-probe fe575e74… bytes=10112000`；`source_dirty=False` |
| `./scripts/build-host-tools.ps1 -VerifyOnly` | `PASS`，手机清单为 companion、同源 `c60769e`、工具目录指纹一致 |

构建脚本提示主机上仍有旧 `device-mcp.exe` 映像被占用，脚本保留旧镜像为备份后完成磁盘替换；未由本仓库管理该外部进程的重启。

## 5. 手机整组覆盖部署

`./scripts/deploy.ps1 -Manifest build/release/manifest.json -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'`：

- 全部 `deployed artifact` 逐项 `(match)`，含 `device-service`、`u2.jar`、`applist.dex`、`service.sh`、`config.json`、`device-mcp`、`mcp-config.json`、`connection.json`；
- `adb install -r` 覆盖安装，`installed App: package=com.mobileuse.vnext versionName=0.39.0 versionCode=41`（保留 App 私有数据，未卸载/清库）；
- `version match: PASS (health release=0.39.0 contract=35)`、`App pairing: PASS`、`recovery barrier: PASS`、`STOP availability: PASS`；
- `phone MCP HTTP adapter: PASS listen=127.0.0.1:18882 url=http://127.0.0.1:18883/mcp sha256=9099956b…`；
- `Deployment ready.`；手机主屏复核 `topResumedActivity=com.mobileuse.vnext/.MainActivity`，无残留虚拟屏。

## 6. 双入口独立探针（本次新产物）

均使用 `build/mcp-probe.exe` 并传 `-manifest build/release/manifest.json -manifest build/host-tools-manifest.json`：

| 入口 | 结果 |
| --- | --- |
| `-transport stdio -binary ./build/device-mcp.exe -config .state/mcp-config.json -result-mode text` | `RESULT PASS`：`instructions bytes=10172 source_match=true protocol=2026-07-28`、`tools count=13`、两份 `catalog_manifest` 指纹 `match`、`catalog_fingerprint 19cb4aa3…`、13 工具 schema、`status`、`result_error`、`start`（`tree=full`/`v5`）、`app` 查询与准入、`observe`/`snap`/`wait_for`/`shell`/`inspect`/`act(key=BACK)`/`end`/取消投影全部 PASS |
| `-transport http-remote -config .state/probe-phone-config.json -result-mode text` | `RESULT PASS`：`service_remote transport=http-remote contract=35 release=0.39.0 process_managed=false`，`auth_missing 401`/`origin_denied 403`/`origin_allowed 200`/`agent_token_rejected 401`，其余同上全部 PASS |

依赖边界：本次只完成磁盘替换与独立进程验证；宿主托管的 MCP 进程重启与重新发现 13 工具由用户处理。

## 7. 最小实机闭环（虚拟屏，契约 35 API）

脚本位于 `.state/contract35/`（私有、gitignored），均以 `X-Mobile-Use-Contract: 35` 调用。

| 场景 | 实测结果 |
| --- | --- |
| 正常页面不误报稀疏树 | `start` → `O6582_11`：`visual`/`hint`/`tree_change` 均省略（完整正常页） |
| `app open` 与 `status(P)` 定格摘要 | `app(open, com.android.settings)` → `executed`，`app.kind=open/pkg=com.android.settings/task_id=417/display_id=299/reused=False/state_preserved=False`；`status(P3502)` 原样重放上述字段、`observation=False`；同 `P` 重复请求同样字段、`observation=False` |
| 失败计数与 `issues` 去重 | 两次相同静态失败 `observation_not_found`（`P3504`/`P3505`）后无参 `diagnose({})`：`coverage=own_records`、`truncated=False`、`error_counts={"observation_not_found":2}`、`issues` 仅 1 条 `{"phase":"admission","code":"observation_not_found"}` |
| `diagnose(reference)` 不变 | `diagnose(P3504)`：`coverage=original_record`、`outcome=not_executed`、`error_counts` 省略 |
| `result v4` 计数行（手机 MCP） | `tools/call diagnose {}` 文本：`diagnosis.issues=[...]` 之后为 `diagnosis.error_counts={"control_revoked":2,"observation_not_found":2,"uncertain":8}`，之后为 `diagnosis.timing_ms=...`；`truncated=true`；同码 `(phase,code)` 去重后仍保留全量计数 |
| 稀疏首帧提示 | 冷渲染网易云音乐时一次自然命中：`app(open)` 帧 `visual=sparse_tree hint=wait_for`（非 `wait_for` 首帧按新规则提示等待） |
| `close_refused_borrowed` 与重放 | 虚拟屏 `claim` 主屏任务 2（`com.miui.home`，`ownership=borrowed`）后 `close` 默认拒绝：`outcome=not_executed`、`code=close_refused_borrowed`、`details.retry_parameter=allow_borrowed`、`app.kind=close`；`status(P4304)` 与重复 `P` 一致重放该错误详情与 `app.kind`，不带观察；随后 `handoff` 返回 `uncertain/task_move_unconfirmed`，`status` 重放定格 `app.kind=handoff/pkg/task/foreground_restore` |
| `end` 收尾 | `end` → `executed`，`cleanup.state=pending` 后按 recovery 收敛；手机主屏恢复正常 |

## 8. 未执行与缺口

- `target_center` 未在实机自然触发：在知乎首页被动文本上尝试的第一次点按被交互祖先/共位控件吸收（`executed`），后续候选 ref 已随树更新失效；该分支由规划器级单测（含裁剪中心精确值、`hasCenter=false` 省略、`text(submit_ref)` 复用、完全离屏正尺寸节点）与反向对照覆盖。**如实注明：实机未自然触发该类无交互祖先页面。**
- `kill_refused_playback`（已知前台服务/播放）未在实机构造；本轮实机只构造了 `close_refused_borrowed`。未知状态分支不附 `retry_parameter` 由单测覆盖。
- `wait_for` 终帧仍稀疏时的 `visual` 保留与 `hint=snap` 未在实机自然触发（多次冷启动尝试均在第一帧后即渲染出内容）；由引擎单测（含反向对照）覆盖。
- `diagnose` 的 `uniqueOverflow` 仅 issues 溢出分支由单测反向对照覆盖；实机 `truncated=true` 由时间线 8 条上限触发。
- 依赖方 MCP 宿主进程重启/重连未执行，由用户处理。
- 文档审计的链接/锚点核验为脚本按 GitHub slug 规则执行，不是实际渲染器验证。

## 9. 文档审计与结论

- 首次独立只读审计：2 项必须修正（`docs/interface.md` 的 `details` 白名单漏 `target_center`/`retry_parameter`；`docs/architecture.md:129` 残留 PowerShell 插值 `$('work_deadline')`/`$('pp.kind')` 与 BEL 控制字符）；其余 7 项口径通过，并与版本源及 6 项行为代码交叉核对。
- 修正后复检：`AUDIT CLEAN`（两处修正确认、字符/插值残留零命中、diff 仅这两处、其余文件统计不变、73 个相对链接与锚点通过、过程性措辞与旧版本号零命中）。
- 文档提交 `ec9a5d9` 正文注明「文档审计：独立只读 subagent 复检 AUDIT CLEAN」。

## 10. 与提案的差异

| 项 | 结论与理由 |
| --- | --- |
| 完全离屏正尺寸节点使 `hasCenter=false` 的端到端路径 | 实测公开路径不可达：`observation.displayBounds` 已将公开 `Bounds` 裁到 `capture.Display`，`collectTapPublicView` 又要求 `capture.Display` 与 `obs.Screen` 同尺寸，故 `publicNodeClippedRect` 恒非空。实现仍保留 `hasCenter` 守卫，省略分支用手工离屏公开 `Bounds` + 合法 raw topology 在规划器边界单测钉住；未扩大改动到 observation 构建不变式。 |
| `act.delivery` / `app.open` 的 full 稀疏帧由 `snap` 改为 `wait_for` | 提案 §4.1 规则 2.1 对非 `wait_for` 且 `tree_change` 为空的稀疏帧要求 `wait_for`（即使带图）；`delivery` 的 full 帧 `tree_change` 为空，故按规则输出 `wait_for`，既有测试预期同步更新。 |
| WS2 保留一处短重复 | 未授权的既有 `server_test.go` 要求 `act.reference` 字段与主描述都含“静态拒绝不认领 O”消费提示，`TestActScrollSchema` 要求分支短标签；其余多段复述已移除。同时按引擎实际行为校准了 `close` 按包名可匹配多个托管窗口的说明（历史文案漂移）。 |
| 集成修正 `3480a96` | 目录瘦身移除了全局指引中的 `inspect({reference:原 act 的 O,node:诊断 ref})` 举例，导致独立探针 `TestInstructionsCheck` 红灯；恢复该 1 行（约 137 B），`tools_list` 指纹不变、总量仍 `< 31,000 B`。 |
| 提案文件清单外的测试文件 | 议题 2 为让既有 details 封闭键表接受新键，最小登记 `internal/engine/act_target_resolution_proposal_test.go`（单键，不放宽 `candidates`/`blocking_refs` 结构断言）；议题 1 新增 `internal/engine/observation_hint_wait_for_test.go`；议题 3/6 新增 `schema_slimming_test.go`、`diagnosis_error_counts_test.go`、`diagnose_scope_test.go`、`app_summary_replay_test.go` 等。 |

## 11. 清理

全部实现分支在合入 `master` 后删除 worktree 与已合并 `impl/*` 分支；主检出保留 `master` 与私有 `.state/` 验证材料。
