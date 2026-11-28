package engine

import (
	"context"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// attachActRejectionDiagnostics only annotates the first result's error copy.
// All refs here belong to the projection actually published with that result.
func (e *Engine) attachActRejectionDiagnostics(ctx context.Context, resultErr *protocol.Error,
	original, published *observationState, operation *protocol.Operation, tap *tapPlan,
	scroll *scrollPlan, originalRef string) {
	if ctx.Err() != nil || resultErr.Code != protocol.CodeTargetChanged {
		return
	}
	view, apiErr := collectTapPublicView(published)
	if apiErr != nil {
		return
	}
	if _, ok := ensureTapViewTopology(published, view); !ok {
		return
	}
	reason, _ := resultErr.Details["reason"].(string)
	var candidates []map[string]any
	var blockers []string
	var matched *protocol.Node
	if originalRef != "" {
		var want *device.RawNode
		if scroll != nil {
			want = sourceNode(original, originalRef)
		} else if tap != nil {
			want = tap.targetRaw
		}
		matched = e.matchRejectedPublicTarget(ctx, original, published, operation.Type, want)
		if matched != nil && scroll != nil {
			entries := collectScrollAncestorAndPageCandidates(published, view, matched)
			candidates = buildFilteredScrollCandidates(published, view, operation, matched, entries)
		} else if matched != nil {
			execution := matched
			if raw := tapPlanExecutionRaw(tap); reason == targetChangedReasonScopeBlocked && raw != nil && raw != want {
				execution = e.matchRejectedPublicTarget(ctx, original, published, operation.Type, raw)
			}
			entries, refs := matchedTapRejectionDiagnostics(published, view, operation, matched, execution, tap, reason)
			candidates = buildStaticCandidates(published, view, operation, entries)
			blockers = refs
		}
	} else if reason == targetChangedReasonPointChanged && operation.Target != nil &&
		sameScreenGeometry(original.Screen, published.Screen) {
		blockers = e.changedPointBlockingRefs(ctx, original, published, view, operation)
	}
	if scroll != nil && reason == targetChangedReasonTrajectoryBlocked {
		// The chosen container can differ from the request's selector. Only the
		// chosen container's fresh trajectory can name blockers.
		container := matched
		if container == nil || scroll.container != sourceNode(original, originalRef) {
			container = e.matchRejectedPublicTarget(ctx, original, published, operation.Type, scroll.container)
		}
		if container != nil {
			blockers = rejectedScrollBlockingRefs(published, view, container, scroll)
		}
	}

	// A matched scroll target already checked page entries in its own window.
	// A geometry-only fallback must not undo that window filter.
	if len(candidates) == 0 && ctx.Err() == nil && (scroll == nil || matched == nil) {
		region := actDiagnosticRegion(original, published, operation, originalRef)
		candidates = buildRectRejectionCandidates(published, view, operation, matched, region)
		if len(candidates) == 0 && !region.Empty() {
			candidates = buildRectRejectionCandidates(published, view, operation, matched, device.Rect{})
		}
	}
	if len(candidates) > 0 {
		resultErr.Details["candidates"] = candidates
	}
	if clean := boundedBlockingRefs(blockers); len(clean) > 0 {
		resultErr.Details["blocking_refs"] = clean
	}
}

func (e *Engine) matchRejectedPublicTarget(ctx context.Context, original, published *observationState,
	kind string, want *device.RawNode) *protocol.Node {
	if want == nil || original.Projection == nil || original.Projection.Capture == nil {
		return nil
	}
	before, after := original.Projection.Capture, published.Projection.Capture
	var match actMatch
	var state actProbeState
	if kind == "scroll" {
		match, state = e.probeActNodeWith(ctx, nil, before, after, want, actMatchOptionsForOperation(kind))
	} else {
		match, state = e.probeTapFrameNode(ctx, nil, before, after, want, false, tapFrameOptions{})
	}
	if state != actProbeUnique {
		return nil
	}
	return publicNodeByRawIndex(published.Projection, indexOf(after, match.raw))
}

func matchedTapRejectionDiagnostics(obs *observationState, view *tapPublicView, operation *protocol.Operation,
	target, execution *protocol.Node, tap *tapPlan, reason string) ([]candidateEntry, []string) {
	screen := screenRectFromDisplay(obs.Screen)
	action := protocol.Action(operation.Type)
	var entries []candidateEntry
	if action == protocol.ActionText {
		entries = collectTextFallbackCandidates(view, target, screen)
	} else {
		entries = collectTapFallbackCandidates(view, target, action, screen, true)
	}
	if reason != targetChangedReasonScopeBlocked || execution == nil {
		return entries, nil
	}
	input, apiErr := buildTapInput(obs, view, execution)
	if apiErr != nil {
		return entries, nil
	}
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		return entries, nil
	}
	points := tapStaticCandidatePoints(scope, input.targetRaw)
	if tap != nil && scope.base.Contains(tap.x, tap.y) {
		points = append(points, tapStaticPoint{x: tap.x, y: tap.y})
	}
	diagnosis := diagnoseTapStaticFailure(view, scope, execution, false, points)
	blockedEntries, blockers := collectDirectBlockingDiagnostics(view, execution, action, screen, diagnosis)
	// Revalidation can also fail the whole-tree occlusion proof after direct
	// scope planning passes. Check its old point and freshly planned point.
	if tap != nil && scope.base.Contains(tap.x, tap.y) {
		for _, owner := range externalInteractiveOwnersAt(view, execution, tap.x, tap.y) {
			blockers = append(blockers, owner.Ref)
		}
	}
	if plan, planErr := chooseTapPlan(scope, input, execution, action, time.Time{}); planErr == nil {
		for _, owner := range externalInteractiveOwnersAt(view, execution, plan.x, plan.y) {
			blockers = append(blockers, owner.Ref)
		}
	}
	return append(blockedEntries, entries...), blockers
}

