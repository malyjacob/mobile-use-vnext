package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type recordedTap struct {
	displayID int
	x, y      int
}

type recordedSetText struct {
	target device.TextTarget
	text   string
}

// revalDevice wraps fakeDevice to capture exact primitive arguments, TextTarget
// bindings, and deterministic hook points at ProbeDisplay/Capture boundaries.
type revalDevice struct {
	*fakeDevice
	recMu          sync.Mutex
	taps           []recordedTap
	longPresses    []recordedTap
	getTextTargets []device.TextTarget
	setTextTargets []recordedSetText
	onProbe        func(context.Context, int) (device.DisplayState, error, bool)
	onCapture      func(context.Context, device.Display) (device.Capture, error, bool)
	tapErr         error
	getTextFunc    func(context.Context, device.TextTarget) (device.TextResult, error)
}

func newRevalDevice() *revalDevice {
	return &revalDevice{fakeDevice: newFakeDevice()}
}

func (d *revalDevice) ProbeDisplay(ctx context.Context, id int) (device.DisplayState, error) {
	d.recMu.Lock()
	hook := d.onProbe
	d.recMu.Unlock()
	if hook != nil {
		if state, err, handled := hook(ctx, id); handled {
			return state, err
		}
	}
	return d.fakeDevice.ProbeDisplay(ctx, id)
}

func (d *revalDevice) Capture(ctx context.Context, display device.Display) (device.Capture, error) {
	d.recMu.Lock()
	hook := d.onCapture
	d.recMu.Unlock()
	if hook != nil {
		if cap, err, handled := hook(ctx, display); handled {
			return cap, err
		}
	}
	return d.fakeDevice.Capture(ctx, display)
}

func (d *revalDevice) Tap(ctx context.Context, displayID, x, y int) (device.DispatchReceipt, error) {
	d.recMu.Lock()
	d.taps = append(d.taps, recordedTap{displayID: displayID, x: x, y: y})
	err := d.tapErr
	d.recMu.Unlock()
	if err != nil {
		return device.DispatchNotDispatched, err
	}
	return d.fakeDevice.Tap(ctx, displayID, x, y)
}

func (d *revalDevice) LongPress(ctx context.Context, displayID, x, y int) (device.DispatchReceipt, error) {
	d.recMu.Lock()
	d.longPresses = append(d.longPresses, recordedTap{displayID: displayID, x: x, y: y})
	d.recMu.Unlock()
	return d.fakeDevice.LongPress(ctx, displayID, x, y)
}

func (d *revalDevice) GetTextOnDisplay(ctx context.Context, target device.TextTarget) (device.TextResult, error) {
	d.recMu.Lock()
	d.getTextTargets = append(d.getTextTargets, target)
	fn := d.getTextFunc
	d.recMu.Unlock()
	if fn != nil {
		return fn(ctx, target)
	}
	return d.fakeDevice.GetTextOnDisplay(ctx, target)
}

func (d *revalDevice) SetTextOnDisplay(ctx context.Context, target device.TextTarget, text string) (device.TextResult, error) {
	d.recMu.Lock()
	d.setTextTargets = append(d.setTextTargets, recordedSetText{target: target, text: text})
	d.recMu.Unlock()
	return d.fakeDevice.SetTextOnDisplay(ctx, target, text)
}

func (d *revalDevice) recordedTaps() []recordedTap {
	d.recMu.Lock()
	defer d.recMu.Unlock()
	return append([]recordedTap(nil), d.taps...)
}

func (d *revalDevice) recordedGetTextTargets() []device.TextTarget {
	d.recMu.Lock()
	defer d.recMu.Unlock()
	return append([]device.TextTarget(nil), d.getTextTargets...)
}

func (d *revalDevice) recordedSetTextTargets() []recordedSetText {
	d.recMu.Lock()
	defer d.recMu.Unlock()
	return append([]recordedSetText(nil), d.setTextTargets...)
}

func (d *revalDevice) recordedSwipes() []swipeCall {
	d.fakeDevice.mu.Lock()
	defer d.fakeDevice.mu.Unlock()
	return append([]swipeCall(nil), d.fakeDevice.swipes...)
}

func newRevalHarness(t *testing.T) (*harness, *revalDevice) {
	t.Helper()
	h := newHarness(t)
	rec := &revalDevice{fakeDevice: h.device}
	h.engine.dev = rec
	return h, rec
}

func revalForeground(pkg string, taskID int) device.Foreground {
	id := taskID
	return device.Foreground{
		State:   device.ForegroundPresent,
		Package: pkg,
		TaskID:  &id,
	}
}

func revalRoot(displayID int, children ...int) device.RawNode {
	return device.RawNode{
		Index:       0,
		Parent:      -1,
		Children:    children,
		Depth:       0,
		DisplayID:   displayID,
		Visible:     true,
		RawBounds:   screenRect(),
		Bounds:      screenRect(),
		Package:     "com.example",
		ClassName:   "android.widget.FrameLayout",
		TextSet:     true,
		DescSet:     true,
		ResourceSet: true,
		HasEnabled:  true,
		Enabled:     true,
	}
}

func transparentNode(index, parent, depth, displayID int, bounds device.Rect, children ...int) device.RawNode {
	return device.RawNode{
		Index:         index,
		Parent:        parent,
		Children:      children,
		Depth:         depth,
		DisplayID:     displayID,
		Visible:       true,
		RawBounds:     bounds,
		Bounds:        bounds,
		Package:       "com.example",
		ClassName:     "android.widget.FrameLayout",
		TextSet:       true,
		DescSet:       true,
		ResourceSet:   true,
		HasEnabled:    true,
		Enabled:       true,
		HasClickable:  true,
		Clickable:     false,
		HasLongClick:  true,
		LongClickable: false,
		HasScrollable: true,
		Scrollable:    false,
		HasPassword:   true,
		Password:      false,
		HasCheckable:  true,
		Checkable:     false,
		HasFocused:    true,
		Focused:       false,
		HasSelected:   true,
		Selected:      false,
		HasChecked:    true,
		Checked:       false,
		Editable:      false,
	}
}

func revalButton(index, parent, depth, displayID int, bounds device.Rect, text, desc, resID string) device.RawNode {
	n := device.RawNode{
		Index:         index,
		Parent:        parent,
		Depth:         depth,
		DisplayID:     displayID,
		Visible:       true,
		RawBounds:     bounds,
		Bounds:        bounds,
		Package:       "com.example",
		ClassName:     "android.widget.Button",
		HasEnabled:    true,
		Enabled:       true,
		HasClickable:  true,
		Clickable:     true,
		HasLongClick:  true,
		LongClickable: true,
		HasScrollable: true,
		HasPassword:   true,
		HasCheckable:  true,
		HasFocused:    true,
		HasSelected:   true,
		HasChecked:    true,
	}
	if text != "" {
		n.Text = text
		n.TextSet = true
	}
	if desc != "" {
		n.Description = desc
		n.DescSet = true
	}
	if resID != "" {
		n.ResourceID = resID
		n.ResourceSet = true
	}
	return n
}

func revalEditText(index, parent, depth, displayID int, bounds device.Rect, text, desc, resID string, focused bool) device.RawNode {
	n := device.RawNode{
		Index:         index,
		Parent:        parent,
		Depth:         depth,
		DisplayID:     displayID,
		Visible:       true,
		RawBounds:     bounds,
		Bounds:        bounds,
		Package:       "com.example",
		ClassName:     "android.widget.EditText",
		Text:          text,
		TextSet:       true,
		Editable:      true,
		HasEnabled:    true,
		Enabled:       true,
		HasClickable:  true,
		Clickable:     true,
		HasLongClick:  true,
		HasScrollable: true,
		HasPassword:   true,
		Password:      false,
		HasCheckable:  true,
		HasFocused:    true,
		Focused:       focused,
		HasSelected:   true,
		HasChecked:    true,
	}
	if desc != "" {
		n.Description = desc
		n.DescSet = true
	} else {
		n.DescSet = true
	}
	if resID != "" {
		n.ResourceID = resID
		n.ResourceSet = true
	}
	return n
}

