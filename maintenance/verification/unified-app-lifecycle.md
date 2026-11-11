# 统一 app 工具（契约 9 / 账本 v5）：实现与验证记录

- 实现提交：`1d27416e253618ae50cd26f2af4b0123a90cc1b8`（2026-11-12）。
- 提案：`maintenance/proposals/unified-app-lifecycle.md`（归档提交移入 `implemented/`）。
- 环境：Windows 主机（Go 1.27.1，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0`）；手机
  `17efb04a`（`zorn`），chroot `/data/local/chroots/debian13`；控制 App `0.9.0`
  （versionCode 11）；设备服务与手机 MCP 适配器均为本提交构建产物；会话模式 `virtual`。
- 本记录区分“已实现 / 已单测 / 已实测”，不以构建替代部署或实测。

## 1. 分诊

取舍类（新能力与新契约），按提案推进。无缺陷修复式的先红复现；反向对照与严格接入用例
作为实现门禁，见 §3。

## 2. 代码落点

- `internal/protocol`：`AppRequest` 七 kind 严格解码、`AppView` 判别编码/解码、`target`
  互斥、错误码与 409/422/503/504 映射；删除 `apps`/`open_app` 公共面；`Result` 只保留
  `app` 成员。
- `internal/engine`：`App` 七分支；find/list 为可取消查询工作（`query_cancelled`/
  `query_timeout`，无 outcome）；open 复用/新建与主屏 shared；claim/handoff 根任务搬移、
  owned/borrowed/shared 归属与 `task_transfers` 责任池；close 冻结 ≤16 逐项结果；kill
  前台/FGS/播放/他屏/借用/root/进程身份守卫与 PID 后验；恢复/status/重复 P 摘要只回
  `app.kind`；退休记录 >21 KiB 压缩为有界 owner 摘要。
- `internal/android`：`RootTask` 解析、`MoveTask`/`RemoveTask` 回执三态、包进程清单
  （start ticks、`:remote`）、精确 `kill -9`、FGS 与 MediaSession 事实。
- `internal/store`：账本 v5、v1–v4 迁移、v5 字段禁入旧版本、base 20 KiB / 退休 24 KiB 且 ≤8 项 /
  迁移池 16 项·16 KiB / 全程 64 KiB 校验、退休记录禁嵌迁移池、禁止降存 v4。
- `internal/httpapi`、`internal/client`：`/api/app`（8 KiB）、按 kind 交付预留、
  `Client.App`，删除旧路由与旧方法。
- `internal/mcp`：11 工具、`app` schema 七分支、按 kind 的 P/取消/IsError、三模式同源、
  `app.*` 文本分类、未知结果只补 `app.kind`；启动日志 tools=11。
- `scripts`、`scripts/mcp-probe`、Java 控制 App：主机工具构建/核验脚本、verify 脚本迁移、
  probe 11 工具与 app 全链路、控制 App 的 app/app_kind 中文状态与 test-target 扩展。

## 3. 单元与集成测试（真实执行）

```text
$env:GOEXPERIMENT='jsonv2'; go test ./internal/... ./cmd/... ./scripts/mcp-probe/ -count=1
ok  internal/android      10.694s
ok  internal/client        0.377s
ok  internal/device        0.067s
ok  internal/engine      238.521s
ok  internal/httpapi       1.721s
ok  internal/imaging       0.190s
ok  internal/mcp           2.161s
ok  internal/observation   0.087s
ok  internal/protocol      0.052s
ok  internal/store         2.914s
ok  cmd/device-mcp         1.732s
ok  scripts/mcp-probe     10.693s
```

- `go vet ./...` 通过；`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` 与
  `GOOS=windows GOARCH=amd64 ... go build ./...` 均 exit 0。
- 严格接入与反向对照：app 跨分支成员、未知 kind、`target` 同时含 `task_id`/`app`、
  `task_id=0`、显式 null、重复键、尾随第二值、`kill` 包名 target 均拒绝且不分配 P；
  `state_preserved`/`restarted`/`recents_cleaned` 的 null 与省略、find/list 空集合、
  close 部分完成、kill PID 数组、查询取消/超时摘要均有单测。
- 账本：v5 往返、v1–v4 迁移、伪版本证据拒绝、退休内嵌迁移池拒绝、容量边界、0.8.0 读取 v5
  受控拒绝且不改写字节；退休单份 >21 KiB 的压缩与 `dropped_unconfirmed` 计数有单测。
- probe：148 个用例，含 7 kind × 3 模式手写 expected 与文本 oracle、工具目录按名称集合比较、
  http-remote 配置拒绝与 app-lifecycle fake 流程。

## 4. 构建与部署（实测）

最终构建来自干净提交 `1d27416`（`source_dirty=false`）：

```text
release manifest  release=0.9.0 contract=9 android=11
tool catalog      count=11 sha256=7f71caf0a5348bf1c9474f8ca2445bda4ba05f132c62a80aa705cf4070521b6a
device-service    sha256=bdc3e68699b452c4e0d79d630ca42778296abcf4ca65ac797a0f41315b30b3c0 bytes=8585376
device-mcp        sha256=ad7b874b88c9a595e604101995231beb5458b402a07dd34491b6d6c4bb6ff1c6 bytes=9109664
control.apk       sha256=214a45d133e38f18cb9f0064aa061bb638b219bd10e93c088364d28cdc9997bb bytes=103965
u2.jar            sha256=28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3 bytes=3306229
applist.dex       sha256=63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8 bytes=4316
service.sh        sha256=4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0 bytes=3643

