package protocol

import (
	"strings"
	"testing"
)

// TestAutoRecoveryStrategyVocabulary pins the three published strategy names.
// They are the whole closed set; renaming one silently would break the
// documented result v4 projection and the client mapping.
func TestAutoRecoveryStrategyVocabulary(t *testing.T) {
	for got, want := range map[string]string{
		AutoRecoveryTransientRetry:  "transient_retry",
		AutoRecoveryTextRebind:      "text_rebind",
		AutoRecoveryContainerRebind: "container_rebind",
	} {
		if got != want {
			t.Fatalf("strategy = %q, want %q", got, want)
		}
	}
}

// TestAutoRecoveryWireOrder pins the fixed member order of auto_recovery and
// its position between text_input and target_reference. Reverse control: moving
// the object or any inner member makes the exact comparison fail.
func TestAutoRecoveryWireOrder(t *testing.T) {
	result := &Result{
		OperationID:    "P1",
		OperationState: "finished",
		Outcome:        "executed",
		Phase:          "dispatch",
		TextInput:      &TextInputView{Write: TextInputExecuted, Submit: TextInputExecuted},
		AutoRecovery: &AutoRecoveryView{
			Strategy:     AutoRecoveryTextRebind,
			OriginalRef:  "n7",
			RecoveredRef: "n9",
			Attempts:     2,
		},
		TargetReference: "O2",
	}
	got, err := JSON(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	want := `{"operation_id":"P1","operation_state":"finished","outcome":"executed",` +
		`"phase":"dispatch","text_input":{"write":"executed","submit":"executed"},` +
		`"auto_recovery":{"strategy":"text_rebind","original_ref":"n7",` +
		`"recovered_ref":"n9","attempts":2},"target_reference":"O2"}`
	if string(got) != want {
		t.Fatalf("auto_recovery encoding:\n got %s\nwant %s", got, want)
	}
}

// TestAutoRecoveryRoundTrip proves the complete object survives the public
// marshal path and the strict decoder in both ref-carrying shapes. The
// coordinate-only shape proves both ref members are omitted from the wire.
func TestAutoRecoveryRoundTrip(t *testing.T) {
	views := []AutoRecoveryView{
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", RecoveredRef: "n3", Attempts: 2},
		{Strategy: AutoRecoveryContainerRebind, OriginalRef: "n3", RecoveredRef: "n8", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, Attempts: 1},
		{Strategy: AutoRecoveryTransientRetry, Attempts: 2},
	}
	for _, view := range views {
		body, err := JSON(&Result{AutoRecovery: &view})
		if err != nil {
			t.Fatalf("encode %+v: %v", view, err)
		}
		wire := string(body)
		if view.OriginalRef == "" && view.RecoveredRef == "" {
			if strings.Contains(wire, "original_ref") || strings.Contains(wire, "recovered_ref") {
				t.Fatalf("coordinate-only view invented a ref: %s", wire)
			}
		}
		decoded, err := decodeResult(t, wire)
		if err != nil {
			t.Fatalf("decode %+v from %s: %v", view, wire, err)
		}
		if decoded.AutoRecovery == nil || *decoded.AutoRecovery != view {
			t.Fatalf("round trip = %+v, want %+v", decoded.AutoRecovery, view)
		}
	}
}

// TestResultWithAutoRecoveryRoundTrip is the whole-result round trip: the new
// member coexists with the text_input snapshot and the target reference without
// perturbing either one.
func TestResultWithAutoRecoveryRoundTrip(t *testing.T) {
	result := &Result{
		OperationID:    "P9",
		OperationState: "finished",
		Outcome:        "executed",
		Phase:          "dispatch",
		TextInput:      &TextInputView{Write: TextInputExecuted, Submit: TextInputNotExecuted},
		AutoRecovery: &AutoRecoveryView{
			Strategy:     AutoRecoveryContainerRebind,
			OriginalRef:  "n4",
			RecoveredRef: "n11",
			Attempts:     2,
		},
		TargetReference: "O5",
	}
	decoded := roundTripDecode(t, result)
	if decoded.AutoRecovery == nil || decoded.AutoRecovery.Strategy != AutoRecoveryContainerRebind {
		t.Fatalf("auto_recovery lost in round trip: %#v", decoded.AutoRecovery)
	}
	if decoded.TextInput == nil || *decoded.TextInput != *result.TextInput {
		t.Fatalf("text_input lost in round trip: %#v", decoded.TextInput)
	}
}

// TestAutoRecoveryAbsentOnOldResponse proves an older service response without
// the member still decodes and keeps AutoRecovery nil instead of a zero value.
func TestAutoRecoveryAbsentOnOldResponse(t *testing.T) {
	for _, body := range []string{
		`{"operation_id":"P1","operation_state":"finished","outcome":"executed","phase":"dispatch"}`,
		`{"operation_id":"P1","text_input":{"write":"executed","submit":"not_executed"}}`,
	} {
		decoded, err := decodeResult(t, body)
		if err != nil {
			t.Fatalf("decode legacy response %s: %v", body, err)
		}
		if decoded.AutoRecovery != nil {
			t.Fatalf("absent auto_recovery invented %#v", decoded.AutoRecovery)
		}
	}
}

// TestAutoRecoveryDecodeRejectsInvalidShapes is the strict decode matrix: the
// object itself is not nullable, strategy is in the closed set, attempts is
// required and at least 1 and integral, the refs appear together or not at all,
// a transient retry repeats its ref and a rebind carries both refs, refs reject
// null, and unknown or duplicate members are refused.
func TestAutoRecoveryDecodeRejectsInvalidShapes(t *testing.T) {
	invalid := []struct {
		name string
		body string
	}{
		{"explicit null object", `{"auto_recovery":null}`},
		{"array object", `{"auto_recovery":[]}`},
		{"scalar object", `{"auto_recovery":"transient_retry"}`},
		{"empty object", `{"auto_recovery":{}}`},
		{"missing strategy", `{"auto_recovery":{"attempts":2}}`},
		{"null strategy", `{"auto_recovery":{"strategy":null,"attempts":2}}`},
		{"empty strategy", `{"auto_recovery":{"strategy":"","attempts":2}}`},
		{"unknown strategy", `{"auto_recovery":{"strategy":"wide_search","attempts":2}}`},
		{"unknown strategy with refs", `{"auto_recovery":{"strategy":"wide_search","original_ref":"n1","recovered_ref":"n2","attempts":2}}`},
		{"missing attempts", `{"auto_recovery":{"strategy":"transient_retry"}}`},
		{"null attempts", `{"auto_recovery":{"strategy":"transient_retry","attempts":null}}`},
		{"zero attempts", `{"auto_recovery":{"strategy":"transient_retry","attempts":0}}`},
		{"negative attempts", `{"auto_recovery":{"strategy":"transient_retry","attempts":-1}}`},
		{"fractional attempts", `{"auto_recovery":{"strategy":"transient_retry","attempts":2.5}}`},
		{"single-sided original ref", `{"auto_recovery":{"strategy":"transient_retry","original_ref":"n1","attempts":2}}`},
		{"single-sided recovered ref", `{"auto_recovery":{"strategy":"transient_retry","recovered_ref":"n1","attempts":2}}`},
		{"text rebind without refs", `{"auto_recovery":{"strategy":"text_rebind","attempts":2}}`},
		{"container rebind without refs", `{"auto_recovery":{"strategy":"container_rebind","attempts":2}}`},
		{"transient ref mismatch", `{"auto_recovery":{"strategy":"transient_retry","original_ref":"n1","recovered_ref":"n2","attempts":2}}`},
		{"null original ref", `{"auto_recovery":{"strategy":"text_rebind","original_ref":null,"recovered_ref":"n2","attempts":2}}`},
		{"null recovered ref", `{"auto_recovery":{"strategy":"text_rebind","original_ref":"n1","recovered_ref":null,"attempts":2}}`},
		{"unknown member", `{"auto_recovery":{"strategy":"transient_retry","attempts":2,"future":true}}`},
		{"duplicate strategy", `{"auto_recovery":{"strategy":"transient_retry","strategy":"text_rebind","attempts":2}}`},
		{"duplicate attempts", `{"auto_recovery":{"strategy":"transient_retry","attempts":2,"attempts":3}}`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeResult(t, tc.body); err == nil {
				t.Fatalf("decoded invalid auto_recovery: %s", tc.body)
			}
		})
	}
}

