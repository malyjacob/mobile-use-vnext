# 项目实施状态

状态：2026-11-11。本页承载用户可见的阶段进度、已实现/已实测/未验证分层与未决项；
`docs/` 只保留正式架构、现行契约与运维说明。

## 1. 阶段

| 阶段 | 状态 | 说明 |
| --- | --- | --- |
| P0 立项与选型 | 完成 | Go 1.27/jsonv2、固定 u2.jar、Java 控制 App、账本 spike |
| P1 只读采集 | 完成 | 实机经 Go 服务完成层级采集 |
| P2 公共模型 | 完成 | engine 状态机、observation 树/delta、store、httpapi |
| P3 最小可写闭环 | 已收口（G3 部分证据） | start/observe/act/end、STOP、恢复；`scripts/verify-p3.ps1` 连续 PASS |
| P4 完整工具面 | 已打通（有限缺口） | 12 个工具端点已实现并实机走通；剩余证据缺口见下 |
| P5 MCP | 完成 | 图片链路、Go client、stdio/Streamable HTTP 双入口；实机验证见 P5 记录 |
| P6 控制呈现/整组交付 | 完成（G6 证据见 P6 记录） | 完整 control_view 呈现、消息中心交付、单一版本源、整组发布、破坏性发布与重启恢复演练 |

## 2. 工具面

