package observation

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The compact geometry tests cover the fixed delivery policy: the root and every
// scroll container keep their four-sided rectangle, every other delivered node
// keeps the center point of its visible rectangle, actionless text and anonymous
// plain nodes hide their known geometry, a rectangle identical to the nearest
// delivered ancestor is hidden, and remaining rectangles below one percent of the
// screen are hidden. Unlabeled non-interactive image leaves are omitted with their
// now-empty anonymous wrappers. Detailed inspection always reveals the original
// rectangles.

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

func requireCenterPoint(t *testing.T, root *protocol.ReadNode, ref string, x, y int) {
	t.Helper()
	node := childOf(t, root, ref)
	if node == nil {
		t.Fatalf("missing node %s", ref)
	}
	if node.Bounds.Kind != protocol.GeometryPoint {
		t.Fatalf("node %s bounds kind = %v, want center point (%+v)",
			ref, node.Bounds.Kind, node.Bounds)
	}
	if node.Bounds.Point != (protocol.Point{X: x, Y: y}) {
		t.Fatalf("node %s center point = %+v, want (%d,%d)", ref, node.Bounds.Point, x, y)
	}
}

func TestCompactGeometryDeliversCenterPoints(t *testing.T) {
	tree := rootT(
		actionNodeT("n2", protocol.RoleButton, rectT(5, 5, 15, 15), protocol.ActionTap),
		actionNodeT("n3", protocol.RoleView, rectT(20, 21, 31, 30), protocol.ActionTap),
		actionNodeT("n4", protocol.RoleInput, rectT(100, 200, 300, 260), protocol.ActionText),
		actionNodeT("n5", protocol.RoleScroll, rectT(0, 300, 400, 500), protocol.ActionSwipe),
		nodeT("n6", protocol.RoleScroll, rectT(0, 500, 400, 800)),
	)
	compact := ProjectCompactTree(tree)

	// The synthetic root and every scroll container keep all four edges.
	requireBoundsKind(t, compact.Root, "n1", protocol.GeometryKnown)
	if compact.Root.Bounds.Rect != *tree.Root.Bounds {
		t.Fatalf("root rectangle = %+v, want %+v", compact.Root.Bounds.Rect, *tree.Root.Bounds)
	}
	for i, ref := range []string{"n5", "n6"} {
		requireBoundsKind(t, compact.Root, ref, protocol.GeometryKnown)
		node := childOf(t, compact.Root, ref)
		want := *tree.Root.Children[3+i].Bounds
		if node.Bounds.Rect != want {
			t.Fatalf("scroll %s rectangle = %+v, want %+v", ref, node.Bounds.Rect, want)
		}
	}

	// Every other delivered node keeps its center point.
	requireCenterPoint(t, compact.Root, "n2", 10, 10)
	requireCenterPoint(t, compact.Root, "n3", 25, 25)
	requireCenterPoint(t, compact.Root, "n4", 200, 230)
}

func TestCompactGeometryClipsCenterPoints(t *testing.T) {
	tree := rootT(
		// Half of this button lies left of the screen: its center is the center
		// of the visible half.
		actionNodeT("n2", protocol.RoleButton, rectT(-100, 0, 100, 100), protocol.ActionTap),
		// A one-pixel control keeps the only pixel it owns.
		actionNodeT("n3", protocol.RoleButton, rectT(0, 0, 1, 1), protocol.ActionTap),
		// A rectangle far outside the screen keeps the center of its source
		// rectangle: no delivered area exists to clip.
		actionNodeT("n4", protocol.RoleButton, rectT(2000, 3000, 2100, 3100), protocol.ActionTap),
	)
	compact := ProjectCompactTree(tree)
	requireCenterPoint(t, compact.Root, "n2", 50, 50)
	requireCenterPoint(t, compact.Root, "n3", 0, 0)
	requireCenterPoint(t, compact.Root, "n4", 2050, 3050)
}

