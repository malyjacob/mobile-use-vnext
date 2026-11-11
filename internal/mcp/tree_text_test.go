package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func testBoolPtr(value bool) *bool { return &value }

func testStringPtr(value string) *string { return &value }

func testBoundsPtr(left, top, right, bottom int) *protocol.Bounds {
	return &protocol.Bounds{Left: left, Top: top, Right: right, Bottom: bottom}
}

func testAxisPtr(value protocol.Axis) *protocol.Axis { return &value }

func testNode(ref string, role protocol.Role, bounds *protocol.Bounds) *protocol.Node {
	return &protocol.Node{
		Ref: ref, Role: role, Bounds: bounds,
		Label:   []string{},
		Actions: []protocol.Action{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
		Children: []*protocol.Node{},
	}
}

func testLargeTree(childCount, labelBytes int) *protocol.Tree {
	root := testNode("n1", protocol.RoleRoot, testBoundsPtr(0, 0, 1080, 2400))
	for index := 2; index < childCount+2; index++ {
		child := testNode("n"+strconv.Itoa(index), protocol.RoleText, testBoundsPtr(0, 0, 1080, 80))
		child.Label = []string{strings.Repeat("x", labelBytes)}
		root.Children = append(root.Children, child)
	}
	return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
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

// testFullTree is the synthetic tree used by the full golden test.
func testFullTree() *protocol.Tree {
	root := testNode("n1", protocol.RoleRoot, testBoundsPtr(0, 0, 1080, 2400))
	group := testNode("n2", protocol.RoleGroup, testBoundsPtr(0, 0, 1080, 220))

	back := testNode("n3", protocol.RoleButton, testBoundsPtr(24, 60, 144, 180))
	back.Label = []string{"返回"}
	back.Actions = []protocol.Action{protocol.ActionTap}

	title := testNode("n4", protocol.RoleText, testBoundsPtr(300, 80, 780, 160))
	title.Label = []string{"设置"}

	scroll := testNode("n5", protocol.RoleScroll, testBoundsPtr(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY

	group12 := testNode("n12", protocol.RoleGroup, nil)
	search := testNode("n13", protocol.RoleInput, testBoundsPtr(48, 1850, 1032, 1990))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}

	group.Children = []*protocol.Node{back, title}
	group12.Children = []*protocol.Node{search}
	scroll.Children = []*protocol.Node{group12}
	root.Children = []*protocol.Node{group, scroll}
	return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
}

const testTreeTextFullGolden = `tree v1 format=full id=O3 scope=reported_visible_with_context nodes=7

n1 root [0,0,1080,2400]
  n2 group [0,0,1080,220]
    n3 button [24,60,144,180] "返回" tap
    n4 text [300,80,780,160] "设置"
  n5 scroll [0,220,1080,2400] axis=y
    n12 group -
      n13 input [48,1850,1032,1990] "搜索设置" value="" hint="输入关键词" tap text
end tree
`

func testDelta() *protocol.Delta {
	add := testNode("n14", protocol.RoleGroup, testBoundsPtr(0, 460, 1080, 580))
	mobile := testNode("n15", protocol.RoleText, testBoundsPtr(48, 490, 700, 550))
	mobile.Label = []string{"移动网络"}
	mobile.Actions = []protocol.Action{protocol.ActionTap}
	add.Children = []*protocol.Node{mobile}

	replace := testNode("n12", protocol.RoleGroup, testBoundsPtr(0, 1800, 1080, 2100))
	search := testNode("n91", protocol.RoleInput, testBoundsPtr(48, 1850, 1032, 1990))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}
	replace.Children = []*protocol.Node{search}

	return &protocol.Delta{
		Format: "delta", Base: "O3",
		Set: map[string]protocol.SetNode{
			"n8": {Checked: protocol.Field[*bool]{Set: true, Value: testBoolPtr(false)}},
			"n7": {
				Label:   protocol.Field[[]string]{Set: true, Value: []string{"WLAN 已关闭"}},
				Actions: protocol.Field[[]protocol.Action]{Set: true, Value: []protocol.Action{protocol.ActionTap}},
			},
		},
		Add:      []*protocol.Node{add},
		Remove:   []string{"n16"},
		Children: map[string][]string{"n5": {"n6", "n9", "n14"}},
		Replace:  map[string]*protocol.Node{"n12": replace},
	}
}

const testTreeTextDeltaGolden = `tree v1 format=delta id=O4 base=O3 scope=reported_visible_with_context changes=6

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
	root := testNode("n1", protocol.RoleRoot, testBoundsPtr(0, 0, 1080, 2400))
	scroll := testNode("n5", protocol.RoleScroll, testBoundsPtr(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY
	confirm := testNode("n7", protocol.RoleButton, testBoundsPtr(40, 600, 240, 680))
	confirm.Label = []string{"确定"}
	confirm.Actions = []protocol.Action{protocol.ActionTap}
	scroll.Children = []*protocol.Node{confirm}
	root.Children = []*protocol.Node{scroll}
	return &protocol.InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 4,
		CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
		Tree: &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root},
	}
}

func testInspectionSubtreeView() *protocol.InspectionView {
	scroll := testNode("n5", protocol.RoleScroll, testBoundsPtr(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY
	confirm := testNode("n7", protocol.RoleButton, testBoundsPtr(40, 600, 240, 680))
	confirm.Label = []string{"确定"}
	confirm.Actions = []protocol.Action{protocol.ActionTap}
	scroll.Children = []*protocol.Node{confirm}
	return &protocol.InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 4,
		CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
		Root: scroll,
	}
}

func testInspectionQueryView() *protocol.InspectionView {
	button := testNode("n7", protocol.RoleButton, testBoundsPtr(40, 600, 240, 680))
	button.Label = []string{"确定"}
	button.Actions = []protocol.Action{protocol.ActionTap}

	group := testNode("n9", protocol.RoleGroup, testBoundsPtr(0, 700, 1080, 900))
	text := testNode("n10", protocol.RoleText, testBoundsPtr(40, 720, 600, 780))
	text.Label = []string{"确定"}
	cancel := testNode("n11", protocol.RoleButton, testBoundsPtr(40, 800, 240, 880))
	cancel.Label = []string{"取消"}
	cancel.Actions = []protocol.Action{protocol.ActionTap}
	group.Children = []*protocol.Node{text, cancel}

	return &protocol.InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 4,
		CapturedAt: "2026-11-10T03:00:00Z", Scope: "query",
		Matches: []protocol.InspectionMatch{
			{Node: button, Path: []string{"n1", "n5"}},
			{Node: group, Path: []string{"n1", "n6"}},
		},
		Total: 3, HasTotal: true, Truncated: testBoolPtr(true),
	}
}

const testTreeTextInspectTreeGolden = `inspect v1 scope=tree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context nodes=3

n1 root [0,0,1080,2400]
  n5 scroll [0,220,1080,2400] axis=y
    n7 button [40,600,240,680] "确定" tap
end inspect
`

const testTreeTextInspectSubtreeGolden = `inspect v1 scope=subtree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context root=n5 nodes=2

n5 scroll [0,220,1080,2400] axis=y
  n7 button [40,600,240,680] "确定" tap
end inspect
`

const testTreeTextInspectQueryGolden = `inspect v1 scope=query observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z tree_scope=reported_visible_with_context matches=2 total=3 truncated=true nodes=4

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
		Tree: &protocol.TreeValue{Full: testFullTree()},
	}}
	if block := mustRenderTreeText(t, result); block != testTreeTextFullGolden {
		t.Fatalf("full block mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextFullGolden)
	}
}

