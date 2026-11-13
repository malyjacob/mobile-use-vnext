# 验证记录：统一压缩公共节点树并按交付表示计算预算

状态：2026-11-13。本次涉及实机：**是**（0.11.0 整组升级到手机，实机执行契约拒绝、最小 STOP、
full/delta/inspect 最小闭环与 stdio/手机 HTTP 两种 MCP 入口的三模式探针）。
实现提交：`4fc86cf26c40be1df099a1ea259c99ca7a8d5abc`。提案：
[compact-node-tree-presentation](../proposals/implemented/compact-node-tree-presentation.md)。
基线提交：`25db973`；提案落盘提交：`2781ffb`；发布构建源码身份：`3a4f579`（干净树）。

## 1. 分诊与范围

取舍类：改变公开表示、默认值、交付预算与共同契约，不是“现行规范说得对、实现走偏”。
讨论与提案已按流程完成，提案由用户确认后实施。

本记录覆盖本次真实改变的路径：

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
| 正式文档 | `docs/` 9 个受影响文件；独立只读审计 8 项发现修正后复检 CLEAN |

## 2. 构建与静态检查（实际执行）

环境：Windows，`go version go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`。

```text
go build ./...                                              → PASS
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...        → PASS
go vet ./...                                                → PASS
./scripts/generate-version.ps1 -Check                       → version source consistent: release=0.11.0 contract=11 android_version_code=13
```

## 3. 单元测试（实际执行）

```text
GOEXPERIMENT=jsonv2 go test -count=1 ./...
ok  cmd/device-mcp            0.7s
?   cmd/device-service        [no test files]
ok  internal/android        10.0s
ok  internal/client          0.8s
ok  internal/device          0.6s
ok  internal/engine        297.7s
?   internal/eventlog        [no test files]
ok  internal/httpapi         1.0s
ok  internal/imaging         0.2s
ok  internal/mcp             1.5s
ok  internal/observation     1.1s
ok  internal/protocol        1.1s
ok  internal/store           2.2s
ok  scripts/mcp-probe       10.5s
```

本次新增或改写的证明窗口：

| 用例 | 证明 |
| --- | --- |
| `observation.TestProjectCompactCheckedDefault` | 完整节点 `checked` 三态：已知 `false` 省略、`true` 与 `null` 显式；解码把缺省恢复为已知 `false`，显式 `null` 仍是未知 |
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

| 变异 | 观测 |
| --- | --- |
| 完整节点始终写 `checked`（去掉 `false` 省略） | `TestProjectCompactCheckedDefault` 失败：`known false checked must be omitted` |
| 关闭匿名几何隐藏（`compact && AnonymousPlain` 恒 false） | `TestGeometryStatesAreDistinct`、`TestPackFoldsOnlyLegalChains` 失败；高冗余 fixture 只缩小 22%（未达 25%） |
| `Candidate` 立即提交（去掉回滚） | `TestCandidateDoesNotAdvanceContinuity` 失败：`a candidate must not publish itself` |
| 公共差分不比较几何状态 | `TestReadDeltaFollowsDisplayStateChanges` 失败：delta 无法重建当前树 |

## 5. 发布构建、整组部署与实机验收（实际执行）

发布侧（干净树 `3a4f579`）：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

真实结果：

- `manifest.json`：`release=0.11.0`、`contract_version=11`、`android=0.11.0/13`、
  `source_commit=3a4f579…`、`source_dirty=false`、6 个产物、工具目录 11/`fcf3ca14…`；
  `verify-manifest.ps1` → PASS。
- 产物 SHA-256：`device-service` `3228ac86…`、`device-mcp` `d5bf5a3c…`、
  `control.apk` `5a0fd156…`、`u2.jar` `28361193…`、`applist.dex` `63818f32…`、
  `service.sh` `4b568a9a…`。
- 主机工具：`device-mcp.exe` `c4901afa…`、`mcp-probe.exe` `bc819279…`；
  `host-tools-manifest.json` 记录同一 source_commit/契约/目录指纹；`-VerifyOnly` → PASS，
  包含手机清单配套校验。
- 构建时运行中的旧主机镜像无法删除备份，`.device-mcp.exe.previous-*` 按设计保留；磁盘上的
  `build/device-mcp.exe` 已是新镜像。

手机整组部署：

```powershell
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json
```

真实结果：

- 停止控制 App、停止服务与手机 MCP；原子替换 6 个清单产物 + `config.json` + `mcp-config.json`，
  逐项 SHA-256 与清单一致；`adb install -r` 覆盖安装，`versionName=0.11.0`、`versionCode=13`。
- `/health`：`status=ok`、`release_version=0.11.0`、`contract_version=11`；
  `can_start=true`；`cleanup=complete`（execution/tasks/display 均 confirmed）。
- 手机 HTTP MCP：`127.0.0.1:18882`（主机转发 `18883`），运行镜像
  `d5bf5a3c…` 与清单一致；工具目录 11 项、指纹 `fcf3ca14…`。
- 私有数据保留：App `files/connection.json`（新）、`files/messages.json`（旧时间戳保留）；
  chroot `recovery-ledger.json` 存在且 `format=mobile-use-vnext-ledger`、`version=5`；未卸载、
  未清库、未删账本。
- 未重启手机。

契约边界（实机 HTTP）：

