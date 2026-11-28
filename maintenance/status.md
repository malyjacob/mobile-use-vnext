# 项目实施状态

2026-11-28，0.40.0（契约 36）交付 `act` 认领后零派发重验自动恢复与统一详情：`transient_retry` / `text_rebind` / `container_rebind`（Attempt 2 后最多一次纯内存重锚；A1 旧父输入唯一性按原始 `Editable` 与公共有面积 `role=input` 并集、A2 排除已输入值并要求占位几何证据、容器身份锚与 2/3 骨架分离），顶层 `Result.auto_recovery`（三策略、双 ref 规则、attempts=2，dispatch 闸门内提交并可经 `status(P)`/重复 P/冻结回放）；`tap`/`long_press`/`text`/`scroll` 的 `409 target_changed` 统一 `phase=revalidate` details，仅首响应交付新 O 时附基于该投影的 `candidates`/`blocking_refs`，不落 `status(P)`；`text` 重验移除 `target_unconfirmed` 覆写，raw `swipe` 保持既有边界；协议/MCP `result v4`/client/mcp-probe/eventlog 同步 `0.40.0 / contract 36 / android 42`。`go build ./...`、`go vet ./...`、linux/arm64 交叉构建通过；`internal/engine` 整包单次 `917.826s`（`-timeout 1000s`）通过，非 engine 全部包通过；`docs/` 与 README 经独立只读审计两轮修正后 `AUDIT CLEAN`；整组发布 `0.40.0`/`36`/`42`（`source_commit=c12d288`、`source_dirty=false`、工具目录 `13/19cb4aa3…`）、手机整组覆盖部署（保留 App 私有数据，health/App 版本、App 配对、恢复屏障、STOP 与手机 HTTP MCP 均 PASS）、Windows 双产物替换与主机 stdio、手机 HTTP 两枚独立探针（`contract=36`、`release=0.40.0`）均 PASS；虚拟屏实机闭环覆盖统一 `409 target_changed` + 新 O + 有效 `candidates`、`status(P)` 反悬垂与 `transient_retry` + `auto_recovery` 回放；`text_rebind`/`container_rebind` 实机成功路径未构造（单测覆盖）；宿主 MCP 进程重启与重连未执行，由用户处理。 [验证记录](verification/2026-11-28-act-revalidation-auto-recovery-and-details.md) · [归档提案](proposals/implemented/act-revalidation-auto-recovery-and-details.md)