host tools        device-mcp.exe sha256=f82ac5e783d09e21ea7a9daf244e79087dd60e3eb07e0aa2ec26496502d0eea0 bytes=10007552
                  mcp-probe.exe  sha256=111a41a415b3af17c1acbec5bdc57a1edefa6b5b81cdefbc87b787d2ce9b2851 bytes=9615360
```

`scripts/verify-manifest.ps1`、`build-host-tools.ps1 -VerifyOnly` 均 PASS。部署输出核验：

- release/contract/versionCode 与 `/health` 一致；控制 App 安装为 `0.9.0/11`；
- 恢复屏障就绪（`can_start=true` 需要配对同步、可用性 ready、STOP 可用）；
- 手机 MCP HTTP 适配器 `127.0.0.1:18882`（主机转发 `18883`）的磁盘 SHA-256 与清单一致；
- 手机服务与手机适配器重启；主机两个 exe 已按清单精确替换。

## 5. 实机验证（实测）

1. `verify-m5.ps1 -SkipBuild -Transport Stdio` → `RESULT PASS`。覆盖 11 工具名称集合/描述/
   schema、目录指纹、`status can_start`、`start`（P/O/session/tree）、`app.find`、
   `app.find` 空集合、`app.list`（session/primary、display_id/ownership/foreground 与无
   outcome）、`app` 跨 kind 成员拒绝且不分配 P、`observe` 图片、`shell`、timeline、`inspect`、
   `act`、`end`、取消（查询不 end、有界结束、`operation_state=finished`）。
2. `verify-m5.ps1 -SkipBuild -Transport Http` → `RESULT PASS`（主机 HTTP 适配器、Origin/
   bearer、同一目录与 app 场景）。
3. `verify-m6.ps1` → `RESULT PASS`：上一发行 contract 8 在 control sync 与 `/api/status`
   均返回 `409 contract_mismatch`；STOP 最小载体、恢复、消息、控制视图与 SIGKILL/SIGTERM
   重启恢复通过。重启后保留 `cleanup=unconfirmed/execution=unconfirmed` 的诚实事实时，
   `can_start=true` 且旧会话不再 active。
4. `verify-p4.ps1 -Section App` → `app: PASS`：`app.find`、`app.list`（session/primary）、
   `app.open` 新建与复用、`claim`/`kill` 静态拒绝、`app.close`、后观察与 cleanup。
5. `scripts/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json
   -result-mode both -suite app-lifecycle` → `RESULT PASS`：远端服务身份/契约、401、
   Origin 403/200、11 工具目录与指纹、`start`、`find`、`list`、`open`、`list` 受控任务
   `owned`、`kill` 因前台拒绝（`kill_refused_foreground`）、`close` 执行、`end` 与取消。
   `claim` 因无受控主屏任务 SKIP。

部署与验证期间观察到一次设备显示创建竞态：`end` 后立即 `start` 时，旧虚屏还在释放，
控制 App 返回 `display_create_timeout`（服务按 `display_create_failed` 记录并保留事实）。
该路径不在本提案新增代码内；probe 与 `verify-p4` 在启动前增加了 5–6 秒有界的 cleanup
观察等待，之后的完整运行均通过。未把该等待当作准入规则改动服务。

最终干净产物的直接探针复核（`build/mcp-probe.exe` 与 `build/device-mcp.exe` 均来自
`1d27416` 的干净构建）：

- `mcp-probe -transport stdio -binary build/device-mcp.exe -config .state/mcp-config.json
  -result-mode both` → `RESULT PASS`（11 工具、catalog、start、find 空/非空、list
  session/primary、跨 kind 拒绝、observe 图片、shell、timeline、inspect、act、end、取消）。
- `mcp-probe -transport http-remote -config .state/probe-phone-config.json -result-mode both
  -suite app-lifecycle` → `RESULT PASS`（远端身份/契约、401、Origin 403/200、11 工具、
  start、find、list、open、受控 list owned、kill 前台拒绝、close、end、取消；claim SKIP）。

本记录在实现提交之后创建，因此之后 `verify-m5.ps1 -SkipBuild` 会因主机清单
`source_dirty=false` 与当前工作树（含本记录）不一致而拒绝；上述直接探针使用同一构建的
两个 exe 对已部署服务复核，未修改清单冒充匹配。重建主机清单需在该文档提交后执行
`build-host-tools.ps1`。

账本 v5 不可回退：只做了隔离 fixture 的受控拒绝验证（0.8.0 读取器拒绝 v5 且不改写字节），
未在设备上执行任何向 0.8.0 的回退。

## 6. 未执行项与缺口

- 设计源头 `mobile-use-platform` 的 docs-only 修订未执行；本仓库按提案语义实现并记录于此。
- 未重启用户的真实 MCP 宿主进程：只核验了宿主 exe 清单/哈希与手机适配器身份；宿主客户端
  需自行重启后重新发现 11 工具。
- `claim` 成功与 `handoff` 的实机闭环未执行（p4 只验证静态拒绝；probe 因无受控主屏任务
  SKIP）。相关单测覆盖了搬移、归属、落点与不确定分支。
- `kill` 的实际进程终止未在用户应用上执行；实机只验证了前台拒绝分支，进程清单/精确
  kill/后验由单测与 android 层表驱动测试覆盖。
- 未在设备上分别以 `text` / `structured` 运行 app 套件；`both` 已在设备覆盖两通道，
  单向模式由 probe 的 7×3 矩阵单测覆盖。
- 未运行 `-race`（Windows 需 CGO/gcc）；未运行 JVM 单测扩展（控制 App 仅编译与清单核验）。
- test-target 的第二任务、`:remote` 与 media FGS 运行时行为未验证（APK 已构建并含对应
  组件）。
