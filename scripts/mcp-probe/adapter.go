package main

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	connectTimeout   = 30 * time.Second
	listenerTimeout  = 10 * time.Second
	childStopTimeout = 5 * time.Second
	resultModeHeader = "X-Mobile-Use-Result-Mode"

	// compatibilityProtocolVersion is the newest handshake-era protocol
	// version. A client pinned to it takes the legacy initialize handshake
	// instead of server/discover, so the probe can verify that both discovery
	// paths publish the same server instructions.
	compatibilityProtocolVersion = "2025-11-25"
)

// newMCPClient builds the probe's MCP client identity.
func newMCPClient() *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{
		Name:    "mobile-use-mcp-probe",
		Version: "1",
	}, nil)
}

// compatibilityStdioInitialize opens a short-lived stdio child pinned to the
// compatibility protocol version and returns its initialize result. The child
// performs no tool call.
func compatibilityStdioInitialize(binaryPath, configPath, resultMode string) func(context.Context) (*sdk.InitializeResult, *checkError) {
	return func(ctx context.Context) (*sdk.InitializeResult, *checkError) {
		session, stop, failure := connectStdioAdapterVersion(ctx, binaryPath, configPath, resultMode,
			&sdk.ClientSessionOptions{ProtocolVersion: compatibilityProtocolVersion})
		if failure != nil {
			return nil, failure
		}
		defer stop()
		return session.InitializeResult(), nil
	}
}

// compatibilityHTTPInitialize sends one initialize request to the stateless HTTP
// endpoint. A full legacy session cannot complete over stateless Streamable HTTP
// because the initialized notification arrives in a separate request with no
// session state; the single initialize response is the compatibility fact the
// adapter publishes, and the probe reads exactly that.
func compatibilityHTTPInitialize(cfg *config) func(context.Context) (*sdk.InitializeResult, *checkError) {
	return func(ctx context.Context) (*sdk.InitializeResult, *checkError) {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` +
			`"protocolVersion":"` + compatibilityProtocolVersion + `","capabilities":{},` +
			`"clientInfo":{"name":"mobile-use-mcp-probe","version":"1"}}}`
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://"+cfg.HTTP.Listen+"/mcp", strings.NewReader(body))
		if err != nil {
			return nil, failf("compat_request_invalid")
		}
		request.Header.Set("Authorization", "Bearer "+cfg.HTTP.Token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		client := &http.Client{
			Transport: &bearerTransport{base: http.DefaultTransport, token: cfg.HTTP.Token},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, failf("compat_request_failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, failf("compat_status_unexpected", field("status", strconv.Itoa(response.StatusCode)))
		}
		return readInitializeResponse(response)
	}
}

// initializeResponse is the subset of the JSON-RPC initialize result the probe
// needs. It never carries tools, trees or credentials.
type initializeResponse struct {
	Result *struct {
		ProtocolVersion string `json:"protocolVersion"`
		Instructions    string `json:"instructions"`
	} `json:"result"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

// readInitializeResponse accepts the two legal Streamable HTTP response shapes:
// one JSON document, or an SSE stream whose data line carries the JSON-RPC
// response.
func readInitializeResponse(response *http.Response) (*sdk.InitializeResult, *checkError) {
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, failf("compat_response_unreadable")
	}
	var envelope initializeResponse
	if strings.HasPrefix(strings.TrimSpace(response.Header.Get("Content-Type")), "text/event-stream") {
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope); err != nil {
				return nil, failf("compat_response_invalid")
			}
			break
		}
	} else if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, failf("compat_response_invalid")
	}
	if envelope.Error != nil {
		return nil, failf("compat_response_error", field("code", strconv.Itoa(envelope.Error.Code)))
	}
	if envelope.Result == nil {
		return nil, failf("compat_response_missing")
	}
	return &sdk.InitializeResult{
		ProtocolVersion: envelope.Result.ProtocolVersion,
		Instructions:    envelope.Result.Instructions,
	}, nil
}

// connectStdioAdapter starts the adapter with the stdio transport and performs
// the MCP initialize handshake. The returned stop function closes the session
// and guarantees the child process is gone.
func connectStdioAdapter(ctx context.Context, binaryPath, configPath,
	resultMode string) (*sdk.ClientSession, func(), *checkError) {
	return connectStdioAdapterVersion(ctx, binaryPath, configPath, resultMode, nil)
}

// connectStdioAdapterVersion is connectStdioAdapter with explicit client session
// options. A second, short-lived child is used only to read the compatibility
// initialize result; it performs no tool call.
func connectStdioAdapterVersion(ctx context.Context, binaryPath, configPath,
	resultMode string, options *sdk.ClientSessionOptions) (*sdk.ClientSession, func(), *checkError) {
	command := exec.Command(binaryPath, stdioAdapterArgs(configPath, resultMode)...)
	command.Stderr = io.Discard
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	session, err := newMCPClient().Connect(connectCtx, &sdk.CommandTransport{Command: command}, options)
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
	if t.resultMode != "" {
		clone.Header.Set(resultModeHeader, t.resultMode)
	}
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
