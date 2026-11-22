# 树连续性、动作目标证明与文本受控提交验证

日期 2026-11-23。实现提交 `758f2e59e2f925814ce867decf90932b728d4d5e`。对照基线 `9245067183fede385d04ddc832de5d5514efed94`。交付 `0.29.0 / contract_version=25 / Android versionCode=31`。[归档提案](../proposals/implemented/tree-continuity-target-proof-and-text-submit.md)

## 已实现

- 树连续性增加跨包装容器锚定、重复资源容器保号与非输入叶子紧几何匹配。确认 full 基线池上限为 4，只在同会话、同显示代次、跨度不超过 4 个修订且年龄小于 180 秒时可用。主基线 delta 小于 full 的 70% 时直接采用。主基线不出 delta 时才试历史池，采用低于各候选 full 的 50% 的最小历史 delta。
- Ref 重验增加 Tier 4，在既有层级没有候选且祖先链失配时，要求强语义、紧几何、全树双向唯一，并按实际落点检查最深命中与竞争拥有者。能唯一定位的祖先失配可附 `changed_ref`。裸 `swipe` 对被动文案跳动使用独立的有界命中集合证明。
- `auto_submit` 默认开启。`submit_ref` 支持 `enter_key` 或公开节点。默认回车仅在旧绑定零候选时试一次写后重绑定。确定已写入且提交未派发返回 `partial / 409 text_submit_not_executed`，按原授权尽可能交付当前 ready O，并为重放保存 `observe(P, retired)` 的恢复基线。提交原语回执未知仍为 `uncertain`。

## 已单测及审计

| 核验 | 结果 |
| --- | --- |
| 对照提交上的先红复现 | 静止重复行身份从 `n3` 变为 `n5`。祖先重建后未变目标返回 `not_executed`。写确认且提交未派发返回 `uncertain`。三项在对照提交均按预期失败。 |
| `GOEXPERIMENT=jsonv2 go test -count=1 -timeout 15m ./cmd/... ./internal/... ./scripts/...` | 全部通过，引擎包 719.411 秒。 |
| 最终文本定向回归 | 全量后收紧撤权时的恢复基线、显示绑定失效和迟到回执投影。`go test -count=1 -timeout 15m ./internal/engine -run 'Test.*(Recovery\|Partial\|TextSubmit\|TextEnterRebind\|SubmitNotDispatched)'` 通过，61.689 秒。 |
| 静态检查与构建 | `go vet ./cmd/... ./internal/... ./scripts/...`、最终 Linux ARM64 `CGO_ENABLED=0 go build ./cmd/... ./internal/... ./scripts/...`、`./scripts/generate-version.ps1 -Check`、`./android-control/test.ps1`、`git diff --check` 均通过。 |
| 正式文档 | 独立只读审计覆盖 README 和本次变更的正式文档。三处修正后复检 clean。另按审计结论统一提交时基线池，历史候选超总留存预算时回退默认 full。 |

[基线窗口测试](../../internal/engine/baseline_pool_test.go) 覆盖 4/5 修订边界、显示代次、时间窗口及基线可读性。[目标证明测试](../../internal/engine/act_target_proof_test.go) 覆盖 Tier 4 正例、多个候选和外部遮挡拒绝。[文本提交测试](../../internal/engine/text_submit_engine_test.go) 覆盖写后缩框重绑定并只发一次 ENTER、歧义和显示变化拒绝、指定按钮点击及有界复采。[部分完成测试](../../internal/engine/text_partial_test.go) 覆盖写/提交事实、撤权、重放和附带 O 的实时资格。

## 发布与双入口独立探针

- `./scripts/build-release.ps1` 和 `./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` 通过。六个产物同源，`source_dirty=false`，APK 签名和版本一致。工具目录共 13 个，SHA-256 为 `6bf61b2727acf0adc0850b7b790534ec4fee8a159fa8266ada0c9fb74eb1eef6`。
- `./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins 'http://127.0.0.1:3000'` 通过。手机以 `adb install -r` 保留 App 私有数据。`/health` 通过发行号和契约校验，控制配对及准入检查通过。运行中的手机 HTTP MCP 二进制哈希等于发布清单。
- `./scripts/build-host-tools.ps1` 和 `./scripts/build-host-tools.ps1 -VerifyOnly` 通过。Windows `device-mcp.exe` 和 `mcp-probe.exe` 替换到磁盘，两件产物和手机发布清单同源。磁盘替换时旧 Windows 进程仍在使用原映像，脚本保留了原映像备份。手机也检测到旧 stdio 进程。依赖方宿主切换由用户处理。
- 本次 `mcp-probe.exe` 分别以 `-transport stdio -binary build/device-mcp.exe` 和 `-transport http-remote` 使用私有连接配置运行。两次均使用 `-result-mode both -suite smoke -manifest build/release/manifest.json -manifest build/host-tools-manifest.json`，均为 `RESULT PASS`。两份目录哈希匹配，schema 的 `auto_submit`、`submit_ref` 与在册源声明一致。`result v4`、树块、`start` → `observe` → `inspect` → `act` → `end` 和取消路径均通过。远端额外验证了 401、403、允许 Origin 及 `contract=25`。

