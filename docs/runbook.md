# 运行手册

面向已有一台 root 手机、已存在 Debian 13 chroot 的受控环境。所有路径与端口来自
`scripts/deploy.ps1`、`scripts/service.sh` 与服务私有配置的默认值；换机型或 chroot 前先核对。
本页覆盖环境、部署、启动停止、健康检查与故障处置。架构与契约分别见
[架构](architecture.md)、[接口契约](interface.md)；Agent 宿主接入见
[Agent 接入指南](integration.md)；日常运维与容量/凭据见[手机侧运维](phone-operations.md)。

## 1. 环境

| 项 | 值 |
| --- | --- |
| 设备 | Xiaomi `24117RK2CC`，Android 16（API 36），arm64 |
| 特权 | Magisk root；Debian 13 chroot 需预先准备 |
| chroot | `/data/local/chroots/debian13` |
| 服务目录（chroot 内） | `/opt/mobile-use-vnext`：`device-service`、`device-mcp`、`config.json`、`mcp-config.json`、`u2.jar`、`applist.dex`、`service.sh` |
| 状态目录（chroot 内） | `/var/lib/mobile-use-vnext`：`recovery-ledger.json`、`messages.json`、`device-service.pid/.log`、`device-mcp.pid/.log`（0700） |
| 服务端口 | `18880`，只监听 `127.0.0.1` |
| 手机 MCP 端口 | `18882`，只监听 `127.0.0.1`（默认部署安装并启动 `device-mcp`） |
| adb 串口 | `127.0.0.1:5555` |
| 主机转发端口 | `18881` → `18880`（直连 HTTP）；`18883` → `18882`（手机 MCP） |
| 控制 App | `com.mobileuse.vnext`，固定连接 `http://127.0.0.1:18880` |
| 固定制品 | `third_party/uiautomator/u2.jar`、`third_party/android-applist/applist.dex`（SHA-256 固定，替换前必须核对） |
| 受管执行临时资源 | `/data/local/tmp/mobile-use-shell-runner.sh`（固定脚本）与 `/data/local/tmp/mobile-use-shell/<run-id>/`（单次运行目录，确认结束后移除） |

服务进程在 chroot 内运行，ADB/u2 通过 Android 侧 `adb`/`app_process` 工作；控制 App 运行在
Android 侧，只与服务通信。Windows 主机只负责构建与下发，不参与设备服务运行时状态。默认部署
同时安装并启动手机 MCP 适配器（`device-mcp`，手机回环 `18882`），主机可经 `18883` 使用，
也可另行运行主机侧适配器；两条路径都只通过服务的 loopback HTTP 入口工作。

受管 shell 的命令文件、start/done 元数据与有界输出位于单次运行目录；命令正文与输出不写
服务日志、不写恢复账本。运行目录残留或进程身份无法核验时，保持恢复屏障并人工核验，
不得按包名/PID 猜测清理。

## 2. 私有文件

| 文件 | 位置 | 内容 |
| --- | --- | --- |
| `deploy.json` | 仓库 `.state/`（部署机） | agent/control/mcp 令牌、端口、chroot、版本 |
| `config.json` | `.state/` 生成，推送到 `/opt/mobile-use-vnext/`（0600） | 监听地址、状态目录、ADB 串口、服务令牌、u2 路径 |
| `mcp-config.json`（手机） | `.state/phone-mcp-config.json` 生成并推送到 `/opt/mobile-use-vnext/`（0600） | 手机 MCP 适配器的 `base_url`、agent 令牌、监听与 MCP token |
| `connection.json` | 推送到 `/data/data/com.mobileuse.vnext/files/`（0600，App UID 所有） | `base_url` + control 令牌 |
| `messages.json`（服务） | chroot `/var/lib/mobile-use-vnext/`（0600） | 未确认的 Agent 消息；有界，正文不进入账本/日志 |
| `messages.json`（App） | `/data/data/com.mobileuse.vnext/files/`（0600） | App 消息中心列表、本地已读与去重 ID |

