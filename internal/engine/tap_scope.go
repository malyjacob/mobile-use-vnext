package engine

import (
	"fmt"
	"sort"
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

// Static tap/long_press ref reasons. They are a closed enum on the three
// static error codes and never extend another tool's same-named code.
const (
	tapStaticReasonRoot                  = "root"
	tapStaticReasonContext               = "context"
	tapStaticReasonNoGeometry            = "no_geometry"
	tapStaticReasonTargetMissingAction   = "target_missing_action"
	tapStaticReasonTargetHasOtherAction  = "target_has_other_action"
	tapStaticReasonAncestorMissingAction = "ancestor_missing_action"
	tapStaticReasonAncestorInvalid       = "ancestor_invalid"
	tapStaticReasonNoInteractiveAncestor = "no_interactive_ancestor"
	tapStaticReasonBlocked               = "blocked"
	tapStaticReasonAmbiguous             = "ambiguous"
	tapStaticReasonOwnerMismatch         = "owner_mismatch"
	tapStaticReasonMixed                 = "mixed"
)

const (
	tapStaticScopeTarget   = "target"
	tapStaticScopeAncestor = "ancestor"
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
	// bubbleAncestorRaw is set only for a default-bubble ref target whose own
	// node has no touch-interactive action. It binds the one nearest interactive
	// ancestor that the action is planned against.
	bubbleAncestorRaw *device.RawNode
	took              time.Duration
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

// tapBlockingOwnerAt returns the blocking boundary owner of one point. It is
// the single owner-selection rule shared by the legacy center hint and the
// static candidate diagnosis: deepest owner first, then earliest preorder.
func tapBlockingOwnerAt(scope *tapScope, x, y int) (*protocol.Node, int, int) {
	if scope == nil {
		return nil, -1, 0
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
	return best, bestDepth, bestOrder
}

func occludingTapRef(scope *tapScope, raw *device.RawNode) string {
	if scope == nil || raw == nil {
		return ""
	}
	x, y, ok := tapSafeCenter(raw.Bounds)
	if !ok || !scope.base.Contains(x, y) {
		return ""
	}
	best, _, _ := tapBlockingOwnerAt(scope, x, y)
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
	attachTapGuidance(apiErr, obs, view, target, action)
	return apiErr
}

// attachTapGuidance adds the legacy atomic ancestor group when the baseline
// position attempted it. It never widens its search: a nil result omits the
// whole group.
func attachTapGuidance(apiErr *protocol.APIError, obs *observationState,
	view *tapPublicView, target *protocol.Node, action protocol.Action) {
	if apiErr == nil {
		return
	}
	guidance := tapGuidanceFor(obs, view, target, action)
	if guidance == nil {
		return
	}
	apiErr.WithDetail("nearest_actionable_ref", guidance.ref)
	apiErr.WithDetail("safe_point", map[string]any{"x": guidance.x, "y": guidance.y})
	if guidance.carrierRef != "" {
		apiErr.WithDetail("safe_carrier_ref", guidance.carrierRef)
	}
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

// nearestInteractiveAncestor returns the first touch-interactive public
// ancestor above target. Context wrappers and plain containers are skipped;
// the root is never a candidate. It never looks beyond that first ancestor.
func nearestInteractiveAncestor(view *tapPublicView, target *protocol.Node) *protocol.Node {
	if view == nil || target == nil {
		return nil
	}
	info := view.info[target]
	if info == nil {
		return nil
	}
	for parent := info.parent; parent != nil; {
		parentInfo := view.info[parent]
		if parentInfo == nil || parent.Role == protocol.RoleRoot {
			return nil
		}
		if parent.Context || !isTouchInteractive(parent) {
			parent = parentInfo.parent
			continue
		}
		return parent
	}
	return nil
}

// samePublicLabel compares the complete public label, not only a prefix.
func samePublicLabel(a, b []string) bool {
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

// chooseBubbleTapPoint selects one fixed safe point inside the ancestor scope.
// The original child's center is preferred; every accepted point must prove
// through the full public tree that it lands on the ancestor or a
// non-interactive carrier inside that ancestor, never on a sibling interactive
// target or a farther ancestor.
func chooseBubbleTapPoint(scope *tapScope, view *tapPublicView,
	ancestor, child *protocol.Node) (int, int, string, *tapCarrier, bool) {
	if scope == nil || view == nil || ancestor == nil || child == nil {
		return 0, 0, "", nil, false
	}
	if child.Bounds != nil {
		rect := rectFromBounds(child.Bounds)
		if rect.Width() > 0 && rect.Height() > 0 {
			x, y, ok := tapSafeCenter(rect)
			if ok && !scope.base.Contains(x, y) {
				if clip := rect.Intersect(scope.base); !clip.Empty() {
					x, y, ok = tapSafeCenter(clip)
				} else {
					ok = false
				}
			}
			if ok && scope.base.Contains(x, y) &&
				provesGuidancePoint(view, ancestor, scope, x, y) {
				return x, y, tapModeCarrierNode, nil, true
			}
		}
	}
	ancestorInfo := view.info[ancestor]
	var ancestorRaw *device.RawNode
	if ancestorInfo != nil && ancestorInfo.hasSource && ancestorInfo.rawIndex >= 0 &&
		ancestorInfo.rawIndex < len(view.capture.Nodes) {
		ancestorRaw = &view.capture.Nodes[ancestorInfo.rawIndex]
	}
	for _, candidate := range enumerateTapCandidates(scope, ancestorRaw) {
		if !provesGuidancePoint(view, ancestor, scope, candidate.x, candidate.y) {
			continue
		}
		return candidate.x, candidate.y, candidate.strategy, candidate.carrier, true
	}
	return 0, 0, "", nil, false
}

// tapStaticPoint is one logical point tested by the static planner or by the
// bounded suggestion grid. It never leaves the error path.
type tapStaticPoint struct {
	x, y int
}

type tapStaticPointFailure struct {
	kind       string
	owner      *protocol.Node
	ownerDepth int
	ownerOrder int
	related    []*protocol.Node
}

type tapStaticOwnerFact struct {
	depth int
	order int
}

// tapStaticDiagnosis is the bounded failure summary of the original fixed
// candidates plus the optional suggestion group. Ref lists are already
// deduplicated, ordered and truncated.
type tapStaticDiagnosis struct {
	reason              string
	occludingRefs       []string
	ambiguousRefs       []string
	ownerRefs           []string
	suggestedPoint      *tapStaticPoint
	suggestedCarrierRef string
}

func tapPublicOrder(view *tapPublicView, node *protocol.Node) int {
	if view != nil && node != nil {
		if info := view.info[node]; info != nil {
			return info.order
		}
	}
	return int(^uint(0) >> 1)
}

// tapSubtreePointOwners replays the deepest-node rule of
// provesSubtreeTapPoint and also returns every tied deepest node in public
// preorder. The boolean is false when the walk cannot complete.
func tapSubtreePointOwners(scope *tapScope, x, y int) (*protocol.Node, []*protocol.Node, bool) {
	if scope == nil || scope.target == nil {
		return nil, nil, false
	}
	bestDepth, bestCount := -1, 0
	var best *protocol.Node
	var ties []*protocol.Node
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
					ties = []*protocol.Node{node}
				case depth == bestDepth:
					bestCount++
					ties = append(ties, node)
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
	if !walk(scope.target, 0) {
		return best, ties, false
	}
	return best, ties, true
}

// tapWholeTreePointOwners replays resolvePoint and returns the tied deepest
// nodes for the ambiguity diagnosis.
func tapWholeTreePointOwners(view *tapPublicView, x, y int) (*protocol.Node, []*protocol.Node, pointState) {
	if view == nil || view.tree == nil || view.tree.Root == nil {
		return nil, nil, pointNone
	}
	bestDepth, bestCount := -1, 0
	var best *protocol.Node
	var ties []*protocol.Node
	var walk func(node *protocol.Node, depth int)
	walk = func(node *protocol.Node, depth int) {
		if node == nil {
			return
		}
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Contains(x, y) {
			switch {
			case depth > bestDepth:
				bestDepth, best, bestCount = depth, node, 1
				ties = []*protocol.Node{node}
			case depth == bestDepth:
				bestCount++
				ties = append(ties, node)
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	walk(view.tree.Root, 0)
	switch {
	case bestCount == 0:
		return nil, nil, pointNone
	case bestCount > 1:
		return nil, ties, pointAmbiguous
	default:
		return best, nil, pointUnique
	}
}

// classifyTapStaticPoint replays the original predicate short-circuit order:
// blocking first, then the subtree deepest-node rule, then, for a lifted
// bubble target, the whole-tree ownership rule.
func classifyTapStaticPoint(view *tapPublicView, scope *tapScope,
	object *protocol.Node, bubble bool, x, y int) tapStaticPointFailure {
	if object == nil || scope == nil {
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous}
	}
	if owner, ownerDepth, ownerOrder := tapBlockingOwnerAt(scope, x, y); owner != nil {
		return tapStaticPointFailure{
			kind: tapStaticReasonBlocked, owner: owner,
			ownerDepth: ownerDepth, ownerOrder: ownerOrder,
		}
	}
	best, ties, ok := tapSubtreePointOwners(scope, x, y)
	if !ok || best == nil || len(ties) != 1 {
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous, related: ties}
	}
	if best != object && scope.carrierByNode[best] == nil {
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous, related: ties}
	}
	if !bubble {
		return tapStaticPointFailure{}
	}
	hit, wholeTies, state := tapWholeTreePointOwners(view, x, y)
	switch state {
	case pointAmbiguous:
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous, related: wholeTies}
	case pointNone:
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous}
	}
	if hit == nil {
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous}
	}
	if info := view.info[hit]; info == nil || !info.hasSource {
		return tapStaticPointFailure{kind: tapStaticReasonAmbiguous}
	}
	if hit == object {
		return tapStaticPointFailure{}
	}
	if carrier := scope.carrierByRef[hit.Ref]; carrier != nil && carrier.node == hit {
		return tapStaticPointFailure{}
	}
	return tapStaticPointFailure{
		kind: tapStaticReasonOwnerMismatch, related: []*protocol.Node{hit},
	}
}

// tapStaticCandidatePoints replays exactly the direct-target candidates the
// planner tested. The list may contain more than three points and is only used
// to classify every original failure.
func tapStaticCandidatePoints(scope *tapScope, raw *device.RawNode) []tapStaticPoint {
	var points []tapStaticPoint
	for _, candidate := range enumerateTapCandidates(scope, raw) {
		points = append(points, tapStaticPoint{x: candidate.x, y: candidate.y})
	}
	return points
}

// tapStaticBubbleCandidatePoints replays the child center (including its clip
// fallback) before the ancestor candidate sequence, preserving the original
// order and duplicate behavior.
func tapStaticBubbleCandidatePoints(scope *tapScope, view *tapPublicView,
	ancestor, child *protocol.Node) []tapStaticPoint {
	var points []tapStaticPoint
	if scope == nil || view == nil {
		return points
	}
	if child != nil && child.Bounds != nil {
		rect := rectFromBounds(child.Bounds)
		if rect.Width() > 0 && rect.Height() > 0 {
			x, y, ok := tapSafeCenter(rect)
			if ok && !scope.base.Contains(x, y) {
				if clip := rect.Intersect(scope.base); !clip.Empty() {
					x, y, ok = tapSafeCenter(clip)
				} else {
					ok = false
				}
			}
			if ok && scope.base.Contains(x, y) {
				points = append(points, tapStaticPoint{x: x, y: y})
			}
		}
	}
	ancestorInfo := view.info[ancestor]
	var ancestorRaw *device.RawNode
	if ancestorInfo != nil && ancestorInfo.hasSource && ancestorInfo.rawIndex >= 0 &&
		ancestorInfo.rawIndex < len(view.capture.Nodes) {
		ancestorRaw = &view.capture.Nodes[ancestorInfo.rawIndex]
	}
	for _, candidate := range enumerateTapCandidates(scope, ancestorRaw) {
		points = append(points, tapStaticPoint{x: candidate.x, y: candidate.y})
	}
	return points
}

// tapFiveSixths returns floor(5*width/6) without computing 5*width.
func tapFiveSixths(width int) int {
	quotient, remainder := width/6, width%6
	result := quotient*5 + remainder
	if remainder > 0 {
		result--
	}
	return result
}

// tapStaticGridPoints returns the fixed 3x3 probe over the half-open scope
// base, y outer and x inner. Duplicate coordinates are removed.
func tapStaticGridPoints(base device.Rect) []tapStaticPoint {
	width, height := base.Width(), base.Height()
	if width <= 0 || height <= 0 {
		return nil
	}
	xs := [3]int{base.Left + width/6, base.Left + width/2, base.Left + tapFiveSixths(width)}
	ys := [3]int{base.Top + height/6, base.Top + height/2, base.Top + tapFiveSixths(height)}
	var points []tapStaticPoint
	seen := map[[2]int]bool{}
	for _, y := range ys {
		for _, x := range xs {
			key := [2]int{x, y}
			if seen[key] {
				continue
			}
			seen[key] = true
			points = append(points, tapStaticPoint{x: x, y: y})
		}
	}
	return points
}

func tapStaticOrderedRefs(view *tapPublicView, orders map[string]int) []string {
	refs := make([]string, 0, len(orders))
	for ref := range orders {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if orders[refs[i]] != orders[refs[j]] {
			return orders[refs[i]] < orders[refs[j]]
		}
		return refs[i] < refs[j]
	})
	if len(refs) > 3 {
		refs = refs[:3]
	}
	return refs
}

func tapStaticOccludingRefs(view *tapPublicView,
	facts map[string]tapStaticOwnerFact) []string {
	refs := make([]string, 0, len(facts))
	for ref := range facts {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		left, right := facts[refs[i]], facts[refs[j]]
		if left.depth != right.depth {
			return left.depth > right.depth
		}
		if left.order != right.order {
			return left.order < right.order
		}
		return refs[i] < refs[j]
	})
	if len(refs) > 3 {
		refs = refs[:3]
	}
	return refs
}

