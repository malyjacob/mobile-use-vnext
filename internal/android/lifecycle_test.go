package android

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureServer is an in-process uiautomator JSON-RPC endpoint that serves one
// hierarchy document and answers /ping. It writes nothing to any device.
type captureServer struct {
	*httptest.Server
	mu         sync.Mutex
	xml        string
	stopOnPing chan struct{}
}

// signalStopped makes the next ping answer as if the server process exited.
func (s *captureServer) signalStopped() { s.stopOnPing <- struct{}{} }

func newCaptureServer(t *testing.T, document string) *captureServer {
	t.Helper()
	server := &captureServer{xml: document, stopOnPing: make(chan struct{}, 8)}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/ping":
			select {
			case <-server.stopOnPing:
				// The modelled server process has exited.
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			default:
			}
			_, _ = io.WriteString(w, "pong")
		case r.Method == http.MethodPost && r.URL.Path == "/jsonrpc/0":
			server.mu.Lock()
			current := server.xml
			server.mu.Unlock()
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`+quoteJSON(current)+`}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server
}

// SetXML replaces the served hierarchy.
func (s *captureServer) SetXML(document string) {
	s.mu.Lock()
	s.xml = document
	s.mu.Unlock()
}

// quoteJSON encodes a string as a JSON string literal.
func quoteJSON(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

// TestEnsureU2StartsAndStopsWithStubTransport drives the server lifecycle
// against the stub adb process and an in-process endpoint.
func TestEnsureU2StartsAndStopsWithStubTransport(t *testing.T) {
	d := newStubDevice(t, "probe")
	server := newCaptureServer(t, captureXML)
	d.testBase = server.URL
	defer server.Close()
	d.localJarPath = makePinnedJar(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A stale shell record whose process already exited must be replaced.
	d.mu.Lock()
	stale := &serverProcess{done: make(chan struct{})}
	close(stale.done)
	d.server = stale
	d.mu.Unlock()

	if err := d.EnsureU2(ctx); err != nil {
		t.Fatalf("EnsureU2: %v", err)
	}
	d.mu.Lock()
	running := d.server != nil && d.server != stale
	d.mu.Unlock()
	if !running {
		t.Fatal("a started server must be tracked for StopU2")
	}
	if !d.U2Ready(ctx) {
		t.Fatal("the endpoint must answer while the server is tracked")
	}
	// Model the device process exiting when the local shell is released.
	server.signalStopped()

	stoppedAt := time.Now()
	if err := d.StopU2(ctx); err != nil {
		t.Fatalf("StopU2: %v", err)
	}
	if elapsed := time.Since(stoppedAt); elapsed > 9*time.Second {
		t.Fatalf("StopU2 waited for the full bound: %v", elapsed)
	}
	d.mu.Lock()
	released := d.server == nil
	d.mu.Unlock()
	if !released {
		t.Fatal("StopU2 must release the local process")
	}
}

// TestEnsureU2ClearsReadyStateOnCancellation proves a context end during
// startup releases the local shell instead of leaking it.
func TestEnsureU2ClearsReadyStateOnCancellation(t *testing.T) {
	d := newStubDevice(t, "probe")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	d.testBase = server.URL
	d.localJarPath = makePinnedJar(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := d.EnsureU2(ctx)
	if err == nil {
		t.Fatal("a server that never becomes ready must fail")
	}
	if CodeOf(err) != CodeServerStart && CodeOf(err) != CodeCancelled {
		t.Fatalf("code=%q", CodeOf(err))
	}
	d.mu.Lock()
	released := d.server == nil
	d.mu.Unlock()
	if !released {
		t.Fatal("a failed start must not leave the local shell tracked")
	}
}

// TestStopU2WithNoServerIsIdempotent ensures the idle stop only pkilis the
// fixed pattern.
func TestStopU2WithNoServerIsIdempotent(t *testing.T) {
	d := newStubDevice(t, "probe")
	if err := d.StopU2(t.Context()); err != nil {
		t.Fatalf("StopU2: %v", err)
	}
	if err := d.StopU2(t.Context()); err != nil {
		t.Fatalf("StopU2 twice: %v", err)
	}
}
