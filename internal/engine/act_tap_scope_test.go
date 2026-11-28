package engine

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The synthetic composite card is the proposal's §1 geometry:
//
//	card    [0,303,1080,1114]  tap + long_press, center (540,708)
//	content [181,473,1046,529] no action
//	image   [181,552,1046,993] tap + long_press, covers the card center
var (
	tapCardBounds    = device.Rect{Left: 0, Top: 303, Right: 1080, Bottom: 1114}
	tapContentBounds = device.Rect{Left: 181, Top: 473, Right: 1046, Bottom: 529}
	tapImageBounds   = device.Rect{Left: 181, Top: 552, Right: 1046, Bottom: 993}
	tapSafePoint     = [2]int{613, 501}
)

type tapPreviewCounter struct {
	Projector
	calls int
}

func (p *tapPreviewCounter) Preview(capture *device.Capture) (*Projection, error) {
	p.calls++
	return p.Projector.Preview(capture)
}

type tapPreviewFuncProjector struct {
	Projector
	preview func(*device.Capture) (*Projection, error)
}

func (p tapPreviewFuncProjector) Preview(capture *device.Capture) (*Projection, error) {
	return p.preview(capture)
}

func tapRawNode(index, parent, depth, displayID int, bounds device.Rect,
	class, text string, clickable, longClick bool) device.RawNode {
	raw := device.RawNode{
		Index: index, Parent: parent, Depth: depth, DisplayID: displayID,
		Visible: true, RawBounds: bounds, Bounds: bounds,
		Package: "com.example", ClassName: class,
		HasEnabled: true, Enabled: true,
		HasClickable: clickable, Clickable: clickable,
		HasLongClick: longClick, LongClickable: longClick,
	}
	if text != "" {
		raw.Text = text
		raw.TextSet = true
	}
	return raw
}

func tapCardCapture(display device.Display) device.Capture {
	card := tapRawNode(1, 0, 1, display.ID, tapCardBounds,
		"android.widget.FrameLayout", "", true, true)
	card.Children = []int{2, 3}
	return device.Capture{
		Display:    display,
		Root:       0,
		CapturedAt: time.Now(),
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
			card,
			tapRawNode(2, 1, 2, display.ID, tapContentBounds,
				"android.widget.TextView", "post", false, false),
			tapRawNode(3, 1, 2, display.ID, tapImageBounds,
				"android.widget.ImageButton", "photo", true, true),
		},
	}
}

func tapCardVariant(mutate func(*device.Capture, device.Display)) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := tapCardCapture(display)
		if mutate != nil {
			mutate(&capture, display)
		}
		return capture, nil
	}
}

func tapCardStep() captureStep { return tapCardVariant(nil) }

func tapPageWithChildren(display device.Display, cardBounds device.Rect,
	children ...device.RawNode) device.Capture {
	card := tapRawNode(1, 0, 1, display.ID, cardBounds,
		"android.widget.FrameLayout", "", true, true)
	card.Children = make([]int, 0, len(children))
	nodes := []device.RawNode{
		{
			Index: 0, Parent: -1, Children: []int{1}, Depth: 0, DisplayID: display.ID,
			Visible: true, RawBounds: screenRect(), Bounds: screenRect(),
			Package: "com.example", ClassName: "android.widget.FrameLayout",
			HasEnabled: true, Enabled: true,
		},
		card,
	}
	for index := range children {
		child := children[index]
		child.Index = index + 2
		child.Parent = 1
		child.Depth = 2
		child.DisplayID = display.ID
		child.Visible = true
		card.Children = append(card.Children, child.Index)
		nodes = append(nodes, child)
	}
	nodes[1] = card
	return device.Capture{
		Display: display, Root: 0, Nodes: nodes, CapturedAt: time.Now(),
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
		},
	}
}

func tapCardWithChildrenStep(cardBounds device.Rect, children ...device.RawNode) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return tapPageWithChildren(display, cardBounds, children...), nil
	}
}

// tapCardWithFreshChildStep adds a competing interactive child over the
// carrier's fixed point while leaving the target and carrier identities intact.
func tapCardWithFreshChildStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		child := tapRawNode(4, 1, 2, display.ID,
			device.Rect{Left: 500, Top: 450, Right: 800, Bottom: 550},
			"android.widget.ImageButton", "new", true, true)
		child.Children = nil
		capture.Nodes = append(capture.Nodes, child)
		capture.Nodes[1].Children = append(capture.Nodes[1].Children, 4)
	})
}

func tapCardWithFreshCarrierIdentityStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		capture.Nodes[2].Text = "different"
		capture.Nodes[2].TextSet = true
	})
}

func tapCardWithFreshTargetDuplicateStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		clone := capture.Nodes[1]
		clone.Index = len(capture.Nodes)
		clone.Parent = capture.Root
		clone.Children = nil
		capture.Nodes = append(capture.Nodes, clone)
		capture.Nodes[capture.Root].Children = append(
			capture.Nodes[capture.Root].Children, clone.Index)
	})
}

func tapCardWithFreshCarrierDuplicateStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		clone := capture.Nodes[2]
		clone.Index = len(capture.Nodes)
		clone.Parent = 1
		clone.Children = nil
		capture.Nodes = append(capture.Nodes, clone)
		capture.Nodes[1].Children = append(capture.Nodes[1].Children, clone.Index)
	})
}

func tapCardWithFreshSafeAlternativeStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		alternative := tapRawNode(4, 1, 2, display.ID,
			device.Rect{Left: 650, Top: 340, Right: 950, Bottom: 440},
			"android.widget.TextView", "alternative", false, false)
		capture.Nodes = append(capture.Nodes, alternative)
		capture.Nodes[1].Children = append(capture.Nodes[1].Children, 4)
	})
}

func tapCardWithFreshImageMoveStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		bounds := device.Rect{Left: 700, Top: 900, Right: 1000, Bottom: 1100}
		capture.Nodes[3].RawBounds = bounds
		capture.Nodes[3].Bounds = bounds
	})
}

func tapCardWithFreshCarrierMoveStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		bounds := device.Rect{Left: 700, Top: 400, Right: 900, Bottom: 450}
		capture.Nodes[2].RawBounds = bounds
		capture.Nodes[2].Bounds = bounds
	})
}

func tapCardWithFreshCarrierMissingStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		capture.Nodes[2].Visible = false
	})
}

func tapCardWithFreshCarrierContextStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		capture.Nodes[2].Visible = false
		child := tapRawNode(4, 2, 3, display.ID,
			device.Rect{Left: 700, Top: 900, Right: 900, Bottom: 950},
			"android.widget.TextView", "inside context", false, false)
		capture.Nodes[2].Children = []int{4}
		capture.Nodes = append(capture.Nodes, child)
	})
}

func tapCardWithFreshCarrierUnderBoundaryStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		wrapper := tapRawNode(4, 1, 2, display.ID, tapCardBounds,
			"android.widget.FrameLayout", "boundary", true, false)
		wrapper.Children = []int{2}
		capture.Nodes = append(capture.Nodes, wrapper)
		capture.Nodes[1].Children = []int{4, 3}
		capture.Nodes[2].Parent = 4
		capture.Nodes[2].Depth = 3
	})
}

// tapCardExternalDeeperStep adds a unique depth-3 node outside the card at the
// carrier's point. The card subtree proof alone cannot see it; only the whole
// tree ownership check can refuse to advertise that point.
func tapCardExternalDeeperStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		overlay := tapRawNode(4, 0, 1, display.ID, screenRect(),
			"android.widget.FrameLayout", "", false, false)
		overlay.Children = []int{5}
		wrapper := tapRawNode(5, 4, 2, display.ID, screenRect(),
			"android.widget.FrameLayout", "", false, false)
		wrapper.Children = []int{6}
		deep := tapRawNode(6, 5, 3, display.ID,
			device.Rect{Left: 500, Top: 450, Right: 800, Bottom: 550},
			"android.widget.TextView", "external", false, false)
		capture.Nodes[0].Children = []int{1, 4}
		capture.Nodes = append(capture.Nodes, overlay, wrapper, deep)
	})
}

func tapCardExternalSameDepthStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		overlay := tapRawNode(4, 0, 1, display.ID, screenRect(),
			"android.widget.FrameLayout", "", false, false)
		sameDepth := tapRawNode(5, 4, 2, display.ID, screenRect(),
			"android.widget.TextView", "external same depth", false, false)
		overlay.Children = []int{5}
		capture.Nodes[0].Children = []int{1, 4}
		capture.Nodes = append(capture.Nodes, overlay, sameDepth)
	})
}

func tapCardExternalAtStep(bounds device.Rect) captureStep {
	return tapCardVariant(func(capture *device.Capture, display device.Display) {
		overlay := tapRawNode(4, 0, 1, display.ID, bounds,
			"android.widget.FrameLayout", "", false, false)
		overlay.Children = []int{5}
		wrapper := tapRawNode(5, 4, 2, display.ID, bounds,
			"android.widget.FrameLayout", "", false, false)
		wrapper.Children = []int{6}
		deep := tapRawNode(6, 5, 3, display.ID, bounds,
			"android.widget.TextView", "external", false, false)
		capture.Nodes[0].Children = []int{1, 4}
		capture.Nodes = append(capture.Nodes, overlay, wrapper, deep)
	})
}

var (
	tapCoverTopBounds    = device.Rect{Left: 0, Top: 303, Right: 1080, Bottom: 708}
	tapCoverBottomBounds = device.Rect{Left: 0, Top: 708, Right: 1080, Bottom: 1114}
)

func tapCardFullyCoveredStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		card := tapRawNode(1, 0, 1, display.ID, tapCardBounds,
			"android.widget.FrameLayout", "", true, true)
		card.Children = []int{2, 3}
		coverTop := tapRawNode(2, 1, 2, display.ID, tapCoverTopBounds,
			"android.widget.ImageButton", "cover top", true, true)
		coverBottom := tapRawNode(3, 1, 2, display.ID, tapCoverBottomBounds,
			"android.widget.ImageButton", "cover bottom", true, true)
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
				card, coverTop, coverBottom,
			},
		}, nil
	}
}

func tapCardWithContextDescendantStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		card := tapRawNode(1, 0, 1, display.ID, tapCardBounds,
			"android.widget.FrameLayout", "", true, true)
		card.Children = []int{2}
		contextNode := tapRawNode(2, 1, 2, display.ID,
			device.Rect{Left: 0, Top: 0, Right: 1, Bottom: 1},
			"android.widget.FrameLayout", "", false, false)
		contextNode.Visible = false
		contextNode.Children = []int{3}
		descendant := tapRawNode(3, 2, 3, display.ID, tapCardBounds,
			"android.widget.TextView", "context descendant", false, false)
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
				card, contextNode, descendant,
			},
		}, nil
	}
}

func tapAmbiguousCardStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		card := tapRawNode(1, 0, 1, display.ID, tapCardBounds,
			"android.widget.FrameLayout", "", true, true)
		left := tapRawNode(2, 1, 2, display.ID, tapCardBounds,
			"android.widget.TextView", "left", false, false)
		right := tapRawNode(3, 1, 2, display.ID, tapCardBounds,
			"android.widget.TextView", "right", false, false)
		card.Children = []int{2, 3}
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
				card, left, right,
			},
		}, nil
	}
}

// tapWithExternalPeer places a passive hit at the same public depth as a
// card carrier, but under a separate branch of the tree.
func tapWithExternalPeer(step captureStep, peer device.RawNode) captureStep {
	return func(dev *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := step(dev, display)
		if err != nil {
			return device.Capture{}, err
		}
		parent := len(capture.Nodes)
		wrapper := proposalNode(0, 0, 0, display, peer.Bounds,
			"android.widget.FrameLayout", "external wrapper")
		actRebindTestAppend(&capture, 0, wrapper)
		actRebindTestAppend(&capture, parent, peer)
		return capture, nil
	}
}

func tapAmbiguousCardWithExternalPeerStep() captureStep {
	return tapWithExternalPeer(tapAmbiguousCardStep(), tapRawNode(0, 0, 0, 0,
		tapCardBounds, "android.widget.TextView", "external peer", false, false))
}

func tapCardWithBrokenParentStep() captureStep {
	return tapCardVariant(func(capture *device.Capture, _ device.Display) {
		capture.Nodes[3].Parent = len(capture.Nodes) + 4
	})
}

func tapPublicBoundsNode(root *protocol.Node, bounds device.Rect) *protocol.Node {
	if root == nil {
		return nil
	}
	if root.Bounds != nil && rectFromBounds(root.Bounds) == bounds {
		return root
	}
	for _, child := range root.Children {
		if found := tapPublicBoundsNode(child, bounds); found != nil {
			return found
		}
	}
	return nil
}

func tapPublicNode(root *protocol.Node, role protocol.Role, bounds device.Rect) *protocol.Node {
	if root == nil {
		return nil
	}
	if root.Role == role && root.Bounds != nil && rectFromBounds(root.Bounds) == bounds {
		return root
	}
	for _, child := range root.Children {
		if found := tapPublicNode(child, role, bounds); found != nil {
			return found
		}
	}
	return nil
}

func tapContextNode(root *protocol.Node) *protocol.Node {
	if root == nil {
		return nil
	}
	if root.Context {
		return root
	}
	for _, child := range root.Children {
		if found := tapContextNode(child); found != nil {
			return found
		}
	}
	return nil
}

func tapNodeWithAction(root *protocol.Node, action protocol.Action) *protocol.Node {
	if root == nil {
		return nil
	}
	if hasAction(root, action) {
		return root
	}
	for _, child := range root.Children {
		if found := tapNodeWithAction(child, action); found != nil {
			return found
		}
	}
	return nil
}

func tapCardNode(t *testing.T, h *harness) *protocol.Node {
	t.Helper()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root, tapCardBounds)
	if card == nil {
		t.Fatal("composite card not found in the public tree")
	}
	return card
}

func tapContentNode(t *testing.T, h *harness) *protocol.Node {
	t.Helper()
	root := currentTree(t, h.engine).Root
	content := tapPublicNode(root, protocol.RoleText, tapContentBounds)
	if content == nil {
		var dump func(*protocol.Node)
		dump = func(node *protocol.Node) {
			if node == nil {
				return
			}
			t.Logf("node ref=%s role=%s bounds=%+v label=%v actions=%v context=%v",
				node.Ref, node.Role, node.Bounds, node.Label, node.Actions, node.Context)
			for _, child := range node.Children {
				dump(child)
			}
		}
		dump(root)
		t.Fatal("content node not found in the public tree")
	}
	return content
}

func tapCoverNodes(t *testing.T, h *harness) (*protocol.Node, *protocol.Node) {
	t.Helper()
	root := currentTree(t, h.engine).Root
	top := tapPublicNode(root, protocol.RoleButton, tapCoverTopBounds)
	bottom := tapPublicNode(root, protocol.RoleButton, tapCoverBottomBounds)
	if top == nil || bottom == nil {
		t.Fatalf("covering buttons not found in the public tree: top=%v bottom=%v", top, bottom)
	}
	return top, bottom
}

// tapRefRequest pins the explicit bubble=false branch so the tap scope and
// guidance behavior is exercised without the default one-ancestor lift.
func tapRefRequest(observationID, ref string) *protocol.ActRequest {
	return &protocol.ActRequest{
		Reference: observationID,
		Operation: protocol.Operation{
			Type: "tap", Target: &protocol.Target{UseRef: true, Ref: ref},
			Bubble: false, HasBubble: true,
		},
	}
}