// tapSuggestStaticPoint probes at most nine new grid points. A point is
// suggested only when it passes the full branch predicate, including the
// whole-tree unique ownership and source checks required by the guidance
// predicate. The original request is still rejected.
func tapSuggestStaticPoint(view *tapPublicView, scope *tapScope,
	object *protocol.Node, original []tapStaticPoint) (*tapStaticPoint, string) {
	if view == nil || view.tree == nil || view.tree.Root == nil || scope == nil || object == nil {
		return nil, ""
	}
	tested := map[[2]int]bool{}
	for _, point := range original {
		tested[[2]int{point.x, point.y}] = true
	}
	seen := map[[2]int]bool{}
	for _, point := range tapStaticGridPoints(scope.base) {
		key := [2]int{point.x, point.y}
		if tested[key] || seen[key] {
			continue
		}
		seen[key] = true
		if !provesGuidancePoint(view, object, scope, point.x, point.y) {
			continue
		}
		ref, state := resolvePoint(view.tree.Root, point.x, point.y)
		if state != pointUnique || ref == "" {
			continue
		}
		carrierRef := ""
		if ref != object.Ref {
			carrier := scope.carrierByRef[ref]
			if carrier == nil || carrier.node == nil || carrier.node.Ref != ref {
				continue
			}
			carrierRef = ref
		}
		suggested := point
		return &suggested, carrierRef
	}
	return nil, ""
}

