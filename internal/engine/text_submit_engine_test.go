package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Engine-level submission tests. They script the write/focus/submit primitives
// and the captures and assert the exact physical dispatch counts, so a replay
// or a second submission primitive fails the test.

func submitTextActRequest(obsRef, ref, text string, autoSubmit *bool,
	submitRef string) *protocol.ActRequest {
	req := &protocol.ActRequest{
		Reference: obsRef,
		Operation: protocol.Operation{
			Type:   "text",
			Target: &protocol.Target{UseRef: true, Ref: ref},
			Text:   text,
		},
	}
	if autoSubmit != nil {
		req.Operation.AutoSubmit, req.Operation.HasAutoSubmit = *autoSubmit, true
	}
	if submitRef != "" {
		req.Operation.SubmitRef, req.Operation.HasSubmitRef = submitRef, true
	}
	return req
}

func submitTextInputBounds() device.Rect {
	return device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
}

func submitTextButtonBounds() device.Rect {
	return device.Rect{Left: 700, Top: 500, Right: 1000, Bottom: 600}
}

func submitTextPageStep(bounds device.Rect, text string, focused bool,
	buttonEnabled bool) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		input := revalEditText(1, 0, 1, display.ID, bounds, text,
			"comment_box", "", focused)
		button := revalButton(2, 0, 1, display.ID, submitTextButtonBounds(),
			"Search", "", "")
		button.HasEnabled, button.Enabled = true, buttonEnabled
		return revalCapture(display, revalRoot(display.ID, 1, 2), input, button), nil
	}
}

// submitTextMissingEnabledPageStep is the post-write frame where the App no
// longer reports the enabled flag at all. The submit path must not treat a
// missing flag as the required disabled -> enabled transition.
func submitTextMissingEnabledPageStep(bounds device.Rect, text string) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		input := revalEditText(1, 0, 1, display.ID, bounds, text,
			"comment_box", "", true)
		button := revalButton(2, 0, 1, display.ID, submitTextButtonBounds(),
			"Search", "", "")
		button.HasEnabled, button.Enabled = false, false
		return revalCapture(display, revalRoot(display.ID, 1, 2), input, button), nil
	}
}

func submitTextDuplicateStep(bounds device.Rect, text string) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		first := revalEditText(1, 0, 1, display.ID, bounds, text,
			"comment_box", "", true)
		second := revalEditText(2, 0, 1, display.ID,
			device.Rect{Left: 100, Top: 600, Right: 900, Bottom: 700},
			text, "comment_box", "", true)
		return revalCapture(display, revalRoot(display.ID, 1, 2), first, second), nil
	}
}

func submitTextPageWithForegroundStep(bounds device.Rect, text string,
	packageName string) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		page, err := submitTextPageStep(bounds, text, true, true)(nil, display)
		if err != nil {
			return device.Capture{}, err
		}
		page.Foreground = revalForeground(packageName, 42)
		return page, nil
	}
}

func newSubmitTextHarness(t *testing.T, initial captureStep) (
	*harness, *textEvDevice, string, string, *protocol.Node) {
	t.Helper()
	h, rec := newRevalHarness(t)
	dev := &textEvDevice{revalDevice: rec}
	h.engine.dev = dev
	h.device.scriptCaptures(initial)
	start := h.startSession()
	root := currentTree(t, h.engine).Root
	input := findPublic(root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleInput
	})
	if input == nil {
		t.Fatal("input node not found")
	}
	return h, dev, start.Observation.ID, input.Ref, root
}

func requireSubmitTextInput(t *testing.T, result *protocol.Result,
	write, submit string) {
	t.Helper()
	if result == nil || result.TextInput == nil {
		t.Fatalf("result text_input is absent: %+v", result)
	}
	if result.TextInput.Write != write || result.TextInput.Submit != submit {
		t.Fatalf("text_input = %+v, want write=%s submit=%s",
			result.TextInput, write, submit)
	}
}

