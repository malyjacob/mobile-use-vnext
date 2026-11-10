package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type shellPreWriteGateStore struct {
	Store
	targetP string
	fail    bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *shellPreWriteGateStore) Save(ctx context.Context, record *Ledger) (int64, error) {
	if record.Shell != nil && record.Shell.OperationID == s.targetP &&
		record.Shell.State == ShellPreparing {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		if s.fail {
			return 0, errors.New("synthetic shell prewrite failure")
		}
	}
	return s.Store.Save(ctx, record)
}

func TestShellPreWriteStopRaceTerminatesUnstartedChain(t *testing.T) {
	for _, saveFails := range []bool{false, true} {
		name := "save_succeeds_revalidation_fails"
		if saveFails {
			name = "save_fails"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithClock(t, false)
			first := h.startSession()
			h.engine.cfg.LedgerTimeout = 3 * time.Second

			var physicalRechecks atomic.Int32
			h.engine.dev = &hookedDevice{
				Device: h.device,
				tasks: func(ctx context.Context) ([]device.Task, error) {
					physicalRechecks.Add(1)
					return h.device.TaskList(ctx)
				},
			}
			h.device.mu.Lock()
			h.device.displayExists = false
			h.device.recentKnown = true
			h.device.mu.Unlock()

			gate := &shellPreWriteGateStore{
				Store: h.store, targetP: "Pprewrite", fail: saveFails,
				entered: make(chan struct{}), release: make(chan struct{}),
			}
			h.engine.persistMu.Lock()
			h.engine.store = gate
			h.engine.persistMu.Unlock()

			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(gate.release) })
			calls := make(chan callResult, 1)
			go func() {
				result, apiErr := h.engine.Shell(context.Background(), "agent", "Pprewrite",
					shellRequestFor(first.Observation, "synthetic"))
				calls <- callResult{result: result, err: apiErr}
			}()
			awaitSignal(t, gate.entered)

			prepare, start, _, _, _, _ := h.device.shellCalls()
			if prepare != 0 || start != 0 {
				t.Fatalf("dispatch crossed the blocked prewrite: prepare=%d start=%d", prepare, start)
			}

			stopStarted := time.Now()
			response, apiErr := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
				ContractVersion: protocol.ContractVersion,
				AppInstanceID:   h.appInstance,
				StopSeq:         1,
				Reason:          "user_stop",
			})
			if apiErr != nil || response == nil || !response.Accepted {
				t.Fatalf("Stop: response=%+v api_error=%v", response, apiErr)
			}
			if elapsed := time.Since(stopStarted); elapsed > time.Second {
				t.Fatalf("STOP waited for the blocked shell prewrite: %v", elapsed)
			}

			prepare, start, _, _, _, _ = h.device.shellCalls()
			if prepare != 0 || start != 0 {
				t.Fatalf("STOP did not prevent shell dispatch: prepare=%d start=%d", prepare, start)
			}

			awaitShellCondition(t, 3*time.Second, "cleanup did not record the shell prewrite barrier", func() bool {
				h.engine.mu.Lock()
				defer h.engine.mu.Unlock()
				return h.engine.cleanup != nil &&
					h.engine.cleanup.State == protocol.CleanupUnconfirmed &&
					h.engine.cleanup.Execution == protocol.CleanupUnconfirmed &&
					h.engine.cleanup.ExecReason == "shell_chain" &&
					h.engine.cleanup.Tasks == protocol.CleanupConfirmed &&
					h.engine.cleanup.Display == protocol.CleanupConfirmed
			})

			h.sync(nil, nil)
			physicalRechecks.Store(0)
			releaseOnce.Do(func() { close(gate.release) })
			result := awaitCall(t, calls)

			wantCode := protocol.CodeStopped
			if saveFails {
				wantCode = protocol.CodeDeviceUnavailable
			}
			if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
				result.Error.Code != wantCode {
				t.Errorf("unstarted shell result: %+v, want code %s", result, wantCode)
			}
			if result.Shell == nil || result.Shell.Termination != terminationNotStarted {
				t.Errorf("unstarted shell omitted its terminal evidence: %+v", result.Shell)
			}
			if result.Observation != nil {
				t.Errorf("unstarted shell published an observation: %+v", result.Observation)
			}

			prepare, start, _, _, _, _ = h.device.shellCalls()
			if prepare != 0 || start != 0 {
				t.Fatalf("unstarted terminalization dispatched a runner: prepare=%d start=%d", prepare, start)
			}

			awaitShellCondition(t, 3*time.Second, "terminal evidence did not trigger a fresh physical recheck", func() bool {
				return physicalRechecks.Load() > 0
			})
			awaitShellCondition(t, 3*time.Second, "cleanup stayed permanently shell_chain", func() bool {
				h.engine.mu.Lock()
				defer h.engine.mu.Unlock()
				return h.engine.cleanup != nil &&
					h.engine.cleanup.Execution == protocol.CleanupConfirmed &&
					h.engine.cleanup.State == protocol.CleanupComplete
			})
		})
	}
}
