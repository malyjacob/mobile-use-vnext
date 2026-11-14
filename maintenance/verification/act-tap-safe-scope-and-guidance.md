# tap/long_press 子树安全落点与结构化引导：实现与验证记录

- 提案：[act-tap-safe-scope-and-guidance.md](../proposals/implemented/act-tap-safe-scope-and-guidance.md)（用户确认后实施）。
- 实现提交：`cc71c876a231e0a2717e18f83f01c204fe96b735`（2026-11-15）。提交标题：
  `feat(act): tap/long_press 子树安全落点与结构化引导（契约 14）`。同一次提交包含实现、
  测试、正式文档、README 与唯一版本源（`release_version` 0.14.0、`contract_version` 14、
  `android_version_code` 16）。
- 发布清单：`build/release/manifest.json` 与 `build/host-tools-manifest.json` 均记录
  `source_commit=cc71c876a231e0a2717e18f83f01c204fe96b735`、`source_dirty=false`。
- 环境：Windows 主机，Go 1.27.1，`GOEXPERIMENT=jsonv2`；发布目标为 linux/arm64、
  `CGO_ENABLED=0`，主机工具目标为 windows/amd64；MCP SDK `v1.8.0`、协议 `2026-07-28`。
- 实机：adb 设备 `17efb04a` / `24117RK2CC`，Android 16，虚拟显示 override
  `1080x2400`、density 450；部署后服务与 App 均为 `0.14.0 / contract 14 /
  versionCode 16`。
- **本记录涉及实机**：版本边界、恢复屏障、最小 STOP、手机 HTTP MCP 适配器及临时复合卡片
  的 tap/long_press 路径在真机执行过。未执行项见 §11，不把单元测试、清单或部署 PASS 写成
  未做过的实机结论。

## 1. 分诊与提案确认

分诊为取舍：基线对 `target.ref` 固定使用 `raw.Bounds.Center()`，且无动作正文只返回没有
祖先线索的 `422`。这与“实现偏离现行规范”的缺陷不同，涉及默认落点、认领后重验和错误详情
契约面。用户确认采用提案的 **R3 + R1**：子树内静态安全落点规划、固定点新鲜树重验，以及
可操作祖先与安全坐标的原子引导；共同契约推进到 14。

提案明确了以下不可越界语义：不静默把无动作 `ref` 晋升为祖先派发；重验阶段不重选坐标；
`safe_carrier_ref` 只解释坐标来源，不是可直接调用的替代动作；树模型证明不覆盖同级/窗口
z 序、透明拦截层、Android 实际消费者或应用业务结果。

## 2. 基线与实现提交

- 提案基线：`81bad647d40d2b9e13f25ff12dfb573f978e5061`。
- 实现提交：`cc71c876a231e0a2717e18f83f01c204fe96b735`。
- 代码范围：新增 `internal/engine/tap_scope.go` 与 `internal/engine/act_tap_scope_test.go`；
  修改 `internal/engine/act.go`、`internal/engine/engine.go`、`internal/protocol/errors.go`、
  `internal/mcp/schema.go` 及协议/HTTP 客户端/MCP 保真测试。
- 版本与文档：`release/versions.json` 与 Go/Java/XML/Manifest 生成物同步为
  `0.14.0 / 14 / 16`；正式文档与 README 同步已落地行为，独立只读审计最终 CLEAN。

## 3. 已实现

