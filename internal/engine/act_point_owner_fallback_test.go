package engine

// This file states §4.5 (coordinate tap/long_press owner conservation) and the
// §8.1 row 5 matrix of the act revalidation resilience proposal as executable
// expectations. Every case builds one pointProof directly from synthetic
// captures plus a projection/observation pair: no engine, no session, no device.

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func pointFallbackDisplay() device.Display {
	return device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
}

func pointFallbackRoot(display device.Display, children ...int) device.RawNode {
	return device.RawNode{
		Index: 0, Parent: -1, Children: children, Depth: 0, DisplayID: display.ID,
		Visible: true, RawBounds: display.Bounds(), Bounds: display.Bounds(),
		Package: "com.example", ClassName: "android.widget.FrameLayout",
		TextSet: true, DescSet: true, ResourceSet: true,
		HasEnabled: true, Enabled: true,
	}
}

// pointFallbackNode is one visible source node with known-safe default state.
// The caller adds text and interaction capabilities.
func pointFallbackNode(index, parent, depth int, display device.Display,
	bounds device.Rect, class, text string) device.RawNode {
	return device.RawNode{
		Index: index, Parent: parent, Depth: depth, DisplayID: display.ID,
		Visible: true, RawBounds: bounds, Bounds: bounds,
		Package: "com.example", ClassName: class,
		Text: text, TextSet: true,
		DescSet: true, ResourceSet: true,
		HasEnabled: true, Enabled: true,
		HasClickable: true, Clickable: false,
		HasLongClick: true, LongClickable: false,
		HasScrollable: true, Scrollable: false,
		HasPassword: true, Password: false,
		HasCheckable: true, Checkable: false,
		HasFocused: true, Focused: false,
		HasSelected: true, Selected: false,
		HasChecked: true, Checked: false,
	}
}

// pointFallbackInteractive carries both tap and long_press, like a real card
// row, so one owner can serve both coordinate operations of §4.5.
func pointFallbackInteractive(index, parent, depth int, display device.Display,
	bounds device.Rect, class, text string) device.RawNode {
	node := pointFallbackNode(index, parent, depth, display, bounds, class, text)
	node.Clickable = true
	node.LongClickable = true
	return node
}

func pointFallbackCapture(display device.Display, nodes ...device.RawNode) device.Capture {
	taskID := 42
	return device.Capture{
		Display: display, Root: 0, Nodes: nodes, CapturedAt: time.Now(),
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: &taskID,
		},
	}
}

// pointFallbackCardFrame builds root -> card (interactive, own business text)
// -> children of one depth. The point lies on every child, so the deepest hit
// set is the child row and the owner is always the card.
func pointFallbackCardFrame(display device.Display, cardBounds, childBounds device.Rect,
	cardText, label string, decoration bool) device.Capture {
	card := pointFallbackInteractive(1, 0, 1, display, cardBounds,
		"androidx.cardview.widget.CardView", cardText)
	nodes := []device.RawNode{pointFallbackRoot(display, 1), card}
	var children []int
	if label != "" {
		nodes = append(nodes, pointFallbackNode(2, 1, 2, display, childBounds,
			"android.widget.TextView", label))
		children = append(children, 2)
	}
	if decoration {
		// A pure decoration leaf: no text, no action, only a visible image.
		nodes = append(nodes, pointFallbackNode(3, 1, 2, display, childBounds,
			"android.widget.ImageView", ""))
		children = append(children, 3)
	}
	nodes[1].Children = children
	return pointFallbackCapture(display, nodes...)
}

