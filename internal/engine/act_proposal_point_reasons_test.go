package engine

import (
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func proposalPointSetPage(display device.Display, variant string) device.Capture {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
	root := revalRoot(display.ID, 1)
	owner := proposalClickableNode(1, 0, 1, display, bounds,
		"android.widget.Button", "point-set-owner")
	firstText := proposalNode(2, 1, 2, display, bounds,
		"android.widget.TextView", "alpha")
	secondLabel := "bravo"
	if variant == "replaced" {
		secondLabel = "charlie"
	}
	secondText := proposalNode(3, 1, 2, display, bounds,
		"android.widget.TextView", secondLabel)
	owner.Children = []int{2, 3}

	switch variant {
	case "deleted":
		owner.Children = []int{2}
		return revalCapture(display, root, owner, firstText)
	case "overlay":
		firstText.Children = []int{4}
		overlay := proposalClickableNode(4, 2, 3, display, bounds,
			"android.widget.FrameLayout", "floating-overlay")
		return revalCapture(display, root, owner, firstText, secondText, overlay)
	default:
		return revalCapture(display, root, owner, firstText, secondText)
	}
}

func proposalPointSingleButtonPage(display device.Display, label string,
	clickable bool) device.Capture {
	bounds := device.Rect{Left: 100, Top: 600, Right: 900, Bottom: 900}
	button := proposalClickableNode(1, 0, 1, display, bounds,
		"android.widget.Button", label)
	button.ResourceID = "action_button"
	button.Clickable = clickable
	return revalCapture(display, revalRoot(display.ID, 1), button)
}

func proposalPointLineagePage(display device.Display,
	anchorResource string) device.Capture {
	root := revalRoot(display.ID, 1)
	anchorBounds := device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000}
	anchor := proposalNode(1, 0, 1, display, anchorBounds,
		"android.widget.FrameLayout", "")
	anchor.ResourceID = anchorResource
	anchor.Children = []int{2}
	target := proposalClickableNode(2, 1, 2, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "Continue")
	target.ResourceID = "continue_button"
	return revalCapture(display, root, anchor, target)
}

// proposalPointAnonymousLineagePage keeps the same rebuilt ancestor but the
// target carries no E1/E2/E3 evidence.
func proposalPointAnonymousLineagePage(display device.Display,
	anchorResource string) device.Capture {
	root := revalRoot(display.ID, 1)
	anchorBounds := device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000}
	anchor := proposalNode(1, 0, 1, display, anchorBounds,
		"android.widget.FrameLayout", "")
	anchor.ResourceID = anchorResource
	anchor.Children = []int{2}
	target := proposalClickableNode(2, 1, 2, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "")
	return revalCapture(display, root, anchor, target)
}

func proposalPointScrollPage(display device.Display, bounds device.Rect,
	withAxisBlockers bool) device.Capture {
	root := revalRoot(display.ID, 1)
	container := proposalScrollNode(1, 0, 1, display, bounds,
		"android.widget.ScrollView")
	if !withAxisBlockers {
		return revalCapture(display, root, container)
	}
	container.Children = []int{2, 3, 4}
	leftBlocker := proposalScrollNode(2, 1, 2, display, device.Rect{
		Left: 230, Top: bounds.Top, Right: 310, Bottom: bounds.Bottom,
	}, "androidx.recyclerview.widget.RecyclerView")
	centerBlocker := proposalScrollNode(3, 1, 2, display, device.Rect{
		Left: 500, Top: bounds.Top, Right: 580, Bottom: bounds.Bottom,
	}, "androidx.recyclerview.widget.RecyclerView")
	rightBlocker := proposalScrollNode(4, 1, 2, display, device.Rect{
		Left: 770, Top: bounds.Top, Right: 850, Bottom: bounds.Bottom,
	}, "androidx.recyclerview.widget.RecyclerView")
	return revalCapture(display, root, container, leftBlocker, centerBlocker, rightBlocker)
}

