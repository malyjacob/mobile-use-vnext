package protocol

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestToolSurfaceStrictAdmission(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
		call func([]byte) *APIError
	}{
		{"app find empty", `{"kind":"find"}`, true, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app find query", `{"kind":"find","query":"设置"}`, true, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app find null query", `{"kind":"find","query":null}`, false, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app find query too long", `{"kind":"find","query":"` + strings.Repeat("a", MaxQueryRunes+1) + `"}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app list", `{"kind":"list","reference":"O1"}`, true, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app list primary", `{"kind":"list","reference":"P1","scope":"primary","app":"com.example"}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app list bad scope", `{"kind":"list","reference":"O1","scope":"all"}`, false, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app list no reference", `{"kind":"list"}`, false, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},

		{"app open ok", `{"kind":"open","reference":"O1","target":{"app":"com.example"}}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app open missing target", `{"kind":"open","reference":"O1"}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app open null target", `{"kind":"open","reference":"O1","target":null}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app open top-level app does not supply target", `{"kind":"open","reference":"O1","app":"com.example"}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app open bad package", `{"kind":"open","reference":"O1","target":{"app":"com..bad"}}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app open bad ref", `{"kind":"open","reference":"X1","target":{"app":"com.example"}}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app open task target", `{"kind":"open","reference":"O1","target":{"task_id":1}}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},

		{"app claim task", `{"kind":"claim","reference":"O1","target":{"task_id":42}}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app claim app", `{"kind":"claim","reference":"O1","target":{"app":"com.example"}}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app claim both", `{"kind":"claim","reference":"O1","target":{"task_id":1,"app":"com.example"}}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app claim empty target", `{"kind":"claim","reference":"O1","target":{}}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app claim zero task", `{"kind":"claim","reference":"O1","target":{"task_id":0}}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app handoff restore", `{"kind":"handoff","reference":"O1","target":{"task_id":1},"restore_foreground":true}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app close borrowed", `{"kind":"close","reference":"O1","target":{"app":"com.example"},"allow_borrowed":true}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app kill task", `{"kind":"kill","reference":"O1","target":{"task_id":1},"allow_stop_playback":true}`, true,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app kill missing target", `{"kind":"kill","reference":"O1"}`, false,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app unknown kind", `{"kind":"move"}`, false, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app kind case", `{"kind":"Find"}`, false, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app missing kind", `{"query":"x"}`, false, func(b []byte) *APIError { _, e := DecodeApp(b); return e }},

		{"notify ok", `{"text":"提醒"}`, true, func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"notify empty", `{"text":""}`, false, func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"notify null", `{"text":null}`, false, func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"notify missing", `{}`, false, func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"notify too long", `{"text":"` + strings.Repeat("测", MaxTextRunes+1) + `"}`, false,
			func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},

		{"shell ok", `{"reference":"P1","command":"id"}`, true, func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"shell root", `{"reference":"O1","command":"id","root":true}`, true, func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"shell null root", `{"reference":"O1","command":"id","root":null}`, false, func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"shell empty command", `{"reference":"O1","command":""}`, false, func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"shell missing command", `{"reference":"O1"}`, false, func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"shell nul", `{"reference":"O1","command":"a\u0000b"}`, false, func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"shell too long", `{"reference":"O1","command":"` + strings.Repeat("x", MaxCommandRunes+1) + `"}`, false,
			func(b []byte) *APIError { _, e := DecodeShell(b); return e }},

		{"inspect tree", `{"reference":"O1"}`, true, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect node", `{"reference":"O1","node":"n2"}`, true, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect query", `{"reference":"O1","query":"确定"}`, true, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect both", `{"reference":"O1","node":"n2","query":"x"}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect null node", `{"reference":"O1","node":null}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect empty node", `{"reference":"O1","node":""}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect missing id", `{"query":"x"}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect old observation_id", `{"observation_id":"O1"}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},

		{"keepalive ok", `{"reference":"O1"}`, true, func(b []byte) *APIError { _, e := DecodeKeepalive(b); return e }},
		{"keepalive null", `{"reference":null}`, false, func(b []byte) *APIError { _, e := DecodeKeepalive(b); return e }},

		{"diagnose empty", `{}`, true, func(b []byte) *APIError { _, e := DecodeDiagnose(b); return e }},
		{"diagnose ref", `{"reference":"P1"}`, true, func(b []byte) *APIError { _, e := DecodeDiagnose(b); return e }},
		{"diagnose null", `{"reference":null}`, false, func(b []byte) *APIError { _, e := DecodeDiagnose(b); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call([]byte(tc.body))
			if tc.ok && err != nil {
				t.Fatalf("expected admission, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}
}

func TestDecodeAppNormalizesEffectiveMembers(t *testing.T) {
	const options = `,"restore_foreground":true,"allow_borrowed":true,"allow_stop_playback":true`
	const bothSelectors = `,"target":{"task_id":-1,"app":"invalid package"}`
	const mutation = `,"reference":"O1","query":"unused","scope":"unused","app":"invalid package"`
	for _, tc := range []struct {
		kind string
		body string
		want AppRequest
	}{
		{AppKindFind,
			`,"query":"x","reference":"invalid","scope":"invalid","app":"invalid package"` + bothSelectors,
			AppRequest{Kind: AppKindFind, Query: "x", HasQuery: true}},
		{AppKindList,
			`,"reference":"P1","scope":"primary","app":"com.example","query":"` + strings.Repeat("x", MaxQueryRunes+1) + `"` + bothSelectors,
			AppRequest{Kind: AppKindList, Reference: "P1", Scope: AppScopePrimary, FilterApp: "com.example", HasFilter: true}},
		{AppKindOpen, mutation + `,"target":{"app":"com.example"}`,
			AppRequest{Kind: AppKindOpen, Reference: "O1", Target: &AppTarget{App: "com.example"}}},
		{AppKindClaim, mutation + `,"target":{"task_id":1}`,
			AppRequest{Kind: AppKindClaim, Reference: "O1", Target: &AppTarget{TaskID: 1, UseTask: true}}},
		{AppKindHandoff, mutation + `,"target":{"app":"com.example"}`,
			AppRequest{Kind: AppKindHandoff, Reference: "O1", Target: &AppTarget{App: "com.example"}, RestoreForeground: true}},
		{AppKindClose, mutation + `,"target":{"task_id":1}`,
			AppRequest{Kind: AppKindClose, Reference: "O1", Target: &AppTarget{TaskID: 1, UseTask: true}, AllowBorrowed: true}},
		{AppKindKill, mutation + `,"target":{"app":"com.example"}`,
			AppRequest{Kind: AppKindKill, Reference: "O1", Target: &AppTarget{App: "com.example"}, AllowStopPlayback: true}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			body := `{"kind":"` + tc.kind + `"` + tc.body + options + `}`
			got, err := DecodeApp([]byte(body))
			if err != nil || !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("DecodeApp = %+v, %v, want %+v", got, err, &tc.want)
			}
		})
	}
}

func TestDecodeAppRequiresReference(t *testing.T) {
	for _, kind := range AppKinds {
		if kind == AppKindFind {
			continue
		}
		for _, reference := range []string{"", `,"reference":""`} {
			t.Run(kind+reference, func(t *testing.T) {
				body := `{"kind":"` + kind + `","target":{"app":"com.example"}` + reference + `}`
				req, err := DecodeApp([]byte(body))
				if req != nil || err == nil || err.Code != CodeInvalidRequest || err.HTTPStatus != http.StatusUnprocessableEntity ||
					err.Details["field"] != "reference" || !strings.Contains(err.Message, kind) || !strings.Contains(err.Message, "reference") {
					t.Fatalf("DecodeApp = %+v, %+v, want %s missing reference error", req, err, kind)
				}
			})
		}
	}
}

func TestDecodeAppRejectsEffectiveTargetWithBothSelectors(t *testing.T) {
	for _, kind := range []string{AppKindOpen, AppKindClaim, AppKindHandoff, AppKindClose, AppKindKill} {
		t.Run(kind, func(t *testing.T) {
			body := `{"kind":"` + kind + `","reference":"O1","target":{"task_id":1,"app":"com.example"}}`
			req, err := DecodeApp([]byte(body))
			if req != nil || err == nil || err.Code != CodeInvalidRequest || err.HTTPStatus != http.StatusUnprocessableEntity {
				t.Fatalf("DecodeApp = %+v, %v, want conflicting target rejection", req, err)
			}
		})
	}
}

func TestAppKillViewFieldOrderAndPresence(t *testing.T) {
	full := &AppView{
		Kind: AppKindKill, Package: "com.example", AndroidUserID: intPtrForTest(0), SourceTaskID: intPtrForTest(42),
		TargetTaskIDs: []int{42, 43}, HasTargetTaskIDs: true, DisplayID: intPtrForTest(12),
		RemainingTaskIDs: []int{43}, HasRemainingTaskIDs: true,
		TargetPIDs: []int{101, 102}, HasTargetPIDs: true, KilledPIDs: []int{101}, HasKilledPIDs: true,
		RemainingPIDs: []int{102}, HasRemainingPIDs: true, HasRestarted: true, PackageState: "still_present",
	}
	for _, tc := range []struct {
		name string
		view *AppView
		want string
	}{
		{"full", full, `{"kind":"kill","package":"com.example","android_user_id":0,"source_task_id":42,"target_task_ids":[42,43],"display_id":12,"remaining_task_ids":[43],"target_pids":[101,102],"killed_pids":[101],"remaining_pids":[102],"restarted":null,"package_state":"still_present"}`},
		{"empty task arrays", &AppView{Kind: AppKindKill, HasTargetTaskIDs: true, HasRemainingTaskIDs: true},
			`{"kind":"kill","target_task_ids":[],"remaining_task_ids":[]}`},
		{"unknown", &AppView{Kind: AppKindKill}, `{"kind":"kill"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(marshal(t, tc.view)); got != tc.want {
				t.Fatalf("kill view = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestKeepaliveDurationAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		minutes  int
		explicit bool
	}{
		{"omitted", `{"reference":"O1"}`, 2, false},
		{"minimum", `{"reference":"O1","duration_minutes":1}`, 1, true},
		{"explicit default", `{"reference":"O1","duration_minutes":2}`, 2, true},
		{"maximum", `{"reference":"O1","duration_minutes":120}`, 120, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, apiErr := DecodeKeepalive([]byte(tc.body))
			if apiErr != nil {
				t.Fatalf("DecodeKeepalive: %v", apiErr)
			}
			minutes, apiErr := req.EffectiveDurationMinutes()
			if apiErr != nil || minutes != tc.minutes || req.HasDurationMinutes != tc.explicit {
				t.Fatalf("duration=%d presence=%v err=%v", minutes, req.HasDurationMinutes, apiErr)
			}
			if !tc.explicit && req.DurationMinutes != 0 {
				t.Fatalf("omitted duration was filled in: %+v", req)
			}
		})
	}
	invalid := []string{
		`{"reference":"O1","duration_minutes":null}`,
		`{"reference":"O1","duration_minutes":"2"}`,
		`{"reference":"O1","duration_minutes":true}`,
		`{"reference":"O1","duration_minutes":1.5}`,
		`{"reference":"O1","duration_minutes":2.0}`,
		`{"reference":"O1","duration_minutes":2e0}`,
		`{"reference":"O1","duration_minutes":0}`,
		`{"reference":"O1","duration_minutes":-1}`,
		`{"reference":"O1","duration_minutes":121}`,
		`{"reference":"O1","duration_minutes":1,"duration_minutes":2}`,
		`{"reference":"O1","Duration_minutes":2}`,
		`{"reference":"O1","duration_minutes":2,"extra":true}`,
		`{"reference":"O1","duration_minutes":2} {}`,
	}
	for _, body := range invalid {
		t.Run(body, func(t *testing.T) {
			req, apiErr := DecodeKeepalive([]byte(body))
			if req != nil || apiErr == nil || apiErr.Code != CodeInvalidRequest {
				t.Fatalf("invalid duration admitted: req=%+v err=%v", req, apiErr)
			}
		})
	}
}

func TestToolSurfaceDuplicateAndTrailingRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		call func([]byte) *APIError
	}{
		{"app duplicate kind", `{"kind":"find","kind":"list"}`,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app trailing value", `{"kind":"find"} {"kind":"list"}`,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"app duplicate reference", `{"kind":"open","reference":"O1","reference":"O2","target":{"app":"com.example"}}`,
			func(b []byte) *APIError { _, e := DecodeApp(b); return e }},
		{"notify duplicate", `{"text":"a","text":"b"}`,
			func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"notify trailing", `{"text":"a"} {"text":"b"}`,
			func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"shell duplicate", `{"reference":"O1","command":"a","command":"b"}`,
			func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"inspect duplicate", `{"reference":"O1","reference":"O2"}`,
			func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"keepalive duplicate", `{"reference":"O1","reference":"O2"}`,
			func(b []byte) *APIError { _, e := DecodeKeepalive(b); return e }},
		{"diagnose duplicate", `{"reference":"O1","reference":"O2"}`,
			func(b []byte) *APIError { _, e := DecodeDiagnose(b); return e }},
		{"diagnose trailing", `{"reference":"O1"} {"reference":"O2"}`,
			func(b []byte) *APIError { _, e := DecodeDiagnose(b); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call([]byte(tc.body)); err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}
}

func TestShellViewNullTriState(t *testing.T) {
	view := &ShellView{Termination: "unknown"}
	got := string(marshal(t, view))
	for _, want := range []string{
		`"termination":"unknown"`, `"exit_code":null`, `"output":null`, `"truncated":null`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "output_error") {
		t.Fatalf("output_error must be omitted when unset: %s", got)
	}

	zero, empty := 0, ""
	no := false
	view = &ShellView{Termination: "exited", ExitCode: &zero, Output: &empty, Truncated: &no}
	got = string(marshal(t, view))
	for _, want := range []string{
		`"exit_code":0`, `"output":""`, `"truncated":false`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestInspectionEncoding(t *testing.T) {
	view := &InspectionView{
		ObservationID: "O2", Origin: "P1", Revision: 2, CapturedAt: "2026-11-09T00:00:00Z",
		Scope: "query", View: ViewDetailed, Total: 1, HasTotal: true,
		Matches: []InspectionMatch{{
			Node: &ReadNode{Ref: "n2", Role: RoleButton, Bounds: KnownBounds(Bounds{1, 2, 3, 4})},
			Path: []string{"n1"},
		}},
	}
	got := string(marshal(t, view))
	for _, want := range []string{
		`"scope":"query"`, `"view":"detailed"`, `"matches":[`, `"node":{`,
		`"path":["n1"]`, `"total":1`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, `"defaults"`) {
		t.Fatalf("query inspection carried defaults: %s", got)
	}
	if strings.Contains(got, `"tree"`) || strings.Contains(got, `"root"`) {
		t.Fatalf("query inspection must not carry tree/root: %s", got)
	}
}

func TestResultImageFieldOrder(t *testing.T) {
	saved := true
	result := &Result{
		OperationID: "P9",
		Saved:       &saved,
		Image: &ImageMeta{
			CapturedAt: "2026-11-18T00:00:00Z",
			Width:      540, Height: 1200,
			MediaType: "image/jpeg", ReadOnly: true,
			Screen: &Screen{Width: 1080, Height: 2400, Rotation: 0},
		},
		IncludeSession: true,
		Session:        &Session{Origin: "P9", State: "active", Mode: "virtual"},
	}
	got := string(marshal(t, result))
	order := []string{`"saved"`, `"image"`, `"session"`}
	last := -1
	for _, key := range order {
		index := strings.Index(got, key)
		if index < 0 {
			t.Fatalf("missing %s in %s", key, got)
		}
		if index < last {
			t.Fatalf("%s out of order in %s", key, got)
		}
		last = index
	}
}

func TestResultToolSectionOrder(t *testing.T) {
	saved := true
	e := &APIError{Code: CodeAppLaunchFailed, Message: "x"}
	result := &Result{
		OperationID: "P9", Outcome: "not_executed",
		Shell:        &ShellView{Termination: "not_started"},
		Notification: &NotificationView{State: "unavailable", Error: e.Public()},
		Diagnosis:    &DiagnosisView{Summary: "s", Coverage: "own_records"},
		MessageID:    "M1", Saved: &saved, Error: e.Public(),
	}
	got := string(marshal(t, result))
	order := []string{`"message_id"`, `"saved"`, `"shell"`, `"notification"`, `"diagnosis"`, `"error"`}
	last := -1
	for _, key := range order {
		index := strings.Index(got, key)
		if key == `"error"` {
			// The notification section carries its own nested error; the
			// top-level error must come after every tool section.
			index = strings.LastIndex(got, key)
		}
		if index < 0 {
			t.Fatalf("missing %s in %s", key, got)
		}
		if index < last {
			t.Fatalf("%s out of order in %s", key, got)
		}
		last = index
	}
}
