package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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

const testTreeTextFullGolden = `tree v3 format=full id=O3 scope=reported_visible_with_context view=compact nodes=9 entries=9

n1 root [0,0,1080,2400]
  n2 group [0,0,1080,220]
    n3 button [24,60,144,180] "返回" tap
    n4 text [300,80,780,160] "设置"
    n6 switch [900,60,1032,180] "启用" tap checked=true
  n5 scroll [0,220,1080,2400] axis=y
    n12 group -
      n13 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
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

const testTreeTextDeltaGolden = `tree v3 format=delta id=O4 base=O3 view=compact scope=reported_visible_with_context changes=6

~ n7 label=["WLAN 已关闭"] actions=["tap"]
~ n8 checked=false
+ n14 group [0,460,1080,580]
  n15 text [48,490,700,550] "移动网络" tap
- n16
children n5 = [n6 n9 n14]
= n12 group [0,1800,1080,2100]
  n91 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
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

// TestTreeTextChainDefinition pins the v3 explicit chain definition: consecutive
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
	want := "tree v3 format=full id=O5 scope=reported_visible_with_context view=compact nodes=4 entries=3\n\n" +
		"n1 root [0,0,1080,2400]\n" +
		"  chain [n2 n3] view [0,0,1080,120]\n" +
		"    n4 text [0,0,1080,60] \"标题\"\nend tree\n"
	if block != want {
		t.Fatalf("chain block mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, want)
	}
	if got, want := headerInt(t, parseTreeTextBlock(t, block).header, "nodes"), 4; got != want {
		t.Fatalf("chain nodes = %d, want %d", got, want)
	}
	if got, want := headerInt(t, parseTreeTextBlock(t, block).header, "entries"), 3; got != want {
		t.Fatalf("chain entries = %d, want %d", got, want)
	}
	parsed := parseFullTreeText(t, block)
	if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
		t.Fatalf("chain definition did not round-trip:\n got %s\nwant %s", got, want)
	}
}

