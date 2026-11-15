# 分册 05：实施顺序、验证计划与交付门禁

状态：提案，未实现。
基线提交：`6ec25c87daefbc01c99f78d968214304641d119d`（2026-11-15；代码前进后须重新核对）。
适用范围：本目录四个设计分册的开发、验证、文档同步、实现提交、发布核验与归档。
影响范围：当前只补充本文件与 README；未来实现涉及的文件和公开语义以分册 01–04 为准。
契约：本册不增加公开字段或放宽判据；沿用总览所列的共同 contract_version 变更。

所属总览：[README.md](README.md)。依据：[工程流程](../../process.md) 与[提案模板](../template.md)，尤其模板 §6–§11、流程 §3/§5–§8，以及 [AGENTS.md](../../../AGENTS.md) 的先红复现、验证边界、文档审计、单一版本源和归档顺序。

## 0. 本册定位

本册统一实施依赖、验证口径和交付出口。算法定义仍在[分册 01](01-source-purification-and-state-flags.md)、[分册 02](02-ref-matching-and-continuity.md)、[分册 03](03-structural-delta-diff-and-apply.md)、[分册 04](04-action-revalidation-relaxation.md)。各册的局部测试清单是本册矩阵的细化，不能互相删减。

本次任务只补计划。本文的命令、用例、设备步骤、指标表均是未来执行要求，当前未执行；先前审查中实际运行的基线检查仍见 README §5 和分册 03 的基线记录，不能用来证明新方案通过。

统一采用“确认范围 → 分步先红/实现/反向对照 → 联动回归与基准 → 正式文档与独立审计 → 实现提交 → 干净提交上的完整制品核验与实机 → 记录/归档”的顺序。发生判据、契约或时序偏离时回到提案修订并重新确认，不以实施细节名义修改语义。

## 6. 实现方案与交付组织

### 6.1 落点与责任

| 工作面 | 主要落点 | 验证责任 |
| --- | --- | --- |
| 净化、公开父关系、来源映射 | `internal/observation/build.go`、`read.go` 及 build/read/candidate 测试 | 源头实现者证明 ref、先序、Source/ContextSource 与 raw index 一致 |
| 三态与阅读编码 | `internal/protocol/node.go`、`read_tree.go`、`surface.go`、`decode_response.go`；observation 的 Equal/diff/apply；engine inspect 预算 | 协议与投影实现者证明 full/add/replace/set/inspect 的一致矩阵 |
| 连续性 | `internal/observation/match.go`、continuity/candidate/preview/refstability 相关测试 | 匹配实现者证明全局计数、双向唯一、投票、固定点、预算与高水位 |
| B2 结构差分 | `diff.go`、`apply.go`、`read_diff.go`、delta/read 测试 | 差分实现者证明最终图、残余删除、两套应用器及 wire 往返 |
| 动作与派发 | `internal/engine/act.go`、`identity.go`、`tap_scope.go`、`scroll.go` 及策略文件/相关测试；设备适配器 `internal/android/{foreground,text,rpc}.go` 与 transport/rpc 测试 | 动作实现者证明真实 runtime 入参、撤权优先和授权作用域；复用/补齐适配器消费证据，不为验证强改无关实现 |
| 消费者与版本 | `internal/mcp/`、`internal/client/`、`internal/httpapi/`、`scripts/mcp-probe/`、`release/versions.json` 及生成文件 | 集成实现者核对 schema、严格解码、文本/结构化结果、预算和工具目录指纹 |
| 交付 | 受影响 `docs/`、发布脚本及 maintenance 记录 | 主负责人整合；独立只读审计者审查 docs，不能代替实现或修改被审文件 |

以上是角色，可由同一个实现者分步承担；不是要求再造多套运行状态。重叠文件由单一负责人串行整合，尤其 `read.go`、`read_diff.go`、`act.go`，不得多人同时覆盖。

### 6.2 集成与提交单位

默认以一组完整实现提交交付四册：各阶段是可检查的开发节点，不是分批部署点。五块差分的生成端与应用端、Defaults 的生产与消费、重算 frame 与 runtime 消费必须一起闭合，不能发布只改其中一端的制品。

开发过程可先写独立测试/算法模块；接入前必须等待上游输出和协议矩阵稳定。若确需跨多个实现提交，每个提交均须可构建、测试闭合，涉及 docs 的每个提交先通过独立审计；新契约只作为一致的整组产物发布。提案一直保持“提案，未实现”，最后一个实现提交之后才归档，并列出全部实现 hash。

### 6.3 配置、版本与观测

