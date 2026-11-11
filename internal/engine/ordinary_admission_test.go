package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestOrdinaryAdmissionRejectsAllToolsBehindUnresolvedShell(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.cfg.ShellTimeout = 20 * time.Millisecond
	h.engine.cfg.ShellConfirmTimeout = 20 * time.Millisecond
	// An unreadable start keeps the exact chain unresolved while the barrier
	// inside its bounded confirmation deadline still closes admission.
	h.device.shell.mu.Lock()
	h.device.shell.startErr = errors.New("synthetic ambiguous start")
	h.device.shell.mu.Unlock()

	shelled, apiErr := h.engine.Shell(context.Background(), "agent", "Pshellbarrier",
		shellRequestFor(first.Observation, "synthetic"))
	if apiErr != nil || shelled.Outcome != OutcomeUncertain {
		t.Fatalf("unresolved shell result = %+v, api error = %v", shelled, apiErr)
	}

	h.engine.mu.Lock()
	runner := h.engine.shell
	workReleased := h.engine.work == nil
	h.engine.mu.Unlock()
	if runner == nil || runner.settled() {
		t.Fatalf("uncertain shell must retain its runner ownership: %+v", runner)
	}
	if !workReleased {
		t.Fatal("uncertain shell result did not release the ordinary work slot")
	}

	calls := []struct {
		name    string
		p       string
		outcome string
		call    func() (*protocol.Result, *protocol.APIError)
	}{
		{
			name: "observe",
			p:    "Pbarrierobserve",
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.Observe(context.Background(), "agent", "Pbarrierobserve",
					&protocol.ObserveRequest{Reference: first.Observation.ID})
			},
		},
		{
			name:    "act",
			p:       "Pbarrieract",
			outcome: OutcomeNotExecuted,
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.Act(context.Background(), "agent", "Pbarrieract",
					tapRequest(first.Observation.ID))
			},
		},
		{
			name:    "open_app",
			p:       "Pbarrieropen",
			outcome: OutcomeNotExecuted,
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.App(context.Background(), "agent", "Pbarrieropen",
					appOpenRequest(first.Observation, "com.example"))
			},
		},
		{
			name:    "shell",
			p:       "Pbarriershell2",
			outcome: OutcomeNotExecuted,
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.Shell(context.Background(), "agent", "Pbarriershell2",
					shellRequestFor(first.Observation, "synthetic-2"))
			},
		},
		{
			name:    "apps",
			p:       "Pbarrierapps",
			outcome: "",
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.App(context.Background(), "agent", "Pbarrierapps",
					&protocol.AppRequest{Kind: protocol.AppKindFind})
			},
		},
	}
	for _, call := range calls {
		result, apiErr := call.call()
		if apiErr != nil || result == nil || result.Error == nil ||
			result.Error.Code != protocol.CodeDeviceBusy ||
			result.Outcome != call.outcome {
			t.Errorf("%s was not rejected with outcome %q/device_busy: result=%+v api_error=%v",
				call.name, call.outcome, result, apiErr)
			continue
		}
		if result.BlockingOperationID != runner.OperationID {
			t.Errorf("%s blocking operation = %q, want %q",
				call.name, result.BlockingOperationID, runner.OperationID)
		}
	}

	outsider, apiErr := h.engine.App(context.Background(), "other", "Pbarrieroutside",
		&protocol.AppRequest{Kind: protocol.AppKindFind})
	if apiErr != nil || outsider == nil || outsider.Error == nil ||
		outsider.Error.Code != protocol.CodeDeviceBusy ||
		outsider.Outcome != "" {
		t.Fatalf("another agent was not rejected as a query conflict: result=%+v api_error=%v",
			outsider, apiErr)
	}
	if outsider.BlockingOperationID != "" {
		t.Fatalf("another agent learned the blocking operation: %q", outsider.BlockingOperationID)
	}

	h.engine.mu.Lock()
	retained := h.engine.shell
	h.engine.mu.Unlock()
	if retained != runner {
		t.Fatalf("ordinary rejections replaced the shell runner: got %+v, want %+v", retained, runner)
	}
}

type ordinaryAdmissionPersistFailStore struct {
	Store
}

func (ordinaryAdmissionPersistFailStore) Save(context.Context, *Ledger) (int64, error) {
	return 0, errors.New("synthetic ledger failure")
}

