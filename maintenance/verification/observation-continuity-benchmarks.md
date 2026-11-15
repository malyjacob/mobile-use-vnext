# 观察连续性 S6 统一基准记录

- 状态：S6 缺项已补齐并完成最终统一重跑；B01–B06 当前侧与 B01/B06 `6ec25c8` 基线均使用同一最终驱动、工具链和主机。
- 涉及实机：**否**。本文只记录 Windows 主机的合成 fixture、Go benchmark 与仓库内 `6ec25c8` 对照。
- 正式执行日期：`2026-11-16`（Asia/Shanghai）。
- 源码基准锚点：`bd770565cedde2391a55ab2a49e87a84adc6edba`，加本文列出的 S6 候选文件修改。
- 旧基线：`6ec25c87daefbc01c99f78d968214304641d119d`。
- 提案依据：[分册 05 §8.3/§10](../proposals/observation-continuity-and-revalidation/05-implementation-and-validation-plan.md) 与分册 01–04 的预算边界。
- 最终统一驱动：[internal/observation/s6_benchmark_test.go](../../internal/observation/s6_benchmark_test.go)，SHA-256 `316e3a5e585090c78e0cc258092b39f22593ecfe8601a79713bf66ca304b9d0a`。
- 最终候选文件：`match.go` SHA-256 `e4600051b1dcadb50dd25d1e79eb0607b154203ba1a5ce275849d84a09b49bf7`；`delta_benchmark_test.go` SHA-256 `10d825c2035f07f65d7c0d646d67b04af5e46d06d88478a9ddad6feae244bb2d`。
- 环境：Windows 主机，`go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0`，CPU `AMD Ryzen 7 5700X 8-Core Processor`。

## 1. 隔离与兼容边界

主工作树在准备期间存在其他并行修改，因此正式当前组不直接使用含未收敛改动的主工作树。当前组和旧基线组分别使用同仓库临时干净 worktree：

- 当前组：`.state/s6-current-bd77056`，HEAD `bd770565cedde2391a55ab2a49e87a84adc6edba`，叠加上文三个最终候选文件；未复制主工作树其他并行修改。
- 旧基线：`.state/s6-baseline-6ec25c8`，HEAD `6ec25c87daefbc01c99f78d968214304641d119d`，只额外复制同一 benchmark 文件。
- 两边驱动 SHA-256 相同；当前 worktree 只增加内部诊断计数与 B03/B04 测试驱动，基线 worktree 只增加兼容 driver。未复制 `docs/`、版本源或其他主验证记录。

兼容性只存在于新增测试代码：

- 用 reflection 读取当前 `Builder.lastStats` 的 `workUsed`、`workLimit`、`budgetHit`、`candidateVisits`、`voteComparisons` 及 accepted Stage 3 matched 计数；基线没有这些字段时报告 `match_stats_present=0`。
- 仅测试代码通过私有字段接缝把当前 Builder 的预算上限压到 `1`，用于确定性触发预算截止；基线没有该字段时报告 `budget_supported=0`，不伪造截止结果。
- B01 在两个版本都验证语义兄弟仍在 raw root 下；当前版本额外断言空叶过滤后的公开节点数。基线保留旧过滤行为时仍可完成同一 benchmark，不因版本差异中止对照。

## 2. 驱动与指标

新增 benchmark：

| ID | Benchmark | 内容 |
| --- | --- | --- |
| B01 | `BenchmarkS6B01Purification` | raw root 下 32/256 个已知默认空叶及一个文本语义兄弟 |
| B02 | `BenchmarkS6B02Continuity` | 五卡 B–E 保号、F 不复用旧号；32 行重复无锚点另计 |
| B04 | `BenchmarkS6B04Matching` | 小树、48 层深树、200 张重复模板、约 20,000 raw 节点 |
| B06 | `BenchmarkS6B06Boundaries` | 五布尔未知、4096 字节文本、512 层 chain、预算截止与完整阅读差分链路 |

既有驱动继续作为 B03/B05 的正式入口：

- B03：`BenchmarkStructuralDeltaB03`，覆盖 32/256/2048/20000 节点与 sparse updates、single property、subtree wrap、subtree unwrap、既有父间 subtree reparent、unchanged；每项在计时前同时验证 `ReadEqual` 与 canonical JSON 精确还原，并断言 `10*deltaBytes < 7*fullBytes`。
- B05：`BenchmarkActRevalidationB05`，报告 candidate visits、lineage steps、tiers、node pairs、候选上限及 `-benchmem`；20k 零/单/多候选、深度 1 ancestor mismatch、target+carrier、carrier/scroll/text 路径均保留。

统一指标定义：

- `raw_nodes/op`：当前合成 capture 的 raw 节点数。
- `public_nodes/op`：公开树节点数，包含合成 root。
- `full_bytes/op`、`read_full_bytes/op`、`read_delta_bytes/op`：对应 canonical JSON 字节数。
- `snapshot_cost/op`：`observation.SnapshotCost(capture, tree)` 的内部成本值。
- `work_units/op`、`work_limit/op`：Builder 的已用/上限确定性工作量。
- `candidate_visits/op`：每次成功 `charge(1)` 后实际扫描一个旧侧或当前侧候选公开节点时加一；包含各匹配轮次中的重复访问，不统计 bucket 后处理或 proposal 端点校验。
- `vote_comparisons/op`：Stage 3 中为确定候选父而检查一个 `R_c` 匹配对，以及在 `samePairSet` 中实际比较一个 `R_c/R_p` 集合成员时各加一；长度不等的短路不伪计成员比较。
- `vote_matches/op`：当前实现 `matchStats.stage3Matched`，即 descendant-pair vote 接受的匹配数；保留为 accepted 结果，不代替比较数。
- `new_refs/op`：当前公开 ref 集合相对上一公开快照新增的数量；首次构建按全部公开 ref 计。
- `preserved_refs/op`：两次公开 ref 集合的交集数量。
- `ns/op`、`B/op`、`allocs/op`：Go testing 与 `-benchmem` 的原始输出。

B06 的 `build_read_diff_json_apply` 每次操作实际执行：

```text
Builder.Build(base)
Builder.Build(current)
ProjectCompactTree(base/current)
ReadDiff(base, current)
protocol.JSON(current/full)
protocol.JSON(delta)
protocol.Decode(delta JSON)
ReadApply(base, decoded delta)
ReadEqual + canonical JSON equality
```

合成 current 同时包含 64 层匿名 chain、4096 字节文本、32 个五布尔未知节点和一个新增语义兄弟。

## 3. 覆盖核对

| ID | 当前覆盖 | 缺口处理 |
| --- | --- | --- |
| B01 | 本次新增 32/256 两档，记录过滤数、语义兄弟、节点数、字节、SnapshotCost 与分配 | 补齐 |
| B02 | 原驱动验证 B–E 保号、F 新号并记录 `work/op`；新驱动增加公开摘要、编码、SnapshotCost、Stage 3 vote 与 new/preserved ref 计数，并补重复无锚点样本 | 补齐指标与样本，不改既有测试 |
| B03 | 四档规模与 sparse、single、wrap、unwrap、既有父间 reparent、unchanged；精确还原与 70% 严格门禁、delta/full 字节及分配 | 补齐 wrap/unwrap/reparent 缺项 |
| B04 | 既有小树/深树/重复模板/20k 与 `work/op`；新驱动报告真实 candidate visits、vote comparisons、accepted votes、raw/public/bytes/SnapshotCost/newref | 补齐实际计数 |
| B05 | 既有 20k 零/单/多候选、lineage、target+carrier、scroll/text 与候选上限 | 复用；不新增 engine 测试文件 |
| B06 | 本次新增五布尔未知、长文本、chain、预算截止与 Build→Read→ReadDiff→JSON→ReadApply | 补齐 |

