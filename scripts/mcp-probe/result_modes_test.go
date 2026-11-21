package main

import (
	"encoding/json/jsontext"
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
		"operation_id": "P7391_1",
		"outcome":      outcomeExecuted,
		"session": map[string]any{
			"origin": "P7391_1", "state": sessionActive, "mode": "virtual",
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
		if projected.view.OperationID != "P7391_1" || len(projected.images) != 1 {
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
		if projected.view.OperationID != "P7391_1" || len(projected.structured) != 0 ||
			len(projected.images) != 1 || !projected.textOnly {
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
		if projected.view.OperationID != "P7391_1" || len(projected.texts) != 0 {
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
	t.Run("text default omission", func(t *testing.T) {
		projected := &callResult{
			textOnly: true,
			texts:    []string{"result v4 ok\nobs O7391_1 at=22:08:55\nend result\n"},
			object: map[string]any{
				"observation": map[string]any{
					"status": observationComplete, "id": "O7391_1",
				},
			},
			view: resultView{Observation: &observationView{
				Status: observationComplete, ID: "O7391_1",
			}},
		}
		if failure := checkObservationQuality(projected, projected.view.Observation); failure != nil {
			t.Fatalf("default omission rejected: %s", failure.reason)
		}
	})
}

// TestCheckQualityTextLine proves the v4 quality contract: the default pair is
// omitted and a non-default quality is rendered once as state(stop_reason).
func TestCheckQualityTextLine(t *testing.T) {
	defaultProjected := &callResult{
		textOnly: true,
		texts: []string{
			"result v4 ok\nobs O7391_1 at=22:08:55\nend result\n",
		},
		view: resultView{Observation: &observationView{ID: "O7391_1"}},
	}
	if failure := checkQualityTextLine(defaultProjected); failure != nil {
		t.Fatalf("default quality omission rejected: %s", failure.reason)
	}
	nonDefault := &callResult{
		textOnly: true,
		texts: []string{
			"result v4 ok\nobs O7391_1 quality=unstable(max_samples)\nend result\n",
		},
		view: resultView{Observation: &observationView{
			ID:      "O7391_1",
			Quality: &qualityView{State: "unstable", StopReason: "max_samples"},
		}},
	}
	if failure := checkQualityTextLine(nonDefault); failure != nil {
		t.Fatalf("non-default quality rejected: %s", failure.reason)
	}
	unexpected := &callResult{
		textOnly: true,
		texts: []string{
			"result v4 ok\nobs O7391_1 quality=stable(stable)\nend result\n",
		},
		view: resultView{Observation: &observationView{ID: "O7391_1"}},
	}
	if failure := checkQualityTextLine(unexpected); failure == nil ||
		failure.reason != "text_line_unexpected" {
		t.Fatalf("default quality token = %v, want text_line_unexpected", failure)
	}
	missing := &callResult{
		textOnly: true,
		texts: []string{
			"result v4 ok\nobs O7391_1 at=22:08:55\nend result\n",
		},
		view: resultView{Observation: &observationView{
			ID:      "O7391_1",
			Quality: &qualityView{State: "unstable", StopReason: "max_samples"},
		}},
	}
	if failure := checkQualityTextLine(missing); failure == nil ||
		failure.reason != "text_line_missing" {
		t.Fatalf("missing non-default quality = %v, want text_line_missing", failure)
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

func TestDecodeResultTextQualityAndEffect(t *testing.T) {
	text := "result v4 ok op=P7391_12\n" +
		"effect=observed\n" +
		"obs O4821_85 at=22:08:55 quality=unstable(max_samples)\n" +
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
	if quality.State != "unstable" || quality.StopReason != "max_samples" {
		t.Fatalf("quality = %+v", quality)
	}
	state, found := lookupPath(decoded.object, []string{"effect", "state"})
	if !found || state != "observed" {
		t.Fatalf("effect object = %#v, %v", state, found)
	}
}

func TestProjectQualityAndEffectChannels(t *testing.T) {
	observation := map[string]any{
		"status": observationComplete,
		"id":     "O4821_85",
		"quality": map[string]any{
			"state": "stable", "samples": 2, "stop_reason": "stable",
		},
		"tree": map[string]any{
			"format": treeFull, "scope": "reported_visible_with_context",
			"view": "compact",
		},
	}
	payload := map[string]any{
		"operation_id": "P7391_12",
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
			if mode == resultModeText {
				// The stable quality is the default omission; text mode must not
				// invent a quality object.
				if projected.view.Observation.Quality != nil {
					t.Fatalf("text default quality = %+v", projected.view.Observation.Quality)
				}
				if failure := checkObservationQualityOmitted(projected); failure != nil {
					t.Fatalf("default quality token: %s", failure.reason)
				}
				return
			}
			quality := projected.view.Observation.Quality
			if quality == nil || quality.State != "stable" || quality.Samples != 2 ||
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

	t.Run("both mode rejects missing non-default quality", func(t *testing.T) {
		nonDefault := map[string]any{
			"operation_id": payload["operation_id"],
			"outcome":      outcomeExecuted,
			"effect":       payload["effect"],
			"observation": map[string]any{
				"status": observationComplete,
				"id":     observation["id"],
				"quality": map[string]any{
					"state": "unstable", "samples": 5, "stop_reason": "max_samples",
				},
				"tree": observation["tree"],
			},
		}
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
			StructuredContent: nonDefault,
		}
		if _, failure := project(result, resultModeBoth); failure == nil ||
			failure.reason != "both_mode_quality_missing" {
			t.Fatalf("failure = %v, want both_mode_quality_missing", failure)
		}
	})
}

func TestProjectResultModeViolations(t *testing.T) {
	payload := map[string]any{"operation_id": "P7391_1"}
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
		"operation_id": "P4821_1",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_1",
			"tree": map[string]any{
				"format": treeFull,
				"scope":  "reported_visible_with_context",
				"view":   "compact",
			},
		},
	}
	fullText := "result v4 ok op=P4821_1\nobs O4821_1\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	fullResult := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: fullText}},
		StructuredContent: fullPayload,
	}
	if _, failure := project(fullResult, resultModeBoth); failure != nil {
		t.Fatalf("matching full metadata rejected: %s %v", failure.reason, failure.fields)
	}

	extraMetadata := map[string]any{
		"operation_id": fullPayload["operation_id"],
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_1",
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
		"operation_id": "P4821_2",
		"observation": map[string]any{
			"status": observationComplete,
			"id":     "O4821_2",
			"tree": map[string]any{
				"format": treeDelta,
				"base":   "O4821_1",
				"view":   "compact",
			},
		},
	}
	deltaText := "result v4 ok op=P4821_2\nobs O4821_2\n\n" +
		"tree v4 delta base=_1 changes=0\n\nend tree\nend result\n"
	deltaResult := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: deltaText}},
		StructuredContent: deltaPayload,
	}
	if _, failure := project(deltaResult, resultModeBoth); failure != nil {
		t.Fatalf("matching expanded base rejected: %s %v", failure.reason, failure.fields)
	}

	deltaPayload["observation"].(map[string]any)["tree"].(map[string]any)["base"] = "O4821_9"
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
			"future": "kept",
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
		t.Fatalf("decodeResultText: %v %v", failure.reason, failure.fields)
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
	if value, ok := lookupPath(decoded.object, []string{"app", "future"}); !ok || value != "kept" {
		t.Fatalf("unknown app member = %#v, %v", value, ok)
	}
}

