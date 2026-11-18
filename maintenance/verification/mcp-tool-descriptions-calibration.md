# MCP 工具说明最小校准验证记录

## 1. 现象与分诊

分诊为公开 Schema 和说明与现行契约的偏差。`act` 与 `inspect` 说明要求 O，目录的
`reference.pattern` 却允许 P。`act` 同时将消费时机写成「调用后」，与静态拒绝不认领 O
的行为冲突。基线为 `f697744467965deb5e4ff332228f420395a36e79`。验证环境为
Windows/amd64、Go 1.27.1，另做 Linux/arm64 交叉构建。验证使用假服务和本地发现接口，
未向手机服务发送工具调用。

## 2. 先红复现

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/mcp -count=1 -run '^TestToolCatalog$'
```

`TestToolCatalog` 通过 SDK 的 `tools/list` 获取实际注册项。修复前新增的四项断言失败，分别是
`act` 和 `inspect` 的 pattern 允许 P，以及 `act` 的工具说明和字段说明都未说明静态拒绝与
认领的区别。校准后同一用例通过。

## 3. 根因

基线 `internal/mcp/schema.go:296`、`:435` 将通用 O/P 的 pattern 用于只接收 O 的工具。
同文件 `:265`、`:297` 将 `act` 认领后的消费写成调用即消费。引擎先做静态检查，再尝试认领，
认领成功才发生单向消费。静态检查也可能发现 O 到期，因此使用「不认领」描述这条保证。
另有 `AGENTS.md:88` 的 12 工具旧计数，以及 `scripts/deploy.ps1:612` 对旧 M6 专项脚本的指向。

## 4. 实现与实际检查

修复提交为本记录所在提交。校准 MCP 工具说明和字段约束、目录指纹、正式文档与部署提示。
目录仍有 13 个工具，源码指纹为 `f00f09510ffa4129a5dfbbd88b51b3e209b2dec677a2f99dfe332ea40035f3b8`。
部署提示使用清单工具数，并指向现行运维说明。讨论已确认最小校准路线，先红定位后直接修复。

| 检查 | 实际结果 |
| --- | --- |
| `GOEXPERIMENT=jsonv2 go test ./internal/mcp ./scripts/mcp-probe -count=1` | 两包通过，覆盖目录指纹、发现预算、指令和探针比对 |
| `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/device-mcp ./scripts/mcp-probe ./scripts/toolcatalog` | 通过 |
| `GOEXPERIMENT=jsonv2 go run ./scripts/toolcatalog` | 13 个工具，指纹与本文、正式文档和测试基线一致 |
| PowerShell `Parser.ParseFile` 解析 `scripts/deploy.ps1`，单独执行最后一条提示语句 | 无语法错误，样例清单计数 13 渲染到 `tools/list` 提示，指向运维说明 §10.4 |
| `git diff --check` | 通过 |
| Codex 桌面同版本 `0.169.0-alpha.11.1` 的新 `app-server`，用 `mcpServerStatus/list` 只发现 `mobile-use` | 现有配置、磁盘适配器和临时源码构建均返回 13 个工具，`snap` 的 `annotations.readOnlyHint=true`。三个目录响应的工具描述均未附全局指令。仅临时源码构建返回 `act`、`inspect` 的 `^O` 与校准后的 `act` 说明 |

## 5. 同类核查

`TestToolCatalog` 核对所有工具的 `reference` pattern。其他公开该字段的工具保留通用
`^[OP]`，包括根 Schema 需要容纳多个 kind 的 `app`。仓库当前工具清单以 13 为准。旧 M6 脚本
的 11 工具断言属于历史专项检查，已有[验证记录](act-tap-safe-scope-and-guidance.md)记载它
已脱离发布门禁。当前部署结束提示改指向[运维探针说明](../../docs/runbook.md#104-mcp-探针)。

## 6. 验证边界

当前工作对话的工具视图第二次检查仍只有 12 项，且每项附有全局指令。新 `app-server` 的
检查覆盖真实 Codex 版本的目录发现接口。新对话的模型工具视图与该接口之间的投影未测。
没有重启当前桌面进程、覆盖 `build/device-mcp.exe`、重建发布清单或部署手机。
HTTP 解码、引擎准入与实际设备执行未变更。旧 M6 脚本包含设备重启与故障演练，未运行。
