package engine

import (
	"context"
	"sort"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	coextensiveMinPercent      = 85
	colocatedMinOverlapPercent = 50
	colocatedMaxScreenPercent  = 50
	maxStaticCandidates        = 4
	maxCandidateDryRuns        = 16
	maxBlockingRefs            = 3
	maxPointHitSet             = 64
)

const (
	tapModeGrid3x3          = "grid_3x3"
	tapModeCoextensiveChild = "coextensive_child"
	tapModeBubbleAncestor   = "bubble_ancestor"
	tapModeBubbleColocated  = "bubble_colocated"

	scrollModeDirect             = "scroll_direct"
	scrollModeCoextensive        = "scroll_coextensive"
	scrollModeAncestorLift       = "scroll_ancestor_lift"
	scrollModeAxisFallback       = "scroll_axis_fallback"
	scrollModePointInnermost     = "scroll_point_innermost"
	scrollModePointFallback      = "scroll_point_fallback"
	scrollModePointDisambiguated = "scroll_point_disambiguated"
	scrollModeInnerFallback      = "scroll_inner_fallback"

	pointModeSet           = "point_set"
	pointModeDisambiguated = "point_disambiguated"
)

const (
	tapStaticScopePoint = "point"

	staticReasonNoScrollContainer         = "no_scroll_container"
	staticReasonNotScrollable             = "not_scrollable"
	staticReasonCompetingTargets          = "competing_targets"
	staticReasonCompetingScrollContainers = "competing_scroll_containers"
	staticReasonColocatedAmbiguous        = "colocated_ambiguous"
	staticReasonTrajectoryBlocked         = "trajectory_blocked"
	staticReasonInsufficientTravel        = "insufficient_travel"
	staticReasonAxisMismatch              = "axis_mismatch"
)

const (
	candidateRelationChild       = "child"
	candidateRelationAncestor    = "ancestor"
	candidateRelationOverlapping = "overlapping"
	candidateRelationCompeting   = "competing"
	candidateRelationPageScroll  = "page_scroll"
)

const (
	targetChangedReasonChildChanged       = "child_changed"
	targetChangedReasonTargetMissing      = "target_missing"
	targetChangedReasonTargetAmbiguous    = "target_ambiguous"
	targetChangedReasonLineageChanged     = "lineage_changed"
	targetChangedReasonCapabilityChanged  = "capability_changed"
	targetChangedReasonScrollScopeChanged = "scroll_scope_changed"
	targetChangedReasonTrajectoryBlocked  = "trajectory_blocked"
	targetChangedReasonScopeBlocked       = "scope_blocked"
	targetChangedReasonPointChanged       = "point_changed"
)

type candidateEntry struct {
	node     *protocol.Node
	relation string
}

func targetChangedError(reason, message string) *protocol.APIError {
	apiErr := protocol.NewError(409, protocol.CodeTargetChanged, message)
	if reason != "" {
		apiErr.WithDetail("reason", reason)
	}
	return apiErr
}

func probeTargetChangedError(state actProbeState, match actMatch, message string) *protocol.APIError {
	if state == actProbeAmbiguous {
		return targetChangedError(targetChangedReasonTargetAmbiguous, message)
	}
	if match.lineageMismatch {
		return targetChangedError(targetChangedReasonLineageChanged, message)
	}
	return targetChangedError(targetChangedReasonTargetMissing, message)
}

// The write-submit revalidation signal is the closed submit reason literal of the
// text stage, carried in error.details["reason"] so the single mapping in the
// text stage can forward it without re-parsing the message. The HTTP code and
// every other reason stay exactly as the ordinary revalidation produced them.
const submitFrameReasonDisabled = "submit_disabled"

// submitFrameDisabledError reports an execution target that is observed disabled
// on the fresh frame. It is a 409 target_changed with the submit classification,
// never an authorization to dispatch.
func submitFrameDisabledError() *protocol.APIError {
	return targetChangedError(submitFrameReasonDisabled, targetChangedMessage)
}