func TestDecodeResultTextEmptyDeltaWithWait(t *testing.T) {
	text := "result v4 ok op=P7391_3 phase=finished\n" +
		"obs O4821_9 at=22:08:55\n" +
		"\n" +
		"tree v4 delta base=_8 changes=0\n" +
		"\n" +
		"end tree\n" +
		"wait trigger=timeout matched=timeout elapsed=1000ms\n" +
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
	text := "result v4 ok\n" +
		"\n" +
		"inspect v3 scope=query observation=O1 revision=1 captured_at=22:00:00 " +
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

// TestDecodeResultTextHeaderGrammar pins the complete v4 header grammar and its
// rejection cases.
func TestDecodeResultTextHeaderGrammar(t *testing.T) {
	accepted := []struct {
		text   string
		header resultHeader
	}{
		{
			text:   "result v4 ok\nend result\n",
			header: resultHeader{Ok: true},
		},
		{
			text: "result v4 ok op=P7391_1 state=running outcome=executed phase=post_observation_failed\nend result\n",
			header: resultHeader{
				Ok: true, OperationID: "P7391_1", State: "running", Outcome: "executed",
				HasOutcome: true, Phase: "post_observation_failed",
			},
		},
		{
			text:   "result v4 err outcome=not_executed phase=admission\nend result\n",
			header: resultHeader{Outcome: "not_executed", HasOutcome: true, Phase: "admission"},
		},
		{
			// An error result always keeps its outcome explicit, even for the
			// conventionally successful value.
			text:   "result v4 err outcome=executed\nend result\n",
			header: resultHeader{Outcome: "executed", HasOutcome: true},
		},
	}
	for _, testCase := range accepted {
		decoded, failure := decodeResultText(testCase.text)
		if failure != nil {
			t.Fatalf("valid header rejected: %s %v", failure.reason, failure.fields)
		}
		if decoded.header != testCase.header {
			t.Fatalf("header = %+v, want %+v", decoded.header, testCase.header)
		}
	}
	rejected := []struct {
		name   string
		text   string
		reason string
	}{
		{"v3 header", "result v3 is_error=false\nend result\n", "text_result_header_invalid"},
		{"missing status", "result v4\nend result\n", "text_result_header_invalid"},
		{"unknown status", "result v4 maybe\nend result\n", "text_result_header_invalid"},
		{"duplicate op", "result v4 ok op=P1 op=P2\nend result\n", "text_result_header_field_invalid"},
		{"out of order", "result v4 ok outcome=uncertain op=P1\nend result\n", "text_result_header_field_invalid"},
		{"empty value", "result v4 ok op=\nend result\n", "text_result_header_field_invalid"},
		{"explicit executed", "result v4 ok outcome=executed\nend result\n", "text_result_outcome_redundant"},
		{"double space", "result v4  ok\nend result\n", "text_result_header_invalid"},
		{"bare cr", "result v4 ok\r\nend result\n", "text_result_naked_cr"},
		{"extra blank", "result v4 ok\n\n\nend result\n", "text_result_blank_invalid"},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

// TestDecodeResultTextObsLine pins the obs line grammar, including the empty
// line ban and the conditional screen/rotation rules.
func TestDecodeResultTextObsLine(t *testing.T) {
	accepted := []struct {
		name     string
		text     string
		status   string
		id       string
		rotation int
	}{
		{
			name:   "minimal with id",
			text:   "result v4 ok\nobs O4821_85\nend result\n",
			status: observationComplete,
			id:     "O4821_85",
		},
		{
			name:   "explicit complete without id",
			text:   "result v4 ok\nobs status=complete\nend result\n",
			status: observationComplete,
		},
		{
			name:   "failed without id",
			text:   "result v4 ok\nobs status=failed missing=tree\nend result\n",
			status: "failed",
		},
		{
			name:     "rotation without screen",
			text:     "result v4 ok\nobs O4821_85 rot=2\nend result\n",
			status:   observationComplete,
			id:       "O4821_85",
			rotation: 2,
		},
		{
			name: "full fields",
			text: "result v4 ok\n" +
				"obs O4821_85 fg=tv.danmaku.bili#177 at=22:08:55 quality=unstable(max_samples) rot=1 missing=tree,image\n" +
				"end result\n",
			status:   observationComplete,
			id:       "O4821_85",
			rotation: 1,
		},
	}
	for _, testCase := range accepted {
		t.Run(testCase.name, func(t *testing.T) {
			decoded, failure := decodeResultText(testCase.text)
			if failure != nil {
				t.Fatalf("valid obs rejected: %s %v", failure.reason, failure.fields)
			}
			if decoded.view.Observation == nil || decoded.view.Observation.Status != testCase.status ||
				decoded.view.Observation.ID != testCase.id {
				t.Fatalf("observation = %+v", decoded.view.Observation)
			}
			if testCase.rotation != 0 {
				if decoded.view.Observation.Screen == nil ||
					decoded.view.Observation.Screen.Rotation != testCase.rotation {
					t.Fatalf("screen = %+v", decoded.view.Observation.Screen)
				}
			}
		})
	}
	rejected := []struct {
		name   string
		text   string
		reason string
	}{
		{"bare obs", "result v4 ok\nobs\nend result\n", "text_result_obs_empty"},
		{"complete with id", "result v4 ok\nobs O4821_85 status=complete\nend result\n", "text_result_obs_status_redundant"},
		{"default quality", "result v4 ok\nobs O4821_85 quality=stable(stable)\nend result\n", "text_result_obs_quality_redundant"},
		{"not sampled quality", "result v4 ok\nobs O4821_85 quality=unknown(not_sampled)\nend result\n", "text_result_obs_quality_redundant"},
		{"bad rotation", "result v4 ok\nobs O4821_85 rot=4\nend result\n", "text_result_rotation_invalid"},
		{"bad time", "result v4 ok\nobs O4821_85 at=bad=time\nend result\n", "text_result_obs_invalid"},
		{"out of order", "result v4 ok\nobs O4821_85 at=22:08:55 fg=none\nend result\n", "text_result_obs_invalid"},
		{"duplicate obs", "result v4 ok\nobs O4821_85\nobs O4821_86\nend result\n", "text_result_path_duplicate"},
		{
			name: "screen with tree but no session",
			text: "result v4 ok\nobs O4821_85 screen=1080x2400@148\n\n" +
				"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_result_obs_screen_redundant",
		},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
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
			text:   "result v4 ok\nblocking_code=not_seen\n",
			reason: "text_result_unterminated",
		},
		{
			name: "empty delta missing tree tail",
			text: "result v4 ok\nobs O4821_2\n\n" +
				"tree v4 delta base=O4821_1 changes=0\n\n" +
				"end result\n",
			reason: "text_tree_unterminated",
		},
		{
			name: "omitted tree",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 omitted reason=ref_format\nend result\n",
			reason: "text_result_block_omitted",
		},
		{
			name: "duplicate path",
			text: "result v4 ok\njson v1 path=\"/blocking_code\" value=\"a\"\n" +
				"json v1 path=\"/blocking_code\" value=\"b\"\nend result\n",
			reason: "text_result_path_duplicate",
		},
		{
			name:   "duplicate scalar",
			text:   "result v4 ok\ncan_start=true\ncan_start=false\nend result\n",
			reason: "text_result_path_duplicate",
		},
		{
			name:   "illegal boolean token",
			text:   "result v4 ok\ncan_start=1\nend result\n",
			reason: "text_result_value_invalid",
		},
		{
			name: "legacy tree defaults line",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 full nodes=1\n" +
				"defaults {\"checked\":false}\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_separator_invalid",
		},
		{
			name: "legacy inspection defaults line",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n" +
				"defaults {\"checked\":false}\n\nn1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_separator_invalid",
		},
		{
			name: "defaults line inside tree body",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 full nodes=1\n\n" +
				"defaults {\"checked\":false}\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_body_invalid",
		},
		{
			name: "full node writes checked default",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 full nodes=1\n\n" +
				"n1 root 0,0,1080,2400 checked=false\nend tree\nend result\n",
			reason: "text_tree_flag_default",
		},
		{
			name: "full node writes enabled default",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 full nodes=1\n\n" +
				"n1 root 0,0,1080,2400 enabled=true\nend tree\nend result\n",
			reason: "text_tree_flag_default",
		},
		{
			name: "inspection node writes checked default",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
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
	text := "result v4 ok\n" +
		"json v1 path=\"/future/a~1b~0c\" value={\"value\":1}\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decodeResultText: %s", failure.reason)
	}
	value, ok := lookupPath(decoded.object, []string{"future", "a/b~c", "value"})
	number, integer := valueInteger(value)
	if !ok || !integer || number != 1 {
		t.Fatalf("decoded pointer value = %#v, %v", value, ok)
	}
}

func TestDecodeResultTextJSONFallbackNumericObjectKey(t *testing.T) {
	text := "result v4 ok\n" +
		"json v1 path=\"/future/0\" value=\"unknown\"\n" +
		"json v1 path=\"/future/name\" value=\"kept\"\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decodeResultText: %s %v", failure.reason, failure.fields)
	}
	future, ok := lookupPath(decoded.object, []string{"future"})
	futureObject, object := future.(map[string]any)
	if !ok || !object || futureObject["0"] != "unknown" || futureObject["name"] != "kept" {
		t.Fatalf("future object = %#v", future)
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
	text := "result v4 ok\nobs O4821_1\n\n" +
		"tree v4 full nodes=3\n\n" +
		"n1 root 0,0,1080,2400\n" +
		" n2 text \"checked=false is a label\" value=\"x\" enabled=false focused=true selected=null protected=null checked=null\n" +
		" n3 button enabled=null checked=true\n" +
		"end tree\nend result\n"
	if _, failure := decodeResultText(text); failure != nil {
		t.Fatalf("valid tri-state full tree rejected: %s %v", failure.reason, failure.fields)
	}

	delta := "result v4 ok\nobs O4821_2\n\n" +
		"tree v4 delta base=O4821_1 changes=2\n\n" +
		"~ n2 checked=false focused=null\n" +
		"+ n4 text 0,0,10,10 checked=true\n" +
		"end tree\nend result\n"
	if _, failure := decodeResultText(delta); failure != nil {
		t.Fatalf("valid delta set rejected: %s %v", failure.reason, failure.fields)
	}

	inspection := "result v4 ok\n\n" +
		"inspect v3 scope=query observation=O1 revision=1 captured_at=22:00:00 " +
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
	inspectTree := "inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
		"tree_scope=reported_visible_with_context view=detailed nodes=3 entries=3\n\n" +
		"n1 root [0,0,1080,2400]\n" +
		"  n2 text [0,0,10,10] \"Hello\" value=\"v\"\n" +
		"  n3 view [0,0,20,20] text\n" +
		"end inspect\n"
	inspectSubtree := "inspect v3 scope=subtree observation=O1 revision=1 captured_at=22:00:00 " +
		"tree_scope=reported_visible_with_context view=detailed root=n5 nodes=2 entries=2\n\n" +
		"n5 group [0,0,10,10] \"sub\"\n" +
		"  n6 [0,0,5,5] tap\n" +
		"end inspect\n"
	inspectQuery := "inspect v3 scope=query observation=O1 revision=1 captured_at=22:00:00 " +
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
			text: "result v4 ok\nobs Otest_1\n\n" + full + "end result\n",
		},
		{
			name: "delta",
			text: "result v4 ok\nobs Otest_2\n\n" + delta + "end result\n",
		},
		{
			name:  "inspect tree",
			text:  "result v4 ok\n\n" + inspectTree + "end result\n",
			scope: "tree",
		},
		{
			name:  "inspect subtree",
			text:  "result v4 ok\n\n" + inspectSubtree + "end result\n",
			scope: "subtree",
		},
		{
			name:  "inspect query",
			text:  "result v4 ok\n\n" + inspectQuery + "end result\n",
			scope: "query",
		},
		{
			name: "empty delta",
			text: "result v4 ok\nobs Otest_3\n\n" +
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

func TestShortTreeBaseUsesUint64Sequence(t *testing.T) {
	text := "result v4 ok\nobs Otest_18446744073709551615\n\n" +
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

	full := "result v4 ok\nobs Oabc+foo_2\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	if _, failure := decodeResultText(full); failure != nil {
		t.Fatalf("full v4 O rejected: %s %v", failure.reason, failure.fields)
	}
	fullMaxO := "result v4 ok\nobs " + maxO + "\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	if _, failure := decodeResultText(fullMaxO); failure != nil {
		t.Fatalf("64-byte v4 O rejected: %s %v", failure.reason, failure.fields)
	}
	observation := &observationView{ID: maxO, Tree: &treeView{Format: treeFull}}
	if failure := checkObservationTreeBlock(&callResult{texts: []string{fullMaxO}},
		observation); failure != nil {
		t.Fatalf("64-byte tree block O rejected: %s %v", failure.reason, failure.fields)
	}
}

