package treetext

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func testBoolPtr(value bool) *bool { return &value }

func testStringPtr(value string) *string { return &value }

func testRect(left, top, right, bottom int) protocol.ReadBounds {
	return protocol.KnownBounds(protocol.Bounds{
		Left: left, Top: top, Right: right, Bottom: bottom,
	})
}

func testPoint(x, y int) protocol.ReadBounds { return protocol.PointBounds(x, y) }

func testNode(
	ref string,
	role protocol.Role,
	bounds protocol.ReadBounds,
) *protocol.ReadNode {
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

// testFullTree exercises the compact tree v5 geometry split: the synthetic root
// and scroll containers carry rectangles while every other delivered node
// carries only its center point.
func testFullTree() *protocol.ReadTree {
	root := testNode("n1", protocol.RoleRoot, testRect(0, 0, 1080, 2400))
	group := testNode("n2", protocol.RoleGroup, testPoint(540, 110))

	back := testNode("n3", protocol.RoleButton, testPoint(84, 120))
	back.Label = []string{"返回"}
	back.Actions = []protocol.Action{protocol.ActionTap}

	title := testNode("n4", protocol.RoleText, testPoint(540, 120))
	title.Label = []string{"设置"}

	toggle := testNode("n6", protocol.RoleSwitch, testPoint(966, 120))
	toggle.Label = []string{"启用"}
	toggle.Actions = []protocol.Action{protocol.ActionTap}
	toggle.Checked = testBoolPtr(true)

	scroll := testNode("n5", protocol.RoleScroll, testRect(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY

	group12 := testNode("n12", protocol.RoleGroup, protocol.UnknownBounds())
	search := testNode("n13", protocol.RoleInput, testPoint(540, 1920))
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
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope,
		View: protocol.ViewCompact, Root: root,
	}
}

const fullGolden = `tree v5 full nodes=9

n1 root 0,0,1080,2400
 n2 group 540,110
  n3 button 84,120 "返回"
  n4 text 540,120 "设置"
  n6 switch 966,120 "启用" tap checked=true
 n5 scroll 0,220,1080,2400 !swipe axis=y
  n12 group -
   n13 input 540,1920 "搜索设置" value="" hint="输入关键词" tap
  n14 group checked=null
end tree
`

func testDelta() *protocol.ReadDelta {
	add := testNode("n14", protocol.RoleGroup, testPoint(540, 520))
	mobile := testNode("n15", protocol.RoleText, testPoint(374, 520))
	mobile.Label = []string{"移动网络"}
	mobile.Actions = []protocol.Action{protocol.ActionTap}
	add.Children = []*protocol.ReadNode{mobile}

	// The replace root repeats the add root point on purpose: each definition is
	// an independent serialization and must not inherit its ancestor stack.
	replace := testNode("n12", protocol.RoleGroup, testPoint(540, 520))
	search := testNode("n91", protocol.RoleInput, testPoint(540, 1920))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}
	replace.Children = []*protocol.ReadNode{search}

	return &protocol.ReadDelta{
		Format: "delta", Base: "O3", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n8": {
				Checked: protocol.Field[*bool]{
					Set: true, Value: testBoolPtr(false),
				},
			},
			"n9": {
				Bounds: protocol.Field[protocol.ReadBounds]{
					Set: true, Value: testPoint(40, 50),
				},
			},
			"n7": {
				Label: protocol.Field[[]string]{
					Set: true, Value: []string{"WLAN 已关闭"},
				},
				Actions: protocol.Field[[]protocol.Action]{
					Set: true, Value: []protocol.Action{protocol.ActionTap},
				},
			},
		},
		Add:      []*protocol.ReadNode{add},
		Remove:   []string{"n16"},
		Children: map[string][]string{"n5": {"n6", "n9", "n14"}},
		Replace:  map[string]*protocol.ReadNode{"n12": replace},
	}
}

const deltaGolden = `tree v5 delta base=O3 changes=7

~ n7 label=["WLAN 已关闭"] actions=["tap"]
~ n8 checked=false
~ n9 bounds=[40,50]
+ n14 group 540,520
 n15 text 374,520 "移动网络" tap
- n16
children n5 = [n6 n9 n14]
= n12 group 540,520
 n91 input 540,1920 "搜索设置" value="" hint="输入关键词" tap
end tree
`

