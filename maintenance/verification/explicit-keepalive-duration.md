# 显式重设会话租期与取消累计硬上限：实现与验证记录

- 实现提交：`f1da2d58ff8ba4ef33b7ea18442f7bdfa1ac3b95`（2026-11-11，含契约 7→8、
  发行 0.8.0 / Android versionCode 10）。
- 提案：[explicit-keepalive-duration.md](../proposals/implemented/explicit-keepalive-duration.md)。
- 环境：Windows 主机（Go 1.27.1，`GOEXPERIMENT=jsonv2`）构建；手机 Linux/arm64 chroot
  服务；Android 控制 App 0.8.0（versionCode 10）；设备序列 `17efb04a`；会话为 `virtual`
  模式；手机侧 HTTP MCP 适配器监听 `127.0.0.1:18882`。
- 本记录分开写“已实现 / 已单测 / 已发布 / 已实测”。单测通过不等于实机结论；未执行项写在
  §7，不把发布清单或一次成功响应当成全部验收。

## 1. 分诊与提案

分诊为取舍：现行规范本身把显式 `keepalive` 限制为单调续期并保留一小时累计硬上限，导致
长时间思考或等待用户时会话被撤销。用户确认采用五分钟 `start` 租期、显式 1～120 分钟
（省略 2 分钟）覆盖截止时间、普通活动按两分钟取最大值、取消累计硬上限；提案见上。

## 2. 已实现

| 提案条目 | 代码落点 |
| --- | --- |
| 严格可选时长、省略与显式非法值区分 | `internal/protocol/surface.go`（`KeepaliveRequest`、`EffectiveDurationMinutes`、`DecodeKeepalive`）、`internal/protocol/encode.go` |
| 显式覆盖而非单调续期；锁内取应用时 `now` | `internal/engine/keepalive.go`（`Keepalive`、`resolveKeepaliveLocked`） |
| `StartSessionTTL=5min`；初始化、激活、首个 full 观察发布 | `internal/engine/engine.go`（`Config`、`DefaultConfig`、`extendSessionLocked`）、`internal/engine/start.go`、`internal/engine/capture.go`（`publishTreeAttempt`） |
| 删除累计硬上限与双期限授权 | `internal/engine/engine.go`（删除 `SessionHardLimit`/`HardExpiresAt`）、`internal/engine/operations.go`（`sessionBeforeDeadline`） |
| 其他普通活动仍按 `max(expires_at, now+2min)` | `internal/engine/act.go`、`internal/engine/capture.go`、`internal/engine/shell.go`；`touchSessionLocked` |
| SDK 默认与显式时长方法 | `internal/client/client.go`（`Keepalive`、`KeepaliveWithDuration`、共用 `keepalive` 发送路径） |
| MCP schema、存在性透传与说明 | `internal/mcp/schema.go`、`internal/mcp/server.go`（`prepareCall`）；工具数保持 12 |
| 版本与文档 | `release/versions.json` 与生成常量；`docs/architecture.md`、`docs/interface.md`、`docs/client.md`、`docs/mcp.md`、`docs/control-protocol.md`、`docs/release.md`、`docs/runbook.md`、`docs/phone-operations.md`、`docs/integration.md`、`README.md` |
| 回归用例 | `internal/engine/lease_test.go`；`internal/protocol/surface_test.go`、`encode_test.go`；`internal/client/client_test.go`；`internal/mcp/server_test.go`、`result_parity_test.go` |

## 3. 已单测

| 命令 | 实际结果 |
| --- | --- |
| `go build ./...`（`GOEXPERIMENT=jsonv2`） | 退出 0 |
| `go vet ./...` | 退出 0 |
| `go test ./... -count=1` | 全部包 `ok`；`internal/engine` 206.860s、`internal/mcp` 4.380s、`internal/store` 3.760s、`scripts/mcp-probe` 11.029s、其余包通过 |

新增/更新的关键断言：

