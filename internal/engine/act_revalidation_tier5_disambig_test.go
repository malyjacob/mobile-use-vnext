package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The Tier 2/3 container-disambiguation fixtures reproduce the "feed of
// isomorphic text-less cards" shape of proposal §8.1 row 2B-A: every card shares
// one structure key, one lineage and no readable text of its own, while the
// readable title and author live in its children. One short card overlaps the
// long target card by more than half of its own area across the two frames, so
// the untouched Tier 2 scan reports a multi-candidate collision.
const (
	tier5DisambigCardResource = "epoxy_model_group_child_container"
	tier5DisambigTitle        = "被肥鱼神力吓哭了"
	tier5DisambigAuthor       = "Ken20020702"
)

// tier5DisambigFrame builds one synthetic capture in preorder. Nodes are
// appended through addNode so every index, parent and depth stays consistent.
type tier5DisambigFrame struct {
	display device.Display
	nodes   []device.RawNode
}

func newTier5DisambigFrame(display device.Display) *tier5DisambigFrame {
	frame := &tier5DisambigFrame{display: display}
	root := revalRoot(display.ID)
	root.Children = nil
	frame.nodes = append(frame.nodes, root)
	frame.container(0, "feed_recycler", display.Bounds())
	return frame
}

func tier5DisambigRect(top, height int) device.Rect {
	return device.Rect{Left: 100, Top: top, Right: 1000, Bottom: top + height}
}

// addNode appends one node below parent and returns its final index.
func (frame *tier5DisambigFrame) addNode(parent int, node device.RawNode) int {
	index := len(frame.nodes)
	node.Index, node.Parent = index, parent
	node.Depth = frame.nodes[parent].Depth + 1
	frame.nodes[parent].Children = append(frame.nodes[parent].Children, index)
	frame.nodes = append(frame.nodes, node)
	return index
}

// container appends one plain transparent container. An empty resource keeps it
// a provably transparent wrapper for the denoised lineage.
func (frame *tier5DisambigFrame) container(parent int, resource string,
	bounds device.Rect) int {
	node := transparentNode(0, 0, 0, frame.display.ID, bounds)
	if resource != "" {
		node.ResourceSet, node.ResourceID = true, resource
	}
	return frame.addNode(parent, node)
}

// card appends one clickable card with no readable text of its own and a title
// and author child.
func (frame *tier5DisambigFrame) card(parent int, bounds device.Rect,
	title, author string) int {
	card := transparentNode(0, 0, 0, frame.display.ID, bounds)
	card.ResourceSet, card.ResourceID = true, tier5DisambigCardResource
	card.HasClickable, card.Clickable = true, true
	index := frame.addNode(parent, card)
	frame.text(index, bounds, title, 40, 80)
	frame.text(index, bounds, author, 90, 130)
	return index
}

func (frame *tier5DisambigFrame) text(parent int, bounds device.Rect,
	value string, top, bottom int) int {
	node := transparentNode(0, 0, 0, frame.display.ID, device.Rect{
		Left: bounds.Left + 40, Top: bounds.Top + top,
		Right: bounds.Right - 40, Bottom: bounds.Top + bottom,
	})
	node.ClassName = "android.widget.TextView"
	node.TextSet, node.Text = true, value
	return frame.addNode(parent, node)
}

func (frame *tier5DisambigFrame) capture() device.Capture {
	return revalCapture(frame.display, frame.nodes...)
}

// tier5DisambigAmbiguousPair is the baseline-collision pair: three isomorphic
// long cards whose list moved up 154px plus the short card that still overlaps
// the long target by more than half of its own area.
func tier5DisambigAmbiguousPair() (device.Capture, device.Capture, int, int) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400}
	original := newTier5DisambigFrame(display)
	original.card(1, tier5DisambigRect(200, 700), "上方推荐标题", "上方作者")
	targetIndex := original.card(1, tier5DisambigRect(900, 700),
		tier5DisambigTitle, tier5DisambigAuthor)
	original.card(1, tier5DisambigRect(1600, 700), "下方推荐标题", "下方作者")
	original.card(1, tier5DisambigRect(1300, 400), "贴边短卡标题", "贴边短卡作者")

	fresh := newTier5DisambigFrame(display)
	fresh.card(1, tier5DisambigRect(46, 700), "上方推荐标题", "上方作者")
	freshTargetIndex := fresh.card(1, tier5DisambigRect(746, 700),
		tier5DisambigTitle, tier5DisambigAuthor)
	fresh.card(1, tier5DisambigRect(1446, 700), "下方推荐标题", "下方作者")
	fresh.card(1, tier5DisambigRect(1300, 400), "贴边短卡标题", "贴边短卡作者")
	return original.capture(), fresh.capture(), targetIndex, freshTargetIndex
}

