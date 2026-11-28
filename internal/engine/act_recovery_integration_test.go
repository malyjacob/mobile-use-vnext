package engine

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func requireActRejectionDetails(t *testing.T, result *protocol.Result, kind, ref, reason string) {
	t.Helper()
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	details := result.Error.Details
	for key, want := range map[string]string{"phase": "revalidate", "kind": kind, "reason": reason} {
		if details[key] != want {
			t.Fatalf("%s = %v, want %q in %v", key, details[key], want, details)
		}
	}
	if got, exists := details["original_ref"]; exists != (ref != "") || (exists && got != ref) {
		t.Fatalf("original_ref = %v (present=%v), want %q", got, exists, ref)
	}
	for _, key := range []string{"stage", "dispatch", "recovered_ref", "candidate_count"} {
		if _, exists := details[key]; exists {
			t.Fatalf("revalidation details unexpectedly contain %s: %v", key, details)
		}
	}
	if result.AutoRecovery != nil {
		t.Fatalf("rejection has recovery marker: %+v", result.AutoRecovery)
	}
}

func requireRecoveryAttempts(t *testing.T, result *protocol.Result, strategies ...string) {
	t.Helper()
	got, exists := result.Error.Details["recovery_attempted"]
	if exists != (len(strategies) > 0) || (exists && !reflect.DeepEqual(got, strategies)) {
		t.Fatalf("recovery_attempted = %v, want %v", got, strategies)
	}
}

func requireRecoveryReplay(t *testing.T, h *harness, request *protocol.ActRequest, result *protocol.Result) {
	t.Helper()
	want := *result.AutoRecovery
	result.AutoRecovery.Strategy = "mutated"
	status, apiErr := h.engine.Status("agent", result.OperationID)
	if apiErr != nil || status.AutoRecovery == nil || *status.AutoRecovery != want || status.Observation != nil {
		t.Fatalf("status = %+v, error = %v, want recovery %+v without an observation", status, apiErr, want)
	}
	status.AutoRecovery.RecoveredRef = "mutated"
	replay := h.actWith(t, result.OperationID, request)
	if replay.AutoRecovery == nil || *replay.AutoRecovery != want || replay.Observation != nil {
		t.Fatalf("duplicate = %+v, want recovery %+v without an observation", replay, want)
	}
}

func rebindIntegrationCard(display device.Display, fresh bool) device.Capture {
	original, changed := actRebindTestCards()
	resource := "com.example:id/old_card"
	if fresh {
		original, resource = changed, "com.example:id/new_card"
		original.Nodes[1].ClassName = "android.widget.ImageButton"
	}
	original.Nodes[1].ResourceID, original.Nodes[1].ResourceSet = resource, true
	original.Display = display
	return original
}

