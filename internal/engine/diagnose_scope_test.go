package engine

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func diagnosisForTest(t *testing.T, e *Engine, reference string) *protocol.DiagnosisView {
	t.Helper()
	result, apiErr := e.Diagnose("agent", &protocol.DiagnoseRequest{
		Reference: reference, HasRef: reference != "",
	})
	if apiErr != nil {
		t.Fatalf("diagnose: %v", apiErr)
	}
	return result.Diagnosis
}

func TestDiagnoseOwnCurrentSession(t *testing.T) {
	started := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	previous := &sessionState{
		Origin: "Pold", Agent: "agent", State: SessionEnded,
		StartedAt: started.Add(-time.Hour), EndedAt: started.Add(-time.Minute),
	}
	current := &sessionState{
		Origin: "Pnew", Agent: "agent", State: SessionActive, StartedAt: started,
	}
	e := &Engine{
		session: current, lastSession: previous, deviceReady: true,
		app:        appState{LastSync: started, Background: true, Notifications: true, StopReady: true, ModeReady: true},
		operations: make(map[string]*operationState),
	}
	add := func(id, agent, origin, code string, created time.Time) {
		e.operations[id] = &operationState{
			ID: id, Agent: agent, SessionOrigin: origin, Kind: "observe",
			State: OperationFinished, Phase: "admission", Outcome: OutcomeNotExecuted,
			Error: &protocol.Error{Code: code}, CreatedAt: created,
			FinishedAt: created.Add(2 * time.Minute),
		}
	}
	add("PoldFailure", "agent", previous.Origin, protocol.CodeOperationUnknown, previous.EndedAt)
	add("PoldDetached", "agent", "", protocol.CodeDeviceBusy, started.Add(-time.Second))
	add("PnewFailure", "agent", current.Origin, protocol.CodeSessionExists, started)
	add("PnewDetached", "agent", "", protocol.CodeNotificationUnavailable, started)
	add("Pforeign", "other", current.Origin, protocol.CodeDeviceUnavailable, started)
	e.operations["PoldRunning"] = &operationState{
		ID: "PoldRunning", Agent: "agent", SessionOrigin: previous.Origin,
		Kind: "act", State: OperationRunning, CreatedAt: started,
	}

	check := func(events int, codes ...string) {
		t.Helper()
		view := diagnosisForTest(t, e, "")
		if len(view.Issues) != len(codes) || len(view.Timeline) != events || view.Truncated {
			t.Fatalf("diagnosis scope: %+v, want %d events and codes %v", view, events, codes)
		}
		wantCounts := make(map[string]int, len(codes))
		for _, code := range codes {
			if !hasDiagnosisIssue(view, "admission", code) {
				t.Fatalf("issues = %+v, missing %q", view.Issues, code)
			}
			wantCounts[code]++
		}
		if !maps.Equal(view.ErrorCounts, wantCounts) {
			t.Fatalf("error_counts = %v, want %v", view.ErrorCounts, wantCounts)
		}
	}
	check(2, protocol.CodeSessionExists, protocol.CodeNotificationUnavailable)

	original := diagnosisForTest(t, e, "PoldFailure")
	if original.Coverage != "original_record" || original.Operation != "PoldFailure" || original.ErrorCounts != nil ||
		!hasDiagnosisIssue(original, "admission", protocol.CodeOperationUnknown) ||
		!hasDiagnosisIssue(original, "session", protocol.CodeSessionEnded) {
		t.Fatalf("old reference: %+v", original)
	}

	current.State = SessionStarting
	current.StartedAt = time.Time{}
	check(1, protocol.CodeSessionExists)

	current.State = SessionEnded
	check(5, protocol.CodeOperationUnknown, protocol.CodeDeviceBusy,
		protocol.CodeSessionExists, protocol.CodeNotificationUnavailable)
}

func TestDiagnoseOwnDeduplicatesRepeatedIssues(t *testing.T) {
	e := &Engine{
		deviceReady: true,
		app:         appState{LastSync: time.Now(), Background: true, Notifications: true, StopReady: true, ModeReady: true},
		operations:  make(map[string]*operationState),
	}
	for i := range 10 {
		id := fmt.Sprintf("P%d", i)
		e.operations[id] = &operationState{
			ID: id, Agent: "agent", Kind: "observe", State: OperationFinished,
			Phase: "admission", Error: &protocol.Error{Code: protocol.CodeDeviceBusy},
			FinishedAt: time.Unix(int64(i+1), 0),
		}
	}
	view := diagnosisForTest(t, e, "")
	if len(view.Issues) != 1 || !hasDiagnosisIssue(view, "admission", protocol.CodeDeviceBusy) {
		t.Fatalf("issues = %+v, want one admission/device_busy issue", view.Issues)
	}
	if len(view.Timeline) != maxDiagnosisEntries || !view.Truncated {
		t.Fatalf("timeline should keep only the latest %d events: %+v", maxDiagnosisEntries, view)
	}
	if first := view.Timeline[0].At; first != formatTime(time.Unix(3, 0)) {
		t.Fatalf("earliest retained event = %q, want third event", first)
	}
	if want := map[string]int{protocol.CodeDeviceBusy: 10}; !maps.Equal(view.ErrorCounts, want) {
		t.Fatalf("error_counts = %v, want %v", view.ErrorCounts, want)
	}
}

