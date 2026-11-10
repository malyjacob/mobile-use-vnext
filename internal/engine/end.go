package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type endResolution struct {
	session          *sessionState
	closeUnadmitted  bool
	alreadyClosed    bool
	referenceUnknown bool
	operation        *operationState
	apiErr           *protocol.APIError
}

// End revokes the original control and starts the independent physical closure.
// It never waits for ordinary device work; a second call reports the same
// closure progress and never creates a second destructive chain.
func (e *Engine) End(agent, p, reference string) (*protocol.Result, *protocol.APIError) {
	op, duplicate, apiErr := e.registerOperation(p, agent, "end")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		e.mu.Lock()
		e.normalizeEndDuplicateLocked(reference, duplicate)
		e.mu.Unlock()
		return duplicate, nil
	}
	e.setOperationReference(op, referenceKind(reference))
	now := time.Now()
	e.mu.Lock()
	var resolution endResolution
	// registerOperation has just created op when duplicate was nil. If the
	// target reference is that same P, the target did not exist before this
	// request and must not inherit the current session merely because the new
	// end operation was admitted first.
	if reference == op.ID && referenceKind(reference) == protocol.RecoveryReferenceP {
		resolution = endResolution{referenceUnknown: true}
	} else {
		resolution = e.resolveEndLocked(agent, reference, now)
	}
	e.bindEndOperationLocked(op, resolution)
	if resolution.operation != nil && resolution.session == nil {
		e.closeKnownEndReferenceLocked(reference, resolution.operation, now)
		if resolution.operation.State == OperationFinished &&
			resolution.operation.Outcome == OutcomeNotExecuted {
			resolution.closeUnadmitted = true
			resolution.alreadyClosed = true
			resolution.apiErr = nil
		} else if resolution.apiErr != nil && resolution.apiErr.Outcome == "" {
			resolution.apiErr.Outcome = OutcomeUncertain
		}
	}
	if resolution.apiErr != nil {
		e.mu.Unlock()
		return e.finishWithError(op, "admission", resolution.apiErr), nil
	}
	result := newResult(op)
	result.Outcome = OutcomeExecuted
	result.TargetReference = reference

	if resolution.referenceUnknown {
		e.closeUnknownEndReferenceLocked(reference, now)
		e.mu.Unlock()
		result = e.finishWithError(op, "reference_unknown",
			protocol.NewErrorOutcome(409, protocol.CodeOperationUnknown,
				"原操作不可用", OutcomeUncertain))
		result.Phase = "reference_unknown"
		result.TargetReference = reference
		closedFlag := true
		result.RequestClosed = &closedFlag
		result.IncludeSession = true
		result.Session = nil
		result.Cleanup = nil
		// If the unknown reference and the request operation are the same P,
		// the closed marker is the durable fact. Keeping a second operation
		// record would let registerOperation accept it again as a duplicate.
		if op.ID == reference {
			e.mu.Lock()
			if e.operations[op.ID] == op {
				delete(e.operations, op.ID)
			}
			e.mu.Unlock()
		}
		e.persistAsync()
		return result, nil
	}

	if resolution.closeUnadmitted || resolution.alreadyClosed {
		e.addClosedLocked(reference, now)
		closedFlag := true
		result.RequestClosed = &closedFlag
		result.Outcome = OutcomeNotExecuted
		result.Phase = "closed_before_admission"
		result.IncludeSession = true
		result.Session = nil
		result.Cleanup = notApplicableCleanup()
		e.mu.Unlock()
		e.finishOperation(op, OutcomeNotExecuted, "closed_before_admission", nil)
		e.persistAsync()
		return result, nil
	}

	session := resolution.session
	if session == nil {
		e.mu.Unlock()
		return e.finishWithError(op, "admission",
			protocol.NewError(409, protocol.CodeSessionNotFound, "原会话不可用")), nil
	}
	var cleanup *cleanupState
	var sessionToClean *sessionState
	if session.State != SessionEnded {
		if e.session == nil || e.session.Epoch != session.Epoch {
			e.mu.Unlock()
			return e.finishWithError(op, "admission",
				protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")), nil
		}
		e.revokeSessionLocked("client_end", now)
		cleanup = e.beginCleanupLocked(session, "client_end")
		if cleanup != nil {
			sessionToClean = session
		}
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(session)
	} else {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(session)
		cleanup = e.cleanupForSessionLocked(session)
		if cleanup == nil && session == e.session &&
			(session.Mode == "primary" || (session.Display.ID == 0 && session.CreateResult == nil)) {
			cleanup = notApplicableCleanupState()
			e.cleanup = cleanup
		}
	}
	result.Cleanup = cleanupViewOf(cleanup)
	e.mu.Unlock()

	if cleanup != nil {
		e.launchPreparedCleanup(sessionToClean, "client_end", cleanup)
	}
	e.persistAsync()
	if e.log != nil {
		e.log.Event(eventlog.Info, "session.end", eventlog.Str("origin", session.Origin),
			eventlog.Str("reason", "client_end"))
	}
	// The projection is planned from the cleanup state read under the same
	// lock; a complete/not-applicable closure is deliberately omitted.
	result.Recovery = e.finishOperation(op, OutcomeExecuted, "ended", nil)
	return result, nil
}

