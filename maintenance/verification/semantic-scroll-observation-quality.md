# 受控 scroll、raw swipe duration 与观察质量/效果：实现与验证记录

- 提案：[semantic-scroll-observation-quality.md](../proposals/implemented/semantic-scroll-observation-quality.md)（确认后实施）。
- 实现提交（按顺序）：
  1. `273bda4` 契约 10 协议与版本源（scroll/swipe duration/quality/effect 线格式）；
  2. `c939f79` 发布/部署脚本门禁与旧项目部署兼容清理；
  3. `383bca5` MCP schema/文本投影与 client 解码；
  4. `90fedf1` engine 观察质量（settle stop_reason 与 image 刷新计入样本）；
  5. `4563621` probe 契约 10 断言；
  6. `65385a2` verify-m6 的上一发行契约修正；
  7. `552acea` engine 受控 scroll / raw swipe duration / effect / P 事实；
  8. `2cc559a` 正式文档（含独立只读审计 CLEAN）。
- 构建基线：`2cc559a`（干净工作树，`source_dirty=false`）。
- 环境：Windows 主机（Go 1.27.1 windows/amd64，`GOEXPERIMENT=jsonv2`，JDK 17.0.20.1、
  Android SDK 可用）；未做手机部署、未触碰设备服务与 App。
- 本记录分开写“已实现 / 已单测 / 已构建 / 未实测”。本次没有实机结论；未执行项见 §7，
  不把单元测试或清单 PASS 写成设备验收。

## 1. 分诊与提案

分诊为取舍：现行 `swipe` 要求起点叶节点完整身份，滚动正文时内容移动导致
`target_changed`；后观察的不稳定结果仍按普通 `ready` O 发布，且观察质量、滚动效果没有公开
事实。用户确认采用受控 `scroll` 尝试 + raw `swipe` 分开、`Observation.quality`、
`Result.effect`，契约前进到 10、发行 0.10.0、Android versionCode 12。提案中列出的路线对比、
非目标与证据边界按原文保留；本记录不重述未执行结论。

## 2. 已实现

| 提案条目 | 代码落点 |
| --- | --- |
| `act.operation.type=scroll` 与 `container`/`direction`/`amount` 严格解码、分支互斥 | `internal/protocol/requests.go`（`Operation`、`parseOperation`、`parseScrollDirection`、`parseScrollAmount`） |
| `swipe.duration_ms` 50–2000、默认 300、严格拒绝 | `internal/protocol/requests.go`（`parseSwipeDuration`）、`internal/protocol/encode.go` |
| `Observation.quality`（state/samples/stop_reason）与 `Result.effect` 线格式 | `internal/protocol/responses.go`、`decode_response.go` |
| 版本源 0.10.0 / 契约 10 / versionCode 12 与生成常量 | `release/versions.json`、`scripts/generate-version.ps1`、生成 Go/Java/XML/Manifest |
| 静态容器解析（ref 直接 scroll、point 候选归约、去重、歧义/无容器/几何拒绝） | `internal/engine/scroll.go`（`resolveScrollPlan`、`scrollContainerAtPoint`、`scrollGeometry`） |
| 认领后容器完整身份/祖先匹配、同一几何函数、实际起终点最内层 scope 证明 | `internal/engine/scroll.go`（`revalidateScrollTarget`、`scrollScopeMatches`）、`internal/engine/act.go` |
| 固定中心线轨迹派发（物理原语/账本 kind 仍 `swipe`，400ms） | `internal/engine/scroll.go`（`dispatchScroll`）、`internal/engine/act.go` |
| raw swipe duration 透传；`ambiguous_point` 对 raw swipe 的可执行提示 | `internal/engine/act.go`、`internal/engine/point.go`、`internal/android/foreground.go` |
| `quality` 投影：settle stop_reason、image 刷新计入样本、observe/交付 `not_sampled`、失败树无 quality | `internal/engine/settle.go`（`qualityOf`）、`capture.go`、`delivery.go` |
| `effect` 推断：容器内容匹配、叶子内容单元、覆盖/尺寸/同号位移门限、CPU/ctx 预算 | `internal/engine/scroll.go`（`computeScrollEffect`、`scrollContentUnits`、`matchScrollUnits`） |
| `ActAction`/`Effect` 有界 P 事实、finish 前条件化提交、status/重复 P 只复制、冻结不升级 | `internal/engine/engine.go`（`operationState`、`operationViewLocked`）、`internal/engine/operations.go`（`recordScrollAction`、`commitScrollEffect`） |
| 受控日志事件与白名单 | `internal/engine/act.go`、`scroll.go`、`capture.go`、`delivery.go`、`internal/eventlog/log.go` |
| MCP schema、guide、结果文本/结构化投影、client 解码 | `internal/mcp/schema.go`、`result_text.go`、`internal/client/client_test.go` |
| 发布门禁：干净树/HEAD 前缀/source_dirty=false/完整手机配套/companion PASS（不接受 SKIP）；开发-only `-AllowDirtySource` | `scripts/verify-manifest.ps1`、`scripts/build-host-tools.ps1` |
| 部署只停止本仓库 App/服务/手机 MCP；旧项目兼容停止删除 | `scripts/deploy.ps1` |
| probe 契约 10、act scroll schema、quality/effect 投影断言 | `scripts/mcp-probe/` |
| 正式文档 | `docs/interface.md`、`mcp.md`、`architecture.md`、`client.md`、`release.md`、`runbook.md`、`integration.md`、`control-protocol.md`、`phone-operations.md`、`README.md`、`maintenance/decisions.md` |

