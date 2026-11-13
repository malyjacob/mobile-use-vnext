package observation

import (
	"bytes"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ReadDiff computes the public delta of two compact read trees. Both inputs and
// the result are wire values: chains are expanded before planning so every
// logical node is compared by its original ref, and the add/replace definitions
// of the result are packed again. A delta that cannot be represented legally or
// that does not reconstruct the current tree is reported as
// ErrNotRepresentable instead of returning a lossy result.
func ReadDiff(baseID string, base, current *protocol.ReadTree) (*protocol.ReadDelta, error) {
	if baseID == "" {
		return nil, notRepresentable("delta base id is empty")
	}
	if base == nil || base.Root == nil || current == nil || current.Root == nil {
		return nil, notRepresentable("baseline or current read tree is missing")
	}
	if base.View != protocol.ViewCompact || current.View != protocol.ViewCompact {
		return nil, notRepresentable("only compact read trees share a delta baseline")
	}
	logicalBase, logicalCurrent := UnpackTree(base), UnpackTree(current)
	delta, err := planReadDelta(logicalBase, logicalCurrent)
	if err != nil {
		return nil, err
	}
	delta.Format = "delta"
	delta.Base = baseID
	delta.View = protocol.ViewCompact
	delta.Defaults = protocol.Defaults{}
	if delta.IsEmpty() {
		return &protocol.ReadDelta{Format: "delta", Base: baseID, View: protocol.ViewCompact}, nil
	}
	packed := PackDelta(delta)
	applied, err := ReadApply(base, packed)
	if err != nil {
		return nil, notRepresentable("delta does not apply: %v", err)
	}
	if !ReadEqual(applied, current) {
		return nil, notRepresentable("delta does not reconstruct the current read tree")
	}
	if !bytes.Equal(mustJSON(applied), mustJSON(current)) {
		return nil, notRepresentable("delta does not reconstruct the canonical current encoding")
	}
	return packed, nil
}

// planReadDelta builds the change blocks of current relative to base. Both
// trees are logical: no definition carries a chain.
func planReadDelta(base, current *protocol.ReadTree) (*protocol.ReadDelta, error) {
	if base.Root.Ref != current.Root.Ref {
		return nil, notRepresentable("root ref changed from %q to %q", base.Root.Ref, current.Root.Ref)
	}
	baseNodes := map[string]*protocol.ReadNode{}
	baseParent := map[string]string{}
	baseOrder := collectReadPreorder(base.Root, nil, baseNodes, baseParent)

	curNodes := map[string]*protocol.ReadNode{}
	curParent := map[string]string{}
	curOrder := collectReadPreorder(current.Root, nil, curNodes, curParent)

	for _, ref := range baseOrder {
		cur, ok := curNodes[ref]
		if !ok {
			continue
		}
		if cur.Role != baseNodes[ref].Role {
			return nil, notRepresentable("role of %q changed", ref)
		}
		if p := curParent[ref]; p != "" && baseNodes[p] == nil {
			return nil, notRepresentable("node %q now lives inside new node %q", ref, p)
		}
	}

	kind := make(map[string]int, len(baseOrder))
	for _, ref := range baseOrder {
		cur, ok := curNodes[ref]
		switch {
		case !ok:
			kind[ref] = kindGone
		case sameReadLogicalNode(baseNodes[ref], cur) && sameReadChildRefs(baseNodes[ref], cur):
			kind[ref] = kindKeep
		default:
			kind[ref] = kindChange
		}
	}

	var removals []string
	for _, ref := range baseOrder {
		if kind[ref] != kindGone || ref == base.Root.Ref {
			continue
		}
		if parent := baseParent[ref]; parent != "" && kind[parent] == kindGone && baseNodes[parent].Role != protocol.RoleRoot {
			continue
		}
		if hasSurvivingReadDescendant(baseNodes[ref], kind) {
			return nil, notRepresentable("subtree %q holds surviving nodes and cannot be removed", ref)
		}
		removals = append(removals, ref)
	}

	replaceable := make(map[string]bool, len(curOrder))
	currentCount := make(map[string]int, len(curOrder))
	survivorCount := make(map[string]int, len(baseOrder))
	for i := len(curOrder) - 1; i >= 0; i-- {
		ref := curOrder[i]
		cur := curNodes[ref]
		count := 1
		closed := baseNodes[ref] != nil
		for _, child := range cur.Children {
			count += currentCount[child.Ref]
			if baseNodes[child.Ref] == nil || baseParent[child.Ref] != ref {
				closed = false
			}
		}
		currentCount[ref] = count
		if ref != current.Root.Ref {
			replaceable[ref] = closed
		}
	}
	for i := len(baseOrder) - 1; i >= 0; i-- {
		ref := baseOrder[i]
		count := 0
		for _, child := range baseNodes[ref].Children {
			if kind[child.Ref] != kindGone {
				count++
			}
			count += survivorCount[child.Ref]
		}
		survivorCount[ref] = count
		if ref != base.Root.Ref {
			replaceable[ref] = replaceable[ref] && survivorCount[ref] == currentCount[ref]-1
		}
	}

	costs := newReadCosts()
	chosen := map[string]bool{}
	var chooseRegions func(n *protocol.ReadNode, parentCovered bool)
	chooseRegions = func(n *protocol.ReadNode, parentCovered bool) {
		if parentCovered {
			return
		}
		if replaceable[n.Ref] && readSubtreeSize(n) >= replaceMinNodes &&
			costs.regionSaving(n, baseNodes, kind) > replaceGain {
			chosen[n.Ref] = true
			for _, child := range n.Children {
				chooseRegions(child, true)
			}
			return
		}
		for _, child := range n.Children {
			chooseRegions(child, false)
		}
	}
	chooseRegions(current.Root, false)
	covered := markReadCovered(current.Root, chosen)
	inChosen := func(ref string) bool { return covered[ref] }

	delta := &protocol.ReadDelta{}
	for _, ref := range removals {
		if !inChosen(ref) {
			delta.Remove = append(delta.Remove, ref)
		}
	}
	removed := make(map[string]bool, len(delta.Remove))
	for _, ref := range delta.Remove {
		removed[ref] = true
	}

	var addRoots []string
	for _, ref := range curOrder {
		if baseNodes[ref] != nil || ref == current.Root.Ref || chosen[ref] {
			continue
		}
		if p := curParent[ref]; p != "" && inChosen(p) {
			continue
		}
		if p := curParent[ref]; p != "" && baseNodes[p] == nil && !chosen[p] {
			continue
		}
		addRoots = append(addRoots, ref)
		delta.Add = append(delta.Add, curNodes[ref].Clone())
	}
	addRoot := make(map[string]bool, len(addRoots))
	for _, ref := range addRoots {
		addRoot[ref] = true
	}

	needsChildren := map[string]bool{}
	for _, ref := range addRoots {
		if parent := curParent[ref]; parent != "" && !addRoot[parent] {
			needsChildren[parent] = true
		}
	}
	for _, ref := range curOrder {
		if baseNodes[ref] == nil || inChosen(ref) || ref == current.Root.Ref {
			continue
		}
		if sameReadChildRefs(baseNodes[ref], curNodes[ref]) {
			continue
		}
		needsChildren[ref] = true
	}
	if !sameReadChildRefs(base.Root, current.Root) {
		needsChildren[current.Root.Ref] = true
	}
	for _, ref := range curOrder {
		if !needsChildren[ref] || baseNodes[ref] == nil {
			continue
		}
		if !readChildrenBlockUseful(baseNodes[ref], curNodes[ref], removed) {
			delete(needsChildren, ref)
		}
	}
	for _, ref := range curOrder {
		if !needsChildren[ref] {
			continue
		}
		if delta.Children == nil {
			delta.Children = map[string][]string{}
		}
		list := make([]string, 0, len(curNodes[ref].Children))
		for _, child := range curNodes[ref].Children {
			list = append(list, child.Ref)
		}
		delta.Children[ref] = list
	}

	for _, ref := range baseOrder {
		if kind[ref] == kindGone || covered[ref] {
			continue
		}
		change, changed := diffReadFields(baseNodes[ref], curNodes[ref])
		if !changed {
			continue
		}
		if delta.Set == nil {
			delta.Set = map[string]protocol.ReadSetNode{}
		}
		delta.Set[ref] = change
	}

	for _, ref := range curOrder {
		if !chosen[ref] {
			continue
		}
		if delta.Replace == nil {
			delta.Replace = map[string]*protocol.ReadNode{}
		}
		delta.Replace[ref] = curNodes[ref].Clone()
	}
	return delta, nil
}

// ReadApply reconstructs the current compact read tree from a compact baseline
// and the packed delta that declares it. Interpretation is atomic: an error
// produces no tree at all.
func ReadApply(base *protocol.ReadTree, delta *protocol.ReadDelta) (*protocol.ReadTree, error) {
	if base == nil || base.Root == nil {
		return nil, notRepresentable("baseline read tree is missing")
	}
	if delta == nil || delta.Format != "delta" {
		return nil, notRepresentable("delta is missing")
	}
	if delta.View != protocol.ViewCompact || base.View != protocol.ViewCompact {
		return nil, notRepresentable("only compact read trees share a delta baseline")
	}
	logicalBase := UnpackTree(base)
	if logicalBase == nil || logicalBase.Root == nil {
		return nil, notRepresentable("baseline read tree is missing")
	}
	expanded := expandReadDelta(delta)
	root, err := applyReadDelta(logicalBase, expanded)
	if err != nil {
		return nil, err
	}
	return PackTree(&protocol.ReadTree{
		Format: logicalBase.Format, Scope: logicalBase.Scope, View: logicalBase.View, Root: root,
	}), nil
}

// expandReadDelta returns a delta whose definitions are logical nodes.
func expandReadDelta(delta *protocol.ReadDelta) *protocol.ReadDelta {
	out := *delta
	if len(delta.Add) > 0 {
		out.Add = make([]*protocol.ReadNode, len(delta.Add))
		for i, node := range delta.Add {
			out.Add[i] = unpackNode(node)
		}
	}
	if len(delta.Replace) > 0 {
		out.Replace = make(map[string]*protocol.ReadNode, len(delta.Replace))
		for ref, node := range delta.Replace {
			out.Replace[ref] = unpackNode(node)
		}
	}
	return &out
}

// applyReadDelta applies one logical delta to one logical baseline.
func applyReadDelta(base *protocol.ReadTree, delta *protocol.ReadDelta) (*protocol.ReadNode, error) {
	root := base.Root.Clone()
	nodes := make(map[string]*protocol.ReadNode)
	parents := make(map[string]string)
	if err := indexReadTree(root, "", nodes, parents); err != nil {
		return nil, err
	}

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
	if err := checkReadNoNesting(removeRoots, parents, "remove"); err != nil {
		return nil, err
	}
	if err := checkReadNoNesting(replaceRoots, parents, "replace"); err != nil {
		return nil, err
	}
	for ref := range replaceRoots {
		for p := parents[ref]; p != ""; p = parents[p] {
			if removeRoots[p] {
				return nil, notRepresentable("replace %q lies inside removed region %q", ref, p)
			}
		}
	}
	for ref := range removeRoots {
		nodes[parents[ref]].Children = withoutReadRef(nodes[parents[ref]].Children, ref)
	}
	for ref := range removeRoots {
		deleteReadSubtree(nodes, ref)
	}
	for ref := range replaceRoots {
		old := nodes[ref]
		for _, child := range old.Children {
			deleteReadSubtree(nodes, child.Ref)
		}
		delete(nodes, ref)
	}

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
		node, err := expandReadDefinition(defs, def)
		if err != nil {
			return nil, err
		}
		nodes[ref] = node
		if parent, ok := nodes[parents[ref]]; ok {
			parent.Children = replaceReadRef(parent.Children, ref, node)
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
		node, err := expandReadDefinition(defs, def)
		if err != nil {
			return nil, err
		}
		nodes[def.Ref] = node
	}

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
			if change.Label.Set || change.Actions.Set || change.Checked.Set ||
				change.Value.Set || change.Hint.Set || change.Axis.Set || change.Resource.Set {
				return nil, notRepresentable("set cannot add interaction fields to the root")
			}
		}
		applyReadSet(target, change)
	}

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
		children := make([]*protocol.ReadNode, 0, len(list))
		for _, childRef := range list {
			child, ok := nodes[childRef]
			if !ok {
				return nil, notRepresentable("children of %q reference unknown %q", ref, childRef)
			}
			children = append(children, child)
		}
		parent.Children = children
	}

	attached := map[string]bool{}
	var markAttached func(n *protocol.ReadNode)
	markAttached = func(n *protocol.ReadNode) {
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
	if err := validateReadResult(root, base); err != nil {
		return nil, err
	}
	return root, nil
}

