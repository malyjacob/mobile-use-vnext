# text 流程、详情与恢复提案的审查记录

本记录仅涉及提案取证、审查与文字修订；没有实施 text 变更，没有实机操作。
提案状态为“提案，未实现”；审查共识不代替用户确认。

## 1. 范围与基线

- 提案：[text-write-verification-and-recovery.md](../proposals/text-write-verification-and-recovery.md)。
- 代码基线：`70607aa901f00a23fd718602e899716b6bb32e5f`，`master`，2026-11-18。
- 基线版本：0.17.0 / contract 17 / Android versionCode 19；本次没有修改版本源或生成文件。
- 只使用本仓库源码、正式契约及固定制品，没有访问其他项目。

## 2. 审查方式

按用户要求，使用 `multi_agent_v1.spawn_agent` 显式指定
`model=workbuddy/deepseek-v4.1-flash`、`reasoning_effort=max`，成功创建独立审查者 Galileo
（agent ID `01a1eadb-99f2-70a3-9282-3e27bcb4e842`）。

审查者只读，不实现、不编辑提案、不操作设备。主 agent 负责判断采纳与修订，修订后交同一审查者
复检；正式审查期间不修改其正在审阅的提案。没有用“尚未返回”作为失败理由中断或重启审查。

## 3. 预审与取舍

预审指出：固定 set 的 Value 可作候选，但需同时保留唯一性、Issued、完整终止回执；明确
候选计数是写前证据，Value 是同节点写后刷新证据。该意见采纳。

以下意见采纳到初稿：

- 将完整有效的未 issued 回执记为文字明确未派发，整体 outcome 另按累计原语计算。
- 私有阶段同时覆盖 press_enter=false，并保持公共 text_input 仅用于 true 的既有范围。
- 仅新鲜 ref 的已聚焦目标可以跳过 tap；坐标保留原点点击与后绑定。
- true 分支写入确认不能自动授权 ENTER，保留独立目标/前台检查及最终授权。
- false 的 write 成功与 KnownOutcome 需在同一短锁提交；迟到回执不得改写冻结结果。
- 诊断补充三个原语的派发事实，避免只给文字派发状态却无法解释已点击导致的整体 uncertain。
  采用 details.dispatch 的 focus/write/enter 固定三键，不把私有执行事实变成另一份授权。
- 详情和 recovery 同源，finished text 的现场核对引导 observe，避免语义终态 status 自循环。

限定采纳“认领后不可 retry/O”：原 claimed O 永不可复用；保留当前重验拒绝已交付新 O 时，
首次结果对该新 O 的 retry/O。重放没有新树时按既有规则降级 observe。
限定采纳“finished 不再 status”：会话/耐久/设备准备/cleanup 等真实高优先级事实仍可覆盖恢复入口。

未采纳可选的请求/实际 Unicode 长度详情：这不是当前决策所必需，且会扩展内容派生信息；
只返回封闭枚举、三原语事实与已知候选计数。也没有选择改造固定 JAR，保留真实制品哈希。

## 4. 正式审查与修订

### 4.1 正式第 1 轮：暂不 CLEAN

审阅版本 SHA-256：`7cd684e9e946fcfd110ab9ddd2c65768e102d2017aa2ce225e4f199e9b7b947b`。
审查者确认核心同节点刷新值判据、accepted 的处理、false 原子提交及终态 observe 方向成立，
另指出 5 项必须修订。以下行号指该次审阅版本，不指修订后的行号。

| 发现 | 理由 | 采纳与修订 |
| --- | --- | --- |
| 第 94–100、241–242 行：无 tap 路径直接用 revalidated raw | 会绕过 bindFocusedText 的严格 TaskID 与 focus 过滤前锚点唯一性 | 采纳；§4.1 与算法明确复用完整 binder，从成功 binding 生成 target；加重复 anchor/TaskID 反例 |
| 第 127、137–138 行：not_attempted 与 not_dispatched 混用，残留旧字段名 | agent 无法分辨尚未申请与已经确定未派发 | 采纳；统一 details.dispatch 三键与四状态，分别定义许可前、许可拒绝和明确未调用 |
| 第 151、189–195 行：遗漏 terminated+error | shell 可正常出现进程结束但非零退出，已派发不等于语义成功 | 采纳；增加完整回执表，派发 dispatched、原语 uncertain；补 focus_not_sent 和 ENTER 对应用例 |
| 第 156–162、196 行：冻结的当前 stage 未定义 | 没有先发生错误时无法从“最后失败阶段”推断 STOP 位置 | 采纳；增加 currentStage 与边界表，原语 may_dispatch 同锁更新，冻结不解析 work.Phase |
| 第 220–228 行：live recovery 覆盖的数据和优先级不完整 | 当前 status(P) 没有这些实时数据输入，可能保留过期 observe 或永久污染基础投影 | 采纳；§4.7 列出锁内来源与优先级，原会话有效性独立判断，覆盖只改结果副本，基础 op.Recovery 不保存临时阻塞 |

可选意见一并采纳：Get 的 focused 只代表候选扫描时刻；补其他 origin 的 cleanup_unconfirmed
不构成活动会话屏障的用例。主 agent 另修正成功日志没有错误时省略 stage/reason，避免虚构枚举；
撤回“一次 observe 必然足够”的过强表述，改成无阻塞的 finished uncertain 可直接进入新观察。

### 4.2 正式第 2 轮

审阅版本 SHA-256：`ab624b8d37ff43b7fbb9faf8561519a8f413355e873a2b4606bcc496cce066e6`。
同一审查者已复检并返回 **CLEAN**，确认“未发现剩余实质问题”。其确认的文件指纹与上述值一致。
复检覆盖成功证据、false 原子提交、STOP 冻结、三原语 dispatch、ENTER 单次边界、recovery
优先级与跨会话撤销，以及版本、链接、测试和隐私边界；审查期间未修改提案正文。

主 agent 复核并接受该结论：预审意见经取舍纳入，第 1 轮的 5 项发现均已修订，第 2 轮无剩余发现。
提案在 CLEAN 之后不再改正文，交付用户审阅；这不是实施批准，也不是实现测试或实机验证结论。

## 5. 机械核对

初稿检查通过：0–11 章节顺序、代码围栏配对、14 个本地链接目标与代码行号范围。
实际核对固定 JAR SHA-256 为：
`28361193DE3D13303543C13BDC7268C22EDECABFA4E68E6FBDFC35FFFE1406A3`。

检查命令使用 PowerShell 调用 Python，只读解析 Markdown 相对链接、行号锚点、章节和版本 JSON；
并执行 `Get-FileHash -Algorithm SHA256 third_party/uiautomator/u2.jar`、`git status --short`。
最终机械检查为 PASS：573 行提案与审查记录的 15 个本地链接、代码行号范围、0–11 章节顺序、
代码围栏、1 个 JSON 示例、表格列数和尾随空白均通过。提案最终指纹与审查者确认值一致。
git diff 显示无跟踪文件变化，git status 只有提案与本记录两份新增文件。

## 6. 验证边界

本次新产生的是提案与审查记录；没有运行实现变更测试、构建新发行物、部署、修改 docs 或提交代码。
上文的机制调查和既有定向测试不等于新方案已验证，也不能替代提案 §8 的先红用例与实机证据。
固定制品反汇编和 Go overlay 调查制品位于被忽略的 build 目录，没有将原始设备材料入库。
