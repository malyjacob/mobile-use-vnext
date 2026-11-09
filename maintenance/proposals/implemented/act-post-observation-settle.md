# 提案：采集前的收敛等待（settle）

状态：已实现（实现提交 `39683f478ff012d31220096b169939efea5c0a9f`，2026-11-09）。
实现与验证：[act 后观察收敛采样实现与验证记录](../../verification/act-post-observation-settle.md)。
基线提交：`f5937371eb738e1391a060136cd43cd9a34ecfe5`（分支 `master`，2026-11-09）。
适用范围：`act` 的后置观察、`start` 的首个观察、`open_app` 的后置观察。
影响范围：`internal/engine`、`internal/observation`、`internal/eventlog`。
契约（`/api/*` 字段与 `contract_version`）：不改变。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；已落地的落点与差异见 §11，
若代码继续前进，须按当时的 `HEAD` 重新核对（重点是 `internal/engine/act.go`、
`capture.go`、`internal/observation/build.go` 的行号与 `engine.DefaultConfig` 的取值）。

---

## 0. 摘要

`act` 派发输入后立即做后置采集，得到的常常是过渡帧。下一次 `act` 的实时重验要求目标
节点与其祖先链的身份逐级一致，因此失败并返回 `target_changed`，强制 Agent 重新
`observe`，一次已成功的动作被白白消耗。

本提案在采集之前加入**收敛采集**：连续采集直到相邻两帧的摘要相等，再发布后一帧。摘要
与重验同源（`identityOf` + 祖先链 + 公开树 + 前台/屏幕）。采样间隔不设固定值，等于一次
dump 的耗时；帧数由 `MaxSamples` 封顶，总时长由 `MaxBudget` 收敛，`MinDwell` 作为
静默窗口下界。

覆盖三个阶段：`act` 的后置观察、`start` 的首个观察、`open_app` 的后置观察。三档策略值
由服务端固定，**不开放给调用方 Agent**，与现有的工作超时一样属于服务策略。

不改 `/api/*` 契约，不改固定制品，不放宽任何既有门禁。能力边界是"消除树在动这一类
失败"，不能识别静态骨架屏。

---

## 1. 问题

### 1.1 现象

`act` 在派发输入后立即做一次后置采集，并把结果发布成新的 `ready` O。对带动画或
加载的界面，这一帧经常是**过渡帧**。Agent 拿这张 O 去做下一次 `act` 时，实时重验
会发现目标节点或它的祖先链与观察时不一致，返回 `409 target_changed`，并强制
Agent 再 `observe` 一次。

结果是：一次本来成功的动作被记为"白干"——输入已经派发、O 已经被认领、还占掉一次
工作位，最后只换来一个"重新观察"。

### 1.2 机制（代码证据）

链条上有三处叠加，缺一不可：

1. **派发与采集之间没有等待。** `dispatchAct` 返回 `executed` 后直接进入
   `captureAndPublish`（[act.go:96](../../../internal/engine/act.go)），中间没有 settle。
2. **设备侧没有 idle 同步。** 采集与输入走两条不同的通道，两条都没有"等界面稳定"：
   - **采集**：`Device.Capture` 经 u2 JSON-RPC 调用 `dumpWindowHierarchy`
     （[device.go:784](../../../internal/android/device.go)）。固定制品 `u2.jar` 的上游
     `openatx/android-uiautomator-server@a98441be`
     （来源见 `third_party/uiautomator/README.md`）中，
     `AccessibilityNodeInfoDumper.getWindowRoots()` 虽然调用 `device.waitForIdle()`，
     但 `AutomatorServiceImpl` 构造器执行了 `setWaitForIdleTimeout(0L)`，超时为 0，
     等价于立即返回。
   - **输入**：不走 u2。`Device.Tap` 构造 `input -d <display> tap x y`，经 `adb shell`
     执行（[foreground.go:95](../../../internal/android/foreground.go)、
     [foreground.go:192](../../../internal/android/foreground.go)）；`swipe` 是带固定
     300ms 手势时长的 `input swipe`，`long_press` 是 700ms 的同点 `swipe`。命令返回只
     代表"注入已受理"，不代表界面进入稳定状态；`swipe` 的惯性在命令返回之后仍在继续。
   - 两者之间没有握手：输入命令一返回就进入后置采集。
3. **重验的判据非常严。** `matchFreshNode`（[act.go:600](../../../internal/engine/act.go)）
   要求新树里存在**唯一**节点，其 `rawIdentity`（[act.go:552](../../../internal/engine/act.go)）
   与观察时完全相同，且**祖先链逐级 identity 相同**。`rawIdentity` 含 `Bounds`，
   于是任何布局位移都会判不等；祖先链含祖先的 `Bounds`，于是**父容器尺寸变化会让整棵
   子树的 ref 一起失效**。

