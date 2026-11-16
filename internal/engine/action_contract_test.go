package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// bubblePage builds a root -> interactive card -> non-interactive child page.
func bubblePage(display device.Display, cardBounds, childBounds device.Rect,
	childText string, childClickable bool) device.Capture {
	return tapPageWithChildren(display, cardBounds, tapRawNode(2, 1, 2, display.ID,
		childBounds, "android.widget.TextView", childText, childClickable, false))
}

func bubbleStep(cardBounds, childBounds device.Rect, childText string,
	childClickable bool) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return bubblePage(display, cardBounds, childBounds, childText, childClickable), nil
	}
}

// bubbleRootOnly returns one capture with the root and a single child, used to
// prove a lost ancestor cannot be replaced by the child alone.
func bubbleRootOnly(display device.Display, childBounds device.Rect) device.Capture {
	child := tapRawNode(1, 0, 1, display.ID, childBounds,
		"android.widget.TextView", "post", false, false)
	return device.Capture{
		Display: display, Root: 0, CapturedAt: time.Now(),
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
		},
		Nodes: []device.RawNode{
			{
				Index: 0, Parent: -1, Children: []int{1}, Depth: 0, DisplayID: display.ID,
				Visible: true, RawBounds: screenRect(), Bounds: screenRect(),
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			},
			child,
		},
	}
}

// bubbleNestedPage inserts one fresh interactive ancestor between the card and
// the child, so the original nearest ancestor is no longer the nearest.
func bubbleNestedPage(display device.Display, cardBounds, wrapperBounds,
	childBounds device.Rect) device.Capture {
	card := tapRawNode(1, 0, 1, display.ID, cardBounds,
		"android.widget.FrameLayout", "", true, true)
	card.Children = []int{2}
	wrapper := tapRawNode(2, 1, 2, display.ID, wrapperBounds,
		"android.widget.Button", "wrapper", true, true)
	wrapper.Children = []int{3}
	child := tapRawNode(3, 2, 3, display.ID, childBounds,
		"android.widget.TextView", "post", false, false)
	return device.Capture{
		Display: display, Root: 0, CapturedAt: time.Now(),
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
		},
		Nodes: []device.RawNode{
			{
				Index: 0, Parent: -1, Children: []int{1}, Depth: 0, DisplayID: display.ID,
				Visible: true, RawBounds: screenRect(), Bounds: screenRect(),
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			},
			card, wrapper, child,
		},
	}
}

// bubbleDuplicateAncestorPage adds a second card with the same raw identity as
// the planned ancestor, making the ancestor lookup ambiguous while the child
// stays unique.
func bubbleDuplicateAncestorPage(display device.Display, cardBounds,
	childBounds device.Rect) device.Capture {
	first := tapRawNode(1, 0, 1, display.ID, cardBounds,
		"android.widget.FrameLayout", "", true, true)
	first.Children = []int{3}
	second := tapRawNode(2, 0, 1, display.ID, cardBounds,
		"android.widget.FrameLayout", "", true, true)
	child := tapRawNode(3, 1, 2, display.ID, childBounds,
		"android.widget.TextView", "post", false, false)
	return device.Capture{
		Display: display, Root: 0, CapturedAt: time.Now(),
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
		},
		Nodes: []device.RawNode{
			{
				Index: 0, Parent: -1, Children: []int{1, 2}, Depth: 0, DisplayID: display.ID,
				Visible: true, RawBounds: screenRect(), Bounds: screenRect(),
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			},
			first, second, child,
		},
	}
}

func bubbleRequest(obsRef, ref string, bubble *bool) *protocol.ActRequest {
	req := &protocol.ActRequest{
		Reference: obsRef,
		Operation: protocol.Operation{
			Type: "tap", Target: &protocol.Target{UseRef: true, Ref: ref},
		},
	}
	if bubble != nil {
		req.Operation.Bubble, req.Operation.HasBubble = *bubble, true
	}
	return req
}

