package engine

import (
	"context"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Controlled-scroll policy. These are service constants, never request fields;
// the static check and the fresh revalidation call the same geometry function.
const (
	scrollPagePercent       = 60
	scrollHalfPercent       = 30
	scrollEdgeMarginMin     = 24
	scrollEdgeMarginDivisor = 10
	minScrollTravel         = 48
	scrollDurationMS        = 400
)

// Effect inference policy. coverage is an integer percent and consensus is an
// integer percent, so the decision is exact integer arithmetic.
const (
	effectMinAnchors         = 2
	effectMinDisplacementPx  = 2
	effectConsensusPercent   = 75
	effectNoMoveTolerancePx  = 1
	effectMaxSizeDeltaPx     = 1
	effectCPUBudgetMS        = 100
	effectReasonPostFailed   = "post_observation_failed"
	effectReasonContainer    = "container_changed"
	effectReasonInsufficient = "insufficient_anchors"
	effectReasonCoverage     = "incomplete_coverage"
	effectReasonUnstable     = "unstable_post_frame"
	effectReasonMotion       = "ambiguous_motion"
	effectReasonSize         = "size_changed"
	effectReasonCPUBudget    = "cpu_budget"
)

// scrollPoint is one logical display point of a planned trajectory.
type scrollPoint struct {
	X, Y int
}

// scrollSpan is one half-open interval on the controlled-scroll axis. All
// interval algebra in this file uses half-open boundaries.
type scrollSpan struct {
	start, end int
}

func (s scrollSpan) length() int { return s.end - s.start }

// Nearest-scope sentinels of the shared traversal. A candidate whose nearest
// scroll scope has no usable source is blocked rather than promoted outward.
const (
	scrollScopeNone     = -1
	scrollScopeUnusable = -2
)

// scrollScopeSpace is the center-line scope reduction of one immutable public
// tree. The static planner and the fresh revalidation both reduce exactly this
// way; neither scans an ancestor chain per point.
type scrollScopeSpace struct {
	vertical bool
	center   int
	public   device.Rect // published rectangle of the selected container
	base     scrollSpan  // axis interval of the raw clip intersected with public
	spans    []scrollSpan
}

func rectFromBounds(bounds *protocol.Bounds) device.Rect {
	return device.Rect{Left: bounds.Left, Top: bounds.Top,
		Right: bounds.Right, Bottom: bounds.Bottom}
}

// orthogonalCenter is the fixed off-axis coordinate of the trajectory. Only this
// coordinate is fixed by the service; the axis is planned from the safe spans.
func orthogonalCenter(rect device.Rect, vertical bool) int {
	if vertical {
		return rect.Left + rect.Width()/2
	}
	return rect.Top + rect.Height()/2
}

func orthogonalContains(rect device.Rect, center int, vertical bool) bool {
	if vertical {
		return center >= rect.Left && center < rect.Right
	}
	return center >= rect.Top && center < rect.Bottom
}

func axisSpan(rect device.Rect, vertical bool) scrollSpan {
	if vertical {
		return scrollSpan{start: rect.Top, end: rect.Bottom}
	}
	return scrollSpan{start: rect.Left, end: rect.Right}
}

func axisCoordinate(point scrollPoint, vertical bool) int {
	if vertical {
		return point.Y
	}
	return point.X
}

func orthogonalCoordinate(point scrollPoint, vertical bool) int {
	if vertical {
		return point.X
	}
	return point.Y
}

// subtractScrollSpans removes the union of the blocking intervals from the base
// interval. Empty, unordered, nested, duplicated and adjacent intervals are all
// handled by clip-sort-merge; adjacent intervals merge, so a one-pixel conflict
// never leaves a zero-width gap.
func subtractScrollSpans(base scrollSpan, blocked []scrollSpan) []scrollSpan {
	clipped := make([]scrollSpan, 0, len(blocked))
	for _, span := range blocked {
		start, end := span.start, span.end
		if start < base.start {
			start = base.start
		}
		if end > base.end {
			end = base.end
		}
		if start >= end {
			continue
		}
		clipped = append(clipped, scrollSpan{start: start, end: end})
	}
	sort.Slice(clipped, func(i, j int) bool {
		if clipped[i].start != clipped[j].start {
			return clipped[i].start < clipped[j].start
		}
		return clipped[i].end < clipped[j].end
	})
	var spans []scrollSpan
	cursor := base.start
	for _, span := range clipped {
		if span.end <= cursor {
			continue
		}
		if span.start > cursor {
			spans = append(spans, scrollSpan{start: cursor, end: span.start})
		}
		cursor = span.end
	}
	if cursor < base.end {
		spans = append(spans, scrollSpan{start: cursor, end: base.end})
	}
	return spans
}

// rawAncestorSet builds the raw ancestor set of the selected container once. A
// malformed parent relation fails closed instead of walking without a bound.
func rawAncestorSet(capture *device.Capture, want int) (map[int]bool, bool) {
	if capture == nil || want < 0 || want >= len(capture.Nodes) {
		return nil, false
	}
	ancestors := make(map[int]bool, len(capture.Nodes))
	for parent := capture.Nodes[want].Parent; parent >= 0; {
		if parent >= len(capture.Nodes) || ancestors[parent] {
			return nil, false
		}
		ancestors[parent] = true
		parent = capture.Nodes[parent].Parent
	}
	return ancestors, true
}

// scrollSafeSpans reduces the whole public tree to the provable intervals of the
// selected container's center line. The traversal updates the nearest scroll
// scope on every node before testing that node as a geometric candidate, so a
// scroll ancestor that does not cross the center line still owns its
// descendants. It never prunes a subtree by the parent rectangle.
func scrollSafeSpans(projection *Projection, want int, clipped device.Rect,
	direction string) (*scrollScopeSpace, bool) {
	space := &scrollScopeSpace{}
	switch direction {
	case protocol.ScrollDirectionUp, protocol.ScrollDirectionDown:
		space.vertical = true
	case protocol.ScrollDirectionLeft, protocol.ScrollDirectionRight:
	default:
		return nil, false
	}
	if projection == nil || projection.Tree == nil || projection.Tree.Root == nil ||
		projection.Capture == nil || want < 0 || want >= len(projection.Capture.Nodes) {
		return nil, false
	}
	if clipped.Empty() {
		return nil, false
	}
	public, ok := publicBoundsByRawIndex(projection, want)
	if !ok {
		return nil, false
	}
	space.public = rectFromBounds(public)
	shared := clipped.Intersect(space.public)
	if shared.Empty() {
		return nil, false
	}
	space.center = orthogonalCenter(clipped, space.vertical)
	if !orthogonalContains(shared, space.center, space.vertical) {
		return nil, false
	}
	space.base = axisSpan(shared, space.vertical)
	ancestors, ok := rawAncestorSet(projection.Capture, want)
	if !ok {
		return nil, false
	}
	var blocked []scrollSpan
	var walk func(node *protocol.Node, depth, nearest int)
	walk = func(node *protocol.Node, depth, nearest int) {
		if node == nil {
			return
		}
		scope := nearest
		if isScrollContainer(node) {
			if index, mapped := projection.Source[node.Ref]; mapped &&
				index >= 0 && index < len(projection.Capture.Nodes) {
				scope = index
			} else {
				scope = scrollScopeUnusable
			}
		}
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Width() > 0 && node.Bounds.Height() > 0 &&
			orthogonalContains(rectFromBounds(node.Bounds), space.center, space.vertical) {
			switch {
			case scope == scrollScopeNone:
				// No innermost scroll scope: it cannot contradict the selection.
			case scope == scrollScopeUnusable:
				blocked = append(blocked, axisSpan(rectFromBounds(node.Bounds), space.vertical))
			case scope == want || ancestors[scope]:
				// The selected container, or a raw ancestor that wraps it.
			default:
				blocked = append(blocked, axisSpan(rectFromBounds(node.Bounds), space.vertical))
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1, scope)
		}
	}
	walk(projection.Tree.Root, 0, scrollScopeNone)
	space.spans = subtractScrollSpans(space.base, blocked)
	return space, true
}

// longestSpan picks the longest provable interval. Equal lengths are ordered by
// the start side of the gesture, so the choice is deterministic.
func (space *scrollScopeSpace) longestSpan(direction string) (scrollSpan, bool) {
	preferLater := direction == protocol.ScrollDirectionDown ||
		direction == protocol.ScrollDirectionRight
	best := scrollSpan{}
	found := false
	for _, span := range space.spans {
		if !found {
			best, found = span, true
			continue
		}
		switch {
		case span.length() > best.length():
		case span.length() == best.length() && preferLater:
		case span.length() == best.length() && !preferLater && span.start < best.start:
		default:
			continue
		}
		best = span
	}
	return best, found
}

// safeRect replaces only the axis bounds of the clipped rectangle, so the fixed
// orthogonal edges of the container are preserved.
func (space *scrollScopeSpace) safeRect(clipped device.Rect, span scrollSpan) device.Rect {
	if space.vertical {
		return device.Rect{Left: clipped.Left, Top: span.start, Right: clipped.Right, Bottom: span.end}
	}
	return device.Rect{Left: span.start, Top: clipped.Top, Right: span.end, Bottom: clipped.Bottom}
}

// provesPoint reports whether one point is inside the screen, the raw clipped
// rectangle and the published container, sits exactly on the fixed center line
// and falls inside a provable interval.
func (space *scrollScopeSpace) provesPoint(point scrollPoint, clipped, screen device.Rect) bool {
	if !insideScrollRect(point, clipped) || !insideScrollRect(point, space.public) ||
		!insideScrollRect(point, screen) {
		return false
	}
	if orthogonalCoordinate(point, space.vertical) != space.center {
		return false
	}
	value := axisCoordinate(point, space.vertical)
	for _, span := range space.spans {
		if value >= span.start && value < span.end {
			return true
		}
	}
	return false
}

// provesSegment is the whole-segment proof: both endpoints must be provable and
// the closed segment between them must fit inside one single provable interval.
func (space *scrollScopeSpace) provesSegment(start, end scrollPoint, clipped, screen device.Rect) bool {
	if !space.provesPoint(start, clipped, screen) || !space.provesPoint(end, clipped, screen) {
		return false
	}
	low := axisCoordinate(start, space.vertical)
	high := axisCoordinate(end, space.vertical)
	if low > high {
		low, high = high, low
	}
	for _, span := range space.spans {
		if span.start <= low && high < span.end {
			return true
		}
	}
	return false
}

// scrollPlan is the static resolution of one controlled scroll attempt. It is
// computed before the work position is taken and never carries device state.
type scrollPlan struct {
	containerIndex int
	container      *device.RawNode
	selector       string // "ref" or "point"
	direction      string
	amount         string // never empty: page or half
	start, end     scrollPoint
}

// scrollFrame is the fresh revalidation result: the frame that was read, its
// pure projection, the proven container and the actual injected trajectory.
type scrollFrame struct {
	capture        *device.Capture
	projection     *Projection
	container      *device.RawNode
	containerIndex int
	direction      string
	amount         string
	start, end     scrollPoint
}

// scrollGeometry is the single trajectory predicate shared by the static check
// and the fresh revalidation. It works on a rectangle already clipped to the
// display and never clamps: an out-of-rectangle or out-of-screen end is a
// rejection, not a shorter gesture.
func scrollGeometry(rect, screen device.Rect, direction, amount string) (scrollPoint, scrollPoint, bool) {
	var start, end scrollPoint
	switch amount {
	case "", protocol.ScrollAmountPage:
		amount = protocol.ScrollAmountPage
	case protocol.ScrollAmountHalf:
	default:
		return start, end, false
	}
	if rect.Empty() {
		return start, end, false
	}
	percent := scrollPagePercent
	if amount == protocol.ScrollAmountHalf {
		percent = scrollHalfPercent
	}
	vertical := false
	switch direction {
	case protocol.ScrollDirectionUp, protocol.ScrollDirectionDown:
		vertical = true
	case protocol.ScrollDirectionLeft, protocol.ScrollDirectionRight:
	default:
		return start, end, false
	}
	extent := rect.Width()
	if vertical {
		extent = rect.Height()
	}
	margin := extent / scrollEdgeMarginDivisor
	if margin < scrollEdgeMarginMin {
		margin = scrollEdgeMarginMin
	}
	usable := extent - 2*margin
	if usable <= 0 {
		return start, end, false
	}
	travel := usable * percent / 100
	if travel < minScrollTravel {
		return start, end, false
	}
	if vertical {
		x := rect.Left + rect.Width()/2
		switch direction {
		case protocol.ScrollDirectionDown:
			start = scrollPoint{X: x, Y: rect.Bottom - margin}
			end = scrollPoint{X: x, Y: start.Y - travel}
		case protocol.ScrollDirectionUp:
			start = scrollPoint{X: x, Y: rect.Top + margin}
			end = scrollPoint{X: x, Y: start.Y + travel}
		}
	} else {
		y := rect.Top + rect.Height()/2
		switch direction {
		case protocol.ScrollDirectionRight:
			start = scrollPoint{X: rect.Right - margin, Y: y}
			end = scrollPoint{X: start.X - travel, Y: y}
		case protocol.ScrollDirectionLeft:
			start = scrollPoint{X: rect.Left + margin, Y: y}
			end = scrollPoint{X: start.X + travel, Y: y}
		}
	}
	if !insideScrollRect(start, rect) || !insideScrollRect(end, rect) ||
		!insideScrollRect(start, screen) || !insideScrollRect(end, screen) {
		return scrollPoint{}, scrollPoint{}, false
	}
	return start, end, true
}

func insideScrollRect(point scrollPoint, rect device.Rect) bool {
	return point.X >= rect.Left && point.X < rect.Right &&
		point.Y >= rect.Top && point.Y < rect.Bottom
}

// scrollEdges names the moving edge of one direction for the controlled plan
// log; it is a closed enum and never a coordinate.
func scrollEdges(direction string) (string, string) {
	switch direction {
	case protocol.ScrollDirectionDown:
		return "bottom", "top"
	case protocol.ScrollDirectionUp:
		return "top", "bottom"
	case protocol.ScrollDirectionRight:
		return "right", "left"
	case protocol.ScrollDirectionLeft:
		return "left", "right"
	}
	return "", ""
}

// isScrollContainer is the single container predicate: a public node directly
// usable as the scope of one controlled scroll attempt. No ancestor promotion.
func isScrollContainer(node *protocol.Node) bool {
	return node != nil && node.Role == protocol.RoleScroll && node.Role != protocol.RoleRoot &&
		!node.Context && node.Bounds != nil && hasAction(node, protocol.ActionSwipe) &&
		(node.Enabled == nil || *node.Enabled)
}

// resolveScrollPlan validates the scroll branch against the observation the
// caller read and computes the initial trajectory. It performs no device I/O
// and never claims; the returned plan is the only input of fresh revalidation.
func resolveScrollPlan(obs *observationState, operation *protocol.Operation) (*scrollPlan, *protocol.APIError) {
	if operation.Container == nil {
		return nil, protocol.Invalid("scroll 缺少 container")
	}
	if operation.Target != nil || operation.From != nil || operation.To != nil ||
		operation.Key != "" || operation.Text != "" || operation.DurationMS != 0 {
		return nil, protocol.Invalid("scroll 不接受 target/from/to/key/text/duration_ms")
	}
	direction := operation.Direction
	switch direction {
	case protocol.ScrollDirectionUp, protocol.ScrollDirectionDown,
		protocol.ScrollDirectionLeft, protocol.ScrollDirectionRight:
	default:
		return nil, protocol.Invalid("scroll direction 无效")
	}
	amount := operation.Amount
	switch amount {
	case "":
		amount = protocol.ScrollAmountPage
	case protocol.ScrollAmountPage, protocol.ScrollAmountHalf:
	default:
		return nil, protocol.Invalid("scroll amount 无效")
	}
	if obs == nil || obs.Projection == nil || obs.Projection.Tree == nil ||
		obs.Projection.Tree.Root == nil || obs.Projection.Capture == nil {
		return nil, unsupportedScrollContainer("该观察没有可用的滚动容器")
	}
	projection := obs.Projection
	selector := "point"
	var container *protocol.Node
	if operation.Container.UseRef {
		selector = "ref"
		container = findPublicNode(projection.Tree.Root, operation.Container.Ref)
		if container == nil {
			return nil, protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点不存在")
		}
		if !isScrollContainer(container) {
			return nil, unsupportedScrollContainer("目标节点不是可滚动容器")
		}
	} else {
		x, y := operation.Container.X, operation.Container.Y
		if !inScreen(x, y, obs.Screen) {
			return nil, protocol.NewError(422, protocol.CodeInvalidCoordinates, "坐标超出目标屏幕")
		}
		index, apiErr := scrollContainerAtPoint(projection, x, y)
		if apiErr != nil {
			return nil, apiErr
		}
		container = publicNodeByRawIndex(projection, index)
		if container == nil || !isScrollContainer(container) {
			return nil, unsupportedScrollContainer("该坐标没有可用的滚动容器")
		}
	}
	index, mapped := projection.Source[container.Ref]
	if !mapped || index < 0 || index >= len(projection.Capture.Nodes) {
		return nil, unsupportedScrollContainer("滚动容器来源不可用")
	}
	raw := &projection.Capture.Nodes[index]
	screen := projection.Capture.Display.Bounds()
	clipped := raw.Bounds.Intersect(screen)
	if clipped.Empty() {
		return nil, unsupportedScrollContainer("滚动容器没有可用几何")
	}
	// The static plan authorizes one attempt: the longest provable interval on
	// the center line, then the geometry on it. Fresh revalidation preserves the
	// segment while it remains proven and otherwise replans in the same scope.
	space, ok := scrollSafeSpans(projection, index, clipped, direction)
	if !ok {
		return nil, unsupportedScrollContainer("滚动容器没有可用几何")
	}
	span, ok := space.longestSpan(direction)
	if !ok {
		return nil, unsupportedScrollTrajectory()
	}
	start, end, ok := scrollGeometry(space.safeRect(clipped, span), screen, direction, amount)
	if !ok || !space.provesSegment(start, end, clipped, screen) {
		return nil, unsupportedScrollTrajectory()
	}
	return &scrollPlan{
		containerIndex: index, container: raw, selector: selector,
		direction: direction, amount: amount, start: start, end: end,
	}, nil
}

// scrollContainerAtPoint reduces every public candidate containing the point to
// its nearest self-or-ancestor scroll container and dedupes by raw source node.
// Two distinct containers are ambiguous, never guessed.
func scrollContainerAtPoint(projection *Projection, x, y int) (int, *protocol.APIError) {
	scopes := map[int]struct{}{}
	var walk func(node *protocol.Node, depth int, ancestors []*protocol.Node)
	walk = func(node *protocol.Node, depth int, ancestors []*protocol.Node) {
		if node == nil {
			return
		}
		path := append(ancestors, node)
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Contains(x, y) {
			for i := len(path) - 1; i >= 0; i-- {
				if !isScrollContainer(path[i]) {
					continue
				}
				if index, mapped := projection.Source[path[i].Ref]; mapped {
					scopes[index] = struct{}{}
				}
				break
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1, path)
		}
	}
	walk(projection.Tree.Root, 0, nil)
	switch len(scopes) {
	case 0:
		return -1, unsupportedScrollContainer("该坐标不落在可滚动容器内")
	case 1:
		for index := range scopes {
			return index, nil
		}
	}
	return -1, protocol.NewError(422, protocol.CodeAmbiguousPoint,
		"坐标落在多个不同的滚动容器上，请使用 container.ref 指定容器")
}

func unsupportedScrollContainer(message string) *protocol.APIError {
	return protocol.NewError(422, protocol.CodeNodeUnsupported, message)
}

// unsupportedScrollTrajectory is the static rejection of a container whose center
// line has no provable trajectory for the requested amount. It deliberately does
// not claim the container cannot scroll on the device: only the published tree is
// proven.
func unsupportedScrollTrajectory() *protocol.APIError {
	return protocol.NewError(422, protocol.CodeNodeUnsupported,
		"无法在该容器中心线上生成可证明的滚动轨迹")
}

// publicNodeByRawIndex maps one raw capture node back to its published node.
// Only published, non-context nodes are mapped; the synthetic root maps to the
// raw root.
func publicNodeByRawIndex(projection *Projection, index int) *protocol.Node {
	if projection == nil || projection.Tree == nil || projection.Tree.Root == nil {
		return nil
	}
	for ref, rawIndex := range projection.Source {
		if rawIndex == index {
			return findPublicNode(projection.Tree.Root, ref)
		}
	}
	return nil
}

// publicBoundsByRawIndex returns the published bounds of one raw node. A node
// without a positive published rectangle has no usable public bounds.
func publicBoundsByRawIndex(projection *Projection, index int) (*protocol.Bounds, bool) {
	node := publicNodeByRawIndex(projection, index)
	if node == nil || node.Bounds == nil {
		return nil, false
	}
	return node.Bounds, true
}

// intervalOverlapRatio is relative to the shorter interval, so an axial
// stretch does not by itself make a scroll container look like a different
// scope. Bounds are half-open.
func intervalOverlapRatio(a, b scrollSpan) float64 {
	if a.length() <= 0 || b.length() <= 0 {
		return 0
	}
	overlap := min(a.end, b.end) - max(a.start, b.start)
	if overlap <= 0 {
		return 0
	}
	return float64(overlap) / float64(min(a.length(), b.length()))
}

// sameScrollScope separates container identity from trajectory planning. The
// container may stretch or move along the requested axis, but a meaningful
// axial overlap and stable cross-axis edges are both required.
func sameScrollScope(oldRect, freshRect, oldScreen, freshScreen device.Rect,
	direction string) bool {
	if oldRect.Empty() || freshRect.Empty() {
		return false
	}
	oldClipped := oldRect.Intersect(oldScreen)
	freshClipped := freshRect.Intersect(freshScreen)
	if oldClipped.Empty() || freshClipped.Empty() {
		return false
	}
	switch direction {
	case protocol.ScrollDirectionUp, protocol.ScrollDirectionDown:
		oldAxis := scrollSpan{start: oldClipped.Top, end: oldClipped.Bottom}
		freshAxis := scrollSpan{start: freshClipped.Top, end: freshClipped.Bottom}
		if intervalOverlapRatio(oldAxis, freshAxis) < 0.5 {
			return false
		}
		tolerance := max(8, min(oldClipped.Width(), freshClipped.Width())/50)
		return absInt(oldClipped.Left-freshClipped.Left) <= tolerance &&
			absInt(oldClipped.Right-freshClipped.Right) <= tolerance
	case protocol.ScrollDirectionLeft, protocol.ScrollDirectionRight:
		oldAxis := scrollSpan{start: oldClipped.Left, end: oldClipped.Right}
		freshAxis := scrollSpan{start: freshClipped.Left, end: freshClipped.Right}
		if intervalOverlapRatio(oldAxis, freshAxis) < 0.5 {
			return false
		}
		tolerance := max(8, min(oldClipped.Height(), freshClipped.Height())/50)
		return absInt(oldClipped.Top-freshClipped.Top) <= tolerance &&
			absInt(oldClipped.Bottom-freshClipped.Bottom) <= tolerance
	}
	return false
}

// revalidateScrollTarget proves the container is still the same source node,
// then proves or replans its trajectory from the fresh public tree. A rejection
// returns the frame it already read so the caller path can deliver it as fresh
// evidence.
func (e *Engine) revalidateScrollTarget(work *workState, obs *observationState,
	plan *scrollPlan) (*device.Capture, *scrollFrame, *protocol.APIError) {
	if work == nil || plan == nil || plan.container == nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}
	if obs == nil || obs.Projection == nil || obs.Projection.Capture == nil ||
		obs.Projection.Tree == nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "实时目标重验失败")
	}
	capture, _, apiErr := e.captureForActRevalidation(work)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	if err := foregroundMatches(obs.Foreground, capture.Foreground); err != nil {
		return capture, nil, err
	}
	qualityState := protocol.QualityUnknown
	if obs.Quality != nil && obs.Quality.State != "" {
		qualityState = obs.Quality.State
	}
	revoked := func() bool { return e.workRevoked(work) }
	match, matchState := e.probeActNode(
		work.Ctx, revoked, obs.Projection.Capture, capture, plan.container)
	switch matchState {
	case actProbeUnique:
	case actProbeCancelled:
		return capture, nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	case actProbeNone, actProbeAmbiguous:
		e.logScrollRevalidate(work.OperationID, false, false, false, false, qualityState)
		return capture, nil, targetChangedScroll()
	default:
		return capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}
	freshContainer := match.raw
	freshIndex := indexOf(capture, freshContainer)
	if freshIndex < 0 {
		return capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}
	oldScreen := obs.Projection.Capture.Display.Bounds()
	newScreen := capture.Display.Bounds()
	freshClipped := freshContainer.Bounds.Intersect(newScreen)
	if freshClipped.Empty() ||
		!sameScrollScope(plan.container.Bounds, freshContainer.Bounds,
			oldScreen, newScreen, plan.direction) {
		e.logScrollRevalidate(work.OperationID, true, false, false, false, qualityState)
		return capture, nil, targetChangedScroll()
	}
	projector := work.Projector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	// Preview never advances ref continuity: the container proof authorizes
	// this attempt and must not shift the refs of already published trees.
	projection, err := projector.Preview(capture)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return capture, nil, stoppedActRevalidation()
	}
	if err != nil || projection == nil || projection.Tree == nil {
		return capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	containerPublic := publicNodeByRawIndex(projection, freshIndex)
	if containerPublic == nil || containerPublic.Bounds == nil || !isScrollContainer(containerPublic) {
		e.logScrollRevalidate(work.OperationID, true, false, false, false, qualityState)
		return capture, nil, targetChangedScroll()
	}
	space, ok := scrollSafeSpans(projection, freshIndex, freshClipped, plan.direction)
	if !ok {
		e.logScrollRevalidate(work.OperationID, true, false, false, false, qualityState)
		return capture, nil, targetChangedScroll()
	}
	start, end := plan.start, plan.end
	startProven := space.provesPoint(start, freshClipped, newScreen)
	endProven := space.provesPoint(end, freshClipped, newScreen)
	proven := space.provesSegment(start, end, freshClipped, newScreen)
	if !proven {
		span, hasSpan := space.longestSpan(plan.direction)
		if !hasSpan {
			e.logScrollRevalidate(
				work.OperationID, true, startProven, endProven, false, qualityState)
			return capture, nil, targetChangedScroll()
		}
		safe := space.safeRect(freshClipped, span)
		var geometryOK bool
		start, end, geometryOK = scrollGeometry(
			safe, newScreen, plan.direction, plan.amount)
		if !geometryOK || !space.provesSegment(start, end, freshClipped, newScreen) {
			e.logScrollRevalidate(
				work.OperationID, true, startProven, endProven, false, qualityState)
			return capture, nil, targetChangedScroll()
		}
		startProven = space.provesPoint(start, freshClipped, newScreen)
		endProven = space.provesPoint(end, freshClipped, newScreen)
		proven = true
	}
	e.logScrollRevalidate(work.OperationID, true, startProven, endProven, proven, qualityState)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return capture, nil, stoppedActRevalidation()
	}
	return capture, &scrollFrame{
		capture: capture, projection: projection, container: freshContainer,
		containerIndex: freshIndex, direction: plan.direction, amount: plan.amount,
		start: start, end: end,
	}, nil
}

