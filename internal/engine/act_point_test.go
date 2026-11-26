package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ambiguousPointCode is the public wire code of a coordinate that resolves to
// more than one equally deep public node. It is spelled here (instead of using
// the service constant) so this file also states the contract it verifies.
const ambiguousPointCode = "ambiguous_point"

// defaultCaptureStep replays the ordinary synthetic page.
func defaultCaptureStep(f *fakeDevice, display device.Display) (device.Capture, error) {
	return f.captureLocked(display)
}

// overlayCaptureStep adds a visible sibling covering the whole display. The
// source button is unchanged, so content identity and ancestry still match;
// only point resolution sees the second same-depth candidate.
func overlayCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		bounds := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
		index := len(capture.Nodes)
		capture.Nodes = append(capture.Nodes, device.RawNode{
			Index: index, Parent: capture.Root, Depth: 1, DisplayID: display.ID,
			Visible: true, RawBounds: bounds, Bounds: bounds,
			Package: "com.example", ClassName: "android.widget.FrameLayout",
			Text: "sheet", TextSet: true,
			HasClickable: true, Clickable: true, HasEnabled: true, Enabled: true,
		})
		capture.Nodes[capture.Root].Children = append(capture.Nodes[capture.Root].Children, index)
		return capture, nil
	}
}

// nestedChildCaptureStep adds a deeper visible interactive child inside the
// observed button. The button itself keeps its identity and ancestry, but the
// point now resolves to a different (deeper) node.
//
// The child is interactive on purpose: a deeper passive decoration child (no
// readable content, no action) is tolerated by the coordinate single-owner
// conservation (§7.1.5, decoration growth under an unchanged unique owner), so
// only a deeper interactive owner exercises the rejection this test claims.
func nestedChildCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		bounds := device.Rect{Left: 400, Top: 120, Right: 600, Bottom: 180}
		index := len(capture.Nodes)
		capture.Nodes = append(capture.Nodes, device.RawNode{
			Index: index, Parent: 1, Depth: 2, DisplayID: display.ID,
			Visible: true, RawBounds: bounds, Bounds: bounds,
			Package: "com.example", ClassName: "android.widget.TextView",
			Text: "tip", TextSet: true,
			HasClickable: true, Clickable: true, HasEnabled: true, Enabled: true,
		})
		capture.Nodes[1].Children = append(capture.Nodes[1].Children, index)
		return capture, nil
	}
}

// duplicateButtonCaptureStep publishes a second node with exactly the observed
// identity and ancestry, so no unique fresh match exists.
func duplicateButtonCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		clone := capture.Nodes[1]
		clone.Index = len(capture.Nodes)
		clone.Children = nil
		capture.Nodes = append(capture.Nodes, clone)
		capture.Nodes[capture.Root].Children = append(capture.Nodes[capture.Root].Children, clone.Index)
		return capture, nil
	}
}

// previewErrorProjector keeps Build intact but fails Preview, which is the
// pure projection step of the freshness proof.
type previewErrorProjector struct{ Projector }

func (previewErrorProjector) Preview(*device.Capture) (*Projection, error) {
	return nil, errors.New("preview failed")
}

// movedButtonCaptureStep keeps the button content but changes its geometry.
func movedButtonCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		bounds := device.Rect{Left: 100, Top: 400, Right: 900, Bottom: 500}
		capture.Nodes[1].RawBounds = bounds
		capture.Nodes[1].Bounds = bounds
		return capture, nil
	}
}

// invisibleButtonCaptureStep makes the button not visible while every source
// attribute compared by identityOf stays identical. Only point resolution can
// see that the observed target is no longer published.
func invisibleButtonCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		capture.Nodes[1].Visible = false
		return capture, nil
	}
}

func (h *harness) actCoordinate(t *testing.T, observationID string, operation protocol.Operation) *protocol.Result {
	t.Helper()
	result, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
		Reference: observationID,
		Operation: operation,
	})
	if apiErr != nil {
		t.Fatalf("act rejected at admission: %v", apiErr)
	}
	return result
}

