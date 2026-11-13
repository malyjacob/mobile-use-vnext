# 验证记录：统一压缩公共节点树并按交付表示计算预算

状态：2026-11-13。本次涉及实机：否（未连接、未部署、未操作手机或主机运行进程）。
实现提交：`4fc86cf26c40be1df099a1ea259c99ca7a8d5abc`。提案：[compact-node-tree-presentation](../proposals/implemented/compact-node-tree-presentation.md)。
基线提交：`25db973`；提案落盘提交：`2781ffb`。

## 1. 分诊与范围

取舍类：改变公开表示、默认值、交付预算与共同契约，不是“现行规范说得对、实现走偏”。
讨论与提案已按流程完成，提案由用户确认后实施。

本记录只覆盖本次真实改变的路径：

| 层 | 落点 |
| --- | --- |
| 公共 DTO | `internal/protocol/read_tree.go`：`ReadNode`、`ReadTree`、`ReadDelta`、`ReadSetNode`、`Defaults`、`ReadBounds`、`ReadTreeValue`，含严格编解码、ref/chain 互斥与视图校验 |
| 观察/补看接线 | `internal/protocol/surface.go`、`responses.go`、`decode_response.go`：`Observation.tree` 与 `inspect` 切换到公共阅读 DTO；tree 作用域的 view/defaults 在嵌套树，subtree/query 在信封声明一次 |
| 公共投影 | `internal/observation/read.go`：`ProjectCompactTree`、`ProjectDetailedTree`、`AnonymousPlain`、`PackTree`、`UnpackTree`、`ReadEqual`、`SnapshotCost` |
| 公共差分 | `internal/observation/read_diff.go`：`ReadDiff`、`ReadApply`，逻辑树展开、定义折链、守恒校验与规范字节成本 |
| 构造器 | `internal/observation/build.go`：`Builder.Candidate` + commit，拒绝的候选不推进 `prev`/`nextRef` |
| 引擎 | `internal/engine/{engine,capture,inspect,retirement}.go`：compact 128 KiB、内部 4/32 MiB、`details.budget`、留存 dry-run、inspect detailed、候选提交点、退休断开投影器 |
| MCP | `internal/mcp/{tree_text,result_text,schema}.go`：`result v2`、`tree v2`、`inspect v2`、块头 defaults 行、chain 行、隐藏几何省略、`set bounds=hidden`、共享语法图例 |
| 探针 | `scripts/mcp-probe/{result_text,result,checks}.go`：v2 文本 oracle、defaults 行与 view 校验、树/补看块前缀 |
| 版本 | `release/versions.json` 与 `scripts/generate-version.ps1` 的四个生成目标：`0.11.0` / `11` / `13` |
| 正式文档 | `docs/` 8 个受影响文件（interface、mcp、architecture、client、integration、release、runbook、control-protocol、phone-operations）；独立只读审计后复检 CLEAN |

## 2. 构建与静态检查（实际执行）

环境：Windows，`go version go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`。

```text
go build ./...                                              → PASS
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...        → PASS
go vet ./...                                                → PASS
./scripts/generate-version.ps1                              → generated: release=0.11.0 contract=11 android_version_code=13
./scripts/generate-version.ps1 -Check                       → version source consistent: release=0.11.0 contract=11 android_version_code=13
```

## 3. 单元测试（实际执行）

```text
GOEXPERIMENT=jsonv2 go test -count=1 ./...
ok  cmd/device-mcp            5.0s
?   cmd/device-service        [no test files]
ok  internal/android        16.2s
ok  internal/client          4.9s
ok  internal/device          (cached)
ok  internal/engine        297.6s
?   internal/eventlog        [no test files]
ok  internal/httpapi         6.7s
ok  internal/imaging         0.2s
ok  internal/mcp             1.6s
ok  internal/observation     0.9s
ok  internal/protocol        2.5s
ok  internal/store           3.4s
ok  scripts/mcp-probe       14.2s
```

本次新增或改写的证明窗口：

