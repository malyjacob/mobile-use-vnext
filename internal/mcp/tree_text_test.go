package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func testBoolPtr(value bool) *bool { return &value }

func testStringPtr(value string) *string { return &value }

func testBounds(left, top, right, bottom int) protocol.ReadBounds {
	return protocol.KnownBounds(protocol.Bounds{Left: left, Top: top, Right: right, Bottom: bottom})
}

func testAxisPtr(value protocol.Axis) *protocol.Axis { return &value }

// testNode builds one public read node with the known defaults a decoded node
// carries: enabled=true, focused/selected/protected=false and checked=false.
// An unknown checked state uses a nil Checked and renders checked=null.
func testNode(ref string, role protocol.Role, bounds protocol.ReadBounds) *protocol.ReadNode {
	return &protocol.ReadNode{
		Ref: ref, Role: role, Bounds: bounds,
		Label:   []string{},
		Actions: []protocol.Action{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
		Checked:  testBoolPtr(false),
		Children: []*protocol.ReadNode{},
	}
}

func testLargeTree(childCount, labelBytes int) *protocol.ReadTree {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	for index := 2; index < childCount+2; index++ {
		child := testNode("n"+strconv.Itoa(index), protocol.RoleText, testBounds(0, 0, 1080, 80))
		child.Label = []string{strings.Repeat("x", labelBytes)}
		root.Children = append(root.Children, child)
	}
	return &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}
}

func mustRenderTreeText(t *testing.T, result *protocol.Result) string {
	t.Helper()
	block, ok := renderTreeText(result)
	if !ok {
		t.Fatal("tree text is not representable")
	}
	return block
}

func requireNoTreeText(t *testing.T, result *protocol.Result) {
	t.Helper()
	block, ok := renderTreeText(result)
	if ok || block != "" {
		t.Fatalf("tree text unexpectedly representable: ok=%t block=%q", ok, block)
	}
}

// testFullTree is the synthetic tree used by the full golden test. It exercises
// the three public geometry states: known rectangles, source geometry the
// source never provided ("-") and geometry the compact view deliberately
// withholds (no bounds column for the anonymous container n14, whose checked
// state is also unknown and therefore rendered checked=null).
func testFullTree() *protocol.ReadTree {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	group := testNode("n2", protocol.RoleGroup, testBounds(0, 0, 1080, 220))

	back := testNode("n3", protocol.RoleButton, testBounds(24, 60, 144, 180))
	back.Label = []string{"返回"}
	back.Actions = []protocol.Action{protocol.ActionTap}

	title := testNode("n4", protocol.RoleText, testBounds(300, 80, 780, 160))
	title.Label = []string{"设置"}

	toggle := testNode("n6", protocol.RoleSwitch, testBounds(900, 60, 1032, 180))
	toggle.Label = []string{"启用"}
	toggle.Actions = []protocol.Action{protocol.ActionTap}
	toggle.Checked = testBoolPtr(true)

	scroll := testNode("n5", protocol.RoleScroll, testBounds(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY

	group12 := testNode("n12", protocol.RoleGroup, protocol.UnknownBounds())
	search := testNode("n13", protocol.RoleInput, testBounds(48, 1850, 1032, 1990))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}

	anonymous := testNode("n14", protocol.RoleGroup, protocol.HiddenBounds())
	anonymous.Checked = nil

	group.Children = []*protocol.ReadNode{back, title, toggle}
	group12.Children = []*protocol.ReadNode{search}
	scroll.Children = []*protocol.ReadNode{group12, anonymous}
	root.Children = []*protocol.ReadNode{group, scroll}
	return &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}
}

const testTreeTextFullGolden = `tree v4 full nodes=9

n1 root 0,0,1080,2400
 n2 group 0,0,1080,220
  n3 button 24,60,144,180 "返回"
  n4 text 300,80,780,160 "设置"
  n6 switch 900,60,1032,180 "启用" tap checked=true
 n5 scroll 0,220,1080,2400 !swipe axis=y
  n12 group -
   n13 input 48,1850,1032,1990 "搜索设置" value="" hint="输入关键词" tap
  n14 group checked=null
end tree
`

func testDelta() *protocol.ReadDelta {
	add := testNode("n14", protocol.RoleGroup, testBounds(0, 460, 1080, 580))
	mobile := testNode("n15", protocol.RoleText, testBounds(48, 490, 700, 550))
	mobile.Label = []string{"移动网络"}
	mobile.Actions = []protocol.Action{protocol.ActionTap}
	add.Children = []*protocol.ReadNode{mobile}

	replace := testNode("n12", protocol.RoleGroup, testBounds(0, 1800, 1080, 2100))
	search := testNode("n91", protocol.RoleInput, testBounds(48, 1850, 1032, 1990))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}
	replace.Children = []*protocol.ReadNode{search}

	return &protocol.ReadDelta{
		Format: "delta", Base: "O3", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n8": {Checked: protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)}},
			"n7": {
				Label:   protocol.Field[[]string]{Set: true, Value: []string{"WLAN 已关闭"}},
				Actions: protocol.Field[[]protocol.Action]{Set: true, Value: []protocol.Action{protocol.ActionTap}},
			},
		},
		Add:      []*protocol.ReadNode{add},
		Remove:   []string{"n16"},
		Children: map[string][]string{"n5": {"n6", "n9", "n14"}},
		Replace:  map[string]*protocol.ReadNode{"n12": replace},
	}
}

const testTreeTextDeltaGolden = `tree v4 delta base=O3 changes=6

~ n7 label=["WLAN 已关闭"] actions=["tap"]
~ n8 checked=false
+ n14 group 0,460,1080,580
 n15 text 48,490,700,550 "移动网络" tap
- n16
children n5 = [n6 n9 n14]
= n12 group 0,1800,1080,2100
 n91 input 48,1850,1032,1990 "搜索设置" value="" hint="输入关键词" tap
end tree
`

func testInspectionTreeView() *protocol.InspectionView {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	scroll := testNode("n5", protocol.RoleScroll, testBounds(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY
	confirm := testNode("n7", protocol.RoleButton, testBounds(40, 600, 240, 680))
	confirm.Label = []string{"确定"}
	confirm.Actions = []protocol.Action{protocol.ActionTap}
	scroll.Children = []*protocol.ReadNode{confirm}
	root.Children = []*protocol.ReadNode{scroll}
	// The tree scope declares view inside the nested complete tree: historical
	// inspection material is always detailed.
	return &protocol.InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 4,
		CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
		Tree: &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed, Root: root},
	}
}

func testInspectionSubtreeView() *protocol.InspectionView {
	scroll := testNode("n5", protocol.RoleScroll, testBounds(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY
	confirm := testNode("n7", protocol.RoleButton, testBounds(40, 600, 240, 680))
	confirm.Label = []string{"确定"}
	confirm.Actions = []protocol.Action{protocol.ActionTap}
	scroll.Children = []*protocol.ReadNode{confirm}
	return &protocol.InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 4,
		CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
		View: protocol.ViewDetailed,
		Root: scroll,
	}
}

func testInspectionQueryView() *protocol.InspectionView {
	button := testNode("n7", protocol.RoleButton, testBounds(40, 600, 240, 680))
	button.Label = []string{"确定"}
	button.Actions = []protocol.Action{protocol.ActionTap}

	group := testNode("n9", protocol.RoleGroup, testBounds(0, 700, 1080, 900))
	text := testNode("n10", protocol.RoleText, testBounds(40, 720, 600, 780))
	text.Label = []string{"确定"}
	cancel := testNode("n11", protocol.RoleButton, testBounds(40, 800, 240, 880))
	cancel.Label = []string{"取消"}
	cancel.Actions = []protocol.Action{protocol.ActionTap}
	group.Children = []*protocol.ReadNode{text, cancel}

	return &protocol.InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 4,
		CapturedAt: "2026-11-10T03:00:00Z", Scope: "query",
		View: protocol.ViewDetailed,
		Matches: []protocol.InspectionMatch{
			{Node: button, Path: []string{"n1", "n5"}},
			{Node: group, Path: []string{"n1", "n6"}},
		},
		Total: 3, HasTotal: true, Truncated: testBoolPtr(true),
	}
}

const testTreeTextInspectTreeGolden = `inspect v3 scope=tree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context view=detailed nodes=3 entries=3

n1 root [0,0,1080,2400]
  n5 scroll [0,220,1080,2400] axis=y
    n7 button [40,600,240,680] "确定" tap
end inspect
`

const testTreeTextInspectSubtreeGolden = `inspect v3 scope=subtree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context view=detailed root=n5 nodes=2 entries=2

n5 scroll [0,220,1080,2400] axis=y
  n7 button [40,600,240,680] "确定" tap
end inspect
`

const testTreeTextInspectQueryGolden = `inspect v3 scope=query observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context view=detailed matches=2 total=3 truncated=true nodes=4 entries=4

match 1/2 path=[n1 n5]
n7 button [40,600,240,680] "确定" tap

match 2/2 path=[n1 n6]
n9 group [0,700,1080,900]
  n10 text [40,720,600,780] "确定"
  n11 button [40,800,240,880] "取消" tap
end inspect
`

func TestTreeTextFullGolden(t *testing.T) {
	result := &protocol.Result{Observation: &protocol.Observation{
		Status: "complete", ID: "O3", Revision: 3,
		Tree: &protocol.ReadTreeValue{Full: testFullTree()},
	}}
	if block := mustRenderTreeText(t, result); block != testTreeTextFullGolden {
		t.Fatalf("full block mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextFullGolden)
	}
}

func TestTreeTextDeltaGolden(t *testing.T) {
	result := &protocol.Result{Observation: &protocol.Observation{
		Status: "complete", ID: "O4", Revision: 4,
		Tree: &protocol.ReadTreeValue{Delta: testDelta()},
	}}
	if block := mustRenderTreeText(t, result); block != testTreeTextDeltaGolden {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextDeltaGolden)
	}
}

func TestTreeTextDeltaExplicitValues(t *testing.T) {
	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n1": {
				Label:     protocol.Field[[]string]{Set: true, Value: []string{}},
				Actions:   protocol.Field[[]protocol.Action]{Set: true, Value: []protocol.Action{}},
				Bounds:    protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.UnknownBounds()},
				Enabled:   protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)},
				Focused:   protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)},
				Selected:  protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)},
				Checked:   protocol.Field[*bool]{Set: true, Value: testBoolPtr(true)},
				Value:     protocol.Field[*string]{Set: true, Value: testStringPtr("")},
				Hint:      protocol.Field[*string]{Set: true, Value: testStringPtr("h")},
				Protected: protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)},
				Axis:      protocol.Field[*protocol.Axis]{Set: true, Value: testAxisPtr(protocol.AxisY)},
				Resource:  protocol.Field[*string]{Set: true, Value: testStringPtr("r")},
			},
			"n2": {
				Label:     protocol.Field[[]string]{Set: true},
				Actions:   protocol.Field[[]protocol.Action]{Set: true},
				Bounds:    protocol.Field[protocol.ReadBounds]{Set: true, Value: testBounds(1, 2, 3, 4)},
				Enabled:   protocol.Field[*bool]{Set: true},
				Focused:   protocol.Field[*bool]{Set: true, Value: testBoolPtr(true)},
				Selected:  protocol.Field[*bool]{Set: true},
				Checked:   protocol.Field[*bool]{Set: true},
				Value:     protocol.Field[*string]{Set: true},
				Hint:      protocol.Field[*string]{Set: true},
				Protected: protocol.Field[*bool]{Set: true, Value: testBoolPtr(true)},
				Axis:      protocol.Field[*protocol.Axis]{Set: true},
				Resource:  protocol.Field[*string]{Set: true},
			},
			"n3": {
				Bounds: protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.HiddenBounds()},
			},
		},
	}
	result := &protocol.Result{Observation: &protocol.Observation{
		ID: "O9", Tree: &protocol.ReadTreeValue{Delta: delta},
	}}
	block := mustRenderTreeText(t, result)
	for _, want := range []string{
		"~ n1 label=[] actions=[] bounds=null enabled=false focused=false selected=false checked=true value=\"\" hint=\"h\" protected=false axis=y resource=\"r\"",
		"~ n2 label=null actions=null bounds=[1,2,3,4] enabled=null focused=true selected=null checked=null value=null hint=null protected=true axis=null resource=null",
		"~ n3 bounds=hidden",
		"end tree\n",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q in:\n%s", want, block)
		}
	}
	parsed := parseDeltaText(t, block)
	if got, want := mustJSONValue(t, parsed), mustJSONValue(t, delta); !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit delta did not round-trip:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
}

func TestTreeTextV4FieldInverse(t *testing.T) {
	block := `tree v4 full nodes=2

n1 root 0,0,1080,2400
 n2 group -10,-20,30,40 "label @ !tap _72" value="v" hint="h" long_press text swipe enabled=false focused=true selected=null protected=false checked=true axis=both resource="r" context
end tree
`
	parsed, err := parseFullTreeTextValue(block, "Oabc_72")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	child := testNode("n2", protocol.RoleGroup, testBounds(-10, -20, 30, 40))
	child.Label = []string{"label @ !tap _72"}
	child.Value = testStringPtr("v")
	child.Hint = testStringPtr("h")
	child.Actions = []protocol.Action{
		protocol.ActionLongPress, protocol.ActionText, protocol.ActionSwipe,
	}
	child.Enabled = testBoolPtr(false)
	child.Focused = testBoolPtr(true)
	child.Selected = nil
	child.Protected = testBoolPtr(false)
	child.Checked = testBoolPtr(true)
	child.Axis = protocol.AxisBoth
	child.Resource = testStringPtr("r")
	child.Context = true
	root.Children = []*protocol.ReadNode{child}
	want := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
	}
	if got, wantJSON := mustJSON(t, parsed), mustJSON(t, want); got != wantJSON {
		t.Fatalf("field inverse mismatch:\n got %s\nwant %s", got, wantJSON)
	}
}

func TestTreeTextV4DefaultActionInverse(t *testing.T) {
	cases := []struct {
		name   string
		column string
		want   []protocol.Action
	}{
		{"button-default", "button 0,0,10,10", []protocol.Action{protocol.ActionTap}},
		{"button-insert-before-long-press", "button 0,0,10,10 long_press", []protocol.Action{protocol.ActionTap, protocol.ActionLongPress}},
		{"button-capability-missing", "button 0,0,10,10 long_press !tap", []protocol.Action{protocol.ActionLongPress}},
		{"input-capability-missing", "input 0,0,10,10 tap !text", []protocol.Action{protocol.ActionTap}},
		{"scroll-capability-missing", "scroll 0,0,10,10 long_press !swipe", []protocol.Action{protocol.ActionLongPress}},
		{"explicit-default", "input 0,0,10,10 text", []protocol.Action{protocol.ActionText}},
		{"duplicate-explicit-default", "button 0,0,10,10 tap tap long_press", []protocol.Action{
			protocol.ActionTap, protocol.ActionTap, protocol.ActionLongPress,
		}},
		{"duplicate-with-negation", "button 0,0,10,10 long_press long_press !tap", []protocol.Action{
			protocol.ActionLongPress, protocol.ActionLongPress,
		}},
		{"duplicate-without-default-role", "group 0,0,10,10 text text", []protocol.Action{
			protocol.ActionText, protocol.ActionText,
		}},
		{"all-explicit", "button 0,0,10,10 tap long_press text swipe", []protocol.Action{
			protocol.ActionTap, protocol.ActionLongPress, protocol.ActionText, protocol.ActionSwipe,
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			block := "tree v4 full nodes=2\n\nn1 root 0,0,100,100\n n2 " +
				testCase.column + "\nend tree\n"
			tree, err := parseFullTreeTextValue(block, "O1")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := tree.Root.Children[0].Actions; !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("actions = %v, want %v", got, testCase.want)
			}
		})
	}
	for _, column := range []string{
		"button 0,0,10,10 tap !tap",
		"input 0,0,10,10 !tap",
		"group 0,0,10,10 !tap",
		"button 0,0,10,10 long_press long_press",
		"button 0,0,10,10 long_press !text",
		"button 0,0,10,10 !tap tap",
		"button 0,0,10,10 !tap !tap",
	} {
		t.Run("reject/"+column, func(t *testing.T) {
			block := "tree v4 full nodes=2\n\nn1 root 0,0,100,100\n n2 " +
				column + "\nend tree\n"
			if _, err := parseFullTreeTextValue(block, "O1"); err == nil {
				t.Fatalf("invalid default-action spelling was accepted: %s", column)
			}
		})
	}
}