func targetChangedScroll() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeTargetChanged,
		"滚动容器或实际轨迹已变化，本次未派发")
}

// dispatchScroll injects the fresh center-line trajectory. The physical
// primitive and the ledger kind stay "swipe"; the semantic action is carried by
// the operation fact. The outcome mapping is exactly the swipe mapping: nothing
// dispatched is not_executed, anything unconfirmed after dispatch is uncertain.
func (e *Engine) dispatchScroll(work *workState, frame *scrollFrame) (string, *protocol.APIError) {
	if frame == nil {
		return OutcomeNotExecuted, targetChangedScroll()
	}
	session := e.sessionForWork(work)
	if session == nil || e.workRevoked(work) {
		return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if apiErr := e.beginUIDispatch(work, "swipe"); apiErr != nil {
		return OutcomeNotExecuted, apiErr
	}
	receipt, dispatchErr := e.dev.Swipe(work.Ctx, session.Display.ID,
		frame.start.X, frame.start.Y, frame.end.X, frame.end.Y, scrollDurationMS)
	e.recordUIReceipt(work, receipt)
	if receipt == device.DispatchNotDispatched {
		return OutcomeNotExecuted, protocol.NewError(503, protocol.CodeDeviceUnavailable, "输入未派发")
	}
	if dispatchErr != nil || receipt != device.DispatchTerminated {
		return OutcomeUncertain, actionUncertain()
	}
	if !e.confirmDispatch(work) {
		return OutcomeUncertain, actionUncertain()
	}
	return OutcomeExecuted, nil
}

// ---------------------------------------------------------------- effect

// contentKey is the bounds-free content identity of one source node. A key must
// be unique on both sides before a unit may be matched.
type contentKey struct {
	Package, Class, ResourceID, Text, Description string
}

func contentKeyOf(node *device.RawNode) contentKey {
	return contentKey{
		Package: node.Package, Class: node.ClassName, ResourceID: node.ResourceID,
		Text: node.Text, Description: node.Description,
	}
}

// contentChainOf returns the content keys of the ancestors, nearest first. It is
// bounded by the capture length so a malformed parent cycle cannot loop.
func contentChainOf(capture *device.Capture, index int) []contentKey {
	if capture == nil || index < 0 || index >= len(capture.Nodes) {
		return nil
	}
	var chain []contentKey
	parent := capture.Nodes[index].Parent
	for count := 0; parent >= 0 && parent < len(capture.Nodes) && count < len(capture.Nodes); count++ {
		chain = append(chain, contentKeyOf(&capture.Nodes[parent]))
		parent = capture.Nodes[parent].Parent
	}
	return chain
}

func sameContentChain(a, b []contentKey) bool {
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

// matchScrollContainer looks for exactly one post node with the same content
// key and the same content-key ancestry as the pre container. Bounds are
// ignored here; they are compared separately.
func matchScrollContainer(pre *scrollFrame, post *device.Capture) (int, bool) {
	if pre == nil || pre.capture == nil || pre.containerIndex < 0 ||
		pre.containerIndex >= len(pre.capture.Nodes) || post == nil {
		return -1, false
	}
	wantKey := contentKeyOf(&pre.capture.Nodes[pre.containerIndex])
	wantChain := contentChainOf(pre.capture, pre.containerIndex)
	found := -1
	for index := range post.Nodes {
		if contentKeyOf(&post.Nodes[index]) != wantKey {
			continue
		}
		if !sameContentChain(contentChainOf(post, index), wantChain) {
			continue
		}
		if found >= 0 {
			return -1, false
		}
		found = index
	}
	return found, found >= 0
}

// scrollContentUnit is one leaf content unit of the container subtree. Its
// bounds are clipped to the container; only clipped size and center are used.
// raw keeps the source rectangle before the screen and container clipping, so
// the classification can prove that the axis endpoints did not move because of
// a clipping window.
type scrollContentUnit struct {
	key  contentKey
	rect device.Rect
	raw  device.Rect
}

func (u scrollContentUnit) width() int  { return u.rect.Width() }
func (u scrollContentUnit) height() int { return u.rect.Height() }

func (u scrollContentUnit) axisCenter(vertical bool) int {
	if vertical {
		return (u.rect.Top + u.rect.Bottom) / 2
	}
	return (u.rect.Left + u.rect.Right) / 2
}

// axisClipped reports whether the axis endpoints differ from the source
// rectangle, that is whether a clipping window changed them.
func (u scrollContentUnit) axisClipped(vertical bool) bool {
	if vertical {
		return u.rect.Top != u.raw.Top || u.rect.Bottom != u.raw.Bottom
	}
	return u.rect.Left != u.raw.Left || u.rect.Right != u.raw.Right
}

// scrollContentUnits collects the leaf content units of one published container
// subtree: non-context, non-root, non-input, unprotected, positive clipped
// bounds, a non-empty label or a public resource, and no descendant that is
// itself a content unit.
func scrollContentUnits(projection *Projection, containerIndex int) []scrollContentUnit {
	if projection == nil || projection.Capture == nil || projection.Tree == nil ||
		projection.Tree.Root == nil {
		return nil
	}
	container := publicNodeByRawIndex(projection, containerIndex)
	if container == nil || container.Bounds == nil {
		return nil
	}
	containerRect := device.Rect{
		Left: container.Bounds.Left, Top: container.Bounds.Top,
		Right: container.Bounds.Right, Bottom: container.Bounds.Bottom,
	}
	var units []scrollContentUnit
	var walk func(node *protocol.Node) bool
	walk = func(node *protocol.Node) bool {
		if node == nil {
			return false
		}
		childUnit := false
		for _, child := range node.Children {
			if walk(child) {
				childUnit = true
			}
		}
		clip, eligible := scrollUnitBounds(node, containerRect)
		if eligible && !childUnit {
			if index, mapped := projection.Source[node.Ref]; mapped &&
				index >= 0 && index < len(projection.Capture.Nodes) {
				raw := &projection.Capture.Nodes[index]
				source := raw.RawBounds
				if source.Empty() {
					source = raw.Bounds
				}
				units = append(units, scrollContentUnit{
					key: contentKeyOf(raw), rect: clip, raw: source,
				})
			}
		}
		return eligible || childUnit
	}
	for _, child := range container.Children {
		walk(child)
	}
	return units
}

func scrollUnitBounds(node *protocol.Node, container device.Rect) (device.Rect, bool) {
	if node == nil || node.Context || node.Role == protocol.RoleRoot ||
		node.Role == protocol.RoleInput || node.Bounds == nil {
		return device.Rect{}, false
	}
	if node.Protected != nil && *node.Protected {
		return device.Rect{}, false
	}
	if len(node.Label) == 0 && node.Resource == nil {
		return device.Rect{}, false
	}
	clip := device.Rect{
		Left: node.Bounds.Left, Top: node.Bounds.Top,
		Right: node.Bounds.Right, Bottom: node.Bounds.Bottom,
	}.Intersect(container)
	if clip.Empty() {
		return device.Rect{}, false
	}
	return clip, true
}

type matchedScrollUnit struct {
	pre, post scrollContentUnit
}

// matchScrollUnits matches units by bounds-free content key. A key that is not
// unique on either side never matches; partial coverage is allowed.
func matchScrollUnits(pre, post []scrollContentUnit) []matchedScrollUnit {
	preCount := make(map[contentKey]int, len(pre))
	for _, unit := range pre {
		preCount[unit.key]++
	}
	postCount := make(map[contentKey]int, len(post))
	postByKey := make(map[contentKey]scrollContentUnit, len(post))
	for _, unit := range post {
		postCount[unit.key]++
		postByKey[unit.key] = unit
	}
	var matched []matchedScrollUnit
	for _, unit := range pre {
		if preCount[unit.key] != 1 || postCount[unit.key] != 1 {
			continue
		}
		other, ok := postByKey[unit.key]
		if !ok {
			continue
		}
		matched = append(matched, matchedScrollUnit{pre: unit, post: other})
	}
	return matched
}

// scrollEffectResult is the complete diagnostic of one effect inference. Only
// State reaches the public result; the counters and the reason stay in logs.
type scrollEffectResult struct {
	State    string
	Anchors  int
	Coverage int
	Axis     string
	Reason   string
}

func scrollAxis(direction string) string {
	switch direction {
	case protocol.ScrollDirectionLeft, protocol.ScrollDirectionRight:
		return "x"
	default:
		return "y"
	}
}

// touchScrollAxisEdge reports whether a unit sits on the low or high axial edge
// of the viewport. Only the request axis is inspected: filling the orthogonal
// axis is not an axial edge.
func touchScrollAxisEdge(rect, viewport device.Rect, vertical bool) bool {
	if vertical {
		return rect.Top-viewport.Top <= effectMaxSizeDeltaPx ||
			viewport.Bottom-rect.Bottom <= effectMaxSizeDeltaPx
	}
	return rect.Left-viewport.Left <= effectMaxSizeDeltaPx ||
		viewport.Right-rect.Right <= effectMaxSizeDeltaPx
}

// orthogonalScrollEdgesStable is the container guard of the request axis: the
// two off-axis edges must not move, while the axial edges may change.
func orthogonalScrollEdgesStable(pre, post device.Rect, vertical bool) bool {
	if vertical {
		return absInt(pre.Left-post.Left) <= effectMaxSizeDeltaPx &&
			absInt(pre.Right-post.Right) <= effectMaxSizeDeltaPx
	}
	return absInt(pre.Top-post.Top) <= effectMaxSizeDeltaPx &&
		absInt(pre.Bottom-post.Bottom) <= effectMaxSizeDeltaPx
}

// rectsEqualWithin reports whether all four edges stay inside the tolerance.
func rectsEqualWithin(a, b device.Rect, tolerance int) bool {
	return absInt(a.Left-b.Left) <= tolerance && absInt(a.Top-b.Top) <= tolerance &&
		absInt(a.Right-b.Right) <= tolerance && absInt(a.Bottom-b.Bottom) <= tolerance
}

// scrollEligibleUnit reports whether one matched pair may contribute positive
// displacement evidence. A pair must be size stable, must not touch an axial
// viewport edge in either frame, and must not have had its axis endpoints moved
// by a clipping window. An equal-size clipped window can still drift with the
// viewport, so equal size alone is not enough.
func scrollEligibleUnit(pair matchedScrollUnit, preViewport, postViewport device.Rect,
	vertical bool) bool {
	if absInt(pair.post.width()-pair.pre.width()) > effectMaxSizeDeltaPx ||
		absInt(pair.post.height()-pair.pre.height()) > effectMaxSizeDeltaPx {
		return false
	}
	if touchScrollAxisEdge(pair.pre.rect, preViewport, vertical) ||
		touchScrollAxisEdge(pair.post.rect, postViewport, vertical) {
		return false
	}
	return !pair.pre.axisClipped(vertical) && !pair.post.axisClipped(vertical)
}

func scrollSign(value int) int {
	if value < 0 {
		return 0
	}
	return 1
}

// computeScrollEffect is the pure CPU effect inference. It reads only the two
// captured frames, performs no device I/O and never rewrites the outcome.
func computeScrollEffect(pre *scrollFrame, post *observationState,
	quality *protocol.Quality, direction string, ctx context.Context, deadline time.Time) scrollEffectResult {
	result := scrollEffectResult{
		State: protocol.EffectUnknown, Axis: scrollAxis(direction),
		Reason: effectReasonPostFailed,
	}
	if pre == nil || pre.capture == nil || pre.projection == nil || pre.direction == "" ||
		pre.containerIndex < 0 || pre.containerIndex >= len(pre.capture.Nodes) ||
		post == nil || post.Projection == nil || post.Projection.Capture == nil ||
		post.Projection.Tree == nil || post.Projection.Tree.Root == nil {
		return result
	}
	expired := func() bool {
		if ctx != nil && ctx.Err() != nil {
			return true
		}
		return time.Now().After(deadline)
	}
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	postIndex, matchedContainer := matchScrollContainer(pre, post.Projection.Capture)
	if !matchedContainer {
		result.Reason = effectReasonContainer
		return result
	}
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	// The container guard allows the viewport to stretch along the request axis,
	// because a collapsing or expanding bar is a legitimate accompaniment of the
	// gesture. The off-axis edges and a positive overlap must still hold, and the
	// public container must still be a scroll container on both sides.
	preContainer := publicNodeByRawIndex(pre.projection, pre.containerIndex)
	postContainer := publicNodeByRawIndex(post.Projection, postIndex)
	if preContainer == nil || postContainer == nil ||
		!isScrollContainer(preContainer) || !isScrollContainer(postContainer) ||
		preContainer.Bounds == nil || postContainer.Bounds == nil {
		result.Reason = effectReasonContainer
		return result
	}
	vertical := scrollAxis(direction) == "y"
	preRect := rectFromBounds(preContainer.Bounds)
	postRect := rectFromBounds(postContainer.Bounds)
	if !orthogonalScrollEdgesStable(preRect, postRect, vertical) ||
		preRect.Intersect(postRect).Empty() {
		result.Reason = effectReasonContainer
		return result
	}
	containerUnchanged := rectsEqualWithin(preRect, postRect, effectMaxSizeDeltaPx)
	preUnits := scrollContentUnits(pre.projection, pre.containerIndex)
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	postUnits := scrollContentUnits(post.Projection, postIndex)
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	units := max(len(preUnits), len(postUnits))
	matched := matchScrollUnits(preUnits, postUnits)
	// The published counters always describe the full matched set, never the
	// filtered positive-evidence subset.
	result.Anchors = len(matched)
	if units > 0 {
		result.Coverage = len(matched) * 100 / units
	}
	if len(matched) < effectMinAnchors {
		result.Reason = effectReasonInsufficient
		return result
	}
	allUnitsRigid := true
	maxMoveAll := 0
	eligible := make([]matchedScrollUnit, 0, len(matched))
	for _, pair := range matched {
		widthDelta := absInt(pair.post.width() - pair.pre.width())
		heightDelta := absInt(pair.post.height() - pair.pre.height())
		axisDelta, orthogonalDelta := widthDelta, heightDelta
		if vertical {
			axisDelta, orthogonalDelta = heightDelta, widthDelta
		}
		if widthDelta > effectMaxSizeDeltaPx || heightDelta > effectMaxSizeDeltaPx {
			allUnitsRigid = false
		}
		// Only an axial size change that touches an axial viewport edge may be
		// excluded: an equal-size clipped window can drift with the viewport, and
		// an interior deformation cannot be explained by clipping at all.
		if orthogonalDelta > effectMaxSizeDeltaPx ||
			(axisDelta > effectMaxSizeDeltaPx &&
				!touchScrollAxisEdge(pair.pre.rect, preRect, vertical) &&
				!touchScrollAxisEdge(pair.post.rect, postRect, vertical)) {
			result.Reason = effectReasonSize
			return result
		}
		delta := pair.post.axisCenter(vertical) - pair.pre.axisCenter(vertical)
		if absInt(delta) > maxMoveAll {
			maxMoveAll = absInt(delta)
		}
		if scrollEligibleUnit(pair, preRect, postRect, vertical) {
			eligible = append(eligible, pair)
		}
	}
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	// Positive evidence needs both frames of reference: screen displacement alone
	// would count content that merely follows the container, and container
	// relative displacement alone would count a viewport that only changed size.
	preOrigin, postOrigin := preRect.Top, postRect.Top
	if !vertical {
		preOrigin, postOrigin = preRect.Left, postRect.Left
	}
	var buckets [2][2]int
	for _, pair := range eligible {
		absolute := pair.post.axisCenter(vertical) - pair.pre.axisCenter(vertical)
		relative := (pair.post.axisCenter(vertical) - postOrigin) -
			(pair.pre.axisCenter(vertical) - preOrigin)
		if absInt(absolute) < effectMinDisplacementPx || absInt(relative) < effectMinDisplacementPx {
			continue
		}
		buckets[scrollSign(absolute)][scrollSign(relative)]++
	}
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	dominant := 0
	for _, row := range buckets {
		for _, count := range row {
			if count > dominant {
				dominant = count
			}
		}
	}
	if len(eligible) >= effectMinAnchors && dominant >= effectMinAnchors &&
		dominant*100 >= len(eligible)*effectConsensusPercent {
		if expired() {
			result.Reason = effectReasonCPUBudget
			return result
		}
		result.State = protocol.EffectObserved
		result.Reason = ""
		return result
	}
	// Negative evidence keeps the pre-proposal semantics: the four container
	// edges and every matched unit must be stable, and it uses the full matched
	// set with absolute centers. It cannot prove that raw content stood still
	// when a clipping window hides it.
	if !containerUnchanged {
		result.Reason = effectReasonContainer
		return result
	}
	if !allUnitsRigid {
		result.Reason = effectReasonSize
		return result
	}
	if quality != nil && quality.State == protocol.QualityStable &&
		len(preUnits) == len(postUnits) && len(matched) == len(preUnits) &&
		len(matched) >= effectMinAnchors && maxMoveAll <= effectNoMoveTolerancePx {
		if expired() {
			result.Reason = effectReasonCPUBudget
			return result
		}
		result.State = protocol.EffectNotObserved
		result.Reason = ""
		return result
	}
	switch {
	case len(preUnits) != len(postUnits) || len(matched) != units:
		result.Reason = effectReasonCoverage
	case quality == nil || quality.State != protocol.QualityStable:
		result.Reason = effectReasonUnstable
	case len(eligible) < effectMinAnchors:
		result.Reason = effectReasonInsufficient
	default:
		result.Reason = effectReasonMotion
	}
	return result
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// inferScrollEffect runs the pure inference and records the controlled log. The
// public result only ever carries the three-state fact.
func (e *Engine) inferScrollEffect(work *workState, pre *scrollFrame, post *observationState,
	public *protocol.Observation, direction string) string {
	ctx := context.Background()
	operationID := ""
	if work != nil {
		operationID = work.OperationID
		if work.Ctx != nil {
			ctx = work.Ctx
		}
	}
	var quality *protocol.Quality
	if public != nil {
		quality = public.Quality
	}
	result := computeScrollEffect(pre, post, quality, direction, ctx,
		time.Now().Add(effectCPUBudgetMS*time.Millisecond))
	e.logScrollEffect(operationID, result)
	return result.State
}

// ---------------------------------------------------------------- logging

func (e *Engine) logScrollPlan(operationID string, plan *scrollPlan) {
	if plan == nil {
		return
	}
	startEdge, endEdge := scrollEdges(plan.direction)
	e.log.Event(eventlog.Debug, "scroll.plan",
		eventlog.Str("operation_id", operationID),
		eventlog.Str("direction", plan.direction),
		eventlog.Str("amount", plan.amount),
		eventlog.Str("container_role", string(protocol.RoleScroll)),
		eventlog.Str("start_edge", startEdge),
		eventlog.Str("end_edge", endEdge))
}

// logScrollRevalidate records the container proof, both endpoint scope proofs and
// the whole-segment proof. The endpoint flags never stand for the segment: a
// segment rejection can carry proven endpoints.
func (e *Engine) logScrollRevalidate(operationID string, containerProven, startProven,
	endProven, proven bool, quality string) {
	e.log.Event(eventlog.Debug, "scroll.revalidate",
		eventlog.Str("operation_id", operationID),
		eventlog.Bool("container_proven", containerProven),
		eventlog.Bool("start_scope_proven", startProven),
		eventlog.Bool("end_scope_proven", endProven),
		eventlog.Bool("proven", proven),
		eventlog.Str("quality", quality))
}

func (e *Engine) logScrollEffect(operationID string, result scrollEffectResult) {
	fields := []eventlog.Field{
		eventlog.Str("operation_id", operationID),
		eventlog.Str("state", result.State),
		eventlog.Int("anchors", result.Anchors),
		eventlog.Int("coverage", result.Coverage),
		eventlog.Str("dominant_axis", result.Axis),
	}
	if result.Reason != "" {
		fields = append(fields, eventlog.Str("reason", result.Reason))
	}
	e.log.Event(eventlog.Debug, "scroll.effect", fields...)
}
