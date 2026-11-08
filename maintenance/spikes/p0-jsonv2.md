# Spike：`encoding/json/v2` 严格接入与三态编码

状态：2026-11-08，**部分完成**。本页只记录代码与测试能证明的内容；MCP/go-sdk/stdio 相关
部分未开展。

## 1. 范围

- 严格接入：未知成员、重复成员名、大小写变体、尾随第二个 JSON 值、类型混用、必填缺失、
  非 nullable 字段的显式 `null`。
- 三态：字段缺失 / 显式 `null` / 零值（`false`、`0`、`""`、`[]`）在请求与树/delta 编码中
  不混淆。
- 工具链边界：Go 1.27.1 + `GOEXPERIMENT=jsonv2`。

## 2. 实现落点

| 关注点 | 代码 |
| --- | --- |
| 严格解码入口 | `internal/protocol/json.go`（`json.Unmarshal(..., json.RejectUnknownMembers(true))`） |
| 请求存在性与空值 | `internal/protocol/requests.go`（先 presence pass，再 typed pass；`checkNulls`） |
| 完整树默认省略 | `internal/protocol/node.go`（`Node.MarshalJSONTo`：`enabled` 默认 true、`focused`/`selected`/`protected` 默认 false、`checked`/`value`/`hint` 等未知省略） |
| delta `set` 三态 | `internal/protocol/node.go`（`Field[T]` + `omitzero`；`false`/`0`/`""`/`[]`/`null` 均显式） |
| 错误词汇 | `internal/protocol/errors.go`（稳定码 + HTTP 映射） |

## 3. 测试证据

包内测试（`internal/protocol/protocol_test.go`）：

- `TestStrictAdmission`：覆盖 `start` 未知成员、重复成员、大小写变体、尾随值、`reference`
  的 `null`/类型错误、`observe` 的必填/`full=null`/`wait_ms` 越界、`act` 的坐标类型/互斥/
  缺项/多余成员/`text=null`/`key=null`、`image=null` 等拒绝与接受分支；
- `TestTextTooLong`：超过 4096 个码点拒绝；
- `TestNodeDefaultEncoding`、`TestNodeRoundTripDefaults`：已知 `true` 省略、未知 `enabled`
  显式 `null`、`checked=false`/`value=""`/`bounds=null` 保留；
- `TestTreeAndDeltaEncoding`、`TestTreeValueDecode`、`TestEmptyDeltaDecode`：full/delta
  的判别与空差异编解码。

相关回归：`internal/observation` 的 delta 往返/不可表达拒绝、`internal/engine` 的
`TestDeltaEncodingIsJSONStable`，以及本轮实机检查（重复键/未知成员/大小写变体/尾随 JSON
→ `422`，缺契约头 → `409`，错误 token → `401`）。

本地以 `GOEXPERIMENT=jsonv2 go test ./...` 在写作时点执行过一次，`protocol`、
`observation`、`engine`、`android` 四个当时已有测试的包通过（2026-11-08，Windows 开发
环境）。

## 4. 结论与缺口

- 已证明：锁定工具链下，严格接入与“缺失/`null`/零值”区分可按契约工作；完整树与 `set`
  的省略语义分离。
- 未证明：`internal/httpapi` 没有包内测试，HTTP 状态/请求体上限/认证组合没有自动化回归；
  实机检查只覆盖了列出的样例。
- 未开展：go-sdk 入参原始 JSON 接入、stdio 的 `notifications/cancelled` 取消映射、
  Streamable HTTP 的 SSE 关闭、MCP 认证包裹、`ImageContent` 字节载体——这些依赖尚不存在的
  `internal/mcp`/client，本 spike 不宣称通过。
- 因此 G0 中“严格 JSON 语义”部分可认为有实现与测试依据；“go-sdk 四项”仍未完成。
