package client

import (
	"bytes"
	"context"
	"encoding/base64"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const testToken = "fake-agent-token-for-tests"

type requestCapture struct {
	method  string
	path    string
	headers http.Header
	body    string
}

func captureRequest(t *testing.T, request *http.Request) requestCapture {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		// Handler goroutines must not call FailNow; the capture still lets
		// the test goroutine report the mismatch.
		t.Errorf("read request body: %v", err)
	}
	return requestCapture{
		method:  request.Method,
		path:    request.URL.Path,
		headers: request.Header.Clone(),
		body:    string(body),
	}
}

func newTestClient(t *testing.T, baseURL string, configure ...func(*Config)) *Client {
	t.Helper()
	config := Config{BaseURL: baseURL, AgentToken: testToken}
	for _, apply := range configure {
		apply(&config)
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return client
}

func writeResponse(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func requireClientError(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("error type is not *client.Error")
	}
	return clientErr
}

func assertToolRequest(t *testing.T, capture requestCapture, path string) {
	t.Helper()
	if capture.method != http.MethodPost {
		t.Fatalf("method = %s, want POST", capture.method)
	}
	if capture.path != path {
		t.Fatalf("path = %s, want %s", capture.path, path)
	}
	authorization := capture.headers.Values(authorizationHeader)
	if len(authorization) != 1 || authorization[0] != "Bearer "+testToken {
		t.Fatal("Authorization header is missing, duplicated or unexpected")
	}
	contract := capture.headers.Values(contractHeader)
	if len(contract) != 1 || contract[0] != protocol.ContractVersion {
		t.Fatal("contract header is missing, duplicated or unexpected")
	}
	contentType := capture.headers.Values("Content-Type")
	if len(contentType) != 1 || contentType[0] != "application/json" {
		t.Fatal("Content-Type header is missing or duplicated")
	}
}

func onlyOperationHeader(t *testing.T, headers http.Header) string {
	t.Helper()
	values := headers.Values(operationHeader)
	if len(values) != 1 {
		t.Fatalf("%s header count = %d, want 1", operationHeader, len(values))
	}
	return values[0]
}

func assertNoOperationHeader(t *testing.T, headers http.Header) {
	t.Helper()
	if values := headers.Values(operationHeader); len(values) != 0 {
		t.Fatalf("unexpected %s header", operationHeader)
	}
}

// generatedOperationParts splits one generated P of the form
// "P" + 4-digit process tag + "_" + monotonic sequence.
func generatedOperationParts(t *testing.T, operation string) (string, uint64) {
	t.Helper()
	if len(operation) < 7 || operation[0] != 'P' || operation[5] != '_' {
		t.Fatalf("generated operation ID %q has the wrong shape", operation)
	}
	tag := operation[1:5]
	if tag[0] < '1' || tag[0] > '9' {
		t.Fatalf("generated operation tag %q has a leading zero", tag)
	}
	for index := 0; index < len(tag); index++ {
		if tag[index] < '0' || tag[index] > '9' {
			t.Fatalf("generated operation tag %q is not 4 decimal digits", tag)
		}
	}
	sequence, err := strconv.ParseUint(operation[6:], 10, 64)
	if err != nil || sequence == 0 {
		t.Fatalf("generated operation sequence %q is not a positive decimal", operation[6:])
	}
	return tag, sequence
}

func assertGeneratedOperation(t *testing.T, operation string) {
	t.Helper()
	generatedOperationParts(t, operation)
	if !validOperationID(operation) {
		t.Fatalf("generated operation ID does not satisfy validOperationID")
	}
}

func TestStartSendsCanonicalRequestAndDecodesResult(t *testing.T) {
	captures := make(chan requestCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{"operation_id":"Pserver","outcome":"executed","session":{"origin":"Pserver","state":"active","mode":"virtual","agent":"local-agent"},"observation":{"status":"complete","id":"O1","origin":"Pserver","revision":1}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/")
	response, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	capture := <-captures
	assertToolRequest(t, capture, "/api/start")
	if capture.body != "{}" {
		t.Fatalf("start body = %q, want {}", capture.body)
	}
	operation := onlyOperationHeader(t, capture.headers)
	assertGeneratedOperation(t, operation)

	if response == nil {
		t.Fatal("Start() returned a nil response")
	}
	if response.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, want 200", response.HTTPStatus)
	}
	if response.OperationID != operation {
		t.Fatalf("Response.OperationID = %q, want the request operation", response.OperationID)
	}
	if response.Result == nil {
		t.Fatal("Result is nil")
	}
	if response.Result.Session == nil || response.Result.Session.State != "active" {
		t.Fatal("session was not decoded")
	}
	if response.Result.Observation == nil || response.Result.Observation.Status != "complete" {
		t.Fatal("observation was not decoded")
	}
}

func TestWithOperationIDHonored(t *testing.T) {
	captures := make(chan requestCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Start(context.Background(), WithOperationID("Pclient-op_1"))
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	capture := <-captures
	if got := onlyOperationHeader(t, capture.headers); got != "Pclient-op_1" {
		t.Fatalf("operation header = %q, want the explicit ID", got)
	}
	if response == nil || response.OperationID != "Pclient-op_1" {
		t.Fatal("Response.OperationID did not keep the explicit ID")
	}
}

func TestKeepaliveDurationRequests(t *testing.T) {
	captures := make(chan requestCapture, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, fmt.Sprintf(
			`{"operation_id":%q,"outcome":"executed","session":{"origin":"Pstart","state":"active","mode":"virtual","agent":"local-agent","started_at":"2026-11-11T02:00:00Z","expires_at":"2026-11-11T02:30:00Z"}}`,
			request.Header.Get(operationHeader)))
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)
	for _, tc := range []struct {
		name    string
		minutes int
		body    string
	}{
		{"omitted", 0, `{"reference":"O1"}`},
		{"minimum", 1, `{"reference":"O1","duration_minutes":1}`},
		{"maximum", 120, `{"reference":"O1","duration_minutes":120}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation := "Pkeep-" + tc.name
			var response *Response
			var err error
			if tc.name == "omitted" {
				response, err = client.Keepalive(context.Background(), "O1", WithOperationID(operation))
			} else {
				response, err = client.KeepaliveWithDuration(context.Background(), "O1", tc.minutes, WithOperationID(operation))
			}
			if err != nil {
				t.Fatalf("Keepalive: %v", err)
			}
			capture := <-captures
			assertToolRequest(t, capture, "/api/keepalive")
			if capture.body != tc.body || onlyOperationHeader(t, capture.headers) != operation {
				t.Fatalf("body/P changed: %+v", capture)
			}
			if response.OperationID != operation || response.Result.Session.ExpiresAt != "2026-11-11T02:30:00Z" {
				t.Fatalf("response lost P or deadline: %+v", response)
			}
		})
	}
}

func TestKeepaliveInvalidDurationDoesNotSend(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)
	for _, minutes := range []int{-1, 0, 121} {
		response, err := client.KeepaliveWithDuration(context.Background(), "O1", minutes, WithOperationID("Pinvalid-duration"))
		clientErr := requireClientError(t, err)
		if response != nil || clientErr.Code != protocol.CodeInvalidRequest || clientErr.Transport ||
			clientErr.OperationID != "Pinvalid-duration" {
			t.Fatalf("duration %d: response=%+v err=%+v", minutes, response, clientErr)
		}
	}
	_, err := client.KeepaliveWithDuration(context.Background(), "O1", 0)
	assertGeneratedOperation(t, requireClientError(t, err).OperationID)
	if hits.Load() != 0 {
		t.Fatalf("invalid durations reached service %d times", hits.Load())
	}
}

func TestInvalidOperationIDSendsNothing(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	invalid := []string{
		"",
		"Q1",
		"P bad",
		"P" + strings.Repeat("a", 64),
		"P-ok\n",
		"Pé",
	}
	for index, operation := range invalid {
		response, err := client.Start(context.Background(), WithOperationID(operation))
		if response != nil {
			t.Fatalf("case %d: invalid operation ID produced a response", index)
		}
		clientErr := requireClientError(t, err)
		if clientErr.Code != protocol.CodeInvalidRequest {
			t.Fatalf("case %d: code = %q, want %q", index, clientErr.Code, protocol.CodeInvalidRequest)
		}
		if clientErr.Transport {
			t.Fatalf("case %d: invalid operation ID was reported as transport", index)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid operation IDs reached the service %d times", hits.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestNewOperationIDFormatAndUniqueness(t *testing.T) {
	const count = 256
	seen := make(map[string]struct{}, count)
	var prefix string
	var previous uint64
	for index := 0; index < count; index++ {
		operation, err := NewOperationID()
		if err != nil {
			t.Fatalf("NewOperationID() error: %v", err)
		}
		assertGeneratedOperation(t, operation)
		tag, sequence := generatedOperationParts(t, operation)
		if index == 0 {
			prefix = tag
		} else if tag != prefix {
			t.Fatalf("case %d: process prefix changed from %q to %q", index, prefix, tag)
		}
		if sequence <= previous {
			t.Fatalf("case %d: sequence %d did not advance past %d", index, sequence, previous)
		}
		previous = sequence
		if _, duplicate := seen[operation]; duplicate {
			t.Fatalf("case %d: NewOperationID returned a duplicate", index)
		}
		seen[operation] = struct{}{}
	}
}

// TestNewOperationIDConcurrentCallsAreUnique proves the shared process prefix
// plus the atomic sequence keep concurrent allocations collision-free without a
// lock on the hot path.
func TestNewOperationIDConcurrentCallsAreUnique(t *testing.T) {
	const workers, perWorker = 8, 64
	const total = workers * perWorker

	ids := make(chan string, total)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := 0; index < perWorker; index++ {
				operation, err := NewOperationID()
				if err != nil {
					t.Errorf("NewOperationID() error: %v", err)
					return
				}
				ids <- operation
			}
		}()
	}
	group.Wait()
	close(ids)

	seen := make(map[string]struct{}, total)
	var prefix string
	for operation := range ids {
		assertGeneratedOperation(t, operation)
		tag, _ := generatedOperationParts(t, operation)
		if prefix == "" {
			prefix = tag
		} else if tag != prefix {
			t.Fatalf("concurrent prefixes %q and %q differ", prefix, tag)
		}
		if _, duplicate := seen[operation]; duplicate {
			t.Fatalf("concurrent call returned a duplicate %q", operation)
		}
		seen[operation] = struct{}{}
	}
	if len(seen) != total {
		t.Fatalf("collected %d unique operation IDs, want %d", len(seen), total)
	}
}

func TestTransportFailureKeepsGeneratedOperation(t *testing.T) {
	var attempts atomic.Int32
	var attemptedOperation string
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts.Add(1)
		attemptedOperation = request.Header.Get(operationHeader)
		return nil, errors.New("dial failure with non-identifying detail")
	})}
	client, err := New(Config{
		BaseURL:    "http://127.0.0.1:1",
		AgentToken: testToken,
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	response, err := client.Start(context.Background())
	if response != nil {
		t.Fatal("transport failure produced a response")
	}
	clientErr := requireClientError(t, err)
	if !clientErr.Transport || clientErr.Code != ErrorTransport {
		t.Fatal("transport failure was not reported as a transport error")
	}
	if clientErr.HTTPStatus != 0 {
		t.Fatalf("transport HTTPStatus = %d, want 0", clientErr.HTTPStatus)
	}
	assertGeneratedOperation(t, clientErr.OperationID)
	if attemptedOperation != clientErr.OperationID {
		t.Fatal("transport error did not retain the operation ID sent on the attempt")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts.Load())
	}
	if clientErr.Result != nil {
		t.Fatal("transport failure produced a structured result")
	}
	if strings.Contains(clientErr.Error(), "dial failure with non-identifying detail") {
		t.Fatal("error text exposed the raw transport error")
	}
	if strings.Contains(clientErr.Error(), testToken) {
		t.Fatal("error text exposed the credential")
	}
}

func TestServiceStructuredErrorIsReturnedWithResult(t *testing.T) {
	const operation = "Pservice409"
	captures := make(chan requestCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusConflict,
			`{"operation_id":"Pservice409","outcome":"not_executed","error":{"code":"session_exists","message":"会话已存在"},"recovery":{"entry":"end","reference":"P"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Start(context.Background(), WithOperationID(operation))
	capture := <-captures
	if got := onlyOperationHeader(t, capture.headers); got != operation {
		t.Fatalf("operation header = %q, want the explicit ID", got)
	}
	if response == nil {
		t.Fatal("structured service error dropped the response")
	}
	clientErr := requireClientError(t, err)
	if clientErr.Code != "session_exists" {
		t.Fatalf("code = %q, want session_exists", clientErr.Code)
	}
	if clientErr.Message != "会话已存在" {
		t.Fatalf("message = %q, want the service message", clientErr.Message)
	}
	if clientErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("HTTPStatus = %d, want 409", clientErr.HTTPStatus)
	}
	if clientErr.OperationID != operation {
		t.Fatal("service error did not keep the request operation ID")
	}
	if clientErr.Transport {
		t.Fatal("service error was reported as transport")
	}
	if clientErr.Result == nil || clientErr.Result.Error == nil {
		t.Fatal("service error did not keep the structured result")
	}
	if response.Result == nil || response.Result.Error == nil {
		t.Fatal("response did not keep the structured result")
	}
	if response.HTTPStatus != http.StatusConflict || response.OperationID != operation {
		t.Fatal("response status or operation ID changed")
	}
}

func TestFailedObservationIsReturnedWithoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusServiceUnavailable,
			`{"operation_id":"Pfailed503","outcome":"executed","observation":{"status":"failed","id":"O1","origin":"Pfailed503","missing":["tree"]},"recovery":{"entry":"observe","reference":"P"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Observe(context.Background(), &protocol.ObserveRequest{
		Reference: "O1",
	}, WithOperationID("Pfailed503"))
	if err != nil {
		t.Fatalf("503 failed observation returned an error: %v", err)
	}
	if response == nil {
		t.Fatal("503 failed observation returned a nil response")
	}
	if response.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("HTTPStatus = %d, want 503", response.HTTPStatus)
	}
	if response.Result == nil || response.Result.Observation == nil {
		t.Fatal("failed observation was not decoded")
	}
	if response.Result.Observation.Status != "failed" {
		t.Fatalf("observation status = %q, want failed", response.Result.Observation.Status)
	}
	if len(response.Result.Observation.Missing) != 1 || response.Result.Observation.Missing[0] != "tree" {
		t.Fatal("failed observation missing fields were not decoded")
	}
}

func TestObserveEncodingIsCanonical(t *testing.T) {
	captures := make(chan requestCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.Observe(context.Background(), &protocol.ObserveRequest{
		Reference: "O1",
		Full:      true,
		Image:     true,
		WaitMS:    250,
	}, WithOperationID("Pobserve"))
	if err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	capture := <-captures
	assertToolRequest(t, capture, "/api/observe")
	const expected = `{"reference":"O1","full":true,"image":true,"wait_ms":250}`
	if capture.body != expected {
		t.Fatalf("observe body = %q, want %q", capture.body, expected)
	}
	for _, member := range []string{`"reference"`, `"full"`, `"image"`, `"wait_ms"`} {
		if strings.Count(capture.body, member) != 1 {
			t.Fatalf("member %s did not appear exactly once", member)
		}
	}
	if got := onlyOperationHeader(t, capture.headers); got != "Pobserve" {
		t.Fatalf("operation header = %q, want Pobserve", got)
	}
}

func TestAppEncodesKindBranches(t *testing.T) {
	cases := []struct {
		name    string
		request *protocol.AppRequest
		body    string
	}{
		{"find-all", &protocol.AppRequest{Kind: protocol.AppKindFind}, `{"kind":"find"}`},
		{"find-query", &protocol.AppRequest{Kind: protocol.AppKindFind, Query: "设置", HasQuery: true}, `{"kind":"find","query":"设置"}`},
		{"list-default", &protocol.AppRequest{Kind: protocol.AppKindList, Reference: "O1"}, `{"kind":"list","reference":"O1"}`},
		{
			"list-primary-filter",
			&protocol.AppRequest{
				Kind: protocol.AppKindList, Reference: "P1",
				Scope: protocol.AppScopePrimary, FilterApp: "com.example", HasFilter: true,
			},
			`{"kind":"list","reference":"P1","scope":"primary","app":"com.example"}`,
		},
		{
			"open",
			&protocol.AppRequest{Kind: protocol.AppKindOpen, Reference: "O1",
				Target: &protocol.AppTarget{App: "com.example"}},
			`{"kind":"open","reference":"O1","target":{"app":"com.example"}}`,
		},
		{
			"claim-task",
			&protocol.AppRequest{
				Kind: protocol.AppKindClaim, Reference: "O1",
				Target: &protocol.AppTarget{TaskID: 42, UseTask: true},
			},
			`{"kind":"claim","reference":"O1","target":{"task_id":42}}`,
		},
		{
			"claim-app",
			&protocol.AppRequest{
				Kind: protocol.AppKindClaim, Reference: "O1",
				Target: &protocol.AppTarget{App: "com.example"},
			},
			`{"kind":"claim","reference":"O1","target":{"app":"com.example"}}`,
		},
		{
			"handoff-restore",
			&protocol.AppRequest{
				Kind: protocol.AppKindHandoff, Reference: "O1",
				Target: &protocol.AppTarget{TaskID: 42, UseTask: true}, RestoreForeground: true,
			},
			`{"kind":"handoff","reference":"O1","target":{"task_id":42},"restore_foreground":true}`,
		},
		{
			"close-borrowed",
			&protocol.AppRequest{
				Kind: protocol.AppKindClose, Reference: "O1",
				Target: &protocol.AppTarget{App: "com.example"}, AllowBorrowed: true,
			},
			`{"kind":"close","reference":"O1","target":{"app":"com.example"},"allow_borrowed":true}`,
		},
		{
			"kill-playback",
			&protocol.AppRequest{
				Kind: protocol.AppKindKill, Reference: "O1",
				Target: &protocol.AppTarget{TaskID: 42, UseTask: true}, AllowStopPlayback: true,
			},
			`{"kind":"kill","reference":"O1","target":{"task_id":42},"allow_stop_playback":true}`,
		},
	}

	captures := make(chan requestCapture, len(cases))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operation := "Papp-" + tc.name
			response, err := client.App(context.Background(), tc.request, WithOperationID(operation))
			if err != nil {
				t.Fatalf("App() error: %v", err)
			}
			capture := <-captures
			assertToolRequest(t, capture, "/api/app")
			if capture.body != tc.body {
				t.Fatalf("app body = %q, want %q", capture.body, tc.body)
			}
			if got := onlyOperationHeader(t, capture.headers); got != operation {
				t.Fatalf("operation header = %q, want %q", got, operation)
			}
			if response == nil || response.OperationID != operation {
				t.Fatal("response did not keep the pre-assigned operation ID")
			}
		})
	}
}

func TestAppRejectsUnencodableBranchesWithoutSending(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	cases := []struct {
		name    string
		request *protocol.AppRequest
	}{
		{"unknown-kind", &protocol.AppRequest{Kind: "bogus"}},
		{"list-missing-reference", &protocol.AppRequest{Kind: protocol.AppKindList}},
		{"open-missing-target", &protocol.AppRequest{Kind: protocol.AppKindOpen, Reference: "O1"}},
		{"claim-missing-target", &protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: "O1"}},
		{
			"kill-package-target",
			&protocol.AppRequest{
				Kind: protocol.AppKindKill, Reference: "O1",
				Target: &protocol.AppTarget{App: "com.example"},
			},
		},
		{
			"find-with-reference",
			&protocol.AppRequest{Kind: protocol.AppKindFind, Reference: "O1"},
		},
		{
			"list-with-target",
			&protocol.AppRequest{
				Kind: protocol.AppKindList, Reference: "O1",
				Target: &protocol.AppTarget{App: "com.example"},
			},
		},
		{
			"open-with-top-level-app",
			&protocol.AppRequest{
				Kind: protocol.AppKindOpen, Reference: "O1",
				FilterApp: "com.example", HasFilter: true,
				Target: &protocol.AppTarget{App: "com.example"},
			},
		},
		{
			"claim-with-close-option",
			&protocol.AppRequest{
				Kind: protocol.AppKindClaim, Reference: "O1",
				Target: &protocol.AppTarget{App: "com.example"}, AllowBorrowed: true,
			},
		},
		{
			"ambiguous-target",
			&protocol.AppRequest{
				Kind: protocol.AppKindClaim, Reference: "O1",
				Target: &protocol.AppTarget{UseTask: true, TaskID: 42, App: "com.example"},
			},
		},
		{
			"invalid-reference",
			&protocol.AppRequest{Kind: protocol.AppKindList, Reference: "X1"},
		},
		{
			"invalid-filter-package",
			&protocol.AppRequest{
				Kind: protocol.AppKindList, Reference: "O1",
				FilterApp: "not a package", HasFilter: true,
			},
		},
		{
			"query-without-presence",
			&protocol.AppRequest{Kind: protocol.AppKindFind, Query: "设置"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := client.App(context.Background(), tc.request, WithOperationID("Papp-reject"))
			if response != nil {
				t.Fatal("unencodable request produced a response")
			}
			clientErr := requireClientError(t, err)
			if clientErr.Code != protocol.CodeInvalidRequest || clientErr.Transport {
				t.Fatalf("code = %q transport=%t, want a caller-side invalid request", clientErr.Code, clientErr.Transport)
			}
			if clientErr.OperationID != "Papp-reject" {
				t.Fatal("caller-side rejection lost the pre-assigned operation ID")
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("unencodable app branches reached the service %d times", hits.Load())
	}
}

func TestAppDecodesFindView(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"operation_id":"Pappfind","app":{"kind":"find","items":[{"name":"设置","package":"com.android.settings"},{"name":"示例应用","package":"com.example"}],"total":7,"truncated":false,"captured_at":"2026-11-12T03:00:00+08:00"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{Kind: protocol.AppKindFind}, WithOperationID("Pappfind"))
	if err != nil {
		t.Fatalf("App() error: %v", err)
	}
	if response == nil || response.Result == nil || response.Result.App == nil {
		t.Fatal("find view was not decoded")
	}
	app := response.Result.App
	if app.Kind != protocol.AppKindFind {
		t.Fatalf("kind = %q, want find", app.Kind)
	}
	if len(app.Items) != 2 || app.Items[0] != (protocol.AppItem{Name: "设置", Package: "com.android.settings"}) ||
		app.Items[1] != (protocol.AppItem{Name: "示例应用", Package: "com.example"}) {
		t.Fatalf("find items differ: %+v", app.Items)
	}
	if !app.HasTotal || app.Total != 7 {
		t.Fatalf("total = %d (present=%t), want 7", app.Total, app.HasTotal)
	}
	if app.Truncated == nil || *app.Truncated {
		t.Fatal("explicit truncated=false was not preserved")
	}
	if app.CapturedAt != "2026-11-12T03:00:00+08:00" {
		t.Fatalf("captured_at = %q", app.CapturedAt)
	}
	if response.Result.Outcome != "" {
		t.Fatalf("query outcome = %q, want omitted", response.Result.Outcome)
	}
}

func TestAppDecodesListView(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"operation_id":"Papplist","app":{"kind":"list","display_id":12,"items":[{"task_id":42,"package":"com.example","ownership":"borrowed","origin_display":0},{"task_id":43,"package":"com.example","ownership":"shared"}],"total":2,"truncated":false,"captured_at":"2026-11-12T03:00:01+08:00","foreground":{"state":"present","package":"com.example","task_id":null}}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{Kind: protocol.AppKindList, Reference: "O1"}, WithOperationID("Papplist"))
	if err != nil {
		t.Fatalf("App() error: %v", err)
	}
	if response == nil || response.Result == nil || response.Result.App == nil {
		t.Fatal("list view was not decoded")
	}
	app := response.Result.App
	if app.Kind != protocol.AppKindList {
		t.Fatalf("kind = %q, want list", app.Kind)
	}
	if app.DisplayID == nil || *app.DisplayID != 12 {
		t.Fatalf("display_id = %v, want 12", app.DisplayID)
	}
	if len(app.TaskItems) != 2 {
		t.Fatalf("list items = %d, want 2", len(app.TaskItems))
	}
	first, second := app.TaskItems[0], app.TaskItems[1]
	if first.TaskID != 42 || first.Ownership != protocol.TaskOwnershipBorrowed ||
		first.OriginDisplay == nil || *first.OriginDisplay != 0 {
		t.Fatalf("first list item differs: %+v", first)
	}
	if second.TaskID != 43 || second.Ownership != protocol.TaskOwnershipShared ||
		second.OriginDisplay != nil {
		t.Fatalf("second list item differs: %+v", second)
	}
	if app.Foreground == nil || app.Foreground.State != "present" ||
		app.Foreground.Package != "com.example" {
		t.Fatalf("foreground differs: %+v", app.Foreground)
	}
	if !app.Foreground.HasTask || app.Foreground.TaskID != nil {
		t.Fatal("foreground task_id=null was not decoded as present-but-unknown")
	}
}

func TestAppDecodesOpenView(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"operation_id":"Pappopen","outcome":"executed","app":{"kind":"open","package":"com.example","task_id":42,"display_id":12,"ownership":"owned","reused":true,"state_preserved":true}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{Kind: protocol.AppKindOpen, Reference: "O1",
			Target: &protocol.AppTarget{App: "com.example"}},
		WithOperationID("Pappopen"))
	if err != nil {
		t.Fatalf("App() error: %v", err)
	}
	if response == nil || response.Result == nil || response.Result.App == nil {
		t.Fatal("open view was not decoded")
	}
	app := response.Result.App
	if app.Kind != protocol.AppKindOpen || app.Package != "com.example" {
		t.Fatalf("open identity differs: %+v", app)
	}
	if app.TaskID == nil || *app.TaskID != 42 || app.DisplayID == nil || *app.DisplayID != 12 {
		t.Fatalf("open task/display differs: %+v", app)
	}
	if app.Ownership != protocol.TaskOwnershipOwned {
		t.Fatalf("ownership = %q, want owned", app.Ownership)
	}
	if app.Reused == nil || !*app.Reused {
		t.Fatal("reused=true was not decoded")
	}
	if !app.HasStatePreserved || app.StatePreserved == nil || !*app.StatePreserved {
		t.Fatal("state_preserved=true was not decoded")
	}
}

func TestAppDecodesClaimAmbiguity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusConflict,
			`{"operation_id":"Pappclaim","outcome":"not_executed","app":{"kind":"claim","package":"com.example","source_display_id":0},"error":{"code":"task_target_ambiguous","message":"主屏存在多个同名应用任务，请用 task_id 精确选择","details":{"task_ids":[42,43]}}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{
			Kind: protocol.AppKindClaim, Reference: "O1",
			Target: &protocol.AppTarget{App: "com.example"},
		}, WithOperationID("Pappclaim"))
	if response == nil {
		t.Fatal("structured ambiguity error dropped the response")
	}
	clientErr := requireClientError(t, err)
	if clientErr.Code != protocol.CodeTaskAmbiguous || clientErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("code = %q HTTP = %d, want task_target_ambiguous/409", clientErr.Code, clientErr.HTTPStatus)
	}
	if clientErr.Result == nil || clientErr.Result.App == nil {
		t.Fatal("ambiguity error lost the app view")
	}
	if clientErr.Result.App.Kind != protocol.AppKindClaim ||
		clientErr.Result.App.SourceDisplayID == nil || *clientErr.Result.App.SourceDisplayID != 0 {
		t.Fatalf("claim source identity differs: %+v", clientErr.Result.App)
	}
	taskIDs, ok := clientErr.Result.Error.Details["task_ids"].([]any)
	if !ok || len(taskIDs) != 2 ||
		taskIDs[0] != float64(42) || taskIDs[1] != float64(43) {
		t.Fatalf("details.task_ids = %#v, want [42,43]", clientErr.Result.Error.Details["task_ids"])
	}
}

// TestActNestedErrorDetailsOverHTTP exercises the actual client transport and
// strict response decoder. Nested JSON objects must stay objects, zero pixel
// coordinates must survive, and the int values written by the service must be
// numerically equal after decoding to float64 and re-encoding.
func TestActNestedErrorDetailsOverHTTP(t *testing.T) {
	cases := []struct {
		name          string
		withCarrier   bool
		withOccluding bool
	}{
		{name: "all evidenced keys", withCarrier: true, withOccluding: true},
		{name: "optional keys omitted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			details := map[string]any{
				"nearest_actionable_ref": "n516",
				"safe_point":             map[string]any{"x": 0, "y": 0},
				"task_ids":               []int{0, 42},
			}
			if tc.withCarrier {
				details["safe_carrier_ref"] = "n491"
			}
			if tc.withOccluding {
				details["occluding_ref"] = "n523"
			}
			source := &protocol.Result{
				OperationID:    "Pactdetails",
				OperationState: "finished",
				Outcome:        "not_executed",
				Error: &protocol.Error{
					Code:    protocol.CodeNodeUnsupported,
					Message: "目标节点不支持点击",
					Details: details,
				},
			}
			sourceJSON, err := protocol.JSON(source)
			if err != nil {
				t.Fatalf("encode service result: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				writeResponse(w, http.StatusUnprocessableEntity, string(sourceJSON))
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			response, err := client.Act(context.Background(), &protocol.ActRequest{
				Reference: "O1",
				Operation: protocol.Operation{
					Type:   "tap",
					Target: &protocol.Target{UseRef: true, Ref: "n491"},
				},
			}, WithOperationID("Pactdetails"))
			if response == nil {
				t.Fatal("structured failure dropped the response")
			}
			clientErr := requireClientError(t, err)
			if clientErr.Code != protocol.CodeNodeUnsupported ||
				clientErr.HTTPStatus != http.StatusUnprocessableEntity ||
				clientErr.Result == nil || clientErr.Result.Error == nil {
				t.Fatalf("structured failure changed: %+v", clientErr)
			}
			gotDetails := clientErr.Result.Error.Details
			safePoint, ok := gotDetails["safe_point"].(map[string]any)
			if !ok {
				t.Fatalf("safe_point decoded as %T, want nested object", gotDetails["safe_point"])
			}
			if safePoint["x"] != float64(0) || safePoint["y"] != float64(0) {
				t.Fatalf("zero coordinates changed: %#v", safePoint)
			}
			if _, present := gotDetails["safe_carrier_ref"]; present != tc.withCarrier {
				t.Fatalf("safe_carrier_ref presence = %t, want %t", present, tc.withCarrier)
			}
			if _, present := gotDetails["occluding_ref"]; present != tc.withOccluding {
				t.Fatalf("occluding_ref presence = %t, want %t", present, tc.withOccluding)
			}

			roundTripped, err := protocol.JSON(clientErr.Result)
			if err != nil {
				t.Fatalf("re-encode decoded result: %v", err)
			}
			var want, got any
			if err := jsonv2.Unmarshal(sourceJSON, &want); err != nil {
				t.Fatalf("decode source JSON: %v", err)
			}
			if err := jsonv2.Unmarshal(roundTripped, &got); err != nil {
				t.Fatalf("decode round-tripped JSON: %v", err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("JSON values changed across HTTP:\nwant %#v\n got %#v", want, got)
			}
		})
	}
}

func TestAppDecodesHandoffNullStatePreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"operation_id":"Papphandoff","outcome":"executed","app":{"kind":"handoff","package":"com.example","task_id":42,"source_display_id":12,"display_id":0,"ownership":"shared","state_preserved":null,"foreground_restore":"not_requested"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{
			Kind: protocol.AppKindHandoff, Reference: "O1",
			Target: &protocol.AppTarget{TaskID: 42, UseTask: true},
		}, WithOperationID("Papphandoff"))
	if err != nil {
		t.Fatalf("App() error: %v", err)
	}
	if response == nil || response.Result == nil || response.Result.App == nil {
		t.Fatal("handoff view was not decoded")
	}
	app := response.Result.App
	if app.Kind != protocol.AppKindHandoff || app.Ownership != protocol.TaskOwnershipShared {
		t.Fatalf("handoff identity differs: %+v", app)
	}
	if app.SourceDisplayID == nil || *app.SourceDisplayID != 12 {
		t.Fatalf("source_display_id = %v, want 12", app.SourceDisplayID)
	}
	if app.DisplayID == nil || *app.DisplayID != 0 {
		t.Fatalf("display_id = %v, want the proven primary display 0", app.DisplayID)
	}
	if !app.HasStatePreserved || app.StatePreserved != nil {
		t.Fatal("explicit state_preserved=null was not preserved")
	}
	if app.ForegroundRestore != protocol.ForegroundRestoreNotRequested {
		t.Fatalf("foreground_restore = %q", app.ForegroundRestore)
	}
	if app.Reused != nil {
		t.Fatal("handoff carried an open-only reused member")
	}
}

func TestAppDecodesCloseTasks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusConflict,
			`{"operation_id":"Pappclose","outcome":"uncertain","app":{"kind":"close","display_id":12,"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},{"task_id":43,"outcome":"uncertain","recents_cleaned":null},{"task_id":44,"outcome":"not_executed"}],"complete":false},"error":{"code":"task_remove_unconfirmed","message":"部分任务的关闭结果未确认，请先核验现状"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{
			Kind: protocol.AppKindClose, Reference: "O1",
			Target: &protocol.AppTarget{TaskID: 42, UseTask: true},
		}, WithOperationID("Pappclose"))
	if response == nil {
		t.Fatal("structured close error dropped the response")
	}
	if clientErr := requireClientError(t, err); clientErr.Code != protocol.CodeAppTaskUnconfirmed {
		t.Fatalf("code = %q, want task_remove_unconfirmed", clientErr.Code)
	}
	app := response.Result.App
	if app == nil || app.Kind != protocol.AppKindClose {
		t.Fatal("close view was not decoded")
	}
	if app.DisplayID == nil || *app.DisplayID != 12 {
		t.Fatalf("close display_id = %v, want 12", app.DisplayID)
	}
	if !app.HasTasks || len(app.Tasks) != 3 {
		t.Fatalf("close tasks = %d (present=%t), want 3", len(app.Tasks), app.HasTasks)
	}
	executed, uncertain, notExecuted := app.Tasks[0], app.Tasks[1], app.Tasks[2]
	if executed.TaskID != 42 || executed.Outcome != "executed" ||
		executed.RecentsCleaned == nil || !*executed.RecentsCleaned {
		t.Fatalf("executed close item differs: %+v", executed)
	}
	if uncertain.TaskID != 43 || uncertain.Outcome != "uncertain" || uncertain.RecentsCleaned != nil {
		t.Fatalf("uncertain close item differs: %+v", uncertain)
	}
	if notExecuted.TaskID != 44 || notExecuted.Outcome != "not_executed" || notExecuted.RecentsCleaned != nil {
		t.Fatalf("not_executed close item differs: %+v", notExecuted)
	}
	if app.Complete == nil || *app.Complete {
		t.Fatal("complete=false was not preserved")
	}
}

func TestAppDecodesKillPIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"operation_id":"Pappkill","outcome":"executed","app":{"kind":"kill","package":"com.example","source_task_id":42,"display_id":12,"target_pids":[1234,1235],"killed_pids":[1234,1235],"remaining_pids":[],"restarted":null}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.App(context.Background(),
		&protocol.AppRequest{
			Kind: protocol.AppKindKill, Reference: "O1",
			Target: &protocol.AppTarget{TaskID: 42, UseTask: true},
		}, WithOperationID("Pappkill"))
	if err != nil {
		t.Fatalf("App() error: %v", err)
	}
	if response == nil || response.Result == nil || response.Result.App == nil {
		t.Fatal("kill view was not decoded")
	}
	app := response.Result.App
	if app.Kind != protocol.AppKindKill || app.Package != "com.example" {
		t.Fatalf("kill identity differs: %+v", app)
	}
	if app.SourceTaskID == nil || *app.SourceTaskID != 42 ||
		app.DisplayID == nil || *app.DisplayID != 12 {
		t.Fatalf("kill task/display differs: %+v", app)
	}
	if !app.HasTargetPIDs || len(app.TargetPIDs) != 2 ||
		app.TargetPIDs[0] != 1234 || app.TargetPIDs[1] != 1235 {
		t.Fatalf("target_pids = %v (present=%t)", app.TargetPIDs, app.HasTargetPIDs)
	}
	if !app.HasKilledPIDs || len(app.KilledPIDs) != 2 ||
		app.KilledPIDs[0] != 1234 || app.KilledPIDs[1] != 1235 {
		t.Fatalf("killed_pids = %v (present=%t)", app.KilledPIDs, app.HasKilledPIDs)
	}
	if !app.HasRemainingPIDs || app.RemainingPIDs == nil || len(app.RemainingPIDs) != 0 {
		t.Fatalf("remaining_pids = %v (present=%t), want a present empty array", app.RemainingPIDs, app.HasRemainingPIDs)
	}
	if !app.HasRestarted || app.Restarted != nil {
		t.Fatal("explicit restarted=null was not preserved")
	}
}

// TestActDecodesEffectAndQuality pins the contract-10 response decoder behind
// Client: effect.state and observation.quality.state/samples/stop_reason keep
// the same values, including the valid explicit samples=0 fact. The client is
// one decode hop, not a second projection layer.
func TestActDecodesEffectAndQuality(t *testing.T) {
	cases := []struct {
		name    string
		effect  string
		quality string
		samples int
		stop    string
	}{
		{"observed_unstable", "observed", "unstable", 3, "max_samples"},
		{"not_observed_stable", "not_observed", "stable", 5, "stable"},
		{"unknown_not_sampled", "unknown", "unknown", 0, "not_sampled"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"operation_id":"Pscroll","outcome":"executed","phase":"completed",`+
					`"effect":{"state":%q},"target_reference":"O1",`+
					`"observation":{"status":"complete","id":"O2","origin":"Pscroll","revision":2,`+
					`"captured_at":"2026-11-10T00:00:00Z","actionable_until":"2026-11-10T00:01:00Z",`+
					`"quality":{"state":%q,"samples":%d,"stop_reason":%q},`+
					`"screen":{"width":1080,"height":2400,"rotation":0}}}`,
				testCase.effect, testCase.quality, testCase.samples, testCase.stop)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				writeResponse(w, http.StatusOK, body)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			response, err := client.Act(context.Background(), &protocol.ActRequest{
				Reference: "O1",
				Operation: protocol.Operation{
					Type:      "scroll",
					Container: &protocol.Target{UseRef: true, Ref: "n372"},
					Direction: protocol.ScrollDirectionDown,
					Amount:    protocol.ScrollAmountPage,
				},
			}, WithOperationID("Pscroll"))
			if err != nil {
				t.Fatalf("Act() error: %v", err)
			}
			if response == nil || response.Result == nil || response.Result.Observation == nil {
				t.Fatal("scroll result was not decoded")
			}
			result := response.Result
			if result.Effect == nil || result.Effect.State != testCase.effect {
				t.Fatalf("effect = %+v, want state %q", result.Effect, testCase.effect)
			}
			quality := result.Observation.Quality
			if quality == nil {
				t.Fatal("observation.quality was not decoded")
			}
			if quality.State != testCase.quality || quality.Samples != testCase.samples ||
				quality.StopReason != testCase.stop {
				t.Fatalf("quality = %+v, want %s/%d/%s",
					quality, testCase.quality, testCase.samples, testCase.stop)
			}
			if result.Phase != "completed" || result.TargetReference != "O1" {
				t.Fatalf("surrounding facts changed: %+v", result)
			}
		})
	}
}

