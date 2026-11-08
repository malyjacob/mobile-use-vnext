package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	mcpTestToken   = "test-agent-token"
	mcpTestTimeout = 10 * time.Second
)

// testServiceRequest is one request the fake device service received.
type testServiceRequest struct {
	Tool        string
	OperationID string
	Body        string
}

// testDeviceService is an in-process stand-in for the device service. It records
// only the facts tests need and never touches a device.
type testDeviceService struct {
	t        *testing.T
	server   *httptest.Server
	stop     chan struct{}
	stopOnce sync.Once
	mu       sync.Mutex
	handlers map[string]func(http.ResponseWriter, *http.Request, string)
	requests []testServiceRequest
}

func newTestDeviceService(t *testing.T) *testDeviceService {
	t.Helper()
	fake := &testDeviceService{
		t:        t,
		stop:     make(chan struct{}),
		handlers: make(map[string]func(http.ResponseWriter, *http.Request, string)),
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.Close)
	return fake
}

func (f *testDeviceService) Close() {
	f.stopOnce.Do(func() {
		close(f.stop)
		f.server.Close()
	})
}

func (f *testDeviceService) setHandler(tool string, handler func(http.ResponseWriter, *http.Request, string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[tool] = handler
}

func (f *testDeviceService) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+mcpTestToken {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Header.Get("X-Mobile-Use-Contract") != protocol.ContractVersion {
		writer.WriteHeader(http.StatusConflict)
		return
	}
	if !strings.HasPrefix(request.URL.Path, "/api/") {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	tool := strings.TrimPrefix(request.URL.Path, "/api/")
	f.mu.Lock()
	f.requests = append(f.requests, testServiceRequest{
		Tool:        tool,
		OperationID: request.Header.Get("X-Mobile-Use-Operation"),
		Body:        string(body),
	})
	handler := f.handlers[tool]
	f.mu.Unlock()
	if handler == nil {
		writeTestJSON(writer, http.StatusOK, `{}`)
		return
	}
	handler(writer, request, string(body))
}

func (f *testDeviceService) requestsFor(tool string) []testServiceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []testServiceRequest
	for _, request := range f.requests {
		if request.Tool == tool {
			found = append(found, request)
		}
	}
	return found
}

func (f *testDeviceService) requestCount(tool string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request.Tool == tool {
			count++
		}
	}
	return count
}

func (f *testDeviceService) totalRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *testDeviceService) waitFor(t *testing.T, tool string, count int, timeout time.Duration) []testServiceRequest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		found := f.requestsFor(tool)
		if len(found) >= count {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d %s request(s); saw %d", count, tool, len(found))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *testDeviceService) blockTestUntilStopped(writer http.ResponseWriter, request *http.Request, body string) {
	select {
	case <-request.Context().Done():
	case <-f.stop:
	}
}

func writeTestJSON(writer http.ResponseWriter, status int, body string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, body)
}

func testOperationHeader(request *http.Request) string {
	return request.Header.Get("X-Mobile-Use-Operation")
}

func newTestDeviceClient(t *testing.T, fake *testDeviceService) *client.Client {
	t.Helper()
	service, err := client.New(client.Config{
		BaseURL:    fake.server.URL,
		AgentToken: mcpTestToken,
		Timeout:    mcpTestTimeout,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return service
}

func newTestMCPServer(t *testing.T, fake *testDeviceService) *sdk.Server {
	t.Helper()
	server, err := NewServer(Options{
		Client:  newTestDeviceClient(t, fake),
		Logger:  slog.New(slog.DiscardHandler),
		Version: "test",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

func connectTestInMemory(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverTransport) }()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("server did not stop")
		}
	})
	return session
}

func testCallContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), mcpTestTimeout)
}

func callTestTool(t *testing.T, session *sdk.ClientSession, name string, arguments any) *sdk.CallToolResult {
	t.Helper()
	ctx, cancel := testCallContext(t)
	defer cancel()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

func testTextContent(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("nil tool result")
	}
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			return text.Text
		}
	}
	t.Fatal("tool result has no TextContent")
	return ""
}

