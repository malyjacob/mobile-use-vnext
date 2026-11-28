package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// This file states the #36 target-proof criteria of §4.2/§4.3/§5.2 as
// executable expectations: the anti-rebuild Tier 4 for ref targets, the
// changed_ref attribution, and the layered coordinate/bare-swipe proofs.

// TestActTargetLogRecordsProvenTier proves the #36 log fact: one proven ref
// target records the bounded integer level that actually matched.
func TestActTargetLogRecordsProvenTier(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	display := targetProofDisplay()
	original := targetProofPanelPage(display, "old_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", "Continue"))
	fresh := targetProofPanelPage(display, "new_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", "Continue"))
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := proposalFindLabel(t, currentTree(t, h.engine).Root, "Continue")
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return fresh
	}))
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireExecuted(t, result)
	logger.Close()
	logged := buffer.String()
	if !strings.Contains(logged, "act.target") {
		t.Fatalf("no act.target event was recorded: %s", logged)
	}
	if !strings.Contains(logged, "tier=4") {
		t.Fatalf("act.target did not record the proving Tier 4: %s", logged)
	}
	if strings.Contains(logged, "Continue") || strings.Contains(logged, target.Ref) {
		t.Fatalf("act.target leaked a ref or label: %s", logged)
	}
}

func targetProofDisplay() device.Display {
	return device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
}

// targetProofPanelPage builds root -> panel(resource) -> target. The panel is
// the rebuilt ancestor; the target keeps its own bounds and content.
func targetProofPanelPage(display device.Display, panelResource string,
	target device.RawNode) device.Capture {
	root := revalRoot(display.ID, 1)
	panelBounds := device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000}
	panel := proposalNode(1, 0, 1, display, panelBounds,
		"android.widget.FrameLayout", "")
	panel.ResourceID = panelResource
	panel.Children = []int{2}
	target.Index, target.Parent, target.Depth = 2, 1, 2
	return revalCapture(display, root, panel, target)
}

// targetProofCardPage builds root -> panel(resource) -> card with readable
// non-interactive descendants. The card carries no text, description or
// resource of its own, so only the subtree fingerprint (E3) identifies it.
func targetProofCardPage(display device.Display, panelResource,
	title, count string) device.Capture {
	root := revalRoot(display.ID, 1)
	panel := proposalNode(1, 0, 1, display,
		device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000},
		"android.widget.FrameLayout", "")
	panel.ResourceID = panelResource
	panel.Children = []int{2}
	card := proposalClickableNode(2, 1, 2, display,
		device.Rect{Left: 100, Top: 300, Right: 980, Bottom: 900},
		"android.widget.FrameLayout", "")
	card.Children = []int{3, 4}
	titleNode := proposalNode(3, 2, 3, display,
		device.Rect{Left: 140, Top: 340, Right: 940, Bottom: 480},
		"android.widget.TextView", title)
	countNode := proposalNode(4, 2, 3, display,
		device.Rect{Left: 140, Top: 520, Right: 940, Bottom: 560},
		"android.widget.TextView", count)
	return revalCapture(display, root, panel, card, titleNode, countNode)
}

// targetProofAnonymousButton is a tap target with no text, description,
// resource or readable subtree: E1, E2 and E3 all fail.
func targetProofAnonymousButton(display device.Display) device.RawNode {
	return proposalClickableNode(2, 1, 2, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "")
}

