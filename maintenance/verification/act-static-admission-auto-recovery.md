# 验证记录：act 静态准入高置信自动恢复与 Issue #52 闭环（契约 37）

实现提交：`e0d92d1`（主实现 `d9ee0c1`，受控夹具调整 `e0d92d1`）。提案入库提交：`7e361f2`。
归档提案：[act-static-admission-auto-recovery](../proposals/implemented/act-static-admission-auto-recovery.md)。
**涉及实机**：是。手机整组覆盖部署、最小实机闭环、Windows 主机双产物替换，以及主机 stdio / 手机 HTTP
独立探针均执行；依赖方 MCP 宿主进程的重启与重连未执行，由用户处理。

结论分层（不得互相替代）：

| 层次 | 结论 |
| --- | --- |
| 已实现 | 提案 §2.1 的 9 项静态自动恢复（轴不兼容内层委托、包装壳下探、缺动作提升、定向文本绑定、被动多命中、2D 残余安全区、同视口子委托、异动作向下委托、最内层消歧）与 §4.5 重验联动；落点见归档提案 §11.1 |
| 已单测 | 静态、重验、空跑候选、日志白名单与旧夹具校准用例；见 §2 |
| 已全量 | `internal/engine` 拆分 6 组（每组 `-timeout 1140s`）全部通过，合计约 1014s；非 engine 全部包通过；交叉编译通过；见 §3 |
| 已实测 | Issue #52 形状（横向分页容器内唯一纵向列表直接 `scroll down`）及包装壳滚动、文本定向绑定、残余安全区、被动重叠、同视口子委托、异动作向下委托共 8 个场景；见 §5.4 |
| 磁盘替换 | 手机整组覆盖部署与 Windows 双产物替换；见 §5.2–§5.3 |
| 独立探针 | 主机 stdio 与手机 HTTP 均 `RESULT PASS`；见 §5.5 |

---

## 1. 环境与构建方式

