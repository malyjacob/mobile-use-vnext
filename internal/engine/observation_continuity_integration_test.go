package engine

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// continuationHarness removes only wall-clock dwell from the fixed policies.
// The sample counts, ordering and commit funnel remain the production path.
func continuationHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.engine.cfg.Settle.Act.MinDwell = 0
	h.engine.cfg.Settle.Start.MinDwell = 0
	h.engine.cfg.Settle.OpenApp.MinDwell = 0
	h.engine.cfg.Wait.MinDwell = 0
	h.engine.cfg.Wait.StableFrames = 2
	h.engine.cfg.Wait.MaxSamples = 8
	return h
}

// continuationCapture is one real public-tree fixture with:
//   - an invisible context ancestor and one preserved text child;
//   - an anonymous visible container whose compact geometry is hidden;
//   - stable resources on the changing controls;
//   - a resource-only visible leaf that source purification must discard; and
//   - optional semantic leaves used to prove ref high-water behavior.
func continuationCapture(marker string, checked *bool, extraResources ...string) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		boolFalse := false
		boolTrue := true
		inputText := "input:" + marker
		contextText := "context:" + marker
		buttonResource := "action_primary"
		inputResource := "input_primary"
		checkboxResource := "checkbox_primary"
		contextResource := "context_primary"
		emptyResource := "purified_empty"

		nodes := []device.RawNode{
			{
				Index: 0, Parent: -1, Children: []int{1, 5}, Depth: 0,
				DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400},
				Bounds:    device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400},
				Package:   "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			},
			{
				Index: 1, Parent: 0, Children: []int{2, 3, 4, 6}, Depth: 1,
				DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 800},
				Bounds:    device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 800},
				Package:   "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
				HasFocused: true, Focused: false,
				HasSelected: true, Selected: false,
				HasPassword: true, Password: false,
				HasChecked: true, Checked: false,
			},
			{
				Index: 2, Parent: 1, Depth: 2, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 20, Top: 20, Right: 300, Bottom: 100},
				Bounds:    device.Rect{Left: 20, Top: 20, Right: 300, Bottom: 100},
				Package:   "com.example", ClassName: "android.widget.Button",
				Text: marker, TextSet: true,
				ResourceSet: true, ResourceID: buttonResource,
				HasClickable: true, Clickable: true,
				HasEnabled: true, Enabled: true,
			},
			{
				Index: 3, Parent: 1, Depth: 2, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 20, Top: 120, Right: 600, Bottom: 220},
				Bounds:    device.Rect{Left: 20, Top: 120, Right: 600, Bottom: 220},
				Package:   "com.example", ClassName: "android.widget.EditText",
				Text: inputText, TextSet: true,
				ResourceSet: true, ResourceID: inputResource,
				HasClickable: true, Clickable: true, Editable: true,
				HasEnabled: true, Enabled: true,
			},
			{
				Index: 4, Parent: 1, Depth: 2, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 20, Top: 240, Right: 300, Bottom: 320},
				Bounds:    device.Rect{Left: 20, Top: 240, Right: 300, Bottom: 320},
				Package:   "com.example", ClassName: "android.widget.CheckBox",
				Text: "remember", TextSet: true,
				ResourceSet: true, ResourceID: checkboxResource,
				HasCheckable: true, Checkable: true,
				HasClickable: true, Clickable: true,
				HasEnabled: true, Enabled: true,
			},
			{
				Index: 5, Parent: 0, Children: []int{7}, Depth: 1,
				DisplayID: display.ID, Visible: false,
				RawBounds: device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1100},
				Bounds:    device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1100},
				Package:   "com.example", ClassName: "android.widget.FrameLayout",
			},
			{
				Index: 6, Parent: 1, Depth: 2, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 700, Top: 20, Right: 760, Bottom: 80},
				Bounds:    device.Rect{Left: 700, Top: 20, Right: 760, Bottom: 80},
				Package:   "com.example", ClassName: "android.view.View",
				ResourceSet: true, ResourceID: emptyResource,
				HasEnabled: true, Enabled: true,
				HasFocused: true, Focused: false,
				HasSelected: true, Selected: false,
				HasPassword: true, Password: false,
				HasChecked: true, Checked: false,
				HasClickable: true, Clickable: false,
			},
			{
				Index: 7, Parent: 5, Depth: 2, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 20, Top: 920, Right: 700, Bottom: 1000},
				Bounds:    device.Rect{Left: 20, Top: 920, Right: 700, Bottom: 1000},
				Package:   "com.example", ClassName: "android.widget.TextView",
				Text: contextText, TextSet: true,
				ResourceSet: true, ResourceID: contextResource,
				HasEnabled: true, Enabled: true,
			},
		}
		nodes[4].HasChecked = checked != nil
		if checked == nil {
			nodes[4].Checked = false
		} else {
			nodes[4].Checked = *checked
		}
		for _, resource := range extraResources {
			index := len(nodes)
			nodes = append(nodes, device.RawNode{
				Index: index, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 20, Top: 360 + 80*index, Right: 600, Bottom: 420 + 80*index},
				Bounds:    device.Rect{Left: 20, Top: 360 + 80*index, Right: 600, Bottom: 420 + 80*index},
				Package:   "com.example", ClassName: "android.widget.TextView",
				Text: "extra:" + resource, TextSet: true,
				ResourceSet: true, ResourceID: resource,
				HasEnabled: true, Enabled: true,
			})
			// Append after the context subtree so the public preorder stays
			// monotonic in raw node order.
			nodes[0].Children = append(nodes[0].Children, index)
		}

		// Keep the fixture's synthetic defaults explicit and mutation-safe.
		_ = boolFalse
		_ = boolTrue
		return device.Capture{
			Display: display,
			Nodes:   nodes,
			Root:    0,
			Foreground: device.Foreground{
				State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
			},
			CapturedAt: time.Now(),
		}, nil
	}
}

