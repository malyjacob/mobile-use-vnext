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
	x, y              int
	strategy          string
	action            string
	targetRef         string
	targetRaw         *device.RawNode
	carrierRef        string
	carrierRaw        *device.RawNode
	bubbleAncestorRaw *device.RawNode
	delegateRaw       *device.RawNode
	colocatedRaw      *device.RawNode
	colocatedInChild  bool
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
	tree            *protocol.Tree
	capture         *device.Capture
	source          map[string]int
	byRef           map[string]*protocol.Node
	info            map[*protocol.Node]*tapPublicInfo
	byRaw           map[int]*protocol.Node
	nodes           []*protocol.Node
	topology        *tapRawTopology
	sourceValidated bool
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
	return protocol.NewError(422, protocol.CodeInvalidTarget, "目标节点没有可用几何")
}

func tapNoCandidateError(action protocol.Action) *protocol.APIError {
	message := "无法在该节点作用域内生成可证明的点击落点"
	if action == protocol.ActionLongPress {
		message = "无法在该节点作用域内生成可证明的长按落点"
	}
	return protocol.NewError(422, protocol.CodeNoSafePoint, message)
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
		return nil, protocol.NewError(422, protocol.CodeInvalidTarget, "该节点不能作为操作目标")
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
	view.topology = topology
	view.sourceValidated = true
	return &tapInput{
		view: view, capture: obs.Projection.Capture, topology: topology,
		target: target, targetRaw: &obs.Projection.Capture.Nodes[index],
		targetIndex: index,
	}, nil
}

func ensureTapViewTopology(obs *observationState, view *tapPublicView) (*tapRawTopology, bool) {
	if view == nil {
		return nil, false
	}
	capture := view.capture
	if capture == nil && obs != nil && obs.Projection != nil {
		capture = obs.Projection.Capture
	}
	if capture == nil {
		return nil, false
	}
	if view.sourceValidated && view.topology != nil && view.topology.capture == capture {
		return view.topology, true
	}
	if view.source == nil && obs != nil && obs.Projection != nil {
		view.source = obs.Projection.Source
	}
	topology, apiErr := validateTapRawTopology(capture)
	if apiErr != nil {
		return nil, false
	}
	if checkTapSourceMapping(view, topology, -1) != tapSourceValid {
		return nil, false
	}
	view.topology = topology
	view.sourceValidated = true
	return topology, true
}

func tapInputFromValidatedView(view *tapPublicView, topology *tapRawTopology,
	target *protocol.Node) (*tapInput, bool) {
	if view == nil || topology == nil || topology.capture == nil || target == nil {
		return nil, false
	}
	info := view.info[target]
	if info == nil || !info.hasSource || info.rawIndex < 0 ||
		info.rawIndex >= len(topology.capture.Nodes) {
		return nil, false
	}
	if view.byRaw[info.rawIndex] != target {
		return nil, false
	}
	return &tapInput{
		view: view, capture: topology.capture, topology: topology,
		target: target, targetRaw: &topology.capture.Nodes[info.rawIndex],
		targetIndex: info.rawIndex,
	}, true
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
	if scope == nil {
		return scrollSpan{}, false
	}
	return tapRectCenterLineSpan(scope, scope.base, vertical, coordinate)
}

