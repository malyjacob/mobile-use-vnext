package engine

import (
	"slices"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const textRefNoTapValue = "hello"

func textRefNoTapPage(bounds, cover device.Rect, clickableAncestor, focused, editable bool) captureStep {
	return proposalCaptureStep(func(display device.Display) device.Capture {
		root := revalRoot(display.ID, 1)
		input := revalEditText(1, 0, 1, display.ID, bounds, "", "comment box", "", focused)
		input.HasClickable, input.Clickable = false, false
		input.Editable = editable
		if clickableAncestor {
			ancestor := proposalClickableNode(1, 0, 1, display, screenRect(),
				"android.widget.FrameLayout", "ancestor")
			input.Index, input.Parent, input.Depth = 2, 1, 2
			ancestor.Children = []int{input.Index}
			return revalCapture(display, root, ancestor, input)
		}
		if !cover.Empty() {
			button := revalButton(2, 1, 2, display.ID, cover, "cover", "", "")
			input.Children = []int{button.Index}
			return revalCapture(display, root, input, button)
		}
		return revalCapture(display, root, input)
	})
}

func newTextRefNoTapHarness(t *testing.T, initial captureStep) (*harness, *textEvDevice, string, string) {
	t.Helper()
	h, rec := newRevalHarness(t)
	dev := &textEvDevice{revalDevice: rec}
	h.engine.dev = dev
	h.device.scriptCaptures(initial)
	start := h.startSession()
	input := findRole(currentTree(t, h.engine).Root, protocol.RoleInput)
	if input == nil || !slices.Equal(input.Actions, []protocol.Action{protocol.ActionText}) {
		t.Fatalf("fixture input must expose only ActionText, got %+v", input)
	}
	return h, dev, start.Observation.ID, input.Ref
}

func requireTextRefNoTapDispatch(t *testing.T, dev *textEvDevice, wantTap, wantSet, wantKey int) {
	t.Helper()
	tap, set, _, key := dev.counts()
	if tap != wantTap || set != wantSet || key != wantKey {
		t.Fatalf("dispatch tap/set/key = %d/%d/%d, want %d/%d/%d", tap, set, key, wantTap, wantSet, wantKey)
	}
}

func requireTextRefNoTapExecuted(t *testing.T, result *protocol.Result, submitted bool) {
	t.Helper()
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("want executed, got %q error=%+v HTTP=%d", result.Outcome, result.Error, result.HTTPStatus)
	}
	if submitted {
		requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	} else if result.TextInput != nil {
		t.Fatalf("auto_submit=false returned text_input = %+v, want absent", result.TextInput)
	}
}

