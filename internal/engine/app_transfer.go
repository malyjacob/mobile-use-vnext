package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// transferState is one root-level task transfer responsibility. The record is
// durable; the pointer identity is not.
type transferState struct {
	record  LedgerTransfer
	durable bool
}

// transferProtectsLocked reports whether any transfer record still forbids a
// delete of this task id. A released record protects nothing.
func (e *Engine) transferProtectsLocked(taskID int) bool {
	if taskID <= 0 {
		return false
	}
	for _, transfer := range e.transfers {
		if transfer == nil || transfer.record.LeafTaskID != taskID {
			continue
		}
		if transfer.record.State != TransferReleased {
			return true
		}
	}
	return false
}

// transferBlocksReclaimLocked reports an old RemoveTask/MoveTask responsibility
// that may still be in flight for this exact task. A new claim must not proceed.
func (e *Engine) transferBlocksReclaimLocked(taskID int) bool {
	for _, transfer := range e.transfers {
		if transfer == nil || transfer.record.LeafTaskID != taskID {
			continue
		}
		switch transfer.record.State {
		case TransferMayDispatch, TransferUnknown, TransferPrepared:
			return true
		}
	}
	return false
}

// transferProtectionSnapshotLocked returns the protected task ids for one
// delete-candidate evaluation. It is read once per cleanup sweep.
func (e *Engine) transferProtectionSnapshotLocked() map[int]bool {
	protected := map[int]bool{}
	for _, transfer := range e.transfers {
		if transfer == nil || transfer.record.State == TransferReleased {
			continue
		}
		if transfer.record.LeafTaskID > 0 {
			protected[transfer.record.LeafTaskID] = true
		}
	}
	return protected
}

// retiredReferencesTaskLocked reports a retired snapshot that may still delete
// this exact task.
func (e *Engine) retiredReferencesTaskLocked(taskID int) bool {
	for _, retired := range e.retired {
		if retired == nil || retired.session == nil {
			continue
		}
		for _, task := range retired.session.Tasks {
			if task.TaskID == taskID {
				return true
			}
		}
		if retired.cleanup != nil {
			for _, id := range retired.cleanup.checkedTaskIDs {
				if id == taskID {
					return true
				}
			}
		}
	}
	return false
}

// taskDeleteReferenceLocked reports whether any record that may still delete this
// task references it. It is the conservative capacity guard for new transfers.
func (e *Engine) taskDeleteReferenceLocked(taskID int) bool {
	if e.session != nil {
		for _, task := range e.session.Tasks {
			if task.TaskID == taskID {
				return true
			}
		}
	}
	for _, retired := range e.retired {
		if retired == nil || retired.session == nil {
			continue
		}
		for _, task := range retired.session.Tasks {
			if task.TaskID == taskID {
				return true
			}
		}
		if retired.cleanup != nil {
			for _, id := range retired.cleanup.checkedTaskIDs {
				if id == taskID {
					return true
				}
			}
		}
	}
	if e.cleanup != nil {
		for _, id := range e.cleanup.checkedTaskIDs {
			if id == taskID {
				return true
			}
		}
	}
	return false
}

// releaseTransferLocked drops one transfer record once its responsibility is
// resolved. A retired session snapshot that still names the task keeps the
// record, because that snapshot keeps a delete path alive; the current session
// registry never does (a confirmed inbound claim is managed by its ownership, a
// confirmed outbound handoff left the registry).
func (e *Engine) releaseTransferLocked(transfer *transferState) {
	if transfer == nil {
		return
	}
	transfer.record.State = TransferReleased
	if e.retiredReferencesTaskLocked(transfer.record.LeafTaskID) {
		return
	}
	for i, candidate := range e.transfers {
		if candidate == transfer {
			e.transfers = append(e.transfers[:i], e.transfers[i+1:]...)
			break
		}
	}
}

// addTransferLocked installs one prepared record and returns its state handle.
func (e *Engine) addTransferLocked(record LedgerTransfer) *transferState {
	transfer := &transferState{record: record}
	e.transfers = append(e.transfers, transfer)
	return transfer
}

