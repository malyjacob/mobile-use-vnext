package protocol

import (
	"encoding/json/v2"
	"net/http"
	"reflect"
	"strings"
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

// testNode builds a canonical node whose omitted state defaults match what the
// Node decoder restores (label and actions as empty arrays).
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

// testReadNode builds the public read definition of one node with delivered
// geometry and an explicit known-false checked fact.
func testReadNode(ref string, role Role) *ReadNode {
	enabled := true
	checked := false
	return &ReadNode{
		Ref:     ref,
		Role:    role,
		Bounds:  KnownBounds(Bounds{Left: 10, Top: 20, Right: 110, Bottom: 220}),
		Label:   []string{},
		Actions: []Action{},
		Enabled: &enabled,
		Checked: &checked,
	}
}

func testReadFullTree(view string) *ReadTree {
	return &ReadTree{Format: "full", Scope: Scope, View: view, Root: testReadNode("n1", RoleRoot)}
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
		Visual:  "sparse_tree",
		Tree:    &ReadTreeValue{Full: testReadFullTree(ViewCompact)},
		Missing: []string{"image"},
	}
}

func TestDecodeSnapResultImage(t *testing.T) {
	original := &Result{
		OperationID: "P1",
		Image: &ImageMeta{
			CapturedAt: "2026-11-18T00:00:00Z",
			Width:      540,
			Height:     1200,
			MediaType:  "image/jpeg",
			ReadOnly:   true,
			Screen:     &Screen{Width: 1080, Height: 2400, Rotation: 0},
			Base64:     "AA==",
		},
	}
	roundTripDecode(t, original)
}

// TestDecodeSnapResultRejectsExplicitNullImage pins the wire rule that the
// top-level image member is an object when present.
func TestDecodeSnapResultRejectsExplicitNullImage(t *testing.T) {
	var result Result
	err := json.Unmarshal([]byte(`{"operation_id":"P1","image":null}`), &result)
	if err == nil {
		t.Fatal("explicit null image was accepted")
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
			Tree: &ReadTreeValue{Delta: &ReadDelta{
				Format: "delta",
				Base:   "O1",
				View:   ViewCompact,
				Set: map[string]ReadSetNode{
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
		Tree:          testReadFullTree(ViewDetailed),
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
		View:          ViewDetailed,
		Root:          testReadNode("n5", RoleGroup),
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
		View:          ViewDetailed,
		Matches: []InspectionMatch{{
			Node: testReadNode("n6", RoleButton),
			Path: []string{"n1", "n5", "n6"},
		}},
		Total:     9,
		HasTotal:  true,
		Truncated: &yes,
	}}
	roundTripDecode(t, original)
}

func TestDecodeInspectionRejectsLegacyDeclaration(t *testing.T) {
	const prefix = `{"observation_id":"O1","origin":"P1","revision":3,` +
		`"captured_at":"2026-11-09T03:00:02Z",`
	cases := []struct {
		name string
		body string
	}{
		{
			name: "query envelope",
			body: prefix + `"scope":"query","view":"detailed","defaults":{"checked":false},` +
				`"matches":[],"total":0}`,
		},
		{
			name: "query envelope null",
			body: prefix + `"scope":"query","view":"detailed","defaults":null,` +
				`"matches":[],"total":0}`,
		},
		{
			name: "subtree envelope",
			body: prefix + `"scope":"subtree","view":"detailed","defaults":{"checked":false},` +
				`"root":{"ref":"n5","role":"group","bounds":[0,0,1,1]}}`,
		},
		{
			name: "tree envelope",
			body: prefix + `"scope":"tree","defaults":{"checked":false},` +
				`"tree":{"format":"full","scope":"reported_visible_with_context","view":"detailed",` +
				`"root":{"ref":"n1","role":"root","bounds":[0,0,1,1]}}}`,
		},
		{
			name: "nested tree",
			body: prefix + `"scope":"tree",` +
				`"tree":{"format":"full","scope":"reported_visible_with_context","view":"detailed",` +
				`"defaults":{"checked":false},"root":{"ref":"n1","role":"root","bounds":[0,0,1,1]}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var view InspectionView
			if err := json.Unmarshal([]byte(tc.body), &view); err == nil {
				t.Fatalf("accepted removed defaults: %s", tc.body)
			}
		})
	}
}

func TestDecodeInspectionShapeValidation(t *testing.T) {
	const prefix = `{"observation_id":"O1","origin":"P1","revision":3,` +
		`"captured_at":"2026-11-09T03:00:02Z",`
	cases := []struct {
		name string
		body string
	}{
		{
			name: "tree outer view",
			body: prefix + `"scope":"tree","view":"detailed",` +
				`"tree":{"format":"full","scope":"reported_visible_with_context","view":"detailed",` +
				`"root":{"ref":"n1","role":"root","bounds":[0,0,1,1]}}}`,
		},
		{
			name: "tree compact nested view",
			body: prefix + `"scope":"tree",` +
				`"tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",` +
				`"root":{"ref":"n1","role":"root","bounds":[0,0,1,1]}}}`,
		},
		{
			name: "subtree missing view",
			body: prefix + `"scope":"subtree","root":{"ref":"n5","role":"group","bounds":[0,0,1,1]}}`,
		},
		{
			name: "subtree compact view",
			body: prefix + `"scope":"subtree","view":"compact",` +
				`"root":{"ref":"n5","role":"group","bounds":[0,0,1,1]}}`,
		},
		{
			name: "query missing view",
			body: prefix + `"scope":"query","matches":[],"total":0}`,
		},
		{
			name: "query compact view",
			body: prefix + `"scope":"query","view":"compact","matches":[],"total":0}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var view InspectionView
			if err := json.Unmarshal([]byte(tc.body), &view); err == nil {
				t.Fatalf("accepted invalid inspection shape: %s", tc.body)
			}
		})
	}
}