func TestActRecoveryRebindDispatch(t *testing.T) {
	for _, kind := range []string{"tap", "long_press", "text_parent", "text_label"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarnessWithLogger(t, false, nil)
				text := kind == "text_parent" || kind == "text_label"
				page := func(fresh bool) captureStep {
					return proposalCaptureStep(func(display device.Display) device.Capture {
						if !text {
							return rebindIntegrationCard(display, fresh)
						}
						bounds := device.Rect{Left: 110, Top: 680, Right: 500, Bottom: 790}
						panel := device.Rect{Left: 70, Top: 200, Right: 600, Bottom: 850}
						resource := "old_input"
						if fresh {
							bounds = device.Rect{Left: 115, Top: 220, Right: 440, Bottom: 290}
							panel = device.Rect{Left: 100, Top: 205, Right: 450, Bottom: 375}
							resource = "fresh_input"
						}
						if kind == "text_label" {
							input := revalEditText(1, 0, 1, display.ID, bounds, "", "Search archive", resource, fresh)
							return revalCapture(display, revalRoot(display.ID, 1), input)
						}
						parent := transparentNode(1, 0, 1, display.ID, panel, 2)
						parent.ResourceID, parent.ResourceSet = "com.example:id/editor_panel", true
						input := revalEditText(2, 1, 2, display.ID, bounds, "", "", resource, fresh)
						return revalCapture(display, revalRoot(display.ID, 1), parent, input)
					})
				}
				h.device.scriptCaptures(page(false))
				start := h.startSession()
				root := currentTree(t, h.engine).Root
				var request *protocol.ActRequest
				strategy := protocol.AutoRecoveryContainerRebind
				if text {
					strategy = protocol.AutoRecoveryTextRebind
					input := mustFindFirstNodeWithAction(t, root, protocol.ActionText)
					writeOnly := false
					request = textFlowRequest(start.Observation.ID, input.Ref, "text fixture", &writeOnly)
				} else {
					card := mustFindNodeByTextOrResource(t, root, "购买")
					request = h.tapRefRequest(t, start.Observation.ID, card.Ref)
					request.Operation.Type = kind
				}
				freshStep := page(true)
				fresh, captureErr := freshStep(h.device, h.engine.current.Screen)
				if captureErr != nil {
					t.Fatal(captureErr)
				}
				preview, previewErr := h.engine.projector.Preview(&fresh)
				if previewErr != nil {
					t.Fatal(previewErr)
				}
				index := 1
				if kind == "text_parent" {
					index = 2
				}
				wantRef := projectionPublicRefForRaw(preview, index)
				if wantRef == "" {
					t.Fatal("recovery target missing from the Attempt 2 preview")
				}
				h.device.scriptCaptures(freshStep)
				probes := h.device.probeCount()
				result := h.actWith(t, "Pstrategy", request)
				proposalRequireExecuted(t, result)
				if result.AutoRecovery == nil || result.AutoRecovery.Strategy != strategy ||
					result.AutoRecovery.Attempts != 2 || result.AutoRecovery.OriginalRef != request.Operation.Target.Ref ||
					result.AutoRecovery.RecoveredRef != wantRef {
					t.Fatalf("auto_recovery = %+v, want %s to Attempt 2 ref %s", result.AutoRecovery, strategy, wantRef)
				}
				// Two revalidation probes and the ordinary post-action observation.
				if got := h.device.probeCount() - probes; got != 3 {
					t.Fatalf("display probes = %d, want two revalidation probes and one post-action probe", got)
				}
				if text {
					if len(h.device.taps) != 0 || len(h.device.texts) != 1 || len(h.device.keys) != 0 {
						t.Fatalf("focus taps/writes/keys = %d/%d/%d, want 0/1/0 for the already-focused replacement",
							len(h.device.taps), len(h.device.texts), len(h.device.keys))
					}
				} else if taps := h.device.taps; len(taps) != 1 || taps[0][1] < 220 || taps[0][1] >= 420 {
					t.Fatalf("rebind did not use the new card bounds: %v", taps)
				}
				requireRecoveryReplay(t, h, request, result)
			})
		})
	}
}

func TestActRecoveryRebindRejectsAmbiguousOrBlocked(t *testing.T) {
	for _, reason := range []string{targetChangedReasonTargetAmbiguous, targetChangedReasonScopeBlocked} {
		t.Run(reason, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarnessWithLogger(t, false, nil)
				h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
					return rebindIntegrationCard(display, false)
				}))
				start := h.startSession()
				card := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "购买")
				h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
					capture := rebindIntegrationCard(display, true)
					bounds := capture.Nodes[1].Bounds
					label := "Covering panel"
					if reason == targetChangedReasonTargetAmbiguous {
						label = "购买"
						bounds.Top, bounds.Bottom = 850, 1050
					}
					blocker := revalButton(0, 0, 0, display.ID, bounds, label, "", "blocker")
					actRebindTestAppend(&capture, 0, blocker)
					return capture
				}))
				result := h.actWith(t, "PunsafeRebind", h.tapRefRequest(t, start.Observation.ID, card.Ref))
				requireActRejectionDetails(t, result, "tap", card.Ref, reason)
				requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry, protocol.AutoRecoveryContainerRebind)
				proposalRequireNoDispatch(t, h)
			})
		})
	}
}

func TestActRecoveryCoordinateTransientRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarnessWithLogger(t, false, nil)
		start := h.startSession()
		h.device.scriptCaptures(overlayCaptureStep(), defaultCaptureStep)
		request := &protocol.ActRequest{Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{X: 500, Y: 150}}}
		result := h.actWith(t, "PcoordTransient", request)
		proposalRequireExecuted(t, result)
		want := protocol.AutoRecoveryView{Strategy: protocol.AutoRecoveryTransientRetry, Attempts: 2}
		if result.AutoRecovery == nil || *result.AutoRecovery != want {
			t.Fatalf("auto_recovery = %+v, want %+v", result.AutoRecovery, want)
		}
		requireRecoveryReplay(t, h, request, result)
		if len(h.device.taps) != 1 {
			t.Fatalf("coordinate transient retry dispatched %d taps", len(h.device.taps))
		}
	})
}

func TestActRecoveryStopsAtRebindBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarnessWithLogger(t, false, nil)
		step := func(fresh bool) captureStep {
			return proposalCaptureStep(func(display device.Display) device.Capture {
				return rebindIntegrationCard(display, fresh)
			})
		}
		h.device.scriptCaptures(step(false))
		start := h.startSession()
		card := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "购买")
		h.device.scriptCaptures(step(true))
		probes := h.device.probeCount()
		projector := h.engine.projector
		var previews int
		h.engine.projector = tapPreviewFuncProjector{Projector: projector, preview: func(capture *device.Capture) (*Projection, error) {
			previews++
			if got := h.device.probeCount() - probes; got != 2 {
				t.Fatalf("rebind preview started after %d probes, want two", got)
			}
			preview, err := projector.Preview(capture)
			if _, stopErr := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
				AppInstanceID: h.appInstance, StopSeq: 1, Reason: "user_stop",
			}); stopErr != nil {
				t.Fatalf("STOP failed: %v", stopErr)
			}
			return preview, err
		}}
		request := h.tapRefRequest(t, start.Observation.ID, card.Ref)
		result := h.actWith(t, "PrebindStop", request)
		requireNotExecuted(t, result, protocol.CodeStopped)
		proposalRequireNoDispatch(t, h)
		if previews != 1 || result.AutoRecovery != nil || result.Observation != nil || result.Error.Details["phase"] != nil {
			t.Fatalf("stop during rebind preview: previews=%d result=%+v", previews, result)
		}
		replay := h.actWith(t, "PrebindStop", request)
		if replay.AutoRecovery != nil {
			t.Fatalf("stopped recovery escaped into replay: %+v", replay.AutoRecovery)
		}
	})
}

func TestActRecoveryDispatchGate(t *testing.T) {
	for _, condition := range []string{"allowed", "stop", "deadline", "display", "finished", "replaced"} {
		t.Run(condition, func(t *testing.T) {
			h := newHarness(t)
			h.startSession()
			work, _ := h.settleWork(t, "PcommitRecovery")
			h.engine.mu.Lock()
			op, obs := h.engine.operations[work.OperationID], h.engine.current
			switch condition {
			case "stop":
				work.Cancel()
			case "deadline":
				work.Deadline = time.Now().Add(-time.Millisecond)
			case "display":
				h.engine.session.Display.Generation = "different_generation"
			case "finished":
				op.State = OperationFinished
			case "replaced":
				h.engine.work = nil
			}
			h.engine.mu.Unlock()
			candidate := &protocol.AutoRecoveryView{Strategy: protocol.AutoRecoveryContainerRebind,
				OriginalRef: "n2", RecoveredRef: "n3", Attempts: 2}
			if got := h.engine.commitActDispatch(work, op, obs, candidate); got != (condition == "allowed") {
				t.Fatalf("dispatch gate = %v for %s", got, condition)
			}
			result := h.engine.newResult(op)
			if condition == "allowed" {
				candidate.OriginalRef = "mutated"
				if result.AutoRecovery == nil || result.AutoRecovery.OriginalRef != "n2" {
					t.Fatalf("saved recovery: %+v", result.AutoRecovery)
				}
				e := h.engine
				e.finishOperation(op, OutcomeNotExecuted, "work_deadline", stoppedActRevalidation())
				result.Observation = &protocol.Observation{ID: "Olate"}
				result.AutoRecovery.Strategy = "mutated"
				e.applyFrozenResult(op, &result)
				if result.AutoRecovery == nil || result.AutoRecovery.Strategy != protocol.AutoRecoveryContainerRebind ||
					result.AutoRecovery.OriginalRef != "n2" || result.Observation != nil || result.Phase != "work_deadline" {
					t.Fatalf("frozen result lost the committed recovery or leaked an observation: %+v", result)
				}
			} else if result.AutoRecovery != nil {
				t.Fatalf("blocked work saved recovery: %+v", result.AutoRecovery)
			}
		})
	}
}