func testImageContents(result *sdk.CallToolResult) []*sdk.ImageContent {
	var images []*sdk.ImageContent
	for _, content := range result.Content {
		if image, ok := content.(*sdk.ImageContent); ok {
			images = append(images, image)
		}
	}
	return images
}

func testStructuredJSON(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	if result == nil || result.StructuredContent == nil {
		t.Fatal("tool result has no StructuredContent")
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return string(encoded)
}

func testStructuredObject(t *testing.T, result *sdk.CallToolResult) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(testStructuredJSON(t, result)), &object); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return object
}

func requireTestContains(t *testing.T, text, fragment string) {
	t.Helper()
	if !strings.Contains(text, fragment) {
		t.Fatalf("missing %q in:\n%s", fragment, text)
	}
}

func requireTestNotContains(t *testing.T, text, fragment string) {
	t.Helper()
	if strings.Contains(text, fragment) {
		t.Fatalf("unexpected %q in:\n%s", fragment, text)
	}
}

func testDefinitionByName(t *testing.T, name string) toolDefinition {
	t.Helper()
	for _, definition := range toolCatalog {
		if definition.name == name {
			return definition
		}
	}
	t.Fatalf("unknown tool definition %q", name)
	return toolDefinition{}
}

const (
	testActiveSessionJSON = `{"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",` +
		`"started_at":"2026-11-09T00:00:00Z","expires_at":"2026-11-09T00:02:00Z"}`
	testScreenJSON     = `{"width":1080,"height":2400,"rotation":0}`
	testForegroundJSON = `{"state":"present","package":"com.example.probe","task_id":12}`
	testFullTreeJSON   = `{"format":"full","scope":"reported_visible_with_context",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}`
	testDeltaTreeJSON = `{"format":"delta","base":"O1"}`
)

