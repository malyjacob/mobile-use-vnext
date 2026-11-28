package engine

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type tri struct {
	known bool
	value bool
}

type triCompare uint8

const (
	triUnknown triCompare = iota
	triEqual
	triMismatch
)

func compareTri(a, b tri) triCompare {
	switch {
	case a.known && b.known && a.value == b.value:
		return triEqual
	case a.known && b.known:
		return triMismatch
	case a.known != b.known:
		return triMismatch
	default:
		return triUnknown
	}
}

func knownTrue(value tri) bool {
	return value.known && value.value
}

func knownFalse(value tri) bool {
	return value.known && !value.value
}

type optionalString struct {
	set   bool
	value string
}

func optionalStringOf(raw *device.RawNode) (text, description, resource optionalString) {
	text = optionalString{set: raw.TextSet, value: raw.Text}
	description = optionalString{set: raw.DescSet, value: raw.Description}
	resource = optionalString{set: raw.ResourceSet, value: raw.ResourceID}
	return text, description, resource
}

func compareOptional(a, b optionalString) triCompare {
	switch {
	case a.set && b.set && a.value == b.value:
		return triEqual
	case a.set && b.set:
		return triMismatch
	case a.set != b.set:
		return triMismatch
	default:
		return triUnknown
	}
}

// structureKey is the ordered, bounds-free source content key used by action
// target matching. It is deliberately separate from the strict settle identity.
type structureKey struct {
	Package, Class string
	Text           optionalString
	Description    optionalString
	Resource       optionalString

	Clickable     tri
	LongClickable tri
	Scrollable    tri
	Enabled       tri
	Checked       tri
	Password      tri
	Checkable     tri
	Editable      bool
}

func structureKeyOf(raw *device.RawNode) structureKey {
	text, description, resource := optionalStringOf(raw)
	return structureKey{
		Package: raw.Package, Class: raw.ClassName,
		Text: text, Description: description, Resource: resource,
		Clickable:     tri{known: raw.HasClickable, value: raw.Clickable},
		LongClickable: tri{known: raw.HasLongClick, value: raw.LongClickable},
		Scrollable:    tri{known: raw.HasScrollable, value: raw.Scrollable},
		Enabled:       tri{known: raw.HasEnabled, value: raw.Enabled},
		Checked:       tri{known: raw.HasChecked, value: raw.Checked},
		Password:      tri{known: raw.HasPassword, value: raw.Password},
		Checkable:     tri{known: raw.HasCheckable, value: raw.Checkable},
		Editable:      raw.Editable,
	}
}

type ancestorStructureKey struct {
	Package, Class string
	Resource       optionalString
	Editable       bool
	Clickable      tri
	LongClickable  tri
	Scrollable     tri
	Enabled        tri
	Password       tri
	Checkable      tri
}

func ancestorStructureKeyOf(raw *device.RawNode) ancestorStructureKey {
	key := structureKeyOf(raw)
	return ancestorStructureKey{
		Package: key.Package, Class: key.Class, Resource: key.Resource,
		Editable: key.Editable, Clickable: key.Clickable,
		LongClickable: key.LongClickable, Scrollable: key.Scrollable,
		Enabled: key.Enabled, Password: key.Password, Checkable: key.Checkable,
	}
}

// actMatchMode selects the operation-specific strong identity evidence policy.
type actMatchMode uint8

const (
	// actMatchTapLike accepts exact readable multiset or numeric-skeleton E3.
	actMatchTapLike actMatchMode = iota
	// actMatchScroll is the scroll-container policy: a scrolled container's
	// readable subtree legitimately changes, so E3 accepts a non-empty readable
	// intersection or the same non-empty resource with strong coverage.
	actMatchScroll
	// actMatchTapLikeExact preserves exact E3 for the write-submit soft-keyboard
	// recovery path.
	actMatchTapLikeExact
)

// actMatchOptions is the complete policy of one action-target match. The zero
// value enables the ordinary tap-like policy through Tier 5. An absent limit
// field means the default limit.
type actMatchOptions struct {
	mode   actMatchMode
	limits actMatchLimits
	// allowEnabledTransition accepts a fresh explicit Enabled false -> true, or
	// a fresh missing enabled flag, on the single binding this option is applied
	// to. It is reserved for the write-submit execution target, never accepts
	// true -> false, and never authorizes dispatch by itself: the caller must
	// still prove the explicit enabled state on the fresh frame.
	allowEnabledTransition bool
	// disableTranslation disables the Tier 5 same-lineage horizontal-band
	// translation recovery. The zero value keeps it enabled for ordinary
	// actions; the coordinate owner-conservation fallback disables it so a
	// moved owner can never be accepted at a far position.
	disableTranslation bool
}

func defaultActMatchOptions() actMatchOptions {
	return actMatchOptions{mode: actMatchTapLike}
}

// actMatchOptionsForOperation selects the Tier 4 evidence policy of one
// operation type. Every other operation keeps the tap-like policy.
func actMatchOptionsForOperation(operationType string) actMatchOptions {
	options := defaultActMatchOptions()
	if operationType == "scroll" {
		options.mode = actMatchScroll
	}
	return options
}

// withLimits replaces the bounded matcher limits without changing the policy.
func (options actMatchOptions) withLimits(limits actMatchLimits) actMatchOptions {
	options.limits = limits
	return options
}

// withEnabledTransition enables the write-submit enabled transition on this
// option set. Callers apply it to the execution binding only.
func (options actMatchOptions) withEnabledTransition() actMatchOptions {
	options.allowEnabledTransition = true
	return options
}

// withoutTranslation disables the Tier 5 translation recovery on this option
// set. The coordinate owner-conservation fallback uses it so an owner that only
// stayed on the same horizontal band can never be accepted after a real move.
func (options actMatchOptions) withoutTranslation() actMatchOptions {
	options.disableTranslation = true
	return options
}

// withExactTapLikeEvidence keeps the exact readable multiset as the only
// tap-like subtree evidence. The write-submit revalidation path uses it so its
// evidence rule stays exactly what it was before the numeric-skeleton policy was
// introduced.
func (options actMatchOptions) withExactTapLikeEvidence() actMatchOptions {
	options.mode = actMatchTapLikeExact
	return options
}

// sameStructureForSubmit is sameStructure with the write-submit enabled
// transition. It is applied to the execution binding only.
func sameStructureForSubmit(a, b *device.RawNode) (compatible, evidence bool) {
	return sameStructureForAct(a, b,
		defaultActMatchOptions().withEnabledTransition())
}

// sameStructure reports whether the two nodes have no known conflict and
// whether their known fields supply positive identity evidence. Unknown and
// unknown produce neither a conflict nor positive evidence.
func sameStructure(a, b *device.RawNode) (compatible, evidence bool) {
	return sameStructureForAct(a, b, defaultActMatchOptions())
}