func TestTreeTextV4PreservesDuplicateExplicitActions(t *testing.T) {
	button := testNode("n2", protocol.RoleButton, testBounds(0, 0, 10, 10))
	button.Actions = []protocol.Action{
		protocol.ActionTap, protocol.ActionTap, protocol.ActionLongPress,
	}
	scroll := testNode("n3", protocol.RoleScroll, testBounds(0, 20, 10, 40))
	scroll.Actions = []protocol.Action{
		protocol.ActionLongPress, protocol.ActionLongPress,
	}
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 100, 100))
	root.Children = []*protocol.ReadNode{button, scroll}
	tree := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
	}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.ReadTreeValue{Full: tree},
	}})
	for _, want := range []string{
		" n2 button 0,0,10,10 tap tap long_press\n",
		" n3 scroll 0,20,10,40 long_press long_press !swipe\n",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("duplicate explicit actions were not preserved with %q:\n%s", want, block)
		}
	}
	parsed, err := parseFullTreeTextValue(block, "O1")
	if err != nil {
		t.Fatalf("parse duplicate actions: %v", err)
	}
	if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
		t.Fatalf("duplicate-action round trip mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestTreeTextV4StrictGeometryAndCounts(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{"full-count-too-large", "tree v4 full nodes=3\n\nn1 root 0,0,1,1\n n2 group 0,0,1,1\nend tree\n"},
		{"full-count-nonpositive", "tree v4 full nodes=0\n\nn1 root 0,0,1,1\nend tree\n"},
		{"full-count-leading-zero", "tree v4 full nodes=02\n\nn1 root 0,0,1,1\n n2 group 0,0,1,1\nend tree\n"},
		{"duplicate-header-field", "tree v4 full nodes=1 nodes=1\n\nn1 root 0,0,1,1\nend tree\n"},
		{"old-bracket-geometry", "tree v4 full nodes=1\n\nn1 root [0,0,1,1]\nend tree\n"},
		{"two-space-indent", "tree v4 full nodes=2\n\nn1 root 0,0,1,1\n  n2 group 0,0,1,1\nend tree\n"},
		{"indent-skip", "tree v4 full nodes=2\n\nn1 root 0,0,1,1\n  n2 group 0,0,1,1\nend tree\n"},
		{"leading-zero-coordinate", "tree v4 full nodes=1\n\nn1 root 01,0,1,1\nend tree\n"},
		{"negative-zero-coordinate", "tree v4 full nodes=1\n\nn1 root -0,0,1,1\nend tree\n"},
		{"reversed-rectangle", "tree v4 full nodes=1\n\nn1 root 2,0,1,1\nend tree\n"},
		{"duplicate-node-ref", "tree v4 full nodes=2\n\nn1 root 0,0,1,1\n n1 group 0,0,1,1\nend tree\n"},
		{"orphan-at", "tree v4 full nodes=1\n\nn1 root @\nend tree\n"},
		{"delta-count", "tree v4 delta base=O1 changes=2\n\n~ n2 label=[\"x\"]\nend tree\n"},
		{"delta-cross-definition-at", "tree v4 delta base=O1 changes=2\n\n+ n2 group 0,0,1,1\n+ n3 group @\nend tree\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var err error
			if strings.Contains(testCase.block, " delta ") {
				_, err = parseDeltaTextValue(testCase.block, "O2")
			} else {
				_, err = parseFullTreeTextValue(testCase.block, "O2")
			}
			if err == nil {
				t.Fatalf("invalid v4 tree was accepted:\n%s", testCase.block)
			}
		})
	}
}

func TestTreeTextV4SameBaseIndependentDeltas(t *testing.T) {
	baseChild := testNode("n2", protocol.RoleText, testBounds(0, 0, 100, 20))
	baseChild.Label = []string{"base"}
	baseRoot := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 100, 100))
	baseRoot.Children = []*protocol.ReadNode{baseChild}
	base := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: baseRoot,
	}
	add := mustParseDeltaFixture(t, `tree v4 delta base=_72 changes=2

+ n3 button 0,30,20,50 "added"
children n1 = [n2 n3]
end tree
`, "Oabc_76")
	update := mustParseDeltaFixture(t, `tree v4 delta base=_72 changes=1

~ n2 label=["updated"]
end tree
`, "Oabc_76")
	if add.Base != "Oabc_72" || update.Base != "Oabc_72" {
		t.Fatalf("independent bases = %q and %q", add.Base, update.Base)
	}
	original := mustJSON(t, base)
	first := cloneReadTree(t, base)
	firstApplied, err := observation.ReadApply(first, add)
	if err != nil {
		t.Fatalf("apply add: %v", err)
	}
	second := cloneReadTree(t, base)
	secondApplied, err := observation.ReadApply(second, update)
	if err != nil {
		t.Fatalf("apply update: %v", err)
	}
	firstTree := observation.UnpackTree(firstApplied)
	secondTree := observation.UnpackTree(secondApplied)
	if got := len(firstTree.Root.Children); got != 2 {
		t.Fatalf("add result children = %d, want 2", got)
	}
	if got := firstTree.Root.Children[0].Label; !reflect.DeepEqual(got, []string{"base"}) {
		t.Fatalf("add delta leaked the update label: %v", got)
	}
	if got := len(secondTree.Root.Children); got != 1 {
		t.Fatalf("update result children = %d, want 1", got)
	}
	if got := secondTree.Root.Children[0].Label; !reflect.DeepEqual(got, []string{"updated"}) {
		t.Fatalf("update label = %v", got)
	}
	if got := mustJSON(t, base); got != original {
		t.Fatalf("application mutated the shared base:\n got %s\nwant %s", got, original)
	}
}

func mustParseDeltaFixture(t *testing.T, block, currentO string) *protocol.ReadDelta {
	t.Helper()
	delta, err := parseDeltaTextValue(block, currentO)
	if err != nil {
		t.Fatalf("parse delta: %v", err)
	}
	return delta
}

func cloneReadTree(t *testing.T, tree *protocol.ReadTree) *protocol.ReadTree {
	t.Helper()
	raw, err := protocol.JSON(tree)
	if err != nil {
		t.Fatalf("marshal tree: %v", err)
	}
	var clone protocol.ReadTree
	if err := protocol.Decode(raw, &clone); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	return &clone
}

// TestTreeTextChainDefinition pins the v4 explicit chain definition: consecutive
// anonymous single-child nodes fold into one definition line whose ref list
// expands to distinct logical nodes in nodes=. The refs are not re-expanded as
// separate lines; entries= counts definition lines only.
func TestTreeTextChainDefinition(t *testing.T) {
	title := testNode("n4", protocol.RoleText, testBounds(0, 0, 1080, 60))
	title.Label = []string{"标题"}
	chain := &protocol.ReadNode{
		Chain:   []string{"n2", "n3"},
		Role:    protocol.RoleView,
		Bounds:  testBounds(0, 0, 1080, 120),
		Label:   []string{},
		Actions: []protocol.Action{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
		Checked:  testBoolPtr(false),
		Children: []*protocol.ReadNode{title},
	}
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	root.Children = []*protocol.ReadNode{chain}
	tree := &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}

	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O5", Tree: &protocol.ReadTreeValue{Full: tree},
	}})
	want := "tree v4 full nodes=4\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" chain [n2 n3] view 0,0,1080,120\n" +
		"  n4 text 0,0,1080,60 \"标题\"\nend tree\n"
	if block != want {
		t.Fatalf("chain block mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, want)
	}
	if got, want := headerInt(t, parseTreeTextBlock(t, block).header, "nodes"), 4; got != want {
		t.Fatalf("chain nodes = %d, want %d", got, want)
	}
	parsed := parseFullTreeText(t, block)
	if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
		t.Fatalf("chain definition did not round-trip:\n got %s\nwant %s", got, want)
	}
}

// TestTreeTextLegacyChainTailInDeltaAddAndReplace keeps v4 chain tails bound to
// the last ref, including inside independent add and replace definitions.
func TestTreeTextLegacyChainTailInDeltaAddAndReplace(t *testing.T) {
	addedButton := testNode("n23", protocol.RoleButton, testBounds(0, 0, 20, 20))
	addedButton.Label = []string{"增加"}
	addedButton.Actions = []protocol.Action{protocol.ActionTap}
	addedChain := testNode("", protocol.RoleView, protocol.HiddenBounds())
	addedChain.Chain = []string{"n21", "n22"}
	addedChain.Children = []*protocol.ReadNode{addedButton}
	addedRoot := testNode("n20", protocol.RoleView, protocol.HiddenBounds())
	addedRoot.Children = []*protocol.ReadNode{addedChain}

	replacedText := testNode("n33", protocol.RoleText, testBounds(0, 0, 40, 20))
	replacedText.Label = []string{"替换"}
	replacedChain := testNode("", protocol.RoleGroup, protocol.HiddenBounds())
	replacedChain.Chain = []string{"n31", "n32"}
	replacedChain.Children = []*protocol.ReadNode{replacedText}
	replacedRoot := testNode("n30", protocol.RoleGroup, protocol.HiddenBounds())
	replacedRoot.Children = []*protocol.ReadNode{replacedChain}

	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Add:     []*protocol.ReadNode{addedRoot},
		Replace: map[string]*protocol.ReadNode{"n30": replacedRoot},
	}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O2", Tree: &protocol.ReadTreeValue{Delta: delta},
	}})
	for _, wanted := range []string{
		"+ n20\n chain [n21 n22] view\n  n23 button 0,0,20,20 \"增加\"\n",
		"= n30 group\n chain [n31 n32] group\n  n33 text 0,0,40,20 \"替换\"\n",
	} {
		if !strings.Contains(block, wanted) {
			t.Fatalf("legacy chain delta lacks %q:\n%s", wanted, block)
		}
	}
	parsed := parseDeltaText(t, block)
	if got, want := mustJSON(t, parsed), mustJSON(t, delta); got != want {
		t.Fatalf("legacy chain delta did not round-trip:\n got %s\nwant %s", got, want)
	}
	if len(parsed.Add) != 1 || len(parsed.Add[0].Children) != 1 ||
		len(parsed.Add[0].Children[0].Chain) != 2 ||
		len(parsed.Add[0].Children[0].Children) != 1 {
		t.Fatalf("add chain tail children were not preserved: %#v", parsed.Add)
	}
	if replaced := parsed.Replace["n30"]; replaced == nil ||
		len(replaced.Children) != 1 || len(replaced.Children[0].Chain) != 2 ||
		len(replaced.Children[0].Children) != 1 {
		t.Fatalf("replace chain tail children were not preserved: %#v", replaced)
	}
}

func TestTreeTextV4OmitsViewRoles(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*protocol.ReadNode)
	}{
		{"no-fields", nil},
		{"rectangle", func(node *protocol.ReadNode) {
			node.Bounds = testBounds(1, 2, 3, 4)
		}},
		{"unknown-geometry", func(node *protocol.ReadNode) {
			node.Bounds = protocol.UnknownBounds()
		}},
		{"enabled-false", func(node *protocol.ReadNode) {
			node.Enabled = testBoolPtr(false)
		}},
		{"enabled-null", func(node *protocol.ReadNode) {
			node.Enabled = nil
		}},
		{"focused-true", func(node *protocol.ReadNode) {
			node.Focused = testBoolPtr(true)
		}},
		{"selected-true", func(node *protocol.ReadNode) {
			node.Selected = testBoolPtr(true)
		}},
		{"protected-true", func(node *protocol.ReadNode) {
			node.Protected = testBoolPtr(true)
		}},
		{"checked-true", func(node *protocol.ReadNode) {
			node.Checked = testBoolPtr(true)
		}},
		{"checked-null", func(node *protocol.ReadNode) {
			node.Checked = nil
		}},
		{"value", func(node *protocol.ReadNode) {
			node.Value = testStringPtr("")
		}},
		{"hint", func(node *protocol.ReadNode) {
			node.Hint = testStringPtr("hint")
		}},
		{"tap", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionTap}
		}},
		{"long-press", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionLongPress}
		}},
		{"swipe", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionSwipe}
		}},
		{"axis", func(node *protocol.ReadNode) {
			node.Axis = protocol.AxisY
		}},
		{"resource", func(node *protocol.ReadNode) {
			node.Resource = testStringPtr("res")
		}},
		{"context", func(node *protocol.ReadNode) {
			node.Context = true
		}},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			child := testNode("n2", protocol.RoleView, protocol.HiddenBounds())
			if testCase.mutate != nil {
				testCase.mutate(child)
			}
			root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
			root.Children = []*protocol.ReadNode{child}
			tree := &protocol.ReadTree{
				Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
			}
			block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
				ID: "O1", Tree: &protocol.ReadTreeValue{Full: tree},
			}})
			line := treeTextLineForRef(t, block, "n2")
			tokens := tokenizeTreeTextLine(strings.TrimLeft(line, " "))
			if len(tokens) == 0 || tokens[0] != "n2" {
				t.Fatalf("unexpected view line: %q", line)
			}
			if strings.TrimRight(line, " ") != line {
				t.Fatalf("view line has trailing spaces: %q", line)
			}
			if testCase.name == "no-fields" && line != " n2" {
				t.Fatalf("fieldless view line = %q, want a direct newline after the ref", line)
			}
			if len(tokens) > 1 && protocol.Role(tokens[1]).Valid() {
				t.Fatalf("view role was not omitted: %q", line)
			}
			parsed := parseFullTreeText(t, block)
			if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
				t.Fatalf("view omission did not round-trip:\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestTreeTextV4RoleRecoveryMatrix(t *testing.T) {
	type firstColumn struct {
		name             string
		mutate           func(*protocol.ReadNode)
		quotedLabel      bool
		roleWordIfHidden bool
	}
	firstColumns := []firstColumn{
		{"label-view", func(node *protocol.ReadNode) {
			node.Label = []string{"view"}
		}, true, false},
		{"label-text", func(node *protocol.ReadNode) {
			node.Label = []string{"text"}
		}, true, false},
		{"label-empty", func(node *protocol.ReadNode) {
			node.Label = []string{""}
		}, true, false},
		{"value", func(node *protocol.ReadNode) {
			node.Value = testStringPtr("")
		}, false, false},
		{"hint", func(node *protocol.ReadNode) {
			node.Hint = testStringPtr("hint")
		}, false, false},
		{"tap", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionTap}
		}, false, false},
		{"swipe", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionSwipe}
		}, false, false},
		{"long-press", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionLongPress}
		}, false, false},
		{"action-text", func(node *protocol.ReadNode) {
			node.Actions = []protocol.Action{protocol.ActionText}
		}, false, true},
		{"value-then-action-text", func(node *protocol.ReadNode) {
			node.Value = testStringPtr("")
			node.Actions = []protocol.Action{protocol.ActionText}
		}, false, false},
		{"state-word", func(node *protocol.ReadNode) {
			node.Enabled = nil
		}, false, false},
		{"context", func(node *protocol.ReadNode) {
			node.Context = true
		}, false, false},
		{"line-end", nil, false, false},
	}
	geometries := []struct {
		name   string
		bounds protocol.ReadBounds
	}{
		{"hidden", protocol.HiddenBounds()},
		{"unknown", protocol.UnknownBounds()},
		{"known", testBounds(1, 2, 3, 4)},
	}
	roles := []protocol.Role{
		protocol.RoleRoot, protocol.RoleGroup, protocol.RoleText, protocol.RoleButton,
		protocol.RoleInput, protocol.RoleCheckbox, protocol.RoleRadio, protocol.RoleSwitch,
		protocol.RoleToggle, protocol.RoleScroll, protocol.RoleImage, protocol.RoleView,
	}
	for _, role := range roles {
		for _, geometry := range geometries {
			for _, firstColumn := range firstColumns {
				caseName := string(role) + "/" + geometry.name + "/" + firstColumn.name
				t.Run(caseName, func(t *testing.T) {
					child := testNode("n2", role, geometry.bounds)
					if firstColumn.mutate != nil {
						firstColumn.mutate(child)
					}
					root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
					root.Children = []*protocol.ReadNode{child}
					tree := &protocol.ReadTree{
						Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
					}
					block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
						ID: "O1", Tree: &protocol.ReadTreeValue{Full: tree},
					}})
					line := treeTextLineForRef(t, block, "n2")
					tokens := tokenizeTreeTextLine(strings.TrimLeft(line, " "))
					if len(tokens) == 0 || tokens[0] != "n2" {
						t.Fatalf("unexpected node line: %q", line)
					}
					gotExplicit := len(tokens) > 1 && protocol.Role(tokens[1]).Valid()
					defaultRole := protocol.RoleView
					if geometry.bounds.Kind == protocol.GeometryHidden && firstColumn.quotedLabel {
						defaultRole = protocol.RoleText
					}
					firstIsRole := geometry.bounds.Kind == protocol.GeometryHidden &&
						firstColumn.roleWordIfHidden
					wantExplicit := firstIsRole || role != defaultRole
					if gotExplicit != wantExplicit {
						t.Fatalf("explicit role = %t, want %t: %q", gotExplicit, wantExplicit, line)
					}
					parsed := parseFullTreeText(t, block)
					if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
						t.Fatalf("role matrix did not round-trip:\n got %s\nwant %s", got, want)
					}
				})
			}
		}
	}
}

