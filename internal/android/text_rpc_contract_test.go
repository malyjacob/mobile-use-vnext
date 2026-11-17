package android

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

type capturedTextRPC struct {
	request rpcRequest
	body    []byte
}

func textTargetFixture() device.TextTarget {
	return device.TextTarget{
		DisplayID:   61,
		Package:     "com.example.app",
		ResourceID:  "com.example.app:id/field",
		ClassName:   "android.widget.EditText",
		Description: "Search",
		Bounds:      device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 220},
		Width:       1080,
		Height:      2400,
	}
}

func receiveTextRPC(t *testing.T, requests <-chan capturedTextRPC) capturedTextRPC {
	t.Helper()
	select {
	case captured := <-requests:
		return captured
	case <-time.After(time.Second):
		t.Fatal("text RPC request was not observed")
		return capturedTextRPC{}
	}
}

func requireTextArguments(t *testing.T, got []any, target device.TextTarget, text *string) {
	t.Helper()
	want := []any{
		float64(target.DisplayID),
		target.Package,
		target.ResourceID,
		target.ClassName,
		target.Description,
		formatBounds(target.Bounds),
		float64(target.Width),
		float64(target.Height),
	}
	if text != nil {
		want = append(want, *text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("text RPC arguments = %#v, want %#v", got, want)
	}
}

func TestTextRPCDescriptionOnlyTargetArgumentOrder(t *testing.T) {
	requests := make(chan capturedTextRPC, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request rpcRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- capturedTextRPC{request: request, body: body}
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

	target := textTargetFixture()
	target.ResourceID = ""
	target.ClassName = "android.widget.EditText"
	target.Description = "Description-only field"
	d := rpcDevice(t, server)

	read, err := d.GetTextOnDisplay(t.Context(), target)
	if err != nil {
		t.Fatalf("GetTextOnDisplay: %v", err)
	}
	if read.CandidateCount != 1 || read.Value == nil || *read.Value != "old" {
		t.Fatalf("read result = %+v", read)
	}
	get := receiveTextRPC(t, requests)
	if get.request.Method != "getTextOnDisplay" {
		t.Fatalf("method = %q", get.request.Method)
	}
	requireTextArguments(t, get.request.Params, target, nil)

	replacement := "replacement"
	written, err := d.SetTextOnDisplay(t.Context(), target, replacement)
	if err != nil {
		t.Fatalf("SetTextOnDisplay: %v", err)
	}
	if written.Receipt != device.DispatchTerminated || written.CandidateCount != 1 ||
		!written.Issued || !written.Accepted {
		t.Fatalf("written result = %+v", written)
	}
	set := receiveTextRPC(t, requests)
	if set.request.Method != "setTextOnDisplay" {
		t.Fatalf("method = %q", set.request.Method)
	}
	requireTextArguments(t, set.request.Params, target, &replacement)
}

func TestGetTextRPCRejectsMalformedAndCancelledResponses(t *testing.T) {
	target := textTargetFixture()
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "result is not an object",
			body: `{"jsonrpc":"2.0","id":1,"result":"text"}`,
			code: CodeRPC,
		},
		{
			name: "candidate count is fractional",
			body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1.5,"value":"old"}}`,
			code: CodeRPC,
		},
		{
			name: "candidate count has the wrong type",
			body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":true,"value":"old"}}`,
			code: CodeRPC,
		},
		{
			name: "value has the wrong type",
			body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":7}}`,
			code: CodeRPC,
		},
		{
			name: "issued has the wrong type",
			body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"old","issued":1}}`,
			code: CodeRPC,
		},
		{
			name: "accepted has the wrong type",
			body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"old","accepted":"true"}}`,
			code: CodeRPC,
		},
		{
			name: "json-rpc error",
			body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"failed"}}`,
			code: CodeRPC,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()

			d := rpcDevice(t, server)
			if _, err := d.GetTextOnDisplay(t.Context(), target); CodeOf(err) != testCase.code {
				t.Fatalf("code = %q error = %v, want %q", CodeOf(err), err, testCase.code)
			}
			if got := hits.Load(); got != 1 {
				t.Fatalf("requests = %d, want one", got)
			}
		})
	}

	t.Run("context cancelled after request", func(t *testing.T) {
		var hits atomic.Int32
		entered := make(chan struct{}, 8)
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			entered <- struct{}{}
			<-release
		}))
		defer server.Close()
		defer close(release)

		d := rpcDevice(t, server)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := d.GetTextOnDisplay(ctx, target)
			done <- err
		}()

		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("request did not reach the server")
		}
		cancel()
		select {
		case err := <-done:
			if CodeOf(err) != CodeCancelled {
				t.Fatalf("code = %q error = %v", CodeOf(err), err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled request did not return")
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("requests = %d, want one", got)
		}
	})
}

func TestSetTextRPCPreservesEdgeCaseBody(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "Chinese", text: "中文输入"},
		{name: "supplementary plane emoji", text: "😀"},
		{name: "empty string", text: ""},
		{name: "newline", text: "first\nsecond"},
		{name: "quote", text: `say "hello"`},
		{name: "backslash", text: `a\b\c`},
	}
	target := textTargetFixture()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var hits atomic.Int32
			requests := make(chan capturedTextRPC, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				body, _ := io.ReadAll(r.Body)
				var request rpcRequest
				if err := json.Unmarshal(body, &request); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				requests <- capturedTextRPC{request: request, body: body}
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":null,"issued":true,"accepted":true}}`)
			}))
			defer server.Close()

			d := rpcDevice(t, server)
			result, err := d.SetTextOnDisplay(t.Context(), target, testCase.text)
			if err != nil {
				t.Fatalf("SetTextOnDisplay: %v", err)
			}
			if result.Receipt != device.DispatchTerminated || result.CandidateCount != 1 ||
				!result.Issued || !result.Accepted {
				t.Fatalf("result = %+v", result)
			}
			captured := receiveTextRPC(t, requests)
			for index, octet := range captured.body {
				if octet >= 0x80 {
					t.Fatalf("request body contains non-ASCII octet %#x at %d", octet, index)
				}
			}
			if captured.request.Method != "setTextOnDisplay" {
				t.Fatalf("method = %q", captured.request.Method)
			}
			if len(captured.request.Params) != 9 {
				t.Fatalf("params = %#v", captured.request.Params)
			}
			got, ok := captured.request.Params[8].(string)
			if !ok || got != testCase.text {
				t.Fatalf("text = %q present=%t, want %q", got, ok, testCase.text)
			}
			if got := hits.Load(); got != 1 {
				t.Fatalf("requests = %d, want one", got)
			}
		})
	}
}