`.state/` 已在 `.gitignore` 中排除；不要复制到仓库、工单、聊天或日志。令牌泄漏时重新部署会
复用旧令牌——需要轮换时删除对应私有文件后再部署。服务消息文件与 App 消息文件都只由各自
进程写入；不要手工编辑，也不要用卸载/清库方式“修复”消息或配对。

## 3. 部署与升级

前置：`adb devices` 能看到 `127.0.0.1:5555` 且状态为 `device`；本机有 Go 1.27.1、Android
SDK/JDK（构建 App 用）；chroot 已存在且可 `su` 进入。

```powershell
./scripts/build-release.ps1                   # 整组产物 + build/release/manifest.json
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/deploy.ps1                          # 构建、核验并按清单部署
./scripts/deploy.ps1 -SkipBuild               # 复用已核验的 build/release
```

`build-release.ps1` 的实际动作：

1. 校验 `release/versions.json` 与生成常量一致（`generate-version.ps1 -Check`）；
2. 构建 linux/arm64、`CGO_ENABLED=0` 的 `device-service` 与 `device-mcp`；
3. 构建 `control.apk`；`-SkipAndroid` 只用于开发，清单标记 `complete=false` 且不可部署；
4. 收集 `u2.jar`、`applist.dex`、`service.sh`，计算每个产物的 SHA-256 与大小；
5. 记录源码提交/脏标记、Go 版本、MCP SDK `v1.8.0`/协议 `2026-07-28`、Android
   包名/versionName/versionCode 与工具目录指纹，写入 `build/release/manifest.json`。

`deploy.ps1` 的实际动作：

1. 未 `-SkipBuild` 时先构建整组发布，然后始终运行 `verify-manifest.ps1`；
2. 读取或生成 `.state/deploy.json`，写 `.state/config.json`、`.state/phone-mcp-config.json`
   与 `.state/connection.json`；
3. 尽力停止旧 Python 服务与现有 Go 服务、旧手机 MCP 适配器，再停止控制 App；
4. 推送 manifest 指向的产物到 `/data/local/tmp/mobile-use-vnext-deploy`，`su` 复制进 chroot；
5. 用 `adb install -r` 覆盖安装 APK（**不卸载、不清库**），重写 `connection.json` 并授予
   通知权限与悬浮窗 appops；
6. 用 `service.sh start` 启动服务，再启动手机 MCP 适配器并确认 `/mcp` 无 token 返回 `401`；
7. 启动 `com.mobileuse.vnext/.ControlService`（MIUI 新装包可能被挂起，脚本会 `pm unsuspend`
   并重试）；
8. 建立 `adb forward tcp:18881 tcp:18880` 与 `tcp:18883 tcp:18882`，轮询 `/health` 与
   `/api/status`，要求 `/health` 的 release/contract 与清单一致且 `can_start=true`；90 秒
   未就绪则失败并打印最后一次 `status`。安装后的 App versionName/versionCode 必须与清单
   一致。默认部署安装并启动手机 MCP 适配器；`-SkipPhoneMcp` 只用于显式跳过（此时部署不
   完整，手机内 MCP 不可用）。

发布完成需要分别确认：版本匹配、App 配对（`can_start=true` 蕴含新鲜配对同步）、恢复屏障、
STOP 可用与 MCP 宿主已重新发现工具。手机适配器的安装/监听/`401` 由部署验证；MCP 客户端
仍须重启实际宿主进程/连接并重新 `tools/list`，不能只替换磁盘文件。

部署不会创建 chroot、不会安装 Magisk、不会修改系统 IME 设置。升级服务或 App 后必须让新
进程重新启动，旧进程会继续使用旧二进制与旧配置。

## 4. 启动、停止、状态

`service.sh` 在 chroot 内运行，只管理 pid 文件、日志轮转与 `/health` 就绪；会话与资源权威
在服务进程内，脚本停止不等于资源已释放。

