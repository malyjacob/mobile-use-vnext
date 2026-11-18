# MCP 条件等待与文本恢复说明验证

## 1. 分诊与触发条件

调用引导的局部校准。此前的隔离回放中，Agent 有时用 `observe(wait_ms=2000)` 替代
`wait_for` 的条件等待。移除全局指令后，两次清空草稿回放都偏离了预设的直接写入及 P 恢复
路径。按已讨论并确认的最小范围，只把现行规则放到相关工具的发现描述中。
独立文档审计还发现，已认领的 `text` 可能仅派发聚焦即整体 `uncertain`。共享说明和工具描述
统一使用整体结果及实际恢复字段，覆盖这一情形。

基线提交为 `3fbff60b8ea8601816938b310a89c81b95740172`。Windows/amd64 使用 Go 1.27.1
和 `GOEXPERIMENT=jsonv2`。设备服务由本地夹具模拟。兼容握手和 `tools/list` 的对比只访问
临时适配器。

## 2. 复现与反向对照

`internal/mcp/schema_test.go` 的 `TestLiveToolDescriptionsClarifyWaitAndTextRecovery`
检查内存 MCP 连接真实注册的描述。新增断言在发布基线指引时失败，三项子用例均为红。
在同一测试文件下用 Go overlay 恢复基线 `internal/mcp/schema.go`，复现了同样的三项失败。
文档审计后新增整体 `outcome=uncertain` 的断言覆盖工具描述和全局指令。修正前缺少该触发，
修正后通过。反向对照针对具体提示，不把模型行为写成确定性断言。

## 3. 归因与边界

- 基线 `internal/mcp/schema.go:149` 给出 `observe.wait_ms` 的单位和范围，缺少从单次延迟采集到
  `wait_for` 的调用分流。`:209` 到 `:213` 给出等待规则，候选将条件轮询和超时仍可成功返回放在开头。
- 基线 `internal/mcp/schema.go:274` 让调用方按恢复结果观察，实际归属 P 的选取在全局指令。
  候选在 `act` 的 text 说明中直接说明整体 `outcome=uncertain` 且
  `recovery.entry=observe`、`recovery.reference=P` 时如何填写 `observe.reference` 及核验框值或页面。
- 基线 `internal/mcp/schema.go:79` 的共享指令只举写入或 ENTER 不确定。引擎的
  `textOverallOutcome` 也把聚焦已派发、写入和 ENTER 尚未派发的已认领结果归为整体 `uncertain`。
  文本恢复仍按实时阻塞事实覆盖，调用方使用实际结果的 `recovery`。
- [观察实现](../../internal/engine/observe.go) 在采集前启动 `WaitMS` 定时器。
  [等待实现](../../internal/engine/wait_for.go) 执行条件轮询及终帧发布。
  [文本恢复](../../internal/engine/text_recovery.go) 按会话和实时阻塞事实投影恢复入口。

## 4. 实现与检查

实现提交为本记录所在提交。更新 `observe`、`wait_for`、`act` 的工具描述、目录指纹基线和
[MCP 入口契约](../../docs/mcp.md)。当前源码目录指纹为
`e25fc67773f1d744ef3dd526685d19f863f4e9d5e6f5e8ccb5548f6690eacde4`。

