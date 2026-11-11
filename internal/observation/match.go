package observation

import (
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
// Passes run parent-first so a matched parent is a usable anchor:
//  1. label-free containers under a matched parent (non-actionable wrappers
//     may ignore unpublished class/package; actionable containers require it);
//  2. same matched parent, identical raw identity evidence;
//  3. unique global raw identity match;
//  4. same matched parent, input anchor (survives value edits);
//  5. same matched parent, identical public identity (class/package changed);
//  6. unique global public identity match.
//
// The container pass is retried after a parent is matched by an identity rule.

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
// ticking and scrolling never break identity by themselves.
type matchKey struct {
	role      protocol.Role
	class     string
	pkg       string
	resource  string
	hasRes    bool
	label     string // label signature; unused when the unit has a resource id
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
	} else if role != protocol.RoleInput && !key.hasRes {
		// An input's own text is its value, never its identity label. A unit
		// with a source resource id is identified by that id: its readable
		// text may legitimately change (a counter, a changing caption) while
		// the unit stays the same. Without a resource id the label is the only
		// identity evidence there is.
		key.label = labelSignature(labelParts(raw))
	}
	if role == protocol.RoleInput && raw.DescSet && !(raw.HasPassword && raw.Password) {
		key.anchor = raw.Description
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
		editable:  role == protocol.RoleInput,
		protected: raw.HasPassword && raw.Password,
	}
	if role == protocol.RoleInput {
		if raw.DescSet && !key.protected {
			key.anchor = raw.Description
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

// prevUnit is one unit of the previous tree with its preorder position.
type prevUnit struct {
	ref       string
	position  int
	parentPos int // -1 for the previous root
	key       matchKey
	pub       publicKey
}

// matchRefs assigns refs to the published units of cap, continuing the
// builder's previous snapshot where unambiguous and allocating new refs from
// the monotonic counter otherwise.
func (b *Builder) matchRefs(cap *device.Capture, units []unit, refs []string) {
	var prev []prevUnit
	prevPosition := map[string]int{}
	if b.prev != nil {
		cursor := 0
		var walk func(n *protocol.Node, parentPos int)
		walk = func(n *protocol.Node, parentPos int) {
			unit := prevUnit{ref: n.Ref, position: cursor, parentPos: parentPos}
			if i, ok := b.prev.Source[n.Ref]; ok {
				raw := &b.prev.Capture.Nodes[i]
				unit.key = signature(raw, n.Role)
				unit.pub = publicSignature(raw, n.Role)
			} else if i, ok := b.prev.ContextSource[n.Ref]; ok {
				unit.key = contextSignature(&b.prev.Capture.Nodes[i])
			}
			prevPosition[n.Ref] = cursor
			prev = append(prev, unit)
			cursor++
			for _, child := range n.Children {
				walk(child, unit.position)
			}
		}
		walk(b.prev.Tree.Root, -1)
	}

	// Only published units take part in matching. An unpublished raw node has
	// no public role and must not consume a ref, a previous position, or a
	// global-uniqueness slot.
	curOrder := make([]int, 0, len(cap.Nodes))
	parentOf := make(map[int]int, len(cap.Nodes))
	childrenOf := make(map[int][]int, len(cap.Nodes))
	for _, i := range preorder(cap) {
		if i != cap.Root && !units[i].published {
			continue
		}
		curOrder = append(curOrder, i)
		if i == cap.Root {
			continue
		}
		p := nearestPublishedParent(cap, units, i)
		parentOf[i] = p
		childrenOf[p] = append(childrenOf[p], i)
	}

	curSig := make(map[int]matchKey, len(curOrder))
	curPub := make(map[int]publicKey, len(curOrder))
	for _, i := range curOrder {
		if i == cap.Root {
			continue
		}
		raw := &cap.Nodes[i]
		if units[i].context {
			curSig[i] = contextSignature(raw)
			continue
		}
		curSig[i] = signature(raw, units[i].role)
		curPub[i] = publicSignature(raw, units[i].role)
	}

	usedRef := map[string]bool{}
	usedPos := map[int]bool{}
	// prevParent maps a current raw index to the previous position of the
	// parent it was matched under, so children have a stable anchor.
	prevParent := map[int]int{}
	allocate := func() string {
		for {
			ref := formatRef(b.nextRef)
			b.nextRef++
			if !usedRef[ref] {
				usedRef[ref] = true
				return ref
			}
		}
	}
	// match records one accepted correspondence.
	match := func(raw int, ref string, pos int) {
		refs[raw] = ref
		usedRef[ref] = true
		usedPos[pos] = true
		prevParent[raw] = pos
	}

	// The synthetic root continues the previous synthetic root: it is the
	// reading unit of this display, not an application node. Top-level current
	// nodes anchor on it, so the previous root's own preorder position is the
	// anchor value.
	if b.prev != nil && b.prev.Tree != nil && b.prev.Tree.Root != nil {
		refs[cap.Root] = b.prev.Tree.Root.Ref
		usedRef[refs[cap.Root]] = true
		if pos, ok := prevPosition[refs[cap.Root]]; ok {
			usedPos[pos] = true
			prevParent[cap.Root] = pos
		}
	} else {
		refs[cap.Root] = allocate()
	}

	// Pass 1: label-free containers (scroll areas, layout rows, context
	// ancestry) under a matched parent. A container carries no published
	// identity of its own, so the parent anchor and sibling structure decide.
	// A non-actionable wrapper may ignore unpublished class/package; an
	// actionable anonymous container is a possible act target and still
	// requires class/package agreement in sameShape.
	//
	// The pass is callable more than once: a parent that is matched by an
	// identity rule becomes an anchor only after that rule ran, so its
	// anonymous children need another container pass.
	matchContainers := func() int {
		matchedNow := 0
		for _, i := range curOrder {
			if i == cap.Root || refs[i] != "" || curSig[i].editable || hasPublicIdentity(curPub[i]) {
				continue
			}
			anchor, ok := prevParent[parentOf[i]]
			if !ok {
				continue
			}
			var candidates []prevUnit
			for _, unit := range prev {
				if usedPos[unit.position] || unit.parentPos != anchor || !sameShape(unit.key, curSig[i]) {
					continue
				}
				candidates = append(candidates, unit)
			}
			if len(candidates) == 0 {
				continue
			}
			if countCurrentSiblings(childrenOf, curSig, parentOf[i], curSig[i]) == 1 {
				if len(candidates) == 1 {
					match(i, candidates[0].ref, candidates[0].position)
					matchedNow++
					continue
				}
				// The public shape is unique on the current side but several
				// previous containers share it. Class/package may still single
				// one out; otherwise the pair is ambiguous and gets a new ref.
				if ref, pos, ok := classCandidate(candidates, curSig[i]); ok {
					match(i, ref, pos)
					matchedNow++
				}
				continue
			}
			// Several identical containers under this parent (list rows). Pair by
			// ordinal only when the parent keeps exactly the same number of public
			// shapes: then nothing moved and the ordinal is the remaining evidence.
			if ref, pos, ok := ordinalSibling(prev, usedPos, childrenOf[parentOf[i]], curSig, anchor, i, sameShape); ok {
				match(i, ref, pos)
				matchedNow++
			}
		}
		return matchedNow
	}
	matchContainers()

	// Pass 2: same matched parent, identical raw identity evidence.
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" {
			continue
		}
		anchor, ok := prevParent[parentOf[i]]
		if !ok || !hasIdentity(curSig[i]) {
			continue
		}
		candidate := ""
		count := 0
		for _, unit := range prev {
			if usedPos[unit.position] || unit.parentPos != anchor ||
				!hasIdentity(unit.key) || !sameUnit(unit.key, curSig[i]) {
				continue
			}
			candidate = unit.ref
			count++
		}
		if count == 1 && countCurrentIdentitySiblings(childrenOf, curSig, parentOf[i], curSig[i]) == 1 {
			match(i, candidate, prevPosition[candidate])
		}
		// count == 0: new or reappeared -> fresh ref below.
		// count > 1: several previous units carry identical evidence under this
		// parent. There is no way to tell which one this is, so it gets a new
		// ref. Later passes may still recover it if the public evidence is
		// unique.
	}

	// Pass 3: exactly one previous unit and exactly one current unit carry the
	// same raw identity evidence anywhere in the trees.
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" || !hasIdentity(curSig[i]) {
			continue
		}
		candidate := -1
		count := 0
		for _, unit := range prev {
			if usedPos[unit.position] || !hasIdentity(unit.key) || !sameUnit(unit.key, curSig[i]) {
				continue
			}
			candidate = unit.position
			count++
		}
		if count == 1 && countCurrentIdentities(cap, curOrder, curSig, curSig[i]) == 1 {
			match(i, prev[candidate].ref, candidate)
		}
	}

	// Pass 4: inputs keep identity through value edits when package, class and
	// the resource/description anchor agree under the same matched parent.
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" || !curSig[i].editable {
			continue
		}
		anchor, ok := prevParent[parentOf[i]]
		if !ok {
			continue
		}
		candidate := ""
		count := 0
		for _, unit := range prev {
			if usedPos[unit.position] || unit.parentPos != anchor || !sameInputAnchor(unit.key, curSig[i]) {
				continue
			}
			candidate = unit.ref
			count++
		}
		if count == 1 && countCurrentInputSiblings(childrenOf, curSig, parentOf[i], curSig[i]) == 1 {
			match(i, candidate, prevPosition[candidate])
		}
	}

	// Pass 4b: retry containers now that identity/input parents have anchors.
	// A scroll area or row container may itself carry a resource id and be
	// matched by an identity rule; its anonymous children could not match
	// before that correspondence existed.
	for attempt := 0; attempt < 2; attempt++ {
		if matchContainers() == 0 {
			break
		}
	}

	// Pass 5: public identity under the same matched parent. This recovers a
	// unit whose raw class/package or unpublished resource id changed while
	// every published field stayed the same. Both sides must be unique.
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" || curPub[i].editable || !hasPublicIdentity(curPub[i]) {
			continue
		}
		anchor, ok := prevParent[parentOf[i]]
		if !ok {
			continue
		}
		candidate := ""
		count := 0
		for _, unit := range prev {
			if usedPos[unit.position] || unit.parentPos != anchor ||
				unit.pub.editable || !hasPublicIdentity(unit.pub) || !samePublicUnit(unit.pub, curPub[i]) {
				continue
			}
			candidate = unit.ref
			count++
		}
		if count == 1 && countCurrentPublic(childrenOf[parentOf[i]], curPub, curPub[i]) == 1 {
			match(i, candidate, prevPosition[candidate])
		}
	}

	// Pass 6: exactly one previous and one current unit carry the same public
	// identity anywhere in the trees (a class change that also moved parent).
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" || curPub[i].editable || !hasPublicIdentity(curPub[i]) {
			continue
		}
		candidate := -1
		count := 0
		for _, unit := range prev {
			if usedPos[unit.position] || unit.pub.editable ||
				!hasPublicIdentity(unit.pub) || !samePublicUnit(unit.pub, curPub[i]) {
				continue
			}
			candidate = unit.position
			count++
		}
		if count == 1 && countCurrentPublicAll(curOrder, curPub, curPub[i], cap.Root) == 1 {
			match(i, prev[candidate].ref, candidate)
		}
	}

	// Pass 6b: the public fallback can also turn a parent into an anchor.
	for attempt := 0; attempt < 2; attempt++ {
		if matchContainers() == 0 {
			break
		}
	}

	// Everything left is new (freshly appeared, reappeared, or ambiguous) and
	// gets a fresh ref in preorder. A ref that left the previous tree is never
	// resurrected: the counter only moves forward.
	for _, i := range curOrder {
		if refs[i] == "" {
			refs[i] = allocate()
		}
	}
}