func diagnoseTapStaticFailure(view *tapPublicView, scope *tapScope,
	object *protocol.Node, bubble bool, original []tapStaticPoint) *tapStaticDiagnosis {
	occluding := map[string]tapStaticOwnerFact{}
	ambiguous := map[string]int{}
	owners := map[string]int{}
	categories := map[string]bool{}
	for _, point := range original {
		failure := classifyTapStaticPoint(view, scope, object, bubble, point.x, point.y)
		if failure.kind == "" {
			continue
		}
		categories[failure.kind] = true
		switch failure.kind {
		case tapStaticReasonBlocked:
			if failure.owner == nil {
				continue
			}
			previous, exists := occluding[failure.owner.Ref]
			if !exists || failure.ownerDepth > previous.depth ||
				(failure.ownerDepth == previous.depth && failure.ownerOrder < previous.order) {
				occluding[failure.owner.Ref] = tapStaticOwnerFact{
					depth: failure.ownerDepth, order: failure.ownerOrder,
				}
			}
		case tapStaticReasonAmbiguous:
			for _, node := range failure.related {
				if node == nil || node.Ref == "" {
					continue
				}
				ambiguous[node.Ref] = tapPublicOrder(view, node)
			}
		case tapStaticReasonOwnerMismatch:
			for _, node := range failure.related {
				if node == nil || node.Ref == "" {
					continue
				}
				owners[node.Ref] = tapPublicOrder(view, node)
			}
		}
	}
	reason := ""
	for kind := range categories {
		if reason == "" {
			reason = kind
			continue
		}
		reason = tapStaticReasonMixed
		break
	}
	if reason == "" {
		// The healthy scope always classifies at least one original candidate.
		// Keep the closed enum and do not invent a success for this path.
		reason = tapStaticReasonMixed
	}
	diagnosis := &tapStaticDiagnosis{
		reason:        reason,
		occludingRefs: tapStaticOccludingRefs(view, occluding),
		ambiguousRefs: tapStaticOrderedRefs(view, ambiguous),
		ownerRefs:     tapStaticOrderedRefs(view, owners),
	}
	diagnosis.suggestedPoint, diagnosis.suggestedCarrierRef =
		tapSuggestStaticPoint(view, scope, object, original)
	return diagnosis
}

