# MCP 树块实现与验证

状态：已实现、已单测、已实机（主机 stdio 探针与手机 HTTP MCP）。
实现提交：`e4867cd4f611217b8ea858f1cdfa4daa09e3acde`。
提案：[MCP 文本结果附带确定性可读树块](../proposals/implemented/mcp-tree-text.md)。

## 1. 结论与边界

`internal/mcp` 在 `key=value` 阶段事实之后追加一个纯函数树块：观察结果渲染
full/delta，`inspect` 渲染 tree/subtree/query。`structuredContent`、`/api/*` 字段、
`contract_version` 与工具入参 schema 均未改变；工具 description 追加同一段语法图例，
因此 `CatalogFingerprint` 随源码变化，发布清单留待下次发布重新生成。

代码落点：

| 落点 | 内容 |
| --- | --- |
| `internal/mcp/tree_text.go` | `renderTreeText`、full/delta/inspect 写入器、显式栈遍历、JSON 转义、ref 校验与数值排序、24/32 KiB 预算缓冲 |
| `internal/mcp/server.go` | `handle` 在 `renderText` 之后追加块，块前恰好一个空行 |
| `internal/mcp/render.go` | 正文注释更新；`renderText` 本身未改 |
| `internal/mcp/schema.go` | `start`、`observe`、`act`、`open_app`、`inspect` 五个 description 共用 `treeTextGuide` |
| `internal/mcp/tree_text_test.go` | golden、显式值、三种 inspect、纯函数、round-trip、计数、转义、预算、非法 ref、终止行、空树、无树 |
| `internal/mcp/server_test.go` | 端到端块断言；原 `requireTestNotContains(text, "n1")` 改为根节点行断言 |
| `scripts/mcp-probe/checks.go` | observe/act 块断言与一次 inspect 补看块断言 |
| `docs/mcp.md`、`docs/interface.md`、`maintenance/decisions.md` | §3/§3.1、§12 预算口径、D32 |

## 2. 环境

- 宿主：Windows，`go version go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`。
- 设备：Xiaomi `24117RK2CC`，Android 16，ADB 序列 `17efb04a`。
- 已部署服务：`release_version=0.4.1`、`contract_version=3`、`can_start=true`。
- 实机路径：当前源码构建的 `build/device-mcp.exe` 以 stdio 连接已部署服务；不是外部模型宿主。

## 3. 单元测试

```text
GOEXPERIMENT=jsonv2 go test ./... -count=1
```

结果：全部包通过；`internal/engine` 143.638s，`internal/mcp` 0.259s，
`scripts/mcp-probe` 1.852s，其余包通过。

