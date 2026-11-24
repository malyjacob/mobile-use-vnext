package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bothModeResult builds one both-mode tool result: the given text projection plus
// the structured object it must describe.
func bothModeResult(t *testing.T, text string, payload map[string]any) *sdk.CallToolResult {
	t.Helper()
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: text}},
		StructuredContent: payload,
	}
}

// compactNode builds one complete compact node object with every state flag known
// at its default, which is the shape the engine publishes for a complete node.
func compactNode(ref, role string, bounds []any, label []string, actions []string,
	children []any) map[string]any {
	labelValue := make([]any, len(label))
	for index, value := range label {
		labelValue[index] = value
	}
	actionValue := make([]any, len(actions))
	for index, value := range actions {
		actionValue[index] = value
	}
	if children == nil {
		children = []any{}
	}
	return map[string]any{
		"ref": ref, "role": role, "bounds": bounds,
		"label": labelValue, "actions": actionValue, "children": children,
		"enabled": true, "focused": false, "selected": false,
		"protected": false, "checked": false,
	}
}

// compactFullTextPayload is the structured side of one two-node compact full tree:
// a synthetic root and one labelled button delivering its center point.
func compactFullTextPayload() map[string]any {
	button := compactNode("n2", "button", []any{540, 120}, []string{"保存"}, []string{"tap"}, nil)
	root := compactNode("n1", "root", []any{0, 0, 1080, 2400}, nil, nil, []any{button})
	return map[string]any{
		"operation_id": "P4821_1",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_1",
			"tree": map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "compact", "root": root,
			},
		},
	}
}

// canonicalFullTextFrame is the canonical projection of compactFullTextPayload.
const canonicalFullTextFrame = "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
	"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"保存\"\n" +
	"end tree\nend result\n"

// TestBothModeComparesTreeBodyAgainstStructured pins the lossless contract of the
// readable tree block: in both mode the delivered body must be exactly the
// canonical projection of the structured tree, so no label, coordinate, count or
// delta set can drift between the two channels.
func TestBothModeComparesTreeBodyAgainstStructured(t *testing.T) {
	t.Run("canonical body passes", func(t *testing.T) {
		result := bothModeResult(t, canonicalFullTextFrame, compactFullTextPayload())
		if _, failure := project(result, resultModeBoth); failure != nil {
			t.Fatalf("canonical body rejected: %s %v", failure.reason, failure.fields)
		}
	})

	divergentBodies := []struct {
		name string
		text string
	}{
		{
			name: "different label",
			text: "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
				"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"取消\"\n" +
				"end tree\nend result\n",
		},
		{
			name: "different point",
			text: "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
				"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 541,120 \"保存\"\n" +
				"end tree\nend result\n",
		},
		{
			name: "different rectangle",
			text: "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
				"tree v5 full nodes=2\n\nn1 root 0,0,1080,2399\n n2 button 540,120 \"保存\"\n" +
				"end tree\nend result\n",
		},
		{
			name: "extra definition",
			text: "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
				"tree v5 full nodes=3\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"保存\"\n" +
				" n3 view 5,5\nend tree\nend result\n",
		},
		{
			name: "lost action",
			text: "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
				"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"保存\" !tap\n" +
				"end tree\nend result\n",
		},
	}
	for _, testCase := range divergentBodies {
		t.Run(testCase.name, func(t *testing.T) {
			result := bothModeResult(t, testCase.text, compactFullTextPayload())
			if _, failure := project(result, resultModeBoth); failure == nil ||
				failure.reason != "both_mode_tree_body_mismatch" {
				t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
			}
		})
	}
}

