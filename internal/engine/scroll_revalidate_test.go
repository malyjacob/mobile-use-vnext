package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// scrollSpecWithContainerBounds returns the ordinary page with a different
// container rectangle, so the container identity still matches by content but
// the full raw identity does not.
func scrollSpecWithContainerBounds(rect device.Rect) []rawNodeSpec {
	return scrollPageSpecs(rect, device.Rect{Top: 1000})
}

func (h *harness) actAsync(p, observationID string, operation protocol.Operation) <-chan struct {
	result *protocol.Result
	err    *protocol.APIError
} {
	done := make(chan struct {
		result *protocol.Result
		err    *protocol.APIError
	}, 1)
	go func() {
		result, apiErr := h.engine.Act(context.Background(), "agent", p, &protocol.ActRequest{
			Reference: observationID,
			Operation: operation,
		})
		done <- struct {
			result *protocol.Result
			err    *protocol.APIError
		}{result: result, err: apiErr}
	}()
	return done
}

func waitAct(t *testing.T, done <-chan struct {
	result *protocol.Result
	err    *protocol.APIError
}) *protocol.Result {
	t.Helper()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("act returned transport error: %v", outcome.err)
		}
		return outcome.result
	case <-time.After(10 * time.Second):
		t.Fatal("act did not return within the test budget")
	}
	return nil
}

// TestScrollContainerAxialGeometryAllowed keeps the container content and
// changes its rectangle within the same scroll scope. The container may stretch
// or move on the requested axis; the original segment remains the plan while it
// is still proven in the fresh frame.
func TestScrollContainerAxialGeometryAllowed(t *testing.T) {
	for name, rect := range map[string]device.Rect{
		"moved":   {Left: 0, Top: 10, Right: 1080, Bottom: 2400},
		"resized": {Left: 0, Top: 0, Right: 1080, Bottom: 2390},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.device.scriptCaptures(scrollPageStep(scrollSpecWithContainerBounds(screenRect())))
			start := h.startSession()
			container := scrollContainerRef(t, h)
			h.device.scriptCaptures(scrollPageStep(scrollSpecWithContainerBounds(rect)))

			result := h.actP(t, "", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
				Direction: protocol.ScrollDirectionDown,
			})
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("same-scope axial geometry outcome = %s error %+v", result.Outcome, result.Error)
			}
			want := swipeCall{displayID: 12, x1: 540, y1: 1776, x2: 540, y2: 624, durationMS: 400}
			calls := swipeCalls(h)
			if len(calls) != 1 || calls[0] != want {
				t.Fatalf("swipe = %+v, want the proven original segment %+v", calls, want)
			}
		})
	}
}

// TestScrollContentMovesContainerStableExecuted is the core fix: the injected
// trajectory is authorized by the container, so moving leaf content no longer
// produces target_changed. The observed frame, the fresh revalidation frame and
// the post frame all show the same container with moving content (including a
// unit under the actual trajectory start). The reverse control is the removed
// leaf identity proof: proving the start-point leaf against the observed frame
// would reject this page because that leaf moved.
func TestScrollContentMovesContainerStableExecuted(t *testing.T) {
	h := newHarness(t)
	observed := scrollPageSpecs(screenRect(), device.Rect{Top: 1000})
	// A content unit covers the actual trajectory start (540, 2160).
	observed = append(observed, rawNodeSpec{
		Parent: 1, Bounds: device.Rect{Left: 0, Top: 2000, Right: 1080, Bottom: 2200},
		Class: "android.widget.TextView", Text: "under-start",
	})
	fresh := scrollPageSpecsMoved(observed, -120)
	post := scrollPageSpecsMoved(fresh, -60)
	h.device.scriptCaptures(scrollPageStep(observed))
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(scrollPageStep(fresh), scrollPageStep(post))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("moved-content scroll outcome = %s error %+v", result.Outcome, result.Error)
	}
	if calls := swipeCalls(h); len(calls) != 1 {
		t.Fatalf("swipes = %d, want exactly one", len(calls))
	}
	requireEffect(t, result, protocol.EffectObserved)
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatal("executed scroll did not publish a post observation")
	}
}

// scrollPageWithInnerScope returns the ordinary page with one extra inner
// scroll scope, so the observed and the fresh frame can disagree about the
// provable center line.
func scrollPageWithInnerScope(base []rawNodeSpec, rect device.Rect) []rawNodeSpec {
	specs := append([]rawNodeSpec(nil), base...)
	return append(specs, rawNodeSpec{Parent: 1, Bounds: rect,
		Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true})
}

