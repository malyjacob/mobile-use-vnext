# M5/G5 实机验证记录（MCP 双入口、图片链路与 Go client）

状态：2026-11-09。本页只记录实际执行过的命令与真实结果；未执行项明确写“未验证”，不把单元
测试或设计推演写成实机结论。原始日志、节点树、坐标、令牌、图片字节、命令行输出与个人信息
不入库；下文只保留受控计数、枚举、尺寸与布尔。

## 1. 版本与环境

| 项 | 值 |
| --- | --- |
| 代码提交 | 实现提交 `8e62b7c3b4c17f53dbc67bd20d79bed185bd1ce6`（基线 `631a18d`） |
| 服务构建 | `0.3.0`；`device-service` SHA-256 `97a2565b32349baba51b71f5c8c13591d55b25ce627e54860e7388983f9e57d9` |
| MCP 适配构建 | `device-mcp` SHA-256 `5ede24179cc48ed5efc0f44070d076fba0ac0d6291f9da0423dfcc7545833e0f`（linux/arm64 构建产物） |
| MCP SDK | `github.com/modelcontextprotocol/go-sdk v1.8.0` |
| MCP 协议 | 新协议 `2026-07-28`（客户端 `server/discover` 协商） |
| 契约 | `contract_version = 1` |
| 设备 | `24117RK2CC`，Android 16（API 36），arm64，Magisk + Debian 13 chroot |
| chroot / 服务 | `/data/local/chroots/debian13`；`/opt/mobile-use-vnext`；`18880`；主机 `18881` |
| 配对 App | `com.mobileuse.vnext` |
| MCP 私有配置 | `.state/mcp-config.json`（0600；令牌不入库、不打印） |

## 2. 构建与部署

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/deploy.ps1 -ReleaseVersion 0.3.0
```

结果：Go 服务与 MCP 适配均构建成功，打印上述 SHA-256；控制 App 重建并安装；部署后
`/health` 返回 `contract_version=1`、`release_version=0.3.0`，`/api/status` 返回
`can_start=true`、`cleanup.state=complete`。部署脚本随后启动配对 App。

## 3. 单元测试与静态检查（Windows，非实机证据）

```bash
GOEXPERIMENT=jsonv2 go test ./... -count=1
GOEXPERIMENT=jsonv2 go vet ./...
GOEXPERIMENT=jsonv2 go test ./internal/engine/ -count=5
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOEXPERIMENT=jsonv2 go build -mod=readonly ./...
git diff --check
```

结果：

- `go test ./...`：全部包 `ok`（`internal/android`、`internal/client`、`internal/engine`、
  `internal/imaging`、`internal/mcp`、`internal/observation`、`internal/protocol`、
  `scripts/mcp-probe`；`internal/httpapi`/`store`/`eventlog`/`device` 无包内测试）。
- `go vet ./...`：无输出。
- `go test ./internal/engine/ -count=5`：`ok … 361.776s`（5 次全绿）。
- linux/arm64、`CGO_ENABLED=0` 全量构建：通过；`-mod=readonly` 构建：通过。
- `git diff --check`：无输出。
- `go test -race` 未执行：本机无 C 编译器（cgo 不可用），不把普通构建写成 race 结论。

## 4. MCP stdio（真机）

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-m5.ps1
```

其中 stdio 分段（完整输出见本节末）：

```text
PASS connect transport=stdio
PASS tools count=12
PASS schema objects=12 additional_properties_false=12 required_start=0 required_observe=1 required_act=2 required_status=0
PASS status can_start=true
PASS start P=P0196711cae1ca50424d6da4b O1=O15 session=active tree=full
PASS observe reference=O15 id=O16 status=complete tree_format=full image_mime=image/jpeg image_bytes=10793 width=540 height=1200
PASS act outcome=executed key=BACK id=O17 status=complete image_mime=image/jpeg image_bytes=10793 width=540 height=1200
PASS end outcome=executed session=ended cleanup.state=complete
PASS cancel P=P374cd22b026c95f4febe87c5 returned=context_error session=none can_start=true operation_state=finished
RESULT PASS
probe stdio exit=0
```

核对：

- 协议版本：SDK 客户端实际协商 `2026-07-28`（独立冒烟程序输出 `protocol_version: 2026-07-28`）。
- 工具目录：恰好 12 个（`start`、`status`、`observe`、`act`、`inspect`、`keepalive`、
  `end`、`apps`、`open_app`、`notify`、`shell`、`diagnose`），无额外工具。
- schema：12 个均为 `type=object`、`additionalProperties=false`；`start` 无属性；
  `observe.required=["reference"]`；`act.required=["observation_id","operation"]`。
- 结果与图片：`observe(image=true)` 与 `act(image=true)` 的图片为 `image/jpeg`、540×1200、
  10793 字节；文本与结构化内容均不含 `base64` 或图片载荷；`ImageContent` 与
  `observation.image` 元数据（`captured_at`/`width`/`height`/`media_type`）同时存在。
- 清理：`end` 后 `session.ended`、`cleanup.state=complete`，`can_start=true`。
- 取消：取消一次 `observe(wait_ms=8000)` 后调用返回；后续 `status()` 显示无活动会话、
  `can_start=true`；被取消调用的 P 经 `status(P)` 为 `operation_state=finished`，即
  MCP 接入层按该 P 发起了有界 `end(P)`。

## 5. MCP Streamable HTTP（真机）

同一 `verify-m5.ps1` 的 HTTP 分段：

