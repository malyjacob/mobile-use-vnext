package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func tapBubbleProposalNetEaseCapture(display device.Display,
	labelBounds device.Rect, labelText string) device.Capture {
	containerBounds := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2200}
	commentBounds := device.Rect{Left: 915, Top: 1675, Right: 1028, Bottom: 1787}
	container := proposalClickableNode(1, 0, 1, display, containerBounds,
		"android.widget.FrameLayout", "")
	comment := proposalClickableNode(2, 1, 2, display, commentBounds,
		"android.widget.ImageButton", "comment")
	label := proposalNode(3, 1, 2, display, labelBounds,
		"android.widget.TextView", labelText)
	container.Children = []int{2, 3}
	return revalCapture(display, revalRoot(display.ID, 1), container, comment, label)
}

func tapBubbleProposalRequireTapWithin(testingHandle *testing.T,
	harnessUnderTest *harness, expectedBounds device.Rect) {
	testingHandle.Helper()
	taps := harnessUnderTest.device.taps
	if len(taps) != 1 {
		testingHandle.Fatalf("taps = %v, want exactly one", taps)
	}
	if !expectedBounds.Contains(taps[0][0], taps[0][1]) {
		testingHandle.Fatalf("tap = %v, want inside %+v", taps[0], expectedBounds)
	}
}

func tapTextProposalEditable(index, parent, depth int, display device.Display,
	bounds device.Rect, clickable, passwordProtected bool, children ...int) device.RawNode {
	input := proposalNode(index, parent, depth, display, bounds,
		"android.widget.EditText", "")
	input.Editable = true
	input.HasClickable = true
	input.Clickable = clickable
	input.HasPassword = true
	input.Password = passwordProtected
	input.Children = children
	return input
}

func TestProposalTapGrid3x3ExecutesDirectly(testingHandle *testing.T) {
	cardBounds := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		card := proposalClickableNode(1, 0, 1, display, cardBounds,
			"android.widget.FrameLayout", "")
		vertical := proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 400, Top: 0, Right: 600, Bottom: 1000},
			"android.widget.ImageButton", "vertical")
		horizontal := proposalClickableNode(3, 1, 2, display,
			device.Rect{Left: 0, Top: 400, Right: 1000, Bottom: 600},
			"android.widget.ImageButton", "horizontal")
		card.Children = []int{2, 3}
		return revalCapture(display, revalRoot(display.ID, 1),
			card, vertical, horizontal)
	})

	harnessUnderTest := newTapScopeHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	card := tapPublicBoundsNode(currentTree(testingHandle, harnessUnderTest.engine).Root, cardBounds)
	if card == nil {
		testingHandle.Fatal("cross-covered card not found")
	}

	result := harnessUnderTest.actP(testingHandle, "Pproposalgrid",
		start.Observation.ID, protocol.Operation{
			Type:   "tap",
			Target: &protocol.Target{UseRef: true, Ref: card.Ref},
		})
	proposalRequireExecuted(testingHandle, result)
	proposalRequireTap(testingHandle, harnessUnderTest, [2]int{166, 166})
}

