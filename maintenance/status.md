# 项目实施状态

状态：2026-11-21，0.24.0（契约 21）MCP 无损结果与树文本完成整组发布、虚拟屏双入口逐字段对照和独立探针验证（[记录](verification/mcp-lossless-text-format.md)）。
0.22.0（契约 20）把会话显示屏身份加入 `observation.screen.display_id`，
并把受管 shell 的显示屏语义写入契约与工具说明，整组部署后在虚拟屏会话实测（issue #11）；
13 个 MCP 工具在虚拟屏模式完成一轮工具面覆盖测试，补齐 claim/handoff
成功路径、`effect=observed`、`act` 交付 delta 与 `close`/`kill` 授权守卫的实机样本（§3）；
0.21.0（契约 20）的 MCP 树文本无损简写（`tree v3`/`inspect v3`）已实现、
整组部署并完成双入口独立探针与实机最小闭环；2026-11-19，契约 20 的默认观察树坐标省略已实现、
整组部署并完成实机最小闭环；
契约 19 的只读截图工具 snap 已实现、整组部署并完成实机最小闭环；契约 18 的 text 写入确认、
错误详情与恢复投影已完成有限实机验证。本页承载用户可见的阶段进度、已实现/已实测/未验证分层与未决项；
`docs/` 只保留正式架构、现行契约与运维说明。

## 1. 阶段

| 阶段 | 状态 | 说明 |
| --- | --- | --- |
| P0 立项与选型 | 完成 | Go 1.27/jsonv2、固定 u2.jar、Java 控制 App、账本 spike |
| P1 只读采集 | 完成 | 实机经 Go 服务完成层级采集 |
| P2 公共模型 | 完成 | engine 状态机、observation 树/delta、store、httpapi |
| P3 最小可写闭环 | 已收口（G3 部分证据） | start/observe/act/end、STOP、恢复；`scripts/verify-p3.ps1` 连续 PASS |
| P4 完整工具面 | 已打通（有限缺口） | 13 个工具端点已实现并实机走通；剩余证据缺口见下 |
| P5 MCP | 完成 | 图片链路、Go client、stdio/Streamable HTTP 双入口；实机验证见 P5 记录 |
| P6 控制呈现/整组交付 | 完成（G6 证据见 P6 记录） | 完整 control_view 呈现、消息中心交付、单一版本源、整组发布、破坏性发布与重启恢复演练 |
| S0–S10 观察连续性、结构差分与动作重验 | 已实现 / 实机有限验证 / 归档记录定稿 | `900e640`、`bb43c19`、`38f47e3`、`049583d` 完成实现；0.15.0/契约 15/versionCode 17 完整发布；用户于 2026-11-16 接受 D01–D06 证据缺口（[记录](verification/observation-continuity-and-revalidation.md)） |

## 2. 工具面