func TestDecodeResultTextActionReconstructionSyntax(t *testing.T) {
	valid := []string{
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 0,0,1,1\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 0,0,1,1 tap\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 0,0,1,1 tap long_press\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 0,0,1,1 tap long_press text swipe\nend tree\nend result\n",
	}
	for _, text := range valid {
		if _, failure := decodeResultText(text); failure != nil {
			t.Fatalf("valid action reconstruction rejected: %s %v", failure.reason, failure.fields)
		}
	}
	invalid := []string{
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 0,0,1,1 !tap tap\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 view 0,0,1,1 !tap\nend tree\nend result\n",
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\n n2 button 0,0,1,1 tap swipe tap\nend tree\nend result\n",
	}
	for _, text := range invalid {
		if _, failure := decodeResultText(text); failure == nil {
			t.Fatalf("invalid action reconstruction accepted")
		}
	}
}

func TestDecodeResultTextRejectsLegacyAndMissingBlocks(t *testing.T) {
	legacyResult := "result v2 is_error=false\nblocking_code=complete\nend result\n"
	if _, failure := decodeResultText(legacyResult); failure == nil ||
		failure.reason != "text_result_header_invalid" {
		t.Fatalf("legacy result = %v", failure)
	}
	legacyV3 := "result v3 is_error=false\nend result\n"
	if _, failure := decodeResultText(legacyV3); failure == nil ||
		failure.reason != "text_result_header_invalid" {
		t.Fatalf("result v3 accepted: %v", failure)
	}
	legacyTree := "result v4 ok\nobs O4821_1\n\n" +
		"tree v3 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	legacyInspect := "result v4 ok\n\n" +
		"inspect v2 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
		"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
		"n1 root [0,0,1080,2400]\nend inspect\nend result\n"
	legacyTreeWithoutSeparator := "result v4 ok\nblocking_code=complete\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	for name, text := range map[string]string{
		"legacy tree":                   legacyTree,
		"legacy inspect":                legacyInspect,
		"legacy tree without separator": legacyTreeWithoutSeparator,
	} {
		t.Run(name, func(t *testing.T) {
			if _, failure := decodeResultText(text); failure == nil ||
				failure.reason != "text_result_block_unknown" && failure.reason != "text_result_block_separator_missing" {
				t.Fatalf("failure = %v", failure)
			}
		})
	}
}