func TestProposalTapCoextensiveChildDelegatesAndRejectsOverflow(testingHandle *testing.T) {
	testingHandle.Run("coextensive_child_delegates", func(testingHandle *testing.T) {
		shellBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 800}
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			shell := proposalClickableNode(1, 0, 1, display, shellBounds,
				"android.widget.FrameLayout", "shell")
			delegate := proposalClickableNode(2, 1, 2, display, shellBounds,
				"android.widget.Button", "delegate")
			shell.Children = []int{2}
			return revalCapture(display, revalRoot(display.ID, 1), shell, delegate)
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root
		shell := proposalFindLabel(testingHandle, root, "shell")
		delegate := proposalFindLabel(testingHandle, root, "delegate")

		result := harnessUnderTest.actP(testingHandle, "Pcoextensive",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: shell.Ref},
			})
		proposalRequireExecuted(testingHandle, result)
		proposalRequireTap(testingHandle, harnessUnderTest, [2]int{500, 550})
		if delegate.Bounds == nil ||
			!rectFromBounds(delegate.Bounds).Contains(harnessUnderTest.device.taps[0][0],
				harnessUnderTest.device.taps[0][1]) {
			testingHandle.Fatalf("delegated tap = %v, want inside %s", harnessUnderTest.device.taps, delegate.Ref)
		}
	})

	testingHandle.Run("overflow_child_fails_bilateral_guard", func(testingHandle *testing.T) {
		cardBounds := device.Rect{Left: 300, Top: 900, Right: 780, Bottom: 1500}
		overflowBounds := screenRect()
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			card := proposalClickableNode(1, 0, 1, display, cardBounds,
				"android.widget.FrameLayout", "small-card")
			overflow := proposalClickableNode(2, 1, 2, display, overflowBounds,
				"android.widget.Button", "overflow")
			card.Children = []int{2}
			return revalCapture(display, revalRoot(display.ID, 1), card, overflow)
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root
		card := proposalFindLabel(testingHandle, root, "small-card")
		overflow := proposalFindLabel(testingHandle, root, "overflow")

		result := harnessUnderTest.actP(testingHandle, "Poverflow",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: card.Ref},
			})
		details := proposalRequireStatic422(testingHandle, result,
			protocol.CodeNoSafePoint)
		proposalRequireDetailString(testingHandle, details, "scope", "target")
		proposalRequireDetailString(testingHandle, details, "scope_ref", card.Ref)
		candidates := proposalCandidates(testingHandle, details)
		proposalRequireCandidateRefs(testingHandle, candidates, overflow.Ref)
		proposalRequireCandidateRelation(testingHandle, candidates, overflow.Ref, "child")
		proposalRequireCandidateExecutable(testingHandle, candidates, overflow.Ref)
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})
}

func TestProposalTapBubbleColocatedNetEasePreventsAncestorDrift(testingHandle *testing.T) {
	labelBounds := device.Rect{Left: 925, Top: 1690, Right: 1018, Bottom: 1750}
	commentBounds := device.Rect{Left: 915, Top: 1675, Right: 1028, Bottom: 1787}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		return tapBubbleProposalNetEaseCapture(display, labelBounds, "999+")
	})

	harnessUnderTest := newTapScopeHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	label := proposalFindLabel(testingHandle,
		currentTree(testingHandle, harnessUnderTest.engine).Root, "999+")

	result := harnessUnderTest.actP(testingHandle, "Pnetease",
		start.Observation.ID, protocol.Operation{
			Type:   "tap",
			Target: &protocol.Target{UseRef: true, Ref: label.Ref},
		})
	proposalRequireExecuted(testingHandle, result)
	tapBubbleProposalRequireTapWithin(testingHandle, harnessUnderTest, commentBounds)
}

