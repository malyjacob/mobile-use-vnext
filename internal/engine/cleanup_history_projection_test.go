package engine

import (
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestEndHistoricalReferenceUsesOriginalCleanupOnly(t *testing.T) {
	h := newHarnessWithClock(t, false)
	oldSession := &sessionState{
		Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionEnded,
		StartedAt: time.Now().Add(-time.Minute), EndedAt: time.Now(), Epoch: 1,
		AppInstanceID: h.appInstance,
	}
	oldCleanup := &cleanupState{
		State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupConfirmed,
		Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupUnconfirmed,
		Display: protocol.CleanupConfirmed, Reason: "client_end",
		Error: &protocol.Error{Code: "recents_cleanup_unconfirmed", Message: "最近任务清理尚未确认"},
	}
	newSession := &sessionState{
		Origin: "Pnew", Agent: "agent", Mode: "virtual", State: SessionActive,
		StartedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Epoch: 2,
		AppInstanceID: h.appInstance,
	}
	newCleanup := &cleanupState{
		State: protocol.CleanupPending, Execution: protocol.CleanupPending,
		Tasks: protocol.CleanupPending, Recents: protocol.CleanupPending,
		Display: protocol.CleanupPending, Reason: "new_session_sentinel",
	}
	h.engine.mu.Lock()
	h.engine.session = newSession
	h.engine.cleanup = newCleanup
	h.engine.retired = []*retiredState{{session: oldSession, cleanup: oldCleanup}}
	h.engine.operations["Pold"] = &operationState{
		ID: "Pold", Agent: "agent", Kind: "start", State: OperationFinished,
		Outcome: OutcomeExecuted, SessionOrigin: oldSession.Origin,
	}
	h.engine.operations["Ptarget"] = &operationState{
		ID: "Ptarget", Agent: "agent", Kind: "observe", State: OperationFinished,
		Outcome: OutcomeExecuted, SessionOrigin: oldSession.Origin,
	}
	h.engine.mu.Unlock()

	result, apiErr := h.engine.End("agent", "Pend", "Ptarget")
	if apiErr != nil {
		t.Fatalf("end historical reference rejected: %v", apiErr)
	}
	if result.Session == nil || result.Session.Origin != oldSession.Origin ||
		result.Session.State != SessionEnded {
		t.Fatalf("historical end session = %+v", result.Session)
	}
	if result.Cleanup == nil || result.Cleanup.State != protocol.CleanupUnconfirmed ||
		result.Cleanup.Recents != protocol.CleanupUnconfirmed ||
		result.Cleanup.Error == nil ||
		result.Cleanup.Error.Code != "recents_cleanup_unconfirmed" {
		t.Fatalf("historical cleanup = %+v", result.Cleanup)
	}
	if result.Recovery != nil {
		t.Fatalf("unconfirmed historical cleanup still requested status: %+v", result.Recovery)
	}
	status, statusErr := h.engine.Status("agent", "Ptarget")
	if statusErr != nil {
		t.Fatalf("status historical reference: %v", statusErr)
	}
	if status.Cleanup == nil || status.Cleanup.State != protocol.CleanupUnconfirmed ||
		status.Cleanup.Recents != protocol.CleanupUnconfirmed ||
		status.Cleanup.Error == nil ||
		status.Cleanup.Error.Code != "recents_cleanup_unconfirmed" {
		t.Fatalf("status historical cleanup = %+v", status.Cleanup)
	}
	if status.Recovery != nil {
		t.Fatalf("unconfirmed historical status still requested recovery: %+v", status.Recovery)
	}

	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.session != newSession {
		t.Fatal("historical end replaced the current session")
	}
	if h.engine.cleanup != newCleanup {
		t.Fatal("historical end changed the current cleanup")
	}
	if len(h.engine.retired) != 1 || h.engine.retired[0].cleanup != oldCleanup {
		t.Fatal("historical end replaced the retired cleanup")
	}
	if h.engine.operations["Pend"].SessionOrigin != oldSession.Origin {
		t.Fatalf("end operation origin = %q", h.engine.operations["Pend"].SessionOrigin)
	}
	if oldCleanup.done != nil {
		t.Fatal("historical end started another cleanup worker")
	}
}

func TestUnknownEndClosesReferenceWithoutEndingCurrentSession(t *testing.T) {
	h := newHarnessWithClock(t, false)
	current := &sessionState{
		Origin: "Pcurrent", Agent: "agent", Mode: "virtual", State: SessionActive,
		StartedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Epoch: 1,
		AppInstanceID: h.appInstance,
	}
	h.engine.mu.Lock()
	h.engine.session = current
	h.engine.mu.Unlock()

	result, apiErr := h.engine.End("agent", "Preq", "Punknown")
	if apiErr != nil {
		t.Fatalf("unknown end returned an access error: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeOperationUnknown ||
		result.Outcome != OutcomeUncertain || result.Phase != "reference_unknown" ||
		result.RequestClosed == nil || !*result.RequestClosed {
		t.Fatalf("unknown end result = %+v", result)
	}
	if result.Session != nil || result.Cleanup != nil {
		t.Fatalf("unknown end fabricated session or cleanup: %+v", result)
	}

	h.engine.mu.Lock()
	if h.engine.session != current || current.State != SessionActive {
		h.engine.mu.Unlock()
		t.Fatal("unknown end changed the active session")
	}
	if _, closed := h.engine.closed["Punknown"]; !closed || h.engine.closedKnown["Punknown"] {
		h.engine.mu.Unlock()
		t.Fatal("unknown reference was not closed without positive admission evidence")
	}
	h.engine.mu.Unlock()

	if _, statusErr := h.engine.Status("agent", "Punknown"); statusErr == nil ||
		statusErr.Code != protocol.CodeOperationUnknown || statusErr.HTTPStatus != 409 {
		t.Fatalf("status unknown P = %+v", statusErr)
	}

	repeat, apiErr := h.engine.End("agent", "Preq", "Punknown")
	if apiErr != nil || repeat == nil || repeat.Error == nil ||
		repeat.Error.Code != protocol.CodeOperationUnknown ||
		repeat.Phase != "reference_unknown" ||
		repeat.RequestClosed == nil || !*repeat.RequestClosed {
		t.Fatalf("repeated unknown end = %+v, apiErr=%v", repeat, apiErr)
	}

	_, closedResult, registerErr := h.engine.registerOperation("Punknown", "agent", "observe")
	if registerErr != nil {
		t.Fatalf("closed registration rejected: %v", registerErr)
	}
	if closedResult == nil || closedResult.Outcome != OutcomeUncertain ||
		closedResult.RequestClosed == nil || !*closedResult.RequestClosed {
		t.Fatalf("closed registration = %+v", closedResult)
	}

	same, apiErr := h.engine.End("agent", "Psame", "Psame")
	if apiErr != nil || same == nil || same.Error == nil ||
		same.Error.Code != protocol.CodeOperationUnknown ||
		same.Outcome != OutcomeUncertain || same.Phase != "reference_unknown" ||
		same.RequestClosed == nil || !*same.RequestClosed {
		t.Fatalf("self-referenced unknown end = %+v, apiErr=%v", same, apiErr)
	}
	h.engine.mu.Lock()
	_, retained := h.engine.operations["Psame"]
	h.engine.mu.Unlock()
	if retained {
		t.Fatal("unknown self-referenced end remained an admitted operation")
	}
}

func TestClosedOperationUsesKnownNotAdmittedOutcome(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.engine.mu.Lock()
	h.engine.closed["Pknown"] = time.Now()
	h.engine.closedKnown["Pknown"] = true
	h.engine.mu.Unlock()

	_, closedResult, apiErr := h.engine.registerOperation("Pknown", "agent", "observe")
	if apiErr != nil {
		t.Fatalf("known closed registration: %v", apiErr)
	}
	if closedResult == nil || closedResult.Outcome != OutcomeNotExecuted ||
		closedResult.RequestClosed == nil || !*closedResult.RequestClosed {
		t.Fatalf("known closed result = %+v", closedResult)
	}
	status, statusErr := h.engine.Status("agent", "Pknown")
	if statusErr != nil {
		t.Fatalf("known closed status: %v", statusErr)
	}
	if status.Outcome != OutcomeNotExecuted || status.Phase != "closed_before_admission" ||
		status.RequestClosed == nil || !*status.RequestClosed {
		t.Fatalf("known closed status result = %+v", status)
	}
	repeated, apiErr := h.engine.End("agent", "Pknown", "Pknown")
	if apiErr != nil || repeated.Outcome != OutcomeNotExecuted ||
		repeated.Phase != "closed_before_admission" ||
		repeated.RequestClosed == nil || !*repeated.RequestClosed ||
		repeated.Session != nil || repeated.Cleanup != nil {
		t.Fatalf("repeated known closed end = %+v, apiErr=%v", repeated, apiErr)
	}

	h.engine.mu.Lock()
	h.engine.operations["Pnotadmitted"] = &operationState{
		ID: "Pnotadmitted", Agent: "agent", Kind: "start",
		State: OperationFinished, Outcome: OutcomeNotExecuted,
		Phase: "admission", CreatedAt: time.Now(), FinishedAt: time.Now(),
	}
	h.engine.mu.Unlock()
	ended, apiErr := h.engine.End("agent", "Pendknown", "Pnotadmitted")
	if apiErr != nil {
		t.Fatalf("end known not-admitted operation: %v", apiErr)
	}
	if ended.Outcome != OutcomeNotExecuted || ended.Phase != "closed_before_admission" ||
		ended.RequestClosed == nil || !*ended.RequestClosed {
		t.Fatalf("known not-admitted end = %+v", ended)
	}
	h.engine.mu.Lock()
	known := h.engine.closedKnown["Pnotadmitted"]
	h.engine.mu.Unlock()
	if !known {
		t.Fatal("positive not-admitted evidence was not retained")
	}

	h.engine.mu.Lock()
	h.engine.operations["Pexecuted"] = &operationState{
		ID: "Pexecuted", Agent: "agent", Kind: "notify",
		State: OperationFinished, Outcome: OutcomeExecuted,
		Phase: "saved", CreatedAt: time.Now(), FinishedAt: time.Now(),
	}
	h.engine.mu.Unlock()
	uncertain, apiErr := h.engine.End("agent", "Pendexecuted", "Pexecuted")
	if apiErr != nil {
		t.Fatalf("end executed detached operation: %v", apiErr)
	}
	if uncertain.Outcome != OutcomeUncertain || uncertain.Error == nil ||
		uncertain.Error.Code != protocol.CodeReferenceNoSession {
		t.Fatalf("executed operation without original session = %+v", uncertain)
	}
	h.engine.mu.Lock()
	known = h.engine.closedKnown["Pexecuted"]
	h.engine.mu.Unlock()
	if known {
		t.Fatal("executed operation was mislabeled as known not admitted")
	}
}

func TestDiagnoseHistoricalReferenceUsesItsCleanupOnly(t *testing.T) {
	h := newHarnessWithClock(t, false)
	oldSession := &sessionState{
		Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionEnded,
		StartedAt: time.Now().Add(-time.Minute), EndedAt: time.Now(), Epoch: 1,
	}
	oldCleanup := &cleanupState{
		State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupConfirmed,
		Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupUnconfirmed,
		Display: protocol.CleanupConfirmed,
		Error:   &protocol.Error{Code: "old_cleanup_error", Message: "旧收尾未确认"},
	}
	h.engine.mu.Lock()
	h.engine.session = &sessionState{
		Origin: "Pnew", Agent: "agent", Mode: "virtual", State: SessionActive,
		StartedAt: time.Now(), Epoch: 2,
	}
	h.engine.retired = []*retiredState{{session: oldSession, cleanup: oldCleanup}}
	h.engine.operations["Poldop"] = &operationState{
		ID: "Poldop", Agent: "agent", Kind: "observe", State: OperationFinished,
		Outcome: OutcomeExecuted, SessionOrigin: oldSession.Origin,
	}
	h.engine.mu.Unlock()

	result, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{
		Reference: "Poldop", HasRef: true,
	})
	if apiErr != nil {
		t.Fatalf("diagnose historical reference: %v", apiErr)
	}
	if !hasDiagnosisIssue(result.Diagnosis, "cleanup", "old_cleanup_error") {
		t.Fatalf("diagnosis did not project the original cleanup: %+v", result.Diagnosis)
	}
}

func TestStatusHistoricalObservationStopsRecoveryWhenCleanupStops(t *testing.T) {
	h := newHarnessWithClock(t, false)
	session := &sessionState{
		Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionEnded,
		StartedAt: time.Now().Add(-time.Minute), EndedAt: time.Now(), Epoch: 1,
	}
	cleanup := &cleanupState{
		State: protocol.CleanupPending, Execution: protocol.CleanupConfirmed,
		Tasks: protocol.CleanupPending, Recents: protocol.CleanupPending,
		Display: protocol.CleanupPending,
	}
	h.engine.mu.Lock()
	h.engine.retired = []*retiredState{{session: session, cleanup: cleanup}}
	h.engine.observations["Oold"] = &observationState{
		ID: "Oold", Agent: "agent", SessionEpoch: session.Epoch,
		Status: ObservationReady, ActionableUntil: time.Now().Add(time.Minute),
	}
	h.engine.mu.Unlock()

	pending, apiErr := h.engine.Status("agent", "Oold")
	if apiErr != nil {
		t.Fatalf("status pending historical observation: %v", apiErr)
	}
	if pending.Cleanup == nil || pending.Cleanup.State != protocol.CleanupPending ||
		pending.Recovery == nil || pending.Recovery.Entry != protocol.RecoveryEntryStatus ||
		pending.Recovery.Cause != protocol.RecoveryCauseCleanupPending {
		t.Fatalf("pending historical status = %+v", pending)
	}

	h.engine.mu.Lock()
	cleanup.State = protocol.CleanupUnconfirmed
	cleanup.Tasks = protocol.CleanupUnconfirmed
	cleanup.Recents = protocol.CleanupUnconfirmed
	cleanup.Display = protocol.CleanupUnconfirmed
	h.engine.mu.Unlock()
	stopped, apiErr := h.engine.Status("agent", "Oold")
	if apiErr != nil {
		t.Fatalf("status stopped historical observation: %v", apiErr)
	}
	if stopped.Cleanup == nil || stopped.Cleanup.State != protocol.CleanupUnconfirmed {
		t.Fatalf("stopped historical cleanup = %+v", stopped.Cleanup)
	}
	if stopped.Recovery != nil {
		t.Fatalf("stopped cleanup still requested status: %+v", stopped.Recovery)
	}
}