| 提案条目 | 落点 |
| --- | --- |
| 目标子树作用域收集、承载/交互/context 边界、半开矩形与有界拓扑/source 校验 | `internal/engine/tap_scope.go`：`collectTapScope`、`provesSubtreeTapPoint`、`validateTapRawTopology`、`checkTapSourceMapping`、`buildTapPublicView` |
| 固定候选顺序：原中心/基础矩形中心 → 非匿名/匿名承载节点中心 → 纵向/横向中心线最长安全区间 | `tap_scope.go`：`enumerateTapCandidates`、`chooseTapPlan`、`planTapTarget`、`tapCenterLineSpan` |
| 三级均失败时认领前 `422`；`occluding_ref` 只按原中心阻塞证据选择最近边界 owner | `tap_scope.go`：`occludingTapRef`、`staticTapRefCheck`、`tapNoCandidateError`；`act.go`：`staticActContentCheckWithTap` |
| 认领后单次新鲜采集只证明静态固定点，不重选 strategy/坐标；目标与承载身份、公开能力、投影和固定点归属均需通过 | `act.go`：`revalidateActTarget`、`revalidateTapTarget`；`tap_scope.go` 的同一作用域/谓词 |
| `Preview` 缺失/失败为 `503 hierarchy_unavailable`，无新 O；唯一匹配及固定点失败为 `409 target_changed` | `act.go`：`revalidateTapTarget`；既有 `deliverRejection` 门禁不变 |
| R1 从目标自身边界开始，沿唯一父链到最近交互祖先；先证明该祖先 ref 可规划，再用子树谓词与整树唯一命中归属双重证明坐标 | `tap_scope.go`：`tapGuidanceFor`、`tapGuidanceCandidates`、`provesGuidancePoint`、`tapUnsupportedWithGuidance` |
| 引导键原子出现；`nearest_actionable_ref` 与 `safe_point` 同组，承载来源才附 `safe_carrier_ref` | `tap_scope.go`：`tapUnsupportedWithGuidance`、`tapGuidanceFor` |
| 嵌套 `error.details` 所有权隔离，覆盖首次结果、保留错误、重复 P 与 `status(P)` | `internal/protocol/errors.go`：`CloneErrorDetails`、`APIError.Public`；`internal/engine/engine.go`：`operationViewLocked` |
| `act.tap_plan` 仅记录白名单事实，不记坐标/ref/树/正文 | `internal/engine/act.go`：`logTapPlan` |
| MCP `act` 描述与实际工具目录指纹 | `internal/mcp/schema.go`；目录 `count=12`、`sha256=6ace74b8bdceb75dd43ffe270e3f33c7b3a6064aff3cc4734256e539460d136e` |

## 4. 已单测

新增 `internal/engine/act_tap_scope_test.go` 覆盖复合卡片、三级候选、context/交互边界、
拓扑损坏、动态竞争、承载变化、projector 选择、R1 双重谓词、视图所有权和白名单日志；
`internal/protocol`、`internal/httpapi`、`internal/client`、`internal/mcp` 覆盖嵌套详情的
HTTP 解码、三种 MCP 模式、取消、重复 P/`status(P)` 与跨层保真。

关键反向对照：

- 同一复合卡片在基线派发中心 `(540,708)`，实现后静态规划为 `(613,501)`；删除 fresh
  子树谓词、承载匹配或全局归属校验时对应用例会失败。
- 新鲜目标/承载身份重复或匹配歧义返回 `409 target_changed`。
- Preview 缺少 Capture 返回 `503 hierarchy_unavailable`，零输入且无新 O。
- 原 O 退休后的重复 P/`status(P)` 只回放历史引导，零采集、零派发。
- 首次、重放与 `status(P)` 返回视图中的嵌套详情互不共享可变对象。

## 5. 已构建与静态检查

命令在独立 shell 执行，测试不带交叉编译环境：

| 命令 | 实际结果 |
| --- | --- |
| `./scripts/generate-version.ps1` / `./scripts/generate-version.ps1 -Check` | `version source consistent: release=0.14.0 contract=14 android_version_code=16`；Go、Java、XML、AndroidManifest 四处一致 |
| `go test ./internal/engine/... ./internal/protocol/... ./internal/httpapi/... ./internal/client/... ./internal/mcp/...` | 全部通过：`internal/engine` 390.293s、`internal/protocol` 0.047s、`internal/httpapi` 0.909s、`internal/client` 0.200s、`internal/mcp` 0.423s |
| `go vet ./internal/engine/... ./internal/protocol/... ./internal/httpapi/... ./internal/client/... ./internal/mcp/...` | 通过 |
| `gofmt -l <全部受影响 Go 文件>` | 无输出，clean |
| `git diff --check` | 通过 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过 |