func TestProposalTapBubbleColocatedAntiBackdropGuards(testingHandle *testing.T) {
	testingHandle.Run("external_half_screen_backdrop_is_not_absorbed", func(testingHandle *testing.T) {
		playerBounds := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2200}
		labelBounds := device.Rect{Left: 400, Top: 1500, Right: 680, Bottom: 1650}
		backdropBounds := device.Rect{Left: 0, Top: 1100, Right: 1080, Bottom: 2400}
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			player := proposalClickableNode(1, 0, 1, display, playerBounds,
				"android.widget.FrameLayout", "player")
			label := proposalNode(2, 1, 2, display, labelBounds,
				"android.widget.TextView", "overlay")
			backdrop := proposalClickableNode(3, 0, 1, display, backdropBounds,
				"android.widget.Button", "backdrop")
			player.Children = []int{2}
			return revalCapture(display, revalRoot(display.ID, 1, 3),
				player, label, backdrop)
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root
		label := proposalFindLabel(testingHandle, root, "overlay")
		player := proposalFindLabel(testingHandle, root, "player")

		result := harnessUnderTest.actP(testingHandle, "Pbackdrop",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: label.Ref},
			})
		details := proposalRequireStatic422(testingHandle, result,
			protocol.CodeNoSafePoint)
		proposalRequireDetailString(testingHandle, details, "scope", "ancestor")
		proposalRequireDetailString(testingHandle, details, "scope_ref", player.Ref)
		proposalRequireDetailString(testingHandle, details, "ancestor_ref", player.Ref)
		requireDetailAbsent(testingHandle, details, "target_center")
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})

	testingHandle.Run("large_noninteractive_wrapper_small_center_button_is_not_absorbed",
		func(testingHandle *testing.T) {
			wrapperBounds := device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 2100}
			buttonBounds := device.Rect{Left: 510, Top: 1170, Right: 570, Bottom: 1230}
			pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
				wrapper := proposalNode(1, 0, 1, display, wrapperBounds,
					"android.widget.FrameLayout", "")
				button := proposalClickableNode(2, 1, 2, display, buttonBounds,
					"android.widget.Button", "dot")
				wrapper.Children = []int{2}
				return revalCapture(display, revalRoot(display.ID, 1), wrapper, button)
			})

			harnessUnderTest := newTapScopeHarness(testingHandle)
			harnessUnderTest.device.scriptCaptures(pageStep)
			start := harnessUnderTest.startSession()
			root := currentTree(testingHandle, harnessUnderTest.engine).Root
			wrapper := tapPublicBoundsNode(root, wrapperBounds)
			if wrapper == nil {
				testingHandle.Fatal("large non-interactive wrapper not found")
			}
			button := proposalFindLabel(testingHandle, root, "dot")

			result := harnessUnderTest.actP(testingHandle, "Pwrappercenter",
				start.Observation.ID, protocol.Operation{
					Type:   "tap",
					Target: &protocol.Target{UseRef: true, Ref: wrapper.Ref},
				})
			details := proposalRequireStaticReason(testingHandle, result,
				protocol.CodeNodeUnsupported, "no_interactive_ancestor")
			proposalRequireDetailString(testingHandle, details, "scope", "target")
			proposalRequireDetailString(testingHandle, details, "scope_ref", wrapper.Ref)
			candidates := proposalCandidates(testingHandle, details)
			proposalRequireCandidateRefs(testingHandle, candidates, button.Ref)
			proposalRequireCandidateRelation(testingHandle, candidates, button.Ref, "child")
			proposalRequireCandidateExecutable(testingHandle, candidates, button.Ref)
			requireDetailIntSlice(testingHandle, details, "target_center", 540, 1200)
			proposalRequireNoDispatch(testingHandle, harnessUnderTest)
			requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
		})
}

