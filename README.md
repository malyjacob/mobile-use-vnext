# mobile-use-vnext

mobile-use 的全新实现：Go 设备服务（运行于手机内 Debian chroot，linux/arm64）＋ Java 控制
App（`com.mobileuse.vnext`）＋ 固定的 uiautomator 服务制品。目标是把“会话—观察—操作—收尾”
做成单一权威、可恢复、可验证的设备控制链路：服务只监听 `127.0.0.1`，控制 App 只通过私有
HTTP 协议配对。

- 模块：`github.com/malyjacob/mobile-use-vnext`
- Go：1.27.1，启用 `GOEXPERIMENT=jsonv2`
- 契约：`contract_version = "23"`，服务、HTTP 客户端、控制 App 精确相等才做普通工作
- 发行：`0.27.0`；`release/versions.json` 是发行号、契约号与 Android versionCode（29）的唯一
  来源，构建生成 Go/Java 常量

## 能力边界

- Agent 工具为 `POST /api/<tool>`，共 13 个：`start`/`status`/`observe`/`snap`/`wait_for`/
  `act`/`inspect`/`keepalive`/`end`/`app`/`notify`/`shell`/`diagnose`；`app` 以一个工具承载
  `find`/`list`/`open`/`claim`/`handoff`/`close`/`kill` 七个互斥 kind。逐项字段、错误码与
  空值规则见 [接口契约](docs/interface.md)。
- `observe`/`act`/`wait_for` 的 `image=true` 返回与本次观察同次采集的一张 JPEG 图片；图片
  元数据在 `observation.image`，HTTP 载体为 `observation.image.base64`。`snap` 单独读取当前屏
  的一张图片，成功只含 `operation_id` 与顶层 `image`，不读树、不发布观察。
- 默认观察树（`view=compact`）按固定策略交付坐标：根与 `actions` 非空的节点保留已知矩形；
  无动作的 `text` 与匿名普通 `view`/`group` 省略矩形；其余展示节点的矩形与父链上最近交付的
  矩形完全相同、或裁剪到目标屏幕后小于屏幕面积 1% 时省略。普通树还会省略符合条件的小型
  无标注图片，清理因此变空的匿名容器，并提升隐藏匿名容器的唯一孩子。`inspect` 的
  `detailed` 视图交付保留快照的全部节点与已知原坐标。
- 控制端为 `POST /v1/control/sync|stop`；同步下发控制视图、至多一条显示命令与未确认的 Agent
  消息，独立 STOP 只绑定停止边界，见 [控制端协议](docs/control-protocol.md)。
- `notify` 保存的消息通过控制同步进入 App 消息中心与系统通知；`notification.state=pending`
  只表示已保存入队，已读仅存在 App 本地，不回报 Agent。
- MCP 双入口（stdio / Streamable HTTP）由独立 `cmd/device-mcp` 提供，Go 客户端在
  `internal/client`；默认部署会把 linux/arm64 适配器安装到 chroot 并在手机
  `127.0.0.1:18882` 启动 HTTP 入口（主机转发 `18883`），主机自建适配器仍可选。见
  [MCP 入口](docs/mcp.md)、[Agent 接入指南](docs/integration.md) 与 [Go 客户端](docs/client.md)。
  `text`/`both` 的结果文本是紧凑的 `result v4` 外壳：头行给 `ok`/`err` 与调用编号，观察合
  并为单行 `obs`，可读块沿用 `tree v4`、`inspect v3`、`shell v1` 与 `timeline v1`
  语法；`recovery` 行在可判定时直接给出精确 `O`/`P` 编号。`text` 按清单省略固定元信息，
  `structured` 的 `structuredContent` 保留完整 `Result` JSON。

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
internal/mcp/           13 工具注册/schema/结果投影、取消包装、HTTP 认证与 Origin
internal/store/         私有恢复账本与 Agent 消息文件（原子文件、单写者）
internal/eventlog/      有界事件/字段白名单日志
android-control/        Java 控制 App 源码与构建入口（含 test-target 验证探针）
third_party/uiautomator/固定 JAR 输入/补丁/哈希/来源/许可证
release/versions.json   发行号、共同契约号与 Android versionCode 的唯一来源
scripts/                构建（含主机工具）、整组发布、部署与手机内 service.sh
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
./scripts/build.ps1                              # 输出 build/device-service 与 build/device-mcp
./scripts/build-release.ps1                      # 整组发布：Go 产物、APK、固定制品与 manifest.json
./scripts/build-release.ps1 -SkipAndroid         # 仅 Go 与固定制品；清单标记 complete=false，不可部署
./scripts/build-host-tools.ps1                   # Windows device-mcp.exe/mcp-probe.exe + host-tools-manifest.json
./scripts/build-host-tools.ps1 -VerifyOnly       # 发布门禁：清单、干净树/HEAD 与完整手机配套清单
./scripts/build-host-tools.ps1 -AllowDirtySource # 仅开发：脏树构建记 source_dirty=true，不能通过 -VerifyOnly
```

`release/versions.json` 是唯一版本源；`scripts/generate-version.ps1` 生成 Go/Java/XML 常量，
`-Check` 只校验。`build/release/manifest.json` 记录源码提交、Go/MCP 版本、各产物 SHA-256 与
大小、Android 版本与工具目录指纹；`scripts/verify-manifest.ps1` 复核清单、产物哈希与工具目录
指纹，并要求干净已提交树（清单 `source_commit` 是 `HEAD` 的前缀、`source_dirty=false`）与
完整手机产物；开发树不能通过。

MCP 适配进程若运行在 Windows 宿主上（stdio/HTTP 验证），用宿主工具入口统一构建与核验：

```powershell
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

