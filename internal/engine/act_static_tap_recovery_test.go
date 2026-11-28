package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func staticRecoveryOperation(kind, ref string) protocol.Operation {
	return protocol.Operation{Type: kind, Target: &protocol.Target{UseRef: true, Ref: ref}}
}

func staticRecoveryNodeByResource(t *testing.T, state *observationState, resource string) *protocol.Node {
	t.Helper()
	ref, ok := sourceRefForResource(state.Projection, resource)
	if !ok {
		t.Fatalf("fixture has no source for resource %q", resource)
	}
	node := nodeByRef(state.tree().Root, ref)
	if node == nil {
		t.Fatalf("fixture has no public node for resource %q at %s", resource, ref)
	}
	return node
}

func TestStaticTextRefRecoveryBindings(t *testing.T) {
	for _, branch := range []string{"D1a-wrapper", "D1a-context-pruned", "D1b-placeholder", "D1c-external"} {
		t.Run(branch, func(t *testing.T) {
			bounds := device.Rect{Left: 100, Top: 300, Right: 700, Bottom: 400}
			step := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := revalCapture(display, revalRoot(display.ID))
				wrapper := proposalClickableNode(0, 0, 0, display, bounds, "android.widget.FrameLayout", "search shell")
				placeholder := proposalNode(0, 0, 0, display, bounds, "android.widget.TextView", "placeholder")
				input := revalEditText(0, 0, 0, display.ID, bounds, "", "", "app:id/editor", true)
				switch branch {
				case "D1a-wrapper", "D1a-context-pruned":
					actRebindTestAppend(&capture, 0, wrapper)
					actRebindTestAppend(&capture, 1, input)
					if branch == "D1a-context-pruned" {
						contextBounds := device.Rect{Left: 720, Top: 300, Right: 920, Bottom: 400}
						context := proposalNode(0, 0, 0, display, contextBounds, "android.widget.FrameLayout", "")
						context.Visible = false
						actRebindTestAppend(&capture, 1, context)
						other := revalEditText(0, 0, 0, display.ID, contextBounds, "", "", "app:id/other", false)
						actRebindTestAppend(&capture, 3, other)
					}
				case "D1b-placeholder":
					actRebindTestAppend(&capture, 0, input)
					actRebindTestAppend(&capture, 1, placeholder)
				case "D1c-external":
					actRebindTestAppend(&capture, 0, placeholder)
					actRebindTestAppend(&capture, 0, input)
				}
				return capture
			})
			h, rec := newRevalHarness(t)
			dev := &textEvDevice{revalDevice: rec}
			h.engine.dev = dev
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			root := state.tree().Root
			input := mustFindNodeByTextOrResource(t, root, "app:id/editor")
			label := "placeholder"
			if branch == "D1a-wrapper" || branch == "D1a-context-pruned" {
				label = "search shell"
			}
			if branch == "D1a-context-pruned" {
				other := staticRecoveryNodeByResource(t, state, "app:id/other")
				view, viewErr := collectTapPublicView(state)
				if viewErr != nil || !view.info[other].parent.Context {
					t.Fatalf("second editor must project beneath a context node: %v", viewErr)
				}
			}
			target := proposalFindLabel(t, root, label)
			op := staticRecoveryOperation("text", target.Ref)
			_, _, plan, apiErr := staticTextRefCheck(state, &op)
			if apiErr != nil || plan == nil || plan.targetRef != input.Ref ||
				plan.targetRaw != &state.Projection.Capture.Nodes[state.Projection.Source[input.Ref]] {
				t.Fatalf("static plan=%+v error=%+v, want binding to %s", plan, apiErr, input.Ref)
			}
			autoSubmit := false
			result := h.actWith(t, "PtextBinding", textFlowRequest(start.Observation.ID, target.Ref, "hello", &autoSubmit))
			requireTextRefNoTapExecuted(t, result, false)
			requireTextRefNoTapDispatch(t, dev, 0, 1, 0)
			if result.AutoRecovery != nil {
				t.Fatalf("static resolution produced auto_recovery: %+v", result.AutoRecovery)
			}
		})
	}
}

