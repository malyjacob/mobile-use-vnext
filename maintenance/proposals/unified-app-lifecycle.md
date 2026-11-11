# 提案：用统一 app 工具管理应用发现、任务认领交接、关窗与进程终止

状态：提案，未实现。
基线提交：`cbe1668cb3ca78c61a24cbcdee527d4ae96fa604`（分支 `master`，2026-11-12）。
适用范围：HTTP、Go 客户端、MCP stdio/Streamable HTTP 的应用发现与生命周期调用；虚拟屏任务归属、STOP/end 收尾、恢复账本；手机与构建主机配套交付。
影响范围：`internal/protocol`、`internal/device`、`internal/android`、`internal/engine`、`internal/store`、`internal/httpapi`、`internal/client`、`internal/mcp`、`android-control` 的状态文案/测试目标、`scripts/mcp-probe`、版本生成、构建部署、正式文档及设计源头。
契约（`/api/*` 字段与 `contract_version`）：改变。以 `/api/app` / MCP `app` 的七个 `kind` 替代 `apps`、`open_app`；工具数 12→11；共同契约 8→9；拟发行 `0.9.0`、Android versionCode 10→11；私有账本格式 4→5。

本文引用的函数、行号、默认值均以以上基线为准；代码前进后须重新核对，重点是 `internal/engine/{openapp,cleanup,recovery,ui_execution,ledger}.go`、`internal/mcp/{server,result_text,result_value,result_mode}.go`、`scripts/{build-release,deploy,verify-manifest}.ps1`、`scripts/mcp-probe`。本提案中的新增文件、字段、命令选项与验收要求均为拟实施内容，不能当作当前能力。

## 0. 摘要

应用生命周期统一为 `app(kind=find|list|open|claim|handoff|close|kill, ...)`，各分支严格限制参数、模式和影响范围。
保留既有 O/P、单工作位置、STOP 先撤权、执行/观察/终态/收尾分层；不增加第二份设备权威。
主屏共享只允许 find/list/open；虚拟屏显式认领主屏 live task、交接单个任务、关闭会话任务或终止合格应用的精确进程。
返回采用现有 Result 外层与 `app.kind` 判别对象；MCP 三模式必须无损表达同一个 AgentResult。
实施交付包括共同版本更新、手机整组覆盖部署、Windows MCP 与 Go 探针重建替换和实际宿主重新发现；本轮只写提案。

## 1. 问题

### 1.1 现象与代价

1. 当前 `open_app` 只能复用目标屏同包 live 窗口或在目标屏新建；不能显式把用户主屏的现有任务及页面栈移入虚屏，不能显式交还。
2. 已有 task 移除与 recents 核验只用于后台收尾；调用方没有关窗、受限进程终止、live task 列举入口。按包选择时也无法先处理同包多任务歧义。
3. 逐一新增同义工具会扩大目录；用户选择一个统一 app 工具、用 kind 区分。合并必须保留查询与变更的不同 P 绑定、O 消费、取消与 IsError 判据。
4. 新增返回字段若只进入 JSON、未进入完整文本，text 宿主将丢失选择 task、判断部分成功与恢复所需事实。反之，文本独有提示也会让 structured 宿主丢信息。
5. 只部署手机服务不能更新仍运行的主机 MCP 进程；只重编译也不能证明手机/主机的实际目录、契约与探针相互匹配。

### 1.2 证据链

以下为代码阅读证据，不是本提案的测试或实机结论。

