package engine

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// This file is the end-to-end scenario regression suite for
// maintenance/proposals/scroll-axis-fallback-and-amount-tiers.md (remote
// Issue #42). Every case drives the public act(scroll) entry on a synthetic but
// realistic nested page: a scripted device capture, the real observation
// projection, the real static planner, the real fresh revalidation, one real
// swipe receipt and, where a selection mode matters, the real scroll.plan event
// log. No planner helper is called directly to produce the asserted numbers.

// ---------------------------------------------------------------- Issue #42 pages

var (
	// issue42OuterListRect is the 1080x2400 full-screen virtual display.
	issue42OuterListRect = device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
	// issue42TabStripRect is the short transverse Tab strip of the field report:
	// 140px tall, so the pre-proposal planner could still prove 55px of "travel".
	issue42TabStripRect = device.Rect{Left: 0, Top: 160, Right: 1080, Bottom: 300}
	// issue42TabTitleRect is the Tab label inside the strip.
	issue42TabTitleRect = device.Rect{Left: 0, Top: 180, Right: 1080, Bottom: 280}
)

// issue42NestedTabPage is the Issue #42 page: one full-screen vertical list whose
// top row is a horizontal Tab strip, with three ordinary list items below it.
// The strip has a single non-scroll child, so its published axis comes from its
// class name; the list is vertical either way.
func issue42NestedTabPage() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: issue42OuterListRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: issue42TabStripRect,
			Class: "android.widget.HorizontalScrollView", Scrollable: true},
		{Parent: 2, Bounds: issue42TabTitleRect, Class: "android.widget.TextView",
			Text: "tab-title"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 400, Right: 1080, Bottom: 600},
			Class: "android.widget.TextView", Text: "item-one"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "item-two"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1200, Right: 1080, Bottom: 1400},
			Class: "android.widget.TextView", Text: "item-three"},
	}
}

// issue42PanelRect is the only scroll scope of the transverse-only page.
var issue42PanelRect = device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 600}

// issue42TransversePanelPage is a page whose only scroll container is horizontal
// (no vertical ancestor and no vertical sibling), with one plain title inside it.
func issue42TransversePanelPage() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: issue42PanelRect, Class: "android.widget.HorizontalScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 200},
			Class: "android.widget.TextView", Text: "panel-title"},
	}
}

var (
	issue42UnknownOuterRect = device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2200}
	issue42UnknownStripRect = device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 650}
)

// issue42UnknownAxisStripPage is the flat-strip page of the C guard: a 1080x150
// strip with no class axis evidence and no usable child layout sits inside a
// 1080x2200 vertical list.
func issue42UnknownAxisStripPage() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: issue42UnknownOuterRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: issue42UnknownStripRect, Class: "android.view.View",
			Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 520, Right: 1080, Bottom: 630},
			Class: "android.widget.TextView", Text: "flat-tab"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "body"},
	}
}

var issue42InnerListRect = device.Rect{Left: 0, Top: 600, Right: 1080, Bottom: 1600}

// issue42VerticalListInListPage nests a vertical list inside the vertical list.
func issue42VerticalListInListPage() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: issue42OuterListRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: issue42InnerListRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 700, Right: 1080, Bottom: 900},
			Class: "android.widget.TextView", Text: "row-one"},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "row-two"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1700, Right: 1080, Bottom: 1900},
			Class: "android.widget.TextView", Text: "page-item"},
	}
}

var issue42SmallWheelRect = device.Rect{Left: 0, Top: 160, Right: 1080, Bottom: 360}

// issue42SmallVerticalWheelPage nests a 200px-tall explicit vertical wheel inside
// the full-screen vertical list. Its flat geometry alone would trip the C guard.
func issue42SmallVerticalWheelPage() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: issue42OuterListRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: issue42SmallWheelRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 180, Right: 1080, Bottom: 340},
			Class: "android.widget.TextView", Text: "wheel-value"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900},
			Class: "android.widget.TextView", Text: "form-row"},
	}
}

