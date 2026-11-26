package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

type revalCountingContext struct {
	context.Context
	checks   int
	cancelAt int
}

func (ctx *revalCountingContext) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestActRevalidationStructureTriStateMatrix(t *testing.T) {
	base := revalButton(1, 0, 1, 12,
		device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300},
		"Save", "action", "btn_save")
	base.TextSet, base.DescSet, base.ResourceSet = true, true, true

	t.Run("known_known_equal", func(t *testing.T) {
		compatible, evidence := sameStructure(&base, &base)
		if !compatible || !evidence {
			t.Fatalf("identical known structure = compatible %v evidence %v, want true/true",
				compatible, evidence)
		}
	})

	for name, mutate := range map[string]func(*device.RawNode){
		"clickable":      func(node *device.RawNode) { node.Clickable = false },
		"long_clickable": func(node *device.RawNode) { node.LongClickable = false },
		"scrollable":     func(node *device.RawNode) { node.Scrollable = true },
		"enabled":        func(node *device.RawNode) { node.Enabled = false },
		"checked":        func(node *device.RawNode) { node.Checked = true },
		"password":       func(node *device.RawNode) { node.Password = true },
		"checkable":      func(node *device.RawNode) { node.Checkable = true },
		"text":           func(node *device.RawNode) { node.Text = "Share" },
		"description":    func(node *device.RawNode) { node.Description = "other" },
		"resource":       func(node *device.RawNode) { node.ResourceID = "other" },
		"editable":       func(node *device.RawNode) { node.Editable = true },
	} {
		t.Run("known_known_mismatch_"+name, func(t *testing.T) {
			other := base
			mutate(&other)
			if compatible, _ := sameStructure(&base, &other); compatible {
				t.Fatalf("known-known %s mismatch was accepted", name)
			}
		})
	}

	for name, mutate := range map[string]func(*device.RawNode){
		"clickable":   func(node *device.RawNode) { node.HasClickable = false },
		"long_click":  func(node *device.RawNode) { node.HasLongClick = false },
		"scrollable":  func(node *device.RawNode) { node.HasScrollable = false },
		"enabled":     func(node *device.RawNode) { node.HasEnabled = false },
		"checked":     func(node *device.RawNode) { node.HasChecked = false },
		"password":    func(node *device.RawNode) { node.HasPassword = false },
		"checkable":   func(node *device.RawNode) { node.HasCheckable = false },
		"text":        func(node *device.RawNode) { node.TextSet = false },
		"description": func(node *device.RawNode) { node.DescSet = false },
		"resource":    func(node *device.RawNode) { node.ResourceSet = false },
	} {
		t.Run("known_unknown_mismatch_"+name, func(t *testing.T) {
			other := base
			mutate(&other)
			if compatible, _ := sameStructure(&base, &other); compatible {
				t.Fatalf("known-unknown %s was accepted", name)
			}
		})
	}

	unknown := device.RawNode{}
	if compatible, evidence := sameStructure(&unknown, &unknown); !compatible || evidence {
		t.Fatalf("unknown-unknown structure = compatible %v evidence %v, want true/false",
			compatible, evidence)
	}

	triCases := []struct {
		name       string
		left       tri
		right      tri
		comparison triCompare
	}{
		{"known_known_equal_true", tri{true, true}, tri{true, true}, triEqual},
		{"known_known_equal_false", tri{true, false}, tri{true, false}, triEqual},
		{"known_known_mismatch", tri{true, true}, tri{true, false}, triMismatch},
		{"known_unknown", tri{true, true}, tri{}, triMismatch},
		{"unknown_known", tri{}, tri{true, false}, triMismatch},
		{"unknown_unknown", tri{}, tri{}, triUnknown},
	}
	for _, tc := range triCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareTri(tc.left, tc.right); got != tc.comparison {
				t.Fatalf("compareTri(%+v,%+v) = %v, want %v",
					tc.left, tc.right, got, tc.comparison)
			}
		})
	}
}

