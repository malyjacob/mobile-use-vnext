# 普通观察树裁剪验证记录

状态：2026-11-20。涉及手机实机。实现提交
`2d6c389457748667771973f6021f3a18ba9cfcf3`，发布版本 0.23.0、共同契约 21、Android versionCode 25。
方案见[归档提案](../proposals/implemented/compact-observation-pruning.md)。

## 1. 实现与审计

分诊为取舍。选用提案 R3，精简 compact 中的小型无标注图片和符合条件的匿名容器。
`ProjectCompactTree` 在同一遍历保留原几何决策并完成裁剪。父节点的 T2a 判定使用原逻辑孩子是否
存在和剩余孩子是否为空。T2b 以存续的直接孩子数为准。保留字段沿用原节点的投影值。
`ReadDiff` 生成普通节点定义，`ReadApply` 展开历史链后构造最终树。动作和补看读取完整快照。

提案独立只读审阅核对了裁剪判据、历史链输入和发布准备。实施前冻结版本，移除缺少来源的
旧窄版统计，并补齐虚拟屏夹具、Origin 和探针准入步骤。复检结论通过。
正式文档独立只读审计发现一处版本源的路径指代不清，改为显式 `release/versions.json` 后
复检 CLEAN。工具目录包含 13 个工具，SHA-256 为
`d9939b8f928fc282cfd118aa32ee4b4f20a5534e3ae6c4fb7d5665a0ee72e14c`。

## 2. 单元测试、回归与反向对照

环境为 Windows x64，Go 1.27.1，`GOEXPERIMENT=jsonv2`，Android 构建使用 JDK 17。
主代理执行以下命令。

```powershell
$env:GOEXPERIMENT='jsonv2'
go test -count=1 -timeout=20m ./cmd/... ./internal/... ./scripts/...
go test ./internal/engine -run '^(TestDeltaCostGateIsStrictlyBelowSeventyPercent|TestI04ActualProjectionPruningDeltaTopology|TestActPrunedImageCarrierEvidence|TestActStaticPrunedCarrierRemainsInspectable|TestActStaticPrunedAmbiguityRemainsInspectable)$' -count=1 -v
$env:GOOS='linux'
$env:GOARCH='arm64'
$env:CGO_ENABLED='0'
go build ./cmd/... ./internal/... ./scripts/...
.\android-control\test.ps1
.\scripts\generate-version.ps1 -Check
```

| 验证 | 实际结果 |
| --- | --- |
| Go 全量 | 最终实现树的全部维护包通过，engine 耗时 `562.488s`。命令覆盖 `cmd/`、`internal/`、`scripts/`。忽略目录 `build/` 内的本地调研程序在此范围外 |
| 引擎定向 | 70% 等值和变更标签少一字节的边界、实投影固定基线、tap/long_press 承载点、静态建议/歧义、原 O 补看均通过。I04 在最终测试代码上额外连续通过 8 次 |
| 交叉构建及生成件 | linux/arm64、`CGO_ENABLED=0` 的全量维护包构建通过。版本生成校验通过 |
| Android JVM | `DisplayAttemptGateTest` 和 `MessageListTest` 通过 |
| MCP 与协议 | 当前树交付、历史 full/add/replace 及链内引用兼容、strict JSON、三模式节点成员、实注册和双握手说明、工具指纹、探针的 v3 当前树检查通过 |

首轮全量快照曾在正在调试的 I04 夹具上失败。早期断言要求删除目标必须显式出现在 `remove`，
实际相对固定 full 基线的结构删除可以由 `replace` 表达。定稿用例以重建后的拓扑、
规范 JSON 和合法结构删除为准。

在同仓库基线 `a6b653d` 上使用 `go test -overlay` 运行新增裁剪判据用例，得到预期失败。
随后对当前版本逐项用私有 overlay 禁用图片省略、空容器清理、单子包装绕过，三组测试分别
在图片仍交付、空容器仍交付、提升后父子列表不符的断言处失败。将
`internal/engine/capture.go` 的 `10*deltaBytes < 7*fullBytes` 改为 `<=` 的单独反向对照，
等值用例在应交付 full 的断言处失败。反向对照只在 overlay 使用替换源码。

## 3. 清单、手机覆盖部署及 Windows 替换

两组产物均从实现提交为 HEAD 的干净工作树构建和核验。

```powershell
.\scripts\build-release.ps1
.\scripts\verify-manifest.ps1 -Manifest build/release/manifest.json
.\scripts\build-host-tools.ps1
.\scripts\build-host-tools.ps1 -VerifyOnly
.\scripts\deploy.ps1 -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'
```

手机清单六件产物与 Windows 清单两件产物均通过大小、SHA-256、源码提交、共同契约和
目录指纹校验。控制 App 用 `adb install -r` 覆盖，`firstInstallTime` 与升级前一致。
配对、恢复屏障和 STOP 可用性门禁通过。手机 HTTP MCP 进程的实际二进制哈希与清单一致。

| 入口或磁盘文件 | SHA-256 |
| --- | --- |
| 手机 `device-service` | `8d2ac63bc0190987519cecfaec9dcc521b5e7f8c4caa859acae7230fa7db6a72` |
| 手机 HTTP `device-mcp` | `96ce1e6c1bbec5861f903155794653c4fca58d270a225d563811da9a208f0d89` |
| Windows `build/device-mcp.exe` | `57fb9bd87501f23aa2ab1480b1461aa6e14c84f1b7d6fee9dea43d4a8ae75a70` |
| Windows `build/mcp-probe.exe` | `42d98ef7e1ddf0e0e3abbd6369b1f1b545c455bc94a54462c12be6b17249fda4` |

