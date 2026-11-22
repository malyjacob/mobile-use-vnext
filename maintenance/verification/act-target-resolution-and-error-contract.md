# 统一 `act` 静态目标解析策略与错误返回契约验证记录

2026-11-22（Asia/Shanghai）。涉及手机实机。实现提交为
`b305e23675d1b55c2f05ca8a904e579e400c72fd`（短 hash `b305e23`），一次落盘代码、单测、
版本源、生成常量与正式文档同步；提案原稿随该提交落盘时仍为“提案，未实现”。发行 `0.26.0`，
共同契约 `22`，Android versionCode `28`。设计见[归档提案](../proposals/implemented/act-target-resolution-and-error-contract.md)。

本次结论按层面分开，不互相替代：

| 层面 | 结论 |
| --- | --- |
| 已实现 | 契约 22 静态 `422` 统一错误契约（新增 `no_safe_trajectory`、统一 `candidates`/`blocking_refs`、零旧键）、`scroll` 双向同视口栈/祖先提升/居中 3 轴线与坐标消歧、`tap`/`long_press`/`text(focus)` 4 级规划（含 `grid_3x3`）+ `coextensive_child`/`bubble_ancestor`/`bubble_colocated` 双绑定、坐标 `pointSet` 与 raw `swipe` 豁免、非 `text` `409 target_changed` 9 项 `reason`、MCP schema/指引更新、9 份 `docs/` 与 `README.md` |
| 已单测 | 全量 Go 包测试（含 `internal/engine` 全套 `619.425s`）、`go vet`、`android-control` 单元测试、`linux/arm64` 交叉编译与版本源一致性校验 |
| 已实测 | 手机整组覆盖部署（保留 App 私有数据）、`/health`、App 配对、恢复屏障、STOP、手机 HTTP MCP、双入口独立探针、双入口虚拟屏实机 `act` 契约 22 最小闭环 |
| 磁盘替换 | Windows `build/device-mcp.exe` 与 `build/mcp-probe.exe` 已是本次 `0.26.0` 产物并经 `-VerifyOnly` 校验通过 |
| 独立进程验证 | 新 `build/mcp-probe.exe` 与私有闭环脚本分别独立驱动 Windows 主机 stdio 子进程与手机运行中的 HTTP MCP |
| 宿主重连交接 | 未执行。依赖 Agent 的托管 MCP 进程重启（含磁盘备份的旧主机 `device-mcp.exe` 与手机上 1 个旧 stdio 进程）与工具重新发现由用户处理 |

## 1. 实现与独立只读文档审计

本轮落实提案 `act-target-resolution-and-error-contract.md` 的全部核心改动，共同契约由 `21`
推进至 `22`（`release_version=0.26.0`、`android_version_code=28`），13 工具目录指纹更新为
`a18acde933ad4358ee0b6d5ec28ca2e4dcba643e33453de31c436763e561c54e`。

独立只读文档审计（`AGENTS.md` #15、#17）审阅 `README.md` 与 9 份受改 `docs/` 文档。首次发起的
`docs_audit_v22`（模型 `auto/deepseek-flash`）因模型路由故障持续停留在 `pending_init` 超过 35
分钟未进入运行，按第 17 条记录原因并在不中断原任务的前提下，改由 `docs_audit_v22_final`（模型
`ollama-cloud/deepseek-v4.1-flash:1017`，思考强度 `max`）执行独立只读审计。初审返回 9 项发现：

| 发现 | 位置 |
| --- | --- |
| 13 工具目录指纹仍为 `0.25.0` 旧值 | `docs/mcp.md:18` |
| `bubble=false` 误写为同时禁用直接目标的 `coextensive_child` | `docs/architecture.md:209`（同步澄清 `docs/interface.md:478`、`docs/mcp.md:52`） |
| `bubble_colocated` 选点门槛漏写先测 `rect(C) ∩ B.base` 局部视觉点再回退直接 4 级 | `docs/interface.md:464`、`docs/architecture.md:205` |
| “严格包含中心点”与半开 `Rect.Contains`（左/上包含、右/下不包含）语义不符 | `docs/interface.md:467` |
| `scroll_scope_changed` 含义写反（“超出轴向重叠 ≥0.50”） | `docs/interface.md:669` |
| 坐标保持原点授权句遗漏 `long_press` | `docs/interface.md:656` |
| 静态拒绝概括漏列 `409 node_not_found`/`node_disabled` 且把可选 `candidates` 写得近似必有 | `docs/mcp.md:56` |
| 非 `C` 后代面积上限存在逻辑分组歧义 | `docs/interface.md:464`、`docs/architecture.md:204-205` |
| `probeRect` 与同段正式变量名 `cClip` 不一致 | `docs/architecture.md:202` |

