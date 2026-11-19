# MCP 树文本无损简写（tree v3 / inspect v3）实现与验证

实现提交：`9009599df87ac192787e730f9e9970b42d8f97e6`（`master`，2026-11-20），父提交
`145bf20da4dfaa037a68be26356350c4de097968`。
提案：[compact-node-role-text.md](../proposals/implemented/compact-node-role-text.md)
（归档时补 §11）。
版本：`0.21.0` / `contract_version=20` / Android `versionCode=23`。
工具目录：`13` 个工具，SHA-256
`914fc4b061e14eb42fa75288e1c695e1649e49797c662a012013abdb265d1dc5`。
本次覆盖手机整组构建/核验/覆盖部署、Windows 主机两个 exe 重建替换、主机 `stdio` 与手机
`http-remote` 两条独立探针及受控页面实机最小闭环；固定 `u2.jar` 与 `applist.dex` 未修改。
所有构建与验证只使用本仓库源码、已声明版本依赖与固定制品。

## 1. 范围与结论

按提案将 MCP 观察树与补看树文本块升级为 `tree v3` 与 `inspect v3`：普通节点在 `n<ref>`
后按首列可逆恢复默认角色（隐藏几何后直接是引号标签默认 `text`，几何/`value=`/`hint=`/
普通动作/状态或属性标记/行尾默认 `view`），首项为角色词（含输入动作 `text`）或与默认冲突
时显式写角色；合法匿名默认 `view` 链在隐藏几何且其余共享字段无输出时简写为 `[n2 n3]`，
`group` 链及带任意输出字段的链保持 `chain [refs] <role> ...`；delta 的 `+`/`=` 完整定义
及其子树复用 v3 规则，`~`/`children`/`-`、外层 `result v2`、`structuredContent`、HTTP
`/api/*` 与共同契约 `20` 均不变。

- **已实现**：`internal/mcp` 渲染与图例/描述、`scripts/mcp-probe` v3 块校验与 v2 拒绝、
  `0.21.0 / 20 / 23` 版本源与生成件、正式文档同步（独立只读文档审计 `CLEAN`）。
- **已单测**：`internal/mcp`、`scripts/mcp-probe` 及全部仓内 Go 包单测通过（含
  `internal/engine` 全包 558.060s），linux/arm64 交叉构建通过，两组反向对照均按预期转红
  并在恢复后全绿。
- **已实测（有限）**：手机整组覆盖部署与磁盘/`/proc/<pid>/exe` 核验 PASS；Windows
  两个 exe 磁盘替换与双清单 companion 核验 PASS；主机 `stdio` 与手机 `http-remote` 两条
  独立探针均 `RESULT PASS`（均返回 `tree_block=v3`）；受控 `com.mobileuse.probe` 页面最小
  闭环 PASS，实机命中 `shorthand_view_chain`、`omitted_text`、detailed 下 `omitted_view`
  与 `explicit_text`、compact `full` 与 `delta`、`inspect` subtree/tree，`both` 模式文本
  与结构化逐节点 `0 mismatch`。实机未命中的语法边缘分支（如输入动作 `text` 撞角色位、
  `group` 链、未知几何 `-`、v2 拒绝）依据单元测试证明。
- **未执行 / 依赖方边界**：依赖本适配器的用户 Agent 宿主及其托管 MCP 进程（Windows
  `DeepSeek Harness.exe` 托管的 `PID 33184` 与手机 `dsh-mcp-connector` 托管的 `pid 20888`）
  未停止或重启，由用户自行切换刷新；私有轨迹的真实模型输入与计费未在本机复测。

## 2. 实现落点

