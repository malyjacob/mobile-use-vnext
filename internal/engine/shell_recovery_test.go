package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// shellOnlyLedger is a previous instance whose only unresolved ownership is a
// managed runner.
func shellOnlyLedger() *Ledger {
	now := time.Now()
	return &Ledger{
		Format: ledgerFormat, Version: ledgerVersion, ServiceInstanceID: "old",
		UpdatedAt: now.Add(-time.Minute), AppInstanceID: testAppID,
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
	return dev
}

func assertShellRecovered(t *testing.T, e *Engine, outcome string, wantError bool) {
	t.Helper()
	e.mu.Lock()
	run := e.shell
	op := e.operations["Pshell"]
	e.mu.Unlock()
	if run != nil {
		t.Fatalf("confirmed chain kept the shell barrier: %+v", run)
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
}

// Case 12a: an alive runner with the recorded identity is terminated; admission
// stays closed until that exact stop is confirmed.
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
	assertCannotStart(t, e)
	close(gate)
	waitRecoveryComplete(t, e)
	assertShellRecovered(t, e, OutcomeUncertain, true)

	dev.shell.mu.Lock()
	reasons := append([]string(nil), dev.shell.terminateReasons...)
	dev.shell.mu.Unlock()
	if len(reasons) != 1 || reasons[0] != terminationCancelled {
		t.Fatalf("terminate reasons=%v", reasons)
	}
	status, apiErr := e.Status("agent", "")
	if apiErr != nil || status.CanStart == nil || !*status.CanStart {
		t.Fatalf("start not admitted after the confirmed stop: %+v %v", status, apiErr)
	}
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
	assertShellRecovered(t, e, OutcomeUncertain, true)
	dev.shell.mu.Lock()
	defer dev.shell.mu.Unlock()
	if dev.shell.terminateCalls != 0 {
		t.Fatalf("a different boot/start identity must not be signalled")
	}
}

// Case 12e: an unconfirmed stop keeps the recovery barrier closed on every
// retry; it never flips can_start to true.
func TestShellRecoveryUnknownKeepsBarrier(t *testing.T) {
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
	e.mu.Lock()
	reason := e.recoveryReason
	e.mu.Unlock()
	if reason != "shell_chain_unconfirmed" {
		t.Fatalf("recovery reason=%q", reason)
	}
	for i := 0; i < 3; i++ {
		e.RetryRecovery(context.Background())
		assertCannotStart(t, e)
	}
	e.mu.Lock()
	run, recovering := e.shell, e.recovery != nil
	e.mu.Unlock()
	if run == nil || run.settled() || !recovering {
		t.Fatalf("unresolved chain must keep the barrier: run=%+v recovering=%v", run, recovering)
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
	e.mu.Lock()
	reason := e.recoveryReason
	e.mu.Unlock()
	if reason != "shell_probe_failed" {
		t.Fatalf("recovery reason=%q", reason)
	}
	assertCannotStart(t, e)
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
	if record.Cleanup == nil || record.Cleanup.Execution != protocol.CleanupConfirmed ||
		record.Cleanup.State != protocol.CleanupComplete {
		t.Fatalf("durable cleanup was not promoted: %+v", record.Cleanup)
	}
	status, apiErr := e.Status("agent", "")
	if apiErr != nil || status.CanStart == nil || !*status.CanStart {
		t.Fatalf("start not admitted after promotion: %+v %v", status, apiErr)
	}
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
		assertShellRecovered(t, e, OutcomeUncertain, true)
		assertCleanupPromoted(t, e)
	})
	t.Run("orphan_shell_chain", func(t *testing.T) {
		record := shellOnlyLedger()
		record.Shell = nil // the exact chain was confirmed in an earlier pass
		record.Cleanup = shellChainCleanup()
		e := recoveryEngine(t, record, newFakeDevice())
		if err := e.StartRecovery(context.Background()); err != nil {
			t.Fatalf("StartRecovery: %v", err)
		}
		syncRecovery(t, e, readySync())
		waitRecoveryComplete(t, e)
		assertCleanupPromoted(t, e)
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
		e.mu.Lock()
		reason, recovering := e.recoveryReason, e.recovery != nil
		e.mu.Unlock()
		if reason != "old_resource_unconfirmed" || !recovering {
			t.Fatalf("UI input uncertainty must stay sticky: reason=%q recovering=%v", reason, recovering)
		}
		assertCannotStart(t, e)
	})
}