func TestProposalTapBubbleColocatedMultiCandidateTieBreakAndAmbiguous(testingHandle *testing.T) {
	labelBounds := device.Rect{Left: 400, Top: 900, Right: 680, Bottom: 1100}

	testingHandle.Run("unique_center_containment_wins", func(testingHandle *testing.T) {
		containingBounds := device.Rect{Left: 500, Top: 950, Right: 580, Bottom: 1050}
		overlappingBounds := device.Rect{Left: 400, Top: 900, Right: 540, Bottom: 1100}
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			label := proposalNode(1, 0, 1, display, labelBounds,
				"android.widget.TextView", "badge")
			containing := proposalClickableNode(2, 0, 1, display, containingBounds,
				"android.widget.Button", "one")
			overlapping := proposalClickableNode(3, 0, 1, display, overlappingBounds,
				"android.widget.Button", "two")
			return revalCapture(display, revalRoot(display.ID, 1, 2, 3),
				label, containing, overlapping)
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root
		label := proposalFindLabel(testingHandle, root, "badge")
		containing := proposalFindLabel(testingHandle, root, "one")

		result := harnessUnderTest.actP(testingHandle, "Ptiebreak",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: label.Ref},
			})
		proposalRequireExecuted(testingHandle, result)
		if containing.Bounds == nil {
			testingHandle.Fatal("unique center-containing button lost its bounds")
		}
		tapBubbleProposalRequireTapWithin(testingHandle, harnessUnderTest,
			rectFromBounds(containing.Bounds))
	})

	testingHandle.Run("two_center_containers_are_colocated_ambiguous", func(testingHandle *testing.T) {
		firstBounds := device.Rect{Left: 500, Top: 950, Right: 580, Bottom: 1050}
		secondBounds := device.Rect{Left: 520, Top: 990, Right: 760, Bottom: 1040}
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			label := proposalNode(1, 0, 1, display, labelBounds,
				"android.widget.TextView", "badge")
			first := proposalClickableNode(2, 0, 1, display, firstBounds,
				"android.widget.Button", "one")
			second := proposalClickableNode(3, 0, 1, display, secondBounds,
				"android.widget.Button", "two")
			return revalCapture(display, revalRoot(display.ID, 1, 2, 3),
				label, first, second)
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root
		label := proposalFindLabel(testingHandle, root, "badge")
		first := proposalFindLabel(testingHandle, root, "one")
		second := proposalFindLabel(testingHandle, root, "two")

		result := harnessUnderTest.actP(testingHandle, "Pambiguous",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: label.Ref},
			})
		details := proposalRequireStaticReason(testingHandle, result,
			protocol.CodeNoSafePoint, "colocated_ambiguous")
		proposalRequireDetailString(testingHandle, details, "scope", "target")
		proposalRequireDetailString(testingHandle, details, "scope_ref", label.Ref)
		candidates := proposalCandidates(testingHandle, details)
		refs := proposalCandidateRefs(candidates)
		if len(refs) != 2 || refs[0] != first.Ref || refs[1] != second.Ref {
			testingHandle.Fatalf("candidate order = %v, want [%s %s]",
				refs, first.Ref, second.Ref)
		}
		for _, candidateRef := range refs {
			proposalRequireCandidateRelation(testingHandle, candidates,
				candidateRef, "overlapping")
			proposalRequireCandidateExecutable(testingHandle, candidates, candidateRef)
		}
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})
}

func TestProposalTapBubbleColocatedRevalidateChildChanged(testingHandle *testing.T) {
	labelBounds := device.Rect{Left: 925, Top: 1690, Right: 1018, Bottom: 1750}

	testingHandle.Run("label_changed", func(testingHandle *testing.T) {
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return tapBubbleProposalNetEaseCapture(display, labelBounds, "999+")
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return tapBubbleProposalNetEaseCapture(display, labelBounds, "1000+")
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		label := proposalFindLabel(testingHandle,
			currentTree(testingHandle, harnessUnderTest.engine).Root, "999+")
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(testingHandle, "Pchildlabel",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: label.Ref},
			})
		proposalRequireTargetChanged(testingHandle, result, "child_changed")
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
	})

	testingHandle.Run("moved_off_colocated_button", func(testingHandle *testing.T) {
		movedBounds := device.Rect{Left: 700, Top: 1690, Right: 793, Bottom: 1750}
		initialStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return tapBubbleProposalNetEaseCapture(display, labelBounds, "999+")
		})
		freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
			return tapBubbleProposalNetEaseCapture(display, movedBounds, "999+")
		})

		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(initialStep)
		start := harnessUnderTest.startSession()
		label := proposalFindLabel(testingHandle,
			currentTree(testingHandle, harnessUnderTest.engine).Root, "999+")
		harnessUnderTest.device.scriptCaptures(freshStep)

		result := harnessUnderTest.actP(testingHandle, "Pchildmoved",
			start.Observation.ID, protocol.Operation{
				Type:   "tap",
				Target: &protocol.Target{UseRef: true, Ref: label.Ref},
			})
		proposalRequireTargetChanged(testingHandle, result, "child_changed")
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
	})
}