func tapStaticScopeNoun(scope string) string {
	if scope == tapStaticScopeAncestor {
		return "提升祖先"
	}
	return "目标节点"
}

func tapStaticActionLabel(action protocol.Action) string {
	switch action {
	case protocol.ActionTap:
		return "点击"
	case protocol.ActionLongPress:
		return "长按"
	case protocol.ActionText:
		return "文本输入"
	case protocol.ActionSwipe:
		return "滑动"
	}
	return string(action)
}

// tapPublicActions returns the complete public action list in the fixed
// tap/long_press/text/swipe order, deduplicated and never nil.
func tapPublicActions(node *protocol.Node) []string {
	actions := make([]string, 0, 4)
	if node == nil {
		return actions
	}
	for _, action := range []protocol.Action{
		protocol.ActionTap, protocol.ActionLongPress,
		protocol.ActionText, protocol.ActionSwipe,
	} {
		if hasAction(node, action) {
			actions = append(actions, string(action))
		}
	}
	return actions
}

func tapActionsText(actions []string) string {
	if len(actions) == 0 {
		return "无"
	}
	labels := make([]string, 0, len(actions))
	for _, action := range actions {
		labels = append(labels, tapStaticActionLabel(protocol.Action(action)))
	}
	result := ""
	for index, label := range labels {
		if index > 0 {
			result += "、"
		}
		result += label
	}
	return result
}

