package observation

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The compact geometry tests cover the fixed delivery policy: action
// positions are kept, actionless text and anonymous plain nodes hide their
// known rectangles, a rectangle identical to the nearest delivered ancestor is
// hidden, and the remaining rectangles below one percent of the screen are
// hidden. Detailed inspection always reveals the original rectangle.

func rectT(left, top, right, bottom int) *protocol.Bounds {
	return &protocol.Bounds{Left: left, Top: top, Right: right, Bottom: bottom}
}

func nodeT(ref string, role protocol.Role, bounds *protocol.Bounds, label ...string) *protocol.Node {
	node := plainT(ref, role)
	node.Bounds = bounds
	node.Label = append([]string{}, label...)
	return node
}

func actionNodeT(ref string, role protocol.Role, bounds *protocol.Bounds, action protocol.Action) *protocol.Node {
	node := nodeT(ref, role, bounds)
	node.Actions = []protocol.Action{action}
	return node
}

func requireBoundsKind(t *testing.T, root *protocol.ReadNode, ref string, kind protocol.GeometryKind) {
	t.Helper()
	node := childOf(t, root, ref)
	if node == nil {
		t.Fatalf("missing node %s", ref)
	}
	if node.Bounds.Kind != kind {
		t.Fatalf("node %s bounds kind = %v, want %v (%+v)",
			ref, node.Bounds.Kind, kind, node.Bounds)
	}
}