全部 9 项发现通过运行 `GOEXPERIMENT=jsonv2 go run ./scripts/toolcatalog` 刷新目录指纹并最小修正
`docs/mcp.md`、`docs/architecture.md`、`docs/interface.md`（未改源码）后，交由同一独立审计员
`docs_audit_v22_final` 复检，返回 **`AUDIT_RESULT: CLEAN`**。

## 2. 单元测试、静态检查与交叉编译

环境为 Windows x64、Go 1.27（`GOEXPERIMENT=jsonv2`）、JDK 17。全部验证在实现提交前完成：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test -count=1 ./cmd/... ./internal/android ./internal/client ./internal/device ./internal/eventlog ./internal/httpapi ./internal/imaging ./internal/mcp ./internal/observation ./internal/protocol ./internal/store ./scripts/mcp-probe
go test -count=1 -timeout 20m ./internal/engine
go vet ./cmd/... ./internal/... ./scripts/...
.\android-control\test.ps1
.\scripts\generate-version.ps1 -Check
$env:GOOS = 'linux'; $env:GOARCH = 'arm64'; $env:CGO_ENABLED = '0'
go build ./cmd/... ./internal/... ./scripts/...
```

实际结果：

- 非引擎 Go 包全部 PASS（`-count=1` 无缓存，覆盖 `protocol`、`httpapi`、`client`、`mcp`、`observation`、`imaging`、`store`、`android`、`device`、`cmd/device-mcp`、`scripts/mcp-probe`）。
- `internal/engine` 全量套件（含 4 个新增提案测试文件与 7 份既有测试适配）：`ok github.com/malyjacob/mobile-use-vnext/internal/engine 619.425s`（0 failures）。
- `go vet ./cmd/... ./internal/... ./scripts/...`：退出码 0，无告警。
- `.\android-control\test.ps1`：`DisplayAttemptGateTest passed`、`MessageListTest passed`。
- `.\scripts\generate-version.ps1 -Check`：`version source consistent: release=0.26.0 contract=22 android_version_code=28`。
- `linux/arm64` 交叉编译 `go build ./cmd/... ./internal/... ./scripts/...`：退出码 0。

## 3. 整组构建、双清单核验与主机工具重建

在干净实现提交 `b305e23675d1b55c2f05ca8a904e579e400c72fd`（`source_dirty=False`）上执行：

```powershell
.\scripts\build-release.ps1
.\scripts\verify-manifest.ps1 -Manifest build/release/manifest.json
.\scripts\build-host-tools.ps1
.\scripts\build-host-tools.ps1 -VerifyOnly
.\build\device-mcp.exe -version
```

全部 PASS。两份清单记录同一源码提交 `b305e23675d1b55c2f05ca8a904e579e400c72fd`、发行 `0.26.0`、
契约 `22` 与 13 工具目录指纹 `a18acde933ad4358ee0b6d5ec28ca2e4dcba643e33453de31c436763e561c54e`：

| 清单与产物 | 大小（字节） | SHA-256 |
| --- | --- | --- |
| `build/release/device-service` | `9175200` | `490dcc6e2ac14a323d2515cf0d97c4e1a2f7352c12419ef7ebcda58bd079b396` |
| `build/release/device-mcp` | `9371808` | `de022265dcddd66d2e2454a13a6ac736c9473aca44123b60f1c490c0488ac69e` |
| `build/release/control.apk`（`0.26.0`/`28`，APK v3 签名验证通过） | `103965` | `81abf18ea23d1736260903950143aaebe49e5675f5fe01573ea6c51948d92035` |
| `build/release/u2.jar` | `3306229` | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` |
| `build/release/applist.dex` | `4316` | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` |
| `build/release/service.sh` | `3643` | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` |
| `build/device-mcp.exe`（`-version` 输出 `0.26.0`） | `10260992` | `2bb133e65074c251e996b49bd356c8a78ce6be0966dc9d97e9b9af0874f220bd` |
| `build/mcp-probe.exe` | `9958400` | `feed58267165847e152d241e70e6059b3b26f2c8052aec1ab83cc45ad9e11d8e` |

