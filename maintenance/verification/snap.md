# snap 只读截图实现与实机验证

实现提交：`de03df9f60ae67881b6397fda772566bfcfb6779`（`master`，2026-11-18）。
提案：[snap.md](../proposals/snap.md)（归档时补 §11）。
基线：`1019c356b24b62e3a9fb90b29ef075258c848404`。
版本：`0.19.0` / `contract_version=19` / Android `versionCode=21`。
固定 uiautomator 制品：`third_party/uiautomator/u2.jar`，SHA-256
`28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3`。
本次涉及手机实机、整组构建/覆盖部署、主机二进制重建和两种 MCP 入口探针；没有修改固定 JAR。
所有验证只用本仓库源码、已声明版本依赖与固定制品，没有访问其他项目。

## 1. 范围与结论

按提案实现第 13 个工具 `snap(reference)`：在活动会话绑定的屏幕上读取一张图，不读取节点树、
不派发输入、不发布观察、不确认基线、不续租、不认领或退休 O。HTTP `/api/snap` 成功只返回
`operation_id` 与顶层 `image`；MCP `snap` 交付同一张 JPEG 与一份 `ImageContent`，三种结果
模式都只带元数据文本/结构化，不带 base64。控制 App 新增“正在截图”与
`snap.capture`/`snap.image`/`snap.commit` 阶段文案。

自动验证、整组构建/清单核验、手机覆盖部署、主机工具重建与实机最小闭环均完成。实机闭环
`start → keepalive(1) → snap(O) → status(O/P) → observe(image=true) → end` 全部 PASS；
JPEG 可解码且尺寸与元数据一致；原 O 在 snap 后仍为 `ready`/`effective`，`expires_at` 保持
显式一分钟续租值；snap 未递增 revision，之后的带图 `observe` 才发布下一个 O。

## 2. 实现落点

| 条目 | 代码落点 |
| --- | --- |
| P 登记/去重、引用解析、工作位绑定在同一状态锁内完成 | `internal/engine/snap.go` `admitSnap`；`internal/engine/operations.go` `registerOperationLocked` |
| 目标屏探测、一次源图读取、SFID 复核、一次编码 | `internal/engine/snap.go` `runSnap`；`internal/engine/capture.go` `readImageForAcquisitionOnDisplay`/`encodeImageForDisplay` |
| 提交门禁、单次图片交付、无 O/基线/续租 | `internal/engine/snap.go` `commitSnap` |
| 未提交终态冻结（撤销/取消/截止）与晚到结果抑制 | `internal/engine/snap.go` `snapUncommittedOutcomeLocked`/`finishSnapPhase`；`internal/engine/retirement.go` `freezeSnapLocked`/`applyFrozenResult` |
| 请求/响应严格接入与顶层 `image` 字段顺序 | `internal/protocol/requests.go`、`internal/protocol/encode.go`、`internal/protocol/responses.go`、`internal/protocol/decode_response.go` |
| HTTP 路由与 1 MiB 带图投递预留 | `internal/httpapi/server.go` |
| Go client `Snap` 与图片校验 | `internal/client/client.go` |
| MCP 第 13 工具、只读注解、取消不发 end、三种模式投影 | `internal/mcp/schema.go`、`internal/mcp/server.go`、`internal/mcp/result_value.go`、`internal/mcp/result_text.go` |
| 控制 App 文案 | `android-control/app/src/main/java/com/mobileuse/vnext/ControlState.java` |
| 探针 snap 检查与 13 工具目录断言 | `scripts/mcp-probe/checks.go`、`scripts/mcp-probe/result.go`、`scripts/mcp-probe/catalog.go` |
| 版本源、生成常量与正式文档 | `release/versions.json`、`internal/protocol/version_generated.go`、Android 生成文件、`docs/*`、`README.md` |

## 3. 自动验证

执行：

```text
GOEXPERIMENT=jsonv2 go test ./cmd/... ./internal/... ./scripts/...
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOEXPERIMENT=jsonv2 go build ./...
powershell scripts/generate-version.ps1 -Check
powershell android-control/test.ps1
powershell android-control/build.ps1
```