func TestV4BlockChecksRequireCompleteBlocks(t *testing.T) {
	projected := &callResult{texts: []string{
		"result v4 ok\nobs O4821_1\n\n" +
			"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
	}}
	block, failure := treeBlockText(projected, "tree v4 full nodes=")
	if failure != nil {
		t.Fatalf("complete tree block rejected: %s", failure.reason)
	}
	if !strings.HasSuffix(block, "end tree\n") {
		t.Fatalf("tree block = %q", block)
	}

	missing := "result v4 ok\nobs O4821_1\n\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend result\n"
	if _, failure := decodeResultText(missing); failure == nil ||
		failure.reason != "text_tree_unterminated" {
		t.Fatalf("unterminated tree = %v", failure)
	}

	projected.texts = []string{"result v4 ok\nobs O4821_1\nend result\n"}
	if _, failure := treeBlockText(projected, "tree v4 full nodes="); failure == nil ||
		failure.reason != "text_tree_block_missing" {
		t.Fatalf("missing tree block = %v", failure)
	}
}

func TestDecodeResultTextV4RejectsInvalidBodies(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name: "tree full count invalid",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 full nodes=0\n\nend tree\nend result\n",
			reason: "text_tree_count_invalid",
		},
		{
			name: "tree full count mismatch",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 full nodes=2\n\nn1 root 0,0,1080,2400\nend tree\nend result\n",
			reason: "text_tree_count_mismatch",
		}, {
			name: "tree delta base invalid",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 delta base=_0 changes=0\n\nend tree\nend result\n",
			reason: "text_tree_base_invalid",
		},
		{
			name: "tree missing observation id",
			text: "result v4 ok\n\n" +
				"tree v4 delta base=O4821_1 changes=0\n\nend tree\nend result\n",
			reason: "text_tree_id_missing",
		},
		{
			name: "tree id outside obs line",
			text: "result v4 ok\njson v1 path=\"/observation/id\" value=\"O4821_1\"\n\n" +
				"tree v4 delta base=O4821_1 changes=0\n\nend tree\nend result\n",
			reason: "text_tree_id_group_required",
		},
		{
			name: "tree count mismatch",
			text: "result v4 ok\nobs O4821_1\n\n" +
				"tree v4 delta base=_1 changes=3\n\n~ n2 checked=false\nend tree\nend result\n",
			reason: "text_tree_count_mismatch",
		},
		{
			name: "inspect unknown scope",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=page observation=O1 revision=1 captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
				"n1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_scope_invalid",
		},
		{
			name: "inspect revision invalid",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=tree observation=O1 revision=x captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
				"n1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_revision_invalid",
		},
		{
			name: "inspect nodes mismatch",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=detailed nodes=2 entries=1\n\n" +
				"n1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_count_mismatch",
		},
		{
			name: "inspect missing observation",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=tree revision=1 captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n\n" +
				"n1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_observation_missing",
		},
		{
			name: "inspect bad view",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=tree observation=O1 revision=1 captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=compact nodes=1 entries=1\n\n" +
				"n1 root [0,0,1080,2400]\nend inspect\nend result\n",
			reason: "text_inspect_view_invalid",
		},
		{
			name: "inspect query matches mismatch",
			text: "result v4 ok\n\n" +
				"inspect v3 scope=query observation=O1 revision=1 captured_at=22:00:00 " +
				"tree_scope=reported_visible_with_context view=detailed matches=2 total=2 truncated=false nodes=1 entries=1\n\n" +
				"match 1/2 path=[n1]\nn2 text [0,0,1,1]\nend inspect\nend result\n",
			reason: "text_inspect_count_mismatch",
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

// TestDecodeResultTextAppBlocks pins the two formal app collection blocks,
// including the count=0 edge that distinguishes "empty result" from "not
// obtained".
func TestDecodeResultTextAppBlocks(t *testing.T) {
	find := "result v4 ok op=P7391_3\n" +
		"app find total=0 truncated=false at=22:05:20\n" +
		"\n" +
		"app_items v1 kind=find count=0\n" +
		"end app_items\n" +
		"end result\n"
	decoded, failure := decodeResultText(find)
	if failure != nil {
		t.Fatalf("empty find rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.App == nil || !decoded.view.App.HasItems || len(decoded.view.App.Items) != 0 ||
		!decoded.view.App.HasTotal || decoded.view.App.Total != 0 {
		t.Fatalf("empty find facts = %+v", decoded.view.App)
	}

	list := "result v4 ok op=P7391_4\n" +
		"app list display=0 total=1 truncated=false fg=none at=22:05:21\n" +
		"\n" +
		"app_items v1 kind=list count=1\n" +
		"#42 com.example.probe owned origin_display=0\n" +
		"end app_items\n" +
		"end result\n"
	decoded, failure = decodeResultText(list)
	if failure != nil {
		t.Fatalf("list rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.App == nil || len(decoded.view.App.TaskItems) != 1 ||
		decoded.view.App.TaskItems[0].TaskID != 42 ||
		!decoded.view.App.TaskItems[0].HasOriginDisplay {
		t.Fatalf("list facts = %+v", decoded.view.App)
	}

	closeText := "result v4 err op=P7391_5 outcome=uncertain\n" +
		"app close display=12 complete=false\n" +
		"\n" +
		"app_tasks v1 count=0\n" +
		"end app_tasks\n" +
		"end result\n"
	decoded, failure = decodeResultText(closeText)
	if failure != nil {
		t.Fatalf("empty close rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.App == nil || !decoded.view.App.HasTasks || len(decoded.view.App.Tasks) != 0 {
		t.Fatalf("empty close facts = %+v", decoded.view.App)
	}

	mismatch := "result v4 ok op=P7391_6\n" +
		"app find total=1 truncated=false at=22:05:20\n" +
		"\n" +
		"app_items v1 kind=find count=2\n" +
		"com.example.probe \"Probe\"\n" +
		"end app_items\n" +
		"end result\n"
	if _, failure := decodeResultText(mismatch); failure == nil ||
		failure.reason != "text_result_app_items_count_mismatch" {
		t.Fatalf("count mismatch = %v", failure)
	}
}

// TestDecodeResultTextNotifySessionAndImages pins the notify aggregate line,
// session=null and the standalone/observation image lines including non-JPEG
// media.
func TestDecodeResultTextNotifySessionAndImages(t *testing.T) {
	notify := "result v4 ok\n" +
		"notify id=msg_1 saved=true state=pending\n" +
		"notify.error delivery_unavailable \"控制端未连接\"\n" +
		"notify.error.details={\"retryable\":true}\n" +
		"end result\n"
	decoded, failure := decodeResultText(notify)
	if failure != nil {
		t.Fatalf("notify rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.MessageID != "msg_1" || decoded.view.Saved == nil || !*decoded.view.Saved ||
		decoded.view.Notification == nil || decoded.view.Notification.State != "pending" ||
		decoded.view.Notification.Error == nil ||
		decoded.view.Notification.Error.Code != "delivery_unavailable" {
		t.Fatalf("notify projection = %+v", decoded.view.Notification)
	}

	sessionNull := "result v4 ok\nsession=null\nend result\n"
	decoded, failure = decodeResultText(sessionNull)
	if failure != nil {
		t.Fatalf("session null rejected: %s", failure.reason)
	}
	if decoded.view.Session != nil {
		t.Fatalf("session null = %+v", decoded.view.Session)
	}

	image := "result v4 ok\n" +
		"image 100x200 read_only media=image/png screen=1080x2400@0 rot=1 at=now\n" +
		"image.error snap_failed \"截图失败\"\n" +
		"image.error.details={\"retryable\":true}\n" +
		"obs O4821_1\n" +
		"obs.image 461x1024 media=image/webp screen=720x1280 rot=3 at=22:00:00\n" +
		"obs.image.error decode_failed \"图像解码失败\"\n" +
		"obs.image.error.details={\"width\":461}\n" +
		"end result\n"
	decoded, failure = decodeResultText(image)
	if failure != nil {
		t.Fatalf("images rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Image == nil || decoded.view.Image.Width != 100 || decoded.view.Image.Height != 200 ||
		decoded.view.Image.MediaType != "image/png" || !decoded.view.Image.ReadOnly ||
		decoded.view.Image.Screen == nil || decoded.view.Image.Screen.Rotation != 1 ||
		decoded.view.Image.Error == nil || decoded.view.Image.Error.Code != "snap_failed" {
		t.Fatalf("top image = %+v", decoded.view.Image)
	}
	if decoded.view.Observation == nil || decoded.view.Observation.Image == nil ||
		decoded.view.Observation.Image.Width != 461 ||
		decoded.view.Observation.Image.Error == nil ||
		decoded.view.Observation.Image.Error.Code != "decode_failed" {
		t.Fatalf("observation image = %+v", decoded.view.Observation)
	}
}

// TestDecodeResultTextCleanupAndAppTaskError pins the two error sub-lines that
// carry command-specific failure facts.
func TestDecodeResultTextCleanupAndAppTaskError(t *testing.T) {
	cleanup := "result v4 err\n" +
		"cleanup unconfirmed exec=failed\n" +
		"cleanup.error display_release_failed \"显示释放失败\"\n" +
		"cleanup.error.details={\"display_id\":0}\n" +
		"end result\n"
	decoded, failure := decodeResultText(cleanup)
	if failure != nil {
		t.Fatalf("cleanup rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Cleanup == nil || decoded.view.Cleanup.State != "unconfirmed" ||
		decoded.view.Cleanup.Execution != "failed" || decoded.view.Cleanup.Error == nil ||
		decoded.view.Cleanup.Error.Code != "display_release_failed" {
		t.Fatalf("cleanup = %+v", decoded.view.Cleanup)
	}

	tasks := "result v4 err\n" +
		"app close display=12 complete=false\n" +
		"\n" +
		"app_tasks v1 count=1\n" +
		"#42 uncertain recents_cleaned=null error=task_remove_unconfirmed \"未确认\" details={\"code\":1}\n" +
		"end app_tasks\n" +
		"end result\n"
	decoded, failure = decodeResultText(tasks)
	if failure != nil {
		t.Fatalf("app tasks rejected: %s %v", failure.reason, failure.fields)
	}
	task := decoded.view.App.Tasks[0]
	if task.Error == nil || task.Error.Code != "task_remove_unconfirmed" ||
		task.RecentsCleaned != nil || !task.HasRecentsCleaned {
		t.Fatalf("app task = %+v", task)
	}
}

// TestDecodeResultTextDiagnosis pins the diagnosis head plus its two JSON
// sub-lines and the timeline block.
func TestDecodeResultTextDiagnosis(t *testing.T) {
	text := "result v4 ok\n" +
		"diagnosis summary=\"probe diagnosis\" coverage=original_record truncated=true op=P7391_1 outcome=uncertain\n" +
		"diagnosis.issues=[{\"phase\":\"session\",\"code\":\"drift\"}]\n" +
		"diagnosis.timing_ms={\"total\":12}\n" +
		"\n" +
		"timeline v1 events=1\n" +
		"event {\"at\":\"22:00:00\",\"phase\":\"session\",\"code\":\"drift\"}\n" +
		"end timeline\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("diagnosis rejected: %s %v", failure.reason, failure.fields)
	}
	if decoded.view.Diagnosis == nil || len(decoded.view.Diagnosis.Timeline) != 1 {
		t.Fatalf("timeline = %+v", decoded.view.Diagnosis)
	}
	issues, found := lookupPath(decoded.object, []string{"diagnosis", "issues"})
	items, ok := issues.([]any)
	if !found || !ok || len(items) != 1 {
		t.Fatalf("issues = %#v", issues)
	}
	timing, found := lookupPath(decoded.object, []string{"diagnosis", "timing_ms"})
	timingObject, ok := timing.(map[string]any)
	if !found || !ok || len(timingObject) != 1 {
		t.Fatalf("timing = %#v", timing)
	}
}

// TestDecodeResultTextRecoveryExactRef pins the recovery line grammar including
// both the abstract kind and a resolved exact ID.
func TestDecodeResultTextRecoveryExactRef(t *testing.T) {
	for _, reference := range []string{"none", "O", "P", "O4821_85", "P7391_12"} {
		text := "result v4 err\nrecovery entry=observe ref=" + reference +
			" input=retired cause=cleanup_pending\nend result\n"
		decoded, failure := decodeResultText(text)
		if failure != nil {
			t.Fatalf("recovery ref %q rejected: %s", reference, failure.reason)
		}
		if decoded.view.Recovery == nil || decoded.view.Recovery.Reference != reference {
			t.Fatalf("recovery = %+v", decoded.view.Recovery)
		}
	}
	bad := "result v4 err\nrecovery entry=observe ref=bad!=value\nend result\n"
	if _, failure := decodeResultText(bad); failure == nil ||
		failure.reason != "text_result_recovery_invalid" {
		t.Fatalf("bad ref = %v", failure)
	}
}

// TestCompareResultTextToStructuredOmissions covers the explicitly declared
// omitted fields and proves that each omission is conditional.
func TestCompareResultTextToStructuredOmissions(t *testing.T) {
	structured := map[string]any{
		"operation_id": "P7391_12",
		"outcome":      outcomeExecuted,
		"session": map[string]any{
			"origin": "P7391_12", "state": sessionActive, "mode": "virtual",
			"agent": "local-agent", "started_at": "2026-11-21T22:05:10.123456789Z",
			"expires_at": "2026-11-21T22:10:10.123456789Z",
		},
		"observation": map[string]any{
			"status": observationComplete, "id": "O4821_85",
			"origin": "P7391_12", "revision": 85,
			"captured_at":      "2026-11-21T22:08:55.867164298Z",
			"actionable_until": "2026-11-21T22:09:55.867164298Z",
			"quality": map[string]any{
				"state": "stable", "samples": 2, "stop_reason": "stable",
			},
			"screen": map[string]any{"width": 1080, "height": 2400, "rotation": 0, "display_id": 148},
			"tree":   map[string]any{"format": treeFull, "scope": "reported_visible_with_context", "view": "compact"},
		},
	}
	text := "result v4 ok op=P7391_12\n" +
		"session active mode=virtual origin=P7391_12 started=22:05:10 expires=22:10:10\n" +
		"obs O4821_85 fg=none at=22:08:55 screen=1080x2400@148\n" +
		"\n" +
		"tree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decode omitted frame: %s %v", failure.reason, failure.fields)
	}
	// The fixture has no foreground member; fg=none adds one. Drop it from both
	// sides before the consistency gate so only omission behavior is measured.
	delete(structured["observation"].(map[string]any), "foreground")
	observation := decoded.object["observation"].(map[string]any)
	delete(observation, "foreground")
	var structuredView resultView
	encoded, _ := json.Marshal(structured)
	if err := json.Unmarshal(encoded, &structuredView); err != nil {
		t.Fatal(err)
	}
	if failure := compareResultTextToStructured(decoded, structured, structuredView, ""); failure != nil {
		t.Fatalf("declared omissions rejected: %s %v", failure.reason, failure.fields)
	}

	t.Run("non-default quality must appear", func(t *testing.T) {
		structuredBad := deepCloneObject(structured)
		structuredBad["observation"].(map[string]any)["quality"] = map[string]any{
			"state": "unstable", "samples": 5, "stop_reason": "max_samples",
		}
		decodedBad := &decodedTextResult{object: deepCloneObject(decoded.object), isError: false,
			header: decoded.header, view: decoded.view}
		if failure := compareResultTextToStructured(decodedBad, structuredBad, structuredView, ""); failure == nil ||
			failure.reason != "both_mode_quality_missing" {
			t.Fatalf("missing non-default quality = %v", failure)
		}
	})

	t.Run("nonzero rotation must appear", func(t *testing.T) {
		structuredBad := deepCloneObject(structured)
		structuredBad["observation"].(map[string]any)["screen"] = map[string]any{
			"width": 1080, "height": 2400, "rotation": 2, "display_id": 148,
		}
		decodedBad := &decodedTextResult{object: deepCloneObject(decoded.object), isError: false,
			header: decoded.header, view: decoded.view}
		if failure := compareResultTextToStructured(decodedBad, structuredBad, structuredView, ""); failure == nil ||
			failure.reason != "both_mode_screen_rotation_mismatch" {
			t.Fatalf("missing rotation = %v", failure)
		}
	})

	t.Run("unknown field must be projected", func(t *testing.T) {
		structuredBad := deepCloneObject(structured)
		structuredBad["future"] = "must-survive"
		decodedBad := &decodedTextResult{object: deepCloneObject(decoded.object), isError: false,
			header: decoded.header, view: decoded.view}
		if failure := compareResultTextToStructured(decodedBad, structuredBad, structuredView, ""); failure == nil ||
			failure.reason != "both_mode_object_mismatch" {
			t.Fatalf("unknown field = %v", failure)
		}
	})
}

// TestResolveRecoveryReference pins every branch of the documented R1 rules,
// including the four safety red lines.
func TestResolveRecoveryReference(t *testing.T) {
	cases := []struct {
		name       string
		view       resultView
		requestRef string
		want       string
	}{
		{
			name: "none",
			view: resultView{Recovery: &recoveryView{Entry: "retry", Reference: "none"}},
			want: "none",
		},
		{
			name: "retry O from observation",
			view: resultView{
				Observation: &observationView{ID: "O4821_9"},
				Recovery:    &recoveryView{Entry: "retry", Reference: "O"},
			},
			want: "O4821_9",
		},
		{
			name:       "retry O from request",
			view:       resultView{Recovery: &recoveryView{Entry: "retry", Reference: "O"}},
			requestRef: "O4821_3",
			want:       "O4821_3",
		},
		{
			name: "status O from target",
			view: resultView{
				TargetReference: "O4821_4",
				Recovery:        &recoveryView{Entry: "status", Reference: "O"},
			},
			want: "O4821_4",
		},
		{
			name: "device busy exact",
			view: resultView{
				BlockingOperationID: "P7391_88",
				Error:               &errorView{Code: "device_busy"},
				Recovery:            &recoveryView{Entry: "status", Reference: "P"},
			},
			want: "P7391_88",
		},
		{
			name: "device busy redline falls back",
			view: resultView{
				OperationID: "P7391_1",
				Error:       &errorView{Code: "device_busy"},
				Session:     &sessionView{Origin: "P7391_1"},
				Recovery:    &recoveryView{Entry: "status", Reference: "P"},
			},
			want: "P",
		},
		{
			name: "reference has no session",
			view: resultView{
				TargetReference: "P7391_old",
				OperationID:     "P7391_new",
				Error:           &errorView{Code: "reference_has_no_session"},
				Recovery:        &recoveryView{Entry: "status", Reference: "P"},
			},
			want: "P7391_old",
		},
		{
			name: "running op wins over session origin",
			view: resultView{
				OperationID:    "P7391_pending",
				OperationState: operationRunning,
				Session:        &sessionView{Origin: "P7391_origin"},
				Recovery:       &recoveryView{Entry: "status", Reference: "P"},
			},
			want: "P7391_pending",
		},
		{
			name: "start operation",
			view: resultView{
				StartOperation: "P7391_start",
				Recovery:       &recoveryView{Entry: "status", Reference: "P"},
			},
			want: "P7391_start",
		},
		{
			name: "observe P from session",
			view: resultView{
				Session:  &sessionView{Origin: "P7391_origin"},
				Recovery: &recoveryView{Entry: "observe", Reference: "P"},
			},
			want: "P7391_origin",
		},
		{
			name: "observe P from request",
			view: resultView{
				Recovery: &recoveryView{Entry: "observe", Reference: "P"},
			},
			requestRef: "P7391_origin",
			want:       "P7391_origin",
		},
		{
			name: "observe P redline observation not found",
			view: resultView{
				OperationID: "P7391_own",
				Error:       &errorView{Code: "observation_not_found"},
				Recovery:    &recoveryView{Entry: "observe", Reference: "P"},
			},
			want: "P",
		},
		{
			name: "end P from session origin",
			view: resultView{
				Session:  &sessionView{Origin: "P7391_origin"},
				Recovery: &recoveryView{Entry: "end", Reference: "P"},
			},
			want: "P7391_origin",
		},
		{
			name: "end P redline without session",
			view: resultView{
				OperationID: "P7391_own",
				Error:       &errorView{Code: "session_ended"},
				Recovery:    &recoveryView{Entry: "end", Reference: "P"},
			},
			want: "P",
		},
		{
			name: "end P from error details",
			view: resultView{
				Error: &errorView{Code: "session_exists", Details: map[string]jsontext.Value{
					"start_operation_id": jsontext.Value(`"P7391_existing"`),
				}},
				Recovery: &recoveryView{Entry: "end", Reference: "P"},
			},
			want: "P7391_existing",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := resolveRecoveryReference(testCase.view, testCase.requestRef); actual != testCase.want {
				t.Fatalf("ref = %q, want %q", actual, testCase.want)
			}
		})
	}
}

// TestBothModeRecoveryExactRef accepts a grounded exact ref and rejects one
// that the same-call facts cannot prove.
func TestBothModeRecoveryExactRef(t *testing.T) {
	payload := map[string]any{
		"operation_id":          "P7391_5",
		"blocking_operation_id": "P7391_9",
		"error": map[string]any{
			"code": "device_busy", "message": "设备忙",
		},
		"recovery": map[string]any{"entry": "status", "reference": "P"},
	}
	goodText := fakeResultText(map[string]any{
		"operation_id":          payload["operation_id"],
		"blocking_operation_id": payload["blocking_operation_id"],
		"error":                 payload["error"],
		"recovery":              map[string]any{"entry": "status", "reference": "P7391_9"},
	}, true)
	result := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: goodText}},
		StructuredContent: payload,
		IsError:           true,
	}
	if _, failure := project(result, resultModeBoth, ""); failure != nil {
		t.Fatalf("grounded exact ref rejected: %s %v", failure.reason, failure.fields)
	}

	badText := fakeResultText(map[string]any{
		"operation_id":          payload["operation_id"],
		"blocking_operation_id": payload["blocking_operation_id"],
		"error":                 payload["error"],
		"recovery":              map[string]any{"entry": "status", "reference": "P7391_5"},
	}, true)
	badResult := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: badText}},
		StructuredContent: payload,
		IsError:           true,
	}
	if _, failure := project(badResult, resultModeBoth, ""); failure == nil ||
		failure.reason != "both_mode_recovery_ref_mismatch" {
		t.Fatalf("ungrounded exact ref = %v", failure)
	}
}
