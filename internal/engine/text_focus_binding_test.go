package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

var (
	focusFoldedBounds   = device.Rect{Left: 100, Top: 1800, Right: 540, Bottom: 1900}
	focusExpandedBounds = device.Rect{Left: 50, Top: 1200, Right: 1000, Bottom: 1450}
)

func focusOwnAnchorPage(display device.Display, bounds device.Rect, focused bool, kind string) device.Capture {
	input := revalEditText(1, 0, 1, display.ID, bounds, "", "", "", focused)
	input.HasClickable, input.Clickable = false, false
	if kind == "resource" {
		input.ResourceSet, input.ResourceID = true, "editor"
	} else {
		input.DescSet, input.Description = true, "editor"
	}
	return revalCapture(display, revalRoot(display.ID, 1), input)
}

func TestTextFocusExpandedOwnAnchor(t *testing.T) {
	for _, kind := range []string{"resource", "description"} {
		t.Run(kind, func(t *testing.T) {
			initial := proposalCaptureStep(func(d device.Display) device.Capture {
				return focusOwnAnchorPage(d, focusFoldedBounds, false, kind)
			})
			focused := proposalCaptureStep(func(d device.Display) device.Capture {
				return focusOwnAnchorPage(d, focusExpandedBounds, true, kind)
			})
			h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
			h.device.scriptCaptures(initial, focused)
			autoSubmit := false
			result := h.actWith(t, "PfocusExpand", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
			requireFocusExecuted(t, result, "expanded")
			requireTextRefNoTapDispatch(t, dev, 1, 1, 0)
			if got := dev.recordedSetTextTargets()[0].target.Bounds; got != focusExpandedBounds {
				t.Fatalf("write bounds = %+v, want %+v", got, focusExpandedBounds)
			}
			display := device.Display{ID: 12, Width: 1080, Height: 2400}
			old := focusOwnAnchorPage(display, focusFoldedBounds, false, kind)
			after := focusOwnAnchorPage(display, focusExpandedBounds, true, kind)
			bound, ok := bindFocusedText(&old, &after, &old.Nodes[1], old.Foreground, 300, 1850)
			if !ok || string(bound.mode) != "expanded" || bound.anchorKind != kind || bound.anchor != (optionalString{set: true, value: "editor"}) {
				t.Fatalf("expanded binding = %+v, ok=%t", bound, ok)
			}
		})
	}

	for _, tc := range []struct {
		name       string
		old, fresh device.Rect
	}{
		{"narrower_900_to_500", device.Rect{Left: 100, Top: 1800, Right: 1000, Bottom: 1900}, device.Rect{Left: 100, Top: 1200, Right: 600, Bottom: 1450}},
		{"disjoint_horizontal", focusFoldedBounds, device.Rect{Left: 540, Top: 1200, Right: 1080, Bottom: 1450}},
		{"off_screen", focusFoldedBounds, device.Rect{Left: 50, Top: 1200, Right: 1081, Bottom: 1450}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			display := device.Display{ID: 12, Width: 1080, Height: 2400}
			old := focusOwnAnchorPage(display, tc.old, false, "resource")
			after := focusOwnAnchorPage(display, tc.fresh, true, "resource")
			if bound, ok := bindFocusedText(&old, &after, &old.Nodes[1], old.Foreground, 300, 1850); ok {
				t.Fatalf("unsafe geometry bound as %q", bound.mode)
			}
		})
	}
}

const focusParentID = "vg_editor_container"

func focusParentAnchorPage(d device.Display, bounds device.Rect, focused bool) device.Capture {
	parent := transparentNode(1, 0, 1, d.ID, d.Bounds(), 2)
	parent.ResourceID = focusParentID
	wrapper := transparentNode(2, 1, 2, d.ID, d.Bounds(), 3)
	input := revalEditText(3, 2, 3, d.ID, bounds, "", "", "", focused)
	input.HasClickable, input.Clickable, input.DescSet = false, false, false
	return revalCapture(d, revalRoot(d.ID, 1), parent, wrapper, input)
}

