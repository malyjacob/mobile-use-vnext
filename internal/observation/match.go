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
//  1. same matched parent, identical identity evidence;
//  2. same matched parent, same class/role shape (label-free containers);
//  3. unique global identity match;
//  4. same matched parent, input anchor (survives value edits).

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

// prevUnit is one unit of the previous tree with its preorder position.
type prevUnit struct {
	ref       string
	position  int
	parentPos int // -1 for the previous root
	key       matchKey
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
				unit.key = signature(&b.prev.Capture.Nodes[i], n.Role)
			} else {
				// A context node has no source geometry. Its role and label
				// are all it has; the empty class keeps it from being paired
				// with a real unit, which always carries a source class.
				unit.key = matchKey{role: n.Role, label: labelSignature(n.Label)}
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

	curOrder := preorder(cap)
	curSig := make(map[int]matchKey, len(curOrder))
	for _, i := range curOrder {
		if i == cap.Root {
			continue
		}
		curSig[i] = signature(&cap.Nodes[i], units[i].role)
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

	// Pass 1: label-free containers (scroll areas, layout rows) under a matched
	// parent. A container carries no identity evidence of its own, so the
	// parent anchor decides and siblings must be unique on both sides. This is
	// not "first child in traversal order": two indistinguishable siblings are
	// never paired.
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" || curSig[i].editable || hasIdentity(curSig[i]) {
			continue
		}
		anchor, ok := prevParent[at(cap, i)]
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
		switch {
		case len(candidates) == 0:
			// New or reappeared container: fresh ref below.
		case countCurrentSiblings(cap, curSig, at(cap, i), curSig[i]) == 1:
			match(i, candidates[0].ref, candidates[0].position)
		default:
			// Several identical containers under this parent (list rows).
			// Pair by ordinal only when the parent keeps exactly the same
			// number of them: then nothing moved and the ordinal, not a
			// coordinate, is the remaining evidence.
			if ref, pos, ok := ordinalSibling(prev, usedPos, cap, curOrder, curSig, anchor, i, sameShape); ok {
				match(i, ref, pos)
			}
		}
	}

	// Pass 2: same matched parent, identical identity evidence.
	for _, i := range curOrder {
		if i == cap.Root || refs[i] != "" {
			continue
		}
		anchor, ok := prevParent[at(cap, i)]
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
		if count == 1 {
			match(i, candidate, prevPosition[candidate])
		}
		// count == 0: new or reappeared -> fresh ref below.
		// count > 1: several previous units carry identical evidence under this
		// parent (a list of buttons sharing one resource id and label). There is
		// no way to tell which one this is, so it gets a new ref. Pass 3 may
		// still recover it if it is unique in the whole tree.
	}

	// Pass 3: exactly one previous unit and exactly one current unit carry the
	// same identity evidence anywhere in the trees.
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
		anchor, ok := prevParent[at(cap, i)]
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
		if count == 1 {
			match(i, candidate, prevPosition[candidate])
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

// hasIdentity reports whether a key carries evidence strong enough for rules 1
// and 3. A bare role and class with no label and no resource is a generic
// container that many units share; it is never paired on that alone.
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

// ordinalSibling pairs i with the previous sibling of the same shape at the
// same ordinal under the same matched parent, but only when both sibling lists
// have exactly the same size and shapes; otherwise it reports false.
func ordinalSibling(prev []prevUnit, usedPos map[int]bool, cap *device.Capture, order []int, sigs map[int]matchKey, anchor, i int, same func(a, b matchKey) bool) (string, int, bool) {
	// Rank of i among current shape-siblings under its parent.
	rank := 0
	sameShapeCount := 0
	for _, child := range cap.Nodes[cap.Nodes[i].Parent].Children {
		if child < 0 || child >= len(cap.Nodes) || !same(sigs[child], sigs[i]) {
			continue
		}
		if !containsInt(order, child) {
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

// at returns the raw parent index of i used for anchor lookup: top-level nodes
// anchor on the synthetic root (raw root index), the raw root anchors on -1.
func at(cap *device.Capture, i int) int {
	if i == cap.Root {
		return -1
	}
	if i < 0 || i >= len(cap.Nodes) {
		return cap.Root
	}
	if p := cap.Nodes[i].Parent; p >= 0 && p < len(cap.Nodes) && p != i {
		return p
	}
	return cap.Root
}

func containsInt(values []int, want int) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// countCurrentSiblings counts current siblings of parentRaw whose shape equals
// sig, including self.
func countCurrentSiblings(cap *device.Capture, sigs map[int]matchKey, parentRaw int, sig matchKey) int {
	count := 0
	for _, child := range cap.Nodes[parentRaw].Children {
		if child >= 0 && child < len(cap.Nodes) && sameShape(sigs[child], sig) {
			count++
		}
	}
	return count
}

// sameUnit is rules 1 and 3: identical role, class, package, resource presence,
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

// sameShape matches label-free containers: same role, class, package and
// protection, no label on either side. A resource id is ignored here because
// every list row usually shares one.
func sameShape(a, b matchKey) bool {
	if a.editable || b.editable {
		return false
	}
	return a.label == "" && b.label == "" && a.role == b.role &&
		a.class == b.class && a.pkg == b.pkg && a.protected == b.protected
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