```text
GOEXPERIMENT=jsonv2 go vet ./...
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

结果：两条命令均以退出码 0 结束，无输出。

## 4. 反向对照

逐项改动实现、运行对应测试、确认失败后恢复；恢复后用 SHA-256 核对文件与改动前一致。

| 去掉的实现 | 运行用例 | 实际结果 |
| --- | --- | --- |
| `handle` 不再追加 `renderTreeText` | `TestTextCarriesTreeBlock` | 5 个子用例失败，文本缺少 `end tree`/`end inspect` |
| `finish` 不再执行预算判断 | `TestTreeTextOmitsWholeBlock` | full/delta/inspect 都输出正常块而非 `omitted` |
| full 块终止行改为 `end` | `TestTreeTextTrailer` | full 块不以 `end tree` 结尾 |
| `validatePublicRef` 恒为真 | `TestTreeTextRejectsMalformedRef` | `n01` 等非法 ref 被渲染进正文 |

`internal/mcp/tree_text.go` 恢复后 SHA-256 为
`05ec866527109292b5fc451d666b05ddd8355bacca2c11b83cdbbc9c5e097829`；
`internal/mcp/server.go` 恢复后 SHA-256 为
`18b1b00b41b2ee04a5262925270ee8ee384564e7d8ffecbb7ab2e05be0efca8c`。

## 5. 实机（MCP stdio）

```text
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-m5.ps1 -Transport Stdio
```

结果 `RESULT PASS`，探针实际执行：

```text
PASS connect transport=stdio
PASS tools count=12
PASS schema objects=12 additional_properties_false=12 required_start=0 required_observe=1 required_act=2 required_status=0
PASS status can_start=true
PASS start P=… O1=O1 session=active tree=full
PASS observe reference=O1 id=O2 status=complete tree_format=full image_mime=image/jpeg image_bytes=10793 width=540 height=1200
PASS inspect scope=tree observation=O2 tree_block=complete
PASS act outcome=executed key=BACK id=O3 status=complete image_mime=image/jpeg image_bytes=10793 width=540 height=1200
PASS end outcome=executed session=ended cleanup.state=complete
PASS cancel P=… returned=context_error session=none can_start=true operation_state=finished
RESULT PASS
```

实机记录中的 P/O 编号每次不同；实现提交 `e4867cd4f611217b8ea858f1cdfa4daa09e3acde`
后在同一设备与部署上复跑了同一命令，结果仍为 `RESULT PASS`，上述断言全部通过。

这条路径证明：实际适配进程对实际服务/设备生成的 `observe`、`act` 文本含完整树块，
`inspect` 文本含补看块与终止行；`structuredContent` 仍可被探针解码，图片仍只走
`ImageContent`。探针不打印树内容、图片字节或令牌。

## 6. 未执行项与原因

| 项 | 状态与原因 |
| --- | --- |
| 外部模型宿主（LangChain 等）实机调用 | 未执行：本机没有该宿主；实机验证使用仓库 MCP 客户端探针 |
| full 块与旧文本的 token/字符数对比 | 未执行：需要模型 tokenizer 或固定采样屏；不把估算写成实测 |
| 适配器 24 KiB 省略的实机构造 | 未执行：实际页面未达到上限；超预算路径由 `TestTreeTextOmitsWholeBlock` 覆盖 |
| 宿主截断（略低于 24 KiB、输出上限更小） | 未执行：需要可配置截断的宿主；测试只证明缺少终止行可被识别 |
| 引擎 32 KiB 查询截断/超限的实机构造 | 未执行：实际树未达到查询预算；判据由引擎单元测试覆盖 |
| `go test -race` | 未执行：Windows 宿主无 C 编译器，cgo 不可用 |

## 7. 与提案的差异

无实现差异。提案 §8.2 中列出的外部宿主、token 对比、宿主截断与实机超预算场景未执行，
原因见上表；未把单元测试结论写成实机结论。

## 8. 0.4.2 整组部署与手机 HTTP MCP

实现归档后按发布流程补齐了手机侧部署：

- `release/versions.json` 前进为 `0.4.2`、`contract_version=3`、`android_version_code=5`，
  版本提交为 `d4cedfd`；生成常量与 AndroidManifest 同步，`generate-version.ps1 -Check` 通过。
- `scripts/build-release.ps1` 产出完整清单：`release=0.4.2`，`source_commit=d4cedfd`，
  `source_dirty=false`，`device-mcp` SHA-256 为
  `f7892335eb30087fe5fad7ac91854de242efc03c52ee657c9fa7ebe2696fc7bc`。
- `scripts/verify-manifest.ps1` 返回 PASS：6 个产物、工具目录 12 项、Android `0.4.2/5`。
- `scripts/deploy.ps1` 返回 PASS：覆盖安装保留 App 私有数据，服务、App 与手机 HTTP MCP
  适配器均按清单哈希核验；手机侧 MCP HTTP 进程的 `/proc/<pid>/exe` 哈希与清单一致，
  手机 MCP URL 为 `http://127.0.0.1:18883/mcp`。
- `scripts/verify-m6.ps1` 返回 `RESULT PASS`：发布清单、服务身份、App `0.4.2/5`、工具目录、
  手机 MCP `server/discover`、契约配对、最小 STOP、消息/通知/消息中心、重启与 SIGTERM
  恢复屏障均通过。
- 手机 HTTP MCP 文本块：临时验证客户端（令牌从私有部署状态读取，不打印）调用 `start`
  与 `inspect`；`start` 文本 806 字节，含 full 树块与 `end tree`；`inspect` 文本 374 字节，
  含 inspect tree 块与 `end inspect`；随后 `end` 返回成功。树内容与令牌均未输出。
- 仍有一个既有 DSH 宿主的 **stdio** connector 进程运行旧二进制（`a54e955…`），
  因为宿主仅在启动时 spawn 子进程；重启该宿主 MCP 进程后才会加载 0.4.2。手机默认
  HTTP MCP 已是最新；手机侧 HTTP 适配器不受此影响。
