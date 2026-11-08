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

// Keepalive explicitly renews the original session activity deadline located by
// an owned O/P. It takes no work position, performs no device I/O, never
// extends an observation's input deadline and never acknowledges a baseline.
func (e *Engine) Keepalive(agent, p string, req *protocol.KeepaliveRequest) (*protocol.Result, *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("keepalive 请求缺失")
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
	now := time.Now()
	e.mu.Lock()
	resolution := e.resolveKeepaliveLocked(agent, req.Reference, now)
	if resolution.apiErr != nil {
		e.mu.Unlock()
		return e.finishKeepaliveError(op, resolution), nil
	}
	// The explicit keepalive is the only lease extension here. Observation
	// actionability and O state stay frozen.
	e.touchSessionLocked(now)
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
				"原观察没有可用的会话").WithNext("status")}
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
				"原操作没有可用的会话").WithNext("status")}
		}
		session = e.sessionByOriginLocked(op.SessionOrigin)
		if session == nil {
			return keepaliveResolution{apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话").WithNext("status")}
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
				"原会话尚未活动").WithNext("status")}
	case !sessionBeforeDeadline(session, now):
		return keepaliveResolution{session: session, view: view,
			apiErr: protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")}
	case session != e.session:
		return keepaliveResolution{session: session, view: view,
			apiErr: protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原会话不是当前会话").WithNext("status")}
	}
	return keepaliveResolution{session: session, view: view}
}

// finishKeepaliveError records the failed renewal and returns the original P
// with its controlled recovery entry point.
func (e *Engine) finishKeepaliveError(op *operationState, resolution keepaliveResolution) *protocol.Result {
	apiErr := resolution.apiErr
	e.finishOperation(op, OutcomeNotExecuted, "admission", apiErr)
	result := newResult(op)
	result.Outcome = OutcomeNotExecuted
	result.Error = apiErr.Public()
	result.Next = apiErr.Next
	if resolution.view != nil {
		result.IncludeSession = true
		result.Session = resolution.view
	}
	return result
}