// sameStructureForAct applies the match policy to the source structure
// comparison. allowEnabledTransition is the only relaxation: the write plan may
// observe its execution target change from disabled to enabled, or lose the
// enabled flag while disabled. Nothing else is relaxed.
func sameStructureForAct(a, b *device.RawNode,
	options actMatchOptions) (compatible, evidence bool) {
	if a == nil || b == nil {
		return false, false
	}
	left, right := structureKeyOf(a), structureKeyOf(b)
	compatible = true
	if left.Package != right.Package || left.Class != right.Class ||
		left.Editable != right.Editable {
		return false, false
	}
	evidence = left.Package != "" || left.Class != ""

	consumeOptional := func(x, y optionalString, nonEmptyAnchor bool) bool {
		switch compareOptional(x, y) {
		case triMismatch:
			compatible = false
		case triEqual:
			if nonEmptyAnchor && x.value != "" {
				evidence = true
			}
		}
		return compatible
	}
	consumeTri := func(x, y tri) bool {
		switch compareTri(x, y) {
		case triMismatch:
			compatible = false
		case triEqual:
			evidence = true
		}
		return compatible
	}

	if !consumeOptional(left.Text, right.Text, true) ||
		!consumeOptional(left.Description, right.Description, true) ||
		!consumeOptional(left.Resource, right.Resource, true) {
		return false, false
	}
	consumeEnabled := func(x, y tri) bool {
		switch compareEnabledForAct(x, y, options.allowEnabledTransition) {
		case triMismatch:
			compatible = false
		case triEqual:
			evidence = true
		}
		return compatible
	}

	for _, pair := range [][2]tri{
		{left.Clickable, right.Clickable},
		{left.LongClickable, right.LongClickable},
		{left.Scrollable, right.Scrollable},
		{left.Checked, right.Checked},
		{left.Password, right.Password},
		{left.Checkable, right.Checkable},
	} {
		if !consumeTri(pair[0], pair[1]) {
			return false, false
		}
	}
	if !consumeEnabled(left.Enabled, right.Enabled) {
		return false, false
	}
	return true, evidence
}

// compareEnabledForAct is the enabled tri-state policy of one comparison. A
// disabled -> enabled flip and a fresh missing flag are accepted only for the
// write-submit execution binding; the reverse flip stays a mismatch.
func compareEnabledForAct(original, fresh tri, allowTransition bool) triCompare {
	if !allowTransition {
		return compareTri(original, fresh)
	}
	switch {
	case original.known && fresh.known && original.value == fresh.value:
		return triEqual
	case original.known && fresh.known && !original.value && fresh.value:
		return triUnknown
	case original.known && !fresh.known:
		return triUnknown
	default:
		return compareTri(original, fresh)
	}
}

type lineageEntry struct {
	parent int
	key    structureKey
	root   bool
}

func isSyntheticDisplayRoot(raw *device.RawNode) bool {
	return raw != nil && raw.Parent == -1 && raw.Package == "" && raw.ClassName == "" &&
		!raw.TextSet && !raw.DescSet && !raw.ResourceSet && !raw.Editable &&
		raw.HasEnabled && raw.Enabled && !raw.HasClickable && !raw.HasLongClick &&
		!raw.HasScrollable && !raw.HasChecked && !raw.HasSelected && !raw.HasFocused &&
		!raw.HasCheckable && !raw.HasPassword
}

// lineageL1 is the nearest-first ancestor chain without Bounds, Focused or
// Selected. The depth bound and visited set turn malformed cycles into a
// hierarchy failure instead of an unbounded walk.
func lineageL1(capture *device.Capture, index int,
	budget *matchBudget) ([]lineageEntry, bool) {
	if capture == nil || index < 0 || index >= len(capture.Nodes) {
		budget.failMalformed()
		return nil, false
	}
	var seen map[int]struct{}
	var lineage []lineageEntry
	parent := capture.Nodes[index].Parent
	for depth := 0; parent != -1; depth++ {
		if parent < -1 || parent >= len(capture.Nodes) ||
			len(lineage) >= len(capture.Nodes) {
			budget.failMalformed()
			return nil, false
		}
		if !budget.consumeLineage(depth) {
			return nil, false
		}
		if seen == nil {
			seen = make(map[int]struct{})
		}
		if _, duplicate := seen[parent]; duplicate {
			budget.failMalformed()
			return nil, false
		}
		seen[parent] = struct{}{}
		node := &capture.Nodes[parent]
		if node.Parent < -1 {
			budget.failMalformed()
			return nil, false
		}
		if isSyntheticDisplayRoot(node) {
			if len(lineage) > 0 {
				lineage[len(lineage)-1].root = true
			}
			break
		}
		lineage = append(lineage, lineageEntry{
			parent: parent,
			key:    structureKeyOf(node),
			root:   node.Parent == -1,
		})
		parent = node.Parent
	}
	return lineage, true
}

// transparentWrapper accepts only a node whose content is known-empty and
// whose interaction and state capabilities are all known safe. Missing
// presence is never converted into a default.
func transparentWrapper(raw *device.RawNode) bool {
	if raw == nil || raw.Parent < 0 {
		return false
	}
	if !raw.TextSet || raw.Text != "" ||
		!raw.DescSet || raw.Description != "" ||
		!raw.ResourceSet || raw.ResourceID != "" {
		return false
	}
	if raw.Editable {
		return false
	}
	if !knownFalse(tri{known: raw.HasClickable, value: raw.Clickable}) ||
		!knownFalse(tri{known: raw.HasLongClick, value: raw.LongClickable}) ||
		!knownFalse(tri{known: raw.HasScrollable, value: raw.Scrollable}) ||
		!knownFalse(tri{known: raw.HasPassword, value: raw.Password}) ||
		!knownFalse(tri{known: raw.HasCheckable, value: raw.Checkable}) {
		return false
	}
	return knownTrue(tri{known: raw.HasEnabled, value: raw.Enabled}) &&
		knownFalse(tri{known: raw.HasFocused, value: raw.Focused}) &&
		knownFalse(tri{known: raw.HasSelected, value: raw.Selected}) &&
		knownFalse(tri{known: raw.HasChecked, value: raw.Checked})
}

// lineageL2 removes only provably transparent wrappers, preserving the root
// and every textual, interactive, editable, protected or state boundary.
func lineageL2(capture *device.Capture, index int,
	budget *matchBudget) ([]lineageEntry, bool) {
	all, ok := lineageL1(capture, index, budget)
	if !ok {
		return nil, false
	}
	out := make([]lineageEntry, 0, len(all))
	for _, entry := range all {
		if budget == nil || !budget.checkActive() {
			return nil, false
		}
		if entry.root {
			out = append(out, entry)
			continue
		}
		if entry.parent < 0 || entry.parent >= len(capture.Nodes) {
			budget.failMalformed()
			return nil, false
		}
		if transparentWrapper(&capture.Nodes[entry.parent]) {
			continue
		}
		out = append(out, entry)
	}
	return out, true
}

type lineageEntryForAct struct {
	parent int
	key    ancestorStructureKey
	root   bool
}

// lineageL2ForAct preserves the same boundaries as lineageL2 while comparing
// only stable ancestor structure.
func lineageL2ForAct(capture *device.Capture, index int,
	budget *matchBudget) ([]lineageEntryForAct, bool) {
	all, ok := lineageL1(capture, index, budget)
	if !ok {
		return nil, false
	}
	out := make([]lineageEntryForAct, 0, len(all))
	for _, entry := range all {
		if budget == nil || !budget.checkActive() {
			return nil, false
		}
		if entry.parent < 0 || entry.parent >= len(capture.Nodes) {
			budget.failMalformed()
			return nil, false
		}
		node := &capture.Nodes[entry.parent]
		if !entry.root && transparentWrapper(node) {
			continue
		}
		out = append(out, lineageEntryForAct{
			parent: entry.parent, key: ancestorStructureKeyOf(node), root: entry.root,
		})
	}
	return out, true
}

func strongLineageAnchorForAct(key ancestorStructureKey) bool {
	return (key.Resource.set && key.Resource.value != "") ||
		knownTrue(key.Clickable) || knownTrue(key.LongClickable) ||
		knownTrue(key.Scrollable) || key.Editable ||
		knownTrue(key.Password) || knownTrue(key.Checkable)
}

