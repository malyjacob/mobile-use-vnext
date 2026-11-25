package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"
	"time"
)

// v5FullText wraps one compact full body into a complete result projection.
func v5FullText(body string, nodes string) string {
	return "result v4 ok\nobs O4821_1\n\n" +
		"tree v5 full nodes=" + nodes + "\n\n" + body + "end tree\nend result\n"
}

// v5DeltaText wraps one compact delta body into a complete result projection.
func v5DeltaText(body string, changes string, observation string) string {
	return "result v4 ok\nobs " + observation + "\n\n" +
		"tree v5 delta base=O4821_1 changes=" + changes + "\n\n" + body +
		"end tree\nend result\n"
}

// TestV5CompactGeometryBindsRoleToDeliveredForm pins the contract 28 geometry
// column: root and scroll deliver the four-sided rectangle, every other role
// delivers the two-integer center point, an unknown source rectangle writes "-",
// and a hidden geometry has no column at all. The same binding applies to the
// delivered geometry an "@" abbreviates.
func TestV5CompactGeometryBindsRoleToDeliveredForm(t *testing.T) {
	accepted := []struct {
		name  string
		body  string
		nodes string
	}{
		{
			name: "every role in its delivered form",
			body: "n1 root 0,0,1080,2400\n" +
				" n2 scroll 0,300,1080,2400 axis=y\n" +
				" n3 button 540,120 \"设置\"\n" +
				" n4 input 606,120 value=\"\"\n" +
				" n5 text 5,5 \"说明\"\n" +
				" n6 image 6,6\n" +
				" n7 group 7,7 \"分组\"\n" +
				" n8 scroll - swipe axis=both\n" +
				" n9 \"default view point is the first column\"\n",
			nodes: "9",
		},
		{
			name:  "default view keeps a point",
			body:  "n1 root 0,0,1080,2400\n n2 5,5 tap\n",
			nodes: "2",
		},
		{
			name:  "hidden geometry omits the column",
			body:  "n1 root 0,0,1080,2400\n n2 \"hidden text\"\n n3 view tap\n",
			nodes: "3",
		},
	}
	for _, testCase := range accepted {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(v5FullText(testCase.body, testCase.nodes)); failure != nil {
				t.Fatalf("valid v5 compact geometry rejected: %s %v",
					failure.reason, failure.fields)
			}
		})
	}

	rejected := []struct {
		name string
		body string
	}{
		{name: "root keeps its rectangle", body: "n1 root 540,1200\n"},
		{name: "scroll keeps its rectangle", body: "n1 root 0,0,1080,2400\n n2 scroll 540,1200\n"},
		{name: "button delivers a point", body: "n1 root 0,0,1080,2400\n n2 button 0,0,1,1\n"},
		{name: "text delivers a point", body: "n1 root 0,0,1080,2400\n n2 text 0,0,1,1 \"说明\"\n"},
		{name: "input delivers a point", body: "n1 root 0,0,1080,2400\n n2 input 0,0,1,1 value=\"\"\n"},
		{name: "default view delivers a point", body: "n1 root 0,0,1080,2400\n n2 0,0,1,1\n"},
		{name: "inverted rectangle", body: "n1 root 10,0,5,2400\n"},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(v5FullText(testCase.body, "9")); failure == nil ||
				failure.reason != "text_tree_body_invalid" {
				t.Fatalf("failure = %v, want text_tree_body_invalid", failure)
			}
		})
	}
}

