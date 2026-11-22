package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// proposalRemovePublicAction removes one capability from an already published
// node. The observation builder never publishes a scroll role without swipe,
// so this fixture mutation states the target_missing_action wire case directly.
func proposalRemovePublicAction(testingHandle *testing.T, node *protocol.Node,
	action protocol.Action) {
	testingHandle.Helper()
	if node == nil {
		testingHandle.Fatal("cannot remove an action from a nil public node")
	}
	filtered := make([]protocol.Action, 0, len(node.Actions))
	removed := false
	for _, existing := range node.Actions {
		if existing == action {
			removed = true
			continue
		}
		filtered = append(filtered, existing)
	}
	if !removed {
		testingHandle.Fatalf("public node %s does not carry action %q", node.Ref, action)
	}
	node.Actions = filtered
}

func proposalRequireEmptyTargetActions(testingHandle *testing.T, details map[string]any) {
	testingHandle.Helper()
	rawActions, exists := details["target_actions"]
	if !exists {
		testingHandle.Fatalf("details lack target_actions: %#v", details)
	}
	actions, ok := rawActions.([]string)
	if !ok {
		testingHandle.Fatalf("target_actions = %#v (%T), want []string",
			rawActions, rawActions)
	}
	if len(actions) != 0 {
		testingHandle.Fatalf("target_actions = %#v, want []", actions)
	}
}

func proposalRequirePointScope(testingHandle *testing.T, details map[string]any) {
	testingHandle.Helper()
	proposalRequireDetailString(testingHandle, details, "scope", "point")
	if value, exists := details["scope_ref"]; exists {
		testingHandle.Fatalf("point-scope details carry scope_ref = %#v", value)
	}
}

func proposalScrollCandidateByRef(testingHandle *testing.T,
	candidates []map[string]any, ref string) map[string]any {
	testingHandle.Helper()
	for _, candidate := range candidates {
		if candidate["ref"] == ref {
			return candidate
		}
	}
	testingHandle.Fatalf("candidates %#v lack ref %s",
		proposalCandidateRefs(candidates), ref)
	return nil
}

func TestProposalScrollLiftNonScrollItemExecutesAndSkipsItemRevalidate(
	testingHandle *testing.T) {
	initialSpecs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 700},
			Class: "android.widget.TextView", Text: "item-before"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1500, Right: 1080, Bottom: 1700},
			Class: "android.widget.TextView", Text: "other-item"},
	}
	// The fresh frame deliberately drops the original list item. Only the
	// lifted scroll ancestor's identity and scroll scope may matter.
	freshSpecs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1100},
			Class: "android.widget.TextView", Text: "replacement-item"},
	}
	initialStep := scrollPageStep(initialSpecs)
	freshStep := scrollPageStep(freshSpecs)
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(initialStep)
	start := harnessUnderTest.startSession()
	itemNode := proposalFindLabel(testingHandle,
		currentTree(testingHandle, harnessUnderTest.engine).Root, "item-before")
	harnessUnderTest.device.scriptCaptures(freshStep, freshStep)

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type: "scroll",
			Container: &protocol.Target{
				UseRef: true, Ref: itemNode.Ref,
			},
			Direction: protocol.ScrollDirectionDown,
		})
	proposalRequireExecuted(testingHandle, result)
	proposalRequireSwipe(testingHandle, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 1776, x2: 540, y2: 624, durationMS: 400,
	})
}

