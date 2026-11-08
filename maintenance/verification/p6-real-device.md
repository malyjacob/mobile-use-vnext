# P6 控制呈现、消息中心与整组交付（实机验证）

记录时间：2026-11-09。代码与发布产物来自提交
`98645a71f23cd43ea172a7a4efb95ffe4f0de9b1`；清单 `source_dirty=false`。本页只记录实际
执行过的命令与结果，不把设计推演写成验证结论。

## 1. 环境

| 项 | 值 |
| --- | --- |
| 设备 | `24117RK2CC`（`ro.serialno=17efb04a`），Android 16（API 36），arm64，Magisk root |
| chroot | `/data/local/chroots/debian13` |
| 服务目录 / 状态目录 | `/opt/mobile-use-vnext`、`/var/lib/mobile-use-vnext` |
| 端口 | 服务 `127.0.0.1:18880`；主机转发 `18881`；u2 `9008` |
| 发布 | `release_version=0.4.0`、`contract_version=2`、`versionCode=3` |
| 部署前基线 | `0.3.0` / contract `1`、`can_start=true`、`cleanup=complete`；旧服务二进制 SHA-256 `97a2565b32349baba51b71f5c8c13591d55b25ce627e54860e7388983f9e57d9`，旧 APK SHA-256 `cb895f96ff6f343ced246781891e792b49fd0761694d62e2b6c1bfc52f47c425` |

## 2. 构建与清单

执行：

```powershell
./scripts/generate-version.ps1 -Check
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
```

结果：版本源一致；整组 6 个产物构建完成；`verify-manifest.ps1` PASS。清单关键值：

```text
release=0.4.0 contract=2 android=0.4.0/3 source_commit=98645a71f23cd43ea172a7a4efb95ffe4f0de9b1 source_dirty=False
tool_catalog=12/d9875ef2a9988a32167fa37943ec949aee3d19e642666b950449bfff68711aa4
device-service sha256=e112d15df25c7acb0fd4a9cde35ba52bfeb26ab2b3be4ad14ecaf2298988f18c bytes=8192160
device-mcp     sha256=1e2d9f2dccf54da226dcd2ffd32c7800e62ba0117d3a511a4b6737dc7977d936 bytes=9109664
control.apk    sha256=30151a2ef0efae9184ce2a0304d1020199b793ac08c6403cc1c405d62691c844 bytes=95773
u2.jar         sha256=28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3 bytes=3306229
applist.dex    sha256=63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8 bytes=4316
service.sh     sha256=4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0 bytes=3643
```

`scripts/verify-manifest.ps1` 同时复核了发布号、契约号、Android versionName/versionCode、
每个产物的 SHA-256/大小与工具目录指纹。构建、清单与部署均未包含 `.state/`、令牌、账本、
消息、截图或树。

## 3. 破坏性整组发布

从 `0.3.0` 开始：先用旧版本发送 STOP，`/api/status` 返回 `cleanup=complete`；停止旧 App
与服务后执行：

```powershell
./scripts/deploy.ps1 -SkipBuild
```

实际输出（节选）：

```text
installed App: package=com.mobileuse.vnext versionName=0.4.0 versionCode=3
version match: PASS (health release=0.4.0 contract=2)
App pairing: PASS (can_start=true implies a fresh paired control sync)
recovery barrier: PASS (health status=ok; can_start=true requires availability=ready)
STOP availability: PASS (stop_ready is part of can_start)
MCP host rediscovery: NOT VERIFIED (restart the real MCP host process separately)
```

部署使用 `adb install -r`，没有卸载 App、`pm clear` 或删除 `/data/data/com.mobileuse.vnext`；
安装后由脚本重新授予通知权限与悬浮窗 appops。该发布前后多次执行覆盖安装（最终发布、二次
部署与不匹配组合的新旧 APK 切换），App `messages.json`（2015 字节，未读 2）与
`connection.json` 保留，`can_start=true`。

## 4. 控制呈现（Activity / 常驻通知 / 悬浮）

`scripts/verify-m6.ps1` 的最终运行（`RESULT PASS`）包含：

```text
CHECK admissible baseline: PASS can_start=true
CHECK primary confirmation: PASS explicit confirmation; cancel keeps virtual
CHECK control detail: PASS availability/session/work/cleanup/mode/permissions
CHECK control view entries: PASS Activity/notification/overlay show the same standby snapshot
CHECK stale presentation: PASS no cached active state on any entry
CHECK stale recovery: PASS can_start=true after service resume
```

- Activity 详情实测文本包含：`停止同步：已同步`、`可用性：就绪`、`会话：无`、
  `设备操作：无`、`资源清理：已确认 · 执行链 已确认 · 任务 不适用 · 最近任务 不适用 ·
  显示 不适用`、`所选模式：虚拟屏`、`通知：可用`、`悬浮窗：可用`。
