package mcp

import "testing"

// TestActPrepareCallSharesBooleanBranchValidation proves MCP admission uses the
// same strict act decoder as HTTP and direct engine entry: retired names are
// unknown, and branch-inapplicable submit members never reach a client call.
func TestActPrepareCallSharesBooleanBranchValidation(t *testing.T) {
	definition := testDefinitionByName(t, "act")
	cases := []struct {
		name string
		body string
	}{
		{"retired_observation_id", `{"observation_id":"O1","operation":{"type":"key","key":"BACK"}}`},
		{"coordinates_bubble_true", `{"reference":"O1","operation":{"type":"tap","target":{"x":1,"y":2},"bubble":true}}`},
		{"coordinates_bubble_false", `{"reference":"O1","operation":{"type":"tap","target":{"x":1,"y":2},"bubble":false}}`},
		{"key_auto_submit", `{"reference":"O1","operation":{"type":"key","key":"BACK","auto_submit":false}}`},
		{"key_submit_ref", `{"reference":"O1","operation":{"type":"key","key":"BACK","submit_ref":"enter_key"}}`},
		{"tap_submit_ref", `{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"submit_ref":"enter_key"}}`},
		{"text_bubble", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","bubble":false}}`},
		{"bubble_null", `{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"bubble":null}}`},
		{"auto_submit_null", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":null}}`},
		{"auto_submit_string", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":"false"}}`},
		{"submit_ref_null", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":null}}`},
		{"submit_ref_empty", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":""}}`},
		{"submit_ref_illegal_word", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"enter"}}`},
		{"submit_ref_zero", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n0"}}`},
		{"submit_ref_leading_zero", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n01"}}`},
		{"submit_ref_too_long", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x",` +
			`"submit_ref":"n1111111111111111111111111111111111111111111111111111111111111111"}}`},
		{"retired_press_enter", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","press_enter":false}}`},
		{"old_alias", `{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"allow_ancestor_fallback":false}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, apiErr := prepareCall(nil, definition, []byte(tc.body)); apiErr == nil {
				t.Fatal("expected admission rejection")
			}
		})
	}
	for _, valid := range []struct {
		name string
		body string
	}{
		{"bubble_false", `{"reference":"O1","operation":{"type":"tap","target":{"ref":"n1"},"bubble":false}}`},
		{"text_defaults_omitted", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x"}}`},
		{"text_auto_submit_false", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false}}`},
		{"text_submit_enter", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"enter_key"}}`},
		{"text_submit_node", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","submit_ref":"n12"}}`},
		{"text_auto_submit_false_ignores_submit_ref", `{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false,"submit_ref":"n99"}}`},
	} {
		t.Run("valid_"+valid.name, func(t *testing.T) {
			if _, apiErr := prepareCall(nil, definition, []byte(valid.body)); apiErr != nil {
				t.Fatalf("valid operation rejected: %+v", apiErr)
			}
		})
	}
}
