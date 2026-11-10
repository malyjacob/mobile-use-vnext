package android

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// rpcDevice builds a transport whose JSON-RPC endpoint is a test server.
func rpcDevice(t *testing.T, server *httptest.Server) *Device {
	t.Helper()
	d, err := New(Config{Serial: "127.0.0.1:5555", Timeout: 2 * time.Second}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.testBase = server.URL
	return d
}

func TestRPCRequestEncodingAndResultDecoding(t *testing.T) {
	var (
		method string
		params []any
		path   string
		header http.Header
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		header = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if request["jsonrpc"] != "2.0" {
			t.Errorf("jsonrpc=%v", request["jsonrpc"])
		}
		if request["id"] != float64(1) {
			t.Errorf("id=%v", request["id"])
		}
		method, _ = request["method"].(string)
		params, _ = request["params"].([]any)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"utf-8 ok"}`)
	}))
	defer server.Close()

	d := rpcDevice(t, server)
	value, err := d.rpc(context.Background(), "getTextOnDisplay", []any{7, "pkg", "id", "cls", "", "[0,0][10,10]", 100, 200}, time.Second)
	if err != nil {
		t.Fatalf("rpc: %v", err)
	}
	if value != "utf-8 ok" {
		t.Fatalf("result=%v", value)
	}
	if path != "/jsonrpc/0" {
		t.Fatalf("path=%q", path)
	}
	if method != "getTextOnDisplay" {
		t.Fatalf("method=%q", method)
	}
	if header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type=%q", header.Get("Content-Type"))
	}
	if len(params) != 8 || params[0] != float64(7) || params[1] != "pkg" || params[5] != "[0,0][10,10]" {
		t.Fatalf("params=%v", params)
	}
	if params[6] != float64(100) || params[7] != float64(200) {
		t.Fatalf("size params=%v", params[6:])
	}
}

func TestRPCErrorIsSanitized(t *testing.T) {
	const secret = "com.example.secret.Token 9fa3-bc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,`+
			`"message":"java.lang.IllegalStateException: `+secret+`","data":"`+secret+`"}}`)
	}))
	defer server.Close()

	d := rpcDevice(t, server)
	_, err := d.rpc(context.Background(), "setTextOnDisplay", []any{secret}, time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if CodeOf(err) != CodeRPC {
		t.Fatalf("code=%q", CodeOf(err))
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "IllegalState") {
		t.Fatalf("error leaks server text: %v", err)
	}
	// The wrapped cause must not print the server body either.
	if cause := unwrapCause(err); cause != nil && strings.Contains(cause.Error(), secret) {
		t.Fatalf("cause leaks server text")
	}
}

func TestRPCClassifiesDisconnectedService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,`+
			`"message":"android.os.DeadObjectException","data":{}}}`)
	}))
	defer server.Close()
	d := rpcDevice(t, server)
	if _, err := d.rpc(context.Background(), "dumpWindowHierarchy", []any{true, 50}, time.Second); CodeOf(err) != CodeServerStopped {
		t.Fatalf("code=%q", CodeOf(err))
	}
}

func TestRPCMalformedResponseIsControlled(t *testing.T) {
	for _, body := range []string{`not json`, `{"jsonrpc":"2.0","id":1}`, `[]`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		d := rpcDevice(t, server)
		_, err := d.rpc(context.Background(), "m", nil, time.Second)
		server.Close()
		if err == nil || CodeOf(err) != CodeRPC {
			t.Fatalf("body %q: err=%v code=%q", body, err, CodeOf(err))
		}
	}
}

func TestRPCNonOKStatusIsControlled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	d := rpcDevice(t, server)
	if _, err := d.rpc(context.Background(), "m", nil, time.Second); CodeOf(err) != CodeRPC {
		t.Fatalf("code=%q", CodeOf(err))
	}
}

func TestRPCUnreachableIsServerStopped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()
	d, err := New(Config{Serial: "s", Timeout: 500 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.testBase = url
	if _, err := d.rpc(context.Background(), "m", nil, 500*time.Millisecond); CodeOf(err) != CodeServerStopped {
		t.Fatalf("code=%q", CodeOf(err))
	}
	if d.U2Ready(context.Background()) {
		t.Fatal("an unreachable endpoint must not be ready")
	}
}

func TestRPCCancellationIsControlled(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)

	d := rpcDevice(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := d.rpc(ctx, "m", nil, 5*time.Second)
	if CodeOf(err) != CodeCancelled {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestPingReadiness(t *testing.T) {
	pong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			t.Errorf("path=%q", r.URL.Path)
		}
		_, _ = io.WriteString(w, "pong")
	}))
	defer pong.Close()
	if !rpcDevice(t, pong).U2Ready(context.Background()) {
		t.Fatal("pong must be ready")
	}

	wrongBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "nope")
	}))
	defer wrongBody.Close()
	if rpcDevice(t, wrongBody).U2Ready(context.Background()) {
		t.Fatal("a non-pong body must not be ready")
	}

	errorStatus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "pong")
	}))
	defer errorStatus.Close()
	if rpcDevice(t, errorStatus).U2Ready(context.Background()) {
		t.Fatal("a non-200 status must not be ready")
	}
}

func TestDumpWindowHierarchyParams(t *testing.T) {
	var params []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(body, &request)
		if request.Method != "dumpWindowHierarchy" {
			t.Errorf("method=%q", request.Method)
		}
		params = request.Params
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"<hierarchy rotation=\"0\"></hierarchy>"}`)
	}))
	defer server.Close()

	d := rpcDevice(t, server)
	if _, err := d.rpc(context.Background(), "dumpWindowHierarchy", []any{true, maxHierarchyDepth}, time.Second); err != nil {
		t.Fatalf("rpc: %v", err)
	}
	if len(params) != 2 || params[0] != true || params[1] != float64(50) {
		t.Fatalf("dumpWindowHierarchy params=%v, want [true,50]", params)
	}
}