| 位置 | 断言 |
| --- | --- |
| `internal/engine/lease_test.go` `TestStartInitialLeaseFiveMinutes` | start 接纳后的初始期限落在五分钟窗口 |
| `lease_test.go` `TestKeepaliveDefaultCanShortenLease` | 省略时长把 30 分钟长租约覆盖为两分钟；O 输入截止与基线不变 |
| `lease_test.go` `TestKeepaliveRequestedMinutesResetThenOrdinaryActivityExtends` | 显式 1/2/120 分钟均从应用时重设；普通活动随后可延长到两分钟；重放同一 P 不再次计时 |
| `lease_test.go` `TestKeepaliveRejectsExplicitInvalidMinutesWithoutMutation` | 直接 engine 显式 -1/0/121 拒绝、不登记 P、不改期限；`now == expires_at` 拒绝 |
| `lease_test.go` `TestStartFirstObservationLeaseAndFailureKeepConcurrentReset` | 首观察发布采用五分钟；失败不额外续期、不回滚期间显式更新；重复/被拒 start 不重新给五分钟 |
| `lease_test.go` `TestOrdinaryLeasePassesFormerHardLimit` | 累计超过两小时仍可按普通两分钟规则续期，反向证明硬上限已删除 |
| `internal/engine/concurrency_test.go` | 过期租约不可复活；撤权后普通续期不生效 |
| `internal/protocol/surface_test.go`、`encode_test.go` | 省略/null/字符串/布尔/非整数/0/负数/121/重复键/大小写/尾随值拒绝；省略编码不补 null |
| `internal/client/client_test.go` | 省略、1、120 的请求体精确；显式非法值不发送；响应保留 P 与 `expires_at` |
| `internal/mcp/server_test.go` | MCP schema `integer 1..120 default 2`；省略/1/120 完整转发；null/0/121 严格拒绝；结果形状不变 |
| `internal/mcp/result_parity_test.go` | 12 工具目录指纹更新为 `7326317c…` |

## 4. 已发布与部署

| 步骤 | 实际结果 |
| --- | --- |
| `scripts/build-release.ps1` | `release=0.8.0 contract=8 android=10 source_commit=f1da2d5 source_dirty=False`；工具目录 `count=12 sha256=7326317c03b6a654da7e2df5a517bb4aaf24365f21a45551e9a1492b83666c31`；六个产物均产出 |
| `scripts/verify-manifest.ps1` | PASS；6 个产物、工具目录 12/`7326317c…`、`source_dirty=false` |
| `scripts/deploy.ps1 -SkipBuild` | PASS；`/health` 返回 release `0.8.0` / contract `8`；`can_start=true`；安装后 App `0.8.0` / versionCode `10`；手机 HTTP MCP 适配器运行二进制哈希与清单一致 |
| 手机服务二进制 | `device-service` SHA-256 `675efe779ebe850911789cd156ba7e9223c32e033a331356831f3e1e90cff1aa` |
| 手机 MCP 二进制 | `device-mcp` SHA-256 `59bba5349616679747c7d2c2923b64806f00607957a27f91bc06a14120b8b120` |
| 控制 APK | `control.apk` SHA-256 `658f421ab5379d01f466a20810739962252027d1402c8e2315296b0ca9006a49` |
| 主机 MCP 适配器 | `go build -trimpath -buildvcs=false -o build/device-mcp.exe ./cmd/device-mcp`；14,417,408 字节，SHA-256 `640b9da8d9a52636b391b767b01771128b668af12e6843c9992e662d17b62d68` |
| 主机探针 | `go build -trimpath -buildvcs=false -o build/mcp-probe.exe ./scripts/mcp-probe`；13,697,024 字节，SHA-256 `7caf64a2edec05d681684a3cf3f1c646f2e5976da0ef7348c52384808564dea7` |

部署输出同时警告：chroot 内仍有一个旧 `device-mcp` stdio 进程；默认 HTTP 适配器已按清单
重启并核验，但该宿主托管的 stdio 适配器必须由其宿主进程重启后才会加载新二进制。

## 5. 已实测（同一设备，virtual 模式）

通过 `POST /api/<tool>` 直接调用（`X-Mobile-Use-Contract: 8`，服务端生成 P；需要重放时
显式使用同一 P）。时间为 UTC；下表的分钟数只由响应 `session.expires_at` 计算，不实等一小时。

