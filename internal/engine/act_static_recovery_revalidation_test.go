package engine

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func passiveRecoveryRevalidationPage(display device.Display, colocated, blocked bool) device.Capture {
	bounds := device.Rect{Left: 200, Top: 300, Right: 500, Bottom: 600}
	image := pruningImageRaw("app:id/passive_image", bounds)
	title := tapRawNode(0, 0, 0, display.ID, bounds, "android.widget.TextView", "title", false, false)
	capture := tapPageWithChildren(display, bounds, image, title)
	if colocated {
		label := proposalNode(0, 0, 0, display, bounds, "android.widget.TextView", "visual anchor")
		actRebindTestAppend(&capture, 0, label)
	}
	if blocked {
		blocker := revalButton(0, 0, 0, display.ID,
			device.Rect{Left: 300, Top: 400, Right: 400, Bottom: 500}, "cover", "", "cover")
		actRebindTestAppend(&capture, 1, blocker)
	}
	return capture
}

func TestActRevalidatePassiveTapRecovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		colocated bool
		movePoint bool
	}{
		{name: "keep original passive point"},
		{name: "reselect unbound direct point", movePoint: true},
		{name: "reselect colocated point", colocated: true, movePoint: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTapScopeHarness(t)
			page := func(blocked bool) captureStep {
				return proposalCaptureStep(func(display device.Display) device.Capture {
					return passiveRecoveryRevalidationPage(display, tc.colocated, blocked)
				})
			}
			h.device.scriptCaptures(page(false))
			start := h.startSession()
			obs := observationStateByID(t, h.engine, start.Observation.ID)
			target := mustFindFirstNodeWithAction(t, obs.tree().Root, protocol.ActionTap)
			if tc.colocated {
				target = proposalFindLabel(t, obs.tree().Root, "visual anchor")
			}
			request := h.tapRefRequest(t, start.Observation.ID, target.Ref)
			_, _, plan, apiErr := staticTapRefCheck(obs, &request.Operation)
			if apiErr != nil || plan == nil || plan.carrierRaw != nil ||
				(plan.colocatedRaw != nil) != tc.colocated || plan.x != 350 || plan.y != 450 {
				t.Fatalf("static plan = %+v, error = %+v, want unbound point (350,450)", plan, apiErr)
			}
			fresh := page(tc.movePoint)
			capture, err := fresh(h.device, obs.Screen)
			if err != nil {
				t.Fatal(err)
			}
			frame, apiErr := h.engine.revalidateTapTargetFrame(context.Background(), nil, nil, obs, &capture, plan)
			if apiErr != nil || frame == nil || frame.carrier != nil {
				t.Fatalf("fresh frame = %+v, error = %+v, want an unbound passive point", frame, apiErr)
			}
			if moved := frame.x != plan.x || frame.y != plan.y; moved != tc.movePoint {
				t.Fatalf("fresh point = (%d,%d), want moved=%t from (350,450)", frame.x, frame.y, tc.movePoint)
			}
			h.device.scriptCaptures(fresh)
			result := h.actWith(t, "Ppassiverevalidation", request)
			proposalRequireExecuted(t, result)
			proposalRequireTap(t, h, [2]int{frame.x, frame.y})
		})
	}
}

func TestActRevalidateLowTierPassiveNeedsGuidanceProof(t *testing.T) {
	for _, tc := range []struct {
		name               string
		colocated, overlay bool
	}{
		{name: "direct clear"},
		{name: "direct covered", overlay: true},
		{name: "colocated clear", colocated: true},
		{name: "colocated covered", colocated: true, overlay: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := passiveRecoveryRevalidationPage(display, tc.colocated, false)
				if tc.overlay {
					actRebindTestAppend(&capture, 0, revalButton(0, 0, 0, display.ID,
						capture.Nodes[1].Bounds, "overlay", "", "overlay"))
				}
				return capture
			})
			checkLowTierPassiveRevalidation(t, tc.colocated, fresh, tc.overlay)
		})
	}
}