func requireNotExecuted(t *testing.T, result *protocol.Result, code string) {
	t.Helper()
	if result == nil || result.Outcome != OutcomeNotExecuted {
		t.Fatalf("outcome = %+v, want not_executed", result)
	}
	if result.Error == nil || result.Error.Code != code {
		t.Fatalf("error = %+v, want %s", result.Error, code)
	}
}

func observationStatus(h *harness, id string) string {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	obs := h.engine.observations[id]
	if obs == nil {
		return ""
	}
	return obs.Status
}

// TestActCoordinatePointRejectsOverlayOnFreshFrame is the reverse control of
// the proposal: on the baseline the unchanged source button still matches, the
// coordinate is dispatched, and the response is executed even though the point
// now resolves to an overlay.
func TestActCoordinatePointRejectsOverlayOnFreshFrame(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must carry the change, so the single queued step
	// (which repeats forever) is the whole fixture.
	h.device.scriptCaptures(overlayCaptureStep())

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("tap was dispatched: %v", h.device.taps)
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationRetired {
		t.Fatalf("observation status = %q, want retired", status)
	}
}

func TestActCoordinatePointRejectsDeeperNodeOnFreshFrame(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must carry the deeper interactive owner.
	h.device.scriptCaptures(nestedChildCaptureStep())

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "long_press", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("long press was dispatched: %v", h.device.taps)
	}
}

func TestActCoordinatePointRejectsMovedBounds(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must carry the move. The coordinate proof disables
	// the Tier 5 translation recovery, so the same-band move still rejects.
	h.device.scriptCaptures(movedButtonCaptureStep())

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("tap was dispatched: %v", h.device.taps)
	}
}

func TestActCoordinatePointRejectsUnpublishedTarget(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must keep the target unpublished.
	h.device.scriptCaptures(invisibleButtonCaptureStep())

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("tap was dispatched: %v", h.device.taps)
	}
}

// TestActCoordinatePointRequiresUniqueFreshIdentity rejects a fresh frame with
// two nodes that both match the observed identity and ancestry.
func TestActCoordinatePointRequiresUniqueFreshIdentity(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must carry the duplicate node.
	h.device.scriptCaptures(duplicateButtonCaptureStep())

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("tap was dispatched: %v", h.device.taps)
	}
}

func TestActCoordinatePointRejectsUnreadableFreshProjection(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep)
	h.engine.projector = previewErrorProjector{h.engine.projector}

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP, protocol.RecoveryInputRetired, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("tap was dispatched: %v", h.device.taps)
	}
}

// TestActCoordinatePointFallsBackWhenPointHasNoNode keeps the documented
// fallback: a point the public tree does not describe is still dispatched with
// only the foreground check, exactly as before this criterion existed.
func TestActCoordinatePointFallsBackWhenPointHasNoNode(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep)

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 10, Y: 500},
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	if len(h.device.taps) != 1 || h.device.taps[0] != [2]int{10, 500} {
		t.Fatalf("taps = %v, want one tap at the requested point", h.device.taps)
	}
}

func TestActCoordinatePointRejectsAmbiguousObservation(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(overlayCaptureStep())
	start := h.startSession()
	captures := h.device.captureCount()

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, ambiguousPointCode)
	if result.Error == nil || result.Error.Details == nil {
		t.Fatalf("ambiguous_point missing details: %+v", result.Error)
	}
	if result.Error.Details["reason"] != staticReasonCompetingTargets ||
		result.Error.Details["scope"] != tapStaticScopePoint {
		t.Fatalf("ambiguous_point details = %#v, want competing_targets/point", result.Error.Details)
	}
	candidates, ok := result.Error.Details["candidates"].([]map[string]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("ambiguous_point candidates = %#v, want 2 competing owners", result.Error.Details["candidates"])
	}
	for _, legacy := range []string{
		"nearest_actionable_ref", "safe_point", "safe_carrier_ref",
		"suggested_point", "suggested_carrier_ref", "occluding_refs",
		"ambiguous_refs", "owner_refs",
	} {
		if _, exists := result.Error.Details[legacy]; exists {
			t.Fatalf("legacy key %q present in details: %#v", legacy, result.Error.Details)
		}
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation status = %q after static rejection, want ready", status)
	}
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("static rejection performed %d new capture(s)", got-captures)
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("input was dispatched: %v", h.device.taps)
	}
}