var (
	issue42CandidateBarRect  = device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 400}
	issue42CandidateListRect = device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 2400}
)

// issue42TransverseAndVerticalPage has a transverse bar and a vertical list as
// siblings, so an axis-mismatch rejection can advertise the executable one.
func issue42TransverseAndVerticalPage() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: issue42CandidateBarRect,
			Class: "android.widget.HorizontalScrollView", Scrollable: true},
		{Parent: 0, Bounds: issue42CandidateListRect, Class: "android.widget.ScrollView",
			Scrollable: true},
	}
}

// ---------------------------------------------------------------- assertions

// issue42RequireExecutedScroll asserts one executed controlled scroll that
// published its post observation.
func issue42RequireExecutedScroll(t *testing.T, result *protocol.Result) {
	t.Helper()
	if result == nil || result.Outcome != OutcomeExecuted {
		t.Fatalf("scroll outcome = %+v, want executed", result)
	}
	if result.Error != nil {
		t.Fatalf("executed scroll carries error %+v", result.Error)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatal("executed scroll published no post observation")
	}
}

// issue42RequireStaticRejection asserts one claim-free static 422 with the given
// reason: no swipe, no new capture, no effect, an effective retry on the same O
// and an observation that stays ready. It returns the details map.
func issue42RequireStaticRejection(t *testing.T, h *harness, observationID string,
	operation protocol.Operation, wantReason string) map[string]any {
	t.Helper()
	captures, swipes := h.device.captureCount(), swipeCount(h)
	result := h.actP(t, "", observationID, operation)
	details := proposalRequireStaticReason(t, result, protocol.CodeNodeUnsupported, wantReason)
	if result.Effect != nil {
		t.Fatalf("static rejection carries effect %+v, want omitted", result.Effect)
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("static rejection performed %d new capture(s)", got-captures)
	}
	if got := swipeCount(h); got != swipes {
		t.Fatalf("static rejection performed %d swipe(s)", got-swipes)
	}
	if status := observationStatus(h, observationID); status != ObservationReady {
		t.Fatalf("static rejection left observation %q, want ready", status)
	}
	return details
}

// issue42PlanFields closes the event log and parses the single scroll.plan record.
func issue42PlanFields(t *testing.T, buffer *strings.Builder,
	logger *eventlog.Logger) map[string]string {
	t.Helper()
	logger.Close()
	var lines []string
	for _, line := range strings.Split(buffer.String(), "\n") {
		if strings.Contains(line, " scroll.plan ") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("scroll.plan lines = %d, want exactly one:\n%s", len(lines), buffer.String())
	}
	fields := make(map[string]string)
	for _, token := range strings.Fields(lines[0]) {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			continue
		}
		fields[key] = strings.Trim(value, `"`)
	}
	return fields
}

// issue42RequirePlan pins the whitelisted plan facts: the selection mode that
// proves where the gesture was planned, plus the request direction and tier.
func issue42RequirePlan(t *testing.T, fields map[string]string,
	mode, direction, amount string) {
	t.Helper()
	if got := fields["mode"]; got != mode {
		t.Fatalf("scroll.plan mode = %q, want %q (fields %v)", got, mode, fields)
	}
	if got := fields["direction"]; got != direction {
		t.Fatalf("scroll.plan direction = %q, want %q", got, direction)
	}
	if got := fields["amount"]; got != amount {
		t.Fatalf("scroll.plan amount = %q, want %q", got, amount)
	}
}

// issue42ScrollNode returns the published scroll container whose rectangle is rect.
func issue42ScrollNode(t *testing.T, h *harness, rect device.Rect) *protocol.Node {
	t.Helper()
	node := findPublic(currentTree(t, h.engine).Root, func(candidate *protocol.Node) bool {
		return candidate.Role == protocol.RoleScroll && candidate.Bounds != nil &&
			rectFromBounds(candidate.Bounds) == rect
	})
	if node == nil {
		t.Fatalf("public tree has no role=scroll container at %+v", rect)
	}
	return node
}