func TestActRevalidatePassiveRejectsSameDepthExternalPeer(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		colocated, clickable bool
	}{
		{name: "direct peer under another interactive ancestor", clickable: true},
		{name: "colocated peer under another interactive ancestor", colocated: true, clickable: true},
		{name: "direct peer with no external interactive owner"},
		{name: "colocated peer with no external interactive owner", colocated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := passiveRecoveryRevalidationPage(display, tc.colocated, false)
				bounds := capture.Nodes[1].Bounds
				wrapper := proposalNode(0, 0, 0, display, bounds, "android.widget.FrameLayout", "external wrapper")
				wrapperIndex := len(capture.Nodes)
				if tc.clickable {
					wrapper.Clickable, wrapper.HasClickable = true, true
				}
				actRebindTestAppend(&capture, 0, wrapper)
				peer := proposalNode(0, 0, 0, display, bounds, "android.widget.TextView", "external peer")
				actRebindTestAppend(&capture, wrapperIndex, peer)
				return capture
			})
			checkLowTierPassiveRevalidation(t, tc.colocated, fresh, true)
		})
	}
}

func checkLowTierPassiveRevalidation(t *testing.T, colocated bool, fresh captureStep, blocked bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
			return passiveRecoveryRevalidationPage(display, colocated, false)
		}))
		start := h.startSession()
		obs := observationStateByID(t, h.engine, start.Observation.ID)
		target := mustFindFirstNodeWithAction(t, obs.tree().Root, protocol.ActionTap)
		if colocated {
			target = proposalFindLabel(t, obs.tree().Root, "visual anchor")
		}
		request := h.tapRefRequest(t, start.Observation.ID, target.Ref)
		_, _, plan, apiErr := staticTapRefCheck(obs, &request.Operation)
		if apiErr != nil || plan == nil || plan.carrierRaw != nil || (plan.colocatedRaw != nil) != colocated {
			t.Fatalf("static plan = %+v, error = %+v, want an unbound passive point", plan, apiErr)
		}
		capture, err := fresh(h.device, obs.Screen)
		if err != nil {
			t.Fatal(err)
		}
		match, state := h.engine.probeTapFrameNode(context.Background(), nil, obs.Projection.Capture,
			&capture, plan.targetRaw, !colocated, tapFrameOptions{})
		if state != actProbeUnique || match.tier != 1 {
			t.Fatalf("target binding state = %v, tier = %d, want unique tier 1", state, match.tier)
		}
		if colocated {
			match, state := h.engine.probeTapFrameNode(context.Background(), nil, obs.Projection.Capture,
				&capture, plan.colocatedRaw, true, tapFrameOptions{})
			if state != actProbeUnique || match.tier != 1 {
				t.Fatalf("execution binding state = %v, tier = %d, want unique tier 1", state, match.tier)
			}
		}
		h.device.scriptCaptures(fresh)
		result := h.actWith(t, "Plowtierpassive", request)
		if blocked {
			requireActRejectionDetails(t, result, "tap", target.Ref, targetChangedReasonScopeBlocked)
			proposalRequireNoDispatch(t, h)
			return
		}
		proposalRequireExecuted(t, result)
		proposalRequireTap(t, h, [2]int{plan.x, plan.y})
	})
}

func TestTier4PassivePathStopsAtBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(target, middle, hit *protocol.Node)
	}{
		{name: "passive path"},
		{name: "interactive intermediate", set: func(_, middle, _ *protocol.Node) {
			middle.Actions = []protocol.Action{protocol.ActionTap}
		}},
		{name: "context intermediate", set: func(_, middle, _ *protocol.Node) { middle.Context = true }},
		{name: "context hit", set: func(_, _, hit *protocol.Node) { hit.Context = true }},
		{name: "interactive hit", set: func(_, _, hit *protocol.Node) {
			hit.Actions = []protocol.Action{protocol.ActionTap}
		}},
		{name: "context target", set: func(target, _, _ *protocol.Node) { target.Context = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := &protocol.Node{Actions: []protocol.Action{protocol.ActionTap}}
			middle, hit := &protocol.Node{}, &protocol.Node{}
			view := &tapPublicView{info: map[*protocol.Node]*tapPublicInfo{
				target: {}, middle: {parent: target}, hit: {parent: middle},
			}}
			if tc.set != nil {
				tc.set(target, middle, hit)
			}
			if got, want := hasValidDirectNonInteractivePath(view, target, hit), tc.set == nil; got != want {
				t.Fatalf("passive path = %t, want %t", got, want)
			}
		})
	}
}

