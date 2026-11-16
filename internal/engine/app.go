package engine

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// App is the single application lifecycle entry. The kind selects exactly one
// closed branch; every branch reuses the same session, observation and work
// authority. Queries (find/list) never consume the observation and have no
// action outcome; the five mutating kinds claim one current ready observation
// and report outcome, post-observation and recovery.
func (e *Engine) App(ctx context.Context, agent, p string, req *protocol.AppRequest) (finalResult *protocol.Result, finalErr *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("app 请求缺失")
	}
	switch req.Kind {
	case protocol.AppKindOpen:
		if req.Reference == "" || req.Target == nil || req.Target.UseTask || req.Target.App == "" {
			return nil, protocol.Invalid("open 需要 reference 与 target.app")
		}
	case protocol.AppKindClaim, protocol.AppKindHandoff, protocol.AppKindClose:
		if req.Reference == "" || req.Target == nil {
			return nil, protocol.Invalid(req.Kind + " 需要 reference 与 target")
		}
	case protocol.AppKindKill:
		if req.Reference == "" || req.Target == nil || !req.Target.UseTask {
			return nil, protocol.Invalid("kill 需要 reference 与 target.task_id")
		}
	}
	var (
		op        *operationState
		duplicate *protocol.Result
		apiErr    *protocol.APIError
	)
	switch req.Kind {
	case protocol.AppKindFind:
		op, duplicate, apiErr = e.registerDetachedOperation(p, agent, appTool)
	case protocol.AppKindList:
		op, duplicate, apiErr = e.registerOperation(p, agent, appTool)
	case protocol.AppKindOpen, protocol.AppKindClaim, protocol.AppKindHandoff,
		protocol.AppKindClose, protocol.AppKindKill:
		op, duplicate, apiErr = e.registerOperation(p, agent, appTool)
	default:
		return nil, protocol.Invalid("不支持的 app kind").WithDetail("allowed_kinds", protocol.AppKinds)
	}
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	e.setOperationAppKind(op, req.Kind)
	defer func() {
		e.applyFrozenResult(op, &finalResult)
	}()
	switch req.Kind {
	case protocol.AppKindFind, protocol.AppKindList:
		e.setOperationReference(op, referenceKind(req.Reference))
		return e.appQuery(ctx, op, req)
	case protocol.AppKindOpen:
		e.setOperationReference(op, protocol.RecoveryReferenceO)
		return e.appOpen(ctx, op, req)
	case protocol.AppKindClaim:
		e.setOperationReference(op, protocol.RecoveryReferenceO)
		return e.appClaim(ctx, op, req)
	case protocol.AppKindHandoff:
		e.setOperationReference(op, protocol.RecoveryReferenceO)
		return e.appHandoff(ctx, op, req)
	case protocol.AppKindClose:
		e.setOperationReference(op, protocol.RecoveryReferenceO)
		return e.appClose(ctx, op, req)
	case protocol.AppKindKill:
		e.setOperationReference(op, protocol.RecoveryReferenceO)
		return e.appKill(ctx, op, req)
	}
	return nil, protocol.Invalid("不支持的 app kind").WithDetail("allowed_kinds", protocol.AppKinds)
}

// setOperationAppKind records the app branch of this operation. It is the only
// fact a duplicate-P or status(P) summary republishes.
func (e *Engine) setOperationAppKind(op *operationState, kind string) {
	if op == nil {
		return
	}
	e.mu.Lock()
	op.AppKind = kind
	e.mu.Unlock()
}

