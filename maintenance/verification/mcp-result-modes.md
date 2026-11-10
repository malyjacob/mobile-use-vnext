# MCP 结果模式实现与验证

状态：已实现、已单测、已实测（主机 stdio 与手机 HTTP）；外部模型宿主及其截断未验证。
实现提交：`09d6b5306cc8c688d37784a78f0088b63c64395f`。
提案：[MCP 结果模式与文本/结构化信息等价](../proposals/implemented/mcp-result-modes.md)。

## 1. 分诊、归因与范围

本次是用户于 2026-11-10 明确批准的既定取舍 R4 落地；提案已有 R1–R7 路线比较，未跳过讨论或提案。基线为 `c799391d2ace5066bb76325601a6585bb87c002f`，提案证据基线为 `4cb11dbcdf319ea445c9be3a97e3b34deff32664`。代码核查确认旧 `server.go:99–114` 的 admission 特判与摘要投影、`render.go` 的 shell/apps/timeline 缺口、`tree_text.go:12–21` 的 24/32 KiB 上限仍存在。

`TestBothModeParity` 在旧入口实际先红：文本缺少结构化结果已有的 `shell.output`。树块子代理在旧实现上运行的大块/非法元数据用例也先红。实现统一 canonical A、结果模式、无省略文本及取消事实，不修改引擎、HTTP 服务契约、工具目录/描述/schema、契约号或发行版本源。

自检/独立代码审查还发现并先红复现：

- `server.go` 原 `invoke` 的两个 select 分支在取消与服务结果同时到达时绕过 `cancelled`，漏掉 `end` 与取消事实。`TestCancellationWithSimultaneousServiceResult` 对工作/只读各执行 20 次；统一取消路径后保留 executed、工作调用仅 end 一次、只读不 end。同类核查覆盖两个 select 分支及三种投影。
- 树裸 token 的非 ASCII 元数据与 delta.replace 的 map key/node.Ref 不一致无法无损表达；`TestTextCodecUnrepresentableTreeMetadata` 先红后绿，同类校验包括 role/actions/axis/scope/base/ref，异常整体 JSON 兜底。
- JCS 默认将数字视为 float64，typed round-trip guard 曾把 9007199254740992/9007199254740993 视为相等；`TestResultJSONComparisonPreservesLargeIntegers` 先红后绿，禁止比较阶段改写数字。
- 测试解码器曾把字符串中的 ` v1 ` 误认成块；对应字符串 fixture 先红后绿，改用五种精确块前缀。这是 oracle 修正，非服务事实变更。

## 2. 环境与构建

- 时间：2026-11-10；宿主 Windows/amd64，Go `1.27.1`，`GOEXPERIMENT=jsonv2`。
- 手机：Xiaomi `24117RK2CC`（ADB `17efb04a`），手机 chroot 为 linux/arm64。
- MCP SDK `v1.8.0`，协议 `2026-07-28`；服务转发 `18881→18880`，手机 MCP `18883→18882`。
- 按提案 §6.1 不立即前进版本源；使用开发验证包 `release=0.4.2`、`contract=3`、Android versionCode `5`，不是新版本发行。清单如实记录基线提交和 `source_dirty=true`。

实际命令（PowerShell 设置环境变量后运行）：

```text
GOEXPERIMENT=jsonv2 go test ./... -count=1
GOEXPERIMENT=jsonv2 go test ./internal/mcp ./cmd/device-mcp ./scripts/mcp-probe -count=1
GOEXPERIMENT=jsonv2 go vet ./internal/mcp ./cmd/device-mcp ./scripts/mcp-probe
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
./scripts/build-release.ps1 -Output build/result-modes-release
./scripts/verify-manifest.ps1 -Manifest build/result-modes-release/manifest.json
./scripts/deploy.ps1 -SkipBuild -Manifest build/result-modes-release/manifest.json
```

