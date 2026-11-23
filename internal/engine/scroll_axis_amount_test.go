package engine

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- amount tiers

// TestScrollAmountTravelTierThresholds pins the four travel tiers on both sides
// of their exact minimum extent. The page/half rows keep the pre-existing 128px
// and 208px thresholds; the step and full rows are the new boundaries.
func TestScrollAmountTravelTierThresholds(t *testing.T) {
	cases := []struct {
		name   string
		extent int
		amount string
		ok     bool
		travel int
	}{
		// step: margin 26, usable 265-52=213 -> 31 (< 32), usable 214 -> 32.
		{"step_minus_one", 265, protocol.ScrollAmountStep, false, 0},
		{"step_minimum", 266, protocol.ScrollAmountStep, true, 32},
		// full: margin 24, usable 56 -> 47 (< 48), usable 57 -> 48.
		{"full_minus_one", 104, protocol.ScrollAmountFull, false, 0},
		{"full_minimum", 105, protocol.ScrollAmountFull, true, 48},
		// page: margin 24, usable 79 -> 47, usable 80 -> 48.
		{"page_minus_one", 127, protocol.ScrollAmountPage, false, 0},
		{"page_minimum", 128, protocol.ScrollAmountPage, true, 48},
		// half: margin 24, usable 159 -> 47, usable 160 -> 48.
		{"half_minus_one", 207, protocol.ScrollAmountHalf, false, 0},
		{"half_minimum", 208, protocol.ScrollAmountHalf, true, 48},
		// One large extent separates the four percentages: margin 100, usable 800.
		{"large_step", 1000, protocol.ScrollAmountStep, true, 120},
		{"large_half", 1000, protocol.ScrollAmountHalf, true, 240},
		{"large_page", 1000, protocol.ScrollAmountPage, true, 480},
		{"large_full", 1000, protocol.ScrollAmountFull, true, 680},
		// The empty amount is the decoder default and behaves exactly like page.
		{"empty_is_page", 1000, "", true, 480},
		{"unknown_amount", 1000, "wide", false, 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			travel, ok := scrollTravelForExtent(testCase.extent, testCase.amount)
			if ok != testCase.ok {
				t.Fatalf("travel(%d, %q) ok = %v, want %v",
					testCase.extent, testCase.amount, ok, testCase.ok)
			}
			if ok && travel != testCase.travel {
				t.Fatalf("travel(%d, %q) = %d, want %d",
					testCase.extent, testCase.amount, travel, testCase.travel)
			}
		})
	}
	if got, ok := normalizeScrollAmount(""); !ok || got != protocol.ScrollAmountPage {
		t.Fatalf("normalizeScrollAmount(\"\") = %q, %v, want page, true", got, ok)
	}
	for _, amount := range []string{"step", "half", "page", "full"} {
		if got, ok := normalizeScrollAmount(amount); !ok || got != amount {
			t.Fatalf("normalizeScrollAmount(%q) = %q, %v, want passthrough", amount, got, ok)
		}
	}
}

// TestScrollAmountTierDurationsAndTrajectories pins the amount-to-duration map
// and the amount-driven travel on one full-screen container: margin 240, usable
// 1920, travel step 288 / half 576 / page 1152 / full 1632 around mid 1200.
func TestScrollAmountTierDurationsAndTrajectories(t *testing.T) {
	page := scrollPageSpecs(screenRect(), device.Rect{Top: 1000})
	cases := []struct {
		name       string
		amount     string
		start, end int
		durationMS int
	}{
		{"step", protocol.ScrollAmountStep, 1344, 1056, 500},
		{"half", protocol.ScrollAmountHalf, 1488, 912, 450},
		{"page", protocol.ScrollAmountPage, 1776, 624, 400},
		{"full", protocol.ScrollAmountFull, 2016, 384, 300},
		// The omitted amount is the page tier.
		{"default", "", 1776, 624, 400},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			h.device.scriptCaptures(scrollPageStep(page))
			start := h.startSession()
			container := scrollContainerRef(t, h)
			h.device.scriptCaptures(scrollPageStep(page), scrollPageStep(page))

			result := h.actP(t, "", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
				Direction: protocol.ScrollDirectionDown, Amount: testCase.amount,
			})
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
			}
			want := swipeCall{displayID: 12, x1: 540, y1: testCase.start,
				x2: 540, y2: testCase.end, durationMS: testCase.durationMS}
			calls := swipeCalls(h)
			if len(calls) != 1 || calls[0] != want {
				t.Fatalf("swipe = %+v, want exactly %+v", calls, want)
			}
		})
	}
}

