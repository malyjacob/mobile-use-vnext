package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Contract 35 §4.2: only the no_interactive_ancestor static rejection carries
// the objective screen-clipped center of the untouched target. The field states
// geometry alone, so every other static reason must omit it and a target with no
// positive clipped area must omit it rather than write null or [0, 0].

func requireDetailIntSlice(t *testing.T, details map[string]any, key string, want ...int) {
	t.Helper()
	got, exists := details[key]
	if !exists {
		t.Fatalf("details lack %q: %#v", key, details)
	}
	values, ok := got.([]int)
	if !ok {
		t.Fatalf("%s = %#v (%T), want []int", key, got, got)
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

// readCompactNodeByRef finds one definition of a compact full tree by ref. A
// chain definition folds consecutive refs into one node, so both forms are
// matched.
func readCompactNodeByRef(node *protocol.ReadNode, ref string) *protocol.ReadNode {
	if node == nil || ref == "" {
		return nil
	}
	if node.Ref == ref {
		return node
	}
	for _, folded := range node.Chain {
		if folded == ref {
			return node
		}
	}
	for _, child := range node.Children {
		if found := readCompactNodeByRef(child, ref); found != nil {
			return found
		}
	}
	return nil
}

// TestActTapNoInteractiveAncestorAttachesClippedTargetCenter is the contract's
// red case: a passive text leaf whose compact wire geometry is hidden and which
// has no interactive ancestor. The rejection states the objective clipped center
// (990, 2350) of the delivered rectangle, not the raw center (1050, 2450) that
// lies outside the 1080x2400 screen, and the observation stays ready because a
// static rejection never claims O.
func TestActTapNoInteractiveAncestorAttachesClippedTargetCenter(t *testing.T) {
	sourceBounds := device.Rect{Left: 900, Top: 2300, Right: 1200, Bottom: 2600}
	clippedBounds := device.Rect{Left: 900, Top: 2300, Right: 1080, Bottom: 2400}
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return bubbleRootOnly(display, sourceBounds), nil
	})
	start := h.startSession()
	leaf := tapPublicBoundsNode(currentTree(t, h.engine).Root, clippedBounds)
	if leaf == nil {
		t.Fatal("clipped passive text leaf not found in the public tree")
	}
	if start.Observation.Tree == nil || start.Observation.Tree.Full == nil {
		t.Fatalf("start observation carries no full tree: %+v", start.Observation)
	}
	wire := readCompactNodeByRef(start.Observation.Tree.Full.Root, leaf.Ref)
	if wire == nil || wire.Bounds.Kind != protocol.GeometryHidden {
		t.Fatalf("compact geometry for %q = %+v, want hidden", leaf.Ref, wire)
	}
	probes, captures, taps := h.device.probeCount(), h.device.captureCount(), len(h.device.taps)

	result := h.actWith(t, "Ptargetcenter", bubbleRequest(start.Observation.ID, leaf.Ref, nil))
	details := requireStaticRejection(t, result, "unsupported_node_action", "no_interactive_ancestor")
	requireDetailString(t, details, "scope", "target")
	requireDetailString(t, details, "scope_ref", leaf.Ref)
	requireDetailStringSlice(t, details, "target_actions")
	requireDetailAbsent(t, details, "ancestor_ref")
	requireDetailIntSlice(t, details, "target_center", 990, 2350)
	requireNoLegacyTapDetailKeys(t, details)
	requireReadyObservation(t, h, start.Observation.ID)
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("static rejection probed the display %d time(s)", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("static rejection captured %d frame(s)", delta)
	}
	if delta := len(h.device.taps) - taps; delta != 0 {
		t.Fatalf("static rejection dispatched %d tap(s)", delta)
	}
}

