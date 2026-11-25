package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Contract 31 package lifecycle tests: close eligibility, package/user
// force-stop, result facts and evidence boundaries. The engine tests are owned
// by the main agent and are intentionally black-box through Engine.App.

func v31Task(taskID, displayID int, pkg string) device.Task {
	return device.Task{
		TaskID: taskID, RootID: taskID, DisplayID: displayID, Package: pkg,
		Component: pkg + "/.Main", UserID: 0, HasUserID: true, Visible: true,
	}
}

func v31KillTask(o *protocol.Observation, taskID int) *protocol.AppRequest {
	return &protocol.AppRequest{Kind: protocol.AppKindKill, Reference: o.ID,
		Target: &protocol.AppTarget{TaskID: taskID, UseTask: true}}
}

func v31KillApp(o *protocol.Observation, pkg string) *protocol.AppRequest {
	return &protocol.AppRequest{Kind: protocol.AppKindKill, Reference: o.ID,
		Target: &protocol.AppTarget{App: pkg}}
}

func v31CloseTask(o *protocol.Observation, taskID int) *protocol.AppRequest {
	return &protocol.AppRequest{Kind: protocol.AppKindClose, Reference: o.ID,
		Target: &protocol.AppTarget{TaskID: taskID, UseTask: true}}
}

func (h *harness) callApp(p string, req *protocol.AppRequest) *protocol.Result {
	h.t.Helper()
	result, apiErr := h.engine.App(context.Background(), "agent", p, req)
	if apiErr != nil {
		h.t.Fatalf("app %s returned admission error: %v", req.Kind, apiErr)
	}
	return result
}

func (h *harness) observationStatus(id string) string {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	obs := h.engine.observations[id]
	if obs == nil {
		return ""
	}
	return obs.Status
}

func (h *harness) closeEligibilities() []closeEligibility {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	return append([]closeEligibility(nil), h.engine.session.CloseEligibilities...)
}

func (h *harness) forceStops() []forceStopCall {
	h.device.mu.Lock()
	defer h.device.mu.Unlock()
	return append([]forceStopCall(nil), h.device.forceStopCalls...)
}

func requireAppField(t *testing.T, view *protocol.AppView, name string) {
	t.Helper()
	if view == nil {
		t.Fatalf("%s: app view is nil", name)
	}
}

func intValues(values []int) []int { return append([]int(nil), values...) }

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAppKillTaskEntryWholePackageFacts proves one task entry force-stops the
// whole user/package once and reports every proven fact.
func TestAppKillTaskEntryWholePackageFacts(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
		{PID: 501, StartTicks: 12, Name: "com.example.app:remote"},
	}}
	h.device.mu.Unlock()

	result := h.callApp("Pkillfacts", v31KillTask(first.Observation, 77))
	app := result.App
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome=%q error=%+v", result.Outcome, result.Error)
	}
	if app.Package != "com.example.app" ||
		app.AndroidUserID == nil || *app.AndroidUserID != 0 ||
		app.SourceTaskID == nil || *app.SourceTaskID != 77 ||
		!app.HasTargetTaskIDs || !equalInts(app.TargetTaskIDs, []int{77}) ||
		!app.HasRemainingTaskIDs || !equalInts(app.RemainingTaskIDs, []int{}) ||
		app.DisplayID == nil || *app.DisplayID != 12 ||
		!app.HasTargetPIDs || !equalInts(app.TargetPIDs, []int{500, 501}) ||
		!app.HasKilledPIDs || !equalInts(app.KilledPIDs, []int{500, 501}) ||
		!app.HasRemainingPIDs || !equalInts(app.RemainingPIDs, []int{}) ||
		app.Restarted == nil || *app.Restarted ||
		app.PackageState != "stopped_at_check" {
		t.Fatalf("kill app=%+v", app)
	}
	stops := h.forceStops()
	if len(stops) != 1 || stops[0].pkg != "com.example.app" || stops[0].userID != 0 {
		t.Fatalf("force-stop calls=%+v", stops)
	}
	h.device.mu.Lock()
	removes, signals := len(h.device.removeCalls), len(h.device.killCalls)
	h.device.mu.Unlock()
	if removes != 0 || signals != 0 {
		t.Fatalf("package force-stop dispatched removes=%d signals=%d", removes, signals)
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 77)
	eligibilities := len(h.engine.session.CloseEligibilities)
	h.engine.mu.Unlock()
	if entry != nil || eligibilities != 0 {
		t.Fatalf("registration=%+v eligibilities=%d", entry, eligibilities)
	}
}

