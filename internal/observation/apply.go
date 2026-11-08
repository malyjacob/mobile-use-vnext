package observation

import (
	"errors"
	"fmt"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ErrNotRepresentable is returned by Diff when the tree pair has no legal delta
// (a node changed role or parent, regions overlap, or verification fails), and
// by Apply when a delta does not describe a legal tree.
var ErrNotRepresentable = errors.New("current tree cannot be represented as a legal delta")

// notRepresentable builds a wrapped ErrNotRepresentable.
func notRepresentable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotRepresentable, fmt.Sprintf(format, args...))
}

// Apply reconstructs current = apply(base, delta).
//
// Interpretation is atomic (design §8.1): every block is a declaration about
// the final result, never a script step. When Apply returns an error no tree is
// produced at all; a partially applied tree is never returned. The result is a
// fresh tree: neither base nor delta is retained or mutated.
func Apply(base *protocol.Tree, delta *protocol.Delta) (*protocol.Tree, error) {
	if base == nil || base.Root == nil {
		return nil, notRepresentable("baseline tree is missing")
	}
	if delta == nil {
		return nil, notRepresentable("delta is missing")
	}
	if delta.Format != "delta" {
		return nil, notRepresentable("delta format is %q", delta.Format)
	}
	if delta.Base == "" {
		return nil, notRepresentable("delta base is missing")
	}

	// 1. Deep copy the baseline. Published trees are immutable and the result
	// must not share mutable nodes with base.
	root := base.Root.Clone()
	nodes := make(map[string]*protocol.Node)
	parents := make(map[string]string)
	if err := indexTree(root, "", nodes, parents); err != nil {
		return nil, err
	}

	// 2. Structure. Mark remove and replace regions on the baseline first;
	// nesting and overlap are only meaningful against baseline positions.
	removeRoots := make(map[string]bool, len(delta.Remove))
	for _, ref := range delta.Remove {
		node, ok := nodes[ref]
		if !ok {
			return nil, notRepresentable("remove ref %q is not in the baseline", ref)
		}
		if node.Role == protocol.RoleRoot {
			return nil, notRepresentable("remove cannot target the root")
		}
		if removeRoots[ref] {
			return nil, notRepresentable("duplicate remove %q", ref)
		}
		removeRoots[ref] = true
	}
	replaceRoots := make(map[string]bool, len(delta.Replace))
	for ref := range delta.Replace {
		if _, ok := nodes[ref]; !ok {
			return nil, notRepresentable("replace ref %q is not in the baseline", ref)
		}
		replaceRoots[ref] = true
	}
	if err := checkNoNesting(removeRoots, parents, "remove"); err != nil {
		return nil, err
	}
	if err := checkNoNesting(replaceRoots, parents, "replace"); err != nil {
		return nil, err
	}
	for ref := range replaceRoots {
		for p := parents[ref]; p != ""; p = parents[p] {
			if removeRoots[p] {
				return nil, notRepresentable("replace %q lies inside removed region %q", ref, p)
			}
		}
	}

	// A pure remove detaches from its external parent; a replace root keeps its
	// parent position and is overwritten by its definition.
	for ref := range removeRoots {
		nodes[parents[ref]].Children = withoutRef(nodes[parents[ref]].Children, ref)
	}
	for ref := range removeRoots {
		deleteSubtree(nodes, ref)
	}
	for ref := range replaceRoots {
		old := nodes[ref]
		for _, child := range old.Children {
			deleteSubtree(nodes, child.Ref)
		}
		delete(nodes, ref)
	}

	// Expand complete definitions. replace re-uses the baseline slot, add
	// creates new nodes; every other ref inside a definition is introduced
	// there as well. Each ref may be defined exactly once in this delta.
	defs := newDefinitionSet()

	replaceOrder := make([]string, 0, len(delta.Replace))
	for ref := range delta.Replace {
		replaceOrder = append(replaceOrder, ref)
	}
	sortStrings(replaceOrder)
	for _, ref := range replaceOrder {
		def := delta.Replace[ref]
		if def == nil {
			return nil, notRepresentable("replace %q has no definition", ref)
		}
		if def.Ref != ref {
			return nil, notRepresentable("replace key %q does not match definition ref %q", ref, def.Ref)
		}
		node, err := expandDefinition(defs, def)
		if err != nil {
			return nil, err
		}
		nodes[ref] = node
		// A replace root keeps its baseline parent position: splice the new
		// node in where the old one sat, so the parent points at it.
		if parent, ok := nodes[parents[ref]]; ok {
			parent.Children = replaceRef(parent.Children, ref, node)
		}
	}

	for _, def := range delta.Add {
		if def == nil {
			return nil, notRepresentable("add has no definition")
		}
		if def.Ref == "" {
			return nil, notRepresentable("add definition has no ref")
		}
		if _, ok := nodes[def.Ref]; ok {
			return nil, notRepresentable("add ref %q already exists in the baseline", def.Ref)
		}
		if _, ok := delta.Replace[def.Ref]; ok {
			return nil, notRepresentable("add ref %q is also replaced", def.Ref)
		}
		node, err := expandDefinition(defs, def)
		if err != nil {
			return nil, err
		}
		nodes[def.Ref] = node
	}

	// 3. set applies to surviving baseline nodes only, never to a complete
	// definition (which already carries its final fields).
	setRefs := make([]string, 0, len(delta.Set))
	for ref := range delta.Set {
		setRefs = append(setRefs, ref)
	}
	sortStrings(setRefs)
	for _, ref := range setRefs {
		target, ok := nodes[ref]
		if !ok {
			return nil, notRepresentable("set ref %q does not survive", ref)
		}
		if defs.contains(ref) {
			return nil, notRepresentable("set ref %q targets a complete definition", ref)
		}
		change := delta.Set[ref]
		if target.Role == protocol.RoleRoot {
			// The synthetic root is a reading unit: it never gains a label,
			// actions or an interactive field through a set block.
			if change.Label.Set || change.Actions.Set || change.Checked.Set ||
				change.Value.Set || change.Hint.Set || change.Axis.Set || change.Resource.Set {
				return nil, notRepresentable("set cannot add interaction fields to the root")
			}
		}
		applySet(target, change)
	}

	// 4. children sets the final direct child list of surviving parents.
	childRefs := make([]string, 0, len(delta.Children))
	for ref := range delta.Children {
		childRefs = append(childRefs, ref)
	}
	sortStrings(childRefs)
	for _, ref := range childRefs {
		parent, ok := nodes[ref]
		if !ok {
			return nil, notRepresentable("children ref %q is not in the result", ref)
		}
		if defs.contains(ref) {
			return nil, notRepresentable("children ref %q is inside a complete definition", ref)
		}
		list := delta.Children[ref]
		children := make([]*protocol.Node, 0, len(list))
		for _, childRef := range list {
			child, ok := nodes[childRef]
			if !ok {
				return nil, notRepresentable("children of %q reference unknown %q", ref, childRef)
			}
			children = append(children, child)
		}
		parent.Children = children
	}

	// 5. Every definition root must end up with a parent: an add root must be
	// listed by its new parent (or be root, which it cannot), and a replace
	// root keeps its baseline parent position.
	attached := map[string]bool{}
	var markAttached func(n *protocol.Node)
	markAttached = func(n *protocol.Node) {
		for _, child := range n.Children {
			attached[child.Ref] = true
			markAttached(child)
		}
	}
	markAttached(root)
	if root.Ref != base.Root.Ref {
		return nil, notRepresentable("root ref changed")
	}
	for _, def := range delta.Add {
		if !attached[def.Ref] {
			return nil, notRepresentable("added node %q has no final parent", def.Ref)
		}
	}

	// 6. Validate the whole result and return it.
	if err := validateResult(root, base); err != nil {
		return nil, err
	}
	return &protocol.Tree{Format: base.Format, Scope: base.Scope, Root: root}, nil
}

