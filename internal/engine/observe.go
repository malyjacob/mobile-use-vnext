package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// observeTarget is the resolved original owner of an observe request.
type observeTarget struct {
	session   *sessionState
	obs       *observationState
	p         *operationState
	forceFull bool
}

// Observe performs one new read-only acquisition for an existing session. It
// never creates a session, never changes ownership and never confirms another
// caller's baseline.
func (e *Engine) Observe(ctx context.Context, agent, p string, req *protocol.ObserveRequest) (finalResult *protocol.Result, finalErr *protocol.APIError) {
	op, duplicate, apiErr := e.registerOperation(p, agent, "observe")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	defer func() { e.applyFrozenResult(op, &finalResult) }()
	e.setOperationReference(op, referenceKind(req.Reference))
	target, resolveErr := e.resolveObserveTarget(agent, req.Reference)
	if resolveErr != nil {
		return e.finishObserveError(op, "admission", resolveErr), nil
	}
	work, busy := e.acquireWork(op, WorkObserve)
	if busy != nil {
		return e.finishObserveError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)
	e.setOperationPhase(op, "admission")

	// A successful observe(O) is a reading acknowledgement of one still-usable
	// full candidate. It happens only after work admission.
	e.mu.Lock()
	if target.obs != nil && target.obs == e.current &&
		target.obs.Full && target.obs.Status == ObservationReady {
		e.confirmBaselineLocked(target.obs)
	}
	e.mu.Unlock()

	if req.WaitMS > 0 {
		e.setOperationPhase(op, "wait")
		timer := time.NewTimer(time.Duration(req.WaitMS) * time.Millisecond)
		select {
		case <-timer.C:
		case <-work.Ctx.Done():
			timer.Stop()
			return e.finishObserveError(op, "wait",
				protocol.NewError(409, protocol.CodeStopped, "观察已撤销")), nil
		}
		timer.Stop()
	}
	if e.workRevoked(work) {
		return e.finishObserveError(op, "admission",
			protocol.NewError(409, protocol.CodeStopped, "观察已撤销")), nil
	}

	e.setOperationPhase(op, "capture")
	public, _, captureErr := e.captureAndPublish(work.Ctx, work, target.session,
		target.forceFull || req.Full, req.Image, "observe", nil)
	result := newResult(op)
	if captureErr != nil {
		// The acquisition may still return read-only image material of its own
		// failed tree; never replace that with a synthetic shape.
		if public != nil {
			result.Observation = public
		} else {
			result.Observation = &protocol.Observation{
				Status: "failed", Missing: []string{"tree"}, Error: captureErr.Public(),
			}
		}
		e.mu.Lock()
		if e.session != nil && e.session.State == SessionEnded {
			result.IncludeSession = true
			result.Session = e.sessionViewLocked(e.session)
		}
		e.mu.Unlock()
		result.Recovery = e.finishOperation(op, "", "failed", captureErr)
		return result, nil
	}
	result.Observation = public
	e.finishOperation(op, "", "finished", nil)
	return result, nil
}

func (e *Engine) resolveObserveTarget(agent, reference string) (*observeTarget, *protocol.APIError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resolveObserveTargetLocked(agent, reference)
}

// resolveObserveTargetLocked is the same resolution without taking the state
// lock, so a single admission critical section can resolve a reference and take
// the work position atomically. The caller holds e.mu.
func (e *Engine) resolveObserveTargetLocked(agent, reference string) (*observeTarget, *protocol.APIError) {
	session := e.session
	kind, obs, p := e.findReferenceLocked(reference)
	target := &observeTarget{session: session, obs: obs, p: p}
	switch kind {
	case "O":
		if obs == nil {
			return nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
		}
		if obs.Agent != agent {
			return nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
		}
		if session == nil || session.State == SessionEnded ||
			obs.SessionEpoch != session.Epoch {
			return nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
		}
	case "P":
		if p == nil {
			if _, closed := e.closed[reference]; closed {
				return nil, protocol.NewError(409, protocol.CodeRequestClosed, "原请求已封闭")
			}
			return nil, protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")
		}
		if p.Agent != agent {
			return nil, protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")
		}
		if session == nil || session.State == SessionEnded ||
			p.SessionOrigin != session.Origin {
			return nil, protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的活动会话")
		}
		target.forceFull = true
	default:
		return nil, protocol.Invalid("reference 类型无效")
	}
	if session == nil || session.State == SessionEnded {
		return nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	return target, nil
}

// finishObserveError marks a read-only request failed without inventing an
// action outcome.
func (e *Engine) finishObserveError(op *operationState, phase string, apiErr *protocol.APIError) *protocol.Result {
	result := newResult(op)
	result.Error = apiErr.Public()
	result.Recovery = e.finishOperation(op, "", phase, apiErr)
	if apiErr.Details != nil {
		if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
			result.BlockingOperationID = blocking
		}
	}
	return result
}