// scrollActWire builds one act body with a raw amount fragment.
func scrollActWire(amountFragment string) []byte {
	body := `{"reference":"O123","operation":{"type":"scroll",` +
		`"container":{"x":540,"y":1100},"direction":"down"` + amountFragment + `}}`
	return []byte(body)
}

// TestScrollAmountWireAdmission pins the closed wire enum: a string that is not
// one of the four tiers, a number, a null and a boolean are all rejected at the
// strict decoder entry as 422 invalid_request, while every tier is admitted and
// an omitted amount decodes to page.
func TestScrollAmountWireAdmission(t *testing.T) {
	for _, amount := range []string{"step", "half", "page", "full"} {
		request, apiErr := protocol.DecodeAct(scrollActWire(`,"amount":"` + amount + `"`))
		if apiErr != nil {
			t.Fatalf("amount %q rejected: %v", amount, apiErr)
		}
		if request.Operation.Amount != amount {
			t.Fatalf("amount %q decoded to %q", amount, request.Operation.Amount)
		}
	}
	request, apiErr := protocol.DecodeAct(scrollActWire(""))
	if apiErr != nil {
		t.Fatalf("omitted amount rejected: %v", apiErr)
	}
	if request.Operation.Amount != protocol.ScrollAmountPage {
		t.Fatalf("omitted amount = %q, want page", request.Operation.Amount)
	}
	for name, fragment := range map[string]string{
		"unknown_word": `,"amount":"wide"`,
		"percentage":   `,"amount":"60%"`,
		"number":       `,"amount":15`,
		"null":         `,"amount":null`,
		"boolean":      `,"amount":true`,
	} {
		t.Run(name, func(t *testing.T) {
			_, apiErr := protocol.DecodeAct(scrollActWire(fragment))
			if apiErr == nil {
				t.Fatalf("amount fragment %q was admitted", fragment)
			}
			if apiErr.Code != protocol.CodeInvalidRequest {
				t.Fatalf("amount fragment %q code = %q, want %q",
					fragment, apiErr.Code, protocol.CodeInvalidRequest)
			}
			if status := protocol.Status(apiErr.Code); status != 422 {
				t.Fatalf("amount fragment %q status = %d, want 422", fragment, status)
			}
		})
	}
}

// ---------------------------------------------------------------- axis fixtures

// A synthetic page of the field report: a vertical list container with one short
// inner scroll strip at [300,450). The strip class decides whether the public
// axis is explicit ("android.widget.HorizontalScrollView" -> x,
// "android.widget.ScrollView" -> y) or unknown ("android.view.View").
var (
	scrollAxisOuterRect = device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2200}
	scrollAxisStripRect = device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 450}
)

func scrollAxisStripPage(stripClass string) []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: scrollAxisOuterRect, Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: scrollAxisStripRect, Class: stripClass, Scrollable: true},
	}
}

// scrollAxisTestInputs builds the pure projection and public view the axis
// predicate consumes, without an engine or device.
func scrollAxisTestInputs(t *testing.T, specs []rawNodeSpec) (*observationState, *tapPublicView) {
	t.Helper()
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	capture := buildCapture(display, specs)
	projection := scrollProjection(t, capture)
	view, apiErr := buildTapPublicView(projection.Tree, projection.Capture, projection.Source)
	if apiErr != nil {
		t.Fatalf("build public view: %v", apiErr)
	}
	return &observationState{Projection: projection, Screen: display,
		DisplayID: display.ID}, view
}

