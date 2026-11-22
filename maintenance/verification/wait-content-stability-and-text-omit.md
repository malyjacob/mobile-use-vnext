# 内容稳定等待与 MCP 文本省略验证记录

2026-11-22（Asia/Shanghai）。涉及手机实机。实现提交 `6202abe40a18ccbfae5d0975b7173509eba50d26`，发行 0.27.0、共同契约 23、Android versionCode 29。[归档提案](../proposals/implemented/wait-content-stability-and-text-omit.md)

## 1. 分诊与审查

#33 归因为基线 [waitFor 轮询](../../internal/engine/wait_for.go) 使用整树摘要，几何动画与无动作文本变化会使三帧门禁超时。#40/#41 的投影来自 [result v4](../../internal/mcp/result_text.go) 与 [工具说明](../../internal/mcp/schema.go)。两类现象均符合原契约，按取舍落实提案 R1。核查条件等待、终帧与 `act` 采样共用关系后，将新摘要限定在 `wait_for stable=true`。

独立实现审查指出 [终帧摘要错误](../../internal/engine/wait_for.go) 可能增加计数。采纳内容摘要清零且不计样，覆盖终帧编码失败测试。保留 `stable=false` 终帧旧摘要错误分支，理由是既有条件等待语义须保留。已将取舍回复审查者。

独立只读正式文档初审指出 [集成指南](../../docs/integration.md) 仍称只省略默认观察质量。修正为省略整个 `observation.quality` 与 `effect` 后，原审计员复检 **CLEAN**。两种指定的 DeepSeek 子代理分别用于实现、探针和独立审查，均为 `max`。最终运行测试、构建、部署和实机均由主代理执行。

## 2. 源码验证与反向对照

Windows x64、Go 1.27.1、JDK 17。实际执行：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test -count=1 -timeout 15m ./internal/engine -run '^TestWaitForContentStabilityWithAnimatedFrames$'
go test -count=1 -timeout 15m ./...
go vet ./cmd/... ./internal/... ./scripts/...
./scripts/generate-version.ps1 -Check
./android-control/test.ps1
git -c core.safecrlf=false diff --check
```

- 改动前新动画用例红。纯稳定等待得到 `timeout`，带查询的稳定等待得到 `timeout`（终帧仍命中查询）。改动后两条路径分别命中 `stable` 和 `query`，`stable=false` 对照仍给出 `unstable/3/condition`。
- 全量运行中 `cmd/`、`internal/`、`scripts/` 下所有跟踪源码包测试均通过，引擎用时 **625.921 秒**。无约束的 `./...` 还包含已有且被忽略的 `build/investigation-text-uncertain`，该副本缺少 `textFlowDevice` 等定义，令总命令退出 1。保留该调查副本；引擎全套只跑了一次。
- 源码 `go vet`、生成版本一致性检查、Android `DisplayAttemptGateTest` / `MessageListTest` 与 `git diff --check` 均通过。根目录 `go vet ./...` 同样命中该调查副本。
- MCP 隔离反向对照恢复旧渲染后，`TestResultV4QualityEffectOmissionManifest` 分别因 `effect=` 与 `quality=` 出现而失败。探针测试拒绝旧标量及 JSON fallback，分别验证 `text`、`both`、`structured`，并覆盖说明与 schema 的禁用词命中。

## 3. 整组构建与部署

在干净实现提交上执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
./build/device-mcp.exe -version
./scripts/deploy.ps1 -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'
```

- 两份清单验证通过，共享源码提交、13 工具目录指纹 `24e29ffad62cd998e1ac5fc26fcf4479e672806132ddce55ad3785794ebc6f0b` 与版本源。APK v3 签名检查通过，手机产物为 linux/arm64。
- 设备 `24117RK2CC`、Android 16、虚拟显示 1080×2400。覆盖部署含服务、固定制品、控制 App 和手机 HTTP MCP。APK 使用 `adb install -r`，前后 `firstInstallTime` 相同。设备文件及运行中 HTTP 适配器校验通过，`/health`、App 配对、恢复屏障及 STOP 可用性通过。
- Windows `build/device-mcp.exe` 和 `build/mcp-probe.exe` 已磁盘替换。前者的旧映像仍由外部进程打开，构建脚本保留了旧备份文件。两份清单和 exe 校验通过，新适配器 `-version` 为 0.27.0。

## 4. 独立探针与实机

以部署状态生成的 Git 忽略配置启动独立进程，配置和令牌未入库：

```powershell
$manifests = @('-manifest', 'build/release/manifest.json', '-manifest', 'build/host-tools-manifest.json')
./build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/wait-content-stability-and-text-omit/stdio.private.json -result-mode both @manifests
./build/mcp-probe.exe -transport http-remote -config .state/wait-content-stability-and-text-omit/http-remote.private.json -result-mode both @manifests
./build/mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/wait-content-stability-and-text-omit/stdio.private.json -result-mode text @manifests
./.state/wait-content-stability-and-text-omit/live.exe
```

- 三次独立探针均为 **RESULT PASS**。两入口的 13 工具目录与双清单指纹一致，`tools/list`、`server/discover` 与兼容 `initialize` 逐处扫描未命中四个禁用词。手机 HTTP 另验证无认证 401、Origin 拒绝 403、许可 200、Agent token 拒绝 401。文本探针的 `wait_for` 与 `act` 均确认省略。
- 手机 HTTP MCP `both`，从新会话打开网易云音乐并进入播放页。两次进度读取证明页面在播放期间推进。连续三次 `wait_for(stable=true, timeout=10, full=true)` 均返回 `trigger=stable`，耗时 **1713、1664、1756 ms**，均保留结构化 `quality=stable/3/stable`。进入播放页的 `act` 返回结构化 `unstable/4/budget`。所有对应文本省略 `quality`。验证后暂停播放。
- 同一会话打开 B 站推荐页并 `scroll(down)`，结果为 `executed`。同一响应的 `result v4` 文本省略两个字段，结构化保留 `effect.state=observed` 与 `quality=unstable/2/budget`。最终 `end` 成功。

## 5. 未执行项

| 项 | 理由 |
| --- | --- |
| 知乎滚动 | 提案允许 B 站或知乎，已使用 B 站完成滚动闭环 |
| 外部依赖 MCP 宿主切换 | 手机还有一个旧 stdio 进程，Windows 也保留占用中的旧进程映像。宿主重启和工具重新发现由依赖方处理，独立探针只验证本次入口 |
| delta 几何省略、`effect.reason`、预算拆分、确认采样与自定义忽略字段 | 超出已确认范围 |