三者合起来：输入命令返回后，采集立刻开始（`CapturedAt` 记在读取之前），一次 dump
本身再持续 100–500ms。因此被冻结成 O 的，是动作后约 50–800ms 这段时间里的界面状
态；而 60 秒输入窗口内真实界面早已收敛到别的状态，重验必然不相等。

### 1.3 一个反直觉的性质

模型思考越久，失败率越高。原因是 O 冻结在动作后那一刻的中间帧，等待不会让 O 变新，
只会让实时界面走得更远。这与 `observe` 的时间特性相反（observe 的 O 是模型思考完
之后才拍的，越久越稳）。

### 1.4 为什么必须处理

- 高频动作类别（`swipe` 后的 fling、`open_app` 冷启动、跨页导航）几乎必然触发。
- 每次触发要付一次废掉的 `act`（最坏 50s 工作窗口）+ 一次 `observe` 的往返与 token。
- 更严重的副作用：Agent 为绕开 `target_changed`，最省事的做法是改用坐标目标，
  而坐标路径**跳过 ref 唯一性证明**，只保留前台一致性检查。这个失败模式会把
  Agent 从安全路径推向不安全路径。

---

## 2. 目标与非目标

### 目标

- 让 `act` 的后置观察、`start` 的首个观察、`open_app` 的后置观察**在界面树收敛之后再
  发布**，从源头减少 `target_changed` 与"首帧半合成"。
- 判据与实时重验**同构**，不允许出现"settle 判稳、重验仍失败"的缝。
- 不引入调用方参数：策略值固定在服务端配置里，与工作超时同级，不作为请求字段，
  不改变 `/api/*` 契约，不放宽任何既有门禁。

### 非目标

- 不追求"视觉静止"：只追求"树不再变化"。
- 不解决"静态骨架屏"这类**树不变但页面未就绪**的中间态（见 §7.3）。
- 不按操作子类型细分策略：只按阶段分为 `act` / `start` / `open_app` 三档；同一档内
  `tap`/`swipe`/`text`/`key` 共用一组值。
- 不提供 target-scoped 摘要：摘要始终覆盖整棵目标屏的树与语境（见 §4.5）。
- 不改固定 `u2.jar` 制品，不改其补丁与哈希。

---

## 3. 候选路线与权衡

| 路线 | 做法 | 优点 | 致命问题 | 结论 |
| --- | --- | --- | --- | --- |
| R1 打开/自建 idle 等待 | 恢复 `waitForIdle`，或给服务加事件静默 RPC | 语义最"正统" | ① 要改固定制品：smali 补丁 + 重算哈希 + 改 manifest + 重新签名；② 它测的是无障碍事件静默，Compose/Flutter/WebView 不保证，且超时上限 10s 会顶穿工作窗口；③ **判据与重验不同源**：事件静默不代表 `identityOf` 不变 | 否决 |
| R2 固定延迟参数 | `act` 增加 `settle_ms`，服务端 sleep 后采集 | 改动最小 | ① 交给 Agent 猜魔数，不同设备/页面最优值不同；② sleep 不产生任何证据，只是把中间帧往后挪；③ 每个宿主会自造一套默认值 | 否决（可作为过渡开关，不作为方案） |
| R3 后置 O 标记 provisional | 后置 O 不可直接 `act`，必须再 `observe` | 规则显式、无隐式陷阱 | 往返退化成"每次动作都要 observe"，后置观察失去意义 | 否决（已确认） |
| R4 放宽重验身份 | 祖先链忽略 `bounds`、只比 `resource-id`+`class` | 能容忍布局漂移 | 违反"歧义即拒绝"的既有立场；列表重排或相似卡片并排时会点到隔壁；把安全边界换成吞吐 | 否决 |
| **R5 连续采集收敛** | 连续采集直到相邻两帧摘要相等，再发布后一帧 | ① 不需要改制品；② 判据可与重验共用同一个 `identityOf`；③ 采样周期自适应设备（= 一次 dump 的耗时），无魔数；④ 不改变契约与门禁 | 见 §7 的能力边界（静态中间态无解） | **选定** |

R5 的关键优势是**失败条件的直接否定**：我们把"稳定"定义成"重验会看到的东西不再变化"，
而不是"画面看起来静止"。只要两帧的身份集合相同，下一次重验就没有理由不等。

---

## 4. 判据

### 4.1 原则

> **settle 的判据必须是重验判据的超集；理想情况下就是同一个函数。**

任何"settle 比 重验 松"的设计都会重新打开裂缝；"settle 比重验 严"只会多等一帧，
是安全的。本方案按"同构"设计，并为此把 `identityOf` 提取为共享实现（§6.1）。

### 4.2 三层摘要

