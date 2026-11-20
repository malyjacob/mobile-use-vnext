package mcp

import (
	"strings"
	"testing"
)

func TestResultV3GroupsContiguousObservationFields(t *testing.T) {
	raw := []byte(`{"operation_id":"P1","observation":{"status":"complete","id":"Oabc_72",` +
		`"revision":2,"quality":{"state":"stable","samples":3,"stop_reason":"stable"},` +
		`"screen":{"width":1080,"height":2400,"rotation":0,"display_id":12},` +
		`"error":{"code":"image_unavailable","message":"缺图"}},"outcome":"executed"}`)
	text, err := encodeResultText(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "result v3 is_error=false\n" +
		"operation_id=P1\n" +
		"observation\n" +
		" status=complete\n" +
		" id=Oabc_72\n" +
		" revision=2\n" +
		" quality.state=stable\n" +
		" quality.samples=3\n" +
		" quality.stop_reason=stable\n" +
		" screen={\"width\":1080,\"height\":2400,\"rotation\":0,\"display_id\":12}\n" +
		" error={\"code\":\"image_unavailable\",\"message\":\"缺图\"}\n" +
		"outcome=executed\n" +
		"end result\n"
	if text != want {
		t.Fatalf("grouped result mismatch\n got:\n%s\nwant:\n%s", text, want)
	}
	if got := strings.Count(text, "\nobservation\n"); got != 1 {
		t.Fatalf("observation group count = %d, want 1", got)
	}
}

func TestResultV3ReopensObservationGroupAfterJSONFallback(t *testing.T) {
	raw := []byte(`{"observation":{"status":"complete","id":"O1",` +
		`"tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":null,"future":true}},` +
		`"missing":["image"]},"outcome":"executed"}`)
	text, err := encodeResultText(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(text, "\nobservation\n"); got != 2 {
		t.Fatalf("observation group count = %d, want 2\n%s", got, text)
	}
	fallback := `json v1 path="/observation/tree" value={"format":"full","scope":"reported_visible_with_context","view":"compact","root":{"ref":"n1","role":"root","bounds":null,"future":true}}`
	if !strings.Contains(text, fallback) {
		t.Fatalf("tree did not use typed-round-trip fallback:\n%s", text)
	}
	after := strings.Index(text, fallback)
	reopened := strings.Index(text[after+len(fallback):], "observation\n missing=[\"image\"]\n")
	if reopened < 0 {
		t.Fatalf("observation fields after fallback did not open a new group:\n%s", text)
	}
}

func TestResultV3TreeUsesObservationIDFoundAfterTree(t *testing.T) {
	raw := []byte(`{"observation":{"status":"complete",` +
		`"tree":{"format":"delta","base":"Oabc_72","view":"compact"},` +
		`"id":"Oabc_76"}}`)
	text, err := encodeResultText(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `json v1 path="/observation/tree"`) {
		t.Fatalf("tree ignored the result observation ID declared after it:\n%s", text)
	}
	if !strings.Contains(text, "\ntree v4 delta base=_72 changes=0\n") {
		t.Fatalf("tree did not use the observation ID found after it:\n%s", text)
	}
	tree := strings.Index(text, "\ntree v4 delta base=_72 changes=0\n")
	reopened := strings.Index(text[tree:], "\nobservation\n id=Oabc_76\n")
	if reopened < 0 {
		t.Fatalf("observation ID did not reopen a group after the tree:\n%s", text)
	}
}

func TestResultV3TreeWithoutReadableObservationIDUsesJSON(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		beforeTree string
	}{
		{name: "missing"},
		{name: "not-token", beforeTree: `,"id":"中文"`},
		{name: "not-observation", beforeTree: `,"id":"P1"`},
	} {
		raw := []byte(`{"observation":{"status":"complete"` + testCase.beforeTree +
			`,"tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",` +
			`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}}`)
		text, err := encodeResultText(raw, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, `json v1 path="/observation/tree"`) {
			t.Fatalf("%s: tree without a readable observation O did not fall back:\n%s", testCase.name, text)
		}
	}
}

func TestResultV3UnknownLiteralDotKeysUseAbsoluteFallbacks(t *testing.T) {
	raw := []byte(`{"observation":{"status":"complete","id":"O1","quality.state":"shadow"},` +
		`"observation.id":"top-level-shadow"}`)
	text, err := encodeResultText(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "result v3 is_error=false\n" +
		"observation\n" +
		" status=complete\n" +
		" id=O1\n" +
		"json v1 path=\"/observation/quality.state\" value=\"shadow\"\n" +
		"json v1 path=\"/observation.id\" value=\"top-level-shadow\"\n" +
		"end result\n"
	if text != want {
		t.Fatalf("literal dot keys were not preserved\n got:\n%s\nwant:\n%s", text, want)
	}
}

func TestResultV3AmbiguousDeltaDefinitionsUseJSON(t *testing.T) {
	for _, change := range []string{
		`"remove":["n2","n2"]`,
		`"add":[{"ref":"n2","role":"button"},{"ref":"n2","role":"button"}]`,
	} {
		raw := []byte(`{"observation":{"id":"Oabc_76","tree":{"format":"delta","base":"Oabc_72","view":"compact",` +
			change + `}}}`)
		text := assertResultTextParity(t, raw, false)
		if !strings.Contains(text, `json v1 path="/observation/tree"`) {
			t.Fatalf("ambiguous delta should fall back to JSON:\n%s", text)
		}
	}
}
