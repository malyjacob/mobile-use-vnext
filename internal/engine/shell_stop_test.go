package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Case 9: STOP never waits for the device chain, the shell result reports the
// confirmed cancellation, and cleanup.execution is promoted from unconfirmed to
// confirmed by the exact termination evidence. No new observation is published.
func TestShellStopDuringRunIsBoundedAndPromotesCleanup(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.engine.cfg.ShellTimeout = 5 * time.Second
	h.engine.cfg.ShellConfirmTimeout = 300 * time.Millisecond

	gate := make(chan struct{})
	h.device.shell.mu.Lock()
	h.device.shell.terminateGate = gate
	h.device.shell.terminate = device.ShellTermination{Confirmed: true}
	h.device.shell.mu.Unlock()
	// The physical closure can prove the virtual display is gone.
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()

	type shellCall struct {
		result *protocol.Result
		err    *protocol.APIError
	}
	calls := make(chan shellCall, 1)
	go func() {
		result, apiErr := h.engine.Shell(context.Background(), "agent", "Psh",
			shellRequestFor(first.Observation, "sleep 300"))
		calls <- shellCall{result: result, err: apiErr}
	}()
	awaitShellCondition(t, 3*time.Second, "shell never started", func() bool {
		_, start, _, _, _, _ := h.device.shellCalls()
		return start == 1
	})

	startedAt := time.Now()
	response, apiErr := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
		ContractVersion: protocol.ContractVersion, AppInstanceID: h.appInstance,
		StopSeq: 1, Reason: "user_stop",
	})
	elapsed := time.Since(startedAt)
	if apiErr != nil {
		t.Fatalf("Stop: %v", apiErr)
	}
	if !response.Accepted {
		t.Fatalf("stop not accepted: %+v", response)
	}
	if elapsed > time.Second {
		t.Fatalf("Stop waited for the device chain: %v", elapsed)
	}

	// The barrier fact is unconfirmed while the exact chain stop is not proven.
	awaitShellCondition(t, 2*time.Second, "cleanup did not record execution unconfirmed", func() bool {
		h.engine.mu.Lock()
		defer h.engine.mu.Unlock()
		return h.engine.cleanup != nil &&
			h.engine.cleanup.Execution == protocol.CleanupUnconfirmed &&
			h.engine.cleanup.ExecReason == "shell_chain"
	})

	close(gate)
	var call shellCall
	select {
	case call = <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("shell call did not finish after the confirmed termination")
	}
	if call.err != nil {
		t.Fatalf("Shell: %v", call.err)
	}
	if call.result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome=%s result=%+v", call.result.Outcome, call.result)
	}
	requireShellView(t, call.result, terminationCancelled, nil)
	if call.result.Observation != nil {
		t.Fatalf("STOP must not publish a new observation: %+v", call.result.Observation)
	}
	requireRecovery(t, call.result, protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRevoked, protocol.RecoveryCauseExecutedRevoked)
	if !call.result.IncludeSession || call.result.Session == nil ||
		call.result.Session.State != SessionEnded {
		t.Fatalf("ended session must be included: %+v", call.result.Session)
	}

	// The confirmed exact stop is release-of-barrier evidence, not an action
	// rewrite: cleanup.execution becomes confirmed and the closure completes
	// without leaving the stale execution_unconfirmed cause behind.
	awaitShellCondition(t, 3*time.Second, "cleanup barrier was not promoted", func() bool {
		h.engine.mu.Lock()
		defer h.engine.mu.Unlock()
		return h.engine.cleanup != nil &&
			h.engine.cleanup.Execution == protocol.CleanupConfirmed &&
			h.engine.cleanup.State == protocol.CleanupComplete
	})
	h.engine.mu.Lock()
	cleanupError := h.engine.cleanup.Error
	h.engine.mu.Unlock()
	if cleanupError != nil {
		t.Fatalf("promoted cleanup kept a stale error: %+v", cleanupError)
	}

	h.engine.mu.Lock()
	observations := len(h.engine.observations)
	current := h.engine.current
	shell := h.engine.shell
	oldStatus := h.engine.observations[first.Observation.ID].Status
	h.engine.mu.Unlock()
	if observations != 1 || current != nil {
		t.Fatalf("observations=%d current=%+v", observations, current)
	}
	if oldStatus != ObservationRetired {
		t.Fatalf("old O status=%s", oldStatus)
	}
	if shell != nil {
		t.Fatalf("confirmed chain kept the barrier: %+v", shell)
	}
}