- 主屏入口点击后出现确认对话框 `共享本机主屏给 Agent？`；取消后仍为 `已选择：虚拟屏`。
- 暂停宿主侧服务进程（`SIGSTOP`，u2 仍存活）8 秒后：Activity 与常驻通知显示
  `连接中断 · 服务状态未知`，悬浮入口显示断线短文案；Activity 不再出现 `虚拟屏 · 待命`
  或 `会话有效`；`SIGCONT` 后 `can_start=true`。
- 同一快照驱动三个入口：待命、失鲜与停止待同步文案都由 `ControlState` 生成，未新增独立
  轮询或第二份会话状态。

## 5. 消息中心

`notify` 调用与同步投递实测（最终发布）：

```text
CHECK notify accepted: PASS id=M1794170625338 notification=pending
CHECK App message store: PASS message persisted
CHECK message acknowledgement: PASS released from service queue
CHECK system notification: PASS channel+text
CHECK message center list: PASS
CHECK message detail: PASS
CHECK local read: PASS unread 3 -> 2 (never reported to the service)
```

- 服务端 `messages` 与 `message_acks` 生效；App 保存成功后确认，服务文件中的同一 ID 消失。
- App 私有 `files/messages.json`（0600）保存消息与已读；列表、详情、未读计数与
  “全部已读”可用；详情打开后未读数减 1，服务端未收到已读。
- 系统通知进入独立频道 `agent-messages`，点击打开对应详情；通知正文只来自本次消息。
- 跨服务重启保留：停止 App 后发送消息，服务停止/启动，`messages.json` 中同一 ID 仍存在；
  启动 App 后消息写入 App 私存并从服务队列释放：

```text
service before restart present
service after restart present
app present
service after ack absent
```

- 消息正文未出现在服务日志、App 诊断或恢复账本；验证只读取 ID 与计数。

## 6. 契约配对与不匹配组合

- 旧契约普通同步：`POST /v1/control/sync` 携带 `contract_version=1` 返回
  `409 contract_mismatch`（`verify-m6`：`CHECK contract pairing: PASS`）。
- 最小 STOP 不校验普通契约：同一请求体使用旧契约发送 `/v1/control/stop` 返回
  `accepted=true`、`state=recorded`；随后正常同步恢复 `can_start=true`。
- 新 App + 旧服务：安装 `0.3.0` APK 后，App 页面显示
  `版本不匹配 · 设备操作不可用`（详情含 `HTTP 422`），普通同步被拒绝；触发 App STOP 后
  服务账本 `stop.seq` 从 4 增至 5，页面显示 `已阻止本地旧命令 · 停止待同步`。恢复新服务后
  `can_start=true`。
- 新服务 + 旧 App：旧 App（versionCode 2）普通同步按服务端契约校验被 `409` 拒绝（同一请求
  形状在 `verify-m6.ps1` 中实测）；其服务仍可发送独立 STOP，服务账本 `stop.seq` 从 5 增至 6。
  旧 App 在没有待同步停止时打开 Activity 会因旧版本自身的 `ControlState.cleanupRisk`
  空指针而退出；这是旧版本缺陷，新版本已修复并有 JVM/实机呈现覆盖，不影响新服务拒绝旧
  普通请求与最小 STOP。
- 恢复新 App 后重新授予通知权限，`can_start=true`，App 私有消息与已读保留。

## 7. 服务重启恢复

`verify-m6.ps1` 最终运行：

```text
CHECK active session before restart: PASS origin=P2
SIGKILL sent to service pid 432
CHECK restart barrier: PASS can_start=false blocking_code=control_sync_pending
CHECK restart recovery: PASS no old active session, cleanup complete, can_start=true
CHECK SIGTERM barrier: PASS can_start=false blocking_code=control_sync_pending
CHECK SIGTERM recovery: PASS App restart defaults virtual, no old active session, can_start=true
```

- 活动会话与虚拟屏存在时 `SIGKILL` 服务；重启后 `can_start=false`，App 显示未知/核验中，
  恢复完成后 `cleanup.state=complete`、无旧 active 会话、`can_start=true`。
- `SIGTERM` 路径先停止 App，使旧显示无法确认释放；重启服务后 `can_start=false`，
  启动新 App 进程后完成恢复，App 默认回到虚拟屏，无旧会话恢复。
- 服务日志出现 `recovery.complete reason="old_resources_resolved"`；未恢复旧输入授权、
  未重放动作。恢复屏障窗口较短（App 在服务实例变化时自行释放显示，约 1–2 秒完成），
  未构造分钟级 `blocked` 窗口；见第 10 节。

