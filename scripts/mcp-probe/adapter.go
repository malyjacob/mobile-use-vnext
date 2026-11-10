package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	connectTimeout   = 30 * time.Second
	listenerTimeout  = 10 * time.Second
	childStopTimeout = 5 * time.Second
	resultModeHeader = "X-Mobile-Use-Result-Mode"
)

// newMCPClient builds the probe's MCP client identity.
func newMCPClient() *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{
		Name:    "mobile-use-mcp-probe",
		Version: "1",
	}, nil)
}

// connectStdioAdapter starts the adapter with the stdio transport and performs
// the MCP initialize handshake. The returned stop function closes the session
// and guarantees the child process is gone.
func connectStdioAdapter(ctx context.Context, binaryPath, configPath,
	resultMode string) (*sdk.ClientSession, func(), *checkError) {
	command := exec.Command(binaryPath, stdioAdapterArgs(configPath, resultMode)...)
	command.Stderr = io.Discard
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	session, err := newMCPClient().Connect(connectCtx, &sdk.CommandTransport{Command: command}, nil)
	if err != nil {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		return nil, nil, failf("stdio_connect_failed")
	}
	stop := func() {
		_ = session.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}
	return session, stop, nil
}

func stdioAdapterArgs(configPath, resultMode string) []string {
	return []string{
		"-transport", "stdio",
		"-config", configPath,
		"-result-mode", resultMode,
	}
}

// bearerTransport adds the private host-side MCP credential to every request.
type bearerTransport struct {
	base       http.RoundTripper
	token      string
	resultMode string
}

func (t *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set(resultModeHeader, t.resultMode)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// childProcess is one adapter process started by the probe (HTTP transport).
// It owns the Wait call so the process is always reaped.
type childProcess struct {
	command *exec.Cmd
	done    chan struct{}
}

// startHTTPChild starts the adapter with the HTTP transport and waits up to
// ten seconds for its loopback TCP listener. A child that exits early is
// reported as a connect failure.
func startHTTPChild(ctx context.Context, binaryPath, configPath, listen string) (*childProcess, *checkError) {
	command := exec.Command(binaryPath, "-transport", "http", "-config", configPath)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, failf("http_child_start_failed")
	}
	child := &childProcess{command: command, done: make(chan struct{})}
	go func() {
		_ = command.Wait()
		close(child.done)
	}()
	if !child.waitListening(ctx, listen) {
		child.stop()
		return nil, failf("http_listen_failed", field("listen", listen))
	}
	return child, nil
}

func (c *childProcess) waitListening(ctx context.Context, listen string) bool {
	deadline := time.Now().Add(listenerTimeout)
	for {
		connection, err := net.DialTimeout("tcp", listen, 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return true
		}
		select {
		case <-c.done:
			return false
		case <-ctx.Done():
			return false
		default:
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *childProcess) stop() {
	if c.command != nil && c.command.Process != nil {
		_ = c.command.Process.Kill()
	}
	select {
	case <-c.done:
	case <-time.After(childStopTimeout):
	}
}

// connectHTTPAdapter connects the MCP client to the already listening HTTP
// adapter. The endpoint is loopback-only and the private bearer token is added
// by the transport, never by a URL.
func connectHTTPAdapter(ctx context.Context, cfg *config,
	resultMode string) (*sdk.ClientSession, func(), *checkError) {
	endpoint := "http://" + cfg.HTTP.Listen + "/mcp"
	httpClient := &http.Client{
		Transport: &bearerTransport{
			base:       http.DefaultTransport,
			token:      cfg.HTTP.Token,
			resultMode: resultMode,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	transport := &sdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	session, err := newMCPClient().Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, nil, failf("http_connect_failed")
	}
	return session, func() { _ = session.Close() }, nil
}