// appStaticCheckLocked validates the active session and the current ready
// observation without claiming or dispatching anything.
func (e *Engine) appStaticCheckLocked(agent, observationID string, now time.Time) (*observationState, *sessionState, *protocol.APIError) {
	session := e.session
	if session == nil || session.State != SessionActive {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	if !sessionBeforeDeadline(session, now) {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")
	}
	obs := e.observations[observationID]
	if obs == nil || obs.Agent != agent || obs.SessionEpoch != session.Epoch {
		return nil, nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	if e.current != obs {
		return nil, nil, protocol.NewError(409, protocol.CodeObservationRetired, "观察已被替换")
	}
	switch obs.Status {
	case ObservationReady:
	case ObservationClaimed:
		return nil, nil, protocol.NewError(409, protocol.CodeObservationClaimed, "观察已被消费")
	case ObservationExpired:
		return nil, nil, protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	default:
		return nil, nil, protocol.NewError(409, protocol.CodeObservationRetired, "观察已退役")
	}
	if now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		return nil, nil, protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	}
	return obs, session, nil
}

// appModeGuardLocked rejects a virtual-only kind on the primary display before
// any observation is claimed. find/list/open are shared; the four window/
// process kinds are virtual only.
func (e *Engine) appModeGuardLocked(session *sessionState, kind string) *protocol.APIError {
	switch kind {
	case protocol.AppKindFind, protocol.AppKindList, protocol.AppKindOpen:
		return nil
	}
	if session == nil || session.Mode != "virtual" {
		return protocol.NewError(409, protocol.CodeWindowActionPrimary,
			"主屏模式不支持该应用生命周期操作")
	}
	return nil
}

// appKindOnly builds the minimum app object a rejected or summary result may
// carry. It never invents an identity or a landing point.
func appKindOnly(kind string) *protocol.AppView {
	if kind == "" {
		return nil
	}
	return &protocol.AppView{Kind: kind}
}

// appViewWithDisplay adds one already-proven display identity.
func appViewWithDisplay(kind string, displayID int) *protocol.AppView {
	view := appKindOnly(kind)
	if view == nil {
		return nil
	}
	value := displayID
	view.DisplayID = &value
	return view
}

func appIntPtr(value int) *int {
	copy := value
	return &copy
}

func appBoolPtr(value bool) *bool {
	copy := value
	return &copy
}

// finishAppError is the single funnel of a mutating app branch that did not
// execute. The outcome must be explicit; the app object carries only the facts
// the caller had already proven.
func (e *Engine) finishAppError(op *operationState, phase string, view *protocol.AppView, apiErr *protocol.APIError) (*protocol.Result, *protocol.APIError) {
	if apiErr.Outcome == "" {
		apiErr.Outcome = OutcomeNotExecuted
	}
	if view == nil {
		view = appKindOnly(op.AppKind)
	}
	result := newResult(op)
	result.Outcome = apiErr.Outcome
	result.App = view
	result.Error = apiErr.Public()
	result.Recovery = e.finishOperation(op, apiErr.Outcome, phase, apiErr)
	if apiErr.Details != nil {
		if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
			result.BlockingOperationID = blocking
		}
	}
	return result, nil
}

// appPostObservation performs the single full, image-free observation of the
// original session display after one app action. A revoked session publishes
// nothing; a failed capture is reported without rewriting the action outcome.
func (e *Engine) appPostObservation(work *workState, op *operationState) (*protocol.Observation, string) {
	if e.detachedWorkRevoked(work) {
		return nil, ""
	}
	session := e.sessionForWork(work)
	if session == nil {
		return nil, ""
	}
	public, _, postErr := e.captureAndPublish(work.Ctx, work, session, true, false, appTool+"."+op.AppKind)
	if postErr != nil {
		return &protocol.Observation{
			Status: "failed", Missing: []string{"tree"}, Error: postErr.Public(),
		}, "post_observation_failed"
	}
	return public, "dispatch"
}

