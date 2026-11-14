# 受控滚动中心线安全区间与联动视口效果：实现与验证记录

- 提案：[scroll-safe-trajectory-and-viewport-effect.md](../proposals/implemented/scroll-safe-trajectory-and-viewport-effect.md)（用户确认后实施）。
- 实现提交：`badc8564af0f4196e04cb1648c35a044d2a7036c`（2026-11-15）。同一次提交含实现、测试、
  正式文档与唯一版本源（`contract_version` 12→13、`release_version` 0.13.0、
  `android_version_code` 15）。
- 构建基线：首次整组构建即该提交（清单 `source_commit=badc8564…7036c`、`source_dirty=false`）。
- 环境：Windows 主机（Go 1.27.1 windows/amd64，`GOEXPERIMENT=jsonv2`；JDK 17、Android SDK
  `android-37.0`/`build-tools 36.0.0`）；手机 adb 设备 `17efb04a`，部署后服务
  `0.13.0 / contract 13`、App `0.13.0 / versionCode 15`，会话为 `virtual` 模式。
- **本记录涉及实机**：契约边界、受控滚动三方向样本、静态 `422` 与联动视口伸缩在真机上执行过；
  未取得样本的场景见 §7，不把单元测试或清单 PASS 写成实机结论。

## 1. 分诊与提案

分诊为取舍：现行实现的端点作用域证明与保守效果判据有正式规范与测试支持，属于设计取舍而不是
实现偏离（[接口 §6](../../docs/interface.md)、[接口 §11](../../docs/interface.md)、
[发布规范](../../docs/release.md)）。用户确认采用提案 R3：固定中心线最长可证区间、认领后只
证明原整段、联动视口下用内部尺寸稳定候选给正证据，并把共同契约号推进到 13。提案 §9.2 未留
给实现阶段自行决定的算法判据；本记录不重述未执行结论，差异与缺口列在提案 §11.2 与本记录 §7。

## 2. 已实现

| 提案条目 | 代码落点 |
| --- | --- |
| 共用中心线作用域归约：固定正交坐标、整树继承最近滚动作用域、raw 祖先集合索引与环保护、不按父矩形剪枝、不可用 source 不回退 | `internal/engine/scroll.go`（`scrollScopeSpace`、`scrollSafeSpans`、`rawAncestorSet`、`scrollScopeNone`/`scrollScopeUnusable`） |
| 阻塞区间的半开裁剪、去空、排序、合并（相交与相邻）、相减；最长区间与平局规则 | `scroll.go`（`subtractScrollSpans`、`longestSpan`、`scrollSpan`） |
| 静态规划：最长区间 → `safeRect` → 既有 `scrollGeometry` → 整段复核；`422` 认领前拒绝 | `scroll.go`（`resolveScrollPlan`、`safeRect`、`unsupportedScrollTrajectory`） |
| 整段证明谓词（屏幕/裁剪/公开容器/固定中心线/同一安全区间） | `scroll.go`（`provesPoint`、`provesSegment`） |
| 认领后原段重验：容器完整身份与祖先链唯一匹配、公开 `scroll` 能力复核、不重选区间、不重算端点 | `scroll.go`（`revalidateScrollTarget`） |
| 联动视口容器守卫：正交边 ≤1px、正面积交集、四边不变标志 | `scroll.go`（`orthogonalScrollEdgesStable`、`rectsEqualWithin`） |
| 尺寸与轴向贴边分类；裁剪端点排除 | `scroll.go`（`scrollEligibleUnit`、`touchScrollAxisEdge`、`scrollContentUnit.axisClipped`） |
| 双参照位移与四桶共识；独立负证据与有序回退；阶段与最终预算检查 | `scroll.go`（`computeScrollEffect`、`scrollSign`） |
| `scroll.revalidate` 整体 `proven` 布尔值 | `scroll.go`（`logScrollRevalidate`）；键已在 `internal/eventlog` 白名单 |
| 唯一版本源与生成常量 | `release/versions.json`、`scripts/generate-version.ps1`、`internal/protocol/version_generated.go`、`android-control/app/src/main/java/com/mobileuse/vnext/GeneratedVersion.java`、`strings_version.xml`、`AndroidManifest.xml` |
| 正式文档 | `docs/interface.md`、`architecture.md`、`release.md`、`control-protocol.md`、`runbook.md`、`phone-operations.md`、`client.md`、`integration.md`、`README.md` |

