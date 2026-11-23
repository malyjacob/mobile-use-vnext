# 受控滚动轴向兼容回退与四档幅度验证

实现提交 `38c7b8258d30398cd5e5bfb016fb6964b091eee2`（分支 `master`，基线 `8c494a35179cb6d27e139c5d60e98f37d4229505`）。Windows amd64、Go 1.27.1、`GOEXPERIMENT=jsonv2`；手机 `24117RK2CC`、Android API 36、虚拟屏 `1080×2400`。发行 0.31.0，契约 27，versionCode 33。涉及实机：是。[归档提案](../proposals/implemented/scroll-axis-fallback-and-amount-tiers.md)

分诊为取舍，提案经用户确认后实施；本轮无跳过步骤。

## 测试与审查

### 已单测（可复跑）

- 主代理执行 `go test -count=1 -timeout 15m` 覆盖除 `internal/engine` 外的 15 个包（`cmd/... ./internal/... ./scripts/...`）：全部 `ok`。其中 `internal/android` 15.6s、`scripts/mcp-probe` 21.4s、`internal/mcp` 7.1s、`internal/observation` 4.6s、`internal/protocol` 3.1s。
- 主代理执行 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`（exit 0）与 `go vet ./...`（exit 0）。
- `go run ./scripts/toolcatalog` → `{"count":13,"sha256":"3a9ad87c7af904226731f9fec90b0def7b2d8233487fdf7435041e365211b98b"}`，与 `docs/mcp.md` 记录的当前源码指纹一致。
- 引擎包全量由实现子代理执行 `go test ./internal/engine -count=1 -timeout 15m` → `ok ... 758.062s`（exit 0）。按用户当次指示，主代理不重跑该包。
- 主代理亲自复跑变更相关的定向回归：`go test ./internal/engine -run 'TestIssue42|TestScrollAxisIssue' -count=1 -timeout 10m` → `ok ... 13.994s`（9 个测试函数、10 个用例；独立测试作者另用 `-shuffle=on` 跑过 13.993s）。
- 新增测试：`internal/observation/scroll_axis_test.go`（类名/子项排布/16 项上限/context/非 scroll 角色，含反向对照）、`internal/engine/scroll_axis_amount_test.go`（四档阈值与时长、C 判据谓词与三路径回退、A 判据豁免、内层兼容过滤、坐标消歧、认领后轴向重验）、`internal/engine/scroll_axis_issue42_test.go`（独立作者端到端回归：Tab 条 down 回退、right 直用、无兼容父容器拒绝且不消费 O、非滚动子节点提升、仅横向祖先拒绝、坐标回退、未知轴扁平条回退、禁止误回退、候选 executable 与同 O 重试）、`internal/protocol` 四档编解码与非法值、`internal/mcp` 四档 schema/描述与字节预算、`scripts/mcp-probe` 两档 enum 负向校验。
- 反向对照：独立测试以“旧实现会派发 55px/61px 迷你纵向滑动、实际选中外层 1008px/744px 行程”的数值性质表达；实现者单测对 C 判据与回退 mode 做谓词级反向对照（去掉守卫即红）。`amount` 非法值（`wide`/`50%`/`15`/`null`/大小写变体）在解码与引擎入口均 `422 invalid_request`。

### 已审（独立只读）

- 正式文档审计按 AGENTS 第 15 条由独立、只读子代理执行，覆盖 10 个变更文件。首轮 `NOT CLEAN` 1 项：`docs/interface.md` 对 `candidates` 附加范围的断言过强（`root`/`context`、`409 node_not_found`/`node_disabled`、坐标越界不附候选；`no_safe_trajectory`/`ambiguous_point` 使用 `child`/`overlapping`、`competing`/`ancestor`）。另有三条观察项（`axis` 求值顺序、`mcp.md` 缺 `firstCompatErr` 分支、`axis=both` 无投影来源）。
- 修正后二次复检 `CLEAN`（F1 逐条对上 `internal/engine/scroll.go` 的 attach 调用点与 `attachStaticCandidates` 的空候选行为；三处观察项已按建议对齐）。提交说明记录“文档审计：CLEAN”。

## 发布与独立探针

从干净实现提交 `38c7b82` 执行：

```powershell
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/deploy.ps1 -SkipBuild -Manifest build/release/manifest.json -McpAllowedOrigins 'http://127.0.0.1:3000'
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

- 发布清单：`release=0.31.0 contract=27 android=0.31.0/33 artifacts=6`，`source_commit=38c7b8258d30398cd5e5bfb016fb6964b091eee2 source_dirty=False`，工具目录 `13/3a9ad87c…b98b`。`verify-manifest` `PASS`（HEAD 与清单一致）。
- 整组覆盖部署 `PASS`：`device-service`、`u2.jar`、`applist.dex`、`service.sh`、`config.json`、`device-mcp`、`mcp-config.json`、`connection.json` 逐件 SHA-256 与清单一致；`adb install -r` 覆盖安装后 App 为 `versionName=0.31.0 versionCode=33`（保留 App 私有数据）；`/health` `release=0.31.0 contract=27`；`can_start=true`（配对、恢复屏障与 STOP 可用性均 PASS）；手机 HTTP MCP 适配器以清单同哈希运行并可达。
- 主机工具磁盘替换：`build/device-mcp.exe`（`d75883e4…`）与 `build/mcp-probe.exe`（`3a33c240…`）覆盖写入，`build/host-tools-manifest.json` `PASS` 且与发布清单 companion 校验通过。
- 独立进程探针（新产物，私有配置）：
  - 主机 stdio：`build/mcp-probe.exe -config .state/mcp-config.json -transport stdio -binary build/device-mcp.exe -result-mode both -suite smoke -manifest build/release/manifest.json -manifest build/host-tools-manifest.json` → `RESULT PASS`；含 `act_scroll_schema`、`catalog_fingerprint 3a9ad87c…`、双清单 `release=0.31.0 contract=27`、instructions 11719 字节与源一致、start/observe/snap/wait_for/shell/inspect/act/end/cancel 全 PASS。
  - 手机 HTTP：同配置改 `-config .state/mcp-remote.json -transport http-remote` → `RESULT PASS`；`origin_allowed`、`agent_token_rejected`、目录/指纹/清单与全部工具面检查同上。
