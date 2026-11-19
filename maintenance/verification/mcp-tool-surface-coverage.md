# MCP 工具面实机覆盖测试（虚拟屏模式）

- 记录时间：2026-11-20。
- 代码锚点：部署产物来自 `9009599df87ac192787e730f9e9970b42d8f97e6`（0.21.0 / 契约 20 /
  versionCode 23，`source_dirty=false`）。写作时 `HEAD=ac5677d`，与锚点之间的唯一提交是
  `ac5677d docs(maintenance): 归档 MCP 树文本无损简写提案并登记验证记录`，**不含代码变更**，
  故本记录的行为断言与当前 `HEAD` 同源。
- 本记录涉及实机：是。两次会话，全程 `session.mode=virtual`。
- 本记录**不涉及代码变更**，是探索性覆盖记录：只把已实测到的分支如实登记，并把未覆盖项
  写在第 6 节。已实现 / 已单测 / 已实测三者分开表述；本页只新增“已实测”证据。
- 按 AGENTS.md 第 11 条，原始截图、节点树、日志与个人信息不入库。本记录只写到应用与页面
  结构层级，不摘录账号、观看/收听/搜索历史等个人内容。

## 1. 分诊与本记录的性质

这不是缺陷修复，也不是取舍提案，因此不适用 `process.md` §3 的“先红复现用例 + 根因
`file:line`”归因路径。性质是**外部调用方视角的工具面覆盖测试**：由人工发起，用真实手机
和真实第三方应用，把 13 个 MCP 工具的公开分支尽可能走一遍，回答“哪些入口确实可用、
哪些拒绝是设计好的、哪些边界还没测到”。

因此本记录不产生“修复提交”，第 5 节的行为观测只作为后续分诊的输入；其中唯一一条疑似
实现问题的项（§5.2）单列，未在本轮定性。

## 2. 部署身份与环境

两个 MCP 入口本次只使用主机 stdio 适配器（DSH 侧托管）。

| 项 | 值 |
| --- | --- |
| 设备 | `24117RK2CC`，Android 16（API 36），arm64 |
| 屏幕 | 物理 `1440x3200`，override `1080x2400`，density `600→450`，rotation `0` |
| 会话模式 | `virtual`（虚拟屏 `display_id=113`），`session.agent="local-agent"` |
| 主机适配器 | `build\device-mcp.exe`，PID 36556，SHA-256 `bb205bbc4d79c5dc89c0ef557e8df28524377bec163d34a84caf64001ccf1d1b` |
| 发布 | `release_version=0.21.0`、`contract_version=20`、`versionCode=23`、`built_at=2026-11-19T18:33:16Z` |
| 主机清单 | `build/host-tools-manifest.json`：`source_commit=9009599…`、`source_dirty=false`；`device-mcp.exe` 与 `mcp-probe.exe` 两项 |
| 工具目录 | `count=13`、`sha256=914fc4b061e14eb42fa75288e1c695e1649e49797c662a012013abdb265d1dc5`（与清单一致） |

手机侧整组制品在测试后逐项核对（`shell root=true`）：

```text
versionName=0.21.0  versionCode=23        # dumpsys package com.mobileuse.vnext
device-service  9044128  e2e2529131769d1945cf86c6ba11734a62d4f5a01c4894f8b9984b1fe62ca875
device-mcp      9175200  0e3a930d0a4433646bda25dc3782ea766545f0c424e3960900a732f03c884b87
applist.dex        4316  63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8
u2.jar          3306229  28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3
service.sh         3643  4b568a9aa3078933b77db780e6db2617f20d1fe6ada5a8b8700d39f3a9d46ea0
```

四项哈希、字节数与 `build/release/manifest.json` 的 0.21.0 制品完全一致（`applist.dex`、
`u2.jar`、`service.sh` 只核对了字节数）。非 root shell 读 `/data/local/chroots/debian13/
opt/mobile-use-vnext/` 被拒（`Permission denied`），故哈希核对使用 `root=true`；`config.json`
与 `mcp-config.json` 的内容未读取、未入库。

## 3. 覆盖矩阵

会话 1：`started_at=2026-11-19T19:36:09Z`、`ended_at=19:41:38Z`，45 个观察修订
（`revision=1–45`），约 70 次工具调用。会话 2 只用于部署身份核对（26 秒）。

13 个工具全部触达，7 个 `app.kind` 全部触达。