// pointFallbackTextlessCardFrame builds root -> card (interactive, no own text)
// -> readable title + numeric annotation. The point lies on the annotation, so
// the deepest hit is a passive child of a textless owner and only E3 can
// conserve the business content underneath it.
func pointFallbackTextlessCardFrame(display device.Display, cardBounds device.Rect,
	title, count string) device.Capture {
	card := pointFallbackInteractive(1, 0, 1, display, cardBounds,
		"androidx.cardview.widget.CardView", "")
	card.Children = []int{2, 3}
	titleNode := pointFallbackNode(2, 1, 2, display,
		device.Rect{Left: 140, Top: 340, Right: 860, Bottom: 440},
		"android.widget.TextView", title)
	countNode := pointFallbackNode(3, 1, 2, display,
		device.Rect{Left: 140, Top: 460, Right: 860, Bottom: 540},
		"android.widget.TextView", count)
	return pointFallbackCapture(display, pointFallbackRoot(display, 1),
		card, titleNode, countNode)
}

// pointFallbackRun is exactly what the caller holds when it reaches the
// fallback: the observation of the frame the caller read, the preview
// projection of the fresh frame, the fresh deepest hit set and the proof.
type pointFallbackRun struct {
	obs        *observationState
	projection *Projection
	fresh      *device.Capture
	oldHits    []*protocol.Node
	freshNodes []*protocol.Node
	proof      *pointProof
	x, y       int
	operation  *protocol.Operation
}

func pointFallbackPrepare(t *testing.T, original, fresh device.Capture,
	x, y int, operation *protocol.Operation) *pointFallbackRun {
	t.Helper()
	return pointFallbackPrepareRevoked(t, original, fresh, x, y, operation, nil)
}

func pointFallbackPrepareRevoked(t *testing.T, original, fresh device.Capture,
	x, y int, operation *protocol.Operation,
	revoked func() bool) *pointFallbackRun {
	t.Helper()
	projector := &testProjector{builder: observation.NewBuilder()}
	oldProjection, err := projector.Build(&original)
	if err != nil {
		t.Fatalf("original projection failed: %v", err)
	}
	freshProjection, err := projector.Preview(&fresh)
	if err != nil {
		t.Fatalf("fresh projection failed: %v", err)
	}
	obs := &observationState{
		DisplayID: original.Display.ID, Screen: original.Display,
		Projection: oldProjection,
	}
	return &pointFallbackRun{
		obs: obs, projection: freshProjection, fresh: &fresh,
		oldHits:    resolvePointSet(oldProjection.Tree.Root, x, y),
		freshNodes: resolvePointSet(freshProjection.Tree.Root, x, y),
		proof:      newPointProof(context.Background(), revoked, oldProjection.Capture, &fresh, fresh.Display.Bounds()),
		x:          x, y: y, operation: operation,
	}
}

func (run *pointFallbackRun) prove() *protocol.APIError {
	return run.proof.provePointOwnerFallback(run.obs, run.projection,
		run.freshNodes, run.x, run.y, run.operation)
}

func (run *pointFallbackRun) oldRawIndex(t *testing.T, node *protocol.Node) int {
	t.Helper()
	if node == nil {
		t.Fatalf("nil observed node")
	}
	index, mapped := run.obs.Projection.Source[node.Ref]
	if !mapped || index < 0 || index >= len(run.obs.Projection.Capture.Nodes) {
		t.Fatalf("observed node %q has no raw index", node.Ref)
	}
	return index
}

func (run *pointFallbackRun) freshRawIndex(t *testing.T, node *protocol.Node) int {
	t.Helper()
	if node == nil {
		t.Fatalf("nil fresh node")
	}
	index, mapped := run.projection.Source[node.Ref]
	if !mapped || index < 0 || index >= len(run.projection.Capture.Nodes) {
		t.Fatalf("fresh node %q has no raw index", node.Ref)
	}
	return index
}

// pointFallbackFreshOwner replays the production owner resolution of the fresh
// frame so a rejection can be attributed to the clause that produced it.
func pointFallbackFreshOwner(t *testing.T,
	run *pointFallbackRun) (*tapPublicView, *protocol.Node) {
	t.Helper()
	view, apiErr := buildTapPublicView(run.projection.Tree, run.fresh,
		run.projection.Source)
	if apiErr != nil {
		t.Fatalf("fresh view build failed: %+v", apiErr)
	}
	owners := collectPointOwners(view, run.freshNodes)
	if len(owners) != 1 {
		t.Fatalf("fresh owner count = %d, want 1", len(owners))
	}
	return view, owners[0]
}