```text
L1 节点指纹序列
   fp[i] = H( identityOf(nodes[i]) ‖ fp[nodes[i].Parent] )      // 根节点 parent 为空
   L1    = H( fp[0] ‖ fp[1] ‖ … ‖ fp[n-1] )                     // preorder

L2 公开树
   L2    = H( canonicalJSON(Preview(capture).Tree) )

L3 语境
   L3    = H(foreground.state ‖ foreground.package ‖ foreground.task_id
             ‖ display.width ‖ display.height ‖ display.rotation)
```

**三层全部相等**才算收敛。

| 层 | 覆盖什么 | 为什么必须有 |
| --- | --- | --- |
| L1 | 重验的判定域（`identityOf` + 祖先链） | 决定 `target_changed` 是否触发 |
| L2 | Agent 实际读到的公开树 | `Visible` 翻转、context 祖先进出会改公开树但不改 L1 |
| L3 | Activity 转场、旋转 | 转场期间前台包会变；只靠 L1 可能"树没变但窗口在换" |

### 4.3 进摘要与不进摘要的字段

`identityOf`（[act.go:565](../../../internal/engine/act.go)）当前包含：

```text
Package, Class, ResourceID, Text, Description
Bounds                                   ← 裁剪到屏幕后的矩形，不是 RawBounds
Clickable, LongClickable, Scrollable
Editable, Password
HasEnabled/Enabled, HasChecked/Checked
HasSelected/Selected, HasFocused/Focused
```

**明确不进摘要**：

| 字段 | 原因 |
| --- | --- |
| `CapturedAt` | 每帧必变，进了永不收敛 |
| `Index` / `Depth` / `DisplayID` | `matchFreshNode` 不使用 |
| `RawBounds` | 重验用 `Bounds` |
| `Children []int` | 已由 preorder 序列 + parent 指纹隐式表达 |
| `Checkable`、`Visible` | 不在 `identityOf` 里；`Visible` 的影响由 L2 覆盖 |
| 字符串字段的存在性（`TextSet`/`DescSet`/`ResourceSet`） | `identityOf` 只比值，不区分"缺失"与"空串" |

注意 `Visible`、`Checkable` 不在身份里是**现状**，判据必须照抄，不能"顺手补全"：
补全会让 settle 比重验更严，白白多等。

### 4.4 为什么不用事件或像素

- **事件静默**：判据不同源，且现代 UI 框架不保证发事件。
- **像素/截图**：重验比的是树不是像素；淡入淡出、跑马灯、视频都只改像素不改树，
  用像素判定会引入大量与失败无关的抖动。
- **两者都需要改固定制品或增加一次截图编码**，成本与收益不匹配。

### 4.5 为什么不做 target-scoped 摘要

一个诱人的优化是：只对"本次动作的目标"做收敛判定，页面其余部分不管。这样在"目标稳定
但别处在动"的界面上能更快收敛。不做，理由有三条：

1. **后置观察同时是 Agent 的下一双眼睛。** 只保证目标子树稳定，发布出去的 O 其余部分
   可能仍是半成品；Agent 据此规划出的下一步更容易出错，而那个错误会以"点错/找不到
   节点"的形式回来，绕一圈还是成本。
2. **目标本身可能在收敛期间被替换。** 局部摘要隐含"目标不会变"，但列表滚动、内容替换
   恰恰会换掉目标节点；此时局部摘要可能仍然"稳定"，却已经不是同一个目标。
3. **判据应当只有一个。** 一旦并存"局部/全局"两套判定，评审与验证成倍增加，而收益只有
   几百毫秒。

如果实机数据显示某些页面因全局摘要长期封顶，优先调整该档的 `MaxBudget`，而不是缩小
判定范围。

---

## 5. 算法

### 5.1 采样间隔 = 一次采集的实际耗时

不设固定间隔：

```text
sample_k 采集起点 ──dump──▶ sample_k 采集结束
                                └─ 立即开始 sample_{k+1}
```

一次采样的内容：`u2 dumpWindowHierarchy`（逐节点 `getChild`，100–500ms）+ 解析 +
`foreground`（dumpsys）+ `Preview` 投影与摘要（纯 CPU）。因此单帧间隔约：

| 场景 | 单帧间隔 |
| --- | --- |
| 小树（<200 节点） | 120–250ms |
| 中等页面 | 250–500ms |
| 大树 / 慢设备 | 500–1200ms |

用 dump 耗时做采样周期是自适应的：它本身就是这台设备、这棵树的最小可观测时间单位。

### 5.2 `MinDwell` 的语义

> `MinDwell` 是**判稳所需的静默窗口宽度**，不是"总驻留时间"，也不是采样间隔。

实现方式是**保证相邻两帧的采集起点间隔 ≥ `MinDwell`**：

```go
gap := MinDwell - time.Since(prev.CapturedAt)
if gap < 0 { gap = 0 }
```

因此在任何时刻，观察到"两帧相等"就意味着这两帧跨越了至少 `MinDwell` 的窗口，
发布规则永远只有一句：**发布一对相等样本中的后一帧**，不存在"先等一会儿再发布旧帧"
的分支（那等于伪造观测）。