- 本册不另设公开请求参数、重试次数或输入授权缓存。分册 02 的 `Wmax=128*(Np+Nc+1)+65_536` 是当前提案初值；分册 04 的几何和工作预算取值按 §9 确认。
- 只手改 `release/versions.json`，运行 `scripts/generate-version.ps1`，再以 `-Check` 检查生成文件。共同契约升级覆盖所有服务、App、适配器、客户端及 probe；工具描述改变后的目录指纹由既有脚本计算。
- 测试内部计数可包括候选访问、投票比较、lineage 步数、分配次数/字节、编码字节与枚举失败原因。产品日志只沿用批准事件/字段，不把这些计数变成日志正文、公开树字段或第二份权威。
- 不记录真实标签、输入正文、树、差分、图片、凭据或原始异常。合成 fixture 进入测试代码；真实设备材料只留在受控私有位置，公开记录只写 fixture ID、计数、枚举、版本和脱敏结论。

## 7. 预期效果、成本与能力边界

目标是减少无语义节点、错误 ref 继承、可表达重组的 full 回退，以及有可靠身份时的布局重验误拒，同时保留保守拒绝能力。性能收益以 §8 的同输入比较确认，不预填百分比。

不能以本计划证明所有骨架屏都会消失、任何列表都可保号、任何结构都可发 delta，或识别完全相同观测签名下的页面替换。无描述 ImageView、无唯一证据、根/同 ref 角色变化、缺身份的载体替换以及 Tier 4 都按各设计分册的边界处理。

输出精简不保证内部快照成本降低：Node.checked 的 unknown 显式 null 可能增加 SnapshotCost。记录编码大小、工作量、耗时和分配；保持既有预算、70% 选择、租约、保留策略和 STOP/有限交接规则，不能以放宽上限或吞错掩盖代价。

## 8. 统一验证计划

### 8.1 单元与算法矩阵

下列用例均为计划新增或扩展，具体测试函数名由实现者落盘后记入验证记录。每行至少有正例和对应拒绝/回退例；缺陷用例必须在修改实现前实际失败。先红必须到达真实路径并因行为断言失败，编译/环境失败或零匹配用例不算复现。null 只用于契约已声明 nullable 的字段，矩阵不能把非法 null 变成新能力。

