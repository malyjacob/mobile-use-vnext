# 架构

本文描述设备服务的进程分层、唯一权威、会话与观察/操作生命周期、full/delta 的提交条件、
工作位置与并发、STOP 与收尾、恢复屏障、账本以及事实分层。

## 1. 进程与包分层

```text
cmd/device-service
  ├─ 私有配置、进程锁、信号、启动顺序、退出
  ├─ internal/httpapi    认证与 /api/<tool>、/v1/control/*
  ├─ internal/engine     唯一业务权威（会话、O/P、工作位置、STOP、收尾、恢复）
  ├─ internal/android    ADB、u2 JSON-RPC、固定 JAR、root 显示助手
  ├─ internal/observation 公开树构造、保号、full/delta 纯算法
  ├─ internal/store      私有恢复账本（原子文件、单写者）
  └─ internal/eventlog   有界字段白名单日志
```

依赖方向由 `cmd` 组装时固定：

- 请求方向是 `httpapi → engine →（经注入的 Device 接口）android`；`engine` 同时使用
  `observation` 的纯算法、`store` 的账本与 `eventlog` 的事件汇。反向依赖不存在：`httpapi`
  不访问 ADB、不保存会话、不判定接纳。
- `engine` 不导入 `internal/android`、`internal/store`；设备、账本、投影与差分都是 `cmd`
  注入的小接口（`Device`、`Store`、`Projector`、`Differ`）。
- `internal/device` 只放原始事实类型（`Capture`、`RawNode`、`Display`、`TextTarget` 等），
  无 I/O、无运行状态。
- `internal/observation` 是纯算法：输入一次 `device.Capture` 输出不可变公开树，或输入两棵树
  输出 delta；不接触会话、账本与设备。
- `internal/android` 只执行有界设备 I/O 并返回受控事实；不决定观察什么、操作什么。
- `internal/store` 实现 `engine.Store`，只持久化 `engine.Ledger`。
- `internal/eventlog` 只接收批准字段；队列有界（1024），满时丢弃并计数，不阻塞 STOP。

进程启动顺序（`cmd/device-service/main.go`）：读取并校验私有配置 → 在固定运行目录取得
`flock` 进程锁 → 创建账本存储 → 构造 android 适配器 → `engine.StartRecovery` → 启动时钟 →
监听 HTTP → 后台 `Prepare` + `EnsureU2`，成功后才 `SetDeviceReady(true)`。第二个实例拿不到
锁时退出（退出码 3），不会接触账本、App 或设备。

## 2. 唯一权威

会话、租约、O/P 接纳与关闭、输入授权、普通设备工作位置、收尾进度与恢复判定只由
`internal/engine` 的互斥状态持有。HTTP 层、控制 App 与其他接入适配器都不保存第二份执行
权威：

- 控制 App 只拥有自己的虚拟屏物理资源与显示生命周期，执行服务下发的
  `create_display`/`close_display`；它不能接纳会话、签发观察或自行执行输入。
- HTTP 认证通过后，工具请求仍必须由 engine 的准入、认领、提交条件逐项通过。
- 客户端持有的 O/P 编号只是引用；编号形状不授予任何权限，归属由认证身份与服务端记录决定
  （`operationState.Agent`、`observationState.Agent`）。

engine 的状态锁只做有限内存检查与转换：锁内不等待 ADB、u2 RPC、磁盘或树算法。设备 I/O、
树构造、delta 计算、账本写入都在锁外完成，提交时重新加锁复核。

## 3. 会话与 O/P 生命周期

### 会话

`starting → active → ended`（`sessionState.State`）。

- `start` 先登记 P、取得工作位置，再创建 `starting` 会话；虚拟屏要等 App 创建回执、App
  上报的显示描述、以及 `dumpsys`/SurfaceFlinger 的显示存在与 SFID 三者一致后才转 `active`。
- 租约 `SessionTTL=120s` 滑动续期，只由被接纳的普通工作延长（成功提交的采集、`act` 认领）；
  `status` 查询不续租。硬上限 `SessionHardLimit=1h`，从 `start` 起算，不随续期突破。
- 结束原因写入 `session.reason`，例如 `client_end`、`user_stop`、`lease_expired`、
  `app_restarted`、`service_shutdown`、`display_create_unconfirmed`。
- 只有服务正在运行的会话（或收尾未完成时保留的 `lastSession`）会进入公开视图；收尾完成后
  会话被清空，控制视图不再把它当作运行中会话。

