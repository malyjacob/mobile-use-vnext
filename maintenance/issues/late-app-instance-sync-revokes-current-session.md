# Issue：迟到的旧 App 实例同步可撤销新实例会话

状态：已在 Go 模拟中复现，未修复；独立于 UI 输入结束证据提案。
代码基线：`ae1fbbc35f32e0dbeb2eda7a519e825f936601e9`；被指出的控制同步分支不属于该提案的实现改动。

## 现象与先红复现

同一个服务进程依次接收 A、B 实例同步，B 随后建立会话；一份迟到的 A 同步被接受为新的
实例切换，导致绑定 B 的会话被 `app_restarted` 撤销。这里的 A/B 均是测试生成的实例，
没有连接真实设备或改变生产 App 状态。

用例：[late-app-instance-sync-repro.go.txt](../verification/fixtures/late-app-instance-sync-repro.go.txt)，
通过 Go overlay 映射为 `internal/engine/zz_issue_late_app_instance_test.go` 后运行：

```powershell
$env:GOEXPERIMENT = 'jsonv2'
$env:CGO_ENABLED = '0'
go test -overlay .state/late-app-instance-overlay.json ./internal/engine -run '^TestIssueLateAppInstanceCannotRevokeCurrentSession$' -count=1 -v -timeout 20s
```

实际失败断言：`late retired instance replaced current App facts and revoked the new session`。
该诊断 fixture 不进入普通测试集合，不把测试替身的会话撤销写成实机跨屏误操作的结论。

## 归因与边界

`internal/engine/control.go:71–75` 对任意不同的 `app_instance_id` 调用实例切换；
`onAppInstanceChangedLocked` 在旧会话 App ID 不匹配时撤销会话。当前载体没有实例序号或
已退役实例屏障，无法在该分支识别“以前出现过、现在已经退役”的实例。

分诊为控制同步的实例顺序缺陷。与迟到设备结束回执的 token 校验不同，它需要明确实例
切换、退役保留及服务重启时的边界；应按维护流程独立取证并选择修复路线。
UI 执行提案保留原实例/代次的提交校验，没有修改控制实例切换判据，本次不混入该协议变更。