2026-11-28，0.39.0（契约 35）交付观察信号精度、`act` 坐标兜底、MCP 目录瘦身与 `app`/`diagnose` 可读性六项收口：`visual=sparse_tree` 门限收紧为 `<20` 字且 `<4` 交互（排除 20–29 字/4–6 交互的完整小弹窗），`wait_for` 终帧的稀疏分类在 `delta` 保留，非 `wait_for` 终帧且无 `unchanged`/`detail_only` 时提示 `hint=wait_for`（即使带图），`wait_for` 终帧仍稀疏或动作后零树变化且无有效图时转 `hint=snap`，有有效图不出 `snap`、无 `visual` 不出 `hint`；`422 unsupported_node_action (reason=no_interactive_ancestor)` 在目标有屏内裁剪几何时附客观 `error.details.target_center: [x, y]`（仅该 reason、不承诺安全、静态拒绝不认领 `O`，`text(submit_ref)` 复用同一规划器）；13 个 MCP 工具发现目录去重压缩至 `tools_list=18,247 B`（净降 33.33%）、`total=28,419 B`，门禁收紧为 `tools_list<=20,500`/`total<31,000`，结构基线指纹证明 13 工具名称顺序、Schema 非描述字段与 4 个只读注解不变；`app.close` 借入拒绝与已知前台服务/播放的 `app.kill` 拒绝附 `details.retry_parameter`（未知态省略），`status(P)`/重复 `P` 对 `open`/`claim`/`handoff`/`close`/`kill` 重放终态定格 `AppView`（首次与各次重放深拷贝隔离；`find`/`list`、运行态、`work_deadline` 冻结与重启后无快照记录仅 `app.kind`，重放不带观察）；无参 `diagnose()` 在存在本 Agent 未结束会话时聚焦该会话（含会话期 detached 操作与零时间戳防回流），操作级 `issues` 按 `(phase, code)` 去重，新增 `error_counts`（全窗口受控计数、不受 8 条截断；`result v4` 在 `diagnosis.issues=` 与 `diagnosis.timing_ms=` 之间输出 `diagnosis.error_counts=<JSON>`），带 `reference` 查询不变。`GOEXPERIMENT=jsonv2` 下 `go build ./...`、`linux/arm64` 交叉构建与 `generate-version.ps1 -Check` 通过；非引擎 15 包全绿；`internal/engine` 整包单次 `901.711s`（`-timeout 1000s`）通过；各工作流含先红/反向对照证据。`docs/` 与 README 经独立只读审计两轮修正后 CLEAN。整组发布 `0.39.0`/`35`/`41`（`source_dirty=false`、工具目录 `13/19cb4aa3…`）、手机整组覆盖部署（保留 App 私有数据，`health` 版本、App `0.39.0/41` 配对、恢复屏障、STOP 与手机 MCP HTTP 适配器均 PASS）、Windows 双产物替换（`device-mcp`/`mcp-probe`）与主机 stdio、手机 HTTP 两枚独立探针（`contract=35`、`release=0.39.0`）均 PASS；虚拟屏实机闭环覆盖正常页不误报稀疏树、冷渲染稀疏首帧 `hint=wait_for`、`status(P)`/重复 `P` 的 `open` 定格摘要、两次同类失败后的 `error_counts` 与 `issues` 去重、借入窗口 `close_refused_borrowed` 的 `retry_parameter` 重放与 `handoff` 定格摘要、`end` 收尾；`target_center`、已知播放 `app.kill` 拒绝与 `wait_for` 终帧稀疏保留未在实机自然触发，由单测反向对照覆盖。依赖方 MCP 宿主进程重启与重连未执行，由用户处理。 [验证记录](verification/signal-precision-schema-slimming-and-diagnostics.md) · [归档提案](proposals/implemented/signal-precision-schema-slimming-and-diagnostics.md)

2026-11-27，0.38.0（契约 34）交付 `act` 展开输入框聚焦绑定与有界只读复查、Tier 2/3 无自有可读文案容器歧义直入 Tier 5、以及 `scroll` 静态 `422` 候选的遮罩/浮层感知过滤与同前景排序（Issue #48 的实现侧收口）：`text` 的 ref 绑定在 exact → near → shifted 之后新增 `expanded`（横向展开与多行撑高，宽高只设 `max(8, 边/5)` 下限、X 轴投影交叠 ≥ 原宽一半，纵向位置本不参与水平带判定）；匿名输入框增加最近非窗口根、非滚动/可编辑父容器的双帧唯一 `resource-id` 锚点兜底（锚点键含类名、父下同类可编辑子框双帧各 1 个、新鲜帧全屏唯一安全聚焦框、`freshNear > 1` 拒绝、零点击跨帧指针不启用）；聚焦 tap 终止后 ref 与坐标路径共用至多 3 次只读 `Capture` 复查（首帧立即、`60ms`/`120ms` 退避、`1200ms` 窗口自首帧未绑定起算、绝不二次 `Tap`、采集/前台/显示/拓扑失败立即收口、STOP 立即 `control_revoked` 且整体保持 `uncertain`），新增 `engine.text.focus` 白名单事件；Tier 2/3 歧义仅在目标无自有可读文案且有子节点、未禁用平移、点击/长按/输入类时跳过 Tier 3 余下尝试与 Tier 4 直入 Tier 5，由 E3 子树证据与 `tier>=4` 全树遮挡裁决，Tier 5 零候选归因 `target_missing` 且不带 `changed_ref`；`scroll` 全部 16 处静态 `422` 候选出口新增跨顶层窗口等值隔离（不依赖固定 `u2.jar` 的 `HashSet` 顺序）、无子无文案遮罩叶与同窗后序实体浮层过滤（空心 HUD 包装层递归、`50%` 精确 2D 并集门槛）、同前景分支优先排序与全剔空候选，`scrollSafeSpansAt` 与固定 `u2.jar` 均保持不变。`GOEXPERIMENT=jsonv2` 下 `go build ./...`、`go vet ./...`、交叉编译（linux/arm64）与 `generate-version.ps1 -Check` 通过；非 engine 15 包全绿；`internal/engine` 整包单次 `885.736s`（`-timeout 1000s`）通过；新增 3 个测试文件、10 个测试函数含逐项先红证据；`docs/` 与 README 独立只读审计两轮修正后 CLEAN。整组发布 `0.38.0`/`34`/`40`（`source_dirty=false`、工具目录 `13/5578041a…`）、手机整组覆盖部署（保留 App 私有数据，`health` 版本、App `0.38.0/40` 配对、恢复屏障与 STOP 可用均 PASS）、Windows 双产物替换（`device-mcp`/`mcp-probe`）与主机 stdio、手机 HTTP 两枚独立探针（`contract=34`、`release=0.38.0`）均 PASS；虚拟屏实机闭环覆盖基础 `start/observe/act/end`、小黑盒折叠评论栏展开（`engine.text.focus mode=expanded attempt=1`，单次 `executed`，基线为 `focus_unconfirmed`）、网易云侧边抽屉与 `v_mask` 下 `scroll` 静态候选整体省略（不再推荐底层页面）、以及关闭浮层后正常页面候选仍 `executable=true` 的反误杀对照；Issue #48 已回复根因与契约 34 取舍并关闭（复核 `CLOSED`/`COMPLETED`）。依赖方 MCP 宿主进程重启与重连未执行，由用户处理。 [验证记录](verification/act-focus-expansion-tier5-disambig-and-scroll-overlay.md) · [归档提案](proposals/implemented/act-focus-expansion-tier5-disambig-and-scroll-overlay.md)

