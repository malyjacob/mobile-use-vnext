package observation

import (
	"errors"
	"strconv"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Snapshot is one immutable public projection of a capture.
//
// Published objects are shared with callers and must never be mutated. Build
// never mutates a previously published Snapshot or any Node reachable from it.
type Snapshot struct {
	Tree    *protocol.Tree
	Capture *device.Capture
	// Source maps a public ref to the index in Capture.Nodes of the raw node
	// it re-validates. Context nodes have no readable geometry, so only
	// non-context nodes (and the synthetic root, which maps to the raw root)
	// appear here.
	Source map[string]int
	// Refs lists the public refs in preorder.
	Refs []string
}

// unit is the projection decision for one capture node.
type unit struct {
	published bool // has its own public node (never true for the raw root)
	context   bool // kept only to preserve reading ancestry
	role      protocol.Role
}

// Builder owns the per-session conservative ref continuity state.
//
// A Builder must be driven by one goroutine at a time; it holds no locks of
// its own and mutates nothing else.
type Builder struct {
	prev    *Snapshot
	nextRef int
}

// NewBuilder returns a Builder with no continuity state.
func NewBuilder() *Builder {
	return &Builder{nextRef: 1}
}

// Previous returns the last snapshot built by this builder (may be nil).
func (b *Builder) Previous() *Snapshot { return b.prev }

// Reset discards continuity state for a new session/scope. Ref numbers
// restart.
func (b *Builder) Reset() {
	b.prev = nil
	b.nextRef = 1
}

// Build projects one capture to a complete public tree. It is pure with
// respect to the previous snapshot: no published object is mutated.
//
// A capture without a usable root or with an empty display is rejected with
// an error; nothing is published and the previous snapshot is left untouched.
func (b *Builder) Build(cap *device.Capture) (*Snapshot, error) {
	if cap == nil {
		return nil, invalidTree("capture is missing")
	}
	if cap.Display.Width <= 0 || cap.Display.Height <= 0 {
		return nil, invalidTree("display rectangle is empty")
	}
	if cap.Root < 0 || cap.Root >= len(cap.Nodes) {
		return nil, invalidTree("capture root is missing")
	}
	if cap.Nodes[cap.Root].Parent != -1 {
		return nil, invalidTree("capture root has a parent")
	}

	units := project(cap)

	// Refs are computed against the previous snapshot, per raw node, so newly
	// published units get monotonic numbers in preorder.
	refs := b.assignRefs(cap, units)

	// Build bottom-up: a parent copies the public nodes of its published
	// children in source order.
	public := make([]*protocol.Node, len(cap.Nodes))
	for _, i := range postOrder(cap) {
		u := units[i]
		if i == cap.Root || !u.published {
			continue
		}
		node := newPublicNode(cap, i, u, refs[i])
		for _, child := range cap.Nodes[i].Children {
			if child < 0 || child >= len(cap.Nodes) {
				continue
			}
			if !hasAncestorPath(cap, child, i) || public[child] == nil {
				continue
			}
			node.Children = append(node.Children, public[child])
		}
		public[i] = node
	}

	root := newRootNode(cap.Display, refs[cap.Root])
	for _, child := range cap.Nodes[cap.Root].Children {
		if child < 0 || child >= len(cap.Nodes) || public[child] == nil {
			continue
		}
		root.Children = append(root.Children, public[child])
	}

	snap := &Snapshot{Capture: cap, Source: make(map[string]int)}
	snap.Source[root.Ref] = cap.Root
	for i := range cap.Nodes {
		if public[i] != nil && !units[i].context {
			snap.Source[public[i].Ref] = i
		}
	}
	snap.Tree = &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
	collectRefs(root, &snap.Refs)

	b.prev = snap
	return snap, nil
}

// invalidTree builds the plain error for an unusable capture. It is not a
// protocol error: the engine decides how to report a failed tree acquisition
// (observation status/error code) and this package must not invent one.
func invalidTree(message string) error {
	return errors.New("observation: " + message)
}

// hasAncestorPath reports whether target is a descendant of ancestor, so that a
// malformed capture with a broken parent index cannot graft an unrelated
// subtree into the public tree.
func hasAncestorPath(cap *device.Capture, target, ancestor int) bool {
	for p := target; p >= 0 && p < len(cap.Nodes); {
		if p == ancestor {
			return true
		}
		next := cap.Nodes[p].Parent
		if next == p {
			return false
		}
		p = next
	}
	return false
}

// project decides which source nodes become public units.
//
// Rules (design §2-§4):
//   - Only nodes of the target display take part.
//   - A visible node is kept unless it is a semantically empty leaf.
//   - An ancestor needed to connect kept nodes is kept too. When such an
//     ancestor is not reported visible it becomes a context node
//     (role group, no bounds, no actions, no label, context=true).
//   - The raw traversal root is never published: the synthetic root takes its
//     place, so the raw root's kept children become children of root.
func project(cap *device.Capture) []unit {
	units := make([]unit, len(cap.Nodes))
	order := postOrder(cap)
	inScope := make([]bool, len(cap.Nodes))
	for _, i := range order {
		inScope[i] = cap.Nodes[i].DisplayID == cap.Display.ID
	}

	// inTree: a published node lives in this node's subtree. It is computed
	// children-first, so a parent always sees its children's decisions.
	inTree := make([]bool, len(cap.Nodes))
	for _, i := range order {
		if i == cap.Root {
			continue
		}
		raw := &cap.Nodes[i]
		childInTree := false
		for _, child := range raw.Children {
			if child >= 0 && child < len(inTree) && inTree[child] && hasAncestorPath(cap, child, i) {
				childInTree = true
				break
			}
		}
		switch {
		case !inScope[i]:
			// A node of another display is a wrapper holding target-display
			// content. It is part of no target-display scope, so it is never
			// published; its kept descendants attach to the nearest published
			// ancestor instead.
			continue
		case raw.Visible && (hasSemantics(raw) || childInTree):
			units[i] = unit{published: true, role: classifyRole(raw)}
		case raw.Visible:
			// Visible but semantically empty and without kept descendants:
			// nothing worth reading, nothing to act on.
			continue
		case childInTree:
			units[i] = unit{published: true, context: true, role: protocol.RoleGroup}
		}
		inTree[i] = units[i].published
	}
	// The raw root is never a public unit even when it is in scope: the
	// synthetic root takes its place and its kept children become root's
	// children.
	return units
}

// assignRefs maps every published (or root) raw index to a ref, continuing the
// previous snapshot's refs conservatively.
func (b *Builder) assignRefs(cap *device.Capture, units []unit) []string {
	refs := make([]string, len(cap.Nodes))
	b.matchRefs(cap, units, refs)
	return refs
}

// postOrder returns children before parents, starting at the traversal root.
func postOrder(cap *device.Capture) []int {
	order := make([]int, 0, len(cap.Nodes))
	seen := make([]bool, len(cap.Nodes))
	type frame struct{ index, next int }
	stack := []frame{{index: cap.Root}}
	seen[cap.Root] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		children := cap.Nodes[top.index].Children
		if top.next < len(children) {
			child := children[top.next]
			top.next++
			if child >= 0 && child < len(cap.Nodes) && !seen[child] && hasAncestorPath(cap, child, top.index) {
				seen[child] = true
				stack = append(stack, frame{index: child})
			}
			continue
		}
		order = append(order, top.index)
		stack = stack[:len(stack)-1]
	}
	return order
}

