package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	mcpserver "github.com/malyjacob/mobile-use-vnext/internal/mcp"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- config

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp-config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const validStdioConfig = `{
  "service": {"base_url": "http://127.0.0.1:18881", "agent_token": "agent-token"},
  "transport": "stdio",
  "http": {"listen": "127.0.0.1:18882", "token": "mcp-token", "allowed_origins": ["http://127.0.0.1:3000"]}
}`

func TestLoadConfig(t *testing.T) {
	t.Run("valid stdio", func(t *testing.T) {
		cfg, err := loadConfig(writeTestConfig(t, validStdioConfig), modeStdio)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.Service.BaseURL != "http://127.0.0.1:18881" || cfg.HTTP.Listen != "127.0.0.1:18882" {
			t.Fatalf("unexpected config projection")
		}
	})
	t.Run("unknown member rejected", func(t *testing.T) {
		body := strings.Replace(validStdioConfig, `"transport": "stdio",`, `"transport": "stdio", "extra": 1,`, 1)
		if _, err := loadConfig(writeTestConfig(t, body), modeStdio); err == nil {
			t.Fatal("unknown member was accepted")
		}
	})
	t.Run("duplicate member rejected", func(t *testing.T) {
		body := strings.Replace(validStdioConfig, `"transport": "stdio",`,
			`"transport": "stdio", "transport": "http",`, 1)
		if _, err := loadConfig(writeTestConfig(t, body), modeStdio); err == nil {
			t.Fatal("duplicate member was accepted")
		}
	})
	t.Run("shared tokens rejected for http mode", func(t *testing.T) {
		body := strings.Replace(validStdioConfig, `"token": "mcp-token"`, `"token": "agent-token"`, 1)
		_, err := loadConfig(writeTestConfig(t, body), modeHTTP)
		if err == nil || err.Error() != errHTTPTokenNotDistinct.Error() {
			t.Fatalf("err = %v, want %v", err, errHTTPTokenNotDistinct)
		}
	})
	t.Run("remote service rejected", func(t *testing.T) {
		body := strings.Replace(validStdioConfig, `http://127.0.0.1:18881`, `http://192.0.2.10:18881`, 1)
		if _, err := loadConfig(writeTestConfig(t, body), modeStdio); err == nil {
			t.Fatal("remote service URL was accepted")
		}
	})
	t.Run("http mode needs a listener", func(t *testing.T) {
		body := strings.Replace(validStdioConfig, `"listen": "127.0.0.1:18882"`, `"listen": ""`, 1)
		_, err := loadConfig(writeTestConfig(t, body), modeHTTP)
		if err == nil || err.Error() != errHTTPListenInvalid.Error() {
			t.Fatalf("err = %v, want %v", err, errHTTPListenInvalid)
		}
	})
}

// ---------------------------------------------------------------- schemas

func testSchema(name string, additional bool) map[string]any {
	properties := map[string]any{}
	required := []string{}
	switch name {
	case "observe":
		properties["reference"] = map[string]any{"type": "string"}
		required = []string{"reference"}
	case "act":
		properties["reference"] = map[string]any{"type": "string"}
		properties["operation"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bubble":      map[string]any{"type": "boolean"},
				"auto_submit": map[string]any{"type": "boolean"},
				"submit_ref": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 64,
					"pattern": "^(enter_key|n[1-9][0-9]*)$",
				},
			},
		}
		required = []string{"reference", "operation"}
	case "app":
		properties["kind"] = map[string]any{
			"type": "string",
			"enum": []any{"find", "list", "open", "claim", "handoff", "close", "kill"},
		}
		properties["query"] = map[string]any{"type": "string"}
		properties["reference"] = map[string]any{"type": "string"}
		properties["scope"] = map[string]any{"type": "string"}
		properties["app"] = map[string]any{"type": "string"}
		properties["target"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "integer"},
				"app":     map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		}
		properties["restore_foreground"] = map[string]any{"type": "boolean", "default": false}
		properties["allow_borrowed"] = map[string]any{"type": "boolean", "default": false}
		properties["allow_stop_playback"] = map[string]any{"type": "boolean", "default": false}
		required = []string{"kind"}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": additional,
	}
}

func testTools() []*sdk.Tool {
	tools := make([]*sdk.Tool, 0, len(toolNames))
	for _, name := range toolNames {
		tools = append(tools, &sdk.Tool{Name: name, InputSchema: testSchema(name, false)})
	}
	return tools
}

