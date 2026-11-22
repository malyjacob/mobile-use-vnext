package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// appOpenRequest builds one open request against a current ready observation.
func appOpenRequest(o *protocol.Observation, app string) *protocol.AppRequest {
	return &protocol.AppRequest{Kind: protocol.AppKindOpen, Reference: o.ID,
		Target: &protocol.AppTarget{App: app}}
}

// intValue dereferences one already-proven identity for assertions.
func intValue(t *testing.T, value *int, what string) int {
	t.Helper()
	if value == nil {
		t.Fatalf("%s is absent", what)
	}
	return *value
}

// syncPrimary reports a user-confirmed primary-display mode.
func (h *harness) syncPrimary() {
	h.t.Helper()
	power, keyguard := true, false
	req := &protocol.AppSyncRequest{
		ContractVersion: protocol.ContractVersion,
		AppInstanceID:   h.appInstance,
		AppVersion:      "test",
		SelectedMode:    "primary",
		ModeGeneration:  h.modeGen,
		ModeReady:       true, StopReady: true, NotificationsEnabled: true, BackgroundRunning: true,
		PowerInteractive: &power, KeyguardLocked: &keyguard,
		PrimaryDisplay: &protocol.AppDisplay{DisplayID: 0, Width: 1080, Height: 2400,
			Density: 420, Rotation: 0, Generation: h.modeGen},
	}
	response, apiErr := h.engine.SyncApp(context.Background(), req)
	if apiErr != nil {
		h.t.Fatalf("primary sync rejected: %v", apiErr)
	}
	h.lastSync = response
}

func TestAppOpenCreatesTaskOnVirtualDisplay(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.App == nil {
		t.Fatalf("result=%+v", result)
	}
	if intValue(t, result.App.TaskID, "task_id") != 77 ||
		intValue(t, result.App.DisplayID, "display_id") != 12 {
		t.Fatalf("app=%+v", result.App)
	}
	if result.App.Reused == nil || *result.App.Reused {
		t.Fatalf("fresh create must not report reuse: %+v", result.App)
	}
	if result.App.Ownership != protocol.TaskOwnershipOwned {
		t.Fatalf("virtual create must be owned: %+v", result.App)
	}
	if result.App.StatePreserved == nil || *result.App.StatePreserved {
		t.Fatalf("fresh create must not claim state preserved: %+v", result.App)
	}
	if result.Observation == nil || result.Observation.Tree == nil || result.Observation.Tree.Full == nil {
		t.Fatalf("expected a full post observation: %+v", result.Observation)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status != ObservationRetired {
		t.Fatalf("old O should be retired: %+v", h.engine.observations[first.Observation.ID])
	}
	if len(h.engine.session.Tasks) != 1 || h.engine.session.Tasks[0].TaskID != 77 {
		t.Fatalf("registry=%+v", h.engine.session.Tasks)
	}
	if h.engine.session.Tasks[0].Ownership != TaskOwned {
		t.Fatalf("registry ownership=%+v", h.engine.session.Tasks[0])
	}
}

func TestAppOpenReusesOwnLiveTask(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.tasks = append(h.device.tasks, device.Task{
		TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app",
		Component: "com.example.app/.MainActivity",
	})
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.example.app", TaskID: intPtr(77)}
	h.device.mu.Unlock()

	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.App == nil || result.App.Reused == nil || !*result.App.Reused ||
		intValue(t, result.App.TaskID, "task_id") != 77 {
		t.Fatalf("app=%+v", result.App)
	}
	if result.App.StatePreserved == nil || !*result.App.StatePreserved {
		t.Fatalf("reuse should report state preserved: %+v", result.App)
	}
	h.device.mu.Lock()
	launchCalls := h.device.launchCalls
	focusCalls := append([]int(nil), h.device.focusCalls...)
	h.device.mu.Unlock()
	if launchCalls != 0 {
		t.Fatalf("reuse must not launch again: %d", launchCalls)
	}
	if len(focusCalls) != 1 || focusCalls[0] != 77 {
		t.Fatalf("focus=%v", focusCalls)
	}
}

func TestAppOpenMultipleCandidatesChoosesFrontmostElseLowest(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.tasks = append(h.device.tasks,
		device.Task{TaskID: 90, RootID: 90, DisplayID: 12, Package: "com.example.app"},
		device.Task{TaskID: 41, RootID: 41, DisplayID: 12, Package: "com.example.app"},
	)
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.example.app", TaskID: intPtr(90)}
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if intValue(t, result.App.TaskID, "task_id") != 90 {
		t.Fatalf("frontmost candidate not chosen: %+v", result.App)
	}

	h2 := newHarness(t)
	second := h2.startSession()
	h2.device.mu.Lock()
	h2.device.tasks = append(h2.device.tasks,
		device.Task{TaskID: 90, RootID: 90, DisplayID: 12, Package: "com.example.app"},
		device.Task{TaskID: 41, RootID: 41, DisplayID: 12, Package: "com.example.app"},
	)
	h2.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.other", TaskID: intPtr(7)}
	h2.device.mu.Unlock()
	result, _ = h2.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(second.Observation, "com.example.app"))
	if intValue(t, result.App.TaskID, "task_id") != 41 {
		t.Fatalf("lowest task id not chosen: %+v", result.App)
	}
}

