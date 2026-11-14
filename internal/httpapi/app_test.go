package httpapi

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const appTestToken = "fake-agent-token-for-tests"

// newAppTestServer builds the real route table without an engine. Every case
// below is rejected by the HTTP boundary before the engine would be called, so
// these admission tests stay independent of engine construction.
func newAppTestServer() *Server {
	return New(Config{
		AgentName:         "local-agent",
		AgentToken:        appTestToken,
		ControlToken:      "fake-control-token-for-tests",
		ServiceInstanceID: "instance-test",
	}, nil, nil)
}

func doAppRequest(t *testing.T, server *Server, method, path, body string, authorize bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorize {
		request.Header.Set("Authorization", "Bearer "+appTestToken)
	}
	request.Header.Set(ContractHeader, protocol.ContractVersion)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func decodeAppResult(t *testing.T, recorder *httptest.ResponseRecorder) *protocol.Result {
	t.Helper()
	var result protocol.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("response is not a structured result: %v", err)
	}
	return &result
}

// TestAPIErrorNestedDetailsWire pins the server-side shape of the optional tap
// guidance. safe_point remains an object with explicit zero coordinates, and
// optional keys that were not proven are absent rather than null or empty.
func TestAPIErrorNestedDetailsWire(t *testing.T) {
	apiErr := protocol.NewErrorOutcome(http.StatusUnprocessableEntity,
		protocol.CodeNodeUnsupported, "目标节点不支持点击", "not_executed")
	apiErr.WithDetail("nearest_actionable_ref", "n516")
	apiErr.WithDetail("safe_point", map[string]any{"x": 0, "y": 0})
	apiErr.WithDetail("task_ids", []int{0, 42})

	recorder := httptest.NewRecorder()
	writeAPIError(recorder, apiErr)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	result := decodeAppResult(t, recorder)
	if result.Error == nil || result.Error.Code != protocol.CodeNodeUnsupported {
		t.Fatalf("error = %+v, want unsupported_node_action", result.Error)
	}
	safePoint, ok := result.Error.Details["safe_point"].(map[string]any)
	if !ok {
		t.Fatalf("safe_point decoded as %T, want nested object", result.Error.Details["safe_point"])
	}
	if safePoint["x"] != float64(0) || safePoint["y"] != float64(0) {
		t.Fatalf("zero safe_point coordinates changed: %#v", safePoint)
	}
	for _, optional := range []string{"safe_carrier_ref", "occluding_ref"} {
		if _, present := result.Error.Details[optional]; present {
			t.Fatalf("unproven optional key %q was emitted", optional)
		}
		if strings.Contains(recorder.Body.String(), optional) {
			t.Fatalf("unproven optional key %q appears on the wire", optional)
		}
	}
	if ids, ok := result.Error.Details["task_ids"].([]any); !ok || len(ids) != 2 ||
		ids[0] != float64(0) || ids[1] != float64(42) {
		t.Fatalf("task_ids = %#v, want [0,42]", result.Error.Details["task_ids"])
	}
}

func TestAppRouteRejectsBodyOverLimit(t *testing.T) {
	raw := `{"kind":"find"}`
	body := raw + strings.Repeat(" ", 8*1024-len(raw)+1)
	recorder := doAppRequest(t, newAppTestServer(), http.MethodPost, "/api/app", body, true)
	result := decodeAppResult(t, recorder)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("error = %+v, want invalid_request", result.Error)
	}
	if result.Error.Message != "请求体超过大小上限" {
		t.Fatalf("message = %q, want the size-limit rejection", result.Error.Message)
	}
	if result.App != nil || result.OperationID != "" {
		t.Fatal("admission rejection carried app facts or an operation ID")
	}
}

func TestAppRouteAdmitsBodyAtLimitBeforeStrictDecode(t *testing.T) {
	// The size gate accepts exactly 8 KiB; the unknown member is then rejected
	// by the strict decoder instead of by readBody.
	raw := `{"kind":"find","bogus":true}`
	body := raw + strings.Repeat(" ", 8*1024-len(raw))
	if len(body) != 8*1024 {
		t.Fatalf("fixture length = %d, want 8192", len(body))
	}
	recorder := doAppRequest(t, newAppTestServer(), http.MethodPost, "/api/app", body, true)
	result := decodeAppResult(t, recorder)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest ||
		result.Error.Message != "请求参数不符合工具契约" {
		t.Fatalf("error = %+v, want the strict-decode rejection", result.Error)
	}
}

func TestAppRouteStrictAdmission(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"unknown-member", `{"kind":"find","bogus":true}`},
		{"cross-kind-member", `{"kind":"find","reference":"O1"}`},
		{"explicit-null", `{"kind":"find","query":null}`},
		{"trailing-value", `{"kind":"find"}{"kind":"find"}`},
		{"duplicate-member", `{"kind":"find","query":"a","query":"b"}`},
		{"case-variant", `{"Kind":"find"}`},
		{"missing-kind", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := doAppRequest(t, newAppTestServer(), http.MethodPost, "/api/app", tc.body, true)
			result := decodeAppResult(t, recorder)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", recorder.Code)
			}
			if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest {
				t.Fatalf("error = %+v, want invalid_request", result.Error)
			}
			if result.App != nil {
				t.Fatal("strict rejection published an app view")
			}
		})
	}
}

func TestAppRouteKeepsAgentAdmission(t *testing.T) {
	server := newAppTestServer()

	if recorder := doAppRequest(t, server, http.MethodGet, "/api/app", `{}`, true); recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", recorder.Code)
	}
	if recorder := doAppRequest(t, server, http.MethodPost, "/api/app", `{"kind":"find"}`, false); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", recorder.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/app", strings.NewReader(`{"kind":"find"}`))
	request.Header.Set("Authorization", "Bearer "+appTestToken)
	request.Header.Set(ContractHeader, "0")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("contract mismatch status = %d, want 409", recorder.Code)
	}
}

func TestAppDeliveryReserveFollowsKind(t *testing.T) {
	cases := []struct {
		kind string
		want int64
	}{
		{protocol.AppKindFind, deliveryReserveApps},
		{protocol.AppKindList, deliveryReserveApps},
		{protocol.AppKindOpen, deliveryReserveTree},
		{protocol.AppKindClaim, deliveryReserveTree},
		{protocol.AppKindHandoff, deliveryReserveTree},
		{protocol.AppKindClose, deliveryReserveTree},
		{protocol.AppKindKill, deliveryReserveTree},
	}
	if len(cases) != len(protocol.AppKinds) {
		t.Fatalf("reserve cases = %d, want every one of the %d app kinds", len(cases), len(protocol.AppKinds))
	}
	for _, tc := range cases {
		if got := appDeliveryReserve(&protocol.AppRequest{Kind: tc.kind}); got != tc.want {
			t.Fatalf("%s reserve = %d, want %d", tc.kind, got, tc.want)
		}
	}
}

func TestLegacyAppRoutesAreRemoved(t *testing.T) {
	server := newAppTestServer()
	for _, path := range []string{"/api/apps", "/api/open_app"} {
		recorder := doAppRequest(t, server, http.MethodPost, path, `{}`, true)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, recorder.Code)
		}
	}
}