- 主机：Windows，`go1.27.1 windows/amd64`，所有 Go 命令显式设置 `GOEXPERIMENT=jsonv2`；
  发布交叉编译为 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`。
- 版本源：`release/versions.json` 为 `0.41.0 / contract_version 37 / android_version_code 43`，
  `source_commit=43807f2`；`scripts/generate-version.ps1` 生成 4 个产物，
  `-Check` 输出 `version source consistent: release=0.41.0 contract=37 android_version_code=43`。
- 发布构建在实现提交 `e0d92d1` 的干净树上执行（`source_dirty=false`）。
- 受控漂移使用仓库既有验证目标 `com.mobileuse.probe/.ObservationFixtureActivity`；本次为其新增
  `d05`–`d10` 场景（横向分页 + 纵向列表、非滚动包装壳、包装壳输入与扁平占位输入、残余安全区卡片、
  被动图文重叠卡片、同视口子容器 + 小角标），不接触用户个人数据。

## 2. 单元测试

### 2.1 新增/更新用例

| 区域 | 覆盖 |
| --- | --- |
| `internal/engine/act_static_scroll_recovery_test.go`（新增） | 轴不兼容内层委托（Issue #52：同界正向、左右仍走分页、双同界列表/同窗与跨窗遮罩反向）、同轴三轴失败仍排除同视口内层、非滚动包装壳下探（50% 恰好、差 1 像素、角落小条、遮罩、禁用/无几何、兼容祖先优先、规划失败不触发）、真实首帧 `scrollable=false` 投影的启用与禁用边界 |
| `internal/engine/act_static_tap_recovery_test.go`（新增） | 文本定向绑定（包装壳、Context 剪枝、占位子、外部共位）实际写入与 ref/raw 对齐、失败硬停与可选候选、残余安全区外部遮挡拒绝、同视口小角标与 `bubble=false`、被动多命中与更浅层外部交互拒绝、共位内层消歧 |
| `internal/engine/act_point_disambiguation_test.go`（新增） | 坐标嵌套拥有者最内层归约（tap/long_press/text 写入目标）、平级歧义与 candidates、`point_changed` blocking_refs、内层双输入与平级第三项 |
| `internal/engine/act_static_recovery_revalidation_test.go`（新增） | 低 Tier 被动多命中整树复验、同深外部被动 peer 拒绝、残余点新帧外部覆盖拒绝、同视口父 Tier4/子 Tier3 双证明、异动作委托父禁用拒绝 |
| `act_proposal_scroll_coord_test.go`、`act_tap_scope_test.go`、`tap_static_error_test.go` | 旧保守夹具按新契约校准（缺动作提升正反例、同深外部节点保留拒绝、inspect 可回归），原有守卫保留 |
| `act_revalidation_*_test.go`、`act_rebind_test.go`、`act_recovery_integration_test.go` | 重验 origin/重选被动证明、同视口父/子双绑定与角标外覆盖、F1 重验归约、定向绑定第二次重验恢复到实际输入框、`text_rebind` 双层门禁、写入目标 TextTarget 与零 ENTER |

### 2.2 先红证据

| 用例 | 修正前真实失败 |
| --- | --- |
| 滚动内层遮罩 | 将 `want=nil` 改为传入目标后，A1 两个遮罩用例与 A2 遮罩用例错误 `executed` |
| 滚动同界/面积 | 隔离基线确认新增正向用例在基线全部红（同界 `down`、A2 两条委托、A3 两条提升） |
| 低 Tier 被动证明 | 移除整树 guidance 复验后，六个覆盖反控错误 `executed` |
| 同视口父 Tier4 | 父检查临时提高到 Tier5 后，父 Tier4/子 Tier3 的外部同深 peer 反控错误 `executed` |
| 残余点外部覆盖 | 移除直接残余原点守卫后，新帧外部按钮覆盖反控错误 `executed` |
| 异动作委托父禁用 | 移除静态父禁用门后，submit 规划错误返回计划（写后必败） |
| 定向绑定硬停 | 修复前六个「歧义/受限祖先 + 外部输入」子例均错误 `executed` |
| F1 归约 | 去掉归约后，嵌套输入与平级第三项 candidates 分别列出外层祖先/三项 |
| 同视口小角标 | 去掉多 blocker 外覆盖检查后，Tier 1 小角标覆盖落点反控错误 `executed` |

### 2.3 定向运行（真实结果）

各实现流在合入前以精确 `-run` 验证（均设置 `GOEXPERIMENT=jsonv2`、`-count=1 -timeout 300s`）：
滚动 5 个顶层测试 32.9s；D1/E1/E2/E3/B1/E4 三批 11.8s / 16.6s / 5.2s；F1 两批 9.2s；
重验 8 个顶层测试 2.6s / 3.2s / 3.4s。全部通过，变异项恢复后复跑通过。

## 3. 全量测试与静态检查

| 命令 | 真实结果 |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./internal/engine -run '^TestA' -count=1 -timeout 1140s` | `ok … 299.5s` |
| `go test ./internal/engine -run '^Test[B-G]' -count=1 -timeout 1140s` | `ok … 57.8s` |
| `go test ./internal/engine -run '^Test[H-O]' -count=1 -timeout 1140s` | `ok … 91.7s` |
| `go test ./internal/engine -run '^Test[P-R]' -count=1 -timeout 1140s` | `ok … 140.2s` |
| `go test ./internal/engine -run '^TestS' -count=1 -timeout 1140s` | `ok … 263.2s` |
| `go test ./internal/engine -run '^Test[T-Z]' -count=1 -timeout 1140s` | `ok … 161.9s` |
| 非 engine 全部包 `go test -count=1 -timeout 900s` | 全部 `ok`：`cmd/device-mcp 0.1s`、`internal/android 12.2s`、`client 0.2s`、`device 0.0s`、`eventlog 0.0s`、`httpapi 1.3s`、`imaging 0.5s`、`mcp 0.6s`、`observation 0.8s`、`protocol 0.1s`、`store 1.3s`、`treetext 0.0s`、`scripts/mcp-probe 16.0s`；`cmd/device-service`、`scripts/toolcatalog` 无测试文件 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | exit 0 |
| `scripts/generate-version.ps1 -Check` | `version source consistent: release=0.41.0 contract=37 android_version_code=43` |
| `git diff --check` | exit 0（仅 CRLF 提示） |

