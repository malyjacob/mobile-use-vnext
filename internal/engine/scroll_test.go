package engine

import (
	"bytes"
	"context"
	"fmt"
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
		"proven=true",
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

// ---------------------------------------------------------------- safe span

// scrollTabPageSpecs is the synthetic geometry of the field report: a main list
// container whose upper part is covered by a same-level scrollable tab bar.
func scrollTabPageSpecs() []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: device.Rect{Right: 1080, Bottom: 2242}, Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 0, Bounds: device.Rect{Left: 23, Top: 169, Right: 1058, Bottom: 304},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
	}
}

// scrollContainerMatching returns the single public scroll container whose
// rectangle equals rect. It is used to select one container out of a page with
// several published scroll scopes.
func scrollContainerMatching(t *testing.T, h *harness, rect device.Rect) string {
	t.Helper()
	node := findPublic(currentTree(t, h.engine).Root, func(node *protocol.Node) bool {
		return node.Role == protocol.RoleScroll && node.Bounds != nil &&
			rectFromBounds(node.Bounds) == rect
	})
	if node == nil {
		t.Fatalf("no published scroll container at %+v", rect)
	}
	return node.Ref
}

// TestScrollSafeSpanAvoidsTabBar pins the fixed center-line span selection on
// the synthetic tab-bar page. The longest provable interval is [304,2242), so
// every direction and amount is planned relative to it. A plan that used the
// whole container rectangle would produce the old coordinates and fail here.
func TestScrollSafeSpanAvoidsTabBar(t *testing.T) {
	cases := []struct {
		name      string
		direction string
		amount    string
		want      swipeCall
	}{
		// Axis extent 1938: margin 193, usable 1552, page 931, half 465.
		{"down_page", protocol.ScrollDirectionDown, protocol.ScrollAmountPage,
			swipeCall{displayID: 12, x1: 540, y1: 2049, x2: 540, y2: 1118, durationMS: 400}},
		{"up_page", protocol.ScrollDirectionUp, protocol.ScrollAmountPage,
			swipeCall{displayID: 12, x1: 540, y1: 497, x2: 540, y2: 1428, durationMS: 400}},
		{"down_half", protocol.ScrollDirectionDown, protocol.ScrollAmountHalf,
			swipeCall{displayID: 12, x1: 540, y1: 2049, x2: 540, y2: 1584, durationMS: 400}},
		{"up_half", protocol.ScrollDirectionUp, protocol.ScrollAmountHalf,
			swipeCall{displayID: 12, x1: 540, y1: 497, x2: 540, y2: 962, durationMS: 400}},
		// The tab bar is off the horizontal center line, so the full width stays
		// provable: margin 108, usable 864, page 518, half 259.
		{"right_page", protocol.ScrollDirectionRight, protocol.ScrollAmountPage,
			swipeCall{displayID: 12, x1: 972, y1: 1121, x2: 454, y2: 1121, durationMS: 400}},
		{"left_page", protocol.ScrollDirectionLeft, protocol.ScrollAmountPage,
			swipeCall{displayID: 12, x1: 108, y1: 1121, x2: 626, y2: 1121, durationMS: 400}},
		{"right_half", protocol.ScrollDirectionRight, protocol.ScrollAmountHalf,
			swipeCall{displayID: 12, x1: 972, y1: 1121, x2: 713, y2: 1121, durationMS: 400}},
		{"left_half", protocol.ScrollDirectionLeft, protocol.ScrollAmountHalf,
			swipeCall{displayID: 12, x1: 108, y1: 1121, x2: 367, y2: 1121, durationMS: 400}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			page := scrollPageStep(scrollTabPageSpecs())
			h.device.scriptCaptures(page)
			start := h.startSession()
			container := scrollContainerMatching(t, h, device.Rect{Right: 1080, Bottom: 2242})
			h.device.scriptCaptures(page, page)

			result := h.actP(t, "", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
				Direction: testCase.direction, Amount: testCase.amount,
			})
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
			}
			calls := swipeCalls(h)
			if len(calls) != 1 {
				t.Fatalf("swipes = %d, want exactly one", len(calls))
			}
			if calls[0] != testCase.want {
				t.Fatalf("swipe = %+v, want %+v", calls[0], testCase.want)
			}
		})
	}
}

