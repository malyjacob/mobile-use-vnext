# 工程决策

状态：2026-11-09。只记录已经体现在工作区代码/脚本中的决定；后续阶段的选择在真正实施前
不预写。每条给出背景、选择与影响。阶段代号只在本页与验证记录中出现，不进入正式文档。

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

## D14 M4 工具工作类别与 detached P

- **背景**：`apps`/`notify` 无会话，`open_app`/`shell` 需要自己的设备工作类别；P3 的
  `registerOperation` 会把当前会话绑定到任意 P，若 `notify`/`apps` 沿用，`end(该P)` 会误结束
  无关的活动会话。
- **选择**：工作类别扩为 `start/observe/act/apps/open_app/shell`；新增
  `registerDetachedOperation`，`apps`/`notify` 的 P 不写 `SessionOrigin`。`open_app`/`shell`
  仍绑定原会话。
- **影响**：`end(appsP)`/`end(notifyP)` 明确返回 `reference_has_no_session`，不会碰到其他
  会话；App 控制视图的 `work.kind` 对未知值已有安全文案，不需要破坏性升级。

## D15 app 清单缓存 60 秒

- **背景**：缓存分案只规定 `apps` 可有可信缓存，没有给出 TTL。
- **选择**：引擎内存单条缓存 60 秒（与 O 输入寿命一致），不持久化；TTL 到期、App 实例变化、
  读取失败、恢复/退出时失效。缓存命中不占工作位置；新读占工作位置。
- **影响**：`apps.captured_at` 永远是真实读取时间；调用方不能用缓存冒充当前采集；重启不
  复活旧清单。

## D16 虚拟会话任务登记与清理

- **背景**：P3 收尾链假设会话从不启动应用；M4 `open_app` 后若仍按“任务非空即未确认”处理，
  每个用过应用的会话都会永久阻塞准入。
- **选择**：虚拟会话在内存登记 `taskEntry`（task id、包名、display、generation、owned、
  proven），账本只存有界 task id/包名/display/proven（≤16 项，无窗口内容）。收尾先按精确
  task id 移除目标屏任务（≤3 轮、每轮重读实时任务），再核验 `dumpsys activity recents`，
  最后关闭显示。主屏会话从不登记/移除用户任务。
- **影响**：`cleanup.tasks`/`recents` 由实时任务与 recents 证据分别确认；未确认时保留
  `task_remove_unconfirmed`/`recents_cleanup_unconfirmed`；恢复屏障对旧 display 上的任务
  先做同样的有界清扫，仍无法确认则保持阻塞。

## D17 慢交付额度

- **背景**：设备工作位置在结果发送前释放；慢连接可让冻结结果无限积压。
- **选择**：HTTP 层独立交付额度：最多 2 份、合计 4 MiB、单份写期限 15 s；需树/清单/诊断
  结果的工具在调用 engine 前预留上界，不足立即 503 `delivery_unavailable`，不排队。
- **影响**：交付失败不改写已提交事实；调用方仍按原 P 恢复；STOP/end/status 不受额度阻塞。

## D18 notify 的 M4 交付边界

- **背景**：设计把消息中心与实际通知交付放在 App；M4 不整理 App 界面。
- **选择**：服务端有界保存消息（256 条 / 1 MiB，内存），`executed` 只确认保存；
  `notification.state=unavailable` 并带 `notification_unavailable`，不伪造 pending/submitted。
- **影响**：M4 可验证保存与去重；App 消息中心接入属后续阶段，正文不进日志/错误/账本。
- **状态**：由 D23/D24/D25 取代；交付通道与 `pending` 语义以新决定为准。

## D19 apps 的应用清单制品

- **背景**：需要本地化应用名，Android shell 没有稳定的标签来源。
- **选择**：固定 `third_party/android-applist/applist.dex`（`app_process`，shell 身份，
  `package<TAB>base64(label)`），带 SHA-256、源码、构建脚本与部署接线；服务按固定哈希懒推送。
- **影响**：标签只进入 `apps.items[].name`，不写日志；换制品必须同时更新哈希、源码与说明。

