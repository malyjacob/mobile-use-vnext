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
  为可读 `key=value` 正文、去 base64 的规范 `Result` 结构化内容与可选 `ImageContent`。
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
