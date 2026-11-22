package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// This file is the read-only text recovery projection. The semantic plan is
// saved once by finishOperation; every view (first result, status(P), duplicate
// P and a frozen result) derives the same details and applies the same live
// session/durability/device/busy overrides to a result copy. No path writes a
// temporary blocker back into op.Recovery.

// applyLiveTextProjection is the result-level entry used by Act. It never
// changes the operation record.
func (e *Engine) applyLiveTextProjection(op *operationState, result **protocol.Result) {
	if op == nil || result == nil || *result == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.applyLiveTextRecoveryLocked(op, *result, time.Now())
}

// applyLiveTextRecoveryLocked applies the live-context table to one text
// result. The caller holds e.mu.
func (e *Engine) applyLiveTextRecoveryLocked(op *operationState, result *protocol.Result, now time.Time) {
	if op == nil || result == nil || op.Kind != "act" || op.ActAction != "text" ||
		op.State != OperationFinished || !op.Claimed {
		return
	}
	base := result.Recovery
	if base == nil {
		return
	}

	// A delivered partial frame is only real evidence while it is still the
	// session's current ready observation. The publish committed before this
	// result was built; STOP or a newer acquisition may have won in between. An
	// observation identity that lost that authority is removed in this same short
	// lock, so the live projection below can never omit recovery because of an O
	// the caller would not be allowed to use. A failed or read-only placeholder
	// carries no identity: it is the delivery-failure fact itself and stays on
	// the result, it simply cannot suppress recovery.
	if op.Outcome == OutcomePartial && result.Observation != nil &&
		result.Observation.ID != "" &&
		!e.partialDeliveryReadyLocked(op, result.Observation, now) {
		result.Observation = nil
	}

	origin := e.sessionByOriginLocked(op.SessionOrigin)
	valid := origin != nil && e.session == origin && origin.State == SessionActive &&
		now.Before(origin.ExpiresAt) && textBindingMatchesLocked(op, origin)
	inputState := protocol.RecoveryInputRetired
	if !valid {
		inputState = protocol.RecoveryInputRevoked
	}
	cause := base.Cause
	if result.Error != nil {
		// An error result never carries a cause; the controlled code and
		// details already explain the failure.
		cause = ""
	}

	apply := func(entry, reference, state, cause, blocking, blockingID string) {
		result.Recovery = newRecovery(entry, reference, state, cause)
		result.BlockingCode = blocking
		result.BlockingOperationID = blockingID
	}

	// Priority 1: recovery ledger and current receipt durability.
	ledgerBlock := ""
	switch {
	case e.blockedReason == "ledger_unavailable" || e.blockedReason == "ledger_corrupt":
		ledgerBlock = e.blockedReason
	case e.ledgerUnsafe:
		ledgerBlock = "ledger_unavailable"
	case e.ui != nil && e.ui.record.State != uiStateMayDispatch && !e.ui.durable:
		ledgerBlock = "ledger_unavailable"
	}
	if ledgerBlock != "" {
		apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause, ledgerBlock, "")
		return
	}

	// Priority 2: the original session is no longer a live input authority.
	if !valid {
		entry := protocol.RecoveryEntryEnd
		cleanup := e.cleanupForOriginLocked(op.SessionOrigin)
		if cleanup != nil && cleanup.State == protocol.CleanupPending {
			entry = protocol.RecoveryEntryStatus
			if cause == "" && result.Error == nil {
				cause = protocol.RecoveryCauseCleanupPending
			}
		}
		blocking := "control_revoked"
		switch {
		case origin == nil:
			blocking = "control_revoked"
		case origin != e.session:
			blocking = "control_revoked"
		case origin.State == SessionEnded && origin.Reason == "user_stop":
			blocking = "control_revoked"
		case origin.State == SessionEnded && origin.Reason == "lease_expired":
			blocking = "session_expired"
		case origin.State == SessionEnded:
			blocking = "session_ended"
		case !now.Before(origin.ExpiresAt):
			blocking = "session_expired"
		}
		apply(entry, protocol.RecoveryReferenceP, inputState, cause, blocking, "")
		return
	}

	// Priority 3: startup recovery still owns the old physical facts.
	if e.recovering || e.recovery != nil {
		blocking := e.recoveryReason
		if blocking == "" {
			blocking = "recovering"
		}
		apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause, blocking, "")
		return
	}

	// Priority 4: device preparation or control sync.
	if !e.deviceReady {
		apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause,
			protocol.CodeDeviceUnavailable, "")
		return
	}
	if !e.appFreshLocked(now) {
		apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause,
			protocol.CodeControlUnavailable, "")
		return
	}

	// Priority 5: a known control/App condition needs a person.
	if ready, why := e.appReadyLocked(now); !ready {
		switch why {
		case protocol.CodeControlUnavailable:
			apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause, why, "")
		default:
			apply(protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone, inputState, cause, why, "")
		}
		return
	}

	// Priority 6: another ordinary work or a bounded shell occupies the device.
	if e.work != nil && e.work.OperationID != op.ID && !workBeyondHandover(e.work, now) {
		blockingID := ""
		if e.work.Agent == op.Agent {
			blockingID = e.work.OperationID
		}
		apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause,
			protocol.CodeDeviceBusy, blockingID)
		return
	}
	if e.shellBarrierLocked(e.shell, now) {
		apply(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, inputState, cause,
			protocol.CodeDeviceBusy, "")
		return
	}

	// Priority 7: the saved semantic projection.
	result.Recovery = base
	result.BlockingCode = ""
	result.BlockingOperationID = ""
	if op.Outcome == OutcomePartial && deliveredReadyObservation(result.Observation) {
		// The caller already holds the fresh ready observation of a partial
		// completion: there is no next semantic step to project on this first
		// response. Replays and status(P) carry no observation and therefore
		// keep the persisted observe(P, retired) base.
		result.Recovery = nil
	}
}