func TestTreeTextDeltaGolden(t *testing.T) {
	result := &protocol.Result{Observation: &protocol.Observation{
		Status: "complete", ID: "O4", Revision: 4,
		Tree: &protocol.TreeValue{Delta: testDelta()},
	}}
	if block := mustRenderTreeText(t, result); block != testTreeTextDeltaGolden {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s", block, testTreeTextDeltaGolden)
	}
}

func TestTreeTextDeltaExplicitValues(t *testing.T) {
	delta := &protocol.Delta{
		Format: "delta", Base: "O1",
		Set: map[string]protocol.SetNode{
			"n1": {
				Label:     protocol.Field[[]string]{Set: true, Value: []string{}},
				Actions:   protocol.Field[[]protocol.Action]{Set: true, Value: []protocol.Action{}},
				Bounds:    protocol.Field[*protocol.Bounds]{Set: true},
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
				Bounds:    protocol.Field[*protocol.Bounds]{Set: true, Value: testBoundsPtr(1, 2, 3, 4)},
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
		},
	}
	result := &protocol.Result{Observation: &protocol.Observation{
		ID: "O9", Tree: &protocol.TreeValue{Delta: delta},
	}}
	block := mustRenderTreeText(t, result)
	for _, want := range []string{
		"~ n1 label=[] actions=[] bounds=null enabled=false focused=false selected=false checked=true value=\"\" hint=\"h\" protected=false axis=y resource=\"r\"",
		"~ n2 label=null actions=null bounds=[1,2,3,4] enabled=null focused=true selected=null checked=null value=null hint=null protected=true axis=null resource=null",
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
		{Observation: &protocol.Observation{ID: "O3", Tree: &protocol.TreeValue{Full: testFullTree()}}},
		{Observation: &protocol.Observation{ID: "O4", Tree: &protocol.TreeValue{Delta: testDelta()}}},
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
			ID: "O3", Tree: &protocol.TreeValue{Full: tree},
		}})
		parsed := parseFullTreeText(t, block)
		if got, want := mustJSON(t, parsed), mustJSON(t, tree); got != want {
			t.Fatalf("full round-trip mismatch:\n got %s\nwant %s", got, want)
		}
	})
	t.Run("delta", func(t *testing.T) {
		delta := testDelta()
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O4", Tree: &protocol.TreeValue{Delta: delta},
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
}

func TestTreeTextEscapes(t *testing.T) {
	label := "quote\" slash\\ line\n tab\t nul\x00 unit\x1f html<>& cjk中文 bad" + string([]byte{0xff})
	root := testNode("n1", protocol.RoleRoot, testBoundsPtr(0, 0, 1, 1))
	root.Label = []string{label, "second[label]=value"}
	root.Resource = testStringPtr("res<&>")
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.TreeValue{Full: &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}},
	}})
	parsed := parseTreeTextBlock(t, block)
	if len(parsed.body) != 1 {
		t.Fatalf("escaped label produced %d body lines:\n%s", len(parsed.body), block)
	}
	for _, want := range []string{
		`quote\" slash\\ line\n tab\t nul\u0000 unit\u001f html<>& cjk中文 bad` + "\uFFFD",
		`"second[label]=value"`,
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
	if got, want := roundTrip.Root.Resource, "res<&>"; got == nil || *got != want {
		t.Fatalf("escaped resource did not round-trip: %v", got)
	}
}