func (e *Engine) bindEndOperationLocked(op *operationState, resolution endResolution) {
	if op == nil {
		return
	}
	op.SessionOrigin = ""
	if resolution.session != nil {
		op.SessionOrigin = resolution.session.Origin
	}
}

func (e *Engine) closeUnknownEndReferenceLocked(reference string, now time.Time) {
	if e.closedKnown == nil {
		e.closedKnown = map[string]bool{}
	}
	e.addClosedLocked(reference, now)
	e.closedKnown[reference] = false
}

func (e *Engine) closeKnownEndReferenceLocked(reference string, op *operationState, now time.Time) {
	if e.closedKnown == nil {
		e.closedKnown = map[string]bool{}
	}
	e.addClosedLocked(reference, now)
	e.closedKnown[reference] = op != nil && op.State == OperationFinished &&
		op.Outcome == OutcomeNotExecuted
}

func (e *Engine) normalizeEndDuplicateLocked(reference string, result *protocol.Result) {
	if result == nil || result.OperationID == "" {
		return
	}
	if op := e.operations[result.OperationID]; op != nil {
		session := e.sessionByOriginLocked(op.SessionOrigin)
		cleanup := e.cleanupForOriginLocked(op.SessionOrigin)
		if op.Kind == "end" || (session != nil && session.State == SessionEnded) {
			result.Cleanup = cleanupViewOf(cleanup)
		}
		if result.Recovery != nil &&
			result.Recovery.Entry == protocol.RecoveryEntryStatus &&
			result.Recovery.Cause == protocol.RecoveryCauseCleanupPending &&
			(cleanup == nil || cleanup.State != protocol.CleanupPending) {
			result.Recovery = nil
		}
		if op.Error != nil && op.Error.Code == protocol.CodeOperationUnknown &&
			op.Phase == "reference_unknown" {
			closedFlag := true
			result.TargetReference = reference
			result.RequestClosed = &closedFlag
		}
		return
	}
	if _, closed := e.closed[result.OperationID]; !closed {
		return
	}
	if e.closedKnown[result.OperationID] {
		result.Outcome = OutcomeNotExecuted
		result.Phase = "closed_before_admission"
		result.TargetReference = reference
		result.IncludeSession = true
		result.Session = nil
		result.Cleanup = nil
		return
	}
	closedFlag := true
	result.Outcome = OutcomeUncertain
	result.Phase = "reference_unknown"
	result.TargetReference = result.OperationID
	result.Error = &protocol.Error{Code: protocol.CodeOperationUnknown, Message: "原操作不可用"}
	result.Recovery = nil
	result.RequestClosed = &closedFlag
	result.IncludeSession = true
	result.Session = nil
	result.Cleanup = nil
}

func (e *Engine) resolveEndLocked(agent, reference string, now time.Time) endResolution {
	kind, obs, op := e.findReferenceLocked(reference)
	switch kind {
	case "O":
		if obs == nil || obs.Agent != agent {
			return endResolution{apiErr: protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")}
		}
		session := e.sessionByEpochLocked(obs.SessionEpoch)
		if session == nil {
			return endResolution{apiErr: protocol.NewError(409, protocol.CodeSessionNotFound, "原会话不可用")}
		}
		return endResolution{session: session}
	case "P":
		if op == nil {
			if _, closed := e.closed[reference]; closed {
				if e.closedKnown[reference] {
					return endResolution{closeUnadmitted: true, alreadyClosed: true}
				}
				return endResolution{referenceUnknown: true}
			}
			return endResolution{referenceUnknown: true}
		}
		if op.Agent != agent {
			return endResolution{apiErr: protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")}
		}
		if op.Kind == "end" {
			session := e.sessionByOriginLocked(op.SessionOrigin)
			if session == nil {
				return endResolution{operation: op}
			}
			return endResolution{session: session}
		}
		if op.SessionOrigin == "" {
			return endResolution{operation: op, apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话")}
		}
		session := e.sessionByOriginLocked(op.SessionOrigin)
		if session == nil {
			return endResolution{operation: op, apiErr: protocol.NewError(409, protocol.CodeSessionNotFound,
				"原会话不可用")}
		}
		return endResolution{session: session}
	default:
		return endResolution{apiErr: protocol.Invalid("reference 类型无效")}
	}
}

func notApplicableCleanup() *protocol.Cleanup {
	return &protocol.Cleanup{
		State: protocol.CleanupComplete, Execution: protocol.CleanupNotApplicable,
		Tasks: protocol.CleanupNotApplicable, Recents: protocol.CleanupNotApplicable,
		Display: protocol.CleanupNotApplicable,
	}
}

func notApplicableCleanupState() *cleanupState {
	return &cleanupState{
		State: protocol.CleanupComplete, Execution: protocol.CleanupNotApplicable,
		Tasks: protocol.CleanupNotApplicable, Recents: protocol.CleanupNotApplicable,
		Display: protocol.CleanupNotApplicable, Reason: "ended", StartedAt: time.Now(),
	}
}