type ordinaryAdmissionIODevice struct {
	Device
	calls atomic.Int32
}

func (d *ordinaryAdmissionIODevice) touched() {
	d.calls.Add(1)
}

func (d *ordinaryAdmissionIODevice) Capture(ctx context.Context, display device.Display) (device.Capture, error) {
	d.touched()
	return d.Device.Capture(ctx, display)
}

func (d *ordinaryAdmissionIODevice) ProbeDisplay(ctx context.Context, id int) (device.DisplayState, error) {
	d.touched()
	return d.Device.ProbeDisplay(ctx, id)
}

func (d *ordinaryAdmissionIODevice) Tap(ctx context.Context, id, x, y int) (device.DispatchReceipt, error) {
	d.touched()
	return d.Device.Tap(ctx, id, x, y)
}

func (d *ordinaryAdmissionIODevice) AppList(ctx context.Context) ([]device.AppEntry, error) {
	d.touched()
	return d.Device.AppList(ctx)
}

func (d *ordinaryAdmissionIODevice) ResolveLauncher(ctx context.Context, pkg string) (string, bool, error) {
	d.touched()
	return d.Device.ResolveLauncher(ctx, pkg)
}

func (d *ordinaryAdmissionIODevice) RootAvailable(ctx context.Context) (bool, error) {
	d.touched()
	return d.Device.RootAvailable(ctx)
}

func (d *ordinaryAdmissionIODevice) PrepareManagedShell(ctx context.Context, runID string, command []byte) error {
	d.touched()
	return d.Device.PrepareManagedShell(ctx, runID, command)
}

func TestOrdinaryAdmissionLedgerFailureBlocksWithoutDeviceIOOrClaim(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.persistMu.Lock()
	h.engine.store = ordinaryAdmissionPersistFailStore{Store: h.store}
	h.engine.persistMu.Unlock()
	if err := h.engine.persistSync(context.Background()); err == nil {
		t.Fatal("synthetic persist failure was not observed")
	}

	h.engine.mu.Lock()
	ledgerUnsafe := h.engine.ledgerUnsafe
	noUI := h.engine.ui == nil
	noShell := h.engine.shell == nil
	h.engine.mu.Unlock()
	if !ledgerUnsafe || !noUI || !noShell {
		t.Fatalf("test setup ledger_unsafe=%t ui_absent=%t shell_absent=%t",
			ledgerUnsafe, noUI, noShell)
	}

	counted := &ordinaryAdmissionIODevice{Device: h.device}
	h.engine.dev = counted
	calls := []struct {
		name    string
		p       string
		outcome string
		call    func() (*protocol.Result, *protocol.APIError)
	}{
		{
			name: "observe",
			p:    "Pledgerobserve",
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.Observe(context.Background(), "agent", "Pledgerobserve",
					&protocol.ObserveRequest{Reference: first.Observation.ID})
			},
		},
		{
			name:    "act",
			p:       "Pledgeract",
			outcome: OutcomeNotExecuted,
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.Act(context.Background(), "agent", "Pledgeract",
					tapRequest(first.Observation.ID))
			},
		},
		{
			name:    "open_app",
			p:       "Pledgeropen",
			outcome: OutcomeNotExecuted,
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.App(context.Background(), "agent", "Pledgeropen",
					appOpenRequest(first.Observation, "com.example"))
			},
		},
		{
			name:    "shell",
			p:       "Pledgershell",
			outcome: OutcomeNotExecuted,
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.Shell(context.Background(), "agent", "Pledgershell",
					&protocol.ShellRequest{
						Reference: first.Observation.ID,
						Command:   "synthetic",
						Root:      true,
					})
			},
		},
		{
			name:    "apps",
			p:       "Pledgerapps",
			outcome: "",
			call: func() (*protocol.Result, *protocol.APIError) {
				return h.engine.App(context.Background(), "agent", "Pledgerapps",
					&protocol.AppRequest{Kind: protocol.AppKindFind})
			},
		},
	}
	for _, call := range calls {
		result, apiErr := call.call()
		if apiErr != nil || result == nil || result.Error == nil ||
			result.Error.Code != protocol.CodeDeviceUnavailable ||
			result.Outcome != call.outcome {
			t.Errorf("%s returned outcome %q while the ledger was unsafe: result=%+v api_error=%v",
				call.name, call.outcome, result, apiErr)
		}
	}
	if calls := counted.calls.Load(); calls != 0 {
		t.Fatalf("ordinary admission touched the device %d times after ledger failure", calls)
	}

	h.engine.mu.Lock()
	observationStatus := h.engine.observations[first.Observation.ID].Status
	claimed := make(map[string]bool, len(calls))
	for _, call := range calls {
		if op := h.engine.operations[call.p]; op != nil {
			claimed[call.p] = op.Claimed
		}
	}
	h.engine.mu.Unlock()
	if observationStatus != ObservationReady {
		t.Fatalf("barrier rejection consumed the ready observation: %s", observationStatus)
	}
	for _, call := range calls {
		if claimed[call.p] {
			t.Errorf("barrier rejection claimed P %s", call.p)
		}
	}
}

