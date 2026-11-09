# act 后观察收敛采样实现与验证记录

状态：2026-11-09。本页只记录本轮实际执行过的构建、测试与检查，以及真实结果。验证对象是
Go 源码与单元测试；**没有**运行 `adb`、没有连接设备、没有执行部署脚本，因此不构成实机结论，
也没有把单元测试通过写成实机行为。

提案：[act 后置观察的收敛采样](../proposals/implemented/act-post-observation-settle.md)。

## 1. 范围与环境

| 项 | 值 |
| --- | --- |
| 工作区 | `E:\Code\Python\mobile-use-vnext` |
| 代码提交 | `39683f478ff012d31220096b169939efea5c0a9f` |
| Go | `go1.27.1 windows/amd64`（`GOEXPERIMENT` 未显式设置，`jsonv2` 在该工具链上可用） |
| 构建目标 | 宿主（windows/amd64）与 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` 交叉构建 |
| CGO | `CGO_ENABLED=0` |
| 设备/部署命令 | 未执行 |
| 改动边界 | `internal/engine`（新增 `identity.go`、`settle.go` 及测试）、`internal/observation/build.go`、`internal/eventlog/log.go`、`cmd/device-service/main.go`、`docs/architecture.md` §4、`docs/interface.md` §1/§5/§6 |

公共 JSON 契约、错误码与 `contract_version` 无变化。

## 2. 落点

| 能力 | 落点 |
| --- | --- |
| 三层收敛判据（层级身份链 / 公开树 / 前台与显示语境） | `internal/engine/settle.go`：`sampleDigestOf`、`hierarchyDigest`、`screenDigest` |
| 判据与实时重验同源 | `internal/engine/identity.go`：`identityOf` 由 `act.go` 提出，两处共用 |
| 采样周期 = 上一帧实测耗时 | `settleRun.lastTook`，预算预检在同一次判断中使用 |
| 静默窗口 | `gap = MinDwell - since(prev.CapturedAt)`；发布一对相等样本的**后**一帧 |
| 预算与撤销 | `settleUntilStable`；采样失败发布上一帧，`stopped` 不发布观察 |
| 采样不推进 ref 连续号 | `internal/observation/build.go`：`Builder.Preview` 保存并恢复 `prev`/`nextRef` |
| 图片路径 | `capture.go`：先 `settleCapture`，再读源图，再由 `captureSettledTree` 刷新一次树 |
| 三档策略与校验 | `DefaultSettlePolicies()`、`SettlePolicies.Validate()`（启动时校验，退出码 2 拒绝） |
| 收敛事实日志 | `observation.settle`（Debug）：`phase`、`samples`、`stable`、`ms`；白名单新增两键 |

## 3. 执行的命令与结果

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过 |
| `GOEXPERIMENT=jsonv2 go build ./...` | 通过 |
| `go vet ./...` | 无输出 |
| `go test ./...` | 全部 `ok`：engine 109.750s、observation、android、client、mcp、protocol、store、imaging、`scripts/mcp-probe` |
| `go test ./internal/engine -run 'TestSettle|TestIdentity' -v` | 15 个用例全部 PASS |
| `go test ./internal/observation -run TestPreview` | PASS |

`Preview` 用例的有效性做了反向对照：临时去掉 `Preview` 的保存/恢复后，同一用例以
`preview advanced the previous snapshot` 失败；恢复实现后通过。

## 4. 用例与证明窗口

| 用例 | 证明的窗口 |
| --- | --- |
| `TestSettlePublishesLaterFrameOfEqualPair` | `[A,B,B]` 两次比较后收敛，发布后一帧 |
| `TestSettleBudgetExhaustionPublishesNewestFrame` | `[A,B,C,D]` 帧数耗尽，发布最新帧且 `stable=false` |
| `TestSettleQuietWindowDelaysTheComparingSample` | 静默窗口生效，发布帧的采集时间落在窗口关闭之后 |
| `TestSettleMinSamplesRequiresAnotherFrame` | `MinSamples=3` 时第三帧才确认 |
| `TestSettleUnreadableSampleKeepsPreviousFrame` | 采样失败发布上一帧，不判后观察失败 |
| `TestSettleStopsWithoutObservation` | 静默窗口内 STOP：`stopped`、无帧、无观察 |
| `TestSettlePolicyIsSelectedByPhase` | `act`/`start`/`open_app` 各取本档；`observe`/`shell` 不收敛 |
| `TestSettleUsesStartTierForFirstObservation` | `start` 首个观察走 Start 档 |
| `TestSettleUsesOpenAppTierForLaunchConfirmation` | `open_app` 后观察走 OpenApp 档 |
| `TestSettleReadsImageAfterConvergence` | 读序为"重验 → 收敛采样 2 帧 → 源图 → 刷新 1 帧" |
| `TestSettlePolicyValidation` | 五类非法策略被拒；默认策略通过 |
| `TestSettleBudgetFitsWorkWindow` | 三档预算均不超过所属工作窗口的 10% |
| `TestSettleDigestFollowsThePublicTree` | `Visible` 翻转不改 L1、改 L2，因此不会误判收敛 |
| `TestSettleKeepsObservationShape` | 观察结果不含 `stable`/`samples`/`settle` 成员 |
| `TestSettleRecordsConvergenceFacts` | `observation.settle` 记到 `samples=2`、`stable=true` |
| `TestIdentityFieldCountMatchesDigest` | `rawIdentity` 字段数变化会强制同步摘要实现 |
| `TestPreviewKeepsContinuityState` | `Preview` 不改 `prev`，ref 编号与不采样时一致 |

## 5. 未执行的部分

| 项 | 原因 |
| --- | --- |
| 实机 A/B（`target_changed` 率、`start` 后首个 `act` 成功率、按档的 `samples`/`stable` 分布、settle P50/P95） | 本环境没有设备连接，未运行 `adb`；提案 §8.2 的指标只能在真机按脚本采样 |
| 三档 `MinDwell`/`MaxBudget` 的实机校准 | 依赖上一项数据 |
| 按动作子类型（`tap`/`swipe`/`text`）细分策略 | 依赖上一项数据 |
| 单进程内连续跑 `start`→`act`→`open_app` 的三档对照用例 | 以"按阶段查表"单测加 `start`、`open_app` 两个端到端用例覆盖，未单独构造连续对照 |