两个推论：

- `MinDwell ≤ 一次 dump 耗时` 时它是 **no-op**。多数真机 dump 在 300ms 量级，
  所以 200ms 事实上等于没设；要起作用需设到 400–600ms。
- `MinDwell` 只解决"过渡刚开始、树还没变"的窄缝，**不能**识别静态骨架屏（§7.3）。

### 5.3 主循环

```go
prev, err := e.captureSample(ctx, work, session)      // f1
start := time.Now()
lastDump := time.Since(start)

for n := 1; ; n++ {
    if e.workRevoked(work) || ctx.Err() != nil {
        return lastGood, cancelled                  // STOP：不发布 observation
    }

    gap := policy.MinDwell - time.Since(prev.CapturedAt)
    if gap < 0 { gap = 0 }

    // 预算预检：用上一帧的实测耗时估计下一帧，放不下就不开始
    if n+1 > policy.MaxSamples ||
        time.Since(start)+gap+lastDump > policy.MaxBudget {
        return prev, unstable
    }

    sleepInterruptible(gap)                         // 可被 ctx / STOP 打断

    dumpStart := time.Now()
    next, err := e.captureSample(ctx, work, session)
    lastDump = time.Since(dumpStart)
    if err != nil {
        return prev, unstable                       // 采样失败：用上一帧
    }

    if digestEqual(next, prev) {                    // 窗口由构造保证 ≥ MinDwell
        return next, stable
    }
    prev = next
}
```

要点：

- **预算预检用上一帧的实测耗时**估计，而不是拍固定值：慢设备自动少采，快设备自动多采。
- **退出时永远返回一帧，而不是报错**。动作已经 `executed`，后置观察的价值是给 Agent
  下一张 O，不能因为"没等到稳定"就判失败。
- `sleepInterruptible` 与仓库现有风格一致（`waitDisplayExists`、
  `displayConfirmedAbsent` 都是可打断的有界轮询）。

### 5.4 发布哪一帧

永远发布**后一帧**（`next`）：

- 它是最新的证据；
- 它的 `CapturedAt` 更晚 → `actionable_until` 更晚 → Agent 可用窗口更长；
- 两帧摘要相等，内容一致。

### 5.5 预算的定义

四个独立参数，缺一不可：

| 参数 | 管什么 | 防什么 |
| --- | --- | --- |
| `MinSamples` | 判稳所需的最少帧数 | "第一帧就当基线"的退化 |
| `MinDwell` | 静默窗口下界 | 过渡刚开始、树暂未变化的窄缝 |
| `MaxSamples` | 帧数硬上限 | 慢设备上单帧 2s 时的最坏叠加 |
| `MaxBudget` | 总墙钟上限 | 所有情况的总兜底 |

三档具体取值见 §6.2；同一档内四个参数一起生效。

预算耗尽**不是失败**：照常发布最后一帧，并在日志里记 `stable=false`，供后续调参。

### 5.6 失败、撤销与超时

| 情况 | 行为 |
| --- | --- |
| 某一帧采集失败（`hierarchy_unavailable` 等） | 发布上一帧，不判后置观察失败 |
| STOP / 工作被撤销 | 立即返回；`act` 维持现有语义：`outcome=executed`、`next=status`、**不带 observation** |
| `work.Ctx` 到期 | 发布最后一帧 |
| 目标屏消失 | `Capture` 失败 → 走"发布上一帧"分支 |

### 5.7 图片路径

现有顺序是"探测显示 → 读源图 → 读树"，且 `ImageMaxSpan=3s` 约束"图读取开始 → 树读取
开始"的跨度（[capture.go:116](../../../internal/engine/capture.go)）。

如果先读图再 settle，跨度会被撑破并报 `image_stale`。因此顺序固定为：

```text
settle（纯树采样，N 帧）
   ↓
wantImage：读源图 → 读最终树（保持现有 span 约束）
!wantImage：直接提交收敛帧
```

`image=true` 时：

- 最终树读完后重新计算摘要；若与收敛帧不等，在剩余预算内再跑一轮 settle；
- 预算已耗尽则发布新树并记 `stable=false`（它更新，不能丢弃）。

代价：`image=true` 多一次 dump（N+1），只在要图时付。

### 5.8 时间轴示例（示意取值，不代表 §6.2 的默认值）

```text
t=0      f1 [0,300)     digest A
t=300    f2 [300,600)   digest B ≠ A       页面还在动
t=600    f3 [600,900)   digest B = B       收敛
t=900    发布 f3（captured_at=600，actionable_until=660）
         成本：3 帧，比现状 +2 帧、约 +600ms
```

`MinDwell = 500ms`、快设备（dump 80ms）时：