func TestTreeTextV4ChainAndGeometry(t *testing.T) {
	chainNode := func(refs []string, role protocol.Role, bounds protocol.ReadBounds) *protocol.ReadNode {
		node := testNode("", role, bounds)
		node.Chain = refs
		return node
	}
	anonymous := chainNode([]string{"n10", "n11"}, protocol.RoleView, protocol.HiddenBounds())
	anonymous.Children = []*protocol.ReadNode{func() *protocol.ReadNode {
		child := testNode("n12", protocol.RoleText, protocol.HiddenBounds())
		child.Label = []string{"child"}
		return child
	}()}
	group := chainNode([]string{"n20", "n21"}, protocol.RoleGroup, protocol.HiddenBounds())
	known := chainNode([]string{"n30", "n31"}, protocol.RoleView, testBounds(1, 2, 3, 4))
	unknown := chainNode([]string{"n32", "n33"}, protocol.RoleView, protocol.UnknownBounds())
	context := chainNode([]string{"n34", "n35"}, protocol.RoleView, protocol.HiddenBounds())
	context.Context = true
	enabledNull := chainNode([]string{"n36", "n37"}, protocol.RoleView, protocol.HiddenBounds())
	enabledNull.Enabled = nil
	checkedNull := chainNode([]string{"n38", "n39"}, protocol.RoleView, protocol.HiddenBounds())
	checkedNull.Checked = nil
	label := chainNode([]string{"n40", "n41"}, protocol.RoleView, protocol.HiddenBounds())
	label.Label = []string{"chain label"}

	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	root.Children = []*protocol.ReadNode{
		anonymous, group, known, unknown, context, enabledNull, checkedNull, label,
	}
	tree := &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.ReadTreeValue{Full: tree},
	}})
	want := "tree v4 full nodes=18\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" chain [n10 n11] view\n" +
		"  n12 \"child\"\n" +
		" chain [n20 n21] group\n" +
		" chain [n30 n31] view 1,2,3,4\n" +
		" chain [n32 n33] view -\n" +
		" chain [n34 n35] view context\n" +
		" chain [n36 n37] view enabled=null\n" +
		" chain [n38 n39] view checked=null\n" +
		" chain [n40 n41] view \"chain label\"\n" +
		"end tree\n"
	if block != want {
		t.Fatalf("chain rendering mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, want)
	}
	parsedBlock := parseTreeTextBlock(t, block)
	if got, want := headerInt(t, parsedBlock.header, "nodes"), 18; got != want {
		t.Fatalf("chain nodes = %d, want %d", got, want)
	}
	parsed := parseFullTreeText(t, block)
	if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
		t.Fatalf("chain definitions did not round-trip:\n got %s\nwant %s", got, want)
	}
}

func TestTreeTextV4ChainDelta(t *testing.T) {
	chainNode := func(refs []string, role protocol.Role, bounds protocol.ReadBounds) *protocol.ReadNode {
		node := testNode("", role, bounds)
		node.Chain = refs
		return node
	}
	addRoot := testNode("n50", protocol.RoleGroup, testBounds(0, 0, 100, 100))
	addChain := chainNode([]string{"n51", "n52"}, protocol.RoleView, protocol.HiddenBounds())
	addChild := testNode("n53", protocol.RoleText, protocol.HiddenBounds())
	addChild.Label = []string{"moved"}
	addChain.Children = []*protocol.ReadNode{addChild}
	addRoot.Children = []*protocol.ReadNode{addChain}

	replaceRoot := testNode("n60", protocol.RoleButton, testBounds(0, 0, 10, 10))
	replaceChain := chainNode([]string{"n61", "n62"}, protocol.RoleGroup, protocol.HiddenBounds())
	replaceRoot.Children = []*protocol.ReadNode{replaceChain}

	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Add:      []*protocol.ReadNode{addRoot},
		Remove:   []string{"n70"},
		Children: map[string][]string{"n60": {"n61", "n62"}},
		Replace:  map[string]*protocol.ReadNode{"n60": replaceRoot},
	}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O2", Tree: &protocol.ReadTreeValue{Delta: delta},
	}})
	want := "tree v4 delta base=O1 changes=4\n\n" +
		"+ n50 group 0,0,100,100\n" +
		" chain [n51 n52] view\n" +
		"  n53 \"moved\"\n" +
		"- n70\n" +
		"children n60 = [n61 n62]\n" +
		"= n60 button 0,0,10,10 !tap\n" +
		" chain [n61 n62] group\n" +
		"end tree\n"
	if block != want {
		t.Fatalf("delta chain rendering mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, want)
	}
	parsed := parseDeltaText(t, block)
	if got, want := mustJSONValue(t, parsed), mustJSONValue(t, delta); !reflect.DeepEqual(got, want) {
		t.Fatalf("delta chain definitions did not round-trip:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
	if got := parsed.Add[0].Children[0]; got == nil || !reflect.DeepEqual(got.Chain, []string{"n51", "n52"}) ||
		len(got.Children) != 1 || got.Children[0].Ref != "n53" {
		t.Fatalf("add chain lost refs or child ownership: %#v", got)
	}
	if got := parsed.Replace["n60"].Children[0]; got == nil ||
		!reflect.DeepEqual(got.Chain, []string{"n61", "n62"}) {
		t.Fatalf("replace chain lost refs: %#v", got)
	}
	if got, want := headerInt(t, parseTreeTextBlock(t, block).header, "changes"), 4; got != want {
		t.Fatalf("delta changes = %d, want %d", got, want)
	}
}

func TestTreeTextV4CompactBytesWin(t *testing.T) {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	view := testNode("n2", protocol.RoleView, protocol.HiddenBounds())
	text := testNode("n3", protocol.RoleText, protocol.HiddenBounds())
	text.Label = []string{"title"}
	chain := testNode("", protocol.RoleView, protocol.HiddenBounds())
	chain.Chain = []string{"n10", "n11"}
	child := testNode("n12", protocol.RoleText, testBounds(1, 2, 3, 4))
	child.Label = []string{"child"}
	chain.Children = []*protocol.ReadNode{child}
	tap := testNode("n4", protocol.RoleView, testBounds(0, 0, 10, 10))
	tap.Actions = []protocol.Action{protocol.ActionTap}
	root.Children = []*protocol.ReadNode{view, text, chain, tap}
	tree := &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.ReadTreeValue{Full: tree},
	}})
	if !strings.Contains(block, "\n chain [n10 n11] view\n") || !strings.Contains(block, "\n n2\n") {
		t.Fatalf("fixture does not exercise v4 omissions:\n%s", block)
	}
	expanded := expandTreeTextRoles(block)
	if len(block) >= len(expanded) {
		t.Fatalf("compact block = %d bytes, explicit comparison = %d bytes", len(block), len(expanded))
	}
}

func TestTreeTextDeltaReplayAndSetPresence(t *testing.T) {
	baseNode := testNode("n2", protocol.RoleText, protocol.HiddenBounds())
	baseNode.Label = []string{"base"}
	baseNode.Value = testStringPtr("keep-value")
	baseNode.Hint = testStringPtr("keep-hint")
	baseNode.Axis = protocol.AxisY
	baseNode.Resource = testStringPtr("keep-resource")
	baseRoot := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	baseRoot.Children = []*protocol.ReadNode{baseNode}
	base := &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: baseRoot}

	addRoot := testNode("n3", protocol.RoleGroup, testBounds(0, 100, 1080, 200))
	chain := testNode("", protocol.RoleView, protocol.HiddenBounds())
	chain.Chain = []string{"n10", "n11"}
	chainChild := testNode("n12", protocol.RoleText, protocol.HiddenBounds())
	chainChild.Label = []string{"new"}
	chain.Children = []*protocol.ReadNode{chainChild}
	addRoot.Children = []*protocol.ReadNode{chain}
	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n2": {
				Label:   protocol.Field[[]string]{Set: true, Value: []string{}},
				Actions: protocol.Field[[]protocol.Action]{Set: true, Value: []protocol.Action{}},
				Bounds:  protocol.Field[protocol.ReadBounds]{Set: true, Value: testBounds(0, 0, 0, 0)},
				Enabled: protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)},
				Checked: protocol.Field[*bool]{Set: true},
				Value:   protocol.Field[*string]{Set: true, Value: testStringPtr("")},
				Resource: protocol.Field[*string]{
					Set: true,
				},
			},
		},
		Add:      []*protocol.ReadNode{addRoot},
		Children: map[string][]string{"n1": {"n2", "n3"}},
	}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O2", Tree: &protocol.ReadTreeValue{Delta: delta},
	}})
	parsed := parseDeltaText(t, block)
	if got, want := mustJSONValue(t, parsed), mustJSONValue(t, delta); !reflect.DeepEqual(got, want) {
		t.Fatalf("delta replay fixture did not round-trip:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
	applied, err := observation.ReadApply(base, parsed)
	if err != nil {
		t.Fatalf("ReadApply: %v", err)
	}
	logical := observation.UnpackTree(applied)
	if logical == nil || logical.Root == nil || len(logical.Root.Children) != 2 {
		t.Fatalf("replayed tree children = %#v", logical)
	}
	replayed := logical.Root.Children[0]
	if replayed.Ref != "n2" {
		t.Fatalf("replayed first child = %q, want n2", replayed.Ref)
	}
	if len(replayed.Label) != 0 || len(replayed.Actions) != 0 {
		t.Fatalf("empty set values were lost: %#v", replayed)
	}
	if replayed.Bounds.Kind != protocol.GeometryKnown ||
		replayed.Bounds.Rect != (protocol.Bounds{}) {
		t.Fatalf("zero rectangle was not preserved: %#v", replayed.Bounds)
	}
	if replayed.Enabled == nil || *replayed.Enabled {
		t.Fatalf("enabled=false was not preserved: %#v", replayed.Enabled)
	}
	if replayed.Checked != nil {
		t.Fatalf("checked=null was not preserved: %#v", replayed.Checked)
	}
	if replayed.Value == nil || *replayed.Value != "" {
		t.Fatalf("value=\"\" was not preserved: %#v", replayed.Value)
	}
	if replayed.Resource != nil {
		t.Fatalf("resource=null was not preserved: %#v", replayed.Resource)
	}
	if replayed.Hint == nil || *replayed.Hint != "keep-hint" ||
		replayed.Axis != protocol.AxisY || replayed.Focused == nil || *replayed.Focused {
		t.Fatalf("unset set fields changed: %#v", replayed)
	}
	if len(logical.Root.Children[1].Children) != 1 {
		t.Fatalf("added chain child ownership changed: %#v", logical.Root.Children[1])
	}
}

func TestTreeTextInspectTree(t *testing.T) {
	result := &protocol.Result{Inspection: testInspectionTreeView()}
	if block := mustRenderTreeText(t, result); block != testTreeTextInspectTreeGolden {
		t.Fatalf("inspect tree mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextInspectTreeGolden)
	}
}

func TestTreeTextInspectSubtree(t *testing.T) {
	result := &protocol.Result{Inspection: testInspectionSubtreeView()}
	if block := mustRenderTreeText(t, result); block != testTreeTextInspectSubtreeGolden {
		t.Fatalf("inspect subtree mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextInspectSubtreeGolden)
	}
}

func TestTreeTextInspectQuery(t *testing.T) {
	result := &protocol.Result{Inspection: testInspectionQueryView()}
	if block := mustRenderTreeText(t, result); block != testTreeTextInspectQueryGolden {
		t.Fatalf("inspect query mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextInspectQueryGolden)
	}
}

func TestTreeTextInspectV3PreservesChains(t *testing.T) {
	anonymous := testNode("", protocol.RoleView, testBounds(1, 2, 3, 4))
	anonymous.Chain = []string{"n10", "n11"}
	child := testNode("n12", protocol.RoleText, testBounds(1, 2, 3, 4))
	child.Label = []string{"child"}
	anonymous.Children = []*protocol.ReadNode{child}

	group := testNode("", protocol.RoleGroup, testBounds(0, 0, 10, 10))
	group.Chain = []string{"n20", "n21"}
	group.Actions = []protocol.Action{protocol.ActionLongPress}

	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 100, 100))
	root.Children = []*protocol.ReadNode{anonymous, group}
	view := &protocol.InspectionView{
		ObservationID: "O1", Origin: "P1", Revision: 1,
		CapturedAt: "2026-11-21T00:00:00Z", Scope: "tree",
		Tree: &protocol.ReadTree{
			Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed, Root: root,
		},
	}
	block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
	for _, want := range []string{
		"\n  chain [n10 n11] view [1,2,3,4]\n",
		"\n  chain [n20 n21] group [0,0,10,10] long_press\n",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("inspect v3 chain block lacks %q:\n%s", want, block)
		}
	}
	parsed := parseInspectionText(t, block)
	want := *view
	want.Origin = ""
	if got, wantJSON := mustJSON(t, parsed), mustJSON(t, &want); got != wantJSON {
		t.Fatalf("inspect v3 chain round trip mismatch:\n got %s\nwant %s", got, wantJSON)
	}
}

func TestTreeTextIsPure(t *testing.T) {
	results := []*protocol.Result{
		{Observation: &protocol.Observation{ID: "O3", Tree: &protocol.ReadTreeValue{Full: testFullTree()}}},
		{Observation: &protocol.Observation{ID: "O4", Tree: &protocol.ReadTreeValue{Delta: testDelta()}}},
		{Inspection: testInspectionQueryView()},
	}
	for index, result := range results {
		first := mustRenderTreeText(t, result)
		for attempt := 0; attempt < 100; attempt++ {
			if again := mustRenderTreeText(t, result); again != first {
				t.Fatalf("result %d is not pure on attempt %d", index, attempt)
			}
		}
		encoded, err := protocol.JSON(result)
		if err != nil {
			t.Fatalf("result %d marshal: %v", index, err)
		}
		var restored protocol.Result
		if err := protocol.Decode(encoded, &restored); err != nil {
			t.Fatalf("result %d decode: %v", index, err)
		}
		if roundTrip := mustRenderTreeText(t, &restored); roundTrip != first {
			t.Fatalf("result %d changed after JSON round-trip:\n--- first ---\n%s\n--- again ---\n%s", index, first, roundTrip)
		}
	}
}

func TestTreeTextRoundTrip(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		tree := testFullTree()
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O3", Tree: &protocol.ReadTreeValue{Full: tree},
		}})
		parsed := parseFullTreeText(t, block)
		if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
			t.Fatalf("full round-trip mismatch:\n got %s\nwant %s", got, want)
		}
	})
	t.Run("delta", func(t *testing.T) {
		delta := testDelta()
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O4", Tree: &protocol.ReadTreeValue{Delta: delta},
		}})
		parsed := parseDeltaText(t, block)
		if got, want := mustJSONValue(t, parsed), mustJSONValue(t, delta); !reflect.DeepEqual(got, want) {
			t.Fatalf("delta round-trip mismatch:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
		}
	})
	for _, testCase := range []struct {
		name string
		view *protocol.InspectionView
	}{
		{"tree", testInspectionTreeView()},
		{"subtree", testInspectionSubtreeView()},
		{"query", testInspectionQueryView()},
	} {
		testCase := testCase
		t.Run("inspect/"+testCase.name, func(t *testing.T) {
			block := mustRenderTreeText(t, &protocol.Result{Inspection: testCase.view})
			parsed := parseInspectionText(t, block)
			want := *testCase.view
			want.Origin = ""
			if got, wantJSON := mustJSON(t, parsed), mustJSON(t, &want); got != wantJSON {
				t.Fatalf("inspection round-trip mismatch:\n got %s\nwant %s", got, wantJSON)
			}
		})
	}
}

