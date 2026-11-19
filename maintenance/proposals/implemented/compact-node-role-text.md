# 提案：在 MCP 树文本中省略可还原的节点类型

状态：已实现（实现提交 `9009599df87ac192787e730f9e9970b42d8f97e6`，2026-11-20）。
实现与验证记录：[compact-node-role-text.md](../../verification/compact-node-role-text.md)。
基线提交：`a1490d3f03cd288aeffe270595c795e0acc3ad12`（分支 `master`，`2026-11-20`）。
适用范围：MCP 的 `text` 和 `both` 中的观察与补看树块，含差分里的完整节点定义。
影响范围：`internal/mcp`、`scripts/mcp-probe`、版本源与生成件、正式文档及发布验证。
契约（`/api/*` 字段与 `contract_version`）：不改变。共同契约保持 `20`，HTTP 和 MCP 结构化结果沿用现行定义。MCP 树文本块使用独立的 v3 语法。

函数、行号、常量和默认值以该提交为准。实现前按当时的 `HEAD` 复核
[MCP 渲染](../../../internal/mcp/tree_text.go)、[探针](../../../scripts/mcp-probe/result_text.go)、
[版本源](../../../release/versions.json)及[发布规则](../../../docs/release.md)。

## 0. 摘要

普通节点的类型可由行首格式准确恢复时，MCP 省略 `view` 或 `text`。
匿名 `view` 链简写为 `[n10 n11]`，`group` 链继续显式写类型。每个原编号、完整文字、
动作、父子关系和状态均可恢复。
发布覆盖手机整组部署、最小实机闭环及 Windows 两个 exe 的构建替换。依赖 Agent 由用户切换。

## 1. 问题

### 1.1 现象与代价

分诊为取舍。现行契约要求 MCP 每行显式输出节点类型，重复的普通节点和文字行因此占用更多上下文。
DSH 一次 v19 私有会话含 60 条工具结果，其中 25 个整树、10 个差分。整树占这批观察树约
87% 的 `o200k_base` 代理 token。60 条结果中 59 条集中在同一个 turn，样本代表一次长操作。

同一批 25 个 full 从 v19 文本解析，在离线重放 v20 坐标规则后试写新节点行，并逐帧还原。
下表按整块树文本的 UTF-8 字节和 `o200k_base` 分帧计量，包含块头尾。

| 离线写法 | 字节合计 | 代理 token 合计 |
| --- | ---: | ---: |
| v20 坐标规则与 v2 记法 | 135,169 | 39,287 |
| 省略可推导的 view，缩写匿名 view 链 | 126,310 | 37,372 |
| 再省略可推导的 text，并保留有歧义的显式 view | 123,200 | 36,750 |

最后一行比重放的 v20 少约 8.9% 字节、6.5% 代理 token。776 个 `text` 定义中有
775 个可省略类型，153 次带引号标签的普通 `view` 定义需保留显式类型。
原始轨迹及派生树留在私有环境。现有数字的证明范围是这组离线文本。DSH 实际模型输入、
计费、其他任务的收益及真实 v20 设备的分布都要另行测量。

### 1.2 证据链

