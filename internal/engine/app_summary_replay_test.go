package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func requireAppSummary(t *testing.T, h *harness, id string, req *protocol.AppRequest, state, phase, outcome string, want *protocol.AppView) {
	t.Helper()
	captures := h.device.captureCount()
	status, apiErr := h.engine.Status("agent", id)
	if apiErr != nil {
		t.Fatalf("status(%s): %v", id, apiErr)
	}
	for _, result := range []*protocol.Result{status, h.callApp(id, req)} {
		if result.OperationID != id || result.OperationState != state || state == OperationFinished && result.Phase != phase ||
			result.Outcome != outcome || result.Observation != nil || !reflect.DeepEqual(result.App, want) {
			t.Fatalf("replay of %s: %+v, app=%+v, want %s/%s/%s, app=%+v", id, result, result.App, state, phase, outcome, want)
		}
	}
	if h.device.captureCount() != captures {
		t.Fatal("app replay captured an observation")
	}
}

func requireAppReplayIsolation(t *testing.T, h *harness, req *protocol.AppRequest, first *protocol.Result, phase string, mutate func(*protocol.AppView)) {
	t.Helper()
	id := first.OperationID
	status, apiErr := h.engine.Status("agent", id)
	if apiErr != nil {
		t.Fatalf("status(%s): %v", id, apiErr)
	}
	duplicate := h.callApp(id, req)
	requireAppSummary(t, h, id, req, OperationFinished, phase, first.Outcome, first.App)
	want, err := protocol.JSON(first.App)
	if err != nil {
		t.Fatal(err)
	}
	assertUnchanged := func(view *protocol.AppView) {
		t.Helper()
		got, err := protocol.JSON(view)
		if err != nil || string(got) != string(want) {
			t.Fatalf("mutating another response changed %s: got %s, want %s, err=%v", id, got, want, err)
		}
	}
	mutate(first.App)
	assertUnchanged(status.App)
	assertUnchanged(duplicate.App)
	mutate(status.App)
	assertUnchanged(duplicate.App)
	mutate(duplicate.App)
	fresh, apiErr := h.engine.Status("agent", id)
	if apiErr != nil {
		t.Fatalf("status(%s) after mutation: %v", id, apiErr)
	}
	assertUnchanged(fresh.App)
}

func requireAppRetryParameter(t *testing.T, h *harness, req *protocol.AppRequest, first *protocol.Result, want string) {
	t.Helper()
	assertParameter := func(result *protocol.Result) {
		t.Helper()
		if result.Error == nil || result.Error.Code != first.Error.Code {
			t.Fatalf("expected %s, got %+v", first.Error.Code, result.Error)
		}
		value, present := result.Error.Details["retry_parameter"]
		if present != (want != "") || present && value != want {
			t.Fatalf("retry_parameter = %v (present=%t), want %q", value, present, want)
		}
	}
	assertParameter(first)
	status, apiErr := h.engine.Status("agent", first.OperationID)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	assertParameter(status)
	duplicate := h.callApp(first.OperationID, req)
	assertParameter(duplicate)
	if want != "" {
		first.Error.Details["retry_parameter"] = "changed"
		assertParameter(status)
		status.Error.Details["retry_parameter"] = "changed"
		assertParameter(duplicate)
		duplicate.Error.Details["retry_parameter"] = "changed"
		assertParameter(h.callApp(first.OperationID, req))
	}
}