func TestToolCatalog(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	ctx, cancel := testCallContext(t)
	defer cancel()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := []string{
		"start", "status", "observe", "act", "inspect", "keepalive",
		"end", "apps", "open_app", "notify", "shell", "diagnose",
	}
	if len(list.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(list.Tools), len(want))
	}
	seen := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		seen[tool.Name] = true
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("missing tool %q", name)
		}
	}

	required := map[string][]string{
		"start":     {},
		"status":    {},
		"observe":   {"reference"},
		"act":       {"observation_id", "operation"},
		"inspect":   {"observation_id"},
		"keepalive": {"reference"},
		"end":       {"reference"},
		"apps":      {},
		"open_app":  {"observation_id", "app"},
		"notify":    {"text"},
		"shell":     {"reference", "command"},
		"diagnose":  {},
	}
	for _, tool := range list.Tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		var schema struct {
			Type                 string                     `json:"type"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatalf("%s: unmarshal schema: %v", tool.Name, err)
		}
		if schema.Type != "object" {
			t.Fatalf("%s: schema type = %q", tool.Name, schema.Type)
		}
		if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Fatalf("%s: schema must reject additional properties", tool.Name)
		}
		if schema.Required == nil {
			t.Fatalf("%s: schema has no required array", tool.Name)
		}
		if len(schema.Required) != len(required[tool.Name]) {
			t.Fatalf("%s: required = %v, want %v", tool.Name, schema.Required, required[tool.Name])
		}
		for index, name := range required[tool.Name] {
			if schema.Required[index] != name {
				t.Fatalf("%s: required = %v, want %v", tool.Name, schema.Required, required[tool.Name])
			}
		}
		for _, name := range required[tool.Name] {
			if _, ok := schema.Properties[name]; !ok {
				t.Fatalf("%s: required member %q is not declared", tool.Name, name)
			}
		}
	}

	capabilities := session.InitializeResult().Capabilities
	if capabilities == nil || capabilities.Tools == nil {
		t.Fatal("tools capability was not advertised")
	}
	if capabilities.Prompts != nil || capabilities.Resources != nil {
		t.Fatalf("unexpected prompts/resources capability: %+v", capabilities)
	}
}

func TestNewServerRequiresClient(t *testing.T) {
	if _, err := NewServer(Options{}); err == nil {
		t.Fatal("NewServer accepted a nil client")
	}
}

func TestStrictAdmission(t *testing.T) {
	cases := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "unknown member", tool: "start", arguments: map[string]any{"extra": true}},
		{name: "wrong type", tool: "observe", arguments: map[string]any{"reference": "O1", "wait_ms": "soon"}},
		{name: "explicit null", tool: "observe", arguments: map[string]any{"reference": "O1", "full": nil}},
		{name: "missing required", tool: "end", arguments: map[string]any{}},
		{name: "case variant", tool: "keepalive", arguments: map[string]any{"Reference": "O1"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := newTestDeviceService(t)
			session := connectTestInMemory(t, newTestMCPServer(t, fake))
			result := callTestTool(t, session, testCase.tool, testCase.arguments)
			if !result.IsError {
				t.Fatalf("%s: expected IsError", testCase.name)
			}
			text := testTextContent(t, result)
			requireTestContains(t, text, "invalid_request: ")
			structured := testStructuredObject(t, result)
			errorObject, ok := structured["error"].(map[string]any)
			if !ok || errorObject["code"] != "invalid_request" {
				t.Fatalf("%s: structured error = %#v", testCase.name, structured)
			}
			if fake.totalRequests() != 0 {
				t.Fatalf("%s: client was called for an invalid request", testCase.name)
			}
		})
	}

	t.Run("trailing value", func(t *testing.T) {
		fake := newTestDeviceService(t)
		adapter := &adapter{
			client: newTestDeviceClient(t, fake),
			logger: slog.New(slog.DiscardHandler),
		}
		result := adapter.handle(context.Background(), testDefinitionByName(t, "observe"),
			[]byte(`{"reference":"O1"} {"reference":"O2"}`))
		if !result.IsError {
			t.Fatal("trailing second value was accepted")
		}
		requireTestContains(t, testTextContent(t, result), "invalid_request: ")
		if fake.totalRequests() != 0 {
			t.Fatal("client was called for a trailing value")
		}
	})

	t.Run("duplicate key", func(t *testing.T) {
		fake := newTestDeviceService(t)
		adapter := &adapter{
			client: newTestDeviceClient(t, fake),
			logger: slog.New(slog.DiscardHandler),
		}
		result := adapter.handle(context.Background(), testDefinitionByName(t, "observe"),
			[]byte(`{"reference":"O1","reference":"O2"}`))
		if !result.IsError {
			t.Fatal("duplicate key was accepted")
		}
		if fake.totalRequests() != 0 {
			t.Fatal("client was called for a duplicate key")
		}
	})
}

func TestStartProjectionWithFullTree(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("start", func(writer http.ResponseWriter, request *http.Request, body string) {
		operation := testOperationHeader(request)
		writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
			`{"operation_id":%q,"outcome":"executed","session":`+testActiveSessionJSON+
				`,"observation":{"status":"complete","id":"O1","origin":%q,"revision":1,`+
				`"captured_at":"2026-11-09T00:00:01Z","actionable_until":"2026-11-09T00:01:01Z",`+
				`"screen":`+testScreenJSON+`,"foreground":`+testForegroundJSON+`,"tree":`+testFullTreeJSON+`}}`,
			operation, operation))
	})
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	result := callTestTool(t, session, "start", map[string]any{})
	if result.IsError {
		t.Fatalf("start reported an error: %s", testTextContent(t, result))
	}
	requests := fake.requestsFor("start")
	if len(requests) != 1 {
		t.Fatalf("start requests = %d", len(requests))
	}
	operation := requests[0].OperationID
	if !strings.HasPrefix(operation, "P") {
		t.Fatalf("start did not send an operation ID: %q", operation)
	}
	text := testTextContent(t, result)
	requireTestContains(t, text, "operation_id="+operation)
	requireTestContains(t, text, "outcome=executed")
	requireTestContains(t, text, "observation.status=complete")
	requireTestContains(t, text, "observation.id=O1")
	requireTestContains(t, text, "observation.tree.format=full")
	requireTestContains(t, text, "observation.screen.width=1080")
	requireTestContains(t, text, "observation.foreground.package=com.example.probe")
	requireTestNotContains(t, text, "n1")
	structured := testStructuredJSON(t, result)
	requireTestContains(t, structured, fmt.Sprintf(`"operation_id":%q`, operation))
	requireTestContains(t, structured, `"format":"full"`)
	if len(testImageContents(result)) != 0 {
		t.Fatal("start without image returned ImageContent")
	}
}

func TestObservePartialWithImage(t *testing.T) {
	fake := newTestDeviceService(t)
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02}
	encodedImage := base64.StdEncoding.EncodeToString(imageBytes)
	fake.setHandler("observe", func(writer http.ResponseWriter, request *http.Request, body string) {
		operation := testOperationHeader(request)
		writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
			`{"operation_id":%q,"observation":{"status":"partial","id":"O2","origin":"P1","revision":2,`+
				`"captured_at":"2026-11-09T00:00:02Z","actionable_until":"2026-11-09T00:01:02Z",`+
				`"screen":`+testScreenJSON+`,"foreground":`+testForegroundJSON+`,"tree":`+testDeltaTreeJSON+`,`+
				`"missing":["tree"],"image":{"captured_at":"2026-11-09T00:00:02Z","width":2,"height":3,`+
				`"media_type":"image/png","read_only":true,"base64":%q}}}`,
			operation, encodedImage))
	})
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	result := callTestTool(t, session, "observe", map[string]any{"reference": "O1", "image": true})
	if result.IsError {
		t.Fatalf("partial observation reported an error: %s", testTextContent(t, result))
	}
	operation := fake.requestsFor("observe")[0].OperationID
	text := testTextContent(t, result)
	requireTestContains(t, text, "operation_id="+operation)
	requireTestContains(t, text, "observation.status=partial")
	requireTestContains(t, text, "observation.missing=tree")
	requireTestContains(t, text, "observation.tree.format=delta")
	requireTestContains(t, text, "observation.tree.base=O1")
	requireTestContains(t, text, "observation.image.media_type=image/png")
	requireTestContains(t, text, "observation.image.width=2")
	requireTestContains(t, text, "observation.image.read_only=true")
	requireTestNotContains(t, text, "base64")
	requireTestNotContains(t, text, encodedImage)

	images := testImageContents(result)
	if len(images) != 1 {
		t.Fatalf("ImageContent count = %d", len(images))
	}
	if images[0].MIMEType != "image/png" {
		t.Fatalf("image MIME = %q", images[0].MIMEType)
	}
	if string(images[0].Data) != string(imageBytes) {
		t.Fatalf("image bytes differ: %x", images[0].Data)
	}
	structured := testStructuredJSON(t, result)
	requireTestNotContains(t, structured, "base64")
	requireTestNotContains(t, structured, encodedImage)
	requireTestContains(t, structured, `"media_type":"image/png"`)
}

func TestObserveFailedWithoutObservation(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("observe", func(writer http.ResponseWriter, request *http.Request, body string) {
		writeTestJSON(writer, http.StatusServiceUnavailable,
			`{"operation_id":`+fmt.Sprintf("%q", testOperationHeader(request))+
				`,"observation":{"status":"failed","missing":["tree"],`+
				`"error":{"code":"hierarchy_unavailable","message":"层级不可用"}},`+
				`"error":{"code":"hierarchy_unavailable","message":"层级不可用"},"next":"observe"}`)
	})
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	result := callTestTool(t, session, "observe", map[string]any{"reference": "O1"})
	if !result.IsError {
		t.Fatal("failed observation without an O should be an error")
	}
	text := testTextContent(t, result)
	requireTestContains(t, text, "observation.status=failed")
	requireTestContains(t, text, "error.code=hierarchy_unavailable")
}

func TestActOutcomes(t *testing.T) {
	t.Run("executed with failed post-observation", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("act", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusServiceUnavailable, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"executed","observation":{"status":"failed",`+
					`"missing":["tree"],"error":{"code":"hierarchy_unavailable","message":"层级不可用"}},`+
					`"error":{"code":"hierarchy_unavailable","message":"层级不可用"},"next":"observe"}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "act", map[string]any{
			"observation_id": "O1",
			"operation":      map[string]any{"type": "key", "key": "BACK"},
		})
		if result.IsError {
			t.Fatalf("executed act reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "outcome=executed")
		requireTestContains(t, text, "observation.status=failed")
	})

	t.Run("not executed", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("act", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusConflict, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"not_executed",`+
					`"error":{"code":"node_not_found","message":"节点不存在"}}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "act", map[string]any{
			"observation_id": "O1",
			"operation":      map[string]any{"type": "tap", "target": map[string]any{"ref": "n7"}},
		})
		if !result.IsError {
			t.Fatal("not_executed act should be an error")
		}
		requireTestContains(t, testTextContent(t, result), "outcome=not_executed")
	})
}

func TestReadOnlyToolProjections(t *testing.T) {
	t.Run("status not seen", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("status", func(writer http.ResponseWriter, request *http.Request, body string) {
			if testOperationHeader(request) != "" {
				t.Errorf("status used an operation ID")
			}
			writeTestJSON(writer, http.StatusOK, `{"operation_id":"P9","operation_state":"not_seen"}`)
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "status", map[string]any{})
		if result.IsError {
			t.Fatalf("status not_seen reported an error: %s", testTextContent(t, result))
		}
		requireTestContains(t, testTextContent(t, result), "operation_state=not_seen")
	})

	t.Run("diagnose record unavailable", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("diagnose", func(writer http.ResponseWriter, request *http.Request, body string) {
			if testOperationHeader(request) != "" {
				t.Errorf("diagnose used an operation ID")
			}
			writeTestJSON(writer, http.StatusOK, `{"diagnosis":{"summary":"近期记录不可用",`+
				`"issues":[],"coverage":"record_unavailable","truncated":false}}`)
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "diagnose", map[string]any{})
		if result.IsError {
			t.Fatalf("diagnose record_unavailable reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "diagnosis.coverage=record_unavailable")
		requireTestContains(t, text, "diagnosis.truncated=false")
	})

	t.Run("inspect content unavailable", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("inspect", func(writer http.ResponseWriter, request *http.Request, body string) {
			if testOperationHeader(request) != "" {
				t.Errorf("inspect used an operation ID")
			}
			writeTestJSON(writer, http.StatusConflict,
				`{"error":{"code":"content_unavailable","message":"树已被淘汰"},"next":"observe"}`)
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "inspect", map[string]any{"observation_id": "O2"})
		if !result.IsError {
			t.Fatal("inspect content_unavailable should be an error")
		}
		requireTestContains(t, testTextContent(t, result), "error.code=content_unavailable")
	})
}

func TestWorkToolProjections(t *testing.T) {
	t.Run("end pending cleanup", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("end", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"executed","target_reference":"P1",`+
					`"session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",`+
					`"started_at":"2026-11-09T00:00:00Z","ended_at":"2026-11-09T00:03:00Z","reason":"client_end"},`+
					`"cleanup":{"state":"unconfirmed","execution":"confirmed","tasks":"confirmed",`+
					`"recents":"not_applicable","display":"unconfirmed",`+
					`"error":{"code":"display_release_unconfirmed","message":"显示释放未确认"}},"next":"status"}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "end", map[string]any{"reference": "P1"})
		if result.IsError {
			t.Fatalf("end with pending cleanup reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "outcome=executed")
		requireTestContains(t, text, "target_reference=P1")
		requireTestContains(t, text, "cleanup.state=unconfirmed")
		requireTestContains(t, text, "cleanup.execution=confirmed")
		requireTestContains(t, text, "cleanup.display=unconfirmed")
		requireTestContains(t, text, "cleanup.error.code=display_release_unconfirmed")
		requireTestContains(t, text, "session.state=ended")
	})

	t.Run("shell exits nonzero", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("shell", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"executed",`+
					`"shell":{"termination":"exited","exit_code":1,"output":"boom","truncated":false}}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "shell", map[string]any{"reference": "O1", "command": "false"})
		if result.IsError {
			t.Fatalf("natural nonzero shell exit reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "shell.termination=exited")
		requireTestContains(t, text, "shell.exit_code=1")
		requireTestContains(t, text, "shell.output_bytes=4")
		requireTestContains(t, text, "shell.truncated=false")
		requireTestNotContains(t, text, "boom")
	})

	t.Run("notify unavailable", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("notify", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"executed","message_id":"M1","saved":true,`+
					`"notification":{"state":"unavailable","error":`+
					`{"code":"notification_unavailable","message":"通知不可用"}}}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "notify", map[string]any{"text": "hello"})
		if result.IsError {
			t.Fatalf("saved notify with unavailable delivery reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "message_id=M1")
		requireTestContains(t, text, "saved=true")
		requireTestContains(t, text, "notification.state=unavailable")
		requireTestNotContains(t, text, "hello")
	})

	t.Run("apps hides names", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("apps", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
				`{"operation_id":%q,"apps":{"items":[{"name":"设置","package":"com.android.settings"}],`+
					`"total":1,"truncated":false,"captured_at":"2026-11-09T00:00:04Z"}}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "apps", map[string]any{})
		if result.IsError {
			t.Fatalf("apps reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "apps.items_count=1")
		requireTestContains(t, text, "apps.total=1")
		requireTestContains(t, text, "apps.truncated=false")
		requireTestNotContains(t, text, "设置")
	})
}