func revalCapture(display device.Display, nodes ...device.RawNode) device.Capture {
	return device.Capture{
		Display:    display,
		Root:       0,
		CapturedAt: time.Now(),
		Foreground: revalForeground("com.example", 42),
		Nodes:      nodes,
	}
}

func mustFindNodeByTextOrResource(t *testing.T, root *protocol.Node, query string) *protocol.Node {
	t.Helper()
	var found *protocol.Node
	var walk func(*protocol.Node)
	walk = func(node *protocol.Node) {
		if node == nil || found != nil {
			return
		}
		for _, lbl := range node.Label {
			if lbl == query {
				found = node
				return
			}
		}
		if node.Resource != nil && *node.Resource == query {
			found = node
			return
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("public node %q not found in tree", query)
	}
	return found
}

func mustFindFirstNodeWithAction(t *testing.T, root *protocol.Node, action protocol.Action) *protocol.Node {
	t.Helper()
	found := tapNodeWithAction(root, action)
	if found == nil {
		t.Fatalf("public node with action %q not found in tree", action)
	}
	return found
}

// ---------------------------------------------------------------- Tier matching & bounds/lineage relaxation

func TestActRevalidationHierarchyAndTiers(t *testing.T) {
	t.Run("ancestor_bounds_changed", func(t *testing.T) {
		// Covers N1: ancestor bounds differ by 1px in fresh frame while target identity/bounds stay unchanged.
		h, rec := newRevalHarness(t)
		makeStep := func(ancBottom int) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: ancBottom}, 2)
				anc.ResourceID = "panel"
				anc.ResourceSet = true
				btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 200, Right: 400, Bottom: 300}, "Save", "", "btn_save")
				btn.TextSet, btn.DescSet, btn.ResourceSet = true, true, true
				return revalCapture(display, root, anc, btn), nil
			}
		}
		h.device.scriptCaptures(makeStep(800))
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Save")

		h.device.scriptCaptures(makeStep(801))
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("ancestor bounds delta should execute: err=%+v res=%+v", apiErr, res)
		}
		taps := rec.recordedTaps()
		if len(taps) != 1 || taps[0].x != 250 || taps[0].y != 250 {
			t.Fatalf("taps = %+v, want [(250,250)]", taps)
		}
	})

	t.Run("target_shifted_within_threshold", func(t *testing.T) {
		// Covers N2: target bounds move 8px down (100,100,300,180) -> (100,108,300,188) with unchanged L1.
		h, rec := newRevalHarness(t)
		makeStep := func(top, bottom int) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 50, Right: 1080, Bottom: 500}, 2)
				anc.ResourceID = "form"
				btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: top, Right: 300, Bottom: bottom}, "Next", "", "btn_next")
				btn.TextSet, btn.DescSet, btn.ResourceSet = true, true, true
				return revalCapture(display, root, anc, btn), nil
			}
		}
		h.device.scriptCaptures(makeStep(100, 180))
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Next")

		h.device.scriptCaptures(makeStep(108, 188))
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("8px shifted target should execute: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 1 {
			t.Fatalf("taps = %+v, want 1", rec.recordedTaps())
		}
	})

	t.Run("transparent_wrapper_inserted", func(t *testing.T) {
		// Covers N3: fresh inserts one provably transparent wrapper between target and non-root anchor.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2)
			anc.ResourceID = "toolbar"
			btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 150, Right: 320, Bottom: 230}, "Confirm", "", "btn_confirm")
			btn.DescSet = true
			return revalCapture(display, root, anc, btn), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Confirm")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2)
			anc.ResourceID = "toolbar"
			wrap := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 90, Top: 140, Right: 330, Bottom: 240}, 3)
			btn := revalButton(3, 2, 3, display.ID, device.Rect{Left: 100, Top: 150, Right: 320, Bottom: 230}, "Confirm", "", "btn_confirm")
			btn.DescSet = true
			return revalCapture(display, root, anc, wrap, btn), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("transparent wrapper Tier 3 should execute: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 1 {
			t.Fatalf("taps = %+v, want 1", rec.recordedTaps())
		}
	})

	t.Run("opaque_wrapper_tristate_rejected", func(t *testing.T) {
		// Covers N4: wrapper with missing presence, non-empty content, or unsafe/unknown state must NOT be filtered as transparent.
		cases := []struct {
			name   string
			mutate func(*device.RawNode)
		}{
			{"text_presence_missing", func(n *device.RawNode) { n.TextSet = false }},
			{"desc_presence_missing", func(n *device.RawNode) { n.DescSet = false }},
			{"resource_presence_missing", func(n *device.RawNode) { n.ResourceSet = false }},
			{"non_empty_text", func(n *device.RawNode) { n.Text = "badge" }},
			{"non_empty_desc", func(n *device.RawNode) { n.Description = "slot" }},
			{"non_empty_resource", func(n *device.RawNode) { n.ResourceID = "holder" }},
			{"clickable_unknown", func(n *device.RawNode) { n.HasClickable = false }},
			{"clickable_true", func(n *device.RawNode) { n.Clickable = true }},
			{"editable_true", func(n *device.RawNode) { n.Editable = true }},
			{"enabled_false", func(n *device.RawNode) { n.Enabled = false }},
			{"enabled_unknown", func(n *device.RawNode) { n.HasEnabled = false }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h, rec := newRevalHarness(t)
				h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1)
					anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2)
					anc.ResourceID = "toolbar"
					btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 150, Right: 320, Bottom: 230}, "Send", "", "btn_send")
					btn.DescSet = true
					return revalCapture(display, root, anc, btn), nil
				})
				start := h.startSession()
				btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Send")

				h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1)
					anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2)
					anc.ResourceID = "toolbar"
					wrap := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 90, Top: 140, Right: 330, Bottom: 240}, 3)
					tc.mutate(&wrap)
					btn := revalButton(3, 2, 3, display.ID, device.Rect{Left: 100, Top: 150, Right: 320, Bottom: 230}, "Send", "", "btn_send")
					btn.DescSet = true
					return revalCapture(display, root, anc, wrap, btn), nil
				})
				res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
					Reference: start.Observation.ID,
					Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
				})
				if apiErr != nil {
					t.Fatalf("unexpected APIError: %+v", apiErr)
				}
				if res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
					t.Fatalf("opaque wrapper %s: want target_changed, got %+v", tc.name, res)
				}
				if len(rec.recordedTaps()) != 0 {
					t.Fatalf("opaque wrapper must dispatch zero taps")
				}
			})
		}
	})

	t.Run("root_only_filtered_lineage_rejected", func(t *testing.T) {
		// Covers N5: wrapper inserted under root; filtering leaves only root in L2 -> Tier 3 must reject.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			btn := revalButton(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 150, Right: 320, Bottom: 230}, "Pay", "", "btn_pay")
			btn.DescSet = true
			return revalCapture(display, root, btn), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Pay")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			wrap := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 90, Top: 140, Right: 330, Bottom: 240}, 2)
			btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 150, Right: 320, Bottom: 230}, "Pay", "", "btn_pay")
			btn.DescSet = true
			return revalCapture(display, root, wrap, btn), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("root-only L2 must reject with target_changed: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("root-only L2 must dispatch zero taps")
		}
	})

	t.Run("duplicate_overlap_candidates_rejected", func(t *testing.T) {
		// Covers N6: two matching fresh candidates with overlaps 0.81 and 0.79 -> ambiguity rejected, never pick 0.81.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2)
			anc.ResourceID = "group"
			btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 200, Right: 200, Bottom: 300}, "Item", "", "btn_item")
			btn.DescSet = true
			return revalCapture(display, root, anc, btn), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Item")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2, 3)
			anc.ResourceID = "group"
			// Overlaps [100,200,200,300]: 81x100 -> 0.81 and 79x100 -> 0.79
			btnA := revalButton(2, 1, 2, display.ID, device.Rect{Left: 119, Top: 200, Right: 219, Bottom: 300}, "Item", "", "btn_item")
			btnA.DescSet = true
			btnB := revalButton(3, 1, 2, display.ID, device.Rect{Left: 121, Top: 200, Right: 221, Bottom: 300}, "Item", "", "btn_item")
			btnB.DescSet = true
			return revalCapture(display, root, anc, btnA, btnB), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("ambiguous Tier 2 candidates must reject: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("ambiguous candidates must dispatch zero taps")
		}
	})

	t.Run("target_text_changed_rejected", func(t *testing.T) {
		// Covers N7: identical resource/class/bounds with changed label -> Tier 1-3 must reject.
		h, rec := newRevalHarness(t)
		makeStep := func(label string) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 500}, 2)
				anc.ResourceID = "bar"
				btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}, label, "", "action_btn")
				btn.DescSet = true
				return revalCapture(display, root, anc, btn), nil
			}
		}
		h.device.scriptCaptures(makeStep("Delete"))
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Delete")

		h.device.scriptCaptures(makeStep("Share"))
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("renamed target must reject: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("renamed target must dispatch zero taps")
		}
	})

	t.Run("shared_fresh_candidate_incoming_ambiguity_rejected", func(t *testing.T) {
		// Covers N33: old frame has two matching-lineage candidates A & B both overlapping fresh C >= 0.50 -> incoming count 2 rejects.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 50, Right: 1080, Bottom: 600}, 2, 3)
			anc.ResourceID = "list_box"
			btnA := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 100, Right: 300, Bottom: 220}, "Item", "", "row_btn")
			btnA.DescSet = true
			btnB := revalButton(3, 1, 2, display.ID, device.Rect{Left: 100, Top: 160, Right: 300, Bottom: 280}, "Item", "", "row_btn")
			btnB.DescSet = true
			return revalCapture(display, root, anc, btnA, btnB), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Item")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 50, Right: 1080, Bottom: 600}, 2)
			anc.ResourceID = "list_box"
			// Overlaps both [100,220] and [160,280] with 90/120 = 0.75 >= 0.50.
			btnC := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 130, Right: 300, Bottom: 250}, "Item", "", "row_btn")
			btnC.DescSet = true
			return revalCapture(display, root, anc, btnC), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("reverse incoming ambiguity must reject: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("reverse incoming ambiguity must dispatch zero taps")
		}
	})

	t.Run("lineage_depth_limit_exceeded", func(t *testing.T) {
		// Covers N38: chain exceeding maxActLineageDepth returns CodeHierarchyFailed with zero delivery.
		h, rec := newRevalHarness(t)
		const chainDepth = 4105
		buildDeep := func(display device.Display) device.Capture {
			nodes := make([]device.RawNode, 0, chainDepth+2)
			nodes = append(nodes, revalRoot(display.ID, 1))
			for idx := 1; idx < chainDepth; idx++ {
				n := transparentNode(idx, idx-1, idx, display.ID, device.Rect{Left: 10, Top: 10, Right: 900, Bottom: 900}, idx+1)
				nodes = append(nodes, n)
			}
			btn := revalButton(chainDepth, chainDepth-1, chainDepth, display.ID, device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}, "Deep", "", "btn_deep")
			btn.DescSet = true
			nodes = append(nodes, btn)
			return revalCapture(display, nodes...)
		}
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildDeep(display), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Deep")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildDeep(display), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil {
			t.Fatalf("unexpected APIError: %+v", apiErr)
		}
		if res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeHierarchyFailed {
			t.Fatalf("depth limit exceeded: want hierarchy_unavailable, got %+v", res)
		}
		if res.Observation != nil {
			t.Fatalf("depth limit error must not deliver observation: %+v", res.Observation)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("depth limit error must dispatch zero taps")
		}
	})
}

