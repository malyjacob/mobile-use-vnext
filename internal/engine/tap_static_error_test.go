package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The proposal's static-error taxonomy is exercised against the same synthetic
// tap scope harness as the baseline planner. The expected codes and keys are
// written as literals so the assertions compile against the pre-change
// implementation and fail on behavior, not on missing constants.

func requireStaticRejection(t *testing.T, result *protocol.Result,
	code, reason string) map[string]any {
	t.Helper()
	if result == nil || result.Outcome != OutcomeNotExecuted || result.Error == nil {
		t.Fatalf("result = %+v, want not_executed with an error", result)
	}
	if result.Error.Code != code {
		t.Fatalf("error code = %q, want %q (%+v)", result.Error.Code, code, result.Error)
	}
	details := result.Error.Details
	if details == nil {
		t.Fatalf("%s rejection carried no details", code)
	}
	if got := details["reason"]; got != reason {
		t.Fatalf("reason = %#v, want %q (details %#v)", got, reason, details)
	}
	return details
}

func requireDetailString(t *testing.T, details map[string]any, key, want string) {
	t.Helper()
	got, exists := details[key]
	if !exists {
		t.Fatalf("details lack %q: %#v", key, details)
	}
	if got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func requireDetailStringSlice(t *testing.T, details map[string]any, key string, want ...string) {
	t.Helper()
	got, exists := details[key]
	if !exists {
		t.Fatalf("details lack %q: %#v", key, details)
	}
	values, ok := got.([]string)
	if !ok {
		t.Fatalf("%s = %#v (%T), want []string", key, got, got)
	}
	if len(values) != len(want) {
		t.Fatalf("%s = %#v, want %#v", key, values, want)
	}
	for index := range want {
		if values[index] != want[index] {
			t.Fatalf("%s = %#v, want %#v", key, values, want)
		}
	}
}

func requireDetailAbsent(t *testing.T, details map[string]any, key string) {
	t.Helper()
	if value, exists := details[key]; exists {
		t.Fatalf("details unexpectedly carry %q = %#v", key, value)
	}
}

func tapCrossCoveredStep() captureStep {
	return tapCardWithChildrenStep(device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000},
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 400, Top: 0, Right: 600, Bottom: 1000},
			"android.widget.ImageButton", "vertical", true, true),
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 0, Top: 400, Right: 1000, Bottom: 600},
			"android.widget.ImageButton", "horizontal", true, true))
}

// tapCrossCoveredExternalStep keeps the card subtree blocked by the cross while
// an independent deeper sibling wins every free whole-tree point.
func tapCrossCoveredExternalStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := tapPageWithChildren(display,
			device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000},
			tapRawNode(0, 0, 0, 0, device.Rect{Left: 400, Top: 0, Right: 600, Bottom: 1000},
				"android.widget.ImageButton", "vertical", true, true),
			tapRawNode(0, 0, 0, 0, device.Rect{Left: 0, Top: 400, Right: 1000, Bottom: 600},
				"android.widget.ImageButton", "horizontal", true, true))
		bounds := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
		external := tapRawNode(4, 0, 1, display.ID, bounds,
			"android.widget.FrameLayout", "", false, false)
		external.Children = []int{5}
		deep := tapRawNode(5, 4, 2, display.ID, bounds,
			"android.widget.TextView", "external", false, false)
		capture.Nodes = append(capture.Nodes, external, deep)
		capture.Nodes[0].Children = []int{1, 4}
		return capture, nil
	}
}

// tapMixedCandidateStep blocks the original center and overlaps the passive
// carriers on the free side with a passive hit from a different branch.
func tapMixedCandidateStep() captureStep {
	freeSide := device.Rect{Left: 0, Top: 0, Right: 400, Bottom: 1000}
	step := tapCardWithChildrenStep(device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000},
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 400, Top: 0, Right: 1000, Bottom: 1000},
			"android.widget.ImageButton", "vertical", true, true),
		tapRawNode(0, 0, 0, 0, freeSide, "android.widget.TextView", "left", false, false),
		tapRawNode(0, 0, 0, 0, freeSide, "android.widget.TextView", "right", false, false))
	return tapWithExternalPeer(step, tapRawNode(0, 0, 0, 0, freeSide,
		"android.widget.TextView", "external peer", false, false))
}