// setTransferStateLocked updates one record and optionally persists it.
func (e *Engine) setTransferState(transfer *transferState, state string) {
	if transfer == nil {
		return
	}
	e.mu.Lock()
	transfer.record.State = state
	e.mu.Unlock()
}

// transferIdentity resolves and caches the boot identity used by a transfer.
func (e *Engine) transferIdentity(ctx context.Context) (device.BootIdentity, *protocol.APIError) {
	identity, err := e.dev.ExecutionIdentity(ctx)
	if err != nil || !validUIIdentity(identity) {
		return device.BootIdentity{}, protocol.NewError(503,
			protocol.CodeDeviceUnavailable, "无法确认设备执行身份")
	}
	return identity, nil
}

// appClaim moves one primary task into the virtual session display and registers
// it as borrowed. Source ownership stays on the primary display: the task was
// the user's, not the session's.
func (e *Engine) appClaim(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	_, session, staticErr := e.appStaticCheckLocked(op.Agent, req.Reference, now)
	if staticErr == nil {
		staticErr = e.appModeGuardLocked(session, req.Kind)
	}
	var epoch int64
	if session != nil {
		epoch = session.Epoch
	}
	e.mu.Unlock()
	if staticErr != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), staticErr)
	}

	work, busy := e.acquireWork(op, WorkAppAction)
	if busy != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), busy)
	}
	defer e.releaseWork(work)

	task, targetErr := e.resolveAppTarget(work.Ctx, 0, req.Target)
	if targetErr != nil {
		return e.finishAppError(op, "preflight", appTargetErrorView(req, nil), targetErr)
	}
	view := appTargetErrorView(req, task)
	if task.Package == e.cfg.ControlPackage {
		return e.finishAppError(op, "preflight", view, protocol.NewError(409,
			protocol.CodeControlUI, "不能搬移控制界面"))
	}
	if rootErr := e.rootSingleLeaf(work.Ctx, *task); rootErr != nil {
		return e.finishAppError(op, "preflight", view, rootErr)
	}
	identity, identityErr := e.transferIdentity(work.Ctx)
	if identityErr != nil {
		return e.finishAppError(op, "preflight", view, identityErr)
	}
	e.mu.Lock()
	busyErr := e.claimGuardLocked(task.TaskID)
	capacityErr := e.appCapacityRefusalLocked(&LedgerTask{
		TaskID: task.TaskID, Package: task.Package, DisplayID: session.Display.ID,
		RootTaskID: task.RootID, Ownership: TaskBorrowed, OriginDisplay: appIntPtr(0),
		Proven: true,
	}, &LedgerTransfer{
		OperationID: op.ID, Direction: TransferIn, State: TransferPrepared,
		Epoch: epoch, BootID: identity.BootID, AppInstanceID: session.AppInstanceID,
		Package: task.Package, LeafTaskID: task.TaskID, RootTaskID: task.RootID,
		SourceDisplayID: 0, TargetDisplayID: session.Display.ID, CreatedAt: now,
	})
	e.mu.Unlock()
	if busyErr != nil {
		return e.finishAppError(op, "preflight", view, busyErr)
	}
	if capacityErr != nil {
		return e.finishAppError(op, "preflight", view, capacityErr)
	}

	e.mu.Lock()
	claimErr := e.claimObservationLocked(op, op.Agent, work, e.observations[req.Reference],
		e.session, time.Now())
	if claimErr == nil && e.session != nil && e.session.Epoch == epoch {
		e.session.InputIdentity = identity
	}
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishAppError(op, "claim", view, claimErr)
	}

	e.mu.Lock()
	transfer := e.addTransferLocked(LedgerTransfer{
		OperationID: op.ID, Direction: TransferIn, State: TransferPrepared,
		Epoch: epoch, BootID: identity.BootID, AppInstanceID: session.AppInstanceID,
		Package: task.Package, LeafTaskID: task.TaskID, RootTaskID: task.RootID,
		SourceDisplayID: 0, TargetDisplayID: session.Display.ID, CreatedAt: time.Now(),
	})
	e.mu.Unlock()
	if !e.persistPreparedTransfer(work, transfer) {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "reserve", view, protocol.NewError(503,
			protocol.CodeDeviceUnavailable, "恢复账本不可用，任务未搬移"))
	}

	// Revalidate the exact source task, its root and the work authority before
	// the one dispatch. A STOP or a display change ends the attempt here.
	revalidateErr := e.revalidateTransferSource(work, task, 0)
	if revalidateErr != nil {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "revalidate", view, revalidateErr)
	}
	e.setTransferState(transfer, TransferMayDispatch)
	if !e.persistTransferBoundary(work, transfer, TransferMayDispatch) {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "reserve", view, protocol.NewError(503,
			protocol.CodeDeviceUnavailable, "恢复账本不可用，任务未搬移"))
	}

	result, moveErr := e.dev.MoveTask(work.Ctx, task.RootID, task.TaskID, session.Display.ID)
	switch moveErr {
	case nil:
	default:
		if result.Receipt == device.DispatchNotDispatched {
			e.setTransferState(transfer, TransferNotDispatched)
			e.mu.Lock()
			e.releaseTransferLocked(transfer)
			e.mu.Unlock()
			return e.finishAppError(op, "dispatch", view, protocol.NewError(409,
				protocol.CodeTaskMoveUnconfirmed, "任务搬移未派发"))
		}
	}
	if result.Receipt != device.DispatchTerminated {
		e.setTransferState(transfer, TransferUnknown)
		e.persistAsync()
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindClaim, Package: task.Package,
			TaskID: appIntPtr(task.TaskID), SourceDisplayID: appIntPtr(0),
			StatePreserved: nil, HasStatePreserved: true,
		}, protocol.CodeTaskMoveUnconfirmed, "已派发任务搬移，但无法确认结果", nil)
	}

	landed, preserved := e.awaitClaimLanding(work, session, task)
	if landed == nil {
		e.setTransferState(transfer, TransferUnknown)
		e.persistAsync()
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindClaim, Package: task.Package,
			TaskID: appIntPtr(task.TaskID), SourceDisplayID: appIntPtr(0),
			StatePreserved: preserved, HasStatePreserved: true,
		}, protocol.CodeTaskMoveUnconfirmed, "任务搬移已派发，但无法确认落点", nil)
	}

	origin := 0
	e.mu.Lock()
	if e.session != nil && e.session.Epoch == epoch && e.session.State != SessionEnded {
		entry := taskEntry{
			TaskID: landed.TaskID, RootID: landed.RootID, Package: landed.Package,
			DisplayID: session.Display.ID, Generation: session.Display.Generation,
			Ownership: TaskBorrowed, OriginDisplay: &origin, Proven: true,
			CreatedAt: time.Now(),
		}
		e.upsertTaskLocked(entry)
		e.releaseTransferLocked(transfer)
	}
	e.mu.Unlock()
	e.persistAsync()

	return e.finishAppExecuted(work, op, &protocol.AppView{
		Kind: protocol.AppKindClaim, Package: task.Package,
		TaskID: appIntPtr(landed.TaskID), SourceDisplayID: appIntPtr(0),
		DisplayID: appIntPtr(session.Display.ID), Ownership: TaskBorrowed,
		StatePreserved: preserved, HasStatePreserved: true,
	})
}

