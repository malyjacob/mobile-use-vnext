package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// recoveryRequestCase drives one exact recovery ref through encodeResultText.
type recoveryRequestCase struct {
	name       string
	raw        string
	requestRef string
	want       string
}

func TestRecoveryExactReferenceMatrix(t *testing.T) {
	cases := []recoveryRequestCase{
		{
			name: "retry O uses delivered observation",
			raw:  `{"observation":{"status":"complete","id":"O4821_9"},"recovery":{"entry":"retry","reference":"O"}}`,
			want: "recovery entry=retry ref=O4821_9",
		},
		{
			name:       "retry O uses request ref",
			raw:        `{"recovery":{"entry":"retry","reference":"O"}}`,
			requestRef: "O123_4",
			want:       "recovery entry=retry ref=O123_4",
		},
		{
			name: "retry O falls back",
			raw:  `{"recovery":{"entry":"retry","reference":"O"}}`,
			want: "recovery entry=retry ref=O",
		},
		{
			name: "status O uses target reference",
			raw:  `{"target_reference":"O123_5","recovery":{"entry":"status","reference":"O"}}`,
			want: "recovery entry=status ref=O123_5",
		},
		{
			name:       "status O uses request ref",
			raw:        `{"recovery":{"entry":"status","reference":"O"}}`,
			requestRef: "O77_1",
			want:       "recovery entry=status ref=O77_1",
		},
		{
			name: "end O uses target reference",
			raw:  `{"target_reference":"O88_2","recovery":{"entry":"end","reference":"O"}}`,
			want: "recovery entry=end ref=O88_2",
		},
		{
			name: "end O falls back",
			raw:  `{"recovery":{"entry":"end","reference":"O"}}`,
			want: "recovery entry=end ref=O",
		},
		{
			name: "status P device busy returns blocking operation",
			raw:  `{"operation_id":"P1_1","blocking_operation_id":"P77_2","error":{"code":"device_busy","message":"忙"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P77_2",
		},
		{
			name: "red line 1 cross-agent device busy falls back",
			raw: `{"operation_id":"P1_1","session":{"origin":"P9_1","state":"active","mode":"virtual"},` +
				`"error":{"code":"device_busy","message":"忙"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P",
		},
		{
			name: "status P reference has no session returns target",
			raw:  `{"target_reference":"P9_1","error":{"code":"reference_has_no_session","message":"x"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P9_1",
		},
		{
			name:       "status P operation unknown uses request ref",
			raw:        `{"error":{"code":"operation_unknown","message":"x"},"recovery":{"entry":"status","reference":"P"}}`,
			requestRef: "P8_1",
			want:       "recovery entry=status ref=P8_1",
		},
		{
			name: "status P closed without request_closed returns target",
			raw:  `{"target_reference":"P5_1","error":{"code":"request_closed","message":"x"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P5_1",
		},
		{
			name: "status P uncertain operation outranks session origin",
			raw: `{"operation_id":"P6_2","outcome":"uncertain",` +
				`"session":{"origin":"P1_1","state":"ended","mode":"virtual"},` +
				`"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P6_2",
		},
		{
			name: "status P executed_revoked operation outranks session origin",
			raw: `{"operation_id":"P6_3","phase":"executed_revoked",` +
				`"session":{"origin":"P1_2","state":"ended","mode":"virtual"},` +
				`"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P6_3",
		},
		{
			name: "status P running operation",
			raw:  `{"operation_id":"P6_4","operation_state":"running","recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P6_4",
		},
		{
			name: "status P closed request keeps its own operation",
			raw:  `{"operation_id":"P6_5","request_closed":true,"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P6_5",
		},
		{
			name: "status P cleanup pending keeps its own operation",
			raw:  `{"operation_id":"P6_6","cleanup":{"state":"pending"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P6_6",
		},
		{
			name: "status P call cancelled keeps its own operation",
			raw:  `{"operation_id":"P6_7","error":{"code":"call_cancelled","message":"x"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P6_7",
		},
		{
			name: "status P cleanup pending cause keeps its own operation",
			raw:  `{"operation_id":"P6_8","recovery":{"entry":"status","reference":"P","cause":"cleanup_pending"}}`,
			want: "recovery entry=status ref=P6_8",
		},
		{
			name: "status P start operation",
			raw:  `{"start_operation_id":"P4_1","recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P4_1",
		},
		{
			name: "status P session origin",
			raw:  `{"session":{"origin":"P3_1","state":"active","mode":"virtual"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P3_1",
		},
		{
			name: "status P target reference",
			raw:  `{"target_reference":"P2_1","recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P2_1",
		},
		{
			name: "red line 4 rejected start falls back",
			raw:  `{"operation_id":"P10_1","error":{"code":"recovering","message":"恢复中"},"recovery":{"entry":"status","reference":"P"}}`,
			want: "recovery entry=status ref=P",
		},
		{
			name: "observe P session origin",
			raw:  `{"session":{"origin":"P2_2","state":"active","mode":"virtual"},"recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P2_2",
		},
		{
			name:       "observe P request observation error keeps request ref",
			raw:        `{"operation_id":"P20_1","observation":{"status":"failed","error":{"code":"hierarchy_unavailable","message":"x"}},"recovery":{"entry":"observe","reference":"P"}}`,
			requestRef: "P20_1",
			want:       "recovery entry=observe ref=P20_1",
		},
		{
			name: "observe P observation error resolves operation",
			raw:  `{"operation_id":"P20_2","observation":{"status":"failed","error":{"code":"hierarchy_unavailable","message":"x"}},"recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P20_2",
		},
		{
			name: "wait_for observation error uses operation",
			raw: `{"operation_id":"P21_3","observation":{"status":"failed","error":{"code":"hierarchy_unavailable","message":"x"}},` +
				`"wait":{"trigger":"timeout","elapsed_ms":100},"recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P21_3",
		},
		{
			name: "observe P post observation failure",
			raw:  `{"operation_id":"P21_1","recovery":{"entry":"observe","reference":"P","cause":"post_observation_failed"}}`,
			want: "recovery entry=observe ref=P21_1",
		},
		{
			name: "observe P uncertain",
			raw:  `{"operation_id":"P21_2","outcome":"uncertain","recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P21_2",
		},
		{
			name: "red line 2 observation not found falls back",
			raw:  `{"operation_id":"P22_1","error":{"code":"observation_not_found","message":"x"},"recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P",
		},
		{
			name: "red line 2 no ready observation falls back",
			raw:  `{"operation_id":"P22_2","error":{"code":"no_ready_observation","message":"x"},"recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P",
		},
		{
			name: "red line 2 content unavailable falls back",
			raw:  `{"operation_id":"P22_3","error":{"code":"content_unavailable","message":"x"},"recovery":{"entry":"observe","reference":"P"}}`,
			want: "recovery entry=observe ref=P",
		},
		{
			name: "end P start error details",
			raw:  `{"error":{"code":"session_exists","message":"x","details":{"start_operation_id":"P30_1"}},"recovery":{"entry":"end","reference":"P"}}`,
			want: "recovery entry=end ref=P30_1",
		},
		{
			name: "end P session origin",
			raw:  `{"session":{"origin":"P31_1","state":"ended","mode":"virtual"},"recovery":{"entry":"end","reference":"P"}}`,
			want: "recovery entry=end ref=P31_1",
		},
		{
			name: "end P start operation",
			raw:  `{"start_operation_id":"P32_1","recovery":{"entry":"end","reference":"P"}}`,
			want: "recovery entry=end ref=P32_1",
		},
		{
			name:       "end P request ref",
			raw:        `{"recovery":{"entry":"end","reference":"P"}}`,
			requestRef: "P33_1",
			want:       "recovery entry=end ref=P33_1",
		},
		{
			name: "red line 3 session ended without session falls back",
			raw:  `{"operation_id":"P34_1","error":{"code":"session_ended","message":"x"},"recovery":{"entry":"end","reference":"P"}}`,
			want: "recovery entry=end ref=P",
		},
		{
			name: "red line 3 control revoked falls back",
			raw:  `{"operation_id":"P34_2","error":{"code":"control_revoked","message":"x"},"recovery":{"entry":"end","reference":"P"}}`,
			want: "recovery entry=end ref=P",
		},
		{
			name: "none reference is stable",
			raw:  `{"operation_id":"P35_1","recovery":{"entry":"user","reference":"none"}}`,
			want: "recovery entry=user ref=none",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			text, err := encodeResultText([]byte(testCase.raw), true, testCase.requestRef)
			if err != nil {
				t.Fatal(err)
			}
			requireTestContains(t, text, testCase.want)
		})
	}
}

// TestRecoveryReferenceNeverRewritesStructuredJSON proves the exact ref is a
// text-only projection: the canonical structured result keeps O/P/none.
func TestRecoveryReferenceNeverRewritesStructuredJSON(t *testing.T) {
	result := &protocol.Result{
		OperationID: "P7391_2",
		Outcome:     "uncertain",
		Recovery: &protocol.Recovery{
			Entry:     protocol.RecoveryEntryStatus,
			Reference: protocol.RecoveryReferenceP,
		},
	}
	execution := &execution{result: result, requestRef: "P7391_1"}
	canonical, err := canonicalResult(execution)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatal(err)
	}
	recovery, _ := decoded["recovery"].(map[string]any)
	if recovery["reference"] != "P" {
		t.Fatalf("structured recovery.reference changed: %#v", recovery)
	}
	text := testTextContent(t, projectExecution(execution, "status", ResultModeBoth))
	requireTestContains(t, text, "recovery entry=status ref=P7391_2")
	if result.Recovery.Reference != protocol.RecoveryReferenceP {
		t.Fatal("projection mutated the source result")
	}
}

// TestPrepareCallRequestRef covers every tool that accepts a request reference.
func TestPrepareCallRequestRef(t *testing.T) {
	cases := []struct {
		tool string
		body string
		want string
	}{
		{"status", `{"reference":"O1_1"}`, "O1_1"},
		{"observe", `{"reference":"O2_1"}`, "O2_1"},
		{"snap", `{"reference":"O3_1"}`, "O3_1"},
		{"wait_for", `{"reference":"O4_1","timeout":2}`, "O4_1"},
		{"act", `{"reference":"O5_1","operation":{"type":"key","key":"BACK"}}`, "O5_1"},
		{"inspect", `{"reference":"O6_1"}`, "O6_1"},
		{"keepalive", `{"reference":"O7_1"}`, "O7_1"},
		{"end", `{"reference":"P8_1"}`, "P8_1"},
		{"app", `{"kind":"list","reference":"O9_1"}`, "O9_1"},
		{"shell", `{"reference":"O10_1","command":"true"}`, "O10_1"},
		{"diagnose", `{"reference":"O11_1"}`, "O11_1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.tool, func(t *testing.T) {
			call, apiErr := prepareCall(nil, testDefinitionByName(t, testCase.tool), []byte(testCase.body))
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			if call.requestRef != testCase.want {
				t.Fatalf("requestRef = %q, want %q", call.requestRef, testCase.want)
			}
		})
	}
	// start and notify have no reference member.
	for _, tool := range []string{"start", "notify"} {
		body := `{}`
		if tool == "notify" {
			body = `{"text":"hello"}`
		}
		call, apiErr := prepareCall(nil, testDefinitionByName(t, tool), []byte(body))
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		if call.requestRef != "" {
			t.Fatalf("%s requestRef = %q, want empty", tool, call.requestRef)
		}
	}
}

// TestRecoveryRefFallsBackForMalformedToken guards the resultToken rule: an
// invalid exact ref must not be emitted as a bare token.
func TestRecoveryRefFallsBackForMalformedToken(t *testing.T) {
	raw := []byte(`{"recovery":{"entry":"retry","reference":"O"}}`)
	text, err := encodeResultText(raw, true, "O bad")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "recovery entry=retry ref=O\n") {
		t.Fatalf("invalid request ref leaked:\n%s", text)
	}
}