// expandReadDefinition converts one complete logical definition into a fresh
// node tree and records every ref it defines.
func expandReadDefinition(defs *definitionSet, def *protocol.ReadNode) (*protocol.ReadNode, error) {
	if defs.refs[def.Ref] {
		return nil, notRepresentable("duplicate definition of %q", def.Ref)
	}
	defs.refs[def.Ref] = true
	node := cloneReadNodeFields(def)
	node.Children = make([]*protocol.ReadNode, 0, len(def.Children))
	for _, child := range def.Children {
		if child == nil {
			return nil, notRepresentable("nil child definition under %q", def.Ref)
		}
		if child.Ref == def.Ref {
			return nil, notRepresentable("node %q is its own parent", def.Ref)
		}
		expanded, err := expandReadDefinition(defs, child)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, expanded)
	}
	return node, nil
}

// applyReadSet applies one complete set entry. Every present member is a
// complete new value, including explicit false, "", [] and null.
func applyReadSet(node *protocol.ReadNode, change protocol.ReadSetNode) {
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

func indexReadTree(root *protocol.ReadNode, parent string, nodes map[string]*protocol.ReadNode,
	parents map[string]string) error {
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
		if err := indexReadTree(child, root.Ref, nodes, parents); err != nil {
			return err
		}
	}
	return nil
}