func TestProposalTapBubbleFalseReturnsCandidates(testingHandle *testing.T) {
	playerBounds := device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}
	labelBounds := device.Rect{Left: 100, Top: 500, Right: 400, Bottom: 600}
	overlapBounds := device.Rect{Left: 200, Top: 480, Right: 420, Bottom: 620}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		player := proposalClickableNode(1, 0, 1, display, playerBounds,
			"android.widget.FrameLayout", "player")
		label := proposalNode(2, 1, 2, display, labelBounds,
			"android.widget.TextView", "caption")
		overlap := proposalClickableNode(3, 1, 2, display, overlapBounds,
			"android.widget.Button", "overlay-button")
		player.Children = []int{2, 3}
		return revalCapture(display, revalRoot(display.ID, 1), player, label, overlap)
	})

	harnessUnderTest := newTapScopeHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	root := currentTree(testingHandle, harnessUnderTest.engine).Root
	label := proposalFindLabel(testingHandle, root, "caption")
	player := proposalFindLabel(testingHandle, root, "player")
	overlap := proposalFindLabel(testingHandle, root, "overlay-button")
	falseValue := false

	result := harnessUnderTest.actWith(testingHandle, "Pstaticcandidates",
		bubbleRequest(start.Observation.ID, label.Ref, &falseValue))
	details := proposalRequireStaticReason(testingHandle, result,
		protocol.CodeNodeUnsupported, "target_missing_action")
	proposalRequireDetailString(testingHandle, details, "scope", "target")
	proposalRequireDetailString(testingHandle, details, "scope_ref", label.Ref)
	candidates := proposalCandidates(testingHandle, details)
	proposalRequireCandidateRefs(testingHandle, candidates, player.Ref, overlap.Ref)
	proposalRequireCandidateRelation(testingHandle, candidates, player.Ref, "ancestor")
	proposalRequireCandidateRelation(testingHandle, candidates, overlap.Ref, "overlapping")
	proposalRequireCandidateExecutable(testingHandle, candidates, player.Ref)
	proposalRequireCandidateExecutable(testingHandle, candidates, overlap.Ref)
	proposalRequireNoDispatch(testingHandle, harnessUnderTest)
	requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
}

