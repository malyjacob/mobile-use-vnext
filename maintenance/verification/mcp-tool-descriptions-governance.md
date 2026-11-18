# MCP 工具说明、只读注解与查询 Schema 校准验证

涉及实机只读核验。实现提交为 `2c0852ffa6999e482cd033d393994d313273b3fc`，实施基线为 `5cc69e7`。Windows/amd64，Go 1.27.1，启用
`GOEXPERIMENT=jsonv2`，另做 Linux/ARM64 交叉构建。第一批是独立适配器缺陷修复，
见[无 P 恢复投影验证](mcp-no-operation-recovery.md)。

## 1. 实现与检查边界

分诊为发现元数据与调用说明的取舍。实施覆盖原始 MCP 发现与探针校验。
按基线及候选的 MCP 线协议比较，共同 HTTP 契约、输入解码和工具集合沿用现行路径。
主机临时候选访问现有手机服务只做查询及一个静态拒绝样本。

## 2. 先红与反向对照

- 在第二批改动前运行
  `go test ./internal/mcp -run '^(TestReadOnlyToolAnnotations|TestLiveQuerySchemaMinimum)$' -count=1`。
  两项先红。目录只有 `snap` 标记，另三项只读工具缺标。三处查询 schema 均缺最短长度。
  以相同用例在候选和全包回归中检查，结果通过。
- 用临时 Go overlay 将 `scripts/mcp-probe/catalog.go` 恢复为实施基线，运行
  `go test -overlay <映射> ./scripts/mcp-probe -run '^TestCheckLiveCatalogReadOnlyHints$' -v -count=1`，
  `status` 漏标与 `act` 多标两项均失败。再用 overlay 仅去掉新判据，跑同一个测试，
  两项再次失败，实测原因是错误注解被放行。恢复候选实现后
  `go test ./scripts/mcp-probe -run '^TestCheckLiveCatalog(ReadOnlyHints)?$' -count=1` 通过。

## 3. 回归与构建

各 Go 命令使用 `GOEXPERIMENT=jsonv2`。