func TestTreeTextV3OmitsViewRoles(t *testing.T) {
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
			if testCase.name == "no-fields" && line != "  n2" {
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

func TestTreeTextV3RoleRecoveryMatrix(t *testing.T) {
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

func TestTreeTextV3ChainAndGeometry(t *testing.T) {
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
	want := "tree v3 format=full id=O1 scope=reported_visible_with_context view=compact nodes=18 entries=10\n\n" +
		"n1 root [0,0,1080,2400]\n" +
		"  [n10 n11]\n" +
		"    n12 \"child\"\n" +
		"  chain [n20 n21] group\n" +
		"  chain [n30 n31] view [1,2,3,4]\n" +
		"  chain [n32 n33] view -\n" +
		"  chain [n34 n35] view context\n" +
		"  chain [n36 n37] view enabled=null\n" +
		"  chain [n38 n39] view checked=null\n" +
		"  chain [n40 n41] view \"chain label\"\n" +
		"end tree\n"
	if block != want {
		t.Fatalf("chain rendering mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, want)
	}
	parsedBlock := parseTreeTextBlock(t, block)
	if got, want := headerInt(t, parsedBlock.header, "nodes"), 18; got != want {
		t.Fatalf("chain nodes = %d, want %d", got, want)
	}
	if got, want := headerInt(t, parsedBlock.header, "entries"), 10; got != want {
		t.Fatalf("chain entries = %d, want %d", got, want)
	}
	parsed := parseFullTreeText(t, block)
	if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
		t.Fatalf("chain definitions did not round-trip:\n got %s\nwant %s", got, want)
	}
}

func TestTreeTextV3ChainDelta(t *testing.T) {
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
	want := "tree v3 format=delta id=O2 base=O1 view=compact scope=reported_visible_with_context changes=4\n\n" +
		"+ n50 group [0,0,100,100]\n" +
		"  [n51 n52]\n" +
		"    n53 \"moved\"\n" +
		"- n70\n" +
		"children n60 = [n61 n62]\n" +
		"= n60 button [0,0,10,10]\n" +
		"  chain [n61 n62] group\n" +
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

func TestTreeTextV3CompactBytesWin(t *testing.T) {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	view := testNode("n2", protocol.RoleView, protocol.HiddenBounds())
	text := testNode("n3", protocol.RoleText, protocol.HiddenBounds())
	text.Label = []string{"title"}
	chain := testNode("", protocol.RoleView, protocol.HiddenBounds())
	chain.Chain = []string{"n10", "n11"}
	child := testNode("n12", protocol.RoleText, protocol.HiddenBounds())
	child.Label = []string{"child"}
	chain.Children = []*protocol.ReadNode{child}
	tap := testNode("n4", protocol.RoleView, testBounds(0, 0, 10, 10))
	tap.Actions = []protocol.Action{protocol.ActionTap}
	root.Children = []*protocol.ReadNode{view, text, chain, tap}
	tree := &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: root}
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.ReadTreeValue{Full: tree},
	}})
	if !strings.Contains(block, "\n  [n10 n11]\n") || !strings.Contains(block, "\n  n2\n") {
		t.Fatalf("fixture does not exercise v3 omissions:\n%s", block)
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
	if got, want := headerInt(t, full.header, "entries"), countNodeLines(full.body); got != want {
		t.Fatalf("full entries = %d, want %d", got, want)
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
		assertCompleteTreeText(t, block, "tree v3 format=full id=O3 ", "end tree\n", 24*1024)
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
		assertCompleteTreeText(t, block, "tree v3 format=delta id=O4 base=O3 ", "end tree\n", 24*1024)
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
		assertCompleteTreeText(t, block, "tree v3 format=full id=O3 ", "end tree\n", observationFullBytes)
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
	want := "tree v3 format=full id=O1 scope=reported_visible_with_context view=compact nodes=1 entries=1\n" +
		blockSeparator +
		"n1 root [0,0,1080,2400]\nend tree\n"
	if block != want {
		t.Fatalf("empty tree block = %q, want %q", block, want)
	}

	delta := &protocol.ReadDelta{Format: "delta", Base: "O1", View: protocol.ViewCompact}
	block = mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O2", Tree: &protocol.ReadTreeValue{Delta: delta},
	}})
	want = "tree v3 format=delta id=O2 base=O1 view=compact scope=reported_visible_with_context changes=0\n" +
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
			header:   "tree v3 format=full id=O1 scope=reported_visible_with_context view=compact nodes=1 entries=1",
			trailer:  "end tree\n",
		},
		{
			name: "observe", tool: "observe", arguments: `{"reference":"O1"}`,
			response: `{"operation_id":"P2","observation":` + observation + `}`,
			header:   "tree v3 format=full id=O1 scope=reported_visible_with_context view=compact nodes=1 entries=1",
			trailer:  "end tree\n",
		},
		{
			name: "observe-delta", tool: "observe", arguments: `{"reference":"O1"}`,
			response: `{"operation_id":"P2","observation":{"status":"complete","id":"O2","revision":2,"tree":` + testDeltaTreeJSON + `}}`,
			header:   "tree v3 format=delta id=O2 base=O1 view=compact scope=reported_visible_with_context changes=0",
			trailer:  "end tree\n",
		},
		{
			name: "act", tool: "act", arguments: `{"reference":"O1","operation":{"type":"key","key":"BACK"}}`,
			response: `{"operation_id":"P2","outcome":"executed","observation":` + observation + `}`,
			header:   "tree v3 format=full id=O1 scope=reported_visible_with_context view=compact nodes=1 entries=1",
			trailer:  "end tree\n",
		},
		{
			name: "app-open", tool: "app", arguments: `{"kind":"open","reference":"O1","target":{"app":"com.example.probe"}}`,
			response: `{"operation_id":"P2","outcome":"executed","app":{"kind":"open",` +
				`"package":"com.example.probe","task_id":42,"display_id":12,"ownership":"owned",` +
				`"reused":false,"state_preserved":false},"observation":` + observation + `}`,
			header:  "tree v3 format=full id=O1 scope=reported_visible_with_context view=compact nodes=1 entries=1",
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
			if !strings.HasPrefix(text, "result v2 is_error=false\n") || !strings.HasSuffix(text, "end result\n") {
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
			if strings.Count(text, "\n\ntree v3 ") != 1 && strings.Count(text, "\n\ninspect v3 ") != 1 {
				t.Fatalf("%s text does not carry exactly one tree block:\n%s", testCase.name, text)
			}
			structured := testStructuredJSON(t, result)
			switch {
			case testCase.tool == "inspect":
				requireTestContains(t, structured, `"scope":"tree"`)
			case strings.Contains(testCase.header, "format=delta"):
				requireTestContains(t, structured, `"format":"delta"`)
			default:
				requireTestContains(t, structured, `"format":"full"`)
			}
		})
	}
}

