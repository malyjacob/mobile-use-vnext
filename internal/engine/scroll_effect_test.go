package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- geometry

func TestScrollGeometryTable(t *testing.T) {
	full := screenRect()
	cases := []struct {
		name       string
		rect       device.Rect
		screen     device.Rect
		direction  string
		amount     string
		start, end scrollPoint
		ok         bool
	}{
		// 2400 high: margin 240, usable 1920, page 1152, half 576.
		{"down_page", full, full, protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{540, 1776}, scrollPoint{540, 624}, true},
		{"down_half", full, full, protocol.ScrollDirectionDown, protocol.ScrollAmountHalf,
			scrollPoint{540, 1488}, scrollPoint{540, 912}, true},
		{"up_page", full, full, protocol.ScrollDirectionUp, protocol.ScrollAmountPage,
			scrollPoint{540, 624}, scrollPoint{540, 1776}, true},
		{"up_half", full, full, protocol.ScrollDirectionUp, protocol.ScrollAmountHalf,
			scrollPoint{540, 912}, scrollPoint{540, 1488}, true},
		// 1080 wide: margin 108, usable 864, page 518, half 259.
		{"right_page", full, full, protocol.ScrollDirectionRight, protocol.ScrollAmountPage,
			scrollPoint{799, 1200}, scrollPoint{281, 1200}, true},
		{"right_half", full, full, protocol.ScrollDirectionRight, protocol.ScrollAmountHalf,
			scrollPoint{670, 1200}, scrollPoint{411, 1200}, true},
		{"left_page", full, full, protocol.ScrollDirectionLeft, protocol.ScrollAmountPage,
			scrollPoint{281, 1200}, scrollPoint{799, 1200}, true},
		{"left_half", full, full, protocol.ScrollDirectionLeft, protocol.ScrollAmountHalf,
			scrollPoint{411, 1200}, scrollPoint{670, 1200}, true},
		// Odd extents use integer floor for margin and travel.
		{"odd_vertical", device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 2401}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{500, 1776}, scrollPoint{500, 624}, true},
		// An empty amount defaults to page, exactly like the decoder.
		{"empty_amount_is_page", full, full, protocol.ScrollDirectionDown, "",
			scrollPoint{540, 1776}, scrollPoint{540, 624}, true},
		// Too small: extent 100, margin 24, usable 52, page travel 31 < 48.
		{"too_small_page", device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 100}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"too_small_half", device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 100}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountHalf,
			scrollPoint{}, scrollPoint{}, false},
		// Half-open screen containment: a start exactly on the screen bottom
		// edge is outside, one pixel more is inside. No clamping.
		{"screen_half_open_excluded", full, device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 1776},
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"screen_half_open_included", full, device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 1777},
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{540, 1776}, scrollPoint{540, 624}, true},
		{"empty_rect", device.Rect{Left: 0, Top: 0, Right: 0, Bottom: 0}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"unknown_direction", full, full, "sideways", protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"unknown_amount", full, full, protocol.ScrollDirectionDown, "wide",
			scrollPoint{}, scrollPoint{}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			start, end, ok := scrollGeometry(testCase.rect, testCase.screen,
				testCase.direction, testCase.amount)
			if ok != testCase.ok {
				t.Fatalf("ok = %v, want %v (start=%+v end=%+v)", ok, testCase.ok, start, end)
			}
			if !ok {
				return
			}
			if start != testCase.start || end != testCase.end {
				t.Fatalf("trajectory = %+v..%+v, want %+v..%+v",
					start, end, testCase.start, testCase.end)
			}
			if !insideScrollRect(start, testCase.rect) || !insideScrollRect(end, testCase.rect) {
				t.Fatalf("trajectory escapes the container: %+v..%+v", start, end)
			}
		})
	}
}

