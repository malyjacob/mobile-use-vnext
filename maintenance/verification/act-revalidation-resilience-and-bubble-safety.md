# 验证记录：act 重验抗外围扰动与 bubble 遮挡安全（契约 33）

实现提交：`69e66cef0892dc017c7a8830c7f13339d8e0087e`。
归档提案：[act-revalidation-resilience-and-bubble-safety](../proposals/implemented/act-revalidation-resilience-and-bubble-safety.md)。
**涉及实机**：本次涉及发布交付，手机整组部署、最小实机闭环与双端独立探针见 §5。

结论分层（不得互相替代）：

| 层次 | 结论 |
| --- | --- |
| 已实现 | 提案 §4.1–§4.6 六个部件全部落地，落点见提案 §11.1 |
| 已单测 | 新增 3 个测试文件、迁移 9 个既有测试文件；变更路径的定向运行全部 `ok` |
| 已全量 | **未执行**，按用户决定跳过（见 §3、§4） |
| 已实测 | 见 §5 |
| 磁盘替换 | 见 §5 |
| 独立探针 | 见 §5 |

---

## 1. 环境与构建方式

- 主机：Windows，`go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0` 仅用于发布构建。
- 单测在开发树执行；发布构建在归档提交后的干净树执行（未跟踪文件会让发布脚本判定 `source_dirty=true`）。
- 版本源：`release/versions.json` 手改为 `0.37.0` / `33` / `39`，`scripts/generate-version.ps1` 重写四个生成文件，`-Check` 通过。

## 2. 单元测试

### 2.1 命令与真实结果

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | exit 0，无输出 |
| `go vet ./internal/engine` | exit 0，无输出 |
| `go test ./internal/engine -run '<6 组提案新增用例 + TestPointOwnerFallback + 迁移后的 21 条>' -count=1 -timeout 600s` | `ok  github.com/malyjacob/mobile-use-vnext/internal/engine  44.910s` |
| `go test ./internal/engine -run 'TestVisual|TestWait|TestUI|TestLegacyUI|TestSnap|TestShell|TestPin|TestReadOnly' -count=1 -timeout 600s` | `ok  github.com/malyjacob/mobile-use-vnext/internal/engine  95.855s` |
| `go test ./internal/eventlog -count=1 -timeout 120s` | `ok  github.com/malyjacob/mobile-use-vnext/internal/eventlog  0.025s` |
| `gofmt -l`（6 个改动的生产文件 + 3 个新增测试文件） | 无输出 |

迁移过程中由协同的子代理在各自文件范围内执行的定向运行同样全绿，主代理已复核其范围与断言差异：
fixture 迁移批次 `ok ... 20.026s`（16 个用例）、断言迁移批次 `ok ... 12.083s`、
坐标兜底 `ok ... 0.062s`。

### 2.2 新增用例与证明窗口