// issue42TextNode returns the published text node carrying label.
func issue42TextNode(t *testing.T, h *harness, label string) *protocol.Node {
	t.Helper()
	node := findPublic(currentTree(t, h.engine).Root, func(candidate *protocol.Node) bool {
		if candidate.Role != protocol.RoleText {
			return false
		}
		for _, candidateLabel := range candidate.Label {
			if candidateLabel == label {
				return true
			}
		}
		return false
	})
	if node == nil {
		t.Fatalf("public tree has no text node labelled %q", label)
	}
	return node
}

// issue42DetailActions returns one []string detail field.
func issue42DetailActions(t *testing.T, details map[string]any, key string) []string {
	t.Helper()
	raw, exists := details[key]
	if !exists {
		t.Fatalf("details lack %q: %#v", key, details)
	}
	actions, ok := raw.([]string)
	if !ok {
		t.Fatalf("details.%s = %#v (%T), want []string", key, raw, raw)
	}
	return actions
}

// issue42NewLoggedHarness builds the standard harness with a real event log.
func issue42NewLoggedHarness(t *testing.T) (*harness, *strings.Builder, *eventlog.Logger) {
	t.Helper()
	buffer := &strings.Builder{}
	logger := eventlog.New(buffer, "instance-test")
	t.Cleanup(logger.Close)
	return newHarnessWithLogger(t, true, logger), buffer, logger
}

// ---------------------------------------------------------------- scenario 1

// TestIssue42TabStripDownFallsBackToOuterList is the core Issue #42 regression:
// asking the transverse Tab strip to scroll down must plan on the outer vertical
// list instead of dispatching a 55px mini-slide inside the 140px strip.
func TestIssue42TabStripDownFallsBackToOuterList(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42NestedTabPage())
	h.device.scriptCaptures(page)
	start := h.startSession()

	outer := issue42ScrollNode(t, h, issue42OuterListRect)
	tab := issue42ScrollNode(t, h, issue42TabStripRect)
	if outer.Axis != protocol.AxisY {
		t.Fatalf("outer list published axis = %q, want %q", outer.Axis, protocol.AxisY)
	}
	if tab.Axis != protocol.AxisX {
		t.Fatalf("tab strip published axis = %q, want %q", tab.Axis, protocol.AxisX)
	}

	h.device.scriptCaptures(page, page)
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: tab.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	})
	issue42RequireExecutedScroll(t, result)

	// Outer list, center line x=540: the strip blocks [160,300), so the longest
	// provable interval is [300,2400) -> extent 2100, margin 210, usable 1680,
	// page travel 1008 around mid 1350.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1854,
		x2: 540, y2: 846, durationMS: 400})

	call := swipeCalls(h)[0]
	if call.x1 != call.x2 {
		t.Fatalf("swipe is not vertical: %+v", call)
	}
	travel := call.y1 - call.y2
	if travel != 1008 {
		t.Fatalf("vertical travel = %d, want the outer-list page travel 1008", travel)
	}
	// Reverse control for the axis gate: without it the strip itself plans a
	// short vertical slide, because a 140px extent still proves more than the
	// 48px minimum travel. Any travel longer than the whole strip can only have
	// been planned on the outer list.
	stripTravel, stripPlannable := scrollTravelForExtent(
		issue42TabStripRect.Height(), protocol.ScrollAmountPage)
	if !stripPlannable || stripTravel <= 0 || stripTravel > issue42TabStripRect.Height() {
		t.Fatalf("strip self-plan = %d px (plannable=%v): the pre-proposal mini-slide "+
			"is no longer reachable, so the reverse control is void",
			stripTravel, stripPlannable)
	}
	if travel <= issue42TabStripRect.Height() {
		t.Fatalf("vertical travel %d does not exceed the tab strip height %d: "+
			"the gesture was planned inside the transverse strip",
			travel, issue42TabStripRect.Height())
	}
	if call.y2 < issue42TabStripRect.Bottom || call.y1 >= issue42OuterListRect.Bottom {
		t.Fatalf("trajectory [%d,%d] leaves the provable outer interval [%d,%d)",
			call.y2, call.y1, issue42TabStripRect.Bottom, issue42OuterListRect.Bottom)
	}

	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModeAxisFallback, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
}