func requireDeliveredActCandidate(t *testing.T, h *harness, result *protocol.Result, label string) map[string]any {
	t.Helper()
	if result.Observation == nil || result.Observation.ID != h.currentObservationID() {
		t.Fatalf("result did not deliver a current observation: %+v", result)
	}
	root := currentTree(t, h.engine).Root
	want := mustFindNodeByTextOrResource(t, root, label)
	candidates, _ := result.Error.Details["candidates"].([]map[string]any)
	if len(candidates) == 0 || len(candidates) > maxStaticCandidates {
		t.Fatalf("unexpected candidate count: %v", candidates)
	}
	for _, candidate := range candidates {
		ref, _ := candidate["ref"].(string)
		if len(candidate) != 6 || findPublicNode(root, ref) == nil {
			t.Fatalf("invalid candidate in delivered O: %v", candidate)
		}
		if ref == want.Ref && candidate["executable"] == true {
			return candidate
		}
	}
	t.Fatalf("want executable %q (%s) among %v", label, want.Ref, candidates)
	return nil
}

func requireDeliveredActBlocker(t *testing.T, h *harness, result *protocol.Result, label string) {
	t.Helper()
	want := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, label)
	refs, _ := result.Error.Details["blocking_refs"].([]string)
	if !slices.Contains(refs, want.Ref) {
		t.Fatalf("blocking_refs = %v, want %s (%s)", refs, want.Ref, label)
	}
}

func TestActRecoveryRejectedScrollFiltersCoveredCandidates(t *testing.T) {
	for _, mode := range []string{"matched", "missing"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			selected := device.Rect{Left: 100, Top: 300, Right: 1000, Bottom: 900}
			covered := device.Rect{Left: 100, Top: 1000, Right: 980, Bottom: 1550}
			backup := device.Rect{Left: 100, Top: 1700, Right: 980, Bottom: 2200}
			base := func(display device.Display, captureBounds device.Rect) device.Capture {
				return proposalPointScrollPage(display, captureBounds, false)
			}
			h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
				return base(display, selected)
			}))
			start := h.startSession()
			target := findRole(currentTree(t, h.engine).Root, protocol.RoleScroll)
			if target == nil {
				t.Fatal("missing original scroll container")
			}
			h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
				capture := revalCapture(display, revalRoot(display.ID))
				if mode == "matched" {
					shifted := selected
					shifted.Left, shifted.Right = 60, 960
					capture = base(display, shifted)
				}
				for _, item := range []struct {
					name   string
					bounds device.Rect
				}{{"covered list", covered}, {"clear list", backup}} {
					list := proposalScrollNode(0, 0, 0, display, item.bounds, "android.widget.ScrollView")
					list.Text, list.TextSet = item.name, true
					actRebindTestAppend(&capture, 0, list)
				}
				panel := proposalNode(0, 0, 0, display, covered, "android.widget.FrameLayout", "")
				actRebindTestAppend(&capture, 0, panel)
				child := covered
				child.Right = child.Left + 520 // Recognizable solid panel, with no action on the panel itself.
				actRebindTestAppend(&capture, len(capture.Nodes)-1,
					proposalClickableNode(0, 0, 0, display, child, "android.widget.Button", "Overlay action"))
				return capture
			}))
			result := h.actP(t, "PscrollOverlay", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{Ref: target.Ref, UseRef: true}, Direction: "down",
			})
			reason := targetChangedReasonTargetMissing
			if mode == "matched" {
				reason = targetChangedReasonScrollScopeChanged
			}
			requireActRejectionDetails(t, result, "scroll", target.Ref, reason)
			requireDeliveredActCandidate(t, h, result, "clear list")
			coveredNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "covered list")
			if slices.Contains(proposalCandidateRefs(proposalCandidates(t, result.Error.Details)), coveredNode.Ref) {
				t.Fatalf("overlay-covered list was offered: %v", result.Error.Details["candidates"])
			}
			proposalRequireNoDispatch(t, h)
		})
	}
}