func TestTreeTextCounts(t *testing.T) {
	full := parseTreeTextBlock(t, testTreeTextFullGolden)
	if got, want := headerInt(t, full.header, "nodes"), countNodeLines(full.body); got != want {
		t.Fatalf("full nodes = %d, want %d", got, want)
	}

	delta := parseTreeTextBlock(t, testTreeTextDeltaGolden)
	if got, want := headerInt(t, delta.header, "changes"), countDeltaChanges(delta.body); got != want {
		t.Fatalf("delta changes = %d, want %d", got, want)
	}

	query := parseTreeTextBlock(t, testTreeTextInspectQueryGolden)
	if got, want := headerInt(t, query.header, "nodes"), countInspectNodeLines(query.body); got != want {
		t.Fatalf("inspect nodes = %d, want %d", got, want)
	}
	if got, want := headerInt(t, query.header, "entries"), countInspectNodeLines(query.body); got != want {
		t.Fatalf("inspect entries = %d, want %d", got, want)
	}
}

func TestTreeTextEscapes(t *testing.T) {
	label := "quote\" slash\\ line\n tab\t nul\x00 unit\x1f html<>& cjk中文 bad" + string([]byte{0xff})
	longLabel := strings.Repeat("长🙂", 2048)
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1, 1))
	root.Label = []string{label, "second[label]=value", "emoji🙂", longLabel}
	root.Resource = testStringPtr("res<&>")
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}},
	}})
	parsed := parseTreeTextBlock(t, block)
	if len(parsed.body) != 1 {
		t.Fatalf("escaped label produced %d body lines:\n%s", len(parsed.body), block)
	}
	for _, want := range []string{
		`quote\" slash\\ line\n tab\t nul\u0000 unit\u001f html<>& cjk中文 bad` + "\uFFFD",
		`"second[label]=value"`,
		`"emoji🙂"`,
		`resource="res<&>"`,
		`end tree` + "\n",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q in:\n%s", want, block)
		}
	}
	if strings.Contains(block, "\r") {
		t.Fatalf("block contains CR:\n%s", block)
	}
	roundTrip := parseFullTreeText(t, block)
	if got, want := roundTrip.Root.Label[0], strings.ToValidUTF8(label, "\uFFFD"); got != want {
		t.Fatalf("escaped label did not round-trip: got %q, want %q", got, want)
	}
	if got, want := roundTrip.Root.Label[2], "emoji🙂"; got != want {
		t.Fatalf("non-BMP label did not round-trip: got %q, want %q", got, want)
	}
	if got, want := roundTrip.Root.Label[3], longLabel; got != want {
		t.Fatalf("long label did not round-trip: got %d bytes, want %d", len(got), len(want))
	}
	if got, want := roundTrip.Root.Resource, "res<&>"; got == nil || *got != want {
		t.Fatalf("escaped resource did not round-trip: %v", got)
	}
}

func TestTreeTextKeepsPreviouslyBoundedBlocks(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		tree := testLargeTree(699, 60)
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O3", Tree: &protocol.ReadTreeValue{Full: tree},
		}})
		assertCompleteTreeText(t, block, "tree v4 full nodes=", "end tree\n", 24*1024)
	})

	t.Run("delta", func(t *testing.T) {
		delta := &protocol.ReadDelta{Format: "delta", Base: "O3", View: protocol.ViewCompact, Set: map[string]protocol.ReadSetNode{}}
		for index := 1; index <= 600; index++ {
			delta.Set["n"+strconv.Itoa(index)] = protocol.ReadSetNode{
				Label: protocol.Field[[]string]{Set: true, Value: []string{strings.Repeat("y", 60)}},
			}
		}
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O4", Tree: &protocol.ReadTreeValue{Delta: delta},
		}})
		assertCompleteTreeText(t, block, "tree v4 delta base=O3 ", "end tree\n", 24*1024)
	})

	t.Run("inspect", func(t *testing.T) {
		tree := testLargeTree(799, 60)
		tree.View = protocol.ViewDetailed
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
			Tree: tree,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v3 scope=tree observation=O2 ", "end inspect\n", 32*1024)
	})
}

func TestTreeTextLargeFixtures(t *testing.T) {
	const (
		observationFullBytes  = 128 * 1024
		inspectionFullBytes   = 128 * 1024
		narrowInspectionBytes = 32 * 1024
	)

	t.Run("observation-full-128KiB", func(t *testing.T) {
		tree := testLargeTree(200, 700)
		result := &protocol.Result{Observation: &protocol.Observation{
			ID: "O3", Tree: &protocol.ReadTreeValue{Full: tree},
		}}
		block := mustRenderTreeText(t, result)
		assertCompleteTreeText(t, block, "tree v4 full nodes=", "end tree\n", observationFullBytes)
		if got, want := mustJSON(t, parseFullTreeText(t, block)), mustJSON(t, tree); got != want {
			t.Fatalf("observation full tree did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("observation full tree text bytes=%d", len(block))
	})

	t.Run("inspection-full-128KiB", func(t *testing.T) {
		tree := testLargeTree(200, 700)
		tree.View = protocol.ViewDetailed
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
			Tree: tree,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v3 scope=tree observation=O2 ", "end inspect\n", inspectionFullBytes)
		if got, want := mustJSON(t, parseInspectionText(t, block)), mustJSON(t, view); got != want {
			t.Fatalf("inspection full tree did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("inspection full tree text bytes=%d", len(block))
	})

	t.Run("inspection-subtree-32KiB", func(t *testing.T) {
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
			View: protocol.ViewDetailed,
			Root: testLargeTree(80, 700).Root,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v3 scope=subtree observation=O2 ", "end inspect\n", narrowInspectionBytes)
		if got, want := mustJSON(t, parseInspectionText(t, block)), mustJSON(t, view); got != want {
			t.Fatalf("inspection subtree did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("inspection subtree text bytes=%d", len(block))
	})

	t.Run("inspection-query-32KiB", func(t *testing.T) {
		matches := make([]protocol.InspectionMatch, 0, 80)
		for index := 2; index < 82; index++ {
			match := testNode("n"+strconv.Itoa(index), protocol.RoleText, testBounds(0, 0, 1080, 80))
			match.Label = []string{strings.Repeat("q", 700)}
			matches = append(matches, protocol.InspectionMatch{Node: match, Path: []string{"n1"}})
		}
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "query",
			View:    protocol.ViewDetailed,
			Matches: matches,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v3 scope=query observation=O2 ", "end inspect\n", narrowInspectionBytes)
		if got, want := mustJSON(t, parseInspectionText(t, block)), mustJSON(t, view); got != want {
			t.Fatalf("inspection query did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("inspection query text bytes=%d", len(block))
	})
}

func TestTreeTextRejectsUnrepresentableMetadata(t *testing.T) {
	validTree := func() *protocol.ReadTree {
		return &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
			Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())}
	}
	cases := []struct {
		name   string
		result *protocol.Result
	}{
		{
			name: "missing observation id",
			result: &protocol.Result{Observation: &protocol.Observation{
				Tree: &protocol.ReadTreeValue{Full: validTree()},
			}},
		},
		{
			name: "tree value has both formats",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID:   "O3",
				Tree: &protocol.ReadTreeValue{Full: validTree(), Delta: &protocol.ReadDelta{Format: "delta", Base: "O1", View: protocol.ViewCompact}},
			}},
		},
		{
			name: "full format",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
					Format: "delta", Scope: protocol.Scope, View: protocol.ViewCompact,
					Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds()),
				}},
			}},
		},
		{
			name: "full scope missing",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
					Format: "full", View: protocol.ViewCompact, Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds()),
				}},
			}},
		},
		{
			name: "full scope invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
					Format: "full", Scope: "visible", View: protocol.ViewCompact,
					Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds()),
				}},
			}},
		},
		{
			name: "full view missing",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
					Format: "full", Scope: protocol.Scope,
					Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds()),
				}},
			}},
		},
		{
			name: "full role invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
					Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
					Root: testNode("n1", protocol.Role("card"), protocol.UnknownBounds()),
				}},
			}},
		},
		{
			name: "full action invalid",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())
				root.Actions = []protocol.Action{protocol.Action("hover")}
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
						Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "full axis invalid",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())
				root.Axis = protocol.Axis("diagonal")
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
						Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "full chain single ref",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())
				root.Children = []*protocol.ReadNode{&protocol.ReadNode{
					Chain:  []string{"n2"},
					Role:   protocol.RoleGroup,
					Bounds: protocol.UnknownBounds(),
				}}
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
						Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "full chain carries ref",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())
				root.Children = []*protocol.ReadNode{&protocol.ReadNode{
					Ref:    "n2",
					Chain:  []string{"n2", "n3"},
					Role:   protocol.RoleGroup,
					Bounds: protocol.UnknownBounds(),
				}}
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
						Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "full detailed tree hides geometry",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, protocol.HiddenBounds())
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{
						Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "delta format",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Delta: &protocol.ReadDelta{Format: "full", Base: "O1", View: protocol.ViewCompact}},
			}},
		},
		{
			name: "delta base missing",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Delta: &protocol.ReadDelta{Format: "delta", View: protocol.ViewCompact}},
			}},
		},
		{
			name: "delta base token invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Delta: &protocol.ReadDelta{Format: "delta", Base: "O1 bad", View: protocol.ViewCompact}},
			}},
		},
		{
			name: "delta view missing",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Delta: &protocol.ReadDelta{Format: "delta", Base: "O1"}},
			}},
		},
		{
			name: "delta set action invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.ReadTreeValue{Delta: &protocol.ReadDelta{
					Format: "delta", Base: "O1", View: protocol.ViewCompact,
					Set: map[string]protocol.ReadSetNode{
						"n2": {Actions: protocol.Field[[]protocol.Action]{
							Set: true, Value: []protocol.Action{protocol.Action("hover")},
						}},
					},
				}},
			}},
		},
		{
			name:   "inspection id missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{CapturedAt: "2026-11-10T03:00:00Z", Scope: "query"}},
		},
		{
			name:   "inspection captured at missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{ObservationID: "O2", Scope: "query"}},
		},
		{
			name:   "inspection captured at token invalid",
			result: &protocol.Result{Inspection: &protocol.InspectionView{ObservationID: "O2", CapturedAt: "2026-11-10 03:00", Scope: "query"}},
		},
		{
			name:   "inspection scope missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z"}},
		},
		{
			name:   "inspection scope invalid",
			result: &protocol.Result{Inspection: &protocol.InspectionView{ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "node"}},
		},
		{
			name: "inspection tree missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
			}},
		},
		{
			name: "inspection tree scope missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
				Tree: &protocol.ReadTree{Format: "full", View: protocol.ViewDetailed, Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())},
			}},
		},
		{
			name: "inspection tree not detailed",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
				Tree: &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())},
			}},
		},
		{
			name: "inspection tree carries envelope view",
			result: func() *protocol.Result {
				view := testInspectionTreeView()
				view.View = protocol.ViewDetailed
				return &protocol.Result{Inspection: view}
			}(),
		},
		{
			name: "inspection subtree view missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
				Root: testNode("n5", protocol.RoleScroll, protocol.UnknownBounds()),
			}},
		},
		{
			name: "inspection subtree root missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
				View: protocol.ViewDetailed,
			}},
		},
		{
			name: "inspection subtree hides geometry",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
				View: protocol.ViewDetailed,
				Root: testNode("n5", protocol.RoleScroll, protocol.HiddenBounds()),
			}},
		},
		{
			name: "inspection query match chain",
			result: func() *protocol.Result {
				view := testInspectionQueryView()
				view.Matches[0].Node = &protocol.ReadNode{
					Chain:  []string{"n7", "n8"},
					Role:   protocol.RoleButton,
					Bounds: protocol.UnknownBounds(),
				}
				return &protocol.Result{Inspection: view}
			}(),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			requireNoTreeText(t, testCase.result)
		})
	}
}

func TestTreeTextRejectsMalformedRef(t *testing.T) {
	full := testFullTree()
	full.Root.Children[0].Ref = "n01"
	requireNoTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O3", Tree: &protocol.ReadTreeValue{Full: full},
	}})

	full = testFullTree()
	full.Root.Children[0].Ref = "bad ref"
	requireNoTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O3", Tree: &protocol.ReadTreeValue{Full: full},
	}})

	cases := []*protocol.ReadDelta{
		{Format: "delta", Base: "O3", View: protocol.ViewCompact, Set: map[string]protocol.ReadSetNode{"n0": {}}},
		{Format: "delta", Base: "O3", View: protocol.ViewCompact, Children: map[string][]string{"n 1": {"n2"}}},
		{Format: "delta", Base: "O3", View: protocol.ViewCompact, Children: map[string][]string{"n1": {"n02"}}},
		{Format: "delta", Base: "O3", View: protocol.ViewCompact, Add: []*protocol.ReadNode{testNode("n02", protocol.RoleGroup, protocol.UnknownBounds())}},
		{Format: "delta", Base: "O3", View: protocol.ViewCompact, Remove: []string{"n1\n2"}},
		{Format: "delta", Base: "O3", View: protocol.ViewCompact, Replace: map[string]*protocol.ReadNode{"n1": testNode("n03", protocol.RoleGroup, protocol.UnknownBounds())}},
	}
	for index, delta := range cases {
		t.Run("delta-"+strconv.Itoa(index), func(t *testing.T) {
			requireNoTreeText(t, &protocol.Result{Observation: &protocol.Observation{
				ID: "O4", Tree: &protocol.ReadTreeValue{Delta: delta},
			}})
		})
	}

	query := testInspectionQueryView()
	query.Matches[0].Path = []string{"n1", "n01"}
	requireNoTreeText(t, &protocol.Result{Inspection: query})

	query = testInspectionQueryView()
	query.Matches[0].Node.Ref = "n1 2"
	requireNoTreeText(t, &protocol.Result{Inspection: query})
}

func TestTreeTextTrailer(t *testing.T) {
	cases := []struct {
		name    string
		result  *protocol.Result
		trailer string
	}{
		{"full", &protocol.Result{Observation: &protocol.Observation{
			ID: "O3", Tree: &protocol.ReadTreeValue{Full: testFullTree()},
		}}, "end tree\n"},
		{"delta", &protocol.Result{Observation: &protocol.Observation{
			ID: "O4", Tree: &protocol.ReadTreeValue{Delta: testDelta()},
		}}, "end tree\n"},
		{"inspect-tree", &protocol.Result{Inspection: testInspectionTreeView()}, "end inspect\n"},
		{"inspect-subtree", &protocol.Result{Inspection: testInspectionSubtreeView()}, "end inspect\n"},
		{"inspect-query", &protocol.Result{Inspection: testInspectionQueryView()}, "end inspect\n"},
	}
	for _, testCase := range cases {
		block := mustRenderTreeText(t, testCase.result)
		if !strings.HasSuffix(block, testCase.trailer) {
			t.Fatalf("%s does not end with %q:\n%s", testCase.name, testCase.trailer, block)
		}
		if strings.Count(block, testCase.trailer) != 1 {
			t.Fatalf("%s has a duplicated trailer:\n%s", testCase.name, block)
		}
		if strings.HasSuffix(block, "\n\n") {
			t.Fatalf("%s has a blank line after its trailer", testCase.name)
		}
	}
}