func TestTreeTextParserV3Gate(t *testing.T) {
	for _, header := range []string{
		"tree v3 format=full",
		"inspect v3 scope=tree",
	} {
		if err := validateTreeTextHeaderVersion(header); err != nil {
			t.Errorf("v3 header rejected: %q: %v", header, err)
		}
	}
	for _, header := range []string{
		"tree v2 format=full",
		"inspect v2 scope=tree",
		"tree v4 format=full",
		"shell v1 termination=exited",
	} {
		if err := validateTreeTextHeaderVersion(header); err == nil {
			t.Errorf("non-v3 header accepted: %q", header)
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

// parseTreeTextBlock checks the v3 frame: header, one blank line, the body and
// the trailer.
func parseTreeTextBlock(t *testing.T, block string) parsedTreeTextBlock {
	t.Helper()
	if !strings.HasSuffix(block, "\n") {
		t.Fatalf("block does not end with a newline: %q", block)
	}
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("block has %d lines", len(lines))
	}
	if lines[1] != "" {
		t.Fatalf("block header is not followed by a blank line: %q", block)
	}
	if err := validateTreeTextHeaderVersion(lines[0]); err != nil {
		t.Fatalf("unsupported tree text block: %v", err)
	}
	return parsedTreeTextBlock{
		header:  lines[0],
		body:    lines[2 : len(lines)-1],
		trailer: lines[len(lines)-1],
	}
}

func validateTreeTextHeaderVersion(header string) error {
	tokens := strings.Fields(header)
	if len(tokens) < 2 {
		return fmt.Errorf("header has too few fields: %q", header)
	}
	kind := tokens[0] + " " + tokens[1]
	switch kind {
	case "tree v3":
		return nil
	case "inspect v3":
		return nil
	case "tree v2", "inspect v2":
		return fmt.Errorf("retired block version %q", kind)
	default:
		return fmt.Errorf("unsupported block version %q", kind)
	}
}

func parseHeaderFields(t *testing.T, header string) (string, map[string]string) {
	t.Helper()
	tokens := strings.Fields(header)
	if len(tokens) < 2 {
		t.Fatalf("header has too few fields: %q", header)
	}
	fields := make(map[string]string, len(tokens)-2)
	for _, token := range tokens[2:] {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			t.Fatalf("header field %q has no '='", token)
		}
		fields[key] = value
	}
	return tokens[0] + " " + tokens[1], fields
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
	parsed := parseTreeTextBlock(t, block)
	kind, fields := parseHeaderFields(t, parsed.header)
	if kind != "tree v3" || fields["format"] != "full" {
		t.Fatalf("not a full tree block: %q", parsed.header)
	}
	view := fields["view"]
	if view != protocol.ViewCompact && view != protocol.ViewDetailed {
		t.Fatalf("full tree block has an invalid view: %q", parsed.header)
	}
	return &protocol.ReadTree{
		Format: "full",
		Scope:  fields["scope"],
		View:   view,
		Root:   parseNodeLines(t, parsed.body),
	}
}

func parseDeltaText(t *testing.T, block string) *protocol.ReadDelta {
	t.Helper()
	parsed := parseTreeTextBlock(t, block)
	kind, fields := parseHeaderFields(t, parsed.header)
	if kind != "tree v3" || fields["format"] != "delta" {
		t.Fatalf("not a delta block: %q", parsed.header)
	}
	delta := &protocol.ReadDelta{Format: "delta", Base: fields["base"], View: fields["view"]}
	for index := 0; index < len(parsed.body); {
		line := parsed.body[index]
		switch {
		case strings.HasPrefix(line, "~ "):
			ref, set := parseSetLine(t, line)
			if delta.Set == nil {
				delta.Set = map[string]protocol.ReadSetNode{}
			}
			delta.Set[ref] = set
			index++
		case strings.HasPrefix(line, "+ "), strings.HasPrefix(line, "= "):
			prefix := line[:2]
			subtree := []string{line[2:]}
			index++
			for index < len(parsed.body) && strings.HasPrefix(parsed.body[index], "  ") {
				subtree = append(subtree, parsed.body[index])
				index++
			}
			node := parseNodeLines(t, subtree)
			if prefix == "+ " {
				delta.Add = append(delta.Add, node)
			} else {
				if delta.Replace == nil {
					delta.Replace = map[string]*protocol.ReadNode{}
				}
				delta.Replace[node.Ref] = node
			}
		case strings.HasPrefix(line, "- "):
			delta.Remove = append(delta.Remove, strings.TrimPrefix(line, "- "))
			index++
		case strings.HasPrefix(line, "children "):
			ref, children := parseChildrenLine(t, line)
			if delta.Children == nil {
				delta.Children = map[string][]string{}
			}
			delta.Children[ref] = children
			index++
		default:
			t.Fatalf("unexpected delta line: %q", line)
		}
	}
	return delta
}

func parseInspectionText(t *testing.T, block string) *protocol.InspectionView {
	t.Helper()
	parsed := parseTreeTextBlock(t, block)
	kind, fields := parseHeaderFields(t, parsed.header)
	if kind != "inspect v3" {
		t.Fatalf("not an inspect block: %q", parsed.header)
	}
	revision, err := strconv.Atoi(fields["revision"])
	if err != nil {
		t.Fatalf("revision is not an integer: %q", fields["revision"])
	}
	view := &protocol.InspectionView{
		ObservationID: fields["observation"],
		Revision:      revision,
		CapturedAt:    fields["captured_at"],
		Scope:         fields["scope"],
	}
	switch view.Scope {
	case "tree":
		view.Tree = &protocol.ReadTree{
			Format: "full",
			Scope:  fields["tree_scope"],
			View:   fields["view"],
			Root:   parseNodeLines(t, parsed.body),
		}
	case "subtree":
		view.View = fields["view"]
		view.Root = parseNodeLines(t, parsed.body)
	case "query":
		view.View = fields["view"]
		view.Matches = parseQueryMatches(t, parsed.body)
		if total, ok := fields["total"]; ok {
			value, err := strconv.Atoi(total)
			if err != nil {
				t.Fatalf("total is not an integer: %q", total)
			}
			view.Total = value
			view.HasTotal = true
		}
		if truncated, ok := fields["truncated"]; ok {
			value, err := strconv.ParseBool(truncated)
			if err != nil {
				t.Fatalf("truncated is not a bool: %q", truncated)
			}
			view.Truncated = &value
		}
	default:
		t.Fatalf("unexpected inspect scope: %q", view.Scope)
	}
	return view
}

func parseQueryMatches(t *testing.T, lines []string) []protocol.InspectionMatch {
	t.Helper()
	matches := []protocol.InspectionMatch{}
	for index := 0; index < len(lines); {
		if lines[index] == "" {
			index++
			continue
		}
		line := lines[index]
		if !strings.HasPrefix(line, "match ") {
			t.Fatalf("unexpected query line: %q", line)
		}
		rest := strings.TrimPrefix(line, "match ")
		at := strings.Index(rest, " path=[")
		if at < 0 || !strings.HasSuffix(rest, "]") {
			t.Fatalf("malformed match line: %q", line)
		}
		inside := strings.TrimSuffix(rest[at+len(" path=["):], "]")
		index++
		subtree := []string{}
		for index < len(lines) && lines[index] != "" {
			subtree = append(subtree, lines[index])
			index++
		}
		matches = append(matches, protocol.InspectionMatch{
			Node: parseNodeLines(t, subtree),
			Path: strings.Fields(inside),
		})
	}
	return matches
}

func parseNodeLines(t *testing.T, lines []string) *protocol.ReadNode {
	t.Helper()
	type pendingNode struct {
		depth int
		node  *protocol.ReadNode
	}
	var root *protocol.ReadNode
	stack := []pendingNode{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		depth, node := parseNodeLine(t, line)
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			if root != nil {
				t.Fatalf("tree text has more than one root")
			}
			root = node
		} else {
			parent := stack[len(stack)-1].node
			parent.Children = append(parent.Children, node)
		}
		stack = append(stack, pendingNode{depth: depth, node: node})
	}
	if root == nil {
		return nil
	}
	return root
}

