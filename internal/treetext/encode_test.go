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