func focusAddKeyboard(c *device.Capture) {
	keyboardIndex := len(c.Nodes)
	rootIndex := keyboardIndex + 1
	c.Nodes[c.Root].Parent = rootIndex
	for i := range c.Nodes {
		c.Nodes[i].Depth++
	}
	keyboard := revalRoot(c.Display.ID)
	keyboard.Index, keyboard.Parent, keyboard.Depth = keyboardIndex, rootIndex, 1
	keyboard.Package = "com.android.inputmethod"
	keyboard.Bounds = device.Rect{Top: 1600, Right: c.Display.Width, Bottom: c.Display.Height}
	keyboard.RawBounds = keyboard.Bounds
	c.Nodes = append(c.Nodes, keyboard, device.RawNode{
		Index: rootIndex, Parent: -1, Children: []int{c.Root, keyboardIndex}, DisplayID: c.Display.ID,
		Visible: true, Bounds: c.Display.Bounds(), RawBounds: c.Display.Bounds(), HasEnabled: true, Enabled: true,
	})
	c.Root = rootIndex
}

func focusAddInput(c *device.Capture, parent int, bounds device.Rect, focused bool) {
	index := len(c.Nodes)
	input := revalEditText(index, parent, c.Nodes[parent].Depth+1, c.Display.ID, bounds, "", "", "", focused)
	input.DescSet = false
	c.Nodes[parent].Children = append(c.Nodes[parent].Children, index)
	c.Nodes = append(c.Nodes, input)
}

