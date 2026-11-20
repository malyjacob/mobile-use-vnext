package main

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseResultMode(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		if actual, ok := parseResultMode(mode); !ok || actual != mode {
			t.Fatalf("parseResultMode(%q) = %q, %v", mode, actual, ok)
		}
	}
	for _, value := range []string{"", "BOTH", "json", " both", "text "} {
		if actual, ok := parseResultMode(value); ok || actual != "" {
			t.Fatalf("parseResultMode(%q) = %q, %v", value, actual, ok)
		}
	}
}

func TestStdioResultModePassThrough(t *testing.T) {
	args := stdioAdapterArgs("private.json", resultModeText)
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"-transport\x00stdio",
		"-config\x00private.json",
		"-result-mode\x00text",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("stdio args omit %q: %#v", want, args)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHTTPResultModeHeader(t *testing.T) {
	var seenAuthorization string
	var seenMode string
	client := &http.Client{Transport: &bearerTransport{
		base: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			seenAuthorization = request.Header.Get("Authorization")
			seenMode = request.Header.Get(resultModeHeader)
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
		token:      "private-token",
		resultMode: resultModeStructured,
	}}
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if seenAuthorization != "Bearer private-token" {
		t.Fatalf("authorization header = %q", seenAuthorization)
	}
	if seenMode != resultModeStructured {
		t.Fatalf("result mode header = %q", seenMode)
	}
}

func TestProjectResultModes(t *testing.T) {
	payload := map[string]any{
		"operation_id": "P1",
		"outcome":      outcomeExecuted,
		"session": map[string]any{
			"origin": "P1", "state": sessionActive, "mode": "virtual",
		},
	}
	text := fakeResultText(payload, false)
	image := &sdk.ImageContent{Data: testJPEG(4096), MIMEType: imageJPEG}

	t.Run("both", func(t *testing.T) {
		projected, failure := project(&sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: text},
				image,
			},
			StructuredContent: payload,
		}, resultModeBoth)
		if failure != nil {
			t.Fatalf("project: %v", failure.reason)
		}
		if projected.view.OperationID != "P1" || len(projected.images) != 1 {
			t.Fatalf("unexpected projection: %+v", projected.view)
		}
	})

	t.Run("text", func(t *testing.T) {
		projected, failure := project(&sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: text},
				image,
			},
		}, resultModeText)
		if failure != nil {
			t.Fatalf("project: %v", failure.reason)
		}
		if projected.view.OperationID != "P1" || len(projected.structured) != 0 ||
			len(projected.images) != 1 {
			t.Fatalf("unexpected projection: %+v", projected)
		}
	})

	t.Run("structured success", func(t *testing.T) {
		projected, failure := project(&sdk.CallToolResult{
			Content:           []sdk.Content{},
			StructuredContent: payload,
		}, resultModeStructured)
		if failure != nil {
			t.Fatalf("project: %v", failure.reason)
		}
		if projected.view.OperationID != "P1" || len(projected.texts) != 0 {
			t.Fatalf("unexpected projection: %+v", projected)
		}
	})

	t.Run("structured success with image", func(t *testing.T) {
		projected, failure := project(&sdk.CallToolResult{
			Content:           []sdk.Content{image},
			StructuredContent: payload,
		}, resultModeStructured)
		if failure != nil {
			t.Fatalf("project: %v", failure.reason)
		}
		if len(projected.texts) != 0 || len(projected.images) != 1 {
			t.Fatalf("unexpected projection: %+v", projected)
		}
	})

	t.Run("structured error", func(t *testing.T) {
		errorPayload := map[string]any{
			"error": map[string]any{"code": "invalid_request"},
		}
		projected, failure := project(&sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{Text: fakeDiagnosticText(errorPayload)},
			},
			StructuredContent: errorPayload,
			IsError:           true,
		}, resultModeStructured)
		if failure != nil {
			t.Fatalf("project: %v", failure.reason)
		}
		if projected.view.Error == nil || projected.view.Error.Code != "invalid_request" {
			t.Fatalf("unexpected projection: %+v", projected.view)
		}
	})
}

// qualityProjected builds one probe projection from a hand-written observation
// object. The expected values stay literals in each case table.
func qualityProjected(t *testing.T, observation string) *callResult {
	t.Helper()
	body := `{"observation":` + observation + `}`
	object, err := exactObject([]byte(body))
	if err != nil {
		t.Fatalf("observation fixture: %v", err)
	}
	var view resultView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("observation fixture: %v", err)
	}
	return &callResult{object: object, structured: []byte(body), view: view}
}