// ---------------------------------------------------------------- Tap carriers & target scope

func TestActRevalidationTapCarrier(t *testing.T) {
	t.Run("carrier_shifted_recomputes_safe_point", func(t *testing.T) {
		// Covers N8: carrier image inside card moves vertically; recompute point within matched carrier, never fall back to ancestors.
		h2, rec2 := newRevalHarness(t)
		makeCardStep := func(cTop, cBot, blockTop, topBarBot int) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "", "", "card_1")
				card.ClassName = "android.widget.FrameLayout"
				card.TextSet = true
				card.Children = []int{2, 3, 4}
				carrier := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 180, Top: cTop, Right: 500, Bottom: cBot})
				carrier.ClassName = "android.widget.ImageView"
				carrier.Description = "thumb"
				carrier.ResourceID = "thumb_img"
				blocker := revalButton(3, 1, 2, display.ID, device.Rect{Left: 0, Top: blockTop, Right: 1080, Bottom: 900}, "Like", "", "btn_like")
				blocker.DescSet = true
				topBar := revalButton(4, 1, 2, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: topBarBot}, "Tag", "", "btn_tag")
				topBar.DescSet = true
				return revalCapture(display, root, card, carrier, blocker, topBar), nil
			}
		}
		h2.device.scriptCaptures(makeCardStep(400, 480, 480, 400))
		start2 := h2.startSession()
		cardNode2 := mustFindNodeByTextOrResource(t, currentTree(t, h2.engine).Root, "card_1")

		h2.device.scriptCaptures(makeCardStep(388, 468, 435, 388))
		res, apiErr := h2.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start2.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: cardNode2.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("shifted carrier should recompute safe point and execute: err=%+v res=%+v", apiErr, res)
		}
		taps := rec2.recordedTaps()
		if len(taps) != 1 {
			t.Fatalf("taps = %+v, want 1", taps)
		}
		if taps[0].x != 340 || taps[0].y < 388 || taps[0].y >= 435 {
			t.Fatalf("tap point (%d,%d) should lie inside shifted unblocked carrier [180,388,500,435)", taps[0].x, taps[0].y)
		}
	})

	t.Run("independent_target_carrier_unbind_keep_and_reselect", func(t *testing.T) {
		// Covers N9: target item_42 has independent content identity; carrier A disappears, carrier B exists.
		t.Run("keep_original_point_when_still_safe", func(t *testing.T) {
			h, rec := newRevalHarness(t)
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "item_42", "", "item_42")
				card.ClassName = "android.widget.FrameLayout"
				card.DescSet = true
				card.Children = []int{2, 3, 4, 5}
				carrierA := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 320, Right: 420, Bottom: 420})
				carrierA.ClassName = "android.widget.ImageView"
				carrierA.Description = "photo_a"
				carrierA.ResourceID = "img_a"
				carrierB := transparentNode(3, 1, 2, display.ID, device.Rect{Left: 640, Top: 320, Right: 960, Bottom: 420})
				carrierB.ClassName = "android.widget.ImageView"
				carrierB.Description = "photo_b"
				carrierB.ResourceID = "img_b"
				blocker := revalButton(4, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				centerCol := revalButton(5, 1, 2, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
				centerCol.DescSet = true
				return revalCapture(display, root, card, carrierA, carrierB, blocker, centerCol), nil
			})
			start := h.startSession()
			cardNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "item_42")

			// Fresh removes carrierA while (260, 370) directly on card remains unblocked.
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "item_42", "", "item_42")
				card.ClassName = "android.widget.FrameLayout"
				card.DescSet = true
				card.Children = []int{2, 3, 4}
				carrierB := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 640, Top: 320, Right: 960, Bottom: 420})
				carrierB.ClassName = "android.widget.ImageView"
				carrierB.Description = "photo_b"
				carrierB.ResourceID = "img_b"
				blocker := revalButton(3, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				centerCol := revalButton(4, 1, 2, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
				centerCol.DescSet = true
				return revalCapture(display, root, card, carrierB, blocker, centerCol), nil
			})
			res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: cardNode.Ref}},
			})
			if apiErr != nil || res.Outcome != OutcomeExecuted {
				t.Fatalf("independent content unbind keep point failed: err=%+v res=%+v", apiErr, res)
			}
			taps := rec.recordedTaps()
			if len(taps) != 1 || taps[0].x != 260 || taps[0].y != 370 {
				t.Fatalf("taps = %+v, want original safe point (260,370)", taps)
			}
		})

		t.Run("reselect_second_carrier_when_original_blocked", func(t *testing.T) {
			h, rec := newRevalHarness(t)
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "item_42", "", "item_42")
				card.ClassName = "android.widget.FrameLayout"
				card.DescSet = true
				card.Children = []int{2, 3, 4, 5}
				carrierA := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 320, Right: 420, Bottom: 420})
				carrierA.ClassName = "android.widget.ImageView"
				carrierA.Description = "photo_a"
				carrierA.ResourceID = "img_a"
				carrierB := transparentNode(3, 1, 2, display.ID, device.Rect{Left: 640, Top: 320, Right: 960, Bottom: 420})
				carrierB.ClassName = "android.widget.ImageView"
				carrierB.Description = "photo_b"
				carrierB.ResourceID = "img_b"
				blocker := revalButton(4, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				centerCol := revalButton(5, 1, 2, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
				centerCol.DescSet = true
				return revalCapture(display, root, card, carrierA, carrierB, blocker, centerCol), nil
			})
			start := h.startSession()
			cardNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "item_42")

			// Fresh: carrierA becomes an interactive blocker over left area; carrierB center (800,370) is safe.
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "item_42", "", "item_42")
				card.ClassName = "android.widget.FrameLayout"
				card.DescSet = true
				card.Children = []int{2, 3, 4, 5}
				nowInteractiveA := revalButton(2, 1, 2, display.ID, device.Rect{Left: 0, Top: 300, Right: 480, Bottom: 500}, "Open", "", "btn_a")
				nowInteractiveA.DescSet = true
				carrierB := transparentNode(3, 1, 2, display.ID, device.Rect{Left: 640, Top: 320, Right: 960, Bottom: 420})
				carrierB.ClassName = "android.widget.ImageView"
				carrierB.Description = "photo_b"
				carrierB.ResourceID = "img_b"
				blocker := revalButton(4, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				centerCol := revalButton(5, 1, 2, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
				centerCol.DescSet = true
				return revalCapture(display, root, card, nowInteractiveA, carrierB, blocker, centerCol), nil
			})
			res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: cardNode.Ref}},
			})
			if apiErr != nil || res.Outcome != OutcomeExecuted {
				t.Fatalf("independent content unbind reselect failed: err=%+v res=%+v", apiErr, res)
			}
			taps := rec.recordedTaps()
			if len(taps) != 1 || taps[0].x != 800 || taps[0].y != 370 {
				t.Fatalf("taps = %+v, want carrierB center (800,370)", taps)
			}
		})
	})

	t.Run("carrier_unbind_rejected_for_anonymous_or_overlaid_target", func(t *testing.T) {
		// Covers N10: anonymous target rejects carrier loss; independent target covered by full interactive overlay also rejects.
		t.Run("anonymous_target", func(t *testing.T) {
			h, rec := newRevalHarness(t)
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "", "", "")
				card.ClassName = "android.widget.FrameLayout"
				card.TextSet, card.DescSet, card.ResourceSet = true, true, true
				card.Children = []int{2, 3, 4}
				carrierA := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 320, Right: 420, Bottom: 420})
				carrierA.ClassName = "android.widget.ImageView"
				carrierA.Description = "photo_a"
				carrierA.ResourceID = "img_a"
				blocker := revalButton(3, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				centerCol := revalButton(4, 1, 2, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
				centerCol.DescSet = true
				return revalCapture(display, root, card, carrierA, blocker, centerCol), nil
			})
			start := h.startSession()
			cardNode := currentTree(t, h.engine).Root.Children[0]

			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "", "", "")
				card.ClassName = "android.widget.FrameLayout"
				card.TextSet, card.DescSet, card.ResourceSet = true, true, true
				card.Children = []int{2, 3}
				blocker := revalButton(2, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				centerCol := revalButton(3, 1, 2, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
				centerCol.DescSet = true
				return revalCapture(display, root, card, blocker, centerCol), nil
			})
			res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: cardNode.Ref}},
			})
			if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
				t.Fatalf("anonymous carrier loss must reject: err=%+v res=%+v", apiErr, res)
			}
			if len(rec.recordedTaps()) != 0 {
				t.Fatalf("anonymous carrier loss must dispatch zero taps")
			}
		})

		t.Run("full_overlay_blocks_independent_target", func(t *testing.T) {
			h, rec := newRevalHarness(t)
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "item_42", "", "item_42")
				card.ClassName = "android.widget.FrameLayout"
				card.DescSet = true
				card.Children = []int{2, 3}
				carrierA := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 180, Top: 320, Right: 500, Bottom: 420})
				carrierA.ResourceID = "img_a"
				blocker := revalButton(3, 1, 2, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Follow", "", "btn_follow")
				blocker.DescSet = true
				return revalCapture(display, root, card, carrierA, blocker), nil
			})
			start := h.startSession()
			cardNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "item_42")

			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				card := revalButton(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "item_42", "", "item_42")
				card.ClassName = "android.widget.FrameLayout"
				card.DescSet = true
				card.Children = []int{2}
				overlay := revalButton(2, 1, 2, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "Overlay", "", "overlay")
				overlay.DescSet = true
				return revalCapture(display, root, card, overlay), nil
			})
			res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: cardNode.Ref}},
			})
			if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
				t.Fatalf("full overlay must reject: err=%+v res=%+v", apiErr, res)
			}
			if len(rec.recordedTaps()) != 0 {
				t.Fatalf("full overlay must dispatch zero taps")
			}
		})
	})

	t.Run("shared_list_ancestor_not_independent_content", func(t *testing.T) {
		// Covers N43: shared list ancestor has ResourceID="list", anonymous card target has no text/desc/resource -> unbind forbidden.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			list := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 1800}, 2)
			list.ResourceID = "list"
			card := revalButton(2, 1, 2, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "", "", "")
			card.ClassName = "android.widget.FrameLayout"
			card.TextSet, card.DescSet, card.ResourceSet = true, true, true
			card.Children = []int{3, 4, 5}
			carrierA := transparentNode(3, 2, 3, display.ID, device.Rect{Left: 100, Top: 320, Right: 420, Bottom: 420})
			carrierA.ClassName = "android.widget.ImageView"
			carrierA.Description = "slot_photo_a"
			carrierA.ResourceID = "slot_a"
			blocker := revalButton(4, 2, 3, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Buy", "", "btn_buy")
			blocker.DescSet = true
			centerCol := revalButton(5, 2, 3, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
			centerCol.DescSet = true
			return revalCapture(display, root, list, card, carrierA, blocker, centerCol), nil
		})
		start := h.startSession()
		cardNode := mustFindFirstNodeWithAction(t, currentTree(t, h.engine).Root, protocol.ActionTap)

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			list := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 1800}, 2)
			list.ResourceID = "list"
			card := revalButton(2, 1, 2, display.ID, device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900}, "", "", "")
			card.ClassName = "android.widget.FrameLayout"
			card.TextSet, card.DescSet, card.ResourceSet = true, true, true
			card.Children = []int{3, 4, 5}
			carrierB := transparentNode(3, 2, 3, display.ID, device.Rect{Left: 100, Top: 320, Right: 420, Bottom: 420})
			carrierB.ClassName = "android.widget.ImageView"
			carrierB.Description = "slot_photo_b"
			carrierB.ResourceID = "slot_b"
			blocker := revalButton(4, 2, 3, display.ID, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 900}, "Buy", "", "btn_buy")
			blocker.DescSet = true
			centerCol := revalButton(5, 2, 3, display.ID, device.Rect{Left: 480, Top: 300, Right: 600, Bottom: 500}, "Divider", "", "btn_div")
			centerCol.DescSet = true
			return revalCapture(display, root, list, card, carrierB, blocker, centerCol), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: cardNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("shared list ancestor must not authorize carrier unbind: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("shared list ancestor unbind must dispatch zero taps")
		}
	})

	t.Run("keeps_original_point_when_still_safe", func(t *testing.T) {
		// Covers N11: fresh adds extra safe child, original static point remains safe -> keep original coordinates.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			btn := revalButton(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 200, Right: 500, Bottom: 300}, "Save", "", "btn_save")
			btn.DescSet = true
			return revalCapture(display, root, btn), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Save")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			btn := revalButton(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 220, Right: 500, Bottom: 320}, "Save", "", "btn_save")
			btn.DescSet = true
			btn.Children = []int{2}
			badge := transparentNode(2, 1, 2, display.ID, device.Rect{Left: 420, Top: 230, Right: 490, Bottom: 310})
			badge.Text = "new"
			return revalCapture(display, root, btn, badge), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("should execute using original safe point: err=%+v res=%+v", apiErr, res)
		}
		taps := rec.recordedTaps()
		if len(taps) != 1 || taps[0].x != 300 || taps[0].y != 250 {
			t.Fatalf("taps = %+v, want original (300,250)", taps)
		}
	})

	t.Run("ref_target_moved_recomputes_when_old_point_blocked", func(t *testing.T) {
		// Covers N12: old point enters a fresh interactive child blocker; fresh subtree retains safe point -> recompute inside target.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 600}, 2)
			anc.ResourceID = "form"
			btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 200, Right: 700, Bottom: 300}, "Submit", "", "btn_submit")
			btn.DescSet = true
			return revalCapture(display, root, anc, btn), nil
		})
		start := h.startSession()
		btnNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "Submit")

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			anc := transparentNode(1, 0, 1, display.ID, device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 600}, 2)
			anc.ResourceID = "form"
			btn := revalButton(2, 1, 2, display.ID, device.Rect{Left: 100, Top: 200, Right: 700, Bottom: 300}, "Submit", "", "btn_submit")
			btn.DescSet = true
			btn.Children = []int{3}
			// Blocker covers center (400,250) leaving left half [100,200,350,300] safely tappable via center line span!
			blocker := revalButton(3, 2, 3, display.ID, device.Rect{Left: 350, Top: 200, Right: 700, Bottom: 300}, "Info", "", "btn_info")
			blocker.DescSet = true
			return revalCapture(display, root, anc, btn, blocker), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btnNode.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("blocked original point with safe subtree span should execute: err=%+v res=%+v", apiErr, res)
		}
		taps := rec.recordedTaps()
		if len(taps) != 1 || taps[0].x < 100 || taps[0].x >= 350 || taps[0].y != 250 {
			t.Fatalf("taps = %+v, want recomputed point in [100,350) at y=250", taps)
		}
	})

	t.Run("coordinate_tap_does_not_chase_moved_target", func(t *testing.T) {
		// Covers N13: coordinate tap when fresh resolves original point to another node must reject, even if original target is nearby.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			btn := revalButton(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}, "OK", "", "btn_ok")
			btn.DescSet = true
			return revalCapture(display, root, btn), nil
		})
		start := h.startSession()

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			root := revalRoot(display.ID, 1)
			other := revalButton(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}, "Cancel", "", "btn_cancel")
			other.DescSet = true
			moved := revalButton(2, 0, 1, display.ID, device.Rect{Left: 100, Top: 320, Right: 300, Bottom: 420}, "OK", "", "btn_ok")
			moved.DescSet = true
			root.Children = []int{1, 2}
			return revalCapture(display, root, other, moved), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{X: 200, Y: 250}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("coordinate tap resolved to different node must reject: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("coordinate mismatch must dispatch zero taps")
		}
	})
}