// hasIdentity reports whether a raw key carries evidence strong enough for
// rules 2 and 3. A bare role and class with no label and no resource is a
// generic container that many units share; it is never paired on that alone.
func hasIdentity(key matchKey) bool {
	if key.protected {
		return true
	}
	if key.editable {
		// An input is identified by class (and resource id or description).
		// Its text is a value, so it never contributes to identity; the input
		// rule in pass 4 still requires package+class plus an anchor.
		return key.class != "" || key.hasRes || key.anchor != ""
	}
	return key.hasRes || key.label != ""
}

// countCurrentIdentities counts current units whose identity evidence equals
// sig, including self, so a global match is accepted only when the current
// side is unambiguous too.
func countCurrentIdentities(cap *device.Capture, order []int, sigs map[int]matchKey, sig matchKey) int {
	count := 0
	for _, j := range order {
		if j != cap.Root && hasIdentity(sigs[j]) && sameUnit(sigs[j], sig) {
			count++
		}
	}
	return count
}

// countCurrentIdentitySiblings counts current published direct children of
// parent whose raw identity equals sig, including self. A local exact match is
// accepted only when the current side is unambiguous too.
func countCurrentIdentitySiblings(childrenOf map[int][]int, sigs map[int]matchKey, parent int, sig matchKey) int {
	count := 0
	for _, child := range childrenOf[parent] {
		if hasIdentity(sigs[child]) && sameUnit(sigs[child], sig) {
			count++
		}
	}
	return count
}

