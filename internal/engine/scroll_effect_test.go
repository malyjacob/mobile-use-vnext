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
			scrollPoint{540, 2160}, scrollPoint{540, 1008}, true},
		{"down_half", full, full, protocol.ScrollDirectionDown, protocol.ScrollAmountHalf,
			scrollPoint{540, 2160}, scrollPoint{540, 1584}, true},
		{"up_page", full, full, protocol.ScrollDirectionUp, protocol.ScrollAmountPage,
			scrollPoint{540, 240}, scrollPoint{540, 1392}, true},
		{"up_half", full, full, protocol.ScrollDirectionUp, protocol.ScrollAmountHalf,
			scrollPoint{540, 240}, scrollPoint{540, 816}, true},
		// 1080 wide: margin 108, usable 864, page 518, half 259.
		{"right_page", full, full, protocol.ScrollDirectionRight, protocol.ScrollAmountPage,
			scrollPoint{972, 1200}, scrollPoint{454, 1200}, true},
		{"right_half", full, full, protocol.ScrollDirectionRight, protocol.ScrollAmountHalf,
			scrollPoint{972, 1200}, scrollPoint{713, 1200}, true},
		{"left_page", full, full, protocol.ScrollDirectionLeft, protocol.ScrollAmountPage,
			scrollPoint{108, 1200}, scrollPoint{626, 1200}, true},
		{"left_half", full, full, protocol.ScrollDirectionLeft, protocol.ScrollAmountHalf,
			scrollPoint{108, 1200}, scrollPoint{367, 1200}, true},
		// Odd extents use integer floor for margin and travel.
		{"odd_vertical", device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 2401}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{500, 2161}, scrollPoint{500, 1009}, true},
		// An empty amount defaults to page, exactly like the decoder.
		{"empty_amount_is_page", full, full, protocol.ScrollDirectionDown, "",
			scrollPoint{540, 2160}, scrollPoint{540, 1008}, true},
		// Too small: extent 100, margin 24, usable 52, page travel 31 < 48.
		{"too_small_page", device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 100}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"too_small_half", device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 100}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountHalf,
			scrollPoint{}, scrollPoint{}, false},
		// Half-open screen containment: a start exactly on the screen bottom
		// edge is outside, one pixel more is inside. No clamping.
		{"screen_half_open_excluded", full, device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2160},
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"screen_half_open_included", full, device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2161},
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{540, 2160}, scrollPoint{540, 1008}, true},
		{"empty_rect", device.Rect{Left: 0, Top: 0, Right: 0, Bottom: 0}, full,
			protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"unknown_direction", full, full, "sideways", protocol.ScrollAmountPage,
			scrollPoint{}, scrollPoint{}, false},
		{"unknown_amount", full, full, protocol.ScrollDirectionDown, "full",
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
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
	}
	return append(specs, units...)
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
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	preCapture := buildCapture(display, preSpecs)
	postCapture := buildCapture(display, postSpecs)
	pre := scrollProjection(t, preCapture)
	post := scrollProjection(t, postCapture)
	return &scrollFrame{
			capture: pre.Capture, projection: pre, containerIndex: 1, direction: protocol.ScrollDirectionDown,
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
	return computeScrollEffect(pre, post, quality, protocol.ScrollDirectionDown,
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
