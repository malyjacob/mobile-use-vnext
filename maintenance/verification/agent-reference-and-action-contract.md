# 验证记录：统一 Agent 请求引用与受控动作契约（第二批）

状态：2026-11-17。实现提交：`d529ee7a7284a5b78d054cf36b8d6762b06efe50`；最终联合实现与版本冻结：
`792c973`（`0.16.0` / 契约 `16` / versionCode `18`）。
提案：[agent-reference-and-action-contract.md](../proposals/implemented/agent-reference-and-action-contract.md)。
本记录覆盖 #17/#18/#19/#23 的实现、单测、反向对照与实机/宿主验证；§8 记录 0.16.0 联合
验收补记，前三节的“未冻结/未归档”表述保留为当时状态。

## 1. 范围与结论

| 项 | 结论 |
| --- | --- |
| #17 统一 `reference` | 已实现、已单测、已实测：`act`/`inspect`/`app` 请求字段统一为 `reference`；旧 `observation_id` 按未知成员拒绝；`act`/`inspect` 仍是 O-only，`app.list` 保留 O/P 语义。 |
| #18 扁平 `app` 与 `open.target.app` | 已实现、已单测、已实测：12 工具与单 `app` 不变；app 根与 `target` 为扁平封闭对象，无组合器；`open` 只接受 `target.app`，顶层 `app` 仅保留给 `list` 过滤；七个 kind 的服务端成员矩阵不变。 |
| #19 `operation.bubble` | 已实现、已单测、已实测：ref tap/long_press 省略/true 有效为 true；原目标完全没有 `tap`/`long_press`/`text` 动作时才做一次最近交互祖先提升；显式 false 保持直接目标错误；坐标目标和其他动作携带即拒绝；认领后 child+A 双绑定、Role/完整 Label、最近祖先与安全点重验。 |
| #23 `operation.press_enter` 与 `text_input` | 已实现、已单测、已实测：text 省略/true 时自动聚焦、全文写入、精确读回后至多一次 ENTER；显式 false 只输入、零 ENTER、无 `text_input`；首次结果、运行中 `status(P)`、重复 P、冻结结果共用同一有界快照；焦点 tap 计入整体 outcome。 |
| 公共说明与探针 | 已实现并单测：instructions/12 描述面向 caller-agent，说明统一 `reference`、默认 bubble/press_enter、显式 false 场景、`text_input` 与恢复下一步；探针检查扁平 app、branch boolean 无通用 default、新指纹与 typed/text 投影。 |
| 正式文档 | 已同步并通过独立只读文档审计 **CLEAN**（两轮：首轮发现 5 项措辞/边界问题，修正后复检；末轮删除重复句后再次 CLEAN）。 |
| 版本与发布 | 本节描述实现时状态：当时未改 `release/versions.json`（仍 `0.15.0`/contract `15`/versionCode `17`），未冻结、未发布、未归档。最终在 `792c973` 一次冻结 `0.16.0`/契约 `16`/versionCode `18` 并整组发布，见 §8。 |

## 2. 环境与构建

- Windows 10.0.19044 x64；Go `go1.27.1 windows/amd64`；`GOEXPERIMENT=jsonv2`。
- 设备：ADB `17efb04a`（Android 16，arm64）；已有 Debian 13 chroot、
  `com.mobileuse.vnext` 与 `com.mobileuse.probe`；服务部署目录
  `/data/local/chroots/debian13/opt/mobile-use-vnext`。
- 本批构建：`GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`
  → `CROSS_DONE`；`GOEXPERIMENT=jsonv2 go vet ./...` → 无输出（通过）。
- 主机二进制由本提交源码构建（未入库）：
  `.state/b-session/candidate/device-mcp.exe`、`.state/b-session/candidate/mcp-probe.exe`；
  设备服务由同一源码交叉构建并覆盖到 chroot。该部署是验证用候选，不是发布清单产物。
- 设备服务 `/health`：`release_version=0.15.0`、`contract_version=15`。

## 3. 实际执行的命令与结果

### 3.1 单测与回归