// TestAppKillPackageEntryMultipleOwnedWindows proves an all-owned multi-window
// package is frozen as one set and force-stopped once.
func TestAppKillPackageEntryMultipleOwnedWindows(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	one, two := v31Task(77, 12, "com.example.app"), v31Task(78, 12, "com.example.app")
	h.addTask(one)
	h.addTask(two)
	h.ownTask(one, TaskOwned, nil)
	h.ownTask(two, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.mu.Unlock()

	result := h.callApp("Pkillmulti", v31KillApp(first.Observation, "com.example.app"))
	if result.Outcome != OutcomeExecuted || result.Error != nil ||
		!result.App.HasTargetTaskIDs || !equalInts(result.App.TargetTaskIDs, []int{77, 78}) {
		t.Fatalf("multi-window kill=%+v app=%+v", result, result.App)
	}
	if result.App.SourceTaskID != nil {
		t.Fatalf("package entry must omit source_task_id: %+v", result.App)
	}
	if stops := h.forceStops(); len(stops) != 1 {
		t.Fatalf("force-stop calls=%+v", stops)
	}
}

// TestAppCloseThenKillPackageUsesEligibility proves close establishes a
// short-lived package authorization that a no-task package kill consumes.
func TestAppCloseThenKillPackageUsesEligibility(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.recentIDs = map[int]bool{}
	h.device.recentKnown = true
	h.device.processes = device.PackageProcesses{AppUID: 10123}
	h.device.mu.Unlock()

	closed := h.callApp("Pclose1", v31CloseTask(first.Observation, 77))
	if closed.Outcome != OutcomeExecuted || closed.Observation == nil {
		t.Fatalf("close=%+v", closed)
	}
	records := h.closeEligibilities()
	if len(records) != 1 || records[0].Package != "com.example.app" ||
		records[0].AndroidUserID != 0 || records[0].AppID != 10123 {
		t.Fatalf("eligibility=%+v", records)
	}
	// The application kept running in the background after its window closed.
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 700, StartTicks: 21, Name: "com.example.app"},
	}}
	h.device.mu.Unlock()

	result := h.callApp("Pkillpkg", v31KillApp(closed.Observation, "com.example.app"))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("package kill=%+v", result)
	}
	app := result.App
	if app.SourceTaskID != nil || !app.HasTargetTaskIDs || !equalInts(app.TargetTaskIDs, []int{}) ||
		app.AndroidUserID == nil || *app.AndroidUserID != 0 ||
		!app.HasTargetPIDs || !equalInts(app.TargetPIDs, []int{700}) ||
		!app.HasKilledPIDs || !equalInts(app.KilledPIDs, []int{700}) ||
		!app.HasRemainingTaskIDs || !equalInts(app.RemainingTaskIDs, []int{}) ||
		app.PackageState != "stopped_at_check" {
		t.Fatalf("package kill app=%+v", app)
	}
	if stops := h.forceStops(); len(stops) != 1 {
		t.Fatalf("force-stop calls=%+v", stops)
	}
	if records := h.closeEligibilities(); len(records) != 0 {
		t.Fatalf("consumed eligibility remains: %+v", records)
	}
}