| 工具 | 实现 | 实机 |
| --- | --- | --- |
| `start`/`status`/`observe`/`act`/`end` | 已实现 | 已验证（P3、P5、P6 记录） |
| `act` 坐标目标的点→节点证明 | 已实现 | 已实测：正常坐标动作 8/8 成功；同包状态变化、几何变化与导航 3/3 拒绝；真实 422 歧义、无节点回退、同包浮层、WebView 未构造（[记录](verification/act-coordinate-point-proof.md)） |
| `apps` | 已实现 | 已验证（Core 脚本） |
| `open_app` | 已实现 | 已验证：创建/复用/未知包/end/STOP 清理 |
| `app` 的 `claim`/`handoff`/`close`/`kill` | 已实现 | 已实测（0.21.0，virtual）：`handoff` 虚拟屏→主屏 `display_id=0`、`state_preserved=true`、`foreground_restore=confirmed`；`claim` 主屏→虚拟屏 `ownership=borrowed` 且应用内页面状态保留；`close` 默认拒绝借入窗口、显式 `allow_borrowed=true` 后 `recents_cleaned=true`；`kill` 默认拒绝播放/前台服务、显式 `allow_stop_playback=true` 后目标 PID 全终止。`kill` 后残留 2 个同包进程且结果自报 `restarted=true`，本轮只如实记录、未定性（[记录](verification/mcp-tool-surface-coverage.md) §5.2） |
| `inspect` | 已实现 | 已验证：整树/子树/查询 |
| `wait_for` | 已实现（契约 12，第 12 个工具） | 已实测（virtual 模式）：`query`+`stable=true` 提前命中 1651ms（`stable/3/stable`）、`absent_query` 首帧命中 469ms、纯 sleep+image 4338ms（`unknown/0/not_sampled`，图片 540×1200 complete）、等待期 `end` 撤销后约 50ms 返回 `409 control_revoked` 且无迟到 `O`；主机与手机侧探针均 `PASS`。primary 模式表征与 App STOP 按钮未执行（[记录](verification/wait-for.md)） |
| `keepalive` | 已实现 | 已验证：续租/结束会话拒绝 |
| `notify` | 已实现 | 已验证：保存/重复/空正文；经控制同步进入 App 消息中心、系统通知与本地已读，确认后释放服务端队列 |
| `diagnose` | 已实现 | 已验证：无参/指定 O |
| `shell` | 已实现 | 已验证：正常/非零/自行124/超时/输出截断/STOP/重启恢复；命令结束不发布观察、同一 O 仍可用（`shell` 后 `act` 未变屏成功、改屏 `target_changed`、过期 `observation_expired`），实机 delta 回退见[记录](verification/shell-keeps-observation.md)。受管命令不绑定会话显示屏、`input` 默认作用于 display 0，该语义与 `screen.display_id` 已写入契约与工具说明，并在 0.22.0 上整组部署验证（[记录](verification/managed-shell-display-semantics.md)） |
| `observe`/`act` 的 `image=true` | 已实现（HTTP + MCP） | 已验证：JPEG 540×1200、MIME、无 base64、只读图分支由单元测试覆盖 |
| `snap`（契约 19，第 13 个工具） | 已实现（HTTP + Go client + MCP + 控制 App 文案） | 已实测：`start → keepalive(1) → snap(O) → status(O/P) → observe(image=true) → end` 闭环 PASS；JPEG 540×1200 与元数据一致，原 O 保持 `ready`/`effective` 且 `expires_at` 不变，snap 不递增 revision，重复 P/`status(P)` 无图摘要；主机 stdio/http、手机 HTTP 远端与 text/structured 探针均 PASS。实机 STOP/取消精确竞态与截图进行中文案未构造（[记录](verification/snap.md)） |
| MCP stdio / Streamable HTTP | 已实现（`cmd/device-mcp`） | 已验证：工具目录/schema、认证/Origin、图片、取消、P 恢复；P6 回归重新拉起实际适配进程。默认部署同时安装并启动手机 `device-mcp`（手机 `18882/mcp`，主机转发 `18883`），`verify-m6` 检查 `server/discover`；主机侧适配器仍可选 |
| MCP 文本树块 | 已实现 | 已实测：0.4.2 手机 HTTP MCP 的 `start`/`inspect` 文本块，以及主机 stdio 探针的 `observe`/`act` 树块；外部宿主、token 对比、宿主截断与实机超预算未执行（[记录](verification/mcp-tree-text.md)） |
| MCP 树文本无损简写（`tree v3`/`inspect v3`，0.21.0） | 已实现 / 已实测（有限） | `9009599`：省略可还原的 `view`/`text` 角色并简写匿名默认 `view` 链 `[refs]`，外层 `result v2` 与共同契约 `20` 不变；0.21.0/契约 20/versionCode 23 整组构建、双清单核验、手机覆盖部署、主机两 exe 磁盘替换及主机 `stdio`/手机 `http-remote` 独立探针均 PASS；受控 `com.mobileuse.probe` 页最小闭环 `both` 模式 0 mismatch（命中 `shorthand_view_chain`、`omitted_text`、detailed 下 `omitted_view`/`explicit_text`、`full`/`delta` 与 `inspect v3`）；依赖方托管进程由用户自行重启切换，未命中边缘分支由单测与反向对照覆盖（[记录](verification/compact-node-role-text.md)） |
| 默认观察树坐标省略（契约 20） | 已实现 / 已实测（有限） | `6838423`：动作位置优先，无动作 `text` 与匿名节点省略矩形，重复与 1% 面积判据；0.20.0/契约 20/versionCode 22 整组构建、清单核验、覆盖部署，主机两个 exe 替换与两条独立探针 PASS。实机受控页三帧策略复算 0 mismatch、文字坐标全隐藏、`inspect` 补回原矩形、ref tap executed；三份附件回放与 85% 门槛对照未执行（[记录](verification/compact-observation-bounds.md)） |
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
| 受控滚动、raw swipe duration 与观察质量/效果（契约 10） | 已实现 | 已部署 0.10.0/契约 10/versionCode 12；Settings 实机 5 次受控 scroll 全部 `executed`、0 次 `target_changed`，quality 均 stable；effect 得到 `not_observed`/`unknown`，尚未取得实机 `observed` 或跨应用样本（[记录](verification/semantic-scroll-observation-quality.md)） |
| 公共树表示与交付预算（契约 11） | 已实现 | 已实测（部分）：0.11.0/契约 11/versionCode 13 整组构建、清单核验、手机覆盖安装与私有数据保留；实机契约 10 拒绝、最小 STOP 接纳、`start→full`、同基线空 delta、inspect 插入不改基线、query 与 end；stdio 与手机 HTTP 两个 MCP 入口的 both/text/structured 探针均 PASS。未执行：`verify-m6`（用户排除）、真实 Capture 压缩比/token/耗时/进程峰值（用户排除）、`act` 重验失败交付的实机样本（[记录](verification/compact-node-tree-presentation.md)） |
| 受控滚动中心线安全区间、原段重验与联动视口效果（契约 13） | 已实现 | 已部署 0.13.0/契约 13/versionCode 15；契约边界实测（旧 12 与缺头均 `409 contract_mismatch`、最小 STOP 独立、`13` 受理）；Settings 7 次垂直 scroll 全部 `executed`、0 次 `target_changed`，取得 4 次实机 `observed`（`anchors=17`/`coverage=80`）与 6 次 `not_observed`；X 首页复现 `2242↔2400` 容器伸缩但受内容换血限制仍为 `unknown`；Zhihu/X 嵌套作用域取得真实 `422` 且零采集零派发。联动栏下的实机 `observed` 与合成覆盖层页未取得（[记录](verification/scroll-safe-trajectory-and-viewport-effect.md)） |
| tap/long_press 子树安全落点与结构化引导（契约 14） | 已实现 / 已实测（有限） | 已部署 0.14.0/契约 14/versionCode 16；静态子树规划、固定点重验、R1 原子引导、`occluding_ref` 边界及嵌套详情所有权已实现并单测；实机临时复合卡片验证正文引导 `nearest_actionable_ref=n21`、`safe_point={540,331}`、`safe_carrier_ref=n22`，独立 O 上卡片 tap ×3 与 long_press 均与内按钮 0 次隔离；契约 13 拒绝/14 受理、恢复屏障与最小 STOP 通过。外部 MCP 宿主未操作，手机 stdio 旧进程需宿主重启；`occluding_ref` 专门页面与活动会话 STOP 未单独实机构造（[记录](verification/act-tap-safe-scope-and-guidance.md)） |
| Agent 输出、instructions 与 wait 终帧复用（契约 16） | 已实现 / 已实测（有限） | `07c85b6` + `792c973`：device-mcp 默认 text、server instructions 集中与 12 描述去重、wait timeout/max_samples 复用最新新鲜帧；0.16.0/契约 16/versionCode 18 整组发布，discovery 元数据 `25076` 严格低于基线 `36250`，目录指纹 `c5fdc545...`；主机 stdio text/structured/both、主机 HTTP 与真实服务 smoke/app-lifecycle PASS。慢 hierarchy wait、真实宿主/模型 #20 与 Instructions 轨迹未执行（[记录](verification/agent-output-and-wait-acquisition.md)） |
| 统一 reference 与受控动作契约（契约 16） | 已实现 / 已实测（有限） | `d529ee7` + `792c973` 完成 #17/#18/#19/#23：act/inspect/app 统一 `reference`、扁平 app Schema 与 `open.target.app`、ref bubble 默认一次最近交互祖先提升、text 默认 `press_enter` 与 `text_input`；编码器与服务端共享封闭 kind 矩阵。0.16.0 已整组发布并部署；实机 text 默认/显式 false、status/重复 P、旧字段/坐标 bubble/旧 app 拒绝、bubble 提升与 end/cleanup 已执行，docs 独立审计 CLEAN。实机 STOP/ENTER 回执注入、claim/handoff 与真实宿主 Instructions 回放未执行（[记录](verification/agent-reference-and-action-contract.md)） |
| act 静态目标拒绝分类（契约 17） | 已实现 / 已实测（有限） | `a02d305`：三码 `invalid_target`/`unsupported_node_action`/`no_safe_point`、封闭 reason、定向 details 与 3×3 建议点；0.17.0/契约 17/versionCode 19 整组构建、清单核验与覆盖部署，App/服务/手机 HTTP 适配器版本匹配。实机 10 类静态拒绝、O 保持 ready、建议点稳定帧执行、旧契约 409/最小 STOP 旧契约受理、主机 stdio 与手机 HTTP 探针 PASS；外部 `codex.exe` 宿主未重启，`context`/`no_geometry` 实机页面与实机大树性能未取得（[记录](verification/act-static-error-taxonomy.md)） |
| text 写入确认、详情与恢复（契约 18） | 已实现 / 已实测（有限） | `b0e54a4`：以一次 set 的同节点刷新值精确相等为写入证据，已聚焦 ref 跳过焦点点击，删除写前 Get 与写后重采集/重绑定/GetText；ENTER 前独立前台/读数门禁；text 详情 stage/reason/dispatch/count；finished uncertain 先 observe/P(retired) 并叠加实时阻塞。0.18.0/契约 18/versionCode 20 整组构建、清单核验与覆盖部署；实机 ref 输入 4 种值/空串/ENTER PASS，换行被应用格式化时 409 `verify/readback_mismatch` 且不发送 ENTER；手机 HTTP MCP 远端探针 PASS。异步/重建控件、多行成功、STOP 实机注入与 10 次重复未执行（[记录](verification/text-write-verification-and-recovery.md)） |
| 观察连续性、结构差分与动作重验（契约 15） | 已实现 | 已实测（有限）：净化/严格三态、五阶段连续性、结构差分与双应用器、动作层级重验及消费者同步已落地；D01 contract 15 三轮 pass，D02 三轮无 fail但缺 native scroll，D03 24 pass/3 fail，D04 3 fail/15 gap，D05 最新 1 pass/1 fail，D06 在 `38`/`049` 各三轮 pass；S6 最终 61 个五样本子项、305 行。缺口已接受（[记录](verification/observation-continuity-and-revalidation.md)、[基准](verification/observation-continuity-benchmarks.md)） |

