package observation

import (
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	replaceGain       = 2
	replaceMinNodes   = 3
	costPerNode       = 1
	costPerSetBlock   = 3
	costPerRemove     = 2
	costPerAddSubtree = 2
)

type nodeKind uint8

const (
	kindGone nodeKind = iota
	kindKeep
	kindChange
)

type planIndex[N any] struct {
	Root     string
	Order    []string
	Nodes    map[string]N
	Children map[string][]string
	Parent   map[string]string
}

type structuralDelta[N, S any] struct {
	Set      map[string]S
	Add      []N
	Remove   []string
	Children map[string][]string
	Replace  map[string]N
}

type planAdapter[N, S any] struct {
	IsNil        func(N) bool
	Ref          func(N) string
	Role         func(N) protocol.Role
	Children     func(N) []N
	SameOwn      func(N, N) bool
	DiffSet      func(N, N) (S, bool)
	CloneOwn     func(N) N
	WithChildren func(N, []N) N
	CloneSubtree func(N) N
}

// Diff computes a delta from base to current. baseID is the confirmed full
// observation id. It returns ErrNotRepresentable when no legal final graph can
// describe the pair, or when the generated delta does not reconstruct current.
func Diff(baseID string, base, current *protocol.Tree) (*protocol.Delta, error) {
	if baseID == "" {
		return nil, notRepresentable("delta base id is empty")
	}
	if base == nil || base.Root == nil || current == nil || current.Root == nil {
		return nil, notRepresentable("baseline or current tree is missing")
	}
	plan, err := planNodeStructuralDelta(base.Root, current.Root)
	if err != nil {
		return nil, err
	}
	delta := &protocol.Delta{
		Format: "delta", Base: baseID,
		Set: plan.Set, Add: plan.Add, Remove: plan.Remove,
		Children: plan.Children, Replace: plan.Replace,
	}
	if delta.IsEmpty() {
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

func planNodeStructuralDelta(base, current *protocol.Node) (*structuralDelta[*protocol.Node, protocol.SetNode], error) {
	adapter := nodePlanAdapter()
	baseIndex, err := indexPlanTree(base, adapter)
	if err != nil {
		return nil, err
	}
	currentIndex, err := indexPlanTree(current, adapter)
	if err != nil {
		return nil, err
	}
	return planStructuralDelta(baseIndex, currentIndex, adapter)
}

// planStructuralDelta computes the single final graph described by the five
// delta blocks. Every decision follows preorder or a sorted key list; maps are
// lookup tables only.
func planStructuralDelta[N, S any](base, current planIndex[N],
	adapter planAdapter[N, S]) (*structuralDelta[N, S], error) {

	if base.Root == "" || current.Root == "" {
		return nil, notRepresentable("tree root is missing")
	}
	if base.Root != current.Root {
		return nil, notRepresentable("root ref changed")
	}

	kind := make(map[string]nodeKind, len(base.Order))
	for _, ref := range base.Order {
		cur, ok := current.Nodes[ref]
		if !ok {
			kind[ref] = kindGone
			continue
		}
		if adapter.Role(cur) != adapter.Role(base.Nodes[ref]) {
			return nil, notRepresentable("role of %q changed", ref)
		}
		if adapter.SameOwn(base.Nodes[ref], cur) &&
			sameRefList(base.Children[ref], current.Children[ref]) {
			kind[ref] = kindKeep
		} else {
			kind[ref] = kindChange
		}
	}

	chosen, covered, baseCovered := chooseReplaceRegions(base, current, kind)
	removeRoots := collectRemoveRoots(base, kind, baseCovered)
	assembly, addRoots := computeAddFrontier(base, current, covered)

	removeSet := make(map[string]bool, len(removeRoots))
	for _, ref := range removeRoots {
		removeSet[ref] = true
	}

	childBlocks := collectChildBlocks(base, current, covered, assembly, addRoots, removeSet)
	if len(childBlocks) == 0 {
		childBlocks = nil
	}
	delta := &structuralDelta[N, S]{Remove: removeRoots, Children: childBlocks}
	for _, ref := range addRoots {
		delta.Add = append(delta.Add, cloneAddDefinition(current, ref, assembly, adapter))
	}
	for _, ref := range base.Order {
		if kind[ref] == kindGone || covered[ref] {
			continue
		}
		change, changed := adapter.DiffSet(base.Nodes[ref], current.Nodes[ref])
		if !changed {
			continue
		}
		if delta.Set == nil {
			delta.Set = make(map[string]S)
		}
		delta.Set[ref] = change
	}
	for _, ref := range current.Order {
		if !chosen[ref] {
			continue
		}
		if delta.Replace == nil {
			delta.Replace = make(map[string]N)
		}
		delta.Replace[ref] = adapter.CloneSubtree(current.Nodes[ref])
	}

	if err := validatePlannedOwnership(base, current, kind, covered,
		assembly, addRoots, childBlocks, delta, adapter); err != nil {
		return nil, err
	}
	return delta, nil
}

func chooseReplaceRegions[N any](base, current planIndex[N],
	kind map[string]nodeKind) (map[string]bool, map[string]bool, map[string]bool) {

	replaceable := make(map[string]bool, len(current.Order))
	currentSize := make(map[string]int, len(current.Order))
	for i := len(current.Order) - 1; i >= 0; i-- {
		ref := current.Order[i]
		size := 1
		_, closed := base.Nodes[ref]
		for _, child := range current.Children[ref] {
			size += currentSize[child]
			if _, ok := base.Nodes[child]; !ok ||
				base.Parent[child] != ref || !replaceable[child] {
				closed = false
			}
		}
		currentSize[ref] = size
		if ref != current.Root {
			replaceable[ref] = closed
		}
	}

	survivorCount := make(map[string]int, len(base.Order))
	explicitCost := make(map[string]int, len(base.Order))
	for i := len(base.Order) - 1; i >= 0; i-- {
		ref := base.Order[i]
		count := 0
		own := 0
		switch kind[ref] {
		case kindGone:
			own = costPerRemove
		case kindChange:
			own = costPerSetBlock
			if !sameRefList(base.Children[ref], current.Children[ref]) {
				own += costPerAddSubtree
			}
		}
		for _, child := range base.Children[ref] {
			if kind[child] != kindGone {
				count++
			}
			count += survivorCount[child]
			own += explicitCost[child]
		}
		survivorCount[ref] = count
		explicitCost[ref] = own
		if ref != base.Root {
			replaceable[ref] = replaceable[ref] && count == currentSize[ref]-1
		}
	}

	chosen := make(map[string]bool)
	var choose func(string, bool)
	choose = func(ref string, parentCovered bool) {
		if parentCovered {
			return
		}
		saving := explicitCost[ref] - (1 + costPerNode*currentSize[ref])
		if replaceable[ref] && currentSize[ref] >= replaceMinNodes && saving > replaceGain {
			chosen[ref] = true
			for _, child := range current.Children[ref] {
				choose(child, true)
			}
			return
		}
		for _, child := range current.Children[ref] {
			choose(child, false)
		}
	}
	choose(current.Root, false)

	currentCovered := markCurrentCoverage(current.Root, chosen, current.Children)
	baseCovered := markBaselineCoverage(base, chosen)
	return chosen, currentCovered, baseCovered
}

func markCurrentCoverage(root string, chosen map[string]bool, children map[string][]string) map[string]bool {
	covered := make(map[string]bool)
	var walk func(string, bool)
	walk = func(ref string, inside bool) {
		inside = inside || chosen[ref]
		if inside {
			covered[ref] = true
		}
		for _, child := range children[ref] {
			walk(child, inside)
		}
	}
	walk(root, false)
	return covered
}

func markBaselineCoverage[N any](base planIndex[N], chosen map[string]bool) map[string]bool {
	covered := make(map[string]bool)
	var walk func(string)
	walk = func(ref string) {
		covered[ref] = true
		for _, child := range base.Children[ref] {
			walk(child)
		}
	}
	for _, ref := range base.Order {
		if chosen[ref] {
			walk(ref)
		}
	}
	return covered
}

func collectRemoveRoots[N any](base planIndex[N], kind map[string]nodeKind,
	baseCovered map[string]bool) []string {

	var roots []string
	insideRemoved := make(map[string]bool, len(base.Order))
	for _, ref := range base.Order {
		insideRemoved[ref] = insideRemoved[base.Parent[ref]]
		if kind[ref] != kindGone || ref == base.Root || baseCovered[ref] {
			continue
		}
		if insideRemoved[ref] {
			continue
		}
		roots = append(roots, ref)
		insideRemoved[ref] = true
	}
	return roots
}

func computeSurvivorFlags[N any](base, current planIndex[N]) map[string]bool {
	hasSurvivor := make(map[string]bool, len(current.Order))
	for i := len(current.Order) - 1; i >= 0; i-- {
		ref := current.Order[i]
		found := false
		for _, child := range current.Children[ref] {
			if _, exists := base.Nodes[child]; exists || hasSurvivor[child] {
				found = true
			}
		}
		hasSurvivor[ref] = found
	}
	return hasSurvivor
}

func computeAddFrontier[N any](base, current planIndex[N],
	covered map[string]bool) (map[string]bool, []string) {

	hasSurvivor := computeSurvivorFlags(base, current)
	assembly := make(map[string]bool)
	for _, ref := range current.Order {
		if _, exists := base.Nodes[ref]; exists || covered[ref] {
			continue
		}
		if hasSurvivor[ref] {
			assembly[ref] = true
		}
	}

	var addRoots []string
	for _, ref := range current.Order {
		if _, exists := base.Nodes[ref]; exists || covered[ref] {
			continue
		}
		parent := current.Parent[ref]
		if parent != "" {
			_, parentInBase := base.Nodes[parent]
			if !parentInBase && !assembly[parent] {
				continue
			}
		}
		addRoots = append(addRoots, ref)
	}
	return assembly, addRoots
}

func cloneAddDefinition[N, S any](current planIndex[N], ref string,
	assembly map[string]bool, adapter planAdapter[N, S]) N {

	out := adapter.CloneOwn(current.Nodes[ref])
	if assembly[ref] {
		return out
	}
	children := make([]N, 0, len(current.Children[ref]))
	for _, child := range current.Children[ref] {
		children = append(children, cloneAddDefinition(current, child, assembly, adapter))
	}
	return adapter.WithChildren(out, children)
}

func collectChildBlocks[N any](base, current planIndex[N], covered, assembly map[string]bool,
	addRoots []string, removed map[string]bool) map[string][]string {

	needs := make(map[string]bool)
	for _, ref := range addRoots {
		if parent := current.Parent[ref]; parent != "" {
			needs[parent] = true
		}
	}
	for _, ref := range current.Order {
		if assembly[ref] {
			needs[ref] = true
		}
	}
	for _, ref := range current.Order {
		if _, exists := base.Nodes[ref]; !exists || covered[ref] {
			continue
		}
		if sameRefList(base.Children[ref], current.Children[ref]) {
			continue
		}
		if childListChangeIsOnlyRemovedRoots(base.Children[ref], current.Children[ref], removed) {
			continue
		}
		needs[ref] = true
	}

	out := make(map[string][]string)
	for _, ref := range current.Order {
		if !needs[ref] {
			continue
		}
		list := append([]string{}, current.Children[ref]...)
		if list == nil {
			list = []string{}
		}
		out[ref] = list
	}
	return out
}

func childListChangeIsOnlyRemovedRoots(old, current []string, removed map[string]bool) bool {
	filtered := make([]string, 0, len(old))
	for _, ref := range old {
		if !removed[ref] {
			filtered = append(filtered, ref)
		}
	}
	return sameRefList(filtered, current)
}

func validatePlannedOwnership[N, S any](base, current planIndex[N],
	kind map[string]nodeKind, covered, assembly map[string]bool,
	addRoots []string, childBlocks map[string][]string,
	delta *structuralDelta[N, S], adapter planAdapter[N, S]) error {

	introduced := make(map[string]string)
	var walkAdd func(N) error
	walkAdd = func(node N) error {
		ref := adapter.Ref(node)
		if _, baseline := base.Nodes[ref]; baseline {
			return notRepresentable("add definition repeats baseline ref %q", ref)
		}
		if owner, exists := introduced[ref]; exists {
			return notRepresentable("definition of %q occurs under %s and add", ref, owner)
		}
		introduced[ref] = "add"
		for _, child := range adapter.Children(node) {
			if err := walkAdd(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range delta.Add {
		if err := walkAdd(node); err != nil {
			return err
		}
	}

	var walkReplace func(N) error
	walkReplace = func(node N) error {
		ref := adapter.Ref(node)
		if owner, exists := introduced[ref]; exists {
			return notRepresentable("definition of %q occurs under %s and replace", ref, owner)
		}
		introduced[ref] = "replace"
		for _, child := range adapter.Children(node) {
			if err := walkReplace(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ref := range sortedRefMapKeys(delta.Replace) {
		if _, baseline := base.Nodes[ref]; !baseline {
			return notRepresentable("replace root %q is not in the baseline", ref)
		}
		if err := walkReplace(delta.Replace[ref]); err != nil {
			return err
		}
	}

	for _, ref := range current.Order {
		_, baseline := base.Nodes[ref]
		if !baseline && introduced[ref] != "add" {
			return notRepresentable("new ref %q has no complete definition", ref)
		}
	}
	for _, ref := range sortedRefMapKeys(introduced) {
		owner := introduced[ref]
		if owner == "add" {
			if _, exists := current.Nodes[ref]; !exists {
				return notRepresentable("add definition %q is absent from current", ref)
			}
		}
	}

	for _, ref := range addRoots {
		parent := current.Parent[ref]
		if parent == "" || !containsRef(childBlocks[parent], ref) {
			return notRepresentable("top-level add %q has no final parent claim", ref)
		}
	}
	for _, ref := range sortedRefSet(assembly) {
		if _, exists := childBlocks[ref]; !exists {
			return notRepresentable("assembly root %q has no children block", ref)
		}
	}
	for _, parent := range sortedRefMapKeys(childBlocks) {
		children := childBlocks[parent]
		_, baselineParent := base.Nodes[parent]
		if !baselineParent && !assembly[parent] {
			return notRepresentable("children block targets closed new node %q", parent)
		}
		if covered[parent] && !assembly[parent] {
			return notRepresentable("children block targets replace region %q", parent)
		}
		for _, child := range children {
			if _, exists := current.Nodes[child]; !exists {
				return notRepresentable("children block of %q names unknown %q", parent, child)
			}
		}
	}

	for _, ref := range base.Order {
		if kind[ref] == kindGone {
			continue
		}
		if _, exists := current.Nodes[ref]; !exists {
			return notRepresentable("surviving baseline %q is absent from current", ref)
		}
	}
	return nil
}

func containsRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

func sameRefList(a, b []string) bool {
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

func indexPlanTree[N, S any](root N, adapter planAdapter[N, S]) (planIndex[N], error) {
	index := planIndex[N]{
		Nodes:    make(map[string]N),
		Children: make(map[string][]string),
		Parent:   make(map[string]string),
	}
	if adapter.IsNil(root) {
		return index, notRepresentable("tree root is missing")
	}
	var walk func(N, string) error
	walk = func(node N, parent string) error {
		if adapter.IsNil(node) {
			return notRepresentable("tree holds a nil node")
		}
		ref := adapter.Ref(node)
		if ref == "" {
			return notRepresentable("tree node has no ref")
		}
		if _, duplicate := index.Nodes[ref]; duplicate {
			return notRepresentable("duplicate ref %q", ref)
		}
		index.Nodes[ref] = node
		index.Parent[ref] = parent
		index.Order = append(index.Order, ref)
		children := adapter.Children(node)
		refs := make([]string, 0, len(children))
		for _, child := range children {
			if adapter.IsNil(child) {
				return notRepresentable("node %q has a nil child", ref)
			}
			refs = append(refs, adapter.Ref(child))
		}
		index.Children[ref] = refs
		for _, child := range children {
			if err := walk(child, ref); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return planIndex[N]{}, err
	}
	index.Root = adapter.Ref(root)
	return index, nil
}

func nodePlanAdapter() planAdapter[*protocol.Node, protocol.SetNode] {
	return planAdapter[*protocol.Node, protocol.SetNode]{
		IsNil: func(node *protocol.Node) bool { return node == nil },
		Ref:   func(node *protocol.Node) string { return node.Ref },
		Role:  func(node *protocol.Node) protocol.Role { return node.Role },
		Children: func(node *protocol.Node) []*protocol.Node {
			return node.Children
		},
		SameOwn:  sameNodeOwn,
		DiffSet:  diffFields,
		CloneOwn: cloneNodeOwn,
		WithChildren: func(node *protocol.Node, children []*protocol.Node) *protocol.Node {
			node.Children = children
			return node
		},
		CloneSubtree: func(node *protocol.Node) *protocol.Node {
			return node.Clone()
		},
	}
}

func sameNodeOwn(a, b *protocol.Node) bool {
	left, right := *a, *b
	left.Children, right.Children = nil, nil
	return equalNode(&left, &right)
}

func cloneNodeOwn(node *protocol.Node) *protocol.Node {
	out := *node
	out.Children = nil
	out.Bounds = cloneBounds(node.Bounds)
	out.Label = append([]string(nil), node.Label...)
	out.Actions = append([]protocol.Action(nil), node.Actions...)
	out.Enabled = cloneOptionalBool(node.Enabled)
	out.Focused = cloneOptionalBool(node.Focused)
	out.Selected = cloneOptionalBool(node.Selected)
	out.Checked = cloneOptionalBool(node.Checked)
	out.Protected = cloneOptionalBool(node.Protected)
	out.Value = cloneOptionalString(node.Value)
	out.Hint = cloneOptionalString(node.Hint)
	out.Resource = cloneOptionalString(node.Resource)
	return &out
}

func cloneOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// diffFields builds the explicit set entry for two nodes with the same ref.
// A field equal to the baseline value is omitted; every changed field carries
// its complete new value, including false, "", [] and null.
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
	if !equalOptionalBool(prev.Enabled, cur.Enabled) {
		change.Enabled = protocol.Field[*bool]{Set: true, Value: cloneOptionalBool(cur.Enabled)}
	}
	if !equalOptionalBool(prev.Focused, cur.Focused) {
		change.Focused = protocol.Field[*bool]{Set: true, Value: cloneOptionalBool(cur.Focused)}
	}
	if !equalOptionalBool(prev.Selected, cur.Selected) {
		change.Selected = protocol.Field[*bool]{Set: true, Value: cloneOptionalBool(cur.Selected)}
	}
	if !equalOptionalBool(prev.Protected, cur.Protected) {
		change.Protected = protocol.Field[*bool]{Set: true, Value: cloneOptionalBool(cur.Protected)}
	}
	if !equalOptionalBool(prev.Checked, cur.Checked) {
		change.Checked = protocol.Field[*bool]{Set: true, Value: cloneOptionalBool(cur.Checked)}
	}
	if !equalOptionalString(prev.Value, cur.Value) {
		change.Value = protocol.Field[*string]{Set: true, Value: cloneOptionalString(cur.Value)}
	}
	if !equalOptionalString(prev.Hint, cur.Hint) {
		change.Hint = protocol.Field[*string]{Set: true, Value: cloneOptionalString(cur.Hint)}
	}
	if prev.Axis != cur.Axis {
		var axis *protocol.Axis
		if cur.Axis != "" {
			value := cur.Axis
			axis = &value
		}
		change.Axis = protocol.Field[*protocol.Axis]{Set: true, Value: axis}
	}
	if !equalOptionalString(prev.Resource, cur.Resource) {
		change.Resource = protocol.Field[*string]{Set: true, Value: cloneOptionalString(cur.Resource)}
	}
	return change, !change.IsZero()
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

func cloneBounds(bounds *protocol.Bounds) *protocol.Bounds {
	if bounds == nil {
		return nil
	}
	copy := *bounds
	return &copy
}
