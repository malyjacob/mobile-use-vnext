package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func shellRequestFor(o *protocol.Observation, command string) *protocol.ShellRequest {
	return &protocol.ShellRequest{Reference: o.ID, Command: command}
}

func awaitShellCondition(t *testing.T, timeout time.Duration, message string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}

func requireShellView(t *testing.T, result *protocol.Result, termination string, exitCode *int) {
	t.Helper()
	if result.Shell == nil {
		t.Fatalf("missing shell view: %+v", result)
	}
	if result.Shell.Termination != termination {
		t.Fatalf("termination=%s want %s", result.Shell.Termination, termination)
	}
	switch {
	case exitCode == nil && result.Shell.ExitCode != nil:
		t.Fatalf("exit_code=%d want null", *result.Shell.ExitCode)
	case exitCode != nil && (result.Shell.ExitCode == nil || *result.Shell.ExitCode != *exitCode):
		t.Fatalf("exit_code=%v want %d", result.Shell.ExitCode, *exitCode)
	}
}

// Case 1: a healthy exit reports the command facts only. The ready observation
// and the confirmed baseline stay untouched, and the flow performs no capture.
func TestShellKeepsReadyObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	// Acknowledging the first full candidate confirms the delta baseline, so
	// the case can prove that a command does not drop it.
	if _, apiErr := h.engine.Observe(context.Background(), "agent", "Pobserve",
		&protocol.ObserveRequest{Reference: first.Observation.ID}); apiErr != nil {
		t.Fatalf("Observe: %v", apiErr)
	}
	h.engine.mu.Lock()
	ready := h.engine.current
	baseline := h.engine.baseline
	observations := len(h.engine.observations)
	h.engine.mu.Unlock()
	if ready == nil || ready.Status != ObservationReady || baseline == nil {
		t.Fatalf("precondition: ready=%+v baseline=%+v", ready, baseline)
	}
	captures := h.device.captureCount()

	h.device.setShellDone(exitedDone(0, 6, false))
	h.device.shell.mu.Lock()
	h.device.shell.output = []byte("hello\n")
	h.device.shell.mu.Unlock()

	result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(&protocol.Observation{ID: ready.ID}, "echo hello"))
	if apiErr != nil {
		t.Fatalf("Shell: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("result=%+v", result)
	}
	zero := 0
	requireShellView(t, result, terminationExited, &zero)
	if result.Shell.Output == nil || *result.Shell.Output != "hello\n" {
		t.Fatalf("output=%v", result.Shell.Output)
	}
	if result.Shell.Truncated == nil || *result.Shell.Truncated {
		t.Fatalf("truncated=%v", result.Shell.Truncated)
	}
	if result.Observation != nil {
		t.Fatalf("shell must not publish an observation: %+v", result.Observation)
	}
	requireNoRecovery(t, result)
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("shell performed %d captures, want none", got-captures)
	}

	h.engine.mu.Lock()
	after, afterBaseline := h.engine.current, h.engine.baseline
	afterStatus := h.engine.observations[ready.ID].Status
	afterObservations := len(h.engine.observations)
	afterWork := h.engine.work
	h.engine.mu.Unlock()
	if after != ready || afterStatus != ObservationReady || after.ClaimedBy != "" {
		t.Fatalf("current=%+v status=%s claimed_by=%q", after, afterStatus, after.ClaimedBy)
	}
	if afterBaseline != baseline {
		t.Fatalf("confirmed baseline changed: %+v", afterBaseline)
	}
	if afterObservations != observations {
		t.Fatalf("observations=%d want %d", afterObservations, observations)
	}
	if afterWork != nil {
		t.Fatalf("device work position was not released: %+v", afterWork)
	}

	prepare, start, _, _, _, remove := h.device.shellCalls()
	if prepare != 1 || start != 1 || remove != 1 {
		t.Fatalf("calls prepare=%d start=%d remove=%d", prepare, start, remove)
	}
	if string(h.device.lastShellCommand()) != "echo hello" {
		t.Fatalf("command bytes were not passed exactly")
	}
}