| 检查 | 实际结果 |
| --- | --- |
| start 初始化/首观察五分钟 | `started_at=2026-11-11T14:55:52.523112418Z`，`expires_at=2026-11-11T15:00:55.117160125Z`，差 302.6 秒。实现从首观察成功发布时起算五分钟，因此可倒推发布发生在启动后约 2.6 秒；返回期限不是初始化时的 `started_at+300s`，而是发布时 `now+300s` |
| 显式 120 分钟 | `keepalive(Pverify-start-manual, 120)` 从应用时 `14:53:15Z` 返回 `16:53:15Z`；超过 `started_at+1h` |
| 普通 observe 不截短长租约 | 30 分钟重设得 `15:23:25Z`；随后 `observe` 返回新的完整观察，查询会话仍为 `15:23:25Z` |
| 省略时长重设为两分钟 | 从 `14:53:15Z` 的长租约重设为 `14:55:15Z` |
| 显式 1 分钟可缩短 | 同一会话重设为 `14:54:15Z`；无需等待原长租约到期 |
| 同一 P 重放不再次计时 | 2 秒后以同一 P 和不同 `duration_minutes=120` 重放，仍返回 `14:54:15Z`、`outcome=executed` |
| 缩短后普通活动可再次延长 | 1 分钟租约后的 `observe` 使会话查询值为 `14:55:17Z`（约当前时 + 2 分钟） |
| 一分钟真实到期 | `keepalive(...,1)` 得 `14:54:31Z`；期满后查询 `state=ended`、`reason=lease_expired`；`cleanup.state=complete`，display/tasks 均 `confirmed` |
| 长租约下 STOP 立即生效 | 会话 `expires_at=16:54:54Z` 时，独立 `/v1/control/stop` 返回 `state=revoked`；立即查询 `state=ended`、`reason=operator_stop`；随后 `can_start=true`、`cleanup=complete` |
| 结束与最终准入 | 验证后 `end` 的会话为 `client_end`，`cleanup=complete`、`display=confirmed`、`can_start=true` |

## 6. MCP 探针

主机 stdio（新 `build/device-mcp.exe`，探针实际拉起）：

```text
./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe \
  -transport stdio -result-mode both -timeout 180
```

结果：`RESULT PASS`；12 工具、schema 对象/必填、`status`、错误投影、start/observe/apps/
shell/timeline/inspect/act/end/cancel 均 PASS。

手机 HTTP（`http://127.0.0.1:18883/mcp`）：

```text
./build/mcp-probe.exe -config .state/mcp-config-18883.json -transport http \
  -result-mode both -timeout 180
```

结果：`RESULT PASS`；401/403/Origin 边界、12 工具及同一套工具检查全部 PASS。工具目录与
发布清单一致：12 个工具，指纹 `7326317c…`。

两次首次探针的瞬态失败如实记录：

- stdio 首次在 start 返回 `control_revoked`（部署后首次准入，App 同步尚未稳定）；随后
  `status can_start=true`，原命令重跑 `RESULT PASS`。
- HTTP 首次在取消检查的第二次 start 返回 `display_create_failed`（紧接 end 后立即再
  start，虚拟屏清理尚未完成）；等待清理完成后重跑 `RESULT PASS`。
- 两次失败都没有发生在 keepalive/租期路径，也不能归因于本次语义改动；这里不隐去。

MCP 的 `keepalive.duration_minutes` 省略/1/120 完整转发由 `internal/mcp/server_test.go`
的内存 MCP 服务覆盖；本次没有在实机上再单独发一次带 `duration_minutes` 的 MCP 工具调用，
实机 keepalive 语义由上表的直接 `/api/keepalive` 请求验证。

## 7. 未执行与边界

| 项 | 说明 |
| --- | --- |
| 实等会话累计超过一小时 | 不实等。取消硬上限由 `TestOrdinaryLeasePassesFormerHardLimit` 的受控时间证明；实机 120 分钟显式租约已返回超过 `started_at+1h` 的绝对期限 |
| 实机 MCP 的 keepalive 时长调用 | 未单独构造；MCP 转发由单测覆盖，实机语义由 HTTP 调用验证，MCP 探针验证工具目录与 schema |
| 旧 stdio MCP 进程 | 部署警告 chroot 内仍有旧 stdio `device-mcp`；默认 HTTP 适配器已更新并核验，旧进程需宿主重启，不在本次部署脚本内强杀 |
| 主机宿主进程刷新 | 已重建 `build/device-mcp.exe`，但 DSH/其他宿主是否已重新拉起该文件不在本次交付内；宿主必须重启实际进程才会重新发现工具 |
| 跨服务/App 重启保留界面 | 非本提案目标；仍按原恢复与收尾规范处理 |

## 8. 结论

实现、单测、整组发布、手机部署、主机适配器/探针重建、MCP 探针与最小实机语义检查均已完成；
显式 keepalive 可延长、缩短、省略重设，普通活动不再截短长租约，累计一小时硬上限已删除，
start 五分钟租期与到期/STOP 边界均按契约工作。未执行项已列明，未把受控单测或一次成功响应
写成实机全量结论。