// TestActTextSubmitRefNoInteractiveAncestorAttachesTargetCenter proves the
// text(submit_ref) reuse path: the submit target is planned through the same
// static planner, so a passive submit target with no interactive ancestor and a
// hidden compact rectangle also carries the objective clipped center. The text
// is never written and O stays ready.
func TestActTextSubmitRefNoInteractiveAncestorAttachesTargetCenter(t *testing.T) {
	inputBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	submitSourceBounds := device.Rect{Left: 100, Top: 2200, Right: 1100, Bottom: 2300}
	submitClippedBounds := device.Rect{Left: 100, Top: 2200, Right: 1080, Bottom: 2300}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		input := revalEditText(1, 0, 1, display.ID, inputBounds, "", "search_box", "", true)
		submit := proposalNode(2, 0, 1, display, submitSourceBounds,
			"android.widget.TextView", "submit")
		return revalCapture(display, revalRoot(display.ID, 1, 2), input, submit)
	})
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(pageStep)
	start := h.startSession()
	root := currentTree(t, h.engine).Root
	input := tapPublicBoundsNode(root, inputBounds)
	if input == nil {
		t.Fatal("search input not found in the public tree")
	}
	submit := proposalFindLabel(t, root, "submit")
	if submit == nil {
		t.Fatal("passive submit target not found in the public tree")
	}
	if submit.Ref == input.Ref {
		t.Fatalf("submit target resolved to the input %q", input.Ref)
	}
	if submit.Bounds == nil || rectFromBounds(submit.Bounds) != submitClippedBounds {
		t.Fatalf("submit target public bounds = %+v, want %+v", submit.Bounds, submitClippedBounds)
	}
	texts, taps := len(h.device.texts), len(h.device.taps)

	request := submitTextActRequest(start.Observation.ID, input.Ref, "hello", nil, submit.Ref)
	result := h.actWith(t, "Ptexttargetcenter", request)
	details := requireStaticRejection(t, result, "unsupported_node_action", "no_interactive_ancestor")
	requireDetailString(t, details, "scope", "target")
	requireDetailString(t, details, "scope_ref", submit.Ref)
	requireDetailIntSlice(t, details, "target_center", 590, 2250)
	requireDetailAbsent(t, details, "ancestor_ref")
	requireNoLegacyTapDetailKeys(t, details)
	requireReadyObservation(t, h, start.Observation.ID)
	if delta := len(h.device.texts) - texts; delta != 0 {
		t.Fatalf("static submit rejection wrote text %d time(s)", delta)
	}
	if delta := len(h.device.taps) - taps; delta != 0 {
		t.Fatalf("static submit rejection dispatched %d tap(s)", delta)
	}
}

// TestPlanBubbleOffscreenTargetOmitsTargetCenter is the omission control: a
// positive-area rectangle that is entirely outside the screen has no clipped
// center, so hasCenter is false and the key must be absent instead of null or
// [0, 0]. The published builder already clips public geometry, so this guard is
// pinned at the one static planner boundary that can receive such a rectangle.
func TestPlanBubbleOffscreenTargetOmitsTargetCenter(t *testing.T) {
	display := device.Display{ID: 0, Width: 1080, Height: 2400, Density: 420}
	onscreen := device.Rect{Left: 0, Top: 100, Right: 200, Bottom: 200}
	capture := buildCapture(display, []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: onscreen, Class: "android.widget.TextView", Text: "offscreen"},
	})
	projection := scrollProjection(t, capture)
	leaf := tapPublicBoundsNode(projection.Tree.Root, onscreen)
	if leaf == nil {
		t.Fatal("passive text leaf not found in the public tree")
	}
	offscreen := protocol.Bounds{Left: 2000, Top: 2500, Right: 2600, Bottom: 2700}
	leaf.Bounds = &offscreen
	obs := &observationState{Projection: projection, Screen: display, DisplayID: display.ID}
	if clipped, ok := publicNodeClippedRect(leaf, screenRectFromDisplay(display)); ok || !clipped.Empty() {
		t.Fatalf("precondition: clipped rect = %+v ok = %t, want an empty intersection",
			clipped, ok)
	}

	operation := &protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: leaf.Ref},
		Bubble: true, HasBubble: true,
	}
	_, _, _, apiErr := staticTapRefCheckWithOptions(obs, operation, tapPlanOptions{})
	if apiErr == nil || apiErr.Code != protocol.CodeNodeUnsupported ||
		apiErr.HTTPStatus != 422 || apiErr.Details["reason"] != "no_interactive_ancestor" {
		t.Fatalf("error = %+v, want 422 unsupported_node_action no_interactive_ancestor", apiErr)
	}
	requireDetailAbsent(t, apiErr.Details, "target_center")
	requireNoLegacyTapDetailKeys(t, apiErr.Details)
}