## D20 图片作为单次观察的附加材料

- **背景**：`observe`/`act` 的 `image=true` 原先返回 `501`；MCP 要求 `ImageContent`。
  设计规定图片与树同次采集、同次提交，但允许分层失败，且图片不能成为第二份页面事实或
  独立图片身份协议。
- **选择**：新增纯算法包 `internal/imaging`（PNG 解码、等比缩小一次、JPEG 编码一次）。
  `engine` 的同一采集改为：确认显示身份 → 以运行时 SFID 读取源图 → 再确认同一显示身份 →
  读取层级 → 编码图片 → 原提交门禁。核心树失败不发布 O；核心树有效而图片失败发布
  `status=partial`、`missing=["image"]` 的有效 O；图片成功而树失败只给
  `read_only=true` 的只读图。附加图片时 `actionable_until` 取图片读取开始与树采集开始中
  更早者。图片预算固定为最长边 1200px、JPEG 质量 80、输出 ≤512 KiB、图树起点跨度 ≤3s、
  源图 ≤24 MiB/16M 像素；不开放每次调用的质量、尺寸、格式或重试参数。
- **载体**：HTTP 以 `observation.image.base64` 携带本次图片字节（同一 `observation.image`
  元数据），客户端解码后清空该成员；MCP 投影为 `ImageContent`，文本与结构化内容不再含
  base64。图片错误只出现在 `observation.image.error`，不改写主动作或核心树。
- **影响**：`internal/engine` 只依赖注入的 `ImageEncoder` 小接口；`cmd/device-service`
  注入 `internal/imaging`；图片字节不进入树、delta、账本、历史或日志；历史图片不可补取。

## D21 MCP 双入口的共用接入层

- **背景**：需要 stdio 与 Streamable HTTP 两个入口，但只能有一套工具注册、schema、严格
  接入与结果投影；MCP 不能绕过服务认证或保存第二份执行权威。
- **选择**：`internal/mcp` 使用 `github.com/modelcontextprotocol/go-sdk v1.8.0`，锁定
  MCP `2026-07-28`，注册固定 12 工具的低层 handler（显式 JSON Schema +
  `additionalProperties=false`）。参数由 `internal/protocol` 的严格解码器接入；结果投影
  为可读 `key=value` 阶段事实正文（D32 追加可选树块）、去 base64 的规范 `Result`
  结构化内容与可选 `ImageContent`。
  `isError` 按“主动作是否达成”分工具投影：`executed` 不因后树/图片/清理失败改判为错误；
  `not_executed`/`uncertain` 为真；`status`/`diagnose` 的正常查询事实为假；无有效 O 的
  `observe` 与不可用 `inspect` 为真。
- **取消**：`internal/mcp` 在发送前用 `client.NewOperationID()` 生成 P 并传给客户端；
  stdio 的 `notifications/cancelled` 与 HTTP 请求 SSE 流关闭都取消本次调用上下文。有本次
  P 的工作调用在锁外上下文中有界 `end(P)`（5s），只读工具只取消读取；取消结果保留已知
  服务事实，但不声称设备动作已撤回。MCP 请求 ID 不作为公开恢复编号。
- **HTTP 入口**：使用 SDK stateless Streamable HTTP（每次请求独立会话，
  `PropagateRequestCancellation`），外层要求私有配置的宿主 bearer 令牌（与 `agent_token`
  不同）并在出现 `Origin` 时精确匹配允许列表；令牌错误 401、Origin 不匹配 403。
  stdio 的 stdout 只写协议帧，日志写 stderr。
- **影响**：两个入口共用同一 handler 与投影，只差传输；MCP 进程只依赖 `internal/client`，
  不直接访问 engine/android/store；服务看到的 Agent 归属仍只由私有服务凭据决定。

## D22 Go 客户端的 P、错误与图片

- **背景**：服务到 SDK 的响应可能丢失，错误不能降成无归属字符串；SDK 需要在发送前持有
  本次 P，并保留图片元数据与字节。