| 条目 | 代码落点 |
| --- | --- |
| v3 块头、角色省略与匿名默认 `view` 链渲染 | `internal/mcp/tree_text.go` `renderFullTreeText`/`renderDeltaTreeText`/`renderInspectionText`/`writeNodeContent`/`writeChainRefs`/`isAnonymousDefaultViewChain`/`omitsNodeRole`/`nodeFirstColumnIsRole`/`writeNodeColumns` |
| 全局树说明、工具描述与目录指纹 | `internal/mcp/schema.go` `treeTextGuide`、`inspect` 描述；目录指纹更新为 `914fc4b061e14eb42fa75288e1c695e1649e49797c662a012013abdb265d1dc5` |
| MCP 树文本、说明、结果等价与服务单测 | `internal/mcp/tree_text_test.go`、`internal/mcp/schema_test.go`、`internal/mcp/result_text_test.go`、`internal/mcp/result_parity_test.go`、`internal/mcp/server_test.go` |
| 探针 v3 块头识别、正文语法/计数校验与 v2/缺块拒绝 | `scripts/mcp-probe/result.go`、`scripts/mcp-probe/checks.go`、`scripts/mcp-probe/result_text.go`、`scripts/mcp-probe/probe_test.go`、`scripts/mcp-probe/result_modes_test.go` |
| 版本源 `0.21.0 / 20 / 23` 与生成件 | `release/versions.json`、`internal/protocol/version_generated.go`、`android-control/app/src/main/java/com/mobileuse/vnext/GeneratedVersion.java`、`android-control/app/src/main/res/values/strings_version.xml`、`android-control/app/src/main/AndroidManifest.xml` |
| 正式文档 | `README.md`、`docs/mcp.md`、`docs/architecture.md`、`docs/release.md`、`docs/control-protocol.md` |

## 3. 自动验证

执行环境：Windows x64，Go `go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`。

```text
./scripts/generate-version.ps1 -Check
GOEXPERIMENT=jsonv2 go vet ./internal/mcp/... ./scripts/mcp-probe/...
GOEXPERIMENT=jsonv2 go test -count=1 ./internal/mcp/... ./scripts/mcp-probe/...
GOEXPERIMENT=jsonv2 go test -count=1 ./cmd/... ./internal/... ./scripts/...
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/... ./internal/... ./scripts/...
```

结果：

- `generate-version.ps1 -Check`：`version source consistent: release=0.21.0 contract=20 android_version_code=23`。
- `go vet`：exit 0，无告警。
- `internal/mcp`：`ok`（0.491s–0.614s）；`scripts/mcp-probe`：`ok`（7.492s–9.833s）；
  `cmd/device-mcp`、`internal/android`、`internal/client`、`internal/device`、
  `internal/httpapi`、`internal/imaging`、`internal/observation`、`internal/protocol`、
  `internal/store` 全部 `ok`；`internal/engine` 全包 `ok`（558.060s）。（注：工作区
  `.gitignore` 下的未追踪目录 `build/investigation-text-uncertain` 不属仓内源码包，
  直接 `go test ./...` 扫入 `build/` 时该未追踪目录编译失败，所有仓内跟踪包均通过。）
- `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/... ./internal/... ./scripts/...`：exit 0。
- 单元测试覆盖：
  - `internal/mcp/tree_text_test.go`：无字段/矩形/未知几何/标志/普通动作起始的 `view` 省略
    （`TestTreeTextV3OmitsViewRoles`）；12 合法角色 × 3 几何状态 × 全部首项类别往返矩阵
    （`TestTreeTextV3RoleRecoveryMatrix`，含无标签 `text` 显式保留、带标签或输入动作 `text`
    的 `view` 显式保留）；full/delta 匿名短 `view` 链与显式 `group`/带字段链及 `nodes`/`entries`
    计数（`TestTreeTextV3ChainAndGeometry`、`TestTreeTextV3ChainDelta`）；合成缩减字节断言
    （`TestTreeTextV3CompactBytesWin`）；delta 重放与三态 `set` 存在性
    （`TestTreeTextDeltaReplayAndSetPresence`）；转义/非 BMP/长文/JSON 兜底/错误与图片等价；
    独立解析器拒绝 `tree v2`、`inspect v2` 与非法简写（`TestTreeTextParserV3Gate`）。
  - `scripts/mcp-probe/result_modes_test.go` 与 `probe_test.go`：只接受 `tree v3` 与
    `inspect v3`，拒绝同契约 `tree v2`/`inspect v2`、缺失树块、未闭合块、计数不匹配、
    detailed 缺几何、以及省略角色 `text` 标签后紧跟几何列等非法正文。

反向对照（临时修改 `internal/mcp/tree_text.go` 运行 `go test -count=1 ./internal/mcp/`，
随后按字节原样恢复并核对恢复前后 SHA-256 完全一致）：

