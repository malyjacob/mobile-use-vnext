package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// unknownForegroundCaptureStep returns the ordinary page with an unreadable
// foreground. The target itself is unchanged, so only the foreground fact can
// reject the act.
func unknownForegroundCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		capture.Foreground = device.Foreground{State: device.ForegroundUnknown, Reason: "window_missing"}
		return capture, nil
	}
}

func (h *harness) tapRefRequest(t *testing.T, observationID, ref string) *protocol.ActRequest {
	t.Helper()
	return &protocol.ActRequest{
		ObservationID: observationID,
		Operation: protocol.Operation{
			Type:   "tap",
			Target: &protocol.Target{UseRef: true, Ref: ref},
		},
	}
}

func (h *harness) actWith(t *testing.T, p string, request *protocol.ActRequest) *protocol.Result {
	t.Helper()
	result, apiErr := h.engine.Act(context.Background(), "agent", p, request)
	if apiErr != nil {
		t.Fatalf("act rejected at admission: %v", apiErr)
	}
	return result
}

func (h *harness) currentObservationID() string {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.current == nil {
		return ""
	}
	return h.engine.current.ID
}

// TestActRevalidationDeliversFreshObservation is T13: a post-claim
// revalidation rejection delivers the frame it already captured as a new ready
// observation and points the caller at it with retry/O/effective. The rejected
// attempt dispatches nothing; the retry on the delivered observation dispatches
// exactly once.
func TestActRevalidationDeliversFreshObservation(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(movedButtonCaptureStep())

	result := h.actWith(t, "Pfirst", h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if len(h.device.taps) != 0 {
		t.Fatalf("rejected act dispatched input: %v", h.device.taps)
	}
	if result.Observation == nil {
		t.Fatal("rejection carried no delivered observation")
	}
	if result.Observation.Status != "complete" {
		t.Fatalf("delivered observation status = %q, want complete", result.Observation.Status)
	}
	if result.Observation.ID == "" || result.Observation.ID == start.Observation.ID {
		t.Fatalf("delivered observation id = %q, want a new id", result.Observation.ID)
	}
	if result.Observation.Tree == nil || result.Observation.Tree.Delta == nil {
		t.Fatalf("delivered tree = %+v, want a delta against the failed observation", result.Observation.Tree)
	}
	if result.Observation.Tree.Delta.Base != start.Observation.ID {
		t.Fatalf("delta base = %q, want %q", result.Observation.Tree.Delta.Base, start.Observation.ID)
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if status := observationStatus(h, start.Observation.ID); status != ObservationRetired {
		t.Fatalf("failed observation status = %q, want retired", status)
	}
	if status := observationStatus(h, result.Observation.ID); status != ObservationReady {
		t.Fatalf("delivered observation status = %q, want ready", status)
	}
	if current := h.currentObservationID(); current != result.Observation.ID {
		t.Fatalf("current observation = %q, want the delivered %q", current, result.Observation.ID)
	}

	// The delivered observation is immediately actionable: a new P retries the
	// same semantic target against the fresh tree and dispatches exactly once.
	retryButton := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if retryButton == nil {
		t.Fatal("button not found in the delivered tree")
	}
	retry := h.actWith(t, "Pretry", h.tapRefRequest(t, result.Observation.ID, retryButton.Ref))
	if retry.Outcome != OutcomeExecuted {
		t.Fatalf("retry outcome = %q error %+v, want executed", retry.Outcome, retry.Error)
	}
	if len(h.device.taps) != 1 {
		t.Fatalf("dispatched taps = %d, want exactly 1", len(h.device.taps))
	}
}

// TestActRevalidationDeliveryUsesFullForDeltaReference pins the base rule: when
// the rejected act referenced a delta observation, the claim did not confirm a
// baseline of its own, so the delivered tree must be full instead of a delta
// against an older base.
func TestActRevalidationDeliveryUsesFullForDeltaReference(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pobs",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	if observed.Observation == nil || observed.Observation.Tree == nil ||
		observed.Observation.Tree.Delta == nil {
		t.Fatalf("observe did not return a delta against the confirmed baseline: %+v", observed.Observation)
	}
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(movedButtonCaptureStep())

	result := h.actWith(t, "Pdelta", h.tapRefRequest(t, observed.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if result.Observation == nil || result.Observation.Tree == nil {
		t.Fatalf("rejection carried no delivered observation: %+v", result)
	}
	if result.Observation.Tree.Full == nil {
		t.Fatalf("delivered tree = %+v, want full for a delta reference", result.Observation.Tree)
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
}

// TestActRevalidationSkipsDeliveryForUnreadableForeground is T14: a frame whose
// foreground cannot be established is not delivered, and the projection stays
// the pre-change observe/P/retired.
func TestActRevalidationSkipsDeliveryForUnreadableForeground(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(unknownForegroundCaptureStep())

	result := h.actWith(t, "Punknown", h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeForegroundChanged)
	if result.Observation != nil {
		t.Fatalf("unreadable foreground still delivered an observation: %+v", result.Observation)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
}

// TestActRevalidationDeliveryReverseControl is T16: with the delivery seam
// disabled the same rejection returns the pre-change projection and carries no
// observation, so T13's assertions are what detect the delivery.
func TestActRevalidationDeliveryReverseControl(t *testing.T) {
	original := deliverRejection
	deliverRejection = func(*Engine, *workState, *sessionState, *observationState,
		*device.Capture, string) *protocol.Observation {
		return nil
	}
	t.Cleanup(func() { deliverRejection = original })

	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(movedButtonCaptureStep())

	result := h.actWith(t, "Pnogrant", h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if result.Observation != nil {
		t.Fatalf("disabled delivery still produced an observation: %+v", result.Observation)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
}

// otherPackageCaptureStep keeps the page but reports another foreground package,
// which is the foreground_changed rejection.
func otherPackageCaptureStep() captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		task := 42
		capture.Foreground = device.Foreground{State: device.ForegroundPresent,
			Package: "com.other", TaskID: &task}
		return capture, nil
	}
}

// TestActRevalidationDeliversOnForegroundChange is T17: a foreground change is a
// deliverable rejection, so the caller still receives fresh evidence and retry.
func TestActRevalidationDeliversOnForegroundChange(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(otherPackageCaptureStep())

	result := h.actWith(t, "Pforeground", h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeForegroundChanged)
	if len(h.device.taps) != 0 {
		t.Fatalf("rejected act dispatched input: %v", h.device.taps)
	}
	if result.Observation == nil || result.Observation.Status != "complete" {
		t.Fatalf("foreground rejection carried no delivered observation: %+v", result.Observation)
	}
	if result.Observation.Tree == nil || result.Observation.Tree.Delta == nil ||
		result.Observation.Tree.Delta.Base != start.Observation.ID {
		t.Fatalf("delivered tree = %+v, want a delta against the failed observation", result.Observation.Tree)
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
}

// TestActRevalidationDeliveryRespectsRevocation is T18: the delivery shares the
// single commit gate, so a work revoked after the frame was read publishes
// nothing and leaves the current observation untouched.
func TestActRevalidationDeliveryRespectsRevocation(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	work, session := h.settleWork(t, "Pdeliver")
	h.engine.mu.Lock()
	obs := h.engine.current
	h.engine.mu.Unlock()
	capture, err := h.device.Capture(context.Background(), session.Display)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	h.engine.mu.Lock()
	work.Revoked = true
	h.engine.mu.Unlock()

	if public := h.engine.deliverRejectedObservation(work, session, obs, &capture,
		protocol.CodeTargetChanged); public != nil {
		t.Fatalf("revoked work delivered an observation: %+v", public)
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("revocation changed the current observation: %q", status)
	}
	if current := h.currentObservationID(); current != start.Observation.ID {
		t.Fatalf("current observation = %q, want the start observation", current)
	}
}

// TestActDuplicateAfterDeliveryDoesNotPromiseTheObservation is T19: a duplicate
// P replays the finished operation view, which never carries the delivered
// observation, so the replay must not point at retry over that O.
func TestActDuplicateAfterDeliveryDoesNotPromiseTheObservation(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(movedButtonCaptureStep())
	request := h.tapRefRequest(t, start.Observation.ID, button.Ref)
	first := h.actWith(t, "Pdup", request)
	requireRecovery(t, first, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")

	replay, apiErr := h.engine.Act(context.Background(), "agent", "Pdup", request)
	if apiErr != nil {
		t.Fatalf("duplicate act rejected: %v", apiErr)
	}
	if replay.Observation != nil {
		t.Fatalf("replayed view carried an observation: %+v", replay.Observation)
	}
	requireRecovery(t, replay, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("duplicate dispatch: %v", h.device.taps)
	}
}

// TestActDuplicateKeepsStaticRetry is T20: the replay downgrade applies only to
// a delivered frame. A static rejection never claimed the O, so its retry still
// names the observation the request itself used.
func TestActDuplicateKeepsStaticRetry(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(overlayCaptureStep())
	start := h.startSession()
	request := &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation:     protocol.Operation{Type: "tap", Target: &protocol.Target{X: 500, Y: 150}},
	}
	first, apiErr := h.engine.Act(context.Background(), "agent", "Pstatic", request)
	if apiErr != nil {
		t.Fatalf("act rejected at admission: %v", apiErr)
	}
	requireNotExecuted(t, first, protocol.CodeAmbiguousPoint)
	requireRecovery(t, first, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")

	replay, apiErr := h.engine.Act(context.Background(), "agent", "Pstatic", request)
	if apiErr != nil {
		t.Fatalf("duplicate act rejected: %v", apiErr)
	}
	requireRecovery(t, replay, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("duplicate dispatch: %v", h.device.taps)
	}
}

// TestActRevalidationSkipsDeliveryWithoutFrame is T15: a revalidation failure
// that never produced a tree keeps the pre-change projection and carries no
// observation.
func TestActRevalidationSkipsDeliveryWithoutFrame(t *testing.T) {
	t.Run("hierarchy", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		if button == nil {
			t.Fatal("button not found in tree")
		}
		h.device.scriptCaptures(failingCapture())

		result := h.actWith(t, "Phierarchy", h.tapRefRequest(t, start.Observation.ID, button.Ref))
		requireNotExecuted(t, result, protocol.CodeHierarchyFailed)
		if result.Observation != nil {
			t.Fatalf("unreadable hierarchy still delivered an observation: %+v", result.Observation)
		}
		requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRetired, "")
	})

	t.Run("display", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		if button == nil {
			t.Fatal("button not found in tree")
		}
		h.device.displayExists = false

		result := h.actWith(t, "Pdisplay", h.tapRefRequest(t, start.Observation.ID, button.Ref))
		requireNotExecuted(t, result, protocol.CodeDisplayUnavailable)
		if result.Observation != nil {
			t.Fatalf("missing display still delivered an observation: %+v", result.Observation)
		}
		requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRetired, "")
	})
}
