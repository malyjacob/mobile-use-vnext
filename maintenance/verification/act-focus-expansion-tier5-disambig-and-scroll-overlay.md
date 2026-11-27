# 验证记录：展开聚焦绑定、Tier 2/3 歧义转 Tier 5 与 scroll 遮罩感知候选（契约 34）

实现提交：`7f8ef834078dfd097f32c1f8ab59b0f754b6445f`。
归档提案：[act-focus-expansion-tier5-disambig-and-scroll-overlay](../proposals/implemented/act-focus-expansion-tier5-disambig-and-scroll-overlay.md)。
**涉及实机**：本次涉及发布交付；手机整组覆盖部署、最小实机闭环、Windows 主机双产物替换与双端独立探针在归档提交之后执行，结果补记于 §5–§6。

结论分层（不得互相替代）：

| 层次 | 结论 |
| --- | --- |
| 已实现 | 提案 §4.1–§4.3 三项全部落地，落点见提案 §11.1 |
| 已单测 | 新增 3 个测试文件、10 个测试函数、22 个子用例；先红与转绿证据见 §2 |
| 已全量 | `internal/engine` 整包 `885.736s` 通过；非 engine 15 包全部 `ok`；交叉编译通过（§3） |
| 已实测 | 待交付阶段补记（§5） |
| 磁盘替换 | 待交付阶段补记（§5） |
| 独立探针 | 待交付阶段补记（§5） |

---

## 1. 环境与构建方式