// TestAppKillEligibilityExpiresAndIsNotRenewedByQueries proves the five-minute
// monotonic window and that read-only queries never extend it.
func TestAppKillEligibilityExpiresAndIsNotRenewedByQueries(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.recentIDs = map[int]bool{}
	h.device.recentKnown = true
	h.device.mu.Unlock()
	closed := h.callApp("Pcloseexpiry", v31CloseTask(first.Observation, 77))
	if len(h.closeEligibilities()) != 1 {
		t.Fatalf("close did not establish eligibility")
	}
	before := h.closeEligibilities()[0].ClosedAt

	// A list and a find are reads: neither may extend the close window.
	_ = h.callApp("Plistx", &protocol.AppRequest{Kind: protocol.AppKindList,
		Reference: closed.Observation.ID})
	h.device.mu.Lock()
	h.device.appEntries = []device.AppEntry{{Name: "Example", Package: "com.example.app"}}
	h.device.mu.Unlock()
	_ = h.callApp("Pfindx", &protocol.AppRequest{Kind: protocol.AppKindFind,
		Query: "example", HasQuery: true})
	after := h.closeEligibilities()[0].ClosedAt
	if !after.Equal(before) {
		t.Fatalf("a query renewed the close eligibility: %v != %v", before, after)
	}

	h.engine.mu.Lock()
	h.engine.session.CloseEligibilities[0].ClosedAt = time.Now().Add(-closeEligibilityTTL - time.Second)
	h.engine.mu.Unlock()
	result := h.callApp("Pkillexpired", v31KillApp(closed.Observation, "com.example.app"))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeKillNotOwned {
		t.Fatalf("expired eligibility result=%+v", result)
	}
	if stops := h.forceStops(); len(stops) != 0 {
		t.Fatalf("expired eligibility dispatched %+v", stops)
	}
	if status := h.observationStatus(closed.Observation.ID); status != ObservationReady {
		t.Fatalf("pre-claim refusal consumed the observation: %q", status)
	}
}

// TestAppKillEligibilityBindingInvalidation proves generation, session and
// installation identity changes revoke the package authorization.
func TestAppKillEligibilityBindingInvalidation(t *testing.T) {
	newEligible := func(t *testing.T) (*harness, *protocol.Result) {
		t.Helper()
		h := newHarness(t)
		first := h.startSession()
		task := v31Task(77, 12, "com.example.app")
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
		h.device.mu.Lock()
		h.device.recentIDs = map[int]bool{}
		h.device.recentKnown = true
		h.device.mu.Unlock()
		closed := h.callApp("Pclosebind", v31CloseTask(first.Observation, 77))
		if len(h.closeEligibilities()) != 1 {
			t.Fatalf("close did not establish eligibility")
		}
		return h, closed
	}

	t.Run("display generation change", func(t *testing.T) {
		h, closed := newEligible(t)
		h.engine.mu.Lock()
		h.engine.session.Display.Generation = "44444444-4444-4444-8444-444444444444"
		h.engine.mu.Unlock()
		result := h.callApp("Pkillgeneration", v31KillApp(closed.Observation, "com.example.app"))
		if result.Error == nil || result.Error.Code != protocol.CodeObservationRetired {
			t.Fatalf("generation result=%+v code=%+v", result, result.Error)
		}
		if len(h.closeEligibilities()) != 0 {
			t.Fatalf("stale generation kept the record")
		}
		if stops := h.forceStops(); len(stops) != 0 {
			t.Fatalf("stale generation dispatched %+v", stops)
		}
	})

	t.Run("session revoke clears", func(t *testing.T) {
		h, _ := newEligible(t)
		h.engine.mu.Lock()
		h.engine.revokeSessionLocked("user_stop", time.Now())
		h.engine.mu.Unlock()
		if records := h.closeEligibilities(); len(records) != 0 {
			t.Fatalf("revoked session kept eligibility: %+v", records)
		}
	})

	t.Run("installation signature change", func(t *testing.T) {
		h, closed := newEligible(t)
		h.device.mu.Lock()
		h.device.packageIdentity = &device.PackageIdentity{
			Known: true, Package: "com.example.app", AndroidUserID: 0,
			AppID: 10123, AppUID: 10123,
			FirstInstallTime: "2026-09-01 10:00:00", SignatureHash: "00000000",
		}
		h.device.mu.Unlock()
		result := h.callApp("Pkillsignature", v31KillApp(closed.Observation, "com.example.app"))
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeKillNotOwned {
			t.Fatalf("changed identity result=%+v", result)
		}
		if len(h.closeEligibilities()) != 0 {
			t.Fatalf("changed identity kept the record")
		}
		if stops := h.forceStops(); len(stops) != 0 {
			t.Fatalf("changed identity dispatched %+v", stops)
		}
	})

	t.Run("installation identity unknown", func(t *testing.T) {
		h, closed := newEligible(t)
		h.device.mu.Lock()
		h.device.packageIdentityUnknown = true
		h.device.mu.Unlock()
		result := h.callApp("Pkillunknownidentity", v31KillApp(closed.Observation, "com.example.app"))
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeDeviceUnavailable {
			t.Fatalf("unknown identity result=%+v", result)
		}
		if status := h.observationStatus(closed.Observation.ID); status != ObservationReady {
			t.Fatalf("pre-claim identity refusal consumed the observation: %q", status)
		}
		if stops := h.forceStops(); len(stops) != 0 {
			t.Fatalf("unknown identity dispatched %+v", stops)
		}
	})
}