```bash
# 以下命令在主机执行，由 adb + su 送进 chroot
adb -s 127.0.0.1:5555 shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh start"'
adb -s 127.0.0.1:5555 shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh status"'
adb -s 127.0.0.1:5555 shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh stop"'
```

- `start`：已有匹配进程则直接返回；否则轮转日志、`nohup` 启动、最多约 15 秒等
  `http://127.0.0.1:18880/health`；失败退出 1。
- `stop`：先 `SIGTERM`，最多 10 秒，再 `SIGKILL`；被强杀时退出 1。
- 显示助手由服务以 `nohup setsid` 脱离后启动，服务崩溃时它和已创建的虚拟屏可以继续存活；
  恢复流程会通过配对 App 精确 `close_display`。不要手工再起第二个 helper，也不要靠杀
  `app_process` 绕过恢复门禁。
- `status`：进程匹配且健康才退出 0。
- 进程退出码：`2` 配置被拒、`3` 进程锁被占用、`4` 账本目录不可用、`5` ADB/Android 配置
  被拒。端口监听失败、设备未就绪等会在日志里留下事件。

手机 MCP 适配器不由 `service.sh` 托管；重部署会停止旧进程、覆盖二进制/配置并启动新进程：

```bash
# 启动 / 查看 / 停止
adb shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "nohup setsid /opt/mobile-use-vnext/device-mcp -transport http -config /opt/mobile-use-vnext/mcp-config.json >> /var/lib/mobile-use-vnext/device-mcp.log 2>&1 < /dev/null &"'
adb shell 'su -c "pgrep -f \"^/opt/mobile-use-vnext/device-mcp \""'
adb shell su -c 'tail -n 50 /data/local/chroots/debian13/var/lib/mobile-use-vnext/device-mcp.log'
adb shell su -c 'pkill -f "^/opt/mobile-use-vnext/device-mcp " || true'
```

控制 App 侧：安装后打开一次 `MainActivity`，或由部署直接启动
`com.mobileuse.vnext/.ControlService`（action `com.mobileuse.vnext.START`）。App 每秒同步，
连续 6 秒无成功响应即判失鲜；失鲜期间 `start` 会以 `control_unavailable` 被拒
（`can_start=false`），同步恢复后自动解除。`availability` 是 App 上报事实的投影，
失鲜不一定马上把它改成 `blocked`；准入判断以 `can_start` 与 `blocking_code` 为准。
同一份快照驱动完整界面、常驻通知与悬浮入口；`notify` 消息另走独立消息通知频道，打开 App
的消息详情只在本机标记已读。

## 5. 健康检查与就绪

```bash
adb -s 127.0.0.1:5555 forward tcp:18881 tcp:18880
curl -s http://127.0.0.1:18881/health
```

`/health` 无认证，返回 `status`、`contract_version`、`service_instance_id`、
`release_version`。它只证明进程能应答，**不是** `can_start`。

就绪以 `POST /api/status` 为准（令牌从 `.state/deploy.json` 读取，下面用占位符）：

```bash
curl -s -X POST http://127.0.0.1:18881/api/status \
  -H "Authorization: Bearer $AGENT_TOKEN" \
  -H "X-Mobile-Use-Contract: 2" \
  -H "Content-Type: application/json" -d '{}'
```

- `can_start=true` 才可 `start`；
- `can_start=false` 时读 `blocking_code` 与 `cleanup`/`session`；
- 端口已监听、`/health` 正常、进程存在都不能替代这个门禁。

手机 MCP 监听检查（主机经 `18883`）：

```powershell
curl.exe -s -o NUL -w "%{http_code}" http://127.0.0.1:18883/mcp
# 无 token：401；带 token 用 GET：405（正常，客户端应 POST）
```

## 6. 日志

| 日志 | 位置 | 说明 |
| --- | --- | --- |
| 服务日志 | chroot `/var/lib/mobile-use-vnext/device-service.log`（轮转为 `.log.1`） | 事件名 + 白名单字段；不写树、输入正文、命令输出或异常文本 |
| MCP 适配器日志 | chroot `/var/lib/mobile-use-vnext/device-mcp.log` | 启动/停止/配置错误；stdout/stderr |
| App 诊断 | `/data/data/com.mobileuse.vnext/files/diagnostics.log` | 固定事件/原因码、UUID、数字；0600，单份有界 |

