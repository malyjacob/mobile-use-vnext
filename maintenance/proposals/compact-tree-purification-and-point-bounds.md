# 提案：紧凑读树内容净体、轻量视觉提示与非根/滚动节点二元中心坐标（契约 28）

状态：提案，未实现。
基线提交：`efe3180069bd0352cff78c78ee4b7ca02566e491`（分支 `master`，`2026-11-24`）。
适用范围：公开树构造（`internal/observation`）、紧凑观察树与固定基线差分（`compact` `ReadTree`/`ReadDelta`）、观察视觉特征提示（`Observation.visual`）、普通树紧凑文本编码（`tree v5`）、MCP `result v4` 文本投影、发现说明与工具描述，以及发布包与双入口探针。
影响范围：`release/versions.json`、生成的版本文件、`internal/protocol`、`internal/observation`、`internal/engine`、`internal/treetext`、`internal/mcp`、`scripts/mcp-probe` 及正式文档 `docs/`。
契约（`/api/*` 字段与 `contract_version`）：改变（共同 `contract_version` 自 `27` 升至 `28`，`release_version` 升至 `0.32.0`，`android_version_code` 升至 `34`：`compact` 视图中非 `root`/`scroll` 节点的已知未隐藏 `bounds` 由 4 整数矩形数组改为 2 整数中心点数组 `[x,y]`；`Observation` 新增可选字符串成员 `visual`；MCP 普通观察树块头自 `tree v4` 升至 `tree v5`）。

本文引用的函数名、行号、常量与默认值均以该基线提交为准；实现落地前若代码已前进，
须按当时的 `HEAD` 重新核对（重点：`internal/observation/build.go`、`internal/observation/read.go`、`internal/observation/read_diff.go`、`internal/protocol/read_tree.go`、`internal/protocol/responses.go`、`internal/engine/capture.go`、`internal/treetext/encode.go`、`internal/mcp/result_text.go`、`internal/mcp/schema.go`、`scripts/mcp-probe/`）。

---

## 0. 摘要

在契约 `22`–`27` 完成外壳瘦身（`result v4` + 短 `O`/`P`）、截图长边 `1024` 与多基线 `tree v4` 差分选择后，观察树自身的冗余节点、复合重复文本与四元组坐标仍是长会话上下文的最大消耗源。本提案在不破坏完整内部树（`Snapshot`）保号、安全落点规划、`wait_for` 内容稳定门禁与 `inspect` 详细补看兜底的前提下，分两层落地节点树展示优化与二元坐标改造：

1. **源投影层（Stage 1，`build.go`）**：仅对公开可读标签清洗零宽字符与 `U+FFFC` 对象替换符，不改动 `device.Capture` 原始节点与 `input.value`，消除纯占位空节点并修复 `wait_for`/`inspect` 字面查询失配。
2. **紧凑读树与视觉提示层（Stage 2/3，`read.go`、`read_tree.go`、`capture.go`、`treetext`、`mcp`）**：全裁无标注非交互 `image` 叶并配套四重精准口径轻量视觉提示（`visual=image_heavy(<pct>%)` / `visual=sparse_tree`）；裁剪被祖先复合标签覆盖的纯被动文本叶；将无字可点击 `view` 容器的纯被动文本子叶提升合并为 `button`（保留原生多段 `Label` 切片）；裁剪 `<=4px` 露边幽灵细条、去重 `input` 相等的 `label`/`value`、提取 `resource` 语义短尾名；将 `compact` 视图中非 `root`/`scroll` 节点的交付几何改为二元中心点 `[x,y]` / `x,y`（普通树块头升至 `tree v5`）。

本提案**明确不做**多分支隐藏匿名容器扁平化（避免扭曲公开树层级结构）、**不做**超长文本预览截断、**不做** 3px 近邻坐标模糊篡改，也**不改变** `inspect v3` 详细补看的完整节点集与四元矩形 `[l,t,r,b]`。

---

## 1. 问题

### 1.1 现象与代价

根据 DSH 实机 4-App（哔哩哔哩、知乎、网易云音乐、闲鱼）长会话研究报告（`.state/dsh-session-2dd44960/research/mcp-context-research-0.24.0.md`，166 棵观察树、11,153 个公开节点）：

1. **树内结构性冗余严重**：
   - 无标注非交互装饰 `image` 叶泛滥（705 个节点，耗 9,240 token）：现行策略仅裁剪面积 `< 1%` 屏幕的小图，占屏 `1%`–`100%` 的信息流封面配图与全屏皮肤底图仍大量占据公开树，模型既看不到像素也无法直接点击；
   - 父子卡片“复合文本复读”（471 个纯文本子叶，耗 4,732 token）：App 为无障碍读屏将卡片内所有子文本拼接为父按钮的 `contentDescription`，同时子 `TextView` 又逐行重复输出同一批文字；
   - “无文字可点击容器 + 纯文本子叶”双层套壳按钮（645 处，单文本 561 处、多文本 84 处，耗 3,066 token）：外层 `LinearLayout(clickable=true)` 包内层 `TextView`，被迫拆成两行、两个 `ref`、显式 `tap`，且外层因自身无文字还被挂上冗长的 `resource` 提示；
   - `resource` 业务埋点串过长（695 个节点，耗 8,625 token，如 `Answer:Card:OpenUrl:content_card:5:negativeFeedback`）；
   - 隐藏字符污染与边界噪声（192 个节点含 `U+200B`/`U+FFFC` 等，耗 ~1,257 token）：闲鱼等应用在汉字间插入 `\u200b`（如 `个\u200b人\u200b闲\u200b置\u200b`）或输出纯 `"￼￼"` 节点，既切碎 BPE token，又导致 `wait_for(query="个人闲置")` 与 `inspect(query=...)` 隐形失配；另有 `input` 同时输出相同 `label` 与 `value`（13 处）及宽/高 `<= 4px` 的露边幽灵细条（19 处）。
2. **四元矩形坐标开销占比达整树 28.98%（3,914 个四元组，37,716 token）**：
   - 95% 以上的点按、长按与输入均通过 `target={ref:"n..."}` 由服务端基于内部完整矩形安全选点执行；模型真正需要完整四边边界 `l,t,r,b` 的主要是 `root` 与 `scroll` 容器，其余控件只需中心坐标 `x,y` 即可完整获知屏幕方位、行列顺序与兜底点击参考点（122 棵整树单因子实测改中心点可省 19,344 token，`-14.43%`）。

### 1.2 证据链