func TestCompactGeometryHidesUndeliveredRects(t *testing.T) {
	// An action delivers geometry even when the node is tiny or duplicated.
	card := actionNodeT("n2", protocol.RoleButton, rectT(10, 10, 110, 110), protocol.ActionTap)
	card.Label = []string{"card"}
	// A labeled group with the very same clipped rectangle as its delivered
	// parent is a duplicate of the nearest delivered ancestor and hides its
	// geometry.
	duplicate := nodeT("n3", protocol.RoleGroup, rectT(10, 10, 110, 110), "same")
	card.Children = []*protocol.Node{duplicate}
	// A text node always hides its rectangle.
	text := nodeT("n4", protocol.RoleText, rectT(0, 400, 400, 460), "标题")
	// An anonymous plain view hides whatever rectangle it has.
	plain := plainT("n5", protocol.RoleView)
	// A labeled group below one percent of the screen hides its rectangle.
	small := nodeT("n6", protocol.RoleGroup, rectT(0, 470, 40, 490), "tiny")
	// Missing source geometry stays explicitly unknown.
	unknown := nodeT("n7", protocol.RoleGroup, nil, "unknown")

	compact := ProjectCompactTree(rootT(card, text, plain, small, unknown))
	requireBoundsKind(t, compact.Root, "n2", protocol.GeometryPoint)
	requireBoundsKind(t, compact.Root, "n3", protocol.GeometryHidden)
	requireBoundsKind(t, compact.Root, "n4", protocol.GeometryHidden)
	requireBoundsKind(t, compact.Root, "n5", protocol.GeometryHidden)
	requireBoundsKind(t, compact.Root, "n6", protocol.GeometryHidden)
	requireBoundsKind(t, compact.Root, "n7", protocol.GeometryUnknown)
}