// parseNodeLine parses the v3 node columns: ref or chain, an optional ordinary
// role, an optional bounds column, labels, value/hint, bare actions and the
// flag columns. Omitted roles follow the first surviving column: a quoted label
// restores text, everything else restores view. A hidden geometry column is
// omitted; "-" is unknown geometry. Unwritten tri-state flags take the
// wire-decode default: enabled=true, focused/selected/protected=false and
// checked=false (checked=null stays nil).
func parseNodeLine(t *testing.T, line string) (int, *protocol.ReadNode) {
	t.Helper()
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	if spaces%2 != 0 {
		t.Fatalf("node line has odd indentation: %q", line)
	}
	tokens := tokenizeTreeTextLine(line[spaces:])
	if len(tokens) == 0 {
		t.Fatalf("node line has too few tokens: %q", line)
	}
	node := &protocol.ReadNode{
		Label: []string{}, Actions: []protocol.Action{}, Children: []*protocol.ReadNode{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
		Checked: testBoolPtr(false),
	}
	index := 0
	switch {
	case tokens[0] == "chain":
		if len(tokens) < 4 || !strings.HasPrefix(tokens[1], "[") {
			t.Fatalf("malformed chain definition: %q", line)
		}
		refs, next := parseChainRefs(t, tokens, 1, line)
		node.Chain = refs
		index = next
		if index >= len(tokens) {
			t.Fatalf("chain definition has no role: %q", line)
		}
		node.Role = protocol.Role(tokens[index])
		if !node.Role.Valid() {
			t.Fatalf("chain definition has invalid role: %q", line)
		}
		index++
	case strings.HasPrefix(tokens[0], "["):
		refs, err := parseAnonymousChainShorthand(tokens)
		if err != nil {
			t.Fatalf("invalid anonymous view chain shorthand %q: %v", line, err)
		}
		node.Chain = refs
		node.Role = protocol.RoleView
		node.Bounds = protocol.HiddenBounds()
		return spaces / 2, node
	default:
		node.Ref = tokens[0]
		index = 1
		if index < len(tokens) && protocol.Role(tokens[index]).Valid() {
			node.Role = protocol.Role(tokens[index])
			index++
		} else if index < len(tokens) && strings.HasPrefix(tokens[index], `"`) {
			node.Role = protocol.RoleText
		} else {
			node.Role = protocol.RoleView
		}
	}
	if index < len(tokens) && (tokens[index] == "-" || strings.HasPrefix(tokens[index], "[")) {
		if tokens[index] == "-" {
			node.Bounds = protocol.UnknownBounds()
		} else {
			node.Bounds = protocol.KnownBounds(parseBoundsToken(t, tokens[index]))
		}
		index++
	} else {
		node.Bounds = protocol.HiddenBounds()
	}
	for _, token := range tokens[index:] {
		switch {
		case strings.HasPrefix(token, `"`):
			node.Label = append(node.Label, unquoteTreeTextToken(t, token))
		case strings.HasPrefix(token, "value="):
			node.Value = testStringPtr(unquoteTreeTextToken(t, strings.TrimPrefix(token, "value=")))
		case strings.HasPrefix(token, "hint="):
			node.Hint = testStringPtr(unquoteTreeTextToken(t, strings.TrimPrefix(token, "hint=")))
		case token == string(protocol.ActionTap), token == string(protocol.ActionLongPress),
			token == string(protocol.ActionText), token == string(protocol.ActionSwipe):
			node.Actions = append(node.Actions, protocol.Action(token))
		case strings.HasPrefix(token, "enabled="):
			switch strings.TrimPrefix(token, "enabled=") {
			case "true":
				node.Enabled = testBoolPtr(true)
			case "false":
				node.Enabled = testBoolPtr(false)
			case "null":
				node.Enabled = nil
			default:
				t.Fatalf("bad enabled token: %q", token)
			}
		case strings.HasPrefix(token, "focused="):
			node.Focused = parseTriBoolToken(t, token)
		case strings.HasPrefix(token, "selected="):
			node.Selected = parseTriBoolToken(t, token)
		case strings.HasPrefix(token, "protected="):
			node.Protected = parseTriBoolToken(t, token)
		case strings.HasPrefix(token, "checked="):
			node.Checked = parseOptionalBoolToken(t, token)
		case strings.HasPrefix(token, "axis="):
			axis := protocol.Axis(strings.TrimPrefix(token, "axis="))
			node.Axis = axis
		case strings.HasPrefix(token, "resource="):
			node.Resource = testStringPtr(unquoteTreeTextToken(t, strings.TrimPrefix(token, "resource=")))
		case token == "context":
			node.Context = true
		default:
			t.Fatalf("unexpected node token: %q", token)
		}
	}
	return spaces / 2, node
}

func parseChainRefs(t *testing.T, tokens []string, start int, line string) ([]string, int) {
	t.Helper()
	refs, next, err := parseChainRefsValue(tokens, start)
	if err != nil {
		t.Fatalf("%v: %q", err, line)
	}
	return refs, next
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

func parseTriBoolToken(t *testing.T, token string) *bool {
	t.Helper()
	_, value, _ := strings.Cut(token, "=")
	switch value {
	case "true":
		return testBoolPtr(true)
	case "false":
		return testBoolPtr(false)
	case "null":
		return nil
	default:
		t.Fatalf("bad tri-bool token: %q", token)
		return nil
	}
}

func parseOptionalBoolToken(t *testing.T, token string) *bool {
	t.Helper()
	_, value, _ := strings.Cut(token, "=")
	switch value {
	case "true":
		return testBoolPtr(true)
	case "false":
		return testBoolPtr(false)
	case "null":
		return nil
	default:
		t.Fatalf("bad optional bool token: %q", token)
		return nil
	}
}

func parseBoundsToken(t *testing.T, token string) protocol.Bounds {
	t.Helper()
	if !strings.HasPrefix(token, "[") || !strings.HasSuffix(token, "]") {
		t.Fatalf("bad bounds token: %q", token)
	}
	parts := strings.Split(token[1:len(token)-1], ",")
	if len(parts) != 4 {
		t.Fatalf("bounds token has %d parts: %q", len(parts), token)
	}
	values := make([]int, 4)
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			t.Fatalf("bounds part %q is not an integer", part)
		}
		values[index] = value
	}
	return protocol.Bounds{Left: values[0], Top: values[1], Right: values[2], Bottom: values[3]}
}

