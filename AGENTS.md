# mobile-use-vnext — 工程约束

本仓库是 mobile-use 的全新实现（Go 主服务 + Java 控制 App + 固定 uiautomator 制品）。
本仓库独立维护设计与实现，正式架构与现行契约以本仓库的 [正式文档](docs/) 为准；
未决设计、提案、决策与验证记录按 [工程流程](maintenance/process.md) 管理。
实现与现行规范发生冲突时，在本仓库内分诊、确认变更并同步实现与文档，不在实现里暗改语义。
不把其他项目仓库作为设计权威或实施前置，也不要求读取、修改、提交或推送其他项目仓库。
构建使用本仓库源码、已声明的版本依赖和固定制品；设计、实现、验证与发布均不要求旧项目的
源码目录、分支、代码或规范存在，也不操作旧项目。

## 硬约束

1. **唯一权威**：会话、租约、O/P 接纳与关闭、输入授权、工作归属只由 `internal/engine`
   持有。HTTP、MCP、App 与客户端不保存第二份执行权威，不各自访问 ADB 或设备。
2. **STOP 不等待**：用户停止/`end` 先撤权、封闭派发、取消原工作并触发独立收尾，不等待
   设备工作、日志/账本刷盘或网络往返。已派发的 Android 命令不保证撤回，不得伪称撤回。
3. **单向消费**：`O` 只能 `ready → claimed(P) → retired` 或 `ready → retired`。认领后
   不归还；静态拒绝不认领；同一 P 不派发第二次；同一会话最多一个 ready O。
4. **提交条件化**：采集/构建候选不直接改公共状态；最终提交前重验原会话、显示代次、
   工作拥有者、STOP/终态与期限。STOP 后的迟到候选不得发布。
5. **状态锁短小**：锁内只做有限内存检查/转换，不等待 ADB、RPC、磁盘、网络或树算法。
   设备同时只有一项当前普通工作；冲突立即 busy，不建普通动作队列。会话撤权后，旧 work、
   UI 与 shell 的最长逻辑交接为 5 秒，到期冻结并脱离当前位置；已派发 Android 命令不保证
   随之停止，旧物理事实不成为后续工作的第二份执行权威。
6. **严格接入**：`/api/<tool>` 拒绝未知字段、重复键、大小写变体、尾随第二个 JSON 值、
   类型混用与未声明 nullable 的显式 `null`。必填字段必须实际出现。
7. **三态与省略**：完整 `Node` 与 `delta.set` 的省略语义不同。`set` 必须保留显式
   `false`/`0`/`""`/`[]`/`null`；不得用 `omitempty` 混淆未修改、置 null 与赋值。
8. **事实分层**：执行/观察/会话终态/物理收尾分别表达，不能相互替代。主动作无完整证据
   为 `uncertain`，不因后观察成功改判；后观察失败不把已确认 `executed` 改成未执行。
9. **恢复屏障**：进程取得固定运行目录内核锁后才能接触账本/App/设备。新会话准入只检查当前
   账本能否载入并保存新归属，以及当前 App/模式/STOP、设备与交互条件；端口启动不是干净证明。
   旧会话撤权后转退休记录，旧 cleanup、执行/任务/recents/display、legacy UI、补偿停止或
   记录容量不构成永久准入封锁；未确认事实如实保留，旧逻辑交接最多 5 秒。
10. **日志白名单**：只记批准的事件、ID、枚举、布尔与数值。禁止树/差异/输入正文/命令/
    消息正文/截图/凭据/原始响应/异常字符串。MCP stdout 只承载协议，诊断走 stderr。
11. **文档边界**：`docs/` 只写已实现架构、现行契约、运行说明与工程规范；
    `maintenance/` 承载提案/流程规范/决策/验证记录/未决项；原始截图、节点树、日志与个人
    信息不入库。
12. **验证边界**：只验证本次真实改变的路径。已实现/已实测/已验证分别表达；不在文档里
    把设计推演写成验证结论。
13. **单一版本源**：发行号、共同 `contract_version` 与 Android versionCode 只在
    `release/versions.json` 手改；Go/Java/XML 常量由脚本生成。发布按
    `build/release/manifest.json` 核验产物、契约与工具目录指纹；不得跳过清单、手改多处
    版本号或用卸载/清库解决签名或配对。
14. **消息边界**：`notify` 只保存并入队；正文只经控制同步与 App 私有存储传递，不写日志、
    错误、恢复账本或发布清单。App 本地已读不上报，不新增 Agent 已读通道。