// pointFallbackOldOwner replays the same resolution on the observed frame.
func pointFallbackOldOwner(t *testing.T, run *pointFallbackRun) *protocol.Node {
	t.Helper()
	view, apiErr := collectTapPublicView(run.obs)
	if apiErr != nil {
		t.Fatalf("observed view build failed: %+v", apiErr)
	}
	owners := collectPointOwners(view, run.oldHits)
	if len(owners) != 1 {
		t.Fatalf("observed owner count = %d, want 1", len(owners))
	}
	return owners[0]
}

func pointFallbackRequireAllowed(t *testing.T, apiErr *protocol.APIError) {
	t.Helper()
	if apiErr != nil {
		t.Fatalf("fallback rejected the unchanged point: %+v (details %#v)",
			apiErr, apiErr.Details)
	}
}

func pointFallbackRequirePointChanged(t *testing.T, apiErr *protocol.APIError) {
	t.Helper()
	if apiErr == nil {
		t.Fatalf("fallback authorized the point, want point_changed")
	}
	if apiErr.Code != protocol.CodeTargetChanged {
		t.Fatalf("error code = %s, want %s (%+v)",
			apiErr.Code, protocol.CodeTargetChanged, apiErr)
	}
	if got := apiErr.Details["reason"]; got != targetChangedReasonPointChanged {
		t.Fatalf("reason = %#v, want %q", got, targetChangedReasonPointChanged)
	}
}

func pointFallbackTapOperation(x, y int) *protocol.Operation {
	return &protocol.Operation{Type: "tap", Target: &protocol.Target{X: x, Y: y}}
}

func pointFallbackLongPressOperation(x, y int) *protocol.Operation {
	return &protocol.Operation{
		Type: "long_press", Target: &protocol.Target{X: x, Y: y},
	}
}

// TestPointOwnerFallbackTapDecorationGrowthKeepsOriginalPoint is the positive
// row of §8.1 row 5: the decoration layer under the point grows or replaces a
// passive leaf while the unique interactive owner, its bounds and the absence of
// an external owner stay the same, so the original point stays authorized.
func TestPointOwnerFallbackTapDecorationGrowthKeepsOriginalPoint(t *testing.T) {
	display := pointFallbackDisplay()
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
	childBounds := device.Rect{Left: 140, Top: 340, Right: 860, Bottom: 660}
	const pointX, pointY = 500, 500
	operation := pointFallbackTapOperation(pointX, pointY)

	t.Run("grown_hit_set", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", true)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)

		if len(run.oldHits) != 1 || len(run.freshNodes) != 2 {
			t.Fatalf("hit sets = observed %d fresh %d, want 1 and 2",
				len(run.oldHits), len(run.freshNodes))
		}
		// Reverse control: the strict hit-set proof rejects before any matching
		// because provePointTarget compares the fresh hit-set size with the
		// observed one and the decoration layer added one deepest node.
		if len(run.freshNodes) == len(run.oldHits) {
			t.Fatalf("strict hit-set cardinality gate would not reject, " +
				"the fallback would not be the deciding proof")
		}
		pointFallbackRequireAllowed(t, run.prove())
	})

	t.Run("passive_leaf_replaced_by_decoration", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "", true)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)

		if len(run.oldHits) != 1 || len(run.freshNodes) != 1 {
			t.Fatalf("hit sets = observed %d fresh %d, want 1 and 1",
				len(run.oldHits), len(run.freshNodes))
		}
		// Reverse control: the strict set proof really fails here. The observed
		// passive leaf has no structurally equal fresh node, so the strict
		// bipartite matching cannot pair it.
		compatible, complete := run.proof.pointTargetNodeCompatible(
			run.oldRawIndex(t, run.oldHits[0]),
			run.freshRawIndex(t, run.freshNodes[0]), pointX, pointY)
		if !complete {
			t.Fatalf("strict compatibility probe did not complete")
		}
		if compatible || maxBipartiteMatching([][]bool{{compatible}}) != 0 {
			t.Fatalf("strict target-set proof paired the replaced leaf " +
				"(compatible=true); the case is not a reverse control")
		}
		pointFallbackRequireAllowed(t, run.prove())
	})
}