func TestStaticTextRefRecoveryRejections(t *testing.T) {
	for _, name := range []string{"half-screen-shell", "half-screen-external", "protected-second-input", "disabled-second-input", "readonly-input", "protected-target", "fake-search-button", "nearest-other-action"} {
		t.Run(name, func(t *testing.T) {
			bounds := device.Rect{Left: 100, Top: 300, Right: 800, Bottom: 500}
			step := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := revalCapture(display, revalRoot(display.ID))
				targetBounds := bounds
				if name == "half-screen-shell" || name == "half-screen-external" {
					targetBounds = device.Rect{Right: display.Width, Bottom: display.Height / 2}
				}
				target := proposalNode(0, 0, 0, display, targetBounds, "android.widget.FrameLayout", "requested")
				editor := revalEditText(0, 0, 0, display.ID, targetBounds, "", "", "app:id/editor", false)
				parent := 0
				switch name {
				case "readonly-input", "protected-target":
					target = tapTextProposalEditable(0, 0, 0, display, bounds, false, name == "protected-target")
					if name == "readonly-input" {
						parent = 1
					}
				case "fake-search-button":
					target = proposalClickableNode(0, 0, 0, display, bounds, "android.widget.Button", "requested")
				case "half-screen-shell", "protected-second-input", "disabled-second-input":
					parent = 1
				case "half-screen-external":
					x, y := targetBounds.Center()
					editor.Bounds = device.Rect{Left: x - 100, Top: y - 50, Right: x + 100, Bottom: y + 50}
					editor.RawBounds = editor.Bounds
				}
				target.ResourceSet, target.ResourceID = true, "app:id/requested"
				if name == "nearest-other-action" {
					actRebindTestAppend(&capture, 0, editor)
					button := proposalClickableNode(0, 0, 0, display, bounds, "android.widget.Button", "button")
					actRebindTestAppend(&capture, 1, button)
					actRebindTestAppend(&capture, 2, target)
					return capture
				}
				actRebindTestAppend(&capture, 0, target)
				if parent == 1 && name != "half-screen-shell" {
					editor.Bounds.Bottom = 425
					editor.RawBounds = editor.Bounds
				}
				actRebindTestAppend(&capture, parent, editor)
				if name == "protected-second-input" || name == "disabled-second-input" {
					extra := tapTextProposalEditable(0, 0, 0, display,
						device.Rect{Left: 100, Top: 460, Right: 200, Bottom: 500},
						true, name == "protected-second-input")
					extra.Enabled = name != "disabled-second-input"
					actRebindTestAppend(&capture, 1, extra)
				}
				return capture
			})
			h, rec := newRevalHarness(t)
			dev := &textEvDevice{revalDevice: rec}
			h.engine.dev = dev
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			target := staticRecoveryNodeByResource(t, state, "app:id/requested")
			if name == "readonly-input" {
				// The projector normally emits RoleInput and ActionText together.
				// Exercise admission with the independently reported role retained.
				if target.Role != protocol.RoleInput {
					t.Fatalf("fixture must have RoleInput, got %+v", target)
				}
				h.engine.mu.Lock()
				target.Actions = nil
				h.engine.mu.Unlock()
			}
			result := h.actWith(t, "PtextReject", textFlowRequest(start.Observation.ID, target.Ref, "hello", nil))
			reason := textReasonTargetNotEditable
			if name == "protected-target" {
				reason = textReasonTargetProtected
			}
			details := proposalRequireStaticReason(t, result, protocol.CodeTextTargetUnknown, reason)
			requireTextDetail(t, details, "stage", "target")
			requireTextDispatch(t, details, "not_attempted", "not_attempted", "not_attempted")
			if name == "protected-target" {
				requireDetailAbsent(t, details, "candidates")
			} else {
				editor := staticRecoveryNodeByResource(t, state, "app:id/editor")
				proposalRequireCandidateRefs(t, proposalCandidates(t, details), editor.Ref)
			}
			requireTextRefNoTapDispatch(t, dev, 0, 0, 0)
			requireReadyObservation(t, h, start.Observation.ID)
		})
	}
}