```text
t=0      f1 [0,80)      A
t=80     gap=420 → 睡到 t=500
t=500    f2 [500,580)   A    窗口 500ms ≥ 500ms，收敛
t=580    发布 f2
         成本：2 帧 + 420ms 睡眠
```

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `internal/engine/identity.go`（新增） | 把 `identityOf` 从 `act.go` 提出，供重验与 settle 共用；`rawIdentity` 不变 |
| `internal/engine/engine.go` | `Projector` 接口增加 `Preview(*device.Capture) (*Projection, error)`；`Config` 增加 `SettlePolicies` 三档 |
| `internal/engine/capture.go` | 新增 `captureSample`（Capture + Preview + 摘要）、`settleCapture`（主循环）、`captureDigest`；`captureAndPublish` 按既有 `phase` 参数查表选策略，**不新增参数** |
| `internal/engine/act.go` | 后置观察传入 `Act` 档策略；`identityOf` 改为引用共享实现 |
| `internal/engine/start.go` | 会话激活后的首个观察传入 `Start` 档策略 |
| `internal/engine/openapp.go` | 启动确认后的后置观察传入 `OpenApp` 档策略 |
| `internal/observation/build.go` | 新增 `Builder.Preview`：保存/恢复 `prev` 与 `nextRef` 后调用 `Build` |
| `cmd/device-service/main.go` | `observationProjector.Preview` 适配 |
| `internal/eventlog` | 白名单增加 `samples`、`stable`（计数与布尔，符合现有安全标准） |
| `internal/engine/*_test.go` | fake projector 补 `Preview`；新增 settle 用例（§8.1） |

`Preview` 的必要性：`Builder.Build` 结尾会 `b.prev = snap`
（[build.go:123](../../../internal/observation/build.go)），`assignRefs` 会推进
`b.nextRef`（[match.go:140](../../../internal/observation/match.go)）。若 settle 循环直接
调 `Build`，每次采样都会把 `prev` 推到上一帧，导致**已发布 O 的 ref 编号发生漂移**，
破坏保守保号。用 `Preview` 采样、收敛后再用最后一张 capture 做一次真 `Build`，
可以做到 **N 次 dump、一次真实投影**。

```go
func (b *Builder) Preview(cap *device.Capture) (*Snapshot, error) {
    savedPrev, savedRef := b.prev, b.nextRef
    defer func() { b.prev, b.nextRef = savedPrev, savedRef }()
    return b.Build(cap)
}
```

### 6.2 配置与三档策略值

策略固定在服务端（`engine.Config`），不作为任何请求字段，与 `WorkTimeout`、
`SessionTTL` 同级。

```go
type SettlePolicy struct {
    Enabled    bool
    MinSamples int
    MaxSamples int
    MinDwell   time.Duration // 静默窗口下界
    MaxBudget  time.Duration // 总墙钟上限
}

type SettlePolicies struct {
    Act     SettlePolicy
    Start   SettlePolicy
    OpenApp SettlePolicy
}
```

初始取值：

| 阶段 | 触发点 | MinSamples | MaxSamples | MinDwell | MaxBudget | 占工作窗口 |
| --- | --- | --- | --- | --- | --- | --- |
| `act` | 派发输入后、后置观察前 | 2 | 5 | 400ms | 2000ms | 4%（50s） |
| `start` | 会话激活后、首个观察前 | 2 | 4 | 300ms | 1500ms | 3.8%（40s） |
| `open_app` | 启动确认后、后置观察前 | 2 | 5 | 600ms | 3000ms | 6%（50s） |

取值依据：

- **`act`**：覆盖 Activity 转场（约 300ms）、IME 弹起（约 250–350ms）与 `swipe` 惯性
  （300–1000ms）的尾部。`MinDwell=400ms` 在典型 dump（约 300ms）之上留 100ms 间隔；
  `MaxBudget=2000ms` 让"慢一档"的页面仍能等到，又不至于让每条动作都贵一倍。
- **`start`**：虚拟屏刚激活时首个树常是"根 + 少量节点"，随后被 App 首帧填满。这里
  不需要等长动画，只需排除"半合成首帧"，所以 `MinDwell=300ms`（在快设备上才真正
  生效），预算 1500ms。
- **`open_app`**：冷启动与"新语境"是已知高发场景，允许更长驻留与更大预算。
  `MinDwell=600ms` 要求至少跨过一次启动动画，`MaxBudget=3000ms` 覆盖中速冷启动；
  超过 3s 的冷启动仍落在 §7.2 的"不能治"里。

`observe` 不纳入：它由调用方按自己的节奏发起，且已有 `wait_ms`；再加一层不可见的
收敛等待，会让调用方无法解释延迟来源，也无法通过参数关闭。

三档的 `MaxSamples` 与 `MaxBudget` 是按"典型 dump ≈ 300ms"配平的：在这个量级上通常
是 `MaxSamples` 先到；设备更慢（dump ≥ 800ms）时由 `MaxBudget` 先到，自动少采。
这正是 §5.3 预算预检想要的行为。