// A chain of commands performs no capture and allocates no observation: the
// same ready O carries every result.
func TestShellChainPerformsNoCapture(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	captures := h.device.captureCount()
	h.device.setShellDone(exitedDone(0, 0, false))
	for index := 0; index < 3; index++ {
		result, apiErr := h.engine.Shell(context.Background(), "agent",
			"Pchain"+strconv.Itoa(index), shellRequestFor(first.Observation, "true"))
		if apiErr != nil {
			t.Fatalf("Shell %d: %v", index, apiErr)
		}
		if result.Outcome != OutcomeExecuted || result.Observation != nil {
			t.Fatalf("run %d: %+v", index, result)
		}
		requireNoRecovery(t, result)
	}
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("shell chain performed %d captures, want none", got-captures)
	}
	h.engine.mu.Lock()
	observations := len(h.engine.observations)
	status := h.engine.observations[first.Observation.ID].Status
	current := h.engine.current
	h.engine.mu.Unlock()
	if observations != 1 || status != ObservationReady || current == nil ||
		current.ID != first.Observation.ID {
		t.Fatalf("observations=%d status=%s current=%+v", observations, status, current)
	}
}

// A command that does not change the screen leaves the O usable: the next act
// revalidates the live target and dispatches without an extra observe.
func TestShellThenActRevalidatesTheSameObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatalf("button not found in tree")
	}
	h.device.setShellDone(exitedDone(0, 0, false))
	if result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "true")); apiErr != nil || result.Observation != nil {
		t.Fatalf("Shell: result=%+v err=%v", result, apiErr)
	}
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pact", &protocol.ActRequest{
		Reference: first.Observation.ID,
		Operation: protocol.Operation{Type: "tap",
			Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("act outcome=%s error=%+v", result.Outcome, result.Error)
	}
	if len(h.device.taps) != 1 {
		t.Fatalf("taps=%v", h.device.taps)
	}
}

// A command that changes the screen is caught by the next act's live
// revalidation, before any input is dispatched.
func TestShellThenActRejectsAChangedTarget(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatalf("button not found in tree")
	}
	h.device.setShellDone(exitedDone(0, 0, false))
	// The command changes the page; the ready O still describes the old one.
	h.device.scriptCaptures(markerCapture("after-shell"))
	if result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "true")); apiErr != nil || result.Observation != nil {
		t.Fatalf("Shell: result=%+v err=%v", result, apiErr)
	}
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pact", &protocol.ActRequest{
		Reference: first.Observation.ID,
		Operation: protocol.Operation{Type: "tap",
			Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil {
		t.Fatalf("a changed target must be a result, not an API error: %v", apiErr)
	}
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeTargetChanged {
		t.Fatalf("result=%+v error=%+v", result, result.Error)
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if len(h.device.taps) != 0 {
		t.Fatalf("a changed target was dispatched: %v", h.device.taps)
	}
}

// A command never extends the input deadline: an O that ran out of time during
// the command is rejected by the next act.
func TestShellThenActRejectsExpiredObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatalf("button not found in tree")
	}
	h.device.setShellDone(exitedDone(0, 0, false))
	if result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "true")); apiErr != nil || result.Observation != nil {
		t.Fatalf("Shell: result=%+v err=%v", result, apiErr)
	}
	h.engine.mu.Lock()
	h.engine.current.ActionableUntil = time.Now().Add(-time.Second)
	h.engine.mu.Unlock()
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pact", &protocol.ActRequest{
		Reference: first.Observation.ID,
		Operation: protocol.Operation{Type: "tap",
			Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil {
		t.Fatalf("expiry must be a result, not an API error: %v", apiErr)
	}
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeObservationExpired {
		t.Fatalf("result=%+v error=%+v", result, result.Error)
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("an expired observation dispatched input: %v", h.device.taps)
	}
}

// Case 2: a non-zero exit and a command that exits 124 itself are both natural
// exits, never a supervisor timeout.
func TestShellNonZeroAndSelfExit124StayExited(t *testing.T) {
	for _, code := range []int{7, 124} {
		code := code
		t.Run(intName(code), func(t *testing.T) {
			h := newHarness(t)
			first := h.startSession()
			h.device.setShellDone(exitedDone(code, 0, false))
			result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
				shellRequestFor(first.Observation, "exit "+intName(code)))
			if apiErr != nil {
				t.Fatalf("Shell: %v", apiErr)
			}
			if result.Outcome != OutcomeExecuted {
				t.Fatalf("outcome=%s", result.Outcome)
			}
			requireShellView(t, result, terminationExited, &code)
		})
	}
}

func intName(value int) string {
	switch value {
	case 7:
		return "seven"
	case 124:
		return "one_two_four"
	}
	return "other"
}

