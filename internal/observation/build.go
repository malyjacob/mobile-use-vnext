package observation

import (
	"errors"
	"regexp"
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
	// ContextSource maps a context ref to its raw index. Context nodes are not
	// actionable, so they stay out of Source; continuity still needs their raw
	// evidence to keep the reading ancestry stable.
	ContextSource map[string]int
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
	prev           *Snapshot
	nextRef        int
	hasBudgetLimit bool
	budgetLimit    uint64
	lastStats      matchStats
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
	b.lastStats = matchStats{}
}

// Build projects one capture to a complete public tree and publishes it as the
// new continuity state. It is pure with respect to the previous snapshot: no
// published object is mutated.
//
// A capture without a usable root or with an empty display is rejected with
// an error; nothing is published and the previous snapshot is left untouched.
func (b *Builder) Build(cap *device.Capture) (*Snapshot, error) {
	snapshot, commit, err := b.Candidate(cap)
	if err != nil {
		return nil, err
	}
	commit()
	return snapshot, nil
}

// Candidate projects one capture without advancing continuity. The returned
// commit function publishes exactly this candidate as the new continuity state
// (previous snapshot, ref counter and match statistics); a discarded candidate
// leaves the builder exactly as it was, so a rejected acquisition can never
// shift the refs of a later published tree. It is the ordinary-baseline case of
// CandidateAgainst and shares its pure projection path.
func (b *Builder) Candidate(cap *device.Capture) (*Snapshot, func(), error) {
	return b.CandidateAgainst(b.prev, cap)
}

// CandidateAgainst projects one capture against an explicit previous snapshot
// instead of the builder's current continuity state. It is a pure read-only
// probe: the builder's previous snapshot, ref high-water mark and match
// statistics are untouched until the returned commit function runs. A
// multi-baseline delta probe can therefore try a historical page-return
// baseline and discard the candidate without polluting the ref numbering of the
// observation that is eventually published.
//
// Fresh refs are allocated above the builder's current high-water mark, so a
// committed candidate never reuses a number another observation may already
// hold.
func (b *Builder) CandidateAgainst(prev *Snapshot, cap *device.Capture) (*Snapshot, func(), error) {
	snapshot, nextRef, stats, err := b.buildAgainst(prev, cap, b.nextRef)
	if err != nil {
		return nil, nil, err
	}
	commit := func() {
		b.prev, b.nextRef, b.lastStats = snapshot, nextRef, stats
	}
	return snapshot, commit, nil
}

// buildAgainst is the projection itself: it projects cap against an explicit
// previous snapshot and returns the candidate snapshot, its next ref
// high-water mark and its match statistics without mutating the builder. Only
// Build and the commit returned by Candidate/CandidateAgainst may keep that
// state.
func (b *Builder) buildAgainst(prev *Snapshot, cap *device.Capture, nextRef int) (*Snapshot, int, matchStats, error) {
	if cap == nil {
		return nil, 0, matchStats{}, invalidTree("capture is missing")
	}
	if cap.Display.Width <= 0 || cap.Display.Height <= 0 {
		return nil, 0, matchStats{}, invalidTree("display rectangle is empty")
	}
	if cap.Root < 0 || cap.Root >= len(cap.Nodes) {
		return nil, 0, matchStats{}, invalidTree("capture root is missing")
	}
	if cap.Nodes[cap.Root].Parent != -1 {
		return nil, 0, matchStats{}, invalidTree("capture root has a parent")
	}

	units := project(cap)

	// Refs are computed against the previous snapshot, per raw node, so newly
	// published units get monotonic numbers in preorder.
	refs := make([]string, len(cap.Nodes))
	nextRef, stats := b.matchRefs(prev, cap, units, refs, nextRef)

	// Build in source preorder and attach every published unit to its nearest
	// published ancestor so descendants across unpublished wrappers remain
	// reachable in source reading order.
	public := make([]*protocol.Node, len(cap.Nodes))
	root := newRootNode(cap.Display, refs[cap.Root])
	public[cap.Root] = root

	order := preorder(cap)
	for _, i := range order {
		if i == cap.Root || !units[i].published {
			continue
		}
		public[i] = newPublicNode(cap, i, units[i], refs[i])
	}
	for _, i := range order {
		if i == cap.Root || !units[i].published {
			continue
		}
		parent := nearestPublishedParent(cap, units, i)
		if parent < 0 || parent >= len(public) || public[parent] == nil {
			parent = cap.Root
		}
		public[parent].Children = append(public[parent].Children, public[i])
	}

	// The parent/child links are complete now, so every scroll container sees
	// its published children. Axis evidence is projected before the snapshot is
	// handed out because published nodes are immutable.
	populateScrollAxes(cap, public, order)

	snap := &Snapshot{
		Capture:       cap,
		Source:        make(map[string]int),
		ContextSource: make(map[string]int),
	}
	snap.Source[root.Ref] = cap.Root
	for _, i := range order {
		if i == cap.Root || public[i] == nil {
			continue
		}
		if units[i].context {
			snap.ContextSource[public[i].Ref] = i
			continue
		}
		snap.Source[public[i].Ref] = i
	}
	snap.Tree = &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
	collectRefs(root, &snap.Refs)

	return snap, nextRef, stats, nil
}