// ---------------------------------------------------------------- Dynamic scroll recomputation & raw swipe

func TestScrollDynamicRevalidation(t *testing.T) {
	buildScrollList := func(display device.Display, containerRect device.Rect, extraChild *device.RawNode) device.Capture {
		root := revalRoot(display.ID, 1)
		list := device.RawNode{
			Index:         1,
			Parent:        0,
			Children:      []int{2},
			Depth:         1,
			DisplayID:     display.ID,
			Visible:       true,
			RawBounds:     containerRect,
			Bounds:        containerRect,
			Package:       "com.example",
			ClassName:     "androidx.recyclerview.widget.RecyclerView",
			ResourceID:    "feed_list",
			TextSet:       true,
			DescSet:       true,
			ResourceSet:   true,
			HasEnabled:    true,
			Enabled:       true,
			HasClickable:  true,
			HasLongClick:  true,
			HasScrollable: true,
			Scrollable:    true,
			HasPassword:   true,
			HasCheckable:  true,
			HasFocused:    true,
			HasSelected:   true,
			HasChecked:    true,
		}
		item := transparentNode(2, 1, 2, display.ID, device.Rect{
			Left:   containerRect.Left + 20,
			Top:    containerRect.Top + 20,
			Right:  containerRect.Right - 20,
			Bottom: containerRect.Top + 120,
		})
		item.Text = "row"
		nodes := []device.RawNode{root, list, item}
		if extraChild != nil {
			extraChild.Index = 3
			extraChild.Parent = 1
			extraChild.Depth = 2
			nodes[1].Children = append(nodes[1].Children, 3)
			nodes = append(nodes, *extraChild)
		}
		return revalCapture(display, nodes...)
	}

	t.Run("raw_swipe_keeps_original_endpoints", func(t *testing.T) {
		// Covers N14: raw swipe uses original from/to even when underlying container bounds/content shift.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, nil), nil
		})
		start := h.startSession()

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 0, Top: 500, Right: 1080, Bottom: 1800}, nil), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type: "swipe",
				From: &protocol.Point{X: 10, Y: 20},
				To:   &protocol.Point{X: 30, Y: 40},
			},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("raw swipe failed: err=%+v res=%+v", apiErr, res)
		}
		swipes := rec.recordedSwipes()
		if len(swipes) != 1 || swipes[0].x1 != 10 || swipes[0].y1 != 20 || swipes[0].x2 != 30 || swipes[0].y2 != 40 {
			t.Fatalf("swipes = %+v, want exact (10,20)->(30,40)", swipes)
		}
	})

	t.Run("vertical_scroll_stretched_recomputes_segment", func(t *testing.T) {
		// Covers N15: vertical container stretches vertically [200,2200] -> [120,2300], same left/right, fresh blocker breaks static end -> recomputes.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, nil), nil
		})
		start := h.startSession()
		listNode := mustFindFirstNodeWithAction(t, currentTree(t, h.engine).Root, protocol.ActionSwipe)

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			blocker := transparentNode(3, 1, 2, display.ID, device.Rect{Left: 0, Top: 1650, Right: 1080, Bottom: 2300})
			blocker.ClassName = "android.widget.HorizontalScrollView"
			blocker.Scrollable = true
			blocker.ResourceID = "inner_pager"
			return buildScrollList(display, device.Rect{Left: 0, Top: 120, Right: 1080, Bottom: 2300}, &blocker), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{UseRef: true, Ref: listNode.Ref},
				Direction: "down",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("axially stretched scroll with safe upper span should execute: err=%+v res=%+v", apiErr, res)
		}
		swipes := rec.recordedSwipes()
		if len(swipes) != 1 || swipes[0].y1 >= 1650 || swipes[0].y2 >= 1650 || swipes[0].y1 <= swipes[0].y2 {
			t.Fatalf("recomputed swipe %+v must lie strictly within safe span [120,1650)", swipes)
		}
	})

	t.Run("vertical_scroll_off_axis_shift_rejected", func(t *testing.T) {
		// Covers N16: vertical scroll container moves 80px horizontally -> off-axis instability rejects.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 100, Top: 200, Right: 980, Bottom: 2200}, nil), nil
		})
		start := h.startSession()
		listNode := mustFindFirstNodeWithAction(t, currentTree(t, h.engine).Root, protocol.ActionSwipe)

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 180, Top: 200, Right: 1060, Bottom: 2200}, nil), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{UseRef: true, Ref: listNode.Ref},
				Direction: "down",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("off-axis 80px shift must reject with target_changed: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedSwipes()) != 0 {
			t.Fatalf("off-axis scroll must dispatch zero swipes")
		}
	})

	t.Run("scroll_recomputes_around_fresh_blocker", func(t *testing.T) {
		// Covers N17: static segment [400,2000] hits fresh blocker around y=1100; lower/upper safe span exceeds minScrollTravel -> recomputes.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, nil), nil
		})
		start := h.startSession()
		listNode := mustFindFirstNodeWithAction(t, currentTree(t, h.engine).Root, protocol.ActionSwipe)

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			blocker := transparentNode(3, 1, 2, display.ID, device.Rect{Left: 0, Top: 1200, Right: 1080, Bottom: 2200})
			blocker.ClassName = "android.widget.HorizontalScrollView"
			blocker.Scrollable = true
			blocker.ResourceID = "inner_carousel"
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, &blocker), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{UseRef: true, Ref: listNode.Ref},
				Direction: "down",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("blocked segment with safe alternative span should recompute: err=%+v res=%+v", apiErr, res)
		}
		swipes := rec.recordedSwipes()
		if len(swipes) != 1 || swipes[0].y1 >= 1200 || swipes[0].y2 < 200 || swipes[0].y1 <= swipes[0].y2 {
			t.Fatalf("recomputed swipe %+v must stay in safe span [200,1200)", swipes)
		}
	})

	t.Run("scroll_all_spans_too_short_rejected", func(t *testing.T) {
		// Covers N18: blockers fragment safe spans below minScrollTravel -> reject.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, nil), nil
		})
		start := h.startSession()
		listNode := mustFindFirstNodeWithAction(t, currentTree(t, h.engine).Root, protocol.ActionSwipe)

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			// Leaves only [200, 280) above inner scrollable blocker -> travel too short.
			blocker := transparentNode(3, 1, 2, display.ID, device.Rect{Left: 0, Top: 280, Right: 1080, Bottom: 2200})
			blocker.ClassName = "android.widget.HorizontalScrollView"
			blocker.Scrollable = true
			blocker.ResourceID = "inner_overlay"
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, &blocker), nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{UseRef: true, Ref: listNode.Ref},
				Direction: "down",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("fragmented scroll must reject: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedSwipes()) != 0 {
			t.Fatalf("fragmented scroll must dispatch zero swipes")
		}
	})

	t.Run("scroll_multiple_matching_containers_rejected", func(t *testing.T) {
		// Covers N19: two fresh scroll containers match identity/lineage -> ambiguity rejects.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			return buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}, nil), nil
		})
		start := h.startSession()
		listNode := mustFindFirstNodeWithAction(t, currentTree(t, h.engine).Root, protocol.ActionSwipe)

		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			cap := buildScrollList(display, device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 1200}, nil)
			second := cap.Nodes[1]
			second.Index = 3
			second.Children = nil
			second.RawBounds = device.Rect{Left: 0, Top: 1150, Right: 1080, Bottom: 2200}
			second.Bounds = second.RawBounds
			cap.Nodes[0].Children = []int{1, 3}
			cap.Nodes = append(cap.Nodes, second)
			return cap, nil
		})
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:      "scroll",
				Container: &protocol.Target{UseRef: true, Ref: listNode.Ref},
				Direction: "down",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeTargetChanged {
			t.Fatalf("multiple matching scroll containers must reject: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedSwipes()) != 0 {
			t.Fatalf("ambiguous container must dispatch zero swipes")
		}
	})
}