| 临时变异 | 转红用例 | 结果 |
| --- | --- | --- |
| 关闭简写（`omitsNodeRole` 与 `isAnonymousDefaultViewChain` 恒返回 `false`） | `TestTreeTextV3OmitsViewRoles`、`TestTreeTextV3RoleRecoveryMatrix`、`TestTreeTextV3ChainAndGeometry`、`TestTreeTextV3ChainDelta`、`TestTreeTextV3CompactBytesWin`、`TestTreeTextReflectsCompactBoundsPolicy` | 红（exit 1） |
| 忽略显式 `view` 例外（`omitsNodeRole` 对全部 `RoleView` 均返回 `true`） | `TestTreeTextV3RoleRecoveryMatrix`（`view/hidden/label-view`、`view/hidden/label-text`、`view/hidden/label-empty`、`view/hidden/action-text`） | 红（exit 1） |

原样恢复后 `internal/mcp`（0.491s）与 `scripts/mcp-probe`（7.984s）复跑全绿。

文档审计：实现提交前由独立只读 subagent 审核 `README.md`、`docs/mcp.md`、`docs/architecture.md`、
`docs/release.md`、`docs/control-protocol.md` 及关联文档与版本/代码一致性，结论为 `CLEAN`。

## 4. 构建、清单与主机磁盘替换

在干净实现提交 `9009599df87ac192787e730f9e9970b42d8f97e6` 上执行：

```text
./scripts/build-release.ps1
./scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
./scripts/build-host-tools.ps1
./scripts/build-host-tools.ps1 -VerifyOnly
```

结果：

- 手机清单 `build/release/manifest.json`：`release=0.21.0`、`contract=20`、
  `android=0.21.0/23`、`source_commit=9009599df87ac192787e730f9e9970b42d8f97e6`、
  `source_dirty=false`、`tool_catalog=13/914fc4b061e14eb42fa75288e1c695e1649e49797c662a012013abdb265d1dc5`；
  `verify-manifest.ps1` PASS，6 个产物齐全。
- 主机清单 `build/host-tools-manifest.json`：同一 `source_commit`、`release=0.21.0`、
  `contract=20`、`source_dirty=false` 与工具目录指纹；`-VerifyOnly` PASS，手机清单
  companion PASS。
- 主机磁盘替换核验：构建前检查发现用户宿主 `DeepSeek Harness.exe`（`PID 26384`）持有
  `build/device-mcp.exe`（子进程 `PID 33184`）。按依赖方边界未停止或重启该托管进程；
  `build-host-tools.ps1` 原子替换了磁盘上的两个 exe（旧 `device-mcp.exe` 映像由脚本
  保留为备份 `build/.device-mcp.exe.previous-e3602290519449b3a1e7ff68676ebfe1`）：
  - `build/device-mcp.exe`：`46c0a2c6...`（10,112,000 B）→ `bb205bbc4d79c5dc89c0ef557e8df28524377bec163d34a84caf64001ccf1d1b`（10,116,608 B）
  - `build/mcp-probe.exe`：`a1a5935e...`（9,738,240 B）→ `e13000940272cd8486d47afb9868852d9c7a53f791e6fd228093732b63d4fe7c`（9,770,496 B）

两份清单产物明细：