## 3. 未验证 / 证据缺口

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
| 外部 MCP 宿主缓存刷新（历史版本） | 已验证：Codex 宿主实际重启后加载 `build/device-mcp.exe`，新进程路径与配置一致；12 工具 stdio 探针通过 |
| 旧 App + 新服务下的旧 App 界面 | 旧版本在控制视图为空时自身 `NullPointerException`（`ControlState.cleanupRisk`），旧 App 服务仍可发送独立 STOP；新版本已修复该空指针 |
| 通知频道关闭/权限撤销下的消息中心 | 代码路径检查；实机仅验证通知可用路径 |
| 后观察收敛采样（`start`/`act`/`open_app`） | 已实现并由单元测试覆盖判据、预算、撤销与图片读序；实机 A/B 未执行：`target_changed` 率、按档 `samples`/`stable` 分布与 P50/P95 需真机采样后才能校准三档取值（[提案](proposals/implemented/act-post-observation-settle.md)、[记录](verification/act-post-observation-settle.md)） |
| act 坐标目标点证明的真实歧义/无节点/同包浮层/WebView | 单元测试覆盖判据；实机未构造，原因见[记录](verification/act-coordinate-point-proof.md) §6 |
| `act` 交付观察的 delta 分支 | 已验证（0.21.0，virtual）：`act`（`tap`/`text`/`long_press`/`swipe`/`key`/`scroll`）、`wait_for`、`observe(wait_ms)` 与一次 `target_changed` 拒绝的交付观察均为 `delta`，`base` 指向最近一张 `full`；取得 `~`/`+`/`-`/`children`/`=` 五类变更行，以及清空输入框后相对基准回落的 `changes=0`。强制 `full=true` 时得 `unknown/0/not_sampled`，`app` 的各 kind 交付仍为 `full`，两者作对照。0.16.0 两次场景仅得 `full` 的记录由本条取代（[记录](verification/mcp-tool-surface-coverage.md)） |
| `screen.display_id` 与 `shell` 显示屏语义 | 已实机：0.22.0/契约 20/versionCode 24 整组构建、清单核验、覆盖部署；`observation.screen`、`app.display_id`、`image.screen` 三处 `display_id` 一致（实测 119），不带 `-d` 的 shell 滑动仍静默无效、带 `-d` 生效；主机 stdio 与手机 HTTP 探针均 PASS。主屏 `display_id=0` 样本与 `-d` 的其它子命令未取证（[记录](verification/managed-shell-display-semantics.md) §7、§8） |
| 真实 input 回执未知的实机注入 / shell `termination=unknown` 实机样本 | 未执行：发布二进制无注入面，打断 adbd 会同时切断恢复观察；受管链在本机总能确认超时终止。两条路径均由先红复现与单元测试覆盖（[记录](verification/remove-work-ui-deadline-lockdown.md)） |
| 实等会话累计超过一小时 | 未执行：由受控时间单测证明硬上限已删除；实机 120 分钟显式租约返回的绝对期限已超过 `started_at+1h`（[记录](verification/explicit-keepalive-duration.md)） |
| 实机 MCP 调用带 `duration_minutes` 的 keepalive | 未单独构造：MCP 转发由单测覆盖，实机 keepalive 语义由直接 HTTP 调用验证（[记录](verification/explicit-keepalive-duration.md)） |
| 受控 scroll/raw duration/quality/effect 的实机与跨应用证据 | 部分执行：Settings 一个应用 5 次 scroll、1 次 raw swipe 已实测；0.21.0 在哔哩哔哩与网易云再取得 2 次实机 `effect=observed`（`amount=page` 与 `amount=half` 各一次），并覆盖 B 站推荐流与嵌套容器内层；同轮外层 scroll 容器以 `unsupported_node_action`（中心线轨迹不可证）、坐标容器以 `ambiguous_point` 拒绝，raw swipe 另得 2 次（列表翻页 400ms、横向切歌 200ms）。长文/WebView/overlay/STOP 时序与阈值校准仍未执行（[记录](verification/semantic-scroll-observation-quality.md)、[0.21.0 覆盖记录](verification/mcp-tool-surface-coverage.md)） |
| 契约 11 公共树表示的整组发布与实机验收 | 已执行（部分）：0.11.0 整组构建/清单/部署与两个 MCP 入口三模式探针、契约 10 拒绝、最小 STOP、full/delta/inspect 最小闭环均已实测；`verify-m6` 由用户排除，`act` 重验失败交付的实机样本未构造（[记录](verification/compact-node-tree-presentation.md)） |
| 真实 Capture 的新旧公共表示对比与运行成本 | 未执行：用户明确排除；原始采集与树不入库，合成高冗余 fixture 只证明 ≥25% 缩减，真实压缩比、token、耗时与进程峰值未测（[记录](verification/compact-node-tree-presentation.md)） |
| 联动栏容器伸缩下的实机 `observed` | 未取得：X 首页复现 `2242↔2400` 容器伸缩，但其虚拟化列表让匹配集为空（`insufficient_anchors`/`container_changed`）；Settings 有 `observed` 但没有容器伸缩（[记录](verification/scroll-safe-trajectory-and-viewport-effect.md) §6.4、§7） |
| 带局部滚动覆盖的受控合成页实机样本 | 未执行：本轮按用户指定范围只部署整组产物与两个主机二进制，未安装验证探针 App；该场景由合成用例与 Zhihu/X 真实嵌套作用域覆盖（[记录](verification/scroll-safe-trajectory-and-viewport-effect.md) §7） |
| 0.14.0 外部 MCP 宿主重新发现工具 | 未执行：手机 HTTP 适配器已验证启动及哈希；手机 stdio 旧进程与外部依赖宿主仍需重启，本轮未操作宿主或 `tools/list`（[记录](verification/act-tap-safe-scope-and-guidance.md) §7、§11） |
| 观察连续性 D01 宿主落盘 | gap：contract 15 三轮 pass 来自 `bb43c19` 的 APK；独立宿主日志落盘证明未取得 |
| 观察连续性 D02 原生滚动 | gap：三轮无 fail，固定 full 基线多 delta、ReadApply、保旧 O/ref 均通过；`native_scroll_unavailable` |
| 观察连续性 D03 | 27 case 中 24 pass、3 fail；越界兄弟 overlay、in-scope 完整阻断负例与 service primitive 精确计数仍缺 |
| 观察连续性 D04 | 18 case 中 3 fail、15 gap；键盘顶起、focus-shift race 与 primitive 精确计数仍缺 |
| 观察连续性 D05/D06 | D05 最新 1 pass/1 fail，旧报告保留 3 fail；STOP 注入与真实外部 MCP 宿主刷新未独立确认 |
| 0.16.0 慢 hierarchy wait / STOP、end 迟到 O | 未执行：当前设备没有可控慢 dump 环境；终帧复用、提交门禁与撤销由 engine 测试覆盖（[记录](verification/agent-output-and-wait-acquisition.md) §8.5） |
| 0.16.0 真实宿主/模型 #20 与 Instructions 轨迹 | 未执行：没有同宿主/模型/上下文保留策略材料；#20 继续保留 `12/70/180s`，不冒充模型行为结论（[记录](verification/agent-output-and-wait-acquisition.md) §8.5、[第二份记录](verification/agent-reference-and-action-contract.md) §8.4） |
| 0.16.0 实机 STOP/ENTER 回执注入 | 未执行：部署无故障注入面；由合成测试覆盖，不冒充实机结论（[记录](verification/agent-reference-and-action-contract.md) §8.4） |
| 0.16.0 claim/handoff 成功路径 | 已验证（0.21.0，virtual）：虚拟屏↔主屏往返一次，`handoff` 得 `source_display_id=113`→`display_id=0`、`state_preserved=true`、`foreground_restore=confirmed`；`claim` 回虚拟屏得 `ownership=borrowed`、`state_preserved=true`，应用内搜索结果页状态保留。0.16.0 因受控主屏任务不存在而 SKIP 的记录由本条取代（[记录](verification/mcp-tool-surface-coverage.md)） |