// scrollSpanLengthPage builds a page whose provable span on the center line is
// exactly [0,length): one inner scroll scope covers everything above it.
func scrollSpanLengthPage(length int) []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Right: 1080, Top: length, Bottom: 2400},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
	}
}

// TestScrollSafeSpanTravelThresholds covers both sides of the fixed 48px travel
// minimum relative to the selected span, not the container: 127/128px for page
// and 207/208px for half.
func TestScrollSafeSpanTravelThresholds(t *testing.T) {
	cases := []struct {
		length     int
		amount     string
		ok         bool
		start, end int
	}{
		// extent 127: margin 24, usable 79, page 47, half 23 - both too short.
		{length: 127, amount: protocol.ScrollAmountPage, ok: false},
		{length: 127, amount: protocol.ScrollAmountHalf, ok: false},
		// extent 128: usable 80, page 48 (the exact minimum), half 24.
		{length: 128, amount: protocol.ScrollAmountPage, ok: true, start: 104, end: 56},
		{length: 128, amount: protocol.ScrollAmountHalf, ok: false},
		// extent 207: usable 159, page 95, half 47.
		{length: 207, amount: protocol.ScrollAmountPage, ok: true, start: 183, end: 88},
		{length: 207, amount: protocol.ScrollAmountHalf, ok: false},
		// extent 208: usable 160, page 96, half 48.
		{length: 208, amount: protocol.ScrollAmountPage, ok: true, start: 184, end: 88},
		{length: 208, amount: protocol.ScrollAmountHalf, ok: true, start: 184, end: 136},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("%d_%s", testCase.length, testCase.amount), func(t *testing.T) {
			h := newHarness(t)
			page := scrollPageStep(scrollSpanLengthPage(testCase.length))
			h.device.scriptCaptures(page)
			start := h.startSession()
			container := scrollContainerRef(t, h)
			operation := protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
				Direction: protocol.ScrollDirectionDown, Amount: testCase.amount,
			}
			if !testCase.ok {
				requireScrollRejected(t, h, start.Observation.ID, operation,
					protocol.CodeNodeUnsupported)
				return
			}
			h.device.scriptCaptures(page, page)
			result := h.actP(t, "", start.Observation.ID, operation)
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
			}
			calls := swipeCalls(h)
			if len(calls) != 1 {
				t.Fatalf("swipes = %d, want one", len(calls))
			}
			want := swipeCall{displayID: 12, x1: 540, y1: testCase.start, x2: 540,
				y2: testCase.end, durationMS: 400}
			if calls[0] != want {
				t.Fatalf("swipe = %+v, want %+v", calls[0], want)
			}
		})
	}
}

// TestScrollSafeSpanFullyBlockedRejectedBeforeClaim proves a fully blocked
// center line costs no device work and leaves the observation ready. Removing
// the static span proof would claim the observation and read a fresh frame.
func TestScrollSafeSpanFullyBlockedRejectedBeforeClaim(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: screenRect(), Class: "androidx.recyclerview.widget.RecyclerView",
			Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Right: 1080, Top: 1000, Bottom: 1200},
			Class: "android.widget.TextView", Text: "inner"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	// The outer container has the inner scope as its only child, so select it by
	// its own rectangle rather than by role.
	container := scrollContainerMatching(t, h, screenRect())

	for _, amount := range []string{protocol.ScrollAmountPage, protocol.ScrollAmountHalf} {
		requireScrollRejected(t, h, start.Observation.ID, protocol.Operation{
			Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
			Direction: protocol.ScrollDirectionDown, Amount: amount,
		}, protocol.CodeNodeUnsupported)
	}
}