| 产物 | SHA-256 | 字节 |
| --- | --- | ---: |
| `build/release/device-service` | `e2e2529131769d1945cf86c6ba11734a62d4f5a01c4894f8b9984b1fe62ca875` | 9,044,128 |
| `build/release/device-mcp`（linux/arm64） | `0e3a930d0a4433646bda25dc3782ea766545f0c424e3960900a732f03c884b87` | 9,175,200 |
| `build/release/control.apk` | `7603bd5d5a09d5eb67f767dd39798d147f5004546092d0ae8bfdf94f1adbfbf3` | 103,965 |
| `build/release/u2.jar` | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` | 3,306,229 |
| `build/release/applist.dex` | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` | 4,316 |
| `build/release/service.sh` | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` | 3,643 |
| `build/device-mcp.exe` | `bb205bbc4d79c5dc89c0ef557e8df28524377bec163d34a84caf64001ccf1d1b` | 10,116,608 |
| `build/mcp-probe.exe` | `e13000940272cd8486d47afb9868852d9c7a53f791e6fd228093732b63d4fe7c` | 9,770,496 |

两份清单的 `source_commit` 均锚定实现提交 `9009599df87ac192787e730f9e9970b42d8f97e6`。归档
提交只修改 `maintenance/`，不重建产物；归档后主树 HEAD 前进，若需重新运行严格 HEAD
校验门禁应从对应源码提交重建。

## 5. 手机覆盖部署与最小实机闭环

设备：`adb` 序列 `17efb04a`，型号 `24117RK2CC`（`zorn`），chroot
`/data/local/chroots/debian13`，目标虚拟屏 `1080x2400`。

部署前检查：旧服务 `/health` 为 `release=0.20.0`、`contract=20`；`/api/status` 返回
`can_start=true`、最近会话 `P22ad5a11-884f-46ff-8f60-7a43837b2049_11` 已 `ended` 且
`cleanup.state=complete`（无活动会话需撤权）。

执行覆盖部署（保持默认手机 HTTP MCP 启动，保留控制 App 私有数据）：

```text
./scripts/deploy.ps1 -SkipBuild -McpAllowedOrigins http://127.0.0.1:3000
```

部署后核验：

- 控制 App：`com.mobileuse.vnext`，`versionName=0.21.0`，`versionCode=23`，经 `adb install -r`
  保留私有数据并配对成功。
- Go 服务：`/health` 返回 `status=ok`、`release_version=0.21.0`、`contract_version=20`、
  `service_instance_id=9ec7022e-d370-44e9-9e0f-dcea1ef315f8`；`/api/status` 返回
  `can_start=true`，恢复屏障与 STOP 可用性 PASS。
- 退休记录与诊断：`/api/status` 如实记录旧会话退休状态（`reason=service_restarted`，
  `cleanup.state=complete`，`execution/tasks/recents/display` 均 `confirmed`）；
  `/api/diagnose` 如实保留历史 `retired_cleanup_dropped` 与 `service_restarted/uncertain`
  时间线（`coverage=own_records`、`truncated=true`），不阻断新会话准入。
- 磁盘文件与运行镜像分别核验：

| 对象 | 磁盘路径 / 进程 | 磁盘 SHA-256 | 运行 `/proc/<pid>/exe` SHA-256 | 与清单比对 |
| --- | --- | --- | --- | --- |
| `device-service` | `pid 21416` | `e2e25291...ca875` | `e2e25291...ca875` | 一致 |
| `device-mcp`（手机 HTTP） | `pid 21465`（`127.0.0.1:18882` / 转发 `18883`） | `0e3a930d...84b87` | `0e3a930d...84b87` | 一致 |
| 依赖方手机 `device-mcp`（`stdio`） | `pid 20888`（`dsh-mcp-connector` `pid 20876` 托管） | `0e3a930d...84b87`（已替换） | `e5ad0da8...1e867`（`(deleted)` 旧 0.20.0 映像） | 属依赖方进程，未触碰，由用户重启刷新 |

最小实机闭环通过本次新构建的 `build/device-mcp.exe -transport stdio -config .state/mcp-config.json -result-mode both`
在虚拟屏受控页面 `com.mobileuse.probe` 执行（不将原树、标签正文、截图或令牌写入仓库）：

| 步骤 | 结果与块头 | 逐节点 `both` 核对与命中分支 |
| --- | --- | --- |
| `status` | `can_start=true` | 准入就绪 |
| `start` | `tree v3 format=full`（`nodes=1 entries=1`） | `0 mismatch`；`explicit_root=1` |
| `app open com.mobileuse.probe` | `outcome=executed`，`tree v3 format=full`（`nodes=16 entries=14`） | 发布受控页观察 |
| `observe(full=true)` | `tree v3 format=full`（`nodes=16 entries=14`，`view=compact`） | `0 mismatch`；命中 `explicit_root=1`、`shorthand_view_chain=1`（3 个匿名 `view` 节点折为 1 行）、`omitted_text=4`、`explicit_input=1`、`explicit_button=6`、`explicit_checkbox=1` |
| `act(tap, ref=n7)` | `outcome=executed`，`tree v3 format=delta`（`base=O_obs`、`changes=5`） | `n7` 从 `tree v3` 文本直接定位；点击计数 `0 → 1` |
| `inspect(O_obs, node=n5)` | `inspect v3 scope=subtree view=detailed`（`nodes=1 entries=1`） | `0 mismatch`；compact 中省略类型的 `n5`（`omitted_text`）在 detailed 恢复已知矩形后转为 `explicit_text`（`bounds_kind=known`、`role=text`） |
| `inspect(O_act)` | `inspect v3 scope=tree view=detailed`（`nodes=16 entries=16`） | `0 mismatch`；命中 `explicit_root=1`、`omitted_view=3`（带矩形普通 `view`）、`explicit_text=4`、`explicit_input=1`、`explicit_button=6`、`explicit_checkbox=1`；确认点击计数为 `1` |
| `end` | `outcome=executed`，`session.state=ended` | 即时 `cleanup={state=pending, execution=confirmed, tasks=pending, recents=pending, display=pending}` |
| `status`（结束后） | `can_start=true`，`session.state=ended` | 最终 `cleanup={state=complete, execution=confirmed, tasks=confirmed, recents=confirmed, display=confirmed}` |

## 6. 双入口独立探针

两条独立探针均使用本次新构建的 `build/mcp-probe.exe`，传入两份清单比对目录身份，各自结束其
创建的会话：

1. **主机 `stdio` 独立探针**（启动本次新构建的 `build/device-mcp.exe`）：

   ```text
   ./build/mcp-probe.exe -transport stdio -config .state/mcp-config.json -binary build/device-mcp.exe -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
   ```

   `RESULT PASS`（exit 0）：`connect transport=stdio result_mode=both`；`instructions`
   6,308 B 与源码逐字节一致（`2026-07-28` 与兼容 `2025-11-25` 均 `source_match=true`）；
   13 工具与两份清单目录指纹 `914fc4b0...dc5` 一致；`observe` 实际返回 `tree_block=v3`
   （`tree_format=full`），`inspect` 返回 `inspect_block=complete tree_block=v3`；
   `start/app/observe/snap/wait_for/shell/timeline/inspect/act/end/cancel` 全部 PASS，
   收尾后 `can_start=true`。

2. **手机 `http-remote` 独立探针**（经主机转发 `18883/mcp` 访问手机 `127.0.0.1:18882`）：

   ```text
   ./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json -result-mode both -timeout 180 -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
   ```

   `RESULT PASS`（exit 0）：`service_remote contract=20 release=0.21.0`；认证与 Origin
   边界（`401/403/200/401`）全部 PASS；`instructions` 6,308 B 与源码逐字节一致；13 工具
   与两份清单目录指纹一致；`observe` 实际返回 `tree_block=v3`，`inspect` 返回
   `inspect_block=complete tree_block=v3`；全流程与 `end`/`cancel` PASS，收尾后
   `can_start=true`。

## 7. 责任边界与未执行项

| 项 | 结论 |
| --- | --- |
| 磁盘替换（手机 6 件套与主机 `build/device-mcp.exe`、`build/mcp-probe.exe`） | 本次完成；两份清单均锚定 `9009599` 并核验 PASS |
| 独立进程验证（手机 `device-service`、手机 HTTP `device-mcp`、主机 `stdio` 探针、手机 `http-remote` 探针、受控页闭环） | 本次完成；均运行 `0.21.0` 新镜像并实测返回 `tree v3` / `inspect v3` |
| 依赖方宿主及托管 MCP 进程切换（Windows `DeepSeek Harness.exe` `PID 33184`、手机 `dsh-mcp-connector` `pid 20888` 及其他用户 Agent） | 由用户自行重启/重连并刷新工具发现；维护侧未停止或重启任何依赖方进程 |
| 实机未命中的语法分支（显式 `view` 带标签或撞输入动作 `text`、`group` 链、未知几何 `-`、delta 内链、`inspect query`、同契约 v2 拒绝） | 实机未构造对应页面，全部由 §3 单元测试与反向对照覆盖 |
| 真实 DSH 轨迹的线上模型输入、计费与其他第三方应用分布 | 未执行：超出最小实机闭环范围；提案 §1.1 离线重放数字只作设计取舍依据 |
