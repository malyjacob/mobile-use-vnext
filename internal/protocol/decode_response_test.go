package protocol

import (
	"encoding/json/v2"
	"reflect"
	"testing"
)

// roundTripDecode marshals original through the public protocol marshal path
// and decodes the exact wire value back into a fresh value of the same type.
func roundTripDecode[T any](t *testing.T, original *T) *T {
	t.Helper()
	data, err := JSON(original)
	if err != nil {
		t.Fatalf("marshal %T: %v", original, err)
	}
	var decoded T
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode %T from %s: %v", original, data, err)
	}
	if !reflect.DeepEqual(original, &decoded) {
		t.Fatalf("%T did not round trip\nwire: %s\noriginal: %#v\ndecoded:  %#v",
			original, data, original, &decoded)
	}
	return &decoded
}

// testNode builds a canonical node whose omitted defaults match what the
// existing Node decoder restores (label and actions as empty arrays).
func testNode(ref string, role Role) *Node {
	enabled := true
	return &Node{
		Ref:     ref,
		Role:    role,
		Bounds:  &Bounds{Left: 10, Top: 20, Right: 110, Bottom: 220},
		Label:   []string{},
		Actions: []Action{},
		Enabled: &enabled,
	}
}

func testFullTree() *Tree {
	return &Tree{Format: "full", Scope: Scope, Root: testNode("n1", RoleRoot)}
}

func testObservation() *Observation {
	taskID := 42
	return &Observation{
		Status:          "complete",
		ID:              "O1",
		Origin:          "P1",
		Revision:        1,
		CapturedAt:      "2026-11-09T03:00:00Z",
		ActionableUntil: "2026-11-09T03:01:00Z",
		Screen:          &Screen{Width: 1080, Height: 2400, Rotation: 0},
		Foreground: &Foreground{
			State:   "present",
			Package: "com.example.app",
			TaskID:  &taskID,
			HasTask: true,
		},
		Tree:    &TreeValue{Full: testFullTree()},
		Missing: []string{"image"},
	}
}

func TestDecodeStartSuccess(t *testing.T) {
	original := &Result{
		OperationID:    "P1",
		OperationState: "finished",
		Outcome:        "success",
		Phase:          "commit",
		IncludeSession: true,
		Session: &Session{
			Origin:    "P1",
			State:     "active",
			Mode:      "virtual",
			Agent:     "agent-a",
			StartedAt: "2026-11-09T03:00:00Z",
			ExpiresAt: "2026-11-09T03:10:00Z",
		},
		Observation: testObservation(),
	}
	roundTripDecode(t, original)
}

func TestDecodeObserveDeltaWithImage(t *testing.T) {
	yes := true
	original := &Result{
		OperationID:    "P2",
		OperationState: "finished",
		Outcome:        "success",
		Observation: &Observation{
			Status:          "complete",
			ID:              "O2",
			Origin:          "P1",
			Revision:        2,
			CapturedAt:      "2026-11-09T03:00:01Z",
			ActionableUntil: "2026-11-09T03:01:01Z",
			Screen:          &Screen{Width: 1080, Height: 2400, Rotation: 0},
			Foreground:      &Foreground{State: "present", HasTask: true},
			Tree: &TreeValue{Delta: &Delta{
				Format: "delta",
				Base:   "O1",
				Set: map[string]SetNode{
					"n2": {Label: Field[[]string]{Set: true, Value: []string{"Done"}}},
					"n3": {Checked: Field[*bool]{Set: true, Value: &yes}},
				},
				Remove: []string{"n9"},
			}},
			Image: &ImageMeta{
				CapturedAt: "2026-11-09T03:00:01Z",
				Width:      540,
				Height:     1200,
				MediaType:  "image/jpeg",
				ReadOnly:   true,
				Base64:     "aGVsbG8=",
			},
		},
	}
	roundTripDecode(t, original)
}