// TestScrollStaticAvoidsInnerScope is the static half of the split: a published
// inner scroll scope is no longer a post-claim rejection, because the planner
// removes it from the plan. The trajectory is shorter and stays outside it.
func TestScrollStaticAvoidsInnerScope(t *testing.T) {
	for name, tc := range map[string]struct {
		inner device.Rect
		want  swipeCall
	}{
		// [0,1800): extent 1800, margin 180, usable 1440, page 864.
		"start": {inner: device.Rect{Left: 0, Top: 1800, Right: 1080, Bottom: 2400},
			want: swipeCall{displayID: 12, x1: 540, y1: 1332, x2: 540, y2: 468, durationMS: 400}},
		// [1200,2400): extent 1200, margin 120, usable 960, page 576.
		"end": {inner: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1200},
			want: swipeCall{displayID: 12, x1: 540, y1: 2088, x2: 540, y2: 1512, durationMS: 400}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			specs := scrollPageWithInnerScope(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}),
				tc.inner)
			page := scrollPageStep(specs)
			h.device.scriptCaptures(page)
			start := h.startSession()
			outer := scrollContainerRef(t, h)
			h.device.scriptCaptures(page, page)

			result := h.actP(t, "", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: outer},
				Direction: protocol.ScrollDirectionDown,
			})
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
			}
			calls := swipeCalls(h)
			if len(calls) != 1 || calls[0] != tc.want {
				t.Fatalf("swipe = %+v, want %+v", calls, tc.want)
			}
		})
	}
}

// TestScrollDynamicSegmentConflictReplans proves a conflict at the start, end,
// or middle of the original segment invalidates that segment, after which the
// fresh longest safe span is planned as one whole segment.
func TestScrollDynamicSegmentConflictReplans(t *testing.T) {
	cases := map[string]struct {
		inner device.Rect
		want  swipeCall
	}{
		"start": {inner: device.Rect{Left: 0, Top: 1700, Right: 1080, Bottom: 2400},
			want: swipeCall{displayID: 12, x1: 540, y1: 1258, x2: 540, y2: 442, durationMS: 400}},
		"end": {inner: device.Rect{Left: 0, Top: 600, Right: 1080, Bottom: 700},
			want: swipeCall{displayID: 12, x1: 540, y1: 1958, x2: 540, y2: 1142, durationMS: 400}},
		"middle": {inner: device.Rect{Left: 0, Top: 1500, Right: 1080, Bottom: 1600},
			want: swipeCall{displayID: 12, x1: 540, y1: 1110, x2: 540, y2: 390, durationMS: 400}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			observed := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
			h.device.scriptCaptures(observed)
			start := h.startSession()
			container := scrollContainerRef(t, h)
			fresh := scrollPageWithInnerScope(
				scrollPageSpecs(screenRect(), device.Rect{Top: 1000}), tc.inner)
			h.device.scriptCaptures(scrollPageStep(fresh), scrollPageStep(fresh))

			result := h.actP(t, "", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
				Direction: protocol.ScrollDirectionDown,
			})
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("blocked segment with a safe alternative outcome = %s error %+v",
					result.Outcome, result.Error)
			}
			calls := swipeCalls(h)
			if len(calls) != 1 || calls[0] != tc.want {
				t.Fatalf("swipe = %+v, want fresh whole-segment plan %+v", calls, tc.want)
			}
		})
	}
}

// TestScrollFreshChangeOutsideSegmentKeepsPlan proves a still-proven segment is
// preserved when a change only shortens another safe interval outside it. This
// is not a general no-replan rule: a failed segment is replanned in the same
// container.
func TestScrollFreshChangeOutsideSegmentKeepsPlan(t *testing.T) {
	h := newHarness(t)
	observed := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h.device.scriptCaptures(observed)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	fresh := scrollPageWithInnerScope(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}),
		device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 500})
	h.device.scriptCaptures(scrollPageStep(fresh), scrollPageStep(fresh))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	want := swipeCall{displayID: 12, x1: 540, y1: 1776, x2: 540, y2: 624, durationMS: 400}
	calls := swipeCalls(h)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls, want)
	}
}

