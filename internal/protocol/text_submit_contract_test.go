package protocol

import (
	"net/http"
	"strings"
	"testing"
)

// TestPressEnterIsRetired proves the retired press_enter member is an unknown
// member on every operation branch, never a silently ignored compatibility
// alias: admission fails with the strict 422 invalid_request.
func TestPressEnterIsRetired(t *testing.T) {
	bodies := []string{
		`{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":true}`,
		`{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":false}`,
		`{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":null}`,
		`{"type":"tap","target":{"ref":"n1"},"press_enter":false}`,
		`{"type":"long_press","target":{"ref":"n1"},"press_enter":true}`,
		`{"type":"key","key":"BACK","press_enter":false}`,
		`{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"press_enter":false}`,
		`{"type":"scroll","container":{"ref":"n1"},"direction":"down","press_enter":false}`,
	}
	for _, operation := range bodies {
		_, apiErr := actOperation(t, operation)
		if apiErr == nil {
			t.Fatalf("accepted the retired press_enter: %s", operation)
		}
		if apiErr.Code != CodeInvalidRequest || apiErr.HTTPStatus != http.StatusUnprocessableEntity {
			t.Fatalf("press_enter rejection = %s/%d, want %s/422",
				apiErr.Code, apiErr.HTTPStatus, CodeInvalidRequest)
		}
	}
	if _, apiErr := DecodeAct([]byte(`{"reference":"O1","press_enter":false,` +
		`"operation":{"type":"text","target":{"ref":"n1"},"text":"x"}}`)); apiErr == nil {
		t.Fatal("accepted a top-level press_enter")
	}
}