- **选择**：`internal/client` 使用 `internal/protocol` 的规范请求编码器与响应解码器；
  工作工具在发送前生成 `P`+24 位十六进制（`NewOperationID`），可由 `WithOperationID`
  指定已持有 P。每条逻辑调用至多一次 HTTP 请求，不重试、不跟随重定向、不排队。传输失败
  返回 `Error{Transport=true, OperationID=<P>, Code="transport_unavailable"}`；服务结构化
  错误保留 `Result` 与稳定 code；响应上限 8 MiB。图片从
  `observation.image.base64` 解码到 `Response.Image` 并清空结果中的载体。`Discover` 只做
  `GET /health` 与 `POST /api/status {}`，把服务实例、契约版本、`can_start`/`blocking_code`
  与自身会话投影成只读材料。
- **影响**：客户端取消 `ctx` 只停止读取/交付，不自动 `end`；取消到有界 `end(P)` 的映射由
  MCP 接入层完成。客户端不保存服务事实副本，`status(P)` 仍是服务端权威。

## D23 notify 消息经控制同步进入 App 消息中心

- **背景**：notify 之前只保存消息，`notification.state=unavailable`，App 消息中心是空骨架；
  设计与用户要求 Agent 告知的消息真正到达手机用户，但不新增 Agent 工具、参数或已读轮询。
- **选择**：复用 `POST /v1/control/sync`。响应增加有界 `messages`（`id`、`seq`、`text`、
  `created_at`，每拍最多 8 条 / 32 KiB 正文，按创建顺序重发未确认项）；请求增加
  `message_acks`（消息 ID 数组，至多 32 个，未知/已释放 ID 忽略）。确认即释放服务端保存。
  `notify` 的 `notification.state` 改为 `pending`（已保存、已入队待投递），不再返回
  `notification_unavailable`；`executed` 仍只表示保存，不表示已显示或已读。
- **契约**：sync 增加必需结构字段，`contract_version` 前进为 `"2"`；服务、App、Go client、
  MCP、docs 与构建常量同步。旧 App 的普通同步被 `409 contract_mismatch` 拒绝，独立 STOP
  不校验普通契约，仍然可用。
- **影响**：消息正文只出现在 sync 响应与 App 私有存储，不进入日志、错误、账本、恢复记录或
  发布清单；App 去重按消息 ID，重复投递不会重复通知或重复列表项。

## D24 未确认消息跨服务重启保留

- **背景**：服务重启会丢内存消息；“已保存”若只在一次进程内成立，用户会丢失 Agent 已确认
  保存的内容。消息不是执行权威，不能因为恢复账本损坏而阻塞恢复。
- **选择**：新增独立私有存储 `<state_dir>/messages.json`（格式
  `mobile-use-vnext-messages`，版本 1），有界 256 条 / 1 MiB 正文 / 2 MiB 文件；原子替换，
  `0600`；只由 messagebox 单写者读写。`next_seq` 随文件持久化，ID 跨重启不重复，从而让
  App 能对重投消息去重。启动时加载；加载失败记录受控码并保持运行期内存交付，同时停止覆盖
  该文件（不销毁现场）。无可读消息文件时以当前毫秒时间初始化 ID 起点，避免 App 仍保留的
  旧 ID 与新消息冲突。服务重启后未确认消息继续按原 ID 投递。
- **影响**：正文不写恢复账本、不写日志；消息存储故障不改变 STOP、清理或准入；确认后消息
  被删除，不保留历史。App 本地已读状态只属于 App。

## D25 App 本地已读不上报

- **背景**：Agent 只需要低频告知，不应引入已读轮询、优先级或用户回执通道。
- **选择**：App 在用户打开消息中心/详情时本地标记已读并更新角标；不向服务或 Agent 回报
  已读，也不新增任何 Agent 可见的已读字段。App 只确认“已保存到本机”，用于释放服务端队列。
- **影响**：服务不知道用户是否已读；`notify` 结果永远不声称已读。App 重启后本地已读保留在
  私有消息文件；删除 App 数据会丢失本地已读与消息。

## D26 单一版本源与整组发布清单

- **背景**：契约号、发行号与 App versionName 分散在 Go、Java、PowerShell、docs 与
  AndroidManifest 中，手改会漂移；发布需要按组核验而不是只看进程存活。
