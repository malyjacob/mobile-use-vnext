# act 静态目标拒绝分类：实现与验证记录

状态：2026-11-18。实现提交：`a02d305f29f27fdfe6b63faf356116e137432c1a`。
本记录覆盖单元测试、交叉构建、版本源、文档审计，以及 2026-11-18 在设备 `17efb04a`
上执行的整组构建/核验/部署、契约边界、受控静态错误样本和主机/手机 MCP 探针。外部
MCP 宿主进程未重启，见 §6；不把该缺口写成已刷新。提案为
[act-static-error-taxonomy.md](../proposals/implemented/act-static-error-taxonomy.md)。

## 1. 分诊与范围

分诊：取舍。基线行为与契约 16 一致，本次改变公开错误面，不按实现缺陷处理。范围限
`tap`/`long_press` 的 `target.ref` 认领前静态拒绝：

- 新增 `422 invalid_target`（`root`/`context`/`no_geometry`）；
- 收窄 `422 unsupported_node_action`（`target_missing_action`/`target_has_other_action`/
  `ancestor_missing_action`/`ancestor_invalid`/`no_interactive_ancestor`）；
- 新增 `422 no_safe_point`（`blocked`/`ambiguous`/`owner_mismatch`/`mixed`）；
- 新增受控 details：`reason`、`target_actions`、`ancestor_ref`、`ancestor_actions`、
  `scope`、`scope_ref`、`occluding_refs`、`ambiguous_refs`、`owner_refs`、
  `suggested_point`、`suggested_carrier_ref`；
- 该静态出口以 `occluding_refs` 取代 `occluding_ref`。

未改变：候选顺序、落点谓词、`tapFrame`、claim/重验/派发、O/P、STOP、`recovery`、
坐标目标、`text`、`swipe`、`scroll`、`key`、12 个工具与输入 schema、Android 业务逻辑。
共同契约 `16 → 17`，发行 `0.17.0`，Android `version_code 19`。

## 2. 先红

先红用例位于 [tap_static_error_test.go](../../internal/engine/tap_static_error_test.go)，
全部以字符串字面量断言新 code/reason/details，因此能在旧实现上编译并按行为失败，而不是
以缺失常量代替复现。

