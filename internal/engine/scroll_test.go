package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- page builder

// rawNodeSpec describes one raw node of a synthetic page. Every node carries
// enough source evidence to be published; scrollable nodes become public
// scroll containers.
type rawNodeSpec struct {
	Parent     int
	Bounds     device.Rect
	Class      string
	Text       string
	Scrollable bool
	Clickable  bool
	Disabled   bool
	Hidden     bool
}

// buildCapture builds one raw capture from specs. Spec 0 is the root.
func buildCapture(display device.Display, specs []rawNodeSpec) device.Capture {
	nodes := make([]device.RawNode, 0, len(specs))
	for index, spec := range specs {
		node := device.RawNode{
			Index: index, Parent: spec.Parent, DisplayID: display.ID,
			RawBounds: spec.Bounds, Bounds: spec.Bounds, Visible: !spec.Hidden,
			Package: "com.example", ClassName: spec.Class,
			Text: spec.Text, TextSet: spec.Text != "",
			HasEnabled: true, Enabled: !spec.Disabled,
			HasScrollable: spec.Scrollable, Scrollable: spec.Scrollable,
			HasClickable: spec.Clickable, Clickable: spec.Clickable,
		}
		if spec.Parent >= 0 && spec.Parent < len(nodes) {
			nodes[spec.Parent].Children = append(nodes[spec.Parent].Children, index)
		}
		nodes = append(nodes, node)
	}
	return device.Capture{
		Display: display, Nodes: nodes, Root: 0,
		Foreground: device.Foreground{State: device.ForegroundPresent,
			Package: "com.example", TaskID: intPtr(42)},
		CapturedAt: time.Now(),
	}
}

func screenRect() device.Rect {
	return device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
}

// scrollPageSpecs is the ordinary controlled-scroll page: a full-screen scroll
// container with three text units wrapping content.
func scrollPageSpecs(container, content device.Rect) []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: container, Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: content.Top, Right: 1080, Bottom: content.Top + 200},
			Class: "android.widget.TextView", Text: "alpha"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: content.Top + 300, Right: 1080, Bottom: content.Top + 500},
			Class: "android.widget.TextView", Text: "bravo"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: content.Top + 600, Right: 1080, Bottom: content.Top + 800},
			Class: "android.widget.TextView", Text: "charlie"},
	}
}

func scrollPageStep(specs []rawNodeSpec) captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		return buildCapture(display, specs), nil
	}
}

// scrollPageTree returns a capture step plus a mutable spec slice so a test can
// derive the "content moved" post frame from the same page.
func scrollPageSpecsMoved(base []rawNodeSpec, delta int) []rawNodeSpec {
	moved := make([]rawNodeSpec, len(base))
	for index, spec := range base {
		if index >= 2 {
			spec.Bounds.Top += delta
			spec.Bounds.Bottom += delta
		}
		moved[index] = spec
	}
	return moved
}

// ---------------------------------------------------------------- test helpers

func (h *harness) actP(t *testing.T, p, observationID string, operation protocol.Operation) *protocol.Result {
	t.Helper()
	result, apiErr := h.engine.Act(context.Background(), "agent", p, &protocol.ActRequest{
		ObservationID: observationID,
		Operation:     operation,
	})
	if apiErr != nil {
		t.Fatalf("act %s rejected at admission: %v", operation.Type, apiErr)
	}
	return result
}

func findPublic(root *protocol.Node, match func(*protocol.Node) bool) *protocol.Node {
	if root == nil {
		return nil
	}
	if match(root) {
		return root
	}
	for _, child := range root.Children {
		if found := findPublic(child, match); found != nil {
			return found
		}
	}
	return nil
}

func scrollContainerRef(t *testing.T, h *harness) string {
	t.Helper()
	node := findRole(currentTree(t, h.engine).Root, protocol.RoleScroll)
	if node == nil {
		t.Fatal("synthetic page has no scroll container")
	}
	return node.Ref
}

func swipeCount(h *harness) int {
	h.device.mu.Lock()
	defer h.device.mu.Unlock()
	return len(h.device.swipes)
}

func swipeCalls(h *harness) []swipeCall {
	h.device.mu.Lock()
	defer h.device.mu.Unlock()
	return append([]swipeCall(nil), h.device.swipes...)
}

func requireNoEffect(t *testing.T, result *protocol.Result) {
	t.Helper()
	if result.Effect != nil {
		t.Fatalf("result carries effect %+v, want omitted", result.Effect)
	}
}