全量 Go 测试通过（engine 143.605s）；其后审查修正仅涉及 MCP 与测试 oracle，相关三包重测通过，最终 MCP 重测通过；vet、交叉构建均退出 0。完整发布组装/清单核验与保留 App 数据的覆盖部署均通过，6 个产物均由清单核验。最终手机 HTTP 进程的 exe SHA-256 与清单匹配：`1dfbf45498fd137a9e3292d19a09859936730acc9d3b58d92554a5e93c966162`。工具目录仍为 12，指纹 `a42fc3247361d1dadadce49a28214053947cd8ecacc967f81ec756a81d7a325b`。

## 3. 已单测的证明窗口

- 三模式全工具/多分支 fixture 比较 `(A,IsError,ImageContent)`；文本由独立测试 decoder 还原，JSON 数字保留整数精度；null/缺失/false/0/空串/空数组独立比较。
- 唯一 resultFields 分类表及显式 composite inventory 结合反射/fixture；新增字段未审阅或手工 marshaler 漏字段均失败。独立审查通过临时 Node.Future 字段模拟验证门禁稳定变红，临时文件未入库。
- 确定性覆盖 Error.Details、Delta.set/children/replace 与 Diagnosis.TimingMS；未知 composite、完整 item/event、转义、块交错、JSON Pointer 与尾部检测通过。
- 取消覆盖工作/只读 × 有服务结果无错误/无服务结果/已有服务错误；保留原错误、执行与收尾事实，注入 mcp.* 且所有模式 IsError=true；API 原 Result 不被注入。
- HTTP Header.Values 拒绝空、多值、逗号、大小写变体、应用层前后空白；从 Extra.Header 逐请求选模式，无头继承进程默认；服务不接收此模式头。HTTP 框架本身剥除的线上 OWS 不作为应用层原始值验证证据。
- flag.Visit 区分显式空；flag > env > both；非法启动退出 2；mcp.start/mcp.tool 只记录 result_mode 枚举及既有白名单字段。
- structured 成功 content=[]；错误 diagnostic 含解释字段；无 outputSchema；目录指纹固定基线。

接近服务上限的独立文本往返 fixture（字节）：

| fixture | canonical | 完整文本 | 双份合计 |
| --- | ---: | ---: | ---: |
| observation full（128 KiB 附近） | 130221 | 130224 | 260445 |
| inspect 全树（128 KiB 附近） | 130274 | 130245 | 260519 |
| inspect subtree（32 KiB 附近） | 31908 | 31952 | 63860 |
| inspect query（32 KiB 附近） | 31958 | 31994 | 63952 |
| shell output=64 KiB | 65614 | 65668 | 131282 |
| apps name=32 KiB（含对象开销） | 32862 | 32901 | 65763 |

另有超旧上限的多节点完整块：观察树 145800、inspect 全树 145863、subtree 58411、query 60058 字节，均可读并完整往返。这些是合成测试，不是设备或外部宿主上限实测。

## 4. 反向对照

临时逐项修改实现，运行下列 `go test ./internal/mcp -run <pattern> -count=1`，检查实际断言失败而非编译失败，每项 finally 恢复原字节。私有脚本 `python .state/verify-result-mode-mutations.py` 运行后 6/6 被检出；原始结果只在 gitignored .state。

| 临时删除/改变 | 检出用例 | 结果 |
| --- | --- | --- |
| shell output 文本 chunk | TestBothModeParity / TestAgentResultLosslessParity | FAIL |
| canonical apps 字段 | TestCanonicalJSONCoversManifest | FAIL |
| end result 尾部 | TestAgentResultLosslessParity / TestTextCodecGolden | FAIL |
| admission 绕过统一 codec | TestAdmissionParity | FAIL |
| 取消 IsError override | TestReadOnlyCancelWithoutError / TestCancellationDetailsParity | FAIL |
| 仅结构化一侧删除 shell.output | TestAgentResultLosslessParity | FAIL |

## 5. 已实测：主机 stdio

```text
./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe -transport stdio -result-mode both -timeout 180
./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe -transport stdio -result-mode text -timeout 180
./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe -transport stdio -result-mode structured -timeout 180
```

