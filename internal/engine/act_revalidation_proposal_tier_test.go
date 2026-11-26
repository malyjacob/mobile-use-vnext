package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

func TestRevalidateSyntheticDisplayRootNormalization(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400}
	original := tierOriginalCapture(display)
	original.Nodes[2].Text, original.Nodes[2].Description, original.Nodes[2].ResourceID = "", "", ""
	fresh := tierOriginalCapture(display)
	fresh.Nodes[0].Parent = 4
	for i := range fresh.Nodes {
		fresh.Nodes[i].Depth++
	}
	target := &fresh.Nodes[2]
	target.Text, target.Description, target.ResourceID = "", "", ""
	target.Bounds.Top += 35
	target.Bounds.Bottom += 35
	target.RawBounds = target.Bounds
	otherWindow := revalRoot(display.ID)
	otherWindow.Index, otherWindow.Parent, otherWindow.Depth = 3, 4, 1
	otherWindow.Package = "com.android.systemui"
	fresh.Nodes = append(fresh.Nodes, otherWindow, device.RawNode{
		Index: 4, Parent: -1, Children: []int{0, 3}, DisplayID: display.ID, Visible: true,
		Bounds: display.Bounds(), RawBounds: display.Bounds(), HasEnabled: true, Enabled: true,
	})
	fresh.Root = 4

	match := assertTierMatch(t, original, fresh, 2, 2, actProbeUnique, 2)
	if match.independentContent || match.independentSubtree {
		t.Fatal("fixture unexpectedly carries independent semantic evidence")
	}
	lineage := func(capture *device.Capture, index int) ([]lineageEntry, int) {
		t.Helper()
		matcher := newTierMatcher(context.Background(), &original, &fresh, 2, defaultActMatchOptions())
		chain, ok := lineageL1(capture, index, &matcher.budget)
		if !ok {
			t.Fatal("lineage walk failed")
		}
		return chain, matcher.budget.lineageSteps
	}
	oldLine, _ := lineage(&original, 2)
	freshLine, steps := lineage(&fresh, 2)
	if len(oldLine) != 2 || len(freshLine) != len(oldLine) || !freshLine[1].root || steps != 3 {
		t.Fatalf("lineage lengths = %d/%d, steps = %d, want 2/2 and 3", len(oldLine), len(freshLine), steps)
	}
	for _, index := range []int{0, 4} {
		if chain, _ := lineage(&fresh, index); len(chain) != 0 {
			t.Fatalf("root-level target %d has %d ancestors, want zero", index, len(chain))
		}
	}

	// Reporting even a false selected state disqualifies the root. This changes
	// no lineage key fields and lets the extra structural level be observed.
	fresh.Nodes[4].HasSelected = true
	control, _ := lineage(&fresh, 2)
	if len(control) != len(oldLine)+1 {
		t.Fatalf("unnormalized lineage length = %d, want %d", len(control), len(oldLine)+1)
	}
	assertTierMatch(t, original, fresh, 2, -1, actProbeNone, 5)
}

func TestRevalidateTier3AncestorNoiseToleranceAndBoundaries(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400}
	original := tierOriginalCapture(display)
	original.Nodes[1].Text = "Old heading"
	original.Nodes[1].Description = "Old summary"
	fresh := tierOriginalCapture(display)
	fresh.Nodes[1].Text = "New heading"
	fresh.Nodes[1].Description = "New summary"
	fresh.Nodes[1].Checked = true
	assertTierMatch(t, original, fresh, 2, 2, actProbeUnique, 3)

	t.Run("target_content_is_strict", func(t *testing.T) {
		fresh.Nodes[2].Text = "Cancel"
		assertTierMatch(t, original, fresh, 2, -1, actProbeNone, 5)
	})

	t.Run("unknown_wrapper_state_is_retained", func(t *testing.T) {
		old := tierOriginalCapture(display)
		wrapped, index := tierFreshCapture(display, "tier3_unique")
		assertTierMatch(t, old, wrapped, 2, index, actProbeUnique, 3)
		wrapped.Nodes[2].HasChecked = false
		matcher := newTierMatcher(context.Background(), &old, &wrapped, 3, defaultActMatchOptions())
		probe := matcher.probeWant(2)
		if probe.state != actProbeNone || !probe.lineageMismatch {
			t.Fatalf("Tier 3 probe = %v, mismatch = %v, want none with lineage mismatch", probe.state, probe.lineageMismatch)
		}
	})

	t.Run("text_only_ancestor_is_not_a_strong_anchor", func(t *testing.T) {
		old := tierOriginalCapture(display)
		newFrame := tierOriginalCapture(display)
		old.Nodes[1].ResourceID, newFrame.Nodes[1].ResourceID = "", ""
		old.Nodes[1].Text, newFrame.Nodes[1].Text = "Old heading", "New heading"
		matcher := newTierMatcher(context.Background(), &old, &newFrame, 3, defaultActMatchOptions())
		probe := matcher.probeWant(2)
		if probe.state != actProbeNone || !probe.lineageMismatch {
			t.Fatalf("Tier 3 probe = %v, mismatch = %v, want none with lineage mismatch", probe.state, probe.lineageMismatch)
		}
	})
}