```bash
adb -s 127.0.0.1:5555 shell su -c 'tail -n 100 /data/local/chroots/debian13/var/lib/mobile-use-vnext/device-service.log'
adb -s 127.0.0.1:5555 shell su -c 'tail -n 100 /data/data/com.mobileuse.vnext/files/diagnostics.log'
```

常见事件：`service.start`、`device.ready`、`android.u2.started`、`android.u2.artifact_rejected`、
`android.capture.failed`、`android.text.set_failed`、`cleanup.start`、`cleanup.complete`、
`cleanup.unconfirmed`、`recovery.complete`、`ledger.save_failed`。日志丢失计数只能证明事件
被丢弃，不能反推设备状态。

## 7. 常见故障

| 现象 | 原因 | 处置 |
| --- | --- | --- |
| 刚装完 App 没有心跳，`can_start=false`（`control_sync_pending`/`control_unavailable`） | MIUI 在新包首次组件启动前挂起（suspended），或系统“屏幕用时限制”拦截 Activity（`MST_LimitActivity`） | 关闭系统的屏幕用时限制；`su -c 'pm unsuspend --user 0 com.mobileuse.vnext'`，再 `su -c 'am start-foreground-service -n com.mobileuse.vnext/.ControlService -a com.mobileuse.vnext.START'`；`deploy.ps1` 已自动重试 |
| `start` 报显示不可用/创建未确认 | App 未运行、通知/停止通道不可用、模式未就绪、虚拟屏 helper 未起来 | 先看 `status.blocking_code`；确认 App 通知与悬浮窗授权、`stop_ready`、`mode_ready`；再看日志 `android.display_helper.*` |
| `dumpsys SurfaceFlinger` 的层 id 解析失败或溢出 | 虚拟屏 SFID 可能超出 `int64` 范围 | 服务按 `uint64` 解析并原样传给 `screencap -d`；不要改成 `int64`/`int`，不要回退到 display 0。只读核对用 `dumpsys SurfaceFlinger --display-id` |
| u2 启动失败或采集报 `hierarchy_unavailable` | 旧 `app_process` 仍在跑旧 JAR、JAR 哈希不符、端口未就绪 | 重启服务触发 `EnsureU2`（校验本地哈希 → 停旧 server → 比对/推送远端 → `/ping`）；JAR 替换后必须重启，进程不会热加载。服务只 `pkill` 固定模式 `^app_process / com.wetest.uia2.Main$`，不影响其他进程 |
| 虚拟屏没有子节点，疑似采集失败 | 空虚拟屏是合法画面 | 公开树只有合成根、无 `children`，是正常空树；不要把它当失败，也不要伪造子节点。空树仍可 `observe`/`end` |
| `can_start=false`、`blocking_code=shell_pending`/`shell_chain_unconfirmed` | 受管命令仍在执行或终止未确认 | 等 `shell` 返回或 `status` 的 `shell.termination` 确认；不要 kill 进程或删运行目录 |
| `can_start=false`、`blocking_code=cleanup_unconfirmed` | 上一个会话的显示/任务/执行链未确认释放 | 保持服务运行，用 `status` 看 `cleanup` 五项；确认 App 在线并收到 `close_display`、`dumpsys` 中旧屏已消失；必要时 `end` 原会话等待收尾。不要删账本、不要用重启绕过门禁 |
| `can_start=false`、`blocking_code=cleanup_pending` | 收尾仍在进行 | 等待收尾；此期间同步仍会投递 `close_display`，但 `start` 被阻塞 |
| `can_start=false`、`blocking_code=old_resource_unconfirmed` / `recovery_pending` | 重启后旧归属未核验完 | 等 App 恢复心跳与模式；恢复先要求 App 同步，再核验旧 display、`close_display` 回执/消失与 TaskList；显示仍在但 App 实例不匹配、TaskList 失败或旧 display 仍有任务时保持阻塞，不能强行 `start`。旧显示不存在也不能只凭一次 absent 放行 |
| `can_start=false`、`blocking_code=ledger_corrupt` / `ledger_unavailable` | 账本损坏或不可写 | 检查 `/var/lib/mobile-use-vnext` 权限与磁盘；损坏账本需要明确的人工处置（移走并重新部署），不能带病启动 |
| `can_start=false`、`blocking_code=device_busy` | 已有普通工作（start/observe/act/apps/open_app/shell）在进行 | 等该操作完成或用 `status` 查 `blocking_operation_id`；不建队列，不重试并发动作 |
| `can_start=false`、`blocking_code=device_locked` / `device_not_interactive` | 锁屏或熄屏 | 解锁并点亮屏幕；服务不绕过锁屏 |
| `can_start=false`、`blocking_code=control_revoked` | 收到 STOP 后未完成一次新同步 | 等 App 下一拍同步（不再携带 `stop_pending`）后再 `start` |
| `can_start=false`、`blocking_code=mode_not_selected`/`stop_unavailable` | App 模式/通知/停止通道不可用 | 在 App 选择虚拟屏或确认主屏共享；授予通知权限并确认通知频道可用 |
| `409 contract_mismatch` | `X-Mobile-Use-Contract` 缺失/不是 `2`，或 App 契约版本不符 | 服务与客户端必须同为 `contract_version=2`；升级整组产物，不要只升一侧 |
| `401 unauthorized` | Bearer 令牌错误，或用了另一身份的令牌 | 从私有配置核对 agent/control 令牌；两者不能互换 |
| 主机 `18883/mcp` 不通或 401 | 手机适配器未运行、转发失效、填错 token | 看 `device-mcp.log` 与 `pgrep`；重建 `adb forward tcp:18883 tcp:18882`；手机路径填 `.state/deploy.json` 的 `mcp_token` |
| MCP 客户端 403 | `Origin` 未在 `allowed_origins` | 部署时用 `-McpAllowedOrigins` 精确放行并重启适配器；不要填 `*` |
| 手机 `18882` 启动失败 | 端口被占用或配置/二进制不匹配 | 只保留一个适配器进程；检查 `mcp-config.json` 与日志 |
| `409 device_busy` | 已有普通工作占用工作位置 | 读 `blocking_operation_id`；串行化调用 |
| `409 observation_expired`/`target_changed`/`foreground_changed` | 观察已过期（60 秒）或界面变化 | 重新 `observe`，用新 O 再 `act`；不要重放旧动作 |
| `504 action_result_uncertain` | 输入已派发但结果无法确认 | 用 `status(P)`/重新观察判断现状，不重放；结束会话时该事实会反映到 `cleanup.execution` |
| App 进程重建或服务重启 | 服务实例变化、App 实例变化 | 服务实例变化后 App 会作废旧命令；App 实例变化会按 `app_restarted` 撤销旧会话，服务正常退出按 `service_shutdown` 撤权。等待新的恢复结果，不沿用缓存状态 |