## 4. 已执行的非正式验证

以下均不是正式分布结论。`benchtime=1x` 只用于证明 fixture、版本兼容路径和指标非空。

```powershell
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

go test ./internal/observation -run '^$' -bench '^BenchmarkS6' `
  -benchmem -benchtime=1x -count=1
```

当前侧关键单次结果：

| 场景 | 关键结果 |
| --- | --- |
| B01/32 | raw `34`，public `2`，过滤空叶 `32`，full `240 B`，SnapshotCost `23670` |
| B01/256 | raw `258`，public `2`，过滤空叶 `256`，full `240 B`，SnapshotCost `177854` |
| B02/五卡 | public `17`，new refs `3`，preserved refs `14`，Stage 3 vote `4`，work `428/70016` |
| B04/20k | raw/public `19997/19997`，Stage 3 vote `3998`，work `539765/5184896` |
| B06/五布尔未知 | 256 个五布尔未知节点，即 `1280` 个未知布尔 |
| B06/chain | 512 refs 折成 1 个 chain definition |
| B06/预算截止 | `budget_supported=1`、`budget_hit=1`、`work=1/1`，public refs 仍唯一 |
| B06/端到端 | `read_apply_equal=1`，full `9989 B`，delta `445 B`，`delta/full=4.455%` |

同一驱动在 `6ec25c8` 的单次对照：

| 场景 | 旧基线关键结果 |
| --- | --- |
| B01/32 | raw `34`，public `34`，过滤空叶 `0`，full `1173 B` |
| B01/256 | raw `258`，public `258`，过滤空叶 `0`，full `7604 B` |
| B06/预算截止 | `budget_supported=0`、`budget_hit=0`；旧实现没有该确定性预算接缝 |
| B06/端到端 | `read_apply_equal=1`，full `10033 B`，delta `474 B`，`delta/full=4.724%` |

当前侧 `go test ./internal/observation -count=1` 与 `go vet ./internal/observation` 通过。B01–B06 的非空列表检查分别得到：

```text
B01 BenchmarkS6B01Purification
B02 BenchmarkContinuityScrollFiveCards, BenchmarkS6B02Continuity
B03 BenchmarkStructuralDeltaB03
B04 BenchmarkContinuityTreeShapes, BenchmarkS6B04Matching
B05 BenchmarkActRevalidationB05
B06 BenchmarkS6B06Boundaries
```

B03/B04/B05 的干净当前 worktree `benchtime=1x` 路径检查均通过；B05 的 20k ancestor-mismatch 预检单次样本报告 `32475552 B/op`、`200464 allocs/op`。最终 5 次分布见 §7.5，不以该预检值代替正式结果。

## 5. 第一轮正式命令与结果（追溯）

收到“开始正式基准”后，每个 ID 先在当前组重新运行非空 `-list`，再以 `-benchtime=1x -count=1` 预热，最后使用 `-benchmem -count=5`。B01/B06 的基线组执行同样的列表、预热和正式命令。当前组 18 条、基线组 6 条，共 24 条正式阶段命令；全部退出码为 `0`，没有失败重试。原始 stdout/stderr 保存在 `.state/s6-benchmarks/formal-*.log`。

两个 worktree 使用相同环境设置：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
```

`formal-environment.log` 与 `formal-baseline-environment.log` 均记录 `go version go1.27.1 windows/amd64`、`GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`、`GOOS=windows`、`GOARCH=amd64`、8 核/16 逻辑处理器、对应 worktree HEAD 与驱动 SHA-256。

当前组非空列表命令（均 `exit 0`，输出见 `formal-list-b01.log` 至 `formal-list-b06.log`）：

```powershell
go test ./internal/observation -list '^BenchmarkS6B01Purification$'
go test ./internal/observation -list '^Benchmark(ContinuityScrollFiveCards|S6B02Continuity)$'
go test ./internal/observation -list '^BenchmarkStructuralDeltaB03$'
go test ./internal/observation -list '^Benchmark(ContinuityTreeShapes|S6B04Matching)$'
go test ./internal/engine -list '^BenchmarkActRevalidationB05$'
go test ./internal/observation -list '^BenchmarkS6B06Boundaries$'
```

当前组预热命令（`-benchtime=1x -count=1`，均 `exit 0`，输出见 `formal-warmup-b01.log` 至 `formal-warmup-b06.log`）：

```powershell
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B01Purification$' -benchmem -benchtime=1x -count=1
go test ./internal/observation -run '^$' -bench '^Benchmark(ContinuityScrollFiveCards|S6B02Continuity)$' -benchmem -benchtime=1x -count=1
go test ./internal/observation -run '^$' -bench '^BenchmarkStructuralDeltaB03$' -benchmem -benchtime=1x -count=1
go test ./internal/observation -run '^$' -bench '^Benchmark(ContinuityTreeShapes|S6B04Matching)$' -benchmem -benchtime=1x -count=1
go test ./internal/engine -run '^$' -bench '^BenchmarkActRevalidationB05$' -benchmem -benchtime=1x -count=1
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B06Boundaries$' -benchmem -benchtime=1x -count=1
```

基线组在 `.state/s6-baseline-6ec25c8` 中执行相同的两组 `-list`、`-benchtime=1x -count=1` 预热和 `-benchmem -count=5` 正式命令：

```powershell
go test ./internal/observation -list '^BenchmarkS6B01Purification$'
go test ./internal/observation -list '^BenchmarkS6B06Boundaries$'
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B01Purification$' -benchmem -benchtime=1x -count=1
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B06Boundaries$' -benchmem -benchtime=1x -count=1
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B01Purification$' -benchmem -count=5
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B06Boundaries$' -benchmem -count=5
```

当前组命令：

```powershell
go test ./internal/observation -run '^$' `
  -bench '^BenchmarkS6B01Purification$' -benchmem -count=5
go test ./internal/observation -run '^$' `
  -bench '^Benchmark(ContinuityScrollFiveCards|S6B02Continuity)$' -benchmem -count=5
go test ./internal/observation -run '^$' `
  -bench '^BenchmarkStructuralDeltaB03$' -benchmem -count=5
go test ./internal/observation -run '^$' `
  -bench '^Benchmark(ContinuityTreeShapes|S6B04Matching)$' -benchmem -count=5
go test ./internal/engine -run '^$' `
  -bench '^BenchmarkActRevalidationB05$' -benchmem -count=5
go test ./internal/observation -run '^$' `
  -bench '^BenchmarkS6B06Boundaries$' -benchmem -count=5
```

B01/B06 旧基线与当前组使用同一条 benchmark 命令，只切换 worktree：

```powershell
go test ./internal/observation -run '^$' `
  -bench '^BenchmarkS6B01Purification$' -benchmem -count=5
go test ./internal/observation -run '^$' `
  -bench '^BenchmarkS6B06Boundaries$' -benchmem -count=5
```

## 6. 第一轮正式结果（追溯，不是最终主表）

8 份正式 benchmark 输出共包含 53 个唯一的五样本子场景：当前侧 46 个，`6ec25c8` 基线侧 7 个。下表保留每个子场景按实际执行顺序排列的 5 次 `ns/op`；中位数是这 5 个值的中位数，`min–max` 同组最小值与最大值。B/op 在日志允许有少量字节波动时显示范围，alloc 数与确定性结构计量未波动时显示单值。原始逐行输出仍以 `.state/s6-benchmarks/formal-*.log` 为准。

