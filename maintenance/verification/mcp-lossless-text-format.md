# MCP 无损结果与树文本验证记录

2026-11-21（Asia/Shanghai）。涉及手机实机。功能与正式文档提交为
`40551f5d9f807f1b95b8e41100bd6be28556f387`，探针报告及最终发布源码提交为
`fb51419bdf28eacc886d3c809879e8db3ea15fe7`。发行 0.24.0，共同契约 21，Android versionCode 26。
设计见[归档提案](../proposals/implemented/mcp-lossless-text-format.md)。

## 1. 实现与文档审计

分诊为格式取舍，交付确认的 R4。完整文本使用 `result v3`，将连续观察字段分组，树外保留可
回传的完整 O。普通树使用 `tree v4`，缩短块头、矩形、缩进和动作，按当前 O 展开同前缀的短
base。`inspect v3` 保留详细语法。两个文本版号与共同契约分别计量。

独立只读审计核对了 6 份正式文档。初审发现示例的 compact 几何、动作、差分挂接、角色省略、
补看计数及既有 ADB 转发措辞问题。修正后由同一审计者复检 CLEAN。主代理另用完整测试解码器
解码正式文档和共享发现说明的示例，并将两组示例差分分别应用到各自整树。
最终探针报告修正不涉及 `docs/`。

## 2. 单元测试与反向对照

环境为 Windows x64、Go 1.27.1、`GOEXPERIMENT=jsonv2`、JDK 17。主代理执行：

```powershell
$env:GOEXPERIMENT='jsonv2'
go test -count=1 ./cmd/device-mcp ./internal/mcp ./internal/protocol ./internal/observation ./internal/client ./scripts/mcp-probe ./scripts/toolcatalog
go vet ./cmd/device-mcp ./internal/mcp ./scripts/mcp-probe
go run ./scripts/toolcatalog
.\scripts\generate-version.ps1 -Check
.\android-control\test.ps1
$env:GOOS='linux'
$env:GOARCH='arm64'
$env:CGO_ENABLED='0'
go build ./cmd/... ./internal/... ./scripts/...
```

定向 Go 测试、vet、版本生成校验、交叉构建及 Android 的 `DisplayAttemptGateTest`、
`MessageListTest` 均通过。完整测试解码覆盖状态三态、未知字段、重复路径拒绝、三种结果
模式与错误结果附树。v4 用例包含默认动作正反例、严格坐标与计数、短 base 的 uint64 上限、
历史链及同一整树基线的独立差分。保留合法头和计数时，篡改标签、动作、矩形或 base 均被
完整事实比较检出。发现汇总为 31,320 字节，既有上限为 36,250 字节。

主代理以 `go test -overlay <overlay> ./internal/mcp -run <定向用例> -count=1 -v`
将生产输出依次改回旧 result 头、旧 tree 头及错误树终止行，分组或固定字节断言分别失败。
初次实机报告中 `start`、`observe` 的校验已读取 v4，成功行仍显示 v3。归因到
`scripts/mcp-probe/checks.go:513`、`:599` 的常量，`inspect` 的 v3 版号正确。先更新
`TestAdapterChecksAgainstFakeAdapter` 的断言，both 与 text 分支报缺少 `tree_block=v4`，
再修正两处成功行并复跑。最终两份清单均从含该修正的干净实现提交构建。

## 3. 历史轨迹复算

在基线 `79e0658` 使用私有 overlay 运行 `TestLosslessBaselineExport`，旧解码器还原
220 份 0.23.0 MCP 投影并重编码，全部逐字节等于原 MCP 文本。导出器为 26 份错误结果
加写的 `Error: ` 前缀在解码时剥离，在两侧体积计量时等量恢复。

在最终源码用同一私有输入运行 `TestLosslessPrivateParity`。220 份新版结果均逐字段
一致、重复编码字节一致。回读 114 棵 full、47 份 delta 和 3 份 inspect。47 份 delta 的
短 base 都恢复为原完整 O，并各自应用到其确认整树，重建树字段及规范 JSON 字节一致。

```powershell
python .state/mcp-lossless-text-format/prepare_replay.py
go test -overlay <baseline.overlay.json> ./internal/mcp -run '^TestLosslessBaselineExport$' -count=1 -v
go test -overlay .state/mcp-lossless-text-format/current.overlay.json ./internal/mcp -run '^TestLosslessPrivateParity$' -count=1 -v
python .state/mcp-lossless-text-format/prepare_replay.py --compare
```

| 计量 | 0.23.0 文本 | 0.24.0 文本 | 节省 |
| --- | ---: | ---: | ---: |
| UTF-8 字节 | 560,170 | 453,255 | 106,915（19.09%） |
| `o200k_base` 代理 token | 186,456 | 167,254 | 19,202（10.30%） |