// reclaimGuardLocked refuses a task whose old transfer/remove responsibility may
// still be in flight. Only that task is limited.
func (e *Engine) reclaimGuardLocked(taskID int) *protocol.APIError {
	if e.transferBlocksReclaimLocked(taskID) {
		apiErr := protocol.NewError(409, protocol.CodeTaskLifecycleBusy,
			"该任务仍有未确认的旧迁移命令")
		apiErr.WithDetail("task_ids", []int{taskID})
		return apiErr
	}
	return nil
}

// claimGuardLocked additionally refuses a task that still belongs to the current
// session or a retired snapshot: that record keeps an unresolved close/remove
// responsibility, so the task must not be claimed into the session again.
func (e *Engine) claimGuardLocked(taskID int) *protocol.APIError {
	if guardErr := e.reclaimGuardLocked(taskID); guardErr != nil {
		return guardErr
	}
	if e.taskRegisteredLocked(taskID) {
		apiErr := protocol.NewError(409, protocol.CodeTaskLifecycleBusy,
			"该任务仍属于既有会话归属")
		apiErr.WithDetail("task_ids", []int{taskID})
		return apiErr
	}
	return nil
}

// taskRegisteredLocked reports a task still owned by the current session or a
// retired snapshot. Such a task keeps an unresolved close/remove
// responsibility, so it must not be claimed into the session again.
func (e *Engine) taskRegisteredLocked(taskID int) bool {
	if taskID <= 0 {
		return false
	}
	if e.session != nil {
		for _, task := range e.session.Tasks {
			if task.TaskID == taskID {
				return true
			}
		}
	}
	for _, retired := range e.retired {
		if retired == nil || retired.session == nil {
			continue
		}
		for _, task := range retired.session.Tasks {
			if task.TaskID == taskID {
				return true
			}
		}
	}
	return false
}