func TestTextFocusExpandedParentResourceAnchor(t *testing.T) {
	t.Run("single_to_multi_window", func(t *testing.T) {
		initial := proposalCaptureStep(func(d device.Display) device.Capture {
			return focusParentAnchorPage(d, focusFoldedBounds, false)
		})
		focused := proposalCaptureStep(func(d device.Display) device.Capture {
			c := focusParentAnchorPage(d, focusExpandedBounds, true)
			focusAddKeyboard(&c)
			return c
		})
		h, dev, obsRef, ref := newTextRefNoTapHarness(t, initial)
		h.device.scriptCaptures(initial, focused)
		autoSubmit := false
		result := h.actWith(t, "PfocusParent", textFlowRequest(obsRef, ref, textRefNoTapValue, &autoSubmit))
		requireFocusExecuted(t, result, "parent_anchor")
		requireTextRefNoTapDispatch(t, dev, 1, 1, 0)
		if got := dev.recordedSetTextTargets()[0].target.Bounds; got != focusExpandedBounds {
			t.Fatalf("write bounds = %+v, want %+v", got, focusExpandedBounds)
		}
		d := device.Display{ID: 12, Width: 1080, Height: 2400}
		old, _ := initial(nil, d)
		after, _ := focused(nil, d)
		bound, ok := bindFocusedText(&old, &after, &old.Nodes[3], old.Foreground, 300, 1850)
		if !ok || string(bound.mode) != "parent_anchor" || bound.raw != &after.Nodes[3] {
			t.Fatalf("parent anchor binding = %+v, ok=%t", bound, ok)
		}
	})

	t.Run("want_from_another_frame", func(t *testing.T) {
		display := device.Display{ID: 12, Width: 1080, Height: 2400}
		old := focusParentAnchorPage(display, focusFoldedBounds, false)
		after := focusParentAnchorPage(display, focusExpandedBounds, true)
		want := old.Nodes[3]
		if bound, ok := bindFocusedText(&old, &after, &want, old.Foreground, 300, 1850); ok {
			t.Fatalf("cross-frame want bound as %q", bound.mode)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(old, fresh *device.Capture)
	}{
		{"duplicate_parent", func(_, fresh *device.Capture) {
			index := len(fresh.Nodes)
			duplicate := transparentNode(index, 0, 1, fresh.Display.ID, fresh.Display.Bounds())
			duplicate.ResourceID = focusParentID
			fresh.Nodes[0].Children = append(fresh.Nodes[0].Children, index)
			fresh.Nodes = append(fresh.Nodes, duplicate)
		}},
		{"window_root", func(old, fresh *device.Capture) {
			for _, c := range []*device.Capture{old, fresh} {
				c.Nodes[0].ResourceID, c.Nodes[1].ResourceID = focusParentID, ""
			}
		}},
		{"fresh_multi_window_root", func(old, fresh *device.Capture) {
			fresh.Nodes[0].ResourceID, fresh.Nodes[1].ResourceID = focusParentID, ""
			fresh.Nodes[0].ClassName = old.Nodes[1].ClassName
			focusAddKeyboard(fresh)
		}},
		{"scroll_view", func(old, fresh *device.Capture) {
			for _, c := range []*device.Capture{old, fresh} {
				c.Nodes[1].ClassName = "android.widget.ScrollView"
				c.Nodes[1].HasScrollable, c.Nodes[1].Scrollable = true, true
			}
		}},
		{"other_unfocused_child", func(_, fresh *device.Capture) {
			focusAddInput(fresh, 1, focusExpandedBounds, false)
		}},
		{"ambiguous_near", func(_, fresh *device.Capture) {
			focusAddInput(fresh, 0, focusFoldedBounds, false)
			focusAddInput(fresh, 0, focusFoldedBounds, false)
		}},
		{"other_focused_field", func(_, fresh *device.Capture) {
			focusAddInput(fresh, 0, device.Rect{Left: 100, Top: 100, Right: 500, Bottom: 200}, true)
		}},
		{"narrower_900_to_500", func(old, fresh *device.Capture) {
			old.Nodes[3].Bounds = device.Rect{Left: 100, Top: 1800, Right: 1000, Bottom: 1900}
			fresh.Nodes[3].Bounds = device.Rect{Left: 100, Top: 1200, Right: 600, Bottom: 1450}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			display := device.Display{ID: 12, Width: 1080, Height: 2400}
			old := focusParentAnchorPage(display, focusFoldedBounds, false)
			after := focusParentAnchorPage(display, focusExpandedBounds, true)
			tc.mutate(&old, &after)
			if tc.name == "ambiguous_near" {
				if n := noAnchorNearCount(&after, &old.Nodes[3], 300, 1850); n != 2 {
					t.Fatalf("near control has %d candidates, want 2", n)
				}
			}
			if bound, ok := bindFocusedText(&old, &after, &old.Nodes[3], old.Foreground, 300, 1850); ok {
				t.Fatalf("ambiguous or unsafe parent anchor bound as %q", bound.mode)
			}
		})
	}
}

func requireFocusExecuted(t *testing.T, result *protocol.Result, mode string) {
	t.Helper()
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("want %s focus and executed, got outcome=%q error=%+v HTTP=%d", mode, result.Outcome, result.Error, result.HTTPStatus)
	}
}

func requireFocusFailure(t *testing.T, result *protocol.Result, code, reason string) {
	t.Helper()
	if result.Outcome != OutcomeUncertain || result.Error == nil || result.Error.Code != code {
		t.Fatalf("want %s with uncertain outcome, got outcome=%q error=%+v", code, result.Outcome, result.Error)
	}
	status := result.HTTPStatus
	if status == 0 {
		status = protocol.Status(result.Error.Code)
	}
	if status != 409 {
		t.Fatalf("focus error HTTP status = %d, want 409", status)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "focus")
	requireTextDetail(t, details, "reason", reason)
	requireTextDispatch(t, details, "dispatched", "not_attempted", "not_attempted")
}

func newFocusPollHarness(t *testing.T, initial captureStep, point bool) (*harness, *textEvDevice, *protocol.ActRequest, *syncBuffer) {
	t.Helper()
	sink := &syncBuffer{}
	log := eventlog.New(sink, "instance-test")
	t.Cleanup(log.Close)
	h := newHarnessWithLogger(t, true, log)
	dev := &textEvDevice{revalDevice: &revalDevice{fakeDevice: h.device}}
	h.engine.dev = dev
	h.device.scriptCaptures(initial)
	start := h.startSession()
	input := findRole(currentTree(t, h.engine).Root, protocol.RoleInput)
	if input == nil {
		t.Fatal("missing fixture input")
	}
	autoSubmit := false
	req := textFlowRequest(start.Observation.ID, input.Ref, textRefNoTapValue, &autoSubmit)
	if point {
		req.Operation.Target = &protocol.Target{X: 300, Y: 1850}
	}
	return h, dev, req, sink
}

func requireFocusEvent(t *testing.T, sink *syncBuffer, op, mode string, attempt int, outcome, reason string) string {
	t.Helper()
	const event = "engine.text.focus"
	events := waitForEvent(t, sink, event)
	if strings.Count(events, event) != 1 {
		t.Fatalf("want one focus event, got %s", events)
	}
	_, line, _ := strings.Cut(events, event)
	line, _, _ = strings.Cut(line, "\n")
	fields := []string{fmt.Sprintf("operation_id=%q", op), fmt.Sprintf("mode=%q", mode),
		fmt.Sprintf("attempt=%d", attempt), fmt.Sprintf("outcome=%q", outcome), "ms="}
	if reason != "" {
		fields = append(fields, fmt.Sprintf("reason=%q", reason))
	} else if strings.Contains(line, "reason=") {
		t.Fatalf("successful focus event includes reason: %s", line)
	}
	for _, field := range fields {
		if !strings.Contains(line, field) {
			t.Errorf("focus event lacks %q: %s", field, line)
		}
	}
	return line
}

func setFocusPollDelays(t *testing.T, delays ...time.Duration) {
	t.Helper()
	previous := textFocusPollDelays
	textFocusPollDelays = delays
	t.Cleanup(func() { textFocusPollDelays = previous })
}

// focusWaitContext signals when the retry pause subscribes to cancellation.
type focusWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *focusWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestTextFocusBoundedRecheckAndStop(t *testing.T) {
	const op = "PfocusPoll"
	initial := proposalCaptureStep(func(d device.Display) device.Capture {
		return focusOwnAnchorPage(d, focusFoldedBounds, false, "resource")
	})
	focused := proposalCaptureStep(func(d device.Display) device.Capture {
		return focusOwnAnchorPage(d, focusFoldedBounds, true, "resource")
	})
	for _, tc := range []struct {
		name, mode string
		point      bool
	}{{"ref", "exact", false}, {"point", "point", true}} {
		t.Run(tc.name, func(t *testing.T) {
			setFocusPollDelays(t, time.Millisecond, time.Millisecond)
			h, dev, req, sink := newFocusPollHarness(t, initial, tc.point)
			h.device.scriptCaptures(initial, initial, focused)
			result := h.actWith(t, op, req)
			requireFocusExecuted(t, result, tc.mode)
			requireTextRefNoTapDispatch(t, dev, 1, 1, 0)
			requireFocusEvent(t, sink, op, tc.mode, 2, "proven", "")
		})
	}
	changedForeground := proposalCaptureStep(func(d device.Display) device.Capture {
		c := focusOwnAnchorPage(d, focusFoldedBounds, true, "resource")
		c.Foreground = revalForeground("com.android.other", 44)
		return c
	})
	for _, tc := range []struct {
		name  string
		first captureStep
		point bool
	}{
		{"capture_error", failingCapture(), false},
		{"ref_foreground_changed", changedForeground, false},
		{"point_foreground_changed", changedForeground, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setFocusPollDelays(t, time.Second, time.Second)
			h, dev, req, sink := newFocusPollHarness(t, initial, tc.point)
			before := h.device.captureCount()
			h.device.scriptCaptures(initial, tc.first, focused)
			started := time.Now()
			result := h.actWith(t, op, req)
			if elapsed := time.Since(started); elapsed >= 750*time.Millisecond {
				t.Fatalf("hard failure waited %s, want immediate termination before backoff", elapsed)
			}
			requireFocusFailure(t, result, protocol.CodeTextTargetUnknown, "focus_unconfirmed")
			requireTextRefNoTapDispatch(t, dev, 1, 0, 0)
			if n := h.device.captureCount() - before; n != 2 {
				t.Fatalf("got %d captures, want only claim and first focus frame", n)
			}
			requireFocusEvent(t, sink, op, "none", 1, "uncertain", "focus_unconfirmed")
		})
	}
	t.Run("stop_in_backoff", func(t *testing.T) {
		setFocusPollDelays(t, time.Second, time.Second)
		h, dev, req, sink := newFocusPollHarness(t, initial, false)
		waiting := make(chan struct{})
		h.device.scriptCaptures(initial, initial, focused)
		dev.tapHook = func() {
			h.engine.mu.Lock()
			defer h.engine.mu.Unlock()
			work := h.engine.work
			work.Ctx = &focusWaitContext{Context: work.Ctx, waiting: waiting}
		}
		type actReply struct {
			result *protocol.Result
			err    *protocol.APIError
		}
		done := make(chan actReply, 1)
		go func() {
			result, err := h.engine.Act(context.Background(), "agent", op, req)
			done <- actReply{result, err}
		}()
		select {
		case <-waiting:
		case reply := <-done:
			t.Fatalf("act finished before STOP during focus backoff: %+v (error=%+v)", reply.result, reply.err)
		case <-time.After(time.Second):
			t.Fatal("focus retry did not enter backoff")
		}
		stopNow(t, h)
		select {
		case reply := <-done:
			if reply.err != nil || reply.result == nil {
				t.Fatalf("act returned %+v", reply)
			}
			requireFocusFailure(t, reply.result, protocol.CodeStopped, "authorization_revoked")
		case <-time.After(750 * time.Millisecond):
			t.Fatal("STOP did not interrupt focus backoff")
		}
		requireTextRefNoTapDispatch(t, dev, 1, 0, 0)
		requireFocusEvent(t, sink, op, "none", 1, "stopped", "authorization_revoked")
	})
}