// TestScrollGeometryDirectionMappingsAreStrict is the direction reverse
// control: each direction must move along its declared axis in exactly one
// sign. Reversing any mapping in scrollGeometry makes this fail.
func TestScrollGeometryDirectionMappingsAreStrict(t *testing.T) {
	full := screenRect()
	downStart, downEnd, downOK := scrollGeometry(full, full, protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
	upStart, upEnd, upOK := scrollGeometry(full, full, protocol.ScrollDirectionUp, protocol.ScrollAmountPage)
	rightStart, rightEnd, rightOK := scrollGeometry(full, full, protocol.ScrollDirectionRight, protocol.ScrollAmountPage)
	leftStart, leftEnd, leftOK := scrollGeometry(full, full, protocol.ScrollDirectionLeft, protocol.ScrollAmountPage)
	if !downOK || !upOK || !rightOK || !leftOK {
		t.Fatal("full-screen container must be executable in every direction")
	}
	if !(downStart.Y > downEnd.Y) || downStart.X != downEnd.X {
		t.Fatalf("down mapping moved the wrong way: %+v..%+v", downStart, downEnd)
	}
	if !(upStart.Y < upEnd.Y) || upStart.X != upEnd.X {
		t.Fatalf("up mapping moved the wrong way: %+v..%+v", upStart, upEnd)
	}
	if !(rightStart.X > rightEnd.X) || rightStart.Y != rightEnd.Y {
		t.Fatalf("right mapping moved the wrong way: %+v..%+v", rightStart, rightEnd)
	}
	if !(leftStart.X < leftEnd.X) || leftStart.Y != leftEnd.Y {
		t.Fatalf("left mapping moved the wrong way: %+v..%+v", leftStart, leftEnd)
	}
	if downStart == upStart || downEnd == upEnd {
		t.Fatal("down and up produced the same trajectory")
	}
}

// ---------------------------------------------------------------- effect

func scrollProjection(t *testing.T, capture device.Capture) *Projection {
	t.Helper()
	snapshot, err := observation.NewBuilder().Build(&capture)
	if err != nil {
		t.Fatalf("build projection: %v", err)
	}
	return &Projection{Tree: snapshot.Tree, Capture: snapshot.Capture, Source: snapshot.Source}
}

func effectPage(units []rawNodeSpec) []rawNodeSpec {
	return effectPageRect(screenRect(), units)
}

// effectPageRect is the same page with an explicit container rectangle, so a
// test can move or resize the viewport between the two frames.
func effectPageRect(container device.Rect, units []rawNodeSpec) []rawNodeSpec {
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: container, Class: "android.widget.ScrollView", Scrollable: true},
	}
	return append(specs, units...)
}

// horizontalEffectPage is the transposed fixture: the request axis is x.
func horizontalEffectPage(container device.Rect, units []rawNodeSpec) []rawNodeSpec {
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: container, Class: "android.widget.HorizontalScrollView", Scrollable: true},
	}
	return append(specs, units...)
}

// hUnit is one horizontally laid out content unit.
func hUnit(left, width int, text string) rawNodeSpec {
	return rawNodeSpec{Parent: 1, Bounds: device.Rect{Left: left, Top: 100,
		Right: left + width, Bottom: 300}, Class: "android.widget.TextView", Text: text}
}

func textUnit(top, height int, text string) rawNodeSpec {
	return rawNodeSpec{Parent: 1, Bounds: device.Rect{Left: 0, Top: top, Right: 1080, Bottom: top + height},
		Class: "android.widget.TextView", Text: text}
}

func movedUnit(top, height int, text string, delta int) rawNodeSpec {
	spec := textUnit(top, height, text)
	spec.Bounds.Top += delta
	spec.Bounds.Bottom += delta
	return spec
}

func effectInputs(t *testing.T, preSpecs, postSpecs []rawNodeSpec) (*scrollFrame, *observationState) {
	t.Helper()
	return effectInputsDirection(t, preSpecs, postSpecs, protocol.ScrollDirectionDown)
}

func effectInputsDirection(t *testing.T, preSpecs, postSpecs []rawNodeSpec,
	direction string) (*scrollFrame, *observationState) {
	t.Helper()
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	preCapture := buildCapture(display, preSpecs)
	postCapture := buildCapture(display, postSpecs)
	pre := scrollProjection(t, preCapture)
	post := scrollProjection(t, postCapture)
	return &scrollFrame{
			capture: pre.Capture, projection: pre, containerIndex: 1, direction: direction,
		},
		&observationState{Projection: post}
}

func stableQuality() *protocol.Quality {
	return &protocol.Quality{
		State: protocol.QualityStable, Samples: 2, StopReason: protocol.QualityStopStable,
	}
}