func proposalPointCoveredTargetPage(display device.Display,
	covered bool) device.Capture {
	bounds := device.Rect{Left: 100, Top: 700, Right: 900, Bottom: 1100}
	root := revalRoot(display.ID, 1)
	target := proposalClickableNode(1, 0, 1, display, bounds,
		"android.widget.Button", "covered-target")
	if !covered {
		return revalCapture(display, root, target)
	}
	target.Children = []int{2}
	cover := proposalClickableNode(2, 1, 2, display, bounds,
		"android.widget.Button", "covering-control")
	return revalCapture(display, root, target, cover)
}

func proposalPointRunTapWithDeadline(testingHandle *testing.T,
	harnessUnderTest *harness, observationID string,
	operation protocol.Operation) *protocol.Result {
	testingHandle.Helper()
	done := harnessUnderTest.actAsync("", observationID, operation)
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			testingHandle.Fatalf("act returned a transport error: %v", outcome.err)
		}
		return outcome.result
	case <-timer.C:
		testingHandle.Fatal(
			"act did not return; a raw parent cycle caused an unbounded traversal")
		return nil
	}
}

func TestProposalPointSameDepthButtonAndDecorExecutes(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 200, Right: 900, Bottom: 500}
	pointX, pointY := 500, 350
	cases := []struct {
		name  string
		build func(device.Display) device.Capture
	}{
		{
			name: "button_and_decor",
			build: func(display device.Display) device.Capture {
				root := revalRoot(display.ID, 1, 2)
				button := proposalClickableNode(1, 0, 1, display, bounds,
					"android.widget.Button", "save")
				decor := proposalNode(2, 0, 1, display, bounds,
					"android.widget.ImageView", "")
				return revalCapture(display, root, button, decor)
			},
		},
		{
			name: "text_pair_under_button",
			build: func(display device.Display) device.Capture {
				root := revalRoot(display.ID, 1)
				owner := proposalClickableNode(1, 0, 1, display, bounds,
					"android.widget.Button", "card-owner")
				owner.Children = []int{2, 3}
				firstText := proposalNode(2, 1, 2, display, bounds,
					"android.widget.TextView", "alpha")
				secondText := proposalNode(3, 1, 2, display, bounds,
					"android.widget.TextView", "bravo")
				return revalCapture(display, root, owner, firstText, secondText)
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pageStep := proposalCaptureStep(testCase.build)
			harnessUnderTest := newHarness(t)
			harnessUnderTest.device.scriptCaptures(pageStep)
			start := harnessUnderTest.startSession()
			harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

			result := harnessUnderTest.actP(t, "", start.Observation.ID,
				protocol.Operation{
					Type: "tap",
					Target: &protocol.Target{
						X: pointX, Y: pointY,
					},
				})
			proposalRequireExecuted(t, result)
			proposalRequireTap(t, harnessUnderTest, [2]int{pointX, pointY})
		})
	}
}

func TestProposalPointSwipeNeverAmbiguousEvenOver64(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 200, Right: 900, Bottom: 700}
	cases := []struct {
		name  string
		count int
	}{
		{name: "three_overlapping_buttons", count: 3},
		{name: "sixty_six_overlapping_nodes", count: 66},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
				root := revalRoot(display.ID)
				siblings := make([]device.RawNode, 0, testCase.count)
				children := make([]int, 0, testCase.count)
				for index := 0; index < testCase.count; index++ {
					node := proposalClickableNode(index+1, 0, 1, display, bounds,
						"android.widget.Button", "swipe-hit")
					children = append(children, node.Index)
					siblings = append(siblings, node)
				}
				root.Children = children
				nodes := append([]device.RawNode{root}, siblings...)
				return revalCapture(display, nodes...)
			})
			harnessUnderTest := newHarness(t)
			harnessUnderTest.device.scriptCaptures(pageStep)
			start := harnessUnderTest.startSession()
			harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

			result := harnessUnderTest.actP(t, "", start.Observation.ID,
				protocol.Operation{
					Type:       "swipe",
					From:       &protocol.Point{X: 500, Y: 300},
					To:         &protocol.Point{X: 500, Y: 1000},
					DurationMS: 300,
				})
			proposalRequireExecuted(t, result)
			proposalRequireSwipe(t, harnessUnderTest, swipeCall{
				displayID: 12, x1: 500, y1: 300, x2: 500, y2: 1000,
				durationMS: 300,
			})
		})
	}
}