func TestAppOpenUnknownAppDoesNotConsumeObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.resolveMissing = true
	h.device.mu.Unlock()
	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.missing"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeAppNotFound ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("result=%+v", result)
	}
	requireRecovery(t, result, protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone,
		protocol.RecoveryInputEffective, "")
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status != ObservationReady {
		t.Fatalf("unknown app must not consume O: %+v", h.engine.observations[first.Observation.ID])
	}
	if h.device.launchCalls != 0 {
		t.Fatalf("launch dispatched for an unknown app")
	}
}

func TestAppOpenControlPackageOnVirtualDisplayRejected(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, h.engine.cfg.ControlPackage))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeControlUI {
		t.Fatalf("result=%+v", result)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status != ObservationReady {
		t.Fatalf("control UI rejection must not consume O")
	}
}

func TestAppOpenLaunchRejectedConsumesObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.launchState = device.LaunchRejected
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if result.Error == nil || result.Error.Code != protocol.CodeAppLaunchFailed ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("result=%+v", result)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status == ObservationReady {
		t.Fatalf("dispatched rejection must keep O consumed")
	}
}

func TestAppOpenLaunchUnknownIsUncertain(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.launchState = device.LaunchUnknown
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeAppLaunchUnknown {
		t.Fatalf("result=%+v", result)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExecutionUnknown {
		t.Fatalf("uncertain launch must keep execution unknown")
	}
}

func TestAppOpenPostObservationFailureKeepsExecuted(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	h.device.mu.Lock()
	h.device.captureErrs = true
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if result.Outcome != OutcomeExecuted || result.Observation == nil ||
		result.Observation.Status != "failed" {
		t.Fatalf("result=%+v", result)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, protocol.RecoveryCausePostObservationFailed)
	if result.Observation.Error == nil {
		t.Fatalf("post observation failure must keep its own error")
	}
}

// launchDispatched reports whether a launch already reached the fake device. A
// scripted task read may only diverge from the real one after that dispatch, so
// the pre-launch baseline still comes from the device fact.
func (h *harness) launchDispatched() bool {
	h.device.mu.Lock()
	defer h.device.mu.Unlock()
	return h.device.launchCalls > 0
}

// TestAppOpenWithExistingTaskOnPrimaryDisplay reproduces Issue #39: a same-package
// task that already exists on the primary display (for example right after a
// handoff) must not be mistaken for a second new task when the virtual display
// launches its own window.
func TestAppOpenWithExistingTaskOnPrimaryDisplay(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.tasks = []device.Task{
		{TaskID: 213, RootID: 213, DisplayID: 0, Package: "com.example.app"},
		{TaskID: 212, RootID: 212, DisplayID: 12, Package: "com.other"},
	}
	h.device.launchTask = &device.Task{TaskID: 214, RootID: 214, Package: "com.example.app"}
	h.device.mu.Unlock()

	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome=%q error=%+v app=%+v, want executed",
			result.Outcome, result.Error, result.App)
	}
	if intValue(t, result.App.TaskID, "task_id") != 214 ||
		intValue(t, result.App.DisplayID, "display_id") != 12 {
		t.Fatalf("new virtual task not attributed exactly: %+v", result.App)
	}
	if result.App.Ownership != protocol.TaskOwnershipOwned {
		t.Fatalf("new virtual task must be owned: %+v", result.App)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if len(h.engine.session.Tasks) != 1 || h.engine.session.Tasks[0].TaskID != 214 ||
		h.engine.session.Tasks[0].Ownership != TaskOwned {
		t.Fatalf("registry=%+v", h.engine.session.Tasks)
	}
}