## 4. 证据入口

- [P3 实机验证](verification/p3-real-device.md)
- [P4 实机验证](verification/p4-real-device.md)
- [P5 实机验证](verification/p5-real-device.md)
- [P6 控制呈现、消息中心与整组交付](verification/p6-real-device.md)
- [act 坐标目标点证明实机验证](verification/act-coordinate-point-proof.md)
- [shell 保留当前观察实机验证](verification/shell-keeps-observation.md)
- [结构化 recovery 入口实现与验证](verification/structured-recovery-entry.md)
- [MCP 文本树块实现、0.4.2 部署与实机验证](verification/mcp-tree-text.md)
- [默认观察树坐标省略的实现、0.20.0 整组发布与实机验证](verification/compact-observation-bounds.md)
- [工作期限与未决输入的会话级封锁实现与实机验证](verification/remove-work-ui-deadline-lockdown.md)
- [显式重设会话租期与取消累计硬上限实现、发布与实机验证](verification/explicit-keepalive-duration.md)
- [ref 连续性与静态页 delta 修复验证](verification/ref-stability-public-identity.md)
- [主屏多窗口层级修复与实机验证](verification/primary-display-multi-window.md)
- [契约 11 公共树表示、交付预算与文本 v2 的实现与验证](verification/compact-node-tree-presentation.md)
- [tap/long_press 子树安全落点与结构化引导的实现与验证](verification/act-tap-safe-scope-and-guidance.md)
- [Agent 输出、instructions 与 wait 终帧复用的实现、发布与联合验证](verification/agent-output-and-wait-acquisition.md)
- [统一 reference 与受控动作契约的实现、单测与实机验证](verification/agent-reference-and-action-contract.md)
- [观察连续性、结构差分与动作重验的实现、发布、实机与归档记录](verification/observation-continuity-and-revalidation.md)
- [S6 统一基准记录（B01–B06）](verification/observation-continuity-benchmarks.md)
- [text 写入确认、错误详情与恢复的实现、发布与实机验证](verification/text-write-verification-and-recovery.md)
- [MCP 工具面虚拟屏覆盖测试（0.21.0）](verification/mcp-tool-surface-coverage.md)
- [MCP 工具面虚拟屏覆盖测试（0.23.0 补充）](verification/mcp-tool-surface-coverage-0.23.0.md)
- [受管 shell 的显示屏语义与 `screen.display_id`](verification/managed-shell-display-semantics.md)
- [工程决策](decisions.md)
- [工程流程](process.md)

