package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// These tests cover the text submit_ref static planner and the write-after
// rebind maths. They never dispatch an input and never depend on the Act main
// entry.

func submitTestDisplay() device.Display {
	return device.Display{
		ID: 12, Width: 1080, Height: 2400, Density: 420,
		Generation: "submit-test-generation",
	}
}

func submitTestField(display device.Display, index int, bounds device.Rect,
	text, desc, res string, focused bool) device.RawNode {
	return revalEditText(index, 0, 1, display.ID, bounds, text, desc, res, focused)
}

func submitTestCapture(display device.Display, children []int,
	nodes ...device.RawNode) device.Capture {
	all := make([]device.RawNode, 0, len(nodes)+1)
	all = append(all, revalRoot(display.ID, children...))
	all = append(all, nodes...)
	return revalCapture(display, all...)
}

func submitTestObservation(t *testing.T, page func(device.Display) device.Capture) (
	*harness, *observationState) {
	t.Helper()
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(page))
	h.startSession()
	h.engine.mu.Lock()
	obs := h.engine.current
	h.engine.mu.Unlock()
	if obs == nil || obs.Projection == nil || obs.Projection.Tree == nil {
		t.Fatal("start produced no current observation")
	}
	return h, obs
}

func submitTestPage(display device.Display, buttonEnabled bool) device.Capture {
	inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	input := submitTestField(display, 1, inputBounds, "", "", "search_box", true)
	button := proposalClickableNode(2, 0, 1, display,
		device.Rect{Left: 700, Top: 500, Right: 1000, Bottom: 600},
		"android.widget.Button", "Search")
	button.HasEnabled = true
	button.Enabled = buttonEnabled
	return submitTestCapture(display, []int{1, 2}, input, button)
}

func submitTestTextOperation(inputRef, submitRef string) *protocol.Operation {
	return &protocol.Operation{
		Type:          "text",
		Target:        &protocol.Target{UseRef: true, Ref: inputRef},
		Text:          "hello",
		AutoSubmit:    true,
		HasAutoSubmit: true,
		SubmitRef:     submitRef,
		HasSubmitRef:  submitRef != "",
	}
}

func submitTestNodeByLabel(t *testing.T, root *protocol.Node, label string) *protocol.Node {
	t.Helper()
	return proposalFindLabel(t, root, label)
}

// TestPlanTextSubmitRefDisabledButtonIsPlanned proves the submit-only
// relaxation: a write-time disabled submit target still gets a complete direct
// tap plan and records initiallyDisabled.
func TestPlanTextSubmitRefDisabledButtonIsPlanned(t *testing.T) {
	h, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		return submitTestPage(display, false)
	})
	root := currentTree(t, h.engine).Root
	input := tapPublicBoundsNode(root, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400})
	button := submitTestNodeByLabel(t, root, "Search")

	plan, apiErr := planTextSubmitRef(obs, submitTestTextOperation(input.Ref, button.Ref))
	if apiErr != nil || plan == nil || plan.tap == nil {
		t.Fatalf("submit plan = %+v error = %+v", plan, apiErr)
	}
	if plan.ref != button.Ref {
		t.Fatalf("plan ref = %q, want %q", plan.ref, button.Ref)
	}
	if !plan.initiallyDisabled {
		t.Fatal("disabled submit button was not recorded as initially disabled")
	}
	if plan.tap.strategy != tapModeDefaultCenter {
		t.Fatalf("strategy = %q, want direct default_center", plan.tap.strategy)
	}
	execution := tapPlanExecutionRaw(plan.tap)
	if execution == nil || execution.HasEnabled == false || execution.Enabled {
		t.Fatalf("execution target enabled state = %+v", execution)
	}
}