| 工具 | 分支 | 结果 |
| --- | --- | --- |
| `start` | 新建会话 + 首采 | 已实测：`virtual` 模式，首采 `quality=stable/2/stable`，租约 5 分钟 |
| `status` | 无参 | 已实测：`can_start`、`cleanup.*`、`session.*` |
| `status` | `O` | 已实测：返回 `observation_state=retired`、`input_state=retired` |
| `status` | `P` | 已实测：返回 `operation_state=finished`、`outcome=executed`、`phase=renewed`/`ended` |
| `observe` | 默认 | 已实测 |
| `observe` | `full=true` + `image=true` | 已实测：`quality=unknown/0/not_sampled`，JPEG `540x1200` |
| `observe` | `wait_ms=1500` | 已实测：采集时点在调用后约 1.5 s |
| `observe` | `P` 指向活动会话 | 已实测：用于 `text` uncertain 后的核对入口 |
| `act` | `tap`（ref） | 已实测 |
| `act` | `tap`（ref，`bubble` 默认提升） | 已实测：无动作文本节点 → 最近可点祖先，成功进入详情页 |
| `act` | `tap`（坐标） | 未单独构造（仅长按用了坐标目标） |
| `act` | `long_press`（ref） | 已实测（被拒路径，见 §5.1） |
| `act` | `long_press`（坐标） | 已实测：一次 `target_changed` 拦截后重试成功 |
| `act` | `text`（默认 `press_enter=true`） | 已实测：`write=executed`、`enter=executed`，业务跳转成功 |
| `act` | `text`（`press_enter=false` 写入） | 已实测：写后同节点精确读回，发送键由 `enabled=false` 转 `true` |
| `act` | `text`（写入空串清空） | 已实测：读回退化为提示态，delta 对基准 full 收敛为 `changes=0` |
| `act` | `scroll`（`container.ref`，`amount=page`） | 已实测：`effect.state=observed` |
| `act` | `scroll`（`amount=half`） | 已实测 |
| `act` | `scroll`（坐标容器） | 已实测（被拒路径，见 §5.1） |
| `act` | `swipe`（raw，`duration_ms`） | 已实测：300 ms 滚动翻页、200 ms 横滑切歌各一次 |
| `act` | `key` `BACK` / `DPAD_UP` / `DPAD_DOWN` | 已实测 |
| `act` | `key` `HOME` / `APP_SWITCH` | 已实测（被拒路径，见 §5.1） |
| `act` | `image=true` | 已实测：随动作同批交付 JPEG `540x1200` |
| `snap` | — | 已实测：只返回 `operation_id` + 图片元数据，不发布 `O` |
| `wait_for` | `query` + `stable=true` | 已实测：`trigger=query`、`elapsed_ms=1713`、`quality=stable/3/stable` |
| `wait_for` | `absent_query` 超时 | 已实测：`trigger=timeout`、`matched=["timeout"]`、`elapsed_ms=5533` |
| `inspect` | `query`（0 命中） | 已实测：`matches=0 total=0 truncated=false` |
| `inspect` | `query`（多命中） | 已实测：`matches=2 total=2`，每条带完整 `path=[…]` 与命中子树 |
| `inspect` | `node`（子树） | 已实测：`nodes=191 entries=191`，detailed 视图补回 compact 省略的矩形 |
| `app.find` | 无参 | 已实测：`app.total=78` |
| `app.find` | `query` | 已实测：按名称折叠子串过滤 |
| `app.list` | `scope=primary` | 已实测：`display_id=0`，含 `ownership=shared` 与 `foreground` |
| `app.list` | `scope=session` | 已实测：随 open/close 实时增减，`display_id=113` |
| `app.open` | 三个应用 | 已实测：`ownership=owned`、`reused=false`、`state_preserved=false` |
| `app.handoff` | `task_id` + `restore_foreground=true` | 已实测：虚拟屏 → 主屏 `display_id=0`，`ownership=shared`、`state_preserved=true`、`foreground_restore=confirmed` |
| `app.claim` | `task_id`（主屏任务） | 已实测：主屏 → 虚拟屏，`ownership=borrowed`、`state_preserved=true`，应用内页面状态保留 |
| `app.close` | 借入窗口（默认） | 已实测（被拒路径，见 §5.1） |
| `app.close` | 借入窗口 + `allow_borrowed=true` | 已实测：`recents_cleaned=true`、`app.complete=true` |
| `app.kill` | 前台服务/播放中（默认） | 已实测（被拒路径，见 §5.1） |
| `app.kill` | `allow_stop_playback=true` | 已实测：返回 `target_pids`/`killed_pids`/`remaining_pids`/`restarted`，见 §5.2 |
| `shell` | 非 root | 已实测：多条命令、`termination=exited`、`exit_code=0`、`output_bytes` |
| `shell` | `root=true` | 已实测：读受保护目录与文件哈希 |
| `notify` | — | 已实测：`saved=true`、`notification.state=pending` |
| `keepalive` | `duration_minutes=10` | 已实测：`expires_at` 改设到调用后 10 分钟 |
| `diagnose` | 无参 | 已实测：`issues` + `timeline`、`coverage=own_records`、`truncated=true` |
| `end` | — | 已实测：`cleanup.state=pending` → 轮询 `status(P)` 后四项 `confirmed`、`cleanup.state=complete` |