func TestTreeTextEmptyTree(t *testing.T) {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	const blockSeparator = "\n"
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}},
	}})
	want := "tree v4 full nodes=1\n" +
		blockSeparator +
		"n1 root 0,0,1080,2400\nend tree\n"
	if block != want {
		t.Fatalf("empty tree block = %q, want %q", block, want)
	}

	delta := &protocol.ReadDelta{Format: "delta", Base: "O1", View: protocol.ViewCompact}
	block = mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O2", Tree: &protocol.ReadTreeValue{Delta: delta},
	}})
	want = "tree v4 delta base=O1 changes=0\n" +
		blockSeparator + "end tree\n"
	if block != want {
		t.Fatalf("empty delta block = %q, want %q", block, want)
	}

	empty := &protocol.InspectionView{
		ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
		Tree: &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed, Root: root},
	}
	block = mustRenderTreeText(t, &protocol.Result{Inspection: empty})
	want = "inspect v3 scope=tree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z " +
		"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n" +
		blockSeparator +
		"n1 root [0,0,1080,2400]\nend inspect\n"
	if block != want {
		t.Fatalf("empty inspection block = %q, want %q", block, want)
	}

	query := &protocol.InspectionView{
		ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "query",
		View:    protocol.ViewDetailed,
		Matches: nil,
	}
	block = mustRenderTreeText(t, &protocol.Result{Inspection: query})
	want = "inspect v3 scope=query observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z " +
		"tree_scope=reported_visible_with_context view=detailed matches=0 nodes=0 entries=0\n" +
		blockSeparator + "end inspect\n"
	if block != want {
		t.Fatalf("empty query block = %q, want %q", block, want)
	}
	parsed := parseInspectionText(t, block)
	if parsed.Matches == nil || len(parsed.Matches) != 0 || parsed.HasTotal || parsed.Truncated != nil {
		t.Fatalf("empty query presence changed: %#v", parsed)
	}
}

func TestTreeTextNoTree(t *testing.T) {
	cases := []*protocol.Result{
		nil,
		{},
		{Error: &protocol.Error{Code: "invalid_request", Message: "bad"}},
		{Observation: &protocol.Observation{Status: "failed"}},
		{Observation: &protocol.Observation{Status: "complete", ID: "O1"}},
		{Observation: &protocol.Observation{Status: "complete", ID: "O1", Tree: &protocol.ReadTreeValue{}}},
	}
	for index, result := range cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			requireNoTreeText(t, result)
		})
	}
}

func TestTextCarriesTreeBlock(t *testing.T) {
	observation := `{"status":"complete","id":"O1","origin":"P1","revision":1,` +
		`"captured_at":"2026-11-09T00:00:01Z","actionable_until":"2026-11-09T00:01:01Z",` +
		`"screen":` + testScreenJSON + `,"foreground":` + testForegroundJSON + `,"tree":` + testFullTreeJSON + `}`
	cases := []struct {
		name      string
		tool      string
		arguments string
		response  string
		header    string
		trailer   string
	}{
		{
			name: "start", tool: "start", arguments: `{}`,
			response: `{"operation_id":"P1","outcome":"executed","session":` + testActiveSessionJSON + `,"observation":` + observation + `}`,
			header:   "tree v4 full nodes=1",
			trailer:  "end tree\n",
		},
		{
			name: "observe", tool: "observe", arguments: `{"reference":"O1"}`,
			response: `{"operation_id":"P2","observation":` + observation + `}`,
			header:   "tree v4 full nodes=1",
			trailer:  "end tree\n",
		},
		{
			name: "observe-delta", tool: "observe", arguments: `{"reference":"O1"}`,
			response: `{"operation_id":"P2","observation":{"status":"complete","id":"O2","revision":2,"tree":` + testDeltaTreeJSON + `}}`,
			header:   "tree v4 delta base=O1 changes=0",
			trailer:  "end tree\n",
		},
		{
			name: "act", tool: "act", arguments: `{"reference":"O1","operation":{"type":"key","key":"BACK"}}`,
			response: `{"operation_id":"P2","outcome":"executed","observation":` + observation + `}`,
			header:   "tree v4 full nodes=1",
			trailer:  "end tree\n",
		},
		{
			name: "app-open", tool: "app", arguments: `{"kind":"open","reference":"O1","target":{"app":"com.example.probe"}}`,
			response: `{"operation_id":"P2","outcome":"executed","app":{"kind":"open",` +
				`"package":"com.example.probe","task_id":42,"display_id":12,"ownership":"owned",` +
				`"reused":false,"state_preserved":false},"observation":` + observation + `}`,
			header:  "tree v4 full nodes=1",
			trailer: "end tree\n",
		},
		{
			name: "inspect", tool: "inspect", arguments: `{"reference":"O1"}`,
			response: `{"inspection":{"observation_id":"O1","origin":"P1","revision":1,` +
				`"captured_at":"2026-11-09T00:00:01Z","scope":"tree","tree":` + testDetailedTreeJSON + `}}`,
			header:  "inspect v3 scope=tree observation=O1 revision=1 captured_at=2026-11-09T00:00:01Z tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1",
			trailer: "end inspect\n",
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fake := newTestDeviceService(t)
			fake.setHandler(testCase.tool, func(writer http.ResponseWriter, request *http.Request, body string) {
				writeTestJSON(writer, 200, testCase.response)
			})
			adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
			result := adapter.handle(context.Background(), testDefinitionByName(t, testCase.tool), []byte(testCase.arguments))
			if result.IsError {
				t.Fatalf("%s reported an error: %s", testCase.name, testTextContent(t, result))
			}
			text := testTextContent(t, result)
			if !strings.HasPrefix(text, "result v4 ok") || !strings.HasSuffix(text, "end result\n") {
				t.Fatalf("%s text is not a complete result envelope:\n%s", testCase.name, text)
			}
			if !strings.Contains(text, testCase.trailer) {
				t.Fatalf("%s text does not carry %q before end result:\n%s", testCase.name, testCase.trailer, text)
			}
			if strings.Contains(text, "omitted") {
				t.Fatalf("%s text contains omitted:\n%s", testCase.name, text)
			}
			if !strings.Contains(text, "\n"+testCase.header+"\n") && !strings.HasPrefix(text, testCase.header+"\n") {
				t.Fatalf("%s text lacks header %q:\n%s", testCase.name, testCase.header, text)
			}
			if strings.Count(text, "\n\ntree v4 ") != 1 && strings.Count(text, "\n\ninspect v3 ") != 1 {
				t.Fatalf("%s text does not carry exactly one tree block:\n%s", testCase.name, text)
			}
			structured := testStructuredJSON(t, result)
			switch {
			case testCase.tool == "inspect":
				requireTestContains(t, structured, `"scope":"tree"`)
			case strings.Contains(testCase.header, "tree v4 delta "):
				requireTestContains(t, structured, `"format":"delta"`)
			default:
				requireTestContains(t, structured, `"format":"full"`)
			}
		})
	}
}

func TestTreeTextParserVersionGate(t *testing.T) {
	for _, header := range []string{
		"tree v4 full nodes=1",
		"inspect v3 scope=tree",
	} {
		if err := validateTreeTextHeaderVersion(header); err != nil {
			t.Errorf("current header rejected: %q: %v", header, err)
		}
	}
	for _, header := range []string{
		"tree v3 format=full",
		"tree v2 format=full",
		"inspect v2 scope=tree",
		"shell v1 termination=exited",
	} {
		if err := validateTreeTextHeaderVersion(header); err == nil {
			t.Errorf("retired or foreign header accepted: %q", header)
		}
	}
	if refs, err := parseAnonymousChainShorthand(tokenizeTreeTextLine("[n10 n11]")); err != nil ||
		!reflect.DeepEqual(refs, []string{"n10", "n11"}) {
		t.Fatalf("valid shorthand rejected: refs=%v err=%v", refs, err)
	}
	for _, line := range []string{
		"[n10 n11] extra",
		"[n10 n11] view",
		"[n10]",
		"[n10 bad]",
	} {
		if _, err := parseAnonymousChainShorthand(tokenizeTreeTextLine(line)); err == nil {
			t.Errorf("invalid shorthand accepted: %q", line)
		}
	}
}

// ---------------------------------------------------------------- text parser

type parsedTreeTextBlock struct {
	header  string
	body    []string
	trailer string
}

// parseTreeTextBlock checks the framed block shape. Ordinary trees use v4;
// historical inspection material remains inspect v3.
func parseTreeTextBlock(t *testing.T, block string) parsedTreeTextBlock {
	t.Helper()
	parsed, err := parseTreeTextBlockValue(block)
	if err != nil {
		t.Fatalf("invalid tree text block: %v", err)
	}
	return parsed
}

func parseTreeTextBlockValue(block string) (parsedTreeTextBlock, error) {
	if block == "" || strings.ContainsRune(block, '\r') {
		return parsedTreeTextBlock{}, fmt.Errorf("block must be non-empty LF text")
	}
	if !strings.HasSuffix(block, "\n") || strings.HasSuffix(block, "\n\n") {
		return parsedTreeTextBlock{}, fmt.Errorf("block must end with exactly one newline")
	}
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	if len(lines) < 3 {
		return parsedTreeTextBlock{}, fmt.Errorf("block has %d lines", len(lines))
	}
	if lines[1] != "" {
		return parsedTreeTextBlock{}, fmt.Errorf("header is not followed by a blank line")
	}
	if err := validateTreeTextHeaderVersion(lines[0]); err != nil {
		return parsedTreeTextBlock{}, err
	}
	kind := strings.Join(strings.Split(lines[0], " ")[:2], " ")
	trailer := "end inspect"
	if kind == "tree v4" {
		trailer = "end tree"
	}
	if lines[len(lines)-1] != trailer {
		return parsedTreeTextBlock{}, fmt.Errorf("trailer %q, want %q", lines[len(lines)-1], trailer)
	}
	return parsedTreeTextBlock{
		header:  lines[0],
		body:    lines[2 : len(lines)-1],
		trailer: trailer,
	}, nil
}

func validateTreeTextHeaderVersion(header string) error {
	if header != strings.TrimSpace(header) || strings.ContainsAny(header, "\t\r\n") {
		return fmt.Errorf("header whitespace is not canonical: %q", header)
	}
	tokens := strings.Split(header, " ")
	for _, token := range tokens {
		if token == "" {
			return fmt.Errorf("header has a repeated space: %q", header)
		}
	}
	if len(tokens) < 2 {
		return fmt.Errorf("header has too few fields: %q", header)
	}
	kind := tokens[0] + " " + tokens[1]
	switch kind {
	case "tree v4":
		return nil
	case "inspect v3":
		return nil
	case "tree v1", "tree v2", "tree v3", "inspect v1", "inspect v2":
		return fmt.Errorf("retired block version %q", kind)
	default:
		return fmt.Errorf("unsupported block version %q", kind)
	}
}

func parseHeaderFields(t *testing.T, header string) (string, map[string]string) {
	t.Helper()
	if strings.HasPrefix(header, "tree v4 ") {
		tokens := strings.Split(header, " ")
		if len(tokens) < 4 {
			t.Fatalf("short tree v4 header: %q", header)
		}
		fields := map[string]string{}
		for _, token := range tokens[3:] {
			key, value, ok := strings.Cut(token, "=")
			if !ok || key == "" || value == "" || strings.Contains(value, "=") {
				t.Fatalf("malformed tree v4 field %q", token)
			}
			if _, exists := fields[key]; exists {
				t.Fatalf("duplicate tree v4 field %q", key)
			}
			fields[key] = value
		}
		return "tree v4", fields
	}
	kind, fields, _, err := parseHeaderFieldsValue(header)
	if err != nil {
		t.Fatal(err)
	}
	return kind, fields
}

func parseHeaderFieldsValue(header string) (string, map[string]string, []string, error) {
	if header != strings.TrimSpace(header) || strings.ContainsAny(header, "\t\r\n") {
		return "", nil, nil, fmt.Errorf("header whitespace is not canonical: %q", header)
	}
	tokens := strings.Split(header, " ")
	if len(tokens) < 2 {
		return "", nil, nil, fmt.Errorf("header has too few fields: %q", header)
	}
	fields := make(map[string]string, len(tokens)-2)
	order := make([]string, 0, len(tokens)-2)
	for _, token := range tokens[2:] {
		if token == "" {
			return "", nil, nil, fmt.Errorf("header has a repeated space: %q", header)
		}
		key, value, ok := strings.Cut(token, "=")
		if !ok || key == "" || value == "" || strings.Contains(value, "=") {
			return "", nil, nil, fmt.Errorf("header field %q is malformed", token)
		}
		if _, exists := fields[key]; exists {
			return "", nil, nil, fmt.Errorf("duplicate header field %q", key)
		}
		fields[key] = value
		order = append(order, key)
	}
	return tokens[0] + " " + tokens[1], fields, order, nil
}

func headerInt(t *testing.T, header, name string) int {
	t.Helper()
	_, fields := parseHeaderFields(t, header)
	value, ok := fields[name]
	if !ok {
		t.Fatalf("header %q has no %q field", header, name)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("header field %s=%q is not an integer", name, value)
	}
	return parsed
}

func parseFullTreeText(t *testing.T, block string) *protocol.ReadTree {
	t.Helper()
	tree, err := parseFullTreeTextValue(block, "")
	if err != nil {
		t.Fatalf("invalid full tree: %v", err)
	}
	return tree
}

func parseDeltaText(t *testing.T, block string) *protocol.ReadDelta {
	t.Helper()
	delta, err := parseDeltaTextValue(block, "")
	if err != nil {
		t.Fatalf("invalid delta tree: %v", err)
	}
	return delta
}

func parseFullTreeTextValue(block, currentO string) (*protocol.ReadTree, error) {
	parsed, err := parseTreeTextBlockValue(block)
	if err != nil {
		return nil, err
	}
	tokens := strings.Split(parsed.header, " ")
	if len(tokens) != 4 || tokens[0] != "tree" || tokens[1] != "v4" ||
		tokens[2] != "full" || !strings.HasPrefix(tokens[3], "nodes=") {
		return nil, fmt.Errorf("full header fields or order are invalid: %q", parsed.header)
	}
	wantNodes, err := parseCanonicalPositive(strings.TrimPrefix(tokens[3], "nodes="))
	if err != nil {
		return nil, fmt.Errorf("full nodes: %w", err)
	}
	if currentO != "" && !validTreeTextObservationID(currentO) {
		return nil, fmt.Errorf("tree has no readable current observation id")
	}
	root, nodes, err := parseV4Subtree(parsed.body, true)
	if err != nil {
		return nil, err
	}
	if nodes != wantNodes {
		return nil, fmt.Errorf("full nodes=%d but definitions expand to %d", wantNodes, nodes)
	}
	return &protocol.ReadTree{
		Format: "full",
		Scope:  protocol.Scope,
		View:   protocol.ViewCompact,
		Root:   root,
	}, nil
}

func parseDeltaTextValue(block, currentO string) (*protocol.ReadDelta, error) {
	parsed, err := parseTreeTextBlockValue(block)
	if err != nil {
		return nil, err
	}
	tokens := strings.Split(parsed.header, " ")
	if len(tokens) != 5 || tokens[0] != "tree" || tokens[1] != "v4" ||
		tokens[2] != "delta" || !strings.HasPrefix(tokens[3], "base=") ||
		!strings.HasPrefix(tokens[4], "changes=") {
		return nil, fmt.Errorf("delta header fields or order are invalid: %q", parsed.header)
	}
	wantChanges, err := parseCanonicalNatural(strings.TrimPrefix(tokens[4], "changes="))
	if err != nil {
		return nil, fmt.Errorf("delta changes: %w", err)
	}
	base, err := expandTreeTextBase(currentO, strings.TrimPrefix(tokens[3], "base="))
	if err != nil {
		return nil, err
	}
	delta, changes, err := parseV4DeltaBody(parsed.body, base)
	if err != nil {
		return nil, err
	}
	if changes != wantChanges {
		return nil, fmt.Errorf("delta changes=%d but body has %d declarations", wantChanges, changes)
	}
	return delta, nil
}

func validTreeTextObservationID(id string) bool {
	return len(id) > 1 && len(id) <= protocol.MaxReferenceLen &&
		id[0] == 'O' && resultToken(id)
}

