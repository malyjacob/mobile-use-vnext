# 提案：新增只读截图工具 `snap`

- 状态：已实现（实现提交 `de03df9f60ae67881b6397fda772566bfcfb6779`，2026-11-18）。
- 实现与验证记录：[snap.md](../../verification/snap.md)。
- 基线提交：`1019c356b24b62e3a9fb90b29ef075258c848404`（`master`，2026-11-18）。
- 适用范围：活动会话内的单次截图，覆盖 HTTP、Go client、MCP 和控制 App 的工作状态。
- 影响范围：`internal/engine`、`protocol`、`httpapi`、`client`、`mcp`、控制 App、探针与发布文件。
- 契约：新增 `/api/snap`、MCP `snap`、结果字段 `image` 和工作类别 `snap`，提升共同契约号。

实现前按当时的 `HEAD` 重新核对版本源、工作接纳与撤销、图片交付和工具目录。分诊为新增公共契约的取舍。

## 0. 摘要

新增 `snap(reference)`，在活动会话绑定的屏幕上立即读取一张图。复用现有截图链路，成功交付顶层 `image`。截图使用既有设备工作位，取消只撤销本次读取，输入仍走 O 和 `act` 实时重验。

## 1. 为什么加

只想确认弹窗遮挡、画布内容或页面布局时，调用 `observe(image=true)` 仍会读取和构建节点树。有树可发布时，服务会递增 revision、用新 O 替换当前 ready O，并在发布点续租。截图成功但树失败时，虽然可能得到只读图，HTTP 仍返回 503，MCP 仍标为 `isError=true`。历史实机记录中，设置页曾在主屏截图上清晰可见，而公开树只有 26 个状态栏节点。该树解析问题已修复，记录说明截图拥有独立的诊断用途。此次评审依据是代码路径和历史记录，性能收益留给实际验证。[观察采集](../../../internal/engine/capture.go#L142) · [观察发布](../../../internal/engine/capture.go#L304) · [MCP 成功条件](../../../internal/mcp/server.go#L452) · [主屏实机记录](../../verification/primary-display-multi-window.md#1-现象触发条件与分诊)

## 2. 目标

一项工作最多读一次源图、编码一次 JPEG。它不读树、不发输入，不更新 O、基线、revision 或租期。当前画面可以独立于节点树是否可读交付，例如活动会话在首树失败后可使用 `start` 的 P 定位目标屏。

## 3. 路线对比

| 路线 | 代价与失败模式 |
| --- | --- |
| 沿用 `observe(image=true)` 和 `wait_for(image=true)` | 公共面维持现状。适合同时取树和图，或等树条件后取图。纯看图仍依赖树成功，还可能替换调用者准备使用的 O。 |
| 给 `observe` 加纯图片模式 | 工具数保持 12，但同一入口会有两套成功条件、结果形状、基线与续租行为。`full`、`wait_ms` 等参数也要为该模式定义兼容和拒绝规则。 |
| 新增 `snap(reference)`（建议） | 一种请求形状，成功条件为交付一张经校验的图片。需要发布第 13 个工具和新的共同契约。屏幕身份变动、设备忙和截图失败仍会使本次调用失败。 |

`snap` 适合立即看当前画面。`observe(image=true)` 适合同时取新树和图。`wait_for(image=true)` 适合在树条件满足或等待到期后，拿终帧观察和一张图。[等待契约](../../../docs/interface.md#14-post-apiwait_for)

## 4. 接口与判据

- `POST /api/snap` 与 MCP `snap` 共用 `{"reference":"O1"}`。请求体上限 8 KiB。`reference` 必填，沿用 O/P 引用格式和当前 Agent、原活动会话的解析口径。保留中的过期、已认领或退休 O 可以定位原会话，图片始终取当前屏。引用已结束会话的 O 或 P、他人的引用、封闭 P、缺字段、显式 `null`、重复键等沿用既有拒绝规则。本次预分配 P 与请求体中的原有引用各自独立。
- HTTP 首次成功返回 `200`，仅包含必现的 `operation_id` 和顶层 `image`。失败使用顶层 `error`，失败、重复 P 和 `status(P)` 摘要均省略 `image`。显式 `null` 和不合法的成功图片均拒绝解码。在 `Result` 的固定字段顺序中，把 `image` 放在 `saved` 后、`session` 前。图片字段复用 `ImageMeta` 的编解码顺序，必须包含有效 `captured_at`、正整数 `width` 和 `height`、`media_type=image/jpeg`、`read_only=true`、合法 `screen` 和非空合法 `base64`。`captured_at` 取源图读取开始时间，`width`、`height` 是输出 JPEG 的像素尺寸。`screen` 记录准入时会话绑定显示的几何快照，用于说明缩放来源，旋转值是会话记录值。
- Go client 增加 `Client.Snap(ctx, *protocol.SnapRequest, ...CallOption)`。HTTP `image.base64` 经同一有界解码路径进入 `Response.Image`，`Result.Image` 保留元数据。SDK 除共用边界外，校验上述必填项，拒绝同时声明顶层 image 和观察内 image，拒绝带 `error`、`outcome` 或 `observation` 的截图成功形状。无图的重复 P 摘要允许正常解码。MCP `text` 以 `image=<紧凑 JSON>` 表达元数据，`structured` 在顶层 `image` 携带同组元数据，`both` 同时交付两种投影。三种模式均只发送一个 `ImageContent`，正文中没有 base64。`snap` 的 `isError=false` 要求完整图片实际交付。
- 成功结果仅有新 P 和图片。`snap` 不分配 O，不确认基线、不续租、不认领、不退休 O，也不生成执行 `outcome`。既有 O 继续按自身有效期和 `act` 实时重验使用。P 进入现有有界操作记录，记录只保存阶段和受控错误。图片字节不进入操作摘要、树历史、恢复账本、日志、探针报告或发布清单。截图里的像素位置可用于阅读，输入授权仍由引擎的 O 和实时重验给出。

以下是服务的 HTTP JSON 响应示例。`base64` 仅承载 JPEG 字节。Go SDK 将它解码到 `Response.Image`，MCP 将 JPEG 交付为 `ImageContent`。MCP 的文本和结构化正文只含图片元数据。

```json
{
  "operation_id": "P2",
  "image": {
    "captured_at": "2026-11-18T00:00:00Z",
    "width": 540,
    "height": 1200,
    "media_type": "image/jpeg",
    "read_only": true,
    "screen": {"width": 1080, "height": 2400, "rotation": 0},
    "base64": "…"
  }
}
```

## 5. 引擎算法与终态

1. **接纳。** HTTP 在严格解析和认证后，先预留一个现有 1 MiB 带图投递槽，失败报 `503 delivery_unavailable`。引擎在同一段状态锁内完成 P 登记或去重、原引用解析和工作接纳。复用 P 登记的锁内部分和 `resolveObserveTargetLocked`，防止显式 `end(P)` 在登记与工作绑定之间封闭 P 后仍放行截图。拒绝本次 P 自引用，优先识别已封闭 P，并保留既有重复 P 摘要。查验活动会话、原租期、App/模式、设备、普通接入屏障、请求未取消以及工作位空闲之后，才把 P 绑定到目标会话 origin，固定会话 epoch、显示 ID/代次和几何快照，设置 `WorkSnap`。并发冲突立即 `409 device_busy`。接纳拒绝的 P 不获得会话归属，一个 P 只派发一次。
2. **读屏。** 工作 context 继承 HTTP 请求，整体工作上限建议固定 25 秒。锁外 `ProbeDisplay`（至多 5 秒）取得目标逻辑屏的实时 SFID。按该 SFID 调用现有 `CaptureImage`（至多 10 秒），源图读完后再探同一逻辑屏（至多 5 秒）确认 SFID。缺失或不匹配即丢图。复用 `readImageForAcquisition`、`encodeImage` 和固定设备截图链路。完整流程最多调用一次 `screencap` 和一次 JPEG 编码，层级 RPC、树构建和差异计算不参与。两次探测能检出观测到的屏幕身份变化，中间 Android 像素变化按普通截图理解。[截图读取与目标复核](../../../internal/engine/capture.go#L548) · [Android 截图](../../../internal/android/device.go#L589)
3. **编码与提交。** 沿用现有 PNG 输入界限、最长边 1200、JPEG quality 80 和 512 KiB 上限。编码器比对源 PNG 的宽高与会话快照。锁外完成编码和载体准备后，在一段短锁内核对 P 仍运行、工作位仍由该 P 持有、原会话 epoch、App/模式、STOP、原租期、请求及工作期限、会话绑定显示 ID/代次和快照字段，另查源图起点距离提交不超过 `CaptureMaxAge`（15 秒）。复用 `finishOperation` 的锁内部分完成终态，锁内仅保存操作事实。STOP/撤权、断连或截止若先于该提交判定，则丢弃候选。提交先通过时 P 保持成功，随后断连或回包丢失也只影响本次交付。`observe` 与 `wait_for` 的 3 秒继续用于有树时的图树跨度。[工作授权](../../../internal/engine/operations.go#L435) · [图像几何检查](../../../internal/imaging/imaging.go#L89) · [图片默认参数](../../../internal/engine/engine.go#L184)
4. **取消与收尾。** MCP 分别表达预分配 P、取消是否发 `end` 和公开只读注解。`snap` 的取值依次为真、假、真。`server.AddTool` 只为 `snap` 设置 `Annotations.ReadOnlyHint`，对部署端 `tools/list` 加独立的必过断言，目录指纹算法继续覆盖工具名、说明和 schema。请求取消只传播到此次读取。若适配器进入取消分支，即使已拿到提交结果也清掉图片元数据和 `ImageContent`，返回 `call_cancelled`。正常路径在读取退出时释放工作位，底层未及时返回时现有工作期限或退役交接负责脱离。显式 STOP/`end` 先撤会话权、取消读取并触发独立收尾，原工作的逻辑交接上限 5 秒。已派发 Android 命令是否停止由设备链路事实决定。[适配器取消分支](../../../internal/mcp/server.go#L184) · [工作冻结](../../../internal/engine/retirement.go#L175)

已登记的 `snap` P 完成后统一显示 `operation_state=finished`，保留以下 `phase` 和受控错误。`status(P)` 与同 P 重放返回原操作摘要，读取即使失败也没有动作 `outcome`。

| 分支 | `phase` | 操作事实 |
| --- | --- | --- |
| 成功或成功提交后响应丢失 | `finished` | 成功，无 `error`。只首次响应可交付图片。 |
| 引用、普通屏障、设备忙等接纳拒绝 | `admission` | 沿用既有错误族。原租期已到返回 `409 session_expired`。未接纳的 P 不绑定会话。 |
| 请求取消先于提交 | `cancelled` | 只记录本次读取取消，无服务端 `error`，MCP 投影 `call_cancelled`。 |
| 25 秒工作截止先于提交且会话仍获授权 | `work_deadline` | `503 image_capture_failed`，消息说明整次读取超时。 |
| STOP、显式 `end`、会话撤权或租期在读取中失效 | `revoked` | `409 control_revoked`。 |
| 采集、检查或编码失败 | `failed` | 探测目标不可用或 SFID 不一致为 `503 display_unavailable`，会话显示绑定变化为 `409 stale_screen`。截图失败、编码失败、编码超额、图超龄分别为 `503 image_capture_failed`、`image_encode_failed`、`image_too_large`、`image_stale`。阶段超时使用该阶段错误。 |

读取结束与通用工作冻结复用同一套 `snap` 终结判据。尚未提交时，先判会话撤权与租期，再判请求取消和本工具截止，最后处理阶段错误或成功。冻结只影响仍运行的 P。旧 goroutine 解阻后使用冻结摘要，禁止覆盖终态或释放新工作位。繁忙时沿用同归属才公开 `blocking_operation_id` 的规则。`snap` 的终态省略工具级 `recovery`，传输契约不匹配沿用现有 `update` 指引。`status(P)` 仍可投影原会话的收尾事实。工作相态使用 `snap.capture`、`snap.image`、`snap.commit`，控制 App 显示「正在截图」及对应阶段。诊断沿用既有操作记录。

## 6. 实现落点

| 路径 | 改动 |
| --- | --- |
| `internal/engine` | `Snap`、原子接纳与终态提交，复用图片和撤销链路。补工作超时冻结、状态与恢复分支。 |
| `internal/protocol` / `httpapi` / `client` | 严格请求解码和编码、顶层 `ImageMeta` 结果解码及字段顺序、HTTP 预留和写出、统一图片解码。 |
| `internal/mcp` / `scripts/mcp-probe` | 注册、说明、取消与成功条件、元数据文本投影、标准 `ImageContent`。更新 `catalog_test`、`result_parity_test`、探针 `checks.go` 的工具名及目录断言。若 `verify-m5.ps1` 仍用于现行验证，一并更新固定计数。 |
| `android-control` / `docs` / `release` | 控制 App 工作类别与相态文案。接口、客户端、控制协议、MCP、集成、运维、手机操作、架构和发版文档按实际变更同步，核对 README 与仓库说明的工具计数。版本号从唯一版本源生成，清单从构建生成。 |

## 7. 能力边界与成本

图像输出遵守系统捕获和编码能力，受保护窗口可能只呈现系统允许的内容。`snap` 交付单帧视觉证据，不能用来等待像素变化、分配输入目标或证明实时旋转。两次 SFID 探测只证明探测点的表面身份一致。源图宽高发生变化时编码校验可拒绝，同尺寸的旋转变化可能无法检测。实时旋转若成为需求，需要引入独立的实时几何证据。预期省下的是取树、构树、算差异和传树的成本。截图、显示复核、JPEG 编码及单工作位占用照常发生，具体延迟以复测为准。

## 8. 验证计划

### 自动验证

- 正常入口：严格解码经 HTTP/MCP 一致拒绝错误形状。验证单次源图读取和单次编码，层级读取、输入派发、revision、基线、当前 O 及 `expires_at` 无改动。覆盖 `O`、`P`、旧引用、异主、过期租期、同 P 并发重放与 `status(P)` 摘要，确认图片只交付一次。
- 边界：模拟 SFID 更换、会话显示代次在编码期间变化、源 PNG 宽高与快照不符、源图超龄、编码失败或超额、设备忙、投递忙。分别控制 STOP、请求取消、截止先于成功提交，以及提交先于这些事件的顺序。重放 `end(P_current)` 先封闭和 `snap` 先接纳两种情况。阻塞编码并触发 STOP，验证 STOP 先返回，解阻后拒绝交付。反向对照去掉提交门禁应导致晚到截图用例失败。MCP 取消用例须验证 `end` 调用为零、原会话仍活动，且竞态结果无图片。
- 投影与回归：HTTP/SDK 互通，MCP 三种结果模式各只有一个 JPEG 图块、元数据一致且正文无 base64，拒绝无效顶层图片与两个图片载体并存。验证失败和重复 P 的无图摘要。复测 `observe(image=true)`、`wait_for(image=true)` 和 `act(image=true)` 的图、树、动作语义，验证 App 新工作状态和目录探针。运行覆盖变更包的 Go 测试、相关 Java 检查和 linux/arm64 构建。

### 最小实机闭环

- 以替换后的主机探针连接手机 HTTP MCP，确认 `tools/list` 有 13 个工具，`snap.annotations.readOnlyHint=true`，目录指纹与两份清单相符。使用受控会话运行 `start → keepalive(duration_minutes=1) → snap(reference=O) → status(O/P) → observe(image=true) → end`。一分钟租期能区分误触发的普通两分钟续租。核验 JPEG 可解码且尺寸与元数据一致，当次成功结果仅带 P 和图，原 O 仍为 `ready`/`effective`，`expires_at` 等于显式续租后的值，`status(P)` 仅给摘要，带图 `observe` 仍发布新 O，最终 `end` 的撤权与收尾分别可查。O 和 revision 的直接差值由引擎用例断言。
- 通过新主机 `device-mcp.exe` 启动临时独立 stdio 探针，复核 `tools/list` 与 `snap` 图片交付。三种结果模式、重复 P 的源图调用计数、取消和 STOP 的精确竞态由自动验证提供确定证据。实机若能可靠观察到截图进行中，可以附做一次显式终止，记录是否实际命中该窗口。外部依赖 Agent 的重连由用户自行安排。
- 记录实际命令、哈希、受控元数据、实机结果和未执行项，写 `maintenance/verification/snap.md`。原始图片、树、日志正文和凭据留在运行环境。

## 9. 审阅点

名字 `snap` 已确认。建议按 §4 的顶层 `image` 契约、§5 的只读与取消语义实施，图像 `screen` 使用会话显示快照，实机按 §8 的最小闭环验收。此处由用户审阅并确认技术方案，批准后进入实施。

## 10. 实施与发布顺序

1. 按 §6 实现，跑自动验证和反向对照。建议版本为 `0.19.0`、`contract_version=19`、Android `versionCode=21`，按实施时版本顺序复核。只手改 `release/versions.json`，使用 `scripts/generate-version.ps1` 生成常量。同步正式文档，取得独立只读文档审计结论，再提交实现。
2. 在干净实现提交运行 `scripts/build-release.ps1`，再运行 `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`。用 `scripts/deploy.ps1 -SkipBuild` 把控制 App、设备服务、固定制品和手机 MCP 适配器整组覆盖部署，保留 App 私有数据、配对与账本。核对 APK 安装身份、已部署及运行制品、服务 release/contract、`can_start`、控制同步和手机 MCP 运行状态。[发布与部署流程](../../../docs/release.md#4-部署顺序)
3. 以同次手机发布清单为配套运行 `scripts/build-host-tools.ps1` 与 `scripts/build-host-tools.ps1 -VerifyOnly`。确认 `build/device-mcp.exe`、`build/mcp-probe.exe` 已替换，两份清单的提交、版本、目录指纹及各自产物大小和 SHA-256 匹配。完成 §8 的实机闭环并留存验证记录。
4. 按流程移动提案并单独提交归档。如需在归档 HEAD 上出包，两份清单从同一干净提交成对重建复核，手机和主机产物以最终匹配清单为准。[归档流程](../../process.md#7-归档)

## 11. 实现结果

实现提交：`de03df9f60ae67881b6397fda772566bfcfb6779`（`master`，2026-11-18）。
验证记录：[snap.md](../../verification/snap.md)。

### 11.1 已落地

| 提案条目 | 代码落点 |
| --- | --- |
| §4/§5 原子接纳：P 登记/去重、原引用解析、活动会话/租期/App/模式/设备/屏障/工作位在同一状态锁内完成；拒绝自引用；成功才绑定会话 origin 与显示快照 | `internal/engine/snap.go` `admitSnap`；`internal/engine/operations.go` `registerOperationLocked` |
| §4/§5 读屏：5 秒探测、一次源图读取、5 秒复核 SFID、一次 JPEG 编码；不读树、不发输入 | `internal/engine/snap.go` `runSnap`；`internal/engine/capture.go` `readImageForAcquisitionOnDisplay`/`encodeImageForDisplay` |
| §4/§5 提交门禁与单次交付：锁内重验 P/工作位/epoch/App/模式/STOP/租期/请求与工作期限/显示 ID/代次/几何与 15 秒源图龄；成功只返回 `operation_id` 与顶层 `image` | `internal/engine/snap.go` `commitSnap`、`snapUncommittedOutcomeLocked` |
| §5 未提交终态与冻结：会话撤权/租期 → 请求取消 → 工具截止 → 阶段错误；晚到结果只重放冻结摘要；`snap` 终态省略工具级 `recovery` | `internal/engine/snap.go`、`internal/engine/retirement.go` `freezeSnapLocked`/`applyFrozenResult`、`internal/engine/operations.go` `finishOperationLocked` |
| §4 HTTP 请求/结果：8 KiB、严格解码、顶层 `image` 在 `saved` 后 `session` 前、`/api/snap` 与 1 MiB 带图投递预留 | `internal/protocol/requests.go`、`internal/protocol/encode.go`、`internal/protocol/responses.go`、`internal/protocol/decode_response.go`、`internal/httpapi/server.go` |
| §4 Go client：`Client.Snap`、顶层 `image.base64` 解码、必填元数据/形状校验、无图摘要可解码 | `internal/client/client.go` `Snap`/`decodeResponseImage`/`decodeSnapImage` |
| §4 MCP：第 13 工具、唯一 `ReadOnlyHint`、预分配 P、取消不发 `end` 且清图、三模式单 `ImageContent`、`isError` 要求实际图片 | `internal/mcp/schema.go`、`internal/mcp/server.go`、`internal/mcp/result_value.go`、`internal/mcp/result_text.go`、`internal/mcp/catalog.go` |
| §5 控制 App：`snap` 工作类别“正在截图”、`snap.capture/snap.image/snap.commit` 阶段文案 | `android-control/app/src/main/java/com/mobileuse/vnext/ControlState.java` |
| §6/§8 探针与目录：13 工具、snap 图片检查、只读注解断言、目录指纹 | `scripts/mcp-probe/checks.go`、`scripts/mcp-probe/result.go`、`scripts/mcp-probe/catalog.go`、`scripts/verify-m5.ps1` |
| §6/§10 版本与正式文档：0.19.0 / contract 19 / Android versionCode 21；接口、客户端、控制协议、MCP、集成、运维、手机操作、架构与发版文档、README | `release/versions.json`、生成常量、`docs/*`、`README.md` |
| §8 自动验证：engine/HTTP/MCP/client/探针用例 | `internal/engine/snap_test.go`、`internal/mcp/snap_test.go`、`internal/protocol/protocol_test.go`/`encode_test.go`/`decode_response_test.go`/`surface_test.go`、`internal/client/client_test.go`、`internal/httpapi/admission_test.go`、`scripts/mcp-probe/*_test.go` |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 提交门禁的显式工作位比较 | 已落地。除提案列出的 P/会话/显示/期限条件外，`snapUncommittedOutcomeLocked` 还显式要求 `e.work == work`，避免工作位被替换后仍提交；与 §5.3 的“工作位仍由该 P 持有”一致。 |
| 版本取值 | 与提案建议一致：0.19.0、`contract_version=19`、Android `versionCode=21`；实施时按 0.18.0/18/20 的实际版本顺序复核。 |
| 实机 STOP/取消的精确竞态 | 未执行。snap 读取窗口很短，未能可靠命中；由 `internal/engine/snap_test.go` 覆盖读取中 STOP 丢弃图片、取消不发 `end`/不交付图片/不撤销会话，以及编码阻塞时的冻结与晚到结果。 |
| 实机 SFID 更换、显示代次变化、源图超龄、编码失败 | 未执行。需要可控注入设备链路；由合成夹具覆盖，实机只验证正常路径。 |
| 实机截图进行中的控制 App 文案 | 未执行。未取得稳定窗口；Java 映射按受控 kind/phase 文案实现。 |
| 外部 Agent/MCP 宿主重连 | 未执行，按提案 §8 由用户自行安排；仓库内主机工具、手机适配器与探针已验证。 |
| 部署后首次 `start` 的 `control_revoked` | 环境事实：与 `1019c35` 记录的 App 停止待同步重试一致；同一探针重跑 PASS，未改代码或数据。 |
| 手机适配器 Origin | 为运行 http-remote 探针的 `origin_allowed` 检查，本次部署放行 `http://127.0.0.1:3000` 单一回环源；未使用 `*`。 |
| 固定 uiautomator JAR | 未修改，哈希仍为 `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3`。 |
