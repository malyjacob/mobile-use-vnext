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
// JSON. Contract 30 adds observation change and hint guidance to this budget.
const baselineDiscoveryBytes = 38200

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
		"`loading` 在 `delta` 也保留，其余同态 `delta` 省略",
		"`change` 仅见于 `act` 常规成功且比较前后树",
		"`unchanged` 前后紧凑树与完整树均相同",
		"`detail_only` 紧凑树相同但完整树细节有变",
		"`matched_base` 前后树内容或公开语境有变且零差异命中历史基线",
		"`hint=snap` 建议用同一 O 截图核对（只读，不消费、不发布 O）",
		"`hint=wait_for` 表示可能仍在加载或过渡",
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
		"start({})",
		"can_start=false",
		"`recovery entry=observe`",
		"默认约 60 秒输入期限（以会话租期截断）",
		"精确截止时间见 structured JSON",
		"幂等",
		"五分钟",
		"两分钟",
		"不续租",
		"keepalive",
		"wait.trigger",
		"wait.matched",
		"写入全文",
		"app.list",
		"diagnose",
		"notify 无 reference",
		"同一 base 的每条 delta",
		"不发送 ENTER",
		"auto_submit",
		"submit_ref",
		"text_input",
		"bubble",
		"统一用 reference",
		"not_executed",
		"uncertain",
		"executed",
		"blocking_operation_id",
		"start_operation_id",
		"target_reference",
		"device_busy",
		"pending cleanup",
		"no_safe_point",
		"自动按 3×3 网格选点",
		"竞争遮挡时停止外扩并自动吸附唯一局部共位可点击控件",
		"最深命中集合",
		"raw swipe 不报 ambiguous_point",
		"同视口嵌套滚动栈",
		"50%/25%/75%",
		"no_safe_trajectory",
		"error.details.candidates",
		"第一个 executable=true 的 ref",
		"同一 O 和新 P",
		"无需重观察或手算坐标",
		"session.origin",
		"全裁无标注非交互 `image` 叶",
		"inspect({reference:原 act 的 O,node:诊断 ref})",
		"O 过期后仍可能可读",
		"`recovery` 行给出的精确 `ref=` 编号",
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
		"观察采样状态",
	} {
		if strings.Contains(discovery, forbidden) {
			t.Errorf("discovery surface contains %q", forbidden)
		}
	}

	waitFor := testDefinitionByName(t, "wait_for")
	for _, required := range []string{"内容稳定", "忽略 bounds 与无动作叶子文本", "连续 3 帧"} {
		requireTestContains(t, waitFor.description, required)
	}
	requireTestNotContains(t, waitFor.description, "整树连续 3 帧一致")

	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(waitFor.schema, &schema); err != nil {
		t.Fatalf("decode wait_for schema: %v", err)
	}
	stable := schema.Properties["stable"].Description
	for _, required := range []string{"内容稳定", "忽略 bounds 与无动作叶子文本", "连续 3 帧"} {
		requireTestContains(t, stable, required)
	}
	requireTestNotContains(t, stable, "整树连续 3 帧一致")
}

// TestServerInstructionsKeepContractBoundary proves the instructions publish
// the reference unification and the branch-qualified defaults, while never
// naming the rejected alternative (allow_ancestor_fallback) or claiming a
// top-level default that would apply to coordinates or other actions.
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
	if !strings.Contains(instructions, "observation.id") {
		t.Error("server instructions do not identify the observation id to reuse")
	}
	for _, required := range []string{
		"请求引用统一用 reference",
		"默认 bubble=true",
		"默认 auto_submit=true",
		"operation.bubble=false",
		"operation.auto_submit=false",
		"operation.submit_ref=",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("server instructions lack %q", required)
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

func actOperationParameterDescriptions(t *testing.T) map[string]string {
	t.Helper()
	var schema struct {
		Properties map[string]struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(testDefinitionByName(t, "act").schema, &schema); err != nil {
		t.Fatalf("decode act schema: %v", err)
	}
	operation, ok := schema.Properties["operation"]
	if !ok {
		t.Fatal("act schema has no operation member")
	}
	out := make(map[string]string, len(operation.Properties))
	for name, property := range operation.Properties {
		out[name] = property.Description
	}
	return out
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
// duration. scroll never accepts a caller-supplied duration.
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
		"step（15%，500ms，最小行程 32px）",
		"half（30%，450ms）",
		"page（默认，60%，400ms）",
		"full（85%，300ms）",
		"scroll 不接受 duration_ms",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("amount description lacks %q", required)
		}
	}
}