产物为 `build/device-mcp.exe` 与 `build/mcp-probe.exe`；`build/host-tools-manifest.json` 记录
发行/契约、源码身份、工具目录 count/sha256 与两个 exe 的大小/SHA-256。

控制 App 由 `android-control/build.ps1` 用本机 SDK/JDK 直接构建（无 Gradle），输出
`android-control/build/control.apk`。验证探针 App 由 `scripts/build-probe.ps1` 构建，仅用于
实机验证，不进入发布包。

## 部署

`scripts/deploy.ps1` 面向当前单一机型/单一 chroot 的受控部署，要求已有一台在线 adb 设备；
默认先构建整组发布并通过 `verify-manifest.ps1` 核验，然后：读取或生成令牌 → 写私有配置 →
停止本仓库的控制 App、设备服务与手机 MCP 适配器（不操作其他项目的服务、脚本与包）→
推送并安装到 chroot → 覆盖安装并启动控制 App（保留 App 私有数据）→ 安装并启动手机 MCP
适配器 → 建立 `adb forward` `18881→18880` 与
`18883→18882` → 轮询 `/health` 与 `/api/status`，直到 `can_start=true`。

```powershell
./scripts/deploy.ps1                       # 完整构建、整组核验并部署
./scripts/deploy.ps1 -SkipBuild            # 复用 build/release 中已核验的产物
./scripts/deploy.ps1 -AppPort 18880 -HostPort 18881 -AgentName local-agent
```

默认目标（与脚本参数一致）：

| 项 | 值 |
| --- | --- |
| chroot | `/data/local/chroots/debian13` |
| 服务目录 | `/opt/mobile-use-vnext`（`device-service`、`device-mcp`、`mcp-config.json`、`u2.jar`、`applist.dex`、`service.sh`、`config.json`） |
| 状态目录 | `/var/lib/mobile-use-vnext`（恢复账本、消息文件、服务/MCP 的 pid 与日志；目录 0700） |
| 服务端口 | `18880`（仅 `127.0.0.1`） |
| 手机 MCP 端口 | `18882`（仅 `127.0.0.1`，默认安装并启动） |
| adb 串口 | `127.0.0.1:5555` |
| 主机验证入口 | `adb forward tcp:18881 tcp:18880` 后的 `http://127.0.0.1:18881` |
| 主机 MCP 入口 | `adb forward tcp:18883 tcp:18882` 后的 `http://127.0.0.1:18883/mcp` |

**私有配置不入库。** 部署在仓库根写入 `.state/`（已由 `.gitignore` 排除，脚本另设 0600）：

- `.state/deploy.json`：`agent_token`、`control_token`、`mcp_token` 与端口等部署记录；
- `.state/config.json`：服务私有配置（含服务令牌）；
- `.state/phone-mcp-config.json`：手机 MCP 适配器私有配置（含 MCP token）；
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
`internal/android`（ADB/u2 走本地桩，不需要真机）、`internal/client`、`internal/httpapi`、
`internal/mcp`、`internal/store`、`internal/device` 与 `scripts/mcp-probe`；`internal/eventlog`
没有包内测试。Windows 上的通过不证明设备路径或账本耐久性。

## 接口

Agent 工具（`POST /api/<tool>`，`Authorization: Bearer <agent_token>`，
`X-Mobile-Use-Contract: 23`）：