func TestCheckSchemas(t *testing.T) {
	if failure := checkSchemas(testTools()); failure != nil {
		t.Fatalf("valid schemas rejected: %v", failure.reason)
	}
	t.Run("start properties", func(t *testing.T) {
		tools := testTools()
		tools[0].InputSchema = map[string]any{
			"type": "object", "properties": map[string]any{"x": map[string]any{}},
			"required": []string{}, "additionalProperties": false,
		}
		if failure := checkSchemas(tools); failure == nil || failure.reason != "start_schema_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("additional properties open", func(t *testing.T) {
		tools := testTools()
		tools[1].InputSchema = testSchema("status", true)
		if failure := checkSchemas(tools); failure == nil || failure.reason != "additional_properties_open" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("observe required mismatch", func(t *testing.T) {
		tools := testTools()
		tools[2].InputSchema = map[string]any{
			"type": "object", "properties": map[string]any{},
			"required": []string{}, "additionalProperties": false,
		}
		if failure := checkSchemas(tools); failure == nil || failure.reason != "required_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("act retired press_enter", func(t *testing.T) {
		tools := testTools()
		act := actToolFrom(t, tools)
		schema, operation := actOperationSchema(t, act)
		operation["press_enter"] = map[string]any{"type": "boolean"}
		act.InputSchema = schema
		if failure := checkSchemas(tools); failure == nil || failure.reason != "act_retired_press_enter" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("act submit_ref default", func(t *testing.T) {
		tools := testTools()
		act := actToolFrom(t, tools)
		schema, operation := actOperationSchema(t, act)
		operation["submit_ref"] = map[string]any{
			"type": "string", "minLength": 1, "maxLength": 64,
			"pattern": "^(enter_key|n[1-9][0-9]*)$", "default": "enter_key",
		}
		act.InputSchema = schema
		if failure := checkSchemas(tools); failure == nil || failure.reason != "act_branch_member_default" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("act auto_submit type", func(t *testing.T) {
		tools := testTools()
		act := actToolFrom(t, tools)
		schema, operation := actOperationSchema(t, act)
		operation["auto_submit"] = map[string]any{"type": "string"}
		act.InputSchema = schema
		if failure := checkSchemas(tools); failure == nil || failure.reason != "act_branch_member_missing" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("act submit_ref pattern", func(t *testing.T) {
		tools := testTools()
		act := actToolFrom(t, tools)
		schema, operation := actOperationSchema(t, act)
		operation["submit_ref"] = map[string]any{
			"type": "string", "minLength": 1, "maxLength": 64, "pattern": "^n[0-9]+$",
		}
		act.InputSchema = schema
		if failure := checkSchemas(tools); failure == nil || failure.reason != "act_submit_ref_shape_invalid" {
			t.Fatalf("failure = %v", failure)
		}
	})
}

// ---------------------------------------------------------------- images

func testJPEG(size int) []byte {
	data := make([]byte, size)
	data[0], data[1], data[2] = 0xFF, 0xD8, 0xFF
	for index := 3; index < size; index++ {
		data[index] = byte(index % 251)
	}
	return data
}

func testObservationWithImage() *observationView {
	return &observationView{
		Status: observationComplete,
		ID:     "O4821_85",
		Tree:   &treeView{Format: treeFull},
		Image: &imageView{
			CapturedAt: "2026-01-01T00:00:00Z",
			Width:      461,
			Height:     1024,
			MediaType:  imageJPEG,
		},
	}
}

func TestImageChecks(t *testing.T) {
	raw := testJPEG(4096)
	projected := &callResult{
		result: &sdk.CallToolResult{},
		texts:  []string{"observation.status=complete\n"},
		images: []*sdk.ImageContent{{MIMEType: imageJPEG, Data: raw}},
	}
	decoded, failure := imageBytes(projected, testObservationWithImage())
	if failure != nil {
		t.Fatalf("imageBytes: %v", failure.reason)
	}
	if len(decoded) != len(raw) {
		t.Fatalf("decoded length = %d", len(decoded))
	}
	if failure := checkNoImageCarrier(projected, raw); failure != nil {
		t.Fatalf("clean result rejected: %v", failure.reason)
	}

	t.Run("encoded carrier", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString(raw)
		projected := &callResult{
			result: &sdk.CallToolResult{},
			images: []*sdk.ImageContent{{MIMEType: imageJPEG, Data: []byte(encoded)}},
		}
		if _, failure := imageBytes(projected, testObservationWithImage()); failure != nil {
			t.Fatalf("encoded carrier rejected: %v", failure.reason)
		}
	})
	t.Run("too small", func(t *testing.T) {
		projected := &callResult{
			result: &sdk.CallToolResult{},
			images: []*sdk.ImageContent{{MIMEType: imageJPEG, Data: testJPEG(100)}},
		}
		if _, failure := imageBytes(projected, testObservationWithImage()); failure == nil || failure.reason != "image_too_small" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("text leak", func(t *testing.T) {
		leaky := &callResult{
			result: &sdk.CallToolResult{},
			texts:  []string{"observation.image.base64=" + base64.StdEncoding.EncodeToString(raw)},
		}
		if failure := checkNoImageCarrier(leaky, raw); failure == nil || failure.reason != "text_carries_base64" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("structured payload leak", func(t *testing.T) {
		leaky := &callResult{
			result:     &sdk.CallToolResult{},
			structured: []byte(`{"observation":{"image":{"data":"` + base64.StdEncoding.EncodeToString(raw) + `"}}}`),
		}
		if failure := checkNoImageCarrier(leaky, raw); failure == nil || failure.reason != "structured_carries_image_bytes" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("461x1024 observation image accepted", func(t *testing.T) {
		observation := testObservationWithImage()
		if failure := checkImageLongSide(observation.Image); failure != nil {
			t.Fatalf("461x1024 rejected: %s", failure.reason)
		}
	})
	t.Run("retired 1200 long side rejected", func(t *testing.T) {
		observation := testObservationWithImage()
		observation.Image.Width = 540
		observation.Image.Height = 1200
		if failure := checkImageLongSide(observation.Image); failure == nil ||
			failure.reason != "image_long_side_exceeds_limit" {
			t.Fatalf("540x1200 = %v", failure)
		}
	})
	t.Run("snap scaling 1080x2400 to 461x1024", func(t *testing.T) {
		width, height := expectedImageSize(1080, 2400, imageMaxLongSide)
		if width != 461 || height != 1024 {
			t.Fatalf("expectedImageSize = %dx%d, want 461x1024", width, height)
		}
		width, height = expectedImageSize(2400, 1080, imageMaxLongSide)
		if width != 1024 || height != 461 {
			t.Fatalf("landscape expectedImageSize = %dx%d, want 1024x461", width, height)
		}
		width, height = expectedImageSize(800, 600, imageMaxLongSide)
		if width != 800 || height != 600 {
			t.Fatalf("small image upscaled to %dx%d", width, height)
		}
	})
}

func TestValidReference(t *testing.T) {
	cases := []struct {
		value  string
		prefix byte
		want   bool
	}{
		{"P7391_12", 'P', true},
		{"O4821_85", 'O', true},
		{"P0123456789abcdef01234567", 'P', true},
		{"O1", 'O', true},
		{"P1", 'O', false},
		{"X1", 'P', false},
		{"P", 'P', false},
		{"Pbad!", 'P', false},
		{"P1234567890123456789012345678901234567890123456789012345678901234", 'P', false},
	}
	for _, testCase := range cases {
		if actual := validReference(testCase.value, testCase.prefix); actual != testCase.want {
			t.Fatalf("validReference(%q, %q) = %v, want %v", testCase.value, string(testCase.prefix), actual, testCase.want)
		}
	}
}

// ---------------------------------------------------------------- negative HTTP

func TestNegativeHTTPChecks(t *testing.T) {
	const mcpToken = "mcp-token"
	const allowedOrigin = "http://127.0.0.1:3000"
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+mcpToken {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" && origin != allowedOrigin {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		writer.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := &config{}
	cfg.Service.AgentToken = "agent-token"
	cfg.HTTP.Listen = strings.TrimPrefix(server.URL, "http://")
	cfg.HTTP.Token = mcpToken
	cfg.HTTP.AllowedOrigins = []string{allowedOrigin}

	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !runNegativeHTTPChecks(ctx, cfg, reporter) {
		t.Fatalf("negative checks failed:\n%s", output.String())
	}
	for _, want := range []string{
		"PASS auth_missing status=401",
		"PASS origin_denied status=403",
		"PASS origin_allowed status=200",
		"PASS agent_token_rejected status=401",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, output.String())
		}
	}

	leaky := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		if header != "Bearer "+mcpToken && header != "Bearer agent-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" && origin != allowedOrigin {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(leaky.Close)
	cfg.HTTP.Listen = strings.TrimPrefix(leaky.URL, "http://")
	var leakOutput bytes.Buffer
	if runNegativeHTTPChecks(ctx, cfg, newReporter(&leakOutput)) {
		t.Fatalf("agent token leak was accepted:\n%s", leakOutput.String())
	}
	if !strings.Contains(leakOutput.String(), "FAIL agent_token_rejected") {
		t.Fatalf("missing agent token failure:\n%s", leakOutput.String())
	}
}

// ---------------------------------------------------------------- fake MCP adapter

type fakeAdapter struct {
	mutex            sync.Mutex
	canStart         bool
	counter          int
	sessions         map[string]bool
	image            []byte
	leakImage        bool
	startCalls       int
	endCalls         int
	resultMode       string
	primaryCandidate bool
	legacyInspect    bool
	omitInspectBlock bool
	// appTaskID is the one live controlled task; a kill keeps the task window
	// (the contract no longer removes it), while close removes it, so a later
	// open allocates the next id and the app-lifecycle loop can close a reopened
	// task after a kill that reclaimed the window.
	appTaskID       int
	appNextTaskID   int
	killRefusalCode string
	// closeEligible models the five-minute close eligibility of the controlled
	// package: one confirmed close allows one package-entry kill.
	closeEligible bool
	// killFactFault is a reverse control that corrupts one dispatched kill
	// result so the matching probe check must fail.
	killFactFault string
	// killUncertain reports one dispatched but unconfirmed force-stop with an
	// unknown final read, exercising the uncertain kill branch.
	killUncertain bool
	// visual is the optional lightweight classification every fake observation
	// carries. Empty keeps the member absent, which is the common shape.
	visual string
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		canStart:      true,
		sessions:      make(map[string]bool),
		image:         testJPEG(4096),
		resultMode:    resultModeBoth,
		appNextTaskID: 41,
	}
}

func (f *fakeAdapter) nextID(prefix string) string {
	f.counter++
	return fmt.Sprintf("%s7391_%d", prefix, f.counter)
}

func (f *fakeAdapter) observation(id, origin string, revision int, withImage bool) map[string]any {
	observation := map[string]any{
		"status":   observationComplete,
		"id":       id,
		"origin":   origin,
		"revision": revision,
		"quality": map[string]any{
			"state": "stable", "samples": 2, "stop_reason": "stable",
		},
		"tree": map[string]any{
			"format": treeFull, "scope": "reported_visible_with_context",
			"view": protocol.ViewCompact,
			// The complete tree body is what the live service sends, so the
			// both-mode body comparison runs against the whole fake suite.
			"root": map[string]any{
				"ref": "n1", "role": "root",
				"bounds": []any{0, 0, 1080, 2400},
				"label":  []any{}, "actions": []any{}, "children": []any{},
				"enabled": true, "focused": false, "selected": false,
				"protected": false, "checked": false,
			},
		},
	}
	if withImage {
		observation["image"] = map[string]any{
			"captured_at": "2026-01-01T00:00:00Z",
			"width":       461,
			"height":      1024,
			"media_type":  imageJPEG,
		}
	}
	if f.visual != "" {
		observation["visual"] = f.visual
	}
	return observation
}

func (f *fakeAdapter) result(payload map[string]any, text string) *sdk.CallToolResult {
	_ = text
	return f.render(payload, false, false)
}

func (f *fakeAdapter) imageResult(payload map[string]any) *sdk.CallToolResult {
	return f.render(payload, false, true)
}

// snapResult is the top-level image success shape: one P, image metadata and
// exactly one ImageContent.
func (f *fakeAdapter) snapResult(operationID string) *sdk.CallToolResult {
	return f.render(map[string]any{
		"operation_id": operationID,
		"image": map[string]any{
			"captured_at": "2026-01-01T00:00:00Z",
			"width":       461, "height": 1024,
			"media_type": imageJPEG, "read_only": true,
			"screen": map[string]any{"width": 1080, "height": 2400, "rotation": 0},
		},
	}, false, true)
}

func (f *fakeAdapter) errorResult(payload map[string]any) *sdk.CallToolResult {
	return f.render(payload, true, false)
}

func (f *fakeAdapter) render(payload map[string]any, isError, withImage bool) *sdk.CallToolResult {
	textPayload := payload
	if withImage && f.leakImage && (f.resultMode == resultModeBoth || f.resultMode == resultModeText) {
		textPayload = cloneStringMap(payload)
		observation, _ := textPayload["observation"].(map[string]any)
		if observation != nil {
			observation = cloneStringMap(observation)
			image, _ := observation["image"].(map[string]any)
			if image == nil {
				image = make(map[string]any)
			} else {
				image = cloneStringMap(image)
			}
			image["base64"] = base64.StdEncoding.EncodeToString(f.image)
			observation["image"] = image
			textPayload["observation"] = observation
		}
	}
	text := fakeResultText(textPayload, isError)
	if f.legacyInspect {
		text = strings.ReplaceAll(text, "inspect v3 ", "inspect v2 ")
	}
	if f.omitInspectBlock {
		text = omitFakeInspectBlock(text, payload)
	}
	content := make([]sdk.Content, 0, 2)
	switch f.resultMode {
	case resultModeText:
		content = append(content, &sdk.TextContent{Text: text})
	case resultModeStructured:
		if isError {
			content = append(content, &sdk.TextContent{Text: fakeDiagnosticText(payload)})
		}
	default:
		content = append(content, &sdk.TextContent{Text: text})
	}
	if withImage {
		content = append(content, &sdk.ImageContent{Data: f.image, MIMEType: imageJPEG})
	}
	result := &sdk.CallToolResult{Content: content, IsError: isError}
	if f.resultMode == resultModeBoth || f.resultMode == resultModeStructured {
		result.StructuredContent = payload
	}
	return result
}

func (f *fakeAdapter) handle(ctx context.Context, name string, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	arguments := map[string]any{}
	if request != nil && request.Params != nil && len(request.Params.Arguments) > 0 {
		_ = json.Unmarshal(request.Params.Arguments, &arguments)
	}
	switch name {
	case "status":
		return f.result(map[string]any{"can_start": f.canStart}, "can_start=true\n"), nil
	case "start":
		f.startCalls++
		p := f.nextID("P")
		o := f.nextID("O")
		f.sessions[p] = true
		return f.result(map[string]any{
			"operation_id": p,
			"outcome":      outcomeExecuted,
			"session":      map[string]any{"origin": p, "state": sessionActive, "mode": "virtual"},
			"observation":  f.observation(o, p, 1, false),
		}, "operation_id="+p+"\n"), nil
	case "observe":
		reference, _ := arguments["reference"].(string)
		if !validReference(reference, 'P') && !validReference(reference, 'O') {
			return f.errorResult(map[string]any{
				"error": map[string]any{"code": "invalid_request"},
			}), nil
		}
		if wait, ok := arguments["wait_ms"].(float64); ok && wait > 0 {
			f.mutex.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
			f.mutex.Lock()
			return f.render(map[string]any{
				"operation_id": f.nextID("P"),
				"error":        map[string]any{"code": "call_cancelled"},
			}, true, false), nil
		}
		o := f.nextID("O")
		return f.imageResult(map[string]any{
			"operation_id": f.nextID("P"),
			"observation":  f.observation(o, reference, 2, true),
		}), nil
	case "snap":
		reference, _ := arguments["reference"].(string)
		if !validReference(reference, 'P') && !validReference(reference, 'O') {
			return f.errorResult(map[string]any{
				"error": map[string]any{"code": "invalid_request"},
			}), nil
		}
		return f.snapResult(f.nextID("P")), nil
	case "wait_for":
		reference, _ := arguments["reference"].(string)
		if !validReference(reference, 'P') && !validReference(reference, 'O') {
			return f.errorResult(map[string]any{
				"error": map[string]any{"code": "invalid_request"},
			}), nil
		}
		o := f.nextID("O")
		observation := f.observation(o, reference, 2, false)
		// A pure-sleep wait samples nothing, so its terminal frame is the
		// explicit not_sampled fact rather than a converged pair.
		observation["quality"] = map[string]any{
			"state": qualityUnknown, "samples": 0, "stop_reason": qualityStopNotSampled,
		}
		return f.render(map[string]any{
			"operation_id": f.nextID("P"),
			"observation":  observation,
			"wait": map[string]any{
				"trigger": waitTriggerTimeout, "matched": []any{waitTriggerTimeout},
				"elapsed_ms": 1000,
			},
		}, false, false), nil
	case "act":
		o := f.nextID("O")
		payload := map[string]any{
			"operation_id": f.nextID("P"),
			"outcome":      outcomeExecuted,
			"observation":  f.observation(o, "P1", 3, true),
		}
		if operation, ok := arguments["operation"].(map[string]any); ok {
			if actionType, _ := operation["type"].(string); actionType == "scroll" {
				payload["effect"] = map[string]any{"state": "observed"}
			}
		}
		return f.imageResult(payload), nil
	case "inspect":
		observationID, _ := arguments["reference"].(string)
		if observationID == probeUnknownObservation {
			return f.errorResult(map[string]any{
				"error": map[string]any{"code": "observation_not_found"},
			}), nil
		}
		text := "inspection.observation_id=" + observationID +
			"\n\ninspect v3 scope=tree observation=" + observationID +
			" revision=2 captured_at=2026-01-01T00:00:00Z" +
			" tree_scope=reported_visible_with_context view=detailed nodes=1 entries=1\n" +
			"\n" +
			"n1 root [0,0,1080,2400]\nend inspect\n"
		return f.result(map[string]any{
			"inspection": map[string]any{
				"observation_id": observationID,
				"origin":         "P1",
				"revision":       2,
				"captured_at":    "2026-01-01T00:00:00Z",
				"scope":          "tree",
				"tree": map[string]any{
					"format": treeFull,
					"scope":  "reported_visible_with_context",
					"view":   protocol.ViewDetailed,
					"root": map[string]any{
						"ref": "n1", "role": "root", "bounds": []int{0, 0, 1080, 2400},
					},
				},
			},
		}, text), nil
	case "app":
		kind, _ := arguments["kind"].(string)
		if kind == "find" {
			// Contract 31: find is read-only and ignores the declared members it
			// does not use, including a redundant reference.
			query, _ := arguments["query"].(string)
			items := []any{map[string]any{"name": "Probe App", "package": "com.example.probe"}}
			total := 1
			if query == probeEmptyQuery {
				items = []any{}
				total = 0
			}
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"app": map[string]any{
					"kind": "find", "items": items, "total": total,
					"truncated": false, "captured_at": "2026-01-01T00:00:00Z",
				},
			}, ""), nil
		}
		displayID := 12
		scope, _ := arguments["scope"].(string)
		if scope == "primary" {
			displayID = 0
		}
		filterPackage, _ := arguments["app"].(string)
		if scope == "primary" && filterPackage != "" {
			// The controlled app-lifecycle claim step only claims a task it can
			// prove is the controlled target.
			items := []any{}
			total := 0
			if f.primaryCandidate {
				items = []any{map[string]any{
					"task_id": 77, "package": controlledAppPackage, "ownership": "shared",
				}}
				total = 1
			}
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"app": map[string]any{
					"kind": "list", "display_id": 0,
					"items": items, "total": total, "truncated": false,
					"captured_at": "2026-01-01T00:00:00Z",
					"foreground":  map[string]any{"state": "unknown", "reason": "foreground_unavailable"},
				},
			}, ""), nil
		}
		targetTaskID := 0
		targetApp := ""
		if targetObject, ok := arguments["target"].(map[string]any); ok {
			if value, ok := targetObject["task_id"].(float64); ok {
				targetTaskID = int(value)
			}
			targetApp, _ = targetObject["app"].(string)
		}
		if kind == "open" {
			if f.appTaskID == 0 {
				f.appNextTaskID++
				f.appTaskID = f.appNextTaskID
			}
			packageName := controlledAppPackage
			if targetApp != "" {
				packageName = targetApp
			}
			o := f.nextID("O")
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      outcomeExecuted,
				"app": map[string]any{
					"kind": "open", "package": packageName, "task_id": f.appTaskID,
					"display_id": 12, "ownership": "owned",
					"reused": false, "state_preserved": false,
				},
				"observation": f.observation(o, "P1", 2, false),
			}, ""), nil
		}
		if kind == "kill" {
			packageKill := targetApp != "" && targetTaskID == 0
			sourceTaskID := targetTaskID
			if sourceTaskID == 0 {
				sourceTaskID = f.appTaskID
			}
			refusal := f.killRefusalCode
			if refusal == "" && f.primaryCandidate {
				// A same-package task on the primary display is not owned by this
				// virtual session, so both entries must refuse it.
				refusal = "kill_refused_task_elsewhere"
			}
			if refusal == "" && packageKill && f.appTaskID == 0 && !f.closeEligible {
				refusal = "kill_refused_not_owned"
			}
			if refusal != "" {
				refused := map[string]any{
					"kind": "kill", "package": controlledAppPackage,
					"android_user_id": 0, "display_id": 12,
					"target_task_ids": f.livePackageTaskIDs(),
				}
				if !packageKill {
					refused["source_task_id"] = sourceTaskID
				}
				return f.errorResult(map[string]any{
					"operation_id": f.nextID("P"),
					"outcome":      "not_executed",
					"app":          refused,
					"error":        map[string]any{"code": refusal},
				}), nil
			}
			if f.killUncertain {
				uncertain := map[string]any{
					"kind": "kill", "package": controlledAppPackage,
					"android_user_id": 0, "display_id": 12,
					"target_task_ids": f.livePackageTaskIDs(),
					"target_pids":     []int{101}, "restarted": nil,
					"package_state": "unknown",
				}
				if !packageKill {
					uncertain["source_task_id"] = sourceTaskID
				}
				return f.errorResult(map[string]any{
					"operation_id": f.nextID("P"),
					"outcome":      "uncertain",
					"app":          uncertain,
					"error":        map[string]any{"code": "kill_unconfirmed"},
				}), nil
			}
			// A package force-stop never removes the task window, so the live
			// task stays listed until the caller closes it.
			payload := map[string]any{
				"kind": "kill", "package": controlledAppPackage,
				"android_user_id": 0, "display_id": 12,
				"target_task_ids":    f.livePackageTaskIDs(),
				"remaining_task_ids": f.livePackageTaskIDs(),
				"target_pids":        []int{101}, "killed_pids": []int{101},
				"remaining_pids": []int{}, "restarted": false,
				"package_state": "still_present",
			}
			if packageKill {
				// The close that established the eligibility also removed the
				// window: both the task and the process sets are empty.
				payload["remaining_task_ids"] = []int{}
				payload["package_state"] = "stopped_at_check"
				f.closeEligible = false
			} else {
				payload["source_task_id"] = sourceTaskID
			}
			f.applyKillFactFault(payload)
			o := f.nextID("O")
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      outcomeExecuted,
				"app":          payload,
				"observation":  f.observation(o, "P1", 2, false),
			}, ""), nil
		}
		if kind == "claim" {
			o := f.nextID("O")
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      outcomeExecuted,
				"app": map[string]any{
					"kind": "claim", "package": controlledAppPackage, "task_id": 77,
					"source_display_id": 0, "display_id": 12,
					"ownership": "borrowed", "state_preserved": true,
				},
				"observation": f.observation(o, "P1", 2, false),
			}, ""), nil
		}
		if kind == "close" {
			closedTaskID := targetTaskID
			if closedTaskID == 0 {
				closedTaskID = f.appTaskID
			}
			f.appTaskID = 0
			// A confirmed close of the owned controlled task establishes the
			// five-minute close eligibility for the package entry.
			f.closeEligible = true
			o := f.nextID("O")
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      outcomeExecuted,
				"app": map[string]any{
					"kind": "close", "display_id": 12,
					"tasks": []any{map[string]any{
						"task_id": closedTaskID, "outcome": outcomeExecuted, "recents_cleaned": true,
					}},
					"complete": true,
				},
				"observation": f.observation(o, "P1", 2, false),
			}, ""), nil
		}
		items := []any{}
		total := 0
		if f.appTaskID != 0 && scope != "primary" &&
			(filterPackage == "" || filterPackage == controlledAppPackage) {
			packageName := controlledAppPackage
			if filterPackage != "" {
				packageName = filterPackage
			}
			items = []any{map[string]any{
				"task_id": f.appTaskID, "package": packageName, "ownership": "owned",
			}}
			total = 1
		}
		return f.result(map[string]any{
			"operation_id": f.nextID("P"),
			"app": map[string]any{
				"kind": "list", "display_id": displayID,
				"items": items,
				"total": total, "truncated": false, "captured_at": "2026-01-01T00:00:00Z",
				"foreground": map[string]any{"state": "unknown", "reason": "foreground_unavailable"},
			},
		}, ""), nil
	case "shell":
		return f.result(map[string]any{
			"operation_id": f.nextID("P"),
			"outcome":      outcomeExecuted,
			"shell": map[string]any{
				"termination": "exited",
				"exit_code":   0,
				"output":      probeShellOutput + "\n",
				"truncated":   false,
			},
		}, ""), nil
	case "diagnose":
		return f.result(map[string]any{
			"diagnosis": map[string]any{
				"summary": "probe diagnosis",
				"issues":  []any{},
				"error_counts": map[string]any{
					"no_safe_point": 2, "uncertain": 1,
				},
				"timeline": []any{
					map[string]any{
						"at": "2026-01-01T00:00:00Z", "phase": "session", "code": sessionActive,
					},
				},
				"coverage": "original_record", "truncated": false,
			},
		}, ""), nil
	case "end":
		f.endCalls++
		reference, _ := arguments["reference"].(string)
		return f.result(map[string]any{
			"operation_id":     f.nextID("P"),
			"outcome":          outcomeExecuted,
			"target_reference": reference,
			"session":          map[string]any{"origin": reference, "state": sessionEnded, "mode": "virtual"},
			"cleanup":          map[string]any{"state": "complete"},
		}, "outcome=executed\n"), nil
	default:
		return f.result(map[string]any{}, ""), nil
	}
}

func cloneStringMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// livePackageTaskIDs is the controlled package's live virtual task set as the
// fake engine would freeze it for a package kill.
func (f *fakeAdapter) livePackageTaskIDs() []int {
	if f.appTaskID == 0 {
		return []int{}
	}
	return []int{f.appTaskID}
}

// applyKillFactFault corrupts one dispatched kill result so a reverse control
// can prove the matching probe check fails instead of passing silently.
func (f *fakeAdapter) applyKillFactFault(payload map[string]any) {
	switch f.killFactFault {
	case "missing_package_state":
		delete(payload, "package_state")
	case "stopped_with_tasks":
		payload["package_state"] = "stopped_at_check"
		payload["remaining_task_ids"] = []int{42}
	}
}

func fakeDiagnosticText(payload map[string]any) string {
	code := "internal"
	if value, ok := payload["error"].(map[string]any); ok {
		if text, ok := value["code"].(string); ok && text != "" {
			code = text
		}
	}
	return "diagnostic v1 is_error=true\nerror.code=\"" + code + "\"\nend diagnostic\n"
}

// fakeResultText emits one result v4 projection from the same payload object a
// fake adapter publishes as structuredContent. It mirrors the documented fixed
// order and the explicit omission rules so mode comparisons exercise the real
// decoder contract instead of a second ad-hoc shape.
func fakeResultText(payload map[string]any, isError bool) string {
	var builder strings.Builder
	builder.WriteString("result v4 ")
	if isError {
		builder.WriteString("err")
	} else {
		builder.WriteString("ok")
	}
	if operationID, ok := payload["operation_id"].(string); ok && operationID != "" {
		builder.WriteString(" op=" + operationID)
	}
	state, _ := payload["operation_state"].(string)
	if state != "" {
		builder.WriteString(" state=" + state)
	}
	outcome, _ := payload["outcome"].(string)
	omitOutcome := !isError && outcome == outcomeExecuted && state == ""
	if outcome != "" && !omitOutcome {
		builder.WriteString(" outcome=" + outcome)
	}
	if phase, ok := payload["phase"].(string); ok && phase != "" {
		builder.WriteString(" phase=" + phase)
	}
	builder.WriteByte('\n')

	if input, ok := payload["text_input"].(map[string]any); ok {
		builder.WriteString("text_input write=" + input["write"].(string) +
			" submit=" + input["submit"].(string) + "\n")
	}
	// The control scalars keep the documented encoder order; the explicit
	// zero-dispatch recovery marker closes the section.
	for _, member := range []struct{ key, label string }{
		{"target_reference", "target_reference"},
		{"request_closed", "request_closed"},
		{"blocking_operation_id", "blocking_operation_id"},
		{"can_start", "can_start"},
		{"blocking_code", "blocking_code"},
		{"start_operation_id", "start_operation_id"},
		{"observation_state", "observation_state"},
		{"input_state", "input_state"},
	} {
		switch member.key {
		case "request_closed", "can_start":
			if value, ok := payload[member.key].(bool); ok {
				builder.WriteString(member.label + "=" + strconv.FormatBool(value) + "\n")
			}
		default:
			if value, ok := payload[member.key].(string); ok && value != "" {
				builder.WriteString(member.label + "=" + value + "\n")
			}
		}
	}
	if recovery, ok := payload["auto_recovery"].(map[string]any); ok {
		writeFakeAutoRecovery(&builder, recovery)
	}
	writeFakeNotify(&builder, payload)
	if image, ok := payload["image"].(map[string]any); ok {
		writeFakeImageLine(&builder, "image", image)
	}
	if value, present := payload["session"]; present {
		if value == nil {
			builder.WriteString("session=null\n")
		} else if session, ok := value.(map[string]any); ok {
			writeFakeSession(&builder, session)
		}
	}
	if observation, ok := payload["observation"].(map[string]any); ok {
		writeFakeObservation(&builder, observation, payload["session"] != nil)
	}
	if inspection, ok := payload["inspection"].(map[string]any); ok {
		writeFakeInspection(&builder, inspection)
	}
	if app, ok := payload["app"].(map[string]any); ok {
		writeFakeApp(&builder, app)
	}
	if shell, ok := payload["shell"].(map[string]any); ok {
		writeFakeShell(&builder, shell)
	}
	if wait, ok := payload["wait"].(map[string]any); ok {
		writeFakeWait(&builder, wait)
	}
	if diagnosis, ok := payload["diagnosis"].(map[string]any); ok {
		writeFakeDiagnosis(&builder, diagnosis)
	}
	if cleanup, ok := payload["cleanup"].(map[string]any); ok {
		writeFakeCleanup(&builder, cleanup)
	}
	if errorObject, ok := payload["error"].(map[string]any); ok {
		writeFakeError(&builder, "error", errorObject)
	}
	if recovery, ok := payload["recovery"].(map[string]any); ok {
		builder.WriteString("recovery entry=" + recovery["entry"].(string) +
			" ref=" + recovery["reference"].(string))
		if value, ok := recovery["input_state"].(string); ok && value != "" {
			builder.WriteString(" input=" + value)
		}
		if value, ok := recovery["cause"].(string); ok && value != "" {
			builder.WriteString(" cause=" + value)
		}
		builder.WriteByte('\n')
	}
	// Any member the fake emitter does not classify travels through the same
	// json v1 fallback a future encoder must use.
	known := map[string]bool{
		"operation_id": true, "operation_state": true, "outcome": true, "phase": true,
		"effect": true, "text_input": true, "auto_recovery": true, "target_reference": true,
		"request_closed": true, "blocking_operation_id": true, "can_start": true,
		"blocking_code": true, "start_operation_id": true, "observation_state": true,
		"input_state": true, "message_id": true, "saved": true, "image": true,
		"session": true, "observation": true, "inspection": true, "app": true,
		"shell": true, "wait": true, "notification": true, "diagnosis": true,
		"cleanup": true, "error": true, "recovery": true,
	}
	for key, value := range payload {
		if known[key] {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			encoded = []byte("null")
		}
		builder.WriteString("json v1 path=" + strconv.Quote("/"+key) + " value=" + string(encoded) + "\n")
	}
	builder.WriteString("end result\n")
	return builder.String()
}

// writeFakeAutoRecovery renders the fixed zero-dispatch recovery line from the
// same payload object the structured channel publishes, so both-mode comparisons
// exercise the documented projection instead of a second ad-hoc shape.
func writeFakeAutoRecovery(builder *strings.Builder, recovery map[string]any) {
	builder.WriteString("auto_recovery")
	if strategy, ok := recovery["strategy"].(string); ok && strategy != "" {
		builder.WriteString(" strategy=" + strategy)
	}
	for _, key := range []string{"original_ref", "recovered_ref"} {
		if value, ok := recovery[key].(string); ok && value != "" {
			builder.WriteString(" " + key + "=" + value)
		}
	}
	switch attempts := recovery["attempts"].(type) {
	case int:
		builder.WriteString(" attempts=" + strconv.Itoa(attempts))
	case float64:
		builder.WriteString(" attempts=" + strconv.Itoa(int(attempts)))
	}
	builder.WriteByte('\n')
}

func writeFakeNotify(builder *strings.Builder, payload map[string]any) {
	messageID, hasMessage := payload["message_id"].(string)
	saved, hasSaved := payload["saved"].(bool)
	var notification map[string]any
	if value, ok := payload["notification"].(map[string]any); ok {
		notification = value
	}
	if !hasMessage && !hasSaved && notification == nil {
		return
	}
	builder.WriteString("notify")
	if hasMessage && messageID != "" {
		builder.WriteString(" id=" + messageID)
	}
	if hasSaved {
		builder.WriteString(" saved=" + strconv.FormatBool(saved))
	}
	if state, ok := notification["state"].(string); ok && state != "" {
		builder.WriteString(" state=" + state)
	}
	builder.WriteByte('\n')
	if errorObject, ok := notification["error"].(map[string]any); ok {
		writeFakeError(builder, "notify.error", errorObject)
	}
}

// writeFakeImageLine renders the shared image metadata line. The media type is
// omitted for JPEG; the geometry, read_only flag, screen and capture time are
// only written when the corresponding structured member exists.
func writeFakeImageLine(builder *strings.Builder, prefix string, image map[string]any) {
	builder.WriteString(prefix)
	if width, ok := image["width"].(int); ok && width > 0 {
		if height, ok := image["height"].(int); ok && height > 0 {
			builder.WriteString(" " + strconv.Itoa(width) + "x" + strconv.Itoa(height))
		}
	}
	if readOnly, ok := image["read_only"].(bool); ok && readOnly {
		builder.WriteString(" read_only")
	}
	if media, ok := image["media_type"].(string); ok && media != "" && media != imageJPEG {
		builder.WriteString(" media=" + media)
	}
	screen, _ := image["screen"].(map[string]any)
	width, _ := screen["width"].(int)
	height, _ := screen["height"].(int)
	if width > 0 && height > 0 {
		builder.WriteString(" screen=" + strconv.Itoa(width) + "x" + strconv.Itoa(height))
		if display, ok := screen["display_id"].(int); ok {
			builder.WriteString("@" + strconv.Itoa(display))
		}
	}
	if rotation, ok := screen["rotation"].(int); ok && rotation != 0 {
		builder.WriteString(" rot=" + strconv.Itoa(rotation))
	}
	if capturedAt, ok := image["captured_at"].(string); ok && capturedAt != "" {
		builder.WriteString(" at=" + fakeClock(capturedAt))
	}
	builder.WriteByte('\n')
	if errorObject, ok := image["error"].(map[string]any); ok {
		writeFakeError(builder, prefix+".error", errorObject)
	}
	if base64, ok := image["base64"].(string); ok && base64 != "" {
		builder.WriteString("json v1 path=" + strconv.Quote("/"+strings.ReplaceAll(prefix, ".", "/")+"/base64") +
			" value=" + strconv.Quote(base64) + "\n")
	}
}

func writeFakeSession(builder *strings.Builder, session map[string]any) {
	state, _ := session["state"].(string)
	mode, _ := session["mode"].(string)
	origin, _ := session["origin"].(string)
	builder.WriteString("session " + state + " mode=" + mode + " origin=" + origin)
	for _, field := range []struct{ key, label string }{
		{"started_at", "started"}, {"expires_at", "expires"},
		{"ended_at", "ended"}, {"reason", "reason"},
	} {
		value, _ := session[field.key].(string)
		if value == "" {
			continue
		}
		if field.key == "reason" {
			builder.WriteString(" reason=" + value)
		} else {
			builder.WriteString(" " + field.label + "=" + fakeClock(value))
		}
	}
	builder.WriteByte('\n')
}

func writeFakeObservation(builder *strings.Builder, observation map[string]any, sessionPresent bool) {
	builder.WriteString("obs")
	if id, ok := observation["id"].(string); ok && id != "" {
		builder.WriteString(" " + id)
	}
	status, _ := observation["status"].(string)
	if status != "" && status != observationComplete {
		builder.WriteString(" status=" + status)
	}
	if foreground, ok := observation["foreground"].(map[string]any); ok {
		builder.WriteString(" fg=" + fakeForegroundToken(foreground))
	}
	if capturedAt, ok := observation["captured_at"].(string); ok && capturedAt != "" {
		builder.WriteString(" at=" + fakeClock(capturedAt))
	}
	if visual, ok := observation["visual"].(string); ok && visual != "" {
		builder.WriteString(" visual=" + visual)
	}
	tree, hasTree := observation["tree"].(map[string]any)
	screen, hasScreen := observation["screen"].(map[string]any)
	if hasScreen && (sessionPresent || !hasTree) {
		width, _ := screen["width"].(int)
		height, _ := screen["height"].(int)
		if width > 0 && height > 0 {
			builder.WriteString(" screen=" + strconv.Itoa(width) + "x" + strconv.Itoa(height))
			if display, ok := screen["display_id"].(int); ok {
				builder.WriteString("@" + strconv.Itoa(display))
			}
		}
	}
	if hasScreen {
		if rotation, ok := screen["rotation"].(int); ok && rotation != 0 {
			builder.WriteString(" rot=" + strconv.Itoa(rotation))
		}
	}
	if missing, ok := observation["missing"].([]any); ok && len(missing) > 0 {
		parts := make([]string, 0, len(missing))
		for _, item := range missing {
			parts = append(parts, item.(string))
		}
		builder.WriteString(" missing=" + strings.Join(parts, ","))
	}
	builder.WriteByte('\n')
	if image, ok := observation["image"].(map[string]any); ok {
		writeFakeImageLine(builder, "obs.image", image)
	}
	if errorObject, ok := observation["error"].(map[string]any); ok {
		writeFakeError(builder, "obs.error", errorObject)
	}
	if !hasTree {
		for _, key := range []string{"origin", "revision", "actionable_until", "screen", "foreground", "quality", "status", "id", "captured_at", "missing", "image", "error"} {
			_ = key
		}
		return
	}
	format, _ := tree["format"].(string)
	base, _ := tree["base"].(string)
	block := ""
	if format == treeDelta {
		block = "tree v5 delta base=" + base + " changes=0\n\n"
	} else {
		block = "tree v5 full nodes=1\n\nn1 root 0,0,1080,2400\n"
	}
	block += "end tree\n"
	writeFakeBlock(builder, block)
}

func fakeAppToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character <= ' ' || character >= 127 || character == '=' ||
			character == '"' || character == '\\' {
			return false
		}
	}
	return true
}

func fakeForegroundToken(foreground map[string]any) string {
	state, _ := foreground["state"].(string)
	switch state {
	case "none":
		return "none"
	case "unknown":
		reason, _ := foreground["reason"].(string)
		if reason != "" && fakeAppToken(reason) {
			return "unknown(" + reason + ")"
		}
		return "unknown"
	case "present":
		pkg, _ := foreground["package"].(string)
		taskID, hasTask := foreground["task_id"].(int)
		switch {
		case pkg != "" && hasTask:
			return pkg + "#" + strconv.Itoa(taskID)
		case pkg != "":
			return pkg
		case hasTask:
			return "#" + strconv.Itoa(taskID)
		default:
			return "present"
		}
	default:
		return "unknown"
	}
}

func writeFakeField(builder *strings.Builder, key string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(`null`)
	}
	builder.WriteString(key)
	builder.WriteByte('=')
	builder.Write(encoded)
	builder.WriteByte('\n')
}

func writeFakeBlock(builder *strings.Builder, block string) {
	builder.WriteByte('\n')
	builder.WriteString(strings.TrimSuffix(block, "\n"))
	builder.WriteByte('\n')
}

func writeFakeInspection(builder *strings.Builder, value any) {
	inspection, _ := value.(map[string]any)
	if inspection == nil {
		writeFakeField(builder, "inspection", value)
		return
	}
	observationID, _ := inspection["observation_id"].(string)
	scope, _ := inspection["scope"].(string)
	revision, _ := inspection["revision"].(int)
	capturedAt, _ := inspection["captured_at"].(string)
	block := "inspect v3 scope=" + scope +
		" observation=" + observationID +
		" revision=" + strconv.Itoa(revision) +
		" captured_at=" + capturedAt +
		" tree_scope=reported_visible_with_context view=detailed" +
		" nodes=1 entries=1\n" +
		"\nn1 root [0,0,1080,2400]\nend inspect\n"
	writeFakeBlock(builder, block)
}