观察质量三态全部取得：`stable`（多数静态页）、`unstable`（`stop_reason=budget`/
`max_samples`，视频播放与列表重排）、`unknown`（`not_sampled`，强制 `full` 与 `wait_ms`）。
树形态两种都取得：`full` 与 `delta`（含 `~`/`+`/`-`/`children`/`=` 与 `changes=0`）。

## 4. 实机操作轨迹

按“像人一样用手机”的顺序，不写个人内容：

| 应用 | 包名 | 走过的路径 | 覆盖到的工具 |
| --- | --- | --- | --- |
| 哔哩哔哩 | `tv.danmaku.bili` | 推荐流 → 视频详情 → 移动网络弹窗 → 播放中长按 → 弹幕输入框 → 个人页 → 历史记录 → 列表翻页 | `open`/`scroll`/`tap`/`long_press`/`text`/`key`/`swipe`/`inspect` |
| X | `com.twitter.android` | 首页时间线 → 探索趋势 → 搜索框 → 结果页 → 推文详情 → 跨屏往返 | `handoff`/`claim`/`close`/`text`+ENTER/`observe`(full+image)/`inspect` |
| 网易云音乐 | `com.netease.cloudmusic` | 首页 → 全屏播放 → 封面横滑切歌 → 暂停 → 发现页 → 搜索 → 点播 → 播放队列下一首 | `open`/`tap`/`swipe`/`key`/`text`/`scroll(half)`/`kill` |

跨应用部分：`app.find`/`app.list` 两个 `scope`、`shell` 设备与文件系统查询、`notify`、
`diagnose`、`keepalive`、`end` 与 `cleanup` 收敛。

## 5. 观测到的行为

### 5.1 保护性拒绝（10 个错误码，全部 `outcome=not_executed`）

本轮所有失败都是有明确错误码的静态或准入拒绝，**没有一次是“静默做了”或“静默没做”**。

| 触发条件 | 错误码 | `error.details` 关键字段 |
| --- | --- | --- |
| 外层 `scroll` 容器中心线无法证明轨迹 | `unsupported_node_action` | `reason`：中心线不可证 |
| 坐标容器同时落在多个滚动容器上 | `ambiguous_point` | 提示改用 `container.ref` |
| 坐标目标误带 `bubble` | `invalid_request` | `bubble` 只适用于 ref 目标 |
| 对只含 `tap` 的节点 `long_press`（`bubble=false`） | `unsupported_node_action` | `reason=target_missing_action`、`target_actions=[]` |
| 同一节点 `bubble=true` 但祖先亦只含 `tap` | `unsupported_node_action` | `reason=ancestor_missing_action`、`ancestor_ref`、`ancestor_actions=["tap"]` |
| 关闭会话借入窗口（未授权） | `close_refused_borrowed` | — |
| 终止有前台服务/播放的应用（未授权） | `kill_refused_playback` | — |
| `key=HOME` / `APP_SWITCH`（virtual 模式） | `unsupported_key` | “需要共享主屏” |
| 对 `target_actions=["tap"]` 的节点 `long_press` | `unsupported_node_action` | `reason=target_has_other_action` |
| 快照到派发之间目标几何变化 | `target_changed` | 附新 `O`，`recovery.entry=retry` |

其中三条值得单独记：

- **不静默替换动作**：节点只有 `tap` 时 `long_press` 直接拒绝，不会退化成点击；`bubble`
  提升时祖先同样只能 `tap`，也照样拒绝并回指 `ancestor_ref`。
- **陈旧坐标不盲发**：坐标长按在快照与派发之间遇到列表重排，返回 `target_changed` 且
  **零派发**，用附带的新 `O` 重试即成功。
- **`text` 分阶段诚实**：一次搜索写入后应用立即重排成实时结果，`ENTER` 目标失效，返回
  `outcome=uncertain`、`write=executed`、`enter=not_executed`、`dispatch.enter=not_attempted`，
  并按 `recovery.entry=observe` + `recovery.reference=P` 引导核对。随后 `observe(P)` 证实
  文字**确实写入**。这条不是缺陷：调用方在边输边搜的控件上应优先 `press_enter=false`。

