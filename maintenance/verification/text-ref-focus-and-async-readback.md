# text(ref) 解除 tap 绑定与 stage=verify 有界异步读回确认验证

实现提交 `964c93a4f2b08894db54a1fbac5b192b7a6ca941`（分支 `master`，基线 `1259cba7145d8b26536d963d7cf860ed3d3ae85c`）。Windows amd64、Go 1.27.1、`GOEXPERIMENT=jsonv2`；手机 Android 16 / SDK 36（仅 user 0）。发行 0.36.0、契约 32、versionCode 38。涉及实机：是。[归档提案](../proposals/implemented/text-ref-focus-and-async-readback.md)

分诊为契约取舍（Issue #49、#50 合并）；提案已经用户确认后实施，本轮无跳过步骤。

## 实现与单测

### 已实现（源码边界）

- **`text(ref)` 准入与聚焦**：`internal/engine/tap_scope.go` 的 `staticTextRefCheck` 删除 `ActionTap`
  硬校验，改以 `role=input`、公开动作含 `text`、非 `root`/`context`、正面积几何、未禁用、
  `protected` 明确为 `false` 判定；无落点时仍返回 `422 no_safe_point` 并把遮挡的非输入控件写入
  `blocking_refs`。`isValidTextPublicTarget` 与 `tryPlanTextDirectBase` 只按 `text` 能力判定，
  静态规划以 `protocol.ActionText` 走原有 4 级子树安全点（`bubble` 关闭、无 `coextensive_child`）。
  `collectDirectBlockingDiagnostics` 的两处 owner 分类抽成 `directBlockingCandidate`：文本用严格
  文本门，`tap`/`long_press` 仍返回原 `validTapPublicTarget`，排序、去重与首现顺序不变。
  `internal/engine/act.go` 的 `framePublicTargetError` 对非 submit 的 `text` 帧改走
  `isValidTextPublicTarget`，`submit_disabled` 分类与普通动作口径不变；`revalidateActTarget`
  把 `unique` 日志移到 `freshTextTargetSafe` 通过之后。已聚焦目标仍走 `bindFocusedText` 零点击
  快路径，未聚焦时由既有 `focusTextRef` 至多点击一次并经 `afterTap` 复核。
- **`stage=verify` 有界确认**：`internal/engine/text_submit.go` 新增
  `textVerifyDelays=[40ms,80ms,150ms]`、`textVerifyMaxElapsed=1000ms`、`textVerifyMaxRebinds=1`、
  `verifyTextWriteReadback` 与 `verifyViaPostWriteRebind`。一次 `setTextOnDisplay` 返回完整终止
  回执且 `candidateCount=1`、`issued=true` 并提交 `currentStage=verify` 后：`0ms` 刷新值精确相等
  直接完成（零额外 RPC）；否则按固定退避做只读 `getTextOnDisplay`，RPC 与退避共用同一绝对截止
  时间（`1000ms` 覆盖 RPC 本身），`workRevoked`/`ctx` 取消立即以 `CodeStopped` +
  `authorization_revoked` 收口；`candidateCount>1` 立即按多候选失败；`candidateCount=0` 仅进入
  一次写后严格重绑定（一次 `Capture` + 既有 `textSubmitCaptureTrusted`/`textSubmitDisplayCurrent`/
  `validateTapRawTopology`/`sameTextTaskContext` 守卫 + `rebindPostWriteTextTarget` + 一次只读
  读回），成功后把 `bound.raw`/`bound.capture` 更新给 `enter_key` 提交门禁。全程不发送第二次
  `SetTextOnDisplay`，不采信普通 `observe`，判据保持全文精确相等；失败仍为
  `409 text_result_uncertain` + `stage=verify` + `reason=readback_mismatch|readback_unavailable`，
  `candidate_count` 仅在已取得有效候选计数时出现。新增有界 `Debug` 事件 `engine.text.verify`
  （`operation_id`/`mode`/`attempt`/`outcome`/`reason`/`count`/`ms`，全部为既有白名单键）。
- **对外描述**：`internal/mcp/schema.go` 的 caller 指南与 `act` 描述补充两条新事实（`text(ref)`
  不要求节点带 `tap`、写后读回覆盖短时异步落地与几何变化）；因发现字节预算（40000）已接近
  饱和，未扩大预算，改为只保留这两条调用方事实，详细契约留在 `docs/`。工具目录指纹同步为
  `count=13`、`sha256=5578041adf04bcd8670a95b731667b85832957de4990f49e2dbf4eae6845d9b9`。