| 工具 | 实现 | 实机 |
| --- | --- | --- |
| `start`/`status`/`observe`/`act`/`end` | 已实现 | 已验证（P3、P5、P6 记录） |
| `act` 坐标目标的点→节点证明 | 已实现 | 已实测：正常坐标动作 8/8 成功；同包状态变化、几何变化与导航 3/3 拒绝；真实 422 歧义、无节点回退、同包浮层、WebView 未构造（[记录](verification/act-coordinate-point-proof.md)） |
| `apps` | 已实现 | 已验证（Core 脚本） |
| `open_app` | 已实现 | 已验证：创建/复用/未知包/end/STOP 清理 |
| `inspect` | 已实现 | 已验证：整树/子树/查询 |
| `keepalive` | 已实现 | 已验证：续租/结束会话拒绝 |
| `notify` | 已实现 | 已验证：保存/重复/空正文；经控制同步进入 App 消息中心、系统通知与本地已读，确认后释放服务端队列 |
| `diagnose` | 已实现 | 已验证：无参/指定 O |
| `shell` | 已实现 | 已验证：正常/非零/自行124/超时/输出截断/STOP/重启恢复；命令结束不发布观察、同一 O 仍可用（`shell` 后 `act` 未变屏成功、改屏 `target_changed`、过期 `observation_expired`），实机 delta 回退见[记录](verification/shell-keeps-observation.md) |
| `observe`/`act` 的 `image=true` | 已实现（HTTP + MCP） | 已验证：JPEG 540×1200、MIME、无 base64、只读图分支由单元测试覆盖 |
| MCP stdio / Streamable HTTP | 已实现（`cmd/device-mcp`） | 已验证：工具目录/schema、认证/Origin、图片、取消、P 恢复；P6 回归重新拉起实际适配进程。默认部署同时安装并启动手机 `device-mcp`（手机 `18882/mcp`，主机转发 `18883`），`verify-m6` 检查 `server/discover`；主机侧适配器仍可选 |
| MCP 文本树块 | 已实现 | 已实测：0.4.2 手机 HTTP MCP 的 `start`/`inspect` 文本块，以及主机 stdio 探针的 `observe`/`act` 树块；外部宿主、token 对比、宿主截断与实机超预算未执行（[记录](verification/mcp-tree-text.md)） |
| MCP 结果模式 | 已实现 | 已实测：both/text/structured 的主机 stdio 闭环、手机 HTTP 三模式各 12 工具及取消恢复；大值仅合成测试，外部宿主/截断/race 未验证（[记录](verification/mcp-result-modes.md)） |
| Go client（`internal/client`） | 已实现 | 已验证：P 保留、断线、重复 P、取消、只读取消、图片 |
| 控制同步消息投递 | 已实现 | 已验证：App 私存、系统通知、消息中心列表/详情、本地已读、确认释放、跨服务重启保留 |
| 完整 control_view 呈现 | 已实现 | 已验证：Activity/通知/悬浮同源、失鲜、停止优先级、清理/权限/模式详情、主屏确认 |
| 单一版本源与发布清单 | 已实现 | 已验证：生成常量、manifest、SHA-256/大小、工具目录指纹、按清单部署 |
| 保留 App 数据的整组发布 | 已实现 | 已验证：0.3.0→0.4.0 破坏性发布、二次覆盖安装保留消息/已读/连接配置 |
| 结构化 `recovery` 入口（契约 3） | 已实现 | 已验证：成功省略、静态拒绝 `retry` 与同 O 重试、会话结束 `end`、收尾 `cleanup_pending`、契约 `update/none`、未知观察 `observe/P`、后观察失败 `cause`；`act uncertain` 与 `start` 撤销分支未实机构造 |
| UI 执行结束证据与旧账本恢复（契约 4） | 已实现 | 已实测：原 v1 execution-only 账本 checkpoint/手机重启恢复；真实 launch 关联失败时原 P uncertain、后观察成功、end 后准入恢复；0.4.3 整组部署与新 stdio 适配器通过。当前 Codex 旧进程需重启，验证边界见[记录](verification/ui-input-execution-evidence.md) |
| `act` 重验失败交付新观察（契约 6） | 已实现 | 已实测：probe 与 Settings 两个场景的 `target_changed` 都返回新 O 与 `retry/O/effective`，交付 O 为 `ready`，用新 P 一步重试成功、旧 O 返回 `observation_retired`；被拒的 `act` 零派发，`end` 后准入屏障恢复；主机 stdio 探针 12 工具 PASS（[记录](verification/act-revalidation-observation-delivery.md)） |
| 工作期限与未决输入的会话级封锁（契约 7） | 已实现 | 已实测：`act` 后静默 60 秒会话仍 `active`；未确认回执不再阻止 `observe`；受管 `shell` 超预算返回后立即 `observe` 成功；服务进程重启（不重启手机）后 `can_start=true` 且旧会话记 `service_restarted`；账本为版本 4；stdio 与手机 HTTP `mcp-probe` 均 PASS。真实回执未知注入与 `termination=unknown` 实机样本未执行（[记录](verification/remove-work-ui-deadline-lockdown.md)） |
| 显式重设会话租期并取消累计硬上限（契约 8） | 已实现 | 已实测：start 首观察后五分钟期限；120 分钟显式租约跨过 `started_at+1h`；普通 observe 不截短 30 分钟长租约；省略/1 分钟可缩短；同一 P 重放不再次计时；一分钟真实到期记 `lease_expired` 且 cleanup 完成；长租约下 STOP 立即 `revoked`。已发布 0.8.0 / versionCode 10，手机 HTTP 与主机 stdio `mcp-probe` PASS（[记录](verification/explicit-keepalive-duration.md)） |

## 3. 未验证 / 未实现

