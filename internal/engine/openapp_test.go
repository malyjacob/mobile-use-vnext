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
	requireRecovery(t, result, protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
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