func TestCompactGeometryDuplicateRules(t *testing.T) {
	// A direct duplicate of the root rectangle hides even when the node is not
	// anonymous plain.
	direct := nodeT("n2", protocol.RoleGroup, rectT(0, 0, 400, 800), "direct")

	// The nearest delivered ancestor of the grandchild is the card, not the
	// hidden anonymous wrapper.
	card := nodeT("n4", protocol.RoleGroup, rectT(10, 10, 390, 790), "card")
	card.Children = []*protocol.Node{nodeT("n5", protocol.RoleGroup, rectT(10, 10, 390, 790), "same")}
	wrapper := plainT("n3", protocol.RoleGroup)
	wrapper.Bounds = rectT(10, 10, 390, 790)
	branch := nodeT("n12", protocol.RoleGroup, rectT(50, 50, 150, 150), "branch")
	wrapper.Children = []*protocol.Node{card, branch}

	// An unknown-geometry layer passes the root rectangle down.
	unknownWrapper := nodeT("n6", protocol.RoleGroup, nil, "unknown")
	unknownWrapper.Children = []*protocol.Node{nodeT("n7", protocol.RoleGroup, rectT(0, 0, 400, 800), "root-sized")}

	// The nearest delivered ancestor differs, so a rectangle equal to a higher
	// ancestor is still delivered.
	inner := nodeT("n8", protocol.RoleGroup, rectT(0, 0, 200, 200), "inner")
	inner.Children = []*protocol.Node{nodeT("n9", protocol.RoleGroup, rectT(0, 0, 400, 800), "outer-sized")}

	// An action node keeps geometry that coincides with its ancestor.
	action := actionNodeT("n10", protocol.RoleButton, rectT(0, 0, 400, 800), protocol.ActionTap)

	// A large cover with its own position is delivered.
	cover := nodeT("n11", protocol.RoleGroup, rectT(50, 50, 250, 250), "cover")

	tree := ProjectCompactTree(rootT(direct, wrapper, unknownWrapper, inner, action, cover))
	requireBoundsKind(t, tree.Root, "n2", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n3", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n4", protocol.GeometryPoint)
	requireBoundsKind(t, tree.Root, "n5", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n6", protocol.GeometryUnknown)
	requireBoundsKind(t, tree.Root, "n7", protocol.GeometryHidden)
	requireBoundsKind(t, tree.Root, "n8", protocol.GeometryPoint)
	// The nearest delivered ancestor of n9 is n8, not the root, so the outer
	// rectangle is a real position and stays delivered.
	requireBoundsKind(t, tree.Root, "n9", protocol.GeometryPoint)
	requireBoundsKind(t, tree.Root, "n10", protocol.GeometryPoint)
	requireBoundsKind(t, tree.Root, "n11", protocol.GeometryPoint)
	requireBoundsKind(t, tree.Root, "n12", protocol.GeometryPoint)
}

func TestCompactGeometryUnknownScreenSkipsRectangleRules(t *testing.T) {
	tree := rootT(
		nodeT("n2", protocol.RoleGroup, rectT(0, 0, 10, 10), "small"),
		actionNodeT("n3", protocol.RoleButton, rectT(-5, -5, 5, 5), protocol.ActionTap),
	)
	tree.Root.Bounds = nil
	compact := ProjectCompactTree(tree)
	// The root has no source geometry, so it stays unknown and there is no
	// screen denominator: the area rule is skipped and the point is the center
	// of the source rectangle.
	requireBoundsKind(t, compact.Root, "n1", protocol.GeometryUnknown)
	requireBoundsKind(t, compact.Root, "n2", protocol.GeometryPoint)
	requireCenterPoint(t, compact.Root, "n2", 5, 5)
	// Without a screen, the mixed-sign center stays inside the source rectangle.
	requireCenterPoint(t, compact.Root, "n3", 0, 0)
}

func TestCompactGeometryOverflowGuardSkipsAreaRule(t *testing.T) {
	tree := rootT(nodeT("n2", protocol.RoleGroup, rectT(0, 0, 10, 10), "small"))
	tree.Root.Bounds = rectT(0, 0, 1<<40, 1<<40)
	compact := ProjectCompactTree(tree)
	requireBoundsKind(t, compact.Root, "n2", protocol.GeometryPoint)
	requireCenterPoint(t, compact.Root, "n2", 5, 5)
}

func TestCompactGeometryDeltaTriStates(t *testing.T) {
	baseRoot := func(children ...*protocol.Node) *protocol.Tree {
		tree := rootT(children...)
		tree.Root.Bounds = rectT(0, 0, 1000, 1000)
		return tree
	}
	baseScroll := actionNodeT("n2", protocol.RoleScroll, rectT(0, 0, 1000, 900), protocol.ActionSwipe)
	base := ProjectCompactTree(baseRoot(
		baseScroll,
		nodeT("n3", protocol.RoleText, rectT(0, 0, 100, 50), "同一文字"),
		nodeT("n4", protocol.RoleCheckbox, rectT(0, 0, 200, 200)),
		nodeT("n5", protocol.RoleGroup, rectT(0, 0, 300, 300), "a"),
		nodeT("n6", protocol.RoleGroup, nil, "b"),
	))
	current := ProjectCompactTree(baseRoot(
		actionNodeT("n2", protocol.RoleScroll, rectT(0, 0, 1000, 900), protocol.ActionSwipe), // known -> unknown? no: scroll stays
		nodeT("n3", protocol.RoleText, rectT(0, 100, 100, 150), "同一文字"),
		nodeT("n4", protocol.RoleCheckbox, rectT(0, 0, 40, 40)),     // point -> hidden (below 1%)
		nodeT("n5", protocol.RoleGroup, rectT(0, 0, 300, 320), "a"), // point -> point
		nodeT("n6", protocol.RoleGroup, rectT(0, 0, 50, 50), "b"),   // unknown -> hidden
	))

	// The fixtures really start in the states the delta must transition.
	requireBoundsKind(t, base.Root, "n2", protocol.GeometryKnown)
	requireBoundsKind(t, base.Root, "n3", protocol.GeometryHidden)
	requireCenterPoint(t, base.Root, "n4", 100, 100)
	requireCenterPoint(t, base.Root, "n5", 150, 150)
	requireBoundsKind(t, base.Root, "n6", protocol.GeometryUnknown)

	delta := mustReadDelta(t, "O1", base, current)
	cases := map[string]protocol.GeometryKind{
		"n4": protocol.GeometryHidden, // point -> hidden via the area threshold
		"n5": protocol.GeometryPoint,  // point -> point
		"n6": protocol.GeometryHidden, // unknown -> hidden via the area threshold
	}
	for ref, kind := range cases {
		set, ok := delta.Set[ref]
		if !ok || !set.Bounds.Set || set.Bounds.Value.Kind != kind {
			t.Fatalf("delta set %s = %+v, want bounds kind %v", ref, set, kind)
		}
	}
	if _, ok := delta.Set["n3"]; ok {
		t.Fatalf("a still-hidden text rectangle must not produce a coordinate update: %+v", delta.Set["n3"])
	}
	if _, ok := delta.Set["n2"]; ok {
		t.Fatalf("an unchanged scroll rectangle must not produce a coordinate update: %+v", delta.Set["n2"])
	}
	if point := delta.Set["n5"].Bounds.Value.Point; point != (protocol.Point{X: 150, Y: 160}) {
		t.Fatalf("moved point = %+v, want (150,160)", point)
	}
	wire := readWireText(t, delta)
	for _, want := range []string{`"bounds":"hidden"`, `"bounds":[150,160]`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("delta wire lost %s: %s", want, wire)
		}
	}
	mustReadApplyEqual(t, base, delta, current)

	// A scroll container that loses its source rectangle becomes explicitly
	// unknown instead of a point.
	unknownScroll := ProjectCompactTree(baseRoot(nodeT("n2", protocol.RoleScroll, nil)))
	unknownBase := ProjectCompactTree(baseRoot(baseScroll))
	scrollDelta := mustReadDelta(t, "O1", unknownBase, unknownScroll)
	if set := scrollDelta.Set["n2"]; !set.Bounds.Set || set.Bounds.Value.Kind != protocol.GeometryUnknown {
		t.Fatalf("scroll rectangle loss = %+v, want bounds null", set)
	}

	// Detailed projection of the current frame restores the hidden text and the
	// threshold-hidden control rectangles.
	detailed := ProjectDetailedTree(baseRoot(
		nodeT("n3", protocol.RoleText, rectT(0, 100, 100, 150), "同一文字"),
		nodeT("n4", protocol.RoleCheckbox, rectT(0, 0, 40, 40)),
	))
	if node := childOf(t, detailed.Root, "n3"); node == nil ||
		node.Bounds.Kind != protocol.GeometryKnown || node.Bounds.Rect.Top != 100 {
		t.Fatalf("detailed view must restore the text rectangle: %+v", node)
	}
	if node := childOf(t, detailed.Root, "n4"); node == nil ||
		node.Bounds.Kind != protocol.GeometryKnown || node.Bounds.Rect.Right != 40 {
		t.Fatalf("detailed view must restore the control rectangle: %+v", node)
	}
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
	hidden := childOf(t, compact.Root, "n2")
	revealed := childOf(t, detailed.Root, "n2")
	if hidden == nil || revealed == nil ||
		hidden.Bounds.Kind != protocol.GeometryHidden ||
		revealed.Bounds.Kind != protocol.GeometryKnown {
		t.Fatalf("geometry state mismatch for n2: compact=%+v detailed=%+v", hidden, revealed)
	}
	if childOf(t, compact.Root, "n3") != nil {
		t.Fatal("the unlabeled image must be absent from compact delivery")
	}
	if revealed := childOf(t, detailed.Root, "n3"); revealed == nil ||
		revealed.Bounds.Kind != protocol.GeometryKnown {
		t.Fatalf("detailed view must restore the pruned image: %+v", revealed)
	}
	node := childOf(t, compact.Root, "n4")
	if node == nil || node.Bounds.Kind != protocol.GeometryPoint ||
		node.Bounds.Point != (protocol.Point{X: 50, Y: 35}) {
		t.Fatalf("action node must keep its center point: %+v", node)
	}
}

