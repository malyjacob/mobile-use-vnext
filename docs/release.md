# 发布与回退

本页定义整组发布单位、单一版本源、构建清单、受控部署顺序与回退边界。控制端协议见
[控制端协议](control-protocol.md)，日常操作命令见[运行手册](runbook.md)。

## 1. 发布单位

一次发行是一组配套产物，不是单个二进制：

| 产物 | 说明 |
| --- | --- |
| `device-service` | 设备服务（linux/arm64，`CGO_ENABLED=0`） |
| `device-mcp` | MCP 适配进程（stdio / Streamable HTTP） |
| `control.apk` | 控制 App，包名 `com.mobileuse.vnext` |
| `u2.jar` | 固定 uiautomator 制品 |
| `applist.dex` | 固定应用清单制品 |
| `service.sh` | chroot 内进程管理脚本 |

服务、App、Go 客户端与 MCP 共用一个 `contract_version`，必须精确相等才进行普通工作；
不一致时普通同步按 `409 contract_mismatch` 拒绝，只有最小 STOP 仍可用。`release_version`
只标识发行产物，不参与能力协商。Go 客户端源码随同一源码提交发布，不作为独立产物写入
清单；MCP 适配器以 `device-mcp` 产物发布。

## 2. 单一版本源

`release/versions.json` 是以下内容的唯一手工来源：

| 字段 | 用途 |
| --- | --- |
| `release_version` | 发行号（MAJOR.MINOR.PATCH），写入 Go `ReleaseVersion`、App versionName 与清单 |
| `contract_version` | 共同普通契约号，写入 Go/Java 常量与清单 |
| `android_version_code` | Android versionCode，写入 `AndroidManifest.xml` 与清单 |
| `source_commit` | 该版本号冻结时的基线提交（仅供校验）；清单记录构建时的 HEAD |

`scripts/generate-version.ps1` 生成：

- `internal/protocol/version_generated.go`（`ContractVersion`、`ReleaseVersion`）；
- `android-control/app/src/main/java/com/mobileuse/vnext/GeneratedVersion.java`；
- `android-control/app/src/main/res/values/strings_version.xml`（`app_version`）；
- 同步 `AndroidManifest.xml` 的 `android:versionCode`。

生成文件随源码提交，构建入口在编译前重新生成或使用 `-Check` 校验。不得在 Go、Java、
PowerShell、docs 或 Manifest 中另写一份版本号；`contract_version` 只在改变普通请求/响应
必需形状、默认行为、核心枚举或执行语义时前进。