结果：全部通过。Go 全套中 `internal/engine` 557.011s，其余受影响包
`protocol`/`client`/`httpapi`/`mcp`/`mcp-probe` 均 `ok`。Java JVM 检查
`DisplayAttemptGateTest` 与 `MessageListTest` PASS；APK 构建后 `aapt2 dump badging`
显示 `versionCode=21`、`versionName=0.19.0`。

新增/更新的自动证据：

- `internal/engine/snap_test.go`：成功交付一张图且 revision/current/baseline/expires 不变，
  原 O 仍 `ready`；重复 P 不重读不重编码且无图；`status(P)` 无图无 `recovery`；
  未知 O、异主、过期租期、自引用、封闭 P、设备忙、设备不可用；
  显示不可用、SFID 变化、源图超龄、编码失败映射；读取中 STOP 丢弃图片；
  请求取消（含准入前取消）不撤销会话；编码期间显示代次变化为 `stale_screen`；
  工作截止冻结后晚到结果只重放 `work_deadline` 摘要。
- `internal/protocol`：snap 请求严格解码/编码与非法引用；顶层 `image` 解码、显式 `null`
  拒绝与 `saved/image/session` 字段顺序。
- `internal/client`：顶层图片解码与 base64 清除；必填元数据、`read_only`、`screen`、
  JPEG 时间与 base64 校验；顶层图片与 `error`/`outcome`/`observation` 并存拒绝；
  无图重复 P 摘要正常解码。
- `internal/mcp`：第 13 工具与唯一 `readOnlyHint=true`；三种结果模式各一个
  `ImageContent`、元数据一致、正文无 base64；无图摘要 `isError=true`；
  取消不发起 `end`、清空图片并返回 `call_cancelled`；已知提交结果在取消竞态中同样丢弃。
- `scripts/mcp-probe`：13 工具目录、只读注解断言、snap 顶层图片检查与
  两处 PASS 期望；主机 stdio/http 与 http-remote 探针均实机 PASS（见 §5）。

## 4. 构建与清单

在干净实现提交 `de03df9` 上执行：

```text
powershell scripts/build-release.ps1
powershell scripts/verify-manifest.ps1 -Manifest build/release/manifest.json
powershell scripts/build-host-tools.ps1
powershell scripts/build-host-tools.ps1 -VerifyOnly
```

结果：

- 手机清单：`release=0.19.0`、`contract=19`、`android=0.19.0/21`、
  `source_commit=de03df9f60ae67881b6397fda772566bfcfb6779`、`source_dirty=false`、
  `tool_catalog=13/91fed94c832e6b80eee98df4c6c4d0053819452ed6e630e25f8700cfb483f1c9`；
  `verify-manifest.ps1` PASS，6 个产物哈希与大小一致。
- 主机清单：同一 `source_commit` 与工具目录，`-VerifyOnly` PASS；手机清单 companion PASS。

产物：

| 产物 | SHA-256 | 字节 |
| --- | --- | --- |
| `device-service` | `eee70a242048820cc5e9fe550e950afb79c1a35c9b305b8f68e64a0f5bd7effa` | 9044128 |
| `device-mcp`（linux/arm64） | `7887889d8b2235eb993ca1998cd6cc159d33c4a5a8bd205eba10e9c03d3265a3` | 9175200 |
| `control.apk` | `ce5887c8eb4ffef190d60b11d8b3138fc2fcd7d908f650a6aaa9baa6a1164dd4` | 99869 |
| `u2.jar` | `28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3` | 3306229 |
| `applist.dex` | `63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8` | 4316 |
| `service.sh` | `4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0` | 3643 |
| `build/device-mcp.exe` | `aa1be6a6a8e0716b3e382550324202db00b08c9e6de2db775538540e4168f724` | 10113536 |
| `build/mcp-probe.exe` | `1b5b3f3b5f468b2f404e58fc350e47fa75efed0591a97c96c8f5aa31c835f717` | 9740800 |

## 5. 整组部署与实机闭环

使用 `scripts/deploy.ps1 -SkipBuild` 覆盖安装。部署输出核对：已安装 App
`com.mobileuse.vnext` 为 `0.19.0/21`；`device-service`、`device-mcp`、固定制品、
`config.json`、`mcp-config.json`、`connection.json` 的部署哈希均与清单匹配；
服务 `release=0.19.0`、`contract=19`；手机 MCP HTTP 适配器监听 `127.0.0.1:18882`
（主机转发 `18883`）且运行哈希匹配；`can_start=true`、STOP 可用。