// targetProofNodeByBounds finds the single public node whose bounds equal the
// requested rectangle.
func targetProofNodeByBounds(t *testing.T, root *protocol.Node,
	bounds device.Rect) *protocol.Node {
	t.Helper()
	var found []*protocol.Node
	visited := map[*protocol.Node]struct{}{}
	var walk func(node *protocol.Node)
	walk = func(node *protocol.Node) {
		if node == nil {
			return
		}
		if _, seen := visited[node]; seen {
			return
		}
		visited[node] = struct{}{}
		if node.Bounds != nil && rectFromBounds(node.Bounds) == bounds &&
			!node.Context && node.Role != protocol.RoleRoot {
			found = append(found, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	if len(found) != 1 {
		t.Fatalf("public nodes with bounds %v = %d, want exactly one",
			bounds, len(found))
	}
	return found[0]
}

func targetProofRequireDetailsString(t *testing.T, result *protocol.Result,
	key, want string) {
	t.Helper()
	if result == nil || result.Error == nil {
		t.Fatalf("result carries no error: %+v", result)
	}
	if got := result.Error.Details[key]; got != want {
		t.Fatalf("details.%s = %#v, want %q (details %#v)",
			key, got, want, result.Error.Details)
	}
}

// ---------------------------------------------------------------- Tier 4 ref

// TestTier4AcceptsRebuiltAncestorWithOwnText proves E1: a rebuilt ancestor plus
// a two-pixel jitter no longer rejects a target that carries its own text.
func TestTier4AcceptsRebuiltAncestorWithOwnText(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofPanelPage(display, "old_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", "Continue"))
	fresh := targetProofPanelPage(display, "new_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 201, Top: 402, Right: 801, Bottom: 602},
			"android.widget.Button", "Continue"))

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := proposalFindLabel(t, currentTree(t, h.engine).Root, "Continue")
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireExecuted(t, result)
	proposalRequireTap(t, h, [2]int{500, 500})
}

// TestTier4AcceptsUniqueResourceTarget proves E2: a target without text but with
// a resource identity that is unique in both complete trees.
func TestTier4AcceptsUniqueResourceTarget(t *testing.T) {
	display := targetProofDisplay()
	bounds := device.Rect{Left: 120, Top: 320, Right: 960, Bottom: 420}
	row := proposalClickableNode(2, 1, 2, display, bounds,
		"android.widget.FrameLayout", "")
	row.ResourceID = "play_row"
	original := targetProofPanelPage(display, "old_panel", row)
	freshRow := proposalClickableNode(2, 1, 2, display, bounds,
		"android.widget.FrameLayout", "")
	freshRow.ResourceID = "play_row"
	fresh := targetProofPanelPage(display, "new_panel", freshRow)

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := mustFindNodeByTextOrResource(t,
		currentTree(t, h.engine).Root, "play_row")
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireExecuted(t, result)
	proposalRequireTap(t, h, [2]int{540, 370})
}

// TestTier4AcceptsReadableSubtreeTarget proves E3: a card without its own text
// is identified by the equal non-empty readable multiset of its descendants.
func TestTier4AcceptsReadableSubtreeTarget(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofCardPage(display, "old_panel", "playlist", "12 songs")
	fresh := targetProofCardPage(display, "new_panel", "playlist", "12 songs")

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := targetProofNodeByBounds(t, currentTree(t, h.engine).Root,
		device.Rect{Left: 100, Top: 300, Right: 980, Bottom: 900})
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireExecuted(t, result)
}

// TestTier4RejectsWithoutSemanticEvidence is the negative counterpart: a rebuilt
// ancestor and a target with no E1/E2/E3 evidence must stay rejected and name
// the changed public ancestor.
func TestTier4RejectsWithoutSemanticEvidence(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofPanelPage(display, "old_panel",
		targetProofAnonymousButton(display))
	fresh := targetProofPanelPage(display, "new_panel",
		targetProofAnonymousButton(display))

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	root := currentTree(t, h.engine).Root
	target := targetProofNodeByBounds(t, root,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600})
	panel := targetProofNodeByBounds(t, root,
		device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000})
	h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
		fresh.Display = display
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireTargetChanged(t, result, "lineage_changed")
	targetProofRequireDetailsString(t, result, "changed_ref", panel.Ref)
	proposalRequireNoDispatch(t, h)
}