func TestActTapFullyOccludedReportsNoSafePointBlocked(t *testing.T) {
	for _, operation := range []string{"tap", "long_press"} {
		t.Run(operation, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(tapCardFullyCoveredStep())
			start := h.startSession()
			card := tapCardNode(t, h)
			coverTop, coverBottom := tapCoverNodes(t, h)
			probes, captures := h.device.probeCount(), h.device.captureCount()
			request := tapRefRequest(start.Observation.ID, card.Ref)
			if operation == "long_press" {
				request = longPressRefRequest(start.Observation.ID, card.Ref)
			}

			result := h.actWith(t, "Pstatic"+operation, request)
			details := requireStaticRejection(t, result, "no_safe_point", "blocked")
			requireDetailString(t, details, "scope", "target")
			requireDetailString(t, details, "scope_ref", card.Ref)
			candidates, ok := details["candidates"].([]map[string]any)
			if !ok || len(candidates) != 2 {
				t.Fatalf("candidates = %#v, want 2 blocking child candidates", details["candidates"])
			}
			gotRefs := map[string]bool{
				candidates[0]["ref"].(string): true,
				candidates[1]["ref"].(string): true,
			}
			if !gotRefs[coverTop.Ref] || !gotRefs[coverBottom.Ref] {
				t.Fatalf("candidate refs = %#v, want %q and %q", candidates, coverTop.Ref, coverBottom.Ref)
			}
			requireDetailAbsent(t, details, "blocking_refs")
			requireNoLegacyTapDetailKeys(t, details)
			if len(h.device.taps) != 0 {
				t.Fatalf("occluded card dispatched input: %v", h.device.taps)
			}
			if delta := h.device.probeCount() - probes; delta != 0 {
				t.Fatalf("probe delta = %d, want zero", delta)
			}
			if delta := h.device.captureCount() - captures; delta != 0 {
				t.Fatalf("capture delta = %d, want zero", delta)
			}
			requireReadyObservation(t, h, start.Observation.ID)
		})
	}
}