说明：重建主机工具时磁盘原 `build/device-mcp.exe` 正被外部运行中的宿主进程占用，构建脚本已按安全策略
将其重命名保留为 `build/.device-mcp.exe.previous-42a48154a8ad4a8590cd9531d1b0dbaa` 并将新产物替换到
目标路径；`-VerifyOnly` 对伴随清单 `build/release/manifest.json` 及两个 `.exe` 校验均 PASS。

## 4. 手机整组覆盖部署

```powershell
.\scripts\deploy.ps1 -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'
```

设备 `24117RK2CC`（代号 `zorn`，序列号 `17efb04a`，Android 16，`1080×2400`，virtual 模式）整组覆盖部署
PASS：

- 设备侧 `device-service`、`u2.jar`、`applist.dex`、`service.sh`、`config.json`、`device-mcp`、
  `mcp-config.json`、`connection.json` 与运行中的手机 HTTP `device-mcp` 实际 SHA-256 与清单逐项 `match`。
- `adb install -r` 覆盖安装控制 App 保留私有数据：`firstInstallTime=2026-11-09 03:45:47`、
  `lastUpdateTime=2026-11-22 09:51:57`、`versionName=0.26.0`、`versionCode=28`。
- 部署后检查：`/health` 返回 `release=0.26.0 contract=22`；App 配对 PASS；恢复屏障 PASS；STOP 可用性
  PASS；手机 HTTP MCP 监听 `127.0.0.1:18882`（主机转发 `http://127.0.0.1:18883/mcp`）PASS。
- 部署日志提示手机上仍有 1 个旧版 `device-mcp` stdio 进程（由既有外部宿主拉起），待用户重启宿主后切换。

## 5. 双入口独立探针

用本次新建的 `build/mcp-probe.exe` 分别针对主机 stdio 与手机 HTTP MCP 执行独立探针（私有配置位于
`.state/act-target-resolution-and-error-contract/`，gitignore 排除、未入库）：

```powershell
.\build\mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/act-target-resolution-and-error-contract/stdio.private.json -result-mode both -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
.\build\mcp-probe.exe -transport http-remote -config .state/act-target-resolution-and-error-contract/http-remote.private.json -result-mode both -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
.\build\mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/act-target-resolution-and-error-contract/stdio.private.json -result-mode text -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

| 执行次序与入口 | 结果 | 关键实测事实 |
| --- | --- | --- |
| 首次：主机 stdio，`both` | `FAIL`（重启窗口 `start=control_revoked`） | `connect`/`instructions(11661)`/`tools(13)`/双清单指纹/`act_scroll_schema`/`schema`/`status(can_start=true)`/`result_error` 全部 PASS；`start` 在服务刚重启窗口命中已知 `control_revoked` |
| 手机 HTTP MCP，`both` | `RESULT PASS` | `contract=22 release=0.26.0`；`auth_missing=401`、`origin_denied=403`、`origin_allowed=200`、`agent_token_rejected=401`；`instructions=11661`；13 工具指纹与双清单 match；`P1895_*` / `O1532_1..4`（`display_id=159`）；三处附图 `461×1024`；`app`/`observe`/`snap`/`wait_for`/`shell`/`timeline`/`inspect`/`act`/`end`/`cancel` 全绿 |
| 主机 stdio，`text` | `RESULT PASS` | `instructions=11661`；13 工具指纹与双清单 match；`P5339_*` / `O1532_6..9`（`display_id=161`）；三处附图 `461×1024`；全套工具与 `cancel` 全绿 |
| 重跑：主机 stdio，`both` | `RESULT PASS` | `instructions=11661`；13 工具指纹与双清单 match；`P2712_*` / `O1532_11..14`（`display_id=163`）；三处附图 `461×1024`；全套工具与 `cancel` 全绿 |

独立探针只证明本次磁盘产物与两个运行入口的现状，不证明外部依赖宿主已重连切换。

## 6. 实机最小 `act` 契约 22 闭环

私有闭环脚本 `.state/act-target-resolution-and-error-contract/v22_e2e_check.go`（gitignore 下，不入库）
以 `result_mode=both` 分别经独立的 Windows `build/device-mcp.exe`（stdio）与手机 HTTP MCP
（`http://127.0.0.1:18883/mcp`）在虚拟屏系统设置（`com.android.settings`）执行同一套 8 步闭环，两侧均
完整 PASS（主机 stdio：`O1532_34..45`；手机 HTTP：`O1532_48..59`）：

