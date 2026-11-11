package main

import (
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
			text:   "result v1 is_error=false\nstatus=not_seen\n",
			reason: "text_result_unterminated",
		},
		{
			name: "omitted tree",
			text: "result v1 is_error=false\nobservation.id=\"O1\"\n\n" +
				"tree v1 omitted reason=ref_format\nend result\n",
			reason: "text_result_block_omitted",
		},
		{
			name: "duplicate path",
			text: "result v1 is_error=false\njson v1 path=\"/status\" value=\"a\"\n" +
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
	text := "result v1 is_error=false\n" +
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