// TestV5AtAbbreviationResolvesNearestDeliveredAncestor pins the "@" rule: only an
// ordinary node may abbreviate, it resolves to the nearest strict ancestor that
// actually delivered geometry (hidden and unknown columns are skipped), the
// resolved form must still match the role, and each added or replaced subtree
// starts with its own ancestor stack.
func TestV5AtAbbreviationResolvesNearestDeliveredAncestor(t *testing.T) {
	accepted := []struct {
		name  string
		body  string
		nodes string
	}{
		{
			name:  "point abbreviation follows a point ancestor",
			body:  "n1 root 0,0,1080,2400\n n2 button 540,120 \"设置\"\n  n3 view @\n",
			nodes: "3",
		},
		{
			name:  "rectangle abbreviation follows the root",
			body:  "n1 root 0,0,1080,2400\n n2 scroll @ axis=y\n",
			nodes: "2",
		},
		{
			name:  "hidden and unknown ancestors are skipped",
			body:  "n1 root 0,0,1080,2400\n n2 button 540,120 \"设置\"\n  n3 \"hidden\"\n   n4 view -\n    n5 image @\n",
			nodes: "5",
		},
	}
	for _, testCase := range accepted {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(v5FullText(testCase.body, testCase.nodes)); failure != nil {
				t.Fatalf("valid @ abbreviation rejected: %s %v", failure.reason, failure.fields)
			}
		})
	}

	// An added subtree keeps its own ancestor stack, so a nested "@" resolves
	// against the subtree root and never against the block or its base.
	addSubtree := v5DeltaText("+ n20 group 5,5 \"分组\"\n n21 view @\n", "1", "O4821_2")
	if _, failure := decodeResultText(addSubtree); failure != nil {
		t.Fatalf("subtree-local @ abbreviation rejected: %s %v", failure.reason, failure.fields)
	}

	rejected := []struct {
		name string
		text string
	}{
		{
			name: "no delivered ancestor",
			text: v5FullText("n1 root -\n n2 button @\n", "2"),
		},
		{
			name: "point ancestor cannot abbreviate a scroll rectangle",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 button 540,120 \"设置\"\n  n3 scroll @\n", "3"),
		},
		{
			name: "rectangle ancestor cannot abbreviate a button point",
			text: v5FullText("n1 root 0,0,1080,2400\n n2 button @\n", "2"),
		},
		{
			name: "subtree root has no ancestor stack",
			text: v5DeltaText("+ n20 button @\n", "1", "O4821_2"),
		},
		{
			name: "chain definition never abbreviates",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n chain [n10 n11] group @ \"分组\"\n", "3"),
		},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != "text_tree_body_invalid" {
				t.Fatalf("failure = %v, want text_tree_body_invalid", failure)
			}
		})
	}
}

// TestV5DeltaSetBoundsForms pins the explicit four forms of one delta geometry
// set: the controlled "hidden" state, an explicit null, a delivered point and a
// delivered rectangle. A set line carries no role, so an unbracketed column, an
// "@" abbreviation or an arity the projection cannot produce is rejected.
func TestV5DeltaSetBoundsForms(t *testing.T) {
	accepted := []string{
		"~ n2 bounds=hidden\n",
		"~ n2 bounds=null\n",
		"~ n2 bounds=[540,120]\n",
		"~ n2 bounds=[0,300,1080,2400]\n",
		"~ n6 bounds=[7,7] checked=false\n",
	}
	for _, body := range accepted {
		t.Run("accept "+body, func(t *testing.T) {
			if _, failure := decodeResultText(v5DeltaText(body, "1", "O4821_2")); failure != nil {
				t.Fatalf("valid set bounds rejected: %s %v", failure.reason, failure.fields)
			}
		})
	}

	rejected := []string{
		"~ n2 bounds=@\n",
		"~ n2 bounds=540,120\n",
		"~ n2 bounds=0,300,1080,2400\n",
		"~ n2 bounds=[540]\n",
		"~ n2 bounds=[1,2,3]\n",
		"~ n2 bounds=[1,2,3,4,5]\n",
		"~ n2 bounds=[]\n",
		"~ n2 bounds=hiddenish\n",
		"~ n2 bounds=[1,2]trailing\n",
	}
	for _, body := range rejected {
		t.Run("reject "+body, func(t *testing.T) {
			if _, failure := decodeResultText(v5DeltaText(body, "1", "O4821_2")); failure == nil ||
				failure.reason != "text_tree_bounds_invalid" {
				t.Fatalf("failure = %v, want text_tree_bounds_invalid", failure)
			}
		})
	}
}

