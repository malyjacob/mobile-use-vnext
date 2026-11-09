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
	mutex      sync.Mutex
	canStart   bool
	counter    int
	sessions   map[string]bool
	image      []byte
	leakImage  bool
	startCalls int
	endCalls   int
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{canStart: true, sessions: make(map[string]bool), image: testJPEG(4096)}
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
	result := &sdk.CallToolResult{StructuredContent: payload}
	if text != "" {
		result.Content = []sdk.Content{&sdk.TextContent{Text: text}}
	}
	return result
}

func (f *fakeAdapter) imageResult(payload map[string]any) *sdk.CallToolResult {
	text := "observation.status=complete\n"
	if f.leakImage {
		text = "observation.image.base64=" + base64.StdEncoding.EncodeToString(f.image)
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{Text: text},
			&sdk.ImageContent{Data: f.image, MIMEType: imageJPEG},
		},
		StructuredContent: payload,
	}
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
		if wait, ok := arguments["wait_ms"].(float64); ok && wait > 0 {
			f.mutex.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
			f.mutex.Lock()
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: "cancelled=true\n"}},
				StructuredContent: map[string]any{
					"operation_id": f.nextID("P"),
					"error":        map[string]any{"code": "call_cancelled"},
				},
				IsError: true,
			}, nil
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

// startFakeAdapter serves the 12 tools over the SDK in-memory transport.
func startFakeAdapter(t *testing.T, adapter *fakeAdapter) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := sdk.NewServer(&sdk.Implementation{Name: "fake-adapter", Version: "1"}, nil)
	for _, name := range toolNames {
		name := name
		server.AddTool(&sdk.Tool{Name: name, InputSchema: testSchema(name, false)},
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
	adapter := newFakeAdapter()
	session := startFakeAdapter(t, adapter)
	serviceServer := startFakeStatusService(t, true)
	service := newTestServiceClient(t, serviceServer)

	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probe := &probe{reporter: reporter, session: session, service: service}
	blocked := probe.runChecks(ctx)
	if code := reporter.finish(blocked); code != 0 {
		t.Fatalf("checks failed (exit %d):\n%s", code, output.String())
	}
	for _, want := range []string{
		"PASS tools count=12",
		"PASS schema objects=12",
		"PASS status can_start=true",
		"PASS start",
		"PASS observe",
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
	if !strings.Contains(output.String(), "FAIL observe reason=text_carries_base64") {
		t.Fatalf("missing leak failure:\n%s", output.String())
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
	case "/api/end":
		f.writeEnd(writer, operationID, body)
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
func newRealAdapterSession(t *testing.T, fake *fakeRecoveryService) (*sdk.ClientSession, *client.Client) {
	t.Helper()
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
	fake := startFakeRecoveryService(t)
	session, service := newRealAdapterSession(t, fake)

	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	probe := &probe{reporter: reporter, session: session, service: service}
	blocked := probe.runChecks(ctx)
	if code := reporter.finish(blocked); code != 0 {
		t.Fatalf("checks failed (exit %d):\n%s", code, output.String())
	}
	for _, want := range []string{
		"PASS tools count=12",
		"PASS schema",
		"PASS start",
		"PASS observe",
		"PASS act",
		"PASS end",
		"PASS cancel",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, output.String())
		}
	}
	fake.mutex.Lock()
	starts, ends := fake.startCount, fake.endCount
	fake.mutex.Unlock()
	if starts != 2 {
		t.Fatalf("start dispatched %d times, want 2", starts)
	}
	if ends != 2 {
		t.Fatalf("end issued %d times, want 2 (explicit and cancellation)", ends)
	}
}

func TestProbeAgainstRealHTTPAdapter(t *testing.T) {
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
	session, stop, failure := connectHTTPAdapter(ctx, cfg)
	if failure != nil {
		t.Fatalf("HTTP connect failed: %s %v", failure.reason, failure.fields)
	}
	defer stop()
	reporter.pass("connect", "transport=http")
	probe := &probe{reporter: reporter, session: session, service: service}
	blocked := probe.runChecks(ctx)
	if code := reporter.finish(blocked); code != 0 {
		t.Fatalf("checks failed (exit %d):\n%s", code, output.String())
	}
	fake.mutex.Lock()
	starts, ends := fake.startCount, fake.endCount
	fake.mutex.Unlock()
	if starts != 2 || ends != 2 {
		t.Fatalf("starts=%d ends=%d, want 2/2", starts, ends)
	}
}