正式命令、列表和预热的退出码表：

| ID/侧 | `-list` | 预热 | 正式 | 原始日志 |
| --- | ---: | ---: | ---: | --- |
| B01/当前 | 0 | 0 | 0 | `formal-list-b01.log`、`formal-warmup-b01.log`、`formal-current-b01.log` |
| B02/当前 | 0 | 0 | 0 | `formal-list-b02.log`、`formal-warmup-b02.log`、`formal-current-b02.log` |
| B03/当前 | 0 | 0 | 0 | `formal-list-b03.log`、`formal-warmup-b03.log`、`formal-current-b03.log` |
| B04/当前 | 0 | 0 | 0 | `formal-list-b04.log`、`formal-warmup-b04.log`、`formal-current-b04.log` |
| B05/当前 | 0 | 0 | 0 | `formal-list-b05.log`、`formal-warmup-b05.log`、`formal-current-b05.log` |
| B06/当前 | 0 | 0 | 0 | `formal-list-b06.log`、`formal-warmup-b06.log`、`formal-current-b06.log` |
| B01/`6ec25c8` | 0 | 0 | 0 | `formal-baseline-list-b01.log`、`formal-baseline-warmup-b01.log`、`formal-baseline-b01.log` |
| B06/`6ec25c8` | 0 | 0 | 0 | `formal-baseline-list-b06.log`、`formal-baseline-warmup-b06.log`、`formal-baseline-b06.log` |

### 6.1 B01 净化与空叶

| 侧 | 场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | 过滤空叶 | new/preserved refs | 匹配统计 |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 当前 | `empty_leaves_32` | 4718 / 4954 / 4592 / 4610 / 4636 | 4636 | 4592–4954 | 6144 | 52 | 34/2 | 240 | 23670 | 32 | 2/0 | present `1`，work `0/65920` |
| 当前 | `empty_leaves_256` | 21084 / 20000 / 20242 / 20219 / 22089 | 20242 | 20000–22089 | 31376 | 52 | 258/2 | 240 | 177854 | 256 | 2/0 | present `1`，work `0/65920` |
| `6ec25c8` | `empty_leaves_32` | 31500 / 28782 / 37726 / 31097 / 29172 | 31097 | 28782–37726 | 40744 | 341 | 34/34 | 1173 | 25889 | 0 | 34/0 | 基线无此字段 |
| `6ec25c8` | `empty_leaves_256` | 344322 / 418991 / 444540 / 472255 / 493949 | 444540 | 344322–493949 | 308201–308203 | 2311 | 258/258 | 7604 | 196182 | 0 | 258/0 | 基线无此字段 |

语义兄弟在两侧都保留在 raw root 下。当前实现额外把 32/256 个已知默认空叶全部过滤，公开树只剩 synthetic root 与语义兄弟，因此 full 编码在 32/256 两档都稳定为 `240 B`；基线两档分别编码 `1173 B` 和 `7604 B`。256 档当前中位时间下降 `95.45%`、B/op 下降 `89.82%`、allocs 下降 `97.75%`，这是净化后的真实规模效应。

### 6.2 B02 连续性与重复无锚点

既有 `BenchmarkContinuityScrollFiveCards` 与新 `BenchmarkS6B02Continuity` 同时执行，二者 fixture 与指标集不完全相同，不能把两者的 ns/op 差值解释为同一实现的性能变化。

| 驱动/场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | work 用/限 | vote | new/preserved |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ContinuityScrollFiveCards` | 60451 / 63851 / 66112 / 67404 / 63784 | 63851 | 60451–67404 | 36600 | 376 | — | — | — | 428/— | — | — |
| `S6B02/five_cards_b_to_e_preserved` | 82468 / 68726 / 70048 / 67554 / 64451 | 68726 | 64451–82468 | 36600 | 376 | 17/17 | 2679 | 14993 | 428/70016 | 4 | 3/14 |
| `S6B02/repeated_no_anchor` | 236932 / 232759 / 220076 / 238322 / 206963 | 232759 | 206963–238322 | 169864 | 1250 | 98/98 | 14609 | 85431 | 1517/90752 | 0 | 96/2 |

五卡场景验证 B–E 的整棵子树 ref 保持、A 的 ref 不复用、F 获得新 ref；Stage 3 descendant vote 为 `4`。重复无锚点场景保留 root 与 scroll，其他重复卡片不伪造稳定锚点，得到 `96` 个新 ref、`2` 个 preserved ref，且没有 descendant vote。

### 6.3 B03 结构差分与 70% 门禁

| 操作/规模 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | delta B | delta/full | full B |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `sparse_updates/32` | 211060 / 230985 / 264346 / 288070 / 280640 | 264346 | 211060–288070 | 161981–162048 | 1987–1988 | 146 | 6.237% | 2341 |
| `sparse_updates/256` | 1602499 / 1612474 / 1570977 / 1594406 / 1657701 | 1602499 | 1570977–1657701 | 1323287–1326811 | 14467–14470 | 567 | 3.084% | 18384 |
| `sparse_updates/2048` | 13907302 / 13234628 / 13716042 / 13768716 / 13845927 | 13768716 | 13234628–13907302 | 11310997–11311172 | 113362 | 595 | 0.398% | 149503 |
| `sparse_updates/20000` | 140039138 / 140260775 / 136915775 / 132438638 / 133100550 | 136915775 | 132438638–140260775 | 105703324–105703898 | 1102878–1102879 | 623 | 0.04159% | 1497950 |
| `single_property/32` | 239984 / 240488 / 229939 / 281389 / 301960 | 240488 | 229939–301960 | 161382–161405 | 1982 | 89 | 3.810% | 2336 |
| `single_property/256` | 1546950 / 1550858 / 1547401 / 1473012 / 1523301 | 1546950 | 1473012–1550858 | 1317626–1320603 | 14418–14420 | 90 | 0.4911% | 18326 |
| `single_property/2048` | 13746898 / 13658413 / 14144704 / 13747415 / 13827686 | 13747415 | 13658413–14144704 | 11304745–11305227 | 113312 | 91 | 0.06089% | 149444 |
| `single_property/20000` | 139521800 / 129671900 / 136644025 / 132547988 / 131983775 | 132547988 | 129671900–139521800 | 105696853–105697730 | 1102828–1102829 | 92 | 0.006142% | 1497891 |
| `subtree_reparent/32` | 231170 / 235448 / 220231 / 222032 / 223238 | 223238 | 220231–235448 | 166572–166616 | 2065 | 178 | 7.361% | 2418 |
| `subtree_reparent/256` | 1594910 / 1444344 / 1520293 / 1545445 / 1438232 | 1520293 | 1438232–1594910 | 1322723–1326156 | 14505–14507 | 178 | 0.9669% | 18409 |
| `subtree_reparent/2048` | 13353378 / 13685759 / 13459007 / 13389482 / 13426669 | 13426669 | 13353378–13685759 | 11325925–11326251 | 113404 | 178 | 0.1190% | 149528 |
| `subtree_reparent/20000` | 132178075 / 132007200 / 134205425 / 131852112 / 138338850 | 132178075 | 131852112–138338850 | 105702077–105702724 | 1102920–1102921 | 178 | 0.01188% | 1497975 |
| `unchanged/32` | 219760 / 211746 / 213493 / 196027 / 218131 | 213493 | 196027–219760 | 160789–160819 | 1974–1975 | 47 | 2.018% | 2329 |
| `unchanged/256` | 1429800 / 1552269 / 1520763 / 1465033 / 1460270 | 1465033 | 1429800–1552269 | 1316862–1320125 | 14411–14413 | 47 | 0.2566% | 18320 |
| `unchanged/2048` | 13161088 / 12797781 / 12891204 / 12865756 / 13226058 | 12891204 | 12797781–13226058 | 11304202–11304459 | 113305 | 47 | 0.03145% | 149439 |
| `unchanged/20000` | 135539200 / 132014275 / 138090850 / 134864425 / 133566475 | 134864425 | 132014275–138090850 | 105696259–105697160 | 1102821–1102822 | 47 | 0.003138% | 1497886 |

16 个子场景全部满足既有严格门禁 `10*deltaBytes < 7*fullBytes`；最大比为 `subtree_reparent/32` 的 `7.361%`，仍远低于 70% 上限。预算与 70% 判据均未调整。

### 6.4 B04 树形态与约 20k 节点

既有 `ContinuityTreeShapes` 与新 `S6B04Matching` 都保留正式结果。两者用于覆盖既有边界和补充 raw/public、编码、SnapshotCost、vote/newref；fixture 并非逐项完全相同，跨驱动 ns/op 不做归因。

| 驱动/场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | work | vote | new/preserved |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ContinuityTreeShapes/small_tree` | 47647 / 47338 / 46862 / 44936 / 45032 | 46862 | 44936–47647 | 25792 | 301 | — | — | — | 295 | — | — |
| `ContinuityTreeShapes/deep_nested_containers` | 300772 / 281403 / 260019 / 291475 / 275649 | 281403 | 260019–300772 | 192128–192129 | 1271 | — | — | — | 1576 | — | — |
| `ContinuityTreeShapes/repeated_template_cards` | 3579244 / 3347407 / 3286889 / 3205881 / 2865192 | 3286889 | 2865192–3579244 | 2177058–2177074 | 10499 | — | — | — | 21753 | — | — |
| `ContinuityTreeShapes/large_twenty_thousand_raw_nodes` | 62560400 / 63112684 / 63229263 / 62967895 / 65486211 | 63112684 | 62560400–65486211 | 64286299–64286619 | 253396 | — | — | — | 539765 | — | — |
| `S6B04/small_tree` | 133810 / 135180 / 129970 / 133230 / 136867 | 133810 | 129970–136867 | 135944 | 555 | 25/25 | 3815 | 21895 | 803/72064 | 0 | 0/25 |
| `S6B04/deep_nested_containers` | 219133 / 214217 / 213848 / 213215 / 213246 | 213848 | 213215–219133 | 192128 | 1271 | 50/50 | 7357 | 42842 | 1576/78464 | 0 | 0/50 |
| `S6B04/repeated_template_cards` | 2655344 / 2775450 / 2903390 / 2723566 / 2718421 | 2723566 | 2655344–2903390 | 2177058–2177060 | 10499 | 802/802 | 129642 | 710785 | 21753/270976 | 199 | 4/798 |
| `S6B04/twenty_thousand_raw_nodes` | 62929906 / 63804044 / 63975839 / 63726483 / 65276411 | 63804044 | 62929906–65276411 | 64268994–64286305 | 253395–253396 | 19997/19997 | 3313056 | 17889202 | 539765/5184896 | 3998 | 5/19992 |