| 既有链路与行为 | 源码位置 |
| --- | --- |
| Stage 1 构造完整内部树 `Snapshot`：严格按原始 `Text`/`Description` 判定语义与角色，未清洗零宽字符或 `U+FFFC` | `internal/observation/build.go:236-282`、`311-333`、`354-408` |
| `needsResourceHint` 在控件自身 `len(node.Label) == 0` 且可点击时附加完整 `raw.ResourceID` | `internal/observation/build.go:435-438`、`465-470` |
| `populateScrollAxes` 依赖 `scroll` 容器直接公开子项的几何排布推断 `axis=x\|y` | `internal/observation/build.go:566-667` |
| 六阶段保守保号依赖公开父容器层级（局部唯一、后代集合投票、同锚定父静止几何双射）及完整 `ResourceID` | `internal/observation/match.go:25-40`、`90-171`、`305-439` |
| `act` 静态规划要求非 `context` 公开节点与 `Capture.Nodes` 一对一双射，并优先取容器内命名子节点矩形作 `tapCarrier` | `internal/engine/tap_scope.go:333-416`、`552-633`、`752-778` |
| `act` 实时重验与 `text` 写入读回校验直接比对不可变的 `device.Capture.Nodes` 原始字段 | `internal/engine/act_revalidation_policy.go:89-103`、`internal/engine/text_submit.go:289-295` |
| `wait_for(query)` 与 `inspect(query)` 在 Stage 1 `protocol.Tree` 上做字面折叠子串匹配 | `internal/engine/wait_for.go:699-719`、`internal/engine/inspect.go:265-278` |
| `wait_for(stable=true)` 的 `filterWaitContent` 在 Stage 1 `Tree` 上仅擦除“无子节点且无动作”被动叶子的 `Label`/`Value`/`Hint` | `internal/engine/wait_content.go:48-62` |
| Stage 2 `ProjectCompactTree` 目前仅裁剪隐藏几何且 `<1%` 屏面积的无标注 `image` 叶，并绕过单子隐藏匿名容器 | `internal/observation/read.go:145-227` |
| `ReadDiff` 要求同一 `ref` 跨帧 `Role` 不变（否则返回 `ErrNotRepresentable` 由 `capture.go` 回退 `full`），并用 `ReadApply` + 规范 JSON 字节自校验 | `internal/observation/diff.go:117-119`、`internal/observation/read_diff.go:13-47` |
| `ReadBounds` 目前只有 `Hidden`/`Unknown`/`Known(4元矩形)` 三态，且 `ReadSetNode` 不含 `Role` | `internal/protocol/read_tree.go:26-98`、`606-619` |
| `cmd/device-mcp` 无跨调用状态：渲染 `delta` 时仅持有当次 `Result.Observation.Tree.Delta`，无历史 `base` 整树 | `internal/mcp/tree_text.go:21-39`、`internal/treetext/encode.go:38-53`、`159-186` |
| `result v4` 通过 `v4ResultFields` 完整性门禁强制覆盖 `protocol.Result` 每个线上面向成员 | `internal/mcp/result_text.go:33-95`、`928-955` |
| `mcp-probe` 持有独立的 `result v4` 与普通树块解析器做双模式一致性核验 | `scripts/mcp-probe/result_v4.go:43-153`、`scripts/mcp-probe/result_text.go:28-109` |

### 1.3 为什么直觉会出错

1. **为什么不能在 Stage 1（`build.go`）直接裁装饰图、删复合复读子叶或合并可点击套壳文本？**
   - 若在 Stage 1 合并可点击外框与内层文本子叶，像广告 `"跳过 5s"` 这类“可点击 `LinearLayout` 套被动 `TextView`”会变成“带 `tap` 的有字节点”，导致 `wait_content.go:54` 不再忽略秒数跳动，`wait_for(stable=true)` 在倒计时界面无法收敛；同时 `tap_scope.go:752` 会失去内层 `TextView` 的精确命名载体框；
   - 若在 Stage 1 删装饰图、截短 `ResourceID` 或删子节点，`match.go` 的 `ResourceID` 区分度与后代投票证据会退化，且 `inspect`（`detailed`）将永久失去原始节点、完整资源 ID 与四元矩形兜底能力。
2. **为什么不能只在 Stage 3（`treetext` 文本编码层）把非 `root`/`scroll` 缩成 `x,y`，而让 Stage 2 `ReadBounds` 保持四元组？**
   - `cmd/device-mcp` 是无状态适配进程，收到 `delta` 结果时只有 `ReadDelta` 而没有 `base` 树；`ReadDelta.Set[ref]`（`~` 行）不携带节点的 `Role`，若 `ReadBounds` 自身不区分四元矩形与二元点，`EncodeDelta` 根本无法判断 `~ n10 bounds=...` 属于 `scroll`（应写四元组）还是 `button`（应写二元组），且对称微伸缩在中心点未变时仍会产生冗余 `~` 噪音，同时破坏 `treetext` 独立无损还原 `ReadTree` 的性质。
3. **为什么全裁装饰图不能配套“按大图并集面积静默自动塞截图”？**
   - 实机轨迹中双列图文信息流（B 站/闲鱼首页）与全屏背景皮肤图占屏普遍达 `48%`–`100%`，静默自动附图会误触发 27–40 次，反亏 1.5 万–4.4 万视觉 token；而真正的纯视觉盲区（视频播放隐藏控件、直播、Canvas）往往 `image` 节点数为 `0`。必须采用“四重过滤口径 + 轻量文本提示 `visual=...`”，把 `snap` 决策权留给模型。

---

## 2. 目标与非目标

### 目标

1. Stage 1（`build.go`）对公开可读标签清洗零宽字符与 `U+FFFC`，消除纯占位假语义节点，使 `wait_for(query)` 与 `inspect(query)` 正常匹配，同时保持 `device.Capture` 与 `input.value` 原样不变。
2. Stage 2（`read.go`）在 `compact` 投影中按确定性顺序完成五项净体：全裁无标注非交互 `image` 叶（A3c）、裁剪被祖先复合标签覆盖的纯被动文本叶（A1a，含短字母数字词边界守卫）、提升无字可点击 `view` 的纯被动文本子叶为 `button`（G2b，保留多段 `Label` 切片并清除冗余 `resource`）、去重 `input` 相等 `label`/`value`（G3）、裁剪 `<=4px` 露边幽灵细条（G5）与提取 `resource` 语义短尾名（A5a）。
3. 在完整公开树上按四重精准口径计算页面视觉特征，新增可选 `Observation.visual`（`"image_heavy(<pct>%)"` 或 `"sparse_tree"`），在 `full` 或视觉状态由无变有时提示，同状态 `delta` 不重复输出。
4. `protocol.ReadBounds` 在 `compact` 视图中正式引入二元中心点 `GeometryPoint`（JSON `[x,y]`，`tree v5` 普通节点 `x,y`，`~` 行 `bounds=[x,y]`），仅 `root` 与 `scroll` 保留四元矩形 `GeometryKnown`；普通树文本块升级为 `tree v5`，共同契约升至 `28`。
5. 同步更新 `internal/mcp/schema.go`（`treeTextGuide`、`callerGuidance`、工具描述）、`scripts/mcp-probe` 独立解析器与 `docs/` 正式文档。

### 非目标

- **不拍平多分支隐藏匿名容器（A2）**：保留现行“仅清理变空隐藏匿名容器（`len == 0`）与绕过单子隐藏匿名容器（`len == 1`）”的规则，`len >= 2` 的匿名容器维持原有层级，避免扭曲树结构。
- **不截断超长标签文本（A4a）**，也不做 `3px` 近邻坐标模糊吸附（A6）。
- **不修改** Stage 1 `protocol.Node.Bounds`、`act` 静态选点/重验、`error.details.candidates[*].bounds` 及 `inspect v3`（`view=detailed`）的完整节点与四元矩形语义。
- **不改变**多基线池窗口（当前基线 1–10、备用 1–4、龄期 `<180s`）与 `tree v5` 差分 `<70%` 字节门槛。