新增/调整用例集中在 `internal/engine/scroll_test.go`、`scroll_revalidate_test.go`、
`scroll_effect_test.go`。

## 3. 先红（保留摘要）

新行为用例在改实现前先红，两段证据：

1. **通路不存在**：用例引用尚未实现的共用区间函数时，`go test ./internal/engine/` 在 setup 阶段
   失败（`undefined: scrollSafeSpans` / `scrollSubtractSpans` 一类的编译错误）。
2. **旧行为被断言否决**：补入共用区间骨架、仍保留旧消费方后，行为级用例真实失败（节选）：

| 先红用例 | 失败摘要 |
| --- | --- |
| `TestScrollSafeSpanAvoidsTabBar/down_page` | 注入 `{x1:540 y1:2018 x2:540 y2:942}`，期望 `{y1:2049 y2:1118}`；旧实现用整个容器矩形 |
| `TestScrollSafeSpanAvoidsTabBar/up_page` | `outcome=not_executed error=target_changed`；旧实现只在认领后拒绝 |
| `TestScrollSafeSpanFullyBlockedRejectedBeforeClaim` | `error=target_changed, want unsupported_node_action`；旧实现会认领并采集一次 |
| `TestScrollDynamicSegmentConflictRejected/middle` | `outcome=executed, want not_executed`；旧实现只查两端，中段冲突仍可派发（本提案必需的反向对照） |
| `TestScrollEffectAxisViewportShrinksWithInternalMovers` | `effect={State:unknown Reason:container_changed}, want observed` |
| `TestScrollEffectContainerContentReferenceFrames/content_follows_and_moves` | 同上：轴向视口变化被四边守卫否决 |
| `TestScrollEffectPostContainerCapability/disabled` | `effect=observed, want unknown/container_changed`；旧实现不检查后容器能力 |

转为绿后，三份文件连同既有 `scroll`/`STOP`/P 事实回归全部通过。

## 4. 已单测与已构建

命令在独立 shell 执行，主机测试不带 `GOOS=linux`/`GOARCH=arm64`。

| 命令 | 真实结果 |
| --- | --- |
| `./scripts/generate-version.ps1` / `-Check` | `version source consistent: release=0.13.0 contract=13 android_version_code=15` |
| `GOEXPERIMENT=jsonv2 go test ./...` | 全部包 `ok`（`internal/engine` 约 353–356s，其余 0.04–15s） |
| `GOEXPERIMENT=jsonv2 go test ./internal/engine/... ./internal/protocol/... ./internal/httpapi/... ./internal/client/... ./internal/mcp/...` | 全部 `ok` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 退出 0 |
| `./scripts/build-release.ps1` | 六个产物；`source_commit=badc8564…7036c`、`source_dirty=False`、tool catalog `count=12 sha256=04177607…72b1e9` |
| `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS … release=0.13.0 contract=13 android=0.13.0/15 artifacts=6 … source_dirty=False` |
| `./scripts/build-host-tools.ps1` | `RESULT PASS`；`device-mcp.exe`、`mcp-probe.exe` 与目录指纹 |
| `./scripts/build-host-tools.ps1 -VerifyOnly` | `PASS`，手机清单作为 companion 版本、目录指纹一致 |

工具目录指纹与上一发行相同（`04177607…72b1e9`）：本次不新增工具、不改 schema 与描述，
只有执行语义与契约号前进。

## 5. 已部署与契约边界（实机）

`./scripts/deploy.ps1 -SkipBuild` 从已提交干净树产物整组部署：

- 清单核验 PASS 后逐产物核对 SHA-256（`device-service`、`device-mcp`、`u2.jar`、
  `applist.dex`、`service.sh`、`config.json`、`mcp-config.json`、`connection.json`），
  APK 覆盖安装后核验 `package=com.mobileuse.vnext versionName=0.13.0 versionCode=15`。