// appTargetErrorView carries the selector facts already read for one failure.
func appTargetErrorView(req *protocol.AppRequest, task *device.Task) *protocol.AppView {
	view := &protocol.AppView{Kind: req.Kind}
	if req.Target == nil {
		return view
	}
	if req.Target.UseTask {
		view.TaskID = appIntPtr(req.Target.TaskID)
	}
	if task != nil {
		view.Package = task.Package
		view.TaskID = appIntPtr(task.TaskID)
		if req.Kind != protocol.AppKindOpen {
			view.SourceDisplayID = appIntPtr(task.DisplayID)
		}
	} else if !req.Target.UseTask {
		view.Package = req.Target.App
	}
	return view
}

// persistPreparedTransfer saves the prepared responsibility before any dispatch.
func (e *Engine) persistPreparedTransfer(work *workState, transfer *transferState) bool {
	if work == nil || transfer == nil {
		return false
	}
	if err := e.persistSync(work.Ctx); err != nil {
		return false
	}
	e.mu.Lock()
	transfer.durable = true
	e.mu.Unlock()
	return true
}

// persistTransferBoundary saves the final pre-dispatch boundary state.
func (e *Engine) persistTransferBoundary(work *workState, transfer *transferState, state string) bool {
	if work == nil || transfer == nil {
		return false
	}
	e.setTransferState(transfer, state)
	if err := e.persistSync(work.Ctx); err != nil {
		return false
	}
	return true
}

// revalidateTransferSource re-reads the exact source task and its root before the
// one dispatch.
func (e *Engine) revalidateTransferSource(work *workState, task *device.Task, displayID int) *protocol.APIError {
	if e.workRevoked(work) {
		return protocol.NewError(409, protocol.CodeStopped, "任务搬移已撤销")
	}
	all, err := e.dev.TaskList(work.Ctx)
	if err != nil {
		return protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	var found *device.Task
	rootLeaves := 0
	for i := range all {
		if all[i].TaskID == task.TaskID {
			found = &all[i]
		}
		if all[i].RootID == task.RootID {
			rootLeaves++
		}
	}
	if found == nil || found.DisplayID != displayID || found.Package != task.Package {
		return protocol.NewError(409, protocol.CodeTaskNotTracked, "原任务已不在预期位置")
	}
	if found.RootID != task.RootID || rootLeaves != 1 {
		return protocol.NewError(409, protocol.CodeTaskRootShared, "任务根容器已变化")
	}
	if e.workRevoked(work) {
		return protocol.NewError(409, protocol.CodeStopped, "任务搬移已撤销")
	}
	return nil
}

// awaitClaimLanding polls for the exact leaf on the session display. preserved is
// true only when the same task and top component can be compared.
func (e *Engine) awaitClaimLanding(work *workState, session *sessionState,
	before *device.Task) (*device.Task, *bool) {
	for _, delay := range []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return nil, nil
			}
			timer.Stop()
		}
		all, err := e.dev.TaskList(work.Ctx)
		if err != nil {
			continue
		}
		var landed *device.Task
		sourceStill := false
		for i := range all {
			if all[i].TaskID != before.TaskID {
				continue
			}
			if all[i].DisplayID == session.Display.ID {
				landed = &all[i]
			} else if all[i].DisplayID == 0 {
				sourceStill = true
			}
		}
		if landed == nil && !sourceStill {
			continue
		}
		if landed == nil {
			// The task is still on the source display: the move did not land.
			return nil, nil
		}
		preserved := landed.Component == before.Component && landed.Package == before.Package
		return landed, appBoolPtr(preserved)
	}
	return nil, nil
}