func deleteReadSubtree(nodes map[string]*protocol.ReadNode, ref string) {
	node, ok := nodes[ref]
	if !ok {
		return
	}
	delete(nodes, ref)
	for _, child := range node.Children {
		deleteReadSubtree(nodes, child.Ref)
	}
}

func replaceReadRef(children []*protocol.ReadNode, ref string, node *protocol.ReadNode) []*protocol.ReadNode {
	for i, child := range children {
		if child.Ref == ref {
			children[i] = node
			return children
		}
	}
	return append(children, node)
}

func withoutReadRef(children []*protocol.ReadNode, ref string) []*protocol.ReadNode {
	out := make([]*protocol.ReadNode, 0, len(children))
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

func checkReadNoNesting(roots map[string]bool, parents map[string]string, what string) error {
	for ref := range roots {
		for p := parents[ref]; p != ""; p = parents[p] {
			if roots[p] {
				return notRepresentable("%s root %q is nested in %s root %q", what, ref, what, p)
			}
		}
	}
	return nil
}

// validateReadResult enforces one definition per ref, unique root, complete
// reachability from root and no role change for a surviving ref.
func validateReadResult(root *protocol.ReadNode, base *protocol.ReadTree) error {
	if root == nil || root.Role != protocol.RoleRoot {
		return notRepresentable("result has no root node")
	}
	baseRoles := map[string]protocol.Role{}
	var walkBase func(n *protocol.ReadNode)
	walkBase = func(n *protocol.ReadNode) {
		if n == nil {
			return
		}
		baseRoles[n.Ref] = n.Role
		for _, child := range n.Children {
			walkBase(child)
		}
	}
	walkBase(base.Root)

	seen := map[string]bool{}
	var check func(n *protocol.ReadNode) error
	check = func(n *protocol.ReadNode) error {
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
			if err := check(child); err != nil {
				return err
			}
		}
		return nil
	}
	return check(root)
}