func residualRevalidationPage(display device.Display, top, bottom int) device.Capture {
	bounds := device.Rect{Right: 300, Bottom: 300}
	child := func(rect device.Rect, label string) device.RawNode {
		return tapRawNode(0, 0, 0, 0, rect, "android.widget.Button", label, true, false)
	}
	children := []device.RawNode{
		child(device.Rect{Left: 20, Right: 150, Bottom: 300}, "left"),
		child(device.Rect{Left: 150, Right: 300, Bottom: 300}, "right"),
		child(device.Rect{Top: bottom, Right: 20, Bottom: 300}, "edge"),
	}
	if top > 0 {
		children = append(children, child(device.Rect{Right: 20, Bottom: top}, "top"))
	}
	return tapPageWithChildren(display, bounds, children...)
}

func TestActRevalidateResidualTapRejectsNewExternalOverlay(t *testing.T) {
	for _, tc := range []struct {
		name    string
		overlay bool
	}{{name: "clear"}, {name: "covered", overlay: true}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTapScopeHarness(t)
				page := func(overlay bool) captureStep {
					return proposalCaptureStep(func(display device.Display) device.Capture {
						capture := residualRevalidationPage(display, 0, 16)
						if overlay {
							actRebindTestAppend(&capture, 0, revalButton(0, 0, 0, display.ID,
								device.Rect{Right: 20, Bottom: 16}, "overlay", "", "overlay"))
						}
						return capture
					})
				}
				h.device.scriptCaptures(page(false))
				start := h.startSession()
				obs := observationStateByID(t, h.engine, start.Observation.ID)
				card := mustFindFirstNodeWithAction(t, obs.tree().Root, protocol.ActionTap)
				request := h.tapRefRequest(t, start.Observation.ID, card.Ref)
				_, _, plan, apiErr := staticTapRefCheck(obs, &request.Operation)
				if apiErr != nil || plan == nil || !plan.residual || plan.x != 10 || plan.y != 8 {
					t.Fatalf("static plan = %+v, error = %+v, want residual point (10,8)", plan, apiErr)
				}
				fresh := page(tc.overlay)
				capture, err := fresh(h.device, obs.Screen)
				if err != nil {
					t.Fatal(err)
				}
				match, state := h.engine.probeTapFrameNode(context.Background(), nil, obs.Projection.Capture,
					&capture, plan.targetRaw, true, tapFrameOptions{})
				if state != actProbeUnique || match.tier != 1 {
					t.Fatalf("target binding state = %v, tier = %d, want unique tier 1", state, match.tier)
				}
				h.device.scriptCaptures(fresh)
				result := h.actWith(t, "Presidualoverlay", request)
				if tc.overlay {
					requireActRejectionDetails(t, result, "tap", card.Ref, targetChangedReasonScopeBlocked)
					proposalRequireNoDispatch(t, h)
					return
				}
				proposalRequireExecuted(t, result)
				proposalRequireTap(t, h, [2]int{10, 8})
			})
		})
	}
}

func TestActRevalidateResidualTapRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                string
		holeTop, holeBottom int
		want                [2]int
	}{
		{name: "keep residual point", holeBottom: 16, want: [2]int{10, 8}},
		{name: "reselect residual point", holeTop: 20, holeBottom: 36, want: [2]int{10, 28}},
		{name: "closed patch is blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := func(top, bottom int) captureStep {
				return proposalCaptureStep(func(display device.Display) device.Capture {
					return residualRevalidationPage(display, top, bottom)
				})
			}
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(page(0, 16))
			start := h.startSession()
			obs := observationStateByID(t, h.engine, start.Observation.ID)
			card := mustFindFirstNodeWithAction(t, obs.tree().Root, protocol.ActionTap)
			request := h.tapRefRequest(t, start.Observation.ID, card.Ref)
			_, _, plan, apiErr := staticTapRefCheck(obs, &request.Operation)
			if apiErr != nil || plan == nil || plan.strategy != tapModeResidualPatch || plan.x != 10 || plan.y != 8 {
				t.Fatalf("static plan = %+v, error = %+v, want residual point (10,8)", plan, apiErr)
			}
			h.device.scriptCaptures(page(tc.holeTop, tc.holeBottom))
			result := h.actWith(t, "Presidualrevalidation", request)
			if tc.holeBottom == 0 {
				requireActRejectionDetails(t, result, "tap", card.Ref, targetChangedReasonScopeBlocked)
				proposalRequireNoDispatch(t, h)
				return
			}
			proposalRequireExecuted(t, result)
			proposalRequireTap(t, h, tc.want)
		})
	}
}

func TestActRevalidateCoextensiveRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, kind, reason string
		moveBadge          bool
	}{
		{name: "B1 other action", kind: "long_press"},
		{name: "E3a editable delegate", kind: "text"},
		{name: "E3a delegate missing", kind: "text", reason: targetChangedReasonTargetMissing},
		{name: "E3b clear small badge", kind: "tap"},
		{name: "E3b small badge covers tier 1 point", kind: "tap", moveBadge: true, reason: targetChangedReasonScopeBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bounds := device.Rect{Left: 100, Top: 400, Right: 220, Bottom: 520}
				page := func(fresh bool) captureStep {
					return proposalCaptureStep(func(display device.Display) device.Capture {
						parent := proposalClickableNode(1, 0, 1, display, bounds, "android.widget.FrameLayout", "parent")
						child := revalButton(2, 1, 2, display.ID, bounds, "delegate", "", "delegate")
						if tc.kind == "text" {
							parent = revalEditText(1, 0, 1, display.ID, bounds, "", "", "parent", false)
							child = revalEditText(2, 1, 2, display.ID, bounds, "", "", "delegate", true)
						}
						if fresh && tc.reason == targetChangedReasonTargetMissing {
							return revalCapture(display, revalRoot(display.ID, 1), parent)
						}
						parent.Children = []int{2}
						capture := revalCapture(display, revalRoot(display.ID, 1), parent, child)
						if tc.kind == "tap" {
							badge := device.Rect{Left: 100, Top: 400, Right: 120, Bottom: 420}
							if fresh && tc.moveBadge {
								badge = device.Rect{Left: 150, Top: 450, Right: 170, Bottom: 470}
							}
							actRebindTestAppend(&capture, 1, revalButton(0, 0, 0, display.ID, badge, "badge", "", "badge"))
						}
						return capture
					})
				}
				h := newTapScopeHarness(t)
				recorder := &revalDevice{fakeDevice: h.device}
				h.engine.dev = recorder
				h.device.scriptCaptures(page(false))
				start := h.startSession()
				obs := observationStateByID(t, h.engine, start.Observation.ID)
				parent := mustFindNodeByTextOrResource(t, obs.tree().Root, "parent")
				request := h.tapRefRequest(t, start.Observation.ID, parent.Ref)
				request.Operation.Type = tc.kind
				var plan *tapPlan
				var apiErr *protocol.APIError
				if tc.kind == "text" {
					writeOnly := false
					request = textFlowRequest(start.Observation.ID, parent.Ref, "delegated text", &writeOnly)
					_, _, plan, apiErr = staticTextRefCheck(obs, &request.Operation)
				} else {
					_, _, plan, apiErr = staticTapRefCheck(obs, &request.Operation)
				}
				if apiErr != nil || plan == nil || plan.delegateRaw == nil || plan.strategy != tapModeCoextensiveChild ||
					plan.delegateParentOtherAction != (tc.kind == "long_press") {
					t.Fatalf("static plan = %+v, error = %+v, want coextensive child delegation", plan, apiErr)
				}
				fresh := page(true)
				if tc.moveBadge {
					capture, err := fresh(h.device, obs.Screen)
					if err != nil {
						t.Fatal(err)
					}
					for _, binding := range []*device.RawNode{plan.targetRaw, plan.delegateRaw} {
						match, state := h.engine.probeTapFrameNode(context.Background(), nil, obs.Projection.Capture,
							&capture, binding, binding == plan.delegateRaw, tapFrameOptions{})
						if state != actProbeUnique || match.tier != 1 {
							t.Fatalf("binding state = %v, tier = %d, want unique tier 1", state, match.tier)
						}
					}
				}
				h.device.scriptCaptures(fresh)
				result := h.actWith(t, "Pcoextensiverevalidation", request)
				if tc.reason != "" {
					requireActRejectionDetails(t, result, tc.kind, parent.Ref, tc.reason)
					requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry)
					proposalRequireNoDispatch(t, h)
					if len(recorder.recordedSetTextTargets()) != 0 || len(h.device.keys) != 0 {
						t.Fatalf("rejected delegation dispatched text or a key: writes=%v keys=%v", h.device.texts, h.device.keys)
					}
					return
				}
				proposalRequireExecuted(t, result)
				switch tc.kind {
				case "text":
					writes := recorder.recordedSetTextTargets()
					if len(writes) != 1 || writes[0].text != request.Operation.Text ||
						writes[0].target != textTargetOf(plan.delegateRaw, obs.Screen) || len(h.device.keys) != 0 {
						t.Fatalf("writes = %+v, keys = %v, want one delegated write and no submit", writes, h.device.keys)
					}
				case "tap", "long_press":
					proposalRequireTap(t, h, [2]int{160, 460})
				}
			})
		})
	}
}