func requireEffect(t *testing.T, result *protocol.Result, state string) {
	t.Helper()
	if result.Effect == nil || result.Effect.State != state {
		t.Fatalf("effect = %+v, want %s", result.Effect, state)
	}
}

func requireScrollRejected(t *testing.T, h *harness, observationID string, operation protocol.Operation, code string) {
	t.Helper()
	captures := h.device.captureCount()
	swipes := swipeCount(h)
	result := h.actP(t, "", observationID, operation)
	requireNotExecuted(t, result, code)
	requireNoEffect(t, result)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("static rejection performed %d new capture(s)", got-captures)
	}
	if got := swipeCount(h); got != swipes {
		t.Fatalf("static rejection performed %d swipe(s)", got-swipes)
	}
	if status := observationStatus(h, observationID); status != ObservationReady {
		t.Fatalf("static rejection left observation %q, want ready", status)
	}
}

// ---------------------------------------------------------------- static tests

func TestScrollRefContainerAcceptedAndDispatched(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(
		scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})),
		scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})),
	)

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("scroll outcome = %s error %+v", result.Outcome, result.Error)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("scroll published no post observation")
	}
	calls := swipeCalls(h)
	if len(calls) != 1 {
		t.Fatalf("swipe count = %d, want exactly one dispatched scroll", len(calls))
	}
	// Full-screen container: margin 240, usable 1920, page travel floor(1152).
	want := swipeCall{displayID: 12, x1: 540, y1: 2160, x2: 540, y2: 1008, durationMS: 400}
	if calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls[0], want)
	}
	requireEffect(t, result, protocol.EffectNotObserved)
}

func TestScrollRejectsNonContainersWithoutClaim(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
		// A disabled scroll container is still published, but is not authorized.
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 1500, Right: 1080, Bottom: 2300},
			Class: "android.widget.ScrollView", Scrollable: true, Disabled: true},
		// A scroll container fully outside the display has no public bounds.
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 3000, Right: 1080, Bottom: 4000},
			Class: "android.widget.ScrollView", Scrollable: true},
		// A hidden wrapper is published as context and is not a container.
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 2000, Right: 1080, Bottom: 2100},
			Class: "android.widget.FrameLayout", Hidden: true},
		{Parent: 5, Bounds: device.Rect{Left: 0, Top: 2000, Right: 1080, Bottom: 2100},
			Class: "android.widget.TextView", Text: "context-child"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	root := currentTree(t, h.engine).Root

	leaf := findPublic(root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleText && len(node.Label) > 0 && node.Label[0] == "alpha"
	})
	disabled := findPublic(root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleScroll && node.Enabled != nil && !*node.Enabled
	})
	noBounds := findPublic(root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleScroll && node.Bounds == nil
	})
	contextNode := findPublic(root, func(node *protocol.Node) bool { return node.Context })
	if leaf == nil || disabled == nil || noBounds == nil || contextNode == nil {
		t.Fatalf("fixture published nodes missing: leaf=%v disabled=%v noBounds=%v context=%v",
			leaf != nil, disabled != nil, noBounds != nil, contextNode != nil)
	}

	for name, operation := range map[string]protocol.Operation{
		"root": {Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: root.Ref},
			Direction: protocol.ScrollDirectionDown},
		"leaf": {Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: leaf.Ref},
			Direction: protocol.ScrollDirectionDown},
		"disabled": {Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: disabled.Ref},
			Direction: protocol.ScrollDirectionDown},
		"no_bounds": {Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: noBounds.Ref},
			Direction: protocol.ScrollDirectionDown},
		"context": {Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: contextNode.Ref},
			Direction: protocol.ScrollDirectionDown},
		"missing": {Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: "n9999"},
			Direction: protocol.ScrollDirectionDown},
	} {
		t.Run(name, func(t *testing.T) {
			code := protocol.CodeNodeUnsupported
			if name == "missing" {
				code = protocol.CodeNodeNotFound
			}
			requireScrollRejected(t, h, start.Observation.ID, operation, code)
		})
	}
}

func TestScrollPointSameContainerCandidatesAccepted(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		// Two overlapping same-depth content candidates inside one container.
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.view.View", Text: "overlay-text"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{X: 540, Y: 1100},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("point scroll outcome = %s error %+v", result.Outcome, result.Error)
	}
	if calls := swipeCalls(h); len(calls) != 1 {
		t.Fatalf("swipe count = %d, want one", len(calls))
	}
}