func strongLineageAnchor(entry lineageEntry) bool {
	key := entry.key
	return (key.Resource.set && key.Resource.value != "") ||
		(key.Text.set && key.Text.value != "") ||
		(key.Description.set && key.Description.value != "") ||
		knownTrue(key.Clickable) ||
		knownTrue(key.LongClickable) ||
		knownTrue(key.Scrollable) ||
		key.Editable ||
		knownTrue(key.Password) ||
		knownTrue(key.Checkable)
}

func rectValidOnScreen(rect, screen device.Rect) bool {
	return !rect.Empty() && rect.Left >= screen.Left && rect.Top >= screen.Top &&
		rect.Right <= screen.Right && rect.Bottom <= screen.Bottom
}

func sizeCompatible(a, b device.Rect) bool {
	aw, ah := a.Width(), a.Height()
	bw, bh := b.Width(), b.Height()
	if aw <= 0 || ah <= 0 || bw <= 0 || bh <= 0 {
		return false
	}
	return absInt(aw-bw) <= max(8, max(aw, bw)/2) &&
		absInt(ah-bh) <= max(8, max(ah, bh)/2)
}

func geometryCompatible(a, b, screen device.Rect, minOverlap float64) bool {
	if !rectValidOnScreen(a, screen) || !rectValidOnScreen(b, screen) ||
		!sizeCompatible(a, b) || overlapRatio(a, b) < minOverlap {
		return false
	}
	ax, ay := a.Center()
	bx, by := b.Center()
	return absInt(ax-bx) <= max(8, max(a.Width(), b.Width())/2) &&
		absInt(ay-by) <= max(8, max(a.Height(), b.Height())/2)
}

const (
	actTier2MinOverlap = 0.50
	actTier3MinOverlap = 0.60

	maxActLineageDepth = 4096
	maxActLineageSteps = 1 << 20
)

type actMatch struct {
	raw                *device.RawNode
	tier               int
	independentContent bool
	// independentSubtree reports that the matched target's own subtree carried
	// the strong identity evidence of the match (the E3 readable multiset or the
	// numeric-skeleton conservation), not only its own text or resource.
	independentSubtree bool
	lineageMismatch    bool
	stats              actMatchStats
}

type actMatchStats struct {
	candidateVisits int
	lineageSteps    int
	tiers           int
}

type matchGate uint8

const (
	matchProceed matchGate = iota
	matchCancelled
	matchBudgetExhausted
	matchMalformed
)

type actProbeState uint8

const (
	actProbeNone actProbeState = iota
	actProbeUnique
	actProbeAmbiguous
	actProbeCancelled
	actProbeBudgetExhausted
	actProbeMalformed
)

type actProbeResult struct {
	candidateIndex     int
	independentContent bool
	independentSubtree bool
	lineageMismatch    bool
	state              actProbeState
}

type actMatchLimits struct {
	candidateLimit int
	lineageLimit   int
	lineageDepth   int
}

func defaultActMatchLimits() actMatchLimits {
	return actMatchLimits{
		lineageLimit: maxActLineageSteps,
		lineageDepth: maxActLineageDepth,
	}
}

type matchBudget struct {
	ctx context.Context

	candidateVisits int
	candidateLimit  int
	lineageSteps    int
	lineageLimit    int
	lineageDepth    int
	// evidenceVisits bounds the Tier 4 semantic-evidence traversal. It has its
	// own ceiling so a Tier 4 candidate scan can never exhaust the candidate
	// budget that Tier 1-3 already charged.
	evidenceVisits int
	evidenceLimit  int
	stop           matchGate
}

func (budget *matchBudget) fail(reason matchGate) bool {
	if budget != nil && budget.stop == matchProceed {
		budget.stop = reason
	}
	return false
}

func (budget *matchBudget) failMalformed() bool {
	return budget.fail(matchMalformed)
}

func (budget *matchBudget) checkActive() bool {
	if budget == nil || budget.ctx == nil || budget.ctx.Err() != nil {
		return budget.fail(matchCancelled)
	}
	return budget.stop == matchProceed
}

func (budget *matchBudget) consumeCandidate() bool {
	if !budget.checkActive() {
		return false
	}
	if budget.candidateVisits >= budget.candidateLimit {
		return budget.fail(matchBudgetExhausted)
	}
	budget.candidateVisits++
	return true
}

func (budget *matchBudget) consumeLineage(depth int) bool {
	if !budget.checkActive() {
		return false
	}
	if depth >= budget.lineageDepth || budget.lineageSteps >= budget.lineageLimit {
		return budget.fail(matchBudgetExhausted)
	}
	budget.lineageSteps++
	return true
}

// consumeEvidence charges one bounded semantic-evidence visit.
func (budget *matchBudget) consumeEvidence() bool {
	if !budget.checkActive() {
		return false
	}
	if budget.evidenceLimit > 0 && budget.evidenceVisits >= budget.evidenceLimit {
		return budget.fail(matchBudgetExhausted)
	}
	budget.evidenceVisits++
	return true
}

func (budget *matchBudget) terminalState() actProbeState {
	if budget == nil {
		return actProbeMalformed
	}
	switch budget.stop {
	case matchCancelled:
		return actProbeCancelled
	case matchBudgetExhausted:
		return actProbeBudgetExhausted
	case matchMalformed:
		return actProbeMalformed
	default:
		return actProbeNone
	}
}

type lineageAtTier struct {
	entries []lineageEntry
	ok      bool
}

type lineageAtTierForAct struct {
	entries []lineageEntryForAct
	ok      bool
}

type resourceAnchor struct {
	displayID     int
	pkg, resource string
}

// resourceCounts caches whole-tree resource uniqueness for E2.
type resourceCounts struct {
	original map[resourceAnchor]int
	fresh    map[resourceAnchor]int
}

type tierMatcher struct {
	ctx            context.Context
	original       *device.Capture
	fresh          *device.Capture
	tier           int
	screen         device.Rect
	budget         matchBudget
	oldLines       map[int]lineageAtTier
	newLines       map[int]lineageAtTier
	oldLinesForAct map[int]lineageAtTierForAct
	newLinesForAct map[int]lineageAtTierForAct
	opts           actMatchOptions
	// geometryTier4 selects the Tier 4 geometry gate: tight bidirectional
	// coverage or the soft-keyboard horizontal band.
	geometryTier4 tier4Geometry
	// counts caches the E2 resource census of both trees; countsReady marks a
	// completed census, counts a nil result.
	counts      *resourceCounts
	countsReady bool
}

func newTierMatcher(ctx context.Context, original, fresh *device.Capture,
	tier int, options actMatchOptions) *tierMatcher {
	limits := options.limits
	candidateLimit := len(fresh.Nodes) + len(original.Nodes)
	if limits.candidateLimit > 0 && limits.candidateLimit < candidateLimit {
		candidateLimit = limits.candidateLimit
	}
	lineageLimit := limits.lineageLimit
	if lineageLimit <= 0 {
		lineageLimit = maxActLineageSteps
	}
	lineageDepth := limits.lineageDepth
	if lineageDepth <= 0 {
		lineageDepth = maxActLineageDepth
	}
	matcher := &tierMatcher{
		ctx: ctx, original: original, fresh: fresh, tier: tier,
		screen: fresh.Display.Bounds(),
		budget: matchBudget{
			ctx: ctx, candidateLimit: candidateLimit,
			lineageLimit: lineageLimit, lineageDepth: lineageDepth,
			evidenceLimit: maxTier4EvidenceSteps,
		},
		opts: options,
	}
	if tier == 1 || tier == 2 || tier == 5 {
		matcher.oldLines = make(map[int]lineageAtTier)
		matcher.newLines = make(map[int]lineageAtTier)
	}
	if tier == 3 || tier == 5 {
		matcher.oldLinesForAct = make(map[int]lineageAtTierForAct)
		matcher.newLinesForAct = make(map[int]lineageAtTierForAct)
	}
	return matcher
}