## 5. 暂缓事项

- 第 3 节的验证缺口暂不单独补测；待其后按真实使用路径补齐证据。
- 历史时点：契约 14 阶段部署为 `0.14.0`（`contract_version=14`、
  `android_version_code=16`），构建自 `cc71c87`、`source_dirty=false`；该描述保留为
  契约 14 发布演进记录（[记录](verification/act-tap-safe-scope-and-guidance.md)）。
- 历史时点：已验证部署为 `0.15.0 / contract_version=15 / android_version_code=17`。release 与
  host manifests 的 `source_commit` 均为
  `049583d328e335c56d56f153b7464ea03f1ca814`、`source_dirty=false`；完整 release 六制品
  齐全且现行 verifier PASS，manifest 没有 `complete` 字段。控制 App 以 `adb install -r`
  覆盖安装并保留私有数据。版本、App 配对、当前准入/恢复屏障、STOP availability、运行中
  手机 HTTP MCP 映像 hash、12 工具/schema 与最终 smoke 均 PASS。`049583d` 相对
  `38f47e3` 只澄清四份 docs；重新构建的 `control.apk` SHA-256 为
  `18a6e7ce612bd5ec1d70b9f78910169762dfb7ce09fbf4fb6c216da6362f6235`，其余五制品
  hash 与 `38` 完全一致；host 2 exe hash/大小不变。Android 源码与版本没有变化，未分析该
  APK 字节差的来源。