- **选择**：仓库内 `release/versions.json` 是发行号、契约号、Android versionCode 与基线
  源码提交的唯一来源。`scripts/generate-version.ps1` 从它生成
  `internal/protocol/version_generated.go`、`GeneratedVersion.java`、
  `res/values/strings_version.xml`，并同步 `AndroidManifest.xml` 的 versionCode；
  `-Check` 模式只校验不写。`scripts/build-release.ps1` 产出整组产物与
  `build/release/manifest.json`：release/contract、实际源码提交与脏标记、Go 版本、MCP
  SDK/协议、每个产物的 SHA-256/字节数、Android 包名/versionName/versionCode、工具目录
  指纹。清单不含 `.state/`、令牌、账本、消息、截图、树或日志。
- **影响**：构建与部署先核验清单；版本号只在 `release/versions.json` 手改。发行号与契约号
  不同义：`release_version` 只定位产物，`contract_version` 决定普通接口兼容。

## D27 发布保留 App 私有数据

- **背景**：旧部署脚本卸载再安装 App，会清空消息、已读、连接配置与停止意图；用清库解决
  签名或配对问题会丢失用户数据与未同步停止。
- **选择**：部署只做覆盖安装（`adb install -r`），不卸载、不清除 App 数据；`connection.json`
  由部署重新写入并保持 root-only。签名不一致或版本降级导致安装失败时停止发布并报告，
  不回退为卸载；回退是另一轮受控部署。发布顺序固定为：准备并核验整组产物 → 暂停普通
  准入并结束旧会话/确认旧资源 → 停止旧服务/App → 安装服务与 App → 启动并核验实例、契约、
  STOP、恢复屏障 → 重启实际 MCP 宿主并重新发现工具。
- **影响**：App 私有消息、已读与停止意图跨升级保留；破坏性契约升级仍要求先完成旧会话
  停止与资源确认，过渡期 `can_start=false`。发布完成需分别记录版本匹配、App 配对、恢复
  屏障、STOP 可用与 MCP 宿主刷新。

## D28 控制呈现优先级与本地风险

- **背景**：App 已有 control_view 骨架，但“App 当前持有虚拟屏”被当作清理风险，活动会话
  期间会误显示“资源清理未确认”；停止、失鲜、清理与活动工作也需要固定优先级。
- **选择**：主文案优先级固定为：本地停止待同步 > 连接失鲜 > 清理风险（服务
  `cleanup.pending/unconfirmed` 或 App 释放未确认）> 活动工作/会话 > 待命。App 持有当前
  会话的虚拟屏本身不是风险；只有本机停止后仍持有、释放未确认或服务报告清理未确认才算
  风险。Activity、常驻通知与悬浮入口消费同一份不可变快照；“上次会话/上次操作”只是 App
  内存中的历史展示（只保留已结束事实），不参与主文案、准入或任何授权。
- **影响**：断线或停止后不会用缓存 active 冒充当前运行；本地停止、停止同步、清理证据与
  最近历史分开显示；不新增第二套会话状态或网络轮询。

## D29 提案生命周期：讨论分析 → 确认提案 → 执行 → 登记归档

- **背景**：一次改动通常从设计质疑开始，经取证、路线权衡与判据细化才稳定，但此前只有
  零散的提案/验证/决策文件：没有规定何时需要提案、提案必须写什么、实现与归档的先后。
  实际出现过三类风险：把“维持现有语义”写进提案却没核对基线、`docs/` 里凭印象写行为、
  归档头需要一个只有在提交后才存在的 hash。
- **选择**：把生命周期固化为 `maintenance/process.md`，在 AGENTS.md 增加第 16 条作为强制
  入口，提案格式由 `maintenance/proposals/template.md` 给出。四段与出口条件固定；判据是
  “三个月后只靠代码、测试和提交信息能否解释为什么是这样”。**先提交实现、再提交归档**：
  归档头写实现提交 hash，且文档审计门禁只覆盖实现提交，归档提交不改 `docs/`。提案状态
  只有“提案，未实现 / 已实现 / 部分实现”三种；部分实现必须逐条写缺口与理由。流程本身
  的变更直接改规范文件并记入本页，不开提案。
