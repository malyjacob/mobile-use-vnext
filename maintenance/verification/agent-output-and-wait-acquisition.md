# 验证记录：Agent 输出与 wait 终帧复用（第一批）

状态：2026-11-17。实现提交：`07c85b6`（分支 `zcode/agent-tool-evolution`）。
提案：[agent-output-and-wait-acquisition.md](../proposals/agent-output-and-wait-acquisition.md)。
本记录只覆盖第一批 #16/#21/#22 与 #20 取证结论；第二份提案未开始，提案未归档，版本未冻结。

## 1. 范围与结论

| 项 | 结论 |
| --- | --- |
| #21 device-mcp 默认结果模式 | 已实现并单测：无 flag/env 时 `text`；显式 `both`/`structured`、env 与 HTTP 每请求头覆盖保持。 |
| #16 Instructions 与工具描述 | 已实现并单测：完整 tree guide 只在 server instructions 出现一次；12 个工具描述 caller-oriented；目录指纹随描述更新；探针逐字节比对 live `server/discover` 说明并附加兼容 `initialize` 检查。 |
| #22 wait 终帧复用 | 已实现并单测：timeout/max_samples 可复用最后成功且之后无失败、选择与提交均在 `CaptureMaxAge=15s` 内的轮询帧；不能复用时只读一次终帧；提交门禁拒绝后不重读。 |
| #20 delta 策略 | 保留 `12/70/180s`。结构 benchmark 的最大 delta/full 为 `7.361%`，但没有宿主/模型任务轨迹、Token 或误用证据，未达到提案 §4.4 的调整门槛。 |
| 正式文档 | 已同步并通过独立只读文档审计 CLEAN。 |
| 第一阶段出口 | **未完全满足**：代码、单测、文档审计与进程级入口检查完成；真实设备服务/宿主、慢层级 wait 行为、真实模型任务回放与发布清单核对未执行。 |

## 2. 环境与构建

- Windows 10.0.19044 x64；Go `go1.27.1 windows/amd64`；`GOEXPERIMENT=jsonv2`。
- 设备：ADB 可见 `17efb04a`（Android 16，型号 24117RK2CC）；设备上存在 `com.mobileuse.vnext` 与 `com.mobileuse.probe` 包，但没有 `/opt/mobile-use-vnext` 部署目录，也没有运行中的 device-service；`127.0.0.1:18880/18881` 均未监听。因此本次没有可用的真实服务会话。
- 跨平台构建：
  `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` → `BUILD_OK`。
- `GOEXPERIMENT=jsonv2 go vet ./...` → 无输出（通过）。
- 主机入口检查使用本提交源码构建的临时二进制（未入库）：
  `.state/host-check/device-mcp.exe`、`.state/host-check/mcp-probe.exe`：

  ```text
  GOEXPERIMENT=jsonv2 CGO_ENABLED=0 go build -trimpath \
    -o .state/host-check/device-mcp.exe ./cmd/device-mcp
  GOEXPERIMENT=jsonv2 CGO_ENABLED=0 go build -trimpath \
    -o .state/host-check/mcp-probe.exe ./scripts/mcp-probe
  ./.state/host-check/mcp-probe.exe -config .state/mcp-config.json \
    -binary .state/host-check/device-mcp.exe -transport stdio -result-mode text
  ./.state/host-check/mcp-probe.exe -config .state/mcp-config.json \
    -binary .state/host-check/device-mcp.exe -transport http -result-mode text
  ```

## 3. 实际执行的命令与结果

### 3.1 受影响的单测与整包回归

| 命令 | 结果 |
| --- | --- |
| `GOEXPERIMENT=jsonv2 go test ./cmd/device-mcp/... -count=1` | `ok ... 0.099s` |
| `GOEXPERIMENT=jsonv2 go test ./internal/mcp/... -count=1` | `ok ... 0.447s` |
| `GOEXPERIMENT=jsonv2 go test ./scripts/mcp-probe/... -count=1` | `ok ... 8.998s` |
| `GOEXPERIMENT=jsonv2 go test ./internal/engine -run 'TestWait' -count=1` | `ok ... 33.799s` |
| `GOEXPERIMENT=jsonv2 go test ./internal/engine -count=1` | `ok ... 485.069s`（修复测试夹具竞态后） |