// TestBothModeComparesDeltaBodyAgainstStructured is the delta half of the same
// contract: a set line, its explicit value and the declared change count must
// describe the structured delta exactly.
func TestBothModeComparesDeltaBodyAgainstStructured(t *testing.T) {
	payload := map[string]any{
		"operation_id": "P4821_2",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_2",
			"tree": map[string]any{
				"format": treeDelta, "base": "O4821_1", "view": "compact",
				"set": map[string]any{
					"n2": map[string]any{"checked": false},
				},
			},
		},
	}
	canonical := "result v4 ok op=P4821_2\nobs O4821_2\n\n" +
		"tree v5 delta base=O4821_1 changes=1\n\n~ n2 checked=false\n" +
		"end tree\nend result\n"
	if _, failure := project(bothModeResult(t, canonical, payload), resultModeBoth); failure != nil {
		t.Fatalf("canonical delta body rejected: %s %v", failure.reason, failure.fields)
	}

	divergent := []struct {
		name string
		text string
	}{
		{
			name: "different set value",
			text: "result v4 ok op=P4821_2\nobs O4821_2\n\n" +
				"tree v5 delta base=O4821_1 changes=1\n\n~ n2 checked=true\n" +
				"end tree\nend result\n",
		},
		{
			name: "missing set line",
			text: "result v4 ok op=P4821_2\nobs O4821_2\n\n" +
				"tree v5 delta base=O4821_1 changes=0\n\n" +
				"end tree\nend result\n",
		},
		{
			name: "extra remove line",
			text: "result v4 ok op=P4821_2\nobs O4821_2\n\n" +
				"tree v5 delta base=O4821_1 changes=2\n\n~ n2 checked=false\n- n3\n" +
				"end tree\nend result\n",
		},
	}
	for _, testCase := range divergent {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := project(bothModeResult(t, testCase.text, payload),
				resultModeBoth); failure == nil ||
				failure.reason != "both_mode_tree_body_mismatch" {
				t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
			}
		})
	}
}

// TestBothModeComparesTruncatedTreeBody extends the lossless body contract to
// the contract 29 truncation column: the canonical encoder writes the compact
// true fact and both delta set spellings, so a readable block that omits,
// downgrades or invents the flag is body drift even though the grammar alone
// would accept every one of those lines.
func TestBothModeComparesTruncatedTreeBody(t *testing.T) {
	fullPayload := func(truncated, withResource bool) map[string]any {
		button := compactNode("n2", "button", []any{540, 120}, []string{"保存"},
			[]string{"tap"}, nil)
		if withResource {
			button["resource"] = "id/save"
		}
		if truncated {
			button["truncated"] = true
		}
		root := compactNode("n1", "root", []any{0, 0, 1080, 2400}, nil, nil,
			[]any{button})
		return map[string]any{
			"operation_id": "P4821_1",
			"observation": map[string]any{
				"status": observationComplete,
				"id":     "O4821_1",
				"tree": map[string]any{
					"format": treeFull, "scope": "reported_visible_with_context",
					"view": "compact", "root": root,
				},
			},
		}
	}
	fullFrame := func(line string) string {
		return "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
			"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n" + line +
			"end tree\nend result\n"
	}
	withResourceLine := " n2 button 540,120 \"保存\" resource=\"id/save\" truncated=true\n"
	bareLine := " n2 button 540,120 \"保存\" truncated=true\n"

	t.Run("canonical true fact passes", func(t *testing.T) {
		for _, testCase := range []struct {
			name         string
			line         string
			withResource bool
		}{
			{name: "after resource", line: withResourceLine, withResource: true},
			{name: "without resource", line: bareLine},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				if _, failure := project(bothModeResult(t, fullFrame(testCase.line),
					fullPayload(true, testCase.withResource)), resultModeBoth); failure != nil {
					t.Fatalf("canonical truncated body rejected: %s %v",
						failure.reason, failure.fields)
				}
			})
		}
	})

	t.Run("text omits the true fact", func(t *testing.T) {
		line := " n2 button 540,120 \"保存\" resource=\"id/save\"\n"
		if _, failure := project(bothModeResult(t, fullFrame(line),
			fullPayload(true, true)), resultModeBoth); failure == nil ||
			failure.reason != "both_mode_tree_body_mismatch" {
			t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
		}
	})

	t.Run("structured omits the true fact", func(t *testing.T) {
		if _, failure := project(bothModeResult(t, fullFrame(withResourceLine),
			fullPayload(false, true)), resultModeBoth); failure == nil ||
			failure.reason != "both_mode_tree_body_mismatch" {
			t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
		}
	})

	t.Run("text writes the forbidden default", func(t *testing.T) {
		line := " n2 button 540,120 \"保存\" resource=\"id/save\" truncated=false\n"
		if _, failure := project(bothModeResult(t, fullFrame(line),
			fullPayload(true, true)), resultModeBoth); failure == nil ||
			failure.reason != "text_tree_flag_default" {
			t.Fatalf("failure = %v, want text_tree_flag_default", failure)
		}
	})

	deltaPayload := func(set map[string]any) map[string]any {
		return map[string]any{
			"operation_id": "P4821_2",
			"observation": map[string]any{
				"status": observationComplete,
				"id":     "O4821_2",
				"tree": map[string]any{
					"format": treeDelta, "base": "O4821_1", "view": "compact",
					"set": set,
				},
			},
		}
	}
	deltaFrame := func(body string) string {
		return "result v4 ok op=P4821_2\nobs O4821_2\n\n" +
			"tree v5 delta base=O4821_1 changes=2\n\n" + body +
			"end tree\nend result\n"
	}
	canonicalDelta := "~ n2 resource=\"id/save\" truncated=true\n~ n3 truncated=false\n"
	canonicalSet := map[string]any{
		"n2": map[string]any{"resource": "id/save", "truncated": true},
		"n3": map[string]any{"truncated": false},
	}

	t.Run("canonical set spellings pass", func(t *testing.T) {
		if _, failure := project(bothModeResult(t, deltaFrame(canonicalDelta),
			deltaPayload(canonicalSet)), resultModeBoth); failure != nil {
			t.Fatalf("canonical truncated set rejected: %s %v",
				failure.reason, failure.fields)
		}
	})

	divergent := []struct {
		name string
		body string
		set  map[string]any
	}{
		{
			name: "text omits the true set member",
			body: "~ n2 resource=\"id/save\"\n~ n3 truncated=false\n",
			set:  canonicalSet,
		},
		{
			name: "text downgrades the set member",
			body: "~ n2 resource=\"id/save\" truncated=false\n~ n3 truncated=false\n",
			set:  canonicalSet,
		},
		{
			name: "structured drops the set member",
			body: canonicalDelta,
			set: map[string]any{
				"n2": map[string]any{"resource": "id/save"},
				"n3": map[string]any{"truncated": false},
			},
		},
	}
	for _, testCase := range divergent {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := project(bothModeResult(t, deltaFrame(testCase.body),
				deltaPayload(testCase.set)), resultModeBoth); failure == nil ||
				failure.reason != "both_mode_tree_body_mismatch" {
				t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
			}
		})
	}
}

