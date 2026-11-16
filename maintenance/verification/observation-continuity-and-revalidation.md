# 观察连续性、结构差分与动作重验：实现与验证记录

- 状态：**S0–S10 完成，维护记录已定稿。代码已按提案范围全部落地；实机与宿主证据存在有限缺口，用户已于 2026-11-16 明确接受并授权归档。**
- 本次涉及实机：**是。** D01–D06 已在 contract 15 上执行。D01 来自 `bb43c19` 完整 release；D02–D05 及首轮 D06 运行于 `38f47e3` 发布链；docs-only 最终锚点 `049583d` 上重建、重部署、smoke 与 D06 复核结果另列。失败、gap 与来源限制均按实际报告保留。
- 已归档提案：[README.md](../proposals/implemented/observation-continuity-and-revalidation/README.md)、[分册 05 实施与验证计划](../proposals/implemented/observation-continuity-and-revalidation/05-implementation-and-validation-plan.md)。
- 代码基线：`6ec25c87daefbc01c99f78d968214304641d119d`（2026-11-15）。
- 提案落盘提交：`70f390edc0af794649c74ce8ccab532b60f977ad`（`docs(maintenance): 新增观察连续性、结构差分与动作重验提案`）。
- S1–S5 实现提交：`900e640fe8343f92c4e8dd82dd3df839dde20756`（`feat(observation,engine): 完成观察净化、连续性、B2差分与动作重验（S1-S5）`）。
- S8 实现提交：`bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`。
- S9 探针修复：`38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`。
- 最终 docs 澄清及发行源码锚点：`049583d328e335c56d56f153b7464ea03f1ca814`；该提交仅修改四份 `docs/`，没有源码、版本或运行时功能变化。
- 全部实现提交：`900e640fe8343f92c4e8dd82dd3df839dde20756`、`bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`、`38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`、`049583d328e335c56d56f153b7464ea03f1ca814`。
- 环境：Windows 主机，`go1.27.1`，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0`；工具目录为 `12` 个工具、`sha256=1a319cb307821d51bccfa1ed3df307c363b758973174623ee935c45bac2ffc55`；当前版本源为 `release=0.15.0 / contract=15 / android_version_code=17`（S1–S5 时为 `0.14.0 / 14 / 16`）。
- 设备为 `24117RK2CC / Android API 36`，报告 physical=`1440x3200` 覆盖 `1080x2400`，density=`600` 覆盖 `450`，会话模式为 `virtual`。

## 1. 阶段进度总览（S0–S10）

| 阶段 | 当前状态 | 说明与证据边界 |
| --- | --- | --- |
| S0 分诊/确认 | 已完成 | 提案目录（总览及分册 01–05）确认并落盘于 `70f390e`；Tier 4 保持默认关闭 |
| S1 复现与参考输入 | 已完成 | 各分册默认空叶、跨 display 上下文、三态 wire、列表保号、wrapper/unwrap、微移/焦点等合成 fixture 与测试驱动已落盘 |
| S2 净化与协议基座 | 已完成（已单测） | 净化过滤、最近公开父与跨 display 上下文组装、`Node`/`ReadNode` 严格三态、删除 `Defaults`、预算与 MCP/client/probe 同步（`V01–V06`、`V10` 候选/预览） |
| S3 连续性 | 已完成（已单测） | 公开索引与全局完整桶、五阶段匹配、集合投票、局部恢复、固定点与预算/高水位回滚（`V07–V11`、`B02`/`B04` 单元基准） |
| S4 结构差分 | 已完成（已单测） | B2 共享最终图规划与 `Node`/`ReadNode` 双应用器、Add 组装前沿、Replace 封闭传播、残余删除与严格 wire 往返（`V12–V15`、`B03` 基准） |
| S5 动作策略与实际派发 | 已完成（已单测/假设备） | Tier 1–3 层级匹配、透明层三态与 L1/L2 约束、`tapFrame` 安全载体解绑、`scrollFrame` 同容器动态重规划、`text` `exact/near/shifted` 与焦点/来源门禁、`android` 传输/RPC 边界测试与 MCP schema/stdio/httpapi/client/probe 对齐（`V16–V20`、`I04–I08`、`B05` 基准） |
| S6 联动与成本 | 已完成 | I01–I03 真实入口/18 项提交竞争门禁、I01–I08 回归；新增显示身份缺陷先红并修复；B01–B06 最终 61 个五样本子项与 6ec25c8 同驱动对照 |
| S7 文档/版本/提交前构建 | 已完成 | 0.15.0/15/17、生成 freshness、九份 docs 独立审计 CLEAN；control/probe APK、vet、linux/arm64 构建及全仓 2,847 个 pass 事件 |
| S8 实现提交 | 已完成 | `bb43c19` 落盘 S6/S7、contract 15、生成物与九份正式 docs；提案当时仍保持“提案，未实现” |
| S9 完整制品与系统/实机 | 已完成（有限验证，缺口已接受） | D01 来自 `bb43c19` 的完整 release；`38f47e3` 完成 probe 修复、完整 release、部署与 smoke，并执行 D02–D05 与首轮 D06；`049583d` 仅作四份 docs 澄清并重建、重部署、复跑 smoke/D06，D01–D05 来源不被迁移 |
| S10 验证记录/归档/发行锚点 | 已完成 | 提案/验证记录进入单独归档提交；用户于 2026-11-16 接受缺口，四份 docs 经独立复检 CLEAN，发行源码固定为 `049583d` 的独立干净 checkout |

## 2. S1–S5 已落地内容与测试落点（`900e640`）

| 范围 | 主要代码落点 | 对应测试与覆盖映射 |
| --- | --- | --- |
| 协议三态与无 `Defaults` 线格式（01/03） | `internal/protocol/node.go`、`read_tree.go`、`surface.go`、`decode_response.go` | `internal/protocol/protocol_test.go`、`decode_response_test.go`、`surface_test.go`（`V04`、`V05`） |
| 净化、跨 display 上下文与公开投影（01） | `internal/observation/build.go`、`read.go`、`tree.go`、`doc.go`；`internal/engine/inspect.go` | `internal/observation/purification_test.go`、`build_test.go`、`read_test.go`；`internal/engine/tree_budget_test.go`、`readonly_test.go`（`V01–V06`、`V10`） |
| 五阶段 ref 连续性与高水位回滚（02） | `internal/observation/match.go` | `internal/observation/continuity_pipeline_test.go`、`continuity_benchmark_test.go`（`V07–V11`） |
| B2 结构差分与双端应用器（03） | `internal/observation/diff.go`、`apply.go`、`read_diff.go` | `internal/observation/delta_test.go`、`read_test.go`、`delta_benchmark_test.go`（`V12–V15`、`B03`） |
| 动作重验、载体解绑、scroll/text 约束（04） | `internal/engine/act_revalidation_policy.go`、`act.go`、`tap_scope.go`、`scroll.go` | `internal/engine/act_revalidation_test.go`、`act_revalidation_matrix_test.go`、`act_revalidation_benchmark_test.go`、`act_tap_scope_test.go`、`scroll_revalidate_test.go`、`scroll_test.go`（`V16–V19`、`I04–I07`、`B05`） |
| Android 传输与文本 RPC 契约证据（04） | 复用既有实现 | `internal/android/input_transport_boundary_test.go`、`text_rpc_contract_test.go`（`V20`） |
| MCP/Client/HTTP/Probe 消费者（01/04/05） | `internal/mcp/schema.go`、`tree_text.go`；`scripts/mcp-probe/result.go`、`result_text.go` | `internal/httpapi/admission_test.go`、`internal/client/client_test.go`、`internal/mcp/{schema_test,stdio_test,tree_text_test,result_text_test,result_parity_test,result_manifest_test,server_test}.go`、`scripts/mcp-probe/{probe_test,result_modes_test}.go`（`V05`、`I08`） |

## 3. 已实际执行的验证（在 `900e640` 最终代码树上运行）

以下命令均在最终格式化 `internal/engine/act_revalidation_policy.go` 后、于提交 `900e640` 对应的工作树上实际执行并通过：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

go vet ./...
go test -count=1 ./...
go build ./...

$env:GOOS = 'linux'
$env:GOARCH = 'arm64'
go build ./...

./scripts/generate-version.ps1 -Check
go run ./scripts/toolcatalog
git diff --check
```