// definitionSet tracks refs introduced by add/replace complete definitions so
// set/children cannot address them and a ref is never defined twice.
type definitionSet struct {
	refs map[string]bool
}

func newDefinitionSet() *definitionSet {
	return &definitionSet{refs: map[string]bool{}}
}

func (d *definitionSet) contains(ref string) bool { return d.refs[ref] }

// expandDefinition converts one complete definition into a fresh node tree.
func expandDefinition(defs *definitionSet, def *protocol.Node) (*protocol.Node, error) {
	if defs.refs[def.Ref] {
		return nil, notRepresentable("duplicate definition of %q", def.Ref)
	}
	defs.refs[def.Ref] = true
	node := &protocol.Node{}
	copyNodeFields(node, def)
	node.Children = make([]*protocol.Node, 0, len(def.Children))
	for _, child := range def.Children {
		if child == nil {
			return nil, notRepresentable("nil child definition under %q", def.Ref)
		}
		if child.Ref == def.Ref {
			return nil, notRepresentable("node %q is its own parent", def.Ref)
		}
		expanded, err := expandDefinition(defs, child)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, expanded)
	}
	return node, nil
}

// copyNodeFields copies every logical field except children.
func copyNodeFields(dst, src *protocol.Node) {
	dst.Ref = src.Ref
	dst.Role = src.Role
	dst.Bounds = src.Bounds
	dst.Label = src.Label
	dst.Actions = src.Actions
	dst.Enabled = src.Enabled
	dst.Focused = src.Focused
	dst.Selected = src.Selected
	dst.Checked = src.Checked
	dst.Value = src.Value
	dst.Hint = src.Hint
	dst.Protected = src.Protected
	dst.Axis = src.Axis
	dst.Resource = src.Resource
	dst.Context = src.Context
}