---

## 3. 路线对比

| 路线 | 做法 | 代价 | 失败模式 |
| --- | --- | --- | --- |
| R1 什么都不做 | 保持现行契约 `27`（`<1%` 裁图 + 全四元组坐标 + `tree v4`） | 零开发与升级成本 | 每场 200 步任务继续白烧数万冗余装饰图、复读文本、套壳按钮与四元组坐标 token，闲鱼零宽字符继续导致 `wait_for` 查询失配 |
| R2 仅在 Stage 3 `treetext` 缩减二元坐标，`ReadBounds` 保持四元组 | `/api/*` 与 `structuredContent` 的 `compact` JSON 不变，仅文本层写 `x,y` | 表面不改 JSON 坐标形状 | 无状态 `device-mcp` 在 `delta` 的 `~ ref bounds=...` 上因缺少 `base` 与 `role` 无法区分 `scroll` 与普通节点，导致 `full` 与 `delta` 坐标语法分裂、中心未变的外框微动仍产生 `~` 噪音，且破坏 `treetext` 独立无损回写 `ReadTree` |
| R3 连同多子匿名容器一起在 `compact` 全局拍平 | 除净体与二元坐标外，将所有 `len >= 2` 的隐藏匿名容器一并拍平 | 进一步压缩缩进与空容器行 | 多列卡片、分组面板的子项全部混入同一父层，严重扭曲页面视觉分组结构，增加模型误判同卡归属的风险 |
| **R4 分层净体 + 轻量视觉提示 + `compact` `ReadBounds` 二元 `Point`（选定）** | Stage 1 仅洗公开标签隐藏字符；Stage 2 `compact` 执行五项净体（保留多子匿名容器）并引入 `GeometryPoint`（`root`/`scroll` 与 `inspect` 保四元组）；Engine 产出受控 `Observation.visual`；树块升 `tree v5`、契约升 `28` | 需升级共同契约至 `28` 并整组构建部署服务、App、MCP 与探针 | 若大卡片几何中心恰好压着内层独立子按钮，模型按 `ref` 点击会自动避让，但若手工抄中心 `x,y` 发坐标点击会落到子按钮（需外框时用 `inspect`）；极少数同 `ref` 跨帧在“套壳 `button`”与“无字 `view`”间翻转时自动回退单帧 `full` |

**选择理由**：R4 严格顺应三层架构职责——Stage 1 保住引擎执行、保号、稳定门禁与 `inspect` 的全部精确证据；Stage 2 让 `compact` JSON 与 `tree v5` 文本保持 100% 无损同构，使无状态 `device-mcp` 在 `full` 与 `delta` 上自包含工作；同时保留多子匿名容器不拍平以维护树分组结构，并用四重口径轻量 `visual` 提示消除全裁装饰图后的纯视觉页盲区。多子匿名容器扁平化留待本轮实机观察效果后再评估是否需要更保守的局部折叠条件。

---

## 4. 判据

### 4.1 Stage 1 公开可读标签净化判据（G1）

1. **受控剔除码点集合（闭集）**：`U+200B`、`U+200C`、`U+200D`、`U+2060`、`U+FEFF`、`U+FFFC`。
2. **清洗规则 `cleanReadableText(s string) string`**：
   - 若 `s` 不含上述 6 个码点中的任何一个：**原样返回 `s`**（不改动普通文本原本的首尾空白或换行）；
   - 若 `s` 含至少一个上述码点：剔除全部上述码点，随后将连续换行（含中间仅由水平空白组成的空行残余，匹配 `\n[ \t]*\n+`）折叠为单个 `\n`，并对结果执行 `strings.TrimSpace`。
3. **作用边界**：
   - 仅作用于 `build.go` 的 `labelParts(raw)`（先对 `raw.Text`、`raw.Description` 分别调用 `cleanReadableText`，非空才采纳，且 `cleanDesc != cleanText` 时才追加第二段）、公开 `Hint`（若存在）及 `match.go` 中输入框描述锚点 `raw.Description`；
   - `hasSemantics(raw)`（`build.go:311`）对非密码、非编辑节点的文本语义存在性改为判定 `len(labelParts(raw)) > 0`，使仅含 `"￼￼"` 或 `"\u200b"` 且无动作、无非默认状态的节点不进入公开树、不分配 `ref`；
   - **不进入判据/不修改**：`device.Capture.Nodes` 原始字段与 `RoleInput` 的 `node.Value` 保持原始字节不变，因此与 `act` 重验（`structureKeyOf`）及 `text` 写入精确读回（`GetTextOnDisplay`）完全正交。

### 4.2 Stage 2 `compact` 几何表示与幽灵细条判据（X2 + G5）

1. **`ReadBounds` 四态定义（`internal/protocol/read_tree.go`）**：
   - `GeometryHidden`：已知几何按 `compact` 策略省略（完整节点省略 `bounds`，`set` 写 `"hidden"`，`tree v5` 普通节点省略几何列、`~` 行写 `bounds=hidden`）；
   - `GeometryUnknown`：源未提供可用几何（JSON 写 `null`，`tree v5` 普通节点写 `-`、`~` 行写 `bounds=null`）；
   - `GeometryKnown`（四元矩形 `Rect Bounds`）：JSON 写 `[left,top,right,bottom]`（满足 `right >= left && bottom >= top`），`tree v5` 普通节点写 `l,t,r,b` 或 `@`，`~` 行写 `bounds=[l,t,r,b]`；
   - `GeometryPoint`（二元点 `Point{X, Y int}`）：JSON 写 `[x,y]`，`tree v5` 普通节点写 `x,y` 或 `@`，`~` 行写 `bounds=[x,y]`。
2. **视图不变式（`ReadTree.Validate()`）**：
   - `view == "detailed"`（`inspect`）：禁止 `GeometryHidden` 与 `GeometryPoint`，所有节点必须为 `GeometryKnown` 或 `GeometryUnknown`；
   - `view == "compact"`（普通观察）：任何携带已知几何的节点，当且仅当 `Role == RoleRoot || Role == RoleScroll` 时为 `GeometryKnown`，其余角色必须为 `GeometryPoint`。
3. **幽灵细条裁剪判据（G5）**：
   - 非 `root`、非 `context`、`out.Role == RoleView`（`image` 仅受 A3c 管辖，`input`/`scroll`/`button` 等显式角色不受 G5 裁剪）、原始内部节点无非 `nil` 子节点（`!originalHasChildren`）、`len(out.Label) == 0 && out.Value == nil && out.Hint == nil && out.Axis == ""`、不含 `ActionText`/`ActionSwipe`（允许无动作或仅带 `tap`/`long_press` 的视口边缘露边细条）、`compactKnownDefaultState(out)` 且 `n.Bounds != nil`；
   - 设 `clipped, positive := compactRect(*n.Bounds, screen)`：当 `screen.screenKnown` 且（`!positive` 或 `min(clipped.Width(), clipped.Height()) <= 4`），或 `!screen.screenKnown` 且 `min(n.Bounds.Width(), n.Bounds.Height()) <= 4` 时，该叶节点在 `compact` 中直接剪除（返回 `nil`）。