func TestStaticTextRefRecoveryStopsBeforeExternalFallback(t *testing.T) {
	for _, name := range []string{
		"d1a-ambiguous-external", "d1a-ambiguous-external-protected", "d1a-single-unusable-external",
		"d1b-interactive-ancestor-external", "d1b-protected-ancestor", "d1b-disabled-ancestor", "d1b-blocked-ancestor",
	} {
		t.Run(name, func(t *testing.T) {
			bounds := device.Rect{Left: 100, Top: 300, Right: 800, Bottom: 500}
			step := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := revalCapture(display, revalRoot(display.ID))
				var target device.RawNode
				switch name {
				case "d1a-ambiguous-external", "d1a-ambiguous-external-protected":
					target = proposalNode(0, 0, 0, display, bounds, "android.widget.FrameLayout", "requested")
					target.ResourceSet, target.ResourceID = true, "app:id/requested"
					actRebindTestAppend(&capture, 0, target)
					first := revalEditText(0, 0, 0, display.ID,
						device.Rect{Left: 100, Top: 300, Right: 800, Bottom: 425}, "", "", "app:id/editor", false)
					protected := name == "d1a-ambiguous-external-protected"
					second := tapTextProposalEditable(0, 0, 0, display,
						device.Rect{Left: 100, Top: 460, Right: 200, Bottom: 500}, true, protected)
					second.Enabled = protected
					actRebindTestAppend(&capture, 1, first)
					actRebindTestAppend(&capture, 1, second)
				case "d1a-single-unusable-external":
					target = proposalNode(0, 0, 0, display, bounds, "android.widget.FrameLayout", "requested")
					target.ResourceSet, target.ResourceID = true, "app:id/requested"
					actRebindTestAppend(&capture, 0, target)
					only := tapTextProposalEditable(0, 0, 0, display,
						device.Rect{Left: 100, Top: 300, Right: 800, Bottom: 425}, true, true)
					actRebindTestAppend(&capture, 1, only)
				default:
					ancestor := proposalClickableNode(0, 0, 0, display, bounds, "android.widget.Button", "button")
					if name != "d1b-interactive-ancestor-external" {
						ancestor = tapTextProposalEditable(0, 0, 0, display, bounds, true, name == "d1b-protected-ancestor")
						ancestor.Enabled = name != "d1b-disabled-ancestor"
					}
					actRebindTestAppend(&capture, 0, ancestor)
					target = proposalNode(0, 0, 0, display, bounds, "android.widget.TextView", "requested")
					target.ResourceSet, target.ResourceID = true, "app:id/requested"
					actRebindTestAppend(&capture, 1, target)
					if name == "d1b-blocked-ancestor" {
						cover := proposalClickableNode(0, 0, 0, display, bounds, "android.widget.Button", "cover")
						actRebindTestAppend(&capture, 1, cover)
					}
				}
				external := revalEditText(0, 0, 0, display.ID, bounds, "", "", "app:id/external", true)
				actRebindTestAppend(&capture, 0, external)
				return capture
			})
			h, rec := newRevalHarness(t)
			dev := &textEvDevice{revalDevice: rec}
			h.engine.dev = dev
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			target := staticRecoveryNodeByResource(t, state, "app:id/requested")
			external := staticRecoveryNodeByResource(t, state, "app:id/external")
			autoSubmit := false
			result := h.actWith(t, "PtextFallbackStop", textFlowRequest(start.Observation.ID, target.Ref, "hello", &autoSubmit))
			details := proposalRequireStaticReason(t, result, protocol.CodeTextTargetUnknown, textReasonTargetNotEditable)
			requireTextDetail(t, details, "stage", "target")
			requireTextDispatch(t, details, "not_attempted", "not_attempted", "not_attempted")
			proposalRequireCandidateExecutable(t, proposalCandidates(t, details), external.Ref)
			requireTextRefNoTapDispatch(t, dev, 0, 0, 0)
			requireReadyObservation(t, h, start.Observation.ID)
		})
	}
}