func scrollAxisTestNode(t *testing.T, view *tapPublicView, rect device.Rect) *protocol.Node {
	t.Helper()
	node := findPublic(view.tree.Root, func(candidate *protocol.Node) bool {
		return candidate.Role == protocol.RoleScroll && candidate.Bounds != nil &&
			rectFromBounds(candidate.Bounds) == rect
	})
	if node == nil {
		t.Fatalf("no published scroll container at %+v", rect)
	}
	return node
}

func requireScrollPlanMode(t *testing.T, buffer *strings.Builder, want string) {
	t.Helper()
	recorded := buffer.String()
	if !strings.Contains(recorded, "scroll.plan") {
		t.Fatalf("no scroll.plan event recorded:\n%s", recorded)
	}
	if !strings.Contains(recorded, `mode="`+want+`"`) {
		t.Fatalf("scroll.plan lacks mode=%q:\n%s", want, recorded)
	}
}

// scrollAxisRequireStaticRejection asserts one claim-free static 422 with the
// given reason, no device work and an observation that stays ready.
func scrollAxisRequireStaticRejection(t *testing.T, h *harness, observationID string,
	operation protocol.Operation, wantReason string) map[string]any {
	t.Helper()
	captures, swipes := h.device.captureCount(), swipeCount(h)
	result := h.actP(t, "", observationID, operation)
	details := proposalRequireStaticReason(t, result, protocol.CodeNodeUnsupported, wantReason)
	requireNoEffect(t, result)
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

// ---------------------------------------------------------------- C guard

// TestScrollAxisFlatStripGuard covers the nested orthogonal flat-strip guard on
// an unknown axis: a 1080x150 strip inside a 1080x2200 vertical list is
// incompatible for down and compatible for right. The vertical request falls
// back to the parent list, the horizontal request uses the strip itself, and the
// coordinate request falls back along the hit-set parent chain.
func TestScrollAxisFlatStripGuard(t *testing.T) {
	// Predicate-level reverse control: removing the C guard turns both of these
	// assertions red while the right/down contrast stays visible.
	t.Run("predicate", func(t *testing.T) {
		obs, view := scrollAxisTestInputs(t, scrollAxisStripPage("android.view.View"))
		strip := scrollAxisTestNode(t, view, scrollAxisStripRect)
		if strip.Axis != "" {
			t.Fatalf("strip axis = %q, want unknown", strip.Axis)
		}
		if scrollContainerAxisCompatible(obs, view, strip, protocol.ScrollDirectionDown) {
			t.Fatal("nested flat strip must be axis-incompatible for down")
		}
		if !scrollContainerAxisCompatible(obs, view, strip, protocol.ScrollDirectionRight) {
			t.Fatal("nested flat strip must stay axis-compatible for right")
		}
		// The outer vertical list is explicitly vertical and is never rejected by
		// its own flat ratio (C only inspects the candidate, not its ancestors).
		outer := scrollAxisTestNode(t, view, scrollAxisOuterRect)
		if outer.Axis != protocol.AxisY {
			t.Fatalf("outer axis = %q, want y", outer.Axis)
		}
		if !scrollContainerAxisCompatible(obs, view, outer, protocol.ScrollDirectionDown) {
			t.Fatal("outer vertical list must be axis-compatible for down")
		}
		if scrollContainerAxisCompatible(obs, view, outer, protocol.ScrollDirectionRight) {
			t.Fatal("explicit vertical list must be axis-incompatible for right")
		}
	})

	t.Run("ref_down_falls_back_to_parent", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		page := scrollPageStep(scrollAxisStripPage("android.view.View"))
		h.device.scriptCaptures(page)
		start := h.startSession()
		strip := scrollContainerMatching(t, h, scrollAxisStripRect)
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: strip},
			Direction: protocol.ScrollDirectionDown,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		// Without the C guard the strip plans itself: extent 150, page 61, so
		// (540,406)->(540,345). The parent plan is the only passing value.
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1745,
			x2: 540, y2: 905, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModeAxisFallback)
	})

	t.Run("ref_right_uses_strip_itself", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		page := scrollPageStep(scrollAxisStripPage("android.view.View"))
		h.device.scriptCaptures(page)
		start := h.startSession()
		strip := scrollContainerMatching(t, h, scrollAxisStripRect)
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: strip},
			Direction: protocol.ScrollDirectionRight,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 799, y1: 375,
			x2: 281, y2: 375, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModeDirect)
	})

	t.Run("point_down_falls_back_to_parent", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		page := scrollPageStep(scrollAxisStripPage("android.view.View"))
		h.device.scriptCaptures(page)
		start := h.startSession()
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{X: 540, Y: 375},
			Direction: protocol.ScrollDirectionDown,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1745,
			x2: 540, y2: 905, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModePointFallback)
	})

	t.Run("no_compatible_parent_rejected", func(t *testing.T) {
		// The unknown-axis strip sits inside an explicitly horizontal panel (so C
		// rejects it) and the only vertical list is a page sibling, not an
		// ancestor, so no fallback exists.
		h := newHarness(t)
		panelRect := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 600}
		siblingRect := device.Rect{Left: 0, Top: 700, Right: 1080, Bottom: 2200}
		page := scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: panelRect, Class: "android.widget.HorizontalScrollView",
				Scrollable: true},
			{Parent: 1, Bounds: scrollAxisStripRect, Class: "android.view.View",
				Scrollable: true},
			{Parent: 0, Bounds: siblingRect, Class: "android.widget.ScrollView",
				Scrollable: true},
		})
		h.device.scriptCaptures(page)
		start := h.startSession()
		strip := scrollContainerMatching(t, h, scrollAxisStripRect)
		sibling := scrollContainerMatching(t, h, siblingRect)

		details := scrollAxisRequireStaticRejection(t, h, start.Observation.ID,
			protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: strip},
				Direction: protocol.ScrollDirectionDown,
			}, staticReasonAxisMismatch)
		proposalRequireDetailString(t, details, "scope", tapStaticScopeTarget)
		proposalRequireDetailString(t, details, "scope_ref", strip)
		candidates := proposalCandidates(t, details)
		proposalRequireCandidateRefs(t, candidates, sibling)
		proposalRequireCandidateExecutable(t, candidates, sibling)
	})
}

