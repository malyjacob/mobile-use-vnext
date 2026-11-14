package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func doWaitForRequest(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAppRequest(t, server, http.MethodPost, "/api/wait_for", body, true)
}

// TestWaitForRouteRejectsBodyOverLimit pins the 16 KiB admission bound of the
// new route: the size gate runs before the strict decoder and before any engine
// work.
func TestWaitForRouteRejectsBodyOverLimit(t *testing.T) {
	raw := `{"reference":"O1","timeout":1}`
	body := raw + strings.Repeat(" ", 16*1024-len(raw)+1)
	recorder := doWaitForRequest(t, newAppTestServer(), body)
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
}

// TestWaitForRouteKeepsStrictAdmission proves the route shares the one strict
// decoder: an unknown member, a missing timeout and a contradictory condition
// pair are all refused before the engine is reached.
func TestWaitForRouteKeepsStrictAdmission(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"unknown member", `{"reference":"O1","timeout":1,"wait_ms":10}`},
		{"missing timeout", `{"reference":"O1"}`},
		{"timeout out of range", `{"reference":"O1","timeout":41}`},
		{"stable needs two seconds", `{"reference":"O1","timeout":1,"stable":true}`},
		{"same condition", `{"reference":"O1","timeout":5,"query":"加载","absent_query":"加载"}`},
		{"null image", `{"reference":"O1","timeout":1,"image":null}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := doWaitForRequest(t, newAppTestServer(), testCase.body)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", recorder.Code)
			}
			result := decodeAppResult(t, recorder)
			if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest {
				t.Fatalf("error = %+v", result.Error)
			}
			if result.Wait != nil || result.Observation != nil || result.Outcome != "" {
				t.Fatalf("admission rejection carried business facts: %+v", result)
			}
		})
	}
}

// TestWaitForRouteReservesDeliverySlot proves the route reserves delivery
// capacity before the engine is reached: a wait holds one slot for its whole
// duration, and a third concurrent delivery is refused with the controlled 503
// instead of queueing.
func TestWaitForRouteReservesDeliverySlot(t *testing.T) {
	server := newAppTestServer()
	first, ok := server.delivery.acquire(deliveryReserveTree)
	if !ok {
		t.Fatal("first reservation refused")
	}
	defer first()
	second, ok := server.delivery.acquire(deliveryReserveTree)
	if !ok {
		t.Fatal("second reservation refused")
	}
	defer second()

	recorder := doWaitForRequest(t, server, `{"reference":"O1","timeout":1}`)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	result := decodeAppResult(t, recorder)
	if result.Error == nil || result.Error.Code != protocol.CodeDeliveryUnavailable {
		t.Fatalf("error = %+v, want delivery_unavailable", result.Error)
	}
}

// TestWaitForRouteRequiresExactContract proves the shared ordinary-contract
// header gate applies to the new route like every other tool.
func TestWaitForRouteRequiresExactContract(t *testing.T) {
	server := newAppTestServer()
	request := httptest.NewRequest(http.MethodPost, "/api/wait_for",
		strings.NewReader(`{"reference":"O1","timeout":1}`))
	request.Header.Set("Authorization", "Bearer "+appTestToken)
	request.Header.Set(ContractHeader, "11")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	result := decodeAppResult(t, recorder)
	if result.Error == nil || result.Error.Code != protocol.CodeContractMismatch {
		t.Fatalf("error = %+v, want contract_mismatch", result.Error)
	}
}

// TestWaitForRouteRejectsGet keeps the POST-only route contract.
func TestWaitForRouteRejectsGet(t *testing.T) {
	recorder := doAppRequest(t, newAppTestServer(), http.MethodGet, "/api/wait_for", "", true)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}