// partialDeliveryReadyLocked reports whether the partial frame returned by this
// result is still exactly the current, ready, unexpired observation of the
// original session and agent. The caller holds e.mu.
func (e *Engine) partialDeliveryReadyLocked(op *operationState, observation *protocol.Observation,
	now time.Time) bool {
	if op == nil || op.TextStage == nil || observation == nil || observation.ID == "" ||
		e.current == nil {
		return false
	}
	current := e.current
	if current.ID != observation.ID || current.Status != ObservationReady ||
		current.Agent != op.Agent || current.SessionEpoch != op.TextStage.sessionEpoch ||
		current.DisplayID != op.TextStage.displayID {
		return false
	}
	return now.Before(current.ActionableUntil)
}

// deliveredReadyObservation reports the closed success predicate of one
// on-the-spot delivered observation: it must have an identity and must not be a
// failed read-only placeholder.
func deliveredReadyObservation(observation *protocol.Observation) bool {
	return observation != nil && observation.ID != "" && observation.Status != "failed"
}

// textBindingMatchesLocked reports whether the operation's immutable admission
// binding still describes this live session. The caller holds e.mu.
func textBindingMatchesLocked(op *operationState, session *sessionState) bool {
	if op == nil || op.TextStage == nil || session == nil {
		return false
	}
	stage := op.TextStage
	return stage.sessionOrigin == session.Origin &&
		stage.sessionEpoch == session.Epoch &&
		stage.appInstanceID == session.AppInstanceID &&
		stage.modeGeneration == session.ModeGeneration &&
		stage.displayID == session.Display.ID &&
		stage.displayGeneration == session.Display.Generation
}

// workBeyondHandover reports a revoked work record that is past the bounded
// logical handover and therefore no longer occupies the ordinary position.
func workBeyondHandover(work *workState, now time.Time) bool {
	return work != nil && work.Revoked && !work.RevokedAt.IsZero() &&
		!now.Before(work.RevokedAt.Add(retirementHandover))
}

// logTextFinished records the bounded text completion fact. Stage and reason
// appear only when the result carries the controlled text detail group; a
// success never invents "none"/"success". No input text, readback value or
// device output is written.
func (e *Engine) logTextFinished(op *operationState, result *protocol.Result) {
	if e == nil || e.log == nil || op == nil || result == nil ||
		op.Kind != "act" || op.ActAction != "text" {
		return
	}
	e.mu.Lock()
	focus, write, submit := textStageSnapshotLocked(op)
	e.mu.Unlock()
	fields := []eventlog.Field{
		eventlog.Str("operation_id", op.ID),
		eventlog.Str("outcome", result.Outcome),
		eventlog.Str("focus_dispatch", focus),
		eventlog.Str("write_dispatch", write),
		eventlog.Str("submit_dispatch", submit),
	}
	if result.Error != nil && result.Error.Details != nil {
		if stage, ok := result.Error.Details["stage"].(string); ok && stage != "" {
			fields = append(fields, eventlog.Str("stage", stage))
		}
		if reason, ok := result.Error.Details["reason"].(string); ok && reason != "" {
			fields = append(fields, eventlog.Str("reason", reason))
		}
		if count, ok := result.Error.Details["candidate_count"].(int); ok {
			fields = append(fields, eventlog.Int("count", count))
		}
	}
	e.log.Event(eventlog.Info, "engine.text.finished", fields...)
}
