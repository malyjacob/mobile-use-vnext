package engine

import (
	"context"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// restoreUIRecoveryLocked restores evidence without sharing mutable ledger
// fields with the recovery record. The caller holds e.mu.
func (e *Engine) restoreUIRecoveryLocked(record *Ledger) {
	e.ui = nil
	e.legacyUI = nil
	e.uiCheckpointReady = false
	if record == nil {
		return
	}
	if record.UI != nil {
		ui := *record.UI
		e.ui = &uiDispatchState{record: ui, durable: true}
	}
	if record.LegacyUI != nil {
		checkpoint := *record.LegacyUI
		e.legacyUI = &checkpoint
		e.uiCheckpointReady = true
	}
	e.executionEvidenceRevision++
}

// resolveRecoveryUI resolves one old UI execution responsibility without
// replaying input. A changed boot on the same stable device proves that the
// old Android-user call is gone; it does not prove that old displays, tasks or
// recents are safe to address, so bootChanged is returned separately.
func (e *Engine) resolveRecoveryUI(ctx context.Context, record *Ledger) (resolved bool, bootChanged bool, reason string) {
	if record == nil {
		return true, false, ""
	}

	e.mu.Lock()
	if e.recovery != record || e.shuttingDown || e.uiReconciling {
		e.mu.Unlock()
		return false, false, "recovery_pending"
	}
	legacyOrigin, legacyPending := legacyUIResponsibility(record)
	if e.ui == nil && e.legacyUI == nil && !legacyPending {
		e.mu.Unlock()
		return true, false, ""
	}
	revision := e.executionEvidenceRevision
	e.uiReconciling = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.uiReconciling = false
		e.mu.Unlock()
	}()

	identity, err := e.dev.ExecutionIdentity(ctx)
	if err != nil || !validUIIdentity(identity) {
		return false, false, "ui_input_unconfirmed"
	}

	e.mu.Lock()
	if e.recovery != record || e.shuttingDown || e.executionEvidenceRevision != revision {
		e.mu.Unlock()
		return false, false, "recovery_pending"
	}

	var ui *LedgerUI
	if e.ui != nil {
		snapshot := e.ui.record
		ui = &snapshot
	}
	var checkpoint *LedgerUICheckpoint
	if e.legacyUI != nil {
		snapshot := *e.legacyUI
		checkpoint = &snapshot
	}
	legacyOrigin, legacyPending = legacyUIResponsibility(record)
	if legacyPending && checkpoint == nil {
		if legacyOrigin == "" {
			e.mu.Unlock()
			return false, false, "ui_input_unconfirmed"
		}
		checkpoint = &LedgerUICheckpoint{
			Origin: legacyOrigin, DeviceID: identity.DeviceID, BootID: identity.BootID,
		}
		// Keep the first observed boundary across failed saves and retries.
		// It becomes durable only after the store accepts the candidate.
		pending := *checkpoint
		e.legacyUI = &pending
	}

	uiMayDispatch := ui != nil && uiStateMayDispatch(ui.State)
	if ui != nil {
		changed, sameDevice := uiIdentityBoundary(identity, ui.DeviceID, ui.BootID)
		if !sameDevice {
			e.mu.Unlock()
			return false, false, "ui_input_unconfirmed"
		}
		bootChanged = bootChanged || changed
	}
	if checkpoint != nil {
		changed, sameDevice := uiIdentityBoundary(identity, checkpoint.DeviceID, checkpoint.BootID)
		if !sameDevice {
			e.mu.Unlock()
			return false, false, "ui_input_unconfirmed"
		}
		bootChanged = bootChanged || changed
	}
	checkpointReady := e.uiCheckpointReady

	candidate := cloneUIRecoveryLedger(record)
	candidate.Version = ledgerVersion
	if legacyPending {
		candidate.LegacyUIPending = true
	}
	if ui != nil {
		snapshot := *ui
		candidate.UI = &snapshot
	}
	if checkpoint != nil {
		snapshot := *checkpoint
		candidate.LegacyUI = &snapshot
	}

	uiNeedsPersistence := ui != nil && !e.ui.durable
	uiCompleted := ui != nil && !uiMayDispatch
	uiSourceResolved := ui == nil || bootChanged || uiCompleted
	legacySourceResolved := checkpoint == nil || bootChanged
	uiSourcePresent := ui != nil || checkpoint != nil
	releaseUIExecution := uiSourcePresent && uiSourceResolved && legacySourceResolved
	shellPending := e.shell != nil && !e.shell.settled()
	needsSave := false
	if releaseUIExecution && !shellPending && uiExecutionNeedsPromotion(candidate) {
		candidate.Cleanup.Execution = protocol.CleanupConfirmed
		needsSave = true
	}
	// A terminal receipt and a checkpoint observed during a failed save must
	// themselves become durable before they can release their responsibility.
	needsSave = needsSave || uiNeedsPersistence
	needsSave = needsSave || (checkpoint != nil && !checkpointReady)
	e.mu.Unlock()

	if needsSave {
		committed, saveReason := e.saveUIRecoveryCandidate(ctx, record, candidate, revision)
		if !committed {
			return false, false, saveReason
		}
	}
	if bootChanged {
		return true, true, ""
	}
	if uiMayDispatch || checkpoint != nil {
		return false, false, "ui_input_reboot_required"
	}
	return true, false, ""
}