func TestEncodeFullGoldenBytes(t *testing.T) {
	block, ok := EncodeFull("O3", testFullTree())
	if !ok {
		t.Fatal("full tree is not encodable")
	}
	if block != fullGolden {
		t.Fatalf("full block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, fullGolden)
	}
}

func TestEncodeDeltaGoldenBytesAndShortBase(t *testing.T) {
	delta := testDelta()
	delta.Base = "O18446744073709551615_3"
	block, ok := EncodeDelta("O18446744073709551615_4", delta)
	if !ok {
		t.Fatal("delta is not encodable")
	}
	const wantHeader = "tree v5 delta base=_3 changes=7\n\n"
	if !strings.HasPrefix(block, wantHeader) {
		t.Fatalf("delta header = %q, want prefix %q", block, wantHeader)
	}
	withLongBase := deltaGolden
	withShortBase := strings.Replace(
		withLongBase,
		"tree v5 delta base=O3 changes=7\n\n",
		wantHeader,
		1,
	)
	if block != withShortBase {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, withShortBase)
	}
}

// TestEncodeFullEqualAncestorAbbreviation pins the @ rule: an ordinary node with
// delivered geometry abbreviates only when the nearest known ancestor geometry
// has the same Kind and payload. Hidden and unknown ancestors are skipped rather
// than resetting the chain, and a point never abbreviates a rectangle.
func TestEncodeFullEqualAncestorAbbreviation(t *testing.T) {
	root := testNode("n1", protocol.RoleRoot, testRect(0, 0, 100, 200))

	group := testNode("n2", protocol.RoleGroup, testPoint(10, 20))
	button := testNode("n3", protocol.RoleButton, testPoint(10, 20))
	button.Actions = []protocol.Action{protocol.ActionTap}
	unknown := testNode("n8", protocol.RoleGroup, protocol.UnknownBounds())
	deepButton := testNode("n9", protocol.RoleButton, testPoint(10, 20))
	deepButton.Actions = []protocol.Action{protocol.ActionTap}
	group.Children = []*protocol.ReadNode{button, unknown}
	unknown.Children = []*protocol.ReadNode{deepButton}

	equalScroll := testNode("n4", protocol.RoleScroll, testRect(0, 0, 100, 200))
	kindMismatch := testNode("n7", protocol.RoleButton, testPoint(0, 0))
	kindMismatch.Actions = []protocol.Action{protocol.ActionTap}
	equalScroll.Children = []*protocol.ReadNode{kindMismatch}

	movedScroll := testNode("n5", protocol.RoleScroll, testRect(0, 50, 100, 200))

	root.Children = []*protocol.ReadNode{group, equalScroll, movedScroll}
	tree := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope,
		View: protocol.ViewCompact, Root: root,
	}
	block, ok := EncodeFull("O1", tree)
	if !ok {
		t.Fatal("tree is not encodable")
	}
	const want = `tree v5 full nodes=8

n1 root 0,0,100,200
 n2 group 10,20
  n3 button @
  n8 group -
   n9 button @
 n4 scroll @ !swipe
  n7 button 0,0
 n5 scroll 0,50,100,200 !swipe
end tree
`
	if block != want {
		t.Fatalf("full block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, want)
	}
}