实际结果：

- `go vet ./...`：退出码 `0`。
- `go test -count=1 ./...`：退出码 `0`，全部 12 个含测试的 Go 包通过（3 个无测试文件包：`cmd/device-service`、`internal/eventlog`、`scripts/toolcatalog`）：
  - `cmd/device-mcp`：`0.095s`
  - `internal/android`：`8.730s`
  - `internal/client`：`0.200s`
  - `internal/device`：`0.028s`
  - `internal/engine`：`455.270s`
  - `internal/httpapi`：`0.072s`
  - `internal/imaging`：`0.222s`
  - `internal/mcp`：`0.495s`
  - `internal/observation`：`0.623s`
  - `internal/protocol`：`0.067s`
  - `internal/store`：`1.227s`
  - `scripts/mcp-probe`：`8.497s`
- Windows 主机构建 `go build ./...` 与交叉构建 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`：均退出码 `0`。
- `./scripts/generate-version.ps1 -Check`：`version source consistent: release=0.14.0 contract=14 android_version_code=16`，退出码 `0`。
- `go run ./scripts/toolcatalog`：`{"count":12,"sha256":"1a319cb307821d51bccfa1ed3df307c363b758973174623ee935c45bac2ffc55"}`，退出码 `0`。
- `git diff --check`：退出码 `0`。

说明（边界与分层）：

- 本阶段记录不声称任何实机 `D01–D06` 或整包清单 `PASS`。
- S4/S5 实施期间对应 subagent 曾在各自收敛阶段运行过 `BenchmarkStructuralDeltaB03`、`BenchmarkActRevalidationB05` 与局部反例突变对照；在最终 `gofmt -w internal/engine/act_revalidation_policy.go` 之后，本会话仅复跑了上述全仓 `go vet` / `go test -count=1 ./...` / 双目标 `go build`，未在最终树上重跑独立 `-bench`，将在 **S6** 与 `B01–B06` 统一基线对比一并执行与记录。

## 4. S6–S10 执行清单（阶段实绩见 §5 起）

以下清单写于 S6 开始前，保留当时计划原文；实际执行、结果与偏离以 §5 起的记录为准。当时依据为 [分册 05 §10](../proposals/implemented/observation-continuity-and-revalidation/05-implementation-and-validation-plan.md)。

1. **S6（联动与成本）**：
   - 检查并补齐跨包端到端 `I01–I03` 测试（采集→净化→匹配→`ReadDiff`→固定基线多增量→`inspect` 原 `O`，以及 Candidate/Preview/STOP/显示代次竞争撤销），联合运行 `I01–I08`。
   - 在统一基准口径下运行并记录 `B01–B06`（特别补全 `B01` 净化/投影成本与 `B06` 端到端链路字节/延迟/分配与旧基线对比）。
2. **S7（文档/版本/提交前构建）**：
   - 唯一版本源 `release/versions.json` 递增共同 `contract_version`、发行号与 `android_version_code`，运行 `./scripts/generate-version.ps1` 与 `-Check`。
   - 同步受影响 `docs/` 正式文档（契约、树格式、delta、动作重验与错误语义）。
   - 发起**独立、只读的文档审计 subagent** 审查本次全部变更 `docs/`，修正所有 `file:line` 发现项并复检至 `CLEAN`（审计期间不修改被审 `docs/`，不因未返回而反复重启）。
   - 运行 `./android-control/build.ps1`（若改 `test-target` 则另运行 `./scripts/build-probe.ps1`）、`linux/arm64` 构建与全量 Go 测试。
3. **S8（最终实现提交）**：
   - 对照分册 01–05 完成反向核对，提交最终实现（包含版本源、生成物与已审计 `docs/`），工作树保持干净；提案目录此时仍保持 `提案，未实现`。
4. **S9（完整制品与系统/实机）**：
   - 在 S8 干净 `HEAD` 上运行 `./scripts/build-release.ps1`、`./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`、`./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json`，重启实际 MCP 宿主进程。
   - 若接入实机，按 S0 约定的设备/页面范围执行 `D01–D06` 并记录真实样本；若无实机或存在缺口，经确认后逐项标明未执行原因。
5. **S10（验证记录定稿与提案归档）**：
   - 更新本验证记录，补充 S6–S9 的实际提交 hash（列出全部实现提交）、文档审计结论、基准表、清单 hash 与实机/缺口结论。
   - 将 `maintenance/proposals/observation-continuity-and-revalidation/` 整目录移至 `maintenance/proposals/implemented/observation-continuity-and-revalidation/`，更新 6 份文件头部状态与全部相对链接，补写 `§11.1`/`§11.2`；同步 `maintenance/status.md` 后单独提交归档（保持 S8 为发行锚点）。

## 5. S6 联动、反向对照与边界核查

分诊：继续用户已确认的取舍提案，不另改匹配判据、预算、70% 比例或 STOP 时序。
S6 补充联动测试、成本驱动与受控实机 fixture，并修正两处源注释漂移：
`internal/observation/doc.go` 的旧四 pass/禁止跨父迁移描述，以及
`internal/engine/identity.go:19` 的“实时重验与 settle 共用唯一身份函数”描述。
`identityOf` 仍用于严格 settle/坐标路径，ref 动作使用独立的 Tier 1–3 匹配。

### 5.1 源码守卫反向对照

在本仓库的独立 worktree `.state/s6-counterfactual`（HEAD `bd77056`）逐项执行：先在原代码上
运行对应测试（全部退出 0），只移除该行行为/守卫后再运行（全部因行为断言退出 1），最后恢复
原字节。没有修改期望值；编译失败不计为反向证据；完成后该 worktree 干净。
以下是本次实际重跑的策略反向对照，不冒称上次会话修复前的先红时间记录。

各命令均采用 `GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`、主机 GOOS/GOARCH，格式为
`go test -count=1 <package> -run <pattern>`。

| 行为与代码位置 | package / pattern | 临时移除内容 | 实际失败断言 |
| --- | --- | --- | --- |
| 值过滤 `internal/observation/build.go:298` | `./internal/observation` / `^TestEmptyLeafPurification/empty_leaves_` | `hasSemantics` 把 TextSet presence 当语义 | 32/256 空叶重新发布，public 34/258 而非 2 |
| 精确三态 `internal/observation/tree.go:82` | `./internal/observation` / `^TestCheckedWireAndStrictTriBool/strict_tri_bool_transitions_all_five_flags` | nil 与任意 bool 视为相等 | unknown→false/true 的 Equal 断言失败 |
| 后代集合投票 `internal/observation/match.go:908` | `./internal/observation` / `^TestContinuityScrollListShiftsAndAnchoredParentLocalRecovery/five_card_scroll` | 跳过 Stage 3 投票调用 | 存续 B 卡从 n6 被重编号为 n18 |
| 新增组装前沿 `internal/observation/diff.go:130` | `./internal/observation` / `^TestDeltaSurvivingNodeMovesIntoNewParentContainer$` | 禁止非空 assembly | 合法新父收养存续节点的 Diff 返回不可表达 |
| Tier 2 `internal/engine/act_revalidation_policy.go:707` | `./internal/engine` / `^TestActRevalidationTierMatrix/tier2_unique_after_tier1_none$` | Tier 循环仅运行 Tier 1 | 应 unique 的微移目标返回 none |
| 透明层 presence `internal/engine/act_revalidation_policy.go:214` | `./internal/engine` / `^TestActRevalidationTransparentWrapperCapabilityMatrix$` | 删除 TextSet 必须已知的门禁 | 缺 text presence 的 wrapper 被错误接受为透明 |

同类核查范围：净化与 source/context 索引闭包、五布尔完整节点及 set 的三态、全局完整桶与
Stage 3/4/5 时序、Node/Read 共用 B2 最终图应用、ref/坐标重验分离、tap/scroll 新鲜 frame
实际消费、text 每次 Get/Set/readback 来源绑定、STOP/预算/畸形树错误分层。
未以提高上限、取消 guard 或改变期望值换取通过。

### 5.2 S6 包级回归

实际运行 `go test -count=1 ./internal/protocol ./internal/observation ./internal/android
./internal/client ./internal/httpapi ./internal/mcp ./scripts/mcp-probe`，退出 0。
本次包耗时分别为 `0.060s / 0.627s / 8.631s / 0.220s / 0.067s / 0.471s / 8.551s`。
engine 的新增 I01–I03 与全套 I01–I08 结论在完成集成后记录；本节不把尚未返回的测试视为通过。

整合后的 engine 联动回归实际执行：

```powershell
go test -count=1 ./internal/engine -run '^(TestI0[123]|TestAct|TestScrollDynamicRevalidation|TestScrollRevalidation|TestTextFocusBinding|TestSameScrollScopeBoundaries|TestWaitFor|TestInspect)'
```

退出 0（`167.352s`）。I01/I02 的真实入口与固定基线三态链、I03 的 18 项栅栏见
`internal/engine/observation_continuity_integration_test.go`。I04 对应
`TestActRevalidationCancellationAndOrdering` 的 claim/replay 与既有 Act 准入测试；I05 对应
同一组 STOP/Probe/Capture 边界、`TestActRevalidationBudgetAndCancellationClassification` 及
`TestActRevalidationDeliveryRespectsRevocation`；I06 对应该组 text focus tap 后 STOP、
Act 主动作/后观察与交付分层用例；I07 对应 `TestScrollDynamicRevalidation`、
`TestTextFocusBinding`、`TestActRevalidationTapCarrier` 的实际参数与坐标路径；I08 对应本节
protocol/client/httpapi/MCP/probe 整包回归。最终提交前另执行完整 Go 测试，不把此选择性命令
称作全仓测试。

在补齐成本计数后，另实际运行以下九项真实并发/结果分层回归（同一 `go test -count=1
./internal/engine -run '^(...|...)$'` 精确函数集合），退出 0（`24.288s`）：
`TestConcurrentClaimHasSingleWinner`、`TestConcurrentSamePAcrossObserveAndActDoesNotDispatchTwice`、
`TestStopDuringDispatchedActKeepsUncertain`、`TestStopDuringPostCaptureDoesNotPublishOrRewriteExecuted`、
`TestStopAfterUnknownReceiptKeepsOutcomeAndClosesWindow`、`TestStopAfterExecutedActKeepsExecutionConfirmed`、
`TestScrollStopDuringDispatchKeepsUncertainFacts`、`TestScrollStopDuringSettleKeepsExecutedFacts`、
`TestInternalUncertainKeepsSessionActiveAndOrdinaryWorkUsable`。

### 5.3 I03 新增缺陷：等待候选错误绑定新显示代次

先红命令：`go test -count=1 ./internal/engine -run '^TestI03LateCandidateAndPreviewRejected$'`
（`GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`），新增测试的首次实际执行退出 1：
`preview/display_generation` 在 `observation_continuity_integration_test.go:1072` 断言
“published a late O”。Candidate 的五项门禁及 Preview 的 STOP/end/owner/expiry 门禁均通过；
I01/I02 合并重跑退出 0（`1.535s`）。

根因：`internal/engine/wait_for.go:111` 在 Preview 等待结束后重新取得 session 快照，覆盖了
读取候选时的显示身份；`internal/engine/capture.go:285` 最终比较的是该新快照与当前显示，
因此不能发现候选属于旧代次。分诊为违背现行条件化提交契约的实现缺陷，不改变提案判据。

同类核查：`captureAndPublish` 在读取/settle 之前刷新 session，`act.go:153` 和
`app.go:190` 的刷新发生在一次独立后观察采集之前，均不携带旧等待候选跨过刷新点；其采集与
最终提交使用同一个读取身份。重验失败交付则从 Act 传入原 session 指针，该指针仍可能被控制
同步修改；已把 delivery 路径加入相同 I03 栅栏，验证门禁是否绑定实际 capture 的显示身份。
扩展 I03 也覆盖 display ID 改变，不只覆盖 generation。修复与复检结果另列。

主协调者扩展到 Candidate/Preview/delivery × STOP/end/generation/display ID/App owner/租约
到期共 18 项后，先在未修复生产代码上运行同一命令，退出 1（`14.199s`）：Preview 与 delivery
各自的 generation/display ID 四项均因“published a late O”在测试 `:1084` 失败。
由此确认 delivery 的 session 指针别名是同类缺陷，未只修等待入口。

修复：WaitFor 保留读取候选时的 session 显示快照，进入最终采集前只复核撤权；
`publishTreeAttempt` 在原 session 检查外，再逐项核对 `tree.capture.Display.ID/Generation`
与当前显示身份，不允许更新后的 session 指针替旧帧背书。该检查在短锁内只比较有限标量，
不增加 I/O、树算法、重试或新状态权威。扩展 I01–I03 复检
`go test -count=1 ./internal/engine -run '^TestI0[123]'` 退出 0（`15.640s`）。

### 5.4 S6 成本

完整命令、61 个子场景的五次分布与归因见[统一基准记录 §7 主表](observation-continuity-benchmarks.md#7-最终统一重跑结果主表)。
当前锚点 `bd77056` 与基线 `6ec25c8` 在相同 `go1.27.1 windows/amd64`、jsonv2、CGO=0 环境
运行同一 SHA-256 驱动，全部 B01–B06 列表非空，预热及正式 `-benchmem -count=5` 命令退出 0。
最终重跑覆盖本次内部 candidateVisits/voteComparisons 计数，以及 B03 wrap/unwrap/既有父间
reparent；当前侧为 `bd77056` 加该候选源码与统一驱动，基线只加同一驱动。
驱动 SHA-256 `316e3a5e585090c78e0cc258092b39f22593ecfe8601a79713bf66ca304b9d0a`；
算法/驱动完整文件 hash 见基准记录。八组正式日志均 PASS/ok，共 305 条 Benchmark 行，各子项
恰好五条。前一轮 53 项结果只在附录保留追溯，不替代本表。
该次测量与最后实现中的纯算法/匹配函数相同；等待/发布门禁修复不在这些微基准路径上。

| 对照 | 当前中位 ns/op | 基线中位 ns/op | 当前/基线编码或分配 |
| --- | ---: | ---: | --- |
| B01/32 默认空叶 | 4,389 | 26,722 | public 2/34；full 240/1,173 B |
| B01/256 默认空叶 | 19,339 | 365,078 | public 2/258；full 240/7,604 B |
| B06 五布尔未知 | 206,082 | 203,253 | 时间分布重叠；B/op 252,705–252,706 / 312,603–312,604；allocs 2,100/1,844 |
| B06 4096B 文本首次构建 | 3,780 | 2,321 | B/op 2,600/1,240；allocs 41/28，固定索引成本回退保留 |
| B06 chain | 862,980 | 824,026 | 分布重叠；B/op 587,220–587,223 / 664,406–664,409 |
| B06 Build→Read→ReadDiff→JSON→ReadApply | 1,709,883 | 3,189,049 | B/op 中位 1,563,841/2,465,379；allocs 中位 10,739/25,066；full 9,989/10,033 B；delta 445/474 B |

B02 五卡存续保号 100%、F 错复用 0；B03 全部 24 个结构样本满足 70% 严格选择；20k
wrap/unwrap/reparent 分别为 `178/93/80 B`。B04 20k 纯算法样本候选访问 `399888`、
实际投票比较 `7996`、接受投票 `3998`，work `539765/5184896`，不冒称其超出交付字节预算的树可发布；B05 20k
ancestor-mismatch visits `60003`、lineage `120003`，未超候选上限且未出现全树容量逐候选预分配；
B06 预算截止 `work=1/1` 后保守分新号，完整还原与 canonical bytes 检查均通过。

### 5.5 分册 04 N1–N43 测试索引

以下均为合成算法/假设备证据，不能替代 D01–D06 实机窗口。函数均位于 `internal/engine/`；
具体运行结果见包级回归与最终全仓测试记录。

| ID | 实际测试函数（同组子用例） |
| --- | --- |
| N1 | `TestActRevalidationHierarchyAndTiers`：仅祖先几何变化 |
| N2 | `TestActRevalidationHierarchyAndTiers`、`TestActRevalidationTierMatrix`：Tier 2 微移 |
| N3 | 同上：L1 不同、L2 相同的 Tier 3 正例 |
| N4 | `TestActRevalidationTransparentWrapperCapabilityMatrix`、`TestActRevalidationHierarchyAndTiers` |
| N5 | `TestActRevalidationHierarchyAndTiers`：root-only L2 拒绝 |
| N6 | 同上及 `TestActRevalidationTierMatrix`：多候选立即拒绝 |
| N7 | 同上：文案变化不启用额外 Tier |
| N8 | `TestActRevalidationTapCarrier`：载体移动重算 |
| N9 | 同上：目标独立身份下 A→B 安全解绑的两种原点变体 |
| N10 | 同上：缺独立身份/全尺寸 blocker 拒绝 |
| N11 | 同上：新候选不抢仍有效的原点 |
| N12 | 同上：原点失效后的目标子树内改点及实际 Tap 参数 |
| N13 | 同上：坐标授权不重定向 |
| N14 | `TestScrollDynamicRevalidation`：raw from/to/duration 保持 |
| N15 | 同上及 `TestSameScrollScopeBoundaries`：轴向伸缩 |
| N16 | 同上：离轴超容差拒绝 |
| N17 | `TestScrollDynamicRevalidation`：新 blocker 后重算并核对 Swipe |
| N18 | 同上：没有足够安全行程拒绝 |
| N19 | 同上：容器歧义拒绝 |
| N20 | `TestTextFocusBinding`、`TestTextBindingModesAndSourceGates`：描述锚点 shifted |
| N21 | 同上：可观察的 class/resource/description/几何差异拒绝 |
| N22 | 同上：唯一焦点不替代锚点唯一性 |
| N23 | 同上：Enabled/Password/Focused 正向来源门禁 |
| N24 | `TestTextFocusBinding`：readback 焦点转移，单次 set、不重 tap |
| N25 | `TestActRevalidationCancellationAndOrdering`：Probe/Capture 后撤权优先 |
| N26 | 同上：focus tap 后 STOP 的 uncertain 分层 |
| N27 | `TestActTapSessionProjectorPreviewFailureIsHierarchyFailure`、`TestActTapBadPreviewProjectionIsHierarchyFailure`、`TestActTapPreviewMissingCaptureIsHierarchyFailure` |
| N28 | `TestActRevalidationBudgetAndCancellationClassification`、`TestActTapRefRejectsMalformedFreshRawTopology` |
| N29 | `TestActRevalidationCancellationAndOrdering`、`TestConcurrentClaimHasSingleWinner` |
| N30 | 同上及 `TestConcurrentSamePAcrossObserveAndActDoesNotDispatchTwice` |
| N31 | `TestActRevalidationCancellationAndOrdering`：settle 相同仍拒绝未知前台 |
| N32 | `TestActRevalidationStructureTriStateMatrix`、`TestActRevalidationTierMatrix` |
| N33 | `TestActRevalidationHierarchyAndTiers`、`TestActRevalidationTierMatrix`：反向唯一 |
| N34 | `TestTextFocusBinding`、`TestTextBindingModesAndSourceGates`：无锚输入来源差异 |
| N35 | 同上：focused 过滤之前的完整候选计数 |
| N36 | `TestTextBindingModesAndSourceGates`：同 TaskID 不提供 window 身份的能力边界 |
| N37 | `TestActRevalidationCandidateVisitBound`、`BenchmarkActRevalidationB05`：20k 候选、lineage 与分配 |
| N38 | `TestActRevalidationHierarchyAndTiers`、`TestActRevalidationBudgetAndCancellationClassification` |
| N39 | `TestActRevalidationBudgetAndCancellationClassification`：定向扫描边界取消 |
| N40 | 同上及 `TestActRevalidationCancellationAndOrdering`：返回边界撤权优先 |
| N41 | 同上：budget/malformed 与 none/ambiguous 的错误族区分 |
| N42 | `TestTextFocusBinding`：首次 Tap 使用 frame 坐标，Get/Set/readback 使用新鲜 binding |
| N43 | `TestActRevalidationTapCarrier`：共享 list 祖先不构成目标独立身份 |

## 6. S7 版本、构建与文档审计

版本只手改 `release/versions.json` 为 `0.15.0 / contract_version=15 /
android_version_code=17`，冻结基线 `source_commit=bd770565cedde2391a55ab2a49e87a84adc6edba`。
`./scripts/generate-version.ps1` 与 `./scripts/generate-version.ps1 -Check` 均退出 0；
Go/Java/XML 常量及控制 Manifest versionCode 由该脚本生成，未手工维护第二份版本。

实际执行且退出 0：

```powershell
./android-control/build.ps1
./scripts/build-probe.ps1
# GOEXPERIMENT=jsonv2 / CGO_ENABLED=0 / host windows/amd64
go vet ./...
go run ./scripts/toolcatalog
# GOOS=linux / GOARCH=arm64 / GOEXPERIMENT=jsonv2 / CGO_ENABLED=0
go build ./...
```

Android 使用脚本默认的 `D:\Android\Sdk`、`D:\Android\Jdk\17`、`android-37.0` 编译平台与
`36.0.0` build-tools（min/target API 36）；两个 APK 都通过编译、签名与 zipalign。
构建只出现通用 deprecated API 提示。probe fixture 新增 D01 的 0/32/256 开关、D02 列表与
wrapper、D03 载体/blocker/滚动作用域、D04 输入/焦点/键盘；不进入发布包。
工具目录仍为 12 项，SHA-256 `1a319cb307821d51bccfa1ed3df307c363b758973174623ee935c45bac2ffc55`。

完整 Go 测试首次命令 `go test -count=1 -json ./...` 退出 1：与 Android 构建的目录清理并行时，
Go 的包扫描遇到 `android-control/build/work/classes/com/mobileuse/vnext` 消失，产生 setup
failure（`0.017s`），没有执行任何测试。该环境竞争不是行为先红；保留在私有
`.state/s7-go-test-setup-failure.jsonl`。Android 构建完成后顺序重跑同一完整命令，其最终结果另记。

顺序重跑最终退出 0：12 个含测试包全部通过，累计 2,847 个 pass 事件（包括子用例），0 fail、
0 测试 skip；3 个无测试文件包的 package skip 不算跳过用例。包级结果为：

| 包 | 耗时秒 |
| --- | ---: |
| `cmd/device-mcp` | 6.021 |
| `internal/android` | 17.081 |
| `internal/client` | 5.343 |
| `internal/device` | 0.033 |
| `internal/engine` | 476.734 |
| `internal/httpapi` | 0.067 |
| `internal/imaging` | 0.207 |
| `internal/mcp` | 7.325 |
| `internal/observation` | 7.552 |
| `internal/protocol` | 2.233 |
| `internal/store` | 1.192 |
| `scripts/mcp-probe` | 15.870 |

无测试文件包为 `cmd/device-service`、`internal/eventlog`、`scripts/toolcatalog`。
该结果覆盖最终 S6 计数/差分驱动、I03 修复与生成的契约 15 常量；没有在 Windows/CGO=0 上
宣称 race 或 Linux 账本耐久证据。

九份变更正式文档已经交由独立只读审计者审阅，范围为 architecture/client/control-protocol/
integration/interface/mcp/phone-operations/release/runbook；审计期间冻结 `docs/`。
审计者为 `agy-cli/gemini-4-hydrogen`（xhigh），agent `01a1e090-77f7-77f3-8aea-092140f32a3d`；
审计失败、发现修正与最终结论按下列记录留痕。

首次审计最终返回明确提供方错误：`Individual quota reached`，提示配额约 `2m21s` 后重置；
没有 file:line 发现清单或 CLEAN 结论。该错误属于明确失败，不是把“尚未返回”判为卡住。
保留原审计者与文件冻结状态，等待提示的重置窗口后才续审，不切换为实现者自审、不伪造结论。

等待 145 秒后由同一审计者续审并返回四类可行动发现，其余六份文档 CLEAN；协调者逐项与
源码复核并作最小修正，未改产品实现或契约判据：

| 发现（审计时行号） | 最小修正 |
| --- | --- |
| `docs/interface.md:1193` | 删除内部方案代号 S-B，直接描述 stable 与查询条件的契约关系 |
| `docs/integration.md:281`、`:670` | PowerShell 契约头 14 改为 15 |
| `docs/control-protocol.md:172`、`:175`、`:177`–`:183` | 按 ControlState.java 的模式待命、当前无操作及七种 app 短文案对齐 |
| `docs/control-protocol.md:193` | 裸 phase 词表补 dispatch |

修正后交同一只读审计者复检；复检期间再次冻结九份文档，最终 CLEAN 结论另记。

最终复检返回：**全部 9 份 docs/*.md 均为 CLEAN**。审计者逐项核对四类修正对应的三份文件
与 ControlState.java、versions.json，合并此前未改动六份文件的 CLEAN 结论；全程只读，未由
审计者修改文件。审计期间没有中断/重启未返回的任务；仅在首次明确配额失败后等待重置窗口，
由原审计者续审。最终实现提交说明同时记录此失败原因、四类修正和 CLEAN 结论。

### 6.1 S10 最终文档审计：Ollama/Nietzsche

最终实现前，九份 docs 另交独立只读 Ollama/Nietzsche（max，agent
`01a1e1c0-eeed-7021-afec-f27386835e31`）审计。此次与 S7 的 Gemini 审计是两个独立流程：
S7 保留首次 `Individual quota reached` 后由原审计者续审并最终 CLEAN 的历史；S10 全程没有
quota 错误、中断、重启或提供方错误。

此前 `docs/architecture.md` 的 scroll 例外澄清本身已审计通过。完整九份 docs 首轮继续返回
三类必须修正项：

| 审计时位置 | 冲突 | 最小修正 |
| --- | --- | --- |
| `docs/control-protocol.md:46-56`、`docs/interface.md:590-600` | 五个 AppSync 字段标为必填，与实际允许省略不一致 | 按既有 wire 解析与省略语义统一为非必填 |
| `docs/architecture.md:511-516`、`docs/phone-operations.md:179-183` | 把 21 KiB 单条优先压缩与 `complete → unconfirmed → pending` 驱逐顺序混同 | 分开描述压缩优先级和退休池驱逐顺序 |
| `docs/control-protocol.md:214-215` | 把 service 换实例写成清除 pending STOP 的条件，顺序写反 | 明确 service 换实例不抹除既有 pending STOP 保留条件 |

主协调者逐项对照源码复核后，交原文档作者 Ramanujan 对四份 docs 作最小修正；提交
`049583d328e335c56d56f153b7464ea03f1ca814` 只改
`docs/architecture.md`、`docs/control-protocol.md`、`docs/interface.md`、
`docs/phone-operations.md`，统计为 35 insertions / 23 deletions，没有产品源码、版本号或
运行时契约改动。修正后冻结九份 docs，交同一 Ollama/Nietzsche 只读审计者复检四份修改文件，
并合并此前五份未改文件的审阅结论。最终原话为：

> 本次全部九份docs CLEAN

## 7. S8 反向核对与提交边界

分册 01–05 的当前默认范围均落地：源事实不改写、source/context 闭包及三态无 Defaults；
五阶段匹配、完整桶与确定性预算；Node/Read 共用 B2 最终图并精确还原；ref Tier 1–3、载体
独立身份门禁、新鲜 tap/scroll frame、text 来源/焦点绑定；严格消费者与实际参数测试闭合。
Tier 4 按已确认默认排除，没有增加动态文案转换。新增内部计数只供测试/基准，不成为产品日志
或执行权威。显示身份缺陷属于原条件化提交契约的实现偏差，先红归因后修复，未另造取舍。

本次完整实现提交涵盖 S6 补充、受控测试 APK fixture、版本源与生成物、九份 CLEAN 正式文档
及 S6/S7 验证留痕；不提前修改六份提案的“提案，未实现”头部、不归档、不推送。
S9 首轮正式 manifest 来自该提交的干净 HEAD。其后的 `38f47e3` 只修复 probe，最终
`049583d` 只澄清四份 docs；最终锚点仍需在独立干净 checkout 按 manifest 重新核验，不能把
提交前候选 APK、旧版本清单或归档后的主树 HEAD 充作同源发布证据。

### 7.1 S9 探针修复：空 delta 读取与失败会话收尾

已有 S8 实现提交为 `bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`。本次是两个缺陷的
实现修复，不改变产品契约、版本源或正式文档，修复只涉及
`scripts/mcp-probe/checks.go`、`probe_test.go`、`result_modes_test.go` 与
`result_text.go`；修复提交为 `38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`。

真实失败与先红证据：

1. S9 smoke 在 `wait_for` 返回 `FAIL wait_for reason=text_tree_unterminated`。
   `.state/s9-smoke-origin-aligned.log:15` 已记录本次 start 创建的
   `P=Pa32ce5fc66f004a16e03b71d`，同一白名单日志 `:22` 记录上述失败；此前 auth、catalog、
   schema、status、start、observe（含图）均为 PASS。纯合成先红用例为
   `scripts/mcp-probe/result_modes_test.go:569` 的
   `TestDecodeResultTextEmptyDeltaWithWait`；修复前实际退出 1，失败落点为
   `result_modes_test.go:590`，原因同为 `text_tree_unterminated`。
2. 探针因上述检查失败退出后，协调者核对实际 `/api/status` 仍为
   `session_exists/active`，origin 恰为上述探针新建的 P；随后由协调者用原 P 受控 `end`
   关闭。纯合成先红用例为 `scripts/mcp-probe/probe_test.go:1822` 的
   `TestRunChecksEndsOwnSessionAfterLateFailure`；修复前实际退出 1，失败落点为
   `probe_test.go:1848`，记录 `end dispatched 0 times after failure, want 1`。

等待失败归因：

- 生产引擎在 `internal/engine/capture.go:258` 选择 delta，`:268` 保留满足 70% 关系的
  增量，并由 `:705` 发布 delta；画面未变化时，零 changes 的空 delta 是现行契约允许的
  正常结果。
- 生产文本渲染器在 `internal/mcp/tree_text.go:74` 固定写为 header、空行、body、trailer；
  delta 渲染入口为 `:99`，`:182` 的空变更集合不写 body。因此空 delta 的合法布局是
  `tree v2 ... changes=0`、空行、紧随的 `end tree`，产品输出没有截断。
- 探针在 `scripts/mcp-probe/result_text.go:191` 把正文起点传给 `findBlockEnd`，但修复前
  helper 又跳过该起点，从下一行开始扫描，因而遗漏空 body 的第一行 `end tree`。根因在
  外部探针解析边界，不在生产 MCP 文本格式。

同类核查：

- `decodeInspectionBlock` 在 `scripts/mcp-probe/result_text.go:258` 使用同一 helper；空的
  query inspection 同样可以合法输出零 matches、零 body 和紧随的 `end inspect`。新增
  `scripts/mcp-probe/result_modes_test.go:600` 的 `TestDecodeResultTextEmptyInspection`
  覆盖该路径。
- 独立 `resultBlockText` 从 header 后一行扫描，shell/timeline 解码器各自从正文首行扫描，
  均不存在本次偏移。
- 修复只把 helper 的首个扫描位置改为调用方给出的正文起点，没有放宽完整性：新增反向对照
  `result_modes_test.go:632` 的 `empty delta missing tree tail`，确认缺少 `end tree` 时仍
  返回 `text_tree_unterminated`，不会吞掉截断树。

失败会话收尾归因与修复：

- 原 `runChecks` 在 start 成功后存在多条提前失败返回，只在所有 smoke 动作成功后才调用
  `endCheck`；外层 transport close 不结束 remote 服务会话，因此本次 parser 误报暴露出
  既存的失败路径收尾缺口。
- `scripts/mcp-probe/checks.go:96` 在 `startCheck` 返回有效 P 后安装清理 defer；
  `:942` 的 `cleanupOwnedSession` 只接受该已确认的 start P，以脱离原取消信号的 5 秒期限
  做一次有界 `end`，不猜测或清理未知会话。
- 正常路径在 `checks.go:133` 关闭 defer 后再执行唯一一次 `endCheck`；正常 end 即使响应
  校验失败也不重放同一 P。修复后的合成测试要求 start 一次、失败后 end 恰好一次，并确认
  该会话进入 `ended`。

修复与相关测试实际命令及结果：

```powershell
$env:GOEXPERIMENT='jsonv2'
$env:CGO_ENABLED='0'

