package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// compatibilityProtocolVersion is the newest handshake-era protocol version.
// A client pinned to it takes the legacy initialize path instead of
// server/discover, so the same server instructions must appear there too.
const compatibilityProtocolVersion = "2025-11-25"

// baselineDiscoveryBytes caps the instructions plus the complete tools/list
// JSON. Contract 35 also caps the tool array independently.
const baselineDiscoveryBytes = 31000
const toolsListByteBudget = 20500

// TestTreeTextGuideStatesTriStateOmission pins the shared tree legend to the
// complete-node defaults and the different set semantics.
func TestTreeTextGuideStatesTriStateOmission(t *testing.T) {
	for _, required := range []string{
		"enabled=true` 省略",
		"enabled=false`/`enabled=null` 显式",
		"focused`/`selected`/`protected`/`checked` 的已知 `false` 省略",
		"`set` 行不同",
		"出现的状态字段一律保留显式 `false`/`true`/`null`",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
	if strings.Contains(treeTextGuide, "defaults {\"checked\":false}") {
		t.Error("tree guide retains the removed defaults declaration")
	}
}

// TestTreeTextGuideStatesCompactGeometryPolicy pins the v5 geometry legend: the
// container rectangle, the ordinary center point and the same-form @ shorthand.
func TestTreeTextGuideStatesCompactGeometryPolicy(t *testing.T) {
	for _, required := range []string{
		"每层恰写一个 ASCII 空格",
		"`root`/`scroll` 交付矩形 `l,t,r,b`",
		"其余交付中心点 `x,y`",
		"`right>=left`",
		"`bottom>=top`",
		"未知写 `-`",
		"缺失列表示隐藏",
		"`@` 表示与同一定义中最近已交付几何的严格祖先同形同值",
		"每个 `+`、`=` 子树独立判断",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
}

// TestTreeTextGuideStatesCompactPruningAndLegacyChains separates the new
// ordinary compact output from the historical chain syntax that readers must
// continue to accept.
func TestTreeTextGuideStatesCompactPruningAndLegacyChains(t *testing.T) {
	for _, required := range []string{
		"全裁无标注非交互 `image` 叶",
		"裁剪被祖先复合标签覆盖的纯被动文本叶",
		"纯被动文本子叶的无字可点击 `view` 升为 `button`",
		"`label==value` 时省略 `label`",
		"`<=4px`",
		"最后的非纯数字短段",
		"所有隐藏匿名容器（无文本/动作/`resource`、状态全默认、至少 1 子）按序扁平化透传",
		"原始无子合成叶例外",
		"变空的匿名容器清理",
		"公开 `label` 首尾空白净化（含 NBSP/纯空白归空）",
		"有动作非 `input` 节点单段 `label` 超 `250` 码点截断并置 `truncated=true`",
		"被动文本/`input.Value` 保持全文，`inspect` 补看全文",
		"无动作 `text` 与匿名容器隐藏几何，详细补看交付矩形",
		"新 v5 只生成普通节点定义",
		"读取端继续接受历史 `[n2 n3]`",
		"链下缩进的孩子属于链尾",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
	if strings.Contains(treeTextGuide, "节点未出现在普通树，不代表详细快照中不存在") {
		t.Error("tree guide repeats the removed compact omission explanation")
	}
}

// TestTreeTextGuideStatesTruncationContract pins the contract-29 truncation
// column: the complete-node grammar places it after resource and before context,
// the set grammar keeps both assignments explicit, and the compact net-body
// legend names the actionable 250-code-point cut while passive text and input
// values keep their full text.
func TestTreeTextGuideStatesTruncationContract(t *testing.T) {
	for _, required := range []string{
		"axis=… resource=… [truncated=true] context",
		"`truncated=true` 只在 compact 有动作非 `input` 节点截断时出现，`false` 省略、detailed 禁止",
		"`truncated=true|false` 同样显式保留，省略表示未修改",
		"有动作非 `input` 节点单段 `label` 超 `250` 码点截断并置 `truncated=true`",
		"被动文本/`input.Value` 保持全文，`inspect` 补看全文",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
	for _, retired := range []string{"`len>=2` 保持层级", "清理变空/单子隐藏匿名容器"} {
		if strings.Contains(treeTextGuide, retired) {
			t.Errorf("tree guide retains retired compact wording %q", retired)
		}
	}
	// The public legend states the caller-facing contract only; the internal
	// baseline pool parameters stay out of discovery.
	for _, internal := range []string{"12, 10, 5", "(12,10,5)", "80%"} {
		if strings.Contains(treeTextGuide, internal) {
			t.Errorf("tree guide leaks internal baseline pool parameter %q", internal)
		}
	}
}

func TestTreeTextGuideStatesResultV4AndTreeV5(t *testing.T) {
	for _, required := range []string{
		"`result v4 <ok|err> [op=…] [state=…] [outcome=…] [phase=…]`",
		"常规成功（`ok` 且无 `state=`）省略 `outcome=executed`",
		"非 `executed`、错误结果与带 `state=` 的重放摘要一律显式输出",
		"`text`/`both` 是面向调用的紧凑投影",
		"`structured` 保留完整审计字段",
		"`obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [visual=<token>] [change=<token>] [hint=<token>] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=…]`",
		"`obs ` 后首个不含 `=` 的字段是完整 O",
		"`status=complete` 省略",
		"`fg` 写 `<包名>#<task_id>`、`<包名>`、`#<task_id>`、`present`、`none` 或 `unknown(<reason>)`",
		"`visual` 为视觉提示",
		"`loading`/`image_heavy(<pct>%)`（45–100）/`media_surface`/`sparse_tree`",
		"`loading` 与 `wait_for` 终帧的 `sparse_tree` 在 `delta` 也保留，其余同态 `delta` 省略",
		"`change` 仅见于 `act` 常规成功且比较前后树",
		"`unchanged` 前后紧凑树与完整树均相同",
		"`detail_only` 紧凑树相同但完整树细节有变",
		"`matched_base` 前后树内容或公开语境有变且零差异命中历史基线",
		"`hint` 只由已发布字段推导",
		"`loading` 提示 `hint=wait_for`",
		"`sparse_tree` 在非 `wait_for` 终帧且 `change` 非 `unchanged`/`detail_only` 时也提示等待，即使带图",
		"`wait_for` 终帧仍稀疏，或稀疏且 `change=unchanged`/`detail_only`，无有效图时转 `hint=snap`",
		"`hint=snap` 用同一 O 截图核对",
		"`hint=wait_for` 用 wait_for 等条件或稳定",
		"`obs.image` 与顶层 `image` 同字段同顺序",
		"`screen` 仅在带 `session` 或无树帧输出",
		"`rot` 在非零旋转时始终输出",
		"所有 `HH:MM:SS` 均为 UTC 秒级时间",
		"`origin`/`revision`/`actionable_until` 只在 `structured` JSON 中保留",
		"单行 `image [<W>x<H>] [read_only] [media=<非 jpeg>]",
		"`app_items v1 kind=find|list count=<n>`",
		"`app_tasks v1 count=<n>`",
		"`session <state> mode=… origin=… [started=…] [expires=…] [ended=…] [reason=…]`",
		"`kind=list` 行写 `#<task_id> <package> <ownership>[ origin_display=<显示号>]`",
		"`recovery entry=<entry> ref=<精确 O/P 编号|O|P|none> [input=…] [cause=…]`",
		"`json v1 path=\"<绝对 JSON Pointer>\" value=<原值>`",
		"tree v5 full nodes=<n>",
		"tree v5 delta base=<O|_<序号>> changes=<n>",
		"inspect v3 scope=",
		"`base=_72`",
		"每层恰写一个 ASCII 空格",
		"`@` 表示",
		"`!tap`、`!text` 或 `!swipe`",
		"按 `tap,long_press,text,swipe` 插回默认动作",
		"inspect v3 一律使用两格缩进、`[l,t,r,b]` 矩形和显式动作词",
		"省略 `role`",
		"首项为引号标签时默认 `text`",
		"其他列或行尾默认 `view`",
		"显式角色覆盖默认",
		"首项为标签或角色词的 `view`",
		"无标签或带几何列的 `text`",
		"`[n2 n3]`",
		"`chain [n2 n3] <role> ...`",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
	for _, retired := range []string{
		"result v2",
		"result v3",
		"tree v4",
		"tree v3",
		"tree v2",
		"inspect v2",
		"连续 `observation.*` 字段",
		"inspection.origin",
		"observation.screen.display_id",
	} {
		if strings.Contains(treeTextGuide, retired) {
			t.Errorf("tree guide retains retired wording %q", retired)
		}
	}
	if strings.Contains(treeTextGuide, "\nobservation\n") {
		t.Error("tree guide retains the indented observation group")
	}
}

// TestDiscoveryDropsResultV3AndObservationScreenPath is the anti-regression
// gate for the two retired spellings: the compact envelope is result v4, and
// the session display id is reachable from the obs/image/app lines instead of
// the removed observation.screen.display_id path.
func TestDiscoveryDropsResultV3AndObservationScreenPath(t *testing.T) {
	if strings.Contains(ServerInstructions(), "result v3") {
		t.Error("server instructions still publish result v3")
	}
	if strings.Contains(ServerInstructions(), "observation.screen.display_id") {
		t.Error("server instructions still publish observation.screen.display_id")
	}
	for _, definition := range toolCatalog {
		for _, retired := range []string{"result v3", "observation.screen.display_id", "actionable_until"} {
			if strings.Contains(definition.description, retired) {
				t.Errorf("%s description retains retired wording %q", definition.name, retired)
			}
		}
	}
}

// TestDiscoveryTreeExamplesDecodeAndApply proves the full, delta and inspect
// frames embedded in the tree guide are valid result v4 text, that the delta
// baseline names the full example and that the inspected node survives the
// delta. Only complete multi-line frames count as examples: the single-line
// `result v4 <ok|err> ...` grammar template shares the same prefix and is not
// an example frame.
func TestDiscoveryTreeExamplesDecodeAndApply(t *testing.T) {
	const framePrefix = "result v4 "
	var facts []map[string]any
	for _, literal := range strings.Split(treeTextGuide, "`") {
		if !strings.HasPrefix(literal, framePrefix) || !strings.Contains(literal, "\n") {
			continue
		}
		if !strings.HasSuffix(literal, "end result") {
			t.Fatalf("discovery example has no trailer: %q", literal)
		}
		decoded, isError, err := decodeResultText(t, literal+"\n")
		if err != nil || isError {
			t.Fatalf("discovery example is invalid: %v", err)
		}
		facts = append(facts, decoded)
	}
	if len(facts) != 3 {
		t.Fatalf("want full, delta and inspect examples, got %d", len(facts))
	}
	fullID, fullTree := discoveryExampleTree(t, facts[0])
	deltaID, deltaTree := discoveryExampleTree(t, facts[1])
	if fullTree.Full == nil || deltaTree.Delta == nil {
		t.Fatal("example omitted its full or delta tree")
	}
	if deltaTree.Delta.Base != fullID {
		t.Fatalf("example delta base = %q, want the example full %q", deltaTree.Delta.Base, fullID)
	}
	inspectionID, inspectionRoot := discoveryExampleInspection(t, facts[2])
	if inspectionID != deltaID {
		t.Fatalf("example inspection observation = %q, want %q", inspectionID, deltaID)
	}
	rebuilt, err := observation.ReadApply(fullTree.Full, deltaTree.Delta)
	if err != nil {
		t.Fatalf("discovery delta cannot apply to the example full: %v", err)
	}
	for _, node := range rebuilt.Root.Children {
		if node.Ref != inspectionRoot.Ref {
			continue
		}
		if len(node.Label) != 1 || len(inspectionRoot.Label) != 1 ||
			node.Label[0] != inspectionRoot.Label[0] {
			t.Fatal("example inspection label differs from the reconstructed tree")
		}
		return
	}
	t.Fatal("inspection ref is absent after applying the example delta")
}

// discoveryExampleTree reads the observation id and tree of one decoded text
// guide example. Only the facts actually present on the wire are read, because
// result v4 omits several strict members on purpose.
func discoveryExampleTree(t *testing.T, facts map[string]any) (string, protocol.ReadTreeValue) {
	t.Helper()
	observationFacts, ok := facts["observation"].(map[string]any)
	if !ok {
		t.Fatal("discovery example has no observation facts")
	}
	id, _ := observationFacts["id"].(string)
	if !strings.HasPrefix(id, "O") {
		t.Fatalf("discovery example observation id = %q", id)
	}
	treeFacts, ok := observationFacts["tree"].(map[string]any)
	if !ok {
		t.Fatalf("%s: discovery example has no tree facts", id)
	}
	wire, err := json.Marshal(treeFacts)
	if err != nil {
		t.Fatalf("%s: marshal example tree: %v", id, err)
	}
	var tree protocol.ReadTreeValue
	if err := protocol.Decode(wire, &tree); err != nil {
		t.Fatalf("%s: decode example tree: %v", id, err)
	}
	return id, tree
}

// discoveryExampleInspection reads the inspected observation id and the root
// subtree of the decoded inspect example.
func discoveryExampleInspection(t *testing.T, facts map[string]any) (string, protocol.ReadNode) {
	t.Helper()
	inspectionFacts, ok := facts["inspection"].(map[string]any)
	if !ok {
		t.Fatal("discovery example has no inspection facts")
	}
	id, _ := inspectionFacts["observation_id"].(string)
	if !strings.HasPrefix(id, "O") {
		t.Fatalf("discovery example inspection observation = %q", id)
	}
	rootFacts, ok := inspectionFacts["root"].(map[string]any)
	if !ok {
		t.Fatal("discovery example inspection has no root fact")
	}
	wire, err := json.Marshal(rootFacts)
	if err != nil {
		t.Fatalf("%s: marshal example inspection root: %v", id, err)
	}
	var root protocol.ReadNode
	if err := protocol.Decode(wire, &root); err != nil {
		t.Fatalf("%s: decode example inspection root: %v", id, err)
	}
	return id, root
}

// TestServerInstructionsCarryCrossToolRules checks the shared workflow and the
// single complete tree legend.
func TestServerInstructionsCarryCrossToolRules(t *testing.T) {
	instructions := ServerInstructions()
	if instructions == "" {
		t.Fatal("ServerInstructions is empty")
	}
	if got := strings.Count(instructions, treeTextGuide); got != 1 {
		t.Fatalf("tree guide occurrences = %d, want 1", got)
	}
	for _, required := range []string{
		"start({})", "can_start=false", "`recovery entry=observe`",
		"默认约 60 秒输入期限（以会话租期截断）", "精确截止时间见 structured JSON",
		"observation.id", "幂等", "五分钟", "两分钟", "不续租", "keepalive",
		"app.list", "diagnose", "app.find/notify 可无会话", "请求引用统一用 reference",
		"缺 delta 基线时用 observe(full=true)", "同一 base 的每条 delta",
		"`recovery` 行给出的精确 `ref=` 编号", "retry/O", "session.origin",
		"blocking_operation_id", "start_operation_id", "target_reference",
		"通常 uncertain 先用 status", "text_input.submit=uncertain", "取得新 O 后按需单发 ENTER",
		"避免原样重发或自动 end/restart", "not_executed", "executed", "device_busy", "pending cleanup",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("server instructions lack %q", required)
		}
	}
}

// TestDiscoveryOmitsObservationInternals scans every published description
// and input-schema description together with both discovery instruction paths.
// The two structured evidence objects remain in result JSON but their field
// names are absent from the model-facing discovery surface.
func TestDiscoveryOmitsObservationInternals(t *testing.T) {
	catalog, err := json.Marshal(CatalogSnapshot())
	if err != nil {
		t.Fatalf("marshal catalog snapshot: %v", err)
	}
	discovery := strings.ToLower(string(catalog) + "\n" + ServerInstructions())
	for _, forbidden := range []string{
		"quality",
		"effect",
		"观察质量",
		"滚动效果",
		"observed/not_observed/unknown",
		"observed",
		"not_observed",
		"unstable",
		"内容位移状态",
		"默认中心", "default_center", "carrier_node", "center_span", "exact → near", "shifted", "tier",
		"观察采样状态",
	} {
		if strings.Contains(discovery, forbidden) {
			t.Errorf("discovery surface contains %q", forbidden)
		}
	}

}

// TestServerInstructionsKeepContractBoundary keeps retired request and recovery spellings out.
func TestServerInstructionsKeepContractBoundary(t *testing.T) {
	instructions := ServerInstructions()
	for _, forbidden := range []string{
		"allow_ancestor_fallback",
		"observation.actionable_until",
		"recovery.entry=observe",
		"recovery.reference=P",
	} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("server instructions publish forbidden wording %q", forbidden)
		}
	}
}

var retiredActDetailKeys = []string{
	"nearest_actionable_ref",
	"safe_point",
	"suggested_point",
	"suggested_carrier_ref",
	"occluding_ref",
	"occluding_refs",
	"ambiguous_refs",
	"owner_refs",
	"safe_carrier_ref",
}

// containsRetiredActDetailKey matches a complete retired key, not a substring
// of a current key such as no_safe_point.
func containsRetiredActDetailKey(text string) bool {
	for _, key := range retiredActDetailKeys {
		for offset := 0; offset < len(text); {
			index := strings.Index(text[offset:], key)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(key)
			beforeBoundary := start == 0 || !isDetailKeyCharacter(text[start-1])
			afterBoundary := end == len(text) || !isDetailKeyCharacter(text[end])
			if beforeBoundary && afterBoundary {
				return true
			}
			offset = start + 1
		}
	}
	return false
}

func isDetailKeyCharacter(character byte) bool {
	return character == '_' ||
		character >= '0' && character <= '9' ||
		character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z'
}

// actOperationPropertySchema decodes one act operation member schema so a test
// can assert published enum and default values instead of description prose.
func actOperationPropertySchema(t *testing.T, member string) map[string]any {
	t.Helper()
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(testDefinitionByName(t, "act").schema, &schema); err != nil {
		t.Fatalf("decode act schema: %v", err)
	}
	operation, ok := schema.Properties["operation"]
	if !ok {
		t.Fatal("act schema has no operation member")
	}
	properties, ok := operation["properties"].(map[string]any)
	if !ok {
		t.Fatal("act operation schema has no properties")
	}
	property, ok := properties[member].(map[string]any)
	if !ok {
		t.Fatalf("act operation has no %s member", member)
	}
	return property
}

// TestActAmountSchemaPublishesFourTiers pins the closed four-tier travel amount
// contract: the exact enum order, the page default and every tier's travel and
// duration. The branch rejects duration_ms in the local act guidance.
func TestActAmountSchemaPublishesFourTiers(t *testing.T) {
	property := actOperationPropertySchema(t, "amount")
	enum, ok := property["enum"].([]any)
	if !ok {
		t.Fatal("amount enum missing")
	}
	want := []string{"step", "half", "page", "full"}
	if len(enum) != len(want) {
		t.Fatalf("amount enum = %v, want %v", enum, want)
	}
	for index, value := range enum {
		if value != want[index] {
			t.Fatalf("amount enum = %v, want %v", enum, want)
		}
	}
	if property["default"] != "page" {
		t.Fatalf("amount default = %v, want page", property["default"])
	}
	description, ok := property["description"].(string)
	if !ok {
		t.Fatal("amount description missing")
	}
	for _, required := range []string{
		"step 15%/500ms（最小 32px）",
		"half 30%/450ms",
		"page 60%/400ms",
		"full 85%/300ms",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("amount description lacks %q", required)
		}
	}
}

// TestActGuidanceDropsRetiredDetailKeys checks all descriptions, including
// nested input Schema fields, and the shared instructions.
func TestActGuidanceDropsRetiredDetailKeys(t *testing.T) {
	catalog, err := json.Marshal(CatalogSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if containsRetiredActDetailKey(ServerInstructions() + "\n" + string(catalog)) {
		t.Fatal("discovery retains a retired act detail key")
	}
}

// TestAppDescriptionPublishesBranchMatrix pins the published seven-branch
// matrix row by row, including that every row occupies its own line and that
// the tolerance sentence names the declared-member set without claiming the
// redundant members are forwarded.
func TestAppDescriptionPublishesBranchMatrix(t *testing.T) {
	description := testDefinitionByName(t, "app").description
	rows := []string{
		"find｜无｜query｜否",
		"list｜reference｜scope、app｜否",
		"open｜reference、target.app｜无｜是",
		"claim｜reference、target｜无｜是",
		"handoff｜reference、target｜restore_foreground｜是",
		"close｜reference、target｜allow_borrowed｜是",
		"kill｜reference、target.task_id 或 target.app｜allow_stop_playback｜是",
	}
	for _, row := range rows {
		if got := strings.Count(description, row); got != 1 {
			t.Errorf("app description carries matrix row %q %d times, want 1", row, got)
		}
		if !strings.Contains(description, "\n"+row+"\n") {
			t.Errorf("app description does not keep matrix row %q on its own line", row)
		}
	}
	for _, fragment := range []string{
		"七个分支都接受其余已声明顶层成员，但只按上表生效；冗余成员不进入服务端请求。",
		"list 必须 reference",
		"target.task_id 或 target.app",
		"一次 force-stop",
		"package_state",
		"kill 摘要按执行、任务/归属、进程、前台观察分层",
	} {
		if !strings.Contains(description, fragment) {
			t.Errorf("app description lacks %q", fragment)
		}
	}
}

// TestToolDescriptionsExcludeTreeGuide proves the complete legend is published
// once, in the instructions, and never repeated in a tool description.
func TestToolDescriptionsExcludeTreeGuide(t *testing.T) {
	for _, definition := range toolCatalog {
		if definition.description == "" {
			t.Fatalf("%s: empty description", definition.name)
		}
		if strings.Contains(definition.description, treeTextGuide) {
			t.Errorf("%s: description repeats the tree guide", definition.name)
		}
	}
}

// TestToolDescriptionsAreCallerOriented checks the local boundaries in isolation
// from ServerInstructions. TestDiscoveryByteBudget also verifies that the live
// discovery description exactly matches its source for every tool.
func TestToolDescriptionsAreCallerOriented(t *testing.T) {
	required := map[string][]string{
		"start": {
			"空对象", "`tree v5 full`", "`obs` 行取完整 O", "`screen=<W>x<H>@<display_id>`",
			"`session` 行的 `expires`", "重放原 start 不重新获得五分钟", "可消费观察", "幂等/查询编号",
		},
		"status": {
			"只读", "不访问设备、不续租、不认领 O", "`state=`", "`can_start=`", "`blocking_operation_id=`", "`recovery ref=`",
			"status(P) 可重放 open/claim/handoff/close/kill 的终态定格 app 摘要", "find/list 或无定格时仅 app.kind", "重放不附观察",
		},
		"observe": {
			"reference 必填", "full=true 强制 full", "wait_ms（0–10000 毫秒）仅延迟本次采集",
			"等待公开树条件应选 wait_for", "`obs` 行取完整 O", "`recovery entry=observe ref=<P>`",
		},
		"snap": {
			"`hint=snap` 只读核对", "不读取树、不消费 O、不发布新 O、不续租", "JPEG ImageContent",
			"`image <W>x<H> read_only screen=", "同一 P 重放或 status(P) 仅无图摘要",
		},
		"wait_for": {
			"timeout 为 1..40 秒（stable=true 时至少 2）", "query/absent_query", "任一条件成立",
			"内容稳定（忽略 bounds 与无动作叶子文本）连续 3 帧", "有查询时须同时满足查询条件和稳定门槛",
			"发布新 O 即成功，包括 wait.trigger=timeout", "`obs` 行取完整 O", "wait.matched 含终帧命中项",
			"超时时追加 timeout", "未发布按 `recovery entry=… ref=…`", "elapsed_ms 含终帧采集", "可超 timeout 至多约 35 秒",
		},
		"act": {
			"当前 ready O。静态拒绝不认领 O，认领即消费", "tap/long_press 用 target", "text 用 target 与 text", "key 用 key 白名单",
			"scroll 用 container/direction，可选 amount", "swipe 用 from/to，可选 duration_ms",
			"默认 bubble=true", "3×3 网格选点", "视觉区锚定最近交互祖先", "竞争遮挡时停止外扩并吸附唯一局部共位可点击控件",
			"bubble=false 只点原目标", "坐标点按/长按一律省略 bubble", "最深命中集合", "raw swipe 不报 ambiguous_point",
			"静态 422（invalid_target/unsupported_node_action/ambiguous_point/no_safe_point/no_safe_trajectory）均为 not_executed，原 O 可用于重试",
			"reason、scope/scope_ref、candidates 和可选 blocking_refs", "首个 candidates[].executable=true", "同一 O、新 P 重试",
			"unsupported_node_action（reason=no_interactive_ancestor）且目标有屏内裁剪几何时，error.details 可含 target_center: [x, y]",
			"仅是裁剪中心，无安全或业务响应保证", "独立准入与重验",
			"默认 auto_submit=true、submit_ref=\"enter_key\"", "全文写入一次，精确读回后至多一次 ENTER", "另发 ENTER",
			"text(ref) 可用于无 tap 能力的输入", "auto_submit=false，只写并读回，忽略 submit_ref", "submit_ref=\"n<ref>\"",
			"自动提交分支返回 text_input:{write,submit}", "写入确认而提交未派发为 partial（text_submit_not_executed）",
			"可能仅派发了聚焦", "已认领 text 的 partial/uncertain", "`recovery entry=observe ref=<P>`", "session.origin 或同会话 operation_id",
			"直接 observe 核对当前框值/页面", "避免原样重发", "dispatch（focus/write/submit：",
			"同视口嵌套滚动栈", "最近滚动祖先", "最内层滚动容器", "规划失败回退外层滚动祖先",
			"轴向不兼容时回退兼容外层滚动祖先", "422 unsupported_node_action（reason=axis_mismatch）", "50%/25%/75%", "scroll 不接受 duration_ms",
			"target_changed 若附新观察", "`obs` 行取完整 O，用新 O 和新 P 重试", "`change=` 比较前后树", "零差异命中历史基线", "`hint=` 建议截图或等待",
			"`!tap`/`!text`/`!swipe`", "`@` 沿最近有已交付几何的祖先", "诊断 ref 可用原 O 调 inspect(node)",
		},
		"inspect": {
			"node 与 query 互斥", "原 act 请求的 O 和诊断 ref 作 node", "详细子树", "O 过期后仍可能可读",
			"content_unavailable", "inspection_too_large", "inspect v3",
		},
		"keepalive": {
			"duration_minutes（1～120，默认 2）", "覆盖截止时间，可缩短租约", "输入期限（60 秒）或工作期限",
			"session_ended、session_expired 为终态，勿重试", "reference_has_no_session，用 status",
		},
		"end": {
			"先撤权并封闭派发，再独立收尾", "`session ended …`", "`cleanup …`", "通常先 pending", "不保证撤回", "`recovery`",
		},
		"app": {
			"find 无需会话", "list 必须 reference", "当前 ready O", "live owned", "一次 force-stop", "package_state",
			"close_refused_borrowed 带 error.details.retry_parameter=\"allow_borrowed\"",
			"已知播放/前台服务的 kill_refused_playback 带 retry_parameter=\"allow_stop_playback\"，状态未知时拒绝且省略该字段",
			"status(P)/重复 P 重放 open/claim/handoff/close/kill 的终态定格 app 摘要", "find/list、运行中或无定格摘要时仅 app.kind",
			"outcome 与 recovery", "`app_items v1`", "`app_tasks v1`", "`obs` 行取新 O",
		},
		"notify": {
			"无需活动会话，无 reference 参数", "executed 只表示消息已保存入队", "显示和已读均未确认",
			"`notify [id=…] [saved=…] [state=pending]`", "无需重发",
		},
		"shell": {
			"root=true", "命令不绑定会话屏", "input -d <display_id>", "`start` 首帧 `obs` 行", "`screen=<W>x<H>@<display_id>`",
			"`snap` 的 `image`", "`app(list)` 的 `display=`", "exit_code", "observe 核对",
			"uncertain 按 recovery 查原 P", "status(P) 即使 finished", "blocking_operation_id", "blocking_code 可为 shell_pending", "短暂退避",
		},
		"diagnose": {
			"只读", "不修复状态", "无参默认聚焦本 Agent 未结束会话（含会话期间独立发起的操作）",
			"操作级 issues 按 (phase, code) 去重", "可选 error_counts", "全部失败/不确定次数，独立于 issues 截断", "coverage=record_unavailable 时改用 status",
		},
	}
	if len(required) != len(toolCatalog) {
		t.Fatalf("description expectations cover %d tools, want %d", len(required), len(toolCatalog))
	}
	for _, definition := range toolCatalog {
		t.Run(definition.name, func(t *testing.T) {
			fragments, declared := required[definition.name]
			if !declared {
				t.Fatal("no description expectations")
			}
			for _, fragment := range fragments {
				requireTestContains(t, definition.description, fragment)
			}
			for _, forbidden := range []string{
				"allow_ancestor_fallback", "server instructions", "账本", "base=_72", "两格缩进",
			} {
				requireTestNotContains(t, definition.description, forbidden)
			}
		})
	}
}

// TestActDescriptionKeepsCallerRecovery also keeps the internal selector
// vocabulary out of the local recovery guidance.
func TestActDescriptionKeepsCallerRecovery(t *testing.T) {
	description := testDefinitionByName(t, "act").description
	for _, forbidden := range []string{"落点", "绑定", "同 Display"} {
		requireTestNotContains(t, description, forbidden)
	}
}

func TestKeepaliveDescriptionKeepsCallerContract(t *testing.T) {
	description := testDefinitionByName(t, "keepalive").description
	for _, forbidden := range []string{
		"max(现有截止时间", "历史去重", "同一可识别 P 重试", "P 记录仍有界保留", "actionable_until",
	} {
		requireTestNotContains(t, description, forbidden)
	}
}

func TestLiveQuerySchemaMinimum(t *testing.T) {
	session := connectTestInMemory(t, newTestMCPServer(t, newTestDeviceService(t)))
	ctx, cancel := testCallContext(t)
	defer cancel()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	expected := map[string][]string{"inspect": {"query"}, "wait_for": {"query", "absent_query"}}
	matched := 0
	for _, tool := range list.Tools {
		fields, ok := expected[tool.Name]
		if !ok {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				MinLength int `json:"minLength"`
				MaxLength int `json:"maxLength"`
			} `json:"properties"`
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			got := schema.Properties[field]
			if got.MinLength != 1 || got.MaxLength != protocol.MaxQueryRunes {
				t.Errorf("%s.%s: minLength=%d maxLength=%d", tool.Name, field, got.MinLength, got.MaxLength)
			}
			matched++
		}
	}
	if matched != 3 {
		t.Errorf("checked %d live query fields, want 3", matched)
	}
}

// TestDiscoveryByteBudget measures the live in-memory SDK discovery the same way
// the baseline was measured and proves the deduplicated text stays below it.
// Descriptions and schemas are logged for comparison with the baseline.
func TestDiscoveryByteBudget(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	initialize := session.InitializeResult()
	if initialize == nil {
		t.Fatal("initialize result missing")
	}
	instructions := initialize.Instructions

	ctx, cancel := testCallContext(t)
	defer cancel()
	catalog, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	toolsJSON, err := json.Marshal(catalog.Tools)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	descriptions := 0
	schemas := 0
	for _, tool := range catalog.Tools {
		descriptions += len(tool.Description)
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		schemas += len(encoded)
		if tool.Name == "act" || tool.Name == "app" {
			t.Logf("%s discovery bytes: description=%d schema=%d", tool.Name, len(tool.Description), len(encoded))
		}
	}
	total := len(instructions) + len(toolsJSON)
	t.Logf("discovery bytes: instructions=%d tools_list=%d descriptions=%d schemas=%d total=%d baseline=%d",
		len(instructions), len(toolsJSON), descriptions, schemas, total, baselineDiscoveryBytes)
	if len(toolsJSON) > toolsListByteBudget {
		t.Errorf("tools/list bytes = %d, want <= %d", len(toolsJSON), toolsListByteBudget)
	}
	if total >= baselineDiscoveryBytes {
		t.Fatalf("discovery total = %d, want < %d", total, baselineDiscoveryBytes)
	}

	if got := strings.Count(instructions, treeTextGuide); got != 1 {
		t.Fatalf("live instructions contain the tree guide %d times, want 1", got)
	}
	if len(catalog.Tools) != len(toolCatalog) {
		t.Fatalf("live tools = %d, want %d", len(catalog.Tools), len(toolCatalog))
	}
	// The SDK publishes tools sorted by name. Compare that deterministic order
	// with the source catalog names so a rename, add or removal fails here too.
	wantNames := make([]string, 0, len(toolCatalog))
	for _, definition := range toolCatalog {
		wantNames = append(wantNames, definition.name)
	}
	sort.Strings(wantNames)
	for index, tool := range catalog.Tools {
		if tool.Name != wantNames[index] {
			t.Fatalf("live tool %d = %q, want %q", index, tool.Name, wantNames[index])
		}
		source := testDefinitionByName(t, tool.Name)
		if tool.Description != source.description {
			t.Fatalf("%s: live description differs from the source catalog", tool.Name)
		}
		if tool.Description == "" {
			t.Fatalf("%s: empty live description", tool.Name)
		}
		if strings.Contains(tool.Description, treeTextGuide) {
			t.Fatalf("%s: live description repeats the tree guide", tool.Name)
		}
	}
}

// TestServerInstructionsDiscovery proves the live server/discover result carries
// the source instructions unchanged.
func TestServerInstructionsDiscovery(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	result := session.InitializeResult()
	if result == nil {
		t.Fatal("initialize result missing")
	}
	if result.Instructions == "" {
		t.Fatal("server/discover instructions are empty")
	}
	if result.Instructions != ServerInstructions() {
		t.Fatal("live discovery instructions differ from the source accessor")
	}
}

// TestServerInstructionsCompatibilityInitialize proves the legacy initialize
// handshake returns the same instructions as server/discover.
func TestServerInstructionsCompatibilityInitialize(t *testing.T) {
	fake := newTestDeviceService(t)
	server := newTestMCPServer(t, fake)
	session := connectTestInMemoryWithOptions(t, server, &sdk.ClientSessionOptions{
		ProtocolVersion: compatibilityProtocolVersion,
	})
	result := session.InitializeResult()
	if result == nil {
		t.Fatal("initialize result missing")
	}
	if result.ProtocolVersion != compatibilityProtocolVersion {
		t.Fatalf("negotiated protocol = %q, want %q", result.ProtocolVersion, compatibilityProtocolVersion)
	}
	if result.Instructions == "" {
		t.Fatal("compatibility initialize instructions are empty")
	}
	if result.Instructions != ServerInstructions() {
		t.Fatal("compatibility initialize instructions differ from the source accessor")
	}
}

// connectTestInMemoryWithOptions is connectTestInMemory with explicit client
// session options, used to force the compatibility initialize handshake.
func connectTestInMemoryWithOptions(t *testing.T, server *sdk.Server, options *sdk.ClientSessionOptions) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverTransport) }()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil).
		Connect(ctx, clientTransport, options)
	if err != nil {
		cancel()
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("server did not stop")
		}
	})
	return session
}
