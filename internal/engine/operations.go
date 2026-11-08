package engine

import (
	"context"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// validOperationID accepts the SDK-supplied P identity without granting any
// authority from its shape: ownership still comes from authentication and the
// service's own records.
func validOperationID(id string) bool {
	if len(id) < 2 || len(id) > protocol.MaxReferenceLen {
		return false
	}
	if id[0] != 'P' {
		return false
	}
	for i := 1; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// registerOperation atomically admits one P before any device side effect. A
// duplicate P returns the original operation view and never dispatches again.
func (e *Engine) registerOperation(p, agent, kind string) (*operationState, *protocol.Result, *protocol.APIError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shuttingDown {
		return nil, nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "服务正在退出")
	}
	if p == "" {
		for {
			p = e.nextOperationIDLocked()
			if _, exists := e.operations[p]; exists {
				continue
			}
			if _, closed := e.closed[p]; !closed {
				break
			}
		}
	}
	if !validOperationID(p) {
		return nil, nil, protocol.Invalid("operation_id 格式无效")
	}
	if existing, ok := e.operations[p]; ok {
		if existing.Agent != agent {
			return nil, nil, protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")
		}
		return nil, e.operationViewLocked(existing), nil
	}
	if closedAt, ok := e.closed[p]; ok {
		_ = closedAt
		closed := true
		return nil, &protocol.Result{
			OperationID: p, OperationState: OperationFinished, Outcome: OutcomeNotExecuted,
			RequestClosed: &closed, Next: "status",
		}, nil
	}
	if len(e.operations) >= e.cfg.MaxOperations {
		e.evictOperationsLocked()
		if len(e.operations) >= e.cfg.MaxOperations {
			return nil, nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "操作记录容量不足")
		}
	}
	op := &operationState{
		ID: p, Agent: agent, Kind: kind, State: OperationRunning,
		Phase: "admitted", CreatedAt: time.Now(),
	}
	if e.session != nil {
		op.SessionOrigin = e.session.Origin
	}
	e.operations[p] = op
	return op, nil, nil
}

func (e *Engine) evictOperationsLocked() {
	protected := map[string]bool{}
	if e.session != nil && e.session.State != SessionEnded {
		protected[e.session.Origin] = true
	}
	if e.work != nil {
		protected[e.work.OperationID] = true
	}
	if e.current != nil {
		protected[e.current.Origin] = true
	}
	var oldest *operationState
	for _, op := range e.operations {
		if protected[op.ID] || op.State != OperationFinished {
			continue
		}
		if oldest == nil || op.CreatedAt.Before(oldest.CreatedAt) {
			oldest = op
		}
	}
	if oldest != nil {
		delete(e.operations, oldest.ID)
	}
}

func (e *Engine) setOperationPhase(op *operationState, phase string) {
	if op == nil {
		return
	}
	e.mu.Lock()
	if op.State == OperationRunning {
		op.Phase = phase
	}
	e.mu.Unlock()
}

func (e *Engine) finishOperation(op *operationState, outcome, phase string, apiErr *protocol.APIError) {
	if op == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op.State = OperationFinished
	op.Outcome = outcome
	op.Phase = phase
	op.FinishedAt = time.Now()
	if apiErr != nil {
		op.Error = apiErr.Public()
	}
}

// acquireWork takes the single ordinary device work position. Device I/O and
// tree work happen outside the state lock.
func (e *Engine) acquireWork(op *operationState, kind string) (*workState, *protocol.APIError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shuttingDown {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "服务正在退出")
	}
	if e.work != nil {
		var blocking string
		if e.work.Agent == op.Agent {
			blocking = e.work.OperationID
		}
		return nil, &protocol.APIError{
			Code: protocol.CodeDeviceBusy, Message: "设备工作尚未完成",
			HTTPStatus: 409, Outcome: OutcomeNotExecuted, Next: "status",
			Details: map[string]any{"blocking_operation_id": blocking},
		}
	}
	timeout := e.cfg.WorkTimeout
	switch kind {
	case WorkStart:
		timeout = e.cfg.StartTimeout + e.cfg.WorkTimeout
	case WorkObserve:
		timeout = e.cfg.WorkTimeout + 15*time.Second
	case WorkAct:
		timeout = 2*e.cfg.WorkTimeout + 10*time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	work := &workState{
		OperationID: op.ID, Agent: op.Agent, Kind: kind, Phase: "admitted",
		Start: time.Now(), Ctx: ctx, Cancel: cancel,
	}
	if e.session != nil {
		work.SessionEpoch = e.session.Epoch
	}
	e.work = work
	e.broadcastAppLocked()
	return work, nil
}

