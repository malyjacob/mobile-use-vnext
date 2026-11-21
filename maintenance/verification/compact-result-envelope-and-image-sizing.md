# 压缩结果外壳、短 O/P 标识与 1024 截屏验证记录

2026-11-22（Asia/Shanghai）。涉及手机实机。实现提交为
`fcf6ca2430ab885c7e4513b300a6b0de11885ea9`（短 hash `fcf6ca2`），一次落盘代码、单测、探针、
版本源与正式文档同步；提案原稿随该提交落盘时仍为“提案，未实现”。发行 `0.25.0`，共同契约
`21`，Android versionCode `27`。设计见[归档提案](../proposals/implemented/compact-result-envelope-and-image-sizing.md)。

本次结论按层面分开，不互相替代：

| 层面 | 结论 |
| --- | --- |
| 已实现 | `result v4` 紧凑外壳、短 `O`/`P`、`recovery` 精确 `ref`、`ImageMaxLongSide=1024`、探针解析器、8 份 `docs/` 与 `README.md` |
| 已单测 | 全量 Go 测试、五类反向对照、两处防御性兜底与四条恢复红线 |
| 已实测 | 手机整组覆盖部署、`/health`、App 配对、恢复屏障、STOP、手机 HTTP MCP、双入口独立探针、实机 `result v4` 最小闭环 |
| 磁盘替换 | Windows `build/device-mcp.exe` 与 `build/mcp-probe.exe` 已是本次 0.25.0 产物 |
| 独立进程验证 | 新 `mcp-probe.exe` 分别驱动主机 stdio 子进程与手机运行中的 HTTP MCP |
| 宿主重连交接 | 未执行。依赖 Agent 的托管进程重启与工具重新发现由用户处理 |

## 1. 实现与正式文档审计

分诊为格式与尺寸取舍，交付确认的三条路线全部落地：`text`/`both` 与 `structured` 正式分层
（`result v4`），真实短标识 `O<4 位>_<序号>` / `P<4 位>_<序号>`，以及 `recovery` 的路线 R1
单次调用无状态确定性解析。`/api/*`、`structured` 与 `diagnostic v1` 未改，共同契约保持 `21`；
普通树沿用 `tree v4`，详细补看沿用 `inspect v3`。

独立只读审计（`AGENTS.md` #15）审阅 `README.md` 与 8 份受改 `docs/`，并抽查
`docs/phone-operations.md`。初审提出 5 项发现：

| 发现 | 位置 |
| --- | --- |
| `observation.screen` 省略条件写反 | `docs/mcp.md:309` |
| `app_items kind=list` 的非整数 `origin_display` 被静默跳过 | `internal/mcp/result_text.go:1331-1337` |
| 合成异常态会产出裸 `obs` 行 | `internal/mcp/result_text.go:957-960` |
| 期限公式表述与实现不一致 | `docs/architecture.md:111-112` |
| `snap` 的 `image` 行漏写可选 `[at=<HH:MM:SS>]` | `docs/mcp.md:123` + `internal/mcp/schema.go:247` |

复检时又发现 `docs/integration.md:430` 有同类漏写，合计 6 项。修正内容：两处代码兜底分别改为
输出 `json v1 path="/app/items/<i>"` 整值回退，以及移除 `len(parts)==0` 时的裸 `obs` 输出分支
（成员级 `json v1` 已兜底时不再写裸 `obs` 行）；说明与两份文档补齐 `[at=<HH:MM:SS>]`；`docs/mcp.md`
改回正确省略条件；`docs/architecture.md` 公式按实现重写。同步补回归单测
`TestResultV4DefensiveFallbackNoDropAndNoBareObs`，并按新描述重算工具目录指纹。同一审计者
终检返回 **CLEAN**。

## 2. 单元测试、反向对照与交叉编译

环境为 Windows x64、Go 1.27（`GOEXPERIMENT=jsonv2`）、JDK 17。主代理执行：

```powershell
$env:GOEXPERIMENT='jsonv2'
go test -timeout 45s ./cmd/... ./internal/android ./internal/client ./internal/device ./internal/eventlog ./internal/httpapi ./internal/imaging ./internal/mcp ./internal/observation ./internal/protocol ./internal/store ./scripts/... -count=1
go test -timeout 600s ./internal/engine -count=1
```

第一组全部 PASS，其中 `internal/mcp` 0.665s、`scripts/mcp-probe` 11.904s、`internal/client`
0.358s、`internal/imaging` 0.592s。`internal/engine` 的 483 个测试运行 561.903s，482 个 PASS；
唯一失败为 `TestContractVersionMatchesGenerated`，报 `source="0.25.0" compiled="0.24.0"`，
原因是该轮在版本源升到 `0.25.0` 之前已开始编译。版本常量生成后单独复测
`go test ./internal/engine -run TestContractVersionMatchesGenerated -count=1`，PASS（0.051s）。