### O（观察）

`O` 的公开状态为 `ready`、`claimed`、`retired`、`expired`。

- 每次成功提交发布一个新 O；新 O 发布时，上一个 `ready` 的 O 立即转 `retired`。同一会话
  最多一个 `ready` O。
- `act` 在静态检查通过、取得工作位置后把 O 从 `ready` 单向改为 `claimed`。认领后即使未派发
  任何输入也不归还；静态拒绝发生在认领之前。
- 过期由时钟（每秒）与请求时的惰性检查共同处理；有效期
  `min(captured_at + 60s, session.expires_at)`。
- 保留上限 `MaxObservations=32`；会话的 O、基线 O、已认领 O 受保护，其余按采集时间淘汰。

### P（操作）

`P` 的公开状态为 `running → finished`；`status(P)` 可返回 `not_seen`。

- 每个工具在任何设备副作用之前登记 P
  （`registerOperation`），可用 `X-Mobile-Use-Operation` 预分配；缺省时服务生成 `P<n>`。
  格式为 `P` + 字母/数字/`-`/`_`，总长 ≤64。
- 同一 P 重复到达只返回原操作的公开视图，不再次派发（包括已失败的操作）。
- P 记录上限 `MaxOperations=256`；淘汰只针对最旧的已完成、且不被会话/工作/O 保护的记录。
- 已结束或显式封闭的 P（`end` 对未接纳/已结束的引用等）进入有界 `closed` 集合（最多 512），
  之后返回 `request_closed=true`，不会重新执行。
- 观察与 P 的公开编号在**同一服务实例内**单调：`O<n>`、`P<n>`；会话自身有 `epoch`，提交与
  恢复都用它识别“原会话”。

## 4. full/delta 与提交条件

一次采集（`captureAndPublish`）的固定步骤：

1. 复核工作未撤销、会话仍属于该工作；
2. `ProbeDisplay` 确认目标逻辑屏存在；
3. `Capture` 读取层级、前台与任务；
4. 采集龄超过 `CaptureMaxAge=15s` → `stale_screen`；
5. 虚拟屏上出现控制 App 的可见节点 → `control_ui_on_virtual_display`，采集失败；
6. `observation.Builder` 构造公开树（合成根、必要祖先、保守保号）；
7. 始终先编码一份完整树；超过 `MaxFullTreeBytes=128 KiB` → `tree_too_large`；
8. 在锁内重验：会话仍 `active`、`epoch` 不变、工作未被撤销、仍持有工作位置、显示 ID 与
   `generation` 未变、采集未过期；
9. 通过后 `session.Revision++`，分配 O，发布，退休上一个 ready O，续租。

full/delta 的选择（`useDelta`）：

- `observe(P)` 强制 full；
- 否则仅在“已有确认基线、基线属于同一会话、`revision - baseline.revision < 12`、
  基线采集龄 `< 180s`”时尝试 delta；
- 差分必须可编码，且编码长度 `< full 长度 × 70%`；否则退回 full。
- delta 无法表达（角色变化、跨父移动、根变化等）时 `Diff` 返回错误，采集改用 full。

**基线的唯一确认入口**：只接受被接纳的 `observe(O)` 对该 O 的确认，或 `act` 对 full O 的
认领确认。即：`start` 的首个 full 只是候选；`observe(O1)` 成功后才把 O1 升为基线，
并把空差异 delta 返回给调用方。客户端必须保留基线树，按 `delta.base` 应用到本地副本。

delta 的五类块：`set`（逐字段完整新值，显式保留 `false`/`0`/`""`/`[]`/`null`）、`add`、
`remove`、`children`（完整子列表）、`replace`（整段替换）。空差异只含 `format` 与 `base`。

## 5. 工作位置与并发

- 设备同时只有一项普通工作（`start`/`observe`/`act`/`apps`/`open_app`/`shell`）。冲突立即返回
  `409 device_busy`，不建普通动作队列；同一 Agent 会得到 `blocking_operation_id`。
- 工作上下文按类型设上限：`start` = `StartTimeout + WorkTimeout`（40s）、`observe` =
  `WorkTimeout + 15s`（35s）、`act`/`open_app` 约 50s、`apps` 约 30s、`shell` 约 65s；具体超时是服务策略，不是请求参数。