func TestProposalTextRefUnifiedStaticErrorsAndPreservedTextContract(testingHandle *testing.T) {
	testingHandle.Run("root_invalid_target", func(testingHandle *testing.T) {
		harnessUnderTest := newTapScopeHarness(testingHandle)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root

		result := harnessUnderTest.actWith(testingHandle, "Ptextroot",
			textFlowRequest(start.Observation.ID, root.Ref, "hello", nil))
		details := proposalRequireStaticReason(testingHandle, result,
			protocol.CodeInvalidTarget, "root")
		proposalRequireDetailString(testingHandle, details, "scope", "target")
		proposalRequireDetailString(testingHandle, details, "scope_ref", root.Ref)
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})

	testingHandle.Run("context_invalid_target", func(testingHandle *testing.T) {
		pageStep := tapCardWithContextDescendantStep()
		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		contextNode := tapContextNode(currentTree(testingHandle, harnessUnderTest.engine).Root)
		if contextNode == nil {
			testingHandle.Fatal("context node not found")
		}

		result := harnessUnderTest.actWith(testingHandle, "Ptextcontext",
			textFlowRequest(start.Observation.ID, contextNode.Ref, "hello", nil))
		details := proposalRequireStaticReason(testingHandle, result,
			protocol.CodeInvalidTarget, "context")
		proposalRequireDetailString(testingHandle, details, "scope", "target")
		proposalRequireDetailString(testingHandle, details, "scope_ref", contextNode.Ref)
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})

	testingHandle.Run("no_geometry_invalid_target", func(testingHandle *testing.T) {
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			card := proposalClickableNode(1, 0, 1, display, tapCardBounds,
				"android.widget.FrameLayout", "card")
			target := proposalNode(2, 1, 2, display, device.Rect{},
				"android.widget.TextView", "without geometry")
			card.Children = []int{2}
			return revalCapture(display, revalRoot(display.ID, 1), card, target)
		})
		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		target := proposalFindLabel(testingHandle,
			currentTree(testingHandle, harnessUnderTest.engine).Root, "without geometry")

		result := harnessUnderTest.actWith(testingHandle, "Ptextnogeometry",
			textFlowRequest(start.Observation.ID, target.Ref, "hello", nil))
		details := proposalRequireStaticReason(testingHandle, result,
			protocol.CodeInvalidTarget, "no_geometry")
		proposalRequireDetailString(testingHandle, details, "scope", "target")
		proposalRequireDetailString(testingHandle, details, "scope_ref", target.Ref)
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})

	testingHandle.Run("input_without_tap_action", func(testingHandle *testing.T) {
		inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			input := tapTextProposalEditable(1, 0, 1, display, inputBounds, false, false)
			input.HasFocused, input.Focused = true, true
			return revalCapture(display, revalRoot(display.ID, 1), input)
		})
		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		input := tapPublicBoundsNode(currentTree(testingHandle, harnessUnderTest.engine).Root, inputBounds)
		if input == nil || !hasAction(input, protocol.ActionText) || hasAction(input, protocol.ActionTap) {
			testingHandle.Fatalf("expected input with text but no tap, got %+v", input)
		}

		autoSubmit := false
		result := harnessUnderTest.actWith(testingHandle, "Ptextdirect",
			textFlowRequest(start.Observation.ID, input.Ref, "hello", &autoSubmit))
		proposalRequireExecuted(testingHandle, result)
		dev := harnessUnderTest.device
		if len(dev.taps) != 0 || len(dev.texts) != 1 || dev.texts[0] != "hello" || len(dev.keys) != 0 {
			testingHandle.Fatalf("want one direct write, got taps=%v texts=%v keys=%v", dev.taps, dev.texts, dev.keys)
		}
	})

	testingHandle.Run("focus_blocked_reports_blocking_ref", func(testingHandle *testing.T) {
		inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
		pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
			input := tapTextProposalEditable(1, 0, 1, display, inputBounds, true, false, 2)
			cover := proposalClickableNode(2, 1, 2, display, inputBounds,
				"android.widget.Button", "cover")
			return revalCapture(display, revalRoot(display.ID, 1), input, cover)
		})
		harnessUnderTest := newTapScopeHarness(testingHandle)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingHandle, harnessUnderTest.engine).Root
		input := tapPublicBoundsNode(root, inputBounds)
		if input == nil {
			testingHandle.Fatal("blocked text input not found")
		}
		cover := proposalFindLabel(testingHandle, root, "cover")

		result := harnessUnderTest.actWith(testingHandle, "Ptextfocusblocked",
			textFlowRequest(start.Observation.ID, input.Ref, "hello", nil))
		details := proposalRequireStaticReason(testingHandle, result,
			protocol.CodeNoSafePoint, "blocked")
		proposalRequireDetailString(testingHandle, details, "scope", "target")
		proposalRequireDetailString(testingHandle, details, "scope_ref", input.Ref)
		requireDetailStringSlice(testingHandle, details, "blocking_refs", cover.Ref)
		requireDetailAbsent(testingHandle, details, "candidates")
		proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
	})

	testingHandle.Run("noneditable_preserves_text_uncertain_details",
		func(testingHandle *testing.T) {
			harnessUnderTest := newTapScopeHarness(testingHandle)
			start := harnessUnderTest.startSession()
			button := findRole(currentTree(testingHandle, harnessUnderTest.engine).Root,
				protocol.RoleButton)
			if button == nil {
				testingHandle.Fatal("non-editable button not found")
			}

			result := harnessUnderTest.actWith(testingHandle, "Ptextnoteditable",
				textFlowRequest(start.Observation.ID, button.Ref, "hello", nil))
			details := proposalRequireStaticReason(testingHandle, result,
				protocol.CodeTextTargetUnknown, "target_not_editable")
			requireTextDetail(testingHandle, details, "stage", "target")
			requireTextDispatch(testingHandle, details,
				"not_attempted", "not_attempted", "not_attempted")
			proposalRequireNoDispatch(testingHandle, harnessUnderTest)
			requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
		})

	testingHandle.Run("protected_preserves_text_uncertain_details",
		func(testingHandle *testing.T) {
			inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
			pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
				input := tapTextProposalEditable(1, 0, 1, display, inputBounds, true, true)
				return revalCapture(display, revalRoot(display.ID, 1), input)
			})
			harnessUnderTest := newTapScopeHarness(testingHandle)
			harnessUnderTest.device.scriptCaptures(pageStep)
			start := harnessUnderTest.startSession()
			input := tapPublicBoundsNode(currentTree(testingHandle, harnessUnderTest.engine).Root,
				inputBounds)
			if input == nil {
				testingHandle.Fatal("protected text input not found")
			}

			result := harnessUnderTest.actWith(testingHandle, "Ptextprotected",
				textFlowRequest(start.Observation.ID, input.Ref, "hello", nil))
			details := proposalRequireStaticReason(testingHandle, result,
				protocol.CodeTextTargetUnknown, "target_protected")
			requireTextDetail(testingHandle, details, "stage", "target")
			requireTextDispatch(testingHandle, details,
				"not_attempted", "not_attempted", "not_attempted")
			proposalRequireNoDispatch(testingHandle, harnessUnderTest)
			requireReadyObservation(testingHandle, harnessUnderTest, start.Observation.ID)
		})

	testingHandle.Run("claimed_target_change_reports_revalidation_details",
		func(testingHandle *testing.T) {
			inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
			initialBuilder := func(display device.Display) device.Capture {
				input := tapTextProposalEditable(1, 0, 1, display, inputBounds, true, false)
				return revalCapture(display, revalRoot(display.ID, 1), input)
			}
			freshStep := proposalCaptureStep(func(display device.Display) device.Capture {
				capture := initialBuilder(display)
				capture.Nodes[1].ClassName = "android.widget.MultiAutoCompleteTextView"
				return capture
			})

			harnessUnderTest := newTapScopeHarness(testingHandle)
			harnessUnderTest.device.scriptCaptures(proposalCaptureStep(initialBuilder))
			start := harnessUnderTest.startSession()
			input := tapPublicBoundsNode(currentTree(testingHandle, harnessUnderTest.engine).Root,
				inputBounds)
			if input == nil {
				testingHandle.Fatal("claimed text input not found")
			}
			harnessUnderTest.device.scriptCaptures(freshStep)

			result := harnessUnderTest.actWith(testingHandle, "Ptextchanged",
				textFlowRequest(start.Observation.ID, input.Ref, "hello", nil))
			requireNotExecuted(testingHandle, result, protocol.CodeTargetChanged)
			status := result.HTTPStatus
			if status == 0 {
				status = protocol.Status(result.Error.Code)
			}
			if status != 409 {
				testingHandle.Fatalf("target_changed HTTP status = %d, want 409", status)
			}
			requireActRejectionDetails(testingHandle, result, "text", input.Ref, targetChangedReasonTargetMissing)
			if result.TextInput == nil || result.TextInput.Write != protocol.TextInputNotExecuted ||
				result.TextInput.Submit != protocol.TextInputNotExecuted {
				testingHandle.Fatalf("text input snapshot = %+v, want no primitives attempted", result.TextInput)
			}
			proposalRequireNoDispatch(testingHandle, harnessUnderTest)
		})
}
