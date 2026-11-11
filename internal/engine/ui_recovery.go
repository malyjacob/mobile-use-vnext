package engine

// restoreUIRecoveryLocked restores evidence without sharing mutable ledger
// fields with the recovery record. The caller holds e.mu.
//
// Recovery only retires old ownership; it never replays an input and never
// waits for an unknown old fact. A restored token is therefore evidence for the
// closure record, not an admission gate.
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