| 实际检查 | 结果 |
| --- | --- |
| `go test ./internal/mcp -run '^(TestLiveToolDescriptionsClarifyWaitAndTextRecovery|TestDiscoveryByteBudget|TestCatalogFingerprintBaseline|TestServerInstructionsDiscovery)$' -count=1 -v` | 通过。源码发现体积为 28,050 字节，基线为 27,676 字节，门槛为小于 36,250 字节 |
| `go test -overlay <基线 schema.go 映射> ./internal/mcp -run '^(TestLiveToolDescriptionsClarifyWaitAndTextRecovery|TestDiscoveryByteBudget)$' -count=1 -v` | 描述回归的三个子用例均按预期失败，体积检查通过 |
| `go test ./internal/mcp ./scripts/mcp-probe -count=1` | 两包通过 |
| `go test ./internal/engine -run '^TestActTextWriteNotDispatchedStaysUncertain$' -count=1` | 通过。仅聚焦派发时，整体仍为 `uncertain`，write 和 ENTER 保持 `not_executed` |
| `go run ./scripts/toolcatalog` | 数量为 13，目录指纹与本页、测试和正式文档一致 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/device-mcp ./scripts/mcp-probe ./scripts/toolcatalog` | 通过，命令启用 `GOEXPERIMENT=jsonv2` |
| 原始 stdio `initialize` 与 `tools/list`，用 overlay 和工作区各临时 `go build` 一份适配器 | 工具集、入参 Schema 和注解逐项相等。描述仅 `observe`、`act`、`wait_for` 分别变动 +54、+242、−30 字节。全局指令 6,943 → 7,051 字节，差异集中在文本恢复所在段落 |

## 5. 同宿主受控回放

使用桌面同版本 Codex `0.169.0-alpha.11.1` 和 DeepSeek V4.1 Flash（`auto/deepseek-flash`）。
回放沿用前一轮的 `low` 推理强度，两组相同。适配器指向临时回环 HTTP 夹具，stdio 代理在主矩阵
中移除握手的全局说明。每次运行以相同配置和工作目录执行 `codex mcp list --json`，断言仅
`mcp_replay` 启用，再用 `codex exec --ephemeral --json --skip-git-repo-check --sandbox read-only`
运行。计入报告的 17 次均退出码 0、发现 13 项工具，未发现外部 MCP 调用、命令执行或文件修改事件。
该计数包含中间候选的 12 次配对回放、一次保留全局指令的烟测，以及最终候选的 4 次文本配对回放。

在临时回放目录实际运行的主矩阵和复测命令如下。

```powershell
python replay.py --baseline-source baseline-source `
  --candidate-source candidate-source --output-root formal-paired-20261118 `
  --reps 3 --max-workers 3 --candidate-global-smoke
python replay.py --baseline-source baseline-source `
  --candidate-source candidate-final-source --output-root final-text-paired-e25fc677 `
  --tasks text --reps 2 --max-workers 2
```

| 样本 | 基线 | 候选 |
| --- | --- | --- |
| 中间候选 `c06ffbc1…`，等待任务首步选择 `wait_for` | 0/3 | 1/3 |
| 中间候选 `c06ffbc1…`，草稿任务首步正确调用 `act` | 2/3 | 3/3 |
| 最终候选 `e25fc677…`，草稿任务首步正确调用 `act` | 1/2 | 2/2 |
| 最终候选 `e25fc677…`，草稿任务随后选过合法的归属 P | 2/2 | 2/2 |

合法归属 P 包含会话 `session.origin` 和同一会话的操作编号。夹具误拒了部分符合契约的调用，
包括 `observe(reference=P0)`、无筛选的 `inspect(O1)` 和等待之后带 `bubble=true` 的 ref 点击。
因此以实际入参判断首步和合法引用。严格两调用路径在最终文本对照中双方各 1/2，包含夹具干扰。
保留全局说明的单次中间候选烟测先选 `status`，未走预设的文本路径。首批调试的 7 次运行排除在表外。
小样本给出调用选择的方向性信号，不能估计长期误用率或证明端到端提升。最终版文字与中间候选的
差异集中在文本恢复触发，等待模型配对仍按其实际测试的中间指纹记录。

## 6. 文档审计与验证边界

`docs/mcp.md` 经独立只读审计。按发现项同步覆盖仅聚焦派发的整体 `uncertain`，复检结论为 CLEAN。
原始 MCP 发现仍以全局字段交付一份指令。Codex Code Mode 逐项附加发生在宿主的工具投影，
direct surface 的共享说明策略仅完成源码核对，未验证具体宿主设置下的模型投影。
本次只构建临时适配器，未重建发布清单、替换部署产物或执行真机回归。