// submitExecutionEnabled proves the fresh enabled state of the execution target
// of one write-submit frame. A plan that observed the target disabled must see
// the explicit disabled -> enabled transition; any other plan must not see an
// explicit disable. A missing flag is accepted only when no transition is owed.
func submitExecutionEnabled(frame *tapFrame, initiallyDisabled bool) bool {
	if frame == nil || frame.raw == nil {
		return false
	}
	if initiallyDisabled {
		return frame.raw.HasEnabled && frame.raw.Enabled
	}
	return !(frame.raw.HasEnabled && !frame.raw.Enabled)
}

// tapPlanExecutionRaw returns the raw node a tap plan would actually execute on:
// the bubble ancestor, the coextensive delegate, the co-located carrier or the
// direct target. The child/parent selector is never the execution target.
func tapPlanExecutionRaw(plan *tapPlan) *device.RawNode {
	if plan == nil {
		return nil
	}
	switch {
	case plan.bubbleAncestorRaw != nil:
		return plan.bubbleAncestorRaw
	case plan.colocatedRaw != nil:
		return plan.colocatedRaw
	case plan.delegateRaw != nil:
		return plan.delegateRaw
	default:
		return plan.targetRaw
	}
}

// structureWithoutEnabled copies one node with its enabled tri-state erased, so
// two nodes can be compared for every other field.
func structureWithoutEnabled(node *device.RawNode) device.RawNode {
	copied := *node
	copied.HasEnabled, copied.Enabled = false, false
	return copied
}

// submitExecutionDisabledOnFresh attributes a failed submit frame to a disabled
// execution target. It reports true when exactly one fresh node matches the
// plan's execution target on every field except the enabled state and is
// explicitly disabled there. The target stays rejected; only the reason changes
// from a generic capability change to submit_disabled.
func submitExecutionDisabledOnFresh(fresh *device.Capture, want *device.RawNode) bool {
	if fresh == nil || want == nil {
		return false
	}
	screen := fresh.Display.Bounds()
	empty := structureWithoutEnabled(want)
	matches := 0
	for index := range fresh.Nodes {
		node := &fresh.Nodes[index]
		if node.DisplayID != want.DisplayID || !node.HasEnabled || node.Enabled {
			continue
		}
		candidate := structureWithoutEnabled(node)
		if compatible, _ := sameStructure(&empty, &candidate); !compatible {
			continue
		}
		if !geometryCompatible(want.Bounds, node.Bounds, screen,
			actTier2MinOverlap) {
			continue
		}
		matches++
		if matches > 1 {
			return false
		}
	}
	return matches == 1
}

// actTargetProbeOutcome maps one probe state to the closed event outcome.
func actTargetProbeOutcome(state actProbeState) string {
	switch state {
	case actProbeUnique:
		return "proven"
	case actProbeAmbiguous:
		return "ambiguous"
	case actProbeNone:
		return "none"
	case actProbeCancelled:
		return "stopped"
	case actProbeBudgetExhausted:
		return "budget"
	default:
		return "malformed"
	}
}

// actTargetLog records one ref-target match decision. tier is the bounded
// integer level that actually proved the target (1-5) and is present only for a
// unique match; a rejection carries no tier. attempt is the bounded revalidation
// attempt that produced the decision (1 or 2) and separates a first-frame pass
// from a transient re-check. No ref, label, text or tree content is ever
// written.
func (e *Engine) actTargetLog(operationID, outcome string, tier, attempt int) {
	if e == nil || e.log == nil || operationID == "" || outcome == "" {
		return
	}
	fields := []eventlog.Field{
		eventlog.Str("operation_id", operationID),
		eventlog.Str("outcome", outcome),
	}
	if tier > 0 && tier <= 5 {
		fields = append(fields, eventlog.Int("tier", tier))
	}
	if attempt >= 1 && attempt <= 2 {
		fields = append(fields, eventlog.Int("attempt", attempt))
	}
	e.log.Event(eventlog.Debug, "act.target", fields...)
}

// bubbleRelationChangedError reports a C->A relationship change and attributes
// it to the original public ancestor ref. The nearest-interactive-ancestor
// relationship is precisely what changed, so the parent ref is the attribution.
func bubbleRelationChangedError(ancestorRef string) *protocol.APIError {
	apiErr := targetChangedError(targetChangedReasonLineageChanged,
		targetChangedMessage)
	if ancestorRef != "" {
		apiErr.WithDetail("changed_ref", ancestorRef)
	}
	return apiErr
}