// TestAppKillEligibilityRevokedByExternalUse proves a primary-display task of
// the same package after close is external use: it refuses and revokes.
func TestAppKillEligibilityRevokedByExternalUse(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.recentIDs = map[int]bool{}
	h.device.recentKnown = true
	h.device.mu.Unlock()
	closed := h.callApp("Pcloseexternal", v31CloseTask(first.Observation, 77))
	if len(h.closeEligibilities()) != 1 {
		t.Fatalf("close did not establish eligibility")
	}
	// The user reopened the application on the primary display.
	h.addTask(v31Task(90, 0, "com.example.app"))

	result := h.callApp("Pkillexternal", v31KillApp(closed.Observation, "com.example.app"))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeKillTaskElsewhere {
		t.Fatalf("external use result=%+v", result)
	}
	if len(h.closeEligibilities()) != 0 {
		t.Fatalf("external use kept the eligibility")
	}
	if stops := h.forceStops(); len(stops) != 0 {
		t.Fatalf("external use dispatched %+v", stops)
	}
	if status := h.observationStatus(closed.Observation.ID); status != ObservationReady {
		t.Fatalf("pre-claim refusal consumed the observation: %q", status)
	}
}

// TestAppKillHandoffRevokesEligibility proves handoff drops the package
// authorization with the ownership registration.
func TestAppKillHandoffRevokesEligibility(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	closedTask := v31Task(77, 12, "com.example.app")
	handoffTask := v31Task(78, 12, "com.example.app")
	h.addTask(closedTask)
	h.addTask(handoffTask)
	h.ownTask(closedTask, TaskOwned, nil)
	h.ownTask(handoffTask, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.recentIDs = map[int]bool{}
	h.device.recentKnown = true
	h.device.mu.Unlock()
	closed := h.callApp("Pclosehandoff", v31CloseTask(first.Observation, 77))
	if len(h.closeEligibilities()) != 1 {
		t.Fatalf("close did not establish eligibility")
	}
	handoff := h.callApp("Phandoffrev", &protocol.AppRequest{
		Kind: protocol.AppKindHandoff, Reference: closed.Observation.ID,
		Target: &protocol.AppTarget{TaskID: 78, UseTask: true},
	})
	if handoff.Outcome != OutcomeExecuted {
		t.Fatalf("handoff=%+v", handoff)
	}
	if records := h.closeEligibilities(); len(records) != 0 {
		t.Fatalf("handoff kept eligibility: %+v", records)
	}
	result := h.callApp("Pkillafterhandoff", v31KillApp(handoff.Observation, "com.example.app"))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeKillTaskElsewhere {
		t.Fatalf("post-handoff package kill=%+v", result)
	}
	if stops := h.forceStops(); len(stops) != 0 {
		t.Fatalf("post-handoff kill dispatched %+v", stops)
	}
}

// TestAppKillRefusalsKeepObservation proves ownership, task and user evidence
// failures never dispatch and preserve the ready observation before claim.
func TestAppKillRefusalsKeepObservation(t *testing.T) {
	t.Run("no task and no eligibility", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		result := h.callApp("Pkillnone", v31KillApp(first.Observation, "com.example.app"))
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeKillNotOwned {
			t.Fatalf("no-task result=%+v", result)
		}
		if status := h.observationStatus(first.Observation.ID); status != ObservationReady {
			t.Fatalf("pre-claim refusal consumed O: %q", status)
		}
	})

	t.Run("borrowed sibling", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		owned, borrowed := v31Task(77, 12, "com.example.app"), v31Task(78, 12, "com.example.app")
		h.addTask(owned)
		h.addTask(borrowed)
		h.ownTask(owned, TaskOwned, nil)
		h.ownTask(borrowed, TaskBorrowed, intPtr(0))
		result := h.callApp("Pkillborrowed", v31KillTask(first.Observation, 77))
		if result.Error == nil || result.Error.Code != protocol.CodeKillNotOwned {
			t.Fatalf("borrowed sibling result=%+v", result)
		}
		if status := h.observationStatus(first.Observation.ID); status != ObservationReady {
			t.Fatalf("pre-claim refusal consumed O: %q", status)
		}
	})

	t.Run("unknown task user", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		task := v31Task(77, 12, "com.example.app")
		task.HasUserID = false
		h.device.mu.Lock()
		h.device.tasks = append(h.device.tasks, task)
		h.device.mu.Unlock()
		h.ownTask(task, TaskOwned, nil)
		result := h.callApp("Pkillnouser", v31KillTask(first.Observation, 77))
		if result.Error == nil || result.Error.Code != protocol.CodeDeviceUnavailable {
			t.Fatalf("unknown user result=%+v", result)
		}
		if status := h.observationStatus(first.Observation.ID); status != ObservationReady {
			t.Fatalf("pre-claim refusal consumed O: %q", status)
		}
	})

	t.Run("task not found", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		result := h.callApp("Pkillmissing", v31KillTask(first.Observation, 999))
		if result.Error == nil || result.Error.Code != protocol.CodeTaskNotFound {
			t.Fatalf("missing task result=%+v", result)
		}
	})
}

