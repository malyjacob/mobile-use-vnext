# 默认观察树坐标省略实现与验证

实现提交：`68384230cbb31abf226fdeb1398860d38937233c`（`master`，2026-11-19），父提交
`697fea951153b59a2f48437d7a06951e39eb419c`。
提案：[compact-observation-bounds.md](../proposals/implemented/compact-observation-bounds.md)
（归档时补 §11）。
版本：`0.20.0` / `contract_version=20` / Android `versionCode=22`。
本次涉及手机实机、整组构建/覆盖部署、Windows 主机两个 exe 重建与两种独立探针；固定
`u2.jar` 与 `applist.dex` 未修改。所有验证只用本仓库源码、已声明版本依赖与固定制品。

## 1. 范围与结论

按提案改变默认观察树（`view=compact`）的已知矩形交付判据：合成根与 `actions` 非空的节点
保留矩形；匿名普通 `view`/`group` 与无动作 `text` 省略矩形；其余展示节点在矩形（裁剪到
屏幕后）与父链上最近实际交付的矩形四边完全相同、或可见面积严格小于合成根面积 1% 时省略。
`detailed` 视图、三态编码、`set` 的 `bounds:"hidden"`/`null`/四整数语义和 70% 整树回退
不变。

自动验证、整组构建/清单核验、手机覆盖部署、主机工具重建、两种 MCP 入口探针与实机最小
闭环均完成。实机受控测试页上三个 full 帧的独立策略复算均 `0 mismatch`：动作节点全部保留
坐标、文字坐标全部省略、`inspect` 能补回隐藏文字的原矩形；一次 `tap` ref 动作
`executed`，`end` 后 `can_start=true`。

## 2. 实现落点

| 条目 | 代码落点 |
| --- | --- |
| compact 判据顺序、最近已交付矩形传递、裁剪与 1% 面积比较 | `internal/observation/read.go` `ProjectCompactTree`/`projectCompactNode`/`compactHidesBounds`/`compactRect`/`belowPercent`/`boundsArea` |
| 详细投影与三态保持 | `internal/observation/read.go` `projectDetailedNode`/`projectReadNode` |
| 差分与链编码不变 | `internal/observation/read_diff.go`、`PackTree`/`PackDelta` 未改语义 |
| 新增判据/边界/重复/差分/折链回归 | `internal/observation/read_bounds_test.go`、`internal/observation/read_test.go` |
| MCP 全局树说明与回归 | `internal/mcp/schema.go` `treeTextGuide`、`internal/mcp/schema_test.go`、`internal/mcp/tree_text_test.go` |
| 连续性差分夹具保持 70% 判据焦点 | `internal/engine/observation_continuity_integration_test.go` |
| 版本源与生成件 | `release/versions.json`、`internal/protocol/version_generated.go`、`GeneratedVersion.java`、`strings_version.xml`、`AndroidManifest.xml` |
| 正式文档 | `README.md`、`docs/architecture.md`、`docs/client.md`、`docs/control-protocol.md`、`docs/interface.md`、`docs/integration.md`、`docs/mcp.md`、`docs/phone-operations.md`、`docs/release.md`、`docs/runbook.md` |

## 3. 自动验证

执行环境：Windows 10.0.19044 x64，Go `go1.27.1 windows/amd64`，`GOEXPERIMENT=jsonv2`。

```text
GOEXPERIMENT=jsonv2 go test -count=1 ./internal/observation/ ./internal/mcp/ ./internal/protocol/
GOEXPERIMENT=jsonv2 go test -count=1 -timeout 1800s ./internal/engine/
GOEXPERIMENT=jsonv2 go test -count=1 ./internal/protocol/ ./internal/client/ ./internal/httpapi/ \
  ./internal/store/ ./internal/android/ ./internal/device/ ./internal/imaging/ \
  ./internal/eventlog/ ./cmd/... ./scripts/...
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
powershell scripts/generate-version.ps1 -Check
```