func TestDiagnoseOwnUniqueIssueOverflow(t *testing.T) {
	// An unavailable engine contributes one global issue, leaving seven slots.
	e := &Engine{operations: make(map[string]*operationState)}
	codes := []string{
		protocol.CodeOperationUnknown, protocol.CodeDeviceBusy, protocol.CodeDeviceBusy,
		protocol.CodeSessionExists, protocol.CodeSessionExpired, protocol.CodeTargetChanged,
		protocol.CodeInvalidRequest, protocol.CodeDeviceUnavailable,
	}
	wantCounts := make(map[string]int)
	wantIssues := []protocol.DiagnosisIssue{{Phase: "availability", Code: protocol.CodeDeviceUnavailable}}
	for i, code := range codes {
		id := fmt.Sprintf("P%d", i)
		phase := fmt.Sprintf("phase%d", i)
		e.operations[id] = &operationState{
			ID: id, Agent: "agent", Kind: "observe", State: OperationFinished,
			Phase: phase, Error: &protocol.Error{Code: code}, FinishedAt: time.Unix(1, 0),
		}
		wantCounts[code]++
	}
	for i := len(codes) - 1; i > 0; i-- {
		wantIssues = append(wantIssues, protocol.DiagnosisIssue{Phase: fmt.Sprintf("phase%d", i), Code: codes[i]})
	}

	check := func(t *testing.T, truncated bool) {
		t.Helper()
		view := diagnosisForTest(t, e, "")
		if !slices.Equal(view.Issues, wantIssues) || len(view.Timeline) != len(codes) {
			t.Fatalf("diagnosis = %+v, want issues %v and %d timeline entries", view, wantIssues, len(codes))
		}
		if view.Truncated != truncated {
			t.Fatalf("truncated = %v, want %v with %d timeline entries", view.Truncated, truncated, len(view.Timeline))
		}
		if !maps.Equal(view.ErrorCounts, wantCounts) {
			t.Fatalf("error_counts = %v, want %v", view.ErrorCounts, wantCounts)
		}
	}
	t.Run("distinct issue overflows", func(t *testing.T) { check(t, true) })

	// Eight events still fit the timeline. The oldest now repeats a newer
	// issue, so all distinct pairs fit despite the same number of operations.
	e.operations["P0"].Phase = e.operations["P7"].Phase
	e.operations["P0"].Error.Code = e.operations["P7"].Error.Code
	delete(wantCounts, codes[0])
	wantCounts[codes[7]]++
	t.Run("duplicate fits", func(t *testing.T) { check(t, false) })
}

func TestDiagnoseOwnErrorCountFallback(t *testing.T) {
	e := &Engine{operations: map[string]*operationState{
		"P1": {ID: "P1", Agent: "agent", State: OperationFinished, Outcome: OutcomeUncertain},
		"P2": {ID: "P2", Agent: "agent", State: OperationFinished, Outcome: OutcomeUncertain, Error: &protocol.Error{}},
		"P3": {ID: "P3", Agent: "agent", State: OperationFinished, Error: &protocol.Error{}},
		"P4": {ID: "P4", Agent: "agent", State: OperationRunning, Outcome: OutcomeUncertain, Error: &protocol.Error{Code: protocol.CodeDeviceBusy}},
		"P5": {ID: "P5", Agent: "agent", State: OperationFinished, Outcome: OutcomeExecuted},
	}}
	view := diagnosisForTest(t, e, "")
	if want := map[string]int{OutcomeUncertain: 2}; !maps.Equal(view.ErrorCounts, want) {
		t.Fatalf("error_counts = %v, want %v", view.ErrorCounts, want)
	}
}

func TestDiagnoseBudgetTrimsCountsLast(t *testing.T) {
	view := &protocol.DiagnosisView{
		Summary: "记录到近期问题", Coverage: "own_records",
		ErrorCounts: map[string]int{protocol.CodeDeviceBusy: 10},
		Issues: []protocol.DiagnosisIssue{
			{Phase: "availability", Code: protocol.CodeDeviceUnavailable},
			{Phase: "admission", Code: protocol.CodeDeviceBusy},
		},
		Timeline: []protocol.DiagnosisEvent{
			{At: "2026-11-28T00:00:00Z", Phase: "observe", Code: protocol.CodeDeviceBusy},
			{At: "2026-11-28T00:00:01Z", Phase: "observe", Code: protocol.CodeDeviceBusy},
		},
	}
	want := *view
	want.Truncated = true
	steps := []struct {
		name string
		drop func()
	}{
		{"first event", func() { want.Timeline = want.Timeline[1:] }},
		{"second event", func() { want.Timeline = want.Timeline[1:] }},
		{"first issue", func() { want.Issues = want.Issues[1:] }},
		{"second issue", func() { want.Issues = want.Issues[1:] }},
		{"counts", func() { want.ErrorCounts = nil }},
	}
	for _, step := range steps {
		step.drop()
		encoded, err := protocol.JSON(&want)
		if err != nil {
			t.Fatalf("encode expected diagnosis: %v", err)
		}
		view.Truncated = false
		trimDiagnosisBudget(view, len(encoded))
		got, err := protocol.JSON(view)
		if err != nil {
			t.Fatalf("encode trimmed diagnosis: %v", err)
		}
		if string(got) != string(encoded) {
			t.Fatalf("after dropping %s\ngot  %s\nwant %s", step.name, got, encoded)
		}
	}
}