部署后第一次 stdio 探针的 `start` 曾返回 `control_revoked`，同一 App 停止待同步场景与
`1019c35` 记录的环境事实一致；随即重跑同一探针，`start` 与全部检查 PASS，未改代码或数据。
为运行手机 HTTP 适配器的 `origin_allowed` 探针检查，本次部署显式放行
`http://127.0.0.1:3000`（仅该回环源）；未使用 `*`，token 仍为必需。

实机最小闭环用文档化 HTTP 契约执行（不打印 token、树、图片字节或日志正文）：

```text
start P=Psnapstart… O=<origin>_6 state=active
keepalive duration_minutes=1 expires_at=2026-11-18T12:16:31.998384313Z
snap P=Psnapsnap… jpeg=540x1200 screen=1080x2400 rotation=0 bytes=10793
status(O) observation_state=ready input_state=effective expires_at=unchanged
status(P) operation_state=finished phase=finished image=absent
snap_replay operation_state=finished image=absent
observe_image id=<origin>_7 revision=2 image=540x1200
end outcome=executed session=ended reason=client_end cleanup=pending
cleanup can_start=true cleanup.state=complete
RESULT PASS
```

关键断言：

- `snap` 成功响应只含 `operation_id` 与顶层 `image`，没有 `error`/`outcome`/`observation`/
  `session`/`recovery`；`operation_id` 等于请求头预分配的 P。
- JPEG 用标准解码器解码得到 540×1200，与 `image.width/height` 一致；
  `media_type=image/jpeg`、`read_only=true`、`captured_at` 可解析、`screen` 为
  1080×2400/rotation 0。
- 原 O 在 snap 后仍 `ready`/`effective`，`expires_at` 等于一分钟显式续租值，未被 snap 改写。
- `status(P)` 与同 P 重放都只返回 `operation_state=finished`/`phase=finished` 的无图摘要。
- 带图 `observe` 仍发布新 O，revision 为 start 首个 O 的 revision + 1；snap 本身未递增 revision。
- `end` 撤权为 `ended/client_end`，收尾从 `pending` 进入 `complete`，最终 `can_start=true`。

MCP 探针：

- 主机 stdio（both）PASS：13 工具、目录指纹、只读注解、snap 540×1200/1080×2400；
- 主机 HTTP（both）PASS：认证/Origin、13 工具、snap；
- 手机 HTTP 适配器（`-transport http-remote`，both）PASS：13 工具、snap、
  `origin_allowed=200`；
- 主机 stdio 的 text 与 structured 模式各 PASS，snap 投影均通过。

## 6. 未执行项与理由

| 项 | 说明 |
| --- | --- |
| 实机显式 STOP 与取消的精确竞态 | snap 读取窗口很短，未可靠命中；由引擎单测覆盖“读取中 STOP 丢弃图片”与“取消不发 end/不交付图片/不改会话”，并在编码阻塞测试中验证冻结与晚到结果。 |
| 实机 SFID 更换、显示代次变化、源图超龄、编码失败 | 需要可控注入或修改设备链路；由 `internal/engine/snap_test.go` 的合成夹具覆盖，实机只验证正常路径。 |
| 实机截图进行中的控制 App 截图文案 | snap 速度快，未取得稳定窗口；Java 映射按受控 kind/phase 文案实现，未单独构造。 |
| 外部 Agent/MCP 宿主重连 | 按提案由用户自行安排；本次只验证仓库内主机工具与手机适配器，不操作外部宿主进程。 |
| 真实浏览器跨源 MCP 客户端 | 只验证了固定 Origin 的允许/拒绝边界，未使用浏览器客户端。 |

## 7. 环境事实与边界

- Windows 只承担构建、部署与主机探针；手机内为 Debian chroot 上的 linux/arm64 服务。
- 服务日志与探针输出只含受控计数、ID、枚举与哈希；原始图片、树、日志正文与凭据留在运行
  环境，不进入仓库。
- 固定 uiautomator JAR 未修改；本次没有新增第三方依赖。
- `docs/` 已由独立只读文档审计 subagent 审核并返回 CLEAN（实现提交说明中记录）。