## 8. 正常停止与资源确认

推荐顺序：

1. 结束 Agent 会话：`POST /api/end`（引用 O/P）或由 App 触发 STOP；
2. 轮询 `POST /api/status`：`cleanup.state=complete`，且 `display` 为 `confirmed` /
   `not_applicable`、`tasks`/`recents` 不为 `pending`/`unconfirmed`；
3. 确认 SurfaceFlinger 中不再有本次 `MobileUseAgent` 虚拟屏层；
4. 再执行 `service.sh stop`。

`service.sh stop`、进程崩溃、`/health` 正常都不等于资源已释放。服务退出时会撤权并启动收尾，
但收尾结果只来自证据；`cleanup=unconfirmed` 时必须保留账本与现场，按上一节排查。

## 9. 边界与禁止事项

- 不在 Windows 上运行服务或把 Windows 文件时间/日志当作耐久证据；账本目录 fsync 的目录项
  在 Windows 被跳过。
- 不手工编辑 `recovery-ledger.json` 或服务/App 的 `messages.json`；不在未确认收尾时删除
  状态目录；不以 `kill -9` 或重启 App 当作清理完成。
- 不用卸载 App、`pm clear` 或删除消息文件解决签名、契约或配对问题。
- 不在虚拟屏上显示控制 App 界面；采集发现控制包可见节点会直接失败。
- 不把令牌、节点树、输入正文、截图、原始 `dumpsys`/logcat 输出贴进仓库或工单。
- 服务只提供 [接口契约](interface.md) 列出的端点；图片通过 `observe`/`act` 的 `image=true`
  取得。MCP 由独立 `device-mcp` 进程提供，不并入设备服务的端口。