func tapRectCenterLineSpan(scope *tapScope, region device.Rect,
	vertical bool, coordinate int) (scrollSpan, bool) {
	if scope == nil || region.Empty() {
		return scrollSpan{}, false
	}
	var base scrollSpan
	if vertical {
		base = scrollSpan{start: region.Top, end: region.Bottom}
	} else {
		base = scrollSpan{start: region.Left, end: region.Right}
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
	if raw != nil {
		if x, y, ok := tapSafeCenter(raw.Bounds); ok {
			if !scope.base.Contains(x, y) {
				x, y, ok = tapSafeCenter(scope.base)
			}
			if ok {
				add(x, y, tapModeDefaultCenter, nil)
			}
		}
	} else if x, y, ok := tapSafeCenter(scope.base); ok {
		add(x, y, tapModeDefaultCenter, nil)
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
	for _, point := range tapStaticGridPoints(scope.base) {
		add(point.x, point.y, tapModeGrid3x3, nil)
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
	return plan, tapNoCandidateError(action)
}

func tryPlanTapDirectBase(obs *observationState, view *tapPublicView,
	node *protocol.Node, action protocol.Action) bool {
	if obs == nil || view == nil || !validTapPublicTarget(node, action) {
		return false
	}
	topology, ok := ensureTapViewTopology(obs, view)
	if !ok {
		return false
	}
	input, ok := tapInputFromValidatedView(view, topology, node)
	if !ok {
		return false
	}
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		return false
	}
	for _, candidate := range enumerateTapCandidates(scope, input.targetRaw) {
		if provesSubtreeTapPoint(scope, candidate.x, candidate.y) {
			return true
		}
	}
	return false
}

func tryPlanTextDirectBase(obs *observationState, view *tapPublicView,
	node *protocol.Node) bool {
	if obs == nil || view == nil || node == nil || node.Context || node.Role != protocol.RoleInput {
		return false
	}
	if !hasAction(node, protocol.ActionText) || !hasAction(node, protocol.ActionTap) {
		return false
	}
	if node.Enabled != nil && !*node.Enabled {
		return false
	}
	if node.Protected == nil || *node.Protected {
		return false
	}
	return tryPlanTapDirectBase(obs, view, node, protocol.ActionTap)
}

func externalInteractiveOwnersAt(view *tapPublicView, ancestor *protocol.Node,
	x, y int) []*protocol.Node {
	if view == nil || ancestor == nil {
		return nil
	}
	var owners []*protocol.Node
	for _, node := range view.nodes {
		if node == nil || node == ancestor || !isTouchInteractive(node) || node.Bounds == nil {
			continue
		}
		rect := rectFromBounds(node.Bounds)
		if rect.Width() <= 0 || rect.Height() <= 0 || !rect.Contains(x, y) {
			continue
		}
		if isPublicAncestor(view, ancestor, node) || isPublicAncestor(view, node, ancestor) {
			continue
		}
		owners = append(owners, node)
	}
	return owners
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
	if hit != ancestor {
		carrier := scope.carrierByRef[ref]
		if carrier == nil || carrier.node != hit {
			return false
		}
	}
	return len(externalInteractiveOwnersAt(view, ancestor, x, y)) == 0
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
func tapVisualRegionPoints(scope *tapScope, childRect, region device.Rect) []tapStaticPoint {
	if scope == nil || region.Empty() {
		return nil
	}
	var points []tapStaticPoint
	seen := map[[2]int]bool{}
	add := func(x, y int) {
		key := [2]int{x, y}
		if !region.Contains(x, y) || !scope.base.Contains(x, y) || seen[key] {
			return
		}
		seen[key] = true
		points = append(points, tapStaticPoint{x: x, y: y})
	}
	if x, y, ok := tapSafeCenter(childRect); ok {
		if !region.Contains(x, y) {
			x, y, ok = tapSafeCenter(region)
		}
		if ok {
			add(x, y)
		}
	} else if x, y, ok := tapSafeCenter(region); ok {
		add(x, y)
	}
	if region.Width() > 0 && region.Height() > 0 {
		midX := region.Left + region.Width()/2
		if span, ok := tapRectCenterLineSpan(scope, region, true, midX); ok {
			add(midX, span.start+(span.end-span.start)/2)
		}
		midY := region.Top + region.Height()/2
		if span, ok := tapRectCenterLineSpan(scope, region, false, midY); ok {
			add(span.start+(span.end-span.start)/2, midY)
		}
	}
	for _, point := range tapStaticGridPoints(region) {
		add(point.x, point.y)
	}
	return points
}

func hasCompetingControlOverlap(view *tapPublicView, scope *tapScope,
	ancestor *protocol.Node, probeRect device.Rect) bool {
	if view == nil || scope == nil || ancestor == nil || probeRect.Empty() {
		return false
	}
	for _, blocked := range scope.blocked {
		if !blocked.rect.Intersect(probeRect).Empty() {
			return true
		}
	}
	for _, node := range view.nodes {
		if node == nil || node == ancestor || !isTouchInteractive(node) || node.Bounds == nil {
			continue
		}
		rect := rectFromBounds(node.Bounds)
		if rect.Width() <= 0 || rect.Height() <= 0 || rect.Intersect(probeRect).Empty() {
			continue
		}
		if isPublicAncestor(view, ancestor, node) || isPublicAncestor(view, node, ancestor) {
			continue
		}
		return true
	}
	return false
}

func chooseBubbleTapPoint(scope *tapScope, view *tapPublicView,
	ancestor, child *protocol.Node) (int, int, string, *tapCarrier, bool) {
	if scope == nil || view == nil || ancestor == nil || child == nil {
		return 0, 0, "", nil, false
	}
	var childRect device.Rect
	if child.Bounds != nil {
		rect := rectFromBounds(child.Bounds)
		if rect.Width() > 0 && rect.Height() > 0 {
			childRect = rect
		}
	}
	cClip := childRect.Intersect(scope.base)
	if cClip.Empty() {
		return 0, 0, "", nil, false
	}
	tested := map[[2]int]bool{}
	tryPoint := func(x, y int) (int, int, string, *tapCarrier, bool) {
		key := [2]int{x, y}
		if tested[key] || !scope.base.Contains(x, y) {
			return 0, 0, "", nil, false
		}
		tested[key] = true
		if !provesGuidancePoint(view, ancestor, scope, x, y) {
			return 0, 0, "", nil, false
		}
		var carrier *tapCarrier
		if ref, state := resolvePoint(view.tree.Root, x, y); state == pointUnique && ref != ancestor.Ref {
			carrier = scope.carrierByRef[ref]
		}
		return x, y, tapModeBubbleAncestor, carrier, true
	}
	for _, point := range tapVisualRegionPoints(scope, childRect, cClip) {
		if x, y, strategy, carrier, ok := tryPoint(point.x, point.y); ok {
			return x, y, strategy, carrier, true
		}
	}
	if hasCompetingControlOverlap(view, scope, ancestor, cClip) {
		return 0, 0, "", nil, false
	}
	var ancestorRaw *device.RawNode
	if ancestorInfo := view.info[ancestor]; ancestorInfo != nil && ancestorInfo.hasSource &&
		ancestorInfo.rawIndex >= 0 && view.capture != nil &&
		ancestorInfo.rawIndex < len(view.capture.Nodes) {
		ancestorRaw = &view.capture.Nodes[ancestorInfo.rawIndex]
	}
	for _, candidate := range enumerateTapCandidates(scope, ancestorRaw) {
		if x, y, strategy, carrier, ok := tryPoint(candidate.x, candidate.y); ok {
			return x, y, strategy, carrier, true
		}
	}
	return 0, 0, "", nil, false
}

func chooseColocatedTapPoint(scope *tapScope, view *tapPublicView,
	child, candidate *protocol.Node, screen device.Rect) (x, y int, strategy string,
	carrier *tapCarrier, inChild bool, ok bool) {
	if scope == nil || view == nil || child == nil || candidate == nil || scope.base.Empty() {
		return 0, 0, "", nil, false, false
	}
	var childRect device.Rect
	if clipped, hasRect := publicNodeClippedRect(child, screen); hasRect {
		childRect = clipped
	} else if child.Bounds != nil {
		rect := rectFromBounds(child.Bounds)
		if rect.Width() > 0 && rect.Height() > 0 {
			childRect = rect
		}
	}
	if inter := childRect.Intersect(scope.base); !inter.Empty() {
		for _, point := range tapVisualRegionPoints(scope, childRect, inter) {
			if !provesSubtreeTapPoint(scope, point.x, point.y) {
				continue
			}
			owner, _ := pointOwner(scope, point.x, point.y)
			return point.x, point.y, tapModeBubbleColocated, owner, true, true
		}
	}
	var candidateRaw *device.RawNode
	if candidateInfo := view.info[candidate]; candidateInfo != nil && candidateInfo.hasSource &&
		candidateInfo.rawIndex >= 0 && view.capture != nil &&
		candidateInfo.rawIndex < len(view.capture.Nodes) {
		candidateRaw = &view.capture.Nodes[candidateInfo.rawIndex]
	}
	for _, option := range enumerateTapCandidates(scope, candidateRaw) {
		if !provesSubtreeTapPoint(scope, option.x, option.y) {
			continue
		}
		return option.x, option.y, tapModeBubbleColocated, option.carrier, false, true
	}
	return 0, 0, "", nil, false, false
}

func subtreeIntersectsRect(root *protocol.Node, base device.Rect) bool {
	if root == nil || base.Empty() {
		return false
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
			continue
		}
		visited[current] = struct{}{}
		if !current.Context && current.Bounds != nil {
			rect := rectFromBounds(current.Bounds)
			if rect.Width() > 0 && rect.Height() > 0 && !rect.Intersect(base).Empty() {
				return true
			}
		}
		for _, child := range current.Children {
			stack = append(stack, child)
		}
	}
	return false
}

func topLevelTapScopeBlockers(scope *tapScope, parent *protocol.Node) []*protocol.Node {
	if scope == nil || parent == nil || scope.base.Empty() {
		return nil
	}
	var blockers []*protocol.Node
	visited := make(map[*protocol.Node]struct{})
	visited[parent] = struct{}{}
	var walk func(node *protocol.Node)
	walk = func(node *protocol.Node) {
		if node == nil {
			return
		}
		if _, seen := visited[node]; seen {
			return
		}
		visited[node] = struct{}{}
		if isTouchInteractive(node) || node.Context {
			if subtreeIntersectsRect(node, scope.base) {
				blockers = append(blockers, node)
			}
			return
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, child := range parent.Children {
		walk(child)
	}
	return blockers
}

func isTopLevelCoextensiveBlocker(scope *tapScope, view *tapPublicView,
	parent, candidate *protocol.Node, screen device.Rect) bool {
	if scope == nil || view == nil || parent == nil || candidate == nil ||
		parent == candidate || candidate.Context || candidate.Role == protocol.RoleRoot {
		return false
	}
	if candidate.Enabled != nil && !*candidate.Enabled {
		return false
	}
	blockers := topLevelTapScopeBlockers(scope, parent)
	if len(blockers) != 1 || blockers[0] != candidate {
		return false
	}
	parentRect, ok := publicNodeClippedRect(parent, screen)
	if !ok {
		return false
	}
	candidateRect, ok := publicNodeClippedRect(candidate, screen)
	if !ok {
		return false
	}
	return isCoextensiveRect(candidateRect, parentRect)
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
// candidates. Node slices are deduplicated and ordered for unified candidate
// and blocking_refs construction.
type tapStaticDiagnosis struct {
	reason         string
	occludingNodes []*protocol.Node
	ambiguousNodes []*protocol.Node
	ownerNodes     []*protocol.Node
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
	hits := resolvePointSet(view.tree.Root, x, y)
	switch len(hits) {
	case 0:
		return nil, nil, pointNone
	case 1:
		return hits[0], nil, pointUnique
	default:
		return nil, hits, pointAmbiguous
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
	if hit != object {
		carrier := scope.carrierByRef[hit.Ref]
		if carrier == nil || carrier.node != hit {
			return tapStaticPointFailure{
				kind: tapStaticReasonOwnerMismatch, related: []*protocol.Node{hit},
			}
		}
	}
	if external := externalInteractiveOwnersAt(view, object, x, y); len(external) > 0 {
		return tapStaticPointFailure{
			kind: tapStaticReasonOwnerMismatch, related: external,
		}
	}
	return tapStaticPointFailure{}
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
// visual-region points and, when no competing control overlaps cClip, the
// ancestor's 4-tier candidate sequence.
func tapStaticBubbleCandidatePoints(scope *tapScope, view *tapPublicView,
	ancestor, child *protocol.Node) []tapStaticPoint {
	var points []tapStaticPoint
	if scope == nil || view == nil || ancestor == nil || child == nil {
		return points
	}
	var childRect device.Rect
	if child.Bounds != nil {
		rect := rectFromBounds(child.Bounds)
		if rect.Width() > 0 && rect.Height() > 0 {
			childRect = rect
		}
	}
	cClip := childRect.Intersect(scope.base)
	if cClip.Empty() {
		return points
	}
	points = append(points, tapVisualRegionPoints(scope, childRect, cClip)...)
	if hasCompetingControlOverlap(view, scope, ancestor, cClip) {
		return points
	}
	var ancestorRaw *device.RawNode
	if ancestorInfo := view.info[ancestor]; ancestorInfo != nil && ancestorInfo.hasSource &&
		ancestorInfo.rawIndex >= 0 && view.capture != nil &&
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
func tapStaticOrderedNodes(nodes map[string]*protocol.Node, orders map[string]int) []*protocol.Node {
	refs := make([]string, 0, len(nodes))
	for ref := range nodes {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(left, right int) bool {
		if orders[refs[left]] != orders[refs[right]] {
			return orders[refs[left]] < orders[refs[right]]
		}
		return refs[left] < refs[right]
	})
	out := make([]*protocol.Node, 0, len(refs))
	for _, ref := range refs {
		out = append(out, nodes[ref])
	}
	return out
}

func tapStaticOccludingNodes(nodes map[string]*protocol.Node,
	facts map[string]tapStaticOwnerFact) []*protocol.Node {
	refs := make([]string, 0, len(nodes))
	for ref := range nodes {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(left, right int) bool {
		leftFact, rightFact := facts[refs[left]], facts[refs[right]]
		if leftFact.depth != rightFact.depth {
			return leftFact.depth > rightFact.depth
		}
		if leftFact.order != rightFact.order {
			return leftFact.order < rightFact.order
		}
		return refs[left] < refs[right]
	})
	out := make([]*protocol.Node, 0, len(refs))
	for _, ref := range refs {
		out = append(out, nodes[ref])
	}
	return out
}

func diagnoseTapStaticFailure(view *tapPublicView, scope *tapScope,
	object *protocol.Node, bubble bool, original []tapStaticPoint) *tapStaticDiagnosis {
	occluding := map[string]tapStaticOwnerFact{}
	occludingByRef := map[string]*protocol.Node{}
	ambiguous := map[string]int{}
	ambiguousByRef := map[string]*protocol.Node{}
	owners := map[string]int{}
	ownersByRef := map[string]*protocol.Node{}
	categories := map[string]bool{}
	for _, point := range original {
		failure := classifyTapStaticPoint(view, scope, object, bubble, point.x, point.y)
		if failure.kind == "" {
			continue
		}
		categories[failure.kind] = true
		switch failure.kind {
		case tapStaticReasonBlocked:
			if failure.owner == nil || failure.owner.Ref == "" {
				continue
			}
			previous, exists := occluding[failure.owner.Ref]
			if !exists || failure.ownerDepth > previous.depth ||
				(failure.ownerDepth == previous.depth && failure.ownerOrder < previous.order) {
				occluding[failure.owner.Ref] = tapStaticOwnerFact{
					depth: failure.ownerDepth, order: failure.ownerOrder,
				}
				occludingByRef[failure.owner.Ref] = failure.owner
			}
		case tapStaticReasonAmbiguous:
			for _, node := range failure.related {
				if node == nil || node.Ref == "" {
					continue
				}
				ambiguous[node.Ref] = tapPublicOrder(view, node)
				ambiguousByRef[node.Ref] = node
			}
		case tapStaticReasonOwnerMismatch:
			for _, node := range failure.related {
				if node == nil || node.Ref == "" {
					continue
				}
				owners[node.Ref] = tapPublicOrder(view, node)
				ownersByRef[node.Ref] = node
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
	return &tapStaticDiagnosis{
		reason:         reason,
		occludingNodes: tapStaticOccludingNodes(occludingByRef, occluding),
		ambiguousNodes: tapStaticOrderedNodes(ambiguousByRef, ambiguous),
		ownerNodes:     tapStaticOrderedNodes(ownersByRef, owners),
	}
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
	apiErr := protocol.NewError(422, protocol.CodeInvalidTarget, message).
		WithDetail("reason", reason).
		WithDetail("scope", scope).
		WithDetail("scope_ref", scopeRef)
	if scope == tapStaticScopeAncestor && scopeRef != "" {
		apiErr.WithDetail("ancestor_ref", scopeRef)
	}
	return apiErr
}

func newTapStaticUnsupportedError(reason string, action protocol.Action,
	scope, scopeRef string, targetActions, ancestorActions []string,
	ancestorRef string) *protocol.APIError {
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
		WithDetail("scope", scope).
		WithDetail("scope_ref", scopeRef).
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
	ancestorRef, reason string) *protocol.APIError {
	if reason == "" {
		reason = tapStaticReasonMixed
	}
	suffix := "存在多类失败"
	switch reason {
	case tapStaticReasonBlocked:
		suffix = "候选点均受阻塞"
	case tapStaticReasonAmbiguous:
		suffix = "候选点均有归属歧义"
	case tapStaticReasonOwnerMismatch:
		suffix = "候选点归属不符"
	case staticReasonColocatedAmbiguous:
		message := fmt.Sprintf("目标位置存在多个可%s的共位候选，无法唯一确定落点",
			tapStaticActionLabel(action))
		apiErr := protocol.NewError(422, protocol.CodeNoSafePoint, message).
			WithDetail("reason", reason).
			WithDetail("scope", scope).
			WithDetail("scope_ref", scopeRef)
		if ancestorRef != "" {
			apiErr.WithDetail("ancestor_ref", ancestorRef)
		}
		return apiErr
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
	return apiErr
}

func pointDistSq64(x1, y1, x2, y2 int) int64 {
	dx := int64(x1) - int64(x2)
	dy := int64(y1) - int64(y2)
	return dx*dx + dy*dy
}

func nearestActionableTapAncestor(view *tapPublicView, target *protocol.Node,
	action protocol.Action) *protocol.Node {
	if view == nil || target == nil {
		return nil
	}
	info := view.info[target]
	if info == nil {
		return nil
	}
	visited := make(map[*protocol.Node]struct{})
	for parent := info.parent; parent != nil; {
		if _, seen := visited[parent]; seen {
			return nil
		}
		visited[parent] = struct{}{}
		parentInfo := view.info[parent]
		if parentInfo == nil || parent.Role == protocol.RoleRoot {
			return nil
		}
		if !parent.Context && validTapPublicTarget(parent, action) {
			return parent
		}
		parent = parentInfo.parent
	}
	return nil
}

func isValidTextPublicTarget(node *protocol.Node) bool {
	if node == nil || node.Context || node.Role != protocol.RoleInput {
		return false
	}
	if node.Bounds == nil || node.Bounds.Width() <= 0 || node.Bounds.Height() <= 0 {
		return false
	}
	if node.Enabled != nil && !*node.Enabled {
		return false
	}
	if node.Protected == nil || *node.Protected {
		return false
	}
	return hasAction(node, protocol.ActionText) && hasAction(node, protocol.ActionTap)
}

func collectTapFallbackCandidates(view *tapPublicView, target *protocol.Node,
	action protocol.Action, screen device.Rect, includeAncestor bool) []candidateEntry {
	if view == nil || target == nil || screen.Empty() {
		return nil
	}
	var entries []candidateEntry
	if includeAncestor {
		if ancestor := nearestActionableTapAncestor(view, target, action); ancestor != nil {
			entries = append(entries, candidateEntry{
				node:     ancestor,
				relation: candidateRelationAncestor,
			})
		}
	}
	targetRect, hasTargetRect := publicNodeClippedRect(target, screen)
	centerX, centerY, hasCenter := tapSafeCenter(targetRect)
	type scored struct {
		node           *protocol.Node
		relation       string
		containsCenter bool
		overlapArea    int64
		area           int64
		order          int
	}
	var nearby []scored
	for _, node := range view.nodes {
		if node == nil || node == target || !validTapPublicTarget(node, action) {
			continue
		}
		if isPublicAncestor(view, node, target) {
			continue
		}
		clipped, ok := publicNodeClippedRect(node, screen)
		if !ok {
			continue
		}
		isChild := isPublicAncestor(view, target, node)
		var overlapArea int64
		contains := false
		if hasTargetRect {
			inter := clipped.Intersect(targetRect)
			if inter.Empty() {
				continue
			}
			overlapArea = rectArea64(inter)
			contains = hasCenter && clipped.Contains(centerX, centerY)
		} else if !isChild {
			continue
		}
		relation := candidateRelationOverlapping
		if isChild {
			relation = candidateRelationChild
		}
		nearby = append(nearby, scored{
			node:           node,
			relation:       relation,
			containsCenter: contains,
			overlapArea:    overlapArea,
			area:           rectArea64(clipped),
			order:          tapPublicOrder(view, node),
		})
	}
	sort.Slice(nearby, func(left, right int) bool {
		if nearby[left].containsCenter != nearby[right].containsCenter {
			return nearby[left].containsCenter
		}
		if nearby[left].overlapArea != nearby[right].overlapArea {
			return nearby[left].overlapArea > nearby[right].overlapArea
		}
		if nearby[left].area != nearby[right].area {
			return nearby[left].area < nearby[right].area
		}
		return nearby[left].order < nearby[right].order
	})
	for _, item := range nearby {
		entries = append(entries, candidateEntry{
			node:     item.node,
			relation: item.relation,
		})
	}
	return entries
}

func collectTextFallbackCandidates(view *tapPublicView, target *protocol.Node,
	screen device.Rect) []candidateEntry {
	if view == nil || target == nil || screen.Empty() {
		return nil
	}
	var entries []candidateEntry
	if info := view.info[target]; info != nil {
		visited := make(map[*protocol.Node]struct{})
		for parent := info.parent; parent != nil; {
			if _, seen := visited[parent]; seen {
				break
			}
			visited[parent] = struct{}{}
			parentInfo := view.info[parent]
			if parentInfo == nil || parent.Role == protocol.RoleRoot {
				break
			}
			if isValidTextPublicTarget(parent) {
				entries = append(entries, candidateEntry{
					node:     parent,
					relation: candidateRelationAncestor,
				})
				break
			}
			parent = parentInfo.parent
		}
	}
	targetRect, hasTargetRect := publicNodeClippedRect(target, screen)
	centerX, centerY, hasCenter := tapSafeCenter(targetRect)
	type scored struct {
		node           *protocol.Node
		relation       string
		containsCenter bool
		overlapArea    int64
		area           int64
		order          int
	}
	var nearby []scored
	for _, node := range view.nodes {
		if node == nil || node == target || !isValidTextPublicTarget(node) {
			continue
		}
		if isPublicAncestor(view, node, target) {
			continue
		}
		clipped, ok := publicNodeClippedRect(node, screen)
		if !ok {
			continue
		}
		isChild := isPublicAncestor(view, target, node)
		var overlapArea int64
		contains := false
		if hasTargetRect {
			inter := clipped.Intersect(targetRect)
			if inter.Empty() {
				continue
			}
			overlapArea = rectArea64(inter)
			contains = hasCenter && clipped.Contains(centerX, centerY)
		} else if !isChild {
			continue
		}
		relation := candidateRelationOverlapping
		if isChild {
			relation = candidateRelationChild
		}
		nearby = append(nearby, scored{
			node:           node,
			relation:       relation,
			containsCenter: contains,
			overlapArea:    overlapArea,
			area:           rectArea64(clipped),
			order:          tapPublicOrder(view, node),
		})
	}
	sort.Slice(nearby, func(left, right int) bool {
		if nearby[left].containsCenter != nearby[right].containsCenter {
			return nearby[left].containsCenter
		}
		if nearby[left].overlapArea != nearby[right].overlapArea {
			return nearby[left].overlapArea > nearby[right].overlapArea
		}
		if nearby[left].area != nearby[right].area {
			return nearby[left].area < nearby[right].area
		}
		return nearby[left].order < nearby[right].order
	})
	for _, item := range nearby {
		entries = append(entries, candidateEntry{
			node:     item.node,
			relation: item.relation,
		})
	}
	return entries
}

func collectDirectBlockingDiagnostics(view *tapPublicView, target *protocol.Node,
	action protocol.Action, screen device.Rect, diagnosis *tapStaticDiagnosis) ([]candidateEntry, []string) {
	if view == nil || target == nil || diagnosis == nil {
		return nil, nil
	}
	targetRect, _ := publicNodeClippedRect(target, screen)
	targetCenterX, targetCenterY, hasCenter := tapSafeCenter(targetRect)
	type scoredChild struct {
		node  *protocol.Node
		dist2 int64
		area  int64
		order int
	}
	var childCandidates []scoredChild
	var blockingRefs []string
	seenCandidate := map[string]struct{}{}
	for _, owner := range diagnosis.occludingNodes {
		if owner == nil || owner.Ref == "" || owner == target {
			continue
		}
		if validTapPublicTarget(owner, action) {
			if _, dup := seenCandidate[owner.Ref]; dup {
				continue
			}
			clipped, ok := publicNodeClippedRect(owner, screen)
			if !ok {
				blockingRefs = append(blockingRefs, owner.Ref)
				continue
			}
			seenCandidate[owner.Ref] = struct{}{}
			var dist2 int64
			if hasCenter {
				if ownerCenterX, ownerCenterY, ok := tapSafeCenter(clipped); ok {
					dist2 = pointDistSq64(ownerCenterX, ownerCenterY, targetCenterX, targetCenterY)
				}
			}
			childCandidates = append(childCandidates, scoredChild{
				node:  owner,
				dist2: dist2,
				area:  rectArea64(clipped),
				order: tapPublicOrder(view, owner),
			})
		} else {
			blockingRefs = append(blockingRefs, owner.Ref)
		}
	}
	sort.Slice(childCandidates, func(left, right int) bool {
		if childCandidates[left].dist2 != childCandidates[right].dist2 {
			return childCandidates[left].dist2 < childCandidates[right].dist2
		}
		if childCandidates[left].area != childCandidates[right].area {
			return childCandidates[left].area < childCandidates[right].area
		}
		return childCandidates[left].order < childCandidates[right].order
	})
	entries := make([]candidateEntry, 0, len(childCandidates))
	for _, item := range childCandidates {
		entries = append(entries, candidateEntry{
			node:     item.node,
			relation: candidateRelationChild,
		})
	}
	for _, node := range diagnosis.ambiguousNodes {
		if node == nil || node.Ref == "" || node == target {
			continue
		}
		if _, isCand := seenCandidate[node.Ref]; isCand {
			continue
		}
		if !validTapPublicTarget(node, action) {
			blockingRefs = append(blockingRefs, node.Ref)
		}
	}
	return entries, blockingRefs
}

func planDirectTapRef(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, target *protocol.Node, action protocol.Action,
	allowCoextensiveChild bool, started time.Time) (*pointCheck, *scrollPlan, *tapPlan, *protocol.APIError) {
	input, apiErr := buildTapInput(obs, view, target)
	if apiErr != nil {
		return nil, nil, nil, apiErr
	}
	screen := screenRectFromDisplay(obs.Screen)
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		if apiErr.HTTPStatus == 422 {
			plan := &tapPlan{
				strategy: tapModeRejected, action: string(action),
				targetRef: target.Ref, targetRaw: input.targetRaw,
				took: time.Since(started),
			}
			errOut := newTapInvalidTargetError(
				tapStaticReasonNoGeometry, tapStaticScopeTarget, target.Ref)
			if operation != nil && operation.Type == "text" {
				attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation,
					collectTextFallbackCandidates(view, target, screen)))
			} else {
				attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation,
					collectTapFallbackCandidates(view, target, action, screen, true)))
			}
			return nil, nil, plan, errOut
		}
		return nil, nil, nil, apiErr
	}
	plan, apiErr := chooseTapPlan(scope, input, target, action, started)
	if apiErr == nil {
		return nil, nil, plan, nil
	}
	if allowCoextensiveChild {
		blockers := topLevelTapScopeBlockers(scope, target)
		if len(blockers) == 1 {
			delegate := blockers[0]
			if validTapPublicTarget(delegate, action) &&
				isTopLevelCoextensiveBlocker(scope, view, target, delegate, screen) {
				if delegateInput, ok := tapInputFromValidatedView(view, input.topology, delegate); ok {
					if delegateScope, scopeErr := collectTapScope(delegateInput); scopeErr == nil {
						if delegatePlan, planErr := chooseTapPlan(delegateScope, delegateInput,
							delegate, action, started); planErr == nil && delegatePlan != nil &&
							delegatePlan.strategy != tapModeRejected {
							delegated := &tapPlan{
								x:           delegatePlan.x,
								y:           delegatePlan.y,
								strategy:    tapModeCoextensiveChild,
								action:      string(action),
								targetRef:   target.Ref,
								targetRaw:   input.targetRaw,
								carrierRef:  delegatePlan.carrierRef,
								carrierRaw:  delegatePlan.carrierRaw,
								delegateRaw: delegateInput.targetRaw,
								took:        time.Since(started),
							}
							return nil, nil, delegated, nil
						}
					}
				}
			}
		}
	}
	original := tapStaticCandidatePoints(scope, input.targetRaw)
	diagnosis := diagnoseTapStaticFailure(view, scope, target, false, original)
	plan.took = time.Since(started)
	errOut := newTapNoSafePointError(action, tapStaticScopeTarget, target.Ref, "", diagnosis.reason)
	entries, blockingRefs := collectDirectBlockingDiagnostics(view, target, action, screen, diagnosis)
	attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
	attachStaticBlockingRefs(errOut, blockingRefs)
	return nil, nil, plan, errOut
}

type bubbleStepAStatus int

const (
	bubbleStepANoAncestor bubbleStepAStatus = iota
	bubbleStepAAncestorInvalid
	bubbleStepAAncestorMissingAction
	bubbleStepAAncestorNoGeometry
	bubbleStepANoSafePoint
)

func planBubbleOrColocated(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, child *protocol.Node, action protocol.Action,
	started time.Time) (*tapPlan, *protocol.APIError) {
	if obs == nil || view == nil || child == nil {
		return nil, tapHierarchyError()
	}
	screen := screenRectFromDisplay(obs.Screen)
	targetActions := tapPublicActions(child)

	ancestor := nearestInteractiveAncestor(view, child)
	status := bubbleStepANoAncestor
	var ancestorScope *tapScope
	if ancestor != nil {
		if ancestor.Enabled != nil && !*ancestor.Enabled ||
			ancestor.Bounds == nil || ancestor.Bounds.Width() <= 0 || ancestor.Bounds.Height() <= 0 {
			status = bubbleStepAAncestorInvalid
		} else if !hasAction(ancestor, action) {
			status = bubbleStepAAncestorMissingAction
		} else {
			ancestorInput, apiErr := buildTapInput(obs, view, ancestor)
			if apiErr != nil {
				if apiErr.Code == protocol.CodeHierarchyFailed ||
					apiErr.Code == protocol.CodeNodeNotFound {
					return nil, apiErr
				}
				status = bubbleStepAAncestorNoGeometry
			} else {
				childInfo := view.info[child]
				if childInfo == nil || !childInfo.hasSource || childInfo.rawIndex < 0 ||
					childInfo.rawIndex >= len(view.capture.Nodes) {
					return nil, tapTargetSourceMissing()
				}
				scope, scopeErr := collectTapScope(ancestorInput)
				if scopeErr != nil {
					if scopeErr.Code == protocol.CodeHierarchyFailed {
						return nil, scopeErr
					}
					status = bubbleStepAAncestorNoGeometry
				} else {
					ancestorScope = scope
					if x, y, strategy, carrier, ok := chooseBubbleTapPoint(
						scope, view, ancestor, child); ok {
						plan := &tapPlan{
							x:                 x,
							y:                 y,
							strategy:          strategy,
							action:            string(action),
							targetRef:         child.Ref,
							targetRaw:         &view.capture.Nodes[childInfo.rawIndex],
							bubbleAncestorRaw: ancestorInput.targetRaw,
							took:              time.Since(started),
						}
						if carrier != nil {
							plan.carrierRef = carrier.node.Ref
							plan.carrierRaw = carrier.raw
						}
						return plan, nil
					}
					status = bubbleStepANoSafePoint
				}
			}
		}
	}

	// Step B: co-located local clickable control absorption.
	type colocatedMatch struct {
		node           *protocol.Node
		input          *tapInput
		x, y           int
		strategy       string
		carrier        *tapCarrier
		inChild        bool
		clipped        device.Rect
		containsCenter bool
		overlapArea    int64
		area           int64
		order          int
	}
	var matched []colocatedMatch
	childRect, hasChildRect := publicNodeClippedRect(child, screen)
	centerX, centerY, hasCenter := tapSafeCenter(childRect)
	if topology, ok := ensureTapViewTopology(obs, view); ok && hasChildRect {
		for _, node := range view.nodes {
			if node == nil || node == child || !validTapPublicTarget(node, action) {
				continue
			}
			if !colocatedMatchGuard(view, child, node, screen) {
				continue
			}
			candInput, ok := tapInputFromValidatedView(view, topology, node)
			if !ok {
				continue
			}
			candScope, scopeErr := collectTapScope(candInput)
			if scopeErr != nil {
				continue
			}
			x, y, strategy, carrier, inChild, ok := chooseColocatedTapPoint(
				candScope, view, child, node, screen)
			if !ok {
				continue
			}
			candRect, _ := publicNodeClippedRect(node, screen)
			matched = append(matched, colocatedMatch{
				node:           node,
				input:          candInput,
				x:              x,
				y:              y,
				strategy:       strategy,
				carrier:        carrier,
				inChild:        inChild,
				clipped:        candRect,
				containsCenter: hasCenter && candRect.Contains(centerX, centerY),
				overlapArea:    rectArea64(candRect.Intersect(childRect)),
				area:           rectArea64(candRect),
				order:          tapPublicOrder(view, node),
			})
		}
	}
	folded := make([]colocatedMatch, 0, len(matched))
	for idx, candidate := range matched {
		superseded := false
		for otherIdx, other := range matched {
			if idx == otherIdx {
				continue
			}
			if isPublicAncestor(view, candidate.node, other.node) &&
				isCoextensiveRect(other.clipped, candidate.clipped) {
				superseded = true
				break
			}
		}
		if !superseded {
			folded = append(folded, candidate)
		}
	}

	buildColocatedPlan := func(chosen colocatedMatch) (*tapPlan, *protocol.APIError) {
		childInfo := view.info[child]
		if childInfo == nil || !childInfo.hasSource || childInfo.rawIndex < 0 ||
			childInfo.rawIndex >= len(view.capture.Nodes) {
			return nil, tapTargetSourceMissing()
		}
		plan := &tapPlan{
			x:                chosen.x,
			y:                chosen.y,
			strategy:         tapModeBubbleColocated,
			action:           string(action),
			targetRef:        child.Ref,
			targetRaw:        &view.capture.Nodes[childInfo.rawIndex],
			colocatedRaw:     chosen.input.targetRaw,
			colocatedInChild: chosen.inChild,
			took:             time.Since(started),
		}
		if chosen.carrier != nil {
			plan.carrierRef = chosen.carrier.node.Ref
			plan.carrierRaw = chosen.carrier.raw
		}
		return plan, nil
	}

	if len(folded) == 1 {
		return buildColocatedPlan(folded[0])
	}
	if len(folded) >= 2 {
		centerWinnerIndex := -1
		centerCount := 0
		for idx, candidate := range folded {
			if candidate.containsCenter {
				centerWinnerIndex = idx
				centerCount++
			}
		}
		if centerCount == 1 {
			return buildColocatedPlan(folded[centerWinnerIndex])
		}
		sort.Slice(folded, func(left, right int) bool {
			if folded[left].containsCenter != folded[right].containsCenter {
				return folded[left].containsCenter
			}
			if folded[left].overlapArea != folded[right].overlapArea {
				return folded[left].overlapArea > folded[right].overlapArea
			}
			if folded[left].area != folded[right].area {
				return folded[left].area < folded[right].area
			}
			return folded[left].order < folded[right].order
		})
		entries := make([]candidateEntry, 0, len(folded))
		for _, candidate := range folded {
			relation := candidateRelationOverlapping
			if isPublicAncestor(view, child, candidate.node) {
				relation = candidateRelationChild
			}
			entries = append(entries, candidateEntry{
				node:     candidate.node,
				relation: relation,
			})
		}
		errOut := newTapNoSafePointError(action, tapStaticScopeTarget, child.Ref, "",
			staticReasonColocatedAmbiguous)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
		return nil, errOut
	}

	// Step B found no match: report Step A failure with unified error details.
	fallback := collectTapFallbackCandidates(view, child, action, screen, false)
	switch status {
	case bubbleStepANoAncestor:
		errOut := newTapStaticUnsupportedError(tapStaticReasonNoInteractiveAncestor,
			action, tapStaticScopeTarget, child.Ref, targetActions, nil, "")
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, fallback))
		return nil, errOut
	case bubbleStepAAncestorInvalid:
		errOut := newTapStaticUnsupportedError(tapStaticReasonAncestorInvalid,
			action, tapStaticScopeAncestor, ancestor.Ref, targetActions,
			tapPublicActions(ancestor), ancestor.Ref)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, fallback))
		return nil, errOut
	case bubbleStepAAncestorMissingAction:
		errOut := newTapStaticUnsupportedError(tapStaticReasonAncestorMissingAction,
			action, tapStaticScopeAncestor, ancestor.Ref, targetActions,
			tapPublicActions(ancestor), ancestor.Ref)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, fallback))
		return nil, errOut
	case bubbleStepAAncestorNoGeometry:
		errOut := newTapInvalidTargetError(tapStaticReasonNoGeometry,
			tapStaticScopeAncestor, ancestor.Ref)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, fallback))
		return nil, errOut
	default:
		original := tapStaticBubbleCandidatePoints(ancestorScope, view, ancestor, child)
		diagnosis := diagnoseTapStaticFailure(view, ancestorScope, ancestor, true, original)
		errOut := newTapNoSafePointError(action, tapStaticScopeAncestor,
			ancestor.Ref, ancestor.Ref, diagnosis.reason)
		entries := append([]candidateEntry(nil), fallback...)
		var blockingRefs []string
		for _, owner := range diagnosis.occludingNodes {
			if owner == nil || owner.Ref == "" || owner == child || owner == ancestor {
				continue
			}
			if validTapPublicTarget(owner, action) {
				relation := candidateRelationOverlapping
				if isPublicAncestor(view, child, owner) || isPublicAncestor(view, ancestor, owner) {
					relation = candidateRelationChild
				}
				entries = append(entries, candidateEntry{node: owner, relation: relation})
			} else {
				blockingRefs = append(blockingRefs, owner.Ref)
			}
		}
		for _, owner := range diagnosis.ownerNodes {
			if owner == nil || owner.Ref == "" || owner == child || owner == ancestor {
				continue
			}
			if validTapPublicTarget(owner, action) {
				relation := candidateRelationOverlapping
				if isPublicAncestor(view, child, owner) {
					relation = candidateRelationChild
				}
				entries = append(entries, candidateEntry{node: owner, relation: relation})
			} else {
				blockingRefs = append(blockingRefs, owner.Ref)
			}
		}
		for _, node := range diagnosis.ambiguousNodes {
			if node == nil || node.Ref == "" || node == child || node == ancestor {
				continue
			}
			if !validTapPublicTarget(node, action) {
				blockingRefs = append(blockingRefs, node.Ref)
			}
		}
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
		attachStaticBlockingRefs(errOut, blockingRefs)
		return nil, errOut
	}
}