## 3. 构建与清单

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
```

`build-release.ps1`：

1. 校验版本源与生成常量一致；
2. 构建 linux/arm64、`CGO_ENABLED=0` 的 `device-service` 与 `device-mcp`，并构建 APK；
3. 收集固定制品与 `service.sh`；产物复制到 `build/release/`；
4. 计算每个产物的 SHA-256 与字节数；
5. 记录源码提交与脏标记、Go 版本、MCP SDK/协议、Android 包名/版本、工具目录指纹；
6. 写入 `build/release/manifest.json`。

清单形状：

```json
{
  "format": "mobile-use-vnext-release",
  "version": 1,
  "release_version": "0.8.0",
  "contract_version": 8,
  "source_commit": "<40 位提交>",
  "source_dirty": false,
  "built_at": "…",
  "go_version": "go1.27.1",
  "go_experiment": "jsonv2",
  "build_target": {"goos": "linux", "goarch": "arm64", "cgo_enabled": false},
  "mcp": {"sdk": "github.com/modelcontextprotocol/go-sdk v1.8.0", "protocol": "2026-07-28"},
  "android": {"package": "com.mobileuse.vnext", "version_name": "0.8.0", "version_code": 10},
  "tool_catalog": {"count": 12, "sha256": "…"},
  "artifacts": [{"name": "device-service", "path": "device-service", "sha256": "…", "bytes": 0}]
}
```

`-SkipAndroid` 只用于开发验证，产出的清单带 `complete=false` 且缺少 `control.apk`；
`verify-manifest.ps1` 与 `deploy.ps1` 都拒绝这种清单。

清单不包含 `.state/`、令牌、账本、消息、截图、节点树或日志。工具目录指纹用于运维比对本次
构建的 `tools/list`，不证明任何宿主已读取它。

## 4. 部署顺序

1. 准备并核验整组构建、清单、契约号、Android 版本与固定制品哈希。
2. 暂停新的普通工作；用旧版本结束活动会话或 STOP，等待最多 5 秒的旧逻辑交接，并记录
   执行链、task/recents 与 display 的 `pending`/`complete`/`unconfirmed`。旧物理事实可以在
   安装窗口继续保留；不强停 App 把缺少描述当作已清理，也不以未确认单独阻断新版本启动。
3. 尝试停止旧服务与 App（停止失败不阻断安装）；安装整组产物。chroot 内的文件产物
   与 `connection.json` 先写临时文件再原子替换，APK 用 `adb install -r` 覆盖安装；部署脚本
   复核已部署文件：chroot 内清单产物对清单 SHA-256，生成的配置与 `connection.json` 对宿主
   生成值；APK 以安装后的 versionName/versionCode 核验。不一致就停止。部署脚本不单独断言
   旧进程已停止或准入已关闭；手机 MCP 适配器若在停止窗口后仍存活，脚本告警。操作者应在
   部署窗口保持不可准入。
4. 启动新服务、App 与手机侧 MCP 适配器：核验 release/contract 与 `can_start`（其成立覆盖
   当前 App/模式/STOP、设备与账本可写性），以及安装后的 App versionName/versionCode；
   同时核验唯一 HTTP MCP 适配器进程的 `/proc/<pid>/exe` SHA-256 与清单一致。旧 session
   转退休记录，旧 cleanup 可仍为 `pending`/`unconfirmed` 并与原 session 同帧展示，不单独
   使 `can_start=false`。脚本不读取实例身份或显示模式，二者由控制端与运行手册另行确认。
   宿主托管的 stdio 适配器若仍持有旧二进制，部署脚本明确告警，必须重启宿主 MCP 进程。
   默认虚拟屏，主屏共享需要用户重新确认。
5. 重启实际 MCP 宿主进程/连接，使宿主重新发现工具目录；不能只替换磁盘文件或依赖
   `list_changed` 通知。手机适配器默认随部署安装并启动，主机自建适配器可选。
6. 条件成立才报告完成；分别记录版本匹配、App 配对、当前恢复入口、保留的退休 cleanup 事实、
   STOP 可用、MCP 宿主刷新与本次最小闭环结果，不合并成一个 success。

App 覆盖安装保留私有消息、已读、连接配置与停止意图。不得卸载 App、`pm clear`、删除消息
文件或清空账本来解决签名、契约或配对问题；安装失败时保留现场并停止发布。部署过程不触发
手机重启；设备服务按脚本重启，覆盖安装后的控制 App 服务由部署启动。

## 5. 失败与回退

- 任何中间失败都保持普通准入关闭，记录已完成阶段与失败阶段，不继续叠加升级。
- 服务进程启动但当前 App 不配对、契约不一致、当前账本不可写、STOP/设备/交互能力未就绪，
  都不算发布完成。旧 retirement cleanup 未确认只作为保留事实，不单独使发布失败。
- 消息在服务重启后按原 ID 继续投递；升级不要求清空 App 消息或已读。
- 回退是另一轮受控部署：安装上一组已核验产物，重新确认旧资源、契约配对与准入；不自动
  回放旧操作、不恢复主屏授权、不清空消息或归属来凑兼容。
- 旧二进制无法读取版本 4 账本时明确不能直接回退；回退必须使用支持版本 4 的兼容产物。
  未确认归属作为退休事实保留，不能删除后假装干净，也不要求旧物理资源全部确认后才允许新
  当前归属保存。