func TestCompactGeometryUsesOrdinaryDefinitionsAndWrapperPruning(t *testing.T) {
	text := nodeT("n2", protocol.RoleText, rectT(0, 0, 100, 50), "文字")
	text.Children = []*protocol.Node{plainT("n3", protocol.RoleView)}
	image := nodeT("n4", protocol.RoleImage, rectT(0, 60, 20, 80))
	image.Children = []*protocol.Node{plainT("n5", protocol.RoleView)}
	// The anonymous singleton path is bypassed, not folded.
	leaf := plainT("n8", protocol.RoleView)
	mid := plainT("n7", protocol.RoleView)
	mid.Children = []*protocol.Node{leaf}
	head := plainT("n6", protocol.RoleView)
	head.Children = []*protocol.Node{mid}

	compact := ProjectCompactTree(rootT(text, image, head))
	requireNoReadChains(t, compact.Root)
	for _, ref := range []string{"n2", "n3", "n4", "n5"} {
		if node := childOf(t, compact.Root, ref); node == nil {
			t.Fatalf("retained logical node %s is missing", ref)
		}
	}
	if childOf(t, compact.Root, "n6") != nil || childOf(t, compact.Root, "n7") != nil {
		t.Fatal("singleton anonymous wrappers must be bypassed")
	}
	if childOf(t, compact.Root, "n8") == nil {
		t.Fatal("the promoted leaf must remain in its original position")
	}
}

