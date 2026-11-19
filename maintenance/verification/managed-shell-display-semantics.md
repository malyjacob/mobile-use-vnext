# 受管 shell 的显示屏语义与 `screen.display_id`：实现与验证记录

- 记录时间：2026-11-20。
- 代码锚点：`HEAD=ac5677d`（0.21.0 / 契约 20 / versionCode 23，已部署整组）**之上未提交的改动**。
  本记录描述的就是这些改动，因此第 4 节不给实现提交 hash。
- 本记录涉及实机：**部分是**。§3 的复现与对照是 0.21.0 实机结果；**改动后的新行为未实机验证**，
  原因见 §8。
- 对应 issue：`#11`「受管 `shell` 的 `input` 类命令作用于默认屏：会话虚屏里静默无效果，
  回执却是 `executed`」。
- **跳过提案的声明**：本次由用户直接指定跳过讨论与提案（"这次改动不用写提案，毕竟是比较小的
  改动"）。按 `process.md` §2 的代价条款，决策理由只留在本节与提交信息里；归因、执行、测试
  与验证记录均未跳过。

## 1. 分诊

**取舍，不是缺陷。** 判据按 `process.md` §1：现行规范（`docs/interface.md` 的 `POST /api/shell`
一节）从未声称受管命令绑定会话显示屏，实现忠实于"设备级逃生口"的设计意图，
`internal/android/shell.go` 自 issue 基线 `fcfd593` 以来**零改动**。因此问题不是"规范对、实现偏"，
而是**规范空白**：调用方无法从任何文档或结果字段读出"这条命令不会作用在你正在看的屏幕上"。

`outcome=executed` 本身不构成虚假陈述：按 AGENTS.md 第 8 条的事实分层，它描述的是命令执行，
不是屏幕变化。真正缺的是可发现性与文档。

## 2. 现象与触发条件

在虚拟屏会话里用受管 `shell` 发 `input` 类命令：回执 `outcome=executed`、`termination=exited`、
`exit_code=0`、`output_bytes=0`，但会话屏不动。调用方会据此认为"已经滑到位"，继续找元素时
拿到误导性的 `target_changed`/`node_not_found`；而 `shell` 按契约不发布观察，连"到底动没动"
都没有即时证据。

## 3. 复现（0.21.0 实机，本轮重跑）

设备 `24117RK2CC` / Android 16，`session.mode=virtual`，`app open com.android.settings` 回执给出
会话屏 `display_id=115`。同一会话、同一页面，两条命令只差 `-d`：

| 命令 | 回执 | 观察结果 |
| --- | --- | --- |
| `input swipe 540 1800 540 600 300` | `executed` / `exited` / `exit_code=0` | `observe` 的 delta 相对基准 `changes=0` —— 逐节点零差异 |
| `input -d 115 swipe 540 1800 540 600 300` | 完全相同的回执 | delta `changes=27`，列表整体上移并滚出新行 |

原 issue 的结论（`fcfd593` 上 2/2 复现、`-d` 可用）在当前构建上**仍然成立**。

### 3.1 issue 未覆盖的新发现

`-d` 本身也不是自证的。同一会话实测：

```text
input -d 999 swipe 540 1800 540 600 300   →  exit 0，无任何输出与报错
input -d 999 text "x"                     →  exit 0，无任何输出与报错
```

`999` 不是存在的显示屏，两条命令仍以 `exit_code=0` 返回。所以"自己带 `-d`"只是把静默失败
换了个位置，唯一可靠的验证手段仍是 `observe` 后对比内容位移。这条已写进文档与工具说明。

## 4. 根因（`file:line`）

- `internal/android/shell.go:82-83`（`runShellScript`）：走 `adb shell sh -c '<script>'`，**不带 `-d`**。
- `internal/android/shell.go:170`（`StartManagedShell`）：`setsid /system/bin/sh <runner> <dir> <seconds>`，
  同样没有显示屏绑定；`root=true` 只是包一层 `su -c`，display 语义不变。

因此命令跑在默认显示（display 0）上，会话虚屏不受影响。这是必然结果，不是时序或权限问题。

## 5. 改动与边界

| 改动 | 落点 |
| --- | --- |
| `screen` 增加可选身份字段 `display_id` | `internal/protocol/responses.go`（`Screen.DisplayID *int`） |
| 客户端解码：缺失即"未上报"，不臆造 0 | `internal/protocol/decode_response.go`（`Screen.UnmarshalJSONFrom`） |
| 引擎投影收敛为单一助手，四处构造点统一 | `internal/engine/capture.go`（`screenOf`）、`snap.go`、`wait_for.go` |
| `shell` 工具说明写入显示屏语义与静默失败告警 | `internal/mcp/schema.go` |
| 契约文档：shell 节与 `screen` 对象 | `docs/interface.md` |
| 工具目录指纹基线 | `internal/mcp/result_parity_test.go` |
| 公共结果字段清单与 canonical fixture | `internal/mcp/result_manifest_test.go` |