校验规则（`Config` 构造时拒绝非法值）：

```text
MaxSamples >= 2
MinSamples >= 2 且 MinSamples <= MaxSamples
MinDwell   <= MaxBudget
MaxBudget  <= 3000ms
每档 MaxBudget <= 该阶段工作窗口的 10%
```

这些是**初始值**：实机数据出来后按 §8.2 的指标调整。调整只改服务端配置与本文档，
不触碰任何契约字段。

### 6.3 日志

沿用字段白名单（`internal/eventlog`）。新增两个键：

```text
samples   int   本次 settle 实际采集帧数（2..MaxSamples）
stable    bool  是否以"连续两帧相等"结束（false 表示预算耗尽）
```

复用已有键：`ms`（settle 总耗时）、`phase`（`start` / `act` / `open_app` 各自的采集
阶段名）、`operation_id`、`kind`、`action`、`code`。级别用 Debug，避免每次调用都写 Info。

指标口径：`stable=false` 的比例、`samples` 分布、`ms` 分布，按 `action` 分组。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 认领后不归还 | 不变：settle 发生在派发之后，O 早已 `claimed` |
| 提交条件化 | 不变：settle 的采样不提交；最终提交仍过原门禁（会话、epoch、工作位、显示代次、`CaptureMaxAge=15s`） |
| 状态锁短小 | 不变：settle 全程在锁外，只读取工作状态 |
| 单一工作位置 | 不变：settle 期间仍持有工作位，其他 Agent 依旧 `device_busy` |
| 工作窗口 | 不变：`start` 40s、`act` 50s、`open_app` 50s；三档 settle 预算分别占 3.8% / 4% / 6% |
| 会话租约 | settle 不提交 → 不续租；最长 1.5s，不影响 120s 租约 |
| 60 秒输入窗口 | 改善：`actionable_until` 从收敛帧算起，比现状更晚 |
| 事实分层 | 不变：settle 只影响"何时拍"，不影响 `outcome` 的判定 |
| 日志白名单 | 新增两个计数/布尔键，不引入正文 |

### 6.5 契约与文档影响

- **`/api/*` 字段不变、错误码不变、`contract_version` 不变。** 变化的是
  "后置观察在什么时刻发生"这一行为语义。
- 实现落地后需要同步修订：
  - `docs/architecture.md` §4（采集步骤：增加收敛采样）；
  - `docs/interface.md` §6（`act` 的后置观察描述：从"立即"改为"树收敛后"）。
- 按仓库规范，涉及 `docs/` 的变更必须先由独立的只读文档审计 subagent 复核，
  确认没有过程性措辞、没有与代码/清单的漂移。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

| 现象 | 是否能消除 |
| --- | --- |
| 转场 / 列表动画 / fling 的中间帧 | 能（树在变 → 不收敛 → 继续采） |
| 键盘弹起、可视区变化 | 能 |
| Activity 转场（前台包变化） | 能（L3 参与判据） |
| `Visible` 翻转导致的公开树变化 | 能（L2 参与判据） |
| 纯像素动画（淡入淡出、跑马灯、视频、进度条） | 不需要——它们本来就不改树，也不触发 `target_changed` |
| `start` 的半合成首帧 | 能（显示刚激活时的空树 → 首帧填充会改变 L1/L2） |
| `open_app` 启动动画的前半段（≤3s） | 部分能（受 `MaxBudget` 限制；更长的冷启动落入下一节） |

### 7.2 不能治什么

| 现象 | 原因 |
| --- | --- |
| **静态骨架屏**（树不变，2s 后内容才到） | 任意两帧相等，判据无法区分"就绪"与"静态等待"；只能靠 `MinDwell` 与动作策略缓解 |
| 采集之后才发生的变化（toast、时钟、广告位） | 任何基于采集的方法都无法预知 |
| 与本次动作无关的页面自抖动 | 同上；这是重验的固有代价，不是 settle 能解决的 |
| 永不稳定界面（跑马灯式**整块替换**、跟随滚动的 `contentDescription`、<300ms 的文本流） | 永远不会收敛 → 每次烧满预算，发布最后一帧并记 `stable=false` |

### 7.3 关于"静态中间态"的诚实说明

`MinDwell` 不能识别骨架屏：骨架屏本身是静止的，延长窗口只会让"静止"更确定。
它能做的是覆盖"过渡刚开始、树还没来得及变化"的窄缝。真正区分"加载中"与"已就绪"
需要业务语义（等待某个节点出现、等待文本变化），那属于 Agent 循环策略，超出后置
观察的范畴。

因此本方案的定位是：**消除"树在动"这一类失败（目前最高发的一类），并把剩余失败
暴露成可度量的 `stable=false`**，而不是宣称消灭 `target_changed`。

### 7.4 成本

