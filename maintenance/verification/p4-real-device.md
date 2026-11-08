# M4/G4 实机验证记录（完整工具面，进行中）

状态：2026-11-09。本页只记录实际执行过的路径与真实结果；未执行的明确写“未验证”，
不把单元测试或设计推演写成实机结论。原始日志、节点树、坐标、令牌与命令输出不入库。

## 1. 版本与环境

| 项 | 值 |
| --- | --- |
| 代码提交 | `e5d0d15056a726eb4b9312f98bff35ad462eab4c`（基础检查点 `3f84918`） |
| 服务构建 | `0.2.0-m4`；最终 shell 恢复修复后 SHA-256 `a13dea122466d7b343c235d4726ed407105150a5933021e00cea24d6c0641033`（早期检查点 `f414a31d…`） |
| 契约 | `contract_version = 1` |
| 设备 | `24117RK2CC`，Android 16（API 36），arm64，Magisk + Debian 13 chroot |
| chroot / 服务 | `/data/local/chroots/debian13`；`/opt/mobile-use-vnext`；`18880`；主机 `18881` |
| 配对 App | `com.mobileuse.vnext`（`0.4.0-p3`） |
| 固定制品 | `u2.jar`；`applist.dex`（SHA-256 `63818f32…`） |

## 2. 实际执行的命令与结果

### 2.1 构建与部署

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build.ps1 -Version 0.2.0-m4
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/deploy.ps1 -SkipBuild -ReleaseVersion 0.2.0-m4
```

结果：构建成功并打印上述 SHA-256；部署后 `/health` 返回
`contract=1 release=0.2.0-m4`，`/api/status` 返回 `can_start=true`。

### 2.2 全量单元测试与静态检查（Windows，非实机证据）

```bash
GOEXPERIMENT=jsonv2 go test ./... -count=1
GOEXPERIMENT=jsonv2 go vet ./...
```

结果：全部包 PASS（`internal/android`、`internal/engine`、`internal/observation`、
`internal/protocol`）；`go vet` 无输出。`internal/httpapi`、`internal/store`、
`internal/eventlog`、`internal/device` 仍无包内测试。

### 2.3 P3 回归

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-p3.ps1 -SkipBuildProbe
```

结果：`PASS p3 writable loop`。`start/full → observe → tap → 中文 text（u2 精确读回）→ end`；
`cleanup=complete`、`display=confirmed`、`tasks=confirmed`、`recents=not_applicable`、
`can_start=true`；SurfaceFlinger 无 `MobileUseAgent` 显示。M4 构建上 P3 未退化。

