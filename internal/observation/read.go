package observation

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The read representation is a pure projection of the complete internal tree.
// It never mutates the internal tree, the capture, the source mapping or a
// previously published object, and it grants no input or session authority.

// AnonymousPlain reports whether one read node satisfies the fixed
// anonymous-plain predicate: an ordinary view/group with no action, no readable
// content and only known default state. The compact geometry policy hides its
// known rectangle and uses this predicate when pruning empty containers and
// flattening their children into the parent.
func AnonymousPlain(n *protocol.ReadNode) bool {
	if n == nil || n.Context {
		return false
	}
	if n.Role != protocol.RoleView && n.Role != protocol.RoleGroup {
		return false
	}
	return compactNoReadableContent(n) && compactKnownDefaultState(n)
}

func compactNoReadableContent(n *protocol.ReadNode) bool {
	if n.Truncated {
		return false
	}
	if len(n.Label) != 0 || len(n.Actions) != 0 {
		return false
	}
	if n.Value != nil || n.Hint != nil || n.Resource != nil || n.Axis != "" {
		return false
	}
	return true
}

func compactKnownDefaultState(n *protocol.ReadNode) bool {
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

// ProjectCompactTree builds the compact public reading tree from the complete
// internal tree.
//
// One recursive traversal decides geometry before descending and then reduces
// bottom-up. On the way down it removes viewport-edge sliver views, de-duplicates
// an input label that repeats its value, truncates long actionable labels on
// non-input nodes, and decides geometry. The root and scroll containers keep
// their rectangles. Other delivered nodes keep the center point of their visible
// rectangle. On the way back it prunes decorative image leaves and passive text
// leaves covered by delivered ancestor labels, removes anonymous containers left
// empty by pruning, and flattens hidden anonymous wrappers. It then hoists
// passive text children of an unlabeled tappable view into a button and shortens
// remaining resource IDs. A missing or degenerate root rectangle skips the rules
// that need a screen rectangle.
func ProjectCompactTree(t *protocol.Tree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	screen := newCompactProjection(t.Root)
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
		Root: projectCompactNodes(t.Root, screen, nil, nil)[0],
	}
}

// ProjectDetailedTree builds the detailed public reading tree. Every logical
// node keeps its known geometry. The fixed inspect view applies no compact
// pruning and emits no chain definitions.
func ProjectDetailedTree(t *protocol.Tree) *protocol.ReadTree {
	if t == nil || t.Root == nil {
		return nil
	}
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed,
		Root: projectDetailedNode(t.Root),
	}
}

// ProjectDetailedNode builds the detailed definition of one internal subtree.
func ProjectDetailedNode(n *protocol.Node) *protocol.ReadNode {
	return projectDetailedNode(n)
}

// compactProjection carries the fixed screen facts of one compact projection.
// screenKnown is false when the synthetic root rectangle is missing or
// degenerate, which skips the area rule, the visible-sliver rule and the clipping
// of delivered center points.
type compactProjection struct {
	screen      protocol.Bounds
	screenKnown bool
	screenArea  int64
}

func newCompactProjection(root *protocol.Node) compactProjection {
	if root == nil || root.Bounds == nil {
		return compactProjection{}
	}
	area, ok := boundsArea(*root.Bounds)
	if !ok {
		return compactProjection{}
	}
	return compactProjection{screen: *root.Bounds, screenKnown: true, screenArea: area}
}

// projectDetailedNode copies one internal node and every known rectangle.
func projectDetailedNode(n *protocol.Node) *protocol.ReadNode {
	if n == nil {
		return nil
	}
	out := copyReadFields(n)
	if n.Bounds == nil {
		out.Bounds = protocol.UnknownBounds()
	} else {
		out.Bounds = protocol.KnownBounds(*n.Bounds)
	}
	for _, child := range n.Children {
		if child == nil {
			continue
		}
		out.Children = append(out.Children, projectDetailedNode(child))
	}
	return out
}

// compactSliverMaxEdge is the largest short edge of a viewport-edge sliver that
// the compact view removes. A view whose visible rectangle is at most this many
// pixels wide or high cannot be read or aimed at and only adds a row.
const compactSliverMaxEdge = 4

// compactActionableLabelMaxRunes limits each label part on an actionable source
// node in compact view, excluding inputs and text hoisted during projection.
const compactActionableLabelMaxRunes = 250

