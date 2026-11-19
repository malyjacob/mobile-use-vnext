package engine

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// snapAdmission is one atomic snap admission: P registration or duplicate
// replay, original-reference resolution and work-position binding happen under
// one state lock, so an explicit end(P) cannot close the P between registration
// and work binding.
type snapAdmission struct {
	work      *workState
	op        *operationState
	duplicate *protocol.Result
	apiErr    *protocol.APIError
	cancelled bool
}

// Snap reads one image of the display bound to the referenced original session.
// It performs no tree read, dispatches no input, publishes no observation,
// confirms no baseline, renews no lease and changes no revision. Success
// delivers one top-level image exactly once; a duplicate P or status(P) replays
// only the bounded operation summary.
func (e *Engine) Snap(ctx context.Context, agent, p string,
	req *protocol.SnapRequest) (finalResult *protocol.Result, finalErr *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("snap 请求缺失")
	}
	admission := e.admitSnap(ctx, agent, p, req.Reference)
	if admission.duplicate != nil {
		return admission.duplicate, nil
	}
	if admission.apiErr != nil {
		if admission.op == nil {
			return nil, admission.apiErr
		}
		return e.finishSnapError(admission.op, "admission", admission.apiErr), nil
	}
	op := admission.op
	defer func() { e.applyFrozenResult(op, &finalResult) }()
	if admission.cancelled {
		e.mu.Lock()
		e.finishOperationLocked(op, "", "cancelled", nil)
		e.mu.Unlock()
		return &protocol.Result{OperationID: op.ID}, nil
	}
	work := admission.work
	defer e.releaseWork(work)
	return e.runSnap(work, op)
}

