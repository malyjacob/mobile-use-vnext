# P3 实机验证记录（最小可写闭环）

状态：2026-11-08。本页只记录实际执行过的路径与真实结果；未执行的项目明确写“未验证/
pending”，不把设计推演或代码分支当作实机结论。原始日志、节点树、坐标、令牌与其他设备
材料不入库；文本只保留本页明确要求的一处验证值。

## 1. 版本与环境

| 项 | 值 |
| --- | --- |
| 代码提交 | 未提交（工作区） |
| 服务构建 | `0.1.0-p3`（`scripts/build.ps1 -Version 0.1.0-p3`，`-X internal/protocol.ReleaseVersion`；最终二进制 SHA-256 `af114af40660a3748c37e6b3d1cd018fba303f7b246a7b499db1d708291cd94f`） |
| 契约 | `contract_version = 1` |
| 设备 | `24117RK2CC` |
| 系统 | Android 16（API 36），arm64 |
| 特权 | Magisk + Debian 13 chroot |
| chroot | `/data/local/chroots/debian13` |
| 服务目录 | `/opt/mobile-use-vnext`（`device-service`、`u2.jar`、`service.sh`、`config.json`） |
| 状态目录 | `/var/lib/mobile-use-vnext` |
| 服务端口 | `18880`（仅 `127.0.0.1`） |
| adb 串口 | `127.0.0.1:5555` |
| 配对 App | `com.mobileuse.vnext`（源码 `versionName=0.4.0-p3`） |
| 固定 JAR | `third_party/uiautomator/u2.jar`（SHA-256 固定） |

构建方式：`GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0`；控制 App 与验证
探针分别由 `android-control/build.ps1`、`scripts/build-probe.ps1` 构建。探针 App 只用于
本轮验证，不进入发布包。

## 2. 执行入口

部署用 `scripts/deploy.ps1`（构建、下发、启动服务与 App、`adb forward`、轮询
`/api/status` 到 `can_start=true`）。接口调用形态如下，令牌、O/P 编号与目标节点按现场替换：

```bash
curl -s -X POST http://127.0.0.1:18881/api/start \
  -H "Authorization: Bearer $AGENT_TOKEN" -H "X-Mobile-Use-Contract: 1" \
  -H "Content-Type: application/json" -d '{}'

curl -s -X POST http://127.0.0.1:18881/api/observe \
  -H "Authorization: Bearer $AGENT_TOKEN" -H "X-Mobile-Use-Contract: 1" \
  -H "Content-Type: application/json" -d '{"reference":"O1"}'

curl -s -X POST http://127.0.0.1:18881/api/act \
  -H "Authorization: Bearer $AGENT_TOKEN" -H "X-Mobile-Use-Contract: 1" \
  -H "Content-Type: application/json" \
  -d '{"observation_id":"O2","operation":{"type":"tap","target":{"ref":"n7"}}}'

curl -s -X POST http://127.0.0.1:18881/api/end \
  -H "Authorization: Bearer $AGENT_TOKEN" -H "X-Mobile-Use-Contract: 1" \
  -H "Content-Type: application/json" -d '{"reference":"P1"}'

# 独立 STOP（App 的 ACTION_STOP 服务动作）
adb -s 127.0.0.1:5555 shell su -c \
  'am start-service -a com.mobileuse.vnext.STOP'
```

验证用探针 App 的界面在虚拟屏上启动；本文不记录其节点树、坐标或原始输出。