func TestActRevalidationTransparentWrapperCapabilityMatrix(t *testing.T) {
	bounds := device.Rect{Left: 10, Top: 20, Right: 900, Bottom: 800}
	baseline := transparentNode(1, 0, 1, 12, bounds)
	if !transparentWrapper(&baseline) {
		t.Fatal("baseline wrapper must be transparent")
	}

	cases := map[string]func(*device.RawNode){
		"root":                  func(node *device.RawNode) { node.Parent = -1 },
		"text_presence_missing": func(node *device.RawNode) { node.TextSet = false },
		"text_nonempty":         func(node *device.RawNode) { node.Text = "badge" },
		"desc_presence_missing": func(node *device.RawNode) { node.DescSet = false },
		"desc_nonempty":         func(node *device.RawNode) { node.Description = "slot" },
		"resource_missing":      func(node *device.RawNode) { node.ResourceSet = false },
		"resource_nonempty":     func(node *device.RawNode) { node.ResourceID = "holder" },
		"editable":              func(node *device.RawNode) { node.Editable = true },
		"clickable_unknown":     func(node *device.RawNode) { node.HasClickable = false },
		"clickable_true":        func(node *device.RawNode) { node.Clickable = true },
		"long_click_unknown":    func(node *device.RawNode) { node.HasLongClick = false },
		"long_click_true":       func(node *device.RawNode) { node.LongClickable = true },
		"scrollable_unknown":    func(node *device.RawNode) { node.HasScrollable = false },
		"scrollable_true":       func(node *device.RawNode) { node.Scrollable = true },
		"password_unknown":      func(node *device.RawNode) { node.HasPassword = false },
		"password_true":         func(node *device.RawNode) { node.Password = true },
		"checkable_unknown":     func(node *device.RawNode) { node.HasCheckable = false },
		"checkable_true":        func(node *device.RawNode) { node.Checkable = true },
		"enabled_unknown":       func(node *device.RawNode) { node.HasEnabled = false },
		"enabled_false":         func(node *device.RawNode) { node.Enabled = false },
		"focused_unknown":       func(node *device.RawNode) { node.HasFocused = false },
		"focused_true":          func(node *device.RawNode) { node.Focused = true },
		"selected_unknown":      func(node *device.RawNode) { node.HasSelected = false },
		"selected_true":         func(node *device.RawNode) { node.Selected = true },
		"checked_unknown":       func(node *device.RawNode) { node.HasChecked = false },
		"checked_true":          func(node *device.RawNode) { node.Checked = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			node := baseline
			mutate(&node)
			if transparentWrapper(&node) {
				t.Fatalf("wrapper mutation %s was accepted as transparent", name)
			}
		})
	}
	if transparentWrapper(nil) {
		t.Fatal("nil wrapper was accepted")
	}
}

func tierOriginalCapture(display device.Display) device.Capture {
	root := revalRoot(display.ID, 1)
	anchor := transparentNode(1, 0, 1, display.ID,
		device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 700}, 2)
	anchor.ResourceID = "tier_anchor"
	target := revalButton(2, 1, 2, display.ID,
		device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300},
		"Continue", "step", "continue_btn")
	target.TextSet, target.DescSet, target.ResourceSet = true, true, true
	target.Checkable = false
	target.Checked = false
	return revalCapture(display, root, anchor, target)
}

func tierTarget(index, parent, depth int, displayID int, bounds device.Rect,
	label string, enabled bool) device.RawNode {
	target := revalButton(index, parent, depth, displayID, bounds,
		label, "step", "continue_btn")
	target.TextSet, target.DescSet, target.ResourceSet = true, true, true
	target.Enabled = enabled
	target.Checkable = false
	target.Checked = false
	return target
}