func expandTreeTextBase(currentO, encoded string) (string, error) {
	if !strings.HasPrefix(encoded, "_") {
		if !validTreeTextObservationID(encoded) {
			return "", fmt.Errorf("invalid full base %q", encoded)
		}
		return encoded, nil
	}
	sequence := strings.TrimPrefix(encoded, "_")
	if _, err := parseCanonicalUint64(sequence); err != nil {
		return "", fmt.Errorf("invalid short base %q", encoded)
	}
	if !validTreeTextObservationID(currentO) {
		return "", fmt.Errorf("short base %q has no current observation id", encoded)
	}
	at := strings.LastIndexByte(currentO, '_')
	if at <= 1 || currentO[0] != 'O' {
		return "", fmt.Errorf("short base %q cannot be completed from %q", encoded, currentO)
	}
	if _, err := parseCanonicalUint64(currentO[at+1:]); err != nil {
		return "", fmt.Errorf("short base %q cannot be completed from %q", encoded, currentO)
	}
	expanded := currentO[:at+1] + sequence
	if len(expanded) > protocol.MaxReferenceLen || !resultToken(expanded) {
		return "", fmt.Errorf("completed base is not a readable observation id")
	}
	return expanded, nil
}

func parseCanonicalNatural(raw string) (int, error) {
	if raw == "0" {
		return 0, nil
	}
	return parseCanonicalPositive(raw)
}

func parseCanonicalPositive(raw string) (int, error) {
	if raw == "" || raw[0] < '1' || raw[0] > '9' {
		return 0, fmt.Errorf("%q is not a canonical positive integer", raw)
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, fmt.Errorf("%q is not a canonical positive integer", raw)
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%q overflows an integer", raw)
	}
	return value, nil
}

func parseCanonicalUint64(raw string) (uint64, error) {
	if raw == "" || raw[0] < '1' || raw[0] > '9' {
		return 0, fmt.Errorf("%q is not a canonical positive uint64", raw)
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, fmt.Errorf("%q is not a canonical positive uint64", raw)
		}
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q overflows uint64", raw)
	}
	return value, nil
}

func parseV4DeltaBody(lines []string, base string) (*protocol.ReadDelta, int, error) {
	delta := &protocol.ReadDelta{Format: "delta", Base: base, View: protocol.ViewCompact}
	seenAdd := map[string]bool{}
	changes := 0
	for index := 0; index < len(lines); {
		line := lines[index]
		if line == "" {
			return nil, 0, fmt.Errorf("delta body contains a blank line")
		}
		if strings.HasPrefix(line, "~ ") {
			ref, set, err := parseV4SetLineValue(line)
			if err != nil {
				return nil, 0, err
			}
			if delta.Set == nil {
				delta.Set = map[string]protocol.ReadSetNode{}
			}
			if _, exists := delta.Set[ref]; exists {
				return nil, 0, fmt.Errorf("duplicate set ref %q", ref)
			}
			delta.Set[ref] = set
			changes++
			index++
			continue
		}
		if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "= ") {
			prefix := line[:2]
			subtree := []string{line[2:]}
			index++
			for index < len(lines) && strings.HasPrefix(lines[index], " ") {
				subtree = append(subtree, lines[index])
				index++
			}
			root, _, err := parseV4Subtree(subtree, false)
			if err != nil {
				return nil, 0, err
			}
			if prefix == "+ " {
				if root.IsChain() {
					return nil, 0, fmt.Errorf("add definitions must have an ordinary root ref")
				}
				if seenAdd[root.Ref] {
					return nil, 0, fmt.Errorf("duplicate add root %q", root.Ref)
				}
				seenAdd[root.Ref] = true
				delta.Add = append(delta.Add, root)
			} else {
				if root.IsChain() {
					return nil, 0, fmt.Errorf("replace definitions must have an ordinary root ref")
				}
				if delta.Replace == nil {
					delta.Replace = map[string]*protocol.ReadNode{}
				}
				if _, exists := delta.Replace[root.Ref]; exists {
					return nil, 0, fmt.Errorf("duplicate replace ref %q", root.Ref)
				}
				delta.Replace[root.Ref] = root
			}
			changes++
			continue
		}
		if strings.HasPrefix(line, "- ") {
			ref := strings.TrimPrefix(line, "- ")
			if !protocol.ValidPublicRef(ref) {
				return nil, 0, fmt.Errorf("invalid remove ref %q", ref)
			}
			for _, existing := range delta.Remove {
				if existing == ref {
					return nil, 0, fmt.Errorf("duplicate remove ref %q", ref)
				}
			}
			delta.Remove = append(delta.Remove, ref)
			changes++
			index++
			continue
		}
		if strings.HasPrefix(line, "children ") {
			ref, children, err := parseChildrenLineValue(line)
			if err != nil {
				return nil, 0, err
			}
			if delta.Children == nil {
				delta.Children = map[string][]string{}
			}
			if _, exists := delta.Children[ref]; exists {
				return nil, 0, fmt.Errorf("duplicate children ref %q", ref)
			}
			delta.Children[ref] = children
			changes++
			index++
			continue
		}
		return nil, 0, fmt.Errorf("unexpected delta line %q", line)
	}
	return delta, changes, nil
}

type v4Ancestor struct {
	depth  int
	bounds protocol.ReadBounds
}

func parseV4Subtree(lines []string, requireRoot bool) (*protocol.ReadNode, int, error) {
	if len(lines) == 0 {
		return nil, 0, fmt.Errorf("definition has no root")
	}
	type pendingNode struct {
		depth int
		node  *protocol.ReadNode
	}
	var root *protocol.ReadNode
	stack := []pendingNode{}
	ancestors := []v4Ancestor{}
	seen := map[string]bool{}
	logicalNodes := 0
	previousDepth := -1
	for index, line := range lines {
		if line == "" || strings.ContainsRune(line, '\r') || strings.ContainsRune(line, '\t') {
			return nil, 0, fmt.Errorf("definition line %d is empty or contains forbidden whitespace", index+1)
		}
		depth := 0
		for depth < len(line) && line[depth] == ' ' {
			depth++
		}
		if depth == len(line) {
			return nil, 0, fmt.Errorf("definition line %d has indentation only", index+1)
		}
		if index == 0 && depth != 0 {
			return nil, 0, fmt.Errorf("definition root must have zero indentation")
		}
		if index > 0 && depth > previousDepth+1 {
			return nil, 0, fmt.Errorf("definition indentation skips a level at line %d", index+1)
		}
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		for len(ancestors) > 0 && ancestors[len(ancestors)-1].depth >= depth {
			ancestors = ancestors[:len(ancestors)-1]
		}
		node, err := parseV4NodeContent(line[depth:], ancestors)
		if err != nil {
			return nil, 0, fmt.Errorf("definition line %d: %w", index+1, err)
		}
		if depth == 0 {
			if root != nil {
				return nil, 0, fmt.Errorf("definition has more than one root")
			}
			if requireRoot && (node.IsChain() || node.Role != protocol.RoleRoot) {
				return nil, 0, fmt.Errorf("full tree root must be an ordinary root node")
			}
			root = node
		} else {
			if len(stack) == 0 || stack[len(stack)-1].depth != depth-1 {
				return nil, 0, fmt.Errorf("definition line %d has no direct parent", index+1)
			}
			parent := stack[len(stack)-1].node
			parent.Children = append(parent.Children, node)
		}
		refs := node.Refs()
		for _, ref := range refs {
			if seen[ref] {
				return nil, 0, fmt.Errorf("duplicate logical ref %q within one definition", ref)
			}
			seen[ref] = true
		}
		logicalNodes += len(refs)
		stack = append(stack, pendingNode{depth: depth, node: node})
		ancestors = append(ancestors, v4Ancestor{depth: depth, bounds: node.Bounds})
		previousDepth = depth
	}
	return root, logicalNodes, nil
}

func parseV4NodeContent(content string, ancestors []v4Ancestor) (*protocol.ReadNode, error) {
	tokens, err := tokenizeTreeTextLineStrict(content)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("node line has no columns")
	}
	node := &protocol.ReadNode{
		Label: []string{}, Actions: []protocol.Action{}, Children: []*protocol.ReadNode{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
		Checked: testBoolPtr(false), Bounds: protocol.HiddenBounds(),
	}
	index := 0
	shorthand := false
	switch {
	case tokens[0] == "chain":
		refs, next, err := parseChainRefsValue(tokens, 1)
		if err != nil {
			return nil, err
		}
		if len(refs) < 2 {
			return nil, fmt.Errorf("chain definition has fewer than two refs")
		}
		if next >= len(tokens) || !protocol.Role(tokens[next]).Valid() {
			return nil, fmt.Errorf("chain definition has no valid role")
		}
		node.Chain = refs
		node.Role = protocol.Role(tokens[next])
		index = next + 1
	case strings.HasPrefix(tokens[0], "["):
		refs, err := parseAnonymousChainShorthand(tokens)
		if err != nil {
			return nil, err
		}
		node.Chain = refs
		node.Role = protocol.RoleView
		shorthand = true
		index = len(tokens)
	default:
		if !protocol.ValidPublicRef(tokens[0]) {
			return nil, fmt.Errorf("invalid public ref %q", tokens[0])
		}
		node.Ref = tokens[0]
		index = 1
	}
	if shorthand {
		return node, nil
	}
	if len(node.Chain) == 0 {
		if index < len(tokens) && protocol.Role(tokens[index]).Valid() {
			node.Role = protocol.Role(tokens[index])
			index++
		} else if index < len(tokens) && strings.HasPrefix(tokens[index], `"`) {
			node.Role = protocol.RoleText
		} else {
			node.Role = protocol.RoleView
		}
	}
	if !node.Role.Valid() {
		return nil, fmt.Errorf("invalid role %q", node.Role)
	}
	geometry := ""
	if index < len(tokens) && isV4GeometryToken(tokens[index]) {
		geometry = tokens[index]
		index++
	}
	if geometry == "@" {
		if node.IsChain() {
			return nil, fmt.Errorf("chain definitions must spell an explicit geometry column")
		}
		var resolved bool
		for ancestorIndex := len(ancestors) - 1; ancestorIndex >= 0; ancestorIndex-- {
			bounds := ancestors[ancestorIndex].bounds
			if bounds.Kind != protocol.GeometryKnown {
				continue
			}
			node.Bounds = protocol.KnownBounds(bounds.Rect)
			resolved = true
			break
		}
		if !resolved {
			return nil, fmt.Errorf("@ has no known strict ancestor")
		}
	} else {
		switch geometry {
		case "":
			node.Bounds = protocol.HiddenBounds()
		case "-":
			node.Bounds = protocol.UnknownBounds()
		default:
			rect, err := parseBoundsValue(geometry)
			if err != nil {
				return nil, err
			}
			node.Bounds = protocol.KnownBounds(rect)
		}
	}
	if err := parseNodeTailFields(node, tokens[index:], !node.IsChain(), !node.IsChain()); err != nil {
		return nil, err
	}
	return node, nil
}

func isV4GeometryToken(token string) bool {
	return token == "-" || token == "@" || isCanonicalBoundsToken(token)
}

func isCanonicalBoundsToken(token string) bool {
	_, err := parseBoundsValue(token)
	return err == nil
}

func parseBoundsValue(token string) (protocol.Bounds, error) {
	parts := strings.Split(token, ",")
	if len(parts) != 4 {
		return protocol.Bounds{}, fmt.Errorf("geometry %q does not have four coordinates", token)
	}
	values := [4]int{}
	for index, part := range parts {
		value, err := parseCanonicalSignedInt(part)
		if err != nil {
			return protocol.Bounds{}, fmt.Errorf("geometry coordinate %q: %w", part, err)
		}
		values[index] = value
	}
	if values[2] < values[0] || values[3] < values[1] {
		return protocol.Bounds{}, fmt.Errorf("geometry %q has reversed edges", token)
	}
	return protocol.Bounds{Left: values[0], Top: values[1], Right: values[2], Bottom: values[3]}, nil
}