func TestDecodeAppFindView(t *testing.T) {
	yes := true
	original := &Result{App: &AppView{
		Kind: AppKindFind,
		Items: []AppItem{
			{Name: "Camera", Package: "com.android.camera"},
			{Name: "Maps", Package: "com.example.maps"},
		},
		HasItems:   true,
		Total:      5,
		HasTotal:   true,
		Truncated:  &yes,
		CapturedAt: "2026-11-09T03:00:03Z",
	}}
	roundTripDecode(t, original)
}

func TestDecodeAppListView(t *testing.T) {
	origin := 0
	original := &Result{App: &AppView{
		Kind:      AppKindList,
		DisplayID: intPtrForTest(12),
		TaskItems: []AppTaskItem{
			{TaskID: 42, Package: "com.example", Ownership: TaskOwnershipBorrowed,
				OriginDisplay: &origin},
			{TaskID: 43, Package: "com.other", Ownership: TaskOwnershipShared},
		},
		HasItems:   true,
		Total:      2,
		HasTotal:   true,
		CapturedAt: "2026-11-09T03:00:04Z",
		Foreground: &Foreground{State: "present", Package: "com.example", TaskID: intPtrForTest(42), HasTask: true},
	}}
	roundTripDecode(t, original)
}

func TestDecodeAppActionViews(t *testing.T) {
	no := false
	original := &Result{App: &AppView{
		Kind: AppKindClaim, Package: "com.example.app",
		TaskID: intPtrForTest(77), DisplayID: intPtrForTest(12),
		SourceDisplayID: intPtrForTest(0),
		Ownership:       TaskOwnershipBorrowed, StatePreserved: &no, HasStatePreserved: true,
	}}
	roundTripDecode(t, original)

	handoff := &Result{App: &AppView{
		Kind: AppKindHandoff, Package: "com.example.app",
		TaskID: intPtrForTest(77), DisplayID: intPtrForTest(0),
		SourceDisplayID: intPtrForTest(12),
		Ownership:       TaskOwnershipShared,
		StatePreserved:  nil, HasStatePreserved: true,
		ForegroundRestore: ForegroundRestoreUnconfirmed,
	}}
	roundTripDecode(t, handoff)

	original = &Result{App: &AppView{
		Kind: AppKindOpen, Package: "com.example.app", TaskID: intPtrForTest(77),
		DisplayID: intPtrForTest(12), Ownership: TaskOwnershipOwned,
		Reused: &no, StatePreserved: &no, HasStatePreserved: true,
	}}
	roundTripDecode(t, original)
}

