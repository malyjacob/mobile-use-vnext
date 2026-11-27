package protocol

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestDecodeDiagnosisErrorCountsRoundTrip(t *testing.T) {
	original := &Result{Diagnosis: &DiagnosisView{
		Summary: "recent issues", Issues: []DiagnosisIssue{}, Coverage: "own_records",
		TimingMS: map[string]int64{"since_start": 25},
	}}
	keys := []string{CodeOperationUnknown, "uncertain", CodeDeviceBusy}
	const want = `{"diagnosis":{"summary":"recent issues","issues":[],` +
		`"error_counts":{"device_busy":10,"operation_unknown":1,"uncertain":2},` +
		`"timing_ms":{"since_start":25},"coverage":"own_records","truncated":false}}`
	values := []int{1, 2, 10}
	for offset := range keys {
		original.Diagnosis.ErrorCounts = make(map[string]int, len(keys))
		for i := range keys {
			index := (i + offset) % len(keys)
			original.Diagnosis.ErrorCounts[keys[index]] = values[index]
		}
		roundTripDecode(t, original)
		encoded, err := JSON(original)
		if err != nil || string(encoded) != want {
			t.Fatalf("canonical JSON (err=%v)\ngot  %s\nwant %s", err, encoded, want)
		}
	}

	for _, counts := range []map[string]int{nil, {}} {
		original.Diagnosis.ErrorCounts = counts
		encoded, err := JSON(original)
		if err != nil || strings.Contains(string(encoded), `"error_counts"`) {
			t.Fatalf("empty counts must be omitted (err=%v): %s", err, encoded)
		}
		// Decode into a previously populated result to pin absence as nil.
		decoded := Result{Diagnosis: &DiagnosisView{ErrorCounts: map[string]int{CodeDeviceBusy: 10}}}
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("decode diagnosis with absent error_counts: %v", err)
		}
		if decoded.Diagnosis.ErrorCounts != nil {
			t.Fatalf("absent error_counts = %v, want nil", decoded.Diagnosis.ErrorCounts)
		}
	}
}

func TestDecodeDiagnosisErrorCountsRejectsInvalid(t *testing.T) {
	tests := map[string]string{
		"null":         `null`,
		"empty object": `{}`,
		"empty key":    `{"":1}`,
		"zero":         `{"test_private_key":0}`,
		"negative":     `{"test_private_key":-1}`,
		"null count":   `{"test_private_key":null}`,
	}
	for name, counts := range tests {
		t.Run(name, func(t *testing.T) {
			body := `{"diagnosis":{"summary":"recent issues","issues":[],"coverage":"own_records",` +
				`"truncated":false,"error_counts":` + counts + `}}`
			var result Result
			err := json.Unmarshal([]byte(body), &result)
			if err == nil {
				t.Fatal("invalid error_counts accepted")
			}
			if strings.Contains(err.Error(), "test_private_key") {
				t.Fatalf("error disclosed a map key: %v", err)
			}
		})
	}
}