// TestPlanTextSubmitRefEnabledButtonIsNotInitiallyDisabled is the positive
// control: the same planner still works when the button is enabled.
func TestPlanTextSubmitRefEnabledButtonIsNotInitiallyDisabled(t *testing.T) {
	h, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		return submitTestPage(display, true)
	})
	root := currentTree(t, h.engine).Root
	input := tapPublicBoundsNode(root, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400})
	button := submitTestNodeByLabel(t, root, "Search")
	plan, apiErr := planTextSubmitRef(obs, submitTestTextOperation(input.Ref, button.Ref))
	if apiErr != nil || plan == nil {
		t.Fatalf("submit plan = %+v error = %+v", plan, apiErr)
	}
	if plan.initiallyDisabled {
		t.Fatal("enabled submit button was recorded as initially disabled")
	}
}

// TestStaticTapRefCheckStillRejectsDisabledTarget proves the normal tap branch
// keeps its 409 node_disabled gate; only the submit planner relaxes it.
func TestStaticTapRefCheckStillRejectsDisabledTarget(t *testing.T) {
	h, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		return submitTestPage(display, false)
	})
	button := submitTestNodeByLabel(t, currentTree(t, h.engine).Root, "Search")
	_, _, _, apiErr := staticTapRefCheck(obs, &protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
	})
	if apiErr == nil || apiErr.Code != protocol.CodeNodeDisabled || apiErr.HTTPStatus != 409 {
		t.Fatalf("disabled normal tap error = %+v, want 409 node_disabled", apiErr)
	}
}

// TestPlanTextSubmitRefMissingNodeRejectsBeforeClaim proves the static lookup
// keeps node_not_found and performs no submission planning for enter_key or an
// effectively disabled switch.
func TestPlanTextSubmitRefMissingNodeRejectsBeforeClaim(t *testing.T) {
	h, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		return submitTestPage(display, false)
	})
	root := currentTree(t, h.engine).Root
	input := tapPublicBoundsNode(root, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400})
	plan, apiErr := planTextSubmitRef(obs, submitTestTextOperation(input.Ref, "n999"))
	if apiErr == nil || apiErr.Code != protocol.CodeNodeNotFound || apiErr.HTTPStatus != 409 {
		t.Fatalf("missing submit_ref error = %+v, want 409 node_not_found", apiErr)
	}
	if plan != nil {
		t.Fatalf("missing submit_ref planned %+v", plan)
	}
	if status := observationStatus(h, obs.ID); status != ObservationReady {
		t.Fatalf("static rejection changed observation status to %q", status)
	}
}

// TestPlanTextSubmitRefNoPlanForEnterKeyOrDisabledSwitch proves the two
// non-tap submission shapes never plan a node lookup.
func TestPlanTextSubmitRefNoPlanForEnterKeyOrDisabledSwitch(t *testing.T) {
	_, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		return submitTestPage(display, false)
	})
	input := findPublic(obs.Projection.Tree.Root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleInput
	})
	if input == nil {
		t.Fatal("input node not found")
	}
	enterKey := submitTestTextOperation(input.Ref, "")
	if plan, apiErr := planTextSubmitRef(obs, enterKey); apiErr != nil || plan != nil {
		t.Fatalf("enter_key planned %+v error %+v", plan, apiErr)
	}
	disabled := submitTestTextOperation(input.Ref, "n999")
	disabled.AutoSubmit, disabled.HasAutoSubmit = false, true
	if plan, apiErr := planTextSubmitRef(obs, disabled); apiErr != nil || plan != nil {
		t.Fatalf("auto_submit=false planned %+v error %+v", plan, apiErr)
	}
}

