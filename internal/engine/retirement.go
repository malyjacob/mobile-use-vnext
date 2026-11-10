package engine

import (
	"context"
	"encoding/json/v2"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const retirementHandover = 5 * time.Second

// retiredState owns only the original closure. It cannot authorize ordinary
// work. Pointers are stable so a late result cannot fall back to a new owner.
type retiredState struct {
	session       *sessionState
	cleanup       *cleanupState
	ui            *uiDispatchState
	shell         *shellState
	legacy        *LedgerUICheckpoint
	create        *LedgerCommand
	service       string
	budgetIssued  bool
	attempts      int
	stoppedReason string
	cancel        context.CancelFunc
}

// WithProjectorFactory gives every new session its own continuity state.
func WithProjectorFactory(factory func() Projector) Option {
	return func(e *Engine) { e.projectorFactory = factory }
}

func closedOutcome(known bool) string {
	if known {
		return OutcomeNotExecuted
	}
	return OutcomeUncertain
}

func (e *Engine) retiredForCleanupLocked(cleanup *cleanupState) *retiredState {
	for _, r := range e.retired {
		if r.cleanup == cleanup {
			return r
		}
	}
	return nil
}

func (e *Engine) cleanupForSessionLocked(session *sessionState) *cleanupState {
	if session == nil {
		return nil
	}
	for _, r := range e.retired {
		if r.session == session || r.session.Origin == session.Origin {
			return r.cleanup
		}
	}
	if e.session == session || e.lastSession == session {
		return e.cleanup
	}
	for _, op := range e.operations {
		if op.SessionOrigin == session.Origin && op.CleanupSnapshot != nil {
			c := op.CleanupSnapshot
			return &cleanupState{State: c.State, Execution: c.Execution, Tasks: c.Tasks, Recents: c.Recents, Display: c.Display, Error: c.Error}
		}
	}
	return nil
}

func (e *Engine) cleanupForOriginLocked(origin string) *cleanupState {
	return e.cleanupForSessionLocked(e.sessionByOriginLocked(origin))
}

func cleanupViewOf(c *cleanupState) *protocol.Cleanup {
	if c == nil {
		return nil
	}
	v := &protocol.Cleanup{State: c.State, Execution: c.Execution, Tasks: c.Tasks, Recents: c.Recents, Display: c.Display}
	if c.Error != nil {
		err := *c.Error
		v.Error = &err
	}
	return v
}

func (e *Engine) retireLocked(session *sessionState, cleanup *cleanupState) *retiredState {
	if r := e.retiredForCleanupLocked(cleanup); r != nil {
		return r
	}
	r := &retiredState{session: session, cleanup: cleanup, ui: e.ui, shell: e.shell, legacy: e.legacyUI,
		service: e.cfg.ServiceInstanceID}
	if session.CreateCommand != "" {
		r.create = &LedgerCommand{ID: session.CreateCommand, Kind: "create_display", AppInstanceID: session.AppInstanceID, RequestedAt: session.StartedAt}
	}
	e.retired = append(e.retired, r)
	e.trimRetiredLocked()
	e.detachRetiredLocked(time.Now())
	return r
}

// detachRetiredLocked never waits for physical completion. The clock bounds a
// cancelled handler even when it ignores its context. Its eventual defer uses
// pointer equality and therefore cannot release a newer work slot.
func (e *Engine) detachRetiredLocked(now time.Time) {
	if e.work != nil && e.work.Revoked && !e.work.RevokedAt.IsZero() && !now.Before(e.work.RevokedAt.Add(retirementHandover)) {
		e.freezeWorkLocked(e.work)
		e.work.Cancel()
		if e.log != nil {
			e.log.Event(eventlog.Info, "retirement.detached", eventlog.Str("operation_id", e.work.OperationID))
		}
		e.work = nil
	}
	for _, r := range e.retired {
		if e.work != nil && e.work.SessionEpoch == r.session.Epoch {
			if now.Before(r.session.EndedAt.Add(retirementHandover)) {
				continue
			}
			e.freezeWorkLocked(e.work)
			e.work.Cancel()
			e.work = nil
			if e.log != nil {
				e.log.Event(eventlog.Info, "retirement.detached", eventlog.Str("origin", r.session.Origin))
			}
		}
		if e.ui == r.ui {
			e.ui = nil
		}
		if e.shell == r.shell {
			e.shell = nil
		}
		if e.legacyUI == r.legacy {
			e.legacyUI = nil
			e.uiCheckpointReady = false
		}
	}
}

func (e *Engine) freezeWorkLocked(work *workState) {
	if work == nil {
		return
	}
	work.Revoked = true
	if op := e.operations[work.OperationID]; op != nil && op.State == OperationRunning {
		op.State, op.Phase, op.FinishedAt = OperationFinished, "work_deadline", time.Now()
		op.Outcome = OutcomeNotExecuted
		if work.DispatchUnknown || work.UI != nil && work.UI.record.State != "not_dispatched" {
			op.Outcome = OutcomeUncertain
		}
		if op.KnownOutcome != "" {
			op.Outcome = op.KnownOutcome
		}
		op.Error = &protocol.Error{Code: protocol.CodeStopped, Message: "原工作已撤销"}
	}
}

func ledgerSessionOf(s *sessionState) *LedgerSession {
	if s == nil {
		return nil
	}
	return &LedgerSession{Origin: s.Origin, Agent: s.Agent, Mode: s.Mode, State: s.State, AppInstanceID: s.AppInstanceID,
		ModeGeneration: s.ModeGeneration, DisplayID: s.Display.ID, SFID: s.Display.SFID, DisplayGeneration: s.Display.Generation,
		Width: s.Display.Width, Height: s.Display.Height, Density: s.Display.Density, Rotation: s.Display.Rotation, StartedAt: s.StartedAt,
		CreateCommand: s.CreateCommand, DeviceID: s.InputIdentity.DeviceID, BootID: s.InputIdentity.BootID}
}

func ledgerCleanupOf(c *cleanupState) *LedgerCleanup {
	if c == nil {
		return nil
	}
	return &LedgerCleanup{State: c.State, Execution: c.Execution, Tasks: c.Tasks, Recents: c.Recents, Display: c.Display,
		Reason: c.Reason, ExecReason: c.ExecReason, StartedAt: c.StartedAt, CheckedTaskIDs: append([]int(nil), c.checkedTaskIDs...),
		ExecutionRecheckRequired: c.needsExecutionRecheck}
}

func (r *retiredState) ledger() LedgerRetired {
	record := &Ledger{Format: ledgerFormat, Version: ledgerVersion, ServiceInstanceID: r.service, UpdatedAt: r.cleanup.StartedAt,
		AppInstanceID: r.session.AppInstanceID, Session: ledgerSessionOf(r.session), Cleanup: ledgerCleanupOf(r.cleanup),
		Tasks: ledgerTasksOf(r.session.Tasks), Shell: r.shell.ledger()}
	if r.ui != nil {
		v := r.ui.record
		record.UI = &v
	}
	if r.legacy != nil {
		v := *r.legacy
		record.LegacyUI = &v
	}
	if r.create != nil {
		v := *r.create
		record.PendingCreate = &v
	}
	return LedgerRetired{Record: record, BudgetIssued: r.budgetIssued, Attempts: r.attempts, StoppedReason: r.stoppedReason}
}

func (e *Engine) trimRetiredLocked() {
	for len(e.retired) > 0 {
		items := make([]LedgerRetired, 0, len(e.retired))
		for _, r := range e.retired {
			items = append(items, r.ledger())
		}
		encoded, _ := json.Marshal(items)
		if len(items) <= 8 && len(encoded) <= 24*1024 {
			return
		}
		index := e.retirementDropIndexLocked()
		r := e.retired[index]
		e.retired = append(e.retired[:index], e.retired[index+1:]...)
		if r.cleanup.State != protocol.CleanupComplete && e.droppedUnconfirmed != ^uint64(0) {
			e.droppedUnconfirmed++
		}
		if r.cancel != nil {
			r.cancel()
		}
		stopCleanup(r.cleanup, "cleanup_capacity_exhausted")
		for _, op := range e.operations {
			if op.SessionOrigin == r.session.Origin {
				op.SessionSnapshot = e.sessionViewLocked(r.session)
				op.CleanupSnapshot = cleanupViewOf(r.cleanup)
			}
		}
		for _, obs := range e.observations {
			if obs.SessionEpoch == r.session.Epoch {
				obs.SessionSnapshot = e.sessionViewLocked(r.session)
				obs.CleanupSnapshot = cleanupViewOf(r.cleanup)
			}
		}
		e.commands = filterCommands(e.commands, targetOf(r.session))
		if e.lastSession == r.session {
			e.lastSession = nil
		}
		if e.session == r.session && r.session.State == SessionEnded {
			e.session = nil
			e.cleanup = nil
		}
		if e.log != nil {
			e.log.Event(eventlog.Warn, "retirement.dropped", eventlog.Str("origin", r.session.Origin))
		}
	}
}

func stopCleanup(c *cleanupState, code string) {
	if c.State == protocol.CleanupComplete {
		return
	}
	c.State = protocol.CleanupUnconfirmed
	for _, f := range []*string{&c.Execution, &c.Tasks, &c.Recents, &c.Display} {
		if *f == protocol.CleanupPending {
			*f = protocol.CleanupUnconfirmed
		}
	}
	if c.Error == nil {
		c.Error = &protocol.Error{Code: code, Message: "原会话自动清理已停止，未确认事实保留"}
	}
	if c.done != nil {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	}
}

// recoveryRetire converts old authority into evidence. Version 3 pending
// retired entries never acquire a second cleanup budget after process restart.
func (e *Engine) recoveryRetire(ctx context.Context, record *Ledger) {
	e.mu.Lock()
	if e.recovery != record {
		e.mu.Unlock()
		return
	}
	e.recovery = nil
	e.commands = nil
	var launch []*retiredState
	restore := func(old *Ledger, issued bool, attempts int, reason string) {
		if old == nil {
			return
		}
		old = CloneLedger(old)
		if old.Session == nil {
			origin, agent, mode, owner, generation, display := "", e.cfg.AgentName, "primary", old.AppInstanceID, "", 0
			if old.UI != nil {
				origin = old.UI.Origin
				display = old.UI.DisplayID
				generation = old.UI.DisplayGeneration
			}
			if old.Shell != nil {
				origin = old.Shell.Origin
				agent = old.Shell.Agent
				display = old.Shell.DisplayID
				generation = old.Shell.DisplayGeneration
				owner = old.Shell.AppInstanceID
			}
			if old.PendingClose != nil {
				display = old.PendingClose.DisplayID
				generation = old.PendingClose.DisplayGeneration
				owner = old.PendingClose.AppInstanceID
			}
			if old.PendingCreate != nil {
				mode = "virtual"
				owner = old.PendingCreate.AppInstanceID
				if origin == "" {
					origin = "P" + old.PendingCreate.ID
				}
			}
			if display > 0 {
				mode = "virtual"
			}
			if origin == "" {
				if old.Cleanup == nil && old.PendingClose == nil {
					return
				}
				origin = "P" + NewUUID()
			}
			old.Session = &LedgerSession{Origin: origin, Agent: agent, Mode: mode, State: SessionEnded, AppInstanceID: owner, DisplayID: display, DisplayGeneration: generation, StartedAt: time.Now()}
		}
		e.session, e.cleanup, e.ui, e.shell, e.legacyUI = nil, nil, nil, nil, nil
		e.restoreRecoveryLocked(old)
		s := e.session
		if s == nil {
			return
		}
		s.CreateCommand = old.Session.CreateCommand
		if s.CreateCommand == "" && old.PendingCreate != nil {
			s.CreateCommand = old.PendingCreate.ID
		}
		s.InputIdentity = device.BootIdentity{DeviceID: old.Session.DeviceID, BootID: old.Session.BootID}
		if !validUIIdentity(s.InputIdentity) && old.UI != nil {
			s.InputIdentity = device.BootIdentity{DeviceID: old.UI.DeviceID, BootID: old.UI.BootID}
		}
		c := e.cleanup
		if c == nil {
			c = &cleanupState{State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupUnconfirmed, Tasks: protocol.CleanupUnconfirmed, Recents: protocol.CleanupUnconfirmed, Display: protocol.CleanupUnconfirmed, StartedAt: time.Now(), Reason: "service_restarted"}
			e.cleanup = c
		}
		if c.Execution == protocol.CleanupUnconfirmed && c.ExecReason == "shell_chain" && old.Shell == nil && old.LegacyUI == nil && !old.LegacyUIPending && (old.UI == nil || old.UI.State != "may_dispatch") {
			c.Execution = protocol.CleanupConfirmed
			c.needsExecutionRecheck = true
		}
		c.done = make(chan struct{})
		r := e.retireLocked(s, c)
		r.service = old.ServiceInstanceID
		r.budgetIssued = issued
		r.attempts = attempts
		r.stoppedReason = reason
		if closureComplete(c) {
			c.State = protocol.CleanupComplete
			r.budgetIssued = true
			r.stoppedReason = "complete"
			return
		}
		if issued {
			stopCleanup(c, "cleanup_restart_stopped")
			r.stoppedReason = "cleanup_restart_stopped"
		} else {
			c.StartedAt = time.Now()
			c.State = protocol.CleanupPending
			r.budgetIssued = true
			launch = append(launch, r)
		}
	}
	for _, item := range record.Retired {
		restore(item.Record, true, item.Attempts, item.StoppedReason)
	}
	restore(record, false, 0, "")
	// Legacy ledgers without a session still retain their uncertainty, but have
	// no identity that could safely authorize destructive compensation.
	e.ui, e.shell, e.legacyUI = nil, nil, nil
	e.uiCheckpointReady = false
	e.commands = nil
	e.recovering = false
	e.recoveryReason = ""
	e.trimRetiredLocked()
	e.broadcastAppLocked()
	e.mu.Unlock()
	if err := e.persistSync(ctx); err != nil {
		return
	}
	for _, r := range launch {
		e.runCleanupChain(targetOf(r.session), r.cleanup)
	}
}

func (e *Engine) retryLedgerSave() {
	e.mu.Lock()
	retry := e.ledgerUnsafe && !e.shuttingDown
	e.mu.Unlock()
	if retry {
		e.persistAsync()
	}
}

func (e *Engine) ownsUITokenLocked(token *uiDispatchState) bool {
	if e.ui == token {
		return true
	}
	for _, r := range e.retired {
		if r.ui == token {
			return true
		}
	}
	return false
}

func (e *Engine) confirmRetiredShellLocked(run *shellState) {
	for _, r := range e.retired {
		if r.shell == run && run.settled() {
			r.cleanup.evidenceRevision++
			r.cleanup.Execution = protocol.CleanupConfirmed
			if r.ui != nil && r.ui.record.State == "may_dispatch" {
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
}

func (e *Engine) noteKnownOutcome(op *operationState, outcome string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if op != nil && op.State == OperationRunning {
		op.KnownOutcome = outcome
	}
}

// A detached handler may return later, but its response cannot contradict the
// outcome frozen by the clock or attach a later session's observation.
func (e *Engine) applyFrozenResult(op *operationState, result **protocol.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if op == nil || op.Phase != "work_deadline" || *result == nil {
		return
	}
	v := e.operationViewLocked(op)
	v.Observation = nil
	*result = v
}

// resolveRetiredExecution consumes exact original end evidence without replay.
func (e *Engine) resolveRetiredExecution(ctx context.Context, r *retiredState) {
	e.mu.Lock()
	run := r.shell
	unresolved := run != nil && !run.settled()
	e.mu.Unlock()
	if unresolved {
		inspectCtx, inspectCancel := context.WithTimeout(ctx, 2*time.Second)
		snapshot, err := e.dev.InspectManagedShell(inspectCtx, run.RunnerID)
		inspectCancel()
		if err == nil {
			switch {
			case snapshot.HasDone:
				e.completeShellRecovery(run, snapshot.Done.Reason, snapshot.Done.ExitCode, snapshot.Done)
			case !snapshot.HasStart:
				e.completeShellRecovery(run, terminationNotStarted, nil, device.ShellDone{})
			case run.HasStart && !sameShellIdentity(run.Start, snapshot.Start), !snapshot.Alive:
				e.completeShellRecovery(run, terminationUnknown, nil, device.ShellDone{})
			default:
				stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
				termination, err := e.dev.TerminateManagedShell(stopCtx, run.RunnerID, run.Root, terminationCancelled)
				stopCancel()
				if err == nil && termination.Confirmed {
					reason := terminationCancelled
					if termination.ExitCode != nil {
						reason = terminationExited
					}
					e.completeShellRecovery(run, reason, termination.ExitCode, device.ShellDone{})
				}
			}
		}
	}
	if unresolved {
		e.mu.Lock()
		settled := run.settled()
		e.mu.Unlock()
		if settled {
			removeCtx, removeCancel := context.WithTimeout(ctx, 2*time.Second)
			_ = e.dev.RemoveManagedShell(removeCtx, run.RunnerID, run.Root)
			removeCancel()
		}
	}
	identity, err := e.cleanupIdentity(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.retiredForCleanupLocked(r.cleanup) != r {
		return
	}
	if err == nil && validUIIdentity(identity) && identity.DeviceID == r.session.InputIdentity.DeviceID && identity.BootID != r.session.InputIdentity.BootID {
		if r.cleanup.Execution != protocol.CleanupConfirmed {
			r.cleanup.Execution = protocol.CleanupConfirmed
			r.cleanup.evidenceRevision++
			r.cleanup.needsExecutionRecheck = true
			if r.session.Mode == "virtual" {
				r.cleanup.Tasks, r.cleanup.Recents, r.cleanup.Display = protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed
			}
		}
		// The boot boundary is end evidence, not an original RPC receipt.
		// Keep may_dispatch and the original identity intact in the ledger.
	}
	if r.ui != nil && r.ui.record.State != "may_dispatch" && r.legacy == nil && (r.shell == nil || r.shell.settled()) {
		r.cleanup.Execution = protocol.CleanupConfirmed
	}
}

func (e *Engine) retirementDropIndexLocked() int {
	for _, state := range []string{protocol.CleanupComplete, protocol.CleanupUnconfirmed, protocol.CleanupPending} {
		index := -1
		for i, r := range e.retired {
			if r.cleanup.State == state && (index < 0 || r.cleanup.StartedAt.Before(e.retired[index].cleanup.StartedAt)) {
				index = i
			}
		}
		if index >= 0 {
			return index
		}
	}
	return 0
}
func cloneCleanupSnapshot(snapshot *protocol.Cleanup) *protocol.Cleanup {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	if snapshot.Error != nil {
		err := *snapshot.Error
		copy.Error = &err
	}
	return &copy
}

func sessionFromSnapshot(v *protocol.Session, epoch int64) *sessionState {
	started, _ := time.Parse(time.RFC3339Nano, v.StartedAt)
	ended, _ := time.Parse(time.RFC3339Nano, v.EndedAt)
	return &sessionState{Origin: v.Origin, Agent: v.Agent, Mode: v.Mode, State: SessionEnded, Epoch: epoch, StartedAt: started, EndedAt: ended, Reason: v.Reason, CleanupStarted: true}
}