// finishAppExecuted commits one confirmed mutating action and performs exactly
// one full post observation. A post-observation failure never rewrites executed.
func (e *Engine) finishAppExecuted(work *workState, op *operationState, view *protocol.AppView) (*protocol.Result, *protocol.APIError) {
	e.noteKnownOutcome(op, OutcomeExecuted)
	result := newResult(op)
	result.Outcome = OutcomeExecuted
	result.App = view
	if e.detachedWorkRevoked(work) {
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	if e.sessionForWork(work) == nil {
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	public, phase := e.appPostObservation(work, op)
	if public != nil {
		result.Observation = public
	}
	if phase == "post_observation_failed" {
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "post_observation_failed", nil)
		return result, nil
	}
	result.Recovery = e.finishOperation(op, OutcomeExecuted, "finished", nil)
	return result, nil
}

// finishAppUncertainWork keeps every proven fact of a dispatched-but-unconfirmed
// action and still delivers the post observation of the original display.
func (e *Engine) finishAppUncertainWork(work *workState, op *operationState, view *protocol.AppView,
	code, message string, details map[string]any) (*protocol.Result, *protocol.APIError) {
	apiErr := protocol.NewError(protocol.Status(code), code, message)
	if apiErr.HTTPStatus == 500 {
		apiErr.HTTPStatus = 409
	}
	apiErr.Outcome = OutcomeUncertain
	for key, value := range details {
		apiErr.WithDetail(key, value)
	}
	if view == nil {
		view = appKindOnly(op.AppKind)
	}
	result := newResult(op)
	result.Outcome = OutcomeUncertain
	result.App = view
	result.Error = apiErr.Public()
	phase := "dispatch"
	if work != nil {
		if public, postPhase := e.appPostObservation(work, op); public != nil {
			result.Observation = public
			phase = postPhase
		}
	}
	result.Recovery = e.finishOperation(op, OutcomeUncertain, phase, apiErr)
	e.addSessionIfEnded(result)
	return result, nil
}

// finishAppQuerySuccess returns one complete query fact without any outcome.
func (e *Engine) finishAppQuerySuccess(op *operationState, view *protocol.AppView) *protocol.Result {
	result := newResult(op)
	result.App = view
	e.finishOperation(op, "", "finished", nil)
	return result
}

// finishAppQueryError returns the query failure facts: no outcome, no invented
// collection, and only the facts that were actually read.
func (e *Engine) finishAppQueryError(op *operationState, phase string, view *protocol.AppView, apiErr *protocol.APIError) *protocol.Result {
	if view == nil {
		view = appKindOnly(op.AppKind)
	}
	result := newResult(op)
	result.App = view
	result.Error = apiErr.Public()
	result.Recovery = e.finishOperation(op, "", phase, apiErr)
	if apiErr.Details != nil {
		if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
			result.BlockingOperationID = blocking
		}
	}
	e.addSessionIfEnded(result)
	return result
}

// appQueryClosed closes one query that may not publish. A cancelled request is
// only a closed query; an expired work budget is a read failure. Neither carries
// an action outcome.
func (e *Engine) appQueryClosed(op *operationState, work *workState) *protocol.Result {
	if work != nil && errors.Is(work.Ctx.Err(), context.DeadlineExceeded) {
		apiErr := protocol.NewError(503, protocol.CodeDeviceUnavailable,
			"查询已超出工作期限")
		e.mu.Lock()
		if op.State == OperationRunning {
			op.State = OperationFinished
			op.Phase = "query_timeout"
			op.FinishedAt = time.Now()
			op.Outcome = ""
			op.Error = apiErr.Public()
			op.Recovery = nil
		}
		e.mu.Unlock()
		result := newResult(op)
		result.OperationState = OperationFinished
		result.Phase = "query_timeout"
		result.App = appKindOnly(op.AppKind)
		result.Error = apiErr.Public()
		e.addSessionIfEnded(result)
		return result
	}
	return e.appQueryCancelled(op, "query_cancelled")
}

// appQueryCancelled closes one query without an outcome or a business error. It
// proves only that this query was closed and read nothing publishable.
func (e *Engine) appQueryCancelled(op *operationState, phase string) *protocol.Result {
	e.mu.Lock()
	if op.State == OperationRunning {
		op.State = OperationFinished
		op.Phase = phase
		op.FinishedAt = time.Now()
		op.Outcome = ""
		op.Error = nil
		op.Recovery = nil
	}
	e.mu.Unlock()
	result := newResult(op)
	result.OperationState = OperationFinished
	result.Phase = phase
	result.App = appKindOnly(op.AppKind)
	e.addSessionIfEnded(result)
	return result
}