// lateTaskList scripts the task snapshot after the launch dispatch. The
// pre-launch baseline of every app open is a whole-device read, so the scripted
// divergence only applies once the launch already reached the device.
func (h *harness) lateTaskList(late []device.Task) *hookedDevice {
	return &hookedDevice{Device: h.device, tasks: func(ctx context.Context) ([]device.Task, error) {
		if !h.launchDispatched() {
			return h.device.TaskList(ctx)
		}
		return append([]device.Task(nil), late...), nil
	}}
}

// TestAppOpenMovedPrimaryTaskNotOwned keeps the whole-device baseline in charge:
// when the system silently moves the known primary task onto the virtual
// display, no new task exists, so the call stays uncertain and registers
// nothing. The known task can never be adopted as this session's own window.
func TestAppOpenMovedPrimaryTaskNotOwned(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.tasks = []device.Task{
		{TaskID: 213, RootID: 213, DisplayID: 0, Package: "com.example.app"},
		{TaskID: 212, RootID: 212, DisplayID: 12, Package: "com.other"},
	}
	h.device.mu.Unlock()
	h.engine.dev = h.lateTaskList([]device.Task{
		{TaskID: 213, RootID: 213, DisplayID: 12, Package: "com.example.app"},
		{TaskID: 212, RootID: 212, DisplayID: 12, Package: "com.other"},
	})

	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeAppLaunchUnknown {
		t.Fatalf("a moved known task was attributed to this launch: %+v", result)
	}
	if result.App.TaskID != nil || result.App.DisplayID != nil || result.App.Ownership != "" {
		t.Fatalf("uncertain attribution fabricated a landing point: %+v", result.App)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if len(h.engine.session.Tasks) != 0 {
		t.Fatalf("a known task was adopted as owned: %+v", h.engine.session.Tasks)
	}
}

// TestAppOpenNewTaskOnPrimaryReportsMoved proves the moved identity is the task
// this launch created elsewhere, never the same-package task that already
// existed on the primary display.
func TestAppOpenNewTaskOnPrimaryReportsMoved(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.tasks = []device.Task{
		{TaskID: 213, RootID: 213, DisplayID: 0, Package: "com.example.app"},
		{TaskID: 212, RootID: 212, DisplayID: 12, Package: "com.other"},
	}
	h.device.mu.Unlock()
	h.engine.dev = h.lateTaskList([]device.Task{
		{TaskID: 213, RootID: 213, DisplayID: 0, Package: "com.example.app"},
		{TaskID: 212, RootID: 212, DisplayID: 12, Package: "com.other"},
		{TaskID: 215, RootID: 215, DisplayID: 0, Package: "com.example.app"},
	})

	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeAppMoved {
		t.Fatalf("a new task on another display was not reported as moved: %+v", result)
	}
	if result.App.TaskID == nil || *result.App.TaskID != 215 {
		t.Fatalf("moved identity must be the new task 215, never the known 213: %+v", result.App)
	}
	if result.App.DisplayID != nil || result.App.Ownership != "" {
		t.Fatalf("a task on another display is not owned here: %+v", result.App)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if len(h.engine.session.Tasks) != 0 {
		t.Fatalf("a moved task must not be registered: %+v", h.engine.session.Tasks)
	}
}

// TestAppOpenUncertainReconcilesLateTaskAsOwned covers the slow-start fallback:
// the launch result is unknown, yet the post-dispatch read already proves one
// unique new task on the target display. The outcome stays uncertain while the
// proven landing point and a conditional owned registration are still reported.
func TestAppOpenUncertainReconcilesLateTaskAsOwned(t *testing.T) {
	pre := []device.Task{
		{TaskID: 213, RootID: 213, DisplayID: 0, Package: "com.example.app"},
		{TaskID: 212, RootID: 212, DisplayID: 12, Package: "com.other"},
	}
	late := append(append([]device.Task(nil), pre...),
		device.Task{TaskID: 214, RootID: 214, DisplayID: 12, Package: "com.example.app"})
	ambiguous := append(append([]device.Task(nil), pre...),
		device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"},
		device.Task{TaskID: 78, RootID: 78, DisplayID: 12, Package: "com.example.app"})

	cases := []struct {
		name string
		late []device.Task
		// protect installs one unresolved task-transfer responsibility, which
		// makes the conditional registration refuse to adopt the task.
		protect   bool
		wantTask  int // 0 means the identity must stay omitted
		wantOwned bool
	}{
		{name: "unique_late_task", late: late, wantTask: 214, wantOwned: true},
		{name: "protected_task_omits_ownership", late: late, protect: true, wantTask: 214},
		{name: "ambiguous_tasks_fabricate_nothing", late: ambiguous},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			first := h.startSession()
			h.device.mu.Lock()
			h.device.tasks = append([]device.Task(nil), pre...)
			h.device.launchState = device.LaunchUnknown
			h.device.mu.Unlock()
			if tc.protect {
				h.engine.mu.Lock()
				h.engine.transfers = append(h.engine.transfers, &transferState{record: LedgerTransfer{
					OperationID: "Ptransfer", Direction: TransferOut, State: TransferUnknown,
					LeafTaskID: 214, RootTaskID: 214, Package: "com.example.app",
					SourceDisplayID: 12, TargetDisplayID: 0,
				}})
				h.engine.mu.Unlock()
			}
			h.engine.dev = h.lateTaskList(tc.late)

			result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
				appOpenRequest(first.Observation, "com.example.app"))
			if apiErr != nil {
				t.Fatalf("app open: %v", apiErr)
			}
			if result.Outcome != OutcomeUncertain || result.Error == nil ||
				result.Error.Code != protocol.CodeAppLaunchUnknown {
				t.Fatalf("a later proven landing point rewrote the dispatch outcome: %+v", result)
			}
			if result.Observation == nil {
				t.Fatalf("uncertain open must still deliver its post observation")
			}
			if tc.wantTask == 0 {
				if result.App.TaskID != nil || result.App.DisplayID != nil {
					t.Fatalf("ambiguous attribution fabricated an identity: %+v", result.App)
				}
			} else {
				if intValue(t, result.App.TaskID, "task_id") != tc.wantTask ||
					intValue(t, result.App.DisplayID, "display_id") != 12 {
					t.Fatalf("proven landing point was not reported: %+v", result.App)
				}
				if result.App.Reused == nil || *result.App.Reused {
					t.Fatalf("reconciled open must not report reuse: %+v", result.App)
				}
				if !result.App.HasStatePreserved || result.App.StatePreserved == nil ||
					*result.App.StatePreserved {
					t.Fatalf("reconciled open must report explicit state_preserved=false: %+v", result.App)
				}
			}
			if tc.wantOwned != (result.App.Ownership == protocol.TaskOwnershipOwned) {
				t.Fatalf("ownership=%q, want owned=%t", result.App.Ownership, tc.wantOwned)
			}
			h.engine.mu.Lock()
			defer h.engine.mu.Unlock()
			if tc.wantOwned {
				if len(h.engine.session.Tasks) != 1 || h.engine.session.Tasks[0].TaskID != 214 ||
					h.engine.session.Tasks[0].Ownership != TaskOwned {
					t.Fatalf("registry=%+v", h.engine.session.Tasks)
				}
				return
			}
			if len(h.engine.session.Tasks) != 0 {
				t.Fatalf("unregistered task leaked into the registry: %+v", h.engine.session.Tasks)
			}
		})
	}
}

func TestAppOpenPrimaryIsSharedAndNotRegistered(t *testing.T) {
	h := newHarness(t)
	h.syncPrimary()
	first, startErr := h.engine.Start(context.Background(), "agent", "")
	if startErr != nil || first.Outcome != OutcomeExecuted || first.Observation == nil {
		t.Fatalf("primary start failed: %+v %v", first, startErr)
	}
	h.device.launchTask = &device.Task{TaskID: 55, Package: "com.example.app", DisplayID: 0}
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.example.app", TaskID: intPtr(55)}
	h.device.mu.Unlock()
	result, apiErr := h.engine.App(context.Background(), "agent", "Papp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open primary: %v", apiErr)
	}
	if result.App.Ownership != protocol.TaskOwnershipShared ||
		intValue(t, result.App.DisplayID, "display_id") != 0 {
		t.Fatalf("primary open must be shared on display 0: %+v", result.App)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if len(h.engine.session.Tasks) != 0 {
		t.Fatalf("primary open must not register user tasks: %+v", h.engine.session.Tasks)
	}
}