func TestProposalPointCompetingOwnersDisambiguateAndReject(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 200, Right: 900, Bottom: 600}
	pointX, pointY := 500, 400

	t.Run("unique_action_owner_executes", func(t *testing.T) {
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			root := revalRoot(display.ID, 1, 2)
			tapOnly := proposalClickableNode(1, 0, 1, display, bounds,
				"android.widget.Button", "tap-owner")
			longOnly := proposalNode(2, 0, 1, display, bounds,
				"android.widget.Button", "long-owner")
			longOnly.HasLongClick = true
			longOnly.LongClickable = true
			return revalCapture(display, root, tapOnly, longOnly)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					X: pointX, Y: pointY,
				},
			})
		proposalRequireExecuted(t, result)
		proposalRequireTap(t, harnessUnderTest, [2]int{pointX, pointY})
	})

	t.Run("two_tap_owners_reject_with_candidates", func(t *testing.T) {
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			root := revalRoot(display.ID, 1, 2)
			firstTap := proposalClickableNode(1, 0, 1, display, bounds,
				"android.widget.Button", "first-tap")
			secondTap := proposalClickableNode(2, 0, 1, display, bounds,
				"android.widget.Button", "second-tap")
			return revalCapture(display, root, firstTap, secondTap)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		firstNode := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "first-tap")
		secondNode := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "second-tap")

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					X: pointX, Y: pointY,
				},
			})
		details := proposalRequireStaticReason(t, result,
			protocol.CodeAmbiguousPoint, "competing_targets")
		proposalRequireDetailString(t, details, "scope", "point")
		if _, exists := details["scope_ref"]; exists {
			t.Fatalf("point-scope ambiguity must omit scope_ref: %#v", details)
		}
		candidates := proposalCandidates(t, details)
		if len(candidates) != 2 {
			t.Fatalf("candidate count = %d, want 2: %#v", len(candidates), candidates)
		}
		proposalRequireCandidateRefs(t, candidates, firstNode.Ref, secondNode.Ref)
		proposalRequireCandidateRelation(t, candidates, firstNode.Ref, "competing")
		proposalRequireCandidateRelation(t, candidates, secondNode.Ref, "competing")
		if status := observationStatus(harnessUnderTest, start.Observation.ID); status != ObservationReady {
			t.Fatalf("observation status = %q, want ready", status)
		}
		proposalRequireNoDispatch(t, harnessUnderTest)
	})
}

func TestProposalPointSetFreshFrameDriftReturnsPointChanged(t *testing.T) {
	pointX, pointY := 500, 500
	cases := []struct {
		name    string
		variant string
	}{
		{name: "added_overlay", variant: "overlay"},
		{name: "deleted_node", variant: "deleted"},
		{name: "replaced_node", variant: "replaced"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
				return proposalPointSetPage(display, "")
			})
			freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
				return proposalPointSetPage(display, testCase.variant)
			})
			harnessUnderTest := newHarness(t)
			harnessUnderTest.device.scriptCaptures(initialStep)
			start := harnessUnderTest.startSession()
			harnessUnderTest.device.scriptCaptures(freshStep)

			result := harnessUnderTest.actP(t, "", start.Observation.ID,
				protocol.Operation{
					Type: "tap",
					Target: &protocol.Target{
						X: pointX, Y: pointY,
					},
				})
			proposalRequireTargetChanged(t, result, "point_changed")
			proposalRequireNoDispatch(t, harnessUnderTest)
		})
	}
}

