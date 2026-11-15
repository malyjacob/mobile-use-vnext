package observation

import (
	"errors"
	"fmt"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ErrNotRepresentable is returned by Diff when the tree pair has no legal
// delta, and by Apply when a delta does not declare a legal final graph.
var ErrNotRepresentable = errors.New("current tree cannot be represented as a legal delta")

func notRepresentable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotRepresentable, fmt.Sprintf(format, args...))
}

type applyAdapter[N, S any] struct {
	planAdapter[N, S]
	ApplySet              func(N, S)
	SetTouchesInteraction func(S) bool
}

type expandedDefinitions[N any] struct {
	AddOrder      []string
	ApplyOrder    []string
	Nodes         map[string]N
	Children      map[string][]string
	Owner         map[string]string
	RegionOf      map[string]string
	AssemblyRoots map[string]bool
}

// Apply reconstructs current = apply(base, delta). Every block is a
// declaration about one final graph. Validation completes before materializing
// a result, so an illegal delta never yields a partially applied tree.
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
	structural := structuralDelta[*protocol.Node, protocol.SetNode]{
		Set: delta.Set, Add: delta.Add, Remove: delta.Remove,
		Children: delta.Children, Replace: delta.Replace,
	}
	root, err := applyStructuralDelta(base.Root, structural, nodeApplyAdapter())
	if err != nil {
		return nil, err
	}
	return &protocol.Tree{Format: base.Format, Scope: base.Scope, Root: root}, nil
}

func nodeApplyAdapter() applyAdapter[*protocol.Node, protocol.SetNode] {
	return applyAdapter[*protocol.Node, protocol.SetNode]{
		planAdapter: nodePlanAdapter(),
		ApplySet:    applyNodeSet,
		SetTouchesInteraction: func(change protocol.SetNode) bool {
			return change.Label.Set || change.Actions.Set || change.Checked.Set ||
				change.Value.Set || change.Hint.Set || change.Axis.Set || change.Resource.Set
		},
	}
}

