# 观察连续性、结构差分与动作重验：S0–S5 阶段进展与验证记录

- 状态：**进行中（已完成 S0–S5，剩余 S6–S10 按用户要求延后至下次会话）**。
- 本次涉及实机：**否**（当前仅在 Windows 主机完成代码实现、包级单元测试/局部基准与跨平台编译；未推进发布版本源、未同步 `docs/`、未构建发布清单或 Android APK、未执行真机 `D01–D06`）。
- 提案目录（仍保持“提案，未实现”）：[README.md](../proposals/observation-continuity-and-revalidation/README.md)、[分册 05 实施与验证计划](../proposals/observation-continuity-and-revalidation/05-implementation-and-validation-plan.md)。
- 代码基线：`6ec25c87daefbc01c99f78d968214304641d119d`（2026-11-15）。
- 提案落盘提交：`70f390edc0af794649c74ce8ccab532b60f977ad`（`docs(maintenance): 新增观察连续性、结构差分与动作重验提案`）。
- S1–S5 实现提交：`900e640fe8343f92c4e8dd82dd3df839dde20756`（`feat(observation,engine): 完成观察净化、连续性、B2差分与动作重验（S1-S5）`）。
- 环境：Windows 主机，`Go 1.27` 系列，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0`；工具目录指纹保持 `12` 个工具、`sha256=1a319cb307821d51bccfa1ed3df307c363b758973174623ee935c45bac2ffc55`；版本源维持 `release=0.14.0 / contract=14 / android_version_code=16`。

## 1. 阶段进度总览（S0–S10）

| 阶段 | 当前状态 | 说明与证据边界 |
| --- | --- | --- |
| S0 分诊/确认 | 已完成 | 提案目录（总览及分册 01–05）确认并落盘于 `70f390e`；Tier 4 保持默认关闭 |
| S1 复现与参考输入 | 已完成 | 各分册默认空叶、跨 display 上下文、三态 wire、列表保号、wrapper/unwrap、微移/焦点等合成 fixture 与测试驱动已落盘 |
| S2 净化与协议基座 | 已完成（已单测） | 净化过滤、最近公开父与跨 display 上下文组装、`Node`/`ReadNode` 严格三态、删除 `Defaults`、预算与 MCP/client/probe 同步（`V01–V06`、`V10` 候选/预览） |
| S3 连续性 | 已完成（已单测） | 公开索引与全局完整桶、五阶段匹配、集合投票、局部恢复、固定点与预算/高水位回滚（`V07–V11`、`B02`/`B04` 单元基准） |
| S4 结构差分 | 已完成（已单测） | B2 共享最终图规划与 `Node`/`ReadNode` 双应用器、Add 组装前沿、Replace 封闭传播、残余删除与严格 wire 往返（`V12–V15`、`B03` 基准） |
| S5 动作策略与实际派发 | 已完成（已单测/假设备） | Tier 1–3 层级匹配、透明层三态与 L1/L2 约束、`tapFrame` 安全载体解绑、`scrollFrame` 同容器动态重规划、`text` `exact/near/shifted` 与焦点/来源门禁、`android` 传输/RPC 边界测试与 MCP schema/stdio/httpapi/client/probe 对齐（`V16–V20`、`I04–I08`、`B05` 基准） |
| S6 联动与成本 | **未执行（下次会话）** | 待补全跨包端到端 `I01–I03` 联动与全套 `I01–I08` 回归，以及新旧基线 `B01–B06`（含 `B01`/`B06` 端到端成本对比与记录） |
| S7 文档/版本/提交前构建 | **未执行（下次会话）** | 待递增 `release/versions.json`、生成版本常量、同步受影响 `docs/` 并完成**独立只读文档审计 CLEAN**、构建 `./android-control/build.ps1`（及必要时 `./scripts/build-probe.ps1`） |
| S8 最终实现提交 | **未执行（下次会话）** | 待完成包含版本源、生成物与已审计 `docs/` 的最终整组实现提交（此时提案仍保持 `提案，未实现`） |
| S9 完整制品与系统/实机 | **未执行（下次会话）** | 待在 S8 干净提交执行 `build-release.ps1`、`verify-manifest.ps1`、`deploy.ps1`、重启实际 MCP 宿主并完成 `D01–D06`（或按接受范围记缺口） |
| S10 验证记录/归档/发行锚点 | **未执行（下次会话）** | 待定稿本记录、将提案整目录移入 `maintenance/proposals/implemented/observation-continuity-and-revalidation/`、重写链接与 §11.1/§11.2、更新 `status.md` 并独立提交归档 |

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

## 4. 下次会话接续入口（剩余阶段 S6–S10）

下次会话从提交 `900e640`（及本记录所在维护提交）继续，按 [分册 05 §10](../proposals/observation-continuity-and-revalidation/05-implementation-and-validation-plan.md) 推进：

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