func TestCheckObservationQuality(t *testing.T) {
	cases := []struct {
		name        string
		observation string
		reason      string
	}{
		{
			name:        "stable sampled",
			observation: `{"status":"complete","quality":{"state":"stable","samples":2,"stop_reason":"stable"}}`,
		},
		{
			name:        "unknown not sampled zero",
			observation: `{"status":"complete","quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"}}`,
		},
		{
			name:        "unknown budget one sample",
			observation: `{"quality":{"state":"unknown","samples":1,"stop_reason":"budget"}}`,
		},
		{
			name:        "missing quality",
			observation: `{"status":"complete"}`,
			reason:      "observation_quality_missing",
		},
		{
			name:        "explicit null quality",
			observation: `{"quality":null}`,
			reason:      "observation_quality_missing",
		},
		{
			name:        "missing samples member",
			observation: `{"quality":{"state":"stable","stop_reason":"stable"}}`,
			reason:      "observation_quality_member_missing",
		},
		{
			name:        "invalid state",
			observation: `{"quality":{"state":"moving","samples":2,"stop_reason":"stable"}}`,
			reason:      "observation_quality_state_invalid",
		},
		{
			name:        "negative samples",
			observation: `{"quality":{"state":"stable","samples":-1,"stop_reason":"stable"}}`,
			reason:      "observation_quality_samples_invalid",
		},
		{
			name:        "invalid stop reason",
			observation: `{"quality":{"state":"stable","samples":2,"stop_reason":"settled"}}`,
			reason:      "observation_quality_stop_reason_invalid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			projected := qualityProjected(t, testCase.observation)
			failure := checkObservationQuality(projected, projected.view.Observation)
			if testCase.reason == "" {
				if failure != nil {
					t.Fatalf("valid quality rejected: %s", failure.reason)
				}
				return
			}
			if failure == nil || failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
	t.Run("missing observation", func(t *testing.T) {
		projected := qualityProjected(t, `{"status":"complete"}`)
		if failure := checkObservationQuality(projected, nil); failure == nil ||
			failure.reason != "post_observation_missing" {
			t.Fatalf("failure = %v", failure)
		}
	})
}

// TestRequireQualityTextLines proves the readable-line assertion accepts the
// explicit samples=0 fact and still rejects a missing member.
func TestRequireQualityTextLines(t *testing.T) {
	quality := &qualityView{State: "unknown", Samples: 0, StopReason: "not_sampled"}
	projected := &callResult{texts: []string{
		"result v3 is_error=false\n" +
			"observation\n" +
			" quality.state=unknown\n" +
			" quality.samples=0\n" +
			" quality.stop_reason=not_sampled\n" +
			"end result\n",
	}}
	if failure := requireQualityTextLines(projected, quality); failure != nil {
		t.Fatalf("samples=0 lines rejected: %s", failure.reason)
	}
	projected.texts = []string{
		"result v3 is_error=false\n" +
			"observation\n" +
			" quality.state=unknown\n" +
			"json v1 path=\"/observation/quality/unknown\" value=\"kept\"\n" +
			"observation\n" +
			" quality.samples=0\n" +
			" quality.stop_reason=not_sampled\n" +
			"end result\n",
	}
	if failure := requireQualityTextLines(projected, quality); failure != nil {
		t.Fatalf("quality lines split across groups rejected: %s", failure.reason)
	}
	projected.texts = []string{"observation\n quality.state=unknown\n quality.samples=0\n"}
	if failure := requireQualityTextLines(projected, quality); failure == nil {
		t.Fatal("missing stop_reason line accepted")
	}
}

func TestCheckScrollEffect(t *testing.T) {
	cases := []struct {
		name   string
		effect *effectView
		object map[string]any
		scroll bool
		reason string
	}{
		{name: "scroll observed", effect: &effectView{State: "observed"}, scroll: true},
		{name: "scroll not observed", effect: &effectView{State: "not_observed"}, scroll: true},
		{name: "scroll unknown", effect: &effectView{State: "unknown"}, scroll: true},
		{name: "scroll missing", scroll: true, reason: "scroll_effect_missing"},
		{
			name: "scroll invalid state", effect: &effectView{State: "moved"},
			scroll: true, reason: "scroll_effect_state_invalid",
		},
		{name: "key has no effect"},
		{
			name: "key carries effect", effect: &effectView{State: "observed"},
			reason: "effect_on_non_scroll",
		},
		{
			name:   "key carries explicit null effect",
			object: map[string]any{"effect": nil},
			reason: "effect_on_non_scroll",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			failure := checkScrollEffect(
				resultView{Effect: testCase.effect}, testCase.object, testCase.scroll)
			if testCase.reason == "" {
				if failure != nil {
					t.Fatalf("valid effect rejected: %s", failure.reason)
				}
				return
			}
			if failure == nil || failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

// TestDecodeResultTextQualityAndEffect pins the generic oracle: effect remains a
// dotted path while observation quality is relative to its v3 group header.
func TestDecodeResultTextQualityAndEffect(t *testing.T) {
	text := "result v3 is_error=false\n" +
		"operation_id=Pscroll000000000000000001\n" +
		"outcome=executed\n" +
		"effect.state=observed\n" +
		"observation\n" +
		" status=complete\n" +
		" id=Oscroll00000000000000001\n" +
		" quality.state=unstable\n" +
		" quality.samples=5\n" +
		" quality.stop_reason=max_samples\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decodeResultText: %s", failure.reason)
	}
	view := decoded.view
	if view.Effect == nil || view.Effect.State != "observed" {
		t.Fatalf("effect = %+v", view.Effect)
	}
	if view.Observation == nil || view.Observation.Quality == nil {
		t.Fatal("observation quality missing")
	}
	quality := view.Observation.Quality
	if quality.State != "unstable" || quality.Samples != 5 || quality.StopReason != "max_samples" {
		t.Fatalf("quality = %+v", quality)
	}
	state, found := lookupPath(decoded.object, []string{"effect", "state"})
	if !found || state != "observed" {
		t.Fatalf("effect object = %#v, %v", state, found)
	}
	samples, found := lookupPath(decoded.object,
		[]string{"observation", "quality", "samples"})
	number, integer := exactInteger(samples)
	if !found || !integer || number != 5 {
		t.Fatalf("samples object = %#v, %v", samples, found)
	}
}

// TestProjectQualityAndEffectChannels proves text and structuredContent carry
// the same effect/quality facts in every result mode, and that a channel that
// drops one of them is rejected.
func TestProjectQualityAndEffectChannels(t *testing.T) {
	observation := map[string]any{
		"status": observationComplete,
		"id":     "Oscroll00000000000000001",
		"quality": map[string]any{
			"state": "stable", "samples": 2, "stop_reason": "stable",
		},
		"tree": map[string]any{
			"format": treeFull, "scope": "reported_visible_with_context",
			"view": "compact",
		},
	}
	payload := map[string]any{
		"operation_id": "Pscroll000000000000000001",
		"outcome":      outcomeExecuted,
		"effect":       map[string]any{"state": "observed"},
		"observation":  observation,
	}
	text := fakeResultText(payload, false)

	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			result := &sdk.CallToolResult{}
			switch mode {
			case resultModeText:
				result.Content = []sdk.Content{&sdk.TextContent{Text: text}}
			case resultModeStructured:
				result.Content = []sdk.Content{}
				result.StructuredContent = payload
			default:
				result.Content = []sdk.Content{&sdk.TextContent{Text: text}}
				result.StructuredContent = payload
			}
			projected, failure := project(result, mode)
			if failure != nil {
				t.Fatalf("project: %s", failure.reason)
			}
			if projected.view.Effect == nil || projected.view.Effect.State != "observed" {
				t.Fatalf("effect = %+v", projected.view.Effect)
			}
			if projected.view.Observation == nil || projected.view.Observation.Quality == nil {
				t.Fatal("observation quality missing")
			}
			quality := projected.view.Observation.Quality
			if quality.State != "stable" || quality.Samples != 2 ||
				quality.StopReason != "stable" {
				t.Fatalf("quality = %+v", quality)
			}
		})
	}

	t.Run("both mode rejects text without effect", func(t *testing.T) {
		withoutEffect := map[string]any{
			"operation_id": payload["operation_id"],
			"outcome":      outcomeExecuted,
			"observation":  observation,
		}
		result := &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: fakeResultText(withoutEffect, false)}},
			StructuredContent: payload,
		}
		if _, failure := project(result, resultModeBoth); failure == nil ||
			failure.reason != "both_mode_object_mismatch" {
			t.Fatalf("failure = %v, want both_mode_object_mismatch", failure)
		}
	})

	t.Run("both mode rejects text without quality", func(t *testing.T) {
		withoutQuality := map[string]any{
			"operation_id": payload["operation_id"],
			"outcome":      outcomeExecuted,
			"effect":       payload["effect"],
			"observation": map[string]any{
				"status": observationComplete,
				"id":     observation["id"],
				"tree":   observation["tree"],
			},
		}
		result := &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: fakeResultText(withoutQuality, false)}},
			StructuredContent: payload,
		}
		if _, failure := project(result, resultModeBoth); failure == nil ||
			failure.reason != "both_mode_object_mismatch" {
			t.Fatalf("failure = %v, want both_mode_object_mismatch", failure)
		}
	})
}