func TestProposalPointFreshMalformedCycleReturnsHierarchyUnavailable(t *testing.T) {
	pointX, pointY := 500, 500
	cases := []struct {
		name           string
		initial        func(device.Display) device.Capture
		freshWithCycle func(device.Display) device.Capture
	}{
		{
			name: "point_unique",
			initial: func(display device.Display) device.Capture {
				bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
				target := proposalClickableNode(1, 0, 1, display, bounds,
					"android.widget.Button", "cycle-target")
				return revalCapture(display, revalRoot(display.ID, 1), target)
			},
			freshWithCycle: func(display device.Display) device.Capture {
				bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
				target := proposalClickableNode(1, 0, 1, display, bounds,
					"android.widget.Button", "cycle-target-changed")
				peer := proposalNode(2, 0, 1, display, bounds,
					"android.widget.FrameLayout", "cycle-peer")
				target.Parent = 2
				peer.Parent = 1
				return revalCapture(display, revalRoot(display.ID, 1), target, peer)
			},
		},
		{
			name: "point_set",
			initial: func(display device.Display) device.Capture {
				return proposalPointSetPage(display, "")
			},
			freshWithCycle: func(display device.Display) device.Capture {
				capture := proposalPointSetPage(display, "")
				capture.Nodes[2].Parent = 3
				capture.Nodes[3].Parent = 2
				return capture
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			initialStep := proposalCaptureStep(testCase.initial)
			cycleStep := proposalCaptureStep(testCase.freshWithCycle)
			harnessUnderTest := newHarness(t)
			harnessUnderTest.device.scriptCaptures(initialStep)
			start := harnessUnderTest.startSession()
			harnessUnderTest.device.scriptCaptures(cycleStep)

			result := proposalPointRunTapWithDeadline(t, harnessUnderTest,
				start.Observation.ID, protocol.Operation{
					Type: "tap",
					Target: &protocol.Target{
						X: pointX, Y: pointY,
					},
				})
			requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
			status := result.HTTPStatus
			if status == 0 {
				status = protocol.Status(result.Error.Code)
			}
			if status != 503 {
				t.Fatalf("HTTP status = %d, want 503 (%+v)",
					status, result.Error)
			}
			if result.Observation != nil {
				t.Fatalf("hierarchy failure delivered a new observation: %+v",
					result.Observation)
			}
			proposalRequireNoDispatch(t, harnessUnderTest)
		})
	}
}