func TestOrdinaryAdmissionWarmAppsCacheHonorsBarriers(t *testing.T) {
	cases := []struct {
		name             string
		setup            func(*harness)
		code             string
		blockingID       string
		checkIDIsolation bool
	}{
		{
			name: "unresolved_shell",
			setup: func(h *harness) {
				h.engine.mu.Lock()
				h.engine.shell = &shellState{
					RunnerID:    "synthetic-runner",
					OperationID: "Pshellunknown",
					Agent:       "agent",
					Origin:      h.engine.session.Origin,
					Epoch:       h.engine.session.Epoch,
					State:       ShellRunning,
					StartedAt:   time.Now(),
					DeadlineAt:  time.Now().Add(time.Minute),
				}
				h.engine.mu.Unlock()
			},
			code:             protocol.CodeDeviceBusy,
			blockingID:       "Pshellunknown",
			checkIDIsolation: true,
		},
		{
			name: "ledger_unsafe",
			setup: func(h *harness) {
				h.engine.persistMu.Lock()
				h.engine.store = ordinaryAdmissionPersistFailStore{Store: h.store}
				h.engine.persistMu.Unlock()
				if err := h.engine.persistSync(context.Background()); err == nil {
					h.t.Fatal("synthetic persist failure was not observed")
				}
			},
			code: protocol.CodeDeviceUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWithClock(t, false)
			h.startSession()
			h.device.mu.Lock()
			h.device.appEntries = []device.AppEntry{{Name: "Synthetic", Package: "com.example"}}
			h.device.mu.Unlock()
			warm, apiErr := h.engine.App(context.Background(), "agent", "Pwarmapps",
				&protocol.AppRequest{Kind: protocol.AppKindFind})
			if apiErr != nil || warm == nil || warm.App == nil {
				t.Fatalf("warm apps cache failed: result=%+v api_error=%v", warm, apiErr)
			}
			h.device.mu.Lock()
			appCalls := h.device.appCalls
			h.device.mu.Unlock()

			tc.setup(h)
			blocked, apiErr := h.engine.App(context.Background(), "agent", "Pblockedapps",
				&protocol.AppRequest{Kind: protocol.AppKindFind})
			if apiErr != nil || blocked == nil || blocked.Error == nil ||
				blocked.Error.Code != tc.code || blocked.Outcome != "" {
				t.Fatalf("warm cache bypassed %s barrier: result=%+v api_error=%v",
					tc.name, blocked, apiErr)
			}
			if tc.blockingID != "" && blocked.BlockingOperationID != tc.blockingID {
				t.Fatalf("blocking operation = %q, want %q",
					blocked.BlockingOperationID, tc.blockingID)
			}
			h.device.mu.Lock()
			afterApps := h.device.appCalls
			h.device.mu.Unlock()
			if afterApps != appCalls {
				t.Fatalf("blocked warm cache performed %d device app reads", afterApps-appCalls)
			}

			if tc.checkIDIsolation {
				outsider, apiErr := h.engine.App(context.Background(), "other", "Poutsider",
					&protocol.AppRequest{Kind: protocol.AppKindFind})
				if apiErr != nil || outsider == nil || outsider.Error == nil ||
					outsider.Error.Code != tc.code ||
					outsider.Outcome != "" {
					t.Fatalf("outsider was not rejected: result=%+v api_error=%v", outsider, apiErr)
				}
				if outsider.BlockingOperationID != "" {
					t.Fatalf("outsider learned the blocking operation: %q",
						outsider.BlockingOperationID)
				}
			}
		})
	}
}