// tapFrameTier4OcclusionProven reports whether a Tier 4 or higher landing
// point has a unique whole-tree deepest hit inside the proven target subtree
// and no external interactive owner covers it. Rebuilt ancestors and vertical
// translations require the whole-tree proof in addition to the subtree scope.
func tapFrameTier4OcclusionProven(view *tapPublicView, target *protocol.Node,
	x, y int) bool {
	if view == nil || view.tree == nil || view.tree.Root == nil || target == nil {
		return false
	}
	if len(externalInteractiveOwnersAt(view, target, x, y)) > 0 {
		return false
	}
	ref, state := resolvePoint(view.tree.Root, x, y)
	if state != pointUnique || ref == "" {
		return false
	}
	hit := view.byRef[ref]
	if hit == nil {
		return false
	}
	return hit == target || isPublicAncestor(view, target, hit)
}

// attachChangedRef adds the optional changed_ref detail to a lineage_changed
// 409. The attribution is bounded pure CPU and never overrides the primary
// error: an unresolvable or ambiguous change omits the detail rather than
// guessing.
func attachChangedRef(ctx context.Context, apiErr *protocol.APIError,
	obs *observationState, fresh *device.Capture, want *device.RawNode) {
	if apiErr == nil || obs == nil || obs.Projection == nil ||
		obs.Projection.Capture == nil || obs.Projection.Tree == nil ||
		fresh == nil || want == nil {
		return
	}
	if apiErr.Code != protocol.CodeTargetChanged ||
		apiErr.Details["reason"] != targetChangedReasonLineageChanged {
		return
	}
	if _, exists := apiErr.Details["changed_ref"]; exists {
		return
	}
	if ref := changedPublicAncestorRef(ctx, obs, fresh, want); ref != "" {
		apiErr.WithDetail("changed_ref", ref)
	}
}

// changedPublicAncestorRef names the nearest public ancestor that no longer
// aligns on the fresh frame. The whole attribution is bounded: the candidate scan
// and both lineage walks charge the shared budget and inspect STOP, and at most
// maxChangedRefAncestors levels are examined. A hidden raw wrapper has no public
// ref and therefore never masquerades as a public layer: the search continues
// outward until a level is uniquely locatable. "" means "not provable".
func changedPublicAncestorRef(ctx context.Context, obs *observationState,
	fresh *device.Capture, want *device.RawNode) string {
	original := obs.Projection.Capture
	oldIndex := indexOf(original, want)
	if oldIndex < 0 {
		return ""
	}
	limits := defaultActMatchLimits()
	budget := &matchBudget{
		ctx: ctx, candidateLimit: len(original.Nodes) + len(fresh.Nodes),
		lineageLimit: limits.lineageLimit, lineageDepth: limits.lineageDepth,
	}
	candidate, ok := changedStructureCandidate(original, oldIndex, fresh, budget)
	if !ok {
		return ""
	}
	oldChain, ok := lineageL1(original, oldIndex, budget)
	if !ok {
		return ""
	}
	newChain, ok := lineageL1(fresh, candidate, budget)
	if !ok {
		return ""
	}
	// An ancestor is only blamed when the old chain itself lost it. A level that
	// still has exactly one structural counterpart in the fresh ancestor chain is
	// merely shifted (for example behind a newly inserted wrapper) and is never
	// reported as changed. Two or more possible counterparts are ambiguous, so
	// the whole attribution is omitted rather than guessed.
	for level := 0; level < len(oldChain) && level < maxChangedRefAncestors; level++ {
		if !budget.consumeCandidate() {
			return ""
		}
		entry := oldChain[level]
		counterparts := 0
		for depth := 0; depth < len(newChain) && depth < maxChangedRefAncestors; depth++ {
			if !budget.consumeCandidate() {
				return ""
			}
			if newChain[depth].root == entry.root &&
				newChain[depth].key == entry.key {
				counterparts++
			}
		}
		switch {
		case counterparts == 1:
			continue
		case counterparts > 1:
			return ""
		}
		// The level is gone. A hidden raw wrapper has no public ref and never
		// masquerades as a public layer: keep walking outward to the nearest
		// ancestor that is uniquely addressable.
		if ref := projectionPublicRefForRaw(obs.Projection, entry.parent); ref != "" {
			return ref
		}
	}
	return ""
}

