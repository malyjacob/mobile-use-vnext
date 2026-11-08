# mobile-use-vnext

mobile-use 的全新实现：Go 设备服务（运行于手机内 Debian chroot，linux/arm64）＋ Java 控制
App（`com.mobileuse.vnext`）＋ 固定的 uiautomator 服务制品。目标是把“会话—观察—操作—收尾”
做成单一权威、可恢复、可验证的设备控制链路：服务只监听 `127.0.0.1`，控制 App 只通过私有
HTTP 协议配对。

- 模块：`github.com/malyjacob/mobile-use-vnext`
- Go：1.27.1，启用 `GOEXPERIMENT=jsonv2`
- 契约：`contract_version = "1"`，服务、HTTP 客户端、控制 App 精确相等才做普通工作
- 发布标识：由构建注入（`-ldflags -X internal/protocol.ReleaseVersion`）；源码默认 `0.1.0-dev`，不是契约

## 能力边界

- Agent 工具为 `POST /api/<tool>`，当前注册 `start`/`status`/`observe`/`act`/`end`/
  `inspect`/`keepalive`/`diagnose`/`apps`/`open_app`/`notify`/`shell`；逐项字段、错误码与
  空值规则见 [接口契约](docs/interface.md)。
- `observe`/`act` 的 `image=true` 返回与本次树同次采集的一张 JPEG 图片；图片元数据在
  `observation.image`，HTTP 载体为 `observation.image.base64`。
- 控制端为 `POST /v1/control/sync|stop`；显示命令固定为 `create_display`、`close_display`，
  见 [控制端最小协议](docs/control-protocol.md)。
- MCP 双入口（stdio / Streamable HTTP）由独立 `cmd/device-mcp` 提供，Go 客户端在
  `internal/client`；见 [MCP 入口](docs/mcp.md) 与 [Go 客户端](docs/client.md)。

## 目录

```text
cmd/device-service/     设备服务进程组装、进程锁、启动与退出
cmd/device-mcp/         MCP 双入口进程（stdio / Streamable HTTP）
internal/protocol/      公共数据、严格 JSON 接入、错误码与可读排版
internal/device/        原始设备事实类型（不执行 I/O、不持运行状态）
internal/observation/   公开树构造、保守保号、full/delta 计算与还原（纯算法）
internal/imaging/       等比缩小与单次 JPEG 编码（纯算法）
internal/engine/        唯一业务入口：会话、O/P、工作位置、STOP、收尾、恢复
internal/android/       ADB、u2 RPC、制品管理与受管执行链
internal/httpapi/       认证 HTTP 接入、/api/<tool>、控制端同步与最小 STOP
internal/client/        强类型 HTTP 客户端（发送前 P、结构化结果、图片字节）
internal/mcp/           12 工具注册/schema/结果投影、取消包装、HTTP 认证与 Origin
internal/store/         私有恢复账本（原子文件、单写者、修订序）
internal/eventlog/      有界事件/字段白名单日志
android-control/        Java 控制 App 源码与构建入口（含 test-target 验证探针）
third_party/uiautomator/固定 JAR 输入/补丁/哈希/来源/许可证
scripts/                构建、部署与手机内 service.sh
docs/                   正式文档
maintenance/            决策、验证记录、spike 与未决项
```

## 构建

Go 服务只以 linux/arm64、`CGO_ENABLED=0` 为运行目标；Windows 仅承担构建与开发检查，
不提供运行账本的耐久证据。

```bash
# POSIX 单行（Git Bash / WSL）
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

```powershell
# Windows PowerShell（等价写法）
$env:GOEXPERIMENT = 'jsonv2'; $env:GOOS = 'linux'; $env:GOARCH = 'arm64'; $env:CGO_ENABLED = '0'
go build ./...
```

发布构建使用脚本（自动设置上述环境、注入版本、打印 SHA-256）：

```powershell
./scripts/build.ps1                              # 输出 build/device-service 与 build/device-mcp，默认 0.3.0
./scripts/build.ps1 -Version 0.3.0 -Output build
```

MCP 适配进程若运行在 Windows 宿主上（stdio/HTTP 验证），用宿主工具链单独构建：

```powershell
$env:GOEXPERIMENT = 'jsonv2'; go build -o build/device-mcp.exe ./cmd/device-mcp
```

控制 App 由 `android-control/build.ps1` 用本机 SDK/JDK 直接构建（无 Gradle），输出
`android-control/build/control.apk`。验证探针 App 由 `scripts/build-probe.ps1` 构建，仅用于
实机验证，不进入发布包。

## 部署

`scripts/deploy.ps1` 面向当前单一机型/单一 chroot 的受控部署，要求已有一台在线 adb 设备；
脚本会做：构建（可用 `-SkipBuild` 跳过）→ 读取或生成令牌 → 写私有配置 → 停止旧控制
（含旧 Python 服务，尽力而为）→ 推送并安装到 chroot → 安装并启动控制 App → 建立
`adb forward` → 轮询 `/health` 与 `/api/status`，直到 `can_start=true`。

```powershell
./scripts/deploy.ps1                       # 完整构建并部署
./scripts/deploy.ps1 -SkipBuild            # 复用已有 build/device-service 与 APK
./scripts/deploy.ps1 -AppPort 18880 -HostPort 18881 -AgentName local-agent
```

默认目标（与脚本参数一致）：

| 项 | 值 |
| --- | --- |
| chroot | `/data/local/chroots/debian13` |
| 服务目录 | `/opt/mobile-use-vnext`（`device-service`、`u2.jar`、`service.sh`、`config.json`） |
| 状态目录 | `/var/lib/mobile-use-vnext`（账本、pid、日志；目录 0700） |
| 服务端口 | `18880`（仅 `127.0.0.1`） |
| adb 串口 | `127.0.0.1:5555` |
| 主机验证入口 | `adb forward tcp:18881 tcp:18880` 后的 `http://127.0.0.1:18881` |