// TestActBubbleDefaultsToOneInteractiveAncestor proves the omitted member is
// effective true, direct targets stay direct, and explicit false preserves the
// unsupported rejection without consuming the observation.
func TestActBubbleDefaultsToOneInteractiveAncestor(t *testing.T) {
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 800}
	childBounds := device.Rect{Left: 200, Top: 400, Right: 700, Bottom: 600}
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(bubbleStep(cardBounds, childBounds, "post", false))
	start := h.startSession()
	child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
	if child == nil {
		t.Fatal("child node not found")
	}

	result := h.actWith(t, "PbubbleDefault", bubbleRequest(start.Observation.ID, child.Ref, nil))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("bubble default outcome = %q, want executed (%+v) error=%+v", result.Outcome, result, result.Error)
	}
	if len(h.device.taps) != 1 {
		t.Fatalf("bubble default dispatched taps = %v, want exactly one", h.device.taps)
	}
	if !childBounds.Contains(h.device.taps[0][0], h.device.taps[0][1]) {
		t.Fatalf("bubble point %v is outside the original child bounds %+v",
			h.device.taps[0], childBounds)
	}

	// A direct target with the requested action never lifts.
	h2 := newTapScopeHarness(t)
	h2.device.scriptCaptures(tapCardStep())
	start2 := h2.startSession()
	card := tapCardNode(t, h2)
	result = h2.actWith(t, "PbubbleDirect", bubbleRequest(start2.Observation.ID, card.Ref, nil))
	if result.Outcome != OutcomeExecuted || len(h2.device.taps) != 1 {
		t.Fatalf("direct bubble default result=%+v taps=%v", result, h2.device.taps)
	}

	// Explicit false keeps the old unsupported rejection and leaves the O ready.
	h3 := newTapScopeHarness(t)
	h3.device.scriptCaptures(bubbleStep(cardBounds, childBounds, "post", false))
	start3 := h3.startSession()
	child3 := tapPublicBoundsNode(currentTree(t, h3.engine).Root, childBounds)
	falseValue := false
	res, apiErr := h3.engine.Act(context.Background(), "agent", "PbubbleFalse",
		bubbleRequest(start3.Observation.ID, child3.Ref, &falseValue))
	if apiErr != nil {
		t.Fatalf("bubble=false admission error: %+v", apiErr)
	}
	if res.Outcome != OutcomeNotExecuted || res.Error == nil ||
		res.Error.Code != protocol.CodeNodeUnsupported {
		t.Fatalf("bubble=false result=%+v, want unsupported/not_executed", res)
	}
	if len(h3.device.taps) != 0 {
		t.Fatalf("bubble=false dispatched taps = %v, want none", h3.device.taps)
	}
	requireReadyObservation(t, h3, start3.Observation.ID)
}