func computeEffect(t *testing.T, pre *scrollFrame, post *observationState,
	quality *protocol.Quality) scrollEffectResult {
	t.Helper()
	return computeEffectDirection(t, pre, post, quality, protocol.ScrollDirectionDown)
}

func computeEffectDirection(t *testing.T, pre *scrollFrame, post *observationState,
	quality *protocol.Quality, direction string) scrollEffectResult {
	t.Helper()
	return computeScrollEffect(pre, post, quality, direction,
		context.Background(), time.Now().Add(time.Second))
}

func requireEffectResult(t *testing.T, got scrollEffectResult, state, reason string) {
	t.Helper()
	if got.State != state || got.Reason != reason {
		t.Fatalf("effect = %+v, want state=%s reason=%q", got, state, reason)
	}
}

// TestScrollEffectNotObservedNeedsFullCoverageAndStable covers the only
// sufficient not_observed definition. Reverse control: dropping the stable gate
// makes the unstable case report not_observed.
func TestScrollEffectNotObservedNeedsFullCoverageAndStable(t *testing.T) {
	units := []rawNodeSpec{
		textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo"), textUnit(1600, 200, "charlie"),
	}
	pre, post := effectInputs(t, effectPage(units), effectPage(units))
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectNotObserved, "")
	if got.Anchors != 3 || got.Coverage != 100 {
		t.Fatalf("anchors=%d coverage=%d, want 3/100", got.Anchors, got.Coverage)
	}

	unstable := &protocol.Quality{
		State: protocol.QualityUnstable, Samples: 2, StopReason: protocol.QualityStopMaxSamples,
	}
	got = computeEffect(t, pre, post, unstable)
	requireEffectResult(t, got, protocol.EffectUnknown, effectReasonUnstable)
}

// TestScrollEffectFixedTitlesDoNotProveNotObserved is the required negative
// case: fixed titles survive while the rest of the content leaves the viewport,
// so coverage is incomplete and the answer must stay unknown. Reverse control:
// removing the coverage condition would report not_observed.
func TestScrollEffectFixedTitlesDoNotProveNotObserved(t *testing.T) {
	preUnits := []rawNodeSpec{
		textUnit(200, 60, "title-one"), textUnit(280, 60, "title-two"),
		textUnit(1000, 200, "body-one"), textUnit(1300, 200, "body-two"),
		textUnit(1600, 200, "body-three"), textUnit(1900, 200, "body-four"),
	}
	postUnits := []rawNodeSpec{
		textUnit(200, 60, "title-one"), textUnit(280, 60, "title-two"),
		textUnit(1000, 200, "new-one"), textUnit(1300, 200, "new-two"),
		textUnit(1600, 200, "new-three"), textUnit(1900, 200, "new-four"),
	}
	pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectUnknown, effectReasonCoverage)
	if got.Anchors != 2 || got.Coverage != 33 {
		t.Fatalf("anchors=%d coverage=%d, want 2/33", got.Anchors, got.Coverage)
	}
}