func TestActTapAmbiguousReportsNoSafePointAmbiguous(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapAmbiguousCardWithExternalPeerStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	left := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "left")
	right := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "right")
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Pstaticambiguous", tapRefRequest(start.Observation.ID, card.Ref))
	details := requireStaticRejection(t, result, "no_safe_point", "ambiguous")
	requireDetailString(t, details, "scope", "target")
	requireDetailString(t, details, "scope_ref", card.Ref)
	peer := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "external peer")
	requireDetailStringSlice(t, details, "blocking_refs", left.Ref, right.Ref, peer.Ref)
	requireDetailAbsent(t, details, "candidates")
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("ambiguous card dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapMixedCandidateFailuresReportBothLists(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapMixedCandidateStep())
	start := h.startSession()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root,
		device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000})
	if card == nil {
		t.Fatal("mixed card not found in the public tree")
	}
	cover := tapPublicNode(currentTree(t, h.engine).Root, protocol.RoleButton,
		device.Rect{Left: 400, Top: 0, Right: 1000, Bottom: 1000})
	if cover == nil {
		t.Fatal("mixed fixture vertical blocker not found")
	}
	left := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "left")
	right := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "right")

	result := h.actWith(t, "Pstaticmixed", tapRefRequest(start.Observation.ID, card.Ref))
	details := requireStaticRejection(t, result, "no_safe_point", "mixed")
	requireDetailString(t, details, "scope", "target")
	requireDetailString(t, details, "scope_ref", card.Ref)
	candidates, ok := details["candidates"].([]map[string]any)
	if !ok || len(candidates) != 1 || candidates[0]["ref"] != cover.Ref ||
		candidates[0]["relation"] != candidateRelationChild || candidates[0]["executable"] != true {
		t.Fatalf("candidates = %#v, want [%q child executable]", details["candidates"], cover.Ref)
	}
	peer := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "external peer")
	requireDetailStringSlice(t, details, "blocking_refs", left.Ref, right.Ref, peer.Ref)
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("mixed rejection dispatched input: %v", h.device.taps)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapCrossOcclusionSuggestsFirstGridPoint(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCrossCoveredStep())
	start := h.startSession()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root,
		device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000})
	if card == nil {
		t.Fatal("cross card not found in the public tree")
	}

	result := h.actWith(t, "Pstaticgrid", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("cross occlusion grid outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{166, 166})
}

func TestActTapCrossOcclusionOmitsExternallyOwnedSuggestion(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCrossCoveredExternalStep())
	start := h.startSession()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root,
		device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000})
	if card == nil {
		t.Fatal("cross card not found in the public tree")
	}

	result := h.actWith(t, "Pstaticexternal", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("cross external sibling grid outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{166, 166})
}

func TestActTapTargetMissingActionReportsEmptyActionsAndGuidance(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	content := tapContentNode(t, h)

	result := h.actWith(t, "Pstatictargetmissing", tapRefRequest(start.Observation.ID, content.Ref))
	details := requireStaticRejection(t, result, "unsupported_node_action", "target_missing_action")
	requireDetailStringSlice(t, details, "target_actions")
	requireDetailString(t, details, "scope", "target")
	requireDetailString(t, details, "scope_ref", content.Ref)
	requireGuidanceCandidate(t, details, card.Ref, candidateRelationAncestor,
		[]int{tapCardBounds.Left, tapCardBounds.Top, tapCardBounds.Right, tapCardBounds.Bottom}, true)
	requireDetailAbsent(t, details, "ancestor_ref")
	requireDetailAbsent(t, details, "target_center")
	requireNoLegacyTapDetailKeys(t, details)
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapSwipeOnlyTargetReportsTargetMissingAction(t *testing.T) {
	swipeNode := tapRawNode(0, 0, 0, 0, tapContentBounds,
		"android.widget.ScrollView", "", false, false)
	swipeNode.HasScrollable, swipeNode.Scrollable = true, true
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardWithChildrenStep(tapCardBounds, swipeNode))
	start := h.startSession()
	swipePublic := tapNodeWithAction(currentTree(t, h.engine).Root, protocol.ActionSwipe)
	if swipePublic == nil {
		t.Fatal("swipe-only target not found")
	}

	result := h.actWith(t, "Pstaticswipe", tapRefRequest(start.Observation.ID, swipePublic.Ref))
	details := requireStaticRejection(t, result, "unsupported_node_action", "target_missing_action")
	requireDetailStringSlice(t, details, "target_actions", "swipe")
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapTargetHasOtherActionReportsPublicActions(t *testing.T) {
	child := tapRawNode(0, 0, 0, 0, tapContentBounds,
		"android.widget.TextView", "post", false, true)
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardWithChildrenStep(tapCardBounds, child))
	start := h.startSession()
	public := tapPublicBoundsNode(currentTree(t, h.engine).Root, tapContentBounds)
	if public == nil {
		t.Fatal("long_press-only target not found")
	}

	result := h.actWith(t, "Pstaticotheraction", tapRefRequest(start.Observation.ID, public.Ref))
	details := requireStaticRejection(t, result, "unsupported_node_action", "target_has_other_action")
	requireDetailStringSlice(t, details, "target_actions", "long_press")
	requireDetailAbsent(t, details, "ancestor_ref")
	requireDetailAbsent(t, details, "nearest_actionable_ref")
	requireDetailAbsent(t, details, "target_center")
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActBubbleAncestorStaticReasons(t *testing.T) {
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 800}
	childBounds := device.Rect{Left: 200, Top: 400, Right: 700, Bottom: 600}

	t.Run("no interactive ancestor", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return bubbleRootOnly(display, childBounds), nil
		})
		start := h.startSession()
		child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
		result := h.actWith(t, "Pstaticnoancestor",
			bubbleRequest(start.Observation.ID, child.Ref, nil))
		details := requireStaticRejection(t, result, "unsupported_node_action", "no_interactive_ancestor")
		requireDetailStringSlice(t, details, "target_actions")
		requireDetailAbsent(t, details, "ancestor_ref")
		// Contract 35 §4.2: this reason alone states the objective clipped center.
		requireDetailIntSlice(t, details, "target_center", 450, 500)
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("ancestor missing action", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			capture := bubblePage(display, cardBounds, childBounds, "post", false)
			capture.Nodes[1].HasClickable, capture.Nodes[1].Clickable = true, false
			capture.Nodes[1].HasLongClick, capture.Nodes[1].LongClickable = true, true
			return capture, nil
		})
		start := h.startSession()
		child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
		card := tapPublicBoundsNode(currentTree(t, h.engine).Root, cardBounds)
		result := h.actWith(t, "Pstaticancestormissing",
			bubbleRequest(start.Observation.ID, child.Ref, nil))
		details := requireStaticRejection(t, result, "unsupported_node_action", "ancestor_missing_action")
		requireDetailString(t, details, "ancestor_ref", card.Ref)
		requireDetailStringSlice(t, details, "ancestor_actions", "long_press")
		requireDetailStringSlice(t, details, "target_actions")
		requireDetailAbsent(t, details, "target_center")
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("ancestor disabled", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			capture := bubblePage(display, cardBounds, childBounds, "post", false)
			capture.Nodes[1].Enabled = false
			return capture, nil
		})
		start := h.startSession()
		child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
		card := tapPublicBoundsNode(currentTree(t, h.engine).Root, cardBounds)
		result := h.actWith(t, "Pstaticancestorinvalid",
			bubbleRequest(start.Observation.ID, child.Ref, nil))
		details := requireStaticRejection(t, result, "unsupported_node_action", "ancestor_invalid")
		requireDetailString(t, details, "ancestor_ref", card.Ref)
		requireDetailStringSlice(t, details, "ancestor_actions", "tap", "long_press")
		requireDetailAbsent(t, details, "target_center")
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("ancestor without bounds", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			capture := bubblePage(display, cardBounds, childBounds, "post", false)
			capture.Nodes[1].RawBounds = device.Rect{}
			capture.Nodes[1].Bounds = device.Rect{}
			return capture, nil
		})
		start := h.startSession()
		child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
		card := tapNodeWithAction(currentTree(t, h.engine).Root, protocol.ActionTap)
		if child == nil || card == nil {
			t.Fatal("ancestor without bounds fixture is incomplete")
		}
		result := h.actWith(t, "Pstaticancestornobounds",
			bubbleRequest(start.Observation.ID, child.Ref, nil))
		details := requireStaticRejection(t, result, "unsupported_node_action", "ancestor_invalid")
		requireDetailString(t, details, "ancestor_ref", card.Ref)
		requireDetailStringSlice(t, details, "ancestor_actions", "tap", "long_press")
		requireDetailAbsent(t, details, "target_center")
		requireReadyObservation(t, h, start.Observation.ID)
	})
}

