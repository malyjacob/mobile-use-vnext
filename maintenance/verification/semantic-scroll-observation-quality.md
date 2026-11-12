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
- 构建基线：首次构建 `2cc559a`；部署所用构建 `bd6aae7`（归档后最终 HEAD，源码与 `2cc559a`
  相同，仅 maintenance 提交不同；清单 `source_commit=bd6aae7`、`source_dirty=false`）。
- 环境：Windows 主机（Go 1.27.1 windows/amd64，`GOEXPERIMENT=jsonv2`，JDK 17.0.20.1、
  Android SDK、adb 设备 `17efb04a`）；手机为 `24117RK2CC`，部署后服务
  `0.10.0 / contract 10`、App `0.10.0 / versionCode 12`，会话为 `virtual` 模式。
- 本记录分开写“已实现 / 已单测 / 已构建 / 已部署 / 已实测（有限范围）”；未执行项见 §7，
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

首次构建在 `2cc559a`；部署前因 HEAD 前进到 `bd6aae7`（只增加归档记录）在干净树上重新构建，
以下为部署所用构建的事实。构建不访问设备。

| 步骤 | 实际结果 |
| --- | --- |
| `pwsh scripts/build-release.ps1` | 退出 0；`release=0.10.0 contract=10 android=12 source_commit=bd6aae7 source_dirty=False`；工具目录 `count=11 sha256=23957b25098f4cec6b959d1fc02fc0ee5639563899aee3bdec5190df5c8b9913`；六个产物齐全 |
| `device-service` | SHA-256 `6e4f4a3712dd6115ff05fd1e72c234a39cef686e9ac87735567fe0a8499df726`，8,585,376 字节 |
| `device-mcp`（linux/arm64） | SHA-256 `46b54cfce3203370bcbffe824188c9230ce3e49874f21dc992ed700dd0f59c97`，9,109,664 字节 |
| `control.apk` | SHA-256 `fb9a48078f46cfa1941b5d4516eedfd13caedd79105ba93bb628aebc6840011e`，103,965 字节（沙箱内 Android 构建非字节可复现；同源码两次构建 APK 哈希不同） |
| `u2.jar` / `applist.dex` / `service.sh` | 已在清单记录 SHA-256/大小；`u2.jar=28361193…`、`applist.dex=63818f32…`、`service.sh=4b568a9a…` |
| `pwsh scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | PASS；6 个产物、Android 0.10.0/12、`source_dirty=False`、`head=bd6aae7…` |
| `pwsh scripts/build-host-tools.ps1` | PASS；`device-mcp.exe` SHA-256 `80af12971b82cdf32e273e627a5d68d7756bc5e7a0f0876ca10c3948a05e0baf`（10,034,688 字节）、`mcp-probe.exe` SHA-256 `117d592846f4f2ebb2ed3378977367677051e9911a4d41d3d9b3fa5baf8c1801`（9,644,032 字节） |
| `pwsh scripts/build-host-tools.ps1 -VerifyOnly` | PASS；phone companion release=0.10.0 contract=10、`source_dirty=false`、工具目录 11/`23957b25…`、`head=bd6aae7…` |

## 4.1 手机整组部署

部署在干净提交 `bd6aae7` 的构建上执行，命令与结果：

| 检查 | 实际结果 |
| --- | --- |
| `pwsh -NoProfile -File scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json` | 退出 0；`Deployment ready` |
| `/health` | `{"status":"ok","contract_version":"10","release_version":"0.10.0"}`（实例 `e9e5f14f…`） |
| 安装后的 App | `versionName=0.10.0`、`versionCode=12` |
| 手机服务进程 | PID 21376，`/proc/21376/exe` SHA-256 `6e4f4a37…`，与清单一致 |
| 手机 HTTP MCP 适配器 | PID 21471，`/proc/21471/exe` SHA-256 `46b54cfc…`，与清单一致；监听 `127.0.0.1:18882`，主机转发 `18883` |
| 会话与准入 | `end` 后 `session.state=ended`、`cleanup=complete`（execution/tasks/recents/display 全 confirmed）、`can_start=true` |
| 旧会话 | 部署前会话因服务重启记为 `service_restarted`，`cleanup=complete`；没有发现未收尾的活动工作 |
| 遗留进程 | 手机侧另有一个旧 stdio 适配器 PID 20408（`/proc/20408/exe` 为已删除的旧二进制，SHA-256 `ad7b874b…`），由手机侧另一个 DSH `dsh-mcp-connector` 宿主托管；本仓库的 Windows 宿主不使用它，需由该宿主自行重启才能加载新二进制 |

## 4.2 主机刷新与探针

| 检查 | 实际结果 |
| --- | --- |
| `mcp_connector_reconnect`（`json-mobile-use-vnext`） | 成功；宿主重新注册 11 个工具；连接状态 `ok` |
| 主机 `device-mcp.exe` 进程 | PID 12964，23:02 启动，路径 `build/device-mcp.exe`，磁盘 SHA-256 `80af1297…` 与 host-tools 清单一致 |
| `mcp_connector_tool_detail act` | description 与 schema 含 `scroll`/`container`/`direction`/`amount`/`duration_ms`，六分支枚举 |
| `build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe -transport stdio -result-mode both -timeout 180` | `RESULT PASS`；`tools=11`、catalog 指纹 `23957b25…`、act scroll schema、`status can_start=true`、`start/observe/act/end`、`act` 后 `quality=stable samples=3 stop_reason=stable effect=absent（key）` |

手机上另有一个由该设备上的 DSH web 宿主托管的旧 stdio 适配器进程（见 4.1）；本会话使用的
Windows DSH 连接已按上表刷新，11 工具与契约 10 的实际连接验证通过。

## 4.3 实机受控滚动与 raw swipe 样本

在 Settings 虚拟屏上执行（`virtual` 模式，1080×2400）；所有受控 `scroll` 都使用公开
`role=scroll` 容器与 `direction`/`amount`，raw `swipe` 显式 `duration_ms=500`。

| 动作 | outcome | effect | quality | 证据/说明 |
| --- | --- | --- | --- | --- |
| `start` | executed | — | stable/2/stable | 首个 full 观察；virtual 屏 |
| `app.open com.android.settings` | executed | — | stable/3/stable | 进入设置列表，容器 `n19` |
| `scroll down page`（容器 `n19`） | executed | unknown（`container_changed`） | stable/3/stable | 折叠标题使容器 bounds 从 `[0,349,…]` 变为 `[0,0,…]`；无 `target_changed` |
| 再次 `scroll down page` | executed | not_observed（anchors=21，coverage=100） | stable/3/stable | 列表已到底、树 delta=0；`not_observed` 只在 stable+全覆盖+无位移时成立 |
| `scroll up page` | executed | unknown（`container_changed`） | stable/3/stable | 标题重新展开，容器 bounds 再次变化 |
| 通知与状态栏页 `scroll down page` | executed | unknown（`container_changed`） | stable/3/stable | 折叠/结构变化改变了容器作用域 |
| 我的设备页 `scroll down page` | executed | unknown（`size_changed`，anchors=23，coverage=76） | stable/3/stable | 边界单元裁剪改变尺寸，按固定判据拒绝推测 |
| raw `swipe` `540,1800 → 540,600`，duration 500 | executed | 不出现（非 scroll） | stable/3/stable | duration 实际到达适配器；无 effect 事实 |

关键结论与边界：

- 5 次受控 `scroll` 全部 `executed`，**没有一次 `target_changed`**；容器解析、真实起终点
  scope 证明与派发在实机上成立（`scroll.revalidate` 日志 `container_proven=true`、
  `start_scope_proven=true`、`end_scope_proven=true`）。
- 实机没有取得 `effect=observed`：设置主列表/通知页的容器 bounds 会随折叠标题变化，判为
  `container_changed`；内容页滚动时边界裁剪造成尺寸差，判为 `size_changed`；列表到底时得到
  `not_observed`。这与提案 §4.5 的固定判据和 §7.2 的能力边界一致，不把 `unknown` 写成未移动，
  也不据此声称阈值已校准。
- raw `swipe` 仍保持起点完整身份证明；本次样本成功，但内容重排时仍可能 `target_changed`，
  不能拿本样本声称 issue #10 的“raw swipe 10 次 0R”目标已达成。
- 本次样本只覆盖 Settings 一个应用与本机系统级页面，不构成跨应用结论；`observed` 的实机样本、
  跨应用分布与 P50/P95 仍未取得。

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
| 手机整组部署（`deploy.ps1 -SkipBuild`） | 已执行；见 §4.1。部署前无未收尾活动会话，旧会话记 `service_restarted`；部署后 `can_start=true`。 |
| 实际宿主 MCP 连接刷新与 `tools/list` catalog 比对 | 已执行（Windows DSH 连接）：重连后 11 工具、act schema 含 scroll、`mcp-probe` catalog 指纹 `23957b25…` 与清单一致；见 §4.2。 |
| 受控 scroll/raw/quality/effect 的实机与跨应用验收 | 部分执行。Settings 一个应用 5 次受控 scroll 全部 `executed`、0 次 `target_changed`；`effect` 得到 `not_observed` 与 `unknown`，未取得实机 `observed`。长文、B 站/WebView、嵌套容器、边界、overlay、STOP 时序与破坏性手势 fixture 未执行；没有跨应用成功率或 P50/P95。 |
| 发布前阈值校准 | 未执行。`scrollPageRatio/scrollHalfRatio/scrollDurationMS/effect*` 保持提案初始值；实机样本不足以校准，不写成已验证。 |
| `scripts/verify-m6.ps1` 实机运行 | 未执行；只修正了其上一发行契约常量 8→9，并通过 PowerShell 解析检查。 |
| 手机侧其余 stdio 宿主 | 未重启：手机侧 DSH web 宿主托管的旧 stdio 适配器（PID 20408，旧二进制已删除）仍存活；本会话使用的 Windows DSH 宿主已刷新。该宿主需自行重启才能加载新二进制。 |

## 8. 结论边界

- 已实现：契约 10 的协议/版本、受控 scroll、raw swipe duration、观察质量、滚动效果、P 事实、
  MCP/client/probe 投影、发布脚本门禁与文档。
- 已单测：上述行为均有单元/回归测试；关键判据有反向对照。
- 已构建：0.10.0 / 契约 10 / versionCode 12 的整组产物与主机工具从干净提交构建，两套清单
  与工具目录指纹核验 PASS。
- 已部署：手机服务/App/HTTP MCP 适配器与 Windows DSH 宿主均升级到契约 10；手机侧另一个旧
  stdio 宿主仍待其自身重启。
- 已实测（有限）：Settings 实机 5 次受控 scroll 全部 `executed`、0 次 `target_changed`，
  `quality` 均为 `stable`；`effect` 固定判据按实机证据得到 `not_observed`/`unknown`，未取得
  `effect=observed` 或跨应用样本；raw `swipe` duration 500 成功。`shell exited 0` 不等于屏移等
  既有边界仍按提案原文保留，本记录不冒认。

## 9. 交付闭环

| 项 | 实际结果 |
| --- | --- |
| 推送 | `master` 推送到 `origin`（`https://github.com/malyjacob/mobile-use-vnext`），未 force；远端包含实现提交 `273bda4`…`2cc559a` 与部署/实机补记 `d15d047`。本补充提交随同一分支再次推送。 |
| issue #10 | 已回复迁移/验证/差异说明：<https://github.com/malyjacob/mobile-use-vnext/issues/10#issuecomment-6827481941>；随后以 `COMPLETED` 关闭，回读 `state=CLOSED`、`stateReason=COMPLETED`、`closedAt=2026-11-12T15:13:11Z`。<https://github.com/malyjacob/mobile-use-vnext/issues/10> |
| issue #7 | 未关闭、未重开；回读仍为 `CLOSED / NOT_PLANNED`。 |
| 未随本闭环完成 | 实机 `effect=observed`、跨应用/长文/嵌套/overlay/STOP 时序与阈值校准；手机侧另一旧 stdio 宿主重启。 |