- **影响**：讨论阶段不落盘，避免半成品提案被当成承诺；提案移动后必须重写仓库相对链接并
  做存在性检查；验证记录与 `status.md` 分别在归档时更新，单测通过不得写成实机结论。

## D30 流程按“分诊 + 可跳过步骤”组织，并覆盖缺陷修复

- **背景**：D29 的四段是直线，隐含“每个变更都值得一轮讨论并落一份提案”。实际有相当一部分
  是缺陷修复：问题已由报错或现象给出，缺的是根因而不是方案；另有小改动，讨论只剩形式，
  但完全不跳又会把流程变成仪式。
- **选择**：流程改为“分诊 → 分析 → 提案（可选）→ 执行 → 归档”。分诊判据一句话：问题能
  不能用“现行规范说得对，实现走偏了”解释——能是缺陷，不能是取舍。缺陷的分析是归因（先红
  的复现用例、`file:line` 根因、同类核查），**不可跳过**；取舍的讨论是路线对比，可跳过，
  条件是取舍已被规范唯一确定、影响面在单包或单文件族内、只有一条可行路线。**只有讨论与
  提案可以跳过**，且必须在提交信息里声明理由；超过一屏的取舍不跳。根因落在现行规范上的
  缺陷转为提案，不在实现里暗改语义。无提案的修复写 `maintenance/verification/` 六节记录，
  可与修复同一提交。
- **影响**：不再要求每个变更都产出提案，但“跳过”必须留痕；缺陷修复多两项硬要求——复现
  用例先红、同类核查；验证记录从“有则更好”变成必需；`docs/` 审计门禁与提交切分不变。

## D31 文档审计 subagent 的等待规则

- **背景**：在一次文档审计中，实现 agent 把审计 subagent 尚未返回误判为卡住，连续中断并
  重新发起多次；每次都没有等到结论，浪费时间，也没有形成可复用的审计结果。
- **选择**：在 AGENTS.md 增加第 17 条，并写入 `maintenance/process.md` §6/§9/§10：审计
  subagent 运行期间等待其结论；只有明确失败、无响应且记录原因时才重新发起，并在提交说明
  中注明。审计期间可以并行处理与审计文件无关的工作，但不得改动正在审计的 `docs/` 文件。
- **影响**：审计结论成为提交门禁的一部分；中断/重启必须有可解释的原因；减少重复启动与
  无结论的审计。

## D32 MCP 文本结果附带确定性树块

- **背景**：MCP 规范要求返回结构化内容的工具同时把序列化结果放进 `TextContent`；部分
  宿主只把 `TextContent` 交给模型。原投影的正文只有阶段事实，模型在主循环里看不到树，
  无法从 `observe`/`act` 的结果选择 `ref`，`inspect` 的命中内容也不可见。
- **选择**：`internal/mcp` 在既有 `key=value` 正文之后追加一个纯函数渲染的树块：观察结果
  渲染 full/delta，`inspect` 渲染 tree/subtree/query；`structuredContent` 与 `/api/*`
  不变。树块使用行式语法、2 空格缩进、JSON 字符串字面量转义与显式 `false`/`""`/`[]`/
  `null`；块尾 `end tree`/`end inspect` 暴露宿主截断。delta 不展开：适配器没有基线树，
  块头 `base=` 是唯一锚点，新 delta 覆盖旧 delta。观察块上限 24 KiB、补看块 32 KiB，
  超限整块省略并输出 `omitted`，不输出半棵树。该选择扩展 D21 的可读正文条款：正文不再
  只有 `key=value`，但仍只是本次 `Result` 的投影，不新增状态、工具、参数或契约版本。
- **影响**：树块只进响应，不进日志；工具 description 变化会改变 `CatalogFingerprint`，
  发布清单在下次发布时重新生成。`docs/mcp.md` §3/§3.1 承载语法与预算，
  `docs/interface.md` §12 补记 `inspect` 的两处既有预算口径。

