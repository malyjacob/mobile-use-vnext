# 部署脚本 MCP 停止/覆盖竞态修复验证记录

状态：2026-11-09。本页记录一次部署脚本缺陷的复现、根因、修复与实机验证。该缺陷不是产品
行为提案：`scripts/deploy.ps1` 在手机 MCP 适配器仍持有旧 inode 时静默未覆盖
`device-mcp`，却继续报告部署成功；修复后同一台设备上的部署逐项复核哈希并通过。

## 1. 现象与触发条件

在实现提交 `2f43ef1d8c6150dde3d1038ce5ed2d31caeecfac` 的发布部署后，`deploy.ps1` 输出
`phone MCP adapter: PASS` 且 `127.0.0.1:18882/mcp` 返回 401，但复查发现：

| 项 | 观察值 |
| --- | --- |
| 清单 `device-mcp` SHA-256 | `97e184b22c0e31b0a1fa59b081ea2ceb8dc9a6e5fb16b10642834bc9a34a12e0` |
| 安装目录 `device-mcp` SHA-256 | `1e2d9f2dccf54da226dcd2ffd32c7800e62ba0117d3a511a4b6737dc7977d936`（上一版） |
| 运行中的 HTTP MCP adapter `/proc/<pid>/exe` | 同一旧哈希 |
| 运行中的 host-managed stdio MCP adapter | 同一旧 inode |

触发条件：手机侧存在由宿主 `stdio-startup-guard` 托管的 `device-mcp -transport stdio`
进程。部署的停止步骤执行后，该 supervisor 会在安装窗口内把 stdio 适配器重新拉起；旧进程
仍持有安装路径的旧 inode。随后 `cp` 覆盖活动可执行文件返回 `ETXTBSY`。设备 shell 中该
失败被后续 `chmod` 的成功退出码掩盖，部署脚本既没有 `set -e`，也没有在复制后比对哈希，
于是继续启动旧二进制并用 401 探针报告成功。

## 2. 复现用例（修复前为红）