// TestScrollEffectObservedConsensus covers same-sign displacement, low coverage
// positive evidence, the 75% boundary and mixed directions. Reverse control:
// removing the consensus condition turns the 2/3 case into observed.
func TestScrollEffectObservedConsensus(t *testing.T) {
	t.Run("two_movers", func(t *testing.T) {
		preUnits := []rawNodeSpec{textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo")}
		postUnits := []rawNodeSpec{movedUnit(1000, 200, "alpha", -50), movedUnit(1300, 200, "bravo", -50)}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectObserved, "")
		if got.Axis != "y" {
			t.Fatalf("axis = %q, want y", got.Axis)
		}
	})

	t.Run("low_coverage_two_movers", func(t *testing.T) {
		preUnits := []rawNodeSpec{
			textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo"),
			textUnit(1600, 200, "charlie"), textUnit(1900, 200, "delta"),
		}
		postUnits := []rawNodeSpec{
			movedUnit(1000, 200, "alpha", -50), movedUnit(1300, 200, "bravo", -50),
			textUnit(1600, 200, "new-one"), textUnit(1900, 200, "new-two"),
		}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectObserved, "")
		if got.Coverage != 50 || got.Anchors != 2 {
			t.Fatalf("anchors=%d coverage=%d, want 2/50", got.Anchors, got.Coverage)
		}
	})

	t.Run("three_of_four_observed", func(t *testing.T) {
		preUnits := []rawNodeSpec{
			textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo"),
			textUnit(1600, 200, "charlie"), textUnit(1900, 200, "delta"),
		}
		postUnits := []rawNodeSpec{
			movedUnit(1000, 200, "alpha", -50), movedUnit(1300, 200, "bravo", -50),
			movedUnit(1600, 200, "charlie", -50), textUnit(1900, 200, "delta"),
		}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectObserved, "")
	})

	t.Run("two_of_three_unknown", func(t *testing.T) {
		preUnits := []rawNodeSpec{
			textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo"),
			textUnit(1600, 200, "charlie"),
		}
		postUnits := []rawNodeSpec{
			movedUnit(1000, 200, "alpha", -50), movedUnit(1300, 200, "bravo", -50),
			movedUnit(1600, 200, "charlie", 50),
		}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonMotion)
	})

	t.Run("direction_conflict_unknown", func(t *testing.T) {
		preUnits := []rawNodeSpec{
			textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo"),
			textUnit(1600, 200, "charlie"), textUnit(1900, 200, "delta"),
		}
		postUnits := []rawNodeSpec{
			movedUnit(1000, 200, "alpha", -50), movedUnit(1300, 200, "bravo", -50),
			movedUnit(1600, 200, "charlie", 50), movedUnit(1900, 200, "delta", 50),
		}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonMotion)
	})

	t.Run("observed_while_unstable", func(t *testing.T) {
		preUnits := []rawNodeSpec{textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo")}
		postUnits := []rawNodeSpec{movedUnit(1000, 200, "alpha", -50), movedUnit(1300, 200, "bravo", -50)}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		unstable := &protocol.Quality{
			State: protocol.QualityUnstable, Samples: 3, StopReason: protocol.QualityStopBudget,
		}
		got := computeEffect(t, pre, post, unstable)
		requireEffectResult(t, got, protocol.EffectObserved, "")
	})
}

// TestScrollEffectParentChildDuplicateCountsOnlyLeaf proves the parent/child
// duplicate content is counted once (the leaf). If both were units, the shared
// key would not be unique and only the two other units would match.
func TestScrollEffectParentChildDuplicateCountsOnlyLeaf(t *testing.T) {
	preSpecs := effectPage([]rawNodeSpec{
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "dup"},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "dup"},
		textUnit(1300, 200, "alpha"), textUnit(1600, 200, "bravo"),
	})
	postSpecs := effectPage([]rawNodeSpec{
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 950, Right: 1080, Bottom: 1150},
			Class: "android.widget.TextView", Text: "dup"},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 950, Right: 1080, Bottom: 1150},
			Class: "android.widget.TextView", Text: "dup"},
		movedUnit(1300, 200, "alpha", -50), movedUnit(1600, 200, "bravo", -50),
	})
	pre, post := effectInputs(t, preSpecs, postSpecs)
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectObserved, "")
	if got.Anchors != 3 {
		t.Fatalf("anchors = %d, want 3 (parent duplicate must not be a unit)", got.Anchors)
	}
}

// TestScrollEffectEmptyStructureNodesNotCounted proves a semantically empty
// group is not a content unit while its labelled children are.
func TestScrollEffectEmptyStructureNodesNotCounted(t *testing.T) {
	preSpecs := effectPage([]rawNodeSpec{
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1900},
			Class: "android.widget.FrameLayout"},
	})
	preSpecs = append(preSpecs,
		rawNodeSpec{Parent: 2, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
		rawNodeSpec{Parent: 2, Bounds: device.Rect{Left: 0, Top: 1300, Right: 1080, Bottom: 1500},
			Class: "android.widget.TextView", Text: "bravo"},
	)
	postSpecs := effectPage([]rawNodeSpec{
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1900},
			Class: "android.widget.FrameLayout"},
	})
	postSpecs = append(postSpecs,
		rawNodeSpec{Parent: 2, Bounds: device.Rect{Left: 0, Top: 950, Right: 1080, Bottom: 1150},
			Class: "android.widget.TextView", Text: "alpha"},
		rawNodeSpec{Parent: 2, Bounds: device.Rect{Left: 0, Top: 1250, Right: 1080, Bottom: 1450},
			Class: "android.widget.TextView", Text: "bravo"},
	)
	pre, post := effectInputs(t, preSpecs, postSpecs)
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectObserved, "")
	if got.Anchors != 2 {
		t.Fatalf("anchors = %d, want 2 (empty group is not a unit)", got.Anchors)
	}
}