// projectCompactNodes returns the compact definitions contributed by one source
// node. A flattened wrapper contributes its projected children in source order.
func projectCompactNodes(n *protocol.Node, screen compactProjection,
	ancestors [][]string, reference *protocol.Bounds) []*protocol.ReadNode {
	if n == nil {
		return nil
	}
	originalHasChildren := hasNonNilChild(n)
	out := copyReadFields(n)

	if !originalHasChildren && compactSliverCanPrune(n, out, screen) {
		return nil
	}
	if out.Role == protocol.RoleInput && out.Value != nil &&
		len(out.Label) == 1 && out.Label[0] == *out.Value {
		out.Label = []string{}
	}
	if !out.Context && out.Role != protocol.RoleInput && len(out.Actions) > 0 && len(out.Label) > 0 {
		for i, part := range out.Label {
			if utf8.RuneCountInString(part) <= compactActionableLabelMaxRunes {
				continue
			}
			end := 0
			for range compactActionableLabelMaxRunes {
				_, size := utf8.DecodeRuneInString(part[end:])
				end += size
			}
			out.Label[i] = part[:end]
			out.Truncated = true
		}
	}

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

	childAncestors := ancestors
	if originalHasChildren && len(out.Label) > 0 {
		childAncestors = appendCompactAncestorLabel(ancestors, out.Label)
	}
	for _, child := range n.Children {
		out.Children = append(out.Children, projectCompactNodes(child, screen, childAncestors, reference)...)
	}

	if !originalHasChildren && compactImageLeafCanPrune(out) {
		return nil
	}
	if !originalHasChildren && isPlainPassiveTextLeaf(out) &&
		compactLabelCovered(out.Label, ancestors) {
		return nil
	}
	if originalHasChildren && len(out.Children) == 0 &&
		out.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(out) {
		return nil
	}
	if out.Bounds.Kind == protocol.GeometryHidden && AnonymousPlain(out) && len(out.Children) > 0 {
		return out.Children
	}
	if compactHoistsTextChildren(out) {
		hoisted := make([]string, 0, len(out.Children))
		for _, child := range out.Children {
			for _, part := range child.Label {
				if part != "" {
					hoisted = append(hoisted, part)
				}
			}
		}
		out.Role = protocol.RoleButton
		out.Label = hoisted
		out.Children = []*protocol.ReadNode{}
		out.Resource = nil
	}
	if out.Resource != nil && *out.Resource != "" {
		short := compactResourceTail(*out.Resource)
		out.Resource = &short
	}
	return []*protocol.ReadNode{out}
}

// hasNonNilChild reports whether the original logical node has at least one
// child definition. An empty child list is the ordinary leaf shape.
func hasNonNilChild(n *protocol.Node) bool {
	if n == nil {
		return false
	}
	for _, child := range n.Children {
		if child != nil {
			return true
		}
	}
	return false
}

// compactSliverCanPrune applies the viewport-edge sliver predicate. It only
// removes an original leaf view without readable content, without a text or
// swipe action and in the known default state when at least one visible edge is
// at most compactSliverMaxEdge pixels. Explicit control roles are out of scope.
func compactSliverCanPrune(n *protocol.Node, out *protocol.ReadNode, screen compactProjection) bool {
	if n.Role != protocol.RoleView || out.Context {
		return false
	}
	if len(out.Label) != 0 || out.Value != nil || out.Hint != nil || out.Axis != "" {
		return false
	}
	for _, action := range out.Actions {
		if action == protocol.ActionText || action == protocol.ActionSwipe {
			return false
		}
	}
	if !compactKnownDefaultState(out) || n.Bounds == nil {
		return false
	}
	if !screen.screenKnown {
		return minInt(n.Bounds.Width(), n.Bounds.Height()) <= compactSliverMaxEdge
	}
	clipped, positive := compactRect(*n.Bounds, screen)
	if !positive {
		return true
	}
	return minInt(clipped.Width(), clipped.Height()) <= compactSliverMaxEdge
}

// compactImageLeafCanPrune applies the fixed unlabeled-image leaf predicate: an
// image leaf without readable content, actions or non-default state carries no
// evidence a reader can use. Neither its area nor a known rectangle is required,
// because the reader can neither see nor act on it.
func compactImageLeafCanPrune(n *protocol.ReadNode) bool {
	if n == nil || n.Context || n.Role != protocol.RoleImage {
		return false
	}
	return compactNoReadableContent(n) && compactKnownDefaultState(n)
}