审计修正后复跑：

```powershell
go test ./cmd/... ./internal/client ./internal/imaging ./internal/mcp ./internal/protocol ./scripts/mcp-probe -count=1
go vet ./cmd/... ./internal/... ./scripts/...
$env:GOOS='linux'; $env:GOARCH='arm64'; $env:CGO_ENABLED='0'
go build ./cmd/... ./internal/... ./scripts/...
.\scripts\generate-version.ps1 -Check
.\android-control\test.ps1
```

定向 Go 测试、`vet`、交叉构建与版本生成校验均 PASS；Android 侧
`DisplayAttemptGateTest passed`、`MessageListTest passed`。

反向对照均由测试断言，不靠人工判断：

| 反向对照 | 覆盖内容 |
| --- | --- |
| `TestResultV4ManifestReverseControl` | 新增未分类的 `protocol.Result` 成员必须让门禁失败（投影集合 + 显式省略集合 = 全量成员） |
| `TestRecoveryExactReferenceMatrix` 及红线用例 | 四条安全红线：跨 Agent `device_busy`、`observation_not_found`、无 `session` 的 `session_ended`、被拒的 `start` 均回退抽象 `P`；`act` 的 `uncertain` 与 `executed_revoked` 带 `session` 时不得误填 `session.origin` |
| `TestRestartAvoidsCollidingInstanceTag` | 刻意把两代服务实例构造成同一 4 位派生标签，载入旧账本后经 `StartRecovery` 确定性避让，不复用旧 `O`/`P` |
| `TestEncodeDefaultLongSideIs1024` | `1080×2400 → 461×1024`、`2400×1080 → 1024×461`、`800×600 → 800×600`（不放大） |
| `TestDiscoveryDropsResultV3AndObservationScreenPath` | 发现说明不得残留 `result v3` 与 `observation.screen.display_id` 过时指引 |

## 3. 构建、清单核验与主机磁盘替换

```powershell
.\scripts\build-release.ps1
.\scripts\verify-manifest.ps1 -Manifest build/release/manifest.json
.\scripts\build-host-tools.ps1
.\scripts\build-host-tools.ps1 -VerifyOnly
```

两组清单均 PASS，核验行为
`release=0.25.0 contract=21 android=0.25.0/27 artifacts=6 tool_catalog=13/e42f9adb7e416e1962cbb43397f12407bb3d7f4d28d02396e32ab3c317427ba1 source_commit=fcf6ca2430ab885c7e4513b300a6b0de11885ea9 source_dirty=False`。
`release/versions.json` 的 `source_commit` 是冻结基线
`6fe8d8bd1b408a5e7bb045d2d3394e7b8b67c994`，清单记录的是本次实现提交，两者按现行规则分工。
`-VerifyOnly` 确认双清单 companion 一致且 `source_dirty=False`；`.\build\device-mcp.exe -version`
输出 `0.25.0`。替换时旧宿主 MCP 进程镜像仍被脚本保留为备份 `.device-mcp.exe.previous-…`，
磁盘主路径已为 0.25.0。

| 实际产物 | SHA-256 | 大小（字节） |
| --- | --- | ---: |
| 手机 `device-service` | `e569b3351f4276fb4246704f39a88b0203a1c40d108a502abff02f1cdaf48587` | 9,044,128 |
| 手机 `device-mcp` | `823ad1d56162e546636506c586dc9318f9b94f9445dc9deca190b31a5beb34a8` | 9,371,808 |
| 手机 `control.apk` | `be69fe8d3e777981e7683ef7ec6077b132e43d7f7e760f2d7fd8ffc0153fa63c` | 103,965 |
| Windows `build/device-mcp.exe` | `c3fb08655d26a1a7e1e1d1355b0f6ee2467b049eb70d6e541e37162b36e08ea2` | 10,258,944 |
| Windows `build/mcp-probe.exe` | `63bae1650b3775b9c19ae3fa94362781a62ef60f18975abe6365a7a2e0a35204` | 9,956,864 |

工具目录指纹在 13 个工具上为
`e42f9adb7e416e1962cbb43397f12407bb3d7f4d28d02396e32ab3c317427ba1`；共享发现说明
10,978 字节，兼容 `2025-11-25` 的 `initialize` 与 `2026-07-28` 的 `server/discover`。

## 4. 手机整组覆盖部署

```powershell
.\scripts\deploy.ps1 -SkipBuild -McpAllowedOrigins 'http://127.0.0.1:3000'
```