// saveUIRecoveryCandidate makes a private candidate durable before publishing
// it into the recovery record. Ownership and shutdown are checked before and
// after the only I/O operation.
func (e *Engine) saveUIRecoveryCandidate(ctx context.Context, record *Ledger, candidate *Ledger, revision uint64) (bool, string) {
	e.persistMu.Lock()
	defer e.persistMu.Unlock()

	e.mu.Lock()
	owned := e.recovery == record && !e.shuttingDown && e.executionEvidenceRevision == revision
	if !owned {
		e.mu.Unlock()
		return false, "recovery_pending"
	}
	// Snapshot live shell/command/cleanup progress under the short state lock,
	// then overlay only the candidate fields this recovery step is committing.
	snapshot := cloneUIRecoveryLedger(e.ledgerRecordLocked())
	candidateSnapshot := cloneUIRecoveryLedger(candidate)
	snapshot.Format = ledgerFormat
	snapshot.Version = ledgerVersion
	snapshot.LegacyUIPending = snapshot.LegacyUIPending || candidateSnapshot.LegacyUIPending
	snapshot.UI = candidateSnapshot.UI
	snapshot.LegacyUI = candidateSnapshot.LegacyUI
	if candidate.Cleanup != nil {
		if snapshot.Cleanup == nil {
			cleanup := *candidate.Cleanup
			cleanup.CheckedTaskIDs = append([]int(nil), candidate.Cleanup.CheckedTaskIDs...)
			snapshot.Cleanup = &cleanup
		} else {
			snapshot.Cleanup.Execution = candidate.Cleanup.Execution
		}
	}
	e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		e.mu.Lock()
		e.ledgerUnsafe = true
		e.mu.Unlock()
		return false, "ledger_unavailable"
	}
	saveCtx, cancel := context.WithTimeout(ctx, e.cfg.LedgerTimeout)
	_, saveErr := e.store.Save(saveCtx, snapshot)
	cancel()
	if saveErr != nil {
		e.mu.Lock()
		e.ledgerUnsafe = true
		e.mu.Unlock()
		if e.log != nil {
			e.log.Event(eventlog.Error, "ledger.save_failed", eventlog.Str("code", "ledger_unavailable"))
		}
		return false, "ledger_unavailable"
	}

	e.mu.Lock()
	if e.recovery != record || e.shuttingDown || e.executionEvidenceRevision != revision {
		e.ledgerUnsafe = false
		e.mu.Unlock()
		return false, "recovery_pending"
	}
	e.commitUIRecoveryCandidateLocked(record, snapshot)
	e.ledgerUnsafe = false
	e.mu.Unlock()
	return true, ""
}