func parseCanonicalSignedInt(raw string) (int, error) {
	if raw == "0" {
		return 0, nil
	}
	if strings.HasPrefix(raw, "-") {
		if len(raw) == 2 && raw[1] >= '1' && raw[1] <= '9' {
			return -int(raw[1] - '0'), nil
		}
		if len(raw) <= 2 || raw[1] < '1' || raw[1] > '9' {
			return 0, fmt.Errorf("%q is not a canonical integer", raw)
		}
		for index := 2; index < len(raw); index++ {
			if raw[index] < '0' || raw[index] > '9' {
				return 0, fmt.Errorf("%q is not a canonical integer", raw)
			}
		}
	} else {
		if raw[0] < '1' || raw[0] > '9' {
			return 0, fmt.Errorf("%q is not a canonical integer", raw)
		}
		for index := 1; index < len(raw); index++ {
			if raw[index] < '0' || raw[index] > '9' {
				return 0, fmt.Errorf("%q is not a canonical integer", raw)
			}
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%q overflows an integer", raw)
	}
	return value, nil
}

func parseNodeTailFields(
	node *protocol.ReadNode,
	tokens []string,
	restoreDefaults bool,
	allowNegation bool,
) error {
	seen := map[string]bool{}
	explicitActions := []protocol.Action{}
	negated := protocol.Action("")
	lastRank := -1
	sawNegation := false
	for _, token := range tokens {
		rank := -1
		switch {
		case strings.HasPrefix(token, `"`):
			value, err := decodeJSONStringToken(token)
			if err != nil {
				return fmt.Errorf("label: %w", err)
			}
			rank = 0
			node.Label = append(node.Label, value)
		case strings.HasPrefix(token, "value="):
			rank = 1
			value, err := decodeNullableJSONString(strings.TrimPrefix(token, "value="))
			if err != nil || value == nil {
				return fmt.Errorf("value must be a JSON string")
			}
			node.Value = value
		case strings.HasPrefix(token, "hint="):
			rank = 2
			value, err := decodeNullableJSONString(strings.TrimPrefix(token, "hint="))
			if err != nil || value == nil {
				return fmt.Errorf("hint must be a JSON string")
			}
			node.Hint = value
		case allowNegation && strings.HasPrefix(token, "!"):
			rank = 3
			if sawNegation {
				return fmt.Errorf("duplicate action negation %q", token)
			}
			action := protocol.Action(strings.TrimPrefix(token, "!"))
			if !action.Valid() {
				return fmt.Errorf("invalid action negation %q", token)
			}
			negated = action
			sawNegation = true
		case protocol.Action(token).Valid():
			rank = 3
			if sawNegation {
				return fmt.Errorf("explicit action %q follows a negation", token)
			}
			explicitActions = append(explicitActions, protocol.Action(token))
		case strings.HasPrefix(token, "enabled="):
			rank = 4
			value, err := decodeTriBool(strings.TrimPrefix(token, "enabled="))
			if err != nil {
				return fmt.Errorf("enabled: %w", err)
			}
			node.Enabled = value
		case strings.HasPrefix(token, "focused="):
			rank = 5
			value, err := decodeTriBool(strings.TrimPrefix(token, "focused="))
			if err != nil {
				return fmt.Errorf("focused: %w", err)
			}
			node.Focused = value
		case strings.HasPrefix(token, "selected="):
			rank = 6
			value, err := decodeTriBool(strings.TrimPrefix(token, "selected="))
			if err != nil {
				return fmt.Errorf("selected: %w", err)
			}
			node.Selected = value
		case strings.HasPrefix(token, "protected="):
			rank = 7
			value, err := decodeTriBool(strings.TrimPrefix(token, "protected="))
			if err != nil {
				return fmt.Errorf("protected: %w", err)
			}
			node.Protected = value
		case strings.HasPrefix(token, "checked="):
			rank = 8
			value, err := decodeTriBool(strings.TrimPrefix(token, "checked="))
			if err != nil {
				return fmt.Errorf("checked: %w", err)
			}
			node.Checked = value
		case strings.HasPrefix(token, "axis="):
			rank = 9
			axis := protocol.Axis(strings.TrimPrefix(token, "axis="))
			if !axis.Valid() {
				return fmt.Errorf("invalid axis %q", axis)
			}
			node.Axis = axis
		case strings.HasPrefix(token, "resource="):
			rank = 10
			value, err := decodeNullableJSONString(strings.TrimPrefix(token, "resource="))
			if err != nil || value == nil {
				return fmt.Errorf("resource must be a JSON string")
			}
			node.Resource = value
		case token == "context":
			rank = 11
			node.Context = true
		default:
			return fmt.Errorf("unexpected node token %q", token)
		}
		key := token
		if at := strings.IndexByte(key, '='); at >= 0 {
			key = key[:at]
		}
		if rank != 0 && rank != 3 {
			if seen[key] {
				return fmt.Errorf("duplicate node field %q", key)
			}
			seen[key] = true
		}
		if rank < lastRank {
			return fmt.Errorf("node fields are out of order at %q", token)
		}
		lastRank = rank
	}
	if !restoreDefaults {
		node.Actions = explicitActions
		return nil
	}
	actions, err := restoreV4DefaultActions(node.Role, explicitActions, negated)
	if err != nil {
		return err
	}
	node.Actions = actions
	return nil
}

func restoreV4DefaultActions(role protocol.Role, explicit []protocol.Action, negated protocol.Action) ([]protocol.Action, error) {
	defaultAction := protocol.Action("")
	switch role {
	case protocol.RoleButton:
		defaultAction = protocol.ActionTap
	case protocol.RoleInput:
		defaultAction = protocol.ActionText
	case protocol.RoleScroll:
		defaultAction = protocol.ActionSwipe
	}
	hasDefault := false
	for _, action := range explicit {
		if !action.Valid() {
			return nil, fmt.Errorf("invalid explicit action %q", action)
		}
		if action == defaultAction {
			hasDefault = true
		}
	}
	if negated != "" {
		if defaultAction == "" || negated != defaultAction {
			return nil, fmt.Errorf("action negation %q does not apply to role %q", negated, role)
		}
		for _, action := range explicit {
			if action == negated {
				return nil, fmt.Errorf("action %q is both explicit and negated", negated)
			}
		}
		return explicit, nil
	}
	if defaultAction == "" || hasDefault {
		return explicit, nil
	}
	lastRank := -1
	for _, action := range explicit {
		rank := actionSourceRank(action)
		if rank <= lastRank {
			return nil, fmt.Errorf("actions cannot restore default %q in source order", defaultAction)
		}
		lastRank = rank
	}
	insertAt := len(explicit)
	defaultRank := actionSourceRank(defaultAction)
	for index, action := range explicit {
		if actionSourceRank(action) > defaultRank {
			insertAt = index
			break
		}
	}
	restored := make([]protocol.Action, 0, len(explicit)+1)
	restored = append(restored, explicit[:insertAt]...)
	restored = append(restored, defaultAction)
	restored = append(restored, explicit[insertAt:]...)
	return restored, nil
}

func actionSourceRank(action protocol.Action) int {
	switch action {
	case protocol.ActionTap:
		return 0
	case protocol.ActionLongPress:
		return 1
	case protocol.ActionText:
		return 2
	case protocol.ActionSwipe:
		return 3
	default:
		return 4
	}
}

func decodeTriBool(raw string) (*bool, error) {
	switch raw {
	case "true":
		return testBoolPtr(true), nil
	case "false":
		return testBoolPtr(false), nil
	case "null":
		return nil, nil
	default:
		return nil, fmt.Errorf("%q is not true, false or null", raw)
	}
}

func decodeJSONStringToken(token string) (string, error) {
	if !strings.HasPrefix(token, `"`) {
		return "", fmt.Errorf("%q is not a JSON string", token)
	}
	var value string
	decoder := json.NewDecoder(strings.NewReader(token))
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if decoder.More() {
		return "", fmt.Errorf("%q has trailing JSON", token)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("%q has a second JSON value", token)
	}
	return value, nil
}

func decodeNullableJSONString(raw string) (*string, error) {
	if raw == "null" {
		return nil, nil
	}
	value, err := decodeJSONStringToken(raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func tokenizeTreeTextLineStrict(line string) ([]string, error) {
	if line == "" {
		return nil, fmt.Errorf("line is empty")
	}
	tokens := []string{}
	var current strings.Builder
	inQuote := false
	escaped := false
	previousSpace := false
	for index := 0; index < len(line); index++ {
		character := line[index]
		if character < 0x20 {
			return nil, fmt.Errorf("line contains a control character")
		}
		if escaped {
			current.WriteByte(character)
			escaped = false
			previousSpace = false
			continue
		}
		if inQuote {
			current.WriteByte(character)
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inQuote = false
			}
			previousSpace = false
			continue
		}
		switch character {
		case '"':
			inQuote = true
			current.WriteByte(character)
			previousSpace = false
		case ' ':
			if previousSpace {
				return nil, fmt.Errorf("line contains repeated spaces")
			}
			if current.Len() == 0 {
				return nil, fmt.Errorf("line has leading or trailing whitespace")
			}
			tokens = append(tokens, current.String())
			current.Reset()
			previousSpace = true
		case '\\':
			return nil, fmt.Errorf("backslash is only valid inside a JSON string")
		default:
			current.WriteByte(character)
			previousSpace = false
		}
	}
	if inQuote || escaped {
		return nil, fmt.Errorf("unterminated JSON string")
	}
	if previousSpace {
		return nil, fmt.Errorf("line has trailing whitespace")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	for _, token := range tokens {
		if strings.HasPrefix(token, `"`) || strings.Contains(token, `="`) {
			value := token
			if at := strings.IndexByte(token, '='); at >= 0 {
				value = token[at+1:]
			}
			if strings.HasPrefix(value, `"`) {
				if _, err := decodeJSONStringToken(value); err != nil {
					return nil, err
				}
			}
		}
	}
	return tokens, nil
}

func parseV4SetLineValue(line string) (string, protocol.ReadSetNode, error) {
	if !strings.HasPrefix(line, "~ ") || strings.HasPrefix(line, "~  ") {
		return "", protocol.ReadSetNode{}, fmt.Errorf("malformed set line %q", line)
	}
	tokens, err := tokenizeTreeTextLineStrict(strings.TrimPrefix(line, "~ "))
	if err != nil {
		return "", protocol.ReadSetNode{}, err
	}
	if len(tokens) < 2 || !protocol.ValidPublicRef(tokens[0]) {
		return "", protocol.ReadSetNode{}, fmt.Errorf("set line has an invalid ref")
	}
	ref := tokens[0]
	seen := map[string]bool{}
	lastRank := -1
	var set protocol.ReadSetNode
	for _, token := range tokens[1:] {
		key, raw, ok := strings.Cut(token, "=")
		if !ok || key == "" || raw == "" {
			return "", protocol.ReadSetNode{}, fmt.Errorf("malformed set field %q", token)
		}
		if seen[key] {
			return "", protocol.ReadSetNode{}, fmt.Errorf("duplicate set field %q", key)
		}
		seen[key] = true
		rank := -1
		switch key {
		case "label":
			rank = 0
			values, err := decodeStringArrayOrNull(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, fmt.Errorf("label: %w", err)
			}
			set.Label = protocol.Field[[]string]{Set: true, Value: values}
		case "actions":
			rank = 1
			values, err := decodeStringArrayOrNull(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, fmt.Errorf("actions: %w", err)
			}
			var actions []protocol.Action
			if values != nil {
				actions = make([]protocol.Action, len(values))
				for index, value := range values {
					action := protocol.Action(value)
					if !action.Valid() {
						return "", protocol.ReadSetNode{}, fmt.Errorf("invalid set action %q", value)
					}
					actions[index] = action
				}
			}
			set.Actions = protocol.Field[[]protocol.Action]{Set: true, Value: actions}
		case "bounds":
			rank = 2
			switch raw {
			case "null":
				set.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.UnknownBounds()}
			case "hidden":
				set.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.HiddenBounds()}
			default:
				if len(raw) < 2 || !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
					return "", protocol.ReadSetNode{}, fmt.Errorf("set bounds must be bracketed")
				}
				bounds, err := parseBoundsValue(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
				if err != nil {
					return "", protocol.ReadSetNode{}, err
				}
				set.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.KnownBounds(bounds)}
			}
		case "enabled":
			rank = 3
			var err error
			set.Enabled, err = parseOptionalBoolFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "focused":
			rank = 4
			var err error
			set.Focused, err = parseOptionalBoolFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "selected":
			rank = 5
			var err error
			set.Selected, err = parseOptionalBoolFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "checked":
			rank = 6
			var err error
			set.Checked, err = parseOptionalBoolFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "value":
			rank = 7
			var err error
			set.Value, err = parseOptionalStringFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "hint":
			rank = 8
			var err error
			set.Hint, err = parseOptionalStringFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "protected":
			rank = 9
			var err error
			set.Protected, err = parseOptionalBoolFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		case "axis":
			rank = 10
			if raw == "null" {
				set.Axis = protocol.Field[*protocol.Axis]{Set: true}
			} else {
				axis := protocol.Axis(raw)
				if !axis.Valid() {
					return "", protocol.ReadSetNode{}, fmt.Errorf("invalid set axis %q", raw)
				}
				set.Axis = protocol.Field[*protocol.Axis]{Set: true, Value: &axis}
			}
		case "resource":
			rank = 11
			var err error
			set.Resource, err = parseOptionalStringFieldValue(raw)
			if err != nil {
				return "", protocol.ReadSetNode{}, err
			}
		default:
			return "", protocol.ReadSetNode{}, fmt.Errorf("unexpected set field %q", key)
		}
		if rank < lastRank {
			return "", protocol.ReadSetNode{}, fmt.Errorf("set fields are out of order at %q", key)
		}
		lastRank = rank
		if key != "label" && key != "actions" && key != "bounds" &&
			key != "enabled" && key != "focused" && key != "selected" &&
			key != "checked" && key != "value" && key != "hint" &&
			key != "protected" && key != "axis" && key != "resource" {
			return "", protocol.ReadSetNode{}, fmt.Errorf("unexpected set field %q", key)
		}
	}
	return ref, set, nil
}

func decodeStringArrayOrNull(raw string) ([]string, error) {
	if raw == "null" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var values []string
	if err := decoder.Decode(&values); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("array has a second JSON value")
	}
	return values, nil
}

func parseOptionalBoolFieldValue(raw string) (protocol.Field[*bool], error) {
	value, err := decodeTriBool(raw)
	if err != nil {
		return protocol.Field[*bool]{}, err
	}
	return protocol.Field[*bool]{Set: true, Value: value}, nil
}

func parseOptionalStringFieldValue(raw string) (protocol.Field[*string], error) {
	value, err := decodeNullableJSONString(raw)
	if err != nil {
		return protocol.Field[*string]{}, err
	}
	return protocol.Field[*string]{Set: true, Value: value}, nil
}

func parseChildrenLineValue(line string) (string, []string, error) {
	if !strings.HasPrefix(line, "children ") {
		return "", nil, fmt.Errorf("malformed children line %q", line)
	}
	rest := strings.TrimPrefix(line, "children ")
	separator := " = ["
	at := strings.Index(rest, separator)
	if at < 0 || !strings.HasSuffix(rest, "]") || strings.Contains(rest[:at], " ") {
		return "", nil, fmt.Errorf("malformed children line %q", line)
	}
	ref := rest[:at]
	if !protocol.ValidPublicRef(ref) {
		return "", nil, fmt.Errorf("invalid children ref %q", ref)
	}
	inside := strings.TrimSuffix(rest[at+len(separator):], "]")
	if inside == "" {
		return ref, []string{}, nil
	}
	if strings.Contains(inside, "  ") || strings.TrimSpace(inside) != inside {
		return "", nil, fmt.Errorf("children refs use non-canonical spacing")
	}
	children := strings.Split(inside, " ")
	for _, child := range children {
		if !protocol.ValidPublicRef(child) {
			return "", nil, fmt.Errorf("invalid child ref %q", child)
		}
	}
	return ref, children, nil
}

func parseInspectionText(t *testing.T, block string) *protocol.InspectionView {
	t.Helper()
	view, err := parseInspectionTextValue(block)
	if err != nil {
		t.Fatalf("invalid inspect block: %v", err)
	}
	return view
}

func parseInspectionTextValue(block string) (*protocol.InspectionView, error) {
	parsed, err := parseTreeTextBlockValue(block)
	if err != nil {
		return nil, err
	}
	kind, fields, order, err := parseHeaderFieldsValue(parsed.header)
	if err != nil {
		return nil, err
	}
	if kind != "inspect v3" {
		return nil, fmt.Errorf("inspection uses %q, want inspect v3", kind)
	}
	revision, err := strconv.Atoi(fields["revision"])
	if err != nil || strconv.Itoa(revision) != fields["revision"] {
		return nil, fmt.Errorf("revision %q is not a canonical integer", fields["revision"])
	}
	view := &protocol.InspectionView{
		ObservationID: fields["observation"],
		Revision:      revision,
		CapturedAt:    fields["captured_at"],
		Scope:         fields["scope"],
	}
	baseOrder := []string{"scope", "observation", "revision", "captured_at", "tree_scope", "view"}
	switch view.Scope {
	case "tree":
		if !reflect.DeepEqual(order, append(baseOrder, "nodes", "entries")) {
			return nil, fmt.Errorf("inspect tree header fields are invalid")
		}
		view.Tree = &protocol.ReadTree{
			Format: "full",
			Scope:  fields["tree_scope"],
			View:   fields["view"],
		}
		if fields["tree_scope"] != protocol.Scope || fields["view"] != protocol.ViewDetailed {
			return nil, fmt.Errorf("inspect tree metadata is invalid")
		}
		root, nodes, err := parseInspectV3Subtree(parsed.body, true)
		if err != nil {
			return nil, err
		}
		view.Tree.Root = root
		if err := validateInspectV3Counts(fields, nodes, countNodeLines(parsed.body)); err != nil {
			return nil, err
		}
	case "subtree":
		wantOrder := append(append([]string{}, baseOrder...), "root", "nodes", "entries")
		if !reflect.DeepEqual(order, wantOrder) {
			return nil, fmt.Errorf("inspect subtree header fields are invalid")
		}
		view.View = fields["view"]
		if view.View != protocol.ViewDetailed {
			return nil, fmt.Errorf("inspect subtree view is invalid")
		}
		root, nodes, err := parseInspectV3Subtree(parsed.body, false)
		if err != nil {
			return nil, err
		}
		view.Root = root
		if root == nil || root.Ref != fields["root"] {
			return nil, fmt.Errorf("inspect subtree root does not match header")
		}
		if err := validateInspectV3Counts(fields, nodes, countNodeLines(parsed.body)); err != nil {
			return nil, err
		}
	case "query":
		prefixOrder := append(append([]string{}, baseOrder...), "matches")
		view.View = fields["view"]
		if view.View != protocol.ViewDetailed {
			return nil, fmt.Errorf("inspect query view is invalid")
		}
		matches, nodes, entries, err := parseInspectV3Query(parsed.body)
		if err != nil {
			return nil, err
		}
		view.Matches = matches
		wantOrder := prefixOrder
		if total, ok := fields["total"]; ok {
			value, err := strconv.Atoi(total)
			if err != nil || strconv.Itoa(value) != total {
				return nil, fmt.Errorf("total %q is not a canonical integer", total)
			}
			view.Total = value
			view.HasTotal = true
			wantOrder = append(wantOrder, "total")
		}
		if truncated, ok := fields["truncated"]; ok {
			value, err := strconv.ParseBool(truncated)
			if err != nil || (truncated != "true" && truncated != "false") {
				return nil, fmt.Errorf("truncated %q is not canonical", truncated)
			}
			view.Truncated = &value
			wantOrder = append(wantOrder, "truncated")
		}
		wantOrder = append(wantOrder, "nodes", "entries")
		if !reflect.DeepEqual(order, wantOrder) {
			return nil, fmt.Errorf("inspect query header fields are invalid")
		}
		if fields["matches"] != strconv.Itoa(len(matches)) {
			return nil, fmt.Errorf("inspect query match count is wrong")
		}
		if err := validateInspectV3Counts(fields, nodes, entries); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unexpected inspect scope %q", view.Scope)
	}
	return view, nil
}

func validateInspectV3Counts(fields map[string]string, nodes, entries int) error {
	if fields["nodes"] != strconv.Itoa(nodes) || fields["entries"] != strconv.Itoa(entries) {
		return fmt.Errorf("inspect counts nodes=%s entries=%s, want %d and %d",
			fields["nodes"], fields["entries"], nodes, entries)
	}
	return nil
}