func tierFreshCapture(display device.Display, kind string) (device.Capture, int) {
	root := revalRoot(display.ID, 1)
	anchor := transparentNode(1, 0, 1, display.ID,
		device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 700}, 2)
	anchor.ResourceID = "tier_anchor"
	originalBounds := device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}
	shiftedBounds := device.Rect{Left: 100, Top: 208, Right: 300, Bottom: 308}
	switch kind {
	case "tier1_unique":
		return revalCapture(display, root, anchor,
			tierTarget(2, 1, 2, display.ID, originalBounds, "Continue", true)), 2
	case "tier2_unique":
		return revalCapture(display, root, anchor,
			tierTarget(2, 1, 2, display.ID, shiftedBounds, "Continue", true)), 2
	case "tier3_unique":
		anchor.Children = []int{2}
		wrapper := transparentNode(2, 1, 2, display.ID,
			device.Rect{Left: 90, Top: 190, Right: 310, Bottom: 310}, 3)
		return revalCapture(display, root, anchor, wrapper,
			tierTarget(3, 2, 3, display.ID, originalBounds, "Continue", true)), 3
	case "tier1_ambiguous":
		anchor.Children = []int{2, 3}
		return revalCapture(display, root, anchor,
			tierTarget(2, 1, 2, display.ID, originalBounds, "Continue", true),
			tierTarget(3, 1, 2, display.ID, originalBounds, "Continue", true)), 2
	case "tier2_ambiguous":
		anchor.Children = []int{2, 3}
		return revalCapture(display, root, anchor,
			tierTarget(2, 1, 2, display.ID, shiftedBounds, "Continue", true),
			tierTarget(3, 1, 2, display.ID, shiftedBounds, "Continue", true)), 2
	case "tier3_ambiguous":
		anchor.Children = []int{2}
		wrapper := transparentNode(2, 1, 2, display.ID,
			device.Rect{Left: 90, Top: 190, Right: 310, Bottom: 310}, 3, 4)
		return revalCapture(display, root, anchor, wrapper,
			tierTarget(3, 2, 3, display.ID, originalBounds, "Continue", true),
			tierTarget(4, 2, 3, display.ID, originalBounds, "Continue", true)), 3
	case "changed_text":
		return revalCapture(display, root, anchor,
			tierTarget(2, 1, 2, display.ID, originalBounds, "Share", true)), 2
	case "changed_enabled":
		return revalCapture(display, root, anchor,
			tierTarget(2, 1, 2, display.ID, originalBounds, "Continue", false)), 2
	default:
		panic("unknown tier fixture")
	}
}

func assertTierMatch(t *testing.T, original, fresh device.Capture,
	wantIndex, candidateIndex int, wantState actProbeState, wantTier int) actMatch {
	t.Helper()
	match, state := matchActTarget(
		context.Background(), nil, &original, &fresh, &original.Nodes[wantIndex])
	if state != wantState {
		t.Fatalf("match state = %v, want %v", state, wantState)
	}
	if state == actProbeUnique {
		if match.tier != wantTier {
			t.Fatalf("match tier = %d, want %d", match.tier, wantTier)
		}
		if indexOf(&fresh, match.raw) != candidateIndex {
			t.Fatalf("candidate index = %d, want %d",
				indexOf(&fresh, match.raw), candidateIndex)
		}
	} else if wantTier != 0 && match.stats.tiers != wantTier {
		t.Fatalf("terminal tier = %d, want %d", match.stats.tiers, wantTier)
	}
	perMatcherLimit := len(original.Nodes) + len(fresh.Nodes)
	if match.stats.candidateVisits > match.stats.tiers*perMatcherLimit {
		t.Fatalf("candidate visits = %d exceed %d tiers * %d",
			match.stats.candidateVisits, match.stats.tiers, perMatcherLimit)
	}
	return match
}

func TestActRevalidationTierMatrix(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	original := tierOriginalCapture(display)

	t.Run("tier1_unique", func(t *testing.T) {
		fresh, candidate := tierFreshCapture(display, "tier1_unique")
		assertTierMatch(t, original, fresh, 2, candidate, actProbeUnique, 1)
	})
	t.Run("tier2_unique_after_tier1_none", func(t *testing.T) {
		fresh, candidate := tierFreshCapture(display, "tier2_unique")
		assertTierMatch(t, original, fresh, 2, candidate, actProbeUnique, 2)
	})
	t.Run("tier3_unique_after_l1_difference", func(t *testing.T) {
		fresh, candidate := tierFreshCapture(display, "tier3_unique")
		assertTierMatch(t, original, fresh, 2, candidate, actProbeUnique, 3)
	})
	t.Run("tier1_ambiguous", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier1_ambiguous")
		assertTierMatch(t, original, fresh, 2, -1, actProbeAmbiguous, 1)
	})
	t.Run("tier2_ambiguous", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier2_ambiguous")
		assertTierMatch(t, original, fresh, 2, -1, actProbeAmbiguous, 2)
	})
	t.Run("tier3_ambiguous", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier3_ambiguous")
		assertTierMatch(t, original, fresh, 2, -1, actProbeAmbiguous, 3)
	})
	// Target structure conflicts rule out every edge in Tiers 1–3 and 5.
	// With no lineage mismatch, Tier 4 is skipped and Tier 5 is the last scan.
	t.Run("target_text_change_rejected", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "changed_text")
		assertTierMatch(t, original, fresh, 2, -1, actProbeNone, 5)
	})
	t.Run("known_state_change_rejected", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "changed_enabled")
		assertTierMatch(t, original, fresh, 2, -1, actProbeNone, 5)
	})
}