// TestEncodeFullTruncatedColumnOrder pins the compact truncation column: the
// true flag stays between resource and context, the default false is omitted,
// and a chain definition follows the same column order.
func TestEncodeFullTruncatedColumnOrder(t *testing.T) {
	root := testNode("n1", protocol.RoleRoot, testRect(0, 0, 1080, 2400))

	truncated := testNode("n2", protocol.RoleButton, testPoint(540, 120))
	truncated.Label = []string{"很长的标题"}
	truncated.Actions = []protocol.Action{protocol.ActionTap}
	truncated.Resource = testStringPtr("app:id/title")
	truncated.Truncated = true

	untruncated := testNode("n3", protocol.RoleButton, testPoint(540, 200))
	untruncated.Label = []string{"短标题"}
	untruncated.Actions = []protocol.Action{protocol.ActionTap}
	untruncated.Resource = testStringPtr("app:id/title")

	contextNode := testNode("n4", protocol.RoleText, testPoint(540, 280))
	contextNode.Label = []string{"说明"}
	contextNode.Resource = testStringPtr("app:id/desc")
	contextNode.Context = true

	chain := testNode("", protocol.RoleGroup, protocol.HiddenBounds())
	chain.Chain = []string{"n5", "n6"}
	chain.Label = []string{"折叠标题"}
	chain.Resource = testStringPtr("app:id/row")
	chain.Truncated = true

	child := testNode("n7", protocol.RoleButton, testPoint(540, 360))
	child.Label = []string{"继续"}
	child.Actions = []protocol.Action{protocol.ActionTap}
	chain.Children = []*protocol.ReadNode{child}

	root.Children = []*protocol.ReadNode{truncated, untruncated, contextNode, chain}
	tree := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope,
		View: protocol.ViewCompact, Root: root,
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("truncation fixture is invalid: %v", err)
	}
	block, ok := EncodeFull("O1", tree)
	if !ok {
		t.Fatal("tree with truncated labels is not encodable")
	}
	const want = `tree v5 full nodes=7

n1 root 0,0,1080,2400
 n2 button 540,120 "很长的标题" resource="app:id/title" truncated=true
 n3 button 540,200 "短标题" resource="app:id/title"
 n4 text 540,280 "说明" resource="app:id/desc" context
 chain [n5 n6] group "折叠标题" resource="app:id/row" truncated=true
  n7 button 540,360 "继续"
end tree
`
	if block != want {
		t.Fatalf("full block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, want)
	}
	// The flag sits after the resource value on both an ordinary and a chain
	// definition, and a false fact never leaks into a full node line.
	for _, marker := range []string{
		`"app:id/title" truncated=true`,
		`"app:id/row" truncated=true`,
	} {
		if !strings.Contains(block, marker) {
			t.Fatalf("block misses %q:\n%s", marker, block)
		}
	}
	if strings.Contains(block, "truncated=false") {
		t.Fatalf("full node line must omit a false truncation flag:\n%s", block)
	}

	// A truncated context node is refused at the public entry, so the fixed
	// resource -> truncated -> context order is pinned on the column writer
	// itself to keep the contract testable.
	contextColumns := testNode("n9", protocol.RoleText, testPoint(1, 2))
	contextColumns.Resource = testStringPtr("app:id/x")
	contextColumns.Truncated = true
	contextColumns.Context = true
	var body textBuffer
	writeNodeColumns(&body, contextColumns, false, false)
	const wantColumns = ` 1,2 resource="app:id/x" truncated=true context`
	if got := string(body.store); got != wantColumns {
		t.Fatalf("node columns = %q, want %q", got, wantColumns)
	}
}

// TestEncodeDeltaSetBoundsForms covers the four explicit ReadBounds forms on a
// set line: hidden, unknown, point and rectangle.
func TestEncodeDeltaSetBoundsForms(t *testing.T) {
	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n2": {Bounds: protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.HiddenBounds()}},
			"n3": {Bounds: protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.UnknownBounds()}},
			"n4": {Bounds: protocol.Field[protocol.ReadBounds]{Set: true, Value: testPoint(10, 20)}},
			"n5": {Bounds: protocol.Field[protocol.ReadBounds]{Set: true, Value: testRect(1, 2, 3, 4)}},
		},
	}
	block, ok := EncodeDelta("O2", delta)
	if !ok {
		t.Fatal("delta is not encodable")
	}
	const want = `tree v5 delta base=O1 changes=4

~ n2 bounds=hidden
~ n3 bounds=null
~ n4 bounds=[10,20]
~ n5 bounds=[1,2,3,4]
end tree
`
	if block != want {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, want)
	}
}

// TestEncodeDeltaSetTruncatedThreeStates keeps the set assignment three-state:
// an unset member stays omitted, while an explicit false and true are both
// written after resource.
func TestEncodeDeltaSetTruncatedThreeStates(t *testing.T) {
	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n2": {Truncated: protocol.Field[bool]{Set: true, Value: true}},
			"n3": {Truncated: protocol.Field[bool]{Set: true, Value: false}},
			"n4": {
				Label:    protocol.Field[[]string]{Set: true, Value: []string{"标题"}},
				Resource: protocol.Field[*string]{Set: true, Value: testStringPtr("app:id/title")},
			},
			"n5": {
				Resource:  protocol.Field[*string]{Set: true, Value: testStringPtr("app:id/row")},
				Truncated: protocol.Field[bool]{Set: true, Value: true},
			},
		},
	}
	block, ok := EncodeDelta("O2", delta)
	if !ok {
		t.Fatal("delta with truncation assignment is not encodable")
	}
	const want = `tree v5 delta base=O1 changes=4

~ n2 truncated=true
~ n3 truncated=false
~ n4 label=["标题"] resource="app:id/title"
~ n5 resource="app:id/row" truncated=true
end tree
`
	if block != want {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, want)
	}
	// An unset truncation member must not fabricate a value, and the set line
	// never spells the unrepresentable null form.
	if strings.Contains(block, "~ n4 label=[\"标题\"] resource=\"app:id/title\" truncated=") {
		t.Fatalf("unset truncation member leaked into the set line:\n%s", block)
	}
	if strings.Contains(block, "truncated=null") {
		t.Fatalf("set line must never write truncated=null:\n%s", block)
	}
}