func (matcher *tierMatcher) strictLineage(capture *device.Capture,
	memo map[int]lineageAtTier, index int) (lineageAtTier, bool) {
	if cached, ok := memo[index]; ok {
		return cached, cached.ok
	}
	entries, ok := lineageL1(capture, index, &matcher.budget)
	cached := lineageAtTier{entries: entries, ok: ok}
	memo[index] = cached
	return cached, ok
}

func (matcher *tierMatcher) lineage(capture *device.Capture,
	memo map[int]lineageAtTierForAct, index int) (lineageAtTierForAct, bool) {
	if cached, ok := memo[index]; ok {
		return cached, cached.ok
	}
	entries, ok := lineageL2ForAct(capture, index, &matcher.budget)
	cached := lineageAtTierForAct{entries: entries, ok: ok}
	memo[index] = cached
	return cached, ok
}

func (matcher *tierMatcher) sameLineageKeysBudgeted(
	a, b []lineageEntry) (equal, complete bool) {
	if len(a) != len(b) {
		return false, true
	}
	for index := range a {
		if !matcher.budget.consumeLineage(0) {
			return false, false
		}
		if a[index].key != b[index].key {
			return false, true
		}
	}
	return true, true
}

func (matcher *tierMatcher) sameStrictLineage(oldIndex, newIndex int) (bool, bool) {
	oldLineage, ok := matcher.strictLineage(matcher.original, matcher.oldLines, oldIndex)
	if !ok {
		return false, false
	}
	newLineage, ok := matcher.strictLineage(matcher.fresh, matcher.newLines, newIndex)
	if !ok {
		return false, false
	}
	return matcher.sameLineageKeysBudgeted(newLineage.entries, oldLineage.entries)
}

func (matcher *tierMatcher) sameLineageKeysForActBudgeted(
	a, b []lineageEntryForAct) (equal, complete bool) {
	if len(a) != len(b) {
		return false, true
	}
	for index := range a {
		if !matcher.budget.consumeLineage(0) {
			return false, false
		}
		if a[index].key != b[index].key {
			return false, true
		}
	}
	return true, true
}

func (matcher *tierMatcher) hasStrongNonRootAnchorBudgeted(
	chain []lineageEntryForAct) (found, complete bool) {
	for _, entry := range chain {
		if !matcher.budget.consumeLineage(0) {
			return false, false
		}
		if !entry.root && strongLineageAnchorForAct(entry.key) {
			return true, true
		}
	}
	return false, true
}

func (matcher *tierMatcher) sameAnchoredLineageForAct(oldIndex, newIndex int) (bool, bool) {
	oldLineage, ok := matcher.lineage(matcher.original, matcher.oldLinesForAct, oldIndex)
	if !ok {
		return false, false
	}
	newLineage, ok := matcher.lineage(matcher.fresh, matcher.newLinesForAct, newIndex)
	if !ok {
		return false, false
	}
	equal, complete := matcher.sameLineageKeysForActBudgeted(newLineage.entries, oldLineage.entries)
	if !complete || !equal {
		return equal, complete
	}
	return matcher.hasStrongNonRootAnchorBudgeted(newLineage.entries)
}

// edgeAtTier evaluates one old-to-fresh edge. complete=false preserves the
// terminal category in budget.stop.
func (matcher *tierMatcher) edgeAtTier(oldIndex, newIndex int) (matched, lineageMismatch, complete bool) {
	if !matcher.budget.consumeCandidate() {
		return false, false, false
	}
	oldNode := &matcher.original.Nodes[oldIndex]
	newNode := &matcher.fresh.Nodes[newIndex]
	if oldNode.DisplayID != newNode.DisplayID {
		return false, false, true
	}
	compatible, _ := sameStructureForAct(oldNode, newNode, matcher.opts)
	if !compatible {
		return false, false, true
	}

	switch matcher.tier {
	case 1:
		if oldNode.Bounds != newNode.Bounds {
			return false, false, true
		}
	case 2:
		if !geometryCompatible(
			oldNode.Bounds, newNode.Bounds, matcher.screen, actTier2MinOverlap) {
			return false, false, true
		}
	case 3:
		if !geometryCompatible(
			oldNode.Bounds, newNode.Bounds, matcher.screen, actTier3MinOverlap) {
			return false, false, true
		}
	default:
		return false, false, matcher.budget.failMalformed()
	}

	var equal bool
	if matcher.tier == 3 {
		equal, complete = matcher.sameAnchoredLineageForAct(oldIndex, newIndex)
	} else {
		equal, complete = matcher.sameStrictLineage(oldIndex, newIndex)
	}
	if !complete {
		return false, false, false
	}
	return equal, !equal, true
}

func stableNonEmptyContent(aSet bool, a string, bSet bool, b string) bool {
	return aSet && bSet && a != "" && a == b
}

func knownUnprotected(a, b *device.RawNode) bool {
	if a.Editable || b.Editable {
		return a.HasPassword && !a.Password && b.HasPassword && !b.Password
	}
	return true
}

func sameTargetResource(node, want *device.RawNode, resourceID string) bool {
	return resourceID != "" && node.DisplayID == want.DisplayID &&
		node.Package == want.Package && node.ResourceSet &&
		node.ResourceID == resourceID
}

// independentTargetContent accepts only target-owned stable non-empty content,
// or a resource that is unique in both complete trees. A shared ancestor or the
// old carrier cannot serve as the target's independent identity.
func (matcher *tierMatcher) independentTargetContent(
	oldIndex, newIndex, oldResourceCount, newResourceCount int) (bool, bool) {
	want := &matcher.original.Nodes[oldIndex]
	candidate := &matcher.fresh.Nodes[newIndex]
	sameText := knownUnprotected(want, candidate) &&
		stableNonEmptyContent(
			want.TextSet, want.Text, candidate.TextSet, candidate.Text)
	sameDescription := knownUnprotected(want, candidate) &&
		stableNonEmptyContent(
			want.DescSet, want.Description, candidate.DescSet, candidate.Description)
	uniqueResource := want.Package != "" && want.ResourceSet &&
		want.ResourceID != "" && oldResourceCount == 1 && newResourceCount == 1
	return sameText || sameDescription || uniqueResource, true
}