go test ./scripts/mcp-probe -count=1
go test ./internal/mcp -count=1
go test ./internal/engine -run 'TestWaitForPureSleepPublishesTimeoutFrame|TestWaitForPReferenceForcesFullTree' -count=1
```

- `./scripts/mcp-probe`：退出 0，`ok`（`7.970s`）；包含两个先红用例、空 query 同类用例、
  截断树反向控制和完整 probe 包回归。
- `./internal/mcp`：退出 0，`ok`（`0.427s`）；核对现行文本投影实现。
- `./internal/engine`：退出 0，`ok`（`4.141s`）；两个指定 fake-device wait 用例通过。

本次没有产品契约变化、源码 `docs/` 变更或版本变更。S7 的九份 `docs/*.md` 独立只读审计
结论继续对该时点有效；`38f47e3` 本身未触发新的 docs 审计。后续 docs-only `049583d`
触发 §6.1 的独立审计，并由同一审计者复检至九份 CLEAN。本节只记录 probe 先红、修复及相关
包回归；manifest、部署、smoke、D01–D06 的系统与实机结论见 §8。

## 8. S9 完整制品、部署、smoke 与 D01–D06

### 8.1 `38f47e3` 发布链与实机边界

`38f47e38fd85ffa8b79e8b9dbea13a4c00babe14` 修复探针空 delta 解析及失败后自身会话收尾，
随后在该干净 HEAD 上生成完整 release（未使用 `-SkipAndroid`），manifest 和 verifier 均记录
`release=0.15.0 / contract=15 / android=0.15.0(17) / source_dirty=false`，六制品齐全。
部署使用 `adb install -r` 覆盖安装并保留 App 私有数据；未卸载、未清库、未重配配对。最终
`s9-final-smoke.log` 为 `RESULT PASS`，auth/Origin/token、connect、12 工具/schema、status、
start、observe 图片、wait_for、shell、timeline、inspect、BACK、执行后观察与 end 均通过。

在这条失败到修复的链路上，第一次默认 deploy 启动的 Origin 白名单与 probe 使用值不一致，
请求返回 `403`；随后以 `-McpAllowedOrigins @('http://127.0.0.1:3000')` 重部署，使服务端
白名单与既有 probe 配置一致。继续执行时 smoke 暴露空 delta 的文本解析错误。
`38f47e3` 修复该解析与失败后会话收尾，重跑得到上述
final smoke PASS；不能把此前那次 `403` 写成最终 smoke 失败，也不能把修复前的 parser 错误
留作未解释的已知失败。

Windows 宿主侧，协调者精确停止了旧 MCP 适配器 PID `6684`、`10504`，随后观察到
`0.15.0` 进程 PID `6180`。这只登记实际观察到的进程序列；真实外部宿主刷新与重新发现
仍由用户自行处理，未独立确认，不能仅凭 PID 变化写成宿主刷新验证完成。

D01 的 contract 15 结果来自 `bb43c19` 的完整 release：control APK SHA-256
`8536b366bd1ce9817e5b66b70d592dc3ac345ef90dddb4eccbc1324019c407c3`。旧 contract 14 与
新 contract 15 共用同一 D01 fixture APK，SHA-256
`35a67ff897912d8f51f0e3f3c51eb69eef53dffecb6209863f9410a2072849b6`。

- 旧 contract 14 最终执行 5 次：4 pass、1 fail；iteration 4 为
  `shell_result_unconfirmed / outcome=uncertain`，随后原会话受控 end 到 `ended`，报告总结果
  仍为 fail。
- 新 contract 15 执行 3 次且全部 pass，但报告总结果为 partial，因为
  `d01_host_disk_log_proof=external_host_evidence_required` 仍缺独立宿主落盘证明。

下表旧值为 4 个成功样本的中位，新值为 3 个成功样本的中位；失败轮次不计入中位。

| 输入 | 侧 | compact 空叶 | public nodes | compact tree B | detailed tree B | inspect text B | observe text B |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | contract 14 | 0 | 9 | 815 | 957 | 851 | 1,218 |
| 0 | contract 15 | 0 | 9 | 771 | 913 | 811 | 1,178 |
| 32 | contract 14 | 32 | 42 | 1,755 | 2,776 | 2,063 | 1,716 |
| 32 | contract 15 | 0 | 9 | 774 | 916 | 814 | 1,181 |
| 256 | contract 14 | 256 | 266 | 8,240 | 15,209 | 10,466 | 5,291 |
| 256 | contract 15 | 0 | 9 | 776 | 918 | 816 | 1,183 |

三轮 contract 15 均保留 semantic sibling、text 与可 tap 的动作语义，并完成
compact/detailed 与 text 测量。

D02 `s9-closeout-d02.json` 为 3 个 case，均 `gap`、无 fail，总结果 partial；三轮唯一覆盖
缺口均为 `native_scroll_unavailable`，且 `initial_scroll_skipped=true`。每轮 21/21 check
为 true：首次 confirmed full 后，3 个 delta 均相对同一固定 full，3/3 与 inspect 一致，
1/1 full 与 inspect 一致，`ReadApply` 恢复结果逐字段等于当前 inspect。三轮 delta/full
比例均为 `0.1071 / 0.1530 / 0.1071`，中位 `0.1071`、最大 `0.1530`。旧 O 的 tree bytes
前后均为 `2,530`、inspect text bytes 均为 `1,925`；B/C/D/E 的 card/title/body 各保留
4 个稳定 ref，A 移除，F 出现时使用新 ref。该结果不写成 D02 整体 PASS，也不声称原生列表
滚动已验证。

D03 `s9-closeout-rerun-d03.json` 共 27 个 case：24 pass、3 fail，原报告
`result=fail`。唯一失败为 `d03_scroll_full_blocker_reject` 三轮，三轮均记录 failed checks
`no_blocker_tap / outcome_not_executed / no_effect_on_rejection`，actual
`outcome=executed`、未记录错误码、`effect=not_observed`，fixture blocker tap 计数各 1。
固定 fixture 把全尺寸可点击 blocker 作为所选 `ScrollView` 的同级兄弟节点加入同一
`FrameLayout`；blocker 没有滚动祖先。现行契约明确“没有滚动祖先的候选不阻塞”，生产路径
也按此执行，因此该项归类为 fixture 未覆盖现行受控滚动安全范围，原报告仍保留 fail。
滚动中心线被容器作用域内完整阻断的 in-scope 负例仍未覆盖；顶层另保留
`d03_primitive_count=service_internal_primitive_count_unavailable` gap。fixture 的 blocker
点击监听计数不是服务内部 primitive 精确计数。

D04 `s9-closeout-rerun-d04.json` 共 18 个 case：0 pass、3 fail、15 gap，顶层 17 个
coverage gap。3 个 `d04_text_focus_readback` 的唯一 false check 为
`focus_window_observed`；三轮均为 `focus_before=b / keyboard_before=hidden`、
`focus_after=a / keyboard_after=hidden`，A 值读回精确、B 未变、
`edits_a=1 / edits_b=0`，source 前后 stable、revision 未变，其余 readback、来源、其他字段
与 cleanup checks 均为 true。失败原因是 fixture 没有构造出键盘顶起窗口，不是 A/B 绑定或
读回错误。其余 15 个 source 变体输入与 duplicate binding 断言通过，但
`focus_shift_window_unproven`、全局 `d04_focus_shift_race` 与
`d04_primitive_count=text_watcher_callback_count_is_not_primitive_count` 仍为 gap。

D05 最新 `s9-closeout-rerun-d05.json` 为 1 pass、1 fail，
`result=admission_refused`：第一轮全部通过；第二轮直接发起新 `start` 时以
`display_create_failed` 中止，原会话 end confirmed。不能写成三轮通过。旧
`s9-closeout-d05.json` 保持原样：0 pass、3 fail，报告结果仍为 fail。每轮 27 checks 中
唯一 false 为 `wait_p_outcome_not_executed`；实际撤权错误为 `control_revoked`，其余 26 项
均 true：`busyP=waitP` 且 busy 返回 `device_busy`，wait 与其 status 均为
`control_revoked`，原 P finished、原 O retired、无迟到观察，新 P/O 与旧 P/O、wait P 均
不同，`can_start=true`，新会话 start/initial full/end confirmed。旧 fail 来自 driver 对
只读 wait 强求 `outcome=not_executed` 的判据错误，但原报告不改写。最新与旧报告仍保留
capture/preview/post-dispatch STOP 注入不可用的覆盖缺口。

首轮 D06 在 `38f47e3` 上为 3/3 case pass，总结果因
`external_s9_probe_required` 为 partial。每轮只执行 full→1 个 delta→`act key=BACK`→
inspect→end，`delta_checks=1`、`later_deltas_checked=false`；该证据保留，不因后续锚点
重跑而改写。

### 8.2 `049583d` 最终发行锚点

`049583d328e335c56d56f153b7464ea03f1ca814` 在 `38f47e3` 之后只修改
`architecture.md`、`control-protocol.md`、`interface.md`、`phone-operations.md` 四份
docs，统计为 35 insertions / 23 deletions。Go 服务、MCP、observation/engine 与固定制品源码
没有功能变化；该提交是最终实现与发行来源，但归档提交不是发行源。

在 `049583d` 的干净 HEAD 上运行完整 `./scripts/build-release.ps1`，未使用
`-SkipAndroid`；`./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`
返回：

```text
PASS manifest=build/release/manifest.json release=0.15.0 contract=15 android=0.15.0/17
artifacts=6 tool_catalog=12/1a319cb307821d51bccfa1ed3df307c363b758973174623ee935c45bac2ffc55
source_commit=049583d328e335c56d56f153b7464ea03f1ca814 source_dirty=False
head=049583d328e335c56d56f153b7464ea03f1ca814
```

manifest 的 `built_at` 为 `2026-11-16T00:58:51Z`，没有可选 `complete` 字段；准确结论是
“六制品齐全且现行 verifier PASS”，不补造 `complete=true`。

| 制品 | 字节 | SHA-256 |
| --- | ---: | --- |
| `device-service` | 8,978,592 | `a04bb99b603dc482a0236339b4eb47f82505d85a283a2c7f9435c5196421f097` |
| `device-mcp` | 9,175,200 | `c1f9eca808ea09b0a0748abdd57c31916da787dc469ef7508d40bf84703312f4` |
| `control.apk` | 103,965 | `18a6e7ce612bd5ec1d70b9f78910169762dfb7ce09fbf4fb6c216da6362f6235` |
| `u2.jar` | 3,306,229 | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` |
| `applist.dex` | 4,316 | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` |
| `service.sh` | 3,643 | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` |

相较 `38f47e3`，Android 源码与版本没有变化；重新构建的 `control.apk` SHA-256 为
`18a6e7ce...`，其余五个制品 hash 与 `38` 完全一致。未分析该 APK 字节差的来源。该对应
关系不替代在 `38` 上取得的 D01–D05 原始报告，也不把它们改写成 `049` 实测。

Host tools manifest 同样记录 `source_commit=049583d... / source_dirty=false /
contract=15`，`./scripts/build-host-tools.ps1 -VerifyOnly` PASS、2 个制品：

| 制品 | 字节 | SHA-256 |
| --- | ---: | --- |
| `device-mcp.exe` | 10,096,128 | `828b4bb0773c4c44c696890f2ab8cca1bbbdf3a3c312dbe4a81952816b9cf056` |
| `mcp-probe.exe` | 9,704,960 | `b5a8d800c8ac7f5c4599b855ffeae8d84af5b9e1aef53a76d7d66293be37a0ea` |

`s9-anchor-deploy.log` 的 manifest 与 HEAD 均为 `049583d`、`source_dirty=False`。控制 App
以 `adb install -r` 覆盖安装并保留私有数据，安装身份为
`com.mobileuse.vnext / 0.15.0 / versionCode 17`。设备服务、控制 App 与手机 HTTP MCP
adapter 均启动；version、App 配对、当前准入/恢复屏障、STOP availability 与运行中
`device-mcp` 二进制 hash 均 PASS。手机 adapter 为
`127.0.0.1:18882`、入口 `http://127.0.0.1:18883/mcp`，工具目录仍为 12 项且指纹不变。

新锚点首次 `s9-anchor-smoke.log` 中，auth/Origin/token、connect、工具、
catalog/fingerprint、schema、status 与 result_error 已通过，随后首次 `start` 返回
`FAIL start reason=tool_is_error code=control_revoked`。独立只读 status 当时显示
`can_start=true`、旧会话 `ended (reason=service_restarted)` 且 cleanup state 为
`unconfirmed`；不把旧会话的 `service_restarted` reason 武断认定为本次撤权根因。
`s9-anchor-smoke-rerun.log` 随后完整 PASS：start、image observe、wait_for、shell、
timeline、inspect、BACK、后观察、end 与 cancel 均通过；end 为
`executed/session=ended/cleanup.state=pending`，cancel 返回 `context_error`，会话仍 ended
且 `can_start=true`。

新锚点 `s9-anchor-d06.json` 为 3/3 case pass，总结果仍是 partial，唯一 gap 为
`external_s9_probe_required`。每轮执行 full→对固定 full 的首个 delta→BACK→inspect→end；
`delta_checks=1`、`later_deltas_checked=false`、`delta_base_valid=true`、
`delta_base_unchanged=true`。每轮 full tree 为 `2,220 B`、full text 为
`2,096/2,098 B`、delta 为 `238/919 B`；BACK 为 executed 且后观察 complete，inspect 指向
当前 O，end 为 executed/ended。iteration 2/3 cleanup complete 分别为 `2,602 ms`、
`2,513 ms`；iteration 1 的 `fixture_settle.attempted=false`，但完整 smoke 仍通过。实际外部 MCP 宿主
刷新由用户自行处理，未独立确认工具重新发现；该 gap 已接受，D06 不扩大为多 delta、滚动或
STOP 注入矩阵。

同仓库 `.state/release-source-049583d` 保存固定 `049583d` 干净 checkout、六产物与 manifest；
在该 checkout 重跑 `verify-manifest.ps1` 得到 `.state/s9-anchor-preserved-verify.log` PASS，
HEAD 与 manifest source 均为 `049583d`。其 host 2 exe 与 manifest 也经
`build-host-tools.ps1 -VerifyOnly`，由 `.state/s9-anchor-preserved-host-verify.log` 返回 PASS。
S10 归档后的主工作树 HEAD 不是发行源；后续复核必须在固定 checkout 执行，不得在新 HEAD
复用旧 manifest 冒充同源 PASS。

## 9. S10 归档、覆盖分层与发行锚点

2026-11-16，用户明确选择“接受上述缺口，继续归档”。S0–S10 的代码范围全部落地，因此提案
实现状态为“已实现”；D01–D06 的宿主证据、原生滚动、fixture/注入和外部宿主刷新缺口属于
“实机验证有限”，不改写为代码部分实现。

事实分层如下：

- 已实现：净化与严格三态、五阶段连续性、结构差分/双应用器、动作层级重验、载体/scroll/text
  安全绑定、消费者同步、条件化提交与预算计数。
- 已单测/假设备：V01–V20、I01–I08、N1–N43 映射及反向对照；显示代次迟到候选缺陷先红修复。
- 主机合成基准：B01–B06 最终 61 个五样本子项、305 行，见
  [统一基准记录](observation-continuity-benchmarks.md)。
- 已实测：contract 15 完整 release/部署/现行 verifier/smoke；D01 三轮 contract 15 pass；
  D02 三轮无 fail但 native-scroll gap；D03 24 pass/3 fail；D04 3 fail/15 gap；
  D05 最新 1 pass/1 fail且旧三轮原报告 fail；D06 在 `38` 与 `049` 各三轮单 delta 闭环
  pass，外部宿主刷新仍未独立确认。
- 未覆盖：D01 宿主落盘、D02 native scroll、D03 in-scope 完整阻断与 primitive 精确计数、
  D04 键盘顶起/focus race/primitive 精确计数、D05 capture/preview/post-dispatch STOP
  注入、D06 实际外部 MCP 宿主刷新。以上缺口均由用户接受。

最终文档审计由独立只读 Ollama/Nietzsche（max，agent
`01a1e1c0-eeed-7021-afec-f27386835e31`）完成。§6.1 的三类首轮发现由原文档作者 Ramanujan
最小修正四份文件；同一审计者复检并合并五份未改文件结论，最终原话为
“本次全部九份docs CLEAN”。此次无中断、重启或提供方错误，不与 S7 Gemini 的配额失败和
四类发现历史混同。

全部实现提交为 `900e640fe8343f92c4e8dd82dd3df839dde20756`、
`bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`、
`38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`、
`049583d328e335c56d56f153b7464ea03f1ca814`。最终已验证发行源码锚点为 `049583d...`；
归档提交只承载维护记录与已归档提案，不能写成产生上述 manifest 的发行源。若需再次核验或
发布，应在 `049583d` 的固定干净 checkout 使用保存的六产物/host 2 exe 与 manifest 重新
执行原 verifier。

归档静态检查覆盖六份提案、主验证记录、统一基准和 status 共 9 个维护文件：354 个本地链接、
226 个行号片段与 1 个标题片段均可达，错误和旧提案目录链接残留均为 0。4 个历史越界行号
链接只将 href 收敛到现存源码文件，显示的历史行号与基线语境保留。六份头部状态、最后实现
hash、验证入口及各一份 §11.1/§11.2 均已核对；归档提交仅承载上述维护材料。