结果：全部通过。`internal/engine` 全包 `ok` 556.109s（474 个测试函数）；其余包均 `ok`；
`generate-version.ps1 -Check` 输出
`version source consistent: release=0.20.0 contract=20 android_version_code=22`；
linux/arm64 交叉构建 exit 0。

新增/更新的自动证据：

- `read_bounds_test.go`：动作节点（含小于 1% 的按钮）保留坐标；只有控件角色而无动作的节点
  仍按面积/重复判断；无动作 `text` 无论大小都省略；未知几何保持 `null`；面积严格小于、
  等于、大于 1% 三种边界；越屏矩形按裁剪后的可见面积判断（恰好 1% 保留、再外移省略）；
  换屏重算分母；重复判据使用裁剪后矩形；直接重复、隔未知/隐藏分组层、更高祖先相同但最近
  交付者不同、动作节点完全重合、大封面独立位置；投影不修改内部树；详细视图恢复原矩形；
  已知/隐藏/未知六向差分转换与 wire 往返；仍隐藏的平移不产生坐标 `set`；隐藏文字/图片
  不进入 chain，既有匿名链仍可还原；屏幕缺失/面积溢出时跳过面积规则。
- `read_test.go`：原未知状态用例按新规则核对坐标，保留未知布尔与折链断言。
- `internal/mcp`：全局树说明固定新判据；用真实 compact 投影渲染节点行，确认动作节点保留
  几何列、文字省略几何列、跨 1% 阈值时输出 `~ n4 bounds=hidden`。
- `internal/engine`：I02 固定基线差分链夹具增加 8 个稳定文字叶。新策略缩短 full 后，
  原夹具的差分恰好越过 70% 门槛而转 full；加稳定叶只把用例焦点保持在差分链，不放宽
  70% 判据。附带把 `continuationCapture` 的可选叶挂到根下、按 raw 顺序追加，保持公开
  preorder 单调。

反向对照（临时移除判据后运行 `go test ./internal/observation/`，随后恢复）：

| 移除的判据 | 预期转红的用例 | 结果 |
| --- | --- | --- |
| 无动作 `text` 省略 | `TestCompactGeometryProtectsActionPositions`、`TestCompactProjectionLeavesInternalTreeUnchanged` | 红 |
| 与最近交付矩形重复 | `TestCompactGeometryAreaBoundary`、`TestCompactGeometryDuplicateRules`、`TestCompactGeometryDeltaTriStates`、`TestUnknownStateNodesKeepBoundsAndStayOutOfChains` | 红 |
| 1% 面积 | `TestCompactGeometryProtectsActionPositions`、`TestCompactGeometryAreaBoundary`、`TestCompactProjectionLeavesInternalTreeUnchanged`、`TestCompactGeometryDeltaTriStates` | 红 |
| 动作位置保护 | `TestCompactGeometryProtectsActionPositions`、`TestCompactGeometryDuplicateRules`、`TestGeometryStatesAreDistinct` | 红 |

恢复实现后同包测试重新 `ok`。

## 4. 构建与清单

在干净实现提交 `6838423` 上执行：

```text
powershell scripts/build-release.ps1
powershell scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
powershell scripts/build-host-tools.ps1
powershell scripts/build-host-tools.ps1 -VerifyOnly
```

结果：

- 手机清单：`release=0.20.0`、`contract=20`、`android=0.20.0/22`、
  `source_commit=68384230cbb31abf226fdeb1398860d38937233c`、`source_dirty=false`、
  `tool_catalog=13/99dd22d0a81280b6875eaa770b81ee0f553e8a6b9e49f4bbb0c83078f25494c8`；
  `verify-manifest.ps1` PASS，6 个产物齐全。工具目录指纹与 0.19.0 相同，因为本次只改
  server instructions 与观察几何，未改工具名、描述或入参 schema。
- 主机清单：同一 `source_commit`、发行/契约与工具目录；`-VerifyOnly` PASS，手机清单
  companion PASS。

产物：