func requireSubmitReason(t *testing.T, result *protocol.Result,
	code, reason string) map[string]any {
	t.Helper()
	if result == nil || result.Error == nil {
		t.Fatalf("result has no error: %+v", result)
	}
	if result.Error.Code != code {
		t.Fatalf("error code = %q, want %q (%+v)", result.Error.Code, code, result.Error)
	}
	details := result.Error.Details
	if details == nil || details["reason"] != reason {
		t.Fatalf("error reason = %v, want %q (%+v)", details["reason"], reason, details)
	}
	return details
}

// TestTextEnterRebindOnceAndSendOneEnter is the #30 primary path: the exact
// read on the old bounds returns zero candidates, one post-write capture rebinds
// the horizontally narrowed field by its unique anchor, the second read
// confirms, and exactly one ENTER is sent.
func TestTextEnterRebindOnceAndSendOneEnter(t *testing.T) {
	bounds := submitTextInputBounds()
	narrowed := device.Rect{Left: 100, Top: 300, Right: 820, Bottom: 400}
	h, dev, obsRef, ref, _ := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, true))
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, true),
		submitTextPageStep(narrowed, "hello", true, true),
		submitTextPageStep(narrowed, "hello", true, true),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	dev.getSteps = []textGetStep{
		{result: device.TextResult{CandidateCount: 0, Receipt: device.DispatchTerminated}},
		{result: exactText("hello")},
	}

	result := h.actWith(t, "Penterrebind",
		submitTextActRequest(obsRef, ref, "hello", nil, ""))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %q error = %+v, want executed", result.Outcome, result.Error)
	}
	requireSubmitTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 1 || get != 2 || key != 1 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 0/1/2/1",
			tap, set, get, key)
	}
}

// TestTextEnterRebindAmbiguousAnchorStopsPartial proves a non-unique anchor
// never taps or sends ENTER: the confirmed write is a partial submission.
func TestTextEnterRebindAmbiguousAnchorStopsPartial(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, _ := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, true))
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, true),
		submitTextDuplicateStep(bounds, "hello"),
		submitTextDuplicateStep(bounds, "hello"),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	dev.getSteps = []textGetStep{
		{result: device.TextResult{CandidateCount: 0, Receipt: device.DispatchTerminated}},
	}

	result := h.actWith(t, "Penterambiguous",
		submitTextActRequest(obsRef, ref, "hello", nil, ""))
	if result.Outcome != OutcomePartial {
		t.Fatalf("outcome = %q error = %+v, want partial", result.Outcome, result.Error)
	}
	requireSubmitReason(t, result, protocol.CodeTextSubmitNotExecuted,
		textReasonSubmitTargetUnconfirmed)
	requireSubmitTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputNotExecuted)
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 1 || get != 1 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 0/1/1/0",
			tap, set, get, key)
	}
}

// TestTextEnterRebindRejectsForeignDisplayGeneration proves the post-write
// frame must carry the authorized display identity before any rebind.
func TestTextEnterRebindRejectsForeignDisplayGeneration(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, _ := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, true))
	foreign := func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		wrong := display
		wrong.Generation = "other-generation"
		page, err := submitTextPageStep(bounds, "hello", true, true)(nil, wrong)
		if err != nil {
			return device.Capture{}, err
		}
		page.Display = wrong
		return page, nil
	}
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, true),
		foreign,
		submitTextPageStep(bounds, "hello", true, true),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	dev.getSteps = []textGetStep{
		{result: device.TextResult{CandidateCount: 0, Receipt: device.DispatchTerminated}},
	}

	result := h.actWith(t, "Pforeignframe",
		submitTextActRequest(obsRef, ref, "hello", nil, ""))
	if result.Outcome != OutcomePartial {
		t.Fatalf("outcome = %q error = %+v, want partial", result.Outcome, result.Error)
	}
	requireSubmitReason(t, result, protocol.CodeTextSubmitNotExecuted,
		textReasonSubmitContextUnconfirmed)
	if _, _, _, key := dev.counts(); key != 0 {
		t.Fatalf("ENTER calls = %d, want 0", key)
	}
}

