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
		properties["observation_id"] = map[string]any{"type": "string"}
		properties["operation"] = map[string]any{"type": "object"}
		required = []string{"observation_id", "operation"}
	case "app":
		properties["kind"] = map[string]any{
			"type": "string",
			"enum": []any{"find", "list", "open", "claim", "handoff", "close", "kill"},
		}
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
		ID:     "O000000000000000000000001",
		Tree:   &treeView{Format: treeFull},
		Image: &imageView{
			CapturedAt: "2026-01-01T00:00:00Z",
			Width:      720,
			Height:     1280,
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
}

func TestValidReference(t *testing.T) {
	cases := []struct {
		value  string
		prefix byte
		want   bool
	}{
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
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		canStart:   true,
		sessions:   make(map[string]bool),
		image:      testJPEG(4096),
		resultMode: resultModeBoth,
	}
}

func (f *fakeAdapter) nextID(prefix string) string {
	f.counter++
	return fmt.Sprintf("%s%024x", prefix, f.counter)
}

func (f *fakeAdapter) observation(id, origin string, revision int, withImage bool) map[string]any {
	observation := map[string]any{
		"status":   observationComplete,
		"id":       id,
		"origin":   origin,
		"revision": revision,
		"tree":     map[string]any{"format": treeFull, "scope": "reported_visible_with_context"},
	}
	if withImage {
		observation["image"] = map[string]any{
			"captured_at": "2026-01-01T00:00:00Z",
			"width":       720,
			"height":      1280,
			"media_type":  imageJPEG,
		}
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
	case "act":
		o := f.nextID("O")
		return f.imageResult(map[string]any{
			"operation_id": f.nextID("P"),
			"outcome":      outcomeExecuted,
			"observation":  f.observation(o, "P1", 3, true),
		}), nil
	case "inspect":
		observationID, _ := arguments["observation_id"].(string)
		if observationID == probeUnknownObservation {
			return f.errorResult(map[string]any{
				"error": map[string]any{"code": "observation_not_found"},
			}), nil
		}
		text := "inspection.observation_id=" + observationID +
			"\n\ninspect v1 scope=tree observation=" + observationID +
			" revision=2 captured_at=2026-01-01T00:00:00Z" +
			" tree_scope=reported_visible_with_context nodes=1\n\n" +
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
					"root": map[string]any{
						"ref": "n1", "role": "root", "bounds": []int{0, 0, 1080, 2400},
					},
				},
			},
		}, text), nil
	case "app":
		kind, _ := arguments["kind"].(string)
		if kind == "find" {
			if _, crossKind := arguments["reference"]; crossKind {
				return f.errorResult(map[string]any{
					"error": map[string]any{"code": "invalid_request"},
				}), nil
			}
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
		if kind == "open" {
			target, _ := arguments["app"].(string)
			o := f.nextID("O")
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      outcomeExecuted,
				"app": map[string]any{
					"kind": "open", "package": target, "task_id": 42,
					"display_id": 12, "ownership": "owned",
					"reused": false, "state_preserved": false,
				},
				"observation": f.observation(o, "P1", 2, false),
			}, ""), nil
		}
		if kind == "kill" {
			return f.errorResult(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      "not_executed",
				"app": map[string]any{
					"kind": "kill", "package": controlledAppPackage,
					"source_task_id": 42, "display_id": 12,
				},
				"error": map[string]any{"code": "kill_refused_foreground"},
			}), nil
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
			o := f.nextID("O")
			return f.result(map[string]any{
				"operation_id": f.nextID("P"),
				"outcome":      outcomeExecuted,
				"app": map[string]any{
					"kind": "close", "display_id": 12,
					"tasks": []any{map[string]any{
						"task_id": 42, "outcome": outcomeExecuted, "recents_cleaned": true,
					}},
					"complete": true,
				},
				"observation": f.observation(o, "P1", 2, false),
			}, ""), nil
		}
		return f.result(map[string]any{
			"operation_id": f.nextID("P"),
			"app": map[string]any{
				"kind": "list", "display_id": displayID,
				"items": []any{map[string]any{
					"task_id": 42, "package": "com.example.probe", "ownership": "owned",
				}},
				"total": 1, "truncated": false, "captured_at": "2026-01-01T00:00:00Z",
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

func fakeDiagnosticText(payload map[string]any) string {
	code := "internal"
	if value, ok := payload["error"].(map[string]any); ok {
		if text, ok := value["code"].(string); ok && text != "" {
			code = text
		}
	}
	return "diagnostic v1 is_error=true\nerror.code=\"" + code + "\"\nend diagnostic\n"
}

func fakeResultText(payload map[string]any, isError bool) string {
	var builder strings.Builder
	builder.WriteString("result v1 is_error=")
	builder.WriteString(strconv.FormatBool(isError))
	builder.WriteByte('\n')
	for key, value := range payload {
		switch key {
		case "session", "cleanup", "error", "recovery":
			if object, ok := value.(map[string]any); ok {
				for childKey, childValue := range object {
					writeFakeField(&builder, key+"."+childKey, childValue)
				}
			} else {
				writeFakeField(&builder, key, value)
			}
		case "observation":
			writeFakeObservation(&builder, value)
		case "inspection":
			writeFakeInspection(&builder, value)
		case "app":
			writeFakeApp(&builder, value)
		case "shell":
			writeFakeShell(&builder, value)
		case "diagnosis":
			writeFakeDiagnosis(&builder, value)
		default:
			writeFakeField(&builder, key, value)
		}
	}
	builder.WriteString("end result\n")
	return builder.String()
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

func writeFakeObservation(builder *strings.Builder, value any) {
	observation, _ := value.(map[string]any)
	if observation == nil {
		writeFakeField(builder, "observation", value)
		return
	}
	for _, key := range []string{
		"status", "id", "origin", "revision", "captured_at",
		"actionable_until", "screen", "foreground", "missing", "image", "error",
	} {
		if child, ok := observation[key]; ok {
			writeFakeField(builder, "observation."+key, child)
		}
	}
	tree, _ := observation["tree"].(map[string]any)
	if tree == nil {
		return
	}
	format, _ := tree["format"].(string)
	id, _ := observation["id"].(string)
	scope, _ := tree["scope"].(string)
	if scope == "" {
		scope = "reported_visible_with_context"
	}
	base, _ := tree["base"].(string)
	block := "tree v1 format=" + format + " id=" + id + " scope=" + scope
	if format == treeDelta && base != "" {
		block += " base=" + base
	}
	block += " nodes=1 changes=0\n\n"
	block += "n1 root [0,0,1080,2400]\nend tree\n"
	writeFakeBlock(builder, block)
}

func writeFakeInspection(builder *strings.Builder, value any) {
	inspection, _ := value.(map[string]any)
	if inspection == nil {
		writeFakeField(builder, "inspection", value)
		return
	}
	if origin, ok := inspection["origin"]; ok {
		writeFakeField(builder, "inspection.origin", origin)
	}
	observationID, _ := inspection["observation_id"].(string)
	scope, _ := inspection["scope"].(string)
	revision, _ := inspection["revision"].(int)
	capturedAt, _ := inspection["captured_at"].(string)
	block := "inspect v1 scope=" + scope +
		" observation=" + observationID +
		" revision=" + strconv.Itoa(revision) +
		" captured_at=" + capturedAt +
		" nodes=1\n\nn1 root [0,0,1080,2400]\nend inspect\n"
	writeFakeBlock(builder, block)
}

// writeFakeApp renders one app result using the declared typed lines. It is a
// test-side emitter only; the probe decoder is exercised against hand-written
// golden text in app_matrix_test.go.
func writeFakeApp(builder *strings.Builder, value any) {
	app, _ := value.(map[string]any)
	if app == nil {
		writeFakeField(builder, "app", value)
		return
	}
	for _, key := range []string{
		"kind", "items", "tasks", "foreground", "target_pids", "killed_pids",
		"remaining_pids", "package", "task_id", "source_task_id", "display_id",
		"source_display_id", "total", "reused", "state_preserved", "complete",
		"truncated", "restarted", "captured_at", "ownership", "foreground_restore",
	} {
		child, present := app[key]
		if !present {
			continue
		}
		builder.WriteString("app.")
		builder.WriteString(key)
		builder.WriteByte('=')
		switch key {
		case "kind", "ownership", "captured_at", "foreground_restore":
			if text, ok := child.(string); ok && fakeAppToken(text) {
				builder.WriteString(text)
				builder.WriteByte('\n')
				continue
			}
		}
		encoded, err := json.Marshal(child)
		if err != nil {
			encoded = []byte("null")
		}
		builder.Write(encoded)
		builder.WriteByte('\n')
	}
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

func writeFakeDiagnosis(builder *strings.Builder, value any) {
	diagnosis, _ := value.(map[string]any)
	if diagnosis == nil {
		writeFakeField(builder, "diagnosis", value)
		return
	}
	for _, key := range []string{
		"summary", "issues", "timing_ms", "coverage", "truncated", "operation_id", "outcome",
	} {
		if child, ok := diagnosis[key]; ok {
			writeFakeField(builder, "diagnosis."+key, child)
		}
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

// startFakeAdapter serves the 12 tools over the SDK in-memory transport.
func startFakeAdapter(t *testing.T, adapter *fakeAdapter) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "fake-adapter", Version: "1"}, nil)
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
			for _, want := range []string{
				"PASS tools count=11",
				"PASS catalog tools=11",
				"PASS catalog_fingerprint",
				"PASS schema objects=11",
				"PASS result_error",
				"PASS status can_start=true",
				"PASS start",
				"PASS app_find",
				"PASS app_find_empty",
				"PASS app_list",
				"PASS app_list_primary",
				"PASS app_admission",
				"PASS observe",
				"PASS shell_projection",
				"PASS timeline_projection",
				"PASS inspect",
				"PASS act",
				"PASS end",
				"PASS cancel",
				"RESULT PASS",
			} {
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
}

func startFakeRecoveryService(t *testing.T) *fakeRecoveryService {
	t.Helper()
	fake := &fakeRecoveryService{
		token:      "test-agent-token",
		canStart:   true,
		operations: make(map[string]string),
		outcomes:   make(map[string]string),
		image:      testJPEG(4096),
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
	observation := f.observationLocked(false)
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
	observation := f.observationLocked(observeBody.Image)
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{OperationID: operationID, Observation: observation}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

func (f *fakeRecoveryService) writeAct(writer http.ResponseWriter, operationID string, body []byte) {
	var actBody struct {
		ObservationID string `json:"observation_id"`
		Image         bool   `json:"image"`
	}
	_ = json.Unmarshal(body, &actBody)
	f.mutex.Lock()
	f.revision++
	observation := f.observationLocked(actBody.Image)
	f.operations[operationID] = operationFinished
	f.outcomes[operationID] = outcomeExecuted
	result := &protocol.Result{
		OperationID: operationID, Outcome: outcomeExecuted,
		Observation: observation, TargetReference: actBody.ObservationID,
	}
	f.mutex.Unlock()
	f.writeResult(writer, result)
}

// writeInspect returns one complete historical tree for the inspect probe.
func (f *fakeRecoveryService) writeInspect(writer http.ResponseWriter, body []byte) {
	var inspectBody struct {
		ObservationID string `json:"observation_id"`
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
	observationID := inspectBody.ObservationID
	if observationID == "" {
		observationID = fmt.Sprintf("O%d", f.revision)
	}
	view := &protocol.InspectionView{
		ObservationID: observationID,
		Origin:        f.sessionOrigin,
		Revision:      f.revision,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Scope:         "tree",
		Tree:          testFullTree().Full,
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
		view = &protocol.AppView{
			Kind: protocol.AppKindList, DisplayID: &displayID,
			TaskItems: []protocol.AppTaskItem{}, HasItems: true,
			Total: 0, HasTotal: true, Truncated: &truncated,
			CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Foreground: &protocol.Foreground{State: "unknown", Reason: "foreground_unavailable"},
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
	result := &protocol.Result{OperationID: operationID, App: view}
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

// observationLocked builds one complete observation. Image bytes travel only
// in the HTTP carrier, exactly as the real service sends them.
func (f *fakeRecoveryService) observationLocked(withImage bool) *protocol.Observation {
	observation := &protocol.Observation{
		Status:     observationComplete,
		ID:         fmt.Sprintf("O%d", f.revision),
		Origin:     f.sessionOrigin,
		Revision:   f.revision,
		CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Tree:       testFullTree(),
	}
	if withImage {
		observation.Image = &protocol.ImageMeta{
			CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Width:      720,
			Height:     1280,
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

func (f *fakeRecoveryService) writeResult(writer http.ResponseWriter, result *protocol.Result) {
	encoded, err := protocol.JSON(result)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encoded)
}

func testFullTree() *protocol.TreeValue {
	return &protocol.TreeValue{Full: &protocol.Tree{
		Format: treeFull,
		Root: &protocol.Node{
			Ref: "n1", Role: protocol.RoleRoot,
			Bounds: &protocol.Bounds{Right: 1080, Bottom: 2400},
		},
	}}
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
			for _, want := range []string{
				"PASS tools count=11",
				"PASS catalog",
				"PASS catalog_fingerprint",
				"PASS schema",
				"PASS result_error",
				"PASS start",
				"PASS app_find",
				"PASS app_find_empty",
				"PASS app_list",
				"PASS app_list_primary",
				"PASS app_admission",
				"PASS observe",
				"PASS shell_projection",
				"PASS timeline_projection",
				"PASS inspect",
				"PASS act",
				"PASS end",
				"PASS cancel",
			} {
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
