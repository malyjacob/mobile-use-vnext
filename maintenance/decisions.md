# 工程决策（P3）

状态：2026-11-08。只记录已经体现在工作区代码/脚本中的决定；后续阶段的选择在真正实施前
不预写。每条给出背景、选择与影响。

## D1 Go 1.27 与 `GOEXPERIMENT=jsonv2`

- **背景**：接入层必须拒绝未知成员、重复键、大小写变体、尾随值与类型混用，并区分
  “字段缺失 / 显式 null / 零值”。标准库 `encoding/json` 的 v1 语义无法直接满足。
- **选择**：`go.mod` 锁定 `go 1.27.1`；构建与测试统一 `GOEXPERIMENT=jsonv2`；`internal/protocol`
  使用 `encoding/json/v2` + `jsontext`，并配 `RejectUnknownMembers(true)` 与显式存在性检查。
- **影响**：所有构建入口必须带 `GOEXPERIMENT=jsonv2`（`scripts/build.ps1` 已内置）；JSON
  编解码集中在 protocol，不散落到业务包；未来升级工具链需重跑严格接入回归。

## D2 固定 uiautomator 服务制品（`u2.jar`）

- **背景**：需要稳定的层级 dump 与显示绑定文本 RPC；上游制品在中文/非 BMP 文本与
  display 绑定上不满足要求。
- **选择**：只使用 `third_party/uiautomator/u2.jar`，SHA-256
  `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3`，来源
  `uiautomator2==3.5.0` 的 wheel 资源 + 本地 smali 补丁；补丁保留非 BMP 字符，并新增
  `getTextOnDisplay`/`setTextOnDisplay`。运行前校验本地哈希，再推送到
  `/data/local/tmp/u2.jar`。
- **影响**：制品与 Go 服务、补丁说明、许可证一起入库；换 JAR 必须同时更新哈希与补丁记录，
  并重启 `app_process`（运行中的 server 不会热加载）；部署不依赖网络下载。

## D3 控制 App 包名 `com.mobileuse.vnext`

- **背景**：新旧实现必须能在同一台手机上并存/替换，且服务需要精确识别自己的配对 App，
  不能按子串或旧包名猜测。
- **选择**：控制 App 固定 `com.mobileuse.vnext`；服务默认 `control_package` 同名；部署脚本
  会卸载旧包 `com.mobileuse.control`，但不会把旧包当作配对对象。控制令牌与 Agent 令牌分离。
- **影响**：App 私有目录、`connection.json` 路径、root 显示助手入口
  （`com.mobileuse.vnext.RootDisplayMain`）与控制包可见性检查都以该包名为准；改包名等于
  破坏性升级。

## D4 控制协议独立 STOP

- **背景**：停止不能被普通心跳、契约升级、设备工作或清理阻塞；新服务 + 未升级 App 时也必须
  能送达停止。
- **选择**：保留两条停止入口——同步心跳里的 `stop_pending` 和独立
  `POST /v1/control/stop`。独立入口不校验普通 `contract_version`，只要求 UUID 与单调
  `stop_seq`；先本地撤权/封闭派发，再由独立线程发送，不等待普通通道。
- **影响**：STOP 与普通契约解耦，版本不匹配仍可停止；`accepted` 只表示边界已绑定，物理释放
  仍由收尾链证明；App 与服务都要持久化待同步停止意图（App 私有偏好、服务账本 `stop`）。

## D5 虚拟屏 SFID 使用 `uint64`

- **背景**：本机 `dumpsys SurfaceFlinger --display-id` 返回的层 id 超过 `int64` 上限。
- **选择**：`device.Display.SFID`、`device.DisplayState.SFID` 与账本 `sf_id` 都是 `uint64`；
  解析用 `strconv.ParseUint(..., 10, 64)`，`screencap -d` 以十进制原样输出。
- **影响**：任何把 SFID 转成 `int`/`int64` 的改动都会溢出或截断；显示存在性不能依赖有符号
  解析，SFID 与逻辑 display id 也不能互换。

## D6 空虚拟屏是合法空树

- **背景**：刚创建、尚未启动任何应用的虚拟屏没有窗口；若把“无节点”当采集失败，会把正常的
  空画面误报为设备故障。
- **选择**：`android.Capture` 在 dump 成功但节点数为 0 时合成一个显示根（bounds = 目标屏、
  `enabled=true`），不伪造子节点；公开树允许只有根、没有 `children`。
- **影响**：`start`/`observe` 在空屏上仍可成功并产生 full/delta；判断“采集失败”只看 RPC/
  解析/显示探测，不看子节点数量；控制 App 界面出现在虚拟屏上仍是失败条件。

## D7 u2 请求统一 ASCII 转义（`\uXXXX`）