func TestTextRefNoTapUnfocusedWrites(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	initial := textRefNoTapPage(bounds, device.Rect{}, false, false, true)
	focused := textRefNoTapPage(bounds, device.Rect{}, false, true, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
	h.device.scriptCaptures(initial, focused)

	autoSubmit := false
	result := h.actWith(t, "PnoTapUnfocused", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
	requireTextRefNoTapExecuted(t, result, false)
	requireTextRefNoTapDispatch(t, dev, 1, 1, 0)
}

func TestTextRefNoTapFocusedWrites(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	focused := textRefNoTapPage(bounds, device.Rect{}, false, true, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, focused)

	autoSubmit := false
	result := h.actWith(t, "PnoTapFocused", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
	requireTextRefNoTapExecuted(t, result, false)
	requireTextRefNoTapDispatch(t, dev, 0, 1, 0)
}

func TestTextRefNoTapAutoSubmitSendsOneEnter(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	initial := textRefNoTapPage(bounds, device.Rect{}, false, false, true)
	focused := textRefNoTapPage(bounds, device.Rect{}, false, true, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
	h.device.scriptCaptures(initial, focused)

	result := h.actWith(t, "PnoTapEnter", textFlowRequest(obsRef, ref, textRefNoTapValue, nil))
	requireTextRefNoTapExecuted(t, result, true)
	requireTextRefNoTapDispatch(t, dev, 1, 1, 1)
	h.device.mu.Lock()
	defer h.device.mu.Unlock()
	if !slices.Equal(h.device.keys, []string{"ENTER"}) {
		t.Fatalf("keys = %v, want [ENTER]", h.device.keys)
	}
}

func TestTextRefNoTapFullyCoveredReportsBlockingButton(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	initial := textRefNoTapPage(bounds, bounds, false, false, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
	button := proposalFindLabel(t, currentTree(t, h.engine).Root, "cover")

	autoSubmit := false
	result := h.actWith(t, "PnoTapCovered", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
	details := proposalRequireStaticReason(t, result, protocol.CodeNoSafePoint, "blocked")
	requireDetailStringSlice(t, details, "blocking_refs", button.Ref)
	if _, present := details["candidates"]; present {
		candidates := proposalCandidates(t, details)
		if slices.Contains(proposalCandidateRefs(candidates), button.Ref) {
			t.Fatalf("blocking button %s was offered as a text candidate: %v", button.Ref, candidates)
		}
	}
	requireReadyObservation(t, h, obsRef)
	requireTextRefNoTapDispatch(t, dev, 0, 0, 0)
}

func TestTextRefNoTapPartialGapFocusesInsideTarget(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 500}
	cover := device.Rect{Left: 100, Top: 300, Right: 650, Bottom: 500}
	initial := textRefNoTapPage(bounds, cover, false, false, true)
	focused := textRefNoTapPage(bounds, cover, false, true, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
	h.device.scriptCaptures(initial, focused)

	autoSubmit := false
	result := h.actWith(t, "PnoTapPartialGap", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
	requireTextRefNoTapExecuted(t, result, false)
	requireTextRefNoTapDispatch(t, dev, 1, 1, 0)
	x, y := dev.taps[0][1], dev.taps[0][2]
	if x < cover.Right || x >= bounds.Right || y < bounds.Top || y >= bounds.Bottom {
		t.Fatalf("focus point (%d,%d) must be in the input's uncovered strip, input=%+v cover=%+v",
			x, y, bounds, cover)
	}
}

func TestTextRefNoTapLostEditableAfterClaim(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	initial := textRefNoTapPage(bounds, device.Rect{}, false, false, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
	h.device.scriptCaptures(textRefNoTapPage(bounds, device.Rect{}, false, false, false))

	autoSubmit := false
	result := h.actWith(t, "PnoTapLostEditable", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	status := result.HTTPStatus
	if status == 0 {
		status = protocol.Status(result.Error.Code)
	}
	if status != 409 {
		t.Fatalf("target change HTTP status = %d, want 409", status)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "reason", "target_unconfirmed")
	requireTextDispatch(t, details, "not_attempted", "not_attempted", "not_attempted")
	if result.Observation == nil || result.Observation.ID == obsRef {
		t.Fatalf("expected a fresh observation after claim, got %+v", result.Observation)
	}
	requireTextRefNoTapDispatch(t, dev, 0, 0, 0)
}

func TestTextRefNoTapDoesNotBubbleToClickableAncestor(t *testing.T) {
	// Deliberately place the input far from the ancestor's center.
	bounds := device.Rect{Left: 100, Top: 300, Right: 350, Bottom: 400}
	initial := textRefNoTapPage(bounds, device.Rect{}, true, false, true)
	focused := textRefNoTapPage(bounds, device.Rect{}, true, true, true)
	h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
	h.device.scriptCaptures(initial, focused)
	ancestor := proposalFindLabel(t, currentTree(t, h.engine).Root, "ancestor")
	if !hasAction(ancestor, protocol.ActionTap) {
		t.Fatalf("fixture ancestor must expose ActionTap, got %+v", ancestor)
	}

	autoSubmit := false
	result := h.actWith(t, "PnoTapNoBubble", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
	requireTextRefNoTapExecuted(t, result, false)
	requireTextRefNoTapDispatch(t, dev, 1, 1, 0)
	x, y := dev.taps[0][1], dev.taps[0][2]
	if x < bounds.Left || x >= bounds.Right || y < bounds.Top || y >= bounds.Bottom {
		t.Fatalf("focus point (%d,%d) escaped input bounds %+v toward clickable ancestor", x, y, bounds)
	}
}