`internal/engine` 全量测试在修复前连续三次在
`TestStopPassesLedgerWaitAndPreventsCreateDispatch` 触发异步 persist 读取 `store` 接口时
的 panic；基线 `1cb6fbd` 的独立 worktree 全量测试通过（`470.575s`）。根因是两处测试夹具
在未持有 `persistMu` 时替换 `Engine.store` 接口，异步 `persistSync` 可能看到撕裂值。已把
`commit_test.go:130` 与 `retirement_isolation_test.go:164` 的替换移到 `persistMu` 下，
全量重跑转绿。该修复只涉及测试夹具，不改变生产语义。

### 3.2 #21 默认模式与优先级

进程级 stdio 启动日志（`mcp.start`）实际结果：

| 启动参数/环境 | 日志 |
| --- | --- |
| 无 `-result-mode`、无环境变量 | `result_mode=text` |
| `-result-mode both` | `result_mode=both` |
| `-result-mode text` | `result_mode=text` |
| `-result-mode structured` | `result_mode=structured` |
| `MOBILE_USE_MCP_RESULT_MODE=structured`，无 flag | `result_mode=structured` |
| `MOBILE_USE_MCP_RESULT_MODE=structured` + `-result-mode text` | `result_mode=text` |
| `MOBILE_USE_MCP_RESULT_MODE=legacy` | 退出码 `2`，stderr `invalid result mode` |

反向对照：把 fallback 临时改回 `ResultModeBoth`，`TestResultModeDefaultAndPrecedence/default_text`
失败（`configured result mode = both, want text`）；恢复 `text` 后通过。

### 3.3 #16 体积、指纹与 live discovery

同一 live in-memory SDK 提取（`session.ListTools` 的 `json.Marshal(list.Tools)`）：

| 分项 | 基线（`1cb6fbd`） | 本提交 |
| --- | ---: | ---: |
| `instructions_utf8_bytes` | 0 | 4967 |
| `tools_list_utf8_bytes` | 36250 | 20128 |
| 12 个 description UTF-8 小计 | 21430 | 6873 |
| 12 个 input schema UTF-8 小计 | 12668 | 12656 |
| **总量（instructions + tools_list）** | **36250** | **25095** |

总量下降 `11155` 字节（`30.8%`），满足提案 §8.1.1 的“严格小于同一基线”；没有为此删除
默认值、恢复入口或 ID 选择。`instructions` 只保留一份完整 tree guide，12 个描述均不含
完整 guide。

`GOEXPERIMENT=jsonv2 go run ./scripts/toolcatalog` →
`{"count":12,"sha256":"312b6975e7a4706241c5a17c0cfcb0336330799e29286f98a4619a9f219c40fd"}`。
`TestCatalogFingerprintBaseline`、`TestDiscoveryByteBudget`、`TestServerInstructionsDiscovery`、
`TestServerInstructionsCompatibilityInitialize`、`TestToolDescriptionsExcludeTreeGuide`、
`TestToolDescriptionsAreCallerOriented` 均通过。A 阶段禁用词（`bubble`、`press_enter`、
`allow_ancestor_fallback`、统一 `reference`、默认 ENTER）在 instructions 与 12 个描述中
均不出现；`recovery.reference` 与既有 body `reference` 保留。

live 入口检查（本提交构建，未运行 device-service）：

```text
stdio:
PASS connect transport=stdio result_mode=text
PASS instructions bytes=4967 source_bytes=4967 source_match=true protocol=2026-07-28
PASS instructions_compat protocol=2025-11-25 bytes=4967 source_match=true
PASS tools count=12 source_count=12 legacy_absent=true
PASS catalog tools=12 names=set_equal description=identical schema=semantic_equal
PASS catalog_fingerprint sha256=312b6975e7a4706241c5a17c0cfcb0336330799e29286f98a4619a9f219c40fd tools=12
FAIL status reason=tool_is_error code=transport_unavailable
RESULT FAIL

http:
PASS auth_missing status=401
PASS origin_denied status=403
PASS origin_allowed status=200
PASS agent_token_rejected status=401
PASS connect transport=http result_mode=text
PASS instructions bytes=4967 source_bytes=4967 source_match=true protocol=2026-07-28
PASS instructions_compat protocol=2025-11-25 bytes=4967 source_match=true
PASS tools count=12 source_count=12 legacy_absent=true
PASS catalog tools=12 names=set_equal description=identical schema=semantic_equal
PASS catalog_fingerprint sha256=312b6975e7a4706241c5a17c0cfcb0336330799e29286f98a4619a9f219c40fd tools=12
FAIL status reason=tool_is_error code=transport_unavailable
RESULT FAIL
```