// TestBothModeComparesTreeJSONFallbackAsWholeObject pins the documented fallback
// path: when the tree member cannot be written as a readable block the projection
// writes the complete object through json v1, and both mode then compares that whole
// tree object - the same value passes, any drift is rejected. No body gate runs, so
// the fallback is never reported as an unrenderable block.
func TestBothModeComparesTreeJSONFallbackAsWholeObject(t *testing.T) {
	tree := map[string]any{
		"format": treeFull, "scope": "reported_visible_with_context",
		"view": "compact",
		"root": compactNode("n1", "root", []any{0, 0, 1080, 2400}, nil, nil,
			[]any{compactNode("n2", "button", []any{540, 120}, []string{"保存"},
				[]string{"tap"}, nil)}),
	}
	payload := map[string]any{
		"operation_id": "P4821_1",
		"observation": map[string]any{
			"status": observationComplete, "id": "O4821_1", "tree": tree,
		},
	}
	treeFallback := "result v4 ok op=P4821_1\nobs O4821_1\n" +
		"json v1 path=\"/observation/tree\" value=" +
		`{"format":"full","scope":"reported_visible_with_context","view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400],"label":[],"actions":[],` +
		`"children":[{"ref":"n2","role":"button","bounds":[540,120],"label":["保存"],` +
		`"actions":["tap"],"children":[],"enabled":true,"focused":false,"selected":false,` +
		`"protected":false,"checked":false}],"enabled":true,"focused":false,` +
		`"selected":false,"protected":false,"checked":false}}` + "\nend result\n"
	if _, failure := project(bothModeResult(t, treeFallback, payload), resultModeBoth); failure != nil {
		t.Fatalf("matching tree fallback rejected: %s %v", failure.reason, failure.fields)
	}

	drifted := strings.Replace(treeFallback, `"label":["保存"]`, `"label":["取消"]`, 1)
	if drifted == treeFallback {
		t.Fatal("fallback fixture was not found for the drift")
	}
	if _, failure := project(bothModeResult(t, drifted, payload), resultModeBoth); failure == nil ||
		failure.reason != "both_mode_object_mismatch" {
		t.Fatalf("failure = %v, want both_mode_object_mismatch", failure)
	}

	// A fallback that carries a tree but drops part of it (here the child node) is
	// still a whole-object difference, so the reduced header comparison cannot hide
	// it.
	partial := strings.Replace(treeFallback,
		`,"children":[{"ref":"n2","role":"button","bounds":[540,120],"label":["保存"],`+
			`"actions":["tap"],"children":[],"enabled":true,"focused":false,"selected":false,`+
			`"protected":false,"checked":false}]`, `,"children":[]`, 1)
	if partial == treeFallback {
		t.Fatal("fallback fixture body was not found for the partial variant")
	}
	if _, failure := project(bothModeResult(t, partial, payload), resultModeBoth); failure == nil ||
		failure.reason != "both_mode_object_mismatch" {
		t.Fatalf("partial fallback = %v, want both_mode_object_mismatch", failure)
	}
}