func newTapInvalidTargetError(reason, scope, scopeRef string) *protocol.APIError {
	message := tapStaticScopeNoun(scope) + "没有可用几何"
	switch reason {
	case tapStaticReasonRoot:
		message = "root 节点不能作为操作目标"
	case tapStaticReasonContext:
		message = "context 节点不能作为操作目标"
	}
	return protocol.NewError(422, protocol.CodeInvalidTarget, message).
		WithDetail("reason", reason).
		WithDetail("scope", scope).
		WithDetail("scope_ref", scopeRef)
}

func newTapStaticUnsupportedError(reason string, action protocol.Action,
	targetActions, ancestorActions []string, ancestorRef string) *protocol.APIError {
	message := "目标节点不含点击、长按或文本输入能力"
	switch reason {
	case tapStaticReasonTargetHasOtherAction:
		message = fmt.Sprintf("目标节点不支持%s，公开动作：%s",
			tapStaticActionLabel(action), tapActionsText(targetActions))
	case tapStaticReasonAncestorMissingAction:
		message = fmt.Sprintf("最近的交互祖先不支持%s，公开动作：%s",
			tapStaticActionLabel(action), tapActionsText(ancestorActions))
	case tapStaticReasonAncestorInvalid:
		message = "最近的交互祖先不可用"
	case tapStaticReasonNoInteractiveAncestor:
		message = "没有可提升的交互祖先"
	}
	apiErr := protocol.NewError(422, protocol.CodeNodeUnsupported, message).
		WithDetail("reason", reason).
		WithDetail("target_actions", targetActions)
	if ancestorRef != "" {
		apiErr.WithDetail("ancestor_ref", ancestorRef)
	}
	if ancestorActions != nil {
		apiErr.WithDetail("ancestor_actions", ancestorActions)
	}
	return apiErr
}

