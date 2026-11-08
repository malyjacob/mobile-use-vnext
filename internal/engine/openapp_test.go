package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func openAppRequest(o *protocol.Observation, app string) *protocol.OpenAppRequest {
	return &protocol.OpenAppRequest{ObservationID: o.ID, App: app}
}

func TestOpenAppCreatesTaskOnVirtualDisplay(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("OpenApp: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.App == nil {
		t.Fatalf("result=%+v", result)
	}
	if result.App.TaskID != 77 || result.App.DisplayID != 12 || result.App.Reused {
		t.Fatalf("app=%+v", result.App)
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
}

func TestOpenAppReusesOwnLiveTask(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.tasks = append(h.device.tasks, device.Task{TaskID: 77, DisplayID: 12, Package: "com.example.app"})
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.example.app", TaskID: intPtr(77)}
	h.device.mu.Unlock()

	result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("OpenApp: %v", apiErr)
	}
	if result.App == nil || !result.App.Reused || result.App.TaskID != 77 {
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

func TestOpenAppUnknownAppDoesNotConsumeObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.resolveMissing = true
	h.device.mu.Unlock()
	result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.missing"))
	if apiErr != nil {
		t.Fatalf("OpenApp: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeAppNotFound ||
		result.Outcome != OutcomeNotExecuted || result.Next != "user" {
		t.Fatalf("result=%+v", result)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status != ObservationReady {
		t.Fatalf("unknown app must not consume O: %+v", h.engine.observations[first.Observation.ID])
	}
	if h.device.launchCalls != 0 {
		t.Fatalf("launch dispatched for an unknown app")
	}
}

func TestOpenAppControlPackageOnVirtualDisplayRejected(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, h.engine.cfg.ControlPackage))
	if apiErr != nil {
		t.Fatalf("OpenApp: %v", apiErr)
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

func TestOpenAppLaunchRejectedConsumesObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.launchState = device.LaunchRejected
	h.device.mu.Unlock()
	result, _ := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.app"))
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

func TestOpenAppLaunchUnknownIsUncertain(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.launchState = device.LaunchUnknown
	h.device.mu.Unlock()
	result, _ := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.app"))
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeAppLaunchUnknown || result.Next != "status" {
		t.Fatalf("result=%+v", result)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExecutionUnknown {
		t.Fatalf("uncertain launch must keep execution unknown")
	}
}

func TestOpenAppPostObservationFailureKeepsExecuted(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	h.device.mu.Lock()
	h.device.captureErrs = true
	h.device.mu.Unlock()
	result, _ := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.app"))
	if result.Outcome != OutcomeExecuted || result.Observation == nil ||
		result.Observation.Status != "failed" || result.Next != "observe" {
		t.Fatalf("result=%+v", result)
	}
	if result.Observation.Error == nil {
		t.Fatalf("post observation failure must keep its own error")
	}
}
