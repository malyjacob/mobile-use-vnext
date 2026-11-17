# act 静态目标拒绝分类：实现与验证记录

状态：2026-11-18。实现提交：`a02d305f29f27fdfe6b63faf356116e137432c1a`。
本记录覆盖单元测试、交叉构建、版本源与文档审计；**不涉及实机、发布清单、部署或外部 MCP
宿主刷新**。提案为
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
| 实机分类样本 | 未执行。当前没有接入设备或部署指令；分类、禁用/无 bounds、祖先原因、建议点与边界均由合成树单测覆盖，不假定真实 UI 可稳定构造每一种公开树形态。 |
| 发布清单、整组 APK/服务/适配器构建与核验 | 未执行。`0.17.0/17/19` 已在唯一版本源冻结并生成常量，但未安排发布窗口；未运行 `build-release.ps1`、`verify-manifest.ps1` 或部署。 |
| 手机覆盖安装、私有数据保留、契约边界与最小 STOP 实机闭环 | 未执行。依赖发布产物与设备。 |
| 主机适配器重建与 `-VerifyOnly` | 未执行。依赖整组发布决定；未替换或验证任何主机 exe。 |
| 外部 MCP 宿主重启与 Instructions 重发现 | 未执行。宿主由用户操作；本次只验证源码说明与目录指纹。 |
| 实机大树性能/分配 | 未执行。仅有合成基准；不把该数据写成实机延迟或零分配保证。 |

## 7. 证据分层

- **已实现**：三码、reason、details、候选诊断、3×3 建议点、MCP 指引、契约 17 与版本源。
- **已单测**：分类矩阵、空动作数组、列表排序/上限、建议点正反例、carrier、谓词等价、
  零设备 I/O、O 保持 `ready`、结果详情深拷贝与三模式投影、指纹与版本检查。
- **未实机**：真实 Android 触摸消费者、业务回调、真实大树的建议点效果。
- **未发布**：清单、部署、手机/主机适配器整组升级与宿主刷新。