func TestSameScrollScopeBoundaries(t *testing.T) {
	verticalScreen := device.Rect{Left: 0, Top: 0, Right: 3000, Bottom: 4000}
	horizontalScreen := device.Rect{Left: 0, Top: 0, Right: 4000, Bottom: 3000}
	cases := []struct {
		name      string
		oldRect   device.Rect
		freshRect device.Rect
		oldScreen device.Rect
		newScreen device.Rect
		direction string
		want      bool
	}{
		{
			name:      "vertical_same",
			oldRect:   device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 2200},
			freshRect: device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 2200},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: true,
		},
		{
			name:      "vertical_exact_half_axial_overlap",
			oldRect:   device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 2200},
			freshRect: device.Rect{Left: 100, Top: 1200, Right: 500, Bottom: 3200},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: true,
		},
		{
			name:      "vertical_below_half_axial_overlap",
			oldRect:   device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 2200},
			freshRect: device.Rect{Left: 100, Top: 1201, Right: 500, Bottom: 3201},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: false,
		},
		{
			name:      "vertical_cross_axis_at_eight_px",
			oldRect:   device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 2200},
			freshRect: device.Rect{Left: 108, Top: 200, Right: 508, Bottom: 2200},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: true,
		},
		{
			name:      "vertical_cross_axis_over_eight_px",
			oldRect:   device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 2200},
			freshRect: device.Rect{Left: 109, Top: 200, Right: 509, Bottom: 2200},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: false,
		},
		{
			name:      "vertical_cross_axis_two_percent",
			oldRect:   device.Rect{Left: 0, Top: 200, Right: 2000, Bottom: 2200},
			freshRect: device.Rect{Left: 40, Top: 200, Right: 2040, Bottom: 2200},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: true,
		},
		{
			name:      "vertical_cross_axis_above_two_percent",
			oldRect:   device.Rect{Left: 0, Top: 200, Right: 2000, Bottom: 2200},
			freshRect: device.Rect{Left: 41, Top: 200, Right: 2041, Bottom: 2200},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: false,
		},
		{
			name:      "horizontal_exact_half_axial_overlap",
			oldRect:   device.Rect{Left: 200, Top: 100, Right: 2200, Bottom: 500},
			freshRect: device.Rect{Left: 1200, Top: 100, Right: 3200, Bottom: 500},
			oldScreen: horizontalScreen, newScreen: horizontalScreen,
			direction: "right", want: true,
		},
		{
			name:      "horizontal_cross_axis_at_eight_px",
			oldRect:   device.Rect{Left: 200, Top: 100, Right: 2200, Bottom: 500},
			freshRect: device.Rect{Left: 200, Top: 108, Right: 2200, Bottom: 508},
			oldScreen: horizontalScreen, newScreen: horizontalScreen,
			direction: "left", want: true,
		},
		{
			name:      "horizontal_cross_axis_over_eight_px",
			oldRect:   device.Rect{Left: 200, Top: 100, Right: 2200, Bottom: 500},
			freshRect: device.Rect{Left: 200, Top: 109, Right: 2200, Bottom: 509},
			oldScreen: horizontalScreen, newScreen: horizontalScreen,
			direction: "left", want: false,
		},
		{
			name:      "empty_rect",
			oldRect:   device.Rect{},
			freshRect: device.Rect{Left: 0, Top: 0, Right: 10, Bottom: 10},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "down", want: false,
		},
		{
			name:      "invalid_direction",
			oldRect:   device.Rect{Left: 0, Top: 0, Right: 500, Bottom: 500},
			freshRect: device.Rect{Left: 0, Top: 0, Right: 500, Bottom: 500},
			oldScreen: verticalScreen, newScreen: verticalScreen,
			direction: "sideways", want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sameScrollScope(
				tc.oldRect, tc.freshRect, tc.oldScreen, tc.newScreen, tc.direction)
			if got != tc.want {
				t.Fatalf("sameScrollScope = %v, want %v", got, tc.want)
			}
		})
	}
}