// TestScrollPointSelectorAvoidsInnerScope proves the point selector uses the same
// static span: the resolved container is planned around the inner scope instead
// of being rejected after the claim.
func TestScrollPointSelectorAvoidsInnerScope(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1800, Right: 1080, Bottom: 2400},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
		{Parent: 3, Bounds: device.Rect{Left: 0, Top: 1900, Right: 1080, Bottom: 2100},
			Class: "android.widget.TextView", Text: "inner"},
	}
	page := scrollPageStep(specs)
	h.device.scriptCaptures(page)
	start := h.startSession()
	h.device.scriptCaptures(page, page)

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{X: 540, Y: 1100},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	want := swipeCall{displayID: 12, x1: 540, y1: 1332, x2: 540, y2: 468, durationMS: 400}
	calls := swipeCalls(h)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls, want)
	}
}

// TestScrollPublishedOverlayScopeAvoided keeps the capability boundary honest: a
// published overlay with its own scroll scope is now a planning input rather than
// an after-the-fact rejection. The injected gesture stays clear of it.
func TestScrollPublishedOverlayScopeAvoided(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
		// Root-level overlay scroll container drawn over the old start point.
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 2100, Right: 1080, Bottom: 2400},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	outer := scrollContainerRef(t, h)
	h.device.scriptCaptures(scrollPageStep(specs), scrollPageStep(specs))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: outer},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	want := swipeCall{displayID: 12, x1: 540, y1: 1554, x2: 540, y2: 546, durationMS: 400}
	calls := swipeCalls(h)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls, want)
	}
}

// TestScrollOuterScrollAncestorAllowed selects the inner of two nested scroll
// containers; the outer wrapper is visible at the same point but is an
// ancestor of the selected container, so the trajectory is still authorized.
func TestScrollOuterScrollAncestorAllowed(t *testing.T) {
	h := newHarness(t)
	inner := device.Rect{Left: 0, Top: 400, Right: 1080, Bottom: 2000}
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: inner, Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 700},
			Class: "android.widget.TextView", Text: "alpha"},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "bravo"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	selected := findPublic(currentTree(t, h.engine).Root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleScroll && node.Bounds != nil &&
			node.Bounds.Top == inner.Top && node.Bounds.Bottom == inner.Bottom
	})
	if selected == nil {
		t.Fatal("inner container missing")
	}
	h.device.scriptCaptures(scrollPageStep(specs))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: selected.Ref},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("nested scroll outcome = %s error %+v", result.Outcome, result.Error)
	}
	calls := swipeCalls(h)
	if len(calls) != 1 {
		t.Fatalf("swipes = %d, want one", len(calls))
	}
	want := swipeCall{displayID: 12, x1: 540, y1: 1584, x2: 540, y2: 816, durationMS: 400}
	if calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls[0], want)
	}
}

// ---------------------------------------------------------------- raw swipe