// compactCycleCapture exercises the actual Builder, purification and compact
// projection path. One stable tappable edge sliver moves between two anonymous
// parents and crosses the sliver pruning boundary: a two-pixel-high strip at the
// viewport edge carries no readable content and is dropped, while the full-sized
// control survives. The input value changes from absent to an explicit empty
// string across the same frames.
func compactCycleCapture(largeSliver, sliverInSecondParent, explicitEmptyValue,
	includeLateSliver bool) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		screen := display.Bounds()
		width, height := screen.Width(), screen.Height()
		half := height / 2
		firstParent := device.Rect{Left: 0, Top: 0, Right: width, Bottom: half}
		secondParent := device.Rect{Left: 0, Top: half, Right: width, Bottom: height}

		sliverParent := 1
		sliverParentBounds := firstParent
		if sliverInSecondParent {
			sliverParent = 2
			sliverParentBounds = secondParent
		}
		sliverWidth, sliverHeight := 200, 2
		if largeSliver {
			sliverHeight = 200
		}
		sliverBounds := device.Rect{
			Left:   100,
			Top:    sliverParentBounds.Top + 300,
			Right:  100 + sliverWidth,
			Bottom: sliverParentBounds.Top + 300 + sliverHeight,
		}
		sliver := device.RawNode{
			Index: 6, Parent: sliverParent, Depth: 2, DisplayID: display.ID,
			Visible: true, RawBounds: sliverBounds, Bounds: sliverBounds,
			Package: "com.example", ClassName: "android.view.View",
			ResourceSet: true, ResourceID: "compact_cycle_sliver",
			HasEnabled: true, Enabled: true,
			HasFocused: true, Focused: false,
			HasSelected: true, Selected: false,
			HasPassword: true, Password: false,
			HasChecked: true, Checked: false,
			HasClickable: true, Clickable: true,
		}

		firstChildren := []int{4}
		secondChildren := []int{5}
		if sliverInSecondParent {
			secondChildren = append(secondChildren, sliver.Index)
		} else {
			firstChildren = append(firstChildren, sliver.Index)
		}
		anonymousParent := func(index int, bounds device.Rect, children []int) device.RawNode {
			return device.RawNode{
				Index: index, Parent: 0, Children: children, Depth: 1, DisplayID: display.ID,
				Visible: true, RawBounds: bounds, Bounds: bounds,
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
				HasFocused: true, Focused: false,
				HasSelected: true, Selected: false,
				HasPassword: true, Password: false,
				HasChecked: true, Checked: false,
				HasClickable: true, Clickable: false,
			}
		}
		anchor := func(index, parent int, bounds device.Rect, label string) device.RawNode {
			return device.RawNode{
				Index: index, Parent: parent, Depth: 2, DisplayID: display.ID,
				Visible: true, RawBounds: bounds, Bounds: bounds,
				Package: "com.example", ClassName: "android.widget.TextView",
				Text: label, TextSet: true,
				HasEnabled: true, Enabled: true,
			}
		}
		input := device.RawNode{
			Index: 3, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: device.Rect{Left: 100, Top: height - 220, Right: width - 100, Bottom: height - 120},
			Bounds:    device.Rect{Left: 100, Top: height - 220, Right: width - 100, Bottom: height - 120},
			Package:   "com.example", ClassName: "android.widget.EditText",
			Text: "", TextSet: explicitEmptyValue,
			ResourceSet: true, ResourceID: "compact_cycle_input",
			HasClickable: true, Clickable: true, Editable: true,
			HasEnabled: true, Enabled: true,
		}

		nodes := []device.RawNode{
			{
				Index: 0, Parent: -1, Children: []int{1, 2, 3}, Depth: 0,
				DisplayID: display.ID, Visible: true, RawBounds: screen, Bounds: screen,
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			},
			anonymousParent(1, firstParent, firstChildren),
			anonymousParent(2, secondParent, secondChildren),
			input,
			anchor(4, 1, device.Rect{
				Left: 20, Top: 20, Right: width - 20, Bottom: 100,
			}, "cycle-first"),
			anchor(5, 2, device.Rect{
				Left: 20, Top: half + 20, Right: width - 20, Bottom: half + 100,
			}, "cycle-second"),
			sliver,
		}
		if includeLateSliver {
			lateBounds := device.Rect{Left: 350, Top: 300, Right: 550, Bottom: 500}
			lateSliver := sliver
			lateSliver.Index = 7
			lateSliver.Parent = 1
			lateSliver.RawBounds = lateBounds
			lateSliver.Bounds = lateBounds
			lateSliver.ResourceID = "compact_late_sliver"
			nodes = append(nodes, lateSliver)
			nodes[1].Children = append(nodes[1].Children, lateSliver.Index)
		}

		return device.Capture{
			Display: display,
			Root:    0,
			Nodes:   nodes,
			Foreground: device.Foreground{
				State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
			},
			CapturedAt: time.Now(),
		}, nil
	}
}

func boolAddress(value bool) *bool {
	return &value
}

func publishedObservation(t *testing.T, result *protocol.Result, entry string) *protocol.Observation {
	t.Helper()
	if result == nil || result.Observation == nil || result.Observation.ID == "" ||
		result.Observation.Tree == nil {
		t.Fatalf("%s did not publish an observation: %+v", entry, result)
	}
	return result.Observation
}

func observationStateByID(t *testing.T, e *Engine, id string) *observationState {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	state := e.observations[id]
	if state == nil || state.tree() == nil || state.Compact == nil {
		t.Fatalf("observation %q is not retained", id)
	}
	return state
}

func sameOptionalBool(left, right *bool) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// assertReadContentConsistent checks that every node delivered by compact also
// exists in detailed with the same public content. Compact may omit a pruned
// subtree, may hide a retained node's geometry and delivers the center point of a
// non-root/non-scroll node instead of its rectangle, so this is a
// projection-subset check rather than a pairwise tree comparison. An actionable
// non-input label longer than the compact truncation limit is additionally
// delivered as a leading UTF-8 codepoint prefix with Truncated=true, while the
// detailed view keeps the full text.
func assertReadContentConsistent(t *testing.T, compact, detailed *protocol.ReadTree) {
	t.Helper()
	compact = observation.UnpackTree(compact)
	detailed = observation.UnpackTree(detailed)
	if compact == nil || detailed == nil {
		t.Fatalf("missing compact/detailed tree: %+v %+v", compact, detailed)
	}
	screen := protocol.Bounds{}
	if compact.Root != nil && compact.Root.Bounds.Kind == protocol.GeometryKnown {
		screen = compact.Root.Bounds.Rect
	}
	detailedByRef := make(map[string]*protocol.ReadNode)
	var indexDetailed func(*protocol.ReadNode)
	indexDetailed = func(node *protocol.ReadNode) {
		if node == nil {
			return
		}
		if node.Ref == "" {
			t.Fatalf("detailed tree contains a chain instead of an ordinary node: %+v", node)
		}
		if detailedByRef[node.Ref] != nil {
			t.Fatalf("detailed tree repeats ref %q", node.Ref)
		}
		detailedByRef[node.Ref] = node
		for _, child := range node.Children {
			indexDetailed(child)
		}
	}
	indexDetailed(detailed.Root)

	var walkCompact func(*protocol.ReadNode)
	walkCompact = func(node *protocol.ReadNode) {
		t.Helper()
		if node == nil {
			return
		}
		if node.Ref == "" {
			t.Fatalf("compact tree contains a chain instead of an ordinary node: %+v", node)
		}
		expected := detailedByRef[node.Ref]
		if expected == nil {
			t.Fatalf("compact ref %q is absent from detailed tree", node.Ref)
		}
		if node.Role != expected.Role || node.Context != expected.Context ||
			node.Axis != expected.Axis ||
			!compactLabelsMatch(node, expected) ||
			!slices.Equal(node.Actions, expected.Actions) ||
			!sameOptionalBool(node.Enabled, expected.Enabled) ||
			!sameOptionalBool(node.Focused, expected.Focused) ||
			!sameOptionalBool(node.Selected, expected.Selected) ||
			!sameOptionalBool(node.Protected, expected.Protected) ||
			!sameOptionalBool(node.Checked, expected.Checked) ||
			!sameOptionalString(node.Value, expected.Value) ||
			!sameOptionalString(node.Hint, expected.Hint) ||
			!sameOptionalString(node.Resource, expected.Resource) {
			t.Fatalf("compact/detailed content mismatch at %q:\ncompact=%+v\ndetailed=%+v",
				node.Ref, node, expected)
		}
		switch node.Bounds.Kind {
		case protocol.GeometryKnown:
			if expected.Bounds.Kind != protocol.GeometryKnown ||
				node.Bounds.Rect != expected.Bounds.Rect {
				t.Fatalf("known compact geometry differs at %q: compact=%+v detailed=%+v",
					node.Ref, node.Bounds, expected.Bounds)
			}
		case protocol.GeometryPoint:
			if expected.Bounds.Kind != protocol.GeometryKnown {
				t.Fatalf("point compact geometry has no known detailed source at %q: %+v",
					node.Ref, expected.Bounds)
			}
			wantX, wantY := compactCenterOf(expected.Bounds.Rect, screen)
			if node.Bounds.Point.X != wantX || node.Bounds.Point.Y != wantY {
				t.Fatalf("compact point %q = (%d,%d), want the center (%d,%d) of %+v",
					node.Ref, node.Bounds.Point.X, node.Bounds.Point.Y, wantX, wantY,
					expected.Bounds.Rect)
			}
		case protocol.GeometryUnknown:
			if expected.Bounds.Kind != protocol.GeometryUnknown {
				t.Fatalf("unknown compact geometry differs at %q: compact=%+v detailed=%+v",
					node.Ref, node.Bounds, expected.Bounds)
			}
		case protocol.GeometryHidden:
			if expected.Bounds.Kind != protocol.GeometryKnown {
				t.Fatalf("hidden compact geometry has no known detailed source at %q: %+v",
					node.Ref, expected.Bounds)
			}
		}
		for _, child := range node.Children {
			walkCompact(child)
		}
	}
	walkCompact(compact.Root)
}