func newTapNoSafePointError(action protocol.Action, scope, scopeRef,
	ancestorRef string, diagnosis *tapStaticDiagnosis) *protocol.APIError {
	reason := tapStaticReasonMixed
	suffix := "存在多类失败"
	if diagnosis != nil {
		reason = diagnosis.reason
		switch reason {
		case tapStaticReasonBlocked:
			suffix = "候选点均受阻塞"
		case tapStaticReasonAmbiguous:
			suffix = "候选点均有归属歧义"
		case tapStaticReasonOwnerMismatch:
			suffix = "候选点归属不符"
		}
	}
	message := fmt.Sprintf("未能从%s的固定候选中证明%s落点：%s",
		tapStaticScopeNoun(scope), tapStaticActionLabel(action), suffix)
	apiErr := protocol.NewError(422, protocol.CodeNoSafePoint, message).
		WithDetail("reason", reason).
		WithDetail("scope", scope).
		WithDetail("scope_ref", scopeRef)
	if ancestorRef != "" {
		apiErr.WithDetail("ancestor_ref", ancestorRef)
	}
	if diagnosis != nil {
		if len(diagnosis.occludingRefs) > 0 {
			apiErr.WithDetail("occluding_refs", diagnosis.occludingRefs)
		}
		if len(diagnosis.ambiguousRefs) > 0 {
			apiErr.WithDetail("ambiguous_refs", diagnosis.ambiguousRefs)
		}
		if len(diagnosis.ownerRefs) > 0 {
			apiErr.WithDetail("owner_refs", diagnosis.ownerRefs)
		}
		if diagnosis.suggestedPoint != nil {
			apiErr.WithDetail("suggested_point", map[string]any{
				"x": diagnosis.suggestedPoint.x, "y": diagnosis.suggestedPoint.y,
			})
			if diagnosis.suggestedCarrierRef != "" {
				apiErr.WithDetail("suggested_carrier_ref", diagnosis.suggestedCarrierRef)
			}
		}
	}
	return apiErr
}

