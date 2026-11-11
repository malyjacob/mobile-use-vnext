package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Internal uncertainty belongs to the operation and its execution evidence. It
// must not end the session or keep ordinary admission closed beyond its bound.
func TestInternalUncertainKeepsSessionActiveAndOrdinaryWorkUsable(t *testing.T) {
	shellCall := func(h *harness, first *protocol.Result) (*protocol.Result, *protocol.APIError) {
		return h.engine.Shell(context.Background(), "agent", "Pinternaluncertain",
			shellRequestFor(first.Observation, "synthetic"))
	}
	openAppCall := func(h *harness, first *protocol.Result) (*protocol.Result, *protocol.APIError) {
		return h.engine.OpenApp(context.Background(), "agent", "Pinternaluncertain",
			openAppRequest(first.Observation, "com.example"))
	}
	attributionSetup := func(multiple bool) func(*harness) {
		return func(h *harness) {
			h.device.mu.Lock()
			h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example"}
			h.device.recentKnown = true
			h.device.recentIDs = map[int]bool{77: true}
			h.device.mu.Unlock()
			// TasksOnDisplay still supplies the real pre-launch task list;
			// hide or make ambiguous the subsequent TaskList attribution read.
			h.engine.dev = &hookedDevice{Device: h.device, tasks: func(context.Context) ([]device.Task, error) {
				if multiple {
					return []device.Task{
						{TaskID: 77, DisplayID: 12, Package: "com.example"},
						{TaskID: 78, DisplayID: 12, Package: "com.example"},
					}, nil
				}
				return nil, nil
			}}
		}
	}
	cases := []struct {
		name          string
		setup         func(*harness)
		call          func(*harness, *protocol.Result) (*protocol.Result, *protocol.APIError)
		errorCode     string
		unknownUI     bool
		boundedShell  bool
		startMayBlock bool
	}{
		{
			name: "act_lost_receipt",
			setup: func(h *harness) {
				h.engine.dev = &hookedDevice{Device: h.device,
					tap: func(context.Context, int, int, int) error {
						return errors.New("synthetic lost input receipt")
					}}
			},
			call: func(h *harness, first *protocol.Result) (*protocol.Result, *protocol.APIError) {
				return h.engine.Act(context.Background(), "agent", "Pinternaluncertain",
					tapRequest(first.Observation.ID))
			},
			errorCode: protocol.CodeActionUnknown,
			unknownUI: true,
		},
		{
			name: "shell_unreadable_start",
			setup: func(h *harness) {
				h.device.shell.mu.Lock()
				h.device.shell.startErr = errors.New("synthetic unreadable shell start")
				h.device.shell.mu.Unlock()
			},
			call:          shellCall,
			errorCode:     protocol.CodeShellUnknown,
			boundedShell:  true,
			startMayBlock: true,
		},
		{
			name: "shell_timeout_unconfirmed_termination",
			setup: func(h *harness) {
				h.device.shell.mu.Lock()
				h.device.shell.terminate = device.ShellTermination{}
				h.device.shell.mu.Unlock()
			},
			call:         shellCall,
			errorCode:    protocol.CodeShellUnknown,
			boundedShell: true,
		},
		{
			name: "shell_finalized_unknown_reason",
			setup: func(h *harness) {
				h.device.setShellDone(device.ShellDone{Reason: "synthetic_unknown_reason"})
			},
			call:      shellCall,
			errorCode: protocol.CodeShellUnknown,
		},
		{
			name:      "open_app_missing_task_attribution",
			setup:     attributionSetup(false),
			call:      openAppCall,
			errorCode: protocol.CodeAppLaunchUnknown,
		},
		{
			name:      "open_app_multiple_task_attribution",
			setup:     attributionSetup(true),
			call:      openAppCall,
			errorCode: protocol.CodeAppLaunchUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessWithClock(t, false)
			first := h.startSession()
			h.engine.mu.Lock()
			h.engine.cfg.ShellTimeout = 20 * time.Millisecond
			h.engine.cfg.ShellConfirmTimeout = 20 * time.Millisecond
			h.engine.mu.Unlock()
			tc.setup(h)

			assertActive := func(stage string) {
				t.Helper()
				h.engine.mu.Lock()
				defer h.engine.mu.Unlock()
				if h.engine.session == nil {
					t.Fatalf("%s: internal uncertainty removed the session", stage)
				}
				if h.engine.session.State != SessionActive || h.engine.session.Reason != "" {
					t.Fatalf("%s: internal uncertainty changed session: state=%q reason=%q",
						stage, h.engine.session.State, h.engine.session.Reason)
				}
			}
			observe := func(p string, allowBusy bool) {
				t.Helper()
				result, apiErr := h.engine.Observe(context.Background(), "agent", p,
					&protocol.ObserveRequest{Reference: first.Observation.ID})
				if apiErr != nil || result == nil {
					t.Fatalf("observe: result=%+v api_error=%v", result, apiErr)
				}
				if allowBusy && result.Error != nil && result.Error.Code == protocol.CodeDeviceBusy {
					return // An unreadable start may still own its bounded barrier.
				}
				if result.Error != nil || result.Observation == nil {
					t.Fatalf("ordinary observation stayed blocked: result=%+v error=%+v", result, result.Error)
				}
			}

			result, apiErr := tc.call(h, first)
			if apiErr != nil || result == nil {
				t.Fatalf("uncertain call: result=%+v api_error=%v", result, apiErr)
			}
			if result.Outcome != OutcomeUncertain || result.Error == nil || result.Error.Code != tc.errorCode {
				t.Fatalf("outcome=%q error=%+v, want uncertain/%s", result.Outcome, result.Error, tc.errorCode)
			}
			assertActive("call returned")

			h.engine.mu.Lock()
			workReleased := h.engine.work == nil
			var run *shellState
			if tc.boundedShell {
				run = h.engine.shell
				if !tc.startMayBlock {
					if run != nil {
						t.Error("unconfirmed timeout retained the live shell work position")
					}
					run = h.engine.shellOrphan
					if run == nil || run.State != ShellUnknown || run.Termination != terminationUnknown || run.provenGone() {
						t.Errorf("unconfirmed timeout lost its unproven close-out fact: %+v", run)
					}
				}
			}
			if tc.unknownUI {
				if token := h.engine.ui; token == nil || token.record.State != uiStateUnknown || !token.returned {
					t.Errorf("lost receipt did not close the UI window as unknown: %+v", token)
				}
				if h.engine.uiPendingLocked() {
					t.Error("returned input call is still pending")
				}
			}
			h.engine.mu.Unlock()
			if !workReleased {
				t.Fatal("uncertain call retained the ordinary work position")
			}
			if tc.unknownUI {
				h.store.mu.Lock()
				persistedUnknown := h.store.record != nil && h.store.record.UI != nil && h.store.record.UI.State == uiStateUnknown
				h.store.mu.Unlock()
				if !persistedUnknown {
					t.Error("lost receipt did not persist the unknown UI window")
				}
			}

			observe("Pinternalimmediateobserve", tc.startMayBlock)
			assertActive("immediate observation")
			if tc.boundedShell {
				if run == nil {
					t.Fatal("uncertain shell lost its recorded runner identity")
				}
				h.engine.mu.Lock()
				run.DeadlineAt = time.Now().Add(-time.Minute)
				h.engine.mu.Unlock()
				h.engine.housekeeping(context.Background())

				h.engine.mu.Lock()
				closedOut := h.engine.work == nil && h.engine.shell == nil && h.engine.shellOrphan == run &&
					run.State == ShellUnknown && run.Termination == terminationUnknown && !run.provenGone()
				h.engine.mu.Unlock()
				if !closedOut {
					t.Fatal("past the shell bound: work not released or unproven orphan identity lost")
				}
				assertActive("shell bound forced")
				observe("Pinternalafterboundobserve", false)
				assertActive("observation after shell bound")
			}

			original, apiErr := h.engine.Status("agent", "Pinternaluncertain")
			if apiErr != nil || original == nil || original.Outcome != OutcomeUncertain {
				t.Fatalf("later work rewrote original uncertain outcome: result=%+v api_error=%v", original, apiErr)
			}
		})
	}
}