四类新驱动样本均满足 `workUsed <= workLimit` 且 `budgetHit=false`。约 20k 节点场景没有出现二次爆炸：`19,997` 个公开节点对应 `539,765` 个确定性 work、`3,998` 个 Stage 3 vote match 和 `5` 个新 ref。

### 6.5 B05 动作重验边界

| 场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | 场景计量 |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| `tier_matching/zero_candidate_20k` | 2602646 / 2604257 / 2584060 / 2587527 / 2588510 | 2588510 | 2584060–2604257 | 768 | 9 | ceiling `240012`; visits `60003`; lineage `0`; pairs `40002`; tiers `3` |
| `tier_matching/single_tier1_20k` | 1711100 / 1712312 / 1712719 / 1712191 / 1713098 | 1712312 | 1711100–1713098 | 1632–1639 | 11 | ceiling `240012`; visits `40002`; lineage `4`; pairs `40002`; tiers `1` |
| `tier_matching/single_tier2_20k` | 2551716 / 2550269 / 2667597 / 2565193 / 2557585 | 2557585 | 2550269–2667597 | 1888–1898 | 14 | ceiling `240012`; visits `60003`; lineage `4`; pairs `40002`; tiers `2` |
| `tier_matching/single_tier3_20k` | 3445143 / 3475468 / 3440635 / 3436069 / 3445604 | 3445143 | 3436069–3475468 | 9056 | 44 | ceiling `240018`; visits `80007`; lineage `21`; pairs `40003`; tiers `3` |
| `tier_matching/multi_candidate_20k` | 867857 / 866492 / 866807 / 881582 / 868796 | 867857 | 866492–881582 | 1968 | 14 | ceiling `240012`; visits `20001`; lineage `5`; pairs `40002`; tiers `1` |
| `tier_matching/target_plus_carrier_20k` | 3560673 / 3584925 / 3555235 / 3565820 / 3605759 | 3565820 | 3555235–3605759 | 3264 | 22 | ceiling `240012`; visits `80004`; lineage `8`; pairs `40002` |
| `tier_matching/ancestor_mismatch_depth1_20k` | 22430158 / 22543950 / 22867285 / 22463406 / 22433406 | 22463406 | 22430158–22867285 | 32470352–32470361 | 200457 | ceiling `240012`; visits `60003`; lineage `120003`; pairs `40002`; tiers `3` |
| `carrier_safe_point_recompute` | 1041530 / 1102773 / 1025985 / 993980 / 1074901 | 1041530 | 993980–1102773 | 344865 | 75 | carriers `2000` |
| `scroll_replan_large_tree` | 324705 / 311823 / 312567 / 325891 / 320298 | 320298 | 311823–325891 | 591272 | 70 | safe spans `1` |
| `text_focus_binding/exact` | 421587 / 424480 / 421857 / 426997 / 426705 | 424480 | 421587–426997 | 0 | 0 | — |
| `text_focus_binding/near` | 424496 / 423470 / 423294 / 427322 / 434131 | 424496 | 423294–434131 | 0 | 0 | — |
| `text_focus_binding/shifted` | 479214 / 485508 / 483301 / 489154 / 480137 | 483301 | 479214–489154 | 0 | 0 | — |

所有 tier-matching 场景的 candidate visits 都低于固定 ceiling。`ancestor_mismatch_depth1_20k` 的高分配与可见结构计数一致：`60,003` 次 candidate visit 产生 `120,003` 次 lineage 推进，并对应 `200,457` allocs；它不是 20k 节点上的 N² 增长，且 candidate 数仍低于 `240,012`。

### 6.6 B06 边界与端到端，含 `6ec25c8` 对照