// planBubbleTapTarget plans the one C->A target lift. A is the first
// touch-interactive ancestor; the plan carries both raw identities so the
// claim-time frame can re-prove both before any dispatch.
func planBubbleTapTarget(obs *observationState, view *tapPublicView,
	child, ancestor *protocol.Node, action protocol.Action, started time.Time) (*tapPlan, *protocol.APIError) {
	if obs == nil || view == nil || child == nil || ancestor == nil {
		return nil, tapHierarchyError()
	}
	// buildTapInput validates the full source map and fills view.byRaw for every
	// public node, so the child lookup below has its raw index.
	ancestorInput, apiErr := buildTapInput(obs, view, ancestor)
	if apiErr != nil {
		return nil, apiErr
	}
	childInfo := view.info[child]
	if childInfo == nil || !childInfo.hasSource || childInfo.rawIndex < 0 ||
		childInfo.rawIndex >= len(view.capture.Nodes) {
		return nil, tapTargetSourceMissing()
	}
	scope, apiErr := collectTapScope(ancestorInput)
	if apiErr != nil {
		if apiErr.Code == protocol.CodeNodeUnsupported {
			return nil, newTapInvalidTargetError(tapStaticReasonNoGeometry,
				tapStaticScopeAncestor, ancestor.Ref)
		}
		return nil, apiErr
	}
	x, y, strategy, carrier, ok := chooseBubbleTapPoint(scope, view, ancestor, child)
	if !ok {
		original := tapStaticBubbleCandidatePoints(scope, view, ancestor, child)
		diagnosis := diagnoseTapStaticFailure(view, scope, ancestor, true, original)
		return nil, newTapNoSafePointError(action, tapStaticScopeAncestor,
			ancestor.Ref, ancestor.Ref, diagnosis)
	}
	plan := &tapPlan{
		x: x, y: y, strategy: strategy, action: string(action),
		targetRef: child.Ref, targetRaw: &view.capture.Nodes[childInfo.rawIndex],
		bubbleAncestorRaw: ancestorInput.targetRaw,
		took:              time.Since(started),
	}
	if carrier != nil {
		plan.carrierRef = carrier.node.Ref
		plan.carrierRaw = carrier.raw
	}
	return plan, nil
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
	action := protocol.ActionTap
	if operation.Type == "long_press" {
		action = protocol.ActionLongPress
	}
	// Keep the baseline order: root, context, missing bounds, disabled,
	// action/lift, then the planner-owned source and geometry checks.
	if target.Role == protocol.RoleRoot {
		return nil, nil, nil, newTapInvalidTargetError(tapStaticReasonRoot,
			tapStaticScopeTarget, target.Ref)
	}
	if target.Context {
		return nil, nil, nil, newTapInvalidTargetError(tapStaticReasonContext,
			tapStaticScopeTarget, target.Ref)
	}
	if target.Bounds == nil {
		apiErr := newTapInvalidTargetError(tapStaticReasonNoGeometry,
			tapStaticScopeTarget, target.Ref)
		attachTapGuidance(apiErr, obs, view, target, action)
		return nil, nil, nil, apiErr
	}
	if target.Enabled != nil && !*target.Enabled {
		return nil, nil, nil, protocol.NewError(409, protocol.CodeNodeDisabled, "目标节点已禁用")
	}
	targetActions := tapPublicActions(target)
	if !hasAction(target, action) {
		if operation.EffectiveBubble() && !isTouchInteractive(target) {
			ancestor := nearestInteractiveAncestor(view, target)
			if ancestor == nil {
				apiErr := newTapStaticUnsupportedError(tapStaticReasonNoInteractiveAncestor,
					action, targetActions, nil, "")
				attachTapGuidance(apiErr, obs, view, target, action)
				return nil, nil, nil, apiErr
			}
			if ancestorActions := tapPublicActions(ancestor); ancestor.Enabled != nil && !*ancestor.Enabled ||
				ancestor.Bounds == nil || ancestor.Bounds.Width() <= 0 || ancestor.Bounds.Height() <= 0 {
				apiErr := newTapStaticUnsupportedError(tapStaticReasonAncestorInvalid,
					action, targetActions, ancestorActions, ancestor.Ref)
				attachTapGuidance(apiErr, obs, view, target, action)
				return nil, nil, nil, apiErr
			} else if !hasAction(ancestor, action) {
				apiErr := newTapStaticUnsupportedError(tapStaticReasonAncestorMissingAction,
					action, targetActions, ancestorActions, ancestor.Ref)
				attachTapGuidance(apiErr, obs, view, target, action)
				return nil, nil, nil, apiErr
			}
			bubblePlan, bubbleErr := planBubbleTapTarget(obs, view, target, ancestor, action, started)
			return nil, nil, bubblePlan, bubbleErr
		}
		reason := tapStaticReasonTargetMissingAction
		if isTouchInteractive(target) {
			reason = tapStaticReasonTargetHasOtherAction
		}
		apiErr := newTapStaticUnsupportedError(reason, action, targetActions, nil, "")
		attachTapGuidance(apiErr, obs, view, target, action)
		return nil, nil, nil, apiErr
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
			return nil, nil, plan, newTapInvalidTargetError(
				tapStaticReasonNoGeometry, tapStaticScopeTarget, target.Ref)
		}
		return nil, nil, nil, apiErr
	}
	plan, apiErr := chooseTapPlan(scope, input, target, action, started)
	if apiErr != nil {
		original := tapStaticCandidatePoints(scope, input.targetRaw)
		diagnosis := diagnoseTapStaticFailure(view, scope, target, false, original)
		plan.took = time.Since(started)
		return nil, nil, plan, newTapNoSafePointError(action,
			tapStaticScopeTarget, target.Ref, "", diagnosis)
	}
	return nil, nil, plan, nil
}

