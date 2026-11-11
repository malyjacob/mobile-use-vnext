package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// keepaliveResolution is the locked outcome of locating the original session.
type keepaliveResolution struct {
	session *sessionState
	view    *protocol.Session
	apiErr  *protocol.APIError
}

// Keepalive replaces the original active session's expiry with now plus the
// requested minutes (default two). It can shorten a long lease, takes no work
// position or device I/O, and changes neither O's input deadline nor baseline.
func (e *Engine) Keepalive(agent, p string, req *protocol.KeepaliveRequest) (*protocol.Result, *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("keepalive 请求缺失")
	}
	minutes, apiErr := req.EffectiveDurationMinutes()
	if apiErr != nil {
		return nil, apiErr
	}
	op, duplicate, apiErr := e.registerOperation(p, agent, "keepalive")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		// The original P keeps its result; a replay never extends the lease a
		// second time and never replays device work.
		return duplicate, nil
	}
	e.setOperationReference(op, referenceKind(req.Reference))
	e.mu.Lock()
	// Expiry admission and replacement share the application instant. A request
	// that arrived before expiry cannot renew after waiting past that boundary.
	now := time.Now()
	resolution := e.resolveKeepaliveLocked(agent, req.Reference, now)
	if resolution.apiErr != nil {
		e.mu.Unlock()
		return e.finishKeepaliveError(op, resolution), nil
	}
	// Explicit reset deliberately overwrites even a later expiry. Ordinary
	// activity's monotonic extension must not be used for this operation.
	resolution.session.ExpiresAt = now.Add(time.Duration(minutes) * time.Minute)
	resolution.view = e.sessionViewLocked(e.session)
	e.mu.Unlock()

	result := newResult(op)
	result.Outcome = OutcomeExecuted
	result.IncludeSession = true
	result.Session = resolution.view
	e.finishOperation(op, OutcomeExecuted, "renewed", nil)
	return result, nil
}

// resolveKeepaliveLocked locates the original session without side effects.
func (e *Engine) resolveKeepaliveLocked(agent, reference string, now time.Time) keepaliveResolution {
	kind, obs, op := e.findReferenceLocked(reference)
	var session *sessionState
	switch kind {
	case "O":
		if obs == nil || obs.Agent != agent {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")}
		}
		session = e.sessionByEpochLocked(obs.SessionEpoch)
		if session == nil {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原观察没有可用的会话")}
		}
	case "P":
		if op == nil {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")}
		}
		if op.Agent != agent {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")}
		}
		if op.SessionOrigin == "" {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话")}
		}
		session = e.sessionByOriginLocked(op.SessionOrigin)
		if session == nil {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话")}
		}
	default:
		return keepaliveResolution{apiErr: protocol.Invalid("reference 类型无效")}
	}
	view := e.sessionViewLocked(session)
	switch {
	case session.State == SessionEnded:
		return keepaliveResolution{session: session, view: view,
			apiErr: protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")}
	case session.State != SessionActive:
		// A starting session has no sliding activity lease to renew yet.
		return keepaliveResolution{session: session, view: view,
			apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原会话尚未活动")}
	case !sessionBeforeDeadline(session, now):
		return keepaliveResolution{session: session, view: view,
			apiErr: protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")}
	case session != e.session:
		return keepaliveResolution{session: session, view: view,
			apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原会话不是当前会话")}
	}
	return keepaliveResolution{session: session, view: view}
}

// finishKeepaliveError records the failed renewal and returns the original P
// with its controlled recovery entry point.
func (e *Engine) finishKeepaliveError(op *operationState, resolution keepaliveResolution) *protocol.Result {
	apiErr := resolution.apiErr
	result := newResult(op)
	result.Outcome = OutcomeNotExecuted
	result.Error = apiErr.Public()
	result.Recovery = e.finishOperation(op, OutcomeNotExecuted, "admission", apiErr)
	if resolution.view != nil {
		result.IncludeSession = true
		result.Session = resolution.view
	}
	return result
}