// diffReadFields builds the explicit set entry of two logical nodes with the
// same ref. Members equal to the baseline value are omitted; every changed
// member is emitted with its complete new value.
func diffReadFields(prev, cur *protocol.ReadNode) (protocol.ReadSetNode, bool) {
	var change protocol.ReadSetNode
	if !equalStrings(prev.Label, cur.Label) {
		change.Label = protocol.Field[[]string]{Set: true, Value: append([]string{}, cur.Label...)}
	}
	if !equalActions(prev.Actions, cur.Actions) {
		change.Actions = protocol.Field[[]protocol.Action]{Set: true, Value: append([]protocol.Action{}, cur.Actions...)}
	}
	if !prev.Bounds.Equal(cur.Bounds) {
		change.Bounds = protocol.Field[protocol.ReadBounds]{Set: true, Value: cur.Bounds}
	}
	if !equalTriBool(prev.Enabled, cur.Enabled, true) {
		change.Enabled = protocol.Field[*bool]{Set: true, Value: cur.Enabled}
	}
	if !equalTriBool(prev.Focused, cur.Focused, false) {
		change.Focused = protocol.Field[*bool]{Set: true, Value: cur.Focused}
	}
	if !equalTriBool(prev.Selected, cur.Selected, false) {
		change.Selected = protocol.Field[*bool]{Set: true, Value: cur.Selected}
	}
	if !equalTriBool(prev.Protected, cur.Protected, false) {
		change.Protected = protocol.Field[*bool]{Set: true, Value: cur.Protected}
	}
	if !equalOptionalBool(prev.Checked, cur.Checked) {
		change.Checked = protocol.Field[*bool]{Set: true, Value: cur.Checked}
	}
	if !equalOptionalString(prev.Value, cur.Value) {
		change.Value = protocol.Field[*string]{Set: true, Value: cur.Value}
	}
	if !equalOptionalString(prev.Hint, cur.Hint) {
		change.Hint = protocol.Field[*string]{Set: true, Value: cur.Hint}
	}
	if prev.Axis != cur.Axis {
		var axis *protocol.Axis
		if cur.Axis != "" {
			c := cur.Axis
			axis = &c
		}
		change.Axis = protocol.Field[*protocol.Axis]{Set: true, Value: axis}
	}
	if !equalOptionalString(prev.Resource, cur.Resource) {
		change.Resource = protocol.Field[*string]{Set: true, Value: cur.Resource}
	}
	return change, !change.IsZero()
}