func TestSetTextReceiptUnknownAfterAmbiguousTransport(t *testing.T) {
	target := textTargetFixture()

	t.Run("connection closed after request", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("response does not support hijacking")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = connection.Close()
		}))
		defer server.Close()

		d := rpcDevice(t, server)
		result, err := d.SetTextOnDisplay(t.Context(), target, "replacement")
		if err == nil || CodeOf(err) != CodeServerStopped {
			t.Fatalf("code = %q error = %v", CodeOf(err), err)
		}
		if result.Receipt != device.DispatchUnknown {
			t.Fatalf("receipt = %d, want unknown", result.Receipt)
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("requests = %d, want one", got)
		}
	})

	t.Run("timeout after request", func(t *testing.T) {
		var hits atomic.Int32
		entered := make(chan struct{}, 8)
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			entered <- struct{}{}
			<-release
		}))
		defer server.Close()
		defer close(release)

		d := rpcDevice(t, server)
		d.timeout = 100 * time.Millisecond
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		type outcome struct {
			result device.TextResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := d.SetTextOnDisplay(ctx, target, "replacement")
			done <- outcome{result: result, err: err}
		}()

		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("request did not reach the server")
		}
		select {
		case got := <-done:
			if CodeOf(got.err) != CodeTimeout {
				t.Fatalf("code = %q error = %v", CodeOf(got.err), got.err)
			}
			if got.result.Receipt != device.DispatchUnknown {
				t.Fatalf("receipt = %d, want unknown", got.result.Receipt)
			}
		case <-time.After(time.Second):
			t.Fatal("timed-out request did not return")
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("requests = %d, want one", got)
		}
	})

	t.Run("context cancelled after request", func(t *testing.T) {
		var hits atomic.Int32
		entered := make(chan struct{}, 8)
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			entered <- struct{}{}
			<-release
		}))
		defer server.Close()
		defer close(release)

		d := rpcDevice(t, server)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type outcome struct {
			result device.TextResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := d.SetTextOnDisplay(ctx, target, "replacement")
			done <- outcome{result: result, err: err}
		}()

		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("request did not reach the server")
		}
		cancel()
		select {
		case got := <-done:
			if CodeOf(got.err) != CodeCancelled {
				t.Fatalf("code = %q error = %v", CodeOf(got.err), got.err)
			}
			if got.result.Receipt != device.DispatchUnknown {
				t.Fatalf("receipt = %d, want unknown", got.result.Receipt)
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled request did not return")
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("requests = %d, want one", got)
		}
	})
}