| ID | 对应分册与落点 | 关键输入及期望 |
| --- | --- | --- |
| V01 | 01；build/read 测试 | 已知默认 presence 齐全的空 View/FrameLayout 过滤；非空文本/描述、真实动作/编辑/checkable、非默认状态及 ImageView 例外保留；仅 resource 或 unknown 不足以保留普通空叶 |
| V02 | 01；build/continuity 测试 | 目标不可见祖先→非目标 display 壳→目标文本：壳不发布，context 正确保留；公开父按最近已发布祖先连接且兄弟保持 raw 先序 |
| V03 | 01；build/candidate/preview 测试 | Refs 无重号且等于公开树先序；Source 与 ContextSource 互斥、并集完整；root→cap.Root；普通/context 各 ref 的 raw index 精确正确；下一帧 context 通过 ContextSource 保号 |
| V04 | 01/03；protocol 与两套 diff/apply | 五个 bool 各覆盖 nil/false/true 的全部 3×3 跃迁；full/add/replace 及 inspect tree/subtree/query 缺席恢复已知布尔默认、null 保持未知；set 的 absent/false/true/null 严格分开，另保留 0/空串/空数组；value/hint/resource 的缺席/null 与已知空串分开，axis 缺席/null 为未知，已知值只能是 x/y/both，显式空串非法 |
| V05 | 01；protocol/MCP/probe/client | full、空/非空 delta、inspect tree/subtree/query 均无 defaults；MCP 文本与结构化结果一致；严格解码仍拒绝未知字段、重复键、大小写变体、类型混用、尾随值及非法 null |
| V06 | 01；tree_budget/inspect/read 测试 | compact full、detailed full、subtree、单项 SnapshotCost 的 N/N-1 边界；query 单命中拒绝与总体前缀截断分别测；留存先淘汰未保护项、仅 protected+new 放不下拒绝且失败不删历史 |
| V07 | 02；continuity 测试 | 五张卡 A–E→B–F：共享模板 resource、唯一标题；B–E 及其局部唯一按钮保号，F 新号，不按剩余唯一把 A 给 F |
| V08 | 02；continuity 测试 | 局部已消耗重复模板后，全局 Pass 3/6 仍按完整旧/新树计数；双端多候选、split/merge/跨 anchor 或 R_c≠R_p 不得靠扫描先后配对 |
| V09 | 02；continuity 测试 | Stage 5 至少一个进入前已有、同 rank 的独立强固定点，任一移位/跨组/冲突整组拒绝；无固定点全换新号；Stage 5 只运行一次，之后只局部恢复，不得重跑 Stage 3/5 或给另一桶反向造证据 |
| V10 | 02；continuity/candidate/preview 测试 | 无锚输入仅同父双向唯一保号；多个或跨父不猜；合成 root 延续、cap.Root≠0；曾分配到 n20、上一帧仅剩至 n11 时，新节点仍从 n21 起；分别证明 Candidate/Preview 丢弃保持 prev/nextRef，commit 才精确推进，旧快照不变 |
| V11 | 02；确定性/压力测试 | 在同一语义树和有序 children 下改变 map 插入和候选枚举顺序，归属/预算截断一致；不要求真实 children 重排后新节点的先序编号也不变；通过非公开测试接缝把容量切在目标 batch 的 N/N-1 逻辑操作点，耗尽放弃当前整批、其余分新号；不同 map 插入顺序保持切点 |
| V12 | 03；delta/read 测试 | 新父收养幸存者、两层新增组装边界、拆壳、既有父间移动、Replace 根跨父；纯新增封闭分支内联，只有空顶层 Add 组装根可由 children 接管 |
| V13 | 03；delta/read 测试 | root→D→[A,B] 变 root→A→B；root→D→A→X 变 root→A；Replace 经逃逸祖先间接存活；深层父变化向上传播“不封闭”，不得误选 Replace |
| V14 | 03；Apply/ReadApply 负例 | remove 根被显式复活、被删父 children=[]、不可达显式环、孤立定义、双父/循环/孤儿均拒绝且基线/delta 不变；remove D 单独合法删整树，漏 current 幸存者由 Diff 等值校验发现 |
| V15 | 03；Read wire/engine 选择 | Unpack→规划→Pack→严格 JSON 往返→ReadApply；覆盖 chain 成员、显式空 children、三态；逐字段相等及 canonical bytes 相等；根/角色变化退 full，70% 比例小于/等于/大于三分支分别断言 |
| V16 | 04；身份策略测试 | Tier 1 祖先 Bounds 变化、Tier 2 目标微移、Tier 3 L1 不同但 L2 相同；透明层三内容 presence 均已知空、能力已知安全；root-only L2、多候选/逆向冲突、任一未知透明属性拒绝 |
| V17 | 04；tap_scope/act 测试 | 原点有效不被新候选抢走；目标/原 carrier 微移重算；item_42 原 carrier A 消失且 B 安全允许解绑；无目标自有强身份、仅共享 list 祖先或全尺寸交互覆盖则拒绝，不上溯、不穿 blocker |
| V18 | 04；scroll/text/运行时假设备 | scroll 同容器轴向伸缩后重算并证明整段；离轴超容差/无安全段拒绝；text exact/near/shifted 各可达且 focus 前统计唯一，TaskID 不冒充 window；Tap/Swipe/Get/SetText 的实际参数等于 fresh frame/binding；semantic scroll 使用 scrollDurationMS，raw swipe 显式 duration_ms 原样传递，省略沿用 DefaultSwipeDurationMS（当前300ms） |
| V19 | 04；预算/错误族测试 | unique/none/ambiguous/cancelled/budget/malformed 分开；STOP 优先 control_revoked，预算/畸形树 hierarchy_unavailable；派发前 target/scroll 无候选或歧义为 target_changed，carrier 按 V17 的安全解绑例外处理；已派发后的失败仍按 I06 分层，不把预算或撤权变成可重试的新 O |
| V20 | 04；android transport/rpc 测试 | engine fake 证明新参数到设备接口后，再证明 foreground.go 的受管 Tap/Swipe 消费 display/坐标/duration，text.go→rpc.go 的 Get/SetTextOnDisplay 将完整 TextTarget 与原正文写入正确 RPC 参数；保留单次 set/各次 readback、候选数与 receipt 分层。已有适配器测试足够时引用该证据，不强改其实现 |

V12–V14 必须同时覆盖内部 Node 和公开逻辑 ReadNode，不能只测其中一个。输入/输出对象均做不可变性检查；结构是否合法不能只靠“最后树看着差不多”。相关完整案例清单见 01 §6、02 §5.2、03 §8、04 §9，04 的 N1–N43 按实际编号逐条映射（编号顺序不代表执行顺序）。

### 8.2 假设备、并发与联动矩阵