func rejectedScrollBlockingRefs(obs *observationState, view *tapPublicView, target *protocol.Node,
	plan *scrollPlan) []string {
	info := view.info[target]
	if info == nil || !info.hasSource || info.rawIndex < 0 || info.rawIndex >= len(view.capture.Nodes) {
		return nil
	}
	clipped := view.capture.Nodes[info.rawIndex].Bounds.Intersect(view.capture.Display.Bounds())
	_, failure := planScrollOnAxes(obs.Projection, info.rawIndex, clipped,
		plan.direction, plan.amount, plan.selector, plan.mode)
	refs := failure.blockingRefs
	for _, node := range failure.blockingScrollNodes {
		refs = append(refs, node.Ref)
	}
	return refs
}

func (e *Engine) changedPointBlockingRefs(ctx context.Context, original, published *observationState,
	view *tapPublicView, operation *protocol.Operation) []string {
	x, y := operation.Target.X, operation.Target.Y
	if !inScreen(x, y, published.Screen) {
		return nil
	}
	var expected *protocol.Node
	if oldView, apiErr := collectTapPublicView(original); apiErr == nil {
		oldOwners := collectPointOwners(oldView, resolvePointSet(oldView.tree.Root, x, y))
		oldOwners = filterMatchingPointOwners(oldOwners, operation)
		oldOwners = innermostPointOwners(oldView, oldOwners)
		if len(oldOwners) == 1 {
			expected = e.matchRejectedPublicTarget(ctx, original, published, operation.Type,
				sourceNode(original, oldOwners[0].Ref))
		}
	}
	var blockers []string
	for _, owner := range externalInteractiveOwnersAt(view, expected, x, y) {
		blockers = append(blockers, owner.Ref)
	}
	return blockers
}

func actDiagnosticRegion(original, published *observationState, operation *protocol.Operation,
	originalRef string) device.Rect {
	if !sameScreenGeometry(original.Screen, published.Screen) {
		return device.Rect{}
	}
	if originalRef != "" {
		if original.Projection != nil && original.Projection.Tree != nil {
			node, _ := findPublicNodeBounded(original.Projection.Tree.Root, originalRef)
			bounds, _ := publicNodeClippedRect(node, screenRectFromDisplay(original.Screen))
			return bounds
		}
		return device.Rect{}
	}
	target := operation.Target
	if operation.Type == "scroll" {
		target = operation.Container
	}
	if target != nil && inScreen(target.X, target.Y, published.Screen) {
		return device.Rect{Left: target.X, Top: target.Y, Right: target.X + 1, Bottom: target.Y + 1}
	}
	return device.Rect{}
}

func buildRectRejectionCandidates(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, exclude *protocol.Node, region device.Rect) []map[string]any {
	entries := collectRectRejectionCandidates(obs, view, operation, exclude, region)
	if operation.Type == "scroll" {
		entries = filterScrollCandidateEntries(obs, view, operation, nil, entries)
	}
	return buildActionCandidates(obs, view, operation, entries, true)
}

func collectRectRejectionCandidates(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, exclude *protocol.Node, region device.Rect) []candidateEntry {
	type scoredCandidate struct {
		entry    candidateEntry
		readable bool
		area     int64
		overlap  int64
		distance int64
	}
	screen := screenRectFromDisplay(obs.Screen)
	local := !region.Empty()
	x, y := region.Center()
	isScroll := operation.Type == "scroll"
	var scored []scoredCandidate
	for _, node := range view.nodes {
		if node == exclude {
			continue
		}
		relation := candidateRelationCompeting
		if isScroll {
			if !isScrollContainer(node) || !scrollContainerAxisCompatible(obs, view, node, operation.Direction) {
				continue
			}
			relation = candidateRelationPageScroll
		} else if !directBlockingCandidate(node, protocol.Action(operation.Type)) {
			continue
		}
		bounds, ok := publicNodeClippedRect(node, screen)
		if !ok {
			continue
		}
		overlap := rectArea64(bounds.Intersect(region))
		if local && overlap == 0 {
			continue
		}
		if local {
			relation = candidateRelationOverlapping
		}
		centerX, centerY := bounds.Center()
		scored = append(scored, scoredCandidate{
			entry:    candidateEntry{node: node, relation: relation},
			readable: !anonymousPlainNode(node), area: rectArea64(bounds),
			overlap: overlap, distance: pointDistSq64(centerX, centerY, x, y),
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i], scored[j]
		if local && a.overlap != b.overlap {
			return a.overlap > b.overlap
		}
		if local && a.distance != b.distance {
			return a.distance < b.distance
		}
		if a.readable != b.readable {
			return a.readable
		}
		if isScroll {
			return a.area > b.area
		}
		return a.area < b.area
	})
	entries := make([]candidateEntry, 0, len(scored))
	for _, candidate := range scored {
		entries = append(entries, candidate.entry)
	}
	return entries
}
