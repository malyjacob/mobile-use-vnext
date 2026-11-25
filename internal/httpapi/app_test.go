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

func requireWireCandidate(t *testing.T, raw any, wantRef, wantRole string,
	wantActions []string, wantBounds []int, wantRelation string, wantExecutable bool) {
	t.Helper()
	candidate, ok := raw.(map[string]any)
	if !ok || len(candidate) != 6 {
		t.Fatalf("candidate = %#v, want the six fixed fields", raw)
	}
	if candidate["ref"] != wantRef || candidate["role"] != wantRole ||
		candidate["relation"] != wantRelation {
		t.Fatalf("candidate identity = %#v", candidate)
	}
	actions, ok := candidate["actions"].([]any)
	if !ok || actions == nil || len(actions) != len(wantActions) {
		t.Fatalf("candidate actions = %#v, want %v", candidate["actions"], wantActions)
	}
	for index, want := range wantActions {
		if actions[index] != want {
			t.Fatalf("candidate actions = %#v, want %v", actions, wantActions)
		}
	}
	bounds, ok := candidate["bounds"].([]any)
	if !ok || bounds == nil || len(bounds) != len(wantBounds) {
		t.Fatalf("candidate bounds = %#v, want %v", candidate["bounds"], wantBounds)
	}
	for index, want := range wantBounds {
		if bounds[index] != float64(want) {
			t.Fatalf("candidate bounds = %#v, want %v", bounds, wantBounds)
		}
	}
	executable, ok := candidate["executable"].(bool)
	if !ok || executable != wantExecutable {
		t.Fatalf("candidate executable = %#v, want %t", candidate["executable"], wantExecutable)
	}
}

// TestAPIErrorCandidatesWire pins the server-side static rejection shape.
// Every candidate field survives the APIError.Public ownership copy, bounds
// remain exact integer-valued numbers on the wire, empty actions stay [], and
// blocking_refs is omitted when no blocking ref was proven.
func TestAPIErrorCandidatesWire(t *testing.T) {
	cases := []struct {
		name         string
		code         string
		message      string
		reason       string
		scopeRef     string
		candidates   []map[string]any
		blockingRefs []string
	}{
		{
			name:     "no safe point",
			code:     protocol.CodeNoSafePoint,
			message:  "存在多个共位目标，无法证明点击落点",
			reason:   "colocated_ambiguous",
			scopeRef: "n5233",
			candidates: []map[string]any{
				{
					"ref":        "n5232",
					"role":       "button",
					"actions":    []string{"tap"},
					"bounds":     []int{915, 1675, 1028, 1787},
					"relation":   "overlapping",
					"executable": true,
				},
				{
					"ref":        "n5231",
					"role":       "group",
					"actions":    []string{},
					"bounds":     []int{0, 1600, 1080, 1900},
					"relation":   "child",
					"executable": false,
				},
			},
			blockingRefs: []string{"n5230", "n5253"},
		},
		{
			name:     "no safe trajectory",
			code:     protocol.CodeNoSafeTrajectory,
			message:  "滚动容器在三条轴线上均无安全轨迹",
			reason:   "trajectory_blocked",
			scopeRef: "n56",
			candidates: []map[string]any{{
				"ref":        "n66",
				"role":       "scroll",
				"actions":    []string{"swipe"},
				"bounds":     []int{0, 363, 1080, 2265},
				"relation":   "child",
				"executable": true,
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := protocol.NewErrorOutcome(protocol.Status(tc.code), tc.code, tc.message, "not_executed").
				WithDetail("reason", tc.reason).
				WithDetail("scope", "target").
				WithDetail("scope_ref", tc.scopeRef).
				WithDetail("candidates", tc.candidates)
			if tc.blockingRefs != nil {
				apiErr.WithDetail("blocking_refs", tc.blockingRefs)
			}

			recorder := httptest.NewRecorder()
			writeAPIError(recorder, apiErr)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", recorder.Code)
			}
			result := decodeAppResult(t, recorder)
			if result.Outcome != "not_executed" || result.Error == nil || result.Error.Code != tc.code {
				t.Fatalf("result = %+v, want %s/not_executed", result, tc.code)
			}
			details := result.Error.Details
			if details["reason"] != tc.reason || details["scope"] != "target" ||
				details["scope_ref"] != tc.scopeRef {
				t.Fatalf("details = %#v", details)
			}
			candidates, ok := details["candidates"].([]any)
			if !ok || len(candidates) != len(tc.candidates) {
				t.Fatalf("candidates = %#v", details["candidates"])
			}
			for index, candidate := range candidates {
				want := tc.candidates[index]
				requireWireCandidate(t, candidate, want["ref"].(string), want["role"].(string),
					want["actions"].([]string), want["bounds"].([]int),
					want["relation"].(string), want["executable"].(bool))
			}
			if strings.Contains(recorder.Body.String(), `"actions":[]`) != (tc.name == "no safe point") {
				t.Fatalf("empty actions encoding differs: %s", recorder.Body.String())
			}
			blockingRefs, present := details["blocking_refs"]
			if tc.blockingRefs == nil {
				if present || strings.Contains(recorder.Body.String(), `"blocking_refs"`) {
					t.Fatalf("absent blocking_refs was emitted: %#v", blockingRefs)
				}
				return
			}
			refs, ok := blockingRefs.([]any)
			if !ok || len(refs) != len(tc.blockingRefs) {
				t.Fatalf("blocking_refs = %#v", blockingRefs)
			}
			for index, want := range tc.blockingRefs {
				if refs[index] != want {
					t.Fatalf("blocking_refs = %#v, want %v", refs, tc.blockingRefs)
				}
			}
		})
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
		{"nested-unknown-member", `{"kind":"find","target":{"bogus":true}}`},
		{"nested-wrong-type", `{"kind":"find","target":{"task_id":"x"}}`},
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