// ---------------------------------------------------------------- A guard

// TestScrollAxisExplicitVerticalExemptsFlatStripGuard covers the exemption rule:
// an explicitly vertical small-height ScrollView stays usable for down, while an
// explicitly horizontal strip conflicts with down and matches right.
func TestScrollAxisExplicitVerticalExemptsFlatStripGuard(t *testing.T) {
	t.Run("predicate", func(t *testing.T) {
		obs, view := scrollAxisTestInputs(t, scrollAxisStripPage("android.widget.ScrollView"))
		wheel := scrollAxisTestNode(t, view, scrollAxisStripRect)
		if wheel.Axis != protocol.AxisY {
			t.Fatalf("fixture wheel axis = %q, want y", wheel.Axis)
		}
		// The flat-strip geometry would reject this node; the explicit axis wins.
		if !scrollContainerAxisCompatible(obs, view, wheel, protocol.ScrollDirectionDown) {
			t.Fatal("explicit vertical axis must exempt the flat-strip guard for down")
		}
		if scrollContainerAxisCompatible(obs, view, wheel, protocol.ScrollDirectionRight) {
			t.Fatal("explicit vertical axis must conflict with right")
		}
	})

	t.Run("small_wheel_plans_itself", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		page := scrollPageStep(scrollAxisStripPage("android.widget.ScrollView"))
		h.device.scriptCaptures(page)
		start := h.startSession()
		wheel := scrollContainerMatching(t, h, scrollAxisStripRect)
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: wheel},
			Direction: protocol.ScrollDirectionDown,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		// The wheel plans itself: extent 150, page 61. Falling back to the parent
		// would instead produce (540,1745)->(540,905).
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 406,
			x2: 540, y2: 345, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModeDirect)
	})

	t.Run("projected_horizontal_bar", func(t *testing.T) {
		obs, view := scrollAxisTestInputs(t,
			scrollAxisStripPage("android.widget.HorizontalScrollView"))
		bar := scrollAxisTestNode(t, view, scrollAxisStripRect)
		if bar.Axis != protocol.AxisX {
			t.Fatalf("projected bar axis = %q, want x", bar.Axis)
		}
		if scrollContainerAxisCompatible(obs, view, bar, protocol.ScrollDirectionDown) {
			t.Fatal("projected horizontal bar must conflict with down")
		}
		if !scrollContainerAxisCompatible(obs, view, bar, protocol.ScrollDirectionRight) {
			t.Fatal("projected horizontal bar must match right")
		}
	})

	t.Run("projected_bar_falls_back", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		page := scrollPageStep(scrollAxisStripPage("android.widget.HorizontalScrollView"))
		h.device.scriptCaptures(page)
		start := h.startSession()
		bar := scrollContainerMatching(t, h, scrollAxisStripRect)
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: bar},
			Direction: protocol.ScrollDirectionDown,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1745,
			x2: 540, y2: 905, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModeAxisFallback)
	})

	t.Run("projected_bar_uses_itself_for_right", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		page := scrollPageStep(scrollAxisStripPage("android.widget.HorizontalScrollView"))
		h.device.scriptCaptures(page)
		start := h.startSession()
		bar := scrollContainerMatching(t, h, scrollAxisStripRect)
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: bar},
			Direction: protocol.ScrollDirectionRight,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 799, y1: 375,
			x2: 281, y2: 375, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModeDirect)
	})
}