关键设计取舍：

- **契约保持 20。** `docs/release.md:46` 规定 `contract_version` 只在改变"普通请求/响应必需形状、
  默认行为、核心枚举或执行语义"时前进。新增一个**可选**输出成员不属于这四类；且
  `internal/protocol/decode_response_test.go:690` 已证明响应解码对未知成员按 forward
  compatibility 忽略，旧客户端读新服务不会报错。
- **`DisplayID` 用 `*int` 而不是 `int`。** `0` 是合法的主屏值（`docs/interface.md` 明确
  "`display_id=0` 是合法的主屏值"）。若用零值表示缺失，旧服务缺席该成员时调用方会误以为
  "当前是主屏"，进而用 `input -d 0` 去驱动手机本身——正是本条要消除的危害。用指针让
  "未上报"与"主屏"可区分。
- **不改 `shell` 的执行语义。** 不自动注入 `-d`，不加 `shell.display` 开关。issue §4 建议 3
  已被其作者降级为可选；在 `-d` 已可用的前提下再加一层只会多一条静默失败路径，且契约成本最高。

## 6. 同类核查

- **同类"静默成功"面**：`shell` 不只 `input` 一条路径会改变界面（`am`/`wm`/`settings` 等同属
  设备级命令，同样作用于 display 0）。文档按**一般规则**写"受管命令不绑定会话显示屏"，
  而不是只针对 `input`，因此这几类一并覆盖。
- **同类"缺显示屏身份"面**：`screen` 对象不止出现在 `observation`，还出现在
  `observation.image` 与顶层 `image`（`snap`）。改动落在共享的 `protocol.Screen` 与单一
  `screenOf` 助手，三处投影一起获得身份字段，不需要第二套。
- **未改的反向投影**：`internal/engine/capture.go` 里由 `obs.Screen` 反推 `device.Display` 的
  路径只取宽/高/旋转用于图片编码，不需要显示身份，未改动。
- **issue 未覆盖的边界**已补测（§3.1）。

## 7. 已实现 / 已单测 / 已发布 / 已实机

**已实现**：§5 全部改动，实现提交 `c7c15e8100b4d6987d8f719696c0a21e5730eb26`
（文档审计 CLEAN 后提交；审计首轮报 2 项阻塞——`docs/mcp.md` 过期指纹、shell 段
"唯一验证办法"与 `docs/interface.md:48` 冲突——已按最小修正改毕并复检）。

**已单测**（`GOEXPERIMENT=jsonv2`，`go build ./...` 退出 0）：

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/protocol ./internal/mcp ./internal/observation ./internal/client -count=1` | 全部 `ok` |
| `go test ./... -count=1` | `internal/engine` 560.203s、`internal/mcp` 0.552s、`internal/protocol` 0.063s、`scripts/mcp-probe` 12.467s、`internal/httpapi` 2.396s、`internal/android` 14.126s、`internal/store` 1.228s、其余全 `ok` |

唯一失败项 `build/investigation-text-uncertain` 是**不受 git 跟踪的本地调研脚手架**
（`git ls-files` 为空），在本改动之前就无法编译，与本次无关。

防漂移元测试按设计先红后绿，两处基线都是**显式复核后**更新，不是绕过：

| 测试 | 首跑结果 | 处理 |
| --- | --- | --- |
| `TestResultFieldManifest` | `protocol.Screen composite inventory changed: Width Height Rotation DisplayID` | 在 `rmfCompositeInventory` 登记 `DisplayID` |
| `TestCanonicalJSONCoversManifest` | `canonical fixtures omit manifest fields: protocol.Screen.DisplayID -> image.screen.display_id / observation.image.screen.display_id / observation.screen.display_id` | 三份 fixture 补 `DisplayID: rmfPtr(115)` |
| `TestCatalogFingerprintBaseline` | `catalog changed: bf9aecee…` | 基线 `914fc4b0…` → `bf9aecee…`，与清单 `tool_catalog.sha256` 同步 |

发现面预算仍充足：`instructions=6308`、`tools_list=20672`、`total=26980`，基线 `36250`
（`TestDiscoveryByteBudget` PASS）。改动使描述增加 384 字节。

**已发布**：`0.22.0` / 契约 `20`（不变）/ Android `versionCode 24`，源码提交
`c7c15e8`、`source_dirty=false`。

| 门禁 | 结果 |
| --- | --- |
| `scripts/build-release.ps1` | 六制品齐全，`release=0.22.0 contract=20 android=24` |
| `scripts/verify-manifest.ps1` | `PASS … artifacts=6 tool_catalog=13/bf9aecee… source_commit=c7c15e8… head=c7c15e8…` |
| `scripts/deploy.ps1 -SkipBuild -McpAllowedOrigins http://127.0.0.1:3000` | `version match`/`App pairing`/`recovery barrier`/`STOP availability` 全 PASS；App 覆盖安装为 `versionName=0.22.0 versionCode=24`，私有数据保留 |
| 手机侧制品 | `device-service db601ccb…`、`device-mcp 08faec74…`，以及 `u2.jar`/`applist.dex`/`service.sh`/`config.json`/`mcp-config.json` 全部 `match` |
| `scripts/build-host-tools.ps1` 与 `-VerifyOnly` | `PASS … artifacts=2`；`device-mcp.exe 8babe20e…`、`mcp-probe.exe 42b0b9fe…` |