// applySet applies one complete set entry: every field present is a complete
// new value, including explicit false, "", [] and null. set never changes ref,
// role or children; the engine keeps those out of the type, and Apply enforces
// the root's fixed shape separately.
func applySet(node *protocol.Node, change protocol.SetNode) {
	if change.Label.Set {
		node.Label = change.Label.Value
	}
	if change.Actions.Set {
		node.Actions = change.Actions.Value
	}
	if change.Bounds.Set {
		node.Bounds = change.Bounds.Value
	}
	if change.Enabled.Set {
		node.Enabled = change.Enabled.Value
	}
	if change.Focused.Set {
		node.Focused = change.Focused.Value
	}
	if change.Selected.Set {
		node.Selected = change.Selected.Value
	}
	if change.Checked.Set {
		node.Checked = change.Checked.Value
	}
	if change.Value.Set {
		node.Value = change.Value.Value
	}
	if change.Hint.Set {
		node.Hint = change.Hint.Value
	}
	if change.Protected.Set {
		node.Protected = change.Protected.Value
	}
	if change.Axis.Set {
		if change.Axis.Value == nil {
			node.Axis = ""
		} else {
			node.Axis = *change.Axis.Value
		}
	}
	if change.Resource.Set {
		node.Resource = change.Resource.Value
	}
}

// indexTree fills ref -> node and ref -> parent ref, rejecting duplicate refs.
func indexTree(root *protocol.Node, parent string, nodes map[string]*protocol.Node, parents map[string]string) error {
	if root == nil {
		return notRepresentable("baseline is missing a node")
	}
	if root.Ref == "" {
		return notRepresentable("baseline node has no ref")
	}
	if _, ok := nodes[root.Ref]; ok {
		return notRepresentable("duplicate ref %q in the baseline", root.Ref)
	}
	nodes[root.Ref] = root
	parents[root.Ref] = parent
	for _, child := range root.Children {
		if err := indexTree(child, root.Ref, nodes, parents); err != nil {
			return err
		}
	}
	return nil
}

// deleteSubtree removes ref and its descendants from the node index.
func deleteSubtree(nodes map[string]*protocol.Node, ref string) {
	node, ok := nodes[ref]
	if !ok {
		return
	}
	delete(nodes, ref)
	for _, child := range node.Children {
		deleteSubtree(nodes, child.Ref)
	}
}

// replaceRef swaps the entry with ref for node, keeping its position.
func replaceRef(children []*protocol.Node, ref string, node *protocol.Node) []*protocol.Node {
	for i, child := range children {
		if child.Ref == ref {
			children[i] = node
			return children
		}
	}
	return append(children, node)
}

// withoutRef returns children with the first entry whose ref matches removed.
func withoutRef(children []*protocol.Node, ref string) []*protocol.Node {
	out := make([]*protocol.Node, 0, len(children))
	dropped := false
	for _, child := range children {
		if !dropped && child.Ref == ref {
			dropped = true
			continue
		}
		out = append(out, child)
	}
	return out
}

// checkNoNesting rejects a region set where one root contains another.
func checkNoNesting(roots map[string]bool, parents map[string]string, what string) error {
	for ref := range roots {
		for p := parents[ref]; p != ""; p = parents[p] {
			if roots[p] {
				return notRepresentable("%s root %q is nested in %s root %q", what, ref, what, p)
			}
		}
	}
	return nil
}

// validateResult enforces the final invariants of §8.2: one definition per
// ref, unique root, everything reachable from root, one parent per node, no
// cycles and no role change for a surviving ref.
func validateResult(root *protocol.Node, base *protocol.Tree) error {
	if root == nil || root.Role != protocol.RoleRoot {
		return notRepresentable("result has no root node")
	}
	baseRoles := map[string]protocol.Role{}
	var walkBase func(n *protocol.Node)
	walkBase = func(n *protocol.Node) {
		if n == nil {
			return
		}
		baseRoles[n.Ref] = n.Role
		for _, child := range n.Children {
			walkBase(child)
		}
	}
	walkBase(base.Root)

	// A cycle always revisits a ref, so the duplicate check below terminates or
	// rejects; no separate depth limit is needed and none is imposed on legal
	// deep trees.
	seen := map[string]bool{}
	var check func(n *protocol.Node, depth int) error
	check = func(n *protocol.Node, depth int) error {
		if n == nil {
			return notRepresentable("result holds a nil node")
		}
		if n.Ref == "" {
			return notRepresentable("result holds a node without ref")
		}
		if seen[n.Ref] {
			return notRepresentable("result defines %q more than once", n.Ref)
		}
		seen[n.Ref] = true
		if role, ok := baseRoles[n.Ref]; ok && role != n.Role {
			return notRepresentable("role of surviving %q changed", n.Ref)
		}
		for _, child := range n.Children {
			if err := check(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return check(root, 0)
}

// isCheckableRole reports whether a role must always publish checked.
func isCheckableRole(role protocol.Role) bool {
	switch role {
	case protocol.RoleCheckbox, protocol.RoleRadio, protocol.RoleSwitch, protocol.RoleToggle:
		return true
	}
	return false
}
