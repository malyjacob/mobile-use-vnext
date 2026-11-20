package observation

import (
	"bytes"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ReadDiff computes the public delta of two compact read trees. Legacy chains
// are expanded before planning and Add/Replace definitions remain ordinary.
// The delta is applied and compared with the expanded current tree by both
// logical equality and canonical wire bytes before it is returned.
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
	plan, err := planReadStructuralDelta(logicalBase, logicalCurrent)
	if err != nil {
		return nil, err
	}
	delta := &protocol.ReadDelta{
		Format: "delta", Base: baseID, View: protocol.ViewCompact,
		Set: plan.Set, Add: plan.Add, Remove: plan.Remove,
		Children: plan.Children, Replace: plan.Replace,
	}
	if err := delta.Validate(); err != nil {
		return nil, notRepresentable("delta is invalid: %v", err)
	}
	applied, err := ReadApply(base, delta)
	if err != nil {
		return nil, notRepresentable("delta does not apply: %v", err)
	}
	if !ReadEqual(applied, logicalCurrent) {
		return nil, notRepresentable("delta does not reconstruct the current read tree")
	}
	if !bytes.Equal(mustJSON(applied), mustJSON(logicalCurrent)) {
		return nil, notRepresentable("delta does not reconstruct the canonical current encoding")
	}
	return delta, nil
}

func planReadStructuralDelta(base, current *protocol.ReadTree) (*structuralDelta[*protocol.ReadNode, protocol.ReadSetNode], error) {
	adapter := readPlanAdapter()
	baseIndex, err := indexPlanTree(base.Root, adapter)
	if err != nil {
		return nil, err
	}
	currentIndex, err := indexPlanTree(current.Root, adapter)
	if err != nil {
		return nil, err
	}
	return planStructuralDelta(baseIndex, currentIndex, adapter)
}

// ReadApply reconstructs a compact read tree. Legacy chains in the baseline and
// in Add/Replace definitions are expanded before the shared final-graph applier.
// The returned full tree always contains ordinary node definitions.
func ReadApply(base *protocol.ReadTree, delta *protocol.ReadDelta) (*protocol.ReadTree, error) {
	if base == nil || base.Root == nil {
		return nil, notRepresentable("baseline read tree is missing")
	}
	if delta == nil {
		return nil, notRepresentable("delta is missing")
	}
	if err := delta.Validate(); err != nil {
		return nil, notRepresentable("%v", err)
	}
	if base.View != protocol.ViewCompact || delta.View != protocol.ViewCompact {
		return nil, notRepresentable("only compact read trees share a delta baseline")
	}
	if err := base.Validate(); err != nil {
		return nil, notRepresentable("baseline read tree is invalid: %v", err)
	}
	logicalBase := UnpackTree(base)
	if logicalBase == nil || logicalBase.Root == nil {
		return nil, notRepresentable("baseline read tree is missing")
	}
	root, err := applyStructuralDelta(logicalBase.Root, expandReadDelta(delta), readApplyAdapter())
	if err != nil {
		return nil, err
	}
	return &protocol.ReadTree{
		Format: logicalBase.Format, Scope: logicalBase.Scope,
		View: logicalBase.View, Root: root,
	}, nil
}

func expandReadDelta(delta *protocol.ReadDelta) structuralDelta[*protocol.ReadNode, protocol.ReadSetNode] {
	out := structuralDelta[*protocol.ReadNode, protocol.ReadSetNode]{
		Set: delta.Set, Remove: delta.Remove, Children: delta.Children,
	}
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
	return out
}

func readPlanAdapter() planAdapter[*protocol.ReadNode, protocol.ReadSetNode] {
	return planAdapter[*protocol.ReadNode, protocol.ReadSetNode]{
		IsNil:    func(node *protocol.ReadNode) bool { return node == nil },
		Ref:      func(node *protocol.ReadNode) string { return node.Ref },
		Role:     func(node *protocol.ReadNode) protocol.Role { return node.Role },
		Children: func(node *protocol.ReadNode) []*protocol.ReadNode { return node.Children },
		SameOwn:  sameReadOwn,
		DiffSet:  diffReadFields,
		CloneOwn: cloneReadNodeFields,
		WithChildren: func(node *protocol.ReadNode, children []*protocol.ReadNode) *protocol.ReadNode {
			node.Children = children
			return node
		},
		CloneSubtree: func(node *protocol.ReadNode) *protocol.ReadNode {
			return node.Clone()
		},
	}
}

func readApplyAdapter() applyAdapter[*protocol.ReadNode, protocol.ReadSetNode] {
	return applyAdapter[*protocol.ReadNode, protocol.ReadSetNode]{
		planAdapter: readPlanAdapter(),
		ApplySet:    applyReadSet,
		SetTouchesInteraction: func(change protocol.ReadSetNode) bool {
			return change.Label.Set || change.Actions.Set || change.Checked.Set ||
				change.Value.Set || change.Hint.Set || change.Axis.Set || change.Resource.Set
		},
	}
}

func sameReadOwn(a, b *protocol.ReadNode) bool {
	left, right := *a, *b
	left.Children, right.Children = nil, nil
	left.Chain, right.Chain = nil, nil
	return equalReadNode(&left, &right)
}

// diffReadFields builds the explicit set entry of two logical nodes.
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

func applyReadSet(node *protocol.ReadNode, change protocol.ReadSetNode) {
	if change.Label.Set {
		node.Label = append([]string{}, change.Label.Value...)
	}
	if change.Actions.Set {
		node.Actions = append([]protocol.Action{}, change.Actions.Value...)
	}
	if change.Bounds.Set {
		node.Bounds = change.Bounds.Value
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