func TestActBubbleOwnerMismatchReportsUniqueExternalOwner(t *testing.T) {
	cardBounds := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
	childBounds := device.Rect{Left: 400, Top: 400, Right: 600, Bottom: 600}
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := bubblePage(display, cardBounds, childBounds, "post", false)
		external := tapRawNode(3, 0, 1, display.ID, cardBounds,
			"android.widget.FrameLayout", "", false, false)
		external.Children = []int{4}
		middle := tapRawNode(4, 3, 2, display.ID, cardBounds,
			"android.widget.FrameLayout", "", false, false)
		middle.Children = []int{5}
		deep := tapRawNode(5, 4, 3, display.ID, cardBounds,
			"android.widget.TextView", "external", false, false)
		capture.Nodes = append(capture.Nodes, external, middle, deep)
		capture.Nodes[0].Children = []int{1, 3}
		return capture, nil
	})
	start := h.startSession()
	child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
	external := tapPublicNode(currentTree(t, h.engine).Root, protocol.RoleText, cardBounds)
	if child == nil || external == nil {
		t.Fatal("owner mismatch fixture is incomplete")
	}

	result := h.actWith(t, "Pstaticowner", bubbleRequest(start.Observation.ID, child.Ref, nil))
	details := requireStaticRejection(t, result, "no_safe_point", "owner_mismatch")
	requireDetailString(t, details, "scope", "ancestor")
	requireDetailStringSlice(t, details, "blocking_refs", external.Ref)
	requireDetailAbsent(t, details, "target_center")
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("owner mismatch dispatched input: %v", h.device.taps)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapInvalidTargetReasons(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		h := newTapScopeHarness(t)
		start := h.startSession()
		root := currentTree(t, h.engine).Root
		result := h.actWith(t, "Pstaticroot", tapRefRequest(start.Observation.ID, root.Ref))
		details := requireStaticRejection(t, result, "invalid_target", "root")
		requireDetailString(t, details, "scope", "target")
		requireDetailString(t, details, "scope_ref", root.Ref)
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("context", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardWithContextDescendantStep())
		start := h.startSession()
		contextNode := tapContextNode(currentTree(t, h.engine).Root)
		result := h.actWith(t, "Pstaticcontext", tapRefRequest(start.Observation.ID, contextNode.Ref))
		details := requireStaticRejection(t, result, "invalid_target", "context")
		requireDetailString(t, details, "scope", "target")
		requireDetailString(t, details, "scope_ref", contextNode.Ref)
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("no bounds with guidance", func(t *testing.T) {
		boundsless := tapRawNode(0, 0, 0, 0, device.Rect{},
			"android.widget.TextView", "no bounds", false, false)
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardWithChildrenStep(tapCardBounds, boundsless))
		start := h.startSession()
		target := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "no bounds")
		card := tapCardNode(t, h)
		result := h.actWith(t, "Pstaticnobounds", tapRefRequest(start.Observation.ID, target.Ref))
		details := requireStaticRejection(t, result, "invalid_target", "no_geometry")
		requireDetailString(t, details, "scope", "target")
		requireDetailString(t, details, "scope_ref", target.Ref)
		requireGuidanceCandidate(t, details, card.Ref, candidateRelationAncestor,
			[]int{tapCardBounds.Left, tapCardBounds.Top, tapCardBounds.Right, tapCardBounds.Bottom}, true)
		requireNoLegacyTapDetailKeys(t, details)
		requireReadyObservation(t, h, start.Observation.ID)
	})
}

func tapContentNodeByLabel(t *testing.T, root *protocol.Node, label string) *protocol.Node {
	t.Helper()
	var walk func(*protocol.Node) *protocol.Node
	walk = func(node *protocol.Node) *protocol.Node {
		if node == nil {
			return nil
		}
		if len(node.Label) == 1 && node.Label[0] == label {
			return node
		}
		for _, child := range node.Children {
			if found := walk(child); found != nil {
				return found
			}
		}
		return nil
	}
	found := walk(root)
	if found == nil {
		t.Fatalf("node with label %q not found", label)
	}
	return found
}

// tapCarrierSuggestionStep blocks every original candidate but leaves the first
// grid point uniquely owned by a non-blocking carrier.
func tapCarrierSuggestionStep() captureStep {
	return tapCardWithChildrenStep(device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000},
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 400, Top: 0, Right: 600, Bottom: 1000},
			"android.widget.ImageButton", "vertical", true, true),
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 0, Top: 400, Right: 1000, Bottom: 600},
			"android.widget.ImageButton", "horizontal", true, true),
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 0, Top: 0, Right: 300, Bottom: 300},
			"android.widget.TextView", "carrier", false, false),
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 140, Top: 140, Right: 160, Bottom: 160},
			"android.widget.ImageButton", "blocker", true, true))
}

