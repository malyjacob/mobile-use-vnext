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
	session := e.latestSessionForAgentLocked(agent)
	var cleanup *cleanupState
	if session != nil {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(session)
		result.StartOperation = session.Origin
		cleanup = e.cleanupForSessionLocked(session)
		result.Cleanup = cleanupViewOf(cleanup)
	}
	if e.recovery != nil {
		reference := protocol.RecoveryReferenceNone
		if result.StartOperation != "" {
			reference = protocol.RecoveryReferenceP
		}
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: reference, Phase: "self",
			Code: protocol.CodeRecovering,
		})
	}
	if cleanup != nil && cleanup.State == protocol.CleanupPending {
		reference := protocol.RecoveryReferenceNone
		if result.StartOperation != "" {
			reference = protocol.RecoveryReferenceP
		}
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: reference, Phase: "self",
			InputState: protocol.RecoveryInputRevoked, Cleanup: cleanupValue(cleanup),
		})
	}
	if reason == "ui_input_reboot_required" {
		result.Recovery = &protocol.Recovery{Entry: protocol.RecoveryEntryUser, Reference: protocol.RecoveryReferenceNone}
	}
	if reason == "ledger_unavailable" {
		reference := protocol.RecoveryReferenceNone
		if result.StartOperation != "" {
			reference = protocol.RecoveryReferenceP
		}
		result.Recovery = e.durabilityRecoveryLocked(reference, "", result.Recovery)
	}
	return result
}

func (e *Engine) latestSessionForAgentLocked(agent string) *sessionState {
	if e.session != nil && e.session.Agent == agent {
		return e.session
	}
	var latest *sessionState
	for _, retired := range e.retired {
		if retired == nil || retired.session == nil || retired.session.Agent != agent {
			continue
		}
		if latest == nil || retired.session.EndedAt.After(latest.EndedAt) {
			latest = retired.session
		}
	}
	if e.lastSession != nil && e.lastSession.Agent == agent &&
		(latest == nil || e.lastSession.EndedAt.After(latest.EndedAt)) {
		latest = e.lastSession
	}
	return latest
}

func (e *Engine) statusOperationLocked(agent, reference string,
	op *operationState, now time.Time) (*protocol.Result, *protocol.APIError) {
	if op == nil {
		if _, closed := e.closed[reference]; closed && e.closedKnown[reference] {
			closedFlag := true
			return &protocol.Result{
				OperationID: reference, OperationState: OperationFinished,
				Outcome: OutcomeNotExecuted, Phase: "closed_before_admission",
				RequestClosed:  &closedFlag,
				IncludeSession: true, Session: nil,
			}, nil
		}
		return nil, protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")
	}
	if op.Agent != agent {
		return nil, protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")
	}
	result := e.operationViewLocked(op)
	result.OperationID = reference
	if op.Error != nil && op.Error.Code == protocol.CodeOperationUnknown &&
		op.Phase == "reference_unknown" {
		closedFlag := true
		result.RequestClosed = &closedFlag
	}
	if op.State == OperationRunning {
		result.Phase = op.Phase
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: protocol.RecoveryReferenceP, Phase: "running",
		})
		return result, nil
	}
	sessionEnded := result.Session != nil && result.Session.State == SessionEnded
	cleanup := e.cleanupForOriginLocked(op.SessionOrigin)
	if op.Kind == "end" || sessionEnded {
		result.Cleanup = cleanupViewOf(cleanup)
	}
	if result.Recovery != nil &&
		result.Recovery.Entry == protocol.RecoveryEntryStatus &&
		result.Recovery.Cause == protocol.RecoveryCauseCleanupPending &&
		(cleanup == nil || cleanup.State != protocol.CleanupPending) {
		result.Recovery = nil
	}
	// Only pending cleanup has an advancing next step. Unconfirmed means the
	// bounded automatic closure has stopped and remains as a reported fact.
	if op.Error == nil && op.Outcome != OutcomeUncertain && result.Session != nil &&
		result.Session.State == SessionEnded && cleanup != nil &&
		cleanup.State == protocol.CleanupPending {
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: protocol.RecoveryReferenceP, Phase: "operation",
			Cleanup: cleanupValue(cleanup),
		})
	}
	result.Recovery = e.durabilityRecoveryLocked(protocol.RecoveryReferenceP, inputStateFor(op.Kind, op.Reference, "", op.Claimed), result.Recovery)
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
	if session == nil && obs.SessionSnapshot != nil {
		copy := *obs.SessionSnapshot
		result.IncludeSession = true
		result.Session = &copy
		result.Cleanup = cloneCleanupSnapshot(obs.CleanupSnapshot)
	}
	cleanup := e.cleanupForSessionLocked(session)
	if session != nil && session.State == SessionEnded {
		result.Cleanup = cleanupViewOf(cleanup)
	}
	switch {
	case session == nil || session.State == SessionEnded:
		result.InputState = protocol.RecoveryInputRevoked
		if cleanup != nil && cleanup.State == protocol.CleanupPending {
			result.Recovery = planRecovery(recoveryInput{
				Tool: "status", Reference: protocol.RecoveryReferenceO, Phase: "operation",
				InputState: protocol.RecoveryInputRevoked, Cleanup: cleanupValue(cleanup),
			})
		}
	case state == ObservationReady && now.Before(obs.ActionableUntil):
		result.InputState = protocol.RecoveryInputEffective
	case state == ObservationReady:
		result.InputState = protocol.RecoveryInputExpired
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: protocol.RecoveryReferenceO, Phase: "observation",
			Code: protocol.CodeObservationExpired, InputState: protocol.RecoveryInputExpired,
		})
	case state == ObservationExpired:
		result.InputState = protocol.RecoveryInputExpired
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: protocol.RecoveryReferenceO, Phase: "observation",
			Code: protocol.CodeObservationExpired, InputState: protocol.RecoveryInputExpired,
		})
	case state == ObservationClaimed:
		result.InputState = protocol.RecoveryInputClaimed
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: protocol.RecoveryReferenceO, Phase: "observation",
			Code: protocol.CodeObservationClaimed, InputState: protocol.RecoveryInputClaimed,
		})
	default:
		result.InputState = protocol.RecoveryInputRetired
		result.Recovery = planRecovery(recoveryInput{
			Tool: "status", Reference: protocol.RecoveryReferenceO, Phase: "observation",
			Code: protocol.CodeObservationRetired, InputState: protocol.RecoveryInputRetired,
		})
	}
	return result, nil
}