func TestProposalScrollMissingSwipeReturnsUnsupportedWithCandidates(
	testingHandle *testing.T) {
	innerBounds := device.Rect{Left: 100, Top: 900, Right: 980, Bottom: 1500}
	pageStep := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: innerBounds, Class: "androidx.recyclerview.widget.RecyclerView",
			Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 100, Top: 1000, Right: 980, Bottom: 1200},
			Class: "android.widget.TextView", Text: "inner-content"},
	})
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	root := currentTree(testingHandle, harnessUnderTest.engine).Root
	targetNode := tapPublicNode(root, protocol.RoleScroll, innerBounds)
	ancestorNode := tapPublicNode(root, protocol.RoleScroll, screenRect())
	if targetNode == nil || ancestorNode == nil {
		testingHandle.Fatal("missing-swipe fixture nodes were not published")
	}
	proposalRemovePublicAction(testingHandle, targetNode, protocol.ActionSwipe)

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type: "scroll",
			Container: &protocol.Target{
				UseRef: true, Ref: targetNode.Ref,
			},
			Direction: protocol.ScrollDirectionDown,
		})
	details := proposalRequireStaticReason(testingHandle, result,
		protocol.CodeNodeUnsupported, "target_missing_action")
	proposalRequireDetailString(testingHandle, details, "scope", "target")
	proposalRequireDetailString(testingHandle, details, "scope_ref", targetNode.Ref)
	proposalRequireEmptyTargetActions(testingHandle, details)
	candidates := proposalCandidates(testingHandle, details)
	proposalRequireCandidateRefs(testingHandle, candidates, ancestorNode.Ref)
	proposalRequireCandidateExecutable(testingHandle, candidates, ancestorNode.Ref)
	ancestorCandidate := proposalScrollCandidateByRef(testingHandle, candidates,
		ancestorNode.Ref)
	if ancestorCandidate["role"] != string(protocol.RoleScroll) {
		testingHandle.Fatalf("ancestor candidate role = %#v, want %q",
			ancestorCandidate["role"], protocol.RoleScroll)
	}
	switch ancestorCandidate["relation"] {
	case "ancestor", "page_scroll":
	default:
		testingHandle.Fatalf("ancestor candidate relation = %#v, want ancestor or page_scroll",
			ancestorCandidate["relation"])
	}
	proposalRequireNoDispatch(testingHandle, harnessUnderTest)
}

func TestProposalScrollNonScrollNoScrollAncestorReturnsPageScrollCandidates(
	testingHandle *testing.T) {
	pageBounds := device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2100}
	pageStep := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: pageBounds, Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "page-content"},
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 2200, Right: 1080, Bottom: 2300},
			Class: "android.widget.TextView", Text: "detached-footer"},
	})
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	root := currentTree(testingHandle, harnessUnderTest.engine).Root
	targetNode := proposalFindLabel(testingHandle, root, "detached-footer")
	pageNode := tapPublicNode(root, protocol.RoleScroll, pageBounds)
	if pageNode == nil {
		testingHandle.Fatal("page scroll container was not published")
	}

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type: "scroll",
			Container: &protocol.Target{
				UseRef: true, Ref: targetNode.Ref,
			},
			Direction: protocol.ScrollDirectionDown,
		})
	details := proposalRequireStaticReason(testingHandle, result,
		protocol.CodeNodeUnsupported, "not_scrollable")
	proposalRequireDetailString(testingHandle, details, "scope", "target")
	proposalRequireDetailString(testingHandle, details, "scope_ref", targetNode.Ref)
	proposalRequireEmptyTargetActions(testingHandle, details)
	candidates := proposalCandidates(testingHandle, details)
	proposalRequireCandidateRefs(testingHandle, candidates, pageNode.Ref)
	proposalRequireCandidateRelation(testingHandle, candidates, pageNode.Ref, "page_scroll")
	proposalRequireCandidateExecutable(testingHandle, candidates, pageNode.Ref)
	proposalRequireNoDispatch(testingHandle, harnessUnderTest)
}

func TestProposalScrollDirectContainerUniqueInnerFallback(testingHandle *testing.T) {
	outerBounds := device.Rect{Left: 300, Top: 1150, Right: 780, Bottom: 1250}
	pageStep := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: outerBounds, Class: "android.widget.ScrollView", Scrollable: true},
		// This overflowing child is not coextensive with the small outer
		// container and blocks every outer axis, but it is uniquely executable.
		{Parent: 1, Bounds: screenRect(), Class: "androidx.recyclerview.widget.RecyclerView",
			Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "overflow-content"},
	})
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	outerNode := tapPublicNode(currentTree(testingHandle, harnessUnderTest.engine).Root,
		protocol.RoleScroll, outerBounds)
	if outerNode == nil {
		testingHandle.Fatal("small outer scroll container was not published")
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type: "scroll",
			Container: &protocol.Target{
				UseRef: true, Ref: outerNode.Ref,
			},
			Direction: protocol.ScrollDirectionDown,
		})
	proposalRequireExecuted(testingHandle, result)
	proposalRequireSwipe(testingHandle, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 1776, x2: 540, y2: 624, durationMS: 400,
	})
}