2026-11-27，0.37.0（契约 33）交付 `act` 实时重验的抗外围扰动与 `bubble` 遮挡安全：祖先链不再计入多顶层窗口时插入的合成显示根，第二个顶层窗口闪现不再改变目标祖先深度；Tier 3 与 Tier 5 的非目标祖先只比较稳定结构（包、类、资源 presence、可编辑与点击/长按/滚动/启用/密码/勾选），非根强锚点不再由 `Text`/`Description` 提供；E3 在精确可读多重集不成立时接受数字骨架守恒（纯数字/计量条目可增删、每段 ASCII 数字折叠为 `#`、两侧排序相等且都至少有一条不含数字的不变文案），滚动容器仍按 E1/E2 或共享文案/相同非空资源 + 双向覆盖 ≥85%；新增 Tier 5 同祖先链 + 同水平带平移找回（仅前置 Tier 全零候选且未禁用平移，要求全树双向唯一）；全树遮挡证明由 `tier==4` 扩为 `tier>=4`，覆盖直接目标、`coextensive_child` 父绑定（附加）与 `bubble_colocated` 任一绑定，`bubble_colocated` 在任一绑定命中 Tier 5 时校验双节点纵向共同位移；carrier 解绑扩展为独立内容或目标子树证据；坐标 `tap`/`long_press` 新增唯一交互拥有者守恒兜底（原点永不变、禁用 Tier 5、两侧单拥有者且无外部交互拥有者）；认领后至多读取两帧、间隔 `150ms` 可中断，仅 7 类瞬态归因复检，`STOP` 立即 `control_revoked`，派发与拒绝交付只用最后一帧。写后提交（`submit_ref`）路径保持 Tier 1–4 与精确多重集、不启用 Tier 5。手机整组覆盖部署（保留 App 私有数据）、Windows 双工具替换、主机 stdio 与手机 HTTP 独立探针、契约 33 实机最小闭环（坐标 `tap`、ref `tap`、`text` 写入、重验 `target_missing` 拒绝并交付第 2 帧、交付 `O` 立即复用、`end`）均 PASS；实机未构造外窗过渡、纵向平移与受控 `scroll`，仅单测覆盖。按用户决定未跑 Go 全量测试与 `internal/engine` 整包，改为定向运行（新增+迁移 `44.910s`、此前被截断的尾部 `95.855s`、`internal/eventlog` `0.025s` 全 `ok`），其余 13 包在同一被作废的运行中均已 `ok`；`docs/` 与 README 独立只读审计修正后 CLEAN。[验证记录](verification/act-revalidation-resilience-and-bubble-safety.md) · [归档提案](proposals/implemented/act-revalidation-resilience-and-bubble-safety.md)

