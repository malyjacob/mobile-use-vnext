# 提案：校准 MCP 工具发现说明与只读注解

状态：已实现（实现提交 2c0852ffa6999e482cd033d393994d313273b3fc，2026-11-19）。

实现与验证记录：[第二批工具发现校准](../../verification/mcp-tool-descriptions-governance.md)。第一批独立修复见[无 P 恢复投影](../../verification/mcp-no-operation-recovery.md)。

基线提交：`a53f95a8131931d96ccc4c1a246dd5653e993947`（分支 `master`，`2026-11-19`）。

适用范围：MCP 全局指令、重点工具描述、三个查询字段的入参 Schema、`tools/list` 只读注解与发现探针。无 P 恢复投影作为配套缺陷独立提交与验收。

影响范围：`internal/mcp`、`scripts/mcp-probe`、`docs/mcp.md`、`docs/integration.md`。配套缺陷涉及 MCP 适配器及其回归测试和取消说明。

契约（`/api/*` 字段与 `contract_version`）：不改变。共同契约为 `19`。MCP 公开的说明、三个 Schema 约束和只读注解会变化。配套缺陷修复会调整 MCP 传输失败及取消结果中由适配器合成的 `recovery`。

文中的代码位置、默认值和测量数据以本基线为准。实施前若 `HEAD` 已前进，分别复核投影路径、
目录 Schema、探针断言与文档。版本取自 [release/versions.json](../../../release/versions.json)。

## 0. 摘要

按两个批次推进，可合并进入同一次发布。第一批修复无本次 P 的查询失败却附带 `status/P`
的缺陷，按无提案缺陷流程提交和记录。第二批是本提案的取舍范围，重排全局说明及高密度的
`act`、`app` 描述，校准实机反馈暴露的调用分流，补齐只读注解及查询 Schema。
每批各自通过回归和对应的文档审计，部署时从干净提交构建并核验产物。

## 1. 问题

### 1.1 现象与代价

- **缺陷。** 适配器在 `status`、`inspect`、`diagnose` 的传输失败或无服务结果的取消中，
  可以给出 `recovery.entry=status`、`recovery.reference=P`，但本次查询没有分配 P。
  调用方按该提示找不到可查询的本次操作。
- **发现与说明的取舍。** 基线的发现体积指标是 **28,050 B**。其中全局指令 7,051 B，
  重编码工具数组 20,999 B。工具描述文本合计 8,802 B，Schema 重编码合计 11,491 B。
  `act` 与 `app` 都用连续长段落，描述和 Schema 两项加起来约占工具数组的一半。
  `bubble`、`press_enter` 及部分静态拒绝语义在多层复述。关键规则也承担宿主降级时的兜底。
- **调用反馈。** Gemini 的实机任务报告涉及 `no_safe_point` 后如何填写坐标、
  `scroll` 的位移证据与容器选择、`wait_for` 的条件组合和超时、同一 `base` 的 delta
  重建、新观察遇到尚未让位的受管命令。它还报告 Antigravity 将全局说明与工具 JSON
  分开保存，所查看的工具 JSON 没有注解。这些反馈用于选定审查场景，宿主行为与实际误用率
  均限于已有样本。
- **发现目录的取舍。** 当前仅 `snap` 发布只读注解。三个查询字段的 Schema 缺少
  `minLength: 1`，现行接入或引擎已经拒绝空值。全局引用说明也没有覆盖所有工具的例外。

### 1.2 证据链

