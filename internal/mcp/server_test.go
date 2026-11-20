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
	"reflect"
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
	// Observations always deliver the compact view; historical inspection
	// material is always detailed, so the tree scope of an inspection response
	// must carry the detailed view in its nested complete tree.
	testFullTreeJSON = `{"format":"full","scope":"reported_visible_with_context",` +
		`"view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}`
	testDetailedTreeJSON = `{"format":"full","scope":"reported_visible_with_context",` +
		`"view":"detailed",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}`
	testDeltaTreeJSON = `{"format":"delta","base":"O1","view":"compact"}`
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
		"start", "status", "observe", "snap", "wait_for", "act", "inspect", "keepalive",
		"end", "app", "notify", "shell", "diagnose",
	}
	if len(list.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(list.Tools), len(want))
	}
	snapshot := CatalogSnapshot()
	if len(snapshot) != len(want) {
		t.Fatalf("catalog snapshot has %d tools, want %d", len(snapshot), len(want))
	}
	for index, name := range want {
		if snapshot[index].Name != name {
			t.Fatalf("catalog snapshot %d = %q, want %q", index, snapshot[index].Name, name)
		}
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
		"snap":      {"reference"},
		"wait_for":  {"reference", "timeout"},
		"act":       {"reference", "operation"},
		"inspect":   {"reference"},
		"keepalive": {"reference"},
		"end":       {"reference"},
		"app":       {"kind"},
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
		if rawReference, ok := schema.Properties["reference"]; ok {
			var reference struct {
				Pattern     string `json:"pattern"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(rawReference, &reference); err != nil {
				t.Fatalf("%s: reference schema: %v", tool.Name, err)
			}
			wantPattern := "^[OP]"
			if tool.Name == "act" || tool.Name == "inspect" {
				wantPattern = "^O"
			}
			if reference.Pattern != wantPattern {
				t.Errorf("%s: reference pattern = %q, want %q", tool.Name, reference.Pattern, wantPattern)
			}
			if tool.Name == "act" {
				for _, description := range []string{tool.Description, reference.Description} {
					if !strings.Contains(description, "静态拒绝不认领 O") ||
						!strings.Contains(description, "消费") {
						t.Error("act description must distinguish static rejection from consumption on claim")
					}
				}
			}
		}
		if tool.Name == "keepalive" {
			var duration struct {
				Type    string `json:"type"`
				Minimum int    `json:"minimum"`
				Maximum int    `json:"maximum"`
				Default int    `json:"default"`
			}
			if err := json.Unmarshal(schema.Properties["duration_minutes"], &duration); err != nil {
				t.Fatalf("duration schema: %v", err)
			}
			if duration.Type != "integer" || duration.Minimum != 1 || duration.Maximum != 120 || duration.Default != 2 {
				t.Fatalf("duration schema does not match admission: %+v", duration)
			}
		}
		if tool.Name == "app" {
			testAppSchemaShape(t, encoded)
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

// TestActScrollSchema pins the contract-10 act surface: scroll is a declared
// branch, container has target's selector shape, direction/amount are bounded
// enums, duration_ms is the swipe-only bounded integer, and the public
// description explains scroll instead of raw swipe.
func TestActScrollSchema(t *testing.T) {
	definition := testDefinitionByName(t, "act")
	var top struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(definition.schema, &top); err != nil {
		t.Fatalf("act schema: %v", err)
	}
	var operation struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(top.Properties["operation"], &operation); err != nil {
		t.Fatalf("act operation schema: %v", err)
	}
	enumOf := func(raw json.RawMessage) []string {
		t.Helper()
		var value struct {
			Enum []string `json:"enum"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("enum schema: %v", err)
		}
		return value.Enum
	}
	if got, want := enumOf(operation.Properties["type"]), []string{"tap", "long_press", "text", "swipe", "scroll", "key"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operation.type enum = %v, want %v", got, want)
	}
	if got, want := enumOf(operation.Properties["direction"]), []string{"up", "down", "left", "right"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("direction enum = %v, want %v", got, want)
	}
	var amount struct {
		Enum    []string `json:"enum"`
		Default string   `json:"default"`
	}
	if err := json.Unmarshal(operation.Properties["amount"], &amount); err != nil {
		t.Fatalf("amount schema: %v", err)
	}
	if !reflect.DeepEqual(amount.Enum, []string{"page", "half"}) || amount.Default != "page" {
		t.Fatalf("amount schema = %+v", amount)
	}
	var duration struct {
		Type    string `json:"type"`
		Minimum int    `json:"minimum"`
		Maximum int    `json:"maximum"`
	}
	if err := json.Unmarshal(operation.Properties["duration_ms"], &duration); err != nil {
		t.Fatalf("duration_ms schema: %v", err)
	}
	if duration.Type != "integer" || duration.Minimum != protocol.MinSwipeDurationMS || duration.Maximum != protocol.MaxSwipeDurationMS {
		t.Fatalf("duration_ms schema = %+v", duration)
	}
	// bubble and press_enter are branch-qualified booleans. Publishing a
	// default here would read as applying to coordinates or other actions.
	for member, branch := range map[string]string{"bubble": "tap/long_press", "press_enter": "text"} {
		var option struct {
			Type        string `json:"type"`
			Description string `json:"description"`
			Default     *bool  `json:"default"`
		}
		if err := json.Unmarshal(operation.Properties[member], &option); err != nil {
			t.Fatalf("%s schema: %v", member, err)
		}
		if option.Type != "boolean" || option.Default != nil || !strings.Contains(option.Description, branch) {
			t.Fatalf("%s schema = %+v, want a branch-qualified boolean without default", member, option)
		}
	}
	var target, container struct {
		Description string          `json:"description"`
		OneOf       json.RawMessage `json:"oneOf"`
	}
	if err := json.Unmarshal(operation.Properties["target"], &target); err != nil {
		t.Fatalf("target schema: %v", err)
	}
	if err := json.Unmarshal(operation.Properties["container"], &container); err != nil {
		t.Fatalf("container schema: %v", err)
	}
	var targetShape, containerShape any
	if err := json.Unmarshal(target.OneOf, &targetShape); err != nil {
		t.Fatalf("target oneOf: %v", err)
	}
	if err := json.Unmarshal(container.OneOf, &containerShape); err != nil {
		t.Fatalf("container oneOf: %v", err)
	}
	if !reflect.DeepEqual(targetShape, containerShape) {
		t.Fatal("container selector shape differs from the target selector shape")
	}
	requireTestContains(t, container.Description, "role=scroll")
	for _, fragment := range []string{
		"operation.type=scroll + container",
		"swipe 是原始手势",
		"树节点的 `swipe` 仅表示源能力",
		"scroll 不承诺真实方向、触摸消费者或业务安全",
		"按想滚动的区域选对应容器 ref",
		"effect.state",
		"observed/not_observed/unknown",
		"执行事实见 outcome",
		"observation.quality 不阻止输入",
	} {
		requireTestContains(t, definition.description, fragment)
	}
}