// TestTier4InsertionOnlyWrapperOmitsChangedRef proves the strict attribution:
// when the old ancestors are all still uniquely present behind a newly inserted
// opaque wrapper, no old public ancestor is blamed and changed_ref is omitted.
func TestTier4InsertionOnlyWrapperOmitsChangedRef(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofPanelPage(display, "old_panel",
		targetProofAnonymousButton(display))
	root := revalRoot(display.ID, 1)
	panel := proposalNode(1, 0, 1, display,
		device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000},
		"android.widget.FrameLayout", "")
	panel.ResourceID = "old_panel"
	panel.Children = []int{2}
	wrapper := proposalNode(2, 1, 2, display,
		device.Rect{Left: 60, Top: 210, Right: 1020, Bottom: 990},
		"android.widget.FrameLayout", "")
	wrapper.ResourceID = "new_wrap"
	wrapper.Children = []int{3}
	button := proposalClickableNode(3, 2, 3, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "")
	fresh := revalCapture(display, root, panel, wrapper, button)

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := targetProofNodeByBounds(t, currentTree(t, h.engine).Root,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600})
	h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
		fresh.Display = display
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireTargetChanged(t, result, "lineage_changed")
	if result.Error == nil || result.Error.Details == nil {
		t.Fatalf("lineage_changed carries no details: %+v", result.Error)
	}
	if changed, exists := result.Error.Details["changed_ref"]; exists {
		t.Fatalf("insertion-only wrapper blamed %#v, want changed_ref omitted", changed)
	}
	proposalRequireNoDispatch(t, h)
}

// TestTier4RejectsAmbiguousRebuild proves the bidirectional uniqueness gate: two
// rebuilt candidates with the same evidence are ambiguous, never a guess.
func TestTier4RejectsAmbiguousRebuild(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofPanelPage(display, "old_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", "Continue"))
	root := revalRoot(display.ID, 1)
	panel := proposalNode(1, 0, 1, display,
		device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000},
		"android.widget.FrameLayout", "")
	panel.ResourceID = "new_panel"
	panel.Children = []int{2, 3}
	first := proposalClickableNode(2, 1, 2, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "Continue")
	second := proposalClickableNode(3, 1, 2, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "Continue")
	fresh := revalCapture(display, root, panel, first, second)

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := proposalFindLabel(t, currentTree(t, h.engine).Root, "Continue")
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireTargetChanged(t, result, "target_ambiguous")
	proposalRequireNoDispatch(t, h)
}

// TestTier4RejectsMovedTargetBeyondCoverage proves the bidirectional 80%
// coverage gate: a target that moved out of its old rectangle is not Tier 4.
// The label contains a digit, so it does not provide a container rebind anchor.
func TestTier4RejectsMovedTargetBeyondCoverage(t *testing.T) {
	display := targetProofDisplay()
	const label = "Continue 5"
	original := targetProofPanelPage(display, "old_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", label))
	fresh := targetProofPanelPage(display, "new_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 460, Top: 400, Right: 1060, Bottom: 600},
			"android.widget.Button", label))

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := proposalFindLabel(t, currentTree(t, h.engine).Root, label)
	h.device.scriptCaptures(proposalCaptureStep(func(display device.Display) device.Capture {
		fresh.Display = display
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireTargetChanged(t, result, "lineage_changed")
	proposalRequireNoDispatch(t, h)
}

// TestTier4RejectsExternalOverlayOnLandingPoint proves that Tier 4 keeps the
// whole-tree external-owner gate: a sibling overlay covering the target center
// blocks the frame even though the target itself was proven.
func TestTier4RejectsExternalOverlayOnLandingPoint(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofPanelPage(display, "old_panel",
		proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", "Continue"))
	root := revalRoot(display.ID, 1)
	panel := proposalNode(1, 0, 1, display,
		device.Rect{Left: 50, Top: 200, Right: 1030, Bottom: 1000},
		"android.widget.FrameLayout", "")
	panel.ResourceID = "new_panel"
	panel.Children = []int{2, 3}
	button := proposalClickableNode(2, 1, 2, display,
		device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
		"android.widget.Button", "Continue")
	overlay := proposalClickableNode(3, 1, 2, display,
		device.Rect{Left: 400, Top: 400, Right: 600, Bottom: 600},
		"android.widget.FrameLayout", "ad-overlay")
	fresh := revalCapture(display, root, panel, button, overlay)

	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return original
	}))
	start := h.startSession()
	target := proposalFindLabel(t, currentTree(t, h.engine).Root, "Continue")
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return fresh
	}))

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: target.Ref},
	})
	proposalRequireTargetChanged(t, result, "scope_blocked")
	proposalRequireNoDispatch(t, h)
}

// ---------------------------------------------------- coordinate and swipe