func omitFakeInspectBlock(text string, payload map[string]any) string {
	inspection, _ := payload["inspection"].(map[string]any)
	if inspection == nil {
		return text
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		if strings.HasPrefix(line, "inspect v3 ") {
			if len(filtered) > 0 && filtered[len(filtered)-1] == "" {
				filtered = filtered[:len(filtered)-1]
			}
			skipping = true
			continue
		}
		if skipping {
			if line == "end inspect" {
				skipping = false
			}
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

// fakeClock truncates one timestamp to the v4 UTC HH:MM:SS form. A value that is
// not RFC3339 is kept literally, mirroring the encoder fallback.
func fakeClock(value string) string {
	if len(value) == 8 {
		return value
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format("15:04:05")
	}
	return value
}

// writeFakeApp renders one app result as the v4 single-line head plus the two
// formal collection blocks.
func writeFakeApp(builder *strings.Builder, app map[string]any) {
	kind, _ := app["kind"].(string)
	builder.WriteString("app " + kind)
	if value, ok := app["package"].(string); ok && value != "" {
		encoded, _ := json.Marshal(value)
		builder.WriteString(" pkg=" + string(encoded))
	}
	for _, field := range []struct{ key, label string }{
		{"task_id", "task"}, {"android_user_id", "user"}, {"source_task_id", "src_task"},
	} {
		if value, ok := app[field.key].(int); ok {
			builder.WriteString(" " + field.label + "=" + strconv.Itoa(value))
		}
	}
	if value, present := app["target_task_ids"]; present {
		encoded, _ := json.Marshal(value)
		builder.WriteString(" target_tasks=" + string(encoded))
	}
	for _, field := range []struct{ key, label string }{
		{"source_display_id", "src_display"}, {"display_id", "display"},
	} {
		if value, ok := app[field.key].(int); ok {
			builder.WriteString(" " + field.label + "=" + strconv.Itoa(value))
		}
	}
	if value, present := app["remaining_task_ids"]; present {
		encoded, _ := json.Marshal(value)
		builder.WriteString(" remaining_tasks=" + string(encoded))
	}
	if value, ok := app["ownership"].(string); ok && value != "" {
		builder.WriteString(" ownership=" + value)
	}
	if value, ok := app["reused"].(bool); ok {
		builder.WriteString(" reused=" + strconv.FormatBool(value))
	}
	if value, present := app["state_preserved"]; present {
		if value == nil {
			builder.WriteString(" state_preserved=null")
		} else if boolean, ok := value.(bool); ok {
			builder.WriteString(" state_preserved=" + strconv.FormatBool(boolean))
		}
	}
	if value, ok := app["foreground_restore"].(string); ok && value != "" {
		builder.WriteString(" fg_restore=" + value)
	}
	if value, ok := app["complete"].(bool); ok {
		builder.WriteString(" complete=" + strconv.FormatBool(value))
	}
	for _, key := range []string{"target_pids", "killed_pids", "remaining_pids"} {
		if value, present := app[key]; present {
			encoded, _ := json.Marshal(value)
			builder.WriteString(" " + key + "=" + string(encoded))
		}
	}
	if value, present := app["restarted"]; present {
		if value == nil {
			builder.WriteString(" restarted=null")
		} else if boolean, ok := value.(bool); ok {
			builder.WriteString(" restarted=" + strconv.FormatBool(boolean))
		}
	}
	if value, ok := app["package_state"].(string); ok && value != "" {
		builder.WriteString(" package_state=" + value)
	}
	if value, ok := app["total"].(int); ok {
		builder.WriteString(" total=" + strconv.Itoa(value))
	}
	if value, ok := app["truncated"].(bool); ok {
		builder.WriteString(" truncated=" + strconv.FormatBool(value))
	}
	if foreground, ok := app["foreground"].(map[string]any); ok {
		builder.WriteString(" fg=" + fakeForegroundToken(foreground))
	}
	if capturedAt, ok := app["captured_at"].(string); ok && capturedAt != "" {
		builder.WriteString(" at=" + fakeClock(capturedAt))
	}
	builder.WriteByte('\n')

	if items, present := app["items"].([]any); present {
		builder.WriteByte('\n')
		builder.WriteString("app_items v1 kind=" + kind + " count=" + strconv.Itoa(len(items)) + "\n")
		for _, item := range items {
			object, _ := item.(map[string]any)
			switch kind {
			case "find":
				name, _ := object["name"].(string)
				pkg, _ := object["package"].(string)
				encoded, _ := json.Marshal(name)
				builder.WriteString(pkg + " " + string(encoded) + "\n")
			case "list":
				taskID, _ := object["task_id"].(int)
				pkg, _ := object["package"].(string)
				ownership, _ := object["ownership"].(string)
				builder.WriteString("#" + strconv.Itoa(taskID) + " " + pkg + " " + ownership)
				if display, ok := object["origin_display"].(int); ok {
					builder.WriteString(" origin_display=" + strconv.Itoa(display))
				}
				builder.WriteByte('\n')
			}
		}
		builder.WriteString("end app_items\n")
	}
	if tasks, present := app["tasks"].([]any); present {
		builder.WriteByte('\n')
		builder.WriteString("app_tasks v1 count=" + strconv.Itoa(len(tasks)) + "\n")
		for _, task := range tasks {
			object, _ := task.(map[string]any)
			taskID, _ := object["task_id"].(int)
			outcome, _ := object["outcome"].(string)
			builder.WriteString("#" + strconv.Itoa(taskID) + " " + outcome)
			if value, present := object["recents_cleaned"]; present {
				if value == nil {
					builder.WriteString(" recents_cleaned=null")
				} else if boolean, ok := value.(bool); ok {
					builder.WriteString(" recents_cleaned=" + strconv.FormatBool(boolean))
				}
			}
			if errorObject, ok := object["error"].(map[string]any); ok {
				code, _ := errorObject["code"].(string)
				message, _ := errorObject["message"].(string)
				encoded, _ := json.Marshal(message)
				builder.WriteString(" error=" + code + " " + string(encoded))
				if details, present := errorObject["details"]; present {
					encodedDetails, _ := json.Marshal(details)
					builder.WriteString(" details=" + string(encodedDetails))
				}
			}
			builder.WriteByte('\n')
		}
		builder.WriteString("end app_tasks\n")
	}
	writeFakeAppFallbacks(builder, app)
}

// writeFakeAppFallbacks emits unknown app members, including unknown fields of
// one collection item, through json v1. A future field must survive the text
// projection instead of being silently dropped by the fake emitter.
func writeFakeAppFallbacks(builder *strings.Builder, app map[string]any) {
	known := map[string]bool{
		"kind": true, "package": true, "task_id": true, "source_task_id": true,
		"android_user_id": true, "target_task_ids": true, "remaining_task_ids": true,
		"source_display_id": true, "display_id": true, "ownership": true,
		"reused": true, "state_preserved": true, "foreground_restore": true,
		"complete": true, "target_pids": true, "killed_pids": true,
		"remaining_pids": true, "restarted": true, "package_state": true, "total": true,
		"truncated": true, "foreground": true, "captured_at": true,
		"items": true, "tasks": true,
	}
	for key, value := range app {
		if known[key] {
			continue
		}
		encoded, _ := json.Marshal(value)
		builder.WriteString("json v1 path=" + strconv.Quote("/app/"+key) + " value=" + string(encoded) + "\n")
	}
	if items, present := app["items"].([]any); present {
		for index, item := range items {
			object, _ := item.(map[string]any)
			for key, value := range object {
				switch key {
				case "name", "package", "task_id", "ownership", "origin_display":
					continue
				}
				encoded, _ := json.Marshal(value)
				builder.WriteString("json v1 path=" + strconv.Quote("/app/items/"+strconv.Itoa(index)+"/"+key) +
					" value=" + string(encoded) + "\n")
			}
		}
	}
	if tasks, present := app["tasks"].([]any); present {
		for index, task := range tasks {
			object, _ := task.(map[string]any)
			for key, value := range object {
				switch key {
				case "task_id", "outcome", "recents_cleaned", "error":
					continue
				}
				encoded, _ := json.Marshal(value)
				builder.WriteString("json v1 path=" + strconv.Quote("/app/tasks/"+strconv.Itoa(index)+"/"+key) +
					" value=" + string(encoded) + "\n")
			}
		}
	}
}

func writeFakeShell(builder *strings.Builder, value any) {
	shell, _ := value.(map[string]any)
	if shell == nil {
		writeFakeField(builder, "shell", value)
		return
	}
	termination, _ := shell["termination"].(string)
	exitCode, _ := shell["exit_code"].(int)
	truncated, _ := shell["truncated"].(bool)
	output, outputPresent := shell["output"].(string)
	var block strings.Builder
	block.WriteString("shell v1 termination=")
	block.WriteString(termination)
	block.WriteString(" exit_code=")
	block.WriteString(strconv.Itoa(exitCode))
	block.WriteString(" truncated=")
	block.WriteString(strconv.FormatBool(truncated))
	block.WriteString(" output_bytes=")
	if outputPresent {
		block.WriteString(strconv.Itoa(len(output)))
	} else {
		block.WriteString("null")
	}
	block.WriteByte('\n')
	if outputPresent {
		if output == "" {
			block.WriteString("output \"\"\n")
		} else {
			for len(output) > 0 {
				end := strings.IndexByte(output, '\n')
				chunk := output
				if end >= 0 {
					chunk = output[:end+1]
					output = output[end+1:]
				} else {
					output = ""
				}
				encoded, _ := json.Marshal(chunk)
				block.WriteString("output ")
				block.Write(encoded)
				block.WriteByte('\n')
			}
		}
	}
	if outputError, ok := shell["output_error"]; ok {
		encoded, _ := json.Marshal(outputError)
		block.WriteString("output_error ")
		block.Write(encoded)
		block.WriteByte('\n')
	}
	block.WriteString("end shell\n")
	writeFakeBlock(builder, block.String())
}

func writeFakeWait(builder *strings.Builder, wait map[string]any) {
	trigger, _ := wait["trigger"].(string)
	builder.WriteString("wait trigger=" + trigger)
	if matched, ok := wait["matched"].([]any); ok && len(matched) > 0 {
		parts := make([]string, 0, len(matched))
		for _, item := range matched {
			parts = append(parts, item.(string))
		}
		builder.WriteString(" matched=" + strings.Join(parts, ","))
	}
	if elapsed, ok := wait["elapsed_ms"].(int64); ok {
		builder.WriteString(" elapsed=" + strconv.FormatInt(elapsed, 10) + "ms")
	} else if elapsed, ok := wait["elapsed_ms"].(int); ok {
		builder.WriteString(" elapsed=" + strconv.Itoa(elapsed) + "ms")
	}
	builder.WriteByte('\n')
}

func writeFakeDiagnosis(builder *strings.Builder, diagnosis map[string]any) {
	summary, _ := diagnosis["summary"].(string)
	coverage, _ := diagnosis["coverage"].(string)
	truncated, _ := diagnosis["truncated"].(bool)
	encodedSummary, _ := json.Marshal(summary)
	builder.WriteString("diagnosis summary=" + string(encodedSummary) +
		" coverage=" + coverage + " truncated=" + strconv.FormatBool(truncated))
	if operation, ok := diagnosis["operation_id"].(string); ok && operation != "" {
		builder.WriteString(" op=" + operation)
	}
	if outcome, ok := diagnosis["outcome"].(string); ok && outcome != "" {
		builder.WriteString(" outcome=" + outcome)
	}
	builder.WriteByte('\n')
	if issues, present := diagnosis["issues"]; present {
		encoded, _ := json.Marshal(issues)
		builder.WriteString("diagnosis.issues=" + string(encoded) + "\n")
	}
	if counts, present := diagnosis["error_counts"]; present {
		encoded, _ := json.Marshal(counts)
		builder.WriteString("diagnosis.error_counts=" + string(encoded) + "\n")
	}
	if timing, present := diagnosis["timing_ms"]; present {
		encoded, _ := json.Marshal(timing)
		builder.WriteString("diagnosis.timing_ms=" + string(encoded) + "\n")
	}
	events, _ := diagnosis["timeline"].([]any)
	if len(events) == 0 {
		return
	}
	var block strings.Builder
	block.WriteString("timeline v1 events=")
	block.WriteString(strconv.Itoa(len(events)))
	block.WriteByte('\n')
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		block.WriteString("event ")
		block.Write(encoded)
		block.WriteByte('\n')
	}
	block.WriteString("end timeline\n")
	writeFakeBlock(builder, block.String())
}

func writeFakeCleanup(builder *strings.Builder, cleanup map[string]any) {
	state, _ := cleanup["state"].(string)
	builder.WriteString("cleanup " + state)
	for _, field := range []struct{ key, label string }{
		{"execution", "exec"}, {"tasks", "tasks"}, {"recents", "recents"}, {"display", "display"},
	} {
		if value, ok := cleanup[field.key].(string); ok && value != "" {
			builder.WriteString(" " + field.label + "=" + value)
		}
	}
	builder.WriteByte('\n')
	if errorObject, ok := cleanup["error"].(map[string]any); ok {
		writeFakeError(builder, "cleanup.error", errorObject)
	}
}

func writeFakeError(builder *strings.Builder, prefix string, errorObject map[string]any) {
	code, _ := errorObject["code"].(string)
	message, _ := errorObject["message"].(string)
	encodedMessage, _ := json.Marshal(message)
	builder.WriteString(prefix + " " + code + " " + string(encodedMessage) + "\n")
	if details, present := errorObject["details"]; present {
		encodedDetails, _ := json.Marshal(details)
		builder.WriteString(prefix + ".details=" + string(encodedDetails) + "\n")
	}
}

// startFakeAdapter serves the 13 tools over the SDK in-memory transport. It
// publishes the source server instructions so the discovery checks exercise the
// same text a real adapter serves.
func startFakeAdapter(t *testing.T, adapter *fakeAdapter) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "fake-adapter", Version: "1"},
		&sdk.ServerOptions{Instructions: mcpserver.ServerInstructions()})
	for _, tool := range sourceCatalogTools(t) {
		name := tool.Name
		server.AddTool(tool,
			func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return adapter.handle(ctx, name, request)
			})
	}
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := newMCPClient().Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// ---------------------------------------------------------------- fake service