| 用例 | 证明 |
| --- | --- |
| `TestRevalidateSyntheticDisplayRootNormalization` | 插入第二个顶层窗口后目标仍按 Tier 2 命中；祖先链长度 2/2、预算步数 3；直接挂在合成根下的目标链长为 0。反向对照：把合成根的 `HasSelected` 置真使其不再满足判定，链长变为 3 且目标退化为 `none`。 |
| `TestRevalidateTier3AncestorNoiseToleranceAndBoundaries` | 非目标祖先 `Text`/`Description`/`Checked` 变化仍 Tier 3 命中；目标自身文案变化拒绝；未知 presence 的包装层仍保留；只有文本的非根祖先不能充当强锚点。 |
| `TestRevalidateNumericSkeletonE3AndCarrierUnbind` | 时间戳/角标增删放行（`independentSubtree` 为真），纯数字替换 `10→11`、业务词 `确认→取消`、跨卡片 `商品A→商品B`、仅有阿拉伯-印度数字锚点全部拒绝；同一 fixture 在 `matchKeyboardShiftedSubmitNode` 下返回 `none`，证明写后提交路径不吃骨架。 |
| `TestRevalidateTier5HorizontalBandTranslationAndGuards` | 同水平带纵向平移在 Tier 5 唯一命中并返回新矩形与两个证据标识；水平出带拒绝；正/反向第二候选报歧义；更早 Tier 的歧义在 Tier 1 终止；`disableTranslation` 与 `.withoutTranslation()` 都使结果为 `none` 且终止于 Tier 3。 |
| `TestActRevalidateTransientSingleRetryAndStop` | 用 `testing/synctest` 虚拟时间精确断言：首帧成功 1 次采集 1 次派发 0 等待；第 2 帧成功 2 次采集 1 次派发 150ms 等待且日志只有一条 `act.target` 带 `attempt=2`；第 2 帧仍失败时交付第 2 帧（框值 `Try again` 而非第 1 帧的 `Loading`）；第 2 次采集失败返回 503 且不交付；`capability_changed` 1 次采集 0 等待；暂停中 STOP 立即 `control_revoked`，1 次采集、0 交付、0 派发。 |
| `TestPointOwnerFallback*`（5 个） | 命中集合增长但唯一拥有者不变时在原坐标执行；换成另一卡片、外部交互遮挡、双拥有者、同点嵌套交互、无自有文案拥有者的子树业务文案替换全部 `point_changed`；整树唯一资源可作独立内容、重复资源仍拒绝；`long_press` 同规则，坐标 `text`/raw `swipe`/`nil` 操作不进入；纵向平移（同水平带）与横向出带、点离拥有者矩形一律不追随。 |
| `TestActRevalidateTier5OcclusionAndColocatedSync` | `colocatedSyncShiftOK` 的 `max(16, maxH/4)` 边界与空矩形拒绝；Tier 4/5 落点被外部交互覆盖时 `scope_blocked`，同几何在 Tier 1–3 不受该函数约束。 |

### 2.3 反向对照（去掉实现即失败，均实测）

| 去掉的实现 | 失败的用例 |
| --- | --- |
| `isSyntheticDisplayRoot` | `TestRevalidateSyntheticDisplayRootNormalization` |
| Tier 3/5 去噪 L2 与强锚点收紧 | `TestRevalidateTier3AncestorNoiseToleranceAndBoundaries` |
| 数字骨架守恒 | `TestRevalidateNumericSkeletonE3AndCarrierUnbind` |
| `actMatchTapLikeExact`（写后提交精确口径） | 同上，`matchKeyboardShiftedSubmitNode` 分支 |
| Tier 5 与 `withoutTranslation()` | `TestRevalidateTier5HorizontalBandTranslationAndGuards`、`TestPointOwnerFallbackNeverFollowsAMovedOwner` |
| `actRevalidateMaxAttempts = 2` | `TestActRevalidateTransientSingleRetryAndStop`（恢复路径与日志 `attempt=2`） |
| `match.tier < 4` 遮挡升级 | `TestActRevalidateTier5OcclusionAndColocatedSync`（Tier 5 覆盖子用例） |
| 兜底条款 5（子树证据） | `TestPointOwnerFallback/subtree_business_text_replaced` |
| 外部交互拥有者检查 | `TestPointOwnerFallback/external_interactive_overlay_covers_point` |
| 操作类型门（仅 tap/long_press） | `TestPointOwnerFallback/editable_owner_is_still_no_point_owner` |
| 单拥有者规则 | `TestPointOwnerFallback/nested_interactive_owners_at_one_point` |
| `Bounds.Contains(x, y)` | `TestPointOwnerFallback/point_left_the_owner_rect` |
| 兜底 `match.raw` 与 `freshOwnerIdx` 的绑定 | `TestPointOwnerFallback/owner_became_another_card` |
| 兜底的全部 `proof.gate()` | `TestPointOwnerFallback/pre_matcher_rejection_still_stops` |
| fixture 跨两帧保持变化 | 迁移批次的两处反向对照：把深层子节点改回纯被动装饰叶使 `TestActCoordinatePointRejectsDeeperNodeOnFreshFrame` 翻为 `executed`；`markerCapture` 在每一层都触发 `sameStructureForAct` 冲突，故文案变化型拒绝跨帧保持 |