// TestScrollSafeSpanKeepsOuterAncestorAndNestedSelection proves the shared
// traversal classifies an outer wrapping scroll ancestor as allowed while an
// inner descendant blocks, and that selecting the inner container still works.
func TestScrollSafeSpanKeepsOuterAncestorAndNestedSelection(t *testing.T) {
	h := newHarness(t)
	inner := device.Rect{Left: 0, Top: 400, Right: 1080, Bottom: 2000}
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: inner, Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Right: 1080, Top: 500, Bottom: 700},
			Class: "android.widget.TextView", Text: "alpha"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	outer := scrollContainerMatching(t, h, screenRect())
	nested := scrollContainerMatching(t, h, inner)

	// The outer container's center line is blocked only inside [400,2000); the
	// longest provable span is therefore [2000,2400), which is long enough.
	h.device.scriptCaptures(scrollPageStep(specs), scrollPageStep(specs))
	outerResult := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: outer},
		Direction: protocol.ScrollDirectionDown,
	})
	if outerResult.Outcome != OutcomeExecuted {
		t.Fatalf("outer span outcome = %s error %+v", outerResult.Outcome, outerResult.Error)
	}
	// [2000,2400): extent 400, margin 40, usable 320, page 192.
	want := swipeCall{displayID: 12, x1: 540, y1: 2360, x2: 540, y2: 2168, durationMS: 400}
	if calls := swipeCalls(h); len(calls) != 1 || calls[0] != want {
		t.Fatalf("outer swipe = %+v, want %+v", calls, want)
	}

	h.device.scriptCaptures(scrollPageStep(specs), scrollPageStep(specs))
	nestedResult := h.actP(t, "Pnested", outerResult.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: nested},
		Direction: protocol.ScrollDirectionDown,
	})
	if nestedResult.Outcome != OutcomeExecuted {
		t.Fatalf("nested span outcome = %s error %+v", nestedResult.Outcome, nestedResult.Error)
	}
	// [400,2000): extent 1600, margin 160, usable 1280, page 768.
	nestedWant := swipeCall{displayID: 12, x1: 540, y1: 1840, x2: 540, y2: 1072, durationMS: 400}
	calls := swipeCalls(h)
	if len(calls) != 2 || calls[1] != nestedWant {
		t.Fatalf("nested swipe = %+v, want %+v", calls, nestedWant)
	}
}

// TestScrollSafeSpanInheritsNonCrossingParentScope is the inheritance reverse
// control: a scroll ancestor that does not cross the fixed center line still
// owns its descendants' scope. Pruning the subtree by the parent rectangle would
// let this page be planned through the inner scope.
func TestScrollSafeSpanInheritsNonCrossingParentScope(t *testing.T) {
	h := newHarness(t)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		// The inner scroll scope stays left of the center line.
		{Parent: 1, Bounds: device.Rect{Left: 0, Top: 300, Right: 200, Bottom: 800},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
		// Its child crosses the center line and must still inherit that scope.
		{Parent: 2, Bounds: device.Rect{Left: 0, Top: 300, Right: 1080, Bottom: 600},
			Class: "android.widget.TextView", Text: "spill"},
	}
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	container := scrollContainerRef(t, h)

	h.device.scriptCaptures(scrollPageStep(specs), scrollPageStep(specs))
	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	// Safe span [600,2400): extent 1800, margin 180, usable 1440, page 864.
	want := swipeCall{displayID: 12, x1: 540, y1: 2220, x2: 540, y2: 1356, durationMS: 400}
	calls := swipeCalls(h)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls, want)
	}
}

// TestScrollSafeSpanTieBreak proves the deterministic tie-break: equal-length
// safe intervals are ordered by gesture start side.
func TestScrollSafeSpanTieBreak(t *testing.T) {
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Right: 1080, Top: 1000, Bottom: 1400},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
	}
	cases := []struct {
		direction string
		want      swipeCall
	}{
		// [0,1000) and [1400,2400) are equally long: extent 1000, margin 100,
		// usable 800, page 480.
		{protocol.ScrollDirectionDown,
			swipeCall{displayID: 12, x1: 540, y1: 2300, x2: 540, y2: 1820, durationMS: 400}},
		{protocol.ScrollDirectionUp,
			swipeCall{displayID: 12, x1: 540, y1: 100, x2: 540, y2: 580, durationMS: 400}},
	}
	for _, testCase := range cases {
		t.Run(testCase.direction, func(t *testing.T) {
			h := newHarness(t)
			page := scrollPageStep(specs)
			h.device.scriptCaptures(page)
			start := h.startSession()
			container := scrollContainerRef(t, h)
			h.device.scriptCaptures(page, page)
			result := h.actP(t, "", start.Observation.ID, protocol.Operation{
				Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
				Direction: testCase.direction,
			})
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
			}
			calls := swipeCalls(h)
			if len(calls) != 1 || calls[0] != testCase.want {
				t.Fatalf("swipe = %+v, want %+v", calls, testCase.want)
			}
		})
	}
}