func TestRevalidateNumericSkeletonE3AndCarrierUnbind(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400}
	bounds := device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}
	capture := func(rebuilt bool, texts []string) device.Capture {
		root := revalRoot(display.ID, 1)
		if rebuilt {
			root.ClassName = "android.widget.LinearLayout"
		}
		nodes := []device.RawNode{root, revalButton(1, 0, 1, display.ID, bounds, "", "", "")}
		for _, text := range texts {
			index := len(nodes)
			child := transparentNode(index, 1, 2, display.ID, bounds)
			child.ClassName, child.Text = "android.widget.TextView", text
			nodes[1].Children = append(nodes[1].Children, index)
			nodes = append(nodes, child)
		}
		return revalCapture(display, nodes...)
	}
	for _, tc := range []struct {
		name       string
		old, fresh []string
		evidence   bool
	}{
		{"timestamp_and_removed_badge", []string{"订单", "2分钟前", "10"}, []string{"订单", "3分钟前"}, true},
		{"timestamp_and_added_badge", []string{"订单", "2分钟前"}, []string{"99+", "3分钟前", "订单"}, true},
		{"exact_multiset", []string{"10"}, []string{"10"}, true},
		{"numeric_replacement", []string{"10"}, []string{"11"}, false},
		{"business_word_change", []string{"操作", "确认"}, []string{"操作", "取消"}, false},
		{"different_list_card", []string{"商品A", "加入购物车"}, []string{"商品B", "加入购物车"}, false},
		{"no_digit_free_anchor", []string{"2分钟前", "第١页"}, []string{"3分钟前", "第١页"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := capture(false, tc.old)
			fresh := capture(false, tc.fresh)
			match := assertTierMatch(t, original, fresh, 1, 1, actProbeUnique, 1)
			if match.independentContent || match.independentSubtree != tc.evidence {
				t.Fatalf("carrier unbind evidence: content = %v, subtree = %v, want false/%v",
					match.independentContent, match.independentSubtree, tc.evidence)
			}

			rebuilt := capture(true, tc.fresh)
			if !tc.evidence {
				match := assertTierMatch(t, original, rebuilt, 1, -1, actProbeNone, 5)
				if match.independentSubtree {
					t.Fatal("rejected target returned subtree evidence")
				}
				return
			}
			match = assertTierMatch(t, original, rebuilt, 1, 1, actProbeUnique, 4)
			if !match.independentSubtree {
				t.Fatal("Tier 4 match did not return subtree evidence")
			}
			if !sameReadableMultiset(tc.old, tc.fresh) {
				_, state := matchKeyboardShiftedSubmitNode(context.Background(), nil,
					&original, &rebuilt, &original.Nodes[1], false, defaultActMatchLimits())
				if state != actProbeNone {
					t.Fatalf("keyboard shift accepted skeleton-only evidence, state = %v", state)
				}
			}
		})
	}
}

func TestRevalidateTier5HorizontalBandTranslationAndGuards(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400}
	const wantIndex = 2
	shifted := device.Rect{Left: 100, Top: 270, Right: 300, Bottom: 370}
	move := func(node *device.RawNode, bounds device.Rect) {
		node.Bounds, node.RawBounds = bounds, bounds
	}
	duplicate := func(frame *device.Capture) {
		node := frame.Nodes[wantIndex]
		node.Index = len(frame.Nodes)
		frame.Nodes = append(frame.Nodes, node)
		frame.Nodes[1].Children = []int{wantIndex, node.Index}
	}
	for _, tc := range []struct {
		name     string
		mutate   func(old, fresh *device.Capture)
		options  actMatchOptions
		want     actProbeState
		lastTier int
	}{
		{name: "vertical_translation", want: actProbeUnique, lastTier: 5},
		{name: "anchored_denoised_lineage", mutate: func(_, fresh *device.Capture) {
			fresh.Nodes[1].Text = "New heading"
		}, want: actProbeUnique, lastTier: 5},
		{name: "out_of_band", mutate: func(_, fresh *device.Capture) {
			out := shifted
			out.Left += 9
			out.Right += 9
			move(&fresh.Nodes[wantIndex], out)
		}, want: actProbeNone, lastTier: 5},
		{name: "forward_ambiguity", mutate: func(_, fresh *device.Capture) {
			duplicate(fresh)
		}, want: actProbeAmbiguous, lastTier: 5},
		{name: "reverse_ambiguity", mutate: func(old, _ *device.Capture) {
			duplicate(old)
		}, want: actProbeAmbiguous, lastTier: 5},
		{name: "earlier_tier_ambiguity_stops", mutate: func(old, fresh *device.Capture) {
			move(&fresh.Nodes[wantIndex], old.Nodes[wantIndex].Bounds)
			duplicate(fresh)
		}, want: actProbeAmbiguous, lastTier: 1},
		{name: "translation_disabled", options: actMatchOptions{disableTranslation: true}, want: actProbeNone, lastTier: 3},
		{name: "without_translation", options: defaultActMatchOptions().withoutTranslation(), want: actProbeNone, lastTier: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, fresh := tierOriginalCapture(display), tierOriginalCapture(display)
			move(&fresh.Nodes[wantIndex], shifted)
			if tc.mutate != nil {
				tc.mutate(&original, &fresh)
			}
			match, state := matchActTargetWithOptions(context.Background(), nil,
				&original, &fresh, &original.Nodes[wantIndex], tc.options)
			if state != tc.want || match.stats.tiers != tc.lastTier {
				t.Fatalf("state = %v, last tier = %d, want %v/%d", state, match.stats.tiers, tc.want, tc.lastTier)
			}
			if state == actProbeUnique && (match.tier != 5 || match.raw != &fresh.Nodes[wantIndex] ||
				match.raw.Bounds != shifted || !match.independentContent || !match.independentSubtree) {
				t.Fatal("Tier 5 did not return the translated target and both evidence flags")
			}
		})
	}
}
