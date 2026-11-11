# ref 连续性与静态页 delta 修复验证

范围：`internal/observation` 的公开树保号、context 节点匹配、full/delta 选择。
实现提交：本记录所在提交。
测试设备：`17efb04a`（Android，虚拟显示模式，测试机）。
测试构建：release `0.8.0`、`contract_version=8`、Android `0.8.0/10`，`source_dirty=true`
（实现与正式文档尚未提交时构建；产物哈希见 §7）。测试后未改变实现行为（仅注释与记录变更）。

结论分层：已实现；已单测；已实测（Settings、X/Twitter、哔哩哔哩、小黑盒）。

## 1. 现象与触发条件

静态页面上连续两次 `observe(O)` 的公开内容相同，但大量 ref 换号，导致 `Diff` 无法把
“父 ref 变化后的子树”表示为合法 delta，观察退回 full。典型样本：

| 场景 | 修复前 |
| --- | --- |
| Settings 主屏 | 128 节点，连续观察仍 full；约 107 个 ref 换号 |
| X/Twitter 时间线 | 154 节点，连续观察 full；约 105 个 ref 换号 |
| 小黑盒帖子详情 | 86 节点，连续观察 full；约 60 个 ref 换号 |
| 哔哩哔哩动态帧 | full；动态内容变化本身可以解释，但静态帧也无法得到空 delta |

代价是客户端无法依赖固定基线，agent 的 ref 缓存频繁失效，并且每次观察重复传输整棵树。

## 2. 复现用例

新增 `internal/observation/refstability_test.go`，用受控 capture 固定以下路径：

| 用例 | 修复前红输出 |
| --- | --- |
| `TestContinuityContextWrapperKeepsRef` | context wrapper `n2 -> n4`，`Diff` 报“node ... now lives inside new node ...” |
| `TestContinuityAnonymousContainerClassChangeKeepsRef` | 匿名容器 `n2 -> n4` |
| `TestContinuityLabeledClassChangeKeepsRef` | 有标签节点 `n2 -> n3` |
| `TestContinuityRowsUnderIdentifiedScrollKeepRefs` | 带自身 resource 的 scroll 下，行容器 `n3 -> n7` |
| `TestContinuityUnpublishedResourceDoesNotForceNewRef` | 未发布 resource 变化被当成身份变化（新增用例，逻辑红） |
| `TestContinuityNewDuplicateDoesNotStealRef` | 单按钮变为两个同身份按钮时，旧的单候选被任意顺序配给第一个（文档审计发现；修复前逻辑红） |
| `TestContinuityHiddenDuplicateDoesNotConsumeRef` / `TestContinuityHiddenSiblingDoesNotBreakRowRefs` | 回归守卫；修复前未发布节点因 role 为零未进入匹配，测试通过 |

执行：

```text
$env:GOEXPERIMENT='jsonv2'
go test ./internal/observation -count=1
```

修复前四个用例失败；修复后全部通过。完整 `go test ./...` 亦通过。

## 3. 根因

行号按修复前代码提交 `f1da2d5`。

| 编号 | 根因 | 位置 | 为什么走到这条分支 |
| --- | --- | --- | --- |
| A | 容器匹配先于身份匹配，父节点稍后才成为锚点 | `internal/observation/match.go:171-205`（Pass 1 在 `:207-234` 的精确身份匹配之前） | Settings 的 scroll 带未发布 resource，被 `hasIdentity` 当成身份节点；它在 Pass 2 才匹配，而其匿名行已在 Pass 1 因 `prevParent` 不存在而跳过 |
| B | 未发布的 raw resource 被当成公开身份 | `internal/observation/match.go:177`、`:293-304` | `hasIdentity` 只看 `hasRes`；projection 只为“无标签且可点击”的节点发布 resource，图标等未发布 resource 的节点因此跳过容器匹配，精确匹配又因 class/resource 变化失败 |
| C | 匿名容器的 `sameShape` 要求 raw class/package 相等 | `internal/observation/match.go:419-425` | 公开树不发布 class/package；它们变化不应单独导致换号，但旧形状匹配把它当成硬门槛 |
| D | context 节点没有 raw 来源，上一帧 key 为空 class | `internal/observation/build.go:113-119`、`internal/observation/match.go:106-113` | `Snapshot.Source` 排除 context；上一帧 context key 只有 role/label，当前帧 key 带 raw class，`sameShape` 永远不相等 |
| E | 当前/上一帧的兄弟计数与序号使用 raw children | `internal/observation/match.go:381-389`、`:322-353`、`:357-368` | 未发布兄弟、跨投影层级的父节点参与计数/锚点，和公开树看到的兄弟列表不一致 |
| F | 全局唯一性计数包含未发布节点 | `internal/observation/match.go:309-317` | 未发布节点也进入 `preorder` 和 `curSig`，可能让公开侧唯一身份不再唯一 |
| G | 同父精确身份/input 只检查上一帧候选唯一，不检查当前侧 | `internal/observation/match.go:347-359`、`:397-408` | 一个旧节点对应两个同身份新节点时，遍历顺序会把旧 ref 任意配给第一个；违反“歧义新号”不变式 |

修复后 `Diff` 仍可能因为真实结构变化（新增/删除整棵子树、真实跨父移动）退回 full；这属于
合法回退，不是本缺陷。

## 4. 修复提交与边界

修复集中在一个实现提交：