func TestProposalScrollCoordNestedStackPicksInnermost(testingHandle *testing.T) {
	outerBounds := device.Rect{Left: 0, Top: 363, Right: 1080, Bottom: 2265}
	innerBounds := device.Rect{Left: 40, Top: 400, Right: 1040, Bottom: 2225}
	pageStep := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: outerBounds, Class: "android.widget.FrameLayout", Scrollable: true},
		{Parent: 1, Bounds: outerBounds, Class: "android.view.ViewGroup", Scrollable: true},
		{Parent: 2, Bounds: innerBounds, Class: "androidx.recyclerview.widget.RecyclerView",
			Scrollable: true},
		{Parent: 3, Bounds: device.Rect{Left: 100, Top: 900, Right: 980, Bottom: 1100},
			Class: "android.widget.TextView", Text: "stack-content"},
	})
	const coordX = 540
	const coordY = 1100
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	root := currentTree(testingHandle, harnessUnderTest.engine).Root
	if outerNodes := proposalScrollNodesByBounds(root, outerBounds); len(outerNodes) != 2 {
		testingHandle.Fatalf("outer same-viewport stack = %d nodes, want 2", len(outerNodes))
	}
	if innerNodes := proposalScrollNodesByBounds(root, innerBounds); len(innerNodes) != 1 {
		testingHandle.Fatalf("inner scroll node = %d nodes, want 1", len(innerNodes))
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type:      "scroll",
			Container: &protocol.Target{X: coordX, Y: coordY},
			Direction: protocol.ScrollDirectionDown,
		})
	proposalRequireExecuted(testingHandle, result)
	proposalRequireSwipe(testingHandle, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 1750, x2: 540, y2: 874, durationMS: 400,
	})
}

func TestProposalScrollCoordInnerInsufficientFallsBackToAncestor(
	testingHandle *testing.T) {
	innerBounds := device.Rect{Left: 500, Top: 1050, Right: 580, Bottom: 1150}
	pageStep := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: innerBounds, Class: "android.widget.HorizontalScrollView",
			Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "outer-content"},
	})
	const coordX = 540
	const coordY = 1100
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type:      "scroll",
			Container: &protocol.Target{X: coordX, Y: coordY},
			Direction: protocol.ScrollDirectionDown,
		})
	proposalRequireExecuted(testingHandle, result)
	// The inner strip has only 100px of travel room and is skipped. The outer
	// container's center line avoids that strip and uses its lower safe span.
	proposalRequireSwipe(testingHandle, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 2075, x2: 540, y2: 1475, durationMS: 400,
	})
}

func TestProposalScrollCoordNoContainerReturnsInvalidTargetWithPageScrolls(
	testingHandle *testing.T) {
	pageBounds := device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2100}
	pageStep := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: pageBounds, Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			Class: "android.widget.TextView", Text: "page-content"},
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 2200, Right: 1080, Bottom: 2350},
			Class: "android.widget.TextView", Text: "footer-outside-scroll"},
	})
	const coordX = 540
	const coordY = 2250
	harnessUnderTest := newHarness(testingHandle)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	pageNode := tapPublicNode(currentTree(testingHandle, harnessUnderTest.engine).Root,
		protocol.RoleScroll, pageBounds)
	if pageNode == nil {
		testingHandle.Fatal("page scroll container was not published")
	}

	result := harnessUnderTest.actP(testingHandle, "", start.Observation.ID,
		protocol.Operation{
			Type:      "scroll",
			Container: &protocol.Target{X: coordX, Y: coordY},
			Direction: protocol.ScrollDirectionDown,
		})
	details := proposalRequireStaticReason(testingHandle, result,
		protocol.CodeInvalidTarget, "no_scroll_container")
	proposalRequirePointScope(testingHandle, details)
	candidates := proposalCandidates(testingHandle, details)
	proposalRequireCandidateRefs(testingHandle, candidates, pageNode.Ref)
	proposalRequireCandidateRelation(testingHandle, candidates, pageNode.Ref, "page_scroll")
	proposalRequireCandidateExecutable(testingHandle, candidates, pageNode.Ref)
	proposalRequireNoDispatch(testingHandle, harnessUnderTest)
}