// TestActBubbleFreshFrameRequiresChildAndAncestor proves both identities are
// revalidated and the child cannot silently unbind to a still-unique ancestor.
func TestActBubbleFreshFrameRequiresChildAndAncestor(t *testing.T) {
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 800}
	childBounds := device.Rect{Left: 200, Top: 400, Right: 700, Bottom: 600}
	cases := []struct {
		name  string
		step  captureStep
		check func(t *testing.T, result *protocol.Result)
	}{
		{
			name: "child_lost_ancestor_still_unique",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				return tapPageWithChildren(display, cardBounds), nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("child lost result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "child_public_label_changed",
			step: bubbleStep(cardBounds, childBounds, "different", false),
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("label change result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "child_gained_touch_action",
			step: bubbleStep(cardBounds, childBounds, "post", true),
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("child gained action result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "child_public_role_changed",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				capture := bubblePage(display, cardBounds, childBounds, "post", false)
				capture.Nodes[2].HasCheckable = true
				capture.Nodes[2].Checkable = true
				return capture, nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("role change result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "ancestor_lost",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				return bubbleRootOnly(display, childBounds), nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("ancestor lost result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "ancestor_action_lost",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				capture := bubblePage(display, cardBounds, childBounds, "post", false)
				capture.Nodes[1].HasClickable = true
				capture.Nodes[1].Clickable = false
				capture.Nodes[1].HasLongClick = true
				capture.Nodes[1].LongClickable = false
				capture.Nodes[1].TextSet = true
				capture.Nodes[1].Text = "card"
				return capture, nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("ancestor action lost result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "nearest_ancestor_changed",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				return bubbleNestedPage(display, cardBounds,
					device.Rect{Left: 150, Top: 350, Right: 850, Bottom: 750},
					childBounds), nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("nearest ancestor changed result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "ancestor_ambiguous",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				return bubbleDuplicateAncestorPage(display, cardBounds, childBounds), nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("ancestor ambiguous result=%+v, want target_changed/not_executed", result)
				}
			},
		},
		{
			name: "safe_point_blocked_by_sibling",
			step: func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				return tapPageWithChildren(display, cardBounds,
					tapRawNode(2, 1, 2, display.ID, childBounds,
						"android.widget.TextView", "post", false, false),
					tapRawNode(3, 1, 2, display.ID, cardBounds,
						"android.widget.Button", "cover", true, true)), nil
			},
			check: func(t *testing.T, result *protocol.Result) {
				t.Helper()
				if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
					result.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("blocked safe point result=%+v, want target_changed/not_executed", result)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(bubbleStep(cardBounds, childBounds, "post", false))
			start := h.startSession()
			child := tapPublicBoundsNode(currentTree(t, h.engine).Root, childBounds)
			if child == nil {
				t.Fatal("child node not found")
			}
			h.device.scriptCaptures(tc.step)
			result, apiErr := h.engine.Act(context.Background(), "agent", "PbubbleFresh",
				bubbleRequest(start.Observation.ID, child.Ref, nil))
			if apiErr != nil {
				t.Fatalf("act admission: %+v", apiErr)
			}
			if result.Error != nil {
				t.Logf("error=%+v", result.Error)
			}
			tc.check(t, result)
			if len(h.device.taps) != 0 {
				t.Fatalf("fresh rejection dispatched taps = %v, want none", h.device.taps)
			}
		})
	}
}

// TestActDirectEntrySharesBooleanBranchValidation proves the engine entry uses
// the same branch-qualified effective-member validation as HTTP and MCP.
func TestActDirectEntrySharesBooleanBranchValidation(t *testing.T) {
	h := newTapScopeHarness(t)
	cases := []*protocol.ActRequest{
		{Reference: "O1", Operation: protocol.Operation{
			Type: "tap", Target: &protocol.Target{X: 1, Y: 2},
			Bubble: true, HasBubble: true}},
		{Reference: "O1", Operation: protocol.Operation{
			Type: "key", Key: "BACK", PressEnter: false, HasPressEnter: true}},
		{Reference: "O1", Operation: protocol.Operation{
			Type: "text", Target: &protocol.Target{UseRef: true, Ref: "n1"}, Text: "x",
			Bubble: false, HasBubble: true}},
	}
	for index, request := range cases {
		_, apiErr := h.engine.Act(context.Background(), "agent", "Pdirect", request)
		if apiErr == nil || apiErr.Code != protocol.CodeInvalidRequest {
			t.Fatalf("case %d admission error = %+v, want invalid_request", index, apiErr)
		}
	}
	if h.device.captureCount() != 0 {
		t.Fatalf("direct invalid requests touched the device: captures=%d", h.device.captureCount())
	}
}

// textFlowDevice lets one text test choose the SetText and ENTER receipts
// without replacing the fake capture/readback behavior.
type textFlowDevice struct {
	*revalDevice
	mu       sync.Mutex
	keyMode  string
	keyCalls int
	keyHook  func()
	setMode  string
	setCalls int
}

func (d *textFlowDevice) Key(ctx context.Context, displayID int, key string) (device.DispatchReceipt, error) {
	d.mu.Lock()
	d.keyCalls++
	mode := d.keyMode
	hook := d.keyHook
	d.mu.Unlock()
	if hook != nil {
		hook()
	}
	switch mode {
	case "not_dispatched":
		return device.DispatchNotDispatched, nil
	case "unknown":
		return device.DispatchUnknown, context.DeadlineExceeded
	}
	return d.revalDevice.fakeDevice.Key(ctx, displayID, key)
}

func (d *textFlowDevice) SetTextOnDisplay(ctx context.Context, target device.TextTarget, text string) (device.TextResult, error) {
	d.mu.Lock()
	d.setCalls++
	mode := d.setMode
	d.mu.Unlock()
	switch mode {
	case "not_dispatched":
		return device.TextResult{CandidateCount: 1, Receipt: device.DispatchNotDispatched}, nil
	case "unknown":
		return device.TextResult{CandidateCount: 1, Receipt: device.DispatchUnknown}, context.DeadlineExceeded
	}
	return d.revalDevice.SetTextOnDisplay(ctx, target, text)
}

func (d *textFlowDevice) setReceiptMode(mode string) {
	d.mu.Lock()
	d.setMode = mode
	d.mu.Unlock()
}

func (d *textFlowDevice) setKeyMode(mode string) {
	d.mu.Lock()
	d.keyMode = mode
	d.mu.Unlock()
}

func (d *textFlowDevice) setKeyHook(hook func()) {
	d.mu.Lock()
	d.keyHook = hook
	d.mu.Unlock()
}

func textFlowBuild(displayID int, bounds device.Rect, text string,
	focused bool) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		root := revalRoot(display.ID, 1)
		inp := revalEditText(1, 0, 1, display.ID, bounds, text, "comment_box", "", focused)
		return revalCapture(display, root, inp), nil
	}
}