| 工具 | 说明 |
| --- | --- |
| `start` | 请求体必须为 `{}`；创建/绑定目标屏幕并发布首个 full 观察；初始化、激活与首观察成功发布使用五分钟租期 |
| `status` | 无参数、`{"reference":"O…"}` 或 `{"reference":"P…"}`；只读查询 |
| `observe` | `reference`（O/P）；按服务端基线返回 full 或 delta；`observe(P)` 与 `full=true` 强制 full；`image=true` 附加本次图片 |
| `snap` | `reference`（O/P）；立即读取当前屏的一张图，不读树、不发布观察；成功只含 `operation_id` 与顶层 `image` |
| `wait_for` | `reference` + `timeout`（必填 1～40，`stable=true` 时至少 2）+ 可选 `query`/`absent_query`/`stable`/`full`/`image`；在唯一工作位上等待后发布一个新 `O` |
| `act` | `reference` + `operation`：`tap`、`long_press`、`swipe`（可选 `duration_ms`）、`scroll`（`container`/`direction`/`amount`）、`key`、`text`；`tap`/`long_press` 的 ref 目标支持 4 级定位（含 3×3 网格）与同视口子容器委托，默认 `bubble=true` 时按视觉区锚定提升最近交互祖先或吸附唯一局部共位控件；`scroll` 支持双向同视口滚动栈、非滚动 ref 自动祖先提升、坐标最内层/回退/消歧与居中 3 轴线轨迹；`act` 目标解析与规划类静态 422 使用 `invalid_target`/`unsupported_node_action`/`ambiguous_point`/`no_safe_point`/`no_safe_trajectory`，有候选时附统一 `candidates`；`text` 默认 `press_enter=true`，一次完成聚焦、写入、精确读回和至多一次 ENTER；`image=true` 为后观察附图 |
| `inspect` | `reference` 可选 `node` 或 `query`；只读历史树 |
| `keepalive` | `reference`（O/P）+ 可选 `duration_minutes`（1～120，默认 2）；从应用时重设当前活动会话租期，可延长或缩短；普通活动仅按两分钟取最大值 |
| `end` | `reference`（O/P）；撤权并启动独立收尾 |
| `app` | `kind` + 分支成员：`find`/`list` 查询应用与 live task；`open` 用 `target.app`，`claim`/`handoff`/`close`/`kill` 管理会话窗口与精确进程；引用字段为 `reference` |
| `notify` | `text`；保存一条 Agent 消息，经控制同步投递到 App 消息中心 |
| `shell` | `reference` + `command` + 可选 `root`；受管命令执行（不发布观察，原 O 仍可用） |
| `diagnose` | 可选 `reference`；有界诊断事实 |

控制端（`POST /v1/control/sync|stop`，`Authorization: Bearer <control_token>`）：心跳同步
下发控制视图、未确认消息与至多一条显示命令；独立 STOP 只绑定停止边界、不等待清理。
`GET /health` 无认证，只表示进程可应答。

严格字段、错误码与空值规则见 [接口契约](docs/interface.md)；控制端协议细节见
[控制端协议](docs/control-protocol.md)；整组发布与回退见 [发布与回退](docs/release.md)。

## 边界

- Agent 已读、消息历史补取、优先级或定时投递不提供；消息按有界队列保存与投递。
- 历史图片补取、图片差异、裁剪/质量参数与第二套观察配置不提供。
- 发布清单与工具目录指纹证明构建内容，不证明 MCP 宿主已重新读取工具；宿主必须实际重启。

## 文档

- [架构](docs/architecture.md)：进程分层、唯一权威、O/P 生命周期、full/delta、图片附加材料、STOP 与收尾、恢复屏障、账本、消息与发布边界。
- [接口契约](docs/interface.md)：逐端点 JSON、空值规则、错误码、图片与工具支持范围。
- [Agent 接入指南](docs/integration.md)：主机/手机内宿主、HTTP 与 MCP、凭据、启动顺序与错误恢复。
- [运行手册](docs/runbook.md)：手机环境、部署/启动/停止、健康检查、消息中心、MCP 入口与常见故障。
- [手机侧运维](docs/phone-operations.md)：进程/文件/容量、重启、权限、凭据轮换、备份与手机侧故障处置。
- [控制端协议](docs/control-protocol.md)：同步、消息投递、显示命令、独立 STOP。
- [发布与回退](docs/release.md)：单一版本源、整组产物与清单、受控部署顺序、回退边界。
- [MCP 入口](docs/mcp.md)：双传输、工具 schema、结果投影、取消与漏收边界。
- [Go 客户端](docs/client.md)：P、结构化错误、发现与图片字节。
- [工程决策](maintenance/decisions.md) 与 `maintenance/verification/`。

`docs/` 只写正式架构、现行契约与运行说明；提案、决策、验证记录和未决项放在
`maintenance/`。原始日志、节点树、截图、坐标与个人信息不入库。
