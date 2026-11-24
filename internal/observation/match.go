package observation

import (
	"sort"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Continuity matching (design §5.2). The rules are deliberately conservative:
// a wrong match silently transfers a ref to a different target, which is worse
// than allocating a new ref and re-introducing the unit.
//
// Two invariants make the result independent of allocation order:
//   - candidates are compared through preorder positions in the previous tree
//     and raw indices in the capture, never through refs that may still be
//     unassigned;
//   - every match needs exactly one candidate on each side, judged against the
//     whole opposite side, so an earlier decision cannot make a later node look
//     unique.
//
// A ref is continued only with that unique candidate. Every ambiguous outcome
// allocates a new ref: never a nearest coordinate, never traversal order alone.
//
// Passes run as a deterministic six-stage pipeline:
//  1. unique anonymous structure top-down from synthetic root;
//  2. six content/identity passes (local exact, local resource-only, global
//     exact, local input anchor, local public, global public);
//  3. descendant-set container voting in two layers under one snapshot: 3A
//     prefers the single unmatched child of an already anchored parent, 3B
//     anchors self-bottom-up across rebuilt wrappers through the unique
//     container chain of the matched descendants' LCA;
//  4. dirty-queue local recovery under newly anchored parents;
//  5. single-pass zero-shift ordinal container recovery over every non-input
//     container bucket with an independent fixed point, followed by local-only
//     recovery;
//  6. single static-geometry pass over unmatched non-input leaves and
//     decorative icon containers under the same anchored parent (equal bucket
//     counts, +-4px bijection, order preserving).
//
// All passes work against snapshots and charge a deterministic work budget;
// exhaustion aborts the incomplete batch without partial application.

// Action bits used by shape matching. An actionable anonymous container is a
// potential act target, so it needs the stricter raw class/package agreement;
// a non-actionable wrapper may ignore those unpublished facts.
const (
	actionTap uint8 = 1 << iota
	actionLongPress
	actionText
	actionSwipe
)

// matchKey is the identity evidence of one public unit. It deliberately
// excludes input value, checked/focused/selected and bounds, so typing,
// ticking and scrolling never break identity by themselves. Non-input units
// keep label alongside resource id so same-resource different-label siblings
// remain distinct unless the local resource-only fallback runs.
type matchKey struct {
	role      protocol.Role
	class     string
	pkg       string
	resource  string
	hasRes    bool
	label     string // label signature; populated for non-input units even with a resource id
	anchor    string // input description, used to survive value edits
	actions   uint8  // tap/long_press/text/swipe bits
	editable  bool
	protected bool
}

// publicKey is the identity evidence visible in the published tree. It exists
// so a node can keep its ref when only an unpublished raw attribute (class,
// package or an unpublished resource id) changes. Raw class/package stay as
// tie-breakers in matchKey; they are not hard identity here.
type publicKey struct {
	role      protocol.Role
	label     string
	resource  string
	hasRes    bool
	anchor    string
	actions   uint8
	editable  bool
	protected bool
}

// signature builds the identity evidence for a raw node published with role.
// Only the published role is used, so a role change always yields a different
// signature and therefore a new ref.
func signature(raw *device.RawNode, role protocol.Role) matchKey {
	key := matchKey{
		role:     role,
		class:    raw.ClassName,
		pkg:      raw.Package,
		actions:  actionsMask(raw),
		editable: role == protocol.RoleInput,
	}
	if raw.ResourceSet && raw.ResourceID != "" {
		key.resource = raw.ResourceID
		key.hasRes = true
	}
	if raw.HasPassword && raw.Password {
		key.protected = true
	} else if role != protocol.RoleInput {
		// An input's own text is its value, never its identity label. Other
		// units preserve label alongside any resource id so distinct rows using
		// a template resource id keep independent identities.
		key.label = labelSignature(labelParts(raw))
	}
	if role == protocol.RoleInput && raw.DescSet && !(raw.HasPassword && raw.Password) {
		key.anchor = cleanReadableText(raw.Description)
	}
	return key
}

// actionsMask returns the public action bits of a raw node. It mirrors
// actionsOf so shape matching never invents an action the projection hides.
func actionsMask(raw *device.RawNode) uint8 {
	var mask uint8
	if raw.HasClickable && raw.Clickable {
		mask |= actionTap
	}
	if raw.HasLongClick && raw.LongClickable {
		mask |= actionLongPress
	}
	if raw.Editable {
		mask |= actionText
	}
	if raw.HasScrollable && raw.Scrollable {
		mask |= actionSwipe
	}
	return mask
}

// labelSignature joins label parts with a separator that cannot appear inside
// one part, so ["a","b"] and ["a\x00b"] stay distinct.
func labelSignature(parts []string) string {
	sig := ""
	for i, part := range parts {
		if i > 0 {
			sig += "\x00"
		}
		sig += part
	}
	return sig
}

// publicSignature builds the identity evidence visible in the public tree. A
// resource id is included only when the projection publishes it (an unlabelled
// interactive unit); otherwise the readable label is the public identity.
func publicSignature(raw *device.RawNode, role protocol.Role) publicKey {
	key := publicKey{
		role:      role,
		actions:   actionsMask(raw),
		editable:  role == protocol.RoleInput,
		protected: raw.HasPassword && raw.Password,
	}
	if role == protocol.RoleInput {
		if raw.DescSet && !key.protected {
			key.anchor = cleanReadableText(raw.Description)
		}
		return key
	}
	label := labelParts(raw)
	key.label = labelSignature(label)
	if raw.ResourceSet && raw.ResourceID != "" && needsResourceHint(raw, len(label) == 0) {
		key.resource = raw.ResourceID
		key.hasRes = true
	}
	return key
}

// contextSignature is the raw evidence of a context node. Context nodes are
// reading ancestry, never targets, so their signature carries no label,
// resource or protection evidence; class/package are tie-breakers only.
func contextSignature(raw *device.RawNode) matchKey {
	return matchKey{
		role:  protocol.RoleGroup,
		class: raw.ClassName,
		pkg:   raw.Package,
	}
}

type matchSource uint8

const (
	sourceNone matchSource = iota
	sourceRoot
	sourceStage1Shape
	sourceExact
	sourceResource
	sourceGlobalExact
	sourceInput
	sourceLocalPublic
	sourceGlobalPublic
	sourceDescendantVote
	sourceLocalStructure
	sourceLocalExact
	sourceLocalResource
	sourceLocalInput
	sourceLocalPublicPost
	sourceOrdinal
	sourcePostOrdinalLocal
	sourceBottomUpVote
	sourceStaticGeometry
)

type matchStats struct {
	workLimit        uint64
	workUsed         uint64
	candidateVisits  uint64
	voteComparisons  uint64
	budgetHit        bool
	stoppedStage     string
	rootMatched      bool
	totalMatched     int
	stage1Matched    int
	stage2Matched    int
	stage3Matched    int
	stage4Matched    int
	stage5Matched    int
	stage6Matched    int
	postLocalMatched int
}

type workBudget struct {
	limit           uint64
	remaining       uint64
	used            uint64
	candidateVisits uint64
	voteComparisons uint64
	hit             bool
}

func newWorkBudget(limit uint64) *workBudget {
	return &workBudget{
		limit:     limit,
		remaining: limit,
	}
}

func (w *workBudget) exhausted() bool {
	return w.hit
}

// visitCandidate counts one old or current public node actually examined as a
// matching candidate. It is diagnostic only and never participates in charge.
func (w *workBudget) visitCandidate() {
	w.candidateVisits++
}

// compareVoteMember counts one actual member comparison between descendant
// vote sets. It is diagnostic only and never participates in charge.
func (w *workBudget) compareVoteMember() {
	w.voteComparisons++
}

// charge consumes deterministic work units, returning false when insufficient
// remaining budget exists so the caller discards the pending unapplied batch.
func (w *workBudget) charge(work uint64) bool {
	if w.hit {
		return false
	}
	if work > w.remaining {
		w.used += w.remaining
		w.remaining = 0
		w.hit = true
		return false
	}
	w.remaining -= work
	w.used += work
	return true
}

const maxTransparentWrapperDepth = 8

// matchNodeFlags are evaluated bottom-up on the complete published hierarchy.
// singleChild uses the same node indices as the corresponding children slice.
type matchNodeFlags struct {
	isDecorativeImageLeaf         bool
	isAnonymousPlain              bool
	isPureDecorativeSubtree       bool
	isTransparentSingleWrapper    bool
	singleChild                   int
	isStaticIconContainer         bool
	isStaticIconGroupMember       bool
	isTransparentWrapperForStage6 bool
}

func classifyMatchNode(
	flags matchNodeFlags, staticIcon bool, children []int, flagsOf func(int) matchNodeFlags,
) matchNodeFlags {
	nonDecorative := 0
	allIconBranches := true
	for _, child := range children {
		childFlags := flagsOf(child)
		if childFlags.isPureDecorativeSubtree {
			continue
		}
		nonDecorative++
		flags.singleChild = child
		allIconBranches = allIconBranches && (childFlags.isStaticIconGroupMember || childFlags.isTransparentWrapperForStage6)
	}
	allDecorative := len(children) > 0 && nonDecorative == 0
	flags.isPureDecorativeSubtree = flags.isDecorativeImageLeaf || (flags.isAnonymousPlain && allDecorative)
	flags.isTransparentSingleWrapper = flags.isAnonymousPlain && nonDecorative == 1
	if !flags.isTransparentSingleWrapper {
		flags.singleChild = -1
	}
	flags.isStaticIconContainer = staticIcon && allDecorative
	flags.isStaticIconGroupMember = staticIcon && (len(children) == 0 || flags.isStaticIconContainer)
	flags.isTransparentWrapperForStage6 = flags.isAnonymousPlain && nonDecorative > 0 && allIconBranches
	return flags
}

func isAnonymousRole(role protocol.Role) bool {
	return role == protocol.RoleView || role == protocol.RoleGroup
}

// publicMatchIndex captures both the previous published snapshot and the current
// capture's published hierarchy with public-preorder Euler intervals.
type publicMatchIndex struct {
	prevRoot     int
	prevCount    int
	prevOrder    []int
	prevRef      []string
	prevParent   []int
	prevChildren [][]int
	prevTin      []int
	prevTout     []int
	prevKey      []matchKey
	prevPub      []publicKey
	prevContext  []bool
	prevBounds   []*protocol.Bounds
	prevFlags    []matchNodeFlags

	root        int
	curCount    int
	order       []int // current published raw indices in public DFS preorder
	curPos      []int // raw node index -> 0..curCount-1 (-1 if unpublished)
	curParent   []int // indexed by curPos -> raw parent index (-1 for root)
	curChildren [][]int
	tin         []int // indexed by raw node index
	tout        []int // indexed by raw node index
	curKey      []matchKey
	curPub      []publicKey
	curContext  []bool
	curBounds   []*protocol.Bounds
	curFlags    []matchNodeFlags
}

func (idx *publicMatchIndex) prevFlagsOf(pos int) matchNodeFlags { return idx.prevFlags[pos] }

func (idx *publicMatchIndex) curFlagsOf(cur int) matchNodeFlags { return idx.curFlags[idx.curPos[cur]] }

func buildPublicMatchIndex(prevSnap *Snapshot, cap *device.Capture, units []unit) *publicMatchIndex {
	idx := &publicMatchIndex{
		prevRoot: -1,
		root:     cap.Root,
	}

	if prevSnap != nil && prevSnap.Tree != nil && prevSnap.Tree.Root != nil {
		var walkCount func(node *protocol.Node) int
		walkCount = func(node *protocol.Node) int {
			count := 1
			for _, ch := range node.Children {
				count += walkCount(ch)
			}
			return count
		}
		prevCount := walkCount(prevSnap.Tree.Root)
		idx.prevRoot = 0
		idx.prevCount = prevCount
		idx.prevOrder = make([]int, 0, prevCount)
		idx.prevRef = make([]string, prevCount)
		idx.prevParent = make([]int, prevCount)
		idx.prevChildren = make([][]int, prevCount)
		idx.prevTin = make([]int, prevCount)
		idx.prevTout = make([]int, prevCount)
		idx.prevKey = make([]matchKey, prevCount)
		idx.prevPub = make([]publicKey, prevCount)
		idx.prevContext = make([]bool, prevCount)
		idx.prevBounds = make([]*protocol.Bounds, prevCount)
		idx.prevFlags = make([]matchNodeFlags, prevCount)

		cursor := 0
		var walkPrev func(node *protocol.Node, parentPos int)
		walkPrev = func(node *protocol.Node, parentPos int) {
			pos := cursor
			cursor++
			idx.prevOrder = append(idx.prevOrder, pos)
			idx.prevRef[pos] = node.Ref
			idx.prevParent[pos] = parentPos
			idx.prevTin[pos] = pos
			if parentPos >= 0 {
				idx.prevChildren[parentPos] = append(idx.prevChildren[parentPos], pos)
			}
			idx.prevContext[pos] = node.Context
			if !node.Context {
				idx.prevBounds[pos] = node.Bounds
			}
			if rawIdx, ok := prevSnap.Source[node.Ref]; ok {
				raw := &prevSnap.Capture.Nodes[rawIdx]
				idx.prevKey[pos] = signature(raw, node.Role)
				idx.prevPub[pos] = publicSignature(raw, node.Role)
			} else if rawIdx, ok := prevSnap.ContextSource[node.Ref]; ok {
				idx.prevKey[pos] = contextSignature(&prevSnap.Capture.Nodes[rawIdx])
			}
			for _, child := range node.Children {
				walkPrev(child, pos)
			}
			idx.prevTout[pos] = cursor - 1
			if pos != idx.prevRoot && !node.Context {
				k := idx.prevKey[pos]
				defaultState := visualKnownDefaultState(node)
				noContent := len(node.Label) == 0 && node.Value == nil && node.Hint == nil && node.Axis == ""
				flags := matchNodeFlags{
					isDecorativeImageLeaf: !k.editable && visualImageLeaf(node),
					isAnonymousPlain: isAnonymousRole(node.Role) && noContent && defaultState &&
						len(node.Actions) == 0 && !k.hasRes && node.Resource == nil,
				}
				staticIcon := node.Role != protocol.RoleInput && !k.editable && len(node.Actions) != 0 &&
					len(node.Label) == 0 && defaultState && positiveBounds(node.Bounds)
				idx.prevFlags[pos] = classifyMatchNode(flags, staticIcon, idx.prevChildren[pos], idx.prevFlagsOf)
			}
		}
		walkPrev(prevSnap.Tree.Root, -1)
	}

	rawOrder := preorder(cap)
	curPos := make([]int, len(cap.Nodes))
	for i := range curPos {
		curPos[i] = -1
	}
	tempChildren := make(map[int][]int)
	curParentByRaw := make(map[int]int)
	for _, rawIdx := range rawOrder {
		if rawIdx != cap.Root && !units[rawIdx].published {
			continue
		}
		if rawIdx == cap.Root {
			curParentByRaw[rawIdx] = -1
			continue
		}
		parent := nearestPublishedParent(cap, units, rawIdx)
		if parent < 0 || parent >= len(units) || (parent != cap.Root && !units[parent].published) {
			parent = cap.Root
		}
		curParentByRaw[rawIdx] = parent
		tempChildren[parent] = append(tempChildren[parent], rawIdx)
	}

	// Traverse published children from cap.Root so public order and Euler intervals
	// strictly reflect the published tree hierarchy regardless of raw slice order.
	order := make([]int, 0, len(curParentByRaw))
	tin := make([]int, len(cap.Nodes))
	tout := make([]int, len(cap.Nodes))
	curTimer := 0
	var walkCur func(cur int)
	walkCur = func(cur int) {
		rank := curTimer
		curTimer++
		curPos[cur] = rank
		order = append(order, cur)
		tin[cur] = rank
		for _, child := range tempChildren[cur] {
			walkCur(child)
		}
		tout[cur] = curTimer - 1
	}
	walkCur(cap.Root)

	curCount := len(order)
	idx.curCount = curCount
	idx.order = order
	idx.curPos = curPos
	idx.tin = tin
	idx.tout = tout
	idx.curParent = make([]int, curCount)
	idx.curChildren = make([][]int, curCount)
	idx.curKey = make([]matchKey, curCount)
	idx.curPub = make([]publicKey, curCount)
	idx.curContext = make([]bool, curCount)
	idx.curBounds = make([]*protocol.Bounds, curCount)
	idx.curFlags = make([]matchNodeFlags, curCount)

	// Reverse public preorder visits children before their parents, regardless
	// of where the capture stores each raw node.
	for rank := len(order) - 1; rank >= 0; rank-- {
		cur := order[rank]
		idx.curParent[rank] = curParentByRaw[cur]
		idx.curChildren[rank] = tempChildren[cur]
		if cur == cap.Root {
			continue
		}
		raw := &cap.Nodes[cur]
		if units[cur].context {
			idx.curContext[rank] = true
			idx.curKey[rank] = contextSignature(raw)
			continue
		}
		role := units[cur].role
		k, pk := signature(raw, role), publicSignature(raw, role)
		bounds := displayBounds(cap, raw)
		idx.curKey[rank], idx.curPub[rank], idx.curBounds[rank] = k, pk, bounds

		// Match newPublicNode's published fields: Value is input-only, Hint
		// has no source, and Axis is scroll-only. The public resource hint
		// may be absent even when a decorative ImageView has a raw ID.
		defaultState := raw.HasEnabled && raw.Enabled && raw.HasFocused && !raw.Focused &&
			raw.HasSelected && !raw.Selected && raw.HasPassword && !raw.Password && raw.HasChecked && !raw.Checked
		noContent := pk.label == "" && role != protocol.RoleInput && role != protocol.RoleScroll
		children := idx.curChildren[rank]
		flags := matchNodeFlags{
			isDecorativeImageLeaf: role == protocol.RoleImage && len(children) == 0 && !pk.editable &&
				noContent && pk.actions == 0 && !pk.hasRes && defaultState,
			isAnonymousPlain: isAnonymousRole(role) && noContent && pk.actions == 0 &&
				!k.hasRes && !pk.hasRes && defaultState,
		}
		staticIcon := role != protocol.RoleInput && !pk.editable && pk.actions != 0 &&
			pk.label == "" && defaultState && positiveBounds(bounds)
		idx.curFlags[rank] = classifyMatchNode(flags, staticIcon, children, idx.curFlagsOf)
	}

	return idx
}

func (idx *publicMatchIndex) hasPublicChildren(cur int) bool {
	pos := idx.curPos[cur]
	return pos >= 0 && len(idx.curChildren[pos]) > 0
}

func (idx *publicMatchIndex) parentOf(cur int) int {
	return idx.curParent[idx.curPos[cur]]
}

func (idx *publicMatchIndex) childrenOf(cur int) []int {
	return idx.curChildren[idx.curPos[cur]]
}

func (idx *publicMatchIndex) curMatchKey(cur int) matchKey {
	return idx.curKey[idx.curPos[cur]]
}

func (idx *publicMatchIndex) curPubKey(cur int) publicKey {
	return idx.curPub[idx.curPos[cur]]
}

// prevContainer reports whether prevPos is a published non-root node with at
// least one published child. Containers are the only candidates of descendant
// voting, ordinal recovery and container identity comparison.
func (idx *publicMatchIndex) prevContainer(prevPos int) bool {
	if prevPos <= idx.prevRoot || prevPos >= idx.prevCount {
		return false
	}
	return len(idx.prevChildren[prevPos]) > 0
}

// curContainer reports whether cur is a published non-root node with at least
// one published child.
func (idx *publicMatchIndex) curContainer(cur int) bool {
	pos := idx.curPos[cur]
	if pos <= 0 || cur == idx.root {
		return false
	}
	return len(idx.curChildren[pos]) > 0
}

func (idx *publicMatchIndex) prevEditable(prevPos int) bool {
	if prevPos < 0 || prevPos >= idx.prevCount {
		return false
	}
	return idx.prevKey[prevPos].editable
}

func (idx *publicMatchIndex) curEditable(cur int) bool {
	pos := idx.curPos[cur]
	if pos < 0 {
		return false
	}
	return idx.curKey[pos].editable
}

func (idx *publicMatchIndex) prevIsContext(prevPos int) bool {
	if prevPos < 0 || prevPos >= idx.prevCount {
		return false
	}
	return idx.prevContext[prevPos]
}

func (idx *publicMatchIndex) curIsContext(cur int) bool {
	pos := idx.curPos[cur]
	return pos >= 0 && idx.curContext[pos]
}

// prevAncestorOf reports whether candidate is an ancestor of node in the
// previous public tree, using the Euler intervals of the preorder walk.
func (idx *publicMatchIndex) prevAncestorOf(candidate, node int) bool {
	if candidate < 0 || candidate >= idx.prevCount || node < 0 || node >= idx.prevCount {
		return false
	}
	return idx.prevTin[candidate] <= idx.prevTin[node] && idx.prevTout[node] <= idx.prevTout[candidate]
}

// prevStaticLeaf reports whether prevPos is eligible for the static-geometry
// closing pass: an unmatched check is the caller's job, but the node itself
// must be a non-context, non-input public leaf or a decorative icon container
// with a known positive rectangle.
func (idx *publicMatchIndex) prevStaticLeaf(prevPos int) bool {
	if prevPos <= idx.prevRoot || prevPos >= idx.prevCount {
		return false
	}
	if idx.prevIsContext(prevPos) || idx.prevEditable(prevPos) || idx.prevKey[prevPos].role == protocol.RoleInput {
		return false
	}
	if len(idx.prevChildren[prevPos]) != 0 && !idx.prevFlagsOf(prevPos).isStaticIconContainer {
		return false
	}
	return positiveBounds(idx.prevBounds[prevPos])
}

// curStaticLeaf reports whether cur is eligible for the static-geometry closing
// pass, using the same requirements as prevStaticLeaf.
func (idx *publicMatchIndex) curStaticLeaf(cur int) bool {
	pos := idx.curPos[cur]
	if pos <= 0 || cur == idx.root {
		return false
	}
	if idx.curIsContext(cur) || idx.curEditable(cur) || idx.curKey[pos].role == protocol.RoleInput {
		return false
	}
	if len(idx.curChildren[pos]) != 0 && !idx.curFlagsOf(cur).isStaticIconContainer {
		return false
	}
	return positiveBounds(idx.curBounds[pos])
}

func positiveBounds(b *protocol.Bounds) bool {
	return b != nil && b.Right > b.Left && b.Bottom > b.Top
}

// directChildUnder maps oldPos to the direct published child of anchor whose
// subtree contains oldPos, returning false for anchor itself or non-descendants.
func (idx *publicMatchIndex) directChildUnder(oldPos, anchor int, budget *workBudget) (int, bool) {
	if oldPos <= idx.prevTin[anchor] || idx.prevTout[oldPos] > idx.prevTout[anchor] {
		return 0, false
	}
	children := idx.prevChildren[anchor]
	if len(children) == 0 {
		return 0, false
	}
	targetTin := idx.prevTin[oldPos]
	lo, hi := 0, len(children)-1
	for lo <= hi {
		if !budget.charge(1) {
			return 0, false
		}
		mid := lo + (hi-lo)/2
		cand := children[mid]
		if targetTin < idx.prevTin[cand] {
			hi = mid - 1
		} else if targetTin > idx.prevTout[cand] {
			lo = mid + 1
		} else {
			return cand, true
		}
	}
	return 0, false
}

func isStructurallyMatchable(k matchKey, pk publicKey) bool {
	return !k.editable && !hasPublicIdentity(pk) && k.label == ""
}

type pairProposal struct {
	prevPos int
	cur     int
	source  matchSource
}

type pairEntry struct {
	prevPos int
	cur     int
	curTin  int
}

type pairSnapshot struct {
	idx       *publicMatchIndex
	prevToCur []int
	curToOld  []int
	prevSrc   []matchSource
}

func (s pairSnapshot) oldUsed(oldPos int) bool {
	return oldPos < 0 || oldPos >= len(s.prevToCur) || s.prevToCur[oldPos] >= 0
}

func (s pairSnapshot) curUsed(cur int) bool {
	pos := s.idx.curPos[cur]
	return pos < 0 || s.curToOld[pos] >= 0
}

func (s pairSnapshot) pairOfOld(oldPos int) (int, bool) {
	if oldPos < 0 || oldPos >= len(s.prevToCur) {
		return 0, false
	}
	cur := s.prevToCur[oldPos]
	return cur, cur >= 0
}

func (s pairSnapshot) pairOfCur(cur int) (int, bool) {
	pos := s.idx.curPos[cur]
	if pos < 0 {
		return 0, false
	}
	oldPos := s.curToOld[pos]
	return oldPos, oldPos >= 0
}

func (s pairSnapshot) sourceOfOld(oldPos int) matchSource {
	if oldPos < 0 || oldPos >= len(s.prevSrc) {
		return sourceNone
	}
	return s.prevSrc[oldPos]
}

type matchState struct {
	idx       *publicMatchIndex
	refs      []string
	usedRef   map[string]bool
	prevToCur []int
	curToOld  []int
	prevSrc   []matchSource
	stats     matchStats
}

func newMatchState(idx *publicMatchIndex, refs []string) *matchState {
	prevToCur := make([]int, idx.prevCount)
	for i := range prevToCur {
		prevToCur[i] = -1
	}
	curToOld := make([]int, idx.curCount)
	for i := range curToOld {
		curToOld[i] = -1
	}
	return &matchState{
		idx:       idx,
		refs:      refs,
		usedRef:   make(map[string]bool, idx.curCount),
		prevToCur: prevToCur,
		curToOld:  curToOld,
		prevSrc:   make([]matchSource, idx.prevCount),
	}
}

func (st *matchState) prevUsed(oldPos int) bool {
	return oldPos < 0 || oldPos >= len(st.prevToCur) || st.prevToCur[oldPos] >= 0
}

func (st *matchState) curUsed(cur int) bool {
	pos := st.idx.curPos[cur]
	return pos < 0 || st.curToOld[pos] >= 0
}

func (st *matchState) matchedOld(cur int) (int, bool) {
	pos := st.idx.curPos[cur]
	if pos < 0 {
		return 0, false
	}
	oldPos := st.curToOld[pos]
	return oldPos, oldPos >= 0
}

func (st *matchState) matchCount() int {
	return st.stats.totalMatched
}

func (st *matchState) assign(cur, prevPos int, source matchSource) {
	curPos := st.idx.curPos[cur]
	ref := st.idx.prevRef[prevPos]
	st.refs[cur] = ref
	st.usedRef[ref] = true
	st.prevToCur[prevPos] = cur
	st.curToOld[curPos] = prevPos
	st.prevSrc[prevPos] = source

	switch source {
	case sourceRoot:
		st.stats.rootMatched = true
	case sourceStage1Shape:
		st.stats.stage1Matched++
		st.stats.totalMatched++
	case sourceExact, sourceResource, sourceGlobalExact, sourceInput, sourceLocalPublic, sourceGlobalPublic:
		st.stats.stage2Matched++
		st.stats.totalMatched++
	case sourceDescendantVote, sourceBottomUpVote:
		st.stats.stage3Matched++
		st.stats.totalMatched++
	case sourceLocalStructure, sourceLocalExact, sourceLocalResource, sourceLocalInput, sourceLocalPublicPost:
		st.stats.stage4Matched++
		st.stats.totalMatched++
	case sourceOrdinal:
		st.stats.stage5Matched++
		st.stats.totalMatched++
	case sourcePostOrdinalLocal:
		st.stats.postLocalMatched++
		st.stats.totalMatched++
	case sourceStaticGeometry:
		st.stats.stage6Matched++
		st.stats.totalMatched++
	}
}

func (st *matchState) snapshotPairs() pairSnapshot {
	prevCopy := make([]int, len(st.prevToCur))
	copy(prevCopy, st.prevToCur)
	curCopy := make([]int, len(st.curToOld))
	copy(curCopy, st.curToOld)
	srcCopy := make([]matchSource, len(st.prevSrc))
	copy(srcCopy, st.prevSrc)
	return pairSnapshot{
		idx:       st.idx,
		prevToCur: prevCopy,
		curToOld:  curCopy,
		prevSrc:   srcCopy,
	}
}

// chooseBatchIfBidirectionallyUnique validates every proposal endpoint against
// baseR and current assignments. Any duplicate prevPos/cur or already-used
// endpoint rejects the entire batch so partial conflicts never leak through.
func (st *matchState) chooseBatchIfBidirectionallyUnique(
	proposals []pairProposal,
	baseR pairSnapshot,
	budget *workBudget,
) ([]pairProposal, bool) {
	if len(proposals) == 0 {
		return nil, true
	}
	oldCounts := make(map[int]int, len(proposals))
	curCounts := make(map[int]int, len(proposals))
	for _, pr := range proposals {
		if !budget.charge(1) {
			return nil, false
		}
		if pr.prevPos < 0 || pr.prevPos >= st.idx.prevCount {
			return nil, true
		}
		if pr.cur < 0 || pr.cur >= len(st.idx.curPos) || st.idx.curPos[pr.cur] < 0 {
			return nil, true
		}
		if baseR.oldUsed(pr.prevPos) || baseR.curUsed(pr.cur) || st.prevUsed(pr.prevPos) || st.curUsed(pr.cur) {
			return nil, true
		}
		oldCounts[pr.prevPos]++
		curCounts[pr.cur]++
		if oldCounts[pr.prevPos] > 1 || curCounts[pr.cur] > 1 {
			return nil, true
		}
	}
	return proposals, true
}

func (st *matchState) applyBatch(accepted []pairProposal) {
	for _, pr := range accepted {
		st.assign(pr.cur, pr.prevPos, pr.source)
	}
}

type parentQueue struct {
	idx     *publicMatchIndex
	inQueue []bool
	items   []int
}

func newParentQueue(idx *publicMatchIndex) *parentQueue {
	return &parentQueue{
		idx:     idx,
		inQueue: make([]bool, idx.curCount),
	}
}

func (q *parentQueue) push(cur int) {
	pos := q.idx.curPos[cur]
	if pos < 0 || q.inQueue[pos] {
		return
	}
	q.inQueue[pos] = true
	q.items = append(q.items, cur)
}

func (q *parentQueue) drainInPublicOrder() []int {
	if len(q.items) == 0 {
		return nil
	}
	sort.Slice(q.items, func(i, j int) bool {
		return q.idx.tin[q.items[i]] < q.idx.tin[q.items[j]]
	})
	out := q.items
	q.items = nil
	for _, cur := range out {
		q.inQueue[q.idx.curPos[cur]] = false
	}
	return out
}

type bucketKind uint8

const (
	bucketUnit bucketKind = iota + 1
	bucketResourceOnly
	bucketInputAnchor
	bucketPublic
	bucketShape
	bucketContainerOrdinal
	bucketStaticLeaf
)

type bucketKey struct {
	kind      bucketKind
	role      protocol.Role
	class     string
	pkg       string
	hasRes    bool
	resource  string
	label     string
	anchor    string
	actions   uint8
	editable  bool
	protected bool
	// pubHasRes and pubResource keep the published resource hint separate from
	// the raw resource id. Buckets that decide container or leaf identity use
	// both so an unpublished difference can never be silently ignored.
	pubHasRes           bool
	pubResource         string
	decorativeContainer bool
}

func unitBucket(k matchKey, _ publicKey) bucketKey {
	return bucketKey{
		kind:      bucketUnit,
		role:      k.role,
		class:     k.class,
		pkg:       k.pkg,
		hasRes:    k.hasRes,
		resource:  k.resource,
		label:     k.label,
		anchor:    k.anchor,
		actions:   k.actions,
		editable:  k.editable,
		protected: k.protected,
	}
}

func resourceOnlyBucket(k matchKey, _ publicKey) bucketKey {
	return bucketKey{
		kind:      bucketResourceOnly,
		role:      k.role,
		class:     k.class,
		pkg:       k.pkg,
		hasRes:    k.hasRes,
		resource:  k.resource,
		actions:   k.actions,
		protected: k.protected,
	}
}

func inputAnchorBucket(k matchKey, _ publicKey) bucketKey {
	bk := bucketKey{
		kind:      bucketInputAnchor,
		role:      k.role,
		class:     k.class,
		pkg:       k.pkg,
		hasRes:    k.hasRes,
		actions:   k.actions,
		protected: k.protected,
	}
	if k.hasRes {
		bk.resource = k.resource
	} else {
		bk.anchor = k.anchor
	}
	return bk
}

func publicBucket(_ matchKey, pk publicKey) bucketKey {
	return bucketKey{
		kind:      bucketPublic,
		role:      pk.role,
		label:     pk.label,
		hasRes:    pk.hasRes,
		resource:  pk.resource,
		anchor:    pk.anchor,
		actions:   pk.actions,
		editable:  pk.editable,
		protected: pk.protected,
	}
}

func shapeBucket(k matchKey) bucketKey {
	bk := bucketKey{
		kind:      bucketShape,
		role:      k.role,
		actions:   k.actions,
		protected: k.protected,
	}
	if k.actions != 0 {
		bk.class = k.class
		bk.pkg = k.pkg
	}
	return bk
}

// containerOrdinalBucket groups non-input containers for the zero-shift
// ordinal pass. It carries the public identity evidence a reader can observe
// (published resource hint and readable label) plus the raw resource evidence,
// role, actions and protection. Actionable containers additionally keep raw
// class and package so two unrelated control rows never share an ordinal slot
// just because both are clickable.
func containerOrdinalBucket(k matchKey, pk publicKey) bucketKey {
	bk := bucketKey{
		kind:        bucketContainerOrdinal,
		role:        k.role,
		hasRes:      k.hasRes,
		resource:    k.resource,
		label:       k.label,
		actions:     k.actions,
		protected:   k.protected,
		pubHasRes:   pk.hasRes,
		pubResource: pk.resource,
	}
	if k.actions != 0 {
		bk.class = k.class
		bk.pkg = k.pkg
	}
	return bk
}

// staticLeafBucket uses ordinal identity evidence and keeps decorative icon
// containers separate from true leaves, even when the two have identical keys.
func staticLeafBucket(k matchKey, pk publicKey, decorativeContainer bool) bucketKey {
	bk := containerOrdinalBucket(k, pk)
	bk.kind = bucketStaticLeaf
	bk.decorativeContainer = decorativeContainer
	return bk
}

func hasIdentityMatch(k matchKey, _ publicKey) bool {
	return hasIdentity(k)
}

func eligibleForGlobalExactMatch(k matchKey, _ publicKey) bool {
	return eligibleForGlobalExact(k)
}

func isResourceOnlyEligible(k matchKey, _ publicKey) bool {
	return !k.editable && k.hasRes
}

func isInputAnchorEligible(k matchKey, _ publicKey) bool {
	return k.role == protocol.RoleInput && (k.hasRes || k.anchor != "")
}

func hasPublicNonInputIdentity(_ matchKey, pk publicKey) bool {
	return !pk.editable && hasPublicIdentity(pk)
}

type bucketUniverse uint8

const (
	localUnmatchedUniverse bucketUniverse = iota
	globalFullUniverse
)

type contentPass struct {
	universe bucketUniverse
	source   matchSource
	key      func(matchKey, publicKey) bucketKey
	eligible func(matchKey, publicKey) bool
}

// matchRefs assigns refs to the published units of cap, continuing an explicit
// previous snapshot through the six-stage continuity pipeline and allocating
// new refs strictly monotonically from nextRef. It never touches the builder's
// own continuity state: the caller keeps the returned high-water mark and match
// statistics only when the candidate is published.
func (b *Builder) matchRefs(
	prev *Snapshot,
	cap *device.Capture,
	units []unit,
	refs []string,
	nextRef int,
) (int, matchStats) {
	idx := buildPublicMatchIndex(prev, cap, units)
	st := newMatchState(idx, refs)

	limit := uint64(128*(idx.prevCount+idx.curCount+1) + 65_536)
	if b.hasBudgetLimit {
		limit = b.budgetLimit
	}
	budget := newWorkBudget(limit)

	allocate := func() string {
		for {
			ref := formatRef(nextRef)
			nextRef++
			if !st.usedRef[ref] {
				st.usedRef[ref] = true
				return ref
			}
		}
	}

	finish := func() (int, matchStats) {
		st.stats.workLimit = budget.limit
		st.stats.workUsed = budget.used
		st.stats.candidateVisits = budget.candidateVisits
		st.stats.voteComparisons = budget.voteComparisons
		st.stats.budgetHit = budget.exhausted()
		return nextRef, st.stats
	}

	if idx.prevRoot >= 0 {
		// Continue the synthetic root. Current root is cap.Root, never raw index 0.
		st.assign(idx.root, idx.prevRoot, sourceRoot)
	} else {
		refs[idx.root] = allocate()
		for _, cur := range idx.order {
			if refs[cur] == "" {
				refs[cur] = allocate()
			}
		}
		return finish()
	}

	dirty := newParentQueue(idx)
	if idx.hasPublicChildren(idx.root) {
		dirty.push(idx.root)
	}

	if !budget.charge(uint64(idx.prevCount + idx.curCount)) {
		st.stats.stoppedStage = "index"
	}

	// Stage 1: unique anonymous structure top-down from root.
	if !budget.exhausted() {
		matchUniqueContainers(st, budget, dirty)
		if budget.exhausted() {
			st.stats.stoppedStage = "unique_containers"
		}
	}

	// Stage 2: six content/identity passes separated by local vs global universe.
	if !budget.exhausted() {
		matchContentPasses(st, budget, dirty)
		if budget.exhausted() && st.stats.stoppedStage == "" {
			st.stats.stoppedStage = "content_passes"
		}
	}

	// Stage 3 finds container anchors via descendant vote (3A under anchored
	// parents, 3B bottom-up through rebuilt wrappers);
	// Stage 4 recovers direct children and single-branch identities under anchors.
	for !budget.exhausted() {
		before := st.matchCount()
		matchContainersByDescendants(st, budget, dirty)
		if budget.exhausted() {
			if st.stats.stoppedStage == "" {
				st.stats.stoppedStage = "descendant_vote"
			}
			break
		}
		matchLocalUnderAnchoredParents(st, budget, dirty, false)
		if budget.exhausted() {
			if st.stats.stoppedStage == "" {
				st.stats.stoppedStage = "local_recovery"
			}
			break
		}
		if st.matchCount() == before {
			break
		}
	}

	// Stage 5 runs once; ordinal results cannot seed further ordinal buckets.
	if !budget.exhausted() {
		matchOrdinalContainersZeroShift(st, budget, dirty)
		if budget.exhausted() && st.stats.stoppedStage == "" {
			st.stats.stoppedStage = "ordinal"
		}
	}

	// After ordinal, only local child recovery is permitted (no Stage 3/5 rerun).
	if !budget.exhausted() {
		matchLocalUnderAnchoredParents(st, budget, dirty, true)
		if budget.exhausted() && st.stats.stoppedStage == "" {
			st.stats.stoppedStage = "post_ordinal_local"
		}
	}

	// Stage 6 closes the static-geometry gap of unchanged separated leaves. It
	// runs once and never re-triggers an earlier stage.
	if !budget.exhausted() {
		matchStaticGeometrySiblings(st, budget)
		if budget.exhausted() && st.stats.stoppedStage == "" {
			st.stats.stoppedStage = "static_geometry"
		}
	}

	// Safe degradation assigns sequential fresh refs in public preorder.
	for _, cur := range idx.order {
		if refs[cur] == "" {
			refs[cur] = allocate()
		}
	}

	return finish()
}

func matchUniqueContainers(st *matchState, budget *workBudget, dirty *parentQueue) {
	if !st.idx.hasPublicChildren(st.idx.root) {
		return
	}
	frontier := []int{st.idx.root}
	for len(frontier) > 0 && !budget.exhausted() {
		baseR := st.snapshotPairs()
		offers, complete := matchParentScopedStructurallyUniqueContainers(st, frontier, sourceStage1Shape, budget)
		if !complete {
			return
		}
		accepted, ok := st.chooseBatchIfBidirectionallyUnique(offers, baseR, budget)
		if !ok || len(accepted) == 0 {
			return
		}
		st.applyBatch(accepted)
		var next []int
		for _, pr := range accepted {
			if st.idx.hasPublicChildren(pr.cur) {
				dirty.push(pr.cur)
				next = append(next, pr.cur)
			}
		}
		sort.Slice(next, func(i, j int) bool {
			return st.idx.tin[next[i]] < st.idx.tin[next[j]]
		})
		frontier = next
	}
}

func matchContentPasses(st *matchState, budget *workBudget, dirty *parentQueue) {
	passes := []contentPass{
		{localUnmatchedUniverse, sourceExact, unitBucket, hasIdentityMatch},
		{localUnmatchedUniverse, sourceResource, resourceOnlyBucket, isResourceOnlyEligible},
		{globalFullUniverse, sourceGlobalExact, unitBucket, eligibleForGlobalExactMatch},
		{localUnmatchedUniverse, sourceInput, inputAnchorBucket, isInputAnchorEligible},
		{localUnmatchedUniverse, sourceLocalPublic, publicBucket, hasPublicNonInputIdentity},
		{globalFullUniverse, sourceGlobalPublic, publicBucket, hasPublicNonInputIdentity},
	}
	for _, pass := range passes {
		if budget.exhausted() {
			return
		}
		baseR := st.snapshotPairs()
		offers, complete := uniqueBidirectionalBuckets(st, pass, baseR, budget)
		if !complete {
			return
		}
		accepted, ok := st.chooseBatchIfBidirectionallyUnique(offers, baseR, budget)
		if !ok {
			return
		}
		if len(accepted) == 0 {
			continue
		}
		st.applyBatch(accepted)
		for _, pr := range accepted {
			if st.idx.hasPublicChildren(pr.cur) {
				dirty.push(pr.cur)
			}
		}
	}
}

func uniqueBidirectionalBuckets(
	st *matchState,
	pass contentPass,
	baseR pairSnapshot,
	budget *workBudget,
) ([]pairProposal, bool) {
	var offers []pairProposal
	if pass.universe == localUnmatchedUniverse {
		for _, curParent := range st.idx.order {
			anchor, anchored := st.matchedOld(curParent)
			if !anchored {
				continue
			}
			oldBuckets := make(map[bucketKey][]int)
			curBuckets := make(map[bucketKey][]int)
			var keyOrder []bucketKey
			seen := make(map[bucketKey]bool)

			for _, oldPos := range st.idx.prevChildren[anchor] {
				if !budget.charge(1) {
					return nil, false
				}
				budget.visitCandidate()
				k, pk := st.idx.prevKey[oldPos], st.idx.prevPub[oldPos]
				if baseR.oldUsed(oldPos) || !pass.eligible(k, pk) {
					continue
				}
				key := pass.key(k, pk)
				oldBuckets[key] = append(oldBuckets[key], oldPos)
				if !seen[key] {
					seen[key] = true
					keyOrder = append(keyOrder, key)
				}
			}
			for _, cur := range st.idx.childrenOf(curParent) {
				if !budget.charge(1) {
					return nil, false
				}
				budget.visitCandidate()
				k, pk := st.idx.curMatchKey(cur), st.idx.curPubKey(cur)
				if baseR.curUsed(cur) || !pass.eligible(k, pk) {
					continue
				}
				key := pass.key(k, pk)
				curBuckets[key] = append(curBuckets[key], cur)
				if !seen[key] {
					seen[key] = true
					keyOrder = append(keyOrder, key)
				}
			}
			if !appendSingletonOffers(&offers, keyOrder, oldBuckets, curBuckets, baseR, pass.source, budget) {
				return nil, false
			}
		}
		return offers, true
	}

	// Global cardinality includes every eligible node in the complete trees
	// before consulting baseR, preventing local consumption from synthesizing a
	// unique global fallback for remaining duplicates.
	oldBuckets := make(map[bucketKey][]int)
	curBuckets := make(map[bucketKey][]int)
	var keyOrder []bucketKey
	seen := make(map[bucketKey]bool)

	for _, oldPos := range st.idx.prevOrder {
		if !budget.charge(1) {
			return nil, false
		}
		budget.visitCandidate()
		if oldPos == st.idx.prevRoot {
			continue
		}
		k, pk := st.idx.prevKey[oldPos], st.idx.prevPub[oldPos]
		if !pass.eligible(k, pk) {
			continue
		}
		key := pass.key(k, pk)
		oldBuckets[key] = append(oldBuckets[key], oldPos)
		if !seen[key] {
			seen[key] = true
			keyOrder = append(keyOrder, key)
		}
	}
	for _, cur := range st.idx.order {
		if !budget.charge(1) {
			return nil, false
		}
		budget.visitCandidate()
		if cur == st.idx.root {
			continue
		}
		k, pk := st.idx.curMatchKey(cur), st.idx.curPubKey(cur)
		if !pass.eligible(k, pk) {
			continue
		}
		key := pass.key(k, pk)
		curBuckets[key] = append(curBuckets[key], cur)
		if !seen[key] {
			seen[key] = true
			keyOrder = append(keyOrder, key)
		}
	}
	if !appendSingletonOffers(&offers, keyOrder, oldBuckets, curBuckets, baseR, pass.source, budget) {
		return nil, false
	}
	return offers, true
}

func appendSingletonOffers(
	offers *[]pairProposal,
	keyOrder []bucketKey,
	oldBuckets map[bucketKey][]int,
	curBuckets map[bucketKey][]int,
	baseR pairSnapshot,
	source matchSource,
	budget *workBudget,
) bool {
	for _, key := range keyOrder {
		if !budget.charge(1) {
			return false
		}
		oldList, curList := oldBuckets[key], curBuckets[key]
		if len(oldList) != 1 || len(curList) != 1 {
			continue
		}
		oldPos, cur := oldList[0], curList[0]
		if baseR.oldUsed(oldPos) || baseR.curUsed(cur) {
			continue
		}
		*offers = append(*offers, pairProposal{
			prevPos: oldPos,
			cur:     cur,
			source:  source,
		})
	}
	return true
}

func matchParentScopedStructurallyUniqueContainers(
	st *matchState,
	scope []int,
	source matchSource,
	budget *workBudget,
) ([]pairProposal, bool) {
	baseR := st.snapshotPairs()
	var offers []pairProposal
	for _, curParent := range scope {
		anchor, anchored := st.matchedOld(curParent)
		if !anchored {
			continue
		}
		oldBuckets := make(map[bucketKey][]int)
		curBuckets := make(map[bucketKey][]int)
		var keyOrder []bucketKey
		seen := make(map[bucketKey]bool)

		for _, oldPos := range st.idx.prevChildren[anchor] {
			if !budget.charge(1) {
				return nil, false
			}
			budget.visitCandidate()
			if !isStructurallyMatchable(st.idx.prevKey[oldPos], st.idx.prevPub[oldPos]) {
				continue
			}
			key := shapeBucket(st.idx.prevKey[oldPos])
			oldBuckets[key] = append(oldBuckets[key], oldPos)
			if !seen[key] {
				seen[key] = true
				keyOrder = append(keyOrder, key)
			}
		}
		for _, cur := range st.idx.childrenOf(curParent) {
			if !budget.charge(1) {
				return nil, false
			}
			budget.visitCandidate()
			k := st.idx.curMatchKey(cur)
			if !isStructurallyMatchable(k, st.idx.curPubKey(cur)) {
				continue
			}
			key := shapeBucket(k)
			curBuckets[key] = append(curBuckets[key], cur)
			if !seen[key] {
				seen[key] = true
				keyOrder = append(keyOrder, key)
			}
		}
		if !appendSingletonOffers(&offers, keyOrder, oldBuckets, curBuckets, baseR, source, budget) {
			return nil, false
		}
	}
	return offers, true
}

type localMatchScope struct {
	oldChildren, curChildren []int
}

func matchParentScopedUniqueBuckets(
	st *matchState,
	scope []localMatchScope,
	pass contentPass,
	baseR pairSnapshot,
	budget *workBudget,
	transparent bool,
) ([]pairProposal, bool) {
	eligible := func(k matchKey, pk publicKey) bool {
		return (!transparent || !k.editable || k.hasRes || k.anchor != "") && pass.eligible(k, pk)
	}
	var offers []pairProposal
	for _, children := range scope {
		oldBuckets := make(map[bucketKey][]int)
		curBuckets := make(map[bucketKey][]int)
		var keyOrder []bucketKey
		seen := make(map[bucketKey]bool)

		for _, oldPos := range children.oldChildren {
			if !budget.charge(1) {
				return nil, false
			}
			budget.visitCandidate()
			k, pk := st.idx.prevKey[oldPos], st.idx.prevPub[oldPos]
			if baseR.oldUsed(oldPos) || !eligible(k, pk) {
				continue
			}
			key := pass.key(k, pk)
			oldBuckets[key] = append(oldBuckets[key], oldPos)
			if !seen[key] {
				seen[key] = true
				keyOrder = append(keyOrder, key)
			}
		}
		for _, cur := range children.curChildren {
			if !budget.charge(1) {
				return nil, false
			}
			budget.visitCandidate()
			k, pk := st.idx.curMatchKey(cur), st.idx.curPubKey(cur)
			if baseR.curUsed(cur) || !eligible(k, pk) {
				continue
			}
			key := pass.key(k, pk)
			curBuckets[key] = append(curBuckets[key], cur)
			if !seen[key] {
				seen[key] = true
				keyOrder = append(keyOrder, key)
			}
		}
		if !appendSingletonOffers(&offers, keyOrder, oldBuckets, curBuckets, baseR, pass.source, budget) {
			return nil, false
		}
	}
	return offers, true
}

// collectLocalFrontier follows the single non-decorative branch of an
// unmatched wrapper. Ordinary direct candidates share the same identity
// buckets as reached descendants, so a wrapper cannot hide a duplicate.
func collectLocalFrontier(
	children []int, isUsed func(int) bool, flagsOf func(int) matchNodeFlags, budget *workBudget,
) (frontier []int, traversed bool) {
	for _, child := range children {
		if !budget.charge(1) {
			return nil, false
		}
		if isUsed(child) || flagsOf(child).isPureDecorativeSubtree {
			continue
		}
		node := child
		for depth := 0; depth < maxTransparentWrapperDepth && flagsOf(node).isTransparentSingleWrapper; depth++ {
			if !budget.charge(1) {
				return nil, false
			}
			node = flagsOf(node).singleChild
			if isUsed(node) {
				node = -1
				break
			}
		}
		if node >= 0 {
			frontier = append(frontier, node)
			traversed = traversed || node != child
		}
	}
	return frontier, traversed
}

type snapshotIntervals struct {
	byCurTin  []pairEntry
	byPrevPos []pairEntry
}

func buildSnapshotIntervals(idx *publicMatchIndex, baseR pairSnapshot) snapshotIntervals {
	var byCur, byPrev []pairEntry
	for _, cur := range idx.order {
		if cur == idx.root {
			continue
		}
		oldPos, matched := baseR.pairOfCur(cur)
		if !matched || oldPos == idx.prevRoot {
			continue
		}
		byCur = append(byCur, pairEntry{
			prevPos: oldPos,
			cur:     cur,
			curTin:  idx.tin[cur],
		})
	}
	byPrev = make([]pairEntry, 0, len(byCur))
	for _, oldPos := range idx.prevOrder {
		if oldPos == idx.prevRoot {
			continue
		}
		cur, matched := baseR.pairOfOld(oldPos)
		if !matched {
			continue
		}
		byPrev = append(byPrev, pairEntry{
			prevPos: oldPos,
			cur:     cur,
			curTin:  idx.tin[cur],
		})
	}
	return snapshotIntervals{byCurTin: byCur, byPrevPos: byPrev}
}

func (iv snapshotIntervals) collectByCurInterval(tinLo, tinHi int, budget *workBudget) ([]pairEntry, bool) {
	start := sort.Search(len(iv.byCurTin), func(i int) bool {
		return iv.byCurTin[i].curTin >= tinLo
	})
	end := sort.Search(len(iv.byCurTin), func(i int) bool {
		return iv.byCurTin[i].curTin > tinHi
	})
	count := end - start
	if count <= 0 {
		if !budget.charge(1) {
			return nil, false
		}
		return nil, true
	}
	if !budget.charge(uint64(count)) {
		return nil, false
	}
	out := make([]pairEntry, count)
	copy(out, iv.byCurTin[start:end])
	return out, true
}

func (iv snapshotIntervals) collectByPrevInterval(prevLo, prevHi int, budget *workBudget) ([]pairEntry, bool) {
	start := sort.Search(len(iv.byPrevPos), func(i int) bool {
		return iv.byPrevPos[i].prevPos >= prevLo
	})
	end := sort.Search(len(iv.byPrevPos), func(i int) bool {
		return iv.byPrevPos[i].prevPos > prevHi
	})
	count := end - start
	if count <= 0 {
		if !budget.charge(1) {
			return nil, false
		}
		return nil, true
	}
	if !budget.charge(uint64(count)) {
		return nil, false
	}
	return iv.byPrevPos[start:end], true
}

func samePairSet(rc, rp []pairEntry, budget *workBudget) (bool, bool) {
	if len(rc) == 0 || len(rc) != len(rp) {
		return false, true
	}
	if len(rc) == 1 {
		if !budget.charge(1) {
			return false, false
		}
		budget.compareVoteMember()
		return rc[0].prevPos == rp[0].prevPos && rc[0].curTin == rp[0].curTin, true
	}
	sort.Slice(rc, func(i, j int) bool {
		if rc[i].prevPos != rc[j].prevPos {
			return rc[i].prevPos < rc[j].prevPos
		}
		return rc[i].curTin < rc[j].curTin
	})
	for i := range rc {
		if !budget.charge(1) {
			return false, false
		}
		budget.compareVoteMember()
		if rc[i].prevPos != rp[i].prevPos || rc[i].curTin != rp[i].curTin {
			return false, true
		}
	}
	return true, true
}

// matchContainersByDescendants anchors unmatched containers from the pairs of
// already matched descendants below them (design §4.1.1). Both layers collect
// proposals against the same snapshot and commit them atomically:
//
//   - 3A: the current container's parent is already anchored, and every matched
//     descendant points into one and the same unmatched direct child of that
//     anchor whose full matched-pair set equals the current container's set.
//   - 3B: the current container's parent is not anchored (a rebuilt wrapper), so
//     the previous container is found self-bottom-up: the LCA of the descendants'
//     previous positions is climbed for old containers with the identical pair
//     set, the current container is climbed for unmatched containers with the
//     identical pair set, and only a single compatible pair on each side is
//     accepted.
func matchContainersByDescendants(st *matchState, budget *workBudget, dirty *parentQueue) bool {
	baseR := st.snapshotPairs()
	intervals := buildSnapshotIntervals(st.idx, baseR)
	var proposals []pairProposal

	// Stage 3A: an anchored parent names one expected previous child.
	for _, cur := range st.idx.order {
		if !budget.charge(1) {
			return false
		}
		budget.visitCandidate()
		if cur == st.idx.root || baseR.curUsed(cur) ||
			!st.idx.curContainer(cur) || st.idx.curEditable(cur) {
			continue
		}
		parent := st.idx.parentOf(cur)
		anchor, anchored := baseR.pairOfCur(parent)
		if !anchored {
			continue
		}
		rc, ok := intervals.collectByCurInterval(st.idx.tin[cur], st.idx.tout[cur], budget)
		if !ok {
			return false
		}
		if len(rc) == 0 {
			continue
		}

		expectedPrev := -1
		conflict := false
		for _, pr := range rc {
			if !budget.charge(1) {
				return false
			}
			budget.compareVoteMember()
			child, ok := st.idx.directChildUnder(pr.prevPos, anchor, budget)
			if budget.exhausted() {
				return false
			}
			if !ok {
				conflict = true
				break
			}
			if expectedPrev == -1 {
				expectedPrev = child
			} else if child != expectedPrev {
				conflict = true
				break
			}
		}
		if conflict || expectedPrev < 0 || baseR.oldUsed(expectedPrev) ||
			!st.idx.prevContainer(expectedPrev) || st.idx.prevEditable(expectedPrev) ||
			!sameContainerIdentity(
				st.idx.prevKey[expectedPrev], st.idx.curMatchKey(cur),
				st.idx.prevPub[expectedPrev], st.idx.curPubKey(cur),
			) {
			continue
		}

		rp, ok := intervals.collectByPrevInterval(st.idx.prevTin[expectedPrev], st.idx.prevTout[expectedPrev], budget)
		if !ok {
			return false
		}
		equal, ok := samePairSet(rc, rp, budget)
		if !ok {
			return false
		}
		if !equal {
			continue
		}
		proposals = append(proposals, pairProposal{
			cur:     cur,
			prevPos: expectedPrev,
			source:  sourceDescendantVote,
		})
	}

	// Stage 3B: bottom-up anchoring across rebuilt wrappers.
	for _, cur := range st.idx.order {
		if !budget.charge(1) {
			return false
		}
		budget.visitCandidate()
		if cur == st.idx.root || baseR.curUsed(cur) ||
			!st.idx.curContainer(cur) || st.idx.curEditable(cur) {
			continue
		}
		parent := st.idx.parentOf(cur)
		if parent >= 0 {
			if _, anchored := baseR.pairOfCur(parent); anchored {
				continue // 3A owns the anchored-parent layer.
			}
		}
		rc, ok := intervals.collectByCurInterval(st.idx.tin[cur], st.idx.tout[cur], budget)
		if !ok {
			return false
		}
		if len(rc) == 0 {
			continue
		}
		prevPos, ok, found := matchBottomUpContainer(st, intervals, cur, rc, budget)
		if !ok {
			return false
		}
		if !found {
			continue
		}
		proposals = append(proposals, pairProposal{
			cur:     cur,
			prevPos: prevPos,
			source:  sourceBottomUpVote,
		})
	}

	accepted, ok := st.chooseBatchIfBidirectionallyUnique(proposals, baseR, budget)
	if !ok || len(accepted) == 0 {
		return false
	}
	st.applyBatch(accepted)
	for _, pr := range accepted {
		if st.idx.hasPublicChildren(pr.cur) {
			dirty.push(pr.cur)
		}
	}
	return true
}

// matchBottomUpContainer finds the single previous container that belongs to an
// unanchored current container through the matched pairs below it. ok is false
// only when the work budget is exhausted; found reports whether one unique pair
// survived every check.
func matchBottomUpContainer(
	st *matchState,
	intervals snapshotIntervals,
	cur int,
	rc []pairEntry,
	budget *workBudget,
) (prevPos int, ok bool, found bool) {
	idx := st.idx

	// 1. LCA of every previous position of the matched descendant pairs.
	lca := rc[0].prevPos
	for _, pr := range rc[1:] {
		if !budget.charge(1) {
			return 0, false, false
		}
		for lca > idx.prevRoot && !idx.prevAncestorOf(lca, pr.prevPos) {
			if !budget.charge(1) {
				return 0, false, false
			}
			lca = idx.prevParent[lca]
			if lca < 0 {
				return 0, true, false
			}
		}
		if lca <= idx.prevRoot {
			// The pairs only meet at the synthetic root: no container chain.
			return 0, true, false
		}
	}

	// 2a. Old containers on the LCA chain whose matched-pair set is identical.
	var prevChain []int
	for p := lca; p > idx.prevRoot && p >= 0; p = idx.prevParent[p] {
		if !budget.charge(1) {
			return 0, false, false
		}
		if st.prevUsed(p) || idx.prevEditable(p) || !idx.prevContainer(p) {
			continue
		}
		rp, complete := intervals.collectByPrevInterval(idx.prevTin[p], idx.prevTout[p], budget)
		if !complete {
			return 0, false, false
		}
		equal, complete := samePairSet(rc, rp, budget)
		if !complete {
			return 0, false, false
		}
		if equal {
			prevChain = append(prevChain, p)
		}
	}
	if len(prevChain) == 0 {
		return 0, true, false
	}

	// 2b. Unmatched current containers on the chain above cur with the same
	// matched-pair set. A matched parent stops the climb.
	var curChain []int
	for c := cur; c >= 0 && c != idx.root; {
		if st.curUsed(c) || idx.curEditable(c) || !idx.curContainer(c) {
			break
		}
		rp, complete := intervals.collectByCurInterval(idx.tin[c], idx.tout[c], budget)
		if !complete {
			return 0, false, false
		}
		equal, complete := samePairSet(rc, rp, budget)
		if !complete {
			return 0, false, false
		}
		if !equal {
			break
		}
		curChain = append(curChain, c)
		parent := idx.parentOf(c)
		if parent == idx.root || st.curUsed(parent) {
			break
		}
		c = parent
	}
	if len(curChain) == 0 {
		return 0, true, false
	}

	// 3. Exactly one compatible old candidate, and exactly one compatible
	// current candidate which must be cur itself.
	prevCand := -1
	for _, p := range prevChain {
		if !budget.charge(1) {
			return 0, false, false
		}
		budget.visitCandidate()
		if !sameContainerIdentity(
			idx.prevKey[p], idx.curMatchKey(cur),
			idx.prevPub[p], idx.curPubKey(cur),
		) {
			continue
		}
		if prevCand >= 0 {
			return 0, true, false // nested same-signature shells: no guess.
		}
		prevCand = p
	}
	if prevCand < 0 {
		return 0, true, false
	}

	curCand := -1
	for _, c := range curChain {
		if !budget.charge(1) {
			return 0, false, false
		}
		budget.visitCandidate()
		if !sameContainerIdentity(
			idx.prevKey[prevCand], idx.curMatchKey(c),
			idx.prevPub[prevCand], idx.curPubKey(c),
		) {
			continue
		}
		if curCand >= 0 {
			return 0, true, false
		}
		curCand = c
	}
	if curCand != cur {
		return 0, true, false
	}
	return prevCand, true, true
}

func matchLocalUnderAnchoredParents(
	st *matchState,
	budget *workBudget,
	dirty *parentQueue,
	postOrdinal bool,
) bool {
	changed := false
	resolveSource := func(src matchSource) matchSource {
		if postOrdinal {
			return sourcePostOrdinalLocal
		}
		return src
	}
	passes := []contentPass{
		{localUnmatchedUniverse, resolveSource(sourceLocalExact), unitBucket, hasIdentityMatch},
		{localUnmatchedUniverse, resolveSource(sourceLocalResource), resourceOnlyBucket, isResourceOnlyEligible},
		{localUnmatchedUniverse, resolveSource(sourceLocalInput), inputAnchorBucket, isInputAnchorEligible},
		{localUnmatchedUniverse, resolveSource(sourceLocalPublicPost), publicBucket, hasPublicNonInputIdentity},
	}
	matchContents := func(scopes []localMatchScope, transparent bool) bool {
		for _, pass := range passes {
			if budget.exhausted() {
				return false
			}
			passBase := st.snapshotPairs()
			offers, complete := matchParentScopedUniqueBuckets(st, scopes, pass, passBase, budget, transparent)
			if !complete {
				return false
			}
			accepted, ok := st.chooseBatchIfBidirectionallyUnique(offers, passBase, budget)
			if !ok {
				return false
			}
			if len(accepted) == 0 {
				continue
			}
			st.applyBatch(accepted)
			changed = true
			for _, pr := range accepted {
				if st.idx.hasPublicChildren(pr.cur) {
					dirty.push(pr.cur)
				}
			}
		}
		return true
	}
	for {
		scope := dirty.drainInPublicOrder()
		if len(scope) == 0 || budget.exhausted() {
			return changed
		}

		baseR := st.snapshotPairs()
		shapeOffers, complete := matchParentScopedStructurallyUniqueContainers(
			st, scope, resolveSource(sourceLocalStructure), budget,
		)
		if !complete {
			return changed
		}
		shapeAccepted, ok := st.chooseBatchIfBidirectionallyUnique(shapeOffers, baseR, budget)
		if !ok {
			return changed
		}
		if len(shapeAccepted) > 0 {
			st.applyBatch(shapeAccepted)
			changed = true
			for _, pr := range shapeAccepted {
				if st.idx.hasPublicChildren(pr.cur) {
					dirty.push(pr.cur)
				}
			}
		}

		direct := make([]localMatchScope, 0, len(scope))
		for _, curParent := range scope {
			if anchor, anchored := st.matchedOld(curParent); anchored {
				direct = append(direct, localMatchScope{st.idx.prevChildren[anchor], st.idx.childrenOf(curParent)})
			}
		}
		if !matchContents(direct, false) {
			return changed
		}

		// Only content passes see the filtered frontier. Structural uniqueness
		// above deliberately includes every direct sibling, even used ones.
		baseR = st.snapshotPairs()
		var frontiers []localMatchScope
		for _, children := range direct {
			oldFrontier, oldTraversed := collectLocalFrontier(children.oldChildren, baseR.oldUsed, st.idx.prevFlagsOf, budget)
			curFrontier, curTraversed := collectLocalFrontier(children.curChildren, baseR.curUsed, st.idx.curFlagsOf, budget)
			if budget.exhausted() {
				return changed
			}
			if oldTraversed || curTraversed {
				frontiers = append(frontiers, localMatchScope{oldFrontier, curFrontier})
			}
		}
		if len(frontiers) > 0 && !matchContents(frontiers, true) {
			return changed
		}
	}
}

// matchOrdinalContainersZeroShift recovers a zero-shift ordinal group of
// non-input containers under one anchored parent (design §4.1.1, Stage 5). The
// bucket is the generalized container identity bucket, not just anonymous
// shapes. A bucket is accepted only when both sides hold the same number k >= 2
// and at least one member was already matched by a non-ordinal stage at the same
// rank, so a wholesale content replacement never inherits stale refs.
func matchOrdinalContainersZeroShift(st *matchState, budget *workBudget, dirty *parentQueue) bool {
	baseR := st.snapshotPairs()
	var proposals []pairProposal

	for _, parentCur := range st.idx.order {
		anchor, anchored := baseR.pairOfCur(parentCur)
		if !anchored {
			continue
		}

		pgroups := make(map[bucketKey][]int)
		cgroups := make(map[bucketKey][]int)
		var keyOrder []bucketKey
		seen := make(map[bucketKey]bool)

		for _, oldPos := range st.idx.prevChildren[anchor] {
			if !budget.charge(1) {
				return false
			}
			budget.visitCandidate()
			if !st.idx.prevContainer(oldPos) || st.idx.prevEditable(oldPos) {
				continue
			}
			k, pk := st.idx.prevKey[oldPos], st.idx.prevPub[oldPos]
			group := containerOrdinalBucket(k, pk)
			pgroups[group] = append(pgroups[group], oldPos)
			if !seen[group] {
				seen[group] = true
				keyOrder = append(keyOrder, group)
			}
		}
		for _, cur := range st.idx.childrenOf(parentCur) {
			if !budget.charge(1) {
				return false
			}
			budget.visitCandidate()
			if !st.idx.curContainer(cur) || st.idx.curEditable(cur) {
				continue
			}
			k, pk := st.idx.curMatchKey(cur), st.idx.curPubKey(cur)
			group := containerOrdinalBucket(k, pk)
			cgroups[group] = append(cgroups[group], cur)
			if !seen[group] {
				seen[group] = true
				keyOrder = append(keyOrder, group)
			}
		}

		for _, shape := range keyOrder {
			prevSibs := pgroups[shape]
			curSibs := cgroups[shape]
			k := len(prevSibs)
			if k <= 1 || k != len(curSibs) {
				continue
			}
			prevRank := make(map[int]int, k)
			curRank := make(map[int]int, k)
			for r, p := range prevSibs {
				prevRank[p] = r
			}
			for r, c := range curSibs {
				curRank[c] = r
			}

			fixed := false
			reject := false
			for r, p := range prevSibs {
				if !budget.charge(1) {
					return false
				}
				budget.visitCandidate()
				cur, matched := baseR.pairOfOld(p)
				if !matched {
					continue
				}
				cr, inGroup := curRank[cur]
				if !inGroup || cr != r {
					reject = true
					break
				}
				if baseR.sourceOfOld(p) != sourceOrdinal {
					fixed = true
				}
			}
			if !reject {
				for r, c := range curSibs {
					if !budget.charge(1) {
						return false
					}
					budget.visitCandidate()
					p, matched := baseR.pairOfCur(c)
					if !matched {
						continue
					}
					pr, inGroup := prevRank[p]
					if !inGroup || pr != r {
						reject = true
						break
					}
					if baseR.sourceOfOld(p) != sourceOrdinal {
						fixed = true
					}
				}
			}
			if reject || !fixed {
				continue
			}

			for r := 0; r < k; r++ {
				p, c := prevSibs[r], curSibs[r]
				if !baseR.oldUsed(p) && !baseR.curUsed(c) {
					proposals = append(proposals, pairProposal{
						cur:     c,
						prevPos: p,
						source:  sourceOrdinal,
					})
				}
			}
		}
	}

	accepted, ok := st.chooseBatchIfBidirectionallyUnique(proposals, baseR, budget)
	if !ok || len(accepted) == 0 {
		return false
	}
	st.applyBatch(accepted)
	for _, pr := range accepted {
		if st.idx.hasPublicChildren(pr.cur) {
			dirty.push(pr.cur)
		}
	}
	return true
}

// visitStaticFrontier walks in public preorder, bounded by the wrapper limit.
// An anchored node owns its descendants. Decorations inside a traversed
// wrapper are skipped, while direct decorative leaves remain candidates.
func visitStaticFrontier(
	children []int, depth int, childrenOf func(int) []int, isUsed func(int) bool,
	flagsOf func(int) matchNodeFlags, staticLeaf func(int) bool, visit func(int), budget *workBudget,
) bool {
	for _, child := range children {
		if !budget.charge(1) {
			return false
		}
		budget.visitCandidate()
		flags := flagsOf(child)
		if isUsed(child) || (depth > 0 && flags.isPureDecorativeSubtree) {
			continue
		}
		if staticLeaf(child) {
			visit(child)
		} else if depth < maxTransparentWrapperDepth && flags.isTransparentWrapperForStage6 {
			if !visitStaticFrontier(childrenOf(child), depth+1, childrenOf, isUsed, flagsOf, staticLeaf, visit, budget) {
				return false
			}
		}
	}
	return true
}

// matchStaticGeometrySiblings closes the last continuity gap for truly
// stationary screens (design §4.1.2). Under one already anchored parent, two
// identical non-input leaves that sit apart and did not move are otherwise
// permanently indistinguishable, so every frame would renumber them.
//
// The pass is deliberately narrow and runs at most once:
//   - only under an anchored parent pair, with bounded traversal through
//     anonymous wrappers whose non-decorative branches lead to static icons,
//   - eligible candidates are non-context, non-input leaves and interactive
//     icons with purely decorative descendants, in separate buckets,
//   - buckets must hold the same unmatched count k >= 2 on both sides,
//   - every old leaf needs exactly one current leaf within +-4px on all four
//     edges, every current leaf is hit exactly once (strict 1-to-1 bijection),
//   - the pairs must keep public preorder, so swapped siblings never trade refs.
//
// A bucket that fails any condition is skipped whole; the batch is committed
// through the same bidirectional uniqueness gate as every other stage.
func matchStaticGeometrySiblings(st *matchState, budget *workBudget) bool {
	baseR := st.snapshotPairs()
	var proposals []pairProposal

	for _, parentCur := range st.idx.order {
		if !budget.charge(1) {
			return false
		}
		anchor, anchored := baseR.pairOfCur(parentCur)
		if !anchored {
			continue
		}

		oldBuckets := make(map[bucketKey][]int)
		curBuckets := make(map[bucketKey][]int)
		var keyOrder []bucketKey
		seen := make(map[bucketKey]bool)

		idx := st.idx
		oldChildrenOf := func(pos int) []int { return idx.prevChildren[pos] }
		collectOld := func(oldPos int) {
			key := staticLeafBucket(idx.prevKey[oldPos], idx.prevPub[oldPos], idx.prevFlagsOf(oldPos).isStaticIconContainer)
			oldBuckets[key] = append(oldBuckets[key], oldPos)
			if !seen[key] {
				seen[key] = true
				keyOrder = append(keyOrder, key)
			}
		}
		if !visitStaticFrontier(oldChildrenOf(anchor), 0, oldChildrenOf, baseR.oldUsed, idx.prevFlagsOf, idx.prevStaticLeaf, collectOld, budget) {
			return false
		}
		collectCur := func(cur int) {
			key := staticLeafBucket(idx.curMatchKey(cur), idx.curPubKey(cur), idx.curFlagsOf(cur).isStaticIconContainer)
			curBuckets[key] = append(curBuckets[key], cur)
			if !seen[key] {
				seen[key] = true
				keyOrder = append(keyOrder, key)
			}
		}
		if !visitStaticFrontier(idx.childrenOf(parentCur), 0, idx.childrenOf, baseR.curUsed, idx.curFlagsOf, idx.curStaticLeaf, collectCur, budget) {
			return false
		}

		for _, key := range keyOrder {
			olds, curs := oldBuckets[key], curBuckets[key]
			if len(olds) < 2 || len(olds) != len(curs) {
				continue
			}
			pairs, complete := pairStaticLeafBounds(st.idx, olds, curs, budget)
			if !complete {
				return false
			}
			if pairs == nil {
				continue
			}
			for _, pr := range pairs {
				proposals = append(proposals, pairProposal{
					cur:     pr.cur,
					prevPos: pr.prevPos,
					source:  sourceStaticGeometry,
				})
			}
		}
	}

	accepted, ok := st.chooseBatchIfBidirectionallyUnique(proposals, baseR, budget)
	if !ok {
		return false
	}
	if len(accepted) == 0 {
		return true
	}
	st.applyBatch(accepted)
	return true
}

// staticLeafPair is one accepted static-geometry pairing.
type staticLeafPair struct {
	prevPos int
	cur     int
}

// staticGeometryTolerance is the maximum per-edge difference of two rectangles
// that still count as "did not move".
const staticGeometryTolerance = 4

// staticLeafBounds returns the published bounds of one leaf position. Callers
// only pass positions already proven to carry a positive rectangle.
func staticLeafBounds(b *protocol.Bounds) device.Rect {
	if b == nil {
		return device.Rect{}
	}
	return device.Rect{Left: b.Left, Top: b.Top, Right: b.Right, Bottom: b.Bottom}
}

// sameStaticBounds reports whether the four edges differ by at most the static
// geometry tolerance.
func sameStaticBounds(a, b device.Rect) bool {
	return absInt(a.Left-b.Left) <= staticGeometryTolerance &&
		absInt(a.Top-b.Top) <= staticGeometryTolerance &&
		absInt(a.Right-b.Right) <= staticGeometryTolerance &&
		absInt(a.Bottom-b.Bottom) <= staticGeometryTolerance
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// pairStaticLeafBounds pairs two equal-sized buckets by a strict +-4px
// bijection that also preserves public preorder. It returns nil (no error) when
// the bucket is ambiguous, has a single candidate on either side, or would swap
// siblings; complete is false only on work-budget exhaustion.
func pairStaticLeafBounds(
	idx *publicMatchIndex,
	olds, curs []int,
	budget *workBudget,
) ([]staticLeafPair, bool) {
	sortedCurs := make([]int, len(curs))
	copy(sortedCurs, curs)
	topOf := make(map[int]int, len(curs))
	for _, c := range curs {
		topOf[c] = staticLeafBounds(idx.curBounds[idx.curPos[c]]).Top
	}
	sort.Slice(sortedCurs, func(i, j int) bool {
		ti, tj := topOf[sortedCurs[i]], topOf[sortedCurs[j]]
		if ti != tj {
			return ti < tj
		}
		li := staticLeafBounds(idx.curBounds[idx.curPos[sortedCurs[i]]]).Left
		lj := staticLeafBounds(idx.curBounds[idx.curPos[sortedCurs[j]]]).Left
		if li != lj {
			return li < lj
		}
		return idx.tin[sortedCurs[i]] < idx.tin[sortedCurs[j]]
	})
	tops := make([]int, len(sortedCurs))
	for i, c := range sortedCurs {
		tops[i] = topOf[c]
	}

	sortedOlds := make([]int, len(olds))
	copy(sortedOlds, olds)
	sort.Ints(sortedOlds)

	hit := make(map[int]int, len(curs))
	pairs := make([]staticLeafPair, 0, len(sortedOlds))
	for _, oldPos := range sortedOlds {
		if !budget.charge(1) {
			return nil, false
		}
		oldRect := staticLeafBounds(idx.prevBounds[oldPos])
		start := sort.SearchInts(tops, oldRect.Top-staticGeometryTolerance)
		matched := -1
		for i := start; i < len(sortedCurs); i++ {
			if !budget.charge(1) {
				return nil, false
			}
			budget.visitCandidate()
			c := sortedCurs[i]
			if tops[i] > oldRect.Top+staticGeometryTolerance {
				break
			}
			if !sameStaticBounds(oldRect, staticLeafBounds(idx.curBounds[idx.curPos[c]])) {
				continue
			}
			if matched >= 0 {
				return nil, true // more than one candidate: never guess.
			}
			matched = c
		}
		if matched < 0 {
			return nil, true
		}
		hit[matched]++
		if hit[matched] > 1 {
			return nil, true
		}
		pairs = append(pairs, staticLeafPair{prevPos: oldPos, cur: matched})
	}
	if len(hit) != len(sortedCurs) {
		return nil, true
	}

	lastTin := -1
	for _, pr := range pairs {
		tin := idx.tin[pr.cur]
		if tin <= lastTin {
			return nil, true // siblings swapped: order must be preserved.
		}
		lastTin = tin
	}
	return pairs, true
}

// hasIdentity reports whether a raw key carries evidence strong enough for
// exact identity matching. A bare role and class with no label and no resource
// is a generic container that many units share; it is never paired on that alone.
func hasIdentity(key matchKey) bool {
	if key.protected {
		return true
	}
	if key.editable {
		return key.class != "" || key.hasRes || key.anchor != ""
	}
	return key.hasRes || key.label != ""
}

// eligibleForGlobalExact reports whether an identity key is permitted to match
// globally across different parents. Anchorless editable inputs are restricted
// to local same-parent matching only.
func eligibleForGlobalExact(key matchKey) bool {
	if !hasIdentity(key) {
		return false
	}
	if key.editable {
		return key.hasRes || key.anchor != ""
	}
	return true
}

// nearestPublishedParent walks the raw parent chain to the first published
// unit. The public tree may differ from the raw tree when a raw ancestor is
// not projected; matching must anchor on the published ancestor the reader
// actually sees.
func nearestPublishedParent(cap *device.Capture, units []unit, i int) int {
	if i == cap.Root {
		return -1
	}
	p := cap.Nodes[i].Parent
	for steps := 0; p >= 0 && p < len(cap.Nodes) && p != cap.Root && steps <= len(cap.Nodes); steps++ {
		if units[p].published {
			return p
		}
		next := cap.Nodes[p].Parent
		if next == p {
			break
		}
		p = next
	}
	return cap.Root
}

// hasPublicIdentity reports whether published fields carry identity evidence.
func hasPublicIdentity(key publicKey) bool {
	return key.protected || key.hasRes || key.label != ""
}

// samePublicUnit compares only published identity fields; raw class, package
// and unpublished resource ids are deliberately absent.
func samePublicUnit(a, b publicKey) bool {
	return a.role == b.role && a.label == b.label &&
		a.hasRes == b.hasRes && a.resource == b.resource &&
		a.anchor == b.anchor && a.actions == b.actions && a.editable == b.editable &&
		a.protected == b.protected
}

// sameUnit compares every identity evidence field of two published units.
func sameUnit(a, b matchKey) bool {
	return a.role == b.role && a.class == b.class && a.pkg == b.pkg &&
		a.hasRes == b.hasRes && a.resource == b.resource &&
		a.label == b.label && a.anchor == b.anchor &&
		a.actions == b.actions && a.editable == b.editable &&
		a.protected == b.protected
}

// sameInputAnchor compares non-value identity fields of two editable inputs.
func sameInputAnchor(a, b matchKey) bool {
	if a.role != protocol.RoleInput || b.role != protocol.RoleInput {
		return false
	}
	if a.class != b.class || a.pkg != b.pkg ||
		a.actions != b.actions || a.protected != b.protected {
		return false
	}
	if a.hasRes && b.hasRes {
		return a.resource == b.resource
	}
	if a.hasRes != b.hasRes {
		return false
	}
	return a.anchor != "" && a.anchor == b.anchor
}

// sameResourceOnly matches non-input units across label changes within an
// anchored parent when every non-label identity attribute agrees.
func sameResourceOnly(a, b matchKey) bool {
	if a.editable || b.editable || !a.hasRes || !b.hasRes {
		return false
	}
	return a.role == b.role && a.class == b.class && a.pkg == b.pkg &&
		a.resource == b.resource && a.actions == b.actions && a.protected == b.protected
}

// sameContainerIdentity reports whether two published containers may stand for
// the same unit in descendant voting (design §4.1.1). Inputs are excluded
// entirely: an editable unit is identified through its value/input-anchor
// channel, never through descendant voting.
//
// Anonymous containers compare by shape only. A container that carries public
// identity evidence must agree on every raw identity field, or on the complete
// published identity. Published resource equality already covers the
// "unlabelled actionable container" case, so two rows sharing a template
// resource and readable label can anchor through their unique descendants.
func sameContainerIdentity(prevK, curK matchKey, prevPK, curPK publicKey) bool {
	if prevK.editable || curK.editable {
		return false
	}
	if isStructurallyMatchable(prevK, prevPK) && isStructurallyMatchable(curK, curPK) {
		return sameShape(prevK, curK)
	}
	if sameUnit(prevK, curK) {
		return true
	}
	if !hasPublicIdentity(prevPK) || !hasPublicIdentity(curPK) {
		return false
	}
	if !samePublicUnit(prevPK, curPK) {
		return false
	}
	if prevK.actions != 0 && prevK.label == "" {
		// samePublicUnit already compares hasRes/resource; keep the guard
		// explicit so a future relaxation cannot silently drop it.
		return prevPK.hasRes == curPK.hasRes && prevPK.resource == curPK.resource
	}
	return true
}

// sameShape matches label-free containers: no label on either side, the same
// role, action set and protection. Raw class/package are ignored only when the
// container is non-actionable; an anonymous actionable container is a possible
// act target, so it still needs the stricter class/package agreement.
func sameShape(a, b matchKey) bool {
	if a.editable || b.editable {
		return false
	}
	if a.label != "" || b.label != "" || a.role != b.role ||
		a.actions != b.actions || a.protected != b.protected {
		return false
	}
	if a.actions != 0 {
		return a.class == b.class && a.pkg == b.pkg
	}
	return true
}

// preorder returns the capture nodes reachable from the traversal root in
// source reading order.
func preorder(cap *device.Capture) []int {
	order := make([]int, 0, len(cap.Nodes))
	seen := make([]bool, len(cap.Nodes))
	var walk func(i int)
	walk = func(i int) {
		order = append(order, i)
		for _, child := range cap.Nodes[i].Children {
			if child >= 0 && child < len(cap.Nodes) && !seen[child] && hasAncestorPath(cap, child, i) {
				seen[child] = true
				walk(child)
			}
		}
	}
	seen[cap.Root] = true
	walk(cap.Root)
	return order
}