- `Deployment ready`：`version match`（health `0.13.0`/`13`）、`App pairing`、`recovery barrier`、
  `STOP availability`、手机 HTTP MCP 适配器 401 探测均为 PASS；`can_start=true`。
- 部署脚本对仍持有旧二进制的 stdio 宿主给出告警；按用户指示，依赖方 Agent 的宿主重启由其
  自行处理，本轮只替换主机侧两个二进制并核验清单。

契约边界（HTTP 经 `adb forward 18881→18880`）：

| 请求 | 结果 |
| --- | --- |
| `POST /api/status` + `X-Mobile-Use-Contract: 12` | `409 contract_mismatch`，`recovery.entry=update` |
| 同一请求缺契约头 | `409 contract_mismatch` |
| `POST /api/status` + `X-Mobile-Use-Contract: 13` | `200`，`can_start=true` |
| `POST /v1/control/stop` + 体内 `"contract_version":"12"` | `200`，`accepted=true`（最小 STOP 独立于普通契约） |

旧契约被拒后重新同步即可恢复 `can_start=true`，与 §4.2 的「静态拒绝不改变准入」一致。

## 6. 实机样本（受控滚动）

环境：虚拟屏 1080×2400；服务 0.13.0/契约 13。所有样本经 `/api/act` 的 `scroll` 分支发起，
每次先用 `observe(full=true)` 取新 `O` 再选择当帧最大的滚动容器；不落库任何原始树或截图。

### 6.1 Settings 垂直样本

容器 1080×2051，6 次 `scroll`（`down`/`up` × `page`/`half`，含重复方向）：

- 全部 `outcome=executed`，`target_changed` **0 次**，`proven=true` 100%。
- `effect`：`observed` × 2（日志 `anchors=17 coverage=80`），`not_observed` × 4
  （`anchors=20–21 coverage=100`）。
- `not_observed` 出现在列表已到端、可见内容确实未再移动的帧上；这正是负证据的语义，
  不表示手势失败，也不表示到达边界（见 §7 的边界说明）。

### 6.2 Settings 横向样本（能力边界）

`right/page` → `outcome=executed`、`effect=unknown`（日志 `anchors=0 coverage=0
reason=container_changed`）。事后确认页面被系统返回手势导航到了下钻页：全宽容器的横向轨迹
起点距屏边仅 `margin=108px`，落在系统手势区内。本例说明服务只证明公开树上的滚动作用域，
不证明 z-order 或真实触摸消费者，与提案 §7.2 的既有边界一致；它没有产生伪 `observed`。

### 6.3 静态拒绝（真实 `422`）

| 页面/容器 | 结果 |
| --- | --- |
| Zhihu 外层 feed（1080×1902，中心线被内层滚动作用域整段覆盖） | `422 unsupported_node_action`，文案「无法在该容器中心线上生成可证明的滚动轨迹」，`recovery=retry/O/effective` |
| Zhihu 横向 chip 栏（1080×113，`page` 行程 39px < 48px） | `422`，同上 |
| X 外层 feed（1080×2242，内层作用域覆盖中心线） | `422`，同上 |

三次拒绝后原 `O` 仍为 `ready`，且这些尝试没有产生 `scroll.plan`/`scroll.revalidate` 事件，
即零重验采集、零派发。改为选择内层容器后同一页面即可 `executed`。

### 6.4 X 联动视口样本（现场报告几何）

`com.twitter.android` 首页 feed：

- 容器几何在 `2242 ↔ 2400` 之间变化：`up/page` 后 `2400 → 2242`（顶栏展开），
  `down/page` 后 `2242 → 2400`（顶栏收起），与 issue #13 报告的底边变化一致。
- 该场景下 `effect` 仍为 `unknown`：一次样本已越过容器守卫、给出
  `reason=insufficient_anchors`（`anchors=0`），说明轴向视口伸缩不再是拒绝原因；其余样本为
  `reason=container_changed`（X 虚拟化列表让容器/祖先的完整内容身份与匹配集同时变化）。