// TestScrollSafeSpanSubtraction covers the interval algebra directly: clipping,
// ordering, merging of intersecting and adjacent intervals, zero-length and
// tangent inputs, and the resulting gaps.
func TestScrollSafeSpanSubtraction(t *testing.T) {
	cases := []struct {
		name    string
		base    scrollSpan
		blocked []scrollSpan
		want    []scrollSpan
	}{
		{"none", scrollSpan{0, 100}, nil, []scrollSpan{{0, 100}}},
		{"middle", scrollSpan{0, 100}, []scrollSpan{{40, 60}},
			[]scrollSpan{{0, 40}, {60, 100}}},
		{"start", scrollSpan{0, 100}, []scrollSpan{{0, 40}}, []scrollSpan{{40, 100}}},
		{"end", scrollSpan{0, 100}, []scrollSpan{{60, 100}}, []scrollSpan{{0, 60}}},
		{"full", scrollSpan{0, 100}, []scrollSpan{{0, 100}}, nil},
		{"overlapping_merge", scrollSpan{0, 100},
			[]scrollSpan{{20, 50}, {40, 70}}, []scrollSpan{{0, 20}, {70, 100}}},
		{"adjacent_merge", scrollSpan{0, 100},
			[]scrollSpan{{20, 40}, {40, 60}}, []scrollSpan{{0, 20}, {60, 100}}},
		{"nested", scrollSpan{0, 100},
			[]scrollSpan{{20, 80}, {40, 60}}, []scrollSpan{{0, 20}, {80, 100}}},
		{"duplicate", scrollSpan{0, 100},
			[]scrollSpan{{40, 60}, {40, 60}}, []scrollSpan{{0, 40}, {60, 100}}},
		{"unordered", scrollSpan{0, 100},
			[]scrollSpan{{70, 90}, {10, 20}}, []scrollSpan{{0, 10}, {20, 70}, {90, 100}}},
		{"zero_length", scrollSpan{0, 100}, []scrollSpan{{50, 50}}, []scrollSpan{{0, 100}}},
		{"one_pixel", scrollSpan{0, 100}, []scrollSpan{{50, 51}},
			[]scrollSpan{{0, 50}, {51, 100}}},
		{"clipped_outside", scrollSpan{0, 100},
			[]scrollSpan{{-50, 10}, {90, 150}}, []scrollSpan{{10, 90}}},
		{"tangent", scrollSpan{0, 100},
			[]scrollSpan{{100, 150}, {-50, 0}}, []scrollSpan{{0, 100}}},
		{"empty_base", scrollSpan{10, 10}, []scrollSpan{{0, 100}}, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := subtractScrollSpans(testCase.base, testCase.blocked)
			if len(got) != len(testCase.want) {
				t.Fatalf("spans = %+v, want %+v", got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("spans = %+v, want %+v", got, testCase.want)
				}
			}
		})
	}
}

