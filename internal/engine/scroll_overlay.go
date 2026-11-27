package engine

import (
	"slices"
	"sort"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const scrollOverlayMinPercent = 50

type scrollOverlayNode struct {
	winID, childIndex, subtreeEnd int
	rect                          device.Rect
	underScroll, contentBelow     bool
	hasInteractive                bool
	solid, solidKnown             bool
}

// scrollOverlayContext belongs to one static rejection. The validated view
// already provides public parents, depth and preorder, including Tree.Root.
type scrollOverlayContext struct {
	projection  *Projection
	view        *tapPublicView
	screen      device.Rect
	multiWindow bool
	nodes       []scrollOverlayNode
	scrims      []*tapPublicInfo
}

func newScrollOverlayContext(obs *observationState, view *tapPublicView) *scrollOverlayContext {
	ctx := &scrollOverlayContext{
		projection: obs.Projection, view: view,
		screen: screenRectFromDisplay(obs.Screen), nodes: make([]scrollOverlayNode, len(view.nodes)),
	}
	windows := scrollOverlayRawWindows(view.capture)
	ctx.multiWindow = windows != nil
	for order, node := range view.nodes {
		info, entry := view.info[node], &ctx.nodes[order]
		entry.rect, _ = publicNodeClippedRect(node, ctx.screen)
		entry.subtreeEnd = order + 1
		entry.hasInteractive = isTouchInteractive(node) || isScrollContainer(node)
		if info.parent != nil {
			parent := ctx.nodes[view.info[info.parent].order]
			entry.underScroll = parent.underScroll || info.parent.Role == protocol.RoleScroll
			entry.winID = parent.winID
		}
		if ctx.multiWindow {
			raw, mapped := view.source[node.Ref]
			if node.Context {
				raw, mapped = obs.Projection.ContextSource[node.Ref]
			}
			if mapped && raw >= 0 && raw < len(windows) {
				entry.winID = windows[raw]
			}
		}
		for childIndex, child := range node.Children {
			ctx.nodes[view.info[child].order].childIndex = childIndex
		}
	}
	for order := len(view.nodes) - 1; order >= 0; order-- {
		node := view.nodes[order]
		info, entry := view.info[node], &ctx.nodes[order]
		if info.parent != nil {
			parent := &ctx.nodes[view.info[info.parent].order]
			parent.subtreeEnd = max(parent.subtreeEnd, entry.subtreeEnd)
			parent.contentBelow = parent.contentBelow || entry.contentBelow || scrollOverlayHasContent(node)
			parent.hasInteractive = parent.hasInteractive || entry.hasInteractive
		}
		// Reject content-bearing descendants from their cached subtree facts.
		// Any remaining scrim searches visit disjoint public subtrees.
		if !entry.underScroll && !entry.rect.Empty() && !entry.contentBelow && isBackdropScrimNode(node) {
			ctx.scrims = append(ctx.scrims, info)
		}
	}
	return ctx
}

// Raw membership survives public projection dropping window wrappers. Window
// IDs are used only for equality: the capture's root order is a HashSet order.
func scrollOverlayRawWindows(capture *device.Capture) []int {
	if capture == nil || capture.Root < 0 || capture.Root >= len(capture.Nodes) {
		return nil
	}
	root := &capture.Nodes[capture.Root]
	if !isSyntheticDisplayRoot(root) || len(root.Children) < 2 {
		return nil
	}
	windows := make([]int, len(capture.Nodes))
	stack := make([]int, 0, len(root.Children))
	for index, window := range root.Children {
		stack = append(stack, window)
		for len(stack) > 0 {
			raw := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if raw < 0 || raw >= len(windows) || raw == capture.Root || windows[raw] != 0 {
				continue
			}
			windows[raw] = index + 1
			stack = append(stack, capture.Nodes[raw].Children...)
		}
	}
	return windows
}

func (ctx *scrollOverlayContext) commonAncestorDepth(want, cand *protocol.Node) int {
	left, right := ctx.view.info[want], ctx.view.info[cand]
	for left != nil && right != nil {
		if left == right {
			return left.depth
		}
		switch {
		case left.depth > right.depth:
			left = ctx.view.info[left.parent]
		case right.depth > left.depth:
			right = ctx.view.info[right.parent]
		default:
			left, right = ctx.view.info[left.parent], ctx.view.info[right.parent]
		}
	}
	return -1
}

func (ctx *scrollOverlayContext) contains(ancestor, descendant *tapPublicInfo) bool {
	return descendant != nil && ancestor.order <= descendant.order &&
		descendant.order < ctx.nodes[ancestor.order].subtreeEnd
}

func scrollOverlayHasContent(node *protocol.Node) bool {
	return len(node.Actions) != 0 || len(node.Label) != 0 || (node.Value != nil && *node.Value != "")
}

func isBackdropScrimNode(node *protocol.Node) bool {
	if !isTouchInteractive(node) || isScrollContainer(node) || len(node.Label) != 0 || node.Value != nil {
		return false
	}
	if len(node.Children) == 0 {
		return true
	}
	return walkScrollOverlayContent(node, func(child *protocol.Node) (descend, stop bool) {
		return true, scrollOverlayHasContent(child)
	})
}

func isSolidOverlayPanel(node *protocol.Node, nodeRect, screen device.Rect) bool {
	if isTouchInteractive(node) || isScrollContainer(node) {
		return true
	}
	if node == nil || nodeRect.Empty() {
		return false
	}
	clip := nodeRect.Intersect(screen)
	var rects []device.Rect
	walkScrollOverlayContent(node, func(child *protocol.Node) (descend, stop bool) {
		if isTouchInteractive(child) || isScrollContainer(child) ||
			(len(child.Children) == 0 && !anonymousPlainNode(child)) {
			if clipped, ok := publicNodeClippedRect(child, clip); ok {
				rects = append(rects, clipped)
			}
			return false, false
		}
		return true, false
	})
	return rectUnionArea64(rects) >= scrollOverlayHalfArea(nodeRect)
}

// walkScrollOverlayContent visits each descendant at most once and lets the
// caller prune covered branches or terminate the traversal early.
func walkScrollOverlayContent(root *protocol.Node, visit func(*protocol.Node) (descend, stop bool)) bool {
	seen := map[*protocol.Node]struct{}{root: {}}
	stack := append([]*protocol.Node(nil), root.Children...)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, visited := seen[node]; node == nil || visited {
			continue
		}
		seen[node] = struct{}{}
		descend, stop := visit(node)
		if stop {
			return false
		}
		if descend {
			stack = append(stack, node.Children...)
		}
	}
	return true
}