// countCurrentInputSiblings counts current published direct children of parent
// whose input anchor equals sig, including self.
func countCurrentInputSiblings(childrenOf map[int][]int, sigs map[int]matchKey, parent int, sig matchKey) int {
	count := 0
	for _, child := range childrenOf[parent] {
		if sigs[child].editable && sameInputAnchor(sigs[child], sig) {
			count++
		}
	}
	return count
}

// ordinalSibling pairs i with the previous sibling of the same shape at the
// same ordinal under the same matched parent, but only when both published
// sibling lists have exactly the same size and shapes; otherwise it reports
// false.
func ordinalSibling(prev []prevUnit, usedPos map[int]bool, siblings []int, sigs map[int]matchKey, anchor, i int, same func(a, b matchKey) bool) (string, int, bool) {
	// Rank of i among current published shape-siblings under its parent.
	rank := 0
	sameShapeCount := 0
	for _, child := range siblings {
		if !same(sigs[child], sigs[i]) {
			continue
		}
		if child == i {
			rank = sameShapeCount
		}
		sameShapeCount++
	}
	// Previous shape-siblings under the anchor.
	var candidates []prevUnit
	for _, unit := range prev {
		if unit.parentPos == anchor && same(unit.key, sigs[i]) {
			candidates = append(candidates, unit)
		}
	}
	if len(candidates) != sameShapeCount || len(candidates) == 0 || rank >= len(candidates) {
		return "", 0, false
	}
	unit := candidates[rank]
	if usedPos[unit.position] {
		return "", 0, false
	}
	return unit.ref, unit.position, true
}