- **版本与文档**：`release/versions.json` 与 4 个生成文件升为 `0.36.0/32/38`；`README.md` 与 9 份
  `docs/` 同步两条契约（`text(ref)` 无 `tap` 准入与聚焦、`verify` 有界只读确认）与全部版本引用。

### 已单测（可复跑）

- 先红：`go test ./internal/engine -run '^(TestTextRefNoTap.*)$' -count=1` 在基线上 7/7 失败，
  均为 `422 unsupported_node_action` / `reason=target_has_other_action` /
  `target_actions=[text]`（8.331s）；`go test ./internal/engine -run '^(TestTextVerify.*)$'
  -count=1` 在基线上停在 `stage=verify` / `readback_mismatch`（`nil` 首值场景为
  `readback_unavailable`）。
- 实现后：#49 七用例 + 两个受影响旧子用例 PASS（11.721s）；#50 十用例 10/10 PASS（14.391s），
  三个指定旧用例 PASS（5.413s）；`text_write_evidence_test.go` 中持续 mismatch/`nil` 与三个恢复/
  状态/日志用例显式脚本化三次只读并断言 `get=3`。
- `GOEXPERIMENT=jsonv2 go vet ./internal/engine ./internal/mcp` 退出 `0`；`internal/mcp` 全包
  `go test -count=1` PASS（1.644s，含 `TestDiscoveryByteBudget` 与工具目录基线）。
- 全量单次 `go test ./cmd/... ./internal/... ./scripts/... -count=1 -timeout 900s`：除下列两项外
  全部包通过（`internal/engine` 841.400s、`internal/android` 21.309s、`scripts/mcp-probe`
  23.325s 等）。①`internal/mcp` 的 `TestDiscoveryByteBudget` 首轮因新增指南超预算
  （`41161 > 40000`），按上文收敛指南后该包全绿；②`internal/engine` 的
  `TestBaselineSelectionFallsBackWhenShortestHasNoRetentionPlan` 在同一进程长跑中一次在 capture
  阶段发布失败观察（`missing=[tree]`），单独 `-run` 重复 3 次全部通过（3.171s）；该用例不经过
  `act` 路径，本次未改动 `observe`/`settle`/`observation`，失败形态与 settle 的墙钟预算一致，
  按负载敏感抖动记录，未作为本次结论。
- `./scripts/generate-version.ps1 -Check` 输出 `version source consistent: release=0.36.0
  contract=32 android_version_code=38`。

### 已审（独立只读）

- `README.md` 与 9 份 `docs/` 由独立、只读 subagent 审计：4 项低危发现全部按最小修正采纳后复检
  `CLEAN`——`no_safe_point` 行 `text(focus)`→`text(ref)`、事件名 `text.verify`→
  `engine.text.verify`、README 准入清单补「正面积几何」、`interface.md` 不再用请求字段 `bubble`
  描述 `text` 内部规划。审计同时确认版本链（`0.36.0/32/38`）与 `release/versions.json`、生成常量、
  Android manifest 一致，`stage`/`reason`/`candidate_count` 闭集与实现逐项对应，无过程性措辞、
  无提案或验证记录泄漏。

## 发布、部署与双入口探针

从干净实现提交 `964c93a4f2b08894db54a1fbac5b192b7a6ca941` 执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins http://127.0.0.1:3000
```

- 发布清单与主机清单同源干净：`source_commit=964c93a4f2b08894db54a1fbac5b192b7a6ca941`、
  `source_dirty=false`、`release=0.36.0`、`contract=32`、`android=0.36.0/38`、
  `tool_catalog=13/5578041adf04bcd8670a95b731667b85832957de4990f49e2dbf4eae6845d9b9`；
  `verify-manifest` PASS。
- 手机产物：`device-service` `b31dc58a…` 9,568,416 B；`device-mcp` `b579eb9a…` 9,371,808 B；
  `control.apk` `085983b8…` 103,965 B；`u2.jar` `28361193…`（未改动）；
  `applist.dex` `63818f32…`；`service.sh` `4b568a9a…`。
- 主机产物：`device-mcp.exe` `25dfeb46…` 10,306,560 B；`mcp-probe.exe` `e8a81e8f…` 10,118,656 B；
  `build-host-tools.ps1` 输出 `RESULT PASS`。
- 手机整组覆盖部署 PASS（`adb install -r`，保留 App 私有数据）：安装 App 为
  `versionName=0.36.0`、`versionCode=38`；`/health` 返回 `release_version=0.36.0`、
  `contract_version=32`；配对、恢复屏障与 STOP 可用性 PASS；手机 MCP HTTP 适配器运行映像
  SHA-256 与清单一致。
- 首次部署未带 `-McpAllowedOrigins`，手机 HTTP 探针的 `origin_allowed` 按部署默认策略返回
  `403`；按既有验证参数带 `http://127.0.0.1:3000` 重新执行同一 `-SkipBuild` 部署后通过。
  这是部署参数问题，不是制品问题。