func TestStaticTapPassiveRecoveryAndDryRun(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "passive-image-and-title"
		if external {
			name = "external-same-depth-peer"
		}
		t.Run(name, func(t *testing.T) {
			image := pruningImageRaw("app:id/passive_image", tapCardBounds)
			label := tapRawNode(0, 0, 0, 0, tapCardBounds, "android.widget.TextView", "title", false, false)
			step := tapCardWithChildrenStep(tapCardBounds, image, label)
			if external {
				peer := tapRawNode(0, 0, 0, 0, tapCardBounds, "android.widget.TextView", "external peer", false, false)
				step = tapWithExternalPeer(step, peer)
			}
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			card := tapCardNode(t, h)
			op := staticRecoveryOperation("tap", card.Ref)
			_, _, plan, apiErr := staticTapRefCheck(state, &op)
			view, viewErr := collectTapPublicView(state)
			if viewErr != nil {
				t.Fatal(viewErr)
			}
			candidates := buildStaticCandidates(state, view, &op, []candidateEntry{{node: card, relation: candidateRelationChild}})
			if len(candidates) != 1 || candidates[0]["executable"] != !external {
				t.Fatalf("dry-run candidates = %#v, external=%t", candidates, external)
			}
			if external {
				if apiErr == nil || apiErr.Code != protocol.CodeNoSafePoint || apiErr.Details["reason"] != "ambiguous" {
					t.Fatalf("external overlap: plan=%+v error=%+v", plan, apiErr)
				}
				return
			}
			if apiErr != nil || plan == nil || plan.carrierRef != "" || plan.carrierRaw != nil || plan.strategy != tapModeDefaultCenter {
				t.Fatalf("passive recovery must be unbound: plan=%+v error=%+v", plan, apiErr)
			}
			labelNode := proposalFindLabel(t, state.tree().Root, "title")
			bubble := staticRecoveryOperation("tap", labelNode.Ref)
			_, _, bubblePlan, apiErr := staticTapRefCheck(state, &bubble)
			if apiErr != nil || bubblePlan == nil || bubblePlan.bubbleAncestorRef != card.Ref || bubblePlan.carrierRaw != nil {
				t.Fatalf("passive bubble must be unbound: plan=%+v error=%+v", bubblePlan, apiErr)
			}
			view, _ = collectTapPublicView(state)
			input, inputErr := buildTapInput(state, view, card)
			if inputErr != nil {
				t.Fatal(inputErr)
			}
			scope, scopeErr := collectTapScope(input)
			if scopeErr != nil {
				t.Fatal(scopeErr)
			}
			if fixed := scope.carrierByNode[labelNode]; fixed == nil || pointHitsFixedCarrier(scope, plan.x, plan.y, fixed) {
				t.Fatal("fixed carrier proof accepted a tied passive hit")
			}
		})
	}
}

func TestStaticTapPassiveRecoveryRejectsShallowerInteractiveOwner(t *testing.T) {
	step := proposalCaptureStep(func(display device.Display) device.Capture {
		image := pruningImageRaw("app:id/passive_image", tapCardBounds)
		label := tapRawNode(0, 0, 0, 0, tapCardBounds, "android.widget.TextView", "title", false, false)
		capture := tapPageWithChildren(display, tapCardBounds, image, label)
		cover := proposalClickableNode(0, 0, 0, display, tapCardBounds, "android.widget.Button", "shallower cover")
		actRebindTestAppend(&capture, 0, cover)
		return capture
	})
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(step)
	start := h.startSession()
	state := observationStateByID(t, h.engine, start.Observation.ID)
	card := tapCardNode(t, h)
	view, apiErr := collectTapPublicView(state)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	input, apiErr := buildTapInput(state, view, card)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	scope, apiErr := collectTapScope(input)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	x, y := tapCardBounds.Center()
	hits := resolvePointSet(view.tree.Root, x, y)
	if len(hits) != 2 || scope.carrierByNode[hits[0]] == nil || scope.carrierByNode[hits[1]] == nil {
		t.Fatalf("fixture must have two deepest passive carriers, got %v", hits)
	}
	cover := proposalFindLabel(t, view.tree.Root, "shallower cover")
	if owners := externalInteractiveOwnersAt(view, card, x, y); len(owners) != 1 || owners[0] != cover {
		t.Fatalf("fixture must have one shallower external owner, got %v", owners)
	}
	result := h.actWith(t, "PpassiveShallowerCover", tapRefRequest(start.Observation.ID, card.Ref))
	proposalRequireStaticReason(t, result, protocol.CodeNoSafePoint, "ambiguous")
	proposalRequireNoDispatch(t, h)
	requireReadyObservation(t, h, start.Observation.ID)
}

func staticResidualBlockers(holeWidth, holeHeight int) []device.RawNode {
	var blockers []device.RawNode
	for _, bounds := range []device.Rect{
		{Left: holeWidth, Right: 150, Bottom: 300},
		{Left: 150, Right: 300, Bottom: 300},
		{Top: holeHeight, Right: holeWidth, Bottom: 300},
	} {
		blockers = append(blockers, tapRawNode(0, 0, 0, 0, bounds, "android.widget.Button", "blocker", true, false))
	}
	return blockers
}

