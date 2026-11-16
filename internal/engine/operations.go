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
	return e.registerOperationBound(p, agent, kind, true)
}

// registerDetachedOperation admits a P that is deliberately not bound to the
// current session (app.find and notify discovery calls). end(thatP)
// therefore cannot terminate an unrelated active session.
func (e *Engine) registerDetachedOperation(p, agent, kind string) (*operationState, *protocol.Result, *protocol.APIError) {
	return e.registerOperationBound(p, agent, kind, false)
}

func (e *Engine) registerOperationBound(p, agent, kind string, bindSession bool) (*operationState, *protocol.Result, *protocol.APIError) {
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
			OperationID: p, OperationState: OperationFinished, Outcome: closedOutcome(e.closedKnown[p]),
			RequestClosed: &closed,
			Recovery: planRecovery(recoveryInput{
				Reference: protocol.RecoveryReferenceP, Code: protocol.CodeRequestClosed,
			}),
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
	if bindSession && e.session != nil {
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

// recordScrollAction records the semantic action of one controlled-scroll
// attempt before the work position is taken. The status lock is held only for
// the one bounded write; an existing action is never overwritten.
func (e *Engine) recordScrollAction(op *operationState) {
	if op == nil {
		return
	}
	e.mu.Lock()
	if op.State == OperationRunning && op.ActAction == "" {
		op.ActAction = "scroll"
	}
	e.mu.Unlock()
}

// commitScrollEffect saves the computed effect as a bounded operation fact. It
// is the last conditional gate before finishOperation: a revoked work, an
// ended/frozen operation or a changed owner leaves the fact uncommitted, so a
// late CPU result can never write back to a frozen P. No device I/O and no tree
// algorithm runs under the lock.
func (e *Engine) commitScrollEffect(work *workState, op *operationState, state string) bool {
	if op == nil || state == "" {
		return false
	}
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, now) || work.Ctx.Err() != nil ||
		op.ActAction != "scroll" {
		return false
	}
	op.Effect = &protocol.Effect{State: state}
	return true
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

func (e *Engine) finishOperation(op *operationState, outcome, phase string, apiErr *protocol.APIError) *protocol.Recovery {
	if op == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State == OperationFinished {
		return op.Recovery
	}
	op.State = OperationFinished
	op.Outcome = outcome
	op.Phase = phase
	op.FinishedAt = time.Now()
	code := ""
	if apiErr != nil {
		op.Error = apiErr.Public()
		code = apiErr.Code
	}
	inputState := op.InputState
	if inputState == "" {
		inputState = inputStateFor(op.Kind, op.AppKind, op.Reference, code, op.Claimed)
	}
	op.Recovery = planRecovery(recoveryInput{
		Tool: op.Kind, AppKind: op.AppKind, Reference: op.Reference, Code: code, Phase: phase,
		Outcome: outcome, Claimed: op.Claimed, Delivered: op.Delivered,
		InputState: inputState,
		Cleanup:    cleanupValue(e.cleanupForOriginLocked(op.SessionOrigin)),
	})
	op.Recovery = e.durabilityRecoveryLocked(protocol.RecoveryReferenceP, inputState, op.Recovery)
	return op.Recovery
}

// setOperationReference records the reference kind the request already used.
// It is a planning fact only and never grants authority.
func (e *Engine) setOperationReference(op *operationState, reference string) {
	if op == nil {
		return
	}
	e.mu.Lock()
	op.Reference = reference
	e.mu.Unlock()
}

// acquireWork takes the single ordinary device work position. Device I/O and
// tree work happen outside the state lock.
func (e *Engine) acquireWork(op *operationState, kind string) (*workState, *protocol.APIError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.detachRetiredLocked(time.Now())
	if e.shuttingDown {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "服务正在退出")
	}
	if barrier := e.ordinaryBarrierLocked(op, time.Now()); barrier != nil {
		return nil, barrier
	}
	if e.work != nil {
		var blocking string
		if e.work.Agent == op.Agent {
			blocking = e.work.OperationID
		}
		return nil, &protocol.APIError{
			Code: protocol.CodeDeviceBusy, Message: "设备工作尚未完成",
			HTTPStatus: 409, Outcome: OutcomeNotExecuted,
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
	case WorkAppQuery:
		timeout = e.cfg.WorkTimeout + 10*time.Second
	case WorkAppAction:
		timeout = 2*e.cfg.WorkTimeout + 10*time.Second
	case WorkShell:
		timeout = 65 * time.Second
	}
	if e.maintenanceCancel != nil {
		e.maintenanceCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	work := &workState{
		OperationID: op.ID, Agent: op.Agent, Kind: kind, Phase: "admitted",
		Start: time.Now(), Deadline: time.Now().Add(timeout), Ctx: ctx, Cancel: cancel, Projector: e.projector,
	}
	if e.session != nil {
		work.SessionEpoch = e.session.Epoch
		if e.projectorFactory != nil && e.session.State != SessionEnded && e.session.Projector != nil {
			work.Projector = e.session.Projector
		}
	}
	e.work = work
	e.broadcastAppLocked()
	return work, nil
}

func (e *Engine) setWorkPhase(work *workState, phase string) {
	e.mu.Lock()
	if e.work == work && work.Phase != phase {
		work.Phase = phase
		e.broadcastAppLocked()
	}
	e.mu.Unlock()
}

// setOperationWait saves the bounded condition-wait outcome of one operation.
// The write is conditional: a finished or frozen operation never accepts a
// later value, so a late phase-C result can never rewrite a closed P.
func (e *Engine) setOperationWait(op *operationState, wait *protocol.WaitResult) bool {
	if op == nil || wait == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning {
		return false
	}
	saved := *wait
	if len(wait.Matched) > 0 {
		saved.Matched = append([]string(nil), wait.Matched...)
	}
	op.Wait = &saved
	return true
}

// acquireWaitWork takes the single ordinary device work position for one
// condition wait. Reference resolution, the expired-session refusal, the
// ordinary barrier, the work conflict, the work record and the lease renewal
// all happen in one bounded critical section, so a wait can never be admitted
// into an already expired session and then killed halfway through its own
// window. The work context inherits the caller's request context, so a client
// disconnect or an MCP cancellation releases the position immediately. The
// second result is the force-full decision of the resolved reference.
func (e *Engine) acquireWaitWork(op *operationState, agent, reference string,
	parent context.Context, timeout time.Duration) (*workState, bool, *protocol.APIError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	e.detachRetiredLocked(now)
	if e.shuttingDown {
		return nil, false, protocol.NewError(503, protocol.CodeDeviceUnavailable, "服务正在退出")
	}
	target, resolveErr := e.resolveObserveTargetLocked(agent, reference)
	if resolveErr != nil {
		return nil, false, resolveErr
	}
	// A condition wait never revives a lease it found already expired. This is
	// the explicit difference from observe, which lets the work authorization
	// check report control_revoked instead.
	if !sessionBeforeDeadline(target.session, now) {
		return nil, false, protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")
	}
	if barrier := e.ordinaryBarrierLocked(op, now); barrier != nil {
		return nil, false, barrier
	}
	if e.work != nil {
		var blocking string
		if e.work.Agent == op.Agent {
			blocking = e.work.OperationID
		}
		return nil, false, &protocol.APIError{
			Code: protocol.CodeDeviceBusy, Message: "设备工作尚未完成",
			HTTPStatus: 409, Outcome: OutcomeNotExecuted,
			Details: map[string]any{"blocking_operation_id": blocking},
		}
	}
	workTimeout := timeout + waitWorkReserve
	if e.maintenanceCancel != nil {
		e.maintenanceCancel()
	}
	ctx, cancel := context.WithTimeout(parent, workTimeout)
	work := &workState{
		OperationID: op.ID, Agent: op.Agent, Kind: WorkWaitFor, Phase: "admitted",
		Start: now, Deadline: now.Add(workTimeout), Ctx: ctx, Cancel: cancel,
		Projector: e.projector, SessionEpoch: target.session.Epoch,
	}
	if e.projectorFactory != nil && target.session.State != SessionEnded &&
		target.session.Projector != nil {
		work.Projector = target.session.Projector
	}
	e.work = work
	// A successful wait(O) reads one still-usable full candidate, so it is the
	// same reading acknowledgement observe performs after admission.
	if target.obs != nil && target.obs == e.current &&
		target.obs.Full && target.obs.Status == ObservationReady {
		e.confirmBaselineLocked(target.obs)
	}
	e.extendSessionLocked(now, e.cfg.SessionTTL)
	e.broadcastAppLocked()
	// A P reference reads a new tree from scratch, so the target carries the
	// same force-full decision observe already honours.
	return work, target.forceFull, nil
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
		e.detachRetiredLocked(time.Now())
		e.broadcastAppLocked()
	}
	e.mu.Unlock()
	e.launchExecutionCleanupRecheck()
}

// workRevoked reports whether STOP/end cancelled this work or its session.
func (e *Engine) workRevoked(work *workState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.workAuthorizedLocked(work, time.Now())
}

// detachedWorkRevoked is the session-free variant used by discovery work
// (apps): it checks the work position, revocation, shutdown and context only.
// It never requires an active session.
func (e *Engine) detachedWorkRevoked(work *workState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return work == nil || e.work != work || work.Revoked ||
		e.shuttingDown || work.Ctx.Err() != nil
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
	return now.Before(session.ExpiresAt)
}

func (e *Engine) workAuthorizedLocked(work *workState, now time.Time) bool {
	return work != nil && e.work == work && !work.Revoked && !e.shuttingDown && e.deviceReady &&
		work.Ctx.Err() == nil && e.session != nil && e.session.State != SessionEnded &&
		e.session.Epoch == work.SessionEpoch && sessionBeforeDeadline(e.session, now) &&
		e.session.AppInstanceID == e.app.InstanceID && e.session.ModeGeneration == e.app.ModeGeneration &&
		e.session.Mode == e.app.Mode
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
	// Semantic uncertainty never recreates a completed input responsibility:
	// only this call's own token can make its dispatch unproven.
	work.DispatchUnknown = work.UI != nil && e.ui == work.UI && e.ui.record.State != uiStateNotDispatched
	if e.session != nil && e.session.Epoch == work.SessionEpoch {
		e.session.ExecutionUnknown = e.executionUnknownLocked()
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
		HTTPStatus: 409, Outcome: OutcomeNotExecuted,
	}
	if e.work != nil && op != nil && e.work.Agent == op.Agent {
		apiErr.Details = map[string]any{"blocking_operation_id": e.work.OperationID}
	}
	return apiErr
}

// newResult builds a result owned by one operation. The immutable text_input
// snapshot, when present, is copied into every first result of that operation.
func newResult(op *operationState) *protocol.Result {
	if op == nil {
		return &protocol.Result{}
	}
	result := &protocol.Result{OperationID: op.ID}
	if op.TextInput != nil {
		snapshot := *op.TextInput
		result.TextInput = &snapshot
	}
	return result
}

// newResult takes the same snapshot under the state lock so a concurrent
// stage advance cannot race the pointer/string copy.
func (e *Engine) newResult(op *operationState) *protocol.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	return newResult(op)
}

// finishWithError marks the operation and returns the public error result. It
// is the single funnel for every tool failure: the recovery projection is
// planned once from the operation's own facts.
func (e *Engine) finishWithError(op *operationState, phase string, apiErr *protocol.APIError) *protocol.Result {
	if apiErr.Outcome == "" {
		apiErr.Outcome = OutcomeNotExecuted
	}
	result := e.newResult(op)
	result.Outcome = apiErr.Outcome
	result.Error = apiErr.Public()
	result.Recovery = e.finishOperation(op, apiErr.Outcome, phase, apiErr)
	if apiErr.Details != nil {
		if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
			result.BlockingOperationID = blocking
		}
	}
	return result
}

var _ = context.Background
var _ = strings.HasPrefix