- **背景**：固定 u2 server 在 JSON 解析前按 Latin-1 解码请求体，原始 UTF-8 中文会在到达
  JSON 解析器前被破坏，导致写入乱码。
- **选择**：`internal/android/rpc.go` 在发送前把请求 JSON 中所有非 ASCII 字符转成
  `\uXXXX`（BMP 外字符拆成代理对），与固定 Python 客户端的线形状一致；响应仍按 UTF-8 解析。
- **影响**：中文/emoji 文本写入与读回保持一致；转义是传输层细节，不改变公开契约；新增 RPC
  参数自动继承该规则，不能绕过 `rpcWith` 直接发请求。

## D8 清理期间放行 `close_display`，但 `start` 继续阻塞

- **背景**：收尾需要 App 执行精确释放命令；若在 `cleanup=pending` 时把所有控制命令都停掉，
  显示永远无法释放。另一方面，旧资源未确认时开始新会话会破坏归属。
- **选择**：`availability` 在收尾期间为 `recovering`（原因 `cleanup_pending`），同步仍可投递
  `close_display`；`create_display` 仍要求 `starting` 会话，`start` 因可用性非 `ready` 被拒。
  收尾转 `unconfirmed` 后可用性变 `blocked`，`can_start=false`。
- **影响**：释放路径不被准入门禁误伤；准入不被释放进度放水。人工不能用“重启/删账本”跳过
  该阻塞。

## D9 单一工作位置、无普通队列

- **背景**：设备输入与采集必须串行；排队会引入第二套顺序权威，并让 STOP 等待队列。
- **选择**：`engine` 只持有一个 `workState`；冲突立即 `409 device_busy`，同一 Agent 返回
  `blocking_operation_id`；不建普通动作队列。`end`/STOP/`status`/控制同步不占该位置。
- **影响**：并发调用方必须自行串行化并处理 busy；STOP 永远不必排在普通工作之后；吞吐上限
  由单工作位置决定，这是 P3 的有意取舍。

## D10 账本：单对象、64 KiB、原子替换

- **背景**：崩溃恢复只需要“原归属与资源证据”，不需要业务内容；多文件/数据库会扩大一致性与
  隐私面。
- **选择**：`internal/store` 写一个 `recovery-ledger.json`，上限 64 KiB；单写者、修订号单调；
  临时文件 → `fsync` → `rename` → `fsync` 父目录；写失败把可用性置为 `ledger_unavailable`。
- **影响**：账本可整份校验、整份替换；不允许写入树、输入正文或命令输出；Windows 跳过目录
  `fsync`，所以 Windows 不作为耐久证据。容量上限要求只存摘要，不存历史。

## D11 full/delta 以“确认基线 + 70% 收益”为准

- **背景**：delta 必须相对一个客户端可重建的基线；无基线或收益不足时，delta 只会增加复杂度
  与误用风险。
- **选择**：只有被接纳的 `observe(O)` 或 `act` 认领 full O 才能确认基线；`observe(P)` 强制
  full；delta 仅在基线同会话、版本差 <12、基线龄 <180s 且编码小于 full 的 70% 时使用，
  否则发 full。提交前在锁内重验会话/代次/工作/采集龄。
- **影响**：客户端必须保存基线并按 `delta.base` 应用；服务端不维护“每个客户端一份基线”，
  同一会话只有一个确认基线；不可表达的差异自动退回 full，不产生非法 delta。

## D12 显示助手脱离服务进程，恢复以多源证据放行

- **背景**：虚拟屏属于配对 App；若 root 显示助手随服务进程退出，服务崩溃时旧屏会被系统移除，
  恢复只剩 `display_absent` 路径，无法验证“旧屏仍存在、需要 App 精确关闭”的分支。
- **选择**：helper 以 `nohup setsid` 脱离服务启动，服务被 `SIGKILL` 后仍可存活；恢复先要求
  App 心跳新鲜，再探测旧 display 并下发精确 `close_display`，随后要求 App 同步、双次 absent
  与 TaskList 成功；`ledgerRecordLocked` 在恢复未解决时返回旧账本快照。
- **影响**：服务崩溃后旧屏可能仍在，恢复必须能通过 App 关闭；不能只凭一次探测、计时器或
  “重启即干净”放行。TaskList 不可读或旧 display 仍有任务时保持阻塞，不用 display absent
  覆盖成 confirmed。

## D13 `observe.full=true` 强制 full

- **背景**：契约允许调用方主动要求完整树；若该字段只被接纳不生效，客户端会在需要重建基线时
  收到 delta，产生错误假设。
- **选择**：`observe` 的 `full=true` 与 `observe(P)` 一样强制本次 full；`full=false`/缺省仍
  由服务端按基线、编码成本与语境决定 full/delta。
- **影响**：主动 full 不改变 O 的单向消费与提交门禁；调用方不能靠 `full=true` 绕过会话/显示/
  工作校验。