// probeWant performs one outgoing fresh scan and, after a unique result, one
// reverse original scan. Each tier therefore visits at most Np+Nc edges.
func (matcher *tierMatcher) probeWant(wantIndex int) actProbeResult {
	want := &matcher.original.Nodes[wantIndex]
	resourceID := ""
	if want.ResourceSet {
		resourceID = want.ResourceID
	}
	newResourceCount := 0
	candidate := -1
	sawLineageMismatch := false
	for newIndex := range matcher.fresh.Nodes {
		if sameTargetResource(&matcher.fresh.Nodes[newIndex], want, resourceID) {
			newResourceCount++
		}
		matched, lineageMismatch, complete := matcher.edgeAtTier(wantIndex, newIndex)
		if !complete {
			return actProbeResult{
				state: matcher.budget.terminalState(), candidateIndex: -1}
		}
		if lineageMismatch {
			sawLineageMismatch = true
		}
		if matched {
			if candidate >= 0 {
				return actProbeResult{state: actProbeAmbiguous, candidateIndex: -1}
			}
			candidate = newIndex
		}
	}
	if candidate < 0 {
		return actProbeResult{
			state: actProbeNone, candidateIndex: -1,
			lineageMismatch: sawLineageMismatch,
		}
	}

	oldResourceCount := 0
	incomingCount := 0
	soleOldIndex := -1
	for oldIndex := range matcher.original.Nodes {
		if sameTargetResource(&matcher.original.Nodes[oldIndex], want, resourceID) {
			oldResourceCount++
		}
		matched, _, complete := matcher.edgeAtTier(oldIndex, candidate)
		if !complete {
			return actProbeResult{
				state: matcher.budget.terminalState(), candidateIndex: -1}
		}
		if matched {
			incomingCount++
			soleOldIndex = oldIndex
			if incomingCount > 1 {
				return actProbeResult{state: actProbeAmbiguous, candidateIndex: -1}
			}
		}
	}
	if incomingCount == 0 || soleOldIndex != wantIndex {
		matcher.budget.failMalformed()
		return actProbeResult{state: actProbeMalformed, candidateIndex: -1}
	}
	if !matcher.budget.checkActive() {
		return actProbeResult{
			state: matcher.budget.terminalState(), candidateIndex: -1}
	}
	independent, complete := matcher.independentTargetContent(
		wantIndex, candidate, oldResourceCount, newResourceCount)
	if !complete {
		return actProbeResult{
			state: matcher.budget.terminalState(), candidateIndex: -1}
	}
	independentSubtree, complete := subtreeEvidence(matcher.original, wantIndex,
		matcher.fresh, candidate, matcher.opts.mode, &matcher.budget)
	if !complete || !matcher.budget.checkActive() {
		return actProbeResult{
			state: matcher.budget.terminalState(), candidateIndex: -1}
	}
	return actProbeResult{
		candidateIndex: candidate, independentContent: independent,
		independentSubtree: independentSubtree, state: actProbeUnique,
	}
}

func validActCapture(capture *device.Capture) bool {
	return capture != nil && len(capture.Nodes) > 0 &&
		capture.Root >= 0 && capture.Root < len(capture.Nodes) &&
		capture.Display.Width > 0 && capture.Display.Height > 0
}

// matchActTargetWithLimits is the pure bounded matcher with the default
// tap-like policy.
func matchActTargetWithLimits(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode,
	limits actMatchLimits) (actMatch, actProbeState) {
	return matchActTargetWithOptions(ctx, revoked, original, fresh, want,
		defaultActMatchOptions().withLimits(limits))
}

// matchActTargetWithOptions tries each eligible tier in order. Tier 4 requires
// a lineage mismatch, and Tier 5 runs only after the preceding tiers find no
// match. A Tier 2/3 ambiguity between text-less sibling containers may instead
// skip Tier 4 and be decided by the Tier 5 semantic-evidence scan.
func matchActTargetWithOptions(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode,
	options actMatchOptions) (actMatch, actProbeState) {
	if ctx == nil || !validActCapture(original) || !validActCapture(fresh) ||
		want == nil {
		return actMatch{}, actProbeMalformed
	}
	originalIndex := indexOf(original, want)
	if originalIndex < 0 {
		return actMatch{}, actProbeMalformed
	}

	var stats actMatchStats
	sawLineageMismatch := false
	disambiguateInTier5 := false
lowerTiers:
	for tier := 1; tier <= 3; tier++ {
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{stats: stats}, actProbeCancelled
		}
		matcher := newTierMatcher(ctx, original, fresh, tier, options)
		probe := matcher.probeWant(originalIndex)
		stats.candidateVisits += matcher.budget.candidateVisits
		stats.lineageSteps += matcher.budget.lineageSteps
		stats.tiers = tier
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{stats: stats}, actProbeCancelled
		}
		switch probe.state {
		case actProbeNone:
			if probe.lineageMismatch {
				sawLineageMismatch = true
			}
			continue
		case actProbeUnique:
			return actMatch{
				raw: &fresh.Nodes[probe.candidateIndex], tier: tier,
				independentContent: probe.independentContent,
				independentSubtree: probe.independentSubtree, stats: stats,
			}, actProbeUnique
		case actProbeAmbiguous:
			if canDisambiguateViaTier5(want, tier, options) {
				// The collision is between text-less sibling containers, so the
				// Tier 5 subtree evidence is the only remaining discriminator:
				// leave the lower tiers at once and never fall through Tier 4.
				disambiguateInTier5 = true
				break lowerTiers
			}
			return actMatch{stats: stats}, actProbeAmbiguous
		case actProbeCancelled:
			return actMatch{stats: stats}, actProbeCancelled
		case actProbeBudgetExhausted:
			return actMatch{stats: stats}, actProbeBudgetExhausted
		default:
			return actMatch{stats: stats}, actProbeMalformed
		}
	}
	for tier := 4; tier <= 5; tier++ {
		if (tier == 4 && (!sawLineageMismatch || disambiguateInTier5)) ||
			(tier == 5 && options.disableTranslation) {
			continue
		}
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{stats: stats}, actProbeCancelled
		}
		matcher := newTierMatcher(ctx, original, fresh, tier, options)
		var probe actProbeResult
		if tier == 4 {
			probe = matcher.probeWantTier4(tier4TightGeometry, options.mode, originalIndex)
		} else {
			probe = matcher.probeWantTier5(options.mode, originalIndex)
		}
		stats.candidateVisits += matcher.budget.candidateVisits
		stats.lineageSteps += matcher.budget.lineageSteps
		stats.tiers = tier
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{stats: stats}, actProbeCancelled
		}
		switch probe.state {
		case actProbeNone:
			continue
		case actProbeUnique:
			return actMatch{
				raw: &fresh.Nodes[probe.candidateIndex], tier: tier,
				independentContent: probe.independentContent,
				independentSubtree: probe.independentSubtree, stats: stats,
			}, actProbeUnique
		default:
			return actMatch{stats: stats}, probe.state
		}
	}
	// A Tier 2/3 collision that was handed to Tier 5 must not inherit an earlier
	// lineage mismatch: the target is missing, not renamed.
	return actMatch{
		stats: stats, lineageMismatch: sawLineageMismatch && !disambiguateInTier5,
	}, actProbeNone
}

func matchActTarget(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode) (actMatch, actProbeState) {
	return matchActTargetWithOptions(
		ctx, revoked, original, fresh, want, defaultActMatchOptions())
}

// Tier 4 (anti-rebuild) constants. The bidirectional coverage gate keeps a large
// rebuilt container from swallowing a small control, and the subtree census is
// bounded per side.
const (
	actTier4MinOverlapPercent  = 80
	actTier4ScrollCoverage     = 85
	maxTier4EvidenceNodes      = 64
	maxTier4EvidenceSteps      = 1 << 16
	actTier4MinSizeTolerance   = 8
	actTier4MinCenterTolerance = 16
	actTier4SizeRatioDiv       = 5
	actTier4CenterTolDiv       = 4
)

// tier4Geometry selects the geometry gate of the Tier 4 proof.
type tier4Geometry uint8

const (
	// tier4TightGeometry is the anti-rebuild gate: both rectangles are on
	// screen and cover each other from both directions.
	tier4TightGeometry tier4Geometry = iota
	// tier4HorizontalBand is the soft-keyboard gate: the rectangle may move on
	// the vertical axis while its horizontal band stays unchanged.
	tier4HorizontalBand
)