两处 `FAIL status` 都是因为没有运行 device-service（`transport_unavailable`），不是
instructions、目录或认证检查失败。

HTTP 兼容 `initialize` 检查使用单次 POST 读取 `2025-11-25` 的 initialize 响应；stateless
Streamable HTTP 无法完成后续 `notifications/initialized` 的全量会话，因此探针只验证该次
响应的 `protocolVersion` 与 `instructions`，不把它伪装成完整会话。stdio 仍用完整兼容会话。

### 3.4 #22 wait 终帧复用

新增测试覆盖：

- `TestWaitForTimeoutReusesLatestFreshFrame`：timeout 复用最后一帧，读次数等于 samples，
  发布新 O 并退休旧 O。
- `TestWaitForMaxSamplesReusesLatestFreshFrame`：采样守卫复用，公开 trigger 仍为 `timeout`，
  quality 为 `unstable`/`max_samples`。
- `TestWaitForLatestSuccessFollowedByFailureCapturesTerminalFrame`：最后尝试失败使早先成功帧
  失效，随后只读一次终帧。
- `TestWaitForFailureThenSuccessReusesLastSuccess`：失败后再成功，复用最后成功帧。
- `TestWaitForNeverSuccessfulPollsReadOneTerminalFrame`：从未成功时只读一次终帧。
- `TestWaitForStaleAtSelectionCapturesTerminalFrame`：选择时已过期则回退一次终帧采集。
- `TestWaitForTerminalReuseStaleAtCommitPublishesNothing`：选择通过、提交门禁过期时返回
  `stale_screen`，不发布新 O，也不再读终帧。
- `TestWaitForTerminalReuseKeepsImagePartialIndependent`：复用帧 + 过期截图得到
  `partial`/`missing=["image"]`，树仍发布。
- `TestWaitForTerminalReuseCommitGateRefusesDisplayChange`：选择后显示代次变化，提交被拒。
- `TestWaitForTerminalReuseCommitGateRefusesRevocation`：选择后撤销，提交被拒。
- 既有 `TestWaitForPureSleepPublishesTimeoutFrame`、命中复用、稳定门禁与图片测试保持通过。

反向对照（工作包执行）：

- 去掉 `terminal_reuse` 分支后，timeout/max_samples 复用、失败后成功、提交过期、图片
  partial、显示代次与撤销等用例失败。
- 去掉最后尝试失效（`run.lastFrame = nil`）后，
  `TestWaitForLatestSuccessFollowedByFailureCapturesTerminalFrame` 失败（读次数为 2，
  复用了失败前帧）。
- 去掉选择时 freshness 守卫后，`TestWaitForStaleAtSelectionCapturesTerminalFrame` 失败。

未改变 `waitCaptureReserve=35s`、`CaptureMaxAge=15s`、`MinDwell=600ms`、timeout 含义、
普通队列/重试/跨调用缓存或 `publishTreeAttempt` 提交门禁。

### 3.5 #20 取证与保留结论

执行：

```text
GOEXPERIMENT=jsonv2 go test ./internal/observation -run '^$' \
  -bench 'BenchmarkStructuralDeltaB03' -benchmem -count=1
```

结果：6 种结构 × 4 种规模的 24 个样本全部满足既有 `10*deltaBytes < 7*fullBytes` 门禁；
最大 delta/full 为 `subtree_wrap/32` 的 `7.361%`，其余样本在 `0.003138%`–`7.361%`。
该 benchmark 只证明结构差分编码比例，不证明真实任务中的累计 Token、delta 误用率、
base 锚点距离、full 频率或 p95 延迟。

结论：**保留 `revision-baseline.Revision < 12`、基线龄 `< 180s`、严格 `< 70%`**。
原因：