// TestAppKillStopBeforeDispatchNeverSignals proves a STOP in the last
// pre-dispatch window keeps the call not_executed and never sends force-stop.
func TestAppKillStopBeforeDispatchNeverSignals(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.processHook = func(call int) {
		if call != 2 {
			return
		}
		h.engine.mu.Lock()
		h.engine.revokeSessionLocked("user_stop", time.Now())
		h.engine.mu.Unlock()
	}
	h.device.mu.Unlock()

	result := h.callApp("Pkillstop", v31KillTask(first.Observation, 77))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeStopped {
		t.Fatalf("stopped kill result=%+v", result)
	}
	if stops := h.forceStops(); len(stops) != 0 {
		t.Fatalf("stopped kill dispatched %+v", stops)
	}
}

// TestAppKillUnconfirmedReceiptStaysHonest proves an unknown or failed
// force-stop command is uncertain and is never dispatched twice.
func TestAppKillUnconfirmedReceiptStaysHonest(t *testing.T) {
	newRunning := func(t *testing.T) (*harness, *protocol.Observation) {
		t.Helper()
		h := newHarness(t)
		first := h.startSession()
		task := v31Task(77, 12, "com.example.app")
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
		h.device.mu.Lock()
		h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
			{PID: 500, StartTicks: 11, Name: "com.example.app"},
		}}
		h.device.mu.Unlock()
		return h, first.Observation
	}

	t.Run("unknown receipt", func(t *testing.T) {
		h, observation := newRunning(t)
		unknown := device.DispatchUnknown
		h.device.mu.Lock()
		h.device.forceStopReceipt = &unknown
		h.device.mu.Unlock()
		result := h.callApp("Pkillunknownreceipt", v31KillTask(observation, 77))
		if result.Outcome != OutcomeUncertain || result.Error == nil ||
			result.Error.Code != protocol.CodeKillUnconfirmed {
			t.Fatalf("unknown receipt result=%+v", result)
		}
		if stops := h.forceStops(); len(stops) != 1 {
			t.Fatalf("unknown receipt calls=%+v", stops)
		}
		if !result.App.HasRemainingTaskIDs || !equalInts(result.App.RemainingTaskIDs, []int{77}) ||
			!result.App.HasRemainingPIDs || !equalInts(result.App.RemainingPIDs, []int{500}) ||
			!result.App.HasKilledPIDs || len(result.App.KilledPIDs) != 0 ||
			result.App.Restarted == nil || *result.App.Restarted ||
			result.App.PackageState != "still_present" {
			t.Fatalf("unknown receipt app=%+v", result.App)
		}
		h.engine.mu.Lock()
		entry := h.engine.sessionTaskLocked(h.engine.session, 77)
		h.engine.mu.Unlock()
		if entry == nil {
			t.Fatalf("uncertain kill dropped the registration")
		}
	})

	t.Run("non-zero exit", func(t *testing.T) {
		h, observation := newRunning(t)
		h.device.mu.Lock()
		h.device.forceStopExitCodeSet = true
		h.device.forceStopExitCode = 1
		h.device.mu.Unlock()
		result := h.callApp("Pkillexit1", v31KillTask(observation, 77))
		if result.Outcome != OutcomeUncertain || result.Error == nil ||
			result.Error.Code != protocol.CodeKillUnconfirmed {
			t.Fatalf("non-zero exit result=%+v", result)
		}
		if stops := h.forceStops(); len(stops) != 1 {
			t.Fatalf("non-zero exit calls=%+v", stops)
		}
	})

	t.Run("not dispatched", func(t *testing.T) {
		h, observation := newRunning(t)
		notDispatched := device.DispatchNotDispatched
		h.device.mu.Lock()
		h.device.forceStopReceipt = &notDispatched
		h.device.processes = device.PackageProcesses{AppUID: 10123}
		h.device.mu.Unlock()
		result := h.callApp("Pkillnotdispatched", v31KillTask(observation, 77))
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeStopped {
			t.Fatalf("not dispatched result=%+v", result)
		}
		app := result.App
		if app.HasKilledPIDs || app.HasRemainingPIDs || app.HasRestarted ||
			app.PackageState != "" {
			t.Fatalf("not_executed leaked later facts: %+v", app)
		}
		if stops := h.forceStops(); len(stops) != 1 {
			t.Fatalf("not dispatched calls=%+v", stops)
		}
	})
}