func pruningImageRaw(resource string, bounds device.Rect) device.RawNode {
	raw := tapRawNode(0, 0, 0, 0, bounds,
		"android.widget.ImageView", "", false, false)
	raw.ResourceSet, raw.ResourceID = true, resource
	raw.HasFocused, raw.Focused = true, false
	raw.HasSelected, raw.Selected = true, false
	raw.HasPassword, raw.Password = true, false
	raw.HasChecked, raw.Checked = true, false
	return raw
}

func pruningCarrierStep() captureStep {
	return tapCardWithChildrenStep(
		device.Rect{Left: 0, Top: 300, Right: 1000, Bottom: 1100},
		pruningImageRaw("app:id/pruning_carrier",
			device.Rect{Left: 495, Top: 300, Right: 505, Bottom: 1100}))
}

func pruningCarrierSuggestionStep() captureStep {
	return tapCardWithChildrenStep(device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000},
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 400, Top: 0, Right: 600, Bottom: 1000},
			"android.widget.ImageButton", "vertical", true, true),
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 0, Top: 400, Right: 1000, Bottom: 600},
			"android.widget.ImageButton", "horizontal", true, true),
		pruningImageRaw("app:id/pruning_suggested_carrier",
			device.Rect{Left: 100, Top: 100, Right: 199, Bottom: 199}),
		tapRawNode(0, 0, 0, 0, device.Rect{Left: 140, Top: 140, Right: 160, Bottom: 160},
			"android.widget.ImageButton", "blocker", true, true))
}

func pruningAmbiguousStep() captureStep {
	bounds := device.Rect{Left: 0, Top: 0, Right: 100, Bottom: 100}
	step := tapCardWithChildrenStep(bounds,
		pruningImageRaw("app:id/pruning_ambiguous_left", bounds),
		pruningImageRaw("app:id/pruning_ambiguous_right", bounds))
	return tapWithExternalPeer(step, pruningImageRaw("app:id/pruning_ambiguous_peer", bounds))
}

func requirePrunedRefInspectable(t *testing.T, h *harness, observation *protocol.Observation,
	ref string, role protocol.Role) {
	t.Helper()
	if observation == nil || observation.Tree == nil || observation.Tree.Full == nil {
		t.Fatalf("fixture did not publish a compact full: %+v", observation)
	}
	if readNodeByRef(observation.Tree.Full.Root, ref) != nil {
		t.Fatalf("pruned ref %q remained in compact full", ref)
	}
	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		Reference: observation.ID, Node: ref, HasNode: true,
	})
	if apiErr != nil {
		t.Fatalf("inspect pruned ref %q from original O %q: %v", ref, observation.ID, apiErr)
	}
	if result == nil || result.Inspection == nil ||
		result.Inspection.ObservationID != observation.ID ||
		result.Inspection.Scope != "subtree" ||
		result.Inspection.View != protocol.ViewDetailed ||
		result.Inspection.Tree != nil ||
		result.Inspection.Root == nil ||
		result.Inspection.Root.Ref != ref ||
		result.Inspection.Root.Role != role ||
		result.Inspection.Root.Bounds.Kind != protocol.GeometryKnown {
		t.Fatalf("detailed inspection for pruned ref %q: %+v", ref, result)
	}
}

// TestActPrunedImageCarrierEvidence covers both input actions on the same
// complete-tree carrier. Compact omits the image, while the original O keeps it
// available for action evidence and historical inspection.
func TestActPrunedImageCarrierEvidence(t *testing.T) {
	for _, operation := range []string{"tap", "long_press"} {
		t.Run(operation, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(pruningCarrierStep())
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			imageRef, ok := sourceRefForResource(state.Projection, "app:id/pruning_carrier")
			if !ok {
				t.Fatal("fixture has no mapped pruning carrier")
			}
			sourceImage := nodeByRef(state.tree().Root, imageRef)
			if sourceImage == nil || sourceImage.Bounds == nil ||
				!sourceImage.Bounds.Contains(500, 700) {
				t.Fatalf("source image %q does not own the card center: %+v", imageRef, sourceImage)
			}
			if readNodeByRef(start.Observation.Tree.Full.Root, imageRef) != nil {
				t.Fatalf("small anonymous image %q was not pruned", imageRef)
			}
			card := tapPublicBoundsNode(state.tree().Root,
				device.Rect{Left: 0, Top: 300, Right: 1000, Bottom: 1100})
			if card == nil {
				t.Fatal("composite card not found")
			}
			request := tapRefRequest(start.Observation.ID, card.Ref)
			if operation == "long_press" {
				request = longPressRefRequest(start.Observation.ID, card.Ref)
			}

			result := h.actWith(t, "Ppruned"+operation, request)
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("%s outcome=%q error=%+v", operation, result.Outcome, result.Error)
			}
			requireTapPoint(t, h, [2]int{500, 700})
			requirePrunedRefInspectable(t, h, start.Observation, imageRef, protocol.RoleImage)
		})
	}
}