- 没有同一模型/宿主/上下文保留策略下的 10/20 轮任务回放，也没有宿主或 tokenizer 的
  实际 Token 证据；按提案 §4.4，不能仅凭结构字节数宣称 Token 门槛通过。
- benchmark 的最大比例远低于 `70%`，无法区分 `70/50/45%` 对真实页面的影响；也没有
  `12/6/4` 跨度序列的 cumulative payload、full 频率或误用数据。
- 没有质量不降、误用不增、累计 Token 不增或下降的成对证据，因此不采用候选数值。
- `180s` 未动；没有新增公开阈值、freshness 或重试字段。

该保留结论不阻塞第二份提案，也不把第一批判为失败。

## 4. 已实现 / 已单测 / 已实测边界

| 层级 | 内容 |
| --- | --- |
| 已实现 | #21 fallback text；#16 instructions/描述/指纹/探针检查；#22 terminal_reuse 与三来源 quality；#20 保留现值（无生产策略变更）。 |
| 已单测 | `cmd/device-mcp`、`internal/mcp`、`scripts/mcp-probe`、`internal/engine` 全量；含反向对照与 A 阶段禁用词断言。 |
| 已做进程级检查 | 本提交构建的 stdio/HTTP 适配器：默认 text、显式三模式、env/flag 优先级、非法值退出 `2`、live `server/discover`/兼容 `initialize` instructions、12 工具目录/指纹、HTTP 认证与 Origin 检查。 |
| 未实测 | 真实设备服务/App 会话、STOP/end 下的 wait 迟到 O、慢 hierarchy 的 Capture 次数与 p95、真实宿主/模型任务回放、发布 manifest 与 live catalog 比对。 |

## 5. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 真实设备 stdio/HTTP 会话、图片与 header 三模式 | 设备上仅有 ADB 与已安装包，没有 `/opt/mobile-use-vnext` 部署目录或运行中的 device-service；本次不部署、不重启服务。 |
| 慢 hierarchy 设备的 timeout/max_samples Capture 次数、p95、旧 O 退休与 STOP 无迟到 O | 没有可运行的设备服务，也没有可控慢 dump 环境。 |
| #20 宿主/模型任务回放、Token、误用率、锚点距离与 full 频率 | 没有代表性宿主/模型与任务回放材料；按提案要求只记录结构 benchmark 与保留结论。 |
| A 阶段 Instructions 行为回放（真实模型工具轨迹） | 同一原因；单测只断言文案契约，不冒充模型行为结论。 |
| 发布 manifest/目录指纹 live 比对与正式发布 | 本次不冻结版本、不发布；`build/` 下旧清单仍含旧指纹，必须在后续干净提交树上重建。 |
| 提案归档 | 用户明确要求本会话不提前归档两份提案；第一阶段系统验证也未完全满足。 |

## 6. 第一阶段出口判定

- 代码实现、变更路径单测、反向对照、跨平台构建、文档同步与独立只读文档审计：**满足**。
- #20 保留/调整结论：**已给出并保留 `12/70/180s`**；证据不足以调整，符合提案的保留分支。
- 系统级与真实设备/宿主验证：**未执行**，因此第一阶段出口**未完全满足**，不能写成
  “第一批已全部通过”。
- 提案保持未归档，状态文件未改；第二份提案的 `reference` 迁移、`bubble`、`press_enter`
  未提前加入。

## 7. 后续会话交接

- 分支：`zcode/agent-tool-evolution`；实现提交：`07c85b6`；本验证记录单独提交。
- 提案：`maintenance/proposals/agent-output-and-wait-acquisition.md` 仍未归档；第二份
  `agent-reference-and-action-contract.md` 未开始。
- 版本源：`release/versions.json`、`docs/release.md`、manifest 均未改；契约仍为 15。
- 待补验证：在具备部署服务与可控慢 dump 的环境重跑 wait 终帧复用、STOP/end 迟到 O、
  三模式+图片；用同一宿主/模型完成 #20 任务回放与 A 阶段工具轨迹；在干净提交树上重建
  manifest 并做 live catalog/指纹比对。
- 已知构建产物：`build/host-tools-manifest.json` 与 `build/release/manifest.json` 仍是旧
  目录指纹；不要用它们核验本提交，需在后续发布流程重建。