// TestScrollEffectSizeAndContainerChangesAreUnknown covers the size gate and
// the container identity gate. Reverse control: dropping the size check would
// turn the resized unit into a movement decision.
func TestScrollEffectSizeAndContainerChangesAreUnknown(t *testing.T) {
	t.Run("unit_resize", func(t *testing.T) {
		preUnits := []rawNodeSpec{textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo")}
		postUnits := []rawNodeSpec{
			{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1000, Bottom: 1200},
				Class: "android.widget.TextView", Text: "alpha"},
			textUnit(1300, 200, "bravo"),
		}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonSize)
	})

	t.Run("container_bounds_change", func(t *testing.T) {
		units := []rawNodeSpec{textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo")}
		post := append([]rawNodeSpec(nil), effectPage(units)...)
		post[1].Bounds = device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2390}
		pre, postObs := effectInputs(t, effectPage(units), post)
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})

	t.Run("container_identity_change", func(t *testing.T) {
		units := []rawNodeSpec{textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo")}
		post := append([]rawNodeSpec(nil), effectPage(units)...)
		post[1].Class = "android.widget.HorizontalScrollView"
		pre, postObs := effectInputs(t, effectPage(units), post)
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
}

// TestScrollEffectInsufficientAnchors proves the minimum anchor rule.
func TestScrollEffectInsufficientAnchors(t *testing.T) {
	preUnits := []rawNodeSpec{textUnit(1000, 200, "alpha")}
	postUnits := []rawNodeSpec{movedUnit(1000, 200, "alpha", -50)}
	pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectUnknown, effectReasonInsufficient)
	if got.Anchors != 1 {
		t.Fatalf("anchors = %d, want 1", got.Anchors)
	}
}

// TestScrollEffectPostFailureAndBudget covers the ctx/CPU boundary and the
// missing post frame: both are unknown, never not_observed.
func TestScrollEffectPostFailureAndBudget(t *testing.T) {
	units := []rawNodeSpec{textUnit(1000, 200, "alpha"), textUnit(1300, 200, "bravo")}
	pre, post := effectInputs(t, effectPage(units), effectPage(units))

	requireEffectResult(t, computeScrollEffect(pre, nil, stableQuality(),
		protocol.ScrollDirectionDown, context.Background(), time.Now().Add(time.Second)),
		protocol.EffectUnknown, effectReasonPostFailed)
	requireEffectResult(t, computeScrollEffect(pre, &observationState{}, stableQuality(),
		protocol.ScrollDirectionDown, context.Background(), time.Now().Add(time.Second)),
		protocol.EffectUnknown, effectReasonPostFailed)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	requireEffectResult(t, computeScrollEffect(pre, post, stableQuality(),
		protocol.ScrollDirectionDown, cancelled, time.Now().Add(time.Second)),
		protocol.EffectUnknown, effectReasonCPUBudget)
	requireEffectResult(t, computeScrollEffect(pre, post, stableQuality(),
		protocol.ScrollDirectionDown, context.Background(), time.Now().Add(-time.Millisecond)),
		protocol.EffectUnknown, effectReasonCPUBudget)
}

// ---------------------------------------------------------------- viewport effect

// TestScrollEffectAxisViewportShrinksWithInternalMovers is the capability the
// proposal adds: a container that only shrinks along the request axis no longer
// forces unknown. The internal units still move together, while the clipped edge
// unit is excluded from positive evidence.
func TestScrollEffectAxisViewportShrinksWithInternalMovers(t *testing.T) {
	preUnits := []rawNodeSpec{
		textUnit(300, 200, "alpha"), textUnit(800, 200, "bravo"), textUnit(2000, 400, "edge"),
	}
	postUnits := []rawNodeSpec{
		textUnit(200, 200, "alpha"), textUnit(700, 200, "bravo"), textUnit(2000, 400, "edge"),
	}
	pre, post := effectInputs(t,
		effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, preUnits),
		effectPageRect(device.Rect{Right: 1080, Bottom: 2100}, postUnits))
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectObserved, "")
	if got.Anchors != 3 {
		t.Fatalf("anchors = %d, want the full matched set 3", got.Anchors)
	}

	// Reverse control: without the axis-edge exclusion the clipped edge unit is
	// a size change and the whole inference collapses to unknown.
	if scrollEligibleUnit(matchedScrollUnit{
		pre:  scrollContentUnit{rect: device.Rect{Top: 2000, Bottom: 2400}, raw: device.Rect{Top: 2000, Bottom: 2400}},
		post: scrollContentUnit{rect: device.Rect{Top: 2000, Bottom: 2100}, raw: device.Rect{Top: 2000, Bottom: 2400}},
	}, device.Rect{Right: 1080, Bottom: 2400}, device.Rect{Right: 1080, Bottom: 2100}, true) {
		t.Fatal("an axis-edge unit with a clipped axial size must not be eligible")
	}
}