### 5.2 待跟进的实现问题：`kill` 残留进程

对后台应用执行 `app.kill`（`allow_stop_playback=true`）后返回：

```text
app.target_pids=[…5 项…]
app.killed_pids=[…同上 5 项…]
app.remaining_pids=[…2 项…]
app.restarted=true
```

即：目标包的全部已定位进程都被终止，但仍有两个同包进程存活，且结果自报 `restarted=true`。
该应用在终止前处于后台、未播放，只保留前台/常驻服务。

**本轮只如实记录现象，不作定性。** 需要回答的是：`restarted=true` 是设计里对“前台服务被
系统拉起”的如实上报，还是 `kill` 的完成判据只覆盖了首次定位到的 PID 集合？两者对应不同的
契约措辞。归属与复现不在本记录范围内。

### 5.3 非缺陷的应用侧现象

- 某应用首页在两次采集之间**不再上报**搜索栏节点（`inspect query` 命中 0）。这是应用自身
  的可见性未上报，不是采集漏项；`inspect` 的 0 命中结果本身也由此得到实机样本。
- 部分列表容器上报 `enabled=false`，但其子节点仍可点击（点击后业务生效）。公共树如实转述，
  调用方不应把容器 `enabled` 当作可交互判据。
- `delta` 以**最近一张 `full`** 为基准。清空输入框后返回的 `changes=0` 意味着“已回到基准
  形态”，而不是“本次动作无效”。这一点容易被调用方误读，建议在工具说明里明确。

## 6. 未覆盖项与原因

| 项 | 原因 |
| --- | --- |
| `act` 坐标 `tap` | 本轮只用坐标做了 `long_press`；坐标 `tap` 的点→节点证明已有专门记录，未重复 |
| `primary` 模式下的会话行为 | 用户指定虚拟屏模式；`HOME`/`APP_SWITCH` 因此只能覆盖拒绝分支 |
| `no_safe_point` / `invalid_target` / `no_geometry` | 三个应用上没有构造出触发页面 |
| `text` 多行写入与 `verify/readback_mismatch` | 三个应用的输入框都单行；换行被格式化那条路径未复现 |
| `text` 回执注入（`termination=unknown`） | 发布二进制无故障注入面 |
| `notify` 送达 App 消息中心 | 只验证到 `saved=true` / `pending`；未在手机上查看消息中心与系统通知 |
| `wait_for` 的 `image=true` 与 `stable` 无查询分支 | 未构造 |
| `diagnose` 指定 `O`/`P` | 只用了无参形式 |
| `app.list` 的 `truncated=true` | 设备 78 个应用未触发预算截断 |
| `inspect` 的 `content_unavailable` 与 `inspection_too_large` | 历史树未被淘汰，最大一棵 305 节点未超限 |
| `shell` 超时 / 输出截断 / `termination=unknown` | 已有专门记录，本轮未重复 |
| `end` 期间的并发 `act` 竞态 | 未构造 |

## 7. 对 `status.md` 的增量（已应用）

本记录为下列既有条目提供了 0.21.0 的实机样本，已按下列方式更新
[status.md](../status.md)：

| 原条目 | 更新方式 |
| --- | --- |
| §3 “0.16.0 实机 STOP/ENTER 回执注入、claim/handoff 成功路径 … claim 因受控主屏任务不存在 SKIP” | 拆成两行：STOP/ENTER 回执注入仍为未执行；`claim`/`handoff` 成功路径改为已验证并指向本记录 |
| §3 “受控 scroll/raw duration/quality/effect 的实机与跨应用证据 … `effect=observed` 实机样本 … 未执行” | 补记 2 次 `effect=observed`、B 站与嵌套容器内层覆盖、两条拒绝码与 2 次 raw swipe；WebView/overlay/STOP 时序与阈值校准仍标未执行 |
| §3 “`act` 交付观察的 delta 分支 … 未取得 delta 样本” | 改为已验证，写明取得 delta 的来源、五类变更行与 `changes=0`，并注明 `app` 各 kind 仍为 `full` 作对照 |
| §2 `app.close`/`app.kill` 的授权守卫 | 原表无对应行，新增一行 `app` 的 `claim`/`handoff`/`close`/`kill`，并写入 `kill` 残留进程的如实记录（指向本记录 §5.2） |

同时更新了 `status.md` 的头部状态行与 §4 证据入口。`apps`/`open_app` 两行仍用契约 16 之前
的工具名，属既有漂移，未在本次一并处理。