// ---------------------------------------------------------------- scenario 2

// TestIssue42TabStripRightScrolledDirectly is the contrast case: the same strip
// remains the correct container for a transverse request and keeps its own
// half-tier duration.
func TestIssue42TabStripRightScrolledDirectly(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42NestedTabPage())
	h.device.scriptCaptures(page)
	start := h.startSession()
	tab := issue42ScrollNode(t, h, issue42TabStripRect)
	h.device.scriptCaptures(page, page)

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: tab.Ref},
		Direction: protocol.ScrollDirectionRight,
		Amount:    protocol.ScrollAmountHalf,
	})
	issue42RequireExecutedScroll(t, result)

	// Strip only: orthogonal row y=230 (160+140/2); width 1080 -> margin 108,
	// usable 864, half travel 259 around mid 540.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 670, y1: 230,
		x2: 411, y2: 230, durationMS: 450})
	call := swipeCalls(h)[0]
	if call.y1 != call.y2 {
		t.Fatalf("swipe is not horizontal: %+v", call)
	}
	if got := call.x1 - call.x2; got != 259 {
		t.Fatalf("horizontal travel = %d, want the strip half travel 259", got)
	}
	if call.y1 < issue42TabStripRect.Top || call.y1 >= issue42TabStripRect.Bottom {
		t.Fatalf("horizontal trajectory row %d is outside the strip [%d,%d)",
			call.y1, issue42TabStripRect.Top, issue42TabStripRect.Bottom)
	}

	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModeDirect, protocol.ScrollDirectionRight, protocol.ScrollAmountHalf)
}

// ---------------------------------------------------------------- scenario 3

// TestIssue42TransverseOnlyPageRejectsDownWithoutClaim covers the page with no
// compatible ancestor: the mismatch is a claim-free 422, and the same O still
// serves the compatible transverse request afterwards.
func TestIssue42TransverseOnlyPageRejectsDownWithoutClaim(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42TransversePanelPage())
	h.device.scriptCaptures(page)
	start := h.startSession()
	bar := issue42ScrollNode(t, h, issue42PanelRect)
	if bar.Axis != protocol.AxisX {
		t.Fatalf("panel published axis = %q, want %q", bar.Axis, protocol.AxisX)
	}

	details := issue42RequireStaticRejection(t, h, start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: bar.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	}, staticReasonAxisMismatch)
	proposalRequireDetailString(t, details, "scope", tapStaticScopeTarget)
	proposalRequireDetailString(t, details, "scope_ref", bar.Ref)
	if candidates, exists := details["candidates"]; exists {
		t.Fatalf("no other scroll container exists on this page, yet candidates = %#v",
			candidates)
	}

	// The static rejection never claimed the observation, so the very same O
	// still admits one compatible request under a new P.
	h.device.scriptCaptures(page, page)
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: bar.Ref},
		Direction: protocol.ScrollDirectionRight,
		Amount:    protocol.ScrollAmountHalf,
	})
	issue42RequireExecutedScroll(t, result)
	// Panel height 600 -> orthogonal row y=300; width 1080 -> half travel 259.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 670, y1: 300,
		x2: 411, y2: 300, durationMS: 450})
	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModeDirect, protocol.ScrollDirectionRight, protocol.ScrollAmountHalf)
}

// ---------------------------------------------------------------- scenario 4