| 命令 | 结果 |
| --- | --- |
| `GOEXPERIMENT=jsonv2 go test ./internal/protocol/... ./internal/client/... ./internal/httpapi/... -count=1` | 全部 `ok` |
| `GOEXPERIMENT=jsonv2 go test ./internal/mcp/... ./scripts/mcp-probe/... -count=1` | `ok internal/mcp 1.737s`、`ok scripts/mcp-probe 8.511s` |
| `GOEXPERIMENT=jsonv2 go test ./cmd/device-mcp/... ./internal/protocol/... ./internal/client/... ./internal/httpapi/... ./internal/mcp/... ./scripts/mcp-probe/... -count=1` | 全部 `ok`（`cmd/device-mcp 0.990s`、protocol `0.046s`、client `0.183s`、httpapi `0.052s`、mcp `0.429s`、probe `8.953s`） |
| `GOEXPERIMENT=jsonv2 go test ./internal/engine -count=1` | `ok ... 500.418s`（gofmt 后最终全量重跑） |
| `GOEXPERIMENT=jsonv2 go vet ./...` | 无输出（通过） |
| `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 无输出（通过） |
| `gofmt -l`（全部变更 Go 文件） | 无输出（已格式化） |
| `git diff --check` | 无空白错误 |

新增测试覆盖：

- `internal/protocol/action_contract_test.go`：reference 新旧字段、旧别名拒绝、bubble/press_enter
  presence 矩阵、显式 false 编码保真、坐标/他动作拒绝、app `open.target.app`、`text_input`
  marshal/decode 五组合法组合与 null/缺键/未知值/非法组合拒绝。
- `internal/engine/action_contract_test.go`：bubble 默认提升、直接目标不提升、显式 false 422 且
  O ready、child 丢失/label 改变/child 新增动作的 fresh 拒绝；text 默认 E/E、显式 false 零 ENTER、
  读回不精确零 ENTER、SetText 未派发、ENTER 未派发、ENTER unknown 时运行中 status、STOP 在
  写入后冻结、STOP 在首个输入前保留 `(N,N)`、重复 P 与直接 engine 入口共享 boolean 矩阵。
- `internal/mcp/action_contract_test.go`：MCP 入口拒绝旧字段、坐标/他动作 boolean、null 与旧名；
  接受显式 false bubble/press_enter。
- 既有 engine 测试中旧的 ref tap/long_press 请求显式固定 `bubble=false`，保留原 guidance
  反例语义；默认 bubble 由新测试覆盖。

### 3.2 反向对照（均为先红后恢复）

| 临时改动 | 期望失败用例 | 实际结果 |
| --- | --- | --- |
| `EffectiveBubble` 强制返回 false | `TestActBubbleDefaultsToOneInteractiveAncestor` | 失败：默认 ref tap 返回 `unsupported_node_action`，零提升。恢复后通过。 |
| `EffectivePressEnter` 强制返回 false | `TestActTextDefaultIncludesOneEnter` | 失败：默认 text 结果无 `text_input`、零 ENTER。恢复后通过。 |
| 用 raw presence（`HasPressEnter && PressEnter`）替代 effective | 同上 | 失败：默认 text 不初始化阶段，结果 `uncertain`。恢复后通过。 |
| 去掉精确 readback 相等门禁 | `TestActTextReadbackMismatchNeverSendsEnter` | 失败：读回不相等仍判 `executed` 并发送 ENTER。恢复后通过。 |
| fresh 重验改用祖先作为 child 并跳过 child 内容/交互性校验 | `TestActBubbleFreshFrameRequiresChildAndAncestor/child_lost...` | 失败：child 丢失、A 仍唯一时被误判 `executed` 并派发。恢复后通过。 |

## 4. 实机/宿主验证

### 4.1 第一阶段出口补测

在实现第二份提案前，先用 A 阶段代码（`07c85b6`）构建并部署 device-service 到设备，补跑：

```text
stdio -result-mode text         RESULT PASS
http  -result-mode text         RESULT PASS
stdio -result-mode structured   RESULT PASS
```

三次均 PASS：默认 text、12 工具目录、instructions 4967 字节、指纹
`312b6975e7a4706241c5a17c0cfcb0336330799e29286f98a4619a9f219c40fd`、start/observe/wait_for/
shell/inspect/act/end/cancel 与图片链路。该补测关闭了上一记录中“真实服务会话未执行”的缺口；
慢 hierarchy 的 wait Capture 次数/p95、STOP/end 迟到 O 与 #20 真实模型任务回放仍未执行，
因此第一阶段系统级出口仍不能写成全部通过；#20 结论继续保留 `12/70/180s`。

### 4.2 第二份提案 live 入口

用本提交源码构建的候选服务与主机适配器：

```text
mcp-probe -transport stdio -result-mode text                         RESULT PASS
mcp-probe -transport http  -result-mode text                         RESULT PASS
mcp-probe -transport stdio -result-mode text -suite app-lifecycle -app-target com.mobileuse.probe
                                                                     RESULT PASS