| 路径 | dump 次数 | 典型增量 |
| --- | --- | --- |
| `act` 后置（`image=false`），2 帧收敛 | 2 | +0.4–0.9s |
| `act` 后置（`image=false`），预算耗尽 | 5 | ≤ +2.0s |
| `act` 后置（`image=true`） | 3–6 | +0.6–2.6s |
| `start` 首个观察 | 2–4 | +0.4–1.5s |
| `open_app` 后置 | 2–5 | +0.6–3.0s |

相对 40–50s 的工作窗口可以接受；对一条 20 步的流程，按典型 2 帧收敛估计约增加
8–18s 总时长，最坏情况（每步都烧满预算）约 +40s。

### 7.5 已知风险

1. **非原子 dump**：一次 dump 跨越 100–500ms，树本身可能是混合帧。表现为"不收敛"
   （安全），极端下两次混合帧可能偶然相等（概率极低，`MinSamples=3` 可压制）。
2. **永不稳定页面**：每次都烧满预算。用 `stable=false` 的比例识别，必要时按页面
   或动作关闭 settle。
3. **判据膨胀**：若未来有人把 settle 判据改松（例如忽略 `description`），会重新
   打开"判稳但重验失败"的裂缝。应以"判据是重验的超集"作为评审硬约束。

---

## 8. 验证计划

### 8.1 单元测试（`internal/engine`）

用可编排的 fake `Device`（按脚本返回 capture 序列）与 fake `Projector` 覆盖：

| 用例 | 期望 |
| --- | --- |
| `[A, B, B]` | 2 次比较后收敛，发布 B |
| `[A, B, C, D]` | 预算或帧数耗尽，发布 D，`stable=false` |
| `[A, A]`，`MinDwell=0` | 立即收敛，发布第 2 帧 |
| `[A, A]`，`MinDwell=500ms`，dump 80ms | 补足间隔后重采，第 3 帧仍相等才发布 |
| 第 2 帧采集报错 | 发布第 1 帧，不判后置观察失败 |
| settle 中途 STOP | 无 observation，`next=status`，`outcome=executed` |
| `Preview` 不推进状态 | 同一 capture 经 `Preview` 后 `Build`，ref 编号与不 settle 时完全一致 |
| 图片路径 | 图在 settle 之后读取，`actionable_until` 不早于树读取起点 |
| `start` 首个观察 | 使用 `Start` 档（300ms/1500ms），不借用 `Act` 档 |
| `open_app` 后置 | 使用 `OpenApp` 档（600ms/3000ms），冷启动序列在预算内收敛 |
| 三档互不干扰 | 同一进程内先后跑 `start`→`act`→`open_app`，各自的帧数与耗时互不影响 |
| 非法配置 | `MaxBudget>3000ms`、`MinDwell>MaxBudget`、`MaxSamples<2` 均在构造期被拒 |
| 响应形状不变 | `Result.observation` 的字段集合与基线提交一致，不出现 settle 相关成员 |

### 8.2 实机 A/B

同一设备、同一脚本、同一流程（例如：打开设置 → 进入列表 → 连续点击 5 项 → 滑动 3 次），
对比开启/关闭 settle：

| 指标 | 口径 |
| --- | --- |
| `target_changed` 率 | 按 `action`（tap/swipe/key/text）分组的失败次数 ÷ 该动作总数 |
| `start` 后首个 `act` 的成功率 | 验证"半合成首帧"是否减少 |
| `open_app` 后首个 `act` 的 `target_changed` 率 | 单独统计冷启动场景 |
| settle 耗时 | 按 `phase`（`start`/`act`/`open_app`）分组的 P50 / P95 |
| `samples` 分布 | 按档分组，2..`MaxSamples` 的占比 |
| `stable=false` 比例 | 按档分组，判断是否存在"永不稳定"页面 |

验收标准（建议）：

- `swipe` 与导航类 `tap` 的 `target_changed` 率显著下降；
- 各档 settle 的 P95 不超过该档 `MaxBudget` 的 75%（说明预算预检在正常工作）；
- `stable=false` 比例 ≤ 5%。

### 8.3 记录

按仓库规范写入 `maintenance/verification/`，注明代码提交、构建方式、设备/环境、
实际执行命令与真实结果；不在 `docs/` 中写验证结论。

---

## 9. 已定与未决

### 9.1 本轮已定

| 事项 | 结论 |
| --- | --- |
| 覆盖范围 | `act` 后置、`start` 首个观察、`open_app` 后置，三档同时落地 |
| 策略归属 | 固定在服务端配置，不作为请求字段，不开放给调用方 Agent 决定 |
| target-scoped 摘要 | 不做（理由见 §4.5） |
| `observe` | 不纳入；保留 `wait_ms` 作为唯一的调用方等待手段 |
| 初始取值 | `act` 400ms/2000ms、`start` 300ms/1500ms、`open_app` 600ms/3000ms |
| `MinSamples` | 三档统一取 **2**，不引入 3 |
| settle 元数据 | `samples` / `stable` **只进 eventlog**，不进入 `observation` 响应 |
| `shell` 的后置观察 | 不纳入 |

