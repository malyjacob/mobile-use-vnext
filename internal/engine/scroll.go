package engine

import (
	"context"
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

// scrollPoint is one logical display point of the fixed trajectory.
type scrollPoint struct {
	X, Y int
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
// caller read and computes the fixed trajectory. It performs no device I/O and
// never claims; the returned plan is the only input of the fresh revalidation.
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
		if container == nil {
			return nil, unsupportedScrollContainer("该坐标没有可用的滚动容器")
		}
	}
	index, mapped := projection.Source[container.Ref]
	if !mapped || index < 0 || index >= len(projection.Capture.Nodes) {
		return nil, unsupportedScrollContainer("滚动容器来源不可用")
	}
	raw := &projection.Capture.Nodes[index]
	clipped := raw.Bounds.Intersect(projection.Capture.Display.Bounds())
	if clipped.Empty() {
		return nil, unsupportedScrollContainer("滚动容器没有可用几何")
	}
	start, end, ok := scrollGeometry(clipped, projection.Capture.Display.Bounds(), direction, amount)
	if !ok {
		return nil, unsupportedScrollContainer("滚动容器不支持该滚动方向")
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

// revalidateScrollTarget proves the container is still the same node in the
// live frame, recomputes the trajectory with the same geometry function and
// proves that the actual injected start and end still resolve to that one
// container. A rejection returns the frame it already read so the caller path
// can deliver it as fresh evidence.
func (e *Engine) revalidateScrollTarget(work *workState, obs *observationState,
	plan *scrollPlan) (*device.Capture, *scrollFrame, *protocol.APIError) {
	if work == nil || plan == nil || plan.container == nil {
		return nil, nil, targetChangedScroll()
	}
	if e.workRevoked(work) {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	session := e.sessionForWork(work)
	if session == nil {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	if obs == nil || obs.Projection == nil || obs.Projection.Capture == nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "实时目标重验失败")
	}
	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if err != nil || !state.Exists {
		return nil, nil, protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
	}
	capture, err := e.dev.Capture(work.Ctx, session.Display)
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "实时目标重验失败")
	}
	if err := foregroundMatches(obs.Foreground, capture.Foreground); err != nil {
		return &capture, nil, err
	}
	qualityState := protocol.QualityUnknown
	if obs.Quality != nil && obs.Quality.State != "" {
		qualityState = obs.Quality.State
	}
	freshContainer, matched := matchFreshNode(&capture, obs.Projection.Capture, plan.container)
	if !matched {
		e.logScrollRevalidate(work.OperationID, false, false, false, qualityState)
		return &capture, nil, targetChangedScroll()
	}
	freshIndex := indexOf(&capture, freshContainer)
	if freshIndex < 0 {
		e.logScrollRevalidate(work.OperationID, false, false, false, qualityState)
		return &capture, nil, targetChangedScroll()
	}
	clipped := freshContainer.Bounds.Intersect(capture.Display.Bounds())
	if clipped.Empty() {
		e.logScrollRevalidate(work.OperationID, true, false, false, qualityState)
		return &capture, nil, targetChangedScroll()
	}
	start, end, ok := scrollGeometry(clipped, capture.Display.Bounds(), plan.direction, plan.amount)
	if !ok {
		e.logScrollRevalidate(work.OperationID, true, false, false, qualityState)
		return &capture, nil, targetChangedScroll()
	}
	projector := work.Projector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	// Preview never advances ref continuity: the container proof authorizes
	// this attempt and must not shift the refs of already published trees.
	projection, err := projector.Preview(&capture)
	if err != nil || projection == nil || projection.Tree == nil {
		return &capture, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	containerPublic := publicNodeByRawIndex(projection, freshIndex)
	if containerPublic == nil || containerPublic.Bounds == nil {
		e.logScrollRevalidate(work.OperationID, true, false, false, qualityState)
		return &capture, nil, targetChangedScroll()
	}
	endInside := containerPublic.Bounds.Contains(end.X, end.Y)
	startScope := scrollScopeMatches(projection, start.X, start.Y, freshIndex)
	endScope := scrollScopeMatches(projection, end.X, end.Y, freshIndex)
	proven := endInside && startScope && endScope
	e.logScrollRevalidate(work.OperationID, true, startScope, endScope, qualityState)
	if !proven {
		return &capture, nil, targetChangedScroll()
	}
	return &capture, &scrollFrame{
		capture: &capture, projection: projection, container: freshContainer,
		containerIndex: freshIndex, direction: plan.direction, amount: plan.amount,
		start: start, end: end,
	}, nil
}

func targetChangedScroll() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeTargetChanged,
		"滚动容器或实际轨迹已变化，本次未派发")
}