Windows 两个文件的磁盘哈希均较构建前改变，替换后的哈希与 Windows 清单一致。
依赖方切换单独计量。部署脚本仍报告一个旧的手机 stdio 托管进程，宿主重启和工具重新发现
由用户管理。独立进程结果见 §5。

## 4. 手机受控页与同帧离线复算

Android 16、1080×2400，沿用虚拟屏模式。受控页是临时同签名覆盖安装的
`com.mobileuse.probe` Activity。构建在 `.state/compact-pruning/fixture-build` 中运行
原 `build-probe.ps1`。独立小图、匿名包装、小图承载卡片、子按钮和并列分支均由原生控件构造。
原测试 APK 在验证结束后同签名覆盖恢复，手机文件 SHA-256 与备份一致。

驱动运行命令为
`python .state/compact-pruning/device.py --fixture .state/compact-pruning/fixture.json --output .state/compact-pruning/live-02`。

| 实机步骤 | 实际结果 |
| --- | --- |
| 初始 full | `start` 取得 ready compact full 并建立虚拟显示。受管 `shell` 仅启动该显示上的夹具。随后 `observe(full=true)` 交付夹具首个 ready full，共 13 节点，省略 3 张小图和 8 个包装。文字及交互节点保留，双子分支保留，裁后单子路径提升 |
| 原 O 静态拒绝与补看 | 卡片 tap 返回 HTTP 422、`no_safe_point`、`outcome=not_executed`。承载图片 ref 缺席于 compact，原 O 保持 ready。同一 `reference`、同一诊断 `node` 补看出图片及其已知矩形 |
| 基线确认 | `observe(reference=夹具首个 full O, full=false)` 交付同基线 delta。比较两帧详细树时忽略 ref 后结构和字段完全相同，页面计数保持零。delta 含 3 个 add、3 个 remove、1 个 children 声明 |
| 有效动作与下一帧 | 以确认后的 ready O 点子按钮，返回 `executed`，应用动作计数加一，阻塞子节点和卡片点击计数仍为零。原承载图保持 ref 并放大，动作后非空 delta 仍指向夹具首个 full O。立即补看该帧完整 detailed 树。重建的 compact 中原图重新出现，匿名容器同时接回图片与子按钮 |
| 结束 | `end` 返回 session ended、cleanup pending，后续独立探针前观测到 cleanup complete、`can_start=true` |

首次驱动额外把基线确认的 delta 限定为空，因编号分配变动而停止并主动结束了测试会话。
按提案实际验收条件调整驱动后复跑，通过同一基线、详细树内容与页面计数核验。

当前和基线源码各自使用 `go test -overlay <overlay> ./internal/mcp -run '^TestCompactPruningDeviceReplay$' -count=1 -v`
回放同组 3 份详细树。基线为同仓库 `a6b653d` 的独立检出。每帧按正式策略选择 full/delta，
严格解码并按已确认 full 重建，校验逻辑树和规范 JSON。当前路径额外对同帧真实 HTTP 树重建，
三帧与各自详细树重新投影得到的 full 逻辑相等、规范字节完全一致。以下字节只计普通树块，
token 由本地 `tiktoken` 0.12.0 的 `o200k_base` 对 MCP `tree v3` 块计算。

| 帧 | 旧 / 新形式 | 旧 / 新逻辑节点 | 旧 / 新 compact JSON 字节 | 旧 / 新树块 token |
| --- | --- | ---: | ---: | ---: |
| 夹具 full | full / full | 24 / 13 | 1491 / 1244 | 331 / 291 |
| 确认已交付的 full | delta / delta | 24 / 13 | 388 / 388 | 152 / 152 |
| 按钮动作后 | delta / delta | 24 / 15 | 802 / 832 | 275 / 267 |
| 合计 | 各 1 full、2 delta | 72 / 41 | 2681 / 2464（−8.1%） | 758 / 710（−6.3%） |

同样输入在 Windows/amd64 Ryzen 7 5700X 上对 `ProjectCompactTree` 运行
`go test -overlay <overlay> ./internal/mcp -run '^$' -bench '^BenchmarkCompactPruningDeviceProjection$' -benchtime=500ms -count=3`。
三次中位数由 13,770 ns/op 降到 4,118 ns/op，分配由 15,749 B/op、396 allocs/op
降到 6,752 B/op、172 allocs/op。计量窗口为纯投影，样本仅含上述三帧。

## 5. 独立 MCP 探针

在手机准入就绪后顺序运行新探针，两份构建清单同时作为目录核验输入。

```powershell
.\build\mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/compact-pruning/stdio.private.json -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
.\build\mcp-probe.exe -transport http-remote -config .state/compact-pruning/http-remote.private.json -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

两条均 `RESULT PASS`。主机新 stdio 子进程与手机已运行的 HTTP 适配器都发现 13 个工具。
`server/discover` 与兼容 `initialize` 返回的 6,922 字节 instructions 和源码一致。
实际 `tools/list` 描述与 schema 匹配，双清单的目录指纹吻合。普通树和详细树使用 v3，
观察/动作/结束及取消恢复通过。远程入口的 401/403/200/401 鉴权与 Origin 检查通过。

## 6. 未执行项与依据

| 项 | 结论与原因 |
| --- | --- |
| 原 45 帧按最终面积判据复放 | 样本缺少同帧详细几何，采用 §4 所列 3 帧受控页面序列 |
| 主屏复测、其他手机与业务页面 | 当前准入模式为虚拟屏，按提案最小改动闭环验证该模式 |
| 历史折叠链在现场生成 | 历史 full/delta 的读取由结构化和文本夹具覆盖，当前产物的实机检查聚焦普通定义交付 |
| 依赖方重启和重连 | 由用户处理，证据范围为磁盘替换及两个独立 MCP 入口 |