// commitUIRecoveryCandidateLocked publishes a saved candidate. The caller
// holds e.mu. Only the UI execution fact is copied into the live cleanup.
func (e *Engine) commitUIRecoveryCandidateLocked(record, candidate *Ledger) {
	snapshot := cloneUIRecoveryLedger(candidate)
	*record = *snapshot
	if snapshot.UI == nil {
		e.ui = nil
	} else {
		returned, saving := false, false
		if e.ui != nil {
			returned, saving = e.ui.returned, e.ui.saving
		}
		ui := *snapshot.UI
		e.ui = &uiDispatchState{record: ui, durable: true, returned: returned, saving: saving}
	}
	if snapshot.LegacyUI == nil {
		e.legacyUI = nil
	} else {
		checkpoint := *snapshot.LegacyUI
		e.legacyUI = &checkpoint
	}
	e.uiCheckpointReady = snapshot.LegacyUI != nil
	if e.cleanup != nil && snapshot.Cleanup != nil {
		e.cleanup.Execution = snapshot.Cleanup.Execution
	}
	e.executionEvidenceRevision++
}

// legacyUIResponsibility identifies version 1-era evidence and version 2
// snapshots carrying its explicit migration marker. It never treats an absent
// version 2 UI field as proof that the old input did not occur.
func legacyUIResponsibility(record *Ledger) (origin string, pending bool) {
	if record == nil {
		return "", false
	}
	// Store.Save upgrades the serialized envelope to version 2. An explicit
	// pending marker keeps the v1 migration need observable across that save,
	// service restart, and an ordinary synchronization.
	if record.Version >= 2 && !record.LegacyUIPending {
		return "", false
	}
	if cleanup := record.Cleanup; cleanup != nil &&
		cleanup.ExecReason == "ui_input" && cleanupExecutionUnsettled(cleanup.Execution) {
		if record.Session != nil {
			return record.Session.Origin, true
		}
		return "", true
	}
	if record.Session != nil && record.Session.State != SessionEnded {
		return record.Session.Origin, true
	}
	return "", false
}

func cleanupExecutionUnsettled(state string) bool {
	switch state {
	case protocol.CleanupPending, protocol.CleanupUnconfirmed, "":
		return true
	default:
		return false
	}
}

func uiStateMayDispatch(state string) bool {
	return state != "terminated" && state != "not_dispatched"
}

func uiIdentityBoundary(current device.BootIdentity, deviceID, bootID string) (changed bool, sameDevice bool) {
	if deviceID != current.DeviceID {
		return false, false
	}
	recorded := device.BootIdentity{DeviceID: deviceID, BootID: bootID}
	if !validUIIdentity(recorded) {
		return false, false
	}
	return bootID != current.BootID, true
}

func uiExecutionNeedsPromotion(record *Ledger) bool {
	if record == nil || record.Cleanup == nil ||
		record.Cleanup.ExecReason != "ui_input" ||
		!cleanupExecutionUnsettled(record.Cleanup.Execution) {
		return false
	}
	return true
}

func cloneUIRecoveryLedger(record *Ledger) *Ledger {
	if record == nil {
		return nil
	}
	out := *record
	if record.Session != nil {
		session := *record.Session
		out.Session = &session
	}
	if record.PendingCreate != nil {
		command := *record.PendingCreate
		out.PendingCreate = &command
	}
	if record.PendingClose != nil {
		command := *record.PendingClose
		out.PendingClose = &command
	}
	if record.Cleanup != nil {
		cleanup := *record.Cleanup
		cleanup.CheckedTaskIDs = append([]int(nil), record.Cleanup.CheckedTaskIDs...)
		out.Cleanup = &cleanup
	}
	out.Tasks = append([]LedgerTask(nil), record.Tasks...)
	if record.Stop != nil {
		stop := *record.Stop
		out.Stop = &stop
	}
	if record.Shell != nil {
		shell := *record.Shell
		if record.Shell.ExitCode != nil {
			exitCode := *record.Shell.ExitCode
			shell.ExitCode = &exitCode
		}
		if record.Shell.Truncated != nil {
			truncated := *record.Shell.Truncated
			shell.Truncated = &truncated
		}
		out.Shell = &shell
	}
	if record.UI != nil {
		ui := *record.UI
		out.UI = &ui
	}
	if record.LegacyUI != nil {
		checkpoint := *record.LegacyUI
		out.LegacyUI = &checkpoint
	}
	return &out
}