// isPlainPassiveTextLeaf reports whether one compact node is a passive text leaf
// whose whole readable content is its own label: ordinary text, no children, no
// action, no value, hint, resource or axis, hidden geometry and the known default
// state.
func isPlainPassiveTextLeaf(n *protocol.ReadNode) bool {
	if n == nil || n.Context || n.IsChain() {
		return false
	}
	if n.Role != protocol.RoleText || len(n.Children) != 0 || len(n.Label) == 0 {
		return false
	}
	if n.Bounds.Kind != protocol.GeometryHidden || len(n.Actions) != 0 {
		return false
	}
	if n.Value != nil || n.Hint != nil || n.Resource != nil || n.Axis != "" {
		return false
	}
	return compactKnownDefaultState(n)
}

// compactHoistsTextChildren reports whether one compact view is an unlabeled
// tappable shell whose every direct child is a plain passive text leaf. Hoisting
// folds that two-row shell into one readable button.
func compactHoistsTextChildren(n *protocol.ReadNode) bool {
	if n == nil || n.Context || n.Role != protocol.RoleView {
		return false
	}
	if len(n.Label) != 0 || n.Value != nil || n.Hint != nil || n.Axis != "" {
		return false
	}
	if !hasReadAction(n.Actions, protocol.ActionTap) || len(n.Children) < 1 {
		return false
	}
	for _, child := range n.Children {
		if !isPlainPassiveTextLeaf(child) {
			return false
		}
	}
	return true
}

// hasReadAction reports whether one action list carries the requested action.
func hasReadAction(actions []protocol.Action, action protocol.Action) bool {
	for _, candidate := range actions {
		if candidate == action {
			return true
		}
	}
	return false
}

// compactResourceTail keeps the last colon- or slash-separated segment that
// carries at least one non-digit character, so a trailing numeric index never
// becomes the whole hint. When every segment is numeric the last segment is
// kept.
func compactResourceTail(resource string) string {
	segments := strings.FieldsFunc(resource, func(r rune) bool {
		return r == ':' || r == '/'
	})
	for i := len(segments) - 1; i >= 0; i-- {
		if !allASCIIDigits(segments[i]) {
			return segments[i]
		}
	}
	if len(segments) == 0 {
		return ""
	}
	return segments[len(segments)-1]
}

// allASCIIDigits reports whether every byte of s is an ASCII decimal digit. The
// empty string has no non-digit character and is treated as numeric.
func allASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// compactDeliveredBounds builds the delivered geometry of one node. The root and
// every scroll container keep their four-sided rectangle; every other node keeps
// a binary center point of its visible rectangle, which is the nearest visible
// part of the original rectangle when the screen is known.
func compactDeliveredBounds(role protocol.Role, rect protocol.Bounds,
	screen compactProjection) protocol.ReadBounds {
	if role == protocol.RoleRoot || role == protocol.RoleScroll {
		return protocol.KnownBounds(rect)
	}
	point := rect
	if screen.screenKnown {
		if clipped, positive := compactRect(rect, screen); positive {
			point = clipped
		}
	}
	x, y := compactCenter(point)
	return protocol.PointBounds(x, y)
}

// compactCenter returns the truncated half-open center of one rectangle. The
// mixed-sign branch and the final clamp keep the point inside the rectangle for
// every representable edge pair.
func compactCenter(rect protocol.Bounds) (int, int) {
	x := compactMidpoint(rect.Left, rect.Right)
	y := compactMidpoint(rect.Top, rect.Bottom)
	if rect.Right > rect.Left {
		x = clampInt(x, rect.Left, rect.Right-1)
	}
	if rect.Bottom > rect.Top {
		y = clampInt(y, rect.Top, rect.Bottom-1)
	}
	return x, y
}

// compactMidpoint returns the truncated integer midpoint of two edges without
// overflowing for same-sign extremes; it mirrors the tap planner's safe average
// so a published coordinate and a planned tap agree.
func compactMidpoint(a, b int) int {
	if (a < 0) != (b < 0) {
		return (a + b) / 2
	}
	return a/2 + b/2 + (a%2+b%2)/2
}

// clampInt limits v to the inclusive range [low, high].
func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