// TestActScrollDescriptionsPublishFourTiersAndAxisFallback proves the act tool
// description and the published server instructions repeat the four-tier amount
// and the axis-compatibility fallback the input schema declares.
func TestActScrollDescriptionsPublishFourTiersAndAxisFallback(t *testing.T) {
	act := testDefinitionByName(t, "act").description
	for _, required := range []string{
		"amount 四档 step 15%/500ms（最小 32px）",
		"half 30%/450ms",
		"page 60%/400ms（默认）",
		"full 85%/300ms",
		"scroll 不接受 duration_ms",
		"轴向不兼容时自动回退兼容外层滚动祖先",
		"422 unsupported_node_action（reason=axis_mismatch）",
	} {
		if !strings.Contains(act, required) {
			t.Errorf("act description lacks %q", required)
		}
	}
	instructions := ServerInstructions()
	for _, required := range []string{
		"amount 四档 step 15%/500ms、half 30%/450ms、page 60%/400ms（默认）、full 85%/300ms",
		"scroll 不接受时长参数",
		"轴向不兼容时自动回退兼容外层滚动祖先",
		"422 unsupported_node_action（reason=axis_mismatch）",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("server instructions lack %q", required)
		}
	}
}

// TestActBranchDescriptionsPublishAutomaticResolution pins the caller-facing
// automatic selection behavior and keeps the retired detail vocabulary out of
// both branch parameters.
func TestActBranchDescriptionsPublishAutomaticResolution(t *testing.T) {
	descriptions := actOperationParameterDescriptions(t)
	for _, required := range []string{
		"省略按 true",
		"3×3",
		"视觉区",
		"竞争遮挡时停止外扩",
		"局部共位可点击控件",
		"false 只点原目标",
		"不做提升或吸附",
	} {
		if !strings.Contains(descriptions["bubble"], required) {
			t.Errorf("bubble description lacks %q", required)
		}
	}
	for _, required := range []string{
		"同视口嵌套滚动栈",
		"最近 role=scroll 祖先",
		"最内层滚动容器",
		"回退外层滚动祖先",
		"轴向与请求方向不兼容时自动回退兼容外层滚动祖先",
		"422 unsupported_node_action（reason=axis_mismatch）",
	} {
		if !strings.Contains(descriptions["container"], required) {
			t.Errorf("container description lacks %q", required)
		}
	}
	if containsRetiredActDetailKey(descriptions["bubble"]) ||
		containsRetiredActDetailKey(descriptions["container"]) {
		t.Fatal("act branch descriptions retain a retired detail key")
	}
}