// TestAppKillEmptyOriginalProcessesDispatchesOnce covers the contract-31 change:
// an empty original process set still gets the one force-stop, and the final
// facts come from the sample rather than a prefill.
func TestAppKillEmptyOriginalProcessesDispatchesOnce(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{}}
	h.device.mu.Unlock()

	result := h.callApp("Pkillemptyorig", v31KillTask(first.Observation, 77))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("empty original result=%+v", result)
	}
	app := result.App
	if !app.HasTargetPIDs || !equalInts(app.TargetPIDs, []int{}) ||
		!app.HasKilledPIDs || !equalInts(app.KilledPIDs, []int{}) ||
		!app.HasRemainingPIDs || !equalInts(app.RemainingPIDs, []int{}) ||
		app.Restarted == nil || *app.Restarted ||
		app.PackageState != "stopped_at_check" {
		t.Fatalf("empty original app=%+v", app)
	}
	if stops := h.forceStops(); len(stops) != 1 {
		t.Fatalf("empty original dispatched %d force-stops", len(stops))
	}
}

// TestAppKillFinalReadUnknownKeepsCommandFact proves a failed post read never
// rewrites the confirmed command and reports the unknown sample honestly.
func TestAppKillFinalReadUnknownKeepsCommandFact(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	knownTask := []device.Task{task}
	h.device.taskSnapshotScript = []taskSnapshotStep{
		{tasks: knownTask, complete: true},
		{tasks: knownTask, complete: true},
		{tasks: knownTask, complete: true},
		{err: context.DeadlineExceeded},
		{err: context.DeadlineExceeded},
		{err: context.DeadlineExceeded},
		{err: context.DeadlineExceeded},
	}
	knownProcess := device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.processScript = []processStep{
		{processes: knownProcess},
		{processes: knownProcess},
		{fail: true},
		{fail: true},
		{fail: true},
		{fail: true},
	}
	h.device.mu.Unlock()

	result := h.callApp("Pkillpostunknown", v31KillTask(first.Observation, 77))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("post-read unknown result=%+v code=%+v phase=%q", result, result.Error, result.Phase)
	}
	app := result.App
	if app.HasRemainingTaskIDs || app.HasKilledPIDs || app.HasRemainingPIDs ||
		!app.HasRestarted || app.Restarted != nil ||
		app.PackageState != "unknown" {
		t.Fatalf("post-read unknown app=%+v", app)
	}
	if stops := h.forceStops(); len(stops) != 1 {
		t.Fatalf("post-read unknown calls=%+v", stops)
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 77)
	h.engine.mu.Unlock()
	if entry == nil {
		t.Fatalf("unknown final task read dropped the registration")
	}
}