func TestAppOpenReplaysReconciledUncertainFacts(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.launchState = device.LaunchUnknown
	late := append([]device.Task(nil), h.device.tasks...)
	h.device.mu.Unlock()
	late = append(late, v31Task(214, 12, "com.example.app"))
	h.engine.dev = h.lateTaskList(late)
	req := appOpenRequest(first.Observation, "com.example.app")
	result := h.callApp("Popenreconciled", req)
	if result.Outcome != OutcomeUncertain || result.Observation == nil || result.App.Ownership != TaskOwned ||
		intValue(t, result.App.TaskID, "task_id") != 214 || result.App.Reused == nil || *result.App.Reused ||
		!result.App.HasStatePreserved || result.App.StatePreserved == nil || *result.App.StatePreserved {
		t.Fatalf("uncertain open lost the late confirmed landing: %+v, app=%+v", result, result.App)
	}
	requireAppReplayIsolation(t, h, req, result, "dispatch", func(view *protocol.AppView) {
		*view.DisplayID, *view.TaskID = 909, 919
		*view.Reused, *view.StatePreserved = true, true
	})
}

func TestAppOpenReplaysAfterPostObservationFailure(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	h.device.captureErrs = true
	h.device.mu.Unlock()
	req := appOpenRequest(first.Observation, "com.example.app")
	result := h.callApp("Popenfailedcapture", req)
	if result.Outcome != OutcomeExecuted || result.Observation == nil || result.Observation.Status != "failed" ||
		intValue(t, result.App.TaskID, "task_id") != 77 {
		t.Fatalf("post-observation failure lost executed facts: %+v, app=%+v", result, result.App)
	}
	requireAppSummary(t, h, result.OperationID, req, OperationFinished, "post_observation_failed", OutcomeExecuted, result.App)
}

func TestAppQueryReplayOnlyKind(t *testing.T) {
	h := newHarness(t)
	h.device.mu.Lock()
	h.device.appEntries = []device.AppEntry{{Name: "Example", Package: "com.example.app"}}
	h.device.mu.Unlock()
	first := h.startSession()
	h.addTask(v31Task(77, 12, "com.example.app"))
	for _, req := range []*protocol.AppRequest{appFindRequest("", false), appListRequest(first.Observation)} {
		t.Run(req.Kind, func(t *testing.T) {
			id := "Pquery_" + req.Kind
			result := h.callApp(id, req)
			if result.Error != nil || !result.App.HasItems || !result.App.HasTotal || result.App.Total == 0 {
				t.Fatalf("query did not produce inventory to exclude from replay: %+v, app=%+v", result, result.App)
			}
			requireAppSummary(t, h, id, req, OperationFinished, "finished", "", &protocol.AppView{Kind: req.Kind})
			h.engine.mu.Lock()
			stored := h.engine.operations[id].App
			h.engine.mu.Unlock()
			if stored != nil {
				t.Fatalf("query cached an app summary: %+v", stored)
			}
		})
	}
}