func TestActRecoveryRejectedDiagnostics(t *testing.T) {
	t.Run("unique target with blocked long press", func(t *testing.T) {
		h := newHarness(t)
		bounds := device.Rect{Left: 100, Top: 700, Right: 900, Bottom: 1100}
		page := func(blocked bool) captureStep {
			return proposalCaptureStep(func(display device.Display) device.Capture {
				target := revalButton(1, 0, 1, display.ID, bounds, "original button", "", "button")
				capture := revalCapture(display, revalRoot(display.ID, 1), target)
				if blocked {
					cover := revalButton(0, 0, 0, display.ID, bounds, "cover", "", "cover")
					cover.LongClickable = false
					actRebindTestAppend(&capture, 1, cover)
					backup := revalButton(0, 0, 0, display.ID,
						device.Rect{Left: 230, Top: 750, Right: 330, Bottom: 900}, "backup", "", "backup")
					actRebindTestAppend(&capture, 1, backup)
				}
				return capture
			})
		}
		h.device.scriptCaptures(page(false))
		start := h.startSession()
		button := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "original button")
		h.device.scriptCaptures(page(true))
		request := longPressRefRequest(start.Observation.ID, button.Ref)
		result := h.actWith(t, "Pblocked", request)
		requireActRejectionDetails(t, result, "long_press", button.Ref, targetChangedReasonScopeBlocked)
		requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry)
		requireDeliveredActBlocker(t, h, result, "cover")
		if candidate := requireDeliveredActCandidate(t, h, result, "backup"); candidate["relation"] != candidateRelationChild {
			t.Fatalf("matched target lost its fresh child relationship: %v", candidate)
		}
		status, apiErr := h.engine.Status("agent", result.OperationID)
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		for _, replay := range []*protocol.Result{status, h.actWith(t, result.OperationID, request)} {
			requireActRejectionDetails(t, replay, "long_press", button.Ref, targetChangedReasonScopeBlocked)
			requireRecoveryAttempts(t, replay, protocol.AutoRecoveryTransientRetry)
			if replay.Observation != nil || replay.Error.Details["candidates"] != nil || replay.Error.Details["blocking_refs"] != nil {
				t.Fatalf("replay retained frame-specific evidence: %+v", replay)
			}
		}
		proposalRequireNoDispatch(t, h)
	})

	t.Run("coordinate tap with a new competing owner", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.device.scriptCaptures(overlayCaptureStep())
		result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
			Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
		})
		requireActRejectionDetails(t, result, "tap", "", targetChangedReasonPointChanged)
		requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry)
		requireDeliveredActBlocker(t, h, result, "sheet")
		requireDeliveredActCandidate(t, h, result, "OK")
		proposalRequireNoDispatch(t, h)
	})

	t.Run("coordinate tap owner changed without an overlay", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.device.scriptCaptures(markerCapture("Renamed OK"))
		result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
			Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
		})
		requireActRejectionDetails(t, result, "tap", "", targetChangedReasonPointChanged)
		requireDeliveredActCandidate(t, h, result, "Renamed OK")
		if refs, exists := result.Error.Details["blocking_refs"]; exists {
			t.Fatalf("renamed owner was misreported as occlusion: %v", refs)
		}
		proposalRequireNoDispatch(t, h)
	})

	t.Run("text lineage with preserved public ancestor", func(t *testing.T) {
		h := newHarness(t)
		page := func(panelResource string, backup bool) captureStep {
			return proposalCaptureStep(func(display device.Display) device.Capture {
				panel := proposalNode(1, 0, 1, display,
					device.Rect{Left: 30, Top: 120, Right: 900, Bottom: 950}, "android.widget.FrameLayout", "")
				panel.ResourceID, panel.ResourceSet, panel.Children = panelResource, true, []int{2}
				input := revalEditText(2, 1, 2, display.ID,
					device.Rect{Left: 100, Top: 700, Right: 600, Bottom: 800}, "", "", "", false)
				capture := revalCapture(display, revalRoot(display.ID, 1), panel, input)
				if backup {
					other := revalEditText(0, 0, 0, display.ID,
						device.Rect{Left: 100, Top: 200, Right: 600, Bottom: 300}, "", "Other editor", "other", false)
					actRebindTestAppend(&capture, 0, other)
				}
				return capture
			})
		}
		h.device.scriptCaptures(page("old_panel", false))
		start := h.startSession()
		root := currentTree(t, h.engine).Root
		input := mustFindFirstNodeWithAction(t, root, protocol.ActionText)
		panel := targetProofNodeByBounds(t, root, device.Rect{Left: 30, Top: 120, Right: 900, Bottom: 950})
		h.device.scriptCaptures(page("new_panel", true))
		result := h.actWith(t, "PlineageText", textFlowRequest(start.Observation.ID, input.Ref, "test", nil))
		requireActRejectionDetails(t, result, "text", input.Ref, targetChangedReasonLineageChanged)
		requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry, protocol.AutoRecoveryTextRebind)
		targetProofRequireDetailsString(t, result, "changed_ref", panel.Ref)
		requireTextInput(t, result, protocol.TextInputNotExecuted, protocol.TextInputNotExecuted)
		proposalRequireNoDispatch(t, h)
	})

	t.Run("missing scroll has page candidate outside the old bounds", func(t *testing.T) {
		h := newHarness(t)
		page := func(top int, resource string) captureStep {
			return proposalCaptureStep(func(display device.Display) device.Capture {
				bounds := device.Rect{Left: 100, Top: top, Right: 980, Bottom: top + 400}
				capture := proposalPointScrollPage(display, bounds, false)
				capture.Nodes[1].ResourceID, capture.Nodes[1].ResourceSet = resource, true
				capture.Nodes[1].Text, capture.Nodes[1].TextSet = resource, true
				return capture
			})
		}
		h.device.scriptCaptures(page(300, "old_scroll"))
		start := h.startSession()
		old := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "old_scroll")
		h.device.scriptCaptures(page(1000, "backup_scroll"))
		result := h.actP(t, "PmissingScroll", start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{Ref: old.Ref, UseRef: true}, Direction: "down",
		})
		requireActRejectionDetails(t, result, "scroll", old.Ref, targetChangedReasonTargetMissing)
		requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry)
		if candidate := requireDeliveredActCandidate(t, h, result, "backup_scroll"); candidate["relation"] != candidateRelationPageScroll {
			t.Fatalf("missing container did not fall back to page scrolls: %v", candidate)
		}
		proposalRequireNoDispatch(t, h)
	})

	t.Run("rotation uses page ranking even when old coordinates intersect", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHarnessWithLogger(t, false, nil)
			start := h.startSession()
			before := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "OK")
			step := func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				display.Rotation = 1
				h.engine.mu.Lock()
				h.engine.session.Display.Rotation = display.Rotation
				h.engine.mu.Unlock()
				overlap := revalButton(1, 0, 1, display.ID,
					device.Rect{Left: 100, Top: 100, Right: 900, Bottom: 200}, "other at old point", "", "overlap")
				backup := revalButton(2, 0, 1, display.ID,
					device.Rect{Left: 90, Top: 800, Right: 350, Bottom: 900}, "backup in new layout", "", "backup")
				return revalCapture(display, revalRoot(display.ID, 1, 2), overlap, backup), nil
			}
			h.device.scriptCaptures(step)
			result := h.actWith(t, "Protated", h.tapRefRequest(t, start.Observation.ID, before.Ref))
			requireActRejectionDetails(t, result, "tap", before.Ref, targetChangedReasonTargetMissing)
			if candidate := requireDeliveredActCandidate(t, h, result, "backup in new layout"); candidate["relation"] != candidateRelationCompeting {
				t.Fatalf("rotated screen did not use the page fallback: %v", candidate)
			}
			proposalRequireNoDispatch(t, h)
		})
	})

	t.Run("missing text falls back after keyboard displacement", func(t *testing.T) {
		h := newHarness(t)
		page := func(top int, resource string) captureStep {
			return proposalCaptureStep(func(display device.Display) device.Capture {
				bounds := device.Rect{Left: 140, Top: top, Right: 600, Bottom: top + 70}
				field := revalEditText(1, 0, 1, display.ID, bounds, "", "", resource, false)
				return revalCapture(display, revalRoot(display.ID, 1), field)
			})
		}
		h.device.scriptCaptures(page(1000, "old_editor"))
		start := h.startSession()
		old := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "old_editor")
		h.device.scriptCaptures(page(300, "new_editor"))
		result := h.actWith(t, "PkeyboardText", textFlowRequest(start.Observation.ID, old.Ref, "test", nil))
		requireActRejectionDetails(t, result, "text", old.Ref, targetChangedReasonTargetMissing)
		requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry, protocol.AutoRecoveryTextRebind)
		if candidate := requireDeliveredActCandidate(t, h, result, "new_editor"); candidate["relation"] != candidateRelationCompeting {
			t.Fatalf("keyboard displacement did not fall back to page: %v", candidate)
		}
		requireTextInput(t, result, protocol.TextInputNotExecuted, protocol.TextInputNotExecuted)
		proposalRequireNoDispatch(t, h)
	})
}