func TestCompactGeometryViewInvariant(t *testing.T) {
	scroll := actionNodeT("n2", protocol.RoleScroll, rectT(0, 0, 400, 300), protocol.ActionSwipe)
	scroll.Children = []*protocol.Node{
		actionNodeT("n3", protocol.RoleButton, rectT(10, 10, 200, 80), protocol.ActionTap),
		actionNodeT("n4", protocol.RoleInput, rectT(10, 90, 200, 150), protocol.ActionText),
		nodeT("n5", protocol.RoleText, rectT(10, 160, 200, 200), "标题"),
		nodeT("n6", protocol.RoleGroup, nil, "未知"),
	}
	tree := rootT(scroll, nodeT("n7", protocol.RoleGroup, rectT(0, 400, 400, 500), "卡片"))

	compact := ProjectCompactTree(tree)
	if err := compact.Validate(); err != nil {
		t.Fatalf("compact view must validate: %v", err)
	}
	var walkCompact func(n *protocol.ReadNode)
	walkCompact = func(n *protocol.ReadNode) {
		switch n.Bounds.Kind {
		case protocol.GeometryKnown:
			if n.Role != protocol.RoleRoot && n.Role != protocol.RoleScroll {
				t.Fatalf("compact rectangle on non-container role %s: %+v", n.Role, n)
			}
		case protocol.GeometryPoint:
			if n.Role == protocol.RoleRoot || n.Role == protocol.RoleScroll {
				t.Fatalf("compact point on container role %s: %+v", n.Role, n)
			}
		case protocol.GeometryHidden, protocol.GeometryUnknown:
		default:
			t.Fatalf("invalid compact geometry kind for %s: %+v", n.Ref, n)
		}
		for _, child := range n.Children {
			walkCompact(child)
		}
	}
	walkCompact(compact.Root)

	// The detailed view never hides geometry and never delivers a point.
	detailed := ProjectDetailedTree(tree)
	if err := detailed.Validate(); err != nil {
		t.Fatalf("detailed view must validate: %v", err)
	}
	var walkDetailed func(n *protocol.ReadNode)
	walkDetailed = func(n *protocol.ReadNode) {
		if n.Bounds.Kind != protocol.GeometryKnown && n.Bounds.Kind != protocol.GeometryUnknown {
			t.Fatalf("detailed view geometry for %s = %v, want rectangle or null",
				n.Ref, n.Bounds.Kind)
		}
		for _, child := range n.Children {
			walkDetailed(child)
		}
	}
	walkDetailed(detailed.Root)
}