// TestScrollEffectHorizontalAxisViewportObserved is the transposed positive
// case: only the right edge of a horizontal viewport shrinks and the two internal
// units move together.
func TestScrollEffectHorizontalAxisViewportObserved(t *testing.T) {
	preUnits := []rawNodeSpec{hUnit(300, 200, "alpha"), hUnit(600, 200, "bravo")}
	postUnits := []rawNodeSpec{hUnit(200, 200, "alpha"), hUnit(500, 200, "bravo")}
	pre, post := effectInputsDirection(t,
		horizontalEffectPage(device.Rect{Right: 1080, Bottom: 2400}, preUnits),
		horizontalEffectPage(device.Rect{Right: 900, Bottom: 2400}, postUnits),
		protocol.ScrollDirectionRight)
	got := computeEffectDirection(t, pre, post, stableQuality(), protocol.ScrollDirectionRight)
	requireEffectResult(t, got, protocol.EffectObserved, "")
	if got.Axis != "x" {
		t.Fatalf("axis = %q, want x", got.Axis)
	}
}

// TestScrollEffectSingleInternalCandidateUnknown keeps the minimum: one internal
// mover is not consensus, and a changed container cannot prove stillness.
func TestScrollEffectSingleInternalCandidateUnknown(t *testing.T) {
	preUnits := []rawNodeSpec{
		textUnit(300, 200, "alpha"), textUnit(2000, 400, "edge"),
	}
	postUnits := []rawNodeSpec{
		textUnit(200, 200, "alpha"), textUnit(2000, 400, "edge"),
	}
	pre, post := effectInputs(t,
		effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, preUnits),
		effectPageRect(device.Rect{Right: 1080, Bottom: 2100}, postUnits))
	got := computeEffect(t, pre, post, stableQuality())
	if got.State != protocol.EffectUnknown {
		t.Fatalf("effect = %+v, want unknown", got)
	}
}

// TestScrollEffectContainerContentReferenceFrames pins the dual reference on the
// three canonical arrangements of a viewport that grows upward.
func TestScrollEffectContainerContentReferenceFrames(t *testing.T) {
	moved := []rawNodeSpec{textUnit(500, 200, "alpha"), textUnit(900, 200, "bravo")}
	t.Run("content_follows_container", func(t *testing.T) {
		// Top 349->0 and content shifted by -349: absolute -349, relative 0.
		post := []rawNodeSpec{movedUnit(500, 200, "alpha", -349), movedUnit(900, 200, "bravo", -349)}
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Top: 349, Right: 1080, Bottom: 2400}, moved),
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, post))
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
	t.Run("content_follows_and_moves", func(t *testing.T) {
		// The same shift plus a real relative -50: both references agree.
		post := []rawNodeSpec{movedUnit(500, 200, "alpha", -399), movedUnit(900, 200, "bravo", -399)}
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Top: 349, Right: 1080, Bottom: 2400}, moved),
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, post))
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectObserved, "")
	})
	t.Run("screen_static_relative_moved", func(t *testing.T) {
		// Content keeps its screen position while the viewport grows: absolute 0,
		// relative 349. A relative-only rule would report observed.
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Top: 349, Right: 1080, Bottom: 2400}, moved),
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, moved))
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
	t.Run("horizontal_transpose", func(t *testing.T) {
		units := []rawNodeSpec{hUnit(500, 200, "alpha"), hUnit(900, 200, "bravo")}
		pre, postObs := effectInputsDirection(t,
			horizontalEffectPage(device.Rect{Left: 349, Right: 1080, Bottom: 2400}, units),
			horizontalEffectPage(device.Rect{Right: 1080, Bottom: 2400}, units),
			protocol.ScrollDirectionRight)
		got := computeEffectDirection(t, pre, postObs, stableQuality(), protocol.ScrollDirectionRight)
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
		if got.Axis != "x" {
			t.Fatalf("axis = %q, want x", got.Axis)
		}
	})
	t.Run("pure_translation", func(t *testing.T) {
		// Both axial edges move together with static internal content.
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, moved),
			effectPageRect(device.Rect{Right: 1080, Top: 60, Bottom: 2400}, moved))
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
	t.Run("bottom_extends_top_fixed", func(t *testing.T) {
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Right: 1080, Bottom: 2200}, moved),
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, moved))
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
}