func TestStaticTapResidualPatch(t *testing.T) {
	for _, tc := range []struct {
		name, bubble          string
		holeWidth, holeHeight int
		wantPatch, overLimit  bool
	}{
		{name: "safe-patch", holeWidth: 20, holeHeight: 16, wantPatch: true},
		{name: "bubble-ancestor-patch", bubble: "ancestor", holeWidth: 20, holeHeight: 16, wantPatch: true},
		{name: "bubble-colocated-patch", bubble: "colocated", holeWidth: 20, holeHeight: 16, wantPatch: true},
		{name: "eight-pixel-gap", holeWidth: 8, holeHeight: 8},
		{name: "edge-budget", holeWidth: 20, holeHeight: 16, overLimit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds := device.Rect{Right: 300, Bottom: 300}
			children := staticResidualBlockers(tc.holeWidth, tc.holeHeight)
			if tc.overLimit {
				for x := 30; x < 160; x += 2 {
					bounds := device.Rect{Left: x, Right: x + 1, Bottom: 300}
					children = append(children, tapRawNode(0, 0, 0, 0, bounds, "android.widget.Button", "blocker", true, false))
				}
			}
			step := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := tapPageWithChildren(display, bounds, children...)
				if tc.bubble != "" {
					parent := 0
					if tc.bubble == "ancestor" {
						parent = 1
					}
					label := tapRawNode(0, 0, 0, 0, bounds, "android.widget.TextView", "label", false, false)
					actRebindTestAppend(&capture, parent, label)
				}
				return capture
			})
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			card := tapPublicBoundsNode(state.tree().Root, bounds)
			if card == nil {
				t.Fatal("card projection missing")
			}
			target := card
			action := "tap"
			if tc.bubble != "" {
				target = proposalFindLabel(t, state.tree().Root, "label")
			}
			if tc.bubble == "colocated" {
				action = "long_press"
			}
			op := staticRecoveryOperation(action, target.Ref)
			_, _, plan, apiErr := staticTapRefCheck(state, &op)
			if tc.wantPatch {
				if apiErr != nil || plan == nil || plan.strategy != tapModeResidualPatch || plan.x != 10 || plan.y != 8 {
					t.Fatalf("static plan=%+v error=%+v, want residual patch center (10, 8)", plan, apiErr)
				}
				if tc.bubble == "ancestor" && plan.bubbleAncestorRef != card.Ref {
					t.Fatalf("residual point lost bubble ancestor %s: %+v", card.Ref, plan)
				}
				if tc.bubble == "colocated" && (plan.colocatedRaw == nil || !plan.colocatedInChild) {
					t.Fatalf("residual point lost co-located visual binding: %+v", plan)
				}
				return
			}
			if apiErr == nil || apiErr.HTTPStatus != 422 || apiErr.Code != protocol.CodeNoSafePoint || apiErr.Details["reason"] != "blocked" {
				t.Fatalf("static plan=%+v error=%+v, want no_safe_point (blocked)", plan, apiErr)
			}
		})
	}
}