// TestTextSubmitRefTapOnce proves the node submit_ref path: the fresh enabled
// frame is revalidated and exactly one Tap is sent with no ENTER.
func TestTextSubmitRefTapOnce(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, root := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, true))
	button := proposalFindLabel(t, root, "Search")
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, true),
		submitTextPageStep(bounds, "hello", true, true),
		submitTextPageStep(bounds, "hello", true, true),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}

	result := h.actWith(t, "Psubmitref",
		submitTextActRequest(obsRef, ref, "hello", nil, button.Ref))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %q error = %+v, want executed", result.Outcome, result.Error)
	}
	requireSubmitTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	tap, set, get, key := dev.counts()
	if tap != 1 || set != 1 || get != 0 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 1/1/0/0",
			tap, set, get, key)
	}
	if len(dev.taps) != 1 {
		t.Fatalf("recorded submit taps = %+v, want exactly one", dev.taps)
	}
	if !submitTextButtonBounds().Contains(dev.taps[0][1], dev.taps[0][2]) {
		t.Fatalf("submit tap = %+v, want inside %+v", dev.taps[0], submitTextButtonBounds())
	}
}

// TestTextSubmitRefMissingEnabledAfterWriteStopsPartial proves a fresh frame
// that lost the enabled flag cannot satisfy the required disabled -> enabled
// transition: it is retried once and then reported as submit_disabled.
func TestTextSubmitRefMissingEnabledAfterWriteStopsPartial(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, root := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, false))
	button := proposalFindLabel(t, root, "Search")
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, false),
		submitTextMissingEnabledPageStep(bounds, "hello"),
		submitTextMissingEnabledPageStep(bounds, "hello"),
		submitTextMissingEnabledPageStep(bounds, "hello"),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}

	result := h.actWith(t, "Pmissingenabled",
		submitTextActRequest(obsRef, ref, "hello", nil, button.Ref))
	if result.Outcome != OutcomePartial {
		t.Fatalf("outcome = %q error = %+v, want partial", result.Outcome, result.Error)
	}
	requireSubmitReason(t, result, protocol.CodeTextSubmitNotExecuted,
		textReasonSubmitDisabled)
	if tap, _, _, key := dev.counts(); tap != 0 || key != 0 {
		t.Fatalf("tap=%d key=%d, want 0/0", tap, key)
	}
}

// TestTextCoordinateWithSubmitRefDispatchesBothTaps proves the coordinate text
// target keeps its authorized focus tap and then reuses the same submit_ref
// planner and one submit tap.
func TestTextCoordinateWithSubmitRefDispatchesBothTaps(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, _, root := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", false, true))
	button := proposalFindLabel(t, root, "Search")
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", false, true),
		submitTextPageStep(bounds, "", true, true),
		submitTextPageStep(bounds, "hello", true, true),
		submitTextPageStep(bounds, "hello", true, true),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}

	request := &protocol.ActRequest{
		Reference: obsRef,
		Operation: protocol.Operation{
			Type:         "text",
			Target:       &protocol.Target{UseRef: false, X: 500, Y: 350},
			Text:         "hello",
			SubmitRef:    button.Ref,
			HasSubmitRef: true,
		},
	}
	result := h.actWith(t, "Pcoordsubmit", request)
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %q error = %+v, want executed", result.Outcome, result.Error)
	}
	requireSubmitTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	tap, set, get, key := dev.counts()
	if tap != 2 || set != 1 || get != 0 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 2/1/0/0",
			tap, set, get, key)
	}
	if len(dev.taps) != 2 {
		t.Fatalf("recorded taps = %+v, want focus then submit", dev.taps)
	}
	if dev.taps[0][1] != 500 || dev.taps[0][2] != 350 {
		t.Fatalf("focus tap = %+v, want authorized point (500,350)", dev.taps[0])
	}
	if !submitTextButtonBounds().Contains(dev.taps[1][1], dev.taps[1][2]) {
		t.Fatalf("submit tap = %+v, want inside %+v", dev.taps[1], submitTextButtonBounds())
	}
}