// appendCompactAncestorLabel appends one retained ancestor label to the
// downward label stack. The returned slice never aliases the caller's backing
// array, so sibling subtrees cannot observe each other's ancestors.
func appendCompactAncestorLabel(ancestors [][]string, label []string) [][]string {
	next := make([][]string, len(ancestors)+1)
	copy(next, ancestors)
	next[len(ancestors)] = label
	return next
}

// normalizeReadableText collapses every whitespace run of one readable part to a
// single space and trims the edges. It is the comparison form of a label part.
func normalizeReadableText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// compactAncestorText returns the comparison form of one ancestor label: its
// non-empty normalized parts joined by one space.
func compactAncestorText(parts []string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if text := normalizeReadableText(part); text != "" {
			normalized = append(normalized, text)
		}
	}
	return strings.Join(normalized, " ")
}

// compactLabelCovered reports whether a composite ancestor label already carries
// one compact label. An ancestor whose label slice equals the whole label
// covers it outright; otherwise every part must occur as a whole word or number
// inside one ancestor's normalized composite text.
func compactLabelCovered(parts []string, ancestors [][]string) bool {
	if len(parts) == 0 {
		return false
	}
	for _, ancestor := range ancestors {
		if equalStrings(ancestor, parts) {
			return true
		}
	}
	for _, part := range parts {
		if !compactPartCovered(part, ancestors) {
			return false
		}
	}
	return true
}

// compactPartCovered reports whether one label part occurs as a whole word or
// number inside one ancestor's normalized composite text. A part that normalizes
// to nothing is never covered.
func compactPartCovered(part string, ancestors [][]string) bool {
	normalized := normalizeReadableText(part)
	if normalized == "" {
		return false
	}
	for _, ancestor := range ancestors {
		if compactAncestorContainsWord(compactAncestorText(ancestor), normalized) {
			return true
		}
	}
	return false
}

// compactAncestorContainsWord reports whether needle occurs in text at a position
// whose neighbours do not continue an ASCII word or a composite number. A pure
// non-ASCII phrase only has to occur literally; an ASCII edge additionally
// rejects a neighbouring word byte and, for a digit edge, a neighbouring
// digit-connector-digit run such as "16:48" or "1.5".
func compactAncestorContainsWord(text, needle string) bool {
	if text == "" || needle == "" || len(needle) > len(text) {
		return false
	}
	for start := 0; start+len(needle) <= len(text); start++ {
		if !strings.HasPrefix(text[start:], needle) {
			continue
		}
		if compactWordBoundaryOK(text, start, start+len(needle), needle) {
			return true
		}
	}
	return false
}

// compactWordBoundaryOK applies the word and composite-number boundary guards of
// one candidate substring match. Bytes at or above 0x80 are never ASCII word
// bytes, so a Chinese phrase needs no neighbour check.
func compactWordBoundaryOK(text string, i, j int, needle string) bool {
	first, last := needle[0], needle[len(needle)-1]
	if asciiWordByte(first) || (len(needle) == 1 && first < 0x80) {
		if i > 0 && asciiWordByte(text[i-1]) {
			return false
		}
		if asciiDigitByte(first) && i >= 2 &&
			asciiDigitByte(text[i-2]) && asciiNumberConnector(text[i-1]) {
			return false
		}
	}
	if asciiWordByte(last) || (len(needle) == 1 && last < 0x80) {
		if j < len(text) && asciiWordByte(text[j]) {
			return false
		}
		if asciiDigitByte(last) && j+1 < len(text) &&
			asciiNumberConnector(text[j]) && asciiDigitByte(text[j+1]) {
			return false
		}
	}
	return true
}

// asciiWordByte reports whether b is an ASCII letter or decimal digit.
func asciiWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// asciiDigitByte reports whether b is an ASCII decimal digit.
func asciiDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// asciiNumberConnector reports whether b separates the parts of one composite
// number such as "16:48", "1.5", "3/4" or "2024-01".
func asciiNumberConnector(b byte) bool {
	return b == ':' || b == '.' || b == '/' || b == '-'
}