// TestAutoRecoveryValidityRule pins the single predicate that both directions
// share, so encoding and decoding cannot drift. The valid rows include the
// coordinate-only transient retry that omits both refs.
func TestAutoRecoveryValidityRule(t *testing.T) {
	valid := []AutoRecoveryView{
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", RecoveredRef: "n3", Attempts: 1},
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", RecoveredRef: "n3", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, Attempts: 2},
		{Strategy: AutoRecoveryTextRebind, OriginalRef: "n3", RecoveredRef: "n9", Attempts: 2},
		{Strategy: AutoRecoveryContainerRebind, OriginalRef: "n3", RecoveredRef: "n9", Attempts: 2},
	}
	for _, view := range valid {
		if !ValidAutoRecovery(&view) {
			t.Fatalf("rejected valid view %+v", view)
		}
	}
	invalid := []AutoRecoveryView{
		{Strategy: "wide_search", Attempts: 2},
		{Strategy: "", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, Attempts: 0},
		{Strategy: AutoRecoveryTransientRetry, Attempts: -1},
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, RecoveredRef: "n3", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", RecoveredRef: "n4", Attempts: 2},
		{Strategy: AutoRecoveryTextRebind, Attempts: 2},
		{Strategy: AutoRecoveryContainerRebind, Attempts: 2},
	}
	for _, view := range invalid {
		if ValidAutoRecovery(&view) {
			t.Fatalf("accepted invalid view %+v", view)
		}
	}
	if ValidAutoRecovery(nil) {
		t.Fatal("nil view is not valid")
	}
}

// TestAutoRecoveryEncodeRejectsInvalidView proves the encoder refuses to
// publish an object that breaks the shared strategy/attempts/ref rule.
func TestAutoRecoveryEncodeRejectsInvalidView(t *testing.T) {
	invalid := []AutoRecoveryView{
		{Attempts: 1},
		{Strategy: "wide_search", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry},
		{Strategy: AutoRecoveryTransientRetry, Attempts: -2},
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, RecoveredRef: "n3", Attempts: 2},
		{Strategy: AutoRecoveryTransientRetry, OriginalRef: "n3", RecoveredRef: "n4", Attempts: 2},
		{Strategy: AutoRecoveryTextRebind, Attempts: 2},
		{Strategy: AutoRecoveryContainerRebind, Attempts: 2},
	}
	for _, view := range invalid {
		if _, err := JSON(&Result{AutoRecovery: &view}); err == nil {
			t.Fatalf("encoded invalid auto_recovery: %+v", view)
		}
	}
}
