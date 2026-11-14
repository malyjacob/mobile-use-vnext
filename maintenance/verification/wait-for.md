# 条件等待与稳定采集 `wait_for`：实现与验证记录

- 实现提交：`a077210f191836d5ad24f0862349c525dd16225a`（2026-11-14，含契约 11→12、
  发行 0.12.0 / Android versionCode 14）。
- 提案：[wait-for.md](../proposals/implemented/wait-for.md)。
- 本记录涉及实机：是。设备序列 `17efb04a`；控制 App 0.12.0（versionCode 14），会话为
  `virtual` 模式；手机侧 HTTP MCP 适配器监听 `127.0.0.1:18882`，主机转发 `18883`，
  服务端口 `18880`/`18881`。
- 本记录分开写“已实现 / 已单测 / 已发布 / 已实测”。单测通过不等于实机结论；未执行项写在
  §7，不把发布清单或一次成功响应当成全部验收。
- **本次不执行 `scripts/verify-m6.ps1`**（用户排除：force-stop/重启演练与 notify/UI 全量
  回归耗时过长且具破坏性）。替代证据见 §5、§6。

## 1. 分诊

分诊为取舍：现行规范里 `observe` 无稳定判定、`wait_ms` 上限 10s 且只能盲等，`act`/`start`/
`app` 的收敛窗口只有 1.5s–3s，导致长耗时流程只能由 Agent 外层反复 `observe` 轮询。用户
确认新增正交工具 `wait_for`（路线 Y，不改造既有动作的后置观察），提案见上。归因、执行、
测试与验证记录均未跳过。

## 2. 已实现

落点表见提案 §11.1。要点：

| 条目 | 落点 |
| --- | --- |
| 严格接入（16 KiB、reference、timeout 1–40、条件 ≤256 码点且非空白、null 拒绝、矛盾条件） | `internal/protocol/requests.go`（`DecodeWaitFor`、`checkWaitQuery`）、`internal/protocol/encode.go` |
| `Result.wait`（`trigger`/`matched`/`elapsed_ms`）与质量枚举扩展 | `internal/protocol/responses.go`、`internal/protocol/decode_response.go` |
| 单一状态锁准入（过期拒绝 + 续租 + 继承请求 `ctx` + `P` 强制 full） | `internal/engine/operations.go`（`acquireWaitWork`）、`internal/engine/observe.go`（`resolveObserveTargetLocked`） |
| 等待采样、连续 3 帧判稳、四路错误分流、早退探测、采样守卫 | `internal/engine/wait_for.go`（`pollWaitConditions`、`sampleWaitFrame`） |
| 验证采集（35s 固定预留、候选帧复用、到时重采终帧、质量对齐） | `internal/engine/wait_for.go`（`completeWaitCapture`、`waitQualityOf`、`waitMatched`）；`internal/engine/capture.go`（`captureValidated`、`readPollCapture`） |
| 传输 / 客户端 / MCP / 控制端 / 脚本 | `internal/httpapi/server.go`、`internal/client/client.go`、`internal/mcp/{schema,server,result_text}.go`、`ControlState.java`、`scripts/mcp-probe`、`scripts/verify-m5.ps1` |
| 版本与指纹 | `release/versions.json` 0.12.0/12/14；工具目录 `count=12`、`sha256=04177607f49d99c84aabd80632c37554989918e6f0454ed882a739d9fa72b1e9` |

## 3. 已单测