| 用例 | 证明 |
| --- | --- |
| `observation.TestProjectCompactCheckedDefault` | 完整节点的 `checked` 三态：已知 `false` 省略、`true` 与 `null` 显式；解码把缺省恢复为已知 `false`，显式 `null` 仍是未知 |
| `observation.TestReadSetPreservesExplicitValues` | `set` 的显式 `false`、`""`、`[]`、`null`、`bounds:"hidden"` 全部保留；省略只表示沿用基线 |
| `observation.TestGeometryStatesAreDistinct` | hidden/unknown/known 两两不等；源未知不被标成隐藏；detailed 不隐藏且通过校验 |
| `observation.TestAnonymousPlainPredicate` | 匿名判据逐项覆盖：动作、内容、空字符串字段、轴、未知/非默认布尔、非普通角色都保留独立节点与几何 |
| `observation.TestPackFoldsOnlyLegalChains` | 仅合法连续后代链折叠；同级、功能节点、定义根不折；全部 ref 与顺序保留；Unpack 可逆 |
| `observation.TestPackIsDeterministic` | 同一逻辑树重复投影/折链产生相同字节 |
| `observation.TestChainMembersWithDifferentInternalRects` | 内部矩形不同但公共几何都隐藏可折链；detailed 恢复各自矩形 |
| `observation.TestReadDeltaFollowsDisplayStateChanges` | 获得动作但矩形不变时公共 delta 补 `actions` 与已知几何；反向恢复为 `actions=[]` 与 `bounds:"hidden"` |
| `observation.TestReadDeltaChildrenReferenceChainMember` | `children` 可引用基线中的 chain 中间 ref，`Apply` 与当前树一致 |
| `observation.TestReadDeltaSameBaselineReplacesPreviousDelta` | 同一基线的新 delta 不继承上一 delta；`D2` 省略字段时恢复基线值 |
| `observation.TestReadDeltaRejectsRoleChange` | 角色变化没有合法 delta（不暗改语义） |
| `observation.TestCompactProjectionReducesRedundantEncoding` | 30 层可压链 + 100 个匿名节点的合成 fixture：compact canonical JSON 至少缩小 25%，detailed 不小于 compact |
| `observation.TestSnapshotCostIsDeterministic` | 内部计费对象 `{raw_capture,full_tree}` 的字节成本可重复 |
| `observation.TestCandidateDoesNotAdvanceContinuity` / `TestCandidateCommitPublishesExactState` | 未提交候选不推进 `prev`/下一 ref；提交后精确发布候选状态 |
| `engine.TestTreeBudgetsNameTheirBudget` | 三个预算分别返回 `tree_too_large` 与 `details.budget=public_full/internal_snapshot/retained_snapshots`、数值 `limit`；拒绝时当前观察与留存条数不变，已确认基线不回滚；恢复预算后同一基线产生空 delta |
| `mcp.TestTreeText*Golden` / `TestTreeTextChainDefinition` / `TestTreeTextRoundTrip` | v2 块头、defaults 行、`entries` 计数、chain 行、隐藏几何无列、`checked` 三态与文本→typed 往返 |
| `mcp.TestCatalogFingerprintBaseline` | 11 工具目录指纹随共享图例更新后稳定 |
| `mcp.TestResultFieldManifest*` | 文本分类表覆盖 `ReadTree`/`ReadNode`/`ReadDelta`/`ReadSetNode`/`ReadBounds` 的全部公共成员 |
| `protocol.TestDecode*` | 新响应形状严格往返：full/delta、inspection 三种 scope、subtree/query 的 view/defaults 位置 |
| `mcp-probe` 全量 | 三种结果模式同源、v2 text↔structured 对象一致、探针 catalog/负面用例 |

## 4. 反向对照（先红后绿，实际执行）

每个变异都在应用后运行对应用例并观察失败，恢复后重新运行通过：