// TestRejectsInvalidEffectAndQualityResponses proves the decoder behind Client
// rejects unknown enums, an explicit-null quality object, and absent or
// negative samples instead of manufacturing a quality fact.
func TestRejectsInvalidEffectAndQualityResponses(t *testing.T) {
	bodies := []struct {
		name string
		body string
	}{
		{"unknown quality state", `{"observation":{"status":"complete","id":"O1","quality":{"state":"bogus","samples":1,"stop_reason":"stable"}}}`},
		{"unknown quality stop reason", `{"observation":{"status":"complete","id":"O1","quality":{"state":"stable","samples":1,"stop_reason":"bogus"}}}`},
		{"unknown effect state", `{"effect":{"state":"bogus"}}`},
		{"null quality object", `{"observation":{"status":"complete","id":"O1","quality":null}}`},
		{"missing samples", `{"observation":{"status":"complete","id":"O1","quality":{"state":"stable","stop_reason":"stable"}}}`},
		{"negative samples", `{"observation":{"status":"complete","id":"O1","quality":{"state":"stable","samples":-1,"stop_reason":"stable"}}}`},
		{"null effect object", `{"effect":null}`},
	}
	var index atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		current := int(index.Add(1)) - 1
		if current >= len(bodies) {
			current = len(bodies) - 1
		}
		writeResponse(w, http.StatusOK, bodies[current].body)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	for _, testCase := range bodies {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := client.Observe(context.Background(), &protocol.ObserveRequest{
				Reference: "O1",
			}, WithOperationID("Pbadquality"))
			if response != nil {
				t.Fatalf("invalid response produced a result: %+v", response)
			}
			clientErr := requireClientError(t, err)
			if clientErr.Code != ErrorResponse {
				t.Fatalf("code = %q, want %q", clientErr.Code, ErrorResponse)
			}
			if clientErr.OperationID != "Pbadquality" {
				t.Fatal("decode rejection lost the operation ID")
			}
		})
	}
}

