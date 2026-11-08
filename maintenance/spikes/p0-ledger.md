# Spike：恢复账本

状态：2026-11-08，**部分完成**。写入路径已实现；崩溃窗口注入与耐久性核对**未执行**，不得
把本页当作 G0 spike C 通过。

## 1. 范围

- 单一恢复对象的大小、格式与内容边界；
- 单写者、修订序、原子替换（temp → fsync → rename → fsync 父目录）；
- 读写失败时的可用性后果；
- 重启后账本驱动恢复的链路。

## 2. 实现落点

| 关注点 | 代码 |
| --- | --- |
| 记录结构 | `internal/engine/ledger.go`（格式 `mobile-use-vnext-ledger`、版本 1、`revision`、会话/命令/收尾/停止边界） |
| 原子文件 | `internal/store/ledger.go`（64 KiB 上限、包内互斥、临时文件 `0600`、`Sync`、`Rename`、父目录 `Sync`） |
| 写入时机 | `internal/engine/start.go`（创建显示助手前同步写原归属与创建意图，激活后再写）、`internal/engine/control.go`（`persistSync`/`persistAsync`） |
| 失败后果 | `engine.persistSync` 失败置 `ledgerUnsafe`；可用性 `blocked`（原因 `ledger_unavailable`）；账本解析失败置 `blockedReason=ledger_corrupt` |
| 恢复消费 | `internal/engine/control.go` 的 `StartRecovery`/`driveRecovery` |

不写入账本的内容：请求体、节点树、图片、输入正文、命令输出、异常字符串。

## 3. 代码能证明的事实

- 账本只有一个 JSON 对象，读取时校验大小、JSON、格式名、版本与修订号；不合法即拒绝。
- 保存路径先写临时文件并 `Sync`，`Rename` 覆盖目标，再对父目录 `Sync`；Windows 跳过目录
  `Sync`（`runtime.GOOS != "windows"`）。
- 保存前若尚未加载，会先读一次，避免静默覆盖外部写入；修订号在包内单调递增。
- 写失败会把可用性置为 `ledger_unavailable`，不会继续假装可启动。
- 重启后 `StartRecovery` 读取记录并驱动显示探测/精确关闭；无法确认时保持 `recovering`。

## 4. 实测与未验证

| 项 | 状态 |
| --- | --- |
| 服务 `SIGKILL`（有 active 会话）后重启 | **已实测**：旧显示已随助手消失，`reason=display_absent`、`cleanup complete`、无 display 残留 |
| App 进程 `force-stop` 后重启服务 | **已实测**：会话被撤销、`cleanup complete`、`can_start=true` |
| 旧显示仍存在时的恢复（精确 `close_display`） | **未实测** |
| `rename` 前后崩溃窗口注入 | **未执行**（无 store 包内测试，也没有故障注入脚本） |
| 整机断电/文件系统掉电 | **未覆盖**，本页不作耐久性结论 |
| Windows 上的耐久性 | **不成立**：目录 `Sync` 被跳过，Windows 只用于构建与开发检查 |

## 5. 结论

- 账本写入/读取/失败降级路径在代码层完整，`SIGKILL` 重启路径有一次端到端实机证据。
- “进程在 `rename` 前后崩溃”的提交语义没有验证，spike C 的崩溃注入部分仍为
  **pending**；不能据此宣称账本已通过耐久性验收。
- 恢复屏障中“旧显示仍存在”的分支同样未验证，见
  [P3 实机记录](../verification/p3-real-device.md)。