func staticTapRefCheck(obs *observationState, operation *protocol.Operation) (
	*pointCheck, *scrollPlan, *tapPlan, *protocol.APIError) {
	started := time.Now()
	view, apiErr := collectTapPublicView(obs)
	if apiErr != nil {
		return nil, nil, nil, apiErr
	}
	if _, apiErr := validateTapRawTopology(obs.Projection.Capture); apiErr != nil {
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
	screen := screenRectFromDisplay(obs.Screen)
	if target.Role == protocol.RoleRoot {
		return nil, nil, nil, newTapInvalidTargetError(tapStaticReasonRoot,
			tapStaticScopeTarget, target.Ref)
	}
	if target.Context {
		return nil, nil, nil, newTapInvalidTargetError(tapStaticReasonContext,
			tapStaticScopeTarget, target.Ref)
	}
	if target.Bounds == nil || target.Bounds.Width() <= 0 || target.Bounds.Height() <= 0 {
		errOut := newTapInvalidTargetError(tapStaticReasonNoGeometry,
			tapStaticScopeTarget, target.Ref)
		entries := collectTapFallbackCandidates(view, target, action, screen, true)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
		return nil, nil, nil, errOut
	}
	if target.Enabled != nil && !*target.Enabled {
		return nil, nil, nil, protocol.NewError(409, protocol.CodeNodeDisabled, "目标节点已禁用")
	}
	targetActions := tapPublicActions(target)
	if !hasAction(target, action) {
		if operation.EffectiveBubble() && !isTouchInteractive(target) {
			bubblePlan, bubbleErr := planBubbleOrColocated(obs, view, operation, target, action, started)
			return nil, nil, bubblePlan, bubbleErr
		}
		reason := tapStaticReasonTargetMissingAction
		if isTouchInteractive(target) {
			reason = tapStaticReasonTargetHasOtherAction
		}
		errOut := newTapStaticUnsupportedError(reason, action, tapStaticScopeTarget,
			target.Ref, targetActions, nil, "")
		entries := collectTapFallbackCandidates(view, target, action, screen, true)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
		return nil, nil, nil, errOut
	}
	return planDirectTapRef(obs, view, operation, target, action, true, started)
}

// staticTextRefCheck validates topology, unified structural/geometry/disabled
// rejections, text editability/protection checks, and direct 4-tier focus tap
// planning without coextensive-child delegation.
func staticTextRefCheck(obs *observationState, operation *protocol.Operation) (
	*pointCheck, *scrollPlan, *tapPlan, *protocol.APIError) {
	if operation == nil || operation.Type != "text" ||
		operation.Target == nil || !operation.Target.UseRef {
		return nil, nil, nil, protocol.Invalid("文字操作缺少 ref target")
	}
	started := time.Now()
	view, apiErr := collectTapPublicView(obs)
	if apiErr != nil {
		return nil, nil, nil, apiErr
	}
	if _, apiErr := validateTapRawTopology(obs.Projection.Capture); apiErr != nil {
		return nil, nil, nil, apiErr
	}
	target := view.byRef[operation.Target.Ref]
	if target == nil {
		return nil, nil, nil, protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点不存在")
	}
	screen := screenRectFromDisplay(obs.Screen)
	if target.Role == protocol.RoleRoot {
		return nil, nil, nil, newTapInvalidTargetError(tapStaticReasonRoot,
			tapStaticScopeTarget, target.Ref)
	}
	if target.Context {
		return nil, nil, nil, newTapInvalidTargetError(tapStaticReasonContext,
			tapStaticScopeTarget, target.Ref)
	}
	if target.Bounds == nil || target.Bounds.Width() <= 0 || target.Bounds.Height() <= 0 {
		errOut := newTapInvalidTargetError(tapStaticReasonNoGeometry,
			tapStaticScopeTarget, target.Ref)
		entries := collectTextFallbackCandidates(view, target, screen)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
		return nil, nil, nil, errOut
	}
	if target.Enabled != nil && !*target.Enabled {
		return nil, nil, nil, protocol.NewError(409, protocol.CodeNodeDisabled, "目标节点已禁用")
	}
	if target.Role != protocol.RoleInput || !hasAction(target, protocol.ActionText) {
		return nil, nil, nil, staticTextError(textReasonTargetNotEditable)
	}
	if target.Protected == nil || *target.Protected {
		return nil, nil, nil, staticTextError(textReasonTargetProtected)
	}
	if !hasAction(target, protocol.ActionTap) {
		errOut := newTapStaticUnsupportedError(tapStaticReasonTargetHasOtherAction,
			protocol.ActionTap, tapStaticScopeTarget, target.Ref,
			tapPublicActions(target), nil, "")
		entries := collectTextFallbackCandidates(view, target, screen)
		attachStaticCandidates(errOut, buildStaticCandidates(obs, view, operation, entries))
		return nil, nil, nil, errOut
	}
	return planDirectTapRef(obs, view, operation, target, protocol.ActionTap, false, started)
}