| 命令 | 实际结果 |
| --- | --- |
| `go build ./...`（`GOEXPERIMENT=jsonv2`） | 退出 0 |
| `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 退出 0 |
| `go vet ./internal/engine ./internal/protocol ./internal/mcp ./internal/httpapi ./scripts/mcp-probe` | 退出 0 |
| `go test ./... -count=1` | 全部包 `ok`：`internal/engine` 317.089s、`internal/mcp` 3.1s、`scripts/mcp-probe` 12.1s、`internal/httpapi`/`internal/protocol`/`internal/client`/`internal/android`/`internal/observation`/`internal/store`/`internal/imaging` 均通过 |
| `./android-control/test.ps1` | `DisplayAttemptGateTest passed`、`MessageListTest passed` |
| `./android-control/build.ps1` | `control.apk` 签名校验通过 |

新增/更新的关键断言（`internal/engine/wait_for_test.go`，16 例）：

| 用例 | 断言 |
| --- | --- |
| `TestWaitForPureSleepPublishesTimeoutFrame` | `trigger=timeout`、`matched=["timeout"]`、`quality=unknown/0/not_sampled`；恰好 1 次终帧采集（纯 sleep 不轮询） |
| `TestWaitForQueryHitReusesCandidateFrame` | 命中即返回、候选帧复用（轮询 2 次读、无第三次读）、`quality=unstable/condition` |
| `TestWaitForStableNeedsThreeConsecutiveFrames`（反向对照） | A,B,B 不得放行（按累计采样数会提前），A,B,B,B 才放行；`quality=stable/4/stable` |
| `TestWaitForAbsentQueryIgnoresTransitionFrame` | 仅合成根的过渡帧不触发 `absent_query`；读 3 次后命中 |
| `TestWaitForMaxSamplesGuardReportsTimeout` | 守卫退出记 `trigger=timeout`、`stop_reason=max_samples` |
| `TestWaitForFrameTimeoutStaysTransient` | 单帧上限截断按瞬态处理，不消耗探测；4 次截断后仍能命中；探测只有验证采集的 1 次 |
| `TestWaitForProbesDisplayAfterRepeatedFailures` | 连续 3 次设备失败后探测到显示缺失 → `503 display_unavailable`，且不带 `wait` |
| `TestWaitForRefusesExpiredSessionWithoutRenewal` | 已过期会话 → `409 session_expired`、`recovery.input_state=revoked`、租约未被续 |
| `TestWaitForRenewsLeaseInsideAdmission` | 准入单锁内把 2s 租约续进 120s 窗口 |
| `TestWaitForClientDisconnectReleasesWorkPosition` | 断连 → `409 control_revoked`、不带 `wait`/`observation`、工作位立即释放、随后 `observe` 成功 |
| `TestWaitForRefusesBusyDevice` | 占用工作位 → `409 device_busy` |
| `TestWaitForRejectsFoldedContradiction` | 折叠后相同的两个条件 → `422 invalid_request`，不发明 outcome |
| `TestWaitForStatusReplayKeepsWaitFact` | `status(P)` 保留 `wait` 且不带 `observation` |
| `TestWaitForPReferenceForcesFullTree` | `P` 引用发布 full（与 `observe(P)` 一致） |
| `TestWaitForImageReuseKeepsCandidateSpan` | 命中帧复用 + 图片完整（`status=complete`），只读 1 次树、1 次图 |
| `TestWaitPolicyValidate` / `TestWaitPolicyBudgetFitsHTTPCeiling` | 策略非法值被拒；默认策略满足 `ceil(40s/600ms)+5 <= 80` 与 `40+35 < 40+40 < 90s` |

其他包：`internal/protocol` 的严格接入表新增 22 条 `wait_for` 用例与 `WaitForRequest`/`WaitResult`
编解码往返；`internal/httpapi/wait_for_test.go` 覆盖 16 KiB 上限、严格接入、投递槽耗尽
（`503 delivery_unavailable`）、契约头与方法；`internal/mcp` 覆盖 12 工具目录、基线指纹、
`required` 映射、结果黄金样例与 diagnostic 透传；`scripts/mcp-probe` 覆盖 12 工具冻结与
`wait_for` 探针检查。

## 4. 已发布

| 步骤 | 命令 | 实际结果 |
| --- | --- | --- |
| 整组构建 | `./scripts/build-release.ps1` | `source_dirty=False`、`source_commit=a077210…225a`；6 产物与 `tool_catalog=12/04177607…72b1e9` 写入 `build/release/manifest.json` |
| 清单核验 | `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS`：`release=0.12.0 contract=12 android=0.12.0/14 artifacts=6 tool_catalog=12/04177607…72b1e9 source_dirty=False head=a077210…225a` |
| 主机双二进制 | `./scripts/build-host-tools.ps1` | `RESULT PASS`；`build/device-mcp.exe` `sha256=0cc43777…d8171`、`build/mcp-probe.exe` `sha256=11e143b9…fd9e05`；目录未被清空（上一镜像保留为 `*.previous-*`） |
| 主机清单门禁 | `./scripts/build-host-tools.ps1 -VerifyOnly` | `PASS`：手机清单为 companion、`source_dirty=False`、`tool_catalog=12/04177607…72b1e9` |
| 手机整组部署 | `./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins 'http://127.0.0.1:3000'` | `Deployment ready.`；`version match: PASS (release=0.12.0 contract=12)`；`App pairing: PASS`；`installed App: versionName=0.12.0 versionCode=14`（`adb install -r`，未卸载、未清库）；`phone MCP HTTP adapter: PASS listen=127.0.0.1:18882` |

说明：第一次部署未带 `-McpAllowedOrigins`，手机侧探针的 `origin_allowed` 因适配器未放行任何
Origin 得到 `403`；按 `docs/runbook.md` 的做法补上精确放行后重新部署，该项转 `PASS`。
`deploy.ps1` 自身不验证 `tools/list`（它只保证 `server/discover` 可达），工具目录由 §5 的
`verify-m5.ps1` 与手机侧 `mcp-probe` 分别确认。

## 5. 已实测：主机侧

`./scripts/verify-m5.ps1 -SkipBuild`（先跑 `build-host-tools.ps1 -VerifyOnly`，再以
stdio / 主机 HTTP / P-recovery 三种 transport 跑 `mcp-probe`）：

- `adapter startup log: PASS tools=12`；`host tools identity ... tool_catalog=<12>`。
- stdio 与主机 HTTP：`PASS tools count=12 source_count=12 legacy_absent=true`、
  `PASS catalog tools=12 names=set_equal description=identical schema=semantic_equal`、
  `PASS catalog_fingerprint sha256=04177607…72b1e9 tools=12`。
- 新增探针检查：`PASS wait_for trigger=timeout matched=1 ms=1339 id=O…_8 status=complete
  quality=unknown/not_sampled`。
- 其余既有检查（`observe` 图片 540×1200、`app_find/list`、`inspect` 树块、`act` 质量
  `stable/3/stable`、`end` 收尾、取消）全部 `PASS`；`RESULT PASS`。

## 6. 已实测：手机侧与 `wait_for` 最小闭环

### 6.1 手机侧 MCP 探针

`./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json
-result-mode text`：

- `PASS service_remote transport=http-remote contract=12 release=0.12.0 process_managed=false`
- `PASS auth_missing status=401`、`PASS origin_denied status=403`、`PASS origin_allowed status=200`、
  `PASS agent_token_rejected status=401`
- `PASS tools count=12`、`PASS catalog tools=12 names=set_equal ...`、
  `PASS catalog_fingerprint sha256=04177607…72b1e9 tools=12`
- `PASS wait_for trigger=timeout matched=1 ms=1354 id=O…_3 status=complete
  quality=unknown/not_sampled`
- `RESULT PASS`

### 6.2 最小闭环（`virtual` 模式，受控测试目标 `com.mobileuse.probe`）

统一前置：`start` 取得会话与 `O`；`app.open(com.mobileuse.probe)` 取得着陆帧；其后每次
`wait_for` 用上一步发布的 `O` 作 `reference`，每次由客户端分配新 `P`。

| 场景 | 请求 | 实际结果 |
| --- | --- | --- |
| `query` + `stable=true` 提前命中 | `wait_for(query="TAP", stable=true, timeout=15)` | HTTP `200`，墙钟 **1651 ms**（未跑满 15s）；`trigger=query`、`matched=["query"]`、`elapsed_ms=1624`、`quality=stable/3/stable` |
| `absent_query` 首帧命中 | `wait_for(absent_query="永远不会出现的文本", timeout=10)` | HTTP `200`，墙钟 **469 ms**；`trigger=absent_query`、`matched=["absent_query"]`、`elapsed_ms=439`、`quality=unknown/1/condition` |
| 纯 sleep + 图片 | `wait_for(timeout=3, image=true)` | HTTP `200`，墙钟 **4338 ms**；`trigger=timeout`、`matched=["timeout"]`、`quality=unknown/0/not_sampled`；`observation.status=complete`、图片 `image/jpeg 540×1200`、无 `missing` |
| 条件永不命中（对照） | `wait_for(query="状态：就绪", stable=true, timeout=15)` | HTTP `200`，跑满 **15429 ms**；`trigger=timeout`、`matched=["timeout"]`、`quality=stable/26/stable`。该文案在页面上位于视口外，公开树里不存在命中节点（见 §7 第 3 条） |
| 等待期撤销 | 后台 `wait_for(timeout=15)`，约 2s 后 `end(reference=<O>)` | 等待在 `end` 后约 **50 ms** 返回 HTTP `409`：`error.code=control_revoked`、`message=等待已撤销`、`recovery={entry:"end",reference:"P",input_state:"revoked"}`，**不带 `wait`、不带 `observation`**；`status(P)` 为 `finished/phase=wait` 且无 `wait`/`observation`；`diagnose` 时间线含 `wait_for:control_revoked`，会话 `ended(client_end)`、`cleanup=complete`、`can_start=true`，没有迟到的新 `O` |

## 7. 未执行项与原因

1. **`scripts/verify-m6.ps1` 未执行**：用户排除（force-stop/重启演练与 notify/UI 全量回归
   耗时且具破坏性）。该脚本的 `tool_catalog.count==11` 与“上一契约==10”断言也未随本次契约
   12 同步，下次启用前必须先修正。
2. **控制端 STOP 按钮未直接按下**：App 的 `com.mobileuse.vnext.STOP` 服务动作不导出，
   `adb shell am startservice` 被 `Requires permission not exported from uid` 拒绝。改用
   Agent 侧 `end(P)` 触发同一条撤销路径（`revokeSessionLocked` → 取消 `work.Ctx`），并记录
   §6.2 最后一行的时延与结果。
3. **primary 模式 `stable=true` 表征未执行**：切换主屏模式需要操作者在 App 界面点击
   （`SELECT_PRIMARY` 同样不导出）。本次不改动用户主屏；virtual 模式下已取得同一表征
   （`stable/3/stable` 与 `stable/26/stable`）。
4. **“加载中→目标按钮”的过渡界面未构造**：仓库没有这类受控页面，改用受控测试目标的真实
   页面完成 `query`+`stable`、`absent_query`、纯 sleep+image 三类闭环。
5. **外部 MCP 宿主进程未重启**：本次只重建并原地替换 `build/device-mcp.exe` 与
   `build/mcp-probe.exe`（含清单核验），未管理其他 Agent 宿主对这两个二进制的依赖与重启；
   宿主侧重新发现 12 工具由用户自行处理。
6. **实机未覆盖的分支**：`tree_too_large`/`stale_screen` 的验证采集失败组合、连续失败后的
   早退 `ProbeDisplay`、`MaxSamples` 守卫，只在单测中构造；实机未逐一手工触发。

## 8. 同类核查

- 既有的 `observe` 在已过期会话上仍走 `control_revoked`（而非 `session_expired`）：本次没有
  改动它，`wait_for` 的差异已在 `docs/interface.md` §14 与提案 §4.4 写明，属有意分歧而非漂移。
- `settleUntilStable` 的 `samples>=MinSamples` 判据未改：`wait_for` 用独立的连续相等计数器，
  两处不共享判据，避免把 3 帧稳定改成累计采样。
- `work.Phase` 的前缀形态（`<tool>.<step>`）与既有 `captureAndPublish` 一致；控制端新增的
  前缀映射同时覆盖 `app.open.*` 这类多级前缀与裸短码 `read`。