PowerShell 部署脚本无法由 Go 单元测试覆盖；复现是可执行的设备步骤：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/deploy.ps1 -SkipBuild
```

```bash
adb shell 'su -c "sha256sum /data/local/chroots/debian13/opt/mobile-use-vnext/device-mcp"'
adb shell 'su -c "sha256sum /proc/<http-mcp-pid>/exe"'
```

与 `build/release/manifest.json` 的 `artifacts[name=device-mcp].sha256` 比较。

修复前结果（红）：

- 安装目录与运行中的 HTTP adapter 都是 `1e2d9f…`；
- 清单是 `97e184…`；
- 部署输出仍为 `phone MCP adapter: PASS`。

修复后同一组命令（绿）：

```text
deployed artifact: device-mcp sha256=97e184b22c0e… (match)
deployed artifact: device-mcp (running http) sha256=97e184b22c0e… (match)
```

修复后的最终部署输出还逐项打印 `device-service`、`u2.jar`、`applist.dex`、`service.sh`、
`config.json`、`mcp-config.json`、`connection.json` 的 `(match)`。

## 3. 根因

| 位置（修复前） | 问题 |
| --- | --- |
| `scripts/deploy.ps1:278` | `pkill -f "^/opt/mobile-use-vnext/device-mcp "` 只按绝对 argv0 停止适配器；宿主 supervisor 可在停止后立即重启 stdio 适配器，且相对 argv0 不在匹配范围内 |
| `scripts/deploy.ps1:340` | `cp $staging/device-mcp $installDirectory/device-mcp` 直接覆盖活动可执行文件；旧进程持有 inode 时返回 `ETXTBSY` |
| `scripts/deploy.ps1:339-344` | 多条 `su -c` 组成的设备脚本没有 `set -e`；失败的 `cp` 被后续 `chmod` 的退出码掩盖 |
| `scripts/deploy.ps1:86` | `Invoke-DeviceScript` 只检查最外层 shell 的退出码，无法发现被掩盖的内部失败 |
| `scripts/deploy.ps1:420-425` | 启动后取任一 `device-mcp` PID（可能是 stdio）并只验证 401；旧监听者同样返回 401，因此不能证明运行的是清单二进制 |
| 缺少步骤 | 复制后没有比对已部署文件与清单的 SHA-256，也没有验证 HTTP adapter 的 `/proc/<pid>/exe` |

直接原因是 `cp` 覆盖活动 inode 失败；检测原因是缺少“部署后按清单哈希复核”的出口条件。

## 4. 修复提交、改动边界与实机结果

修复提交：本记录所在提交。被部署的发布仍是 `2f43ef1`，本次不改服务、App、契约、清单或
`contract_version`。

| 文件 | 改动 |
| --- | --- |
| `scripts/deploy.ps1` | `device-mcp` 等文件先写 `.new` 再 `mv` 原子替换；安装块与 `connection.json` 写入使用 `set -eu` + `&&`；停止增加相对 argv0 模式与有界等待；按清单构建哈希表；复制后逐项校验已部署文件；只接受唯一 HTTP adapter 并校验其 `/proc/<pid>/exe` 哈希；检测仍持旧 inode 的 host-managed stdio adapter 并告警；停止窗口后仍有 MCP 进程时也告警 |
| `docs/release.md` §4 | 写明原子替换、部署后 SHA-256 复核、HTTP adapter 运行哈希与 stdio 告警 |
| `maintenance/verification/deploy-mcp-race.md` | 本记录 |

`phone MCP HTTP adapter: PASS` 现在只在运行中的 HTTP adapter 哈希与清单一致时打印；宿主
托管的 stdio adapter 不在脚本可重启范围内，脚本明确告警而不是把它计入成功。

### 4.1 实机验证结果

| 项 | 值 |
| --- | --- |
| 设备 | `24117RK2CC`（`ro.serialno=17efb04a`），Android 16（API 36），arm64 |
| 服务 | `0.4.0`，`contract_version=2`；`can_start=true`；无活动会话；`cleanup=complete` |
| 发布清单 | `source_commit=2f43ef1…`，`source_dirty=false`；`verify-manifest.ps1` PASS |
| PowerShell 语法 | `[Parser]::ParseFile` 返回 `PARSE_OK` |
| 修复后部署 | `scripts/deploy.ps1 -SkipBuild` 成功；所有已部署文件 `(match)`；HTTP adapter 运行哈希 `(match)`；`phone MCP stdio adapter: no stale binary detected` |
| 运行进程 | `device-service`、HTTP adapter 与 stdio adapter 的 `/proc/<pid>/exe` 分别为清单值 `d8d3c86c…`、`97e184b…`、`97e184b…` |
| MCP 工具面 | `tools/list` 返回 12 个工具；`act` 描述包含“坐标目标会在派发前按点解析节点并证明未变化” |
| 清理 | 部署后 `can_start=true`、`cleanup=complete`；无活动会话 |

本次只改部署脚本与文档，没有改 Go 代码；实现提交 `2f43ef1` 的完整 `go test ./...`、
`go vet ./...` 与 Linux/arm64 构建结果见
[act-coordinate-point-proof.md](act-coordinate-point-proof.md)。

文档审计：独立只读审计 `docs/release.md` §4；修正“实例/模式核验”和“停止失败/告警”两处
措辞后复检为 **CLEAN**。

## 5. 同类核查

| 同类点 | 结论 |
| --- | --- |
| 其他清单产物（`device-service`、`u2.jar`、`applist.dex`、`service.sh`） | 同样改为临时文件 + rename；复制后逐一比对清单哈希，任意不一致立即停止 |
| 生成配置（`config.json`、`mcp-config.json`） | 对宿主生成文件的 SHA-256 比对，防止静默复制失败 |
| App 私有 `connection.json` | 改为临时文件 + rename，并对宿主生成值比对 SHA-256 |
| “只验证代理事实” | 旧逻辑用 401 代表适配器成功；新逻辑要求唯一 HTTP PID 且其可执行文件哈希匹配，401 只作为监听就绪的前置条件 |
| 多命令设备 shell | 安装/连接写入块加入 `set -eu` 与 `&&`，内部 `cp`/`mv` 失败会中止部署 |
| 停止匹配范围 | 同时匹配绝对 argv0 和相对 `device-mcp -transport`，并等待有界时间；supervisor 仍可重启，但原子安装保证文件正确，运行中的旧 stdio 会被检测并告警 |

## 6. 未覆盖项与原因

| 项 | 原因 |
| --- | --- |
| 宿主 supervisor 对 stdio adapter 的最终重启 | 脚本不能重启宿主 MCP 进程；它只检测旧 inode 并告警。必须在宿主侧重启 MCP 进程/连接后才会重新发现工具，这与 `docs/release.md` §4 第 5 步一致 |
| 部署自身验证 `tools/list` | 部署契约一直把宿主工具目录刷新列为独立步骤；本次修复只保证 HTTP adapter 运行二进制与清单一致。实机 `tools/list` 是部署后手工复核，不是部署脚本的出口条件 |
| PowerShell 单元测试 | 该脚本直接操作 adb、chroot 与设备进程，没有可注入的宿主测试环境；本记录以语法解析 + 实机部署作为复现/验证。未把实机部署写成单元测试 |
| `-SkipPhoneMcp` 路径 | 本次修复的竞态只在安装手机 MCP adapter 时出现；跳过路径不安装/启动 adapter，未单独重跑，但其安装块与哈希检查仍受 `if (-not $SkipPhoneMcp)` 保护 |
| supervisor 在哈希检查之后再次拉起旧 inode | 检查是时间点事实；原子安装已保证磁盘文件正确，HTTP adapter 运行哈希已证明。stdio 进程的后续重启仍由宿主负责，脚本只能告警 |
| 其他 Android/toybox 版本 | `sha256sum`、`pgrep -x`、`/proc/<pid>/exe` 在本设备可用；未在旧 Android 或不同 toybox 组合上验证。HTTP adapter 的 `pgrep -f` 与运行哈希已验证 |
| 停止窗口后仍有 MCP 进程的告警 | 代码路径已实现（`Write-Warning`），本次部署在窗口结束时无 `device-mcp` 残留，未触发；未构造残留进程来实机验证告警文本 |

## 结论

- **已实现**：原子安装、失败传播、清单/配置/连接文件哈希复核、HTTP adapter 运行哈希、
  stdio 旧 inode 告警。
- **已实机验证**：修复前同一设备上 `device-mcp` 是旧哈希且部署报告 PASS；修复后安装文件、
  HTTP adapter 运行文件与清单一致，部署逐项打印 `(match)`，服务 `can_start=true`、
  `cleanup=complete`。
- **未覆盖**：宿主 MCP 进程必须单独重启；部署脚本不自证 `tools/list`；超级用户 supervisor
  的后续重启只能告警，不能由脚本保证。
