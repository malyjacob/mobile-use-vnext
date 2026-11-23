# 当前 full 基线与 tree v4 差分验证

实现提交 `d80341be36e5d1100abed132d1cfdff54134e97a`，分诊为默认行为取舍。Windows amd64、Go 1.27.1、GOEXPERIMENT=jsonv2，手机 24117RK2CC、Android API 36，虚拟屏。发行 0.30.0，契约 26，versionCode 32。[归档提案](../proposals/implemented/active-full-baseline-and-tree-v4-delta.md)

## 测试与审查

- 主代理执行 `go test -count=1 -timeout 15m ./cmd/... ./internal/... ./scripts/...`，各包通过，引擎 730.686 秒。
- 全量后采纳实现审查的两项发现。先红用例复现留存规划期间到期仍提交，以及 full 回退可留下第四份备用。修正后按新时间重验工作、采集及基线，预算不足时保留原状态。另补真实发布路径下文本与 JSON 排序相反、最短差分放不下而后续候选可交付的两项集成测试。
- 主代理在修正后执行 `go test -count=1 -timeout 15m ./internal/engine -run 'Test(BaselinePool|BaselineSelection|RetainedSnapshot|TreeText|DeltaCostGate)'`（25.945 秒）和单项 `TestFullFallbackWithoutCurrentKeepsThreeBackups`（2.141 秒），均通过。当前跨度 4/5/10/11 和严格 70% 边界由受控用例覆盖。
- 独立只读正式文档审计的三项意见均采纳。修正锁内预算规划说明、探针清单校验范围和 README 共享包目录后，复检 PASS（clean）。版本生成检查和差异空白检查通过。

## 发布与独立探针

主代理从上述干净提交依次执行以下命令。手机清单六件产物、APK 版本和运行中的手机 HTTP MCP 哈希均通过核验。覆盖安装保留 App 私有数据，配对与准入检查通过。

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins 'http://127.0.0.1:3000'
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

两个 Windows exe 已替换，主机与手机清单同源，13 工具指纹均为 `6bf61b2727acf0adc0850b7b790534ec4fee8a159fa8266ada0c9fb74eb1eef6`。新 `build/mcp-probe.exe` 使用私有配置，分别以 `-transport stdio -binary build/device-mcp.exe` 和 `-transport http-remote` 执行 `-result-mode both -suite smoke -manifest build/release/manifest.json -manifest build/host-tools-manifest.json`。两次均 RESULT PASS，包括两清单匹配、观察、补看、动作、结束和取消。远端拒绝无认证和禁用 Origin。Windows 有旧映像仍被占用，手机检测到一个旧 stdio 进程。依赖方重启与重连由用户负责。

## 实机行为

主代理通过新 stdio 适配器以 both 模式调用工具，在内存中计实际树块的 UTF-8 字节，含末行换行。临时本地验证入口为 `python .state/contract26-baseline-check.py` 和带 `fixture` 参数的同一命令。

| 场景 | 实际结果 |
| --- | --- |
| 系统设置静止列表 | 修订 3 的 O7750_13 为 1322 字节 full。修订 4 至 9 均返回引用它的 43 字节 delta，跨度 5 和 6 仍命中，inspect(base) 成功。六次 observe 端到端耗时 0.523–0.659 秒。 |
| 受控页面进入第二页再返回 | 起始页 O7750_22 为 625 字节 full，第二页 O7750_23 为 268 字节 full，BACK 返回 O7750_24，delta 为 168 字节，引用跨 2 修订的 O7750_22。inspect(base) 返回该 O，两次动作耗时分别为 1.943 秒和 1.885 秒。 |

两次成功会话均调用 end。首次启动受控场景时返回 display_create_failed，未取得可用会话；重新 start 后完成闭环。耗时包含采集、投影、传输及动作，不单独估算算法增量。原始树、输入和私有配置未入库。