// TestActScrollForwarded proves the admitted scroll branch reaches the device
// service with the canonical container/direction/amount body and that the
// returned effect/quality are projected to both text and structured forms.
func TestActScrollForwarded(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("act", func(writer http.ResponseWriter, request *http.Request, body string) {
		writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
			`{"operation_id":%q,"outcome":"executed","phase":"completed",`+
				`"effect":{"state":"observed"},"target_reference":"O1",`+
				`"observation":{"status":"complete","id":"O2","origin":%q,"revision":2,`+
				`"quality":{"state":"unstable","samples":3,"stop_reason":"max_samples"}}}`,
			testOperationHeader(request), testOperationHeader(request)))
	})
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	result := callTestTool(t, session, "act", map[string]any{
		"reference": "O1",
		"operation": map[string]any{
			"type":      "scroll",
			"container": map[string]any{"ref": "n372"},
			"direction": "down",
			"amount":    "page",
		},
	})
	if result.IsError {
		t.Fatalf("scroll act reported an error: %s", testTextContent(t, result))
	}
	requests := fake.requestsFor("act")
	wantBody := `{"reference":"O1","operation":{"type":"scroll",` +
		`"container":{"ref":"n372"},"direction":"down","amount":"page"},"image":false}`
	if len(requests) != 1 || requests[0].Body != wantBody {
		t.Fatalf("scroll body changed: %+v", requests)
	}
	text := testTextContent(t, result)
	requireTestContains(t, text, "effect.state=observed")
	requireTestContains(t, text, " quality.state=unstable")
	requireTestContains(t, text, " quality.samples=3")
	requireTestContains(t, text, " quality.stop_reason=max_samples")
	structured := testStructuredObject(t, result)
	effect, _ := structured["effect"].(map[string]any)
	if effect["state"] != "observed" {
		t.Fatalf("structured effect = %#v", structured["effect"])
	}
	observation, _ := structured["observation"].(map[string]any)
	quality, _ := observation["quality"].(map[string]any)
	if quality["state"] != "unstable" || quality["samples"] != float64(3) ||
		quality["stop_reason"] != "max_samples" {
		t.Fatalf("structured quality = %#v", observation["quality"])
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
		{name: "retired act observation_id", tool: "act", arguments: map[string]any{
			"observation_id": "O1",
			"operation":      map[string]any{"type": "key", "key": "BACK"},
		}},
		{name: "retired inspect observation_id", tool: "inspect", arguments: map[string]any{"observation_id": "O1"}},
		{name: "wrong type", tool: "observe", arguments: map[string]any{"reference": "O1", "wait_ms": "soon"}},
		{name: "explicit null", tool: "observe", arguments: map[string]any{"reference": "O1", "full": nil}},
		{name: "missing required", tool: "end", arguments: map[string]any{}},
		{name: "case variant", tool: "keepalive", arguments: map[string]any{"Reference": "O1"}},
		{name: "null duration", tool: "keepalive", arguments: map[string]any{"reference": "O1", "duration_minutes": nil}},
		{name: "zero duration", tool: "keepalive", arguments: map[string]any{"reference": "O1", "duration_minutes": 0}},
		{name: "duration above maximum", tool: "keepalive", arguments: map[string]any{"reference": "O1", "duration_minutes": 121}},
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
			requireTestContains(t, text, "result v3 is_error=true\nerror.code=invalid_request")
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
		requireTestContains(t, testTextContent(t, result), "result v3 is_error=true\nerror.code=invalid_request")
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

func TestKeepaliveDurationForwarding(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("keepalive", func(w http.ResponseWriter, request *http.Request, _ string) {
		writeTestJSON(w, http.StatusOK, fmt.Sprintf(`{"operation_id":%q,"outcome":"executed","session":%s}`,
			testOperationHeader(request), testActiveSessionJSON))
	})
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	for index, tc := range []struct {
		name      string
		arguments map[string]any
		body      string
	}{
		{"omitted", map[string]any{"reference": "O1"}, `{"reference":"O1"}`},
		{"minimum", map[string]any{"reference": "O1", "duration_minutes": 1}, `{"reference":"O1","duration_minutes":1}`},
		{"maximum", map[string]any{"reference": "O1", "duration_minutes": 120}, `{"reference":"O1","duration_minutes":120}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := callTestTool(t, session, "keepalive", tc.arguments)
			if result.IsError {
				t.Fatalf("keepalive failed: %s", testTextContent(t, result))
			}
			requests := fake.requestsFor("keepalive")
			if len(requests) != index+1 || requests[index].Body != tc.body {
				t.Fatalf("duration presence/value changed: %+v", requests)
			}
			request := requests[index]
			structured := testStructuredObject(t, result)
			if !strings.HasPrefix(request.OperationID, "P") || structured["operation_id"] != request.OperationID ||
				structured["outcome"] != "executed" || len(structured) != 3 {
				t.Fatalf("keepalive result shape/P changed: %#v", structured)
			}
			sessionView, ok := structured["session"].(map[string]any)
			if !ok || sessionView["expires_at"] != "2026-11-09T00:02:00Z" {
				t.Fatalf("session deadline lost: %#v", structured)
			}
			requireTestContains(t, testTextContent(t, result), "session.expires_at=2026-11-09T00:02:00Z")
		})
	}
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
	requireTestContains(t, text, "\nobservation\n status=complete\n id=O1\n")
	requireTestContains(t, text, "\n screen="+testScreenJSON+"\n")
	requireTestContains(t, text, "\n foreground="+testForegroundJSON+"\n")
	requireTestContains(t, text, "\n\ntree v4 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\n")
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
	requireTestContains(t, text, "\nobservation\n status=partial\n")
	requireTestContains(t, text, "\n missing=[\"tree\"]\n")
	requireTestContains(t, text, "\n\ntree v4 delta base=O1 changes=0\n\nend tree\n")
	requireTestContains(t, text, "\"media_type\":\"image/png\"")
	requireTestContains(t, text, "\"width\":2")
	requireTestContains(t, text, "\"read_only\":true")
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
				`"error":{"code":"hierarchy_unavailable","message":"层级不可用"},`+
				`"recovery":{"entry":"observe","reference":"P"}}`)
	})
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	result := callTestTool(t, session, "observe", map[string]any{"reference": "O1"})
	if !result.IsError {
		t.Fatal("failed observation without an O should be an error")
	}
	text := testTextContent(t, result)
	requireTestContains(t, text, "\nobservation\n status=failed\n")
	requireTestContains(t, text, "error.code=hierarchy_unavailable")
}

func TestActOutcomes(t *testing.T) {
	t.Run("executed with failed post-observation", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("act", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusServiceUnavailable, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"executed","observation":{"status":"failed",`+
					`"missing":["tree"],"error":{"code":"hierarchy_unavailable","message":"层级不可用"}},`+
					`"error":{"code":"hierarchy_unavailable","message":"层级不可用"},`+
					`"recovery":{"entry":"observe","reference":"P","input_state":"retired",`+
					`"cause":"post_observation_failed"}}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "act", map[string]any{
			"reference": "O1",
			"operation": map[string]any{"type": "key", "key": "BACK"},
		})
		if result.IsError {
			t.Fatalf("executed act reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "outcome=executed")
		requireTestContains(t, text, "\nobservation\n status=failed\n")
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
			"reference": "O1",
			"operation": map[string]any{"type": "tap", "target": map[string]any{"ref": "n7"}},
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
				`{"error":{"code":"content_unavailable","message":"树已被淘汰"},`+
					`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`)
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "inspect", map[string]any{"reference": "O2"})
		if !result.IsError {
			t.Fatal("inspect content_unavailable should be an error")
		}
		requireTestContains(t, testTextContent(t, result), "error.code=content_unavailable")
	})
}

func TestWorkToolProjections(t *testing.T) {
	t.Run("end unconfirmed cleanup remains a success fact", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("end", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
				`{"operation_id":%q,"outcome":"executed","target_reference":"P1",`+
					`"session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent",`+
					`"started_at":"2026-11-09T00:00:00Z","ended_at":"2026-11-09T00:03:00Z","reason":"client_end"},`+
					`"cleanup":{"state":"unconfirmed","execution":"confirmed","tasks":"confirmed",`+
					`"recents":"not_applicable","display":"unconfirmed",`+
					`"error":{"code":"display_release_unconfirmed","message":"显示释放未确认"}}}`,
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
		requireTestContains(t, text, "cleanup.error={\"code\":\"display_release_unconfirmed\"")
		requireTestContains(t, text, "session.state=ended")
		if strings.Contains(text, "recovery.entry=") {
			t.Fatalf("unconfirmed cleanup still requested recovery: %s", text)
		}
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
		requireTestContains(t, text, "shell v1 termination=exited exit_code=1 truncated=false output_bytes=4")
		requireTestContains(t, text, "output \"boom\"")
		// A managed command publishes no observation.
		requireTestNotContains(t, text, "observation")
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

	t.Run("app find includes names", func(t *testing.T) {
		fake := newTestDeviceService(t)
		fake.setHandler("app", func(writer http.ResponseWriter, request *http.Request, body string) {
			writeTestJSON(writer, http.StatusOK, fmt.Sprintf(
				`{"operation_id":%q,"app":{"kind":"find","items":[{"name":"设置","package":"com.android.settings"}],`+
					`"total":1,"truncated":false,"captured_at":"2026-11-09T00:00:04Z"}}`,
				testOperationHeader(request)))
		})
		session := connectTestInMemory(t, newTestMCPServer(t, fake))
		result := callTestTool(t, session, "app", map[string]any{"kind": "find"})
		if result.IsError {
			t.Fatalf("app find reported an error: %s", testTextContent(t, result))
		}
		text := testTextContent(t, result)
		requireTestContains(t, text, "app.kind=find")
		requireTestContains(t, text, `app.items=[{"name":"设置","package":"com.android.settings"}]`)
		requireTestContains(t, text, "app.total=1")
		requireTestContains(t, text, "app.truncated=false")
		requireTestContains(t, text, "app.captured_at=2026-11-09T00:00:04Z")
		requireTestNotContains(t, text, "outcome=")
		requests := fake.requestsFor("app")
		if len(requests) != 1 || requests[0].Body != `{"kind":"find"}` {
			t.Fatalf("app body = %+v", requests)
		}
		if !strings.HasPrefix(requests[0].OperationID, "P") {
			t.Fatal("app find did not allocate a P")
		}
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
				`"recents":"pending","display":"pending"},`+
				`"recovery":{"entry":"status","reference":"P","input_state":"revoked",`+
				`"cause":"cleanup_pending"}}`,
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
			[]byte(`{"reference":"O1","operation":{"type":"key","key":"BACK"}}`))
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
	if recovery := execution.result.Recovery; recovery == nil ||
		recovery.Entry != protocol.RecoveryEntryStatus || recovery.Reference != protocol.RecoveryReferenceP {
		t.Fatalf("cancelled act recovery = %#v, want status/P", recovery)
	}
	if execution.result.Session == nil || execution.result.Session.State != "ended" {
		t.Fatalf("end session facts were not merged: %#v", execution.result.Session)
	}
	if execution.result.Cleanup == nil || execution.result.Cleanup.State != "pending" {
		t.Fatalf("end cleanup facts were not merged: %#v", execution.result.Cleanup)
	}
	text := testTextContent(t, projectExecution(execution, "act", ResultModeBoth))
	requireTestContains(t, text, "\"mcp.cancelled\":true")
	requireTestContains(t, text, "call_cancelled")
	requireTestContains(t, text, operation)
	requireTestContains(t, text, "有界 end")
	requireTestContains(t, text, "不证明")
	requireTestContains(t, text, "cleanup.state=pending")
}

func TestCancellationReadOnlyDirect(t *testing.T) {
	for _, test := range readOnlyRecoveryCases {
		t.Run(test.name, func(t *testing.T) {
			fake := newTestDeviceService(t)
			fake.setHandler(test.tool, fake.blockTestUntilStopped)
			adapter := &adapter{client: newTestDeviceClient(t, fake)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resultChannel := make(chan *execution, 1)
			go func() {
				resultChannel <- adapter.run(ctx, testDefinitionByName(t, test.tool), []byte(test.args))
			}()
			requests := fake.waitFor(t, test.tool, 1, mcpTestTimeout)
			cancel()

			var execution *execution
			select {
			case execution = <-resultChannel:
			case <-time.After(mcpTestTimeout):
				t.Fatal("cancelled query did not return")
			}
			if requests[0].OperationID != "" || fake.requestCount("end") != 0 {
				t.Fatal("read-only cancellation assigned a P or issued an end")
			}
			requireReadOnlyFailureWithoutRecovery(t, execution, test.tool, "call_cancelled")
		})
	}
}

var readOnlyRecoveryCases = []struct{ name, tool, args string }{
	{"status_self", "status", `{}`},
	{"status_operation", "status", `{"reference":"P7"}`},
	{"inspect", "inspect", `{"reference":"O7"}`},
	{"diagnose", "diagnose", `{}`},
}

func TestReadOnlyTransportFailureOmitsRecovery(t *testing.T) {
	fake := newTestDeviceService(t)
	adapter := &adapter{client: newTestDeviceClient(t, fake)}
	fake.Close()
	for _, test := range readOnlyRecoveryCases {
		t.Run(test.name, func(t *testing.T) {
			execution := adapter.run(context.Background(), testDefinitionByName(t, test.tool), []byte(test.args))
			requireReadOnlyFailureWithoutRecovery(t, execution, test.tool, client.ErrorTransport)
		})
	}
}

func TestTransportFailureKeepsAllocatedOperationRecovery(t *testing.T) {
	fake := newTestDeviceService(t)
	adapter := &adapter{client: newTestDeviceClient(t, fake)}
	fake.Close()
	for _, test := range []struct{ tool, args string }{
		{"act", `{"reference":"O1","operation":{"type":"key","key":"BACK"}}`},
		{"snap", `{"reference":"O1"}`},
		{"app", `{"kind":"find"}`},
	} {
		t.Run(test.tool, func(t *testing.T) {
			execution := adapter.run(context.Background(), testDefinitionByName(t, test.tool), []byte(test.args))
			result := execution.result
			if result.Error == nil || result.Error.Code != client.ErrorTransport || !execution.isError(test.tool) {
				t.Fatalf("unexpected transport result: %+v", result)
			}
			if !strings.HasPrefix(result.OperationID, "P") {
				t.Fatalf("transport result lost the allocated P: %q", result.OperationID)
			}
			if recovery := result.Recovery; recovery == nil ||
				recovery.Entry != protocol.RecoveryEntryStatus || recovery.Reference != protocol.RecoveryReferenceP {
				t.Fatalf("transport recovery = %#v, want status/P", recovery)
			}
		})
	}
}

func TestReadOnlyServiceRecoveryIsPreserved(t *testing.T) {
	serviceRecovery := protocol.Recovery{Entry: protocol.RecoveryEntryStatus,
		Reference: protocol.RecoveryReferenceP, Cause: protocol.RecoveryCauseRunning}
	known := callOutcome{response: &client.Response{Result: &protocol.Result{
		OperationID: "P7", OperationState: "running", Recovery: &serviceRecovery,
	}}}
	adapter := &adapter{}
	for _, test := range []struct {
		name string
		call *execution
	}{
		{"service_result", projectCall(known.response, nil, "", "")},
		{"cancelled_with_service_result", adapter.cancelled(&preparedCall{readOnly: true}, "", nil, known, true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := projectExecution(test.call, "status", ResultModeBoth)
			text := testTextContent(t, out)
			requireTestContains(t, text, "recovery.entry=status")
			requireTestContains(t, text, "recovery.reference=P")
			obj := testStructuredObject(t, out)
			recovery, _ := obj["recovery"].(map[string]any)
			if obj["operation_id"] != "P7" || recovery["entry"] != "status" || recovery["reference"] != "P" || recovery["cause"] != "running" {
				t.Fatalf("original service recovery or operation changed: %v", obj)
			}
		})
	}
}

func requireReadOnlyFailureWithoutRecovery(t *testing.T, e *execution, tool, code string) {
	t.Helper()
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeStructured, ResultModeText} {
		t.Run(mode.String(), func(t *testing.T) {
			result := projectExecution(e, tool, mode)
			if !result.IsError {
				t.Fatal("failed query was reported as successful")
			}
			text := testTextContent(t, result)
			requireTestContains(t, text, "error.code="+code)
			requireTestNotContains(t, text, "operation_id=")
			requireTestNotContains(t, text, "recovery.")
			if code == "call_cancelled" {
				requireTestContains(t, text, `"mcp.cancelled":true`)
				requireTestContains(t, text, cancellationReadOnlyMessage)
			}
			if mode != ResultModeText {
				object := testStructuredObject(t, result)
				for _, field := range []string{"operation_id", "recovery"} {
					if _, present := object[field]; present {
						t.Errorf("unexpected result field %q", field)
					}
				}
				err, _ := object["error"].(map[string]any)
				if err["code"] != code {
					t.Errorf("error.code = %v, want %q", err["code"], code)
				}
				if code == "call_cancelled" {
					details, _ := err["details"].(map[string]any)
					if details["mcp.cancelled"] != true || details["mcp.cancellation"] != cancellationReadOnlyMessage {
						t.Error("cancellation details were not preserved")
					}
				}
			}
		})
	}
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
				"reference": "O1",
				"operation": map[string]any{"type": "key", "key": "BACK"},
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