| 路径 | 基线证据 |
| --- | --- |
| 无 P 投影的先红复现 | 审查时用临时 Go overlay 扩展 [MCP 本地夹具](../../../internal/mcp/server_test.go#L48)。关闭回环假服务后走适配器入口，`status({})`、`status(reference=P7)`、`inspect(reference=O7)`、`diagnose({})` 均得到 `transport_unavailable`、空 `operation_id` 和多余的 `status/P`。用同一测试文件调用取消投影的无服务结果分支，`call_cancelled` 带出同样的恢复字段。既有 [只读取消测试](../../../internal/mcp/server_test.go#L1119) 验证无 `end`，当时未断言恢复省略。 |
| 根因与同类核查 | [适配器入口](../../../internal/mcp/server.go#L150) 对三项只读查询以空 `operationID` 发起调用。[传输投影](../../../internal/mcp/server.go#L382) 的两个合成点及[取消投影](../../../internal/mcp/server.go#L260) 的初始化和兜底都调用 [adapterRecovery](../../../internal/mcp/server.go#L435)，它只看错误码。`app.find/list` 预分配 P，取消分支明确清除恢复。`snap` 同样分配 P，取消分支丢图且无 `end`。服务自身的恢复来源独立，例如 [status 自查](../../../internal/engine/status.go#L29) 可用 `start_operation_id` 提供归属。 |
| 字节与宿主 | 审查运行 [TestDiscoveryByteBudget](../../../internal/mcp/schema_test.go#L390)，实测总量 28,050 B，通过现行 `<36,250 B` 门槛。该指标取说明 UTF-8 长度与工具数组 JSON 之和。既有验证记录区分了 [Codex 目录与模型投影](../../verification/mcp-tool-descriptions-calibration.md#L37) 及 [剥离全局指令的配对回放](../../verification/mcp-tool-call-guidance.md#L53)。小样本给出误用场景及方向性结果。 |
| `wait_for` | [轮询逻辑](../../../internal/engine/wait_for.go#L298) 以 OR 合并两个条件，`stable=true` 时另外检查稳定门槛。[终帧评估](../../../internal/engine/wait_for.go#L628) 先收集实际命中项，超时时再追加 `timeout`。审查时修改 [已有终帧夹具](../../../internal/engine/wait_for_test.go#L755) 使条件仅在最后采集的帧命中，得到 `trigger=timeout` 和 `matched=["query","timeout"]`。[MCP 成功判据](../../../docs/mcp.md#L239) 依赖是否交付合法核心 O。 |
| `shell` | 审查以 [命令启动未确认夹具](../../../internal/engine/shell_test.go#L394) 复现 `status(P)=finished/uncertain`、`status({}).blocking_code=shell_pending` 以及同 Agent `observe` 返回 `device_busy` 并指向该 P。[普通工作准入](../../../internal/engine/ordinary_admission.go#L18) 单独检查 [shell 屏障](../../../internal/engine/shell.go#L149)，[起会话状态](../../../internal/engine/engine.go#L684) 可投影该屏障。 |
| 坐标、滚动与 delta | [建议坐标规则](../../../docs/interface.md#L504) 要求坐标重试独立准入与 fresh 证明。[滚动结果](../../../docs/interface.md#L634) 区分 `outcome` 和 `effect.state`。[delta 还原](../../../internal/observation/apply.go#L34) 以完整基线和本次差异重建当前树。 |
| 注解与 Schema | [注册](../../../internal/mcp/server.go#L73)、[目录快照](../../../internal/mcp/catalog.go#L25) 和 [目录探针](../../../scripts/mcp-probe/catalog.go#L88) 都按仅 `snap` 标记。引擎的 [status](../../../internal/engine/status.go#L9)、[inspect](../../../internal/engine/inspect.go#L43)、[diagnose](../../../internal/engine/diagnose.go#L26) 都是查询。[inspect](../../../internal/engine/inspect.go#L55) 拒绝空串，[wait_for 解码](../../../internal/protocol/requests.go#L344) 也拒绝全空白。 |

### 1.3 为什么直觉会出错

- 把关键说明都移到全局会使工具描述单独交付时缺少调用所需的信息。已有回放中，剥离全局
  说明后出现过等待工具选错和文本恢复偏离。保留局部简述可以覆盖这种投影。[调用引导验证](../../verification/mcp-tool-call-guidance.md#L53)
- 按字节数齐头删减会一并删去关键参数分支。宿主的投影与分词还会影响最终上下文用量。
  当前证据可以支持分项度量和局部重排，模型行为提升需要另行验证。
- `ReadOnlyHint` 表达工具意图。实时查询仍依赖调用时刻的状态，宿主如何展示、审批或
  缓存需按该宿主验证。`snap` 也仍需要工作位和本次 P。

## 2. 目标与非目标

1. **第一批**消除无本次 P 的适配器恢复误导，覆盖传输失败与取消，独立完成先红、修复及回归。
2. **第二批**按 §4 校准指定发现内容。重点重排全局指令和 `act`、`app`，补齐已定位的
   `start`、`notify`、`wait_for`、`shell` 等提示，保留已验证过的关键调用语义。
3. 使 `tools/list`、源码快照和探针一致发布四项只读提示。安全的 Schema 细化限于三个
   字段的最短长度与相关说明。
4. 工作范围限于 MCP。引擎的 `shell` 终态命名、恢复决策与准入属于单独分诊事项。
   现有输入组合器和指纹覆盖范围继续使用。多语言、`outputSchema`、动态提示与误用统计
   属于独立需求。

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| 最小修复 | 修投影并局部补正已有工具说明。 | 变动小，测试集中。 | 长段落与注解差异仍存在，重复说明仍可能漂移。 |
| **拆分并定向校准** | 先独立修投影，再校准发现元数据并重排重点说明。 | 两次验收及文档审计，目录变更随发布核验。 | 重排时若删掉兜底，只有工具描述的宿主仍可能走错。保留 §4 的语义守卫。 |
| 一次全量重写 | 统一改写 13 项描述、Schema 文案和全局指令，同时变更投影。 | 审查和回归范围大，须复查更多调用情景。 | 元数据和结果行为同时变化，调用偏离时难以归因。 |

选择**拆分并定向校准**。传输和取消问题能用确定性回归独立验证。发现说明可逐条映射已有误用场景并
用新旧目录对照。若定向修改之后仍有可复现的宿主误用，再按实际缺失的说明扩展范围。

## 4. 判据

### 4.1 第一批 · 无 P 恢复投影

| 路径 | 预期 |
| --- | --- |
| `status`、`inspect`、`diagnose` 的适配器传输失败或未取得服务结果的取消 | 省略适配器合成的 `recovery` 和本次 `operation_id`。保留错误码及现有取消详情。连接恢复后可用原参数重新查询。 |
| 只读查询取得服务结果 | 沿用该分支现有的服务事实保留规则。服务依据原操作或会话给出的有效 `recovery` 保持可见。 |
| 已分配 P 的调用 | 传输失败保留已有 `status/P` 恢复。取消沿用各工具分支。`snap` 仍丢图、跳过 `end`，按原逻辑以本次 P 恢复。`app.find/list` 取消继续省略恢复。`end(P)` 的条件和边界沿用现行规则。 |

请求中用于定位原事实的 `reference` 不参与适配器是否合成 `status/P` 的判据。
依据是本次调用实际分配的 P。没有本次 P 时也无需给出新的恢复枚举。

### 4.2 第二批 · 说明职责与关键调用语义

| 层级 | 主责 |
| --- | --- |
| 全局指令 | 会话与租期、O/P、跨工具恢复、工具选择和完整结果图例。完整树图例发布一次。在 delta 段开头先说明「当前树由 `base` 指向的 full 加本次 delta 重建」，同一 base 的多条 delta 分别应用到该 full。 |
| 工具说明 | 核心用途、调用前置、关键副作用、结果和下一步。重点分段整理 `act`、`app`。当规则影响工具选择、输入授权或不确定结果恢复时保留必要的短复述。`start` 保留 O/P 和 full/delta 的轻量提示。 |
| 字段 Schema | 合法形态、范围、默认值与适用分支。保留 `bubble` 和 `press_enter` 的分支限定默认语义，继续省略两字段的通用 `default`。 |

- **等待与观察。** 明确 `observe.wait_ms` 是一次采集前的延迟，等待条件用 `wait_for`。
  同传 `query`、`absent_query` 时，任一成立即可满足查询条件。另有 `stable=true`
  时，还要通过稳定门槛。超时且发布新 O 时 `isError=false`，`wait.matched` 包含
  `timeout`，也可包含终帧命中的条件。终帧未发布按观察失败处理。
- **点按建议。** `no_safe_point` 且有 `error.details.suggested_point` 时，原 O
  仍 ready 可用同一 O 和新 P，以 `operation.target={x,y}` 尝试坐标分支，省略 `bubble`。MCP
  为重试分配新 P。坐标独立通过准入和重验，原静态拒绝仍维持零派发及不认领 O 的语义。
  保留静态拒绝三码的区别与一次最近祖先提升边界。
- **滚动。** `effect.state` 的 `observed`、`not_observed`、`unknown` 报告内容位移证据。
  执行事实由 `outcome` 表达。嵌套时按预期滚动区域选择明确的容器 ref。保留 `scroll`
  与原始 `swipe` 的分流，以及树节点 `swipe` 能力的含义。
- **文本。** 保留一次调用完成必要聚焦、写入及读回、按 `press_enter` 决定是否发送 ENTER
  的边界。已认领且整体 `outcome=uncertain`，实际结果给出 `observe/P` 时，用原活动
  会话的具体归属 P 观察框值或页面。仅聚焦已派发也可能进入该分支。按结果区分是否需要另发
  ENTER，避免原样重发。
- **受管命令。** `status(P)=finished` 表示操作记录结束。受管命令终止未确认时普通工作仍
  可能返回 `device_busy`，同 Agent 可看到相应 `blocking_operation_id`。
  `status({}).blocking_code` 可显示 `shell_pending`。遇到该阻塞短暂退避，原会话仍活动
  时再观察。已有其他阻塞时按实际状态处理，`can_start` 留给新会话判断。
- **引用与生命周期。** 补齐全局引用说明中的 `snap`（活动原会话的 O/P）、`diagnose`
  （可省略或传 O/P）和 `app.list`（ready O 或可解析到活动原会话的 P）。`app` 按
  `find`、`list`、五种变更分支组织，保留各分支的引用和副作用。`notify` 自身说明
  可在没有活动会话时调用，且无 `reference` 参数。

### 4.3 第二批 · 注解、指纹与 Schema

- `status`、`inspect`、`diagnose`、`snap` 发布 `annotations.readOnlyHint=true`。
  其余工具保留现有注解策略。`CatalogSnapshot` 和注册依据相同规则，探针按工具名比较
  live 与源码的 `ReadOnlyHint`，缺标和多标都失败。
- 目录指纹继续覆盖工具数、顺序、名称、描述与入参 Schema。全局指令和只读标记分别通过
  活体比对校验。描述与 Schema 改动后同步指纹基线。
- `inspect.query` 增加 `minLength: 1`，保留 256 码点上限。`wait_for.query` 与
  `wait_for.absent_query` 同样增加 `minLength: 1`，字段说明另写明不能全空白。
  两个等待条件在大小写折叠后相同会被拒绝。运行时继续使用现行严格解码和引擎校验。
  `act.oneOf`、`inspect.not` 和扁平的 `app` Schema 继续使用。

## 5. 算法

第一批在现有适配器合成点使用同一个判据。伪代码如下。

```text
adapterRecovery(code, operationID):
    if operationID is empty:
        return nil
    if code is transport_unavailable or call_cancelled:
        return {entry: status, reference: P}
    return nil
```

`projectCall` 保留已取得的服务恢复，在现有分支检查后才合成候选。
`cancelled` 保留 app 查询、snap 及工作调用的分支和时限，在初始化和兜底处使用同一判据。
已有服务结果按原规则保留。准入错误仍走原有接入恢复路径。

第二批以固定目录定义服务指令、工具说明和字段 Schema。注册和快照用同一只读标记规则，
即无 P 的只读查询或 `snap`。探针先校验工具集合，再对每项比对描述、Schema 与标记。
兼容 `initialize` 和 `server/discover` 的指令通过现有独立检查。

## 6. 实现方案

### 6.1 接口与文件

| 批次 | 文件 | 改动 |
| --- | --- | --- |
| 第一批 | `internal/mcp/server.go` 与现有取消和投影测试 | 在四个合成点传入实际预分配的 P。加入先红回归，并验证已有服务结果和带 P 分支。 |
| 第二批 | `internal/mcp/schema.go`、`schema_test.go`、`server_test.go` | 按 §4 定向组织内容，补三个长度约束。用稳定的字段、枚举与分支校验替换仅绑定标点和旧句式的断言。 |
| 第二批 | `internal/mcp/server.go`、`catalog.go`、`snap_test.go`、`result_parity_test.go` | 同步只读标记与目录快照，测试精确标记集合以及更新后的指纹。 |
| 第二批 | `scripts/mcp-probe/catalog.go`、`catalog_test.go` 和旧计数注释所在文件 | 逐工具校验只读标记，覆盖应标未标和不应标误标。文档性注释统一按实际 13 项表述。 |

### 6.2 配置与取值

沿用现有运行参数。继续用现行 `<36,250 B` 发现字节门槛，报告全局指令、工具数组、描述、
Schema 以及总量，与 28,050 B 基线比较。单独记录 `act`、`app` 整理前后的字节数。
25,500 B 只作为原草案的参考值，验收采用 §4 的语义判据和现行字节门槛。

### 6.3 日志与可观测性

复用适配器和探针现有的白名单日志。用受控工具名、错误码、计数与指纹定位变化。
说明体积只记录汇总值。实机反馈记录使用脱敏事实，原始树、截图和响应留在仓库外。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威与事实分层 | 适配器投影现有事实，忙阻塞、会话状态和设备执行仍由引擎决定。 |
| STOP 与取消 | 取消发起 `end` 的范围和时限沿用现行实现。已派发动作的结果与设备收尾继续分别表达。 |
| O 单向消费与准入 | 静态拒绝保持不认领，坐标重试按新调用准入及重验。 |
| 严格接入、版本与发布 | 静态 Schema 对齐现有拒绝规则。共同版本由单一版本源管理，目录与产物按清单核验。 |

### 6.5 契约与文档影响

第一批同步 [MCP 取消、P 与漏收边界](../../../docs/mcp.md#L402)，写明只读查询的适配器失败
省略合成恢复，以及服务已有事实的保留规则。第二批同步 [MCP 工具发现与调用说明](../../../docs/mcp.md)
及 [集成说明](../../../docs/integration.md#L183)，包括目录指纹、四个只读工具与调整后的说法。
各批改动的正式文档在相应实现提交前分别完成独立只读审计。
[架构文档](../../../docs/architecture.md#L664) 的指纹定义继续适用。

## 7. 预期效果与能力边界

### 7.1 能治什么

无 P 的适配器失败给出可解释的查询结果。重点工具的高频分支和恢复提示便于查找。
支持 MCP 注解的宿主可识别四项只读工具，调用方也能从目录看到三个字段的最短长度约束。

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| 宿主省略全局说明、注解，或重复注入全局说明 | 宿主控制其投影。服务可以提供局部兜底和可靠的原始目录。 |
| `shell` 返回不确定后的有限忙阻塞、`phase` 命名和执行链物理未知 | 都由当前引擎与设备事实决定。本提案覆盖如何理解和处理这些结果。 |
| 模型长期误用率或上下文节省幅度 | 字节是可复验的目录指标，任务表现及分词成本需要按宿主和模型另测。 |

### 7.3 成本

第一批在现有投影中加一次 P 判定，第二批改变启动注册和发现内容。设备请求次数沿用现行
路径。发布者需重建并核验对应清单，更新宿主进程后重新发现。

## 8. 验证计划

### 8.1 单元测试

| 批次 | 用例与验收 |
| --- | --- |
| 第一批 | 把 §1.2 的先红现象固化为回归。覆盖三个只读工具的断连、无服务结果取消，以及 `status` 查询旧 P 时的缺省结果。验证错误码、`operation_id`、`recovery`、取消详情和无 `end`。在 MCP 结果模式中按既有投影验证字段省略。先红再绿，去掉合成守卫应重新失败。 |
| 第一批 | 覆盖已有服务恢复、`snap` 与工作调用的带 P 失败、`app.find/list` 查询取消。验证服务恢复保留、原 `end` 策略及图片丢弃边界。 |
| 第二批 | 真实 `tools/list` 发布精确四项只读标记。探针移除 `status` 的标记或给 `act` 加标时失败，改回正确目录后通过。独立验证指纹与指令。 |
| 第二批 | 用 `tools/list` 校验三个 `minLength` 与相关 Schema 的结构守卫。用已有严格解码或引擎路径核验空值和等待全空白拒绝。 |
| 第二批 | 验证 §4.2 的关键调用语义，覆盖只提供工具描述时需要保住的分流和文本恢复条件。保留防泄漏、图例唯一、只读集合、结果模式与分项字节检查。 |

各批执行受影响 Go 包回归，启用 `GOEXPERIMENT=jsonv2`。第一批覆盖 `./internal/mcp`。
第二批覆盖 `./internal/mcp ./internal/protocol ./scripts/mcp-probe`，并对实质调整的
描述路径运行相应引擎夹具。发布前对 `device-mcp`、`mcp-probe`、`toolcatalog` 做
`linux/arm64`、`CGO_ENABLED=0` 交叉构建。

### 8.2 实机/系统级

1. **提交前的本地验证。** 用临时适配器和回环假服务分别核验 stdio 与 Streamable HTTP
   的目录、指令和代表性无 P 传输失败。包含 `server/discover` 与兼容 `initialize`。
   记录两批各自的检查结果。可加做同宿主配对回放，记录使用的投影和实际选参。
2. **发布时的构建与活体验证。** 以干净的目标提交构建主机工具和手机发布包，先核对清单再按
   运维流程覆盖部署及重启实际宿主。用对应构建的探针分别核对主机 stdio 和手机 HTTP，
   验收 13 工具、四项只读标记、发现指令和目录指纹。实机验证限于部署及实际改变的路径，
   文案效果按实际回放记录。没有执行的宿主或真机检查在记录中注明原因。[部署说明](../../../docs/runbook.md#3-部署与升级)

### 8.3 记录

第一批另写 `maintenance/verification/mcp-no-operation-recovery.md`，记录先红、根因、
同类核查、修复提交和实际命令。第二批实现后按[工程流程 §7](../../process.md#7-归档)归档本
提案，§11 引用第一批记录并以第二批实现提交为归档基准。第二批验证记录使用
`maintenance/verification/mcp-tool-descriptions-governance.md`。各记录注明环境、是否接触
实机、实际命令、结果和未执行项。

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 推进方向 | 用户同意拆分。第一批是独立的无 P 投影缺陷，第二批按本提案定向校准说明和元数据。两批可以同次发布。 |
| 范围 | 全局、`act` 与 `app` 优先，其他说明按具体盲区调整。目录保持 13 项，指纹算法和共同契约沿用现行口径。 |
| 验收口径 | 先按语义验收、再看字节分项。真机部署与在线比对属于发布验证。 |

### 9.2 未决

用户已确认第二批实施。发布验证按 §8.2 执行。

## 10. 实施顺序

1. 核对实施时的 `HEAD`。第一批先固化可执行的先红用例，修复并做同类回归。同一提交带
   验证记录与经独立审计的对应文档，提交信息写明已定位缺陷直接修复及省略专门提案的理由。
2. 第二批可并行准备文案和用例，以第一批实现为提交基线。完成定向说明、注解、Schema、
   探针与指纹更新，运行本地回归和发现校验。
3. 第二批同步对应正式文档，独立审计通过后提交实现。提案状态到实现提交落盘时仍为
   「提案，未实现」。
4. 按 §8.3 单独提交第二批的归档与验证记录。发布时按 §8.2 从实际目标提交构建、核验、
   部署和探测，按实际结果记录发布验证。

## 11. 实现结果

实现提交：2c0852ffa6999e482cd033d393994d313273b3fc。

### 11.1 已落地

| 批次与条目 | 落点 |
| --- | --- |
| 第一批独立修复（`5cc69e7`） | [无 P 恢复投影验证](../../verification/mcp-no-operation-recovery.md)记录先红、四个合成点与取消回归。 |
| 全局说明、工具分流和字段描述 | [`treeTextGuide`、`callerGuidance` 和 `toolCatalog`](../../../internal/mcp/schema.go)。重排 `act`、`app`，校准等待、坐标、位移、受管命令与通知指引。 |
| 只读注解和探针 | [`NewServer`](../../../internal/mcp/server.go)、[`CatalogSnapshot`](../../../internal/mcp/catalog.go)、[`checkLiveCatalog`](../../../scripts/mcp-probe/catalog.go)。精确校验四项注解。 |
| 查询 Schema 与目录指纹 | [`toolCatalog`](../../../internal/mcp/schema.go)声明三个 `minLength: 1`，[目录基线测试](../../../internal/mcp/result_parity_test.go)与[正式文档](../../../docs/mcp.md)同步。 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| 外部宿主配对回放 | 未执行。按 §8.2 为可选验证。本次测到候选 MCP 线协议与手机服务的只读查询，结果见验证记录。 |
| 发布清单、覆盖部署、宿主刷新 | 未执行。§8.2 将这些检查放在发布阶段，当前验证使用临时适配器。 |

## 附：本提案不改变的事项

- 共同 HTTP 契约、版本源、工具集合、结果模式与树协议格式。
- 引擎、设备、控制 App 的工作和会话状态机。
- `act.oneOf`、`inspect.not`、`app` 的扁平入参结构及目录指纹算法。
- `snap`、`app.find/list` 和其他工作调用的既有取消策略。