func textBindingCapture(display device.Display, nodes ...device.RawNode) device.Capture {
	return revalCapture(display, nodes...)
}

func textBindingWant(display device.Display, bounds device.Rect) device.RawNode {
	return revalEditText(1, 0, 1, display.ID, bounds, "",
		"search field", "search_box", false)
}

func textBindingFresh(display device.Display, bounds device.Rect) device.RawNode {
	return revalEditText(1, 0, 1, display.ID, bounds, "",
		"search field", "search_box", true)
}

func TestTextBindingModesAndSourceGates(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	wantBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	tapX, tapY := 500, 350

	t.Run("exact", func(t *testing.T) {
		want := textBindingWant(display, wantBounds)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1),
			textBindingFresh(display, wantBounds))
		binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1], original.Foreground, tapX, tapY)
		if !ok || binding.mode != textExact {
			t.Fatalf("exact binding = %+v ok=%v", binding, ok)
		}
	})

	t.Run("near", func(t *testing.T) {
		nearBounds := device.Rect{Left: 110, Top: 310, Right: 910, Bottom: 410}
		want := textBindingWant(display, wantBounds)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1),
			textBindingFresh(display, nearBounds))
		binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1], original.Foreground, tapX, tapY)
		if !ok || binding.mode != textNear {
			t.Fatalf("near binding = %+v ok=%v", binding, ok)
		}
	})

	t.Run("shifted", func(t *testing.T) {
		shiftedBounds := device.Rect{Left: 100, Top: 800, Right: 900, Bottom: 900}
		want := textBindingWant(display, wantBounds)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1),
			textBindingFresh(display, shiftedBounds))
		binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1], original.Foreground, tapX, tapY)
		if !ok || binding.mode != textShifted {
			t.Fatalf("shifted binding = %+v ok=%v", binding, ok)
		}
	})

	for name, mutate := range map[string]func(*device.RawNode){
		"package":            func(node *device.RawNode) { node.Package = "com.other" },
		"class":              func(node *device.RawNode) { node.ClassName = "android.widget.TextView" },
		"description_value":  func(node *device.RawNode) { node.Description = "other" },
		"description_absent": func(node *device.RawNode) { node.DescSet = false },
		"resource_value":     func(node *device.RawNode) { node.ResourceID = "other_box" },
		"resource_absent":    func(node *device.RawNode) { node.ResourceSet = false },
		"horizontal_width": func(node *device.RawNode) {
			node.Bounds = device.Rect{Left: 100, Top: 800, Right: 500, Bottom: 900}
		},
	} {
		t.Run("source_mismatch_"+name, func(t *testing.T) {
			want := textBindingWant(display, wantBounds)
			original := textBindingCapture(display, revalRoot(display.ID, 1), want)
			freshNode := textBindingFresh(display,
				device.Rect{Left: 100, Top: 800, Right: 900, Bottom: 900})
			mutate(&freshNode)
			fresh := textBindingCapture(display, revalRoot(display.ID, 1), freshNode)
			if binding, ok := bindFocusedText(
				&original, &fresh, &original.Nodes[1],
				original.Foreground, tapX, tapY); ok {
				t.Fatalf("source mismatch %s bound as %+v", name, binding)
			}
		})
	}

	t.Run("task_switch", func(t *testing.T) {
		want := textBindingWant(display, wantBounds)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1),
			textBindingFresh(display, wantBounds))
		fresh.Foreground = revalForeground("com.example", 99)
		if binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1],
			original.Foreground, tapX, tapY); ok {
			t.Fatalf("task switch bound as %+v", binding)
		}
	})

	t.Run("same_task_same_signature_is_not_window_identity", func(t *testing.T) {
		// N36 capability boundary: Capture has no window token. A replacement on
		// the same task with every observable source signature, anchor and
		// geometry identical is indistinguishable, so choosing that exact
		// candidate is deterministic but is not proof of the same window/node.
		want := textBindingWant(display, wantBounds)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1),
			textBindingFresh(display, wantBounds))
		binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1],
			original.Foreground, tapX, tapY)
		if !ok || binding.mode != textExact ||
			binding.raw != &fresh.Nodes[1] {
			t.Fatalf("identical-signature replacement binding = %+v ok=%v",
				binding, ok)
		}
	})

	t.Run("anchor_counts_are_display_scoped", func(t *testing.T) {
		want := textBindingWant(display, wantBounds)
		foreignOriginal := revalEditText(2, 0, 1, display.ID+1, wantBounds,
			"", "search field", "search_box", false)
		original := textBindingCapture(
			display, revalRoot(display.ID, 1, 2), want, foreignOriginal)
		foreignFresh := revalEditText(2, 0, 1, display.ID+1, wantBounds,
			"", "search field", "search_box", true)
		fresh := textBindingCapture(
			display, revalRoot(display.ID, 1, 2),
			textBindingFresh(display, wantBounds), foreignFresh)
		binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1],
			original.Foreground, tapX, tapY)
		if !ok || binding.mode != textExact ||
			binding.raw != &fresh.Nodes[1] {
			t.Fatalf("cross-display anchor leaked into binding = %+v ok=%v",
				binding, ok)
		}
	})

	t.Run("duplicate_anchor_before_focus_filter", func(t *testing.T) {
		want := textBindingWant(display, wantBounds)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		focused := textBindingFresh(display, wantBounds)
		unfocused := revalEditText(2, 0, 1, display.ID,
			device.Rect{Left: 100, Top: 600, Right: 900, Bottom: 700},
			"", "search field", "search_box", false)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1, 2),
			focused, unfocused)
		if binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1],
			original.Foreground, tapX, tapY); ok {
			t.Fatalf("duplicate anchor bound as %+v", binding)
		}
	})

	t.Run("duplicate_anonymous_near_before_focus_filter", func(t *testing.T) {
		want := revalEditText(1, 0, 1, display.ID, wantBounds, "", "", "", false)
		original := textBindingCapture(display, revalRoot(display.ID, 1), want)
		focused := revalEditText(1, 0, 1, display.ID, wantBounds, "", "", "", true)
		unfocused := revalEditText(2, 0, 1, display.ID, wantBounds, "", "", "", false)
		fresh := textBindingCapture(display, revalRoot(display.ID, 1, 2),
			focused, unfocused)
		if binding, ok := bindFocusedText(
			&original, &fresh, &original.Nodes[1],
			original.Foreground, tapX, tapY); ok {
			t.Fatalf("duplicate anonymous near fields bound as %+v", binding)
		}
	})

	for name, mutate := range map[string]func(*device.RawNode){
		"want_enabled_unknown":  func(node *device.RawNode) { node.HasEnabled = false },
		"want_password_unknown": func(node *device.RawNode) { node.HasPassword = false },
		"want_password_true":    func(node *device.RawNode) { node.Password = true },
	} {
		t.Run(name, func(t *testing.T) {
			want := textBindingWant(display, wantBounds)
			mutate(&want)
			original := textBindingCapture(display, revalRoot(display.ID, 1), want)
			fresh := textBindingCapture(display, revalRoot(display.ID, 1),
				textBindingFresh(display, wantBounds))
			if binding, ok := bindFocusedText(
				&original, &fresh, &original.Nodes[1],
				original.Foreground, tapX, tapY); ok {
				t.Fatalf("%s bound as %+v", name, binding)
			}
		})
	}

	for name, mutate := range map[string]func(*device.RawNode){
		"fresh_focused_unknown":  func(node *device.RawNode) { node.HasFocused = false },
		"fresh_enabled_unknown":  func(node *device.RawNode) { node.HasEnabled = false },
		"fresh_password_unknown": func(node *device.RawNode) { node.HasPassword = false },
		"fresh_password_true":    func(node *device.RawNode) { node.Password = true },
	} {
		t.Run(name, func(t *testing.T) {
			want := textBindingWant(display, wantBounds)
			original := textBindingCapture(display, revalRoot(display.ID, 1), want)
			freshNode := textBindingFresh(display, wantBounds)
			mutate(&freshNode)
			fresh := textBindingCapture(display, revalRoot(display.ID, 1), freshNode)
			if binding, ok := bindFocusedText(
				&original, &fresh, &original.Nodes[1],
				original.Foreground, tapX, tapY); ok {
				t.Fatalf("%s bound as %+v", name, binding)
			}
		})
	}
}

