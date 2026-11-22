package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// TestFinishUsesPhaseSpecificTextStatus proves a controlled tool error can keep
// the documented 422 static-text status even when the same wire code maps to
// 409 after claim. The result JSON is unchanged.
func TestFinishUsesPhaseSpecificTextStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}
	result := &protocol.Result{
		OperationID: "Pstatic", Outcome: "not_executed",
		Error:      &protocol.Error{Code: protocol.CodeTextTargetUnknown, Message: "文字操作需要可确认的真实输入对象"},
		HTTPStatus: 422,
	}
	server.finish(recorder, result, nil)
	if recorder.Code != 422 {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	result.HTTPStatus = 0
	server.finish(recorder, result, nil)
	if recorder.Code != 409 {
		t.Fatalf("code-only status = %d, want 409", recorder.Code)
	}
}

// TestFinishTextSubmitNotExecutedIsConflict pins the partial text act on the
// HTTP boundary: the closed code maps to 409, the renamed write/submit snapshot
// stays in the body, and a delivered ready observation never changes the
// status of a confirmed partial outcome.
func TestFinishTextSubmitNotExecutedIsConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}
	result := &protocol.Result{
		OperationID: "Ppartial",
		Outcome:     protocol.OutcomePartial,
		TextInput: &protocol.TextInputView{
			Write: protocol.TextInputExecuted, Submit: protocol.TextInputNotExecuted,
		},
		Error: &protocol.Error{
			Code:    protocol.CodeTextSubmitNotExecuted,
			Message: "写入已确认，提交未派发",
			Details: map[string]any{
				"stage": "submit", "reason": "submit_target_unconfirmed", "candidate_count": 0,
				"dispatch": map[string]any{
					"focus": "not_attempted", "write": "executed", "submit": "not_attempted",
				},
			},
		},
		Observation: &protocol.Observation{Status: "complete", ID: "O2"},
		Recovery: &protocol.Recovery{
			Entry: protocol.RecoveryEntryObserve, Reference: protocol.RecoveryReferenceP,
			InputState: protocol.RecoveryInputRetired,
		},
	}
	server.finish(recorder, result, nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	var decoded protocol.Result
	if err := protocol.Decode(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if decoded.Outcome != protocol.OutcomePartial {
		t.Fatalf("outcome = %q, want partial", decoded.Outcome)
	}
	if decoded.TextInput == nil || decoded.TextInput.Write != protocol.TextInputExecuted ||
		decoded.TextInput.Submit != protocol.TextInputNotExecuted {
		t.Fatalf("text_input = %+v", decoded.TextInput)
	}
	if decoded.Error == nil || decoded.Error.Code != protocol.CodeTextSubmitNotExecuted {
		t.Fatalf("error = %+v", decoded.Error)
	}
	if decoded.Observation == nil || decoded.Observation.ID != "O2" {
		t.Fatalf("observation = %+v", decoded.Observation)
	}
	if decoded.Recovery == nil || decoded.Recovery.Reference != protocol.RecoveryReferenceP {
		t.Fatalf("recovery = %+v", decoded.Recovery)
	}
}

// TestActRouteRejectsRetiredAndIllegalSubmitMembers proves the HTTP boundary
// shares the strict act decoder: the retired press_enter name, a null or
// mistyped auto_submit and an illegal submit_ref all stop as 422 before any
// engine work.
func TestActRouteRejectsRetiredAndIllegalSubmitMembers(t *testing.T) {
	bodies := []string{
		`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":false}}`,
		`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":null}}`,
		`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":"true"}}`,
		`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":null}}`,
		`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":""}}`,
		`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"enter"}}`,
		`{"reference":"O1","operation":{"type":"key","key":"BACK","auto_submit":false}}`,
		`{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"submit_ref":"n5"}}`,
	}
	for _, body := range bodies {
		server := newAppTestServer()
		reachedEngine, status, result := serveAdmissionRequest(t, server, "act", body)
		if reachedEngine {
			t.Fatalf("illegal act text member reached the engine: %s", body)
		}
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", status, body)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest {
			t.Fatalf("error = %+v, want invalid_request: %s", result.Error, body)
		}
	}
}