// TestBothModeComparesLegacyChainBody proves a legal legacy definition passes the
// body comparison in both spellings: the explicit chain form is compared as it is,
// and the anonymous default-view shorthand is normalized line by line into the
// explicit form instead of exempting the whole tree. A shorthand that disagrees with
// the structured refs is still a mismatch.
func TestBothModeComparesLegacyChainBody(t *testing.T) {
	explicitChain := map[string]any{
		"chain": []any{"n2", "n3"}, "role": "group",
		"bounds": []any{5, 5}, "label": []any{"分组"}, "actions": []any{},
		"children": []any{}, "enabled": true, "focused": false, "selected": false,
		"protected": false, "checked": false,
	}
	anonymousChain := map[string]any{
		"chain": []any{"n2", "n3"}, "role": "view",
		"label": []any{}, "actions": []any{}, "children": []any{},
		"enabled": true, "focused": false, "selected": false,
		"protected": false, "checked": false,
	}
	fullPayload := func(chain map[string]any) map[string]any {
		root := compactNode("n1", "root", []any{0, 0, 1080, 2400}, nil, nil, []any{chain})
		return map[string]any{
			"operation_id": "P4821_3",
			"observation": map[string]any{
				"status": observationComplete,
				"id":     "O4821_3",
				"tree": map[string]any{
					"format": treeFull, "scope": "reported_visible_with_context",
					"view": "compact", "root": root,
				},
			},
		}
	}
	frame := "result v4 ok op=P4821_3\nobs O4821_3\n\n" +
		"tree v5 full nodes=3\n\nn1 root 0,0,1080,2400\n%s\nend tree\nend result\n"

	explicit := "result v4 ok op=P4821_3\nobs O4821_3\n\n" +
		"tree v5 full nodes=3\n\nn1 root 0,0,1080,2400\n chain [n2 n3] group 5,5 \"分组\"\n" +
		"end tree\nend result\n"
	if _, failure := project(bothModeResult(t, explicit, fullPayload(explicitChain)),
		resultModeBoth); failure != nil {
		t.Fatalf("explicit legacy chain body rejected: %s %v", failure.reason, failure.fields)
	}

	shorthand := fmt.Sprintf(frame, " [n2 n3]")
	if _, failure := project(bothModeResult(t, shorthand, fullPayload(anonymousChain)),
		resultModeBoth); failure != nil {
		t.Fatalf("normalized shorthand body rejected: %s %v", failure.reason, failure.fields)
	}

	driftedShorthand := fmt.Sprintf(frame, " [n2 n4]")
	if _, failure := project(bothModeResult(t, driftedShorthand, fullPayload(anonymousChain)),
		resultModeBoth); failure == nil ||
		failure.reason != "both_mode_tree_body_mismatch" {
		t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
	}
}