| 变异 | 观测 |
| --- | --- |
| 完整节点始终写 `checked`（去掉 `false` 省略） | `TestProjectCompactCheckedDefault` 失败：`known false checked must be omitted` |
| 关闭匿名几何隐藏（`compact && AnonymousPlain` 恒 false） | `TestGeometryStatesAreDistinct`、`TestPackFoldsOnlyLegalChains` 失败；高冗余 fixture 只缩小 22%（未达 25%） |
| `Candidate` 立即提交（去掉回滚） | `TestCandidateDoesNotAdvanceContinuity` 失败：`a candidate must not publish itself` |
| 公共差分不比较几何状态 | `TestReadDeltaFollowsDisplayStateChanges` 失败：delta 无法重建当前树 |

## 5. 未执行项与原因

| 项 | 状态与原因 |
| --- | --- |
| 整组发布构建、`verify-manifest.ps1`、`build-host-tools.ps1 -VerifyOnly` | 未执行：主机 MCP 适配器正从 `build/device-mcp.exe` 运行（PID 12964），而 `build-release.ps1` 的输出路径就是该运行镜像。按提案 §8.2，覆盖前必须先停止精确旧进程或改在干净发布工作区构建；本会话不重启用户宿主进程。因此也没有可由 `verify-manifest.ps1` 核验的、来自本次源码身份的清单 |
| 手机整组部署与实机验收（compact/detailed、三种结果模式、双端目录、最小闭环） | 未执行：本次没有受控设备窗口，部署会更换用户手机版本；提案 §8.2 的实机项整体未做 |
| 真实 Capture 的新旧表示对比、text/canonical 字节比、token 计数 | 未执行：原始采集与树不入库，且本次未连接设备；只用合成 fixture 证明高冗余场景 ≥25%，不外推真实比例 |
| 投影/编码耗时与进程峰值 | 未执行：同上，不作为内存或延时结论 |
| `verify-m6.ps1` | 未执行（需要已部署手机）。本次只把它硬编码的“上一发行契约”从 9 修订为 10，与 `contract_version=11` 和 runbook 表述一致；修订后未运行 |
| 契约 10 客户端/App 被契约 11 拒绝的实机验证 | 未执行：没有部署 0.11.0，也没有旧客户端联调窗口；该边界由既有精确版本检查实现，未取得本次实机证据 |
| 主机适配器替换与 MCP 宿主重新发现目录 | 未执行：不重启用户宿主，不覆盖运行镜像 |

## 6. 与提案的偏离

- 候选式构造采用 `Builder.Candidate(capture) (snapshot, commit, error)`，由引擎在唯一提交点调用 `commit`；没有另做 clone+commit 结构，语义与提案 §5.1 一致。
- 内部留存计费按观察记录中唯一快照的 `Cost` 累计；`baseline` 只保存 compact 阅读表示，不额外持有内部快照。退休会话在 `retireLocked` 断开 `session.Projector` 引用，但不对仍可能被旧 work 持有的投影器调用 `Reset`（避免与在途预览竞争）；在途 work 的局部对象不计入长期留存。提案允许这一边界。
- `observationState` 不再保存 `Projector` 字段：历史 O 只保留完整快照与标量元数据，输入预览使用 work 自己的投影器。点命中证明只需投影的 `Source` 映射，不依赖 ref 连续性，因此使用引擎默认投影器。
- 产品代码只负责文本编码；文本逆解析以测试侧 oracle 实现（`internal/mcp/tree_text_test.go`、`scripts/mcp-probe/result_text.go`），未新增生产解析入口。
- 提案 §6.2 表中的 `MaxObservationBytes` 命名未沿用；三个预算使用 `MaxFullTreeBytes`（128 KiB）、`MaxSnapshotCostBytes`（4 MiB）、`MaxRetainedSnapshotCostBytes`（32 MiB）与 `MaxInspectionUnitBytes`（32 KiB）。
- 由于第 5 节的原因，提案 §8.2 的发布/部署/实机步骤没有执行；这些是未执行项，不是已完成结论。