func TestTreeTextKeepsPreviouslyBoundedBlocks(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		tree := testLargeTree(699, 60)
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O3", Tree: &protocol.TreeValue{Full: tree},
		}})
		assertCompleteTreeText(t, block, "tree v1 format=full id=O3 ", "end tree\n", 24*1024)
	})

	t.Run("delta", func(t *testing.T) {
		delta := &protocol.Delta{Format: "delta", Base: "O3", Set: map[string]protocol.SetNode{}}
		for index := 1; index <= 600; index++ {
			delta.Set["n"+strconv.Itoa(index)] = protocol.SetNode{
				Label: protocol.Field[[]string]{Set: true, Value: []string{strings.Repeat("y", 60)}},
			}
		}
		block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
			ID: "O4", Tree: &protocol.TreeValue{Delta: delta},
		}})
		assertCompleteTreeText(t, block, "tree v1 format=delta id=O4 base=O3 ", "end tree\n", 24*1024)
	})

	t.Run("inspect", func(t *testing.T) {
		tree := testLargeTree(799, 60)
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
			Tree: tree,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v1 scope=tree observation=O2 ", "end inspect\n", 32*1024)
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
			ID: "O3", Tree: &protocol.TreeValue{Full: tree},
		}}
		block := mustRenderTreeText(t, result)
		assertCompleteTreeText(t, block, "tree v1 format=full id=O3 ", "end tree\n", observationFullBytes)
		if got, want := mustJSON(t, parseFullTreeText(t, block)), mustJSON(t, tree); got != want {
			t.Fatalf("observation full tree did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("observation full tree text bytes=%d", len(block))
	})

	t.Run("inspection-full-128KiB", func(t *testing.T) {
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
			Tree: testLargeTree(200, 700),
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v1 scope=tree observation=O2 ", "end inspect\n", inspectionFullBytes)
		if got, want := mustJSON(t, parseInspectionText(t, block)), mustJSON(t, view); got != want {
			t.Fatalf("inspection full tree did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("inspection full tree text bytes=%d", len(block))
	})

	t.Run("inspection-subtree-32KiB", func(t *testing.T) {
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
			Root: testLargeTree(80, 700).Root,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v1 scope=subtree observation=O2 ", "end inspect\n", narrowInspectionBytes)
		if got, want := mustJSON(t, parseInspectionText(t, block)), mustJSON(t, view); got != want {
			t.Fatalf("inspection subtree did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("inspection subtree text bytes=%d", len(block))
	})

	t.Run("inspection-query-32KiB", func(t *testing.T) {
		matches := make([]protocol.InspectionMatch, 0, 80)
		for index := 2; index < 82; index++ {
			match := testNode("n"+strconv.Itoa(index), protocol.RoleText, testBoundsPtr(0, 0, 1080, 80))
			match.Label = []string{strings.Repeat("q", 700)}
			matches = append(matches, protocol.InspectionMatch{Node: match, Path: []string{"n1"}})
		}
		view := &protocol.InspectionView{
			ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "query",
			Matches: matches,
		}
		block := mustRenderTreeText(t, &protocol.Result{Inspection: view})
		assertCompleteTreeText(t, block, "inspect v1 scope=query observation=O2 ", "end inspect\n", narrowInspectionBytes)
		if got, want := mustJSON(t, parseInspectionText(t, block)), mustJSON(t, view); got != want {
			t.Fatalf("inspection query did not round-trip:\n got %s\nwant %s", got, want)
		}
		t.Logf("inspection query text bytes=%d", len(block))
	})
}

func TestTreeTextRejectsUnrepresentableMetadata(t *testing.T) {
	validTree := func() *protocol.Tree {
		return &protocol.Tree{Format: "full", Scope: protocol.Scope,
			Root: testNode("n1", protocol.RoleRoot, nil)}
	}
	cases := []struct {
		name   string
		result *protocol.Result
	}{
		{
			name: "missing observation id",
			result: &protocol.Result{Observation: &protocol.Observation{
				Tree: &protocol.TreeValue{Full: validTree()},
			}},
		},
		{
			name: "tree value has both formats",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID:   "O3",
				Tree: &protocol.TreeValue{Full: validTree(), Delta: &protocol.Delta{Format: "delta", Base: "O1"}},
			}},
		},
		{
			name: "full format",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Full: &protocol.Tree{
					Format: "delta", Scope: protocol.Scope,
					Root: testNode("n1", protocol.RoleRoot, nil),
				}},
			}},
		},
		{
			name: "full scope missing",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Full: &protocol.Tree{
					Format: "full", Root: testNode("n1", protocol.RoleRoot, nil),
				}},
			}},
		},
		{
			name: "full scope invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Full: &protocol.Tree{
					Format: "full", Scope: "visible",
					Root: testNode("n1", protocol.RoleRoot, nil),
				}},
			}},
		},
		{
			name: "full role invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Full: &protocol.Tree{
					Format: "full", Scope: protocol.Scope,
					Root: testNode("n1", protocol.Role("card"), nil),
				}},
			}},
		},
		{
			name: "full action invalid",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, nil)
				root.Actions = []protocol.Action{protocol.Action("hover")}
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.TreeValue{Full: &protocol.Tree{
						Format: "full", Scope: protocol.Scope, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "full axis invalid",
			result: func() *protocol.Result {
				root := testNode("n1", protocol.RoleRoot, nil)
				root.Axis = protocol.Axis("diagonal")
				return &protocol.Result{Observation: &protocol.Observation{
					ID: "O3", Tree: &protocol.TreeValue{Full: &protocol.Tree{
						Format: "full", Scope: protocol.Scope, Root: root,
					}},
				}}
			}(),
		},
		{
			name: "delta format",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Delta: &protocol.Delta{Format: "full", Base: "O1"}},
			}},
		},
		{
			name: "delta base missing",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Delta: &protocol.Delta{Format: "delta"}},
			}},
		},
		{
			name: "delta base token invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Delta: &protocol.Delta{Format: "delta", Base: "O1 bad"}},
			}},
		},
		{
			name: "delta set action invalid",
			result: &protocol.Result{Observation: &protocol.Observation{
				ID: "O3", Tree: &protocol.TreeValue{Delta: &protocol.Delta{
					Format: "delta", Base: "O1",
					Set: map[string]protocol.SetNode{
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
				Tree: &protocol.Tree{Format: "full", Root: testNode("n1", protocol.RoleRoot, nil)},
			}},
		},
		{
			name: "inspection subtree root missing",
			result: &protocol.Result{Inspection: &protocol.InspectionView{
				ObservationID: "O2", CapturedAt: "2026-11-10T03:00:00Z", Scope: "subtree",
			}},
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
		ID: "O3", Tree: &protocol.TreeValue{Full: full},
	}})

	full = testFullTree()
	full.Root.Children[0].Ref = "bad ref"
	requireNoTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O3", Tree: &protocol.TreeValue{Full: full},
	}})

	cases := []*protocol.Delta{
		{Format: "delta", Base: "O3", Set: map[string]protocol.SetNode{"n0": {}}},
		{Format: "delta", Base: "O3", Children: map[string][]string{"n 1": {"n2"}}},
		{Format: "delta", Base: "O3", Children: map[string][]string{"n1": {"n02"}}},
		{Format: "delta", Base: "O3", Add: []*protocol.Node{testNode("n02", protocol.RoleGroup, nil)}},
		{Format: "delta", Base: "O3", Remove: []string{"n1\n2"}},
		{Format: "delta", Base: "O3", Replace: map[string]*protocol.Node{"n1": testNode("n03", protocol.RoleGroup, nil)}},
	}
	for index, delta := range cases {
		t.Run("delta-"+strconv.Itoa(index), func(t *testing.T) {
			requireNoTreeText(t, &protocol.Result{Observation: &protocol.Observation{
				ID: "O4", Tree: &protocol.TreeValue{Delta: delta},
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
			ID: "O3", Tree: &protocol.TreeValue{Full: testFullTree()},
		}}, "end tree\n"},
		{"delta", &protocol.Result{Observation: &protocol.Observation{
			ID: "O4", Tree: &protocol.TreeValue{Delta: testDelta()},
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
	root := testNode("n1", protocol.RoleRoot, testBoundsPtr(0, 0, 1080, 2400))
	block := mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O1", Tree: &protocol.TreeValue{Full: &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}},
	}})
	want := "tree v1 format=full id=O1 scope=reported_visible_with_context nodes=1\n\n" +
		"n1 root [0,0,1080,2400]\nend tree\n"
	if block != want {
		t.Fatalf("empty tree block = %q, want %q", block, want)
	}

	delta := &protocol.Delta{Format: "delta", Base: "O1"}
	block = mustRenderTreeText(t, &protocol.Result{Observation: &protocol.Observation{
		ID: "O2", Tree: &protocol.TreeValue{Delta: delta},
	}})
	want = "tree v1 format=delta id=O2 base=O1 scope=reported_visible_with_context changes=0\n\nend tree\n"
	if block != want {
		t.Fatalf("empty delta block = %q, want %q", block, want)
	}

	empty := &protocol.InspectionView{
		ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "tree",
		Tree: &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root},
	}
	block = mustRenderTreeText(t, &protocol.Result{Inspection: empty})
	want = "inspect v1 scope=tree observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z " +
		"tree_scope=reported_visible_with_context nodes=1\n\n" +
		"n1 root [0,0,1080,2400]\nend inspect\n"
	if block != want {
		t.Fatalf("empty inspection block = %q, want %q", block, want)
	}

	query := &protocol.InspectionView{
		ObservationID: "O2", Revision: 4, CapturedAt: "2026-11-10T03:00:00Z", Scope: "query",
		Matches: nil,
	}
	block = mustRenderTreeText(t, &protocol.Result{Inspection: query})
	want = "inspect v1 scope=query observation=O2 revision=4 captured_at=2026-11-10T03:00:00Z " +
		"tree_scope=reported_visible_with_context matches=0 nodes=0\n\nend inspect\n"
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
		{Observation: &protocol.Observation{Status: "complete", ID: "O1", Tree: &protocol.TreeValue{}}},
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
			header:   "tree v1 format=full id=O1 scope=reported_visible_with_context nodes=1",
			trailer:  "end tree\n",
		},
		{
			name: "observe", tool: "observe", arguments: `{"reference":"O1"}`,
			response: `{"operation_id":"P2","observation":` + observation + `}`,
			header:   "tree v1 format=full id=O1 scope=reported_visible_with_context nodes=1",
			trailer:  "end tree\n",
		},
		{
			name: "observe-delta", tool: "observe", arguments: `{"reference":"O1"}`,
			response: `{"operation_id":"P2","observation":{"status":"complete","id":"O2","revision":2,"tree":` + testDeltaTreeJSON + `}}`,
			header:   "tree v1 format=delta id=O2 base=O1 scope=reported_visible_with_context changes=0",
			trailer:  "end tree\n",
		},
		{
			name: "act", tool: "act", arguments: `{"observation_id":"O1","operation":{"type":"key","key":"BACK"}}`,
			response: `{"operation_id":"P2","outcome":"executed","observation":` + observation + `}`,
			header:   "tree v1 format=full id=O1 scope=reported_visible_with_context nodes=1",
			trailer:  "end tree\n",
		},
		{
			name: "app-open", tool: "app", arguments: `{"kind":"open","observation_id":"O1","app":"com.example.probe"}`,
			response: `{"operation_id":"P2","outcome":"executed","app":{"kind":"open",` +
				`"package":"com.example.probe","task_id":42,"display_id":12,"ownership":"owned",` +
				`"reused":false,"state_preserved":false},"observation":` + observation + `}`,
			header:  "tree v1 format=full id=O1 scope=reported_visible_with_context nodes=1",
			trailer: "end tree\n",
		},
		{
			name: "inspect", tool: "inspect", arguments: `{"observation_id":"O1"}`,
			response: `{"inspection":{"observation_id":"O1","origin":"P1","revision":1,` +
				`"captured_at":"2026-11-09T00:00:01Z","scope":"tree","tree":` + testFullTreeJSON + `}}`,
			header:  "inspect v1 scope=tree observation=O1 revision=1 captured_at=2026-11-09T00:00:01Z tree_scope=reported_visible_with_context nodes=1",
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
			if !strings.HasPrefix(text, "result v1 is_error=false\n") || !strings.HasSuffix(text, "end result\n") {
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
			if strings.Count(text, "\n\ntree v1 ") != 1 && strings.Count(text, "\n\ninspect v1 ") != 1 {
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

// ---------------------------------------------------------------- text parser

type parsedTreeTextBlock struct {
	header  string
	body    []string
	trailer string
}

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
	return parsedTreeTextBlock{
		header:  lines[0],
		body:    lines[2 : len(lines)-1],
		trailer: lines[len(lines)-1],
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

func parseFullTreeText(t *testing.T, block string) *protocol.Tree {
	t.Helper()
	parsed := parseTreeTextBlock(t, block)
	kind, fields := parseHeaderFields(t, parsed.header)
	if kind != "tree v1" || fields["format"] != "full" {
		t.Fatalf("not a full tree block: %q", parsed.header)
	}
	return &protocol.Tree{
		Format: "full",
		Scope:  fields["scope"],
		Root:   parseNodeLines(t, parsed.body),
	}
}

func parseDeltaText(t *testing.T, block string) *protocol.Delta {
	t.Helper()
	parsed := parseTreeTextBlock(t, block)
	kind, fields := parseHeaderFields(t, parsed.header)
	if kind != "tree v1" || fields["format"] != "delta" {
		t.Fatalf("not a delta block: %q", parsed.header)
	}
	delta := &protocol.Delta{Format: "delta", Base: fields["base"]}
	for index := 0; index < len(parsed.body); {
		line := parsed.body[index]
		switch {
		case strings.HasPrefix(line, "~ "):
			ref, set := parseSetLine(t, line)
			if delta.Set == nil {
				delta.Set = map[string]protocol.SetNode{}
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
					delta.Replace = map[string]*protocol.Node{}
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
	if kind != "inspect v1" {
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
		view.Tree = &protocol.Tree{
			Format: "full",
			Scope:  fields["tree_scope"],
			Root:   parseNodeLines(t, parsed.body),
		}
	case "subtree":
		view.Root = parseNodeLines(t, parsed.body)
	case "query":
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

func parseNodeLines(t *testing.T, lines []string) *protocol.Node {
	t.Helper()
	type pendingNode struct {
		depth int
		node  *protocol.Node
	}
	var root *protocol.Node
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

func parseNodeLine(t *testing.T, line string) (int, *protocol.Node) {
	t.Helper()
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	if spaces%2 != 0 {
		t.Fatalf("node line has odd indentation: %q", line)
	}
	tokens := tokenizeTreeTextLine(line[spaces:])
	if len(tokens) < 3 {
		t.Fatalf("node line has too few tokens: %q", line)
	}
	node := &protocol.Node{
		Ref: tokens[0], Role: protocol.Role(tokens[1]),
		Label: []string{}, Actions: []protocol.Action{}, Children: []*protocol.Node{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
	}
	if tokens[2] != "-" {
		node.Bounds = parseBoundsToken(t, tokens[2])
	}
	for _, token := range tokens[3:] {
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

func parseBoundsToken(t *testing.T, token string) *protocol.Bounds {
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
	return &protocol.Bounds{Left: values[0], Top: values[1], Right: values[2], Bottom: values[3]}
}

func parseSetLine(t *testing.T, line string) (string, protocol.SetNode) {
	t.Helper()
	tokens := tokenizeTreeTextLine(strings.TrimPrefix(line, "~ "))
	if len(tokens) == 0 {
		t.Fatalf("empty set line")
	}
	ref := tokens[0]
	var set protocol.SetNode
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
			if value == "null" {
				set.Bounds = protocol.Field[*protocol.Bounds]{Set: true}
			} else {
				set.Bounds = protocol.Field[*protocol.Bounds]{Set: true, Value: parseBoundsToken(t, value)}
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
