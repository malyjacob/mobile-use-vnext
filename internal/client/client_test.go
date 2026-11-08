package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func assertGeneratedOperation(t *testing.T, operation string) {
	t.Helper()
	if len(operation) != 25 || operation[0] != 'P' {
		t.Fatalf("generated operation ID has the wrong shape")
	}
	for index := 1; index < len(operation); index++ {
		character := operation[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			t.Fatalf("generated operation ID is not lowercase hexadecimal")
		}
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
	for index := 0; index < count; index++ {
		operation, err := NewOperationID()
		if err != nil {
			t.Fatalf("NewOperationID() error: %v", err)
		}
		assertGeneratedOperation(t, operation)
		if !validOperationID(operation) {
			t.Fatalf("case %d: NewOperationID returned an invalid operation ID", index)
		}
		if _, duplicate := seen[operation]; duplicate {
			t.Fatalf("case %d: NewOperationID returned a duplicate", index)
		}
		seen[operation] = struct{}{}
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
			`{"operation_id":"Pservice409","outcome":"not_executed","error":{"code":"session_exists","message":"会话已存在"},"next":"status"}`)
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
			`{"operation_id":"Pfailed503","outcome":"executed","observation":{"status":"failed","id":"O1","origin":"Pfailed503","missing":["tree"]},"next":"observe"}`)
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
		ObservationID: "O2",
		HasNode:       true,
		Node:          "n7",
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
	if inspectCapture.body != `{"observation_id":"O2","node":"n7"}` {
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
			`{"can_start":false,"blocking_code":"cleanup_unconfirmed","start_operation_id":"P1","session":{"origin":"P1","state":"ended","mode":"virtual","agent":"local-agent"},"cleanup":{"state":"unconfirmed","execution":"confirmed","tasks":"confirmed","recents":"not_applicable","display":"unconfirmed"}}`)
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
	if instance.BlockingCode != "cleanup_unconfirmed" {
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
		{"act-nil", func() (*Response, error) { return client.Act(ctx, nil) }, true},
		{"apps-nil", func() (*Response, error) { return client.Apps(ctx, nil) }, true},
		{"open-app-nil", func() (*Response, error) { return client.OpenApp(ctx, nil) }, true},
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