- `internal/observation/build.go`：新增 `Snapshot.ContextSource`，只保存 context 节点的 raw
  索引；`Source` 仍只保存可动作/可读节点。
- `internal/observation/match.go`：
  - 匹配空间只保留发布单元；未发布节点不消耗 ref、位置或唯一性名额；
  - context 节点用独立 `contextSignature`，上一帧从 `ContextSource` 取 raw 证据；
  - 匿名容器按公开形状、最近已发布父锚点、同形兄弟序号匹配；非动作容器忽略未发布
    class/package，可动作匿名容器仍要求 class/package 一致；
  - 新增公开身份 fallback（同父唯一 / 全局唯一），只比较 role、公开 label、公开 resource、
    protected、actions，不比较未发布 class/package；
  - 精确身份匹配和容器匹配之间增加有界重试，解决父节点晚匹配导致的锚点缺失；
  - 同父精确身份与 input 锚点匹配增加“当前侧同身份兄弟唯一”检查，避免一个旧 ref 被
    遍历顺序任意配给多个新节点之一。
- `internal/observation/refstability_test.go`：上述复现与回归用例。

公开请求/响应字段、错误码、`contract_version`、MCP schema 与 delta 五类块均未改变。行为上
静态帧更可能返回空 delta；无法表达或超过 70% 编码门时仍返回 full。

`docs/architecture.md` §4 补充了保号口径：公开事实与已匹配父锚点、未发布节点不参与、
context 独立来源、可动作匿名容器保持 class/package 一致、歧义新号。

## 5. 同类核查

- 检查了 `Source`、`ContextSource`、`preorder`、`curSig`、`hasIdentity`、`sameShape`、
  `ordinalSibling`、`countCurrentSiblings`、`at` 的所有使用点；未发布节点参与匹配的同类
  路径已统一过滤。
- 检查了 raw class/package 作为硬身份的所有形状匹配；只保留可动作匿名容器的严格分支，
  其余改为公开形状/公开身份。
- 检查了父锚点来源；统一改为最近已发布祖先，避免 raw 父节点未投影时锚点缺失。
- HTTP、MCP、engine 的 full/delta 选择与基线确认未改动；本次未发现第二套投影或默认
  full 的路径。

## 6. 未覆盖项与原因

| 项 | 状态与原因 |
| --- | --- |
| 真实设备上的 context 节点样本 | 未取得：本轮四个静态页面的公开树没有 context group；context 路径由先红单测覆盖 |
| 非空 delta 的实机语义 | 已取得哔哩哔哩非空 delta 样本（set/add/remove/children/replace，约 1.1KB）；服务端 `Diff` 以 `Apply`/`Equal` 验证通过，客户端侧应用到本地副本未单独执行 |
| 长时间动画/视频帧的 ref 稳定性 | 未执行：动画帧的真实内容变化可能合理退回 full，不能作为本修复的失败样本 |
| 可动作匿名容器的错误换号注入 | 未执行：已保留 class/package 严格分支；未构造两个同形可动作按钮互换的实机场景 |
| 文档审计 | 独立只读审计结论 CLEAN（2026-11-12）；已修正保号依据、input/可动作容器的 class/package 要求与当前侧唯一性表述 |

## 7. 实机复测

构建、校验、部署：

```text
pwsh -NoProfile -File scripts/build-release.ps1 -Output build/refstability-release
pwsh -NoProfile -File scripts/verify-manifest.ps1 -Manifest build/refstability-release/manifest.json
pwsh -NoProfile -File scripts/deploy.ps1 -SkipBuild -Manifest build/refstability-release/manifest.json
```

结果：构建成功；`verify-manifest` PASS（`source_dirty=true` 警告，6 个产物哈希校验通过）；
`deploy` PASS（版本、配对、恢复屏障、STOP、手机 HTTP MCP 适配器均通过）。测试构建的
`device-service` SHA-256 为 `b749a2d3e5c5bc2addc3b7045bb4e3a88962083f471f9a3cf1fa1afdf1b04af1`。

设备观察（均为真实虚拟屏，HTTP 直连服务；未打印 token）：

| 场景 | 结果 |
| --- | --- |
| Settings：`open_app` | full，128 节点，完整树 JSON 约 11106 字节 |
| Settings：连续两次 `observe(O)` | 均为 delta；`base` 为 open_app 的 full O；`set/add/remove/children/replace` 全为 0；编码约 67 字节 |
| X/Twitter：`open_app` 后连续两次 `observe` | 均为 delta；空差异；约 67 字节 |
| 哔哩哔哩：`open_app` 后连续两次 `observe` | 均为 delta；第一次 `set=2/add=1/remove=1/children=1/replace=1`，约 1171 字节；第二次 `add=1/remove=1/children=1/replace=1`，约 1083 字节 |
| 小黑盒：`open_app` → 点击帖子 → `act` 后观察 | `act` 交付 full；之后两次 `observe` 均为 delta；空差异；约 68 字节 |

修复前同一设备/同一路径的 Settings 连续观察为 full（约 107 个新 ref），小黑盒帖子详情为
full（约 60 个新 ref）；修复后的空 delta 与 §2 的单测方向一致。哔哩哔哩动态帧出现真实内容
变化时仍返回非空 delta，说明保号稳定后 `Diff` 能表达这些变化；更大的结构变化若无法表达或
超过 70% 门，仍按既有规则退回 full。
