# 手机侧运维

本页覆盖手机内设备服务、固定运行制品与配对控制 App 的日常运行、文件、容量、服务重启、凭据与
故障处置。整组发布与回退见[发布与回退](release.md)；部署命令与常见错误见[运行手册](runbook.md)；
Agent 宿主接入见[Agent 接入指南](integration.md)。

## 1. 组件与职责

| 组件 | 运行位置/身份 | 职责 |
| --- | --- | --- |
| `device-service` | chroot `/opt/mobile-use-vnext/device-service`，root | 唯一业务权威：会话、O/P、工作位置、STOP、收尾、恢复、消息队列 |
| `u2.jar` + `app_process` | Android 侧，服务按固定哈希推送并启动 | 层级采集与显示绑定文本 RPC |
| `RootDisplayMain` 助手 | Android 侧，root，`nohup setsid` 脱离服务 | 虚拟屏创建/释放的 Binder 入口 |
| 控制 App `com.mobileuse.vnext` | Android 前台服务 + Activity | 控制同步、模式选择、本地停止、虚拟屏、通知/悬浮与消息中心 |
| Android 侧 adb 服务 | `127.0.0.1:5555` | 服务执行 ADB 命令的传输依赖 |
| 手机 MCP 适配器 `device-mcp` | chroot `/opt/mobile-use-vnext/device-mcp`，默认部署启动 | 手机 `127.0.0.1:18882/mcp`；Android/手机内 MCP 宿主使用 |

- 服务进程在固定运行目录取得内核锁；第二个实例拿不到锁时退出码 `3`，不会接触账本、
  App 或设备。
- u2 server 与 root 显示助手只由服务管理。不要手工再启动第二个 u2/helper，也不要按包名
  或宽泛 `pkill` 清理。
- 控制 App 与设备服务是配对关系；App 不是第二份执行权威，只拥有自己的显示资源与停止通道。

## 2. 文件、权限与容量

| 路径 | 内容 | 权限/上限 |
| --- | --- | --- |
| `/opt/mobile-use-vnext/device-service` | 设备服务二进制 | `0755` |
| `/opt/mobile-use-vnext/device-mcp` | MCP 适配器二进制 | `0755` |
| `/opt/mobile-use-vnext/config.json` | 监听、状态目录、ADB、令牌、制品路径 | `0600` |
| `/opt/mobile-use-vnext/mcp-config.json` | 手机 MCP 适配器配置（含 MCP token） | `0600` |
| `/opt/mobile-use-vnext/u2.jar`、`applist.dex` | 固定制品 | `0600`，按哈希校验 |
| `/opt/mobile-use-vnext/service.sh` | 设备服务启停脚本 | `0755` |
| `/var/lib/mobile-use-vnext/recovery-ledger.json` | 版本 5 恢复账本：当前归属、退休资源证据与根级任务迁移责任池 | `0600`，完整 ≤64 KiB；退休明细 ≤8 条且 ≤24 KiB；迁移池 ≤16 项、≤16 KiB；base ≤20 KiB |
| `/var/lib/mobile-use-vnext/messages.json` | 未确认 Agent 消息 | `0600`，≤2 MiB（256 条 / 1 MiB 正文） |
| `/var/lib/mobile-use-vnext/device-service.log` | 服务事件日志（启动时轮转） | `0600`，无固定上限 |
| `/var/lib/mobile-use-vnext/device-service.pid` | 服务进程记录 | `0600` |
| `/var/lib/mobile-use-vnext/device-mcp.log` | MCP 适配器日志 | `0600`，无固定上限 |
| `/var/lib/mobile-use-vnext/device-mcp.pid` | MCP 适配器进程记录 | `0600` |
| `/data/data/com.mobileuse.vnext/files/connection.json` | App 的 `base_url` + control token | App UID 私有，`0600` |
| `/data/data/com.mobileuse.vnext/files/messages.json` | App 消息、本地已读 | App UID 私有，`0600` |
| `/data/data/com.mobileuse.vnext/files/diagnostics.log` | App 受控诊断 | App UID 私有，有界 |

- 不要手工编辑账本、消息文件、`config.json` 或 App 私有文件；不要删除状态目录来“重置”。
- 账本只保存归属与资源证据，不保存树、输入正文、命令或消息；消息正文只存在于消息文件与
  App 私存。
- 消息文件有界；账本损坏时服务进入 `blocked`，不以清空账本恢复准入。

## 3. 启动、停止与检查