func TestDecodeReadOnlyPartialImageWithoutObservationID(t *testing.T) {
	original := &Result{
		OperationID:    "P3",
		OperationState: "finished",
		Observation: &Observation{
			Status: "partial",
			Image: &ImageMeta{
				ReadOnly:  true,
				MediaType: "image/png",
				Base64:    "cGFydGlhbA==",
			},
		},
	}
	roundTripDecode(t, original)
}

func TestDecodeFailedObservationWithImageError(t *testing.T) {
	original := &Result{
		OperationID:    "P4",
		OperationState: "failed",
		Observation: &Observation{
			Status:  "failed",
			Missing: []string{"tree"},
			Image: &ImageMeta{
				ReadOnly: true,
				Error: &Error{
					Code:    CodeImageCapture,
					Message: "image capture failed",
				},
			},
			Error: &Error{Code: CodeHierarchyFailed, Message: "tree unavailable"},
		},
	}
	roundTripDecode(t, original)
}

func TestDecodeActExecutedWithPostObservationAndCleanup(t *testing.T) {
	original := &Result{
		OperationID:    "P5",
		OperationState: "finished",
		Outcome:        "executed",
		Phase:          "post_observe",
		Observation:    testObservation(),
		Cleanup: &Cleanup{
			State:     CleanupComplete,
			Execution: CleanupNone,
			Tasks:     CleanupComplete,
			Recents:   CleanupComplete,
			Display:   CleanupNotApplicable,
		},
	}
	roundTripDecode(t, original)
}

func TestDecodeStatusNotSeen(t *testing.T) {
	original := &Result{OperationState: "not_seen"}
	roundTripDecode(t, original)
}

func TestDecodeStatusCanStartFalse(t *testing.T) {
	no := false
	original := &Result{
		OperationState:      "not_seen",
		RequestClosed:       &no,
		BlockingOperationID: "P8",
		CanStart:            &no,
		BlockingCode:        CodeRecovering,
	}
	roundTripDecode(t, original)
}

func TestDecodeInspectTree(t *testing.T) {
	original := &Result{Inspection: &InspectionView{
		ObservationID: "O1",
		Origin:        "P1",
		Revision:      3,
		CapturedAt:    "2026-11-09T03:00:02Z",
		Scope:         "tree",
		Tree:          testFullTree(),
	}}
	roundTripDecode(t, original)
}

func TestDecodeInspectSubtree(t *testing.T) {
	original := &Result{Inspection: &InspectionView{
		ObservationID: "O1",
		Origin:        "P1",
		Revision:      3,
		CapturedAt:    "2026-11-09T03:00:02Z",
		Scope:         "subtree",
		Root:          testNode("n5", RoleGroup),
	}}
	roundTripDecode(t, original)
}

func TestDecodeInspectQueryMatches(t *testing.T) {
	yes := true
	original := &Result{Inspection: &InspectionView{
		ObservationID: "O1",
		Origin:        "P1",
		Revision:      3,
		CapturedAt:    "2026-11-09T03:00:02Z",
		Scope:         "query",
		Matches: []InspectionMatch{{
			Node: testNode("n6", RoleButton),
			Path: []string{"n1", "n5", "n6"},
		}},
		Total:     9,
		HasTotal:  true,
		Truncated: &yes,
	}}
	roundTripDecode(t, original)
}

func TestDecodeAppsView(t *testing.T) {
	original := &Result{Apps: &AppsView{
		Items: []AppEntry{
			{Name: "Camera", Package: "com.android.camera"},
			{Name: "Maps", Package: "com.example.maps"},
		},
		Total:      5,
		Truncated:  true,
		CapturedAt: "2026-11-09T03:00:03Z",
	}}
	roundTripDecode(t, original)
}

func TestDecodeAppView(t *testing.T) {
	no := false
	original := &Result{App: &AppView{
		Package:        "com.example.app",
		TaskID:         77,
		DisplayID:      12,
		Reused:         true,
		StatePreserved: &no,
	}}
	roundTripDecode(t, original)
}