| 请求 | 结果 |
| --- | --- |
| `/api/status` + `X-Mobile-Use-Contract: 10` | `409 contract_mismatch`，`recovery.entry=update`、`reference=none` |
| `/api/status` + `X-Mobile-Use-Contract: 11` | 200，`can_start=true` |
| `/v1/control/sync`，body `contract_version="10"` | `409 contract_mismatch`（控制端与服务契约不匹配），`recovery.entry=update` |
| `/v1/control/stop`，body `contract_version="10"`，无普通契约头 | 200 `accepted=true`、`state=recorded`；4 秒后 App 同步，`can_start=true` |

最小闭环（手机虚拟屏，真实 HTTP）：

| 步骤 | 结果 |
| --- | --- |
| `start {}` | 新会话；首个观察 `format=full view=compact`，`checked` 默认与匿名几何按新表示输出 |
| `observe(O1)` | 确认 O1 为基线；返回 `format=delta base=O1`，五块均为空（页面未变） |
| `inspect(O2)`（整树） | `scope=tree`、嵌套 `view=detailed`；不采集、不续租、不确认基线 |
| `observe(O2)` | 仍以 O1 为 `base`，空 delta —— inspect 插入未改变基线 |
| `inspect(O3, query="设置")` | `scope=query`、`view=detailed`、`total=0`、`truncated=false`、空命中 |
| `end(P)` | `outcome=executed`、会话 `ended`；随后 cleanup 完成 |

MCP 入口（新主机适配器 stdio 与手机 HTTP MCP）：

| 入口 | 模式 | 结果 |
| --- | --- | --- |
| stdio（`build/device-mcp.exe` + 既有私有配置） | `both` / `text` / `structured` | 三次均 `RESULT PASS`：11 工具、catalog 与两份清单指纹一致、start→full、observe、inspect 文本/结构化块、act `executed`（quality `stable/3`）、end、取消恢复 |
| 手机 HTTP（`-transport http-remote`，主机转发 18883） | `both` / `text` / `structured` | 三次均 `RESULT PASS`：`contract=11 release=0.11.0`、无 token 401、恶意 Origin 403、合法无 Origin 200、agent token 被拒 401、目录指纹一致、完整最小闭环 |

## 6. 未执行项与原因

| 项 | 状态与原因 |
| --- | --- |
| `verify-m6.ps1` | **未执行**：用户在本次要求中明确排除该脚本（其破坏性重启/SIGTERM 演练不在本次范围）。脚本的上一契约断言已在实现提交中修订为 10，未运行 |
| 真实 Capture 的新旧表示对比、text/canonical 字节比、token 计数 | **未执行**：用户明确排除；原始采集与树不入库，本次未离线比较。合成高冗余 fixture 只证明 ≥25%，不外推真实比例 |
| 投影/编码耗时与进程峰值 | **未执行**：用户明确排除；不作为内存或延时结论 |
| `act` 重验失败交付新观察（实机） | **未构造**：需要在动作派发前稳定地让目标树变化，本次未找到不干扰用户手机的可重复注入方式；该路径由引擎既有门禁与单测/探针 fake 路径覆盖，但没有本次实机样本 |
| 契约 10 旧 App 实机联调 | **未执行**：App 随整组升级到 0.11.0，现场没有旧 App；只验证了契约 10 的普通 API 头与控制同步被 409 拒绝 |
| 主机 DSH 连接器加载新镜像 | **未执行**：按用户要求由其自行恢复宿主依赖；新 `build/device-mcp.exe` 已落盘并由 stdio 探针实际拉起验证，运行中的 DSH 子进程仍执行旧镜像，直到宿主重启 |
| 手机侧旧 stdio 宿主（hermes）恢复 | **未执行**：部署停止的旧 stdio 进程需其宿主自行重启；`dsh-mcp-connector` 已自动重启并运行新二进制（进程年龄晚于安装），hermes 宿主未在本次窗口重启 |
| `verify-m5.ps1` | **未使用**：它会重写私有配置，违反只替换产物、保留配置的边界 |

## 7. 与提案的偏离

- 候选式构造采用 `Builder.Candidate(capture) (snapshot, commit, error)`，由引擎在唯一提交点调用 `commit`；没有另做 clone+commit 结构，语义与提案 §5.1 一致。
- 内部留存计费按观察记录中唯一快照的 `Cost` 累计；`baseline` 只保存 compact 阅读表示，不额外持有内部快照。退休会话在 `retireLocked` 断开 `session.Projector` 引用，但不对仍可能被旧 work 持有的投影器调用 `Reset`（避免与在途预览竞争）；在途 work 的局部对象不计入长期留存。提案允许这一边界。
- `observationState` 不再保存 `Projector` 字段：历史 O 只保留完整快照与标量元数据，输入预览使用 work 自己的投影器。点命中证明只需投影的 `Source` 映射，不依赖 ref 连续性，因此使用引擎默认投影器。
- 产品代码只负责文本编码；文本逆解析以测试侧 oracle 实现（`internal/mcp/tree_text_test.go`、`scripts/mcp-probe/result_text.go`），未新增生产解析入口。
- 提案 §6.2 表中的 `MaxObservationBytes` 命名未沿用；三个预算使用 `MaxFullTreeBytes`（128 KiB）、`MaxSnapshotCostBytes`（4 MiB）、`MaxRetainedSnapshotCostBytes`（32 MiB）与 `MaxInspectionUnitBytes`（32 KiB）。
- 手机 HTTP MCP 的 `allowed_origins` 为空；探针按该真实策略验证“任意 Origin 403、无 Origin 200”，没有为了通过检查而改写私有配置。
- `verify-m6` 的上一契约断言从 9 修订为 10，与 `contract_version=11` 和 runbook 表述一致；该脚本本次未运行。