部署/升级在主机执行，并保持手机处于不可准入窗口：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json
```

手机整组产物由 `build/release/manifest.json` 核验；主机 `device-mcp.exe`/`mcp-probe.exe` 由
`build/host-tools-manifest.json` 核验；两者使用同一 `release/versions.json`。覆盖安装保留
App 私有数据，不卸载、不清库。

构建与核验在已提交的干净树上执行：`verify-manifest.ps1` 要求清单 `source_commit` 是实际
`HEAD` 的前缀（等值也通过）、`source_dirty=false` 且六个手机产物齐全；主机工具的
`-VerifyOnly` 还要求完整的手机清单作为同一配套版本。对脏树使用 `-AllowDirtySource` 只用于
开发构建，该清单永远不能通过核验。部署只停止本仓库的 `com.mobileuse.vnext`、`device-service` 与 `device-mcp`，不
操作其他项目的服务、脚本或包。

```bash
# 以下命令在主机执行，由 adb + su 送入 chroot
adb shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh status"'
adb shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh start"'
adb shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "export PATH=/usr/local/bin:/usr/bin:/bin; /opt/mobile-use-vnext/service.sh stop"'
```

- `start`：已有匹配进程则直接返回；否则轮转日志、后台启动，最多约 15 秒等 `/health`；
  失败退出 1。
- `stop`：先 `SIGTERM`，最多等 10 秒，再 `SIGKILL`；被强杀时退出 1。服务自身退出流程
  最多等待已接纳的后台工作约 25 秒，因此脚本可能在服务仍在收尾时强杀；强杀不是清理完成。
- 服务依赖 Android 侧 adb 服务与固定 u2 制品；`/health` 只证明进程能应答，不证明
  `can_start=true` 或资源已释放。
- App 未运行时服务仍可响应 `/health`/`/api/status`，但普通准入会被 `control_unavailable`
  或 `control_sync_pending` 拦住。

手机 MCP 适配器由部署安装并启动，但 **`service.sh` 只管理 `device-service`，不管理
`device-mcp`**。适配器生命周期命令：

```bash
# 启动（手机内 127.0.0.1:18882）
adb shell su -c 'chroot /data/local/chroots/debian13 /bin/sh -c "nohup setsid /opt/mobile-use-vnext/device-mcp -transport http -config /opt/mobile-use-vnext/mcp-config.json >> /var/lib/mobile-use-vnext/device-mcp.log 2>&1 < /dev/null &"'

# 查看进程与日志
adb shell 'su -c "pgrep -f \"^/opt/mobile-use-vnext/device-mcp \""'
adb shell su -c 'tail -n 50 /data/local/chroots/debian13/var/lib/mobile-use-vnext/device-mcp.log'