// tightGeometryCompatible is the Tier 4 bidirectional geometry lock. Both
// rectangles must be valid on screen, cover each other by at least 80%, stay
// within the size tolerance and stay within the center tolerance.
func tightGeometryCompatible(a, b, screen device.Rect) bool {
	if !rectValidOnScreen(a, screen) || !rectValidOnScreen(b, screen) {
		return false
	}
	inter := a.Intersect(b)
	if inter.Empty() {
		return false
	}
	areaA, areaB, areaInter := rectArea64(a), rectArea64(b), rectArea64(inter)
	if areaA <= 0 || areaB <= 0 || areaInter <= 0 {
		return false
	}
	if areaInter*100 < areaA*actTier4MinOverlapPercent ||
		areaInter*100 < areaB*actTier4MinOverlapPercent {
		return false
	}
	aw, ah := a.Width(), a.Height()
	bw, bh := b.Width(), b.Height()
	if absInt(aw-bw) > max(actTier4MinSizeTolerance,
		max(aw, bw)/actTier4SizeRatioDiv) ||
		absInt(ah-bh) > max(actTier4MinSizeTolerance,
			max(ah, bh)/actTier4SizeRatioDiv) {
		return false
	}
	ax, ay := a.Center()
	bx, by := b.Center()
	return absInt(ax-bx) <= max(actTier4MinCenterTolerance,
		max(aw, bw)/actTier4CenterTolDiv) &&
		absInt(ay-by) <= max(actTier4MinCenterTolerance,
			max(ah, bh)/actTier4CenterTolDiv)
}

// tier4GeometryCompatible applies one Tier 4 geometry gate on screen.
func tier4GeometryCompatible(gate tier4Geometry, a, b, screen device.Rect) bool {
	switch gate {
	case tier4HorizontalBand:
		return rectValidOnScreen(b, screen) && sameHorizontalBand(a, b)
	default:
		return tightGeometryCompatible(a, b, screen)
	}
}

// countResourceAnchors censuses every non-empty resource identity of one tree.
// Missing or malformed captures fail the proof instead of guessing.
func countResourceAnchors(capture *device.Capture,
	budget *matchBudget) (map[resourceAnchor]int, bool) {
	if capture == nil || budget == nil {
		return nil, false
	}
	counts := make(map[resourceAnchor]int)
	for index := range capture.Nodes {
		if !budget.consumeEvidence() {
			return nil, false
		}
		node := &capture.Nodes[index]
		if !node.ResourceSet || node.ResourceID == "" {
			continue
		}
		counts[resourceAnchor{
			displayID: node.DisplayID, pkg: node.Package,
			resource: node.ResourceID,
		}]++
	}
	return counts, true
}

// ensureResourceCounts computes the E2 census once per matcher.
func (matcher *tierMatcher) ensureResourceCounts() (*resourceCounts, bool) {
	if matcher.countsReady {
		return matcher.counts, matcher.counts != nil
	}
	matcher.countsReady = true
	original, ok := countResourceAnchors(matcher.original, &matcher.budget)
	if !ok {
		return nil, false
	}
	fresh, ok := countResourceAnchors(matcher.fresh, &matcher.budget)
	if !ok {
		return nil, false
	}
	matcher.counts = &resourceCounts{original: original, fresh: fresh}
	return matcher.counts, true
}

// tier4SelfEvidence is E1: the target itself carries the same non-empty
// readable text or description.
func tier4SelfEvidence(oldNode, newNode *device.RawNode) bool {
	if oldNode == nil || newNode == nil ||
		!knownUnprotected(oldNode, newNode) {
		return false
	}
	return stableNonEmptyContent(
		oldNode.TextSet, oldNode.Text, newNode.TextSet, newNode.Text) ||
		stableNonEmptyContent(
			oldNode.DescSet, oldNode.Description,
			newNode.DescSet, newNode.Description)
}

// tier4UniqueResource is E2: one non-empty resource identity that occurs
// exactly once in each complete tree.
func tier4UniqueResource(oldNode, newNode *device.RawNode,
	counts *resourceCounts) bool {
	if oldNode == nil || newNode == nil || counts == nil ||
		!oldNode.ResourceSet || oldNode.ResourceID == "" ||
		!newNode.ResourceSet || newNode.ResourceID != oldNode.ResourceID {
		return false
	}
	anchor := resourceAnchor{
		displayID: oldNode.DisplayID, pkg: oldNode.Package,
		resource: oldNode.ResourceID,
	}
	return counts.original[anchor] == 1 && counts.fresh[anchor] == 1
}

// hasOwnReadableText reports whether a node carries its own readable text; E3 is
// reserved for nodes without one.
func hasOwnReadableText(node *device.RawNode) bool {
	return node != nil &&
		((node.TextSet && node.Text != "") ||
			(node.DescSet && node.Description != ""))
}

// canDisambiguateViaTier5 reports whether one Tier 2/3 ambiguity may be handed to
// the Tier 5 semantic-evidence scan instead of being reported immediately. Only a
// text-less container with readable descendants on the ordinary tap-like policy
// qualifies: a control that already carries its own readable text, a leaf without
// descendants, the write-submit and coordinate-owner policy (translation
// disabled) and the scroll policy all keep the immediate rejection.
func canDisambiguateViaTier5(want *device.RawNode, tier int,
	options actMatchOptions) bool {
	if tier != 2 && tier != 3 {
		return false
	}
	if options.disableTranslation || options.mode != actMatchTapLike {
		return false
	}
	return want != nil && !hasOwnReadableText(want) && len(want.Children) > 0
}

// readableSubtree collects the non-empty readable Text and Description values of
// the descendants in preorder. Visible, unprotected nodes only; every visited
// node is charged to the evidence ceiling and at most maxTier4EvidenceNodes
// descendants are visited.
func readableSubtree(capture *device.Capture, index int,
	budget *matchBudget) ([]string, bool) {
	if capture == nil || budget == nil || index < 0 || index >= len(capture.Nodes) {
		return nil, false
	}
	var texts []string
	seen := make(map[int]struct{}, 8)
	stack := make([]int, 0, 8)
	pushChildren := func(node *device.RawNode) {
		for childIndex := len(node.Children) - 1; childIndex >= 0; childIndex-- {
			stack = append(stack, node.Children[childIndex])
		}
	}
	pushChildren(&capture.Nodes[index])
	visited := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current < 0 || current >= len(capture.Nodes) {
			budget.failMalformed()
			return nil, false
		}
		if _, duplicate := seen[current]; duplicate {
			continue
		}
		seen[current] = struct{}{}
		if visited >= maxTier4EvidenceNodes {
			break
		}
		if !budget.consumeEvidence() {
			return nil, false
		}
		visited++
		node := &capture.Nodes[current]
		if node.Visible && !node.Password {
			if node.TextSet && node.Text != "" {
				texts = append(texts, node.Text)
			}
			if node.DescSet && node.Description != "" {
				texts = append(texts, node.Description)
			}
		}
		pushChildren(node)
	}
	return texts, true
}

func sortedReadableTexts(texts []string) []string {
	if len(texts) == 0 {
		return nil
	}
	sorted := append([]string(nil), texts...)
	sort.Strings(sorted)
	return sorted
}