func TestTextTargetValidationBoundaries(t *testing.T) {
	var hits atomic.Int32
	requests := make(chan capturedTextRPC, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		var request rpcRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests <- capturedTextRPC{request: request, body: body}
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

	cases := []struct {
		name   string
		mutate func(*device.TextTarget)
	}{
		{name: "negative display", mutate: func(target *device.TextTarget) { target.DisplayID = -1 }},
		{name: "zero width", mutate: func(target *device.TextTarget) { target.Width = 0 }},
		{name: "negative width", mutate: func(target *device.TextTarget) { target.Width = -1 }},
		{name: "zero height", mutate: func(target *device.TextTarget) { target.Height = 0 }},
		{name: "negative height", mutate: func(target *device.TextTarget) { target.Height = -1 }},
		{name: "empty bounds", mutate: func(target *device.TextTarget) { target.Bounds = device.Rect{} }},
		{name: "empty package", mutate: func(target *device.TextTarget) { target.Package = "" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target := textTargetFixture()
			testCase.mutate(&target)
			if _, err := d.GetTextOnDisplay(t.Context(), target); CodeOf(err) != CodeInvalidArgs {
				t.Fatalf("get code = %q error = %v", CodeOf(err), err)
			}
			result, err := d.SetTextOnDisplay(t.Context(), target, "replacement")
			if CodeOf(err) != CodeInvalidArgs || result.Receipt != device.DispatchNotDispatched {
				t.Fatalf("set result = %+v code = %q error = %v", result, CodeOf(err), err)
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("invalid target reached transport %d time(s)", got)
			}
		})
	}

	target := textTargetFixture()
	target.ResourceID = ""
	target.ClassName = ""
	target.Description = ""
	read, err := d.GetTextOnDisplay(t.Context(), target)
	if err != nil || read.CandidateCount != 1 || read.Value == nil || *read.Value != "old" {
		t.Fatalf("identity-empty get result = %+v error = %v", read, err)
	}
	get := receiveTextRPC(t, requests)
	requireTextArguments(t, get.request.Params, target, nil)

	replacement := "replacement"
	written, err := d.SetTextOnDisplay(t.Context(), target, replacement)
	if err != nil || written.Receipt != device.DispatchTerminated ||
		written.CandidateCount != 1 || !written.Issued || !written.Accepted {
		t.Fatalf("identity-empty set result = %+v error = %v", written, err)
	}
	set := receiveTextRPC(t, requests)
	requireTextArguments(t, set.request.Params, target, &replacement)
}

// TestSetTextRPCRejectsIncompleteOrContradictoryResults proves the decode layer
// never turns a missing member, a negative count or a combination the fixed
// helper cannot produce into 0/false/"" or into a successful result. Every such
// response keeps the dispatch receipt unknown.
func TestSetTextRPCRejectsIncompleteOrContradictoryResults(t *testing.T) {
	target := textTargetFixture()
	cases := []struct {
		name string
		body string
	}{
		{"result is not an object", `{"jsonrpc":"2.0","id":1,"result":"done"}`},
		{"missing candidateCount", `{"jsonrpc":"2.0","id":1,"result":{"value":null,"issued":false,"accepted":false}}`},
		{"negative candidateCount", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":-1,"value":null,"issued":false,"accepted":false}}`},
		{"fractional candidateCount", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1.5,"value":null,"issued":true,"accepted":true}}`},
		{"missing value", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"issued":true,"accepted":true}}`},
		{"missing issued", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"new","accepted":true}}`},
		{"missing accepted", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"new","issued":true}}`},
		{"non-string value", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":7,"issued":true,"accepted":true}}`},
		{"non-boolean issued", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"new","issued":"yes","accepted":true}}`},
		{"unique but not issued", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"new","issued":false,"accepted":false}}`},
		{"non-unique but issued", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":0,"value":null,"issued":true,"accepted":false}}`},
		{"non-unique but accepted", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":0,"value":null,"issued":false,"accepted":true}}`},
		{"non-unique with a value", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":2,"value":"new","issued":false,"accepted":false}}`},
		{"negative count with issued", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":-2,"value":null,"issued":true,"accepted":true}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			d := rpcDevice(t, server)
			result, err := d.SetTextOnDisplay(t.Context(), target, "new")
			if err == nil || !errors.Is(err, device.ErrTextResultInvalid) {
				t.Fatalf("error = %v, want ErrTextResultInvalid", err)
			}
			if result.Receipt != device.DispatchUnknown {
				t.Fatalf("receipt = %d, want unknown", result.Receipt)
			}
		})
	}
}