func (e *Engine) setWorkPhase(work *workState, phase string) {
	e.mu.Lock()
	if e.work == work {
		work.Phase = phase
		e.broadcastAppLocked()
	}
	e.mu.Unlock()
}

func (e *Engine) releaseWork(work *workState) {
	e.mu.Lock()
	if e.work == work {
		if obs := e.observations[work.ClaimedO]; obs != nil && obs.Status == ObservationClaimed && obs.ClaimedBy == work.OperationID {
			obs.Status = ObservationRetired
			obs.RetiredAt = time.Now()
		}
		e.work = nil
		if work.Cancel != nil {
			work.Cancel()
		}
		e.broadcastAppLocked()
	}
	e.mu.Unlock()
}

// workRevoked reports whether STOP/end cancelled this work or its session.
func (e *Engine) workRevoked(work *workState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.workAuthorizedLocked(work, time.Now())
}

// sessionForWork returns the session fixed by this work, if still the same.
func (e *Engine) sessionForWork(work *workState) *sessionState {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.workAuthorizedLocked(work, time.Now()) {
		return nil
	}
	// Only immutable snapshots cross the state lock. In particular, display
	// reports and STOP may mutate the original session while an adapter waits.
	snapshot := *e.session
	return &snapshot
}

func sessionBeforeDeadline(session *sessionState, now time.Time) bool {
	return now.Before(session.ExpiresAt) && now.Before(session.HardExpiresAt)
}

func (e *Engine) workAuthorizedLocked(work *workState, now time.Time) bool {
	return work != nil && e.work == work && !work.Revoked && !e.shuttingDown &&
		work.Ctx.Err() == nil && e.session != nil && e.session.State != SessionEnded &&
		e.session.Epoch == work.SessionEpoch && sessionBeforeDeadline(e.session, now) &&
		e.session.AppInstanceID == e.app.InstanceID && e.session.ModeGeneration == e.app.ModeGeneration &&
		e.session.Mode == e.app.Mode
}

// beginDispatch is the linearization point shared with STOP. Once it wins,
// Android may receive the input even if STOP immediately cancels its context.
func (e *Engine) beginDispatch(work *workState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.workAuthorizedLocked(work, time.Now()) {
		return false
	}
	work.DispatchUnknown = true
	return true
}

// confirmDispatch accepts timely completion evidence only. A late adapter
// return cannot rewrite the unknown execution already observed by STOP.
func (e *Engine) confirmDispatch(work *workState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.workAuthorizedLocked(work, time.Now()) {
		return false
	}
	work.DispatchUnknown = false
	return true
}

func (e *Engine) markDispatchUnknown(work *workState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if work == nil {
		return
	}
	work.DispatchUnknown = true
	// The uncertainty must outlive the ordinary work slot so a later STOP/end
	// still records cleanup.execution=unconfirmed.
	if e.session != nil && e.session.Epoch == work.SessionEpoch {
		e.session.ExecutionUnknown = true
	}
}

func (e *Engine) clearDispatchUnknown(work *workState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	work.DispatchUnknown = false
}

// busyError builds the controlled busy result for an operation that lost the
// work position.
func (e *Engine) busyError(op *operationState) *protocol.APIError {
	e.mu.Lock()
	defer e.mu.Unlock()
	apiErr := &protocol.APIError{
		Code: protocol.CodeDeviceBusy, Message: "设备工作尚未完成",
		HTTPStatus: 409, Outcome: OutcomeNotExecuted, Next: "status",
	}
	if e.work != nil && op != nil && e.work.Agent == op.Agent {
		apiErr.Details = map[string]any{"blocking_operation_id": e.work.OperationID}
	}
	return apiErr
}

// newResult builds a result owned by one operation.
func newResult(op *operationState) *protocol.Result {
	if op == nil {
		return &protocol.Result{}
	}
	return &protocol.Result{OperationID: op.ID}
}

// finishWithError marks the operation and returns the public error result.
func (e *Engine) finishWithError(op *operationState, phase string, apiErr *protocol.APIError) *protocol.Result {
	if apiErr.Outcome == "" {
		apiErr.Outcome = OutcomeNotExecuted
	}
	e.finishOperation(op, apiErr.Outcome, phase, apiErr)
	result := newResult(op)
	result.Outcome = apiErr.Outcome
	result.Error = apiErr.Public()
	result.Next = apiErr.Next
	if apiErr.Details != nil {
		if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
			result.BlockingOperationID = blocking
		}
	}
	return result
}

var _ = context.Background
var _ = strings.HasPrefix