func TestTextFocusLogWhitelistAndAttemptLimit(t *testing.T) {
	const op = "PfocusWhitelist"
	const privateValue = "private-request-value"
	const privateResource = "private-editor-resource"
	const privateDescription = "private-editor-description"
	setFocusPollDelays(t, time.Millisecond, time.Millisecond, time.Millisecond)
	initial := proposalCaptureStep(func(d device.Display) device.Capture {
		c := focusOwnAnchorPage(d, focusFoldedBounds, false, "resource")
		c.Nodes[1].ResourceID = privateResource
		c.Nodes[1].DescSet, c.Nodes[1].Description = true, privateDescription
		return c
	})
	h, dev, req, sink := newFocusPollHarness(t, initial, false)
	req.Operation.Text = privateValue
	before := h.device.captureCount()
	h.device.scriptCaptures(initial)
	result := h.actWith(t, op, req)
	requireFocusFailure(t, result, protocol.CodeTextTargetUnknown, "focus_unconfirmed")
	requireTextRefNoTapDispatch(t, dev, 1, 0, 0)
	if n := h.device.captureCount() - before; n != 4 {
		t.Fatalf("got %d captures, want one claim frame and three focus frames", n)
	}

	line := requireFocusEvent(t, sink, op, "none", 3, "uncertain", "focus_unconfirmed")
	keys := map[string]bool{
		"service_instance": true, // Added to every event by the logger.
		"operation_id":     true,
		"mode":             true,
		"attempt":          true,
		"outcome":          true,
		"reason":           true,
		"ms":               true,
	}
	for _, field := range strings.Fields(line) {
		key, _, ok := strings.Cut(field, "=")
		if !ok || !keys[key] {
			t.Errorf("unexpected focus log field %q: %s", key, line)
		}
		delete(keys, key)
	}
	if len(keys) != 0 {
		t.Errorf("missing focus log fields %v: %s", keys, line)
	}
	for _, forbidden := range []string{privateValue, privateResource, privateDescription, fmt.Sprintf("%q", req.Operation.Target.Ref)} {
		if strings.Contains(line, forbidden) {
			t.Errorf("focus log exposed %q: %s", forbidden, line)
		}
	}
}