func TestScrollPointTwoScrollScopesAmbiguous(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1600},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 1000, Right: 1080, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()

	captures := h.device.captureCount()
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{X: 540, Y: 1100},
		Direction: protocol.ScrollDirectionDown,
	})
	requireNotExecuted(t, result, protocol.CodeAmbiguousPoint)
	if result.Error == nil || !strings.Contains(result.Error.Message, "container.ref") {
		t.Fatalf("ambiguous message is not actionable: %+v", result.Error)
	}
	if h.device.captureCount() != captures || swipeCount(h) != 0 {
		t.Fatal("ambiguous static resolution performed device work")
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation = %q, want ready", status)
	}
}

func TestScrollPointWithoutContainerUnsupported(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200},
			Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 2250, Right: 1080, Bottom: 2350},
			Class: "android.widget.TextView", Text: "footer"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()

	requireScrollRejected(t, h, start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{X: 540, Y: 2300},
		Direction: protocol.ScrollDirectionDown,
	}, protocol.CodeNodeUnsupported)
}

func TestScrollTooSmallContainerRejectedBeforeClaim(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 200},
			Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 110, Right: 1080, Bottom: 190},
			Class: "android.widget.TextView", Text: "alpha"},
		{Parent: 0, Bounds: device.Rect{Left: 0, Top: 2200, Right: 1080, Bottom: 2300},
			Class: "android.widget.Button", Text: "retry", Clickable: true},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	container := scrollContainerRef(t, h)

	for _, amount := range []string{protocol.ScrollAmountPage, protocol.ScrollAmountHalf} {
		requireScrollRejected(t, h, start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
			Direction: protocol.ScrollDirectionDown, Amount: amount,
		}, protocol.CodeNodeUnsupported)
	}

	// The same observation is still usable by a later action.
	button := findPublic(currentTree(t, h.engine).Root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleButton
	})
	if button == nil {
		t.Fatal("fixture button missing")
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	result := h.actP(t, "P-after", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("reuse outcome = %s error %+v", result.Outcome, result.Error)
	}
}

func TestScrollDefensiveValidation(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	start := h.startSession()
	container := &protocol.Target{UseRef: true, Ref: scrollContainerRef(t, h)}

	for name, operation := range map[string]protocol.Operation{
		"missing_container": {Type: "scroll", Direction: protocol.ScrollDirectionDown},
		"bad_direction":     {Type: "scroll", Container: container, Direction: "sideways"},
		"bad_amount":        {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown, Amount: "full"},
		"target": {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown,
			Target: &protocol.Target{UseRef: true, Ref: "n1"}},
		"from": {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown,
			From: &protocol.Point{X: 1, Y: 2}},
		"to": {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown,
			To: &protocol.Point{X: 1, Y: 2}},
		"key": {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown,
			Key: "BACK"},
		"text": {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown,
			Text: "hello"},
		"duration": {Type: "scroll", Container: container, Direction: protocol.ScrollDirectionDown,
			DurationMS: 300},
	} {
		t.Run(name, func(t *testing.T) {
			requireScrollRejected(t, h, start.Observation.ID, operation, protocol.CodeInvalidRequest)
		})
	}

	// A static rejection never records the scroll action fact.
	root := currentTree(t, h.engine).Root
	rejected := h.actP(t, "Pstaticfail", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: root.Ref},
		Direction: protocol.ScrollDirectionDown,
	})
	requireNotExecuted(t, rejected, protocol.CodeNodeUnsupported)
	h.engine.mu.Lock()
	action := h.engine.operations["Pstaticfail"].ActAction
	h.engine.mu.Unlock()
	if action != "" {
		t.Fatalf("static rejection recorded action %q, want none", action)
	}
}