// scrollScopeMatches requires every public candidate at the actual point to
// resolve to the selected container or to an outer wrapping scroll ancestor of
// it. The nearest scope of a descendant is the container itself, while an outer
// wrapping ancestor is allowed because it does not change which innermost
// scope owns the injected point. An inner descendant scope, an incomparable
// scope, or a point with no scope at all is a rejection. A candidate without
// any scroll ancestor cannot change the innermost scope and is ignored.
func scrollScopeMatches(projection *Projection, x, y, want int) bool {
	if projection == nil || projection.Tree == nil || projection.Tree.Root == nil {
		return false
	}
	capture := projection.Capture
	ancestors := map[int]bool{}
	if capture != nil && want >= 0 && want < len(capture.Nodes) {
		for parent := capture.Nodes[want].Parent; parent >= 0 && parent < len(capture.Nodes); {
			if ancestors[parent] {
				break
			}
			ancestors[parent] = true
			parent = capture.Nodes[parent].Parent
		}
	}
	found := false
	ok := true
	var walk func(node *protocol.Node, depth int, ancestorsPath []*protocol.Node)
	walk = func(node *protocol.Node, depth int, ancestorsPath []*protocol.Node) {
		if node == nil || !ok {
			return
		}
		path := append(ancestorsPath, node)
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Contains(x, y) {
			scope := -1
			for i := len(path) - 1; i >= 0; i-- {
				if !isScrollContainer(path[i]) {
					continue
				}
				index, mapped := projection.Source[path[i].Ref]
				if !mapped {
					ok = false
					return
				}
				scope = index
				break
			}
			switch {
			case scope < 0:
				// No scroll ancestor: it does not own an innermost scroll
				// scope, so it cannot contradict the selected container.
			case scope == want:
				found = true
			case ancestors[scope]:
				// Outer wrapping scroll ancestor: allowed.
			default:
				ok = false
				return
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1, path)
		}
	}
	walk(projection.Tree.Root, 0, nil)
	return ok && found
}

// dispatchScroll injects the fixed center-line trajectory. The physical
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
type scrollContentUnit struct {
	key  contentKey
	rect device.Rect
}

func (u scrollContentUnit) width() int  { return u.rect.Width() }
func (u scrollContentUnit) height() int { return u.rect.Height() }

func (u scrollContentUnit) axisCenter(vertical bool) int {
	if vertical {
		return (u.rect.Top + u.rect.Bottom) / 2
	}
	return (u.rect.Left + u.rect.Right) / 2
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
				units = append(units, scrollContentUnit{
					key: contentKeyOf(&projection.Capture.Nodes[index]), rect: clip,
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
	preBounds, preOK := publicBoundsByRawIndex(pre.projection, pre.containerIndex)
	postBounds, postOK := publicBoundsByRawIndex(post.Projection, postIndex)
	if !preOK || !postOK || boundsDiffer(preBounds, postBounds, effectMaxSizeDeltaPx) {
		result.Reason = effectReasonContainer
		return result
	}
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
	result.Anchors = len(matched)
	if units > 0 {
		result.Coverage = len(matched) * 100 / units
	}
	if len(matched) < effectMinAnchors {
		result.Reason = effectReasonInsufficient
		return result
	}
	vertical := scrollAxis(direction) == "y"
	for _, pair := range matched {
		if absInt(pair.post.width()-pair.pre.width()) > effectMaxSizeDeltaPx ||
			absInt(pair.post.height()-pair.pre.height()) > effectMaxSizeDeltaPx {
			result.Reason = effectReasonSize
			return result
		}
	}
	if expired() {
		result.Reason = effectReasonCPUBudget
		return result
	}
	movedPositive, movedNegative, maxMove := 0, 0, 0
	for _, pair := range matched {
		delta := pair.post.axisCenter(vertical) - pair.pre.axisCenter(vertical)
		if delta >= effectMinDisplacementPx {
			movedPositive++
		} else if delta <= -effectMinDisplacementPx {
			movedNegative++
		}
		if absInt(delta) > maxMove {
			maxMove = absInt(delta)
		}
	}
	dominant := movedPositive
	if movedNegative > dominant {
		dominant = movedNegative
	}
	if dominant >= effectMinAnchors && dominant*100 >= len(matched)*effectConsensusPercent {
		result.State = protocol.EffectObserved
		result.Reason = ""
		return result
	}
	if quality != nil && quality.State == protocol.QualityStable &&
		len(preUnits) == len(postUnits) && len(matched) == len(preUnits) &&
		len(matched) >= effectMinAnchors && maxMove <= effectNoMoveTolerancePx {
		result.State = protocol.EffectNotObserved
		result.Reason = ""
		return result
	}
	switch {
	case len(preUnits) != len(postUnits) || len(matched) != units:
		result.Reason = effectReasonCoverage
	case quality == nil || quality.State != protocol.QualityStable:
		result.Reason = effectReasonUnstable
	default:
		result.Reason = effectReasonMotion
	}
	return result
}

func boundsDiffer(a, b *protocol.Bounds, tolerance int) bool {
	if a == nil || b == nil {
		return true
	}
	return absInt(a.Left-b.Left) > tolerance || absInt(a.Top-b.Top) > tolerance ||
		absInt(a.Right-b.Right) > tolerance || absInt(a.Bottom-b.Bottom) > tolerance
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

func (e *Engine) logScrollRevalidate(operationID string, containerProven, startProven,
	endProven bool, quality string) {
	e.log.Event(eventlog.Debug, "scroll.revalidate",
		eventlog.Str("operation_id", operationID),
		eventlog.Bool("container_proven", containerProven),
		eventlog.Bool("start_scope_proven", startProven),
		eventlog.Bool("end_scope_proven", endProven),
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
