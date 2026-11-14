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
		"result v2 is_error=false\n" +
			"observation.quality.state=unknown\n" +
			"observation.quality.samples=0\n" +
			"observation.quality.stop_reason=not_sampled\n" +
			"end result\n",
	}}
	if failure := requireQualityTextLines(projected, quality); failure != nil {
		t.Fatalf("samples=0 lines rejected: %s", failure.reason)
	}
	projected.texts = []string{"observation.quality.state=unknown\nobservation.quality.samples=0\n"}
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

// TestDecodeResultTextQualityAndEffect pins the generic oracle: effect and
// observation quality are ordinary dotted paths, not a special block.
func TestDecodeResultTextQualityAndEffect(t *testing.T) {
	text := "result v2 is_error=false\n" +
		"operation_id=Pscroll000000000000000001\n" +
		"outcome=executed\n" +
		"effect.state=observed\n" +
		"observation.status=complete\n" +
		"observation.id=Oscroll00000000000000001\n" +
		"observation.quality.state=unstable\n" +
		"observation.quality.samples=5\n" +
		"observation.quality.stop_reason=max_samples\n" +
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
			"view": "compact", "defaults": map[string]any{"checked": false},
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

func TestDecodeResultTextRejectsIncompleteProjection(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name:   "missing tail",
			text:   "result v2 is_error=false\nstatus=not_seen\n",
			reason: "text_result_unterminated",
		},
		{
			name: "omitted tree",
			text: "result v2 is_error=false\nobservation.id=\"O1\"\n\n" +
				"tree v2 omitted reason=ref_format\nend result\n",
			reason: "text_result_block_omitted",
		},
		{
			name: "duplicate path",
			text: "result v2 is_error=false\njson v1 path=\"/status\" value=\"a\"\n" +
				"json v1 path=\"/status\" value=\"b\"\nend result\n",
			reason: "text_result_path_duplicate",
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
	text := "result v2 is_error=false\n" +
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