1. **非滚动同视口外包装静态 `422` + 同 `O` 零重观察重试**：对包裹 `role=scroll`（`n17`）的同视口无动作外包装
   `n16` 执行 `scroll(down)`，返回 `422 unsupported_node_action`（`reason=not_scrollable`、`outcome=not_executed`），
   `structured` 与 `result v4` 文本 `error.details` 均携带 `candidates` 且零旧键，首项 `candidates[0]` 为
   `ref=n17`、`relation=page_scroll`、`executable=true`；原 `O`（`O1532_34` / `O1532_48`）保持 `ready`。
   直接取 `candidates[0].ref` 用**同一 `O`** 重试 `scroll(down)`，返回 `outcome=executed`、`effect=observed`。
2. **非滚动 `ref` 祖先提升**：对滚动列表内无动作文本叶节点（`n61`）执行 `scroll(up)`，自动向上提升到滚动容器
   并执行成功（`outcome=executed`、`effect=observed`）。
3. **坐标居中滚动**：对列表内与可点击行/文本重叠的坐标 `(540, 1200)` 依次执行 `scroll(down)` 与 `scroll(up)`，
   均一次 `outcome=executed`、`effect=observed`。
4. **禁用节点 `409 node_disabled` 保 `O`**：对已禁用文本节点（`n180`）执行 `tap`，返回 `409 node_disabled`、
   `outcome=not_executed`，原 `O`（`O1532_38` / `O1532_52`）未被认领。
5. **`bubble=false` 静态 `422` + 同 `O` 零重观察重试**：在保持同一 `O` 的前提下对可点击行内无动作文本（`n185`）
   执行 `tap(bubble=false)`，返回 `422 unsupported_node_action`（`reason=target_missing_action`），零旧键，
   `candidates[0]` 为 `ref=n182`、`relation=ancestor`、`executable=true`；直接用**同一 `O`** 点击
   `candidates[0].ref` 返回 `outcome=executed`，随后 `BACK` 返回主页。
6. **`bubble=true`（默认）视觉锚定提升点击**：直接对无动作子文本（`n281`）执行 `tap`（省略 `bubble`），
   自动命中所属可点击行（`n278`）返回 `outcome=executed`，随后 `BACK` 返回主页。
7. **重叠节点坐标点击（`pointSet` + 动作拥有者证明）**：对文本与可点击行重叠的中心坐标 `(219, 560)` 执行
   `tap`，直接返回 `outcome=executed`，随后 `BACK` 返回主页。
8. **raw `swipe` 免 `ambiguous_point`**：从重叠节点坐标 `(219, 1400)` 向 `(219, 900)` 执行 `swipe(duration_ms=300)`，
   直接返回 `outcome=executed`；随后 `end` 收尾，`status` 确认 `can_start=true`。

## 7. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 依赖 Agent（宿主）既有托管 MCP 进程重启与工具重新发现 | 未执行。按 `AGENTS.md` #19 与提案 §8.2 由用户处理；本记录明确区分磁盘替换、独立进程验证与宿主切换。 |
| 真机第三方应用上的双向同视口双层滚动栈、跨兄弟 `bubble_colocated`、多候选 `422 ambiguous_point`、`422 no_safe_trajectory`、`409 target_changed` 9 项 `reason` 与 `primary` 模式 | 未在真机第三方页面单独构造；相关算法、拓扑环守卫、双绑定重验与全部错误码/归因矩阵均已由 `internal/engine` 全量单元测试（`619.425s`，0 failures）覆盖，本次实机按提案 §8.2 聚焦虚拟屏最小闭环与双入口探针。 |
