package android

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// maxRPCResponse bounds one JSON-RPC body. A hierarchy dump of a very deep tree
// stays well below this; anything larger is a transport anomaly.
const maxRPCResponse = 64 << 20
const rpcRequestID = 1

// rpcRequest is the fixed JSON-RPC 2.0 envelope the pinned server expects.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// rpcResponse carries the envelope only. error.data may contain device
// internals and is never surfaced.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResultError is a stable classification of one JSON-RPC error payload.
var errRPCMalformed = newErr(CodeRPC, "the server response is not a JSON-RPC result", nil)

// rpc sends one method call and returns its decoded result. It never retries:
// a lost response is an uncertain outcome for the caller to record.
func (d *Device) rpc(ctx context.Context, method string, params []any, timeout time.Duration) (any, error) {
	return d.rpcWith(ctx, d.client, d.endpoint(), method, params, timeout)
}

// endpoint is the JSON-RPC base URL; tests may override it.
func (d *Device) endpoint() string {
	if d.testBase != "" {
		return d.testBase
	}
	return d.base
}

// rpcWith performs the HTTP exchange against one endpoint. It is separated so
// tests can point it at an httptest server without a device.
func (d *Device) rpcWith(ctx context.Context, client *http.Client, base, method string, params []any, timeout time.Duration) (value any, err error) {
	if ctx == nil {
		return nil, errContextEnded
	}
	if client == nil {
		client = http.DefaultClient
	}
	if timeout <= 0 {
		timeout = d.timeout
	}
	body, marshalErr := json.Marshal(rpcRequest{
		JSONRPC: "2.0", ID: rpcRequestID, Method: method, Params: params,
	})
	if marshalErr != nil {
		return nil, newErr(CodeInvalidArgs, "the request parameters are not encodable", nil)
	}
	// The pinned server decodes the body as Latin-1 before JSON parsing. Raw
	// UTF-8 would corrupt Chinese text, so every non-ASCII rune is emitted as a
	// JSON \uXXXX escape (the same wire shape the pinned Python client uses).
	body = asciiJSON(body)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/jsonrpc/0", bytes.NewReader(body))
	if requestErr != nil {
		return nil, newErr(CodeInvalidArgs, "the RPC request could not be built", nil)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "")
	request.Header.Set("User-Agent", "mobile-use-vnext")

	client = withShortTransport(client, timeout)
	response, doErr := client.Do(request)
	if doErr != nil {
		return nil, rpcTransportError(ctx, doErr)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return nil, newErr(CodeRPC, "the server rejected the request", nil)
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, maxRPCResponse+1))
	if readErr != nil {
		return nil, rpcTransportError(ctx, readErr)
	}
	if len(payload) > maxRPCResponse {
		return nil, newErr(CodeRPC, "the server response is too large", nil)
	}
	var decoded rpcResponse
	if decodeErr := json.Unmarshal(payload, &decoded); decodeErr != nil {
		return nil, errRPCMalformed
	}
	payload = nil // the raw body is never retained or logged.
	if decoded.JSONRPC != "2.0" || decoded.ID != rpcRequestID ||
		(len(decoded.Result) == 0) == (decoded.Error == nil) {
		return nil, errRPCMalformed
	}
	if decoded.Error != nil {
		return nil, classifyRPCError(decoded.Error)
	}
	var result any
	if decodeErr := json.Unmarshal(decoded.Result, &result); decodeErr != nil {
		return nil, errRPCMalformed
	}
	return result, nil
}

// withShortTransport returns a client with the configured timeout that ignores
// proxy environment variables, so a loopback JSON-RPC endpoint is never routed
// through an unrelated proxy.
func withShortTransport(client *http.Client, timeout time.Duration) *http.Client {
	cloned := *client
	cloned.Timeout = timeout
	if cloned.Transport == nil {
		if transport, ok := http.DefaultTransport.(*http.Transport); ok {
			copied := transport.Clone()
			copied.Proxy = nil
			copied.DialContext = (&net.Dialer{Timeout: timeout}).DialContext
			cloned.Transport = copied
		}
	}
	return &cloned
}

// classifyRPCError maps the server's error object to a stable code. The message
// text is inspected only to classify; it is never returned or logged.
func classifyRPCError(serverErr *rpcError) error {
	message := serverErr.Message
	switch {
	case strings.Contains(message, "UiAutomation not connected"),
		strings.Contains(message, "DeadObjectException"),
		strings.Contains(message, "DeadSystemRuntimeException"):
		return newErr(CodeServerStopped, "the accessibility service is not connected", nil)
	case strings.Contains(message, "StackOverflowError"):
		return newErr(CodeHierarchy, "the hierarchy exceeded the server stack", nil)
	case strings.Contains(message, "UiObjectNotFoundException"):
		return newErr(CodeRPC, "the server found no matching object", nil)
	case strings.Contains(message, "NoSuchMethodException"),
		strings.Contains(message, "MethodNotFound"),
		serverErr.Code == -32601:
		return newErr(CodeRPCMethod, "the server does not implement the method", nil)
	}
	return newErr(CodeRPC, "the server returned an error", nil)
}

// rpcTransportError separates cancellation, timeout and unreachability.
func rpcTransportError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return newErr(CodeCancelled, "the operation was cancelled", ctx.Err())
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newErr(CodeTimeout, "the RPC call exceeded its deadline", err)
	}
	return newErr(CodeServerStopped, "the server endpoint is not reachable", err)
}

// ping performs the readiness GET. It reports true only for a 200 "pong".
func (d *Device) ping(ctx context.Context, client *http.Client, base string) bool {
	if ctx == nil || d == nil {
		return false
	}
	timeout := 2 * time.Second
	if d.timeout < timeout {
		timeout = d.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/ping", nil)
	if err != nil {
		return false
	}
	request.Header.Set("Accept-Encoding", "")
	request.Header.Set("User-Agent", "mobile-use-vnext")
	response, err := withShortTransport(client, timeout).Do(request)
	if err != nil {
		return false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		_ = response.Body.Close()
	}()
	if response.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(body)) == "pong"
}

// U2Ready reports whether the RPC endpoint answers.
func (d *Device) U2Ready(ctx context.Context) bool {
	if d == nil {
		return false
	}
	return d.ping(ctx, d.client, d.endpoint())
}

// asciiJSON converts every non-ASCII rune in a JSON document to a unicode
// escape. The pinned uiautomator server decodes request bodies as Latin-1, so
// raw UTF-8 text would be corrupted before it reaches the JSON parser.
func asciiJSON(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data))
	for index := 0; index < len(data); {
		octet := data[index]
		if octet < utf8.RuneSelf {
			out.WriteByte(octet)
			index++
			continue
		}
		r, size := utf8.DecodeRune(data[index:])
		if r == utf8.RuneError && size == 1 {
			out.WriteByte(octet)
			index++
			continue
		}
		index += size
		if r > 0xFFFF {
			r -= 0x10000
			high := 0xD800 + (r >> 10)
			low := 0xDC00 + (r & 0x3FF)
			writeEscape(&out, high)
			writeEscape(&out, low)
			continue
		}
		writeEscape(&out, r)
	}
	return out.Bytes()
}

// writeEscape emits one JSON unicode escape without a Go string literal that
// would itself be interpreted as an escape.
func writeEscape(out *bytes.Buffer, r rune) {
	out.WriteByte(92) // backslash
	out.WriteByte('u')
	fmt.Fprintf(out, "%04x", r)
}
