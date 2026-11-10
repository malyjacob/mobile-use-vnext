package engine

import (
	"context"
	"regexp"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// uiDispatchState belongs to the engine, independently of the ordinary work
// slot and of the action's outcome. Every snapshot copies record by value.
type uiDispatchState struct {
	record   LedgerUI
	deadline time.Time
	durable  bool
	returned bool
	saving   bool
}

var uiBootUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUIIdentity(identity device.BootIdentity) bool {
	return len(identity.DeviceID) > 0 && len(identity.DeviceID) <= 128 && uiBootUUID.MatchString(identity.BootID)
}

func (e *Engine) uiPendingLocked() bool {
	return e.ui != nil && (e.ui.record.State == "may_dispatch" || !e.ui.durable)
}

// Durable storage is a separate gate; it cannot rewrite the action outcome.
func (e *Engine) durabilityRecoveryLocked(reference, inputState string, previous *protocol.Recovery) *protocol.Recovery {
	if !e.ledgerUnsafe && !(e.ui != nil && e.ui.record.State != "may_dispatch" && !e.ui.durable) {
		return previous
	}
	cause := ""
	if previous != nil {
		cause = previous.Cause
	}
	return newRecovery(protocol.RecoveryEntryStatus, reference, inputState, cause)
}

func (e *Engine) noteShellTerminalLocked(run *shellState) {
	if run == nil || run.settled() {
		return
	}
	e.executionEvidenceRevision++
	if e.cleanup != nil && e.session != nil && e.session.Origin == run.Origin && e.session.State == SessionEnded {
		e.cleanup.needsExecutionRecheck = true
	}
}

func (e *Engine) uiRebootRequiredLocked() bool {
	return e.legacyUI != nil && e.uiCheckpointReady ||
		e.ui != nil && e.ui.record.State == "may_dispatch" && e.ui.durable && e.ui.returned
}

// beginUIDispatch saves a may-dispatch record before competing with STOP at
// the final in-memory boundary. No disk or device wait holds the state lock.
func (e *Engine) beginUIDispatch(work *workState, kind string) *protocol.APIError {
	e.mu.Lock()
	if !e.workAuthorizedLocked(work, time.Now()) || e.uiPendingLocked() {
		e.mu.Unlock()
		return protocol.NewError(409, protocol.CodeStopped, "输入未派发：原工作已撤销或执行未确认")
	}
	identity := e.session.InputIdentity
	identityRevision := e.deviceIdentityRevision
	e.mu.Unlock()
	if !validUIIdentity(identity) {
		var err error
		identity, err = e.dev.ExecutionIdentity(work.Ctx)
		if err != nil || !validUIIdentity(identity) {
			return protocol.NewError(503, protocol.CodeDeviceUnavailable, "输入未派发：无法确认设备执行身份")
		}
	}
	e.mu.Lock()
	if !e.workAuthorizedLocked(work, time.Now()) || e.uiPendingLocked() || e.deviceIdentityRevision != identityRevision {
		e.mu.Unlock()
		return protocol.NewError(409, protocol.CodeStopped, "输入未派发：原会话已撤销")
	}
	e.session.InputIdentity = identity
	work.UIStep++
	token := &uiDispatchState{deadline: work.Deadline, record: LedgerUI{
		OperationID: work.OperationID, Origin: e.session.Origin,
		Epoch: work.SessionEpoch, Step: work.UIStep, Kind: kind,
		DisplayID: e.session.Display.ID, DisplayGeneration: e.session.Display.Generation,
		DeviceID: identity.DeviceID, BootID: identity.BootID, State: "may_dispatch",
	}}
	e.ui, work.UI = token, token
	e.session.ExecutionUnknown = true
	e.executionEvidenceRevision++
	e.mu.Unlock()
	if err := e.persistSync(work.Ctx); err != nil {
		e.recordUIReceipt(work, device.DispatchNotDispatched)
		return protocol.NewError(503, protocol.CodeDeviceUnavailable, "输入未派发：恢复账本不可用")
	}
	e.mu.Lock()
	if e.ui == token {
		token.durable = true
	}
	authorized := e.workAuthorizedLocked(work, time.Now()) && e.ui == token && e.deviceIdentityRevision == identityRevision
	if authorized {
		work.DispatchUnknown = true
	}
	e.mu.Unlock()
	if !authorized {
		e.recordUIReceipt(work, device.DispatchNotDispatched)
		return protocol.NewError(409, protocol.CodeStopped, "输入未派发：原会话已撤销")
	}
	return nil
}

// recordUIReceipt accepts termination for the original token even after STOP.
// It never changes a P result, renews authority or publishes an observation.
func (e *Engine) recordUIReceipt(work *workState, receipt device.DispatchReceipt) {
	e.mu.Lock()
	if work == nil || work.UI == nil || !e.ownsUITokenLocked(work.UI) {
		e.mu.Unlock()
		return
	}
	token := work.UI
	token.returned = true
	if receipt != device.DispatchTerminated && receipt != device.DispatchNotDispatched {
		work.DispatchUnknown = true
		e.mu.Unlock()
		return
	}
	if token.record.State == "may_dispatch" {
		token.record.State = "terminated"
		if receipt == device.DispatchNotDispatched {
			token.record.State = "not_dispatched"
		}
		token.durable = false
		e.executionEvidenceRevision++
	}
	work.DispatchUnknown = false
	e.mu.Unlock()
	e.persistUIReceipt(token)
}

func (e *Engine) persistUIReceipt(token *uiDispatchState) {
	e.mu.Lock()
	if !e.ownsUITokenLocked(token) || token.record.State == "may_dispatch" || token.durable || token.saving {
		e.mu.Unlock()
		return
	}
	token.saving = true
	for _, r := range e.retired {
		if r.ui == token {
			r.cleanup.evidenceRevision++
			r.cleanup.Execution = protocol.CleanupConfirmed
			if r.shell != nil && !r.shell.settled() {
				r.cleanup.Execution = protocol.CleanupUnconfirmed
			}
			if r.session.Mode == "virtual" {
				r.cleanup.Tasks = protocol.CleanupUnconfirmed
				r.cleanup.Recents = protocol.CleanupUnconfirmed
				r.cleanup.Display = protocol.CleanupUnconfirmed
				r.cleanup.needsExecutionRecheck = true
			}
			if r.cleanup.State != protocol.CleanupPending {
				stopCleanup(r.cleanup, "execution_boundary_changed")
			}
		}
	}
	// Persist the physical recheck obligation in the same snapshot as the
	// terminal receipt. A crash cannot leave only the old confirmed facts.
	if e.cleanup != nil && e.session != nil && e.session.Origin == token.record.Origin &&
		e.session.State == SessionEnded {
		e.cleanup.needsExecutionRecheck = true
		if e.shell == nil || e.shell.settled() {
			e.cleanup.Execution = protocol.CleanupConfirmed
			if e.cleanup.Error != nil && e.cleanup.Error.Code == "execution_unconfirmed" {
				e.cleanup.Error = nil
			}
		}
	}
	e.mu.Unlock()
	err := e.persistSync(context.Background())
	e.mu.Lock()
	token.saving = false
	if err == nil && e.ownsUITokenLocked(token) {
		token.durable = true
		if e.session != nil && e.session.Origin == token.record.Origin {
			e.session.ExecutionUnknown = e.shell != nil && !e.shell.settled()
		}
	}
	e.broadcastAppLocked()
	e.mu.Unlock()
	e.launchExecutionCleanupRecheck()
}

// launchExecutionCleanupRecheck only reads the original scope after its execution
// boundary. It never repeats an input or starts another destructive chain.
func (e *Engine) launchExecutionCleanupRecheck() {
	e.mu.Lock()
	cleanup := e.cleanup
	if e.retiredForCleanupLocked(cleanup) != nil {
		e.mu.Unlock()
		return
	}
	if cleanup == nil || !cleanup.needsExecutionRecheck || cleanup.State == protocol.CleanupPending ||
		e.uiReconciling || e.uiPendingLocked() || e.work != nil || e.shuttingDown || e.recovery != nil ||
		e.session == nil || e.session.State != SessionEnded ||
		e.shell != nil && !e.shell.settled() {
		e.mu.Unlock()
		return
	}
	target := targetOf(e.session)
	revision := e.executionEvidenceRevision
	ids := append([]int(nil), cleanup.checkedTaskIDs...)
	for _, task := range target.tasks {
		ids = append(ids, task.TaskID)
	}
	e.uiReconciling = true
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), e.cfg.CleanupTimeout)
		defer cancel()
		tasks, recents, display := e.verifyClosedExecutionTarget(ctx, target, ids)
		e.mu.Lock()
		e.uiReconciling = false
		if e.cleanup != cleanup || e.session == nil || e.session.Epoch != target.epoch ||
			e.executionEvidenceRevision != revision || e.uiPendingLocked() || e.shuttingDown ||
			!e.appFreshLocked(time.Now()) || e.app.Display != nil {
			e.mu.Unlock()
			return
		}
		result := *cleanup
		result.Tasks, result.Recents, result.Display = tasks, recents, display
		result.evidenceRevision, result.needsExecutionRecheck = revision, false
		e.mu.Unlock()
		e.finishCleanup(target, cleanup, result)
	}()
}