// testEndHandler returns the standard end result used by cancellation tests.
func testEndHandler() func(http.ResponseWriter, *http.Request, string) {
	return func(writer http.ResponseWriter, request *http.Request, body string) {
		writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
			`{"operation_id":%q,"outcome":"executed","session":{"origin":"P1","state":"ended",`+
				`"mode":"virtual","agent":"local-agent","started_at":"2026-11-09T00:00:00Z",`+
				`"ended_at":"2026-11-09T00:03:00Z","reason":"client_end"},`+
				`"cleanup":{"state":"pending","execution":"confirmed","tasks":"pending",`+
				`"recents":"pending","display":"pending"},"next":"status"}`,
			testOperationHeader(request)))
	}
}

func TestCancellationWorkToolDirect(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("act", fake.blockTestUntilStopped)
	fake.setHandler("end", testEndHandler())
	adapter := &adapter{
		client: newTestDeviceClient(t, fake),
		logger: slog.New(slog.DiscardHandler),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan *execution, 1)
	go func() {
		resultChannel <- adapter.run(ctx, testDefinitionByName(t, "act"),
			[]byte(`{"observation_id":"O1","operation":{"type":"key","key":"BACK"}}`))
	}()
	actRequests := fake.waitFor(t, "act", 1, mcpTestTimeout)
	operation := actRequests[0].OperationID
	cancel()

	var execution *execution
	select {
	case execution = <-resultChannel:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled act did not return")
	}
	endRequests := fake.waitFor(t, "end", 1, mcpTestTimeout)
	if len(fake.requestsFor("act")) != 1 {
		t.Fatal("cancelled act was dispatched more than once")
	}
	if !strings.Contains(endRequests[0].Body, fmt.Sprintf("%q", operation)) {
		t.Fatalf("end body %s does not reference %s", endRequests[0].Body, operation)
	}
	if !execution.isError("act") {
		t.Fatal("cancelled act without a known result should be an error")
	}
	if execution.result.OperationID != operation {
		t.Fatalf("operation_id = %q, want %q", execution.result.OperationID, operation)
	}
	if execution.result.Error == nil || execution.result.Error.Code != "call_cancelled" {
		t.Fatalf("cancellation error = %#v", execution.result.Error)
	}
	if execution.result.Session == nil || execution.result.Session.State != "ended" {
		t.Fatalf("end session facts were not merged: %#v", execution.result.Session)
	}
	if execution.result.Cleanup == nil || execution.result.Cleanup.State != "pending" {
		t.Fatalf("end cleanup facts were not merged: %#v", execution.result.Cleanup)
	}
	text := renderText(execution.result, execution.cancelled, execution.cancellation)
	requireTestContains(t, text, "cancelled=true")
	requireTestContains(t, text, "call_cancelled")
	requireTestContains(t, text, operation)
	requireTestContains(t, text, "有界 end")
	requireTestContains(t, text, "不证明")
	requireTestContains(t, text, "cleanup.state=pending")
}