func TestRawSwipeDurationPassedToDevice(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(defaultCaptureStep)
	start := h.startSession()
	observationID := start.Observation.ID

	cases := []struct {
		duration int
		want     int
	}{
		{duration: 0, want: protocol.DefaultSwipeDurationMS},
		{duration: protocol.MinSwipeDurationMS, want: protocol.MinSwipeDurationMS},
		{duration: protocol.MaxSwipeDurationMS, want: protocol.MaxSwipeDurationMS},
		{duration: 300, want: 300},
	}
	for _, testCase := range cases {
		result := h.actP(t, "", observationID, protocol.Operation{
			Type: "swipe",
			From: &protocol.Point{X: 500, Y: 150}, To: &protocol.Point{X: 500, Y: 1000},
			DurationMS: testCase.duration,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("swipe duration %d outcome = %s error %+v", testCase.duration, result.Outcome, result.Error)
		}
		if result.Observation == nil {
			t.Fatalf("swipe duration %d published no post observation", testCase.duration)
		}
		observationID = result.Observation.ID
		requireNoEffect(t, result)
	}
	calls := swipeCalls(h)
	if len(calls) != len(cases) {
		t.Fatalf("swipes = %d, want %d", len(calls), len(cases))
	}
	for index, testCase := range cases {
		if calls[index].durationMS != testCase.want {
			t.Fatalf("swipe %d duration = %d, want %d", index, calls[index].durationMS, testCase.want)
		}
	}
}

func TestRawSwipeDuplicatePDispatchesOnce(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(defaultCaptureStep)
	start := h.startSession()
	operation := protocol.Operation{
		Type: "swipe", From: &protocol.Point{X: 500, Y: 150}, To: &protocol.Point{X: 500, Y: 1000},
	}
	first := h.actP(t, "Pswipe", start.Observation.ID, operation)
	if first.Outcome != OutcomeExecuted {
		t.Fatalf("first swipe = %s error %+v", first.Outcome, first.Error)
	}
	second := h.actP(t, "Pswipe", start.Observation.ID, operation)
	if second.OperationState != OperationFinished || second.Outcome != OutcomeExecuted {
		t.Fatalf("duplicate P = %+v", second)
	}
	if got := swipeCount(h); got != 1 {
		t.Fatalf("swipes = %d, want exactly one", got)
	}
}

// TestRawSwipeStillRejectsMovedStartLeaf keeps the raw-swipe criterion: only
// the raw branch keeps the complete start-node identity proof.
func TestRawSwipeStillRejectsMovedStartLeaf(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(defaultCaptureStep)
	start := h.startSession()
	// Both revalidation frames must carry the move. A raw swipe proves its start
	// point through the coordinate proof, which disables the Tier 5 translation
	// recovery, so the same-band move still rejects.
	h.device.scriptCaptures(movedButtonCaptureStep())

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "swipe", From: &protocol.Point{X: 500, Y: 150}, To: &protocol.Point{X: 500, Y: 1000},
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireNoEffect(t, result)
	if result.Observation == nil {
		t.Fatal("rejected raw swipe did not deliver the fresh frame")
	}
	if got := swipeCount(h); got != 0 {
		t.Fatalf("swipes = %d, want none", got)
	}
}

func TestRawSwipeAmbiguousMessageIsExecutable(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(overlayCaptureStep())
	start := h.startSession()

	tap := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, tap, protocol.CodeAmbiguousPoint)
	if tap.Error == nil || !strings.Contains(tap.Error.Message, "ref") {
		t.Fatalf("tap ambiguous hint changed: %+v", tap.Error)
	}

	h.device.scriptCaptures(overlayCaptureStep(), overlayCaptureStep())
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "swipe", From: &protocol.Point{X: 500, Y: 150}, To: &protocol.Point{X: 500, Y: 1000},
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("raw swipe on same-depth overlap outcome = %s error %+v, want executed",
			result.Outcome, result.Error)
	}
	if got := swipeCount(h); got != 1 {
		t.Fatalf("raw swipe dispatched %d gestures, want 1", got)
	}
}

func TestRawSwipeRejectsRefTargetBeforeDispatch(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(defaultCaptureStep)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button missing")
	}
	captures := h.device.captureCount()
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "swipe", From: &protocol.Point{X: 500, Y: 150}, To: &protocol.Point{X: 500, Y: 1000},
		Target: &protocol.Target{UseRef: true, Ref: button.Ref},
	})
	requireNotExecuted(t, result, protocol.CodeInvalidRequest)
	if h.device.captureCount() != captures || swipeCount(h) != 0 {
		t.Fatal("swipe with ref target performed device work")
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation = %q, want ready", status)
	}
}

// ---------------------------------------------------------------- P facts