## 4. 文档审计

按 `AGENTS.md` 第 15 条由独立、只读 subagent 审计本次涉及的 `README.md` 与
`docs/{interface,architecture,mcp,integration,runbook,release,control-protocol}.md`；最终轮使用
ollama 模型。审计历经多轮 `BLOCKED`：修正项包括残余安全区单命中外部拥有者条件、Tier 4/5 单/多命中
全树证明、冒泡与共位选点、滚动四条 `ref` 路径与错误收口、F1 归约前件、文本定向绑定与诊断键范围、
版本字面量、链接/示例等。全部发现修正后复检为 **AUDIT CLEAN**，无剩余阻断项。

审计同时核对：`contract_version=37`、`0.41.0`、`versionCode=43`、`X-Mobile-Use-Contract: 37`、
`/health` 示例、`release.md` 冻结基线与 `release/versions.json` 一致；无过程性措辞/阶段代号/
提案代号；Markdown 链接与锚点全部可解析；未新增错误码/详情键/枚举；`Result.auto_recovery`
结构不变且静态自动解析不写该字段。

## 5. 发布构建、部署、实机闭环与双端探针

### 5.1 发布构建与清单核验（干净树 `e0d92d1`）

| 命令 | 真实结果 |
| --- | --- |
| `scripts/build-release.ps1` | `release=0.41.0 contract=37 android=43 source_commit=e0d92d1032aefbd2806b0ba9f6c7a3b88511f5dc source_dirty=False`；工具目录 `13/19cb4aa389e227cda892d727a270aa118c57951d0434c282169a819c6526ebae` |
| 产物 SHA-256 | `device-service a72a0f599d3285c22ccea381338ef30f311cac57a255343cb6191615c84ba215`（9765024 B）、`device-mcp c09cf1d946f58506e125a2cbc1d4952fccc6de07c9559e824598746916a5e5df`（9371808 B）、`control.apk f72cd5f332f3275ea4ca3349e1fd5347700ba1f3e321da17812e1302c34c8adc`（103965 B）、`u2.jar 28361193…`（3306229 B）、`applist.dex 63818f32…`（4316 B）、`service.sh 4b568a9a…`（3643 B） |
| `scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS`，`artifacts=6`、`head=e0d92d1`、`source_dirty=False` |
| `scripts/build-host-tools.ps1` | `RESULT PASS`；`device-mcp 8fc7d540bd2b6c08580da0cfe2bf0d84a4bd2d7dcd09d372c1bb92bc6e5f28ac`（10311168 B）、`mcp-probe 681835bc3543df3bce05be61d7ec10c0b0502ad7c6edf72c795f5f2e1b9d0246`（10129408 B）、`source_dirty=False`、`contract=37` |
| `scripts/build-host-tools.ps1 -VerifyOnly` | `PASS`，手机清单为同一提交的配套发布，工具目录 `13/19cb4aa3…` |

### 5.2 Windows 主机产物替换

同一源码提交构建并替换 `build/device-mcp.exe` 与 `build/mcp-probe.exe`；磁盘替换与独立进程探针已完成；
依赖方 MCP 宿主进程重启与工具重新发现由用户处理（部署提示宿主持有旧进程时保留替换并需重启）。

### 5.3 手机整组覆盖部署（保留 App 私有数据）

`scripts/deploy.ps1 -Manifest build/release/manifest.json -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'` 输出：

- `version match: PASS (health release=0.41.0 contract=37)`；
- `installed App: package=com.mobileuse.vnext versionName=0.41.0 versionCode=43`（`adb install -r`，未卸载/未清库）；
- `App pairing: PASS`、`recovery barrier: PASS`、`STOP availability: PASS`；
- 所有 `deployed artifact` 逐项 `(match)`；`device-service` 在 chroot 内启动（pid 27024）；
- `phone MCP HTTP adapter: PASS listen=127.0.0.1:18882 url=http://127.0.0.1:18883/mcp sha256=c09cf1d9…`。