- 当前已验证部署为 `0.16.0 / contract_version=16 / android_version_code=18`。实现提交为
  `792c973fc70399e0a063fdd13b2b1c2a4056950d`；归档提交 `685d8dc` 改变 HEAD 后，按
  verifier 的精确 HEAD 门禁从当时 HEAD 重建 release 与 host-tools 清单并重新部署。最终
  清单 `source_commit` 为本记录所在提交（见 `build/release/manifest.json` 与
  `build/host-tools-manifest.json`）、`source_dirty=false`；release 六制品齐全，
  `verify-manifest.ps1` PASS，host tools `-VerifyOnly` PASS，目录指纹
  `c5fdc545cc77882a4662b6a4524517475065cfc3248ec42223ae4c1189529928`（12 工具）。
  控制 App 以 `adb install -r` 覆盖安装并保留私有数据，安装后 `0.16.0/18`。部署后 health、
  App 配对、恢复屏障、STOP availability、两个手机 MCP 适配器 `/proc/<pid>/exe` 与新
  `device-mcp` 一致。主机 stdio text/structured/both、主机 HTTP、真实服务 smoke 与
  app-lifecycle 探针 PASS；instructions 5958 字节且与源码逐字节一致。按用户指示，Codex
  宿主工具调用不作为本轮验收项；主机侧二进制已重建为 0.16.0 并 `-VerifyOnly` PASS
  （[第一份记录](verification/agent-output-and-wait-acquisition.md) §8、[第二份记录](verification/agent-reference-and-action-contract.md) §8）。