```

最终指纹与说明：

```text
instructions bytes=5958 source_match=true
catalog_fingerprint sha256=c5fdc545cc77882a4662b6a4524517475065cfc3248ec42223ae4c1189529928 tools=12
schema required_act=2 required_app=1 additional_properties_false=12
```

`app-lifecycle` 实测：

- `app_open` 用 `open.target.app` 打开 `com.mobileuse.probe`，返回 owned task/display；
- `app_list` 用 `reference` 查询到同一 controlled task；
- `app_kill` 对前台目标返回 `kill_refused_foreground`/`not_executed`；
- `app_close` 返回 `executed` 且逐项 tasks 结果完整；
- `app_claim` 因受控主屏任务不存在而 SKIP（原因记录，未冒充通过）；
- `end` 后会话 ended、cleanup pending，取消检查通过。

### 4.3 实机 request/action 语义

在同一设备与候选服务上通过真实 HTTP 调用验证：

| 场景 | 结果 |
| --- | --- |
| `open` 使用 `target.app` | `executed`，复用同一 probe task（`reused=true`/`state_preserved=true`），无第二份任务。 |
| `open` 使用旧顶层 `app` | `422 invalid_request`，`details.field=app`，O 未被消费。 |
| `act` 使用旧 `observation_id` | `422 invalid_request`（未知成员）。 |
| 坐标 tap 携带 `bubble=true` | `422 invalid_request`（bubble 只适用于 ref 目标）。 |
| ref tap `bubble=false` 指向无动作文本 child | `422 unsupported_node_action`；details 给出 `nearest_actionable_ref`、`safe_point`、`safe_carrier_ref`；O 保持 ready。 |
| 同一 child 省略 `bubble` | `executed`；原 child 为 Settings 的 WLAN 文本，提升到最近可点击行，后观察出现 WLAN 页面标题；单次动作、无逐层点击。 |
| text 省略 `press_enter` | `executed`，`text_input={write:executed,enter:executed}`；后观察输入框为全文、焦点随后移到按钮，与 ENTER 派发一致。 |
| text 显式 `press_enter=false` | `executed`，`text_input` 缺失；输入框全文为 draft 且仍保持焦点，证明未发送 ENTER。 |
| 同一 P 重放已完成的 text | 第二次返回 `operation_state=finished` 与相同的 `{write:executed,enter:executed}`，无第二次派发。 |
| `status(P)` 查询已完成的 text | 返回相同 `text_input={write:executed,enter:executed}`。 |
| 最终候选脚本 `bubble=false` → 默认 bubble | `bubble=false` 返回 `unsupported_node_action`；同一 O 上省略 bubble 返回 `executed`，后观察进入搜索结果页。 |

说明：实机 `bubble=false` 的 `safe_point` 数值只作为本次错误 details 证据记录；不把坐标、ref
或页面正文写入日志/账本。上述会话均在验证后 `end`。

## 5. 第一阶段出口与第二份提案的边界

- 第一阶段代码/单测/文档审计/真实 stdio、HTTP、structured 入口：**已执行**；慢 hierarchy
  wait 与真实宿主/模型 #20 回放：**未执行**。
- 用户已确认 #20 保留现值可以通过，并明确要求本会话实施第二份提案；本记录不把第一阶段
  写成“全部系统验证通过”，也不把第二份提案的 host probe 冒充 #20 证据。
- 第二份提案的 #17/#18/#19/#23 代码、单测、反向对照、实机/宿主验证与文档审计：**已执行**。
- 未冻结版本、未生成正式 manifest、未发布、未归档提案；`release/versions.json` 仍为
  `0.15.0`/contract `15`/versionCode `17`。

## 6. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 实机在 SetText 后、ENTER 持久边界前后触发 STOP/end | 需要精确注入窗口；当前设备/部署无故障注入面。该路径由 `TestActTextStopAfterWrite...`、`TestActTextStopBeforeFirstInput...` 与冻结/重复 P 单测覆盖，未冒充实机结论。 |
| 实机 ENTER `not_dispatched`/`unknown` 回执与 display generation 变化 | 无受控设备回执注入面；合成测试覆盖 `enter=not_executed`、`enter=uncertain` 与显示代次门禁。 |
| `app.claim`/`handoff` 实机成功路径 | probe app-lifecycle 的 `claim` 因受控主屏任务不存在而 SKIP；未构造第二任务。`open/list/kill/close` 已实测。 |
| 真实宿主/模型按 Instructions 的工具轨迹回放 | 未运行同一模型宿主的 A/B 工具轨迹；instructions 只做了字节/语义与 live discovery 一致性检查，未冒充模型行为结论。 |
| #20 任务回放、Token、误用率、锚点距离 | 未取得同宿主/模型/上下文保留策略材料；继续保留 `12/70/180s`。 |
| 慢 hierarchy 设备的 wait Capture 次数、p95、STOP/end 迟到 O | 没有可控慢 dump 环境。 |
| 正式发布 manifest、目录指纹 live 比对与部署 | 本会话不冻结版本；`build/` 下旧清单不可用于本提交。 |
| 提案归档 | 用户明确要求不提前归档；第三会话在唯一发布与最终验证后处理。 |

## 7. 第三会话交接

- 分支/提交：`zcode/agent-tool-evolution`，实现提交 `d529ee7a7284a5b78d054cf36b8d6762b06efe50`；
  文档审计 CLEAN。
- 版本源：`release/versions.json`、`docs/release.md`、manifest 均未改；契约仍为 15。
- 待做：
  1. 按唯一发布门禁一次冻结 `release/versions.json`（共同 `contract_version=16`、新发行号、
     Android versionCode），运行生成脚本并同步最终文档；任何 `docs/` 变化重新走独立只读审计。
  2. 从干净已提交源码构建手机/主机产物，生成并核验 `build/release/manifest.json` 与
     host-tools 清单，核对新目录指纹 `c5fdc545...`（若第三会话再改 schema/描述，必须重新生成）。
  3. 归档两份提案：实现提交写 `d529ee7`（第二份）与 `07c85b6`（第一份），补 §11、验证记录
     与 `maintenance/status.md`；文件名不变并重写相对链接。
  4. 视环境补跑：慢 hierarchy wait、STOP/end 迟到 O、真实宿主/模型 Instructions 回放、
     text ENTER 回执注入；这些不再阻塞代码实现，但未执行前不得写成已实测。
- 已知工作区状态：实现提交后仅剩本验证记录待提交；`.state/` 下临时二进制、日志与部署材料
  未入库，也不是发布证据。

## 8. 0.16.0 联合验收补记（2026-11-17）

本节记录第三会话的最终联合验收，补充而不是改写前七节的历史状态。

### 8.1 实现与版本

- 最终联合实现提交：`792c973fc70399e0a063fdd13b2b1c2a4056950d`，在 `d529ee7` 的第二批实现
  之上补齐联调缺口并完成唯一版本冻结。
- `release/versions.json` 冻结为 `0.16.0`/`16`/`18`；生成常量 `-Check` 通过。
- 本批范围内新增/修正：
  - `AppRequest.Encode` 在编码前执行与服务端 `DecodeApp` 相同的封闭 kind 成员矩阵，拒绝
    跨 kind 成员、错配 target、非法 reference/包名、未声明 presence 和非适用布尔选项，
    不再静默丢弃字段；显式 `false` 仍保真输出。
  - bubble fresh 重验对层级损坏保留 `503 hierarchy_unavailable`，与 direct ref 路径一致。
  - 清理 `tapPlan` 只写不读的祖先 ref 字段；修正 parity 中不可能的 `executed` + `(E,N)`。
  - manifest/probe 覆盖 `text_input` 两种新增阶段与三编码投影（详见第一份记录 §8.2）。
  - `docs/interface.md` 澄清：`status(O)` 观察到会话已撤销时也可对同一 O 调用 `end`，由该 O
    所属会话执行收尾；不声称 status 会发布 `entry=end` 恢复项。

### 8.2 已单测

| 命令 | 结果 |
| --- | --- |
| `GOEXPERIMENT=jsonv2 go test ./internal/engine -count=1` | `ok 505.245s` |
| `GOEXPERIMENT=jsonv2 go test ./cmd/device-mcp/... ./internal/protocol/... ./internal/client/... ./internal/httpapi/... ./internal/mcp/... ./scripts/mcp-probe/... -count=1` | 全部 `ok` |
| `GOEXPERIMENT=jsonv2 go test ./internal/android/... ./internal/device/... ./internal/imaging/... ./internal/observation/... ./internal/store/... -count=1` | 全部 `ok` |
| `GOEXPERIMENT=jsonv2 go vet ./...` | 无输出（通过） |
| `GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 无输出（通过） |