### 9.2 未决

1. **三档初始值的实机校准**：§6.2 的数值是推理取值，需按 §8.2 的指标回归调整；
   是否需要按操作子类型（`tap`/`swipe`/`text`）进一步细分，取决于 `swipe` 与导航类
   `tap` 的实际表现差异。

---

## 10. 实施顺序

1. 提取 `identityOf` 到共享位置；为 `Projector` 增加 `Preview` 并补单测。
2. 实现 `captureSample` / `captureDigest` / `settleCapture`，`SettlePolicies` 三档与
   日志键就位。
3. 依次接入 `act` 后置、`start` 首个观察、`open_app` 后置；每处先默认关闭，单测全绿
   后再默认开启。
4. 实机 A/B（三档分别对比），产出 `maintenance/verification/` 记录与
   `samples`/`stable` 分布。
5. 依据数据校准三档 `MinDwell`/`MaxBudget`，判断是否按操作子类型细分。
6. 修订 `docs/architecture.md`、`docs/interface.md`，通过独立只读文档审计后提交。

---

## 11. 实现结果

实现提交：`39683f478ff012d31220096b169939efea5c0a9f`。实机数据之外的落点与逐条检查见
[实现与验证记录](../../verification/act-post-observation-settle.md)。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |
| §4 三层摘要 | `internal/engine/settle.go`：`sampleDigestOf`、`hierarchyDigest`、`screenDigest` |
| §4.1 判据与重验同源 | `internal/engine/identity.go`：`rawIdentity`/`identityOf` 由 `act.go` 提出，重验与 settle 共用 |
| §4.5 不做 target-scoped 摘要 | 落地为整树判据，未引入局部摘要 |
| §5.1 采样间隔 | 每帧实测整帧耗时（读树 + 投影 + 摘要），作为下一帧的预算估计 |
| §5.2 `MinDwell` | `gap = MinDwell - since(prev.CapturedAt)`；只保证采集起点间隔，不存在"等待后发布旧帧" |
| §5.3 / §5.4 | `settleUntilStable`；发布一对相等样本的**后**一帧 |
| §5.5 预算 | 四个参数齐备；预算耗尽不算失败 |
| §5.6 失败与撤销 | 采样失败发布上一帧；STOP 报 `stopped` 且不发布观察 |
| §5.7 图片路径 | 先收敛、再读源图、再由 `captureSettledTree` 刷新一次树；不一致时在剩余预算内继续收敛 |
| §6.1 接口与文件 | `engine.Projector.Preview`、`observation.Builder.Preview`、`cmd/device-service` 适配 |
| §6.2 三档取值 | `DefaultSettlePolicies()`；`SettlePolicies.Validate()` 在进程启动时校验（非法值以退出码 2 拒绝） |
| §6.3 日志 | `observation.settle`（Debug）：`phase`、`samples`、`stable`、`ms`；白名单新增 `samples`、`stable` |
| §6.4 不变式 | 提交门禁、单一工作位置、租约与事实分层均未改动 |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |
| `act` 后观察被撤销时的响应 | **按 §5.6 的本意调整**：提案写"维持现有语义"，但基线实际是 `next="observe"` + `status=failed`；现改为 `outcome=executed`、`next="status"`、无观察，与派发前的撤销分支一致。已写入 `docs/interface.md` §6 |
| `Config` 校验的位置 | `engine.New` 不返回错误，因此 `SettlePolicies.Validate()` 在 `cmd/device-service` 启动时调用 |
| §8.2 实机 A/B | **未执行**：本环境没有设备连接，未运行 `adb`；指标只能在真机按脚本采样 |
| §10 第 5 步 三档校准 | **未开始**：依赖上一项数据，仍是 §9.2 的唯一未决项 |
| 按操作子类型（`tap`/`swipe`/`text`）细分 | **未开始**：同样依赖实机数据 |
| §8.1 "三档互不干扰" | 以"按阶段查表"单测加 `start`、`open_app` 两个端到端用例覆盖，未构造单进程连续三档的对照用例 |
| §8.1 其余用例 | 全部落地，逐条结果见[实现与验证记录](../../verification/act-post-observation-settle.md) §4 |

---

## 附：本提案不改变的事项

- `O` 的公开状态机（`ready/claimed/retired/expired`）与一次性语义。
- `P` 的幂等与重复回放语义。
- 单一工作位置、无普通动作队列。
- `act` 的三态判定（`executed/not_executed/uncertain`）与"不可重放"。
- 图片与树共享一次提交、`ImageMaxSpan=3s` 的绑定约束。
- 账本、STOP、收尾链与恢复屏障。
