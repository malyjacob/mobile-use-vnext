package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// resolveRecoveryShellChain resolves the durable managed runner ownership of a
// previous instance. It only reads device evidence and issues one exact
// termination; the old command is never replayed and its output is never
// claimed. resolved=false keeps the recovery barrier closed.
func (e *Engine) resolveRecoveryShellChain(ctx context.Context) (bool, string) {
	e.mu.Lock()
	run := e.shell
	e.mu.Unlock()
	if run == nil || run.settled() {
		return true, ""
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	snapshot, err := e.dev.InspectManagedShell(inspectCtx, run.RunnerID)
	cancel()
	if err != nil {
		// No conclusion may be drawn from an unreadable runner directory.
		return false, "shell_probe_failed"
	}
	switch {
	case snapshot.HasDone:
		// The runner wrote its own end receipt; done proves the chain stopped.
		e.completeShellRecovery(run, snapshot.Done.Reason, snapshot.Done.ExitCode, snapshot.Done)
		return true, ""
	case !snapshot.HasStart:
		// The runner never recorded a start: the command provably never ran.
		e.completeShellRecovery(run, terminationNotStarted, nil, device.ShellDone{})
		return true, ""
	case run.HasStart && !sameShellIdentity(run.Start, snapshot.Start):
		// A different boot id or different PID start ticks means the recorded
		// process cannot be this one; a reused PID is not our chain.
		e.completeShellRecovery(run, terminationUnknown, nil, snapshot.Done)
		return true, ""
	case !snapshot.Alive:
		e.completeShellRecovery(run, terminationUnknown, nil, snapshot.Done)
		return true, ""
	}
	termination, termErr := e.terminateShellRun(run, terminationCancelled)
	if termErr != nil {
		return false, "shell_probe_failed"
	}
	if !termination.Confirmed {
		return false, "shell_chain_unconfirmed"
	}
	if termination.ExitCode != nil {
		e.completeShellRecovery(run, terminationExited, termination.ExitCode, snapshot.Done)
		return true, ""
	}
	e.completeShellRecovery(run, terminationCancelled, nil, snapshot.Done)
	return true, ""
}

// completeShellRecovery records the resolved old chain: the old P becomes
// finished/previous_instance (executed only when a natural exit was proven),
// the runner identity leaves the ledger and the directory is removed.
func (e *Engine) completeShellRecovery(run *shellState, termination string,
	exitCode *int, done device.ShellDone) {
	outcome := OutcomeUncertain
	var apiErr *protocol.Error
	if termination == terminationExited && exitCode != nil {
		outcome = OutcomeExecuted
	} else {
		apiErr = &protocol.Error{Code: protocol.CodeShellUnknown,
			Message: "无法确认本次命令结束状态"}
	}
	now := time.Now()
	e.mu.Lock()
	e.noteShellTerminalLocked(run)
	run.State, run.Phase = ShellConfirmed, "previous_instance"
	run.Termination, run.ExitCode = termination, exitCode
	run.ConfirmedAt = now
	if done.HasBytes {
		run.OutputBytes = done.Bytes
	}
	run.Truncated = done.Truncated
	if e.shell == run {
		e.shell = nil
	}
	if e.shellOrphan == run {
		e.shellOrphan = nil
	}
	op := e.operations[run.OperationID]
	restoredOperation := op == nil
	if op == nil {
		if len(e.operations) >= e.cfg.MaxOperations {
			e.evictOperationsLocked()
		}
		op = &operationState{ID: run.OperationID, Agent: run.Agent, Kind: "shell", CreatedAt: run.StartedAt}
		e.operations[run.OperationID] = op
	}
	if restoredOperation {
		op.State = OperationFinished
		op.Phase = "previous_instance"
		op.Outcome = outcome
		op.SessionOrigin = run.Origin
		op.FinishedAt = now
		op.Error = apiErr
	}
	e.confirmRetiredShellLocked(run)
	if e.session != nil && e.session.Epoch == run.Epoch {
		e.confirmCleanupBarrierLocked()
	}
	e.broadcastAppLocked()
	e.mu.Unlock()
	e.persistAsync()
	e.logShell("shell.previous_instance", run,
		eventlog.Str("phase", "previous_instance"),
		eventlog.Str("termination", termination))
}

// promoteOrphanShellCleanup releases a shell-caused execution uncertainty whose
// runner identity is no longer in the ledger. Durable ownership is written
// before dispatch and cleared only after the exact chain is confirmed gone, so
// an absent Shell record together with exec_reason=shell_chain is release
// evidence recorded by a previous pass; only the closure write was lost.
func (e *Engine) promoteOrphanShellCleanup(record *Ledger) {
	if record == nil || record.Cleanup == nil || record.Shell != nil || record.LegacyUI != nil || record.UI != nil && record.UI.State == "may_dispatch" {
		return
	}
	if record.Cleanup.ExecReason != "shell_chain" ||
		record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shell != nil && !e.shell.settled() {
		return
	}
	if e.cleanup != nil && e.cleanup.ExecReason == "shell_chain" &&
		e.cleanup.Execution == protocol.CleanupUnconfirmed {
		e.cleanup.Execution = protocol.CleanupConfirmed
		e.executionEvidenceRevision++
		e.cleanup.needsExecutionRecheck = e.cleanup.needsExecutionRecheck || e.session != nil ||
			record.PendingClose != nil || record.PendingCreate != nil || len(record.Tasks) > 0
		if e.cleanup.Error != nil && e.cleanup.Error.Code == "execution_unconfirmed" {
			e.cleanup.Error = nil
		}
		if closureComplete(e.cleanup) {
			e.cleanup.State = protocol.CleanupComplete
		}
		record.Cleanup.Execution = e.cleanup.Execution
		record.Cleanup.State = e.cleanup.State
		return
	}
	live := &cleanupState{
		State: record.Cleanup.State, Execution: protocol.CleanupConfirmed,
		Tasks: record.Cleanup.Tasks, Recents: record.Cleanup.Recents,
		Display: record.Cleanup.Display,
	}
	if closureComplete(live) {
		live.State = protocol.CleanupComplete
	}
	record.Cleanup.Execution, record.Cleanup.State = live.Execution, live.State
}

// sameShellIdentity compares the durable start identity with the runner copy.
// A missing boot id on either side never proves a mismatch by itself.
func sameShellIdentity(recorded, observed device.ShellStart) bool {
	if recorded.PID != observed.PID || recorded.StartTicks != observed.StartTicks {
		return false
	}
	if recorded.BootID != "" && observed.BootID != "" && recorded.BootID != observed.BootID {
		return false
	}
	return true
}