| 侧 | 场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | 关键计量 |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 当前 | `five_unknown_booleans` | 166931 / 170957 / 164382 / 197991 / 248872 | 170957 | 164382–248872 | 252689–252691 | 2100 | 257/257 | 39543 | 229575 | 256 个五布尔未知节点，即 1280 个未知布尔；new refs `257` |
| 当前 | `long_text_4096` | 3729 / 3493 / 3524 / 3965 / 3634 | 3634 | 3493–3965 | 2584 | 41 | 2/2 | 4386 | 10081 | text `4096 B` |
| 当前 | `chain_512` | 1038154 / 840867 / 822771 / 974938 / 883069 | 883069 | 822771–1038154 | 587204–587213 | 5646 | 514/514 | 3819 | 390372 | 1 个 chain definition，512 个 chain refs；new refs `514` |
| 当前 | `budget_cutoff` | 226181 / 213076 / 210034 / 217206 / 194315 | 213076 | 194315–226181 | 215537–215538 | 1863 | 98/98 | 14611 | 85433 | supported `1`，hit `1`，work `1/1`，new/preserved `97/1` |
| 当前 | `build_read_diff_json_apply` | 1737041 / 1599963 / 1567833 / 1564284 / 1643159 | 1599963 | 1564284–1737041 | 1563735–1563917 | 10738–10740 | 99/99 | 9989 | 87610 | read full `9989 B`，delta `445 B`，`4.455%`，paired cost `174313`，chain `1/64`，unknown bool `170`，apply equal `1` |
| `6ec25c8` | `five_unknown_booleans` | 247127 / 216431 / 258004 / 365922 / 399023 | 258004 | 216431–399023 | 312602–312605 | 1844 | 257/257 | 39587 | 225735 | 256 个五布尔未知节点，即 1280 个未知布尔；new refs `257` |
| `6ec25c8` | `long_text_4096` | 2341 / 2235 / 2071 / 2156 / 2297 | 2235 | 2071–2341 | 1240 | 28 | 2/2 | 4430 | 10066 | text `4096 B` |
| `6ec25c8` | `chain_512` | 960859 / 924126 / 803273 / 744818 / 879979 | 879979 | 744818–960859 | 664405–664408 | 5639 | 514/514 | 3863 | 398549 | 1 个 chain definition，512 个 chain refs；new refs `514` |
| `6ec25c8` | `budget_cutoff` | 950244 / 840080 / 789610 / 805587 / 765476 | 805587 | 765476–950244 | 1097864–1097865 | 1769 | 98/98 | 14551 | 83874 | supported `0`，hit `0`，work `0/0`；基线没有确定性预算接缝，不能作等义性能比较 |
| `6ec25c8` | `build_read_diff_json_apply` | 3385862 / 3193217 / 3089552 / 3083703 / 3090822 | 3090822 | 3083703–3385862 | 2465122–2465333 | 25062–25066 | 99/99 | 10033 | 88124 | read full `10033 B`，delta `474 B`，`4.724%`，paired cost `175356`，chain `1/64`，unknown bool `170`，apply equal `1` |

当前 B06 预算截止在 `work=1/1` 时置 `budget_hit=1`，仍输出 98 个唯一公开 ref；由于截止早于内容匹配，new/preserved 为 `97/1` 是预期安全结果，不用于稳定 ref 覆盖判断。端到端路径实际完成 Build → Read → ReadDiff → strict JSON encode/decode → ReadApply，并用 `ReadEqual` 和 canonical JSON 相等确认还原。

B01/B06 同驱动、同环境对照：

| 对照项 | 当前中位 | `6ec25c8` 中位 | 变化 | 结论 |
| --- | ---: | ---: | ---: | --- |
| B01/32 ns | 4636 | 31097 | -85.09% | 明显改善 |
| B01/32 B/op / allocs | 6144 / 52 | 40744 / 341 | -84.92% / -84.75% | 明显改善 |
| B01/256 ns | 20242 | 444540 | -95.45% | 明显改善 |
| B01/256 B/op / allocs | 31376 / 52 | 308202 / 2311 | -89.82% / -97.75% | 明显改善 |
| B06 unknown ns | 170957 | 258004 | -33.74% | 明显改善 |
| B06 unknown B/op | 252689 | 312602 | -19.17% | 总分配字节改善 |
| B06 unknown allocs | 2100 | 1844 | +13.88% | 调用数增加，但没有压过时间与字节收益 |
| B06 long text ns | 3634 | 2235 | **+62.60%** | **明显回退** |
| B06 long text B/op | 2584 | 1240 | **+108.39%** | **明显回退** |
| B06 long text allocs | 41 | 28 | **+46.43%** | **明显回退** |
| B06 chain ns | 883069 | 879979 | +0.35% | 分布重叠，属噪声量级 |
| B06 chain B/op | 587209 | 664407 | -11.62% | 分配字节改善 |
| B06 E2E ns | 1599963 | 3090822 | -48.24% | 明显改善 |
| B06 E2E B/op / allocs | 1563831 / 10739 | 2465165 / 25064 | -36.56% / -57.15% | 明显改善 |
| B06 E2E full / delta B | 9989 / 445 | 10033 / 474 | -0.44% / -6.12% | 编码更小 |

唯一明显回退是首次构建 4096 字节长文本。它的 full 编码反而从 `4430 B` 降到 `4386 B`，SnapshotCost 仅从 `10066` 变为 `10081`，因此不是文本正文复制或编码字节膨胀。直接结构归因在新增连续性管线：当前 `build.go:121` 调用 `matchRefs` 后，即使 `b.prev == nil`，也会先由 `match.go:275` 的 `buildPublicMatchIndex` 和 `match.go:538` 的 `newMatchState` 建立完整当前索引/状态，再由 `match.go:841` 的首次构建分支提前返回。这个两节点 fixture 因而承担了五阶段匹配器的固定索引与状态分配；基线对首次构建只建立较少的 map/slice 状态。该归因由代码结构和同一 fixture 的 alloc 增量相互印证，本轮没有另跑 CPU/heap profile。

其余结论：

- B01 证明默认空叶净化在 32/256 两档都把公开树压到 root + 语义兄弟，且没有牺牲语义节点。
- B02/B04 的 new/preserved ref、work 与 vote 在预算内，B04 约 20k 节点没有超限或二次增长。
- B03 所有结构与规模样本继续满足 70% 门禁。
- B05 高位 line mismatch 的分配由 120k lineage 次数的确定性工作量解释，候选上限未突破。
- B06 未知五布尔、chain、预算截止与 Build→Read→ReadDiff→JSON→ReadApply 路径均通过；除上述 long-text 首次构建外没有明显回退。
- 未修改生产源码、既有测试、预算、70% 判据或任何文档契约来改善数字。

主协调者处理结论：保留两节点首次构建约 `1.4µs` 的固定成本，记录相对回退与分配增量，
不改变匹配/安全判据来优化该样本。B04 的 20k 原始节点样本用于纯算法压力和确定性工作量，
其 `full=3,313,056 B`、`SnapshotCost=17,889,202 B` 超过引擎的公开/单快照交付预算，
不代表该树能经服务发布。B01/B06 比较对象是同一合成驱动在两个源码锚点上的实际重跑，
不是设备或 MCP 网络延迟。其后 S6 修复只涉及 engine 的等待/发布显示身份门禁；本表使用的
observation 算法和 B05 纯匹配/安全点/绑定函数保持与测量源码相同，不把表中数据冒称为实机结果。

## 7. 最终统一重跑结果（主表）

本节是最终主表。§5/§6 保留为前一轮追溯，不以其中 53 个子项替代本次结果。

最终正式脚本退出 `0`。八份主日志为 `final-current-b01.log` 至 `final-current-b06.log`、`final-baseline-b01.log` 与 `final-baseline-b06.log`；均为 `PASS/ok`，无 `FAIL/panic/fatal`。共包含 61 个唯一五样本子项（当前 54、基线 7）、305 条 Benchmark 行，每个子项恰为 5 条。