// longPressRefRequest pins the explicit bubble=false branch for the same
// reason as tapRefRequest.
func longPressRefRequest(observationID, ref string) *protocol.ActRequest {
	return &protocol.ActRequest{
		Reference: observationID,
		Operation: protocol.Operation{
			Type: "long_press", Target: &protocol.Target{UseRef: true, Ref: ref},
			Bubble: false, HasBubble: true,
		},
	}
}

func newTapScopeHarness(t *testing.T) *harness {
	t.Helper()
	return newTapScopeHarnessWithLogger(t, nil)
}

func newTapScopeHarnessWithLogger(t *testing.T, logger *eventlog.Logger) *harness {
	t.Helper()
	h := newHarnessWithLogger(t, true, logger)
	h.engine.cfg.Settle.Start.Enabled = false
	h.engine.cfg.Settle.Act.Enabled = false
	h.engine.cfg.Settle.OpenApp.Enabled = false
	return h
}

func requireTapPoint(t *testing.T, h *harness, want [2]int) {
	t.Helper()
	if len(h.device.taps) != 1 {
		t.Fatalf("dispatched taps = %v, want exactly one at %v", h.device.taps, want)
	}
	if h.device.taps[0] != want {
		t.Fatalf("dispatched tap = %v, want %v", h.device.taps[0], want)
	}
}

func requireReadyObservation(t *testing.T, h *harness, id string) {
	t.Helper()
	if status := observationStatus(h, id); status != ObservationReady {
		t.Fatalf("observation status = %q, want ready after a static rejection", status)
	}
}

// TestActTapCompositeCardDispatchesCarrierPoint is the first red case: the
// baseline dispatches the card's raw center (540,708), which lies in the image
// button. The accepted behavior is the carrier's fixed point (613,501).
func TestActTapCompositeCardDispatchesCarrierPoint(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)

	result := h.actWith(t, "Pcard", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

func TestActLongPressCompositeCardDispatchesCarrierPoint(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)

	result := h.actWith(t, "Plongcard", longPressRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

// TestActTapCardRejectsFreshCompetingChildAtFixedPoint proves the fixed point
// itself is re-proven. The target and carrier identity are unchanged; only a
// new interactive child covers the planned carrier point.
func TestActTapCardRejectsFreshCompetingChildAtFixedPoint(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	defer logger.Close()
	h := newTapScopeHarnessWithLogger(t, logger)
	h.device.scriptCaptures(tapCardStep(), tapCardWithFreshChildStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Pfreshchild", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if len(h.device.taps) != 0 {
		t.Fatalf("rejected fixed point dispatched input: %v", h.device.taps)
	}
	// scope_blocked is retried, so both revalidation attempts read their own
	// probe and frame.
	if delta := h.device.probeCount() - probes; delta != actRevalidateMaxAttempts {
		t.Fatalf("probe delta = %d, want %d, one fresh display probe per revalidation attempt",
			delta, actRevalidateMaxAttempts)
	}
	if delta := h.device.captureCount() - captures; delta != actRevalidateMaxAttempts {
		t.Fatalf("capture delta = %d, want %d, one fresh capture per revalidation attempt",
			delta, actRevalidateMaxAttempts)
	}
	if result.Observation == nil {
		logger.Close()
		t.Fatalf("rejected fixed point did not deliver the fresh frame it read:\n%s", buffer.String())
	}
}

func TestActTapCardRejectsFreshCarrierIdentityChange(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep(), tapCardWithFreshCarrierIdentityStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Pcarrierchanged", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if len(h.device.taps) != 0 {
		t.Fatalf("carrier identity rejection dispatched input: %v", h.device.taps)
	}
	// scope_blocked is retried, so both revalidation attempts read their own
	// probe and frame.
	if delta := h.device.probeCount() - probes; delta != actRevalidateMaxAttempts {
		t.Fatalf("probe delta = %d, want %d, one fresh display probe per revalidation attempt",
			delta, actRevalidateMaxAttempts)
	}
	if delta := h.device.captureCount() - captures; delta != actRevalidateMaxAttempts {
		t.Fatalf("capture delta = %d, want %d, one fresh capture per revalidation attempt",
			delta, actRevalidateMaxAttempts)
	}
	if result.Observation == nil {
		t.Fatal("carrier identity rejection did not deliver its fresh frame")
	}
}

func TestActTapCardRejectsFreshDuplicateTargetOrCarrierIdentity(t *testing.T) {
	variants := []struct {
		name   string
		step   captureStep
		frames int
	}{
		// A target_ambiguous rejection is not on the retry whitelist, so this
		// subtest proves the whitelist is scoped.
		{"target", tapCardWithFreshTargetDuplicateStep(), 1},
		{"carrier", tapCardWithFreshCarrierDuplicateStep(), actRevalidateMaxAttempts},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(tapCardStep(), variant.step)
			start := h.startSession()
			card := tapCardNode(t, h)
			probes, captures := h.device.probeCount(), h.device.captureCount()

			result := h.actWith(t, "Pduplicate"+variant.name,
				tapRefRequest(start.Observation.ID, card.Ref))
			requireNotExecuted(t, result, protocol.CodeTargetChanged)
			if len(h.device.taps) != 0 {
				t.Fatalf("duplicate %s identity dispatched input: %v",
					variant.name, h.device.taps)
			}
			if delta := h.device.probeCount() - probes; delta != variant.frames {
				t.Fatalf("duplicate %s probe delta = %d, want %d",
					variant.name, delta, variant.frames)
			}
			if delta := h.device.captureCount() - captures; delta != variant.frames {
				t.Fatalf("duplicate %s capture delta = %d, want %d",
					variant.name, delta, variant.frames)
			}
		})
	}
}

func TestActTapCardKeepsPlannedPointWhenFreshAddsSafeAlternative(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep(), tapCardWithFreshSafeAlternativeStep())
	start := h.startSession()
	card := tapCardNode(t, h)

	result := h.actWith(t, "Pfixedpoint", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

// TestActTapCardRejectsFreshCarrierMutationsWithoutReselecting proves a carrier
// that can no longer be re-proven is rejected without reselecting another
// carrier, while a carrier that only moved away is unbound in favour of the
// target's own conserved subtree at the original point.
func TestActTapCardRejectsFreshCarrierMutationsWithoutReselecting(t *testing.T) {
	variants := []struct {
		name string
		step captureStep
	}{
		{"disappeared", tapCardWithFreshCarrierMissingStep()},
		{"became_context", tapCardWithFreshCarrierContextStep()},
		{"under_interactive_boundary", tapCardWithFreshCarrierUnderBoundaryStep()},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(tapCardStep(), variant.step)
			start := h.startSession()
			card := tapCardNode(t, h)
			probes, captures := h.device.probeCount(), h.device.captureCount()

			result := h.actWith(t, "Pcarrier"+variant.name, tapRefRequest(start.Observation.ID, card.Ref))
			requireNotExecuted(t, result, protocol.CodeTargetChanged)
			if len(h.device.taps) != 0 {
				t.Fatalf("carrier mutation %s dispatched input: %v", variant.name, h.device.taps)
			}
			if delta := h.device.probeCount() - probes; delta != actRevalidateMaxAttempts {
				t.Fatalf("carrier mutation %s probe delta = %d, want %d",
					variant.name, delta, actRevalidateMaxAttempts)
			}
			if delta := h.device.captureCount() - captures; delta != actRevalidateMaxAttempts {
				t.Fatalf("carrier mutation %s capture delta = %d, want %d",
					variant.name, delta, actRevalidateMaxAttempts)
			}
			if result.Observation == nil {
				t.Fatalf("carrier mutation %s did not deliver its fresh frame", variant.name)
			}
		})
	}

	// The stale carrier is unbound and the origin point is kept because the
	// target's own readable subtree is conserved.
	t.Run("moved_keeps_original_point", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardStep(), tapCardWithFreshCarrierMoveStep())
		start := h.startSession()
		card := tapCardNode(t, h)

		result := h.actWith(t, "Pcarriermoved", tapRefRequest(start.Observation.ID, card.Ref))
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
		}
		requireTapPoint(t, h, tapSafePoint)
	})
}