func TestProjectResultModeViolations(t *testing.T) {
	payload := map[string]any{"operation_id": "P1"}
	text := fakeResultText(payload, false)
	cases := []struct {
		name   string
		mode   string
		result *sdk.CallToolResult
		reason string
	}{
		{
			name: "text has structured",
			mode: resultModeText,
			result: &sdk.CallToolResult{
				Content:           []sdk.Content{&sdk.TextContent{Text: text}},
				StructuredContent: payload,
			},
			reason: "text_mode_has_structured_content",
		},
		{
			name: "text missing trailer",
			mode: resultModeText,
			result: &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{
					Text: strings.TrimSuffix(text, "end result\n"),
				}},
			},
			reason: "text_result_unterminated",
		},
		{
			name: "structured has full text",
			mode: resultModeStructured,
			result: &sdk.CallToolResult{
				Content:           []sdk.Content{&sdk.TextContent{Text: text}},
				StructuredContent: payload,
			},
			reason: "structured_mode_has_result_text",
		},
		{
			name: "structured null content",
			mode: resultModeStructured,
			result: &sdk.CallToolResult{
				StructuredContent: payload,
			},
			reason: "structured_mode_content_null",
		},
		{
			name: "structured error is not diagnostic",
			mode: resultModeStructured,
			result: &sdk.CallToolResult{
				Content:           []sdk.Content{&sdk.TextContent{Text: text}},
				StructuredContent: payload,
				IsError:           true,
			},
			reason: "diagnostic_text_header_invalid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := project(testCase.result, testCase.mode); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

func TestProjectBothPreservesTreeMetadata(t *testing.T) {
	fullPayload := map[string]any{
		"operation_id": "Ptree0000000000000000001",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "Otest_1",
			"tree": map[string]any{
				"format": treeFull,
				"scope":  "reported_visible_with_context",
				"view":   "compact",
			},
		},
	}
	fullText := "result v3 is_error=false\noperation_id=Ptree0000000000000000001\n" +
		"observation\n status=complete\n id=Otest_1\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	fullResult := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: fullText}},
		StructuredContent: fullPayload,
	}
	if _, failure := project(fullResult, resultModeBoth); failure != nil {
		t.Fatalf("matching full metadata rejected: %s", failure.reason)
	}

	extraMetadata := map[string]any{
		"operation_id": fullPayload["operation_id"],
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "Otest_1",
			"tree": map[string]any{
				"format": treeFull,
				"scope":  "reported_visible_with_context",
				"view":   "compact",
				"extra":  "must-compare",
			},
		},
	}
	fullResult.StructuredContent = extraMetadata
	if _, failure := project(fullResult, resultModeBoth); failure == nil ||
		failure.reason != "both_mode_object_mismatch" {
		t.Fatalf("extra structured metadata = %v, want both_mode_object_mismatch", failure)
	}

	deltaPayload := map[string]any{
		"operation_id": "Ptree0000000000000000002",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "Otest_2",
			"tree": map[string]any{
				"format": treeDelta,
				"base":   "Otest_1",
				"view":   "compact",
			},
		},
	}
	deltaText := "result v3 is_error=false\noperation_id=Ptree0000000000000000002\n" +
		"observation\n status=complete\n id=Otest_2\n\n" +
		"tree v4 delta base=_1 changes=0\n\nend tree\nend result\n"
	deltaResult := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: deltaText}},
		StructuredContent: deltaPayload,
	}
	if _, failure := project(deltaResult, resultModeBoth); failure != nil {
		t.Fatalf("matching expanded base rejected: %s", failure.reason)
	}

	deltaPayload["observation"].(map[string]any)["tree"].(map[string]any)["base"] = "Otest_9"
	if _, failure := project(deltaResult, resultModeBoth); failure == nil ||
		failure.reason != "both_mode_object_mismatch" {
		t.Fatalf("base metadata mismatch = %v, want both_mode_object_mismatch", failure)
	}
}

func TestDecodeResultTextContentBlocks(t *testing.T) {
	payload := map[string]any{
		"app": map[string]any{
			"kind": "find",
			"items": []any{map[string]any{
				"name": "设置", "package": "com.android.settings", "future": true,
			}},
			"total": 1, "truncated": false, "captured_at": "2026-01-01T00:00:00Z",
		},
		"shell": map[string]any{
			"termination": "exited",
			"exit_code":   0,
			"output":      "first\r\nsecond\n",
			"truncated":   false,
		},
		"diagnosis": map[string]any{
			"summary": "fixture",
			"issues":  []any{},
			"timeline": []any{map[string]any{
				"at": "2026-01-01T00:00:00Z", "phase": "observe", "code": "complete", "future": true,
			}},
			"coverage": "original_record", "truncated": false,
		},
	}
	decoded, failure := decodeResultText(fakeResultText(payload, false))
	if failure != nil {
		t.Fatalf("decodeResultText: %v", failure.reason)
	}
	if decoded.view.App == nil || decoded.view.App.Kind != "find" ||
		!decoded.view.App.HasItems || len(decoded.view.App.Items) != 1 ||
		decoded.view.App.Items[0].Package != "com.android.settings" {
		t.Fatalf("app projection = %+v", decoded.view.App)
	}
	if decoded.view.Shell == nil || decoded.view.Shell.Output == nil ||
		*decoded.view.Shell.Output != "first\r\nsecond\n" {
		t.Fatalf("shell projection = %+v", decoded.view.Shell)
	}
	if decoded.view.Diagnosis == nil || len(decoded.view.Diagnosis.Timeline) != 1 {
		t.Fatalf("timeline projection = %+v", decoded.view.Diagnosis)
	}
	appItem, _ := lookupPath(decoded.object, []string{"app", "items", "0"})
	appObject, _ := appItem.(map[string]any)
	eventItem, _ := lookupPath(decoded.object, []string{"diagnosis", "timeline", "0"})
	eventObject, _ := eventItem.(map[string]any)
	if appObject["future"] != true || eventObject["future"] != true {
		t.Fatal("complete item/event JSON was not preserved")
	}
}