### 2.4 `scripts/verify-p4.ps1` 分段结果

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-p4.ps1 -Section Core
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-p4.ps1 -Section OpenApp
```

结果：两次均 `PASS verify-p4`。

- Core：`apps({})` 返回 `items/total/truncated/captured_at` 且无 `outcome`；包名查询命中
  probe；未知成员返回 422；`diagnose({})` 返回 `coverage=own_records`；`notify` 返回
  `executed/message_id/saved=true/notification.state=unavailable`。
- OpenApp：`start` + `open_app(com.mobileuse.probe)` 返回 `executed`，任务落在虚拟屏
  （实机观察到的 `display_id` 为 132/133/137 等本次运行值），后置观察为 complete/full；
  第二次 `open_app` 返回 `reused=true` 且 task id 不变；未知包返回 `409 app_not_found`
  且未消费 O；`end` 后 `cleanup=complete`，`tasks=confirmed`、`recents=confirmed`、
  `display=confirmed`、`can_start=true`，SurfaceFlinger 无 `MobileUseAgent`；
  用户 STOP（显式组件 `-n com.mobileuse.vnext/.ControlService -a …STOP`）后同样
  `cleanup=complete` 且 `can_start` 恢复。
- Shell（`-Section Shell`）：`shell: PASS`。正常命令 `exited/0` 且有 full 后观察；`root=true`
  的 `id -u` 返回 `exited/0`、输出 `0`（设备 root 身份），后观察 complete；非零退出
  `exited/7`；命令自行 `exit 124` 保持 `exited/124`（不是 timeout）；大输出返回恰好 65536
  字节、`truncated=true`；`sleep 40` 在约 30s 后 `termination=timeout`、`exit_code=null`、
  仍有后观察；STOP 在命令运行中返回 `executed/cancelled`（HTTP 200），STOP 调用本身
  小于 5s，cleanup 最终 complete 且 `can_start` 恢复，无残留 display/runner。
- Shell 重启恢复（`-Section ShellRecovery`）：`shell recovery: PASS`。SIGKILL 服务并重启后，
  旧 runner 被精确终止，日志出现 `shell.previous_instance` 与 `shell.barrier_confirmed`、
  `recovery.complete`；`can_start` 先保持 closed 后恢复 true，runner 目录被移除。
  `status(旧shellP)` 返回 `finished/previous_instance/uncertain`（HTTP 504 是对该 uncertain
  结果的正常映射），会话 `ended/service_restarted`，cleanup complete；旧命令没有被重放。

### 2.5 逐工具实机结果

| 工具 | 操作 | 实测结果 |
| --- | --- | --- |
| `apps` | `{}`、`{"query":"probe"}`、`{"query":"设置"}`、未知成员 | 返回完整条目与 `captured_at`；中文/包名查询均命中；未知成员 422 |
| `open_app` | probe 首次/复用、未知包、end 清理、STOP 清理 | 首次 `reused=false` 且 task/display 有值；复用 `reused=true` 同 task；未知包 409 `app_not_found`；清理与 STOP 均 `tasks/recents/display=confirmed` |
| `inspect` | 整树、指定 `node`、label 查询、节点+查询同现 | 整树 `scope=tree`；子树 `scope=subtree` 且 ref 一致；label 查询 `total=1`；同现返回 422 |
| `keepalive` | active O、ended O、重复 P | active 返回 `executed` 与新 `expires_at`；ended 返回 `session_ended`/next=end；重复 P 返回原视图 |
| `notify` | 正常、重复 P、空正文 | 正常 `executed`/`M1`/`saved=true`/`unavailable`；重复 P 不再保存；空正文 422 |
| `diagnose` | 无参、指定 O | 无参 `own_records`；指定 O `original_record` 并带受控 issue/timing |

## 3. 未验证 / 待补

| 项 | 状态 | 说明 |
| --- | --- | --- |
| `shell` | 已验证 | 正常/非零/自行124/超时/输出截断/STOP 均已实机通过 |
| shell 重启恢复 | 已验证 | SIGKILL 后 runner 精确终止、屏障先闭后开、旧 P 为 previous_instance、目录移除 |
| open_app 并发同 O、重复 P、停止竞争 | **未验证** | 单元测试待补；本轮实机只覆盖顺序创建/复用/清理/STOP |
| apps 预算截断、缓存失鲜/并发 | **未验证（实机）** | 单元测试覆盖 32 KiB 预算与缓存；实机未构造超限清单 |
| inspect 32 KiB 截断/超大单位 | **未验证（实机）** | 单元测试覆盖；实机树未超限 |
| notify App 消息中心交付 | **不可用** | M4 未接 App 消息中心；`notification.state=unavailable` |
| 12 工具 schema/错误码统一收口 | 已完成 | 路由、请求/结果字段、错误码与 HTTP 映射已核对；文档复核见提交记录 |
| `go test -race` | **不可用** | 生产目标 `CGO_ENABLED=0`；Windows 未跑 race |

## 3.1 开发机账本修复说明

上一轮 shell start-tick bug 使测试会话留下 `cleanup.execution=unconfirmed` 且无 shell 记录，
服务恢复持续 `old_resource_unconfirmed`。确认无 `MobileUseAgent` display、无
`/data/local/tmp/mobile-use-shell/*`、目标 display 无任务后，把旧账本
`recovery-ledger.json` 复制/移动为 `recovery-ledger.json.dev-repair-20261109-022327` /
`.repaired-20261109-022327` 并重启。该操作只用于恢复开发测试机，不是产品允许的绕过路径；
根因（空 start ticks 被当无效）已在 `internal/android` 修复并加测试。

## 4. 结论

- 12 个工具端点均已在真机走通；shell 的正常、非零、自行 124、超时、64 KiB 输出截断、
  STOP 取消与 SIGKILL 重启恢复均有实机证据。
- open_app 的创建、复用、未知包拒绝、任务/recents/display 清理与 STOP 清理均已验证。
- P3 回归在同一最终构建上 PASS，未见退化。
- 未验证项已在第 3 节列明：apps/inspect 的实机预算截断、open_app 并发与停止竞争、
  Android 重启后的恢复、notify 的 App 消息中心交付、`-race`。这些不等于 12 工具面
  未打通，但仍是签收前的剩余证据缺口。
