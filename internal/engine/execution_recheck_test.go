package engine

import (
	"context"
	"encoding/json/v2"
	"sync"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestTerminalEvidenceSavesPhysicalRecheckObligation(t *testing.T) {
	for _, source := range []string{"ui", "shell"} {
		t.Run(source, func(t *testing.T) {
			h := newHarnessWithClock(t, false)
			session := &sessionState{
				Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionEnded,
				Epoch: 1, AppInstanceID: h.appInstance,
				Display: device.Display{ID: 12, Generation: testDisplayGen},
			}
			cleanup := &cleanupState{
				State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupUnconfirmed,
				Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupConfirmed,
				Display: protocol.CleanupConfirmed,
			}
			retired := &retiredState{session: session, cleanup: cleanup}
			var work *workState
			var run *shellState
			if source == "shell" {
				cleanup.ExecReason = "shell_chain"
				run = &shellState{
					RunnerID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
					Origin:   session.Origin, Agent: session.Agent, Epoch: session.Epoch,
					State: ShellRunning,
				}
				retired.shell = run
			} else {
				cleanup.ExecReason = "ui_input"
				token := &uiDispatchState{record: LedgerUI{
					OperationID: "Pinput", Origin: session.Origin,
					Epoch: session.Epoch, DisplayID: 12, State: "may_dispatch",
				}}
				retired.ui = token
				work = &workState{UI: token}
			}
			h.engine.mu.Lock()
			h.engine.retired = []*retiredState{retired}
			// Model the crash window before any asynchronous physical recheck.
			h.engine.shuttingDown = true
			h.engine.mu.Unlock()
			if source == "ui" {
				h.engine.recordUIReceipt(work, device.DispatchTerminated)
			} else {
				h.engine.confirmShellBarrier(run, terminationCancelled, nil)
				if err := h.engine.persistSync(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			h.store.mu.Lock()
			savedLedger := copyUIRecoveryLedgerForTest(h.store.record)
			h.store.mu.Unlock()
			data, err := json.Marshal(savedLedger)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Retired []struct {
					Record struct {
						Cleanup map[string]any `json:"cleanup"`
					} `json:"record"`
				} `json:"retired"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.Retired) != 1 ||
				saved.Retired[0].Record.Cleanup["execution_recheck_required"] != true {
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
			base := newFakeDevice()
			setUIRecoveryIdentity(base, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
			base.mu.Lock()
			base.displayExists = false
			base.recentKnown = true
			base.recentIDs = map[int]bool{41: true}
			base.mu.Unlock()
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseIO := func() {
				releaseOnce.Do(func() { close(release) })
			}
			defer releaseIO()
			var physicalRead sync.Once
			dev := &hookedDevice{
				Device: base,
				tasks: func(ctx context.Context) ([]device.Task, error) {
					physicalRead.Do(func() { close(entered) })
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return base.TaskList(ctx)
				},
			}
			e, store := newUIRecoveryTestEngine(record, dev)
			syncRecovery(t, e, readySync())
			awaitSignal(t, entered)
			retired := retiredLedgerForOrigin(t, e, "Pold")
			if retired.Record.Cleanup == nil ||
				!retired.Record.Cleanup.ExecutionRecheckRequired ||
				retired.Record.Cleanup.State == protocol.CleanupComplete ||
				retired.Record.Cleanup.Recents != protocol.CleanupConfirmed {
				t.Fatalf("retired snapshot lost the pre-restart recheck obligation: %+v", retired.Record.Cleanup)
			}
			saved := store.lastSave(t)
			if saved.Version != ledgerVersion || saved.Session != nil ||
				saved.Cleanup != nil || len(saved.Retired) != 1 ||
				saved.Retired[0].Record == nil || saved.Retired[0].Record.Cleanup == nil ||
				!saved.Retired[0].Record.Cleanup.ExecutionRecheckRequired {
				t.Fatalf("recheck obligation was not durable before device I/O: %+v", saved)
			}
			releaseIO()
			waitRecoveryAttempt(t, e)
			waitRetiredCleanupStopped(t, e, "Pold")
			assertCanStart(t, e)
			retired = retiredLedgerForOrigin(t, e, "Pold")
			base.mu.Lock()
			removals := append([]int(nil), base.removeCalls...)
			base.mu.Unlock()
			if source == "ui" {
				if retired.Record.Cleanup == nil ||
					retired.Record.Cleanup.ExecutionRecheckRequired ||
					retired.Record.Cleanup.State != protocol.CleanupComplete ||
					retired.Record.Cleanup.Recents != protocol.CleanupConfirmed ||
					len(removals) != 1 || removals[0] != 41 {
					t.Fatalf("same-boot recheck did not replace stale recents with fresh physical evidence: cleanup=%+v removals=%v", retired.Record.Cleanup, removals)
				}
			} else {
				if retired.Record.Cleanup == nil ||
					retired.Record.Cleanup.Execution != protocol.CleanupConfirmed ||
					retired.Record.Cleanup.ExecutionRecheckRequired ||
					retired.Record.Cleanup.Recents != protocol.CleanupUnconfirmed ||
					retired.Record.Cleanup.State == protocol.CleanupComplete ||
					len(removals) != 0 {
					t.Fatalf("identity-free shell recheck fabricated recents release or removal: cleanup=%+v removals=%v", retired.Record.Cleanup, removals)
				}
			}
		})
	}
}