// TestAppKillStillPresentAndRestarted proves the sample distinguishes a
// surviving window/process and a fresh instance from a stopped package.
func TestAppKillStillPresentAndRestarted(t *testing.T) {
	t.Run("survivors", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		task := v31Task(77, 12, "com.example.app")
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
		h.device.mu.Lock()
		h.device.forceStopNoop = true
		h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
			{PID: 500, StartTicks: 11, Name: "com.example.app"},
		}}
		h.device.mu.Unlock()
		result := h.callApp("Pkillsurvivor", v31KillTask(first.Observation, 77))
		if result.Outcome != OutcomeExecuted ||
			!result.App.HasRemainingTaskIDs || !equalInts(result.App.RemainingTaskIDs, []int{77}) ||
			!result.App.HasRemainingPIDs || !equalInts(result.App.RemainingPIDs, []int{500}) ||
			!result.App.HasKilledPIDs || len(result.App.KilledPIDs) != 0 ||
			result.App.Restarted == nil || *result.App.Restarted ||
			result.App.PackageState != "still_present" {
			t.Fatalf("survivor app=%+v", result.App)
		}
	})

	t.Run("restarted", func(t *testing.T) {
		h := newHarness(t)
		first := h.startSession()
		task := v31Task(77, 12, "com.example.app")
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
		h.device.mu.Lock()
		h.device.forceStopNoop = true
		h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
			{PID: 500, StartTicks: 11, Name: "com.example.app"},
		}}
		h.device.forceStopHook = func() {
			h.device.mu.Lock()
			h.device.processes.Instances = []device.ProcessInstance{
				{PID: 600, StartTicks: 99, Name: "com.example.app"},
			}
			h.device.mu.Unlock()
		}
		h.device.mu.Unlock()
		result := h.callApp("Pkillrestart", v31KillTask(first.Observation, 77))
		if result.Outcome != OutcomeExecuted ||
			!result.App.HasKilledPIDs || !equalInts(result.App.KilledPIDs, []int{500}) ||
			!result.App.HasRemainingPIDs || !equalInts(result.App.RemainingPIDs, []int{600}) ||
			result.App.Restarted == nil || !*result.App.Restarted ||
			result.App.PackageState != "still_present" {
			t.Fatalf("restarted app=%+v", result.App)
		}
	})
}

// TestAppKillStopAfterDispatchStaysUncertain proves an already dispatched
// force-stop is never rewritten to not_executed and never waits for a post read.
func TestAppKillStopAfterDispatchStaysUncertain(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.forceStopHook = func() {
		h.engine.mu.Lock()
		h.engine.revokeSessionLocked("user_stop", time.Now())
		h.engine.mu.Unlock()
	}
	h.device.mu.Unlock()

	result := h.callApp("Pkillstopafter", v31KillTask(first.Observation, 77))
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeKillUnconfirmed {
		t.Fatalf("post-dispatch STOP result=%+v", result)
	}
	if result.Observation != nil {
		t.Fatalf("a revoked session must not publish a post observation")
	}
	if stops := h.forceStops(); len(stops) != 1 {
		t.Fatalf("post-dispatch STOP calls=%+v", stops)
	}
}

