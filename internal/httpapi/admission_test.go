package httpapi

import (
	stdjson "encoding/json"
	jsonv2 "encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// routeAdmissionBase is one minimally valid body per ordinary /api route. Each
// mutation below must be rejected by the shared strict decoder before the
// engine (nil in this test server) is reached.
var routeAdmissionBase = []struct {
	route    string
	firstKey string
	body     map[string]any
}{
	{"start", "", map[string]any{}},
	{"status", "reference", map[string]any{"reference": "O1"}},
	{"observe", "reference", map[string]any{"reference": "O1"}},
	{"wait_for", "reference", map[string]any{"reference": "O1", "timeout": 1}},
	{"act", "observation_id", map[string]any{
		"observation_id": "O1",
		"operation":      map[string]any{"type": "key", "key": "BACK"},
	}},
	{"end", "reference", map[string]any{"reference": "O1"}},
	{"inspect", "observation_id", map[string]any{"observation_id": "O1"}},
	{"keepalive", "reference", map[string]any{"reference": "O1"}},
	{"diagnose", "", map[string]any{}},
	{"notify", "text", map[string]any{"text": "message"}},
	{"app", "kind", map[string]any{"kind": "find"}},
	{"shell", "reference", map[string]any{"reference": "O1", "command": "echo hi"}},
}

func encodeAdmissionBody(t *testing.T, body map[string]any) string {
	t.Helper()
	encoded, err := jsonv2.Marshal(body)
	if err != nil {
		t.Fatalf("encode admission fixture: %v", err)
	}
	return string(encoded)
}

func admissionMutations(t *testing.T, body map[string]any, first string) []struct {
	name string
	body string
} {
	t.Helper()
	encoded := encodeAdmissionBody(t, body)
	var mutations []struct {
		name string
		body string
	}
	add := func(name, value string) {
		mutations = append(mutations, struct {
			name string
			body string
		}{name: name, body: value})
	}

	unknown := cloneAdmissionBody(body)
	unknown["__unknown__"] = true
	add("unknown-member", encodeAdmissionBody(t, unknown))

	add("trailing-second-value", encoded+encoded)

	if first == "" {
		caseVariant := map[string]any{"Start": map[string]any{}}
		add("case-variant", encodeAdmissionBody(t, caseVariant))
		return mutations
	}

	duplicate := strings.TrimSuffix(encoded, "}")
	value, err := jsonv2.Marshal(body[first])
	if err != nil {
		t.Fatalf("encode duplicate value: %v", err)
	}
	duplicate += `,"` + first + `":` + string(value) + "}"
	add("duplicate-member", duplicate)

	caseName := strings.ToUpper(first[:1]) + first[1:]
	caseVariant := cloneAdmissionBody(body)
	caseVariant[caseName] = body[first]
	add("case-variant", encodeAdmissionBody(t, caseVariant))

	typeMixing := cloneAdmissionBody(body)
	typeMixing[first] = 1
	add("type-mixing", encodeAdmissionBody(t, typeMixing))

	nullValue := cloneAdmissionBody(body)
	nullValue[first] = nil
	add("explicit-null", encodeAdmissionBody(t, nullValue))

	return mutations
}

func cloneAdmissionBody(body map[string]any) map[string]any {
	clone := make(map[string]any, len(body)+1)
	for key, value := range body {
		clone[key] = value
	}
	return clone
}

// serveAdmissionRequest reports whether the request reached the (nil) engine.
// A malformed body must be fully rejected by the HTTP boundary, so the absence
// of a panic is part of the proof.
func serveAdmissionRequest(t *testing.T, server *Server, route, body string) (admitted bool, recorderStatusCode int, result *protocol.Result) {
	t.Helper()
	defer func() {
		if recover() != nil {
			// A nil engine panic means the malformed body survived admission.
			admitted = true
		}
	}()
	recorder := doAppRequest(t, server, http.MethodPost, "/api/"+route, body, true)
	return admitted, recorder.Code, decodeAppResult(t, recorder)
}

// TestEveryAPIRouteRejectsStrictAdmissionViolations proves that the route
// table has no lenient ingress path: unknown members, duplicate keys, case
// variants, a trailing second JSON value, type mixing and explicit null on a
// non-nullable member all stop at the shared decoder.
func TestEveryAPIRouteRejectsStrictAdmissionViolations(t *testing.T) {
	for _, route := range routeAdmissionBase {
		t.Run(route.route, func(t *testing.T) {
			server := newAppTestServer()
			for _, mutation := range admissionMutations(t, route.body, route.firstKey) {
				t.Run(mutation.name, func(t *testing.T) {
					reachedEngine, status, result := serveAdmissionRequest(t, server, route.route, mutation.body)
					if reachedEngine {
						t.Fatal("strict admission reached the engine")
					}
					if status != http.StatusUnprocessableEntity {
						t.Fatalf("status = %d, want 422", status)
					}
					if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest {
						t.Fatalf("error = %+v, want invalid_request", result.Error)
					}
					if result.Outcome != "" || result.Observation != nil ||
						result.Session != nil || result.App != nil || result.Shell != nil {
						t.Fatalf("admission rejection carried business facts: %+v", result)
					}
				})
			}
		})
	}
}

// TestAdmissionMutationFixturesRemainJSON guards the byte-level duplicate
// fixture above: if the base encoder ever changes shape, the test should fail
// as a fixture error instead of silently testing a different mutation.
func TestAdmissionMutationFixturesRemainJSON(t *testing.T) {
	for _, route := range routeAdmissionBase {
		for _, mutation := range admissionMutations(t, route.body, route.firstKey) {
			if !stdjson.Valid([]byte(mutation.body)) && mutation.name != "trailing-second-value" {
				t.Fatalf("%s/%s is not JSON: %s", route.route, mutation.name, mutation.body)
			}
		}
	}
}
