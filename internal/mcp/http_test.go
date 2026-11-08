package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// testBearerTransport adds the private bearer credential to every SDK request and
// optionally one fixed Origin header.
type testBearerTransport struct {
	base   http.RoundTripper
	token  string
	origin string
}

func (t *testBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	if t.origin != "" {
		clone.Header.Set("Origin", t.origin)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// testRecordingTransport observes whether any response used the SSE framing while
// delegating all HTTP behavior unchanged.
type testRecordingTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	sse  bool
}

func (t *testRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err == nil {
		if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			t.mu.Lock()
			t.sse = true
			t.mu.Unlock()
		}
	}
	return response, err
}

func (t *testRecordingTransport) sawSSE() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sse
}

func rawTestMCPRequest(t *testing.T, endpoint, token, origin string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("raw request: %v", err)
	}
	return response
}

func connectTestStreamable(t *testing.T, endpoint string, transport http.RoundTripper) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil).
		Connect(ctx, &sdk.StreamableClientTransport{
			Endpoint:   endpoint,
			HTTPClient: &http.Client{Transport: transport},
		}, nil)
	if err != nil {
		cancel()
		t.Fatalf("streamable connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
	})
	return session
}

func TestHTTPHandlerAuthOriginAndCatalog(t *testing.T) {
	fake := newTestDeviceService(t)
	server := newTestMCPServer(t, fake)
	const allowedOrigin = "https://allowed.example"
	handler := NewHTTPHandler(server, HTTPConfig{
		Token:          "secret-token",
		AllowedOrigins: []string{allowedOrigin + "/"},
	})
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	t.Run("missing token", func(t *testing.T) {
		response := rawTestMCPRequest(t, httpServer.URL, "", "")
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.StatusCode)
		}
		if response.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("WWW-Authenticate = %q", response.Header.Get("WWW-Authenticate"))
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		response := rawTestMCPRequest(t, httpServer.URL, "wrong-token", "")
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.StatusCode)
		}
	})

	t.Run("wrong origin", func(t *testing.T) {
		response := rawTestMCPRequest(t, httpServer.URL, "secret-token", "https://evil.example")
		defer response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", response.StatusCode)
		}
	})

	t.Run("allowed origin and 2026-07-28", func(t *testing.T) {
		recorder := &testRecordingTransport{}
		transport := &testBearerTransport{
			base:   recorder,
			token:  "secret-token",
			origin: allowedOrigin,
		}
		session := connectTestStreamable(t, httpServer.URL, transport)
		if version := session.InitializeResult().ProtocolVersion; version != protocolVersion {
			t.Fatalf("negotiated protocol = %q, want %q", version, protocolVersion)
		}
		ctx, cancel := context.WithTimeout(context.Background(), mcpTestTimeout)
		defer cancel()
		tools, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if len(tools.Tools) != len(toolCatalog) {
			t.Fatalf("tools over HTTP = %d, want %d", len(tools.Tools), len(toolCatalog))
		}
		if !recorder.sawSSE() {
			t.Fatal("Streamable HTTP responses were not SSE-framed")
		}
	})
}

func TestHTTPSSECancellationEndsOperation(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("act", fake.blockTestUntilStopped)
	fake.setHandler("end", testEndHandler())
	server := newTestMCPServer(t, fake)
	handler := NewHTTPHandler(server, HTTPConfig{Token: "secret-token"})
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	recorder := &testRecordingTransport{}
	session := connectTestStreamable(t, httpServer.URL, &testBearerTransport{
		base:  recorder,
		token: "secret-token",
	})
	listCtx, listCancel := context.WithTimeout(context.Background(), mcpTestTimeout)
	tools, err := session.ListTools(listCtx, nil)
	listCancel()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != len(toolCatalog) {
		t.Fatalf("tools over HTTP = %d", len(tools.Tools))
	}

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
	case err := <-callDone:
		if err == nil {
			t.Fatal("cancelled HTTP call returned no error")
		}
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled HTTP call did not return")
	}
	if len(fake.requestsFor("act")) != 1 {
		t.Fatal("cancelled act was dispatched more than once")
	}
	if !strings.Contains(endRequests[0].Body, operation) {
		t.Fatalf("end body %s does not reference %s", endRequests[0].Body, operation)
	}
	if !recorder.sawSSE() {
		t.Fatal("cancellation test did not use an SSE response")
	}
}
