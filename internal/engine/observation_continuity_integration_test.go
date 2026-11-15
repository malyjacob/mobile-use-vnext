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
				Index: index, Parent: 1, Depth: 2, DisplayID: display.ID, Visible: true,
				RawBounds: device.Rect{Left: 20, Top: 360 + 80*index, Right: 600, Bottom: 420 + 80*index},
				Bounds:    device.Rect{Left: 20, Top: 360 + 80*index, Right: 600, Bottom: 420 + 80*index},
				Package:   "com.example", ClassName: "android.widget.TextView",
				Text: "extra:" + resource, TextSet: true,
				ResourceSet: true, ResourceID: resource,
				HasEnabled: true, Enabled: true,
			})
			nodes[1].Children = append(nodes[1].Children, index)
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

// assertReadContentConsistent compares the logical content emitted by compact
// and detailed views. Geometry is intentionally excluded: compact may mark an
// anonymous node's known rectangle hidden while detailed reveals it.
func assertReadContentConsistent(t *testing.T, compact, detailed *protocol.ReadTree) {
	t.Helper()
	compact = observation.UnpackTree(compact)
	detailed = observation.UnpackTree(detailed)
	if compact == nil || detailed == nil {
		t.Fatalf("missing compact/detailed tree: %+v %+v", compact, detailed)
	}
	var walk func(*protocol.ReadNode, *protocol.ReadNode)
	walk = func(left, right *protocol.ReadNode) {
		t.Helper()
		if left == nil || right == nil {
			if left != right {
				t.Fatalf("compact/detailed nil mismatch: %+v %+v", left, right)
			}
			return
		}
		if left.Ref != right.Ref || left.Role != right.Role || left.Context != right.Context ||
			left.Axis != right.Axis ||
			!slices.Equal(left.Label, right.Label) ||
			!slices.Equal(left.Actions, right.Actions) ||
			!sameOptionalBool(left.Enabled, right.Enabled) ||
			!sameOptionalBool(left.Focused, right.Focused) ||
			!sameOptionalBool(left.Selected, right.Selected) ||
			!sameOptionalBool(left.Protected, right.Protected) ||
			!sameOptionalBool(left.Checked, right.Checked) ||
			!sameOptionalString(left.Value, right.Value) ||
			!sameOptionalString(left.Hint, right.Hint) ||
			!sameOptionalString(left.Resource, right.Resource) ||
			len(left.Children) != len(right.Children) {
			t.Fatalf("compact/detailed content mismatch at %q:\ncompact=%+v\ndetailed=%+v",
				left.Ref, left, right)
		}
		for index := range left.Children {
			walk(left.Children[index], right.Children[index])
		}
	}
	walk(compact.Root, detailed.Root)
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

	result, apiErr := e.Inspect("agent", &protocol.InspectRequest{ObservationID: id})
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
	if e.baseline != nil {
		guard.baselineID = e.baseline.ObservationID
		guard.baselineBytes, _ = protocol.JSON(e.baseline.Tree)
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
			Kind:          protocol.AppKindOpen,
			ObservationID: waitObservation.ID,
			App:           "com.example.app",
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
		&protocol.InspectRequest{ObservationID: startObservation.ID})
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
	h.device.scriptCaptures(continuationCapture("i02-base", boolAddress(false)))
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
	if h.engine.baseline == nil || h.engine.baseline.ObservationID != startObservation.ID {
		t.Fatalf("baseline changed during acknowledgement: %+v", h.engine.baseline)
	}
	h.engine.mu.Unlock()

	var deltaChain []*protocol.ReadDelta
	var deltaStates []*observationState
	for index, checked := range []*bool{nil, boolAddress(true), boolAddress(false)} {
		marker := fmt.Sprintf("i02-delta-%d", index)
		h.device.scriptCaptures(continuationCapture(marker, checked))
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
	h.device.scriptCaptures(continuationCapture("i02-full", boolAddress(true)))
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

	h.device.scriptCaptures(continuationCapture("i02-after-full", boolAddress(false)))
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
	if e.baseline != nil {
		guard.baselineID = e.baseline.ObservationID
		guard.baselineBytes, _ = protocol.JSON(e.baseline.Tree)
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
	if h.engine.baseline != nil {
		baselineID = h.engine.baseline.ObservationID
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