func TestDecodeAppCloseView(t *testing.T) {
	yes := true
	original := &Result{App: &AppView{
		Kind: AppKindClose, DisplayID: intPtrForTest(12), HasTasks: true,
		Tasks: []AppCloseTask{
			{TaskID: 42, Outcome: "executed", RecentsCleaned: &yes, HasRecentsCleaned: true},
			{TaskID: 43, Outcome: "uncertain", RecentsCleaned: nil, HasRecentsCleaned: true},
			{TaskID: 44, Outcome: "not_executed", Error: &Error{
				Code: CodeStopped, Message: "任务关闭已停止"}},
		},
		Complete: &yes,
	}}
	roundTripDecode(t, original)
}

func TestDecodeAppKillView(t *testing.T) {
	original := &Result{App: &AppView{
		Kind: AppKindKill, Package: "com.example.app", AndroidUserID: intPtrForTest(0),
		SourceTaskID: intPtrForTest(42), DisplayID: intPtrForTest(12),
		TargetTaskIDs: []int{42}, HasTargetTaskIDs: true, RemainingTaskIDs: []int{}, HasRemainingTaskIDs: true,
		TargetPIDs: []int{1234, 1235}, KilledPIDs: []int{1234}, RemainingPIDs: []int{1235},
		HasTargetPIDs: true, HasKilledPIDs: true, HasRemainingPIDs: true,
		Restarted: nil, HasRestarted: true, PackageState: "still_present",
	}}
	roundTripDecode(t, original)
	roundTripDecode(t, &Result{App: &AppView{Kind: AppKindKill}})
}

func TestDecodeAppKillViewRejectsInvalidMemberShapes(t *testing.T) {
	for _, member := range []string{
		`"android_user_id":null`,
		`"android_user_id":"0"`,
		`"android_user_id":0.5`,
		`"target_task_ids":null`,
		`"target_task_ids":42`,
		`"target_task_ids":[null]`,
		`"remaining_task_ids":null`,
		`"remaining_task_ids":["42"]`,
		`"package_state":null`,
		`"package_state":1`,
	} {
		t.Run(member, func(t *testing.T) {
			var view AppView
			body := `{"kind":"kill",` + member + `}`
			if err := json.Unmarshal([]byte(body), &view); err == nil {
				t.Fatalf("accepted invalid app view: %+v", view)
			}
		})
	}
}