func TestStatusEncodingAndNoOperation(t *testing.T) {
	captures := make(chan requestCapture, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{"can_start":true}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	first, err := client.Status(context.Background(), "")
	if err != nil {
		t.Fatalf("Status(\"\") error: %v", err)
	}
	second, err := client.Status(context.Background(), "O1")
	if err != nil {
		t.Fatalf("Status(\"O1\") error: %v", err)
	}
	emptyCapture := <-captures
	referenceCapture := <-captures

	assertToolRequest(t, emptyCapture, "/api/status")
	assertNoOperationHeader(t, emptyCapture.headers)
	if emptyCapture.body != "{}" {
		t.Fatalf("status() body = %q, want {}", emptyCapture.body)
	}
	assertToolRequest(t, referenceCapture, "/api/status")
	assertNoOperationHeader(t, referenceCapture.headers)
	if referenceCapture.body != `{"reference":"O1"}` {
		t.Fatalf("status(reference) body = %q, want reference object", referenceCapture.body)
	}
	if first.OperationID != "" || second.OperationID != "" {
		t.Fatal("status reported an operation ID")
	}
}

func TestInspectAndDiagnoseNeverAllocateOperation(t *testing.T) {
	captures := make(chan requestCapture, 3)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/inspect", func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{}`)
	})
	mux.HandleFunc("/api/diagnose", func(w http.ResponseWriter, request *http.Request) {
		captures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK, `{}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL)
	inspectResponse, err := client.Inspect(context.Background(), &protocol.InspectRequest{
		Reference: "O2",
		HasNode:   true,
		Node:      "n7",
	})
	if err != nil {
		t.Fatalf("Inspect() error: %v", err)
	}
	diagnoseResponse, err := client.Diagnose(context.Background(), &protocol.DiagnoseRequest{
		HasRef:    true,
		Reference: "O2",
	})
	if err != nil {
		t.Fatalf("Diagnose(reference) error: %v", err)
	}
	emptyDiagnoseResponse, err := client.Diagnose(context.Background(), &protocol.DiagnoseRequest{})
	if err != nil {
		t.Fatalf("Diagnose() error: %v", err)
	}
	inspectCapture := <-captures
	diagnoseCapture := <-captures
	emptyDiagnoseCapture := <-captures

	assertToolRequest(t, inspectCapture, "/api/inspect")
	assertNoOperationHeader(t, inspectCapture.headers)
	if inspectCapture.body != `{"reference":"O2","node":"n7"}` {
		t.Fatalf("inspect body = %q", inspectCapture.body)
	}
	assertToolRequest(t, diagnoseCapture, "/api/diagnose")
	assertNoOperationHeader(t, diagnoseCapture.headers)
	if diagnoseCapture.body != `{"reference":"O2"}` {
		t.Fatalf("diagnose body = %q", diagnoseCapture.body)
	}
	assertToolRequest(t, emptyDiagnoseCapture, "/api/diagnose")
	assertNoOperationHeader(t, emptyDiagnoseCapture.headers)
	if emptyDiagnoseCapture.body != "{}" {
		t.Fatalf("diagnose() body = %q, want {}", emptyDiagnoseCapture.body)
	}
	if inspectResponse.OperationID != "" || diagnoseResponse.OperationID != "" ||
		emptyDiagnoseResponse.OperationID != "" {
		t.Fatal("read-only tool reported an operation ID")
	}
}

func TestSnapImageDecodedAndCarrierCleared(t *testing.T) {
	imageBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46}
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	body := fmt.Sprintf(`{"operation_id":"Psnap","image":{"captured_at":"2026-11-18T00:00:00Z","width":461,"height":1024,"media_type":"image/jpeg","read_only":true,"screen":{"width":1080,"height":2400,"rotation":0},"base64":"%s"}}`, encoded)
	var capture requestCapture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		capture = captureRequest(t, request)
		writeResponse(w, http.StatusOK, body)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Snap(context.Background(), &protocol.SnapRequest{Reference: "O1"},
		WithOperationID("Psnap"))
	if err != nil {
		t.Fatalf("Snap() error: %v", err)
	}
	assertToolRequest(t, capture, "/api/snap")
	if capture.body != `{"reference":"O1"}` {
		t.Fatalf("snap body = %q", capture.body)
	}
	if capture.headers.Get(operationHeader) != "Psnap" {
		t.Fatal("snap did not send the pre-assigned operation ID")
	}
	if response.OperationID != "Psnap" || response.HTTPStatus != http.StatusOK {
		t.Fatalf("response identity = %+v", response)
	}
	if response.Image == nil || !bytes.Equal(response.Image.Data, imageBytes) {
		t.Fatal("snap image was not decoded")
	}
	if response.Image.MediaType != "image/jpeg" || response.Image.Width != 461 ||
		response.Image.Height != 1024 || !response.Image.ReadOnly ||
		response.Image.CapturedAt != "2026-11-18T00:00:00Z" ||
		response.Image.Screen == nil || response.Image.Screen.Width != 1080 {
		t.Fatalf("snap image metadata = %+v", response.Image)
	}
	if response.Result.Image == nil || response.Result.Image.Base64 != "" {
		t.Fatal("snap metadata was not retained without the base64 carrier")
	}
}

func TestSnapRejectsInvalidSuccessShapes(t *testing.T) {
	baseImage := `"captured_at":"2026-11-18T00:00:00Z","width":461,"height":1024,"media_type":"image/jpeg","read_only":true,"screen":{"width":1080,"height":2400,"rotation":0},"base64":"AAAA"`
	mutate := func(old, replacement string) string {
		return strings.Replace(baseImage, old, replacement, 1)
	}
	imageBodies := []string{
		mutate(`"base64":"AAAA"`, `"base64":""`),
		mutate(`image/jpeg`, `image/png`),
		mutate(`"read_only":true`, `"read_only":false`),
		mutate(`"captured_at":"2026-11-18T00:00:00Z"`, `"captured_at":""`),
		mutate(`"captured_at":"2026-11-18T00:00:00Z"`, `"captured_at":"yesterday"`),
		mutate(`"width":461`, `"width":0`),
		mutate(`"height":1024`, `"height":0`),
		mutate(`{"width":1080,"height":2400,"rotation":0}`, `{"width":0,"height":2400,"rotation":0}`),
		mutate(`{"width":1080,"height":2400,"rotation":0}`, `{"width":1080,"height":2400,"rotation":4}`),
		mutate(`"screen":{"width":1080,"height":2400,"rotation":0}`, `"screen":null`),
		mutate(`"base64":"AAAA"`, `"base64":"!!!"`),
		mutate(`"base64":"AAAA"`, `"base64":"AAAA","error":{"code":"x","message":"y"}`),
	}
	bodies := make([]string, 0, len(imageBodies)+3)
	for _, image := range imageBodies {
		bodies = append(bodies, `{"operation_id":"P1","image":{`+image+`}}`)
	}
	bodies = append(bodies,
		`{"operation_id":"P1","image":{`+baseImage+`},"observation":{"status":"complete","id":"O1"}}`,
		`{"operation_id":"P1","image":{`+baseImage+`},"error":{"code":"x","message":"y"}}`,
		`{"operation_id":"P1","image":{`+baseImage+`},"outcome":"executed"}`,
	)
	var index atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		current := int(index.Add(1)) - 1
		if current >= len(bodies) {
			current = len(bodies) - 1
		}
		writeResponse(w, http.StatusOK, bodies[current])
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	for caseIndex := range bodies {
		response, err := client.Snap(context.Background(),
			&protocol.SnapRequest{Reference: "O1"}, WithOperationID("Pbad"))
		if response != nil {
			t.Fatalf("case %d: invalid shape produced a response: %+v", caseIndex, response)
		}
		clientErr := requireClientError(t, err)
		if clientErr.Code != ErrorResponse {
			t.Fatalf("case %d: code = %q, want %q", caseIndex, clientErr.Code, ErrorResponse)
		}
	}
}

// TestSnapDuplicateSummaryWithoutImageDecodes pins the allowed no-image summary:
// a duplicate P replay is a normal result, just not an MCP success.
func TestSnapDuplicateSummaryWithoutImageDecodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK, `{"operation_id":"P1","operation_state":"finished","phase":"finished"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Snap(context.Background(),
		&protocol.SnapRequest{Reference: "O1"}, WithOperationID("P1"))
	if err != nil {
		t.Fatalf("Snap() error: %v", err)
	}
	if response.Image != nil || response.Result.Image != nil {
		t.Fatal("duplicate summary carried an image")
	}
	if response.Result.OperationState != "finished" || response.Result.Phase != "finished" {
		t.Fatalf("duplicate summary = %+v", response.Result)
	}
}

func TestObservationImageDecodedAndCarrierCleared(t *testing.T) {
	imageBytes := []byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0xff}
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	body := fmt.Sprintf(`{"operation_id":"Pimage","observation":{"status":"complete","id":"O2","image":{"captured_at":"2026-01-01T00:00:00Z","width":32,"height":64,"media_type":"image/jpeg","read_only":true,"screen":{"width":1080,"height":2400,"rotation":1},"base64":"%s"}}}`, encoded)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK, body)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Observe(context.Background(), &protocol.ObserveRequest{
		Reference: "O1",
	}, WithOperationID("Pimage"))
	if err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	if response.Image == nil {
		t.Fatal("observation image was not decoded")
	}
	if !bytes.Equal(response.Image.Data, imageBytes) {
		t.Fatal("decoded image bytes differ")
	}
	if response.Image.MediaType != "image/jpeg" || response.Image.Width != 32 || response.Image.Height != 64 {
		t.Fatal("decoded image metadata differs")
	}
	if response.Image.CapturedAt != "2026-01-01T00:00:00Z" || !response.Image.ReadOnly {
		t.Fatal("decoded image facts differ")
	}
	if response.Image.Screen == nil || response.Image.Screen.Rotation != 1 {
		t.Fatal("decoded image screen differs")
	}
	if response.Result == nil || response.Result.Observation == nil || response.Result.Observation.Image == nil {
		t.Fatal("structured image metadata is missing")
	}
	if response.Result.Observation.Image.Base64 != "" {
		t.Fatal("base64 carrier was not cleared from the structured result")
	}
}