计数与提案的实测目标一致。轨迹、overlay 源、凭据及实机捕获留在 gitignore 下的私有
`.state/`，仓库只保存聚合结果。

## 4. 发布清单、手机覆盖和主机磁盘替换

主代理从最终干净实现提交构建并核验两组清单，手机 6 件产物和 Windows 2 件产物全部通过
版本、大小、SHA-256 与源码身份校验。两组工具目录均为 13 项，指纹为
`5738c699da9c0a980e782f44943910718844447d9d00e04c0fb5a79682afc3cf`。

```powershell
.\scripts\build-release.ps1
.\scripts\verify-manifest.ps1 -Manifest build/release/manifest.json
.\scripts\build-host-tools.ps1
.\scripts\build-host-tools.ps1 -VerifyOnly
.\scripts\deploy.ps1 -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'
```

手机为 24117RK2CC，Android 16，1080×2400，虚拟屏模式。控制 App 通过 `adb install -r`
覆盖，初装时间和原配对凭据一致，实际包版本为 0.24.0/26。配对、恢复屏障、STOP 可用性
及新会话准入通过。部署脚本确认手机 HTTP 进程实际可执行文件与手机清单一致。

| 实际产物 | SHA-256 |
| --- | --- |
| 手机 `device-service` | `26837332a09e9c63f2ff233b3cb756353f874dc7304bb6bd2767987cf9ed788e` |
| 手机 HTTP `device-mcp` | `0b5a6924ba250b45ad6bd8993c944eb1c817a9718c6f8f96d3822032109f98f8` |
| 手机覆盖安装的 `control.apk` | `8ee3fa4f95474d190403e040eb5e0a67825c5bc3af88fa89f27c277bc6ff59ca` |
| Windows `build/device-mcp.exe` | `18167f045e2931511933fa62b0c7b344ec6b0641ac9e317bafd65766a6620f9c` |
| Windows `build/mcp-probe.exe` | `311b483654f716ff1dfcef88fcf1638f386a3b118c8d2a89f528eee00e7ddfb1` |

Windows 两个路径的磁盘哈希都较 0.23.0 改变，且与最终主机清单吻合。`-VerifyOnly` 在
最终实现提交仍为 HEAD 时执行，独立进程证据见下节。

## 5. 双入口独立探针与实机闭环

```powershell
.\build\mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/mcp-lossless-text-format/stdio.private.json -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
.\build\mcp-probe.exe -transport http-remote -config .state/mcp-lossless-text-format/http-remote.private.json -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
& .state/mcp-lossless-text-format/verify-fixture.ps1
```

最终探针两入口均 `RESULT PASS`，`start`、`observe` 成功行确认普通树 v4，`inspect`
确认详细块 v3。`server/discover` 与兼容 `initialize` 的 9,045 字节说明匹配源码。
`tools/list` 通过名称、描述、schema 与双清单目录指纹校验。手机 HTTP 鉴权和 Origin
检查、两入口错误文本、图片和取消恢复检查通过。最终手机远程探针首轮收到
`shell_result_unconfirmed`，探针结束了所建会话。确认准入恢复后重跑通过。

受控页使用同签名测试 APK 临时覆盖 `com.mobileuse.probe`。两入口分别执行
`start → shell 启动受控页 → observe(full=true) → observe(确认基线) → act(tap) → inspect(node) → end`。
首次驱动把指定节点的补看字段当整树读取，修正为实际 `subtree` 结构后闭环通过。
最终两入口各交付 2 棵整树、2 份短基线差分和 1 个详细补看块。受控页命中 `@` 和省略
`tap`，由组内完整 O 驱动节点点击，计数从 0 变为 1，动作后非空差分各含 1 条顶层声明。
两入口的独立捕获逐字段比对一致，差分各自使用捕获中对应的完整 base 重建，重建树逻辑及
规范 JSON 字节一致。两会话均正常结束并报告收尾完成，恢复原测试 APK 后核对其哈希，
两入口再次返回 `can_start=true`。

| 最终闭环捕获 | 完整结果数 | 字节旧 → 新 | 代理 token 旧 → 新 |
| --- | ---: | ---: | ---: |
| 主机 stdio | 14 | 9,067 → 8,062 | 2,972 → 2,644 |
| 手机 HTTP MCP | 15 | 9,609 → 8,604 | 3,168 → 2,840 |

两侧旧版大小为同一结构化捕获送入基线渲染器的结果，实机只运行最终产物。主屏、其他设备
与依赖 Agent 的托管进程切换未执行。按提案由用户重启依赖宿主并重新发现工具。