// TestActGuidanceDropsRetiredDetailKeys proves every caller-facing guidance
// surface uses the candidates contract instead of the removed per-failure keys.
func TestActGuidanceDropsRetiredDetailKeys(t *testing.T) {
	descriptions := actOperationParameterDescriptions(t)
	surfaces := map[string]string{
		"server instructions": ServerInstructions(),
		"act":                 testDefinitionByName(t, "act").description,
		"bubble":              descriptions["bubble"],
		"container":           descriptions["container"],
	}
	for name, text := range surfaces {
		if containsRetiredActDetailKey(text) {
			t.Errorf("%s retains a retired act detail key", name)
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

// TestToolDescriptionsAreCallerOriented asserts every description states its
// own purpose, key parameters/defaults, result or action boundary and minimal
// recovery, and that internal algorithms and the rejected ancestor-fallback
// name stay out.
func TestToolDescriptionsAreCallerOriented(t *testing.T) {
	required := map[string][]string{
		"start": {
			"请求体必须是空对象",
			"`obs` 行",
			"`screen=<W>x<H>@<display_id>`",
			"`session` 行的 `expires`",
			"重放原 start 不重新获得五分钟",
			"可消费观察",
			"幂等/查询编号",
			"`tree v5 full`",
			"base 指向的 full 加本次 delta",
		},
		"status": {
			"`state=`",
			"`can_start=`",
			"`blocking_operation_id=`",
			"`recovery ref=`",
			"不续租",
		},
		"snap": {
			"`hint=snap` 只读核对",
			"不读取树、不消费 O、不发布新 O",
			"ImageContent",
			"`image <W>x<H> read_only screen=",
			"不续租",
			"无图摘要",
		},
		"observe": {
			"reference 必填",
			"0–10000",
			"`obs` 行",
			"`recovery entry=observe ref=<P>`",
			"`base=_72`",
			"full=true",
			"同一 full 上的多个差分各自应用",
		},
		"wait_for": {
			"1..40",
			"stable=true",
			"内容稳定",
			"忽略 bounds 与无动作叶子文本",
			"连续 3 帧",
			"wait.trigger",
			"wait.matched",
			"35 秒",
			"`obs` 行",
			"`recovery entry=… ref=…`",
			"`base=_72`",
			"full=true",
			"同一 full 上的多个差分各自应用",
		},
		"act": {
			"reference",
			"auto_submit",
			"submit_ref",
			"text_input",
			"bubble",
			"另发 ENTER",
			"invalid_target",
			"unsupported_node_action",
			"no_safe_point",
			"reason",
			"no_safe_trajectory",
			"candidates",
			"blocking_refs",
			"executable=true",
			"3×3 网格选点",
			"竞争控件遮挡时停止向祖先其他区域漂移",
			"唯一的局部共位可点击控件",
			"最深命中集合",
			"raw swipe 不返回 ambiguous_point",
			"同视口嵌套滚动栈",
			"最近滚动祖先",
			"最内层滚动容器",
			"50%/25%/75%",
			"同一 O、新 P",
			"inspect({reference:原 O,node:诊断 ref})",
			"target_changed",
			"`obs` 行",
			"`change=` 树变化",
			"`matched_base` 为前后有变、零差异命中历史基线，不是没生效",
			"`hint=` 建议截图或等待",
			"`!tap`/`!text`/`!swipe`",
			"`@` 表示",
			"uncertain",
			"partial",
			"text_submit_not_executed",
			"dispatch（focus/write/submit）",
			"`recovery entry=observe ref=<P>`",
			"无需重观察或手算坐标",
		},
		"inspect": {
			"node 与 query 互斥",
			"原 act 请求的 O",
			"该 ref 作 node",
			"O 过期后仍可能可读",
			"content_unavailable",
			"inspection_too_large",
			"inspect v3",
			"两格缩进",
			"不使用普通树 v5 的默认动作、`@` 或短 base",
		},
		"keepalive": {
			"1～120",
			"默认 2",
			"缩短",
			"输入期限（60 秒）",
			"session_ended",
			"reference_has_no_session",
			"session_expired",
			"status",
		},
		"end": {
			"`session ended …`",
			"`cleanup …`",
			"pending",
			"不保证撤回",
			"`recovery`",
		},
		"app": {
			"find 无需会话",
			"scope=primary",
			"当前 ready O",
			"live owned",
			"outcome",
			"`app_items v1`",
			"`app_tasks v1`",
			"`obs` 行",
		},
		"notify": {
			"executed 只表示消息已保存",
			"notification.state",
			"`notify [id=…] [saved=…] [state=pending]`",
			"无需活动会话",
			"无 reference 参数",
		},
		"shell": {
			"root=true",
			"exit_code",
			"observe",
			"status(P)",
			"finished",
			"blocking_operation_id",
			"blocking_code",
			"shell_pending",
			"`start` 首帧 `obs` 行",
			"`screen=<W>x<H>@<display_id>`",
			"`app(list)` 的 `display=`",
		},
		"diagnose": {
			"coverage=record_unavailable",
			"status",
		},
	}
	if len(required) != len(toolCatalog) {
		t.Fatalf("description expectations cover %d tools, want %d", len(required), len(toolCatalog))
	}
	for _, definition := range toolCatalog {
		fragments, declared := required[definition.name]
		if !declared {
			t.Fatalf("%s: no description expectations", definition.name)
		}
		for _, fragment := range fragments {
			if !strings.Contains(definition.description, fragment) {
				t.Errorf("%s description lacks %q", definition.name, fragment)
			}
		}
		for _, forbidden := range []string{"allow_ancestor_fallback", "server instructions", "账本"} {
			if strings.Contains(definition.description, forbidden) {
				t.Errorf("%s description publishes A-phase-forbidden %q", definition.name, forbidden)
			}
		}
	}
}

// TestLiveToolDescriptionsClarifyWaitAndTextRecovery checks the descriptions
// and instructions actually returned by live discovery, not only the source
// catalog.
func TestLiveToolDescriptionsClarifyWaitAndTextRecovery(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	ctx, cancel := testCallContext(t)
	defer cancel()

	catalog, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	descriptions := make(map[string]string, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		descriptions[tool.Name] = tool.Description
	}

	const textRecoveryTrigger = "已认领 text 的整体 outcome=uncertain"
	required := map[string][]string{
		"observe": {
			"wait_ms",
			"毫秒",
			"等待公开树条件应选 wait_for",
			"`obs` 行",
			"`recovery entry=observe ref=<P>`",
			"`base=_72`",
			"full=true",
			"同一 full 上的多个差分各自应用",
		},
		"wait_for": {
			"轮询当前会话的公开树条件",
			"任一条件成立",
			"stable=true",
			"内容稳定",
			"忽略 bounds 与无动作叶子文本",
			"连续 3 帧",
			"发布新观察时调用成功",
			"`obs` 行",
			"`recovery entry=… ref=…`",
			"`base=_72`",
			"full=true",
			"同一 full 上的多个差分各自应用",
			"wait.trigger=timeout",
			"wait.matched 包含终帧命中的条件",
			"超时时还包含 timeout",
			"最终未发布新 O",
		},
		"act": {
			textRecoveryTrigger,
			"仅已派发聚焦",
			"`recovery entry=observe ref=<P>`",
			"直接 observe 核对当前框值/页面",
			"原活动会话的具体归属 P",
			"session.origin",
			"属于该会话的 operation_id",
			"避免原样重发",
			"submit_ref",
			"text_submit_not_executed",
			"`!tap`/`!text`/`!swipe`",
			"`@` 表示",
			"3×3 网格选点",
			"no_safe_trajectory",
			"candidates",
			"executable=true",
		},
		"inspect": {
			"原 act 请求的 O",
			"诊断 ref",
			"该 ref 作 node",
			"详细子树",
			"O 过期后仍可能可读",
			"两格缩进",
			"不使用普通树 v5 的默认动作、`@` 或短 base",
		},
	}
	for name, fragments := range required {
		t.Run(name, func(t *testing.T) {
			description, ok := descriptions[name]
			if !ok {
				t.Fatalf("%s missing from live tools/list", name)
			}
			for _, fragment := range fragments {
				if !strings.Contains(description, fragment) {
					t.Errorf("live %s description lacks %q", name, fragment)
				}
			}
		})
	}

	initialize := session.InitializeResult()
	if initialize == nil {
		t.Fatal("initialize result missing")
	}
	for _, fragment := range []string{
		textRecoveryTrigger,
		"直接 observe 核对当前框值/页面",
		"`recovery entry=observe ref=<P>`",
		"通常 uncertain 先用 status",
		"全裁无标注非交互 `image` 叶",
		"inspect({reference:原 act 的 O,node:诊断 ref})",
		"链下缩进的孩子属于链尾",
		"`result v4 <ok|err> [op=…] [state=…] [outcome=…] [phase=…]`",
		"`obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [visual=<token>] [change=<token>] [hint=<token>]",
		"`hint=snap` 用同一 O 调 snap 核对画面",
		"`hint=wait_for` 用 wait_for 等条件或稳定",
		"`change` 表示 `act` 前后树比较",
		"`detail_only` 仅完整树细节有变",
		"`matched_base` 前后有变、零差异命中历史基线",
		"tree v5 full nodes=<n>",
		"`base=_72`",
		"`@` 表示与同一定义中最近已交付几何的严格祖先同形同值",
		"`!tap`、`!text` 或 `!swipe`",
		"3×3 网格选点",
		"同视口嵌套滚动栈",
		"no_safe_trajectory",
		"executable=true",
	} {
		if !strings.Contains(initialize.Instructions, fragment) {
			t.Errorf("live server instructions lack %q", fragment)
		}
	}
}

// TestActDescriptionKeepsCallerRecovery removes the internal landing and focus
// binding algorithm from the public text while keeping the actionable recovery
// contract and the explicit no-ENTER boundary.
func TestActDescriptionKeepsCallerRecovery(t *testing.T) {
	description := testDefinitionByName(t, "act").description
	for _, forbidden := range []string{
		"default_center",
		"carrier_node",
		"center_span",
		"exact → near",
		"shifted",
		"safe_carrier_ref",
		"occluding_ref",
		"Tier",
		"落点",
		"绑定",
		"同 Display",
	} {
		if strings.Contains(description, forbidden) {
			t.Errorf("act description retains internal algorithm wording %q", forbidden)
		}
	}
	for _, required := range []string{
		"全文写入",
		"另发 ENTER",
		"auto_submit=false",
		"submit_ref",
		"text_submit_not_executed",
		"text_input",
		"bubble=true",
		"3×3",
		"no_safe_trajectory",
		"candidates",
		"executable=true",
		"同一 O、新 P",
		"用新 O 和新 P 重试",
		"uncertain",
		"避免原样重发",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("act description lacks caller recovery %q", required)
		}
	}
}

// TestKeepaliveDescriptionKeepsCallerContract removes the internal renewal
// comparison and history dedup wording while keeping the range, default,
// shortening behaviour and error recovery.
func TestKeepaliveDescriptionKeepsCallerContract(t *testing.T) {
	description := testDefinitionByName(t, "keepalive").description
	for _, forbidden := range []string{
		"max(现有截止时间",
		"历史去重",
		"同一可识别 P 重试",
		"P 记录仍有界保留",
		"actionable_until",
		"账本",
	} {
		if strings.Contains(description, forbidden) {
			t.Errorf("keepalive description retains internal wording %q", forbidden)
		}
	}
	for _, required := range []string{
		"1～120",
		"默认 2",
		"缩短",
		"输入期限（60 秒）",
		"session_ended",
		"reference_has_no_session",
		"session_expired",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("keepalive description lacks %q", required)
		}
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
// Sub-items explain where the bytes went; they are not separate thresholds.
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
