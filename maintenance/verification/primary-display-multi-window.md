# 主屏多窗口层级只发布首个窗口的缺陷修复

日期：2026-11-11。
代码基线：`29a651328956689e79936bd1ad51050e62b298a4`。
范围：主屏（display 0）层级捕获与公开树投影；不包含服务端重新部署或手机实机复测。
环境：Windows/amd64，`go1.27.1`，`GOEXPERIMENT=jsonv2`，`CGO_ENABLED=0`；
设备为在线 Android 测试机。

## 1. 现象、触发条件与分诊

Codex 经 stdio MCP 适配器接入选主屏后，`start` 返回 `mode=primary`，截图能读到真实桌面；
截图中的桌面和应用页面均正常，但公开树只有 26 个状态栏节点。打开设置后页面已切换，公开树
仍只有这 26 个节点，应用窗口内容完全不可见。同一操作在虚拟屏下能读到完整应用树。

在主机经一次性 `adb forward` 直接读取同一时刻的 u2 `dumpWindowHierarchy`，原始结果有
153 个节点，全部属于 display 0，并由 5 个顶层窗口组成。说明设备采集并非空树，丢失发生在
服务的解析与投影之间。原始 XML 未入库，调试转发与临时文件已清除。

分诊：缺陷。`docs/interface.md` 的公开树 scope 是目标屏上报告可见的节点及其必要祖先；
主屏同一显示上的多个顶层窗口都属于该 scope。实现只沿一个顶层窗口建立遍历树，与现行规范
不一致，不涉及契约取舍，也不需要提案。

## 2. 先红复现与修复后回归

回归用例：
`internal/android/transport_test.go:125` 的
`TestCaptureConnectsMultipleTopLevelWindows`。它构造同一 display 下的状态栏窗口和
应用窗口，断言应用窗口中的 `WLAN` 既能从 `Capture.Root` 到达，也出现在
`observation.Builder.Build` 的公开树中。

为验证最终用例在基线上确实为红，使用临时 Go overlay 将
`internal/android/device.go` 与 `internal/android/hierarchy.go` 替换为 `HEAD` 版本，
不修改工作区：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test -overlay <临时 overlay> ./internal/android `
  -run '^TestCaptureConnectsMultipleTopLevelWindows$' -count=1 -v
```

修复前实际结果：退出码 `1`，
`the second top-level window is not reachable from capture root 0`，用例失败。

修复后：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/android -run '^TestCaptureConnectsMultipleTopLevelWindows$' -count=1 -v
```

实际结果：退出码 `0`，该用例通过。

相关包与全仓回归：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
go test ./internal/android ./internal/observation -count=1
go test ./... -count=1
$env:GOOS = 'linux'; $env:GOARCH = 'arm64'; $env:CGO_ENABLED = '0'
go build ./...
```

实际结果：以上命令退出码均为 `0`；`internal/android`、`internal/observation` 通过，
`internal/engine` 在 165.908 秒内通过，其余包全部通过；linux/arm64 交叉构建退出码为 `0`。

## 3. 根因

基线代码的因果链：

| 位置 | 事实与后果 |
| --- | --- |
| `internal/android/hierarchy.go:118-122` | `parseHierarchy` 只返回一个 `keptRoot`；同一 display 的其它顶层窗口虽然保留为节点，但都没有从该根可达 |
| `internal/android/device.go:845` | `Capture` 直接接收并使用这一个 `root` |
| `internal/android/device.go:853-863` | 基线只在“节点数为零”时创建合成显示根；多顶层窗口不会进入该分支 |
| `internal/observation/build.go:89`、`:105-106` | 公开树只从 `cap.Root` 的后序遍历和根子节点建立；其它 `Parent == -1` 的顶层窗口整棵丢弃 |

主屏 dump 的顶层顺序中状态栏窗口先出现，因此 `keptRoot` 选中状态栏；应用窗口作为同级
顶层窗口被排除在遍历之外。虚拟屏通常只有目标应用一个顶层窗口，所以同一缺陷此前没有暴露。

## 4. 改动边界与验证层次

- `internal/android/hierarchy.go` 新增 `displayRootNode` 与
  `connectDisplayWindows`；当一个 display 上有多个 `Parent == -1` 节点时，追加一个
  合成显示根并重新连接子节点。单顶层窗口保持原根不变。
- `internal/android/device.go` 的 `Capture` 对非空结果调用上述连接；空屏分支复用
  `displayRootNode`，不改变空屏语义。
- 未修改公开字段、错误码、契约版本、发行版本、Android 产物、恢复账本或 `docs/`。
- 已实现：是，提交 `28cc6ef7214d94a1cbb981e7d2995397dee660d2`。
- 已单测：是，定向用例、相关包与全仓测试均通过。
- 已实测：0.4.3 整组产物按清单部署到手机，`source_commit` 为上述提交、
  `source_dirty=false`，`device-service` SHA-256 为
  `bcbb5e7952aafb3fc07ea94c69b54ff6b863661048eb03c328cb0b9e205fc6e1`。
  主屏 `start` 的公开树有 97 个节点；打开设置后为 129 个节点，`WLAN` 等应用内容可见；
  点击 WLAN 后的后观察为 70 个节点；`HOME` 返回桌面的后观察为 97 个节点。结束会话后
  `cleanup.state=complete`、`can_start=true`。
- 主机适配器已重建为 `build/device-mcp.exe`，SHA-256 为
  `B2CCEBA71E98842D066A5C6DA1E98BD3269EFDD76CF49666B61049B151FE16F6`；Codex 宿主重启
  后的实际进程路径已指向该文件。
- 修复提交：`28cc6ef7214d94a1cbb981e7d2995397dee660d2`；本记录在后一次维护提交中补充
  部署与实机验证。

部署与主机适配器构建命令：

```powershell
.\scripts\build-release.ps1
.\scripts\verify-manifest.ps1 -Manifest build/release/manifest.json
.\scripts\deploy.ps1 -SkipBuild
$env:GOEXPERIMENT = 'jsonv2'; $env:CGO_ENABLED = '0'
go build -trimpath -buildvcs=false -o build/device-mcp.exe ./cmd/device-mcp
```

部署前和部署后均运行独立 stdio MCP 探针：12 个工具、schema、`start`、`observe`、
`apps`、`shell`、`inspect`、`act`、`end` 与取消清理全部 `PASS`，最终 `RESULT PASS`。

## 5. 同类核查

- `parseHierarchy` 的唯一生产调用点是 `Device.Capture`；没有第二条层级解析或投影入口。
- `observation`、`settle`、点选解析和公开树消费者都以单一 `cap.Root` 为入口；本次修复在
  捕获边界恢复“一个显示一个遍历根”的不变量，下游无需增加第二套窗口遍历。
- 单窗口捕获仍使用原窗口作根，既有 `TestCaptureIsReadOnlyAndFiltered` 的
  `capture.Root == 0` 断言继续通过。
- 空 display 的合成根与多窗口合成根共用 `displayRootNode`，显示 ID、尺寸、可见性和启用
  事实保持一致。

## 6. 未覆盖项与原因

- 未在分屏、自由窗口、输入法或其它厂商 ROM 上逐项构造多窗口样本；本次逻辑按顶层节点
  数量而非窗口类型连接，但没有这些环境的实机证据。
- 没有保留原始层级 XML、截图或窗口包名，无法用现场数据做离线回放；复现改为仓库内的
  合成 RPC 用例。