func TestDecodeResultTextEmptyDeltaWithWait(t *testing.T) {
	text := "result v3 is_error=false\n" +
		"operation_id=Pwait000000000000000001\n" +
		"phase=finished\n" +
		"observation\n" +
		" status=complete\n" +
		" id=Owait00000000000000001\n" +
		" quality.state=unknown\n" +
		" quality.samples=0\n" +
		" quality.stop_reason=not_sampled\n" +
		"\n" +
		"tree v4 delta base=Owait00000000000000000 changes=0\n" +
		"\n" +
		"end tree\n" +
		"wait.trigger=timeout\n" +
		"wait.matched=[\"timeout\"]\n" +
		"wait.elapsed_ms=1000\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("empty delta with wait rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Wait == nil || decoded.view.Wait.Trigger != waitTriggerTimeout ||
		len(decoded.view.Wait.Matched) != 1 ||
		decoded.view.Wait.Matched[0] != waitTriggerTimeout ||
		decoded.view.Wait.ElapsedMS != 1000 {
		t.Fatalf("wait projection = %+v", decoded.view.Wait)
	}
}

func TestDecodeResultTextEmptyInspection(t *testing.T) {
	text := "result v3 is_error=false\n" +
		"\n" +
		"inspect v3 scope=query observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed " +
		"matches=0 nodes=0 entries=0\n" +
		"\n" +
		"end inspect\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("empty inspection rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Inspection == nil || decoded.view.Inspection.Scope != "query" ||
		decoded.view.Inspection.ObservationID != "O1" ||
		decoded.view.Inspection.Revision != 1 {
		t.Fatalf("inspection projection = %+v", decoded.view.Inspection)
	}
}

func TestDecodeResultTextRejectsIncompleteProjection(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name:   "missing tail",
			text:   "result v3 is_error=false\nstatus=not_seen\n",
			reason: "text_result_unterminated",
		},
		{
			name: "empty delta missing tree tail",
			text: "result v3 is_error=false\nobservation\n id=O2\n\n" +
				"tree v4 delta base=O1 changes=0\n\n" +
				"end result\n",
			reason: "text_tree_unterminated",
		},
		{
			name: "omitted tree",
			text: "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
				"tree v4 omitted reason=ref_format\nend result\n",
			reason: "text_result_block_omitted",
		},
		{
			name: "duplicate path",
			text: "result v3 is_error=false\njson v1 path=\"/status\" value=\"a\"\n" +
				"json v1 path=\"/status\" value=\"b\"\nend result\n",
			reason: "text_result_path_duplicate",
		},
		{
			name: "legacy tree defaults line",
			text: "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
				"tree v4 full nodes=1\n" +
				"defaults {\"checked\":false}\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_separator_invalid",
		},
		{
			name: "legacy inspection defaults line",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n" +
				"defaults {\"checked\":false}\n\nn1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_separator_invalid",
		},
		{
			name: "defaults line inside tree body",
			text: "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
				"tree v4 full nodes=1\n\n" +
				"defaults {\"checked\":false}\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "full node writes checked default",
			text: "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
				"tree v4 full nodes=1\n\n" +
				"n1 root 0,0,1080,2400 checked=false\nend tree\nend result\n",
			reason: "text_tree_flag_default",
		},
		{
			name: "full node writes enabled default",
			text: "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
				"tree v4 full nodes=1\n\n" +
				"n1 root 0,0,1080,2400 enabled=true\nend tree\nend result\n",
			reason: "text_tree_flag_default",
		},
		{
			name: "full node writes focused default",
			text: "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
				"tree v4 full nodes=1\n\n" +
				"n1 root 0,0,1080,2400 focused=false\nend tree\nend result\n",
			reason: "text_tree_flag_default",
		},
		{
			name: "inspection node writes checked default",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
				"n1 root [0,0,1080,2400] checked=false\nend inspect\nend result\n",
			reason: "text_inspect_flag_default",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

func TestDecodeResultTextJSONPointer(t *testing.T) {
	text := "result v3 is_error=false\n" +
		"json v1 path=\"/future/a~1b~0c\" value={\"value\":1}\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decodeResultText: %v", failure.reason)
	}
	value, ok := lookupPath(decoded.object, []string{"future", "a/b~c", "value"})
	number, integer := valueInteger(value)
	if !ok || !integer || number != 1 {
		t.Fatalf("decoded pointer value = %#v, %v", value, ok)
	}
}

func TestDecodeResultTextJSONFallbackNumericObjectKey(t *testing.T) {
	text := "result v3 is_error=false\n" +
		"json v1 path=\"/observation/quality/0\" value=\"unknown\"\n" +
		"observation\n" +
		" quality.state=good\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decodeResultText: %s %v", failure.reason, failure.fields)
	}
	quality, ok := lookupPath(decoded.object, []string{"observation", "quality"})
	qualityObject, object := quality.(map[string]any)
	if !ok || !object || qualityObject["0"] != "unknown" || qualityObject["state"] != "good" {
		t.Fatalf("quality object = %#v", quality)
	}

	root := map[string]any{
		"values": []any{map[string]any{"seed": true}},
	}
	updated, failure := setNestedValue(root, []string{"values", "0", "next"}, int64(1))
	if failure != nil {
		t.Fatalf("setNestedValue existing array: %s %v", failure.reason, failure.fields)
	}
	next, ok := lookupPath(updated, []string{"values", "0", "next"})
	if !ok || next != int64(1) {
		t.Fatalf("existing array update = %#v, %v", next, ok)
	}
}

