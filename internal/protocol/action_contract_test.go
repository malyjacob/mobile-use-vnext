package protocol

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func actOperation(t *testing.T, operation string) (*ActRequest, *APIError) {
	t.Helper()
	return DecodeAct([]byte(`{"reference":"O1","operation":` + operation + `}`))
}

// TestActEffectiveBranchMembers covers the branch-qualified presence matrix.
func TestActEffectiveBranchMembers(t *testing.T) {
	cases := []struct {
		name          string
		operation     string
		ok            bool
		bubble        bool
		hasBubble     bool
		autoSubmit    bool
		hasAutoSubmit bool
		submitRef     string
		hasSubmitRef  bool
	}{
		{"ref_tap_omitted", `{"type":"tap","target":{"ref":"n1"}}`, true, false, false, false, false, "", false},
		{"ref_tap_true", `{"type":"tap","target":{"ref":"n1"},"bubble":true}`, true, true, true, false, false, "", false},
		{"ref_tap_false", `{"type":"tap","target":{"ref":"n1"},"bubble":false}`, true, false, true, false, false, "", false},
		{"ref_long_press_false", `{"type":"long_press","target":{"ref":"n1"},"bubble":false}`, true, false, true, false, false, "", false},
		{"coords_tap_omitted", `{"type":"tap","target":{"x":1,"y":2}}`, true, false, false, false, false, "", false},
		{"coords_tap_bubble_true", `{"type":"tap","target":{"x":1,"y":2},"bubble":true}`, false, false, false, false, false, "", false},
		{"coords_tap_bubble_false", `{"type":"tap","target":{"x":1,"y":2},"bubble":false}`, false, false, false, false, false, "", false},
		{"key_bubble", `{"type":"key","key":"BACK","bubble":false}`, false, false, false, false, false, "", false},
		{"swipe_bubble", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"bubble":false}`, false, false, false, false, false, "", false},
		{"text_bubble", `{"type":"text","target":{"ref":"n1"},"text":"x","bubble":false}`, false, false, false, false, false, "", false},
		{"text_omitted", `{"type":"text","target":{"ref":"n1"},"text":"x"}`, true, false, false, false, false, "", false},
		{"text_auto_submit_true", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":true}`, true, false, false, true, true, "", false},
		{"text_auto_submit_false", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false}`, true, false, false, false, true, "", false},
		{"text_submit_ref_enter", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"enter_key"}`, true, false, false, false, false, SubmitRefEnterKey, true},
		{"text_submit_ref_node", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n42"}`, true, false, false, false, false, "n42", true},
		{"coords_text_submit_ref_node", `{"type":"text","target":{"x":1,"y":2},"text":"x","submit_ref":"n42"}`, true, false, false, false, false, "n42", true},
		{"text_auto_submit_false_with_ref", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false,"submit_ref":"n99"}`, true, false, false, false, true, "n99", true},
		{"text_both_members", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":true,"submit_ref":"enter_key"}`, true, false, false, true, true, SubmitRefEnterKey, true},
		{"tap_auto_submit", `{"type":"tap","target":{"ref":"n1"},"auto_submit":false}`, false, false, false, false, false, "", false},
		{"tap_submit_ref", `{"type":"tap","target":{"ref":"n1"},"submit_ref":"n2"}`, false, false, false, false, false, "", false},
		{"long_press_submit_ref", `{"type":"long_press","target":{"ref":"n1"},"submit_ref":"enter_key"}`, false, false, false, false, false, "", false},
		{"key_auto_submit", `{"type":"key","key":"BACK","auto_submit":true}`, false, false, false, false, false, "", false},
		{"key_submit_ref", `{"type":"key","key":"BACK","submit_ref":"n2"}`, false, false, false, false, false, "", false},
		{"swipe_auto_submit", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"auto_submit":true}`, false, false, false, false, false, "", false},
		{"scroll_submit_ref", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","submit_ref":"n2"}`, false, false, false, false, false, "", false},
		{"bubble_null", `{"type":"tap","target":{"ref":"n1"},"bubble":null}`, false, false, false, false, false, "", false},
		{"bubble_string", `{"type":"tap","target":{"ref":"n1"},"bubble":"true"}`, false, false, false, false, false, "", false},
		{"auto_submit_null", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":null}`, false, false, false, false, false, "", false},
		{"auto_submit_string", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":"false"}`, false, false, false, false, false, "", false},
		{"auto_submit_number", `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":0}`, false, false, false, false, false, "", false},
		{"submit_ref_null", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":null}`, false, false, false, false, false, "", false},
		{"submit_ref_number", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":42}`, false, false, false, false, false, "", false},
		{"submit_ref_empty", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":""}`, false, false, false, false, false, "", false},
		{"submit_ref_upper_case", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"ENTER_KEY"}`, false, false, false, false, false, "", false},
		{"submit_ref_bare_digits", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"42"}`, false, false, false, false, false, "", false},
		{"submit_ref_zero", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n0"}`, false, false, false, false, false, "", false},
		{"submit_ref_leading_zero", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n01"}`, false, false, false, false, false, "", false},
		{"submit_ref_trailing_space", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n1 "}`, false, false, false, false, false, "", false},
		{"submit_ref_enter_key_space", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":" enter_key"}`, false, false, false, false, false, "", false},
		{"submit_ref_too_long", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n` + strings.Repeat("1", MaxSubmitRefLen) + `"}`, false, false, false, false, false, "", false},
		{"submit_ref_max_len", `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n` + strings.Repeat("1", MaxSubmitRefLen-1) + `"}`, true, false, false, false, false, "n" + strings.Repeat("1", MaxSubmitRefLen-1), true},
		{"press_enter_retired", `{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":true}`, false, false, false, false, false, "", false},
		{"press_enter_retired_on_tap", `{"type":"tap","target":{"ref":"n1"},"press_enter":false}`, false, false, false, false, false, "", false},
		{"old_alias", `{"type":"tap","target":{"ref":"n1"},"allow_ancestor_fallback":false}`, false, false, false, false, false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, apiErr := actOperation(t, tc.operation)
			if tc.ok {
				if apiErr != nil {
					t.Fatalf("admission rejected: %+v", apiErr)
				}
				op := req.Operation
				if op.Bubble != tc.bubble || op.HasBubble != tc.hasBubble ||
					op.AutoSubmit != tc.autoSubmit || op.HasAutoSubmit != tc.hasAutoSubmit ||
					op.SubmitRef != tc.submitRef || op.HasSubmitRef != tc.hasSubmitRef {
					t.Fatalf("presence = bubble(%v,%v) auto_submit(%v,%v) submit_ref(%q,%v), "+
						"want bubble(%v,%v) auto_submit(%v,%v) submit_ref(%q,%v)",
						op.Bubble, op.HasBubble, op.AutoSubmit, op.HasAutoSubmit,
						op.SubmitRef, op.HasSubmitRef,
						tc.bubble, tc.hasBubble, tc.autoSubmit, tc.hasAutoSubmit,
						tc.submitRef, tc.hasSubmitRef)
				}
				return
			}
			if apiErr == nil {
				t.Fatal("expected rejection")
			}
			if apiErr.Code != CodeInvalidRequest {
				t.Fatalf("error code = %s, want %s", apiErr.Code, CodeInvalidRequest)
			}
		})
	}
}

// TestActEffectiveDefaults proves omitted and explicit true are equivalent in
// the applicable branch, while explicit false is preserved, and that only a
// text operation with an effective node submit_ref reports a submit tap.
func TestActEffectiveDefaults(t *testing.T) {
	omitted, apiErr := actOperation(t, `{"type":"tap","target":{"ref":"n1"}}`)
	if apiErr != nil || !omitted.Operation.EffectiveBubble() {
		t.Fatalf("omitted ref bubble effective = %v err=%+v", omitted, apiErr)
	}
	explicitTrue, apiErr := actOperation(t, `{"type":"tap","target":{"ref":"n1"},"bubble":true}`)
	if apiErr != nil || !explicitTrue.Operation.EffectiveBubble() {
		t.Fatalf("explicit true bubble effective = %v err=%+v", explicitTrue, apiErr)
	}
	explicitFalse, apiErr := actOperation(t, `{"type":"tap","target":{"ref":"n1"},"bubble":false}`)
	if apiErr != nil || explicitFalse.Operation.EffectiveBubble() {
		t.Fatalf("explicit false bubble effective = %v err=%+v", explicitFalse, apiErr)
	}

	textOmitted, apiErr := actOperation(t, `{"type":"text","target":{"ref":"n1"},"text":"x"}`)
	if apiErr != nil || !textOmitted.Operation.EffectiveAutoSubmit() ||
		textOmitted.Operation.EffectiveSubmitRef() != SubmitRefEnterKey ||
		textOmitted.Operation.IsSubmitTap() {
		t.Fatalf("omitted text submit defaults = %+v err=%+v", textOmitted.Operation, apiErr)
	}
	textFalse, apiErr := actOperation(t, `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false}`)
	if apiErr != nil || textFalse.Operation.EffectiveAutoSubmit() ||
		textFalse.Operation.IsSubmitTap() {
		t.Fatalf("explicit false auto_submit = %+v err=%+v", textFalse.Operation, apiErr)
	}
	textRef, apiErr := actOperation(t, `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n42"}`)
	if apiErr != nil || !textRef.Operation.EffectiveAutoSubmit() ||
		!textRef.Operation.IsSubmitTap() || textRef.Operation.EffectiveSubmitRef() != "n42" {
		t.Fatalf("explicit node submit_ref = %+v err=%+v", textRef.Operation, apiErr)
	}
	coordRef, apiErr := actOperation(t, `{"type":"text","target":{"x":1,"y":2},"text":"x","submit_ref":"n7"}`)
	if apiErr != nil || !coordRef.Operation.IsSubmitTap() {
		t.Fatalf("coordinate text submit_ref = %+v err=%+v", coordRef.Operation, apiErr)
	}
	offWithRef, apiErr := actOperation(t, `{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false,"submit_ref":"n99"}`)
	if apiErr != nil || offWithRef.Operation.EffectiveAutoSubmit() ||
		offWithRef.Operation.IsSubmitTap() {
		t.Fatalf("auto_submit=false kept the node selector: %+v err=%+v", offWithRef.Operation, apiErr)
	}
	enterRef, apiErr := actOperation(t, `{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"enter_key"}`)
	if apiErr != nil || enterRef.Operation.IsSubmitTap() {
		t.Fatalf("enter_key reported a submit tap: %+v err=%+v", enterRef.Operation, apiErr)
	}
	tapOp, apiErr := actOperation(t, `{"type":"tap","target":{"ref":"n1"}}`)
	if apiErr != nil || tapOp.Operation.IsSubmitTap() {
		t.Fatalf("tap reported a submit tap: %+v err=%+v", tapOp.Operation, apiErr)
	}
}

// TestActEncoderPreservesExplicitFalse proves the canonical client encoder does
// not collapse explicit false into an omitted member.
func TestActEncoderPreservesExplicitFalse(t *testing.T) {
	body, err := (&ActRequest{
		Reference: "O1",
		Operation: Operation{
			Type: "tap", Target: &Target{UseRef: true, Ref: "n1"},
			Bubble: false, HasBubble: true,
		},
	}).Encode()
	if err != nil {
		t.Fatalf("encode bubble false: %v", err)
	}
	if !strings.Contains(string(body), `"bubble":false`) {
		t.Fatalf("bubble=false missing from encoded body: %s", body)
	}

	body, err = (&ActRequest{
		Reference: "O1",
		Operation: Operation{
			Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
			AutoSubmit: false, HasAutoSubmit: true,
			SubmitRef: "n42", HasSubmitRef: true,
		},
	}).Encode()
	if err != nil {
		t.Fatalf("encode auto_submit false: %v", err)
	}
	for _, want := range []string{`"auto_submit":false`, `"submit_ref":"n42"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("%s missing from encoded body: %s", want, body)
		}
	}
	decoded, apiErr := DecodeAct(body)
	if apiErr != nil {
		t.Fatalf("DecodeAct(%s): %v", body, apiErr)
	}
	op := decoded.Operation
	if op.AutoSubmit || !op.HasAutoSubmit || op.SubmitRef != "n42" || !op.HasSubmitRef {
		t.Fatalf("auto_submit/submit_ref round trip = %+v", op)
	}

	body, err = (&ActRequest{
		Reference: "O1",
		Operation: Operation{Type: "tap", Target: &Target{UseRef: true, Ref: "n1"}},
	}).Encode()
	if err != nil {
		t.Fatalf("encode omitted: %v", err)
	}
	for _, retired := range []string{"bubble", "auto_submit", "submit_ref", "press_enter"} {
		if strings.Contains(string(body), retired) {
			t.Fatalf("omitted member %q was written: %s", retired, body)
		}
	}
}

// TestActEncoderRejectsInapplicableMembers proves a direct client cannot bypass
// the shared branch matrix.
func TestActEncoderRejectsInapplicableMembers(t *testing.T) {
	cases := []*ActRequest{
		{Reference: "O1", Operation: Operation{
			Type: "tap", Target: &Target{X: 1, Y: 2}, Bubble: true, HasBubble: true}},
		{Reference: "O1", Operation: Operation{
			Type: "key", Key: "BACK", AutoSubmit: true, HasAutoSubmit: true}},
		{Reference: "O1", Operation: Operation{
			Type: "tap", Target: &Target{UseRef: true, Ref: "n1"},
			SubmitRef: "n2", HasSubmitRef: true}},
		{Reference: "O1", Operation: Operation{
			Type: "swipe", From: &Point{X: 1, Y: 2}, To: &Point{X: 3, Y: 4},
			AutoSubmit: false, HasAutoSubmit: true}},
		{Reference: "O1", Operation: Operation{
			Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
			Bubble: false, HasBubble: true}},
		{Reference: "O1", Operation: Operation{
			Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
			SubmitRef: "n0", HasSubmitRef: true}},
		{Reference: "O1", Operation: Operation{
			Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
			SubmitRef: "", HasSubmitRef: true}},
		{Reference: "O1", Operation: Operation{
			Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
			SubmitRef: "caps", HasSubmitRef: true}},
	}
	for index, request := range cases {
		if _, err := request.Encode(); err == nil {
			t.Fatalf("case %d encoded an inapplicable member", index)
		}
	}
}

// TestDecodeReferenceUnification covers act/inspect and the app reference field.
func TestDecodeReferenceUnification(t *testing.T) {
	if _, apiErr := DecodeAct([]byte(`{"observation_id":"O1","operation":{"type":"key","key":"BACK"}}`)); apiErr == nil {
		t.Fatal("act accepted the retired observation_id")
	}
	if _, apiErr := DecodeInspect([]byte(`{"observation_id":"O1"}`)); apiErr == nil {
		t.Fatal("inspect accepted the retired observation_id")
	}
	if _, apiErr := DecodeInspect([]byte(`{"reference":"O1"}`)); apiErr != nil {
		t.Fatalf("inspect rejected reference: %+v", apiErr)
	}
	if _, apiErr := DecodeApp([]byte(`{"kind":"open","observation_id":"O1","target":{"app":"com.example"}}`)); apiErr == nil {
		t.Fatal("app accepted the retired observation_id")
	}
	if _, apiErr := DecodeApp([]byte(`{"kind":"open","reference":"O1","target":{"app":"com.example"}}`)); apiErr != nil {
		t.Fatalf("app open rejected target.app: %+v", apiErr)
	}
}

// TestDecodeAppOpenTargetMatrix proves open only accepts target.app and rejects
// the old top-level app or a task target before any engine work.
func TestDecodeAppOpenTargetMatrix(t *testing.T) {
	if _, apiErr := DecodeApp([]byte(`{"kind":"open","reference":"O1","app":"com.example"}`)); apiErr == nil {
		t.Fatal("open accepted top-level app")
	}
	if _, apiErr := DecodeApp([]byte(`{"kind":"open","reference":"O1","target":{"task_id":3}}`)); apiErr == nil {
		t.Fatal("open accepted a task_id target")
	}
	if _, apiErr := DecodeApp([]byte(`{"kind":"open","reference":"O1","target":{"app":"bad package"}}`)); apiErr == nil {
		t.Fatal("open accepted an invalid package")
	}
	if _, apiErr := DecodeApp([]byte(`{"kind":"claim","reference":"O1","target":{"task_id":3}}`)); apiErr != nil {
		t.Fatalf("claim rejected reference+task target: %+v", apiErr)
	}
	if _, apiErr := DecodeApp([]byte(`{"kind":"list","reference":"O1","app":"com.example"}`)); apiErr != nil {
		t.Fatalf("list rejected reference+app filter: %+v", apiErr)
	}
}

// TestAppEncoderUsesReferenceAndTargetApp proves the canonical app body shape.
func TestAppEncoderUsesReferenceAndTargetApp(t *testing.T) {
	body, err := (&AppRequest{Kind: AppKindOpen, Reference: "O1",
		Target: &AppTarget{App: "com.example"}}).Encode()
	if err != nil {
		t.Fatalf("encode open: %v", err)
	}
	encoded := string(body)
	for _, want := range []string{`"kind":"open"`, `"reference":"O1"`, `"target":{"app":"com.example"}`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("open body missing %s: %s", want, encoded)
		}
	}
	if strings.Contains(encoded, `"observation_id"`) ||
		strings.Contains(encoded, `,"app":"com.example"`) {
		t.Fatalf("open body kept the retired shape: %s", encoded)
	}
}

func decodeResult(t *testing.T, body string) (*Result, error) {
	t.Helper()
	var result Result
	err := json.Unmarshal([]byte(body), &result)
	return &result, err
}

// TestTextInputWireMatrix proves the fixed object is strict in both directions.
func TestTextInputWireMatrix(t *testing.T) {
	valid := []TextInputView{
		{Write: TextInputNotExecuted, Submit: TextInputNotExecuted},
		{Write: TextInputUncertain, Submit: TextInputNotExecuted},
		{Write: TextInputExecuted, Submit: TextInputNotExecuted},
		{Write: TextInputExecuted, Submit: TextInputUncertain},
		{Write: TextInputExecuted, Submit: TextInputExecuted},
	}
	for _, input := range valid {
		body, err := JSON(&Result{TextInput: &input})
		if err != nil {
			t.Fatalf("encode %+v: %v", input, err)
		}
		if !strings.Contains(string(body), `"text_input":{"write":"`+input.Write+`","submit":"`+input.Submit+`"}`) {
			t.Fatalf("fixed shape mismatch: %s", body)
		}
		decoded, err := decodeResult(t, string(body))
		if err != nil {
			t.Fatalf("decode %+v: %v", input, err)
		}
		if decoded.TextInput == nil || *decoded.TextInput != input {
			t.Fatalf("round trip = %+v, want %+v", decoded.TextInput, input)
		}
	}

	invalid := []string{
		`{"text_input":null}`,
		`{"text_input":{"write":"not_executed"}}`,
		`{"text_input":{"write":"not_executed","submit":null}}`,
		`{"text_input":{"write":"unknown","submit":"not_executed"}}`,
		`{"text_input":{"write":"not_executed","submit":"executed"}}`,
		`{"text_input":{"write":"uncertain","submit":"uncertain"}}`,
		`{"text_input":{"write":"not_executed","submit":"not_executed","extra":true}}`,
		`{"text_input":{"write":"executed","enter":"executed"}}`,
	}
	for _, body := range invalid {
		if _, err := decodeResult(t, body); err == nil {
			t.Fatalf("decoded invalid text_input: %s", body)
		}
	}

	if _, err := JSON(&Result{TextInput: &TextInputView{
		Write: TextInputNotExecuted, Submit: TextInputExecuted,
	}}); err == nil {
		t.Fatal("encoded an illegal text_input combination")
	}
}