func TestMalformedImageCarrierIsInvalidResponse(t *testing.T) {
	bodies := []string{
		`{"observation":{"status":"complete","image":{"width":32,"height":64,"media_type":"image/jpeg","base64":"!!!"}}}`,
		`{"observation":{"status":"complete","image":{"width":32,"height":64,"base64":"AAAA"}}}`,
		`{"observation":{"status":"complete","image":{"width":0,"height":64,"media_type":"image/jpeg","base64":"AAAA"}}}`,
		`{"observation":{"status":"complete","image":{"width":32,"height":0,"media_type":"image/jpeg","base64":"AAAA"}}}`,
	}
	var index atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		current := int(index.Add(1)) - 1
		if current >= len(bodies) {
			current = len(bodies) - 1
		}
		writeResponse(w, http.StatusOK, bodies[current])
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	for caseIndex := range bodies {
		response, err := client.Observe(context.Background(), &protocol.ObserveRequest{
			Reference: "O1",
		}, WithOperationID("Pbadimage"))
		if response != nil {
			t.Fatalf("case %d: malformed image produced a response", caseIndex)
		}
		clientErr := requireClientError(t, err)
		if clientErr.Code != ErrorResponse {
			t.Fatalf("case %d: code = %q, want %q", caseIndex, clientErr.Code, ErrorResponse)
		}
		if clientErr.HTTPStatus != http.StatusOK {
			t.Fatalf("case %d: HTTPStatus = %d, want 200", caseIndex, clientErr.HTTPStatus)
		}
		if clientErr.OperationID != "Pbadimage" {
			t.Fatalf("case %d: operation ID was not retained", caseIndex)
		}
	}
}

func TestObservationImageWithoutCarrierIsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"observation":{"status":"complete","image":{"width":32,"height":64,"media_type":"image/jpeg"}}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Observe(context.Background(), &protocol.ObserveRequest{
		Reference: "O1",
	})
	if err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	if response.Image != nil {
		t.Fatal("image without a base64 carrier was projected")
	}
	if response.Result.Observation.Image.Base64 != "" {
		t.Fatal("unexpected base64 carrier")
	}
}

// TestObservationTreeTriStateDecode pins the client as a transparent wire
// consumer: a missing complete-node state member restores the contract default,
// explicit null stays unknown, and explicit non-default values stay false/true.
// Re-encoding must omit only the known defaults and retain every null.
func TestObservationTreeTriStateDecode(t *testing.T) {
	const responseBody = `{"observation":{"status":"complete","id":"O1","tree":{` +
		`"format":"full","scope":"reported_visible_with_context","view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400],"children":[` +
		`{"ref":"n2","role":"text","bounds":[0,0,100,100]},` +
		`{"ref":"n3","role":"text","bounds":[0,0,100,100],` +
		`"enabled":null,"focused":null,"selected":null,"checked":null,"protected":null},` +
		`{"ref":"n4","role":"button","bounds":[0,0,100,100],` +
		`"enabled":false,"focused":true,"selected":true,"checked":true,"protected":true}` +
		`]}}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK, responseBody)
	}))
	defer server.Close()

	response, err := newTestClient(t, server.URL).Status(context.Background(), "O1")
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if response == nil || response.Result == nil || response.Result.Observation == nil ||
		response.Result.Observation.Tree == nil || response.Result.Observation.Tree.Full == nil ||
		response.Result.Observation.Tree.Full.Root == nil {
		t.Fatal("full observation tree was not decoded")
	}
	children := response.Result.Observation.Tree.Full.Root.Children
	if len(children) != 3 {
		t.Fatalf("child count = %d, want 3", len(children))
	}
	byRef := make(map[string]*protocol.ReadNode, len(children))
	for _, child := range children {
		byRef[child.Ref] = child
	}

	defaults := byRef["n2"]
	if defaults == nil || defaults.Enabled == nil || !*defaults.Enabled {
		t.Fatal("missing enabled did not restore known true")
	}
	for name, value := range map[string]*bool{
		"focused": defaults.Focused, "selected": defaults.Selected,
		"checked": defaults.Checked, "protected": defaults.Protected,
	} {
		if value == nil || *value {
			t.Fatalf("missing %s did not restore known false", name)
		}
	}

	unknown := byRef["n3"]
	if unknown == nil || unknown.Enabled != nil || unknown.Focused != nil ||
		unknown.Selected != nil || unknown.Checked != nil || unknown.Protected != nil {
		t.Fatal("explicit null was not preserved as unknown")
	}

	explicit := byRef["n4"]
	if explicit == nil || explicit.Enabled == nil || *explicit.Enabled {
		t.Fatal("explicit enabled=false was not preserved")
	}
	for name, value := range map[string]*bool{
		"focused": explicit.Focused, "selected": explicit.Selected,
		"checked": explicit.Checked, "protected": explicit.Protected,
	} {
		if value == nil || !*value {
			t.Fatalf("explicit %s=true was not preserved", name)
		}
	}

	encoded, err := protocol.JSON(response.Result)
	if err != nil {
		t.Fatalf("protocol.JSON: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, "defaults") {
		t.Fatal("client re-encode emitted a defaults declaration")
	}
	for _, member := range []string{"enabled", "focused", "selected", "checked", "protected"} {
		if !strings.Contains(text, `"`+member+`":null`) {
			t.Fatalf("explicit null %s was not retained: %s", member, text)
		}
	}
	defaultNode, err := protocol.JSON(defaults)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"enabled", "focused", "selected", "checked", "protected"} {
		if strings.Contains(string(defaultNode), `"`+member+`"`) {
			t.Fatalf("known default %s was not omitted: %s", member, defaultNode)
		}
	}
	if !strings.Contains(text, `"enabled":false`) {
		t.Fatalf("explicit enabled=false was not retained: %s", text)
	}
}

// TestTargetChangedKeepsDeliveredObservationAndRetry pins the client's error
// branch: a structured rejection may carry the new observation and the retry
// entry. The client performs exactly one request and never retries by itself.
func TestTargetChangedKeepsDeliveredObservationAndRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/api/act" {
			t.Errorf("path = %s, want /api/act", request.URL.Path)
		}
		if got := request.Header.Get(operationHeader); !strings.HasPrefix(got, "P") {
			t.Errorf("operation header = %q", got)
		}
		writeResponse(w, http.StatusConflict,
			`{"operation_id":"P7","operation_state":"finished","outcome":"not_executed","phase":"revalidate",`+
				`"observation":{"status":"complete","id":"O2","origin":"P1","revision":2,"tree":{`+
				`"format":"full","scope":"reported_visible_with_context","view":"compact",`+
				`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}},`+
				`"error":{"code":"target_changed","message":"目标元素已变化，本次未派发"},`+
				`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	}))
	defer server.Close()

	response, err := newTestClient(t, server.URL).Act(context.Background(), &protocol.ActRequest{
		Reference: "O1",
		Operation: protocol.Operation{
			Type: string(protocol.ActionTap),
			Target: &protocol.Target{
				Ref: "n1", UseRef: true,
			},
		},
	}, WithOperationID("Pclient"))
	clientErr := requireClientError(t, err)
	if response == nil || response.Result == nil {
		t.Fatal("service rejection did not retain a response result")
	}
	if clientErr.Code != protocol.CodeTargetChanged || clientErr.HTTPStatus != http.StatusConflict {
		t.Fatalf("client error = %+v, want target_changed/409", clientErr)
	}
	if clientErr.Result != response.Result {
		t.Fatal("typed error and response do not share the decoded result")
	}
	if response.Result.Observation == nil || response.Result.Observation.ID != "O2" {
		t.Fatalf("delivered observation = %+v, want O2", response.Result.Observation)
	}
	if response.Result.Recovery == nil || response.Result.Recovery.Entry != protocol.RecoveryEntryRetry ||
		response.Result.Recovery.Reference != protocol.RecoveryReferenceO {
		t.Fatalf("recovery = %+v, want retry/O", response.Result.Recovery)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want exactly one (no automatic retry)", got)
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		remaining := int64(maxResponseBytes + 1)
		chunk := bytes.Repeat([]byte("x"), 256<<10)
		for remaining > 0 {
			size := int64(len(chunk))
			if size > remaining {
				size = remaining
			}
			if _, err := w.Write(chunk[:size]); err != nil {
				return
			}
			remaining -= size
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	response, err := client.Start(context.Background(), WithOperationID("Poversize"))
	if response != nil {
		t.Fatal("oversized response produced a response")
	}
	clientErr := requireClientError(t, err)
	if clientErr.Code != ErrorTooLarge {
		t.Fatalf("code = %q, want %q", clientErr.Code, ErrorTooLarge)
	}
	if clientErr.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, want 200", clientErr.HTTPStatus)
	}
	if clientErr.OperationID != "Poversize" {
		t.Fatal("oversized response lost the operation ID")
	}
}