func TestProposalScrollCoordCompetingContainersDisambiguateAndReject(
	testingHandle *testing.T) {
	testingHandle.Run("unique_executable", func(testingCase *testing.T) {
		containerBounds := device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1300}
		shortBounds := device.Rect{Left: 500, Top: 1000, Right: 580, Bottom: 1100}
		pageStep := scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
			{Parent: 1, Bounds: containerBounds, Class: "androidx.recyclerview.widget.RecyclerView",
				Scrollable: true},
			{Parent: 1, Bounds: shortBounds, Class: "android.widget.HorizontalScrollView",
				Scrollable: true},
		})
		const coordX = 540
		const coordY = 1050
		harnessUnderTest := newHarness(testingCase)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

		result := harnessUnderTest.actP(testingCase, "", start.Observation.ID,
			protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{X: coordX, Y: coordY},
				Direction: protocol.ScrollDirectionDown,
			})
		proposalRequireExecuted(testingCase, result)
		// The short strip cannot travel; the 400px crossing container remains
		// executable after the short strip is subtracted from its center line.
		proposalRequireSwipe(testingCase, harnessUnderTest, swipeCall{
			displayID: 12, x1: 540, y1: 1246, x2: 540, y2: 1155, durationMS: 400,
		})
	})

	testingHandle.Run("competing", func(testingCase *testing.T) {
		containerABounds := device.Rect{Left: 0, Top: 700, Right: 1080, Bottom: 1700}
		containerBBounds := device.Rect{Left: 300, Top: 900, Right: 780, Bottom: 2100}
		pageStep := scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
			{Parent: 1, Bounds: containerABounds,
				Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
			{Parent: 1, Bounds: containerBBounds,
				Class: "android.widget.HorizontalScrollView", Scrollable: true},
		})
		const coordX = 540
		const coordY = 1100
		harnessUnderTest := newHarness(testingCase)
		harnessUnderTest.device.scriptCaptures(pageStep)
		start := harnessUnderTest.startSession()
		root := currentTree(testingCase, harnessUnderTest.engine).Root
		outerNode := tapPublicNode(root, protocol.RoleScroll, screenRect())
		containerANode := tapPublicNode(root, protocol.RoleScroll, containerABounds)
		containerBNode := tapPublicNode(root, protocol.RoleScroll, containerBBounds)
		if outerNode == nil || containerANode == nil || containerBNode == nil {
			testingCase.Fatal("competing scroll fixture nodes were not published")
		}

		result := harnessUnderTest.actP(testingCase, "", start.Observation.ID,
			protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{X: coordX, Y: coordY},
				Direction: protocol.ScrollDirectionDown,
			})
		details := proposalRequireStaticReason(testingCase, result,
			protocol.CodeAmbiguousPoint, "competing_scroll_containers")
		proposalRequirePointScope(testingCase, details)
		candidates := proposalCandidates(testingCase, details)
		proposalRequireCandidateRefs(testingCase, candidates,
			containerANode.Ref, containerBNode.Ref, outerNode.Ref)
		proposalRequireCandidateRelation(testingCase, candidates,
			containerANode.Ref, "competing")
		proposalRequireCandidateRelation(testingCase, candidates,
			containerBNode.Ref, "competing")
		proposalRequireCandidateRelation(testingCase, candidates,
			outerNode.Ref, "ancestor")
		proposalRequireCandidateExecutable(testingCase, candidates,
			containerANode.Ref)
		proposalRequireCandidateExecutable(testingCase, candidates,
			containerBNode.Ref)
		proposalRequireCandidateExecutable(testingCase, candidates,
			outerNode.Ref)
		proposalRequireNoDispatch(testingCase, harnessUnderTest)
	})
}