// TestIssue42TabTitleTextDownLiftsToOuterList covers the second field-report
// path: a plain text node inside the strip must skip the transverse ancestor and
// lift to the outer compatible list.
func TestIssue42TabTitleTextDownLiftsToOuterList(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42NestedTabPage())
	h.device.scriptCaptures(page)
	start := h.startSession()
	tab := issue42ScrollNode(t, h, issue42TabStripRect)
	title := issue42TextNode(t, h, "tab-title")
	if title.Role == protocol.RoleScroll {
		t.Fatalf("tab label fixture unexpectedly published as a scroll container")
	}
	if !issue42IsDescendantOf(t, h, title, tab) {
		t.Fatalf("tab label %s is not published inside the strip %s", title.Ref, tab.Ref)
	}

	h.device.scriptCaptures(page, page)
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: title.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	})
	issue42RequireExecutedScroll(t, result)

	// Identical to the outer-list plan of scenario 1; the strip can only produce
	// a 55px vertical slide, so these coordinates pin the lift.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1854,
		x2: 540, y2: 846, durationMS: 400})
	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModeAxisFallback, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
}

// issue42IsDescendantOf reports whether the published node child sits inside the
// published subtree of ancestor.
func issue42IsDescendantOf(t *testing.T, h *harness, child, ancestor *protocol.Node) bool {
	t.Helper()
	if child == nil || ancestor == nil || child.Ref == ancestor.Ref {
		return false
	}
	published := findPublic(currentTree(t, h.engine).Root,
		func(candidate *protocol.Node) bool { return candidate.Ref == ancestor.Ref })
	if published == nil {
		t.Fatalf("published ancestor %s not found", ancestor.Ref)
	}
	return findPublic(published,
		func(candidate *protocol.Node) bool { return candidate.Ref == child.Ref }) != nil
}

// ---------------------------------------------------------------- scenario 5

// TestIssue42TabTitleWithOnlyTransverseAncestorRejected covers the ancestor
// scope: with no other scroll ancestor the mismatch names the transverse
// ancestor and its actions, and dispatches nothing.
func TestIssue42TabTitleWithOnlyTransverseAncestorRejected(t *testing.T) {
	h := newHarness(t)
	page := scrollPageStep(issue42TransversePanelPage())
	h.device.scriptCaptures(page)
	start := h.startSession()
	bar := issue42ScrollNode(t, h, issue42PanelRect)
	title := issue42TextNode(t, h, "panel-title")

	details := issue42RequireStaticRejection(t, h, start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: title.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	}, staticReasonAxisMismatch)
	proposalRequireDetailString(t, details, "scope", tapStaticScopeAncestor)
	proposalRequireDetailString(t, details, "scope_ref", bar.Ref)
	proposalRequireDetailString(t, details, "ancestor_ref", bar.Ref)

	ancestorActions := issue42DetailActions(t, details, "ancestor_actions")
	if len(ancestorActions) != 1 || ancestorActions[0] != string(protocol.ActionSwipe) {
		t.Fatalf("ancestor_actions = %#v, want exactly [swipe] for the transverse panel",
			ancestorActions)
	}
	targetActions := issue42DetailActions(t, details, "target_actions")
	if len(targetActions) != 0 {
		t.Fatalf("target_actions = %#v, want none for a plain text label", targetActions)
	}
}

// ---------------------------------------------------------------- scenario 6

// TestIssue42PointOnTabStripDownFallsBackToOuterList covers the coordinate path:
// the innermost container at the point is the transverse strip, so the point
// falls back along the hit-set parent chain to the outer list.
func TestIssue42PointOnTabStripDownFallsBackToOuterList(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42NestedTabPage())
	h.device.scriptCaptures(page)
	start := h.startSession()
	h.device.scriptCaptures(page, page)

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{X: 540, Y: 230},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	})
	issue42RequireExecutedScroll(t, result)

	// The point (540,230) is inside the strip [160,300); the executed plan is the
	// outer list interval [300,2400) computed in scenario 1.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1854,
		x2: 540, y2: 846, durationMS: 400})
	call := swipeCalls(h)[0]
	if call.x1 != call.x2 {
		t.Fatalf("point plan is not vertical: %+v", call)
	}
	if travel := call.y1 - call.y2; travel <= issue42TabStripRect.Height() {
		t.Fatalf("point plan travel %d fits inside the strip height %d",
			travel, issue42TabStripRect.Height())
	}
	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModePointFallback, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
}