// TestEncodeDeltaTruncatedDefinitions covers the same truncation column on the
// add and replace definition lines, which share the node column writer.
func TestEncodeDeltaTruncatedDefinitions(t *testing.T) {
	add := testNode("n2", protocol.RoleButton, testPoint(10, 20))
	add.Label = []string{"很长的标题"}
	add.Actions = []protocol.Action{protocol.ActionTap}
	add.Resource = testStringPtr("app:id/title")
	add.Truncated = true

	replace := testNode("n4", protocol.RoleGroup, protocol.HiddenBounds())
	replace.Truncated = true

	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Add:     []*protocol.ReadNode{add},
		Replace: map[string]*protocol.ReadNode{"n4": replace},
	}
	block, ok := EncodeDelta("O2", delta)
	if !ok {
		t.Fatal("delta definitions with truncation are not encodable")
	}
	const want = `tree v5 delta base=O1 changes=2

+ n2 button 10,20 "很长的标题" resource="app:id/title" truncated=true
= n4 group truncated=true
end tree
`
	if block != want {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, want)
	}
}

// TestEncodeChainDefinitionsRemainLossless keeps historical chain definitions
// byte-compatible with the v4 form: the chain line never uses @, hidden geometry
// stays omitted, and a hidden chain does not clear the nearest known ancestor for
// a following subtree.
func TestEncodeChainDefinitionsRemainLossless(t *testing.T) {
	root := testNode("n1", protocol.RoleRoot, testRect(0, 0, 100, 200))

	chain := testNode("", protocol.RoleGroup, protocol.HiddenBounds())
	chain.Chain = []string{"n2", "n3"}
	button := testNode("n4", protocol.RoleButton, testPoint(10, 20))
	button.Label = []string{"继续"}
	button.Actions = []protocol.Action{protocol.ActionTap}
	chain.Children = []*protocol.ReadNode{button}

	deepScroll := testNode("n5", protocol.RoleScroll, testRect(0, 0, 100, 200))
	root.Children = []*protocol.ReadNode{chain, deepScroll}
	tree := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope,
		View: protocol.ViewCompact, Root: root,
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("chain fixture is invalid: %v", err)
	}
	block, ok := EncodeFull("O1", tree)
	if !ok {
		t.Fatal("chain tree is not encodable")
	}
	const want = `tree v5 full nodes=5

n1 root 0,0,100,200
 chain [n2 n3] group
  n4 button 10,20 "继续"
 n5 scroll @ !swipe
end tree
`
	if block != want {
		t.Fatalf("chain block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, want)
	}
}

func TestEncodeRejectsNonCompactOrInvalidInputs(t *testing.T) {
	full := testFullTree()
	full.View = protocol.ViewDetailed
	if block, ok := EncodeFull("O1", full); ok || block != "" {
		t.Fatalf("detailed full encoding = (%q, %t), want rejection", block, ok)
	}

	delta := testDelta()
	delta.Remove = []string{"n16", "n16"}
	if block, ok := EncodeDelta("O3", delta); ok || block != "" {
		t.Fatalf("duplicate remove encoding = (%q, %t), want rejection", block, ok)
	}
}

// TestEncodeRejectsInvalidGeometry keeps the v5 geometry invariants at the
// encoder boundary: compact rectangles belong to root and scroll only, details
// never carry points, and malformed rectangles or unknown states are rejected.
func TestEncodeRejectsInvalidGeometry(t *testing.T) {
	t.Run("compact button rectangle", func(t *testing.T) {
		tree := testFullTree()
		tree.Root.Children[0].Children[0].Bounds = testRect(0, 0, 1080, 220)
		if block, ok := EncodeFull("O1", tree); ok || block != "" {
			t.Fatalf("compact button rectangle encoding = (%q, %t), want rejection", block, ok)
		}
	})
	t.Run("compact root point", func(t *testing.T) {
		tree := testFullTree()
		tree.Root.Bounds = testPoint(540, 1200)
		if block, ok := EncodeFull("O1", tree); ok || block != "" {
			t.Fatalf("compact root point encoding = (%q, %t), want rejection", block, ok)
		}
	})
	t.Run("negative rectangle", func(t *testing.T) {
		tree := testFullTree()
		tree.Root.Bounds = testRect(10, 0, 0, 200)
		if block, ok := EncodeFull("O1", tree); ok || block != "" {
			t.Fatalf("negative rectangle encoding = (%q, %t), want rejection", block, ok)
		}
	})
	t.Run("unknown geometry state", func(t *testing.T) {
		delta := testDelta()
		delta.Add = []*protocol.ReadNode{{
			Ref: "n20", Role: protocol.RoleButton,
			Bounds: protocol.ReadBounds{Kind: protocol.GeometryKind(9)},
			Label:  []string{},
		}}
		if block, ok := EncodeDelta("O3", delta); ok || block != "" {
			t.Fatalf("unknown geometry state encoding = (%q, %t), want rejection", block, ok)
		}
	})
}