```text
PASS auth_missing status=401
PASS origin_denied status=403
PASS origin_allowed status=200
PASS agent_token_rejected status=401
PASS connect transport=http
PASS tools count=12
PASS schema objects=12 additional_properties_false=12 required_start=0 required_observe=1 required_act=2 required_status=0
PASS status can_start=true
PASS start P=Pee2cf640435b82c294388564 O1=O19 session=active tree=full
PASS observe reference=O19 id=O20 status=complete tree_format=full image_mime=image/jpeg image_bytes=10793 width=540 height=1200
PASS act outcome=executed key=BACK id=O21 status=complete image_mime=image/jpeg image_bytes=10793 width=540 height=1200
PASS end outcome=executed session=ended cleanup.state=complete
PASS cancel P=Pd9b5ccaf1e16626e2ad1d254 returned=context_error session=none can_start=true operation_state=finished
RESULT PASS
probe http exit=0
```

核对：

- 认证：无 Bearer → 401；使用服务 `agent_token` → 401（MCP 宿主令牌与服务 Agent 凭据
  分离）；允许 Origin → 非 401/403；错误 Origin → 403。
- 传输：SDK 客户端协商 `2026-07-28`；12 工具与 stdio 相同；`start/observe(image)/act(image)/
  end` 与 stdio 结果一致；图片 540×1200、`image/jpeg`、10793 字节，正文无 base64。
- 取消：取消 `observe(wait_ms=8000)` 后 SSE 请求流关闭，适配层记录 `call_cancelled`，
  会话结束且 `can_start=true`；被取消 P 为 `operation_state=finished`。

## 6. 服务→SDK 漏收与 P 恢复（真机）

```text
PASS status can_start=true
PASS missed_response P=P4b0b4024f1e5addc072733e9 transport=true
PASS status_recovery operation_state=finished
PASS single_dispatch session=active start_operation_id=P4b0b4024f1e5addc072733e9
PASS observe reference=P4b0b4024f1e5addc072733e9 O=O24 tree=full status=complete
PASS end outcome=executed session=ended cleanup.state=complete can_start=true
PASS duplicate P=P4b0b4024f1e5addc072733e9 operation_state=finished new_session=false
RESULT PASS
probe p-recovery exit=0
```

该分段用真实服务与一个“发送请求后丢弃响应”的 HTTP 传输模拟服务→SDK 漏收：

- `Start` 返回 `transport=true` 的结构化错误并保留 P，不重发；
- `status(P)` 查到原操作 `finished`，`status()` 的 `start_operation_id` 等于该 P，证明只有
  一个启动派发；
- `observe(P, full=true)` 恢复新 full O；
- `end(P)` 后 `cleanup.state=complete` 且 `can_start=true`；
- 用同一 P 再次 `start` 只得到原操作视图，未创建第二个会话。

MCP 外层整份工具结果丢失且宿主无 P 的情形无法用现有 MCP 客户端注入验证。适配层不提供
按 MCP 请求 ID 查询的工具或参数；恢复入口只有已有 O/P 或 `status()`。本项未验证，不宣称
已实测。

## 7. 图片链路分层核对

单元测试（`internal/engine/image_test.go`、`internal/imaging`、`internal/mcp`）覆盖并实际
执行：

- `image=false`：不调用 `CaptureImage`、不编码、结果无 `image`；
- 核心树有效、图片失败：`status=partial`、`missing=["image"]`、`image.error`，O 仍有效；
- 图片成功、核心树失败：无新 O，`read_only=true` 且带同屏 `screen`；
- 图片成功、核心树有效：`status=complete`、`image/jpeg`、输出尺寸为实际缩小尺寸；
- 图片过跨度 → `image_stale` 且不附加；编码超限 → `image_too_large`；编码/来源失败 →
  `image_encode_failed`/`image_capture_failed`；
- `actionable_until` 取图片与树更早的读取起点；
- HTTP 层的 `observation.image.base64` 由 `internal/client` 解码到 `Response.Image` 后清空；
  MCP 文本/结构化内容不含 base64。

实机图片为同屏静态帧：`act(key=BACK)` 后图片字节数与尺寸相同，未构造页面明显变化的图像
对比，因此“页面变化后的图片内容差异”未验证。

## 8. 未验证 / 限制

| 项 | 状态 |
| --- | --- |
| `go test -race` / 竞态检测 | 未执行：Windows 宿主无 C 编译器，cgo 不可用 |
| MCP HTTP 多宿主并发连接、SSE 重连 | 未构造；stateless 单请求隔离与 401/403 已实测 |
| MCP 外层整份结果丢失且宿主无 P | 无法注入；无 request-id 恢复工具，边界与工具目录已核对 |
| 页面明显变化后的图片内容差异 | 未构造；只验证同屏静态帧与格式/尺寸/无 base64 |
| M4 遗留细粒度证据（apps/inspect 实机预算、open_app 并发、Android 重启等） | 仍未补；不属本次变更路径 |
| 完整 control_view、App 界面整理、发布包 | 未实现（后续阶段） |

## 9. 证据边界

- 本页的实机结论只来自 `scripts/verify-m5.ps1` 的三段 probe 输出与两次独立冒烟程序；
  原始输出未入库，未记录节点树、图片字节、令牌或设备日志。
- 构建与部署由 `scripts/build.ps1`、`scripts/deploy.ps1` 完成；部署后服务实例 ID 变化，
  App 重新安装并启动。
- 单元测试通过不等同于实机结论；实机未覆盖项已在第 8 节单列。
