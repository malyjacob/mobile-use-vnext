package protocol

import (
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
		{"apps empty", `{}`, true, func(b []byte) *APIError { _, e := DecodeApps(b); return e }},
		{"apps query", `{"query":"设置"}`, true, func(b []byte) *APIError { _, e := DecodeApps(b); return e }},
		{"apps null query", `{"query":null}`, false, func(b []byte) *APIError { _, e := DecodeApps(b); return e }},
		{"apps unknown", `{"query":"x","limit":1}`, false, func(b []byte) *APIError { _, e := DecodeApps(b); return e }},
		{"apps query too long", `{"query":"` + strings.Repeat("a", MaxQueryRunes+1) + `"}`, false,
			func(b []byte) *APIError { _, e := DecodeApps(b); return e }},

		{"open_app ok", `{"observation_id":"O1","app":"com.example"}`, true, func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},
		{"open_app missing app", `{"observation_id":"O1"}`, false, func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},
		{"open_app null app", `{"observation_id":"O1","app":null}`, false, func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},
		{"open_app bad package", `{"observation_id":"O1","app":"com..bad"}`, false, func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},
		{"open_app bad ref", `{"observation_id":"X1","app":"com.example"}`, false, func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},
		{"open_app unknown", `{"observation_id":"O1","app":"com.example","component":"x"}`, false,
			func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},

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

		{"inspect tree", `{"observation_id":"O1"}`, true, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect node", `{"observation_id":"O1","node":"n2"}`, true, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect query", `{"observation_id":"O1","query":"确定"}`, true, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect both", `{"observation_id":"O1","node":"n2","query":"x"}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect null node", `{"observation_id":"O1","node":null}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect empty node", `{"observation_id":"O1","node":""}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},
		{"inspect missing id", `{"query":"x"}`, false, func(b []byte) *APIError { _, e := DecodeInspect(b); return e }},

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

func TestToolSurfaceDuplicateAndTrailingRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		call func([]byte) *APIError
	}{
		{"apps duplicate", `{"query":"a","query":"b"}`,
			func(b []byte) *APIError { _, e := DecodeApps(b); return e }},
		{"apps trailing", `{"query":"a"} {"query":"b"}`,
			func(b []byte) *APIError { _, e := DecodeApps(b); return e }},
		{"open_app duplicate", `{"observation_id":"O1","observation_id":"O2","app":"com.example"}`,
			func(b []byte) *APIError { _, e := DecodeOpenApp(b); return e }},
		{"notify duplicate", `{"text":"a","text":"b"}`,
			func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"notify trailing", `{"text":"a"} {"text":"b"}`,
			func(b []byte) *APIError { _, e := DecodeNotify(b); return e }},
		{"shell duplicate", `{"reference":"O1","command":"a","command":"b"}`,
			func(b []byte) *APIError { _, e := DecodeShell(b); return e }},
		{"inspect duplicate", `{"observation_id":"O1","observation_id":"O2"}`,
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
		Scope: "query", Total: 1, HasTotal: true,
		Matches: []InspectionMatch{{
			Node: &Node{Ref: "n2", Role: RoleButton, Bounds: &Bounds{1, 2, 3, 4}},
			Path: []string{"n1"},
		}},
	}
	got := string(marshal(t, view))
	for _, want := range []string{
		`"scope":"query"`, `"matches":[`, `"node":{`, `"path":["n1"]`, `"total":1`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, `"tree"`) || strings.Contains(got, `"root"`) {
		t.Fatalf("query inspection must not carry tree/root: %s", got)
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