func TestContextCancellationRetainsOperation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		close(started)
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	client := newTestClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := make(chan struct{})
	go func() {
		defer close(canceled)
		select {
		case <-started:
		case <-time.After(2 * time.Second):
		}
		cancel()
	}()

	response, err := client.Start(ctx, WithOperationID("Pcanceled"))
	<-canceled
	if response != nil {
		t.Fatal("canceled call produced a response")
	}
	clientErr := requireClientError(t, err)
	if !clientErr.Transport || clientErr.Code != ErrorTransport {
		t.Fatal("cancellation was not reported as a transport error")
	}
	if clientErr.OperationID != "Pcanceled" {
		t.Fatal("cancellation lost the operation ID")
	}
	if clientErr.HTTPStatus != 0 {
		t.Fatalf("HTTPStatus = %d, want 0", clientErr.HTTPStatus)
	}
}

func TestConfiguredTimeoutIsApplied(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	client := newTestClient(t, server.URL, func(config *Config) {
		config.Timeout = 100 * time.Millisecond
	})
	begin := time.Now()
	response, err := client.Start(context.Background(), WithOperationID("Ptimeout"))
	elapsed := time.Since(begin)
	if response != nil {
		t.Fatal("timed out call produced a response")
	}
	clientErr := requireClientError(t, err)
	if !clientErr.Transport || clientErr.Code != ErrorTransport {
		t.Fatal("timeout was not reported as a transport error")
	}
	if clientErr.OperationID != "Ptimeout" {
		t.Fatal("timeout lost the operation ID")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call did not respect the configured deadline (elapsed %s)", elapsed)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, "/target", http.StatusFound)
	})
	mux.HandleFunc("/target", func(w http.ResponseWriter, request *http.Request) {
		targetHits.Add(1)
		writeResponse(w, http.StatusOK, `{}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	shared := &http.Client{}
	client := newTestClient(t, server.URL, func(config *Config) {
		config.HTTPClient = shared
	})
	if shared.CheckRedirect != nil {
		t.Fatal("New mutated the caller-supplied HTTP client")
	}
	response, err := client.Start(context.Background(), WithOperationID("Predirect"))
	if response != nil {
		t.Fatal("redirect produced a response")
	}
	clientErr := requireClientError(t, err)
	if clientErr.Code != ErrorResponse {
		t.Fatalf("code = %q, want %q", clientErr.Code, ErrorResponse)
	}
	if clientErr.HTTPStatus != http.StatusFound {
		t.Fatalf("HTTPStatus = %d, want 302", clientErr.HTTPStatus)
	}
	if clientErr.OperationID != "Predirect" {
		t.Fatal("redirect lost the operation ID")
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target was requested")
	}
}

func TestDiscoverBuildsInstance(t *testing.T) {
	healthCaptures := make(chan requestCapture, 1)
	statusCaptures := make(chan requestCapture, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, request *http.Request) {
		healthCaptures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK,
			fmt.Sprintf(`{"status":"ok","contract_version":"%s","service_instance_id":"instance-1","release_version":"0.1.0-dev"}`, protocol.ContractVersion))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, request *http.Request) {
		statusCaptures <- captureRequest(t, request)
		writeResponse(w, http.StatusOK,
			`{"can_start":false,"blocking_code":"device_unavailable","start_operation_id":"P1","session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent"},"cleanup":{"state":"unconfirmed","execution":"confirmed","tasks":"confirmed","recents":"not_applicable","display":"unconfirmed"}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL)
	instance, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}
	healthCapture := <-healthCaptures
	statusCapture := <-statusCaptures

	if healthCapture.method != http.MethodGet || healthCapture.path != "/health" {
		t.Fatal("health request did not use GET /health")
	}
	if healthCapture.headers.Get(authorizationHeader) != "" {
		t.Fatal("health request carried an Authorization header")
	}
	assertToolRequest(t, statusCapture, "/api/status")
	assertNoOperationHeader(t, statusCapture.headers)
	if statusCapture.body != "{}" {
		t.Fatalf("status body = %q, want {}", statusCapture.body)
	}

	if instance == nil {
		t.Fatal("Discover() returned a nil instance")
	}
	if instance.ContractVersion != protocol.ContractVersion || instance.ServiceInstanceID != "instance-1" ||
		instance.ReleaseVersion != "0.1.0-dev" {
		t.Fatal("discovered service identity differs")
	}
	if instance.CanStart {
		t.Fatal("instance reported can_start=true")
	}
	if instance.BlockingCode != "device_unavailable" {
		t.Fatalf("blocking code = %q", instance.BlockingCode)
	}
	if instance.StartOperationID != "P1" {
		t.Fatal("start operation ID was not decoded")
	}
	if instance.Session == nil || instance.Session.State != "ended" {
		t.Fatal("session was not decoded")
	}
	if instance.Cleanup == nil || instance.Cleanup.State != "unconfirmed" {
		t.Fatal("cleanup was not decoded")
	}
}