2026-11-27，0.36.0（契约 32）交付 `text(ref)` 解除 `tap` 绑定与 `stage=verify` 有界异步读回确认（Issue #49、#50）：`text(ref)` 的静态准入、候选资格与认领后重验只按 `role=input`+公开动作含 `text`+未禁用+非受保护+正面积几何判定，不再要求 `tap`，仍保留目标子树内 4 级聚焦安全点与 `afterTap` 严格获焦证明、禁用祖先提升/共位吸附；阻塞的非输入控件归入 `blocking_refs`。`SetTextOnDisplay` 已确认唯一派发后，`0ms` 刷新值精确相等即完成，否则至多 3 次只读 `GetTextOnDisplay`（`40/80/150ms`，墙钟上限 `1000ms`，受 STOP/取消立即中断）与 `candidateCount=0` 时至多 1 次严格写后重采集+读回；永不重发写原语、不放宽全文精确相等、不采信普通 `observe`，新增有界 `engine.text.verify` 事件。手机整组覆盖部署（保留 App 私有数据）与虚拟屏实机闭环 PASS：探针 App 同步写走 `mode=direct`、默认 `auto_submit=true` 只发一次 ENTER；知乎无 `tap` 正文框 `text(ref)` 直接 `executed`，异步落地在 `mode=poll ms=98` 确认，长文撑高触发 `mode=rebind ms=370` 单次重采集；全程 `auto_submit=false` 零 ENTER、未发布。Windows 主机双工具替换、主机 stdio 与手机 HTTP 独立探针（`contract=32`、目录指纹 `13/5578041a…`）均 PASS；Go 全量单测除 `internal/mcp` 发现字节预算（按要求收敛对外指南后该包全绿）与一条与本次改动无关的长跑抖动（单独重复 3 次通过）外全部通过；`docs/` 与 README 独立只读审计修正后 CLEAN。[验证记录](verification/text-ref-focus-and-async-readback.md) · [归档提案](proposals/implemented/text-ref-focus-and-async-readback.md)

2026-11-25，0.34.0（契约 30）交付动作树变化事实、加载/媒体/稀疏视觉分类与观察级只读 `snap`/`wait_for` 提示：`act` 常规成功在锁外比较被消费前帧与发布帧（前景公开投影与屏幕几何、忽略 `ref` 的紧凑树与完整公开树），按需输出 `tree_change`（`unchanged`/`detail_only`/`matched_base`，常规内容变化省略）；`observation.visual` 闭集扩充为 `loading`/`image_heavy(<pct>%)`/`media_surface`/`sparse_tree`，使用完整公开树、原始采集类名与收敛质量，`sparse_tree` 校准为 `<30`/`<7`，`loading` 在 `delta` 永不省略；新增 `observation.hint`（`snap`/`wait_for`），由已发布 `visual`、`tree_change` 与有效图片确定性推导，不进入 `recovery`；同步 `result v4` `obs` 行 `change=`/`hint=` 列、MCP 发现说明与 `act`/`snap` 指引、独立探针与版本源 0.34.0/30/36。手机整组覆盖部署（保留 App 私有数据）、Windows 双工具替换、主机 stdio 三模式与手机 HTTP both 独立探针、契约 30 实机最小闭环（`act` 零树变化 → `change=unchanged hint=snap` 且 `snap` 不消费 O、历史基线返回 → `change=matched_base` 无 `hint`、稀疏页 → `visual=sparse_tree hint=snap`）均 PASS；`loading`/`media_surface` 实机未稳定构造，仅单测覆盖。Go 全量测试（engine 789.469s）、双清单与发布核验通过；`docs/` 独立审计修正后 CLEAN。[验证记录](verification/observation-change-loading-and-snap-hint.md) · [归档提案](proposals/implemented/observation-change-loading-and-snap-hint.md)

