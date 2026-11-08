# 项目实施状态

状态：2026-11-09。本页承载用户可见的阶段进度、已实现/已实测/未验证分层与未决项；
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
| `apps` | 已实现 | 已验证（Core 脚本） |
| `open_app` | 已实现 | 已验证：创建/复用/未知包/end/STOP 清理 |
| `inspect` | 已实现 | 已验证：整树/子树/查询 |
| `keepalive` | 已实现 | 已验证：续租/结束会话拒绝 |
| `notify` | 已实现 | 已验证：保存/重复/空正文；经控制同步进入 App 消息中心、系统通知与本地已读，确认后释放服务端队列 |
| `diagnose` | 已实现 | 已验证：无参/指定 O |
| `shell` | 已实现 | 已验证：正常/非零/自行124/超时/输出截断/STOP/重启恢复 |
| `observe`/`act` 的 `image=true` | 已实现（HTTP + MCP） | 已验证：JPEG 540×1200、MIME、无 base64、只读图分支由单元测试覆盖 |
| MCP stdio / Streamable HTTP | 已实现（`cmd/device-mcp`） | 已验证：工具目录/schema、认证/Origin、图片、取消、P 恢复；P6 回归重新拉起实际适配进程。默认部署同时安装并启动手机 `device-mcp`（手机 `18882/mcp`，主机转发 `18883`），`verify-m6` 检查 `server/discover`；主机侧适配器仍可选 |
| Go client（`internal/client`） | 已实现 | 已验证：P 保留、断线、重复 P、取消、只读取消、图片 |
| 控制同步消息投递 | 已实现 | 已验证：App 私存、系统通知、消息中心列表/详情、本地已读、确认释放、跨服务重启保留 |
| 完整 control_view 呈现 | 已实现 | 已验证：Activity/通知/悬浮同源、失鲜、停止优先级、清理/权限/模式详情、主屏确认 |
| 单一版本源与发布清单 | 已实现 | 已验证：生成常量、manifest、SHA-256/大小、工具目录指纹、按清单部署 |
| 保留 App 数据的整组发布 | 已实现 | 已验证：0.3.0→0.4.0 破坏性发布、二次覆盖安装保留消息/已读/连接配置 |

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
| 外部 MCP 宿主缓存刷新 | 未验证：本机以 `scripts/verify-m5.ps1` 拉起实际 `device-mcp.exe` 并重新 `tools/list`；未接入外部宿主 |
| 旧 App + 新服务下的旧 App 界面 | 旧版本在控制视图为空时自身 `NullPointerException`（`ControlState.cleanupRisk`），旧 App 服务仍可发送独立 STOP；新版本已修复该空指针 |
| 通知频道关闭/权限撤销下的消息中心 | 代码路径检查；实机仅验证通知可用路径 |

## 4. 证据入口

- [P3 实机验证](verification/p3-real-device.md)
- [P4 实机验证](verification/p4-real-device.md)
- [P5 实机验证](verification/p5-real-device.md)
- [P6 控制呈现、消息中心与整组交付](verification/p6-real-device.md)
- [工程决策](decisions.md)

## 5. 暂缓事项

- 第 3 节的验证缺口暂不单独补测；待其后按真实使用路径补齐证据。
- 当前部署为 `0.4.0`（`contract_version=2`），`can_start=true`；发布产物构建自提交
  `98645a7`，`source_dirty=false`。旧 0.3.0 服务与 App 仅保留为不匹配验证材料。