4. **中心点计算与 `@` 判据**：
   - 隐藏判定先按原四元矩形执行（`AnonymousPlain(out) || n.Role == RoleText`、与父链最近已交付节点的裁屏四元矩形相等 `rect == *reference`、或裁屏面积 `< 1%` 屏幕面积）；
   - 判定交付时，向子树传递的四元参考框 `reference` 仍更新为 `deliveredRect(*n.Bounds, screen)`；若 `n.Role == RoleRoot || n.Role == RoleScroll` 则 `out.Bounds = KnownBounds(*n.Bounds)`，否则对 `rect`（`screenKnown` 且 `positive` 时取 `clipped`，否则取 `*n.Bounds`）以无溢出半开中点公式计算：
     `x = midpoint(rect.Left, rect.Right)`，`y = midpoint(rect.Top, rect.Bottom)`（与 `tap_scope.go:537` 的 `tapSafeAverage` 一致，恒满足 `rect.Contains(x, y)`），赋 `out.Bounds = PointBounds(x, y)`；
   - `tree v5` 中的 `@`：普通非 `chain` 节点具有已知几何（`GeometryKnown` 或 `GeometryPoint`），且同一串行化定义（`full`、`+` 子树或 `=` 子树）中最近一个具有已知几何的严格祖先满足 `node.Bounds.Equal(ancestor.Bounds)`（同 `Kind` 且数值完全相等）时写 `@`。

### 4.3 Stage 2 `compact` 树内容净体判据（G3 / A3c / A1a / G2b / A5a）

1. **`input` 标签与值去重（G3）**：`out.Role == RoleInput && out.Value != nil && len(out.Label) == 1 && out.Label[0] == *out.Value` 时，置 `out.Label = []string{}`。
2. **纯被动文本叶谓词 `isPlainPassiveTextLeaf(n *protocol.ReadNode)`**：
   - `n != nil && !n.Context && !n.IsChain() && n.Role == RoleText && len(n.Children) == 0 && len(n.Label) > 0 && n.Bounds.Kind == GeometryHidden && len(n.Actions) == 0 && n.Value == nil && n.Hint == nil && n.Resource == nil && n.Axis == "" && compactKnownDefaultState(n)`。
3. **无标注非交互 `image` 叶全裁（A3c）**：
   - `!originalHasChildren && !out.Context && out.Role == RoleImage && compactNoReadableContent(out) && compactKnownDefaultState(out)`（不限 `Bounds.Kind`、不限面积占比）。
4. **祖先复合标签覆盖纯被动文本叶裁剪（A1a）**：
   - 当前节点原始无子节点且满足 `isPlainPassiveTextLeaf(out)`；
   - 祖先栈仅收集在 `compact` 中实际保留了非空 `out.Label` 的严格祖先；设空白折叠函数 `norm(s) = strings.Join(strings.Fields(s), " ")`：
     - 若存在某个祖先 `Label` 切片与 `out.Label` 逐项完全相等，判为覆盖；
     - 否则要求 `out.Label` 的每一项 `part` 在归一化后 `np := norm(part)` 非空，且存在某个祖先的归一化拼接串 `anc`（各非空归一化段以单空格连接）存在子串匹配位置 `[i, j)`（`anc[i:j] == np`）满足词与复合数值边界守卫：
       - 若 `np[0]` 为 ASCII 字母/数字（`[0-9A-Za-z]`）或 `len(np) == 1 && np[0] < 0x80`，则左侧相邻字符（`i > 0` 时 `anc[i-1]`）不得为 `[0-9A-Za-z]`；且若 `np[0]` 为 ASCII 数字（`[0-9]`），左侧不得为“ASCII 数字 + `':'`/`'.'`/`'/'`/`'-'`”（即排除 `i >= 2 && isASCIIDigit(anc[i-2]) && isNumConnector(anc[i-1])`）；
       - 若 `np[len(np)-1]` 为 ASCII 字母/数字（`[0-9A-Za-z]`）或 `len(np) == 1 && np[0] < 0x80`，则右侧相邻字符（`j < len(anc)` 时 `anc[j]`）不得为 `[0-9A-Za-z]`；且若 `np[len(np)-1]` 为 ASCII 数字（`[0-9]`），右侧不得为“`':'`/`'.'`/`'/'`/`'-'` + ASCII 数字”（即排除 `j+1 < len(anc) && isNumConnector(anc[j]) && isASCIIDigit(anc[j+1])`）；
       - 其余情况（如纯中文短语）子串命中即成立。
5. **无字可点击 `view` 纯文本子叶提升合并为 `button`（G2b）**：
   - 在子节点完成 A3c、A1a、变空容器清理与**单子隐藏匿名容器绕过**之后检查当前节点 `out`：
   - `!out.Context && out.Role == RoleView && len(out.Label) == 0 && out.Value == nil && out.Hint == nil && out.Axis == "" && hasAction(out.Actions, ActionTap) && len(out.Children) >= 1`；
   - 且 `out.Children` 中**每一个**直接子节点均满足 `isPlainPassiveTextLeaf(child)`；
   - 满足时：`out.Role = RoleButton`，按子节点顺序将每个 `child.Label` 的非空项依次追加至 `out.Label`（保持 `[]string` 原生多段切片，不插入人工连接符），置 `out.Children = []*protocol.ReadNode{}`，并置 `out.Resource = nil`（已有明确可读标签后不再保留无字资源提示）。
6. **`resource` 语义短尾名提取（A5a）**：
   - 对上行归约结束后仍保留 `out.Resource != nil && *out.Resource != ""` 的节点，按 `:` 与 `/` 切分非空段，自右向左取最后一个含非 ASCII 数字字符（即不全为 `'0'..'9'`）的片段；若全部非空段均为纯数字，则取最后一个非空段；写回 `*out.Resource`。

### 4.4 四重精准口径轻量视觉提示判据（`Observation.visual`）

在 Stage 1 完整公开树 `tree`（含尚未裁剪的 `image` 节点）与观测前台状态 `foreground.State` 上执行纯计算：

1. **前置条件**：`tree != nil && tree.Root != nil && tree.Root.Bounds != nil`，且屏幕面积 `screenArea = width * height > 0`；不满足时返回空。
2. **全页统计量**：
   - `totalTextRunes`：遍历全树非 `root`、非 `context` 节点，统计未被祖先 `Label` 覆盖（复用 4.3 第 4 条覆盖判据防双倍计数）的 `Label` 各段、非密码 `Value` 与 `Hint` 中的非空白 Unicode 码点数（`!unicode.IsSpace(r)`）；
   - `interactiveCount`：全树非 `root`、非 `context`、具有正面积裁屏矩形且 `len(node.Actions) > 0` 的节点数。