// Preview projects one capture without advancing continuity state. The settle
// loop needs the public tree of a candidate frame, but only the published frame
// may keep the ref numbering of the next observation: advancing it on every
// sample would shift the refs of already published observations.
func (b *Builder) Preview(cap *device.Capture) (*Snapshot, error) {
	prev, nextRef, stats := b.prev, b.nextRef, b.lastStats
	defer func() { b.prev, b.nextRef, b.lastStats = prev, nextRef, stats }()
	return b.Build(cap)
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
			// A node of another display is never published itself, yet it still
			// propagates whether any kept target-display descendant lives inside
			// its subtree so target-display ancestors become context or visible
			// containers.
			inTree[i] = childInTree
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

// hasSemantics reports whether a visible leaf carries readable content,
// interaction capability, non-default state, or an intrinsic leaf role. Text
// semantics are decided on the cleaned public label, which treats whitespace
// and invisible placeholders alone as empty.
func hasSemantics(raw *device.RawNode) bool {
	if len(labelParts(raw)) > 0 {
		return true
	}
	if (raw.HasClickable && raw.Clickable) ||
		(raw.HasLongClick && raw.LongClickable) ||
		(raw.HasScrollable && raw.Scrollable) ||
		raw.Editable ||
		(raw.HasCheckable && raw.Checkable) {
		return true
	}
	if (raw.HasEnabled && !raw.Enabled) ||
		(raw.HasChecked && raw.Checked) ||
		(raw.HasFocused && raw.Focused) ||
		(raw.HasSelected && raw.Selected) ||
		(raw.HasPassword && raw.Password) {
		return true
	}
	return retainsIntrinsicLeafRole(classifyRole(raw))
}

func retainsIntrinsicLeafRole(role protocol.Role) bool {
	switch role {
	case protocol.RoleImage,
		protocol.RoleInput,
		protocol.RoleButton,
		protocol.RoleCheckbox,
		protocol.RoleRadio,
		protocol.RoleSwitch,
		protocol.RoleToggle,
		protocol.RoleScroll,
		protocol.RoleText:
		return true
	}
	return false
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
// own text, then own description when it differs. The whole cleaned string is
// kept; text is never split into business fields and the raw capture is never
// modified.
func labelParts(raw *device.RawNode) []string {
	if raw.HasPassword && raw.Password {
		// A protected input's text is never published.
		return nil
	}
	var label []string
	text := ""
	if raw.TextSet {
		text = cleanReadableText(raw.Text)
	}
	if text != "" {
		label = append(label, text)
	}
	if raw.DescSet {
		desc := cleanReadableText(raw.Description)
		if desc != "" && desc != text {
			label = append(label, desc)
		}
	}
	return label
}

// readableInvisibleRunes is the closed set of code points that carry no
// readable meaning and are removed from public readable labels: the zero-width
// space, zero-width non-joiner, zero-width joiner, word joiner, byte-order mark
// and object replacement character.
const readableInvisibleRunes = "\u200b\u200c\u200d\u2060\ufeff\ufffc"

// readableInvisibleReplacer removes exactly the closed set above byte for byte,
// so every other code point and every invalid byte of a reported string stays
// unchanged.
var readableInvisibleReplacer = strings.NewReplacer(
	"\u200b", "", "\u200c", "", "\u200d", "", "\u2060", "", "\ufeff", "", "\ufffc", "",
)

// readableBlankLineRun matches one line break followed by one or more further
// lines that hold nothing but horizontal whitespace and a line break: the residue
// of empty lines that only survives label purification as whitespace. The repeated
// group folds the whole run in one pass, including mixed residues such as
// "\n \n\t\n".
var readableBlankLineRun = regexp.MustCompile("\n(?:[ \t]*\n)+")

// cleanReadableText trims the edges of a public readable string. If it
// contains invisible placeholders, remove them and collapse runs of blank lines
// before trimming. The raw capture and input values keep their original bytes.
func cleanReadableText(s string) string {
	if strings.ContainsAny(s, readableInvisibleRunes) {
		s = readableInvisibleReplacer.Replace(s)
		s = readableBlankLineRun.ReplaceAllString(s, "\n")
	}
	return strings.TrimSpace(s)
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
	// No capture field maps to the public Hint today: a uiAutomator node reports
	// text, a content description and a resource id, not a hint string. The wire
	// member exists for readers and for a future producer; any such producer must
	// pass its text through cleanReadableText like labelParts does, and needs its
	// own purification test at that point.
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
// a reading unit: known enabled=true, focused/selected/protected/checked=false,
// no actions and the display rectangle as bounds.
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
		Checked:   &no,
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

// Scroll axis projection bounds. The child-layout evidence is deliberately
// small and bounded: at most scrollAxisMaxSamples leading children are sampled,
// the main axis must span at least scrollAxisMinSpan pixels and dominate the
// cross axis by scrollAxisDominance, and at least half of the adjacent pairs
// must overlap on the cross axis.
const (
	scrollAxisMinItems   = 2
	scrollAxisMaxSamples = 16
	scrollAxisMinSpan    = 24
	scrollAxisDominance  = 2
)

// populateScrollAxes projects the scroll axis of every public scroll container
// in preorder.
//
// The projection is deterministic: it uses only the source class name and the
// layout of the container's published children, so the same capture always
// yields the same axis and no extra device read is involved. Child layout
// evidence wins over the class name; when neither proves a single axis the
// field stays empty (unknown) and the tree omits it.
func populateScrollAxes(cap *device.Capture, public []*protocol.Node, order []int) {
	if cap == nil {
		return
	}
	for _, rawIdx := range order {
		if rawIdx < 0 || rawIdx >= len(cap.Nodes) || rawIdx >= len(public) {
			continue
		}
		node := public[rawIdx]
		if node == nil || node.Context || node.Role != protocol.RoleScroll || node.Bounds == nil {
			continue
		}
		if axis := inferScrollAxisFromChildren(node); axis != "" {
			node.Axis = axis
			continue
		}
		node.Axis = inferScrollAxisFromClass(cap.Nodes[rawIdx].ClassName)
	}
}

// inferScrollAxisFromClass maps native scroll container class names to their
// fixed scroll axis. The order matters: HorizontalScrollView must be recognized
// before the generic ScrollView rule, and a ViewPager defaults to horizontal
// paging unless its class name marks a vertical variant.
func inferScrollAxisFromClass(class string) protocol.Axis {
	switch {
	case strings.Contains(class, "HorizontalScrollView"):
		return protocol.AxisX
	case strings.Contains(class, "NestedScrollView"),
		strings.Contains(class, "ListView"),
		strings.Contains(class, "AbsListView"):
		return protocol.AxisY
	case strings.Contains(class, "ScrollView") && !strings.Contains(class, "Horizontal"):
		return protocol.AxisY
	case strings.Contains(class, "ViewPager"):
		if strings.Contains(class, "Vertical") {
			return protocol.AxisY
		}
		return protocol.AxisX
	}
	return ""
}

// inferScrollAxisFromChildren infers the scroll axis from how the published
// children are laid out. A single non-scroll child is treated as a content
// wrapper and its own children are sampled instead, which covers the common
// "scroll container > one root content group > items" structure. Fewer than two
// samples, a layout that is not dominated by one axis (a grid or a
// two-dimensional canvas) and inconsistent cross-axis overlaps leave the axis
// unknown.
func inferScrollAxisFromChildren(node *protocol.Node) protocol.Axis {
	if node == nil {
		return ""
	}
	items := scrollAxisSamples(node.Children)
	if len(items) == 1 && items[0].Role != protocol.RoleScroll {
		items = scrollAxisSamples(items[0].Children)
	}
	if len(items) < scrollAxisMinItems {
		return ""
	}
	if len(items) > scrollAxisMaxSamples {
		items = items[:scrollAxisMaxSamples]
	}

	prev := items[0].Bounds
	prevX, prevY := boundsCenterX(prev), boundsCenterY(prev)
	minX, maxX, minY, maxY := prevX, prevX, prevY, prevY
	var sumDX, sumDY int64
	overlapX, overlapY := 0, 0
	for _, item := range items[1:] {
		cur := item.Bounds
		curX, curY := boundsCenterX(cur), boundsCenterY(cur)
		sumDX += absInt64(curX - prevX)
		sumDY += absInt64(curY - prevY)
		minX, maxX = min(minX, curX), max(maxX, curX)
		minY, maxY = min(minY, curY), max(maxY, curY)
		if halfOpenIntersects(int64(prev.Left), int64(prev.Right), int64(cur.Left), int64(cur.Right)) {
			overlapX++
		}
		if halfOpenIntersects(int64(prev.Top), int64(prev.Bottom), int64(cur.Top), int64(cur.Bottom)) {
			overlapY++
		}
		prev, prevX, prevY = cur, curX, curY
	}
	pairs := int64(len(items) - 1)
	spanX, spanY := maxX-minX, maxY-minY

	if spanX >= scrollAxisMinSpan &&
		sumDX >= scrollAxisDominance*sumDY &&
		spanX >= scrollAxisDominance*spanY &&
		int64(overlapY)*2 >= pairs {
		return protocol.AxisX
	}
	if spanY >= scrollAxisMinSpan &&
		sumDY >= scrollAxisDominance*sumDX &&
		spanY >= scrollAxisDominance*spanX &&
		int64(overlapX)*2 >= pairs {
		return protocol.AxisY
	}
	return ""
}

// scrollAxisSamples returns the children that can witness a layout direction:
// public, non-context and with positive-area geometry.
func scrollAxisSamples(children []*protocol.Node) []*protocol.Node {
	items := make([]*protocol.Node, 0, len(children))
	for _, child := range children {
		if child == nil || child.Context || child.Bounds == nil {
			continue
		}
		if child.Bounds.Width() <= 0 || child.Bounds.Height() <= 0 {
			continue
		}
		items = append(items, child)
	}
	return items
}

// boundsCenterX returns the half-open center column of the rectangle.
func boundsCenterX(b *protocol.Bounds) int64 {
	return (int64(b.Left) + int64(b.Right)) / 2
}

// boundsCenterY returns the half-open center row of the rectangle.
func boundsCenterY(b *protocol.Bounds) int64 {
	return (int64(b.Top) + int64(b.Bottom)) / 2
}

// halfOpenIntersects reports whether [aStart, aEnd) and [bStart, bEnd) share
// positive length.
func halfOpenIntersects(aStart, aEnd, bStart, bEnd int64) bool {
	start, end := aStart, aEnd
	if bStart > start {
		start = bStart
	}
	if bEnd < end {
		end = bEnd
	}
	return end > start
}

// absInt64 returns the absolute value of v. Centers are far below the int64
// range, but the sampled sums and ratios are computed in int64 so a hostile
// capture can never overflow them.
func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// formatRef formats an allocated ref number.
func formatRef(n int) string { return "n" + strconv.Itoa(n) }
