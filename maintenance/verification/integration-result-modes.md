# Agent 接入指南结果模式文档补全验证记录

状态：2026-11-10。本页记录 `docs/integration.md` 缺少 MCP 结果模式接入说明的现象、
复现、根因、修复与文档审计。本次只改正式文档与维护记录，不改代码、MCP 结果投影行为、
工具目录、`/api/*` 契约、发行号或发布清单。

## 1. 现象与触发条件

按 `docs/integration.md` 配置 MCP 宿主时，只能看到 URL、Bearer token、Origin 与
`mcp-config.json` 字段；没有说明 `both`、`text`、`structured` 三种结果模式、默认值、
启动参数/环境变量/HTTP 请求头、模式内容差异或非法值边界。接入方只能使用默认 `both`，
无法按宿主能力选择单向结果形式。

## 2. 复现用例（修复前为红）

文档级复现：

```powershell
rg -n "result-mode|结果模式|X-Mobile-Use-Result-Mode|MOBILE_USE_MCP_RESULT_MODE|structuredContent" docs/integration.md
```

修复前输出 `NO_MATCHES`（退出码 `1`）。同一搜索在 `docs/mcp.md` 与 `docs/runbook.md`
有匹配，说明适配器契约已有正式文档，缺口在 Agent 接入指南。

## 3. 根因

| 位置（修复前） | 问题 |
| --- | --- |
| `docs/integration.md:152` | §4.3 只写 URL 与 `Authorization`，没有结果模式选择 |
| `docs/integration.md:189` | §4.5 直接进入 Origin；没有承载 MCP 结果模式的接入说明 |
| `docs/integration.md:432` | §10 配置字段表只列适配器 JSON 字段，没有说明结果模式不属于该配置 |
| `internal/mcp/result_mode.go:12-25` | `both`/`text`/`structured`、请求头与环境变量已经定义 |
| `internal/mcp/result_mode.go:34-82` | 精确值解析、默认值与请求头拒绝边界已经存在 |
| `internal/mcp/result_projection.go:5-38` | 三种模式的 `content`/`structuredContent` 组装已经存在 |
| `internal/mcp/http.go:43-46` | 非法模式头在工具执行前返回 `400` |
| `cmd/device-mcp/main.go:106-122` | `-result-mode` > 环境变量 > `both` 的优先级已经存在 |
| `cmd/device-mcp/config.go:17-29` | 适配器配置只有 `service`、`transport`、`http`，没有 `result_mode` |

根因是正式文档的组织缺口：`docs/mcp.md` 与 `docs/runbook.md` 已描述结果模式，但
`docs/integration.md` 的宿主接入路径没有引用这些事实，接入方按本页配置时看不到选择方式。

## 4. 修复提交、改动边界与验证

修复提交：本记录所在提交。

改动边界：

- `docs/integration.md` 新增 §4.5 结果模式，包含模式表、默认值、三种选择方式、stdio 与
  HTTP 示例、严格 JSON 配置边界和截断提示；原 §4.5 Origin 顺延为 §4.6。
- §6.3、§6.4、§6.5、§7.1、§10、§11、§12 增加结果模式交叉引用、配置提示、FAQ 与
  `400 invalid result mode` 映射。
- 本记录只记录文档补全，不改变适配器、服务、App、契约号、工具目录或发布产物。

文档内验证：

```powershell
rg -n "result-mode|结果模式|X-Mobile-Use-Result-Mode|MOBILE_USE_MCP_RESULT_MODE|structuredContent" docs/integration.md
```

修复后命中 §4.5、§6.3、§6.4、§6.5、§7.1、§10、§11 与 §12 的预期行。

```powershell
git diff --check
```

相对 Markdown 链接检查输出 `LINKS_OK`；`git diff --check` 无空白错误。

## 5. 同类核查

| 同类点 | 结论 |
| --- | --- |
| `docs/mcp.md` §3–§5 | 已描述三模式、`result v1`/`diagnostic v1`、stdio 优先级与 HTTP 头边界 |
| `docs/runbook.md` §10.3 | 已描述启动参数、环境变量、每请求头、退出码 `2` 与 `400` |
| `docs/client.md` | 描述 Go 客户端访问 `/api/*`，不经过 MCP 结果模式投影，无需补模式配置 |
| `README.md` 能力边界 | 只概述 MCP 双入口并链接 `docs/mcp.md` 与 `docs/integration.md`，不展开宿主配置 |
| `docs/integration.md` §10 | 已明确结果模式不是适配器 JSON 成员，避免把 `result_mode` 写入严格配置 |

## 6. 未覆盖项与原因

| 项 | 原因 |
| --- | --- |
| 外部模型宿主实连与截断 | 本次是文档补全，不新增代码路径；现有三模式实机验证见 [mcp-result-modes](mcp-result-modes.md) |
| 手机适配器进程默认模式切换 | 文档只描述启动参数/环境变量与每请求头；默认部署没有单独的结果模式参数，未新增部署脚本能力 |
| `docs/mcp.md`、`docs/runbook.md` 的重复审计 | 本次未修改这两个文件；内容已在其中存在，接入指南只做交叉引用与最小重复 |

文档审计：独立只读审计 subagent 审核 `docs/integration.md`，返回 `CLEAN`。审计者未修改
文件，审计期间未改动被审文档。
