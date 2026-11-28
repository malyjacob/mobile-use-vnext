# 验证记录：act 重验自动恢复、统一 409 详情与 auto_recovery（契约 36）

实现提交：`c12d28876cf663d9d527e99eaba827bb3a426815`（提案入库提交 `2e19762`）。
归档提案：[act-revalidation-auto-recovery-and-details](../proposals/implemented/act-revalidation-auto-recovery-and-details.md)。
**涉及实机**：是。手机整组覆盖部署、最小实机闭环、Windows 主机双产物替换与主机 stdio / 手机 HTTP 独立探针均执行；依赖方 MCP 宿主进程的重启与重连未执行，由用户处理。

结论分层（不得互相替代）：

| 层次 | 结论 |
| --- | --- |
| 已实现 | 提案 §2.1 的三项：统一重验详情、transient_retry / text_rebind / container_rebind、顶层 auto_recovery；落点见提案 §11.1 |
| 已单测 | 重锚安全反控、自动恢复、统一详情、反悬垂、协议/MCP/探针/日志全套；见 §2 |
| 已全量 | `internal/engine` 整包 `917.826s`（`-timeout 1000s`）通过；非 engine 全部包通过；交叉编译通过；见 §3 |
| 已实测 | 统一 `409 target_changed` + 新 O + `candidates`、`transient_retry` + `auto_recovery` 与 `status(P)` 回放；见 §5.4 |
| 磁盘替换 | 手机整组覆盖部署与 Windows 双产物替换；见 §5.2–§5.3 |
| 独立探针 | 主机 stdio 与手机 HTTP 均 `RESULT PASS`；见 §5.5 |

---

## 1. 环境与构建方式

- 主机：Windows，`go1.27.1 windows/amd64`，所有 Go 命令显式设置 `GOEXPERIMENT=jsonv2`；发布交叉编译为 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`。
- 版本源：`release/versions.json` 为 `0.40.0 / contract_version 36 / android_version_code 42`，`scripts/generate-version.ps1` 生成 4 个产物。
- 发布构建在实现提交 `c12d288` 的干净树上执行（`source_dirty=false`）。
- 受控漂移使用仓库既有验证目标 `com.mobileuse.probe/.ObservationFixtureActivity` 的 `d03`/`d04` 场景与广播更新；不使用用户个人数据。

## 2. 单元测试

### 2.1 新增/更新用例

| 区域 | 覆盖 |
| --- | --- |
| `internal/engine/act_rebind_test.go` | `text_rebind` A1 父资源锚（含父框缩放）、A2 静态描述、容器重锚 2/3 骨架与数字微变；反向：双输入旧父、禁用/折叠/未公开旧同胞、可编辑正文冒充静态锚、框外占位、重复购买按钮、遮挡、`bubble_*`/`coextensive_child`、面积/交叠不达标 |
| `internal/engine/act_recovery_integration_test.go` | `tap`/`long_press`/`text_parent`/`text_label` 重锚成功、dispatch 闸门、统一详情与双路径候选、`status(P)`/重复 P 反悬垂、STOP/工作期限/前台切换、冻结回放、重锚后已聚焦零点击 fast path |
| `internal/engine/act_revalidate_transient_test.go` | Attempt 2 + `transient_retry`、STOP/期限/前台在 150ms 等待中的反控、`recovery_attempted` |
| `internal/engine/act_delivery_test.go` 等既有文件 | 交付返回值扩展、text 重验不再 `target_unconfirmed`、统一 details 断言、旧 Tier 4 fixture 显示代次补齐 |
| `internal/protocol/auto_recovery_test.go` | 严格 wire 顺序、三策略闭集、ref 同现/同省、transient 等值、rebind 必带 refs、旧响应缺字段、反控矩阵 |
| `internal/mcp/result_manifest_test.go`、`result_text_v4_test.go`、`result_parity_test.go` | 字段清单/复合 inventory、`auto_recovery` 行位置与列序、21 条非法 fallback、三模式 parity、投影与协议判定一致性 |
| `scripts/mcp-probe/probe_test.go` 等 | 解析/拒绝/`both` 语义比对、rebind refs 必选、纯坐标 transient 允许 |
| `internal/eventlog/log_test.go` | `act.auto_recovery` 仅白名单键、无 ref/文本/树 |

### 2.2 先红证据

| 用例 | 基线/修正前真实失败 |
| --- | --- |
| 协议/MCP manifest | `protocol.Result.AutoRecovery has no result v4 projection / resultFields classification at auto_recovery` |
| 重锚安全反控 | A1 双栏仅残留另一栏、A2 正文冒充描述、容器输入值冒充静态锚、框外占位均在修正前错误得到 `Frame` |
| 滚动拒绝候选 | 修正前同窗非交互覆盖面板下，matched/missing 两路候选均错误给出 `executable=true` |
| 坐标 blocker | 修正前原 owner 仅改文案时错误输出 `blocking_refs` |
| 重锚 text fast path | 修正前已聚焦换 resource-id 的重锚输入额外产生 1 次 focus tap（1 tap/1 write），修正后 0 tap/1 write |

### 2.3 定向运行（真实结果）

| 命令（均带 `GOEXPERIMENT=jsonv2`） | 结果 |
| --- | --- |
| `go test ./internal/engine -run '^(TestActRebind|TestPlanActRebind)' -count=1 -timeout 300s` | `ok … 1.068s` |
| `go test ./internal/engine -run '^(TestActRecoveryRejectedScrollFiltersCoveredCandidates|TestActRecoveryRejectedDiagnostics|TestActRevalidateTransientSingleRetryAndStop|TestScrollOverlay|TestProposalTargetChangedNineReasonMatrix)' -count=1 -timeout 300s` | `ok … 32.788s` |
| `go test ./internal/engine -run '^(TestActRecoveryRebindDispatch|TestActRecoveryRebindRejectsAmbiguousOrBlocked|TestActRecoveryStopsAtRebindBoundary|TestTextRefNoTapStillRequiresUniqueAnchorInOriginalObservation|TestTextSubmitRefTapOnce)' -count=1 -timeout 300s` | `ok … 2.951s` |
| `go test ./internal/engine -run '^(TestActRecoveryRebindDispatch|TestTextRefNoTap|TestTextSubmitRefTapOnce|TestTextSubmitRefStillDisabledRetriesOnceThenPartial)' -count=1 -timeout 300s` | `ok … 14.314s` |
| `go test ./internal/protocol -count=1 -timeout 300s` | `ok … 0.924s` |
| `go test ./internal/mcp -count=1 -timeout 300s` | `ok … 0.659s` |
| `go test ./scripts/mcp-probe -count=1 -timeout 300s` | `ok … 14.842s` |
| `go test ./internal/client -count=1 -timeout 300s` | `ok … 0.214s` |

## 3. 全量测试与静态检查

| 命令 | 真实结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./internal/engine -count=1 -timeout 1000s` | `ok github.com/malyjacob/mobile-use-vnext/internal/engine 917.826s` |
| 非 engine 全部包 `go test -count=1 -timeout 900s` | 全部 `ok`：`cmd/device-mcp 5.720s`、`internal/android 18.998s`、`internal/client 5.605s`、`internal/device 1.957s`、`internal/eventlog 1.233s`、`internal/httpapi 4.185s`、`internal/imaging 3.408s`、`internal/mcp 5.938s`、`internal/observation 2.637s`、`internal/protocol 0.071s`、`internal/store 4.288s`、`internal/treetext 2.979s`、`scripts/mcp-probe 15.819s`；`cmd/device-service`、`scripts/toolcatalog` 无测试文件 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | exit 0 |
| `scripts/generate-version.ps1 -Check` | `version source consistent: release=0.40.0 contract=36 android_version_code=42` |
| `git diff --check` | exit 0（仅 CRLF 提示） |