func tier5DisambigStateName(state actProbeState) string {
	switch state {
	case actProbeNone:
		return "none"
	case actProbeUnique:
		return "unique"
	case actProbeAmbiguous:
		return "ambiguous"
	case actProbeCancelled:
		return "cancelled"
	case actProbeBudgetExhausted:
		return "budget_exhausted"
	default:
		return "malformed"
	}
}

// TestRevalidateTier2AmbiguousContainerDisambiguatesInTier5 is the red-first row
// 2B-A: the baseline short-circuits the Tier 2 collision with target_ambiguous,
// while the intended policy hands the collision to the existing Tier 5 evidence
// scan and matches the translated card there.
func TestRevalidateTier2AmbiguousContainerDisambiguatesInTier5(t *testing.T) {
	original, fresh, targetIndex, freshTargetIndex := tier5DisambigAmbiguousPair()

	t.Run("baseline_tier2_collision", func(t *testing.T) {
		matcher := newTierMatcher(context.Background(), &original, &fresh, 2,
			defaultActMatchOptions())
		probe := matcher.probeWant(targetIndex)
		if probe.state != actProbeAmbiguous {
			t.Fatalf("Tier 2 probe = %s, want ambiguous",
				tier5DisambigStateName(probe.state))
		}
	})

	match, state := matchActTargetWithOptions(context.Background(), nil,
		&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
	if state != actProbeUnique {
		t.Fatalf("match state = %s after disambiguation, want unique",
			tier5DisambigStateName(state))
	}
	if match.tier != 5 || match.stats.tiers != 5 {
		t.Fatalf("match tier = %d (last tier %d), want 5",
			match.tier, match.stats.tiers)
	}
	if match.raw != &fresh.Nodes[freshTargetIndex] {
		t.Fatalf("matched candidate index = %d, want %d",
			indexOf(&fresh, match.raw), freshTargetIndex)
	}
	if !match.independentContent || !match.independentSubtree {
		t.Fatalf("Tier 5 evidence: content = %v, subtree = %v, want true/true",
			match.independentContent, match.independentSubtree)
	}
}

// TestRevalidateTier23AmbiguousSafetyGuards keeps every red line of proposal
// §8.1 row "2B 安全边界矩阵" intact.
func TestRevalidateTier23AmbiguousSafetyGuards(t *testing.T) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400}

	// (1) A card with its own readable text or a childless leaf stays rejected:
	// both have no subtree evidence left to disambiguate with.
	t.Run("1a_own_readable_text_still_ambiguous", func(t *testing.T) {
		original, fresh, targetIndex, _ := tier5DisambigAmbiguousPair()
		for index := range original.Nodes {
			if original.Nodes[index].ResourceID == tier5DisambigCardResource {
				original.Nodes[index].TextSet, original.Nodes[index].Text =
					true, "卡片自身文案"
			}
		}
		for index := range fresh.Nodes {
			if fresh.Nodes[index].ResourceID == tier5DisambigCardResource {
				fresh.Nodes[index].TextSet, fresh.Nodes[index].Text =
					true, "卡片自身文案"
			}
		}
		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
		if state != actProbeAmbiguous || match.stats.tiers != 2 {
			t.Fatalf("state = %s at tier %d, want ambiguous at tier 2",
				tier5DisambigStateName(state), match.stats.tiers)
		}
	})

	t.Run("1b_childless_leaf_still_ambiguous", func(t *testing.T) {
		originalFrame := newTier5DisambigFrame(display)
		targetIndex := originalFrame.container(1, "", tier5DisambigRect(900, 700))
		original := originalFrame.capture()
		freshFrame := newTier5DisambigFrame(display)
		freshFrame.container(1, "", tier5DisambigRect(746, 700))
		freshFrame.container(1, "", tier5DisambigRect(1300, 400))
		fresh := freshFrame.capture()
		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
		if state != actProbeAmbiguous || match.stats.tiers != 2 {
			t.Fatalf("state = %s at tier %d, want ambiguous at tier 2",
				tier5DisambigStateName(state), match.stats.tiers)
		}
	})

	// (2) Tier 1 ambiguity is never routed to Tier 5.
	t.Run("2_tier1_ambiguity_never_enters_tier5", func(t *testing.T) {
		originalFrame := newTier5DisambigFrame(display)
		targetIndex := originalFrame.card(1, tier5DisambigRect(900, 700),
			tier5DisambigTitle, tier5DisambigAuthor)
		original := originalFrame.capture()
		freshFrame := newTier5DisambigFrame(display)
		freshFrame.card(1, tier5DisambigRect(900, 700), tier5DisambigTitle, tier5DisambigAuthor)
		freshFrame.card(1, tier5DisambigRect(900, 700), "另一张卡片标题", "另一张卡片作者")
		fresh := freshFrame.capture()
		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
		if state != actProbeAmbiguous || match.stats.tiers != 1 {
			t.Fatalf("state = %s at tier %d, want ambiguous at tier 1",
				tier5DisambigStateName(state), match.stats.tiers)
		}
	})

	// (3) The write-submit and coordinate-owner policy (translation disabled) and
	// the scroll policy keep the immediate rejection.
	t.Run("3a_translation_disabled_still_ambiguous", func(t *testing.T) {
		original, fresh, targetIndex, _ := tier5DisambigAmbiguousPair()
		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex],
			defaultActMatchOptions().withoutTranslation())
		if state != actProbeAmbiguous || match.stats.tiers != 2 {
			t.Fatalf("state = %s at tier %d, want ambiguous at tier 2",
				tier5DisambigStateName(state), match.stats.tiers)
		}
	})

	t.Run("3b_scroll_mode_still_ambiguous", func(t *testing.T) {
		original, fresh, targetIndex, _ := tier5DisambigAmbiguousPair()
		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex],
			actMatchOptions{mode: actMatchScroll})
		if state != actProbeAmbiguous || match.stats.tiers != 2 {
			t.Fatalf("state = %s at tier %d, want ambiguous at tier 2",
				tier5DisambigStateName(state), match.stats.tiers)
		}
	})

	// (4) A Tier 5 hit still passes the tier>=4 whole-tree occlusion proof: an
	// external button covering the landing point blocks the frame as
	// scope_blocked, while the clear point is accepted.
	for _, tc := range []struct {
		name       string
		overlay    bool
		wantReason string
	}{
		{name: "4a_blocked_by_external_overlay", overlay: true,
			wantReason: targetChangedReasonScopeBlocked},
		{name: "4b_clear_landing_point", overlay: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, fresh, targetIndex, freshTargetIndex := tier5DisambigAmbiguousPair()
			if tc.overlay {
				overlayBounds := fresh.Nodes[freshTargetIndex].Bounds
				overlayIndex := len(fresh.Nodes)
				overlay := revalButton(overlayIndex, 1, 2, display.ID,
					overlayBounds, "浮层按钮", "", "overlay_btn")
				fresh.Nodes[1].Children = append(fresh.Nodes[1].Children, overlayIndex)
				fresh.Nodes = append(fresh.Nodes, overlay)
			}
			match, state := matchActTargetWithOptions(context.Background(), nil,
				&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
			if state != actProbeUnique || match.tier != 5 {
				t.Fatalf("state = %s at tier %d, want unique Tier 5",
					tier5DisambigStateName(state), match.tier)
			}
			projector := &testProjector{builder: observation.NewBuilder()}
			projection, err := projector.Preview(&fresh)
			if err != nil {
				t.Fatal(err)
			}
			view, apiErr := buildTapPublicView(projection.Tree, &fresh, projection.Source)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			topology, apiErr := validateTapRawTopology(&fresh)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			if check := checkTapSourceMapping(view, topology, freshTargetIndex); check != tapSourceValid {
				t.Fatalf("target source mapping = %v, want valid", check)
			}
			target := view.byRaw[freshTargetIndex]
			if target == nil {
				t.Fatal("matched card is missing from the public view")
			}
			input := &tapInput{view: view, capture: &fresh, topology: topology,
				target: target, targetRaw: &fresh.Nodes[freshTargetIndex],
				targetIndex: freshTargetIndex}
			scope, apiErr := collectTapScope(input)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			x, y := fresh.Nodes[freshTargetIndex].Bounds.Center()
			if occluded := !tapFrameTier4OcclusionProven(view, target, x, y); occluded != (tc.wantReason != "") {
				t.Fatalf("whole-tree occlusion proof = %v, want %v",
					!occluded, tc.wantReason == "")
			}
			plan := &tapPlan{action: "tap", x: x, y: y}
			frame, apiErr := (&Engine{}).resolveDirectTapScopeFrame(
				context.Background(), nil, nil, &fresh, topology, view, plan,
				match, target, freshTargetIndex, input, scope, tapFrameOptions{})
			if tc.wantReason != "" {
				if frame != nil || apiErr == nil ||
					apiErr.Code != protocol.CodeTargetChanged ||
					apiErr.Details["reason"] != tc.wantReason {
					t.Fatalf("frame=%+v error=%+v, want target_changed/%s",
						frame, apiErr, tc.wantReason)
				}
				return
			}
			if apiErr != nil || frame == nil || frame.x != x || frame.y != y {
				t.Fatalf("frame=%+v error=%+v, want the proven landing point",
					frame, apiErr)
			}
		})
	}

	// (5) Two cards whose readable subtrees are identical stay genuinely
	// ambiguous inside Tier 5.
	t.Run("5_identical_subtree_text_still_ambiguous", func(t *testing.T) {
		originalFrame := newTier5DisambigFrame(display)
		targetIndex := originalFrame.card(1, tier5DisambigRect(900, 700),
			tier5DisambigTitle, tier5DisambigAuthor)
		original := originalFrame.capture()
		freshFrame := newTier5DisambigFrame(display)
		freshFrame.card(1, tier5DisambigRect(46, 700), "上方推荐标题", "上方作者")
		freshFrame.card(1, tier5DisambigRect(746, 700), tier5DisambigTitle, tier5DisambigAuthor)
		freshFrame.card(1, tier5DisambigRect(1300, 400), "贴边短卡标题", "贴边短卡作者")
		freshFrame.card(1, tier5DisambigRect(1600, 700), tier5DisambigTitle, tier5DisambigAuthor)
		fresh := freshFrame.capture()
		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
		if state != actProbeAmbiguous || match.stats.tiers != 5 {
			t.Fatalf("state = %s at tier %d, want ambiguous at tier 5",
				tier5DisambigStateName(state), match.stats.tiers)
		}
	})

	// (6) A Tier 3 collision reached after a Tier 1/2 lineage mismatch that then
	// finds no Tier 5 evidence reports target_missing, never lineage_changed.
	t.Run("6_tier3_none_maps_to_target_missing", func(t *testing.T) {
		originalFrame := newTier5DisambigFrame(display)
		targetIndex := originalFrame.card(1, tier5DisambigRect(900, 700),
			tier5DisambigTitle, tier5DisambigAuthor)
		original := originalFrame.capture()

		freshFrame := newTier5DisambigFrame(display)
		wrapperA := freshFrame.container(1, "", tier5DisambigRect(746, 700))
		freshFrame.card(wrapperA, tier5DisambigRect(746, 700), "全新标题", "新作者")
		wrapperB := freshFrame.container(1, "", tier5DisambigRect(750, 700))
		freshFrame.card(wrapperB, tier5DisambigRect(750, 700), "另一张卡片标题", "另一张卡片作者")
		promo := freshFrame.container(0, "promo_recycler", display.Bounds())
		freshFrame.card(promo, tier5DisambigRect(746, 700), "推广标题", "推广作者")
		fresh := freshFrame.capture()

		match, state := matchActTargetWithOptions(context.Background(), nil,
			&original, &fresh, &original.Nodes[targetIndex], defaultActMatchOptions())
		if state != actProbeNone || match.stats.tiers != 5 {
			t.Fatalf("state = %s at tier %d, want none at tier 5",
				tier5DisambigStateName(state), match.stats.tiers)
		}
		if match.lineageMismatch {
			t.Fatal("Tier 5 miss kept the earlier lineage mismatch")
		}
		apiErr := probeTargetChangedError(state, match, targetChangedMessage)
		if apiErr == nil || apiErr.Code != protocol.CodeTargetChanged ||
			apiErr.Details["reason"] != targetChangedReasonTargetMissing {
			t.Fatalf("error=%+v, want target_changed/%s", apiErr,
				targetChangedReasonTargetMissing)
		}
		if ref, exists := apiErr.Details["changed_ref"]; exists {
			t.Fatalf("changed_ref = %#v, want the detail omitted", ref)
		}
	})
}

