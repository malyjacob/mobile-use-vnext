# act 坐标目标点证明实现与验证记录

状态：2026-11-09。本页记录提案
[act 的坐标目标按“点解析节点”证明派发前未变化](../proposals/implemented/act-coordinate-point-proof.md)
的实现落点、单元测试与实机验证。**实机部分已在 `24117RK2CC` 上执行**；未构造的场景在
§6 明确列出，不把单元测试或代码路径检查写成实机结论。

## 1. 环境与边界

| 项 | 值 |
| --- | --- |
| 实现提交 | `2f43ef1d8c6150dde3d1038ce5ed2d31caeecfac` |
| 工作区 | `E:\Code\Python\mobile-use-vnext` |
| Go | `go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2` |
| 构建目标 | `linux/arm64`，`CGO_ENABLED=0` |
| 发布 | `0.4.0`；`contract_version=2`；Android `versionName=0.4.0`、`versionCode=3` |
| 清单 | `source_commit=2f43ef1d8c6150dde3d1038ce5ed2d31caeecfac`，`source_dirty=false` |
| 产物 | `device-service` SHA-256 `d8d3c86c…`、`device-mcp` `97e184b…`、`control.apk` `42d036a…`；工具目录 `12/eed72b3…` |
| 设备 | `24117RK2CC`（`ro.serialno=17efb04a`），Android 16（API 36），arm64 |
| 部署 | chroot `/data/local/chroots/debian13`；服务 `127.0.0.1:18880`；主机转发 `18881` |
| 改动边界 | `internal/engine`（新增 `point.go`、用例与 `act.go` 重验路径）、`internal/protocol/errors.go`、`internal/eventlog/log.go`、`internal/mcp/schema.go`、`docs/interface.md` |

公共请求/结果字段与 `contract_version` 未变；新增的公开面只有 422 错误码
`ambiguous_point` 与 MCP `act` 描述中的一句坐标证明说明。
实现提交的 `docs/interface.md` 变更经独立只读文档审计；修正坐标证明失败时的
`target_changed`/`hierarchy_unavailable` 映射缺口后复检为 clean。

## 2. 实现落点

| 提案条目 | 落点 |
| --- | --- |
| 点解析：最深、非 root、非 context、有 bounds；同级并列 = 歧义 | `internal/engine/point.go`：`resolvePoint`、`pointState` |
| 观察侧唯一 / 歧义 / 无节点 | `resolveObservedPoint`；`staticActContentCheck` 在 `inScreen` 之后调用 |
| 静态歧义拒绝：422、不认领、不采集 | `internal/engine/act.go`：`staticActContentCheck`；`internal/protocol/errors.go`：`CodeAmbiguousPoint` 与 422 映射 |
| 新鲜侧证明：身份 + 祖先链唯一，且同点重新解析到同一 raw 节点 | `internal/engine/act.go`：`revalidateActTarget`、`provePointTarget`、`indexOf` |
| 新鲜公开树不推进 ref 连续号 | 复用既有 `Projector.Preview`（`engine.go` 接口，`observation.Builder.Preview`） |
| `swipe` 只证起点 | `point.go`：`coordinatePoint`；`revalidateActTarget` 在 ref 分支之前执行点证明 |
| `text` 证焦点点击点 | 同上；后续 `dispatchText` 的绑定、写入与读回未改 |
| 白名单事件 | `internal/engine/act.go`：`logActPoint`；`internal/eventlog/log.go`：新增 `proven` 键 |
| 文档与 MCP 描述 | `docs/interface.md` §2/§6；`internal/mcp/schema.go` 的 `act` 描述 |
| 用例 | `internal/engine/point_test.go`、`act_point_test.go`；`internal/protocol/errors_test.go` |