新增/加强的确定性断言：

- `TestAppRejectsUnencodableBranchesWithoutSending` 新增 find-with-reference、list-with-target、
  open-with-top-level-app、claim-with-close-option、ambiguous-target、invalid-reference、
  invalid-filter-package、query-without-presence；临时去掉 `validateAppRequest` 后 9 个用例
  全部变红并触达服务，恢复后转绿。
- `TestActBubbleFreshFrameRequiresChildAndAncestor` 新增 child 角色变化、祖先丢失、祖先失去
  动作、最近祖先变化、祖先歧义、安全点被 sibling 阻断；均 `target_changed`、零派发。
- text 默认/显式 false、`text_input` 五组合、status/重复 P/冻结快照、readback 不精确、
  ENTER 未派发/未知、STOP 前后窗口等既有反向对照保持通过。

### 8.3 已实测（系统级）

同一 0.16.0 部署（服务 `0.16.0/16`，App `0.16.0/18`）上，通过真实 HTTP 调用
`com.mobileuse.probe` 与 Settings 受控页面，实际结果：

| 场景 | 结果 |
| --- | --- |
| `start` → `app.open` probe → `observe(full)` | 会话 active，找到 input ref。 |
| text 省略 `press_enter` | `executed`，`text_input={write:executed,enter:executed}`。 |
| `status(P)` 查询该 text | `operation_state=finished`、`outcome=executed`、同一 `text_input`。 |
| 同一 P 重放该 text | 返回同一 `finished` 视图与 `{executed,executed}`，无第二次派发。 |
| text 显式 `press_enter=false` | `executed`，首次结果与 `status(P)` 均无 `text_input`。 |
| act 携带旧 `observation_id` | `422 invalid_request`，未生成 operation_id。 |
| 坐标 tap 携带 `bubble=true` | `422 invalid_request`，details 明确坐标目标不接受 bubble。 |
| `app.open` 使用旧顶层 `app` | `422 invalid_request`，details.field=app。 |
| ref tap `bubble=false` 指向无动作文本 child | `422 unsupported_node_action`；details 给出 nearest_actionable_ref、safe_point、safe_carrier_ref。 |
| 同一 child 省略 `bubble` | `executed`；后观察标题为 Settings“搜索记录”，证明一次一跳提升生效。 |
| `end` | `executed`；随后 `status()` 为 `can_start=true`、session ended、cleanup 全部 confirmed。 |