// maxChangedRefAncestors bounds one changed_ref attribution walk.
const maxChangedRefAncestors = 64

// changedStructureCandidate finds the single fresh node that still carries the
// failed target's source structure within the tier-2 geometry band. None or more
// than one candidate leaves the change unattributable. The scan is budget-and-
// ctx bounded.
func changedStructureCandidate(original *device.Capture, oldIndex int,
	fresh *device.Capture, budget *matchBudget) (int, bool) {
	if original == nil || fresh == nil || budget == nil || oldIndex < 0 ||
		oldIndex >= len(original.Nodes) {
		return -1, false
	}
	want := &original.Nodes[oldIndex]
	screen := fresh.Display.Bounds()
	candidate := -1
	for index := range fresh.Nodes {
		if !budget.consumeCandidate() {
			return -1, false
		}
		node := &fresh.Nodes[index]
		if node.DisplayID != want.DisplayID {
			continue
		}
		if compatible, _ := sameStructure(want, node); !compatible {
			continue
		}
		if want.Bounds != node.Bounds &&
			!geometryCompatible(want.Bounds, node.Bounds, screen,
				actTier2MinOverlap) {
			continue
		}
		if candidate >= 0 {
			return -1, false
		}
		candidate = index
	}
	return candidate, candidate >= 0
}

// projectionPublicRefForRaw resolves one raw index back to a public ref through
// both source maps. The lookup requires strict uniqueness across Source and
// ContextSource, and the synthetic root raw index is never reported.
func projectionPublicRefForRaw(projection *Projection, rawIndex int) string {
	if projection == nil || rawIndex < 0 ||
		(projection.Capture != nil && rawIndex == projection.Capture.Root) {
		return ""
	}
	ref := ""
	for candidateRef, index := range projection.Source {
		if index != rawIndex || candidateRef == "" {
			continue
		}
		if ref != "" && ref != candidateRef {
			return ""
		}
		ref = candidateRef
	}
	for candidateRef, index := range projection.ContextSource {
		if index != rawIndex || candidateRef == "" {
			continue
		}
		if ref != "" && ref != candidateRef {
			return ""
		}
		ref = candidateRef
	}
	return ref
}

func rectArea64(rect device.Rect) int64 {
	if rect.Empty() {
		return 0
	}
	return int64(rect.Width()) * int64(rect.Height())
}

func clipRectToScreen(rect, screen device.Rect) device.Rect {
	if rect.Empty() || screen.Empty() {
		return device.Rect{}
	}
	return rect.Intersect(screen)
}

func screenRectFromDisplay(screen device.Display) device.Rect {
	if screen.Width <= 0 || screen.Height <= 0 {
		return device.Rect{}
	}
	return device.Rect{Left: 0, Top: 0, Right: screen.Width, Bottom: screen.Height}
}

func publicNodeClippedRect(node *protocol.Node, screen device.Rect) (device.Rect, bool) {
	if node == nil || node.Bounds == nil {
		return device.Rect{}, false
	}
	clipped := clipRectToScreen(rectFromBounds(node.Bounds), screen)
	if clipped.Empty() {
		return device.Rect{}, false
	}
	return clipped, true
}

func isCoextensiveRect(first, second device.Rect) bool {
	if first.Empty() || second.Empty() {
		return false
	}
	inter := first.Intersect(second)
	if inter.Empty() {
		return false
	}
	interW := int64(inter.Width()) * 100
	interH := int64(inter.Height()) * 100
	return interW >= int64(first.Width())*coextensiveMinPercent &&
		interH >= int64(first.Height())*coextensiveMinPercent &&
		interW >= int64(second.Width())*coextensiveMinPercent &&
		interH >= int64(second.Height())*coextensiveMinPercent
}

func rectOverlapAtLeast(subject, other device.Rect, minPercent int64) bool {
	subjectArea := rectArea64(subject)
	if subjectArea <= 0 || other.Empty() {
		return false
	}
	interArea := rectArea64(subject.Intersect(other))
	return interArea*100 >= subjectArea*minPercent
}