新增测试集中在 `internal/engine/scroll_test.go`、`scroll_revalidate_test.go`、
`scroll_effect_test.go`、`quality_test.go`、`internal/protocol/*_test.go`、
`internal/mcp/*_test.go`、`internal/client/client_test.go`、`internal/android/transport_test.go`、
`scripts/mcp-probe/*_test.go`。

## 3. 已单测

| 命令（`GOEXPERIMENT=jsonv2`） | 实际结果 |
| --- | --- |
| `go build ./...` | 退出 0 |
| `go vet ./...` | 退出 0 |
| `go test ./... -count=1` | 全部包 `ok`；`internal/engine` 294.806s，其余包见下 |
| `go test ./internal/engine -run TestScroll -count=1 -timeout 90s -v` | 40+ scroll 用例全部 PASS，28.4s |
| `go test ./internal/engine -count=1 -timeout 600s` | `ok` 292.297s |
| `go test ./internal/android -count=1` | `ok` 8.144s |
| `go test ./internal/mcp ./internal/client ./internal/httpapi -count=1` | 全部 `ok` |
| `go test ./scripts/mcp-probe -count=1` | `ok` 8.979s |
| `pwsh -NoProfile -File scripts/generate-version.ps1 -Check` | `release=0.10.0 contract=10 android_version_code=12` |

关键断言摘要：

- 协议：scroll 四方向/page/half/默认、分支互斥、显式 null、未知成员/枚举、swipe duration
  50/300/2000 与 0/负数/49/2001/浮点/null；`quality`/`effect` 编码顺序、`samples=0` 与
  `not_sampled` 显式编码、解码必填/枚举/负样本拒绝。
- scroll 静态：ref 直接容器、叶/非 scroll/root/context/disabled/无 bounds 拒绝、
  point 同容器去重、两个不同 scope 歧义、无容器拒绝、过小容器在认领前拒绝（零采集/派发、
  O 保持 ready）；busy 不认领且同一 O 可重试。
- scroll 重验：容器移动/缩放 `target_changed` 并交付新帧；容器稳定、内容移动仍 `executed`
  （核心回归）；实际起终点落入内层 scope 拒绝；外层包裹祖先允许；已发布且改变实际起终点
  scope 的 overlay 拒绝；ref 与 point 选择器都执行实际轨迹证明。
- 轨迹：四方向、page/half、整数 margin/travel、半开矩形/屏幕、越界不 clamp、过小拒绝。
- effect：100% 覆盖 + stable + 无位移 -> `not_observed`；固定标题保留、其余内容换出 ->
  `unknown`；2 个一致移动/Low coverage 2 movers/3-of-4 -> `observed`；2-of-3、
  方向冲突 -> `unknown`；父子内容只算叶子；空结构节点不计数；容器/单元 resize -> `unknown`；
  <2 anchors、后观察失败/撤销、ctx/CPU 预算 -> `unknown`；`effect` 不改 outcome；
  `executed + unknown` 合法。