补充：

- `mcp-probe -suite app-lifecycle` PASS：`app_open/list/kill/close` 实测，`app_claim` 因受控
  主屏任务不存在 SKIP，未冒充通过。
- 手机 stdio 与 HTTP 两个适配器在部署后逐一复核 `/proc/<pid>/exe`，均为 release SHA-256
  `20c82ded...`；旧 stdio 适配器映像（0.15.0 `c1f9eca...`）在部署窗口被宿主拉起，停止后
  由宿主自动重启为新映像。
- 主机 stdio text/structured/both、主机 HTTP、手机 authenticated initialize 的系统级结果见
  第一份记录 §8.4；instructions 与源码逐字节一致，12 工具目录与两个清单指纹一致。

### 8.4 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 实机在 SetText 后、ENTER 持久边界前后触发 STOP/end | 当前部署无故障注入面；由 `TestActTextStopAfterWrite...`、`TestActTextStopBeforeFirstInput...` 与冻结/重复 P 单测覆盖，不冒充实机结论。 |
| 实机 ENTER `not_dispatched`/`unknown` 回执与 display generation 变化 | 无受控回执注入面；合成测试覆盖 `enter=not_executed`、`enter=uncertain` 与显示代次门禁。 |
| `app.claim`/`handoff` 实机成功路径 | 探针的 claim 因受控主屏任务不存在 SKIP；未构造第二任务。 |
| 真实宿主/模型按 Instructions 的工具轨迹回放 | 未运行同一模型宿主的 A/B 轨迹；只验证 live discovery、字节语义与实际工具结果。 |
| Codex 宿主工具调用 | 用户明确无需管理 Codex；主机侧 `build/device-mcp.exe` 已重建为 0.16.0 并 `-VerifyOnly` PASS，后续由用户自行触发。 |

### 8.5 归档后的清单重建

归档提交 `685d8dc` 只移动 maintenance 文档，不改变 Go/Java/XML 源码；它改变 HEAD 后，
`source_commit=792c973` 的清单不再满足 verifier 的精确 HEAD 门禁。按发布要求从 `685d8dc`
重建 release 与 host-tools 清单并重新部署；Go/固定制品与目录指纹不变，`control.apk` 重签
hash 变化。重建后的 stdio text smoke、app-lifecycle 与真实 text/status/重复 P/bubble/
严格拒绝/end 流程再次 PASS。本记录提交后最终清单从记录所在提交重建，`source_commit` 以
build 清单为准。