func newTextFlowHarness(t *testing.T, bounds device.Rect) (*harness, *textFlowDevice,
	string, string) {
	t.Helper()
	h, rec := newRevalHarness(t)
	dev := &textFlowDevice{revalDevice: rec}
	h.engine.dev = dev
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false))
	start := h.startSession()
	input := currentTree(t, h.engine).Root.Children[0]
	if input == nil {
		t.Fatal("input node not found")
	}
	return h, dev, start.Observation.ID, input.Ref
}

func textFlowRequest(obsRef, ref, text string, pressEnter *bool) *protocol.ActRequest {
	req := &protocol.ActRequest{
		Reference: obsRef,
		Operation: protocol.Operation{
			Type: "text", Target: &protocol.Target{UseRef: true, Ref: ref}, Text: text,
		},
	}
	if pressEnter != nil {
		req.Operation.PressEnter, req.Operation.HasPressEnter = *pressEnter, true
	}
	return req
}

func requireTextInput(t *testing.T, result *protocol.Result, write, enter string) {
	t.Helper()
	if result == nil || result.TextInput == nil {
		t.Fatalf("result text_input is absent: %+v", result)
	}
	if result.TextInput.Write != write || result.TextInput.Enter != enter {
		t.Fatalf("text_input = %+v, want write=%s enter=%s", result.TextInput, write, enter)
	}
}

// TestActTextDefaultIncludesOneEnter proves the omitted press_enter sends one
// ENTER after the exact readback and publishes the fixed primitive snapshot.
func TestActTextDefaultIncludesOneEnter(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "hello", true),
	)
	result := h.actWith(t, "PtextDefault", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("default text outcome = %q, want executed (%+v)", result.Outcome, result)
	}
	requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	if dev.keyCalls != 1 {
		t.Fatalf("ENTER calls = %d, want exactly 1", dev.keyCalls)
	}
}

// TestActTextExplicitFalseOnlyInputs proves press_enter=false suppresses ENTER
// and the public text_input object while keeping focus/write/readback.
func TestActTextExplicitFalseOnlyInputs(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "draft", true),
	)
	falseValue := false
	result := h.actWith(t, "PtextFalse",
		textFlowRequest(obsRef, ref, "draft", &falseValue))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("input-only text outcome = %q, want executed (%+v)", result.Outcome, result)
	}
	if result.TextInput != nil {
		t.Fatalf("input-only text carried text_input: %+v", result.TextInput)
	}
	if dev.keyCalls != 0 {
		t.Fatalf("input-only text sent %d keys, want 0", dev.keyCalls)
	}
}