// compactLabelsMatch compares the label of one compact node with the complete label
// of the detailed node it was projected from. The compact projection rewrites only
// one label: an input whose single label part repeats its value publishes no label,
// while the detailed view keeps the source text as both. A node carrying
// Truncated=true is accepted as the T250 prefix delivery instead, see
// truncatedLabelsMatch.
func compactLabelsMatch(compact, detailed *protocol.ReadNode) bool {
	if compact == nil || detailed == nil {
		return false
	}
	if slices.Equal(compact.Label, detailed.Label) {
		return true
	}
	if compact.Truncated {
		return truncatedLabelsMatch(compact, detailed)
	}
	return compact.Role == protocol.RoleInput && len(compact.Label) == 0 &&
		len(detailed.Label) == 1 && detailed.Value != nil &&
		detailed.Label[0] == *detailed.Value
}

// truncatedLabelsMatch accepts one T250 compact delivery: an actionable,
// non-input, non-context, non-root node whose compact label parts are proper
// UTF-8 codepoint prefixes of the detailed parts at the same index. The detailed
// view always keeps the full text, so a truncated node must differ from it and
// must never split a multi-byte rune.
func truncatedLabelsMatch(compact, detailed *protocol.ReadNode) bool {
	if compact.Role == protocol.RoleInput || compact.Role == protocol.RoleRoot ||
		compact.Context || len(compact.Actions) == 0 {
		return false
	}
	if len(compact.Label) == 0 || len(compact.Label) != len(detailed.Label) {
		return false
	}
	truncated := false
	for index, part := range compact.Label {
		full := detailed.Label[index]
		if !utf8.ValidString(part) || !strings.HasPrefix(full, part) {
			return false
		}
		if utf8.RuneCountInString(part) < utf8.RuneCountInString(full) {
			truncated = true
		}
	}
	return truncated
}

// compactCenterOf returns the center point the compact projection publishes for one
// complete rectangle: the truncated half-open center of its visible part, or of the
// whole rectangle when nothing of it is visible. Device edges are non-negative, so
// this is the plain truncated midpoint the projection uses, clamped by construction
// inside the rectangle.
func compactCenterOf(rect, screen protocol.Bounds) (int, int) {
	visible := protocol.Bounds{
		Left:   max(rect.Left, screen.Left),
		Top:    max(rect.Top, screen.Top),
		Right:  min(rect.Right, screen.Right),
		Bottom: min(rect.Bottom, screen.Bottom),
	}
	if visible.Right > visible.Left && visible.Bottom > visible.Top {
		rect = visible
	}
	return (rect.Left + rect.Right) / 2, (rect.Top + rect.Bottom) / 2
}

