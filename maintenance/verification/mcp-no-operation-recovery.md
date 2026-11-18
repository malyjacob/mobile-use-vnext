# MCP 查询失败的 P 恢复投影验证

涉及实机。Windows/amd64，Go 1.27.1，启用 `GOEXPERIMENT=jsonv2`。基线提交
`a53f95a8131931d96ccc4c1a246dd5653e993947`。设备侧使用现有 Android 部署
`release_version=0.19.0`、`contract_version=19`，从现有主机端口转发只读核验。

## 1. 现象与触发条件

分诊为 MCP 适配器投影缺陷。无本次 P 的 `status`、`inspect`、`diagnose` 遇到
`transport_unavailable` 或未取得服务结果即取消时，结果省略 `operation_id`，却附带
`recovery.entry=status` 和 `recovery.reference=P`。向 `status` 传入旧 P 也会触发，
该引用只定位原操作。对应现行调用边界见 [MCP 取消与漏收](../../docs/mcp.md#6-取消p-与漏收边界)。

## 2. 先红复现

[断连回归](../../internal/mcp/server_test.go#L1159) 关闭回环假服务后走适配器入口。
[取消回归](../../internal/mcp/server_test.go#L1123) 等待请求进入假服务，再取消调用上下文。
两条路径都覆盖 `status({})`、`status(reference=P7)`、`inspect(reference=O7)` 和
`diagnose({})`，分别检查 `both`、`structured` 和 `text` 模式。取消还验证请求没有本次 P
且未发出 `end`。

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/mcp -run '^(TestReadOnlyTransportFailureOmitsRecovery|TestCancellationReadOnlyDirect)$' -count=1
```

修复前，24 个用例均因文本含多余的 `recovery.entry=status`、`recovery.reference=P` 失败。
修复后原命令通过，错误码、取消详情保留，文本和结构化结果都省略该恢复字段。

## 3. 根因

`internal/mcp/server.go:150` 对这三个查询以空 `operationID` 调用。
基线 `internal/mcp/server.go:435` 的 `adapterRecovery` 只根据错误码合成 `status/P`。
传输投影的初始化与兜底（基线 `:390`、`:403`），以及取消的初始化与兜底（基线
`:273`、`:345`）均经过该函数，因而可能给出没有本次操作编号的恢复入口。

## 4. 修复与实际检查

修复提交为本记录所在提交。四个合成点统一传入本次实际预分配的 P，由
[合成恢复入口](../../internal/mcp/server.go#L434) 决定是否生成恢复。服务返回的原编号和
恢复按现有分支保留。文档同步无 P 的取消与传输失败表现，并澄清 `app.find/list` 已知集合
和带 P 的两类取消例外。

| 实际命令或操作 | 结果 |
| --- | --- |
| `go test ./internal/mcp ./scripts/mcp-probe -count=1` | 两包通过，含目录、结果模式、HTTP 取消及探针回归。 |
| `go test ./cmd/device-mcp -count=1` | 通过。 |
| 下方定向回归命令 | 八组测试各连续通过 10 次。 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/device-mcp ./scripts/mcp-probe ./scripts/toolcatalog` | 交叉构建通过。 |
| `go run ./scripts/toolcatalog` | 13 个工具，指纹为 `e25fc67773f1d744ef3dd526685d19f863f4e9d5e6f5e8ccb5548f6690eacde4`。 |

```powershell
go test ./internal/mcp -run '^(TestReadOnlyTransportFailureOmitsRecovery|TestCancellationReadOnlyDirect|TestReadOnlyServiceRecoveryIsPreserved|TestTransportFailureKeepsAllocatedOperationRecovery|TestCancellationWorkToolDirect|TestSnapCancellationClearsImageAndSkipsEnd|TestCancellationAppQueryDoesNotEnd|TestCancellationAppQueryKeepsKnownFacts)$' -count=10
```

另用临时 Python 回放，以 `go build -trimpath -buildvcs=false -o <temp>/device-mcp.exe ./cmd/device-mcp`
构建候选。分别运行候选的 `-transport stdio` 和 `-transport http`，指定 `-config`
及 `-result-mode both`。上游指向一个已绑定但未监听的回环端口，向两个入口发送
`server/discover`、`tools/list`、四项 `tools/call`，再独立执行兼容 `initialize`。
现代发现的全局说明和 13 工具目录逐项一致，只有 `snap` 带只读标记。兼容握手返回
同一份说明，stdio 的兼容目录也相同。两入口的四项断连调用都返回
`transport_unavailable` 和 `isError=true`，文本与结构化结果均省略 `operation_id`、
`recovery`。

实机先经现有主机转发请求 `GET /health`，返回 `status=ok`、契约 19 和发行 0.19.0。
随后同一临时候选以 stdio 连接现有手机服务，`status({})` 与 `diagnose({})` 均成功，
分别返回自查事实和诊断，没有为查询分配 P。

## 5. 同类核查

- [服务结果保留](../../internal/mcp/server_test.go#L1197) 覆盖 `status(P)` 的已知原 P、
  `status/P` 和 `cause=running`，含取消前已取得服务结果的分支。
- [带 P 的断连](../../internal/mcp/server_test.go#L1171) 覆盖 `act`、`snap` 和
  `app.find`，均保留本次 P 与 `status/P`。
- [工作取消](../../internal/mcp/server_test.go#L1063) 验证按本次 P 发起 `end` 并保留恢复。
  [snap 取消](../../internal/mcp/snap_test.go#L158) 验证丢图、保留本次 P 的恢复且不发出
  `end`。[app 查询取消](../../internal/mcp/app_test.go#L560) 覆盖 `find/list` 分支，
  按原规则省略恢复并保留已有服务事实。

## 6. 验证边界与文档审计

实机范围为临时候选的正常查询，断连和取消由本地夹具注入。手机产物替换、发布清单及运行中
手机 MCP 入口探针属于发布验证。设备动作、引擎状态机和工具发现治理属于独立范围。

独立只读文档审计使用 DeepSeek V4.1 Flash 1017（`auto/deepseek-flash`，推理强度 `max`）。
按初审意见澄清 [MCP 查询集合保留](../../docs/mcp.md#L417) 和
[运维说明中的取消例外](../../docs/runbook.md#L394)。两份正式文档复检结论为 CLEAN。