15. **文档审计门禁**：最终提交/推送前，只要变更涉及 `docs/`（新增、修改、删除、移动，
    或其中的契约/版本/链接/命令表述变化），必须由一个**独立、只读**的文档审计 subagent
    审核本次涉及的 `docs/` 文件，审计者不得修改文件。审计口径：
    - `docs/` 只写正式架构、现行契约、运维说明与工程规范；
    - 进度、验证记录、工程决策、提案、未决项、TODO 一律放在 `maintenance/`；
    - 禁止 `已实现`/`未实现`/`未验证`/`本轮`/`P3`/`P4`/`M5`/`进行中`/`待补`/`下一步`
      等过程性措辞（阶段代号只允许出现在 `maintenance/`）；
    - 同时检查常见文档冲突与漂移：同一端口/路径/字段/错误码/契约版本前后矛盾，
      `docs/` 与代码、`release/versions.json`、manifest、脚本不一致，过时示例、
      失效链接、重复或互相矛盾的规范；
    - 审计返回 `file:line` 与最小修正；发现项修正并复检（或取得 clean 结论）后才能提交，
      提交说明注明审计结论。审计 subagent 不承担实现，不得直接覆盖文档。
16. **变更流程**：先分诊（缺陷 / 取舍 / 规范），再按 `maintenance/process.md` 推进，
    提案格式见 `maintenance/proposals/template.md`。缺陷必须由**先红的复现用例**证明，
    并归因到 `file:line` 与同类核查；根因落在现行规范上的缺陷转为提案（取舍），不得在
    实现里暗改语义。取舍类先取证、给出至少两条路线。**只有“讨论”和“提案”可以跳过**
    （条件见流程文件），且必须在提交信息里声明跳过理由；归因、执行、测试与验证记录不可
    跳过。有提案时：确认前状态只能是“提案，未实现”，实现提交落盘前不改提案状态，
    **先提交实现、再提交归档**，归档头写实现提交 hash 并补 §11（已落地 / 与提案不同或
    未执行，后者必须写理由），移入 `maintenance/proposals/implemented/` 并重写相对链接；
    部分实现要标明缺口。无提案的修复写 `maintenance/verification/`。规范本身的变更直接
    改本节与流程文件，并记入 `maintenance/decisions.md`。
17. **审计等待**：最终文档审查 subagent 运行期间，不得把“尚未返回”当成“卡住了”而反复
    中断或重启。正确做法是等待它给出结论；只有明确失败、无响应且记录了原因时才重新发起，
    并在提交说明中注明。审计期间可以并行处理与审计文件无关的工作，但不得改动正在审计的
    `docs/` 文件。

## 目录

```text
cmd/device-service/     设备服务进程组装、启动、退出
cmd/device-mcp/         MCP 双入口进程（stdio / Streamable HTTP）
internal/protocol/      公共数据、严格 JSON 接入、错误码与可读排版
internal/device/        原始设备事实类型（不执行 I/O、不持运行状态）
internal/observation/   公开树构造、保守保号、full/delta 计算与还原
internal/imaging/       图片等比缩小与单次 JPEG 编码（纯算法）
internal/engine/        唯一业务入口：会话、O/P、工作位置、STOP、收尾、恢复、消息队列
internal/android/       ADB、u2 RPC、制品管理与受管执行链
internal/httpapi/       认证 HTTP 接入、/api/<tool>、控制端同步与最小 STOP
internal/client/        强类型 HTTP 客户端（发送前 P、结构化结果、图片字节）
internal/mcp/           12 工具注册/schema/投影、取消包装、HTTP 认证与 Origin
internal/store/         私有恢复账本与有界消息文件（原子文件、单写者）
internal/eventlog/      有界事件/字段白名单日志
android-control/        Java 控制 App 源码与构建入口
third_party/uiautomator/固定 JAR 输入/补丁/哈希/来源/许可证
release/                发行号、契约号与 Android versionCode 的唯一版本源
maintenance/            公开维护材料
docs/                   正式文档
```

## 构建

Go 工具链锁定 1.27 系列并启用 `GOEXPERIMENT=jsonv2`。常规构建目标为 linux/arm64、
`CGO_ENABLED=0`。Windows 只承担构建与开发检查，不提供运行账本的耐久证据。

```text
GOEXPERIMENT=jsonv2 GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

Go 服务、Java App 与固定 JAR 独立构建，发布包由 `scripts/build-release.ps1` 组装；
`build/release/manifest.json` 记录源码提交、共同 `contract_version`、发行号、每个产物的
SHA-256/大小、Android 版本与工具目录指纹。默认部署安装并启动手机 MCP 适配器，主机自建
适配器仍可选。部署先用 `scripts/verify-manifest.ps1` 核验，再覆盖安装并保留 App 私有数据；
MCP 宿主必须重启实际进程以重新发现工具。

## 提交与验证记录

每次变更同步更新其源码已经具备的正式文档内容；部分实现只写已实现部分，其余留在
`maintenance/`。涉及 `docs/` 的变更必须先通过第 15 条文档审计门禁。验证记录写入
`maintenance/verification/`，注明代码提交、构建方式、设备/环境、实际执行命令与真实结果。