// hasSemantics reports whether the raw node carries anything worth reading or
// acting on. It is the "not semantically empty" test of design §3.2.
func hasSemantics(raw *device.RawNode) bool {
	return raw.TextSet || raw.DescSet || raw.HasEnabled || raw.HasChecked ||
		raw.HasSelected || raw.HasFocused || raw.HasClickable ||
		raw.HasLongClick || raw.HasScrollable || raw.HasCheckable ||
		raw.HasPassword || raw.Editable
}

// classifyRole maps source evidence to a public role (design §4.3). Only
// attributes the source actually reported are used; a missing attribute never
// becomes a capability.
func classifyRole(raw *device.RawNode) protocol.Role {
	if raw.Editable {
		return protocol.RoleInput
	}
	if raw.HasCheckable && raw.Checkable {
		class := raw.ClassName
		switch {
		case strings.Contains(class, "CheckBox"):
			return protocol.RoleCheckbox
		case strings.Contains(class, "RadioButton"):
			return protocol.RoleRadio
		case strings.Contains(class, "Switch"), strings.Contains(class, "ToggleButton"):
			return protocol.RoleSwitch
		default:
			return protocol.RoleToggle
		}
	}
	if raw.HasScrollable && raw.Scrollable {
		return protocol.RoleScroll
	}
	label := labelParts(raw)
	if raw.HasClickable && raw.Clickable {
		// A labelled control acts like a button; an unlabelled clickable area
		// keeps a view role but still carries the tap action.
		if len(label) > 0 {
			return protocol.RoleButton
		}
		return protocol.RoleView
	}
	if strings.Contains(raw.ClassName, "Image") && len(label) == 0 {
		return protocol.RoleImage
	}
	if strings.Contains(raw.ClassName, "TextView") && len(label) > 0 {
		return protocol.RoleText
	}
	return protocol.RoleView
}

// labelParts returns the readable label of one public unit in the fixed order:
// own text, then own description when it differs. The whole string is kept;
// text is never split into business fields.
func labelParts(raw *device.RawNode) []string {
	if raw.HasPassword && raw.Password {
		// A protected input's text is never published.
		return nil
	}
	var label []string
	if raw.TextSet && raw.Text != "" {
		label = append(label, raw.Text)
	}
	if raw.DescSet && raw.Description != "" && raw.Description != raw.Text {
		label = append(label, raw.Description)
	}
	return label
}