- 当前 worktree：`.state/s6-current-bd77056`，HEAD `bd770565cedde2391a55ab2a49e87a84adc6edba`，加最终 `match.go`、`delta_benchmark_test.go`、`s6_benchmark_test.go`。
- 基线 worktree：`.state/s6-baseline-6ec25c8`，HEAD `6ec25c87daefbc01c99f78d968214304641d119d`，加同一最终 driver。
- 环境：`go1.27.1 windows/amd64`、`GOEXPERIMENT=jsonv2`、`CGO_ENABLED=0`、AMD Ryzen 7 5700X，benchmark 默认 `-16`。
- final driver：`316e3a5e585090c78e0cc258092b39f22593ecfe8601a79713bf66ca304b9d0a`。
- 当前算法候选：`match.go=e4600051b1dcadb50dd25d1e79eb0607b154203ba1a5ce275849d84a09b49bf7`；`delta_benchmark_test.go=10d825c2035f07f65d7c0d646d67b04af5e46d06d88478a9ddad6feae244bb2d`。

`final-list-*` 六项、`final-warmup-*` 六项及基线对应四项全部退出 `0`。正式命令：

```powershell
# .state/s6-current-bd77056
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B01Purification$' -benchmem -count=5
go test ./internal/observation -run '^$' -bench '^Benchmark(ContinuityScrollFiveCards|S6B02Continuity)$' -benchmem -count=5
go test ./internal/observation -run '^$' -bench '^BenchmarkStructuralDeltaB03$' -benchmem -count=5
go test ./internal/observation -run '^$' -bench '^Benchmark(ContinuityTreeShapes|S6B04Matching)$' -benchmem -count=5
go test ./internal/engine -run '^$' -bench '^BenchmarkActRevalidationB05$' -benchmem -count=5
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B06Boundaries$' -benchmem -count=5

# .state/s6-baseline-6ec25c8
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B01Purification$' -benchmem -count=5
go test ./internal/observation -run '^$' -bench '^BenchmarkS6B06Boundaries$' -benchmem -count=5
```

对应输出为 `final-current-b01.log` 至 `final-current-b06.log`、`final-baseline-b01.log` 与 `final-baseline-b06.log`。下表保留每个子项按执行顺序的 5 次 `ns/op`，并列出中位、范围及稳定结构计量。

### 7.1 B01 净化与空叶

| 侧 | 场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | 过滤空叶 | new/preserved |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 当前 | `empty_leaves_32` | 4491 / 4352 / 4408 / 4389 / 4359 | 4389 | 4352–4491 | 6160 | 52 | 34/2 | 240 | 23670 | 32 | 2/0 |
| 当前 | `empty_leaves_256` | 19339 / 18772 / 20065 / 20699 / 18737 | 19339 | 18737–20699 | 31392 | 52 | 258/2 | 240 | 177854 | 256 | 2/0 |
| 基线 | `empty_leaves_32` | 26722 / 27622 / 26301 / 26970 / 26643 | 26722 | 26301–27622 | 40744 | 341 | 34/34 | 1173 | 25889 | 0 | 34/0 |
| 基线 | `empty_leaves_256` | 254162 / 301609 / 380593 / 365078 / 486496 | 365078 | 254162–486496 | 308201–308202 | 2311 | 258/258 | 7604 | 196182 | 0 | 258/0 |

32/256 档时间分别下降 `83.58%`、`94.70%`；公开节点保持 root + 语义兄弟，full 保持 `240 B`。

### 7.2 B02 连续性与重复无锚点