func parseSetLine(t *testing.T, line string) (string, protocol.ReadSetNode) {
	t.Helper()
	tokens := tokenizeTreeTextLine(strings.TrimPrefix(line, "~ "))
	if len(tokens) == 0 {
		t.Fatalf("empty set line")
	}
	ref := tokens[0]
	var set protocol.ReadSetNode
	for _, token := range tokens[1:] {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			t.Fatalf("set field %q has no '='", token)
		}
		switch key {
		case "label":
			set.Label = protocol.Field[[]string]{Set: true, Value: parseStringArrayOrNull(t, value)}
		case "actions":
			raw := parseStringArrayOrNull(t, value)
			var actions []protocol.Action
			if raw != nil {
				actions = make([]protocol.Action, len(raw))
				for index, action := range raw {
					actions[index] = protocol.Action(action)
				}
			}
			set.Actions = protocol.Field[[]protocol.Action]{Set: true, Value: actions}
		case "bounds":
			switch value {
			case "null":
				set.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.UnknownBounds()}
			case "hidden":
				set.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.HiddenBounds()}
			default:
				set.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.KnownBounds(parseBoundsToken(t, value))}
			}
		case "enabled":
			set.Enabled = parseOptionalBoolField(t, value)
		case "focused":
			set.Focused = parseOptionalBoolField(t, value)
		case "selected":
			set.Selected = parseOptionalBoolField(t, value)
		case "checked":
			set.Checked = parseOptionalBoolField(t, value)
		case "value":
			set.Value = parseOptionalStringField(t, value)
		case "hint":
			set.Hint = parseOptionalStringField(t, value)
		case "protected":
			set.Protected = parseOptionalBoolField(t, value)
		case "axis":
			if value == "null" {
				set.Axis = protocol.Field[*protocol.Axis]{Set: true}
			} else {
				axis := protocol.Axis(value)
				set.Axis = protocol.Field[*protocol.Axis]{Set: true, Value: &axis}
			}
		case "resource":
			set.Resource = parseOptionalStringField(t, value)
		default:
			t.Fatalf("unexpected set field: %q", token)
		}
	}
	return ref, set
}