// TestScrollSafeSpanPointCrossCheck cross-checks the span reduction against an
// independent per-point predicate over every integer coordinate of small
// synthetic pages: two implementations of the same rule must agree.
func TestScrollSafeSpanPointCrossCheck(t *testing.T) {
	pages := map[string]struct {
		specs  []rawNodeSpec
		want   device.Rect
		screen device.Rect
	}{
		"tab": {
			specs: []rawNodeSpec{
				{Parent: -1, Bounds: device.Rect{Right: 40, Bottom: 40}, Class: "android.widget.FrameLayout"},
				{Parent: 0, Bounds: device.Rect{Right: 40, Bottom: 40}, Class: "android.widget.ScrollView", Scrollable: true},
				{Parent: 1, Bounds: device.Rect{Left: 5, Top: 6, Right: 35, Bottom: 10},
					Class: "androidx.recyclerviewwidget.RecyclerView", Scrollable: true},
			},
			want:   device.Rect{Right: 40, Bottom: 40},
			screen: device.Rect{Right: 40, Bottom: 40},
		},
		"nested_and_inner": {
			specs: []rawNodeSpec{
				{Parent: -1, Bounds: device.Rect{Right: 40, Bottom: 40}, Class: "android.widget.FrameLayout"},
				{Parent: 0, Bounds: device.Rect{Right: 40, Bottom: 40}, Class: "android.widget.ScrollView", Scrollable: true},
				{Parent: 1, Bounds: device.Rect{Left: 4, Top: 8, Right: 36, Bottom: 24},
					Class: "android.widget.ScrollView", Scrollable: true},
				{Parent: 1, Bounds: device.Rect{Left: 4, Top: 28, Right: 36, Bottom: 34},
					Class: "androidx.recyclerviewwidget.RecyclerView", Scrollable: true},
			},
			want:   device.Rect{Right: 40, Bottom: 40},
			screen: device.Rect{Right: 40, Bottom: 40},
		},
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			display := device.Display{ID: 12, Width: page.screen.Right, Height: page.screen.Bottom, Density: 420}
			capture := buildCapture(display, page.specs)
			projection := scrollProjection(t, capture)
			wantIndex := 0
			for ref, index := range projection.Source {
				node := findPublicNode(projection.Tree.Root, ref)
				if node != nil && node.Role == protocol.RoleScroll && node.Bounds != nil &&
					rectFromBounds(node.Bounds) == page.want {
					wantIndex = index
				}
			}
			clipped := capture.Nodes[wantIndex].Bounds.Intersect(page.screen)
			space, ok := scrollSafeSpans(projection, wantIndex, clipped, protocol.ScrollDirectionDown)
			if !ok {
				t.Fatal("span reduction failed on a valid page")
			}
			center := clipped.Left + clipped.Width()/2
			spans := 0
			for y := clipped.Top; y < clipped.Bottom; y++ {
				got := space.provesPoint(scrollPoint{X: center, Y: y}, clipped, page.screen)
				want := scrollScopeMatchesPoint(projection, center, y, wantIndex)
				if got != want {
					t.Fatalf("y=%d: span point proof = %v, per-point predicate = %v", y, got, want)
				}
				if got {
					spans++
				}
			}
			if spans == 0 {
				t.Fatal("cross-check page has no provable point")
			}
			var covered int
			for _, span := range space.spans {
				covered += span.end - span.start
			}
			if covered != spans {
				t.Fatalf("span length %d != provable points %d", covered, spans)
			}
		})
	}
}

// scrollScopeMatchesPoint is the independent per-point reference implementation
// of the scope predicate. It re-scans every ancestor chain for one point instead
// of inheriting the nearest scope, so a bug in the shared traversal shows up as
// a disagreement. The nearest scope must map to the raw index; an unmapped or
// out-of-range scope blocks, which is stricter than the pre-proposal rule.
func scrollScopeMatchesPoint(projection *Projection, x, y, want int) bool {
	if projection == nil || projection.Tree == nil || projection.Tree.Root == nil ||
		projection.Capture == nil {
		return false
	}
	ancestors := map[int]bool{}
	for parent := projection.Capture.Nodes[want].Parent; parent >= 0 &&
		parent < len(projection.Capture.Nodes); parent = projection.Capture.Nodes[parent].Parent {
		if ancestors[parent] {
			break
		}
		ancestors[parent] = true
	}
	found := false
	ok := true
	var walk func(node *protocol.Node, depth int, path []*protocol.Node)
	walk = func(node *protocol.Node, depth int, path []*protocol.Node) {
		if node == nil || !ok {
			return
		}
		current := append(path, node)
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Contains(x, y) {
			scope := -1
			for index := len(current) - 1; index >= 0; index-- {
				if !isScrollContainer(current[index]) {
					continue
				}
				mapped, has := projection.Source[current[index].Ref]
				if !has || mapped < 0 || mapped >= len(projection.Capture.Nodes) {
					ok = false
					return
				}
				scope = mapped
				break
			}
			switch {
			case scope < 0:
			case scope == want, ancestors[scope]:
				found = true
			default:
				ok = false
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1, current)
		}
	}
	walk(projection.Tree.Root, 0, nil)
	return ok && found
}