三种模式均有完整 `RESULT PASS`：目录/schema、status、错误投影、start、observe 图片、apps 条目、shell 输出、timeline、inspect、act 图片、end、取消并恢复 can_start。图片为 JPEG 540×1200，样本 10793 字节。text 首次尝试在 shell 收到服务端 `shell_result_unconfirmed`，该次并非通过；用 end 清理后重跑通过。未修改引擎或以此推断根因；保留为设备执行证据的一次失败，后续手机三模式 shell 均成功。

另以真实子进程分别传显式空/非法 flag 与空/非法 env，均退出 2 且 stdout=0 字节；优先级成功分支由 main 的入口单测覆盖。

## 6. 已实测：手机 HTTP

私有 SDK 验证客户端：`GOEXPERIMENT=jsonv2 go run ./.state/result-mode-phone-check`，只打印模式、工具名、字节数和图片数量；凭据读取 .state，不打印、不入库。顺序为 status/start/apps/open_app/keepalive/observe/inspect/act/shell/notify/diagnose/end；每模式调用 12 工具，每个新 session 在 finally 尝试 end，不通过重放动作来比较通道。

三模式共 36 次工具调用全部通过：`RESULT PASS phone_all_modes_all_12_tools`。both/text 的 apps 名称/包名、shell 输出、timeline、完整树与终止行可见；observe/act 有一个 ImageContent；structured 的成功结果 text_bytes=0 且无图时 content=[]。示例观测：open_app 文本 9728、observe 9789、inspect 9377、shell 202 字节。SDK 接受 structured 空 content。

手机原始 HTTP 检查：空/非法/大小写变体/逗号/两个同名 header 行均 400；无头返回 both。应用层空白严格性由 handler 直测覆盖，未把 net/http 的标准头归一化误记为线上拒绝。

`GOEXPERIMENT=jsonv2 go run ./.state/result-mode-phone-cancel`：三个模式分别取消 wait_ms=8000 的 observe，均返回 context cancellation；经 status 确认 session=none、can_start=true。最终修正后的二进制重部署后再次运行同一取消命令，三种模式再次全部通过。没有声称已派发 Android 命令可撤回。

部署初始时 App 同步失鲜、can_start=false；在无活动会话且旧收尾 confirmed 后启动 MainActivity 与前台 ControlService，恢复新鲜同步。没有删除账本、卸载 App 或清私有数据。

## 7. 审计、偏离与未执行项

独立只读文档审计使用用户指定的 `ollama-cloud/deepseek-v4.1-flash:1017`、reasoning=max。初审发现 Origin 末尾斜杠与旧 initialize 生命周期两处既有文档漂移；按建议最小修正，复检 clean。审计者没有修改文件，运行期间未改被审 docs，未中断/重启审计。独立只读代码审查发现的取消竞态与 oracle 误判已先红修正并复检。

实现未改变模式/事实集/结构化形状/执行时序契约；文件拆出 result_projection.go 组织投影。版本按提案 §6.1 不立即递增，manifest 不增加可选 result_modes 字段。没有对外发布新版本。

| 未执行项 | 原因与边界 |
| --- | --- |
| 外部模型宿主及空 content 接受度 | 当前使用官方 SDK 客户端，不把它当外部宿主证明 |
| 外部宿主主动截断及 token 成本 | 没有合适的截断宿主/tokenizer；单测缺尾检测、记录字节成本 |
| 手机上限树/补看/输出/apps 场景 | 当前页面未达服务上限；接近上限仅合成 fixture 验证 |
| 实机只读取消同时返回无错误结果等竞态 | 不能确定性注入；直接 invoke 和三模式单测覆盖 |
| go test -race | Windows 未找到 C 编译器，未启用 cgo |
| 外部旧 stdio 宿主刷新 | 部署仍检测到一个既有旧 stdio 进程；未擅自停止外部宿主，需其所有者重启。手机 HTTP 进程已按清单替换，验证客户端重新连线并发现目录 |

原始节点树、截图、响应、日志与凭据全部留在 gitignored .state/build 或设备私存，不入库。本记录只保留批准的元数据与真实验证边界。
