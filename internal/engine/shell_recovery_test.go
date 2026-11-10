package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// shellOnlyLedger is a version 2 previous instance whose only unresolved live
// authority is a managed runner. It still carries the original session so the
// v3 migration can preserve the ownership record.
func shellOnlyLedger() *Ledger {
	now := time.Now()
	return &Ledger{
		Format: ledgerFormat, Version: 2, ServiceInstanceID: "old",
		UpdatedAt: now.Add(-time.Minute), AppInstanceID: testAppID,
		Session: &LedgerSession{
			Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionEnded,
			AppInstanceID: testAppID, ModeGeneration: testModeID,
			DisplayID: 12, DisplayGeneration: testDisplayGen,
			Width: 1080, Height: 2400, Density: 420,
			StartedAt: now.Add(-time.Minute),
		},
		Cleanup: shellChainCleanup(),
		Shell: &LedgerShell{
			RunnerID:    "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			OperationID: "Pshell", Agent: "agent", Origin: "Pold", Epoch: 1,
			AppInstanceID: testAppID, ModeGeneration: testModeID,
			State: ShellRunning, Phase: "running", CommandRunes: 12,
			StartedAt: now.Add(-time.Minute), DeadlineAt: now.Add(-30 * time.Second),
			StartPID: 100, StartPGID: 100, StartTicks: 7, BootID: "boot-old",
		},
	}
}

func shellRecoveryDevice(alive bool, snapshotStart device.ShellStart) *fakeDevice {
	dev := newFakeDevice()
	dev.shell.mu.Lock()
	dev.shell.hasStart = true
	dev.shell.startInfo = snapshotStart
	dev.shell.alive = alive
	dev.shell.mu.Unlock()
	dev.mu.Lock()
	dev.displayExists = false
	dev.recentKnown = true
	dev.recentIDs = map[int]bool{}
	dev.mu.Unlock()
	return dev
}

func assertShellRecovered(t *testing.T, e *Engine, outcome string, wantError bool) {
	t.Helper()
	e.mu.Lock()
	current := e.shell
	var retired *retiredState
	for _, candidate := range e.retired {
		if candidate.session != nil && candidate.session.Origin == "Pold" {
			retired = candidate
			break
		}
	}
	var run *shellState
	if retired != nil {
		run = retired.shell
	}
	op := e.operations["Pshell"]
	ledger := e.ledgerRecordLocked()
	e.mu.Unlock()
	if current != nil {
		t.Fatalf("retired shell remained in current ownership: %+v", current)
	}
	if ledger.Shell != nil {
		t.Fatalf("v3 current fields retained the retired shell: %+v", ledger.Shell)
	}
	if retired == nil || run == nil {
		t.Fatalf("retired ownership lost the shell record: %+v", ledger.Retired)
	}
	if !run.settled() {
		t.Fatalf("confirmed chain remained unresolved in retired ownership: %+v", run)
	}
	if len(ledger.Retired) != 1 || ledger.Retired[0].Record == nil ||
		ledger.Retired[0].Record.Shell == nil {
		t.Fatalf("retired ledger lost the original shell fact: %+v", ledger.Retired)
	}
	if op == nil {
		t.Fatalf("old P record was not restored")
	}
	if op.State != OperationFinished || op.Phase != "previous_instance" {
		t.Fatalf("old P record=%+v", op)
	}
	if op.Agent != "agent" {
		t.Fatalf("old P agent=%q, want agent", op.Agent)
	}
	if op.Outcome != outcome {
		t.Fatalf("outcome=%s want %s", op.Outcome, outcome)
	}
	if wantError && (op.Error == nil || op.Error.Code != protocol.CodeShellUnknown) {
		t.Fatalf("old P error=%+v", op.Error)
	}
	if !wantError && op.Error != nil {
		t.Fatalf("old P error=%+v", op.Error)
	}
	// The recovered P must stay addressable by the owning agent, not 409.
	status, statusErr := e.Status("agent", "Pshell")
	if statusErr != nil {
		t.Fatalf("status(Pshell) rejected after recovery: %v", statusErr)
	}
	if status.OperationState != OperationFinished || status.Phase != "previous_instance" {
		t.Fatalf("status(Pshell)=%+v", status)
	}
}