// ---------------------------------------------------------------- point axis

// TestScrollPointAxisCompatibleUniqueSelection covers the competing innermost
// containers: the explicitly vertical one is the only axis-compatible executable
// candidate for down, so the existing ambiguity is resolved to it; when every
// innermost candidate conflicts with the axis, the point is rejected statically
// with the page's executable containers attached.
func TestScrollPointAxisCompatibleUniqueSelection(t *testing.T) {
	containerABounds := device.Rect{Left: 0, Top: 700, Right: 1080, Bottom: 1600}
	containerBBounds := device.Rect{Left: 300, Top: 900, Right: 780, Bottom: 2100}
	page := func(classA, classB string) captureStep {
		return scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView",
				Scrollable: true},
			{Parent: 1, Bounds: containerABounds, Class: classA, Scrollable: true},
			{Parent: 1, Bounds: containerBBounds, Class: classB, Scrollable: true},
		})
	}

	t.Run("unique_axis_compatible", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		step := page("android.widget.ScrollView", "android.widget.HorizontalScrollView")
		h.device.scriptCaptures(step)
		start := h.startSession()
		h.device.scriptCaptures(step, step)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{X: 540, Y: 1100},
			Direction: protocol.ScrollDirectionDown,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		// Only the vertical container is planned: its center line is carved by the
		// horizontal strip, leaving [700,900) and page travel 91 around mid 800.
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 846,
			x2: 540, y2: 755, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModePointDisambiguated)
	})

	t.Run("all_incompatible_rejected", func(t *testing.T) {
		h := newHarness(t)
		step := page("android.widget.HorizontalScrollView",
			"android.widget.HorizontalScrollView")
		h.device.scriptCaptures(step)
		start := h.startSession()
		pageNode := scrollContainerMatching(t, h, screenRect())

		details := scrollAxisRequireStaticRejection(t, h, start.Observation.ID,
			protocol.Operation{
				Type: "scroll", Container: &protocol.Target{X: 540, Y: 1100},
				Direction: protocol.ScrollDirectionDown,
			}, staticReasonAxisMismatch)
		proposalRequireDetailString(t, details, "scope", tapStaticScopePoint)
		if value, exists := details["scope_ref"]; exists {
			t.Fatalf("point-scope axis_mismatch carries scope_ref = %#v", value)
		}
		candidates := proposalCandidates(t, details)
		proposalRequireCandidateRefs(t, candidates, pageNode)
		proposalRequireCandidateExecutable(t, candidates, pageNode)
	})
}

// ---------------------------------------------------------------- inner fallback

