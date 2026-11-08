# mobile-use-vnext

mobile-use 的全新实现：Go 设备服务（运行于手机内 Debian chroot，linux/arm64）＋ Java 控制
App（`com.mobileuse.vnext`）＋ 固定的 uiautomator 服务制品。目标是把“会话—观察—操作—收尾”
做成单一权威、可恢复、可验证的设备控制链路：服务只监听 `127.0.0.1`，控制 App 只通过私有
HTTP 协议配对。

- 模块：`github.com/malyjacob/mobile-use-vnext`
- Go：1.27.1，启用 `GOEXPERIMENT=jsonv2`
- 契约：`contract_version = "1"`，服务、HTTP 客户端、控制 App 精确相等才做普通工作
- 发布标识：`0.1.0-p3`（`-ldflags -X internal/protocol.ReleaseVersion`）
- 当前阶段：**P3 最小可写闭环**

## 当前阶段

P3 已实现：配对控制 App 的最小控制协议与独立 STOP、启动恢复账本与 `can_start` 门禁、
`start`（首个 full 观察）/`status`/`observe`/`act`（`tap`、`long_press`、`swipe`、`key`、
`text`）/`end`、精确归属与物理收尾。设备服务与 App 的共同契约版本为 `1`；控制 App 的
`versionName` 为 `0.4.0-p3`。

事实分层（以 2026-11-08 的工作区为准）：

| 类别 | 内容 |
| --- | --- |
| 已实现 | 上述工具与 `/v1/control/sync`、`/v1/control/stop`；`create_display`/`close_display` 两种显示命令；进程锁、恢复账本、收尾链、事件日志白名单 |
| 已实测 | 见 [P3 实机记录](maintenance/verification/p3-real-device.md)：`scripts/verify-p3.ps1` 连续四次 PASS（虚拟屏 start/observe/tap/中文 text/end、清理与重新准入）、服务 SIGKILL 后旧显示仍存在的精确关闭恢复、App `force-stop` 后的撤销与收尾、并发认领、`observe.full=true`、严格接入与 `image=true` → 501 |
| 未验证 | `primary` 模式、App 通知按钮实点、HTTP 层独立回归、完整 G3 |
| 未实现 | MCP 双入口与客户端、图片交付（`image=true` 返回 501）、`open_app`/`apps`/`keepalive`/`inspect` 等其余工具、消息中心 |

G3 尚未整体收口：实现已到“最小可写闭环”，`display` 仍存在的恢复分支已实测，但 `primary`
模式、App 通知按钮实点与 HTTP 独立回归还没有实机证据，不能把 P3 之外的阶段能力写成已具备。

## 目录

```text
cmd/device-service/     设备服务进程组装、进程锁、启动与退出
internal/protocol/      公共数据、严格 JSON 接入、错误码与可读排版
internal/device/        原始设备事实类型（不执行 I/O、不持运行状态）
internal/observation/   公开树构造、保守保号、full/delta 计算与还原（纯算法）
internal/engine/        唯一业务入口：会话、O/P、工作位置、STOP、收尾、恢复
internal/android/       ADB、u2 RPC、制品管理与受管执行链
internal/httpapi/       认证 HTTP 接入、/api/<tool>、控制端同步与最小 STOP
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
./scripts/build.ps1                              # 输出 build/device-service，默认 0.1.0-p3
./scripts/build.ps1 -Version 0.1.0-p3 -Output build
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

当前包内测试覆盖 `internal/protocol`、`internal/observation`、`internal/engine`、
`internal/android`（ADB/u2 走本地桩，不需要真机）。`internal/httpapi`、`internal/store`、
`internal/eventlog`、`internal/device` 没有包内测试；Windows 上的通过不证明设备路径或账本
耐久性。设备相关验证边界见 [P3 实机记录](maintenance/verification/p3-real-device.md)。

服务已部署、`can_start=true` 时，可运行实机闭环脚本（探针 APK 缺失会自动构建）：

```powershell
./scripts/verify-p3.ps1 -SkipBuildProbe
```

## 已实现接口

Agent 工具（`POST /api/<tool>`，`Authorization: Bearer <agent_token>`，
`X-Mobile-Use-Contract: 1`）：

| 工具 | 说明 |
| --- | --- |
| `start` | 请求体必须为 `{}`；创建/绑定目标屏幕并发布首个 full 观察，返回 `session` 与 `observation` |
| `status` | 无参数、`{"reference":"O…"}` 或 `{"reference":"P…"}`；查询，不续租、不确认基线 |
| `observe` | `reference` 必填（O/P），`wait_ms` 0–10000；按服务端基线返回 full 或 delta；`observe(P)` 与 `full=true` 强制 full |
| `act` | `observation_id` + `operation`：`tap`、`long_press`、`swipe`、`key`、`text`；一次性认领 O，动作后附后观察 |
| `end` | `reference`（O/P）；撤权并启动独立收尾，返回 `cleanup` 进度 |

控制端（`POST /v1/control/sync|stop`，`Authorization: Bearer <control_token>`）：心跳同步
下发普通控制视图与至多一条显示命令，独立 STOP 只绑定停止边界、不等待清理。显示命令固定为
`create_display`、`close_display` 两种。`GET /health` 无认证，只表示进程可应答。

严格字段、错误码与空值规则见 [接口契约](docs/interface.md)；控制端协议细节见
[控制端最小协议](docs/control-protocol.md)。

## 未实现

- MCP 双入口（stdio / Streamable HTTP）与 Go client：无 `internal/mcp`、无 SDK 配置。
- 图片/截图交付：`observe`、`act` 的 `image=true` 直接返回 `501 unsupported_feature`；
  设备层没有图片块或 MCP ImageContent 输出。
- `open_app`、`apps`、`keepalive`、`inspect`、`notify`、`shell`、`diagnose` 等其余工具：
  路由未注册。
- 完整 control_view 呈现（P6）、消息/回执/已读、App 界面整理。
- go-sdk 取消语义与 stdio 配置 spike：未开展（见
  [jsonv2 spike](maintenance/spikes/p0-jsonv2.md) 与 [账本 spike](maintenance/spikes/p0-ledger.md)）。

## 文档

- [架构](docs/architecture.md)：进程分层、唯一权威、O/P 生命周期、full/delta、STOP 与收尾、恢复屏障、账本。
- [接口契约](docs/interface.md)：逐端点 JSON、空值规则、错误码、工具支持范围。
- [运行手册](docs/runbook.md)：手机环境、部署/启动/停止、健康检查、常见故障。
- [控制端最小协议](docs/control-protocol.md)：同步、显示命令、独立 STOP。
- [工程决策](maintenance/decisions.md) 与 [P3 实机记录](maintenance/verification/p3-real-device.md)。

`docs/` 只写已实现架构、现行契约与运行说明；提案、决策、验证记录和未决项放在
`maintenance/`。原始日志、节点树、截图、坐标与个人信息不入库。