- `end`、STOP、`status` 不占普通工作位置，也不等待普通工作完成。
- 控制同步 `/v1/control/sync` 不占工作位置，不做设备 I/O；只更新 App 事实、处理命令回执与
  停止意图，至多下发一条显示命令。

## 6. STOP、end 与收尾

### 停止边界

两个入口都归结为 `bindStopLocked`：

- 普通同步携带 `stop_pending`（`seq` 单调、`reason`）；
- 独立 `POST /v1/control/stop`（最小载体，不校验普通 `contract_version`）。

绑定条件：同一 `app_instance_id` 的 `seq` 必须严格大于已记录值；否则忽略（HTTP 响应仍为
`accepted=true`，`state=duplicate`）。成功绑定时：

1. 记录停止边界（`stopBoundary`，并写入账本 `stop`）；
2. 若存在未结束会话：`state=ended`、`reason=user_stop`、会话的 ready O 退休、工作标记撤销并
   取消上下文；
3. 锁外启动独立收尾链；
4. 不等待设备工作、日志/账本刷盘或网络；已派发的 Android 命令不保证撤回。

停止后 `start` 仍会被拒绝，直到同一 App 实例在停止边界之后成功同步一次且不再携带
`stop_pending`；期间 `status.blocking_code=control_revoked`。

### end

`end(reference)` 解析 O 或 P 的原会话：撤权 → 建立唯一 `pending` 收尾记录 → 返回
`executed` 与当时的 `cleanup`。第二次 `end` 只观察同一条收尾链，不创建第二条破坏性链。
对从未被接纳的 P，`end` 返回 `request_closed=true` 且 `cleanup` 全为
`not_applicable`。已结束会话且尚无收尾记录时，`primary` 模式或“无显示且无创建结果”直接
记为 `not_applicable`，否则补建收尾。

### 收尾链

`cleanup` 固定五元组：`state`（`pending`/`complete`/`unconfirmed`）、`execution`、
`tasks`、`recents`、`display`（每项可为 `pending`/`confirmed`/`unconfirmed`/`not_applicable`）。

- `execution`：若结束瞬间仍有本会话已派发但结果未知的输入（`DispatchUnknown`），记
  `unconfirmed`；否则 `confirmed`。受管执行链（`shell`）在取得精确终止证据后可将这一项
  从 `unconfirmed` 提升为 `confirmed`；UI 输入的不确定性保持不可改判。
- `primary` 模式：`tasks`/`recents`/`display` 全为 `not_applicable`，从不关闭用户应用。
- 虚拟屏且显示身份未知：给 App 至多 4s 上报描述；仍无描述时，若创建明确失败或 App 明确
  报告无自有显示，则 `display=confirmed`，否则 `unconfirmed`。
- 显示身份已知：App 实例未更换时下发精确 `close_display`（带
  `target_app_instance_id`/`target_display_id`/`target_generation`）。关闭显示前先移除该虚拟屏上的全部实时任务（至多三轮，每轮重读实时任务），再核验
  `dumpsys activity recents`：任务全部消失且最近任务项也消失时 `tasks=confirmed`、
  `recents=confirmed`；仍有任务或无法核验则相应记 `unconfirmed` 并带
  `task_remove_unconfirmed`/`recents_cleanup_unconfirmed`。随后最多 `CleanupTimeout=12s`
  轮询，要求连续两次确认显示不存在才记 `display=confirmed`。
- 虚拟会话在内存维护受管窗口登记（`taskEntry`：task id、包名、display、generation、
  owned、proven，上限 16）；它是每次动作前由实时任务列表核对的缓存，不跨会话或重启
  继承控制权；主屏会话不建立该登记。
- `state=complete` 仅当 `execution`/`display`/`tasks`/`recents` 均不为 `pending` 或
  `unconfirmed`。完成时清空该会话的 O、基线、任务登记与显示命令。
- 收尾期间 `availability=recovering`（原因 `cleanup_pending`），同步仍会投递
  `close_display`（否则无法释放），但 `start` 因可用性非 `ready` 被阻塞；收尾 `unconfirmed`
  时 `availability=blocked`（原因 `cleanup_unconfirmed`），`can_start=false`。
- 未确认结束的受管 shell 链保持 `recovering`（原因 `shell_pending`）；终止中尚无证据为
  `shell_chain_unconfirmed`；期间不接纳普通工作，直到精确 runner 被确认结束。
- 服务退出（SIGINT/SIGTERM）：撤权并启动收尾，等待已接纳的后台工作至多 25s，然后同步写一次
  账本；进程退出本身从不被当作资源已释放。