## 10. MCP 入口

`cmd/device-mcp` 是独立的 MCP 适配进程，可用 stdio 或 Streamable HTTP 运行；它通过
`internal/client` 访问服务的 `/api/<tool>`，不接触设备或账本。工具目录、schema、结果与
取消语义见 [MCP 入口](mcp.md)；客户端 API 见 [Go 客户端](client.md)；完整接入示例见
[Agent 接入指南](integration.md)。

### 10.1 默认：手机适配器

部署会把 linux/arm64 的 `device-mcp` 和手机侧私有 `mcp-config.json` 安装到 chroot，并在
手机 `127.0.0.1:18882` 启动 HTTP 入口：

```text
手机内：  http://127.0.0.1:18882/mcp
主机转发：http://127.0.0.1:18883/mcp
```

主机 MCP 客户端填 URL 与 `Authorization: Bearer <mcp_token>`；`mcp_token` 在
`.state/deploy.json`，同一个值也在 chroot `/opt/mobile-use-vnext/mcp-config.json` 的
`http.token`。它不是 `agent_token`，也不是控制 App 的 `control_token`。

```powershell
adb forward tcp:18883 tcp:18882
$mcpToken = (Get-Content .state/deploy.json -Raw | ConvertFrom-Json).mcp_token
# 客户端：URL=http://127.0.0.1:18883/mcp, Authorization: Bearer $mcpToken
```

无 token 的 `/mcp` 返回 `401` 是正常的监听证明；带 token 使用 `GET` 返回 `405` 也是正常，
MCP 客户端应使用 POST。手机内 Android 宿主直接使用 `127.0.0.1:18882/mcp`，不经过转发。

手机适配器的配置由部署生成，字段为 `service.base_url`（18880）、`service.agent_token`、
`transport=http`、`http.listen`（18882）、`http.token`（即 `mcp_token`）与
`http.allowed_origins`。需要放行带 `Origin` 的客户端时，部署时传
`-McpAllowedOrigins 'http://127.0.0.1:3000'`，不要使用通配符。

### 10.2 可选：主机适配器

只有宿主只支持 stdio，或要在主机本地监听 MCP 时才需要：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go build -o build/device-mcp.exe ./cmd/device-mcp
```

主机私有配置 `.state/mcp-config.json`（0600，不入库）：

```json
{
  "service": {"base_url": "http://127.0.0.1:18881", "agent_token": "<agent-token>"},
  "transport": "stdio",
  "http": {
    "listen": "127.0.0.1:18882",
    "token": "<host-mcp-token>",
    "allowed_origins": []
  }
}
```

```powershell
# stdio：由 MCP 宿主拉起的子进程
./build/device-mcp.exe -transport stdio -config .state/mcp-config.json

