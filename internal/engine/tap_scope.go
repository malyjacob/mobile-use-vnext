package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	tapModeDefaultCenter = "default_center"
	tapModeCarrierNode   = "carrier_node"
	tapModeCenterSpan    = "center_span"
	tapModeRejected      = "rejected"
)

// tapPlan is the immutable request-local result of static planning. It never
// enters the public tree, the session or the recovery ledger.
type tapPlan struct {
	x, y       int
	strategy   string
	action     string
	targetRef  string
	targetRaw  *device.RawNode
	carrierRef string
	carrierRaw *device.RawNode
	took       time.Duration
}

type tapRawTopology struct {
	capture *device.Capture
	tin     []int
	tout    []int
}

func (t *tapRawTopology) isAncestor(ancestor, descendant int) bool {
	if t == nil || ancestor < 0 || descendant < 0 ||
		ancestor >= len(t.tin) || descendant >= len(t.tin) {
		return false
	}
	return t.tin[ancestor] <= t.tin[descendant] && t.tout[descendant] <= t.tout[ancestor]
}

type tapPublicInfo struct {
	node      *protocol.Node
	parent    *protocol.Node
	depth     int
	order     int
	rawIndex  int
	hasSource bool
}

type tapPublicView struct {
	tree    *protocol.Tree
	capture *device.Capture
	source  map[string]int
	byRef   map[string]*protocol.Node
	info    map[*protocol.Node]*tapPublicInfo
	byRaw   map[int]*protocol.Node
	nodes   []*protocol.Node
}

type tapInput struct {
	view        *tapPublicView
	capture     *device.Capture
	topology    *tapRawTopology
	target      *protocol.Node
	targetRaw   *device.RawNode
	targetIndex int
}

type tapBlockedRect struct {
	rect       device.Rect
	owner      *protocol.Node
	ownerDepth int
	ownerOrder int
}

type tapCarrier struct {
	node     *protocol.Node
	raw      *device.RawNode
	rawIndex int
	order    int
}

type tapScope struct {
	target        *protocol.Node
	base          device.Rect
	blocked       []tapBlockedRect
	carriers      []*tapCarrier
	carrierByRef  map[string]*tapCarrier
	carrierByNode map[*protocol.Node]*tapCarrier
}

type tapCandidate struct {
	x, y     int
	strategy string
	carrier  *tapCarrier
}

type tapGuidance struct {
	ref        string
	carrierRef string
	x, y       int
}

type tapSourceCheck int

const (
	tapSourceValid tapSourceCheck = iota
	tapSourceTargetMissing
	tapSourceDamaged
)

func tapHierarchyError() *protocol.APIError {
	return protocol.NewError(503, protocol.CodeHierarchyFailed, "层级结构不可用")
}

func tapTargetSourceMissing() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点来源不可用")
}

func tapTargetChanged() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeTargetChanged, "目标元素已变化，本次未派发")
}

func tapTargetGeometryError() *protocol.APIError {
	return protocol.NewError(422, protocol.CodeNodeUnsupported, "目标节点没有可用几何")
}

func tapNoCandidateError(action protocol.Action) *protocol.APIError {
	message := "无法在该节点作用域内生成可证明的点击落点"
	if action == protocol.ActionLongPress {
		message = "无法在该节点作用域内生成可证明的长按落点"
	}
	return protocol.NewError(422, protocol.CodeNodeUnsupported, message)
}