自动化闭环使用 `scripts/verify-p3.ps1`（读取同一个 `.state/deploy.json` 与主机 forward）：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-p3.ps1 -SkipBuildProbe
```

## 3. 实测结果

以下结果均来自本轮实机执行，按“操作 → 结果”原样记录。

### 3.1 最小闭环

| # | 操作 | 实测结果 |
| --- | --- | --- |
| 1 | `POST /api/start` | 返回 `200 executed`；首个 O1 为 full；空虚拟屏合法（root 无子节点） |
| 2 | `observe(O1)` | 返回 `200`；树为 delta（空差异） |
| 3 | 在虚拟屏启动验证用探针 App 后 `observe` | 得到 full 树，含 `input`/`button`/`checkbox` 节点 |
| 4 | `act` `tap` | 返回 `200 executed`；后观察中 `taps` 计数由 `0` 变 `1` |
| 5 | `act` `text` 写入 `你好，世界！` | 返回 `200 executed`；UI 层读回文本精确等于 `你好，世界！`；修复了 u2 服务 Latin-1 解码导致的乱码，RPC 请求现以 `\uXXXX` 转义发送 |
| 6 | `end(P1)` 后轮询 `status` | `end(P1)` 返回 `200 executed`；`cleanup` = `complete`/`execution=confirmed`/`tasks=confirmed`/`recents=not_applicable`/`display=confirmed`；SurfaceFlinger 中 `MobileUseAgent` 层消失；`can_start=true` |
| 7 | 通过 App `ACTION_STOP`（`am start-service -a com.mobileuse.vnext.STOP`）触发独立 STOP | 会话 `ended`；`cleanup` `complete`；`display` 确认释放；下一拍同步后 `can_start=true` |

### 3.2 进程终止与恢复

| # | 操作 | 实测结果 |
| --- | --- | --- |
| 8 | 有 active 会话时 `SIGKILL` 服务进程；root 显示助手以 `nohup setsid` 脱离服务后仍存活、虚拟屏仍在；随后重启服务 | 恢复先等待 App 同步，再探测到旧显示存在，经 App 精确 `close_display`；TaskList 成功且目标 display 无任务后 `reason=old_resources_resolved`；`cleanup` `complete`/`display=confirmed`/`tasks=confirmed`；SurfaceFlinger 中 `MobileUseAgent` 消失；`can_start=true` |
| 9 | App 进程 `force-stop` 后重启服务 | 会话被撤销；`cleanup` `complete`；`can_start=true` |

早期一次 SIGKILL 时 root 助手随服务退出、旧显示被系统移除，恢复日志为 `reason=display_absent`；
随后给 helper 增加 `nohup setsid` 脱离，才出现上表第 8 条“显示仍存在”的实测路径。两种路径都
不能只凭一次探测放行：恢复还要求 App 同步与 TaskList 成功。

### 3.3 并发与接入边界

| # | 操作 | 实测结果 |
| --- | --- | --- |
| 10 | 同一 O 并发两次 `tap` | 一个 `200 executed`、一个 `409 observation_claimed`；计数器只 `+1` |
| 11 | `observe`/`act` 携带 `image=true` | 返回 `501 unsupported_feature` |
| 12 | 重复键、未知成员、大小写变体、尾随 JSON | 返回 `422` |
| 13 | 缺少契约头 | 返回 `409` |
| 14 | 错误 token | 返回 `401` |

### 3.4 自动化回归

`scripts/verify-p3.ps1 -SkipBuildProbe` 在最终版本上连续运行四次，均退出码 0（PASS）：

- `start` → `observe(full)` → `tap` → `observe(full)`（计数 `1`）→ 中文 `text` → u2 精确读回
  `你好，世界！`（长度 6）→ 等待任务离开目标 display → `end`；
- 收尾轮询 `cleanup=complete`、`display=confirmed`、`tasks=confirmed`、`recents=not_applicable`、
  `can_start=true`，且 `dumpsys SurfaceFlinger --display-id` 无 `MobileUseAgent`。

四次运行前均关闭了系统的“屏幕用时限制”，避免 MIUI 对新装/后台 App 的 suspended 拦截干扰；
该限制曾被观察到导致 Activity 启动被 `MST_LimitActivity` 拦截。

结果解读（不超出上表事实）：

- 第 1–2 步验证了“首个 full 候选 → `observe(O1)` 确认基线 → 空差异 delta”的完整周转；
- 第 3–4 步验证了虚拟屏上的真实层级采集与 `tap` 派发，`taps` 计数证明输入到达目标界面；
- 第 5 步验证了中文文本写入与独立读回一致，以及 `\uXXXX` 传输修复；
- 第 6 步验证了 `end` 的撤权 → 独立收尾 → 显示释放确认链路，`can_start=true` 说明准入门
  重新打开；
- 第 7 步验证了不依赖普通同步的独立 STOP 通道同样能走完撤权、收尾与重新准入；
- 第 8–9 步验证了进程终止后的启动屏障与 App 更换路径；第 10 步验证 O 的单向认领在并发下
  只有一个请求取得派发权；第 11–14 步验证了图片拒绝、严格 JSON、契约与认证边界。

动作结果、观察结果、会话终态与物理收尾分别记录，未相互替代：例如第 6 步的 `executed`
来自 `end`，`cleanup.*` 来自设备探测与 App 事实，`can_start` 来自服务准入计算。

## 4. 已知限制 / 未验证

| 项 | 状态 | 说明 |
| --- | --- | --- |
| 完整 G3 | **未收口** | 恢复分支已实测；App 通知按钮实点、`primary` 模式与 HTTP 独立回归仍未覆盖 |
| `open_app` / MCP / 图片 | **未实现** | 工具不存在；`image=true` 返回 `501 unsupported_feature`；无 MCP 双入口与客户端 |
| STOP 的 App 通知按钮 | **未在 UI 实点** | 本轮通过 `ACTION_STOP` 服务动作触发独立 STOP；通知栏/App 内的停止按钮未做本轮的人工点击验证 |
| `primary` 模式 | **未实机覆盖** | 代码支持绑定 display 0，本轮闭环只在虚拟屏完成 |
| 服务正常退出演练 | **未执行** | `service.sh stop` 触发的撤权与收尾未在本轮作为独立场景记录 |
| HTTP 接入层回归 | **无包内测试** | `internal/httpapi` 没有自动化测试；第 10–14 项是本轮实机协议检查，不是独立回归套件 |
| 账本崩溃注入 | **未执行** | 账本写入路径有代码实现，未做进程崩溃窗口注入 |

## 5. 结论

- P3 的“最小可写闭环”在本机虚拟屏上已实测通过：`start`（full）→ `observe`（delta/full）→
  `act`（tap、中文 text）→ `end`（收尾完成、显示释放）→ 重新准入；独立 STOP 亦通过；
  `scripts/verify-p3.ps1` 连续四次 PASS。
- 服务被 `SIGKILL`（旧显示仍存在，经 App 精确关闭）与 App 重启两条恢复路径已实测通过；
  并发认领、`observe.full=true`、图片拒绝、严格 JSON、契约与认证边界已实测。
- G3 尚未整体收口：App 通知按钮实点、`primary` 模式、服务正常退出演练与 HTTP 独立回归仍
  缺证据（见上表），不能据此宣称 P3 全部验收完成，更不能宣称 P4 工具面可用。
- 本轮未验证的能力仍按“未实现/未验证”处理；不以本记录推断 MCP、图片或其他工具已可用。