func targetProofSwipePage(display device.Display, label string,
	extra ...device.RawNode) device.Capture {
	root := revalRoot(display.ID, 1)
	container := proposalScrollNode(1, 0, 1, display,
		device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 900},
		"android.widget.ScrollView")
	labelNode := proposalNode(2, 1, 2, display,
		device.Rect{Left: 400, Top: 500, Right: 600, Bottom: 560},
		"android.widget.TextView", label)
	nodes := []device.RawNode{root, container, labelNode}
	children := []int{2}
	for _, node := range extra {
		node.Index = len(nodes)
		node.Parent = 1
		node.Depth = 2
		children = append(children, node.Index)
		nodes = append(nodes, node)
	}
	nodes[1].Children = children
	return revalCapture(display, nodes...)
}

func targetProofSwipe(t *testing.T, h *harness,
	observationID string) *protocol.Result {
	t.Helper()
	return h.actP(t, "", observationID, protocol.Operation{
		Type:       "swipe",
		From:       &protocol.Point{X: 500, Y: 530},
		To:         &protocol.Point{X: 500, Y: 900},
		DurationMS: 300,
	})
}

// TestBareSwipeIgnoresPassiveTextJump is the #36 regression: a seconds-ticking
// passive text at the start point no longer rejects a bare swipe.
func TestBareSwipeIgnoresPassiveTextJump(t *testing.T) {
	display := targetProofDisplay()
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofSwipePage(display, "11.0")
	}))
	start := h.startSession()
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofSwipePage(display, "46.0")
	}))

	result := targetProofSwipe(t, h, start.Observation.ID)
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, h, swipeCall{
		displayID: 12, x1: 500, y1: 530, x2: 500, y2: 900, durationMS: 300,
	})
}

// TestBareSwipeMatchesIdenticalSiblingLeaves proves the bounded bipartite
// matching: two identical passive leaves on the same point are matched 1-to-1
// instead of being rejected by a per-edge uniqueness rule.
func TestBareSwipeMatchesIdenticalSiblingLeaves(t *testing.T) {
	display := targetProofDisplay()
	page := func() device.Capture {
		return targetProofSwipePage(display, "tick",
			proposalNode(0, 0, 0, display,
				device.Rect{Left: 400, Top: 500, Right: 600, Bottom: 560},
				"android.widget.TextView", "tick"))
	}
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return page()
	}))
	start := h.startSession()
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return page()
	}))

	result := targetProofSwipe(t, h, start.Observation.ID)
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, h, swipeCall{
		displayID: 12, x1: 500, y1: 530, x2: 500, y2: 900, durationMS: 300,
	})
}

// TestBareSwipeRejectsNewDeepOverlay proves the anti-occlusion rule: a new
// interactive node on the start point blocks the swipe.
func TestBareSwipeRejectsNewDeepOverlay(t *testing.T) {
	display := targetProofDisplay()
	overlay := proposalClickableNode(0, 0, 0, display,
		device.Rect{Left: 300, Top: 400, Right: 700, Bottom: 700},
		"android.widget.FrameLayout", "ad-overlay")
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofSwipePage(display, "11.0")
	}))
	start := h.startSession()
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofSwipePage(display, "11.0", overlay)
	}))

	result := targetProofSwipe(t, h, start.Observation.ID)
	proposalRequireTargetChanged(t, result, "point_changed")
	proposalRequireNoDispatch(t, h)
}

// TestBareSwipeRejectsChangedStructure proves that a structural change at the
// start point is still rejected: only passive text and wrappers are ignored.
func TestBareSwipeRejectsChangedStructure(t *testing.T) {
	display := targetProofDisplay()
	changed := func() device.Capture {
		page := targetProofSwipePage(display, "tick")
		page.Nodes[2].ClassName = "android.widget.Button"
		page.Nodes[2].HasClickable, page.Nodes[2].Clickable = true, true
		return page
	}
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofSwipePage(display, "tick")
	}))
	start := h.startSession()
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return changed()
	}))

	result := targetProofSwipe(t, h, start.Observation.ID)
	proposalRequireTargetChanged(t, result, "point_changed")
	proposalRequireNoDispatch(t, h)
}