// TestDecodeResultTextAcceptsTriStateContract proves the body validator does
// not reject explicit unknown values, explicit non-default known values, delta
// set false, or a quoted label that merely looks like a state member.
func TestDecodeResultTextAcceptsTriStateContract(t *testing.T) {
	text := "result v3 is_error=false\nobservation\n id=\"O1\"\n\n" +
		"tree v4 full nodes=3\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" n2 text \"checked=false is a label\" value=\"x\" enabled=false focused=true selected=null protected=null checked=null\n" +
		" n3 button enabled=null checked=true\n" +
		"end tree\nend result\n"
	if _, failure := decodeResultText(text); failure != nil {
		t.Fatalf("valid tri-state full tree rejected: %s %v", failure.reason, failure.fields)
	}

	delta := "result v3 is_error=false\nobservation\n id=\"O2\"\n\n" +
		"tree v4 delta base=O1 changes=2\n\n" +
		"~ n2 checked=false focused=null\n" +
		"+ n4 text 0,0,10,10 checked=true\n" +
		"end tree\nend result\n"
	if _, failure := decodeResultText(delta); failure != nil {
		t.Fatalf("valid delta set rejected: %s %v", failure.reason, failure.fields)
	}

	inspection := "result v3 is_error=false\n\n" +
		"inspect v3 scope=query observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed matches=1 total=1 truncated=false nodes=1 entries=1\n\n" +
		"match 1/1 path=[n1 n2]\nn3 text [0,0,10,10] checked=null\nend inspect\nend result\n"
	if _, failure := decodeResultText(inspection); failure != nil {
		t.Fatalf("valid query inspection rejected: %s %v", failure.reason, failure.fields)
	}
}

func TestDecodeResultTextV4Blocks(t *testing.T) {
	full := "tree v4 full nodes=11\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" n2 group 24,60,144,180 \"菜单\"\n" +
		"  n3 button @ \"设置\"\n" +
		"  n4 text \"说明\"\n" +
		"  chain [n10 n11] group 0,0,10,10 \"分组\"\n" +
		" n5 button 10,20,30,40 tap long_press text swipe\n" +
		" n6 input 0,0,10,10 tap text " +
		"enabled=false focused=true selected=null protected=null checked=true " +
		"axis=y resource=\"res\" context\n" +
		" n7 scroll - swipe axis=both\n" +
		" n8 view \"sibling\"\n" +
		" n9 button 0,0,1,1 !tap\n" +
		"end tree\n"
	delta := "tree v4 delta base=_1 changes=5\n\n" +
		"~ n2 checked=false focused=null\n" +
		"+ n20 group 0,0,1,1\n" +
		" n21 button @ !tap\n" +
		"= n24 view 0,0,2,2 checked=true\n" +
		" n25 scroll 0,0,3,3 swipe\n" +
		"- n3\n" +
		"children n4 = [n5 n6]\n" +
		"end tree\n"
	inspectTree := "inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed nodes=3 entries=3\n\n" +
		"n1 root [0,0,1080,2400]\n" +
		"  n2 text [0,0,10,10] \"Hello\" value=\"v\"\n" +
		"  n3 view [0,0,20,20] text\n" +
		"end inspect\n"
	inspectSubtree := "inspect v3 scope=subtree observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed root=n5 nodes=2 entries=2\n\n" +
		"n5 group [0,0,10,10] \"sub\"\n" +
		"  n6 [0,0,5,5] tap\n" +
		"end inspect\n"
	inspectQuery := "inspect v3 scope=query observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed " +
		"matches=2 total=2 truncated=false nodes=3 entries=3\n\n" +
		"match 1/2 path=[n1 n2]\n" +
		"n2 text [0,0,10,10] \"one\"\n" +
		"\n" +
		"match 2/2 path=[n1 n3]\n" +
		"n3 group [0,0,20,20]\n" +
		"  n4 [0,0,5,5] checked=null\n" +
		"end inspect\n"

	cases := []struct {
		name   string
		text   string
		format string
		scope  string
	}{
		{
			name: "full",
			text: "result v3 is_error=false\nobservation\n id=Otest_1\n\n" + full + "end result\n",
		},
		{
			name: "delta",
			text: "result v3 is_error=false\nobservation\n id=Otest_2\n\n" + delta + "end result\n",
		},
		{
			name:  "inspect tree",
			text:  "result v3 is_error=false\n\n" + inspectTree + "end result\n",
			scope: "tree",
		},
		{
			name:  "inspect subtree",
			text:  "result v3 is_error=false\n\n" + inspectSubtree + "end result\n",
			scope: "subtree",
		},
		{
			name:  "inspect query",
			text:  "result v3 is_error=false\n\n" + inspectQuery + "end result\n",
			scope: "query",
		},
		{
			name: "empty delta",
			text: "result v3 is_error=false\nobservation\n id=Otest_3\n\n" +
				"tree v4 delta base=_1 changes=0\n\n" +
				"end tree\nend result\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decoded, failure := decodeResultText(testCase.text)
			if failure != nil {
				t.Fatalf("valid v4 block rejected: %s %v", failure.reason, failure.fields)
			}
			if testCase.format != "" && (decoded.view.Observation == nil ||
				decoded.view.Observation.Tree == nil ||
				decoded.view.Observation.Tree.Format != testCase.format) {
				t.Fatalf("tree projection = %+v", decoded.view.Observation)
			}
			if testCase.scope != "" && (decoded.view.Inspection == nil ||
				decoded.view.Inspection.Scope != testCase.scope) {
				t.Fatalf("inspection projection = %+v", decoded.view.Inspection)
			}
		})
	}
}