// newPublicNode projects one published source node to its complete public node.
func newPublicNode(cap *device.Capture, index int, u unit, ref string) *protocol.Node {
	raw := &cap.Nodes[index]
	node := &protocol.Node{
		Ref:      ref,
		Role:     u.role,
		Label:    []string{},
		Actions:  []protocol.Action{},
		Children: []*protocol.Node{},
	}
	if u.context {
		// Context nodes exist for reading ancestry only: no geometry, no
		// capability, no readable content of their own.
		node.Role = protocol.RoleGroup
		node.Context = true
		return node
	}

	node.Label = append(node.Label, labelParts(raw)...)
	node.Bounds = displayBounds(cap, raw)
	node.Actions = actionsOf(raw)
	node.Enabled = triBool(raw.HasEnabled, raw.Enabled)
	node.Focused = triBool(raw.HasFocused, raw.Focused)
	node.Selected = triBool(raw.HasSelected, raw.Selected)
	node.Protected = triBool(raw.HasPassword, raw.Password)
	if raw.ResourceSet && raw.ResourceID != "" && needsResourceHint(raw, len(node.Label) == 0) {
		r := raw.ResourceID
		node.Resource = &r
	}

	switch node.Role {
	case protocol.RoleInput:
		// input always publishes value: "" is a known empty value, null is
		// "the value was not obtained". A protected input's text is never
		// published either as label or value.
		if raw.TextSet && !(raw.HasPassword && raw.Password) {
			v := raw.Text
			node.Value = &v
		}
		node.Checked = triBool(raw.HasChecked, raw.Checked)
	case protocol.RoleCheckbox, protocol.RoleRadio, protocol.RoleSwitch, protocol.RoleToggle:
		// Checkable roles always publish checked explicitly (true/false/null)
		// so the reader never has to guess.
		node.Checked = triBool(raw.HasChecked, raw.Checked)
	default:
		if raw.HasChecked {
			node.Checked = triBool(true, raw.Checked)
		}
	}
	return node
}

// needsResourceHint reports whether the source resource id is worth publishing
// as a source hint (design §4.1: only for unlabelled interactive units or
// unknown drawn views; never as identity, never as an action selector).
func needsResourceHint(raw *device.RawNode, noLabel bool) bool {
	if !noLabel {
		return false
	}
	return raw.HasClickable && raw.Clickable
}

// displayBounds returns the source rectangle clipped to the display. A visible
// node must have positive area; otherwise it has no usable geometry.
func displayBounds(cap *device.Capture, raw *device.RawNode) *protocol.Bounds {
	clipped := raw.Bounds.Intersect(cap.Display.Bounds())
	if clipped.Empty() {
		return nil
	}
	return &protocol.Bounds{
		Left: clipped.Left, Top: clipped.Top,
		Right: clipped.Right, Bottom: clipped.Bottom,
	}
}

// actionsOf builds the action list in the fixed order tap, long_press, text,
// swipe, from source capabilities only.
func actionsOf(raw *device.RawNode) []protocol.Action {
	actions := []protocol.Action{}
	if raw.HasClickable && raw.Clickable {
		actions = append(actions, protocol.ActionTap)
	}
	if raw.HasLongClick && raw.LongClickable {
		actions = append(actions, protocol.ActionLongPress)
	}
	if raw.Editable {
		actions = append(actions, protocol.ActionText)
	}
	if raw.HasScrollable && raw.Scrollable {
		actions = append(actions, protocol.ActionSwipe)
	}
	return actions
}

// newRootNode builds the synthetic reading root for one display. The root is
// a reading unit: known enabled=true, focused/selected/protected=false, no
// actions and the display rectangle as bounds.
func newRootNode(display device.Display, ref string) *protocol.Node {
	yes, no := true, false
	b := protocol.Bounds{Left: 0, Top: 0, Right: display.Width, Bottom: display.Height}
	return &protocol.Node{
		Ref:       ref,
		Role:      protocol.RoleRoot,
		Bounds:    &b,
		Label:     []string{},
		Actions:   []protocol.Action{},
		Enabled:   &yes,
		Focused:   &no,
		Selected:  &no,
		Protected: &no,
		Children:  []*protocol.Node{},
	}
}

// triBool returns nil when the source did not provide the attribute and a
// pointer to the reported value otherwise.
func triBool(known, value bool) *bool {
	if !known {
		return nil
	}
	v := value
	return &v
}

// collectRefs appends every ref in preorder.
func collectRefs(n *protocol.Node, out *[]string) {
	if n == nil {
		return
	}
	*out = append(*out, n.Ref)
	for _, child := range n.Children {
		collectRefs(child, out)
	}
}

// formatRef formats an allocated ref number.
func formatRef(n int) string { return "n" + strconv.Itoa(n) }