| ID | 触发顺序 | 必须观察的结果 |
| --- | --- | --- |
| I01 | start/observe/wait_for/app 采集→净化→匹配→ReadDiff→交付→inspect 原 O | 来源索引、compact full/delta、detailed 历史内容一致；inspect 不采集、不续租、不改基线；不新增普通工作队列 |
| I02 | full O 确认基线，连续多次相对同一基线发 delta | 客户端按 delta.base 应用于固定基线；不把上个 delta 当新基线叠加；调用方切换 full 与差分不丢 ref/三态 |
| I03 | Preview 或 Candidate 已生成但最终提交前 STOP/end、显示代次或工作拥有者变化、候选过期 | 迟到候选不发布、不推进 ref/高水位、不确认新基线；旧公开快照不变 |
| I04 | 两 P 同时认领同 O；同 P 重放；静态拒绝与重验拒绝分别触发 | 只有一个认领成功，同 P 不二次派发；静态拒绝不认领，认领后拒绝不归还旧 O；失败观察只走原有条件化交付 |
| I05 | Probe/Capture 中、两轮扫描边界、Tier 空候选/返回末尾、Preview 后、delivery 前触发 STOP | control_revoked 优先；无后续 primitive、无 retry O、无 markDelivered；用阻塞栅栏证明 STOP 不等原工作完成；原工作逻辑退休按既有最多 5 秒交接，不能把 Android 命令物理撤回当验收 |
| I06 | primitive 已派发后 STOP，或主动作/后观察分别成功失败 | 保留 executed/uncertain、会话终态与观察事实分层；不能因为后观察成功改写主动作证据；text focus tap 后失败不重开已结束的 tap |
| I07 | ref 动作重算坐标，同时发同位置的 raw 坐标动作 | ref 使用实际 fresh frame；坐标 tap/text 保留原点授权，raw swipe 保留原 from/to 和显式 duration_ms；省略时使用既有300ms默认，semantic scroll 使用 scrollDurationMS，不因复用 matcher 获得重定向能力 |
| I08 | /api 严格请求、client 解码、MCP text/structured/both 及 stdio/HTTP 假传输 | 新响应格式一致，旧契约混用被原有契约门禁拒绝；stdout 只承载 MCP；工具说明、目录指纹、probe 解析一致 |

并发测试优先用锁外栅栏和实际调用计数证明先后，不用“大睡眠后看没报错”代替。若使用 race detector，必须在受支持且具备相应 CGO/编译器的开发环境单独记录；`CGO_ENABLED=0` 的常规构建不宣称运行了 race，也不为本变更强行新增无关环境矩阵。

### 8.3 反向对照与性能基准

每个真实行为改动至少指定一条“移除该实现/守卫就失败”的用例。对当前实现缺陷，记录先红命令、失败断言、根因 file:line 和同类核查；对新策略，在已确认判据下做正反例并证明旧策略/临时回退不能通过。临时回退只在受控检查中进行，完成后还原；不得改期望值来制造红绿，也不得丢弃其他工作区变更。

基准使用提交入库的合成 fixture 和固定 seed，旧基线与新实现使用同一测试驱动、Go 工具链、环境、节点属性和操作序列。比较对象与结果至少包括：

| 场景 | 样本与量化验收 |
| --- | --- |
| B01 空叶 | raw root 下 32/256 个全默认空叶及一个保留语义兄弟：公开节点恰少对应空叶数，语义节点与 raw 事实保留；新 compact full 字节少于旧编码 |
| B02 列表 | 五卡 B–E 幸存 fixture：已证明身份的四卡及局部唯一按钮保号率 100%，F 复用旧 A ref 为 0；重复无锚点另计，不追求靠猜测提高保号率 |
| B03 结构 | 固定大段不变内容，仅 wrap/unwrap/重挂接：规划成功且精确还原；只有实际编码满足 10×deltaBytes<7×fullBytes 才要求运行时选 delta，小树不满足比例仍可合法 full |
| B04 匹配 | 小树、深层树、重复模板及接近 20,000 raw 节点：记录候选访问、投票比较、Wmax 用量、新号数；预算不超已确认上限且截断确定，无错误复用 |
| B05 动作 | 20k 旧/新节点的零/单/多候选、深度 1 全 ancestor-mismatch：每 Tier 定向候选访问≤Np+Nc，target+carrier 最多三 Tier 的扫描总计≤6×(Np+Nc)，不构造 Np×Nc；lineage 步数、visited/memo/slice 的分配全部计量 |
| B06 边界 | 五布尔未知密集、较长已知文本、chain 打包、预算截止：记录 full/delta/内部成本及 bytes/op、allocs/op、ns/op；具有可观察身份/几何/作用域差异的结构和输入安全反例，错误接纳/误派发数为 0；N36 只核对不可辨识边界，不要求识别完全同签名替换 |

正确性和确定性上限是必过门禁；耗时、分配和端到端轮次采用同环境前后比较，不能预填“提升 xx%”。每个基准做预热，正式重复至少 5 次并记录分布。若发现明显回退，由实现者给出归因和取舍，主负责人决定修正还是回到提案调整；不得只提高预算。微基准时间不能代替设备输入延迟或宿主是否将输出落盘的实测。

拟新增的 benchmark 可命名为 `BenchmarkContinuity...`、`BenchmarkStructuralDelta...`、`BenchmarkActRevalidation...`。运行前用 `go test -list` 核对匹配项非空；仅有 PASS 或“no tests to run/no benchmarks”不算执行证据。

### 8.4 实机/系统验证计划（当前未执行）