// TestPointOwnerFallbackRejectsChangedOwnerOverlayAndBusinessText covers the
// three rejections of §4.5: another card took the point, an external interactive
// overlay now covers it, or the subtree business text was replaced without an
// unchanged non-numeric anchor.
func TestPointOwnerFallbackRejectsChangedOwnerOverlayAndBusinessText(t *testing.T) {
	display := pointFallbackDisplay()
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
	childBounds := device.Rect{Left: 140, Top: 340, Right: 860, Bottom: 660}
	const pointX, pointY = 500, 500
	operation := pointFallbackTapOperation(pointX, pointY)

	t.Run("owner_became_another_card", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品A", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品B", "商品B", false)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)

		// The owner conservation clauses themselves hold: one owner, every hit
		// under it, no external overlay. Only the identity of the owner changed.
		_, freshOwner := pointFallbackFreshOwner(t, run)
		if len(freshOwner.Label) != 1 || freshOwner.Label[0] != "商品B" {
			t.Fatalf("fresh owner label = %#v, want 商品B", freshOwner.Label)
		}
		oldIndex := run.oldRawIndex(t, pointFallbackOldOwner(t, run))
		_, state := matchActTargetWithOptions(context.Background(), nil,
			run.proof.original, run.proof.fresh,
			&run.proof.original.Nodes[oldIndex],
			defaultActMatchOptions().withoutTranslation())
		if state != actProbeNone {
			t.Fatalf("owner matcher state = %v, want none (the card itself changed)", state)
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("external_interactive_overlay_covers_point", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		// The overlay is a sibling root child, so it is neither an ancestor nor a
		// descendant of the owner: it is an external interactive owner.
		overlay := pointFallbackInteractive(3, 0, 1, display,
			device.Rect{Left: 300, Top: 400, Right: 700, Bottom: 600},
			"android.widget.Button", "浮层")
		fresh.Nodes = append(fresh.Nodes, overlay)
		fresh.Nodes[0].Children = []int{1, 3}
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)

		// Reverse control: the hit set still resolves to the same unique owner,
		// so the rejection is the external-owner clause and nothing else.
		view, freshOwner := pointFallbackFreshOwner(t, run)
		external := externalInteractiveOwnersAt(view, freshOwner, pointX, pointY)
		if len(external) != 1 {
			t.Fatalf("external interactive owners at the point = %d, want 1",
				len(external))
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("two_owners_share_the_hit_set", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		// A second, unrelated interactive container puts its own deepest node on
		// the same point, so the fresh hit set names two candidate owners.
		second := pointFallbackInteractive(3, 0, 1, display, cardBounds,
			"androidx.cardview.widget.CardView", "第二卡片")
		second.Children = []int{4}
		secondChild := pointFallbackNode(4, 3, 2, display, childBounds,
			"android.widget.TextView", "商品A")
		fresh.Nodes = append(fresh.Nodes, second, secondChild)
		fresh.Nodes[0].Children = []int{1, 3}
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)

		if len(run.freshNodes) != 2 {
			t.Fatalf("fresh hit count = %d, want 2", len(run.freshNodes))
		}
		view, apiErr := buildTapPublicView(run.projection.Tree, run.fresh,
			run.projection.Source)
		if apiErr != nil {
			t.Fatalf("fresh view build failed: %+v", apiErr)
		}
		if owners := collectPointOwners(view, run.freshNodes); len(owners) != 2 {
			t.Fatalf("fresh owner count = %d, want 2 (the guess this rule forbids)",
				len(owners))
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("nested_interactive_owners_at_one_point", func(t *testing.T) {
		// The frame is identical on both sides: cardB (interactive) holds a passive
		// wrapper with one text leaf and an inner interactive button with its own
		// text leaf at the same depth. The two deepest hits resolve to two
		// different interactive owners, yet every hit is inside cardB and cardA is
		// not external to it, so only the "exactly one owner" rule rejects.
		frame := func() device.Capture {
			root := pointFallbackRoot(display, 1)
			outer := pointFallbackInteractive(1, 0, 1, display,
				device.Rect{Left: 50, Top: 250, Right: 1030, Bottom: 850},
				"androidx.cardview.widget.CardView", "外层卡片")
			outer.Children = []int{2, 4}
			wrapper := pointFallbackNode(2, 1, 2, display,
				device.Rect{Left: 80, Top: 280, Right: 1000, Bottom: 520},
				"android.widget.FrameLayout", "")
			wrapper.Children = []int{3}
			outerText := pointFallbackNode(3, 2, 3, display,
				device.Rect{Left: 100, Top: 300, Right: 980, Bottom: 500},
				"android.widget.TextView", "外层文案")
			inner := pointFallbackInteractive(4, 1, 2, display,
				device.Rect{Left: 80, Top: 300, Right: 700, Bottom: 500},
				"android.widget.Button", "内层按钮")
			inner.Children = []int{5}
			innerText := pointFallbackNode(5, 4, 3, display,
				device.Rect{Left: 100, Top: 320, Right: 680, Bottom: 480},
				"android.widget.TextView", "内层文案")
			return pointFallbackCapture(display, root, outer, wrapper,
				outerText, inner, innerText)
		}
		const nestedX, nestedY = 500, 450
		run := pointFallbackPrepare(t, frame(), frame(), nestedX, nestedY, operation)
		if len(run.freshNodes) != 2 {
			t.Fatalf("fresh hit count = %d, want 2", len(run.freshNodes))
		}
		view, apiErr := buildTapPublicView(run.projection.Tree, run.fresh,
			run.projection.Source)
		if apiErr != nil {
			t.Fatalf("fresh view build failed: %+v", apiErr)
		}
		owners := collectPointOwners(view, run.freshNodes)
		if len(owners) != 2 {
			t.Fatalf("fresh owner count = %d, want 2", len(owners))
		}
		// Reverse control: every other owner clause would pass this frame.
		if !isPublicAncestor(view, owners[0], owners[1]) {
			t.Fatalf("the inner owner is not inside the outer owner")
		}
		for _, hit := range run.freshNodes {
			if hit != owners[0] && !isPublicAncestor(view, owners[0], hit) {
				t.Fatalf("a hit is outside the first owner")
			}
		}
		if external := externalInteractiveOwnersAt(view, owners[0], nestedX, nestedY); len(external) != 0 {
			t.Fatalf("external interactive owners = %d, want 0", len(external))
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("subtree_business_text_replaced", func(t *testing.T) {
		original := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品A", "12")
		fresh := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品B", "13")
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)

		// Reverse control: the owner binding itself still matches (the container
		// kept its structure, bounds and absence of own text); only the readable
		// subtree changed, so E3 is the rejecting clause.
		oldOwnerNode := pointFallbackOldOwner(t, run)
		_, freshOwnerNode := pointFallbackFreshOwner(t, run)
		oldIndex := run.oldRawIndex(t, oldOwnerNode)
		freshIndex := run.freshRawIndex(t, freshOwnerNode)
		oldOwner := &run.proof.original.Nodes[oldIndex]
		freshOwner := &run.proof.fresh.Nodes[freshIndex]
		if hasOwnReadableText(oldOwner) || hasOwnReadableText(freshOwner) {
			t.Fatalf("the textless-owner precondition does not hold")
		}
		match, state := matchActTargetWithOptions(context.Background(), nil,
			run.proof.original, run.proof.fresh, oldOwner,
			defaultActMatchOptions().withoutTranslation())
		if state != actProbeUnique {
			t.Fatalf("owner matcher state = %v, want unique (only E3 may reject)", state)
		}
		if indexOf(run.proof.fresh, match.raw) != freshIndex {
			t.Fatalf("owner matcher bound another fresh node")
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("subtree_numeric_annotation_conserved", func(t *testing.T) {
		original := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品A", "12")
		fresh := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品A", "13")
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)
		// The numeric skeleton plus one unchanged non-numeric anchor is the
		// intended conservation of §4.3 inside the owner fallback.
		pointFallbackRequireAllowed(t, run.prove())
	})

	t.Run("whole_tree_unique_resource_is_independent_content", func(t *testing.T) {
		original := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品A", "12")
		fresh := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品B", "13")
		// E2 is the second escape of clause 5: a resource identity that occurs
		// exactly once in each complete tree is the owner's own content, so the
		// subtree multiset is not the only anchor any more.
		original.Nodes[1].ResourceID = "card_primary"
		fresh.Nodes[1].ResourceID = "card_primary"
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)
		pointFallbackRequireAllowed(t, run.prove())
	})

	t.Run("resource_present_but_not_unique_still_rejects", func(t *testing.T) {
		original := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品A", "12")
		fresh := pointFallbackTextlessCardFrame(display, cardBounds,
			"商品B", "13")
		// The same resource appears twice in each tree, so it is not an identity
		// and clause 5 falls back to the replaced-subtree rejection.
		for index := range original.Nodes {
			original.Nodes[index].ResourceID = "card_shared"
		}
		for index := range fresh.Nodes {
			fresh.Nodes[index].ResourceID = "card_shared"
		}
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)
		pointFallbackRequirePointChanged(t, run.prove())
	})
}

// TestPointOwnerFallbackOperationScope proves that only coordinate tap and
// long_press reach the owner rule: the very same frame that is authorized for a
// tap stays rejected for text, raw swipe and a missing operation.
func TestPointOwnerFallbackOperationScope(t *testing.T) {
	display := pointFallbackDisplay()
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
	childBounds := device.Rect{Left: 140, Top: 340, Right: 860, Bottom: 660}
	const pointX, pointY = 500, 500

	t.Run("long_press_uses_the_same_rule", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", true)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY,
			pointFallbackLongPressOperation(pointX, pointY))
		if len(run.freshNodes) != 2 {
			t.Fatalf("fresh hit count = %d, want 2", len(run.freshNodes))
		}
		pointFallbackRequireAllowed(t, run.prove())
	})

	t.Run("coordinate_text_never_enters", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", true)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY,
			&protocol.Operation{
				Type: "text", Target: &protocol.Target{X: pointX, Y: pointY},
				Text: "hello",
			})
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("editable_owner_is_still_no_point_owner", func(t *testing.T) {
		// Every owner-conservation clause of this frame would hold for a text
		// operation: the single interactive owner is an editable input, so
		// pointOwnerMatchesOperation accepts it and the matcher binds it. Only the
		// operation-type gate keeps the fallback out of the coordinate text path.
		frame := func() device.Capture {
			input := pointFallbackInteractive(1, 0, 1, display,
				device.Rect{Left: 100, Top: 400, Right: 900, Bottom: 600},
				"android.widget.EditText", "输入框")
			input.Editable = true
			return pointFallbackCapture(display, pointFallbackRoot(display, 1), input)
		}
		operation := &protocol.Operation{
			Type: "text", Target: &protocol.Target{X: pointX, Y: pointY},
			Text: "hello",
		}
		run := pointFallbackPrepare(t, frame(), frame(), pointX, pointY, operation)
		_, freshOwner := pointFallbackFreshOwner(t, run)
		if !pointOwnerMatchesOperation(freshOwner, operation) {
			t.Fatalf("the editable owner does not match a text operation, " +
				"so the case is not a reverse control for the type gate")
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("raw_swipe_never_enters", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", true)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY,
			&protocol.Operation{
				Type: "swipe",
				From: &protocol.Point{X: pointX, Y: pointY},
				To:   &protocol.Point{X: pointX, Y: 900},
			})
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("nil_operation_never_enters", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", true)
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, nil)
		pointFallbackRequirePointChanged(t, run.prove())
	})
}

// TestPointOwnerFallbackNeverFollowsAMovedOwner is the §4.5 translation control:
// the owner matcher is called with translation disabled, so an owner that only
// kept its horizontal band can never be accepted somewhere else, and a
// horizontally displaced owner never follows either.
func TestPointOwnerFallbackNeverFollowsAMovedOwner(t *testing.T) {
	display := pointFallbackDisplay()
	const pointX, pointY = 500, 900
	operation := pointFallbackTapOperation(pointX, pointY)

	// One card, no children: the observed and the fresh hit set are the card
	// itself, so every owner-conservation clause except the identity geometry
	// holds by construction.
	page := func(bounds device.Rect) device.Capture {
		card := pointFallbackInteractive(1, 0, 1, display, bounds,
			"androidx.cardview.widget.CardView", "行项目")
		return pointFallbackCapture(display, pointFallbackRoot(display, 1), card)
	}

	t.Run("vertical_move_inside_the_same_band", func(t *testing.T) {
		original := page(device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 1100})
		fresh := page(device.Rect{Left: 100, Top: 800, Right: 900, Bottom: 1600})
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)
		if len(run.oldHits) != 1 || len(run.freshNodes) != 1 {
			t.Fatalf("hit sets = observed %d fresh %d, want 1 and 1",
				len(run.oldHits), len(run.freshNodes))
		}
		oldIndex := run.oldRawIndex(t, run.oldHits[0])
		freshIndex := run.freshRawIndex(t, run.freshNodes[0])

		// Reverse control: with Tier 5 enabled the same binding is provable as a
		// translation, so the rejection below is caused by withoutTranslation().
		match, state := matchActTargetWithOptions(context.Background(), nil,
			run.proof.original, run.proof.fresh,
			&run.proof.original.Nodes[oldIndex], defaultActMatchOptions())
		if state != actProbeUnique || match.tier != 5 {
			t.Fatalf("translation-enabled match = (%v, tier %d), want unique Tier 5",
				state, match.tier)
		}
		if indexOf(run.proof.fresh, match.raw) != freshIndex {
			t.Fatalf("translation-enabled match bound another fresh node")
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("horizontal_move_out_of_the_band", func(t *testing.T) {
		// The card is 400 wide and the point sits 300 from its left edge, so a
		// 250px sideways move keeps the point inside the owner while dropping the
		// mutual overlap to 0.375 and the horizontal band to 250px.
		original := page(device.Rect{Left: 200, Top: 300, Right: 600, Bottom: 1100})
		fresh := page(device.Rect{Left: 450, Top: 300, Right: 850, Bottom: 1100})
		run := pointFallbackPrepare(t, original, fresh, pointX, pointY, operation)
		if len(run.oldHits) != 1 || len(run.freshNodes) != 1 {
			t.Fatalf("hit sets = observed %d fresh %d, want 1 and 1",
				len(run.oldHits), len(run.freshNodes))
		}
		oldIndex := run.oldRawIndex(t, run.oldHits[0])
		// Even with Tier 5 enabled the horizontal band is violated, so this owner
		// is never followed sideways either.
		if _, state := matchActTargetWithOptions(context.Background(), nil,
			run.proof.original, run.proof.fresh,
			&run.proof.original.Nodes[oldIndex],
			defaultActMatchOptions()); state == actProbeUnique {
			t.Fatalf("horizontal move out of the band matched")
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})

	t.Run("point_left_the_owner_rect", func(t *testing.T) {
		// The card moves up 20px while its label keeps its place, so the point is
		// still inside the deepest hit but no longer inside the owner rectangle.
		// Tier 2 would still bind the owner, yet the unchanged point would land
		// outside it, which the origin rule never follows.
		const nearEdgeX, nearEdgeY = 500, 690
		nearEdge := pointFallbackTapOperation(nearEdgeX, nearEdgeY)
		frame := func(cardTop, cardBottom int) device.Capture {
			card := pointFallbackInteractive(1, 0, 1, display,
				device.Rect{Left: 100, Top: cardTop, Right: 900, Bottom: cardBottom},
				"androidx.cardview.widget.CardView", "行项目")
			card.Children = []int{2}
			label := pointFallbackNode(2, 1, 2, display,
				device.Rect{Left: 140, Top: 340, Right: 860, Bottom: 700},
				"android.widget.TextView", "行项目")
			return pointFallbackCapture(display, pointFallbackRoot(display, 1),
				card, label)
		}
		run := pointFallbackPrepare(t, frame(300, 700), frame(280, 680),
			nearEdgeX, nearEdgeY, nearEdge)
		if len(run.freshNodes) != 1 {
			t.Fatalf("fresh hit count = %d, want 1", len(run.freshNodes))
		}
		_, freshOwnerNode := pointFallbackFreshOwner(t, run)
		freshOwner := &run.proof.fresh.Nodes[run.freshRawIndex(t, freshOwnerNode)]
		if freshOwner.Bounds.Contains(nearEdgeX, nearEdgeY) {
			t.Fatalf("the fresh owner still contains the point; " +
				"the case is not a reverse control")
		}
		// Reverse control: the owner binding itself is still provable.
		oldIndex := run.oldRawIndex(t, pointFallbackOldOwner(t, run))
		if _, state := matchActTargetWithOptions(context.Background(), nil,
			run.proof.original, run.proof.fresh,
			&run.proof.original.Nodes[oldIndex],
			defaultActMatchOptions().withoutTranslation()); state != actProbeUnique {
			t.Fatalf("owner matcher state = %v, want unique (only the origin "+
				"containment may reject)", state)
		}
		pointFallbackRequirePointChanged(t, run.prove())
	})
}

// TestPointOwnerFallbackStopWinsOverContentRejection proves the terminal
// category of the fallback: a revoked proof returns control_revoked and never
// degrades into point_changed, whether the content would have passed or failed.
func TestPointOwnerFallbackStopWinsOverContentRejection(t *testing.T) {
	display := pointFallbackDisplay()
	cardBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 700}
	childBounds := device.Rect{Left: 140, Top: 340, Right: 860, Bottom: 660}
	const pointX, pointY = 500, 500
	operation := pointFallbackTapOperation(pointX, pointY)
	revoked := func() bool { return true }

	requireStopped := func(t *testing.T, apiErr *protocol.APIError) {
		t.Helper()
		if apiErr == nil {
			t.Fatalf("revoked fallback returned no error")
		}
		if apiErr.Code != protocol.CodeStopped {
			t.Fatalf("error code = %s, want %s (%+v)",
				apiErr.Code, protocol.CodeStopped, apiErr)
		}
	}

	t.Run("allowed_content_still_stops", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", true)
		run := pointFallbackPrepareRevoked(t, original, fresh, pointX, pointY,
			operation, revoked)
		requireStopped(t, run.prove())
	})

	t.Run("matcher_stage_rejection_still_stops", func(t *testing.T) {
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品A", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品B", "商品B", false)
		run := pointFallbackPrepareRevoked(t, original, fresh, pointX, pointY,
			operation, revoked)
		requireStopped(t, run.prove())
	})

	t.Run("pre_matcher_rejection_still_stops", func(t *testing.T) {
		// Two owners at the point reject long before the matcher runs, so only an
		// explicit STOP gate can keep control_revoked ahead of that rejection.
		original := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		fresh := pointFallbackCardFrame(display, cardBounds, childBounds,
			"商品卡片", "商品A", false)
		second := pointFallbackInteractive(3, 0, 1, display, cardBounds,
			"androidx.cardview.widget.CardView", "第二卡片")
		second.Children = []int{4}
		secondChild := pointFallbackNode(4, 3, 2, display, childBounds,
			"android.widget.TextView", "商品A")
		fresh.Nodes = append(fresh.Nodes, second, secondChild)
		fresh.Nodes[0].Children = []int{1, 3}
		run := pointFallbackPrepareRevoked(t, original, fresh, pointX, pointY,
			operation, revoked)
		requireStopped(t, run.prove())
	})
}