func TestAppCloseReplayPreservesTaskPresenceAndErrorDetails(t *testing.T) {
	h := newHarnessWithClock(t, false)
	req := v31CloseTask(&protocol.Observation{ID: "Oclose"}, 77)
	for _, tc := range []struct {
		name string
		view *protocol.AppView
		wire string
	}{
		{name: "omitted", view: &protocol.AppView{Kind: req.Kind}, wire: `{"kind":"close"}`},
		{name: "empty", view: &protocol.AppView{Kind: req.Kind, HasTasks: true, Tasks: []protocol.AppCloseTask{}}, wire: `{"kind":"close","tasks":[]}`},
		{name: "with_error", view: &protocol.AppView{
			Kind: req.Kind, HasTasks: true, Tasks: []protocol.AppCloseTask{{
				TaskID: 77, Outcome: OutcomeNotExecuted, HasRecentsCleaned: true,
				Error: &protocol.Error{Code: protocol.CodeTaskAmbiguous, Message: "多个任务", Details: map[string]any{"task_ids": []int{77, 78}}},
			}},
		}, wire: `{"kind":"close","tasks":[{"task_id":77,"outcome":"not_executed","recents_cleaned":null,"error":{"code":"task_target_ambiguous","message":"多个任务","details":{"task_ids":[77,78]}}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "Pclose_" + tc.name
			op, duplicate, apiErr := h.engine.registerOperation(id, "agent", appTool)
			if apiErr != nil || duplicate != nil {
				t.Fatalf("could not register test close: %+v, %v", duplicate, apiErr)
			}
			h.engine.setOperationAppKind(op, req.Kind)
			// The real close admission rejects an empty plan. Exercise the
			// terminal directly to cover the protocol's explicit empty set.
			first, _ := h.engine.finishAppError(op, "preflight", tc.view,
				protocol.NewError(409, protocol.CodeTaskNotFound, "没有可关闭的任务"))
			requireAppSummary(t, h, id, req, OperationFinished, "preflight", OutcomeNotExecuted, tc.view)
			encoded, err := protocol.JSON(h.callApp(id, req).App)
			if err != nil || string(encoded) != tc.wire {
				t.Fatalf("encoded replay = %s, want %s, err=%v", encoded, tc.wire, err)
			}
			if tc.name == "with_error" {
				requireAppReplayIsolation(t, h, req, first, "preflight", func(view *protocol.AppView) {
					details := view.Tasks[0].Error.Details
					details["task_ids"].([]int)[0] = 909
					details["task_ids"] = []int{919}
				})
				if tc.view.Tasks[0].Error.Details["task_ids"].([]int)[0] != 77 {
					t.Fatal("first response shared task error details with its source")
				}
			}
		})
	}
}

// appCloseReceiptHook steps the close pipeline after removal is confirmed but
// before the engine receives the receipt. This avoids wall-clock waits.
type appCloseReceiptHook struct {
	Device
	afterRemove func()
}

func (d appCloseReceiptHook) RemoveTask(ctx context.Context, taskID int) (device.TaskRemoveResult, error) {
	result, err := d.Device.RemoveTask(ctx, taskID)
	d.afterRemove()
	return result, err
}

func TestAppCloseSummaryAtRevocationAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		phase   string
		outcome string
	}{
		{phase: "executed_revoked", outcome: OutcomeExecuted},
		{phase: "work_deadline", outcome: OutcomeNotExecuted},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			h := newHarnessWithClock(t, false)
			first := h.startSession()
			task := v31Task(77, 12, "com.example.app")
			h.addTask(task)
			h.ownTask(task, TaskOwned, nil)
			req := v31CloseTask(first.Observation, task.TaskID)
			const id = "Pclose_late_receipt"
			kindOnly := &protocol.AppView{Kind: req.Kind}
			h.engine.dev = appCloseReceiptHook{Device: h.device, afterRemove: func() {
				requireAppSummary(t, h, id, req, OperationRunning, "", "", kindOnly)
				h.engine.mu.Lock()
				work := h.engine.work
				work.Revoked = true
				work.RevokedAt = time.Now()
				if tc.phase == "work_deadline" {
					h.engine.detachRetiredLocked(work.RevokedAt.Add(retirementHandover))
				}
				h.engine.mu.Unlock()
				if tc.phase == "work_deadline" {
					requireAppSummary(t, h, id, req, OperationFinished, tc.phase, tc.outcome, kindOnly)
				}
			}}
			result := h.callApp(id, req)
			if result.Outcome != tc.outcome || result.Observation != nil {
				t.Fatalf("first close result = %+v", result)
			}
			if tc.phase == "work_deadline" {
				if result.Phase != tc.phase || !reflect.DeepEqual(result.App, kindOnly) {
					t.Fatalf("late receipt replaced frozen result: %+v, app=%+v", result, result.App)
				}
				requireAppSummary(t, h, id, req, OperationFinished, tc.phase, tc.outcome, kindOnly)
				return
			}
			if !result.App.HasTasks || len(result.App.Tasks) != 1 || result.App.Tasks[0].TaskID != task.TaskID ||
				result.App.Tasks[0].Outcome != OutcomeExecuted {
				t.Fatalf("executed_revoked lost confirmed task: %+v", result.App)
			}
			requireAppReplayIsolation(t, h, req, result, tc.phase, func(view *protocol.AppView) {
				view.Tasks[0].TaskID = 909
			})
		})
	}
}