// admitSnap performs the complete snap admission in one critical section. A
// rejected P never gains session ownership; an admitted P is bound to the
// target session origin and the fixed display snapshot.
func (e *Engine) admitSnap(parent context.Context, agent, p, reference string) snapAdmission {
	now := time.Now()
	parentCtx := parent
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	op, duplicate, apiErr := e.registerOperationLocked(p, agent, WorkSnap, false)
	if apiErr != nil || duplicate != nil {
		return snapAdmission{op: op, duplicate: duplicate, apiErr: apiErr}
	}
	e.detachRetiredLocked(now)
	if e.shuttingDown {
		return snapAdmission{op: op,
			apiErr: protocol.NewError(503, protocol.CodeDeviceUnavailable, "服务正在退出")}
	}
	// This request's own pre-assigned P was never a usable prior reference.
	// A previously closed P is identified by registration before this point.
	if reference == op.ID {
		return snapAdmission{op: op,
			apiErr: protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")}
	}
	target, resolveErr := e.resolveObserveTargetLocked(agent, reference)
	if resolveErr != nil {
		return snapAdmission{op: op, apiErr: resolveErr}
	}
	// A snap never revives a lease it found already expired.
	if !sessionBeforeDeadline(target.session, now) {
		return snapAdmission{op: op,
			apiErr: protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")}
	}
	if !e.deviceReady {
		return snapAdmission{op: op,
			apiErr: protocol.NewError(503, protocol.CodeDeviceUnavailable, "设备不可用")}
	}
	if target.session.AppInstanceID != e.app.InstanceID {
		return snapAdmission{op: op,
			apiErr: protocol.NewError(409, protocol.CodeControlUnavailable, "控制端不可用")}
	}
	if target.session.Mode != e.app.Mode || target.session.ModeGeneration != e.app.ModeGeneration {
		return snapAdmission{op: op,
			apiErr: protocol.NewError(409, protocol.CodeModeNotSelected, "尚未选择可用模式")}
	}
	if barrier := e.ordinaryBarrierLocked(op, now); barrier != nil {
		return snapAdmission{op: op, apiErr: barrier}
	}
	if e.work != nil {
		busy := protocol.NewError(409, protocol.CodeDeviceBusy, "设备工作尚未完成")
		if e.work.Agent == op.Agent {
			busy.WithDetail("blocking_operation_id", e.work.OperationID)
		}
		return snapAdmission{op: op, apiErr: busy}
	}
	// A request cancelled before any work is bound is recorded as its own
	// cancellation, never as a device failure.
	if parentCtx.Err() != nil {
		return snapAdmission{op: op, cancelled: true}
	}
	timeout := e.cfg.SnapTimeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	if e.maintenanceCancel != nil {
		e.maintenanceCancel()
	}
	workCtx, cancel := context.WithTimeout(parentCtx, timeout)
	work := &workState{
		OperationID: op.ID, Agent: op.Agent, Kind: WorkSnap, Phase: "admitted",
		Start: now, Deadline: now.Add(timeout), Ctx: workCtx, Cancel: cancel,
		Projector: e.projector, SessionEpoch: target.session.Epoch,
		Parent: parentCtx, Display: target.session.Display,
	}
	op.SessionOrigin = target.session.Origin
	e.work = work
	e.broadcastAppLocked()
	return snapAdmission{work: work, op: op}
}

// runSnap performs the one source read of the request and its single encode.
// The commit remains the only publication point.
func (e *Engine) runSnap(work *workState, op *operationState) (*protocol.Result, *protocol.APIError) {
	e.setOperationPhase(op, "capture")
	e.setWorkPhase(work, "snap.capture")
	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	probe, probeErr := e.dev.ProbeDisplay(probeCtx, work.Display.ID)
	cancel()
	if probeErr != nil || !probe.Exists || probe.SFID == 0 {
		return e.finishSnapPhase(op, work, "failed",
			protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")), nil
	}
	attempt, imageErr := e.readImageForAcquisitionOnDisplay(work.Ctx, work, work.Display.ID,
		"snap", probe.SFID)
	if imageErr != nil {
		return e.finishSnapPhase(op, work, "failed", imageErr), nil
	}
	e.setOperationPhase(op, "image")
	// The encode happens exactly once, outside the state lock, against the
	// display geometry fixed at admission.
	e.encodeImageForDisplay(attempt, work.Display)
	if attempt.err != nil {
		return e.finishSnapPhase(op, work, "failed", attempt.err), nil
	}
	if attempt.meta == nil || len(attempt.data) == 0 {
		return e.finishSnapPhase(op, work, "failed",
			protocol.NewError(503, protocol.CodeImageEncode, "图片编码失败")), nil
	}
	e.setOperationPhase(op, "commit")
	e.setWorkPhase(work, "snap.commit")
	return e.commitSnap(work, op, attempt), nil
}

// commitSnap is the single publication point of one snap. Every gate and the
// terminal transition run under one short state lock: a STOP, display change,
// cancellation or deadline that arrives first discards the candidate image.
func (e *Engine) commitSnap(work *workState, op *operationState, attempt *imageAttempt) *protocol.Result {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State == OperationFinished {
		return e.operationViewLocked(op)
	}
	if phase, apiErr := e.snapUncommittedOutcomeLocked(work, now); phase != "" {
		return e.finishSnapPhaseLocked(op, phase, apiErr)
	}
	// The session display binding and the geometry snapshot fixed at admission
	// must still hold; a display report during the read invalidates the image.
	if e.session.Display.ID != work.Display.ID ||
		e.session.Display.Generation != work.Display.Generation ||
		e.session.Display.Width != work.Display.Width ||
		e.session.Display.Height != work.Display.Height ||
		e.session.Display.Rotation != work.Display.Rotation {
		return e.finishSnapPhaseLocked(op, "failed",
			protocol.NewError(409, protocol.CodeStaleScreen, "目标屏幕代次已变化"))
	}
	if now.Sub(attempt.source.CapturedAt) > e.cfg.CaptureMaxAge {
		return e.finishSnapPhaseLocked(op, "failed",
			protocol.NewError(503, protocol.CodeImageStale, "截图已过期"))
	}
	meta := *attempt.meta
	if meta.CapturedAt == "" {
		meta.CapturedAt = formatTime(attempt.source.CapturedAt)
	}
	meta.ReadOnly = true
	meta.Screen = screenOf(work.Display)
	meta.Error = nil
	meta.Base64 = base64.StdEncoding.EncodeToString(attempt.data)
	e.finishOperationLocked(op, "", "finished", nil)
	return &protocol.Result{OperationID: op.ID, Image: &meta}
}

// snapUncommittedOutcomeLocked classifies an uncommitted snap terminal in the
// fixed order: session revocation and lease first, then the caller's request
// cancellation and this tool's own deadline. It returns an empty phase when the
// read may still commit.
func (e *Engine) snapUncommittedOutcomeLocked(work *workState, now time.Time) (string, *protocol.APIError) {
	if work == nil {
		return "revoked", protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	session := e.session
	if e.work != work ||
		session == nil || session.State == SessionEnded ||
		session.Epoch != work.SessionEpoch || e.shuttingDown || !e.deviceReady ||
		session.AppInstanceID != e.app.InstanceID ||
		session.ModeGeneration != e.app.ModeGeneration || session.Mode != e.app.Mode ||
		!sessionBeforeDeadline(session, now) {
		return "revoked", protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	if work.Parent != nil && work.Parent.Err() != nil {
		return "cancelled", nil
	}
	if !work.Deadline.IsZero() && !now.Before(work.Deadline) {
		return "work_deadline", protocol.NewError(503, protocol.CodeImageCapture,
			"整次截图读取已超时")
	}
	if work.Ctx != nil && work.Ctx.Err() == context.DeadlineExceeded {
		return "work_deadline", protocol.NewError(503, protocol.CodeImageCapture,
			"整次截图读取已超时")
	}
	return "", nil
}

// finishSnapPhase records one phase failure, re-checking the stop conditions
// first so a revoked or cancelled read never reports a device-phase error.
func (e *Engine) finishSnapPhase(op *operationState, work *workState,
	phase string, apiErr *protocol.APIError) *protocol.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State == OperationFinished {
		return e.operationViewLocked(op)
	}
	if work != nil {
		if stopPhase, stopErr := e.snapUncommittedOutcomeLocked(work, time.Now()); stopPhase != "" {
			phase, apiErr = stopPhase, stopErr
		}
	}
	return e.finishSnapPhaseLocked(op, phase, apiErr)
}

// finishSnapError finishes an admission rejection. It never invents an action
// outcome or a tool-level recovery.
func (e *Engine) finishSnapError(op *operationState, phase string,
	apiErr *protocol.APIError) *protocol.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.finishSnapPhaseLocked(op, phase, apiErr)
}

// finishSnapPhaseLocked is the terminal builder. The caller holds e.mu.
func (e *Engine) finishSnapPhaseLocked(op *operationState, phase string,
	apiErr *protocol.APIError) *protocol.Result {
	if op.State == OperationFinished {
		return e.operationViewLocked(op)
	}
	if apiErr == nil {
		e.finishOperationLocked(op, "", phase, nil)
		return &protocol.Result{OperationID: op.ID}
	}
	e.finishOperationLocked(op, "", phase, apiErr)
	result := &protocol.Result{OperationID: op.ID, Error: apiErr.Public()}
	if apiErr.HTTPStatus != 0 && apiErr.HTTPStatus != protocol.Status(apiErr.Code) {
		result.HTTPStatus = apiErr.HTTPStatus
	}
	if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
		result.BlockingOperationID = blocking
	}
	return result
}