func TestDecodeShellView(t *testing.T) {
	empty := ""
	original := &Result{Shell: &ShellView{
		Termination: "unconfirmed",
		ExitCode:    nil,
		Output:      &empty,
		Truncated:   nil,
		OutputError: &Error{
			Code:    CodeShellOutput,
			Message: "shell output unavailable",
		},
	}}
	roundTripDecode(t, original)
}

func TestDecodeNotificationWithError(t *testing.T) {
	original := &Result{Notification: &NotificationView{
		State: "failed",
		Error: &Error{Code: CodeNotificationUnavailable, Message: "delivery unavailable"},
	}}
	roundTripDecode(t, original)
}

func TestDecodeDiagnosis(t *testing.T) {
	original := &Result{Diagnosis: &DiagnosisView{
		Summary: "capture was stale",
		Issues: []DiagnosisIssue{
			{Phase: "capture", Code: CodeStaleScreen},
			{Phase: "project", Code: CodeTreeTooLarge},
		},
		TimingMS: map[string]int64{"capture": 1200, "project": 3},
		Timeline: []DiagnosisEvent{
			{At: "2026-11-09T03:00:04Z", Phase: "capture", Code: CodeStaleScreen},
			{At: "2026-11-09T03:00:05Z", Phase: "project"},
		},
		Coverage:  "full",
		Truncated: true,
		Operation: "P6",
		Outcome:   "success",
	}}
	roundTripDecode(t, original)
}

func TestDecodeErrorResultWithDetails(t *testing.T) {
	original := &Result{
		OperationID:    "P7",
		OperationState: "failed",
		Outcome:        "not_executed",
		Error: &Error{
			Code:    CodeDeviceBusy,
			Message: "device busy",
			Details: map[string]any{"limit": float64(5), "check": "recovery"},
		},
	}
	decoded := roundTripDecode(t, original)
	if decoded.Error == nil || decoded.Error.Details["limit"] != float64(5) {
		t.Fatalf("details were not restored: %#v", decoded.Error)
	}
}

func TestDecodeEndWithCleanupError(t *testing.T) {
	original := &Result{
		OperationID:    "P8",
		OperationState: "finished",
		Outcome:        "finished",
		Cleanup: &Cleanup{
			State:     CleanupUnconfirmed,
			Execution: CleanupComplete,
			Tasks:     CleanupComplete,
			Recents:   CleanupComplete,
			Display:   CleanupComplete,
			Error: &Error{
				Code:    CodeCleanupUnconfirmed,
				Message: "cleanup could not be confirmed",
			},
		},
	}
	roundTripDecode(t, original)
}

func TestDecodeHealth(t *testing.T) {
	roundTripDecode(t, &Health{
		Status:            "ok",
		ContractVersion:   ContractVersion,
		ServiceInstanceID: "svc-1",
		ReleaseVersion:    "0.1.0-dev",
	})
}

func TestDecodeForegroundNullTaskID(t *testing.T) {
	original := &Foreground{State: "present", HasTask: true}
	decoded := roundTripDecode(t, original)
	if decoded.TaskID != nil {
		t.Fatalf("explicit null task_id became %v", *decoded.TaskID)
	}
	if !decoded.HasTask {
		t.Fatal("explicit null task_id must still record HasTask")
	}

	zero := 0
	withZero := roundTripDecode(t, &Foreground{State: "present", TaskID: &zero, HasTask: true})
	if withZero.TaskID == nil || *withZero.TaskID != 0 || !withZero.HasTask {
		t.Fatalf("explicit zero task_id was not preserved: %#v", withZero)
	}

	none := roundTripDecode(t, &Foreground{State: "none"})
	if none.TaskID != nil || none.HasTask {
		t.Fatalf("state none produced task identity: %#v", none)
	}

	unknown := roundTripDecode(t, &Foreground{
		State:  "unknown",
		Reason: "display_section_missing",
	})
	if unknown.Reason != "display_section_missing" || unknown.TaskID != nil {
		t.Fatalf("unknown foreground lost its reason: %#v", unknown)
	}
}