// TestAppKillRevalidationRefusesChangedTaskSet proves the second guard refuses
// a changed task snapshot before dispatch.
func TestAppKillRevalidationRefusesChangedTaskSet(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.taskSnapshotScript = []taskSnapshotStep{
		{tasks: []device.Task{task}, complete: true},
		{tasks: []device.Task{task}, complete: true},
		{tasks: []device.Task{}, complete: true},
	}
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.mu.Unlock()

	result := h.callApp("Pkilltaskset", v31KillTask(first.Observation, 77))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeTaskNotTracked {
		t.Fatalf("changed task set result=%+v", result)
	}
	if status := h.observationStatus(first.Observation.ID); status == ObservationReady {
		t.Fatalf("post-claim refusal left the observation ready: %q", status)
	}
	if stops := h.forceStops(); len(stops) != 0 {
		t.Fatalf("changed task set dispatched %+v", stops)
	}
}

// TestAppKillRevalidationRefusesChangedIdentity proves an installation change
// between the pre-claim and pre-dispatch guards is not executed.
func TestAppKillRevalidationRefusesChangedIdentity(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := v31Task(77, 12, "com.example.app")
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	original := device.PackageIdentity{
		Known: true, Package: "com.example.app", AndroidUserID: 0,
		AppID: 10123, AppUID: 10123,
		FirstInstallTime: "2026-08-04 19:53:47", SignatureHash: "8f53736c",
	}
	changed := original
	changed.FirstInstallTime = "2026-09-02 10:00:00"
	changed.SignatureHash = "11111111"
	h.device.packageIdentityScript = []device.PackageIdentity{original, changed}
	h.device.processes = device.PackageProcesses{AppUID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.mu.Unlock()

	result := h.callApp("Pkillidentitychange", v31KillTask(first.Observation, 77))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeDeviceUnavailable {
		t.Fatalf("changed identity result=%+v", result)
	}
	if stops := h.forceStops(); len(stops) != 0 {
		t.Fatalf("changed identity dispatched %+v", stops)
	}
}

// TestCloseEligibilityBoundsAndMerging pins the bounded in-memory store.
func TestCloseEligibilityBoundsAndMerging(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	base := time.Now()
	h.engine.mu.Lock()
	for i := 0; i < maxCloseEligibilities; i++ {
		addCloseEligibilityLocked(h.engine.session, closeEligibility{
			Package:       "com.example.p" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			AndroidUserID: 0, DisplayID: 12,
			DisplayGeneration: h.engine.session.Display.Generation,
			SessionEpoch:      h.engine.session.Epoch,
			ClosedAt:          base.Add(time.Duration(i) * time.Second),
		})
	}
	if len(h.engine.session.CloseEligibilities) != maxCloseEligibilities {
		t.Fatalf("records=%d", len(h.engine.session.CloseEligibilities))
	}
	oldest := h.engine.session.CloseEligibilities[0].Package
	addCloseEligibilityLocked(h.engine.session, closeEligibility{
		Package: "com.example.new", AndroidUserID: 0, DisplayID: 12,
		DisplayGeneration: h.engine.session.Display.Generation,
		SessionEpoch:      h.engine.session.Epoch, ClosedAt: base.Add(time.Hour),
	})
	if len(h.engine.session.CloseEligibilities) != maxCloseEligibilities {
		t.Fatalf("eviction did not bound the store")
	}
	if record := closeEligibilityLocked(h.engine.session, oldest, base); record != nil {
		t.Fatalf("oldest record survived eviction: %+v", record)
	}
	if record := closeEligibilityLocked(h.engine.session, "com.example.new", base); record == nil {
		t.Fatalf("new record is missing")
	}
	// A second close of the same package/user replaces the window.
	addCloseEligibilityLocked(h.engine.session, closeEligibility{
		Package: "com.example.new", AndroidUserID: 0, DisplayID: 12,
		DisplayGeneration: h.engine.session.Display.Generation,
		SessionEpoch:      h.engine.session.Epoch, ClosedAt: base.Add(3 * time.Hour),
	})
	h.engine.mu.Unlock()
	records := h.closeEligibilities()
	found := 0
	for _, record := range records {
		if record.Package == "com.example.new" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("duplicate package records=%d", found)
	}
}