// validateTapRawTopology bounds every parent and child lookup before the new
// ref path calls the identity matcher or the projector. It requires one root,
// reciprocal child indexes, exactly one parent per non-root node, no duplicate
// mounting, no disconnected cycle and no unreachable node.
func validateTapRawTopology(capture *device.Capture) (*tapRawTopology, *protocol.APIError) {
	if capture == nil || capture.Display.Width <= 0 || capture.Display.Height <= 0 {
		return nil, tapHierarchyError()
	}
	nodes := capture.Nodes
	if len(nodes) == 0 || capture.Root < 0 || capture.Root >= len(nodes) ||
		nodes[capture.Root].Parent != -1 {
		return nil, tapHierarchyError()
	}
	root := capture.Root
	parentOf := make([]int, len(nodes))
	for i := range parentOf {
		parentOf[i] = -2
	}
	for index := range nodes {
		node := &nodes[index]
		if node.Parent == -1 {
			if index != root {
				return nil, tapHierarchyError()
			}
		} else if node.Parent < 0 || node.Parent >= len(nodes) {
			return nil, tapHierarchyError()
		}
		for _, child := range node.Children {
			if child < 0 || child >= len(nodes) || child == index {
				return nil, tapHierarchyError()
			}
			if parentOf[child] != -2 {
				return nil, tapHierarchyError()
			}
			parentOf[child] = index
			if nodes[child].Parent != index {
				return nil, tapHierarchyError()
			}
		}
	}
	if parentOf[root] != -2 {
		return nil, tapHierarchyError()
	}
	for index := range nodes {
		if index == root {
			continue
		}
		if parentOf[index] == -2 || nodes[index].Parent != parentOf[index] {
			return nil, tapHierarchyError()
		}
	}

	tin := make([]int, len(nodes))
	tout := make([]int, len(nodes))
	seen := make([]bool, len(nodes))
	type frame struct {
		index int
		next  int
	}
	stack := []frame{{index: root}}
	seen[root] = true
	timer := 0
	timer++
	tin[root] = timer
	visited := 1
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		children := nodes[top.index].Children
		if top.next < len(children) {
			child := children[top.next]
			top.next++
			if seen[child] {
				return nil, tapHierarchyError()
			}
			seen[child] = true
			timer++
			tin[child] = timer
			visited++
			stack = append(stack, frame{index: child})
			continue
		}
		timer++
		tout[top.index] = timer
		stack = stack[:len(stack)-1]
	}
	if visited != len(nodes) {
		return nil, tapHierarchyError()
	}
	return &tapRawTopology{capture: capture, tin: tin, tout: tout}, nil
}

func collectTapPublicView(obs *observationState) (*tapPublicView, *protocol.APIError) {
	if obs == nil || obs.Projection == nil || obs.Projection.Tree == nil ||
		obs.Projection.Tree.Root == nil || obs.Projection.Capture == nil {
		return nil, tapHierarchyError()
	}
	capture := obs.Projection.Capture
	if capture.Display.ID != obs.DisplayID ||
		capture.Display.Width != obs.Screen.Width ||
		capture.Display.Height != obs.Screen.Height {
		return nil, tapHierarchyError()
	}
	return buildTapPublicView(obs.Projection.Tree, obs.Projection.Capture, obs.Projection.Source)
}

// buildTapPublicView validates the public graph before any node lookup. The
// traversal is bounded by the raw node count and rejects pointer cycles,
// duplicate refs, nil children and multiple parents.
func buildTapPublicView(tree *protocol.Tree, capture *device.Capture,
	source map[string]int) (*tapPublicView, *protocol.APIError) {
	if tree == nil || tree.Root == nil || capture == nil || len(capture.Nodes) == 0 ||
		capture.Display.Width <= 0 || capture.Display.Height <= 0 {
		return nil, tapHierarchyError()
	}
	if tree.Root.Role != protocol.RoleRoot {
		return nil, tapHierarchyError()
	}
	view := &tapPublicView{
		tree: tree, capture: capture, source: source,
		byRef: map[string]*protocol.Node{},
		info:  map[*protocol.Node]*tapPublicInfo{},
		byRaw: map[int]*protocol.Node{},
	}
	limit := len(capture.Nodes) + 1
	var walk func(node, parent *protocol.Node, depth int) *protocol.APIError
	walk = func(node, parent *protocol.Node, depth int) *protocol.APIError {
		if node == nil || len(view.nodes) >= limit || node.Ref == "" {
			return tapHierarchyError()
		}
		if _, exists := view.info[node]; exists {
			return tapHierarchyError()
		}
		if _, exists := view.byRef[node.Ref]; exists {
			return tapHierarchyError()
		}
		view.byRef[node.Ref] = node
		view.info[node] = &tapPublicInfo{
			node: node, parent: parent, depth: depth, order: len(view.nodes),
			rawIndex: -1,
		}
		view.nodes = append(view.nodes, node)
		for _, child := range node.Children {
			if child == nil {
				return tapHierarchyError()
			}
			if apiErr := walk(child, node, depth+1); apiErr != nil {
				return apiErr
			}
		}
		return nil
	}
	if apiErr := walk(tree.Root, nil, 0); apiErr != nil {
		return nil, apiErr
	}
	return view, nil
}

