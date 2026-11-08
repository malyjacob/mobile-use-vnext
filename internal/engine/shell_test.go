package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
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

// Case 1: a normal exit publishes exactly one forced-full observation, retires
// (never claims) the old O and leaves one new ready full O.
func TestShellNormalExitPublishesFullObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.setShellDone(exitedDone(0, 6, false))
	h.device.shell.mu.Lock()
	h.device.shell.output = []byte("hello\n")
	h.device.shell.mu.Unlock()

	result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "echo hello"))
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
	if result.Observation == nil || result.Observation.Status != "complete" ||
		result.Observation.Tree == nil || result.Observation.Tree.Full == nil {
		t.Fatalf("post observation=%+v", result.Observation)
	}
	if result.Next != "" {
		t.Fatalf("next=%q", result.Next)
	}

	h.engine.mu.Lock()
	old := h.engine.observations[first.Observation.ID]
	full := h.engine.current
	h.engine.mu.Unlock()
	if old == nil || old.Status != ObservationRetired || old.ClaimedBy != "" {
		t.Fatalf("old O must be retired without a claim: %+v", old)
	}
	if full == nil || full.Status != ObservationReady || !full.Full || full.ID == first.Observation.ID {
		t.Fatalf("new O must be a ready full observation: %+v", full)
	}
	prepare, start, _, _, _, remove := h.device.shellCalls()
	if prepare != 1 || start != 1 || remove != 1 {
		t.Fatalf("calls prepare=%d start=%d remove=%d", prepare, start, remove)
	}
	if string(h.device.lastShellCommand()) != "echo hello" {
		t.Fatalf("command bytes were not passed exactly")
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
		h.device.shell.mu.Lock()
		h.device.shell.startErr = device.ErrShellNotStarted
		h.device.shell.mu.Unlock()
		result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "false"))
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeShellStartFailed || result.Next != "status" {
			t.Fatalf("result=%+v", result)
		}
		requireShellView(t, result, terminationNotStarted, nil)
		if result.Shell.Output != nil || result.Shell.Truncated != nil {
			t.Fatalf("unstarted command must not claim output: %+v", result.Shell)
		}
		if result.Observation == nil || result.Observation.Status != "complete" {
			t.Fatalf("healthy scope still needs one full: %+v", result.Observation)
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
			result.Error.Code != protocol.CodeShellUnknown || result.Next != "status" {
			t.Fatalf("result=%+v", result)
		}
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
		result.Error.Code != protocol.CodeRootUnavailable || result.Next != "user" {
		t.Fatalf("result=%+v", result)
	}
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

// Case 10: a failed post observation keeps the confirmed shell fact.
func TestShellPostObservationFailureKeepsExecuted(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.setShellDone(exitedDone(1, 0, false))
	h.device.mu.Lock()
	h.device.captureErrs = true
	h.device.mu.Unlock()
	result, _ := h.engine.Shell(context.Background(), "agent", "Psh",
		shellRequestFor(first.Observation, "exit 1"))
	if result.Outcome != OutcomeExecuted || result.Observation == nil ||
		result.Observation.Status != "failed" || result.Next != "observe" {
		t.Fatalf("result=%+v", result)
	}
	requireShellView(t, result, terminationExited, intPtr(1))
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