## 4. 文档审计

按 `AGENTS.md` 第 15 条由独立、只读 subagent 审计本次涉及的 `docs/` 与 `README.md`。首轮返回 5 项阻断 + 7 项建议，全部采纳并修正；复检又发现 3 处新增文字一致性偏差，修正后最终复检 **AUDIT CLEAN**。审计结论覆盖版本 `0.40.0/36/42`、字段顺序、`result v4` 行位置、策略/ref 闭集、纯坐标省略、`status(P)` 无 `candidates`/`blocking_refs`、`text_input` wire 值、raw `swipe` 例外与 A1/A2 描述一致性。

## 5. 发布构建、部署、实机闭环与双端探针

### 5.1 发布构建与清单核验（干净树 `c12d288`）

| 命令 | 真实结果 |
| --- | --- |
| `scripts/build-release.ps1` | `release=0.40.0 contract=36 android=42 source_commit=c12d28876cf663d9d527e99eaba827bb3a426815 source_dirty=False`；工具目录 `13/19cb4aa389e227cda892d727a270aa118c57951d0434c282169a819c6526ebae` |
| 产物 SHA-256 | `device-service f7fa2741…`（9765024 B）、`device-mcp 74902698…`（9371808 B）、`control.apk 0c77d625…`（103965 B）、`u2.jar 28361193…`（3306229 B）、`applist.dex 63818f32…`（4316 B）、`service.sh 4b568a9a…`（3643 B） |
| `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS`，`artifacts=6`、`head=c12d288`、`source_dirty=False` |
| `scripts/build-host-tools.ps1` | `RESULT PASS`；`device-mcp bc440f3d…`（10311168 B）、`mcp-probe 240a8ef6…`（10129408 B）、`source_dirty=False`、`contract=36` |