| 驱动/场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | work 用/限 | vote | new/preserved |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ContinuityScrollFiveCards` | 55526 / 61039 / 60255 / 56805 / 60920 | 60255 | 55526–61039 | 36600 | 376 | — | — | — | 428/— | — | — |
| `S6B02/five_cards_b_to_e_preserved` | 77975 / 71909 / 69845 / 73386 / 68951 | 71909 | 68951–77975 | 36600 | 376 | 17/17 | 2679 | 14993 | 428/70016 | 4 | 3/14 |
| `S6B02/repeated_no_anchor` | 255041 / 261215 / 241269 / 242369 / 237862 | 242369 | 237862–261215 | 169864 | 1250 | 98/98 | 14609 | 85431 | 1517/90752 | 0 | 96/2 |

五卡 B–E 整棵子树保号，F 不复用旧号；重复无锚点只保留 root/scroll，没有猜测性 vote。

### 7.3 B03 结构差分

24 个子项均在计时前完成逻辑等值与 canonical JSON 精确还原，并全部满足 `10*deltaBytes < 7*fullBytes`。wrap、unwrap、既有父间 reparent 与 unchanged 分别保留独立样本。

| 操作/规模 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | delta B | delta/full | full B |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `sparse_updates/32` | 238412 / 276727 / 282246 / 307750 / 296656 | 282246 | 238412–307750 | 161994–162037 | 1987–1988 | 146 | 6.237% | 2341 |
| `sparse_updates/256` | 1453922 / 1453977 / 1414532 / 1495990 / 1423864 | 1453922 | 1414532–1495990 | 1324574–1326130 | 14468–14469 | 567 | 3.084% | 18384 |
| `sparse_updates/2048` | 12494634 / 12954709 / 12744511 / 12830651 / 12873915 | 12830651 | 12494634–12954709 | 11311063–11311208 | 113362 | 595 | 0.398% | 149503 |
| `sparse_updates/20000` | 131753362 / 132834388 / 136941812 / 133233838 / 135252112 | 133233838 | 131753362–136941812 | 105703356–105704517 | 1102878–1102879 | 623 | 0.04159% | 1497950 |
| `single_property/32` | 237659 / 213491 / 220357 / 224502 / 231373 | 224502 | 213491–237659 | 161358–161411 | 1981–1982 | 89 | 3.810% | 2336 |
| `single_property/256` | 1409043 / 1451161 / 1411149 / 1410999 / 1417212 | 1411149 | 1409043–1451161 | 1319782–1320185 | 14419 | 90 | 0.4911% | 18326 |
| `single_property/2048` | 12560610 / 12484129 / 12540379 / 12560307 / 12544045 | 12544045 | 12484129–12560610 | 11304722–11305088 | 113312 | 91 | 0.06089% | 149444 |
| `single_property/20000` | 132244238 / 132225462 / 134735050 / 131138425 / 134247325 | 132244238 | 131138425–134735050 | 105696831–105697694 | 1102828–1102829 | 92 | 0.006142% | 1497891 |
| `subtree_wrap/32` | 221572 / 208800 / 215705 / 219175 / 232038 | 219175 | 208800–232038 | 166570–166627 | 2065 | 178 | 7.361% | 2418 |
| `subtree_wrap/256` | 1458283 / 1419761 / 1485950 / 1473563 / 1594704 | 1473563 | 1419761–1594704 | 1322483–1325255 | 14504–14506 | 178 | 0.9669% | 18409 |
| `subtree_wrap/2048` | 12791141 / 12693010 / 12482540 / 12556924 / 13071018 | 12693010 | 12482540–13071018 | 11325907–11326186 | 113404 | 178 | 0.1190% | 149528 |
| `subtree_wrap/20000` | 133263412 / 132514250 / 137507350 / 133418438 / 135107275 | 133418438 | 132514250–137507350 | 105701844–105702699 | 1102920–1102921 | 178 | 0.01188% | 1497975 |
| `subtree_unwrap/32` | 220169 / 217845 / 229612 / 224828 / 217119 | 220169 | 217119–229612 | 168235–168265 | 2020 | 93 | 3.993% | 2329 |
| `subtree_unwrap/256` | 1540848 / 1589931 / 1514250 / 1594322 / 1529410 | 1540848 | 1514250–1594322 | 1346729–1349771 | 14460–14462 | 93 | 0.5076% | 18320 |
| `subtree_unwrap/2048` | 12932306 / 12605550 / 12837184 / 12791038 / 12723488 | 12791038 | 12605550–12932306 | 11540507–11540620 | 113370 | 93 | 0.06223% | 149439 |
| `subtree_unwrap/20000` | 136683712 / 135893025 / 137321938 / 136228250 / 136342712 | 136342712 | 135893025–137321938 | 107445346–107445948 | 1103001–1103002 | 93 | 0.006209% | 1497886 |
| `subtree_reparent/32` | 200832 / 193915 / 197696 / 190892 / 182821 | 193915 | 182821–200832 | 159517–159546 | 1990 | 80 | 3.406% | 2349 |
| `subtree_reparent/256` | 1544577 / 1590893 / 1492816 / 1584516 / 1525042 | 1544577 | 1492816–1590893 | 1320885–1323097 | 14434–14435 | 80 | 0.4363% | 18338 |
| `subtree_reparent/2048` | 12557042 / 12581975 / 12611078 / 12578836 / 12567800 | 12578836 | 12557042–12611078 | 11305890–11306072 | 113332 | 80 | 0.05353% | 149455 |
| `subtree_reparent/20000` | 133612700 / 134435138 / 130236000 / 134882975 / 133481462 | 133612700 | 130236000–134882975 | 105697837–105698644 | 1102848–1102849 | 80 | 0.005341% | 1497900 |
| `unchanged/32` | 188362 / 187606 / 202145 / 197086 / 188387 | 188387 | 187606–202145 | 160787–160816 | 1974–1975 | 47 | 2.018% | 2329 |
| `unchanged/256` | 1555806 / 1510356 / 1506312 / 1476739 / 1613980 | 1510356 | 1476739–1613980 | 1318879–1321482 | 14412–14413 | 47 | 0.2566% | 18320 |
| `unchanged/2048` | 12480114 / 12431882 / 12622274 / 12651319 / 12466108 | 12480114 | 12431882–12651319 | 11304247–11304493 | 113305 | 47 | 0.03145% | 149439 |
| `unchanged/20000` | 137897538 / 133159988 / 135251888 / 132953800 / 134998625 | 134998625 | 132953800–137897538 | 105696550–105697158 | 1102821–1102822 | 47 | 0.003138% | 1497886 |

最大比例为 `subtree_wrap/32` 的 `7.361%`。unwrap 的 20k 样本为 `136342712 ns`、`93 B` delta、`0.006209%`；既有父间 reparent 为 `133612700 ns`、`80 B` delta、`0.005341%`。

### 7.4 B04 匹配边界

既有 `ContinuityTreeShapes` 与新 `S6B04Matching` 均保留。新驱动最后一列按 `candidate_visits/vote_comparisons/vote_matches` 记录，前两项是真实遍历/成员比较，末项仅为 accepted vote 结果。

| 驱动/场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | work 用/限 | visits/comparisons/accepted | new/preserved |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `ContinuityTreeShapes/small_tree` | 50229 / 47982 / 49930 / 50687 / 45453 | 49930 | 45453–50687 | 25792 | 301 | — | — | — | 295/— | — | — |
| `ContinuityTreeShapes/deep_nested_containers` | 282923 / 261245 / 277016 / 265802 / 259984 | 265802 | 259984–282923 | 192128–192129 | 1271 | — | — | — | 1576/— | — | — |
| `ContinuityTreeShapes/repeated_template_cards` | 3530092 / 3165401 / 3269954 / 3305672 / 3148323 | 3269954 | 3148323–3530092 | 2177057–2177062 | 10499 | — | — | — | 21753/— | — | — |
| `ContinuityTreeShapes/large_twenty_thousand_raw_nodes` | 63620850 / 63237216 / 63803628 / 64938356 / 63691826 | 63691826 | 63237216–64938356 | 64286300–64286622 | 253396 | — | — | — | 539765/— | — | — |
| `S6B04/small_tree` | 131127 / 125427 / 132576 / 124973 / 131887 | 131127 | 124973–132576 | 135944 | 555 | 25/25 | 3815 | 21895 | 803/72064 | 653/0/0 | 0/25 |
| `S6B04/deep_nested_containers` | 218559 / 229020 / 230423 / 224115 / 235025 | 229020 | 218559–235025 | 192128 | 1271 | 50/50 | 7357 | 42842 | 1576/78464 | 1328/0/0 | 0/50 |
| `S6B04/repeated_template_cards` | 2709782 / 2873478 / 2934166 / 2960300 / 3019353 | 2934166 | 2709782–3019353 | 2177059–2177061 | 10499 | 802/802 | 129642 | 710785 | 21753/270976 | 16400/398/199 | 4/798 |
| `S6B04/twenty_thousand_raw_nodes` | 63754994 / 64012750 / 65018567 / 62492806 / 62728567 | 63754994 | 62492806–65018567 | 64286287–64286297 | 253396 | 19997/19997 | 3313056 | 17889202 | 539765/5184896 | 399888/7996/3998 | 5/19992 |

20k 场景的 `399888` 次候选访问和 `7996` 次 vote 比较均为实际运行计数；accepted vote `3998` 不再被用作比较数。四类新驱动样本均满足 `workUsed <= workLimit` 且 `budgetHit=false`。

### 7.5 B05 动作重验边界

| 场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | 场景计量 |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| `tier_matching/zero_candidate_20k` | 2596346 / 2616315 / 2589869 / 2591022 / 2588107 | 2591022 | 2588107–2616315 | 768 | 9 | ceiling `240012`; visits `60003`; lineage `0`; pairs `40002`; tiers `3` |
| `tier_matching/single_tier1_20k` | 1733823 / 1721079 / 1715873 / 1717462 / 1757944 | 1721079 | 1715873–1757944 | 1632–1639 | 11 | ceiling `240012`; visits `40002`; lineage `4`; pairs `40002`; tiers `1` |
| `tier_matching/single_tier2_20k` | 2561053 / 2560348 / 2568117 / 2562240 / 2568531 | 2562240 | 2560348–2568531 | 1888–1898 | 14 | ceiling `240012`; visits `60003`; lineage `4`; pairs `40002`; tiers `2` |
| `tier_matching/single_tier3_20k` | 3466631 / 3536487 / 3460082 / 3498414 / 3449573 | 3466631 | 3449573–3536487 | 9056 | 44 | ceiling `240018`; visits `80007`; lineage `21`; pairs `40003`; tiers `3` |
| `tier_matching/multi_candidate_20k` | 872676 / 872142 / 871315 / 888755 / 889713 | 872676 | 871315–889713 | 1968 | 14 | ceiling `240012`; visits `20001`; lineage `5`; pairs `40002`; tiers `1` |
| `tier_matching/target_plus_carrier_20k` | 3572008 / 3575491 / 3572568 / 3590060 / 3645712 | 3575491 | 3572008–3645712 | 3264 | 22 | ceiling `240012`; visits `80004`; lineage `8`; pairs `40002` |
| `tier_matching/ancestor_mismatch_depth1_20k` | 24735265 / 23288147 / 22760348 / 23354076 / 22751478 | 23288147 | 22751478–24735265 | 32470353–32470472 | 200457 | ceiling `240012`; visits `60003`; lineage `120003`; pairs `40002`; tiers `3` |
| `carrier_safe_point_recompute` | 918719 / 912184 / 963628 / 806750 / 836379 | 912184 | 806750–963628 | 344864–344865 | 75 | carriers `2000` |
| `scroll_replan_large_tree` | 309355 / 315265 / 312630 / 311145 / 310701 | 311145 | 309355–315265 | 591272 | 70 | safe spans `1` |
| `text_focus_binding/exact` | 425984 / 425950 / 425448 / 424995 / 427088 | 425950 | 424995–427088 | 0 | 0 | — |
| `text_focus_binding/near` | 425269 / 425666 / 434289 / 430257 / 426392 | 426392 | 425269–434289 | 0 | 0 | — |
| `text_focus_binding/shifted` | 483307 / 484336 / 482974 / 482487 / 482935 | 482974 | 482487–484336 | 0 | 0 | — |

ancestor-mismatch 的 `200457 allocs` 对应 `120003` lineage steps，candidate visits 仍低于 ceiling，没有出现 N×N 扫描。

### 7.6 B06 边界与端到端

| 侧 | 场景 | ns/op × 5（执行顺序） | 中位 | min–max | B/op | allocs | raw/public | full B | SnapshotCost | 关键计量 |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 当前 | `five_unknown_booleans` | 162941 / 188231 / 223099 / 223145 / 206082 | 206082 | 162941–223145 | 252705–252706 | 2100 | 257/257 | 39543 | 229575 | 256 个五布尔未知节点，1280 个未知布尔；new refs `257` |
| 当前 | `long_text_4096` | 3780 / 3704 / 3995 / 3563 / 4053 | 3780 | 3563–4053 | 2600 | 41 | 2/2 | 4386 | 10081 | text `4096 B` |
| 当前 | `chain_512` | 862980 / 904504 / 879350 / 805576 / 773130 | 862980 | 773130–904504 | 587220–587223 | 5646 | 514/514 | 3819 | 390372 | 1 个 chain definition，512 个 chain refs；new refs `514` |
| 当前 | `budget_cutoff` | 219867 / 204504 / 211295 / 212285 / 194323 | 211295 | 194323–219867 | 215553–215554 | 1863 | 98/98 | 14611 | 85433 | supported `1`，hit `1`，work `1/1`，new/preserved `97/1` |
| 当前 | `build_read_diff_json_apply` | 1709883 / 1647009 / 1717488 / 1806707 / 1668413 | 1709883 | 1647009–1806707 | 1563800–1563950 | 10739–10740 | 99/99 | 9989 | 87610 | full `9989 B`，delta `445 B`，`4.455%`，paired cost `174313`，apply equal `1` |
| 基线 | `five_unknown_booleans` | 200796 / 203253 / 201101 / 207761 / 227173 | 203253 | 200796–227173 | 312603–312604 | 1844 | 257/257 | 39587 | 225735 | 256 个五布尔未知节点，1280 个未知布尔；new refs `257` |
| 基线 | `long_text_4096` | 2026 / 2113 / 2337 / 2501 / 2321 | 2321 | 2026–2501 | 1240 | 28 | 2/2 | 4430 | 10066 | text `4096 B` |
| 基线 | `chain_512` | 972775 / 823116 / 940948 / 809526 / 824026 | 824026 | 809526–972775 | 664406–664409 | 5639 | 514/514 | 3863 | 398549 | 1 个 chain definition，512 个 chain refs；new refs `514` |
| 基线 | `budget_cutoff` | 973366 / 843368 / 885252 / 905659 / 814621 | 885252 | 814621–973366 | 1097864–1097866 | 1769 | 98/98 | 14551 | 83874 | supported `0`，hit `0`，work `0/0`；基线无预算接缝，不作等义性能比较 |
| 基线 | `build_read_diff_json_apply` | 3303382 / 3189049 / 3137098 / 3149857 / 3237708 | 3189049 | 3137098–3303382 | 2464975–2465504 | 25062–25067 | 99/99 | 10033 | 88124 | full `10033 B`，delta `474 B`，`4.724%`，paired cost `175356`，apply equal `1` |

B01/B06 同驱动、同环境对照：

| 对照项 | 当前中位 | `6ec25c8` 中位 | 变化 | 结论 |
| --- | ---: | ---: | ---: | --- |
| B01/32 ns | 4389 | 26722 | -83.58% | 明显改善 |
| B01/256 ns | 19339 | 365078 | -94.70% | 明显改善 |
| B06 unknown ns | 206082 | 203253 | +1.39% | 分布重叠，时间处于噪声量级；B/op -19.16% |
| B06 long text ns | 3780 | 2321 | **+62.86%** | **明显回退** |
| B06 long text B/op | 2600 | 1240 | **+109.68%** | **明显回退** |
| B06 long text allocs | 41 | 28 | **+46.43%** | **明显回退** |
| B06 chain ns | 862980 | 824026 | +4.73% | 范围重叠；B/op -11.62% |
| B06 E2E ns | 1709883 | 3189049 | -46.38% | 明显改善 |
| B06 E2E B/op | 1563841 | 2465379 | -36.56% | 分配字节改善 |
| B06 E2E allocs | 10739 | 25066 | -57.16% | 调用数改善 |
| B06 E2E full / delta B | 9989 / 445 | 10033 / 474 | -0.44% / -6.12% | 编码更小 |

唯一明显回退仍是首次构建 4096 字节长文本。full 编码从 `4430 B` 降到 `4386 B`，SnapshotCost 基本持平，因此不是正文复制或编码膨胀。结构归因仍是首次 Build 在 `build.go:121` 进入 `matchRefs` 后，在没有上一快照的提前返回前已经建立完整当前匹配索引/状态；入口位于 `match.go:857`，并在 `match.go:291` 的 `buildPublicMatchIndex`、`match.go:554` 的 `newMatchState` 后进入首次构建提前返回。该归因来自代码结构和稳定的 alloc 增量，本轮按协调者决定保留，不做优化。

最终结论：

- B01 的 32/256 空叶净化在公开节点、编码和时间上均保持规模收益。
- B02/B04 的 ref、work、candidate visit、vote comparison 均在预算内；B04 20k 没有超限或 N×N 扫描。
- B03 的 wrap、unwrap、既有父间 reparent 全部独立覆盖并通过精确还原与 70% 门禁。
- B05 的高分配由 120k lineage steps 解释，candidate ceiling 未突破。
- B06 未知五布尔、chain、预算截止和完整 Build→Read→ReadDiff→JSON→ReadApply 均通过；长文本固定首建成本仍单独登记。
- 预算、70% 判据、STOP、匹配接受逻辑与产品日志均未改动。

## 8. 主工作树 `doc.go` 调查结论

准备期间主工作树曾出现 `internal/observation/doc.go` 的 `M` 状态，内容是对应 S1–S5 的并行注释更新。该文件随后消失并不是 worktree 清理、benchmark 或 Git 隔离命令造成的：

- 本任务没有运行 `git restore`、`git checkout` 或 `git reset`。
- 所有基线和当前 benchmark 都在同仓库独立 worktree 中执行，不会回写主工作树 tracked 文件。
- 实际原因是一次误判后的手动操作：把当时显示的改动当成非预期残留，并用 `apply_patch` 将其内容改回 HEAD，导致该并行改动从主工作树消失。
- 收到提醒后没有再次尝试恢复、覆盖或重放任何他人改动，后续也没有触碰 `.state/s6-counterfactual`。

主协调者已恢复并复核该注释修改。该误操作未涉及用户原有未提交文件；后续通过独占写范围
整合，基准结果与生产算法未受影响。除上述已披露的注释误操作、新增 benchmark、本维护记录、
`.state` 临时 worktree 与基准日志外，基准任务没有修复或复原其他主工作树文件。