// TestRevalidateTier5DisambigAdmissionPredicate pins the four conditions of the
// admission predicate itself instead of only its routing effect.
func TestRevalidateTier5DisambigAdmissionPredicate(t *testing.T) {
	original, _, targetIndex, _ := tier5DisambigAmbiguousPair()
	container := &original.Nodes[targetIndex]
	withOwnText := *container
	withOwnText.TextSet, withOwnText.Text = true, "卡片自身文案"
	childless := *container
	childless.Children = nil
	tapLike := defaultActMatchOptions()

	for _, tc := range []struct {
		name    string
		want    *device.RawNode
		tier    int
		options actMatchOptions
		allowed bool
	}{
		{"tier2_tap_like_container", container, 2, tapLike, true},
		{"tier3_tap_like_container", container, 3, tapLike, true},
		{"tier1_never", container, 1, tapLike, false},
		{"tier4_never", container, 4, tapLike, false},
		{"own_readable_text", &withOwnText, 2, tapLike, false},
		{"childless_leaf", &childless, 2, tapLike, false},
		{"nil_want", nil, 2, tapLike, false},
		{"translation_disabled", container, 2, tapLike.withoutTranslation(), false},
		{"scroll_mode", container, 2, actMatchOptions{mode: actMatchScroll}, false},
		{"exact_evidence_mode", container, 2, tapLike.withExactTapLikeEvidence(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canDisambiguateViaTier5(tc.want, tc.tier, tc.options); got != tc.allowed {
				t.Fatalf("canDisambiguateViaTier5 = %v, want %v", got, tc.allowed)
			}
		})
	}
}