// sameReadLogicalNode reports whether two logical nodes have equal own fields
// and an equal direct child ref list.
func sameReadLogicalNode(a, b *protocol.ReadNode) bool {
	if len(a.Children) != len(b.Children) {
		return false
	}
	shallowA, shallowB := *a, *b
	shallowA.Children, shallowB.Children = nil, nil
	return equalReadNode(&shallowA, &shallowB)
}

// sameReadChildRefs reports whether two nodes list the same direct children in
// the same order.
func sameReadChildRefs(a, b *protocol.ReadNode) bool {
	if len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if a.Children[i].Ref != b.Children[i].Ref {
			return false
		}
	}
	return true
}

func hasSurvivingReadDescendant(n *protocol.ReadNode, kind map[string]int) bool {
	for _, child := range n.Children {
		if kind[child.Ref] != kindGone || hasSurvivingReadDescendant(child, kind) {
			return true
		}
	}
	return false
}

func readSubtreeSize(n *protocol.ReadNode) int {
	size := 1
	for _, child := range n.Children {
		size += readSubtreeSize(child)
	}
	return size
}

// readChildrenBlockUseful reports whether a changed child list needs an explicit
// children block: the block is redundant when the change is exactly the
// disappearance of children removed elsewhere and the order is unchanged.
func readChildrenBlockUseful(prev, cur *protocol.ReadNode, removed map[string]bool) bool {
	remaining := make([]string, 0, len(prev.Children))
	for _, child := range prev.Children {
		if removed[child.Ref] {
			continue
		}
		remaining = append(remaining, child.Ref)
	}
	if len(remaining) != len(cur.Children) {
		return true
	}
	for i := range remaining {
		if remaining[i] != cur.Children[i].Ref {
			return true
		}
	}
	return false
}