func TestActTapCardKeepsFixedPointWhenFreshMovesOutsideElement(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep(), tapCardWithFreshImageMoveStep())
	start := h.startSession()
	card := tapCardNode(t, h)

	result := h.actWith(t, "Poutsidechange", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

func actTapContentGuidance(t *testing.T, h *harness, p string) (*protocol.Result,
	*protocol.Node, *protocol.Node, *protocol.ActRequest) {
	t.Helper()
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	content := tapContentNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()
	request := tapRefRequest(start.Observation.ID, content.Ref)

	result := h.actWith(t, p, request)
	requireNotExecuted(t, result, protocol.CodeNodeUnsupported)
	if result.Error == nil {
		t.Fatal("content tap rejection carried no error")
	}
	details := result.Error.Details
	if details["reason"] != tapStaticReasonTargetMissingAction ||
		details["scope"] != tapStaticScopeTarget ||
		details["scope_ref"] != content.Ref {
		t.Fatalf("content guidance details = %#v, want target_missing_action/target/%q",
			details, content.Ref)
	}
	requireGuidanceCandidate(t, details, card.Ref, candidateRelationAncestor,
		[]int{tapCardBounds.Left, tapCardBounds.Top, tapCardBounds.Right, tapCardBounds.Bottom}, true)
	if len(h.device.taps) != 0 {
		t.Fatalf("content guidance dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero for a static rejection", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero for a static rejection", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
	return result, card, content, request
}

func TestActTapContentGuidanceIncludesAncestorAndCarrierPoint(t *testing.T) {
	h := newTapScopeHarness(t)
	actTapContentGuidance(t, h, "Pguide")
}

func TestActTapContentGuidanceFallsBackToCenterSpan(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardExternalAtStep(
		device.Rect{Left: 500, Top: 450, Right: 800, Bottom: 550}))
	start := h.startSession()
	content := tapContentNode(t, h)
	card := tapCardNode(t, h)

	result := h.actWith(t, "Pguidefallback", tapRefRequest(start.Observation.ID, content.Ref))
	requireNotExecuted(t, result, protocol.CodeNodeUnsupported)
	if result.Error == nil {
		t.Fatal("content tap rejection carried no error")
	}
	details := result.Error.Details
	if details["reason"] != tapStaticReasonTargetMissingAction ||
		details["scope"] != tapStaticScopeTarget ||
		details["scope_ref"] != content.Ref {
		t.Fatalf("content guidance details = %#v, want target_missing_action/target/%q",
			details, content.Ref)
	}
	requireGuidanceCandidate(t, details, card.Ref, candidateRelationAncestor,
		[]int{tapCardBounds.Left, tapCardBounds.Top, tapCardBounds.Right, tapCardBounds.Bottom}, true)
	if len(h.device.taps) != 0 {
		t.Fatalf("guidance rejection dispatched input: %v", h.device.taps)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapContentGuidanceStopsAtNOrNearestAncestorBoundary(t *testing.T) {
	t.Run("target itself is touch-interactive", func(t *testing.T) {
		content := tapRawNode(0, 0, 0, 0, tapContentBounds,
			"android.widget.TextView", "post", false, true)
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardWithChildrenStep(tapCardBounds, content,
			tapRawNode(0, 0, 0, 0, tapImageBounds,
				"android.widget.ImageButton", "photo", true, true)))
		start := h.startSession()
		contentPublic := tapContentNode(t, h)

		result := h.actWith(t, "PguideN", tapRefRequest(start.Observation.ID, contentPublic.Ref))
		requireNotExecuted(t, result, protocol.CodeNodeUnsupported)
		if result.Error == nil || result.Error.Details["reason"] != "target_has_other_action" {
			t.Fatalf("interactive target reason = %+v, want target_has_other_action", result.Error)
		}
		if actions, ok := result.Error.Details["target_actions"].([]string); !ok ||
			len(actions) != 1 || actions[0] != "long_press" {
			t.Fatalf("target_actions = %#v, want [long_press]", result.Error.Details["target_actions"])
		}
		for _, key := range []string{"nearest_actionable_ref", "safe_point", "safe_carrier_ref"} {
			if _, exists := result.Error.Details[key]; exists {
				t.Fatalf("interactive target still produced guidance %q: %+v", key, result.Error.Details)
			}
		}
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("nearest interactive ancestor is disabled", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardVariant(func(capture *device.Capture, _ device.Display) {
			capture.Nodes[1].Enabled = false
		}))
		start := h.startSession()
		content := tapContentNode(t, h)

		result := h.actWith(t, "PguideDisabled", tapRefRequest(start.Observation.ID, content.Ref))
		requireNotExecuted(t, result, protocol.CodeNodeUnsupported)
		if result.Error == nil || result.Error.Details["reason"] != "target_missing_action" {
			t.Fatalf("disabled ancestor reason = %+v, want target_missing_action", result.Error)
		}
		for _, key := range []string{"nearest_actionable_ref", "safe_point", "safe_carrier_ref"} {
			if _, exists := result.Error.Details[key]; exists {
				t.Fatalf("disabled nearest ancestor still produced guidance %q: %+v", key, result.Error.Details)
			}
		}
		requireReadyObservation(t, h, start.Observation.ID)
	})
}

// The advertised ref is usable on the same still-ready O. This is one success
// after the static rejection, not two consumptions of the same O.
func TestActTapContentGuidanceRefNextTurn(t *testing.T) {
	h := newTapScopeHarness(t)
	_, card, _, _ := actTapContentGuidance(t, h, "Pguideref")
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pguiderefnext",
		tapRefRequest(h.currentObservationID(), card.Ref))
	if apiErr != nil {
		t.Fatalf("follow-up ref act rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("follow-up ref outcome = %q error %+v", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

func TestActTapContentGuidanceCoordinateNextTurn(t *testing.T) {
	h := newTapScopeHarness(t)
	actTapContentGuidance(t, h, "Pguidecoord")
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pguidecoordnext",
		&protocol.ActRequest{
			Reference: h.currentObservationID(),
			Operation: protocol.Operation{
				Type: "tap", Target: &protocol.Target{X: tapSafePoint[0], Y: tapSafePoint[1]},
			},
		})
	if apiErr != nil {
		t.Fatalf("follow-up coordinate act rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("follow-up coordinate outcome = %q error %+v", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

// TestActTapContentGuidanceRejectsDeeperExternalUniqueHit is the R1 reverse
// control: the card subtree proof still succeeds, but a deeper external node
// uniquely wins the whole-tree resolution. Advertising the point would be a
// misclick trap, so the whole guidance group is omitted.
func TestActTapContentGuidanceRejectsDeeperExternalUniqueHit(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardExternalDeeperStep())
	start := h.startSession()
	content := tapContentNode(t, h)

	result := h.actWith(t, "Pexternal", tapRefRequest(start.Observation.ID, content.Ref))
	requireNotExecuted(t, result, protocol.CodeNodeUnsupported)
	if result.Error == nil {
		t.Fatal("content tap rejection carried no error")
	}
	if result.Error.Details["reason"] != "target_missing_action" {
		t.Fatalf("external deeper winner reason = %#v, want target_missing_action", result.Error.Details)
	}
	for _, key := range []string{"nearest_actionable_ref", "safe_point", "safe_carrier_ref"} {
		if _, exists := result.Error.Details[key]; exists {
			t.Fatalf("external deeper winner still produced guidance %q: %#v", key, result.Error.Details)
		}
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapContentGuidanceRejectsSameDepthExternalAmbiguity(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardExternalSameDepthStep())
	start := h.startSession()
	content := tapContentNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Psamedepth", tapRefRequest(start.Observation.ID, content.Ref))
	requireNotExecuted(t, result, protocol.CodeNodeUnsupported)
	if result.Error == nil || result.Error.Details["reason"] != "target_missing_action" {
		t.Fatalf("same-depth external ambiguity reason = %+v, want target_missing_action", result.Error)
	}
	for _, key := range []string{"nearest_actionable_ref", "safe_point", "safe_carrier_ref"} {
		if _, exists := result.Error.Details[key]; exists {
			t.Fatalf("same-depth ambiguity still produced guidance %q: %#v", key, result.Error.Details)
		}
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("ambiguous guidance rejection dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActLongPressContentGuidanceAndIndependentNextTurn(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	content := tapContentNode(t, h)
	if !hasAction(card, protocol.ActionLongPress) {
		t.Fatal("guidance ancestor does not support long_press")
	}
	probes, captures := h.device.probeCount(), h.device.captureCount()

	guidance := h.actWith(t, "Plongguide",
		longPressRefRequest(start.Observation.ID, content.Ref))
	requireNotExecuted(t, guidance, protocol.CodeNodeUnsupported)
	if guidance.Error == nil {
		t.Fatal("long_press content rejection carried no error")
	}
	details := guidance.Error.Details
	if details["reason"] != tapStaticReasonTargetMissingAction ||
		details["scope"] != tapStaticScopeTarget ||
		details["scope_ref"] != content.Ref {
		t.Fatalf("long_press guidance details = %#v, want target_missing_action/target/%q",
			details, content.Ref)
	}
	requireGuidanceCandidate(t, details, card.Ref, candidateRelationAncestor,
		[]int{tapCardBounds.Left, tapCardBounds.Top, tapCardBounds.Right, tapCardBounds.Bottom}, true)
	if len(h.device.taps) != 0 {
		t.Fatalf("long_press guidance dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)

	next := h.actWith(t, "Plongguidenext",
		longPressRefRequest(start.Observation.ID, card.Ref))
	if next.Outcome != OutcomeExecuted {
		t.Fatalf("long_press next turn outcome = %q error %+v", next.Outcome, next.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

func TestActTapFullyOccludedCardRejectsBeforeClaim(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardFullyCoveredStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	coverTop, coverBottom := tapCoverNodes(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Poccluded", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeNoSafePoint)
	if result.Error == nil {
		t.Fatal("occluded card rejection carried no error")
	}
	details := result.Error.Details
	if details["reason"] != "blocked" || details["scope"] != "target" ||
		details["scope_ref"] != card.Ref {
		t.Fatalf("occluded details = %#v, want blocked/target/%q", details, card.Ref)
	}
	candidates, ok := details["candidates"].([]map[string]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want 2 blocking child candidates", details["candidates"])
	}
	gotRefs := map[string]bool{
		candidates[0]["ref"].(string): true,
		candidates[1]["ref"].(string): true,
	}
	if !gotRefs[coverTop.Ref] || !gotRefs[coverBottom.Ref] {
		t.Fatalf("candidate refs = %#v, want %q and %q", candidates, coverTop.Ref, coverBottom.Ref)
	}
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("occluded card dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapAmbiguousCardRejectsWithoutOccludingRef(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapAmbiguousCardWithExternalPeerStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Pambiguous", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeNoSafePoint)
	if result.Error == nil {
		t.Fatal("ambiguous card rejection carried no error")
	}
	details := result.Error.Details
	if details["reason"] != "ambiguous" || details["scope"] != "target" ||
		details["scope_ref"] != card.Ref {
		t.Fatalf("ambiguous details = %#v, want ambiguous/target/%q", details, card.Ref)
	}
	root := currentTree(t, h.engine).Root
	left := tapContentNodeByLabel(t, root, "left")
	right := tapContentNodeByLabel(t, root, "right")
	peer := tapContentNodeByLabel(t, root, "external peer")
	requireDetailStringSlice(t, details, "blocking_refs", left.Ref, right.Ref, peer.Ref)
	if _, exists := details["candidates"]; exists {
		t.Fatalf("ambiguity-only rejection unexpectedly carried candidates: %#v", details)
	}
	requireNoLegacyTapDetailKeys(t, details)
	if len(h.device.taps) != 0 {
		t.Fatalf("ambiguous card dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapRefTargetSourceMissingIsStatic(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()
	h.engine.mu.Lock()
	delete(h.engine.current.Projection.Source, card.Ref)
	h.engine.mu.Unlock()

	result := h.actWith(t, "Psource", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeNodeNotFound)
	if len(h.device.taps) != 0 {
		t.Fatalf("missing source dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapRefPlainButtonKeepsCenter(t *testing.T) {
	h := newTapScopeHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found")
	}

	result := h.actWith(t, "Pplain", tapRefRequest(start.Observation.ID, button.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("plain button outcome = %q error %+v", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{500, 150})
}

func TestActTapUsesVerticalCenterSpanWithoutCarrier(t *testing.T) {
	cardBounds := device.Rect{Left: 0, Top: 300, Right: 1000, Bottom: 1000}
	cover := tapRawNode(0, 0, 0, 0,
		device.Rect{Left: 0, Top: 550, Right: 1000, Bottom: 1000},
		"android.widget.ImageButton", "cover", true, true)
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardWithChildrenStep(cardBounds, cover))
	start := h.startSession()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root, cardBounds)
	if card == nil {
		t.Fatal("card not found in the public tree")
	}

	result := h.actWith(t, "Pverticalspan", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{500, 425})
}

func TestActTapUsesHorizontalCenterSpanTieBreak(t *testing.T) {
	cardBounds := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
	cover := tapRawNode(0, 0, 0, 0,
		device.Rect{Left: 400, Top: 0, Right: 600, Bottom: 1000},
		"android.widget.ImageButton", "cover", true, true)
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardWithChildrenStep(cardBounds, cover))
	start := h.startSession()
	card := tapPublicBoundsNode(currentTree(t, h.engine).Root, cardBounds)
	if card == nil {
		t.Fatal("card not found in the public tree")
	}

	result := h.actWith(t, "Phorizontalspan", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{200, 500})
}

func TestActTapCarrierNodePrefersNamedBeforeAnonymous(t *testing.T) {
	anonymous := tapRawNode(0, 0, 0, 0,
		device.Rect{Left: 50, Top: 340, Right: 300, Bottom: 440},
		"android.widget.FrameLayout", "", false, false)
	anonymous.HasFocused, anonymous.Focused = true, false
	anonymous.HasSelected, anonymous.Selected = true, false
	anonymous.HasPassword, anonymous.Password = true, false
	anonymous.HasChecked, anonymous.Checked = true, false
	anonymous.Children = []int{5}
	retainedChild := tapRawNode(5, 2, 3, 0, device.Rect{},
		"android.widget.TextView", "anonymous child", false, false)
	named := tapRawNode(0, 0, 0, 0,
		device.Rect{Left: 350, Top: 340, Right: 600, Bottom: 440},
		"android.widget.TextView", "named", false, false)
	cover := tapRawNode(0, 0, 0, 0, tapImageBounds,
		"android.widget.ImageButton", "cover", true, true)

	h := newTapScopeHarness(t)
	h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := tapPageWithChildren(display, tapCardBounds, anonymous, named, cover)
		retainedChild.DisplayID = display.ID
		capture.Nodes = append(capture.Nodes, retainedChild)
		return capture, nil
	})
	start := h.startSession()
	card := tapCardNode(t, h)
	anonymousPublic := tapPublicBoundsNode(currentTree(t, h.engine).Root,
		device.Rect{Left: 50, Top: 340, Right: 300, Bottom: 440})
	if anonymousPublic == nil || !anonymousPlainNode(anonymousPublic) {
		t.Fatalf("anonymous carrier fixture did not satisfy the read predicate: %+v", anonymousPublic)
	}
	if len(anonymousPublic.Children) != 1 {
		t.Fatalf("anonymous carrier fixture has %d children, want one retained child",
			len(anonymousPublic.Children))
	}

	result := h.actWith(t, "Pcarrierorder", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
	}
	requireTapPoint(t, h, [2]int{475, 390})
}

func TestTapSafeCenterPreservesTruncationWithoutOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	for _, test := range []struct {
		name string
		rect device.Rect
		x, y int
	}{
		{"mixed-sign negative odd sum", device.Rect{Left: -4, Top: -4, Right: 3, Bottom: 3}, 0, 0},
		{"int extremes", device.Rect{Left: minInt, Top: minInt, Right: maxInt, Bottom: maxInt}, 0, 0},
		{"positive overflow edge", device.Rect{Left: maxInt - 1, Top: maxInt - 1,
			Right: maxInt, Bottom: maxInt}, maxInt - 1, maxInt - 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			x, y, ok := tapSafeCenter(test.rect)
			if !ok || x != test.x || y != test.y {
				t.Fatalf("center = (%d,%d,%v), want (%d,%d,true)",
					x, y, ok, test.x, test.y)
			}
		})
	}
}

func TestActTapContextDescendantBlocksWithoutOwnBoundsOrSource(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardWithContextDescendantStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	contextNode := tapContextNode(currentTree(t, h.engine).Root)
	if contextNode == nil || contextNode.Bounds != nil {
		t.Fatalf("context node = %+v, want context=true bounds=nil", contextNode)
	}
	h.engine.mu.Lock()
	_, hasSource := h.engine.current.Projection.Source[contextNode.Ref]
	h.engine.mu.Unlock()
	if hasSource {
		t.Fatalf("context %q must not have a source mapping", contextNode.Ref)
	}
	probes, captures := h.device.probeCount(), h.device.captureCount()
	h.engine.mu.Lock()
	contextRaw := h.engine.current.Projection.Capture.Nodes[2]
	descendantRaw := h.engine.current.Projection.Capture.Nodes[3]
	h.engine.mu.Unlock()
	if contextRaw.Bounds.Contains(540, 708) {
		t.Fatal("context fixture's own bounds unexpectedly contain the card center")
	}
	if !descendantRaw.Bounds.Contains(540, 708) {
		t.Fatal("context descendant does not extend over the card center")
	}

	result := h.actWith(t, "Pcontextblocked", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeNoSafePoint)
	if result.Error == nil || result.Error.Details["reason"] != "blocked" {
		t.Fatalf("context descendant did not block the card: %+v", result.Error)
	}
	if refs, ok := result.Error.Details["blocking_refs"].([]string); !ok ||
		len(refs) != 1 || refs[0] != contextNode.Ref {
		t.Fatalf("blocking_refs = %#v, want [%q]",
			result.Error.Details["blocking_refs"], contextNode.Ref)
	}
	requireNoLegacyTapDetailKeys(t, result.Error.Details)
	if len(h.device.taps) != 0 {
		t.Fatalf("context-blocked card dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("capture delta = %d, want zero", delta)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

func TestActTapSwipeOnlyChildIsNotAnInteractionBoundary(t *testing.T) {
	for _, operation := range []string{"tap", "long_press"} {
		t.Run(operation, func(t *testing.T) {
			swipeNode := tapRawNode(0, 0, 0, 0, tapCardBounds,
				"android.widget.ScrollView", "", false, false)
			swipeNode.HasScrollable, swipeNode.Scrollable = true, true
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(tapCardWithChildrenStep(tapCardBounds, swipeNode))
			start := h.startSession()
			card := tapCardNode(t, h)
			swipePublic := tapNodeWithAction(currentTree(t, h.engine).Root, protocol.ActionSwipe)
			if swipePublic == nil || len(swipePublic.Actions) != 1 ||
				swipePublic.Actions[0] != protocol.ActionSwipe {
				t.Fatalf("swipe-only fixture published actions %v", swipePublic)
			}
			request := tapRefRequest(start.Observation.ID, card.Ref)
			if operation == "long_press" {
				request = longPressRefRequest(start.Observation.ID, card.Ref)
			}

			result := h.actWith(t, "Pswipe"+operation, request)
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome = %q error %+v, want executed", result.Outcome, result.Error)
			}
			requireTapPoint(t, h, [2]int{540, 708})
		})
	}
}

func TestActTapCompetingDescendantCapabilitiesBlockBothActions(t *testing.T) {
	variants := []struct {
		name   string
		mutate func(*device.RawNode)
	}{
		{"disabled", func(node *device.RawNode) {
			node.Enabled = false
		}},
		{"long_press_only", func(node *device.RawNode) {
			node.HasClickable, node.Clickable = true, false
		}},
		{"text_only", func(node *device.RawNode) {
			node.HasClickable, node.Clickable = true, false
			node.HasLongClick, node.LongClickable = true, false
			node.Editable = true
		}},
	}
	for _, variant := range variants {
		for _, operation := range []string{"tap", "long_press"} {
			t.Run(variant.name+"/"+operation, func(t *testing.T) {
				topChild := tapRawNode(0, 0, 0, 0, tapCoverTopBounds,
					"android.widget.ImageButton", "cover top", true, true)
				bottomChild := tapRawNode(0, 0, 0, 0, tapCoverBottomBounds,
					"android.widget.ImageButton", "cover bottom", true, true)
				variant.mutate(&topChild)
				variant.mutate(&bottomChild)
				h := newTapScopeHarness(t)
				h.device.scriptCaptures(tapCardWithChildrenStep(tapCardBounds, topChild, bottomChild))
				start := h.startSession()
				card := tapCardNode(t, h)
				probes, captures := h.device.probeCount(), h.device.captureCount()
				request := tapRefRequest(start.Observation.ID, card.Ref)
				if operation == "long_press" {
					request = longPressRefRequest(start.Observation.ID, card.Ref)
				}

				result := h.actWith(t, "Pblock"+variant.name+operation, request)
				requireNotExecuted(t, result, protocol.CodeNoSafePoint)
				if result.Error == nil || result.Error.Details["reason"] != "blocked" {
					t.Fatalf("%s/%s reason = %+v, want blocked", variant.name, operation, result.Error)
				}
				if len(h.device.taps) != 0 {
					t.Fatalf("%s/%s dispatched input: %v", variant.name, operation, h.device.taps)
				}
				if delta := h.device.probeCount() - probes; delta != 0 {
					t.Fatalf("probe delta = %d, want zero", delta)
				}
				if delta := h.device.captureCount() - captures; delta != 0 {
					t.Fatalf("capture delta = %d, want zero", delta)
				}
				requireReadyObservation(t, h, start.Observation.ID)
			})
		}
	}
}

// A fresh malformed raw ancestry must be rejected before identity matching or
// preview. The baseline ignores the broken parent and still dispatches.
func TestActTapRefRejectsMalformedFreshRawTopology(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep(), tapCardWithBrokenParentStep())
	counter := &tapPreviewCounter{Projector: h.engine.projector}
	h.engine.projector = counter
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()
	counter.calls = 0

	result := h.actWith(t, "Pbrokentree", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
	if len(h.device.taps) != 0 {
		t.Fatalf("malformed fresh tree dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 1 {
		t.Fatalf("probe delta = %d, want one", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 1 {
		t.Fatalf("capture delta = %d, want one", delta)
	}
	if result.Observation != nil {
		t.Fatalf("hierarchy failure delivered an observation: %+v", result.Observation)
	}
	if counter.calls != 0 {
		t.Fatalf("malformed fresh raw topology entered Preview %d times", counter.calls)
	}
}

func TestActTapStaticPublicAndSourceDamageIsBounded(t *testing.T) {
	t.Run("public cycle", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardStep())
		counter := &tapPreviewCounter{Projector: h.engine.projector}
		h.engine.projector = counter
		start := h.startSession()
		card := tapCardNode(t, h)
		h.engine.mu.Lock()
		original := append([]*protocol.Node(nil), card.Children...)
		card.Children = append(card.Children, card)
		h.engine.mu.Unlock()
		probes, captures := h.device.probeCount(), h.device.captureCount()
		counter.calls = 0

		result := h.actWith(t, "Ppubliccycle", tapRefRequest(start.Observation.ID, card.Ref))
		requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
		h.engine.mu.Lock()
		card.Children = original
		h.engine.mu.Unlock()
		if len(h.device.taps) != 0 {
			t.Fatalf("public cycle dispatched input: %v", h.device.taps)
		}
		if delta := h.device.probeCount() - probes; delta != 0 {
			t.Fatalf("probe delta = %d, want zero", delta)
		}
		if delta := h.device.captureCount() - captures; delta != 0 {
			t.Fatalf("capture delta = %d, want zero", delta)
		}
		if counter.calls != 0 {
			t.Fatalf("public cycle entered Preview %d times", counter.calls)
		}
		requireReadyObservation(t, h, start.Observation.ID)
	})

	t.Run("duplicate source reverse mapping", func(t *testing.T) {
		h := newTapScopeHarness(t)
		h.device.scriptCaptures(tapCardStep())
		counter := &tapPreviewCounter{Projector: h.engine.projector}
		h.engine.projector = counter
		start := h.startSession()
		card := tapCardNode(t, h)
		image := tapPublicBoundsNode(currentTree(t, h.engine).Root, tapImageBounds)
		if image == nil {
			t.Fatal("image source node not found")
		}
		h.engine.mu.Lock()
		previous, hadPrevious := h.engine.current.Projection.Source[image.Ref]
		h.engine.current.Projection.Source[image.Ref] =
			h.engine.current.Projection.Source[card.Ref]
		h.engine.mu.Unlock()
		probes, captures := h.device.probeCount(), h.device.captureCount()
		counter.calls = 0

		result := h.actWith(t, "Pdupsource", tapRefRequest(start.Observation.ID, card.Ref))
		requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
		h.engine.mu.Lock()
		if hadPrevious {
			h.engine.current.Projection.Source[image.Ref] = previous
		} else {
			delete(h.engine.current.Projection.Source, image.Ref)
		}
		h.engine.mu.Unlock()
		if len(h.device.taps) != 0 {
			t.Fatalf("duplicate source dispatched input: %v", h.device.taps)
		}
		if delta := h.device.probeCount() - probes; delta != 0 {
			t.Fatalf("probe delta = %d, want zero", delta)
		}
		if delta := h.device.captureCount() - captures; delta != 0 {
			t.Fatalf("capture delta = %d, want zero", delta)
		}
		if counter.calls != 0 {
			t.Fatalf("duplicate source entered Preview %d times", counter.calls)
		}
		requireReadyObservation(t, h, start.Observation.ID)
	})
}

func TestActTapUsesWorkProjectorBeforeEngineProjector(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	base := h.engine.projector
	h.engine.projectorFactory = func() Projector {
		return &testProjector{builder: observation.NewBuilder()}
	}
	h.engine.projector = previewErrorProjector{Projector: base}
	start := h.startSession()
	card := tapCardNode(t, h)

	result := h.actWith(t, "Pworkprojector", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("work projector was not used: outcome = %q error %+v",
			result.Outcome, result.Error)
	}
	requireTapPoint(t, h, tapSafePoint)
}

func TestActTapSessionProjectorPreviewFailureIsHierarchyFailure(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	base := h.engine.projector
	h.engine.projectorFactory = func() Projector {
		return previewErrorProjector{Projector: base}
	}
	start := h.startSession()
	card := tapCardNode(t, h)
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Psessionprojector", tapRefRequest(start.Observation.ID, card.Ref))
	requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
	if len(h.device.taps) != 0 {
		t.Fatalf("preview failure dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 1 {
		t.Fatalf("probe delta = %d, want one", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 1 {
		t.Fatalf("capture delta = %d, want one", delta)
	}
	if result.Observation != nil {
		t.Fatalf("hierarchy failure delivered an observation: %+v", result.Observation)
	}
}

func TestActTapFreshProofFallsBackToEngineProjector(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)

	h.engine.mu.Lock()
	obs := h.engine.current
	h.engine.mu.Unlock()
	operation := &protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: card.Ref},
	}
	_, _, plan, apiErr := staticTapRefCheck(obs, operation)
	if apiErr != nil || plan == nil {
		t.Fatalf("static tap plan failed: plan=%+v error=%+v", plan, apiErr)
	}
	fresh, err := h.device.Capture(context.Background(), obs.Screen)
	if err != nil {
		t.Fatalf("fresh capture: %v", err)
	}
	if _, apiErr = h.engine.revalidateTapTargetFrame(
		context.Background(), nil, nil, obs, &fresh, plan); apiErr != nil {
		t.Fatalf("engine projector fallback rejected the proof: %+v", apiErr)
	}

	engineProjector := h.engine.projector
	h.engine.projector = nil
	_, apiErr = h.engine.revalidateTapTargetFrame(
		context.Background(), nil, nil, obs, &fresh, plan)
	h.engine.projector = engineProjector
	if apiErr == nil || apiErr.Code != protocol.CodeHierarchyFailed {
		t.Fatalf("missing projectors should be a hierarchy failure, got %+v", apiErr)
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("direct proof changed the observation to %q", status)
	}
}

func TestActTapBadPreviewProjectionIsHierarchyFailure(t *testing.T) {
	root := &protocol.Node{Ref: "nroot", Role: protocol.RoleRoot}
	cases := []struct {
		name    string
		preview func(*device.Capture) (*Projection, error)
	}{
		{"nil projection", func(*device.Capture) (*Projection, error) {
			return nil, nil
		}},
		{"missing tree", func(*device.Capture) (*Projection, error) {
			return &Projection{Source: map[string]int{}}, nil
		}},
		{"missing root", func(*device.Capture) (*Projection, error) {
			return &Projection{Tree: &protocol.Tree{}, Source: map[string]int{}}, nil
		}},
		{"missing source", func(*device.Capture) (*Projection, error) {
			return &Projection{Tree: &protocol.Tree{Root: root}}, nil
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h := newTapScopeHarness(t)
			h.device.scriptCaptures(tapCardStep())
			start := h.startSession()
			card := tapCardNode(t, h)
			base := h.engine.projector
			h.engine.projector = tapPreviewFuncProjector{
				Projector: base, preview: test.preview,
			}
			probes, captures := h.device.probeCount(), h.device.captureCount()

			result := h.actWith(t, "Pbadpreview", tapRefRequest(start.Observation.ID, card.Ref))
			h.engine.projector = base
			requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
			if len(h.device.taps) != 0 {
				t.Fatalf("bad preview dispatched input: %v", h.device.taps)
			}
			if delta := h.device.probeCount() - probes; delta != 1 {
				t.Fatalf("probe delta = %d, want one", delta)
			}
			if delta := h.device.captureCount() - captures; delta != 1 {
				t.Fatalf("capture delta = %d, want one", delta)
			}
			if result.Observation != nil {
				t.Fatalf("bad preview delivered a new observation: %+v", result.Observation)
			}
		})
	}
}

func TestActTapPreviewMissingCaptureIsHierarchyFailure(t *testing.T) {
	h := newTapScopeHarness(t)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)
	base := h.engine.projector
	h.engine.projector = tapPreviewFuncProjector{
		Projector: base,
		preview: func(capture *device.Capture) (*Projection, error) {
			projection, err := base.Preview(capture)
			if projection != nil {
				projection.Capture = nil
			}
			return projection, err
		},
	}
	probes, captures := h.device.probeCount(), h.device.captureCount()

	result := h.actWith(t, "Ppreviewnocapture",
		tapRefRequest(start.Observation.ID, card.Ref))
	h.engine.projector = base
	requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
	if len(h.device.taps) != 0 {
		t.Fatalf("preview without capture dispatched input: %v", h.device.taps)
	}
	if delta := h.device.probeCount() - probes; delta != 1 {
		t.Fatalf("probe delta = %d, want one", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 1 {
		t.Fatalf("capture delta = %d, want one", delta)
	}
	if result.Observation != nil {
		t.Fatalf("preview without capture delivered a new observation: %+v", result.Observation)
	}
}

// TestActTapGuidanceViewsOwnNestedDetails is the engine ownership gate: the
// first response, a duplicate P replay and status(P) must not share nested maps.
func TestActTapGuidanceViewsOwnNestedDetails(t *testing.T) {
	h := newTapScopeHarness(t)
	first, card, _, request := actTapContentGuidance(t, h, "Powner")
	wantBounds := []int{tapCardBounds.Left, tapCardBounds.Top, tapCardBounds.Right, tapCardBounds.Bottom}

	candidates, ok := first.Error.Details["candidates"].([]map[string]any)
	if !ok || len(candidates) == 0 {
		t.Fatalf("candidates = %#v, want non-empty slice", first.Error.Details["candidates"])
	}
	candidates[0]["ref"] = "mutated"
	candidates[0]["bounds"].([]int)[0] = 99
	candidates[0]["actions"].([]string)[0] = "mutated"

	status, apiErr := h.engine.Status("agent", "Powner")
	if apiErr != nil {
		t.Fatalf("status rejected: %v", apiErr)
	}
	requireGuidanceCandidate(t, status.Error.Details, card.Ref, candidateRelationAncestor, wantBounds, true)

	replay, apiErr := h.engine.Act(context.Background(), "agent", "Powner", request)
	if apiErr != nil {
		t.Fatalf("duplicate act rejected: %v", apiErr)
	}
	requireGuidanceCandidate(t, replay.Error.Details, card.Ref, candidateRelationAncestor, wantBounds, true)
	replayCandidates := replay.Error.Details["candidates"].([]map[string]any)
	replayCandidates[0]["bounds"].([]int)[1] = 0
	replayCandidates[0]["actions"].([]string)[0] = "polluted"
	requireGuidanceCandidate(t, status.Error.Details, card.Ref, candidateRelationAncestor, wantBounds, true)

	h.engine.mu.Lock()
	stored := h.engine.operations["Powner"].Error
	h.engine.mu.Unlock()
	storedCandidates := stored.Details["candidates"].([]map[string]any)
	if storedCandidates[0]["ref"] != card.Ref ||
		!reflect.DeepEqual(storedCandidates[0]["bounds"], wantBounds) ||
		!reflect.DeepEqual(storedCandidates[0]["actions"], []string{"tap", "long_press"}) {
		t.Fatalf("stored operation details were polluted: %#v", stored.Details)
	}
}

// A guidance response is historical operation evidence. Retiring its source
// observation must not make status(P) or a duplicate P re-acquire or revalidate
// the tree; both views return the complete error details recorded at admission.
func TestActTapGuidanceRetiredObservationReplayIsHistorical(t *testing.T) {
	h := newTapScopeHarness(t)
	first, _, _, request := actTapContentGuidance(t, h, "Pretiredguidance")

	observed, apiErr := h.engine.Observe(context.Background(), "agent",
		"Pguidanceobserve", &protocol.ObserveRequest{Reference: request.Reference})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	if observed.Observation == nil || observed.Observation.ID == request.Reference {
		t.Fatalf("observe did not publish a replacement: %+v", observed.Observation)
	}
	if got := observationStatus(h, request.Reference); got != ObservationRetired {
		t.Fatalf("source observation status = %q, want retired", got)
	}

	probes, captures := h.device.probeCount(), h.device.captureCount()
	taps := len(h.device.taps)

	status, apiErr := h.engine.Status("agent", "Pretiredguidance")
	if apiErr != nil {
		t.Fatalf("retired guidance status rejected: %v", apiErr)
	}
	if status.Error == nil || status.Error.Code != first.Error.Code {
		t.Fatalf("status error = %+v, want code %s", status.Error, first.Error.Code)
	}
	if !reflect.DeepEqual(status.Error.Details, first.Error.Details) {
		t.Fatalf("status details = %#v, want original %#v",
			status.Error.Details, first.Error.Details)
	}

	replay, apiErr := h.engine.Act(context.Background(), "agent",
		"Pretiredguidance", request)
	if apiErr != nil {
		t.Fatalf("duplicate retired guidance rejected: %v", apiErr)
	}
	requireNotExecuted(t, replay, first.Error.Code)
	if !reflect.DeepEqual(replay.Error.Details, first.Error.Details) {
		t.Fatalf("duplicate details = %#v, want original %#v",
			replay.Error.Details, first.Error.Details)
	}

	if delta := h.device.probeCount() - probes; delta != 0 {
		t.Fatalf("retired guidance replay probe delta = %d, want zero", delta)
	}
	if delta := h.device.captureCount() - captures; delta != 0 {
		t.Fatalf("retired guidance replay capture delta = %d, want zero", delta)
	}
	if delta := len(h.device.taps) - taps; delta != 0 {
		t.Fatalf("retired guidance replay tap delta = %d, want zero", delta)
	}
	if got := observationStatus(h, request.Reference); got != ObservationRetired {
		t.Fatalf("source observation status after replay = %q, want retired", got)
	}
}

func requireNoLegacyTapDetailKeys(t *testing.T, details map[string]any) {
	t.Helper()
	for _, key := range []string{
		"nearest_actionable_ref", "safe_point", "safe_carrier_ref",
		"suggested_point", "suggested_carrier_ref",
		"occluding_refs", "occluding_ref", "ambiguous_refs", "owner_refs",
	} {
		if value, exists := details[key]; exists {
			t.Fatalf("legacy key %q survived with %#v in %#v", key, value, details)
		}
	}
}

func requireGuidanceCandidate(t *testing.T, details map[string]any,
	wantRef, wantRelation string, wantBounds []int, wantExecutable bool) {
	t.Helper()
	if details == nil {
		t.Fatal("missing guidance details")
	}
	requireNoLegacyTapDetailKeys(t, details)
	candidates, ok := details["candidates"].([]map[string]any)
	if !ok || len(candidates) == 0 {
		t.Fatalf("candidates = %#v, want non-empty []map[string]any", details["candidates"])
	}
	first := candidates[0]
	if first["ref"] != wantRef || first["relation"] != wantRelation ||
		first["executable"] != wantExecutable {
		t.Fatalf("candidates[0] = %#v, want ref=%q relation=%q executable=%t",
			first, wantRef, wantRelation, wantExecutable)
	}
	if !reflect.DeepEqual(first["bounds"], wantBounds) {
		t.Fatalf("candidates[0].bounds = %#v, want %#v", first["bounds"], wantBounds)
	}
	if actions, ok := first["actions"].([]string); !ok || len(actions) == 0 {
		t.Fatalf("candidates[0].actions = %#v, want non-empty []string", first["actions"])
	}
}

func TestActTapPlanLogsOnlyWhitelistedFacts(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newTapScopeHarnessWithLogger(t, logger)
	h.device.scriptCaptures(tapCardStep())
	start := h.startSession()
	card := tapCardNode(t, h)

	result := h.actWith(t, "Plog", tapRefRequest(start.Observation.ID, card.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q error %+v", result.Outcome, result.Error)
	}
	logger.Close()
	recorded := buffer.String()
	for _, want := range []string{
		"act.tap_plan", `action="tap"`, `mode="carrier_node"`, "proven=true",
	} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("event log lacks %q:\n%s", want, recorded)
		}
	}
	for _, forbidden := range []string{"x=", "y=", "ref=", "safe_point", "613", "501"} {
		if strings.Contains(recorded, forbidden) {
			t.Fatalf("event log carries %q:\n%s", forbidden, recorded)
		}
	}
}
