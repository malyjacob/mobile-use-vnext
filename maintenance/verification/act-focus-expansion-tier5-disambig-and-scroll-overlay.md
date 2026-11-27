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
| 已实测 | 见 §5.3（基础闭环、展开评论栏、抽屉与 `v_mask` 下的候选过滤、反误杀对照） |
| 磁盘替换 | 见 §5.1–§5.2（发布产物、整组覆盖部署与手机适配器哈希逐项 match） |
| 独立探针 | 见 §5.4（主机 stdio 与手机 HTTP 均 `RESULT PASS`） |

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

### 5.1 发布构建与清单核验（干净树 `a36497d`）

| 命令 | 真实结果 |
| --- | --- |
| `./scripts/build-release.ps1` | `release=0.38.0 contract=34 android=40 source_commit=a36497de236af159c0c34b8eab4433ad583ecef5 source_dirty=False`；工具目录 `13/5578041adf04bcd8670a95b731667b85832957de4990f49e2dbf4eae6845d9b9` |
| 产物 SHA-256 | `device-service 8408702e…`、`device-mcp 8c3ab945…`、`control.apk 502e22fd…`、`u2.jar 28361193…`（与基线相同）、`applist.dex 63818f32…`、`service.sh 4b568a9a…` |
| `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS`，`artifacts=6`、`head=a36497de…`、`source_dirty=False` |
| `./scripts/build-host-tools.ps1` | `RESULT PASS`；`device-mcp 8ea96c3b…`、`mcp-probe 01a4b66a…`、`source_dirty=False`、`contract=34` |
| `./scripts/build-host-tools.ps1 -VerifyOnly` | `PASS`，与手机发布清单同源 `a36497d`、工具目录指纹一致 |

### 5.2 手机整组覆盖部署（保留 App 私有数据）

`./scripts/deploy.ps1 -Manifest build/release/manifest.json -SkipBuild` 输出：`version match: PASS (health release=0.38.0 contract=34)`、`installed App: package=com.mobileuse.vnext versionName=0.38.0 versionCode=40`（`adb install -r`）、`App pairing: PASS`、`recovery barrier: PASS`、`STOP availability: PASS`、`phone MCP HTTP adapter: PASS sha256=8c3ab945…`，所有 `deployed artifact` 逐项 `(match)`。

如实记录一次口径修正：首次部署时手机适配器的 `allowed_origins` 未包含独立探针使用的 `http://127.0.0.1:3000`，手机 HTTP 探针在 `origin_allowed` 得到 `403`；按部署脚本参数 `-McpAllowedOrigins 'http://127.0.0.1:3000'` 重部署后复跑 PASS。该参数此前几次发布同样使用，属部署参数遗漏而非代码缺陷。

### 5.3 最小实机闭环（虚拟屏，`display_id=287/292`）

| 场景 | 结果 |
| --- | --- |
| 基础闭环 | `start` → `O1332_1`（`display_id=287`）→ `shell` 启动探针 App → `observe` 得 15 节点树 → `act(text, ref=n6, auto_submit=false)` → `executed`，delta `n6 {focused:true, value:"契约34实机验证"}` |
| 聚焦复查事件 | 服务日志：`engine.text.focus operation_id="P34text1" mode="near" attempt=1 outcome="proven" ms=239`（首帧绑定、单次 Tap） |
| 展开评论栏（小黑盒） | 帖子详情页 `n173` 为底栏左侧半宽输入胶囊（中心 `x=363, y=2290`）；`act(text, ref=n173, auto_submit=false)` → **单次 `executed`**；delta：`n173 bounds 由 363,2290 → 540,2096`（全宽展开并上移）、`focused:true`、`value:"契约34展开验证"`，同时新增 `n200 view resource="v_mask"`、编辑器工具条（`iv_expression`/`iv_pic`/`iv_at`/`iv_game`/`iv_cy`）与 `发送` 按钮；日志 `engine.text.focus operation_id="P34text2" mode="expanded" attempt=1 outcome="proven" ms=318`。基线（契约 33）在同一路径返回 `409 text_target_uncertain (stage=focus, reason=focus_unconfirmed)` |
| `v_mask` 下的滚动候选 | 同帧对顶部标签 `n159`（非滚动）请求 `scroll(up, half)` → `422 unsupported_node_action (reason=not_scrollable)`，**`candidates` 整体省略**（`v_mask` 下的背景滚动容器被排除） |
| 侧边抽屉（网易云音乐，Issue #48 原始场景） | 打开抽屉后 `scroll(n250 抽屉列表, down, half)` → `422 no_safe_trajectory (reason=trajectory_blocked)`，**`candidates` 整体省略**（原 `n7405`/`n7416` 类底层页面容器不再出现） |
| 抽屉内非滚动控件 | 抽屉顶栏控件 `scroll(n246, half)` → `422 unsupported_node_action (reason=not_scrollable)`，候选只剩抽屉自身列表 `n250` 且 `executable=false`，不回退底层页 |
| 反误杀对照（同一设备、关闭抽屉） | 正常页面 `scroll(n193, up, half)` → `422 not_scrollable`，候选 `n28`/`n34`/`n42` 均 `executable=true` |
| `end` 与收尾 | `end` → `executed`，`cleanup` 由 `pending` 收敛为 `complete`，`can_start=true` |

### 5.4 双端独立探针（新产物）

| 探针 | 结果 |
| --- | --- |
| `./build/mcp-probe.exe -transport stdio -binary ./build/device-mcp.exe -config .state/mcp-config.json -result-mode text` | `RESULT PASS`：13 工具 schema/`additional_properties_false`、`status`、`result_error`、`start`（`tree=v5`、`session=virtual`）、`app` 查询与准入、`observe`/`snap`/`wait_for`/`shell`/`inspect`、`act(key=BACK)`、`end`、取消投影全部 PASS |
| `./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode text` | `RESULT PASS`：`service_remote transport=http-remote contract=34 release=0.38.0 process_managed=false` + 同一套件全部 PASS |

依赖方切换边界：本次只完成磁盘替换与独立进程验证；**托管 MCP 宿主进程未重启**（构建脚本提示旧 `device-mcp.exe` 仍在使用并保留备份），宿主重新发现 13 个工具与重连由用户处理。

## 6. Issue #48

在 GitHub Issue #48 回复完整根因、契约 34 的跨窗口等值隔离/遮罩叶/实体浮层/同前景排序规则、不改 `u2.jar` 与 `candidates` 6 字段的取舍、单测与上述实机结果，并关闭：

- 回复：`https://github.com/malyjacob/mobile-use-vnext/issues/48#issuecomment-7220548124`
- 关闭复核：`state=CLOSED`、`stateReason=COMPLETED`、`closedAt=2026-11-27T14:15:21Z`
- 推送：`a1e2c7a..a36497d master`（实现与归档提交）

## 7. 未执行与缺口

- 依赖方 MCP 宿主进程的重启与重连未执行（由用户处理）；本记录只主张“磁盘替换 + 独立进程探针”。
- 议题 2 的“真实 App 154px 卡片平移竞态”未在真机稳定构造，仅单测覆盖；实机只做了 ref 卡片/按钮点击的闭环。
- 议题 3 的多窗口（合成显示根 ≥2 顶层窗口）等值隔离未在真机构造，仅单测覆盖；真机覆盖的是单窗口抽屉与 `v_mask` 两类。
- 1C 父容器锚点兜底未在真机命中（真机命中 `expanded` 与 `near`），其正确性由单测覆盖。