**私有配置不入库。** 部署在仓库根写入 `.state/`（已由 `.gitignore` 排除，脚本另设 0600）：

- `.state/deploy.json`：`agent_token`、`control_token` 与端口等部署记录；
- `.state/config.json`：服务私有配置（含两个令牌）；
- `.state/connection.json`：推送进 App 私有目录的 `base_url` + `control_token`。

这些文件只在部署机与手机私有目录存在，不复制到仓库、文档或日志。

## 测试

```bash
GOEXPERIMENT=jsonv2 go test ./...
```

```powershell
$env:GOEXPERIMENT = 'jsonv2'; go test ./...
```

包内测试覆盖 `internal/protocol`、`internal/observation`、`internal/imaging`、`internal/engine`、
`internal/android`（ADB/u2 走本地桩，不需要真机）、`internal/client`、`internal/mcp` 与
`scripts/mcp-probe`；`internal/httpapi`、`internal/store`、`internal/eventlog`、
`internal/device` 没有包内测试。Windows 上的通过不证明设备路径或账本耐久性。

## 接口

Agent 工具（`POST /api/<tool>`，`Authorization: Bearer <agent_token>`，
`X-Mobile-Use-Contract: 1`）：

| 工具 | 说明 |
| --- | --- |
| `start` | 请求体必须为 `{}`；创建/绑定目标屏幕并发布首个 full 观察 |
| `status` | 无参数、`{"reference":"O…"}` 或 `{"reference":"P…"}`；只读查询 |
| `observe` | `reference`（O/P）；按服务端基线返回 full 或 delta；`observe(P)` 与 `full=true` 强制 full；`image=true` 附加本次图片 |
| `act` | `observation_id` + `operation`：`tap`、`long_press`、`swipe`、`key`、`text`；`image=true` 为后观察附图 |
| `inspect` | `observation_id` 可选 `node` 或 `query`；只读历史树 |
| `keepalive` | `reference`（O/P）；续租原活动会话 |
| `end` | `reference`（O/P）；撤权并启动独立收尾 |
| `apps` | 可选 `query`；发现可启动应用 |
| `open_app` | `observation_id` + `app`；在会话屏打开/复用应用并返回新观察 |
| `notify` | `text`；保存一条 Agent 消息 |
| `shell` | `reference` + `command` + 可选 `root`；受管命令执行与后观察 |
| `diagnose` | 可选 `reference`；有界诊断事实 |

控制端（`POST /v1/control/sync|stop`，`Authorization: Bearer <control_token>`）：心跳同步
下发普通控制视图与至多一条显示命令，独立 STOP 只绑定停止边界、不等待清理。`GET /health`
无认证，只表示进程可应答。

严格字段、错误码与空值规则见 [接口契约](docs/interface.md)；控制端协议细节见
[控制端最小协议](docs/control-protocol.md)。

## 未提供

- App 消息中心的完整通知交付、已读回执与界面整理。
- 完整 `control_view` 呈现与整组发布包组装；破坏性发布演练不在当前能力范围。
- 历史图片补取、图片差异、裁剪/质量参数与第二套观察配置。

## 文档

- [架构](docs/architecture.md)：进程分层、唯一权威、O/P 生命周期、full/delta、图片附加材料、STOP 与收尾、恢复屏障、账本。
- [接口契约](docs/interface.md)：逐端点 JSON、空值规则、错误码、图片与工具支持范围。
- [运行手册](docs/runbook.md)：手机环境、部署/启动/停止、健康检查、MCP 入口与常见故障。
- [控制端最小协议](docs/control-protocol.md)：同步、显示命令、独立 STOP。
- [MCP 入口](docs/mcp.md)：双传输、工具 schema、结果投影、取消与漏收边界。
- [Go 客户端](docs/client.md)：P、结构化错误、发现与图片字节。
- [工程决策](maintenance/decisions.md) 与 `maintenance/verification/`。

`docs/` 只写正式架构、现行契约与运行说明；提案、决策、验证记录和未决项放在
`maintenance/`。原始日志、节点树、截图、坐标与个人信息不入库。