// checkTapSourceMapping validates the complete source map and fills the
// unique raw-index lookup. A target that has no mapping is distinguished from
// other mapping damage so static calls can keep node_not_found while fresh
// calls can convert it to target_changed.
func checkTapSourceMapping(view *tapPublicView, topology *tapRawTopology,
	targetRaw int) tapSourceCheck {
	if view == nil || topology == nil || topology.capture == nil {
		return tapSourceDamaged
	}
	view.byRaw = map[int]*protocol.Node{}
	for _, info := range view.info {
		info.rawIndex = -1
		info.hasSource = false
	}
	if targetRaw >= 0 {
		found := false
		for _, info := range view.info {
			if info.node.Context {
				continue
			}
			if index, ok := view.source[info.node.Ref]; ok && index == targetRaw {
				found = true
				break
			}
		}
		if !found {
			return tapSourceTargetMissing
		}
	}
	if view.source == nil {
		return tapSourceDamaged
	}
	for _, info := range view.info {
		node := info.node
		index, mapped := view.source[node.Ref]
		if node.Context {
			if mapped {
				return tapSourceDamaged
			}
			continue
		}
		if !mapped || index < 0 || index >= len(topology.capture.Nodes) {
			return tapSourceDamaged
		}
		if node.Role == protocol.RoleRoot {
			if index != topology.capture.Root {
				return tapSourceDamaged
			}
		} else if index == topology.capture.Root {
			return tapSourceDamaged
		}
		if previous, exists := view.byRaw[index]; exists && previous != node {
			return tapSourceDamaged
		}
		view.byRaw[index] = node
		info.rawIndex = index
		info.hasSource = true
	}
	for ref, index := range view.source {
		node, exists := view.byRef[ref]
		if !exists || node.Context || index < 0 || index >= len(topology.capture.Nodes) {
			return tapSourceDamaged
		}
	}
	for _, info := range view.info {
		if info.node.Context || !info.hasSource {
			continue
		}
		for parent := info.parent; parent != nil; {
			parentInfo := view.info[parent]
			if parentInfo == nil {
				return tapSourceDamaged
			}
			if !parent.Context {
				if !parentInfo.hasSource ||
					!topology.isAncestor(parentInfo.rawIndex, info.rawIndex) {
					return tapSourceDamaged
				}
				break
			}
			parent = parentInfo.parent
		}
	}
	if targetRaw >= 0 && view.byRaw[targetRaw] == nil {
		return tapSourceTargetMissing
	}
	return tapSourceValid
}