- 0.16.0 首次 smoke 在服务重启窗口同样出现一次 `start=control_revoked`；独立 status 为
  `can_start=true`、旧会话 `ended(reason=service_restarted)`、cleanup complete，重跑完整
  PASS，未判为契约缺陷。
- 契约 16 的两份提案已归档到 `proposals/implemented/`：
  `agent-output-and-wait-acquisition.md`（实现 `07c85b6`、`792c973`）与
  `agent-reference-and-action-contract.md`（实现 `d529ee7`、`792c973`），§11 记录已落地
  落点、#20 保留现值与未执行验证。
- 新锚点首次 smoke 在 auth/Origin/token、connect、工具、catalog、schema 与 status 通过后，
  `start` 返回 `control_revoked`；随后独立只读 status 为 `can_start=true`、旧会话
  `ended(reason=service_restarted)`、cleanup `unconfirmed`，未把该 reason 武断认定为本次
  撤权根因。重跑 smoke 完整 PASS；D06 在 `38` 与 `049` 各三轮单 delta 闭环 PASS，实际外部
  MCP 宿主刷新仍由用户自行处理，未独立确认（[记录](verification/observation-continuity-and-revalidation.md) §8）。
- `049583d` 的干净 checkout、六产物/manifest 与 host 2 exe/manifest 已保存在
  `.state/release-source-049583d`，其自身 `verify-manifest` 与 `build-host-tools -VerifyOnly`
  均 PASS。归档后的主树 HEAD 不是发行源，后续复核不得在新 HEAD 复用旧 manifest 冒充同源
  PASS。