// applyStructuralDelta is the shared final-graph interpreter for both the
// complete internal tree and the public logical read tree.
func applyStructuralDelta[N, S any](baseRoot N, delta structuralDelta[N, S],
	adapter applyAdapter[N, S]) (N, error) {

	var zero N
	base, err := indexPlanTree(baseRoot, adapter.planAdapter)
	if err != nil {
		return zero, err
	}
	if adapter.Role(base.Nodes[base.Root]) != protocol.RoleRoot {
		return zero, notRepresentable("baseline root role is not root")
	}

	removeSet, err := validateRemoveRoots(base, delta.Remove, adapter)
	if err != nil {
		return zero, err
	}
	removeRoots := sortedRefSet(removeSet)
	replaceRoots := sortedRefMapKeys(delta.Replace)
	if err := validateReplaceRoots(base, delta.Replace, adapter); err != nil {
		return zero, err
	}
	if err := rejectNestedRegions(removeSet, base.Parent, "remove"); err != nil {
		return zero, err
	}
	if err := rejectNestedRegions(refSet(replaceRoots), base.Parent, "replace"); err != nil {
		return zero, err
	}
	if err := validateStructuralRegions(removeRoots, replaceRoots, base.Parent); err != nil {
		return zero, err
	}

	defs, err := expandDefinitions(base, delta, delta.Children, adapter)
	if err != nil {
		return zero, err
	}

	nodes := make(map[string]N, len(base.Nodes)+len(defs.Nodes))
	for ref, node := range base.Nodes {
		nodes[ref] = adapter.CloneOwn(node)
	}
	for _, ref := range defs.ApplyOrder {
		nodes[ref] = defs.Nodes[ref]
	}

	explicitParents := make(map[string]string)
	for _, parent := range sortedRefMapKeys(delta.Children) {
		if _, exists := nodes[parent]; !exists {
			return zero, notRepresentable("children ref %q is not in the result", parent)
		}
		_, baselineParent := base.Nodes[parent]
		overridableBaseline := baselineParent && defs.Owner[parent] == ""
		if !defs.AssemblyRoots[parent] && !overridableBaseline {
			return zero, notRepresentable("children cannot override closed node %q", parent)
		}
		seen := make(map[string]bool)
		for _, child := range delta.Children[parent] {
			if _, exists := nodes[child]; !exists {
				return zero, notRepresentable("children of %q reference unknown %q", parent, child)
			}
			if seen[child] {
				return zero, notRepresentable("children of %q repeat %q", parent, child)
			}
			seen[child] = true
			if previous, exists := explicitParents[child]; exists && previous != parent {
				return zero, notRepresentable("child %q has two explicit parents", child)
			}
			explicitParents[child] = parent
		}
	}

	eligibleOld := baselineDeletionEligibility(base, removeRoots, replaceRoots)
	finalChildren := make(map[string][]string, len(nodes))
	for _, parent := range sortedRefMapKeys(nodes) {
		list, fromBaseline, err := finalChildList(parent, base, defs, delta.Children)
		if err != nil {
			return zero, err
		}
		seen := make(map[string]bool)
		for _, child := range list {
			if removeSet[child] {
				if fromBaseline {
					continue
				}
				return zero, notRepresentable("final edge %q -> %q targets a remove root", parent, child)
			}
			if _, exists := nodes[child]; !exists {
				return zero, notRepresentable("final edge %q -> %q names an unknown node", parent, child)
			}
			if child == parent {
				return zero, notRepresentable("node %q is its own parent", parent)
			}
			if seen[child] {
				return zero, notRepresentable("parent %q repeats child %q", parent, child)
			}
			seen[child] = true
			finalChildren[parent] = append(finalChildren[parent], child)
		}
	}

	live := make(map[string]bool)
	color := make(map[string]uint8)
	parentClaim := make(map[string]string)
	var visit func(string) error
	visit = func(ref string) error {
		switch color[ref] {
		case 1:
			return notRepresentable("final graph contains a cycle at %q", ref)
		case 2:
			return notRepresentable("final graph reaches %q twice", ref)
		}
		if removeSet[ref] {
			return notRepresentable("remove root %q remains reachable", ref)
		}
		if _, exists := nodes[ref]; !exists {
			return notRepresentable("final graph references missing %q", ref)
		}
		color[ref] = 1
		live[ref] = true
		for _, child := range finalChildren[ref] {
			if child == base.Root {
				return notRepresentable("root %q has a parent", child)
			}
			if _, exists := parentClaim[child]; exists {
				return notRepresentable("child %q has two live parents", child)
			}
			parentClaim[child] = ref
			if err := visit(child); err != nil {
				return err
			}
		}
		color[ref] = 2
		return nil
	}
	if err := visit(base.Root); err != nil {
		return zero, err
	}

	for _, ref := range sortedRefMapKeys(defs.Nodes) {
		if !live[ref] {
			return zero, notRepresentable("complete definition %q is unreachable", ref)
		}
	}
	for _, parent := range sortedRefMapKeys(delta.Children) {
		if !live[parent] {
			return zero, notRepresentable("children parent %q is not live", parent)
		}
		for _, child := range delta.Children[parent] {
			if !live[child] {
				return zero, notRepresentable("children child %q is not live", child)
			}
		}
	}
	for _, ref := range sortedRefMapKeys(delta.Set) {
		if !live[ref] {
			return zero, notRepresentable("set ref %q does not survive", ref)
		}
		if _, baseline := base.Nodes[ref]; !baseline || defs.Owner[ref] != "" {
			return zero, notRepresentable("set must target an undefined baseline survivor %q", ref)
		}
		if ref == base.Root && adapter.SetTouchesInteraction(delta.Set[ref]) {
			return zero, notRepresentable("set cannot add interaction fields to the root")
		}
	}

	for _, ref := range sortedRefMapKeys(nodes) {
		if live[ref] {
			continue
		}
		if !eligibleOld[ref] || defs.Owner[ref] != "" {
			return zero, notRepresentable("unreachable node %q is outside deletion residue", ref)
		}
		delete(nodes, ref)
		delete(finalChildren, ref)
	}

	for _, ref := range sortedRefMapKeys(delta.Set) {
		adapter.ApplySet(nodes[ref], delta.Set[ref])
	}
	for _, ref := range base.Order {
		if live[ref] && adapter.Role(nodes[ref]) != adapter.Role(base.Nodes[ref]) {
			return zero, notRepresentable("role of surviving %q changed", ref)
		}
	}
	if adapter.Role(nodes[base.Root]) != protocol.RoleRoot {
		return zero, notRepresentable("result root role changed")
	}

	var materialize func(string) (N, error)
	materialize = func(ref string) (N, error) {
		node, exists := nodes[ref]
		if !exists {
			return zero, notRepresentable("materialization references missing %q", ref)
		}
		children := make([]N, 0, len(finalChildren[ref]))
		for _, child := range finalChildren[ref] {
			childNode, err := materialize(child)
			if err != nil {
				return zero, err
			}
			children = append(children, childNode)
		}
		return adapter.WithChildren(node, children), nil
	}
	root, err := materialize(base.Root)
	if err != nil {
		return zero, err
	}
	return root, nil
}