# 停止
adb shell su -c 'pkill -f "^/opt/mobile-use-vnext/device-mcp " || true'
```

- 同一时间只能有一个监听 `18882` 的适配器；重新部署会先停止旧进程再启动新进程。
- 适配器无会话状态；重启后调用方重新 `tools/list`，会话权威仍在设备服务。

主机侧转发：

```powershell
adb forward --remove tcp:18881
adb forward tcp:18881 tcp:18880
adb forward --remove tcp:18883
adb forward tcp:18883 tcp:18882
curl.exe -s http://127.0.0.1:18881/health
```

`18881` 供主机直连 HTTP；`18883` 供主机 MCP 客户端访问手机适配器。不要再把手机 `18882`
转发到主机 `18882`，否则会与主机自建 MCP 适配器冲突。

### 3.1 工具目录与联合版本校验

手机侧 MCP 适配器与设备服务共用同一工具目录，固定为 12 个工具：`start`、`status`、
`observe`、`wait_for`、`act`、`inspect`、`keepalive`、`end`、`app`、`notify`、`shell`、
`diagnose`。适配器按同一份目录投影 `tools/list`，发布清单与探针按工具目录指纹核对，描述或
schema 变化都会改变指纹。

服务端、手机 MCP 适配器与控制 App 必须同为 `contract_version=16`，控制 App 还须与清单的
versionName/versionCode 一致：

- `/health` 的 `contract_version` 与 `release_version` 必须等于清单值；
- 普通同步与 `/api/*` 的契约头不匹配返回 `409 contract_mismatch`，只有最小 STOP 仍可用；
- 适配器与服务版本不一致时，工具调用会被服务按 `409 contract_mismatch` 拒绝；升级后必须
  重启适配器进程，宿主要重新 `tools/list` 才能看到新目录。

## 4. 启动与恢复

- 本仓库不安装开机自启。设备系统启动后按顺序确认：chroot 可进入、Android 侧 adb 服务可用、
  固定制品存在；然后 `service.sh start`、启动控制 App、启动手机 MCP 适配器、重新建立
  主机转发 `18881` 与 `18883`。
- 首次 `/api/status` 可能为 `can_start=false`，原因只来自当前 App/模式/STOP、设备、锁屏、
  账本可写性或最多 5 秒的旧逻辑交接。旧 cleanup 可同时显示为 `pending`/`unconfirmed`，
  但不单独保持 `can_start=false`。这期间不要删除账本、不要强停 App 或反复重启服务。
- 控制 App 重建默认虚拟屏，不恢复主屏共享；主屏需要用户在 Activity 中重新确认。
- 服务实例变化后 App 会先使旧命令失效并释放自有显示；旧账本/资源证据仍由服务恢复流程
  核验，不能改挂到新实例继续执行。
- 服务强杀或设备启动边界变化后，旧虚拟屏、任务与受管 shell 链可能仍存在；以同一原会话的
  `status.cleanup` 为准，不用“进程已启动”或“设备已启动”判断资源干净。

## 5. 电池、后台与权限

- 控制 App 使用 `specialUse` 前台服务与部分唤醒锁，保证控制同步与停止通道在锁屏/离开
  页面后仍运行；唤醒锁不亮屏、不持有主屏窗口。
- 通知权限与 `agent-control` 频道是 `stop_ready` 的必要条件；系统通知关闭或频道被禁用时
  普通准入会被 `stop_unavailable` 拦住，直到恢复。
- 消息通知频道 `agent-messages` 独立；关闭它只影响新消息提示，不改变控制同步、消息列表或
  停止通道。消息仍保存在 App 私存。
- 悬浮窗权限影响悬浮入口，不影响常驻通知与 App 内停止按钮；部署会用 appops 授予，但用户
  仍可在系统设置中撤销。
- MIUI/Android 的后台限制、省电策略与“屏幕用时限制”可能挂起新装 App 或拦 Activity；
  部署脚本会 `pm unsuspend` 并重试，但系统策略仍需用户/运维确认。
- Doze、锁屏与熄屏没有被绕过；App 上报 `power_interactive`/`keyguard_locked`，服务按
  `device_not_interactive`/`device_locked` 拒绝普通工作。

## 6. 凭据与轮换

| 凭据 | 位置 | 轮换方式 |
| --- | --- | --- |
| Agent token | 服务 `config.json`；部署机 `.state/deploy.json` | 修改部署机私有记录后重新部署；服务与 Agent/MCP 配置同步更新 |
| Control token | 服务 `config.json`；App `connection.json` | 重新部署会重写 App 连接配置 |
| 手机 MCP token | `.state/deploy.json` 的 `mcp_token`；chroot `mcp-config.json` 的 `http.token` | 在 `.state/deploy.json` 写入/删除该字段后重新部署；适配器配置与进程一起刷新 |
| 主机 MCP token（可选） | `.state/mcp-config.json` 的 `http.token` | 重新生成/编辑该私有配置并重启主机适配器与客户端 |

- 令牌只出现在对应私有文件中；不要打印到终端历史、工单或日志。
- 轮换后旧令牌立即失效；所有使用方必须同步更新，否则会得到 `401`。
- 不要通过卸载 App、清空数据或删除 `.state/deploy.json` 解决认证/配对问题；后者会丢失
  旧令牌与部署记录。

## 7. 容量与清理

- 账本、消息文件、App 消息与诊断均有界；增长主要来自服务日志、MCP 适配器日志与受管
  shell 运行目录。
- 账本 v5 按实际序列化分区：完整账本 ≤64 KiB；base（不含 `retired` 与 `task_transfers`）
  ≤20 KiB；`retired` ≤8 项且 ≤24 KiB；`task_transfers` ≤16 项、每项 ≤1000 B、数组 ≤16 KiB。
- `retired` 仅在超过 8 条或序列化集合超过 24 KiB 时进入容量裁剪。裁剪时先压缩一条尚未
  压缩且单条编码超过 21 KiB 的退休记录，停止其 cleanup、收缩归属摘要，再重新检查容量；
  没有可压缩记录时才驱逐。
- 驱逐顺序为 `complete` → `unconfirmed` → `pending`，仅适用于实际移除记录；同状态先驱逐
  开始时间更早者。被压缩或驱逐的未完成清理记为 `unconfirmed` 并计入
  `dropped_unconfirmed`，不能写成 `complete`。保护池项的回收必须晚于所有可能删除同一任务
  的引用；`prepared`/`may_dispatch`/`unknown` 的出站记录不得为腾空间丢弃。
- 服务日志只在 `service.sh start` 时轮转（`.log.1`、`.log.1.old`）；长期不重启时需外部
  轮转或监控磁盘，不要删除正在写入的日志。
- 受管 shell 的单次运行目录位于 `/data/local/tmp/mobile-use-shell/<run-id>/`；确认结束后
  由服务收尾移除。`status` 仍显示 shell 未结束/未确认时不要手工删目录或杀进程。
- 磁盘/权限导致账本写失败时，可用性转 `blocked`（`ledger_unavailable`）；消息文件写失败
  只记录受控事件并继续运行期交付，不改变 STOP、清理或准入。先修文件系统/权限，不要删文件
  绕过。
- 不要清空 `messages.json` 来“解决”消息队列；队列有界，确认/淘汰会自动释放。

## 8. 备份、迁移与回退

- 需要一致备份时先停止服务，再以只读方式复制 `/var/lib/mobile-use-vnext/`。运行中复制
  账本因原子替换通常可读，但可能对应某一时刻的中间状态。
- 不要在服务运行时恢复旧账本；旧归属可能仍占用资源。服务保留其退休记录与未确认事实，
  需要当前归属可保存且当前 App/设备能力就绪，不以旧资源全部确认作为新会话准入条件。
- App 数据（消息、已读、连接、停止意图）属于 App 私有目录；默认 `allowBackup=false`，
  仓库不提供自动备份/迁移工具。换机或重装前由运维决定是否 root 复制；不要用卸载/清库
  解决签名或配对。
- 升级/回退按[发布与回退](release.md)执行：整组产物、清单核验、覆盖安装、恢复屏障与
  MCP 宿主重启；升级不触发手机重启，设备服务按脚本重启。回退是另一轮受控部署，不自动
  回放旧操作。账本写版本 5；首次 0.9.0 写入 v5 后不支持回退到 0.8.0，只能使用同为 v5 的
  候选或前滚修复；不得删除账本、清空 App 数据或忽略迁移字段。

## 9. 故障处置

| 现象 | 原因/检查 | 处置 |
| --- | --- | --- |
| `can_start=false`，`control_unavailable`/`control_sync_pending` | App 未运行、失鲜或未同步 | 启动 `com.mobileuse.vnext/.ControlService`，确认通知权限；等待同步 |
| `stop_unavailable` | 通知权限/频道关闭、前台服务未运行 | 恢复通知权限与频道；App 内仍可请求停止 |
| `mode_not_selected` | 虚拟屏未就绪或主屏未确认 | 在 App 选择虚拟屏或重新确认主屏共享 |
| `device_not_interactive`/`device_locked` | 熄屏或锁屏 | 解锁点亮；服务不绕过锁屏 |
| 同一原会话的 `cleanup.pending` | 有限自动补偿仍在运行 | 最多 12 秒、三轮；可同时 `can_start=true`，不要无限轮询 |
| 同一原会话的 `cleanup.unconfirmed` | 主动补偿已停止，仍有未知事实 | 保留同帧 session/五项证据；不改成 confirmed，不单独阻止新会话 |
| `device_busy` 且原会话刚结束 | 旧 work/UI/shell 正在最多 5 秒的逻辑交接 | 等待交接；不杀设备进程、不删运行目录 |
| 原会话返回 `uncertain` 后普通工具仍可用 | 输入窗口在调用返回时关闭，且不是准入屏障 | 用一次新 `observe` 确认现状；不重放原输入；受管 shell 例外，见下方 `shell_pending` 行 |
| `recovery_pending`/`control_sync_pending` | 当前启动恢复入口或 App 当前同步未就绪 | 检查当前 ADB、App、模式与 STOP；旧 cleanup 未确认本身不是原因；恢复不要求重启手机 |
| `shell_pending` | 受管命令未结束或终止未确认 | 屏障最长到 `deadline_at + ShellConfirmTimeout + 3s`（默认约执行预算结束后 8 秒）；到界后以 `termination=unknown` 结案让位，不杀 runner、不删运行目录，且 `cleanup.execution` 保持未确认 |
| `ledger_corrupt`/`ledger_unavailable` | 账本损坏或不可写 | 检查目录权限/磁盘；损坏账本需人工处置并重新部署，不能带病启动 |
| `device_unavailable` | Android 侧 adb/u2 不可用 | 检查 `adb -s 127.0.0.1:5555 get-state`、u2 端口与固定制品哈希；重启服务触发 `EnsureU2` |
| 采集返回 `display_unavailable` | 当前目标显示探测失败 | 确认 SurfaceFlinger/`dumpsys display` 与 App 当前 `display`；不要手工关闭用户主屏 |
| 控制 App 崩溃或反复重启 | 权限、系统挂起或旧版本缺陷 | 更新到配套 App；`pm unsuspend`；查看 App 诊断日志的受控事件 |
| 主机 `/health` 不通 | adb 转发失效或服务未启动 | 重建 `adb forward`；`service.sh status`；核对端口 |
| 主机 `18883/mcp` 不通 | 手机适配器未运行、转发失效或端口冲突 | 看 `device-mcp.log` 与 `pgrep`；重建 `adb forward tcp:18883 tcp:18882` |
| 手机适配器启动即退出 | `18882` 被占用、配置错误或二进制不匹配 | 只保留一个适配器进程；检查 `mcp-config.json` 与日志 |
| MCP 客户端 401 | 用了主机 token、agent token，或 token 已轮换 | 手机路径填 `mcp_token`；主机适配器填其 `http.token`；重启客户端 |
| 手机适配器 403 | 客户端发送了 `Origin` 但未在 `allowed_origins` | 部署时用 `-McpAllowedOrigins` 精确放行并重启适配器 |
| 消息中心无新消息 | 服务队列、App 同步或消息频道 | 看 `notify` 是否 `pending`、App 是否运行；消息频道关闭不影响私存 |
| 通知栏没有停止按钮 | 通知权限/频道关闭或 FGS 未运行 | 恢复通知权限；App 内按钮与悬浮入口仍可用 |

日志：

```bash
adb shell su -c 'tail -n 100 /data/local/chroots/debian13/var/lib/mobile-use-vnext/device-service.log'
adb shell su -c 'tail -n 100 /data/local/chroots/debian13/var/lib/mobile-use-vnext/device-mcp.log'
adb shell su -c 'tail -n 100 /data/data/com.mobileuse.vnext/files/diagnostics.log'
```

日志只含受控事件、ID、枚举、布尔与计数；不要在其中寻找树、输入正文、命令或消息正文。

## 10. 紧急停止

1. **首选 App 内停止按钮**：本地立即撤权并释放自有显示，再独立同步服务。
2. **常驻通知或悬浮入口**：同一停止回调；悬浮入口不可用时通知仍应可用。
3. **独立最小停止**（App 不可用时由运维使用 control token）：

   ```powershell
   $control = (Get-Content .state/deploy.json -Raw | ConvertFrom-Json).control_token
   Invoke-RestMethod http://127.0.0.1:18881/v1/control/stop -Method Post `
     -Headers @{ Authorization = "Bearer $control" } -ContentType 'application/json' `
     -Body '{"contract_version":"16","app_instance_id":"<uuid>","stop_seq":1,"reason":"operator_stop"}'
   ```

   `accepted=true` 只表示停止边界已绑定；仍需用 App/`status.cleanup` 核验执行链、任务与
   显示是否释放。合成停止请求会撤销当前活动会话，但不代替 App 本地停止意图；同一
   `app_instance_id` 下重复使用同一 `stop_seq` 只会得到 `duplicate`，每次独立操作应使用
   新 UUID，或在该 UUID 上递增序号。服务会在 App 下一次成功同步后重新评估准入。

4. `service.sh stop` 只停止设备服务进程；服务退出会撤权并启动收尾，但收尾结果仍来自证据。
   手机 MCP 适配器是独立进程，需要时按第 3 节单独停止。不要用 `kill -9`、删除账本或重启
   App 当作清理完成。

## 11. 安全检查

- 服务、u2 与 MCP 只使用回环地址；不把端口暴露到局域网/公网，不做反向代理。
- 手机 MCP 适配器以 root 读取 agent token；Android 客户端只应持有 `mcp_token`，不要把
  `mcp-config.json` 或 `config.json` 复制到应用可读目录。
- root 私有文件保持 `0600`/`0700`；不复制令牌到 `/sdcard`、共享目录或其他应用可读位置。
- 控制 App 只从私有 `connection.json` 读取 control token；Activity/通知/悬浮都只消费
  同一份服务快照。
- 不手工启动第二份服务、u2 或显示助手；不以清除数据、卸载 App 或删账本解决配对/恢复。
- 日志、诊断与工单中不出现令牌、树、输入正文、命令、消息正文、截图或原始响应。
