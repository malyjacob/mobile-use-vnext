package observation

import (
	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The read representation is a pure projection of the complete internal tree.
// It never mutates the internal tree, the capture, the source mapping or a
// previously published object, and it grants no input or session authority.

// AnonymousPlain reports whether one read node satisfies the fixed
// anonymous-plain predicate: an ordinary view/group with no action, no readable
// content and only known default state. Such a node keeps its ref and its
// position in the tree; only its known geometry may be hidden by the compact
// view.
func AnonymousPlain(n *protocol.ReadNode) bool {
	if n == nil || n.Context {
		return false
	}
	if n.Role != protocol.RoleView && n.Role != protocol.RoleGroup {
		return false
	}
	if len(n.Label) != 0 || len(n.Actions) != 0 {
		return false
	}
	if n.Value != nil || n.Hint != nil || n.Resource != nil || n.Axis != "" {
		return false
	}
	if n.Enabled == nil || !*n.Enabled {
		return false
	}
	for _, flag := range []*bool{n.Focused, n.Selected, n.Protected, n.Checked} {
		if flag == nil || *flag {
			return false
		}
	}
	return true
}

// ProjectCompactTree builds the compact public reading tree: the logical read
// tree of t with known geometry hidden for anonymous plain nodes and
// consecutive anonymous single-child paths folded into chain definitions.
func ProjectCompactTree(t *protocol.Tree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	return PackTree(&protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
		Root: projectReadNode(t.Root, true),
	})
}

// ProjectDetailedTree builds the detailed public reading tree: every logical
// node keeps its known geometry and no chain is folded. It is the fixed inspect
// view and never hides source geometry.
func ProjectDetailedTree(t *protocol.Tree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed,
		Root: projectReadNode(t.Root, false),
	}
}

// ProjectDetailedNode builds the detailed definition of one internal subtree.
func ProjectDetailedNode(n *protocol.Node) *protocol.ReadNode {
	return projectReadNode(n, false)
}

// projectReadNode copies every public field of one internal node. In the
// compact view a known rectangle of an anonymous plain node becomes hidden; all
// other known rectangles are delivered and a missing source rectangle stays
// explicitly unknown.
func projectReadNode(n *protocol.Node, compact bool) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	out := &protocol.ReadNode{
		Ref:       n.Ref,
		Role:      n.Role,
		Label:     append([]string{}, n.Label...),
		Actions:   append([]protocol.Action{}, n.Actions...),
		Enabled:   cloneReadBool(n.Enabled),
		Focused:   cloneReadBool(n.Focused),
		Selected:  cloneReadBool(n.Selected),
		Protected: cloneReadBool(n.Protected),
		Checked:   cloneReadBool(n.Checked),
		Value:     cloneReadString(n.Value),
		Hint:      cloneReadString(n.Hint),
		Axis:      n.Axis,
		Resource:  cloneReadString(n.Resource),
		Context:   n.Context,
		Children:  make([]*protocol.ReadNode, 0, len(n.Children)),
	}
	switch {
	case n.Bounds == nil:
		out.Bounds = protocol.UnknownBounds()
	case compact && AnonymousPlain(out):
		out.Bounds = protocol.HiddenBounds()
	default:
		out.Bounds = protocol.KnownBounds(*n.Bounds)
	}
	for _, child := range n.Children {
		if child == nil {
			continue
		}
		out.Children = append(out.Children, projectReadNode(child, compact))
	}
	return out
}

// PackTree folds consecutive anonymous single-child paths into chain
// definitions. The tree root stays one ordinary definition anchor; only its
// descendants are folded. The function is pure and deterministic: the same
// logical tree always produces the same grouping and bytes.
func PackTree(t *protocol.ReadTree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	return &protocol.ReadTree{
		Format: t.Format, Scope: t.Scope, View: t.View,
		Root: packDefinitionRoot(t.Root),
	}
}

// PackDelta folds chains inside every add and replace definition. A definition
// root always stays an ordinary ref anchor.
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

// packDefinitionRoot packs one complete definition whose root must keep its
// ordinary ref.
func packDefinitionRoot(n *protocol.ReadNode) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	out := cloneReadNodeFields(n)
	out.Children = packChildren(n.Children)
	return out
}

// packSubtree packs one non-definition node, folding the longest legal
// consecutive chain that starts at it.
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

// chainable reports whether one node may be a chain member: it is anonymous
// plain and its public geometry is hidden.
func chainable(n *protocol.ReadNode) bool {
	return n != nil && n.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(n)
}