- 契约 10 的实现、部署与实机补记已推送到 `origin`；issue #10 已回复并以 `COMPLETED`
  关闭，`#7` 未关闭、未重开（[记录](verification/semantic-scroll-observation-quality.md) §9）。
- 契约 11（公共树表示与交付预算）已实现、整组发布并部署到手机；`verify-m6` 由用户排除，
  真实 Capture 压缩比/token 与运行成本按用户要求未测（[记录](verification/compact-node-tree-presentation.md)）。
- 契约 13（受控滚动安全区间与联动视口效果）已实现、整组发布并部署到手机；X 首页的
  `2242↔2400` 容器伸缩已复现，但该应用仍缺正证据，联动栏下的实机 `observed` 尚未取得
  （[记录](verification/scroll-safe-trajectory-and-viewport-effect.md) §7）。
- 契约 14（tap/long_press 子树安全落点与结构化引导）已实现、整组发布并部署到手机；临时
  复合卡片取得静态拒绝、原子引导、固定点重验路径与业务后观察，`occluding_ref` 专门页面与
  活动会话下最小 STOP 撤销未单独实机构造（[记录](verification/act-tap-safe-scope-and-guidance.md) §9、§11）。
- 契约 13 的实现与归档提交已推送 `origin`（`master` 到 `6c52c8e`；归档提交信息里的「未推送」
  描述当时状态，已被本条取代）。issue #12 与 #13 各附一条实现与实机结果回复后以 `COMPLETED`
  关闭；#11、#14、#15 仍开放。
- 历史时点：提案在 `70f390e` 以“提案，未实现”落盘；完成 `900e640` 的 S1–S5 后，当时计划
  仍列有 S6–S10，并约定在后续会话继续。该状态已由后续实现取代，原文只作为演进记录。
- 提案 [观察连续性、结构差分与动作重验](proposals/implemented/observation-continuity-and-revalidation/README.md)
  已完成 S0–S10 并归档。全部实现提交为 `900e640fe8343f92c4e8dd82dd3df839dde20756`、
  `bb43c1943d66e3ea5fe5fd938f9a5b68c82dfdeb`、
  `38f47e38fd85ffa8b79e8b9dbea13a4c00babe14`、
  `049583d328e335c56d56f153b7464ea03f1ca814`。`049583d` 在 `38` 之后只澄清四份 docs，
  是最终发行源码锚点。代码范围全部落地，故提案状态为“已实现”；D01–D06 的宿主、fixture、
  注入与刷新缺口已由用户于 2026-11-16 接受，按“实机有限验证”保留。
- 四份 docs 澄清经独立只读 Ollama/Nietzsche（max，agent
  `01a1e1c0-eeed-7021-afec-f27386835e31`）最终复检，原话为“本次全部九份docs CLEAN”。
  S7 Gemini 的配额失败、四类发现与最终 CLEAN 历史仍独立保留，两次审计不互相替代
  （[记录](verification/observation-continuity-and-revalidation.md) §6、§6.1、§9）。
