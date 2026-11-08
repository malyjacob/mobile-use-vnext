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
| P6 控制呈现/整组交付 | 未开始 | 完整 control_view、App 界面整理、发布包 |

## 2. 工具面

| 工具 | 实现 | 实机 |
| --- | --- | --- |
| `start`/`status`/`observe`/`act`/`end` | 已实现 | 已验证（P3、P5 记录） |
| `apps` | 已实现 | 已验证（Core 脚本） |
| `open_app` | 已实现 | 已验证：创建/复用/未知包/end/STOP 清理 |
| `inspect` | 已实现 | 已验证：整树/子树/查询 |
| `keepalive` | 已实现 | 已验证：续租/结束会话拒绝 |
| `notify` | 已实现 | 已验证：保存/重复/空正文；App 消息中心交付未接入 |
| `diagnose` | 已实现 | 已验证：无参/指定 O |
| `shell` | 已实现 | 已验证：正常/非零/自行124/超时/输出截断/STOP/重启恢复 |
| `observe`/`act` 的 `image=true` | 已实现（HTTP + MCP） | 已验证：JPEG 540×1200、MIME、无 base64、只读图分支由单元测试覆盖 |
| MCP stdio / Streamable HTTP | 已实现（`cmd/device-mcp`） | 已验证：工具目录/schema、认证/Origin、图片、取消、P 恢复 |
| Go client（`internal/client`） | 已实现 | 已验证：P 保留、断线、重复 P、取消、只读取消、图片 |

## 3. 未验证 / 未实现

| 项 | 状态 |
| --- | --- |
| shell 超时/输出截断/终止证据/重启恢复 | 已验证 |
| shell 受管链在 STOP 后的清理提升 | 已验证（execution 升为 confirmed） |
| apps 预算截断/缓存失鲜并发 | 单元测试覆盖；实机未构造 |
| inspect 超限/截断 | 单元测试覆盖；实机树未超限 |
| notify 的 App 消息中心交付 | 未接入 |
| MCP `-race` 竞态检查 | 未执行：Windows 宿主无 C 编译器，cgo 不可用 |
| MCP HTTP 多宿主并发/SSE 重连 | 未构造；单请求隔离、401/403 已实测 |
| MCP 外层整份结果丢失且宿主无 P | 无法注入；无 request-id 恢复工具，仅按已有 O/P 或 `status()` 恢复 |
| 页面明显变化后的图片内容差异 | 未构造；实机为同屏静态帧 |
| 完整 control_view / App 界面整理 / 发布包 | 未实现（P6） |

## 4. 证据入口

- [P3 实机验证](verification/p3-real-device.md)
- [P4 实机验证](verification/p4-real-device.md)
- [P5 实机验证](verification/p5-real-device.md)
- [工程决策](decisions.md)

## 5. 暂缓事项

- 第 3 节的验证缺口暂不单独补测；待其后按真实使用路径补齐证据。
- 当前部署为 `0.3.0`，`can_start=true`；P6 开始前保持该部署，不因细粒度缺口暂停。