// TestSubmitRefAdmissionErrors proves every syntactic rejection is the strict
// 422 invalid_request, including the values that are ignored when
// auto_submit=false: the type and grammar are checked before the lenient
// ignore rule.
func TestSubmitRefAdmissionErrors(t *testing.T) {
	cases := []struct {
		name      string
		operation string
	}{
		{"auto_submit_null", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":null}`},
		{"auto_submit_int", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":0}`},
		{"auto_submit_text", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":"true"}`},
		{"submit_ref_null", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":null}`},
		{"submit_ref_int", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":9}`},
		{"submit_ref_object", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":{}}`},
		{"submit_ref_empty", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":""}`},
		{"submit_ref_enter_key_case", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"Enter_Key"}`},
		{"submit_ref_digits", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"1"}`},
		{"submit_ref_zero", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n0"}`},
		{"submit_ref_padded", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n007"}`},
		{"submit_ref_space", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":" n7"}`},
		{"submit_ref_long", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n` +
			strings.Repeat("7", MaxSubmitRefLen) + `"}`},
		{"submit_ref_ignored_still_invalid", `{"type":"text","target":{"ref":"n1"},"text":"x",` +
			`"auto_submit":false,"submit_ref":""}`},
		{"submit_ref_on_tap", `{"type":"tap","target":{"ref":"n1"},"submit_ref":"n2"}`},
		{"auto_submit_on_key", `{"type":"key","key":"BACK","auto_submit":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, apiErr := actOperation(t, tc.operation)
			if apiErr == nil {
				t.Fatalf("accepted an invalid text submission member: %s", tc.operation)
			}
			if apiErr.Code != CodeInvalidRequest || apiErr.HTTPStatus != http.StatusUnprocessableEntity {
				t.Fatalf("rejection = %s/%d, want %s/422", apiErr.Code, apiErr.HTTPStatus, CodeInvalidRequest)
			}
		})
	}
}

// TestDirectOperationValidation covers the shared branch matrix used by direct
// engine and MCP calls that never pass through the wire decoder.
func TestDirectOperationValidation(t *testing.T) {
	text := func(mutate func(*Operation)) *Operation {
		op := &Operation{Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x"}
		if mutate != nil {
			mutate(op)
		}
		return op
	}
	admitted := []*Operation{
		text(nil),
		text(func(op *Operation) { op.AutoSubmit, op.HasAutoSubmit = false, true }),
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = SubmitRefEnterKey, true }),
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = "n7", true }),
	}
	for _, op := range admitted {
		if apiErr := op.ValidateEffectiveDefaults(); apiErr != nil {
			t.Fatalf("direct validation rejected %+v: %+v", op, apiErr)
		}
	}
	rejected := []*Operation{
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = "", true }),
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = "n0", true }),
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = "7", true }),
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = "x", true }),
		text(func(op *Operation) { op.SubmitRef, op.HasSubmitRef = strings.Repeat("n", 65), true }),
		{Type: "tap", Target: &Target{UseRef: true, Ref: "n1"}, AutoSubmit: true, HasAutoSubmit: true},
		{Type: "tap", Target: &Target{UseRef: true, Ref: "n1"}, SubmitRef: "n2", HasSubmitRef: true},
		{Type: "key", Key: "BACK", SubmitRef: SubmitRefEnterKey, HasSubmitRef: true},
	}
	for _, op := range rejected {
		apiErr := op.ValidateEffectiveDefaults()
		if apiErr == nil {
			t.Fatalf("direct validation admitted %+v", op)
		}
		if apiErr.Code != CodeInvalidRequest || apiErr.HTTPStatus != http.StatusUnprocessableEntity {
			t.Fatalf("direct rejection = %s/%d, want %s/422", apiErr.Code, apiErr.HTTPStatus, CodeInvalidRequest)
		}
	}
}

// TestEffectiveSubmitSelectors pins the defaulting and tap predicate so the
// engine and MCP layers share one meaning for an omitted selector.
func TestEffectiveSubmitSelectors(t *testing.T) {
	var nilOp *Operation
	if !nilOp.EffectiveAutoSubmit() || nilOp.EffectiveSubmitRef() != SubmitRefEnterKey || nilOp.IsSubmitTap() {
		t.Fatal("nil operation defaults are not submit=true and enter_key")
	}

	textRef := &Operation{Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
		SubmitRef: "n7", HasSubmitRef: true}
	if !textRef.EffectiveAutoSubmit() || textRef.EffectiveSubmitRef() != "n7" || !textRef.IsSubmitTap() {
		t.Fatalf("node submit selector = %+v", textRef)
	}
	textOff := &Operation{Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
		AutoSubmit: false, HasAutoSubmit: true, SubmitRef: "n7", HasSubmitRef: true}
	if textOff.EffectiveAutoSubmit() || textOff.EffectiveSubmitRef() != "n7" || textOff.IsSubmitTap() {
		t.Fatalf("auto_submit=false selector = %+v", textOff)
	}
	// A non-text branch never reports a submit tap even with a stray member;
	// ValidateEffectiveDefaults is the gate that rejects such an operation.
	tap := &Operation{Type: "tap", Target: &Target{UseRef: true, Ref: "n1"},
		SubmitRef: "n7", HasSubmitRef: true}
	if tap.IsSubmitTap() {
		t.Fatal("tap reported a submit tap")
	}
}

// TestOutcomePartialValue pins the fourth global outcome value and proves the
// composite text result keeps it on the wire. The decoder stays
// forward-compatible: it reads but never enforces the producer set.
func TestOutcomePartialValue(t *testing.T) {
	if OutcomePartial != "partial" {
		t.Fatalf("OutcomePartial = %q", OutcomePartial)
	}

	body, err := JSON(&Result{OperationID: "P1", OperationState: "finished", Outcome: OutcomePartial,
		TextInput: &TextInputView{Write: TextInputExecuted, Submit: TextInputNotExecuted}})
	if err != nil {
		t.Fatalf("encode partial result: %v", err)
	}
	if !strings.Contains(string(body), `"outcome":"partial"`) ||
		!strings.Contains(string(body), `"text_input":{"write":"executed","submit":"not_executed"}`) {
		t.Fatalf("partial result shape = %s", body)
	}
	decoded, err := decodeResult(t, string(body))
	if err != nil {
		t.Fatalf("decode partial result: %v", err)
	}
	if decoded.Outcome != OutcomePartial || decoded.TextInput == nil ||
		decoded.TextInput.Submit != TextInputNotExecuted {
		t.Fatalf("partial result round trip = %+v", decoded)
	}

	// An unknown future outcome stays readable instead of failing the whole
	// result; closed-set enforcement belongs to the producer, not the decoder.
	future, err := decodeResult(t, `{"outcome":"future_value"}`)
	if err != nil || future.Outcome != "future_value" {
		t.Fatalf("forward-compatible outcome = %+v err=%v", future, err)
	}
}

// TestTextSubmitNotExecutedStatus pins the new closed error code and its 409
// status, plus the confirmed-partial error shape.
func TestTextSubmitNotExecutedStatus(t *testing.T) {
	if CodeTextSubmitNotExecuted != "text_submit_not_executed" {
		t.Fatalf("CodeTextSubmitNotExecuted = %q", CodeTextSubmitNotExecuted)
	}
	if got := Status(CodeTextSubmitNotExecuted); got != http.StatusConflict {
		t.Fatalf("status = %d, want 409", got)
	}
	apiErr := NewErrorOutcome(Status(CodeTextSubmitNotExecuted),
		CodeTextSubmitNotExecuted, "文本已写入但未提交", OutcomePartial).
		WithDetail("stage", "submit").
		WithDetail("reason", "submit_target_unconfirmed").
		WithDetail("candidate_count", 0).
		WithDetail("dispatch", map[string]any{
			"focus": "dispatched", "write": "dispatched", "submit": "not_attempted",
		})
	if apiErr.Outcome != OutcomePartial {
		t.Fatalf("error outcome = %q", apiErr.Outcome)
	}
	body, err := JSON(&Result{
		OperationID: "P1", OperationState: "finished", Outcome: OutcomePartial,
		Error:     apiErr.Public(),
		TextInput: &TextInputView{Write: TextInputExecuted, Submit: TextInputNotExecuted},
	})
	if err != nil {
		t.Fatalf("encode confirmed-partial error: %v", err)
	}
	decoded, err := decodeResult(t, string(body))
	if err != nil {
		t.Fatalf("decode confirmed-partial error: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeTextSubmitNotExecuted {
		t.Fatalf("decoded error = %+v", decoded.Error)
	}
	details := decoded.Error.Details
	if details["stage"] != "submit" || details["reason"] != "submit_target_unconfirmed" {
		t.Fatalf("decoded details = %#v", details)
	}
	dispatch, ok := details["dispatch"].(map[string]any)
	if !ok || dispatch["focus"] != "dispatched" || dispatch["write"] != "dispatched" ||
		dispatch["submit"] != "not_attempted" {
		t.Fatalf("decoded dispatch = %#v", details["dispatch"])
	}
	if strings.Contains(string(body), `"enter"`) {
		t.Fatalf("retired enter naming survived: %s", body)
	}
}

// TestChangedRefDetailContract pins changed_ref as an optional controlled
// target_changed detail that survives the result wire in both directions and
// never restates the rejection reason.
func TestChangedRefDetailContract(t *testing.T) {
	apiErr := NewError(Status(CodeTargetChanged), CodeTargetChanged, "目标已变化").
		WithDetail("reason", "lineage_changed").
		WithDetail("changed_ref", "n42")
	if got := Status(CodeTargetChanged); got != http.StatusConflict {
		t.Fatalf("target_changed status = %d, want 409", got)
	}
	body, err := JSON(&Result{
		OperationID: "P1", OperationState: "finished", Outcome: "not_executed",
		Error: apiErr.Public(),
	})
	if err != nil {
		t.Fatalf("encode changed_ref result: %v", err)
	}
	decoded, err := decodeResult(t, string(body))
	if err != nil {
		t.Fatalf("decode changed_ref result: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeTargetChanged {
		t.Fatalf("decoded error = %+v", decoded.Error)
	}
	if decoded.Error.Details["changed_ref"] != "n42" ||
		decoded.Error.Details["reason"] != "lineage_changed" {
		t.Fatalf("decoded details = %#v", decoded.Error.Details)
	}

	// The detail is optional: a rejection that cannot locate the ancestor
	// simply omits it, and no other detail key is invented.
	plain := NewError(Status(CodeTargetChanged), CodeTargetChanged, "目标已变化").
		WithDetail("reason", "lineage_changed")
	plainBody, err := JSON(&Result{Error: plain.Public()})
	if err != nil {
		t.Fatalf("encode plain target_changed: %v", err)
	}
	if strings.Contains(string(plainBody), "changed_ref") {
		t.Fatalf("changed_ref was invented: %s", plainBody)
	}
}
