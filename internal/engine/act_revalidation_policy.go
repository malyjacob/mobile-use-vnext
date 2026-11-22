package engine

import (
	"context"
	"time"

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

// sameStructure reports whether the two nodes have no known conflict and
// whether their known fields supply positive identity evidence. Unknown and
// unknown produce neither a conflict nor positive evidence.
func sameStructure(a, b *device.RawNode) (compatible, evidence bool) {
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
	for _, pair := range [][2]tri{
		{left.Clickable, right.Clickable},
		{left.LongClickable, right.LongClickable},
		{left.Scrollable, right.Scrollable},
		{left.Enabled, right.Enabled},
		{left.Checked, right.Checked},
		{left.Password, right.Password},
		{left.Checkable, right.Checkable},
	} {
		if !consumeTri(pair[0], pair[1]) {
			return false, false
		}
	}
	return true, evidence
}

type lineageEntry struct {
	parent int
	key    structureKey
	root   bool
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
	stop            matchGate
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

type tierMatcher struct {
	ctx      context.Context
	original *device.Capture
	fresh    *device.Capture
	tier     int
	screen   device.Rect
	budget   matchBudget
	oldLines map[int]lineageAtTier
	newLines map[int]lineageAtTier
}

func newTierMatcher(ctx context.Context, original, fresh *device.Capture,
	tier int, limits actMatchLimits) *tierMatcher {
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
	return &tierMatcher{
		ctx: ctx, original: original, fresh: fresh, tier: tier,
		screen: fresh.Display.Bounds(),
		budget: matchBudget{
			ctx: ctx, candidateLimit: candidateLimit,
			lineageLimit: lineageLimit, lineageDepth: lineageDepth,
		},
		oldLines: make(map[int]lineageAtTier),
		newLines: make(map[int]lineageAtTier),
	}
}

func (matcher *tierMatcher) lineage(capture *device.Capture,
	memo map[int]lineageAtTier, index int) (lineageAtTier, bool) {
	if cached, ok := memo[index]; ok {
		return cached, cached.ok
	}
	var cached lineageAtTier
	if matcher.tier == 3 {
		cached.entries, cached.ok = lineageL2(capture, index, &matcher.budget)
	} else {
		cached.entries, cached.ok = lineageL1(capture, index, &matcher.budget)
	}
	memo[index] = cached
	return cached, cached.ok
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

func (matcher *tierMatcher) hasStrongNonRootAnchorBudgeted(
	chain []lineageEntry) (found, complete bool) {
	for _, entry := range chain {
		if !matcher.budget.consumeLineage(0) {
			return false, false
		}
		if !entry.root && strongLineageAnchor(entry) {
			return true, true
		}
	}
	return false, true
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
	compatible, _ := sameStructure(oldNode, newNode)
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

	oldLineage, ok := matcher.lineage(matcher.original, matcher.oldLines, oldIndex)
	if !ok {
		return false, false, false
	}
	newLineage, ok := matcher.lineage(matcher.fresh, matcher.newLines, newIndex)
	if !ok {
		return false, false, false
	}
	equal, complete := matcher.sameLineageKeysBudgeted(
		newLineage.entries, oldLineage.entries)
	if !complete {
		return false, false, false
	}
	if !equal {
		return false, true, true
	}
	if matcher.tier == 3 {
		found, complete := matcher.hasStrongNonRootAnchorBudgeted(newLineage.entries)
		if !complete {
			return false, false, false
		}
		return found, !found, true
	}
	return true, false, true
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
	return actProbeResult{
		candidateIndex: candidate, independentContent: independent,
		state: actProbeUnique,
	}
}

func validActCapture(capture *device.Capture) bool {
	return capture != nil && len(capture.Nodes) > 0 &&
		capture.Root >= 0 && capture.Root < len(capture.Nodes) &&
		capture.Display.Width > 0 && capture.Display.Height > 0
}

// matchActTargetWithLimits is the pure bounded matcher. Tier 4 is deliberately
// absent: the three tiers below are the complete default policy.
func matchActTargetWithLimits(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode,
	limits actMatchLimits) (actMatch, actProbeState) {
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
	for tier := 1; tier <= 3; tier++ {
		if ctx.Err() != nil || (revoked != nil && revoked()) {
			return actMatch{stats: stats}, actProbeCancelled
		}
		matcher := newTierMatcher(ctx, original, fresh, tier, limits)
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
				independentContent: probe.independentContent, stats: stats,
			}, actProbeUnique
		case actProbeAmbiguous:
			return actMatch{stats: stats}, actProbeAmbiguous
		case actProbeCancelled:
			return actMatch{stats: stats}, actProbeCancelled
		case actProbeBudgetExhausted:
			return actMatch{stats: stats}, actProbeBudgetExhausted
		default:
			return actMatch{stats: stats}, actProbeMalformed
		}
	}
	return actMatch{stats: stats, lineageMismatch: sawLineageMismatch}, actProbeNone
}

func matchActTarget(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode) (actMatch, actProbeState) {
	return matchActTargetWithLimits(
		ctx, revoked, original, fresh, want, defaultActMatchLimits())
}

type tapFrame struct {
	capture  *device.Capture
	raw      *device.RawNode
	x, y     int
	carrier  *device.RawNode
	tier     int
	strategy string
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

// pointOwner explains a point already proven by provesSubtreeTapPoint.
func pointOwner(scope *tapScope, x, y int) (*tapCarrier, bool) {
	if scope == nil || !provesSubtreeTapPoint(scope, x, y) {
		return nil, false
	}
	hit, ok := resolveTapScopePoint(scope, x, y)
	if !ok {
		return nil, false
	}
	if hit == scope.target {
		return nil, true
	}
	carrier := scope.carrierByNode[hit]
	return carrier, carrier != nil
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
	if carrier, ok := pointOwner(scope, preferX, preferY); ok {
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