首次部署漏传 `-McpAllowedOrigins`，手机 HTTP 探针在 `origin_allowed` 上返回 403；带上
`http://127.0.0.1:3000` 重新部署后通过。这是部署参数问题，不是制品问题。

**已实机**（0.22.0，`session.mode=virtual`；直接读服务 HTTP 的原始 JSON，绕开局中适配器的
重序列化）：

| 检查 | 实际结果 |
| --- | --- |
| `start` 的 `observation.screen` | `{"width":1080,"height":2400,"rotation":0,"display_id":119}` |
| `app {kind:"open"}` 回执 `display_id` | `119`，与观察中的 `screen.display_id` 一致 |
| `shell {command:"input swipe 540 1800 540 600 300"}` | `executed`/`exited`/`exit_code=0`；随后的 `observe(full=true)` 与前一帧**逐字节相同**（树 5104 字节） |
| `shell {command:"input -d 119 swipe 540 1800 540 600 300"}` | 同一回执；随后的 `observe(full=true)` 与前一帧**不同** |
| `snap` 的顶层 `image.screen` | `{"width":1080,"height":2400,"rotation":0,"display_id":119}` |

即 §3 的静默失败在 0.22.0 上仍如实在存（未改 shell 执行语义），而调用方现在能直接读到寻址
所需的显示身份，`observation.screen`、`app.display_id`、`image.screen` 三处取值一致。

独立探针（新产物）：

| 通道 | 结果 |
| --- | --- |
| 主机 `stdio`（`build/device-mcp.exe`） | `RESULT PASS`：`instructions 6308/6308 source_match=true`、`tools 13`、`catalog description=identical schema=semantic_equal`、两份清单指纹 `match`，`start`/`observe`/`snap`/`wait_for`/`shell`/`inspect`/`act`/`end`/取消恢复全 PASS |
| 手机 `http-remote`（`127.0.0.1:18883/mcp`） | `RESULT PASS`，同样 13 工具，`catalog_fingerprint=bf9aecee…` |

**依赖方切换未执行**：DSH 当前托管的是上一版适配器进程，两次部署都提示
"restart the host MCP process to rediscover tools"。本记录的实机数据全部经服务 HTTP 直接
取得，不依赖该进程；宿主重启后的端到端回放属用户侧动作。

## 8. 未覆盖项与原因

| 项 | 原因 |
| --- | --- |
| 主屏会话下 `display_id=0` 的实机样本 | 本轮设备停留在虚拟屏模式，未构造主屏会话；该值由 `screenOf` 与契约同源，未单独取证 |
| `-d` 对其它 `input` 子命令（`keyevent`/`tap` 等）的行为 | 只实测了 `swipe` 与 `text`；`text` 只验证了"不报错"，未验证是否真的注入 |
| `-d <不存在的 id>` 的 0.22.0 复核 | §3.1 已在 0.21.0 上测过；`shell` 执行语义未改，结论沿用，未重复取证 |
| 经 DSH 宿主的端到端回放 | 宿主仍是上一版适配器进程，需用户重启后重新发现工具 |
| `-d` 对其它 `input` 子命令（`keyevent`/`tap` 等）的行为 | 只实测了 `swipe` 与 `text`；`text` 只验证了"不报错"，未验证是否真的注入 |
| 旧服务（不发 `display_id`）+ 新客户端 | 由 `decode_response_test.go` 的 forward-compatibility 用例覆盖，未构造真实旧服务 |

## 9. 与 issue 原文的差异

issue §6 称"第 1、2 条（文档 + 暴露 `display_id`）不构成契约变更"。按本仓库
`docs/release.md:46` 的判据，这个结论**取决于新成员是否必填**：若要求客户端必须收到，属
"必需形状"变化，需前进契约；本次取可选增量，故契约保持 20。该条按现行规范成立，但理由与
原文不同，记在此处以免下次误读。
