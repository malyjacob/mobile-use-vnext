package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Status is a read-only report. It never captures a tree, renews a lease,
// acknowledges a baseline or changes input authority.
func (e *Engine) Status(agent, reference string) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if reference == "" {
		return e.statusSelfLocked(agent, now), nil
	}
	kind, obs, op := e.findReferenceLocked(reference)
	switch kind {
	case "O":
		return e.statusObservationLocked(agent, reference, obs, now)
	case "P":
		return e.statusOperationLocked(agent, reference, op, now)
	default:
		return nil, protocol.Invalid("reference 类型无效")
	}
}

func (e *Engine) statusSelfLocked(agent string, now time.Time) *protocol.Result {
	canStart, reason := e.canStartLocked(now)
	result := &protocol.Result{CanStart: &canStart}
	if !canStart {
		result.BlockingCode = reason
	}
	if e.session != nil && e.session.Agent == agent {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(e.session)
		result.StartOperation = e.session.Origin
	} else if e.lastSession != nil && e.lastSession.Agent == agent &&
		e.cleanup != nil && e.cleanup.State != protocol.CleanupComplete {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(e.lastSession)
		result.StartOperation = e.lastSession.Origin
	}
	if e.cleanup != nil {
		result.Cleanup = e.cleanupViewLocked()
	}
	if e.recovery != nil {
		result.Next = "status"
	}
	return result
}

func (e *Engine) statusOperationLocked(agent, reference string,
	op *operationState, now time.Time) (*protocol.Result, *protocol.APIError) {
	if op == nil {
		if _, closed := e.closed[reference]; closed {
			closedFlag := true
			return &protocol.Result{
				OperationID: reference, OperationState: OperationFinished,
				Outcome: OutcomeNotExecuted, RequestClosed: &closedFlag,
				IncludeSession: true, Session: nil,
			}, nil
		}
		return &protocol.Result{OperationID: reference, OperationState: "not_seen"}, nil
	}
	if op.Agent != agent {
		return nil, protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")
	}
	result := e.operationViewLocked(op)
	result.OperationID = reference
	if op.State == OperationRunning {
		result.Phase = op.Phase
		result.Next = "status"
		return result, nil
	}
	if op.Outcome == OutcomeUncertain {
		result.Next = "status"
	}
	if result.Next == "" && op.Error == nil && result.Session != nil &&
		result.Session.State == SessionEnded && e.cleanup != nil &&
		e.cleanup.State != protocol.CleanupComplete {
		result.Next = "status"
	}
	return result, nil
}

func (e *Engine) statusObservationLocked(agent, reference string,
	obs *observationState, now time.Time) (*protocol.Result, *protocol.APIError) {
	if obs == nil || obs.Agent != agent {
		return nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	state := obs.Status
	if state == ObservationReady && now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		state = ObservationExpired
	}
	result := &protocol.Result{ObservationState: state, TargetReference: reference}
	session := e.sessionByEpochLocked(obs.SessionEpoch)
	if session != nil {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(session)
	}
	switch {
	case session == nil || session.State == SessionEnded:
		result.InputState = "revoked"
		result.Next = "status"
	case state == ObservationReady && now.Before(obs.ActionableUntil):
		result.InputState = "effective"
	case state == ObservationReady:
		result.InputState = "expired"
		result.Next = "observe"
	default:
		result.InputState = state
		result.Next = "observe"
	}
	return result, nil
}
