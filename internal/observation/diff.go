package observation

import (
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// replaceGain is the estimated saving a replace region must produce before it is
// preferred over the explicit set/children/add/remove form. A replace also has
// a readability cost (the reader is handed a whole region instead of a delta),
// so a marginal win is not enough.
const replaceGain = 2

// Node kinds used while planning.
const (
	kindGone   = iota // baseline node that disappears
	kindKeep          // survives with unchanged own fields and child list
	kindChange        // survives with changed own fields and/or child list
)

// Diff computes a delta from base to current. baseID is the confirmed full
// observation id. It returns ErrNotRepresentable when a legal delta cannot be
// built (a node changed role or parent, regions overlap, or verification fails).
//
// The returned delta is verified: Apply(base, delta) must Equal current. On
// verification failure Diff returns ErrNotRepresentable rather than a delta
// that might decode into a different tree.
func Diff(baseID string, base, current *protocol.Tree) (*protocol.Delta, error) {
	if baseID == "" {
		return nil, notRepresentable("delta base id is empty")
	}
	if base == nil || base.Root == nil || current == nil || current.Root == nil {
		return nil, notRepresentable("baseline or current tree is missing")
	}

	delta, err := planDelta(base, current)
	if err != nil {
		return nil, err
	}
	delta.Format = "delta"
	delta.Base = baseID
	if delta.IsEmpty() {
		// An empty difference is exactly {format, base}: no empty blocks and no
		// nil map that would encode as null.
		return &protocol.Delta{Format: "delta", Base: baseID}, nil
	}
	applied, err := Apply(base, delta)
	if err != nil {
		return nil, notRepresentable("delta does not apply: %v", err)
	}
	if !Equal(applied, current) {
		return nil, notRepresentable("delta does not reconstruct the current tree")
	}
	return delta, nil
}

// planDelta builds the delta blocks that declare current relative to base.
func planDelta(base, current *protocol.Tree) (*protocol.Delta, error) {
	if base.Root.Ref != current.Root.Ref {
		// The root ref anchors the scope: a different root ref is a new scope
		// and needs a full tree, not a delta.
		return nil, notRepresentable("root ref changed from %q to %q", base.Root.Ref, current.Root.Ref)
	}

	baseNodes := map[string]*protocol.Node{}
	baseParent := map[string]string{}
	baseOrder := collectPreorder(base.Root, nil, baseNodes, baseParent)

	curNodes := map[string]*protocol.Node{}
	curParent := map[string]string{}
	curOrder := collectPreorder(current.Root, nil, curNodes, curParent)

	// Reject the pairs no delta can express: a ref cannot change role, move to
	// a different parent, or move into an added subtree (an add must introduce
	// all of its refs).
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
		case sameLogicalNode(baseNodes[ref], cur) && sameChildRefs(baseNodes[ref], cur):
			kind[ref] = kindKeep
		default:
			kind[ref] = kindChange
		}
	}

	// Removed roots: highest disappearing baseline nodes in baseline order. A
	// baseline node with a surviving descendant cannot be removed as a region.
	var removals []string
	for _, ref := range baseOrder {
		if kind[ref] != kindGone || ref == base.Root.Ref {
			continue
		}
		if parent := baseParent[ref]; parent != "" && kind[parent] == kindGone && baseNodes[parent].Role != protocol.RoleRoot {
			continue
		}
		if hasSurvivingDescendant(baseNodes[ref], kind) {
			return nil, notRepresentable("subtree %q holds surviving nodes and cannot be removed", ref)
		}
		removals = append(removals, ref)
	}

	// A region is replaceable when it is a baseline node whose current subtree
	// is exactly its surviving baseline descendants, each still under the same
	// baseline parent, plus nothing else. Such a region is self-contained: no
	// add/remove/children block crosses its boundary, so one replace definition
	// can describe it.
	//
	// The test runs once in post-order: a subtree is closed iff
	//   - every current node in it has a baseline position,
	//   - every child that has a baseline position sits under its baseline
	//     parent, and
	//   - every surviving baseline descendant of the region is present.
	// Both sides are counted bottom-up, so planning stays linear in tree size
	// (a per-node parent-chain walk would be quadratic on deep trees).
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
		// Descendants only: the region root is compared separately by ref.
		count := 0
		for _, child := range baseNodes[ref].Children {
			if kind[child.Ref] != kindGone {
				count++
			}
			count += survivorCount[child.Ref]
		}
		survivorCount[ref] = count
		if ref != base.Root.Ref {
			// currentCount includes the region root itself, so the
			// descendants of the current subtree are currentCount[ref]-1.
			replaceable[ref] = replaceable[ref] && survivorCount[ref] == currentCount[ref]-1
		}
	}

	// Choose regions top-down: a chosen region covers its whole subtree. The
	// covered set is propagated downward during the walk so no node needs to
	// climb its parent chain (that would be quadratic on deep trees).
	chosen := map[string]bool{}
	covered := make(map[string]bool, len(curOrder))
	var chooseRegions func(n *protocol.Node, parentCovered bool)
	chooseRegions = func(n *protocol.Node, parentCovered bool) {
		if parentCovered {
			covered[n.Ref] = true
			return
		}
		if replaceable[n.Ref] && subtreeSize(n) >= replaceMinNodes && estimateSaving(n, baseNodes, kind) > replaceGain {
			chosen[n.Ref] = true
			covered[n.Ref] = true
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

	// Authoritative coverage: a chosen region root and everything under it is
	// fully described by that definition, so no other block may address it.
	covered = markCovered(current.Root, chosen)
	inChosen := func(ref string) bool { return covered[ref] }

	delta := &protocol.Delta{}

	// remove: removal roots outside every replaced region.
	for _, ref := range removals {
		if !inChosen(ref) {
			delta.Remove = append(delta.Remove, ref)
		}
	}
	removed := make(map[string]bool, len(delta.Remove))
	for _, ref := range delta.Remove {
		removed[ref] = true
	}

	// add: highest new current nodes not already introduced by a new parent's
	// add definition. A new node strictly inside a chosen region is covered by
	// that definition and is never listed. A new node that *is* a chosen
	// region root cannot happen: a region root always has a baseline position.
	var addRoots []string
	for _, ref := range curOrder {
		if baseNodes[ref] != nil || ref == current.Root.Ref || chosen[ref] {
			continue
		}
		if p := curParent[ref]; p != "" && inChosen(p) {
			// Introduced by the replace definition of that region.
			continue
		}
		if p := curParent[ref]; p != "" && baseNodes[p] == nil && !chosen[p] {
			// Introduced inside its new parent's complete definition.
			continue
		}
		addRoots = append(addRoots, ref)
		delta.Add = append(delta.Add, curNodes[ref].Clone())
	}
	addRoot := make(map[string]bool, len(addRoots))
	for _, ref := range addRoots {
		addRoot[ref] = true
	}

	// children: every surviving non-region parent whose direct list changed,
	// plus every parent of an added root. A list that only lost removed
	// children is redundant and is skipped.
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
		if sameChildRefs(baseNodes[ref], curNodes[ref]) {
			continue
		}
		needsChildren[ref] = true
	}
	if !sameChildRefs(base.Root, current.Root) {
		needsChildren[current.Root.Ref] = true
	}
	for _, ref := range curOrder {
		if !needsChildren[ref] || baseNodes[ref] == nil {
			continue
		}
		if !childrenBlockUseful(baseNodes[ref], curNodes[ref], curParent, removed) {
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

	// set: changed own fields of surviving nodes outside covered regions.
	for _, ref := range baseOrder {
		if kind[ref] == kindGone || covered[ref] {
			continue
		}
		change, changed := diffFields(baseNodes[ref], curNodes[ref])
		if !changed {
			continue
		}
		if delta.Set == nil {
			delta.Set = map[string]protocol.SetNode{}
		}
		delta.Set[ref] = change
	}

	// replace: complete current subtrees of the chosen regions.
	for _, ref := range curOrder {
		if !chosen[ref] {
			continue
		}
		if delta.Replace == nil {
			delta.Replace = map[string]*protocol.Node{}
		}
		delta.Replace[ref] = curNodes[ref].Clone()
	}

	return delta, nil
}

// regionClosureHolds reports whether the current subtree of ref is exactly the
// surviving baseline descendants of ref, each under its baseline parent.
func regionClosureHolds(ref string, baseNodes map[string]*protocol.Node,
	curNodes map[string]*protocol.Node, curParent map[string]string, kind map[string]int) bool {
	// Surviving baseline descendants of ref, mapped to their baseline parent.
	survivors := map[string]string{}
	var walkBase func(n *protocol.Node)
	walkBase = func(n *protocol.Node) {
		for _, child := range n.Children {
			if kind[child.Ref] != kindGone {
				survivors[child.Ref] = n.Ref
			}
			walkBase(child)
		}
	}
	walkBase(baseNodes[ref])

	// Every current descendant of ref must be such a survivor under that same
	// parent, and every survivor must appear.
	seen := map[string]bool{}
	closed := true
	var walkCur func(n *protocol.Node)
	walkCur = func(n *protocol.Node) {
		for _, child := range n.Children {
			want, ok := survivors[child.Ref]
			if !ok || want != n.Ref {
				closed = false
				return
			}
			seen[child.Ref] = true
			walkCur(child)
		}
	}
	walkCur(curNodes[ref])
	return closed && len(seen) == len(survivors)
}

// Replace-cost weights are a proxy for the encoded size, not for runtime: a
// complete definition costs one unit per node plus its field content, while a
// set block costs the block header, the ref and the changed field names. A
// sparse region is therefore cheaper as set/children, a dense one as replace.
// replaceMinNodes is the smallest region worth replacing. A one- or two-node
// region is always clearer as explicit set/add/remove blocks.
const replaceMinNodes = 3

const (
	costPerNode       = 1 // one definition line in a complete subtree
	costPerSetBlock   = 3 // ref key + changed field names
	costPerRemove     = 2 // remove root entry + header
	costPerAddSubtree = 2 // add root entry + block header
)

// subtreeSize counts the nodes of a subtree including itself.
func subtreeSize(n *protocol.Node) int {
	size := 1
	for _, child := range n.Children {
		size += subtreeSize(child)
	}
	return size
}

// estimateSaving estimates how many cost units a region replacement saves over
// the explicit form.
func estimateSaving(cur *protocol.Node, baseNodes map[string]*protocol.Node, kind map[string]int) int {
	cost := 1 // replace block header
	var size func(n *protocol.Node)
	size = func(n *protocol.Node) {
		cost += costPerNode
		for _, child := range n.Children {
			size(child)
		}
	}
	size(cur)

	explicit := 0
	var walk func(n *protocol.Node)
	walk = func(n *protocol.Node) {
		switch kind[n.Ref] {
		case kindGone:
			explicit += costPerRemove
		case kindChange:
			explicit += costPerSetBlock
			explicit += costPerAddSubtree // children block when the list changed
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(baseNodes[cur.Ref])
	return explicit - cost
}

// childrenBlockUseful reports whether a changed child list needs an explicit
// children block. The block is redundant when the change is exactly "the
// children that are removed elsewhere disappeared" with order unchanged;
// everything else (an addition, a reorder, a child that moved in from another
// parent) needs the complete current list.
func childrenBlockUseful(prev, cur *protocol.Node, curParent map[string]string, removed map[string]bool) bool {
	// Remaining baseline order after the removals must equal the current
	// order for the block to be redundant.
	remaining := make([]string, 0, len(prev.Children))
	for _, child := range prev.Children {
		if removed[child.Ref] {
			continue
		}
		remaining = append(remaining, child.Ref)
	}
	current := make([]string, 0, len(cur.Children))
	for _, child := range cur.Children {
		current = append(current, child.Ref)
	}
	if len(remaining) != len(current) {
		return true
	}
	for i := range remaining {
		if remaining[i] != current[i] {
			return true
		}
	}
	return false
}

// hasSurvivingDescendant reports whether any baseline descendant of n survives.
func hasSurvivingDescendant(n *protocol.Node, kind map[string]int) bool {
	for _, child := range n.Children {
		if kind[child.Ref] != kindGone || hasSurvivingDescendant(child, kind) {
			return true
		}
	}
	return false
}

// diffFields builds the explicit set entry for two nodes with the same ref.
// A field equal to the baseline value is omitted; every changed field is
// emitted with its complete new value, including false, "", [] and null.
func diffFields(prev, cur *protocol.Node) (protocol.SetNode, bool) {
	var change protocol.SetNode
	if !equalStrings(prev.Label, cur.Label) {
		change.Label = protocol.Field[[]string]{Set: true, Value: append([]string{}, cur.Label...)}
	}
	if !equalActions(prev.Actions, cur.Actions) {
		change.Actions = protocol.Field[[]protocol.Action]{Set: true, Value: append([]protocol.Action{}, cur.Actions...)}
	}
	if !equalBounds(prev.Bounds, cur.Bounds) {
		change.Bounds = protocol.Field[*protocol.Bounds]{Set: true, Value: cloneBounds(cur.Bounds)}
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
		// The zero axis means "no known axis"; set must still express the
		// change, and the protocol decodes a null axis back to the zero value.
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

// markCovered returns the set of refs that are a chosen region root or live
// inside one.
func markCovered(root *protocol.Node, chosen map[string]bool) map[string]bool {
	covered := make(map[string]bool, len(chosen)*4)
	var walk func(n *protocol.Node, inside bool)
	walk = func(n *protocol.Node, inside bool) {
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

// inChosenRegion reports whether ref is a chosen region root or lives inside
// one. Such nodes are fully described by the replace definition.
func inChosenRegion(ref string, parents map[string]string, chosen map[string]bool) bool {
	// The walk is bounded by the map size so a malformed parent map (for
	// example a duplicated ref from a broken caller) cannot loop forever.
	for p, steps := ref, 0; p != "" && steps <= len(parents); p, steps = parents[p], steps+1 {
		if chosen[p] {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalActions(a, b []protocol.Action) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBounds(a, b *protocol.Bounds) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func cloneBounds(b *protocol.Bounds) *protocol.Bounds {
	if b == nil {
		return nil
	}
	c := *b
	return &c
}

// collectPreorder appends every ref in preorder and, when the maps are non-nil,
// records ref -> node and ref -> parent.
func collectPreorder(n *protocol.Node, out []string, nodes map[string]*protocol.Node, parents map[string]string) []string {
	if n == nil {
		// A caller-built tree must not contain nil children; skipping is
		// safer than panicking, and Diff rejects the pair through Apply.
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
		out = collectPreorder(child, out, nodes, parents)
	}
	return out
}
