package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestEqualCompactContent(t *testing.T) {
	makeTree := func() *protocol.ReadTree { return ProjectCompactTree(contentEqualityTree()) }
	a, b := makeTree(), makeTree()
	b.Root.Ref, b.Root.Children[0].Ref = "n11", "n12"
	t.Run("ignores refs", func(t *testing.T) {
		if !EqualCompactContent(a, b) || ReadEqual(a, b) {
			t.Fatal("EqualCompactContent must ignore refs and ReadEqual must compare refs")
		}
	})
	b = makeTree()
	b.Format, b.Scope = "", ""
	if !EqualCompactContent(a, b) {
		t.Fatal("format and scope must not affect compact content equality")
	}
	if EqualCompactContent(nil, nil) || EqualCompactContent(a, nil) ||
		EqualCompactContent(&protocol.ReadTree{}, &protocol.ReadTree{}) {
		t.Fatal("compact content equality requires two trees with roots")
	}
	b.View = protocol.ViewDetailed
	if EqualCompactContent(a, b) {
		t.Fatal("different views compared equal")
	}

	for _, tc := range []struct {
		name   string
		change func(*protocol.ReadNode)
	}{
		{"role", func(n *protocol.ReadNode) { n.Role = protocol.RoleView }},
		{"context", func(n *protocol.ReadNode) { n.Context = true }},
		{"truncated", func(n *protocol.ReadNode) { n.Truncated = true }},
		{"label", func(n *protocol.ReadNode) { n.Label[0] = "changed" }},
		{"actions", func(n *protocol.ReadNode) { n.Actions[0] = protocol.ActionLongPress }},
		{"bounds", func(n *protocol.ReadNode) { n.Bounds = protocol.UnknownBounds() }},
		{"enabled", func(n *protocol.ReadNode) { n.Enabled = nil }},
		{"focused", func(n *protocol.ReadNode) { n.Focused = nil }},
		{"selected", func(n *protocol.ReadNode) { n.Selected = readBool(true) }},
		{"protected", func(n *protocol.ReadNode) { n.Protected = nil }},
		{"checked", func(n *protocol.ReadNode) { n.Checked = readBool(true) }},
		{"value", func(n *protocol.ReadNode) { n.Value = readString("") }},
		{"hint", func(n *protocol.ReadNode) { n.Hint = readString("") }},
		{"resource", func(n *protocol.ReadNode) { n.Resource = readString("") }},
		{"axis", func(n *protocol.ReadNode) { n.Axis = protocol.AxisX }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := makeTree()
			tc.change(changed.Root.Children[0])
			if EqualCompactContent(a, changed) {
				t.Fatalf("changed %s compared equal", tc.name)
			}
		})
	}
	b = makeTree()
	b.Root.Children[0], b.Root.Children[1] = b.Root.Children[1], b.Root.Children[0]
	if EqualCompactContent(a, b) {
		t.Fatal("changed child order compared equal")
	}
	b.Root.Children = b.Root.Children[:1]
	if EqualCompactContent(a, b) {
		t.Fatal("changed child count compared equal")
	}
}

func TestEqualCompactContentChain(t *testing.T) {
	makeTree := func(child *protocol.ReadNode) *protocol.ReadTree {
		return &protocol.ReadTree{View: protocol.ViewCompact, Root: &protocol.ReadNode{
			Ref: "n1", Role: protocol.RoleRoot, Bounds: protocol.KnownBounds(protocol.Bounds{Right: 10, Bottom: 10}),
			Children: []*protocol.ReadNode{child},
		}}
	}
	leaf := &protocol.ReadNode{Ref: "n4", Role: protocol.RoleText, Label: []string{"tail child"}}
	single := &protocol.ReadNode{Ref: "n5", Role: protocol.RoleGroup,
		Bounds: protocol.HiddenBounds(), Children: []*protocol.ReadNode{leaf}}
	chain := &protocol.ReadNode{Chain: []string{"n2", "n3"}, Role: single.Role,
		Bounds: single.Bounds, Children: single.Children}
	head := &protocol.ReadNode{Ref: "n6", Role: single.Role,
		Bounds: single.Bounds, Children: []*protocol.ReadNode{single}}
	chainedTree, singleTree, expandedTree := makeTree(chain), makeTree(single), makeTree(head)
	if EqualCompactContent(singleTree, chainedTree) || EqualCompactContent(chainedTree, singleTree) {
		t.Error("a single node compared equal to a two-node chain")
	}
	if !EqualCompactContent(chainedTree, expandedTree) || !EqualCompactContent(expandedTree, chainedTree) {
		t.Error("the historical chain must equal its logical expansion with different refs")
	}
}