func TestProposalTargetChangedNineReasonMatrix(t *testing.T) {
	t.Run("child_changed", func(t *testing.T) {
		cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 800}
		childBounds := device.Rect{Left: 200, Top: 400, Right: 700, Bottom: 600}
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(
			bubbleStep(cardBounds, childBounds, "post", false))
		start := harnessUnderTest.startSession()
		child := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "post")
		harnessUnderTest.device.scriptCaptures(
			bubbleStep(cardBounds, childBounds, "different", false))

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					UseRef: true, Ref: child.Ref,
				},
			})
		proposalRequireTargetChanged(t, result, "child_changed")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("target_missing", func(t *testing.T) {
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointSingleButtonPage(display, "Continue", true)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return revalCapture(display, revalRoot(display.ID))
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		target := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "Continue")
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					UseRef: true, Ref: target.Ref,
				},
			})
		proposalRequireTargetChanged(t, result, "target_missing")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("target_ambiguous", func(t *testing.T) {
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointSingleButtonPage(display, "Continue", true)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			bounds := device.Rect{Left: 100, Top: 600, Right: 900, Bottom: 900}
			first := proposalClickableNode(1, 0, 1, display, bounds,
				"android.widget.Button", "Continue")
			first.ResourceID = "action_button"
			second := proposalClickableNode(2, 0, 1, display, bounds,
				"android.widget.Button", "Continue")
			second.ResourceID = "action_button"
			return revalCapture(display, revalRoot(display.ID, 1, 2), first, second)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		target := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "Continue")
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					UseRef: true, Ref: target.Ref,
				},
			})
		proposalRequireTargetChanged(t, result, "target_ambiguous")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("lineage_changed", func(t *testing.T) {
		t.Run("tier4_accepts_rebuilt_ancestor", func(t *testing.T) {
			initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
				return proposalPointLineagePage(display, "old_panel")
			})
			freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
				return proposalPointLineagePage(display, "new_panel")
			})
			harnessUnderTest := newHarness(t)
			harnessUnderTest.device.scriptCaptures(initialStep)
			start := harnessUnderTest.startSession()
			target := proposalFindLabel(t,
				currentTree(t, harnessUnderTest.engine).Root, "Continue")
			harnessUnderTest.device.scriptCaptures(freshStep)

			result := harnessUnderTest.actP(t, "", start.Observation.ID,
				protocol.Operation{
					Type: "tap",
					Target: &protocol.Target{
						UseRef: true, Ref: target.Ref,
					},
				})
			proposalRequireExecuted(t, result)
			proposalRequireTap(t, harnessUnderTest, [2]int{500, 500})
		})

		t.Run("without_semantic_evidence_names_changed_ancestor",
			func(t *testing.T) {
				initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
					return proposalPointAnonymousLineagePage(display, "old_panel")
				})
				freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
					return proposalPointAnonymousLineagePage(display, "new_panel")
				})
				harnessUnderTest := newHarness(t)
				harnessUnderTest.device.scriptCaptures(initialStep)
				start := harnessUnderTest.startSession()
				root := currentTree(t, harnessUnderTest.engine).Root
				target := targetProofNodeByBounds(t, root,
					device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600})
				panel := targetProofNodeByBounds(t, root,
					device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000})
				harnessUnderTest.device.scriptCaptures(freshStep)

				result := harnessUnderTest.actP(t, "", start.Observation.ID,
					protocol.Operation{
						Type: "tap",
						Target: &protocol.Target{
							UseRef: true, Ref: target.Ref,
						},
					})
				proposalRequireTargetChanged(t, result, "lineage_changed")
				if got := result.Error.Details["changed_ref"]; got != panel.Ref {
					t.Fatalf("changed_ref = %#v, want %q", got, panel.Ref)
				}
				proposalRequireNoDispatch(t, harnessUnderTest)
			})
	})

	t.Run("capability_changed", func(t *testing.T) {
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointSingleButtonPage(display, "Continue", true)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointSingleButtonPage(display, "Continue", false)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		target := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "Continue")
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					UseRef: true, Ref: target.Ref,
				},
			})
		proposalRequireTargetChanged(t, result, "capability_changed")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("scroll_scope_changed", func(t *testing.T) {
		initialBounds := device.Rect{Left: 100, Top: 0, Right: 1000, Bottom: 2400}
		freshBounds := device.Rect{Left: 60, Top: 0, Right: 960, Bottom: 2400}
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointScrollPage(display, initialBounds, false)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointScrollPage(display, freshBounds, false)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		containers := proposalScrollNodesByBounds(
			currentTree(t, harnessUnderTest.engine).Root, initialBounds)
		if len(containers) != 1 {
			t.Fatalf("initial scroll container count = %d, want 1", len(containers))
		}
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "scroll",
				Container: &protocol.Target{
					UseRef: true, Ref: containers[0].Ref,
				},
				Direction: protocol.ScrollDirectionDown,
			})
		proposalRequireTargetChanged(t, result, "scroll_scope_changed")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("trajectory_blocked", func(t *testing.T) {
		bounds := screenRect()
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointScrollPage(display, bounds, false)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointScrollPage(display, bounds, true)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		containers := proposalScrollNodesByBounds(
			currentTree(t, harnessUnderTest.engine).Root, bounds)
		if len(containers) != 1 {
			t.Fatalf("initial scroll container count = %d, want 1", len(containers))
		}
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "scroll",
				Container: &protocol.Target{
					UseRef: true, Ref: containers[0].Ref,
				},
				Direction: protocol.ScrollDirectionDown,
			})
		proposalRequireTargetChanged(t, result, "trajectory_blocked")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("scope_blocked", func(t *testing.T) {
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointCoveredTargetPage(display, false)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointCoveredTargetPage(display, true)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		target := proposalFindLabel(t,
			currentTree(t, harnessUnderTest.engine).Root, "covered-target")
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					UseRef: true, Ref: target.Ref,
				},
			})
		proposalRequireTargetChanged(t, result, "scope_blocked")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})

	t.Run("point_changed", func(t *testing.T) {
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointSingleButtonPage(display, "old-point", true)
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return proposalPointSingleButtonPage(display, "new-point", true)
		})
		harnessUnderTest := newHarness(t)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(t, "", start.Observation.ID,
			protocol.Operation{
				Type: "tap",
				Target: &protocol.Target{
					X: 500, Y: 750,
				},
			})
		proposalRequireTargetChanged(t, result, "point_changed")
		proposalRequireNoDispatch(t, harnessUnderTest)
	})
}