func parseOptionalBoolField(t *testing.T, raw string) protocol.Field[*bool] {
	t.Helper()
	switch raw {
	case "null":
		return protocol.Field[*bool]{Set: true}
	case "true":
		return protocol.Field[*bool]{Set: true, Value: testBoolPtr(true)}
	case "false":
		return protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)}
	default:
		t.Fatalf("bad bool field: %q", raw)
		return protocol.Field[*bool]{}
	}
}

func parseOptionalStringField(t *testing.T, raw string) protocol.Field[*string] {
	t.Helper()
	if raw == "null" {
		return protocol.Field[*string]{Set: true}
	}
	return protocol.Field[*string]{Set: true, Value: testStringPtr(unquoteTreeTextToken(t, raw))}
}

func parseStringArrayOrNull(t *testing.T, raw string) []string {
	t.Helper()
	if raw == "null" {
		return nil
	}
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		t.Fatalf("bad string array: %q", raw)
	}
	inside := raw[1 : len(raw)-1]
	if inside == "" {
		return []string{}
	}
	parts := splitQuotedList(t, inside)
	values := make([]string, len(parts))
	for index, part := range parts {
		values[index] = unquoteTreeTextToken(t, part)
	}
	return values
}

func splitQuotedList(t *testing.T, raw string) []string {
	t.Helper()
	parts := []string{}
	var current strings.Builder
	inQuote := false
	escaped := false
	for _, character := range raw {
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
		case '"':
			inQuote = true
			current.WriteRune(character)
		case ',':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func parseChildrenLine(t *testing.T, line string) (string, []string) {
	t.Helper()
	rest := strings.TrimPrefix(line, "children ")
	at := strings.Index(rest, " = [")
	if at < 0 || !strings.HasSuffix(rest, "]") {
		t.Fatalf("malformed children line: %q", line)
	}
	ref := rest[:at]
	inside := rest[at+len(" = ["):]
	inside = strings.TrimSuffix(inside, "]")
	if strings.TrimSpace(inside) == "" {
		return ref, []string{}
	}
	return ref, strings.Fields(inside)
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
				internalTestNode("n4", protocol.RoleImage,
					&protocol.Bounds{Left: 0, Top: 500, Right: imageRight, Bottom: 700}, ""),
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
	if line := find(" n2 button "); !strings.Contains(line, "[10,10,50,50]") {
		t.Fatalf("action node lost its rectangle: %q", line)
	}
	if line := find(" n3 "); line != `  n3 "标题"` {
		t.Fatalf("actionless text must omit both role and geometry: %q", line)
	}
	if line := find(" n4 image "); !strings.Contains(line, "[0,500,300,700]") {
		t.Fatalf("large display rectangle must be delivered: %q", line)
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