func TestScrollBusyKeepsObservationReady(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.engine.mu.Lock()
	h.engine.work = &workState{OperationID: "Pblock", Agent: "agent", Kind: WorkObserve,
		Phase: "capture", SessionEpoch: h.engine.session.Epoch, Start: time.Now(),
		Ctx: context.Background(), Cancel: func() {}}
	h.engine.mu.Unlock()
	captures, swipes := h.device.captureCount(), swipeCount(h)

	result := h.actP(t, "Pbusy", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	requireNotExecuted(t, result, protocol.CodeDeviceBusy)
	requireNoEffect(t, result)
	if h.device.captureCount() != captures || swipeCount(h) != swipes {
		t.Fatal("busy scroll performed device work")
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("busy scroll consumed the observation: %q", status)
	}
	// status(P) and duplicate P know the action but must not fabricate effect.
	status, apiErr := h.engine.Status("agent", "Pbusy")
	if apiErr != nil {
		t.Fatalf("status(P) after busy: %v", apiErr)
	}
	if status.Outcome != OutcomeNotExecuted || status.Effect != nil {
		t.Fatalf("busy status(P) = %+v, want not_executed without effect", status)
	}
	duplicate, apiErr := h.engine.Act(context.Background(), "agent", "Pbusy", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation: protocol.Operation{Type: "scroll",
			Container: &protocol.Target{UseRef: true, Ref: container}, Direction: protocol.ScrollDirectionDown},
	})
	if apiErr != nil || duplicate.Outcome != OutcomeNotExecuted || duplicate.Effect != nil {
		t.Fatalf("duplicate busy P = %+v, apiErr=%v", duplicate, apiErr)
	}
	h.engine.mu.Lock()
	h.engine.work = nil
	h.engine.mu.Unlock()

	// The same observation is still usable by a new P.
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	retry := h.actP(t, "Pretry", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if retry.Outcome != OutcomeExecuted {
		t.Fatalf("retry outcome = %s error %+v", retry.Outcome, retry.Error)
	}
}

func TestScrollPlanRecordsControlledFacts(t *testing.T) {
	var buffer strings.Builder
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000})))
	result := h.actP(t, "P-log", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("scroll outcome = %s error %+v", result.Outcome, result.Error)
	}
	logger.Close()
	recorded := buffer.String()
	for _, want := range []string{
		"scroll.plan", "scroll.revalidate", "scroll.effect",
		"P-log", `direction="down"`, `amount="page"`, `container_role="scroll"`,
		`start_edge="bottom"`, `end_edge="top"`,
		"container_proven=true", "start_scope_proven=true", "end_scope_proven=true",
		`coverage=100`, `anchors=3`, `dominant_axis="y"`,
	} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("event log lacks %q:\n%s", want, recorded)
		}
	}
	for _, forbidden := range []string{" ref=", " x=", " y=", " text=", "alpha", "bravo"} {
		if strings.Contains(recorded, forbidden) {
			t.Fatalf("event log carries %q:\n%s", forbidden, recorded)
		}
	}
	for _, line := range strings.Split(recorded, "\n") {
		if strings.Contains(line, "scroll.effect") && strings.Contains(line, "reason=") {
			t.Fatalf("a decided effect must not carry an unobserved reason: %s", line)
		}
	}
}

// ---------------------------------------------------------------- log facts

// TestActPointLogCarriesOperationID proves the existing coordinate decision
// event now names the operation and still carries no point, ref or text.
func TestActPointLogCarriesOperationID(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	h.device.scriptCaptures(defaultCaptureStep)
	start := h.startSession()

	result := h.actP(t, "P-point-log", start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("tap outcome = %s error %+v", result.Outcome, result.Error)
	}
	logger.Close()
	recorded := buffer.String()
	found := false
	for _, line := range strings.Split(recorded, "\n") {
		if !strings.Contains(line, "act.point") {
			continue
		}
		found = true
		if !strings.Contains(line, `operation_id="P-point-log"`) {
			t.Fatalf("act.point lacks operation_id: %s", line)
		}
		if !strings.Contains(line, "proven=true") {
			t.Fatalf("act.point lacks the proof fact: %s", line)
		}
	}
	if !found {
		t.Fatalf("no act.point event recorded:\n%s", recorded)
	}
	for _, forbidden := range []string{" ref=", " x=", " y=", " text="} {
		if strings.Contains(recorded, forbidden) {
			t.Fatalf("act.point log carries %q:\n%s", forbidden, recorded)
		}
	}
}

// TestScrollLogWhitelistDropsUncontrolledField is the reverse control of the
// single event whitelist: only controlled keys survive, so a free-text field
// can never reach the log even when an event asks for it.
func TestScrollLogWhitelistDropsUncontrolledField(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	logger.Event(eventlog.Debug, "scroll.effect",
		eventlog.Str("operation_id", "P1"),
		eventlog.Str("state", protocol.EffectUnknown),
		eventlog.Int("coverage", 50),
		eventlog.Str("ref", "n1"),
		eventlog.Str("text", "secret"))
	logger.Close()
	recorded := buffer.String()
	for _, want := range []string{`operation_id="P1"`, `state="unknown"`, "coverage=50"} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("whitelisted field %q dropped:\n%s", want, recorded)
		}
	}
	for _, forbidden := range []string{"ref=", "text=", "secret"} {
		if strings.Contains(recorded, forbidden) {
			t.Fatalf("uncontrolled field %q reached the log:\n%s", forbidden, recorded)
		}
	}
}