默认只使用一部已有支持设备、一个已选显示模式和一个实际 MCP 宿主入口；默认手机适配器路径。stdio/主机 HTTP 的编码边界先由 I08 覆盖，不因本提案扩展无关设备、模式、应用生命周期或断电恢复矩阵。若真实改动进入其他分支，再按差异增加对应路径。

使用本仓库 [android-control/test-target](../../../android-control/test-target/) 的受控页面；现有页面不具备的状态先扩展合成 fixture，再记录新增测试代码与构建/安装命令，不假定当前 probe 已能覆盖骨架、包装层和位移。只有固定测试包允许承受设计的点击、滚动和文本写入。

| 场景 | 执行步骤 | 验收与记录 |
| --- | --- | --- |
| D01 净化/输出 | 同页面切换默认空 View 占位开/关；start→observe→inspect；比较旧/新同输入版本 | 空叶数与公开节点变化相符；可读/可动作内容仍在；记录 compact/detailed/文本字节，若要声称不落盘必须在指定实际宿主记录其结果 |
| D02 连续性/差分 | 唯一标题、共享 ID 的列表滚动一项，再原地改一行文案并增删包装层 | 存续内容 ref 保留、新项新号；验证 delta.base、还原结果及编码比例；不满足唯一/比例时接受新号/full，不为指标放松规则 |
| D03 点击/滚动 | 提示条收起、微移、稳定 item 的载体替换、滚动容器轴向伸缩；分别加入阻塞子控件 | 安全正例执行，反例拒绝；只记录 fixture 对应结果及实际 primitive 计数/枚举；不以设备画面“像点到了”代替目标状态证据 |
| D04 输入 | 点击有稳定锚点输入框使键盘顶起；另测同类其他框获焦与来源差异 | 正确字段读回合成文本，其他字段不变；焦点仅作门禁；无身份/错误焦点拒绝或按派发证据 uncertain，不自动再点另一个框 |
| D05 STOP/重入 | 在受控延迟采集/重验及派发后分别停止，查询原 P/O；随后按当前准入条件开启新会话 | 无迟到 O/二次 primitive；撤权和主动作/后观察结果分层；逻辑交接不把旧物理未知变成永久封锁；只记录实际证明窗口 |
| D06 宿主联动 | 覆盖安装、启动实际服务/App/手机适配器，并重启实际 MCP 宿主进程重新发现工具；做一条 full→delta→act→inspect→end | 新契约、生成版本与 manifest/目录指纹相符；文本/结构化解析一致，来源同一已核验制品；端口启动不作为干净或恢复成功证明 |

各场景固定前置页面和操作序列，至少重复 3 次；任一误派发必须失败并归因，不以成功率平均掩盖。设备/API/显示模式、源提交、产物 hash、次数、输出字节、错误族、必要的延迟统计及主动作/观察/会话结论分别记录。没有设备或缺 fixture 的项写“未执行”及原因，不能凭单测写已实测，也不能在正式发布结论中隐去缺口。

### 8.5 命令入口与执行条件

以下命令是未来执行清单，均从本仓库根目录运行。PowerShell 环境设置与主机测试应在本机可执行目标下运行，不沿用交叉编译的 GOOS/GOARCH；示例采用 Go 1.27 系列。每条命令独立检查退出码并记录；本文未实际执行它们。

```powershell
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

go test ./internal/protocol ./internal/observation -count=1
go test ./internal/engine -count=1
go test ./internal/android -run 'Test.*(Text|Swipe|Tap)' -count=1
go test ./internal/client ./internal/httpapi ./internal/mcp ./scripts/mcp-probe -count=1
```

按 §10 在真实依赖变化后运行对应包；最终组合变化覆盖以上受影响包，不重复运行无关功能矩阵。基准在用例真正落盘且 `-list` 非空后运行，例如：

```powershell
go test ./internal/observation -run '^$' -bench 'Benchmark(Continuity|StructuralDelta)' -benchmem -count=5
go test ./internal/engine -run '^$' -bench 'BenchmarkActRevalidation' -benchmem -count=5
```

提交前的版本/常规目标构建检查。控制 App 的 GeneratedVersion.java/XML 会随版本改变，需候选编译；test-target 有变更时另构建验证 probe，不能等到提交后才发现 Java 消费者无法编译：

```powershell
./scripts/generate-version.ps1 -Check
./android-control/build.ps1
$env:GOEXPERIMENT = 'jsonv2'
$env:GOOS = 'linux'
$env:GOARCH = 'arm64'
$env:CGO_ENABLED = '0'
go build ./...
```

若 test-target fixture 有变更，再单独构建验证 probe；该 APK 不进入发布包：

```powershell
./scripts/build-probe.ps1
```

交叉构建后恢复主机环境再运行 Go 测试；构建脚本的 SDK/JDK 参数使用本项目已声明的环境并记入记录。这些 APK 是提交前候选，不代表完整 manifest 已核验；不在 Windows 上把 ledger 的耐久行为写成已验证。