// classCandidate picks the only previous container whose raw class/package
// agree when the public shape alone matched several candidates.
func classCandidate(candidates []prevUnit, sig matchKey) (string, int, bool) {
	var found prevUnit
	count := 0
	for _, unit := range candidates {
		if unit.key.class == sig.class && unit.key.pkg == sig.pkg {
			found = unit
			count++
		}
	}
	if count == 1 {
		return found.ref, found.position, true
	}
	return "", 0, false
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

// countCurrentSiblings counts current published direct children of parent
// whose public shape equals sig, including self.
func countCurrentSiblings(childrenOf map[int][]int, sigs map[int]matchKey, parent int, sig matchKey) int {
	count := 0
	for _, child := range childrenOf[parent] {
		if sameShape(sigs[child], sig) {
			count++
		}
	}
	return count
}

// countCurrentPublic counts current published direct children of parent whose
// public identity equals sig, including self.
func countCurrentPublic(siblings []int, sigs map[int]publicKey, sig publicKey) int {
	count := 0
	for _, child := range siblings {
		if hasPublicIdentity(sigs[child]) && samePublicUnit(sigs[child], sig) {
			count++
		}
	}
	return count
}

// countCurrentPublicAll counts public identities anywhere in the published
// current tree.
func countCurrentPublicAll(order []int, sigs map[int]publicKey, sig publicKey, root int) int {
	count := 0
	for _, j := range order {
		if j == root || sigs[j].editable {
			continue
		}
		if hasPublicIdentity(sigs[j]) && samePublicUnit(sigs[j], sig) {
			count++
		}
	}
	return count
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
		a.anchor == b.anchor && a.editable == b.editable &&
		a.protected == b.protected
}

// sameUnit is rules 2 and 3: identical role, class, package, resource presence,
// label signature and protection.
func sameUnit(a, b matchKey) bool {
	return a.role == b.role && a.class == b.class && a.pkg == b.pkg &&
		a.hasRes == b.hasRes && a.resource == b.resource &&
		a.label == b.label && a.protected == b.protected
}

// sameInputAnchor is rule 4.
func sameInputAnchor(a, b matchKey) bool {
	if a.role != protocol.RoleInput || b.role != protocol.RoleInput {
		return false
	}
	if a.class != b.class || a.pkg != b.pkg || a.protected != b.protected {
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