// TestBothModeFailsClosedOnUnrenderableStructuredTree pins the closed half of the
// body gate: once a readable tree v5 block exists, a structured tree the canonical
// encoder cannot spell is the drift the gate exists to reject - it is never read as
// "nothing to compare". This is the original silent-pass reproduction: a fabricated
// body next to a header-only structured tree.
func TestBothModeFailsClosedOnUnrenderableStructuredTree(t *testing.T) {
	fullFrame := func(text string, tree map[string]any) map[string]any {
		observation := map[string]any{
			"status": observationComplete, "id": "O4821_1",
		}
		if tree != nil {
			observation["tree"] = tree
		}
		return map[string]any{"operation_id": "P4821_1", "observation": observation}
	}
	fullText := "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
		"tree v5 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 540,120 \"伪造\"\n" +
		"end tree\nend result\n"

	cases := []struct {
		name  string
		tree  map[string]any
		cause string
	}{
		{
			name: "header only full tree",
			tree: map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "compact",
			},
			cause: "decode_invalid",
		},
		{
			name: "illegal structured ref",
			tree: map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "compact",
				"root": map[string]any{
					"ref": "bad ref", "role": "root", "bounds": []any{0, 0, 1080, 2400},
					"label": []any{}, "actions": []any{}, "children": []any{},
				},
			},
			cause: "decode_invalid",
		},
		{
			name: "root carries the wrong geometry form",
			tree: map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "compact",
				"root": compactNode("n1", "root", []any{540, 1200}, nil, nil, nil),
			},
			cause: "decode_invalid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, failure := project(bothModeResult(t, fullText, fullFrame(fullText, testCase.tree)),
				resultModeBoth)
			if failure == nil || failure.reason != "both_mode_tree_body_unrenderable" {
				t.Fatalf("failure = %v, want both_mode_tree_body_unrenderable", failure)
			}
			if !strings.Contains(strings.Join(failure.fields, " "), "cause="+testCase.cause) {
				t.Fatalf("fields = %v, want cause=%s", failure.fields, testCase.cause)
			}
		})
	}

	// A non-compact structured view and a missing structured tree are refused one
	// gate earlier by the object comparison, so they can never be read as "nothing
	// to compare" either.
	earlierGates := []struct {
		name string
		tree map[string]any
	}{
		{
			name: "non compact structured view",
			tree: map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "detailed",
				"root": compactNode("n1", "root", []any{0, 0, 1080, 2400}, nil, nil, nil),
			},
		},
		{name: "missing structured tree", tree: nil},
	}
	for _, testCase := range earlierGates {
		t.Run(testCase.name, func(t *testing.T) {
			_, failure := project(bothModeResult(t, fullText, fullFrame(fullText, testCase.tree)),
				resultModeBoth)
			if failure == nil {
				t.Fatal("unstructured tree was accepted")
			}
		})
	}
}