完整制品的构建、核验与部署只在 §10 的干净已提交实现上进行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json
```

部署命令使用已有连接参数/私有配置，不把凭据写进命令记录；正常覆盖安装并保留 App 私有数据。`-SkipAndroid` 只生成不完整开发包，不是本计划的发布或实机候选。若使用主机工具，先按现行流程运行 `scripts/build-host-tools.ps1` 并核验其清单；手机 http-remote probe 不启动/停止远端适配器。

现有 probe 的接口检查可以复用：

```powershell
./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode both -suite smoke -manifest build/release/manifest.json
```

私有配置由操作者按现行 runbook 准备，不入库。通用 smoke 不替代 D01–D05 的新增受控场景；如要自动执行这些场景，须实现对应受限 suite/fixture 后才运行并记录实际参数，不虚构 probe 选项。

### 8.6 验证记录与结论分层

实现时维护 `maintenance/verification/observation-continuity-and-revalidation.md`，开头声明是否涉及实机；本次不创建空的“通过”记录。至少包括：

1. 分诊、确认范围、四册与 V/I/B/D ID 到实际测试函数/fixture 的映射、未决项关闭结论。
2. 代码基线/全部实现提交、提案版本，平台/Go/Android/设备/模式/实际 MCP 入口及制品 hash。
3. 每个缺陷的先红命令与失败断言、根因 file:line、同类核查；策略反向对照对应守卫与失败证据。
4. 实际执行命令、退出码、用例数、真实计数/字节/基准分布；未执行项和原因，不复制私有材料。
5. 已实现、已单测、假设备/系统验证、已实测分别列出；基线通过、设计推演、链接检查不能混入新实现通过数。
6. 文档独立审计者、范围、file:line 发现及修正/复检结论；发布与实际部署所用 manifest 的来源提交。
7. 与提案不同或未执行的内容及理由；失败/修复/复检链保持可追溯，不只记录最后一次绿结果。

## 9. 已定与未决

### 9.1 已定的执行约束

| 事项 | 结论 |
| --- | --- |
| 当前授权 | 只补提案；不视为用户已经确认实现或授权设备/发布动作 |
| 依赖 | 净化/三态/公开索引先稳定，连续性与结构差分分别验证后联动，动作随后接入；完整协议生产/消费一起切换 |
| 身份与权威 | 阅读 ref 不授予输入权限；源 Has*/Set、STOP、工作归属、Candidate 最终提交与 O/P 单向规则保持 |
| 版本 | 共同契约升级，版本号只改 versions.json；不为每个开发阶段分别发行不兼容半成品 |
| 发布 gate | 默认且本计划采用的发行源固定为最后实现提交，归档提交只承载维护记录；manifest 必须与实际发行 checkout 的 HEAD 一致且源码干净，候选构建成功不等于正式核验 |
| 结论边界 | 算法/假设备/实机分别留证；不能承诺全部 full 消失、误派发靠平均通过或同签名替换可识别 |

### 9.2 实现前必须确认的取舍

由用户或其明确指定的契约负责人，在 S0 进入实现前确认并写入本表；实现 agent 可提出数值和证据，不能自我批准。下列建议不是已获确认的行为。

| 事项 | 当前建议/依据 | 谁、何时决定 |
| --- | --- | --- |
| Tier 2/3 几何与中心/尺寸阈值 | 沿分册 04 的 0.50/0.60 及尺寸/中心约束作为候选；用边界 fixture 说明可接受与不可接受范围 | 用户/契约负责人，S0；更改进入提案修订 |
| scroll 离轴容差 | 分册 04 的 8px/2% 是候选，不能删掉离轴稳定检查 | 同上，S0 |
| shifted 是否接受 Description-only | 分册 04 写了资源/描述两类锚点；须确认描述锚点是否属于本次范围，唯一性先于 focus | 同上，S0 |
| Tier 3 无非根 anchor | 建议不允许，保持当前修订稿安全边界 | 同上，S0 |
| Tier 4 | 默认不纳入本次实现；若要加入受限动态文案转换，另给转换集合和正反例再确认 | 同上，S0 决定范围；未确认不得启用 |
| 动作工作量/深度上限 | 04 的 4096 与 1<<20 是候选值；可用合成参考驱动核对成本，不能当作已实测最优值 | 同上，S0 确认初值；S6 基准验证适配性，若须改判据再确认 |
| 无实机/fixture 缺口是否可接受 | 默认正式验收要求 D01–D06；若交付只接受单测/假设备证据，须逐项预先列出缺口且不得标已实测；代码实现状态与验证覆盖状态分别记录 | 用户/交付负责人，S0 决定可接受范围；S9 出现新缺口时重新明确接受，否则不能进入 S10 |
| 发行号/Android versionCode 与实际验证环境 | 在实施时按当前 versions.json 决定唯一一致的升级组合；设备、模式、宿主从已有可用环境选择，不新增无关矩阵 | 发行/验证负责人，S0 指定角色，S7 定稿版本，S9 设备执行前记明具体环境 |

S0 可在提案确认时一次认可上述默认建议，不要求逐项重复审批。若有不准备实施的可选项，明确排除并写理由，不能留下“实施后看着办”；未决必选项未关闭不得开始依赖它的实现。

## 10. 具体实施顺序与阶段出口

### 10.1 依赖和可并行范围

主路径为 `S0 → S1 → S2 → S3 → S4 → S5 → S6 → S7 → S8 → S9 → S10`。
S3 是连续性，S4 是 B2；各自纯算法/合成 fixture 可在 S2 之后并行准备，但共享 `read.go/read_diff.go` 串行整合；S4 的实际 capture→matching→ReadDiff 联动必须在 S3 完成后验证。S5 的纯 matcher 测试可提前准备，正式接入要等 S2–S4 的公开树、source map 与阅读契约稳定。

S4 内部顺序为 Node 规划/Apply → Read 适配/严格 wire 往返 → engine 的 publishTreeAttempt 真正选 delta，分别留证；不能只证明算法可表达而没测运行时。
S5 内部顺序为纯 matcher/原因分类 → ref 点击和 carrier/frame → text 首次焦点 tap、Get/Set/readback binding → scroll frame 与 raw 坐标隔离 → STOP/交付竞争。先扩展假设备记录完整 display、坐标、duration 和 TextTarget，包括预检/每次读回/单次 set；不能让只计次数的旧 fake 冒充新 bounds 已进入 RPC 的证据。

所有阶段按“读真实入口 → 先红或策略正反例 → 实现 → 单测 → 反向对照 → 本阶段回归 → 记录”执行。未满足出口不靠多次重试覆盖过去；影响已有判据时回到 S0 修订。

| 阶段 | 具体动作与产出 | 出口与依赖 |
| --- | --- | --- |
| S0 分诊/确认 | 重新核对 HEAD 与四册真实入口；给每个源码问题分诊，列 V/I/B/D 映射、负责人和 §9.2 决策；确认本次实际路径 | 问题定义与路线获用户确认，必选未决关闭；不把这次补计划当成实现许可 |
| S1 复现与参考输入 | 建立最小默认空叶、跨 display/context、三态、列表、wrapper/unwrap、微移/焦点 fixture；落盘对应测试驱动和计数接缝，采集旧基线数据 | 缺陷先红+根因+同类核查；新策略的反向对照明确；新增 benchmark 匹配项非空，基线数据标明旧行为；fixture 不依赖其他项目 |
| S2 净化与协议基座 | 按 01 改值过滤、跨非目标 display 子树标志传播、最近公开父组装、root/context；统一 Node/Read 三态与比较；删除 Defaults，联动 inspect/MCP/probe/client/预算 | V01–V06、V10 的 Candidate/Preview 基础及 I08 通过；真实消费者编译/测试一致，无协议半切换；生成版本在整组交付时统一处理 |
| S3 连续性 | 按 02 建公开索引与全局完整桶，接五阶段、集合投票、局部恢复、固定点与高水位/预算；保持来源映射 | V07–V11 及 B02/B04 通过；相同有序输入的归属确定，无弱证据反向造强锚点；预算截断只降保号率 |
| S4 结构差分 | 按 03 接入 Add 组装前沿、最终 live/residue、Replace 封闭性传播、最高 remove 祖先覆盖；同时落地 Node/Read 应用器和 pack/wire | V12–V15、B03 及 S3+S4 联动通过；合法重组精确还原，非法声明无部分树；实际70%选择与固定 base 正确 |
| S5 动作策略与实际派发 | 按 04 分开 L1/L2、定向扫描/反向唯一、透明 presence、原因分类；点击 frame/载体安全解绑、scroll frame、text binding 接入真实 runtime | V16–V20、I04–I07 及 N1–N43 对应范围通过；fake Tap/Swipe/Get/SetText 参数精确；没有祖先越权、焦点猜测、STOP retry 或坐标动作重定向 |
| S6 联动与成本 | 运行 I01–I08 和 B01–B06；核对全套相关包、不可变性、源码/协议守卫反向对照；比较旧新计数、字节、耗时、分配 | 正反用例无错误接纳/误派发；确定性预算达标；成本变化有记录与归因，不能用改变上限/70%/STOP换绿；真实改动的包回归完成 |
| S7 文档/版本/提交前构建 | 确定版本组合并运行生成脚本；同步已具备行为到受影响 docs；运行版本 freshness、linux/arm64/CGO=0 构建、控制 APK 候选编译；test-target 改动时 probe APK 也编译，并做消费者检查 | 构建和相关测试通过；独立只读审计覆盖本次全部 docs，发现修正并复检 clean。审计期间不改被审 docs，不因尚未返回中断/重启 |
| S8 实现提交 | 反向核对四册与本计划的功能、契约、边界和未执行项；将源码/测试/docs/生成版本按 coherent 单位提交 | 实现提交 hash 存在，提案仍“提案，未实现”；提交说明写分诊、必要跳步理由（若有）、文档审计结论及偏离；工作树满足正式发布所需干净条件 |
| S9 完整制品与系统/实机 | 在 S8 的干净提交构建完整 release，verify manifest 后覆盖部署、重启实际 MCP 进程，再执行 D01–D06 | manifest complete=true、source_commit=实际HEAD、source_dirty=false，版本/哈希/目录指纹全符；D01–D06 全通过，或按 S0 接受的交付范围逐项记录未执行缺口，新缺口须再获接受；不把无实机误写成已实测，也不把验证缺口自动等同于代码部分实现。失败先归因修复、相关测试/审计及新实现提交，再重建核验，不归档为全部完成 |
| S10 验证记录/归档/发行锚点 | 完成 §8.6 记录；先存在最后实现 hash，再移动整个提案目录、重写链接和追加 §11 两表；单独归档提交 | README 与01–05每份头部按真实代码覆盖改为已实现/部分实现（最后实现hash、日期）并加验证记录链接，缺口逐条说明；status.md 仅真实结论变化时修改；发行源保持最后实现提交 |

### 10.2 manifest 与提交顺序的特殊约束

实际 [verify-manifest.ps1](../../../scripts/verify-manifest.ps1) 要求“已提交、源码干净、manifest.source_commit 与实际 HEAD 一致”，[build-release.ps1](../../../scripts/build-release.ps1) 的 `-SkipAndroid` 包标为不完整。因此正式 manifest PASS 不能被写成 S8 实现提交前已完成的门禁；S7 完成的是测试/生成 freshness/构建及文档审计，S9 才产生可部署制品的正式核验。

本计划固定最后实现提交（S8，若 S9 修复则取最后一个新实现 hash）为发行源。S9 在该提交的干净 checkout 构建、核验、部署和实测；S10 归档提交携带已经产生的验证记录和 §11，明确它是维护记录提交，不是这次发行源码。这样验证证据先存在，再由归档记录引用，不要求归档提交记录它自己的未来核验结果。

归档后若需再次核验或发布该制品，应在同一仓库固定于该实现提交的干净 checkout 运行原有脚本，并核对同一 manifest/制品来源；可以保留该发行 checkout，将归档记录放在维护分支。不得在归档后的不同 HEAD 下复用旧清单冒充 PASS，也不要求操作其他项目仓库。

若未来另选“从归档提交发行”，必须另行给出不依赖后续源码提交的发布证据保存方式、核验和必要 smoke，并修订交付计划。本册不默认执行第二条路线，不将最终 HEAD、归档 hash 和已验证实现 hash 混为一谈。

S9 无实机环境或某 fixture 未覆盖时，不伪造通过；在验证记录和归档 §11.2 明确缺口与原因。是否接受该交付范围由用户/交付负责人按 S0 约定决定，不能把“没测到问题”改写为“已实测”。

### 10.3 失败、变更与停止条件

- 缺陷复现不红：继续取证，不改实现；根因落在规范上：转取舍，不暗改语义。
- 单项/联动/安全反例失败：停在该阶段，保留实际失败与修复证据；回归范围随真实差异调整。
- 性能预算耗尽：按 02 的新号降级或 04 的层级错误处理；不得强制保号、跳 Tier、取消 source/owner 检查。
- STOP/end：遵守既有撤权和派发门禁，不因验证需要延迟停止、重开 P/O 或宣称撤回已派发命令。
- 文档审计：只读审计结论未返回就等待；明确失败/无响应且记录原因后才重发，提交说明记录原因。
- 版本/manifest/签名/配对不符：按现行发布流程排查，不跳清单、不手改多个版本、不卸载/清库解围。

## 11. 实现结果（归档时填写）

本次没有实现提交或新方案验证结论。归档时在 README 写最后实现 hash、全部实现提交与验证记录链接；README 与01–05六份文件头部都更新真实状态、最后实现 hash、归档日期及验证记录链接，并为本册及四个设计分册补两张表：

- §11.1：分册条目、V/I/B/D ID → 实际文件/函数/测试/实现提交及真实验证范围。
- §11.2：与提案不同或未执行的项 → 实际行为、缺口与理由；判据/契约/时序偏离附重新确认依据。

整目录移到 `maintenance/proposals/implemented/observation-continuity-and-revalidation/` 后，本册到仓库源码/docs 的相对链接需再上一层，到 maintenance verification 的链接也需重新计算；保留各文件名并检查所有目标。验证记录写本次真实实施，不把本册的未来命令或前轮静态审核记录直接复制成已执行结果。