// TestV5TruncatedColumnGrammar pins the contract 29 truncation column of the
// compact tree block. A complete node writes only the true fact, between
// resource= and context; the same column applies to chain and delta +/=
// definitions; a delta set line preserves both explicit booleans after
// resource=; and the detailed inspection view never carries the column at all.
func TestV5TruncatedColumnGrammar(t *testing.T) {
	acceptedFull := []struct {
		name  string
		body  string
		nodes string
	}{
		{
			name: "true after resource and before context",
			body: "n1 root 0,0,1080,2400\n" +
				" n2 button 540,120 \"设置\" truncated=true\n" +
				" n3 text \"说明\" resource=\"id/text\" truncated=true context\n",
			nodes: "3",
		},
		{
			name: "chain definition keeps the same column",
			body: "n1 root 0,0,1080,2400\n" +
				" chain [n2 n3] group 5,5 \"分组\" resource=\"id/group\" truncated=true\n",
			nodes: "3",
		},
	}
	for _, testCase := range acceptedFull {
		t.Run("full accept "+testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(
				v5FullText(testCase.body, testCase.nodes)); failure != nil {
				t.Fatalf("valid truncated full node rejected: %s %v",
					failure.reason, failure.fields)
			}
		})
	}

	acceptedDelta := []struct {
		name    string
		body    string
		changes string
	}{
		{
			name: "add and replace definitions",
			body: "+ n20 button 5,5 \"x\" resource=\"id/x\" truncated=true\n" +
				"= n24 view 1,1 resource=\"id/v\" truncated=true\n",
			changes: "2",
		},
		{
			name:    "set keeps both explicit values",
			body:    "~ n2 resource=\"id/b\" truncated=true\n~ n3 truncated=false\n",
			changes: "2",
		},
		{
			name:    "set keeps the column after an earlier flag",
			body:    "~ n4 checked=false resource=\"id/c\" truncated=true\n",
			changes: "1",
		},
		{
			name:    "set keeps an explicit false after resource",
			body:    "~ n5 resource=\"id/d\" truncated=false\n",
			changes: "1",
		},
	}
	for _, testCase := range acceptedDelta {
		t.Run("delta accept "+testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(
				v5DeltaText(testCase.body, testCase.changes, "O4821_2")); failure != nil {
				t.Fatalf("valid truncated delta line rejected: %s %v",
					failure.reason, failure.fields)
			}
		})
	}

	rejected := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name: "full column before resource",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" truncated=true resource=\"r\"\n",
				"2"),
			reason: "text_tree_body_invalid",
		},
		{
			name: "full column before axis",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 scroll 0,0,1080,2400 truncated=true axis=y\n",
				"2"),
			reason: "text_tree_body_invalid",
		},
		{
			name: "full column after context",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" resource=\"r\" context truncated=true\n",
				"2"),
			reason: "text_tree_body_invalid",
		},
		{
			name: "full column repeated",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" resource=\"r\" truncated=true truncated=true\n",
				"2"),
			reason: "text_tree_body_invalid",
		},
		{
			name: "full node writes the default false",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" truncated=false\n",
				"2"),
			reason: "text_tree_flag_default",
		},
		{
			name: "full node writes null",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" truncated=null\n",
				"2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name: "full node writes an unknown word",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" truncated=maybe\n",
				"2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name: "full node member has no value",
			text: v5FullText(
				"n1 root 0,0,1080,2400\n n2 text \"x\" truncated\n",
				"2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name: "delta add writes the default false",
			text: v5DeltaText(
				"+ n20 button 5,5 \"x\" truncated=false\n", "1", "O4821_2"),
			reason: "text_tree_flag_default",
		},
		{
			name:   "set line writes null",
			text:   v5DeltaText("~ n2 truncated=null\n", "1", "O4821_2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name:   "set line writes an unknown word",
			text:   v5DeltaText("~ n2 truncated=maybe\n", "1", "O4821_2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name:   "set line member has no value",
			text:   v5DeltaText("~ n2 truncated\n", "1", "O4821_2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name:   "set line column before resource",
			text:   v5DeltaText("~ n2 truncated=true resource=\"r\"\n", "1", "O4821_2"),
			reason: "text_tree_flag_invalid",
		},
		{
			name:   "set line column repeated",
			text:   v5DeltaText("~ n2 truncated=true truncated=false\n", "1", "O4821_2"),
			reason: "text_tree_flag_invalid",
		},
	}
	for _, testCase := range rejected {
		t.Run("reject "+testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

// TestInspectRejectsTruncatedColumn pins the detailed view half of the same
// contract: inspect v3 always delivers full text, so neither the true fact nor
// its default spelling is writable there.
func TestInspectRejectsTruncatedColumn(t *testing.T) {
	header := "inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
		"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n"
	rejected := []struct {
		name   string
		node   string
		reason string
	}{
		{
			name:   "detailed node writes true",
			node:   "n1 root [0,0,1080,2400] truncated=true\n",
			reason: "text_inspect_flag_invalid",
		},
		{
			name:   "detailed node writes false",
			node:   "n1 root [0,0,1080,2400] truncated=false\n",
			reason: "text_inspect_flag_default",
		},
		{
			name:   "detailed node writes null",
			node:   "n1 root [0,0,1080,2400] truncated=null\n",
			reason: "text_inspect_flag_invalid",
		},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			text := "result v4 ok\n\n" + header + testCase.node + "end inspect\nend result\n"
			if _, failure := decodeResultText(text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}

	t.Run("query matches keep the same ban", func(t *testing.T) {
		queryHeader := "inspect v3 scope=query observation=O1 revision=1 captured_at=22:00:00 " +
			"tree_scope=reported_visible_with_context view=detailed " +
			"matches=1 total=1 truncated=false nodes=2 entries=2\n\n"
		text := "result v4 ok\n\n" + queryHeader +
			"match 1/1 path=[n1 n2]\nn1 group [0,0,10,10]\n  n2 text [0,0,5,5] " +
			"truncated=true\nend inspect\nend result\n"
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_inspect_flag_invalid" {
			t.Fatalf("failure = %v, want text_inspect_flag_invalid", failure)
		}
	})
}

// TestObsLineVisualTokenGrammar pins the visual column's closed set, including
// the canonical range of the whole-page image percentage.
func TestObsLineVisualTokenGrammar(t *testing.T) {
	accepted := []string{"loading", "media_surface", "sparse_tree", "image_heavy(45%)", "image_heavy(52%)", "image_heavy(100%)"}
	for _, token := range accepted {
		t.Run("accept "+token, func(t *testing.T) {
			decoded, failure := decodeResultText(
				"result v4 ok\nobs O4821_85 at=22:08:55 visual=" + token + "\nend result\n")
			if failure != nil {
				t.Fatalf("valid visual rejected: %s %v", failure.reason, failure.fields)
			}
			if decoded.view.Observation == nil || decoded.view.Observation.Visual != token {
				t.Fatalf("observation visual = %+v", decoded.view.Observation)
			}
			value, ok := lookupPath(decoded.object, []string{"observation", "visual"})
			if !ok || value != token {
				t.Fatalf("projected visual = %#v, %v", value, ok)
			}
		})
	}

	rejected := []struct {
		name   string
		line   string
		reason string
	}{
		{name: "below the clamped floor", line: "visual=image_heavy(44%)", reason: "text_result_obs_invalid"},
		{name: "zero percent", line: "visual=image_heavy(0%)", reason: "text_result_obs_invalid"},
		{name: "above one hundred", line: "visual=image_heavy(101%)", reason: "text_result_obs_invalid"},
		{name: "padded percentage", line: "visual=image_heavy(045%)", reason: "text_result_obs_invalid"},
		{name: "missing percent sign", line: "visual=image_heavy(52)", reason: "text_result_obs_invalid"},
		{name: "empty percentage", line: "visual=image_heavy()", reason: "text_result_obs_invalid"},
		{name: "trailing token", line: "visual=image_heavy(52%)x", reason: "text_result_obs_invalid"},
		{name: "unknown kind", line: "visual=sparse", reason: "text_result_obs_invalid"},
		{name: "dash spelling", line: "visual=image-heavy(52%)", reason: "text_result_obs_invalid"},
		{name: "empty value", line: "visual=", reason: "text_result_obs_invalid"},
		{name: "before the capture time", line: "visual=sparse_tree at=22:08:55", reason: "text_result_obs_invalid"},
		{name: "duplicate column", line: "visual=sparse_tree visual=sparse_tree", reason: "text_result_obs_invalid"},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			text := "result v4 ok\nobs O4821_85 " + testCase.line + "\nend result\n"
			if _, failure := decodeResultText(text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}

	t.Run("quality stays rejected after a visual token", func(t *testing.T) {
		text := "result v4 ok\nobs O4821_85 visual=sparse_tree" +
			" quality=unstable(max_samples)\nend result\n"
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_result_obs_quality_present" {
			t.Fatalf("failure = %v, want text_result_obs_quality_present", failure)
		}
	})
}

func TestObsLineChangeAndHintTokenGrammar(t *testing.T) {
	const prefix = "result v4 ok\nobs O4821_85 "
	const suffix = "\nend result\n"
	for _, test := range []struct{ line, change, hint string }{
		{"change=unchanged", "unchanged", ""},
		{"hint=snap", "", "snap"},
		{"visual=loading change=detail_only hint=wait_for screen=1080x2400", "detail_only", "wait_for"},
		{"change=matched_base", "matched_base", ""},
	} {
		t.Run(test.line, func(t *testing.T) {
			decoded, failure := decodeResultText(prefix + test.line + suffix)
			if failure != nil {
				t.Fatalf("decode: %v", failure)
			}
			observation := decoded.view.Observation
			if observation == nil || observation.TreeChange != test.change || observation.Hint != test.hint {
				t.Fatalf("unexpected observation: %+v", observation)
			}
		})
	}
	for _, line := range []string{
		"change=changed", "hint=observe", "change=", "hint=",
		"change=unchanged visual=sparse_tree", "hint=snap change=unchanged",
		"screen=1080x2400 hint=snap", "change=unchanged change=matched_base", "hint=snap hint=wait_for",
	} {
		t.Run(line, func(t *testing.T) {
			if _, failure := decodeResultText(prefix + line + suffix); failure == nil || failure.reason != "text_result_obs_invalid" {
				t.Fatalf("failure = %v, want text_result_obs_invalid", failure)
			}
		})
	}
}

// TestInspectKeepsBracketedRectanglesForEveryRole pins the detailed contract: the
// inspection body always delivers the bracketed four-sided rectangle, whatever the
// role, and never the compact center point or the compact "@" abbreviation.
func TestInspectKeepsBracketedRectanglesForEveryRole(t *testing.T) {
	header := "inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
		"tree_scope=reported_visible_with_context view=detailed nodes=5 entries=5\n\n"
	accepted := header +
		"n1 root [0,0,1080,2400]\n" +
		"  n2 button [0,0,10,10] \"设置\"\n" +
		"  n3 scroll [0,300,1080,2400] swipe axis=y\n" +
		"  n4 input [0,0,10,10] value=\"\"\n" +
		"  n5 text -\n" +
		"end inspect\n"
	if _, failure := decodeResultText("result v4 ok\n\n" + accepted + "end result\n"); failure != nil {
		t.Fatalf("bracketed detailed rectangles rejected: %s %v", failure.reason, failure.fields)
	}

	rejected := []struct {
		name string
		body string
	}{
		{
			name: "point geometry",
			body: header + "n1 root [0,0,1080,2400]\n  n2 button 540,120\nend inspect\n",
		},
		{
			name: "abbreviated geometry",
			body: header + "n1 root [0,0,1080,2400]\n  n2 view @\nend inspect\n",
		},
		{
			name: "unbracketed rectangle",
			body: header + "n1 root [0,0,1080,2400]\n  n2 button 0,0,10,10\nend inspect\n",
		},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			text := "result v4 ok\n\n" + testCase.body + "end result\n"
			if _, failure := decodeResultText(text); failure == nil ||
				failure.reason != "text_inspect_body_invalid" {
				t.Fatalf("failure = %v, want text_inspect_body_invalid", failure)
			}
		})
	}
}

// TestV4TreeBlockIsNoLongerReadable pins the block-header migration: a result
// whose ordinary tree still claims the previous major version is not a readable
// block the probe would accept.
func TestV4TreeBlockIsNoLongerReadable(t *testing.T) {
	for _, text := range []string{
		"result v4 ok\nobs O4821_1\n\ntree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\ntree v4 delta base=O4821_1 changes=0\n\nend tree\nend result\n",
	} {
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_result_block_unknown" {
			t.Fatalf("failure = %v, want text_result_block_unknown", failure)
		}
	}
	// The v5 header check cannot be satisfied by a legacy block either.
	projected := &callResult{texts: []string{
		"result v4 ok\nobs O4821_1\n\ntree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
	}}
	if _, failure := treeBlockText(projected, "tree v5 full nodes="); failure == nil ||
		failure.reason != "text_tree_block_missing" {
		t.Fatalf("v4 block satisfied the v5 header check: %v", failure)
	}
}

// TestCompareResultTextToStructuredVisual pins the new observation member on the
// cross-mode gate: the text projection must state exactly the structured hint.
func TestCompareResultTextToStructuredVisual(t *testing.T) {
	structured := map[string]any{
		"operation_id": "P4821_1",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_1",
			"visual": "image_heavy(52%)",
			"tree": map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "compact",
			},
		},
	}
	withVisual := "result v4 ok op=P4821_1\nobs O4821_1 visual=image_heavy(52%)\n\n" +
		"tree v5 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	decoded, failure := decodeResultText(withVisual)
	if failure != nil {
		t.Fatalf("decode visual frame: %s %v", failure.reason, failure.fields)
	}
	var view resultView
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("encode structured fixture: %v", err)
	}
	if err := json.Unmarshal(encoded, &view); err != nil {
		t.Fatalf("decode structured fixture: %v", err)
	}
	if failure := compareResultTextToStructured(decoded, structured, view, ""); failure != nil {
		t.Fatalf("matching visual rejected: %s %v", failure.reason, failure.fields)
	}

	t.Run("omitted text visual", func(t *testing.T) {
		text := "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
			"tree v5 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
		omitted, failure := decodeResultText(text)
		if failure != nil {
			t.Fatalf("decode omitted visual: %s", failure.reason)
		}
		if failure := compareResultTextToStructured(omitted,
			deepCloneObject(structured), view, ""); failure == nil ||
			failure.reason != "both_mode_object_mismatch" {
			t.Fatalf("omitted visual = %v, want both_mode_object_mismatch", failure)
		}
	})

	t.Run("switched text visual", func(t *testing.T) {
		text := "result v4 ok op=P4821_1\nobs O4821_1 visual=sparse_tree\n\n" +
			"tree v5 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
		switched, failure := decodeResultText(text)
		if failure != nil {
			t.Fatalf("decode switched visual: %s", failure.reason)
		}
		if failure := compareResultTextToStructured(switched,
			deepCloneObject(structured), view, ""); failure == nil ||
			failure.reason != "both_mode_object_mismatch" {
			t.Fatalf("switched visual = %v, want both_mode_object_mismatch", failure)
		}
	})
}