3. **分支一：`image_heavy(<pct>%)`**：
   - 收集满足 A3c 被裁口径（非 `context`、`Role == RoleImage`、无子节点、无动作、无 `Label`/`Value`/`Hint`/`Resource`/`Axis`、五布尔已知默认）的图片叶裁屏矩形 `r`：
     - 过滤碎片图：`area(r) * 100 < screenArea * 5` 跳过；
     - 剔除全屏底图：`area(r) * 100 >= screenArea * 90` 跳过；
     - 剔除背景承载图：统计全树其他非 `root`、非 `context` 且（`len(Label) > 0 || (Value != nil && *Value != "") || len(Actions) > 0`）的控件，若其正面积裁屏矩形面积严格小于 `area(r)` 且中心点落在 `r` 内（`r.Contains(cx, cy)`）的个数 `>= 3`，跳过；
   - 至多取按面积降序前 `64` 个合格前景大图矩形，以精确坐标离散化求矩形并集像素面积 `unionArea`；
   - 若 `unionArea * 100 >= screenArea * 45` 且 `totalTextRunes < 150`：令 `pct = int((unionArea*100 + screenArea/2) / screenArea)`（夹逼在 `[45, 100]`），命中种类 `kind = "image_heavy"`、值 `fmt.Sprintf("image_heavy(%d%%)", pct)`。
4. **分支二：`sparse_tree`**：
   - 未命中分支一时，若 `foreground.State != "none"`（排除尚未打开任何应用的空白桌面）且 `totalTextRunes < 20` 且 `interactiveCount < 5`，命中种类 `kind = "sparse_tree"`、值 `"sparse_tree"`。
5. **分支三：`delta` 去重门禁（`publishTreeAttempt`）**：
   - 本次观察以 `full` 发布（`delta == nil`）：原样输出本次计算的 `visual` 值（空则省略）；
   - 本次观察以 `delta` 发布（`delta != nil`）：取同会话同显示代次紧邻上一帧已发布观察 `e.current`（若不存在则取所选 `baseline` 对应观察）记录的原始 `visualKind`；仅当本次 `kind != ""` 且 `kind != prevVisualKind`（视觉异常状态由无变有或切换种类）时输出本次 `visual`，否则置空省略，不在同页 `delta` 连续刷屏。

---

## 5. 算法

### 5.1 `ProjectCompactTree` 单遍下行/上行伪代码

```text
func projectCompactNode(n *Node, screen compactProjection,
                        reference *Bounds, ancestors []ancestorLabel) *ReadNode:
    if n == nil:
        return nil
    out := copyReadFields(n)

    // 1. G5: 露边幽灵细条裁剪（仅原始无子、无文本、默认状态）
    originalHasChildren := hasNonNilChild(n)
    if !originalHasChildren && canPruneSliver(n, out, screen):
        return nil

    // 2. G3: input label == value 去重
    if out.Role == RoleInput && out.Value != nil &&
       len(out.Label) == 1 && out.Label[0] == *out.Value:
        out.Label = []string{}

    // 3. 几何判定（隐藏判定沿用原四元框；交付时 root/scroll 用 4 元框，其余用 2 元中心点）
    switch:
    case n.Bounds == nil:
        out.Bounds = UnknownBounds()
    case n.Role == RoleRoot || len(n.Actions) != 0:
        out.Bounds = compactDeliveredBounds(n.Role, *n.Bounds, screen)
        reference = deliveredRect(*n.Bounds, screen)
    case compactHidesBounds(n, out, screen, reference):
        out.Bounds = HiddenBounds()
    default:
        out.Bounds = compactDeliveredBounds(n.Role, *n.Bounds, screen)
        reference = deliveredRect(*n.Bounds, screen)

    // 4. 向下传递实际保留的祖先标签栈，递归投影子节点
    childAncestors := ancestors
    if originalHasChildren && len(out.Label) > 0:
        childAncestors = appendAncestorLabel(ancestors, out.Label)
    for _, child := range n.Children:
        if projected := projectCompactNode(child, screen, reference, childAncestors); projected != nil:
            out.Children = append(out.Children, projected)

    // 5. 上行归约链（严格顺序）
    // Step 5.1 (A3c): 全裁无标注非交互 image 叶
    if !originalHasChildren && compactImageCanPrune(out):
        return nil
    // Step 5.2 (A1a): 裁剪被祖先复合标签覆盖的纯被动文本叶
    if !originalHasChildren && isPlainPassiveTextLeaf(out) &&
       coveredByAncestorLabel(out.Label, ancestors):
        return nil
    // Step 5.3: 清理变空隐藏匿名容器 & 绕过单子隐藏匿名容器（len >= 2 原样保留！）
    if originalHasChildren && len(out.Children) == 0 &&
       out.Bounds.Kind == GeometryHidden && AnonymousPlain(out):
        return nil
    if out.Bounds.Kind == GeometryHidden && AnonymousPlain(out) && len(out.Children) == 1:
        return out.Children[0]
    // Step 5.4 (G2b): 无字可点击 view 吸收全部纯被动文本子叶升为 button
    if canHoistClickableTextChildren(out):
        hoisted := []string{}
        for _, ch := range out.Children:
            for _, s := range ch.Label:
                if s != "":
                    hoisted = append(hoisted, s)
        out.Role = RoleButton
        out.Label = hoisted
        out.Children = []*ReadNode{}
        out.Resource = nil
    // Step 5.5 (A5a): 剩余无字控件的 resource 截取语义短尾名
    if out.Resource != nil && *out.Resource != "":
        short := shortResourceTail(*out.Resource)
        out.Resource = &short
    return out
```

### 5.2 发布与边界行为时间轴

```text
device.Capture (不可变原值)
   │
   ├─► Builder.Candidate / CandidateAgainst (锁外, build.go)
   │      ├─ cleanReadableText 净化公开 Label (G1)
   │      └─ 产出完整 Snapshot (4元 Bounds, 完整子节点, 完整 ResourceID)
   │
   ├─► Engine.publicCandidate (锁外, capture.go)
   │      ├─ ProjectCompactTree 产出 compact ReadTree (净体 + 2元 Point / 4元 Rect)
   │      ├─ InferVisualHint(projection.Tree, cap.Foreground.State) 产出 (visualKind, visualRaw)
   │      └─ 校验 MaxFullTreeBytes (128 KiB) 与 MaxSnapshotCostBytes (4 MiB)
   │
   ├─► treeTextBaselineDelta (锁外, capture.go + treetext)
   │      └─ EncodeFull / EncodeDelta (tree v5) 精确计算 UTF-8 字节，筛选 <70% 最短 delta
   │
   └─► publishTreeAttempt 锁内提交门禁 (capture.go)
          ├─ 重验会话 active、工作授权、STOP、显示代次与 CaptureMaxAge
          ├─ 计算最终对外 Observation.Visual (full 直出；delta 仅状态变化时输出)
          └─ 持久化基线池与留存快照，返回 protocol.Observation
```