func TestDiscoverCanStart(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			fmt.Sprintf(`{"status":"ok","contract_version":"%s","service_instance_id":"instance-2","release_version":"0.1.0-dev"}`, protocol.ContractVersion))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK, `{"can_start":true}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL)
	instance, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}
	if !instance.CanStart {
		t.Fatal("instance did not report can_start=true")
	}
	if instance.BlockingCode != "" || instance.Session != nil || instance.Cleanup != nil {
		t.Fatal("healthy instance carried blocking facts")
	}
}

func TestDiscoverRejectsContractMismatch(t *testing.T) {
	var statusHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			`{"status":"ok","contract_version":"0","service_instance_id":"instance-1","release_version":"0.1.0-dev"}`)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, request *http.Request) {
		statusHits.Add(1)
		writeResponse(w, http.StatusOK, `{"can_start":true}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL)
	instance, err := client.Discover(context.Background())
	if instance != nil {
		t.Fatal("contract mismatch produced an instance")
	}
	clientErr := requireClientError(t, err)
	if clientErr.Code != protocol.CodeContractMismatch {
		t.Fatalf("code = %q, want %q", clientErr.Code, protocol.CodeContractMismatch)
	}
	if clientErr.OperationID != "" {
		t.Fatal("discovery error carried an operation ID")
	}
	if statusHits.Load() != 0 {
		t.Fatal("status was requested despite a contract mismatch")
	}
}