// TestCoordinateTapSurvivesAncestorRebuild proves the coordinate tier-B
// fallback: a rebuilt ancestor with an unchanged target still executes.
func TestCoordinateTapSurvivesAncestorRebuild(t *testing.T) {
	display := targetProofDisplay()
	button := func() device.RawNode {
		return proposalClickableNode(2, 1, 2, display,
			device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
			"android.widget.Button", "确认")
	}
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofPanelPage(display, "old_panel", button())
	}))
	start := h.startSession()
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofPanelPage(display, "new_panel", button())
	}))

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 500},
	})
	proposalRequireExecuted(t, result)
	proposalRequireTap(t, h, [2]int{500, 500})
}

// TestCoordinateTapRejectsChangedLabelAfterRebuild is the strict counterpart:
// the target's own text is never relaxed, even after an ancestor rebuild.
func TestCoordinateTapRejectsChangedLabelAfterRebuild(t *testing.T) {
	display := targetProofDisplay()
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofPanelPage(display, "old_panel",
			proposalClickableNode(2, 1, 2, display,
				device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
				"android.widget.Button", "确认"))
	}))
	start := h.startSession()
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return targetProofPanelPage(display, "new_panel",
			proposalClickableNode(2, 1, 2, display,
				device.Rect{Left: 200, Top: 400, Right: 800, Bottom: 600},
				"android.widget.Button", "取消"))
	}))

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 500},
	})
	proposalRequireTargetChanged(t, result, "point_changed")
	proposalRequireNoDispatch(t, h)
}

// TestCoordinateEmptyPointStaysEmpty proves that a blank point is only still
// authorized while it stays blank: a newly covering node rejects the input.
func TestCoordinateEmptyPointStaysEmpty(t *testing.T) {
	display := targetProofDisplay()
	elsewhere := proposalClickableNode(1, 0, 1, display,
		device.Rect{Left: 100, Top: 600, Right: 900, Bottom: 900},
		"android.widget.Button", "far-away")
	h := newHarness(t)
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return revalCapture(display, revalRoot(display.ID, 1), elsewhere)
	}))
	start := h.startSession()
	cover := proposalClickableNode(1, 0, 1, display,
		device.Rect{Left: 0, Top: 0, Right: 300, Bottom: 700},
		"android.widget.FrameLayout", "banner")
	h.device.scriptCaptures(proposalCaptureStep(func(device.Display) device.Capture {
		return revalCapture(display, revalRoot(display.ID, 1), cover)
	}))

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 10, Y: 500},
	})
	proposalRequireTargetChanged(t, result, "point_changed")
	proposalRequireNoDispatch(t, h)
}

// ---------------------------------------------------- write-submit policy

func targetProofSubmitPage(display device.Display, enabled bool,
	label string) device.Capture {
	bounds := device.Rect{Left: 200, Top: 1600, Right: 880, Bottom: 1700}
	root := proposalNode(0, -1, 0, display,
		device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400},
		"android.widget.FrameLayout", "")
	root.Children = []int{1}
	panel := proposalNode(1, 0, 1, display,
		device.Rect{Left: 0, Top: 1500, Right: 1080, Bottom: 1800},
		"android.widget.FrameLayout", "")
	panel.Children = []int{2}
	button := proposalClickableNode(2, 1, 2, display, bounds,
		"android.widget.Button", label)
	button.ResourceID = "btn_search"
	button.HasEnabled, button.Enabled = true, enabled
	return revalCapture(display, root, panel, button)
}

func targetProofSubmitPlan(t *testing.T, projector Projector,
	original *device.Capture) (*tapPlan, *observationState) {
	t.Helper()
	projection, err := projector.Build(original)
	if err != nil {
		t.Fatalf("projection build failed: %v", err)
	}
	ref := ""
	for candidateRef, index := range projection.Source {
		if index == 2 {
			ref = candidateRef
			break
		}
	}
	if ref == "" {
		t.Fatalf("target raw index has no public ref")
	}
	return &tapPlan{
		x: 540, y: 1650, strategy: tapModeGrid3x3, action: "tap",
		targetRef: ref, targetRaw: &original.Nodes[2],
	}, &observationState{Projection: projection}
}