// intPtrForTest returns a pointer to one literal for response fixtures.
func intPtrForTest(value int) *int {
	copy := value
	return &copy
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

// requireDecodedCandidate checks the fixed candidate object carried by an
// error.details.candidates array after a generic JSON decode. Numeric bounds
// are float64 on the in-memory side but must remain exact integers.
func requireDecodedCandidate(t *testing.T, raw any, wantRef, wantRole string,
	wantActions []string, wantBounds []int, wantRelation string, wantExecutable bool) {
	t.Helper()
	candidate, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("candidate = %T, want object", raw)
	}
	if len(candidate) != 6 {
		t.Fatalf("candidate fields = %#v, want exactly ref/role/actions/bounds/relation/executable", candidate)
	}
	if candidate["ref"] != wantRef || candidate["role"] != wantRole ||
		candidate["relation"] != wantRelation {
		t.Fatalf("candidate identity = %#v", candidate)
	}
	actions, ok := candidate["actions"].([]any)
	if !ok || actions == nil || len(actions) != len(wantActions) {
		t.Fatalf("candidate actions = %#v, want %v", candidate["actions"], wantActions)
	}
	for index, want := range wantActions {
		if actions[index] != want {
			t.Fatalf("candidate actions = %#v, want %v", actions, wantActions)
		}
	}
	bounds, ok := candidate["bounds"].([]any)
	if !ok || bounds == nil || len(bounds) != len(wantBounds) {
		t.Fatalf("candidate bounds = %#v, want %v", candidate["bounds"], wantBounds)
	}
	for index, want := range wantBounds {
		if bounds[index] != float64(want) {
			t.Fatalf("candidate bounds = %#v, want %v", bounds, wantBounds)
		}
	}
	executable, ok := candidate["executable"].(bool)
	if !ok || executable != wantExecutable {
		t.Fatalf("candidate executable = %#v, want %t", candidate["executable"], wantExecutable)
	}
}

// requireSameJSONValue asserts two wire values are semantically equal after
// generic decoding, so integer-valued float64 bounds do not change the payload.
func requireSameJSONValue(t *testing.T, want, got []byte) {
	t.Helper()
	var wantValue, gotValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	if !reflect.DeepEqual(wantValue, gotValue) {
		t.Fatalf("JSON value changed\nwant %#v\n got %#v", wantValue, gotValue)
	}
}

// TestDecodeErrorResultWithCandidates pins the strict response decoder's
// unified static rejection details: candidate objects and their nested arrays
// stay intact, an empty action list stays [] rather than null, and blocking
// refs retain their string values. Re-encoding preserves every value.
func TestDecodeErrorResultWithCandidates(t *testing.T) {
	body := []byte(`{"operation_id":"P15","operation_state":"finished","outcome":"not_executed",` +
		`"error":{"code":"no_safe_point","message":"共位目标冲突","details":` +
		`{"reason":"colocated_ambiguous","scope":"target","scope_ref":"n5233",` +
		`"candidates":[` +
		`{"ref":"n5232","role":"button","actions":["tap"],"bounds":[915,1675,1028,1787],` +
		`"relation":"overlapping","executable":true},` +
		`{"ref":"n5231","role":"group","actions":[],"bounds":[0,1600,1080,1900],` +
		`"relation":"child","executable":false}],` +
		`"blocking_refs":["n5230","n5253"]}}}`)
	var decoded Result
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode nested details: %v", err)
	}
	if decoded.Error == nil {
		t.Fatal("decoded error is nil")
	}
	details := decoded.Error.Details
	if details["reason"] != "colocated_ambiguous" || details["scope"] != "target" ||
		details["scope_ref"] != "n5233" {
		t.Fatalf("static details = %#v", details)
	}
	candidates, ok := details["candidates"].([]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want two objects", details["candidates"])
	}
	requireDecodedCandidate(t, candidates[0], "n5232", "button",
		[]string{"tap"}, []int{915, 1675, 1028, 1787}, "overlapping", true)
	requireDecodedCandidate(t, candidates[1], "n5231", "group",
		[]string{}, []int{0, 1600, 1080, 1900}, "child", false)
	blockingRefs, ok := details["blocking_refs"].([]any)
	if !ok || len(blockingRefs) != 2 ||
		blockingRefs[0] != "n5230" || blockingRefs[1] != "n5253" {
		t.Fatalf("blocking_refs = %#v", details["blocking_refs"])
	}

	reencoded, err := JSON(&decoded)
	if err != nil {
		t.Fatalf("re-encode nested details: %v", err)
	}
	requireSameJSONValue(t, body, reencoded)
}