2026-11-24，0.33.0（契约 29）交付紧凑树多子扁平化、有动作长标签截断、静止保号与基线池调整：公开 label 无条件修剪首尾空白并把纯空白/NBSP 归空；compact 下行阶段对超过 250 码点的有动作非 input 标签前置截断并以 `truncated` 布尔标记（`ReadNode`/`ReadSetNode`/`tree v5` 固定列序，严格拒 `null`，detailed 与 root/context 禁 `true`）；所有隐藏匿名容器（单子与多子）按序拍平并自然释放 button 文本提升；静止页面按纯装饰图子树识别等价静态图标容器与安全透明包装层（深度 8、分桶隔离）修复图标/行内文本原地换号，列表回收仍发新号；基线池改为 1 主 + 4 备（跨度 12/10），差分门槛改为严格 `<80%`。手机整组覆盖部署（保留 App 私有数据）、Windows 双工具替换、主机 stdio 三模式与手机 HTTP both 独立探针、契约 29 实机最小闭环（扁平化/提升、静止零差分、跨度 6 备用基线选回、`inspect` 全文四元矩形）均 PASS；Go 全量测试、双清单与发布核验通过；`docs/` 独立审计修正后 CLEAN。[验证记录](verification/compact-tree-flattening-truncation-continuity-and-pool.md) · [归档提案](proposals/implemented/compact-tree-flattening-truncation-continuity-and-pool.md)

2026-11-24，0.32.0（契约 28）交付紧凑读树净化、二元中心点与轻量视觉提示：公开 label 净化零宽/占位码点；compact 执行露边细条、input 标签去重、无标注 image 全裁、祖先复合标签文本叶裁剪、变空容器清理、单子容器绕过、无字可点击 view 提升与 resource 短尾名；root/scroll 保持四元矩形、其余交付二元中心点；新增可选 `observation.visual`（`image_heavy`/`sparse_tree`，full 直出、同态 delta 省略）与 `tree v5`。手机整组覆盖部署、Windows 双工具替换、主机 stdio 三模式与手机 HTTP both 独立探针、契约 28 分帧/差分/基线复用实测均 PASS；Go 全量测试、双清单与发布核验通过；`docs/` 独立审计 3 项修正后 CLEAN。[验证记录](verification/compact-tree-purification-and-point-bounds.md) · [归档提案](proposals/implemented/compact-tree-purification-and-point-bounds.md)

2026-11-23，0.31.0（契约 27）交付受控滚动轴向兼容回退与四档幅度（Issue #42）：公开树为 `role=scroll` 节点投影 `axis`；`act(scroll)` 引入 A+C 轴向兼容判定、父链自动回退（`scroll_axis_fallback`）、唯一轴向兼容内层兜底与认领前 `422 unsupported_node_action`（`reason=axis_mismatch`）+ 候选；认领后轴冲突按 `409 target_changed`（`capability_changed`）零派发；`amount` 扩充为 `step`/`half`/`page`/`full`（15/30/60/85%，500/450/400/300ms）。手机整组覆盖部署（保留 App 私有数据）、Windows 双工具替换、主机 stdio 与手机 HTTP 独立探针、B 站与知乎 `axis` 投影及回退/候选/step-full 实机闭环均 PASS；`docs/` 独立审计 CLEAN。[验证记录](verification/scroll-axis-fallback-and-amount-tiers.md) · [归档提案](proposals/implemented/scroll-axis-fallback-and-amount-tiers.md)

2026-11-23，0.29.0（契约 25）交付树多基线、抗重构目标证明与文本受控提交。手机整组覆盖部署、Windows 双工具替换与双入口独立探针均通过。知乎静止搜索列表两次零差分，知乎与 B 站均在 3 个修订内命中可补看的历史基线。知乎裸滑、回答底栏连续点击和网易云三类文本操作均实测成功。Tier 4 与写后重绑定的精确分支由单测覆盖。 [验证记录](verification/tree-continuity-target-proof-and-text-submit.md) · [归档提案](proposals/implemented/tree-continuity-target-proof-and-text-submit.md)