// TestOperationViewScrollEffectProjection is the unit-level contract of the
// bounded operation fact: a committed effect is copied, a retained finished
// scroll without one projects unknown, and nothing else invents an effect.
func TestOperationViewScrollEffectProjection(t *testing.T) {
	h := newHarnessWithClock(t, false)
	committed := &protocol.Effect{State: protocol.EffectObserved}
	cases := []struct {
		name   string
		op     *operationState
		effect *protocol.Effect
	}{
		{"running_scroll", &operationState{ID: "P1", Agent: "agent", Kind: "act", State: OperationRunning,
			Phase: "dispatch", ActAction: "scroll"}, nil},
		{"finished_executed_scroll", &operationState{ID: "P2", Agent: "agent", Kind: "act",
			State: OperationFinished, Outcome: OutcomeExecuted, Phase: "finished", ActAction: "scroll"},
			&protocol.Effect{State: protocol.EffectUnknown}},
		{"finished_uncertain_scroll", &operationState{ID: "P3", Agent: "agent", Kind: "act",
			State: OperationFinished, Outcome: OutcomeUncertain, Phase: "dispatch", ActAction: "scroll"},
			&protocol.Effect{State: protocol.EffectUnknown}},
		{"finished_not_executed_scroll", &operationState{ID: "P4", Agent: "agent", Kind: "act",
			State: OperationFinished, Outcome: OutcomeNotExecuted, Phase: "dispatch", ActAction: "scroll"}, nil},
		{"finished_executed_tap", &operationState{ID: "P5", Agent: "agent", Kind: "act",
			State: OperationFinished, Outcome: OutcomeExecuted, Phase: "finished"}, nil},
		{"committed_effect", &operationState{ID: "P6", Agent: "agent", Kind: "act",
			State: OperationFinished, Outcome: OutcomeExecuted, Phase: "finished",
			ActAction: "scroll", Effect: committed}, committed},
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	for _, testCase := range cases {
		h.engine.operations[testCase.op.ID] = testCase.op
		view := h.engine.operationViewLocked(testCase.op)
		switch {
		case testCase.effect == nil && view.Effect != nil:
			t.Fatalf("%s projected %+v, want omitted", testCase.name, view.Effect)
		case testCase.effect != nil && (view.Effect == nil || view.Effect.State != testCase.effect.State):
			t.Fatalf("%s projected %+v, want %s", testCase.name, view.Effect, testCase.effect.State)
		}
		if view.Observation != nil {
			t.Fatalf("%s attached an observation to a P view", testCase.name)
		}
	}
}

func TestScrollUncertainReceiptProjectsUnknown(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.mu.Lock()
	unknown := device.DispatchUnknown
	h.device.swipeReceipt = &unknown
	h.device.mu.Unlock()
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))

	result := h.actP(t, "Punknown", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("outcome = %s, want uncertain", result.Outcome)
	}
	requireEffect(t, result, protocol.EffectUnknown)
	status, apiErr := h.engine.Status("agent", "Punknown")
	if apiErr != nil {
		t.Fatalf("status(P): %v", apiErr)
	}
	requireEffect(t, status, protocol.EffectUnknown)
	if status.Observation != nil {
		t.Fatal("status(P) attached an observation")
	}
	duplicate := h.actP(t, "Punknown", start.Observation.ID, protocol.Operation{Type: "tap",
		Target: &protocol.Target{X: 1, Y: 2}})
	requireEffect(t, duplicate, protocol.EffectUnknown)
}

func TestScrollPostObservationFailedProjectsUnknown(t *testing.T) {
	h := newHarness(t)
	page := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h.device.scriptCaptures(page)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(page, failingCapture())

	result := h.actP(t, "Ppostfail", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s, want executed", result.Outcome)
	}
	if result.Recovery == nil || result.Recovery.Cause != protocol.RecoveryCausePostObservationFailed {
		t.Fatalf("recovery = %+v, want post_observation_failed", result.Recovery)
	}
	requireEffect(t, result, protocol.EffectUnknown)
	status, apiErr := h.engine.Status("agent", "Ppostfail")
	if apiErr != nil {
		t.Fatalf("status(P): %v", apiErr)
	}
	requireEffect(t, status, protocol.EffectUnknown)
}