func TestActCoordinatePointProvesTextFocusPoint(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep)

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "text", Target: &protocol.Target{X: 500, Y: 350}, Text: "hello",
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	if len(h.device.taps) != 1 || h.device.taps[0] != [2]int{500, 350} {
		t.Fatalf("taps = %v, want the focus tap at the requested point", h.device.taps)
	}
	if len(h.device.texts) != 1 || h.device.texts[0] != "hello" {
		t.Fatalf("texts = %v", h.device.texts)
	}
}

func TestActCoordinatePointProvesSwipeStartOnly(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep)

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "swipe", From: &protocol.Point{X: 500, Y: 150}, To: &protocol.Point{X: 500, Y: 1000},
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("swipe dispatched a tap: %v", h.device.taps)
	}
}

func TestActCoordinatePointRecordsDecisionFacts(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep)

	first := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	if first.Outcome != OutcomeExecuted || first.Observation == nil {
		t.Fatalf("first act = %+v", first)
	}
	second := h.actCoordinate(t, first.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 10, Y: 500},
	})
	if second.Outcome != OutcomeExecuted {
		t.Fatalf("second act = %+v", second)
	}
	logger.Close()

	recorded := buffer.String()
	for _, want := range []string{
		"act.point", `state="unique"`, "proven=true",
		`state="none"`, "proven=false",
	} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("event log lacks %q:\n%s", want, recorded)
		}
	}
	for _, forbidden := range []string{"x=", "y=", "ref=", "text="} {
		if strings.Contains(recorded, forbidden) {
			t.Fatalf("event log carries %q:\n%s", forbidden, recorded)
		}
	}
}

// TestActCoordinatePointKeepsObservationWhenProofFails verifies the claimed
// observation is never returned to ready after a failed fresh-side proof: the
// work release retires it instead.
func TestActCoordinatePointKeepsObservationWhenProofFails(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must carry the change; the proof keeps failing.
	h.device.scriptCaptures(overlayCaptureStep())

	first := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, first, protocol.CodeTargetChanged)
	requireRecovery(t, first, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")

	second, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
		Reference: start.Observation.ID,
		Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{X: 500, Y: 150}},
	})
	if apiErr != nil {
		t.Fatalf("second act rejected: %v", apiErr)
	}
	if second.Error == nil || second.Error.Code != protocol.CodeObservationRetired {
		t.Fatalf("second act error = %+v, want observation_retired", second.Error)
	}
}

// TestActRefTargetStillUsesIdentityOnly guarantees the new point criterion did
// not change the ref path: the same ambiguous fresh frame is irrelevant when
// the caller named a node.
func TestActRefTargetStillUsesIdentityOnly(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(overlayCaptureStep(), defaultCaptureStep)

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
	})
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("ref tap outcome = %s error %+v", result.Outcome, result.Error)
	}
	if len(h.device.taps) != 1 {
		t.Fatalf("taps = %v", h.device.taps)
	}
}

// TestActCoordinatePointRejectsChangedTextFocusPoint proves the text focus
// tap is not dispatched when the point proof fails.
func TestActCoordinatePointRejectsChangedTextFocusPoint(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// Both revalidation frames must carry the change, so the focus point proof
	// fails on each of them.
	h.device.scriptCaptures(overlayCaptureStep())

	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "text", Target: &protocol.Target{X: 500, Y: 350}, Text: "hello",
	})
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO, protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("focus tap was dispatched: %v", h.device.taps)
	}
	if len(h.device.texts) != 0 {
		t.Fatalf("text was written: %v", h.device.texts)
	}
}

func TestActCoordinatePointWorkTimeoutUnchanged(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep)

	// A point act still completes within the ordinary work window; this is a
	// guard that the extra projection does not change the phase accounting.
	deadline := time.Now().Add(2 * time.Second)
	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	if time.Now().After(deadline) {
		t.Fatal("coordinate act exceeded the test budget")
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %s error %+v", result.Outcome, result.Error)
	}
}