func TestCompactGeometryProtectsActionPositions(t *testing.T) {
	smallButton := actionNodeT("n2", protocol.RoleButton, rectT(5, 5, 15, 15), protocol.ActionTap)
	smallView := actionNodeT("n3", protocol.RoleView, rectT(20, 20, 30, 30), protocol.ActionTap)
	checkbox := nodeT("n4", protocol.RoleCheckbox, rectT(40, 40, 50, 50))
	bigText := nodeT("n5", protocol.RoleText, rectT(0, 100, 300, 200), "大标题")
	smallText := nodeT("n6", protocol.RoleText, rectT(0, 210, 50, 230), "小字")
	unknown := nodeT("n7", protocol.RoleImage, nil)
	smallImage := nodeT("n8", protocol.RoleImage, rectT(60, 60, 80, 80))
	plainArea := nodeT("n9", protocol.RoleView, rectT(0, 300, 100, 400), "卡片")
	tree := ProjectCompactTree(rootT(smallButton, smallView, checkbox, bigText,
		smallText, unknown, smallImage, plainArea))

	requireBoundsKind(t, tree.Root, "n2", protocol.GeometryKnown)
	requireBoundsKind(t, tree.Root, "n3", protocol.GeometryKnown)
	// A control role without an action is not protected: the checkbox is a
	// small display node and hides its rectangle.
	requireBoundsKind(t, tree.Root, "n4", protocol.GeometryHidden)
	// Actionless text hides regardless of size.
	requireBoundsKind(t, tree.Root, "n5", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n6", protocol.GeometryHidden)
	// Missing source geometry stays explicitly unknown.
	requireBoundsKind(t, tree.Root, "n7", protocol.GeometryUnknown)
	requireBoundsKind(t, tree.Root, "n8", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n9", protocol.GeometryKnown)

	detailed := ProjectDetailedTree(rootT(smallButton, smallView, checkbox, bigText,
		smallText, unknown, smallImage, plainArea))
	for _, ref := range []string{"n2", "n3", "n4", "n5", "n6", "n8", "n9"} {
		node := childOf(t, detailed.Root, ref)
		if node == nil || node.Bounds.Kind != protocol.GeometryKnown {
			t.Fatalf("detailed view must deliver %s: %+v", ref, node)
		}
	}
	requireBoundsKind(t, detailed.Root, "n7", protocol.GeometryUnknown)
}

func TestCompactGeometryAreaBoundary(t *testing.T) {
	root := func(width, height int) *protocol.Tree {
		tree := rootT()
		tree.Root.Bounds = rectT(0, 0, width, height)
		tree.Root.Children = []*protocol.Node{
			nodeT("n2", protocol.RoleImage, rectT(0, 0, 100, 100)),    // exactly 1%
			nodeT("n3", protocol.RoleImage, rectT(0, 0, 99, 100)),     // below 1%
			nodeT("n4", protocol.RoleImage, rectT(0, 0, 101, 100)),    // above 1%
			nodeT("n5", protocol.RoleImage, rectT(900, 0, 1200, 100)), // clipped exactly 1%
			nodeT("n6", protocol.RoleImage, rectT(901, 0, 1200, 100)), // clipped below 1%
		}
		return tree
	}
	compact := ProjectCompactTree(root(1000, 1000))
	requireBoundsKind(t, compact.Root, "n2", protocol.GeometryKnown)
	requireBoundsKind(t, compact.Root, "n3", protocol.GeometryHidden)
	requireBoundsKind(t, compact.Root, "n4", protocol.GeometryKnown)
	requireBoundsKind(t, compact.Root, "n5", protocol.GeometryKnown)
	requireBoundsKind(t, compact.Root, "n6", protocol.GeometryHidden)
	if node := childOf(t, compact.Root, "n5"); node.Bounds.Rect.Right != 1200 {
		t.Fatalf("delivered bounds must stay the internal rectangle: %+v", node.Bounds)
	}

	// The same rectangles on a larger screen fall below the new threshold.
	resized := ProjectCompactTree(root(2000, 2000))
	requireBoundsKind(t, resized.Root, "n2", protocol.GeometryHidden)
	requireBoundsKind(t, resized.Root, "n4", protocol.GeometryHidden)

	// The duplicate rule compares the clipped rectangles: the child below the
	// delivered parent has the same visible rectangle and hides it.
	parent := nodeT("n2", protocol.RoleImage, rectT(900, 0, 1000, 1000))
	parent.Children = []*protocol.Node{
		nodeT("n3", protocol.RoleImage, rectT(900, 0, 1100, 1000)),
		nodeT("n4", protocol.RoleImage, rectT(900, 0, 1100, 900)),
	}
	screen := rootT(parent)
	screen.Root.Bounds = rectT(0, 0, 1000, 1000)
	duplicates := ProjectCompactTree(screen)
	requireBoundsKind(t, duplicates.Root, "n2", protocol.GeometryKnown)
	requireBoundsKind(t, duplicates.Root, "n3", protocol.GeometryHidden)
	requireBoundsKind(t, duplicates.Root, "n4", protocol.GeometryKnown)
}

func TestCompactProjectionLeavesInternalTreeUnchanged(t *testing.T) {
	before := rootT(
		nodeT("n2", protocol.RoleText, rectT(0, 100, 300, 200), "标题"),
		nodeT("n3", protocol.RoleImage, rectT(0, 0, 20, 20)),
		nodeT("n4", protocol.RoleButton, rectT(10, 10, 90, 60), "确定"),
	)
	before.Root.Children[2].Actions = []protocol.Action{protocol.ActionTap}
	encodedBefore := readWireText(t, before)
	compact := ProjectCompactTree(before)
	if encodedBefore != readWireText(t, before) {
		t.Fatal("compact projection mutated the internal tree")
	}
	detailed := ProjectDetailedTree(before)
	if encodedBefore != readWireText(t, before) {
		t.Fatal("detailed projection mutated the internal tree")
	}
	// The detailed view restores the very rectangles the compact view hid.
	for _, ref := range []string{"n2", "n3"} {
		hidden := childOf(t, compact.Root, ref)
		revealed := childOf(t, detailed.Root, ref)
		if hidden == nil || revealed == nil ||
			hidden.Bounds.Kind != protocol.GeometryHidden ||
			revealed.Bounds.Kind != protocol.GeometryKnown {
			t.Fatalf("geometry state mismatch for %s: compact=%+v detailed=%+v",
				ref, hidden, revealed)
		}
	}
	if node := childOf(t, compact.Root, "n4"); node == nil ||
		node.Bounds.Kind != protocol.GeometryKnown || node.Bounds.Rect.Left != 10 {
		t.Fatalf("action node must keep its rectangle: %+v", node)
	}
}

func TestCompactGeometryDuplicateRules(t *testing.T) {
	// A direct duplicate of the root rectangle hides even when the node is not
	// anonymous plain.
	direct := nodeT("n2", protocol.RoleImage, rectT(0, 0, 400, 800))

	// The nearest delivered ancestor of the grandchild is the card, not the
	// hidden anonymous wrapper.
	card := nodeT("n4", protocol.RoleImage, rectT(10, 10, 390, 790))
	card.Children = []*protocol.Node{nodeT("n5", protocol.RoleImage, rectT(10, 10, 390, 790))}
	wrapper := nodeT("n3", protocol.RoleGroup, rectT(10, 10, 390, 790))
	wrapper.Children = []*protocol.Node{card}

	// An unknown-geometry layer passes the root rectangle down.
	unknownWrapper := nodeT("n6", protocol.RoleImage, nil)
	unknownWrapper.Children = []*protocol.Node{nodeT("n7", protocol.RoleImage, rectT(0, 0, 400, 800))}

	// The nearest delivered ancestor differs, so a rectangle equal to a higher
	// ancestor is still delivered.
	inner := nodeT("n8", protocol.RoleImage, rectT(0, 0, 200, 200))
	inner.Children = []*protocol.Node{nodeT("n9", protocol.RoleImage, rectT(0, 0, 400, 800))}

	// An action node keeps a rectangle that coincides with its ancestor.
	action := actionNodeT("n10", protocol.RoleButton, rectT(0, 0, 400, 800), protocol.ActionTap)

	// A large cover with its own position is delivered.
	cover := nodeT("n11", protocol.RoleImage, rectT(50, 50, 250, 250))

	tree := ProjectCompactTree(rootT(direct, wrapper, unknownWrapper, inner, action, cover))
	requireBoundsKind(t, tree.Root, "n2", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n3", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n4", protocol.GeometryKnown)
	requireBoundsKind(t, tree.Root, "n5", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n6", protocol.GeometryUnknown)
	requireBoundsKind(t, tree.Root, "n7", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n8", protocol.GeometryKnown)
	requireBoundsKind(t, tree.Root, "n9", protocol.GeometryKnown)
	requireBoundsKind(t, tree.Root, "n10", protocol.GeometryKnown)
	requireBoundsKind(t, tree.Root, "n11", protocol.GeometryKnown)
}

func TestCompactGeometryUnknownScreenSkipsAreaRule(t *testing.T) {
	tree := rootT(nodeT("n2", protocol.RoleImage, rectT(0, 0, 10, 10)))
	tree.Root.Bounds = nil
	compact := ProjectCompactTree(tree)
	// The root has no source geometry, so it stays unknown and there is no
	// screen denominator; the remaining node keeps its known rectangle.
	requireBoundsKind(t, compact.Root, "n1", protocol.GeometryUnknown)
	requireBoundsKind(t, compact.Root, "n2", protocol.GeometryKnown)
}

func TestCompactGeometryOverflowGuardSkipsAreaRule(t *testing.T) {
	tree := rootT(nodeT("n2", protocol.RoleImage, rectT(0, 0, 10, 10)))
	tree.Root.Bounds = rectT(0, 0, 1<<40, 1<<40)
	compact := ProjectCompactTree(tree)
	requireBoundsKind(t, compact.Root, "n2", protocol.GeometryKnown)
}

func TestCompactGeometryDeltaTriStates(t *testing.T) {
	baseRoot := func(children ...*protocol.Node) *protocol.Tree {
		tree := rootT(children...)
		tree.Root.Bounds = rectT(0, 0, 1000, 1000)
		return tree
	}
	base := ProjectCompactTree(baseRoot(
		nodeT("n2", protocol.RoleImage, rectT(10, 10, 110, 110)),
		nodeT("n3", protocol.RoleImage, nil),
		nodeT("n4", protocol.RoleText, rectT(0, 0, 100, 50), "同一文字"),
		nodeT("n5", protocol.RoleImage, rectT(0, 0, 100, 100)),
		nodeT("n6", protocol.RoleImage, rectT(0, 0, 50, 50)),
		nodeT("n7", protocol.RoleImage, nil),
		nodeT("n8", protocol.RoleImage, rectT(0, 0, 1000, 1000)),
	))
	current := ProjectCompactTree(baseRoot(
		nodeT("n2", protocol.RoleImage, nil),
		nodeT("n3", protocol.RoleImage, rectT(120, 120, 220, 220)),
		nodeT("n4", protocol.RoleText, rectT(0, 100, 100, 150), "同一文字"),
		nodeT("n5", protocol.RoleImage, rectT(0, 0, 99, 100)),
		nodeT("n6", protocol.RoleImage, nil),
		nodeT("n7", protocol.RoleImage, rectT(0, 0, 50, 50)),
		nodeT("n8", protocol.RoleImage, rectT(200, 200, 300, 300)),
	))

	// The fixtures really start in the states the delta must transition.
	requireBoundsKind(t, base.Root, "n2", protocol.GeometryKnown)
	requireBoundsKind(t, base.Root, "n3", protocol.GeometryUnknown)
	requireBoundsKind(t, base.Root, "n4", protocol.GeometryHidden)
	requireBoundsKind(t, base.Root, "n5", protocol.GeometryKnown)
	requireBoundsKind(t, base.Root, "n6", protocol.GeometryHidden)
	requireBoundsKind(t, base.Root, "n7", protocol.GeometryUnknown)
	requireBoundsKind(t, base.Root, "n8", protocol.GeometryHidden)

	delta := mustReadDelta(t, "O1", base, current)
	cases := map[string]protocol.GeometryKind{
		"n2": protocol.GeometryUnknown, // known -> unknown
		"n3": protocol.GeometryKnown,   // unknown -> known
		"n5": protocol.GeometryHidden,  // known -> hidden via the area threshold
		"n6": protocol.GeometryUnknown, // hidden -> unknown
		"n7": protocol.GeometryHidden,  // unknown -> hidden via the area threshold
		"n8": protocol.GeometryKnown,   // hidden -> known by leaving the ancestor rectangle
	}
	for ref, kind := range cases {
		set, ok := delta.Set[ref]
		if !ok || !set.Bounds.Set || set.Bounds.Value.Kind != kind {
			t.Fatalf("delta set %s = %+v, want bounds kind %v", ref, set, kind)
		}
	}
	if _, ok := delta.Set["n4"]; ok {
		t.Fatalf("a still-hidden text rectangle must not produce a coordinate update: %+v", delta.Set["n4"])
	}
	wire := readWireText(t, delta)
	for _, want := range []string{`"bounds":null`, `"bounds":"hidden"`, `"bounds":[120,120,220,220]`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("delta wire lost %s: %s", want, wire)
		}
	}
	mustReadApplyEqual(t, base, delta, current)

	// Detailed projection of the current frame restores the hidden text and
	// image rectangles.
	detailed := ProjectDetailedTree(baseRoot(
		nodeT("n4", protocol.RoleText, rectT(0, 100, 100, 150), "同一文字"),
		nodeT("n5", protocol.RoleImage, rectT(0, 0, 99, 100)),
	))
	if node := childOf(t, detailed.Root, "n4"); node == nil ||
		node.Bounds.Kind != protocol.GeometryKnown || node.Bounds.Rect.Top != 100 {
		t.Fatalf("detailed view must restore the text rectangle: %+v", node)
	}
	if node := childOf(t, detailed.Root, "n5"); node == nil ||
		node.Bounds.Kind != protocol.GeometryKnown || node.Bounds.Rect.Right != 99 {
		t.Fatalf("detailed view must restore the image rectangle: %+v", node)
	}
}

func TestCompactGeometryDoesNotChainHiddenTextOrImage(t *testing.T) {
	text := nodeT("n2", protocol.RoleText, rectT(0, 0, 100, 50), "文字")
	text.Children = []*protocol.Node{plainT("n3", protocol.RoleView)}
	image := nodeT("n4", protocol.RoleImage, rectT(0, 60, 20, 80))
	image.Children = []*protocol.Node{plainT("n5", protocol.RoleView)}
	// A legal anonymous single-child path still folds.
	leaf := plainT("n8", protocol.RoleView)
	mid := plainT("n7", protocol.RoleView)
	mid.Children = []*protocol.Node{leaf}
	head := plainT("n6", protocol.RoleView)
	head.Children = []*protocol.Node{mid}

	compact := ProjectCompactTree(rootT(text, image, head))
	if hasChain(compact.Root, []string{"n2", "n3"}) || hasChain(compact.Root, []string{"n4", "n5"}) {
		t.Fatalf("hidden text or image nodes must stay independent: %+v", compact.Root)
	}
	if !hasChain(compact.Root, []string{"n6", "n7", "n8"}) {
		t.Fatalf("the legal anonymous chain must still fold: %+v", compact.Root)
	}
	logical := UnpackTree(compact)
	for _, ref := range []string{"n2", "n3", "n4", "n5"} {
		if node := childOf(t, logical.Root, ref); node == nil {
			t.Fatalf("logical node %s is missing after packing", ref)
		}
	}
}