| 实际命令或操作 | 结果 |
| --- | --- |
| `go test ./internal/mcp ./internal/protocol ./cmd/device-mcp ./scripts/mcp-probe -count=1` | 四包通过，覆盖工具目录、模式、取消、严格解码和探针。 |
| `go test ./internal/engine -run '^(TestActTextWriteNotDispatchedStaysUncertain\|TestInspectQueryMatchesLabelValueHintResource\|TestOrdinaryAdmissionRejectsAllToolsBehindUnresolvedShell\|TestShellStartOutcomes\|TestWaitFor(AbsentQueryIgnoresTransitionFrame\|RejectsFoldedContradiction\|LatestSuccessFollowedByFailureCapturesTerminalFrame)\|TestScrollOuterScrollAncestorAllowed\|TestScrollEffectSizeAndContainerChangesAreUnknown)$' -count=1` | 通过。覆盖仅聚焦的文本恢复、查询匹配、等待终帧、受管命令忙阻塞、嵌套滚动与位移未知。 |
| `go test ./internal/engine -run '^(TestActTapCrossOcclusionSuggestsFirstGridPoint\|TestActBubbleNoSafePointReportsAncestorScope)$' -count=1` | 通过。覆盖建议点与最近祖先拒绝范围。 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/device-mcp ./scripts/mcp-probe ./scripts/toolcatalog` | 交叉构建通过。 |
| `go run ./scripts/toolcatalog` | 13 项，目录指纹为 `99dd22d0a81280b6875eaa770b81ee0f553e8a6b9e49f4bbb0c83078f25494c8`，与测试和正式文档相同。 |

## 4. 发现体积与协议对比

`go test ./internal/mcp -run '^TestDiscoveryByteBudget$' -v -count=1` 的同口径数据如下。
基线用临时 Go overlay 恢复 `schema.go`、`catalog.go`、`server.go`，保留分项计量测试。

| 指标（B） | 基线 | 候选 |
| --- | ---: | ---: |
| 全局指令 | 7,051 | 5,326 |
| 工具数组重编码 | 20,999 | 20,288 |
| 描述文本合计 | 8,802 | 8,478 |
| 压缩 Schema 合计 | 11,491 | 10,909 |
| 发现总量（全局指令 + 工具数组） | 28,050 | 25,614 |
| `act` 描述 / Schema | 2,867 / 3,947 | 2,526 / 3,304 |
| `app` 描述 / Schema | 1,347 / 2,107 | 1,082 / 2,107 |

总量少 2,436 B（8.7%），通过 `<36,250 B` 门槛。描述和 Schema 分项都包含于工具数组。
字节数据衡量目录体积。

临时 Python 回放用 `go build -trimpath -buildvcs=false -o <temp>/device-mcp.exe ./cmd/device-mcp`
构建候选，基线另加 `-overlay <映射>`。分别向临时适配器的 stdio 和 Streamable HTTP
发送 `server/discover`、`tools/list` 及兼容 `initialize`，并做以下检查。

1. 对比两版 stdio 工具名、注解、描述、Schema。仍有 13 项，四项只读标记，六项描述变化。
   忽略字段说明后，三个查询字段新增 `minLength: 1` 是全部结构差异。
2. 候选两传输的现代发现目录与指令逐项相等。兼容握手的指令也相同。向两入口交叉提交
   空或全空白的 `wait_for.query` 和 `absent_query`，上游指向未监听回环端口，
   四次均在接入阶段返回 `invalid_request`。
3. 同一临时 stdio 候选经现有转发连接手机服务。`status({})` 与
   `diagnose({})` 均成功。直接发送 `inspect(reference=O7, query="")` 得到
   `invalid_request`。该测试使用已运行的服务确认拒绝分支，不采集新观察或写入 UI。

## 5. 审计与验证边界

独立只读文档审计使用 DeepSeek V4.1 Flash 1017（`auto/deepseek-flash`，推理强度 `max`）。
初审意见涉及一处说明措辞及目录指纹与旧清单的来源，修订后 `docs/mcp.md` 与
`docs/integration.md` 复检为 CLEAN。读取两份既有清单的 `source_commit` 与目录指纹，
均对应 `a53f95a`（`e25fc677…de4`），其目录定义与实施基线相同。候选目录指纹属于本次源码。

实施验证覆盖 MCP 线协议、现有引擎夹具和临时适配器到手机服务的查询。整组发布结果见 §6。
模型配对回放未执行，现有证据用于确认说明内容及调用契约，没有估算模型误用率。

## 6. 整组发布与主机替换（2026-11-19）

发布源码为 `a98b4adc0db644d1a31b6ad961f253506a07b946`，构建和发布门禁在该干净提交
执行。环境为 Windows/amd64、Go 1.27.1、Android SDK 与 JDK 17。手机为 USB 连接的
Android ARM64，既有 chroot 在线。发行 `0.19.0`，共同契约 `19`，控制 App 版本码 `21`。
该节随后的维护提交登记结果，发布清单继续绑定实际构建的源码提交。

### 6.1 构建与门禁

```powershell
powershell -NoProfile -File scripts/build-release.ps1
powershell -NoProfile -File scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
powershell -NoProfile -File scripts/build-host-tools.ps1
powershell -NoProfile -File scripts/build-host-tools.ps1 -VerifyOnly
```

四项通过。手机清单核验六个产物，主机清单核验 `build/device-mcp.exe` 和
`build/mcp-probe.exe`，两份清单具有相同源码、发行、共同契约和 13 工具目录指纹
`99dd22d0a81280b6875eaa770b81ee0f553e8a6b9e49f4bbb0c83078f25494c8`。
主机脚本替换了两个目标 exe。因两个原主机适配器进程仍引用旧映像，脚本将旧映像保留为
备份。替换后两个既有进程仍在原宿主下运行。

### 6.2 手机整组部署

部署前 `status({})` 给出 `can_start=true`、原会话 `ended`、旧收尾 `unconfirmed`。
沿用现有私有配对和 Origin 配置，执行如下命令。

```powershell
$phone = Get-Content -LiteralPath .state/phone-mcp-config.json -Raw | ConvertFrom-Json
./scripts/deploy.ps1 -SkipBuild -McpAllowedOrigins @($phone.http.allowed_origins)
```

部署门禁再次通过。清单文件与生成的配置按脚本核对，APK 用 `adb install -r` 覆盖安装，
验证包 `com.mobileuse.vnext` 的版本为 `0.19.0/21`。新设备服务和唯一手机 HTTP MCP
进程的 `/proc/<pid>/exe` 哈希均与清单匹配。设备服务实例已变更，健康检查、App 配对、
恢复屏障和 STOP 可用项通过。手机 HTTP MCP 经 `18883` 转发可用，脚本另观察到一个
宿主管理的旧手机 stdio 适配器仍在运行。

### 6.3 发布烟测

```powershell
./build/mcp-probe.exe -transport stdio -binary ./build/device-mcp.exe `
  -config .state/mcp-config.json -result-mode both -suite smoke -timeout 180 `
  -manifest build/release/manifest.json -manifest build/host-tools-manifest.json

./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json `
  -result-mode both -suite smoke -timeout 180 `
  -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

两套烟测依次运行，均返回 `RESULT PASS`。13 个工具、四项只读注解、描述和 Schema 与源码
一致，目录指纹匹配两份清单。现代发现与兼容握手的说明均为 5,326 B，逐字节匹配。
手机 HTTP 的鉴权和既有回环 Origin 检查通过。两次探针会话均为虚拟屏，观察、等待、取消
和终止闭环通过。

以发布版主机 exe 另做隔离断连回放，上游指向已绑定但未监听的回环端口。使用 stdio 的
`both` 模式调用 `status({})`、`status(reference=P7)`、`inspect(reference=O7)` 和
`diagnose({})`，均返回 `transport_unavailable` 并省略本次 P 与合成恢复。同一夹具中
`snap(reference=O7)` 保留已分配的 P 和 `status/P`，符合第一批的边界。

烟测结束后重新核验两份清单，通过。`GET /health` 返回 `ok`、发行 `0.19.0`、
契约 `19`。`POST /api/status` 返回 `can_start=true`、会话 `ended`、本次收尾
`complete`。宿主刷新由用户处理，烟测使用独立探针进程和刚部署的手机 HTTP 入口。