// TestScrollEffectDualReferenceCancelAndBuckets covers the two conservative
// consequences: screen motion that is cancelled by the container, and equal
// relative motion split across absolute zero points.
func TestScrollEffectDualReferenceCancelAndBuckets(t *testing.T) {
	t.Run("absolute_cancelled", func(t *testing.T) {
		// The container moves up by 100 while the content keeps its screen
		// position: relative +100, absolute 0. Relative-only evidence would be a
		// false positive.
		units := []rawNodeSpec{textUnit(500, 200, "alpha"), textUnit(900, 200, "bravo")}
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Right: 1080, Top: 100, Bottom: 2400}, units),
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, units))
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
	t.Run("four_bucket_split", func(t *testing.T) {
		// Every unit moves down on screen, but two lose relative position and two
		// gain it. Merging the buckets by absolute sign alone would reach 4/4.
		preUnits := []rawNodeSpec{
			textUnit(400, 200, "alpha"), textUnit(700, 200, "bravo"),
			textUnit(1200, 200, "charlie"), textUnit(1500, 200, "delta"),
		}
		postUnits := []rawNodeSpec{
			textUnit(500, 200, "alpha"), textUnit(800, 200, "bravo"),
			textUnit(1600, 200, "charlie"), textUnit(1900, 200, "delta"),
		}
		pre, postObs := effectInputs(t,
			effectPageRect(device.Rect{Right: 1080, Bottom: 2400}, preUnits),
			effectPageRect(device.Rect{Right: 1080, Top: 300, Bottom: 2400}, postUnits))
		got := computeEffect(t, pre, postObs, stableQuality())
		if got.State != protocol.EffectUnknown {
			t.Fatalf("effect = %+v, want unknown", got)
		}
	})
}

// TestScrollEffectInternalDeformationUnknown keeps the global size guard: a
// deformation that is not on the axis edge cannot be filtered away by the
// surviving movers.
func TestScrollEffectInternalDeformationUnknown(t *testing.T) {
	preUnits := []rawNodeSpec{
		textUnit(400, 200, "alpha"), textUnit(700, 200, "bravo"), textUnit(1000, 200, "charlie"),
	}
	postUnits := []rawNodeSpec{
		textUnit(300, 200, "alpha"), textUnit(600, 200, "bravo"), textUnit(900, 400, "charlie"),
	}
	pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
	got := computeEffect(t, pre, post, stableQuality())
	requireEffectResult(t, got, protocol.EffectUnknown, effectReasonSize)
}