- **撤销 / 超时 / 采集失败**：STOP 或 `end` 先撤权，锁外候选直接丢弃，不更新 `Builder` 连续性与 `prevVisualKind`；无树失败观察（`status="failed"` 或仅含只读图的 `status="partial"`）永不设置 `Observation.Visual`。
- **预算耗尽**：公开树 `128 KiB` 预算仍在 `compact` JSON 上计量（净体与二元坐标只会显著减小 `compactBytes`，降低触发 `tree_too_large` 的概率）；内部快照 `4 MiB`/`32 MiB` 预算仍在 `raw_capture + full_tree` 上诚实计量。
- **同 `ref` 跨帧角色翻转**：若某可点击容器在基线帧因含纯文本叶被 G2b 升为 `button`，而在后续帧动态失去文本子叶退回 `view` 且仍保住同一 `ref`，`ReadDiff` 命中既有 `role changed` 判据返回 `ErrNotRepresentable`，`treeTextBaselineDelta` 自动跳过该候选并在无其他合格基线时安全发布 `full` 树，绝不产出非法 `delta`。

---

## 6. 实现方案

### 6.1 接口与文件

| 文件 | 改动 |
| --- | --- |
| `release/versions.json` 及 4 个生成文件 | `release_version` 升至 `"0.32.0"`，`contract_version` 升至 `28`，`android_version_code` 升至 `34`，运行 `scripts/generate-version.ps1` 同步 Go/Java/XML/Manifest |
| `internal/observation/build.go`、`match.go` | 新增 `cleanReadableText` 净化公开 `labelParts` 与输入框描述锚点，调整 `hasSemantics` 文本非空判定对齐净化后结果 |
| `internal/observation/visual.go`（新文件） | 实现纯函数 `InferVisualHint(tree *protocol.Tree, foregroundState string) (kind, value string)` 及有界精确矩形并集面积计算 |
| `internal/protocol/read_tree.go`、`decode_response_test.go`、`encode_test.go` | 新增 `Point`、`GeometryPoint`、`PointBounds(x, y)`；更新 `ReadBounds` 编解码（支持 2 整数 `[x,y]` 与 4 整数 `[l,t,r,b]`）及 `ReadTree.Validate()` 的 `compact`/`detailed` 视图几何约束 |
| `internal/protocol/responses.go`、`decode_response.go` | `Observation` 新增可选 `Visual string`（JSON `"visual"`，空串省略，解码拒绝显式 `null` 与空串） |
| `internal/observation/read.go`、`read_diff.go` | 实现 `ProjectCompactTree` 的 G5/G3/二元中心点/A3c/A1a/G2b/A5a 完整流水线；更新相关单测与基准用例 |
| `internal/engine/engine.go`、`capture.go` | `observationState` 记录原始 `VisualKind`；`treeAttempt` / `baselineDeltaCandidate` 携带候选视觉分类；`publishTreeAttempt` 与 `observationPublicLocked` 按 `full`/`delta` 规则填充 `Observation.Visual` |
| `internal/treetext/encode.go`、`encode_test.go` | 普通树块头升级为 `tree v5 full` / `tree v5 delta`；普通节点与 `~` 行支持 `GeometryPoint`（`x,y` 与 `bounds=[x,y]`）及同 `Kind` `@` 缩写 |
| `internal/mcp/result_text.go`、`tree_text.go`、`schema.go` | `v4ResultFields` 注册 `"observation.visual": v4Token`；`renderObservationV4` 在 `obs` 行输出 `visual=<token>` 并将 `"visual"` 加入 `switch member.name` 跳过列表（防触发 `/observation/visual` fallback 行）；更新 `treeTextGuide`、`callerGuidance` 与工具描述（`tree v5`、二元坐标、净体规则、`visual=` 说明） |
| `scripts/mcp-probe/result_text.go`、`result_v4.go` 及探针单测 | 独立解析器同步升级至 `tree v5`、二元/四元几何校验与 `obs` 行 `visual=` 语义核验 |
| `docs/interface.md`、`docs/mcp.md`、`docs/architecture.md`、`docs/release.md` | 同步契约 `28`、`Observation.visual`、`compact` 净体与二元几何策略、`tree v5` 文法及最新工具目录指纹 |

### 6.2 配置与取值

所有阈值为包内确定性编译期常量，不新增外部配置或工具请求参数（零公开入参膨胀）：

- 幽灵细条最小可见边阈值：`compactSliverMaxEdge = 4`（`<= 4px` 剪除，`>= 5px` 保留）；
- 视觉提示常量：`visualMinImagePercent = 5`（`< 5%` 碎图忽略）、`visualMaxBackdropPercent = 90`（`>= 90%` 全屏底图剔除）、`visualBackdropControlLimit = 3`（内部含 `>= 3` 个带字/可交互更小控件判定为背景图）、`visualMaxSampledRects = 64`、`visualHeavyUnionPercent = 45`（前景并集 `>= 45%`）、`visualHeavyMaxTextRunes = 150`（`< 150` 字）、`visualSparseMaxTextRunes = 20`（`< 20` 字）、`visualSparseMaxInteractive = 5`（`< 5` 个可交互控件）。

### 6.3 日志与可观测性

- 严格遵守日志白名单（AGENTS.md 硬约束第 10 条）：不将标签文本、裁剪后的 `resource` 名称、坐标或节点树写入 `eventlog`；
- 如需诊断视觉提示触发频率，仅可在调试日志中使用受控枚举 `"none"` / `"image_heavy"` / `"sparse_tree"`，绝不记录页面文本或包内私有内容。

### 6.4 与既有不变式的关系

| 不变式 | 影响 |
| --- | --- |
| 唯一权威与状态锁短小 | 不变：树净化、紧凑投影、视觉并集计算与 `tree v5` 编码全部在 `e.mu` 锁外完成 |
| 原始采集与重验真实性 | 不变：`device.Capture` 零修改；`act` 选点、重验与 `text` 读回均读取完整内部树与原 `RawNode` |
| 单向消费与提交条件化 | 不变：`visual` 去重仅在 `publishTreeAttempt` 最终提交通过时读取并更新当前观察状态 |
| 三态与省略区分 | 保持：`complete` 节点继续省略 `GeometryHidden`，`delta.set` 继续显式区分未修改、`bounds=hidden`、`bounds=null`、`bounds=[x,y]` 与 `bounds=[l,t,r,b]` |
| `inspect` 详细兜底契约 | 不变：`inspect`（`view=detailed`）继续从完整 `Snapshot.Tree` 投影全部原始公开节点、完整 `resource` 与四元组 `[l,t,r,b]` |

### 6.5 契约与文档影响

- 共同 `contract_version` 升级为 `28`（HTTP 请求头 `X-Mobile-Use-Contract: 28` 与控制端同步协议同步）；
- `docs/interface.md` 更新接入契约号、`Observation.visual` 定义、`ReadNode.bounds` 在 `compact` 与 `detailed` 下的二元/四元规则及 `compact` 五项净体策略；
- `docs/mcp.md` 更新工具目录 SHA-256 指纹、`obs` 行 `visual=` 语法与 `tree v5` 完整块/差分块文法；
- `docs/architecture.md` 与 `docs/release.md` 同步 `compact` 投影与契约/指纹说明。

---

## 7. 预期效果与能力边界

### 7.1 能治什么