- 独立探针（新 `build/mcp-probe.exe`，同一双清单）：
  - 主机 stdio：`-transport stdio -binary build/device-mcp.exe -config .state/mcp-config.json
    -result-mode text` 为 `RESULT PASS`；13 工具、目录指纹、`start`/`observe`/`snap`/`wait_for`/
    `inspect`/`act`/`end` 与 `session_mode=virtual` 全部通过。
  - 手机 Streamable HTTP：`-transport http-remote -config .state/probe-phone-config.json
    -result-mode text -suite app-lifecycle -timeout 600` 为 `RESULT PASS`；`contract=32`、
    `release=0.36.0`、目录指纹一致，`app_kill`/`app_close`/`app_kill_package` 执行，
    `app_kill_primary_refused` 与 `app_claim` 因主屏无受控任务 SKIP（环境，不是制品）。
- 磁盘替换、独立进程验证与依赖方切换分别记录：探针启动本次主机 exe 并连接本次手机适配器
  进程；依赖 Agent 的 MCP 托管进程与重连未操作，由用户处理。部署脚本提示宿主必须实际重启  才能重新发现工具。

## 实机行为（虚拟屏会话）

会话经 HTTP 直调服务（令牌来自私有 `.state/deploy.json`，未入库），`start` 返回
`session.mode=virtual`、`display_id=275`、屏幕 `1080x2400`。受控应用为验证专用
`com.mobileuse.probe`；第二个受控目标是已登录的 `com.zhihu.android`（写回答编辑器）。所有写入
均为 `auto_submit=false`（探针的提交分支除外），未点击「发布」。

1. **同步控件走 `0ms` 快路径（已实测，探针 App）**：`com.mobileuse.probe` 前台 task 379，输入框
   `n6`（`actions=[tap,long_press,text]`，值仅提示文字）。`act(text, ref=n6,
   text="同步输入一二三 😀 test-A", auto_submit=false)` 返回 `executed`（1,474 ms 端到端），
   新观察值为精确原文（含非 BMP 字符），`focused=true`；服务日志
   `engine.text.verify mode="direct" attempt=0 outcome="executed" ms=0 count=1`。随后已聚焦二次
   写入 `text="二次聚焦写入 test-B"` 同样 `executed`、值精确、`mode=direct ms=0`；默认
   `auto_submit=true` 的 `text="自动提交 test-C"` 返回 `executed`、`text_input={write:executed,
   submit:executed}`（只发一次 ENTER）。
2. **无 `tap` 的输入框可直写（已实测，Issue #49，知乎写回答编辑器）**：编辑器正文
   `n429` 公开为 `role=input`、`actions=["text"]`（无 `tap`），初始值只有换行且未聚焦；基线上
   同一形态返回 `422 unsupported_node_action`/`target_has_other_action`。`act(text, ref=n429,
   text="诊断测试草稿：契约32 #49 异步落地验证 😀", auto_submit=false)` 返回 **`executed`**：
   引擎在目标子树内规划安全点并至多点击一次聚焦，经 `afterTap` 复核后写入一次。新观察值精确
   等于原文（含非 BMP），`focused=true`，节点引用由 `n429` 变为 `n470`。
3. **异步落地由有界只读读回确认（已实测，Issue #50）**：同上一次写入的服务日志为
   `engine.text.verify mode="poll" attempt=1 outcome="executed" ms=98 count=1` —— `0ms` 刷新值
   不等，第一次 `40ms` 退避后的只读读回在约 98ms 确认，单次 `act` 内返回 `executed`，无需额外
   `observe`。已聚焦的二次写入 `text="二次写入：已聚焦零点击路径"` 同样 `executed`
   （`mode=poll attempt=1 ms=80`），值精确。