// fakeStatusService answers /api/status for the probe's independent polls.
func startFakeStatusService(t *testing.T, canStart bool) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-agent-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Header.Get("X-Mobile-Use-Contract") != protocol.ContractVersion {
			writer.WriteHeader(http.StatusConflict)
			return
		}
		if request.URL.Path != "/api/status" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Reference string `json:"reference"`
		}
		data, _ := io.ReadAll(io.LimitReader(request.Body, 1<<16))
		if len(data) > 0 {
			_ = json.Unmarshal(data, &body)
		}
		var result *protocol.Result
		if body.Reference == "" {
			result = &protocol.Result{CanStart: &canStart}
			if canStart {
				result.Cleanup = &protocol.Cleanup{State: protocol.CleanupComplete}
			} else {
				result.BlockingCode = "control_sync_pending"
			}
		} else {
			result = &protocol.Result{OperationID: body.Reference, OperationState: operationFinished, Outcome: outcomeExecuted}
		}
		encoded, err := protocol.JSON(result)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(encoded)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func newTestServiceClient(t *testing.T, server *httptest.Server) *client.Client {
	t.Helper()
	service, err := client.New(client.Config{BaseURL: server.URL, AgentToken: "test-agent-token"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return service
}

// ---------------------------------------------------------------- MCP checks

func TestAdapterChecksAgainstFakeAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			adapter := newFakeAdapter()
			adapter.resultMode = mode
			session := startFakeAdapter(t, adapter)
			serviceServer := startFakeStatusService(t, true)
			service := newTestServiceClient(t, serviceServer)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			probe := &probe{
				reporter: reporter, session: session, service: service, resultMode: mode,
			}
			blocked := probe.runChecks(ctx)
			if code := reporter.finish(blocked); code != 0 {
				t.Fatalf("checks failed (exit %d):\n%s", code, output.String())
			}
			wants := []string{
				"PASS tools count=13",
				"PASS catalog tools=13",
				"PASS catalog_fingerprint",
				"PASS act_scroll_schema",
				"PASS schema objects=13",
				"PASS result_error",
				"PASS status can_start=true",
				"PASS start",
				"PASS app_find",
				"PASS app_find_empty",
				"PASS app_list",
				"PASS app_list_primary",
				"PASS app_admission",
				"PASS observe",
				"PASS snap",
				"PASS shell_projection",
				"PASS timeline_projection",
				"PASS inspect",
				"PASS act",
				"PASS end",
				"PASS cancel",
				"RESULT PASS",
			}
			if mode != resultModeStructured {
				wants = append(wants, "PASS observe", "tree_block=v5",
					"PASS inspect", "inspect_block=complete tree_block=v3")
			}
			for _, want := range wants {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
			if adapter.startCalls != 2 || adapter.endCalls != 1 {
				t.Fatalf("startCalls=%d endCalls=%d", adapter.startCalls, adapter.endCalls)
			}
		})
	}
}