func TestActRevalidateCoextensiveKidBadgeKeepsParentTier4Proof(t *testing.T) {
	for _, peer := range []bool{false, true} {
		name := "clear"
		if peer {
			name = "same depth external peer"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				page := func(fresh bool) captureStep {
					return proposalCaptureStep(func(display device.Display) device.Capture {
						bounds := device.Rect{Left: 100, Top: 400, Right: 220, Bottom: 520}
						heading := transparentNode(1, 0, 1, display.ID, bounds, 2)
						heading.Text = "Old heading"
						if fresh {
							heading.Text = "New heading"
						}
						parent := revalButton(2, 1, 2, display.ID, bounds, "parent", "", "parent")
						parent.Children = []int{3}
						delegate := revalButton(3, 2, 3, display.ID, bounds, "delegate", "", "delegate")
						capture := revalCapture(display, revalRoot(display.ID, 1), heading, parent, delegate)
						if fresh {
							badge := device.Rect{Left: 100, Top: 400, Right: 120, Bottom: 420}
							actRebindTestAppend(&capture, 2, revalButton(0, 0, 0, display.ID, badge, "badge", "", "badge"))
						}
						if fresh && peer {
							branchParent := 0
							for _, label := range []string{"outside panel", "outside group", "external peer"} {
								branch := proposalNode(0, 0, 0, display, bounds, "android.widget.TextView", label)
								actRebindTestAppend(&capture, branchParent, branch)
								branchParent = len(capture.Nodes) - 1
							}
						}
						return capture
					})
				}
				h := newTapScopeHarness(t)
				h.device.scriptCaptures(page(false))
				start := h.startSession()
				obs := observationStateByID(t, h.engine, start.Observation.ID)
				parent := mustFindNodeByTextOrResource(t, obs.tree().Root, "parent")
				request := h.tapRefRequest(t, start.Observation.ID, parent.Ref)
				_, _, plan, apiErr := staticTapRefCheck(obs, &request.Operation)
				if apiErr != nil || plan == nil || plan.delegateRaw == nil || plan.strategy != tapModeCoextensiveChild {
					t.Fatalf("static plan = %+v, error = %+v, want coextensive delegation", plan, apiErr)
				}
				fresh := page(true)
				capture, err := fresh(h.device, obs.Screen)
				if err != nil {
					t.Fatal(err)
				}
				for _, binding := range []struct {
					raw       *device.RawNode
					execution bool
					wantTier  int
				}{{plan.targetRaw, false, 4}, {plan.delegateRaw, true, 3}} {
					match, state := h.engine.probeTapFrameNode(context.Background(), nil, obs.Projection.Capture,
						&capture, binding.raw, binding.execution, tapFrameOptions{})
					if state != actProbeUnique || match.tier != binding.wantTier {
						t.Fatalf("execution=%t state=%v tier=%d, want unique tier %d", binding.execution, state, match.tier, binding.wantTier)
					}
				}
				h.device.scriptCaptures(fresh)
				result := h.actWith(t, "Pmixedtiers", request)
				if peer {
					requireActRejectionDetails(t, result, "tap", parent.Ref, targetChangedReasonScopeBlocked)
					proposalRequireNoDispatch(t, h)
					return
				}
				proposalRequireExecuted(t, result)
				proposalRequireTap(t, h, [2]int{plan.x, plan.y})
			})
		})
	}
}