// TestActTextReadbackMismatchNeverSendsEnter proves the exact readback gate is
// a hard precondition for ENTER.
func TestActTextReadbackMismatchNeverSendsEnter(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	different := "different"
	dev.getTextFunc = func(context.Context, device.TextTarget) (device.TextResult, error) {
		return device.TextResult{CandidateCount: 1, Value: &different}, nil
	}
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "hello", true),
	)
	result := h.actWith(t, "PtextMismatch", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("readback mismatch outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	requireTextInput(t, result, protocol.TextInputUncertain, protocol.TextInputNotExecuted)
	if dev.keyCalls != 0 {
		t.Fatalf("readback mismatch sent %d ENTER calls, want 0", dev.keyCalls)
	}
}

// TestActTextEnterNotDispatchedKeepsWriteExecuted proves the two primitives are
// independent and no second ENTER is attempted on replay.
func TestActTextEnterNotDispatchedKeepsWriteExecuted(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	dev.setKeyMode("not_dispatched")
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "hello", true),
	)
	result := h.actWith(t, "PtextEnterNo", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("enter not-dispatched outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputNotExecuted)
	if dev.keyCalls != 1 {
		t.Fatalf("ENTER calls = %d, want exactly 1", dev.keyCalls)
	}

	duplicate, apiErr := h.engine.Act(context.Background(), "agent", "PtextEnterNo",
		textFlowRequest(obsRef, ref, "hello", nil))
	if apiErr != nil {
		t.Fatalf("duplicate P admission: %+v", apiErr)
	}
	requireTextInput(t, duplicate, protocol.TextInputExecuted, protocol.TextInputNotExecuted)
	if dev.keyCalls != 1 {
		t.Fatalf("duplicate P sent another ENTER: calls=%d", dev.keyCalls)
	}
}

// TestActTextRunningStatusCarriesCurrentSnapshot proves status(P) sees the
// write-committed/enter-uncertain snapshot while ENTER is in flight.
func TestActTextRunningStatusCarriesCurrentSnapshot(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	var status *protocol.Result
	dev.setKeyMode("unknown")
	dev.setKeyHook(func() {
		status, _ = h.engine.Status("agent", "PtextStatus")
	})
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "hello", true),
	)
	result := h.actWith(t, "PtextStatus", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("enter unknown outcome = %q, want uncertain", result.Outcome)
	}
	requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputUncertain)
	if status == nil {
		t.Fatal("running status was not captured")
	}
	requireTextInput(t, status, protocol.TextInputExecuted, protocol.TextInputUncertain)
}

// TestActTextWriteNotDispatchedStaysUncertain proves a focus tap alone makes
// the overall outcome uncertain even when both public keys stay not_executed.
func TestActTextWriteNotDispatchedStaysUncertain(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	dev.setReceiptMode("not_dispatched")
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
	)
	result := h.actWith(t, "PtextWriteNo", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("write not-dispatched outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	requireTextInput(t, result, protocol.TextInputNotExecuted, protocol.TextInputNotExecuted)
	if dev.keyCalls != 0 {
		t.Fatalf("write not-dispatched sent %d ENTER calls, want 0", dev.keyCalls)
	}
}

// TestActTextStopBeforeFirstInputKeepsInitialSnapshot proves STOP after claim
// but before the focus tap publishes (not_executed, not_executed) and the
// overall not_executed fact.
func TestActTextStopBeforeFirstInputKeepsInitialSnapshot(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	stopped := false
	dev.revalDevice.onCapture = func(_ context.Context, _ device.Display) (device.Capture, error, bool) {
		if !stopped {
			stopped = true
			_, _ = h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
				AppInstanceID: h.appInstance, StopSeq: 1, Reason: "user_stop",
			})
		}
		return device.Capture{}, nil, false
	}
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false))
	result, apiErr := h.engine.Act(context.Background(), "agent", "PtextStopEarly",
		textFlowRequest(obsRef, ref, "hello", nil))
	if apiErr != nil {
		t.Fatalf("act admission: %+v", apiErr)
	}
	if result.Outcome != OutcomeNotExecuted {
		t.Fatalf("STOP before first input outcome = %q, want not_executed (%+v)",
			result.Outcome, result)
	}
	requireTextInput(t, result, protocol.TextInputNotExecuted, protocol.TextInputNotExecuted)
	if dev.keyCalls != 0 || dev.setCalls != 0 {
		t.Fatalf("STOP before first input dispatched key=%d set=%d", dev.keyCalls, dev.setCalls)
	}
}

// TestActTextStopAfterWriteFreezesConservativeSnapshot proves STOP after the
// write but before ENTER publishes uncertain with enter not executed.
func TestActTextStopAfterWriteFreezesConservativeSnapshot(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	stopped := false
	dev.setKeyHook(func() {
		if stopped {
			return
		}
		stopped = true
		_, _ = h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
			AppInstanceID: h.appInstance, StopSeq: 1, Reason: "user_stop",
		})
	})
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "hello", true),
	)
	result := h.actWith(t, "PtextStop", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("STOP after write outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputUncertain)
	if dev.keyCalls != 1 {
		t.Fatalf("ENTER calls after STOP = %d, want 1", dev.keyCalls)
	}
}