func scrollOverlayHalfArea(rect device.Rect) int64 {
	area := rectArea64(rect)
	return area/2 + area%2
}

// rectUnionArea64 sweeps x while tracking the covered length between adjacent
// y endpoints. Coverage counts keep overlapping rectangles from double counting.
func rectUnionArea64(rects []device.Rect) int64 {
	if len(rects) == 1 {
		return rectArea64(rects[0])
	}
	type edge struct {
		x, top, bottom int
		delta          int
	}
	edges := make([]edge, 0, 2*len(rects))
	ys := make([]int, 0, 2*len(rects))
	for _, rect := range rects {
		if !rect.Empty() {
			edges = append(edges, edge{rect.Left, rect.Top, rect.Bottom, 1},
				edge{rect.Right, rect.Top, rect.Bottom, -1})
			ys = append(ys, rect.Top, rect.Bottom)
		}
	}
	if len(edges) == 0 {
		return 0
	}
	sort.Ints(ys)
	ys = slices.Compact(ys)
	sort.Slice(edges, func(i, j int) bool { return edges[i].x < edges[j].x })
	type coverage struct {
		count  int
		length int64
	}
	covered := make([]coverage, 4*(len(ys)-1))
	var update func(index, lo, hi, from, to, delta int)
	update = func(index, lo, hi, from, to, delta int) {
		entry := &covered[index]
		mid := lo + (hi-lo)/2
		if from <= lo && hi <= to {
			entry.count += delta
		} else {
			if from < mid {
				update(index*2, lo, mid, from, to, delta)
			}
			if mid < to {
				update(index*2+1, mid, hi, from, to, delta)
			}
		}
		switch {
		case entry.count > 0:
			entry.length = int64(ys[hi]) - int64(ys[lo])
		case hi-lo > 1:
			entry.length = covered[index*2].length + covered[index*2+1].length
		default:
			entry.length = 0
		}
	}
	var area int64
	previousX := edges[0].x
	for _, event := range edges {
		area += (int64(event.x) - int64(previousX)) * covered[1].length
		update(1, 0, len(ys)-1, sort.SearchInts(ys, event.top), sort.SearchInts(ys, event.bottom), event.delta)
		previousX = event.x
	}
	return area
}

func isScrollCandidateOverlayOccluded(ctx *scrollOverlayContext, want, cand *protocol.Node,
	direction, amount string) bool {
	candInfo, wantInfo := ctx.view.info[cand], ctx.view.info[want]
	if candInfo == nil {
		return false
	}
	candidate := &ctx.nodes[candInfo.order]
	if ctx.multiWindow && wantInfo != nil && candidate.winID != ctx.nodes[wantInfo.order].winID {
		return true
	}
	if candidate.rect.Empty() {
		return false
	}

	var maskRects []device.Rect
	wantDepth := ctx.commonAncestorDepth(want, cand)
	for _, scrim := range ctx.scrims {
		rect := ctx.nodes[scrim.order].rect
		if ctx.contains(scrim, candInfo) || ctx.contains(candInfo, scrim) ||
			!rectOverlapAtLeast(candidate.rect, rect, scrollOverlayMinPercent) {
			continue
		}
		if wantInfo == nil || wantDepth <= ctx.commonAncestorDepth(want, scrim.node) {
			maskRects = append(maskRects, candidate.rect.Intersect(rect))
		}
	}

	for branch := candInfo; branch.parent != nil; branch = ctx.view.info[branch.parent] {
		childIndex := ctx.nodes[branch.order].childIndex
		for _, sibling := range branch.parent.Children[childIndex+1:] {
			if ctx.collectLaterOverlay(sibling, wantInfo, candidate, &maskRects) {
				return true
			}
		}
	}
	return len(maskRects) != 0 && ctx.masksBlockScrollAxes(cand, candidate.rect, maskRects, direction, amount)
}