// TestScrollSafeSpanRejectsUnusableScopeSource is the conservative rule for an
// unmapped or out-of-range nearest scope: it blocks the candidate instead of
// falling back to an outer scope.
func TestScrollSafeSpanRejectsUnusableScopeSource(t *testing.T) {
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Right: 1080, Top: 1000, Bottom: 1200},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
		{Parent: 2, Bounds: device.Rect{Right: 1080, Top: 1000, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
	}
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}
	capture := buildCapture(display, specs)
	projection := scrollProjection(t, capture)
	clipped := capture.Nodes[1].Bounds.Intersect(screenRect())

	space, ok := scrollSafeSpans(projection, 1, clipped, protocol.ScrollDirectionDown)
	if !ok {
		t.Fatal("baseline span reduction failed")
	}
	if len(space.spans) != 2 || space.spans[0] != (scrollSpan{start: 0, end: 1000}) ||
		space.spans[1] != (scrollSpan{start: 1200, end: 2400}) {
		t.Fatalf("spans = %+v, want the inner scope carved out", space.spans)
	}

	// Break the inner scope's own source mapping: its descendants must not fall
	// back to the selected container, so the same interval stays blocked.
	delete(projection.Source, projection.Tree.Root.Children[0].Children[0].Ref)
	space, ok = scrollSafeSpans(projection, 1, clipped, protocol.ScrollDirectionDown)
	if !ok {
		t.Fatal("span reduction must fail closed without panicking")
	}
	if len(space.spans) != 2 || space.spans[1].end != 2400 {
		t.Fatalf("spans = %+v, want the interval to stay blocked", space.spans)
	}
}

// TestScrollRevalidateLogSeparatesSegment covers the log boundary: every safe
// span can be too short to replan while both endpoints of the original segment
// remain individually provable. The endpoint flags stay separate from the
// whole-segment proof.
func TestScrollRevalidateLogSeparatesSegment(t *testing.T) {
	var buffer strings.Builder
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	observed := scrollPageStep(scrollPageSpecs(screenRect(), device.Rect{Top: 1000}))
	h.device.scriptCaptures(observed)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	fresh := scrollPageSpecs(screenRect(), device.Rect{Top: 1000})
	for _, blocked := range []device.Rect{
		{Right: 1080, Top: 0, Bottom: 1000},
		{Right: 1080, Top: 1100, Bottom: 2100},
		{Right: 1080, Top: 2200, Bottom: 2400},
	} {
		fresh = append(fresh, rawNodeSpec{
			Parent: 1, Bounds: blocked,
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true,
		})
	}
	h.device.scriptCaptures(scrollPageStep(fresh), scrollPageStep(fresh))

	result := h.actP(t, "Psegment", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	logger.Close()
	recorded := buffer.String()
	var line string
	for _, candidate := range strings.Split(recorded, "\n") {
		if strings.Contains(candidate, "scroll.revalidate") {
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("no scroll.revalidate event recorded:\n%s", recorded)
	}
	for _, want := range []string{
		"start_scope_proven=true", "end_scope_proven=true", "proven=false",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("scroll.revalidate lacks %q: %s", want, line)
		}
	}
	for _, forbidden := range []string{" ref=", " x=", " y=", "alpha", "bravo"} {
		if strings.Contains(recorded, forbidden) {
			t.Fatalf("event log carries %q:\n%s", forbidden, recorded)
		}
	}
}

// TestScrollSafeSpanIgnoresDisabledScrollScope keeps the baseline candidate
// conditions: a disabled scroll node is not a usable scope, so it neither blocks
// the center line nor hides its descendants from the selected container.
func TestScrollSafeSpanIgnoresDisabledScrollScope(t *testing.T) {
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.ScrollView", Scrollable: true},
		{Parent: 1, Bounds: device.Rect{Right: 1080, Top: 1000, Bottom: 1400},
			Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true, Disabled: true},
		{Parent: 2, Bounds: device.Rect{Right: 1080, Top: 1000, Bottom: 1200},
			Class: "android.widget.TextView", Text: "alpha"},
	}
	h := newHarness(t)
	page := scrollPageStep(specs)
	h.device.scriptCaptures(page)
	start := h.startSession()
	container := scrollContainerRef(t, h)
	h.device.scriptCaptures(page, page)

	result := h.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: container},
		Direction: protocol.ScrollDirectionDown,
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	// An unusable scope blocks nothing: the whole container stays provable.
	want := swipeCall{displayID: 12, x1: 540, y1: 2160, x2: 540, y2: 1008, durationMS: 400}
	calls := swipeCalls(h)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("swipe = %+v, want %+v", calls, want)
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