- 19 次 `scroll.plan` 全部 `scroll.revalidate … proven=true`，`proven=false` 0 次。

### 6.5 会话收尾

两次实机会话均以 `/api/end` 结束（`session.state=ended`，`cleanup=pending` 属既有保留事实），
随后 `can_start=true`。手机未重启、未卸载 App、未清库。

## 7. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 联动栏页面取得实机 `observed` | X 的容器确实沿轴伸缩，但其内容换血与容器身份变化让匹配集为空；没有找到「容器伸缩 + 至少两个稳定内部候选」的可用页面。Settings 的 `observed` 没有伴随容器伸缩。 |
| 「带局部滚动覆盖」的受控合成页实机样本 | 本轮按用户指定的部署范围只安装整组发布产物与两个主机二进制，未安装验证探针 App；该场景由合成用例与 Zhihu/X 的真实嵌套作用域样本覆盖。 |
| 现场报告 Tab 栏的精确坐标实机复现 | 没有与报告几何一致的可用页面；§7.1 的四个方向 × 两个幅度的精确坐标由合成用例断言。 |
| MCP 宿主侧工具重新发现 | 用户自行处理依赖方 Agent；本轮只替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 并核验主机清单。 |
| `scripts/verify-m6.ps1` | 沿用此前用户排除项，本轮未执行。 |
| `not_observed` 与到达边界的关系 | 不构造：`not_observed` 只描述可测矩形未见位移，服务不判断是否到达边界，也不据此推断手势失败。 |

## 8. 性能摘要

口径只有两项，且都只在本机同环境比较样本：

- 静态拒绝（`422`）：HTTP 往返 18–26ms（4 次样本），随后零 `scroll.plan`、零采集、零派发；
  这不是「省下的那次 probe+capture」的单独测量，只是拒绝路径的整体往返。
- 已执行 `scroll`：HTTP 往返 2.4–2.8s（19 次样本），含重验采集、一次 swipe 与后观察收敛。

CPU 复杂度按提案为 `O(N)` 遍历 + `O(B log B)` 排序减法；本轮未测 P50/P95、未测单次规划耗时，
不宣称固定收益或跨应用成功率。

## 9. 正式文档审计

涉及 `docs/` 与 `README.md` 的变更由**独立只读** subagent 审计（不实现、不改文件、不提交）。
首轮 5 项发现与 1 项审计附加发现全部修正后复检 **VERDICT: CLEAN**：

1. `docs/interface.md` 的 `/api/*` 契约头示例仍是 `12`；
2. `docs/runbook.md` 两个 curl 示例的契约头仍是 `12`；
3. `docs/interface.md` 的 effect「尺寸与轴向贴边」句自相矛盾（实现是贴边即排除，含等尺寸）；
4. `docs/architecture.md` 把 `anchors`/`coverage` 误挂在 `scroll.revalidate` 下；
5. `docs/interface.md` 未标明 `anchors`/`coverage` 只是受控日志诊断量、不进入结果；
6. 审计额外指出 `docs/integration.md` 仍有多处 `12` 契约头（本次一并修正并纳入复检）。

复检同时确认：四份文件与 `release/versions.json`/生成常量一致（`0.13.0`/`13`/`15`、12 工具）、
effect 判据与 `internal/engine/scroll.go` 一致、相对链接全部可解析、无过程性措辞。

## 10. 结论分层

- **已实现**：§2 的全部落点，含契约 13 与版本生成物。
- **已单测**：§3 的先红用例与 §4 的包内测试（区间代数、逐整数点交叉核对、四方向幅度与
  门限两侧、动态端点/中段、参照系四桶、负证据与裁剪盲区、STOP/P 事实）。
- **已构建**：linux/arm64 交叉构建、整组发布构建、清单核验、主机工具构建与核验。
- **已部署**：整组产物覆盖安装到手机，App 私有数据与账本保留。
- **已实测（有限范围）**：§5 契约边界与 §6 的 Settings/X/Zhihu 样本；未取得样本的场景已在 §7
  写明原因。