// Case 3: output budget, known-empty output and missing output.
func TestShellOutputFactsStayLayered(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		large := strings.Repeat("a", 70000)
		h.device.shell.mu.Lock()
		h.device.shell.output = []byte(large)
		h.device.shell.mu.Unlock()
		h.device.setShellDone(exitedDone(0, len(large), true))
		result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "cat big"))
		requireShellView(t, result, terminationExited, intPtr(0))
		if result.Shell.Output == nil || len(*result.Shell.Output) != 65536 {
			t.Fatalf("output len=%v want 65536", outputLen(result.Shell.Output))
		}
		if result.Shell.Truncated == nil || !*result.Shell.Truncated {
			t.Fatalf("truncated=%v", result.Shell.Truncated)
		}
	})
	t.Run("known_empty", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		h.device.setShellDone(exitedDone(0, 0, false))
		result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "true"))
		requireShellView(t, result, terminationExited, intPtr(0))
		if result.Shell.Output == nil || *result.Shell.Output != "" {
			t.Fatalf("empty output must be a known empty string: %v", result.Shell.Output)
		}
		if result.Shell.Truncated == nil || *result.Shell.Truncated {
			t.Fatalf("truncated=%v", result.Shell.Truncated)
		}
	})
	t.Run("missing_output", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		h.device.shell.mu.Lock()
		h.device.shell.outputErr = errors.New("unreadable")
		h.device.shell.mu.Unlock()
		h.device.setShellDone(exitedDone(0, 3, false))
		result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "cat lost"))
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("output loss must not change executed: %+v", result)
		}
		requireShellView(t, result, terminationExited, intPtr(0))
		if result.Shell.Output != nil {
			t.Fatalf("output must stay null: %v", *result.Shell.Output)
		}
		if result.Shell.OutputError == nil || result.Shell.OutputError.Code != protocol.CodeShellOutput {
			t.Fatalf("output_error=%+v", result.Shell.OutputError)
		}
		if result.Shell.Truncated == nil || *result.Shell.Truncated {
			t.Fatalf("truncated stays known from the runner: %v", result.Shell.Truncated)
		}
	})
}

func outputLen(value *string) any {
	if value == nil {
		return nil
	}
	return len(*value)
}

// Case 4: provably not started versus start/end unknown.
func TestShellStartOutcomes(t *testing.T) {
	t.Run("not_started", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		captures := h.device.captureCount()
		h.device.shell.mu.Lock()
		h.device.shell.startErr = device.ErrShellNotStarted
		h.device.shell.mu.Unlock()
		result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "false"))
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeShellStartFailed {
			t.Fatalf("result=%+v", result)
		}
		requireRecovery(t, result, protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
		requireShellView(t, result, terminationNotStarted, nil)
		if result.Shell.Output != nil || result.Shell.Truncated != nil {
			t.Fatalf("unstarted command must not claim output: %+v", result.Shell)
		}
		if result.Observation != nil {
			t.Fatalf("unstarted command must not publish an observation: %+v", result.Observation)
		}
		if got := h.device.captureCount(); got != captures {
			t.Fatalf("unstarted command performed %d captures", got-captures)
		}
		h.engine.mu.Lock()
		status := h.engine.observations[first.Observation.ID].Status
		current := h.engine.current
		h.engine.mu.Unlock()
		if status != ObservationReady || current == nil || current.ID != first.Observation.ID {
			t.Fatalf("status=%s current=%+v", status, current)
		}
		_, _, _, _, _, remove := h.device.shellCalls()
		if remove != 1 {
			t.Fatalf("runner dir must be removed after a confirmed non-start: %d", remove)
		}
	})
	t.Run("unconfirmed", func(t *testing.T) {
		// No clock: the result assertion must not race a housekeeping probe.
		h := newHarnessWithClock(t, false)
		first := h.startSession()
		h.device.shell.mu.Lock()
		h.device.shell.startErr = device.ErrShellStartUnconfirmed
		h.device.shell.mu.Unlock()
		result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "maybe"))
		if result.Outcome != OutcomeUncertain || result.Error == nil ||
			result.Error.Code != protocol.CodeShellUnknown {
			t.Fatalf("result=%+v", result)
		}
		requireRecovery(t, result, protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
		requireShellView(t, result, terminationUnknown, nil)
		if result.Shell.Output != nil || result.Shell.Truncated != nil {
			t.Fatalf("unknown end must not claim output: %+v", result.Shell)
		}
		if result.Observation != nil {
			t.Fatalf("unknown end must not publish an observation: %+v", result.Observation)
		}
		h.engine.mu.Lock()
		run, dispatchUnknown := h.engine.shell, h.engine.work == nil
		h.engine.mu.Unlock()
		if run == nil || run.settled() {
			t.Fatalf("unknown chain must keep the barrier: %+v", run)
		}
		if !dispatchUnknown {
			t.Fatalf("work slot must be released after the uncertain result")
		}
		state, reason := h.engine.Availability()
		if state != AvailabilityRecovering ||
			(reason != "shell_pending" && reason != "shell_chain_unconfirmed") {
			t.Fatalf("availability=%s %s", state, reason)
		}
	})
}