## 8. M5 回归与 MCP 重新发现

整组发布后重新构建并启动实际 MCP 适配进程（`verify-m5.ps1` 每次 probe 拉起新的
`build/device-mcp.exe`），重新执行 `tools/list`：

```text
stdio:  PASS tools count=12; start -> observe(image) -> act(image) -> end; cancel; RESULT PASS
http:   PASS auth 401/403, tools count=12, image 540x1200 JPEG, cancel; RESULT PASS
p-rec:  PASS missed_response, status recovery, single dispatch, end; RESULT PASS
service health=ok contract_version=2; service can_start=true
```

`observe`/`act` 的图片为 `image/jpeg`、540×1200、10793 字节，文本正文不含 base64。
这验证的是本机 probe 作为 MCP 宿主重新发现工具；未接入外部宿主缓存，见第 10 节。

### 8.1 手机 MCP 适配器默认部署（2026-11-09 补充）

默认部署改为安装并启动手机侧 `device-mcp`（手机 `127.0.0.1:18882/mcp`，主机转发
`18883`）。实际执行：

```text
scripts/deploy.ps1 -SkipBuild
...
phone MCP adapter: PASS listen=127.0.0.1:18882 url=http://127.0.0.1:18883/mcp

scripts/verify-m6.ps1
CHECK phone MCP adapter: PASS url=http://127.0.0.1:18883/mcp discover=supportedVersions
RESULT PASS
```

补充手工检查：

- `server/discover` 返回 `supportedVersions=["2026-07-28"]`；
- `tools/list` 返回 12 个工具，名称与接口契约一致；
- 无 token 的 `GET /mcp` 返回 `401`，带 token 的 `GET` 返回 `405`（客户端应 POST）；
- chroot `/opt/mobile-use-vnext/mcp-config.json`、`/var/lib/mobile-use-vnext/device-mcp.log`
  与 `device-mcp.pid` 均为 `0600`；
- `/var/lib/mobile-use-vnext/device-mcp.pid` 为实际 `device-mcp` 进程 PID。

边界：`service.sh` 仍只管理 `device-service`；重启手机后需要按
[手机侧运维](../../docs/phone-operations.md)单独启动手机 MCP 适配器。
代码提交：`e4a135e`（部署脚本与文档）；部署产物仍来自 manifest 的
`source_commit=98645a7`。

## 9. 数据保留

- 最终发布前后两次 `deploy.ps1` 均使用覆盖安装；App `files/messages.json`、未读状态、
  `connection.json`、停止偏好保留。
- 不匹配验证中安装旧 APK、再安装新 APK 后，App 私存消息仍在（`messages.json` 2015 字节，
  未读 2），`can_start=true`；没有通过卸载/清库解决签名或配对。

## 10. 限制与未验证

- 倒序/迟到 `control_view.revision` 未单独注入实机；由 App 只接受同实例更大 revision 与
  代次丢弃旧响应的代码路径覆盖，未做专项实机注入。
- 服务重启后的恢复屏障在本次实机中表现为 `control_sync_pending` 到 `can_start=true`，
  未观察到持续的 `recovery_pending`/`old_resource_unconfirmed` 长时间窗口；恢复在 App
  实例变化释放显示后约 1–2 秒完成。
- 外部 MCP 宿主（如桌面客户端）的缓存刷新未验证；本机只验证 probe 拉起实际
  `device-mcp.exe` 并重新 `tools/list`。
- 旧 App 在新服务下的 Activity 空指针崩溃属于旧版本缺陷；旧 App 服务仍可发送独立 STOP。
- 通知权限/频道关闭时的消息中心降级只做代码路径检查，实机只走通知可用路径。
- 消息“已保存但确认丢失”的重复投递未在设备上构造；去重由 `MessageListTest` 覆盖，
  服务端按未确认重发并以 ID 幂等释放。
- `go test -race` 未执行：Windows 宿主无 C 编译器，cgo 不可用。
- apps 预算截断/缓存失鲜并发、inspect 超限、页面明显变化后的图片差异等 P4 遗留未在
  本阶段重新构造。

## 11. 复现命令

```powershell
# 版本、整组构建与清单
./scripts/generate-version.ps1 -Check
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json

# 部署（保留 App 私有数据）
./scripts/deploy.ps1 -SkipBuild

# 控制呈现、消息、契约、重启恢复
./scripts/verify-m6.ps1

# M5 回归与 MCP 工具重新发现
./scripts/verify-m5.ps1

# Go/Java 检查
$env:GOEXPERIMENT='jsonv2'; go test ./... -count=1; go vet ./...
powershell -File android-control/test.ps1
powershell -File android-control/build.ps1
```