// testTruncationTree builds a compact tree whose actionable button is truncated
// and whose context node truncation is selectable, so the root and context
// positions of the flag can be rejected independently.
func testTruncationTree(rootTruncated, contextTruncated bool) *protocol.ReadTree {
	root := testNode("n1", protocol.RoleRoot, testRect(0, 0, 100, 200))
	root.Truncated = rootTruncated

	button := testNode("n2", protocol.RoleButton, testPoint(10, 20))
	button.Label = []string{"很长的标题"}
	button.Actions = []protocol.Action{protocol.ActionTap}
	button.Truncated = true

	contextNode := testNode("n3", protocol.RoleText, testPoint(10, 40))
	contextNode.Label = []string{"说明"}
	contextNode.Context = true
	contextNode.Truncated = contextTruncated

	root.Children = []*protocol.ReadNode{button, contextNode}
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope,
		View: protocol.ViewCompact, Root: root,
	}
}

// TestEncodeRejectsTruncatedRootOrContext keeps full text on the compact root
// and on every context node: both positions refuse the truncation fact at the
// public encoding entry, for a full tree and for delta add/replace definitions.
func TestEncodeRejectsTruncatedRootOrContext(t *testing.T) {
	t.Run("full root", func(t *testing.T) {
		if block, ok := EncodeFull("O1", testTruncationTree(true, false)); ok || block != "" {
			t.Fatalf("truncated root encoding = (%q, %t), want rejection", block, ok)
		}
	})
	t.Run("full context child", func(t *testing.T) {
		if block, ok := EncodeFull("O1", testTruncationTree(false, true)); ok || block != "" {
			t.Fatalf("truncated context encoding = (%q, %t), want rejection", block, ok)
		}
	})
	t.Run("delta context definition", func(t *testing.T) {
		delta := testDelta()
		contextNode := testNode("n30", protocol.RoleText, testPoint(10, 40))
		contextNode.Label = []string{"说明"}
		contextNode.Context = true
		contextNode.Truncated = true
		delta.Add = []*protocol.ReadNode{contextNode}
		if block, ok := EncodeDelta("O3", delta); ok || block != "" {
			t.Fatalf("truncated delta context encoding = (%q, %t), want rejection", block, ok)
		}
	})
	t.Run("delta root role definition", func(t *testing.T) {
		delta := testDelta()
		rootRole := testNode("n30", protocol.RoleRoot, testPoint(10, 40))
		rootRole.Truncated = true
		delta.Add = []*protocol.ReadNode{rootRole}
		if block, ok := EncodeDelta("O3", delta); ok || block != "" {
			t.Fatalf("truncated delta root encoding = (%q, %t), want rejection", block, ok)
		}
	})
}

// TestValidReadNodeTreeDetailsKeepsTruncationInScope is the direct reverse
// control for the treetext-side gate. The public entry already refuses these
// shapes through protocol.ReadTree.Validate, so the local rule is asserted on
// its own: removing it must turn this test red even though the entry stays
// closed.
func TestValidReadNodeTreeDetailsKeepsTruncationInScope(t *testing.T) {
	if !validReadNodeTreeDetails(testTruncationTree(false, false).Root, true, false) {
		t.Fatal("truncated actionable nodes must stay encodable")
	}
	if validReadNodeTreeDetails(testTruncationTree(true, false).Root, true, false) {
		t.Fatal("truncated compact root must be rejected")
	}
	if validReadNodeTreeDetails(testTruncationTree(false, true).Root, true, false) {
		t.Fatal("truncated context node must be rejected")
	}

	// Delta add and replace definitions validate without a required root, so a
	// root-role definition root is refused here as well.
	rootRole := testNode("n30", protocol.RoleRoot, testPoint(10, 40))
	rootRole.Truncated = true
	if validDeltaNodeTree(rootRole) {
		t.Fatal("truncated root-role delta definition must be rejected")
	}
	// The same definition without the flag stays a plain valid subtree.
	rootRole.Truncated = false
	if !validDeltaNodeTree(rootRole) {
		t.Fatal("untruncated definition must stay valid")
	}
}