func rectAreaLessThanPercent(rect, screen device.Rect, maxPercent int64) bool {
	screenArea := rectArea64(screen)
	if screenArea <= 0 || rect.Empty() {
		return false
	}
	return rectArea64(rect)*100 < screenArea*maxPercent
}

func findPublicNodeBounded(root *protocol.Node, ref string) (*protocol.Node, bool) {
	if root == nil || ref == "" {
		return nil, true
	}
	visited := make(map[*protocol.Node]struct{})
	stack := []*protocol.Node{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == nil {
			continue
		}
		if _, seen := visited[current]; seen {
			return nil, false
		}
		visited[current] = struct{}{}
		if current.Ref == ref {
			return current, true
		}
		for index := len(current.Children) - 1; index >= 0; index-- {
			stack = append(stack, current.Children[index])
		}
	}
	return nil, true
}

func isPublicAncestor(view *tapPublicView, ancestor, descendant *protocol.Node) bool {
	if view == nil || ancestor == nil || descendant == nil || ancestor == descendant {
		return false
	}
	visited := make(map[*protocol.Node]struct{})
	current := descendant
	for current != nil {
		if _, seen := visited[current]; seen {
			return false
		}
		visited[current] = struct{}{}
		info := view.info[current]
		if info == nil || info.parent == nil {
			return false
		}
		if info.parent == ancestor {
			return true
		}
		current = info.parent
	}
	return false
}

// colocatedSyncShiftOK checks that both bindings moved vertically together.
func colocatedSyncShiftOK(oldChild, freshChild, oldColocated, freshColocated device.Rect) bool {
	oldChildH, freshChildH := oldChild.Height(), freshChild.Height()
	oldColocatedH, freshColocatedH := oldColocated.Height(), freshColocated.Height()
	if min(oldChildH, freshChildH, oldColocatedH, freshColocatedH) <= 0 {
		return false
	}
	maxH := max(oldChildH, freshChildH, oldColocatedH, freshColocatedH)
	_, oldChildY := oldChild.Center()
	_, freshChildY := freshChild.Center()
	_, oldColocatedY := oldColocated.Center()
	_, freshColocatedY := freshColocated.Center()
	childShift := freshChildY - oldChildY
	colocatedShift := freshColocatedY - oldColocatedY
	return absInt(childShift-colocatedShift) <= max(16, maxH/4)
}

func colocatedMatchGuard(view *tapPublicView, child, candidate *protocol.Node, screen device.Rect) bool {
	if view == nil || child == nil || candidate == nil || child == candidate {
		return false
	}
	if isPublicAncestor(view, candidate, child) {
		return false
	}
	childRect, ok := publicNodeClippedRect(child, screen)
	if !ok {
		return false
	}
	candRect, ok := publicNodeClippedRect(candidate, screen)
	if !ok {
		return false
	}
	if isPublicAncestor(view, child, candidate) {
		return isCoextensiveRect(candRect, childRect) ||
			(rectOverlapAtLeast(childRect, candRect, colocatedMinOverlapPercent) &&
				rectAreaLessThanPercent(candRect, screen, colocatedMaxScreenPercent))
	}
	centerX, centerY, ok := tapSafeCenter(childRect)
	if !ok {
		return false
	}
	if !candRect.Contains(centerX, centerY) &&
		!rectOverlapAtLeast(childRect, candRect, colocatedMinOverlapPercent) {
		return false
	}
	return rectAreaLessThanPercent(candRect, screen, colocatedMaxScreenPercent)
}

func buildStaticCandidates(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, entries []candidateEntry) []map[string]any {
	return buildActionCandidates(obs, view, operation, entries, false)
}

