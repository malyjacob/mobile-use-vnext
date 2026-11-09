package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type endResolution struct {
	session         *sessionState
	closeUnadmitted bool
	alreadyClosed   bool
	apiErr          *protocol.APIError
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
		return duplicate, nil
	}
	e.setOperationReference(op, referenceKind(reference))
	now := time.Now()
	e.mu.Lock()
	resolution := e.resolveEndLocked(agent, reference, now)
	if resolution.apiErr != nil {
		e.mu.Unlock()
		return e.finishWithError(op, "admission", resolution.apiErr), nil
	}
	result := newResult(op)
	result.Outcome = OutcomeExecuted
	result.TargetReference = reference

	if resolution.closeUnadmitted || resolution.alreadyClosed {
		e.addClosedLocked(reference, now)
		closedFlag := true
		result.RequestClosed = &closedFlag
		result.IncludeSession = true
		result.Session = nil
		result.Cleanup = notApplicableCleanup()
		e.mu.Unlock()
		e.finishOperation(op, OutcomeExecuted, "closed_before_admission", nil)
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
		if e.cleanup == nil {
			if session.Mode == "primary" || (session.Display.ID == 0 && session.CreateResult == nil) {
				e.cleanup = notApplicableCleanupState()
			} else {
				cleanup = e.beginCleanupLocked(session, "client_end")
				if cleanup != nil {
					sessionToClean = session
				}
			}
		}
	}
	result.Cleanup = e.cleanupViewLocked()
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
				return endResolution{alreadyClosed: true}
			}
			return endResolution{closeUnadmitted: true}
		}
		if op.Agent != agent {
			return endResolution{apiErr: protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")}
		}
		if op.Kind == "end" {
			return endResolution{session: e.sessionByOriginLocked(op.SessionOrigin)}
		}
		if op.SessionOrigin == "" {
			return endResolution{apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话")}
		}
		session := e.sessionByOriginLocked(op.SessionOrigin)
		if session == nil {
			if _, closed := e.closed[reference]; closed {
				return endResolution{alreadyClosed: true}
			}
			return endResolution{apiErr: protocol.NewError(409, protocol.CodeSessionNotFound,
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