// sameReadableMultiset reports whether both sides carry the same non-empty
// readable multiset.
func sameReadableMultiset(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 || len(left) != len(right) {
		return false
	}
	first, second := sortedReadableTexts(left), sortedReadableTexts(right)
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func isDynamicNumericToken(s string) bool {
	hasDigit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case strings.ContainsRune(".,:+-/% ", r):
		default:
			return false
		}
	}
	return hasDigit
}

func numericSkeleton(s string) string {
	var skeleton strings.Builder
	inDigits := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			if !inDigits {
				skeleton.WriteByte('#')
			}
			inDigits = true
			continue
		}
		skeleton.WriteByte(c)
		inDigits = false
	}
	return skeleton.String()
}

func subtreeSkeletonMatch(oldTexts, newTexts []string) bool {
	skeletons := func(texts []string) []string {
		result := make([]string, 0, len(texts))
		for _, text := range texts {
			if text != "" && !isDynamicNumericToken(text) {
				result = append(result, numericSkeleton(text))
			}
		}
		return result
	}
	if !sameReadableMultiset(skeletons(oldTexts), skeletons(newTexts)) {
		return false
	}
	anchors := make(map[string]struct{})
	for _, text := range oldTexts {
		if text != "" && strings.IndexFunc(text, unicode.IsDigit) < 0 {
			anchors[text] = struct{}{}
		}
	}
	for _, text := range newTexts {
		if _, ok := anchors[text]; ok {
			return true
		}
	}
	return false
}

// sharesReadableText reports whether both sides share at least one readable
// entry. It is the scroll-container relaxation of E3.
func sharesReadableText(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, found := seen[value]; found {
			return true
		}
	}
	return false
}

// coverageAtLeastPercent reports whether both rectangles cover each other by at
// least the requested percentage.
func coverageAtLeastPercent(a, b device.Rect, minPercent int64) bool {
	inter := a.Intersect(b)
	if inter.Empty() {
		return false
	}
	areaA, areaB, areaInter := rectArea64(a), rectArea64(b), rectArea64(inter)
	if areaA <= 0 || areaB <= 0 || areaInter <= 0 {
		return false
	}
	return areaInter*100 >= areaA*minPercent && areaInter*100 >= areaB*minPercent
}

// subtreeEvidence applies E3 for one target pair. Tap-like actions can use
// numeric conservation after an exact multiset miss. A scroll container accepts
// a shared entry or the same non-empty resource with strong coverage.
func subtreeEvidence(original *device.Capture, oldIndex int,
	fresh *device.Capture, newIndex int, mode actMatchMode,
	budget *matchBudget) (bool, bool) {
	if original == nil || fresh == nil || budget == nil ||
		oldIndex < 0 || oldIndex >= len(original.Nodes) ||
		newIndex < 0 || newIndex >= len(fresh.Nodes) {
		return false, false
	}
	oldNode := &original.Nodes[oldIndex]
	newNode := &fresh.Nodes[newIndex]
	if hasOwnReadableText(oldNode) || hasOwnReadableText(newNode) {
		return false, true
	}
	if len(oldNode.Children) == 0 || len(newNode.Children) == 0 {
		return false, true
	}
	oldTexts, ok := readableSubtree(original, oldIndex, budget)
	if !ok {
		return false, false
	}
	newTexts, ok := readableSubtree(fresh, newIndex, budget)
	if !ok {
		return false, false
	}
	if mode == actMatchScroll {
		if sharesReadableText(oldTexts, newTexts) {
			return true, true
		}
		return sameNonEmptyResourceCoverage(oldNode, newNode, actTier4ScrollCoverage), true
	}
	if sameReadableMultiset(oldTexts, newTexts) {
		return true, true
	}
	return mode == actMatchTapLike && subtreeSkeletonMatch(oldTexts, newTexts), true
}

// sameNonEmptyResourceCoverage accepts the same non-empty resource identity when
// both rectangles cover each other strongly.
func sameNonEmptyResourceCoverage(a, b *device.RawNode, minPercent int64) bool {
	if a == nil || b == nil || !a.ResourceSet || a.ResourceID == "" ||
		!b.ResourceSet || b.ResourceID != a.ResourceID {
		return false
	}
	return coverageAtLeastPercent(a.Bounds, b.Bounds, minPercent)
}

// tier4Evidence applies the three-way strong identity evidence of E1, E2, E3.
func (matcher *tierMatcher) tier4Evidence(oldIndex, newIndex int,
	mode actMatchMode) (bool, bool) {
	oldNode := &matcher.original.Nodes[oldIndex]
	newNode := &matcher.fresh.Nodes[newIndex]
	if tier4SelfEvidence(oldNode, newNode) {
		return true, true
	}
	counts, ok := matcher.ensureResourceCounts()
	if !ok {
		return false, false
	}
	if tier4UniqueResource(oldNode, newNode, counts) {
		return true, true
	}
	return subtreeEvidence(matcher.original, oldIndex, matcher.fresh, newIndex,
		mode, &matcher.budget)
}

// tier4Edge evaluates one anti-rebuild candidate edge. complete=false preserves
// the terminal category in budget.stop.
func (matcher *tierMatcher) tier4Edge(gate tier4Geometry, mode actMatchMode,
	oldIndex, newIndex int) (matched, complete bool) {
	if !matcher.budget.consumeCandidate() {
		return false, false
	}
	oldNode := &matcher.original.Nodes[oldIndex]
	newNode := &matcher.fresh.Nodes[newIndex]
	if oldNode.DisplayID != newNode.DisplayID {
		return false, true
	}
	compatible, _ := sameStructureForAct(oldNode, newNode, matcher.opts)
	if !compatible {
		return false, true
	}
	if !tier4GeometryCompatible(gate, oldNode.Bounds, newNode.Bounds, matcher.screen) {
		return false, true
	}
	return matcher.tier4Evidence(oldIndex, newIndex, mode)
}

// probeWantTier4 proves one anti-rebuild target: a structure-compatible,
// geometrically locked fresh node with strong semantic evidence that is unique in
// both directions of the complete trees.
func (matcher *tierMatcher) probeWantTier4(gate tier4Geometry,
	mode actMatchMode, wantIndex int) actProbeResult {
	return matcher.probeWantWithEvidence(wantIndex, func(oldIndex, newIndex int) (bool, bool) {
		return matcher.tier4Edge(gate, mode, oldIndex, newIndex)
	})
}

func (matcher *tierMatcher) tier5Edge(mode actMatchMode,
	oldIndex, newIndex int) (matched, complete bool) {
	if !matcher.budget.consumeCandidate() {
		return false, false
	}
	oldNode := &matcher.original.Nodes[oldIndex]
	newNode := &matcher.fresh.Nodes[newIndex]
	if oldNode.DisplayID != newNode.DisplayID {
		return false, true
	}
	compatible, _ := sameStructureForAct(oldNode, newNode, matcher.opts)
	if !compatible || !rectValidOnScreen(newNode.Bounds, matcher.screen) ||
		!sameHorizontalBand(oldNode.Bounds, newNode.Bounds) {
		return false, true
	}
	aligned, complete := matcher.sameStrictLineage(oldIndex, newIndex)
	if complete && !aligned {
		aligned, complete = matcher.sameAnchoredLineageForAct(oldIndex, newIndex)
	}
	if !complete || !aligned {
		return false, complete
	}
	return matcher.tier4Evidence(oldIndex, newIndex, mode)
}

func (matcher *tierMatcher) probeWantTier5(mode actMatchMode, wantIndex int) actProbeResult {
	return matcher.probeWantWithEvidence(wantIndex, func(oldIndex, newIndex int) (bool, bool) {
		return matcher.tier5Edge(mode, oldIndex, newIndex)
	})
}