func TestAdapterChecksBlocked(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.canStart = false
	session := startFakeAdapter(t, adapter)
	serviceServer := startFakeStatusService(t, true)
	service := newTestServiceClient(t, serviceServer)

	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe := &probe{reporter: reporter, session: session, service: service}
	blocked := probe.runChecks(ctx)
	if !blocked {
		t.Fatalf("blocked = false:\n%s", output.String())
	}
	if code := reporter.finish(blocked); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(output.String(), "FAIL status reason=can_start_false") {
		t.Fatalf("missing blocked line:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "RESULT BLOCKED") {
		t.Fatalf("missing blocked result:\n%s", output.String())
	}
	if adapter.startCalls != 0 {
		t.Fatalf("device work started while blocked")
	}
}

func TestInspectCheckRejectsLegacyBlock(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.legacyInspect = true
	session := startFakeAdapter(t, adapter)

	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := &probe{
		reporter: newReporter(&output), session: session,
		resultMode: resultModeBoth,
	}
	if runner.inspectCheck(ctx, "O1") {
		t.Fatalf("legacy inspect block passed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "FAIL inspect reason=text_result_block_unknown") {
		t.Fatalf("legacy inspect was not attributed:\n%s", output.String())
	}
}

func TestInspectCheckRejectsMissingBlock(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.omitInspectBlock = true
	adapter.resultMode = resultModeText
	session := startFakeAdapter(t, adapter)

	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runner := &probe{
		reporter: newReporter(&output), session: session,
		resultMode: resultModeText,
	}
	if runner.inspectCheck(ctx, "O1") {
		t.Fatalf("missing inspect block passed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "FAIL inspect reason=text_inspect_block_missing") {
		t.Fatalf("missing inspect block was not attributed:\n%s", output.String())
	}
}

func TestAdapterChecksImageLeakFails(t *testing.T) {
	adapter := newFakeAdapter()
	session := startFakeAdapter(t, adapter)
	serviceServer := startFakeStatusService(t, true)
	service := newTestServiceClient(t, serviceServer)

	// Make the adapter leak the image carrier in its text projection.
	adapter.leakImage = true
	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	probe := &probe{reporter: reporter, session: session, service: service}
	blocked := probe.runChecks(ctx)
	if code := reporter.finish(blocked); code != 1 {
		t.Fatalf("exit = %d, want 1:\n%s", code, output.String())
	}
	if !strings.Contains(output.String(), "FAIL observe") {
		t.Fatalf("missing leak failure:\n%s", output.String())
	}
	// A leaked carrier is either caught by the explicit base64 scan or, in both
	// mode, by the text/structured object comparison; either way it must fail.
	if !strings.Contains(output.String(), "reason=text_carries_base64") &&
		!strings.Contains(output.String(), "reason=both_mode_object_mismatch") {
		t.Fatalf("leak was not attributed:\n%s", output.String())
	}
}

// TestInstructionsCheck proves the mandatory discovery check compares the live
// instructions with the source accessor and rejects a missing payload.
func TestInstructionsCheck(t *testing.T) {
	for _, term := range []string{"QUALITY", "effect", "观察质量", "滚动效果"} {
		result := &sdk.InitializeResult{Instructions: mcpserver.ServerInstructions() + " " + term}
		if failure := checkInstructions(result); failure == nil ||
			failure.reason != "instructions_hidden_result_term" {
			t.Fatalf("instructions term %q: failure = %v", term, failure)
		}
	}
	source := mcpserver.ServerInstructions()
	for _, required := range []string{
		"新 v5 只生成普通节点定义",
		"tree v5 full nodes=<n>",
		"inspect v3 一律使用两格缩进",
		"链下缩进的孩子属于链尾",
		"inspect({reference:原 act 的 O,node:诊断 ref})",
		"O 过期后仍可能可读",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("source instructions lack %q", required)
		}
	}
	session := startFakeAdapter(t, newFakeAdapter())
	var output bytes.Buffer
	runner := &probe{reporter: newReporter(&output), session: session}
	if !runner.instructionsCheck() {
		t.Fatalf("instructions check failed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "PASS instructions") ||
		!strings.Contains(output.String(), "source_match=true") {
		t.Fatalf("missing instructions pass:\n%s", output.String())
	}

	empty := startNoInstructionsAdapter(t)
	output.Reset()
	runner = &probe{reporter: newReporter(&output), session: empty}
	if runner.instructionsCheck() {
		t.Fatalf("empty instructions passed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "FAIL instructions reason=instructions_missing") {
		t.Fatalf("missing instructions failure:\n%s", output.String())
	}
}

// TestCompatibilityInstructionsCheck proves the additional initialize-era check
// reads the same instructions and requires the compatibility protocol version.
func TestCompatibilityInstructionsCheck(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fake-adapter", Version: "1"},
		&sdk.ServerOptions{Instructions: mcpserver.ServerInstructions()})
	connector := inMemoryCompatibilityInitialize(server, compatibilityProtocolVersion)
	var output bytes.Buffer
	runner := &probe{reporter: newReporter(&output), compatInitialize: connector}
	if !runner.compatibilityInstructionsCheck(context.Background()) {
		t.Fatalf("compatibility instructions check failed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "PASS instructions_compat") ||
		!strings.Contains(output.String(), "protocol="+compatibilityProtocolVersion) {
		t.Fatalf("missing compatibility pass:\n%s", output.String())
	}

	mismatched := sdk.NewServer(&sdk.Implementation{Name: "fake-adapter", Version: "1"},
		&sdk.ServerOptions{Instructions: mcpserver.ServerInstructions() + "stale"})
	output.Reset()
	runner = &probe{reporter: newReporter(&output),
		compatInitialize: inMemoryCompatibilityInitialize(mismatched, compatibilityProtocolVersion)}
	if runner.compatibilityInstructionsCheck(context.Background()) {
		t.Fatalf("mismatched instructions passed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "FAIL instructions_compat reason=instructions_mismatch") {
		t.Fatalf("missing mismatch failure:\n%s", output.String())
	}

	output.Reset()
	runner = &probe{reporter: newReporter(&output)}
	if !runner.compatibilityInstructionsCheck(context.Background()) {
		t.Fatalf("nil connector should be skipped, not failed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "SKIP instructions_compat reason=no_secondary_transport") {
		t.Fatalf("missing skip line:\n%s", output.String())
	}
}

// startNoInstructionsAdapter serves an empty server over the in-memory
// transport, which is the reverse control for the instructions check.
func startNoInstructionsAdapter(t *testing.T) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "no-instructions", Version: "1"}, nil)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := newMCPClient().Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// inMemoryCompatibilityInitialize opens a second in-memory session pinned to the
// given protocol version against the same server and returns its initialize
// result.
func inMemoryCompatibilityInitialize(server *sdk.Server, protocolVersion string) func(context.Context) (*sdk.InitializeResult, *checkError) {
	return func(ctx context.Context) (*sdk.InitializeResult, *checkError) {
		serverTransport, clientTransport := sdk.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			return nil, failf("server_connect_failed")
		}
		session, err := newMCPClient().Connect(ctx, clientTransport, &sdk.ClientSessionOptions{
			ProtocolVersion: protocolVersion,
		})
		if err != nil {
			_ = serverSession.Close()
			return nil, failf("client_connect_failed")
		}
		result := session.InitializeResult()
		_ = session.Close()
		_ = serverSession.Close()
		return result, nil
	}
}

// ---------------------------------------------------------------- p-recovery service

type fakeRecoveryService struct {
	server        *httptest.Server
	mutex         sync.Mutex
	token         string
	canStart      bool
	startCount    int
	endCount      int
	shellCount    int
	revision      int
	session       *protocol.Session
	sessionOrigin string
	operations    map[string]string // P -> finished/running
	outcomes      map[string]string
	image         []byte
	// appTaskID is the one live controlled task. A kill keeps the task window,
	// close removes it, and the next open returns a new id for the closed close
	// loop.
	appTaskID       int
	appNextTaskID   int
	killRefusalCode string
	// primaryCandidate exposes one same-package shared task on the primary
	// display, so the package-entry refusal path is reachable.
	primaryCandidate bool
	// closeEligible models the five-minute close eligibility of the controlled
	// package: one confirmed close allows one package-entry kill.
	closeEligible bool
	// The two omission switches are reverse controls: removing a fact from a
	// fixture must make the matching probe check fail.
	omitActQuality   bool
	omitScrollEffect bool
}

func startFakeRecoveryService(t *testing.T) *fakeRecoveryService {
	t.Helper()
	fake := &fakeRecoveryService{
		token:         "test-agent-token",
		canStart:      true,
		operations:    make(map[string]string),
		outcomes:      make(map[string]string),
		image:         testJPEG(4096),
		appNextTaskID: 41,
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeRecoveryService) authorized(request *http.Request) bool {
	return request.Header.Get("Authorization") == "Bearer "+f.token &&
		request.Header.Get("X-Mobile-Use-Contract") == protocol.ContractVersion
}

func (f *fakeRecoveryService) serve(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/health" {
		writeTestJSON(writer, map[string]any{
			"status": "ok", "contract_version": protocol.ContractVersion,
			"service_instance_id": "fake", "release_version": "test",
		})
		return
	}
	if !f.authorized(request) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	operationID := request.Header.Get("X-Mobile-Use-Operation")
	switch request.URL.Path {
	case "/api/status":
		var statusBody struct {
			Reference string `json:"reference"`
		}
		_ = json.Unmarshal(body, &statusBody)
		f.writeStatus(writer, statusBody.Reference)
	case "/api/start":
		f.writeStart(writer, operationID)
	case "/api/observe":
		f.writeObserve(writer, request, operationID, body)
	case "/api/snap":
		f.writeSnap(writer, operationID)
	case "/api/wait_for":
		f.writeWait(writer, operationID, body)
	case "/api/act":
		f.writeAct(writer, operationID, body)
	case "/api/inspect":
		f.writeInspect(writer, body)
	case "/api/end":
		f.writeEnd(writer, operationID, body)
	case "/api/app":
		f.writeApp(writer, operationID, body)
	case "/api/shell":
		f.writeShell(writer, operationID, body)
	case "/api/diagnose":
		f.writeDiagnosis(writer)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeRecoveryService) writeStatus(writer http.ResponseWriter, reference string) {
	f.mutex.Lock()
	if reference != "" {
		state, found := f.operations[reference]
		if !found {
			f.mutex.Unlock()
			f.writeResult(writer, &protocol.Result{OperationID: reference, OperationState: operationNotSeen})
			return
		}
		result := &protocol.Result{
			OperationID: reference, OperationState: state,
			Outcome: f.outcomes[reference], Phase: "finished",
		}
		if f.session != nil && f.session.Origin == reference {
			result.IncludeSession = true
			result.Session = f.session
		}
		f.mutex.Unlock()
		f.writeResult(writer, result)
		return
	}
	canStart := f.canStart
	result := &protocol.Result{CanStart: &canStart}
	if f.session != nil {
		result.IncludeSession = true
		result.Session = f.session
		result.StartOperation = f.sessionOrigin
	}
	if f.canStart {
		result.Cleanup = &protocol.Cleanup{State: protocol.CleanupComplete}
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeStart(writer http.ResponseWriter, operationID string) {
	f.mutex.Lock()
	if _, exists := f.operations[operationID]; exists {
		result := &protocol.Result{
			OperationID: operationID, OperationState: operationFinished,
			Outcome: f.outcomes[operationID], Phase: "finished",
			IncludeSession: f.session != nil, Session: f.session, StartOperation: f.sessionOrigin,
		}
		f.mutex.Unlock()
		f.writeResult(writer, result)
		return
	}
	f.startCount++
	f.sessionOrigin = operationID
	f.session = &protocol.Session{
		Origin: operationID, State: sessionActive, Mode: "virtual",
		Agent: "local-agent", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	f.canStart = false
	f.revision++
	observation := f.observationLocked(false, stableTestQuality())
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{
		OperationID: operationID, Outcome: outcomeExecuted, Phase: "finished",
		IncludeSession: true, Session: f.session, Observation: observation,
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeObserve(writer http.ResponseWriter, request *http.Request, operationID string, body []byte) {
	var observeBody struct {
		Reference string `json:"reference"`
		Full      bool   `json:"full"`
		Image     bool   `json:"image"`
		WaitMS    int    `json:"wait_ms"`
	}
	_ = json.Unmarshal(body, &observeBody)
	if observeBody.WaitMS > 0 {
		timer := time.NewTimer(time.Duration(observeBody.WaitMS) * time.Millisecond)
		select {
		case <-request.Context().Done():
		case <-timer.C:
		}
		timer.Stop()
	}
	f.mutex.Lock()
	f.revision++
	observation := f.observationLocked(observeBody.Image, unsampledTestQuality())
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{OperationID: operationID, Observation: observation}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

// writeWait answers one condition wait with the frozen timeout fact and the
// terminal frame it published.
func (f *fakeRecoveryService) writeWait(writer http.ResponseWriter, operationID string, body []byte) {
	var waitBody struct {
		Reference string `json:"reference"`
		Timeout   int    `json:"timeout"`
	}
	_ = json.Unmarshal(body, &waitBody)
	if waitBody.Timeout < 1 || waitBody.Timeout > 40 {
		f.writeResult(writer, &protocol.Result{OperationID: operationID, Error: &protocol.Error{
			Code: protocol.CodeInvalidRequest, Message: "timeout 超出允许范围",
		}})
		return
	}
	f.mutex.Lock()
	f.revision++
	observation := f.observationLocked(false, unsampledTestQuality())
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{
		OperationID: operationID,
		Observation: observation,
		Wait: &protocol.WaitResult{
			Trigger:   protocol.WaitTriggerTimeout,
			Matched:   []string{protocol.WaitTriggerTimeout},
			ElapsedMS: 1000,
		},
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeAct(writer http.ResponseWriter, operationID string, body []byte) {
	var actBody struct {
		Reference string `json:"reference"`
		Image     bool   `json:"image"`
		Operation struct {
			Type       string `json:"type"`
			AutoSubmit *bool  `json:"auto_submit"`
		} `json:"operation"`
	}
	_ = json.Unmarshal(body, &actBody)
	f.mutex.Lock()
	f.revision++
	quality := stableTestQuality()
	if f.omitActQuality {
		quality = nil
	}
	observation := f.observationLocked(actBody.Image, quality)
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{
		OperationID: operationID, Outcome: outcomeExecuted,
		Observation: observation, TargetReference: actBody.Reference,
	}
	// Only a scroll act result carries an effect fact; key/tap/swipe results
	// must omit it entirely.
	if actBody.Operation.Type == "scroll" && !f.omitScrollEffect {
		result.Effect = &protocol.Effect{State: protocol.EffectObserved}
	}
	// An accepted text act carries the stage snapshot unless the caller
	// explicitly disabled auto_submit; that false branch keeps the old shape.
	if actBody.Operation.Type == "text" &&
		(actBody.Operation.AutoSubmit == nil || *actBody.Operation.AutoSubmit) {
		result.TextInput = &protocol.TextInputView{
			Write: protocol.TextInputExecuted, Submit: protocol.TextInputExecuted,
		}
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

// writeInspect returns one complete historical tree for the inspect probe.
func (f *fakeRecoveryService) writeInspect(writer http.ResponseWriter, body []byte) {
	var inspectBody struct {
		Reference string `json:"reference"`
	}
	_ = json.Unmarshal(body, &inspectBody)
	f.mutex.Lock()
	if f.session == nil {
		f.mutex.Unlock()
		f.writeResult(writer, &protocol.Result{
			Error: &protocol.Error{
				Code: "observation_not_found", Message: "probe observation unavailable",
			},
		})
		return
	}
	observationID := inspectBody.Reference
	if observationID == "" {
		observationID = fmt.Sprintf("O%d", f.revision)
	}
	view := &protocol.InspectionView{
		ObservationID: observationID,
		Origin:        f.sessionOrigin,
		Revision:      f.revision,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Scope:         "tree",
		View:          protocol.ViewDetailed,
		Tree:          testDetailedTree(),
	}
	f.mutex.Unlock()
	f.writeResult(writer, &protocol.Result{Inspection: view})
}

func (f *fakeRecoveryService) writeApp(writer http.ResponseWriter, operationID string, body []byte) {
	var appBody struct {
		Kind   string `json:"kind"`
		Query  string `json:"query"`
		Scope  string `json:"scope"`
		App    string `json:"app"`
		Target struct {
			TaskID int    `json:"task_id"`
			App    string `json:"app"`
		} `json:"target"`
	}
	_ = json.Unmarshal(body, &appBody)
	f.mutex.Lock()
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	var view *protocol.AppView
	var observation *protocol.Observation
	var resultError *protocol.Error
	outcome := ""
	switch appBody.Kind {
	case protocol.AppKindFind:
		items := []protocol.AppItem{{Name: "Probe App", Package: "com.example.probe"}}
		total := 1
		if appBody.Query == probeEmptyQuery {
			items = []protocol.AppItem{}
			total = 0
		}
		truncated := false
		view = &protocol.AppView{
			Kind: protocol.AppKindFind, Items: items, HasItems: true,
			Total: total, HasTotal: true,
			Truncated: &truncated, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	case protocol.AppKindList:
		displayID := 12
		if appBody.Scope == protocol.AppScopePrimary {
			displayID = 0
		}
		truncated := false
		items := []protocol.AppTaskItem{}
		total := 0
		switch {
		case appBody.App != "" && appBody.Scope == protocol.AppScopePrimary && f.primaryCandidate:
			// One same-package shared task lives on the primary display; the
			// package-entry kill must refuse while it stays outside this session.
			items = []protocol.AppTaskItem{{
				TaskID: 77, Package: appBody.App, Ownership: "shared",
			}}
			total = 1
		case appBody.App != "" && appBody.Scope != protocol.AppScopePrimary && f.appTaskID != 0:
			items = []protocol.AppTaskItem{{
				TaskID: f.appTaskID, Package: appBody.App, Ownership: "owned",
			}}
			total = 1
		}
		view = &protocol.AppView{
			Kind: protocol.AppKindList, DisplayID: &displayID,
			TaskItems: items, HasItems: true,
			Total: total, HasTotal: true, Truncated: &truncated,
			CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Foreground: &protocol.Foreground{State: "unknown", Reason: "foreground_unavailable"},
		}
	case protocol.AppKindOpen:
		if f.appTaskID == 0 {
			f.appNextTaskID++
			f.appTaskID = f.appNextTaskID
		}
		taskID := f.appTaskID
		displayID := 12
		reused := false
		statePreserved := false
		outcome = outcomeExecuted
		view = &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: appBody.Target.App, TaskID: &taskID,
			DisplayID: &displayID, Ownership: "owned", Reused: &reused,
			StatePreserved: &statePreserved, HasStatePreserved: true,
		}
		f.revision++
		observation = f.observationLocked(false, stableTestQuality())
	case protocol.AppKindClose:
		closedTaskID := appBody.Target.TaskID
		if closedTaskID == 0 {
			closedTaskID = f.appTaskID
		}
		f.appTaskID = 0
		// A confirmed close establishes the five-minute close eligibility.
		f.closeEligible = true
		displayID := 12
		recents := true
		complete := true
		outcome = outcomeExecuted
		view = &protocol.AppView{
			Kind: protocol.AppKindClose, DisplayID: &displayID, Complete: &complete,
			Tasks: []protocol.AppCloseTask{{
				TaskID: closedTaskID, Outcome: outcomeExecuted,
				RecentsCleaned: &recents, HasRecentsCleaned: true,
			}},
			HasTasks: true,
		}
		f.revision++
		observation = f.observationLocked(false, stableTestQuality())
	case protocol.AppKindKill:
		packageKill := appBody.Target.App != "" && appBody.Target.TaskID == 0
		sourceTaskID := appBody.Target.TaskID
		if sourceTaskID == 0 {
			sourceTaskID = f.appTaskID
		}
		displayID := 12
		userID := 0
		liveTaskIDs := []int{}
		if f.appTaskID != 0 {
			liveTaskIDs = append(liveTaskIDs, f.appTaskID)
		}
		refusal := f.killRefusalCode
		if refusal == "" && f.primaryCandidate {
			// The same-package task on the primary display is not owned by this
			// virtual session, so both entry forms must refuse.
			refusal = "kill_refused_task_elsewhere"
		}
		if refusal == "" && packageKill && f.appTaskID == 0 && !f.closeEligible {
			refusal = "kill_refused_not_owned"
		}
		outcome = outcomeExecuted
		view = &protocol.AppView{
			Kind: protocol.AppKindKill, Package: controlledAppPackage,
			AndroidUserID: &userID, DisplayID: &displayID,
			TargetTaskIDs: liveTaskIDs, HasTargetTaskIDs: true,
		}
		if !packageKill {
			view.SourceTaskID = &sourceTaskID
		}
		if refusal != "" {
			outcome = "not_executed"
			resultError = &protocol.Error{Code: refusal, Message: "任务不可终止"}
		} else {
			targetPIDs := []int{101}
			killedPIDs := []int{101}
			remainingPIDs := []int{}
			remainingTaskIDs := liveTaskIDs
			packageState := "still_present"
			if packageKill {
				// The close that established the eligibility removed the window:
				// both the final task and the final process sets are empty.
				remainingTaskIDs = []int{}
				packageState = "stopped_at_check"
				f.closeEligible = false
			}
			restarted := false
			view.TargetPIDs = targetPIDs
			view.HasTargetPIDs = true
			view.KilledPIDs = killedPIDs
			view.HasKilledPIDs = true
			view.RemainingPIDs = remainingPIDs
			view.HasRemainingPIDs = true
			view.RemainingTaskIDs = remainingTaskIDs
			view.HasRemainingTaskIDs = true
			view.Restarted = &restarted
			view.HasRestarted = true
			view.PackageState = packageState
			f.revision++
			observation = f.observationLocked(false, stableTestQuality())
		}
	default:
		f.mutex.Unlock()
		f.writeResult(writer, &protocol.Result{
			OperationID: operationID, Outcome: "not_executed",
			App:   &protocol.AppView{Kind: appBody.Kind},
			Error: &protocol.Error{Code: "invalid_request", Message: "probe does not exercise this kind"},
		})
		return
	}
	f.outcomes[operationID] = outcome
	result := &protocol.Result{
		OperationID: operationID, Outcome: outcome, App: view,
		Observation: observation, Error: resultError,
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeShell(writer http.ResponseWriter, operationID string, body []byte) {
	var shellBody struct {
		Reference string `json:"reference"`
		Command   string `json:"command"`
	}
	_ = json.Unmarshal(body, &shellBody)
	f.mutex.Lock()
	f.shellCount++
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	exitCode := 0
	output := probeShellOutput + "\n"
	truncated := false
	result := &protocol.Result{
		OperationID: operationID,
		Outcome:     outcomeExecuted,
		Shell: &protocol.ShellView{
			Termination: "exited",
			ExitCode:    &exitCode,
			Output:      &output,
			Truncated:   &truncated,
		},
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeDiagnosis(writer http.ResponseWriter) {
	f.writeResult(writer, &protocol.Result{
		Diagnosis: &protocol.DiagnosisView{
			Summary: "probe diagnosis",
			Issues:  []protocol.DiagnosisIssue{},
			Timeline: []protocol.DiagnosisEvent{{
				At: time.Now().UTC().Format(time.RFC3339Nano), Phase: "session", Code: sessionActive,
			}},
			Coverage:  "original_record",
			Truncated: false,
		},
	})
}

// stableTestQuality is the settled, sampled quality an act post observation
// carries: samples>0 with stop_reason=stable is the normal controlled branch.
func stableTestQuality() *protocol.Quality {
	return &protocol.Quality{
		State:      protocol.QualityStable,
		Samples:    2,
		StopReason: protocol.QualityStopStable,
	}
}

// unsampledTestQuality is the explicit observe/delivery branch: unknown state,
// zero samples and not_sampled, never a missing object.
func unsampledTestQuality() *protocol.Quality {
	return &protocol.Quality{
		State:      protocol.QualityUnknown,
		Samples:    0,
		StopReason: protocol.QualityStopNotSampled,
	}
}

// observationLocked builds one complete observation. Image bytes travel only
// in the HTTP carrier, exactly as the real service sends them. Every returned
// observation carries the explicit quality object; nil is only used by the
// missing-quality reverse control.
func (f *fakeRecoveryService) observationLocked(withImage bool, quality *protocol.Quality) *protocol.Observation {
	observation := &protocol.Observation{
		Status:     observationComplete,
		ID:         fmt.Sprintf("O%d", f.revision),
		Origin:     f.sessionOrigin,
		Revision:   f.revision,
		CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Quality:    quality,
		Tree:       testFullTree(),
	}
	if withImage {
		observation.Image = &protocol.ImageMeta{
			CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Width:      461,
			Height:     1024,
			MediaType:  imageJPEG,
			Base64:     base64.StdEncoding.EncodeToString(f.image),
		}
	}
	return observation
}

func (f *fakeRecoveryService) writeEnd(writer http.ResponseWriter, operationID string, body []byte) {
	var endBody struct {
		Reference string `json:"reference"`
	}
	_ = json.Unmarshal(body, &endBody)
	f.mutex.Lock()
	if f.session != nil {
		f.session.State = sessionEnded
		f.session.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	f.canStart = true
	f.endCount++
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{
		OperationID: operationID, Outcome: outcomeExecuted,
		TargetReference: endBody.Reference, IncludeSession: f.session != nil, Session: f.session,
		Cleanup: &protocol.Cleanup{State: protocol.CleanupComplete},
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeSnap(writer http.ResponseWriter, operationID string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.operations[operationID] = operationFinished
	f.writeResult(writer, &protocol.Result{
		OperationID: operationID,
		Image: &protocol.ImageMeta{
			CapturedAt: time.Now().UTC().Format(time.RFC3339),
			Width:      461,
			Height:     1024,
			MediaType:  imageJPEG,
			ReadOnly:   true,
			Screen:     &protocol.Screen{Width: 1080, Height: 2400, Rotation: 0},
			Base64:     base64.StdEncoding.EncodeToString(f.image),
		},
	})
}

func (f *fakeRecoveryService) writeResult(writer http.ResponseWriter, result *protocol.Result) {
	encoded, err := protocol.JSON(result)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encoded)
}

func testFullTree() *protocol.ReadTreeValue {
	return &protocol.ReadTreeValue{Full: &protocol.ReadTree{
		Format: treeFull,
		Scope:  protocol.Scope,
		View:   protocol.ViewCompact,
		Root: &protocol.ReadNode{
			Ref: "n1", Role: protocol.RoleRoot,
			Bounds: protocol.KnownBounds(protocol.Bounds{Right: 1080, Bottom: 2400}),
		},
	}}
}

// testDetailedTree is the historical inspect tree of the same observation: the
// detailed view expands every retained field and never hides geometry.
func testDetailedTree() *protocol.ReadTree {
	compact := testFullTree().Full
	return &protocol.ReadTree{
		Format: compact.Format, Scope: compact.Scope, View: protocol.ViewDetailed,
		Root: compact.Root.Clone(),
	}
}

func writeTestJSON(writer http.ResponseWriter, value any) {
	encoded, err := protocol.JSON(value)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encoded)
}

func recoveryConfig(server *httptest.Server) *config {
	cfg := &config{}
	cfg.Service.BaseURL = server.URL
	cfg.Service.AgentToken = "test-agent-token"
	cfg.Transport = "stdio"
	cfg.HTTP.Listen = "127.0.0.1:18882"
	cfg.HTTP.Token = "mcp-token"
	cfg.HTTP.AllowedOrigins = []string{"http://127.0.0.1:3000"}
	return cfg
}

func TestPRecoveryAgainstFakeService(t *testing.T) {
	fake := startFakeRecoveryService(t)
	cfg := recoveryConfig(fake.server)
	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if code := runPRecovery(ctx, cfg, reporter); code != 0 {
		t.Fatalf("p-recovery failed (exit %d):\n%s", code, output.String())
	}
	for _, want := range []string{
		"PASS status can_start=true",
		"PASS missed_response",
		"PASS status_recovery",
		"PASS single_dispatch",
		"PASS observe",
		"PASS end",
		"PASS duplicate",
		"RESULT PASS",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, output.String())
		}
	}
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	if fake.startCount != 1 {
		t.Fatalf("start dispatched %d times, want 1", fake.startCount)
	}
}

func TestPRecoveryBlocked(t *testing.T) {
	fake := startFakeRecoveryService(t)
	fake.canStart = false
	cfg := recoveryConfig(fake.server)
	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code := runPRecovery(ctx, cfg, reporter); code != 2 {
		t.Fatalf("exit = %d, want 2:\n%s", code, output.String())
	}
	if !strings.Contains(output.String(), "RESULT BLOCKED") {
		t.Fatalf("missing blocked result:\n%s", output.String())
	}
}

// ---------------------------------------------------------------- real adapter

// newRealAdapterSession serves the real MCP adapter core over the SDK
// in-memory transport, backed by the fake service. It exercises the published
// schemas, result projection, image carrier and cancellation wrapper without a
// device.
func sdkResultMode(mode string) mcpserver.ResultMode {
	switch mode {
	case resultModeText:
		return mcpserver.ResultModeText
	case resultModeStructured:
		return mcpserver.ResultModeStructured
	default:
		return mcpserver.ResultModeBoth
	}
}

func newRealAdapterSession(t *testing.T, fake *fakeRecoveryService,
	mode string) (*sdk.ClientSession, *client.Client) {
	t.Helper()
	service, err := client.New(client.Config{BaseURL: fake.server.URL, AgentToken: fake.token})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	server, err := mcpserver.NewServer(mcpserver.Options{
		Client:            service,
		Logger:            slog.New(slog.DiscardHandler),
		DefaultResultMode: sdkResultMode(mode),
	})
	if err != nil {
		t.Fatalf("adapter server: %v", err)
	}
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := newMCPClient().Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, service
}

func TestProbeAgainstRealAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			fake := startFakeRecoveryService(t)
			session, service := newRealAdapterSession(t, fake, mode)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			probe := &probe{
				reporter: reporter, session: session, service: service, resultMode: mode,
			}
			blocked := probe.runChecks(ctx)
			if code := reporter.finish(blocked); code != 0 {
				t.Fatalf("checks failed (exit %d):\n%s", code, output.String())
			}
			wants := []string{
				"PASS tools count=13",
				"PASS catalog",
				"PASS catalog_fingerprint",
				"PASS act_scroll_schema",
				"PASS schema",
				"PASS result_error",
				"PASS start",
				"PASS app_find",
				"PASS app_find_empty",
				"PASS app_list",
				"PASS app_list_primary",
				"PASS app_admission",
				"PASS observe",
				"PASS snap",
				"PASS shell_projection",
				"PASS timeline_projection",
				"PASS inspect",
				"PASS act",
				"PASS end",
				"PASS cancel",
			}
			if mode != resultModeStructured {
				wants = append(wants, "PASS observe", "tree_block=v5",
					"PASS inspect", "inspect_block=complete tree_block=v3")
			}
			for _, want := range wants {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
			fake.mutex.Lock()
			starts, ends, shells := fake.startCount, fake.endCount, fake.shellCount
			fake.mutex.Unlock()
			if starts != 2 {
				t.Fatalf("start dispatched %d times, want 2", starts)
			}
			if ends != 2 {
				t.Fatalf("end issued %d times, want 2 (explicit and cancellation)", ends)
			}
			if shells != 1 {
				t.Fatalf("shell dispatched %d times, want 1", shells)
			}
		})
	}
}

// TestAppLifecycleAgainstRealAdapter drives the real result v4 encoder through
// the controlled app lifecycle so the probe's app head and collection blocks
// are compared against real encoder output, not only the test-side emitter.
func TestAppLifecycleAgainstRealAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			fake := startFakeRecoveryService(t)
			session, service := newRealAdapterSession(t, fake, mode)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			runner := &probe{
				reporter: reporter, session: session, service: service,
				resultMode: mode, suite: suiteAppLifecycle, appTarget: controlledAppPackage,
			}
			if code := reporter.finish(runner.runChecks(ctx)); code != 0 {
				t.Fatalf("app lifecycle suite failed (exit %d):\n%s", code, output.String())
			}
			for _, want := range []string{
				"PASS app_open",
				"PASS app_list_controlled",
				expectedKillLine,
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
				"SKIP app_kill_primary_refused reason=controlled_primary_task_absent",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				expectedPackageKillLine,
				"SKIP app_claim reason=controlled_primary_task_absent",
				"PASS end",
			} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
		})
	}
}

// TestAppLifecycleRefusedKillAgainstRealAdapter keeps the refusal branch on the
// real result encoder: a valid kill refusal code must still pass
// app_kill_refused, close the original task and refuse the package entry too.
func TestAppLifecycleRefusedKillAgainstRealAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			fake := startFakeRecoveryService(t)
			fake.killRefusalCode = "kill_refused_task_elsewhere"
			session, service := newRealAdapterSession(t, fake, mode)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			runner := &probe{
				reporter: reporter, session: session, service: service,
				resultMode: mode, suite: suiteAppLifecycle, appTarget: controlledAppPackage,
			}
			if code := reporter.finish(runner.runChecks(ctx)); code != 0 {
				t.Fatalf("app lifecycle suite failed (exit %d):\n%s", code, output.String())
			}
			for _, want := range []string{
				"PASS app_kill_refused task_id=42 code=kill_refused_task_elsewhere outcome=not_executed",
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
				"SKIP app_kill_primary_refused reason=controlled_primary_task_absent",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				"PASS app_kill_package pkg=com.mobileuse.probe refused=kill_refused_task_elsewhere outcome=not_executed",
				"SKIP app_claim reason=controlled_primary_task_absent",
				"PASS end",
			} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
		})
	}
}

func TestRunChecksEndsOwnSessionAfterLateFailure(t *testing.T) {
	fake := startFakeRecoveryService(t)
	fake.omitActQuality = true
	session, service := newRealAdapterSession(t, fake, resultModeBoth)

	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probe := &probe{
		reporter: reporter, session: session, service: service,
		resultMode: resultModeBoth,
	}
	if code := reporter.finish(probe.runChecks(ctx)); code != 1 {
		t.Fatalf("late failure exit = %d, want 1:\n%s", code, output.String())
	}
	if !strings.Contains(output.String(), "FAIL act reason=observation_quality_missing") {
		t.Fatalf("late failure was not attributed:\n%s", output.String())
	}

	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	if fake.startCount != 1 {
		t.Fatalf("start dispatched %d times, want 1", fake.startCount)
	}
	if fake.endCount != 1 {
		t.Fatalf("end dispatched %d times after failure, want 1", fake.endCount)
	}
	if fake.session == nil || fake.session.State != sessionEnded {
		t.Fatalf("own session after failure = %+v, want ended", fake.session)
	}
}

func TestProbeAgainstRealHTTPAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			fake := startFakeRecoveryService(t)
			service, err := client.New(client.Config{BaseURL: fake.server.URL, AgentToken: fake.token})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			server, err := mcpserver.NewServer(mcpserver.Options{
				Client: service,
				Logger: slog.New(slog.DiscardHandler),
			})
			if err != nil {
				t.Fatalf("adapter server: %v", err)
			}
			const mcpToken = "mcp-token"
			handler := mcpserver.NewHTTPHandler(server, mcpserver.HTTPConfig{
				Token:          mcpToken,
				AllowedOrigins: []string{"http://127.0.0.1:3000"},
			})
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)

			cfg := recoveryConfig(fake.server)
			cfg.HTTP.Listen = strings.TrimPrefix(httpServer.URL, "http://")
			cfg.HTTP.Token = mcpToken

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if !runNegativeHTTPChecks(ctx, cfg, reporter) {
				t.Fatalf("negative checks failed:\n%s", output.String())
			}
			session, stop, failure := connectHTTPAdapter(ctx, cfg, mode)
			if failure != nil {
				t.Fatalf("HTTP connect failed: %s %v", failure.reason, failure.fields)
			}
			defer stop()
			reporter.pass("connect", "transport=http")
			probe := &probe{
				reporter: reporter, session: session, service: service, resultMode: mode,
			}
			blocked := probe.runChecks(ctx)
			if code := reporter.finish(blocked); code != 0 {
				t.Fatalf("checks failed (exit %d):\n%s", code, output.String())
			}
			if mode != resultModeStructured {
				for _, want := range []string{
					"PASS observe", "tree_block=v5",
					"PASS inspect", "inspect_block=complete tree_block=v3",
				} {
					if !strings.Contains(output.String(), want) {
						t.Fatalf("missing %q in:\n%s", want, output.String())
					}
				}
			}
			fake.mutex.Lock()
			starts, ends, shells := fake.startCount, fake.endCount, fake.shellCount
			fake.mutex.Unlock()
			if starts != 2 || ends != 2 {
				t.Fatalf("starts=%d ends=%d, want 2/2", starts, ends)
			}
			if shells != 1 {
				t.Fatalf("shell dispatched %d times, want 1", shells)
			}
		})
	}
}

// ---------------------------------------------------------------- act quality/effect

// scrollActArguments is the contract-10 scroll operation the fake service
// accepts: a container ref plus a declared direction, never a device target.
func scrollActArguments() map[string]any {
	return map[string]any{
		"reference": "O1",
		"operation": map[string]any{
			"type":      "scroll",
			"container": map[string]any{"ref": "n2"},
			"direction": "down",
		},
	}
}

// checkScrollActResult asserts the complete scroll projection. The positive
// matrix and the reverse controls both call it, so omitting a member from the
// fixture must fail the same assertion instead of a test-specific shortcut.
func checkScrollActResult(projected *callResult, mode string) *checkError {
	view := projected.view
	if projected.result.IsError {
		return failf("scroll_act_is_error", field("code", codeOf(view)))
	}
	if resultOutcome(projected) != outcomeExecuted {
		return failf("scroll_act_not_executed", field("outcome", resultOutcome(projected)))
	}
	if failure := checkScrollEffect(projected, true); failure != nil {
		return failure
	}
	if failure := checkObservationQuality(projected, view.Observation); failure != nil {
		return failure
	}
	if mode == resultModeText {
		if view.Effect != nil {
			return failf("text_effect_unexpected")
		}
		if view.Observation.Quality != nil {
			return failf("observation_quality_unexpected")
		}
		if failure := checkEffectTextLine(projected); failure != nil {
			return failure
		}
		if failure := checkQualityTextLine(projected); failure != nil {
			return failure
		}
		return nil
	}
	if view.Effect == nil {
		return failf("scroll_effect_missing")
	}
	quality := view.Observation.Quality
	if quality == nil {
		return failf("observation_quality_missing")
	}
	if mode == resultModeBoth {
		if failure := checkEffectTextLine(projected); failure != nil {
			return failure
		}
		if failure := checkQualityTextLine(projected); failure != nil {
			return failure
		}
	}
	effectState, found := lookupPath(projected.object, []string{"effect", "state"})
	if !found || effectState != view.Effect.State {
		return failf("effect_object_mismatch")
	}
	samples, found := lookupPath(projected.object,
		[]string{"observation", "quality", "samples"})
	number, integer := exactInteger(samples)
	if !found || !integer || number != int64(quality.Samples) {
		return failf("quality_object_mismatch")
	}
	return nil
}

// TestActScrollResultProjection drives one scroll act through the real adapter
// and proves text omits effect/quality while structured and both retain them.
// The parallel key act proves a non-scroll result never carries an effect.
func TestActScrollResultProjection(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			fake := startFakeRecoveryService(t)
			session, service := newRealAdapterSession(t, fake, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			runner := &probe{
				reporter: newReporter(io.Discard), session: session,
				service: service, resultMode: mode,
			}

			projected, failure := runner.call(ctx, "act", scrollActArguments())
			if failure != nil {
				t.Fatalf("scroll act: %s %v", failure.reason, failure.fields)
			}
			if failure := checkScrollActResult(projected, mode); failure != nil {
				t.Fatalf("scroll projection: %s %v", failure.reason, failure.fields)
			}
			if mode == resultModeText {
				if projected.view.Effect != nil {
					t.Fatalf("text effect = %+v, want omitted", projected.view.Effect)
				}
				if projected.view.Observation.Quality != nil {
					t.Fatalf("text quality = %+v, want omitted",
						projected.view.Observation.Quality)
				}
			} else {
				if projected.view.Effect == nil || projected.view.Effect.State != "observed" {
					t.Fatalf("structured effect = %+v, want observed", projected.view.Effect)
				}
				quality := projected.view.Observation.Quality
				if quality == nil || quality.State != "stable" || quality.Samples != 2 ||
					quality.StopReason != "stable" {
					t.Fatalf("structured quality = %+v, want stable/2/stable", quality)
				}
			}

			key, failure := runner.call(ctx, "act", map[string]any{
				"reference": "O2",
				"operation": map[string]any{"type": "key", "key": "BACK"},
			})
			if failure != nil {
				t.Fatalf("key act: %s %v", failure.reason, failure.fields)
			}
			if key.view.Effect != nil {
				t.Fatalf("key result carries effect %+v", key.view.Effect)
			}
			if failure := checkScrollEffect(key, false); failure != nil {
				t.Fatalf("key result effect: %s", failure.reason)
			}
			if _, present := lookupPath(key.object, []string{"effect"}); present {
				t.Fatal("key result object carries effect")
			}
			if mode != resultModeStructured {
				for _, text := range key.texts {
					for _, line := range strings.Split(text, "\n") {
						if strings.HasPrefix(line, "effect=") || strings.HasPrefix(line, "effect.") {
							t.Fatal("key result carries a readable effect line")
						}
					}
				}
			}
		})
	}
}

// TestActTextInputProjection drives composite text results through the real
// adapter and asserts the fixed stage object in text, structured and both
// modes, including the explicit auto_submit=false shape with no object.
func TestActTextInputProjection(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		t.Run(mode, func(t *testing.T) {
			fake := startFakeRecoveryService(t)
			session, service := newRealAdapterSession(t, fake, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			runner := &probe{
				reporter: newReporter(io.Discard), session: session,
				service: service, resultMode: mode,
			}

			entered, failure := runner.call(ctx, "act", map[string]any{
				"reference": "O2",
				"operation": map[string]any{
					"type": "text", "target": map[string]any{"ref": "n1"}, "text": "probe",
				},
			})
			if failure != nil {
				t.Fatalf("default text act: %s %v", failure.reason, failure.fields)
			}
			if entered.view.TextInput == nil ||
				entered.view.TextInput.Write != protocol.TextInputExecuted ||
				entered.view.TextInput.Submit != protocol.TextInputExecuted {
				t.Fatalf("default text_input = %+v, want executed/executed", entered.view.TextInput)
			}

			inputOnly, failure := runner.call(ctx, "act", map[string]any{
				"reference": "O2",
				"operation": map[string]any{
					"type": "text", "target": map[string]any{"ref": "n1"},
					"text": "probe", "auto_submit": false,
				},
			})
			if failure != nil {
				t.Fatalf("input-only text act: %s %v", failure.reason, failure.fields)
			}
			if inputOnly.view.TextInput != nil {
				t.Fatalf("auto_submit=false text_input = %+v, want absent", inputOnly.view.TextInput)
			}
			if _, present := lookupPath(inputOnly.object, []string{"text_input"}); present {
				t.Fatal("auto_submit=false object carries text_input")
			}
		})
	}
}

// TestCheckTextInputSnapshot guards the fixed stage object: only the five
// ordered combinations are accepted and a partial or unknown state fails.
func TestCheckTextInputSnapshot(t *testing.T) {
	legal := []*textInputView{
		{Write: textInputNotExecuted, Submit: textInputNotExecuted},
		{Write: textInputUncertain, Submit: textInputNotExecuted},
		{Write: textInputExecuted, Submit: textInputNotExecuted},
		{Write: textInputExecuted, Submit: textInputUncertain},
		{Write: textInputExecuted, Submit: textInputExecuted},
	}
	for _, input := range legal {
		if failure := checkTextInputSnapshot(input); failure != nil {
			t.Fatalf("legal snapshot %+v rejected: %s", input, failure.reason)
		}
	}
	if failure := checkTextInputSnapshot(nil); failure != nil {
		t.Fatalf("absent snapshot rejected: %s", failure.reason)
	}
	illegal := []*textInputView{
		{Write: "", Submit: textInputNotExecuted},
		{Write: textInputNotExecuted, Submit: textInputExecuted},
		{Write: textInputUncertain, Submit: textInputUncertain},
		{Write: "unknown", Submit: textInputNotExecuted},
	}
	for _, input := range illegal {
		if failure := checkTextInputSnapshot(input); failure == nil {
			t.Fatalf("illegal snapshot %+v accepted", input)
		}
	}
}

// TestActCheckRejectsMissingQuality is the reverse control for the new live
// assertion: the same act check must fail when the fixture omits the quality
// object the live service always emits.
func TestActCheckRejectsMissingQuality(t *testing.T) {
	fake := startFakeRecoveryService(t)
	fake.omitActQuality = true
	session, service := newRealAdapterSession(t, fake, resultModeBoth)
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runner := &probe{reporter: newReporter(&output), session: session, service: service}
	if runner.actCheck(ctx, "O1") {
		t.Fatalf("act without observation quality passed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "FAIL act reason=observation_quality_missing") {
		t.Fatalf("missing quality failure:\n%s", output.String())
	}
}

// TestActScrollEffectOmissionFails is the reverse control for the scroll
// fixture: dropping effect must fail the complete scroll projection.
func TestActScrollEffectOmissionFails(t *testing.T) {
	fake := startFakeRecoveryService(t)
	fake.omitScrollEffect = true
	session, service := newRealAdapterSession(t, fake, resultModeBoth)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runner := &probe{
		reporter: newReporter(io.Discard), session: session,
		service: service, resultMode: resultModeBoth,
	}
	projected, failure := runner.call(ctx, "act", scrollActArguments())
	if failure != nil {
		t.Fatalf("scroll act: %s %v", failure.reason, failure.fields)
	}
	if failure := checkScrollActResult(projected, resultModeBoth); failure == nil ||
		failure.reason != "scroll_effect_missing" {
		t.Fatalf("omitted effect accepted: %v", failure)
	}
}

// TestDecodeResultTextAutoRecovery pins the zero-dispatch recovery line grammar:
// strategy and attempts are mandatory, the two public refs are optional but
// all-or-nothing, a transient retry repeats the original ref, and the line sits
// in the control-scalar section after input_state.
func TestDecodeResultTextAutoRecovery(t *testing.T) {
	legal := []struct {
		name string
		text string
		want map[string]any
	}{
		{
			name: "transient retry repeats the original ref",
			text: "result v4 ok op=P1\n" +
				"input_state=effective\n" +
				"auto_recovery strategy=transient_retry original_ref=n7 recovered_ref=n7 attempts=2\n" +
				"end result\n",
			want: map[string]any{
				"strategy":     autoRecoveryTransientRetry,
				"original_ref": "n7", "recovered_ref": "n7", "attempts": 2,
			},
		},
		{
			name: "text rebind publishes the replacement ref",
			text: "result v4 ok op=P1\n" +
				"auto_recovery strategy=text_rebind original_ref=n12 recovered_ref=n31 attempts=2\n" +
				"end result\n",
			want: map[string]any{
				"strategy":     autoRecoveryTextRebind,
				"original_ref": "n12", "recovered_ref": "n31", "attempts": 2,
			},
		},
		{
			name: "container rebind",
			text: "result v4 ok op=P1\n" +
				"auto_recovery strategy=container_rebind original_ref=n3 recovered_ref=n8 attempts=2\n" +
				"end result\n",
			want: map[string]any{
				"strategy":     autoRecoveryContainerRebind,
				"original_ref": "n3", "recovered_ref": "n8", "attempts": 2,
			},
		},
		{
			name: "coordinate retry omits both refs",
			text: "result v4 ok op=P1\n" +
				"auto_recovery strategy=transient_retry attempts=2\n" +
				"end result\n",
			want: map[string]any{"strategy": autoRecoveryTransientRetry, "attempts": 2},
		},
	}
	for _, testCase := range legal {
		t.Run("legal/"+testCase.name, func(t *testing.T) {
			decoded, failure := decodeResultText(testCase.text)
			if failure != nil {
				t.Fatalf("decode: %s %v", failure.reason, failure.fields)
			}
			got, ok := lookupPath(decoded.object, []string{"auto_recovery"})
			if !ok || !semanticEqual(got, testCase.want) {
				t.Fatalf("auto_recovery = %#v, want %#v", got, testCase.want)
			}
			if decoded.view.AutoRecovery == nil ||
				decoded.view.AutoRecovery.Strategy != testCase.want["strategy"] {
				t.Fatalf("typed auto_recovery = %+v", decoded.view.AutoRecovery)
			}
		})
	}

	rejected := []struct {
		name   string
		text   string
		reason string
	}{
		{
			name:   "reversed columns",
			text:   "result v4 ok\n" + "auto_recovery attempts=2 strategy=transient_retry\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "missing strategy",
			text:   "result v4 ok\n" + "auto_recovery attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "unknown strategy",
			text:   "result v4 ok\n" + "auto_recovery strategy=future_retry attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "missing attempts",
			text:   "result v4 ok\n" + "auto_recovery strategy=transient_retry\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "zero attempts",
			text:   "result v4 ok\n" + "auto_recovery strategy=transient_retry attempts=0\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "non numeric attempts",
			text:   "result v4 ok\n" + "auto_recovery strategy=transient_retry attempts=two\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "half a ref pair",
			text:   "result v4 ok\n" + "auto_recovery strategy=text_rebind original_ref=n7 attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "text rebind without refs",
			text:   "result v4 ok\n" + "auto_recovery strategy=text_rebind attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "container rebind without refs",
			text:   "result v4 ok\n" + "auto_recovery strategy=container_rebind attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "ref outside the public grammar",
			text:   "result v4 ok\n" + "auto_recovery strategy=text_rebind original_ref=n0 recovered_ref=n7 attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "transient retry changes the ref",
			text:   "result v4 ok\n" + "auto_recovery strategy=transient_retry original_ref=n7 recovered_ref=n8 attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "duplicate strategy",
			text:   "result v4 ok\n" + "auto_recovery strategy=transient_retry strategy=text_rebind original_ref=n7 recovered_ref=n7 attempts=2\n" + "end result\n",
			reason: "text_result_auto_recovery_invalid",
		},
		{
			name:   "empty body",
			text:   "result v4 ok\n" + "auto_recovery\n" + "end result\n",
			reason: "text_result_item_invalid",
		},
		{
			name: "after notify",
			text: "result v4 ok\n" +
				"notify id=M1 saved=true\n" +
				"auto_recovery strategy=transient_retry attempts=2\n" +
				"end result\n",
			reason: "text_result_order_invalid",
		},
	}
	for _, testCase := range rejected {
		t.Run("reject/"+testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(testCase.text); failure == nil ||
				failure.reason != testCase.reason {
				t.Fatalf("failure = %v, want %s", failure, testCase.reason)
			}
		})
	}
}

// TestCheckAutoRecovery guards the typed marker: an absent object is valid, the
// three published strategies and attempts >= 1 are required, and the two public
// node refs are both-or-none. Only a transient retry may omit both refs, and it
// must repeat the original ref when it carries them.
func TestCheckAutoRecovery(t *testing.T) {
	legal := []*autoRecoveryView{
		nil,
		{Strategy: autoRecoveryTransientRetry, Attempts: 2},
		{Strategy: autoRecoveryTransientRetry, OriginalRef: "n7", RecoveredRef: "n7", Attempts: 2},
		{Strategy: autoRecoveryTextRebind, OriginalRef: "n12", RecoveredRef: "n31", Attempts: 2},
		{Strategy: autoRecoveryContainerRebind, OriginalRef: "n3", RecoveredRef: "n8", Attempts: 1},
	}
	for _, recovery := range legal {
		if failure := checkAutoRecovery(recovery); failure != nil {
			t.Fatalf("legal marker %+v rejected: %s", recovery, failure.reason)
		}
	}
	illegal := []struct {
		recovery *autoRecoveryView
		reason   string
	}{
		{&autoRecoveryView{Strategy: "", Attempts: 2}, "auto_recovery_strategy_invalid"},
		{&autoRecoveryView{Strategy: "future_retry", Attempts: 2}, "auto_recovery_strategy_invalid"},
		{&autoRecoveryView{Strategy: autoRecoveryTransientRetry}, "auto_recovery_attempts_invalid"},
		{&autoRecoveryView{Strategy: autoRecoveryTransientRetry, Attempts: -1}, "auto_recovery_attempts_invalid"},
		{&autoRecoveryView{Strategy: autoRecoveryTextRebind, OriginalRef: "n7", Attempts: 2}, "auto_recovery_refs_incomplete"},
		{&autoRecoveryView{Strategy: autoRecoveryTextRebind, RecoveredRef: "n8", Attempts: 2}, "auto_recovery_refs_incomplete"},
		{&autoRecoveryView{Strategy: autoRecoveryTextRebind, Attempts: 2}, "auto_recovery_refs_missing"},
		{&autoRecoveryView{Strategy: autoRecoveryContainerRebind, Attempts: 2}, "auto_recovery_refs_missing"},
		{&autoRecoveryView{Strategy: autoRecoveryTextRebind, OriginalRef: "n0", RecoveredRef: "n8", Attempts: 2}, "auto_recovery_ref_invalid"},
		{&autoRecoveryView{Strategy: autoRecoveryContainerRebind, OriginalRef: "abc", RecoveredRef: "n8", Attempts: 2}, "auto_recovery_ref_invalid"},
		{&autoRecoveryView{Strategy: autoRecoveryTransientRetry, OriginalRef: "n7", RecoveredRef: "n8", Attempts: 2}, "auto_recovery_transient_refs_differ"},
	}
	for _, testCase := range illegal {
		if failure := checkAutoRecovery(testCase.recovery); failure == nil ||
			failure.reason != testCase.reason {
			t.Fatalf("marker %+v = %v, want %s", testCase.recovery, failure, testCase.reason)
		}
	}
}

// TestBothModeAutoRecoveryComparison proves the both-mode gate compares the
// marker member for member: a missing, extra, or value-changed marker fails and
// the four documented shapes round trip through the fake emitter and parser.
func TestBothModeAutoRecoveryComparison(t *testing.T) {
	shapes := []map[string]any{
		{
			"strategy":     autoRecoveryTransientRetry,
			"original_ref": "n7", "recovered_ref": "n7", "attempts": 2,
		},
		{
			"strategy":     autoRecoveryTextRebind,
			"original_ref": "n12", "recovered_ref": "n31", "attempts": 2,
		},
		{"strategy": autoRecoveryTransientRetry, "attempts": 2},
	}
	for _, recovery := range shapes {
		recovery := recovery
		payload := map[string]any{
			"operation_id": "P7391_12", "outcome": outcomeExecuted,
			"auto_recovery": recovery,
		}
		result := &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: fakeResultText(payload, false)}},
			StructuredContent: payload,
		}
		projected, failure := project(result, resultModeBoth)
		if failure != nil {
			t.Fatalf("matching marker rejected: %s %v", failure.reason, failure.fields)
		}
		if projected.view.AutoRecovery == nil ||
			projected.view.AutoRecovery.Strategy != recovery["strategy"] {
			t.Fatalf("typed marker = %+v", projected.view.AutoRecovery)
		}
	}

	base := map[string]any{
		"operation_id": "P7391_12", "outcome": outcomeExecuted,
		"auto_recovery": map[string]any{
			"strategy":     autoRecoveryTextRebind,
			"original_ref": "n12", "recovered_ref": "n31", "attempts": 2,
		},
	}
	cases := []struct {
		name       string
		textSource map[string]any
	}{
		{
			name: "text omits the marker",
			textSource: map[string]any{
				"operation_id": "P7391_12", "outcome": outcomeExecuted,
			},
		},
		{
			name: "text invents a marker",
			textSource: map[string]any{
				"operation_id": "P7391_12", "outcome": outcomeExecuted,
				"auto_recovery": map[string]any{
					"strategy": autoRecoveryTransientRetry, "attempts": 2,
				},
			},
		},
		{
			name: "text changes the recovered ref",
			textSource: map[string]any{
				"operation_id": "P7391_12", "outcome": outcomeExecuted,
				"auto_recovery": map[string]any{
					"strategy":     autoRecoveryTextRebind,
					"original_ref": "n12", "recovered_ref": "n32", "attempts": 2,
				},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := &sdk.CallToolResult{
				Content:           []sdk.Content{&sdk.TextContent{Text: fakeResultText(testCase.textSource, false)}},
				StructuredContent: base,
			}
			if _, failure := project(result, resultModeBoth); failure == nil ||
				failure.reason != "both_mode_auto_recovery_mismatch" {
				t.Fatalf("failure = %v, want both_mode_auto_recovery_mismatch", failure)
			}
		})
	}
}