// ---------------------------------------------------------------- scenario 7

// TestScrollAxisIssue42UnknownAxisFlatStripDownFallsBack covers the C guard: the
// class carries no axis evidence and the strip has no usable child layout, so
// only the nested orthogonal flat-strip geometry can reject it.
func TestScrollAxisIssue42UnknownAxisFlatStripDownFallsBack(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42UnknownAxisStripPage())
	h.device.scriptCaptures(page)
	start := h.startSession()

	outer := issue42ScrollNode(t, h, issue42UnknownOuterRect)
	strip := issue42ScrollNode(t, h, issue42UnknownStripRect)
	if outer.Axis != protocol.AxisY {
		t.Fatalf("outer list published axis = %q, want %q", outer.Axis, protocol.AxisY)
	}
	if strip.Axis != "" {
		t.Fatalf("unknown-class strip published axis = %q, want omitted/unknown", strip.Axis)
	}
	// A single child cannot witness a layout direction, so the A rule has no
	// evidence here and only the C flat-strip geometry can reject the request.
	if children := len(strip.Children); children >= 2 {
		t.Fatalf("unknown-axis strip published %d children; the fixture must stay "+
			"without child layout evidence", children)
	}

	h.device.scriptCaptures(page, page)
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: strip.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	})
	issue42RequireExecutedScroll(t, result)

	// Outer list [0,2200): the strip blocks [500,650), longest interval
	// [650,2200) -> extent 1550, margin 155, usable 1240, page travel 744 around
	// mid 1425.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1797,
		x2: 540, y2: 1053, durationMS: 400})
	call := swipeCalls(h)[0]
	if travel := call.y1 - call.y2; travel <= issue42UnknownStripRect.Height() {
		t.Fatalf("flat-strip request travel %d fits inside the strip height %d",
			travel, issue42UnknownStripRect.Height())
	}
	// Reverse control for the C guard: the strip is still self-plannable
	// (150px extent proves 61px of page travel), so only the nested flat-strip
	// geometry kept the request off it.
	stripTravel, stripPlannable := scrollTravelForExtent(
		issue42UnknownStripRect.Height(), protocol.ScrollAmountPage)
	if !stripPlannable || stripTravel <= 0 || stripTravel > issue42UnknownStripRect.Height() {
		t.Fatalf("flat strip self-plan = %d px (plannable=%v): the reverse control is void",
			stripTravel, stripPlannable)
	}
	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModeAxisFallback, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
}

// ---------------------------------------------------------------- scenario 8