func TestDecodeResultTextV3ObservationGroups(t *testing.T) {
	lateTree := "result v3 is_error=false\n\n" +
		"tree v4 full nodes=1\n\n" +
		"n1 root 0,0,1080,2400\n" +
		"end tree\n" +
		"observation\n id=Olate_1\n status=complete\n" +
		"end result\n"
	decoded, failure := decodeResultText(lateTree)
	if failure != nil {
		t.Fatalf("late observation id rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Observation == nil || decoded.view.Observation.ID != "Olate_1" ||
		decoded.view.Observation.Status != observationComplete {
		t.Fatalf("late observation group = %+v", decoded.view.Observation)
	}

	rootNull := "result v3 is_error=false\nobservation=null\nend result\n"
	decoded, failure = decodeResultText(rootNull)
	if failure != nil || decoded.object["observation"] != nil {
		t.Fatalf("root observation null = %+v, %v", decoded, failure)
	}

	cases := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name:   "direct path",
			text:   "result v3 is_error=false\nobservation.id=O1\nend result\n",
			reason: "text_result_observation_group_required",
		},
		{
			name: "empty group",
			text: "result v3 is_error=false\nobservation\n\n" +
				"inspect v3 scope=query observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed matches=0 nodes=0 entries=0\n\n" +
				"end inspect\nend result\n",
			reason: "text_result_observation_group_empty",
		},
		{
			name:   "double space field",
			text:   "result v3 is_error=false\nobservation\n  id=O1\nend result\n",
			reason: "text_result_observation_group_invalid",
		},
		{
			name: "duplicate across groups",
			text: "result v3 is_error=false\nobservation\n id=O1\n" +
				"observation\n id=O1\nend result\n",
			reason: "text_result_path_duplicate",
		},
		{
			name: "ancestor child conflict across groups",
			text: "result v3 is_error=false\nobservation\n quality=null\n" +
				"observation\n quality.state=stable\nend result\n",
			reason: "text_result_path_conflict",
		},
		{
			name: "child ancestor conflict across groups",
			text: "result v3 is_error=false\nobservation\n quality.state=stable\n" +
				"observation\n quality=null\nend result\n",
			reason: "text_result_path_conflict",
		},
		{
			name: "tree owner id from fallback",
			text: "result v3 is_error=false\n" +
				"json v1 path=\"/observation/id\" value=\"O1\"\n\n" +
				"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_id_group_required",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

func TestShortTreeBaseUsesUint64Sequence(t *testing.T) {
	text := "result v3 is_error=false\nobservation\n" +
		" id=Otest_18446744073709551615\n\n" +
		"tree v4 delta base=_18446744073709551615 changes=0\n\n" +
		"end tree\nend result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("uint64 max base rejected: %s %v", failure.reason, failure.fields)
	}
	base, ok := lookupPath(decoded.object, []string{"observation", "tree", "base"})
	if !ok || base != "Otest_18446744073709551615" {
		t.Fatalf("expanded base = %#v, %v", base, ok)
	}
}

func TestTreeTextOUsesV4TokenGrammar(t *testing.T) {
	maxO := "O" + strings.Repeat("a", 63)
	valid := []string{"Oabc+foo_2", maxO}
	for _, value := range valid {
		if !validTreeTextO(value) {
			t.Fatalf("valid v4 O rejected: %q", value)
		}
	}
	for _, value := range []string{"O", "Oabc=foo", "O" + strings.Repeat("a", 64), "O设置"} {
		if validTreeTextO(value) {
			t.Fatalf("invalid v4 O accepted: %q", value)
		}
	}

	full := "result v3 is_error=false\nobservation\n id=Oabc+foo_2\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	if _, failure := decodeResultText(full); failure != nil {
		t.Fatalf("full v4 O rejected: %s %v", failure.reason, failure.fields)
	}
	fullMaxO := "result v3 is_error=false\nobservation\n id=" + maxO + "\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	if _, failure := decodeResultText(fullMaxO); failure != nil {
		t.Fatalf("64-byte v4 O rejected: %s %v", failure.reason, failure.fields)
	}
	observation := &observationView{ID: maxO, Tree: &treeView{Format: treeFull}}
	if failure := checkObservationTreeBlock(&callResult{texts: []string{fullMaxO}},
		observation); failure != nil {
		t.Fatalf("64-byte tree block O rejected: %s %v", failure.reason, failure.fields)
	}

	delta := "result v3 is_error=false\nobservation\n id=Oabc+foo_2\n\n" +
		"tree v4 delta base=_1 changes=0\n\nend tree\nend result\n"
	decoded, failure := decodeResultText(delta)
	if failure != nil {
		t.Fatalf("short v4 O base rejected: %s %v", failure.reason, failure.fields)
	}
	base, ok := lookupPath(decoded.object, []string{"observation", "tree", "base"})
	if !ok || base != "Oabc+foo_1" {
		t.Fatalf("punctuated base = %#v, %v", base, ok)
	}

	maxCurrent := "Oabc+" + strings.Repeat("x", 57) + "_7"
	if len(maxCurrent) != 64 {
		t.Fatalf("test O length = %d", len(maxCurrent))
	}
	lateDelta := "result v3 is_error=false\n\n" +
		"tree v4 delta base=_6 changes=0\n\nend tree\n" +
		"observation\n id=" + maxCurrent + "\n" +
		"end result\n"
	decoded, failure = decodeResultText(lateDelta)
	if failure != nil {
		t.Fatalf("late 64-byte current O rejected: %s %v", failure.reason, failure.fields)
	}
	base, ok = lookupPath(decoded.object, []string{"observation", "tree", "base"})
	if !ok || base != maxCurrent[:len(maxCurrent)-1]+"6" {
		t.Fatalf("late max O base = %#v, %v", base, ok)
	}

	overflow := "result v3 is_error=false\nobservation\n id=" + maxCurrent + "\n\n" +
		"tree v4 delta base=_12 changes=0\n\nend tree\nend result\n"
	if _, failure := decodeResultText(overflow); failure == nil ||
		failure.reason != "text_tree_base_expanded_invalid" {
		t.Fatalf("expanded 65-byte base = %v", failure)
	}
}