// TestTextSubmitRefStillDisabledRetriesOnceThenPartial proves a still-disabled
// execution target is retried at most once and then reported as submit_disabled.
func TestTextSubmitRefStillDisabledRetriesOnceThenPartial(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, root := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, false))
	button := proposalFindLabel(t, root, "Search")
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, false),
		submitTextPageStep(bounds, "hello", true, false),
		submitTextPageStep(bounds, "hello", true, false),
		submitTextPageStep(bounds, "hello", true, false),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}

	result := h.actWith(t, "Pstilldisabled",
		submitTextActRequest(obsRef, ref, "hello", nil, button.Ref))
	if result.Outcome != OutcomePartial {
		t.Fatalf("outcome = %q error = %+v, want partial", result.Outcome, result.Error)
	}
	requireSubmitReason(t, result, protocol.CodeTextSubmitNotExecuted,
		textReasonSubmitDisabled)
	tap, set, _, key := dev.counts()
	if tap != 0 || set != 1 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d key=%d, want 0/1/0", tap, set, key)
	}
}

// TestTextSubmitRefEnabledAfterRetry proves the single 150ms re-capture can
// pick up the App's asynchronous enable and then taps once.
func TestTextSubmitRefEnabledAfterRetry(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, root := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, false))
	button := proposalFindLabel(t, root, "Search")
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, false),
		submitTextPageStep(bounds, "hello", true, false),
		submitTextPageStep(bounds, "hello", true, true),
		submitTextPageStep(bounds, "hello", true, true),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}

	result := h.actWith(t, "Penabledretry",
		submitTextActRequest(obsRef, ref, "hello", nil, button.Ref))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %q error = %+v, want executed", result.Outcome, result.Error)
	}
	requireSubmitTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	if tap, _, _, key := dev.counts(); tap != 1 || key != 0 {
		t.Fatalf("tap=%d key=%d, want 1/0", tap, key)
	}
}

// TestTextSubmitRefContextSwitchStopsPartial proves a foreground/task switch
// after the write stops before any submit tap.
func TestTextSubmitRefContextSwitchStopsPartial(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, root := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, true))
	button := proposalFindLabel(t, root, "Search")
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, true),
		submitTextPageWithForegroundStep(bounds, "hello", "com.other"),
		submitTextPageWithForegroundStep(bounds, "hello", "com.other"),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}

	result := h.actWith(t, "Pcontextswitch",
		submitTextActRequest(obsRef, ref, "hello", nil, button.Ref))
	if result.Outcome != OutcomePartial {
		t.Fatalf("outcome = %q error = %+v, want partial", result.Outcome, result.Error)
	}
	requireSubmitReason(t, result, protocol.CodeTextSubmitNotExecuted,
		textReasonSubmitContextUnconfirmed)
	if tap, _, _, key := dev.counts(); tap != 0 || key != 0 {
		t.Fatalf("tap=%d key=%d, want 0/0", tap, key)
	}
}

// TestTextAutoSubmitFalseIgnoresSubmitRef proves the lenient switch: an
// auto_submit=false request that carries a syntactically valid node ref writes
// and returns without any plan, lookup, focus or ENTER.
func TestTextAutoSubmitFalseIgnoresSubmitRef(t *testing.T) {
	bounds := submitTextInputBounds()
	h, dev, obsRef, ref, _ := newSubmitTextHarness(t,
		submitTextPageStep(bounds, "", true, true))
	h.device.scriptCaptures(
		submitTextPageStep(bounds, "", true, true),
		submitTextPageStep(bounds, "hello", true, true),
	)
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	falseValue := false

	result := h.actWith(t, "Pnosubmit",
		submitTextActRequest(obsRef, ref, "hello", &falseValue, "n999"))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %q error = %+v, want executed", result.Outcome, result.Error)
	}
	if result.TextInput != nil {
		t.Fatalf("auto_submit=false returned text_input: %+v", result.TextInput)
	}
	if tap, set, get, key := dev.counts(); tap != 0 || set != 1 || get != 0 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 0/1/0/0",
			tap, set, get, key)
	}
}