- 依赖方切换未执行（按分工由用户处理）：部署提示手机侧仍有 1 个旧 stdio 适配进程占用旧二进制；托管 MCP 宿主需要重启才能加载新工具描述与 schema。

## 实机行为

虚拟屏 `1080×2400`，经 HTTP `/api/*`（`X-Mobile-Use-Contract: 27`）驱动；`start` 后依次打开哔哩哔哩与知乎。

1. **`axis` 投影（已实测）**：哔哩哔哩首页公开树中，顶部 Tab 条 `n30` 为 `axis=x`（`0,279,985,380`），横向分页容器 `n46` 为 `axis=x`（`0,380,1080,2400`），横向轮播 `n53` 为 `axis=x`（`20,403,1060,988`），其内纵向容器 `n54` 为 `axis=y`。知乎首页 `n175` Tab 条 `axis=x`、`n204` 横向分页 `axis=x`、`n214` 纵向列表 `axis=y`。
2. **轴向失配静态拒绝（已实测）**：`scroll ref=n30, direction=down, amount=page` → `outcome=not_executed`、`422 unsupported_node_action`、`reason=axis_mismatch`、`scope=target`、`scope_ref=n30`、`target_actions=["swipe"]`；`candidates` 中 `n50` 与 `n54` 为 `executable=true`，横向的 `n46`/`n53` 为 `executable=false`；零 Swipe、O 未被消费（随后同 O 请求 `right` 成功）。
3. **横向方向正常使用（已实测）**：同 Tab 条 `direction=right, amount=half` → `executed`、`effect.state=observed`；Tab 按钮真实左移（`直播` `0,279,146,380` → `0,279,127,380`，`推荐` 起点 `146` → `127`）。
4. **#42 核心：横向容器上的纵向请求自动回退并产生真实位移（已实测）**：哔哩哔哩填充流中横向轮播 `n415`（`axis=x`，`20,403,1060,988`）内嵌于纵向填充流 `n412`。`scroll ref=n415, direction=down, amount=page` → `executed`、`effect.state=observed`；随后 full 观察中填充流整体上移（图片 `Top` 从 `1005/1559/2113` 变为 `222/703/1257`），delta 携带 `set` 边界变化与 `add`/`remove`，证明手势落在外层纵向容器而非 140px 高的横条内。
5. **四档幅度（已实测，幅度不承诺等于注入行程）**：对填充流 `n412` 连续执行 `amount=step` 与 `amount=full`（均 `executed`）。`step` 后同一批卡片仍可见、内容上移约 `456px`（`n479` `Top 703→247`、`n616` `1257→801`、`n647` `1811→1355`），`effect.state=observed`；`full` 后视口内容整体更换（图片数 `8→6`、仅一个旧引用以不同内容出现），`effect.state=unknown`。服务端注入行程仍由单测固定为可用跨度 `15%/85%` 与 `500/450/400/300ms`；实机位移包含 Android 嵌套滚动/抛滑，契约不承诺实际位移等于注入行程。
6. **坐标路径与参数拒绝（已实测）**：点 `(492,172)` 落在 Tab 条上请求 `down` → `422 unsupported_node_action`、`reason=axis_mismatch`、`scope=point`（按契约省略 `scope_ref`），`candidates` 中 `n412` `executable=true`、`n408` `executable=false`。`scroll` 携带 `duration_ms` → `422 invalid_request`（`scroll 只接受 container、direction 与 amount`）。
7. **收尾（已实测）**：`end` 后 `cleanup.state=pending → complete`，`execution/tasks/recents/display` 均 `confirmed`，`can_start=true`。

## 未执行项与原因

- 实机只覆盖 `down`/`right`；`up`/`left` 的对称路径由 `scrollContainerAxisCompatible` 谓词单测与回退用例覆盖，未做实机。
- 多层连续跳级回退（横向条外套横向分页再套纵向列表）、`firstCompatErr` 优先返回、`scroll_inner_fallback` 与 `scroll_point_disambiguated` 的实机样本未构造；前三者由单测覆盖，后者在实现者单测中覆盖。
- `axis=both` 无投影来源（推断器只产生 `x`/`y`/省略），契约值域由既有解码测试覆盖。
- MCP `result v4` 文本中的 `axis` 未在真实宿主会话逐字人工比对；文本渲染由 `internal/treetext` 单测与实机 HTTP 公开树的 `axis` 字段共同覆盖。
- 手机侧旧 stdio 适配进程与主机 MCP 宿主重启、重连由用户处理，不计入本记录；独立探针只证明本次新产物与入口。