// TestAPIErrorPublicCandidatesRoundTrip follows the production projection and
// response-decoder path for a static scroll rejection. The fixed candidate
// fields, optional blocking refs and integer bounds survive unchanged.
func TestAPIErrorPublicCandidatesRoundTrip(t *testing.T) {
	apiErr := NewError(http.StatusUnprocessableEntity, CodeNoSafeTrajectory, "无安全滚动轨迹").
		WithDetail("reason", "trajectory_blocked").
		WithDetail("scope", "target").
		WithDetail("scope_ref", "n56").
		WithDetail("candidates", []map[string]any{{
			"ref":        "n66",
			"role":       "scroll",
			"actions":    []string{"swipe"},
			"bounds":     []int{0, 363, 1080, 2265},
			"relation":   "child",
			"executable": true,
		}}).
		WithDetail("blocking_refs", []string{"n57"})

	wire, err := JSON(&Result{
		OperationID:    "P16",
		OperationState: "finished",
		Outcome:        "not_executed",
		Error:          apiErr.Public(),
	})
	if err != nil {
		t.Fatalf("encode public error: %v", err)
	}
	var decoded Result
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("decode public error: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeNoSafeTrajectory {
		t.Fatalf("decoded error = %#v", decoded.Error)
	}
	details := decoded.Error.Details
	candidates, ok := details["candidates"].([]any)
	if !ok || len(candidates) != 1 {
		t.Fatalf("candidates = %#v", details["candidates"])
	}
	requireDecodedCandidate(t, candidates[0], "n66", "scroll",
		[]string{"swipe"}, []int{0, 363, 1080, 2265}, "child", true)
	if refs, ok := details["blocking_refs"].([]any); !ok || len(refs) != 1 || refs[0] != "n57" {
		t.Fatalf("blocking_refs = %#v", details["blocking_refs"])
	}

	reencoded, err := JSON(&decoded)
	if err != nil {
		t.Fatalf("re-encode decoded result: %v", err)
	}
	requireSameJSONValue(t, wire, reencoded)
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
		{"app missing kind", `{"app":{"items":[]}}`},
		{"app kind null", `{"app":{"kind":null}}`},
		{"app items null", `{"app":{"kind":"find","items":null}}`},
		{"app pids null", `{"app":{"kind":"kill","target_pids":null}}`},
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

// TestObservationQualityEncodingOrder pins the fixed public member order and
// the explicit zero-sample facts. Reverse control: dropping the quality member,
// or omitting samples=0 / stop_reason (for example by adding omitempty), makes
// the exact comparison fail.
func TestObservationQualityEncodingOrder(t *testing.T) {
	observation := &Observation{
		Status:          "complete",
		ID:              "O1",
		Origin:          "P1",
		Revision:        1,
		CapturedAt:      "2026-11-12T00:00:00Z",
		ActionableUntil: "2026-11-12T00:01:00Z",
		Quality:         &Quality{State: QualityUnknown, Samples: 0, StopReason: QualityStopNotSampled},
		Screen:          &Screen{Width: 1080, Height: 2400, Rotation: 0},
	}
	got, err := JSON(observation)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	want := `{"status":"complete","id":"O1","origin":"P1","revision":1,` +
		`"captured_at":"2026-11-12T00:00:00Z","actionable_until":"2026-11-12T00:01:00Z",` +
		`"quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"},` +
		`"screen":{"width":1080,"height":2400,"rotation":0}}`
	if string(got) != want {
		t.Fatalf("observation quality encoding:\n got %s\nwant %s", got, want)
	}
}

// TestFailedObservationHasNoQuality proves a failed observation with no tree
// publishes its error material without a manufactured quality fact.
func TestFailedObservationHasNoQuality(t *testing.T) {
	observation := &Observation{
		Status:  "failed",
		Missing: []string{"tree"},
		Error:   &Error{Code: CodeHierarchyFailed, Message: "tree unavailable"},
	}
	got, err := JSON(observation)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	if strings.Contains(string(got), "quality") {
		t.Fatalf("failed observation carried quality: %s", got)
	}
	if !strings.Contains(string(got), `"status":"failed"`) {
		t.Fatalf("failed status missing: %s", got)
	}
}

// TestResultEffectEncodingOrder pins effect between phase and target_reference.
// Reverse control: moving or dropping the member makes the exact comparison
// fail.
func TestResultEffectEncodingOrder(t *testing.T) {
	result := &Result{
		OperationID:     "P1",
		OperationState:  "finished",
		Outcome:         "executed",
		Phase:           "post_observe",
		Effect:          &Effect{State: EffectObserved},
		TargetReference: "O2",
	}
	got, err := JSON(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	want := `{"operation_id":"P1","operation_state":"finished","outcome":"executed",` +
		`"phase":"post_observe","effect":{"state":"observed"},"target_reference":"O2"}`
	if string(got) != want {
		t.Fatalf("result effect encoding:\n got %s\nwant %s", got, want)
	}
}

// TestDecodeObservationQuality is the quality decode matrix: every member is
// required and enum-bound, explicit null is rejected, samples=0 stays valid,
// and an absent object stays nil for failed/legacy shapes.
func TestDecodeObservationQuality(t *testing.T) {
	valid := []struct {
		name string
		body string
		want Quality
	}{
		{
			name: "stable",
			body: `{"status":"complete","quality":{"state":"stable","samples":3,"stop_reason":"stable"}}`,
			want: Quality{State: QualityStable, Samples: 3, StopReason: QualityStopStable},
		},
		{
			name: "zero samples is explicit",
			body: `{"status":"complete","quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"}}`,
			want: Quality{State: QualityUnknown, Samples: 0, StopReason: QualityStopNotSampled},
		},
		{
			name: "unstable max samples",
			body: `{"status":"complete","quality":{"state":"unstable","samples":5,"stop_reason":"max_samples"}}`,
			want: Quality{State: QualityUnstable, Samples: 5, StopReason: QualityStopMaxSamples},
		},
		{
			name: "unreadable",
			body: `{"status":"partial","quality":{"state":"unknown","samples":1,"stop_reason":"unreadable"}}`,
			want: Quality{State: QualityUnknown, Samples: 1, StopReason: QualityStopUnreadable},
		},
		{
			name: "budget",
			body: `{"status":"complete","quality":{"state":"unstable","samples":2,"stop_reason":"budget"}}`,
			want: Quality{State: QualityUnstable, Samples: 2, StopReason: QualityStopBudget},
		},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			var observation Observation
			if err := json.Unmarshal([]byte(tc.body), &observation); err != nil {
				t.Fatalf("decode %s: %v", tc.body, err)
			}
			if observation.Quality == nil || *observation.Quality != tc.want {
				t.Fatalf("quality = %#v, want %#v", observation.Quality, tc.want)
			}
		})
	}

	t.Run("absent stays nil", func(t *testing.T) {
		var observation Observation
		if err := json.Unmarshal([]byte(`{"status":"complete"}`), &observation); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if observation.Quality != nil {
			t.Fatalf("absent quality became %#v", observation.Quality)
		}
	})

	// The response wire keeps its documented forward-compatibility rule: unknown
	// members of a known object are ignored rather than failing the result.
	t.Run("unknown member ignored", func(t *testing.T) {
		var observation Observation
		body := `{"status":"complete","quality":{"state":"stable","samples":2,"stop_reason":"stable","future":true}}`
		if err := json.Unmarshal([]byte(body), &observation); err != nil {
			t.Fatalf("unknown member should stay readable: %v", err)
		}
		if observation.Quality == nil || observation.Quality.Samples != 2 {
			t.Fatalf("quality lost: %#v", observation.Quality)
		}
	})

	invalid := []struct {
		name string
		body string
	}{
		{"null object", `{"status":"complete","quality":null}`},
		{"missing state", `{"status":"complete","quality":{"samples":0,"stop_reason":"not_sampled"}}`},
		{"missing samples", `{"status":"complete","quality":{"state":"unknown","stop_reason":"not_sampled"}}`},
		{"missing stop reason", `{"status":"complete","quality":{"state":"unknown","samples":0}}`},
		{"null state", `{"status":"complete","quality":{"state":null,"samples":0,"stop_reason":"not_sampled"}}`},
		{"null samples", `{"status":"complete","quality":{"state":"unknown","samples":null,"stop_reason":"not_sampled"}}`},
		{"null stop reason", `{"status":"complete","quality":{"state":"unknown","samples":0,"stop_reason":null}}`},
		{"unknown state", `{"status":"complete","quality":{"state":"settled","samples":0,"stop_reason":"not_sampled"}}`},
		{"unknown stop reason", `{"status":"complete","quality":{"state":"unknown","samples":0,"stop_reason":"settled"}}`},
		{"negative samples", `{"status":"complete","quality":{"state":"unknown","samples":-1,"stop_reason":"budget"}}`},
		{"fractional samples", `{"status":"complete","quality":{"state":"unknown","samples":1.5,"stop_reason":"budget"}}`},
		{"string samples", `{"status":"complete","quality":{"state":"unknown","samples":"1","stop_reason":"budget"}}`},
		{"bool state", `{"status":"complete","quality":{"state":true,"samples":1,"stop_reason":"budget"}}`},
		{"array object", `{"status":"complete","quality":[]}`},
		{"string object", `{"status":"complete","quality":"stable"}`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			var observation Observation
			if err := json.Unmarshal([]byte(tc.body), &observation); err == nil {
				t.Fatalf("expected decode error for %s", tc.body)
			}
		})
	}
}