// chainShorterThanOrdinary reports whether the chain encoding of these members
// is strictly shorter than the ordinary single-node encoding of the same spine.
// The children of the last member are identical in both forms and are excluded.
func chainShorterThanOrdinary(members []*protocol.ReadNode) bool {
	ordinary := 0
	for i := range members {
		ordinary += len(mustJSON(spineNode(members[i], members[i].Ref, nil)))
		if i+1 < len(members) {
			// ,"children":[ <next> ]
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

// spineNode returns a childless copy of n with either its own ref or a chain
// definition.
func spineNode(n *protocol.ReadNode, ref string, chain []string) *protocol.ReadNode {
	out := cloneReadNodeFields(n)
	out.Children = nil
	out.Ref = ref
	out.Chain = chain
	return out
}

// UnpackTree expands every chain definition into its consecutive logical nodes.
func UnpackTree(t *protocol.ReadTree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	return &protocol.ReadTree{
		Format: t.Format, Scope: t.Scope, View: t.View,
		Root: unpackNode(t.Root),
	}
}

// unpackNode expands one definition node into logical nodes.
func unpackNode(n *protocol.ReadNode) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	children := make([]*protocol.ReadNode, 0, len(n.Children))
	for _, child := range n.Children {
		children = append(children, unpackNode(child))
	}
	if len(n.Chain) == 0 {
		out := cloneReadNodeFields(n)
		out.Children = children
		return out
	}
	// The chain members share every public field; only the last one keeps the
	// children. The expansion therefore reuses one field template.
	template := cloneReadNodeFields(n)
	template.Chain = nil
	tail := children
	var built *protocol.ReadNode
	for i := len(n.Chain) - 1; i >= 0; i-- {
		node := *template
		node.Ref = n.Chain[i]
		node.Children = tail
		built = &node
		tail = []*protocol.ReadNode{built}
	}
	return built
}

// ReadEqual compares two read trees after expanding chains. Every logical
// node, field, geometry state and child order must be identical.
func ReadEqual(a, b *protocol.ReadTree) bool {
	left, right := UnpackTree(a), UnpackTree(b)
	if left == nil || right == nil {
		return left == right
	}
	if left.Format != right.Format || left.Scope != right.Scope || left.View != right.View {
		return false
	}
	return equalReadNode(left.Root, right.Root)
}

func equalReadNode(a, b *protocol.ReadNode) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Ref != b.Ref || a.Role != b.Role || a.Context != b.Context {
		return false
	}
	if !a.Bounds.Equal(b.Bounds) {
		return false
	}
	if !equalStrings(a.Label, b.Label) || !equalActions(a.Actions, b.Actions) {
		return false
	}
	if !equalOptionalBool(a.Enabled, b.Enabled) ||
		!equalOptionalBool(a.Focused, b.Focused) ||
		!equalOptionalBool(a.Selected, b.Selected) ||
		!equalOptionalBool(a.Protected, b.Protected) ||
		!equalOptionalBool(a.Checked, b.Checked) {
		return false
	}
	if !equalOptionalString(a.Value, b.Value) || !equalOptionalString(a.Hint, b.Hint) ||
		!equalOptionalString(a.Resource, b.Resource) {
		return false
	}
	if a.Axis != b.Axis || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !equalReadNode(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

// cloneReadBool copies an optional boolean fact.
func cloneReadBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// cloneReadString copies an optional string fact.
func cloneReadString(v *string) *string {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// cloneReadNodeFields copies one node without its children.
func cloneReadNodeFields(n *protocol.ReadNode) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	out := *n
	out.Chain = append([]string(nil), n.Chain...)
	out.Label = append([]string{}, n.Label...)
	out.Actions = append([]protocol.Action{}, n.Actions...)
	out.Enabled = cloneReadBool(n.Enabled)
	out.Focused = cloneReadBool(n.Focused)
	out.Selected = cloneReadBool(n.Selected)
	out.Protected = cloneReadBool(n.Protected)
	out.Checked = cloneReadBool(n.Checked)
	out.Value = cloneReadString(n.Value)
	out.Hint = cloneReadString(n.Hint)
	out.Resource = cloneReadString(n.Resource)
	out.Children = nil
	return &out
}

// mustJSON encodes one already validated read value. A marshaling error is a
// programming error, not a caller error; the deterministic empty object keeps
// the byte comparison total.
func mustJSON(value any) []byte {
	encoded, err := protocol.JSON(value)
	if err != nil {
		return nil
	}
	return encoded
}

// snapshotCostWire is the fixed internal accounting object of one retained
// snapshot. It contains the raw capture and the complete internal tree; the
// compact view is never used as a substitute.
type snapshotCostWire struct {
	RawCapture *device.Capture `json:"raw_capture"`
	FullTree   *protocol.Tree  `json:"full_tree"`
}

// SnapshotCost returns the deterministic JSON size of one snapshot's internal
// accounting object. It is a repeatable accounting proxy, not Go heap or RSS.
func SnapshotCost(capture *device.Capture, tree *protocol.Tree) (int, error) {
	encoded, err := protocol.JSON(snapshotCostWire{RawCapture: capture, FullTree: tree})
	if err != nil {
		return 0, err
	}
	return len(encoded), nil
}

// ReadTreeBytes returns the canonical byte length of one public read value.
func ReadTreeBytes(value any) (int, error) {
	encoded, err := protocol.JSON(value)
	if err != nil {
		return 0, err
	}
	return len(encoded), nil
}