| 产物 | SHA-256 | 字节 |
| --- | --- | --- |
| `device-service` | `793f689442bd2f3ed488936350243112b30be4dc40faddad37646fb330c12d73` | 9044128 |
| `device-mcp`（linux/arm64） | `e5ad0da86e385b9960b41647d26f7cf5124f4b8fd4716eadb91a3df91561e867` | 9175200 |
| `control.apk` | `5bd34a3ebee3df23f9bc6fcce2902e3480c4d7c935168fc747323cd763cb1e7b` | 99869 |
| `u2.jar` | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` | 3306229 |
| `applist.dex` | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` | 4316 |
| `service.sh` | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` | 3643 |
| `build/device-mcp.exe` | `46c0a2c640238432fa85b677d20209ba73a7b4b590f067bdf3e2d95e7ae81323` | 10112000 |
| `build/mcp-probe.exe` | `a1a5935eb2dd46168d99569b6ea8039c7ef50fd993a25bf2d7b0994dd9511ec1` | 9738240 |

两份清单的 `source_commit` 都锚定实现提交 `6838423`。归档提交只改
`maintenance/`，不重建产物；之后主树 HEAD 不再等于该源码提交，`verify-manifest.ps1` 与
`build-host-tools.ps1 -VerifyOnly` 的精确 HEAD 门禁会拒绝在当前 HEAD 复用这两份清单，
需要复核时应从对应源码提交重建。

## 5. 整组部署与实机闭环

设备：`adb` 序列 `17efb04a`，型号 `24117RK2CC`（`zorn`），目标虚拟屏 `1080x2400`。

`scripts/deploy.ps1 -SkipBuild -McpAllowedOrigins http://127.0.0.1:3000` 覆盖安装。部署
输出核对：App 为 `0.20.0/22`；`device-service`、`device-mcp`、固定制品、`config.json`、
`mcp-config.json`、`connection.json` 的部署哈希均与清单匹配；`/health` 返回
`release=0.20.0`、`contract=20`；`can_start=true`；手机 MCP HTTP 适配器监听
`127.0.0.1:18882`（主机转发 `18883`）。

过程事实：第一次部署未传 `-McpAllowedOrigins`，手机 HTTP 探针的 `origin_allowed`
返回 `403`；按既有部署配置带 `http://127.0.0.1:3000` 重新执行同一 `-SkipBuild` 部署后
PASS。该次修正只改部署参数，没有改代码、token 或设备配置，也未使用 `*`。

部署后手机进程 `/proc/<pid>/exe` 哈希：

| 进程 | SHA-256 | 与清单 |
| --- | --- | --- |
| `device-service` | `793f689442bd2f3ed488936350243112b30be4dc40faddad37646fb330c12d73` | 一致 |
| `device-mcp -transport http` | `e5ad0da86e385b9960b41647d26f7cf5124f4b8fd4716eadb91a3df91561e867` | 一致 |
| `device-mcp -transport stdio` | `e5ad0da86e385b9960b41647d26f7cf5124f4b8fd4716eadb91a3df91561e867` | 一致 |

第一次部署告警“stdio 旧二进制仍在运行”；依赖方宿主随后自行重启了该 stdio 进程，第二次
部署确认无旧映像，上表为重启后的实际进程。

实机最小闭环使用受控测试目标 `com.mobileuse.probe`，通过文档化 HTTP 契约执行，只记录聚合
事实，不写树、标签、坐标正文或截图到仓库：

```text
status({}) can_start=true
app open com.mobileuse.probe outcome=executed
observe(full=true) -> 受控测试页 full
act tap(ref=TAP 按钮) outcome=executed -> 新 observation
observe(full=true) -> 点击后 full
inspect(历史 O, node=隐藏文字 ref) -> detailed 子树
end -> session.state=ended, cleanup complete
```

三个 full 帧上用独立脚本按 §4 重新计算每个节点的几何状态，与实测树逐节点比较：

| 指标 | 起始帧（空虚拟屏） | 受控测试页 | 点击后 |
| --- | ---: | ---: | ---: |
| 已知坐标节点 | 1（根） | 9 | 9 |
| 隐藏坐标节点 | 0 | 5 | 5 |
| 未知坐标节点 | 0 | 0 | 0 |
| 状态判据 mismatch | 0 | 0 | 0 |
| 有动作但隐藏坐标 | 0 | 0 | 0 |
| `text` 角色却交付坐标 | 0 | 0 | 0 |
| 有动作且坐标已知 | 0 | 8 | 8 |