// upsertTaskLocked records or replaces one session task entry.
func (e *Engine) upsertTaskLocked(entry taskEntry) {
	if e.session == nil || entry.TaskID <= 0 {
		return
	}
	for i := range e.session.Tasks {
		if e.session.Tasks[i].TaskID == entry.TaskID {
			e.session.Tasks[i] = entry
			return
		}
	}
	if len(e.session.Tasks) >= 16 {
		return
	}
	e.session.Tasks = append(e.session.Tasks, entry)
}

// appHandoff moves one session-managed task back to the primary display and
// removes its delete responsibility before publishing the fact.
func (e *Engine) appHandoff(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	_, session, staticErr := e.appStaticCheckLocked(op.Agent, req.Reference, now)
	if staticErr == nil {
		staticErr = e.appModeGuardLocked(session, req.Kind)
	}
	var epoch int64
	if session != nil {
		epoch = session.Epoch
	}
	e.mu.Unlock()
	if staticErr != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), staticErr)
	}

	work, busy := e.acquireWork(op, WorkAppAction)
	if busy != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), busy)
	}
	defer e.releaseWork(work)

	task, targetErr := e.resolveAppTarget(work.Ctx, session.Display.ID, req.Target)
	if targetErr != nil {
		return e.finishAppError(op, "preflight", appTargetErrorView(req, nil), targetErr)
	}
	view := appTargetErrorView(req, task)
	e.mu.Lock()
	entry := e.sessionTaskLocked(e.session, task.TaskID)
	var ownership string
	var origin *int
	if entry != nil {
		ownership = entry.Ownership
		if entry.OriginDisplay != nil {
			origin = appIntPtr(*entry.OriginDisplay)
		}
	}
	reclaimErr := e.reclaimGuardLocked(task.TaskID)
	e.mu.Unlock()
	if entry == nil || (ownership != TaskOwned && ownership != TaskBorrowed) {
		return e.finishAppError(op, "preflight", view, protocol.NewError(409,
			protocol.CodeTaskNotTracked, "该任务不属于本会话"))
	}
	if reclaimErr != nil {
		return e.finishAppError(op, "preflight", view, reclaimErr)
	}
	if task.Package == e.cfg.ControlPackage {
		return e.finishAppError(op, "preflight", view, protocol.NewError(409,
			protocol.CodeControlUI, "不能搬移控制界面"))
	}
	if rootErr := e.rootSingleLeaf(work.Ctx, *task); rootErr != nil {
		return e.finishAppError(op, "preflight", view, rootErr)
	}
	e.mu.Lock()
	capacityErr := e.appCapacityRefusalLocked(nil, &LedgerTransfer{
		OperationID: op.ID, Direction: TransferOut, State: TransferPrepared,
		Epoch: epoch, AppInstanceID: session.AppInstanceID,
		Package: task.Package, LeafTaskID: task.TaskID, RootTaskID: task.RootID,
		SourceDisplayID: session.Display.ID, SourceGeneration: session.Display.Generation,
		TargetDisplayID: 0, CreatedAt: now,
	})
	e.mu.Unlock()
	if capacityErr != nil {
		return e.finishAppError(op, "preflight", view, capacityErr)
	}

	e.mu.Lock()
	claimErr := e.claimObservationLocked(op, op.Agent, work, e.observations[req.Reference],
		e.session, time.Now())
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishAppError(op, "claim", view, claimErr)
	}

	e.mu.Lock()
	transfer := e.addTransferLocked(LedgerTransfer{
		OperationID: op.ID, Direction: TransferOut, State: TransferPrepared,
		Epoch: epoch, AppInstanceID: session.AppInstanceID,
		Package: task.Package, LeafTaskID: task.TaskID, RootTaskID: task.RootID,
		SourceDisplayID: session.Display.ID, SourceGeneration: session.Display.Generation,
		TargetDisplayID: 0, CreatedAt: time.Now(),
	})
	e.mu.Unlock()
	if !e.persistPreparedTransfer(work, transfer) {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "reserve", view, protocol.NewError(503,
			protocol.CodeDeviceUnavailable, "恢复账本不可用，任务未搬移"))
	}
	if revalidateErr := e.revalidateTransferSource(work, task, session.Display.ID); revalidateErr != nil {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "revalidate", view, revalidateErr)
	}
	if !e.persistTransferBoundary(work, transfer, TransferMayDispatch) {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "reserve", view, protocol.NewError(503,
			protocol.CodeDeviceUnavailable, "恢复账本不可用，任务未搬移"))
	}

	result, moveErr := e.dev.MoveTask(work.Ctx, task.RootID, task.TaskID, 0)
	if moveErr != nil && result.Receipt == device.DispatchNotDispatched {
		e.setTransferState(transfer, TransferNotDispatched)
		e.mu.Lock()
		e.releaseTransferLocked(transfer)
		e.mu.Unlock()
		return e.finishAppError(op, "dispatch", view, protocol.NewError(409,
			protocol.CodeTaskMoveUnconfirmed, "任务交还未派发"))
	}
	if result.Receipt != device.DispatchTerminated {
		e.setTransferState(transfer, TransferUnknown)
		e.persistAsync()
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindHandoff, Package: task.Package,
			TaskID: appIntPtr(task.TaskID), SourceDisplayID: appIntPtr(session.Display.ID),
			StatePreserved: nil, HasStatePreserved: true,
			ForegroundRestore: handoffRestoreState(req),
		}, protocol.CodeTaskMoveUnconfirmed, "已派发任务交还，但无法确认结果", nil)
	}

	landed, preserved := e.awaitHandoffLanding(work, session, task)
	if landed == nil {
		e.setTransferState(transfer, TransferUnknown)
		e.persistAsync()
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindHandoff, Package: task.Package,
			TaskID: appIntPtr(task.TaskID), SourceDisplayID: appIntPtr(session.Display.ID),
			StatePreserved: nil, HasStatePreserved: true,
			ForegroundRestore: handoffRestoreState(req),
		}, protocol.CodeTaskMoveUnconfirmed, "任务交还已派发，但无法确认落点", nil)
	}

	e.mu.Lock()
	if e.session != nil && e.session.Epoch == epoch && e.session.State != SessionEnded {
		e.removeSessionTaskLocked(task.TaskID)
		revokeCloseEligibilityLocked(e.session, task.Package)
		e.releaseTransferLocked(transfer)
	}
	e.mu.Unlock()
	e.persistAsync()

	restore := protocol.ForegroundRestoreNotRequested
	if req.RestoreForeground {
		restore = e.restoreHandoffForeground(work, landed)
	}
	_ = origin
	return e.finishAppExecuted(work, op, &protocol.AppView{
		Kind: protocol.AppKindHandoff, Package: task.Package,
		TaskID: appIntPtr(landed.TaskID), SourceDisplayID: appIntPtr(session.Display.ID),
		DisplayID: appIntPtr(0), Ownership: TaskShared,
		StatePreserved: preserved, HasStatePreserved: true,
		ForegroundRestore: restore,
	})
}