func TestDecodeResultTextActionReconstructionSyntax(t *testing.T) {
	valid := []struct {
		name string
		text string
	}{
		{
			name: "explicit default permits full source list order",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 button 0,0,10,10 swipe long_press tap\nend tree\nend result\n",
		},
		{
			name: "negated default preserves full source list order",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 button 0,0,10,10 swipe long_press !tap\nend tree\nend result\n",
		},
		{
			name: "explicit default preserves duplicate actions",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 button 0,0,10,10 tap swipe tap tap\nend tree\nend result\n",
		},
		{
			name: "negated default preserves duplicate actions",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 button 0,0,10,10 swipe long_press long_press !tap\nend tree\nend result\n",
		},
		{
			name: "inferred source order permits repeated non-default actions",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 button 0,0,10,10 long_press long_press swipe\nend tree\nend result\n",
		},
		{
			name: "inspect explicit actions retain wire order",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=2 entries=2\n\n" +
				"n1 root [0,0,1080,2400]\n" +
				"  n2 button [0,0,10,10] swipe tap tap long_press\nend inspect\nend result\n",
		},
		{
			name: "inspect empty action list stays empty",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=2 entries=2\n\n" +
				"n1 root [0,0,1080,2400]\n" +
				"  n2 button [0,0,10,10] checked=null\nend inspect\nend result\n",
		},
	}
	for _, testCase := range valid {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure != nil {
				t.Fatalf("valid action list rejected: %s %v", failure.reason, failure.fields)
			}
		})
	}

	invalid := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name: "omitted default requires remaining source order",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 button 0,0,10,10 swipe long_press\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "input omitted default requires remaining source order",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				"tree v4 full nodes=2\n\n" +
				"n1 root 0,0,1080,2400\n" +
				" n2 input 0,0,10,10 swipe tap\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "inspect rejects v4 action negation",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=2 entries=2\n\n" +
				"n1 root [0,0,1080,2400]\n" +
				"  n2 button [0,0,10,10] long_press !tap\nend inspect\nend result\n",
			reason: "text_inspect_body_invalid",
		},
	}
	for _, testCase := range invalid {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

// TestCompactTreeCheckRejectsOnlyNewChainOutput keeps historical chain parsing
// available while making a current observation fail if it still emits a folded
// chain. Labels that merely contain the word "chain" are ordinary text.
func TestCompactTreeCheckRejectsOnlyNewChainOutput(t *testing.T) {
	text := "result v3 is_error=false\nobservation\n id=O1\n\n" +
		"tree v4 full nodes=3\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" chain [n2 n3] group 0,0,10,10\n" +
		"end tree\nend result\n"
	observation := &observationView{ID: "O1", Tree: &treeView{Format: treeFull}}
	if _, failure := decodeResultText(text); failure != nil {
		t.Fatalf("historical chain parser rejected: %s %v", failure.reason, failure.fields)
	}
	if failure := checkObservationTreeBlock(&callResult{texts: []string{text}},
		observation); failure == nil || failure.reason != "tree_chain_present" {
		t.Fatalf("new chain output check = %v, want tree_chain_present", failure)
	}

	ordinary := "result v3 is_error=false\nobservation\n id=O2\n\n" +
		"tree v4 full nodes=2\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" n2 text \"chain [not a definition]\"\n" +
		"end tree\nend result\n"
	if containsCompactTreeChain(ordinary) {
		t.Fatal("quoted label was mistaken for a chain definition")
	}

	fallback := "result v3 is_error=false\nobservation\n id=O3\n" +
		"json v1 path=\"/observation/tree\" value={\"format\":\"full\",\"scope\":\"reported_visible_with_context\",\"view\":\"compact\",\"root\":{\"ref\":\"n1\",\"role\":\"root\",\"bounds\":[0,0,1080,2400]}}\n" +
		"end result\n"
	fallbackObservation := &observationView{ID: "O3", Tree: &treeView{Format: treeFull}}
	if failure := checkObservationTreeBlock(&callResult{texts: []string{fallback}},
		fallbackObservation); failure == nil || failure.reason != "text_tree_block_missing" {
		t.Fatalf("fallback tree projection = %v, want text_tree_block_missing", failure)
	}
}

func TestDecodeResultTextRejectsLegacyAndMissingBlocks(t *testing.T) {
	legacyResult := "result v2 is_error=false\nstatus=complete\nend result\n"
	if _, failure := decodeResultText(legacyResult); failure == nil ||
		failure.reason != "text_result_header_invalid" {
		t.Fatalf("legacy result header = %v, want text_result_header_invalid", failure)
	}
	legacyTree := "result v3 is_error=false\nobservation\n id=O1\n\n" +
		"tree v3 format=full id=O1 scope=reported_visible_with_context " +
		"view=compact nodes=1 entries=1\n\n" +
		"n1 root [0,0,1080,2400]\nend tree\nend result\n"
	legacyInspect := "result v3 is_error=false\n\n" +
		"inspect v2 scope=tree observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
		"n1 root [0,0,1080,2400]\nend inspect\nend result\n"
	legacyTreeWithoutSeparator := "result v3 is_error=false\nstatus=complete\n" +
		"tree v3 format=full id=O1 scope=reported_visible_with_context " +
		"view=compact nodes=1 entries=1\n\n" +
		"n1 root [0,0,1080,2400]\nend tree\nend result\n"
	for name, text := range map[string]string{
		"tree v3 after blank":       legacyTree,
		"tree v3 without separator": legacyTreeWithoutSeparator,
		"inspect v2 after blank":    legacyInspect,
	} {
		t.Run(name, func(t *testing.T) {
			if _, failure := decodeResultText(text); failure == nil {
				t.Fatal("legacy block was accepted")
			}
		})
	}

	observation := &observationView{ID: "O1", Tree: &treeView{Format: treeFull}}
	projected := &callResult{texts: []string{legacyTree}}
	if failure := checkObservationTreeBlock(projected, observation); failure == nil ||
		failure.reason != "text_tree_block_missing" {
		t.Fatalf("legacy tree block check = %v, want text_tree_block_missing", failure)
	}
	projected.texts = []string{"result v3 is_error=false\nobservation\n id=O1\nend result\n"}
	if failure := checkObservationTreeBlock(projected, observation); failure == nil ||
		failure.reason != "text_tree_block_missing" {
		t.Fatalf("missing tree block check = %v, want text_tree_block_missing", failure)
	}
	if _, failure := treeBlockText(&callResult{texts: []string{legacyInspect}},
		"inspect v3 scope=tree observation=O1"); failure == nil ||
		failure.reason != "text_inspect_block_missing" {
		t.Fatalf("legacy inspect block check = %v, want text_inspect_block_missing", failure)
	}
	inspection := &inspectionView{ObservationID: "O1", Scope: "tree"}
	if failure := checkInspectionTreeBlock(&callResult{texts: []string{legacyInspect}},
		inspection, "O1"); failure == nil ||
		failure.reason != "text_inspect_block_missing" {
		t.Fatalf("legacy inspect check = %v, want text_inspect_block_missing", failure)
	}
	missing := "result v3 is_error=false\ninspection.observation_id=O1\nend result\n"
	if failure := checkInspectionTreeBlock(&callResult{texts: []string{missing}},
		inspection, "O1"); failure == nil ||
		failure.reason != "text_inspect_block_missing" {
		t.Fatalf("missing inspect check = %v, want text_inspect_block_missing", failure)
	}
}

func TestV4BlockChecksRequireCompleteBlocks(t *testing.T) {
	tree := "result v3 is_error=false\nobservation\n id=O1\n\n" +
		"tree v4 full nodes=1\n\n" +
		"n1 root 0,0,1080,2400\nend tree\nend result\n"
	observation := &observationView{ID: "O1", Tree: &treeView{Format: treeFull}}
	if failure := checkObservationTreeBlock(&callResult{texts: []string{tree}},
		observation); failure != nil {
		t.Fatalf("complete v4 tree rejected: %s %v", failure.reason, failure.fields)
	}
	unterminated := "result v3 is_error=false\nobservation\n id=O1\n\n" +
		"tree v4 full nodes=1\n\n" +
		"n1 root 0,0,1080,2400\nend result\n"
	if failure := checkObservationTreeBlock(&callResult{texts: []string{unterminated}},
		observation); failure == nil || failure.reason != "text_block_unterminated" {
		t.Fatalf("unterminated tree check = %v, want text_block_unterminated", failure)
	}

	delta := "result v3 is_error=false\nobservation\n id=Otest_2\n\n" +
		"tree v4 delta base=_1 changes=0\n\nend tree\nend result\n"
	deltaObservation := &observationView{
		ID:   "Otest_2",
		Tree: &treeView{Format: treeDelta, Base: "Otest_1"},
	}
	if failure := checkObservationTreeBlock(&callResult{texts: []string{delta}},
		deltaObservation); failure != nil {
		t.Fatalf("expanded short base rejected: %s %v", failure.reason, failure.fields)
	}
	deltaObservation.Tree.Base = "Otest_9"
	if failure := checkObservationTreeBlock(&callResult{texts: []string{delta}},
		deltaObservation); failure == nil || failure.reason != "text_tree_base_mismatch" {
		t.Fatalf("base mismatch check = %v", failure)
	}

	unknownInspect := "result v3 is_error=false\n\n" +
		"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
		"n1 root -\nend inspect\nend result\n"
	if _, failure := decodeResultText(unknownInspect); failure != nil {
		t.Fatalf("inspect unknown geometry rejected: %s %v", failure.reason, failure.fields)
	}

	inspect := "result v3 is_error=false\n\n" +
		"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
		"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
		"n1 root [0,0,1080,2400]\nend inspect\nend result\n"
	inspection := &inspectionView{ObservationID: "O1", Scope: "tree"}
	if failure := checkInspectionTreeBlock(&callResult{texts: []string{inspect}},
		inspection, "O1"); failure != nil {
		t.Fatalf("complete v3 inspect rejected: %s %v", failure.reason, failure.fields)
	}
}

func TestDecodeResultTextV4RejectsInvalidBodies(t *testing.T) {
	fullHeader := "tree v4 full nodes="
	cases := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name: "full nodes mismatch",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_count_mismatch",
		},
		{
			name: "full count not canonical",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "01\n\n" +
				"n1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_count_invalid",
		},
		{
			name: "full count overflow",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "999999999999999999999999999\n\n" +
				"n1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_count_invalid",
		},
		{
			name: "delta changes mismatch",
			text: "result v3 is_error=false\nobservation\n id=O2\n\n" +
				"tree v4 delta base=O1 changes=2\n\n" +
				"~ n2 checked=false\nend tree\nend result\n",
			reason: "text_tree_count_mismatch",
		},
		{
			name: "short base zero",
			text: "result v3 is_error=false\nobservation\n id=Otest_2\n\n" +
				"tree v4 delta base=_0 changes=0\n\n" +
				"end tree\nend result\n",
			reason: "text_tree_base_invalid",
		},
		{
			name: "short base current has no suffix",
			text: "result v3 is_error=false\nobservation\n id=Otest\n\n" +
				"tree v4 delta base=_2 changes=0\n\n" +
				"end tree\nend result\n",
			reason: "text_tree_base_current_invalid",
		},
		{
			name: "short base uint64 overflow",
			text: "result v3 is_error=false\nobservation\n id=Otest_2\n\n" +
				"tree v4 delta base=_18446744073709551616 changes=0\n\n" +
				"end tree\nend result\n",
			reason: "text_tree_base_invalid",
		},
		{
			name: "query matches mismatch",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=query observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed " +
				"matches=2 total=2 truncated=false nodes=1 entries=1\n\n" +
				"match 1/1 path=[n1]\nn2 text [0,0,10,10] \"one\"\n" +
				"end inspect\nend result\n",
			reason: "text_inspect_count_mismatch",
		},
		{
			name: "folded chain rejected",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "3\n\n" +
				"n1 root 0,0,1080,2400\n [n10 n11]\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "detailed chain",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=2 entries=2\n\n" +
				"n1 root [0,0,1080,2400]\n" +
				"  chain [n2 n3] group [0,0,10,10]\nend inspect\nend result\n",
			reason: "text_inspect_body_invalid",
		},
		{
			name: "detailed omitted geometry",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
				"n1 root\nend inspect\nend result\n",
			reason: "text_inspect_body_invalid",
		},
		{
			name: "full root not root",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "1\n\n" +
				"n1 view 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "two-space child indentation",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n  n2 \"x\"\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "tab indentation",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n\tn2 \"x\"\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "depth jump",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n    n2 \"x\"\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "bad geometry",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 button 0,0,10\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "bracketed geometry in v4",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 button [0,0,10,10]\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "coordinate leading zero",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 button 00,0,10,10\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "negative zero coordinate",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 button -0,0,10,10\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "flag out of order",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 checked=true enabled=false\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "unknown column",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 future=value\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "invalid enabled flag value",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 button 0,0,10,10 enabled=maybe\nend tree\nend result\n",
			reason: "text_tree_flag_invalid",
		},
		{
			name: "invalid checked flag value",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 checked=0\nend tree\nend result\n",
			reason: "text_tree_flag_invalid",
		},
		{
			name: "invalid inspect flag value",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
				"n1 root [0,0,1080,2400] focused=yes\nend inspect\nend result\n",
			reason: "text_inspect_flag_invalid",
		},
		{
			name: "geometry after omitted text label in compact tree",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 \"设置\" 0,0,10,10\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "geometry after omitted text label in detailed inspect",
			text: "result v3 is_error=false\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=now " +
				"tree_scope=reported_visible_with_context view=detailed nodes=2 entries=2\n\n" +
				"n1 root [0,0,1080,2400]\n  n2 \"设置\" [0,0,10,10]\nend inspect\nend result\n",
			reason: "text_inspect_body_invalid",
		},
		{
			name: "at without known ancestor",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root -\n n2 button @\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "at does not borrow known sibling ancestor",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "3\n\n" +
				"n1 root -\n" +
				" n2 button 0,0,10,10\n" +
				" n3 button @\n" +
				"end tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "delta at does not borrow known sibling ancestor",
			text: "result v3 is_error=false\nobservation\n id=O2\n\n" +
				"tree v4 delta base=O1 changes=1\n\n" +
				"+ n1 group -\n" +
				" n2 button 0,0,10,10\n" +
				" n3 button @\n" +
				"end tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "at does not cross delta subtrees",
			text: "result v3 is_error=false\nobservation\n id=O2\n\n" +
				"tree v4 delta base=O1 changes=2\n\n" +
				"+ n1 group 0,0,10,10\n" +
				"= n2 button @\n" +
				"end tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "wrong role negation",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "1\n\n" +
				"n1 root 0,0,1080,2400 !tap\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "default action both explicit and negated",
			text: "result v3 is_error=false\nobservation\n id=O1\n\n" +
				fullHeader + "2\n\n" +
				"n1 root 0,0,1080,2400\n n2 button 0,0,10,10 tap !tap\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}