### 5.2 Windows 主机产物替换

同一源码提交构建并替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`；构建日志提示旧 `device-mcp.exe` 仍被 1 个宿主进程占用，已保留备份 `.previous-8a343c33…`。磁盘替换与独立进程验证已完成；依赖方 MCP 宿主进程重启与重连由用户处理。

### 5.3 手机整组覆盖部署（保留 App 私有数据）

`scripts/deploy.ps1 -Manifest build/release/manifest.json -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'` 输出：

- `version match: PASS (health release=0.40.0 contract=36)`；
- `installed App: package=com.mobileuse.vnext versionName=0.40.0 versionCode=42`（`adb install -r`，未卸载/未清库）；
- `App pairing: PASS`、`recovery barrier: PASS`、`STOP availability: PASS`；
- `phone MCP HTTP adapter: PASS listen=127.0.0.1:18882 url=http://127.0.0.1:18883/mcp sha256=74902698…`；
- 所有 `deployed artifact` 逐项 `(match)`；`device-service` 在 chroot 内启动。

### 5.4 最小实机闭环（虚拟屏 `display_id=314–322`）

使用新 `0.40.0/36` HTTP 服务与 `com.mobileuse.probe` 受控场景；每步逐项执行并核对返回 JSON：

| 场景 | 真实结果 |
| --- | --- |
| 统一详情失败 + 新 O + candidates | 屏幕 `display_id=320`，目标 `n8`（`D03 item_42 independent stable anchor`，`actions=tap`）；广播 `full-overlay` 后 `act(tap,n8)` → `P9676_34` `409 target_changed`，`details={phase:"revalidate",kind:"tap",original_ref:"n8",recovery_attempted:["transient_retry"],reason:"scope_blocked",candidates:[{ref:"n21",role:"view",actions:["tap"],bounds:[45,472,1035,843],relation:"child",executable:true}]}`；同响应交付新 `O9676_31`（`status=complete`），`auto_recovery` 缺席 |
| 交付 O 反悬垂 | `status(P9676_34)` 的 `error.details` 仅 `reason/phase/kind/original_ref/recovery_attempted`，无 `candidates`/`blocking_refs`，无 `observation` |
| `transient_retry` + `auto_recovery` | 屏幕 `display_id=322`，目标 `n8`；广播 `full-overlay` 后在 150ms 窗口内撤销为 `carrier-a`；`act(tap,n8)` → `P9676_44` `outcome=executed`，`auto_recovery={strategy:"transient_retry",original_ref:"n8",recovered_ref:"n8",attempts:2}`；随后 `status(P9676_44)` 回放同一标记且无 `observation` |
| 文本重锚尝试（未命中恢复） | 屏幕 `317/318/319`，`d04` 输入 `n12`（`Synthetic input A`）；`revision`、`alternate-description`、`focus/keyboard` 三种漂移后的 `act(text, auto_submit=false)` 均直接 `executed`，未触发 `auto_recovery` |
| 会话收尾 | `end(P9676_44)` `executed`，会话结束；之后 `start` 仍可接纳（无永久封锁） |

实机边界：`text_rebind` 与 `container_rebind` 的成功路径未在真机构造，均由单测覆盖；实机恢复证据为 `transient_retry`。`blocking_refs` 在该次最终实机失败样本中为 0，未作为实机已验证项主张；其过滤与输出由单测覆盖。

### 5.5 双端独立探针（新产物）

| 探针 | 结果 |
| --- | --- |
| `build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/mcp-config.json -result-mode text` | `RESULT PASS`：13 工具 schema/只读注解、status、observe/snap/wait_for/shell/inspect、`act(key=BACK)`、`end`、取消投影全部 PASS |
| `build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode text` | `RESULT PASS`：同一套件全部 PASS（手机 HTTP MCP，`contract=36`） |

依赖方边界：本次只完成磁盘替换、手机适配器启动与独立进程探针；托管 MCP 宿主进程未重启，工具重新发现与重连由用户处理。

## 6. Issue #43

实现与归档推送后回复完整落地内容并关闭 GitHub Issue #43（`0.40.0`/契约 36、统一详情、三种恢复策略、`auto_recovery`、单测/实机/探针结果，Issue #52 已单独分诊纳入后续静态拒绝自动恢复议程）。闭环结果补记于后续维护提交。

## 7. 未执行与缺口

- `text_rebind` / `container_rebind` 实机成功路径未构造，只有单测覆盖；实机只验证了 `transient_retry`。
- 主机 MCP 宿主进程的重启与重连未执行（由用户处理）；记录只主张磁盘替换 + 独立进程探针。
- `blocking_refs` 的实机最终样本为 0，过滤正确性由单测反控覆盖。
- Issue #52（静态准入拒绝自动恢复）明确不属本次范围，留待后续独立议程。