// TestDecodeResultEffect is the effect decode matrix: state is required and
// enum-bound, explicit null is rejected, and an absent object stays nil.
func TestDecodeResultEffect(t *testing.T) {
	for _, state := range []string{EffectObserved, EffectNotObserved, EffectUnknown} {
		t.Run(state, func(t *testing.T) {
			var result Result
			body := `{"operation_id":"P1","effect":{"state":"` + state + `"}}`
			if err := json.Unmarshal([]byte(body), &result); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
			if result.Effect == nil || result.Effect.State != state {
				t.Fatalf("effect = %#v, want %q", result.Effect, state)
			}
		})
	}

	t.Run("absent stays nil", func(t *testing.T) {
		var result Result
		if err := json.Unmarshal([]byte(`{"operation_id":"P1"}`), &result); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if result.Effect != nil {
			t.Fatalf("absent effect became %#v", result.Effect)
		}
	})

	invalid := []struct {
		name string
		body string
	}{
		{"null object", `{"effect":null}`},
		{"missing state", `{"effect":{}}`},
		{"null state", `{"effect":{"state":null}}`},
		{"unknown state", `{"effect":{"state":"moved"}}`},
		{"array object", `{"effect":[]}`},
		{"string object", `{"effect":"observed"}`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			var result Result
			if err := json.Unmarshal([]byte(tc.body), &result); err == nil {
				t.Fatalf("expected decode error for %s", tc.body)
			}
		})
	}
}

// TestDecodeQualityEffectRoundTrip proves the public encoder and the strict
// decoder agree on the new facts, including samples=0.
func TestDecodeQualityEffectRoundTrip(t *testing.T) {
	original := testObservation()
	original.Quality = &Quality{State: QualityUnstable, Samples: 5, StopReason: QualityStopMaxSamples}
	roundTripDecode(t, original)

	original.Quality = &Quality{State: QualityUnknown, Samples: 0, StopReason: QualityStopNotSampled}
	roundTripDecode(t, original)

	roundTripDecode(t, &Result{
		OperationID:     "P9",
		OperationState:  "finished",
		Outcome:         "executed",
		Phase:           "post_observe",
		Effect:          &Effect{State: EffectNotObserved},
		TargetReference: "O9",
		Observation:     original,
	})
}