// Case 5: a supervisor timeout is only reported as such when the stop of the
// exact chain is confirmed.
func TestShellTimeoutOutcome(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		confirmed := confirmed
		t.Run(map[bool]string{true: "confirmed", false: "unconfirmed"}[confirmed], func(t *testing.T) {
			h := newHarness(t)
			first := h.startSession()
			h.engine.cfg.ShellTimeout = 120 * time.Millisecond
			h.engine.cfg.ShellConfirmTimeout = 200 * time.Millisecond
			h.device.shell.mu.Lock()
			h.device.shell.terminate = device.ShellTermination{Confirmed: confirmed, Unknown: !confirmed}
			h.device.shell.mu.Unlock()
			result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
				shellRequestFor(first.Observation, "sleep 300"))
			if confirmed {
				if result.Outcome != OutcomeExecuted {
					t.Fatalf("outcome=%s", result.Outcome)
				}
				requireShellView(t, result, terminationTimeout, nil)
			} else {
				if result.Outcome != OutcomeUncertain || result.Error == nil ||
					result.Error.Code != protocol.CodeShellUnknown {
					t.Fatalf("result=%+v", result)
				}
				requireShellView(t, result, terminationUnknown, nil)
				if result.Observation != nil {
					t.Fatalf("unconfirmed timeout must not observe")
				}
			}
			h.device.shell.mu.Lock()
			reasons := append([]string(nil), h.device.shell.terminateReasons...)
			h.device.shell.mu.Unlock()
			if len(reasons) != 1 || reasons[0] != terminationTimeout {
				t.Fatalf("terminate reasons=%v", reasons)
			}
		})
	}
}

// Case 6: root denial is a user-facing admission rejection that never touches
// the runner or the ready observation.
func TestShellRootDeniedDoesNotDispatch(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.shell.mu.Lock()
	h.device.shell.rootDenied = true
	h.device.shell.mu.Unlock()
	result, _ := h.engine.Shell(context.Background(), "agent", "Psh", &protocol.ShellRequest{
		Reference: first.Observation.ID, Command: "id", Root: true,
	})
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeRootUnavailable {
		t.Fatalf("result=%+v", result)
	}
	requireRecovery(t, result, protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone, "", "")
	if result.Shell != nil {
		t.Fatalf("pre-boundary rejection must omit shell: %+v", result.Shell)
	}
	prepare, start, _, _, _, _ := h.device.shellCalls()
	if prepare != 0 || start != 0 {
		t.Fatalf("root denial dispatched: prepare=%d start=%d", prepare, start)
	}
	h.engine.mu.Lock()
	status := h.engine.observations[first.Observation.ID].Status
	h.engine.mu.Unlock()
	if status != ObservationReady {
		t.Fatalf("root denial consumed O: %s", status)
	}
}

// Case 7: busy never consumes the observation and never dispatches.
func TestShellBusyKeepsObservationReady(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.engine.mu.Lock()
	h.engine.work = &workState{OperationID: "Pother", Agent: "agent", Kind: WorkObserve,
		Phase: "capture", SessionEpoch: h.engine.session.Epoch, Start: time.Now(),
		Ctx: context.Background(), Cancel: func() {}}
	h.engine.mu.Unlock()
	result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "id"))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeDeviceBusy {
		t.Fatalf("result=%+v", result)
	}
	if result.Shell != nil {
		t.Fatalf("busy rejection must omit shell: %+v", result.Shell)
	}
	prepare, start, _, _, _, _ := h.device.shellCalls()
	if prepare != 0 || start != 0 {
		t.Fatalf("busy dispatched: prepare=%d start=%d", prepare, start)
	}
	h.engine.mu.Lock()
	status := h.engine.observations[first.Observation.ID].Status
	h.engine.work = nil
	h.engine.mu.Unlock()
	if status != ObservationReady {
		t.Fatalf("busy consumed O: %s", status)
	}
}