// markReadCovered returns the set of refs covered by one chosen replace region.
func markReadCovered(root *protocol.ReadNode, chosen map[string]bool) map[string]bool {
	covered := make(map[string]bool, len(chosen)*4)
	var walk func(n *protocol.ReadNode, inside bool)
	walk = func(n *protocol.ReadNode, inside bool) {
		inside = inside || chosen[n.Ref]
		if inside {
			covered[n.Ref] = true
		}
		for _, child := range n.Children {
			walk(child, inside)
		}
	}
	walk(root, false)
	return covered
}

// collectReadPreorder appends every ref in preorder and, when the maps are
// non-nil, records ref -> node and ref -> parent.
func collectReadPreorder(n *protocol.ReadNode, out []string, nodes map[string]*protocol.ReadNode,
	parents map[string]string) []string {
	if n == nil {
		return out
	}
	out = append(out, n.Ref)
	if nodes != nil {
		nodes[n.Ref] = n
	}
	for _, child := range n.Children {
		if parents != nil && child != nil {
			parents[child.Ref] = n.Ref
		}
		out = collectReadPreorder(child, out, nodes, parents)
	}
	return out
}

// readCosts caches the actual encoded byte costs of the candidate blocks and
// definitions. The cost model is the canonical wire encoding itself, including
// the checked default, the definition anchors and the chain grouping.
type readCosts struct {
	definitions map[*protocol.ReadNode]int
}

func newReadCosts() *readCosts {
	return &readCosts{definitions: map[*protocol.ReadNode]int{}}
}

// definitionBytes returns the encoded size of the packed complete definition.
func (c *readCosts) definitionBytes(n *protocol.ReadNode) int {
	if size, ok := c.definitions[n]; ok {
		return size
	}
	size := len(mustJSON(packDefinitionRoot(n)))
	c.definitions[n] = size
	return size
}

// regionSaving estimates how many canonical bytes a replace block saves over
// the explicit set/children/remove form.
func (c *readCosts) regionSaving(cur *protocol.ReadNode, baseNodes map[string]*protocol.ReadNode,
	kind map[string]int) int {
	replaceCost := len(`"`+cur.Ref+`":`) + c.definitionBytes(cur) + 1
	explicit := 0
	var walk func(n *protocol.ReadNode)
	walk = func(n *protocol.ReadNode) {
		switch kind[n.Ref] {
		case kindGone:
			explicit += len(`"`+n.Ref+`":`) + 2
		case kindChange:
			explicit += len(`"`+n.Ref+`":`) + 2
			explicit += len(mustJSON(map[string]protocol.ReadSetNode{n.Ref: mustReadSet(baseNodes[n.Ref], n)}))
			if !sameReadChildRefs(baseNodes[n.Ref], n) {
				explicit += len(`"`+n.Ref+`":`) + 2
				explicit += len(mustJSON(mustReadChildren(n)))
			}
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(cur)
	return explicit - replaceCost
}

// mustReadSet returns the set entry of two nodes; it is only used by the cost
// model for nodes already classified as changed.
func mustReadSet(prev, cur *protocol.ReadNode) protocol.ReadSetNode {
	change, _ := diffReadFields(prev, cur)
	return change
}

// mustReadChildren returns the child ref list of one node in wire order.
func mustReadChildren(n *protocol.ReadNode) []string {
	list := make([]string, 0, len(n.Children))
	for _, child := range n.Children {
		list = append(list, child.Ref)
	}
	return list
}