执行：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/engine -run 'TestActTapFullyOccludedReportsNoSafePointBlocked|TestActTapAmbiguousReportsNoSafePointAmbiguous|TestActTapMixedCandidateFailuresReportBothLists|TestActTapCrossOcclusionSuggestsFirstGridPoint|TestActTapCrossOcclusionOmitsExternallyOwnedSuggestion|TestActTapTargetMissingActionReportsEmptyActionsAndGuidance|TestActTapSwipeOnlyTargetReportsTargetMissingAction|TestActTapTargetHasOtherActionReportsPublicActions|TestActBubbleAncestorStaticReasons|TestActBubbleOwnerMismatchReportsUniqueExternalOwner|TestActTapInvalidTargetReasons' -count=1
```

真实结果：`FAIL`。代表输出：

- 全遮挡卡片：旧实现返回 `unsupported_node_action`，details 仅 `occluding_ref:n3`；
  断言要求 `no_safe_point/blocked`。
- 歧义卡片：旧实现返回 `unsupported_node_action` 且 details 为空。
- 混合候选：旧实现同一 code，只有 `occluding_ref:n3`。
- 缺动作目标与祖先原因：旧实现 details 为空或只有引导键，没有 `reason`/动作数组。
- root/context/缺 bounds：旧实现返回 `unsupported_node_action`；断言要求 `invalid_target`。
- bubble 唯一外部 owner：旧实现返回 `unsupported_node_action` 且无 details。

实现后同一命令转绿；新增用例保留在仓库。

## 3. 实现落点

| 项 | 落点 |
| --- | --- |
| 新 code、422 映射、details 白名单注释 | `internal/protocol/errors.go`：`CodeInvalidTarget`、`CodeNoSafePoint`、`Status` |
| 静态入口分类与检查顺序 | `internal/engine/tap_scope.go`：`staticTapRefCheck` |
| 直接目标候选耗尽诊断 | `internal/engine/tap_scope.go`：`diagnoseTapStaticFailure`、`classifyTapStaticPoint`、`tapStaticCandidatePoints` |
| bubble 祖先范围诊断 | `internal/engine/tap_scope.go`：`planBubbleTapTarget`、`tapStaticBubbleCandidatePoints` |
| reason/details 构造 | `internal/engine/tap_scope.go`：`newTapInvalidTargetError`、`newTapStaticUnsupportedError`、`newTapNoSafePointError`、`tapPublicActions` |
| 3×3 建议点 | `internal/engine/tap_scope.go`：`tapStaticGridPoints`、`tapFiveSixths`、`tapSuggestStaticPoint` |
| ref 列表去重/排序/截断 | `internal/engine/tap_scope.go`：`tapStaticOccludingRefs`、`tapStaticOrderedRefs` |
| text 聚焦错误保持基线 | `internal/engine/tap_scope.go`：`staticTapRefCheckLegacy`、`planBubbleTapTargetLegacy`；`staticTextRefCheck` 调用前者 |
| MCP 指引 | `internal/mcp/schema.go`：`act` 描述与 `callerGuidance` |
| 文档与版本源 | `docs/interface.md`、`docs/architecture.md`、`docs/mcp.md`、README、`release/versions.json` 与生成文件 |

`internal/engine/act.go` 的分支入口与顺序未改；`chooseTapPlan`、`collectTapScope`、
`chooseBubbleTapPoint`、`provesGuidancePoint` 的通过条件与默认成本未改；fresh 重验仍返回
`target_changed`/`hierarchy_unavailable`，不生成新诊断或建议点。

## 4. 同类与共享路径核查

- **谓词等价**：`TestTapStaticDiagnosisMatchesBranchPredicate` 在健康合成 scope 上逐点比较
  直接分支与 `provesSubtreeTapPoint`、bubble 分支与 `provesGuidancePoint`；诊断“通过”与
  原 bool 谓词接受完全一致。
- **fresh 路径**：既有 bubble fresh 矩阵与 direct fresh 重验用例全部通过；静态诊断没有
  进入 fresh 分支。
- **祖先引导**：既有引导用例更新为同时断言新 `reason`/`target_actions`；原子组仍只在基线
  位置给出，失败的引导仍整组省略。
- **text 聚焦**：`staticTextRefCheck` 改用基线实现副本，text 的任何错误码、消息与 details
  不变；`internal/engine` 全量测试通过。
- **scroll 与其他工具**：`unsupported_node_action` 的既有 scroll 用法未改；`scroll_test.go`
  通过。
- **反向对照**：全遮挡用例不返回 `suggested_point`；十字遮挡叠加更深外部 owner 时
  `TestActTapCrossOcclusionOmitsExternallyOwnedSuggestion` 证明去掉整树归属校验会错误给出
  建议点；旧键 `occluding_ref` 在本次静态出口不再出现。

## 5. 实际执行与结果

环境：Windows 10（win32 10.0.19044 x64），Go 1.27.1 windows/amd64，
`GOEXPERIMENT=jsonv2`。Windows 只承担构建与开发检查。

| 命令 | 结果 |
| --- | --- |
| `GOEXPERIMENT=jsonv2 go vet ./...` | 通过，无输出 |
| `GOEXPERIMENT=jsonv2 go test ./... -count=1` | 全部通过；`internal/engine` 520.031s |
| `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过，无输出 |
| `powershell.exe -NoProfile -File scripts/generate-version.ps1 -Check` | `release=0.17.0 contract=17 android_version_code=19` |
| `go test ./internal/mcp -run 'TestDiscoveryByteBudget' -v` | instructions=6412、tools_list=19558、总 25970 字节，低于基线 36250 |
| `go test ./internal/mcp -run 'TestCatalogFingerprintBaseline'` | 目录指纹 `7efcd25506af221876900f4d8a2063f6c09e0658bcd9db350f8f8cfe75eef02e` |
| `go test ./internal/engine -run '^$' -bench BenchmarkTapStaticDiagnosis -benchmem -count=3` | 见下表 |