func targetProofRequireSubmitReason(t *testing.T, apiErr *protocol.APIError,
	want string) {
	t.Helper()
	if apiErr == nil {
		t.Fatalf("missing submit revalidation error")
	}
	if apiErr.Code != protocol.CodeTargetChanged {
		t.Fatalf("error code = %s, want target_changed", apiErr.Code)
	}
	if got := apiErr.Details["reason"]; got != want {
		t.Fatalf("submit reason = %#v, want %q (details %#v)",
			got, want, apiErr.Details)
	}
}

func TestSubmitTapFrameAcceptsEnabledTransition(t *testing.T) {
	display := targetProofDisplay()
	projector := &testProjector{builder: observation.NewBuilder()}
	original := targetProofSubmitPage(display, false, "")
	fresh := targetProofSubmitPage(display, true, "")
	plan, obs := targetProofSubmitPlan(t, projector, &original)

	frame, apiErr := (&Engine{}).revalidateSubmitTapFrame(context.Background(),
		nil, projector, obs, &fresh, plan, true)
	if apiErr != nil || frame == nil {
		t.Fatalf("disabled -> enabled submit frame rejected: %+v", apiErr)
	}
	if frame.x != 540 || frame.y != 1650 {
		t.Fatalf("submit frame point = (%d,%d), want (540,1650)", frame.x, frame.y)
	}
}

func TestSubmitTapFrameRejectsStillDisabled(t *testing.T) {
	display := targetProofDisplay()
	projector := &testProjector{builder: observation.NewBuilder()}
	original := targetProofSubmitPage(display, false, "")
	fresh := targetProofSubmitPage(display, false, "")
	plan, obs := targetProofSubmitPlan(t, projector, &original)

	frame, apiErr := (&Engine{}).revalidateSubmitTapFrame(context.Background(),
		nil, projector, obs, &fresh, plan, true)
	if frame != nil || apiErr == nil {
		t.Fatalf("still-disabled submit frame accepted: frame=%+v err=%+v",
			frame, apiErr)
	}
	targetProofRequireSubmitReason(t, apiErr, submitFrameReasonDisabled)
}

func TestSubmitTapFrameRejectsMissingEnabledFlag(t *testing.T) {
	display := targetProofDisplay()
	projector := &testProjector{builder: observation.NewBuilder()}
	original := targetProofSubmitPage(display, false, "")
	fresh := targetProofSubmitPage(display, true, "")
	fresh.Nodes[2].HasEnabled = false
	plan, obs := targetProofSubmitPlan(t, projector, &original)

	frame, apiErr := (&Engine{}).revalidateSubmitTapFrame(context.Background(),
		nil, projector, obs, &fresh, plan, true)
	if frame != nil || apiErr == nil {
		t.Fatalf("missing enabled flag accepted: frame=%+v err=%+v", frame, apiErr)
	}
	targetProofRequireSubmitReason(t, apiErr, submitFrameReasonDisabled)
}

func TestSubmitTapFrameRejectsEnabledToDisabled(t *testing.T) {
	display := targetProofDisplay()
	projector := &testProjector{builder: observation.NewBuilder()}
	original := targetProofSubmitPage(display, true, "")
	fresh := targetProofSubmitPage(display, false, "")
	plan, obs := targetProofSubmitPlan(t, projector, &original)

	frame, apiErr := (&Engine{}).revalidateSubmitTapFrame(context.Background(),
		nil, projector, obs, &fresh, plan, false)
	if frame != nil || apiErr == nil {
		t.Fatalf("enabled -> disabled submit frame accepted: frame=%+v err=%+v",
			frame, apiErr)
	}
	targetProofRequireSubmitReason(t, apiErr, submitFrameReasonDisabled)
}