func TestActRevalidationBudgetAndCancellationClassification(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	original := tierOriginalCapture(display)

	t.Run("cancelled_before_scan", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier1_unique")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, state := matchActTargetWithLimits(
			ctx, nil, &original, &fresh, &original.Nodes[2], defaultActMatchLimits())
		if state != actProbeCancelled {
			t.Fatalf("state = %v, want cancelled", state)
		}
	})

	t.Run("cancelled_during_candidate_scan", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier1_unique")
		ctx := &revalCountingContext{Context: context.Background(), cancelAt: 3}
		match, state := matchActTargetWithLimits(
			ctx, nil, &original, &fresh, &original.Nodes[2], defaultActMatchLimits())
		if state != actProbeCancelled {
			t.Fatalf("state = %v, want cancelled", state)
		}
		if match.stats.candidateVisits > 1 {
			t.Fatalf("cancelled scan visited %d candidates, want at most one",
				match.stats.candidateVisits)
		}
	})

	t.Run("revoked_after_unique_before_return", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier1_unique")
		calls := 0
		revoked := func() bool {
			calls++
			return calls >= 2
		}
		_, state := matchActTargetWithLimits(
			context.Background(), revoked, &original, &fresh,
			&original.Nodes[2], defaultActMatchLimits())
		if state != actProbeCancelled {
			t.Fatalf("state = %v, want cancelled after unique scan", state)
		}
	})

	t.Run("candidate_budget_stops_before_next_tier", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "changed_text")
		match, state := matchActTargetWithLimits(
			context.Background(), nil, &original, &fresh, &original.Nodes[2],
			actMatchLimits{candidateLimit: 1})
		if state != actProbeBudgetExhausted {
			t.Fatalf("state = %v, want budget exhausted", state)
		}
		if match.stats.tiers != 1 {
			t.Fatalf("tiers = %d, want no fallback after budget", match.stats.tiers)
		}
	})

	t.Run("lineage_budget_stops_before_next_tier", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier1_unique")
		match, state := matchActTargetWithLimits(
			context.Background(), nil, &original, &fresh, &original.Nodes[2],
			actMatchLimits{lineageLimit: 1})
		if state != actProbeBudgetExhausted {
			t.Fatalf("state = %v, want budget exhausted", state)
		}
		if match.stats.tiers != 1 {
			t.Fatalf("tiers = %d, want no fallback after budget", match.stats.tiers)
		}
	})

	t.Run("malformed_parent_cycle", func(t *testing.T) {
		fresh, _ := tierFreshCapture(display, "tier1_unique")
		fresh.Nodes[2].Parent = 2
		_, state := matchActTargetWithLimits(
			context.Background(), nil, &original, &fresh,
			&original.Nodes[2], defaultActMatchLimits())
		if state != actProbeMalformed {
			t.Fatalf("state = %v, want malformed", state)
		}
	})
}