// compactHidesBounds applies the non-action part of the compact geometry
// policy. The caller has already delivered the root and every node with an
// action.
func compactHidesBounds(n *protocol.Node, out *protocol.ReadNode,
	screen compactProjection, reference *protocol.Bounds) bool {
	if AnonymousPlain(out) || n.Role == protocol.RoleText {
		return true
	}
	rect, positive := compactRect(*n.Bounds, screen)
	if reference != nil && rect == *reference {
		return true
	}
	if !screen.screenKnown {
		return false
	}
	if !positive {
		return true
	}
	return belowPercent(rectArea(rect), screen.screenArea)
}

// deliveredRect is the rectangle a delivered node passes to its descendants:
// the visible part on the target screen when the screen is known.
func deliveredRect(rect protocol.Bounds, screen compactProjection) *protocol.Bounds {
	clipped, _ := compactRect(rect, screen)
	return &clipped
}

// compactRect intersects one internal rectangle with the target screen. The
// positive result reports whether the visible part has area.
func compactRect(rect protocol.Bounds, screen compactProjection) (protocol.Bounds, bool) {
	if !screen.screenKnown {
		return rect, rect.Right > rect.Left && rect.Bottom > rect.Top
	}
	clipped := protocol.Bounds{
		Left:   maxInt(rect.Left, screen.screen.Left),
		Top:    maxInt(rect.Top, screen.screen.Top),
		Right:  minInt(rect.Right, screen.screen.Right),
		Bottom: minInt(rect.Bottom, screen.screen.Bottom),
	}
	return clipped, clipped.Right > clipped.Left && clipped.Bottom > clipped.Top
}

// belowPercent reports whether area is strictly smaller than one percent of
// screenArea. The comparison is exact for every screen area and avoids an
// overflowing multiplication.
func belowPercent(area, screenArea int64) bool {
	if area <= 0 || screenArea <= 0 {
		return area <= 0 && screenArea > 0
	}
	threshold := screenArea / 100
	if area > threshold {
		return false
	}
	return area*100 < screenArea
}

// boundsArea returns the positive integer area of one rectangle. It reports
// false for an empty rectangle or when the product cannot be represented.
func boundsArea(rect protocol.Bounds) (int64, bool) {
	width := int64(rect.Right) - int64(rect.Left)
	height := int64(rect.Bottom) - int64(rect.Top)
	if width <= 0 || height <= 0 || width > math.MaxInt64/height {
		return 0, false
	}
	return width * height, true
}

func rectArea(rect protocol.Bounds) int64 {
	area, _ := boundsArea(rect)
	return area
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// copyReadFields copies every public field of one internal node except its
// children; each projection adds the geometry state and the children.
func copyReadFields(n *protocol.Node) *protocol.ReadNode {
	return &protocol.ReadNode{
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

// EqualCompactContent compares the logical contents of two read trees while
// ignoring refs. Ordinary trees are compared directly without allocating.
func EqualCompactContent(a, b *protocol.ReadTree) bool {
	if a == nil || b == nil || a.Root == nil || b.Root == nil || a.View != b.View {
		return false
	}
	if readTreeHasChain(a.Root) || readTreeHasChain(b.Root) {
		a, b = UnpackTree(a), UnpackTree(b)
	}
	return equalReadNodes(a.Root, b.Root, false)
}

func readTreeHasChain(n *protocol.ReadNode) bool {
	if n == nil {
		return false
	}
	if len(n.Chain) > 0 {
		return true
	}
	for _, child := range n.Children {
		if readTreeHasChain(child) {
			return true
		}
	}
	return false
}

func equalReadNode(a, b *protocol.ReadNode) bool {
	return equalReadNodes(a, b, true)
}

func equalReadNodes(a, b *protocol.ReadNode, compareRefs bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	if (compareRefs && a.Ref != b.Ref) || a.Role != b.Role || a.Context != b.Context || a.Truncated != b.Truncated {
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
		if !equalReadNodes(a.Children[i], b.Children[i], compareRefs) {
			return false
		}
	}
	return true
}

// EqualPublicTreeContent compares the logical contents of two complete public
// trees while ignoring refs and tree format metadata.
func EqualPublicTreeContent(a, b *protocol.Tree) bool {
	return a != nil && b != nil && a.Root != nil && b.Root != nil && equalPublicNodes(a.Root, b.Root)
}

func equalPublicNodes(a, b *protocol.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Role != b.Role || a.Context != b.Context || !equalBounds(a.Bounds, b.Bounds) {
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
		if !equalPublicNodes(a.Children[i], b.Children[i]) {
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