2026-11-22，0.27.0（契约 23）交付 `wait_for` 内容稳定与 MCP 文本省略。手机整组覆盖部署、Windows 双工具替换、双入口探针、网易云播放页三次稳定等待及 B 站滚动双投影均通过（[记录](verification/wait-content-stability-and-text-omit.md)）。
状态：2026-11-22，0.26.0（契约 22）完成 `act` 静态目标解析策略与错误返回契约统一（#27、#28、#31）：新增 `422 no_safe_trajectory`、以统一 `candidates`/`blocking_refs` 取代 9 个历史引导/诊断键、升级 `scroll` 同视口栈/祖先提升/居中 3 轴线、`tap`/`long_press` 4 级规划与视觉锚定冒泡/共位吸附、坐标 `pointSet` 与 raw `swipe` 豁免及非 `text` `409 target_changed` 9 项封闭 `reason`；手机整组覆盖部署（保留 App 私有数据）、Windows 主机两件产物磁盘替换、双入口独立探针与双入口虚拟屏实机最小闭环均 PASS（[记录](verification/act-target-resolution-and-error-contract.md)）。
2026-11-22，0.25.0（契约 21）完成 `result v4` 紧凑结果外壳、短 `O`/`P` 标识、精确 `recovery` 引用与 1024 截屏长边上限；手机整组覆盖部署（保留 App 私有数据）、Windows 主机两件产物磁盘替换、双入口独立探针与虚拟屏实机 `result v4` 文本闭环均 PASS（[记录](verification/compact-result-envelope-and-image-sizing.md)）。
2026-11-21，0.24.0（契约 21）MCP 无损结果与树文本完成整组发布、虚拟屏双入口逐字段对照和独立探针验证（[记录](verification/mcp-lossless-text-format.md)）。
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
| `observe`/`act` 的 `image=true` | 已实现（HTTP + MCP） | 已验证：JPEG（0.25.0 起长边上限 1024，`1080×2400` 实机为 `461×1024`；此前记录为 `540×1200`）、MIME、无 base64、只读图分支由单元测试覆盖 |
| `snap`（契约 19，第 13 个工具） | 已实现（HTTP + Go client + MCP + 控制 App 文案） | 已实测：`start → keepalive(1) → snap(O) → status(O/P) → observe(image=true) → end` 闭环 PASS；JPEG 540×1200（0.25.0 起长边上限 1024，实机为 `461×1024`）与元数据一致，原 O 保持 `ready`/`effective` 且 `expires_at` 不变，snap 不递增 revision，重复 P/`status(P)` 无图摘要；主机 stdio/http、手机 HTTP 远端与 text/structured 探针均 PASS。实机 STOP/取消精确竞态与截图进行中文案未构造（[记录](verification/snap.md)） |
| MCP stdio / Streamable HTTP | 已实现（`cmd/device-mcp`） | 已验证：工具目录/schema、认证/Origin、图片、取消、P 恢复；P6 回归重新拉起实际适配进程。默认部署同时安装并启动手机 `device-mcp`（手机 `18882/mcp`，主机转发 `18883`），`verify-m6` 检查 `server/discover`；主机侧适配器仍可选 |
| MCP 文本树块 | 已实现 | 已实测：0.4.2 手机 HTTP MCP 的 `start`/`inspect` 文本块，以及主机 stdio 探针的 `observe`/`act` 树块；外部宿主、token 对比、宿主截断与实机超预算未执行（[记录](verification/mcp-tree-text.md)） |
| MCP 树文本无损简写（`tree v3`/`inspect v3`，0.21.0） | 已实现 / 已实测（有限） | `9009599`：省略可还原的 `view`/`text` 角色并简写匿名默认 `view` 链 `[refs]`，外层 `result v2` 与共同契约 `20` 不变；0.21.0/契约 20/versionCode 23 整组构建、双清单核验、手机覆盖部署、主机两 exe 磁盘替换及主机 `stdio`/手机 `http-remote` 独立探针均 PASS；受控 `com.mobileuse.probe` 页最小闭环 `both` 模式 0 mismatch（命中 `shorthand_view_chain`、`omitted_text`、detailed 下 `omitted_view`/`explicit_text`、`full`/`delta` 与 `inspect v3`）；依赖方托管进程由用户自行重启切换，未命中边缘分支由单测与反向对照覆盖（[记录](verification/compact-node-role-text.md)） |
| 默认观察树坐标省略（契约 20） | 已实现 / 已实测（有限） | `6838423`：动作位置优先，无动作 `text` 与匿名节点省略矩形，重复与 1% 面积判据；0.20.0/契约 20/versionCode 22 整组构建、清单核验、覆盖部署，主机两个 exe 替换与两条独立探针 PASS。实机受控页三帧策略复算 0 mismatch、文字坐标全隐藏、`inspect` 补回原矩形、ref tap executed；三份附件回放与 85% 门槛对照未执行（[记录](verification/compact-observation-bounds.md)） |
| MCP 结果模式 | 已实现 | 已实测：both/text/structured 的主机 stdio 闭环、手机 HTTP 三模式各 12 工具及取消恢复；大值仅合成测试，外部宿主/截断/race 未验证（[记录](verification/mcp-result-modes.md)） |
| MCP `result v4` 紧凑外壳、短 `O`/`P` 与精确恢复引用（0.25.0） | 已实现 / 已实测（有限） | `fcf6ca2`：`text`/`both` 改为 `result v4` 的一到两行外壳，观察、会话与集合行紧凑化，`text` 正式与 `structured` 分层；真实 `O`/`P` 改为 4 位实例标签加序号（实机 `O2137_1`/`P4434_1`），`recovery` 按路线 R1 解析为可直接回传的真实编号并在四条红线回退抽象 `P`；截屏长边上限 `1200→1024`（`1080×2400` 得 `461×1024`）。0.25.0/契约 21/versionCode 27 整组构建、双清单核验（13 工具、指纹 `e42f9adb…`）、手机覆盖部署、主机两件产物替换、主机 stdio 与手机 HTTP 独立探针、虚拟屏实机最小 `v4` 文本闭环均 PASS；`docs/` 独立审计 6 项修正后 CLEAN；宿主重连与主屏/其他机型重演未执行（[记录](verification/compact-result-envelope-and-image-sizing.md)） |
| 统一 `act` 静态目标解析策略与错误返回契约（0.26.0，契约 22） | 已实现 / 已实测（有限） | `b305e23`：解决 #27/#28/#31；新增 `422 no_safe_trajectory`，废除 9 个历史引导/诊断键，全 `act` 静态 `422` 统一返回经当前 `O` 干跑的 `candidates`（至多 4 项含 `executable`）与可选 `blocking_refs`；`scroll` 支持双向 ≥85% 同视口免阻、非滚动 `ref` 祖先提升、坐标最内层/回退/消歧与 3 轴线居中轨迹；`tap`/`long_press` 将 `grid_3x3` 扶正为第 4 级执行候选并新增 `coextensive_child`/`bubble_ancestor`/`bubble_colocated` 双绑定重验，彻底删除过期 Legacy 副本；坐标升级 `pointSet`（上限 64）与 raw `swipe` 豁免；非 `text` `409 target_changed` 附 9 项封闭 `reason`。0.26.0/契约 22/versionCode 28 整组构建、双清单核验（13 工具、指纹 `a18acde9…`）、手机覆盖部署（保留 App 私有数据）、主机两件产物替换、主机 stdio 与手机 HTTP 独立探针及双入口设置页 8 步闭环均 PASS；`docs/` 独立审计 9 项修正后 CLEAN；宿主重连由用户处理，第三方复杂页面与 `primary` 模式由 `internal/engine` 全量单测覆盖（[记录](verification/act-target-resolution-and-error-contract.md)） |
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
| 0.25.0 主屏模式、其他机型与跨重启账本标签避让的实机重演 | 未执行：跨重启标签避让、主屏/非零旋转与 4 条恢复红线由 Go 单元测试覆盖，本次实机范围聚焦虚拟屏最小闭环与双入口探针；宿主重连由用户处理（[记录](verification/compact-result-envelope-and-image-sizing.md) §7） |

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
- [MCP `result v4` 紧凑外壳、短 `O`/`P` 与 1024 截屏的实现、0.25.0 整组发布与双入口验证](verification/compact-result-envelope-and-image-sizing.md)
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