- P 事实：static scroll 后在 claim 前记 `ActAction`；busy 拒绝对 status(P) 不伪造 effect；
  dispatch uncertain、executed_revoked、post_observation_failed -> `unknown`；成功 effect 在
  status(P)/重复 P 复制；closed known/unknown、淘汰后不恢复旧 action/effect。
- quality：1 帧 budget/unreadable -> `unknown`；≥2 帧不等 -> `unstable`；相等 -> `stable`；
  max_samples/budget/unreadable 各自枚举；observe/交付 -> `unknown/0/not_sampled`；
  image=true 刷新相等计入样本并置 stable、最多 6 帧；刷新读取失败不增样本；失败树无 quality。
- raw swipe：移动叶节点仍 `target_changed`；duration 实际到达 adapter（默认 300，显式
  50/2000）；同 P 重放只派发一次；`ambiguous_point` 对 swipe 文案可执行、错误码不变。
- MCP/probe：act schema 含 scroll/container/direction/amount/duration_ms；文本投影含
  effect/quality 且三模式同源；probe 断言 scroll-only effect、act 后观察 quality 与 schema。
- 日志：`scroll.plan`/`scroll.revalidate`/`scroll.effect`/`act.point` 带 operation_id；
  非白名单键实际被丢弃；不记录 ref/坐标/文本/树。

## 4. 已构建与清单核验

以下均在干净提交 `2cc559a` 上执行，工作树无已跟踪改动；构建不访问设备。