| 项 | 状态 |
| --- | --- |
| shell 超时/输出截断/终止证据/重启恢复 | 已验证 |
| shell 受管链在 STOP 后的清理提升 | 已验证（execution 升为 confirmed） |
| apps 预算截断/缓存失鲜并发 | 单元测试覆盖；实机未构造 |
| inspect 超限/截断 | 单元测试覆盖；实机树未超限 |
| MCP `-race` 竞态检查 | 未执行：Windows 宿主无 C 编译器，cgo 不可用 |
| MCP HTTP 多宿主并发/SSE 重连 | 未构造；单请求隔离、401/403 已实测 |
| MCP 外层整份结果丢失且宿主无 P | 无法注入；无 request-id 恢复工具，仅按已有 O/P 或 `status()` 恢复 |
| 页面明显变化后的图片内容差异 | 未构造；实机为同屏静态帧 |
| 倒序/迟到 `control_view.revision` 注入 | 实机未构造；App 只接受同实例更大的 revision，旧响应按代次丢弃（代码路径与单元测试覆盖，未单独实机注入） |
| 长时间 recovering/blocked 窗口 | 已确认 SIGKILL/SIGTERM 后 `can_start=false` 且无旧会话恢复；恢复通常在 App 实例变化释放显示后约 1–2 秒完成，未构造分钟级阻塞 |
| 外部 MCP 宿主缓存刷新 | 已验证：Codex 宿主实际重启后加载 `build/device-mcp.exe`，新进程路径与配置一致；12 工具 stdio 探针通过 |
| 旧 App + 新服务下的旧 App 界面 | 旧版本在控制视图为空时自身 `NullPointerException`（`ControlState.cleanupRisk`），旧 App 服务仍可发送独立 STOP；新版本已修复该空指针 |
| 通知频道关闭/权限撤销下的消息中心 | 代码路径检查；实机仅验证通知可用路径 |
| 后观察收敛采样（`start`/`act`/`open_app`） | 已实现并由单元测试覆盖判据、预算、撤销与图片读序；实机 A/B 未执行：`target_changed` 率、按档 `samples`/`stable` 分布与 P50/P95 需真机采样后才能校准三档取值（[提案](proposals/implemented/act-post-observation-settle.md)、[记录](verification/act-post-observation-settle.md)） |
| act 坐标目标点证明的真实歧义/无节点/同包浮层/WebView | 单元测试覆盖判据；实机未构造，原因见[记录](verification/act-coordinate-point-proof.md) §6 |
| `act` 交付观察的 delta 分支 | `base` 规则由单元测试覆盖；实机两次场景交付均为 `full`，未取得 delta 样本 |
| 真实 input 回执未知的实机注入 / shell `termination=unknown` 实机样本 | 未执行：发布二进制无注入面，打断 adbd 会同时切断恢复观察；受管链在本机总能确认超时终止。两条路径均由先红复现与单元测试覆盖（[记录](verification/remove-work-ui-deadline-lockdown.md)） |
| 实等会话累计超过一小时 | 未执行：由受控时间单测证明硬上限已删除；实机 120 分钟显式租约返回的绝对期限已超过 `started_at+1h`（[记录](verification/explicit-keepalive-duration.md)） |
| 实机 MCP 调用带 `duration_minutes` 的 keepalive | 未单独构造：MCP 转发由单测覆盖，实机 keepalive 语义由直接 HTTP 调用验证（[记录](verification/explicit-keepalive-duration.md)） |

## 4. 证据入口

- [P3 实机验证](verification/p3-real-device.md)
- [P4 实机验证](verification/p4-real-device.md)
- [P5 实机验证](verification/p5-real-device.md)
- [P6 控制呈现、消息中心与整组交付](verification/p6-real-device.md)
- [act 坐标目标点证明实机验证](verification/act-coordinate-point-proof.md)
- [shell 保留当前观察实机验证](verification/shell-keeps-observation.md)
- [结构化 recovery 入口实现与验证](verification/structured-recovery-entry.md)
- [MCP 文本树块实现、0.4.2 部署与实机验证](verification/mcp-tree-text.md)
- [工作期限与未决输入的会话级封锁实现与实机验证](verification/remove-work-ui-deadline-lockdown.md)
- [显式重设会话租期与取消累计硬上限实现、发布与实机验证](verification/explicit-keepalive-duration.md)
- [ref 连续性与静态页 delta 修复验证](verification/ref-stability-public-identity.md)
- [主屏多窗口层级修复与实机验证](verification/primary-display-multi-window.md)
- [工程决策](decisions.md)
- [工程流程](process.md)

## 5. 暂缓事项

- 第 3 节的验证缺口暂不单独补测；待其后按真实使用路径补齐证据。
- 当前部署为 `0.8.0`（`contract_version=8`、`android_version_code=10`），`can_start=true`；
  发布产物构建自提交 `9046cb1`，`source_dirty=false`；`device-service` 与
  `build/release-clean/manifest.json` 一致；手机 HTTP MCP 适配器二进制与清单一致，主机
  stdio 适配器已重建为 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，需由各 agent 宿主
  进程自行重启才能加载新的工具描述与目录指纹；chroot 内仍有一个旧 stdio `device-mcp`
  进程，同样需其宿主重启。旧 0.7.0 及更早产物仍作为不匹配验证材料。