## D33 MCP 结果模式共享无损事实集

- **背景**：旧文本摘要遗漏 shell 输出、apps 条目与诊断 timeline；D32 的树块上限造成第二处
  信息丢失，取消说明又只出现在文本。用户已批准 `mcp-result-modes` 提案的 R4 路线。
- **选择**：`both`（默认）/`text`/`structured` 共用确定性 canonical Result；完整文本采用
  `result v1`，结构化错误辅助采用 `diagnostic v1`，两者各有终止行。完整文本与结构化内容
  无损表示同一 `(A,IsError,ImageContent)`，可读块不可表示时 JSON Pointer 兜底，不省略。
  取消在 `error.details.mcp.*` 中记录且覆盖 `isError=true`，不改写执行事实。HTTP 每请求头
  与 stdio flag/env 选择模式；非法值在工作前拒绝。字段清单、独立解码与全工具 fixture
  构成长期等价门禁。D32 的块语法保留，24/32 KiB 省略策略由本条取代。
- **影响**：MCP 文本格式发生不兼容变化，工具目录/描述/schema/指纹、`/api/*` 与契约号不变；
  为保持目录指纹，description 中旧 `omitted` 提示保留，由 `docs/mcp.md` 明确格式边界。
  `both` 承担双份编码开销，宿主截断仍由宿主处理；不增加日志正文或设备执行权威。

## D34 退休会话有限善后且不永久封锁准入

- **背景**：旧 cleanup、未确认 UI/shell 或 legacy checkpoint 曾可长期占用当前槽位并把整台
  服务挡在新会话之外；一次 recents 清理失败即可无限期返回 `cleanup_unconfirmed`。
- **选择**：批准 `cleanup-without-permanent-gate` 取舍。当前会话与退休记录分离；撤权后旧
  逻辑交接最多 5 秒，独立物理补偿总计 12 秒、最多三轮。`pending` 表示有限处理仍在运行，
  `unconfirmed` 表示主动处理已停止；二者都不替代事实，也不永久决定新会话准入。账本版本 3
  最多保留 8 条退休明细、明细合计 24 KiB，整个账本仍不超过 64 KiB。新会话只由当前账本
  可写性、App/模式/STOP、设备与交互能力、活动会话和当前普通工作决定。未知 P 的 `end` 返回
  `operation_unknown`，同时封闭该 P 的未来接纳，但不改写过去执行事实或结束其它会话。
- **影响**：旧 Android 调用与新会话可能在物理时间上重叠，已派发命令不保证撤回；精确清理
  仍须重验 boot、App 实例、原创建命令、display/generation 和任务归属。旧 cleanup、legacy
  UI 或容量不足不再作为永久准入理由，运维也不以手机重启、清账本或清 App 数据作为出口。
  共同契约使用版本 5，App 的 retired attempt 不成为 current descriptor，也不决定
  `mode_ready`。

## D35 契约 6：act 重验失败交付新观察

- **背景**：`act` 在认领观察之后、派发输入之前做实时重验；失败时它已经读取过一帧，但该帧被
  丢弃，调用方只能再 `observe` 一次、再发一个新的 `act`。一次重验拒绝因此消耗三次调用、三个
  新 P 与三次采集。
- **选择**：批准 `act-revalidation-observation-delivery` 取舍。重验失败且已取得可用新帧时，
  服务把该帧发布为新的 ready 观察随失败结果返回，并把 `recovery` 从
  `observe/P/retired` 改为 `retry/O/effective`。交付只在 `target_changed`、
  `foreground_changed` 与重验阶段 `node_not_found` 发生；无帧、前台不可确认、撤销、树超限、
  帧龄超限或控制 UI 出现在目标虚拟屏时保持原行为。交付复用同一帧，不重新采集、不做收敛采样、
  不确认基线、不派发输入。delta 基准由 `forceFull = !obs.Full` 决定：满足 delta 条件时，引用
  full O 的交付 delta 其 `base` 就是刚刚失败的那棵树，引用 delta O 时改发 full。