func assertSourceMapping(t *testing.T, state *observationState) {
	t.Helper()
	projection := state.Projection
	if projection == nil || projection.Capture == nil || projection.Source == nil {
		t.Fatalf("observation %q has no source projection", state.ID)
	}
	ordinary := make(map[string]bool)
	usedRaw := make(map[int]bool)
	lastRaw := -1
	var walk func(*protocol.Node)
	walk = func(node *protocol.Node) {
		t.Helper()
		if node == nil {
			return
		}
		if !node.Context {
			rawIndex, ok := projection.Source[node.Ref]
			if !ok {
				t.Fatalf("ordinary ref %q has no Source entry", node.Ref)
			}
			if rawIndex < 0 || rawIndex >= len(projection.Capture.Nodes) {
				t.Fatalf("Source[%q]=%d outside %d raw nodes", node.Ref, rawIndex, len(projection.Capture.Nodes))
			}
			if projection.Capture.Nodes[rawIndex].Index != rawIndex {
				t.Fatalf("Source[%q]=%d points at raw node with Index=%d",
					node.Ref, rawIndex, projection.Capture.Nodes[rawIndex].Index)
			}
			if usedRaw[rawIndex] {
				t.Fatalf("raw index %d is shared by multiple ordinary refs", rawIndex)
			}
			if rawIndex <= lastRaw {
				t.Fatalf("Source preorder is not monotonic at %q: raw=%d after %d", node.Ref, rawIndex, lastRaw)
			}
			lastRaw = rawIndex
			usedRaw[rawIndex] = true
			ordinary[node.Ref] = true
		} else if _, leaked := projection.Source[node.Ref]; leaked {
			t.Fatalf("context ref %q leaked into actionable Source", node.Ref)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(projection.Tree.Root)
	if len(ordinary) != len(projection.Source) {
		t.Fatalf("ordinary refs=%d but Source entries=%d", len(ordinary), len(projection.Source))
	}

	emptyRaw := -1
	for index, raw := range projection.Capture.Nodes {
		if raw.ResourceSet && raw.ResourceID == "purified_empty" {
			emptyRaw = index
			break
		}
	}
	if emptyRaw < 0 {
		t.Fatal("fixture is missing the resource-only leaf")
	}
	if usedRaw[emptyRaw] {
		t.Fatalf("purified resource-only raw node %d was published", emptyRaw)
	}
	var leakedResource bool
	var scanResource func(*protocol.Node)
	scanResource = func(node *protocol.Node) {
		if node == nil {
			return
		}
		if node.Resource != nil && *node.Resource == "purified_empty" {
			leakedResource = true
		}
		for _, child := range node.Children {
			scanResource(child)
		}
	}
	scanResource(projection.Tree.Root)
	if leakedResource {
		t.Fatal("resource-only leaf survived public purification")
	}
}

func assertInspectionMatchesObservation(t *testing.T, e *Engine, id string) {
	t.Helper()
	state := observationStateByID(t, e, id)
	expectedCompact := observation.ProjectCompactTree(state.tree())
	if !observation.ReadEqual(state.Compact, expectedCompact) {
		t.Fatalf("retained compact tree %q differs from its internal projection", id)
	}

	result, apiErr := e.Inspect("agent", &protocol.InspectRequest{Reference: id})
	if apiErr != nil {
		t.Fatalf("inspect %q: %v", id, apiErr)
	}
	if result == nil || result.Inspection == nil || result.Inspection.Tree == nil ||
		result.Inspection.View != protocol.ViewDetailed ||
		result.Inspection.Tree.View != protocol.ViewDetailed {
		t.Fatalf("inspect %q did not return detailed history: %+v", id, result)
	}
	expectedDetailed := observation.ProjectDetailedTree(state.tree())
	if !observation.ReadEqual(result.Inspection.Tree, expectedDetailed) {
		t.Fatalf("detailed inspection %q differs from the retained internal tree", id)
	}
	wantBytes, err := protocol.JSON(expectedDetailed)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := protocol.JSON(result.Inspection.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("detailed inspection %q changed canonical tree bytes", id)
	}
	assertReadContentConsistent(t, state.Compact, result.Inspection.Tree)
	assertSourceMapping(t, state)
}

func nodeByRef(root *protocol.Node, ref string) *protocol.Node {
	if root == nil {
		return nil
	}
	if root.Ref == ref {
		return root
	}
	for _, child := range root.Children {
		if found := nodeByRef(child, ref); found != nil {
			return found
		}
	}
	return nil
}

func readNodeByRef(root *protocol.ReadNode, ref string) *protocol.ReadNode {
	if root == nil {
		return nil
	}
	if root.Ref == ref {
		return root
	}
	for _, child := range root.Children {
		if found := readNodeByRef(child, ref); found != nil {
			return found
		}
	}
	return nil
}

func sourceRefForResource(projection *Projection, resource string) (string, bool) {
	if projection == nil || projection.Capture == nil {
		return "", false
	}
	for _, raw := range projection.Capture.Nodes {
		if !raw.ResourceSet || raw.ResourceID != resource {
			continue
		}
		for ref, rawIndex := range projection.Source {
			if rawIndex == raw.Index {
				return ref, true
			}
		}
	}
	return "", false
}

func readParentOf(root, child *protocol.ReadNode) *protocol.ReadNode {
	if root == nil || child == nil {
		return nil
	}
	for _, candidate := range root.Children {
		if candidate == child {
			return root
		}
		if parent := readParentOf(candidate, child); parent != nil {
			return parent
		}
	}
	return nil
}

func requireOrdinaryReadDefinitions(t *testing.T, root *protocol.ReadNode) {
	t.Helper()
	var walk func(*protocol.ReadNode)
	walk = func(node *protocol.ReadNode) {
		t.Helper()
		if node == nil {
			t.Fatalf("read definition contains a nil node")
		}
		if node.Ref == "" || node.IsChain() {
			t.Fatalf("new compact output contains a non-ordinary definition: %+v", node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
}

func requireOrdinaryReadDelta(t *testing.T, delta *protocol.ReadDelta) {
	t.Helper()
	for _, node := range delta.Add {
		requireOrdinaryReadDefinitions(t, node)
	}
	for ref, node := range delta.Replace {
		if node == nil || node.Ref != ref {
			t.Fatalf("replace %q is not rooted at its ordinary ref: %+v", ref, node)
		}
		requireOrdinaryReadDefinitions(t, node)
	}
}

func refByResource(t *testing.T, state *observationState, resource string) string {
	t.Helper()
	if ref, ok := sourceRefForResource(state.Projection, resource); ok {
		return ref
	}
	if resource == "context_primary" {
		var found string
		var walk func(*protocol.Node)
		walk = func(node *protocol.Node) {
			if node == nil || found != "" {
				return
			}
			if node.Context {
				for _, label := range node.Label {
					if strings.HasPrefix(label, "context:") {
						found = node.Ref
						return
					}
				}
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(state.tree().Root)
		if found != "" {
			return found
		}
	}
	t.Fatalf("observation %q has no mapped resource ref for %q", state.ID, resource)
	return ""
}

func assertDefaultRefsStable(t *testing.T, states []*observationState) {
	t.Helper()
	for _, resource := range []string{
		"action_primary", "input_primary", "checkbox_primary", "context_primary",
	} {
		want := refByResource(t, states[0], resource)
		for index, state := range states[1:] {
			got := refByResource(t, state, resource)
			if got != want {
				t.Fatalf("%s ref changed at observation[%d]: %s -> %s", resource, index+1, want, got)
			}
		}
	}
}

func assertDeltaReconstructs(t *testing.T, state *observationState,
	base *protocol.ReadTree, wantBase string) *protocol.ReadDelta {
	t.Helper()
	// The result tree is checked separately by callers; this is the fixed-base
	// logical tree committed with the observation.
	delta, err := observation.ReadDiff(wantBase, base, state.Compact)
	if err != nil {
		t.Fatalf("recompute ReadDiff for %q: %v", state.ID, err)
	}
	if delta.Base != wantBase {
		t.Fatalf("delta base=%q, want %q", delta.Base, wantBase)
	}
	applied, err := observation.ReadApply(base, delta)
	if err != nil {
		t.Fatalf("ReadApply for %q: %v", state.ID, err)
	}
	if !observation.ReadEqual(applied, state.Compact) {
		t.Fatalf("ReadApply did not reconstruct observation %q", state.ID)
	}
	wantBytes, err := protocol.JSON(state.Compact)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := protocol.JSON(applied)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("ReadApply changed canonical bytes for %q", state.ID)
	}
	return delta
}

func assertDeliveredDelta(t *testing.T, result *protocol.Result, state *observationState,
	base *protocol.ReadTree, wantBase string) *protocol.ReadDelta {
	t.Helper()
	if result.Observation.Tree.Delta == nil || result.Observation.Tree.Full != nil {
		t.Fatalf("expected delivered delta for %q: %+v", state.ID, result.Observation.Tree)
	}
	delta := result.Observation.Tree.Delta
	if delta.Base != wantBase {
		t.Fatalf("delivered delta base=%q, want %q", delta.Base, wantBase)
	}
	applied, err := observation.ReadApply(base, delta)
	if err != nil {
		t.Fatalf("apply delivered delta for %q: %v", state.ID, err)
	}
	if !observation.ReadEqual(applied, state.Compact) {
		t.Fatalf("delivered delta did not reconstruct %q", state.ID)
	}
	return delta
}

type inspectReadOnlyGuard struct {
	expiresAt             time.Time
	baselineID            string
	currentID             string
	baselineBytes         []byte
	currentTreeBytes      []byte
	builderPreviousBytes  []byte
	nextObservation       int64
	observationCount      int
	captureCalls          int
	ordinaryWork          bool
	builderPreviousObject any
}

func captureInspectReadOnlyGuard(t *testing.T, h *harness) inspectReadOnlyGuard {
	t.Helper()
	e := h.engine
	e.mu.Lock()
	guard := inspectReadOnlyGuard{
		expiresAt:        e.session.ExpiresAt,
		nextObservation:  e.nextObservation,
		observationCount: len(e.observations),
		ordinaryWork:     e.work != nil,
	}
	if baseline := baselineWithID(e.baselines, e.activeBaselineID); baseline != nil {
		guard.baselineID = baseline.ObservationID
		guard.baselineBytes, _ = protocol.JSON(baseline.Tree)
	}
	if e.current != nil {
		guard.currentID = e.current.ID
		guard.currentTreeBytes, _ = protocol.JSON(e.current.tree())
	}
	e.mu.Unlock()
	projector, ok := e.projector.(*testProjector)
	if !ok {
		t.Fatalf("unexpected projector type %T", e.projector)
	}
	previous := projector.builder.Previous()
	if previous != nil {
		guard.builderPreviousObject = previous
		var err error
		guard.builderPreviousBytes, err = protocol.JSON(previous.Tree)
		if err != nil {
			t.Fatal(err)
		}
	}
	guard.captureCalls = h.device.captureCount()
	return guard
}

func assertInspectReadOnly(t *testing.T, h *harness, before inspectReadOnlyGuard) {
	t.Helper()
	after := captureInspectReadOnlyGuard(t, h)
	if !after.expiresAt.Equal(before.expiresAt) {
		t.Fatalf("inspect renewed the lease: %s -> %s", before.expiresAt, after.expiresAt)
	}
	if after.baselineID != before.baselineID || !bytes.Equal(after.baselineBytes, before.baselineBytes) {
		t.Fatalf("inspect changed the baseline: %q -> %q", before.baselineID, after.baselineID)
	}
	if after.currentID != before.currentID ||
		!bytes.Equal(after.currentTreeBytes, before.currentTreeBytes) {
		t.Fatalf("inspect changed the current observation: %q -> %q", before.currentID, after.currentID)
	}
	if after.builderPreviousObject != before.builderPreviousObject ||
		!bytes.Equal(after.builderPreviousBytes, before.builderPreviousBytes) {
		t.Fatalf("inspect changed projector continuity")
	}
	if after.nextObservation != before.nextObservation {
		t.Fatalf("inspect allocated an observation id: %d -> %d", before.nextObservation, after.nextObservation)
	}
	if after.observationCount != before.observationCount {
		t.Fatalf("inspect changed retained observations: %d -> %d",
			before.observationCount, after.observationCount)
	}
	if after.captureCalls != before.captureCalls {
		t.Fatalf("inspect performed device capture: %d -> %d", before.captureCalls, after.captureCalls)
	}
	if after.ordinaryWork {
		t.Fatal("inspect took an ordinary work position")
	}
}

// TestI01EntryPathObservationContinuityAndInspect drives all four engine
// acquisition entry points through the same public projection, fixed-base
// delta and historical inspection chain.
func TestI01EntryPathObservationContinuityAndInspect(t *testing.T) {
	h := continuationHarness(t)
	h.device.scriptCaptures(continuationCapture("i01-start", boolAddress(false)))
	start := h.startSession()
	startObservation := publishedObservation(t, start, "start")
	if startObservation.Tree.Full == nil || startObservation.Tree.Delta != nil {
		t.Fatalf("start did not publish a full tree: %+v", startObservation.Tree)
	}
	if h.device.captureCount() == 0 {
		t.Fatal("start did not capture the device")
	}
	assertInspectionMatchesObservation(t, h.engine, startObservation.ID)

	beforeObserveCaptures := h.device.captureCount()
	h.device.scriptCaptures(continuationCapture("i01-observe", boolAddress(false)))
	observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pi01observe",
		&protocol.ObserveRequest{Reference: startObservation.ID})
	if apiErr != nil {
		t.Fatalf("observe entry: %v", apiErr)
	}
	observeObservation := publishedObservation(t, observed, "observe")
	if h.device.captureCount() <= beforeObserveCaptures {
		t.Fatal("observe entry did not capture")
	}
	observeState := observationStateByID(t, h.engine, observeObservation.ID)
	assertDeliveredDelta(t, observed, observeState, observationStateByID(t, h.engine, startObservation.ID).Compact,
		startObservation.ID)

	beforeWaitCaptures := h.device.captureCount()
	h.device.scriptCaptures(continuationCapture("i01-wait", boolAddress(false)))
	waited, apiErr := h.engine.WaitFor(context.Background(), "agent", "Pi01wait",
		&protocol.WaitForRequest{
			Reference: observeObservation.ID,
			Query:     "i01-wait",
			HasQuery:  true,
			Timeout:   1,
		})
	if apiErr != nil {
		t.Fatalf("wait_for entry: %v", apiErr)
	}
	waitObservation := publishedObservation(t, waited, "wait_for")
	if waited.Wait == nil || waited.Wait.Trigger != protocol.WaitTriggerQuery {
		t.Fatalf("wait_for did not report its condition trigger: %+v", waited.Wait)
	}
	if h.device.captureCount() <= beforeWaitCaptures {
		t.Fatal("wait_for entry did not capture")
	}
	waitState := observationStateByID(t, h.engine, waitObservation.ID)
	assertDeliveredDelta(t, waited, waitState,
		observationStateByID(t, h.engine, startObservation.ID).Compact, startObservation.ID)

	beforeAppCaptures := h.device.captureCount()
	h.device.scriptCaptures(continuationCapture("i01-app", boolAddress(false)))
	h.device.launchTask = &device.Task{
		TaskID: 77, RootID: 77, Package: "com.example.app",
		Component: "com.example.app/.MainActivity",
	}
	appResult, apiErr := h.engine.App(context.Background(), "agent", "Pi01app",
		&protocol.AppRequest{
			Kind:      protocol.AppKindOpen,
			Reference: waitObservation.ID,
			Target:    &protocol.AppTarget{App: "com.example.app"},
		})
	if apiErr != nil {
		t.Fatalf("app entry: %v", apiErr)
	}
	if appResult.Outcome != OutcomeExecuted {
		t.Fatalf("app outcome=%q error=%+v", appResult.Outcome, appResult.Error)
	}
	appObservation := publishedObservation(t, appResult, "app")
	if appObservation.Tree.Full == nil || appObservation.Tree.Delta != nil {
		t.Fatalf("app post observation was not full: %+v", appObservation.Tree)
	}
	if h.device.captureCount() <= beforeAppCaptures {
		t.Fatal("app entry did not perform its post-observation capture")
	}

	states := []*observationState{
		observationStateByID(t, h.engine, startObservation.ID),
		observeState,
		waitState,
		observationStateByID(t, h.engine, appObservation.ID),
	}
	assertDefaultRefsStable(t, states)
	for _, state := range states {
		assertInspectionMatchesObservation(t, h.engine, state.ID)
	}

	// The final inspect is intentionally aimed at the first O after all later
	// entries retired it. It must read history without acquiring or renewing
	// anything.
	beforeInspect := captureInspectReadOnlyGuard(t, h)
	inspection, apiErr := h.engine.Inspect("agent",
		&protocol.InspectRequest{Reference: startObservation.ID})
	if apiErr != nil {
		t.Fatalf("inspect original O: %v", apiErr)
	}
	if inspection == nil || inspection.Inspection == nil ||
		inspection.Inspection.ObservationID != startObservation.ID {
		t.Fatalf("inspect returned the wrong historical O: %+v", inspection)
	}
	assertInspectReadOnly(t, h, beforeInspect)
}

func checkedState(value *bool) string {
	if value == nil {
		return "unknown"
	}
	if *value {
		return "true"
	}
	return "false"
}

// TestI02FixedBaselineDeltaChain proves every later delta is calculated from
// the same confirmed full O, even while the caller follows a chain of deltas.
func TestI02FixedBaselineDeltaChain(t *testing.T) {
	h := continuationHarness(t)
	// The compact projection withholds most display geometry, so the changing
	// controls alone are close to the fixed 80% delta threshold. Stable labelled
	// leaves keep this fixture focused on the fixed-baseline delta chain instead
	// of turning it into a full-fallback sample.
	stableResources := []string{
		"i02_stable_00_baseline", "i02_stable_01_baseline",
		"i02_stable_02_baseline", "i02_stable_03_baseline",
		"i02_stable_04_baseline", "i02_stable_05_baseline",
		"i02_stable_06_baseline", "i02_stable_07_baseline",
	}
	i02Capture := func(marker string, checked *bool) captureStep {
		return continuationCapture(marker, checked, stableResources...)
	}
	h.device.scriptCaptures(i02Capture("i02-base", boolAddress(false)))
	start := h.startSession()
	startObservation := publishedObservation(t, start, "start")
	startState := observationStateByID(t, h.engine, startObservation.ID)
	startTreeBytes, err := protocol.JSON(startState.tree())
	if err != nil {
		t.Fatal(err)
	}
	startPublicBytes, err := protocol.JSON(startObservation.Tree.Full)
	if err != nil {
		t.Fatal(err)
	}
	base := startState.Compact

	ackResult, apiErr := h.engine.Observe(context.Background(), "agent", "Pi02ack",
		&protocol.ObserveRequest{Reference: startObservation.ID})
	if apiErr != nil {
		t.Fatalf("confirm full baseline: %v", apiErr)
	}
	current := publishedObservation(t, ackResult, "baseline acknowledgement")
	assertDeliveredDelta(t, ackResult, observationStateByID(t, h.engine, current.ID), base,
		startObservation.ID)
	h.engine.mu.Lock()
	if baseline := baselineWithID(h.engine.baselines, h.engine.activeBaselineID); baseline == nil ||
		baseline.ObservationID != startObservation.ID {
		t.Fatalf("baseline changed during acknowledgement: %+v", baseline)
	}
	h.engine.mu.Unlock()

	var deltaChain []*protocol.ReadDelta
	var deltaStates []*observationState
	for index, checked := range []*bool{nil, boolAddress(true), boolAddress(false)} {
		marker := fmt.Sprintf("i02-delta-%d", index)
		h.device.scriptCaptures(i02Capture(marker, checked))
		result, apiErr := h.engine.Observe(context.Background(), "agent",
			fmt.Sprintf("Pi02delta%d", index), &protocol.ObserveRequest{Reference: current.ID})
		if apiErr != nil {
			t.Fatalf("delta %d: %v", index, apiErr)
		}
		current = publishedObservation(t, result, fmt.Sprintf("delta %d", index))
		state := observationStateByID(t, h.engine, current.ID)
		delta := assertDeliveredDelta(t, result, state, base, startObservation.ID)
		if delta.Base != startObservation.ID {
			t.Fatalf("delta %d switched base to %q", index, delta.Base)
		}
		deltaChain = append(deltaChain, delta)
		deltaStates = append(deltaStates, state)

		checkboxRef := refByResource(t, state, "checkbox_primary")
		applied, err := observation.ReadApply(base, delta)
		if err != nil {
			t.Fatalf("delta %d ReadApply: %v", index, err)
		}
		appliedCheckbox := readNodeByRef(observation.UnpackTree(applied).Root, checkboxRef)
		if appliedCheckbox == nil {
			t.Fatalf("delta %d ReadApply lost checkbox ref %s", index, checkboxRef)
		}
		if !sameOptionalBool(appliedCheckbox.Checked, checked) {
			t.Fatalf("delta %d applied checked=%s, want %s",
				index, checkedState(appliedCheckbox.Checked), checkedState(checked))
		}
		if set, ok := delta.Set[checkboxRef]; ok && set.Checked.Set &&
			!sameOptionalBool(set.Checked.Value, checked) {
			t.Fatalf("delta %d set checked=%s, want %s",
				index, checkedState(set.Checked.Value), checkedState(checked))
		}
		deltaBytes, err := protocol.JSON(delta)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 && !bytes.Contains(deltaBytes, []byte(`"checked":null`)) {
			t.Fatalf("delta 0 did not encode explicit checked:null: %s", deltaBytes)
		}
		if index == 1 && !bytes.Contains(deltaBytes, []byte(`"checked":true`)) {
			t.Fatalf("delta 1 did not encode checked:true: %s", deltaBytes)
		}
	}
	if len(deltaChain) != 3 {
		t.Fatalf("delta chain length=%d", len(deltaChain))
	}

	allStates := []*observationState{
		startState,
		observationStateByID(t, h.engine, ackResult.Observation.ID),
	}
	allStates = append(allStates, deltaStates...)
	assertDefaultRefsStable(t, allStates)

	// The original O and its historical detail remain byte-for-byte stable.
	startState = observationStateByID(t, h.engine, startObservation.ID)
	gotTreeBytes, err := protocol.JSON(startState.tree())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotTreeBytes, startTreeBytes) {
		t.Fatal("later deltas mutated the original internal O")
	}
	gotPublicBytes, err := protocol.JSON(startObservation.Tree.Full)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPublicBytes, startPublicBytes) {
		t.Fatal("later deltas mutated the original delivered full tree")
	}
	assertInspectionMatchesObservation(t, h.engine, startObservation.ID)

	// Switching to a full response must retain refs and tri-state. A later
	// observe(full O) acknowledges that full O as the next fixed base.
	h.device.scriptCaptures(i02Capture("i02-full", boolAddress(true)))
	fullResult, apiErr := h.engine.Observe(context.Background(), "agent", "Pi02full",
		&protocol.ObserveRequest{Reference: current.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("force full: %v", apiErr)
	}
	fullObservation := publishedObservation(t, fullResult, "forced full")
	if fullObservation.Tree.Full == nil || fullObservation.Tree.Delta != nil {
		t.Fatalf("full switch did not return a full tree: %+v", fullObservation.Tree)
	}
	fullState := observationStateByID(t, h.engine, fullObservation.ID)
	checkboxRef := refByResource(t, fullState, "checkbox_primary")
	checkbox := nodeByRef(fullState.tree().Root, checkboxRef)
	if checkbox == nil || checkedState(checkbox.Checked) != "true" {
		t.Fatalf("full switch lost known checked=true: %+v", fullState.tree())
	}
	assertDefaultRefsStable(t, []*observationState{startState, fullState})
	if !observation.ReadEqual(fullObservation.Tree.Full, fullState.Compact) {
		t.Fatal("delivered full tree differs from the committed compact tree")
	}

	// Keep checked at true so the newly acknowledged full is the shortest
	// eligible base: with the ten-revision backup window the older full could
	// otherwise win the byte comparison, while this fixture proves the
	// acknowledgement switches the chain to the new full.
	h.device.scriptCaptures(i02Capture("i02-after-full", boolAddress(true)))
	afterFullResult, apiErr := h.engine.Observe(context.Background(), "agent", "Pi02afterfull",
		&protocol.ObserveRequest{Reference: fullObservation.ID})
	if apiErr != nil {
		t.Fatalf("delta after full acknowledgement: %v", apiErr)
	}
	afterFullObservation := publishedObservation(t, afterFullResult, "delta after full")
	assertDeliveredDelta(t, afterFullResult,
		observationStateByID(t, h.engine, afterFullObservation.ID),
		fullState.Compact, fullObservation.ID)
}

// TestI04ActualProjectionPruningDeltaTopology uses Engine with the real
// observation.Builder rather than synthetic read trees. It proves that one
// tappable edge-sliver ref survives compact sliver pruning, reappearance and
// reparenting while every later delta remains relative to the acknowledged initial
// full.
func TestI04ActualProjectionPruningDeltaTopology(t *testing.T) {
	h := continuationHarness(t)
	h.device.scriptCaptures(compactCycleCapture(true, false, false, false))
	start := h.startSession()
	startObservation := publishedObservation(t, start, "start")
	startState := observationStateByID(t, h.engine, startObservation.ID)
	base := startState.Compact
	if base == nil || base.Root == nil || base.View != protocol.ViewCompact {
		t.Fatalf("start did not publish a compact full: %+v", base)
	}
	requireOrdinaryReadDefinitions(t, base.Root)
	sliverRef, ok := sourceRefForResource(startState.Projection, "compact_cycle_sliver")
	if !ok {
		t.Fatal("start projection has no sliver ref")
	}
	initialSliver := readNodeByRef(base.Root, sliverRef)
	if initialSliver == nil || initialSliver.Bounds.Kind != protocol.GeometryPoint {
		t.Fatalf("initial full did not contain the full-size sliver %q: %+v",
			sliverRef, initialSliver)
	}

	ackResult, apiErr := h.engine.Observe(context.Background(), "agent", "Pi04ack",
		&protocol.ObserveRequest{Reference: startObservation.ID})
	if apiErr != nil {
		t.Fatalf("acknowledge initial full: %v", apiErr)
	}
	current := publishedObservation(t, ackResult, "baseline acknowledgement")
	ackState := observationStateByID(t, h.engine, current.ID)
	if ackResult.Observation.Tree.Delta == nil ||
		ackResult.Observation.Tree.Delta.Base != startObservation.ID ||
		!ackResult.Observation.Tree.Delta.IsEmpty() {
		t.Fatalf("acknowledgement did not return the empty delta: %+v", ackResult.Observation.Tree)
	}
	assertDeliveredDelta(t, ackResult, ackState, base, startObservation.ID)

	type frameExpectation struct {
		name                string
		capture             captureStep
		sliverPresent       bool
		sliverParent        string
		wantLateSliverAdded bool
		wantRemoved         bool
		inputEmpty          bool
	}
	frames := []frameExpectation{
		{
			name:        "pruned_in_first_parent",
			capture:     compactCycleCapture(false, false, true, false),
			wantRemoved: true,
			inputEmpty:  true,
		},
		{
			name:          "reappeared_and_moved_to_second_parent",
			capture:       compactCycleCapture(true, true, true, false),
			sliverPresent: true, sliverParent: "cycle-second",
			inputEmpty: true,
		},
		{
			name:                "replaced_cycle_with_new_sliver",
			capture:             compactCycleCapture(false, true, true, true),
			wantRemoved:         true,
			wantLateSliverAdded: true,
			inputEmpty:          true,
		},
	}
	baseBytes, err := protocol.JSON(base)
	if err != nil {
		t.Fatalf("encode initial full: %v", err)
	}
	for _, frame := range frames {
		h.device.scriptCaptures(frame.capture)
		result, apiErr := h.engine.Observe(context.Background(), "agent",
			"Pi04"+strings.ReplaceAll(frame.name, "_", ""),
			&protocol.ObserveRequest{Reference: current.ID})
		if apiErr != nil {
			t.Fatalf("%s: %v", frame.name, apiErr)
		}
		current = publishedObservation(t, result, frame.name)
		if current.Tree.Delta == nil || current.Tree.Delta.Base != startObservation.ID {
			t.Fatalf("%s returned a non-fixed-base delta: %+v", frame.name, current.Tree)
		}
		state := observationStateByID(t, h.engine, current.ID)
		if !observation.ReadEqual(state.Compact, observation.UnpackTree(state.Compact)) {
			t.Fatalf("%s committed a read tree containing legacy chains", frame.name)
		}
		requireOrdinaryReadDefinitions(t, state.Compact.Root)

		gotSliverRef, mapped := sourceRefForResource(state.Projection, "compact_cycle_sliver")
		if !mapped || gotSliverRef != sliverRef {
			t.Fatalf("%s sliver ref=%q/%t, want stable %q", frame.name, gotSliverRef, mapped, sliverRef)
		}
		sliver := readNodeByRef(state.Compact.Root, sliverRef)
		if frame.sliverPresent {
			if sliver == nil {
				t.Fatalf("%s did not publish the full-size sliver %q", frame.name, sliverRef)
			}
			parent := readParentOf(state.Compact.Root, sliver)
			if parent == nil || !tapNodeHasLabel(parent, frame.sliverParent) {
				t.Fatalf("%s sliver parent=%+v, want anchor %q",
					frame.name, parent, frame.sliverParent)
			}
			if sliver.Bounds.Kind != protocol.GeometryPoint ||
				len(sliver.Label) != 0 ||
				!slices.Equal(sliver.Actions, []protocol.Action{protocol.ActionTap}) ||
				sliver.Value != nil || sliver.Hint != nil ||
				sliver.Resource == nil || *sliver.Resource != "compact_cycle_sliver" {
				t.Fatalf("%s compact sliver fields changed: %+v", frame.name, sliver)
			}
		} else if sliver != nil {
			t.Fatalf("%s kept the pruned sliver %q: %+v", frame.name, sliverRef, sliver)
		}

		delta := stateDeltaFromObserve(t, result, state, base)
		requireOrdinaryReadDelta(t, delta)
		if frame.wantLateSliverAdded && len(delta.Add) == 0 {
			t.Fatalf("%s did not exercise an ordinary add definition: %+v", frame.name, delta)
		}
		if frame.wantRemoved {
			if len(delta.Remove) == 0 && len(delta.Replace) == 0 {
				t.Fatalf("%s pruning emitted no structural removal: %+v", frame.name, delta)
			}
		}
		if frame.wantLateSliverAdded {
			lateSliverRef, mapped := sourceRefForResource(state.Projection, "compact_late_sliver")
			if !mapped {
				t.Fatalf("%s has no mapped late sliver ref", frame.name)
			}
			lateDefinition := readNodeInDefinitions(delta.Add, lateSliverRef)
			if lateDefinition == nil || lateDefinition.Bounds.Kind != protocol.GeometryPoint {
				t.Fatalf("%s add definitions lack late sliver %q: %+v",
					frame.name, lateSliverRef, delta.Add)
			}
			if readNodeByRef(state.Compact.Root, lateSliverRef) == nil {
				t.Fatalf("%s current full omitted the late sliver %q", frame.name, lateSliverRef)
			}
		}

		encodedDelta, err := protocol.JSON(delta)
		if err != nil {
			t.Fatalf("%s encode delta: %v", frame.name, err)
		}
		var strictDelta protocol.ReadDelta
		if err := protocol.Decode(encodedDelta, &strictDelta); err != nil {
			t.Fatalf("%s strict decode delta: %v", frame.name, err)
		}
		applied, err := observation.ReadApply(base, &strictDelta)
		if err != nil {
			t.Fatalf("%s apply strictly decoded delta: %v", frame.name, err)
		}
		currentBytes, err := protocol.JSON(state.Compact)
		if err != nil {
			t.Fatalf("%s encode current full: %v", frame.name, err)
		}
		var strictCurrent protocol.ReadTree
		if err := protocol.Decode(currentBytes, &strictCurrent); err != nil {
			t.Fatalf("%s strict decode current full: %v", frame.name, err)
		}
		if !observation.ReadEqual(applied, &strictCurrent) {
			t.Fatalf("%s applied delta differs logically from the current full", frame.name)
		}
		appliedBytes, err := protocol.JSON(applied)
		if err != nil {
			t.Fatalf("%s encode applied delta: %v", frame.name, err)
		}
		if !bytes.Equal(appliedBytes, currentBytes) {
			t.Fatalf("%s applied delta canonical bytes differ from the current full", frame.name)
		}

		inputRef, mapped := sourceRefForResource(state.Projection, "compact_cycle_input")
		if !mapped {
			t.Fatalf("%s has no mapped input ref", frame.name)
		}
		if frame.inputEmpty {
			set, exists := delta.Set[inputRef]
			if !exists || !set.Value.Set || set.Value.Value == nil || *set.Value.Value != "" {
				t.Fatalf("%s delta did not explicitly preserve an empty input value: %+v",
					frame.name, delta.Set)
			}
		}
		appliedInput := readNodeByRef(applied.Root, inputRef)
		if frame.inputEmpty {
			if appliedInput == nil || appliedInput.Value == nil || *appliedInput.Value != "" {
				t.Fatalf("%s explicit empty input value was not preserved: %+v", frame.name, appliedInput)
			}
			if appliedInput.Hint != nil || appliedInput.Label == nil ||
				appliedInput.Actions == nil {
				t.Fatalf("%s default empty fields were not preserved: %+v", frame.name, appliedInput)
			}
		}

		h.engine.mu.Lock()
		baselineID := ""
		if baseline := baselineWithID(h.engine.baselines, h.engine.activeBaselineID); baseline != nil {
			baselineID = baseline.ObservationID
		}
		h.engine.mu.Unlock()
		if baselineID != startObservation.ID {
			t.Fatalf("%s switched baseline to %q", frame.name, baselineID)
		}
	}
	afterBaseBytes, err := protocol.JSON(base)
	if err != nil {
		t.Fatalf("re-encode initial full: %v", err)
	}
	if !bytes.Equal(baseBytes, afterBaseBytes) {
		t.Fatal("delta replay mutated the confirmed initial full")
	}
}

func tapNodeHasLabel(node *protocol.ReadNode, want string) bool {
	if node == nil {
		return false
	}
	for _, label := range node.Label {
		if label == want {
			return true
		}
	}
	for _, child := range node.Children {
		if tapNodeHasLabel(child, want) {
			return true
		}
	}
	return false
}

func readNodeInDefinitions(nodes []*protocol.ReadNode, ref string) *protocol.ReadNode {
	for _, node := range nodes {
		if found := readNodeByRef(node, ref); found != nil {
			return found
		}
	}
	return nil
}

func stateDeltaFromObserve(t *testing.T, result *protocol.Result,
	state *observationState, base *protocol.ReadTree) *protocol.ReadDelta {
	t.Helper()
	if result == nil || result.Observation == nil || result.Observation.Tree == nil ||
		result.Observation.Tree.Delta == nil || result.Observation.Tree.Full != nil {
		t.Fatalf("observe did not return a delta: %+v", result)
	}
	delta := result.Observation.Tree.Delta
	unpacked, err := observation.ReadApply(base, delta)
	if err != nil {
		t.Fatalf("apply delivered delta: %v", err)
	}
	if !observation.ReadEqual(unpacked, state.Compact) {
		t.Fatalf("delivered delta does not reconstruct observation %q", state.ID)
	}
	return delta
}

type observationGateProjector struct {
	Projector
	candidate func(*Projection, func())
	preview   func(*Projection)
}

func (p *observationGateProjector) Candidate(capture *device.Capture) (*Projection, func(), error) {
	projection, commit, err := p.Projector.Candidate(capture)
	if err == nil && p.candidate != nil {
		p.candidate(projection, commit)
	}
	return projection, commit, err
}

func (p *observationGateProjector) Preview(capture *device.Capture) (*Projection, error) {
	projection, err := p.Projector.Preview(capture)
	if err == nil && p.preview != nil {
		p.preview(projection)
	}
	return projection, err
}

type i03Guard struct {
	displayID            int
	displayGeneration    string
	baselineID           string
	currentID            string
	baselineBytes        []byte
	currentTreeBytes     []byte
	currentCompactBytes  []byte
	builderPreviousBytes []byte
	nextObservation      int64
	observationCount     int
	ordinaryWork         bool
}

func takeI03Guard(t *testing.T, h *harness, builder *observation.Builder) i03Guard {
	t.Helper()
	e := h.engine
	e.mu.Lock()
	guard := i03Guard{
		nextObservation:  e.nextObservation,
		observationCount: len(e.observations),
		ordinaryWork:     e.work != nil,
	}
	if e.session != nil {
		guard.displayID = e.session.Display.ID
		guard.displayGeneration = e.session.Display.Generation
	}
	if baseline := baselineWithID(e.baselines, e.activeBaselineID); baseline != nil {
		guard.baselineID = baseline.ObservationID
		guard.baselineBytes, _ = protocol.JSON(baseline.Tree)
	}
	if e.current != nil {
		guard.currentID = e.current.ID
		guard.currentTreeBytes, _ = protocol.JSON(e.current.tree())
		guard.currentCompactBytes, _ = protocol.JSON(e.current.Compact)
	}
	e.mu.Unlock()
	previous := builder.Previous()
	if previous == nil {
		t.Fatal("I03 baseline has no projector continuity")
	}
	var err error
	guard.builderPreviousBytes, err = protocol.JSON(previous.Tree)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func assertI03Guard(t *testing.T, h *harness, builder *observation.Builder, before i03Guard) {
	t.Helper()
	after := takeI03Guard(t, h, builder)
	if after.baselineID != before.baselineID || !bytes.Equal(after.baselineBytes, before.baselineBytes) {
		t.Fatalf("late candidate changed baseline: %q -> %q", before.baselineID, after.baselineID)
	}
	if after.currentID != before.currentID ||
		!bytes.Equal(after.currentTreeBytes, before.currentTreeBytes) ||
		!bytes.Equal(after.currentCompactBytes, before.currentCompactBytes) {
		t.Fatalf("late candidate changed the public snapshot: %q -> %q", before.currentID, after.currentID)
	}
	if !bytes.Equal(after.builderPreviousBytes, before.builderPreviousBytes) {
		t.Fatal("late candidate advanced projector continuity/high-water state")
	}
	if after.nextObservation != before.nextObservation {
		t.Fatalf("late candidate allocated O id: %d -> %d", before.nextObservation, after.nextObservation)
	}
	if after.observationCount != before.observationCount {
		t.Fatalf("late candidate changed retained observations: %d -> %d",
			before.observationCount, after.observationCount)
	}
	if after.ordinaryWork {
		t.Fatal("ordinary work remained after rejected publication")
	}
}

func i03Baseline(t *testing.T) (*harness, *protocol.Result, *protocol.Observation, Projector, *observation.Builder) {
	t.Helper()
	h := continuationHarness(t)
	h.device.scriptCaptures(continuationCapture("i03-base", boolAddress(false)))
	start := h.startSession()
	startObservation := publishedObservation(t, start, "start")
	ack, apiErr := h.engine.Observe(context.Background(), "agent", "Pi03baseack",
		&protocol.ObserveRequest{Reference: startObservation.ID})
	if apiErr != nil {
		t.Fatalf("I03 baseline acknowledgement: %v", apiErr)
	}
	current := publishedObservation(t, ack, "baseline acknowledgement")
	h.engine.mu.Lock()
	baselineID := ""
	if baseline := baselineWithID(h.engine.baselines, h.engine.activeBaselineID); baseline != nil {
		baselineID = baseline.ObservationID
	}
	h.engine.mu.Unlock()
	if baselineID != startObservation.ID {
		t.Fatalf("I03 baseline=%q, want %q", baselineID, startObservation.ID)
	}
	projector := h.engine.projector
	testProjector, ok := projector.(*testProjector)
	if !ok {
		t.Fatalf("unexpected projector type %T", projector)
	}
	return h, start, current, projector, testProjector.builder
}

func applyI03Mutation(t *testing.T, h *harness, start *protocol.Result, mutation string) {
	t.Helper()
	switch mutation {
	case "stop":
		response, apiErr := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
			AppInstanceID: h.appInstance, StopSeq: 1, Reason: "continuity_i03_stop",
		})
		if apiErr != nil || response == nil {
			t.Fatalf("STOP gate: response=%+v error=%v", response, apiErr)
		}
	case "end":
		result, apiErr := h.engine.End("agent", "Pi03end", start.OperationID)
		if apiErr != nil || result == nil {
			t.Fatalf("end gate: result=%+v error=%v", result, apiErr)
		}
	case "display_generation":
		h.engine.mu.Lock()
		h.engine.session.Display.Generation = "i03-generation-changed"
		h.engine.mu.Unlock()
	case "display_id":
		h.engine.mu.Lock()
		h.engine.session.Display.ID++
		h.engine.mu.Unlock()
	case "owner":
		h.engine.mu.Lock()
		h.engine.session.AppInstanceID = "i03-owner-changed"
		h.engine.mu.Unlock()
	case "expiry":
		h.engine.mu.Lock()
		h.engine.session.ExpiresAt = time.Now().Add(-time.Millisecond)
		h.engine.mu.Unlock()
	default:
		t.Fatalf("unknown I03 mutation %q", mutation)
	}
}

// TestI03LateCandidateAndPreviewRejected places a deterministic barrier after
// a real Candidate or Preview returns. Each mutation crosses that barrier before
// the single publication funnel runs.
func TestI03LateCandidateAndPreviewRejected(t *testing.T) {
	for _, route := range []string{"candidate", "preview", "delivery"} {
		for _, mutation := range []string{"stop", "end", "display_generation", "display_id", "owner", "expiry"} {
			t.Run(route+"/"+mutation, func(t *testing.T) {
				h, start, current, baseProjector, builder := i03Baseline(t)
				buttonRef, _ := sourceRefForResource(observationStateByID(t, h.engine, current.ID).Projection,
					"action_primary")
				before := takeI03Guard(t, h, builder)
				resource := fmt.Sprintf("i03_%s_%s_extra", route, mutation)
				h.device.scriptCaptures(continuationCapture("i03-late-"+route+"-"+mutation,
					boolAddress(true), resource))

				entered := make(chan struct{})
				release := make(chan struct{})
				generatedRef := make(chan string, 1)
				var releaseOnce sync.Once
				defer releaseOnce.Do(func() { close(release) })
				var hookOnce sync.Once
				hook := func(projection *Projection) {
					hookOnce.Do(func() {
						ref, _ := sourceRefForResource(projection, resource)
						generatedRef <- ref
						close(entered)
						<-release
					})
				}
				gate := &observationGateProjector{Projector: baseProjector}
				if route != "preview" {
					gate.candidate = func(projection *Projection, _ func()) { hook(projection) }
				} else {
					gate.preview = hook
				}
				h.engine.projector = gate

				done := make(chan callResult, 1)
				go func() {
					switch route {
					case "candidate":
						result, apiErr := h.engine.Observe(context.Background(), "agent", "Pi03candidate",
							&protocol.ObserveRequest{Reference: current.ID})
						done <- callResult{result: result, err: apiErr}
					case "preview":
						result, apiErr := h.engine.WaitFor(context.Background(), "agent", "Pi03preview",
							&protocol.WaitForRequest{
								Reference: current.ID,
								Stable:    true,
								Timeout:   1,
							})
						done <- callResult{result: result, err: apiErr}
					case "delivery":
						result, apiErr := h.engine.Act(context.Background(), "agent", "Pi03delivery",
							h.tapRefRequest(t, current.ID, buttonRef))
						done <- callResult{result: result, err: apiErr}
					}
				}()

				awaitSignal(t, entered)
				lateRef := <-generatedRef
				if lateRef == "" {
					t.Fatalf("%s did not generate its new public ref", route)
				}
				applyI03Mutation(t, h, start, mutation)
				releaseOnce.Do(func() { close(release) })
				result := awaitCall(t, done)
				if result != nil && result.Observation != nil && result.Observation.ID != "" {
					t.Fatalf("%s/%s published a late O: %+v", route, mutation, result.Observation)
				}
				assertI03Guard(t, h, builder, before)
				if len(h.device.taps) != 0 {
					t.Fatalf("%s/%s dispatched input during rejected publication", route, mutation)
				}

				// The rejected candidate's semantic leaf must still be absent
				// from the untouched public snapshot.
				h.engine.mu.Lock()
				currentState := h.engine.observations[current.ID]
				h.engine.mu.Unlock()
				if currentState == nil {
					t.Fatalf("original current O %q disappeared", current.ID)
				}
				if leaked, ok := sourceRefForResource(currentState.Projection, resource); ok {
					t.Fatalf("late candidate leaked ref %q into the old snapshot", leaked)
				}

				if mutation == "display_generation" || mutation == "display_id" {
					// Restore only the test mutation and publish a different
					// leaf. Reusing the rejected number proves neither Candidate
					// nor Preview advanced nextRef.
					h.engine.mu.Lock()
					h.engine.session.Display.ID = before.displayID
					h.engine.session.Display.Generation = before.displayGeneration
					h.engine.mu.Unlock()
					replacement := resource + "_replacement"
					h.device.scriptCaptures(continuationCapture(
						"i03-replacement-"+route, boolAddress(false), replacement))
					replacementResult, apiErr := h.engine.Observe(context.Background(), "agent",
						"Pi03replacement", &protocol.ObserveRequest{Reference: current.ID})
					if apiErr != nil {
						t.Fatalf("%s replacement publication: %v", route, apiErr)
					}
					replacementObservation := publishedObservation(t, replacementResult,
						route+" replacement")
					replacementState := observationStateByID(t, h.engine, replacementObservation.ID)
					replacementRef, ok := sourceRefForResource(replacementState.Projection, replacement)
					if !ok {
						t.Fatalf("%s replacement leaf is absent", route)
					}
					if replacementRef != lateRef {
						t.Fatalf("%s advanced high-water after rejection: rejected=%s replacement=%s",
							route, lateRef, replacementRef)
					}
				}
			})
		}
	}
}