func TestMatchKeyboardShiftedSubmitNode(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofSubmitPage(display, true, "搜索")
	shifted := targetProofSubmitPage(display, true, "搜索")
	shifted.Nodes[2].Bounds = device.Rect{Left: 200, Top: 800, Right: 880, Bottom: 900}
	shifted.Nodes[2].RawBounds = shifted.Nodes[2].Bounds

	match, state := matchKeyboardShiftedSubmitNode(context.Background(), nil,
		&original, &shifted, &original.Nodes[2], false, defaultActMatchLimits())
	if state != actProbeUnique || match.tier != 4 {
		t.Fatalf("keyboard-shifted match state=%v tier=%d, want unique Tier 4",
			state, match.tier)
	}
	if indexOf(&shifted, match.raw) != 2 {
		t.Fatalf("keyboard-shifted candidate = %d, want 2",
			indexOf(&shifted, match.raw))
	}

	horizontal := targetProofSubmitPage(display, true, "搜索")
	horizontal.Nodes[2].Bounds = device.Rect{Left: 520, Top: 1600, Right: 1200, Bottom: 1700}
	horizontal.Nodes[2].RawBounds = horizontal.Nodes[2].Bounds
	if _, state := matchKeyboardShiftedSubmitNode(context.Background(), nil,
		&original, &horizontal, &original.Nodes[2], false,
		defaultActMatchLimits()); state != actProbeNone {
		t.Fatalf("horizontal shift state = %v, want none", state)
	}

	ambiguous := targetProofSubmitPage(display, true, "搜索")
	duplicate := proposalClickableNode(3, 1, 2, display,
		device.Rect{Left: 200, Top: 800, Right: 880, Bottom: 900},
		"android.widget.Button", "搜索")
	duplicate.ResourceID = "btn_search"
	ambiguous = revalCapture(display, ambiguous.Nodes[0], ambiguous.Nodes[1],
		shifted.Nodes[2], duplicate)
	ambiguous.Nodes[1].Children = []int{2, 3}
	if _, state := matchKeyboardShiftedSubmitNode(context.Background(), nil,
		&original, &ambiguous, &original.Nodes[2], false,
		defaultActMatchLimits()); state != actProbeAmbiguous {
		t.Fatalf("two shifted candidates state = %v, want ambiguous", state)
	}
}

func TestTapPlanExecutionRaw(t *testing.T) {
	direct := proposalNode(1, 0, 1, targetProofDisplay(),
		device.Rect{Left: 0, Top: 0, Right: 10, Bottom: 10},
		"android.widget.Button", "")
	ancestor := direct
	if got := tapPlanExecutionRaw(&tapPlan{targetRaw: &direct}); got != &direct {
		t.Fatalf("direct execution raw = %p, want %p", got, &direct)
	}
	if got := tapPlanExecutionRaw(&tapPlan{targetRaw: &direct,
		bubbleAncestorRaw: &ancestor}); got != &ancestor {
		t.Fatalf("bubble execution raw = %p, want %p", got, &ancestor)
	}
	if got := tapPlanExecutionRaw(&tapPlan{targetRaw: &direct,
		delegateRaw: &ancestor}); got != &ancestor {
		t.Fatalf("coextensive execution raw = %p, want %p", got, &ancestor)
	}
	if got := tapPlanExecutionRaw(&tapPlan{targetRaw: &direct,
		colocatedRaw: &ancestor}); got != &ancestor {
		t.Fatalf("colocated execution raw = %p, want %p", got, &ancestor)
	}
	if got := tapPlanExecutionRaw(nil); got != nil {
		t.Fatalf("nil plan execution raw = %p, want nil", got)
	}
}

func TestSubmitExecutionDisabledOnFresh(t *testing.T) {
	display := targetProofDisplay()
	original := targetProofSubmitPage(display, true, "")
	want := &original.Nodes[2]
	one := targetProofSubmitPage(display, false, "")
	if !submitExecutionDisabledOnFresh(&one, want) {
		t.Fatalf("one disabled matching node must attribute submit_disabled")
	}
	two := targetProofSubmitPage(display, false, "")
	duplicate := proposalClickableNode(3, 1, 2, display,
		device.Rect{Left: 200, Top: 1600, Right: 880, Bottom: 1700},
		"android.widget.Button", "")
	duplicate.ResourceID = "btn_search"
	duplicate.HasEnabled, duplicate.Enabled = true, false
	two = revalCapture(display, two.Nodes[0], two.Nodes[1], two.Nodes[2], duplicate)
	two.Nodes[1].Children = []int{2, 3}
	if submitExecutionDisabledOnFresh(&two, want) {
		t.Fatalf("two disabled candidates must stay unattributable")
	}
	enabled := targetProofSubmitPage(display, true, "")
	if submitExecutionDisabledOnFresh(&enabled, want) {
		t.Fatalf("enabled fresh target attributed as disabled")
	}
}