性能基准（合成 1002 节点、100 个原候选、全部歧义；Windows/amd64，非实机延迟保证）：

| 子项 | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| 原谓词逐点（100 点） | 8 610 941 / 8 611 721 / 8 614 836 | 7 426 429 / 7 426 433 / 7 426 428 | 2 000 |
| 完整失败诊断（含 9 点建议探测） | 13 581 804 / 13 547 101 / 13 637 038 | 9 921 438 / 9 921 436 / 9 921 433 | 3 419 |

该数据只覆盖失败路径的合成最坏形态；成功路径不构建诊断。未测量实机大树、不同树形或发布
产物运行延迟。

文档审计：独立只读 subagent 首轮返回 6 项（`docs/integration.md` 两处旧契约头、
`docs/mcp.md` 旧目录指纹与祖先措辞、`docs/architecture.md` 适用范围、
`docs/interface.md` 祖先键枚举），全部修正后复检返回 `CLEAN`。审计期间未修改被审文档。

## 6. 未执行项与原因

| 项 | 结论与原因 |
| --- | --- |
| 外部 MCP 宿主进程/连接重启 | 未执行。宿主 `codex.exe`（PID 23952）仍持有两个旧 stdio 适配器进程（PID 24828、21708）；该进程同时承载当前工作环境，不能由实现 agent 结束。主机侧新 exe 已通过独立 stdio 探针验证，但实际宿主 `tools/list` 重发现仍需用户重启宿主。 |
| 实机 `invalid_target/context` | 未构造。受控 probe 与 Settings 的公开树均无 `context=true` 节点；不以合成树结果冒充实机样本。 |
| 实机 `invalid_target/no_geometry` | 未构造。0×0 与 0×300 的受控 TextView 均未进入公开树；仅保留合成单测覆盖。 |
| 实机大树性能/分配 | 未执行。只有 1002 节点合成基准；不写成实机延迟或零分配保证。 |
| Android 实际触摸消费者与业务结果 | 证据边界不变：公开树证明与 `executed` 不证明应用业务回调。 |

## 7. 证据分层

- **已实现**：三码、reason、details、候选诊断、3×3 建议点、MCP 指引、契约 17 与版本源。
- **已单测**：分类矩阵、空动作数组、列表排序/上限、建议点正反例、carrier、谓词等价、
  零设备 I/O、O 保持 `ready`、结果详情深拷贝与三模式投影、指纹与版本检查。
- **已构建/核验**：手机整组清单（6 产物）与主机工具清单（2 产物）均从 `source_commit=5e10d07`、
  `source_dirty=false` 的干净树生成；`verify-manifest.ps1` 与 `build-host-tools.ps1 -VerifyOnly`
  均 `PASS`。
- **已部署**：手机服务、控制 App、手机 HTTP MCP 适配器为 `0.17.0 / contract 17 /
  versionCode 19`，覆盖安装保留私有数据。
- **已实测（有限）**：§8 的契约边界、10 类静态拒绝、建议点稳定帧执行、主机 stdio 探针与
  手机 HTTP MCP 探针。
- **未执行**：§6 的宿主进程重启、两个无法构造的实机 invalid_target 分支与实机大树性能。

## 8. 正式构建、部署与实机验证（2026-11-18）

### 8.1 设备与环境

- 设备：adb `17efb04a`，型号 `24117RK2CC`，Android 16；root 可用（Magisk）。
- 主机：Windows 10 x64，Go 1.27.1 windows/amd64，`GOEXPERIMENT=jsonv2`。
- 构建 HEAD：`5e10d076cce50f643a98caac8b65dda78f5a7ea2`（归档提交；源码内容与实现提交
  `a02d305` 相同，归档只增加 `maintenance/`）。构建时 `source_dirty=false`。