机器 `24117RK2CC`（代号 `zorn`，设备号 `17efb04a`，Android 16，`1080×2400`，virtual 模式）
整组覆盖部署 PASS。`adb install -r` 保留控制 App 私有数据：`firstInstallTime=2026-11-09
03:45:47`、`lastUpdateTime=2026-11-22 04:54:17`、`versionName=0.25.0`、`versionCode=27`。
设备端 `device-service`、`device-mcp`、`control.apk` 的实际 SHA-256 与上表逐件 match。
部署后 `/health` 返回 `release=0.25.0 contract=21`，App 配对、恢复屏障、STOP 可用性与手机
HTTP MCP（`127.0.0.1:18882`，经转发为 `http://127.0.0.1:18883/mcp`）均 PASS。

## 5. 双入口独立探针

```powershell
.\build\mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/compact-result-envelope-and-image-sizing/stdio.private.json -result-mode both -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
.\build\mcp-probe.exe -transport http-remote -config .state/compact-result-envelope-and-image-sizing/http-remote.private.json -result-mode both -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
.\build\mcp-probe.exe -transport stdio -binary build/device-mcp.exe -config .state/compact-result-envelope-and-image-sizing/stdio.private.json -result-mode text -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

三组均 `RESULT PASS`：

| 入口与模式 | 关键事实 |
| --- | --- |
| 主机 stdio，`both` | `P4434_*` / `O2137_1..4`；`instructions=10978`；13 工具指纹与双清单匹配；覆盖 `app_find`/`app_find_empty`/`app_list`/`app_list_primary`、`observe`/`snap`/`act` 三处附图与 `shell`/`timeline`/`inspect`/`end`/`cancel` |
| 手机 HTTP MCP，`both` | `auth_missing=401`、`origin_denied=403`、`origin_allowed=200`、`agent_token_rejected=401`；`P2673_*` / `O2137_6..9`；13 工具指纹与双清单匹配；三处附图均为 `461×1024` |
| 主机 stdio，`text` | `P5148_*` / `O2137_11..14`；确认默认省略 `quality=default samples=omitted stop_reason=default`；附图 `461×1024` |

三组附图均为 `461×1024`，说明长边上限在主机与手机两侧入口同时生效。探针只证明本次产物与
两个入口的现状，不证明依赖宿主已切换。

## 6. 实机最小 `result v4` 文本闭环

私有检查程序 `.state/compact-result-envelope-and-image-sizing/v4_e2e_check.go`（gitignore
下，不入库）以 `-result-mode text` 分别经独立的 Windows `build/device-mcp.exe` stdio 与手机
HTTP `127.0.0.1:18883/mcp` 跑同一组步骤。两侧均完整通过：Windows stdio
`P4130_1..9` / `O2137_16..18`，手机 HTTP `P2673_15..23` / `O2137_19..21`。

1. `start` 首帧：`result v4 ok op=P…` + `session active mode=virtual origin=P… started=…
   expires=…` + `obs O… fg=none at=… screen=1080x2400@<display_id>`，即会话行与首帧屏宽高
   同时出现。
2. 重复 `start`（`session_exists`）：精确输出 `recovery entry=end ref=<P_origin>`。
3. `observe` 与 `act(key=BACK)`：2 行紧凑外壳（省略 `outcome=executed`、`status=complete`、
   `origin`/`revision`/`actionable_until`/默认 `quality`/`screen`）加短基线
   `tree v4 delta base=_<seq>`。
4. 对已退役 `O1` 执行 `act`（`observation_retired`）：精确输出
   `recovery entry=observe ref=<P_op> input=retired`。
5. `app(find, query="com.android.settings")` 输出 `app_items v1 kind=find count=1`；
   `app(list)` 输出 `app_items v1 kind=list count=0`，空集合仍出块头。
6. `snap` 输出 `image 461x1024 read_only screen=1080x2400@<display_id> at=<HH:MM:SS>`，客户端
   实际 JPEG 解码分辨率精确为 `461×1024`。
7. `end` 精确输出 `recovery entry=status ref=<P_end> input=revoked cause=cleanup_pending`；
   2 秒后 `status` 确认 `can_start=true`、`cleanup complete`。

两侧的 `O`/`P` 均为 4 位数字前缀加序号，`base=_<seq>` 可直接由当前 `O` 补齐；`recovery`
的 `ref` 在四条红线内解析为可直接回传的真实编号。

## 7. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 依赖 Agent（宿主）既有托管 MCP 进程的重连与工具重新发现 | 未执行。按 `AGENTS.md` #19 与提案 §7.2 由用户处理；本记录严格区分磁盘替换、独立进程验证与宿主切换，独立探针不构成宿主已切换的证据。 |
| 主屏（`primary`）模式、其他 Android 机型、设备服务进程跨重启账本标签避让的真实物理重演 | 未实机执行。跨重启标签避让与主屏/非零旋转/全部 4 条安全红线已由 Go 单元测试覆盖；本次实机范围按提案 §7.2 聚焦虚拟屏最小闭环与双入口探针。 |