// TestReadInitializeResponse covers the two Streamable HTTP response shapes the
// compatibility initialize check accepts. It asserts only the public facts the
// probe reads and never depends on a full session.
func TestReadInitializeResponse(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		wantProto   string
		wantBytes   int
		wantFail    string
	}{
		{
			name: "json", contentType: "application/json",
			body:      `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","instructions":"guide"}}`,
			wantProto: "2025-11-25", wantBytes: 5,
		},
		{
			name: "sse", contentType: "text/event-stream",
			body:      "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-11-25\",\"instructions\":\"guide\"}}\n\n",
			wantProto: "2025-11-25", wantBytes: 5,
		},
		{
			name: "json_error", contentType: "application/json",
			body:     `{"jsonrpc":"2.0","id":1,"error":{"code":-32600}}`,
			wantFail: "compat_response_error",
		},
		{
			name: "missing_result", contentType: "application/json",
			body:     `{"jsonrpc":"2.0","id":1}`,
			wantFail: "compat_response_missing",
		},
		{
			name: "malformed", contentType: "application/json",
			body:     `not-json`,
			wantFail: "compat_response_invalid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := &http.Response{
				Header: http.Header{"Content-Type": []string{testCase.contentType}},
				Body:   io.NopCloser(strings.NewReader(testCase.body)),
			}
			result, failure := readInitializeResponse(response)
			if testCase.wantFail != "" {
				if failure == nil || failure.reason != testCase.wantFail {
					t.Fatalf("failure = %+v, want %s", failure, testCase.wantFail)
				}
				return
			}
			if failure != nil {
				t.Fatalf("unexpected failure: %+v", failure)
			}
			if result.ProtocolVersion != testCase.wantProto || len(result.Instructions) != testCase.wantBytes {
				t.Fatalf("result = %+v, want protocol=%s bytes=%d",
					result, testCase.wantProto, testCase.wantBytes)
			}
		})
	}
}