1. **大幅压缩观察树体积与累计上下文**：在不拍平多子匿名容器的前提下，全裁无标注装饰图（`-6.17%` 树）、删祖先复合复读子叶（`-3.16%` 树）、提升套壳按钮（`-2.05%` 树并消除冗余 `tap`/`resource`）、`resource` 短尾名（`-3.45%` 树）、零宽净化与非 `root`/`scroll` 二元中心坐标（`-14.43%` 整树）叠加后，预计单次观察树 token 下降 **~30%–32%**，套壳按钮直接收敛为单行可读 `button`。
2. **消除隐藏字符导致的查询与分词退化**：闲鱼、知乎等带 `\u200b` / `\uFFFC` 的商品与帖子卡片恢复正常中文分词，`wait_for(query=...)` 与 `inspect(query=...)` 可直接按正常人类字面词命中。
3. **补齐纯视觉页与极稀树盲区感知**：全裁装饰图后，遇到主题画廊、封面预览、视频全屏隐藏控件或自绘 Canvas 页面时，`obs` 行以 ~6 token 的轻量 `visual=image_heavy(...)` 或 `visual=sparse_tree` 明确提示模型按需调用 `snap`。

### 7.2 不能治什么（必须写）

| 现象 | 原因 |
| --- | --- |
| 多层嵌套多分支匿名容器造成的缩进深度（如 8–10 层多子 `FrameLayout`/`LinearLayout`） | 本次为保护页面分组拓扑不被扭曲，刻意保留了 `len >= 2` 的隐藏匿名容器，不执行多子扁平化 |
| 单节点 300–400+ 字超长商品详情或长评论标签的长尾占用 | 本次刻意不引入标签预览截断，确保模型无需额外往返 `inspect` 即可读全节点正文 |
| 模型跳过 `ref` 手工抄取大卡片中心 `x,y` 发起坐标点击，恰遇卡片正中心有内层子按钮 | `compact` 中非 `root`/`scroll` 仅展示中心点 `x,y` 不含四边宽高；正常使用 `target={ref:"n..."}` 会由服务端 `tapScope` 自动避开子按钮，手工算偏移手势则需先 `inspect` 取四元组 |
| 200+ 步超长任务在宿主侧不折叠旧历史时的线性累积 | 服务端只控制单次返回体积，历史观察树滑动折叠属于宿主上下文管理职责 |

### 7.3 成本

- **设备与网络 I/O**：零新增 ADB / u2 RPC / 截图读取调用。
- **服务端 CPU 与内存**：均为单遍纯内存线性扫描（矩形并集最多对 64 个前景大图做离散化，单帧耗时 `< 0.2ms`），全部在 `engine` 状态锁外执行；网络传输与 JSON/文本字节较基线减少约 30%。

---

## 8. 验证计划

### 8.1 单元测试

| 用例 | 期望 |
| --- | --- |
| **反向对照 1（G1 零宽与占位符净化）**：构造含 `个\u200b人\u200b闲\u200b置\u200b` 与纯 `"\uFFFC\uFFFC"` 节点的 `Capture`，运行 `Builder.Build` 与 `wait_for(query="个人闲置")` | 净化后纯 `U+FFFC` 节点不进入公开树，`wait_for` 立即命中 `"个人闲置"`，且 `Capture.Nodes` 原串未变；去掉 G1 实现则 `wait_for` 失配且多出空义节点 |
| **反向对照 2（A3c 全裁装饰图 + 保留有义图片）**：构造占屏 `25%` 与 `100%` 的无标注无动作默认 `image` 叶，以及带 `tap`、带 `label`、`selected=true` 的三类 `image` | `ProjectCompactTree` 裁掉前两者（连带清理因此变空的匿名父容器）并保留后三者，`ProjectDetailedTree` 全部保留；去掉 A3c 则 `>=1%` 无字图仍残留 |
| **A1a 复合标签覆盖与词边界守卫**：父 `button` 标签为 `"创作者小助手, 16:48 发布, 68"`，测试子纯文本叶 `"创作者小助手"`、`"68"`（覆盖应裁）与独立子叶 `"48"`、`"8"`、或 `selected=true` 子叶（不应误裁） | 前两者被精准剪除，`"48"`/`"8"` 因词边界守卫保留，`selected=true` 子叶保留 |
| **G2b 图文套壳提升与多文本切片**：`view(tap) > FrameLayout(匿名) > [image(无字装饰图), text("歌手")]` 与 `view(tap) > [text("¥99"), text("包邮")]`，以及含子 `button` 的卡片和多子匿名容器 | 前两者分别提升为 `button "歌手"` 与 `button "¥99" "包邮"` 且清除冗余 `resource`；含交互子节点的卡片与 `len >= 2` 的隐藏匿名容器均不误合并、不拍平 |
| **A5a/G3/G5 边界单测**：`Answer:Card:content_card:5:negativeFeedback` 与末尾纯数字 `...:content_card:5`、`input` 的 `label==value`、`height=2px` 露边可点 `view(tap)` 细条 vs `height=5px` 正常 `view(tap)` vs `height=2px` 的 `image(tap)`/`input` | 短尾名分别得 `"negativeFeedback"` 与 `"content_card"`；`input` 省去重复 `label`；`2px` 无字 `view(tap)` 细条被裁，而 `5px` 正常条及 `2px` 的 `image(tap)`/`input` 均保留 |
| **二元中心点、`ReadDiff` 与 `tree v5` 往返**：覆盖 `root`/`scroll`（4 元组）与 `button`/`input`/`view`（2 元组）、`1px` 宽半开边界、`@` 同 `Kind` 缩写、`~ ref bounds=[x,y]` 差分应用与 `inspect v3` 四元组不变性 | `ReadDiff` + `ReadApply` 100% 重建 `ReadTree`；`EncodeFull`/`EncodeDelta` 产出合法 `tree v5` 且被 `mcp-probe` 独立解析器无损核验通过；`detailed` 混入 `GeometryPoint` 被 `Validate()` 拒绝 |
| **视觉提示四重口径与 `delta` 去重**：分别构造（a）95% 全屏底图 + 60 个文字控件页、（b）48% 双列封面 + 300 字信息流、（c）52% 前景画廊大图 + 50 字主题页、（d）`image=0`、10 字、2 按钮的视频播放页、（e）`foreground=none` 空白虚屏，以及连续两帧画廊 `full -> delta` | （a）（b）（e）均输出空 `visual`；（c）首帧输出 `visual=image_heavy(52%)`、紧随同态 `delta` 省略；（d）输出 `visual=sparse_tree`；`result v4` 的 `obs` 行与 `structuredContent` 严格一致 |

### 8.2 实机/系统级（发布交付默认范围）

由主代理亲自执行全量测试、发布构建、实机整组部署与双入口独立探针验证（不委派 subagent 代跑）：