// TestPlanTextSubmitRefBubbleAncestorDisabled proves the disabled gate of the
// bubble ancestor step only relaxes for submit: the label itself is not
// interactive, so the one nearest interactive ancestor is planned.
func TestPlanTextSubmitRefBubbleAncestorDisabled(t *testing.T) {
	_, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		container := proposalClickableNode(1, 0, 1, display,
			device.Rect{Left: 100, Top: 500, Right: 1000, Bottom: 700},
			"android.widget.FrameLayout", "")
		container.HasEnabled, container.Enabled = true, false
		label := proposalNode(2, 1, 2, display,
			device.Rect{Left: 700, Top: 550, Right: 950, Bottom: 650},
			"android.widget.TextView", "Submit now")
		container.Children = []int{2}
		input := submitTestField(display, 3,
			device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "", "search_box", true)
		return submitTestCapture(display, []int{1, 3}, container, label, input)
	})
	root := obs.Projection.Tree.Root
	input := findPublic(root, func(node *protocol.Node) bool { return node.Role == protocol.RoleInput })
	label := proposalFindLabel(t, root, "Submit now")
	plan, apiErr := planTextSubmitRef(obs, submitTestTextOperation(input.Ref, label.Ref))
	if apiErr != nil || plan == nil || plan.tap == nil {
		t.Fatalf("bubble submit plan = %+v error = %+v", plan, apiErr)
	}
	if plan.tap.strategy != tapModeBubbleAncestor {
		t.Fatalf("strategy = %q, want %q", plan.tap.strategy, tapModeBubbleAncestor)
	}
	if plan.tap.bubbleAncestorRef == "" {
		t.Fatal("bubble plan has no ancestor ref")
	}
	if !plan.initiallyDisabled {
		t.Fatal("disabled bubble ancestor was not recorded as initially disabled")
	}
}

// TestPlanTextSubmitRefCoextensiveChildDisabled covers the coextensive-child
// delegation with a disabled execution target.
func TestPlanTextSubmitRefCoextensiveChildDisabled(t *testing.T) {
	shellBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 800}
	_, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		shell := proposalClickableNode(1, 0, 1, display, shellBounds,
			"android.widget.FrameLayout", "shell")
		shell.HasEnabled, shell.Enabled = true, false
		delegate := proposalClickableNode(2, 1, 2, display, shellBounds,
			"android.widget.Button", "delegate")
		delegate.HasEnabled, delegate.Enabled = true, false
		shell.Children = []int{2}
		input := submitTestField(display, 3,
			device.Rect{Left: 100, Top: 1000, Right: 900, Bottom: 1100}, "", "", "search_box", true)
		return submitTestCapture(display, []int{1, 3}, shell, delegate, input)
	})
	root := obs.Projection.Tree.Root
	input := findPublic(root, func(node *protocol.Node) bool { return node.Role == protocol.RoleInput })
	shell := proposalFindLabel(t, root, "shell")
	plan, apiErr := planTextSubmitRef(obs, submitTestTextOperation(input.Ref, shell.Ref))
	if apiErr != nil || plan == nil || plan.tap == nil {
		t.Fatalf("coextensive submit plan = %+v error = %+v", plan, apiErr)
	}
	if plan.tap.strategy != tapModeCoextensiveChild {
		t.Fatalf("strategy = %q, want %q", plan.tap.strategy, tapModeCoextensiveChild)
	}
	if plan.tap.delegateRaw == nil || !plan.initiallyDisabled {
		t.Fatalf("coextensive plan delegate = %+v initiallyDisabled=%t",
			plan.tap.delegateRaw, plan.initiallyDisabled)
	}
}