// handoffRestoreState reports the foreground-restore fact of an unconfirmed
// handoff: a requested-but-unproven restore is unconfirmed, never not_requested.
func handoffRestoreState(req *protocol.AppRequest) string {
	if req != nil && req.RestoreForeground {
		return protocol.ForegroundRestoreUnconfirmed
	}
	return protocol.ForegroundRestoreNotRequested
}

// awaitHandoffLanding polls for the exact leaf on the primary display.
func (e *Engine) awaitHandoffLanding(work *workState, session *sessionState,
	before *device.Task) (*device.Task, *bool) {
	for _, delay := range []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return nil, nil
			}
			timer.Stop()
		}
		all, err := e.dev.TaskList(work.Ctx)
		if err != nil {
			continue
		}
		var landed *device.Task
		onSource := false
		for i := range all {
			if all[i].TaskID != before.TaskID {
				continue
			}
			if all[i].DisplayID == 0 {
				landed = &all[i]
			} else if all[i].DisplayID == session.Display.ID {
				onSource = true
			}
		}
		if landed == nil {
			if onSource {
				return nil, nil
			}
			continue
		}
		preserved := landed.Component == before.Component && landed.Package == before.Package
		return landed, appBoolPtr(preserved)
	}
	return nil, nil
}

// removeSessionTaskLocked removes one task from every current delete list.
func (e *Engine) removeSessionTaskLocked(taskID int) {
	if e.session == nil {
		return
	}
	kept := e.session.Tasks[:0]
	for _, task := range e.session.Tasks {
		if task.TaskID == taskID {
			continue
		}
		kept = append(kept, task)
	}
	e.session.Tasks = kept
	if e.cleanup != nil {
		checked := e.cleanup.checkedTaskIDs[:0]
		for _, id := range e.cleanup.checkedTaskIDs {
			if id == taskID {
				continue
			}
			checked = append(checked, id)
		}
		e.cleanup.checkedTaskIDs = checked
	}
}