// staticTapRefCheckLegacy preserves the pre-taxonomy static tap behavior for
// the text focus tap. Text errors are outside the tap/long_press taxonomy.
func staticTapRefCheckLegacy(obs *observationState, operation *protocol.Operation) (
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
		if operation.EffectiveBubble() && !isTouchInteractive(target) {
			ancestor := nearestInteractiveAncestor(view, target)
			if ancestor == nil || !validTapPublicTarget(ancestor, action) {
				return nil, nil, nil, tapUnsupportedWithGuidance(obs, view, target, action,
					unsupported)
			}
			bubblePlan, bubbleErr := planBubbleTapTargetLegacy(obs, view, target, ancestor, action, started)
			return nil, nil, bubblePlan, bubbleErr
		}
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

func planBubbleTapTargetLegacy(obs *observationState, view *tapPublicView,
	child, ancestor *protocol.Node, action protocol.Action, started time.Time) (*tapPlan, *protocol.APIError) {
	if obs == nil || view == nil || child == nil || ancestor == nil {
		return nil, tapHierarchyError()
	}
	ancestorInput, apiErr := buildTapInput(obs, view, ancestor)
	if apiErr != nil {
		return nil, apiErr
	}
	childInfo := view.info[child]
	if childInfo == nil || !childInfo.hasSource || childInfo.rawIndex < 0 ||
		childInfo.rawIndex >= len(view.capture.Nodes) {
		return nil, tapTargetSourceMissing()
	}
	scope, apiErr := collectTapScope(ancestorInput)
	if apiErr != nil {
		return nil, apiErr
	}
	x, y, strategy, carrier, ok := chooseBubbleTapPoint(scope, view, ancestor, child)
	if !ok {
		return nil, tapNoCandidateError(action)
	}
	plan := &tapPlan{
		x: x, y: y, strategy: strategy, action: string(action),
		targetRef: child.Ref, targetRaw: &view.capture.Nodes[childInfo.rawIndex],
		bubbleAncestorRaw: ancestorInput.targetRaw,
		took:              time.Since(started),
	}
	if carrier != nil {
		plan.carrierRef = carrier.node.Ref
		plan.carrierRaw = carrier.raw
	}
	return plan, nil
}

// staticTextRefCheck keeps the text capability checks in the shared static
// content pass and plans the focus tap with the same target-subtree planner as
// tap. The returned plan carries the focus action; the text target itself is
// rechecked by the dispatch binder after the tap.
func staticTextRefCheck(obs *observationState, operation *protocol.Operation) (
	*pointCheck, *scrollPlan, *tapPlan, *protocol.APIError) {
	if operation == nil || operation.Type != "text" ||
		operation.Target == nil || !operation.Target.UseRef {
		return nil, nil, nil, protocol.Invalid("文字操作缺少 ref target")
	}
	point, scroll, apiErr := staticActContentCheck(obs, operation)
	if apiErr != nil {
		return point, scroll, nil, apiErr
	}
	focus := *operation
	focus.Type = "tap"
	focus.PressEnter, focus.HasPressEnter = false, false
	focus.Bubble, focus.HasBubble = false, true
	return staticTapRefCheckLegacy(obs, &focus)
}