// TestPlanTextSubmitRefColocatedDisabled covers step B's disabled candidate
// relaxation using the sibling label/overlapping button shape.
func TestPlanTextSubmitRefColocatedDisabled(t *testing.T) {
	labelBounds := device.Rect{Left: 925, Top: 1690, Right: 1018, Bottom: 1750}
	commentBounds := device.Rect{Left: 915, Top: 1675, Right: 1028, Bottom: 1787}
	_, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		container := proposalClickableNode(1, 0, 1, display,
			device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2200},
			"android.widget.FrameLayout", "")
		comment := proposalClickableNode(2, 1, 2, display, commentBounds,
			"android.widget.ImageButton", "comment")
		comment.HasEnabled, comment.Enabled = true, false
		label := proposalNode(3, 1, 2, display, labelBounds,
			"android.widget.TextView", "999+")
		container.Children = []int{2, 3}
		input := submitTestField(display, 4,
			device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "", "search_box", true)
		return submitTestCapture(display, []int{1, 4}, container, comment, label, input)
	})
	root := obs.Projection.Tree.Root
	input := findPublic(root, func(node *protocol.Node) bool { return node.Role == protocol.RoleInput })
	label := proposalFindLabel(t, root, "999+")
	plan, apiErr := planTextSubmitRef(obs, submitTestTextOperation(input.Ref, label.Ref))
	if apiErr != nil || plan == nil || plan.tap == nil {
		t.Fatalf("colocated submit plan = %+v error = %+v", plan, apiErr)
	}
	if plan.tap.strategy != tapModeBubbleColocated {
		t.Fatalf("strategy = %q, want %q", plan.tap.strategy, tapModeBubbleColocated)
	}
	if plan.tap.colocatedRaw == nil || !plan.initiallyDisabled {
		t.Fatalf("colocated plan = %+v initiallyDisabled=%t",
			plan.tap, plan.initiallyDisabled)
	}
}

// TestStaticTextCoordinateStillPlansSubmit proves coordinate text also reaches
// the submit planner through the unified static entry.
func TestStaticTextCoordinateStillPlansSubmit(t *testing.T) {
	h, obs := submitTestObservation(t, func(display device.Display) device.Capture {
		return submitTestPage(display, false)
	})
	_ = h
	root := obs.Projection.Tree.Root
	button := proposalFindLabel(t, root, "Search")
	operation := &protocol.Operation{
		Type:      "text",
		Target:    &protocol.Target{UseRef: false, X: 500, Y: 350},
		Text:      "hello",
		SubmitRef: button.Ref, HasSubmitRef: true,
	}
	point, scroll, tap, submit, apiErr := staticActContentCheckWithTap(obs, operation)
	if apiErr != nil || submit == nil || submit.tap == nil {
		t.Fatalf("coordinate text submit plan = %+v error = %+v", submit, apiErr)
	}
	if point == nil || scroll != nil || tap != nil {
		t.Fatalf("coordinate static result = point %+v scroll %+v tap %+v", point, scroll, tap)
	}
}

// --- write-after rebind maths ---

func TestSameVerticalBandResizeBounds(t *testing.T) {
	oldBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	cases := []struct {
		name string
		next device.Rect
		want bool
	}{
		{"inline_shrink", device.Rect{Left: 100, Top: 300, Right: 820, Bottom: 400}, true},
		{"small_vertical_shift", device.Rect{Left: 100, Top: 305, Right: 820, Bottom: 405}, true},
		{"vertical_keyboard_lift", device.Rect{Left: 100, Top: 900, Right: 900, Bottom: 1000}, false},
		{"horizontal_scroll", device.Rect{Left: 600, Top: 300, Right: 1400, Bottom: 400}, false},
		{"degenerate", device.Rect{Left: 100, Top: 300, Right: 100, Bottom: 300}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameVerticalBandResize(oldBounds, tc.next); got != tc.want {
				t.Fatalf("sameVerticalBandResize = %t, want %t", got, tc.want)
			}
		})
	}
}

func submitTestRebindFrames(t *testing.T, want device.RawNode,
	afterNodes ...device.RawNode) (device.Capture, device.Capture) {
	t.Helper()
	display := submitTestDisplay()
	before := revalCapture(display, revalRoot(display.ID, 1), want)
	children := make([]int, 0, len(afterNodes))
	for index := range afterNodes {
		afterNodes[index].Index = index + 1
		afterNodes[index].Parent = 0
		afterNodes[index].Depth = 1
		children = append(children, index+1)
	}
	all := make([]device.RawNode, 0, len(afterNodes)+1)
	all = append(all, revalRoot(display.ID, children...))
	all = append(all, afterNodes...)
	after := revalCapture(display, all...)
	return before, after
}