- **影响**：同一错误码的响应形状与 `recovery` 取值改变，按 `docs/release.md` 的判据
  `contract_version` 前进为 `6`；共享同一发布闸门，未新增请求字段、未新增恢复入口取值。
  MCP 的入参 schema 与投影机制不变，但 `act` 的描述与目录指纹随本次契约变更，错误结果现在
  可以带树块。`retry` 的含义改为“结果指向的那个 ready O 可用”，不再以“本次请求未认领观察”
  为前提。

## D36 契约 7：拆除工作期限与未决输入的会话级封锁

- **背景**：一次 `adb shell` 传输抖动使 UI 输入回执变成 `DispatchUnknown`，预写停在
  `may_dispatch`，于是包括 `observe` 在内的全部普通工作被拒；`act` 之后第 50 秒会话又被
  `work_deadline` 撤销。两者都不是必需事实，而是把毫秒级投递窗口表达成永久状态的后果。
- **选择**：批准 `remove-work-ui-deadline-lockdown` 取舍。会话只因外部原因结束（`end`、
  STOP、租约、App 实例变化、服务退出、启动阶段失败）；`work.Deadline` 到期只冻结该 P 并
  脱离工作位；输入窗口在调用返回时关闭为 `terminated`/`not_dispatched`/`unknown`，已关闭
  窗口不构成准入屏障；受管 shell 屏障保留但以
  `DeadlineAt + ShellConfirmTimeout + 3s` 为上界，到界结案为 `termination=unknown` 并让位，
  身份进入单槽孤儿位供收尾再试一次精确终止，且不把 `cleanup.execution` 写成 `confirmed`；
  删除不可达的恢复期重启要求及其 reason 映射。私有账本版本 3 升为 4（UI 状态新增 `unknown`）。
- **影响**：`contract_version` 前进为 `7`（默认行为与执行语义变化，且“除外部不可抗力外，
  内部原因不得撤销会话、不得让会话无法继续使用、不得让服务锁死到只能重启手机”作为规范性
  语句进入 `docs/architecture.md` 与 `docs/interface.md`）。`keepalive` 的既有语义（只承诺
  租约，不承诺保活超过 1 小时硬上限）同时写入契约面，行为不变。保留风险：输入不提供幂等，
  被放弃的 `shell` 可能与后续工作物理重叠，回执未知后应立即 `observe` 收敛。

## D37 契约 8：显式重设会话租期并取消累计硬上限

- **背景**：Agent 长时间思考或等待用户时可能来不及在 120 秒内续租；旧 `keepalive` 只能把
  截止时间单调推到 `min(now+120s, started_at+1h)`，既不能提前申请较长等待窗口，也不能
  缩短长租约，持续保活仍无法跨过累计一小时。用户确认这是生命周期策略取舍，不是实现缺陷。
- **选择**：批准 `explicit-keepalive-duration` 取舍。`start` 初始化、激活与首个 full 观察
  成功发布使用五分钟租期；其他普通活动仍只在既有触发点取
  `max(expires_at, now+2分钟)`，不继承显式时长；新 `keepalive` 的可选 `duration_minutes`
  为 1～120 的整数，省略为 2，engine 在同一状态锁内取应用时 `now` 后直接覆盖
  `expires_at = now + duration_minutes`，可延长或缩短；同一可识别 P 重放不再次重设。
  删除 `SessionHardLimit`/`HardExpiresAt`，会话只有一个 `ExpiresAt` 权威；到期、STOP/end、
  观察有效期、基线与工作期限保持各自边界。严格拒绝 `null`、字符串、布尔、非整数、
  0/负数/121 及未知/重复/大小写字段。
- **影响**：`contract_version` 前进为 `8`，发行 0.8.0、Android versionCode 10；旧调用方不能
  再假定显式 `keepalive` 只增不减，默认两分钟调用可能主动缩短五分钟启动租期或长租约。
  MCP schema/描述、Go 客户端 `KeepaliveWithDuration` 与正式文档同步；工具数仍为 12，目录
  指纹随描述变化。保留风险：未提前申请足够时长仍会到期回收；显式长租约会增加遗弃会话
  占用时间，调用方应在完成后 `end`。