- 主机：Windows，`go1.27.1 windows/amd64`，所有 Go 命令显式设置 `GOEXPERIMENT=jsonv2`；发布交叉编译为 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`。
- 单测在开发树执行；发布构建在归档提交后的干净树执行（未跟踪文件会让发布脚本判定 `source_dirty=true`）。
- 版本源：`release/versions.json` 手改为 `0.38.0` / `34` / `40`，`scripts/generate-version.ps1` 重写四个生成文件。

## 2. 单元测试

### 2.1 新增用例

| 文件 | 测试函数 | 覆盖 |
| --- | --- | --- |
| `internal/engine/text_focus_binding_test.go` | `TestTextFocusExpandedOwnAnchor` | 1B-A：半宽折叠框（`resource-id` 与 `description` 两种锚点）点击后横向展开为全宽并纵向位移，`mode=expanded` 绑定并一次 `executed`；反向对照：宽度大幅缩窄（`900→500`）与水平完全无交叠的异框拒绝写入 |
| 同上 | `TestTextFocusExpandedParentResourceAnchor` | 1C-A：匿名折叠框嵌套在双帧唯一 `resource-id` 父容器（含透明包装）下展开，且点击后由单窗口切换为含软键盘的多窗口合成根，`mode=parent_anchor` 一次 `executed`；反向对照覆盖父容器不唯一、父为窗口根、父为可滚动祖先、父下 2 个同类 `EditText`、`noAnchorNearCount(fresh) > 1`、缩窄、屏幕存在第 2 个安全聚焦框、零点击跨帧指针不启用 |
| 同上 | `TestTextFocusBoundedRecheckAndStop` | 1-Poll：ref 与 point 两条路径首帧未聚焦、第 2 帧获焦后一次 `executed`（`tap == 1`，日志 `mode` 分别为 `exact` 与 `point`）；`Capture` 报错与前台切换 0ms 额外等待立即 `focus_unconfirmed`；退避期间 STOP 立即 `409 control_revoked`、保持整体 `uncertain`、日志 `attempt=1`/`outcome=stopped`、1 次 Tap、0 次写入 |
| 同上 | `TestTextFocusLogWhitelistAndAttemptLimit` | `engine.text.focus` 仅含 `service_instance` 与六个白名单键，不出现请求文本、`ref`、`resource`、`description`；三帧后收口 |
| `internal/engine/scroll_overlay_test.go` | `TestScrollOverlayRectUnionArea64` | 整数扫描线精确并集（重叠子矩形不重复计数） |
| 同上 | `TestScrollStaticCandidatesCrossWindowIsolationOrderIndependent` | 3-O1：合成显示根下两种顶层窗口顺序（弹窗在前/主窗口在前）均排除异窗滚动容器；弹窗内自带点击遮罩叶时坐标 `no_scroll_container` 在两种顺序下同样排除 |
| 同上 | `TestScrollOverlayOccludedCandidatesExcluded` | 3-O2O3：抽屉列表 `no_safe_trajectory` 排除全部底层容器；抽屉顶栏纵滑/横滑不回退底层页；前序 `v_mask` 排除后序容器；同抽屉内第 2 个未遮挡容器按同前景优先；反误杀对照（空心 HUD 包装层、前序视频播放器容器、底部 miniPlayer）保持 `executable=true` |
| `internal/engine/act_revalidation_tier5_disambig_test.go` | `TestRevalidateTier2AmbiguousContainerDisambiguatesInTier5` | 2B-A：3 张同构无文案卡片上移 154px、贴边短卡重叠 ≥50%，基线 Tier 2 歧义，实现后 Tier 5 唯一命中 |
| 同上 | `TestRevalidateTier23AmbiguousSafetyGuards` | 六条安全边界：有自有文案/无子叶子仍立即歧义；Tier 1 歧义不进 Tier 5；禁用平移与 `actMatchScroll` 仍歧义；Tier 5 命中后外部遮挡拦截 `scope_blocked`；子树文案相同仍歧义；Tier 3 歧义转 Tier 5 零命中归因 `target_missing` 且无 `changed_ref` |
| 同上 | `TestRevalidateTier5DisambigAdmissionPredicate` | `canDisambiguateViaTier5` 准入矩阵（10 行） |

### 2.2 先红证据（实现前，基线代码上的真实失败）

| 用例 | 基线失败断言（原文摘录） |
| --- | --- |
| 1B-A（`resource-id` 与 `description`） | `want expanded focus and executed, got outcome="uncertain"`，原因 `focus_unconfirmed` |
| 1C-A | `want parent_anchor focus and executed, got outcome="uncertain"`，原因 `focus_unconfirmed` |
| 1-Poll（ref / point） | `want exact focus and executed, got outcome="uncertain"`、`want point focus and executed, got outcome="uncertain"`；采集错误/前台切换子例 `event "engine.text.focus" was not recorded`；STOP 子例 `act finished before STOP during focus backoff` |
| 2B-A | `match state = ambiguous after disambiguation, want unique` |
| 2B 安全边界 ④（两组） | `state = ambiguous at tier 0, want unique Tier 5` |
| 2B 安全边界 ⑤ | `state = ambiguous at tier 2, want ambiguous at tier 5` |
| 2B 安全边界 ⑥ | `state = ambiguous at tier 3, want none at tier 5` |
| 3-O1（两种窗口序 × 3 出口） | 6 例均为 `candidates = … "executable":true …, want candidates omitted` |
| 3-O2O3（抽屉列表） | `candidates = … n3(true), n4(true), n5(false), n6(false) …, want candidates omitted` |
| 3-O2O3（抽屉顶栏纵/横滑、同前景排序、前序 `v_mask`） | `want only front list n9` / `want candidates omitted` / `first candidate = n3, want same-drawer scroll n12 (candidates [n3 n4 n12 n9])` |

守卫类子例（1a/1b/2/3a/3b、空心 HUD、视频播放器前序容器、miniPlayer）在基线上即通过，用于证明实现前后这些不变式不被放宽或误伤。

### 2.3 定向运行（真实结果）

| 命令（均带 `GOEXPERIMENT=jsonv2`） | 结果 |
| --- | --- |
| `go test ./internal/engine -run '^TestTextFocus(ExpandedOwnAnchor|ExpandedParentResourceAnchor|BoundedRecheckAndStop)$' -count=1 -timeout 120s`（实现前） | 按预期先红（断言见 §2.2） |
| `go test ./internal/engine -run '^(TestTextFocus|TestScrollOverlay|TestScrollStaticCandidates)' -count=1 -timeout 300s`（主代理复核运行） | `ok … 49.256s` |
| `go test ./internal/engine -run '^Test(TextFocus|TextBindingModesAndSourceGates|TextRefNoTap|ActCoordinatePoint(Proves|Rejects)TextFocusPoint|ActText|TextCoordinateWithSubmitRef|TextSubmitRef|TextEnterRebind|TextVerify)' -count=1 -timeout 120s` | `ok … 81.979s`（含既有 `source_mismatch_horizontal_width`、`width_changed_on_shift`） |
| `go test ./internal/engine -run '^TestRevalidateTier' -count=1 -timeout 300s` | 先红 → 实现后 `ok … 1.0s` |
| `go test ./internal/engine -run 'TestActRevalidation|TestRevalidate|TestActRevalidate' -count=1 -timeout 300s` | `ok … 50.2s`（两次） |
| `go test ./internal/engine -run 'TestPointOwnerFallback' -count=1 -timeout 300s` | `ok` |
| `go test ./internal/engine -run '^Test(ScrollOverlay|ScrollStaticCandidates)' -count=1 -timeout 120s` | 先红 `15.529s` → 实现后 `ok … 19.343s`（`-v`） |
| `go test ./internal/engine -run '^Test(Scroll(Amount|Axis|Closed|Container|Content|Defensive|Dynamic|Effect|Fresh|Geometry|Log|Outer|Plan|Point|Post|Published|Ref|Rejects|Revalidate|Safe|Stop|Success|Too|Uncertain|Busy)|Issue42)' -count=1 -timeout 150s` | `ok … 109.750s` |
| `go test ./internal/engine -run '^TestProposalScroll' -count=1 -timeout 55s` | `ok … 20.184s`（含 competing/half-screen） |
| `go test ./internal/engine -run '^TestScrollOverlayOccludedCandidatesExcluded/folded_windows_no_scrim$' -count=1` | `ok … 2.139s` |
| `tap_static_error` 定向 21 项滤选（`-timeout 75s`） | `ok … 20.486s` |

定向运行由主代理与实现子代理分别在其范围内执行；主代理复核了各次 `-run` 的选择表达式与断言差异，并另行运行了 8 个新增测试函数的合并选择（`49.256s`）。

**测量口径说明**：一次合并套件在 Windows harness 的 per-fixture 清理开销累计到 160s 被 `go test` 墙钟截断，按 `-run` 拆为 4 组后全部通过；该截断是测量口径问题，不是代码缺陷。语义与断言未因拆分而改变。

## 3. 全量测试与静态检查

| 命令 | 真实结果 |
| --- | --- |
| `go build ./...` | exit 0，无输出 |
| `go vet ./...` | exit 0，无输出 |
| `gofmt -l`（改动的 3 个生产文件 + 5 个新增文件） | 无输出 |
| `go test ./internal/engine -count=1 -timeout 1000s` | `ok  github.com/malyjacob/mobile-use-vnext/internal/engine  885.736s` |
| 非 engine 15 包 `go test -count=1 -timeout 900s` | 全部 `ok`：`cmd/device-mcp 2.034s`、`internal/android 22.719s`、`internal/client 5.504s`、`internal/device 5.352s`、`internal/eventlog 0.084s`、`internal/httpapi 4.644s`、`internal/imaging 11.534s`、`internal/mcp 8.190s`、`internal/observation 7.171s`、`internal/protocol 3.243s`、`internal/store 4.379s`、`internal/treetext 2.367s`、`scripts/mcp-probe 23.226s`；`cmd/device-service` 与 `scripts/toolcatalog` 无测试文件 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | exit 0 |
| `./scripts/generate-version.ps1 -Check` | `version source consistent: release=0.38.0 contract=34 android_version_code=40` |

## 4. 文档审计

按 `AGENTS.md` 第 15 条由独立、只读 subagent 审计本次涉及的 `docs/` 与 `README.md`。两轮：

1. 首轮 `FINDINGS` 4 项，全部采纳并按实现修正：①遮罩叶排除方向的主语写反（`docs/interface.md`、`docs/architecture.md`）；②1C 父锚点选择多写了“同类”（实际只按显示/包/非空 `resource-id` 选锚点，类名只进入锚点键）；③1C 前置条件误写为“双帧各恰有 1 个匿名框”（实际只要求原帧 1 个、新鲜帧 >1 拒绝）；④`expanded` 的“Y 轴位移不受 ±20% 约束”表述不准确（纵向位置本就不参与水平带判定）。
2. 复检又发现 1 处重复门禁句，删除后复检 **CLEAN**；`docs/mcp.md` 无版本字面量与语义漂移，未改。

审计过程记录：首次文档审计 subagent 与文档同步 subagent 均出现“无关闭消息、无响应”的失败；按第 21 条记录原因后，文档审计收紧范围并降低思考预算重发，文档同步交由主代理逐项复核其落盘成果（版本 `-Check`、全量残留扫描、语义逐条对照）。未发现缺失项。

## 5. 实机部署、最小闭环与双端探针

（交付阶段执行后补记：手机整组覆盖部署、虚拟屏基础闭环、展开输入框 `text(ref)`、Issue #48 抽屉/遮罩 `scroll` 候选、列表卡片 `ref tap` 复验、Windows 双产物替换、主机 stdio 与手机 HTTP 独立探针。）

## 6. Issue #48

（交付阶段执行后补记：回复根因、契约 34 方案取舍与验证结论，关闭并复核 `CLOSED`。）

## 7. 未执行与缺口

- 归档提交时尚未执行：整组覆盖部署、实机最小闭环、主机双产物替换、双端独立探针、推送与 Issue #48 关闭（见 §5–§6，交付阶段补记）。
- 议题 2 的“真实 App 154px 平移竞态”与议题 3 的“真机侧边抽屉/遮罩”在单测中已稳定复现；实机部分只验证可稳定构造的场景，无法稳定构造的差异在 §5 如实区分“已单测”与“已实测”。