func TestRebindPostWriteTextTargetResourceAnchorInlineResize(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "search field", "search_box", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 820, Bottom: 400},
			"hello", "search field", "search_box", true))
	got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello")
	if !ok || got == nil || got.Text != "hello" || got.Bounds != after.Nodes[1].Bounds {
		t.Fatalf("rebind = %+v ok=%t", got, ok)
	}
}

func TestRebindPostWriteTextTargetEmptyTextIsLegal(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"draft", "", "search_box", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
			"", "", "search_box", true))
	got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "")
	if !ok || got == nil || got.TextSet == false || got.Text != "" {
		t.Fatalf("empty-text rebind = %+v ok=%t", got, ok)
	}
}

func TestRebindPostWriteTextTargetDescriptionAnchorAllowsFreshResource(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "search field", "", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 820, Bottom: 400},
			"hello", "search field", "search_box", true))
	got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello")
	if !ok || got == nil {
		t.Fatalf("description-anchor rebind = %+v ok=%t", got, ok)
	}
}

func TestRebindPostWriteTextTargetUnanchoredDuplicateIsNotDisambiguatedByGeometry(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "", "", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
			"hello", "", "", true),
		submitTestField(display, 2, device.Rect{Left: 100, Top: 1800, Right: 900, Bottom: 1900},
			"hello", "", "", true))
	if got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello"); ok {
		t.Fatalf("far duplicate was disambiguated by geometry: %+v", got)
	}
}

func TestRebindPostWriteTextTargetUnanchoredSingleButGeometryMismatch(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "", "", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 400, Top: 1800, Right: 1200, Bottom: 1900},
			"hello", "", "", true))
	if got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello"); ok {
		t.Fatalf("single far candidate passed geometry: %+v", got)
	}
}

func TestRebindPostWriteTextTargetDuplicateAnchorRejected(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "", "search_box", true)
	before := revalCapture(display, revalRoot(display.ID, 1, 2), want,
		submitTestField(display, 2, device.Rect{Left: 100, Top: 500, Right: 900, Bottom: 600},
			"", "", "search_box", false))
	after := revalCapture(display, revalRoot(display.ID, 1),
		submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 820, Bottom: 400},
			"hello", "", "search_box", true))
	if got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello"); ok {
		t.Fatalf("duplicate pre-write anchor rebound: %+v", got)
	}
}

func TestRebindPostWriteTextTargetOffscreenCandidateRejected(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "", "", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 100, Top: -200, Right: 900, Bottom: -100},
			"hello", "", "", true))
	if got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello"); ok {
		t.Fatalf("offscreen candidate rebound: %+v", got)
	}
}

func TestRebindPostWriteTextTargetRequiresFocusedCandidate(t *testing.T) {
	display := submitTestDisplay()
	want := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"", "", "search_box", true)
	before, after := submitTestRebindFrames(t, want,
		submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
			"hello", "", "search_box", false))
	if got, ok := rebindPostWriteTextTarget(&before, &before.Nodes[1], &after, "hello"); ok {
		t.Fatalf("unfocused candidate rebound: %+v", got)
	}
}

func TestTextWriteAnchorCountDescriptionIgnoresResource(t *testing.T) {
	display := submitTestDisplay()
	node := submitTestField(display, 1, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400},
		"hello", "search field", "search_box", true)
	capture := revalCapture(display, revalRoot(display.ID, 1), node)
	if got := textWriteAnchorCount(&capture, &capture.Nodes[1], "description",
		optionalString{set: true, value: "search field"}); got != 1 {
		t.Fatalf("description count with resource = %d, want 1", got)
	}
	if got := textWriteAnchorCount(&capture, &capture.Nodes[1], "resource",
		optionalString{set: true, value: "search_box"}); got != 1 {
		t.Fatalf("resource count = %d, want 1", got)
	}
}