func TestActStaticPrunedCarrierRemainsInspectable(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(pruningCarrierSuggestionStep())
	start := h.startSession()
	state := observationStateByID(t, h.engine, start.Observation.ID)
	carrierRef, ok := sourceRefForResource(state.Projection, "app:id/pruning_suggested_carrier")
	if !ok {
		t.Fatal("fixture has no mapped suggested carrier")
	}
	sourceCarrier := nodeByRef(state.tree().Root, carrierRef)
	if sourceCarrier == nil || sourceCarrier.Bounds == nil ||
		!sourceCarrier.Bounds.Contains(166, 166) {
		t.Fatalf("source carrier %q does not own the suggested point: %+v", carrierRef, sourceCarrier)
	}
	card := tapPublicBoundsNode(state.tree().Root,
		device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000})
	if card == nil {
		t.Fatal("suggestion card not found")
	}

	result := h.actWith(t, "Pprunedsuggest",
		tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("pruned grid carrier outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{166, 166})
	requirePrunedRefInspectable(t, h, start.Observation, carrierRef, protocol.RoleImage)
}

func TestActStaticPrunedAmbiguityRemainsInspectable(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(pruningAmbiguousStep())
	start := h.startSession()
	state := observationStateByID(t, h.engine, start.Observation.ID)
	leftRef, leftOK := sourceRefForResource(state.Projection, "app:id/pruning_ambiguous_left")
	rightRef, rightOK := sourceRefForResource(state.Projection, "app:id/pruning_ambiguous_right")
	peerRef, peerOK := sourceRefForResource(state.Projection, "app:id/pruning_ambiguous_peer")
	if !leftOK || !rightOK || !peerOK {
		t.Fatalf("fixture refs: left=%q/%t right=%q/%t peer=%q/%t", leftRef, leftOK, rightRef, rightOK, peerRef, peerOK)
	}
	card := tapPublicBoundsNode(state.tree().Root,
		device.Rect{Left: 0, Top: 0, Right: 100, Bottom: 100})
	if card == nil {
		t.Fatal("ambiguous card not found")
	}
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Pprunedambiguous",
		tapRefRequest(start.Observation.ID, card.Ref))
	details := requireStaticRejection(t, result, "no_safe_point", "ambiguous")
	requireDetailStringSlice(t, details, "blocking_refs", leftRef, rightRef, peerRef)
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("static ambiguity dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("static ambiguity probe delta=%d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("static ambiguity capture delta=%d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
	requirePrunedRefInspectable(t, h, start.Observation, leftRef, protocol.RoleImage)
	requirePrunedRefInspectable(t, h, start.Observation, rightRef, protocol.RoleImage)
	requirePrunedRefInspectable(t, h, start.Observation, peerRef, protocol.RoleImage)
}

func TestActTapSuggestionReportsCarrierRef(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCarrierSuggestionStep())
	start := h.startSession()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root,
		device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000})
	if card == nil {
		t.Fatal("carrier suggestion card not found")
	}

	result := h.actWith(t, "Pstaticcarrier", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("grid carrier outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{166, 166})
}

func TestActBubbleNoSafePointReportsAncestorScope(t *testing.T) {
	cardBounds := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
	childBounds := device.Rect{Left: 400, Top: 400, Right: 600, Bottom: 600}
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := bubblePage(display, cardBounds, childBounds, "post", false)
		cover := tapRawNode(3, 1, 2, display.ID, cardBounds,
			"android.widget.Button", "cover", false, true)
		capture.Nodes = append(capture.Nodes, cover)
		capture.Nodes[1].Children = append(capture.Nodes[1].Children, 3)
		return capture, nil
	})
	start := h.startSession()
	child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root, cardBounds)
	cover := tapContentNodeByLabel(t, currentTree(t, h.engine).Root, "cover")
	if child == nil || card == nil || cover == nil {
		t.Fatal("bubble no-safe-point fixture is incomplete")
	}

	result := h.actWith(t, "Pstaticbubbleblocked",
		bubbleRequest(start.Observation.ID, child.Ref, nil))
	details := requireStaticRejection(t, result, "no_safe_point", "blocked")
	requireDetailString(t, details, "scope", "ancestor")
	requireDetailString(t, details, "scope_ref", card.Ref)
	requireDetailString(t, details, "ancestor_ref", card.Ref)
	requireDetailStringSlice(t, details, "blocking_refs", cover.Ref)
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("bubble blocked rejection dispatched input: %v", h.device.taps)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestTapFiveSixthsMatchesFloorWithoutOverflow(t *testing.T) {
	for width := 1; width <= 600; width++ {
		want := 5 * width / 6
		if got := tapFiveSixths(width); got != want {
			t.Fatalf("tapFiveSixths(%d) = %d, want %d", width, got, want)
		}
	}
	maxInt := int(^uint(0) >> 1)
	if got := tapFiveSixths(maxInt); got < 0 || got >= maxInt {
		t.Fatalf("tapFiveSixths(maxInt) = %d, want a non-negative value below maxInt", got)
	}
}

