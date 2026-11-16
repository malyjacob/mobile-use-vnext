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

// TestActEffectiveBooleanMembers covers the branch-qualified presence matrix.
func TestActEffectiveBooleanMembers(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		ok        bool
		bubble    bool
		hasBubble bool
		enter     bool
		hasEnter  bool
	}{
		{"ref_tap_omitted", `{"type":"tap","target":{"ref":"n1"}}`, true, false, false, false, false},
		{"ref_tap_true", `{"type":"tap","target":{"ref":"n1"},"bubble":true}`, true, true, true, false, false},
		{"ref_tap_false", `{"type":"tap","target":{"ref":"n1"},"bubble":false}`, true, false, true, false, false},
		{"ref_long_press_false", `{"type":"long_press","target":{"ref":"n1"},"bubble":false}`, true, false, true, false, false},
		{"coords_tap_omitted", `{"type":"tap","target":{"x":1,"y":2}}`, true, false, false, false, false},
		{"coords_tap_bubble_true", `{"type":"tap","target":{"x":1,"y":2},"bubble":true}`, false, false, false, false, false},
		{"coords_tap_bubble_false", `{"type":"tap","target":{"x":1,"y":2},"bubble":false}`, false, false, false, false, false},
		{"key_bubble", `{"type":"key","key":"BACK","bubble":false}`, false, false, false, false, false},
		{"swipe_bubble", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"bubble":false}`, false, false, false, false, false},
		{"text_bubble", `{"type":"text","target":{"ref":"n1"},"text":"x","bubble":false}`, false, false, false, false, false},
		{"text_omitted", `{"type":"text","target":{"ref":"n1"},"text":"x"}`, true, false, false, false, false},
		{"text_true", `{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":true}`, true, false, false, true, true},
		{"text_false", `{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":false}`, true, false, false, false, true},
		{"tap_press_enter", `{"type":"tap","target":{"ref":"n1"},"press_enter":false}`, false, false, false, false, false},
		{"key_press_enter", `{"type":"key","key":"BACK","press_enter":false}`, false, false, false, false, false},
		{"bubble_null", `{"type":"tap","target":{"ref":"n1"},"bubble":null}`, false, false, false, false, false},
		{"bubble_string", `{"type":"tap","target":{"ref":"n1"},"bubble":"true"}`, false, false, false, false, false},
		{"press_enter_null", `{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":null}`, false, false, false, false, false},
		{"old_alias", `{"type":"tap","target":{"ref":"n1"},"allow_ancestor_fallback":false}`, false, false, false, false, false},
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
					op.PressEnter != tc.enter || op.HasPressEnter != tc.hasEnter {
					t.Fatalf("presence = bubble(%v,%v) enter(%v,%v), want bubble(%v,%v) enter(%v,%v)",
						op.Bubble, op.HasBubble, op.PressEnter, op.HasPressEnter,
						tc.bubble, tc.hasBubble, tc.enter, tc.hasEnter)
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
// the applicable branch, while explicit false is preserved.
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
	if apiErr != nil || !textOmitted.Operation.EffectivePressEnter() {
		t.Fatalf("omitted press_enter effective = %v err=%+v", textOmitted, apiErr)
	}
	textFalse, apiErr := actOperation(t, `{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":false}`)
	if apiErr != nil || textFalse.Operation.EffectivePressEnter() {
		t.Fatalf("explicit false press_enter effective = %v err=%+v", textFalse, apiErr)
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
			PressEnter: false, HasPressEnter: true,
		},
	}).Encode()
	if err != nil {
		t.Fatalf("encode press_enter false: %v", err)
	}
	if !strings.Contains(string(body), `"press_enter":false`) {
		t.Fatalf("press_enter=false missing from encoded body: %s", body)
	}

	body, err = (&ActRequest{
		Reference: "O1",
		Operation: Operation{Type: "tap", Target: &Target{UseRef: true, Ref: "n1"}},
	}).Encode()
	if err != nil {
		t.Fatalf("encode omitted: %v", err)
	}
	if strings.Contains(string(body), "bubble") || strings.Contains(string(body), "press_enter") {
		t.Fatalf("omitted members were written: %s", body)
	}
}

// TestActEncoderRejectsInapplicableMembers proves a direct client cannot bypass
// the shared branch matrix.
func TestActEncoderRejectsInapplicableMembers(t *testing.T) {
	cases := []*ActRequest{
		{Reference: "O1", Operation: Operation{
			Type: "tap", Target: &Target{X: 1, Y: 2}, Bubble: true, HasBubble: true}},
		{Reference: "O1", Operation: Operation{
			Type: "key", Key: "BACK", PressEnter: false, HasPressEnter: true}},
		{Reference: "O1", Operation: Operation{
			Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x",
			Bubble: false, HasBubble: true}},
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
		{Write: TextInputNotExecuted, Enter: TextInputNotExecuted},
		{Write: TextInputUncertain, Enter: TextInputNotExecuted},
		{Write: TextInputExecuted, Enter: TextInputNotExecuted},
		{Write: TextInputExecuted, Enter: TextInputUncertain},
		{Write: TextInputExecuted, Enter: TextInputExecuted},
	}
	for _, input := range valid {
		body, err := JSON(&Result{TextInput: &input})
		if err != nil {
			t.Fatalf("encode %+v: %v", input, err)
		}
		if !strings.Contains(string(body), `"text_input":{"write":"`+input.Write+`","enter":"`+input.Enter+`"}`) {
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
		`{"text_input":{"write":"not_executed","enter":null}}`,
		`{"text_input":{"write":"unknown","enter":"not_executed"}}`,
		`{"text_input":{"write":"not_executed","enter":"executed"}}`,
		`{"text_input":{"write":"uncertain","enter":"uncertain"}}`,
		`{"text_input":{"write":"not_executed","enter":"not_executed","extra":true}}`,
	}
	for _, body := range invalid {
		if _, err := decodeResult(t, body); err == nil {
			t.Fatalf("decoded invalid text_input: %s", body)
		}
	}

	if _, err := JSON(&Result{TextInput: &TextInputView{
		Write: TextInputNotExecuted, Enter: TextInputExecuted,
	}}); err == nil {
		t.Fatal("encoded an illegal text_input combination")
	}
}
