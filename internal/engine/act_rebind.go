package engine

import (
	"strings"
	"time"
	"unicode"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type actRebindRequest struct {
	Kind      string
	Reason    string
	TargetRef string
	Obs       *observationState
	OrigView  *tapPublicView
	OrigRaw   *device.RawNode
	OrigPlan  *tapPlan
	Fresh     *device.Capture
	FreshView *tapPublicView
}

type actRebindDecision struct {
	Attempted    bool
	Strategy     string
	RecoveredRef string
	Target       *protocol.Node
	Raw          *device.RawNode
	Frame        *tapFrame
}

const (
	actRebindText      = "text_rebind"
	actRebindContainer = "container_rebind"
)

// planActRebind uses the caller's second revalidation capture. The caller owns
// foreground and work authorization checks before and after this planning step.
func planActRebind(req actRebindRequest) (actRebindDecision, *protocol.APIError) {
	if req.TargetRef == "" || (req.Reason != targetChangedReasonTargetMissing &&
		req.Reason != targetChangedReasonLineageChanged) {
		return actRebindDecision{}, nil
	}
	var decision actRebindDecision
	switch req.Kind {
	case "text":
		decision.Strategy = actRebindText
	case "tap", "long_press":
		if req.OrigPlan == nil || req.OrigPlan.bubbleAncestorRaw != nil ||
			req.OrigPlan.colocatedRaw != nil || req.OrigPlan.delegateRaw != nil {
			return actRebindDecision{}, nil
		}
		decision.Strategy = actRebindContainer
	default:
		return actRebindDecision{}, nil
	}

	decision.Attempted = true
	if req.Obs == nil || req.Obs.Projection == nil || req.OrigRaw == nil ||
		req.OrigView == nil || req.Fresh == nil || req.FreshView == nil {
		return decision, nil
	}
	original := req.Obs.Projection.Capture
	oldView, oldTopology, ok := actRebindValidatedView(req.OrigView, original)
	if !ok {
		return decision, nil
	}
	oldInput, ok := tapInputFromValidatedView(oldView, oldTopology, oldView.byRef[req.TargetRef])
	if !ok || oldInput.targetRaw != req.OrigRaw {
		return decision, nil
	}

	var oldSkeleton map[string]int
	var oldAnchors []string
	if decision.Strategy == actRebindContainer {
		oldAnchors = actRebindStaticTexts(actRebindIdentityTexts(original, oldInput.targetIndex))
		if len(oldAnchors) == 0 {
			return actRebindDecision{}, nil
		}
		oldSkeleton = actRebindSkeleton(actRebindSubtreeReadable(original, oldInput.targetIndex, true))
	}

	freshView, freshTopology, ok := actRebindValidatedView(req.FreshView, req.Fresh)
	if !ok {
		return decision, nil
	}

	var target *protocol.Node
	var reason string
	if decision.Strategy == actRebindText {
		target, reason = actRebindTextTarget(oldInput, freshView, freshTopology)
	} else {
		target, reason = actRebindContainerTarget(oldInput, freshView, freshTopology,
			protocol.Action(req.Kind), oldSkeleton, oldAnchors)
	}
	if reason != "" {
		return decision, tapTargetChangedReason(reason)
	}
	if target == nil {
		return decision, nil
	}

	blocked := func() (actRebindDecision, *protocol.APIError) {
		return decision, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	input, ok := tapInputFromValidatedView(freshView, freshTopology, target)
	if !ok {
		return blocked()
	}
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		return blocked()
	}
	// chooseTapPlan only selects within this direct scope. Its timing field is
	// unused here. The whole-tree proof is required for every rebind.
	plan, apiErr := chooseTapPlan(scope, input, target, protocol.Action(req.Kind), time.Time{})
	if apiErr != nil || !tapFrameTier4OcclusionProven(freshView, target, plan.x, plan.y) {
		return blocked()
	}
	decision.RecoveredRef, decision.Target, decision.Raw = target.Ref, target, input.targetRaw
	decision.Frame = &tapFrame{
		capture: req.Fresh, raw: input.targetRaw, x: plan.x, y: plan.y,
		carrier: plan.carrierRaw, tier: 4, strategy: plan.strategy,
	}
	return decision, nil
}

// The existing validator fills source indexes in place. Build a local view
// when needed so the caller's views and both captures remain untouched.
func actRebindValidatedView(view *tapPublicView, capture *device.Capture) (*tapPublicView, *tapRawTopology, bool) {
	if capture == nil || view.capture != capture {
		return nil, nil, false
	}
	if !view.sourceValidated || view.topology == nil || view.topology.capture != capture {
		local, apiErr := buildTapPublicView(view.tree, capture, view.source)
		if apiErr != nil {
			return nil, nil, false
		}
		view = local
	}
	topology, ok := ensureTapViewTopology(nil, view)
	return view, topology, ok
}

func actRebindTextTarget(old *tapInput, view *tapPublicView, topology *tapRawTopology) (*protocol.Node, string) {
	original, fresh := old.capture, topology.capture
	want := old.targetRaw
	oldParent, freshParent := actRebindParentAnchor(old.view, view, want)
	if freshParent >= 0 {
		candidate, ambiguous := actRebindUniqueTextDescendant(view, freshParent)
		if ambiguous {
			return nil, targetChangedReasonTargetAmbiguous
		}
		if candidate == nil {
			return nil, ""
		}
		bounds := fresh.Nodes[view.info[candidate].rawIndex].Bounds
		if !actRebindHorizontalOverlap(bounds, fresh.Nodes[freshParent].Bounds) ||
			!actRebindHorizontalOverlap(bounds, original.Nodes[oldParent].Bounds) {
			return nil, ""
		}
		return candidate, ""
	}

	if !want.HasPassword || want.Password {
		return nil, ""
	}
	anchors := actRebindStaticTexts(actRebindTextLabels(original, want))
	unique, _ := actRebindUniqueTexts(original, fresh, anchors)
	var candidate *protocol.Node
	for _, node := range view.nodes {
		if !isValidTextPublicTarget(node) {
			continue
		}
		raw := &fresh.Nodes[view.info[node].rawIndex]
		if !freshTextTargetSafe(raw) || !actRebindGeometry(want.Bounds, raw.Bounds) ||
			!actRebindHasAnchor(actRebindTextLabels(fresh, raw), unique) {
			continue
		}
		if candidate != nil {
			return nil, targetChangedReasonTargetAmbiguous
		}
		candidate = node
	}
	return candidate, ""
}

func actRebindParentAnchor(oldView, freshView *tapPublicView, want *device.RawNode) (int, int) {
	original, fresh := oldView.capture, freshView.capture
	index := want.Parent
	for range maxParentAnchorDepth {
		if isRawWindowRoot(original, index) {
			break
		}
		anchor := &original.Nodes[index]
		if textParentAnchorBoundary(anchor) {
			break
		}
		if anchor.Visible && anchor.Package == want.Package && anchor.DisplayID == want.DisplayID &&
			anchor.ResourceID != "" && actRebindUniqueParent(original, anchor) == index {
			if match := actRebindUniqueParent(fresh, anchor); match >= 0 &&
				actRebindPublicParentMatches(oldView, index, freshView, match) &&
				actRebindOnlyOriginalEditor(oldView, index, want) {
				return index, match
			}
		}
		index = anchor.Parent
	}
	return -1, -1
}

// Count original editor identities before capability checks. An old sibling
// can become writable in the fresh frame after the requested editor disappears.
func actRebindOnlyOriginalEditor(view *tapPublicView, parent int, want *device.RawNode) bool {
	found := false
	for index := range view.capture.Nodes {
		if !view.topology.isAncestor(parent, index) {
			continue
		}
		raw := &view.capture.Nodes[index]
		public := view.byRaw[index]
		publicInput := public != nil && !public.Context && public.Role == protocol.RoleInput &&
			public.Bounds != nil && public.Bounds.Width() > 0 && public.Bounds.Height() > 0
		if !raw.Editable && !publicInput {
			continue
		}
		if raw != want {
			return false
		}
		found = true
	}
	return found
}

// The boolean distinguishes multiple eligible fields from an empty subtree.
func actRebindUniqueTextDescendant(view *tapPublicView, parent int) (*protocol.Node, bool) {
	var candidate *protocol.Node
	for _, node := range view.nodes {
		if !isValidTextPublicTarget(node) {
			continue
		}
		index := view.info[node].rawIndex
		if !view.topology.isAncestor(parent, index) || !freshTextTargetSafe(&view.capture.Nodes[index]) {
			continue
		}
		if candidate != nil {
			return nil, true
		}
		candidate = node
	}
	return candidate, false
}

func actRebindPublicParentMatches(oldView *tapPublicView, oldIndex int, freshView *tapPublicView, freshIndex int) bool {
	old, fresh := oldView.byRaw[oldIndex], freshView.byRaw[freshIndex]
	if !actRebindSamePublicParent(old, fresh) {
		return false
	}
	for _, view := range []*tapPublicView{oldView, freshView} {
		count := 0
		for _, node := range view.nodes {
			if actRebindSamePublicParent(old, node) {
				count++
			}
		}
		if count != 1 {
			return false
		}
	}
	return true
}

func actRebindSamePublicParent(a, b *protocol.Node) bool {
	if a == nil || b == nil || a.Context || b.Context || a.Role == protocol.RoleRoot || a.Role != b.Role {
		return false
	}
	var left, right string
	if a.Resource != nil {
		left = *a.Resource
	}
	if b.Resource != nil {
		right = *b.Resource
	}
	return left == right
}

func actRebindUniqueParent(capture *device.Capture, anchor *device.RawNode) int {
	match := -1
	for index := range capture.Nodes {
		node := &capture.Nodes[index]
		if node.Package != anchor.Package || node.ClassName != anchor.ClassName || node.ResourceID != anchor.ResourceID {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = index
	}
	if isRawWindowRoot(capture, match) {
		return -1
	}
	node := &capture.Nodes[match]
	if !node.Visible || node.DisplayID != anchor.DisplayID || textParentAnchorBoundary(node) {
		return -1
	}
	return match
}

func actRebindContainerTarget(old *tapInput, view *tapPublicView, topology *tapRawTopology,
	action protocol.Action, oldSkeleton map[string]int, oldAnchors []string) (*protocol.Node, string) {
	fresh := topology.capture
	unique, repeated := actRebindUniqueTexts(old.capture, fresh, oldAnchors)
	if len(unique) == 0 {
		if repeated {
			// A repeated template anchor is ambiguous even if its geometry
			// would allow us to select just one of the matching controls.
			return nil, targetChangedReasonTargetAmbiguous
		}
		return nil, ""
	}
	oldOwn := actRebindStaticTexts(actRebindOwnTexts(old.targetRaw, false))
	oldBounds := old.targetRaw.Bounds
	screen := fresh.Display.Bounds()
	var candidate *protocol.Node
	for _, node := range view.nodes {
		if !validTapPublicTarget(node, action) {
			continue
		}
		index := view.info[node].rawIndex
		raw := &fresh.Nodes[index]
		if !raw.Visible || raw.Bounds.Intersect(screen) == screen ||
			rectFromBounds(node.Bounds).Intersect(screen) == screen ||
			!actRebindGeometry(oldBounds, raw.Bounds) {
			continue
		}
		own := actRebindStaticTexts(actRebindOwnTexts(raw, false))
		if len(oldOwn) > 0 && len(own) > 0 && !sharesReadableText(oldOwn, own) {
			continue
		}
		if !actRebindHasAnchor(actRebindIdentityTexts(fresh, index), unique) {
			continue
		}
		texts := actRebindSubtreeReadable(fresh, index, true)
		if !actRebindSkeletonOverlap(oldSkeleton, actRebindSkeleton(texts)) {
			continue
		}
		if candidate != nil {
			return nil, targetChangedReasonTargetAmbiguous
		}
		candidate = node
	}
	return candidate, ""
}

func actRebindOwnTexts(node *device.RawNode, includeEditorValues bool) []string {
	var texts []string
	if node.Visible && !node.Password {
		if (includeEditorValues || !node.Editable) && node.TextSet && strings.TrimSpace(node.Text) != "" {
			texts = append(texts, node.Text)
		}
		if node.DescSet && strings.TrimSpace(node.Description) != "" {
			texts = append(texts, node.Description)
		}
	}
	return texts
}

func actRebindTextLabels(capture *device.Capture, node *device.RawNode) []string {
	// Editable Text is the field value. Only its description and passive
	// placeholder children can identify the control for A2.
	texts := actRebindOwnTexts(node, false)
	for _, index := range node.Children {
		child := &capture.Nodes[index]
		if len(child.Children) == 0 && !child.Editable && !child.Clickable &&
			!child.LongClickable && !child.Scrollable && child.Visible &&
			!child.Bounds.Intersect(node.Bounds).Empty() {
			texts = append(texts, actRebindOwnTexts(child, false)...)
		}
	}
	return texts
}

func actRebindIdentityTexts(capture *device.Capture, index int) []string {
	return actRebindSubtreeReadable(capture, index, false)
}

// Collect the same evidence as readableSubtree plus the target's own text.
// The topology is already validated, and the overlap needs the complete
// multiset, including any entries beyond the Tier-4 matcher evidence cap.
func actRebindSubtreeReadable(capture *device.Capture, index int, includeEditorValues bool) []string {
	var texts []string
	stack := []int{index}
	for len(stack) > 0 {
		index := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := &capture.Nodes[index]
		texts = append(texts, actRebindOwnTexts(node, includeEditorValues)...)
		stack = append(stack, node.Children...)
	}
	return texts
}

func actRebindStaticTexts(texts []string) []string {
	var static []string
	for _, text := range texts {
		if !isDynamicNumericToken(text) && strings.IndexFunc(text, unicode.IsDigit) < 0 {
			static = append(static, text)
		}
	}
	return static
}

func actRebindUniqueTexts(original, fresh *device.Capture, anchors []string) (map[string]struct{}, bool) {
	oldCounts := make(map[string]int, len(anchors))
	newCounts := make(map[string]int, len(anchors))
	for _, anchor := range anchors {
		oldCounts[anchor], newCounts[anchor] = 0, 0
	}
	for _, frame := range []struct {
		capture *device.Capture
		counts  map[string]int
	}{{original, oldCounts}, {fresh, newCounts}} {
		for index := range frame.capture.Nodes {
			for _, text := range actRebindOwnTexts(&frame.capture.Nodes[index], true) {
				if count, ok := frame.counts[text]; ok {
					frame.counts[text] = count + 1
				}
			}
		}
	}
	unique := make(map[string]struct{})
	repeated := false
	for anchor, count := range oldCounts {
		freshCount := newCounts[anchor]
		if count == 1 && freshCount == 1 {
			unique[anchor] = struct{}{}
		} else if freshCount > 0 && (count > 1 || freshCount > 1) {
			repeated = true
		}
	}
	return unique, repeated
}

func actRebindHasAnchor(texts []string, unique map[string]struct{}) bool {
	for _, text := range texts {
		if _, ok := unique[text]; ok {
			return true
		}
	}
	return false
}

func actRebindSkeleton(texts []string) map[string]int {
	counts := make(map[string]int, len(texts))
	for _, text := range texts {
		if !isDynamicNumericToken(text) {
			counts[numericSkeleton(text)]++
		}
	}
	return counts
}

func actRebindSkeletonOverlap(left, right map[string]int) bool {
	inter, union := 0, 0
	for text, count := range left {
		inter += min(count, right[text])
		union += count
	}
	for _, count := range right {
		union += count
	}
	union -= inter
	return union > 0 && inter*3 >= union*2
}

// Horizontal coverage is relative to the smaller span, so a field may sit
// inside a wider parent and vertical keyboard movement does not affect it.
func actRebindHorizontalOverlap(a, b device.Rect) bool {
	width := min(a.Width(), b.Width())
	overlap := min(a.Right, b.Right) - max(a.Left, b.Left)
	return width > 0 && overlap >= width-width/2
}

func actRebindGeometry(original, fresh device.Rect) bool {
	oldArea, freshArea := rectArea64(original), rectArea64(fresh)
	return oldArea > 0 && freshArea > 0 &&
		min(oldArea, freshArea) >= (max(oldArea, freshArea)-1)/4+1 &&
		actRebindHorizontalOverlap(original, fresh)
}