// TestScrollAxisInnerFallbackFilter covers step 2 of the reference resolver: an
// axis-incompatible target delegates to the one compatible inner container, and
// an axis-incompatible inner container is never delegated to.
func TestScrollAxisInnerFallbackFilter(t *testing.T) {
	pagerRect := device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}

	t.Run("incompatible_target_picks_compatible_inner", func(t *testing.T) {
		var buffer strings.Builder
		logger := eventlog.New(&buffer, "instance-test")
		h := newHarnessWithLogger(t, true, logger)
		innerRect := device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 1800}
		page := scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: pagerRect, Class: "android.widget.HorizontalScrollView",
				Scrollable: true},
			// The inner list has no class axis evidence and is not coextensive with
			// the pager (1500 of 2000 rows), so it is a real fallback scope and stays
			// compatible for down.
			{Parent: 1, Bounds: innerRect, Class: "androidx.recyclerview.widget.RecyclerView",
				Scrollable: true},
		})
		h.device.scriptCaptures(page)
		start := h.startSession()
		pager := scrollContainerMatching(t, h, pagerRect)
		h.device.scriptCaptures(page, page)

		result := h.actP(t, "", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: pager},
			Direction: protocol.ScrollDirectionDown,
		})
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
		}
		logger.Close()
		// The inner list spans [300,1800): margin 150, usable 1200, page 720.
		proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1410,
			x2: 540, y2: 690, durationMS: 400})
		requireScrollPlanMode(t, &buffer, scrollModeInnerFallback)
	})

	t.Run("incompatible_inner_is_not_delegated", func(t *testing.T) {
		h := newHarness(t)
		barRect := device.Rect{Left: 0, Top: 400, Right: 1080, Bottom: 700}
		page := scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: pagerRect, Class: "android.widget.HorizontalScrollView",
				Scrollable: true},
			{Parent: 1, Bounds: barRect, Class: "android.widget.HorizontalScrollView",
				Scrollable: true},
		})
		h.device.scriptCaptures(page)
		start := h.startSession()
		pager := scrollContainerMatching(t, h, pagerRect)

		details := scrollAxisRequireStaticRejection(t, h, start.Observation.ID,
			protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: pager},
				Direction: protocol.ScrollDirectionDown,
			}, staticReasonAxisMismatch)
		proposalRequireDetailString(t, details, "scope", tapStaticScopeTarget)
		proposalRequireDetailString(t, details, "scope_ref", pager)
	})
}

// ---------------------------------------------------------------- revalidation

// TestScrollRevalidateAxisConflictRejectsBeforeDispatch proves the fresh-frame
// guard: the observed container is explicitly vertical and plans directly, while
// the fresh frame publishes an explicitly horizontal axis for the same container
// identity because its children are now laid out horizontally. The axis conflict
// rejects with 409 capability_changed and dispatches nothing.
func TestScrollRevalidateAxisConflictRejectsBeforeDispatch(t *testing.T) {
	observed := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
	}
	fresh := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 100, Right: 300, Bottom: 200},
			Class: "android.widget.TextView", Text: "one"},
		{Parent: 1, Bounds: device.Rect{Left: 400, Top: 100, Right: 700, Bottom: 200},
			Class: "android.widget.TextView", Text: "two"},
		{Parent: 1, Bounds: device.Rect{Left: 800, Top: 100, Right: 1080, Bottom: 200},
			Class: "android.widget.TextView", Text: "three"},
	}
	h := newHarness(t)
	h.device.scriptCaptures(scrollPageStep(observed))
	start := h.startSession()
	containerNode := findRole(currentTree(t, h.engine).Root, protocol.RoleScroll)
	if containerNode == nil || containerNode.Axis != protocol.AxisY {
		t.Fatalf("observed container axis = %#v, want y", containerNode)
	}
	h.device.scriptCaptures(scrollPageStep(fresh))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: containerNode.Ref},
		Direction: protocol.ScrollDirectionDown,
	})
	proposalRequireTargetChanged(t, result, targetChangedReasonCapabilityChanged)
	// The message pins the axis-specific branch rather than the ordinary
	// lost-capability branch.
	if result.Error.Message != "滚动容器已失去请求方向的滚动能力，本次未派发" {
		t.Fatalf("axis-conflict message = %q", result.Error.Message)
	}
	proposalRequireNoDispatch(t, h)
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatal("axis-conflict rejection delivered no fresh frame")
	}
}