func TestStaticTapResidualPatchRejectsExternalInteractiveCover(t *testing.T) {
	bounds := device.Rect{Right: 300, Bottom: 300}
	hole := device.Rect{Right: 20, Bottom: 16}
	step := proposalCaptureStep(func(display device.Display) device.Capture {
		capture := tapPageWithChildren(display, bounds, staticResidualBlockers(hole.Width(), hole.Height())...)
		cover := proposalClickableNode(0, 0, 0, display, hole, "android.widget.Button", "external cover")
		actRebindTestAppend(&capture, 0, cover)
		return capture
	})
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(step)
	start := h.startSession()
	state := observationStateByID(t, h.engine, start.Observation.ID)
	card := tapPublicBoundsNode(state.tree().Root, bounds)
	if card == nil {
		t.Fatal("card projection missing")
	}
	probes, captures := h.device.probeCount(), h.device.captureCount()
	result := h.actWith(t, "PresidualExternalCover", tapRefRequest(start.Observation.ID, card.Ref))
	proposalRequireStaticReason(t, result, protocol.CodeNoSafePoint, "blocked")
	proposalRequireNoDispatch(t, h)
	if h.device.probeCount() != probes || h.device.captureCount() != captures {
		t.Fatalf("static rejection did device work: probes=%d captures=%d", h.device.probeCount()-probes, h.device.captureCount()-captures)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestStaticCoextensiveTapWithBadgeHonorsBubbleFalse(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 400, Right: 220, Bottom: 520}
	badgeBounds := device.Rect{Left: bounds.Left, Top: bounds.Top, Right: bounds.Left + 20, Bottom: bounds.Bottom}
	step := proposalCaptureStep(func(display device.Display) device.Capture {
		parent := proposalClickableNode(1, 0, 1, display, bounds, "android.widget.FrameLayout", "parent")
		parent.Children = []int{2, 3}
		delegate := proposalClickableNode(2, 1, 2, display, bounds, "android.widget.Button", "delegate")
		delegate.HasLongClick, delegate.LongClickable = true, true
		badge := proposalClickableNode(3, 1, 2, display, badgeBounds, "android.widget.Button", "badge")
		return revalCapture(display, revalRoot(display.ID, 1), parent, delegate, badge)
	})
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(step)
	start := h.startSession()
	state := observationStateByID(t, h.engine, start.Observation.ID)
	root := state.tree().Root
	parent := proposalFindLabel(t, root, "parent")
	delegate := proposalFindLabel(t, root, "delegate")
	tap := staticRecoveryOperation("tap", parent.Ref)
	tap.Bubble, tap.HasBubble = false, true
	_, _, plan, apiErr := staticTapRefCheck(state, &tap)
	if apiErr != nil || plan == nil || plan.strategy != tapModeCoextensiveChild || plan.delegateParentOtherAction ||
		plan.delegateRaw != &state.Projection.Capture.Nodes[state.Projection.Source[delegate.Ref]] ||
		!bounds.Contains(plan.x, plan.y) || badgeBounds.Contains(plan.x, plan.y) {
		t.Fatalf("tap plan=%+v error=%+v, want delegate %s and a point outside the badge", plan, apiErr, delegate.Ref)
	}
	longPress := staticRecoveryOperation("long_press", parent.Ref)
	_, _, b1Plan, apiErr := staticTapRefCheck(state, &longPress)
	if apiErr != nil || b1Plan == nil || b1Plan.strategy != tapModeCoextensiveChild || !b1Plan.delegateParentOtherAction {
		t.Fatalf("long_press plan=%+v error=%+v, want B1 with default bubble", b1Plan, apiErr)
	}
	longPress.Bubble, longPress.HasBubble = false, true
	result := h.actP(t, "PcoextBadgeB1BubbleOff", start.Observation.ID, longPress)
	proposalRequireStaticReason(t, result, protocol.CodeNodeUnsupported, "target_has_other_action")
	proposalRequireNoDispatch(t, h)
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestStaticCoextensiveTapRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, action, reject                           string
		badge, badgeAtCenter                           int
		bubbleOff, childInput, protected, ancestorOnly bool
	}{
		{name: "text-child-input", action: "text", childInput: true},
		{name: "text-child-button", action: "text", reject: "blocked"},
		{name: "text-protected-child", action: "text", childInput: true, protected: true, reject: "blocked"},
		{name: "tap-small-badge", action: "tap", badge: 20},
		{name: "tap-quarter-boundary", action: "tap", badge: 30},
		{name: "tap-large-split", action: "tap", badge: 40, reject: "blocked"},
		{name: "tap-badge-over-point", action: "tap", badgeAtCenter: 20, reject: "blocked"},
		{name: "long-press-child", action: "long_press"},
		{name: "long-press-without-bubble", action: "long_press", bubbleOff: true, reject: "target_has_other_action"},
		{name: "long-press-ancestor-only", action: "long_press", ancestorOnly: true, reject: "target_has_other_action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds := device.Rect{Left: 100, Top: 400, Right: 220, Bottom: 520}
			step := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := revalCapture(display, revalRoot(display.ID))
				parent := proposalClickableNode(0, 0, 0, display, bounds, "android.widget.FrameLayout", "parent")
				if tc.action == "text" {
					parent = tapTextProposalEditable(0, 0, 0, display, bounds, false, false)
				}
				parent.ResourceSet, parent.ResourceID = true, "app:id/parent"
				delegate := proposalClickableNode(0, 0, 0, display, bounds, "android.widget.Button", "delegate")
				if tc.childInput {
					delegate = tapTextProposalEditable(0, 0, 0, display, bounds, true, tc.protected)
				}
				delegate.ResourceSet, delegate.ResourceID = true, "app:id/delegate"
				if tc.action == "long_press" {
					delegate.HasLongClick, delegate.LongClickable = true, true
				}
				if tc.ancestorOnly {
					actRebindTestAppend(&capture, 0, delegate)
					actRebindTestAppend(&capture, 1, parent)
					return capture
				}
				actRebindTestAppend(&capture, 0, parent)
				actRebindTestAppend(&capture, 1, delegate)
				if tc.badge > 0 || tc.badgeAtCenter > 0 {
					badgeBounds := bounds
					badgeBounds.Right = bounds.Left + tc.badge
					if tc.badgeAtCenter > 0 {
						badgeBounds.Left = 150
						badgeBounds.Right = badgeBounds.Left + tc.badgeAtCenter
					}
					badge := proposalClickableNode(0, 0, 0, display, badgeBounds, "android.widget.Button", "badge")
					actRebindTestAppend(&capture, 1, badge)
				}
				return capture
			})
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			parent := staticRecoveryNodeByResource(t, state, "app:id/parent")
			delegate := staticRecoveryNodeByResource(t, state, "app:id/delegate")
			op := staticRecoveryOperation(tc.action, parent.Ref)
			if tc.bubbleOff {
				op.Bubble, op.HasBubble = false, true
			}
			var plan *tapPlan
			var apiErr *protocol.APIError
			if tc.action == "text" {
				_, _, plan, apiErr = staticTextRefCheck(state, &op)
			} else {
				_, _, plan, apiErr = staticTapRefCheck(state, &op)
			}
			if tc.reject != "" {
				if apiErr == nil || apiErr.HTTPStatus != 422 || apiErr.Details["reason"] != tc.reject {
					t.Fatalf("static plan=%+v error=%+v, want %s", plan, apiErr, tc.reject)
				}
				return
			}
			if apiErr != nil || plan == nil || plan.strategy != tapModeCoextensiveChild || plan.targetRef != parent.Ref ||
				plan.delegateRaw != &state.Projection.Capture.Nodes[state.Projection.Source[delegate.Ref]] ||
				plan.delegateParentOtherAction != (tc.action == "long_press") {
				t.Fatalf("static plan=%+v error=%+v, want delegate %s", plan, apiErr, delegate.Ref)
			}
		})
	}
}

func TestStaticColocatedTapRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		innerUnderOuter, outerOpensInside bool
	}{
		{name: "in-child-over-outside", innerUnderOuter: true},
		{name: "innermost-within-child", innerUnderOuter: true, outerOpensInside: true},
		{name: "competing-peers", outerOpensInside: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds := device.Rect{Left: 400, Top: 900, Right: 600, Bottom: 1100}
			step := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := revalCapture(display, revalRoot(display.ID))
				label := proposalNode(0, 0, 0, display, bounds, "android.widget.TextView", "badge")
				outer := proposalClickableNode(0, 0, 0, display,
					device.Rect{Left: 250, Top: 850, Right: 750, Bottom: 1150}, "android.widget.Button", "outer")
				innerBounds := bounds
				if tc.outerOpensInside {
					innerBounds.Left += 50
				}
				inner := proposalClickableNode(0, 0, 0, display, innerBounds, "android.widget.Button", "inner")
				actRebindTestAppend(&capture, 0, label)
				actRebindTestAppend(&capture, 0, outer)
				parent := 0
				if tc.innerUnderOuter {
					parent = 2
				}
				actRebindTestAppend(&capture, parent, inner)
				return capture
			})
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(step)
			start := h.startSession()
			state := observationStateByID(t, h.engine, start.Observation.ID)
			label := proposalFindLabel(t, state.tree().Root, "badge")
			inner := proposalFindLabel(t, state.tree().Root, "inner")
			op := staticRecoveryOperation("tap", label.Ref)
			_, _, plan, apiErr := staticTapRefCheck(state, &op)
			if !tc.innerUnderOuter {
				if apiErr == nil || apiErr.Code != protocol.CodeNoSafePoint || apiErr.Details["reason"] != "colocated_ambiguous" {
					t.Fatalf("static plan=%+v error=%+v, want colocated_ambiguous", plan, apiErr)
				}
				outer := proposalFindLabel(t, state.tree().Root, "outer")
				proposalRequireCandidateRefs(t, proposalCandidates(t, apiErr.Details), outer.Ref, inner.Ref)
				return
			}
			if apiErr != nil || plan == nil || plan.strategy != tapModeBubbleColocated || !plan.colocatedInChild ||
				plan.colocatedRaw != &state.Projection.Capture.Nodes[state.Projection.Source[inner.Ref]] {
				t.Fatalf("static plan=%+v error=%+v, want inner child %s", plan, apiErr, inner.Ref)
			}
		})
	}
}