// ---------------------------------------------------------------- Text focus binding & ref text planning

func TestTextFocusBinding(t *testing.T) {
	t.Run("shifted_input_with_unique_description_anchor", func(t *testing.T) {
		// Covers N20: no-resource EditText at bottom [100,1800,980,1900] shifts 500px up on keyboard -> bound via Description anchor.
		h, rec := newRevalHarness(t)
		obsBounds := device.Rect{Left: 100, Top: 1800, Right: 980, Bottom: 1900}
		shiftedBounds := device.Rect{Left: 100, Top: 1300, Right: 980, Bottom: 1400}
		buildTextCap := func(bounds device.Rect, text string, focused bool) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				inp := revalEditText(1, 0, 1, display.ID, bounds, text, "comment_box", "", focused)
				return revalCapture(display, root, inp), nil
			}
		}
		h.device.scriptCaptures(buildTextCap(obsBounds, "", false))
		start := h.startSession()
		inputNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "comment_box")

		h.device.scriptCaptures(
			buildTextCap(obsBounds, "", false),         // revalidate
			buildTextCap(shiftedBounds, "", true),      // afterTap (keyboard shifted up 500px)
			buildTextCap(shiftedBounds, "hello", true), // readback
		)
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:   "text",
				Target: &protocol.Target{UseRef: true, Ref: inputNode.Ref},
				Text:   "hello",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("shifted input with unique Description anchor should execute: err=%+v res=%+v", apiErr, res)
		}
		setTargets := rec.recordedSetTextTargets()
		if len(setTargets) != 1 || setTargets[0].target.Bounds != shiftedBounds {
			t.Fatalf("SetTextOnDisplay bounds = %+v, want %+v", setTargets, shiftedBounds)
		}
		getTargets := rec.recordedGetTextTargets()
		if len(getTargets) < 1 || getTargets[0].Bounds != shiftedBounds {
			t.Fatalf("GetTextOnDisplay bounds = %+v, want %+v", getTargets, shiftedBounds)
		}
	})

	t.Run("source_presence_mismatch_rejected", func(t *testing.T) {
		// Covers N21 & N34: replacement field differs in class, description, resource presence, or shifted width -> must return OutcomeUncertain.
		cases := []struct {
			name   string
			mutate func(*device.RawNode)
		}{
			{"class_changed", func(n *device.RawNode) { n.ClassName = "android.widget.MultiAutoCompleteTextView" }},
			{"desc_mismatch", func(n *device.RawNode) { n.Description = "other"; n.DescSet = true }},
			{"resource_presence_changed", func(n *device.RawNode) { n.ResourceSet = true; n.ResourceID = "new_id" }},
			{"width_changed_on_shift", func(n *device.RawNode) {
				n.Bounds = device.Rect{Left: 100, Top: 1300, Right: 500, Bottom: 1400}
				n.RawBounds = n.Bounds
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h, rec := newRevalHarness(t)
				obsBounds := device.Rect{Left: 100, Top: 1800, Right: 980, Bottom: 1900}
				h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1)
					inp := revalEditText(1, 0, 1, display.ID, obsBounds, "", "", "", false)
					return revalCapture(display, root, inp), nil
				})
				start := h.startSession()
				inputNode := currentTree(t, h.engine).Root.Children[0]

				h.device.scriptCaptures(
					func(_ *fakeDevice, display device.Display) (device.Capture, error) {
						root := revalRoot(display.ID, 1)
						inp := revalEditText(1, 0, 1, display.ID, obsBounds, "", "", "", false)
						return revalCapture(display, root, inp), nil
					},
					func(_ *fakeDevice, display device.Display) (device.Capture, error) {
						root := revalRoot(display.ID, 1)
						repl := revalEditText(1, 0, 1, display.ID, obsBounds, "", "", "", true)
						tc.mutate(&repl)
						return revalCapture(display, root, repl), nil
					},
				)
				res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
					Reference: start.Observation.ID,
					Operation: protocol.Operation{
						Type:   "text",
						Target: &protocol.Target{UseRef: true, Ref: inputNode.Ref},
						Text:   "secret",
					},
				})
				if apiErr != nil || res.Outcome != OutcomeUncertain {
					t.Fatalf("mismatch %s must return OutcomeUncertain: err=%+v res=%+v", tc.name, apiErr, res)
				}
				if len(rec.recordedSetTextTargets()) != 0 {
					t.Fatalf("mismatch %s must never call SetTextOnDisplay", tc.name)
				}
			})
		}
	})

	t.Run("duplicate_anchor_with_only_one_focused_rejected", func(t *testing.T) {
		// Covers N22 & N35: two matching-anchor or overlapping no-anchor inputs exist even though only one is focused -> reject before focus filtering.
		t.Run("duplicate_anchor_nodes", func(t *testing.T) {
			h, rec := newRevalHarness(t)
			makeStep := func(focused bool) captureStep {
				return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1, 2)
					inp1 := revalEditText(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "pin", "pin_box", focused)
					inp2 := revalEditText(2, 0, 1, display.ID, device.Rect{Left: 100, Top: 500, Right: 900, Bottom: 600}, "", "pin", "pin_box", false)
					return revalCapture(display, root, inp1, inp2), nil
				}
			}
			h.device.scriptCaptures(makeStep(false))
			start := h.startSession()
			firstInput := currentTree(t, h.engine).Root.Children[0]

			h.device.scriptCaptures(makeStep(false), makeStep(true))
			res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{
					Type:   "text",
					Target: &protocol.Target{UseRef: true, Ref: firstInput.Ref},
					Text:   "1234",
				},
			})
			if apiErr != nil || res.Outcome != OutcomeUncertain {
				t.Fatalf("duplicate anchor across fields must return OutcomeUncertain: err=%+v res=%+v", apiErr, res)
			}
			if len(rec.recordedSetTextTargets()) != 0 {
				t.Fatalf("duplicate anchor must not call SetTextOnDisplay")
			}
		})

		t.Run("duplicate_overlapping_no_anchor_inputs", func(t *testing.T) {
			// Covers N35: two overlapping anonymous inputs contain tap point, only one is focused -> pre-focus near count == 2 rejects.
			h, rec := newRevalHarness(t)
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				inp := revalEditText(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "", "", false)
				return revalCapture(display, root, inp), nil
			})
			start := h.startSession()
			firstInput := currentTree(t, h.engine).Root.Children[0]

			h.device.scriptCaptures(
				func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1)
					inp := revalEditText(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "", "", false)
					return revalCapture(display, root, inp), nil
				},
				func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1, 2)
					inp1 := revalEditText(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "", "", true)
					inp2 := revalEditText(2, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "", "", false)
					return revalCapture(display, root, inp1, inp2), nil
				},
			)
			res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{
					Type:   "text",
					Target: &protocol.Target{UseRef: true, Ref: firstInput.Ref},
					Text:   "abc",
				},
			})
			if apiErr != nil || res.Outcome != OutcomeUncertain {
				t.Fatalf("duplicate no-anchor overlapping inputs must return OutcomeUncertain: err=%+v res=%+v", apiErr, res)
			}
			if len(rec.recordedSetTextTargets()) != 0 {
				t.Fatalf("duplicate no-anchor must not call SetTextOnDisplay")
			}
		})
	})

	t.Run("missing_focused_enabled_password_facts_rejected", func(t *testing.T) {
		// Covers N23: missing positive HasEnabled / HasPassword / HasFocused facts must reject binding.
		cases := []struct {
			name   string
			mutate func(*device.RawNode)
		}{
			{"missing_enabled", func(n *device.RawNode) { n.HasEnabled = false }},
			{"missing_password", func(n *device.RawNode) { n.HasPassword = false }},
			{"missing_focused", func(n *device.RawNode) { n.HasFocused = false }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h, rec := newRevalHarness(t)
				bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
				h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					root := revalRoot(display.ID, 1)
					inp := revalEditText(1, 0, 1, display.ID, bounds, "", "user", "user_box", false)
					return revalCapture(display, root, inp), nil
				})
				start := h.startSession()
				inputNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "user")

				h.device.scriptCaptures(
					func(_ *fakeDevice, display device.Display) (device.Capture, error) {
						root := revalRoot(display.ID, 1)
						inp := revalEditText(1, 0, 1, display.ID, bounds, "", "user", "user_box", false)
						return revalCapture(display, root, inp), nil
					},
					func(_ *fakeDevice, display device.Display) (device.Capture, error) {
						root := revalRoot(display.ID, 1)
						inp := revalEditText(1, 0, 1, display.ID, bounds, "", "user", "user_box", true)
						tc.mutate(&inp)
						return revalCapture(display, root, inp), nil
					},
				)
				res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
					Reference: start.Observation.ID,
					Operation: protocol.Operation{
						Type:   "text",
						Target: &protocol.Target{UseRef: true, Ref: inputNode.Ref},
						Text:   "alice",
					},
				})
				if apiErr != nil || res.Outcome != OutcomeUncertain {
					t.Fatalf("%s must return OutcomeUncertain: err=%+v res=%+v", tc.name, apiErr, res)
				}
				if len(rec.recordedSetTextTargets()) != 0 {
					t.Fatalf("%s must not call SetTextOnDisplay", tc.name)
				}
			})
		}
	})

	t.Run("focus_switched_before_readback_uncertain", func(t *testing.T) {
		// Covers N24: focus moves to another node before readback -> must finish uncertain without re-tapping.
		h, rec := newRevalHarness(t)
		makeCap := func(focusIdx int, val string) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1, 2)
				inp1 := revalEditText(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, val, "first", "field_1", focusIdx == 1)
				inp2 := revalEditText(2, 0, 1, display.ID, device.Rect{Left: 100, Top: 500, Right: 900, Bottom: 600}, "", "second", "field_2", focusIdx == 2)
				return revalCapture(display, root, inp1, inp2), nil
			}
		}
		h.device.scriptCaptures(makeCap(0, ""))
		start := h.startSession()
		firstInput := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "first")

		h.device.scriptCaptures(
			makeCap(0, ""),     // revalidate
			makeCap(1, ""),     // afterTap
			makeCap(2, "done"), // readback: focus jumped to field_2!
		)
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:   "text",
				Target: &protocol.Target{UseRef: true, Ref: firstInput.Ref},
				Text:   "done",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeUncertain {
			t.Fatalf("focus switch before readback must return OutcomeUncertain: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 1 {
			t.Fatalf("focus switch must not re-tap: taps=%+v", rec.recordedTaps())
		}
	})

	t.Run("task_switch_after_tap_rejected", func(t *testing.T) {
		// Covers TaskID foreground check boundary (N36 context).
		h, rec := newRevalHarness(t)
		makeStep := func(taskID int) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				inp := revalEditText(1, 0, 1, display.ID, device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}, "", "email", "email_box", taskID == 99)
				cap := revalCapture(display, root, inp)
				cap.Foreground = revalForeground("com.example", taskID)
				return cap, nil
			}
		}
		h.device.scriptCaptures(makeStep(42))
		start := h.startSession()
		inputNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "email")

		h.device.scriptCaptures(makeStep(42), makeStep(99))
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:   "text",
				Target: &protocol.Target{UseRef: true, Ref: inputNode.Ref},
				Text:   "x@y.z",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeUncertain {
			t.Fatalf("task switch after tap must return OutcomeUncertain: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedSetTextTargets()) != 0 {
			t.Fatalf("task switch after tap must not call SetTextOnDisplay")
		}
	})

	t.Run("ref_text_static_point_blocked_recomputes_safe_point", func(t *testing.T) {
		// Covers N42: ref text static center enters a fresh child blocker while left span remains safe ->
		// first Tap equals recomputed safe point, and GetText/SetText receive bound fresh bounds.
		h, rec := newRevalHarness(t)
		obsInputBounds := device.Rect{Left: 100, Top: 300, Right: 1000, Bottom: 400}
		shiftedInputBounds := device.Rect{Left: 100, Top: 220, Right: 1000, Bottom: 320}
		makeStep := func(inBounds, clearBounds device.Rect, text string, focused bool) captureStep {
			return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				root := revalRoot(display.ID, 1)
				inp := revalEditText(1, 0, 1, display.ID, inBounds, text, "search", "search_input", focused)
				inp.Children = []int{2}
				clearBtn := revalButton(2, 1, 2, display.ID, clearBounds, "", "Clear", "clear_btn")
				clearBtn.TextSet = true
				return revalCapture(display, root, inp, clearBtn), nil
			}
		}
		// Static center of [100,300,1000,400] is (550,350), clearBtn starts at [820,310,980,390].
		h.device.scriptCaptures(makeStep(obsInputBounds, device.Rect{Left: 820, Top: 310, Right: 980, Bottom: 390}, "", false))
		start := h.startSession()
		inputNode := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "search")

		// Fresh reval: clearBtn shifts to [500,300,980,400], blocking old static center (550,350); left span [100,500) center is (300,350).
		h.device.scriptCaptures(
			makeStep(obsInputBounds, device.Rect{Left: 500, Top: 300, Right: 980, Bottom: 400}, "", false),
			makeStep(shiftedInputBounds, device.Rect{Left: 500, Top: 220, Right: 980, Bottom: 320}, "", true),
			makeStep(shiftedInputBounds, device.Rect{Left: 500, Top: 220, Right: 980, Bottom: 320}, "query", true),
		)
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:   "text",
				Target: &protocol.Target{UseRef: true, Ref: inputNode.Ref},
				Text:   "query",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeExecuted {
			t.Fatalf("ref text with blocked center and safe left span should recompute & execute: err=%+v res=%+v", apiErr, res)
		}
		taps := rec.recordedTaps()
		if len(taps) != 1 || taps[0].x != 300 || taps[0].y != 350 {
			t.Fatalf("first Tap = %+v, want recomputed safe point (300,350)", taps)
		}
		setTargets := rec.recordedSetTextTargets()
		if len(setTargets) != 1 || setTargets[0].target.Bounds != shiftedInputBounds {
			t.Fatalf("SetTextOnDisplay target = %+v, want fresh bounds %+v", setTargets, shiftedInputBounds)
		}
		getTargets := rec.recordedGetTextTargets()
		if len(getTargets) < 2 || getTargets[0].Bounds != shiftedInputBounds {
			t.Fatalf("GetTextOnDisplay targets = %+v, want fresh bounds %+v", getTargets, shiftedInputBounds)
		}
	})
}