func buildTapInput(obs *observationState, view *tapPublicView,
	target *protocol.Node) (*tapInput, *protocol.APIError) {
	if obs == nil || obs.Projection == nil || obs.Projection.Capture == nil ||
		obs.Projection.Source == nil || view == nil || target == nil {
		return nil, tapHierarchyError()
	}
	if target.Context || target.Role == protocol.RoleRoot {
		return nil, protocol.NewError(422, protocol.CodeNodeUnsupported, "该节点不能作为操作目标")
	}
	index, mapped := obs.Projection.Source[target.Ref]
	if !mapped || index < 0 || index >= len(obs.Projection.Capture.Nodes) {
		return nil, tapTargetSourceMissing()
	}
	topology, apiErr := validateTapRawTopology(obs.Projection.Capture)
	if apiErr != nil {
		return nil, apiErr
	}
	switch checkTapSourceMapping(view, topology, index) {
	case tapSourceTargetMissing:
		return nil, tapTargetSourceMissing()
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if view.byRaw[index] != target {
		return nil, tapHierarchyError()
	}
	return &tapInput{
		view: view, capture: obs.Projection.Capture, topology: topology,
		target: target, targetRaw: &obs.Projection.Capture.Nodes[index],
		targetIndex: index,
	}, nil
}

// isTouchInteractive is the conservative public capability boundary. It does
// not exempt disabled nodes and does not treat swipe alone as interaction.
func isTouchInteractive(node *protocol.Node) bool {
	if node == nil || node.Context || node.Role == protocol.RoleRoot {
		return false
	}
	return hasAction(node, protocol.ActionTap) ||
		hasAction(node, protocol.ActionLongPress) ||
		hasAction(node, protocol.ActionText)
}

func anonymousPlainNode(node *protocol.Node) bool {
	if node == nil || node.Context {
		return false
	}
	if node.Role != protocol.RoleView && node.Role != protocol.RoleGroup {
		return false
	}
	if len(node.Label) != 0 || len(node.Actions) != 0 ||
		node.Value != nil || node.Hint != nil || node.Resource != nil || node.Axis != "" {
		return false
	}
	if node.Enabled == nil || !*node.Enabled {
		return false
	}
	for _, flag := range []*bool{node.Focused, node.Selected, node.Protected, node.Checked} {
		if flag == nil || *flag {
			return false
		}
	}
	return true
}

// tapSafeAverage returns the same truncated integer midpoint as
// (a+b)/2 without overflowing for same-sign extremes.
func tapSafeAverage(a, b int) int {
	if (a < 0) != (b < 0) {
		return (a + b) / 2
	}
	return a/2 + b/2 + (a%2+b%2)/2
}

func tapSafeCenter(rect device.Rect) (int, int, bool) {
	if rect.Right <= rect.Left || rect.Bottom <= rect.Top {
		return 0, 0, false
	}
	return tapSafeAverage(rect.Left, rect.Right),
		tapSafeAverage(rect.Top, rect.Bottom), true
}

func collectTapScope(input *tapInput) (*tapScope, *protocol.APIError) {
	if input == nil || input.capture == nil || input.view == nil ||
		input.target == nil || input.targetRaw == nil || input.target.Bounds == nil {
		return nil, tapHierarchyError()
	}
	if input.capture.Display.Width <= 0 || input.capture.Display.Height <= 0 {
		return nil, tapHierarchyError()
	}
	public := rectFromBounds(input.target.Bounds)
	if public.Width() <= 0 || public.Height() <= 0 || input.targetRaw.Bounds.Empty() {
		return nil, tapTargetGeometryError()
	}
	screen := input.capture.Display.Bounds()
	base := input.targetRaw.Bounds.Intersect(screen).Intersect(public)
	if base.Empty() {
		return nil, tapTargetGeometryError()
	}
	scope := &tapScope{
		target: input.target, base: base,
		carrierByRef:  map[string]*tapCarrier{},
		carrierByNode: map[*protocol.Node]*tapCarrier{},
	}
	order := 0
	var walk func(node *protocol.Node, depth int, inBlocked bool, owner *protocol.Node,
		ownerDepth, ownerOrder int) *protocol.APIError
	walk = func(node *protocol.Node, depth int, inBlocked bool, owner *protocol.Node,
		ownerDepth, ownerOrder int) *protocol.APIError {
		if node == nil {
			return tapHierarchyError()
		}
		order++
		boundary := isTouchInteractive(node) || node.Context
		blockedHere := inBlocked || boundary
		if boundary {
			owner = node
			ownerDepth = depth
			ownerOrder = order
		}
		if blockedHere {
			if !node.Context && node.Bounds != nil {
				rect := rectFromBounds(node.Bounds)
				if rect.Width() > 0 && rect.Height() > 0 {
					if clipped := rect.Intersect(base); !clipped.Empty() {
						scope.blocked = append(scope.blocked, tapBlockedRect{
							rect: clipped, owner: owner,
							ownerDepth: ownerDepth, ownerOrder: ownerOrder,
						})
					}
				}
			}
		} else if node.Role != protocol.RoleRoot && node.Bounds != nil {
			rect := rectFromBounds(node.Bounds)
			if rect.Width() > 0 && rect.Height() > 0 {
				info := input.view.info[node]
				if info == nil || !info.hasSource ||
					info.rawIndex < 0 || info.rawIndex >= len(input.capture.Nodes) {
					return tapHierarchyError()
				}
				carrier := &tapCarrier{
					node: node, raw: &input.capture.Nodes[info.rawIndex],
					rawIndex: info.rawIndex, order: order,
				}
				scope.carriers = append(scope.carriers, carrier)
				scope.carrierByRef[node.Ref] = carrier
				scope.carrierByNode[node] = carrier
			}
		}
		for _, child := range node.Children {
			if apiErr := walk(child, depth+1, blockedHere, owner,
				ownerDepth, ownerOrder); apiErr != nil {
				return apiErr
			}
		}
		return nil
	}
	for _, child := range input.target.Children {
		if apiErr := walk(child, 1, false, nil, -1, -1); apiErr != nil {
			return nil, apiErr
		}
	}
	return scope, nil
}

func provesSubtreeTapPoint(scope *tapScope, x, y int) bool {
	if scope == nil || scope.target == nil || !scope.base.Contains(x, y) {
		return false
	}
	for _, blocked := range scope.blocked {
		if blocked.rect.Contains(x, y) {
			return false
		}
	}
	bestDepth, bestCount := -1, 0
	var best *protocol.Node
	visited := map[*protocol.Node]bool{}
	var walk func(node *protocol.Node, depth int) bool
	walk = func(node *protocol.Node, depth int) bool {
		if node == nil || visited[node] {
			return false
		}
		visited[node] = true
		if !node.Context && node.Role != protocol.RoleRoot && node.Bounds != nil {
			rect := rectFromBounds(node.Bounds)
			if rect.Width() > 0 && rect.Height() > 0 && rect.Contains(x, y) {
				switch {
				case depth > bestDepth:
					bestDepth, best, bestCount = depth, node, 1
				case depth == bestDepth:
					bestCount++
				}
			}
		}
		for _, child := range node.Children {
			if !walk(child, depth+1) {
				return false
			}
		}
		return true
	}
	if !walk(scope.target, 0) || bestCount != 1 || best == nil {
		return false
	}
	if best == scope.target {
		return true
	}
	return scope.carrierByNode[best] != nil
}

func longestTapSpan(spans []scrollSpan) (scrollSpan, bool) {
	if len(spans) == 0 {
		return scrollSpan{}, false
	}
	best := spans[0]
	bestLength := best.end - best.start
	for _, span := range spans[1:] {
		length := span.end - span.start
		if length > bestLength || (length == bestLength && span.start < best.start) {
			best, bestLength = span, length
		}
	}
	return best, bestLength > 0
}

func tapCenterLineSpan(scope *tapScope, vertical bool, coordinate int) (scrollSpan, bool) {
	if scope == nil || scope.base.Empty() {
		return scrollSpan{}, false
	}
	var base scrollSpan
	if vertical {
		base = scrollSpan{start: scope.base.Top, end: scope.base.Bottom}
	} else {
		base = scrollSpan{start: scope.base.Left, end: scope.base.Right}
	}
	var blocked []scrollSpan
	for _, item := range scope.blocked {
		if vertical {
			if coordinate >= item.rect.Left && coordinate < item.rect.Right {
				blocked = append(blocked, scrollSpan{start: item.rect.Top, end: item.rect.Bottom})
			}
		} else if coordinate >= item.rect.Top && coordinate < item.rect.Bottom {
			blocked = append(blocked, scrollSpan{start: item.rect.Left, end: item.rect.Right})
		}
	}
	return longestTapSpan(subtractScrollSpans(base, blocked))
}

func enumerateTapCandidates(scope *tapScope, raw *device.RawNode) []tapCandidate {
	if scope == nil {
		return nil
	}
	var candidates []tapCandidate
	seen := map[[2]int]bool{}
	add := func(x, y int, strategy string, carrier *tapCarrier) {
		if !scope.base.Contains(x, y) || seen[[2]int{x, y}] {
			return
		}
		seen[[2]int{x, y}] = true
		candidates = append(candidates, tapCandidate{
			x: x, y: y, strategy: strategy, carrier: carrier,
		})
	}
	if x, y, ok := tapSafeCenter(raw.Bounds); ok {
		if !scope.base.Contains(x, y) {
			x, y, ok = tapSafeCenter(scope.base)
		}
		if ok {
			add(x, y, tapModeDefaultCenter, nil)
		}
	}
	var named, anonymous []*tapCarrier
	for _, carrier := range scope.carriers {
		if anonymousPlainNode(carrier.node) {
			anonymous = append(anonymous, carrier)
		} else {
			named = append(named, carrier)
		}
	}
	for _, carrier := range append(named, anonymous...) {
		if carrier.node.Bounds == nil {
			continue
		}
		rect := rectFromBounds(carrier.node.Bounds)
		clip := rect.Intersect(scope.base)
		if clip.Empty() {
			continue
		}
		x, y, ok := tapSafeCenter(rect)
		if ok && !scope.base.Contains(x, y) {
			x, y, ok = tapSafeCenter(clip)
		}
		if !ok {
			continue
		}
		add(x, y, tapModeCarrierNode, carrier)
	}
	if scope.base.Width() > 0 && scope.base.Height() > 0 {
		x := scope.base.Left + scope.base.Width()/2
		if span, ok := tapCenterLineSpan(scope, true, x); ok {
			add(x, span.start+(span.end-span.start)/2, tapModeCenterSpan, nil)
		}
		y := scope.base.Top + scope.base.Height()/2
		if span, ok := tapCenterLineSpan(scope, false, y); ok {
			add(span.start+(span.end-span.start)/2, y, tapModeCenterSpan, nil)
		}
	}
	return candidates
}

func occludingTapRef(scope *tapScope, raw *device.RawNode) string {
	if scope == nil || raw == nil {
		return ""
	}
	x, y, ok := tapSafeCenter(raw.Bounds)
	if !ok || !scope.base.Contains(x, y) {
		return ""
	}
	var best *protocol.Node
	bestDepth, bestOrder := -1, 0
	for _, blocked := range scope.blocked {
		if blocked.owner == nil || !blocked.rect.Contains(x, y) {
			continue
		}
		if best == nil || blocked.ownerDepth > bestDepth ||
			(blocked.ownerDepth == bestDepth && blocked.ownerOrder < bestOrder) {
			best = blocked.owner
			bestDepth, bestOrder = blocked.ownerDepth, blocked.ownerOrder
		}
	}
	if best == nil {
		return ""
	}
	return best.Ref
}

func chooseTapPlan(scope *tapScope, input *tapInput, target *protocol.Node,
	action protocol.Action, started time.Time) (*tapPlan, *protocol.APIError) {
	plan := &tapPlan{
		strategy: tapModeRejected, action: string(action),
		targetRef: target.Ref, targetRaw: input.targetRaw,
	}
	for _, candidate := range enumerateTapCandidates(scope, input.targetRaw) {
		if !provesSubtreeTapPoint(scope, candidate.x, candidate.y) {
			continue
		}
		plan.x, plan.y = candidate.x, candidate.y
		plan.strategy = candidate.strategy
		if candidate.carrier != nil {
			plan.carrierRef = candidate.carrier.node.Ref
			plan.carrierRaw = candidate.carrier.raw
		}
		plan.took = time.Since(started)
		return plan, nil
	}
	plan.took = time.Since(started)
	apiErr := tapNoCandidateError(action)
	if ref := occludingTapRef(scope, input.targetRaw); ref != "" {
		apiErr.WithDetail("occluding_ref", ref)
	}
	return plan, apiErr
}

func planTapTarget(obs *observationState, target *protocol.Node,
	action protocol.Action) (*tapPlan, *protocol.APIError) {
	started := time.Now()
	view, apiErr := collectTapPublicView(obs)
	if apiErr != nil {
		return nil, apiErr
	}
	input, apiErr := buildTapInput(obs, view, target)
	if apiErr != nil {
		return nil, apiErr
	}
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		return nil, apiErr
	}
	return chooseTapPlan(scope, input, target, action, started)
}