// TestScrollEffectPostContainerCapability covers the post container guards: a
// container that loses its scroll capability or changes identity cannot be
// crossed by content movement.
func TestScrollEffectPostContainerCapability(t *testing.T) {
	units := []rawNodeSpec{textUnit(400, 200, "alpha"), textUnit(700, 200, "bravo")}
	movedUnits := []rawNodeSpec{movedUnit(400, 200, "alpha", -100), movedUnit(700, 200, "bravo", -100)}
	t.Run("disabled", func(t *testing.T) {
		post := effectPage(movedUnits)
		post[1].Disabled = true
		pre, postObs := effectInputs(t, effectPage(units), post)
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
	t.Run("duplicate_content_key", func(t *testing.T) {
		// A second published node with the same content key and ancestry makes the
		// container match ambiguous, never a guess.
		post := append(effectPage(movedUnits), rawNodeSpec{Parent: 0, Bounds: screenRect(),
			Class: "android.widget.ScrollView", Scrollable: true})
		pre, postObs := effectInputs(t, effectPage(units), post)
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
	t.Run("orthogonal_edge_changed", func(t *testing.T) {
		post := append([]rawNodeSpec(nil), effectPage(movedUnits)...)
		post[1].Bounds = device.Rect{Left: 20, Right: 1060, Bottom: 2400}
		pre, postObs := effectInputs(t, effectPage(units), post)
		got := computeEffect(t, pre, postObs, stableQuality())
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)
	})
}

// TestScrollEffectEqualSizeClipDriftUnknown is the anti-drift control: two static
// oversized units clipped by a translated equal-length viewport keep their
// clipped size while the clipped centres move together. They must never become
// positive evidence.
func TestScrollEffectEqualSizeClipDriftUnknown(t *testing.T) {
	units := []rawNodeSpec{
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 5000},
			Class: "android.widget.TextView", Text: "alpha"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: -1000, Right: 1080, Bottom: 6000},
			Class: "android.widget.TextView", Text: "bravo"},
	}
	preContainer := device.Rect{Right: 1080, Bottom: 800}
	postContainer := device.Rect{Right: 1080, Top: 100, Bottom: 900}
	pre, postObs := effectInputs(t, effectPageRect(preContainer, units), effectPageRect(postContainer, units))
	got := computeEffect(t, pre, postObs, stableQuality())
	requireEffectResult(t, got, protocol.EffectUnknown, effectReasonContainer)

	// Reverse control: the axis-edge exclusion is what keeps these clipped
	// windows out of the positive evidence, even at equal clipped size.
	pair := matchedScrollUnit{
		pre:  scrollContentUnit{rect: preContainer, raw: device.Rect{Right: 1080, Bottom: 5000}},
		post: scrollContentUnit{rect: postContainer, raw: device.Rect{Right: 1080, Bottom: 5000}},
	}
	if scrollEligibleUnit(pair, preContainer, postContainer, true) {
		t.Fatal("an axis-edge clipped unit must not be eligible")
	}
}

// TestScrollEffectStillNotObserved covers the negative evidence that must not be
// tightened: every matched unit is still, even when the eligible positive
// candidates are not enough.
func TestScrollEffectStillNotObserved(t *testing.T) {
	t.Run("edge_only_units", func(t *testing.T) {
		units := []rawNodeSpec{textUnit(0, 200, "alpha"), textUnit(2200, 200, "bravo")}
		pre, post := effectInputs(t, effectPage(units), effectPage(units))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectNotObserved, "")
		if got.Anchors != 2 {
			t.Fatalf("anchors = %d, want 2", got.Anchors)
		}
	})
	t.Run("clipped_oversized_units", func(t *testing.T) {
		// Both units exceed the viewport in both frames; their visible rectangle
		// never changes. This is the documented clipping blind spot and stays
		// not_observed.
		preUnits := []rawNodeSpec{
			{Parent: 1, Bounds: device.Rect{Left: 0, Top: -100, Right: 1080, Bottom: 2500},
				Class: "android.widget.TextView", Text: "alpha"},
			{Parent: 1, Bounds: device.Rect{Left: 0, Top: -50, Right: 1080, Bottom: 2550},
				Class: "android.widget.TextView", Text: "bravo"},
		}
		postUnits := []rawNodeSpec{
			{Parent: 1, Bounds: device.Rect{Left: 0, Top: -50, Right: 1080, Bottom: 2550},
				Class: "android.widget.TextView", Text: "alpha"},
			{Parent: 1, Bounds: device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2600},
				Class: "android.widget.TextView", Text: "bravo"},
		}
		pre, post := effectInputs(t, effectPage(preUnits), effectPage(postUnits))
		got := computeEffect(t, pre, post, stableQuality())
		requireEffectResult(t, got, protocol.EffectNotObserved, "")
	})
	t.Run("not_observed_needs_stability", func(t *testing.T) {
		units := []rawNodeSpec{textUnit(400, 200, "alpha"), textUnit(700, 200, "bravo")}
		pre, post := effectInputs(t, effectPage(units), effectPage(units))
		unstable := &protocol.Quality{State: protocol.QualityUnstable, Samples: 2,
			StopReason: protocol.QualityStopMaxSamples}
		got := computeEffect(t, pre, post, unstable)
		requireEffectResult(t, got, protocol.EffectUnknown, effectReasonUnstable)
	})
}
