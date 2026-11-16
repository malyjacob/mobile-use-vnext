package mcp

import "testing"

// TestActPrepareCallSharesBooleanBranchValidation proves MCP admission uses the
// same strict act decoder as HTTP and direct engine entry: retired names are
// unknown, and branch-inapplicable booleans never reach a client call.
func TestActPrepareCallSharesBooleanBranchValidation(t *testing.T) {
	definition := testDefinitionByName(t, "act")
	cases := []struct {
		name string
		body string
	}{
		{"retired_observation_id", `{"observation_id":"O1","operation":{"type":"key","key":"BACK"}}`},
		{"coordinates_bubble_true", `{"reference":"O1","operation":{"type":"tap","target":{"x":1,"y":2},"bubble":true}}`},
		{"coordinates_bubble_false", `{"reference":"O1","operation":{"type":"tap","target":{"x":1,"y":2},"bubble":false}}`},
		{"key_press_enter", `{"reference":"O1","operation":{"type":"key","key":"BACK","press_enter":false}}`},
		{"text_bubble", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","bubble":false}}`},
		{"bubble_null", `{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"bubble":null}}`},
		{"press_enter_null", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":null}}`},
		{"old_alias", `{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"allow_ancestor_fallback":false}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, apiErr := prepareCall(nil, definition, []byte(tc.body)); apiErr == nil {
				t.Fatal("expected admission rejection")
			}
		})
	}
	if _, apiErr := prepareCall(nil, definition,
		[]byte(`{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"bubble":false}}`)); apiErr != nil {
		t.Fatalf("valid explicit-false bubble rejected: %+v", apiErr)
	}
	if _, apiErr := prepareCall(nil, definition,
		[]byte(`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":false}}`)); apiErr != nil {
		t.Fatalf("valid explicit-false press_enter rejected: %+v", apiErr)
	}
}