// TestFakeResultTextRendersVisualOnObsLine proves the fake adapter and the probe
// agree on the declared column: the emitter writes the hint between the capture
// time and the screen, and the decoder reads it back unchanged.
func TestFakeResultTextRendersVisualOnObsLine(t *testing.T) {
	payload := map[string]any{
		"operation_id": "P4821_1",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_1",
			"visual": "image_heavy(52%)",
			"screen": map[string]any{
				"width": 1080, "height": 2400, "rotation": 2, "display_id": 148,
			},
		},
	}
	text := fakeResultText(payload, false)
	want := "obs O4821_1 visual=image_heavy(52%) screen=1080x2400@148 rot=2\n"
	if !containsLine(text, want) {
		t.Fatalf("fake obs line missing %q in:\n%s", want, text)
	}
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decode fake visual frame: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Observation == nil ||
		decoded.view.Observation.Visual != "image_heavy(52%)" {
		t.Fatalf("observation = %+v", decoded.view.Observation)
	}
}

// containsLine reports whether one exact line appears in text.
func containsLine(text, line string) bool {
	for _, candidate := range bytes.Split([]byte(text), []byte("\n")) {
		if string(candidate)+"\n" == line {
			return true
		}
	}
	return false
}

// TestProbeObserveAcceptsVisualHint drives one real observe check against the fake
// adapter: a classified frame must pass in both readable-and-structured mode while
// the projection still carries the v5 tree block and the v3 inspection block.
func TestProbeObserveAcceptsVisualHint(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText} {
		t.Run(mode, func(t *testing.T) {
			adapter := newFakeAdapter()
			adapter.resultMode = mode
			adapter.visual = "sparse_tree"
			session := startFakeAdapter(t, adapter)
			var output bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			runner := &probe{
				reporter: newReporter(&output), session: session, resultMode: mode,
			}
			if _, ok := runner.observeCheck(ctx, "O4821_1"); !ok {
				t.Fatalf("observe with visual failed:\n%s", output.String())
			}
			if !bytes.Contains(output.Bytes(), []byte("PASS observe")) ||
				!bytes.Contains(output.Bytes(), []byte("tree_block=v5")) {
				t.Fatalf("observe pass line lacks the v5 block:\n%s", output.String())
			}
		})
	}
}