// probeWantWithEvidence shares one budget across the forward and reverse scans.
func (matcher *tierMatcher) probeWantWithEvidence(wantIndex int,
	edge func(oldIndex, newIndex int) (matched, complete bool)) actProbeResult {
	if wantIndex < 0 || wantIndex >= len(matcher.original.Nodes) {
		matcher.budget.failMalformed()
		return actProbeResult{state: actProbeMalformed, candidateIndex: -1}
	}
	candidate := -1
	for newIndex := range matcher.fresh.Nodes {
		matched, complete := edge(wantIndex, newIndex)
		if !complete {
			return actProbeResult{
				state: matcher.budget.terminalState(), candidateIndex: -1}
		}
		if !matched {
			continue
		}
		if candidate >= 0 {
			return actProbeResult{state: actProbeAmbiguous, candidateIndex: -1}
		}
		candidate = newIndex
	}
	if candidate < 0 {
		return actProbeResult{state: actProbeNone, candidateIndex: -1}
	}
	incomingCount := 0
	soleOldIndex := -1
	for oldIndex := range matcher.original.Nodes {
		matched, complete := edge(oldIndex, candidate)
		if !complete {
			return actProbeResult{
				state: matcher.budget.terminalState(), candidateIndex: -1}
		}
		if !matched {
			continue
		}
		incomingCount++
		soleOldIndex = oldIndex
		if incomingCount > 1 {
			return actProbeResult{state: actProbeAmbiguous, candidateIndex: -1}
		}
	}
	if incomingCount != 1 || soleOldIndex != wantIndex {
		matcher.budget.failMalformed()
		return actProbeResult{state: actProbeMalformed, candidateIndex: -1}
	}
	if !matcher.budget.checkActive() {
		return actProbeResult{
			state: matcher.budget.terminalState(), candidateIndex: -1}
	}
	return actProbeResult{
		candidateIndex: candidate, independentContent: true,
		independentSubtree: true, state: actProbeUnique,
	}
}

// matchKeyboardShiftedSubmitNode proves one write-submit binding that only moved
// on the vertical axis with the soft keyboard: unchanged horizontal band, strong
// semantic evidence and whole-tree bidirectional uniqueness. It never authorizes
// dispatch by itself; the submit stage still proves the explicit enabled state.
func matchKeyboardShiftedSubmitNode(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode,
	allowEnabledTransition bool,
	limits actMatchLimits) (actMatch, actProbeState) {
	if ctx == nil || !validActCapture(original) || !validActCapture(fresh) ||
		want == nil {
		return actMatch{}, actProbeMalformed
	}
	wantIndex := indexOf(original, want)
	if wantIndex < 0 {
		return actMatch{}, actProbeMalformed
	}
	options := defaultActMatchOptions().withLimits(limits)
	if allowEnabledTransition {
		options = options.withEnabledTransition()
	}
	if ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	matcher := newTierMatcher(ctx, original, fresh, 4, options)
	probe := matcher.probeWantTier4(tier4HorizontalBand, actMatchTapLikeExact, wantIndex)
	stats := actMatchStats{
		candidateVisits: matcher.budget.candidateVisits,
		lineageSteps:    matcher.budget.lineageSteps,
		tiers:           4,
	}
	if ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{stats: stats}, actProbeCancelled
	}
	switch probe.state {
	case actProbeUnique:
		return actMatch{
			raw: &fresh.Nodes[probe.candidateIndex], tier: 4,
			independentContent: true, stats: stats,
		}, actProbeUnique
	case actProbeAmbiguous:
		return actMatch{stats: stats}, actProbeAmbiguous
	case actProbeCancelled:
		return actMatch{stats: stats}, actProbeCancelled
	case actProbeBudgetExhausted:
		return actMatch{stats: stats}, actProbeBudgetExhausted
	case actProbeMalformed:
		return actMatch{stats: stats}, actProbeMalformed
	default:
		return actMatch{stats: stats}, actProbeNone
	}
}

type tapFrame struct {
	capture  *device.Capture
	raw      *device.RawNode
	x, y     int
	carrier  *device.RawNode
	tier     int
	strategy string
	rebound  bool
}

// resolveTapScopePoint applies the same deepest-unique rule as
// provesSubtreeTapPoint while including the scope target itself. resolvePoint
// starts below a tree root and therefore cannot name that depth-zero target.
func resolveTapScopePoint(scope *tapScope, x, y int) (*protocol.Node, bool) {
	if scope == nil || scope.target == nil {
		return nil, false
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
		return nil, false
	}
	return best, true
}

// pointOwner returns a carrier only when there is one proven deepest hit.
func pointOwner(scope *tapScope, x, y int) (*tapCarrier, bool) {
	if provesSubtreeTapPoint(scope, x, y) {
		if hit, ok := resolveTapScopePoint(scope, x, y); ok {
			if hit == scope.target {
				return nil, true
			}
			carrier := scope.carrierByNode[hit]
			return carrier, carrier != nil
		}
	}
	return nil, false
}

func revalidatedPointOwner(view *tapPublicView, scope *tapScope, x, y int) (*tapCarrier, bool) {
	if carrier, ok := pointOwner(scope, x, y); ok {
		return carrier, true
	}
	return nil, scope != nil && provesGuidancePointPassive(view, scope.target, scope, x, y)
}

func pointHitsFixedCarrier(scope *tapScope, x, y int,
	fixedCarrier *tapCarrier) bool {
	if scope == nil || fixedCarrier == nil ||
		!provesSubtreeTapPoint(scope, x, y) {
		return false
	}
	hit, ok := resolveTapScopePoint(scope, x, y)
	if !ok || hit == scope.target {
		return false
	}
	return scope.carrierByNode[hit] == fixedCarrier
}

func fixedCarrierSafePoint(scope *tapScope, targetRaw *device.RawNode,
	fixedCarrier *tapCarrier) (int, int, string, bool) {
	if scope == nil || fixedCarrier == nil {
		return 0, 0, "", false
	}
	for _, candidate := range enumerateTapCandidates(scope, targetRaw) {
		if candidate.carrier != fixedCarrier {
			continue
		}
		if pointHitsFixedCarrier(scope, candidate.x, candidate.y, fixedCarrier) {
			return candidate.x, candidate.y, candidate.strategy, true
		}
	}
	return 0, 0, "", false
}

func inputPlanStrategyForPoint(scope *tapScope, carrier *tapCarrier) string {
	if carrier != nil {
		return tapModeCarrierNode
	}
	return tapModeDefaultCenter
}

func chooseUnboundTapPlan(scope *tapScope, input *tapInput,
	target *protocol.Node, action protocol.Action,
	preferX, preferY int) (*tapPlan, *protocol.APIError) {
	if carrier, ok := revalidatedPointOwner(input.view, scope, preferX, preferY); ok {
		return &tapPlan{
			x: preferX, y: preferY,
			strategy:  inputPlanStrategyForPoint(scope, carrier),
			action:    string(action),
			targetRef: target.Ref, targetRaw: input.targetRaw,
			carrierRef: carrierRef(carrier), carrierRaw: carrierRaw(carrier),
		}, nil
	}
	return chooseTapPlan(scope, input, target, action, time.Now())
}

func carrierRef(carrier *tapCarrier) string {
	if carrier == nil || carrier.node == nil {
		return ""
	}
	return carrier.node.Ref
}

func carrierRaw(carrier *tapCarrier) *device.RawNode {
	if carrier == nil {
		return nil
	}
	return carrier.raw
}