func (ctx *scrollOverlayContext) collectLaterOverlay(node *protocol.Node, want *tapPublicInfo,
	candidate *scrollOverlayNode, maskRects *[]device.Rect) bool {
	info := ctx.view.info[node]
	entry := &ctx.nodes[info.order]
	if (ctx.multiWindow && entry.winID != candidate.winID) || entry.rect.Empty() || !entry.hasInteractive {
		return false
	}
	if !entry.solidKnown {
		entry.solid = isSolidOverlayPanel(node, entry.rect, ctx.screen)
		entry.solidKnown = true
	}
	if entry.solid {
		if ctx.contains(info, want) && rectOverlapAtLeast(candidate.rect, entry.rect, scrollOverlayMinPercent) {
			return true
		}
		if clipped := entry.rect.Intersect(candidate.rect); !clipped.Empty() {
			*maskRects = append(*maskRects, clipped)
		}
		return false
	}
	for _, child := range node.Children {
		if ctx.collectLaterOverlay(child, want, candidate, maskRects) {
			return true
		}
	}
	return false
}

func (ctx *scrollOverlayContext) masksBlockScrollAxes(cand *protocol.Node, candRect device.Rect,
	maskRects []device.Rect, direction, amount string) bool {
	vertical, ok := scrollDirectionVertical(direction)
	if !ok {
		return false
	}
	clipped, shared := candRect, candRect
	index, mapped := ctx.projection.Source[cand.Ref]
	if mapped && index >= 0 && index < len(ctx.projection.Capture.Nodes) {
		clipped = ctx.projection.Capture.Nodes[index].Bounds.Intersect(ctx.screen)
		shared = clipped.Intersect(candRect)
	} else {
		index = -1
	}
	coords := scrollOrthogonalCoords(clipped, shared, vertical)
	if len(coords) == 0 {
		return false
	}
	base := axisSpan(shared, vertical)
	var blocked []scrollSpan
	var open [3][]scrollSpan
	baseAllows := false
	for axis, coord := range coords {
		blocked = blocked[:0]
		for _, rect := range maskRects {
			if orthogonalContains(rect, coord, vertical) {
				blocked = append(blocked, axisSpan(rect, vertical))
			}
		}
		open[axis] = subtractScrollSpans(base, blocked)
		baseAllows = baseAllows || scrollOverlaySpansAllowTravel(open[axis], amount)
	}
	// Entire-axis coverage can exclude a candidate that already had no proof.
	if !baseAllows && rectUnionArea64(maskRects) >= scrollOverlayHalfArea(candRect) {
		return true
	}

	proofAllowed := false
	for axis, coord := range coords {
		space, valid := scrollSafeSpansAt(ctx.projection, index, clipped, direction, coord)
		if !valid || !scrollOverlaySpansAllowTravel(space.spans, amount) {
			continue
		}
		proofAllowed = true
		if scrollOverlayIntersectedTravel(space.spans, open[axis], amount) {
			return false
		}
	}
	return proofAllowed
}

func scrollOverlaySpansAllowTravel(spans []scrollSpan, amount string) bool {
	for _, span := range spans {
		if _, ok := scrollTravelForExtent(span.length(), amount); ok {
			return true
		}
	}
	return false
}

// Both inputs are sorted disjoint intervals over the same candidate axis.
func scrollOverlayIntersectedTravel(proven, open []scrollSpan, amount string) bool {
	for len(proven) != 0 && len(open) != 0 {
		start := max(proven[0].start, open[0].start)
		end := min(proven[0].end, open[0].end)
		if _, ok := scrollTravelForExtent(end-start, amount); ok {
			return true
		}
		if proven[0].end < open[0].end {
			proven = proven[1:]
		} else {
			open = open[1:]
		}
	}
	return false
}

func buildFilteredScrollCandidates(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, want *protocol.Node, entries []candidateEntry) []map[string]any {
	if obs == nil || obs.Projection == nil || view == nil || operation == nil || len(entries) == 0 {
		return nil
	}
	ctx := newScrollOverlayContext(obs, view)
	kept := make([]candidateEntry, 0, len(entries))
	for _, entry := range entries {
		if !isScrollCandidateOverlayOccluded(ctx, want, entry.node, operation.Direction, operation.Amount) {
			kept = append(kept, entry)
		}
	}
	if want != nil {
		sort.SliceStable(kept, func(i, j int) bool {
			return ctx.commonAncestorDepth(want, kept[i].node) > ctx.commonAncestorDepth(want, kept[j].node)
		})
	}
	return buildStaticCandidates(obs, view, operation, kept)
}
