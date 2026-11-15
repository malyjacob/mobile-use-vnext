package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// capturedWriter tees the adapter's stdio output so the test can inspect every
// byte after the MCP client has consumed it.
type capturedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	dst io.WriteCloser
}

func (w *capturedWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buf.Write(value)
	w.mu.Unlock()
	return w.dst.Write(value)
}

func (w *capturedWriter) Close() error { return w.dst.Close() }

func (w *capturedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestStdioFramingCarriesOnlyMCPFrames runs the registered adapter over the
// newline-delimited stdio transport and a fake device service. The captured
// stdout must contain only JSON-RPC frames; diagnostics may use the logger but
// never this stream.
func TestStdioFramingCarriesOnlyMCPFrames(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("status", func(writer http.ResponseWriter, _ *http.Request, _ string) {
		writeTestJSON(writer, http.StatusOK, `{"operation_state":"not_seen","can_start":true}`)
	})
	service := newTestDeviceClient(t, fake)
	server, err := NewServer(Options{
		Client: service,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	serverIn, serverInWriter := io.Pipe()
	serverOutReader, serverOut := io.Pipe()
	stdout := &capturedWriter{dst: serverOut}
	serverTransport := &sdk.IOTransport{Reader: serverIn, Writer: stdout}
	clientTransport := &sdk.IOTransport{Reader: serverOutReader, Writer: serverInWriter}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "stdio-test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v", err)
	}
	callCtx, callCancel := context.WithTimeout(context.Background(), mcpTestTimeout)
	result, err := session.CallTool(callCtx, &sdk.CallToolParams{
		Name: "status", Arguments: map[string]any{},
	})
	callCancel()
	if err != nil {
		_ = session.Close()
		cancel()
		t.Fatalf("status call: %v", err)
	}
	if result == nil || result.IsError {
		_ = session.Close()
		cancel()
		t.Fatalf("status result = %+v", result)
	}
	_ = session.Close()
	cancel()
	_ = serverInWriter.Close()
	_ = stdout.Close()
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stdio server did not stop")
	}

	stream := stdout.String()
	if strings.TrimSpace(stream) == "" {
		t.Fatal("stdio stdout contained no MCP frames")
	}
	for index, line := range strings.Split(strings.TrimSuffix(stream, "\n"), "\n") {
		if line == "" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("stdout line %d is not JSON: %q", index, line)
		}
		if frame["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line %d is not an MCP frame: %q", index, line)
		}
		if _, hasMethod := frame["method"]; !hasMethod {
			if _, hasID := frame["id"]; !hasID {
				t.Fatalf("stdout line %d is neither a request nor a response: %q", index, line)
			}
		}
		if strings.Contains(line, "mcp.tool") || strings.Contains(line, "duration_ms") {
			t.Fatalf("diagnostic log leaked to stdout: %q", line)
		}
	}
}