// Case 12a: an alive runner with the recorded identity is terminated by the
// retired cleanup. The old chain remains a reported fact instead of holding the
// ordinary admission gate.
func TestShellRecoveryTerminatesLiveRunner(t *testing.T) {
	dev := shellRecoveryDevice(true, device.ShellStart{
		HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-old",
	})
	gate := make(chan struct{})
	dev.shell.mu.Lock()
	dev.shell.terminateGate = gate
	dev.shell.terminate = device.ShellTermination{Confirmed: true}
	dev.shell.mu.Unlock()

	e := recoveryEngine(t, shellOnlyLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	assertCannotStart(t, e)
	syncRecovery(t, e, readySync())
	awaitShellCondition(t, 3*time.Second, "live runner was not terminated", func() bool {
		dev.shell.mu.Lock()
		defer dev.shell.mu.Unlock()
		return dev.shell.terminateCalls == 1
	})
	assertCanStart(t, e)
	close(gate)
	waitRecoveryComplete(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertShellRecovered(t, e, OutcomeUncertain, true)

	dev.shell.mu.Lock()
	reasons := append([]string(nil), dev.shell.terminateReasons...)
	dev.shell.mu.Unlock()
	if len(reasons) != 1 || reasons[0] != terminationCancelled {
		t.Fatalf("terminate reasons=%v", reasons)
	}
	assertCanStart(t, e)
}

// Case 12b: a natural exit receipt is proven executed; no signal is sent.
func TestShellRecoveryDoneReceiptNeedsNoSignal(t *testing.T) {
	dev := shellRecoveryDevice(true, device.ShellStart{
		HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-old",
	})
	zero := 0
	dev.shell.mu.Lock()
	dev.shell.hasDone, dev.shell.done = true, device.ShellDone{
		Reason: terminationExited, ExitCode: &zero, Bytes: 3, HasBytes: true,
	}
	dev.shell.mu.Unlock()
	e := recoveryEngine(t, shellOnlyLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	syncRecovery(t, e, readySync())
	waitRecoveryComplete(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertShellRecovered(t, e, OutcomeExecuted, false)
	dev.shell.mu.Lock()
	defer dev.shell.mu.Unlock()
	if dev.shell.terminateCalls != 0 {
		t.Fatalf("a proven natural exit must not be signalled")
	}
}

// Case 12c: a gone PID needs no signal.
func TestShellRecoveryGoneProcessNeedsNoSignal(t *testing.T) {
	dev := shellRecoveryDevice(false, device.ShellStart{
		HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-old",
	})
	e := recoveryEngine(t, shellOnlyLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	syncRecovery(t, e, readySync())
	waitRecoveryComplete(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertShellRecovered(t, e, OutcomeUncertain, true)
	dev.shell.mu.Lock()
	defer dev.shell.mu.Unlock()
	if dev.shell.terminateCalls != 0 {
		t.Fatalf("a gone process must not be signalled")
	}
}

// Case 12d: a changed boot id proves the recorded process is gone; no signal.
func TestShellRecoveryBootChangeNeedsNoSignal(t *testing.T) {
	dev := shellRecoveryDevice(true, device.ShellStart{
		HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-new",
	})
	e := recoveryEngine(t, shellOnlyLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	syncRecovery(t, e, readySync())
	waitRecoveryComplete(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertShellRecovered(t, e, OutcomeUncertain, true)
	dev.shell.mu.Lock()
	defer dev.shell.mu.Unlock()
	if dev.shell.terminateCalls != 0 {
		t.Fatalf("a different boot/start identity must not be signalled")
	}
}

// Case 12e: an unconfirmed stop remains an unknown retired fact. Its cleanup
// stops within its budget and never becomes confirmed merely because admission
// is allowed again.
func TestShellRecoveryUnknownStopsWithoutAdmissionGate(t *testing.T) {
	dev := shellRecoveryDevice(true, device.ShellStart{
		HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-old",
	})
	dev.shell.mu.Lock()
	dev.shell.terminate = device.ShellTermination{Unknown: true}
	dev.shell.mu.Unlock()
	e := recoveryEngine(t, shellOnlyLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	e.mu.Lock()
	recovering := e.recovery != nil
	e.mu.Unlock()
	if recovering {
		t.Fatal("retired shell remained in startup recovery")
	}
	for i := 0; i < 3; i++ {
		e.RetryRecovery(context.Background())
		assertCanStart(t, e)
	}
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.Shell == nil || retired.Record.Shell.State == ShellConfirmed {
		t.Fatalf("unknown runner state was rewritten as confirmed: %+v", retired.Record.Shell)
	}
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.State == protocol.CleanupComplete {
		t.Fatalf("unknown stop was promoted to confirmed cleanup: %+v", retired.Record.Cleanup)
	}
}

// A transport failure while probing is bounded and retried on the next tick.
func TestShellRecoveryProbeFailureStaysBounded(t *testing.T) {
	dev := shellRecoveryDevice(true, device.ShellStart{
		HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-old",
	})
	dev.shell.mu.Lock()
	dev.shell.inspectErr = errors.New("adb unavailable")
	dev.shell.mu.Unlock()
	e := recoveryEngine(t, shellOnlyLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertCanStart(t, e)
	_, _, inspectCalls, _, terminateCalls, _ := dev.shellCalls()
	if inspectCalls == 0 {
		t.Fatal("retired shell compensation skipped the runner probe")
	}
	if terminateCalls != 0 {
		t.Fatalf("unreadable runner was signalled %d times", terminateCalls)
	}
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.State == protocol.CleanupComplete {
		t.Fatalf("probe failure fabricated a confirmed end: %+v", retired.Record.Cleanup)
	}
}

// shellChainCleanup is a closure whose only unresolved fact is a shell-caused
// execution uncertainty.
func shellChainCleanup() *LedgerCleanup {
	return &LedgerCleanup{
		State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupUnconfirmed,
		Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupNotApplicable,
		Display: protocol.CleanupConfirmed, Reason: "user_stop",
		ExecReason: "shell_chain", StartedAt: time.Now().Add(-time.Minute),
	}
}

func assertCleanupPromoted(t *testing.T, e *Engine) {
	t.Helper()
	e.mu.Lock()
	cleanup := e.cleanup
	record := e.ledgerRecordLocked()
	e.mu.Unlock()
	if cleanup == nil || cleanup.State != protocol.CleanupComplete ||
		cleanup.Execution != protocol.CleanupConfirmed {
		t.Fatalf("cleanup was not promoted: %+v", cleanup)
	}
	if record.Session != nil || record.Cleanup != nil || record.Shell != nil {
		t.Fatalf("retired completion remained in current fields: %+v", record)
	}
	if len(record.Retired) != 1 || record.Retired[0].Record == nil ||
		record.Retired[0].Record.Cleanup == nil ||
		record.Retired[0].Record.Cleanup.Execution != protocol.CleanupConfirmed ||
		record.Retired[0].Record.Cleanup.State != protocol.CleanupComplete {
		t.Fatalf("retired cleanup was not promoted: %+v", record.Retired)
	}
	assertCanStart(t, e)
}

// A shell-caused execution uncertainty is promoted by exact-chain release
// evidence, both when the runner is resolved in this pass and when a previous
// pass already cleared the durable runner ownership.
func TestShellRecoveryPromotesShellChainCleanup(t *testing.T) {
	t.Run("runner_resolved_in_pass", func(t *testing.T) {
		record := shellOnlyLedger()
		record.Cleanup = shellChainCleanup()
		dev := shellRecoveryDevice(false, device.ShellStart{
			HasIdentity: true, PID: 100, PGID: 100, StartTicks: 7, BootID: "boot-old",
		})
		e := recoveryEngine(t, record, dev)
		if err := e.StartRecovery(context.Background()); err != nil {
			t.Fatalf("StartRecovery: %v", err)
		}
		syncRecovery(t, e, readySync())
		waitRecoveryComplete(t, e)
		waitRetiredCleanupStopped(t, e, "Pold")
		assertShellRecovered(t, e, OutcomeUncertain, true)
		assertCleanupPromoted(t, e)
	})
	t.Run("orphan_shell_chain", func(t *testing.T) {
		record := shellOnlyLedger()
		record.Shell = nil // the exact chain was confirmed in an earlier pass
		record.Cleanup = shellChainCleanup()
		dev := newFakeDevice()
		dev.mu.Lock()
		dev.displayExists = false
		dev.recentKnown = true
		dev.recentIDs = map[int]bool{}
		dev.mu.Unlock()
		e := recoveryEngine(t, record, dev)
		if err := e.StartRecovery(context.Background()); err != nil {
			t.Fatalf("StartRecovery: %v", err)
		}
		syncRecovery(t, e, readySync())
		waitRecoveryComplete(t, e)
		waitRetiredCleanupStopped(t, e, "Pold")
		assertCleanupPromoted(t, e)
		dev.mu.Lock()
		removals := append([]int(nil), dev.removeCalls...)
		dev.mu.Unlock()
		if len(removals) != 0 {
			t.Fatalf("orphan shell recovery deleted recents without same-boot ownership: %v", removals)
		}
	})
	t.Run("ui_input_uncertainty_stays_sticky", func(t *testing.T) {
		record := shellOnlyLedger()
		record.Shell = nil
		cleanup := shellChainCleanup()
		cleanup.ExecReason = "ui_input"
		record.Cleanup = cleanup
		e := recoveryEngine(t, record, newFakeDevice())
		if err := e.StartRecovery(context.Background()); err != nil {
			t.Fatalf("StartRecovery: %v", err)
		}
		syncRecovery(t, e, readySync())
		waitRecoveryAttempt(t, e)
		waitRetiredCleanupStopped(t, e, "Pold")
		assertCanStart(t, e)
		retired := retiredLedgerForOrigin(t, e, "Pold")
		if retired.Record.Cleanup == nil ||
			retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed ||
			retired.Record.Cleanup.State == protocol.CleanupComplete {
			t.Fatalf("UI input uncertainty must stay unconfirmed: %+v", retired.Record.Cleanup)
		}
	})
}