func TestDiscoverRejectsUnreadyHealth(t *testing.T) {
	var statusHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, request *http.Request) {
		writeResponse(w, http.StatusOK,
			fmt.Sprintf(`{"status":"recovering","contract_version":"%s","service_instance_id":"instance-1","release_version":"0.1.0-dev"}`, protocol.ContractVersion))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, request *http.Request) {
		statusHits.Add(1)
		writeResponse(w, http.StatusOK, `{"can_start":true}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := newTestClient(t, server.URL)
	instance, err := client.Discover(context.Background())
	if instance != nil {
		t.Fatal("unready health produced an instance")
	}
	clientErr := requireClientError(t, err)
	if clientErr.Code != ErrorResponse {
		t.Fatalf("code = %q, want %q", clientErr.Code, ErrorResponse)
	}
	if statusHits.Load() != 0 {
		t.Fatal("status was requested despite unready health")
	}
}

func TestMalformedResponsesAreInvalid(t *testing.T) {
	bodies := []string{
		`not json`,
		`{"can_start":true}{"can_start":false}`,
		`null`,
		`[]`,
		``,
	}
	var index atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		current := int(index.Add(1)) - 1
		if current >= len(bodies) {
			current = len(bodies) - 1
		}
		writeResponse(w, http.StatusOK, bodies[current])
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	for caseIndex := range bodies {
		response, err := client.Status(context.Background(), "")
		if response != nil {
			t.Fatalf("case %d: malformed response was accepted", caseIndex)
		}
		clientErr := requireClientError(t, err)
		if clientErr.Code != ErrorResponse {
			t.Fatalf("case %d: code = %q, want %q", caseIndex, clientErr.Code, ErrorResponse)
		}
		if clientErr.HTTPStatus != http.StatusOK {
			t.Fatalf("case %d: HTTPStatus = %d, want 200", caseIndex, clientErr.HTTPStatus)
		}
	}
}

func TestNilOrUnencodableRequestsDoNotSend(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		writeResponse(w, http.StatusOK, `{}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	ctx := context.Background()
	calls := []struct {
		name             string
		invoke           func() (*Response, error)
		retainsOperation bool
	}{
		{"observe-nil", func() (*Response, error) { return client.Observe(ctx, nil) }, true},
		{"snap-nil", func() (*Response, error) { return client.Snap(ctx, nil) }, true},
		{"snap-empty", func() (*Response, error) { return client.Snap(ctx, &protocol.SnapRequest{}) }, true},
		{"act-nil", func() (*Response, error) { return client.Act(ctx, nil) }, true},
		{"app-nil", func() (*Response, error) { return client.App(ctx, nil) }, true},
		{"notify-nil", func() (*Response, error) { return client.Notify(ctx, nil) }, true},
		{"shell-nil", func() (*Response, error) { return client.Shell(ctx, nil) }, true},
		{"inspect-nil", func() (*Response, error) { return client.Inspect(ctx, nil) }, false},
		{"diagnose-nil", func() (*Response, error) { return client.Diagnose(ctx, nil) }, false},
		{"end-empty", func() (*Response, error) { return client.End(ctx, "") }, true},
		{"keepalive-empty", func() (*Response, error) { return client.Keepalive(ctx, "") }, true},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			response, err := call.invoke()
			if response != nil {
				t.Fatal("caller-side rejection produced a response")
			}
			clientErr := requireClientError(t, err)
			if clientErr.Code != protocol.CodeInvalidRequest {
				t.Fatalf("code = %q, want %q", clientErr.Code, protocol.CodeInvalidRequest)
			}
			if clientErr.Transport {
				t.Fatal("caller-side rejection was reported as transport")
			}
			if call.retainsOperation {
				if !validOperationID(clientErr.OperationID) {
					t.Fatal("work tool did not retain its preallocated operation ID")
				}
			} else if clientErr.OperationID != "" {
				t.Fatal("read-only tool reported an operation ID")
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("caller-side rejections reached the service %d times", hits.Load())
	}
}

func TestResponseReadFailureIsTransport(t *testing.T) {
	var attempts atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        make(http.Header),
			Body:          failingReadCloser{},
			ContentLength: -1,
		}, nil
	})}
	client, err := New(Config{
		BaseURL:    "http://127.0.0.1:1",
		AgentToken: testToken,
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	response, err := client.Start(context.Background(), WithOperationID("Preadfail"))
	if response != nil {
		t.Fatal("read failure produced a response")
	}
	clientErr := requireClientError(t, err)
	if !clientErr.Transport || clientErr.Code != ErrorTransport {
		t.Fatal("read failure was not reported as a transport error")
	}
	if clientErr.HTTPStatus != 0 {
		t.Fatalf("HTTPStatus = %d, want 0", clientErr.HTTPStatus)
	}
	if clientErr.OperationID != "Preadfail" {
		t.Fatal("read failure lost the operation ID")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts.Load())
	}
	if strings.Contains(clientErr.Error(), "read failure with non-identifying detail") {
		t.Fatal("error text exposed the raw read error")
	}
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failure with non-identifying detail")
}

func (failingReadCloser) Close() error { return nil }

func TestNewValidatesConfig(t *testing.T) {
	client, err := New(Config{BaseURL: "http://example.com/", AgentToken: testToken})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if client.baseURL != "http://example.com" {
		t.Fatalf("baseURL = %q, want trailing slash trimmed", client.baseURL)
	}
	if client.timeout != defaultTimeout {
		t.Fatalf("timeout = %s, want default %s", client.timeout, defaultTimeout)
	}
	if client.httpClient.CheckRedirect == nil {
		t.Fatal("default client does not block redirects")
	}
	if err := client.httpClient.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatal("default client does not use ErrUseLastResponse")
	}

	pathClient, err := New(Config{BaseURL: "http://example.com/base/", AgentToken: testToken})
	if err != nil {
		t.Fatalf("New() path error: %v", err)
	}
	if pathClient.baseURL != "http://example.com/base" {
		t.Fatalf("baseURL = %q, want trimmed base path", pathClient.baseURL)
	}

	timeoutClient, err := New(Config{
		BaseURL:    "http://example.com",
		AgentToken: testToken,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() timeout error: %v", err)
	}
	if timeoutClient.timeout != 5*time.Second {
		t.Fatalf("timeout = %s, want 5s", timeoutClient.timeout)
	}

	invalid := []Config{
		{BaseURL: "", AgentToken: testToken},
		{BaseURL: "example.com", AgentToken: testToken},
		{BaseURL: "ftp://example.com", AgentToken: testToken},
		{BaseURL: "http://", AgentToken: testToken},
		{BaseURL: "http:///path", AgentToken: testToken},
		{BaseURL: "http://user:pass@example.com", AgentToken: testToken},
		{BaseURL: "http://example.com?query=1", AgentToken: testToken},
		{BaseURL: "http://example.com?", AgentToken: testToken},
		{BaseURL: "http://example.com#fragment", AgentToken: testToken},
		{BaseURL: "http://example.com#", AgentToken: testToken},
	}
	for index, config := range invalid {
		if _, err := New(config); err == nil {
			t.Fatalf("case %d: New accepted an invalid configuration", index)
		}
	}
	if _, err := New(Config{BaseURL: "http://example.com"}); err == nil {
		t.Fatal("New accepted an empty agent token")
	}
}

func TestCallerHTTPClientIsNotMutated(t *testing.T) {
	shared := &http.Client{}
	if _, err := New(Config{
		BaseURL:    "http://example.com",
		AgentToken: testToken,
		HTTPClient: shared,
	}); err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if shared.CheckRedirect != nil {
		t.Fatal("New mutated the caller-supplied HTTP client")
	}
}