// TestSetTextRPCAcceptsTheTwoCompleteHelperShapes proves both shapes the fixed
// helper can produce stay complete terminated results, including the
// unique/issued/readback combination that does not require accepted=true.
func TestSetTextRPCAcceptsTheTwoCompleteHelperShapes(t *testing.T) {
	target := textTargetFixture()
	cases := []struct {
		name   string
		body   string
		count  int
		value  *string
		issued bool
	}{
		{name: "non-unique not called", body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":0,"value":null,"issued":false,"accepted":false}}`, count: 0},
		{name: "unique called but accepted false", body: `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":"new","issued":true,"accepted":false}}`, count: 1, value: stringPtr("new"), issued: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			d := rpcDevice(t, server)
			result, err := d.SetTextOnDisplay(t.Context(), target, "new")
			if err != nil {
				t.Fatalf("SetTextOnDisplay: %v", err)
			}
			if result.Receipt != device.DispatchTerminated || result.CandidateCount != testCase.count ||
				result.Issued != testCase.issued {
				t.Fatalf("result = %+v", result)
			}
			if testCase.value == nil {
				if result.Value != nil {
					t.Fatalf("value = %q, want nil", *result.Value)
				}
			} else if result.Value == nil || *result.Value != *testCase.value {
				t.Fatalf("value = %v, want %q", result.Value, *testCase.value)
			}
		})
	}
}

// TestGetTextRPCRequiresCountAndValue proves the ENTER gate cannot treat a
// missing count or value as a valid exact readback.
func TestGetTextRPCRequiresCountAndValue(t *testing.T) {
	target := textTargetFixture()
	cases := []struct {
		name string
		body string
	}{
		{"missing candidateCount", `{"jsonrpc":"2.0","id":1,"result":{"value":"old"}}`},
		{"missing value", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1}}`},
		{"negative candidateCount", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":-1,"value":"old"}}`},
		{"non-string value", `{"jsonrpc":"2.0","id":1,"result":{"candidateCount":1,"value":true}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			d := rpcDevice(t, server)
			if _, err := d.GetTextOnDisplay(t.Context(), target); err == nil ||
				!errors.Is(err, device.ErrTextResultInvalid) {
				t.Fatalf("error = %v, want ErrTextResultInvalid", err)
			}
		})
	}
}

func stringPtr(value string) *string { return &value }