func TestEqualCompactContentNoAllocations(t *testing.T) {
	a, b := ProjectCompactTree(contentEqualityTree()), ProjectCompactTree(contentEqualityTree())
	if got := testing.AllocsPerRun(100, func() {
		if !EqualCompactContent(a, b) {
			t.Fatal("identical content compared unequal")
		}
	}); got != 0 {
		t.Fatalf("ordinary trees allocated %v times per comparison, want 0", got)
	}
}

func TestEqualPublicTreeContent(t *testing.T) {
	a, b := contentEqualityTree(), contentEqualityTree()
	b.Root.Ref, b.Root.Children[0].Ref = "n11", "n12"
	t.Run("ignores refs", func(t *testing.T) {
		if !EqualPublicTreeContent(a, b) || Equal(a, b) {
			t.Fatal("EqualPublicTreeContent must ignore refs and Equal must compare refs")
		}
	})
	b = contentEqualityTree()
	b.Format, b.Scope = "", ""
	if !EqualPublicTreeContent(a, b) {
		t.Fatal("format and scope must not affect public content equality")
	}
	if EqualPublicTreeContent(nil, nil) || EqualPublicTreeContent(a, nil) ||
		EqualPublicTreeContent(&protocol.Tree{}, &protocol.Tree{}) {
		t.Fatal("public content equality requires two trees with roots")
	}

	for _, tc := range []struct {
		name   string
		change func(*protocol.Node)
	}{
		{"role", func(n *protocol.Node) { n.Role = protocol.RoleView }},
		{"context", func(n *protocol.Node) { n.Context = true }},
		{"label", func(n *protocol.Node) { n.Label[0] = "changed" }},
		{"actions", func(n *protocol.Node) { n.Actions[0] = protocol.ActionLongPress }},
		{"nil bounds", func(n *protocol.Node) { n.Bounds = nil }},
		{"bounds edge", func(n *protocol.Node) { n.Bounds.Right++ }},
		{"enabled", func(n *protocol.Node) { n.Enabled = nil }},
		{"focused", func(n *protocol.Node) { n.Focused = nil }},
		{"selected", func(n *protocol.Node) { n.Selected = readBool(true) }},
		{"protected", func(n *protocol.Node) { n.Protected = nil }},
		{"checked", func(n *protocol.Node) { n.Checked = readBool(true) }},
		{"value", func(n *protocol.Node) { n.Value = readString("") }},
		{"hint", func(n *protocol.Node) { n.Hint = readString("") }},
		{"resource", func(n *protocol.Node) { n.Resource = readString("") }},
		{"axis", func(n *protocol.Node) { n.Axis = protocol.AxisX }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := contentEqualityTree()
			tc.change(changed.Root.Children[0])
			if EqualPublicTreeContent(a, changed) || EqualPublicTreeContent(changed, a) {
				t.Fatalf("changed %s compared equal", tc.name)
			}
		})
	}
	b = contentEqualityTree()
	b.Root.Children[0], b.Root.Children[1] = b.Root.Children[1], b.Root.Children[0]
	if EqualPublicTreeContent(a, b) {
		t.Fatal("changed child order compared equal")
	}
	b.Root.Children = b.Root.Children[:1]
	if EqualPublicTreeContent(a, b) {
		t.Fatal("changed child count compared equal")
	}
}

func contentEqualityTree() *protocol.Tree {
	child := nodeT("n2", protocol.RoleScroll, rectT(0, 0, 20, 20), "list")
	child.Actions = []protocol.Action{protocol.ActionSwipe}
	child.Axis = protocol.AxisY
	return visualTree(100, 100, child, nodeT("n3", protocol.RoleText, rectT(30, 0, 60, 20), "text"))
}