func TestDecodeAbsentPointersStayNil(t *testing.T) {
	var result Result
	if err := json.Unmarshal([]byte(`{"operation_id":"P1","operation_state":"not_seen"}`), &result); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if result.RequestClosed != nil || result.CanStart != nil || result.Saved != nil {
		t.Fatalf("absent flags invented values: %#v", result)
	}
	if result.Session != nil || result.IncludeSession {
		t.Fatalf("absent session invented values: %#v", result)
	}

	no := false
	decoded := roundTripDecode(t, &Result{RequestClosed: &no, CanStart: &no, Saved: &no})
	for name, flag := range map[string]*bool{
		"request_closed": decoded.RequestClosed,
		"can_start":      decoded.CanStart,
		"saved":          decoded.Saved,
	} {
		if flag == nil || *flag {
			t.Fatalf("%s explicit false was not preserved: %v", name, flag)
		}
	}
}

func TestDecodeNullOptionalObjects(t *testing.T) {
	var result Result
	body := `{"operation_id":"P1","observation":null,"session":null,"error":null,"cleanup":null}`
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("decode null members: %v", err)
	}
	if result.Observation != nil || result.Error != nil || result.Cleanup != nil {
		t.Fatalf("null object members did not stay nil: %#v", result)
	}
	if result.Session != nil {
		t.Fatalf("null session did not stay nil: %#v", result.Session)
	}
	if !result.IncludeSession {
		t.Fatal("present null session must still record IncludeSession")
	}
}

func TestDecodeUnknownMemberIgnored(t *testing.T) {
	var result Result
	body := `{"operation_id":"P1","future_member":{"nested":true}}`
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("unknown member should be ignored for forward compatibility: %v", err)
	}
	if result.OperationID != "P1" {
		t.Fatalf("known member lost: %#v", result)
	}
}

func TestDecodeRejectsInvalidResponseShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"result null", `null`},
		{"result array", `[]`},
		{"operation id null", `{"operation_id":null}`},
		{"operation id number", `{"operation_id":1}`},
		{"operation state null", `{"operation_state":null}`},
		{"observation null status", `{"observation":{"status":null}}`},
		{"observation status number", `{"observation":{"status":1}}`},
		{"observation array", `{"observation":[]}`},
		{"foreground task id string", `{"observation":{"status":"complete","foreground":{"state":"present","task_id":"1"}}}`},
		{"apps items null", `{"apps":{"items":null,"total":0,"truncated":false,"captured_at":"x"}}`},
		{"apps missing captured at", `{"apps":{"items":[],"total":0,"truncated":false}}`},
		{"shell missing exit code", `{"shell":{"termination":"x","output":"","truncated":null}}`},
		{"error code null", `{"error":{"code":null,"message":"x"}}`},
		{"duplicate member", `{"operation_id":"P1","operation_id":"P2"}`},
		{"trailing value", `{"operation_id":"P1"} {"operation_id":"P2"}`},
		{"malformed", `{"operation_id":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var result Result
			if err := json.Unmarshal([]byte(tc.body), &result); err == nil {
				t.Fatalf("expected decode error for %s", tc.body)
			}
		})
	}
}

func TestDecodeDirectScalarTypesRejectNull(t *testing.T) {
	var screen Screen
	if err := json.Unmarshal([]byte(`{"width":null,"height":2400,"rotation":0}`), &screen); err == nil {
		t.Fatal("expected screen.width null to fail")
	}
	var foreground Foreground
	if err := json.Unmarshal([]byte(`{"state":null}`), &foreground); err == nil {
		t.Fatal("expected foreground.state null to fail")
	}
	var apiErr Error
	if err := json.Unmarshal([]byte(`{"code":"x"}`), &apiErr); err == nil {
		t.Fatal("expected missing error.message to fail")
	}
}