// acquireAppQueryWork takes the query work position with the request context
// inherited. A caller cancellation therefore stops the read instead of holding
// the ordinary position for the full work budget.
func (e *Engine) acquireAppQueryWork(op *operationState, parent context.Context) (*workState, *protocol.APIError) {
	e.mu.Lock()
	e.detachRetiredLocked(time.Now())
	if e.shuttingDown {
		e.mu.Unlock()
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "服务正在退出")
	}
	if barrier := e.ordinaryBarrierLocked(op, time.Now()); barrier != nil {
		e.mu.Unlock()
		return nil, barrier
	}
	if e.work != nil {
		var blocking string
		if e.work.Agent == op.Agent {
			blocking = e.work.OperationID
		}
		e.mu.Unlock()
		return nil, &protocol.APIError{
			Code: protocol.CodeDeviceBusy, Message: "设备工作尚未完成",
			HTTPStatus: 409, Outcome: OutcomeNotExecuted,
			Details: map[string]any{"blocking_operation_id": blocking},
		}
	}
	timeout := e.cfg.WorkTimeout + 10*time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	if e.maintenanceCancel != nil {
		e.maintenanceCancel()
	}
	work := &workState{
		OperationID: op.ID, Agent: op.Agent, Kind: WorkAppQuery, Phase: "admitted",
		Start: time.Now(), Deadline: time.Now().Add(timeout), Ctx: ctx, Cancel: cancel,
		Projector: e.projector,
	}
	if e.session != nil {
		work.SessionEpoch = e.session.Epoch
		work.Projector = e.session.Projector
	}
	e.work = work
	e.broadcastAppLocked()
	e.mu.Unlock()
	// The caller's cancellation releases the ordinary position immediately: a
	// read that ignores its context can never hold the device for the whole
	// work budget after its request is gone. The watcher ends when the work
	// context is cancelled by this release or by STOP.
	go e.watchQueryCancel(work)
	return work, nil
}

// watchQueryCancel freezes a cancelled query and releases the work position.
func (e *Engine) watchQueryCancel(work *workState) {
	if work == nil || work.Ctx.Done() == nil {
		return
	}
	<-work.Ctx.Done()
	e.mu.Lock()
	if e.work == work {
		e.detachWorkLocked(work)
	}
	e.mu.Unlock()
}

// appQueryContextRevoked reports a query whose caller or session ended. The
// query may publish nothing after this point.
func (e *Engine) appQueryContextRevoked(work *workState) bool {
	if work == nil {
		return true
	}
	if work.Ctx.Err() != nil {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.work != work || work.Revoked || e.shuttingDown
}

// appCapacityRefusal refuses a new ownership or transfer record before any
// observation is claimed and before any side effect. The complete serialized
// ledger is the final gate; the array bounds are checked first so a refusal
// names the real limit.
func (e *Engine) appCapacityRefusalLocked(extraTask *LedgerTask, extraTransfer *LedgerTransfer) *protocol.APIError {
	if extraTask != nil && e.session != nil && len(e.session.Tasks) >= 16 {
		return capacityError("会话自有任务数量达到上限", 16)
	}
	if extraTransfer != nil {
		if len(e.transfers) >= e.cfg.MaxAppTransfers {
			return capacityError("迁移记录数量达到上限", e.cfg.MaxAppTransfers)
		}
		if encoded, err := json.Marshal(*extraTransfer); err != nil || len(encoded) > 1000 {
			return capacityError("迁移记录超过单条上限", 1000)
		}
	}
	record := e.ledgerRecordLocked()
	if record == nil {
		return protocol.NewError(503, protocol.CodeDeviceUnavailable, "恢复账本不可用")
	}
	record = CloneLedger(record)
	if extraTask != nil {
		record.Tasks = append(record.Tasks, *extraTask)
	}
	if extraTransfer != nil {
		record.TaskTransfers = append(record.TaskTransfers, *extraTransfer)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return protocol.NewError(503, protocol.CodeDeviceUnavailable, "恢复账本编码失败")
	}
	if len(encoded) > e.cfg.MaxLedgerBytes {
		return capacityError("恢复账本超过总容量", e.cfg.MaxLedgerBytes)
	}
	return nil
}

func capacityError(message string, limit int) *protocol.APIError {
	apiErr := protocol.NewError(protocol.Status(protocol.CodeAppCapacity),
		protocol.CodeAppCapacity, message)
	apiErr.WithDetail("limit", limit)
	return apiErr
}

// liveTasksByPackage returns the live tasks of one package on one display in
// ascending task-id order. known is false when the list could not be read.
func (e *Engine) liveTasksByPackage(ctx context.Context, displayID int, pkg string) ([]device.Task, bool) {
	all, err := e.dev.TaskList(ctx)
	if err != nil {
		return nil, false
	}
	var matches []device.Task
	for _, task := range all {
		if task.DisplayID == displayID && task.Package == pkg && task.TaskID > 0 {
			matches = append(matches, task)
		}
	}
	sortTasks(matches)
	return matches, true
}

// sortTasks orders tasks by ascending task id, the deterministic selector order.
func sortTasks(tasks []device.Task) {
	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0 && tasks[j].TaskID < tasks[j-1].TaskID; j-- {
			tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
		}
	}
}