// ------------------------------------------------------------- pure policy

func TestSameStructureForSubmitEnabledPolicy(t *testing.T) {
	display := targetProofDisplay()
	bounds := device.Rect{Left: 100, Top: 100, Right: 200, Bottom: 200}
	cases := []struct {
		name             string
		originalEnabled  tri
		freshEnabled     tri
		plainCompatible  bool
		submitCompatible bool
	}{
		{"enabled_to_enabled", tri{true, true}, tri{true, true}, true, true},
		{"disabled_to_disabled", tri{true, false}, tri{true, false}, true, true},
		{"disabled_to_enabled", tri{true, false}, tri{true, true}, false, true},
		{"enabled_to_disabled", tri{true, true}, tri{true, false}, false, false},
		{"disabled_to_unknown", tri{true, false}, tri{false, false}, false, true},
		{"unknown_to_enabled", tri{false, false}, tri{true, true}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := proposalNode(1, 0, 1, display, bounds,
				"android.widget.Button", "")
			original.HasEnabled, original.Enabled = tc.originalEnabled.known,
				tc.originalEnabled.value
			fresh := proposalNode(1, 0, 1, display, bounds,
				"android.widget.Button", "")
			fresh.HasEnabled, fresh.Enabled = tc.freshEnabled.known,
				tc.freshEnabled.value
			if compatible, _ := sameStructure(&original, &fresh); compatible != tc.plainCompatible {
				t.Fatalf("plain compatible = %v, want %v", compatible,
					tc.plainCompatible)
			}
			if compatible, _ := sameStructureForSubmit(&original, &fresh); compatible != tc.submitCompatible {
				t.Fatalf("submit compatible = %v, want %v", compatible,
					tc.submitCompatible)
			}
		})
	}
}

func TestTightGeometryCompatibleBoundaries(t *testing.T) {
	screen := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
	cases := []struct {
		name string
		a, b device.Rect
		want bool
	}{
		{"exact", device.Rect{Left: 100, Top: 100, Right: 300, Bottom: 300},
			device.Rect{Left: 100, Top: 100, Right: 300, Bottom: 300}, true},
		{"eighty_percent_each", device.Rect{Left: 0, Top: 0, Right: 100, Bottom: 100},
			device.Rect{Left: 20, Top: 0, Right: 120, Bottom: 100}, true},
		{"seventy_nine_percent", device.Rect{Left: 0, Top: 0, Right: 100, Bottom: 100},
			device.Rect{Left: 21, Top: 0, Right: 121, Bottom: 100}, false},
		{"large_container_swallow", device.Rect{Left: 100, Top: 100, Right: 200, Bottom: 200},
			device.Rect{Left: 0, Top: 0, Right: 400, Bottom: 400}, false},
		{"micro_jitter", device.Rect{Left: 100, Top: 100, Right: 300, Bottom: 300},
			device.Rect{Left: 102, Top: 98, Right: 302, Bottom: 298}, true},
		{"off_screen", device.Rect{Left: 100, Top: 100, Right: 300, Bottom: 300},
			device.Rect{Left: 1000, Top: 100, Right: 1200, Bottom: 300}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tightGeometryCompatible(tc.a, tc.b, screen); got != tc.want {
				t.Fatalf("tightGeometryCompatible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMaxBipartiteMatching(t *testing.T) {
	cases := []struct {
		name  string
		edges [][]bool
		want  int
	}{
		{"empty", nil, 0},
		{"complete_two", [][]bool{{true, true}, {true, true}}, 2},
		{"single_column", [][]bool{{true}, {true}}, 1},
		{"chain", [][]bool{{true, false}, {true, true}}, 2},
		{"no_edges", [][]bool{{false, false}, {false, false}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxBipartiteMatching(tc.edges); got != tc.want {
				t.Fatalf("maxBipartiteMatching = %d, want %d", got, tc.want)
			}
		})
	}
}