func TestCancellationReadOnlyDirect(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("status", fake.blockTestUntilStopped)
	adapter := &adapter{
		client: newTestDeviceClient(t, fake),
		logger: slog.New(slog.DiscardHandler),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan *execution, 1)
	go func() {
		resultChannel <- adapter.run(ctx, testDefinitionByName(t, "status"), []byte(`{}`))
	}()
	fake.waitFor(t, "status", 1, mcpTestTimeout)
	cancel()

	var execution *execution
	select {
	case execution = <-resultChannel:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled status did not return")
	}
	if fake.requestCount("end") != 0 {
		t.Fatal("read-only cancellation issued an end")
	}
	if !execution.isError("status") {
		t.Fatal("cancelled read-only without a result should be an error")
	}
	if execution.result.Error == nil || execution.result.Error.Code != "call_cancelled" {
		t.Fatalf("cancellation error = %#v", execution.result.Error)
	}
	text := renderText(execution.result, execution.cancelled, execution.cancellation)
	requireTestContains(t, text, "cancelled=true")
	requireTestContains(t, text, "只读工具未发起 end")
}

func TestCancellationInMemorySDK(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("act", fake.blockTestUntilStopped)
	fake.setHandler("end", testEndHandler())
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &sdk.CallToolParams{
			Name: "act",
			Arguments: map[string]any{
				"observation_id": "O1",
				"operation":      map[string]any{"type": "key", "key": "BACK"},
			},
		})
		callDone <- err
	}()
	actRequests := fake.waitFor(t, "act", 1, mcpTestTimeout)
	operation := actRequests[0].OperationID
	cancel()

	endRequests := fake.waitFor(t, "end", 1, mcpTestTimeout)
	select {
	case <-callDone:
	case <-time.After(mcpTestTimeout):
		t.Fatal("in-memory cancelled call did not return")
	}
	if len(fake.requestsFor("act")) != 1 {
		t.Fatal("cancelled act was dispatched more than once")
	}
	if !strings.Contains(endRequests[0].Body, fmt.Sprintf("%q", operation)) {
		t.Fatalf("end body %s does not reference %s", endRequests[0].Body, operation)
	}

	fake.setHandler("status", fake.blockTestUntilStopped)
	statusCtx, statusCancel := context.WithCancel(context.Background())
	statusDone := make(chan error, 1)
	go func() {
		_, err := session.CallTool(statusCtx, &sdk.CallToolParams{Name: "status", Arguments: map[string]any{}})
		statusDone <- err
	}()
	fake.waitFor(t, "status", 1, mcpTestTimeout)
	statusCancel()
	select {
	case <-statusDone:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled status did not return")
	}
	time.Sleep(100 * time.Millisecond)
	if fake.requestCount("end") != 1 {
		t.Fatal("read-only cancellation issued an end")
	}
}

// A structured service-level error has no query fact to report, so status and
// diagnose must stay errors even though a Result object was obtained.
func TestReadOnlyServiceErrorsAreErrors(t *testing.T) {
	serviceFailure := &execution{
		result:  &protocol.Result{Error: &protocol.Error{Code: protocol.CodeUnauthorized, Message: "认证失败"}},
		service: true,
	}
	for _, tool := range []string{"status", "diagnose"} {
		if !serviceFailure.isError(tool) {
			t.Fatalf("%s treated a service error as a query fact", tool)
		}
	}
	queryFact := &execution{result: &protocol.Result{OperationState: "not_seen"}, service: true}
	for _, tool := range []string{"status", "diagnose"} {
		if queryFact.isError(tool) {
			t.Fatalf("%s treated a query fact as an error", tool)
		}
	}
}
