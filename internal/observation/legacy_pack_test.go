package observation

import "github.com/malyjacob/mobile-use-vnext/internal/protocol"

// projectLegacyCompactNode builds the historical logical compact tree used to
// exercise chain input. It applies geometry only and deliberately skips the
// content purification rules, so it still produces the chainable anonymous
// wrappers the historical definition format needs.
func projectLegacyCompactNode(n *protocol.Node, screen compactProjection,
	reference *protocol.Bounds) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	out := copyReadFields(n)
	switch {
	case n.Bounds == nil:
		out.Bounds = protocol.UnknownBounds()
	case n.Role == protocol.RoleRoot || len(n.Actions) != 0:
		out.Bounds = compactDeliveredBounds(n.Role, *n.Bounds, screen)
		reference = deliveredRect(*n.Bounds, screen)
	case compactHidesBounds(n, out, screen, reference):
		out.Bounds = protocol.HiddenBounds()
	default:
		out.Bounds = compactDeliveredBounds(n.Role, *n.Bounds, screen)
		reference = deliveredRect(*n.Bounds, screen)
	}
	for _, child := range n.Children {
		if child != nil {
			out.Children = append(
				out.Children,
				projectLegacyCompactNode(child, screen, reference),
			)
		}
	}
	return out
}

// PackTree builds historical chain fixtures. Production projection and delta
// generation no longer pack ordinary trees, while readers continue to expand
// these definitions through UnpackTree.
func PackTree(t *protocol.ReadTree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	return &protocol.ReadTree{
		Format: t.Format, Scope: t.Scope, View: t.View,
		Root: packDefinitionRoot(t.Root),
	}
}

// PackDelta builds historical add/replace chain fixtures. Definition roots
// remain ordinary ref anchors.
func PackDelta(d *protocol.ReadDelta) *protocol.ReadDelta {
	if d == nil {
		return nil
	}
	out := *d
	if len(d.Add) > 0 {
		out.Add = make([]*protocol.ReadNode, len(d.Add))
		for i, node := range d.Add {
			out.Add[i] = packDefinitionRoot(node)
		}
	}
	if len(d.Replace) > 0 {
		out.Replace = make(map[string]*protocol.ReadNode, len(d.Replace))
		for ref, node := range d.Replace {
			out.Replace[ref] = packDefinitionRoot(node)
		}
	}
	return &out
}

func packDefinitionRoot(n *protocol.ReadNode) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	out := cloneReadNodeFields(n)
	out.Children = packChildren(n.Children)
	return out
}

// packSubtree folds the longest legal consecutive chain that starts at n.
func packSubtree(n *protocol.ReadNode) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	if !chainable(n) {
		out := cloneReadNodeFields(n)
		out.Children = packChildren(n.Children)
		return out
	}
	members := []*protocol.ReadNode{n}
	last := n
	for len(last.Children) == 1 {
		next := last.Children[0]
		if next == nil || !chainable(next) || next.Role != n.Role {
			break
		}
		members = append(members, next)
		last = next
	}
	if len(members) >= 2 && chainShorterThanOrdinary(members) {
		out := cloneReadNodeFields(last)
		out.Ref = ""
		out.Chain = make([]string, len(members))
		for i, member := range members {
			out.Chain[i] = member.Ref
		}
		out.Children = packChildren(last.Children)
		return out
	}
	out := cloneReadNodeFields(n)
	out.Children = packChildren(n.Children)
	return out
}

func packChildren(children []*protocol.ReadNode) []*protocol.ReadNode {
	if len(children) == 0 {
		return nil
	}
	out := make([]*protocol.ReadNode, 0, len(children))
	for _, child := range children {
		out = append(out, packSubtree(child))
	}
	return out
}

func chainable(n *protocol.ReadNode) bool {
	return n != nil && n.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(n)
}

func chainShorterThanOrdinary(members []*protocol.ReadNode) bool {
	ordinary := 0
	for i := range members {
		ordinary += len(mustJSON(spineNode(members[i], members[i].Ref, nil)))
		if i+1 < len(members) {
			ordinary += len(`,"children":[`) + len("]")
		}
	}
	last := members[len(members)-1]
	refs := make([]string, len(members))
	for i, member := range members {
		refs[i] = member.Ref
	}
	chained := len(mustJSON(spineNode(last, "", refs)))
	return chained < ordinary
}

func spineNode(n *protocol.ReadNode, ref string, chain []string) *protocol.ReadNode {
	out := cloneReadNodeFields(n)
	out.Children = nil
	out.Ref = ref
	out.Chain = chain
	return out
}