func tapUnsupportedWithGuidance(obs *observationState, view *tapPublicView,
	target *protocol.Node, action protocol.Action, message string) *protocol.APIError {
	apiErr := protocol.NewError(422, protocol.CodeNodeUnsupported, message)
	guidance := tapGuidanceFor(obs, view, target, action)
	if guidance == nil {
		return apiErr
	}
	apiErr.WithDetail("nearest_actionable_ref", guidance.ref)
	apiErr.WithDetail("safe_point", map[string]any{"x": guidance.x, "y": guidance.y})
	if guidance.carrierRef != "" {
		apiErr.WithDetail("safe_carrier_ref", guidance.carrierRef)
	}
	return apiErr
}

func tapGuidanceFor(obs *observationState, view *tapPublicView,
	target *protocol.Node, action protocol.Action) *tapGuidance {
	if target == nil || target.Context || isTouchInteractive(target) {
		return nil
	}
	info := view.info[target]
	if info == nil {
		return nil
	}
	for parent := info.parent; parent != nil; {
		parentInfo := view.info[parent]
		if parentInfo == nil {
			return nil
		}
		if parent.Role == protocol.RoleRoot {
			return nil
		}
		if parent.Context {
			return nil
		}
		if !isTouchInteractive(parent) {
			parent = parentInfo.parent
			continue
		}
		if parent.Enabled != nil && !*parent.Enabled ||
			parent.Bounds == nil || parent.Bounds.Width() <= 0 ||
			parent.Bounds.Height() <= 0 || !hasAction(parent, action) {
			return nil
		}
		input, apiErr := buildTapInput(obs, view, parent)
		if apiErr != nil {
			return nil
		}
		scope, apiErr := collectTapScope(input)
		if apiErr != nil {
			return nil
		}
		if plan, apiErr := chooseTapPlan(scope, input, parent, action, time.Now()); apiErr != nil || plan == nil || plan.strategy == tapModeRejected {
			return nil
		}
		candidates := tapGuidanceCandidates(scope, input.targetRaw, target)
		seen := map[[2]int]bool{}
		for _, candidate := range candidates {
			key := [2]int{candidate.x, candidate.y}
			if seen[key] {
				continue
			}
			seen[key] = true
			if !provesGuidancePoint(view, parent, scope, candidate.x, candidate.y) {
				continue
			}
			guidance := &tapGuidance{
				ref: parent.Ref, x: candidate.x, y: candidate.y,
			}
			if candidate.carrier != nil {
				guidance.carrierRef = candidate.carrier.node.Ref
			}
			return guidance
		}
		return nil
	}
	return nil
}