## 7. 恢复屏障

进程取得固定运行目录内核锁之后，才接触账本、App 与设备。`StartRecovery` 读取账本：

- 账本损坏/不可读 → `blockedReason=ledger_corrupt`，可用性 `blocked`，不接纳新会话；
- 无记录 → 保持 `recovering`，直到 App 心跳新鲜且模式/停止通道可用、无待处理收尾；设备就绪
  另由可用性检查要求（`device_unavailable`）；
- 有记录 → 先要求 App 心跳新鲜，再按记录中的会话/待创建命令逐项核验：
  - 无会话且无待创建 → `no_session`，恢复完成；
  - 记录有受管 runner：按记录的 runner 身份探测/终止；`shell_probe_failed` 或
    `shell_chain_unconfirmed` 时保持 `recovering`，确认结束才写完成记录、不重放命令；
  - 记录有显示 ID：探测显示；仍存在且 App 实例匹配 → 下发精确 `close_display` 并保持
    `recovering`，等待 App 回执/显示消失后重试；显示已不存在也要先完成 App 同步与 TaskList
    核验，不能只凭一次 absent 放行；
  - 记录无显示 ID 但 App 上报了显示描述 → 以该描述下发精确 `close_display`，不信任旧
    `UpdatedAt` 计时器；
  - 显示探测失败/TaskList 失败/旧 display 仍有任务 → 保持 `recovering` 或
    `old_resource_unconfirmed`，周期重试；
- `finishRecovery` 只有在“App 心跳新鲜 + 显示已确认释放 + TaskList 成功且目标 display 无
  任务”后才写完成记录；`tasks` 由该证据写 `confirmed`/`not_applicable`，`execution`/
  `recents` 的 `unconfirmed` 不会被清成 `confirmed`。恢复未解决期间 `ledgerRecordLocked`
  返回旧账本快照，普通同步不会抹掉旧归属。

`can_start`（`status` 返回）是完整准入门的投影：可用性为 `ready`、无活动会话、App 新鲜且
`background_running`/`notifications_enabled`/`stop_ready` 为真、模式就绪且代次匹配、设备
交互且未锁屏、无未确认停止意图。任一不满足都返回 `can_start=false` 与 `blocking_code`。
端口已监听、`/health` 返回 `ok` 都不是干净证明。

## 8. 账本

`internal/store` 把单个 JSON 对象写在 `<state_dir>/recovery-ledger.json`：

- 预算 64 KiB；格式名 `mobile-use-vnext-ledger`，版本 1，修订号单调递增；
- 单写者：包内互斥 + 进程锁；`Save` 先 fsync 临时文件，再 rename 覆盖，再 fsync 父目录
  （Windows 跳过目录 fsync，因此 Windows 不提供同等耐久证据）；
- 内容只有归属与资源证据：会话（origin/agent/mode/state/App 实例/模式代次/显示 ID/SFID/
  generation/尺寸/密度/旋转/开始时间）、待创建/待关闭命令、收尾五元组、停止边界、虚拟屏
  受管任务 id 登记（≤16）、受管 shell runner 身份与阶段；
- 明确不含：请求体、节点树、图片、输入正文、命令输出、异常字符串；
- `start` 在启动显示助手前同步写一次（原归属 + 创建意图），会话激活后再写一次；其余进度
  异步写。写失败置 `ledgerUnsafe`，可用性转 `blocked`（原因 `ledger_unavailable`）；
- 输入派发本身不进入账本；“已派发但结果未知”只存在于进程内的工作状态，进程退出后以
  资源探测与收尾记录为准。

账本只由 engine 读取与生成，HTTP 层与 App 都不写账本。

## 9. 事实分层与日志

同一次请求可能同时表达四层事实，互不替代：

| 层 | 字段 | 含义 |
| --- | --- | --- |
| 动作执行 | `outcome` | `executed` / `not_executed` / `uncertain`；只描述本次输入是否已派发并有完成证据 |
| 观察 | `observation.status` | `complete` / `failed`；后观察失败不改写已确认的 `executed` |
| 会话 | `session.state` | `starting` / `active` / `ended` |
| 物理收尾 | `cleanup.*` | 显示/任务/执行链是否确认释放 |

事件日志只记白名单键（ID、枚举、布尔、计数、时长），值经过单行截断；不记录树、差异、输入
正文、命令、原始响应或异常文本。