func validateRemoveRoots[N, S any](base planIndex[N], refs []string,
	adapter applyAdapter[N, S]) (map[string]bool, error) {

	roots := make(map[string]bool, len(refs))
	for _, ref := range refs {
		node, exists := base.Nodes[ref]
		if !exists {
			return nil, notRepresentable("remove ref %q is not in the baseline", ref)
		}
		if adapter.Role(node) == protocol.RoleRoot {
			return nil, notRepresentable("remove cannot target the root")
		}
		if roots[ref] {
			return nil, notRepresentable("duplicate remove %q", ref)
		}
		roots[ref] = true
	}
	return roots, nil
}

func validateReplaceRoots[N, S any](base planIndex[N], replace map[string]N,
	adapter applyAdapter[N, S]) error {

	for _, ref := range sortedRefMapKeys(replace) {
		if _, exists := base.Nodes[ref]; !exists {
			return notRepresentable("replace ref %q is not in the baseline", ref)
		}
		def := replace[ref]
		if adapter.IsNil(def) {
			return notRepresentable("replace %q has no definition", ref)
		}
		if adapter.Ref(def) != ref {
			return notRepresentable("replace key %q does not match definition ref %q", ref, adapter.Ref(def))
		}
	}
	return nil
}

func expandDefinitions[N, S any](base planIndex[N], delta structuralDelta[N, S],
	explicit map[string][]string, adapter applyAdapter[N, S]) (*expandedDefinitions[N], error) {

	out := &expandedDefinitions[N]{
		Nodes:         make(map[string]N),
		Children:      make(map[string][]string),
		Owner:         make(map[string]string),
		RegionOf:      make(map[string]string),
		AssemblyRoots: make(map[string]bool),
	}
	claim := func(def N, owner, regionRoot string) error {
		if adapter.IsNil(def) {
			return notRepresentable("complete definition is missing")
		}
		ref := adapter.Ref(def)
		if ref == "" {
			return notRepresentable("complete definition has no ref")
		}
		if _, duplicate := out.Owner[ref]; duplicate {
			return notRepresentable("duplicate definition of %q", ref)
		}
		out.Owner[ref] = owner
		if owner == "replace" {
			out.RegionOf[ref] = regionRoot
		}
		children := adapter.Children(def)
		refs := make([]string, 0, len(children))
		for _, child := range children {
			if adapter.IsNil(child) {
				return notRepresentable("definition %q holds a nil child", ref)
			}
			refs = append(refs, adapter.Ref(child))
		}
		out.Nodes[ref] = adapter.CloneOwn(def)
		out.Children[ref] = refs
		out.ApplyOrder = append(out.ApplyOrder, ref)
		return nil
	}

	var visitAdd func(N, bool) error
	visitAdd = func(def N, topLevel bool) error {
		if adapter.IsNil(def) {
			return notRepresentable("add has no definition")
		}
		ref := adapter.Ref(def)
		if _, exists := base.Nodes[ref]; exists {
			return notRepresentable("add ref %q already exists in the baseline", ref)
		}
		if err := claim(def, "add", ""); err != nil {
			return err
		}
		out.AddOrder = append(out.AddOrder, ref)
		_, overridden := explicit[ref]
		if topLevel && len(adapter.Children(def)) == 0 && overridden {
			out.AssemblyRoots[ref] = true
		} else if overridden {
			return notRepresentable("children can only override an empty top-level add root %q", ref)
		}
		for _, child := range adapter.Children(def) {
			if err := visitAdd(child, false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, def := range delta.Add {
		if err := visitAdd(def, true); err != nil {
			return nil, err
		}
	}

	var visitReplace func(string, N, string, bool) error
	visitReplace = func(regionRoot string, def N, parentInDefinition string, topLevel bool) error {
		if adapter.IsNil(def) {
			return notRepresentable("replace has no definition")
		}
		ref := adapter.Ref(def)
		if err := claim(def, "replace", regionRoot); err != nil {
			return err
		}
		if _, overridden := explicit[ref]; overridden {
			return notRepresentable("children cannot override replace definition %q", ref)
		}
		if baselineNode, survives := base.Nodes[ref]; survives {
			if adapter.Role(def) != adapter.Role(baselineNode) {
				return notRepresentable("replace changes surviving role %q", ref)
			}
			if topLevel {
				if ref != regionRoot {
					return notRepresentable("replace key %q and definition ref %q differ", regionRoot, ref)
				}
			} else {
				if !isDescendantOrSelf(ref, regionRoot, base.Parent) {
					return notRepresentable("replace definition %q crosses its baseline region", ref)
				}
				if base.Parent[ref] != parentInDefinition {
					return notRepresentable("replace definition changes surviving parent %q", ref)
				}
			}
		}
		for _, child := range adapter.Children(def) {
			if err := visitReplace(regionRoot, child, ref, false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ref := range sortedRefMapKeys(delta.Replace) {
		if err := visitReplace(ref, delta.Replace[ref], "", true); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func finalChildList[N any](parent string, base planIndex[N],
	defs *expandedDefinitions[N], explicit map[string][]string) ([]string, bool, error) {

	if list, exists := explicit[parent]; exists {
		return list, false, nil
	}
	if list, exists := defs.Children[parent]; exists {
		return list, false, nil
	}
	list, exists := base.Children[parent]
	if !exists {
		return nil, false, notRepresentable("node %q has no children declaration", parent)
	}
	return list, true, nil
}

func validateStructuralRegions(removeRoots, replaceRoots []string, parents map[string]string) error {
	for _, removeRoot := range removeRoots {
		for _, replaceRoot := range replaceRoots {
			if isDescendantOrSelf(removeRoot, replaceRoot, parents) {
				return notRepresentable("remove %q lies inside or equals replace region %q", removeRoot, replaceRoot)
			}
		}
	}
	return nil
}

func baselineDeletionEligibility[N any](base planIndex[N], removes, replaces []string) map[string]bool {
	eligible := make(map[string]bool)
	for _, root := range removes {
		walkBaselineClosure(root, base.Children, func(ref string) {
			eligible[ref] = true
		})
	}
	for _, root := range replaces {
		for _, child := range base.Children[root] {
			walkBaselineClosure(child, base.Children, func(ref string) {
				eligible[ref] = true
			})
		}
	}
	return eligible
}

func walkBaselineClosure(root string, children map[string][]string, visit func(string)) {
	visit(root)
	for _, child := range children[root] {
		walkBaselineClosure(child, children, visit)
	}
}

func rejectNestedRegions(roots map[string]bool, parents map[string]string, what string) error {
	for _, ref := range sortedRefSet(roots) {
		for parent, steps := parents[ref], 0; parent != "" && steps <= len(parents); parent, steps = parents[parent], steps+1 {
			if roots[parent] {
				return notRepresentable("%s root %q is nested in %s root %q", what, ref, what, parent)
			}
		}
	}
	return nil
}

func isDescendantOrSelf(ref, ancestor string, parents map[string]string) bool {
	if ancestor == "" {
		return false
	}
	for current, steps := ref, 0; current != "" && steps <= len(parents); current, steps = parents[current], steps+1 {
		if current == ancestor {
			return true
		}
	}
	return false
}

func refSet(refs []string) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, ref := range refs {
		out[ref] = true
	}
	return out
}

func sortedRefSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for ref := range set {
		out = append(out, ref)
	}
	sortStrings(out)
	return out
}

func sortedRefMapKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for ref := range values {
		out = append(out, ref)
	}
	sortStrings(out)
	return out
}

func applyNodeSet(node *protocol.Node, change protocol.SetNode) {
	if change.Label.Set {
		node.Label = append([]string{}, change.Label.Value...)
	}
	if change.Actions.Set {
		node.Actions = append([]protocol.Action{}, change.Actions.Value...)
	}
	if change.Bounds.Set {
		node.Bounds = cloneBounds(change.Bounds.Value)
	}
	if change.Enabled.Set {
		node.Enabled = cloneOptionalBool(change.Enabled.Value)
	}
	if change.Focused.Set {
		node.Focused = cloneOptionalBool(change.Focused.Value)
	}
	if change.Selected.Set {
		node.Selected = cloneOptionalBool(change.Selected.Value)
	}
	if change.Checked.Set {
		node.Checked = cloneOptionalBool(change.Checked.Value)
	}
	if change.Value.Set {
		node.Value = cloneOptionalString(change.Value.Value)
	}
	if change.Hint.Set {
		node.Hint = cloneOptionalString(change.Hint.Value)
	}
	if change.Protected.Set {
		node.Protected = cloneOptionalBool(change.Protected.Value)
	}
	if change.Axis.Set {
		if change.Axis.Value == nil {
			node.Axis = ""
		} else {
			node.Axis = *change.Axis.Value
		}
	}
	if change.Resource.Set {
		node.Resource = cloneOptionalString(change.Resource.Value)
	}
}

func isCheckableRole(role protocol.Role) bool {
	switch role {
	case protocol.RoleCheckbox, protocol.RoleRadio, protocol.RoleSwitch, protocol.RoleToggle:
		return true
	}
	return false
}