// ---------------------------------------------------------------- STOP cancellation, foreground & ordering

func TestActRevalidationCancellationAndOrdering(t *testing.T) {
	t.Run("stop_after_probe_display_returns_stopped", func(t *testing.T) {
		// Covers N25 & N40: ProbeDisplay failure after STOP must return CodeStopped, not display unavailable.
		h, rec := newRevalHarness(t)
		start := h.startSession()
		btn := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		if btn == nil {
			t.Fatal("default button not found")
		}
		rec.onProbe = func(ctx context.Context, _ int) (device.DisplayState, error, bool) {
			h.engine.mu.Lock()
			if h.engine.work != nil {
				h.engine.work.Revoked = true
				if h.engine.work.Cancel != nil {
					h.engine.work.Cancel()
				}
			}
			h.engine.mu.Unlock()
			return device.DisplayState{}, errors.New("probe interrupted"), true
		}
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btn.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeStopped {
			t.Fatalf("want CodeStopped after ProbeDisplay revocation, got err=%+v res=%+v", apiErr, res)
		}
		if res.Observation != nil || len(rec.recordedTaps()) != 0 {
			t.Fatalf("stopped probe must not deliver observation or tap")
		}
	})

	t.Run("stop_after_capture_returns_stopped_no_delivery", func(t *testing.T) {
		// Covers N25 & N40: STOP arrives when fresh Capture returns a changed tree -> must return CodeStopped with zero delivery.
		h, rec := newRevalHarness(t)
		start := h.startSession()
		btn := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		if btn == nil {
			t.Fatal("default button not found")
		}
		rec.onCapture = func(_ context.Context, display device.Display) (device.Capture, error, bool) {
			cap, err := h.device.Capture(context.Background(), display)
			if err == nil && len(cap.Nodes) > 1 {
				cap.Nodes[1].Text = "Different"
			}
			h.engine.mu.Lock()
			if h.engine.work != nil {
				h.engine.work.Revoked = true
				if h.engine.work.Cancel != nil {
					h.engine.work.Cancel()
				}
			}
			h.engine.mu.Unlock()
			return cap, err, true
		}
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btn.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeStopped {
			t.Fatalf("want CodeStopped over target_changed after STOP, got err=%+v res=%+v", apiErr, res)
		}
		if res.Observation != nil {
			t.Fatalf("stopped revalidation must not deliver rejected observation: %+v", res.Observation)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("stopped revalidation must dispatch zero taps")
		}
	})

	t.Run("stop_after_text_focus_tap_preserves_uncertain", func(t *testing.T) {
		// Covers N26: STOP arrives after focus tap before SetTextOnDisplay -> OutcomeUncertain.
		h, rec := newRevalHarness(t)
		start := h.startSession()
		input := findRole(currentTree(t, h.engine).Root, protocol.RoleInput)
		if input == nil {
			t.Fatal("default input not found")
		}
		capCount := 0
		rec.onCapture = func(ctx context.Context, display device.Display) (device.Capture, error, bool) {
			capCount++
			cap, err := h.device.Capture(ctx, display)
			if capCount == 2 { // afterTap capture inside dispatchText
				h.engine.mu.Lock()
				if h.engine.work != nil {
					h.engine.work.Revoked = true
					if h.engine.work.Cancel != nil {
						h.engine.work.Cancel()
					}
				}
				h.engine.mu.Unlock()
			}
			return cap, err, true
		}
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{
				Type:   "text",
				Target: &protocol.Target{UseRef: true, Ref: input.Ref},
				Text:   "hello",
			},
		})
		if apiErr != nil || res.Outcome != OutcomeUncertain {
			t.Fatalf("stop after focus tap must return OutcomeUncertain: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 1 {
			t.Fatalf("focus tap count = %d, want 1", len(rec.recordedTaps()))
		}
		if len(rec.recordedSetTextTargets()) != 0 {
			t.Fatalf("SetTextOnDisplay must not run after STOP")
		}
	})

	t.Run("unknown_foreground_rejected", func(t *testing.T) {
		// Covers N31: identical raw nodes but unknown fresh foreground -> CodeForegroundUnknown.
		h, rec := newRevalHarness(t)
		h.device.scriptCaptures(func(f *fakeDevice, display device.Display) (device.Capture, error) {
			cap, err := f.captureLocked(display)
			cap.Foreground = device.Foreground{State: device.ForegroundUnknown}
			return cap, err
		})
		start := h.startSession()
		btn := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		res, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
			Reference: start.Observation.ID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btn.Ref}},
		})
		if apiErr != nil || res.Outcome != OutcomeNotExecuted || res.Error == nil || res.Error.Code != protocol.CodeForegroundUnknown {
			t.Fatalf("unknown foreground must reject with CodeForegroundUnknown: err=%+v res=%+v", apiErr, res)
		}
		if len(rec.recordedTaps()) != 0 {
			t.Fatalf("unknown foreground must dispatch zero taps")
		}
	})

	t.Run("concurrent_claim_single_winner_and_replay", func(t *testing.T) {
		// Covers N29 & N30: concurrent claims on one observation allow one winner; replay returns frozen result.
		h, rec := newRevalHarness(t)
		start := h.startSession()
		btn := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		obsID := start.Observation.ID

		ready := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		rec.onProbe = func(_ context.Context, _ int) (device.DisplayState, error, bool) {
			once.Do(func() {
				close(ready)
				<-release
			})
			return device.DisplayState{}, nil, false
		}

		type out struct {
			res *protocol.Result
			err *protocol.APIError
		}
		ch1 := make(chan out, 1)
		const winOpID = "Preval_win"
		const loseOpID = "Preval_lose"
		go func() {
			r, e := h.engine.Act(context.Background(), "agent", winOpID, &protocol.ActRequest{
				Reference: obsID,
				Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btn.Ref}},
			})
			ch1 <- out{r, e}
		}()
		select {
		case <-ready:
		case early := <-ch1:
			close(release)
			t.Fatalf("winner returned before probe hook: %+v", early)
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("timed out waiting for probe hook")
		}
		res2, err2 := h.engine.Act(context.Background(), "agent", loseOpID, &protocol.ActRequest{
			Reference: obsID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btn.Ref}},
		})
		close(release)
		res1Out := <-ch1
		if res1Out.err != nil || res1Out.res.Outcome != OutcomeExecuted {
			t.Fatalf("winner failed: %+v", res1Out)
		}
		if err2 != nil || res2.Outcome != OutcomeNotExecuted || res2.Error == nil {
			t.Fatalf("second concurrent claimer must fail cleanly: err=%+v res=%+v", err2, res2)
		}
		if len(rec.recordedTaps()) != 1 {
			t.Fatalf("concurrent claims must dispatch only once, got %d", len(rec.recordedTaps()))
		}
		replay, replayErr := h.engine.Act(context.Background(), "agent", winOpID, &protocol.ActRequest{
			Reference: obsID,
			Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: btn.Ref}},
		})
		if replayErr != nil || replay.Outcome != OutcomeExecuted || replay.OperationID != res1Out.res.OperationID {
			t.Fatalf("replay mismatch: err=%+v res=%+v", replayErr, replay)
		}
		if len(rec.recordedTaps()) != 1 {
			t.Fatalf("replay must not dispatch again")
		}
	})
}

// unused format check silencer if needed later
var _ = fmt.Sprint
var _ = strings.Contains