func TestPlanTextSubmitRefB1RejectsDisabledParent(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		parentEnabled, childEnabled bool
	}{
		{name: "disabled parent", childEnabled: true},
		{name: "enabled parent with initially disabled execution child", parentEnabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
				inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
				input := submitTestField(display, 1, inputBounds, "", "", "search_box", true)
				bounds := device.Rect{Left: 100, Top: 420, Right: 220, Bottom: 540}
				parent := transparentNode(2, 0, 1, display.ID, bounds, 3)
				parent.Text, parent.LongClickable, parent.Enabled = "Submit wrapper", true, tc.parentEnabled
				child := revalButton(3, 2, 2, display.ID, bounds, "Submit", "", "submit")
				child.Enabled = tc.childEnabled
				return revalCapture(display, revalRoot(display.ID, 1, 2), input, parent, child)
			}))
			start := h.startSession()
			obs := observationStateByID(t, h.engine, start.Observation.ID)
			root := obs.tree().Root
			input := mustFindFirstNodeWithAction(t, root, protocol.ActionText)
			parent := proposalFindLabel(t, root, "Submit wrapper")
			operation := submitTestTextOperation(input.Ref, parent.Ref)
			plan, apiErr := planTextSubmitRef(obs, operation)
			if tc.parentEnabled {
				if apiErr != nil || plan == nil || !plan.initiallyDisabled || !plan.tap.delegateParentOtherAction ||
					plan.tap.delegateRaw != &obs.Projection.Capture.Nodes[3] {
					t.Fatalf("submit plan=%+v error=%+v, want delegation to initially disabled button", plan, apiErr)
				}
			} else {
				if plan != nil || apiErr == nil || apiErr.Code != protocol.CodeNodeUnsupported ||
					apiErr.Details["reason"] != tapStaticReasonTargetHasOtherAction {
					t.Fatalf("submit plan=%+v error=%+v, want target_has_other_action", plan, apiErr)
				}
				request := &protocol.ActRequest{Reference: start.Observation.ID, Operation: *operation}
				result := h.actWith(t, "Pdisabledsubmitparent", request)
				proposalRequireStaticReason(t, result, protocol.CodeNodeUnsupported, tapStaticReasonTargetHasOtherAction)
				requireReadyObservation(t, h, start.Observation.ID)
			}
			proposalRequireNoDispatch(t, h)
			if len(h.device.texts) != 0 || len(h.device.keys) != 0 {
				t.Fatalf("submit planning dispatched text or key: writes=%v keys=%v", h.device.texts, h.device.keys)
			}
		})
	}
}

func TestActRevalidateNestedPointRecovery(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "nested owners"
		if conflict {
			name = "sibling owners"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.device.scriptCaptures(pointDisambiguationNestedStep(nil))
			start := h.startSession()
			fresh := pointDisambiguationNestedStep(func(capture *device.Capture) {
				if conflict {
					capture.Nodes[2] = revalButton(2, 1, 2, capture.Display.ID, pointDisambiguationNote, "cover", "", "cover")
				}
			})
			h.device.scriptCaptures(fresh)
			result := h.actCoordinate(t, start.Observation.ID, pointDisambiguationOperation("tap"))
			if conflict {
				requireActRejectionDetails(t, result, "tap", "", targetChangedReasonPointChanged)
				cover := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "cover")
				requireDetailStringSlice(t, result.Error.Details, "blocking_refs", cover.Ref)
				proposalRequireNoDispatch(t, h)
				return
			}
			proposalRequireExecuted(t, result)
			proposalRequireTap(t, h, pointDisambiguationPoint)
		})
	}
}