| 现行事实 | 基线位置 |
| --- | --- |
| open_app 只读预检、O 认领、目标重验、primary/virtual 分流 | [openapp.go](../../internal/engine/openapp.go#L33)；复用/新建 [L152](../../internal/engine/openapp.go#L152)；主屏不登记 [L216](../../internal/engine/openapp.go#L216) |
| Task 没有 root task ID；解析器只保留显示位置与 leaf 事实 | [types.go](../../internal/device/types.go#L204)、[dumpsys.go](../../internal/android/dumpsys.go#L250) |
| FocusTask 带派发回执，RemoveTask 仅返回 error | [launch.go](../../internal/android/launch.go#L114)、[L132](../../internal/android/launch.go#L132) |
| 任务账本没有 ownership、origin_display、迁移责任 | [ledger.go](../../internal/engine/ledger.go#L113)；恢复项一律 owned [openapp.go](../../internal/engine/openapp.go#L292) |
| cleanup 冻结原任务，再结合本屏 live task 与 recent-only 精确 ID 删除 | [cleanup.go](../../internal/engine/cleanup.go#L29)、[L536](../../internal/engine/cleanup.go#L536)、[L649](../../internal/engine/cleanup.go#L649) |
| UI 派发前保存责任，落盘后与 STOP 再竞争 | [ui_execution.go](../../internal/engine/ui_execution.go#L84) |
| 查询 apps 使用 detached P，成功/失败均不带主 outcome | [apps.go](../../internal/engine/apps.go#L24)、[L134](../../internal/engine/apps.go#L134)、[L155](../../internal/engine/apps.go#L155) |
| recovery 把 act/open_app 名字写入消费者判定；uncertain 指向 status | [recovery.go](../../internal/engine/recovery.go#L80)、[L88](../../internal/engine/recovery.go#L88)、[L112](../../internal/engine/recovery.go#L112)、[L377](../../internal/engine/recovery.go#L377) |
| status 不采集新证据，操作视图回放摘要而非原结果材料 | [status.go](../../internal/engine/status.go#L9)、[engine.go](../../internal/engine/engine.go#L1031) |
| MCP 按整工具分配 P/取消策略，按工具名判断 IsError | [server.go](../../internal/mcp/server.go#L128)、[L197](../../internal/mcp/server.go#L197)、[L383](../../internal/mcp/server.go#L383) |
| 文本分类表只有旧 app 单任务字段，复合 JSON 可以无损承载新增嵌套成员 | [result_text.go](../../internal/mcp/result_text.go#L26) |
| 三模式既定的完整编码与 diagnostic 边界 | [MCP 契约](../../docs/mcp.md#L37)、[模式提案](implemented/mcp-result-modes.md#L18) |
| 共同版本为 0.8.0 / contract 8 / Android 10；账本为 v4、64 KiB | [版本源](../../release/versions.json#L1)、[ledger.go](../../internal/engine/ledger.go#L5)、[L252](../../internal/engine/ledger.go#L252) |
| 手机构建只产生 linux/arm64 服务与适配器；Go 探针默认使用主机 exe | [build.ps1](../../scripts/build.ps1#L42)、[probe/main.go](../../scripts/mcp-probe/main.go#L39) |
| 现 probe 的 HTTP 模式启动本地子适配器，不能当作已部署手机适配器的验证 | [probe/main.go](../../scripts/mcp-probe/main.go#L108) |
| verify-m5 会重生成主机 HTTP token，旧构建分支不固定目标/产物身份；启动日志仍写 tools=12 | [verify-m5.ps1](../../scripts/verify-m5.ps1#L103)、[L125](../../scripts/verify-m5.ps1#L125)、[main.go](../../cmd/device-mcp/main.go#L125) |
| 源目录指纹包含顺序、描述与嵌入 schema，不能对 SDK 重排后的 tools/list 字节直接套同一哈希 | [catalog.go](../../internal/mcp/catalog.go#L14) |
| 发布清单记录当前 HEAD、dirty、目录指纹、产物；部署覆盖安装保留私存 | [build-release.ps1](../../scripts/build-release.ps1#L172)、[发行契约](../../docs/release.md#L89) |

旧项目参考位置（本机同级仓库 `mobile-use-platform`，调研基线 `225dd6a020e1343e0eb94f821b03ae101341dc93`）：

- `service/src/mobile_use/android/task_lifecycle.py:377`：move 使用 root ID，要求只有一个 leaf。
- 同文件 `:501`：borrow 精确移动、预登记意图；`:875`：单窗口 return；`:736`：close 与 recents 分开；`:1011`：live owned、他屏、前台、服务/播放检查与精确 PID kill。
- 同文件 `:684`：primary 禁止窗口生命周期变更；`:598`：state_preserved 仅比较顶部组件。
- `docs/verification/2026-11-01/task-lifecycle.md:39` 与 `2026-11-02/mcp-toolset-redesign.md:21` 是旧项目受控实机记录，不构成本提案在 vNext 的实测证明。

### 1.3 为什么直觉会出错

- 包名不是 task 身份；同包可以有多个窗口，多个屏的窗口可能共用进程。当前屏有 task 只是 kill 的必要条件。
- `am display move-stack` 与 remove/focus 的参数层级不同，不能拿 leaf ID 直接搬 root，也不能整体搬含多个 leaf 的 root。
- 搬移后 live 查询暂时看不到 task，不表示可以按旧登记清它的 recents；交出责任必须在账本与退休收尾中保留。
- 一个工具的 MCP 元数据不能自动区分只读 kind。若仍按工具名统一取消，取消 list 会误 end 原会话。
- `diagnostic v1` 是错误摘要，不能拿它与 structuredContent 做完整等价比较。
- 已 finished 的 uncertain 原 P 不因反复 status 取得新设备证据；新观察也不能追认原动作执行成功。

## 2. 目标与非目标

目标：

1. 七个 kind 在 HTTP、SDK、MCP 使用同一严格契约；最终工具数 11，移除 apps/open_app 路由、注册与客户端公开同义入口。
2. 主屏/虚屏权限可判定；不接受任意目标显示或切换模式参数，不静默跟随到主屏。
3. 以 task ID 归属、root/leaf 区分、boot/App/display generation 与迁移意图保证原责任隔离。
4. 每个结果可判断主动作、当前可观察页面、任务/进程后验与下一步；部分执行不丢已知事实。
5. text、structured、both 对同一份结果逐字段无损对应；测试与探针分别证明编码和实际传输。
6. 整组升级到 0.9.0 / contract 9 / Android 11，重建手机、Windows MCP、Go 探针，核验实际运行入口与目录。

非目标：任意 Activity/Intent 启动、任意 display 迁移、复制任务、恢复 recent-only 页面栈、跨会话继续控制、force-stop、按 UID/宽泛进程名杀进程、自动重放副作用、改变 end 默认清理、STOP 等待交还、Dozing 调查、跨设备兼容矩阵。本轮不改生产代码、生成版本、构建、设备、正式 docs 或设计源头。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 最小保持 | 只保留现 apps/open_app，其他依靠 shell | 无新增契约，但调用方自行写危险的全局命令与核验 | 无统一归属、映射和错误恢复 |
| R2 多个直接工具 | find/list/open/claim/handoff/close/kill 各自注册 | 入口直观，但工具目录扩大、重复说明与接纳容易漂移 | 查询取消、模式权限和返回惯例各自演变 |
| R3 统一 app，kind 严格分支（选定） | 合并 apps/open_app，并增五个 kind；内部复用引擎职责 | 分支 schema、P 类型、IsError、文本字段表必须同步改 | 只改工具名时查询被当写动作、文本丢字段 |
| R4 扩入现 act | 在 act.operation 中加入应用管理与查询 | 少一个工具，但输入节点操作混入无会话查询与任务生命周期 | O 必填和查询无 O 相互冲突，工具职责难以解释 |

选择 R3；不同 kind 是同一工具的互斥分支，不是任意动作列表，不提供批量 kind 队列。kind=close 的包级多 task 仍是一次有界计划。

## 4. 判据

### 4.1 入口、身份与严格请求

公开入口为 `POST /api/app` 与 MCP `app`。七个 kind 大小写敏感，均为小写。JSON 接入继续拒绝未知字段、重复键、大小写变体、尾随第二个值、类型混用和未声明 null。请求体上限沿用应用工具 8 KiB。根对象必有 kind，各分支只接受下表成员。

| kind | 必填（除 kind） | 可选与默认 | 不接受 |
| --- | --- | --- | --- |
| find | 无 | query：省略为全部可启动应用，≤256 Unicode 码点，沿用字面折叠子串 | reference、observation_id、target、app、scope |
| list | reference：本 Agent 活动原会话的 O/P | scope=session；可为 primary；app 为明确包名过滤 | observation_id、target、query |
| open | observation_id：当前 ready O；app：明确包名 | 无 | reference、target、scope、query、创建/借入策略、image |
| claim | observation_id；target | 无 | reference、scope、app、query、任意目标屏、隐式新建回退 |
| handoff | observation_id；target | restore_foreground=false | reference、scope、app、query、任意目标屏 |
| close | observation_id；target | allow_borrowed=false | reference、scope、app、query、杀进程选项 |
| kill | observation_id；target={task_id} | allow_stop_playback=false | 包名 target、reference、scope、app、query、force-stop、任意 PID/信号 |

claim/handoff/close 的 target 为 `{task_id: 正整数}` 或 `{app: 明确包名}`，恰好一种；kill 只接受 task_id，由实时任务解析包名。task ID 是带原会话/显示/boot 检查的选择器，不独立授予权限。list 的 `scope=primary` 在主屏会话与 session 指向同一主屏，不形成第二种授权。

HTTP/SDK 每次新调用仍分配自己的 P；reference 只是定位既有会话。find 的 P 为 detached，不因调用时恰有全局活动会话而绑定；list 的 P 绑定明确解析的原会话。列表不能用于操作后跳过再次实时重验。

MCP 根 inputSchema 继续声明 properties/required/additionalProperties=false，再以 kind 的互斥分支限制成员；服务严格解码为权威，不依赖宿主是否完整实现 oneOf。发布 schema、解码器和客户端编码器必须接受/拒绝同一组样本。

### 4.2 模式与目标范围

| kind | virtual | primary |
| --- | --- | --- |
| find | 无会话应用发现 | 同左 |
| list | scope=session列本虚屏，scope=primary列主屏候选元数据 | 两个scope均列主屏，无任务所有权 |
| open | 复用本虚屏该包 live task，否则本屏新建；不认领主屏 | 沿用同屏打开；不登记用户任务 |
| claim | 主屏 0 → 原会话虚屏；主屏 live task 必须精确/唯一 | 派发前拒绝 |
| handoff | 原会话虚屏 → 主屏 0；每次一个任务 | 派发前拒绝 |
| close | 仅本会话管理、仍在本虚屏的目标；包名为本包全部目标 | 派发前拒绝 |
| kill | 合格的本屏 live owned task 对应应用；见 §4.5 | 派发前拒绝 |

模式来自原会话与当前控制端代次；不新增调用方 mode/display_id、强制接管或主屏回退。控制 App 不能被 virtual open/claim/close/kill 或作为搬移目标；共享 root 无法安全拆分时拒绝。主屏查询不采集主屏树或图片；handoff 后继续观察原虚屏。

### 4.3 task 归属与选择

- task 的稳定选择键为同 boot 下的 leaf task ID；记录 root ID、包名、当前 display、来源 display、ownership、原 epoch/generation 与证明事实。
- `owned` 为会话虚屏资源中可确认的自有任务，`borrowed` 为显式 claim 的主屏任务，`shared` 表示主屏查询结果、主屏打开/复用但未登记的任务，或已交出任务。shared 不进入会话删除名单。
- 本私有虚屏上的新 live 窗口经对账可进入资源登记；已交出/迁移排除项不得因本屏扫描、旧快照或 checkedTaskIDs 重新变成 owned。
- open 维持本屏复用/新建判据；已有多个可复用同包窗口时，优先当前前台，否则按 task ID 升序确定性选择；不把此选择规则用于 claim/handoff。
- claim 按包名在主屏匹配，handoff 按包名在当前会话虚屏匹配；零个拒绝，多个给出排序后的 task_ids 并要求精确选择，不能取第一个。claim 不受 Launcher 是否可解析阻断。
- close 的包级目标在同一工作内冻结为排序后的精确 task 集合；派发前整体校验 borrowed 保护，失败时不得先删一部分。每次实际删除前再核验位置/身份。执行中应用新开窗口不自动加入这次 close，仍属于后续对账与 end 收尾。
- recent-only 条目不是 claim/kill 资格。close 公共入口选择 live 目标，关闭后的 recents 责任留给该次核验及原会话收尾，不按包清除其他条目。

### 4.4 主动作、观察与事实判定

find/list 无 outcome。变更 kind 的主 outcome 只有既有三值：

| outcome | 判据 |
| --- | --- |
| not_executed | 整个请求没有已派发或已确认的主动作副作用；前置拒绝、明确未派发/被设备拒绝 |
| executed | open 的启动/复用、claim/handoff 的精确移动、close 的所有主移除、kill 的原目标进程结束均有完整证据；后观察或独立 recents/前台恢复可另行失败 |
| uncertain | 主动作缺少完整证据，或有部分副作用而整个计划未完整完成；绝不把部分完成写成“什么都没发生” |

回执为 unknown 时，后树或任务消失不追认该回执；有实际派发的主动作必须同时满足本次派发结束与对应因果/任务/进程证据。§4.5 已确认原目标进程集合为空是无信号也 executed 的显式例外。所有结果只承载已知事实，未知的 task_id/display_id 不能填 0（主屏 display_id=0 是合法真实值），也不以 []/false 冒充未知。

state_preserved：open 新建为 false；复用/移动在同一 task 与顶部 Activity 可比较且未变时为 true，已知发生变化为 false，证据不足为 null。true 是尽力延续证据，不保证像素、滚动、网络数据、输入法或未保存表单不变。它不是移动 executed 的必要条件。

### 4.5 kill 的影响范围

派发前必须满足并最终近派发重验：

1. 精确 task 当前仍在原虚屏，属于本会话 live owned；仅缓存 owned 或 borrowed 均不够。
2. 该包在其他显示屏没有 live task；同屏同包还存在 borrowed/非本会话管理的窗口时也拒绝，避免以一个 owned task 为由连带终止借来窗口。
3. 包不是本屏前台；前台未知时拒绝。
4. 前台服务、MediaSession 状态必须可核验；有 FGS/PLAYING 且 allow_stop_playback=false 时拒绝。true 只允许中断已知服务/播放，不绕过身份、live、他屏、控制 App 和未知证据检查。
5. 确认目标用户/包 UID 与进程归属，枚举包主进程及 `package:...` 子进程；每个原进程以 boot、PID、start ticks 等实例身份最终复核。不能按 UID、宽泛前缀、单独 pidof 或历史 PID 批量杀。
6. root 不可用明确拒绝，不隐式降级或再次提权。只对核验后的原实例发送一次精确 `kill -9`；只读复核不重发信号，不自动追杀重启的新进程。

如果合格 live task 存在但已确认目标进程集合为空，沿用旧项目的完成语义：executed，三个 PID 数组为空；不发送 kill 信号。进程读取失败不能走此分支。先 close 掉最后一个 live task 再 kill 必须拒绝。

kill 是应用包进程操作，不能承诺按屏切开同一进程或不影响包内服务；本方案拒绝已知跨屏/归属冲突，并限制当前目标。最终检查与 Android 外部并发仍有时间差，不宣称原子冻结整个系统。

### 4.6 统一返回对象

外层沿用 Result 的公共顺序和字段；app 仍在原来的 app 位置。移除公开 apps 结果成员；不新增 success、独立 advice、自由 warnings、原始命令或设备原文。服务已登记的 app 调用及重复 P 摘要必带 app.kind。适配器在严格解码成功并预分配 P、但服务结果尚未知时，可从该已接纳请求补 app:{kind} 作为调用元数据；不得补 task、落点、PID 等设备事实，也不得声称服务已登记/finished。适配器自身的参数接入拒绝不分配 P、不补 app。HTTP 服务接入拒绝本身无 P；MCP 转发已预分配 P 的调用遇到服务认证/契约/传输失败时，沿用当前适配器惯例保留该 P，它不证明服务接纳。

| 外层 | 规则 |
| --- | --- |
| operation_id | 已登记调用必出；查询也有 P |
| outcome | 只用于五个变更 kind；查询成功/失败均省略 |
| app | kind 判别；本次取得的身份/集合/后验事实 |
| observation | 变更后的原会话屏 full 节点观察；无图；失败保留现有 failed/missing/error；撤权后不发布 |
| error | 主请求拒绝/主动作未确认；code/message/details 沿用受控结构 |
| recovery | 必要时指示下一步，不改变执行/观察/终态/收尾事实 |
| session/cleanup | 相关终态/撤权变化时补充；不把主动 close 的结果塞入 end cleanup |
| operation_state/phase | status/摘要等确有区分需要时出现，不在正常成功里重复常量 |

数组确定为空用 []；仅对明确声明 nullable 的未知值使用 null，未声明 nullable 的未知标量与不适用字段省略。整个集合未取得则省略集合并报告错误，不能 tasks/items=null 或 [] 冒充读成功。本文为响应增加 nullable 字段，不改变请求拒绝显式 null 的规则。

#### 4.6.1 find 与 list

find 必填：kind、items、total、truncated、captured_at。items 每项 name/package，保留原发现语义与 32 KiB 应用清单预算。失败只有已知元数据和 error，无 items、无 outcome、无 session/observation。

list 必填：kind、display_id、items、total、truncated、captured_at、foreground。items 每项 task_id、package、ownership，origin_display 仅有原归属记录时出现；不重复容器 display_id。foreground 复用 present/none/unknown；present 的 task_id 可在现行允许情形为 null。任务可读但前台未知不使任务列表失效。按 task_id 升序排序，total 为过滤后预算前数量，32 KiB 内只返回完整项；未解析完整 inventory 不视为截断成功。

```json
{"operation_id":"P1","app":{"kind":"find","items":[{"name":"示例应用","package":"com.example"}],"total":1,"truncated":false,"captured_at":"2026-11-12T03:00:00+08:00"}}
{"operation_id":"P2","app":{"kind":"list","display_id":12,"items":[{"task_id":42,"package":"com.example","ownership":"borrowed","origin_display":0}],"total":1,"truncated":false,"captured_at":"2026-11-12T03:00:01+08:00","foreground":{"state":"present","package":"com.example","task_id":42}}}
```

以上两行是两个独立响应示例。list 不消费/延长 O，不续租、不确认阅读基线。只读对账更新缓存不搬移窗口、不把主屏查询候选认领进会话。

#### 4.6.2 open、claim、handoff

正常 executed 的 app 必填：kind、package、task_id、display_id、ownership、state_preserved。open 另有 reused（必填布尔）；claim/handoff 另有 source_display_id（必填）。handoff 另有 foreground_restore：not_requested/confirmed/unconfirmed，默认未请求。ownership=shared 表示已交出，之后 end 不得删该任务。

成功后还带现有完整 observation；以下只展示 app 部分，不能作为完整响应 fixture：

```json
{"kind":"open","package":"com.example","task_id":42,"display_id":12,"ownership":"owned","reused":false,"state_preserved":false}
{"kind":"claim","package":"com.example","task_id":43,"source_display_id":0,"display_id":12,"ownership":"borrowed","state_preserved":true}
{"kind":"handoff","package":"com.example","task_id":43,"source_display_id":12,"display_id":0,"ownership":"shared","state_preserved":null,"foreground_restore":"not_requested"}
```

成功时 handoff.foreground_restore 必出，未请求必须为 not_requested。主屏 open 的 ownership 固定为 shared，不因成功打开而取得删除权。

以下矩阵约束首次业务响应（P 摘要与适配器未取得服务响应的调用元数据另见 §4.7/§5.1）。**现场预检已读到的身份可以返回，只有请求中的 app/task_id 不能当作已证明身份**。身份数值与 ownership 未知时省略，不声明 nullable；app 直接标量仅 state_preserved 与 kill.restarted 按本文定义允许 null；复合值中的 close.tasks[*].recents_cleaned 与 list.foreground.task_id 分别沿用各自声明的 nullable，不能把本句扩展成禁止既有 foreground null。display_id 表示该 kind 最后一次已经证明的作用屏；claim/handoff 的结果落点没证实则省略，不能填源屏或请求目标屏。

| kind / 主状态 | 身份与位置 | 其他字段存在规则 |
| --- | --- | --- |
| open executed | package/task_id/display_id/ownership 必出 | reused 必出；state_preserved 必出，值 true/false/null |
| open uncertain | 已核验 package 必出；task_id 只在本次已观察到精确目标时出；只有后验落点与归属已证明才出 display_id/ownership | 已确认走复用或创建分支则 reused=true/false，否则省略；已确定全新任务 state_preserved=false，其余有比较证据为 true/false，无证据为 null；state_preserved 必出 |
| open not_executed | 现场已核验的 package/task_id/display_id/ownership 才出；只解析了请求包名则只有 kind | 未选择分支则 reused 省略；未进行连续性比较则 state_preserved 省略，有实际比较才出 true/false/null |
| claim/handoff executed | package/task_id/source_display_id/display_id/ownership 全部必出；handoff=shared | state_preserved 必出 true/false/null；handoff.foreground_restore 必出 |
| claim/handoff uncertain | 已核验的 package/task_id/source_display_id 必出；目的落点未证明时 display_id/ownership 均省略；已证明落点与归属则可出，但原回执不完整仍 uncertain | state_preserved 必出，比较证据不足为 null；handoff.foreground_restore 必出，未请求=not_requested，请求但未能确认安全恢复=unconfirmed；非 handoff 省略 |
| claim/handoff not_executed | 只出现场核验到的源 package/task_id/source_display_id；不发结果 display_id/ownership | state_preserved 与 handoff.foreground_restore 均省略，不执行与本次未发生移动无关的恢复步骤 |
| 任意 kind 在模式/引用/容量等目标读取前拒绝 | app 只含 kind；已有本会话作用屏证明时可另带 display_id，不能借此表明移动落点 | 不补业务后验字段；本行优先于以上已进入目标计划的规则 |

find/list 的首次读取整体失败时只有 kind 和确知的 display_id/captured_at 等读取元数据，未取得集合则 items/total/truncated 省略；list inventory 读取失败时也省略 foreground，即使独立前台读取成功也不交付孤立前台对象，固定为整体读取失败形态。若只是 foreground 失败而 inventory 完整，则按 §4.6.1 返回已取得集合和 foreground.unknown，不降成全读失败。list 成功项 ownership 始终必出，不受移动 uncertain 的省略规则影响。

handoff 的前台恢复与已确认移动分开；恢复失败不撤销交出、不重新搬任务，不单独制造顶层主动作错误。原虚屏变空使用正常 observation.foreground.state=none，不主动结束会话。

#### 4.6.3 close

取得非空执行计划后 app 必填 kind、display_id、tasks、complete。tasks 是本次冻结目标的完整有界列表（最多 16 个）；每项 task_id、outcome，适用时 recents_cleaned，项级拒绝/未确认原因可带 error（同一受控结构）。零个符合条件的 live 目标在消费 O/派发前以 not_executed + task_not_found 拒绝；实时冻结集超过 16 或无法完整登记时以 app_capacity_exceeded 整体拒绝，不截断到前 16 个；两种拒绝均省略 tasks/complete，保留已知作用屏。

- 项 outcome=executed：该项主关闭已经确认；recents_cleaned 必出，true/false/null 分别为已清/确认仍在/未确认。
- 项 outcome=not_executed：该项明确未派发/未产生副作用；recents_cleaned 不适用则省略。
- 项 outcome=uncertain：主关闭未确认；recents_cleaned 只表达实际独立核验，适用但未知为 null。
- 全部项 executed → 顶层 executed；全部明确 not_executed → 顶层 not_executed；其余混合或任何 uncertain → 顶层 uncertain。不同项的确定性不因整体 uncertain 丢失。
- complete=true 当且仅当全部项 executed 且全部 recents_cleaned=true。主关闭全部完成但 recents 未确认时，顶层 executed、complete=false，不另造顶层主动作 error。剩余精确清理责任保留给原会话收尾，不阻塞后续健康普通工作。

```json
{"operation_id":"P9","outcome":"uncertain","app":{"kind":"close","display_id":12,"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},{"task_id":43,"outcome":"uncertain","recents_cleaned":null}],"complete":false},"error":{"code":"task_remove_unconfirmed","message":"部分任务的关闭结果未确认，请先核验现状"},"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}
```

整体前置拒绝时不返回伪造的 tasks:[]。工作在中途取消/到期时，尚未派发项记 not_executed，已派发无证据项记 uncertain，已确认项保留；冻结快照若不足以取得完整逐项事实，则省略未取得材料，不发出 complete=true。

#### 4.6.4 kill

executed 的 app 必填 kind、package、source_task_id、display_id、target_pids、killed_pids；remaining_pids 在后验清单可读时出现；restarted 必出，值为布尔或 null（适用但未取得完整后验）。三个 PID 数组都按数值排序。target_pids/killed_pids 指原目标实例的外显 PID；remaining_pids 指后验时该包全部可归因的在场 PID，可能包括新实例，restarted 表示是否出现了原集合外的新实例。主 outcome 只判原实例是否结束，不以 remaining_pids 是否为空代替。私有 boot/UID/start ticks 只用于核验，不回显。

| 主状态/阶段 | 身份和 PID 字段存在规则 |
| --- | --- |
| executed，原集合非空 | 已核验的包/task/作用屏与 target_pids/killed_pids 必出；remaining_pids 后验读取完整才出；restarted 必出，未取得完整后验为 null |
| executed，原集合已确认空 | 已核验包/task/作用屏必出；target_pids=[]、killed_pids=[]、remaining_pids=[]、restarted=false；这是无需发信号的显式完成分支 |
| uncertain | 身份与已经完整核验的 target_pids 必出；本次已进入发信号/回执阶段，killed_pids 必出（只含已证实结束的原实例，无则 []）；后验未读全则 remaining_pids 省略、restarted=null |
| not_executed，任意前置拒绝或派发被明确拒绝且无结束 | 只返现场核验到的包/task/作用屏；target_pids 仅在完整读取后出；killed_pids 省略，表示未进入主执行结果阶段；只有实际完成后验才出 remaining_pids/restarted，否则两者省略 |
| 取消/交付丢失但适配器未取得服务结果 | 只带 app.kind 调用元数据；不能从请求 task_id 或空集合伪造设备结果；原 P 后续由 status 查询摘要 |

not_executed 的“未发信号”不使用 killed_pids=[] 冒充完成核验；只有 executed/uncertain 执行阶段的空数组才表达该阶段确认数为零。

```json
{"kind":"kill","package":"com.example","source_task_id":42,"display_id":12,"target_pids":[1234,1235],"killed_pids":[1234,1235],"remaining_pids":[],"restarted":false}
```

所有原目标进程终结已确认且派发回执完整 → executed；原目标仍存活或无法完成原实例核验 → uncertain，保留已确认 killed_pids。restarted=true 只表示后来出现新实例，不能改判原目标的终止。进程后验读取失败时省略 remaining_pids，restarted=null，绝不能填空数组/false。kill 不返回 complete/recents_cleaned，不承诺 task 已消失。

### 4.7 错误、恢复与摘要

不新增 recovery 入口枚举；为 app 按 kind、阶段、原引用、是否消费 O、是否派发、是否已返回来规划，不能把名字 app 当成旧 open_app 简单替换。其他工具不在本提案中整体重设计。

| 情况 | outcome/旧 O | recovery 与后续 |
| --- | --- | --- |
| JSON/认证/契约接入拒绝 | 服务接入前无已登记 P、无主 outcome；MCP 严格参数拒绝不分配 P，转发后拒绝可保留预分配 P（不证明已接纳） | 保留既有接入惯例，contract_mismatch→update；其他无 recovery |
| find/list 的 request ctx 取消 | 查询无 outcome、不 end；engine phase=query_cancelled、无业务 error；MCP 用 call_cancelled/mcp.*（已有 error 则保留） | recovery 省略，不把 finished 原查询 P 当恢复推进；session/O/租约不变 |
| 模式不支持 | not_executed，原 O 未消费 | user/none；仍有效时 input_state=effective。用户仅在确需该能力时在 App 切模式；不原样循环 |
| 目标歧义，可通过修改参数纠正 | not_executed，原 O 未消费 | retry/O/effective，details.task_ids 给当前候选；新 P、精确目标 |
| task 未找到、不再在本屏、归属/保护拒绝 | not_executed（前置阶段） | 不通用附 retry；在受控 message/details 说明原因；没有强制恢复步骤则省略 recovery。需要人解除的全局条件用 user |
| O 过期/被替换/消费 | not_executed | observe/P，input_state 沿用对应状态 |
| 已消费 O，派发前重验失败 | not_executed，旧 O 不归还 | observe/P/retired；若同工作实际交付了新 ready O，按既有交付规则 retry/O/effective |
| busy | 本请求未消费 O、未派发；查询无 outcome | status：同 Agent 有 blocking_operation_id 则查该 P；没有可公开阻塞 P 则查 status()，不能循环查本次已 finished 的拒绝 P |
| app 直接返回 finished uncertain | uncertain，通常旧 O 已 retired | observe/P；新 O 后按需 app.list 查看 session/primary。不能把静态原 P 记录当成设备核验 |
| HTTP/MCP 交付失败，原调用是否结束尚未知 | 不伪造未执行 | status(原 P)；running 才继续有界查询。finished 后按原 recovery 或新查询核验 |
| 主动作 executed、后树失败 | executed，业务事实保留 | observation.failed；observe/P/retired/cause=post_observation_failed；不重做主动作 |
| STOP/end/到期 | 保留已知 executed/not_executed/uncertain | session/cleanup 按原归属返回；end/status 恢复按现行终态规则，不发布迟到新 O |

find/list 失败不带 outcome；成功取得集合才带 items。临时读取失败不统一填 retry/O，也不把 finished 的查询 P 指回自身 status 以假装有推进。明确全局就绪问题可指向 status()/user；无必须执行的通用恢复步骤时省略 recovery，message 说明“读取失败，条件恢复后以新 P 重新查询”。

错误码按既有职责复用：app_not_found、app_launch_failed、app_launch_unconfirmed、application_moved、task_focus_unavailable、task_remove_unconfirmed、device_busy、device_unavailable、root_unavailable、observation_*、session_*。新增 app 业务码：window_action_not_supported_on_primary_display、task_not_found、task_not_tracked、task_target_ambiguous、task_root_shared、task_move_unconfirmed、close_refused_borrowed、kill_refused_not_owned、kill_refused_task_elsewhere、kill_refused_foreground、kill_refused_playback、kill_refused_process_shared、kill_unconfirmed、app_capacity_exceeded、task_lifecycle_busy。新业务冲突为 HTTP 409；格式错误为 422；读取/设备能力不可取得为 503；已有 app_launch_unconfirmed 保留 504。新增码必须显式进入 Status 映射，不能依赖默认 500→409。

受控 details 只新增必要的 task_ids、display_ids、allowed_kinds、limit；不给原始 dump、进程 ARGS、组件正文或异常。code 是稳定判据，message 是中文说明，不以解析 message 代替结构化处理。

P 的两种用途必须写清：`observe({reference:"P9"})` 引用本会话既有 P9，新的 observe 请求使用新的幂等 P；reference=P 是类型说明，不是让调用方把新请求 P 填入 body。修正现行接口文档中相应含糊表述，不改变现行 Observe 的归属解析。

status(P)/重复 P 只返回操作摘要，不重新派发、重新采集或补回业务列表/原树。app 操作记录新增最小 kind 标记：重复 P 的 app 调用摘要必带 app:{kind}；status(P) 可带该元数据；不能伪造 items:[]。查询摘要通过 operation_state 和 phase 明确区分于首次查询数据；query_cancelled 只表示查询被关闭，需要集合的调用方以新 P 重做 find/list。无 error 的查询摘要 IsError=false 只表示摘要已提供，不表示原查询成功或含列表；status 自身仍按查询事实判定。变更摘要仍使用原 outcome/error/recovery；新观察不能改写原 P 的主 outcome。

recoveryInput/operationState 记录 app-kind，app 的专用 planner 必须先于通用 statusCodes/outcome 分支运行。find/list 的完成、错误、deadline、ctx 取消、会话终态冻结都走无 outcome 路径，不使用默认补 not_executed 的 finishWithError。取消按 §5.1 返回原事实；未知交付可用 status 原 P 核验 running，但 query_cancelled/finished 查询不附指回自身 status 的 recovery。

### 4.8 MCP 三模式与一份事实源

硬契约继续为 `AgentResult=(canonical A, CallToolResult.IsError, ImageContent)`。A 是去除图片 base64 的确定性 Result；取消保留 `error.details.mcp.*`。模式不改业务、错误、取消、P 分配、续租或设备调用次数。

| 模式 | content | structuredContent | 完整事实来源 |
| --- | --- | --- | --- |
| text | 完整 result v1；适用时相同图片 | 不发送 | result v1 无损解码的 A + is_error 头 + 图片 |
| structured | 图片；IsError=true 时最小 diagnostic v1 | 完整 A | structuredContent + IsError + 图片；diagnostic 不是完整 A |
| both（默认） | 与 text 相同的完整编码和图片 | 与 structured 相同的 A | 两套编码均还原同一 AgentResult，图片不重复生成 |

app 本轮不请求图片；其五个变更 kind 的观察均为节点 full。其他工具的 ImageContent 载体保持原样，不能因合并 app 改成 JSON base64。

文本优先沿用现有 key=value 与完整 JSON 字段，**本轮不另发明 app 窗口块语法、不更改 result v1/tree v1 版本**。字段分类如下：

| app 路径 | 文本编码 |
| --- | --- |
| kind、ownership、foreground_restore | fieldToken，受控枚举 |
| package | fieldString，始终 JSON 字符串 |
| task_id、source_task_id、display_id、source_display_id、total | fieldLiteral，数字类型准确；origin_display 只在 items JSON 内，不新增 app.origin_display |
| reused、state_preserved、complete、truncated、restarted | fieldLiteral，false/null 必须保留 |
| captured_at | fieldToken，时间戳按现行规则 |
| items、tasks、foreground、target_pids、killed_pids、remaining_pids | fieldJSON，紧凑完整 JSON，保留嵌套错误、数组顺序和省略/null |

未知/不可表示字段继续整值 `json v1 path=... value=...` 兜底，不能只发计数或丢字段。复合字段使用 JSON 的目的是完整而确定，不把 task 列表变成不能还原的自由文字。

以下为 §4.6.3 那份关闭响应的完整 text 编码（无 observation 的同一 A），作为一组映射 fixture：

```text
result v1 is_error=true
operation_id=P9
outcome=uncertain
app.kind=close
app.display_id=12
app.tasks=[{"task_id":42,"outcome":"executed","recents_cleaned":true},{"task_id":43,"outcome":"uncertain","recents_cleaned":null}]
app.complete=false
error.code=task_remove_unconfirmed
error.message="部分任务的关闭结果未确认，请先核验现状"
recovery.entry=observe
recovery.reference=P
recovery.input_state=retired
end result
```

structured 模式发送同一 A 的 structuredContent，IsError=true，并可附包含 operation_id/outcome/app.kind/error/recovery 的 diagnostic v1；它不需要在 diagnostic 再重复 app.tasks。both 必须同时满足以上两种编码。正常无图 structured 成功 content=[]，不能偷偷补一份完整文本以改变模式。

IsError 判据：find/list 首次完整查询或已明确标记的无 error 操作摘要为 false（包括 query_cancelled 的状态摘要，不代表原读取成功），读取/接入/传输错误为 true；五个变更 kind 以主 outcome 判定，executed 为 false，not_executed/uncertain 为 true。close complete=false 但仅 recents 未确认、handoff 仅前台恢复未确认、或主动作后树失败均不改写主 outcome/IsError；取消调用本身永远 IsError=true，同时保留原已知主动作事实；同一 P 随后的 status(P) 沿用现行 result.Error 判据，query_cancelled 摘要无 error 时为 false，不因为原调用被取消而强制 true。此处不改其他工具的 IsError。structured 模式是否附 diagnostic 由本次调用 IsError 决定，不是看任意嵌套 observation.error。

diagnostic 白名单只增加 app.kind（如果 A 中存在），不直接放开 app 全对象，不增加任何只在诊断出现的事实。全量文本的 app.*、项级 error/recovery、null/[] 必须能被测试解码器与主机 probe 从 text 单侧恢复；不能依赖 both 的 structuredContent 掩盖漏字段。查询取消的三模式 mcp.cancellation 必须同源且写“查询已取消；未发起 end”，不能声称发出了有界 end；变更取消只记录实际发起的 end。

## 5. 算法

### 5.1 查询与变更接纳

```text
严格解码 kind/字段
  find: 登记 detached P → 有界缓存/单普通工作读取 → app.find（无 outcome）
  list: 解析原会话 O/P → 登记绑定 P → 取得普通工作 → 确认 scope 对应显示
        → 只读 task inventory/前台 → 结果提交前复核原会话/代次 → app.list
  变更: 登记绑定 P → 静态 O/模式/参数检查 → 取得普通工作
        → 只读目标与保护证据 → 预留完整结果/归属记录容量
        → 原子 claim 当前 O + 阅读确认 → 身份/显示/目标最终重验
        → 保存原归属/迁移意图/派发责任 → 复核 STOP/期限 → 派发一次
        → 有界回执与只读后验 → 记录已知主事实
        → 原会话屏 full 后观察 → 条件发布/完成 P → 释放普通工作
```

预检失败不消费 O；claim 后不归还。首个副作用前确认 O 仍在输入期限，后续同一计划子步骤继续受原 work、会话、显示与工作期限约束，不给每项重新获得一个 O。设备调用、存储和树计算均不持状态锁。

find/list 选择**查询 request ctx 可取消工作**的方案，不选择“客户端已取消但仍占满普通工作期限”的方案：

1. 新查询取得 work 前后检查调用方 ctx；查询的 work ctx 同时受 request ctx、现行 work deadline 和会话 STOP 取消。list 的 P 保持绑定原会话，find 保持 detached；取消不改变 session/O、阅读基线、绑定与租约。
2. request ctx 取消只关闭本 query 的发布资格、取消读取。正常适配器尽快返回；无响应读取按现有 retirementHandover=5s 上界冻结并从普通位置脱离，不撤销会话、不拆屏、不等待设备物理读命令终结。迟到只读候选不得发布。下一正常工作不得因这次已取消查询持续到整个 WorkTimeout 才能接纳。
3. query 的 finish/freeze 分支写 operation_state=finished、phase=query_cancelled、app:{kind}、无 outcome、无 error、无集合、无 recovery；取消只证明查询已关闭，不伪装成功读取。status(P) 正常返回该关闭事实，IsError=false。查询工作超时则 phase=query_timeout、error=device_unavailable、仍无 outcome。若会话同时 STOP/end，返回已知原会话终态，但查询仍无 outcome。
4. `freezeWorkLocked`/退休冻结与 query finish 必须区分 app 查询；禁止经过默认补 not_executed 的 finishWithError。取消回调核验原 P/work token，只影响本查询；不能误取消下一工作、不能借调用 end(P) 实现 query 取消。
5. MCP preparedCall 分别记录“分配 P”“取消策略/end”“取消文案与 recovery”“IsError 判据”四项 kind 属性。现 definition.readOnly 只兼管前两项，现 IsError 另按工具名判定；两处都要拆分。app 整工具元数据非只读，七 kind 均预分配 P，不通过设置 readOnly=true 取消 P 分配。
6. 查询取消未取得服务结果时，适配器返回已预分配 operation_id、app:{kind}、error.code=call_cancelled，注入 mcp.cancelled=true 与受控文案“查询已取消；未发起 end”，无 outcome/集合/recovery，不假定服务已经 finished。服务结果已知时保留已知业务字段与原 error；查询取消的 MCP 恢复策略覆盖为省略，不复制原 recovery。两种情形均注入 mcp.*、IsError=true；无论服务结果是否已知，只要没有原 error 就生成 call_cancelled，已有 error 则保留其 code/message/details 后注入取消元数据，不产生第二个 error。此规则沿用当前 canonicalResult 的取消合成方式。engine 的 query_cancelled 记录不生成新的取消业务错误码。
7. 五个变更取消按原 P 执行现有有界 end，并保留真实的 end 文案/恢复入口；未发起 end 的查询不能复制该文案。HTTP 接入/SDK/MCP 各自仅传递 ctx 和事实，不另持执行权威。

### 5.2 任务搬移与责任转移

增加有界 `MoveTask` 原语，输入 leaf/root/source/target 的已核验身份，使用 `am display move-stack <root> <target>`，返回 DispatchReceipt 和受控执行事实。engine 决定候选、权限、结果与后验；adapter 不维护第二个 registry。共享 root 拒绝，只有当前手机可核验的单 leaf root 才移动。

每次搬移在派发前持久化最小 TaskTransfer 责任：原 P/epoch、boot、App instance、源显示/generation、leaf/root、包名、目标显示、方向 in/out 与 prepared/may_dispatch/terminated/not_dispatched/unknown 状态；不持久化原请求、树或自由文本。

```text
t0  只读核验源 live task/root/目标与容量，尚未消费 O
 t1 claim O → 写 transfer prepared 与原 UI may_dispatch 责任 → 落盘
 t2 再与 STOP/期限竞争，允许才派发 move
 t3 记录本次回执；只读确认同一 leaf 的实际落点
 t4 claim: 在目标虚屏确认后登记 borrowed
    handoff: 从删除责任移出，保留出站排除证据，落盘后发布交出事实
 t5 原虚屏 full 观察；不观察/追随主屏树
```

出站 prepared/may_dispatch/unknown 及已确认交出的任务对所有 cleanup 候选均有显式排除：session.Tasks、冻结快照、checkedTaskIDs、**本屏 live 扫描自动采纳**、recent-only、恢复重试与迟到复核。每次删除前都按原 owner 查询同一个全局迁移保护池，不能只靠 live 位置或旧快照判断。确认未派发后才可解除出站排除；原任务是否仍应收尾由原 owner/当前实时位置决定。入站未知只记录待核验责任，不能因为原主屏 task 的 ID 被写入意图就允许删除主屏或 recent-only 用户条目。原/目标两侧仅对会话虚屏记录可归属 generation；主屏以本 boot/display 0 标识，不伪造会话虚屏代次。

再次认领同一 boot/task 前，检查其他原 owner 是否仍有该精确 task 的删除资格；先封闭旧的未派发删除引用。若仍有已派发、结束未确认的旧 RemoveTask，不移动该 task，返回 task_lifecycle_busy；只限制该目标，不阻塞其他 task/新会话。不能用一次 live 读回追认旧删除命令已经终结。

STOP/end 在任意 t 先撤权，不等 move。已派发命令不保证撤回；迟到回执只能更新原退休责任，不发布新 O、不恢复原控制权。出站未知不能触发自动反向搬移、重发原 move 或按 ID 删除；源虚屏仍可按原 end/STOP 规则关闭，仍在源屏的窗口可能被销毁，不承诺 STOP 下保留现场。入站没有目标屏落点证明时不得主动清理主屏任务。

handoff 完成后移除 session 删除项；保护池是 v5 根对象的 task_transfers，当前/退休记录只引用原身份，不嵌入复制池。容量管理按以下顺序：先停止选中旧 owner 的 cleanup/retry 资格，封闭其 current/retired/frozen/checked/live 采纳/恢复重试/迟到回调可用的删除引用；再压缩/驱逐该退休资源记录；最后仅在没有任何仍有删除资格的引用时回收对应保护项。仍存在引用的 outbound prepared/may_dispatch/unknown 不得为腾空间被丢弃。

旧 owner 已永远失去派发/删除资格、且没有任何可恢复引用时，可沿用现有有界退休策略将未知物理事实汇总为 dropped_unconfirmed/cleanup unconfirmed，再回收不再保护任何删除链的记录；这不把未知变 clean，也不声称已派发物理命令被撤回。压缩后不能还保留 checked IDs 或重试通道。显示销毁、数字 ID 复用、跨 boot、新会话均不能让旧记录重新授予删除权。容量预留失败在 O claim/dispatch 前拒绝新迁移，不让历史记录容量永久挡住 STOP/end 或其他新会话准入。

### 5.3 open、close、kill 与失败收口

- open 本屏复用先查 live，不被无关 Launcher 解析阻断；只有实际创建才解析入口。复用使用精确 focus，不重发 Intent。主屏路径保持不登记用户窗口。
- close 在整体权限通过后按 ID 顺序处理；每个 remove 有本次 DispatchReceipt。每步前重验原 boot/显示/归属/迁移排除；不按包直接发清理。发生错误/取消/预算耗尽停止新派发，记录已完成与未派发项；读回 live 和 recents 是独立事实。
- kill 先确定原实例集合，再以 §4.5 的最终守卫派发一次信号。原 PID 新实例不是旧进程仍活着；部分结束不抹掉已知 PID 事实。无完整回执或原实例终结证据则 uncertain。
- handoff 默认接受移动造成的主屏前台变化；restore_foreground=true 时在移动收敛后按旧项目约 2 秒延迟进行一次尽力恢复，用户已经切到无关前台则不覆盖；恢复步骤也受 STOP 与工作期限约束。
- 任何内部失败不自动 end 健康会话；app 内部未确认本身不成为永久准入锁。未知窗口在调用返回时关闭并保存事实；物理未知不能被后观察改成已执行。
- 后观察失败保留 executed，使用现有 observation.failed 与 recovery；撤权后只回原会话终态，不发布迟到 full。

### 5.4 私有账本与恢复

账本 v5 扩展 LedgerTask 的 ownership/origin_display/root 与原身份，并增加**根级唯一** TaskTransfer/出站排除池及克隆、校验；Retired.Record 禁止嵌入 task_transfers。store 支持加载历史 v1–v4，并继续处理既有 legacy UI 屏障；旧任务仅按旧格式的原拥有关系升级为 owned，绝不根据包名推断 borrowed/交出。

容量按实际序列化计，不假设八份满记录都能放入 24 KiB：base（root 身份、当前 session/tasks/cleanup/stop/shell/UI/pending，排除 retired 与 task_transfers 两个数组）≤20 KiB；退休数组≤24 KiB且≤8项；全局迁移数组≤16 KiB且≤16项，每项编码≤1000 B；两个数组属性及拼接额外预算≤512 B。总上界 20,480+24,576+16,384+512=61,952 B，低于 65,536 B，余 3,584 B。仍以完整编码≤64 KiB作最终门禁，独立预算不能代替总量检查。

对退休数组采用确定性压缩：单份完整退休候选（含 wrapper）≤21 KiB；超过则先封闭不再继续 cleanup 的旧引用并生成≤1 KiB的精确 owner/未知计数摘要，不能截断仍有权删除的目标。最多8是条数上限；若“新候选+旧摘要”仍超过24 KiB，按现行年龄顺序封闭/驱逐最旧 owner，直到实际数组满足字节上限，不保证保留8份完整记录。所有8份原先仍可 cleanup 时也允许按此顺序撤掉旧 cleanup 资格并如实记 unconfirmed；不能等它们变 clean 才保存新归属。保护池回收严格晚于旧引用封闭。

ordinary 准入预留不仅算本次 transfer：还模拟新增任务/UI/待派发责任后的 base，以及“当前归属立即转退休”的 wrapper/压缩结果，给派发回执/终态补写预留最坏空间。open/claim/handoff 增长都要预检；其他新增归属也复用该有界预留，不能把容量错误拖到 STOP/end。新会话和 STOP/end 使用有界退休压缩，不因保留全部旧明细而失败；真实 I/O/账本损坏仍如实触发既有门禁。

v5 专属字段不得出现在标为 v1–v4 的账本中。加载 v5 后禁止丢字段降存 v4；**首次 0.9.0 一旦写入 v5，不支持回退到 0.8.0，采用同为 v5 的候选或前滚修复**。本提案不假定存在尚未产出的兼容回退包。不得删除账本、清 App 数据或忽略迁移字段以回退。

新进程在固定内核锁之后才读账本/App/设备。恢复不重放 move/kill，不恢复旧会话执行权；按原归属核验或保留未知，有限交接与当前可写账本成立后允许新工作。保存派发前失败表示未派发；设备已确认执行但后保存失败必须保留 executed 与现有账本故障门禁，不能改判为未执行。预先保存的迁移意图继续保护交出的任务。

## 6. 实现方案

### 6.1 接口与文件

以下为文件族与拟新增落点，实施前按当时源码核对命名，不以文件拆分替代唯一权威。

| 文件/模块 | 改动 |
| --- | --- |
| internal/protocol/requests.go、surface.go、encode.go、decode_response.go、responses.go、errors.go | AppRequest 分支、AppView 判别对象、逐项 close、nullable/presence、错误映射；移除公开 Apps/OpenApp 同义面 |
| internal/device/types.go | root/leaf、top/base 包事实、进程实例与 mover/remover 受控回执；不持运行状态 |
| internal/android/dumpsys.go、launch.go；拟新增 task/process 适配代码 | 完整 inventory 识别，root 单 leaf，move/remove receipt，FGS/媒体/进程证据、精确 kill 与有界回读 |
| internal/engine；拟新增 app.go、app_tasks.go、app_transfer.go、app_kill.go | 统一业务接纳，复用 open，live 对账与迁移责任，分支工作/结果/恢复；禁止各 handler 自行访问设备 |
| internal/engine/cleanup.go、ledger.go、control.go、retirement.go、ui_execution.go | 全局迁移池与所有删除入口排除、原责任/迟到证据、v5 恢复、退休压缩与 STOP 竞争 |
| internal/engine/operations.go、recovery.go、status.go、end.go、engine.go | app-kind 元数据/planner 优先级、查询 request ctx 与无 outcome finish/freeze、query_cancelled 摘要/取消、原 end 绑定语义 |
| internal/store/ledger.go 及校验测试 | v5 读写、版本字段检查、历史迁移、有界证据与拒绝降级 |
| internal/httpapi/server.go、delivery.go | /api/app，移除 /api/apps、/api/open_app；8 KiB严格入口，解码kind后按query/tree预留交付，传递request ctx与受控状态映射 |
| internal/client/client.go | 强类型 App 方法与分支编码；原 P 与新调用幂等键区分；无旧别名 |
| internal/mcp/schema.go、server.go、result_value.go、result_text.go、render.go、相关 manifest/parity 测试 | 11 工具，按 kind 的分配/取消/IsError，AppView 编码与全部文本字段，三模式同源 |
| android-control/.../ControlState.java 等 | 新 app work/kind 的受控中文状态；版本生成；不把业务权威下沉到 App |
| android-control/test-target | 按需补受控页面栈/中文文本、第二任务、:remote 与可控 MediaSession/FGS 测试能力；不改用户应用 |
| cmd/device-mcp/main.go、internal/mcp/catalog.go | 启动 tools 数由 ToolCount 派生；提供纯目录快照供 probe 逐工具对比，不读取配置或访问设备，不增加 MCP 工具 |
| scripts/mcp-probe | 11 工具/seven-kind schema、app 新结果解码、text oracle、三模式 app 场景与已部署 HTTP 入口连接 |
| scripts/verify-p4.ps1 | Core/OpenApp 场景迁移到 /api/app 与新 app.kind 结果；保留原受控回归目的，不留继续调用已删 /api/apps、/api/open_app 的脚本 |
| scripts/verify-m5.ps1 | 复用 host-tools 构建/核验，SkipBuild 不只查存在；复用有效私有 token，不在验证时静默轮换并破坏实际宿主配置 |
| scripts/verify-m6.ps1 | 新旧契约对照显式覆盖上一发行contract8；手机发现/进程检查不冒充实际tools/list与生命周期通过 |
| 拟新增 scripts/build-host-tools.ps1 | 同源码构建并校验 Windows device-mcp.exe、mcp-probe.exe 与独立主机清单；不误用 Android build-probe.ps1 |
| release/versions.json、生成文件、build-release/deploy/verify-manifest | 共同版本与目录指纹、v5 回退说明、整组手机与主机产物核验 |
| AGENTS.md、README 与相关 docs | 工具数/目录、当前契约/架构/SDK/MCP/运维/版本/部署/回退更新；工程约束本身若改变须按流程另记决定 |

### 6.2 配置与取值

| 项目 | 拟取值与依据 |
| --- | --- |
| 工具目录 | start/status/observe/act/inspect/keepalive/end/app/notify/shell/diagnose，共 11 |
| app 请求上限 | 8 KiB，沿用原应用端点；不开放 timeout/TTL/quality/image |
| find/list 返回预算 | 各 32 KiB 完整项前缀；total/truncated 明确。inventory 可读性不是字节截断 |
| 自有任务登记 | 沿用当前 16 个；新增目标前预留容量，不静默漏登记 |
| 迁移/排除记录 | 根级全局池≤16项，每项≤1000 B、数组≤16 KiB，不嵌入退休副本；base≤20 KiB，退休≤8项/24 KiB，容器额外≤512 B，完整账本≤64 KiB。退休单份完整候选≤21 KiB/摘要≤1 KiB；压缩、删除资格与预留顺序见 §5.4 |
| 普通工作期限 | find/list 均沿用现 WorkApps 的 `WorkTimeout+10s`，默认30s；五个变更采用现 WorkOpenApp 的 `2*WorkTimeout+10s`，默认50s。子步骤受剩余总预算约束，不每项重置；查询取消复用5s逻辑交接上界 |
| HTTP delivery 预留 | 严格解码 kind 后，find/list 用 deliveryReserveApps（现64 KiB），五个变更用 deliveryReserveTree（现256 KiB）；先预留再调用 engine，不能统一按列表小预算交付 full 树 |
| 后观察 | 五个变更统一 full、无图，复用 open_app 的收敛策略；不改变独立 observe 与 shell 语义 |
| 移动/focus 回读 | 复用当前本机有界短回读的级别（0/200/400/800/1200ms）；只读，不重派发 |
| 前台恢复 | false 默认；true 延用旧项目约 2s 的尽力恢复，不能超出工作期限 |
| allow_borrowed | close 默认 false；true 才允许销毁借入窗口，不自动归还 |
| allow_stop_playback | kill 默认 false；true 只越过已知 FGS/PLAYING 拒绝，不越过未知/归属/跨屏限制 |
| 升级版本 | 0.9.0 / contract 9 / Android versionCode 11，实施冻结时重验没有其他发布占用 |
| 版本 source_commit | 实施时的版本冻结基线；release manifest 记录构建时实际 HEAD。不得误把版本文件里的基线当二进制提交 |

以上由实现负责人按本文和配置常量校验；除表列的明确布尔选项外，容量、超时、模式和目标屏不暴露为新请求旋钮。改变默认值、执行语义或预算/回收顺序须修订提案/工程决策；现场运行者不能通过临时配置绕过权限或放宽身份检查。

保留现行没有会话累计时长硬上限、显式 keepalive 与普通滑动续租规则。五个变更按 O 接纳/派发的既有续租点处理；find/list 不续租。

### 6.3 日志与可观测性

复用 operation/work/UI/cleanup 事件与原 P/epoch/display generation。新增受控 app_kind、transfer_direction、transfer_state、ownership、task_id/root_task_id、source/target display、verified/remaining 计数、result_mode 等必要枚举/ID/数值字段。事件覆盖 app 预检拒绝、move/remove/kill 派发及结束、责任交出、未知保留、后观察、容量拒绝与旧责任排除。

日志不得记应用名、包名/组件正文、PID 的 ARGS、MediaSession token、树/图片、命令、异常原文、消息或凭据。包名只在必要公开响应与私有归属账本按既有边界出现；日志记录批准的 ID/计数。MCP stdout 只承载协议。诊断展示原 kind/阶段/结果与受控事实，不提供原始设备 dump。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| engine 唯一权威 | 统一入口不让 MCP/HTTP/App 维护第二份任务表或 ADB 通道 |
| STOP 不等待 | 立即撤权与拆屏路径不等待迁移/kill；已发命令不保证撤回 |
| O 单向消费 | 五个变更使用同一规则，查询不消费；未派发失败也不归还已 claim O |
| 单工作、无队列 | 查询新采集与变更共用位置；冲突即时 busy，批量 close 是同一有界计划 |
| 条件发布 | 原 epoch/display/work/STOP/期限重验；不发布迟到主屏/虚屏树 |
| 事实分层 | app 窗口/进程后验不改主 outcome；主动 close 不伪装成 session cleanup |
| 有限退休交接 | 仍最多 5 秒逻辑让位；迁移未知按原记录保留，不永久封锁新会话 |
| 私有有界账本 | v5 最小归属，不存完整响应/请求；容量满在副作用前拒绝 |
| 三模式等价 | 新字段逐项进入 A 与文本分类/decoder/probe；取消也同源 |
| end 语义 | 保持当前虚屏统一收尾，不加 keep/window_action/默认交还；主屏不关用户任务 |

### 6.5 契约与文档影响

进入实现前，按 AGENTS 的设计源头约束先在同级 mobile-use-platform 的 `docs/architecture/` 做 docs-only 修订：至少 vnext-interface-spec、new-project-architecture、concurrency-and-observation-commit、cache-retention-budgets-and-restart，以及涉及工具数/收尾/恢复的直接相关分案。区分旧项目 task-lifecycle 的现行旧能力和 vNext 新契约，不把旧 MCP release 默认 handoff 复制进 vNext。

vNext 实现具备后同步 docs/interface.md（路由、字段、错误、P 引用）、mcp.md（三模式、目录、按kind取消/文案/recovery；现第6节的“取消会发end”改为分支事实）、client.md、architecture.md、control-protocol.md（work 枚举）、release.md、runbook.md、phone-operations.md（v5、首次发行不可退0.8.0、部署）、integration.md 与 README/AGENTS 中的工具目录。工具名不保留兼容 alias；旧 contract 8 客户端必须整组升级。

本提案阶段只新增 maintenance 提案；不提前把拟定能力写进正式 docs。实施涉及 docs 的提交前必须独立只读文档审计 clean，修正后复检；审计期间不修改被审 docs。

## 7. 预期效果与能力边界

### 7.1 能治什么

调用方用一个工具完成应用发现、live task 查询、明确本屏打开、精确认领/交出、关窗与受限进程终止；同包歧义不靠猜。错误给出执行事实和可用恢复入口，text 宿主能取得与 structured 宿主相同的任务/PID/部分结果，不再依赖结构化旁路。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| claim 后任意页面不完全相同 | 显示配置、Activity 重建、网络及应用自身状态不是 task ID 相同的保证 |
| STOP/end 后借入页面丢失 | end 保持清理，STOP 可立即拆屏；要保留须在正常工作中显式 handoff |
| 主屏用户窗口 close/kill | 本次明确禁止；不能把主屏共享当任务所有权 |
| 本屏有 task 就能安全按屏杀进程 | 进程不按屏隔离；本提案拒绝跨屏/共享等已知冲突，仍不冻结 Android 外部并发 |
| kill 后没有后台进程/最近任务卡片 | kill 不 force-stop、不追杀重启、不代替 task/recents 清理 |
| close 最后窗口后再 kill 残留进程 | 已无合格本屏 live owned task，必须拒绝 |
| recent-only 恢复原页面栈 | 最近任务索引不是 live 页面现场 |
| status 把 uncertain 变为 executed | status 不访问设备；新读只报告现状，不追认旧副作用 |
| 替换 exe 后宿主立刻有新工具 | 实际进程与连接需重启和重新发现，磁盘新文件不是运行证据 |
| Windows 测试证明手机账本耐久/所有真实应用状态保留 | Windows 只作开发检查；实机结论限本次受控路径 |

### 7.3 成本

统一工具减少目录入口，但七个分支增加 schema、结果类型、错误与三模式 fixture。list/claim/kill 增加真实 task/进程/媒体读取；迁移需派发前/完成后的归属保存；close 的多个任务共用总预算，可能在期限内部分完成。restore_foreground=true 额外消耗约 2 秒并可能闪屏。没有新的每请求超时/质量旋钮，也不宣称固定延迟、token 或成功率提升。

## 8. 验证计划

### 8.1 单元测试

| 路径/用例 | 期望与反向对照 |
| --- | --- |
| 七 kind schema/strict JSON | 有效样本双侧接受；未知/重复/null/跨分支字段/错误 target 均拒绝，不调用设备 |
| 模式 7×2 | find/list/open 两模式可用，其他 primary 在 claim O 前拒绝；移除模式守卫应使测试失败 |
| P/取消 | find detached；list 绑定原会话；取消 list 后≤5s释放普通位、session仍active/旧O可用/下次act可接纳、status(P)为query_cancelled且无outcome/error；MCP保留P/app.kind；未知结果与known+无原error均生成call_cancelled，message与mcp.cancellation为“查询已取消；未发起end”，mcp.cancelled=true；known+原error保留原error且不复制原recovery；known业务集合保留，未知集合不伪造；不发end。变更保留真实有界end文案。去掉ctx继承、per-kind取消或无outcome freeze必须失败 |
| O 与 busy | 静态/只读拒绝不消费；claim 后不归还；同 O 并发只有一个成功；busy 不排队 |
| query 空/失败/前台未知 | []、缺失集合+error、foreground.unknown三种事实不混淆；inventory失败而独立foreground成功仍省略foreground，反向情形返完整集合+unknown；查询始终无outcome |
| open | 本屏 live 复用不发 Launcher intent；仅新建解析入口；主屏不登记；多候选确定选择 |
| claim/handoff | exact/unique/ambiguous、root/leaf 不同、共享 root、控制 App、无 Launcher、原/目标位置变化 |
| transfer 与 cleanup | pending/outbound/confirmed 出站从全部删除候选（含live自动采纳）排除；handoff unknown+STOP/end且本屏仍live不得按ID删；主屏消失只留recents也不得删；旧in-flight RemoveTask挡住同task再次claim；退休压缩先撤旧资格再回收保护；移除守卫必须出现错误RemoveTask调用 |
| STOP/迟到/重启 | 每个持久化/派发/落点/完成边界注入 STOP/崩溃；不迟到发 O、不重放 move、不清主屏；旧未知最多 5 秒逻辑让位 |
| close | 精确单项、包级冻结、零目标task_not_found、>16整体app_capacity_exceeded且不截断；borrowed默认整体拒绝/true关闭、执行中移走、部分完成/未派发剩余、recents true/false/null、最后窗口空屏 |
| kill | live owned 必需、borrowed/他屏/同屏 borrowed/前台/FGS/播放/未知/root 拒绝、:remote、多实例 PID、start ticks 变化、原集合空、部分结束、新进程重启；禁用最终身份守卫必须失败 |
| outcome/后观察 | 原回执 unknown 不因后树成功改判；executed 后树失败仍 executed；已确认交出不因前台恢复失败反向重搬 |
| v5 账本 | v1–v4迁移、伪版本证据/嵌套transfer池拒绝、深克隆；16项/1000B/16KiB和base20/退休24/总64边界；模拟8份最坏退休候选并确定压缩后保存成功、仍有权引用的保护不丢、STOP/end不因容量置ledgerUnsafe；0.8.0读v5受控拒绝且字节不改写；无请求/树/原文泄漏 |
| status/重复 P | 仅摘要、不执行/不伪造列表；app.kind 可见；旧 outcome 不被后读修改；新查询必须新 P |
| recovery | 按 kind/阶段生成；mode 不给循环 retry；finished uncertain 不永久 status；原 P/body reference 与新幂等 P 区分 |
| 版本/目录 | 11 工具、七 kind、无 apps/open_app；契约 9 精确配对、旧 8 拒绝；版本生成 -Check |

MCP 矩阵必须覆盖 **7 kind × 3 result mode** 的正常响应和每个 kind 的适用错误分支，不用 device fixture 制造不适用的组合：

| fixture 类 | 必查事实 |
| --- | --- |
| find/list 正常、空、截断、读取失败 | 列表项正文/顺序、name/package、[]、total/truncated/time；失败无伪 outcome/集合 |
| open/claim/handoff 正常及 state_preserved=null/false | 身份、落点/来源、ownership/reused、null 类型与 source/target 不混 |
| close 完整、recents 独立未确认、部分执行 | 全部逐项 outcome/error、recents null/false、complete=false，不能只显示计数 |
| kill 完整、原集合空、部分/后验不可读、重启 | 全 PID 数组和省略、restarted false/null/true，不把空数组当未知 |
| 接入、模式、目标歧义、保护拒绝、busy | error/details/recovery/input_state/阻塞 P，structured diagnostic 与 A 边界 |
| 后树失败、执行期间撤权、取消 | executed 保留、observation.error、原 session/cleanup、mcp.* 取消元数据 |
| 未来字段/不可表示复合值 | json v1 完整兜底；未知不能静默漏掉；大于 2^53 整数不经 float64 舍入 |

每个 kind 的 expected A 必须按 §4.6 手写原始 JSON 与 expected app.* 行/golden，不能从被测结构体 marshal 后再把它当预期，防止两边共漏字段。断言 `Decode(text)==expected A==Canonical(structuredContent)`，IsError 与图片逐项一致；both 内部两个通道也须相等。正常已知 app 字段必须走声明的 typed 行，不能靠 json v1 兜底通过 golden；未来未知字段另测无损 fallback。

反向对照分别设门禁：删掉 app.tasks 的分类时，字段清单/known-field golden 必须失败（纯 round-trip 因有 json v1 兜底可以仍通过，不能把它称为 codec 丢失）；只删测试 decoder 对 app.restarted 等 typed 行的接受时，text-only 还原门禁必须失败；直接从预期完整 tasks JSON 中删嵌套 recents_cleaned 时，手写 expected A 字段断言必须失败。recents_cleaned 位于复合 JSON 内，不存在独立 resultFields 项。只检查 both 有 structuredContent 不算 text 通过。正式 renderer 不读取请求/设备来补设备事实。

本地必要命令以实际包落点为准：设置 `GOEXPERIMENT=jsonv2`，运行 protocol、device/android、engine/store、httpapi/client、mcp、mcp-probe 的定向测试；修改这些包的整组回归之后交叉构建 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`。并发回归在支持的环境运行 race；Windows/amd64 支持 race，但需相应 CGO/gcc 条件，未配置或未运行须记录，不能算通过。无需扩展不相关业务页面矩阵。

### 8.2 实机/系统级

本提案起草时全部未执行。实施时仅在受控测试 App/任务上，记录原用户 task/display 基线，只清理本次创建的资源；不杀真实用户应用来证明功能。

A. 受控生命周期：

1. 主屏创建可控任务并写入中文状态，记录精确 task；virtual start 后 list primary 取得候选。
2. claim exact task：ID 保持、主屏不再托管该 leaf、虚屏正确落点；观察中文与返回栈，记录本测试范围内状态证据。
3. 增加同包主屏第二任务；按包 claim 歧义拒绝，原 O 可用且无移动；精确 task 可选择。
4. handoff 一个 borrowed 和一个 owned：移到主屏、会话仍 virtual、后观察原虚屏；end 不删除已交出 task。覆盖默认前台变化与可控的 restore true；不宣称无闪屏。
5. close 单任务/按包、多窗口、borrowed 默认拒绝/显式允许；核验 live/recents 与剩余任务，关闭最后窗口仍可 observe/open。
6. kill 受控后台 owned 应用的主进程与 :remote，核验原实例结束；覆盖主屏同包、borrowed、前台、已知 PLAYING 拒绝及显式允许。先 close 最后 task 后 kill 必拒绝。
7. STOP 与 end：end 清理未交出 owned/borrowed、不自动交还；真实 App STOP 立即撤权；不将已发 Android 命令伪称撤回。确定性难复现的竞态由本地注入，不强做真实设备故障矩阵。
8. 主屏模式由用户在 App 确认后，find/list/open 可用；open ownership=shared且不登记；claim/handoff/close/kill 精确拒绝且未触发设备变更。不得由脚本伪造用户主屏确认。

B. 适配器/探针：

- 主机 stdio 与主机自建 HTTP 各验证 text/structured/both；固定 fake service fixture 用于逐字段同源等价，真实设备调用用于闭环。不同真实调用的 P/时间/页面可以不同，不能跨调用要求字节相等；both 同一次调用与单模式 fixture 才是严格等价口径。
- 手机实际已部署 HTTP 适配器也通过转发入口验证三模式及 11 工具目录；先用 adb forward --list 确认本次设备与主机18883→手机18882映射，结合部署进程哈希排除旧转发/主机端点。不能把 probe 自动起的主机 HTTP 子进程当作手机验证。
- 更新 Go probe 的 text decoder/字段断言，覆盖 app 全字段与无 outcome 查询；结果只打印受控 PASS/FAIL、枚举/布尔/数量，不输出任务正文、凭据、树、PID ARGS。
- 现 CLI 只有 stdio/http/p-recovery。拟新增 `-transport http-remote`：复用 HTTP SDK/鉴权检查，只连接私有配置指定的回环入口，不启动/停止远端进程。配置保持既有 service/http 形状，指向手机转发的私有独立配置由操作者本地准备，不打印 token。
- 拟新增 `-suite app-lifecycle`（默认 smoke 保留现最小检查）：仅使用受控测试目标与明确原任务盘点；fixture 可由 Android Probe 扩展提供。主屏测试需要单独准备，不自动切模式。原 p-recovery 专用路径继续兼容，不把结果模式参数误说成覆盖了它。
- tool catalog count/hash 必须与本次手机 release 和主机 tools 清单对应；宿主实际重启后再次 tools/list，旧 apps/open_app 不存在。运行期先按工具名对齐，验证完整名称集合、description 和 inputSchema 与同源纯目录快照语义相等（对象键顺序不影响比较，数组/数字精度保留），再验证快照对应的 CatalogFingerprint 与两份清单一致；不能直接把 SDK 重排/反序列化后的 tools/list JSON 字节与源指纹比较，也不能只验 count。保持现行源指纹算法，不暗改其顺序语义。
- verify-m5 必须复用有效配置/token，仅在缺失时创建；与手机入口使用独立配置及实际手机 MCP token，不拿主机自建 HTTP token 连接手机。旧脚本若尚未改造，不可直接运行它覆盖已核验的 host-tools 二进制或私有配置。

C. 版本、构建、手机部署与主机替换（实施时执行，不是本轮已执行命令）：

1. 在正式实现前完成设计源头 docs-only 修订。实现期间版本源改为候选 0.9.0/9/11，source_commit 记版本冻结基线；只运行 generate-version 生成 Go/Java/XML/Manifest，不能手改多处常量。提案状态保持“提案，未实现”。
2. 本地必要检查、正式 docs 独立审计 clean 后提交实现；从实际实现提交构建配套产物，清单记录该 HEAD 与真实 dirty。正式交付要求相关源码 clean；不得手填 source_dirty=false。
3. 手机发行构建/核验使用现有命令：

```powershell
./scripts/generate-version.ps1 -Check
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
```

产物为 `build/release/device-service`、`device-mcp`、`control.apk`、固定 u2/applist/service.sh 与 manifest。禁止 SkipAndroid 或不完整清单部署；固定 JAR/dex 不因本功能无理由改哈希。

4. **主机单独构建**：新增脚本读取同一版本源，在 Windows/amd64、CGO_ENABLED=0、GOEXPERIMENT=jsonv2 下构建 `build/device-mcp.exe`、`build/mcp-probe.exe`。不将 linux/arm64 文件改后缀冒充 Windows exe。建议命令：

```powershell
# 以下是拟新增脚本选项，必须先在实现中提供
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

脚本使用 `go build -trimpath -buildvcs=false` 与同源 ReleaseVersion 注入，生成 `build/host-tools-manifest.json`：format/version、release/contract、source_commit/source_dirty、goos/goarch/cgo、tool_catalog count/hash、两个 exe 的相对路径/大小/SHA-256。主机清单与手机清单必须对应同一实现提交和契约/目录；主机产物不硬塞进 linux/arm64 release artifact 列表。输出采用精确暂存/替换，不递归清空整个 build 目录。verify-m5 改为调用该构建入口，SkipBuild 调用 VerifyOnly；不得在清单生成后又用旧 go build 命令覆盖 exe。

VerifyOnly 不运行设备或构建：校验主机清单格式/目标/共同版本/源码与目录身份、两个 exe 大小和哈希，以及手机发行清单的提交/契约/指纹配套关系；失败返回非零，不改写清单凑匹配。单独的 `device-mcp.exe -version` 只打印发行号，不能代替这些校验。

5. 准备完整候选后结束旧会话/STOP，保存原退休未知并完成最多 5 秒逻辑交接，保持发布窗口普通准入关闭。用现有 `deploy.ps1 -SkipBuild -Manifest build/release/manifest.json` 进行整组覆盖部署，不能跳过手机 MCP；不使用 SkipOldServiceStop。保留原签名、配对、消息、停止意图、账本，禁止卸载/pm clear/清库。Android versionName/code、服务 release/contract、当前 App/STOP/模式/可写账本与实际手机 MCP /proc/<pid>/exe 哈希均需核验。
6. 对需要覆盖的 Windows exe，先辨认实际镜像路径和相关宿主托管进程，结束旧活动调用并停止该精确进程，再替换两个主机二进制；不宽泛杀所有同名进程。保留私有配置，重启真实 MCP 宿主连接，重新发现目录。验收记录必须同时包含：实际宿主客户端看到的11个工具名字且无apps/open_app；本次托管适配器进程启动时间/镜像路径（或手机HTTP连接的已核验远端身份）；可取得时附受控mcp.start的tools=11/result_mode。缺少实际客户端发现/运行身份时只能写“主机测试子进程通过，实际宿主未核验”，不能宣布实际宿主刷新完成。
7. 示例 probe 命令（私有配置路径仅为本地约定，不在提案中填凭据）：

```powershell
# 现行参数；实现后 probe 断言应已更新到 11 工具与 app
./build/mcp-probe.exe -transport stdio -binary ./build/device-mcp.exe -config .state/mcp-config.json -result-mode text
./build/mcp-probe.exe -transport stdio -binary ./build/device-mcp.exe -config .state/mcp-config.json -result-mode structured
./build/mcp-probe.exe -transport stdio -binary ./build/device-mcp.exe -config .state/mcp-config.json -result-mode both
# host HTTP 用现 -transport http 起受控子进程；其三模式逐项运行，注意与手机转发端口区分
# 拟新增 remote/suite；配置 http.listen 指向手机实际转发入口，通常主机 18883
./build/mcp-probe.exe -transport http-remote -config .state/mcp-phone-config.json -result-mode text -suite app-lifecycle -timeout 600
```

stdio、host HTTP 与 phone remote 的适用三模式均要有真实结果；app-lifecycle 原地重复危险动作前重建受控场景与新 P，不通过重放副作用比较通道。不能仅运行 --help/工具发现就宣称生命周期实测。
8. 迁移后的 verify-p4 Core/OpenApp 与适用 verify-m5 场景必须使用新契约；verify-m6 的契约对照补上一发行contract8被拒，不能仅验任意数字1。首次v5发行用隔离账本fixture证明0.8.0受控拒读且不改写；不在真实设备上为验证回退而启动不兼容服务。
9. 最终分别报告：源码/清单版本匹配、手机安装及实际进程哈希、Windows 两个 exe 哈希、实际宿主目录刷新证据、三模式编解码、受控 app 闭环、原用户资源保留、容量/退休未知和未执行项。首次0.9.0不支持退0.8.0，只能同v5候选或前滚修复，不通过清库回退。

### 8.3 记录

实现后写入 `maintenance/verification/unified-app-lifecycle.md`：实际实现提交、构建方式、运行环境/设备、命令与退出码、手机及主机清单/哈希、实际入口/转发与结果模式、宿主客户端11工具发现和运行身份、受控task/进程/迁移断言、先红反向对照、容量拒绝/保存边界与未确认汇总、v5不可退0.8.0的隔离负向证据、清理与原资源保留、未执行项/原因。令牌、原始树/截图/命令输出不入库。区分代码实现、单测、手机实测与主机入口实测，不将旧项目历史记录计入本轮。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论与来源 |
| --- | --- |
| 工具组织 | 用户确认统一入口，以 kind 分支；本提案将 apps/open_app 一并合并为 app，七种操作 |
| 主屏 close/kill | 用户明确禁止；claim/handoff 也仅用于虚屏跨屏资源管理 |
| 进程目标/方式 | 用户明确本屏 live task 对应应用；精确 PID kill -9，不 force-stop |
| end | 用户明确沿用现语义，不默认交还窗口 |
| Result/错误方向 | 用户认可现有外层、app.kind、查询无 outcome、逐项事实、惯用 recovery 与后树分层 |
| MCP 三模式 | 用户要求特别核对 text/structured 映射；继承同一 AgentResult 的长期硬契约 |
| 交付范围 | 用户要求实施时更新共同版本、手机部署、构建主机 MCP 与 Go 探针二进制 |
| 当前工作阶段 | 用户本轮要求起草提案；生产实现、版本实际修改与部署在确认后按流程执行 |
| 例行设计选择（供本提案复核） | target 用 app/task_id 互斥，kill 仅 task_id；close borrowed 默认 false；handoff restore 默认 false；kill 保护规则及同屏 borrowed 冲突；查询取消不 end；v5 账本与有界迁移排除；按 kind 处理摘要/IsError；32 KiB列表/全账本16条迁移池与分区预算；候选版本0.9.0/9/11 |
| 交付自动化选择（供复核） | 新增 host-tools 构建/核验清单；probe http-remote 与 app-lifecycle 为实施内容，明确不冒充现有能力 |

### 9.2 未决

接口判据、模式权限、参数默认值、字段、聚合/错误/恢复、三模式映射、版本与交付范围均在本提案给出具体建议，没有留给实现者临时猜测的产品语义。

- **提案确认**：由用户在实施前确认本文（尤其 §4.6–4.8 的返回/摘要与 §6.2/§8.2 的例行选择）；当前状态不得写为已实现。
- **现场条件**：实施负责人在构建/部署前核对当时 HEAD、版本是否已被其他发行占用、实际主机 MCP 镜像路径/宿主、私有配置与设备状态；有变化时更新事实与命令，涉及契约判据/默认值/时序变更则修订提案再确认。凭据不写入提案。
- **主屏实测准备**：用户在实际测试窗口从控制 App 确认主屏共享；未获得该现场条件不能声称 primary 路径实机通过，但可完成其本地模式门禁测试。不得自动模拟用户确认。

## 10. 实施顺序

1. 确认本提案；核对源码/版本基线。先修订设计源头 docs-only，并按适用规则独立只读审计；不在实现暗改 vNext 既有源规格。
2. 定义 protocol/AppView/kind/schema/error/recovery/摘要 fixture，先写真实失败的新增契约与模式/三模式反向测试；建立 v5 责任结构与严格校验。
3. 在接口冻结后可并行：A 做 Android task/root/move/remove/进程事实；B 做协议/客户端/HTTP 与固定 Result fixture；C 做 MCP 三模式、per-kind P/取消/IsError、probe decoder。各自不跨层持状态；最终由主负责人集成。
4. engine 集成 live 对账、O 接纳、迁移持久化/排除、close/kill、STOP/退休/恢复。先验证主屏与交出保护，再跑有副作用实机路径。
5. 更新 App 受控状态与测试目标，版本唯一源及生成文件；统一删除 apps/open_app 公开面，更新 11 工具/目录指纹、强类型解码与必要回归。
6. 建立 Windows host-tools 清单脚本、probe remote/suite；完成 §8.1 定向测试、三模式同源等价与交叉构建。
7. 正式 docs 只写源码已具备的行为；独立只读审计 clean 后提交实现，提案头仍保持未实现。按实际实现提交构建配套手机/主机产物。
8. 按 §8.2 部署手机、替换主机 exe、重启实际宿主并重新发现，运行受控生命周期与适用三模式验收。发现需要改契约/时序的偏离先回提案，不以放宽保护通过测试。
9. 记录真实结果；实现提交存在后另做提案归档，补 §11、实现 hash 与验证记录、重写相对链接。部分落地写明缺口，不以“已构建”冒充“已部署/已实测”。

## 11. 实现结果（归档时补写）

尚无实现提交。本节只在实现提交存在、按流程归档时填写；不得提前把待执行命令或设计推演写成验证结论。

### 11.1 已落地

归档时逐项填写提案条目、代码落点与实现提交，当前不填已落地项。

### 11.2 与提案不同或未执行

归档时逐项填写偏离/未执行及真实理由；当前本提案的全部实现、构建与部署均未执行。

---

## 附：本提案不改变的事项

- engine 唯一权威、原 O/P 单向授权、防重、普通工作唯一位置、STOP 独立与迟到候选条件提交。
- end 的当前清理语义、主屏共享确认来源、没有会话累计时长硬上限、现行 keepalive 与 shell 保留 O 行为。
- MCP 2026-07-28、既有 stdio/HTTP 传输、both 默认、result v1/tree v1、图片只在 ImageContent、stdio stdout 仅协议。
- 固定 u2/applist 制品与许可来源、控制 App 覆盖安装保留私有数据、禁止清库解决签名/配对、当前目标设备与验证边界。