本记录不把五包回归写成全仓矩阵通过；`go test -race` 与五包之外的包未执行。

## 6. 整组与主机清单/哈希

在实现提交 `cc71c87` 的干净源码上，`scripts/build-release.ps1`、
`scripts/verify-manifest.ps1 -Manifest build/release/manifest.json`、
`scripts/build-host-tools.ps1`、`scripts/build-host-tools.ps1 -VerifyOnly` 与
`scripts/deploy.ps1` 均已实际执行并通过。以下哈希取自该次构建和核验留下的清单。

现有发布清单记录以下手机产物：

| 产物 | SHA-256 | bytes |
| --- | --- | ---: |
| `device-service` | `70ff214c22d349e330496931bf50db67f01c95339b593361191026a0f8f8bb07` | 8847520 |
| `device-mcp` | `6c7a58f0959575e3ffeaa215ac318f063ab685f5aebd23a19cdcadeb0797f52b` | 9175200 |
| `control.apk` | `11ebca10711c80f4f2bbc1076f65ee670d16b25ef51c15eb9a8d031a3be0b9a4` | 103965 |
| `u2.jar` | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` | 3306229 |
| `applist.dex` | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` | 4316 |
| `service.sh` | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` | 3643 |

现有主机工具清单记录：

| 产物 | SHA-256 | bytes |
| --- | --- | ---: |
| `device-mcp.exe` | `930dfa86ad4b33bef8a16567bf8ee20f39cd1bbc7abdf01c6b8aa370743dfb5a` | 10096128 |
| `mcp-probe.exe` | `023c0ae021c84ef7c1671110e70d03efe949c55eae7a3c96c7f8ee8af8660080` | 9695744 |

两份清单的工具目录均为 `count=12`、
`sha256=6ace74b8bdceb75dd43ffe270e3f33c7b3a6064aff3cc4734256e539460d136e`。
归档阶段未重建发布制品，也未重新运行 `scripts/verify-manifest.ps1` 或
`scripts/build-host-tools.ps1 -VerifyOnly`。归档后 HEAD 会因 maintenance 文档与发布源码
不同，不能据此补造发布门禁结果。

## 7. 已部署

从 `source_commit=cc71c87`、`source_dirty=false` 的发布清单部署：

- 手机服务与 App 均为 `0.14.0 / contract 14 / versionCode 16`。
- 服务启动、App 配对、恢复屏障、STOP 可用性均 PASS。
- 手机 HTTP MCP 适配器已启动，运行二进制 SHA-256 与清单匹配。
- 手机 stdio 仍有旧进程，需要其宿主重启后加载新二进制；本轮未重启外部宿主，也未宣称其
  已通过 `tools/list` 重新发现 0.14.0 工具目录。

## 8. 已实测：契约、恢复与 STOP

| 场景 | 实际结果 |
| --- | --- |
| `/api/status` 携带旧契约 `13` | `409 contract_mismatch` |
| `/api/status` 携带新契约 `14` | `200` |
| control sync 携带旧契约 `13` | `409` |
| control sync 携带新契约 `14` | `200`，`availability=ready` |
| 发布前恢复屏障 | 旧会话 `ended/service_restarted`，`cleanup complete`，`can_start=true`；随后新 `start` 成功 |
| 最小 STOP（先 `end` 且 cleanup pending） | `/v1/control/stop` 携带 `contract_version=13` 返回 `200`、`accepted=true`、`state=recorded` |

活动会话撤销未单独取得实机样本，不能由“先 end 后 STOP”样本替代。

## 9. 已实测：临时复合卡片

临时 test-target 只在验证期间安装，用于构造以下几何；原始树、截图与日志不入库：

| ref | bounds | 动作 |
| --- | --- | --- |
| `n21` | `[60,261,1020,581]` | `tap`、`long_press` |
| `n22` | `[80,281,1000,381]` | 无 |
| `n23` | `[80,381,1000,521]` | `tap` |

正文 `n22` 的 tap 返回 `422 unsupported_node_action`，`error.details` 为
`nearest_actionable_ref=n21`、`safe_point={"x":540,"y":331}`、
`safe_carrier_ref=n22`；原 O 的 `status(O).input_state=effective`。

使用彼此独立的 O 执行：

| 请求 | 业务后观察 |
| --- | --- |
| `n21` tap | 卡片点击计数 1，内部按钮 0 |
| safe_point `(540,331)` | 卡片点击计数 2，内部按钮 0 |
| `n21` tap | 卡片点击计数 3，内部按钮 0 |
| `n21` long_press | 卡片已长按 |

这组样本验证了静态候选与业务后观察在该临时页面上的对应关系；它不证明任意第三方应用的
事件消费者或业务语义。临时 test-target 源码已完全恢复，原夹具已重建重装，
`git diff --exit-code -- android-control/test-target` 通过；临时代码未提交，也未进入发布。

临时 test-target 覆盖安装后的第一次 `app.open` 返回过 `504 app_launch_unconfirmed`；当时
observation 完整且前台已是 probe，随后 full observe + open 恢复。该无关生命周期异常未修改，
不纳入本提案的判据或成功结论。

## 10. 正式文档审计

实现提交涉及 `docs/` 与 `README.md`，由独立只读 subagent 审计（不实现、不改文件）。审计
发现 `docs/runbook.md` 的阶段代号与已漂移的 `verify-m6` 说明后完成最小修正并复检，最终
`VERDICT: CLEAN`。归档阶段本次只改 `maintenance/`，不回写或重审 `docs/`。

## 11. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 外部 MCP 宿主 `tools/list` 与工具刷新 | 未操作外部宿主；手机 stdio 旧进程需宿主重启，只验证了手机 HTTP 适配器启动及哈希。 |
| `scripts/verify-m6.ps1` | 脚本已漂移，不作为发布门禁；本轮以发布清单、实际版本边界与部署结果为准。 |
| 活动会话下最小 STOP 撤销 | 未单独构造；只取得先 `end` 且 cleanup pending 时 STOP 受理的样本。 |
| 专门的真实 `occluding_ref` 页面 | 未单独构造；由合成全遮挡/歧义用例覆盖键选择和省略语义，实机验证集中在 R1 引导。 |
| 五包之外的全仓测试、`-race`、跨设备矩阵 | 严格限制在本次真实变化相关五包与单一设备；Windows 宿主也没有可用的 cgo race 环境。 |
| 临时 test-target 首次 `app.open` 异常 | 观察到一次既有 `app_launch_unconfirmed`，但随后的完整观察和 open 可恢复；属无关生命周期路径，未修。 |
| Android 实际触摸消费者与业务结果 | 公开树没有同级/窗口 z 序、透明拦截层和私有触摸代理；`executed` 与树证明不能保证应用最终行为。 |

## 12. 原始材料与结论边界

不保存原始节点树、截图、日志、消息正文、adb 输出或个人环境信息；本记录只保留受控几何、
哈希、命令与结果摘要。原始 test-target 临时代码未入库，验证结束后已恢复并重建原夹具。

- **已实现**：§3 的规划、固定点重验、R1 引导、详情所有权、版本与文档。
- **已单测**：§4 与五包回归，包括反向对照。
- **已构建/核验**：§5 的生成、测试、vet、格式、diff 与 linux/arm64 构建；§6 的现有清单
  和哈希。
- **已实测（有限范围）**：§7–§9 的部署、契约/恢复/STOP 与临时复合卡片。
- **未执行**：§11 列出的宿主刷新、活动 STOP、全仓矩阵、真实 `occluding_ref` 页面及
  Android 业务结果保证。