4. **写后撑高触发单次严格重采集（已实测，T2 分支）**：向同一输入框写入 128 字、5 行、含
   `😀🚀` 的文本，返回 `executed`（1,741 ms 端到端），日志
   `engine.text.verify mode="rebind" attempt=1 outcome="executed" ms=370 count=1`：写后
   `getTextOnDisplay(旧目标)` 返回 `candidateCount=0`，引擎执行一次严格写后重采集与唯一绑定，
   再以一次只读读回确认；观察值精确等于含换行的原文，输入框几何中心由 `(540,540)` 变为
   `(540,1280)`，`focused=true`。
5. **零 ENTER、零误发（已实测）**：上述三次知乎写入均为 `auto_submit=false`，结果不含
   `text_input`，服务日志无提交原语，未点击「发布」。测试过程中知乎自动保存了一篇草稿；
   退出时选择「不保存并退出」后仍在草稿箱生成一篇，遂进入草稿箱「管理」，仅选中本次
   「刚刚」一篇删除（未发布回答草稿计数 4→3），`1 天前`/`1 年前` 等既有草稿未改动。最终
   `end` 返回 `cleanup=pending`，随后 `status` 为 `can_start=true`、
   `cleanup={state:complete, execution:confirmed, tasks:confirmed, display:confirmed}`。

## 未执行项与原因

- 未实测「无 `tap` 输入框 100% 被可点击子控件覆盖 → `422 no_safe_point` + `blocking_refs`」与
  「部分遮挡时落点绕开遮挡」的实机形态：受控探针没有可点击子控件覆盖的输入框，知乎正文也
  不是全遮挡；由 `TestTextRefNoTapFullyCoveredReportsBlockingButton`、
  `TestTextRefNoTapPartialGapFocusesInsideTarget` 与两个既有子用例覆盖，未写成实机结论。
- 未实测真实格式化/截断负例（`readback_mismatch` 保持 `uncertain`）：受控探针无格式化页面，
  知乎编辑器未复现格式化；由 `TestTextVerifyFormattedValueStaysMismatch` 与
  `TestTextVerifyMultiCandidateStopsImmediately`、`TestTextVerifyRebindNotUniqueStaysUncertain`
  覆盖。
- 未实测 STOP 打断退避的实机时序：由 `TestTextVerifyStopDuringBackoff` 的确定性取消覆盖（实机
  STOP 通道本身已在部署 PASS 与探针 `cancel` 中验证）。
- 未执行 `-race`：Windows 宿主无 C 编译器、cgo 不可用。
- 依赖方 Agent 托管的 MCP 进程与重连未操作：本次交互式 MCP 工具仍指向旧进程（`start` 返回
  `409 contract_mismatch`），验证改用 HTTP 直调与独立探针；宿主必须实际重启才能重新发现
  13 个工具。部署脚本两次提示手机 MCP stdio 旧映像可能仍被其他进程占用。

## 归档 HEAD 的发布包复建

在归档提交 `5555936dc43b2d68bc2f59533b0b6c33ec922517`（工作树干净）上重新执行
`build-release.ps1`、`verify-manifest.ps1` 与 `build-host-tools.ps1`：

- `device-service` `b31dc58a…`（9,568,416 B）、`device-mcp` `b579eb9a…`（9,371,808 B）、
  `u2.jar` `28361193…`、`applist.dex` `63818f32…`、`service.sh` `4b568a9a…` 与实现提交构建
  **逐字节一致**；主机 `device-mcp.exe` `25dfeb46…`、`mcp-probe.exe` `e8a81e8f…`、目录指纹
  `13/5578041a…` 同样一致；`verify-manifest` 与 `build-host-tools` 均 PASS
  （`source_commit=5555936…`、`source_dirty=false`）。
- `control.apk` 由 `085983b8…` 变为 `500c4d4b…`，字节数同为 103,965 B：APK 构建含签名与打包
  时间戳，不是逐字节可复现产物；两个归档提交之间源码差异仅为 `maintenance/` 文档。
- 手机上的部署来自实现提交清单（`source_commit=964c93a…`、`control.apk=085983b8…`），部署
  脚本按该清单核验了 chroot 产物哈希与安装后的 App `versionName=0.36.0`/`versionCode=38`；
  归档 HEAD 复建未重新部署，因为 Go 与固定制品哈希未变、APK 仅签名时间戳不同，重新安装不
  增加证据。当前 `build/` 下的清单以归档提交为上界记录，后续归档记录提交只改 `maintenance/`。