// Case 8: the same P is never dispatched twice.
func TestShellDuplicatePDispatchOnce(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.setShellDone(exitedDone(0, 0, false))
	req := shellRequestFor(first.Observation, "true")
	if _, apiErr := h.engine.Shell(context.Background(), "agent", "Psh", req); apiErr != nil {
		t.Fatalf("Shell: %v", apiErr)
	}
	duplicate, apiErr := h.engine.Shell(context.Background(), "agent", "Psh", req)
	if apiErr != nil {
		t.Fatalf("duplicate: %v", apiErr)
	}
	if duplicate.OperationState != OperationFinished || duplicate.Outcome != OutcomeExecuted {
		t.Fatalf("duplicate=%+v", duplicate)
	}
	prepare, start, _, _, _, _ := h.device.shellCalls()
	if prepare != 1 || start != 1 {
		t.Fatalf("duplicate dispatched again: prepare=%d start=%d", prepare, start)
	}
}

// Case 10: an unreadable hierarchy never reaches the shell result. The escape
// hatch (repairing a broken tree with a command) still reports a clean fact,
// and the ready O stays usable.
func TestShellNeedsNoTreeForItsResult(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.setShellDone(exitedDone(1, 0, false))
	h.device.mu.Lock()
	h.device.captureErrs = true
	h.device.mu.Unlock()
	result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "exit 1"))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("result=%+v", result)
	}
	requireShellView(t, result, terminationExited, intPtr(1))
	if result.Observation != nil {
		t.Fatalf("a broken tree changed the shell result: %+v", result)
	}
	requireNoRecovery(t, result)
	h.engine.mu.Lock()
	status := h.engine.observations[first.Observation.ID].Status
	h.engine.mu.Unlock()
	if status != ObservationReady {
		t.Fatalf("shell consumed the observation: %s", status)
	}
}

// Case 11: ownership is durable before Prepare and never contains the command.
func TestShellLedgerOwnershipBeforeDispatch(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	const command = "echo LEDGER-SECRET-COMMAND"
	const output = "LEDGER-SECRET-OUTPUT"
	h.device.setShellDone(exitedDone(0, len(output), false))
	h.device.shell.mu.Lock()
	h.device.shell.output = []byte(output)
	seen := false
	h.device.shell.prepareHook = func() {
		h.store.mu.Lock()
		record := h.store.record
		h.store.mu.Unlock()
		if record == nil || record.Shell == nil {
			t.Errorf("ledger has no shell ownership before Prepare")
			return
		}
		if record.Shell.RunnerID == "" || record.Shell.OperationID != "Psh" {
			t.Errorf("ledger shell identity=%+v", record.Shell)
		}
		if record.Shell.CommandRunes != len([]rune(command)) {
			t.Errorf("command runes=%d", record.Shell.CommandRunes)
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Errorf("marshal ledger: %v", err)
			return
		}
		if strings.Contains(string(encoded), "LEDGER-SECRET") {
			t.Errorf("ledger contains the command or output")
		}
		seen = true
	}
	h.device.shell.mu.Unlock()

	result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, command))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("result=%+v", result)
	}
	if !seen {
		t.Fatalf("Prepare ran without durable ownership")
	}
	if err := h.engine.persistSync(context.Background()); err != nil {
		t.Fatalf("persist: %v", err)
	}
	h.store.mu.Lock()
	record := h.store.record
	h.store.mu.Unlock()
	if record == nil || record.Shell != nil {
		t.Fatalf("confirmed chain must clear the durable shell record: %+v", record)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "LEDGER-SECRET") {
		t.Fatalf("ledger contains the command or output")
	}
}

// Case 13: logs carry whitelisted facts only, never the command or output.
func TestShellLogsNeverCarryCommandOrOutput(t *testing.T) {
	h := newHarness(t)
	out := &lockedBuffer{}
	logger := eventlog.New(out, "instance-test")
	h.engine.log = logger
	first := h.startSession()
	h.device.setShellDone(exitedDone(0, 12, false))
	h.device.shell.mu.Lock()
	h.device.shell.output = []byte("LOG-SECRET-OUT")
	h.device.shell.mu.Unlock()
	result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "echo LOG-SECRET-CMD"))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("result=%+v", result)
	}
	logger.Close()
	logged := out.String()
	if strings.Contains(logged, "LOG-SECRET") {
		t.Fatalf("log leaked command or output: %s", logged)
	}
	if !strings.Contains(logged, "shell.finish") || !strings.Contains(logged, "termination") {
		t.Fatalf("bounded shell event missing: %s", logged)
	}
}
