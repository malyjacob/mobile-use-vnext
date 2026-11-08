# 运行手册

面向已有一台 root 手机、已存在 Debian 13 chroot 的受控环境。所有路径与端口来自
`scripts/deploy.ps1`、`scripts/service.sh` 与服务私有配置的默认值；换机型或 chroot 前先核对。
本页覆盖环境、部署、启动停止、健康检查与故障处置。架构与契约分别见
[架构](architecture.md)、[接口契约](interface.md)。

## 1. 环境

| 项 | 值 |
| --- | --- |
| 设备 | Xiaomi `24117RK2CC`，Android 16（API 36），arm64 |
| 特权 | Magisk root；Debian 13 chroot 需预先准备 |
| chroot | `/data/local/chroots/debian13` |
| 服务目录（chroot 内） | `/opt/mobile-use-vnext`：`device-service`、`u2.jar`、`applist.dex`、`service.sh`、`config.json` |
| 状态目录（chroot 内） | `/var/lib/mobile-use-vnext`：`recovery-ledger.json`、`device-service.pid`、`device-service.log`（0700） |
| 服务端口 | `18880`，只监听 `127.0.0.1` |
| adb 串口 | `127.0.0.1:5555` |
| 主机转发端口 | `18881`（`adb forward tcp:18881 tcp:18880`） |
| 控制 App | `com.mobileuse.vnext`，固定连接 `http://127.0.0.1:18880` |
| 固定制品 | `third_party/uiautomator/u2.jar`、`third_party/android-applist/applist.dex`（SHA-256 固定，替换前必须核对） |
| 受管执行临时资源 | `/data/local/tmp/mobile-use-shell-runner.sh`（固定脚本）与 `/data/local/tmp/mobile-use-shell/<run-id>/`（单次运行目录，确认结束后移除） |

服务进程在 chroot 内运行，ADB/u2 通过 Android 侧 `adb`/`app_process` 工作；控制 App 运行在
Android 侧，只与服务通信。Windows 主机只负责构建与下发，不参与运行时状态。

受管 shell 的命令文件、start/done 元数据与有界输出位于单次运行目录；命令正文与输出不写
服务日志、不写恢复账本。运行目录残留或进程身份无法核验时，保持恢复屏障并人工核验，
不得按包名/PID 猜测清理。

## 2. 私有文件

| 文件 | 位置 | 内容 |
| --- | --- | --- |
| `deploy.json` | 仓库 `.state/`（部署机） | agent/control 令牌、端口、chroot、版本 |
| `config.json` | `.state/` 生成，推送到 `/opt/mobile-use-vnext/`（0600） | 监听地址、状态目录、ADB 串口、令牌、u2 路径 |
| `connection.json` | 推送到 `/data/data/com.mobileuse.vnext/files/`（0600，App UID 所有） | `base_url` + control 令牌 |

`.state/` 已在 `.gitignore` 中排除；不要复制到仓库、工单、聊天或日志。令牌泄漏时重新部署会
复用旧令牌——需要轮换时删除对应私有文件后再部署。

## 3. 部署与升级

前置：`adb devices` 能看到 `127.0.0.1:5555` 且状态为 `device`；本机有 Go 1.27.1、Android
SDK/JDK（构建 App 用）；chroot 已存在且可 `su` 进入。

```powershell
cd E:\Code\Python\mobile-use-vnext
./scripts/build.ps1                           # 可选：单独构建 Go 服务
./scripts/deploy.ps1                          # 完整构建 + 部署
./scripts/deploy.ps1 -SkipBuild               # 复用现有产物
```

`deploy.ps1` 的实际动作：

1. 校验在线设备与必需产物；构建 Go 服务与控制 App（未 `-SkipBuild` 时）；
2. 读取或生成 `.state/deploy.json`，写 `.state/config.json`、`.state/connection.json`；
3. 尽力停止旧 Python 服务、旧包 `com.mobileuse.control` 与现有 Go 服务；
4. 推送到 `/data/local/tmp/mobile-use-vnext-deploy`，再 `su` 复制进 chroot，设置权限；
5. 安装 APK、写入 `connection.json`、授予通知权限与悬浮窗 appops；
6. 用 `service.sh start` 启动服务，用 root 启动 `com.mobileuse.vnext/.ControlService`
   （MIUI 新装包可能被挂起，脚本会 `pm unsuspend` 并重试 8 次）；
7. `adb forward tcp:18881 tcp:18880`，轮询 `/health` 与 `/api/status`，直到
   `can_start=true`；90 秒未就绪则失败并打印最后一次 `status`。

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

控制 App 侧：安装后打开一次 `MainActivity`，或由部署直接启动
`com.mobileuse.vnext/.ControlService`（action `com.mobileuse.vnext.START`）。App 每秒同步，
连续 6 秒无成功响应即判失鲜；失鲜期间 `start` 会以 `control_unavailable` 被拒
（`can_start=false`），同步恢复后自动解除。`availability` 是 App 上报事实的投影，
失鲜不一定马上把它改成 `blocked`；准入判断以 `can_start` 与 `blocking_code` 为准。

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
  -H "X-Mobile-Use-Contract: 1" \
  -H "Content-Type: application/json" -d '{}'
```

- `can_start=true` 才可 `start`；
- `can_start=false` 时读 `blocking_code` 与 `cleanup`/`session`；
- 端口已监听、`/health` 正常、进程存在都不能替代这个门禁。

## 6. 日志

| 日志 | 位置 | 说明 |
| --- | --- | --- |
| 服务日志 | chroot `/var/lib/mobile-use-vnext/device-service.log`（轮转为 `.log.1`） | 事件名 + 白名单字段；不写树、输入正文、命令输出或异常文本 |
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
| `dumpsys SurfaceFlinger` 的层 id 解析失败或溢出 | 该机 SFID 超出 int64（大于 2^63） | 服务按 `uint64` 解析并原样传给 `screencap -d`；不要改成 `int64`/`int`，不要回退到 display 0。只读核对用 `dumpsys SurfaceFlinger --display-id` |
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
| `409 contract_mismatch` | `X-Mobile-Use-Contract` 缺失/不是 `1`，或 App 契约版本不符 | 服务与客户端必须同为 `contract_version=1`；升级整组产物，不要只升一侧 |
| `401 unauthorized` | Bearer 令牌错误，或用了另一身份的令牌 | 从私有配置核对 agent/control 令牌；两者不能互换 |
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
- 不手工编辑 `recovery-ledger.json`；不在未确认收尾时删除状态目录；不以 `kill -9` 或重启
  App 当作清理完成。
- 不在虚拟屏上显示控制 App 界面；采集发现控制包可见节点会直接失败。
- 不把令牌、节点树、输入正文、截图、原始 `dumpsys`/logcat 输出贴进仓库或工单。
- 服务只提供 [接口契约](interface.md) 列出的端点；图片与 MCP 不由本服务提供，
  不要用 ADB 手工命令伪造为服务能力。