func TestTapStaticGridPointsAreBoundedAndDeduplicated(t *testing.T) {
	for _, base := range []device.Rect{
		{Left: 0, Top: 0, Right: 1, Bottom: 1},
		{Left: 10, Top: 20, Right: 13, Bottom: 22},
		{Left: 17, Top: 19, Right: 29, Bottom: 31},
		{Left: 100, Top: 200, Right: 1001, Bottom: 2003},
	} {
		points := tapStaticGridPoints(base)
		if len(points) == 0 || len(points) > 9 {
			t.Fatalf("grid(%+v) produced %d points, want 1..9", base, len(points))
		}
		seen := map[[2]int]bool{}
		for _, point := range points {
			if !base.Contains(point.x, point.y) {
				t.Fatalf("grid(%+v) point (%d,%d) is outside the half-open base",
					base, point.x, point.y)
			}
			if point.x < 0 || point.y < 0 {
				t.Fatalf("grid(%+v) produced a negative point (%d,%d)", base, point.x, point.y)
			}
			key := [2]int{point.x, point.y}
			if seen[key] {
				t.Fatalf("grid(%+v) repeated point (%d,%d)", base, point.x, point.y)
			}
			seen[key] = true
		}
	}
}

func TestTapStaticRefListsOrderAndLimit(t *testing.T) {
	view := &tapPublicView{info: map[*protocol.Node]*tapPublicInfo{}}
	nodes := make([]*protocol.Node, 6)
	for index := range nodes {
		nodes[index] = &protocol.Node{Ref: "n" + string(rune('a'+index))}
		view.info[nodes[index]] = &tapPublicInfo{order: index}
	}
	occluding := map[string]tapStaticOwnerFact{
		nodes[0].Ref: {depth: 1, order: 0},
		nodes[1].Ref: {depth: 3, order: 4},
		nodes[2].Ref: {depth: 3, order: 2},
		nodes[3].Ref: {depth: 2, order: 1},
		nodes[4].Ref: {depth: 3, order: 5},
	}
	if got := tapStaticOccludingRefs(view, occluding); len(got) != 3 ||
		got[0] != nodes[2].Ref || got[1] != nodes[1].Ref || got[2] != nodes[4].Ref {
		t.Fatalf("occluding order = %v, want depth-desc/preorder-asc top three", got)
	}
	ambiguous := map[string]int{
		nodes[5].Ref: 5, nodes[1].Ref: 1, nodes[0].Ref: 0, nodes[4].Ref: 4,
	}
	if got := tapStaticOrderedRefs(view, ambiguous); len(got) != 3 ||
		got[0] != nodes[0].Ref || got[1] != nodes[1].Ref || got[2] != nodes[4].Ref {
		t.Fatalf("ambiguous order = %v, want preorder first three", got)
	}
}

// TestTapStaticDiagnosisMatchesBranchPredicate is the kernel-equivalence gate:
// a point classified as passing must be exactly a point the original bool
// predicate accepts, for both the direct and bubble branches.
func TestTapStaticDiagnosisMatchesBranchPredicate(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCarrierSuggestionStep())
		h.startSession()
		h.engine.mu.Lock()
		obs := h.engine.current
		h.engine.mu.Unlock()
		view, apiErr := collectTapPublicView(obs)
		if apiErr != nil {
			t.Fatalf("collect view: %+v", apiErr)
		}
		card := tapPublicBoundsNode(view.tree.Root,
			device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000})
		input, apiErr := buildTapInput(obs, view, card)
		if apiErr != nil {
			t.Fatalf("build input: %+v", apiErr)
		}
		scope, apiErr := collectTapScope(input)
		if apiErr != nil {
			t.Fatalf("collect scope: %+v", apiErr)
		}
		for y := scope.base.Top; y < scope.base.Bottom; y += 37 {
			for x := scope.base.Left; x < scope.base.Right; x += 37 {
				failure := classifyTapStaticPoint(view, scope, card, false, x, y)
				if (failure.kind == "") != provesSubtreeTapPoint(scope, x, y) {
					t.Fatalf("point (%d,%d): diagnosis kind %q diverges from direct predicate",
						x, y, failure.kind)
				}
			}
		}
	})

	t.Run("bubble", func(t *testing.T) {
		cardBounds := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
		childBounds := device.Rect{Left: 400, Top: 400, Right: 600, Bottom: 600}
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return bubblePage(display, cardBounds, childBounds, "post", false), nil
		})
		h.startSession()
		h.engine.mu.Lock()
		obs := h.engine.current
		h.engine.mu.Unlock()
		view, apiErr := collectTapPublicView(obs)
		if apiErr != nil {
			t.Fatalf("collect view: %+v", apiErr)
		}
		card := tapPublicBoundsNode(view.tree.Root, cardBounds)
		child := tapPublicBoundsNode(view.tree.Root, childBounds)
		input, apiErr := buildTapInput(obs, view, card)
		if apiErr != nil {
			t.Fatalf("build input: %+v", apiErr)
		}
		scope, apiErr := collectTapScope(input)
		if apiErr != nil {
			t.Fatalf("collect scope: %+v", apiErr)
		}
		for y := scope.base.Top; y < scope.base.Bottom; y += 37 {
			for x := scope.base.Left; x < scope.base.Right; x += 37 {
				failure := classifyTapStaticPoint(view, scope, card, true, x, y)
				if (failure.kind == "") != provesGuidancePoint(view, card, scope, x, y) {
					t.Fatalf("point (%d,%d): diagnosis kind %q diverges from bubble predicate for %q",
						x, y, failure.kind, child.Ref)
				}
			}
		}
	})
}