func TestScrollSuccessEffectReplaysOnStatusAndDuplicate(t *testing.T) {
	h := newHarness(t)
	page := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h.device.scriptCaptures(page)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(page, page)

	result := h.actP(t, "Psuccess", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	requireEffect(t, result, protocol.EffectNotObserved)
	status, apiErr := h.engine.Status("agent", "Psuccess")
	if apiErr != nil {
		t.Fatalf("status(P): %v", apiErr)
	}
	if status.Outcome != OutcomeExecuted || status.Observation != nil {
		t.Fatalf("status(P) = %+v", status)
	}
	requireEffect(t, status, protocol.EffectNotObserved)
	duplicate := h.actP(t, "Psuccess", start.Observation.ID, protocol.Operation{Type: "tap",
		Target: &protocol.Target{X: 1, Y: 2}})
	if duplicate.Outcome != OutcomeExecuted || duplicate.Observation != nil {
		t.Fatalf("duplicate P = %+v", duplicate)
	}
	requireEffect(t, duplicate, protocol.EffectNotObserved)
	if got := swipeCount(h); got != 1 {
		t.Fatalf("swipes = %d, want one", got)
	}
}

func TestScrollClosedAndEvictedOperationFacts(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.engine.mu.Lock()
	now := time.Now()
	h.engine.closed["Pknown"] = now
	h.engine.closedKnown["Pknown"] = true
	h.engine.mu.Unlock()
	status, apiErr := h.engine.Status("agent", "Pknown")
	if apiErr != nil {
		t.Fatalf("known closed status: %v", apiErr)
	}
	if status.Outcome != OutcomeNotExecuted || status.Phase != "closed_before_admission" {
		t.Fatalf("known closed status = %+v", status)
	}
	requireNoEffect(t, status)
	_, closedResult, registerErr := h.engine.registerOperation("Pknown", "agent", "act")
	if registerErr != nil || closedResult == nil {
		t.Fatalf("closed registration = %+v err=%v", closedResult, registerErr)
	}
	requireNoEffect(t, closedResult)

	h.engine.mu.Lock()
	h.engine.closed["Punknown"] = now
	h.engine.closedKnown["Punknown"] = false
	h.engine.mu.Unlock()
	if _, unknownErr := h.engine.Status("agent", "Punknown"); unknownErr == nil ||
		unknownErr.Code != protocol.CodeOperationUnknown {
		t.Fatalf("unknown closed status = %+v", unknownErr)
	}

	// A fully evicted P is admitted as a new operation: no old action or
	// effect may be restored from it.
	h2 := newHarness(t)
	h2.engine.cfg.MaxOperations = 2
	page := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h2.device.scriptCaptures(page)
	start := h2.startSession()
	container := scrollContainerRef(t, h2)
	h2.device.scriptCaptures(page, page)
	first := h2.actP(t, "Pevict", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	requireEffect(t, first, protocol.EffectNotObserved)

	h2.device.scriptCaptures(page)
	second := h2.actP(t, "Pfiller", first.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 10, Y: 500},
	})
	if second.Outcome != OutcomeExecuted {
		t.Fatalf("filler tap = %s error %+v", second.Outcome, second.Error)
	}
	h2.engine.mu.Lock()
	_, stillRetained := h2.engine.operations["Pevict"]
	h2.engine.mu.Unlock()
	if stillRetained {
		t.Fatal("finished P was not evicted by the bounded capacity")
	}
	replayed := h2.actP(t, "Pevict", second.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 10, Y: 500},
	})
	if replayed.Outcome != OutcomeExecuted {
		t.Fatalf("re-registered P outcome = %s error %+v", replayed.Outcome, replayed.Error)
	}
	requireNoEffect(t, replayed)
}

// ---------------------------------------------------------------- STOP

func TestScrollStopDuringDispatchKeepsUncertainFacts(t *testing.T) {
	h := newHarness(t)
	page := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h.device.scriptCaptures(page)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(page, page)
	h.device.mu.Lock()
	h.device.swipeHook = func() {
		h.sync(nil, &protocol.AppStopPending{Seq: 1, Reason: "user_stop"})
	}
	h.device.mu.Unlock()

	result := h.actP(t, "Pstop", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("stop-during-dispatch outcome = %s, want uncertain", result.Outcome)
	}
	requireEffect(t, result, protocol.EffectUnknown)
	if result.Observation != nil {
		t.Fatal("a revoked dispatch published an observation")
	}
	status, apiErr := h.engine.Status("agent", "Pstop")
	if apiErr != nil {
		t.Fatalf("status(P): %v", apiErr)
	}
	requireEffect(t, status, protocol.EffectUnknown)
}

func TestScrollStopDuringSettleKeepsExecutedFacts(t *testing.T) {
	h := newHarness(t)
	page := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h.device.scriptCaptures(page)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(page)
	// Make the post-settle quiet window long enough to interleave STOP
	// deterministically inside it.
	h.engine.cfg.Settle.Act.MinDwell = 1500 * time.Millisecond

	done := h.actAsync("Pstops", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	time.Sleep(250 * time.Millisecond)
	h.sync(nil, &protocol.AppStopPending{Seq: 1, Reason: "user_stop"})
	result := waitAct(t, done)
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("stop-during-settle outcome = %s, want executed", result.Outcome)
	}
	if result.Recovery == nil || result.Recovery.Cause != protocol.RecoveryCauseExecutedRevoked {
		t.Fatalf("recovery = %+v, want executed_revoked", result.Recovery)
	}
	requireEffect(t, result, protocol.EffectUnknown)
	if result.Observation != nil {
		t.Fatal("a revoked settle published a late observation")
	}
	status, apiErr := h.engine.Status("agent", "Pstops")
	if apiErr != nil {
		t.Fatalf("status(P): %v", apiErr)
	}
	requireEffect(t, status, protocol.EffectUnknown)
	// The claimed observation is retired, never returned to ready.
	if got := observationStatus(h, start.Observation.ID); got != ObservationRetired {
		t.Fatalf("observation status = %q, want retired", got)
	}
}