| 已核对的行为 | 依据 |
| --- | --- |
| v2 输出每个完整定义的类型。整树、补看和 delta 中 add/replace 的完整定义共用写行函数 | [tree_text.go:86](../../../internal/mcp/tree_text.go#L86)、[tree_text.go:115](../../../internal/mcp/tree_text.go#L115)、[tree_text.go:182](../../../internal/mcp/tree_text.go#L182)、[tree_text.go:403](../../../internal/mcp/tree_text.go#L403) |
| 普通 view 也能带文字或描述。`text` 同时是节点类型与输入动作词 | [build.go:361](../../../internal/observation/build.go#L361)、[build.go:382](../../../internal/observation/build.go#L382)、[node.go:14](../../../internal/protocol/node.go#L14) |
| 匿名链以一个定义交付多个连续 ref，孩子属于末成员 | [read.go:320](../../../internal/observation/read.go#L320)、[mcp.md:311](../../../docs/mcp.md#L311) |
| 文本与结构化结果可还原为同一个 Result。无法可读渲染时走既有 canonical JSON 兜底 | [mcp.md:159](../../../docs/mcp.md#L159)、[mcp.md:355](../../../docs/mcp.md#L355) |
| full/delta 按 compact JSON 字节和已确认基线选择。子文字 ref 做默认 bubble 点按时，重验绑定子节点及交互祖先 | [capture.go:254](../../../internal/engine/capture.go#L254)、[interface.md:455](../../../docs/interface.md#L455) |
| 独立探针识别 v2 块头，在检查链路中直接使用该版本 | [result_text.go:115](../../../scripts/mcp-probe/result_text.go#L115)、[checks.go:631](../../../scripts/mcp-probe/checks.go#L631) |
| 共同契约与 MCP 文本版本独立。探针从清单读取目录指纹 | [决策 D33](../../decisions.md#d33-mcp-结果模式共享无损事实集)、[release.md:46](../../../docs/release.md#L46)、[catalog.go:209](../../../scripts/mcp-probe/catalog.go#L209) |

### 1.3 为什么按字面猜类型会出错

普通 `view` 也可带引号说明。把所有带文字的行读成 `text` 会改变类型，因此需要显式
`view` 覆盖默认值。格式还必须区分角色位置的 `text` 与动作位置的 `text`。
长文预览在样本中最多再省 0.76% 代理 token，最大帧未变，按需读全文会增加一次
`inspect` 往返。[#25 已按不计划实现关闭](https://github.com/malyjacob/mobile-use-vnext/issues/25)。

## 2. 目标与非目标

- 新文本编码能逐字段恢复同一公共结果。保留节点与链成员 ref、角色、父子与兄弟顺序、
  完整文字、三态、几何和动作。覆盖 full、delta 的完整定义和三个 `inspect` 范围。
- 对同一个含可简写节点的合成夹具，新树块严格短于 v2；热路径输出确定，探针能发现并识别新语法。
- 本案只改 MCP 可读编码。删除包装、文字上卷、长文本裁剪、标签去重、包装链与孩子合行、
  改坐标及调整基线或 70% JSON 门槛均超出范围。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 沿用 v2 | 继续显式写类型和 `chain` | 无迁移成本 | 重复结构词继续占用上下文 |
| R2 可逆简写 | 指定默认角色和匿名 view 链记法，块头标示 v3 | 更新读取器、说明与整组产物 | 漏处理类型冲突时会破坏等价 |
| R3 删除包装或上卷文字 | 改变展示的树结构或标签位置 | 重定节点寻址、差分与目标验证边界 | Agent 可能失去原子文字 ref 或分组 |

拟选 R2，有同批帧收益，且能保留同一公共树。R1 保留为验证与回退对照。R3 在结构体积仍
构成实测瓶颈时再议。样本的 10 个 delta 都指向最近的 full，修订差 1–2，基线策略继续沿用。

## 4. 判据

普通行由原缩进、ref、可选显式类型、现有字段列组成。读取 ref 后依下表判断。

| ref 后的第一项 | 恢复的角色 |
| --- | --- |
| 不带引号的合法角色词 | 该显式角色 |
| JSON 字符串字面量 | `text`，字符串作为第一个 label |
| 几何、动作、字段标记或行尾 | `view` |

发送端按原列序判断移除类型后露出的首项。仅当该首项按默认判据恢复出原类型、且不会被
当作显式角色词时，才省略角色。

- `text` 的首项是 label 时省略类型。前面仍有已知矩形或未知矩形 `-` 时显式写 `text`。
- `view` 的首项是 label 或不带引号的角色词时显式写 `view`。例如只露出输入动作
  `text` 会撞到角色位。用现成的显式 `view` 消歧，动作拼写沿用原词表。
- 其他角色始终显式。引号内的角色名仍是标签。
- 合法匿名默认 `view` 链在几何已隐藏、其余共享字段原本就无输出时简写为 `[n10 n11]`。
  节点定义开头的 ref 列表与普通 ref 后的矩形是不同位置。`group` 链保留 `chain [refs] group`，
  其他合法链也沿用 `chain [refs] role ...`。链成员顺序、末成员归属与计数沿用现行规则。
- 差分的 `set`、`children`、`remove` 继续使用原语法。`add`、`replace` 的完整定义使用
  本规则，同时遵循现有定义根和 ref 限制。

```text
n1 root [0,0,1080,2400]
  [n10 n11]
    n45 "设置"
    n46 view "推荐内容"
    n47 button [20,80,300,160] "确认" tap
    n48 [20,200,300,280] tap
```

## 5. 算法

1. 适配器取得现行 canonical Result 和图片载体，按原模式选择文本、结构、图片及错误投影。
2. 通过可读校验的 full、delta 或 inspection 使用 `tree v3` 或 `inspect v3` 块头。沿原遍历
   次序写行，按 §4 省角色词或缩写链。块头其他字段和计数保持原定义。
3. 无法可读编码的值走原 canonical JSON 兜底。外层 `result v2`、`diagnostic v1`、分隔行和
   结束标记沿用原定义。
4. 撤销、发布失败、超时及预算耗尽继续由原路径产生结果。适配器编码既有结果，无新增采集、
   等待、状态、缓存或树预算。

`服务产生 Result → MCP 文本块 → 独立读取器还原 → 比对 canonical Result`

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| [internal/mcp/tree_text.go](../../../internal/mcp/tree_text.go) | 更新树和补看块头，在共享节点写行路径实现 §4 |
| [internal/mcp/schema.go](../../../internal/mcp/schema.go) | 更新 `treeTextGuide`，与握手发现同源发布 |
| [内部树文本用例](../../../internal/mcp/tree_text_test.go)与[结果等价用例](../../../internal/mcp/result_parity_test.go) | 更新金样，独立读取并逐字段验证 |
| [scripts/mcp-probe](../../../scripts/mcp-probe/) | 更新块识别、正文合法性检查及用例，smoke 核验实际 v3 出口 |
| [release/versions.json](../../../release/versions.json)与生成件 | 使用审定版本生成共同发行身份 |
| [docs/mcp.md](../../../docs/mcp.md)和版本引用文档 | 更新公开语法、示例、现行版本引用 |

### 6.2 配置与取值

以 `tree v3`、`inspect v3` 标示本语法，沿用 `text`、`both`、`structured` 的选择方式。
拟定 `0.21.0`、共同契约 `20`、Android versionCode `23`。负责人确认提案时核对，实施者在
冻结前重新检查版本源及基线。

### 6.3 日志与可观测性

沿用事件白名单。探针和验证记录只记录投影结果、计数、命令、阶段和版本等批准事实。
原消息、树、截图、令牌、私有配置和响应正文留在验证环境。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一执行权威、STOP、限时交接、条件提交 | 仍由 engine 负责，适配器仅写已有结果 |
| ref、几何、内容与 O/P | 公共树和快照沿用 v20 投影，既有动作重验照常工作 |
| 差分与三态 | 原 JSON full/delta 选择与重放规则适用，保留 set 的缺失和显式值 |
| 文本/结构等价 | 独立读取后与 canonical Result 一致，图片载体和诊断规则沿用 |

### 6.5 契约与文档影响

MCP 文本语法由块头 v3 标识。按照既有[版本边界](../../decisions.md#d33-mcp-结果模式共享无损事实集)
和[发布规则](../../../docs/release.md#2-单一版本源)，共同契约保持 `20`。外部文本读取器按块版本识别
语法，也可选现有结构化结果模式。共同契约匹配仍允许旧适配器与服务通信，因此旧进程可能继续
输出 v2。实际验证以新进程镜像、握手说明和 v3 树块为证。同步 MCP 说明、图例、示例、README
和正式文档中的现行发行引用。仍发布 13 个工具，目录指纹按构建产物核验。实现提交前通过第 15 条
只读文档审计。

## 7. 预期效果与能力边界

### 7.1 能治什么

减少普通节点、纯文字节点和匿名视图链反复出现的词。Agent 仍可直接用子文字编号指定目标。

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| 页面本身节点多、正文长 | 每个原节点的可读内容与结构仍需交付 |
| 宿主仍可能截断单次结果或压缩历史 | 宿主有独立的输出和上下文规则，本案只改变树块字节 |
| 依旧经常发全树 | 选择门槛计量的是公共 compact JSON，强制 full 路径沿用原规则 |

### 7.3 成本

节点行仍一次遍历。额外的首项判断复用已有字段。无设备往返或新增服务状态。
调用方需要理解默认角色，解析文本的外部实现需要识别 v3。

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| 无字段、矩形、标志或普通动作起始的 view | 省略类型并还原原 view，列序一致 |
| 角色首项组合 | 对所有合法角色、三种几何状态和可构造的首项类别做表驱动往返。覆盖引号 label、`value=`/`hint=`、`tap`/`swipe`/`long_press`/`text`、状态词与行尾。无 label 的 text 保留显式角色，首项冲突的 view 同样保留显式角色 |
| 链与几何 | 在 full 与 delta 的完整子定义覆盖短 view 链、显式 group 链、矩形与普通节点。核对链 ref、孩子归属与 `nodes`/`entries` |
| full、delta、inspect | 独立读取还原所有完整定义和三个补看范围；差分重放还原目标树；set 明确保留缺失、`false`、`0`、`""`、`[]`、`null` |
| 内容与版本 | 转义、非 BMP、长文字、原 JSON 兜底、错误与图片等价。探针树块检查只接受 `tree v3`、`inspect v3`，拒绝缺块及同契约的 `tree v2`、`inspect v2` 固定样例 |
| 反向对照 | 去掉简写使新金样和合成缩减断言失败；忽略显式 view 例外使独立等价测试失败 |

执行 `GOEXPERIMENT=jsonv2` 下的 `go test ./internal/mcp ./scripts/mcp-probe`、版本生成检查
和 linux/arm64、`CGO_ENABLED=0` 的交叉构建。合成夹具随库，私有样本数字用来比较设计选项。

### 8.2 实机/系统级

1. **整组安装与入口身份。** 用 §10 核验清单后覆盖部署同源手机整组，保留控制 App 私有数据。
   读回 Android 版本、服务发行/契约、App 配对和 `can_start`。比对服务与手机 HTTP MCP 的
   实际 `/proc/<pid>/exe` 哈希和清单。磁盘文件与运行镜像分别核验。
   记录当前恢复和 STOP 状态，照实保留旧退休记录的物理收尾事实。
2. **最小实机闭环。** 在配对手机虚拟屏，使用现有受控 `com.mobileuse.probe` 页面执行
   `status → start → app open → observe(full=true) → act(tap, ref) → inspect → end → status`。
   从新文本树找按钮 ref，核对 `executed`、新观察和点击计数变化。比较同一 `both` 结果的简写
   文本与结构化角色、ref 及完整标签，补看返回 `inspect v3` 的对应 detailed 节点。
   `end` 后确认会话结束并恢复 `can_start=true`，照实记录 cleanup。实机报告只覆盖现场命中的
   语法分支和实际收到的 full/delta，其余分支依据单测。
3. **主机替换与双入口。** Windows 两个 exe 由同一实现提交重建并替换，记录文件路径、大小和
   SHA-256。用新探针启动独立主机 stdio，另连手机 HTTP MCP，依次跑 `smoke`，核对实时
   的 13 个工具及目录指纹、握手说明和 v3 树块。`-manifest` 在探针中只供应预期目录身份，
   两组清单的源码、发行和制品身份由 §10 的清单核验确认。探针分别结束自己取得的会话。
   实机记录注明两条通路实际返回的块版本，v2 拒绝结果列入单测记录。受控页面步骤可合入
   一条 MCP 通路，另一条通路完成独立 smoke。
4. **依赖方边界。** 用户自行处理其 Agent、托管 MCP 进程、重连和工具发现。记录区分磁盘
   替换、独立探针和依赖方切换。维护侧只操作自身的主机进程与范围内的手机部署进程。

### 8.3 记录

写入 `maintenance/verification/compact-node-role-text.md`，记录实现与构建提交、环境、
执行命令与结果。分别注明已单测、已实测、实际两份清单、两个主机 exe、手机身份与配对、
最小闭环和 cleanup 事实。依赖方由用户处理，未执行项说明原因。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 优化范围 | MCP 可逆类型与链简写，保留完整正文、动作及节点关系 |
| 格式边界 | `tree v3`、`inspect v3`，外层结果、HTTP 数据模型与共同契约 `20` 沿用现行定义 |
| 消歧方式 | 显式角色覆盖默认值，动作词沿用 |
| 发布范围 | 手机整组覆盖部署、变更路径实机最小闭环、Windows 两个 exe 替换及双入口独立探针 |
| 依赖方 | 用户自行切换依赖 Agent 和它们托管的 MCP 进程 |

### 9.2 未决

- 项目负责人审阅时确认本提案范围与 `0.21.0 / 20 / 23` 的版本建议，并决定实施时间。
- 实施者在发布窗口前核对配对设备、虚拟屏、回环端口、Origin 和本机目标路径。配置采用
  当次既有部署值。目标不明或门禁未通过时保留阶段证据并停在该处。

## 10. 实施顺序

1. 独立审阅收敛后呈交负责人。获确认后复核基线与版本冻结。只在 `release/versions.json`
   手工更新版本源。`source_commit` 记录版本冻结时的基线，两份构建清单记录同一实际构建
   提交。执行生成、实现、受影响回归和反向对照。
2. 同步正式文档。通过独立只读文档审计后提交实现。提案此时仍为「提案，未实现」。
3. 在同一个干净的实现 `HEAD` 构建并核验手机与 Windows 产物。提前核对主机目标路径及
   持有文件的进程，只处理维护范围内的独立进程。核验通过后用哈希确认两个 exe 实际替换。
   两份清单的发行、契约、源码提交及工具目录指纹须一致。

   ```powershell
   ./scripts/build-release.ps1
   ./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
   ./scripts/build-host-tools.ps1
   ./scripts/build-host-tools.ps1 -VerifyOnly
   ```

4. 进入手机部署窗口，暂停新普通工作。用旧版本 `end` 或 STOP 撤权，按发布规则等待最多
   5 秒逻辑交接，照实保留未确认物理事实。用 `./scripts/deploy.ps1 -SkipBuild` 及现场所需
   端口和 Origin 参数覆盖部署，保持默认手机 MCP 启动。失败按
   [发布回退边界](../../../docs/release.md#5-失败与回退)处理，保护私有 App 数据与账本。
5. 按 §8 验证实际部署、跑最小闭环和双入口独立探针。以下私有配置路径以当次现有配对和
   转发为准，先核对权限及 Origin。

   ```powershell
   ./build/mcp-probe.exe -transport stdio -config .state/mcp-config.json -binary build/device-mcp.exe -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
   ./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
   ```

6. 完成验证后写记录，按流程将提案移入 `implemented/`，补实现提交、§11 和验证记录链接，
   检查相对链接。先实现、后归档分别提交。发布结果分别注明手机、主机和依赖方责任。

## 11. 实现结果

实现提交：`9009599df87ac192787e730f9e9970b42d8f97e6`（2026-11-20）。
实现与验证记录：[compact-node-role-text.md](../../verification/compact-node-role-text.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| `tree v3` / `inspect v3` 块头与普通节点可逆角色省略、匿名默认 `view` 链 `[refs]` 简写 | `internal/mcp/tree_text.go` `renderFullTreeText`/`renderDeltaTreeText`/`renderInspectionText`/`writeNodeContent`/`writeChainRefs`/`isAnonymousDefaultViewChain`/`omitsNodeRole`/`nodeFirstColumnIsRole`/`writeNodeColumns` |
| 全局树图例与 `inspect` 工具描述同步，工具目录指纹更新 | `internal/mcp/schema.go` `treeTextGuide`、`inspect` 描述（13 工具，SHA-256 `914fc4b061e14eb42fa75288e1c695e1649e49797c662a012013abdb265d1dc5`） |
| MCP 树文本、说明、等价与服务测试及反向对照 | `internal/mcp/tree_text_test.go`、`internal/mcp/schema_test.go`、`internal/mcp/result_text_test.go`、`internal/mcp/result_parity_test.go`、`internal/mcp/server_test.go` |
| 探针 v3 识别、正文语法/计数检查及 v2/缺块拒绝 | `scripts/mcp-probe/result.go`、`scripts/mcp-probe/checks.go`、`scripts/mcp-probe/result_text.go`、`scripts/mcp-probe/probe_test.go`、`scripts/mcp-probe/result_modes_test.go` |
| 版本源 `0.21.0 / 20 / 23` 与生成件 | `release/versions.json`、`internal/protocol/version_generated.go`、`android-control/app/src/main/java/com/mobileuse/vnext/GeneratedVersion.java`、`android-control/app/src/main/res/values/strings_version.xml`、`android-control/app/src/main/AndroidManifest.xml` |
| 正式文档同步与独立只读审计 | `README.md`、`docs/mcp.md`、`docs/architecture.md`、`docs/release.md`、`docs/control-protocol.md`（独立只读文档审计 `CLEAN`） |
| 同源整组构建/清单核验、手机覆盖部署、主机双 exe 替换、双入口独立探针与受控页最小闭环 | 详见 [compact-node-role-text.md](../../verification/compact-node-role-text.md) §4–§6 |

### 11.2 与提案不同或未执行

| 项 | 情况与理由 |
| --- | --- |
| 依赖方宿主及托管 MCP 进程切换 | 按提案 §8.2.4 与发布边界，未停止或重启 Windows `DeepSeek Harness.exe` 托管的 `device-mcp.exe`（`PID 33184`）与手机 `dsh-mcp-connector` 托管的 `device-mcp -transport stdio`（`pid 20888`）；磁盘产物已替换，独立探针与受控页闭环已用新产物验证，依赖方切换由用户自行处理。 |
| 实机未命中的语法边缘分支 | 受控 `com.mobileuse.probe` 页面实机命中 `shorthand_view_chain`、`omitted_text`、detailed 下 `omitted_view`/`explicit_text`、compact `full`/`delta` 与 `inspect v3`；显式 `view` 撞标签或输入动作 `text`、`group` 链、未知几何 `-`、delta 内链、`inspect query` 及同契约 v2 拒绝未单独构造实机页面，均由 §3 单元测试与反向对照覆盖。 |
| 真实 DSH 轨迹的模型输入/计费测量与其他第三方应用分布 | 未执行：超出最小实机闭环范围；提案 §1.1 离线重放数字只作方案比选证据，未冒充实机收益。 |

## 附：本提案不改变的事项

- HTTP 字段、JSON `chain` 表示、几何策略、共同契约号、服务协议与工作状态机。
- 内容与顺序、节点 ref、动作执行、补看语义、整树与差分的选择和重放。
- 宿主上下文生命周期、文本预算、截图编码及工具集合。