受控页隐藏的 5 个节点中 4 个是 `text`（文字坐标全部省略），另 1 个是无动作小图标（面积
低于 1%）。`inspect` 对隐藏文字 ref 返回的 detailed 子树带原四整数 `bounds`。动作按钮
（含最小面积 86,400 px²）全部保留坐标，ref `tap` 返回 `executed` 并在点击后观察中看到
`taps` 文本更新。会话 `end` 后 `/api/status` 为 `can_start=true`，cleanup
`state=complete`、execution/tasks/recents/display 均 `confirmed`。

## 6. 独立探针（主机与手机）

主机 stdio 探针（新 `build/mcp-probe.exe` + 新 `build/device-mcp.exe`，两份清单核验）：

```text
./build/mcp-probe.exe -config .state/mcp-config.json -binary build/device-mcp.exe \
  -transport stdio -result-mode both -timeout 180 \
  -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

`RESULT PASS`：连接、instructions 逐字节一致（5,578 B，`2026-07-28` 与兼容
`2025-11-25` 两条握手）、13 工具、目录语义一致、两份清单指纹一致、`start/observe/snap/
wait_for/shell/inspect/act/end/cancel` 全部 PASS。

手机 HTTP MCP 探针（经 `18883/mcp` 转发到手机 `18882`）：

```text
./build/mcp-probe.exe -transport http-remote -config .state/probe-phone-config.json \
  -result-mode both -timeout 180 \
  -manifest build/release/manifest.json -manifest build/host-tools-manifest.json
```

`RESULT PASS`：`service_remote contract=20 release=0.20.0`、认证/Origin 边界、instructions
同源、13 工具、两份清单指纹、结果投影与完整最小闭环全部 PASS，`end` 后
`can_start=true`。

## 7. 样例回放与 85% 门槛对照（未执行）

提案 §8.2.1 的三份附件（X 搜索结果、网易云搜索页、B 站推荐页）与原始节点树不在本工作区
或本次可访问的私有材料中，无法按 `37 / 34 / 44` 坐标数与
`4,229 / 7,213 / 6,951 B` 做逐节点离线复算。该边界由 `read_bounds_test.go` 的合成夹具
覆盖；实机结果见 §5，不冒充附件复算。

提案 §8.2.3 的 85% 差分门槛对照需要含确认基线的连续滚动帧序列；本次未取得同段连续帧，
仍按已定结论沿用 70%，没有把 85% 写成实测结论。

## 8. 责任边界与未执行项

| 项 | 结论 |
| --- | --- |
| 手机整组构建、清单核验、覆盖部署、App/服务/手机 HTTP/stdio 适配器版本与哈希 | 本次执行 |
| 主机 `build/device-mcp.exe`、`build/mcp-probe.exe` 重建与替换 | 本次执行；哈希见 §4，`-VerifyOnly` PASS |
| 主机 stdio 与手机 HTTP 两条独立探针 | 本次执行，均 `RESULT PASS` |
| 依赖本适配器的 Agent 宿主重连/重启与 `tools/list` 刷新 | 用户自行处理，未计入独立探针；未停止或重启任何依赖方宿主 |
| 三份附件逐节点回放与 MCP 文本字节上限 | 未执行：材料不在本工作区，原因见 §7 |
| 85% 差分门槛对照 | 未执行：缺连续滚动帧，原因见 §7 |
| 实机大树/宿主截断/真实应用的逐节点坐标核对 | 未执行：本次按最小实机闭环验证受控页面 |
| `verify-m6` 与 `act` 重验失败交付的实机样本 | 未执行：不在本次最小闭环范围 |

文档审计：本次实现提交涉及 `docs/`，独立只读审计初检发现 9 处旧契约头示例与
`docs/integration.md` 未同步；修订后复检结论为 `CLEAN`。