## 已实测

Lenovo 24117RK2CC，Android API 36，虚拟屏 `1080×2400`。主代理用部署后的 Go 服务 `/api/<tool>` 执行动作与 `inspect`，从手机事件中只摘录操作编号和白名单事实。未提交原始设备数据。四个验证会话最终均为 `ended / cleanup=complete`，准入恢复 `can_start=true`。

| 场景 | 实际输出 |
| --- | --- |
| 受控验证页 `com.mobileuse.probe` 0.1.0 | `P7205_5` 回返 `O7205_15`，`delta base=O7205_13`，相隔 2 个修订，事件 `baseline_pool=historical`，原按钮 ref 保留且 `inspect(base)` 可读。旧成员 `press_enter` 返回 `422 invalid_request`。`P7205_6` 明确指定按钮，`write=executed / submit=executed`，计数 `0→1`。`P7205_7` 带有效语法但不存在的 ref 且 `auto_submit=false`，精确写入，计数仍为 1，`text_input` 省略。 |
| #29 知乎 10.86.0，静止搜索首页 | `O7205_50` full 后两次连续观察 `O7205_51` 和 `O7205_52` 均为 `delta base=O7205_50 changes=0`。17 个 `search_find` 行的 ref 按顺序完全一致。初次加载期间曾看到条目增加，稳定后复测得到上述结果。 |
| #29 知乎视频列表往返 | 列表 `O7205_56` 修订 26，播放页修订 27/28。`P7205_52` BACK 得 `O7205_59` 修订 29，`delta base=O7205_56 changes=0`，`baseline_pool=historical`，原视频条目 ref 保留，`inspect(base)` HTTP 200。 |
| #29 B 站 6.56.0，搜索列表往返 | 列表 `O7205_107` 修订 13，播放页修订 14/15。`P7205_108` BACK 得 `O7205_110` 修订 16，`delta base=O7205_107 changes=1`，`baseline_pool=historical`，原视频条目 ref 保留，`inspect(base)` HTTP 200。 |
| #36 知乎播放中裸 `swipe` | `P7205_57` 从倒计时被动文本所在坐标发出裸手势，`outcome=executed`，点证明 `state=unique proven=true`。手势前倒计时为 `02:14`，随后观察到同页为 `01:58`。 |
| #36 知乎回答底栏 | `P7205_86` 点“赞同”，立即使用交付 O 以 `P7205_87` 点“收藏”。两次均 `executed`，日志均 `act.target outcome=proven tier=1`。随后撤回测试收藏和赞同。 |
| #30 网易云音乐 9.5.61，默认回车 | `P7205_16` 由已聚焦搜索框触发默认提交，HTTP 200，`write=executed / submit=executed`，观察到搜索结果分类，事件确认写入和提交均已派发。 |
| #30 网易云音乐，显式提交与草稿 | `P7205_19` 用“搜索”文本子节点作 `submit_ref`，两项均 `executed` 并进入结果页。`P7205_22` 带合法但不存在的 `submit_ref` 且 `auto_submit=false`，HTTP 200，现场输入框读回全文，仍处在搜索输入页，省略 `text_input`。 |

## 证据边界

- #29 原报告 `O85 → O90` 跨 5 个修订，按确认的窗口仍为 full。实机证明了 3 个修订内的历史回返，4 和 5 的边界由引擎测试覆盖。
- 本次知乎样本未触发 Tier 4。祖先重建下的 Tier 4 与 `changed_ref` 由受控 Go 样本验证。网易云日志确认默认提交成功，现有事件无法区分旧绑定直读和写后重绑定。真实 `partial` 与提交回执未知未构造，由引擎、HTTP 与 MCP 测试覆盖。
- 实机验证覆盖本机虚拟屏。依赖方进程实际重连、主屏与其他机型不在本次实机记录中。