func parseInspectV3Subtree(lines []string, allowChains bool) (*protocol.ReadNode, int, error) {
	if len(lines) == 0 {
		return nil, 0, fmt.Errorf("inspect subtree has no root")
	}
	type pendingNode struct {
		depth int
		node  *protocol.ReadNode
	}
	var root *protocol.ReadNode
	stack := []pendingNode{}
	refs := map[string]bool{}
	previousDepth := -1
	for index, line := range lines {
		if line == "" || strings.ContainsAny(line, "\r\t") {
			return nil, 0, fmt.Errorf("inspect line %d is empty or has forbidden whitespace", index+1)
		}
		spaces := 0
		for spaces < len(line) && line[spaces] == ' ' {
			spaces++
		}
		if spaces%2 != 0 {
			return nil, 0, fmt.Errorf("inspect line %d has odd indentation", index+1)
		}
		depth := spaces / 2
		if index == 0 && depth != 0 {
			return nil, 0, fmt.Errorf("inspect root is indented")
		}
		if index > 0 && depth > previousDepth+1 {
			return nil, 0, fmt.Errorf("inspect indentation skips a level")
		}
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		node, err := parseInspectV3NodeContent(line[spaces:])
		if err != nil {
			return nil, 0, fmt.Errorf("inspect line %d: %w", index+1, err)
		}
		if node.IsChain() && !allowChains {
			return nil, 0, fmt.Errorf("inspect v3 does not use chain definitions")
		}
		for _, ref := range node.Refs() {
			if refs[ref] {
				return nil, 0, fmt.Errorf("duplicate inspect ref %q", ref)
			}
			refs[ref] = true
		}
		if depth == 0 {
			if root != nil {
				return nil, 0, fmt.Errorf("inspect subtree has multiple roots")
			}
			root = node
		} else {
			if len(stack) == 0 || stack[len(stack)-1].depth != depth-1 {
				return nil, 0, fmt.Errorf("inspect node has no direct parent")
			}
			parent := stack[len(stack)-1].node
			parent.Children = append(parent.Children, node)
		}
		stack = append(stack, pendingNode{depth: depth, node: node})
		previousDepth = depth
	}
	return root, len(refs), nil
}

func parseInspectV3NodeContent(content string) (*protocol.ReadNode, error) {
	tokens, err := tokenizeTreeTextLineStrict(content)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("inspect node has no columns")
	}
	node := &protocol.ReadNode{
		Label: []string{}, Actions: []protocol.Action{}, Children: []*protocol.ReadNode{},
		Bounds: protocol.HiddenBounds(), Enabled: testBoolPtr(true),
		Focused: testBoolPtr(false), Selected: testBoolPtr(false),
		Protected: testBoolPtr(false), Checked: testBoolPtr(false),
	}
	index := 0
	switch {
	case tokens[0] == "chain":
		refs, next, err := parseChainRefsValue(tokens, 1)
		if err != nil {
			return nil, err
		}
		if next >= len(tokens) || !protocol.Role(tokens[next]).Valid() {
			return nil, fmt.Errorf("inspect chain has no valid role")
		}
		node.Chain = refs
		node.Role = protocol.Role(tokens[next])
		index = next + 1
	case strings.HasPrefix(tokens[0], "["):
		refs, err := parseAnonymousChainShorthand(tokens)
		if err != nil {
			return nil, err
		}
		node.Chain = refs
		node.Role = protocol.RoleView
		return node, nil
	default:
		if !protocol.ValidPublicRef(tokens[0]) {
			return nil, fmt.Errorf("inspect node has no valid ref")
		}
		node.Ref = tokens[0]
		index = 1
	}
	if index < len(tokens) && protocol.Role(tokens[index]).Valid() {
		if len(node.Chain) == 0 {
			node.Role = protocol.Role(tokens[index])
			index++
		}
	} else if index < len(tokens) && strings.HasPrefix(tokens[index], `"`) {
		if len(node.Chain) == 0 {
			node.Role = protocol.RoleText
		}
	}
	if !node.Role.Valid() {
		if len(node.Chain) > 0 {
			return nil, fmt.Errorf("inspect chain has no valid role")
		}
		node.Role = protocol.RoleView
	}
	if index < len(tokens) && (tokens[index] == "-" || strings.HasPrefix(tokens[index], "[")) {
		if tokens[index] == "-" {
			node.Bounds = protocol.UnknownBounds()
		} else {
			token := tokens[index]
			if !strings.HasPrefix(token, "[") || !strings.HasSuffix(token, "]") {
				return nil, fmt.Errorf("inspect geometry is not bracketed")
			}
			bounds, err := parseBoundsValue(strings.TrimSuffix(strings.TrimPrefix(token, "["), "]"))
			if err != nil {
				return nil, err
			}
			node.Bounds = protocol.KnownBounds(bounds)
		}
		index++
	}
	if err := parseNodeTailFields(node, tokens[index:], false, false); err != nil {
		return nil, err
	}
	return node, nil
}

func parseInspectV3Query(lines []string) ([]protocol.InspectionMatch, int, int, error) {
	matches := []protocol.InspectionMatch{}
	nodes := 0
	entries := 0
	index := 0
	totalMatches := -1
	for index < len(lines) {
		if lines[index] == "" {
			return nil, 0, 0, fmt.Errorf("inspect query has an unexpected blank line")
		}
		line := lines[index]
		prefix := "match " + strconv.Itoa(len(matches)+1) + "/"
		if !strings.HasPrefix(line, prefix) {
			return nil, 0, 0, fmt.Errorf("malformed inspect match line %q", line)
		}
		rest := strings.TrimPrefix(line, prefix)
		totalText, pathText, ok := strings.Cut(rest, " path=[")
		if !ok || !strings.HasSuffix(pathText, "]") || strings.Contains(totalText, " ") {
			return nil, 0, 0, fmt.Errorf("malformed inspect match line %q", line)
		}
		total, err := parseCanonicalPositive(totalText)
		if err != nil {
			return nil, 0, 0, err
		}
		inside := strings.TrimSuffix(pathText, "]")
		path := []string{}
		if inside != "" {
			if strings.Contains(inside, "  ") || strings.TrimSpace(inside) != inside {
				return nil, 0, 0, fmt.Errorf("inspect match path spacing is invalid")
			}
			path = strings.Split(inside, " ")
			for _, ref := range path {
				if !protocol.ValidPublicRef(ref) {
					return nil, 0, 0, fmt.Errorf("invalid inspect path ref %q", ref)
				}
			}
		}
		if totalMatches < 0 {
			totalMatches = total
		}
		if total != totalMatches || total < len(matches)+1 {
			return nil, 0, 0, fmt.Errorf("inspect match denominator is inconsistent")
		}
		index++
		subtree := []string{}
		for index < len(lines) && lines[index] != "" {
			subtree = append(subtree, lines[index])
			index++
		}
		node, count, err := parseInspectV3Subtree(subtree, false)
		if err != nil {
			return nil, 0, 0, err
		}
		matches = append(matches, protocol.InspectionMatch{Node: node, Path: path})
		nodes += count
		entries += len(subtree)
		if index < len(lines) {
			if lines[index] != "" {
				return nil, 0, 0, fmt.Errorf("inspect matches require a blank separator")
			}
			index++
			if index == len(lines) {
				return nil, 0, 0, fmt.Errorf("inspect query has a trailing blank line")
			}
		}
	}
	if totalMatches >= 0 && totalMatches != len(matches) {
		return nil, 0, 0, fmt.Errorf("inspect query expected %d matches, decoded %d", totalMatches, len(matches))
	}
	return matches, nodes, entries, nil
}

func parseAnonymousChainShorthand(tokens []string) ([]string, error) {
	refs, next, err := parseChainRefsValue(tokens, 0)
	if err != nil {
		return nil, err
	}
	if next != len(tokens) {
		return nil, fmt.Errorf("shorthand must not carry trailing columns")
	}
	return refs, nil
}

func parseChainRefsValue(tokens []string, start int) ([]string, int, error) {
	if start >= len(tokens) || !strings.HasPrefix(tokens[start], "[") {
		return nil, 0, fmt.Errorf("malformed chain ref list")
	}
	var raw strings.Builder
	index := start
	for {
		if index >= len(tokens) {
			return nil, 0, fmt.Errorf("chain definition is not closed")
		}
		if index > start {
			raw.WriteByte(' ')
		}
		raw.WriteString(tokens[index])
		index++
		if strings.HasSuffix(tokens[index-1], "]") {
			break
		}
	}
	inside := raw.String()
	if !strings.HasPrefix(inside, "[") || !strings.HasSuffix(inside, "]") {
		return nil, 0, fmt.Errorf("malformed chain ref list")
	}
	refs := strings.Fields(inside[1 : len(inside)-1])
	if len(refs) < 2 {
		return nil, 0, fmt.Errorf("chain definition has fewer than two refs")
	}
	for _, ref := range refs {
		if !protocol.ValidPublicRef(ref) {
			return nil, 0, fmt.Errorf("chain definition has invalid ref %q", ref)
		}
	}
	return refs, index, nil
}

func tokenizeTreeTextLine(line string) []string {
	tokens := []string{}
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, character := range line {
		if escaped {
			current.WriteRune(character)
			escaped = false
			continue
		}
		if inQuote {
			current.WriteRune(character)
			if character == '\\' {
				escaped = true
			} else if character == '"' {
				inQuote = false
			}
			continue
		}
		switch character {
		case ' ':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		case '"':
			inQuote = true
			current.WriteRune(character)
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func unquoteTreeTextToken(t *testing.T, token string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal([]byte(token), &value); err != nil {
		t.Fatalf("invalid JSON string token %q: %v", token, err)
	}
	return value
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := protocol.JSON(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// mustJSONValue decodes the canonical JSON into generic values so map key
// order never affects equality.
func mustJSONValue(t *testing.T, value any) any {
	t.Helper()
	encoded, err := protocol.JSON(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal canonical JSON: %v", err)
	}
	return decoded
}

func countNodeLines(lines []string) int {
	count := 0
	for _, line := range lines {
		if line != "" {
			count++
		}
	}
	return count
}

func countDeltaChanges(lines []string) int {
	count := 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "~ "),
			strings.HasPrefix(line, "+ "),
			strings.HasPrefix(line, "- "),
			strings.HasPrefix(line, "= "),
			strings.HasPrefix(line, "children "):
			count++
		}
	}
	return count
}

func countInspectNodeLines(lines []string) int {
	count := 0
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "match ") {
			continue
		}
		count++
	}
	return count
}

func treeTextLineForRef(t *testing.T, block, ref string) string {
	t.Helper()
	for _, line := range strings.Split(block, "\n") {
		content := strings.TrimLeft(line, " ")
		if content == "" {
			continue
		}
		tokens := tokenizeTreeTextLine(content)
		if len(tokens) > 0 && tokens[0] == ref {
			return line
		}
	}
	t.Fatalf("tree block has no line for ref %q:\n%s", ref, block)
	return ""
}

// expandTreeTextRoles makes every recoverable ordinary role and anonymous view
// chain explicit. It is only a measurement baseline for the compaction test.
func expandTreeTextRoles(block string) string {
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	expanded := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "tree ") || line == "end tree" {
			expanded = append(expanded, line)
			continue
		}
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		tokens := tokenizeTreeTextLine(line[spaces:])
		if len(tokens) == 0 {
			expanded = append(expanded, line)
			continue
		}
		if strings.HasPrefix(tokens[0], "[") {
			expanded = append(expanded, line[:spaces]+"chain "+strings.Join(tokens, " ")+" view")
			continue
		}
		if tokens[0] == "chain" || (len(tokens) > 1 && protocol.Role(tokens[1]).Valid()) {
			expanded = append(expanded, line)
			continue
		}
		role := protocol.RoleView
		if len(tokens) > 1 && strings.HasPrefix(tokens[1], `"`) {
			role = protocol.RoleText
		}
		explicit := line[:spaces] + tokens[0] + " " + string(role)
		if len(tokens) > 1 {
			explicit += " " + strings.Join(tokens[1:], " ")
		}
		expanded = append(expanded, explicit)
	}
	return strings.Join(expanded, "\n") + "\n"
}

func assertCompleteTreeText(t *testing.T, block, prefix, suffix string, minimumBytes int) {
	t.Helper()
	if strings.Contains(block, "omitted") {
		t.Fatalf("complete block contains omitted:\n%s", block)
	}
	if !strings.HasPrefix(block, prefix) {
		t.Fatalf("block does not start with %q:\n%s", prefix, block)
	}
	if !strings.HasSuffix(block, suffix) {
		t.Fatalf("block does not end with %q:\n%s", suffix, block)
	}
	if len(block) < minimumBytes {
		t.Fatalf("complete block is %d bytes, want at least %d", len(block), minimumBytes)
	}
}

// internalTestNode builds one internal projection node for the tree-text
// regression of the compact geometry policy.
func internalTestNode(ref string, role protocol.Role, rect *protocol.Bounds,
	label string, actions ...protocol.Action) *protocol.Node {
	yes, no := true, false
	labels := []string{}
	if label != "" {
		labels = append(labels, label)
	}
	return &protocol.Node{
		Ref: ref, Role: role, Bounds: rect, Label: labels, Actions: actions,
		Enabled: &yes, Focused: &no, Selected: &no, Protected: &no, Checked: &no,
		Children: []*protocol.Node{},
	}
}

// TestTreeTextReflectsCompactBoundsPolicy renders a real compact projection so
// a hidden text node carries no geometry column while action and display
// rectangles keep it, and a rectangle that crosses the one-percent threshold
// becomes an explicit set bounds=hidden.
func TestTreeTextReflectsCompactBoundsPolicy(t *testing.T) {
	yes, no := true, false
	buildTree := func(imageRight int) *protocol.Tree {
		// Keep the image outside the T2a pruning predicate so this fixture
		// isolates the compact geometry threshold.
		thresholdImage := internalTestNode("n4", protocol.RoleImage,
			&protocol.Bounds{Left: 0, Top: 500, Right: imageRight, Bottom: 700}, "缩略图")
		thresholdImage.Enabled = &no
		root := &protocol.Node{
			Ref: "n1", Role: protocol.RoleRoot,
			Bounds: &protocol.Bounds{Left: 0, Top: 0, Right: 1080, Bottom: 2400},
			Label:  []string{}, Actions: []protocol.Action{},
			Enabled: &yes, Focused: &no, Selected: &no, Protected: &no, Checked: &no,
			Children: []*protocol.Node{
				internalTestNode("n2", protocol.RoleButton,
					&protocol.Bounds{Left: 10, Top: 10, Right: 50, Bottom: 50},
					"确定", protocol.ActionTap),
				internalTestNode("n3", protocol.RoleText,
					&protocol.Bounds{Left: 20, Top: 60, Right: 200, Bottom: 100}, "标题"),
				thresholdImage,
			},
		}
		return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
	}
	base := observation.ProjectCompactTree(buildTree(300))
	fullBlock := mustRenderTreeText(t, &protocol.Result{
		Observation: &protocol.Observation{
			ID: "O1", Tree: &protocol.ReadTreeValue{Full: base},
		},
	})
	lines := strings.Split(fullBlock, "\n")
	find := func(token string) string {
		for _, line := range lines {
			if strings.Contains(line, token) {
				return line
			}
		}
		return ""
	}
	if line := find(" n2 button "); !strings.Contains(line, "10,10,50,50") {
		t.Fatalf("action node lost its rectangle: %q", line)
	}
	if line := find(" n3 "); line != ` n3 "标题"` {
		t.Fatalf("actionless text must omit both role and geometry: %q", line)
	}
	if line := find(" n4 image "); !strings.Contains(line, "0,500,300,700") ||
		!strings.Contains(line, `"缩略图"`) || !strings.Contains(line, "enabled=false") {
		t.Fatalf("retained threshold fixture must carry its public fields and rectangle: %q", line)
	}

	current := observation.ProjectCompactTree(buildTree(100))
	delta, err := observation.ReadDiff("O1", base, current)
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	deltaBlock := mustRenderTreeText(t, &protocol.Result{
		Observation: &protocol.Observation{
			ID: "O2", Tree: &protocol.ReadTreeValue{Delta: delta},
		},
	})
	if !strings.Contains(deltaBlock, "~ n4 bounds=hidden") {
		t.Fatalf("threshold crossing must render bounds=hidden:\n%s", deltaBlock)
	}
}
