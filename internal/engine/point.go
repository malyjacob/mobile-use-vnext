package engine

import (
	"context"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// pointState is the fixed result of resolving one logical display point in a
// public tree. Unique and set results prove the deepest hit set on the fresh
// frame, an ambiguous point is rejected before claim, and none keeps the
// foreground-only behavior.
type pointState string

const (
	pointUnique    pointState = "unique"
	pointSet       pointState = "set"
	pointAmbiguous pointState = "ambiguous"
	pointNone      pointState = "none"
)

// pointCheck is the immutable result of resolving a coordinate target in the
// observation the caller actually read. It is computed before claim and carries
// only the point, its resolution state, the resolved raw source indices, mode
// and the pure-CPU cost of the proof; it never carries a ref or tree content.
type pointCheck struct {
	x, y    int
	state   pointState
	index   int
	indices []int
	mode    string
	// empty distinguishes a genuinely empty deepest hit set from an oversized
	// set that stays in the foreground-only fallback. An empty set is re-proved
	// as still empty on the fresh frame, so a point that was blank cannot later
	// be silently covered by a new node or overlay.
	empty bool
	took  time.Duration
}

// coordinatePoint returns the point a coordinate target must prove. tap,
// long_press and text prove their target point; swipe proves only its start
// point because the end point is direction and magnitude, not node identity.
// Ref targets and key operations have no point to resolve.
func coordinatePoint(operation *protocol.Operation) (x, y int, ok bool) {
	if operation == nil {
		return 0, 0, false
	}
	switch operation.Type {
	case "tap", "long_press", "text":
		if operation.Target != nil && !operation.Target.UseRef {
			return operation.Target.X, operation.Target.Y, true
		}
	case "swipe":
		if operation.From != nil {
			return operation.From.X, operation.From.Y, true
		}
	}
	return 0, 0, false
}

// resolvePointSet collects all non-root, non-context public nodes with
// positive-area half-open bounds containing (x, y) at the maximum depth in
// preorder. A visited guard prevents unbounded traversal on cyclic graphs.
func resolvePointSet(tree *protocol.Node, x, y int) []*protocol.Node {
	if tree == nil {
		return nil
	}
	bestDepth := -1
	var best []*protocol.Node
	visited := make(map[*protocol.Node]struct{})
	var walk func(node *protocol.Node, depth int)
	walk = func(node *protocol.Node, depth int) {
		if node == nil {
			return
		}
		if _, seen := visited[node]; seen {
			return
		}
		visited[node] = struct{}{}
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Width() > 0 && node.Bounds.Height() > 0 &&
			node.Bounds.Contains(x, y) {
			switch {
			case depth > bestDepth:
				bestDepth = depth
				best = []*protocol.Node{node}
			case depth == bestDepth:
				best = append(best, node)
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	walk(tree, 0)
	return best
}

// resolvePoint takes the deepest public node whose half-open bounds contain the
// point. Root and context nodes are not candidates; their descendants still
// are. Equally deep candidates cannot be ancestors of each other, so more than
// one means the point did not identify a node and the result is ambiguous.
func resolvePoint(tree *protocol.Node, x, y int) (string, pointState) {
	nodes := resolvePointSet(tree, x, y)
	switch len(nodes) {
	case 0:
		return "", pointNone
	case 1:
		return nodes[0].Ref, pointUnique
	default:
		return "", pointAmbiguous
	}
}

// resolveObservedPoint resolves the operation's coordinate target in the
// observation projection the caller read. Ambiguity is rejected here, before
// claim, capture or any device read; no node keeps the foreground-only path.
func resolveObservedPoint(obs *observationState, operation *protocol.Operation) (*pointCheck, *protocol.APIError) {
	x, y, ok := coordinatePoint(operation)
	if !ok {
		return nil, nil
	}
	started := time.Now()
	view, apiErr := collectTapPublicView(obs)
	if apiErr != nil {
		return nil, apiErr
	}
	if _, apiErr := validateTapRawTopology(obs.Projection.Capture); apiErr != nil {
		return nil, apiErr
	}
	hits := resolvePointSet(obs.Projection.Tree.Root, x, y)
	check := &pointCheck{x: x, y: y, state: pointNone, index: -1}
	switch len(hits) {
	case 0:
		check.empty = true
		check.took = time.Since(started)
		return check, nil
	case 1:
		index, mapped := obs.Projection.Source[hits[0].Ref]
		if mapped && index >= 0 && index < len(obs.Projection.Capture.Nodes) {
			check.state = pointUnique
			check.index = index
			check.indices = []int{index}
		}
		check.took = time.Since(started)
		return check, nil
	}

	if operation != nil && operation.Type == "swipe" {
		if len(hits) <= maxPointHitSet {
			if indices, mapped := collectPointRawIndices(obs, hits); mapped {
				check.state = pointSet
				check.indices = indices
				check.mode = pointModeSet
			}
		}
		check.took = time.Since(started)
		return check, nil
	}

	if len(hits) > maxPointHitSet {
		owners := collectPointOwners(view, hits)
		matching := filterMatchingPointOwners(owners, operation)
		candidates := matching
		if len(candidates) == 0 {
			candidates = owners
		}
		if len(candidates) == 0 {
			candidates = hits
		}
		check.state = pointAmbiguous
		check.took = time.Since(started)
		return check, newAmbiguousPointError(obs, view, operation, candidates)
	}

	owners := collectPointOwners(view, hits)
	if len(owners) <= 1 {
		if indices, mapped := collectPointRawIndices(obs, hits); mapped {
			check.state = pointSet
			check.indices = indices
			check.mode = pointModeSet
		}
		check.took = time.Since(started)
		return check, nil
	}

	matching := filterMatchingPointOwners(owners, operation)
	if len(matching) == 1 {
		if indices, mapped := collectPointRawIndices(obs, hits); mapped {
			check.state = pointSet
			check.indices = indices
			check.mode = pointModeDisambiguated
		}
		check.took = time.Since(started)
		return check, nil
	}

	candidates := matching
	if len(candidates) == 0 {
		candidates = owners
	}
	check.state = pointAmbiguous
	check.took = time.Since(started)
	return check, newAmbiguousPointError(obs, view, operation, candidates)
}

func collectPointRawIndices(obs *observationState, nodes []*protocol.Node) ([]int, bool) {
	if obs == nil || obs.Projection == nil || obs.Projection.Capture == nil || len(nodes) == 0 {
		return nil, false
	}
	rawCount := len(obs.Projection.Capture.Nodes)
	seen := make(map[int]struct{}, len(nodes))
	indices := make([]int, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Ref == "" {
			return nil, false
		}
		index, mapped := obs.Projection.Source[node.Ref]
		if !mapped || index < 0 || index >= rawCount {
			return nil, false
		}
		if _, duplicate := seen[index]; duplicate {
			continue
		}
		seen[index] = struct{}{}
		indices = append(indices, index)
	}
	if len(indices) == 0 {
		return nil, false
	}
	return indices, true
}

func nearestPointInteractiveOwner(view *tapPublicView, node *protocol.Node) *protocol.Node {
	if view == nil || node == nil {
		return nil
	}
	visited := make(map[*protocol.Node]struct{})
	for current := node; current != nil; {
		if _, seen := visited[current]; seen {
			return nil
		}
		visited[current] = struct{}{}
		if current.Role == protocol.RoleRoot {
			return nil
		}
		if !current.Context && isTouchInteractive(current) {
			return current
		}
		info := view.info[current]
		if info == nil {
			return nil
		}
		current = info.parent
	}
	return nil
}

func collectPointOwners(view *tapPublicView, hits []*protocol.Node) []*protocol.Node {
	seen := make(map[*protocol.Node]struct{}, len(hits))
	owners := make([]*protocol.Node, 0, len(hits))
	for _, node := range hits {
		owner := nearestPointInteractiveOwner(view, node)
		if owner == nil {
			continue
		}
		if _, duplicate := seen[owner]; duplicate {
			continue
		}
		seen[owner] = struct{}{}
		owners = append(owners, owner)
	}
	return owners
}

func pointOwnerMatchesOperation(node *protocol.Node, operation *protocol.Operation) bool {
	if node == nil || operation == nil {
		return false
	}
	switch operation.Type {
	case "tap":
		return validTapPublicTarget(node, protocol.ActionTap)
	case "long_press":
		return validTapPublicTarget(node, protocol.ActionLongPress)
	case "text":
		if node.Context || node.Role != protocol.RoleInput ||
			node.Bounds == nil || node.Bounds.Width() <= 0 || node.Bounds.Height() <= 0 {
			return false
		}
		if node.Enabled != nil && !*node.Enabled {
			return false
		}
		if node.Protected == nil || *node.Protected {
			return false
		}
		return hasAction(node, protocol.ActionText)
	default:
		return false
	}
}

func filterMatchingPointOwners(owners []*protocol.Node, operation *protocol.Operation) []*protocol.Node {
	matching := make([]*protocol.Node, 0, len(owners))
	for _, owner := range owners {
		if pointOwnerMatchesOperation(owner, operation) {
			matching = append(matching, owner)
		}
	}
	return matching
}

// Point hit-set matching bounds. The observed hit set is already capped by
// maxPointHitSet, so a Kuhn augmenting-path search over the resulting adjacency
// matrix stays bounded without touching the tree-scan budget.
const (
	actSwipePointMinOverlap   = 0.60
	actSwipePointMaxAreaRatio = 4
)

// maxBipartiteMatching returns the size of a maximum matching of the bipartite
// graph described by edges (observed rows x fresh columns). The hit-set vertex
// count is bounded by maxPointHitSet, so each augmenting search is bounded.
func maxBipartiteMatching(edges [][]bool) int {
	rows := len(edges)
	columns := 0
	if rows > 0 {
		columns = len(edges[0])
	}
	if rows == 0 || columns == 0 {
		return 0
	}
	matchedRow := make([]int, columns)
	for index := range matchedRow {
		matchedRow[index] = -1
	}
	seen := make([]bool, columns)
	var augment func(row int) bool
	augment = func(row int) bool {
		for column := 0; column < columns; column++ {
			if !edges[row][column] || seen[column] {
				continue
			}
			seen[column] = true
			if matchedRow[column] == -1 || augment(matchedRow[column]) {
				matchedRow[column] = row
				return true
			}
		}
		return false
	}
	total := 0
	for row := 0; row < rows; row++ {
		for column := range seen {
			seen[column] = false
		}
		if augment(row) {
			total++
		}
	}
	return total
}

// pointProof is the bounded context of one coordinate-target proof. It carries
// the observed and fresh captures, the screen, the STOP gate and the bounded
// budget; nothing here is published.
type pointProof struct {
	ctx      context.Context
	revoked  func() bool
	original *device.Capture
	fresh    *device.Capture
	screen   device.Rect
	budget   *matchBudget
	counts   *resourceCounts
	counted  bool
}

func newPointProof(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, screen device.Rect) *pointProof {
	limits := defaultActMatchLimits()
	candidateLimit := len(original.Nodes) + len(fresh.Nodes) +
		maxPointHitSet*maxPointHitSet
	return &pointProof{
		ctx: ctx, revoked: revoked, original: original, fresh: fresh,
		screen: screen,
		budget: &matchBudget{
			ctx: ctx, candidateLimit: candidateLimit,
			lineageLimit: limits.lineageLimit, lineageDepth: limits.lineageDepth,
			evidenceLimit: maxTier4EvidenceSteps,
		},
	}
}

func (proof *pointProof) cancelled() bool {
	return proof.revoked != nil && proof.revoked()
}

// gate applies the STOP and budget gate before the next matching row. A
// revocation marks the budget cancelled first, so the terminal mapping can only
// return STOP and never a content rejection.
func (proof *pointProof) gate() *protocol.APIError {
	if proof.cancelled() {
		proof.budget.fail(matchCancelled)
		return stoppedActRevalidation()
	}
	if !proof.budget.checkActive() {
		return pointProofTerminalError(proof.budget)
	}
	return nil
}

// pointProofTerminalError maps a stopped budget to the same categories the
// ref matcher uses: STOP wins over the hierarchy failure.
func pointProofTerminalError(budget *matchBudget) *protocol.APIError {
	switch budget.terminalState() {
	case actProbeCancelled:
		return stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	default:
		return targetChangedAtPoint()
	}
}

// swipePointNodeCompatible is the bare-swipe edge rule: source structure without
// passive leaf text, plus an anti-occlusion geometry gate. Focused, Selected,
// Checked and ancestor wrappers are deliberately ignored.
func swipePointNodeCompatible(oldRaw, newRaw *device.RawNode, x, y int,
	screen device.Rect) bool {
	if oldRaw == nil || newRaw == nil ||
		oldRaw.DisplayID != newRaw.DisplayID ||
		oldRaw.Package != newRaw.Package ||
		oldRaw.ClassName != newRaw.ClassName ||
		oldRaw.Editable != newRaw.Editable {
		return false
	}
	left, right := structureKeyOf(oldRaw), structureKeyOf(newRaw)
	if compareOptional(left.Resource, right.Resource) == triMismatch {
		return false
	}
	for _, pair := range [][2]tri{
		{left.Clickable, right.Clickable},
		{left.LongClickable, right.LongClickable},
		{left.Scrollable, right.Scrollable},
		{left.Enabled, right.Enabled},
		{left.Password, right.Password},
		{left.Checkable, right.Checkable},
	} {
		if compareTri(pair[0], pair[1]) == triMismatch {
			return false
		}
	}
	oldActive := knownTrue(left.Clickable) || knownTrue(left.LongClickable) ||
		knownTrue(left.Scrollable) || oldRaw.Editable
	newActive := knownTrue(right.Clickable) || knownTrue(right.LongClickable) ||
		knownTrue(right.Scrollable) || newRaw.Editable
	if oldActive || newActive {
		// An actionable node keeps its readable identity; only passive leaves
		// (for example a seconds-ticking progress text) may change their text.
		if compareOptional(left.Text, right.Text) == triMismatch ||
			compareOptional(left.Description, right.Description) == triMismatch {
			return false
		}
	}
	if !rectValidOnScreen(newRaw.Bounds, screen) || !newRaw.Bounds.Contains(x, y) {
		return false
	}
	if !sizeCompatible(oldRaw.Bounds, newRaw.Bounds) ||
		overlapRatio(oldRaw.Bounds, newRaw.Bounds) < actSwipePointMinOverlap {
		return false
	}
	oldArea, newArea := rectArea64(oldRaw.Bounds), rectArea64(newRaw.Bounds)
	if oldArea <= 0 || newArea <= 0 {
		return false
	}
	return oldArea*actSwipePointMaxAreaRatio >= newArea &&
		newArea*actSwipePointMaxAreaRatio >= oldArea
}

// pointTargetNodeCompatible is the coordinate tap/long_press/text edge rule: the
// target's own structure stays strictly equal and two geometry tiers are tried.
// Tier A accepts a stable raw lineage with relaxed geometry; tier B is the
// anti-rebuild fallback: tight bidirectional coverage plus strong semantics.
func (proof *pointProof) pointTargetNodeCompatible(
	oldIndex, newIndex, x, y int) (bool, bool) {
	if !proof.budget.consumeCandidate() {
		return false, false
	}
	oldNode := &proof.original.Nodes[oldIndex]
	newNode := &proof.fresh.Nodes[newIndex]
	if oldNode.DisplayID != newNode.DisplayID {
		return false, true
	}
	compatible, _ := sameStructure(oldNode, newNode)
	if !compatible {
		return false, true
	}
	if !rectValidOnScreen(newNode.Bounds, proof.screen) ||
		!newNode.Bounds.Contains(x, y) {
		return false, true
	}
	if sameRawLineageKeys(proof.original, oldIndex, proof.fresh, newIndex,
		proof.budget) {
		return geometryCompatible(oldNode.Bounds, newNode.Bounds, proof.screen,
			actTier2MinOverlap), true
	}
	if proof.budget.stop != matchProceed {
		return false, false
	}
	if !tightGeometryCompatible(oldNode.Bounds, newNode.Bounds, proof.screen) {
		return false, true
	}
	return proof.semanticEvidence(oldIndex, newIndex)
}

// semanticEvidence is E1, E2 or E3 between one observed and one fresh node.
func (proof *pointProof) semanticEvidence(oldIndex, newIndex int) (bool, bool) {
	oldNode := &proof.original.Nodes[oldIndex]
	newNode := &proof.fresh.Nodes[newIndex]
	if tier4SelfEvidence(oldNode, newNode) {
		return true, true
	}
	counts, ok := proof.resourceCounts()
	if !ok {
		return false, false
	}
	if tier4UniqueResource(oldNode, newNode, counts) {
		return true, true
	}
	return subtreeEvidence(proof.original, oldIndex, proof.fresh, newIndex,
		actMatchTapLike, proof.budget)
}

// resourceCounts censuses both trees once for E2.
func (proof *pointProof) resourceCounts() (*resourceCounts, bool) {
	if proof.counted {
		return proof.counts, proof.counts != nil
	}
	proof.counted = true
	original, ok := countResourceAnchors(proof.original, proof.budget)
	if !ok {
		return nil, false
	}
	fresh, ok := countResourceAnchors(proof.fresh, proof.budget)
	if !ok {
		return nil, false
	}
	proof.counts = &resourceCounts{original: original, fresh: fresh}
	return proof.counts, true
}

// proveSwipeSet accepts the bare swipe only when every observed hit node can be
// paired with a distinct structurally compatible fresh node whose geometry still
// covers the start point. A new deeper overlay changes the hit-set size or its
// structure and fails immediately. STOP is checked on every row and always wins
// over a content rejection.
func (proof *pointProof) proveSwipeSet(observedIndices, freshIndices []int,
	x, y int) *protocol.APIError {
	edges := make([][]bool, len(observedIndices))
	for row, oldIndex := range observedIndices {
		if oldIndex < 0 || oldIndex >= len(proof.original.Nodes) {
			return targetChangedAtPoint()
		}
		if apiErr := proof.gate(); apiErr != nil {
			return apiErr
		}
		edges[row] = make([]bool, len(freshIndices))
		for column, newIndex := range freshIndices {
			edges[row][column] = swipePointNodeCompatible(
				&proof.original.Nodes[oldIndex], &proof.fresh.Nodes[newIndex],
				x, y, proof.screen)
		}
	}
	if apiErr := proof.gate(); apiErr != nil {
		return apiErr
	}
	if maxBipartiteMatching(edges) != len(observedIndices) {
		return targetChangedAtPoint()
	}
	return nil
}

// proveTargetSet accepts a coordinate tap/long_press/text only when every
// observed hit node can be paired with a distinct fresh node under the strict
// target rule and the fresh point introduces no new competing owner. STOP is
// checked on every row and always wins over a content rejection.
func (proof *pointProof) proveTargetSet(observedIndices, freshIndices []int,
	x, y int, view *tapPublicView, freshNodes []*protocol.Node,
	operation *protocol.Operation) *protocol.APIError {
	edges := make([][]bool, len(observedIndices))
	for row, oldIndex := range observedIndices {
		if oldIndex < 0 || oldIndex >= len(proof.original.Nodes) {
			return targetChangedAtPoint()
		}
		if apiErr := proof.gate(); apiErr != nil {
			return apiErr
		}
		edges[row] = make([]bool, len(freshIndices))
		for column, newIndex := range freshIndices {
			compatible, complete := proof.pointTargetNodeCompatible(
				oldIndex, newIndex, x, y)
			if !complete {
				return pointProofTerminalError(proof.budget)
			}
			edges[row][column] = compatible
		}
	}
	if apiErr := proof.gate(); apiErr != nil {
		return apiErr
	}
	if maxBipartiteMatching(edges) != len(observedIndices) {
		return targetChangedAtPoint()
	}
	owners := collectPointOwners(view, freshNodes)
	if len(owners) > 1 &&
		len(filterMatchingPointOwners(owners, operation)) != 1 {
		return targetChangedAtPoint()
	}
	return nil
}

func rankPointCandidateNodes(view *tapPublicView, nodes []*protocol.Node, screen device.Rect) []*protocol.Node {
	type scoredNode struct {
		node  *protocol.Node
		area  int64
		order int
	}
	seen := make(map[string]struct{}, len(nodes))
	scored := make([]scoredNode, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Ref == "" || node.Role == protocol.RoleRoot || node.Context {
			continue
		}
		if _, duplicate := seen[node.Ref]; duplicate {
			continue
		}
		clipped, ok := publicNodeClippedRect(node, screen)
		if !ok {
			continue
		}
		seen[node.Ref] = struct{}{}
		scored = append(scored, scoredNode{
			node:  node,
			area:  rectArea64(clipped),
			order: tapPublicOrder(view, node),
		})
	}
	sort.Slice(scored, func(left, right int) bool {
		if scored[left].area != scored[right].area {
			return scored[left].area < scored[right].area
		}
		return scored[left].order < scored[right].order
	})
	ranked := make([]*protocol.Node, 0, len(scored))
	for _, item := range scored {
		ranked = append(ranked, item.node)
	}
	return ranked
}

func newAmbiguousPointError(obs *observationState, view *tapPublicView,
	operation *protocol.Operation, nodes []*protocol.Node) *protocol.APIError {
	apiErr := protocol.NewError(422, protocol.CodeAmbiguousPoint,
		"坐标落在多个竞争目标上，请改用候选 ref")
	apiErr.WithDetail("reason", staticReasonCompetingTargets)
	apiErr.WithDetail("scope", tapStaticScopePoint)
	if obs == nil || view == nil || operation == nil || len(nodes) == 0 {
		return apiErr
	}
	screen := screenRectFromDisplay(obs.Screen)
	ranked := rankPointCandidateNodes(view, nodes, screen)
	entries := make([]candidateEntry, 0, len(ranked))
	for _, node := range ranked {
		entries = append(entries, candidateEntry{
			node:     node,
			relation: candidateRelationCompeting,
		})
	}
	attachStaticCandidates(apiErr, buildStaticCandidates(obs, view, operation, entries))
	return apiErr
}

// indexOf returns the index of node in capture, or -1 when it is not a node of
// that capture. Identity matching alone never crosses captures by pointer.
func indexOf(capture *device.Capture, node *device.RawNode) int {
	if capture == nil || node == nil {
		return -1
	}
	for i := range capture.Nodes {
		if &capture.Nodes[i] == node {
			return i
		}
	}
	return -1
}