func (e *Engine) verifyClosedExecutionTarget(ctx context.Context, target cleanupTarget, ids []int) (string, string, string) {
	if target.mode != "virtual" {
		return protocol.CleanupNotApplicable, protocol.CleanupNotApplicable, protocol.CleanupNotApplicable
	}
	tasks, recents, display := protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed
	if target.display.ID <= 0 {
		return tasks, recents, display
	}
	if live, err := e.tasksOnDisplayByTaskList(ctx, target.display.ID); err == nil && len(live) == 0 {
		tasks = protocol.CleanupConfirmed
	}
	if recent, known := e.readCleanupRecents(ctx); known {
		recents = protocol.CleanupConfirmed
		for _, id := range ids {
			if recent[id] {
				recents = protocol.CleanupUnconfirmed
				break
			}
		}
	}
	if absent, known := e.displayConfirmedAbsent(ctx, target.display.ID); known && absent {
		display = protocol.CleanupConfirmed
	}
	return tasks, recents, display
}

// A boot boundary proves termination only. All physical checks remain read-only
// because old numerical IDs may now name somebody else's resources.
func (e *Engine) finishExecutionRecovery(ctx context.Context, record *Ledger, app appState) {
	e.mu.Lock()
	if e.recovery != record {
		e.mu.Unlock()
		e.setRecoveryReason("old_resource_unconfirmed")
		return
	}
	target := cleanupTarget{mode: "primary"}
	switch {
	case e.session != nil:
		target = targetOf(e.session)
	case record.PendingClose != nil:
		target.mode = "virtual"
		target.display = device.Display{ID: record.PendingClose.DisplayID, Generation: record.PendingClose.DisplayGeneration}
	case record.Shell != nil:
		target.display = device.Display{ID: record.Shell.DisplayID, Generation: record.Shell.DisplayGeneration}
		if record.Shell.DisplayID > 0 {
			target.mode = "virtual"
		}
	case record.PendingCreate != nil || len(record.Tasks) > 0:
		e.mu.Unlock()
		e.setRecoveryReason("old_resource_unconfirmed")
		return
	}
	ids := []int{}
	if record.Cleanup != nil {
		ids = append(ids, record.Cleanup.CheckedTaskIDs...)
	}
	for _, task := range record.Tasks {
		ids = append(ids, task.TaskID)
	}
	e.mu.Unlock()
	queryCtx, cancel := context.WithTimeout(ctx, e.cfg.CleanupTimeout)
	defer cancel()
	tasks, recents, display := e.verifyClosedExecutionTarget(queryCtx, target, ids)
	if target.mode == "virtual" && len(ids) == 0 && record.Cleanup != nil && unresolvedCleanupFact(record.Cleanup.Recents) {
		recents = protocol.CleanupUnconfirmed
	}
	if unresolvedCleanupFact(tasks) || unresolvedCleanupFact(recents) || unresolvedCleanupFact(display) {
		e.setRecoveryReason("old_resource_unconfirmed")
		return
	}
	e.mu.Lock()
	ready, _ := e.appReadyLocked(time.Now())
	if e.recovery != record || !ready || e.shuttingDown || e.app.InstanceID != app.InstanceID ||
		e.app.ModeGeneration != app.ModeGeneration || e.app.Display != nil {
		e.mu.Unlock()
		e.setRecoveryReason("control_sync_pending")
		return
	}
	closure := LedgerCleanup{Execution: protocol.CleanupConfirmed}
	if record.Cleanup != nil {
		closure = *record.Cleanup
		closure.CheckedTaskIDs = append([]int(nil), record.Cleanup.CheckedTaskIDs...)
	}
	closure.Execution = protocol.CleanupConfirmed
	closure.Tasks, closure.Recents, closure.Display = tasks, recents, display
	closure.ExecutionRecheckRequired = false
	record.Cleanup = &closure
	e.mu.Unlock()
	e.finishRecovery(record, app, target.display.ID, "old_resources_resolved")
}