// TestObservationVisualGrammarAcrossModes pins the optional classification member
// on every projection mode: only the two documented tokens are legal, the member
// is absent or a string (never null or another type), and a frame without a tree
// never carries one.
func TestObservationVisualGrammarAcrossModes(t *testing.T) {
	withTree := func(visual any) map[string]any {
		observation := map[string]any{
			"status": observationComplete,
			"id":     "O4821_1",
			"tree": map[string]any{
				"format": treeFull, "scope": "reported_visible_with_context",
				"view": "compact", "root": compactNode("n1", "root",
					[]any{0, 0, 1080, 2400}, nil, nil, nil),
			},
		}
		if visual != nil {
			observation["visual"] = visual
		}
		return observation
	}
	payload := func(observation map[string]any) map[string]any {
		return map[string]any{"operation_id": "P4821_1", "observation": observation}
	}

	t.Run("structured accepts a legal hint", func(t *testing.T) {
		result := &sdk.CallToolResult{
			Content:           []sdk.Content{},
			StructuredContent: payload(withTree("image_heavy(52%)")),
		}
		if _, failure := project(result, resultModeStructured); failure != nil {
			t.Fatalf("legal structured visual rejected: %s %v", failure.reason, failure.fields)
		}
	})

	invalid := []struct {
		name   string
		visual any
		reason string
	}{
		{name: "unknown token", visual: "bogus(", reason: "observation_visual_invalid"},
		{name: "percentage below the clamp", visual: "image_heavy(44%)", reason: "observation_visual_invalid"},
		{name: "empty string", visual: "", reason: "observation_visual_invalid"},
		{name: "explicit null", visual: nil, reason: "observation_visual_unexpected"},
	}
	for _, testCase := range invalid {
		t.Run("structured rejects "+testCase.name, func(t *testing.T) {
			observation := withTree(nil)
			switch testCase.name {
			case "explicit null":
				observation["visual"] = nil
			default:
				observation["visual"] = testCase.visual
			}
			result := &sdk.CallToolResult{
				Content:           []sdk.Content{},
				StructuredContent: payload(observation),
			}
			if _, failure := project(result, resultModeStructured); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}

	t.Run("structured refuses a non-string member", func(t *testing.T) {
		// The typed projection cannot decode a numeric member at all, so the
		// shape is refused before the member grammar is reached.
		observation := withTree(nil)
		observation["visual"] = 52
		result := &sdk.CallToolResult{
			Content:           []sdk.Content{},
			StructuredContent: payload(observation),
		}
		if _, failure := project(result, resultModeStructured); failure == nil {
			t.Fatal("numeric visual member accepted")
		}
	})

	t.Run("structured rejects a hint without a tree", func(t *testing.T) {
		observation := map[string]any{
			"status": "failed", "missing": []any{"tree"}, "visual": "sparse_tree",
		}
		result := &sdk.CallToolResult{
			Content:           []sdk.Content{},
			StructuredContent: payload(observation),
		}
		if _, failure := project(result, resultModeStructured); failure == nil ||
			failure.reason != "observation_visual_without_tree" {
			t.Fatalf("failure = %v, want observation_visual_without_tree", failure)
		}
	})

	t.Run("text rejects a hint without a tree", func(t *testing.T) {
		text := "result v4 ok\nobs O4821_1 visual=sparse_tree\nend result\n"
		result := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
		if _, failure := project(result, resultModeText); failure == nil ||
			failure.reason != "observation_visual_without_tree" {
			t.Fatalf("failure = %v, want observation_visual_without_tree", failure)
		}
	})

	t.Run("text accepts a hint with a tree", func(t *testing.T) {
		text := "result v4 ok\nobs O4821_1 visual=sparse_tree\n\n" +
			"tree v5 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
		result := &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
		if _, failure := project(result, resultModeText); failure != nil {
			t.Fatalf("legal text visual rejected: %s %v", failure.reason, failure.fields)
		}
	})
}

// TestProbeObserveRejectsInvalidStructuredVisual closes the structured-only blind
// spot: without a readable block there is no parser to police the classification
// member, so the projection gate itself must refuse an impossible token.
func TestProbeObserveRejectsInvalidStructuredVisual(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.resultMode = resultModeStructured
	adapter.visual = "image_heavy(44%)"
	session := startFakeAdapter(t, adapter)
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runner := &probe{reporter: newReporter(&output), session: session, resultMode: resultModeStructured}
	if _, ok := runner.observeCheck(ctx, "O4821_1"); ok {
		t.Fatalf("impossible structured visual passed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "observation_visual_invalid") {
		t.Fatalf("invalid structured visual was not attributed:\n%s", output.String())
	}
}

// TestFakeAdapterTreeBodyGateIsLive proves the body comparison is actually reached
// on the fake adapter's complete-tree payload shape, and that a one-pixel drift in
// the readable body is reported as a mismatch instead of passing silently.
func TestFakeAdapterTreeBodyGateIsLive(t *testing.T) {
	adapter := newFakeAdapter()
	payload := map[string]any{
		"operation_id": "P4821_1",
		"observation":  adapter.observation("O4821_1", "P4821_1", 1, false),
	}
	original := adapter.result(payload, "")
	if len(original.Content) != 1 {
		t.Fatalf("fake result content = %d", len(original.Content))
	}
	text, ok := original.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("fake result content is %T", original.Content[0])
	}
	if _, failure := project(original, resultModeBoth); failure != nil {
		t.Fatalf("fake adapter frame rejected: %s %v", failure.reason, failure.fields)
	}
	if !strings.Contains(text.Text, "n1 root 0,0,1080,2400") {
		t.Fatalf("fake frame lacks the canonical root body:\n%s", text.Text)
	}
	drifted := strings.Replace(text.Text, "n1 root 0,0,1080,2400",
		"n1 root 0,0,1080,2399", 1)
	if drifted == text.Text {
		t.Fatal("fake frame body was not found for the drift")
	}
	corrupted := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: drifted}},
		StructuredContent: payload,
	}
	if _, failure := project(corrupted, resultModeBoth); failure == nil ||
		failure.reason != "both_mode_tree_body_mismatch" {
		t.Fatalf("failure = %v, want both_mode_tree_body_mismatch", failure)
	}
}