### 5.4 最小实机闭环（虚拟屏 `display_id=330`）

使用新 `0.41.0/37` HTTP 服务与 `com.mobileuse.probe` 受控场景；每步核对返回结果与场景内计数器：

| 场景 | 真实结果 |
| --- | --- |
| Issue #52：横向分页容器内唯一纵向列表（`d05`） | `scroll(container=外层分页, direction=down)` → `P1511_4` `executed`；外层 `axis=x`、请求 `down`，执行即证明委托给内层列表 |
| 非滚动包装壳滚动（`d06`） | `scroll(container=包装壳, down)` → `P1511_8` `executed`；包装壳为 `role=view` 非滚动，证明下探到内层滚动容器 |
| 文本定向绑定：包装壳（`d07`） | `text(ref=包装壳, auto_submit=false)` → `P1511_12` `executed`；回读输入框值 `wrapped-value` |
| 文本定向绑定：扁平占位标签共位吸附（`d07`） | `text(ref=占位标签)` → `P1511_16` `executed`；回读输入框值 `flat-value` |
| 2D 残余安全区（`d08`） | `tap(ref=卡片)` → `P1511_20` `executed`；场景状态 `cardTaps=1 blockerTaps=0`，落点避开中心阻塞按钮 |
| 被动图文重叠（`d09`） | `tap(ref=卡片)` → `P1511_24` `executed`；场景状态 `cardTaps=1` |
| 同视口子委托（`d10`） | `tap(ref=外层卡片)` → `P1511_28` `executed`；状态 `parentTaps=0 childTaps=1 badgeTaps=0`，委托给同视口子容器且未误触角标 |
| 异动作向下委托（`d10`） | `long_press(ref=外层卡片)` → `P1511_32` `executed`；状态 `parentTaps=0 childLongTaps=1` |

会话收尾 `end(P1511_34)` `executed`，会话结束。

实机边界：`F1`（坐标嵌套拥有者）与 `A3`（线格式缺动作分支）未在真机构造；`A3` 的正常投影形状由
真实首帧单测证明，`F1` 由单测覆盖。D1 的占位子向上绑定（`d07` 包装壳/共位之外的第三条）未单独
在实机构造；半屏/受保护/禁用/大屏反控均由单测覆盖。

### 5.5 双端独立探针（新产物）

| 探针 | 结果 |
| --- | --- |
| `build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/mcp-config.json -result-mode text` | `RESULT PASS`：13 工具 schema/只读注解、status、observe/snap/wait_for/shell/inspect、`act(key=BACK)`、`end`、取消投影全部 PASS |
| `build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode text` | `RESULT PASS`：同一套件全部 PASS（手机 HTTP MCP，`contract=37`，工具目录指纹 `19cb4aa3…`） |

依赖方边界：本次只完成磁盘替换、手机适配器启动与独立进程探针；托管 MCP 宿主进程未重启，
工具重新发现与重连由用户处理。

## 6. Issue #52

实现与归档提交推送后，通过 `gh issue comment 52` 回复落地内容（`0.41.0` / 契约 37、Issue #52 形状
直接 `scroll down` 一次成功、其余 8 项静态恢复与重验联动、单测/全量/实机/探针结果），并
`gh issue close 52`。结果补记于后续提交。

## 7. 未执行与缺口

- 依赖方 MCP 宿主进程的重启与重连未执行（由用户处理）；记录只主张磁盘替换 + 独立进程探针。
- `A3`（缺 `swipe` 的 `role=scroll`）在正常投影不可达，保留为线格式/合成树防御分支；真实
  `scrollable=false` 列表由普通祖先提升覆盖，并由真实首帧单测证明。
- `F1` 坐标嵌套拥有者、D1 占位子向上绑定、半屏/受保护/禁用/大屏反控未在实机构造，只有单测覆盖。
- 静态恢复的 dry-run `executable` 与 `candidates` 由单测覆盖；实机未逐一构造候选场景。