# 主机 Streamable HTTP：独立进程，宿主以 bearer 认证连接
./build/device-mcp.exe -transport http -config .state/mcp-config.json
```

主机适配器的 `http.token` 是另一个独立口令；`service.agent_token` 与 `http.token` 必须
不同，`base_url` 与 `http.listen` 必须是 loopback。主机 HTTP 入口使用主机 `18882`；手机
适配器已转发到 `18883`，不要把手机 `18882` 再转发到主机 `18882`，否则端口冲突。

HTTP 入口的每个请求都需要 `Authorization: Bearer <MCP token>`；携带 `Origin` 时必须命中
`allowed_origins`。缺失/错误令牌返回 `401`，不允许的 Origin 返回 `403`。MCP 会话按请求
隔离；协议版本锁定 `2026-07-28`，客户端使用更低版本时拒绝。

取消与漏收：stdio 的 `notifications/cancelled` 与 HTTP 的请求 SSE 流关闭都映射为本次调用
取消；有本次 P 时适配进程发起有界 `end(P)`，只读工具仅取消读取。连接断开或 `end` 发出都
不等于手机动作已撤回或原执行链已结束；恢复仍按 `status(P)`/`status()` 与收尾事实。

## 11. 消息中心

- Agent 调用 `POST /api/notify` 后，消息先进入服务有界队列；App 每秒同步取下未确认消息，
  写入私有 `messages.json` 后在下一次同步确认。确认只释放服务端队列，不代表已显示/已读。
- 新消息发到独立通知频道 `agent-messages`；点击通知打开对应消息详情。打开消息中心或详情
  即在本机标记已读并更新角标，不向服务或 Agent 回报。
- 服务重启后未确认消息按原 ID 重发；App 按 ID 去重，不会重复列表项或重复通知。App 私有
  消息与已读不因服务实例变化、App 覆盖安装或 Activity 重建而清空。
- 不把消息正文写入服务日志、App 诊断、账本或发布清单。需要排查投递时只看受控计数/ID 与
  App 消息中心列表，不导出正文。

```bash
# 服务侧队列规模（不打印正文）
adb -s 127.0.0.1:5555 shell su -c 'wc -c /data/local/chroots/debian13/var/lib/mobile-use-vnext/messages.json'
# App 私有消息文件权限（不打印正文）
adb -s 127.0.0.1:5555 shell su -c 'ls -l /data/data/com.mobileuse.vnext/files/messages.json'
```

## 12. 整组发布、回退与服务重启演练

整组发布以 `docs/release.md` 为准。操作顺序固定：

1. 准备并核验构建、清单、契约号与工具目录；未核验的目录不得部署。
2. 暂停普通准入；用旧版本结束活动会话/STOP，分别确认执行链、task/recents/display。
   未确认就停止推进，不强停 App 把缺少描述当已清理。
3. 停止旧服务/App，安装整组产物（`adb install -r`，保留 App 私有数据）；过渡期保持不可
   准入。安装失败时保留现场，不以卸载/清库重试。
4. 启动新服务、手机侧 MCP 适配器与 App；要求 release/contract 与清单一致、
   `can_start=true`、STOP 可用、恢复屏障通过。默认虚拟屏，主屏共享需要用户重新确认。
5. 重启实际 MCP 宿主进程/连接，重新发现工具目录；只替换磁盘文件不算完成。
6. 运行相关最小闭环与回归；任何阶段失败都保持普通准入关闭并记录失败阶段。

回退是另一轮受控部署：安装上一组已核验产物、重新确认资源与准入；不自动回放旧操作、不恢复
主屏授权、不清空消息或归属来凑兼容。旧二进制无法读取已迁移数据时明确不能直接回退。

服务重启恢复演练（活动会话/资源存在时）：

1. 构造活动会话并使用虚拟屏；记录 `status` 的会话、`cleanup` 与显示。
2. 停止服务：`service.sh stop`（SIGTERM 路径）或受控 `SIGKILL` 路径；不得删除账本。
3. 重启服务与 App。预期：App 显示核验中/不可用，`can_start=false`，直到旧资源证据确认；
   不恢复旧会话、不重放动作。
4. 按 `status.cleanup` 核对执行链、task/recents/display；只有全部确认后 `can_start=true`。
5. 若长期 `blocked`，保留账本与现场，按 §7 排查，不以重启或删账本绕过。