## 3. 执行的命令与真实结果

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/engine -run 'TestActCoordinatePoint' -count=1`（实现前，基线） | **红**：浮层、深层新节点、位移、不可见目标、静态歧义、`text` 焦点变化等用例返回 `executed`；事件日志无 `act.point` |
| `go test ./internal/engine -run 'TestActCoordinatePoint|TestResolvePoint|TestCoordinatePoint' -count=1` | `ok` |
| `go test ./internal/protocol -run TestAmbiguousPointStatus` | `ok`（422 映射） |
| `go test ./... -count=1` | 全部 `ok`；`internal/engine` 129.554s，其余包均通过 |
| `go vet ./...` | 无输出 |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过 |
| `powershell -File scripts/build-release.ps1` | 成功；打印 `release=0.4.0 contract=2 android=3 source_commit=2f43ef1… source_dirty=False` |
| `powershell -File scripts/verify-manifest.ps1 -Manifest build/release/manifest.json` | `PASS` |
| `powershell -File scripts/deploy.ps1 -SkipBuild` | 成功；App 身份、版本匹配、App 配对、恢复屏障、STOP 可用性均 `PASS` |
| 部署后二进制复核 | `device-service` 与 manifest 一致；phone MCP HTTP adapter 首次仍为旧 binary（host-managed stdio 进程占用导致 `cp` 静默失败），以 rename 替换并重启后一致；stdio adapter 需宿主重启 |
| 手机 MCP HTTP adapter `tools/list` | 12 个工具；`act` 描述包含“坐标目标会在派发前按点解析节点并证明未变化” |
| `GET /health` | `status=ok`、`contract_version=2`、`release_version=0.4.0` |
| `python .state/verify-act-point.py` | 实机场景通过；结果见 §5 |
| `python .state/verify-settings-change.py` | 实机同包滚动变化通过；结果见 §5 |
| `python .state/verify-settings-navigation.py` | 实机同包导航变化通过；结果见 §5 |
| `python .state/verify-fallback.py` | 未找到可回退点；原因见 §6 |
| 实机 `long_press` 坐标补测 | HTTP 200、`executed`、`act.point state="unique" proven=true` |

单元测试覆盖解析器的三种状态、半开区间边界、root/context 排除、最深层次歧义、坐标目标
选择；引擎路径覆盖浮层、深层新节点、位移、不可见目标、重复新鲜身份、`Preview` 失败、
静态歧义（O 仍 ready、无额外采集）、`text` 焦点点、`swipe` 起点、无节点回退、`ref` 回归
与“认领后不归还”。其中“变化场景”用基线先行证明了红，再随实现转绿。

## 4. 实机用例与结果

设备上由服务真实完成 `start`、`observe`、`act`、`open_app`、`end`；设备输入通过
`adb shell input -d <display>` 注入目标虚拟屏，不经过服务，因此可以制造“观察后屏幕变化”。

| 用例 | 操作 | 真实结果 |
| --- | --- | --- |
| 正常坐标 `tap` | 探测 App 按钮中心，连续 5 次坐标 `act` | 5/5 HTTP 200、`executed`；计数从 0 精确增到 5 |
| `ref` 回归 | 同一按钮按 `ref` `tap` | HTTP 200、`executed`；无新判据介入 |
| 坐标 `text` | 输入框点坐标，写入 `point-proof` | HTTP 200、`executed`；独立观察读回值精确等于写入值 |
| 坐标 `long_press` | 按钮中心长按 | HTTP 200、`executed`；`act.point state="unique" proven=true` |
| `swipe` 起点 | 按钮中心为起点做滑动 | HTTP 200、`executed`；终点不参与证明 |
| 同包节点状态变化 | 观察后向复选框注入一次点击，使 `checked` 变化；再用旧 O 的坐标 `act` | HTTP 409、`not_executed`、`target_changed`、`next=observe`；随后观察确认复选框只被切换一次，服务未派发第二次输入 |
| 同包几何变化 | 打开“设置”首页，观察 128 节点；注入一次列表滚动；再用旧 O 的坐标 `act` | HTTP 409、`not_executed`、`target_changed`、`next=observe`；新树中同一标签节点仍在，但 bounds 已变化 |
| 同包导航变化 | 在“设置”首页注入一次列表项点击进入子页；再用旧 O 的坐标 `act` | HTTP 409、`not_executed`、`target_changed`、`next=observe`；新树节点数 128→35，同一标签仍出现但 bounds 已变化 |

## 5. 实机指标

| 指标 | 结果 |
| --- | --- |
| 构造变化拒绝率 | 3/3（复选框状态变化、设置列表滚动几何变化、设置首页导航） |
| 正常坐标动作误拒率 | 0/8（5 次 `tap`、`text`、`long_press`、`swipe` 均 `executed`） |
| `ref` 回归 | 1/1 `executed` |
| `act.point` 事件 | 11 条：`unique/true` 8 条、`unique/false` 3 条；`none`/`ambiguous` 未在实机构造 |
| 证明耗时 | 11 条事件均为 `ms=0`；事件字段是整数毫秒，实际证明耗时低于 1ms 分辨率；满足“<10ms”目标 |
| 设备读取 | 证明路径只调用 `Preview`，没有新增 `dev.*` 调用；实机未单独计数读取次数 |
| 端到端清理 | 每次会话 `end` 后 `cleanup=complete`、`can_start=true` |

## 6. 未执行项与原因

| 项 | 原因 |
| --- | --- |
| 实机 `422 ambiguous_point` | 探测 App（9 个公开节点）与设置首页（128 个公开节点）均未找到同级重叠的最深候选；无法从现有界面构造“点本来就有歧义”。单元测试覆盖唯一/歧义/无节点与静态拒绝不认领、不采集 |
| 实机“点无节点”回退 | 未执行：探测 App 的公开树有覆盖全屏的非 root 容器，屏幕内每个点都会解析到节点；设置首页未做逐点回退扫描。单元测试覆盖该回退并断言 `state=none` |
| 同包 Dialog/BottomSheet 遮挡 | 探测 App 没有 Dialog/BottomSheet，设置首页的现有路径未打开同包浮层；未使用生产 App 或构造破坏性操作。单元测试用全屏同级浮层与按钮内深层新节点覆盖了拒绝路径 |
| WebView/canvas 内部内容 | 没有 WebView 测试页；这是提案 §7.2 已知边界（树只暴露容器，容器身份不变时判据会通过但没有内容级证明） |
| 大树 p95 | 只测了 9 与 128 个公开节点的树；更大树上的 CPU 成本未采样 |
| `go test -race` | Windows 宿主无 C 编译器，`CGO_ENABLED=0` 目标下未运行；与既有记录一致 |
| MCP 传输层坐标动作 | 坐标证明在引擎/HTTP 路径完成；MCP 只做工具投影与投影描述，未单独通过 MCP 宿主发起坐标动作。发布清单的工具目录指纹已随描述更新并核验 |
| 显示几何代次变化但 bounds 未变 | 提案 §7.2 已列为既有缺口，本轮不修；实机只覆盖了 bounds 实际变化的滚动场景 |

## 7. 结论

- **已实现**：静态点解析与歧义拒绝、派发前节点级证明、`ambiguous_point`、`act.point`
  白名单事件、MCP 描述与 `docs/interface.md`。
- **已单测**：解析器三态、边界、最深节点、root/context 排除；浮层、深层新节点、位移、
  不可见、重复身份、`Preview` 失败、静态歧义、`text`、`swipe`、无节点回退、`ref` 回归、
  O 不归还。
- **已实测**：正常坐标 `tap`/`text`/`long_press`/`swipe` 证明与派发；同包状态变化、几何
  变化与导航在派发前返回 `target_changed`；`ref` 无回归；实机 `act.point` 耗时低于整数
  毫秒分辨率。
- 未构造的实机场景已在 §6 列出；它们不改变“实现已落地”的结论，只界定实机证据边界。