// TestActTapPlannerGeometryReportsInvalidTarget covers the planner-stage
// geometry failure: the published bounds still look valid, but the raw
// rectangle cannot produce a non-empty base.
func TestActTapPlannerGeometryReportsInvalidTarget(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()
	h.engine.mu.Lock()
	index, mapped := h.engine.current.Projection.Source[card.Ref]
	if mapped {
		h.engine.current.Projection.Capture.Nodes[index].Bounds = device.Rect{}
	}
	h.engine.mu.Unlock()
	if !mapped {
		t.Fatal("card source mapping missing")
	}

	result := h.actWith(t, "Pstaticgeometry", tapRefRequest(start.Observation.ID, card.Ref))
	details := requireStaticRejection(t, result, "invalid_target", "no_geometry")
	requireDetailString(t, details, "scope", "target")
	requireDetailString(t, details, "scope_ref", card.Ref)
	if len(h.device.taps) != 0 {
		t.Fatalf("geometry rejection dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

var benchmarkTapDiagnosis *tapStaticDiagnosis

// BenchmarkTapStaticDiagnosis compares the original predicate with the full
// failure diagnosis on a 1002-node ambiguous scope. It measures the failure
// path only; the success path never builds a diagnosis or probes the grid.
func BenchmarkTapStaticDiagnosis(b *testing.B) {
	const leaves = 1000
	bounds := protocol.Bounds{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
	root := &protocol.Node{Ref: "n0", Role: protocol.RoleRoot, Bounds: &bounds}
	target := &protocol.Node{
		Ref: "n1", Role: protocol.RoleView, Bounds: &bounds,
		Actions: []protocol.Action{protocol.ActionTap},
	}
	root.Children = []*protocol.Node{target}
	view := &tapPublicView{
		tree:  &protocol.Tree{Root: root},
		byRef: map[string]*protocol.Node{},
		info:  map[*protocol.Node]*tapPublicInfo{},
	}
	add := func(node, parent *protocol.Node, depth, order int) {
		view.byRef[node.Ref] = node
		view.info[node] = &tapPublicInfo{
			node: node, parent: parent, depth: depth, order: order,
			rawIndex: order, hasSource: true,
		}
	}
	add(root, nil, 0, 0)
	add(target, root, 1, 1)
	scope := &tapScope{
		target:        target,
		base:          rectFromBounds(&bounds),
		carrierByRef:  map[string]*tapCarrier{},
		carrierByNode: map[*protocol.Node]*tapCarrier{},
	}
	for index := 0; index < leaves; index++ {
		ref := "n" + string(rune('a'+index%26)) + string(rune('a'+(index/26)%26))
		leaf := &protocol.Node{
			Ref: ref, Role: protocol.RoleText, Bounds: &bounds,
			Label: []string{"leaf"},
		}
		target.Children = append(target.Children, leaf)
		add(leaf, target, 2, index+2)
		carrier := &tapCarrier{node: leaf, rawIndex: index + 2, order: index + 2}
		scope.carriers = append(scope.carriers, carrier)
		scope.carrierByRef[ref] = carrier
		scope.carrierByNode[leaf] = carrier
	}
	points := make([]tapStaticPoint, 0, 100)
	for index := 0; index < 100; index++ {
		points = append(points, tapStaticPoint{x: index*9 + 1, y: index*7 + 1})
	}
	b.Logf("scope nodes=%d original candidates=%d", leaves+2, len(points))
	b.Run("predicate", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; iteration < b.N; iteration++ {
			for _, point := range points {
				provesSubtreeTapPoint(scope, point.x, point.y)
			}
		}
	})
	b.Run("diagnosis", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; iteration < b.N; iteration++ {
			benchmarkTapDiagnosis = diagnoseTapStaticFailure(view, scope, target, false, points)
		}
	})
}