func TestActRevalidationCandidateVisitBound(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	const count = 64
	build := func(label string) device.Capture {
		children := make([]int, 0, count)
		nodes := make([]device.RawNode, 0, count+1)
		nodes = append(nodes, revalRoot(display.ID))
		for index := 1; index <= count; index++ {
			children = append(children, index)
			bounds := device.Rect{Left: 10, Top: 10, Right: 300, Bottom: 100}
			if index == count {
				bounds = device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}
			}
			node := revalButton(index, 0, 1, display.ID, bounds,
				label, "", "visit_target")
			node.TextSet, node.DescSet, node.ResourceSet = true, true, true
			nodes = append(nodes, node)
		}
		nodes[0].Children = children
		return revalCapture(display, nodes...)
	}
	original := build("Other")
	target := original.Nodes[count]
	target.Text = "Target"
	original.Nodes[count] = target
	fresh := build("Other")
	freshTarget := fresh.Nodes[count]
	freshTarget.Text = "Target"
	fresh.Nodes[count] = freshTarget

	match, state := matchActTarget(
		context.Background(), nil, &original, &fresh, &original.Nodes[count])
	if state != actProbeUnique || match.tier != 1 {
		t.Fatalf("state=%v tier=%d, want unique Tier 1", state, match.tier)
	}
	limit := len(original.Nodes) + len(fresh.Nodes)
	if match.stats.candidateVisits > limit {
		t.Fatalf("candidate visits = %d, want <= %d", match.stats.candidateVisits, limit)
	}
	if match.stats.candidateVisits != limit {
		t.Fatalf("candidate visits = %d, want one outgoing and one incoming scan = %d",
			match.stats.candidateVisits, limit)
	}
}