// restoreHandoffForeground performs one bounded best-effort restore after the
// move converged. A user already in an unrelated foreground is never overridden.
func (e *Engine) restoreHandoffForeground(work *workState, landed *device.Task) string {
	delay := e.cfg.ForegroundRestore
	if delay <= 0 {
		delay = 2 * time.Second
	}
	timer := time.NewTimer(delay)
	select {
	case <-timer.C:
	case <-work.Ctx.Done():
		timer.Stop()
		return protocol.ForegroundRestoreUnconfirmed
	}
	timer.Stop()
	foreground, err := e.dev.Foreground(work.Ctx, 0)
	if err != nil || foreground.State == device.ForegroundUnknown {
		return protocol.ForegroundRestoreUnconfirmed
	}
	if foreground.State == device.ForegroundPresent && foreground.Package != landed.Package {
		return protocol.ForegroundRestoreUnconfirmed
	}
	if foreground.State == device.ForegroundPresent &&
		(foreground.TaskID == nil || *foreground.TaskID == landed.TaskID) {
		return protocol.ForegroundRestoreConfirmed
	}
	receipt, focusErr := e.dev.FocusTask(work.Ctx, landed.TaskID)
	if focusErr != nil || receipt != device.DispatchTerminated {
		return protocol.ForegroundRestoreUnconfirmed
	}
	if e.verifyForeground(work, 0, landed.Package, landed.TaskID) {
		return protocol.ForegroundRestoreConfirmed
	}
	return protocol.ForegroundRestoreUnconfirmed
}

// logTransfer records only the controlled transfer facts.
func (e *Engine) logTransfer(direction, state string, taskID int) {
	if e.log == nil {
		return
	}
	e.log.Event(eventlog.Info, "app.transfer",
		eventlog.Str("direction", direction), eventlog.Str("state", state),
		eventlog.Int("task_id", taskID))
}
