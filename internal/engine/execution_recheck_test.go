package engine

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestTerminalEvidenceSavesPhysicalRecheckObligation(t *testing.T) {
	for _, source := range []string{"ui", "shell"} {
		t.Run(source, func(t *testing.T) {
			record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
			record.Session.State = SessionEnded
			e, store := newUIRecoveryTestEngine(record, newFakeDevice())
			e.mu.Lock()
			e.restoreRecoveryLocked(record)
			e.recovery = nil
			// Model the crash window before any asynchronous physical recheck.
			e.shuttingDown = true
			work := &workState{UI: e.ui}
			if source == "shell" {
				e.ui = nil
				e.cleanup.ExecReason = "shell_chain"
				run := &shellState{State: ShellRunning, Origin: record.Session.Origin}
				e.shell = run
				e.noteShellTerminalLocked(run)
				run.State = ShellConfirmed
				e.cleanup.Execution = protocol.CleanupConfirmed
			}
			e.mu.Unlock()
			if source == "ui" {
				e.recordUIReceipt(work, device.DispatchTerminated)
			} else if err := e.persistSync(context.Background()); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(store.lastSave(t))
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Cleanup map[string]any `json:"cleanup"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Cleanup["execution_recheck_required"] != true {
				t.Fatal("terminal evidence became durable without its physical recheck obligation")
			}
		})
	}
}

func TestRestartRetainsPhysicalRecheckAfterTerminalEvidence(t *testing.T) {
	for _, source := range []string{"ui", "shell"} {
		t.Run(source, func(t *testing.T) {
			record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
			record.Session.State = SessionEnded
			record.Cleanup.Execution = protocol.CleanupConfirmed
			record.Cleanup.CheckedTaskIDs = []int{41}
			if source == "shell" {
				record.UI = nil
				record.Cleanup.ExecReason = "shell_chain"
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			wire["cleanup"].(map[string]any)["execution_recheck_required"] = true
			data, _ = json.Marshal(wire)
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			h := newHarnessWithClock(t, false)
			setUIRecoveryIdentity(h.device, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
			h.device.mu.Lock()
			h.device.displayExists = false
			h.device.recentKnown = true
			h.device.recentIDs = map[int]bool{41: true}
			h.device.mu.Unlock()
			h.engine.mu.Lock()
			h.engine.recovery = record
			h.engine.recovering = true
			h.engine.restoreRecoveryLocked(record)
			h.engine.mu.Unlock()
			h.engine.driveRecovery(context.Background())
			status, apiErr := h.engine.Status("agent", "")
			if apiErr != nil || status.CanStart == nil || *status.CanStart {
				t.Fatal("restart accepted stale confirmed recents from before terminal evidence")
			}
			if status.BlockingCode != "old_resource_unconfirmed" {
				t.Fatalf("unexpected recovery gate: %s", status.BlockingCode)
			}
		})
	}
}