func buildActionCandidates(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, entries []candidateEntry, executableOnly bool) []map[string]any {
	if obs == nil || view == nil || operation == nil || len(entries) == 0 {
		return nil
	}
	screen := screenRectFromDisplay(obs.Screen)
	if screen.Empty() {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	deduped := make([]candidateEntry, 0, min(len(entries), maxCandidateDryRuns))
	for _, entry := range entries {
		node := entry.node
		if node == nil || node.Ref == "" || node.Role == protocol.RoleRoot || node.Context {
			continue
		}
		if _, duplicate := seen[node.Ref]; duplicate {
			continue
		}
		if _, ok := publicNodeClippedRect(node, screen); !ok {
			continue
		}
		seen[node.Ref] = struct{}{}
		deduped = append(deduped, entry)
		if len(deduped) >= maxCandidateDryRuns {
			break
		}
	}
	if len(deduped) == 0 {
		return nil
	}
	type evaluatedCandidate struct {
		entry      candidateEntry
		clipped    device.Rect
		executable bool
	}
	evaluated := make([]evaluatedCandidate, 0, len(deduped))
	for _, entry := range deduped {
		clipped, ok := publicNodeClippedRect(entry.node, screen)
		if !ok {
			continue
		}
		executable := false
		switch operation.Type {
		case "scroll":
			executable = tryPlanScrollBase(obs, view, entry.node, operation.Direction, operation.Amount)
		case "tap":
			executable = tryPlanTapDirectBase(obs, view, entry.node, protocol.ActionTap)
		case "long_press":
			executable = tryPlanTapDirectBase(obs, view, entry.node, protocol.ActionLongPress)
		case "text":
			executable = tryPlanTextDirectBase(obs, view, entry.node)
		}
		if executableOnly && !executable {
			continue
		}
		evaluated = append(evaluated, evaluatedCandidate{
			entry:      entry,
			clipped:    clipped,
			executable: executable,
		})
	}
	sort.SliceStable(evaluated, func(left, right int) bool {
		return evaluated[left].executable && !evaluated[right].executable
	})
	limit := min(len(evaluated), maxStaticCandidates)
	out := make([]map[string]any, 0, limit)
	for index := 0; index < limit; index++ {
		item := evaluated[index]
		out = append(out, map[string]any{
			"ref":        item.entry.node.Ref,
			"role":       string(item.entry.node.Role),
			"actions":    tapPublicActions(item.entry.node),
			"bounds":     []int{item.clipped.Left, item.clipped.Top, item.clipped.Right, item.clipped.Bottom},
			"relation":   item.entry.relation,
			"executable": item.executable,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func attachStaticCandidates(apiErr *protocol.APIError, candidates []map[string]any) {
	if apiErr == nil || len(candidates) == 0 {
		return
	}
	apiErr.WithDetail("candidates", candidates)
}

func attachStaticBlockingRefs(apiErr *protocol.APIError, refs []string) {
	if apiErr != nil {
		if clean := boundedBlockingRefs(refs); len(clean) > 0 {
			apiErr.WithDetail("blocking_refs", clean)
		}
	}
}

func boundedBlockingRefs(refs []string) []string {
	seen := make(map[string]struct{}, min(len(refs), maxBlockingRefs))
	clean := make([]string, 0, min(len(refs), maxBlockingRefs))
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		clean = append(clean, ref)
		if len(clean) >= maxBlockingRefs {
			break
		}
	}
	return clean
}

func collectPageScrollCandidates(obs *observationState, view *tapPublicView, excludeRef string) []candidateEntry {
	if obs == nil || view == nil {
		return nil
	}
	screen := screenRectFromDisplay(obs.Screen)
	type scored struct {
		node  *protocol.Node
		area  int64
		order int
	}
	found := make([]scored, 0)
	for _, node := range view.nodes {
		if node == nil || node.Ref == "" || node.Ref == excludeRef {
			continue
		}
		if !isScrollContainer(node) {
			continue
		}
		clipped, ok := publicNodeClippedRect(node, screen)
		if !ok {
			continue
		}
		found = append(found, scored{
			node:  node,
			area:  rectArea64(clipped),
			order: tapPublicOrder(view, node),
		})
	}
	sort.Slice(found, func(left, right int) bool {
		if found[left].area != found[right].area {
			return found[left].area > found[right].area
		}
		return found[left].order < found[right].order
	})
	entries := make([]candidateEntry, 0, len(found))
	for _, item := range found {
		entries = append(entries, candidateEntry{
			node:     item.node,
			relation: candidateRelationPageScroll,
		})
	}
	return entries
}