// resolveAppTarget resolves one admitted selector against the live task list of
// the source display. Zero matches is task_not_found; several matches is a
// controlled ambiguity carrying the sorted candidate ids.
func (e *Engine) resolveAppTarget(ctx context.Context, sourceDisplayID int, target *protocol.AppTarget) (*device.Task, *protocol.APIError) {
	all, err := e.dev.TaskList(ctx)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	if target.UseTask {
		for i := range all {
			if all[i].TaskID != target.TaskID {
				continue
			}
			if all[i].DisplayID != sourceDisplayID {
				return nil, protocol.NewError(409, protocol.CodeTaskNotTracked,
					"该任务不在预期显示屏上")
			}
			return &all[i], nil
		}
		return nil, protocol.NewError(409, protocol.CodeTaskNotFound, "找不到该任务")
	}
	var matches []device.Task
	for _, task := range all {
		if task.DisplayID != sourceDisplayID || task.Package != target.App || task.TaskID <= 0 {
			continue
		}
		matches = append(matches, task)
	}
	sortTasks(matches)
	switch len(matches) {
	case 0:
		return nil, protocol.NewError(409, protocol.CodeTaskNotFound, "该应用没有可管理的任务")
	case 1:
		return &matches[0], nil
	default:
		ids := make([]int, 0, len(matches))
		for _, task := range matches {
			ids = append(ids, task.TaskID)
		}
		apiErr := protocol.NewError(409, protocol.CodeTaskAmbiguous,
			"包名匹配到多个任务，请用精确 task_id 选择")
		apiErr.WithDetail("task_ids", ids)
		return nil, apiErr
	}
}

// rootSingleLeaf proves that root contains exactly one leaf task, which is the
// only movable case. A shared, unknown or unreadable root is refused.
func (e *Engine) rootSingleLeaf(ctx context.Context, task device.Task) *protocol.APIError {
	if task.RootID <= 0 {
		return protocol.NewError(409, protocol.CodeTaskRootShared,
			"任务根容器不可识别，拒绝搬移")
	}
	all, err := e.dev.TaskList(ctx)
	if err != nil {
		return protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	count := 0
	for _, other := range all {
		if other.RootID == task.RootID {
			count++
		}
	}
	if count != 1 {
		return protocol.NewError(409, protocol.CodeTaskRootShared,
			"任务根容器包含多个窗口，无法安全搬移")
	}
	return nil
}

// sessionTaskLocked returns the current session registry entry of one task.
func (e *Engine) sessionTaskLocked(session *sessionState, taskID int) *taskEntry {
	if session == nil {
		return nil
	}
	for i := range session.Tasks {
		if session.Tasks[i].TaskID == taskID {
			return &session.Tasks[i]
		}
	}
	return nil
}

// appTargetView is the already-proven part of one claim/handoff/close target.
type appTargetView struct {
	task      device.Task
	entry     *taskEntry
	ownership string
	origin    *int
}