## 3. 被作废的全量运行（如实记录）

第一次全量尝试：`go test ./... -count=1 -timeout 800s`。

- **结果**：`internal/engine` 在 `panic: test timed out after 13m20s` 处终止，进程退出码 1。
- **真实原因**：`-timeout 800s` 低于 `AGENTS.md` 第 24 条与 `maintenance/process.md` §6 规定的
  15 分钟下限，也低于该包历史上 841.400s 的正常耗时；这是执行者的参数错误，**不是产品缺陷，
  也不是环境波动**。
- **既有事实**：同一运行中其余 13 个包（`cmd/device-mcp`、`internal/{android,client,device,eventlog,httpapi,imaging,mcp,observation,protocol,store,treetext}`、`scripts/mcp-probe`）全部 `ok`。
- **引擎包内报出的失败**：21 条，全部为"期望 `not_executed` 实际 `executed`"的断言失败，与超时无关。
  逐条单独复现确认：全部可在单跑时复现，不是跨用例干扰。
- **该次运行未跑到的部分**：panic 发生在 `ui_recovery_test.go` 期间，其后三个文件
  （`visual_test.go`、`wait_content_test.go`、`wait_for_test.go`）未执行，已用定向方式补跑
  `ok ... 95.855s`。

## 4. 未执行项与原因

| 未执行项 | 原因 |
| --- | --- |
| `go test ./...` 全量 | 用户决定不重跑；引擎包是整套测试中最耗时的部分 |
| `go test ./internal/engine` 整包 | 同上 |
| 引擎包"全绿"的断言 | 未整包运行，故不作此结论。可用证据只有：那次运行报出的失败恰好是这 21 条（已逐条复现并迁移）、其余 13 包全部 `ok`、被截断的尾部三文件定向补跑 `ok`。这些都支持"变更路径已验证"，不支持"引擎包全绿" |

**残余风险**：变更面之外是否被间接扰动，没有一次性的直接证据。本次改动的生产范围限于
`act` 重验、坐标点证明、`scroll` 的日志透传与 `eventlog` 注释，未触及 `settle`、`text` 写入与
读回、cleanup、retirement、`observe` 连续性等机制。

## 5. 实机与发布

（占位，按归档顺序在其后执行并回填：整组覆盖部署、最小实机闭环、Windows 双工具替换、
主机 stdio 与手机 HTTP 独立探针。）

## 6. 文档审计

- 由独立、只读的文档审计 subagent 执行，共两轮 14 项发现，全部修正后复检为 **clean**。
- 覆盖：`docs/` 只写已实现架构/现行契约/运维说明/工程规范；过程性措辞 0 命中；`reason` 枚举仍为
  9 个取值；静态四级落点策略未被重编号；`0.36.0`、契约 `32`、versionCode `38` 在 `docs/` 与
  `README.md` 中 0 命中；版本源、生成常量与文档一致为 `0.37.0`/`33`/`39`；无新增断链。
- 审计者不承担实现、未修改任何文件。

## 7. 已知既有边界（本次未改变）

坐标 `tap`/`long_press` 的严格点证明以**最深命中集合**为授权单位，只比较该集合内节点的结构；
交互拥有者自身的 `Text` 只由拥有者守恒兜底读取，而兜底只在严格证明失败后才运行。因此
"拥有者自身文案变化、但其被动深层子节点不变"的帧在本次改动前后都被接受。这属于既有语义，
提案 §4.5 未要求改变，本次未改动，也不宣称已覆盖。