func TestTextRPCArgumentOrder(t *testing.T) {
	var calls [][]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(body, &request)
		calls = append(calls, request.Params)
		switch request.Method {
		case "getTextOnDisplay":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"old"}}`)
		case "setTextOnDisplay":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":null,"issued":true,"accepted":true}}`)
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	d := rpcDevice(t, server)
	target := device.TextTarget{
		DisplayID:   61,
		Package:     "com.example.app",
		ResourceID:  "com.example.app:id/field",
		ClassName:   "android.widget.EditText",
		Description: "Search",
		Bounds:      device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 220},
		Width:       1080,
		Height:      2400,
	}
	read, err := d.GetTextOnDisplay(context.Background(), target)
	if err != nil {
		t.Fatalf("GetTextOnDisplay: %v", err)
	}
	if read.CandidateCount != 1 || read.Value == nil || *read.Value != "old" || read.Issued || read.Accepted {
		t.Fatalf("read=%+v", read)
	}
	if read.Receipt != device.DispatchUnknown {
		t.Fatalf("read receipt=%d", read.Receipt)
	}
	written, err := d.SetTextOnDisplay(context.Background(), target, "hello")
	if err != nil {
		t.Fatalf("SetTextOnDisplay: %v", err)
	}
	if written.CandidateCount != 1 || !written.Issued || !written.Accepted || written.Value != nil {
		t.Fatalf("written=%+v", written)
	}
	if written.Receipt != device.DispatchTerminated {
		t.Fatalf("written receipt=%d", written.Receipt)
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%d", len(calls))
	}
	want := []any{
		float64(61),
		"com.example.app",
		"com.example.app:id/field",
		"android.widget.EditText",
		"Search",
		"[0,100][1080,220]",
		float64(1080),
		float64(2400),
	}
	if len(calls[0]) != len(want) {
		t.Fatalf("get params=%v", calls[0])
	}
	for index := range want {
		if calls[0][index] != want[index] {
			t.Fatalf("get params[%d]=%v want %v (all %v)", index, calls[0][index], want[index], calls[0])
		}
	}
	if len(calls[1]) != len(want)+1 || calls[1][len(want)] != "hello" {
		t.Fatalf("set params=%v", calls[1])
	}
	for index := range want {
		if calls[1][index] != want[index] {
			t.Fatalf("set params[%d]=%v want %v", index, calls[1][index], want[index])
		}
	}
}

func TestTextRPCRejectsIncompleteTargetBeforeDevice(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer server.Close()
	d := rpcDevice(t, server)
	target := device.TextTarget{DisplayID: 1, Width: 10, Height: 10}
	if _, err := d.GetTextOnDisplay(context.Background(), target); CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("code=%q", CodeOf(err))
	}
	result, err := d.SetTextOnDisplay(context.Background(), target, "hello")
	if CodeOf(err) != CodeInvalidArgs || result.Receipt != device.DispatchNotDispatched {
		t.Fatalf("set result=%+v code=%q err=%v", result, CodeOf(err), err)
	}
	if hits != 0 {
		t.Fatal("an incomplete target must not reach the server")
	}
}

func TestSetTextRequiresCompleteMatchingResult(t *testing.T) {
	target := device.TextTarget{
		DisplayID: 61,
		Package:   "com.example.app",
		Bounds:    device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 220},
		Width:     1080,
		Height:    2400,
	}
	cases := []struct {
		name string
		body string
	}{
		{"wrong jsonrpc", `{"jsonrpc":"1.0","id":1,"result":{"candidateCount":1,"value":null,"issued":true,"accepted":true}}`},
		{"wrong id", `{"jsonrpc":"2.0","id":2,"result":{"candidateCount":1,"value":null,"issued":true,"accepted":true}}`},
		{"missing count", `{"jsonrpc":"2.0","id":1,"result":{"value":null,"issued":true,"accepted":true}}`},
		{"count type", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":"1","value":null,"issued":true,"accepted":true}}`},
		{"missing value", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"issued":true,"accepted":true}}`},
		{"value type", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":7,"issued":true,"accepted":true}}`},
		{"missing issued", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":null,"accepted":true}}`},
		{"issued type", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":null,"issued":1,"accepted":true}}`},
		{"missing accepted", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":null,"issued":true}}`},
		{"accepted type", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":null,"issued":true,"accepted":"true"}}`},
		{"jsonrpc error", `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"failed"}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			d := rpcDevice(t, server)
			result, err := d.SetTextOnDisplay(t.Context(), target, "hello")
			if err == nil || result.Receipt != device.DispatchUnknown {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestSetTextSemanticRejectionStillTerminates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":0,"value":null,"issued":false,"accepted":false}}`)
	}))
	defer server.Close()
	d := rpcDevice(t, server)
	target := device.TextTarget{
		DisplayID: 61,
		Package:   "com.example.app",
		Bounds:    device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 220},
		Width:     1080,
		Height:    2400,
	}
	result, err := d.SetTextOnDisplay(t.Context(), target, "hello")
	if err != nil || result.Receipt != device.DispatchTerminated ||
		result.Issued || result.Accepted || result.CandidateCount != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// unwrapCause returns the wrapped cause if the error exposes one.
func unwrapCause(err error) error {
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return wrapped.Unwrap()
	}
	return nil
}