| 步骤 | 实际结果 |
| --- | --- |
| `pwsh scripts/build-release.ps1` | 退出 0；`release=0.10.0 contract=10 android=12 source_commit=2cc559a source_dirty=False`；工具目录 `count=11 sha256=23957b25098f4cec6b959d1fc02fc0ee5639563899aee3bdec5190df5c8b9913`；六个产物齐全 |
| `device-service` | SHA-256 `6e4f4a3712dd6115ff05fd1e72c234a39cef686e9ac87735567fe0a8499df726`，8,585,376 字节 |
| `device-mcp`（linux/arm64） | SHA-256 `46b54cfce3203370bcbffe824188c9230ce3e49874f21dc992ed700dd0f59c97`，9,109,664 字节 |
| `control.apk` | SHA-256 `7bdbec43c261e66d2c74e6023f104244f658e66665ba78954cc02bd215722fa7`，103,965 字节 |
| `u2.jar` / `applist.dex` / `service.sh` | 已在清单记录 SHA-256/大小；`u2.jar=28361193…`、`applist.dex=63818f32…`、`service.sh=4b568a9a…` |
| `pwsh scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | PASS；6 个产物、Android 0.10.0/12、`source_dirty=False`、`head=2cc559a…` |
| `pwsh scripts/build-host-tools.ps1` | PASS；`device-mcp.exe` SHA-256 `80af12971b82cdf32e273e627a5d68d7756bc5e7a0f0876ca10c3948a05e0baf`（10,034,688 字节）、`mcp-probe.exe` SHA-256 `117d592846f4f2ebb2ed3378977367677051e9911a4d41d3d9b3fa5baf8c1801`（9,644,032 字节） |
| `pwsh scripts/build-host-tools.ps1 -VerifyOnly` | PASS；phone companion release=0.10.0 contract=10、`source_dirty=false`、工具目录 11/`23957b25…`、`head=2cc559a…` |

## 5. 反向对照（实际执行，恢复后复跑绿）

| 临时去掉/改动的实现 | 变红的用例 |
| --- | --- |
| 去掉 scroll 分支互斥（protocol） | `TestDecodeActBranchMutex` 等 7 例 |
| 让 `quality.samples=0` 省略 | quality 编码顺序/roundtrip 用例 |
| 去掉 image 刷新相等的 `stable=true` | `TestQualityImageRefreshEqualCompletesPair` |
| `qualityOf` 的 `samples>=2` 改为 `>=1` | 单帧 budget/unreadable 的 unknown 用例 |
| 去掉 quality 日志的 observation_id | 两条 quality 日志用例 |
| effect consensus 75→0 | `TestScrollEffectObservedConsensus/two_of_three_unknown` |
| 去掉 not_observed 的覆盖条件 | `TestScrollEffectFixedTitlesDoNotProveNotObserved` |
| 去掉 not_observed 的 `quality==stable` | `TestScrollEffectNotObservedNeedsFullCoverageAndStable` |
| 关闭单元尺寸差检查 | `TestScrollEffectSizeAndContainerChangesAreUnknown/unit_resize` |
| 反转 down 几何映射 | `TestScrollGeometryTable` / `TestScrollGeometryDirectionMappingsAreStrict` |
| 用起点叶节点 `matchFreshNode` 替换容器授权 | `TestScrollContentMovesContainerStableExecuted`（核心用例） |
| 去掉结果文本的 effect/quality 分类 | manifest/parity/文本用例 |
| 去掉 act schema 的 scroll 成员 | `TestActScrollSchema` |
| probe 去掉 effect/quality | `TestActScrollResultProjection` / `TestProbeAgainstRealAdapter` |

## 6. 文档审计

变更涉及 `docs/`。由独立、只读的审计 subagent 审核 `docs/interface.md`、`mcp.md`、
`architecture.md`、`client.md`、`release.md`、`runbook.md`、`integration.md`、
`control-protocol.md`、`phone-operations.md`（同时核对 README 与 maintenance 描述）。

- 首轮结论 `NOT CLEAN`：3 组 must-fix（scroll 缺失 ref 的错误码自相矛盾、mcp quality 出现
  条件、`source_commit` 前缀措辞）及若干 minor。
- 已逐条修正，复检结论 `CLEAN`；审计者未修改任何文件。正式文档随实现提交 `2cc559a` 落盘。

## 7. 未执行项与原因

| 项 | 结论与原因 |
| --- | --- |
| 手机整组部署（`deploy.ps1 -SkipBuild`） | 未执行。本次只做到从干净提交构建并核验整组清单；部署会停止当前服务/App 并改变已配对设备的运行版本，且需要按 `docs/release.md` §4 在部署窗口保持准入关闭。未取得部署/安装后 App versionName/versionCode、`can_start`、手机 HTTP MCP `/proc/<pid>/exe` 哈希等事实，不写成已部署。 |
| 实际宿主 MCP 连接刷新与 `tools/list` catalog 比对 | 未执行。主机 exe 已重建并核验，但未替换/重启实际宿主的 MCP 进程或连接；未用实际 DSH MCP 连接重算 catalog 指纹，也未经该连接做 scroll。 |
| 受控 scroll/raw/quality/effect 的实机与跨应用验收 | 未执行。提案 §8.2 的长文、设置、B 站/WebView、嵌套容器、边界、overlay、STOP 时序、破坏性手势 fixture 等均未在真实设备上取数；没有实机 success rate、`quality`/`effect` 分布或阈值校准。 |
| 发布前阈值校准 | 未执行。`scrollPageRatio/scrollHalfRatio/scrollDurationMS/effect*` 保持提案初始值；这些值未经实机校准，不写成已验证。 |
| issue #10 回复/关闭、分支推送 | 未执行。按提案 §8.4/§10，这些只在实现、部署/实测、版本与远端记录齐备后进行；本次不提前关闭，不伪报已推送。 |
| `scripts/verify-m6.ps1` 实机运行 | 未执行（未部署）；只修正了其上一发行契约常量 8→9，并通过 PowerShell 解析检查。 |

## 8. 结论边界

- 已实现：契约 10 的协议/版本、受控 scroll、raw swipe duration、观察质量、滚动效果、P 事实、
  MCP/client/probe 投影、发布脚本门禁与文档。
- 已单测：上述行为均有单元/回归测试；关键判据有反向对照。
- 已构建：0.10.0 / 契约 10 / versionCode 12 的整组产物与主机工具从干净提交构建，两套清单
  与工具目录指纹核验 PASS。
- 未实测：没有任何设备部署或实机滚动/质量/效果结论；`shell exited 0` 不等于屏移等既有边界
  仍按提案原文保留，本记录不冒认。