- 悬置文件 `maintenance/verification/tap-targeting-and-bubble-analysis.md` 保持未跟踪；
  为获得干净构建状态，本次只在本地 `.git/info/exclude` 排除该路径，未读取、修改或提交其内容。

### 8.2 整组清单与主机工具

`scripts/build-release.ps1` 真实产物：

| 产物 | SHA-256 | bytes |
| --- | --- | ---: |
| `device-service` | `d433ac4d1d65ca8265d7d498ea4e35452d8cb952bf9f2f63b370f2fb4788b317` | 8 978 592 |
| `device-mcp` | `d7733f2481c39cc03e413d6f0671234df5327613c801c2cb45a09ea844cb556d` | 9 175 200 |
| `control.apk` | `e125b2125ced7a64426c1aaeb2a16d4a203d3d9e9ea2ba145f13e49c78f6bb77` | 99 869 |
| `u2.jar` | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` | 3 306 229 |
| `applist.dex` | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` | 4 316 |
| `service.sh` | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` | 3 643 |

- `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`：`PASS`，
  release `0.17.0`、contract `17`、Android `0.17.0/19`、工具目录 `12/7efcd255...`。
- `scripts/build-host-tools.ps1`：`PASS`；`device-mcp.exe`
  `1c29d64213cc1a8d46fc98599a848bdbba2566426982450340e66a83d2146cce`（10 102 784 B）、
  `mcp-probe.exe` `74dc9e41d8f65e2b783db93836f8787ef4a682e0cce80c520ea0c002c2b991c7`
  （9 725 440 B）。
- `scripts/build-host-tools.ps1 -VerifyOnly`：`PASS`，并核验配套手机清单
  `source_commit=5e10d07`、`source_dirty=false`、目录 `12/7efcd255...`。

本记录提交晚于上述构建 HEAD；记录本身不改源码，但同一清单只对应 `5e10d07`，不能对新记录
提交再次声称清单已核验。后续如需重新核验，必须从当时的干净 HEAD 重建配套产物。

### 8.3 手机覆盖安装与准入

`scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json` 返回 `EXIT:0`：

- 安装使用 `adb install -r`，没有卸载、`pm clear` 或删除 `/data/data/com.mobileuse.vnext`；
  私有数据保留由覆盖安装方式保证，本记录未读取其内容。
- 安装后 `dumpsys package`：`com.mobileuse.vnext versionName=0.17.0 versionCode=19`。
- 部署脚本逐个核验手机端 `device-service`、`u2.jar`、`applist.dex`、`service.sh`、
  `config.json`、`device-mcp`、`mcp-config.json` 与清单 SHA-256，全部 `(match)`。
- `/health`：`release_version=0.17.0`、`contract_version=17`；`/api/status` 返回
  `can_start=true`，恢复屏障与 STOP 可用性 PASS。
- 手机 HTTP MCP 适配器：PID 9393，`/proc/9393/exe` 的 SHA-256 等于清单
  `d7733f...`；`127.0.0.1:18882` 返回 401 就绪。
- 手机 stdio 适配器：PID 8418 仍为旧镜像（`/proc/8418/exe` SHA-256
  `20c82ded...`）；未重启其宿主，见 §6。

### 8.4 契约与最小 STOP

| 请求 | 实际结果 |
| --- | --- |
| `/api/status` 携带 `X-Mobile-Use-Contract: 16` | `409 contract_mismatch`，`recovery=update/none` |
| `/api/status` 携带 `17` | `200`，`can_start=true` |
| `/v1/control/stop` 携带旧 `contract_version=16` | `200 accepted=true state=recorded`，不扩大普通契约检查 |

### 8.5 受控静态错误样本

临时 `StaticErrorFixtureActivity` 只装入受控 `com.mobileuse.probe` 的验证 APK；页面由
FrameLayout/TextView/Button 构造，未读取用户应用。每个样本先 `observe` 取得独立 O，再对
目标 ref 发一次 tap；静态拒绝后 `status(O)` 均为 `ready`，`outcome=not_executed`，没有后观察。
页面标签只用于本次取证，临时源码在验证后删除，原 probe 已重建重装
（`git diff --exit-code -- android-control/test-target` 通过）。

| 页面/目标 | 实际 code/reason | details 受控事实 |
| --- | --- | --- |
| 同 bounds 可点击覆盖 | `no_safe_point/blocked` | `scope=target`、`scope_ref=se_full_card`、`occluding_refs=[se_cover]`，无 `suggested_point` |
| 两个同 bounds 非交互子节点 | `no_safe_point/ambiguous` | `ambiguous_refs=[se_left,se_right]`，无 `occluding_refs` |
| 十字可点击遮挡 | `no_safe_point/blocked` | `occluding_refs=[se_horizontal]`，`suggested_point={x:218,y:558}` |
| 十字遮挡 + 更深外部非交互节点 | `no_safe_point/blocked` | 无 `suggested_point`（整树唯一归属不通过） |
| 竖向阻挡 + 两个同 bounds 左侧文本 | `no_safe_point/mixed` | `occluding_refs=[se_mixed_bar]`、`ambiguous_refs=[se_mixed_left1,se_mixed_left2]`、`suggested_point={x:857,y:558}` |
| 可点击卡片内的无动作正文，`bubble=false` | `unsupported_node_action/target_missing_action` | `target_actions=[]`；`nearest_actionable_ref=se_guide_card`、`safe_carrier_ref=se_guide_text` |
| 仅 non-interactive 文本，默认 `bubble=true` | `unsupported_node_action/no_interactive_ancestor` | `target_actions=[]` |
| 只有 long_press 的文本，`bubble=false` | `unsupported_node_action/target_has_other_action` | `target_actions=["long_press"]`，消息含“公开动作：长按” |
| 根 ref | `invalid_target/root` | `scope=target`、`scope_ref=n1` |
| long_press-only 祖先 + 无动作正文 | `unsupported_node_action/ancestor_missing_action` | `ancestor_ref=se_lift_card`、`ancestor_actions=["long_press"]` |
| 禁用可点击祖先 + 无动作正文 | `unsupported_node_action/ancestor_invalid` | `ancestor_ref=se_lift_disabled_card`、`ancestor_actions=["tap"]` |

以上响应只出现新 details 键；旧 `occluding_ref` 未出现在本次静态出口（该断言同时由单测
`TestActTapFullyOccludedReportsNoSafePointBlocked` 的 `requireDetailAbsent` 覆盖）。

### 8.6 建议点执行

- 首次在未稳定帧上执行十字遮挡的 `suggested_point=(218,558)`，重验返回
  `target_changed`、`not_executed`，并交付新 O；这是提案 §7.2 已声明的 fresh 帧边界，
  不是建议点静态证明失败。
- 随后用 `wait_for stable=true` 取得稳定 O，重新静态拒绝得到 `(220,556)`，在同一 O 上以
  新 P 坐标 tap：`200 executed`，后观察 O31。
- mixed 页面稳定帧建议 `(860,556)`，坐标 tap：`200 executed`，后观察 O33。
- 这组样本证明建议点可执行，但不证明 Android 业务回调或任意页面的 fresh 稳定性。

### 8.7 MCP 探针

| 探针 | 实际结果 |
| --- | --- |
| 主机新 `build/device-mcp.exe` stdio、text | `RESULT PASS`；instructions 6412 字节且 `source_match=true`，12 工具，目录 `7efcd255...` 与两份清单一致；start/observe/act/end 与取消闭环通过 |
| 已部署手机 HTTP 适配器（`mcp-probe -transport http-remote`、text） | `RESULT PASS`；`contract=17 release=0.17.0 process_managed=false`，401/403/Origin/agent token 边界、12 工具、instructions、目录指纹与两份清单一致，start/observe/act/end 与取消闭环通过 |

主机 `codex.exe` 仍运行旧 stdio 镜像，实际宿主的工具重发现未完成；§6 已单列。
