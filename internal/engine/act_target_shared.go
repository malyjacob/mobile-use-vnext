package engine

import (
	"sort"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	coextensiveMinPercent       = 85
	colocatedMinOverlapPercent  = 50
	colocatedMaxScreenPercent   = 50
	maxStaticCandidates         = 4
	maxCandidateDryRuns         = 16
	maxBlockingRefs             = 3
	maxPointHitSet              = 64
)

const (
	tapModeGrid3x3          = "grid_3x3"
	tapModeCoextensiveChild = "coextensive_child"
	tapModeBubbleAncestor   = "bubble_ancestor"
	tapModeBubbleColocated  = "bubble_colocated"

	scrollModeDirect             = "scroll_direct"
	scrollModeCoextensive        = "scroll_coextensive"
	scrollModeAncestorLift       = "scroll_ancestor_lift"
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
	if apiErr == nil || len(refs) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(refs))
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
	if len(clean) > 0 {
		apiErr.WithDetail("blocking_refs", clean)
	}
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