1. **全量测试与构建核验**：运行全仓 Go 单元测试（`internal/engine` 单次超时设为 15 分钟，收敛后集中执行）、Android 控制 App 构建及 `scripts/build-release.ps1`，用 `scripts/verify-manifest.ps1` 核验 `build/release/manifest.json`（确认 `release_version=0.32.0`、`contract_version=28`、`android_version_code=34` 及最新工具目录 SHA-256 指纹）。
2. **交付一（手机整组覆盖部署）**：执行 `scripts/deploy.ps1` 将新服务、手机 chroot MCP 适配器与控制 APK 整组覆盖部署至实机（保留 App 私有数据与配对状态），确认 `/health` 报告 `contract_version="28"`、`release_version="0.32.0"`。
3. **交付二（最小实机闭环验证）**：在实机虚拟屏会话上驱动真实页面完成 `start` → `app(open)` → `act(tap/scroll/text)` → `observe`（含触发 `delta`）→ `inspect(subtree)` → `end` 闭环，实测核验：
   - 普通观察返回 `tree v5 full` / `tree v5 delta`，`root`/`scroll` 为四元组、其余控件为二元中心点 `x,y`；
   - 套壳按钮合并、装饰图裁剪、`resource` 短尾名生效，且基于新 `O` 的 `act(tap/scroll)` 正常 `executed`；
   - `inspect` 仍返回 `inspect v3`、`view=detailed` 与完整 `[l,t,r,b]` 四元矩形。
4. **交付三（Windows 主机产物替换与双入口独立探针）**：从同一源码提交运行 `scripts/build-host-tools.ps1` 替换主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe`，核验主机侧清单哈希后，用新 `build/mcp-probe.exe` 分别对**主机独立 stdio MCP 进程**与**手机 Streamable HTTP MCP 入口**执行完整探针验证（含 `server/discover` 说明逐字节比对、工具目录指纹、`result v4` + `tree v5` 三模式一致性）。
5. **依赖方责任边界**：外部依赖的 Agent 宿主、其托管的常驻 MCP 进程重启与重连默认由用户自行处理；独立探针仅证明本次磁盘替换产物与双入口契约正确，验证记录严格区分磁盘替换、独立进程验证与依赖方切换。

### 8.3 记录

归档时写入 `maintenance/verification/compact-tree-purification-and-point-bounds.md`，如实记录实现提交、构建清单、全量单测结果、实机部署与闭环命令输出、主机双 exe 替换哈希及 stdio/HTTP 双入口探针真实结果，严格区分已实现、已单测与已实测。

---

## 9. 已定与未决

### 9.1 已定

| 事项 | 结论 |
| --- | --- |
| 优化分层边界 | Stage 1 仅做公开可读标签隐藏字符净化（不动 `Capture` 原值与 `input.value`）；其余五项树结构净体与二元中心点全部落在 Stage 2 `compact` 投影，`inspect` 详细视图保持全节点与四元组 |
| 多子匿名容器处理 | **暂不拍平** `len >= 2` 的隐藏匿名容器，维持现有仅清理变空容器与绕过单子隐藏匿名容器的规则，保护树分组层级 |
| 全裁装饰图与视觉提示 | 全裁无标注非交互 `image` 叶（A3c），并同步实现四重精准口径 `Observation.visual`（`image_heavy(<pct>%)` / `sparse_tree`，`delta` 同态去重） |
| G2b 多文本子叶提升形式 | 保留 `ReadNode.Label` 原生多段 `[]string` 切片（`tree v5` 输出多个引号字符串），不拼接人工分隔符；提升为 `button` 后清除原本因无字才挂上的 `resource` |
| 非 `root`/`scroll` 坐标形式 | `compact` `ReadBounds` 新增 `GeometryPoint`（JSON `[x,y]`，`tree v5` 普通行 `x,y`、`~` 行 `bounds=[x,y]`），`root` 与 `scroll` 保持四元矩形；不做 `3px` 模糊吸附 |
| 超长文本截断 | 本次**不纳入**超长标签预览截断 |
| 版本与块头演进 | 普通观察树块头升至 `tree v5`（`inspect v3` 与 `result v4` 头号保持不变），共同 `contract_version` 自 `27` 升至 `28` |

### 9.2 未决

无未决项。

---

## 10. 实施顺序

1. **协议与版本源（步骤 1）**：更新 `release/versions.json` 并运行 `scripts/generate-version.ps1`；在 `internal/protocol` 中扩展 `GeometryPoint`/`PointBounds`、`ReadTree.Validate()` 与 `Observation.Visual` 编解码，补齐 `internal/protocol` 单元测试。
2. **源净化、紧凑净体与视觉提示纯算法（步骤 2，依赖步骤 1）**：在 `internal/observation` 中落地 `build.go` 标签隐藏字符净化、`read.go` 五项净体与二元中心点投影、`visual.go` 四重口径视觉提示算法，完成 `internal/observation` 单元测试与反向对照用例。
3. **树文本 `tree v5` 编码器（步骤 3，可与步骤 2 并行，依赖步骤 1）**：升级 `internal/treetext` 支持 `tree v5` 头、二元点 `x,y`、`bounds=[x,y]` 与同 `Kind` `@` 缩写，补齐 `internal/treetext` 测试。
4. **引擎接线、MCP 投影说明与探针同步（步骤 4，依赖步骤 2、3）**：在 `internal/engine/capture.go` 接入 `Visual` 计算与 `delta` 去重；在 `internal/mcp` 注册 `observation.visual`、渲染 `obs` 行 `visual=` 并更新 `schema.go` 说明与工具描述；同步升级 `scripts/mcp-probe` 的 `tree v5` 与 `visual=` 独立校验器。
5. **正式文档同步、全量测试与独立文档审计（步骤 5，依赖步骤 4）**：更新 `docs/interface.md`、`docs/mcp.md`、`docs/architecture.md`、`docs/release.md`；主代理集中执行全仓 Go 单元测试与探针测试；发起独立只读文档审计 subagent 审核全部变更的 `docs/` 文件并收敛至 clean。
6. **发布构建、实机整组部署与双入口验证（步骤 6，依赖步骤 5）**：构建发布包并核验 `manifest.json` → 手机整组覆盖部署与最小实机闭环验证 → 替换主机 `build/device-mcp.exe` 与 `build/mcp-probe.exe` 并跑通主机 stdio 与手机 HTTP 双入口探针 → 提交实现（含 `docs/`）→ 归档本提案并提交验证记录。

---

## 11. 实现结果（归档时补写）

实现提交：`<hash>`。实现与验证记录：`../../verification/compact-tree-purification-and-point-bounds.md`
（本节在归档移动之后补写，届时文件在 `implemented/` 下，故链接比新建时多一层）。

### 11.1 已落地

| 提案条目 | 落点 |
| --- | --- |

### 11.2 与提案不同或未执行

| 项 | 结论与理由 |
| --- | --- |

---

## 附：本提案不改变的事项

- 不改变 `device.Capture` 原始节点数据、`Snapshot.Tree` 的四元 `Bounds` 与 `Source` 1:1 映射。
- 不改变 `act` 的 `tap`/`long_press`/`text`/`scroll`/`swipe`/`key` 静态规划、安全点选点、Tier 1–4 实时重验及 5 个 `422` 静态错误码与 `candidates[*].bounds` 四元数组。
- 不改变 `wait_for` 的 `stable=true` 内容稳定摘要规则与采样时间参数。
- 不改变 `inspect`（`inspect v3`、`view=detailed`）的完整节点保留、两格缩进、显式动作与 `[l,t,r,b]` 四元矩形输出。
- 不拍平 `len >= 2` 的隐藏匿名容器，不截断超长标签文本，不自动静默附加截图。
- 不改变多基线池容量（1 当前 + 3 备用）、窗口跨度与 `<70%` 差分字节门槛。