func tapGuidanceCandidates(scope *tapScope, raw *device.RawNode,
	target *protocol.Node) []tapCandidate {
	var candidates []tapCandidate
	if target != nil && target.Bounds != nil {
		rect := rectFromBounds(target.Bounds)
		if rect.Width() > 0 && rect.Height() > 0 {
			if clip := rect.Intersect(scope.base); !clip.Empty() {
				if x, y, ok := tapSafeCenter(rect); ok {
					if !scope.base.Contains(x, y) {
						x, y, ok = tapSafeCenter(clip)
					}
					if ok && scope.base.Contains(x, y) {
						candidates = append(candidates, tapCandidate{
							x: x, y: y, strategy: tapModeCarrierNode,
							carrier: &tapCarrier{node: target},
						})
					}
				}
			}
		}
	}
	return append(candidates, enumerateTapCandidates(scope, raw)...)
}

func provesGuidancePoint(view *tapPublicView, ancestor *protocol.Node,
	scope *tapScope, x, y int) bool {
	if view == nil || view.tree == nil || view.tree.Root == nil ||
		ancestor == nil || !provesSubtreeTapPoint(scope, x, y) {
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
	info := view.info[hit]
	if info == nil || !info.hasSource {
		return false
	}
	if hit == ancestor {
		return true
	}
	carrier := scope.carrierByRef[ref]
	return carrier != nil && carrier.node == hit
}

func validTapPublicTarget(node *protocol.Node, action protocol.Action) bool {
	if node == nil || node.Context || node.Role == protocol.RoleRoot ||
		node.Bounds == nil || node.Bounds.Width() <= 0 || node.Bounds.Height() <= 0 {
		return false
	}
	if node.Enabled != nil && !*node.Enabled {
		return false
	}
	return hasAction(node, action)
}

func staticTapRefCheck(obs *observationState, operation *protocol.Operation) (
	*pointCheck, *scrollPlan, *tapPlan, *protocol.APIError) {
	started := time.Now()
	view, apiErr := collectTapPublicView(obs)
	if apiErr != nil {
		return nil, nil, nil, apiErr
	}
	target := view.byRef[operation.Target.Ref]
	if target == nil {
		return nil, nil, nil, protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点不存在")
	}
	if target.Role == protocol.RoleRoot {
		return nil, nil, nil, protocol.NewError(422, protocol.CodeNodeUnsupported,
			"该节点不能作为操作目标")
	}
	action := protocol.ActionTap
	unsupported := "目标节点不支持点击"
	if operation.Type == "long_press" {
		action = protocol.ActionLongPress
		unsupported = "目标节点不支持长按"
	}
	if target.Context || target.Bounds == nil {
		return nil, nil, nil, tapUnsupportedWithGuidance(obs, view, target, action,
			"该节点不能作为操作目标")
	}
	if target.Enabled != nil && !*target.Enabled {
		return nil, nil, nil, protocol.NewError(409, protocol.CodeNodeDisabled, "目标节点已禁用")
	}
	if !hasAction(target, action) {
		return nil, nil, nil, tapUnsupportedWithGuidance(obs, view, target, action, unsupported)
	}
	input, apiErr := buildTapInput(obs, view, target)
	if apiErr != nil {
		return nil, nil, nil, apiErr
	}
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		if apiErr.HTTPStatus == 422 {
			plan := &tapPlan{
				strategy: tapModeRejected, action: string(action),
				targetRef: target.Ref, targetRaw: input.targetRaw,
				took: time.Since(started),
			}
			return nil, nil, plan, apiErr
		}
		return nil, nil, nil, apiErr
	}
	plan, apiErr := chooseTapPlan(scope, input, target, action, started)
	return nil, nil, plan, apiErr
}