// TestScrollAxisIssue42NoMisFallbackForVerticalTargets proves the gate does not
// over-fire: an already compatible vertical target keeps planning on itself.
func TestScrollAxisIssue42NoMisFallbackForVerticalTargets(t *testing.T) {
	t.Run("nested_vertical_list", func(t *testing.T) {
		h, buffer, logger := issue42NewLoggedHarness(t)
		page := scrollPageStep(issue42VerticalListInListPage())
		h.device.scriptCaptures(page)
		start := h.startSession()
		inner := issue42ScrollNode(t, h, issue42InnerListRect)
		if inner.Axis != protocol.AxisY {
			t.Fatalf("inner list published axis = %q, want %q", inner.Axis, protocol.AxisY)
		}

		h.device.scriptCaptures(page, page)
		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type:      "scroll",
			Container: &protocol.Target{UseRef: true, Ref: inner.Ref},
			Direction: protocol.ScrollDirectionDown,
			Amount:    protocol.ScrollAmountPage,
		})
		issue42RequireExecutedScroll(t, result)

		// Inner list [600,1600): extent 1000, margin 100, usable 800, page travel
		// 480 around mid 1100. A fallback to the outer list would instead produce
		// (540,2192)->(540,1808), so these coordinates pin the direct plan.
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1340,
			x2: 540, y2: 860, durationMS: 400})
		issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
			scrollModeDirect, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
	})

	t.Run("small_vertical_wheel", func(t *testing.T) {
		h, buffer, logger := issue42NewLoggedHarness(t)
		page := scrollPageStep(issue42SmallVerticalWheelPage())
		h.device.scriptCaptures(page)
		start := h.startSession()
		wheel := issue42ScrollNode(t, h, issue42SmallWheelRect)
		if wheel.Axis != protocol.AxisY {
			t.Fatalf("small wheel published axis = %q, want %q", wheel.Axis, protocol.AxisY)
		}
		// The wheel is 2:1 wider than tall and shorter than 25% of the outer
		// list, so the C flat-strip rule alone would reject it for down; only the
		// explicit vertical axis exemption keeps it plannable.
		width, height := wheel.Bounds.Width(), wheel.Bounds.Height()
		if width < 2*height || height*4 >= issue42OuterListRect.Height() {
			t.Fatalf("wheel %dx%d does not trip the nested flat-strip guard; "+
				"the explicit-axis exemption is not exercised", width, height)
		}

		h.device.scriptCaptures(page, page)
		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type:      "scroll",
			Container: &protocol.Target{UseRef: true, Ref: wheel.Ref},
			Direction: protocol.ScrollDirectionDown,
			Amount:    protocol.ScrollAmountPage,
		})
		issue42RequireExecutedScroll(t, result)

		// Explicit vertical axis exempts the geometry guard: the wheel [160,360)
		// plans itself -> extent 200, margin 24, usable 152, page travel 91 around
		// mid 260. A fallback would produce (540,1869)->(540,890).
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 306,
			x2: 540, y2: 215, durationMS: 400})
		issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
			scrollModeDirect, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
	})
}

// ---------------------------------------------------------------- scenario 9

// TestIssue42AxisMismatchOffersExecutableVerticalCandidate proves the rejection
// carries a directly usable candidate and that this candidate works on the same
// observation under a new P.
func TestIssue42AxisMismatchOffersExecutableVerticalCandidate(t *testing.T) {
	h, buffer, logger := issue42NewLoggedHarness(t)
	page := scrollPageStep(issue42TransverseAndVerticalPage())
	h.device.scriptCaptures(page)
	start := h.startSession()
	bar := issue42ScrollNode(t, h, issue42CandidateBarRect)
	list := issue42ScrollNode(t, h, issue42CandidateListRect)

	details := issue42RequireStaticRejection(t, h, start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: bar.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	}, staticReasonAxisMismatch)
	proposalRequireDetailString(t, details, "scope", tapStaticScopeTarget)
	proposalRequireDetailString(t, details, "scope_ref", bar.Ref)

	candidates := proposalCandidates(t, details)
	proposalRequireCandidateRefs(t, candidates, list.Ref)
	proposalRequireCandidateExecutable(t, candidates, list.Ref)
	proposalRequireCandidateRelation(t, candidates, list.Ref, candidateRelationPageScroll)
	if first, _ := candidates[0]["ref"].(string); first != list.Ref {
		t.Fatalf("candidates[0].ref = %q, want the executable vertical list %s",
			first, list.Ref)
	}

	// The advertised candidate is directly usable on the same observation.
	h.device.scriptCaptures(page, page)
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type:      "scroll",
		Container: &protocol.Target{UseRef: true, Ref: list.Ref},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	})
	issue42RequireExecutedScroll(t, result)
	// List [500,2400): extent 1900, margin 190, usable 1520, page travel 912
	// around mid 1450.
	proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1906,
		x2: 540, y2: 994, durationMS: 400})
	issue42RequirePlan(t, issue42PlanFields(t, buffer, logger),
		scrollModeDirect, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
}
